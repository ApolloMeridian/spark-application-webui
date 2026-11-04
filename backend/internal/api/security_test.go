package api

import (
	"context"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"strings"
	"testing"
	"time"
)

type securityUIService struct {
	fakeService
	target string
}

func (s securityUIService) SparkUIProxyTarget(context.Context, string, string) (string, error) {
	return s.target, nil
}

func TestSparkUIIsolationAndCredentialStripping(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("console credentials forwarded to Driver")
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "spark-console-session=stolen")
		w.Header().Set("Content-Security-Policy", "default-src *")
		io.WriteString(w, `<html><head></head><body><a href="/jobs/">jobs</a></body></html>`)
	}))
	defer upstream.Close()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	store := &fakeAuthStore{user: domain.User{ID: "u", Username: "alice", Role: domain.RoleAdmin, Namespaces: []string{"spark"}}, hash: string(hash), sessions: map[string]domain.UserSession{}}
	auth := localauth.New(store, time.Hour, bcrypt.MinCost)
	_, token, _, err := auth.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatal(err)
	}
	h := New(config.Config{Namespaces: []string{"spark"}}, securityUIService{target: upstream.URL}, auth, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest("GET", "/v1/namespaces/spark/applications/a/spark-ui/jobs/", nil)
	r.AddCookie(&http.Cookie{Name: "spark-console-session", Value: token})
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Origin", "null")
	r.Header.Set("X-Forwarded-Prefix", `/api" onmouseover="bad`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 302 {
		t.Fatal("ticket issuance", w.Code, w.Body.String())
	}
	location := strings.TrimPrefix(w.Header().Get("Location"), "/api")
	request := httptest.NewRequest("GET", location, nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "onmouseover") {
		t.Fatal("unsafe proxy response", w.Header(), w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "null" || !strings.Contains(w.Body.String(), `spark-ui-location`) {
		t.Fatal("sandbox AJAX bridge missing")
	}
	foreign := httptest.NewRecorder()
	h.ServeHTTP(foreign, httptest.NewRequest("GET", strings.Replace(location, "/applications/a/", "/applications/b/", 1), nil))
	if foreign.Code != 401 {
		t.Fatal("ticket authorized different application", foreign.Code)
	}
	if strings.Contains(redactSparkUITicket(location), strings.Split(strings.Split(location, "/t/")[1], "/")[0]) {
		t.Fatal("ticket leaked into logs")
	}
	if err := auth.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	revoked := httptest.NewRecorder()
	h.ServeHTTP(revoked, httptest.NewRequest("GET", location, nil))
	if revoked.Code != 401 {
		t.Fatal("ticket survived logout", revoked.Code)
	}
}

func TestCSRFAndForwardedHeaderSpoofing(t *testing.T) {
	h, _ := authenticatedHandler(t, domain.RoleAdmin)
	for _, tc := range []struct {
		origin, referer, content, custom, site string
		want                                   int
	}{
		{"", "", "text/plain", "", "", 403}, {"null", "", "application/json", "", "", 403},
		{"https://evil.example", "", "application/json", "SparkControlCenter", "", 403},
		{"", "https://evil.example/path", "application/json", "", "", 403},
		{"", "", "application/json", "", "cross-site", 403},
		{"", "", "application/json", "", "", 200},
		{"", "", "text/plain", "SparkControlCenter", "", 200},
	} {
		r := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"username":"alice","password":"password123"}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Referer", tc.referer)
		r.Header.Set("Content-Type", tc.content)
		r.Header.Set("X-Requested-With", tc.custom)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v got %d %s", tc, w.Code, w.Body.String())
		}
	}
}

func TestAuthGateBoundsAttemptsAndParallelism(t *testing.T) {
	g := &authGate{}
	for i := 0; i < 10; i++ {
		if !g.allow("127.0.0.1:1", "alice") {
			t.Fatal(i)
		}
	}
	if g.allow("127.0.0.2:2", "alice") {
		t.Fatal("account limit bypassed via IP change")
	}
	for i := 0; i < 4; i++ {
		if !g.acquire() {
			t.Fatal(i)
		}
	}
	if g.acquire() {
		t.Fatal("unbounded password hashing")
	}
	g.release()
	if !g.acquire() {
		t.Fatal("slot not released")
	}
}

func TestReturnURLRejectsBackslashNetworkPaths(t *testing.T) {
	r := httptest.NewRequest("GET", "http://console.example.test/", nil)
	for _, value := range []string{`/\evil.example/`, `/%5cevil.example/`, `//evil.example/`} {
		if safeReturnURL(r, value, "/overview") != "/overview" {
			t.Fatal(value)
		}
	}
}
