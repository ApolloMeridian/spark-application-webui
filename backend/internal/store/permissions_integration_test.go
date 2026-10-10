package store

import (
	"context"
	"os"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"testing"
	"time"
)

func TestPostgresTenantPolicyLiveSessionAndNamespaceDeny(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pg, err := Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	id := "acl-" + time.Now().UTC().Format("20060102150405.000000000")
	if _, err = pg.pool.Exec(ctx, `INSERT INTO tenants(id,name,permissions) VALUES($1,$1,'{"allNamespaces":false,"namespaces":["spark"],"kill":true,"workflowsRead":true,"workflowsWrite":true}')`, id); err != nil {
		t.Fatal(err)
	}
	defer pg.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, id)
	auth := localauth.New(pg, time.Hour, 4)
	u, err := auth.CreateUser(ctx, domain.User{Role: domain.RoleAdmin}, localauth.CreateUserInput{Username: id, Password: "Test-password-123", Role: domain.RoleViewer, TenantID: id})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.pool.Exec(context.Background(), `DELETE FROM local_users WHERE id=$1`, u.ID)
	_, token, _, err := auth.Login(ctx, id, "Test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := auth.Authenticate(ctx, token)
	if err != nil || !user.Can("kill") || len(user.Namespaces) != 1 || user.Namespaces[0] != "spark" {
		t.Fatal(user, err)
	}
	if _, err = pg.pool.Exec(ctx, `UPDATE tenants SET permissions='{"allNamespaces":false,"namespaces":[],"kill":false}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	user, _, err = auth.Authenticate(ctx, token)
	if err != nil || user.Can("kill") || len(user.Namespaces) != 1 || user.Namespaces[0] != "__no_namespaces__" {
		t.Fatal("revocation / deny-all did not reach existing session", user, err)
	}
	for _, x := range []struct {
		role domain.UserRole
		want string
	}{{domain.RoleAdmin, "admin"}, {domain.RoleViewer, "viewer"}} {
		candidate := domain.User{ID: id + string(x.role), Username: id + string(x.role), Role: x.role, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		created, e := pg.UpsertOIDCUser(ctx, candidate, candidate.Username, "https://issuer.example.invalid", candidate.ID, true)
		defer pg.pool.Exec(context.Background(), `DELETE FROM local_users WHERE id=$1`, candidate.ID)
		if e != nil || created.TenantID != x.want {
			t.Fatal("OIDC tenant default", created, e)
		}
		if x.role == domain.RoleAdmin {
			candidate.Role = domain.RoleViewer
			demoted, e := pg.UpsertOIDCUser(ctx, candidate, candidate.Username, "https://issuer.example.invalid", candidate.ID, true)
			if e != nil || demoted.TenantID != "viewer" {
				t.Fatal("removed OIDC admin group retained default admin privileges", demoted, e)
			}
		}
	}
}
