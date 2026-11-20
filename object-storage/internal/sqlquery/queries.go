package sqlquery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const queryFields = `id::text,state,tables,columns,rows,pages,truncated,message,operation,created_at,expires_at,table_uuids,catalog,write_table,statement_plan,ordinal,
  CASE WHEN statement_count>1 AND EXISTS(SELECT 1 FROM console_sql_queries child
       WHERE child.parent_id=console_sql_queries.id AND child.state='succeeded' AND child.expires_at<=now()) THEN 'expired'
       WHEN state <> 'succeeded' OR expires_at IS NULL OR (pages=0 AND columns IN ('[]'::jsonb,'null'::jsonb)) THEN 'none'
       WHEN expires_at <= now() THEN 'expired' ELSE 'available' END`

func scanQuery(row pgx.Row) (query, []string, error) {
	var q query
	var ids []string
	err := row.Scan(&q.ID, &q.State, &q.Tables, &q.Columns, &q.Rows, &q.Pages, &q.Truncated, &q.Message, &q.Operation, &q.Created, &q.Expires, &ids, &q.Catalog, &q.Write, &q.Plan, &q.Ordinal, &q.ResultState)
	return q, ids, err
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request, x user) {
	var input struct {
		SQL       string `json:"sql"`
		RequestID string `json:"requestId"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.SQL) > 32768 || strings.Contains(input.SQL, "${") || !uuidPattern.MatchString(input.RequestID) {
		reply(w, 400, nil, "invalid SQL or request ID")
		return
	}
	statements, err := splitStatements(input.SQL)
	if err != nil {
		s.recordDenied(r.Context(), x, input.RequestID, fingerprint(input.SQL), statementPlan{}, failedDuring("planning", err))
		reply(w, 400, nil, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	hash := fingerprint(input.SQL)
	plan := statementPlan{Kind: "script"}
	ids := []string{}
	var policy string
	if len(statements) == 1 {
		plan, err = s.planStatement(ctx, statements[0], planningContext{})
		if err != nil {
			s.recordDenied(ctx, x, input.RequestID, hash, plan, failedDuring("planning", err))
			status := 503
			var parseErr parseFailure
			if errors.As(err, &parseErr) {
				status = parseErr.status
			}
			reply(w, status, nil, err.Error())
			return
		}
		ids, err = s.authorize(ctx, x, plan.Access)
		if err != nil {
			s.recordDenied(ctx, x, input.RequestID, hash, plan, failedDuring("authorization", err))
			reply(w, 403, nil, err.Error())
			return
		}
	} else {
		policy, err = s.policyVersion(ctx, x)
		if err != nil {
			reply(w, 503, nil, "SQL permissions unavailable")
			return
		}
	}
	names, write := plan.resources()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, 503, nil, "query database unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(16001602)"); err != nil {
		reply(w, 503, nil, "query queue unavailable")
		return
	}
	var id, previous string
	err = tx.QueryRow(ctx, `SELECT id::text,fingerprint FROM console_sql_queries
		WHERE tenant=$1 AND user_id=$2 AND request_key=$3`, x.Tenant, x.ID, input.RequestID).Scan(&id, &previous)
	if err == nil {
		if previous != hash {
			reply(w, 409, nil, "request ID already used")
			return
		}
		reply(w, 202, map[string]string{"id": id}, "")
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		reply(w, 503, nil, "query queue unavailable")
		return
	}
	var pending, ownerPending int
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE tenant=$1 AND user_id=$2)
		FROM console_sql_queries WHERE parent_id IS NULL AND state IN ('queued','running')`, x.Tenant, x.ID).Scan(&pending, &ownerPending)
	if err != nil {
		reply(w, 503, nil, "query queue unavailable")
		return
	}
	if pending >= 64 || ownerPending >= 3 {
		reply(w, 429, nil, "query queue is full")
		return
	}
	err = tx.QueryRow(ctx, `INSERT INTO console_sql_queries(id,tenant,user_id,actor_name,statement,fingerprint,request_key,tables,table_uuids,catalog,write_table,statement_plan,statement_count,policy_version)
		VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id::text`, x.Tenant, x.ID, x.Name, input.SQL, hash, input.RequestID, names, ids, s.cfg.catalogID(), write, plan, len(statements), policy).Scan(&id)
	if err == nil {
		event := sqlAuditDetails{RequestFingerprint: hash, State: "queued", Plan: plan}
		if len(statements) > 1 {
			event.Script = &scriptAuditCounts{Total: len(statements)}
		}
		err = auditSQL(ctx, tx, x, id, "SQL_SUBMIT", "SUCCESS", event, nil)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		reply(w, 503, nil, "query queue unavailable")
		return
	}
	s.notify()
	reply(w, 202, map[string]string{"id": id}, "")
}

