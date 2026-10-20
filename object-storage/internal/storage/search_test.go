package storage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type searchProvider struct{ fakeProvider }

func (f *searchProvider) ListRecursive(_ context.Context, b, p, token string, n int) (Page, error) {
	if p != "team/a/" || n != 1 {
		return Page{}, fail(400, "unexpected_scan")
	}
	if token == "" {
		return Page{Objects: []Object{{Key: p + "unrelated.txt"}}, Next: "second"}, nil
	}
	return Page{Objects: []Object{{Key: p + "nested/Config.YAML"}}}, nil
}

func TestRecursiveSearchScopePaginationAndCursorBinding(t *testing.T) {
	store := dbFixture(t)
	ctx := context.Background()
	if err := store.Bootstrap(ctx, []Bootstrap{{Username: "admin", Tenant: "default", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: Actions}}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Connections: []Connection{{ID: "demo", Name: "Search", Provider: "gcs", Buckets: []string{"bucket"}}}}
	cfg.Connections[0].GCS.AuthMode = "gkeWif"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("x", 32))
	h := NewServer(cfg, store, key, map[string]Provider{"demo": &searchProvider{}}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	send := func(target string, want int) Page {
		t.Helper()
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("X-Spark-Storage-Identity", Sign(key, Claims{ID: "admin", Tenant: "default", Method: "GET", Target: r.URL.RequestURI(), Audience: "spark-object-storage", Expires: time.Now().Add(time.Minute).Unix()}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", target, w.Code, w.Body.String())
		}
		var out Page
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	base := "/v1/objects?connection=demo&bucket=bucket&key=team/a/&limit=1"
	one := send(base+"&search=CONFIG", 200)
	if len(one.Objects) != 0 || one.Next == "" || one.Scanned != 1 {
		t.Fatalf("first empty batch must retain cursor: %+v", one)
	}
	two := send(base+"&search=config&cursor="+url.QueryEscape(one.Next), 200)
	if len(two.Objects) != 1 || two.Objects[0].Key != "team/a/nested/Config.YAML" || two.Next != "" {
		t.Fatalf("nested result: %+v", two)
	}
	send(base+"&search=other&cursor="+url.QueryEscape(one.Next), 400)
	send(base+"&cursor="+url.QueryEscape(one.Next), 400)
	send(strings.Replace(base, "key=team/a/", "key=team/ab/", 1)+"&search=config", 403)
	send(base+"&search="+strings.Repeat("x", 257), 400)
	send(base+"&search=a%00b", 400)
}
