package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

const effectLease = 90 * time.Second
const effectRPCDeadline = 20 * time.Second

type taskObservation struct {
	app domain.SparkApplication
	err error
}

func observationKey(task domain.TaskRun) string {
	return task.ID + ":" + strconv.Itoa(task.Attempt) + ":" + task.Namespace + "/" + task.ApplicationName
}

// Close database rows before touching Kubernetes. Parallelism is bounded and an
// unavailable observation never changes a task into a terminal state.
func (s *Service) observeTasks(ctx context.Context) (map[string]taskObservation, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.data FROM workflow_task_runs t JOIN workflow_runs r ON r.id=t.run_id WHERE t.state='running' AND r.state IN ('running','paused','cancelling') ORDER BY t.id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	tasks := []domain.TaskRun{}
	for rows.Next() {
		var raw []byte
		var task domain.TaskRun
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &task); err != nil {
			break
		}
		tasks = append(tasks, task)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := map[string]taskObservation{}
	var mu sync.Mutex
	jobs := make(chan domain.TaskRun)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range jobs {
				step, cancel := context.WithTimeout(ctx, 5*time.Second)
				app, err := s.apps.GetWorkflowApplication(step, task.Namespace, task.ApplicationName)
				cancel()
				mu.Lock()
				result[observationKey(task)] = taskObservation{app, err}
				mu.Unlock()
			}
		}()
	}
	for _, task := range tasks {
		select {
		case jobs <- task:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return result, ctx.Err()
}

func enqueueEffect(ctx context.Context, tx pgx.Tx, run domain.WorkflowRun, task domain.TaskRun, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO workflow_effects(id,run_id,task_id,attempt,kind) VALUES($1,$2,$3,$4,$5) ON CONFLICT(task_id,attempt,kind) DO UPDATE SET state='pending',available_at=now(),lease_token='',lease_until='-infinity',updated_at=now() WHERE workflow_effects.state='completed'`, id("effect"), run.ID, task.ID, task.Attempt, kind)
	return err
}

type workflowEffect struct {
	ID, RunID, TaskID, Kind, Token string
	Attempt                        int
	Uncertain                      bool
}

func (s *Service) claimEffect(ctx context.Context) (workflowEffect, error) {
	var e workflowEffect
	e.Token = id("claim")
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM workflow_effects WHERE state<>'completed' AND available_at<=now() AND (state='pending' OR lease_until<=now())
 ORDER BY CASE WHEN kind='kill' THEN 0 ELSE 1 END,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE workflow_effects e SET state='leased',lease_token=$1,lease_until=now()+($2*interval '1 second'),updated_at=now()
FROM candidate c WHERE e.id=c.id RETURNING e.id,e.run_id,e.task_id,e.attempt,e.kind,COALESCE(e.uncertain_until>now(),false)`, e.Token, effectLease.Seconds()).Scan(&e.ID, &e.RunID, &e.TaskID, &e.Attempt, &e.Kind, &e.Uncertain)
	return e, err
}

