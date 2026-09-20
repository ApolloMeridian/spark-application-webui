package store

import (
	"context"
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
	summary, err := store.HistorySummary(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Submitted < 1 || summary.Failed < 1 {
		t.Fatalf("expected submitted and failed history counts, got %#v", summary)
	}
}
