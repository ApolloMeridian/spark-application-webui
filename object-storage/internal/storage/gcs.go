package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	gcs "cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type GCS struct {
	Client *gcs.Client
	HTTP   *http.Client
	Chunk  int64
}

func NewGCS(ctx context.Context, c Connection, chunk int64) (*GCS, error) {
	var creds *google.Credentials
	var e error
	if c.GCS.AuthMode == "kubernetesWif" {
		raw, err := os.ReadFile(c.GCS.CredentialFile)
		if err != nil {
			return nil, err
		}
		var kind struct {
			Type             string `json:"type"`
			TokenURL         string `json:"token_url"`
			ImpersonationURL string `json:"service_account_impersonation_url"`
			CredentialSource struct {
				File       string `json:"file"`
				URL        string `json:"url"`
				Executable any    `json:"executable"`
			} `json:"credential_source"`
		}
		if json.Unmarshal(raw, &kind) != nil || kind.Type != "external_account" || kind.TokenURL != "https://sts.googleapis.com/v1/token" || !validImpersonationURL(kind.ImpersonationURL) || kind.CredentialSource.File != "/var/run/secrets/storage-wif/"+c.ID+"/token" || kind.CredentialSource.URL != "" || kind.CredentialSource.Executable != nil {
			return nil, fmt.Errorf("only projected-token external_account WIF is permitted")
		}
		creds, e = google.CredentialsFromJSON(ctx, raw, gcs.ScopeReadWrite)
	} else {
		creds, e = google.FindDefaultCredentials(ctx, gcs.ScopeReadWrite)
		if creds != nil && len(creds.JSON) > 0 {
			return nil, fmt.Errorf("GKE WIF must use metadata credentials, not credential files")
		}
	}
	if e != nil {
		return nil, e
	}
	client, e := gcs.NewClient(ctx, option.WithCredentials(creds))
	if e != nil {
		return nil, e
	}
	httpClient := oauth2.NewClient(ctx, creds.TokenSource)
	return &GCS{client, httpClient, chunk}, nil
}
func validImpersonationURL(value string) bool {
	if value == "" {
		return true
	}
	u, e := url.Parse(value)
	return e == nil && u.Scheme == "https" && u.Host == "iamcredentials.googleapis.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/v1/projects/-/serviceAccounts/") && strings.HasSuffix(u.Path, ":generateAccessToken")
}
func gcsObject(x *gcs.ObjectAttrs) Object {
	return Object{Key: x.Name, Size: x.Size, Version: strconv.FormatInt(x.Generation, 10), Modified: x.Updated}
}
func (s *GCS) List(ctx context.Context, b, p, token string, n int) (Page, error) {
	iter := s.Client.Bucket(b).Objects(ctx, &gcs.Query{Prefix: p, Delimiter: "/"})
	pager := iterator.NewPager(iter, n, token)
	var values []*gcs.ObjectAttrs
	next, e := pager.NextPage(&values)
	if e != nil {
		return Page{}, e
	}
	out := Page{Objects: []Object{}, Next: next}
	for _, v := range values {
		if v.Prefix != "" {
			out.Objects = append(out.Objects, Object{Key: v.Prefix, Directory: true})
		} else if v.Name != p {
			out.Objects = append(out.Objects, gcsObject(v))
		}
	}
	return out, nil
}
func (s *GCS) Head(ctx context.Context, b, k string) (Object, error) {
	x, e := s.Client.Bucket(b).Object(k).Attrs(ctx)
	if e != nil {
		return Object{}, e
	}
	return gcsObject(x), nil
}
func (s *GCS) handle(b, k, v string) (*gcs.ObjectHandle, error) {
	o := s.Client.Bucket(b).Object(k)
	if v == "" {
		return o, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n <= 0 {
		return nil, fail(400, "invalid_version")
	}
	return o.If(gcs.Conditions{GenerationMatch: n}), nil
}
func (s *GCS) Open(ctx context.Context, b, k, v string) (io.ReadCloser, error) {
	o, e := s.handle(b, k, v)
	if e != nil {
		return nil, e
	}
	return o.NewReader(ctx)
}
func (s *GCS) Put(ctx context.Context, b, k string, r io.Reader, size int64, v string) (Object, error) {
	o, e := s.handle(b, k, v)
	if e != nil {
		return Object{}, e
	}
	if v == "" {
		o = o.If(gcs.Conditions{DoesNotExist: true})
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	w := o.NewWriter(child)
	w.ChunkSize = int(s.Chunk)
	w.ContentType = "application/octet-stream"
	count, e := io.Copy(w, io.LimitReader(r, size+1))
	if e != nil || count != size {
		cancel()
		_ = w.Close()
		if e == nil {
			e = fail(400, "size_mismatch")
		}
		return Object{}, e
	}
	if e = w.Close(); e != nil {
		return Object{}, e
	}
	return gcsObject(w.Attrs()), nil
}
func (s *GCS) Delete(ctx context.Context, b, k, v string) error {
	o, e := s.handle(b, k, v)
	if e != nil {
		return e
	}
	return o.Delete(ctx)
}
func (s *GCS) Copy(ctx context.Context, b, k, db, dk, v string) (Object, error) {
	src, e := s.handle(b, k, v)
	if e != nil {
		return Object{}, e
	}
	x, e := s.Client.Bucket(db).Object(dk).If(gcs.Conditions{DoesNotExist: true}).CopierFrom(src).Run(ctx)
	if e != nil {
		return Object{}, e
	}
	return gcsObject(x), nil
}
func (s *GCS) Start(ctx context.Context, b, k string, size int64, v string) (Upload, error) {
	values := url.Values{"uploadType": {"resumable"}, "name": {k}, "ifGenerationMatch": {"0"}}
	if v != "" {
		values.Set("ifGenerationMatch", v)
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://storage.googleapis.com/upload/storage/v1/b/"+url.PathEscape(b)+"/o?"+values.Encode(), strings.NewReader("{}"))
	if e != nil {
		return Upload{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	req.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return Upload{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Upload{}, fail(resp.StatusCode, "gcs_upload_init_failed")
	}
	location := resp.Header.Get("Location")
	if !validSession(location) {
		return Upload{}, fmt.Errorf("invalid GCS session")
	}
	return Upload{ID: location, Size: size, Version: v, Parts: []Part{}}, nil
}
func validSession(value string) bool {
	u, e := url.Parse(value)
	return e == nil && u.Scheme == "https" && u.Host == "storage.googleapis.com" && u.User == nil && strings.HasPrefix(u.Path, "/upload/")
}
func (s *GCS) request(ctx context.Context, u Upload, r io.Reader, size int64, contentRange string) (Upload, error) {
	if !validSession(u.ID) {
		return u, fail(400, "invalid_upload")
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", u.ID, r)
	if e != nil {
		return u, e
	}
	req.ContentLength = size
	req.Header.Set("Content-Range", contentRange)
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return u, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		var object struct {
			Generation string `json:"generation"`
			Size       string `json:"size"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&object) != nil || object.Generation == "" || object.Size != strconv.FormatInt(u.Size, 10) {
			return u, fail(502, "invalid_provider_response")
		}
		u.Complete = true
		u.Offset = u.Size
		u.FinalVersion = object.Generation
		return u, nil
	}
	if resp.StatusCode != 308 {
		return u, fail(resp.StatusCode, "gcs_upload_failed")
	}
	u.Offset = 0
	if v := resp.Header.Get("Range"); v != "" {
		pair := strings.Split(strings.TrimPrefix(v, "bytes="), "-")
		if len(pair) != 2 || pair[0] != "0" {
			return u, fmt.Errorf("invalid GCS range")
		}
		last, e := strconv.ParseInt(pair[1], 10, 64)
		if e != nil || last < 0 || last >= u.Size {
			return u, fmt.Errorf("invalid GCS range")
		}
		u.Offset = last + 1
	}
	return u, nil
}
func (s *GCS) Part(ctx context.Context, b, k string, u Upload, n int32, r io.Reader, size int64) (Upload, error) {
	return s.request(ctx, u, r, size, fmt.Sprintf("bytes %d-%d/%d", u.Offset, u.Offset+size-1, u.Size))
}
func (s *GCS) Status(ctx context.Context, b, k string, u Upload) (Upload, error) {
	return s.request(ctx, u, nil, 0, fmt.Sprintf("bytes */%d", u.Size))
}
func (s *GCS) Complete(ctx context.Context, b, k string, u Upload) (Object, error) {
	latest, e := s.Status(ctx, b, k, u)
	if e != nil {
		return Object{}, e
	}
	if !latest.Complete {
		return Object{}, fail(400, "upload_incomplete")
	}
	return Object{Key: k, Size: u.Size, Version: latest.FinalVersion}, nil
}
func (s *GCS) Abort(ctx context.Context, b, k string, u Upload) error {
	if !validSession(u.ID) {
		return fail(400, "invalid_upload")
	}
	req, _ := http.NewRequestWithContext(ctx, "DELETE", u.ID, nil)
	resp, e := s.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 499 && resp.StatusCode != 204 && resp.StatusCode != 200 {
		return fail(resp.StatusCode, "gcs_abort_failed")
	}
	return nil
}
