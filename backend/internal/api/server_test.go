package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
)

type fakeService struct{}

func (fakeService) Ready(context.Context) error { return nil }
func (fakeService) ListApplications(context.Context) ([]domain.SparkApplication, error) {
	return []domain.SparkApplication{{ID: "1", Name: "alpha", Namespace: "spark", Owner: "alice", Team: "data", State: "RUNNING"}, {ID: "2", Name: "beta", Namespace: "spark", Owner: "bob", Team: "ml", State: "COMPLETED"}}, nil
}
func (fakeService) GetApplication(context.Context, string, string) (domain.SparkApplication, error) {
	return domain.SparkApplication{}, nil
}
func (fakeService) Summary(context.Context) (domain.DashboardSummary, error) {
	return domain.DashboardSummary{}, nil
}
func (fakeService) KillApplication(context.Context, string, string, string) (domain.OperationAudit, error) {
	return domain.OperationAudit{Result: "SUCCESS"}, nil
}
func (fakeService) ListAudit(context.Context) ([]domain.OperationAudit, error) {
	return []domain.OperationAudit{}, nil
}

func TestAdminBypassSession(t *testing.T) {
	handler := New(config.Config{AdminBypass: true}, fakeService{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"role\":\"admin\",\"username\":\"admin\"}\n" {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestApplicationFiltering(t *testing.T) {
	handler := New(config.Config{AdminBypass: true}, fakeService{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/applications?state=RUNNING&keyword=ali", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() == "[]\n" {
		t.Fatalf("unexpected filtered response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestReturnURLRejectsForeignHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://spark-console.local/v1/auth/login", nil)
	if got := safeReturnURL(request, "https://evil.example/phish", "/overview"); got != "/overview" {
		t.Fatalf("unsafe redirect accepted: %s", got)
	}
}
