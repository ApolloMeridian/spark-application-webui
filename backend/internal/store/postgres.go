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

func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *PostgresStore) Close()                         { s.pool.Close() }
