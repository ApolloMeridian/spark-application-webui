package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/smithy-go"
	"google.golang.org/api/googleapi"
)

type Server struct {
	Config        Config
	Store         *Store
	Key           []byte
	Providers     map[string]Provider
	Logger        *slog.Logger
	slots         chan struct{}
	requestsSlots chan struct{}
	mu            sync.Mutex
	rates         map[string]rate
	Requests      atomic.Uint64
	Denied        atomic.Uint64
}
type rate struct {
	Time  time.Time
	Count int
}
type principalKey struct{}
type principal struct {
	User   Identity
	Grants []Grant
}

func NewServer(c Config, store *Store, key []byte, providers map[string]Provider, logger *slog.Logger) *Server {
	return &Server{Config: c, Store: store, Key: key, Providers: providers, Logger: logger, slots: make(chan struct{}, c.Concurrent), requestsSlots: make(chan struct{}, c.Concurrent*4), rates: map[string]rate{}}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if s.Store.Pool.Ping(ctx) != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "spark_storage_requests_total %d\nspark_storage_denied_total %d\nspark_storage_active_transfers %d\n", s.Requests.Load(), s.Denied.Load(), len(s.slots))
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/connections", s.connections)
	api.HandleFunc("GET /v1/objects", s.list)
	api.HandleFunc("GET /v1/download", s.download)
	api.HandleFunc("GET /v1/text", s.text)
	api.HandleFunc("PUT /v1/text", s.saveText)
	api.HandleFunc("POST /v1/text", s.saveText)
	api.HandleFunc("DELETE /v1/objects", s.delete)
	api.HandleFunc("POST /v1/copy", s.copy)
	api.HandleFunc("POST /v1/uploads", s.startUpload)
	api.HandleFunc("GET /v1/uploads", s.uploadStatus)
	api.HandleFunc("PUT /v1/uploads/part", s.uploadPart)
	api.HandleFunc("POST /v1/uploads/complete", s.completeUpload)
	api.HandleFunc("DELETE /v1/uploads", s.abortUpload)
	api.HandleFunc("GET /v1/policies", s.policies)
	api.HandleFunc("POST /v1/policies", s.savePolicy)
	api.HandleFunc("DELETE /v1/policies/{id}", s.deletePolicy)
	api.HandleFunc("GET /v1/operations", func(w http.ResponseWriter, r *http.Request) {
		items, e := s.Store.Operations(r.Context(), who(r).User)
		if e == nil {
			visible := []Operation{}
			u := who(r)
			for _, x := range items {
				if Allows(u.User, u.Grants, x.Connection, x.Bucket, x.Key, "list") || Allows(u.User, u.Grants, x.Connection, x.Bucket, x.Key, "read") {
					visible = append(visible, x)
				}
			}
			items = visible
		}
		s.respond(w, items, e)
	})
	mux.Handle("/v1/", s.authenticate(api))
	return mux
}
func who(r *http.Request) principal { return r.Context().Value(principalKey{}).(principal) }
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.Config.Timeout())
		defer cancel()
		r = r.WithContext(ctx)
		select {
		case s.requestsSlots <- struct{}{}:
			defer func() { <-s.requestsSlots }()
		default:
			s.respond(w, nil, fail(429, "request_limit"))
			return
		}
		s.Requests.Add(1)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		claims, e := Verify(s.Key, r)
		if e != nil {
			s.respond(w, nil, e)
			return
		}
		user, e := s.Store.Identity(r.Context(), claims.ID, claims.Tenant)
		if e != nil {
			s.respond(w, nil, e)
			return
		}
		s.mu.Lock()
		now := time.Now()
		entry := s.rates[user.ID]
		if now.Sub(entry.Time) >= time.Minute {
			entry = rate{Time: now}
		}
		entry.Count++
		s.rates[user.ID] = entry
		allowed := entry.Count <= s.Config.RequestsPerMinute
		if len(s.rates) > 10000 {
			for k, v := range s.rates {
				if now.Sub(v.Time) > 2*time.Minute {
					delete(s.rates, k)
				}
			}
		}
		s.mu.Unlock()
		if !allowed {
			s.respond(w, nil, fail(429, "rate_limit"))
			return
		}
		grants, e := s.Store.Grants(r.Context(), user, false)
		if e != nil {
			s.respond(w, nil, e)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey{}, principal{user, grants})))
	})
}
func (s *Server) transfer(w http.ResponseWriter) (func(), bool) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, true
	default:
		s.respond(w, nil, fail(429, "transfer_limit"))
		return nil, false
	}
}
func (s *Server) connection(id, b string) (Connection, Provider, error) {
	for _, c := range s.Config.Connections {
		if c.ID == id && contains(c.Buckets, b) {
			return c, s.Providers[id], nil
		}
	}
	return Connection{}, nil, fail(403, "access_denied")
}
func (s *Server) authorize(r *http.Request, c, b, k, a string) (Provider, error) {
	_, p, e := s.connection(c, b)
	if e != nil {
		return nil, e
	}
	u := who(r)
	if !Allows(u.User, u.Grants, c, b, k, a) {
		return nil, fail(403, "access_denied")
	}
	return p, nil
}
func target(r *http.Request) (string, string, string) {
	q := r.URL.Query()
	return q.Get("connection"), q.Get("bucket"), q.Get("key")
}
func (s *Server) respond(w http.ResponseWriter, x any, e error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e != nil {
		status, code := 500, "storage_unavailable"
		var f *Failure
		var api smithy.APIError
		var ge *googleapi.Error
		switch {
		case errors.As(e, &f):
			status, code = f.Status, f.Code
		case errors.Is(e, gcs.ErrObjectNotExist):
			status, code = 404, "object_not_found"
		case errors.As(e, &ge):
			if ge.Code == 404 {
				status, code = 404, "object_not_found"
			} else if ge.Code == 412 {
				status, code = 409, "version_conflict"
			}
		case errors.As(e, &api):
			switch api.ErrorCode() {
			case "NoSuchKey", "NotFound", "NoSuchUpload":
				status, code = 404, "object_not_found"
			case "PreconditionFailed", "ConditionalRequestConflict":
				status, code = 409, "version_conflict"
			case "AccessDenied":
				status, code = 502, "provider_access_denied"
			}
		}
		if status < 400 || status > 599 {
			status = 502
		}
		if status == 401 || status == 403 {
			s.Denied.Add(1)
		}
		if status >= 500 {
			s.Logger.Warn("storage request failed", "code", code)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": code})
		return
	}
	_ = json.NewEncoder(w).Encode(x)
}
func decode(w http.ResponseWriter, r *http.Request, x any, limit int64) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if e := d.Decode(x); e != nil {
		return fail(400, "invalid_request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fail(400, "invalid_request")
	}
	return nil
}
func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	type root struct {
		Bucket  string   `json:"bucket"`
		Prefix  string   `json:"prefix"`
		Actions []string `json:"actions"`
	}
	type view struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Roots    []root `json:"roots"`
		Editable bool   `json:"editable"`
	}
	out := []view{}
	u := who(r)
	for _, c := range s.Config.Connections {
		v := view{c.ID, c.Name, c.Provider, []root{}, c.Provider == "gcs" || c.S3.ConditionalWrites}
		for _, g := range u.Grants {
			if g.Connection == c.ID && contains(c.Buckets, g.Bucket) {
				actions := []string{}
				for _, a := range g.Actions {
					if Allows(u.User, []Grant{g}, c.ID, g.Bucket, g.Prefix, a) {
						actions = append(actions, a)
					}
				}
				if len(actions) > 0 {
					v.Roots = append(v.Roots, root{g.Bucket, g.Prefix, actions})
				}
			}
		}
		if len(v.Roots) > 0 {
			out = append(out, v)
		}
	}
	s.respond(w, map[string]any{"connections": out, "textMaxBytes": s.Config.TextMaxBytes, "maxUploadBytes": s.Config.MaxUpload, "maxDownloadBytes": s.Config.MaxDownload, "partSizeBytes": s.Config.PartSize}, nil)
}
func policyHash(grants []Grant) string {
	raw, _ := json.Marshal(grants)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	if !ValidPrefix(k) {
		s.respond(w, nil, fail(400, "invalid_prefix"))
		return
	}
	p, e := s.authorize(r, c, b, k, "list")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	u := who(r)
	cursor := ""
	if value := r.URL.Query().Get("cursor"); value != "" {
		ticket, e := unseal(s.Key, value, u.User, "page")
		if e != nil || ticket.Connection != c || ticket.Bucket != b || ticket.Key != k || ticket.Policy != policyHash(u.Grants) {
			s.respond(w, nil, fail(400, "invalid_cursor"))
			return
		}
		cursor = ticket.Cursor
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		n = 100
	}
	if n > 500 {
		n = 500
	}
	out, e := p.List(r.Context(), b, k, cursor, n)
	if e == nil {
		for _, o := range out.Objects {
			if !ValidKey(o.Key) || !strings.HasPrefix(o.Key, k) {
				s.respond(w, nil, fail(502, "invalid_provider_response"))
				return
			}
		}
		if out.Next != "" {
			out.Next, e = seal(s.Key, Ticket{User: u.User.ID, Tenant: u.User.Tenant, Connection: c, Bucket: b, Key: k, Kind: "page", Cursor: out.Next, Policy: policyHash(u.Grants), Expires: time.Now().Add(15 * time.Minute).Unix()})
		}
	}
	s.respond(w, out, e)
}
func textKey(k string) bool {
	return contains([]string{".txt", ".log", ".json", ".yaml", ".yml", ".csv", ".sql", ".hql", ".py", ".java", ".js", ".ts", ".bat", ".cfg", ".conf", ".properties", ".xml", ".md", ".html", ".svg", ".sh", ".ini", ".toml"}, strings.ToLower(path.Ext(k)))
}
func previewText(reader io.Reader, size, limit int64) ([]byte, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	truncated := size > limit || int64(len(raw)) > limit
	if int64(len(raw)) > limit {
		raw = raw[:limit]
	}
	// Only trim an incomplete final UTF-8 rune at the preview boundary.
	if truncated {
		start := len(raw) - 1
		for start >= 0 && !utf8.RuneStart(raw[start]) {
			start--
		}
		if start >= 0 && !utf8.FullRune(raw[start:]) {
			raw = raw[:start]
		}
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, false, fail(415, "not_text")
	}
	return raw, truncated, nil
}
func (s *Server) text(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	p, e := s.authorize(r, c, b, k, "read")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if !textKey(k) {
		s.respond(w, nil, fail(415, "not_text"))
		return
	}
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	head, e := p.Head(r.Context(), b, k)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	reader, e := p.Open(r.Context(), b, k, head.Version)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer reader.Close()
	raw, truncated, e := previewText(reader, head.Size, s.Config.TextMaxBytes)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	s.respond(w, map[string]any{"content": string(raw), "object": head, "truncated": truncated, "previewBytes": len(raw), "readOnly": truncated}, nil)
}
func (s *Server) mutate(r *http.Request, action, c, b, k, target string, fn func() (any, error)) (any, error) {
	id, e := s.Store.Begin(r.Context(), who(r).User, action, c, b, k, target)
	if e != nil {
		return nil, e
	}
	x, e := fn()
	result := "succeeded"
	if e != nil {
		result = "failed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if auditErr := s.Store.Finish(ctx, id, result); auditErr != nil {
		return nil, fail(503, "operation_audit_pending")
	}
	return x, e
}
func (s *Server) saveText(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	permission, action := "edit", "EDIT"
	creating := r.Method == http.MethodPost
	if creating {
		permission, action = "upload", "CREATE"
	}
	p, e := s.authorize(r, c, b, k, permission)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if !textKey(k) {
		s.respond(w, nil, fail(415, "not_text"))
		return
	}
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	var body struct {
		Content string `json:"content"`
		Version string `json:"version"`
	}
	if e = decode(w, r, &body, s.Config.TextMaxBytes*6+4096); e != nil {
		s.respond(w, nil, e)
		return
	}
	raw := []byte(body.Content)
	if int64(len(raw)) > s.Config.TextMaxBytes {
		s.respond(w, nil, fail(413, "text_too_large"))
		return
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 || (creating && body.Version != "") || (!creating && body.Version == "") {
		s.respond(w, nil, fail(400, "invalid_text_or_version"))
		return
	}
	if !creating {
		head, err := p.Head(r.Context(), b, k)
		if err != nil {
			s.respond(w, nil, err)
			return
		}
		if head.Size > s.Config.TextMaxBytes {
			s.respond(w, nil, fail(413, "text_too_large"))
			return
		}
	}
	x, e := s.mutate(r, action, c, b, k, "", func() (any, error) {
		return p.Put(r.Context(), b, k, bytes.NewReader(raw), int64(len(raw)), body.Version)
	})
	s.respond(w, x, e)
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	p, e := s.authorize(r, c, b, k, "download")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	head, e := p.Head(r.Context(), b, k)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if head.Size > s.Config.MaxDownload {
		s.respond(w, nil, fail(413, "download_too_large"))
		return
	}
	reader, e := p.Open(r.Context(), b, k, head.Version)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer reader.Close()
	id, e := s.Store.Begin(r.Context(), who(r).User, "DOWNLOAD", c, b, k, "")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(k)}))
	w.Header().Set("Content-Length", strconv.FormatInt(head.Size, 10))
	count, e := io.CopyN(w, reader, head.Size)
	if id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result := "succeeded"
		if e != nil || count != head.Size {
			result = "failed"
		}
		if err := s.Store.Finish(ctx, id, result); err != nil {
			s.Logger.Warn("download audit pending", "operation", id)
		}
	}
}
func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	p, e := s.authorize(r, c, b, k, "delete")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		s.respond(w, nil, fail(400, "version_required"))
		return
	}
	x, e := s.mutate(r, "DELETE", c, b, k, "", func() (any, error) { return map[string]bool{"deleted": true}, p.Delete(r.Context(), b, k, version) })
	s.respond(w, x, e)
}
func (s *Server) copy(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	p, e := s.authorize(r, c, b, k, "copy-source")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	var body struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if e = decode(w, r, &body, 4096); e != nil {
		s.respond(w, nil, e)
		return
	}
	if body.Version == "" {
		s.respond(w, nil, fail(400, "version_required"))
		return
	}
	if _, e = s.authorize(r, c, body.Bucket, body.Key, "copy-destination"); e != nil {
		s.respond(w, nil, e)
		return
	}
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	head, e := p.Head(r.Context(), b, k)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if head.Size > s.Config.MaxUpload {
		s.respond(w, nil, fail(413, "copy_too_large"))
		return
	}
	x, e := s.mutate(r, "COPY", c, b, k, body.Bucket+"/"+body.Key, func() (any, error) { return p.Copy(r.Context(), b, k, body.Bucket, body.Key, body.Version) })
	s.respond(w, x, e)
}
func (s *Server) startUpload(w http.ResponseWriter, r *http.Request) {
	c, b, k := target(r)
	p, e := s.authorize(r, c, b, k, "upload")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	var body struct {
		Size    int64  `json:"size"`
		Version string `json:"version"`
	}
	if e = decode(w, r, &body, 4096); e != nil {
		s.respond(w, nil, e)
		return
	}
	if body.Size < 0 || body.Size > s.Config.MaxUpload || body.Size/s.Config.PartSize >= 10000 {
		s.respond(w, nil, fail(413, "upload_too_large"))
		return
	}
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	id, e := s.Store.Begin(r.Context(), who(r).User, "UPLOAD", c, b, k, "")
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if body.Size == 0 {
		out, e := p.Put(r.Context(), b, k, strings.NewReader(""), 0, body.Version)
		result := "succeeded"
		if e != nil {
			result = "failed"
		}
		if ae := s.Store.Finish(r.Context(), id, result); ae != nil {
			e = ae
		}
		s.respond(w, map[string]any{"complete": true, "object": out}, e)
		return
	}
	upload, e := p.Start(r.Context(), b, k, body.Size, body.Version)
	if e != nil {
		_ = s.Store.Finish(r.Context(), id, "failed")
		s.respond(w, nil, e)
		return
	}
	u := who(r).User
	ticket, e := seal(s.Key, Ticket{User: u.ID, Tenant: u.Tenant, Connection: c, Bucket: b, Key: k, Kind: "upload", Operation: id, Upload: upload, Expires: time.Now().Add(24 * time.Hour).Unix()})
	s.respond(w, map[string]any{"ticket": ticket, "offset": 0, "partSizeBytes": s.Config.PartSize}, e)
}
func (s *Server) upload(r *http.Request) (Ticket, Provider, func(), error) {
	t, e := unseal(s.Key, r.Header.Get("X-Storage-Upload"), who(r).User, "upload")
	if e != nil {
		return t, nil, nil, e
	}
	p, e := s.authorize(r, t.Connection, t.Bucket, t.Key, "upload")
	if e != nil {
		return t, nil, nil, e
	}
	release, e := s.Store.Lease(r.Context(), who(r).User, t.Operation)
	return t, p, release, e
}
func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request) {
	t, p, release, e := s.upload(r)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer release()
	u, e := p.Status(r.Context(), t.Bucket, t.Key, t.Upload)
	s.respond(w, map[string]any{"offset": u.Offset, "complete": u.Complete, "partSizeBytes": s.Config.PartSize}, e)
}
func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request) {
	t, p, unlock, e := s.upload(r)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer unlock()
	release, ok := s.transfer(w)
	if !ok {
		return
	}
	defer release()
	u, e := p.Status(r.Context(), t.Bucket, t.Key, t.Upload)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if u.Complete {
		s.respond(w, nil, fail(409, "upload_already_finalized"))
		return
	}
	n := int32(u.Offset/s.Config.PartSize + 1)
	wanted := s.Config.PartSize
	if u.Size-u.Offset < wanted {
		wanted = u.Size - u.Offset
	}
	offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if e != nil || offset != u.Offset || wanted <= 0 {
		s.respond(w, nil, fail(409, "upload_offset_mismatch"))
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, wanted+1))
	if e != nil || int64(len(raw)) != wanted {
		s.respond(w, nil, fail(400, "size_mismatch"))
		return
	}
	u, e = p.Part(r.Context(), t.Bucket, t.Key, u, n, bytes.NewReader(raw), wanted)
	s.respond(w, map[string]any{"offset": u.Offset, "complete": u.Complete}, e)
}
func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request) {
	t, p, unlock, e := s.upload(r)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer unlock()
	out, e := p.Complete(r.Context(), t.Bucket, t.Key, t.Upload)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	if e = s.Store.Finish(r.Context(), t.Operation, "succeeded"); e != nil {
		s.respond(w, nil, fail(503, "operation_audit_pending"))
		return
	}
	s.respond(w, out, nil)
}
func (s *Server) abortUpload(w http.ResponseWriter, r *http.Request) {
	t, p, unlock, e := s.upload(r)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	defer unlock()
	e = p.Abort(r.Context(), t.Bucket, t.Key, t.Upload)
	if e == nil {
		e = s.Store.Finish(r.Context(), t.Operation, "cancelled")
	}
	s.respond(w, map[string]bool{"cancelled": e == nil}, e)
}
func (s *Server) policies(w http.ResponseWriter, r *http.Request) {
	u := who(r)
	if u.User.Role != "admin" {
		s.respond(w, nil, fail(403, "access_denied"))
		return
	}
	all, e := s.Store.Grants(r.Context(), u.User, true)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	out := []Grant{}
	for _, g := range all {
		if s.canManage(r, g) {
			out = append(out, g)
		}
	}
	users, e := s.Store.PolicyUsers(r.Context())
	s.respond(w, map[string]any{"grants": out, "users": users}, e)
}
func (s *Server) canManage(r *http.Request, g Grant) bool {
	u := who(r)
	if _, _, e := s.connection(g.Connection, g.Bucket); e != nil {
		return false
	}
	return canDelegate(u.User, u.Grants, g)
}
func (s *Server) savePolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Grant       Grant `json:"grant"`
		ConfirmRoot bool  `json:"confirmRoot"`
	}
	if e := decode(w, r, &body, 16384); e != nil {
		s.respond(w, nil, e)
		return
	}
	g := body.Grant
	g.ID = ""
	if !s.canManage(r, g) {
		s.respond(w, nil, fail(403, "access_denied"))
		return
	}
	if g.Prefix == "" && !body.ConfirmRoot {
		s.respond(w, nil, fail(400, "root_confirmation_required"))
		return
	}
	target, e := s.Store.PolicyTarget(r.Context(), g.UserID)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	g.Tenant = target.Tenant
	e = s.Store.ChangeGrant(r.Context(), who(r).User, g, false)
	s.respond(w, map[string]bool{"saved": e == nil}, e)
}
func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	u := who(r)
	all, e := s.Store.Grants(r.Context(), u.User, true)
	if e != nil {
		s.respond(w, nil, e)
		return
	}
	for _, g := range all {
		if g.ID == r.PathValue("id") && s.canManage(r, g) {
			e = s.Store.ChangeGrant(r.Context(), u.User, g, true)
			s.respond(w, map[string]bool{"deleted": e == nil}, e)
			return
		}
	}
	s.respond(w, nil, fail(404, "grant_not_found"))
}
