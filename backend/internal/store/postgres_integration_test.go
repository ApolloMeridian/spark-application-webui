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
