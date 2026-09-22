package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

type fakeService struct{}

func (fakeService) Ready(context.Context) error { return nil }
func (fakeService) ListApplications(context.Context) ([]domain.SparkApplication, error) {
	return []domain.SparkApplication{{ID: "1", Name: "alpha", Namespace: "spark", Owner: "alice", State: "RUNNING"}, {ID: "2", Name: "beta", Namespace: "spark", Owner: "bob", State: "COMPLETED"}}, nil
}
func (fakeService) GetApplication(context.Context, string, string) (domain.SparkApplication, error) {
	return domain.SparkApplication{}, nil
}
func (fakeService) GetApplicationMetrics(context.Context, string, string, time.Time, time.Time) ([]domain.MetricPoint, error) {
	return []domain.MetricPoint{}, nil
}
func (fakeService) SubmitApplication(_ context.Context, namespace, manifest, _ string) (domain.SparkApplication, error) {
	return domain.SparkApplication{Name: "submitted", Namespace: namespace, YAML: manifest}, nil
}
func (fakeService) SparkUIProxyTarget(context.Context, string, string) (string, error) {
	return "http://spark-ui.spark.svc:4040", nil
}
func (fakeService) GetExecutorLogs(context.Context, string, string, string, time.Time, time.Time, string, int) ([]domain.LogEntry, error) {
	return []domain.LogEntry{{Timestamp: "2026-09-21T00:00:00Z", Line: "executor ready"}}, nil
}
func (fakeService) Summary(context.Context, time.Time, time.Time) (domain.DashboardSummary, error) {
	return domain.DashboardSummary{}, nil
}
func (fakeService) KillApplication(context.Context, string, string, string, string) (domain.OperationAudit, error) {
	return domain.OperationAudit{Result: "SUCCESS"}, nil
}
func (fakeService) DeleteApplication(context.Context, string, string, string, string) (domain.OperationAudit, error) {
	return domain.OperationAudit{Operation: "DELETE", Result: "SUCCESS"}, nil
}
func (fakeService) ListAudit(context.Context) ([]domain.OperationAudit, error) {
	return []domain.OperationAudit{}, nil
}

type fakeAuthStore struct {
	user     domain.User
	hash     string
	sessions map[string]domain.UserSession
}

func (f *fakeAuthStore) BootstrapInitialAdmin(context.Context, domain.User, string, string) error {
	return nil
}
func (f *fakeAuthStore) FindUserCredentials(_ context.Context, username string) (domain.User, string, error) {
	if strings.EqualFold(username, f.user.Username) {
		return f.user, f.hash, nil
	}
	return domain.User{}, "", localauth.ErrUserNotFound
}
func (f *fakeAuthStore) FindUserByID(_ context.Context, id string) (domain.User, error) {
	if id == f.user.ID {
		return f.user, nil
	}
	return domain.User{}, localauth.ErrUserNotFound
}
func (f *fakeAuthStore) ListUsers(context.Context) ([]domain.User, error) {
	return []domain.User{f.user}, nil
}
func (f *fakeAuthStore) CreateUser(context.Context, domain.User, string, string) error { return nil }
func (f *fakeAuthStore) UpsertOIDCUser(_ context.Context, user domain.User, _, _, _ string, _ bool) (domain.User, error) {
	f.user = user
	return user, nil
}
func (f *fakeAuthStore) UpdateUser(_ context.Context, user domain.User, _ *string) error {
	f.user = user
	return nil
}
func (f *fakeAuthStore) DeleteUser(context.Context, string) error { return nil }
func (f *fakeAuthStore) CreateSession(_ context.Context, session domain.UserSession) error {
	f.sessions[session.TokenHash] = session
	return nil
}
func (f *fakeAuthStore) FindSession(_ context.Context, token string, now time.Time) (domain.User, domain.UserSession, error) {
	session, ok := f.sessions[token]
	if !ok || !session.ExpiresAt.After(now) {
		return domain.User{}, domain.UserSession{}, localauth.ErrUserNotFound
	}
	return f.user, session, nil
}
func (f *fakeAuthStore) DeleteSession(_ context.Context, token string) error {
	delete(f.sessions, token)
	return nil
}
func (f *fakeAuthStore) DeleteSessionsForUser(context.Context, string) error {
	f.sessions = map[string]domain.UserSession{}
	return nil
}

func authenticatedHandler(t *testing.T, role domain.UserRole) (http.Handler, *http.Cookie) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAuthStore{user: domain.User{ID: "user-1", Username: "alice", DisplayName: "Alice", Role: role}, hash: string(hash), sessions: map[string]domain.UserSession{}}
	authService := localauth.New(store, time.Hour, bcrypt.MinCost)
	handler := New(config.Config{AuthMode: "local"}, fakeService{}, authService, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"username":"alice","password":"password123"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	return handler, cookies[0]
}

