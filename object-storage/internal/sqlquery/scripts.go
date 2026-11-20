package sqlquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Split Spark statements before parsing so quotes and comments can contain semicolons.
func splitStatements(source string) ([]string, error) {
	var statements []string
	var quote byte
	var block int
	lineComment, content := false, false
	start := 0
	appendStatement := func(end int) {
		if content {
			statements = append(statements, strings.TrimSpace(source[start:end]))
		}
		content = false
		start = end + 1
	}
	for i := 0; i < len(source); i++ {
		c := source[i]
		next := byte(0)
		if i+1 < len(source) {
			next = source[i+1]
		}
		if lineComment {
			lineComment = c != '\n' && c != '\r'
			continue
		}
		if block > 0 {
			if c == '*' && next == '/' {
				block--
				i++
			} else if c == '/' && next == '*' {
				block++
				i++
			}
			continue
		}
		if quote != 0 {
			switch {
			case c == quote && next == quote:
				i++
			case c == quote:
				quote = 0
			case c == '\\' && quote != '`' && next != 0:
				i++
			}
			continue
		}
		switch {
		case c == ';':
			appendStatement(i)
		case c == '-' && next == '-':
			lineComment = true
			i++
		case c == '/' && next == '*':
			block++
			i++
		case c == '\'' || c == '"' || c == '`':
			quote = c
			content = true
		case c != ' ' && c != '\n' && c != '\r' && c != '\t':
			content = true
		}
	}
	if quote != 0 || block != 0 {
		return nil, errors.New("unterminated SQL quote or comment")
	}
	appendStatement(len(source))
	if len(statements) == 0 || len(statements) > 20 {
		return nil, errors.New("submit between 1 and 20 SQL statements")
	}
	return statements, nil
}

type planningContext struct {
	namespace string
	tempViews map[string][]objectAccess
}

func (s *Server) policyVersion(ctx context.Context, x user) (string, error) {
	grants, err := s.grantsFor(ctx, x, false)
	if err != nil {
		return "", err
	}
	versions := make([]string, 0, len(grants))
	for _, grant := range grants {
		versions = append(versions, grant.version)
	}
	return fingerprint(strings.Join(versions, "\n")), nil
}

func (s *Server) checkAuthorization(ctx context.Context, job work) error {
	if job.Policy != "" {
		now, err := s.policyVersion(ctx, job.x)
		if err != nil || now != job.Policy {
			return errors.New("SQL policy changed")
		}
		return nil
	}
	ids, err := s.authorize(ctx, job.x, job.q.statementPlan().Access)
	if err != nil || !slices.Equal(ids, job.UUIDs) {
		return errors.New("SQL permission changed")
	}
	return nil
}

