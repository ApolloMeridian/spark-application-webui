package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

func (s *PostgresStore) migrateLiveApplications(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS application_collections(namespace text PRIMARY KEY,owner text NOT NULL DEFAULT '',expires_at timestamptz NOT NULL DEFAULT '-infinity',collected_at timestamptz);
 CREATE TABLE IF NOT EXISTS live_applications(namespace text NOT NULL,application_name text NOT NULL,application_id text NOT NULL,tenant_id text,owner_name text NOT NULL,state text NOT NULL,created_at text NOT NULL,finished_at text NOT NULL,payload jsonb NOT NULL,PRIMARY KEY(namespace,application_name));
 CREATE INDEX IF NOT EXISTS live_apps_page_idx ON live_applications(namespace,created_at DESC,application_name);
 CREATE INDEX IF NOT EXISTS live_apps_tenant_state_idx ON live_applications(tenant_id,state,namespace);
 CREATE INDEX IF NOT EXISTS live_apps_uid_idx ON live_applications(namespace,application_id);
 CREATE INDEX IF NOT EXISTS spark_history_page_idx ON spark_application_history(namespace,tenant_id,created_at DESC,application_id);`)
	return err
}

// Collection uses a DB-clock lease, never a transaction spanning external I/O.
func (s *PostgresStore) CollectApplicationNamespace(ctx context.Context, ns string, collect func(context.Context) ([]domain.SparkApplication, error)) error {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	owner := hex.EncodeToString(b[:])
	var claimed string
	err := s.pool.QueryRow(ctx, `INSERT INTO application_collections(namespace,owner,expires_at) VALUES($1,$2,now()+interval '60 seconds') ON CONFLICT(namespace) DO UPDATE SET owner=$2,expires_at=now()+interval '60 seconds' WHERE application_collections.expires_at<=now() AND (application_collections.collected_at IS NULL OR application_collections.collected_at<now()-interval '3 seconds') RETURNING owner`, ns, owner).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanup, `UPDATE application_collections SET expires_at=now() WHERE namespace=$1 AND owner=$2`, ns, owner)
	}()
	work, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	apps, err := collect(work)
	if err != nil {
		return err
	}
	if err = s.UpsertApplications(work, apps); err != nil {
		return err
	}
	tx, err := s.pool.Begin(work)
	if err != nil {
		return err
	}
	defer tx.Rollback(work)
	var valid bool
	if err = tx.QueryRow(work, `SELECT owner=$2 AND expires_at>now() FROM application_collections WHERE namespace=$1 FOR UPDATE`, ns, owner).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("application collection lease expired")
	}
	if _, err = tx.Exec(work, `DELETE FROM live_applications WHERE namespace=$1`, ns); err != nil {
		return err
	}
	for _, app := range apps {
		if app.Namespace != ns {
			return fmt.Errorf("collection namespace mismatch")
		}
		// Detailed snapshots stay in history; live list rows remain lightweight.
		app.YAML = ""
		app.Logs = []string{}
		app.Events = []domain.KubernetesEvent{}
		app.Metrics = []domain.MetricPoint{}
		raw, e := json.Marshal(app)
		if e != nil {
			return e
		}
		_, err = tx.Exec(work, `INSERT INTO live_applications(namespace,application_name,application_id,tenant_id,owner_name,state,created_at,finished_at,payload) VALUES($1,$2,$3,CASE WHEN $4='' THEN '' ELSE COALESCE((SELECT p.tenant_id FROM workflow_task_runs t JOIN projects p ON p.id=t.project_id WHERE t.id=$4),(SELECT r.tenant_id FROM workflow_resources r WHERE r.task_id=$4 AND r.namespace=$1 AND r.application_name=$2)) END,$5,$6,$7,$8,$9)`, ns, app.Name, app.ID, app.WorkflowTaskID, app.Owner, app.State, app.CreatedAt, app.FinishedAt, raw)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(work, `UPDATE application_collections SET collected_at=now(),expires_at=now() WHERE namespace=$1 AND owner=$2`, ns, owner); err != nil {
		return err
	}
	return tx.Commit(work)
}

func (s *PostgresStore) QueryLiveApplications(ctx context.Context, q domain.ApplicationQuery) (domain.ApplicationList, error) {
	result := domain.ApplicationList{Items: []domain.SparkApplication{}, Page: q.Page, PageSize: q.PageSize}
	if q.Page < 1 || q.Page > 1000000 || q.PageSize < 1 || q.PageSize > 200 {
		return result, &kube.APIError{StatusCode: 400, Message: "invalid pagination"}
	}
	ns := q.Namespaces
	if q.Namespace != "" {
		allowed := false
		for _, n := range ns {
			if n == q.Namespace {
				allowed = true
			}
		}
		if !allowed {
			return result, nil
		}
		ns = []string{q.Namespace}
	}
	if len(ns) == 0 {
		return result, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var fresh int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM application_collections WHERE namespace=ANY($1) AND collected_at>now()-interval '90 seconds'`, ns).Scan(&fresh); err != nil {
		return result, err
	}
	if fresh != len(ns) {
		return result, &kube.APIError{StatusCode: 503, Message: "application index is synchronizing; retry shortly"}
	}
	// Keep archived UIDs visible alongside same-name replacements. Never include
	// archived resources in live resource totals or scheduler observations.
	cte := `WITH archived AS (
 SELECT h.*, CASE WHEN h.state IN ('COMPLETED','FAILED','SUBMISSION_FAILED','KILLED') THEN h.state ELSE 'DELETED' END AS archived_state
 FROM spark_application_history h WHERE h.snapshot<>'{}'::jsonb AND NOT EXISTS(SELECT 1 FROM live_applications l WHERE l.application_id=h.application_id AND l.namespace=h.namespace)
 ), a AS (
 SELECT l.namespace,l.application_name,l.application_id,l.tenant_id,l.owner_name,l.state,l.created_at,l.finished_at,l.payload,false AS historical
 FROM live_applications l WHERE NOT EXISTS(SELECT 1 FROM spark_application_history h WHERE h.application_id=l.application_id AND h.purged_at IS NOT NULL)
 UNION ALL
 SELECT h.namespace,h.application_name,h.application_id,h.tenant_id,h.owner_name,h.archived_state,h.snapshot->>'createdAt',COALESCE(h.snapshot->>'finishedAt',''),
 (h.snapshot-'logs'-'events'-'metrics'-'yaml') || jsonb_build_object('historical',true,'sparkUiAvailable',false,'lastObservedAt',h.last_seen_at,'state',h.archived_state,'logs','[]'::jsonb,'events','[]'::jsonb,'metrics','[]'::jsonb,'yaml',''),true
 FROM archived h) `
	where := ` FROM a WHERE namespace=ANY($1) AND ($2='' OR tenant_id='' OR tenant_id=$2) AND (historical OR NOT EXISTS(SELECT 1 FROM workflow_resources r WHERE r.namespace=a.namespace AND r.application_name=a.application_name AND $2<>'' AND r.tenant_id<>$2)) AND ($3='' OR state=$3) AND ($4='' OR owner_name=$4) AND ($5='' OR strpos(lower(application_name||' '||namespace||' '||owner_name),lower($5))>0) AND (NOT $6 OR EXISTS(SELECT 1 FROM application_favorites f WHERE f.user_id=$7 AND f.namespace=a.namespace AND f.application_name IN (a.application_id,a.application_name)))`
	args := []any{ns, domain.TenantScope(ctx), q.State, q.Owner, q.Keyword, q.Favorite, q.UserID}
	if err = tx.QueryRow(ctx, cte+"SELECT count(*)"+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	order := map[string]string{"name": "application_name", "owner": "owner_name", "state": "state", "createdAt": "created_at", "finishedAt": "finished_at"}[q.Sort]
	if order == "" {
		order = "created_at"
	}
	dir := "DESC"
	if q.Direction == "asc" {
		dir = "ASC"
	}
	args = append(args, q.PageSize, int64(q.Page-1)*int64(q.PageSize))
	rows, err := tx.Query(ctx, cte+"SELECT payload"+where+" ORDER BY "+order+" "+dir+",namespace,application_name,application_id LIMIT $8 OFFSET $9", args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var raw []byte
		var app domain.SparkApplication
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return result, err
		}
		if err = json.Unmarshal(raw, &app); err != nil {
			rows.Close()
			return result, err
		}
		result.Items = append(result.Items, app)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
