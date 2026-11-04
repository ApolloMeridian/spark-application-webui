package workflow

import (
	"context"
	"spark-control-center/backend/internal/domain"
	"testing"
	"time"
)

func TestSchedulerLockDoesNotBlockConfigurationWrites(t *testing.T) {
	s, user, _, _ := fixture(t)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(14001400)`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err = s.SaveProject(bounded, user, domain.Project{Name: "Unblocked config"}); err != nil {
		t.Fatal("config waits behind scheduler lock", err)
	}
}

func TestFailedTaskNameDoesNotClaimApplicationTenant(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Tasks) == 0 {
		t.Fatal("no task")
	}
	task := run.Tasks[0]
	if _, err = s.pool.Exec(ctx, `UPDATE workflow_task_runs SET state='failed',data=jsonb_set(data,'{applicationName}','"external-app"'::jsonb) WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	other := user
	other.TenantID = "default"
	allowed, err := s.ApplicationTenant(ctx, other, "spark", "external-app")
	if err != nil || !allowed {
		t.Fatal("failed task claimed external resource", err)
	}
}
