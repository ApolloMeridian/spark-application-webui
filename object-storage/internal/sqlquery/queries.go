package sqlquery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const queryFields = `id::text,state,tables,columns,rows,pages,truncated,message,operation,created_at,expires_at,table_uuids`

func scanQuery(row pgx.Row) (query, []string, error) {
	var q query
	var ids []string
	err := row.Scan(&q.ID, &q.State, &q.Tables, &q.Columns, &q.Rows, &q.Pages, &q.Truncated, &q.Message, &q.Operation, &q.Created, &q.Expires, &ids)
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
	sql := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(input.SQL), ";"))
	if sql == "" || len(sql) > 32768 || strings.Contains(sql, "${") || !uuidPattern.MatchString(input.RequestID) {
		reply(w, 400, nil, "invalid SQL or request ID")
		return
	}
	if p, ok := s.cfg.Profiles[x.ID]; !ok || p.Tenant != x.Tenant {
		reply(w, 403, nil, "SQL identity is not provisioned")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	names, err := s.clients.parse(ctx, sql)
	if err != nil {
		s.recordDenied(ctx, x, input.RequestID, fingerprint(sql), "SQL rejected by parser")
		reply(w, 400, nil, err.Error())
		return
	}
	ids, err := s.authorize(ctx, x, names)
	if err != nil {
		s.recordDenied(ctx, x, input.RequestID, fingerprint(sql), "SQL table access denied")
		reply(w, 403, nil, "one or more input tables are not granted")
		return
	}
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
	hash := fingerprint(sql)
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
		FROM console_sql_queries WHERE state IN ('queued','running')`, x.Tenant, x.ID).Scan(&pending, &ownerPending)
	if err != nil {
		reply(w, 503, nil, "query queue unavailable")
		return
	}
	if pending >= 64 || ownerPending >= 3 {
		reply(w, 429, nil, "query queue is full")
		return
	}
	err = tx.QueryRow(ctx, `INSERT INTO console_sql_queries(id,tenant,user_id,actor_name,statement,fingerprint,request_key,tables,table_uuids)
		VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, x.Tenant, x.ID, x.Name, sql, hash, input.RequestID, names, ids).Scan(&id)
	if err == nil {
		err = audit(ctx, tx, x, id, "SQL_SUBMIT", "SUCCESS", hash+" "+strings.Join(names, ","))
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

func (s *Server) recordDenied(ctx context.Context, x user, id, hash, reason string) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		dbError(err)
		return
	}
	defer tx.Rollback(ctx)
	if err = audit(ctx, tx, x, id, "SQL_DENIED", "FAILED", reason+" "+hash); err == nil {
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
	if q.State != "succeeded" || q.Expires == nil || !q.Expires.After(time.Now()) {
		return false
	}
	if p, ok := s.cfg.Profiles[x.ID]; !ok || p.Tenant != x.Tenant {
		return false
	}
	if _, err := s.actualUser(ctx, x.actor); err != nil {
		return false
	}
	now, err := s.authorize(ctx, x, q.Tables)
	return err == nil && slices.Equal(now, ids)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, x user) {
	rows, err := s.db.Query(r.Context(), `SELECT `+queryFields+` FROM console_sql_queries
		WHERE tenant=$1 AND user_id=$2 ORDER BY created_at DESC LIMIT 100`, x.Tenant, x.ID)
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
	reply(w, 200, all, "")
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, x user) {
	q, ids, err := s.owned(r, x)
	if err != nil {
		reply(w, 404, nil, "query not found")
		return
	}
	if q.State == "succeeded" && !s.canRead(r.Context(), x, q, ids) {
		q.Columns, q.Pages = nil, 0
	}
	reply(w, 200, q, "")
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
		state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,
		statement=CASE WHEN state='queued' THEN '' ELSE statement END
		WHERE id=$1 AND tenant=$2 AND user_id=$3 AND NOT cancel_requested AND state IN ('queued','running')`, id, x.Tenant, x.ID)
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
	q     query
	x     user
	SQL   string
	UUIDs []string
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
	if _, err = tx.Exec(ctx, `UPDATE console_sql_queries SET state='interrupted',message='Worker lost; submit a new run',statement=''
		WHERE state='running' AND worker_deadline<=now()`); err != nil {
		return work{}, err
	}
	var job work
	err = tx.QueryRow(ctx, `WITH next AS (
	  SELECT q.id FROM console_sql_queries q WHERE q.state='queued' AND NOT EXISTS(
	   SELECT 1 FROM console_sql_queries r WHERE r.state='running' AND r.tenant=q.tenant AND r.user_id=q.user_id)
	   AND (SELECT count(*) FROM console_sql_queries WHERE state='running') < $1
	  ORDER BY q.created_at LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE console_sql_queries q SET state='running',worker_deadline=now()+interval '20 seconds'
	  FROM next WHERE q.id=next.id
	  RETURNING q.id::text,q.tenant,q.user_id,q.actor_name,q.statement,q.tables,q.table_uuids`, s.cfg.Workers).Scan(
		&job.q.ID, &job.x.Tenant, &job.x.ID, &job.x.Name, &job.SQL, &job.q.Tables, &job.UUIDs)
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
		uuids, err := s.authorize(step, job.x, job.q.Tables)
		tag, lease := s.db.Exec(step, `UPDATE console_sql_queries SET worker_deadline=now()+interval '20 seconds'
			WHERE id=$1 AND state='running' AND NOT cancel_requested AND worker_deadline>now()`, job.q.ID)
		cancel()
		if auth != nil || err != nil || lease != nil || !slices.Equal(job.UUIDs, uuids) || tag.RowsAffected() != 1 {
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
	state, result := "succeeded", "SUCCESS"
	message := ""
	if err != nil {
		state, result, message = "failed", "FAILED", err.Error()
	}
	encoded, _ := json.Marshal(q.Columns)
	e = tx.QueryRow(ctx, `UPDATE console_sql_queries SET state=CASE WHEN cancel_requested THEN 'cancelled' ELSE $2 END,
		columns=$3,rows=$4,pages=$5,truncated=$6,message=CASE WHEN cancel_requested THEN '' ELSE $7 END,statement='',
		expires_at=now()+make_interval(secs=>$8) WHERE id=$1 AND state='running' AND worker_deadline>now()
		RETURNING state`, job.q.ID, state, encoded, q.Rows, q.Pages, q.Truncated, message, s.cfg.Retention.Seconds()).Scan(&state)
	if e == nil {
		if state == "cancelled" {
			result = "SUCCESS"
		}
		e = audit(ctx, tx, job.x, job.q.ID, "SQL_FINISH", result, state+" operation="+q.Operation)
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	dbError(e)
}
