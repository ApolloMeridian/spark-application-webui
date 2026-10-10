package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *PostgresStore) WorkflowPool() *pgxpool.Pool { return s.pool }

func (s *PostgresStore) migrateWorkflows(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
CREATE TABLE IF NOT EXISTS tenants (id text PRIMARY KEY, name text NOT NULL UNIQUE, created_at timestamptz NOT NULL DEFAULT now());
INSERT INTO tenants (id,name) VALUES ('default','default') ON CONFLICT DO NOTHING;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS permissions jsonb;
INSERT INTO tenants(id,name,permissions) VALUES
('admin','admin','{"allNamespaces":true,"namespaces":[],"submit":true,"templatesRead":true,"templatesWrite":true,"workflowsRead":true,"workflowsWrite":true,"kill":true,"delete":true,"alertsRead":true,"alertsWrite":true,"objectStorage":true}'),
('viewer','viewer','{"allNamespaces":true,"namespaces":[],"submit":false,"templatesRead":false,"templatesWrite":false,"workflowsRead":false,"workflowsWrite":false,"kill":false,"delete":false,"alertsRead":false,"alertsWrite":false,"objectStorage":false}') ON CONFLICT DO NOTHING;
ALTER TABLE local_users ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default' REFERENCES tenants(id);
ALTER TABLE operation_audit ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS projects (id text PRIMARY KEY, tenant_id text NOT NULL REFERENCES tenants(id), name text NOT NULL, description text NOT NULL DEFAULT '', created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,name));
CREATE TABLE IF NOT EXISTS workflows (id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE, current_version integer NOT NULL, definition jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS workflow_versions (workflow_id text NOT NULL REFERENCES workflows(id) ON DELETE CASCADE, version integer NOT NULL, snapshot jsonb NOT NULL, created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(workflow_id,version));
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS max_version integer NOT NULL DEFAULT 1;
UPDATE workflows SET max_version=GREATEST(max_version,current_version,COALESCE((SELECT MAX(version) FROM workflow_versions WHERE workflow_id=workflows.id),0));
ALTER TABLE application_templates ADD COLUMN IF NOT EXISTS max_version integer NOT NULL DEFAULT 1;
UPDATE application_templates SET max_version=GREATEST(max_version,current_version,COALESCE((SELECT MAX(version) FROM application_template_versions WHERE template_id=application_templates.id),0));
CREATE TABLE IF NOT EXISTS workflow_runs (id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), workflow_id text NOT NULL REFERENCES workflows(id), schedule_id text, scheduled_at timestamptz NOT NULL, state text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(schedule_id,scheduled_at));
CREATE INDEX IF NOT EXISTS workflow_runs_project_idx ON workflow_runs(project_id,created_at DESC);
CREATE INDEX IF NOT EXISTS workflow_runs_active_idx ON workflow_runs(state) WHERE state IN ('queued','running','paused','cancelling');
CREATE TABLE IF NOT EXISTS workflow_task_runs (id text PRIMARY KEY, run_id text NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE, project_id text NOT NULL REFERENCES projects(id), node_id text NOT NULL, state text NOT NULL, data jsonb NOT NULL, UNIQUE(run_id,node_id));
CREATE INDEX IF NOT EXISTS workflow_tasks_project_idx ON workflow_task_runs(project_id,run_id);
CREATE TABLE IF NOT EXISTS workflow_resources(namespace text NOT NULL,application_name text NOT NULL,task_id text NOT NULL,tenant_id text NOT NULL REFERENCES tenants(id),PRIMARY KEY(namespace,application_name));
CREATE TABLE IF NOT EXISTS workflow_schedules (id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), workflow_id text NOT NULL REFERENCES workflows(id), owner_id text NOT NULL REFERENCES local_users(id) ON DELETE CASCADE, enabled boolean NOT NULL, next_run_at timestamptz NOT NULL, data jsonb NOT NULL);
CREATE INDEX IF NOT EXISTS workflow_schedules_due_idx ON workflow_schedules(next_run_at) WHERE enabled;
CREATE TABLE IF NOT EXISTS workflow_backfills (id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE, workflow_id text NOT NULL REFERENCES workflows(id) ON DELETE CASCADE, owner_id text NOT NULL, cron text NOT NULL, time_zone text NOT NULL, next_at timestamptz NOT NULL, ends_at timestamptz NOT NULL, snapshot jsonb NOT NULL, state text NOT NULL DEFAULT 'queued', created_count bigint NOT NULL DEFAULT 0, error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS workflow_backfills_queue_idx ON workflow_backfills(updated_at) WHERE state='queued';
ALTER TABLE workflow_backfills ADD COLUMN IF NOT EXISTS starts_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE workflow_backfills ADD COLUMN IF NOT EXISTS direction text NOT NULL DEFAULT 'forward';
ALTER TABLE workflow_backfills ADD COLUMN IF NOT EXISTS failure_policy text NOT NULL DEFAULT 'stop';
ALTER TABLE workflow_backfills ADD COLUMN IF NOT EXISTS last_run_id text NOT NULL DEFAULT '';
ALTER TABLE workflow_backfills ADD COLUMN IF NOT EXISTS last_status text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS workflow_scheduler_state (id integer PRIMARY KEY, epoch bigint NOT NULL DEFAULT 0, last_tick_at timestamptz);
INSERT INTO workflow_scheduler_state(id) VALUES(1) ON CONFLICT DO NOTHING;
`)
	return err
}