func (s *Service) effectRun(ctx context.Context, e workflowEffect) (domain.WorkflowRun, domain.TaskRun, error) {
	var run domain.WorkflowRun
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT r.data FROM workflow_runs r JOIN workflow_effects e ON e.run_id=r.id WHERE e.id=$1 AND e.state='leased' AND e.lease_token=$2 AND e.lease_until>now()`, e.ID, e.Token).Scan(&raw)
	if err != nil {
		return run, domain.TaskRun{}, err
	}
	if err = json.Unmarshal(raw, &run); err != nil {
		return run, domain.TaskRun{}, err
	}
	for _, task := range run.Tasks {
		if task.ID == e.TaskID && task.Attempt == e.Attempt {
			return run, task, nil
		}
	}
	return run, domain.TaskRun{}, pgx.ErrNoRows
}

func (s *Service) runAuthorized(ctx context.Context, run domain.WorkflowRun) bool {
	user, err := s.store.FindUserByID(ctx, run.OwnerID)
	if err != nil || user.Disabled || writable(user) != nil || tenant(user) != run.Snapshot.TenantID {
		return false
	}
	for _, node := range run.Snapshot.Nodes {
		if s.namespace(user, node.Namespace) != nil {
			return false
		}
	}
	return true
}

type effectResult struct {
	Accepted, Skipped, Missing, TerminalFailure bool
	Message                                     string
}

func apiStatus(err error, status int) bool {
	var api *kube.APIError
	return errors.As(err, &api) && api.StatusCode == status
}

// The instance ID alone is insufficient when retries reuse an application name.
// Legacy in-flight manifests without the new attempt annotation remain readable.
func ownedTask(app domain.SparkApplication, task domain.TaskRun) bool {
	if !owned(app, task.ID) {
		return false
	}
	var object map[string]any
	if yaml.Unmarshal([]byte(app.YAML), &object) != nil {
		return false
	}
	meta, _ := object["metadata"].(map[string]any)
	annotations, _ := meta["annotations"].(map[string]any)
	value := fmt.Sprint(annotations["spark-control-center.io/task-attempt"])
	if value == "<nil>" {
		return !strings.Contains(task.Manifest, "spark-control-center.io/task-attempt")
	}
	return value == strconv.Itoa(task.Attempt)
}

// No database transaction or checked-out connection is held across these RPCs.
// Every CREATE is non-overwriting and every Kill is preceded by fresh ownership.
func (s *Service) performEffect(ctx context.Context, e workflowEffect) (effectResult, error) {
	run, task, err := s.effectRun(ctx, e)
	if err != nil {
		return effectResult{}, err
	}
	app, err := s.apps.GetWorkflowApplication(ctx, task.Namespace, task.ApplicationName)
	if err != nil && !apiStatus(err, 404) {
		return effectResult{}, err
	}
	if err == nil {
		if !ownedTask(app, task) {
			return effectResult{TerminalFailure: true, Message: "SparkApplication name collision; foreign resource was not modified"}, nil
		}
		if e.Kind == "submit" {
			return effectResult{Accepted: true}, nil
		}
		if app.State == "COMPLETED" || app.State == "FAILED" || app.State == "SUBMISSION_FAILED" || app.State == "FAILED_SUBMISSION" {
			return effectResult{Accepted: true}, nil
		}
		_, err = s.apps.KillApplication(ctx, task.Namespace, task.ApplicationName, run.CreatedBy, "workflow cancellation/timeout")
		return effectResult{Accepted: err == nil}, err
	}
	if e.Kind == "kill" {
		return effectResult{Missing: true}, nil
	}
	if terminal(run.Status) || run.Status == "cancelling" || run.Status == "paused" || !s.runAuthorized(ctx, run) {
		if e.Uncertain {
			return effectResult{}, fmt.Errorf("CREATE outcome is uncertain; retaining cancellation reconciliation")
		}
		return effectResult{Skipped: true}, nil
	}
	if _, err = s.apps.DryRunApplication(ctx, task.Namespace, task.Manifest, run.CreatedBy); err != nil {
		return effectResult{TerminalFailure: apiStatus(err, 400) || apiStatus(err, 403) || apiStatus(err, 422), Message: err.Error()}, err
	}
	// A slow dry-run must not bypass a concurrent pause/cancellation/revocation.
	run, task, err = s.effectRun(ctx, e)
	if err != nil {
		return effectResult{}, err
	}
	if terminal(run.Status) || run.Status == "cancelling" || run.Status == "paused" || !s.runAuthorized(ctx, run) {
		return effectResult{Skipped: true}, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE workflow_effects SET uncertain_until=now()+interval '2 minutes' WHERE id=$1 AND state='leased' AND lease_token=$2 AND lease_until>now()`, e.ID, e.Token)
	if err != nil || tag.RowsAffected() == 0 {
		if err == nil {
			err = pgx.ErrNoRows
		}
		return effectResult{}, err
	}
	_, err = s.apps.SubmitApplication(ctx, task.Namespace, task.Manifest, run.CreatedBy)
	if err == nil {
		return effectResult{Accepted: true}, nil
	}
	if apiStatus(err, 400) || apiStatus(err, 403) || apiStatus(err, 422) {
		return effectResult{TerminalFailure: true, Message: err.Error()}, nil
	}
	// A 409 may be our own prior accepted CREATE. Network errors are ambiguous:
	// leave the persisted intent retryable, never incorrectly fail/retry a job.
	return effectResult{}, err
}

