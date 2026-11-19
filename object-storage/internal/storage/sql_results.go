package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const ResultHeader = "X-Spark-SQL-Results"
const ResultAudience = "spark-console-sql-results"
const MaxResultPage = 2 << 20

type ResultLocation struct {
	Connection string `json:"connection"`
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix"`
}

var resultKey = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}/[0-9]{6}\.json$`)
var resultID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

func (s *Server) reserved(bucket, key string) bool {
	x := s.Config.SQLResults
	return x != nil && bucket == x.Bucket &&
		(strings.HasPrefix(key, x.Prefix) || key == strings.TrimSuffix(x.Prefix, "/"))
}

func (s *Server) resultAllowed(connection, bucket, key, action string) bool {
	x := s.Config.SQLResults
	if x == nil || !s.reserved(bucket, key) {
		return true
	}
	// Another configured connection can point at the same physical bucket.
	if x.Connection != connection {
		return false
	}
	if action == "list" {
		folder := strings.TrimSuffix(key, "/")
		if folder == strings.TrimSuffix(x.Prefix, "/") || folder == x.Prefix+"pages" {
			return true
		}
		return strings.HasPrefix(folder, x.Prefix+"pages/") &&
			resultID.MatchString(strings.TrimPrefix(folder, x.Prefix+"pages/"))
	}
	return (action == "read" || action == "download" || action == "copy-source") &&
		strings.HasPrefix(key, x.Prefix+"pages/") &&
		resultKey.MatchString(strings.TrimPrefix(key, x.Prefix+"pages/"))
}

func (s *Server) resultVisible(connection, bucket string, object Object) bool {
	if object.Directory {
		return s.resultAllowed(connection, bucket, object.Key, "list")
	}
	return s.resultAllowed(connection, bucket, object.Key, "read")
}

func (s *Server) sqlResult(w http.ResponseWriter, r *http.Request) {
	x := s.Config.SQLResults
	if x == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := VerifyAudience(s.Key, r, ResultHeader, ResultAudience); err != nil {
		s.respond(w, nil, err)
		return
	}
	id := r.PathValue("id") + "/" + r.PathValue("page")
	valid := resultKey.MatchString(id)
	if r.Method == http.MethodDelete {
		valid = resultID.MatchString(r.PathValue("id"))
	}
	if !valid || r.URL.RawQuery != "" {
		http.Error(w, "invalid result page", http.StatusBadRequest)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "result storage busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	_, provider, err := s.connection(x.Connection, x.Bucket)
	if err != nil || provider == nil {
		http.Error(w, "result storage unavailable", http.StatusServiceUnavailable)
		return
	}
	key := x.Prefix + "pages/" + id
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodDelete {
		prefix := x.Prefix + "pages/" + r.PathValue("id") + "/"
		page, err := provider.ListRecursive(ctx, x.Bucket, prefix, "", 100)
		if err != nil {
			s.respond(w, nil, err)
			return
		}
		for _, object := range page.Objects {
			if object.Directory || !strings.HasPrefix(object.Key, prefix) ||
				!resultKey.MatchString(strings.TrimPrefix(object.Key, x.Prefix+"pages/")) || object.Version == "" {
				s.respond(w, nil, fail(502, "invalid_provider_response"))
				return
			}
		}
		for _, object := range page.Objects {
			if err := provider.Delete(ctx, x.Bucket, object.Key, object.Version); err != nil {
				s.respond(w, nil, err)
				return
			}
		}
		if page.Next != "" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodPut {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxResultPage))
		if err != nil || len(raw) == 0 {
			http.Error(w, "invalid result page", http.StatusBadRequest)
			return
		}
		if _, err := provider.Put(ctx, x.Bucket, key, bytes.NewReader(raw), int64(len(raw)), ""); err != nil {
			http.Error(w, "result storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := provider.Open(ctx, x.Bucket, key, "")
	if err != nil {
		http.Error(w, "result page unavailable", http.StatusServiceUnavailable)
		return
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, MaxResultPage+1))
	if err != nil || len(raw) > MaxResultPage {
		http.Error(w, "invalid stored result", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
