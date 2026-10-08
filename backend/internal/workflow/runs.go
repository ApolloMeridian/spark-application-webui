package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

func (s *Service) ListSchedules(ctx context.Context, user domain.User, project string) ([]domain.WorkflowSchedule, error) {
	result := []domain.WorkflowSchedule{}
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT s.data,w.definition FROM workflow_schedules s JOIN workflows w ON w.id=s.workflow_id WHERE s.project_id=$1 ORDER BY s.next_run_at`, project)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			var definition []byte
			var x domain.WorkflowSchedule
			if err := rows.Scan(&data, &definition); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &x); err != nil {
				return err
			}
			var flow domain.Workflow
			if err := json.Unmarshal(definition, &flow); err != nil {
				return err
			}
			if !visible(user, flow) {
				continue
			}
			result = append(result, x)
		}
		return rows.Err()
	})
	return result, err
}
func (s *Service) SaveSchedule(ctx context.Context, user domain.User, x domain.WorkflowSchedule) (domain.WorkflowSchedule, error) {
	if err := writable(user); err != nil {
		return x, err
	}
	if x.Name == "" || len(x.Name) > 100 {
		return x, invalid("schedule name required (max 100 characters)")
	}
	if x.TimeZone == "" {
		x.TimeZone = s.cfg.WorkflowTimeZone
		if x.TimeZone == "" {
			x.TimeZone = "UTC"
		}
	}
	parser, err := ParseCron(x.Cron, x.TimeZone)
	if err != nil {
		return x, invalid(err.Error())
	}
	if x.Misfire == "" {
		x.Misfire = "skip"
	}
	if x.Misfire != "skip" && x.Misfire != "fire_once" && x.Misfire != "catch_up" {
		return x, invalid("invalid misfire policy")
	}
	now := time.Now().UTC()
	start := now
	if x.StartsAt != "" {
		at, e := time.Parse(time.RFC3339Nano, x.StartsAt)
		if e != nil {
			return x, invalid("invalid schedule start")
		}
		if at.After(start) {
			start = at.Add(-time.Nanosecond)
		}
	}
	next := parser.Next(start)
	if next.IsZero() {
		return x, invalid("cron has no future occurrence")
	}
	if x.EndsAt != "" {
		end, e := time.Parse(time.RFC3339Nano, x.EndsAt)
		if e != nil || end.Before(next) {
			return x, invalid("schedule end must be after next occurrence")
		}
	}
	x.NextRunAt = stamp(next)
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		flow, e := readWorkflow(ctx, tx, user, x.ProjectID, x.WorkflowID)
		if e != nil {
			return e
		}
		for _, n := range flow.Nodes {
			if e := s.namespace(user, n.Namespace); e != nil {
				return e
			}
		}
		if x.ID == "" {
			x.ID = id("schedule")
		} else {
			var exists bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_schedules WHERE id=$1 AND project_id=$2)`, x.ID, x.ProjectID).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return missing()
			}
		}
		x.CreatedBy = user.Username
		data, e := json.Marshal(x)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO workflow_schedules(id,project_id,workflow_id,owner_id,enabled,next_run_at,data) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET workflow_id=EXCLUDED.workflow_id,owner_id=EXCLUDED.owner_id,enabled=EXCLUDED.enabled,next_run_at=EXCLUDED.next_run_at,data=EXCLUDED.data`, x.ID, x.ProjectID, x.WorkflowID, user.ID, x.Enabled, next, data)
		if e != nil {
			return e
		}
		return auditTx(ctx, tx, user.Username, "SCHEDULE_SAVE", x.ID, x.Cron+" "+x.TimeZone)
	})
	return x, err
}
func (s *Service) DeleteSchedule(ctx context.Context, user domain.User, project, key string) error {
	if err := writable(user); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM workflow_schedules WHERE id=$1 AND project_id=$2`, key, project)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return missing()
		}
		return auditTx(ctx, tx, user.Username, "SCHEDULE_DELETE", key, "")
	})
}

