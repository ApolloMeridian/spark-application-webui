package workflow

import (
	"context"
	"errors"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/store"
	"testing"
	"time"
)

func TestApplicationArchiveKeepsSameNameUIDsAndTenantIsolation(t *testing.T) {
	s, user, _, _ := fixture(t)
	ctx := domain.WithTenant(context.Background(), user.TenantID)
	app := domain.SparkApplication{ID: "old-instance", Name: "reused-job", Namespace: "spark", State: "COMPLETED", CreatedAt: stamp(time.Now().Add(-time.Hour)), FinishedAt: stamp(time.Now().Add(-time.Minute)), Owner: "owner", YAML: "old yaml", Logs: []string{"old logs"}}
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
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID != "new-instance" || !page.Items[1].Historical {
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
}
