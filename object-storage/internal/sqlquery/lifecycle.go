package sqlquery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const lifecycleSchema = `
ALTER TABLE IF EXISTS operation_audit ADD COLUMN IF NOT EXISTS sql_details jsonb;
DO $$ BEGIN
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema()
		AND table_name='console_sql_queries' AND column_name='cleanup_after') THEN
		ALTER TABLE console_sql_queries ADD COLUMN cleanup_after timestamptz;
		UPDATE console_sql_queries SET cleanup_after=expires_at WHERE pages>0 AND catalog<>'';
	END IF;
END $$;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS cleanup_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS started_at timestamptz;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS parent_id uuid;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS ordinal integer NOT NULL DEFAULT 1;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS statement_count integer NOT NULL DEFAULT 1;
ALTER TABLE console_sql_queries ADD COLUMN IF NOT EXISTS policy_version text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS console_sql_steps_idx ON console_sql_queries(parent_id,ordinal) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS console_sql_cleanup_idx ON console_sql_queries(catalog,cleanup_after)
	WHERE cleanup_after IS NOT NULL;
CREATE INDEX IF NOT EXISTS console_sql_retention_idx ON console_sql_queries(created_at)
	WHERE state NOT IN ('queued','running') AND cleanup_after IS NULL;
`

func (s *Server) maintainHistory(ctx context.Context) {
	cleanup := time.NewTicker(5 * time.Second)
	retention := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			step, cancel := context.WithTimeout(ctx, 20*time.Second)
			dbError(s.cleanupResult(step))
			cancel()
		case <-retention.C:
			step, cancel := context.WithTimeout(ctx, 10*time.Second)
			dbError(s.pruneHistory(step))
			cancel()
		}
	}
}

func (s *Server) cleanupResult(ctx context.Context) error {
	var id string
	var attempts int
	var lease time.Time
	err := s.db.QueryRow(ctx, `WITH due AS (
		SELECT id FROM console_sql_queries WHERE catalog=$1 AND cleanup_after<=now()
		AND state NOT IN ('queued','running') ORDER BY cleanup_after LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE console_sql_queries q SET cleanup_after=now()+interval '1 minute'
	FROM due WHERE q.id=due.id RETURNING q.id::text,q.cleanup_attempts,q.cleanup_after`, s.cfg.catalogID()).Scan(&id, &attempts, &lease)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	done, cleanupErr := s.clients.deleteResult(ctx, id)
	var next *time.Time
	if !done || cleanupErr != nil {
		delay := 5 * time.Second
		if cleanupErr != nil {
			delay = min(10*time.Minute, delay<<min(attempts, 8))
			attempts = min(attempts+1, 8)
		} else {
			attempts = 0
		}
		at := time.Now().Add(delay)
		next = &at
	}
	_, err = s.db.Exec(ctx, `UPDATE console_sql_queries SET cleanup_after=$2,cleanup_attempts=$3
		WHERE id=$1 AND cleanup_after=$4`, id, next, attempts, lease)
	return errors.Join(err, cleanupErr)
}

func (s *Server) pruneHistory(ctx context.Context) error {
	_, textErr := s.db.Exec(ctx, `UPDATE console_sql_queries SET statement='' WHERE id IN (
		SELECT id FROM console_sql_queries WHERE created_at < now()-make_interval(secs=>$1)
		AND state NOT IN ('queued','running') AND cleanup_after IS NOT NULL AND statement<>'' ORDER BY created_at LIMIT 500
	)`, s.cfg.HistoryRetention.Seconds())
	_, historyErr := s.db.Exec(ctx, `DELETE FROM console_sql_queries WHERE id IN (
		SELECT id FROM console_sql_queries WHERE created_at < now()-make_interval(secs=>$1)
		AND state NOT IN ('queued','running') AND cleanup_after IS NULL ORDER BY created_at LIMIT 500
	)`, s.cfg.HistoryRetention.Seconds())
	_, auditErr := s.db.Exec(ctx, `DELETE FROM operation_audit WHERE id IN (
		SELECT id FROM operation_audit WHERE occurred_at < now()-make_interval(secs=>$1)
		AND starts_with(operation,'SQL_') ORDER BY occurred_at LIMIT 500
	)`, s.cfg.AuditRetention.Seconds())
	return errors.Join(textErr, historyErr, auditErr)
}