func (s *Server) recordDenied(ctx context.Context, x user, id, hash string, plan statementPlan, failure error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		dbError(err)
		return
	}
	defer tx.Rollback(ctx)
	if err = auditSQL(ctx, tx, x, id, "SQL_DENIED", "FAILED",
		sqlAuditDetails{RequestFingerprint: hash, State: "rejected", Plan: plan}, failure); err == nil {
		err = tx.Commit(ctx)
	}
	dbError(err)
}

func (s *Server) owned(r *http.Request, x user) (query, []string, error) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return query{}, nil, pgx.ErrNoRows
	}
	return scanQuery(s.db.QueryRow(r.Context(), `SELECT `+queryFields+` FROM console_sql_queries WHERE id=$1 AND tenant=$2 AND user_id=$3`, id, x.Tenant, x.ID))
}

func (s *Server) canRead(ctx context.Context, x user, q query, ids []string) bool {
	if q.Catalog != s.cfg.catalogID() || q.ResultState != "available" || q.Expires == nil || !q.Expires.After(time.Now()) {
		return false
	}
	if _, err := s.actualUser(ctx, x.actor); err != nil {
		return false
	}
	now, err := s.authorize(ctx, x, q.statementPlan().Access)
	return err == nil && slices.Equal(now, ids)
}

func (q query) statementPlan() statementPlan {
	if q.Plan != nil {
		return *q.Plan
	}
	plan := statementPlan{Kind: "query"}
	for _, name := range q.Tables {
		plan.Access = append(plan.Access, objectAccess{Scope: "table", Action: "read", Resource: name})
	}
	if q.Write != "" {
		plan.Kind = "command"
		plan.Access = append(plan.Access, objectAccess{Scope: "table", Action: "write", Resource: q.Write})
	}
	return plan
}

func (plan statementPlan) description() string {
	items := []string{plan.Kind}
	if plan.StatementType != "" {
		items = append(items, "statement="+plan.StatementType)
	}
	for _, target := range plan.Targets {
		items = append(items, "target."+target.Type+"="+target.Resource)
	}
	for _, target := range plan.Access {
		if target.MetadataTable != "" {
			if item := "metadataTable=" + target.MetadataTable; !slices.Contains(items, item) {
				items = append(items, item)
			}
		}
		items = append(items, target.Action+"="+target.Resource)
	}
	return strings.Join(items, " ")
}

