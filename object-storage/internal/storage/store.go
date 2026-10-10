package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }
type Operation struct {
	ID         string    `json:"id"`
	User       string    `json:"user"`
	Action     string    `json:"action"`
	Connection string    `json:"connection"`
	Bucket     string    `json:"bucket"`
	Key        string    `json:"key"`
	Target     string    `json:"target"`
	Result     string    `json:"result"`
	Created    time.Time `json:"created"`
}

func newID() string {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		panic(e)
	}
	return hex.EncodeToString(raw)
}
func OpenStore(ctx context.Context, dsn string) (*Store, error) {
	config, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, e
	}
	config.MaxConns = 12
	config.MinConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		return nil, e
	}
	s := &Store{pool}
	if e = s.Migrate(ctx); e != nil {
		pool.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(15001500)`)
	if e != nil {
		return e
	}
	// Reuse core identities, but never alter or migrate the core schema here.
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT to_regclass('local_users') IS NOT NULL`).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		return fmt.Errorf("initialize core Spark Console database before starting storage plugin")
	}
	_, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS object_storage_grants (
 id text PRIMARY KEY,user_id text NOT NULL REFERENCES local_users(id) ON DELETE CASCADE,
 tenant_id text NOT NULL,connection_id text NOT NULL,bucket text NOT NULL,prefix text NOT NULL,actions text[] NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(user_id,tenant_id,connection_id,bucket,prefix));
 CREATE INDEX IF NOT EXISTS storage_grants_user_idx ON object_storage_grants(user_id,tenant_id);
 CREATE TABLE IF NOT EXISTS object_storage_operations (
 id text PRIMARY KEY,user_id text NOT NULL,tenant_id text NOT NULL,username text NOT NULL,action text NOT NULL,
 connection_id text NOT NULL,bucket text NOT NULL,object_key text NOT NULL,target_key text NOT NULL DEFAULT '',
 result text NOT NULL DEFAULT 'pending',created_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz);
 CREATE INDEX IF NOT EXISTS storage_operations_retention_idx ON object_storage_operations(created_at);
 CREATE INDEX IF NOT EXISTS storage_operations_tenant_idx ON object_storage_operations(tenant_id,created_at DESC);
 CREATE TABLE IF NOT EXISTS object_storage_bootstrap (id text PRIMARY KEY);`)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Identity(ctx context.Context, id, tenant string) (Identity, error) {
	var u Identity
	var permitted bool
	e := s.Pool.QueryRow(ctx, `SELECT u.id,u.tenant_id,u.role,u.username,(t.permissions IS NULL OR COALESCE((t.permissions->>'objectStorage')::boolean,false)) FROM local_users u JOIN tenants t ON t.id=u.tenant_id WHERE u.id=$1 AND u.tenant_id=$2 AND NOT u.disabled`, id, tenant).Scan(&u.ID, &u.Tenant, &u.Role, &u.Username, &permitted)
	if errors.Is(e, pgx.ErrNoRows) {
		return u, fail(401, "inactive_account")
	}
	if e == nil && !permitted {
		return u, fail(403, "access_denied")
	}
	return u, e
}
func (s *Store) Grants(ctx context.Context, u Identity, all bool) ([]Grant, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,user_id,tenant_id,connection_id,bucket,prefix,actions FROM object_storage_grants WHERE tenant_id=$1 AND ($2 OR user_id=$3) ORDER BY connection_id,bucket,prefix,id LIMIT 2000`, u.Tenant, all, u.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []Grant{}
	for rows.Next() {
		var g Grant
		if e = rows.Scan(&g.ID, &g.UserID, &g.Tenant, &g.Connection, &g.Bucket, &g.Prefix, &g.Actions); e != nil {
			return nil, e
		}
		result = append(result, g)
	}
	return result, rows.Err()
}
func (s *Store) Begin(ctx context.Context, u Identity, action, c, b, k, target string) (string, error) {
	id := newID()
	_, e := s.Pool.Exec(ctx, `INSERT INTO object_storage_operations(id,user_id,tenant_id,username,action,connection_id,bucket,object_key,target_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, u.ID, u.Tenant, u.Username, action, c, b, k, target)
	return id, e
}
func (s *Store) Finish(ctx context.Context, id, result string) error {
	_, e := s.Pool.Exec(ctx, `UPDATE object_storage_operations SET result=$2,finished_at=now() WHERE id=$1 AND result='pending'`, id, result)
	return e
}
func (s *Store) Lease(ctx context.Context, u Identity, id string) (func(), error) {
	conn, e := s.Pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,15001501))`, id).Scan(&locked); e != nil || !locked {
		conn.Release()
		if e != nil {
			return nil, e
		}
		return nil, fail(409, "upload_busy")
	}
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, e := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,15001501))`, id)
		if e != nil {
			_ = conn.Conn().Close(ctx)
		}
		conn.Release()
	}
	var result string
	e = conn.QueryRow(ctx, `SELECT result FROM object_storage_operations WHERE id=$1 AND user_id=$2 AND tenant_id=$3`, id, u.ID, u.Tenant).Scan(&result)
	if e != nil || result != "pending" {
		release()
		return nil, fail(409, "upload_not_pending")
	}
	return release, nil
}
func (s *Store) ChangeGrant(ctx context.Context, u Identity, g Grant, remove bool) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	raw, _ := json.Marshal(g.Actions)
	if remove {
		_, e = tx.Exec(ctx, `DELETE FROM object_storage_grants WHERE id=$1 AND tenant_id=$2`, g.ID, u.Tenant)
	} else {
		if g.ID == "" {
			g.ID = newID()
		}
		_, e = tx.Exec(ctx, `INSERT INTO object_storage_grants(id,user_id,tenant_id,connection_id,bucket,prefix,actions) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,tenant_id,connection_id,bucket,prefix) DO UPDATE SET actions=EXCLUDED.actions,updated_at=now()`, g.ID, g.UserID, u.Tenant, g.Connection, g.Bucket, g.Prefix, g.Actions)
	}
	if e != nil {
		return e
	}
	// bounded metadata, not object content; target is the affected account.
	_, e = tx.Exec(ctx, `INSERT INTO object_storage_operations(id,user_id,tenant_id,username,action,connection_id,bucket,object_key,target_key,result,finished_at) VALUES($1,$2,$3,$4,'POLICY_CHANGE',$5,$6,$7,$8,'succeeded',now())`, newID(), u.ID, u.Tenant, u.Username, g.Connection, g.Bucket, g.Prefix, g.UserID+":"+string(raw))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Bootstrap(ctx context.Context, items []Bootstrap) error {
	for _, x := range items {
		if !ValidPrefix(x.Prefix) || len(x.Actions) == 0 || x.Tenant == "" {
			return fmt.Errorf("invalid bootstrap grant")
		}
		for _, a := range x.Actions {
			if !contains(Actions, a) {
				return fmt.Errorf("invalid bootstrap action")
			}
		}
		// One-time inserts only: revoked grants are not recreated on Pod restart.
		tx, e := s.Pool.Begin(ctx)
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(x)
		key := string(raw)
		tag, e := tx.Exec(ctx, `INSERT INTO object_storage_bootstrap(id) VALUES($1) ON CONFLICT DO NOTHING`, key)
		if e == nil && tag.RowsAffected() > 0 {
			var u Identity
			e = tx.QueryRow(ctx, `SELECT id,tenant_id,role,username FROM local_users WHERE username=$1 AND tenant_id=$2 AND role='admin' AND NOT disabled`, x.Username, x.Tenant).Scan(&u.ID, &u.Tenant, &u.Role, &u.Username)
			// OIDC accounts may not exist at initial startup. Retry on later scans.
			if errors.Is(e, pgx.ErrNoRows) {
				tx.Rollback(ctx)
				continue
			}
			if e == nil {
				_, e = tx.Exec(ctx, `INSERT INTO object_storage_grants(id,user_id,tenant_id,connection_id,bucket,prefix,actions) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, newID(), u.ID, u.Tenant, x.Connection, x.Bucket, x.Prefix, x.Actions)
			}
			if e == nil {
				_, e = tx.Exec(ctx, `INSERT INTO object_storage_operations(id,user_id,tenant_id,username,action,connection_id,bucket,object_key,result,finished_at) VALUES($1,$2,$3,'bootstrap','POLICY_CHANGE',$4,$5,$6,'succeeded',now())`, newID(), u.ID, u.Tenant, x.Connection, x.Bucket, x.Prefix)
			}
		}
		if e != nil {
			tx.Rollback(ctx)
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Clean(ctx context.Context, days int) error {
	_, e := s.Pool.Exec(ctx, `WITH old AS (SELECT id FROM object_storage_operations WHERE created_at<now()-($1 * interval '1 day') AND result<>'pending' ORDER BY created_at LIMIT 1000) DELETE FROM object_storage_operations WHERE id IN(SELECT id FROM old)`, days)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, `WITH stale AS (SELECT id FROM object_storage_operations WHERE result='pending' AND created_at<now()-interval '25 hours' ORDER BY created_at LIMIT 1000) UPDATE object_storage_operations SET result='unknown',finished_at=now() WHERE id IN(SELECT id FROM stale)`)
	return e
}
func (s *Store) Operations(ctx context.Context, u Identity) ([]Operation, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,username,action,connection_id,bucket,object_key,target_key,result,created_at FROM object_storage_operations WHERE tenant_id=$1 AND ($2='admin' OR user_id=$3) ORDER BY created_at DESC LIMIT 200`, u.Tenant, u.Role, u.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var x Operation
		if e = rows.Scan(&x.ID, &x.User, &x.Action, &x.Connection, &x.Bucket, &x.Key, &x.Target, &x.Result, &x.Created); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func contains(items []string, x string) bool {
	for _, a := range items {
		if a == x {
			return true
		}
	}
	return false
}
