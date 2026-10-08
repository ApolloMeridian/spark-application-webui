package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"spark-control-center/backend/internal/domain"
)

func TestPostgresAuditRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	audit := domain.NewAudit("integration-"+time.Now().UTC().Format("20060102150405.000000000"), "spark", "integration-demo", "admin", "test", "SUCCESS", "integration test")
	if err := store.Insert(ctx, audit); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == audit.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inserted audit %q was not returned", audit.ID)
	}
}

func TestPostgresApplicationHistoryRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	app := domain.SparkApplication{
		ID: "history-integration-" + now.Format("20060102150405.000000000"), Cluster: "test", Namespace: "spark",
		Name: "failed-demo", Owner: "integration", State: "FAILED", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339),
		FinishedAt: now.Format(time.RFC3339),
	}
	if err := store.UpsertApplications(ctx, []domain.SparkApplication{app}); err != nil {
		t.Fatal(err)
	}
	summary, err := store.HistorySummary(ctx, now.Add(-time.Hour), now.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Submitted < 1 || summary.Failed < 1 {
		t.Fatalf("expected submitted and failed history counts, got %#v", summary)
	}
}

func TestPostgresOIDCAutoCreateUsesEmptyNamespaceArray(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	suffix := now.Format("20060102150405.000000000")
	username := "oidc-" + suffix
	candidate := domain.User{
		ID:          "oidc-integration-" + suffix,
		Username:    username,
		DisplayName: "OIDC Integration User",
		Email:       username + "@example.invalid",
		Role:        domain.RoleViewer,
		AuthSource:  "oidc",
		CreatedAt:   now.Format(time.RFC3339Nano),
		UpdatedAt:   now.Format(time.RFC3339Nano),
		Namespaces:  nil,
	}
	created, err := store.UpsertOIDCUser(ctx, candidate, username, "https://issuer.example.invalid", candidate.ID, true)
	if err != nil {
		t.Fatalf("auto-create OIDC user with no namespace assignment: %v", err)
	}
	if created.Namespaces == nil || len(created.Namespaces) != 0 {
		t.Fatalf("expected a non-nil empty namespace list, got %#v", created.Namespaces)
	}
	persisted, err := store.FindUserByID(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("read auto-created OIDC user: %v", err)
	}
	if persisted.Namespaces == nil || len(persisted.Namespaces) != 0 {
		t.Fatalf("expected PostgreSQL to persist an empty namespace array, got %#v", persisted.Namespaces)
	}
}

func TestPostgresUpsertAlertReturnsPersistedTimestamps(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	rule, err := store.SaveAlertRule(ctx, domain.AlertRule{Name: "integration-" + suffix, Type: "failure", Severity: "error", Enabled: true, CreatedBy: "integration"})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	alert := domain.Alert{ID: "alert-" + suffix, RuleID: rule.ID, RuleName: rule.Name, Namespace: "spark", ApplicationName: "failed-job", Fingerprint: "fingerprint-" + suffix, Severity: "error", Status: "active", Summary: "failed", Evidence: []string{"state=FAILED"}, Confidence: "high"}
	createdAlert, created, err := store.UpsertAlert(ctx, alert)
	if err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	if !created || createdAlert.FirstSeenAt == "" || createdAlert.LastSeenAt == "" {
		t.Fatalf("expected created alert with persisted timestamps, got created=%v alert=%#v", created, createdAlert)
	}
	updatedAlert, created, err := store.UpsertAlert(ctx, alert)
	if err != nil {
		t.Fatalf("update alert: %v", err)
	}
	if created || updatedAlert.FirstSeenAt != createdAlert.FirstSeenAt || updatedAlert.LastSeenAt == "" {
		t.Fatalf("expected updated alert with stable firstSeenAt, got created=%v alert=%#v", created, updatedAlert)
	}
}

func TestPostgresDiagnosticRuleRoundTripAndVersionHistory(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rule, err := s.SaveAlertRule(ctx, domain.AlertRule{Name: "diagnoses", Type: "diagnostic", DiagnosticCodes: []string{"VOLUME_MOUNT", "FUTURE_DIAGNOSIS"}, Severity: "error", Enabled: true, CreatedBy: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.DeleteAlertRule(context.Background(), rule.ID) }()
	rules, err := s.ListAlertRules(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, saved := range rules {
		if saved.ID == rule.ID {
			found = len(saved.DiagnosticCodes) == 2 && saved.DiagnosticCodes[0] == "VOLUME_MOUNT"
		}
	}
	if !found {
		t.Fatal("diagnostic codes did not survive PostgreSQL round trip")
	}
	rule.DiagnosticCodes = nil // Empty means every current and future diagnostic code.
	updated, err := s.SaveAlertRule(ctx, rule)
	if err != nil || updated.Version != 2 {
		t.Fatalf("update diagnostic rule: %#v, %v", updated, err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT cardinality(diagnostic_codes) FROM alert_rules WHERE id=$1`, rule.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty diagnostic codes must be an empty array: %d, %v", count, err)
	}
	var snapshot domain.AlertRule
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT snapshot FROM alert_rule_versions WHERE rule_id=$1 AND version=1`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || len(snapshot.DiagnosticCodes) != 2 {
		t.Fatalf("original diagnostic rule version changed: %#v, %v", snapshot, err)
	}
}