func (plan statementPlan) resources() ([]string, string) {
	names := []string{}
	var write string
	for _, target := range plan.Access {
		if target.Action == "write" {
			write = target.Resource
		} else if !slices.Contains(names, target.Resource) {
			names = append(names, target.Resource)
		}
	}
	return names, write
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, x user) {
	offset := 0
	paged := r.URL.Query().Has("page")
	if paged {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 || page > 100000 {
			reply(w, 400, nil, "invalid history page")
			return
		}
		offset = (page - 1) * 10
	}
	limit := 100
	if paged {
		limit = 11
	}
	rows, err := s.db.Query(r.Context(), `SELECT `+queryFields+` FROM console_sql_queries
		WHERE tenant=$1 AND user_id=$2 AND parent_id IS NULL AND created_at >= now()-make_interval(secs=>$5)
		ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, x.Tenant, x.ID, limit, offset, s.cfg.HistoryRetention.Seconds())
	if err != nil {
		reply(w, 503, nil, "query history unavailable")
		return
	}
	defer rows.Close()
	all := []query{}
	for rows.Next() {
		q, _, err := scanQuery(rows)
		if err != nil {
			reply(w, 503, nil, "query history unavailable")
			return
		}
		// History metadata does not include result schema after a grant is revoked.
		q.Columns = nil
		all = append(all, q)
	}
	if rows.Err() != nil {
		reply(w, 503, nil, "query history unavailable")
		return
	}
	if paged {
		reply(w, 200, map[string]any{"queries": all[:min(len(all), 10)], "hasNext": len(all) > 10}, "")
	} else {
		reply(w, 200, all, "")
	}
}

func (s *Server) statementText(w http.ResponseWriter, r *http.Request, x user) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		reply(w, 404, nil, "query not found")
		return
	}
	var statement string
	err := s.db.QueryRow(r.Context(), `SELECT statement FROM console_sql_queries
		WHERE id=$1 AND tenant=$2 AND user_id=$3 AND created_at >= now()-make_interval(secs=>$4)`, id, x.Tenant, x.ID, s.cfg.HistoryRetention.Seconds()).Scan(&statement)
	if errors.Is(err, pgx.ErrNoRows) {
		reply(w, 404, nil, "query not found")
	} else if err != nil {
		reply(w, 503, nil, "query text unavailable")
	} else {
		reply(w, 200, map[string]string{"sql": statement}, "")
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, x user) {
	q, ids, err := s.owned(r, x)
	if err != nil {
		reply(w, 404, nil, "query not found")
		return
	}
	if q.State == "succeeded" && !s.canRead(r.Context(), x, q, ids) {
		q.Columns, q.Pages = nil, 0
		if q.ResultState == "available" {
			q.ResultState = "unavailable"
		}
	}
	if q.Plan != nil && q.Plan.Kind == "script" {
		q.Steps, err = s.querySteps(r.Context(), x, q.ID)
		if err != nil {
			reply(w, 503, nil, "SQL statement history unavailable")
			return
		}
	}
	q.RefreshBranches = q.affectedBranches()
	reply(w, 200, q, "")
}

func (q query) affectedBranches() []string {
	if q.State != "succeeded" || q.Plan == nil || q.Plan.Kind != "command" {
		return nil
	}
	var branches []string
	for _, target := range q.Plan.Access {
		if !slices.Contains([]string{"create", "metadataWrite", "delete", "purge"}, target.Action) {
			continue
		}
		name := target.Resource
		if target.Scope == "table" || target.Scope == "view" || target.Scope == "namespace" && target.Action == "delete" {
			name = namespaceOf(name)
		}
		if !slices.Contains(branches, name) {
			branches = append(branches, name)
		}
	}
	return branches
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request, x user) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		reply(w, 404, nil, "query not found")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, 503, nil, "query database unavailable")
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE console_sql_queries SET cancel_requested=true,
		state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END
		WHERE id=$1 AND tenant=$2 AND user_id=$3 AND parent_id IS NULL AND NOT cancel_requested AND state IN ('queued','running')`, id, x.Tenant, x.ID)
	if err == nil && tag.RowsAffected() == 0 {
		reply(w, 409, nil, "query is not cancellable")
		return
	}
	if err == nil {
		err = audit(ctx, tx, x, id, "SQL_CANCEL", "SUCCESS", "cancellation requested")
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		reply(w, 503, nil, "cancellation unavailable")
		return
	}
	reply(w, 202, map[string]string{"id": id}, "")
}

type work struct {
	q      query
	x      user
	SQL    string
	UUIDs  []string
	Policy string
}

func (s *Server) claim(ctx context.Context) (work, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return work{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(16001602)"); err != nil {
		return work{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE console_sql_queries SET state='interrupted',message='Worker lost; submit a new run',
		cleanup_after=CASE WHEN state='running' THEN now()+interval '30 seconds' ELSE NULL END
		WHERE parent_id IS NULL AND ((state='running' AND worker_deadline<=now()) OR (state='queued' AND catalog<>$1))`, s.cfg.catalogID()); err != nil {
		return work{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE console_sql_queries step SET state='interrupted',
		message='Worker lost; submit a new run',cleanup_after=now()+interval '30 seconds'
		FROM console_sql_queries parent WHERE step.parent_id=parent.id AND step.state='running'
		AND parent.state NOT IN ('queued','running')`); err != nil {
		return work{}, err
	}
	var job work
	err = tx.QueryRow(ctx, `WITH next AS (
	  SELECT q.id FROM console_sql_queries q WHERE q.state='queued' AND q.parent_id IS NULL AND NOT EXISTS(
	   SELECT 1 FROM console_sql_queries r WHERE r.parent_id IS NULL AND r.state='running' AND r.tenant=q.tenant AND r.user_id=q.user_id)
	   AND (SELECT count(*) FROM console_sql_queries WHERE state='running' AND parent_id IS NULL) < $1
	  ORDER BY q.created_at LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE console_sql_queries q SET state='running',started_at=now(),worker_deadline=now()+interval '20 seconds'
	  FROM next WHERE q.id=next.id
	  RETURNING q.id::text,q.tenant,q.user_id,q.actor_name,q.statement,q.tables,q.table_uuids,q.catalog,q.write_table,q.statement_plan,q.policy_version`, s.cfg.Workers).Scan(
		&job.q.ID, &job.x.Tenant, &job.x.ID, &job.x.Name, &job.SQL, &job.q.Tables, &job.UUIDs, &job.q.Catalog, &job.q.Write, &job.q.Plan, &job.Policy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return work{}, err
	}
	// Commit lease recovery even with no newly claimable query.
	if commit := tx.Commit(ctx); commit != nil {
		return work{}, commit
	}
	return job, err
}

func (s *Server) dispatch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		if len(s.workers) == cap(s.workers) {
			continue
		}
		step, cancel := context.WithTimeout(ctx, 5*time.Second)
		job, err := s.claim(step)
		cancel()
		if err != nil {
			dbError(err)
			continue
		}
		s.workers <- struct{}{}
		go func() {
			defer func() { <-s.workers; s.notify() }()
			s.run(ctx, job)
		}()
	}
}

