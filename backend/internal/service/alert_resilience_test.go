package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"testing"
	"time"
)

type partialAlertKube struct{ fakeKubernetes }

func (f *partialAlertKube) ListSparkApplications(context.Context, string) ([]kube.Object, error) {
	a, b := completedObject(), completedObject()
	a["metadata"].(map[string]any)["name"] = "bad"
	b["metadata"].(map[string]any)["name"] = "good"
	for _, o := range []kube.Object{a, b} {
		o["status"].(map[string]any)["applicationState"] = map[string]any{"state": "FAILED"}
	}
	return []kube.Object{a, b}, nil
}
func (f *partialAlertKube) GetSparkApplication(_ context.Context, _, name string) (kube.Object, error) {
	if name == "bad" {
		return nil, fmt.Errorf("transient")
	}
	o := completedObject()
	o["metadata"].(map[string]any)["name"] = "good"
	o["status"].(map[string]any)["applicationState"] = map[string]any{"state": "FAILED"}
	return o, nil
}

type resilientAlerts struct {
	fakeAlertStore
	saved    []domain.Alert
	resolved []string
}

func (f *resilientAlerts) UpsertAlert(_ context.Context, a domain.Alert) (domain.Alert, bool, error) {
	f.saved = append(f.saved, a)
	return a, true, nil
}
func (f *resilientAlerts) ListAlerts(context.Context, []string) ([]domain.Alert, error) {
	return []domain.Alert{{ID: "prior", Namespace: "spark", ApplicationName: "bad", Status: "active"}}, nil
}
func (f *resilientAlerts) ResolveInactiveAlerts(_ context.Context, active []string) error {
	f.resolved = active
	return nil
}
func TestAlertDiagnosticFailureDoesNotAbortOrRecoverUnknownJob(t *testing.T) {
	pg := &resilientAlerts{fakeAlertStore: fakeAlertStore{rules: []domain.AlertRule{{ID: "rule", Type: "diagnostic", Enabled: true, Severity: "error"}}}}
	s := New(config.Config{Namespaces: []string{"spark"}}, &partialAlertKube{}, fakePrometheus{}, pg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.EvaluateAlerts(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(pg.saved) == 0 || pg.saved[0].ApplicationName != "good" {
		t.Fatal("healthy diagnostic collection was skipped", pg.saved)
	}
	found := false
	for _, id := range pg.resolved {
		if id == "prior" {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown job alert would be recovered")
	}
}
func TestWebhookNeverFollowsRedirectOrDialsMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://169.254.169.254/", 302) }))
	defer server.Close()
	client := newWebhookClient(time.Second)
	r, err := client.Get(server.URL)
	if err != nil || r.StatusCode != 302 {
		t.Fatal(r, err)
	}
	r.Body.Close()
	if _, err = client.Get("http://169.254.169.254/"); err == nil {
		t.Fatal("metadata access allowed")
	}
}
