package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type PolicyUser struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Role           string `json:"role"`
	TenantID       string `json:"tenantId"`
	TenantName     string `json:"tenantName"`
	StorageEnabled bool   `json:"storageEnabled"`
}

func (s *Store) PolicyUsers(ctx context.Context) ([]PolicyUser, error) {
	rows, err := s.Pool.Query(ctx, `SELECT u.id,u.username,u.role,u.tenant_id,t.name,(t.permissions IS NULL OR COALESCE((t.permissions->>'objectStorage')::boolean,false)) FROM local_users u JOIN tenants t ON t.id=u.tenant_id WHERE NOT u.disabled ORDER BY u.username LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []PolicyUser{}
	for rows.Next() {
		var u PolicyUser
		if err = rows.Scan(&u.ID, &u.Username, &u.Role, &u.TenantID, &u.TenantName, &u.StorageEnabled); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// Resolve by immutable user ID, never by a caller-supplied target tenant.
func (s *Store) PolicyTarget(ctx context.Context, id string) (Identity, error) {
	var u Identity
	var permitted bool
	err := s.Pool.QueryRow(ctx, `SELECT u.id,u.tenant_id,u.role,u.username,(t.permissions IS NULL OR COALESCE((t.permissions->>'objectStorage')::boolean,false)) FROM local_users u JOIN tenants t ON t.id=u.tenant_id WHERE u.id=$1 AND NOT u.disabled`, id).Scan(&u.ID, &u.Tenant, &u.Role, &u.Username, &permitted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !permitted) {
		return u, fail(400, "invalid_user")
	}
	return u, err
}

func grantScope(g Grant) string {
	raw, _ := json.Marshal([]string{g.UserID, g.Tenant, g.Connection, g.Bucket, g.Prefix})
	return string(raw)
}

func canDelegate(u Identity, grants []Grant, g Grant) bool {
	if u.Role != "admin" || !ValidPrefix(g.Prefix) || len(g.Actions) == 0 {
		return false
	}
	for _, a := range g.Actions {
		if !contains(Actions, a) || !Allows(u, grants, g.Connection, g.Bucket, g.Prefix, a) {
			return false
		}
	}
	return true
}

// Recheck identities and authority inside the write transaction. Tenant changes,
// account/tenant revocation and concurrent updates cannot misattribute the grant.
func (s *Store) ChangeGrant(ctx context.Context, actor Identity, g Grant, remove bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var u Identity
	var permitted bool
	err = tx.QueryRow(ctx, `SELECT u.id,u.tenant_id,u.role,u.username,(t.permissions IS NULL OR COALESCE((t.permissions->>'objectStorage')::boolean,false)) FROM local_users u JOIN tenants t ON t.id=u.tenant_id WHERE u.id=$1 AND u.tenant_id=$2 AND NOT u.disabled FOR SHARE OF u,t`, actor.ID, actor.Tenant).Scan(&u.ID, &u.Tenant, &u.Role, &u.Username, &permitted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!permitted || u.Role != "admin")) {
		return fail(403, "access_denied")
	}
	if err != nil {
		return err
	}
	if !remove {
		var tenant string
		err = tx.QueryRow(ctx, `SELECT u.tenant_id,(t.permissions IS NULL OR COALESCE((t.permissions->>'objectStorage')::boolean,false)) FROM local_users u JOIN tenants t ON t.id=u.tenant_id WHERE u.id=$1 AND NOT u.disabled FOR SHARE OF u,t`, g.UserID).Scan(&tenant, &permitted)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!permitted || tenant != g.Tenant)) {
			return fail(400, "invalid_user")
		}
		if err != nil {
			return err
		}
	}
	// Serialize even absent rows so concurrent inserts cannot overwrite a broader grant.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,15001502))`, grantScope(g)); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,user_id,tenant_id,connection_id,bucket,prefix,actions FROM object_storage_grants WHERE user_id=$1 AND tenant_id=$2 ORDER BY id FOR SHARE`, u.ID, u.Tenant)
	if err != nil {
		return err
	}
	owned := []Grant{}
	for rows.Next() {
		var x Grant
		if err = rows.Scan(&x.ID, &x.UserID, &x.Tenant, &x.Connection, &x.Bucket, &x.Prefix, &x.Actions); err != nil {
			rows.Close()
			return err
		}
		owned = append(owned, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !canDelegate(u, owned, g) {
		return fail(403, "access_denied")
	}
	var old Grant
	if remove {
		err = tx.QueryRow(ctx, `SELECT id,user_id,tenant_id,connection_id,bucket,prefix,actions FROM object_storage_grants WHERE id=$1 FOR UPDATE`, g.ID).Scan(&old.ID, &old.UserID, &old.Tenant, &old.Connection, &old.Bucket, &old.Prefix, &old.Actions)
	} else {
		err = tx.QueryRow(ctx, `SELECT id,user_id,tenant_id,connection_id,bucket,prefix,actions FROM object_storage_grants WHERE user_id=$1 AND tenant_id=$2 AND connection_id=$3 AND bucket=$4 AND prefix=$5 FOR UPDATE`, g.UserID, g.Tenant, g.Connection, g.Bucket, g.Prefix).Scan(&old.ID, &old.UserID, &old.Tenant, &old.Connection, &old.Bucket, &old.Prefix, &old.Actions)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && (!canDelegate(u, owned, old) || grantScope(old) != grantScope(g)) {
		return fail(403, "access_denied")
	}
	if remove {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(404, "grant_not_found")
		}
		_, err = tx.Exec(ctx, `DELETE FROM object_storage_grants WHERE id=$1 AND tenant_id=$2`, old.ID, old.Tenant)
	} else {
		if g.ID == "" {
			g.ID = newID()
		}
		_, err = tx.Exec(ctx, `INSERT INTO object_storage_grants(id,user_id,tenant_id,connection_id,bucket,prefix,actions) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,tenant_id,connection_id,bucket,prefix) DO UPDATE SET actions=EXCLUDED.actions,updated_at=now()`, g.ID, g.UserID, g.Tenant, g.Connection, g.Bucket, g.Prefix, g.Actions)
	}
	if err != nil {
		return err
	}
	// Audit actor ownership is preserved; bounded target metadata identifies the recipient tenant.
	target, _ := json.Marshal(map[string]any{"userId": g.UserID, "tenantId": g.Tenant, "actions": g.Actions, "revoked": remove})
	_, err = tx.Exec(ctx, `INSERT INTO object_storage_operations(id,user_id,tenant_id,username,action,connection_id,bucket,object_key,target_key,result,finished_at) VALUES($1,$2,$3,$4,'POLICY_CHANGE',$5,$6,$7,$8,'succeeded',now())`, newID(), u.ID, u.Tenant, u.Username, g.Connection, g.Bucket, g.Prefix, string(target))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
