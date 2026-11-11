package workflow

import (
	"context"
	"errors"
	"slices"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/store"
	"testing"
	"time"
)

func TestApplicationArchiveKeepsSameNameUIDsAndTenantIsolation(t *testing.T) {
	s, user, _, _ := fixture(t)
	ctx := domain.WithTenant(context.Background(), user.TenantID)
	app := domain.SparkApplication{ID: "old-instance", Name: "reused-job", Namespace: "spark", State: "RUNNING", CreatedAt: stamp(time.Now().Add(-time.Hour)), Owner: "owner", YAML: "old yaml", Logs: []string{"old logs"}}
	if err := s.store.CollectApplicationNamespace(ctx, "spark", func(context.Context) ([]domain.SparkApplication, error) { return []domain.SparkApplication{app}, nil }); err != nil {
		t.Fatal(err)
	}
	_, err := s.pool.Exec(ctx, `UPDATE application_collections SET collected_at=now()-interval '10 seconds'`)
	if err != nil {
		t.Fatal(err)
	}
	replacement := app
	replacement.ID = "new-instance"
	replacement.State = "RUNNING"
	replacement.YAML = "new yaml"
	replacement.CreatedAt = stamp(time.Now())
	replacement.FinishedAt = ""
	if err = s.store.CollectApplicationNamespace(ctx, "spark", func(context.Context) ([]domain.SparkApplication, error) {
		return []domain.SparkApplication{replacement}, nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.store.QueryLiveApplications(ctx, domain.ApplicationQuery{Namespaces: []string{"spark"}, Page: 1, PageSize: 20, Sort: "createdAt"})
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID != "new-instance" || !page.Items[1].Historical || page.Items[1].State != "DELETED" {
		t.Fatal(page, err)
	}
	saved, err := s.store.GetApplicationSnapshotByID(ctx, "spark", "old-instance")
	if err != nil || saved.YAML != "old yaml" || saved.Logs[0] != "old logs" {
		t.Fatal(saved, err)
	}
	// A workflow's historical tenant is immutable even after its name is reused.
	if _, err = s.pool.Exec(ctx, `UPDATE spark_application_history SET tenant_id='other-tenant' WHERE application_id='old-instance'`); err != nil {
		t.Fatal(err)
	}
	page, err = s.store.QueryLiveApplications(ctx, domain.ApplicationQuery{Namespaces: []string{"spark"}, Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	if _, err = s.store.GetApplicationSnapshotByID(ctx, "spark", "old-instance"); !errors.Is(err, store.ErrSnapshotNotFound) {
		t.Fatal(err)
	}
	audit := domain.NewOperationAudit("purge-test", "spark", app.Name, user.Username, "PURGE_HISTORY", "cleanup", "SUCCESS", "snapshot removed")
	if err = s.store.PurgeApplicationSnapshot(ctx, app.ID, audit); !errors.Is(err, store.ErrSnapshotNotFound) {
		t.Fatal("purge escaped tenant scope", err)
	}
	owner := domain.WithTenant(context.Background(), "other-tenant")
	if err = s.store.PurgeApplicationSnapshot(owner, app.ID, audit); err != nil {
		t.Fatal(err)
	}
	if err = s.store.UpsertApplications(ctx, []domain.SparkApplication{app}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.GetApplicationSnapshotByID(owner, "spark", app.ID); !errors.Is(err, store.ErrSnapshotNotFound) {
		t.Fatal("stale observation restored purged snapshot", err)
	}
	stats, err := s.store.HistorySummary(owner, time.Now().Add(-2*time.Hour), time.Now(), []string{"spark"})
	if err != nil || stats.Submitted != 1 {
		t.Fatal(stats, err)
	}
	audits, err := s.store.List(owner, 10)
	if err != nil || !slices.ContainsFunc(audits, func(entry domain.OperationAudit) bool {
		return entry.ID == audit.ID && entry.TenantID == "other-tenant" && entry.Operation == "PURGE_HISTORY"
	}) {
		t.Fatal(audits, err)
	}
}