func (s *Server) executeScript(ctx context.Context, parent work) (bool, error) {
	statements, err := splitStatements(parent.SQL)
	if err != nil {
		return false, failedDuring("planning", err)
	}
	p, session, closeSession, err := s.openEngineSession(ctx, parent)
	if err != nil {
		return false, failedDuring("session", err)
	}
	defer closeSession()
	scope := planningContext{tempViews: map[string][]objectAccess{}}
	for index, sql := range statements {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := s.actualUser(ctx, parent.x.actor); err != nil {
			return false, failedDuring("authorization", errors.New("SQL access was revoked"))
		}
		if err := s.checkAuthorization(ctx, parent); err != nil {
			return false, failedDuring("authorization", err)
		}
		plan, planErr := s.planStatement(ctx, sql, scope)
		planErr = failedDuring("planning", planErr)
		ids := []string{}
		if planErr == nil {
			ids, planErr = s.authorize(ctx, parent.x, plan.Access)
			planErr = failedDuring("authorization", planErr)
		}
		if ids == nil {
			ids = []string{}
		}
		names, write := plan.resources()
		job := work{q: query{
			Plan: &plan, Catalog: parent.q.Catalog, Tables: names, Write: write, Ordinal: index + 1,
		}, SQL: sql, UUIDs: ids, x: parent.x, Policy: parent.Policy}
		err = s.db.QueryRow(ctx, `INSERT INTO console_sql_queries(id,tenant,user_id,actor_name,statement,fingerprint,request_key,
			tables,table_uuids,catalog,write_table,statement_plan,parent_id,ordinal,state,started_at)
			SELECT gen_random_uuid(),tenant,user_id,actor_name,'',$2,gen_random_uuid(),$3,$4,catalog,$5,$6,id,$7,'running',now()
			FROM console_sql_queries WHERE id=$1 AND state='running' AND NOT cancel_requested AND worker_deadline>now()
			RETURNING id::text`, parent.q.ID, fingerprint(sql), names, ids, write, plan, job.q.Ordinal).Scan(&job.q.ID)
		if err != nil {
			dbError(err)
			return false, failedDuring("audit", errors.New("could not record SQL statement"))
		}
		output := job.q
		stepErr := planErr
		if stepErr == nil {
			bindings, err := s.bindQuery(ctx, job)
			if err != nil {
				stepErr = failedDuring("binding", err)
			} else if err := s.checkAuthorization(ctx, parent); err != nil {
				stepErr = failedDuring("authorization", err)
			} else {
				output, stepErr = s.executeStatement(ctx, job, p, session, parent.q.ID, bindings)
			}
		}
		finished, err := s.finishStep(parent, job, output, stepErr, index == len(statements)-1)
		if err != nil {
			return false, err
		}
		if finished {
			return true, nil
		}
		switch plan.Kind {
		case "use":
			scope.namespace = plan.Value
		case "temp":
			scope.tempViews[plan.Value] = plan.Access
		}
	}
	return false, nil
}

func (s *Server) finishStep(parent work, step work, output query, failure error, last bool) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	state, result, message := "succeeded", "SUCCESS", ""
	if failure != nil {
		state, result, message = "failed", "FAILED", failure.Error()
	}
	var duration int64
	columns, _ := json.Marshal(output.Columns)
	err = tx.QueryRow(ctx, `UPDATE console_sql_queries step SET state=CASE WHEN parent.cancel_requested THEN 'cancelled' ELSE $3 END,
		columns=$4,rows=$5,pages=$6,truncated=$7,message=$8,operation=$10,
		expires_at=CASE WHEN $3='succeeded' AND NOT parent.cancel_requested AND $4::jsonb<>'null'::jsonb
			THEN now()+make_interval(secs=>$9) END,
		cleanup_after=CASE WHEN $3<>'succeeded' OR parent.cancel_requested THEN now()+interval '30 seconds'
			WHEN $6>0 THEN now()+make_interval(secs=>$9) END
		FROM console_sql_queries parent WHERE step.id=$1 AND parent.id=$2 AND step.parent_id=parent.id
			AND step.state='running' AND parent.state='running' AND parent.worker_deadline>now()
		RETURNING step.state,(extract(epoch FROM now()-step.started_at)*1000)::bigint`, step.q.ID, parent.q.ID,
		state, columns, output.Rows, output.Pages, output.Truncated, message, s.cfg.Retention.Seconds(), output.Operation).Scan(&state, &duration)
	if state == "cancelled" {
		result = "FAILED"
		failure = failedDuring("cancelled", errors.New("SQL run cancelled"))
	}
	if err == nil {
		err = auditSQL(ctx, tx, parent.x, parent.q.ID, "SQL_STATEMENT", result, sqlAuditDetails{
			Step: step.q.Ordinal, State: state, DurationMS: duration, StatementFingerprint: fingerprint(step.SQL),
			RequestFingerprint: fingerprint(parent.SQL), OperationID: output.Operation, Plan: step.q.statementPlan(),
			Outcome: output.AuditOutcome, ObjectUUIDs: output.AuditObjects, EngineSucceeded: output.EngineSucceeded,
		}, failure)
	}
	terminal := failure != nil || last
	if err == nil && terminal {
		if failure != nil {
			failure = fmt.Errorf("Statement %d: %w", step.q.Ordinal, failure)
		}
		err = s.finishInTx(ctx, tx, parent, parent.q, failure)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		dbError(err)
		return false, failedDuring("audit", errors.New("could not save SQL statement outcome"))
	}
	return terminal, nil
}

