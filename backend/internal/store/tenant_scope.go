package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

func (s *PostgresStore) migrateTenantScope(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
ALTER TABLE application_templates ADD COLUMN IF NOT EXISTS tenant_id text;
UPDATE application_templates t SET tenant_id=COALESCE((SELECT u.tenant_id FROM local_users u WHERE u.username=t.created_by LIMIT 1),'_legacy_unassigned') WHERE tenant_id IS NULL;
ALTER TABLE application_templates ALTER COLUMN tenant_id SET DEFAULT 'default';
ALTER TABLE application_templates ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE application_templates DROP CONSTRAINT IF EXISTS application_templates_namespace_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS application_templates_tenant_name_idx ON application_templates(tenant_id,namespace,name);
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS tenant_id text;
UPDATE alert_rules r SET tenant_id=COALESCE((SELECT u.tenant_id FROM local_users u WHERE u.username=r.created_by LIMIT 1),'_legacy_unassigned') WHERE tenant_id IS NULL;
ALTER TABLE alert_rules ALTER COLUMN tenant_id SET DEFAULT 'default';
ALTER TABLE alert_rules ALTER COLUMN tenant_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS alert_rules_tenant_idx ON alert_rules(tenant_id,updated_at DESC);
ALTER TABLE application_alerts ADD COLUMN IF NOT EXISTS tenant_id text;
UPDATE application_alerts a SET tenant_id=COALESCE((SELECT r.tenant_id FROM alert_rules r WHERE r.id=a.rule_id),'_legacy_unassigned') WHERE tenant_id IS NULL;
ALTER TABLE application_alerts ALTER COLUMN tenant_id SET DEFAULT 'default';
ALTER TABLE application_alerts ALTER COLUMN tenant_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS application_alerts_tenant_idx ON application_alerts(tenant_id,last_seen_at DESC);
CREATE TABLE IF NOT EXISTS alert_evaluation_lease(id boolean PRIMARY KEY DEFAULT true,owner text NOT NULL DEFAULT '',expires_at timestamptz NOT NULL DEFAULT '-infinity',last_completed_at timestamptz NOT NULL DEFAULT '-infinity',CHECK(id));
INSERT INTO alert_evaluation_lease(id) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS spark_ui_tickets(token_hash text PRIMARY KEY,session_hash text NOT NULL REFERENCES local_user_sessions(token_hash) ON DELETE CASCADE,namespace text NOT NULL,application_name text NOT NULL,tenant_id text NOT NULL,expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS spark_ui_tickets_expiry_idx ON spark_ui_tickets(expires_at);
CREATE INDEX IF NOT EXISTS spark_ui_tickets_session_idx ON spark_ui_tickets(session_hash);
ALTER TABLE spark_application_history ADD COLUMN IF NOT EXISTS tenant_id text;
CREATE INDEX IF NOT EXISTS spark_history_tenant_idx ON spark_application_history(tenant_id,created_at DESC);
`)
	if err != nil {
		return err
	}
	if err = s.migrateAlertOutbox(ctx, tx); err != nil {
		return err
	}
	if err = s.migrateLiveApplications(ctx, tx); err != nil {
		return err
	}
	return s.migrateAuthBudget(ctx, tx)
}

// Batch lookup: external jobs without a successful workflow binding remain
// namespace scoped. Failed/queued task attempts never claim a resource.
func (s *PostgresStore) FilterTenantApplications(ctx context.Context, apps []domain.SparkApplication) ([]domain.SparkApplication, error) {
	tenant := domain.TenantScope(ctx)
	if tenant == "" || len(apps) == 0 {
		return apps, nil
	}
	namespaces := []string{}
	taskIDs := []string{}
	seen := map[string]bool{}
	for _, app := range apps {
		if app.WorkflowTaskID != "" {
			taskIDs = append(taskIDs, app.WorkflowTaskID)
		}
		if !seen[app.Namespace] {
			namespaces = append(namespaces, app.Namespace)
			seen[app.Namespace] = true
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT namespace,application_name FROM workflow_resources WHERE namespace=ANY($1) AND tenant_id<>$2`, namespaces, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocked := map[string]bool{}
	for rows.Next() {
		var ns, name string
		if err = rows.Scan(&ns, &name); err != nil {
			return nil, err
		}
		blocked[ns+"/"+name] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	owned := map[string]bool{}
	if len(taskIDs) > 0 {
		tasks, e := s.pool.Query(ctx, `SELECT t.id FROM workflow_task_runs t JOIN projects p ON p.id=t.project_id WHERE t.id=ANY($1) AND p.tenant_id=$2 UNION SELECT task_id FROM workflow_resources WHERE task_id=ANY($1) AND tenant_id=$2 AND namespace=ANY($3)`, taskIDs, tenant, namespaces)
		if e != nil {
			return nil, e
		}
		for tasks.Next() {
			var id string
			if e = tasks.Scan(&id); e != nil {
				tasks.Close()
				return nil, e
			}
			owned[id] = true
		}
		e = tasks.Err()
		tasks.Close()
		if e != nil {
			return nil, e
		}
	}
	result := make([]domain.SparkApplication, 0, len(apps))
	for _, app := range apps {
		if !blocked[app.Namespace+"/"+app.Name] && (app.WorkflowTaskID == "" || owned[app.WorkflowTaskID]) {
			result = append(result, app)
		}
	}
	return result, nil
}