func serveAuthenticated(handler http.Handler, cookie *http.Cookie, recorder *httptest.ResponseRecorder, request *http.Request) {
	request.AddCookie(cookie)
	handler.ServeHTTP(recorder, request)
}

func TestLocalSession(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleAdmin)
	recorder := httptest.NewRecorder()
	serveAuthenticated(handler, cookie, recorder, httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"username":"alice"`)) {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestApplicationFiltering(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleAdmin)
	recorder := httptest.NewRecorder()
	serveAuthenticated(handler, cookie, recorder, httptest.NewRequest(http.MethodGet, "/v1/applications?state=RUNNING&keyword=ali", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() == "[]\n" {
		t.Fatalf("unexpected filtered response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSubmitApplication(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleAdmin)
	body := bytes.NewBufferString(`{"yaml":"apiVersion: sparkoperator.k8s.io/v1beta2\\nkind: SparkApplication"}`)
	recorder := httptest.NewRecorder()
	serveAuthenticated(handler, cookie, recorder, httptest.NewRequest(http.MethodPost, "/v1/namespaces/spark/applications", body))
	if recorder.Code != http.StatusCreated || !bytes.Contains(recorder.Body.Bytes(), []byte(`"name":"submitted"`)) {
		t.Fatalf("unexpected submit response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestViewerCannotSubmitApplication(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleViewer)
	recorder := httptest.NewRecorder()
	serveAuthenticated(handler, cookie, recorder, httptest.NewRequest(http.MethodPost, "/v1/namespaces/spark/applications", bytes.NewBufferString(`{"yaml":"kind: SparkApplication"}`)))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestViewerWriteEndpointsAreForbidden(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleViewer)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/namespaces/spark/applications/demo/kill", bytes.NewBufferString(`{"reason":"test"}`)),
		httptest.NewRequest(http.MethodDelete, "/v1/namespaces/spark/applications/demo", bytes.NewBufferString(`{"reason":"test"}`)),
		httptest.NewRequest(http.MethodGet, "/v1/audit", nil),
		httptest.NewRequest(http.MethodGet, "/v1/users", nil),
	}
	for _, request := range requests {
		recorder := httptest.NewRecorder()
		serveAuthenticated(handler, cookie, recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected 403, got %d %s", request.Method, request.URL.Path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDataEndpointsRequireSession(t *testing.T) {
	handler, _ := authenticatedHandler(t, domain.RoleAdmin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/applications", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCrossOriginLoginIsRejected(t *testing.T) {
	handler, _ := authenticatedHandler(t, domain.RoleAdmin)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://spark-console.local/v1/auth/login", bytes.NewBufferString(`{"username":"alice","password":"password123"}`))
	request.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestExecutorLogsRange(t *testing.T) {
	handler, cookie := authenticatedHandler(t, domain.RoleViewer)
	recorder := httptest.NewRecorder()
	serveAuthenticated(handler, cookie, recorder, httptest.NewRequest(http.MethodGet, "/v1/namespaces/spark/applications/demo/executors/demo-exec-1/logs?from=2026-09-21T00:00:00Z&to=2026-09-21T01:00:00Z&direction=forward", nil))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("executor ready")) {
		t.Fatalf("unexpected executor logs response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestReturnURLRejectsForeignHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://spark-console.local/v1/auth/login", nil)
	if got := safeReturnURL(request, "https://evil.example/phish", "/overview"); got != "/overview" {
		t.Fatalf("unsafe redirect accepted: %s", got)
	}
}

func TestSparkUIProxyRewritesInternalAbsoluteURLs(t *testing.T) {
	target, err := url.Parse("http://kafka-hp-jvm-ui-svc.spark.svc:4040")
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/v1/namespaces/spark/applications/kafka-hp-jvm/spark-ui"
	location := rewriteSparkUILocation("http://kafka-hp-jvm-ui-svc.spark.svc:4040/jobs/?id=1", target, prefix)
	if location != prefix+"/jobs/?id=1" {
		t.Fatalf("internal Location escaped the proxy: %q", location)
	}
	html := []byte(`<a href="http://kafka-hp-jvm-ui-svc.spark.svc:4040/jobs/">Jobs</a><script src="/static/app.js"></script>`)
	rewritten := string(rewriteSparkUIHTML(html, target, prefix))
	if strings.Contains(rewritten, "kafka-hp-jvm-ui-svc.spark.svc") || !strings.Contains(rewritten, `href="`+prefix+`/jobs/"`) || !strings.Contains(rewritten, `src="`+prefix+`/static/app.js"`) {
		t.Fatalf("internal Spark UI link escaped the proxy: %s", rewritten)
	}
}