func (s *Server) querySteps(ctx context.Context, x user, id string) ([]query, error) {
	rows, err := s.db.Query(ctx, `SELECT `+queryFields+` FROM console_sql_queries
		WHERE parent_id=$1 AND tenant=$2 AND user_id=$3 ORDER BY ordinal`, id, x.Tenant, x.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []query
	for rows.Next() {
		out, _, err := scanQuery(rows)
		if err != nil {
			return nil, err
		}
		out.Columns = nil
		out.RefreshBranches = out.affectedBranches()
		steps = append(steps, out)
	}
	return steps, rows.Err()
}

func (s *Server) planStatement(ctx context.Context, sql string, scope planningContext) (statementPlan, error) {
	plan, err := s.clients.parse(ctx, sql, scope)
	if err != nil {
		return plan, err
	}
	targets := make([]objectAccess, 0, len(plan.Access))
	for _, target := range plan.Access {
		if target.Scope != "temp" {
			targets = append(targets, target)
			continue
		}
		targets = append(targets, scope.tempViews[target.Resource]...)
	}
	plan.Access = targets
	if len(plan.Access) > 64 {
		return plan, errors.New("too many SQL resources")
	}
	resolved, err := s.resolveViews(ctx, plan.Access)
	if err == nil {
		plan.Access = resolved
	}
	return plan, err
}

func (s *Server) resolveViews(ctx context.Context, requested []objectAccess) ([]objectAccess, error) {
	if !slices.ContainsFunc(requested, func(a objectAccess) bool {
		return a.Scope == "table" && a.MetadataTable == "" && a.BranchTable == "" && (a.Action == "read" || a.Action == "metadataRead")
	}) {
		return requested, nil
	}
	// Resolve explicit relation kinds before authorization. A view's Spark
	// definition is inspected with its own default namespace, never the caller's.
	catalog, err := s.clients.catalog(ctx)
	if err != nil {
		return nil, err
	}
	checked := map[objectAccess]bool{}
	var targets []objectAccess
	var add func(objectAccess, int) error
	add = func(target objectAccess, depth int) error {
		if checked[target] {
			return nil
		}
		if depth > 8 || len(checked) >= 64 {
			return errors.New("view dependency limit exceeded")
		}
		checked[target] = true
		if target.Scope != "table" || target.MetadataTable != "" || target.BranchTable != "" ||
			(target.Action != "read" && target.Action != "metadataRead") {
			targets = append(targets, target)
			return nil
		}
		if _, err := catalog.table(ctx, target.Resource); err == nil {
			targets = append(targets, target)
			return nil
		} else if !isCatalogNotFound(err) {
			return err
		}
		info, err := catalog.view(ctx, target.Resource)
		if err != nil {
			// Preserve the normal permission denial and missing-table response.
			if isCatalogNotFound(err) {
				targets = append(targets, target)
				return nil
			}
			return err
		}
		target.Scope = "view"
		target.ViewVersion = info.CurrentVersion
		targets = append(targets, target)
		if target.Action == "metadataRead" {
			return nil
		}
		sql, namespace := info.sparkDefinition()
		parsed, err := s.clients.parse(ctx, sql, planningContext{namespace: namespace})
		if err != nil || parsed.Kind != "query" {
			return errors.New("unsupported Spark view definition")
		}
		for _, child := range parsed.Access {
			if err = add(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, target := range requested {
		if err := add(target, 0); err != nil {
			return nil, err
		}
	}
	if targets == nil {
		targets = []objectAccess{}
	}
	return targets, nil
}
