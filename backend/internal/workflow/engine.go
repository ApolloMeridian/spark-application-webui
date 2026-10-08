package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

func (s *Service) Start(ctx context.Context) {
	if !s.cfg.WorkflowsEnabled || !s.cfg.SchedulerEnabled {
		return
	}
	go func() {
		interval := s.cfg.SchedulerInterval
		if interval < time.Second {
			interval = 5 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			tick, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := s.Tick(tick, time.Now().UTC())
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.Error("workflow scheduler tick", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// All decisions are serialized by a PostgreSQL transaction advisory lock. No
// in-memory queue is authoritative. A lost transaction cannot commit progress;
// deterministic CREATE names plus ownership checks recover external side effects.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(14001400)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	if err = s.fireSchedules(ctx, tx, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT data FROM workflow_runs WHERE state IN ('queued','running','paused','cancelling') ORDER BY CASE WHEN state='paused' AND COALESCE(data->>'startedAt','')='' THEN 2 WHEN state='queued' THEN 1 ELSE 0 END,created_at,id LIMIT 100 FOR UPDATE`)
	if err != nil {
		return err
	}
	runs := []domain.WorkflowRun{}
	for rows.Next() {
		var raw []byte
		var run domain.WorkflowRun
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &run); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	budget := 10
	maxRuns := s.cfg.WorkflowMaxRuns
	if maxRuns == 0 {
		maxRuns = 20
	}
	maxTasks := s.cfg.WorkflowMaxTasks
	if maxTasks == 0 {
		maxTasks = 50
	}
	for i := range runs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		run := &runs[i]
		activeRuns, activeTasks := 0, 0
		for _, r := range runs {
			if r.StartedAt != "" && !terminal(r.Status) {
				activeRuns++
			}
			for _, t := range r.Tasks {
				if t.Status == "running" || t.Status == "submitting" {
					activeTasks++
				}
			}
		}
		if run.Status == "queued" && run.StartedAt == "" && activeRuns >= maxRuns {
			continue
		}
		available := maxTasks - activeTasks
		if available < 0 {
			available = 0
		}
		if err = s.advance(ctx, tx, run, runs, now, &budget, &available); err != nil {
			return err
		}
		if err = writeRun(ctx, tx, *run); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE workflow_scheduler_state SET epoch=epoch+1,last_tick_at=$1 WHERE id=1`, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) fireSchedules(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT data,owner_id FROM workflow_schedules WHERE enabled AND next_run_at<=$1 ORDER BY next_run_at LIMIT 50 FOR UPDATE`, now)
	if err != nil {
		return err
	}
	type entry struct {
		schedule domain.WorkflowSchedule
		owner    string
	}
	entries := []entry{}
	for rows.Next() {
		var raw []byte
		var x entry
		if err = rows.Scan(&raw, &x.owner); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &x.schedule); err != nil {
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
	for _, entry := range entries {
		x := entry.schedule
		parser, e := ParseCron(x.Cron, x.TimeZone)
		if e != nil {
			return e
		}
		due, _ := time.Parse(time.RFC3339Nano, x.NextRunAt)
		var end time.Time
		if x.EndsAt != "" {
			end, _ = time.Parse(time.RFC3339Nano, x.EndsAt)
		}
		owner, e := s.store.FindUserByID(ctx, entry.owner)
		authorized := e == nil && writable(owner) == nil
		var flow domain.Workflow
		if authorized {
			flow, e = readWorkflow(ctx, tx, owner, x.ProjectID, x.WorkflowID)
			authorized = e == nil
			for _, n := range flow.Nodes {
				if s.namespace(owner, n.Namespace) != nil {
					authorized = false
				}
			}
		}
		if !authorized {
			x.Enabled = false
			if e = auditTx(ctx, tx, "scheduler", "SCHEDULE_BLOCKED", x.ID, "owner permissions, tenant or account changed"); e != nil {
				return e
			}
		} else if flow.Enabled {
			if x.Misfire == "fire_once" {
				upper := now
				if !end.IsZero() && end.Before(upper) {
					upper = end
				}
				if latest := latestOccurrence(parser, due, upper); !latest.IsZero() {
					due = latest
				}
			}
			fired := 0
			limit := s.cfg.WorkflowCatchupLimit
			if limit == 0 {
				limit = 20
			}
			for !due.IsZero() && !due.After(now) {
				if !end.IsZero() && due.After(end) {
					break
				}
				next := parser.Next(due)
				shouldFire := true
				if x.Misfire == "skip" && now.Sub(due) > s.cfg.SchedulerInterval*2+time.Second {
					shouldFire = false
				}
				if shouldFire {
					if _, e = s.createRun(ctx, tx, owner, flow, due, "schedule", x.ID); e != nil {
						return e
					}
					fired++
				}
				due = next
				if fired >= limit {
					due = parser.Next(now)
					if e = auditTx(ctx, tx, "scheduler", "SCHEDULE_CATCHUP_LIMIT", x.ID, fmt.Sprintf("remaining missed occurrences skipped (limit=%d)", limit)); e != nil {
						return e
					}
					break
				}
				// skip can jump without iterating months of downtime.
				if x.Misfire == "skip" {
					due = parser.Next(now)
					break
				}
			}
		} else {
			due = parser.Next(now)
		}
		if due.IsZero() {
			x.Enabled = false
			due = now.Add(365 * 24 * time.Hour)
		}
		if !end.IsZero() && due.After(end) {
			x.Enabled = false
		}
		x.NextRunAt = stamp(due)
		raw, _ := json.Marshal(x)
		if _, e = tx.Exec(ctx, `UPDATE workflow_schedules SET enabled=$2,next_run_at=$3,data=$4 WHERE id=$1`, x.ID, x.Enabled, due, raw); e != nil {
			return e
		}
	}
	return nil
}
func busyWorkflow(run *domain.WorkflowRun, runs []domain.WorkflowRun) bool {
	for _, other := range runs {
		if other.ID == run.ID || other.WorkflowID != run.WorkflowID || terminal(other.Status) {
			continue
		}
		if other.Status == "running" || other.Status == "paused" || other.Status == "cancelling" || other.CreatedAt < run.CreatedAt || (other.CreatedAt == run.CreatedAt && other.ID < run.ID) {
			return true
		}
	}
	return false
}
func busyNode(run *domain.WorkflowRun, node string, runs []domain.WorkflowRun) bool {
	for _, other := range runs {
		if other.ID == run.ID || other.WorkflowID != run.WorkflowID || terminal(other.Status) {
			continue
		}
		for _, task := range other.Tasks {
			if task.NodeID == node && (task.Status == "running" || task.Status == "submitting") {
				return true
			}
		}
	}
	return false
}
func failRun(run *domain.WorkflowRun, message string, now time.Time) {
	run.Message = message
	run.Status = "cancelling"
	if run.StartedAt == "" {
		run.StartedAt = stamp(now)
	}
}
func (s *Service) advance(ctx context.Context, tx pgx.Tx, run *domain.WorkflowRun, runs []domain.WorkflowRun, now time.Time, budget, available *int) error {
	owner, authErr := s.store.FindUserByID(ctx, run.OwnerID)
	if authErr != nil || writable(owner) != nil || tenant(owner) != run.Snapshot.TenantID {
		failRun(run, "owner is missing, disabled, demoted or moved to another tenant", now)
	} else {
		for _, n := range run.Snapshot.Nodes {
			if s.namespace(owner, n.Namespace) != nil {
				failRun(run, "owner namespace permission revoked", now)
			}
		}
	}
	if run.Status == "queued" {
		if run.Snapshot.Strategy != "parallel" && busyWorkflow(run, runs) {
			if run.Snapshot.Strategy == "serial_discard" {
				run.Status = "discarded"
				run.FinishedAt = stamp(now)
				for i := range run.Tasks {
					run.Tasks[i].Status = "skipped"
					run.Tasks[i].FinishedAt = stamp(now)
				}
			}
			return nil
		}
		run.Status = "running"
		run.StartedAt = stamp(now)
	}
	if run.Snapshot.TimeoutMinutes > 0 && run.StartedAt != "" {
		at, _ := time.Parse(time.RFC3339Nano, run.StartedAt)
		if now.Sub(at) > time.Duration(run.Snapshot.TimeoutMinutes)*time.Minute {
			failRun(run, "workflow timeout", now)
		}
	}
	active := 0
	for _, t := range run.Tasks {
		if t.Status == "running" || t.Status == "submitting" {
			active++
		}
	}
	for i := range run.Tasks {
		task := &run.Tasks[i]
		var node domain.WorkflowNode
		for _, n := range run.Snapshot.Nodes {
			if n.ID == task.NodeID {
				node = n
				break
			}
		}
		if task.Status == "running" || task.Status == "submitting" {
			app, err := s.apps.GetWorkflowApplication(ctx, task.Namespace, task.ApplicationName)
			if err != nil {
				var apiErr *kube.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
					task.Status = "failed"
					task.Message = "SparkApplication disappeared"
					task.FinishedAt = stamp(now)
					active--
					*available++
				} else {
					task.Message = "poll: " + err.Error()
					continue
				}
			} else {
				if !owned(app, task.ID) {
					task.Status = "failed"
					task.Message = "SparkApplication name collision; foreign resource was not modified"
					task.FinishedAt = stamp(now)
					active--
					*available++
					continue
				}
				switch app.State {
				case "COMPLETED":
					task.Status = "succeeded"
					task.FinishedAt = stamp(now)
					active--
					*available++
				case "FAILED", "SUBMISSION_FAILED", "FAILED_SUBMISSION":
					task.Status = "failed"
					if task.CancelRequested && !task.TimedOut {
						task.Status = "cancelled"
					}
					task.Message = "SparkApplication state=" + app.State
					task.FinishedAt = stamp(now)
					active--
					*available++
				default:
					timedOut := false
					if node.TimeoutMinutes > 0 && task.StartedAt != "" {
						at, _ := time.Parse(time.RFC3339Nano, task.StartedAt)
						timedOut = now.Sub(at) > time.Duration(node.TimeoutMinutes)*time.Minute
					}
					if (run.Status == "cancelling" || timedOut) && !task.CancelRequested {
						if *budget < 1 {
							continue
						}
						*budget--
						_, err = s.apps.KillApplication(ctx, task.Namespace, task.ApplicationName, run.CreatedBy, "workflow cancellation/timeout")
						if err != nil {
							task.Message = "kill retry: " + err.Error()
							continue
						}
						task.CancelRequested = true
						task.TimedOut = timedOut
						task.Message = "termination requested; waiting for Kubernetes terminal state"
					}
				}
			}
		}
		if task.Status == "failed" && run.Status != "cancelling" && task.Attempt <= node.Retries {
			task.Attempt++
			task.Status = "queued"
			delay := 10 * time.Second * time.Duration(1<<uint(task.Attempt-2))
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
			task.NextAttemptAt = stamp(now.Add(delay))
			task.StartedAt = ""
			task.FinishedAt = ""
			task.CancelRequested = false
			task.TimedOut = false
			at, _ := time.Parse(time.RFC3339Nano, run.ScheduledAt)
			manifest, name, err := RenderNode(node, at, run.Snapshot.TimeZone, task.ID, task.Attempt)
			if err != nil {
				return err
			}
			task.Manifest = manifest
			task.ApplicationName = name
		}
		if task.Status != "queued" {
			continue
		}
		if run.Status == "cancelling" {
			task.Status = "cancelled"
			task.FinishedAt = stamp(now)
			continue
		}
		if run.Status == "paused" {
			continue
		}
		ready := true
		failedDep := false
		for _, dep := range node.DependsOn {
			for _, other := range run.Tasks {
				if other.NodeID == dep {
					if other.Status != "succeeded" {
						ready = false
					}
					if other.Status == "failed" || other.Status == "skipped" || other.Status == "cancelled" {
						failedDep = true
					}
				}
			}
		}
		if failedDep {
			task.Status = "skipped"
			task.Message = "upstream task did not succeed"
			task.FinishedAt = stamp(now)
			continue
		}
		if !ready || active >= run.Snapshot.MaxParallel {
			continue
		}
		if task.NextAttemptAt != "" {
			at, _ := time.Parse(time.RFC3339Nano, task.NextAttemptAt)
			if now.Before(at) {
				continue
			}
		}
		if node.Strategy != "parallel" && busyNode(run, node.ID, runs) {
			if node.Strategy == "serial_discard" {
				task.Status = "skipped"
				task.Message = "serial discard: previous task still active"
				task.FinishedAt = stamp(now)
			}
			continue
		}
		if *budget < 1 || *available < 1 {
			continue
		}
		*budget--
		// Reconcile a CREATE accepted before a crashed/uncommitted tick.
		app, err := s.apps.GetWorkflowApplication(ctx, task.Namespace, task.ApplicationName)
		if err == nil {
			if !owned(app, task.ID) {
				task.Status = "failed"
				task.Message = "SparkApplication name already exists; overwrite is forbidden"
				task.FinishedAt = stamp(now)
				continue
			}
		}
		if err != nil {
			var apiErr *kube.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
				task.Message = "preflight: " + err.Error()
				continue
			}
			_, err = s.apps.DryRunApplication(ctx, task.Namespace, task.Manifest, run.CreatedBy)
			if err == nil {
				_, err = s.apps.SubmitApplication(ctx, task.Namespace, task.Manifest, run.CreatedBy)
			}
			if err != nil {
				task.Status = "failed"
				task.Message = err.Error()
				task.FinishedAt = stamp(now)
				continue
			}
		}
		task.Status = "running"
		if _, err := tx.Exec(ctx, `INSERT INTO workflow_resources(namespace,application_name,task_id,tenant_id) VALUES($1,$2,$3,$4) ON CONFLICT(namespace,application_name) DO UPDATE SET task_id=EXCLUDED.task_id,tenant_id=EXCLUDED.tenant_id`, task.Namespace, task.ApplicationName, task.ID, run.Snapshot.TenantID); err != nil {
			return err
		}
		task.StartedAt = stamp(now)
		task.NextAttemptAt = ""
		task.Message = ""
		active++
		*available--
		if err = auditTx(ctx, tx, run.CreatedBy, "WORKFLOW_TASK_DISPATCH", task.ApplicationName, fmt.Sprintf("run=%s node=%s attempt=%d", run.ID, node.ID, task.Attempt)); err != nil {
			return err
		}
	}
	allDone := true
	failed := false
	for _, t := range run.Tasks {
		if t.Status == "queued" || t.Status == "running" || t.Status == "submitting" {
			allDone = false
		}
		if t.Status == "failed" || t.Status == "skipped" {
			failed = true
		}
	}
	if allDone {
		if run.Status == "cancelling" {
			if run.Message != "" {
				run.Status = "failed"
			} else {
				run.Status = "cancelled"
			}
		} else if failed {
			run.Status = "failed"
		} else {
			run.Status = "succeeded"
		}
		run.FinishedAt = stamp(now)
		return auditTx(ctx, tx, "scheduler", "WORKFLOW_FINISHED", run.ID, run.Status+" "+run.Message)
	}
	return nil
}
func owned(app domain.SparkApplication, taskID string) bool {
	var object map[string]any
	if yaml.Unmarshal([]byte(app.YAML), &object) != nil {
		return false
	}
	meta, _ := object["metadata"].(map[string]any)
	annotations, _ := meta["annotations"].(map[string]any)
	return annotations["spark-control-center.io/task-instance"] == taskID
}

// ApplicationTenant hides workflow-created jobs from another tenant, while
// namespace-authorized externally submitted jobs retain their existing visibility.
func (s *Service) ApplicationTenant(ctx context.Context, user domain.User, ns, name string) (bool, error) {
	var tenants []string
	rows, err := s.pool.Query(ctx, `SELECT tenant_id FROM workflow_resources WHERE namespace=$1 AND application_name=$2 UNION SELECT p.tenant_id FROM workflow_task_runs t JOIN projects p ON p.id=t.project_id WHERE t.data->>'namespace'=$1 AND t.data->>'applicationName'=$2`, ns, name)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return false, err
		}
		tenants = append(tenants, value)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	for _, value := range tenants {
		if value != tenant(user) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	var epoch int64
	var at *time.Time
	err := s.pool.QueryRow(ctx, `SELECT epoch,last_tick_at FROM workflow_scheduler_state WHERE id=1`).Scan(&epoch, &at)
	result := map[string]any{"enabled": s.cfg.SchedulerEnabled, "epoch": epoch, "lastTickAt": ""}
	if at != nil {
		result["lastTickAt"] = stamp(*at)
	}
	return result, err
}

func (s *Service) Metrics(ctx context.Context) (string, error) {
	var queued, active, tasks, failed int
	var lag float64
	err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_runs WHERE state='queued'),(SELECT count(*) FROM workflow_runs WHERE state IN ('running','paused','cancelling')),(SELECT count(*) FROM workflow_task_runs WHERE state='running'),(SELECT count(*) FROM workflow_task_runs WHERE state='failed'),COALESCE((SELECT GREATEST(0,EXTRACT(EPOCH FROM now()-MIN(next_run_at))) FROM workflow_schedules WHERE enabled),0)`).Scan(&queued, &active, &tasks, &failed, &lag)
	return fmt.Sprintf("# TYPE spark_control_center_workflow_queued gauge\nspark_control_center_workflow_queued %d\n# TYPE spark_control_center_workflow_active gauge\nspark_control_center_workflow_active %d\n# TYPE spark_control_center_workflow_tasks_active gauge\nspark_control_center_workflow_tasks_active %d\n# TYPE spark_control_center_workflow_tasks_failed gauge\nspark_control_center_workflow_tasks_failed %d\n# TYPE spark_control_center_schedule_lag_seconds gauge\nspark_control_center_schedule_lag_seconds %g\n", queued, active, tasks, failed, lag), err
}
