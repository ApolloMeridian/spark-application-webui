package storage

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// All replicas sharing this schema share one budget. The database clock owns
// windows/expiry; no connection or transaction is held during provider I/O.
const budgetLeaseTTL = 15 * time.Second
const budgetRenewEvery = 5 * time.Second
const budgetSafety = 2 * time.Second

type budgetCancelKey struct{}
type budgetLease struct{ ID, Node string }

func migrateLimits(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS object_storage_limit_nodes (
 id text PRIMARY KEY,transfers integer NOT NULL CHECK(transfers>0),rpm integer NOT NULL CHECK(rpm>0),expires_at timestamptz NOT NULL);
 CREATE INDEX IF NOT EXISTS storage_limit_nodes_expiry_idx ON object_storage_limit_nodes(expires_at);
 CREATE TABLE IF NOT EXISTS object_storage_budget_leases (
 id text PRIMARY KEY,node_id text NOT NULL,kind text NOT NULL CHECK(kind IN('request','transfer','upload')),
 resource text NOT NULL DEFAULT '',expires_at timestamptz NOT NULL);
 CREATE INDEX IF NOT EXISTS storage_budget_expiry_idx ON object_storage_budget_leases(expires_at);
 CREATE UNIQUE INDEX IF NOT EXISTS storage_budget_upload_idx ON object_storage_budget_leases(resource) WHERE kind='upload';
 CREATE TABLE IF NOT EXISTS object_storage_rate_windows (
 user_id text NOT NULL,tenant_id text NOT NULL,window_start timestamptz NOT NULL,count integer NOT NULL CHECK(count>0),
 PRIMARY KEY(user_id,tenant_id,window_start));
 CREATE INDEX IF NOT EXISTS storage_rate_window_expiry_idx ON object_storage_rate_windows(window_start);`)
	return err
}

func (s *Store) reserveBudget(ctx context.Context, node string, c Config, user Identity, kind, resource string) (budgetLease, error) {
	lease := budgetLease{ID: newID(), Node: node}
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(step)
	if err != nil {
		return lease, err
	}
	defer tx.Rollback(step)
	// Only short quota decisions serialize. There are no network/provider calls here.
	if _, err = tx.Exec(step, `SELECT pg_advisory_xact_lock(15001504)`); err != nil {
		return lease, err
	}
	if _, err = tx.Exec(step, `DELETE FROM object_storage_budget_leases WHERE expires_at<=clock_timestamp();
 DELETE FROM object_storage_limit_nodes WHERE expires_at<=clock_timestamp();
 DELETE FROM object_storage_rate_windows WHERE window_start<date_trunc('minute',clock_timestamp())-interval '2 minutes'`); err != nil {
		return lease, err
	}
	if _, err = tx.Exec(step, `INSERT INTO object_storage_limit_nodes(id,transfers,rpm,expires_at) VALUES($1,$2,$3,clock_timestamp()+interval '30 seconds')
 ON CONFLICT(id) DO UPDATE SET transfers=EXCLUDED.transfers,rpm=EXCLUDED.rpm,expires_at=EXCLUDED.expires_at`, node, c.Concurrent, c.RequestsPerMinute); err != nil {
		return lease, err
	}
	var capacity, rpm int
	if err = tx.QueryRow(step, `SELECT MIN(transfers),MIN(rpm) FROM object_storage_limit_nodes WHERE expires_at>clock_timestamp()`).Scan(&capacity, &rpm); err != nil {
		return lease, err
	}
	// A rolling deployment temporarily uses the smallest live configuration.
	deny := func(code string) (budgetLease, error) {
		if err := tx.Commit(step); err != nil {
			return lease, err
		}
		return lease, fail(429, code)
	}
	if kind == "upload" {
		var valid, busy bool
		if err = tx.QueryRow(step, `SELECT EXISTS(SELECT 1 FROM object_storage_operations WHERE id=$1 AND user_id=$2 AND tenant_id=$3 AND result='pending'),EXISTS(SELECT 1 FROM object_storage_budget_leases WHERE kind='upload' AND resource=$1)`, resource, user.ID, user.Tenant).Scan(&valid, &busy); err != nil {
			return lease, err
		}
		if !valid {
			return lease, fail(409, "upload_not_pending")
		}
		if busy {
			if err = tx.Commit(step); err != nil {
				return lease, err
			}
			return lease, fail(409, "upload_busy")
		}
	} else {
		var active int
		if kind != "request" && kind != "transfer" {
			return lease, fmt.Errorf("invalid budget kind")
		}
		if err = tx.QueryRow(step, `SELECT count(*) FROM object_storage_budget_leases WHERE kind=$1`, kind).Scan(&active); err != nil {
			return lease, err
		}
		if kind == "request" {
			capacity *= 4
		}
		if active >= capacity {
			return deny(kind + "_limit")
		}
	}
	if kind == "request" {
		var count int
		err = tx.QueryRow(step, `INSERT INTO object_storage_rate_windows(user_id,tenant_id,window_start,count)
 VALUES($1,$2,date_trunc('minute',clock_timestamp()),1) ON CONFLICT(user_id,tenant_id,window_start)
 DO UPDATE SET count=object_storage_rate_windows.count+1 WHERE object_storage_rate_windows.count<$3 RETURNING count`, user.ID, user.Tenant, rpm).Scan(&count)
		if err == pgx.ErrNoRows {
			return deny("rate_limit")
		}
		if err != nil {
			return lease, err
		}
	}
	if _, err = tx.Exec(step, `INSERT INTO object_storage_budget_leases(id,node_id,kind,resource,expires_at) VALUES($1,$2,$3,$4,clock_timestamp()+interval '15 seconds')`, lease.ID, node, kind, resource); err != nil {
		return lease, err
	}
	return lease, tx.Commit(step)
}

func (s *Store) renewBudget(ctx context.Context, lease budgetLease) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE object_storage_budget_leases SET expires_at=clock_timestamp()+interval '15 seconds' WHERE id=$1 AND node_id=$2 AND expires_at>clock_timestamp()`, lease.ID, lease.Node)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("storage lease expired or was revoked")
	}
	if _, err = tx.Exec(ctx, `UPDATE object_storage_limit_nodes SET expires_at=clock_timestamp()+interval '30 seconds' WHERE id=$1`, lease.Node); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) releaseBudget(ctx context.Context, lease budgetLease) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM object_storage_budget_leases WHERE id=$1 AND node_id=$2`, lease.ID, lease.Node)
	return err
}

func (s *Server) acquireBudget(r *http.Request, kind, resource string, user Identity) (func(), error) {
	cancel, _ := r.Context().Value(budgetCancelKey{}).(context.CancelFunc)
	if cancel == nil {
		return nil, fmt.Errorf("missing request cancellation")
	}
	started := time.Now()
	lease, err := s.Store.reserveBudget(r.Context(), s.nodeID, s.Config, user, kind, resource)
	if err != nil {
		return nil, err
	}
	return s.keepBudget(r.Context(), cancel, lease, started), nil
}

func (s *Server) keepBudget(ctx context.Context, cancel context.CancelFunc, lease budgetLease, started time.Time) func() {
	// Cancel before database expiry even when renewal is unavailable. The
	// request's body/socket deadlines interrupt stalled streaming I/O as well.
	watchdog := time.AfterFunc(time.Until(started.Add(budgetLeaseTTL-budgetSafety)), cancel)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(budgetRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				began := time.Now()
				step, end := context.WithTimeout(ctx, 3*time.Second)
				err := s.Store.renewBudget(step, lease)
				end()
				if err != nil {
					cancel()
					return
				}
				if ctx.Err() != nil {
					return
				}
				watchdog.Reset(time.Until(began.Add(budgetLeaseTTL - budgetSafety)))
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			watchdog.Stop()
			cleanup, end := context.WithTimeout(context.Background(), 3*time.Second)
			defer end()
			if err := s.Store.releaseBudget(cleanup, lease); err != nil {
				s.Logger.Warn("storage budget release deferred to expiry")
			}
		})
	}
}
