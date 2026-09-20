package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"spark-control-center/backend/internal/domain"
)

type AuditStore interface {
	Insert(context.Context, domain.OperationAudit) error
	List(context.Context, int) ([]domain.OperationAudit, error)
	UpsertApplications(context.Context, []domain.SparkApplication) error
	HistorySummary(context.Context, time.Time, time.Time) (domain.HistorySummary, error)
	Ping(context.Context) error
	Close()
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string, maxConnections int32) (*PostgresStore, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = maxConnections
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	statements := []string{`
CREATE TABLE IF NOT EXISTS operation_audit (
  id text PRIMARY KEY,
  application_name text NOT NULL,
  namespace text NOT NULL,
  operator_name text NOT NULL,
  operation text NOT NULL,
  reason text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  result text NOT NULL CHECK (result IN ('SUCCESS', 'FAILED')),
	  message text NOT NULL
	)`,
		`CREATE INDEX IF NOT EXISTS operation_audit_occurred_at_idx ON operation_audit (occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS operation_audit_application_idx ON operation_audit (namespace, application_name, occurred_at DESC)`,
		`CREATE TABLE IF NOT EXISTS spark_application_history (
  application_id text PRIMARY KEY,
  cluster_name text NOT NULL,
  namespace text NOT NULL,
  application_name text NOT NULL,
  owner_name text NOT NULL DEFAULT '',
  state text NOT NULL,
  created_at timestamptz NOT NULL,
  started_at timestamptz,
  finished_at timestamptz,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
)`,
		`CREATE INDEX IF NOT EXISTS spark_application_history_created_at_idx ON spark_application_history (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS spark_application_history_finished_at_idx ON spark_application_history (finished_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate audit schema: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) Insert(ctx context.Context, audit domain.OperationAudit) error {
	when, err := time.Parse(time.RFC3339Nano, audit.Timestamp)
	if err != nil {
		return fmt.Errorf("parse audit timestamp: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO operation_audit
  (id, application_name, namespace, operator_name, operation, reason, occurred_at, result, message)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		audit.ID, audit.ApplicationName, audit.Namespace, audit.Operator, audit.Operation,
		audit.Reason, when, audit.Result, audit.Message)
	if err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, limit int) ([]domain.OperationAudit, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, application_name, namespace, operator_name, operation, reason, occurred_at, result, message
FROM operation_audit ORDER BY occurred_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit records: %w", err)
	}
	defer rows.Close()
	result := make([]domain.OperationAudit, 0)
	for rows.Next() {
		var audit domain.OperationAudit
		var when time.Time
		if err := rows.Scan(&audit.ID, &audit.ApplicationName, &audit.Namespace, &audit.Operator,
			&audit.Operation, &audit.Reason, &when, &audit.Result, &audit.Message); err != nil {
			return nil, fmt.Errorf("scan audit record: %w", err)
		}
		audit.Timestamp = when.UTC().Format(time.RFC3339Nano)
		result = append(result, audit)
	}
	return result, rows.Err()
}

func (s *PostgresStore) UpsertApplications(ctx context.Context, applications []domain.SparkApplication) error {
	if len(applications) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin application history transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, app := range applications {
		createdAt, err := time.Parse(time.RFC3339, app.CreatedAt)
		if err != nil {
			return fmt.Errorf("parse created time for %s/%s: %w", app.Namespace, app.Name, err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO spark_application_history
  (application_id, cluster_name, namespace, application_name, owner_name, state, created_at, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (application_id) DO UPDATE SET
  cluster_name=EXCLUDED.cluster_name,
  namespace=EXCLUDED.namespace,
  application_name=EXCLUDED.application_name,
  owner_name=EXCLUDED.owner_name,
  state=EXCLUDED.state,
  created_at=EXCLUDED.created_at,
  started_at=COALESCE(EXCLUDED.started_at, spark_application_history.started_at),
  finished_at=COALESCE(EXCLUDED.finished_at, spark_application_history.finished_at),
  last_seen_at=now()`,
			app.ID, app.Cluster, app.Namespace, app.Name, app.Owner, app.State, createdAt,
			optionalTime(app.StartedAt), optionalTime(app.FinishedAt))
		if err != nil {
			return fmt.Errorf("upsert application history for %s/%s: %w", app.Namespace, app.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit application history transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) HistorySummary(ctx context.Context, from, to time.Time) (domain.HistorySummary, error) {
	result := domain.HistorySummary{From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339)}
	err := s.pool.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE created_at >= $1 AND created_at <= $2),
  count(*) FILTER (
    WHERE state IN ('FAILED', 'SUBMISSION_FAILED')
      AND COALESCE(finished_at, last_seen_at) >= $1
      AND COALESCE(finished_at, last_seen_at) <= $2
  )
FROM spark_application_history`, from, to).Scan(&result.Submitted, &result.Failed)
	if err != nil {
		return domain.HistorySummary{}, fmt.Errorf("query application history summary: %w", err)
	}
	return result, nil
}

func optionalTime(value string) any {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return parsed
}

func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *PostgresStore) Close()                         { s.pool.Close() }
