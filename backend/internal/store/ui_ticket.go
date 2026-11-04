package store

import (
	"context"
	"spark-control-center/backend/internal/domain"
)

func (s *PostgresStore) SaveSparkUITicket(ctx context.Context, t domain.SparkUITicket) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM spark_ui_tickets WHERE expires_at<=now()`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO spark_ui_tickets(token_hash,session_hash,namespace,application_name,tenant_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, t.TokenHash, t.SessionHash, t.Namespace, t.Application, t.TenantID, t.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) FindSparkUITicket(ctx context.Context, hash string) (domain.SparkUITicket, error) {
	var t domain.SparkUITicket
	err := s.pool.QueryRow(ctx, `SELECT token_hash,session_hash,namespace,application_name,tenant_id,expires_at FROM spark_ui_tickets WHERE token_hash=$1 AND expires_at>now()`, hash).Scan(&t.TokenHash, &t.SessionHash, &t.Namespace, &t.Application, &t.TenantID, &t.ExpiresAt)
	return t, err
}
