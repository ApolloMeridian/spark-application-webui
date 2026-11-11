package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

func (s *PostgresStore) GetApplicationSnapshotByID(ctx context.Context, ns, id string) (domain.SparkApplication, error) {
	var raw []byte
	var seen time.Time
	err := s.pool.QueryRow(ctx, `SELECT snapshot,last_seen_at FROM spark_application_history WHERE namespace=$1 AND application_id=$2 AND snapshot<>'{}'::jsonb AND ($3='' OR tenant_id='' OR tenant_id=$3)`, ns, id, domain.TenantScope(ctx)).Scan(&raw, &seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SparkApplication{}, ErrSnapshotNotFound
	}
	if err != nil {
		return domain.SparkApplication{}, err
	}
	var app domain.SparkApplication
	if err = json.Unmarshal(raw, &app); err != nil {
		return app, err
	}
	app.Historical = true
	app.LastObservedAt = seen.UTC().Format(time.RFC3339Nano)
	return app, nil
}

// Retain a tombstone for the immutable UID so an in-flight observation cannot
// restore the deleted snapshot. The audit and removal commit together.
func (s *PostgresStore) PurgeApplicationSnapshot(ctx context.Context, id string, audit domain.OperationAudit) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
UPDATE spark_application_history
SET purged_at=now(), snapshot='{}'::jsonb, diagnosis='[]'::jsonb
WHERE namespace=$1 AND application_id=$2 AND purged_at IS NULL AND snapshot<>'{}'::jsonb
  AND ($3='' OR tenant_id='' OR tenant_id=$3)
RETURNING application_name, COALESCE(NULLIF(tenant_id,''),$3)`, audit.Namespace, id, domain.TenantScope(ctx)).Scan(&audit.ApplicationName, &audit.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSnapshotNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM live_applications WHERE namespace=$1 AND application_id=$2`, audit.Namespace, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM application_favorites WHERE namespace=$1 AND application_name=$2`, audit.Namespace, id); err != nil {
		return err
	}
	if err := insertOperationAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
