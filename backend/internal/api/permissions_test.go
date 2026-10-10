package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

func TestTenantCapabilityRoutes(t *testing.T) {
	for _, x := range []struct{ method, path, want string }{
		{"GET", "/v1/templates", "templatesRead"}, {"PATCH", "/v1/templates/t", "templatesWrite"},
		{"GET", "/v1/alerts", "alertsRead"}, {"POST", "/v1/alert-rules", "alertsWrite"},
		{"GET", "/v1/diagnostics/fingerprints", "alertsRead"}, {"GET", "/v1/projects/p/runs", "workflowsRead"},
		{"POST", "/v1/projects/p/workflows/f/trigger", "workflowsWrite"},
		{"GET", "/v1/object-storage/connections", "objectStorage"},
		{"POST", "/v1/namespaces/spark/applications", "submit"},
		{"POST", "/v1/namespaces/spark/applications/a/kill", "kill"},
		{"DELETE", "/v1/namespaces/spark/applications/a", "delete"},
		{"PUT", "/v1/namespaces/spark/applications/a/favorite", ""},
		{"GET", "/v1/applications", ""}, {"GET", "/v1/auth/me", ""},
	} {
		if got := requestCapability(httptest.NewRequest(x.method, x.path, nil)); got != x.want {
			t.Errorf("%s %s: %q", x.method, x.path, got)
		}
	}
}

func TestTenantPermissionsOverrideRole(t *testing.T) {
	u := domain.User{Role: domain.RoleAdmin, Permissions: &domain.TenantPermissions{AllNamespaces: true}}
	if u.Can("kill") || u.Can("objectStorage") {
		t.Fatal("administrator bypassed tenant policy")
	}
	u.Role = domain.RoleViewer
	u.Permissions.Kill = true
	u.Permissions.WorkflowsWrite = true
	if !u.Can("kill") || u.Can("workflowsWrite") {
		t.Fatal("explicit permission / read prerequisite mismatch")
	}
	u.Permissions.WorkflowsRead = true
	if !u.Can("workflowsWrite") {
		t.Fatal("delegated viewer denied")
	}
	u.Disabled = true
	if u.Can("kill") {
		t.Fatal("disabled account allowed")
	}
}

func TestTenantSubmissionPolicyEnforcedAtHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  domain.UserRole
		allow bool
		want  int
	}{
		{"delegated viewer", domain.RoleViewer, true, http.StatusCreated},
		{"minimum viewer", domain.RoleViewer, false, http.StatusForbidden},
		{"restricted administrator", domain.RoleAdmin, false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			store := &fakeAuthStore{user: domain.User{ID: "user-1", Username: "alice", Role: tc.role, Permissions: &domain.TenantPermissions{AllNamespaces: true, Submit: tc.allow}}, hash: string(hash), sessions: map[string]domain.UserSession{}}
			handler := New(config.Config{AuthMode: "local", Namespaces: []string{"spark"}}, fakeService{}, localauth.New(store, time.Hour, bcrypt.MinCost), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			login := httptest.NewRecorder()
			handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"username":"alice","password":"password123"}`)))
			if login.Code != http.StatusOK {
				t.Fatalf("login: %d", login.Code)
			}
			recorder := httptest.NewRecorder()
			serveAuthenticated(handler, login.Result().Cookies()[0], recorder, httptest.NewRequest(http.MethodPost, "/v1/namespaces/spark/applications", bytes.NewBufferString(`{"yaml":"kind: SparkApplication"}`)))
			if recorder.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}