func (s *Service) DryRun(ctx context.Context, user domain.User, project, key string, at time.Time) ([]domain.ManifestPreview, error) {
	result := []domain.ManifestPreview{}
	var flow domain.Workflow
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		var err error
		flow, err = readWorkflow(ctx, tx, user, project, key)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := writable(user); err != nil {
		return nil, err
	}
	for _, n := range flow.Nodes {
		if err = s.namespace(user, n.Namespace); err != nil {
			return nil, err
		}
		manifest, _, e := RenderNode(n, at, flow.TimeZone, "preview-12345678", 1)
		if e != nil {
			return nil, invalid(e.Error())
		}
		preview, e := s.apps.DryRunApplication(ctx, n.Namespace, manifest, user.Username)
		if e != nil {
			return nil, e
		}
		result = append(result, preview)
	}
	return result, nil
}
func (s *Service) Trigger(ctx context.Context, user domain.User, project, key string, at time.Time, values map[string]map[string]string) (domain.WorkflowRun, error) {
	var run domain.WorkflowRun
	if err := writable(user); err != nil {
		return run, err
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		flow, err := readWorkflow(ctx, tx, user, project, key)
		if err != nil {
			return err
		}
		for _, n := range flow.Nodes {
			if err := s.namespace(user, n.Namespace); err != nil {
				return err
			}
		}
		for i := range flow.Nodes {
			if overrides, ok := values[flow.Nodes[i].ID]; ok {
				copyValues := map[string]string{}
				for k, v := range flow.Nodes[i].Values {
					copyValues[k] = v
				}
				for k, v := range overrides {
					copyValues[k] = v
				}
				flow.Nodes[i].Values = copyValues
			}
		}
		run, err = s.createRun(ctx, tx, user, flow, at, "manual", "")
		return err
	})
	return run, err
}

type BackfillOptions struct {
	Direction     string
	FailurePolicy string
}

