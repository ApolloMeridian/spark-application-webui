package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBootstrapActionChangesDoNotAuditNoopsOrRestoreRevokedRights(t *testing.T) {
	s := dbFixture(t)
	ctx := context.Background()
	initial := Bootstrap{Username: "admin", Tenant: "default", Connection: "demo", Bucket: "bucket", Prefix: "team/a/", Actions: []string{"list", "read"}}
	if err := s.Bootstrap(ctx, []Bootstrap{initial}); err != nil {
		t.Fatal(err)
	}
	changed := initial
	changed.Actions = []string{"list", "read", "delete"}
	if err := s.Bootstrap(ctx, []Bootstrap{changed}); err != nil {
		t.Fatal(err)
	}
	u := Identity{ID: "admin", Tenant: "default", Role: "admin", Username: "admin"}
	grants, err := s.Grants(ctx, u, false)
	if err != nil || len(grants) != 1 || contains(grants[0].Actions, "delete") {
		t.Fatal("bootstrap broadened existing actions", grants, err)
	}
	operations, err := s.Operations(ctx, u)
	if err != nil || len(operations) != 1 {
		t.Fatal("no-op bootstrap created misleading audit", operations, err)
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM object_storage_grants;DELETE FROM object_storage_bootstrap`); err != nil {
		t.Fatal(err)
	}
	// Legacy JSON markers must retain their revocation semantics on upgrade too.
	legacy, _ := json.Marshal(initial)
	if _, err = s.Pool.Exec(ctx, `INSERT INTO object_storage_bootstrap(id) VALUES($1)`, string(legacy)); err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(ctx, []Bootstrap{changed}); err != nil {
		t.Fatal(err)
	}
	grants, err = s.Grants(ctx, u, false)
	if err != nil || len(grants) != 0 {
		t.Fatal("changed legacy bootstrap restored a revoked grant", grants, err)
	}
}