func (s *Server) keepLease(ctx context.Context, job work, stop context.CancelFunc) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		step, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, auth := s.actualUser(step, job.x.actor)
		err := s.checkAuthorization(step, job)
		tag, lease := s.db.Exec(step, `UPDATE console_sql_queries SET worker_deadline=now()+interval '20 seconds'
			WHERE id=$1 AND state='running' AND NOT cancel_requested AND worker_deadline>now()`, job.q.ID)
		cancel()
		if auth != nil || err != nil || lease != nil || tag.RowsAffected() != 1 {
			stop()
			return
		}
	}
}

func (s *Server) finish(job work, q query, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		dbError(e)
		return
	}
	defer tx.Rollback(ctx)
	e = s.finishInTx(ctx, tx, job, q, err)
	if e == nil {
		e = tx.Commit(ctx)
	}
	dbError(e)
}

func (s *Server) finishInTx(ctx context.Context, tx pgx.Tx, job work, q query, failure error) error {
	state, result := "succeeded", "SUCCESS"
	message := ""
	if failure != nil {
		state, result, message = "failed", "FAILED", failure.Error()
	}
	encoded, _ := json.Marshal(q.Columns)
	var durationMS int64
	var requestFingerprint string
	var total int
	err := tx.QueryRow(ctx, `UPDATE console_sql_queries SET state=CASE WHEN cancel_requested THEN 'cancelled' ELSE $2 END,
		columns=$3,rows=$4,pages=$5,truncated=$6,message=CASE WHEN cancel_requested THEN '' ELSE $7 END,
		expires_at=CASE WHEN NOT cancel_requested AND $2='succeeded' AND $3::jsonb<>'null'::jsonb THEN now()+make_interval(secs=>$8) END,
		cleanup_after=CASE WHEN cancel_requested OR $2<>'succeeded' THEN now()+interval '30 seconds'
			WHEN $5>0 THEN now()+make_interval(secs=>$8) END WHERE id=$1 AND state='running' AND worker_deadline>now()
		RETURNING state, COALESCE((extract(epoch FROM now()-started_at)*1000)::bigint,0),fingerprint,statement_count`,
		job.q.ID, state, encoded, q.Rows, q.Pages, q.Truncated, message, s.cfg.Retention.Seconds()).Scan(&state, &durationMS, &requestFingerprint, &total)
	if err != nil {
		return err
	}
	if state == "cancelled" {
		result = "FAILED"
		failure = failedDuring("cancelled", errors.New("SQL run cancelled"))
	}
	event := sqlAuditDetails{RequestFingerprint: requestFingerprint, State: state, DurationMS: durationMS, Plan: job.q.statementPlan()}
	if event.Plan.Kind == "script" {
		counts := scriptAuditCounts{Total: total}
		err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE operation<>''),
			COALESCE(min(ordinal) FILTER (WHERE state='failed'),0)
			FROM console_sql_queries WHERE parent_id=$1`, job.q.ID).
			Scan(&counts.Attempted, &counts.EngineAccepted, &counts.FailedStep)
		if err != nil {
			return err
		}
		counts.Skipped = max(0, counts.Total-counts.Attempted)
		event.Script = &counts
	} else {
		event.Step, event.StatementFingerprint, event.OperationID = 1, fingerprint(job.SQL), q.Operation
	}
	return auditSQL(ctx, tx, job.x, job.q.ID, "SQL_FINISH", result, event, failure)
}
