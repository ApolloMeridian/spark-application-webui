package storage

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type resultProvider struct {
	fakeProvider
	key, value string
	page       Page
	deleted    []string
}

func (p *resultProvider) Put(_ context.Context, bucket, key string, body io.Reader, _ int64, _ string) (Object, error) {
	if bucket != "allowed" {
		return Object{}, fail(403, "invalid_bucket")
	}
	data, err := io.ReadAll(body)
	p.key, p.value = key, string(data)
	return Object{}, err
}

func (p *resultProvider) Open(_ context.Context, bucket, key, _ string) (io.ReadCloser, error) {
	if bucket != "allowed" || key != p.key {
		return nil, fail(404, "missing")
	}
	return io.NopCloser(strings.NewReader(p.value)), nil
}

func (p *resultProvider) ListRecursive(_ context.Context, bucket, prefix, cursor string, limit int) (Page, error) {
	if bucket != "allowed" || !strings.HasPrefix(prefix, "private/pages/") || cursor != "" || limit != 100 {
		return Page{}, fail(400, "wrong_cleanup_location")
	}
	return p.page, nil
}

func (p *resultProvider) Delete(_ context.Context, bucket, key, version string) error {
	if bucket != "allowed" || version != "v1" {
		return fail(400, "wrong_cleanup_version")
	}
	p.deleted = append(p.deleted, key)
	return nil
}

func TestPrivateSQLResultsUseSelectedProvider(t *testing.T) {
	for _, kind := range []string{"s3", "gcs"} {
		t.Run(kind, func(t *testing.T) {
			key := []byte(strings.Repeat("k", 32))
			provider := &resultProvider{}
			s := NewServer(Config{
				Connections: []Connection{{ID: kind, Provider: kind, Buckets: []string{"allowed"}}},
				SQLResults:  &ResultLocation{Connection: kind, Bucket: "allowed", Prefix: "private/"},
				Concurrent:  1,
			}, nil, key, map[string]Provider{kind: provider}, slog.Default())
			path := "/internal/sql-results/00000000-0000-0000-0000-000000000001/000001.json"
			invoke := func(method, target, audience string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, target, strings.NewReader(`{"rows":[[1]]}`))
				r.Header.Set(ResultHeader, Sign(key, Claims{ID: "sql", Tenant: "internal", Method: method,
					Target: target, Audience: audience, Expires: time.Now().Add(time.Minute).Unix()}))
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			if w := invoke(http.MethodPut, path, "spark-object-storage"); w.Code != 401 {
				t.Fatal(w.Code)
			}
			if w := invoke(http.MethodPut, path+"?bucket=other", ResultAudience); w.Code != 400 {
				t.Fatal(w.Code)
			}
			if w := invoke(http.MethodPut, path, ResultAudience); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if provider.key != "private/pages/00000000-0000-0000-0000-000000000001/000001.json" {
				t.Fatal(provider.key)
			}
			if w := invoke(http.MethodGet, path, ResultAudience); w.Code != 200 || w.Body.String() != provider.value {
				t.Fatal(w.Code, w.Body.String())
			}
			if !s.reserved("allowed", "private/pages") || !s.reserved("allowed", provider.key) ||
				s.reserved("allowed", "private/pages-backup/old.json") || s.reserved("other", provider.key) {
				t.Fatal("reserved result boundary is incorrect")
			}
			for _, action := range []string{"read", "download", "copy-source"} {
				if !s.resultAllowed(kind, "allowed", provider.key, action) {
					t.Fatalf("%s of a result page should use the normal storage ACL", action)
				}
			}
			for _, action := range []string{"edit", "upload", "delete", "copy-destination"} {
				if s.resultAllowed(kind, "allowed", provider.key, action) ||
					s.resultAllowed("alias", "allowed", provider.key, action) {
					t.Fatalf("%s should be protected even through another connection", action)
				}
				for _, object := range []string{"private/artifacts/test.jar", "private/staging/input.txt", "private/pages-backup/old.json"} {
					if !s.resultAllowed(kind, "allowed", object, action) {
						t.Fatalf("%s should follow normal ACL for %s", action, object)
					}
				}
			}
			if !s.resultVisible(kind, "allowed", Object{Key: "private/", Directory: true}) ||
				!s.resultVisible(kind, "allowed", Object{Key: "private/staging/", Directory: true}) ||
				!s.resultVisible(kind, "allowed", Object{Key: "private/artifacts/", Directory: true}) {
				t.Fatal("wrong directory visibility")
			}
			deletePath := strings.TrimSuffix(path, "/000001.json")
			provider.page = Page{Objects: []Object{{Key: provider.key, Version: "v1"}}, Next: "second-page"}
			if got := invoke(http.MethodDelete, deletePath, "spark-object-storage"); got.Code != 401 {
				t.Fatal(got.Code)
			}
			if got := invoke(http.MethodDelete, deletePath, ResultAudience); got.Code != 202 || len(provider.deleted) != 1 {
				t.Fatal(got.Code, provider.deleted)
			}
			provider.page.Objects = append(provider.page.Objects, Object{Key: "private/artifacts/test.jar", Version: "v1"})
			if got := invoke(http.MethodDelete, deletePath, ResultAudience); got.Code != 502 || len(provider.deleted) != 1 {
				t.Fatal(got.Code, provider.deleted)
			}
			provider.page = Page{}
			if got := invoke(http.MethodDelete, deletePath, ResultAudience); got.Code != 204 {
				t.Fatal(got.Code)
			}
		})
	}
}
