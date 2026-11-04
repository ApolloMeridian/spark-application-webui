package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"testing"
	"time"
)

func TestSharedNamespaceTemplatesRulesAlertsAndHistoryAreTenantScoped(t *testing.T) {
	s, user, _, _ := fixture(t)
	a := domain.WithTenant(context.Background(), user.TenantID)
	b := domain.WithTenant(context.Background(), "default")
	tplA, err := s.store.SaveTemplate(a, domain.ApplicationTemplate{Name: "Shared name", Namespace: "spark", Manifest: testManifest, Parameters: []domain.TemplateParameter{}, CreatedBy: user.Username})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SaveTemplate(b, domain.ApplicationTemplate{Name: "Shared name", Namespace: "spark", Manifest: testManifest, Parameters: []domain.TemplateParameter{}, CreatedBy: "other"}); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{a, b} {
		list, e := s.store.ListTemplates(ctx, nil, true)
		if e != nil || len(list) != 1 {
			t.Fatal(list, e)
		}
	}
	if _, err = s.store.GetTemplate(b, tplA.ID); err == nil {
		t.Fatal("cross-tenant template read")
	}
	if _, err = s.store.SaveTemplate(b, tplA); err == nil {
		t.Fatal("cross-tenant template edit")
	}
	if err = s.store.DeleteTemplate(b, tplA.ID); err == nil {
		t.Fatal("cross-tenant template delete")
	}
	other := user
	other.TenantID = "default"
	if _, err = s.TemplateVersionAction(b, other, tplA.ID, 1, false); err == nil {
		t.Fatal("cross-tenant restore")
	}
	ruleA, err := s.store.SaveAlertRule(a, domain.AlertRule{Name: "A", Type: "failure", Enabled: true, Severity: "error", CreatedBy: user.Username})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SaveAlertRule(b, domain.AlertRule{Name: "B", Type: "failure", Enabled: true, Severity: "error", CreatedBy: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SaveAlertRule(b, ruleA); err == nil {
		t.Fatal("cross-tenant rule edit")
	}
	for _, ctx := range []context.Context{a, b} {
		rules, e := s.store.ListAlertRules(ctx, nil)
		if e != nil || len(rules) != 1 {
			t.Fatal(rules, e)
		}
	}
	alert, _, err := s.store.UpsertAlert(a, domain.Alert{ID: "owned-alert", RuleID: ruleA.ID, RuleName: "A", Namespace: "spark", ApplicationName: "owned", Fingerprint: "owned", Severity: "error", Summary: "private", Evidence: []string{"detail"}, Confidence: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if list, e := s.store.ListAlerts(b, nil); e != nil || len(list) != 0 {
		t.Fatal("cross-tenant alerts", list, e)
	}
	if err = s.store.UpdateAlertStatus(b, alert.ID, "acknowledged", "other", nil, nil); err == nil {
		t.Fatal("cross-tenant alert mutation")
	}
	if _, err = s.pool.Exec(a, `INSERT INTO workflow_resources(namespace,application_name,task_id,tenant_id) VALUES('spark','owned','owned-task',$1)`, user.TenantID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	apps := []domain.SparkApplication{{ID: "owned", WorkflowTaskID: "owned-task", Namespace: "spark", Name: "owned", CreatedAt: stamp(now), State: "FAILED", Diagnostics: []domain.ApplicationDiagnostic{{Code: "VOLUME_MOUNT"}}}, {ID: "external", Namespace: "spark", Name: "external", CreatedAt: stamp(now), State: "FAILED", Diagnostics: []domain.ApplicationDiagnostic{{Code: "IMAGE_PULL"}}}}
	if err = s.store.UpsertApplications(a, apps); err != nil {
		t.Fatal(err)
	}
	visible, err := s.store.FilterTenantApplications(b, apps)
	if err != nil || len(visible) != 1 || visible[0].Name != "external" {
		t.Fatal(visible, err)
	}
	history, err := s.store.HistorySummary(b, now.Add(-time.Hour), now.Add(time.Hour), []string{"spark"})
	if err != nil || history.Submitted != 1 || history.Failed != 1 {
		t.Fatal(history, err)
	}
	fingerprints, err := s.store.ListFailureFingerprints(b, []string{"spark"})
	if err != nil || len(fingerprints) != 1 || fingerprints[0].Code != "IMAGE_PULL" {
		t.Fatal(fingerprints, err)
	}
	// Reusing a name for another tenant must not reassign the previous UID's history.
	if _, err = s.pool.Exec(b, `UPDATE workflow_resources SET tenant_id='default',task_id='replacement-task' WHERE namespace='spark' AND application_name='owned'`); err != nil {
		t.Fatal(err)
	}
	replacement := apps[0]
	replacement.ID = "replacement-uid"
	replacement.WorkflowTaskID = "replacement-task"
	if err = s.store.UpsertApplications(b, []domain.SparkApplication{replacement}); err != nil {
		t.Fatal(err)
	}
	history, err = s.store.HistorySummary(b, now.Add(-time.Hour), now.Add(time.Hour), []string{"spark"})
	if err != nil || history.Submitted != 2 {
		t.Fatal("historical tenant changed with name reuse", history, err)
	}
	if _, err = s.pool.Exec(b, `UPDATE spark_application_history SET tenant_id=NULL WHERE application_id='replacement-uid'`); err != nil {
		t.Fatal(err)
	}
	history, err = s.store.HistorySummary(b, now.Add(-time.Hour), now.Add(time.Hour), []string{"spark"})
	if err != nil || history.Submitted != 1 {
		t.Fatal("unknown legacy history was shared", history, err)
	}
}

func TestSparkUITicketsUseSharedPostgresAndRevokeWithParentSession(t *testing.T) {
	s, user, _, _ := fixture(t)
	ctx := context.Background()
	parent := "fixture-session-raw"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(parent)))
	if err := s.store.CreateSession(ctx, domain.UserSession{TokenHash: hash, UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	auth := localauth.New(s.store, time.Hour, 4)
	ticket, err := auth.IssueSparkUITicket(ctx, parent, "spark", "job-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticateSparkUITicket(ctx, ticket, "spark", "job-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticateSparkUITicket(ctx, ticket, "spark", "job-b"); err == nil {
		t.Fatal("ticket escaped its application")
	}
	if err = s.store.DeleteSession(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticateSparkUITicket(ctx, ticket, "spark", "job-a"); err == nil {
		t.Fatal("ticket survived parent session revocation")
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM spark_ui_tickets`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ticket cascade failed", count, err)
	}
}

func TestAlertLeaseDeduplicatesReplicasAndAllowsRecovery(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.store.RunAlertEvaluation(ctx, time.Hour, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	called := false
	if err := s.store.RunAlertEvaluation(ctx, time.Hour, func(context.Context) error { called = true; return nil }); err != nil || called {
		t.Fatal("duplicate evaluator", err)
	}
	if err := s.pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE alert_evaluation_lease SET expires_at=now()-interval '1 second',last_completed_at='-infinity'`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.RunAlertEvaluation(ctx, time.Hour, func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatal("takeover failed", err)
	}
}
