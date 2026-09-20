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
func (fakeService) SubmitApplication(_ context.Context, namespace, manifest string) (domain.SparkApplication, error) {
	return domain.SparkApplication{Name: "submitted", Namespace: namespace, YAML: manifest}, nil
}
func (fakeService) SparkUIProxyTarget(context.Context, string, string) (string, error) {
	return "http://spark-ui.spark.svc:4040", nil
}
func (fakeService) Summary(context.Context, time.Time, time.Time) (domain.DashboardSummary, error) {
	return domain.DashboardSummary{}, nil
}
func (fakeService) KillApplication(context.Context, string, string, string) (domain.OperationAudit, error) {
	return domain.OperationAudit{Result: "SUCCESS"}, nil
}
func (fakeService) DeleteApplication(context.Context, string, string, string) (domain.OperationAudit, error) {
	return domain.OperationAudit{Operation: "DELETE", Result: "SUCCESS"}, nil
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

func TestSubmitApplication(t *testing.T) {
	handler := New(config.Config{AdminBypass: true}, fakeService{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := bytes.NewBufferString(`{"yaml":"apiVersion: sparkoperator.k8s.io/v1beta2\\nkind: SparkApplication"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/namespaces/spark/applications", body))
	if recorder.Code != http.StatusCreated || !bytes.Contains(recorder.Body.Bytes(), []byte(`"name":"submitted"`)) {
		t.Fatalf("unexpected submit response: %d %s", recorder.Code, recorder.Body.String())
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
