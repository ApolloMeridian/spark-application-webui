package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
)

func TestPostgresUserTenantAuditIsReadableAndAtomic(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	name := "audit-" + time.Now().UTC().Format("20060102150405.000000000")
	actor := domain.User{ID: "audit-actor", Username: "audit-admin", Role: domain.RoleAdmin, TenantID: "admin"}
	auth := localauth.New(pg, time.Hour, 4)
	user, err := auth.CreateUser(ctx, actor, localauth.CreateUserInput{Username: name, Password: "Fixture-password-123", Role: domain.RoleViewer, TenantID: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.pool.Exec(context.Background(), `DELETE FROM local_users WHERE id=$1`, user.ID)
	defer pg.pool.Exec(context.Background(), `DELETE FROM operation_audit WHERE application_name=$1`, name)
	nextTenant, password := "default", "New-fixture-password-456"
	if _, err := auth.UpdateUser(ctx, actor, user.ID, localauth.UpdateUserInput{TenantID: &nextTenant, Password: &password}); err != nil {
		t.Fatal(err)
	}
	var operation, message, auditTenant, operator string
	if err := pg.pool.QueryRow(ctx, `SELECT operation,message,tenant_id,operator_name FROM operation_audit WHERE application_name=$1 AND operation='USER_TENANT_CHANGE'`, name).Scan(&operation, &message, &auditTenant, &operator); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{name, "userId=" + user.ID, "(viewer)", "(default)", " -> ", "password changed"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("audit missing %q: %s", expected, message)
		}
	}
	if auditTenant != "admin" || operator != actor.Username || strings.Contains(message, password) || strings.Contains(message, "$2a$") {
		t.Fatal("incorrect attribution or credential leak", message)
	}
	// Simulate unavailable audit persistence without affecting other fixtures.
	constraint := pgx.Identifier{"reject_" + strings.ReplaceAll(name, ".", "_")}.Sanitize()
	if _, err := pg.pool.Exec(ctx, `ALTER TABLE operation_audit ADD CONSTRAINT `+constraint+` CHECK (application_name <> '`+name+`' OR operation <> 'USER_TENANT_CHANGE') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer pg.pool.Exec(context.Background(), `ALTER TABLE operation_audit DROP CONSTRAINT `+constraint)
	nextTenant = "viewer"
	if _, err := auth.UpdateUser(ctx, actor, user.ID, localauth.UpdateUserInput{TenantID: &nextTenant}); err == nil {
		t.Fatal("user mutation succeeded despite failed audit")
	}
	saved, err := pg.FindUserByID(ctx, user.ID)
	if err != nil || saved.TenantID != "default" {
		t.Fatal("failed audit did not roll back tenant mutation", saved, err)
	}
	if _, err := pg.pool.Exec(ctx, `ALTER TABLE operation_audit DROP CONSTRAINT `+constraint); err != nil {
		t.Fatal(err)
	}
	if err := auth.DeleteUser(ctx, actor, user.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pg.pool.QueryRow(ctx, `SELECT count(*) FROM operation_audit WHERE application_name=$1 AND operation IN ('USER_CREATE','USER_TENANT_CHANGE','USER_DELETE')`, name).Scan(&count); err != nil || count != 3 {
		t.Fatal("expected one semantic audit per user mutation", count, err)
	}
}
