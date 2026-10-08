package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/robfig/cron/v3"
	"spark-control-center/backend/internal/kube"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

// Backfill requests are durable cursors, not an unbounded in-memory list.
type BackfillRequest struct {
	ID            string `json:"id"`
	WorkflowID    string `json:"workflowId"`
	WorkflowName  string `json:"workflowName"`
	Version       int    `json:"version"`
	Cron          string `json:"cron"`
	TimeZone      string `json:"timeZone"`
	NextAt        string `json:"nextAt"`
	EndsAt        string `json:"endsAt"`
	Status        string `json:"status"`
	Direction     string `json:"direction"`
	FailurePolicy string `json:"failurePolicy"`
	StartsAt      string `json:"startsAt"`
	CreatedCount  int64  `json:"createdCount"`
	Error         string `json:"error"`
	CreatedAt     string `json:"createdAt"`
}

func (s *Service) ListBackfills(ctx context.Context, user domain.User, project string) ([]BackfillRequest, error) {
	result := []BackfillRequest{}
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,workflow_id,cron,time_zone,next_at,ends_at,state,created_count,error,created_at,snapshot,direction,failure_policy,starts_at FROM workflow_backfills WHERE project_id=$1 ORDER BY created_at DESC LIMIT 500`, project)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x BackfillRequest
			var next, end, created, start time.Time
			var raw []byte
			var flow domain.Workflow
			if err := rows.Scan(&x.ID, &x.WorkflowID, &x.Cron, &x.TimeZone, &next, &end, &x.Status, &x.CreatedCount, &x.Error, &created, &raw, &x.Direction, &x.FailurePolicy, &start); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &flow); err != nil {
				return err
			}
			if !visible(user, flow) {
				continue
			}
			x.WorkflowName = flow.Name
			x.Version = flow.Version
			x.NextAt = stamp(next)
			x.EndsAt = stamp(end)
			x.CreatedAt = stamp(created)
			x.StartsAt = stamp(start)
			result = append(result, x)
		}
		return rows.Err()
	})
	return result, err
}

func (s *Service) CancelBackfill(ctx context.Context, user domain.User, project, key string) error {
	if err := writable(user); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		var raw []byte
		var state string
		var flow domain.Workflow
		if err := tx.QueryRow(ctx, `SELECT snapshot,state FROM workflow_backfills WHERE id=$1 AND project_id=$2 FOR UPDATE`, key, project).Scan(&raw, &state); err != nil {
			if err == pgx.ErrNoRows {
				return missing()
			}
			return err
		}
		if err := json.Unmarshal(raw, &flow); err != nil {
			return err
		}
		if !visible(user, flow) {
			return missing()
		}
		if state != "queued" && state != "blocked" {
			return conflict("backfill is no longer pending")
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_backfills SET state='cancelled',updated_at=now() WHERE id=$1`, key); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "WORKFLOW_BACKFILL_CANCEL", flow.ID, "batch="+key+"; existing runs retained")
	})
}

func nextBackfillOccurrence(parser cron.Schedule, from, to, at time.Time, direction string) time.Time {
	if direction == "backward" {
		first := parser.Next(from.Add(-time.Nanosecond))
		return latestOccurrence(parser, first, at.Add(-time.Nanosecond))
	}
	next := parser.Next(at)
	if next.After(to) {
		return time.Time{}
	}
	return next
}

func (s *Service) generateBackfills(ctx context.Context, tx pgx.Tx) error {
	// Each request has only one materialized instance at a time. The cursor and
	// last outcome commit with the run, so recovery cannot duplicate an occurrence.
	rows, err := tx.Query(ctx, `SELECT id,owner_id,cron,time_zone,next_at,starts_at,ends_at,snapshot,created_count,direction,failure_policy,last_run_id,last_status FROM workflow_backfills WHERE state='queued' AND (last_run_id='' OR last_status IN ('succeeded','failed','cancelled','discarded')) ORDER BY updated_at,id LIMIT 20 FOR UPDATE`)
	if err != nil {
		return err
	}
	type entry struct {
		id, owner, cron, zone, direction, policy, lastRun, lastState string
		next, start, end                                             time.Time
		flow                                                         domain.Workflow
		count                                                        int64
	}
	entries := []entry{}
	for rows.Next() {
		var x entry
		var raw []byte
		if err = rows.Scan(&x.id, &x.owner, &x.cron, &x.zone, &x.next, &x.start, &x.end, &raw, &x.count, &x.direction, &x.policy, &x.lastRun, &x.lastState); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &x.flow); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range entries {
		if x.lastRun != "" && !terminal(x.lastState) {
			continue
		}
		state, message := "queued", ""
		if x.lastState == "cancelled" || (x.policy == "stop" && (x.lastState == "failed" || x.lastState == "discarded")) {
			state = "failed"
			message = "previous instance " + x.lastRun + " ended as " + x.lastState
		} else if x.next.Before(x.start) || x.next.After(x.end) {
			state = "completed"
		} else {
			owner, e := s.store.FindUserByID(ctx, x.owner)
			authorized := e == nil && writable(owner) == nil && tenant(owner) == x.flow.TenantID
			if authorized {
				_, e = readWorkflow(ctx, tx, owner, x.flow.ProjectID, x.flow.ID)
				authorized = e == nil && visible(owner, x.flow)
				for _, n := range x.flow.Nodes {
					if s.namespace(owner, n.Namespace) != nil {
						authorized = false
					}
				}
			}
			if !authorized {
				state = "blocked"
				message = "owner permissions, tenant or account changed"
			} else {
				parser, e := ParseCron(x.cron, x.zone)
				if e != nil {
					return e
				}
				run, e := s.createRun(ctx, tx, owner, x.flow, x.next, "backfill", "", x.id)
				if e != nil {
					var apiErr *kube.APIError
					if !errors.As(e, &apiErr) || apiErr.StatusCode != 400 {
						return e
					}
					state = "blocked"
					message = e.Error()
				} else {
					x.count++
					x.lastRun = run.ID
					x.lastState = run.Status
					next := nextBackfillOccurrence(parser, x.start, x.end, x.next, x.direction)
					if next.IsZero() {
						next = x.end.Add(time.Second)
					}
					x.next = next
				}
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE workflow_backfills SET next_at=$2,created_count=$3,state=$4,error=$5,last_run_id=$6,last_status=$7,updated_at=now() WHERE id=$1`, x.id, x.next, x.count, state, message, x.lastRun, x.lastState); err != nil {
			return err
		}
		if state != "queued" {
			operation := "WORKFLOW_BACKFILL_COMPLETE"
			if state != "completed" {
				operation = "WORKFLOW_BACKFILL_BLOCKED"
			}
			if err = auditTx(ctx, tx, "scheduler", operation, x.flow.ID, fmt.Sprintf("batch=%s count=%d %s", x.id, x.count, message)); err != nil {
				return err
			}
		}
	}
	return nil
}
