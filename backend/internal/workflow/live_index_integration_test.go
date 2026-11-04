package workflow

import (
	"context"
	"fmt"
	"spark-control-center/backend/internal/domain"
	"testing"
	"time"
)

func TestLiveIndexPagesWithTenantScopeAndCollectionFencing(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := domain.WithTenant(context.Background(), user.TenantID)
	flow := single(t, s, user, project)
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	apps := []domain.SparkApplication{}
	for i := 0; i < 12; i++ {
		apps = append(apps, domain.SparkApplication{ID: fmt.Sprint("live-", i), Namespace: "spark", Name: fmt.Sprintf("job-%02d", i), CreatedAt: stamp(time.Now()), State: "RUNNING", Owner: "owner"})
	}
	apps = append(apps, domain.SparkApplication{ID: "own", Name: "own", Namespace: "spark", CreatedAt: stamp(time.Now()), State: "FAILED", WorkflowTaskID: run.Tasks[0].ID}, domain.SparkApplication{ID: "unknown", Name: "unknown", Namespace: "spark", CreatedAt: stamp(time.Now()), State: "RUNNING", WorkflowTaskID: "unrecognized-task"})
	if err = s.store.CollectApplicationNamespace(ctx, "spark", func(context.Context) ([]domain.SparkApplication, error) { return apps, nil }); err != nil {
		t.Fatal(err)
	}
	query := domain.ApplicationQuery{Namespaces: []string{"spark"}, Page: 2, PageSize: 5, Sort: "name", Direction: "asc"}
	page, err := s.store.QueryLiveApplications(ctx, query)
	if err != nil || page.Total != 13 || len(page.Items) != 5 || page.Items[0].Name != "job-05" {
		t.Fatal(page, err)
	}
	other := domain.WithTenant(context.Background(), "default")
	page, err = s.store.QueryLiveApplications(other, query)
	if err != nil || page.Total != 12 {
		t.Fatal("pending task leaked across tenants", page, err)
	}
	query.Keyword = "%"
	page, err = s.store.QueryLiveApplications(ctx, query)
	if err != nil || page.Total != 0 {
		t.Fatal("keyword interpreted as wildcard", page, err)
	}
	query.Keyword = ""
	query.Favorite = true
	query.UserID = user.ID
	if err = s.store.SetFavorite(ctx, user.ID, "spark", "job-00", true); err != nil {
		t.Fatal(err)
	}
	query.Page = 1
	page, err = s.store.QueryLiveApplications(ctx, query)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE application_collections SET collected_at=now()-interval '2 minutes' WHERE namespace='spark'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.QueryLiveApplications(ctx, query); err == nil {
		t.Fatal("stale index silently served")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE application_collections SET expires_at=now()+interval '1 minute' WHERE namespace='spark'`); err != nil {
		t.Fatal(err)
	}
	called := false
	if err = s.store.CollectApplicationNamespace(ctx, "spark", func(context.Context) ([]domain.SparkApplication, error) { called = true; return nil, nil }); err != nil || called {
		t.Fatal("competing collector entered", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE application_collections SET expires_at=now()-interval '1 minute' WHERE namespace='spark'`); err != nil {
		t.Fatal(err)
	}
	if err = s.store.CollectApplicationNamespace(ctx, "spark", func(c context.Context) ([]domain.SparkApplication, error) {
		_, e := s.pool.Exec(c, `UPDATE application_collections SET owner='replacement' WHERE namespace='spark'`)
		return apps, e
	}); err == nil {
		t.Fatal("expired collector committed snapshot")
	}
}