func (s *Service) releaseEffect(e workflowEffect, message string, immediate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	delay := 5
	if immediate {
		delay = 0
	}
	_, err := s.pool.Exec(ctx, `UPDATE workflow_effects SET state='pending',lease_until='-infinity',available_at=now()+($3*interval '1 second'),updated_at=now() WHERE id=$1 AND state='leased' AND lease_token=$2`, e.ID, e.Token, delay)
	if err != nil {
		s.logger.Warn("workflow effect release failed; lease takeover remains available", "effect", e.ID, "error", err)
	} else if message != "" {
		s.logger.Warn("workflow effect will be reconciled", "effect", e.ID, "kind", e.Kind, "error", message)
	}
}

func (s *Service) deliverEffects(ctx context.Context, now time.Time, limit int) error {
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := s.claimEffect(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		step, cancel := context.WithTimeout(ctx, effectRPCDeadline)
		result, err := s.performEffect(step, e)
		cancel()
		if err != nil && !result.TerminalFailure {
			s.releaseEffect(e, err.Error(), ctx.Err() != nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if err = s.ackEffect(ctx, e, result, now); err != nil {
			s.releaseEffect(e, err.Error(), true)
			return err
		}
	}
	return nil
}

func (s *Service) ackEffect(ctx context.Context, e workflowEffect, result effectResult, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT data FROM workflow_runs WHERE id=$1 FOR UPDATE`, e.RunID).Scan(&raw); err != nil {
		return err
	}
	var run domain.WorkflowRun
	if err = json.Unmarshal(raw, &run); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_effects WHERE id=$1 AND state='leased' AND lease_token=$2 AND lease_until>now())`, e.ID, e.Token).Scan(&valid); err != nil || !valid {
		return err
	}
	for i := range run.Tasks {
		task := &run.Tasks[i]
		if task.ID != e.TaskID || task.Attempt != e.Attempt {
			continue
		}
		// Re-read current policy, never the user captured before the RPC.
		authorized := s.runAuthorized(ctx, run)
		if !authorized && !terminal(run.Status) {
			failRun(&run, "owner permissions, tenant or account changed during dispatch", now)
		}
		switch {
		case result.TerminalFailure:
			task.Status, task.Message, task.FinishedAt = "failed", result.Message, stamp(now)
		case result.Missing:
			task.Status, task.Message, task.FinishedAt = "failed", "SparkApplication disappeared", stamp(now)
			if run.Status == "cancelling" && !task.TimedOut {
				task.Status = "cancelled"
			}
		case result.Skipped:
			if run.Status == "paused" && authorized {
				task.Status, task.StartedAt = "queued", ""
			} else {
				task.Status, task.FinishedAt = "cancelled", stamp(now)
			}
		case result.Accepted && e.Kind == "submit":
			task.Status, task.Message, task.NextAttemptAt = "running", "", ""
			if _, err = tx.Exec(ctx, `INSERT INTO workflow_resources(namespace,application_name,task_id,tenant_id) VALUES($1,$2,$3,$4) ON CONFLICT(namespace,application_name) DO UPDATE SET task_id=EXCLUDED.task_id,tenant_id=EXCLUDED.tenant_id`, task.Namespace, task.ApplicationName, task.ID, run.Snapshot.TenantID); err != nil {
				return err
			}
			if run.Status == "cancelling" || !authorized || terminal(run.Status) {
				if err = enqueueEffect(ctx, tx, run, *task, "kill"); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `INSERT INTO operation_audit(id,namespace,application_name,operator_name,operation,reason,occurred_at,result,message,tenant_id) VALUES($1,$2,$3,$4,'WORKFLOW_TASK_DISPATCH','',now(),'SUCCESS',$5,$6)`, id("audit"), task.Namespace, task.ApplicationName, run.CreatedBy, fmt.Sprintf("run=%s node=%s attempt=%d", run.ID, task.NodeID, task.Attempt), run.Snapshot.TenantID); err != nil {
				return err
			}
		case result.Accepted && e.Kind == "kill":
			task.CancelRequested = true
			task.Message = "termination requested; waiting for Kubernetes terminal state"
		}
		if err = s.finishRun(ctx, tx, &run, now); err != nil {
			return err
		}
		if err = writeRun(ctx, tx, run); err != nil {
			return err
		}
		break
	}
	if _, err = tx.Exec(ctx, `UPDATE workflow_effects SET state='completed',lease_token='',lease_until='-infinity',uncertain_until=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2`, e.ID, e.Token); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