func (s *Service) Backfill(ctx context.Context, user domain.User, project, key, cron, zone string, from, to time.Time, confirm bool, options ...BackfillOptions) ([]string, error) {
	if err := writable(user); err != nil {
		return nil, err
	}
	if to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		return nil, invalid("backfill range must be within 366 days")
	}
	parser, err := ParseCron(cron, zone)
	if err != nil {
		return nil, invalid(err.Error())
	}
	option := BackfillOptions{Direction: "forward", FailurePolicy: "stop"}
	if len(options) > 0 {
		if options[0].Direction != "" {
			option.Direction = options[0].Direction
		}
		if options[0].FailurePolicy != "" {
			option.FailurePolicy = options[0].FailurePolicy
		}
	}
	if option.Direction != "forward" && option.Direction != "backward" {
		return nil, invalid("invalid backfill direction")
	}
	if option.FailurePolicy != "stop" && option.FailurePolicy != "continue" {
		return nil, invalid("invalid backfill failure policy")
	}
	times := []string{}
	first := parser.Next(from.Add(-time.Nanosecond))
	if option.Direction == "backward" {
		first = latestOccurrence(parser, first, to)
	}
	for at := first; !at.IsZero() && !at.Before(from) && !at.After(to) && len(times) < 50; at = nextBackfillOccurrence(parser, from, to, at, option.Direction) {
		times = append(times, stamp(at))
	}
	if len(times) == 0 {
		return nil, invalid("time range / Cron configuration has no backfill occurrence")
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		flow, e := readWorkflow(ctx, tx, user, project, key)
		if e != nil {
			return e
		}
		for _, n := range flow.Nodes {
			if e = s.namespace(user, n.Namespace); e != nil {
				return e
			}
		}
		if !confirm {
			return nil
		}
		if !flow.Enabled {
			return conflict("workflow is disabled")
		}
		first, _ := time.Parse(time.RFC3339Nano, times[0])
		// Validate parameter defaults before accepting the durable request.
		for _, n := range flow.Nodes {
			if _, _, e = RenderNode(n, first, flow.TimeZone, "preview-12345678", 1); e != nil {
				return invalid(n.Name + ": " + e.Error())
			}
		}
		snapshot, e := json.Marshal(flow)
		if e != nil {
			return e
		}
		batch := id("backfill")
		if _, e = tx.Exec(ctx, `INSERT INTO workflow_backfills(id,project_id,workflow_id,owner_id,cron,time_zone,next_at,starts_at,ends_at,snapshot,direction,failure_policy) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, batch, project, key, user.ID, cron, zone, first, from, to, snapshot, option.Direction, option.FailurePolicy); e != nil {
			return e
		}
		return auditTx(ctx, tx, user.Username, "WORKFLOW_BACKFILL", key, fmt.Sprintf("batch=%s %s .. %s cron=%s zone=%s direction=%s onFailure=%s", batch, stamp(from), stamp(to), cron, zone, option.Direction, option.FailurePolicy))
	})
	return times, err
}
func (s *Service) createRun(ctx context.Context, tx pgx.Tx, user domain.User, flow domain.Workflow, at time.Time, trigger, scheduleID string, backfillID ...string) (domain.WorkflowRun, error) {
	run := domain.WorkflowRun{ID: id("run"), OwnerID: user.ID, ProjectID: flow.ProjectID, WorkflowID: flow.ID, Version: flow.Version, Status: "queued", Trigger: trigger, ScheduledAt: stamp(at), CreatedAt: stamp(time.Now()), CreatedBy: user.Username, Snapshot: flow, Tasks: []domain.TaskRun{}}
	if len(backfillID) > 0 {
		run.BackfillID = backfillID[0]
	}
	if !flow.Enabled {
		return run, conflict("workflow is disabled")
	}
	instanceStamp := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000"), ".", "")
	run.Name = flow.Name + "-" + instanceStamp + "-" + run.ID[len(run.ID)-8:]
	for _, n := range flow.Nodes {
		task := domain.TaskRun{ID: id("task"), RunID: run.ID, ProjectID: run.ProjectID, NodeID: n.ID, Namespace: n.Namespace, Status: "queued", Attempt: 1}
		task.Name = n.Name + "-" + instanceStamp + "-" + task.ID[len(task.ID)-8:]
		manifest, name, err := RenderNode(n, at, flow.TimeZone, task.ID, task.Attempt)
		if err != nil {
			return run, invalid(n.Name + ": " + err.Error())
		}
		task.Manifest = manifest
		task.ApplicationName = name
		run.Tasks = append(run.Tasks, task)
	}
	data, err := json.Marshal(run)
	if err != nil {
		return run, err
	}
	var schedule any
	if scheduleID != "" {
		schedule = scheduleID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO workflow_runs(id,project_id,workflow_id,schedule_id,scheduled_at,state,data) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(schedule_id,scheduled_at) DO NOTHING`, run.ID, run.ProjectID, run.WorkflowID, schedule, at, run.Status, data)
	if err != nil {
		return run, err
	}
	if tag.RowsAffected() == 0 {
		return run, nil
	}
	for _, task := range run.Tasks {
		data, _ := json.Marshal(task)
		if _, err = tx.Exec(ctx, `INSERT INTO workflow_task_runs(id,run_id,project_id,node_id,state,data) VALUES($1,$2,$3,$4,$5,$6)`, task.ID, run.ID, run.ProjectID, task.NodeID, task.Status, data); err != nil {
			return run, err
		}
	}
	err = auditTx(ctx, tx, user.Username, "WORKFLOW_TRIGGER", run.ID, fmt.Sprintf("trigger=%s logicalTime=%s workflow=%s version=%d", trigger, run.ScheduledAt, flow.ID, flow.Version))
	return run, err
}
func (s *Service) ListRuns(ctx context.Context, user domain.User, project string, tasks bool) (any, error) {
	runs := []domain.WorkflowRun{}
	taskResult := []domain.TaskRun{}
	err := s.readTransaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		query := `SELECT data,data->'snapshot' FROM workflow_runs WHERE project_id=$1 ORDER BY created_at DESC LIMIT 500`
		if tasks {
			query = `SELECT t.data,r.data->'snapshot' FROM workflow_task_runs t JOIN workflow_runs r ON r.id=t.run_id WHERE t.project_id=$1 ORDER BY r.created_at DESC LIMIT 2000`
		}
		rows, err := tx.Query(ctx, query, project)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var snapshot []byte
			if err := rows.Scan(&raw, &snapshot); err != nil {
				return err
			}
			var flow domain.Workflow
			if err := json.Unmarshal(snapshot, &flow); err != nil {
				return err
			}
			if !visible(user, flow) {
				continue
			}
			if tasks {
				var x domain.TaskRun
				if err := json.Unmarshal(raw, &x); err != nil {
					return err
				}
				taskResult = append(taskResult, x)
			} else {
				var x domain.WorkflowRun
				if err := json.Unmarshal(raw, &x); err != nil {
					return err
				}
				runs = append(runs, x)
			}
		}
		return rows.Err()
	})
	if tasks {
		return taskResult, err
	}
	return runs, err
}
func writeRun(ctx context.Context, tx pgx.Tx, run domain.WorkflowRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE workflow_runs SET state=$2,data=$3 WHERE id=$1`, run.ID, run.Status, data); err != nil {
		return err
	}
	if run.BackfillID != "" {
		if _, err = tx.Exec(ctx, `UPDATE workflow_backfills SET last_status=$2 WHERE id=$1 AND last_run_id=$3`, run.BackfillID, run.Status, run.ID); err != nil {
			return err
		}
	}
	for _, t := range run.Tasks {
		raw, _ := json.Marshal(t)
		if _, err = tx.Exec(ctx, `UPDATE workflow_task_runs SET state=$2,data=$3 WHERE id=$1`, t.ID, t.Status, raw); err != nil {
			return err
		}
	}
	return nil
}
func terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled" || state == "discarded"
}
func (s *Service) RunAction(ctx context.Context, user domain.User, project, key, action string) error {
	if err := writable(user); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if err := projectAccess(ctx, tx, user, project); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT data FROM workflow_runs WHERE id=$1 AND project_id=$2 FOR UPDATE`, key, project).Scan(&raw); err != nil {
			return err
		}
		var run domain.WorkflowRun
		if err := json.Unmarshal(raw, &run); err != nil {
			return err
		}
		for _, n := range run.Snapshot.Nodes {
			if err := s.namespace(user, n.Namespace); err != nil {
				return err
			}
		}
		switch action {
		case "pause":
			if run.Status != "running" && run.Status != "queued" {
				return conflict("only active instances may be paused")
			}
			run.Status = "paused"
		case "resume":
			if run.Status != "paused" {
				return conflict("instance is not paused")
			}
			if run.StartedAt == "" {
				run.Status = "queued"
			} else {
				run.Status = "running"
			}
		case "cancel":
			if terminal(run.Status) {
				return conflict("instance is already terminal")
			}
			run.Status = "cancelling"
		case "rerun_failed":
			if run.Status != "failed" {
				return conflict("only failed instances can rerun failed tasks")
			}
			run.Status = "queued"
			run.StartedAt = ""
			run.FinishedAt = ""
			run.OwnerID = user.ID
			run.CreatedBy = user.Username
			for i := range run.Tasks {
				t := &run.Tasks[i]
				if t.Status == "failed" || t.Status == "skipped" {
					t.Status = "queued"
					t.Attempt++
					t.StartedAt = ""
					t.FinishedAt = ""
					t.NextAttemptAt = ""
					t.Message = ""
					t.CancelRequested = false
					t.TimedOut = false
					for _, n := range run.Snapshot.Nodes {
						if n.ID == t.NodeID {
							at, _ := time.Parse(time.RFC3339Nano, run.ScheduledAt)
							manifest, name, err := RenderNode(n, at, run.Snapshot.TimeZone, t.ID, t.Attempt)
							if err != nil {
								return err
							}
							t.Manifest = manifest
							t.ApplicationName = name
						}
					}
				}
			}
		case "delete":
			if !terminal(run.Status) {
				return conflict("cancel active execution before deleting history")
			}
			if _, err := tx.Exec(ctx, `DELETE FROM workflow_runs WHERE id=$1`, key); err != nil {
				return err
			}
			return auditTx(ctx, tx, user.Username, "WORKFLOW_HISTORY_DELETE", key, "SparkApplications retained")
		default:
			return invalid("unknown execution action")
		}
		if err := writeRun(ctx, tx, run); err != nil {
			return err
		}
		return auditTx(ctx, tx, user.Username, "WORKFLOW_"+strings.ToUpper(action), key, "")
	})
}
