package store

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) migrateAuthBudget(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS authentication_rate_windows(key text PRIMARY KEY,window_start timestamptz NOT NULL,count integer NOT NULL);CREATE INDEX IF NOT EXISTS authentication_rate_expiry_idx ON authentication_rate_windows(window_start);`)
	return err
}

// Shared account/source windows use only hashes, a DB clock and short locks.
// Hashing passwords happens after committing this transaction.
func (s *PostgresStore) AllowAuthenticationAttempt(ctx context.Context, source, account string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(14001406)`); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM authentication_rate_windows WHERE window_start<date_trunc('minute',now())-interval '1 minute'`); err != nil {
		return false, err
	}
	var size int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM authentication_rate_windows`).Scan(&size); err != nil {
		return false, err
	}
	if size >= 20000 {
		return false, nil
	}
	keys := []string{"source:" + source, "account:" + account}
	limits := []int{120, 10}
	for i, key := range keys {
		var n int
		if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT count FROM authentication_rate_windows WHERE key=$1 AND window_start=date_trunc('minute',now())),0)`, key).Scan(&n); err != nil {
			return false, err
		}
		if n >= limits[i] {
			return false, nil
		}
	}
	for _, key := range keys {
		if _, err = tx.Exec(ctx, `INSERT INTO authentication_rate_windows(key,window_start,count) VALUES($1,date_trunc('minute',now()),1) ON CONFLICT(key) DO UPDATE SET count=CASE WHEN authentication_rate_windows.window_start=EXCLUDED.window_start THEN authentication_rate_windows.count+1 ELSE 1 END,window_start=EXCLUDED.window_start`, key); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
