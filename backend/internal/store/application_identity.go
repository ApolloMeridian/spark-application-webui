package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
	"time"
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
