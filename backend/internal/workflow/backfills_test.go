package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"spark-control-center/backend/internal/domain"
)

func TestPostgresBackfillCursorBeyond200AndRecovery(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(204 * time.Second)
	preview, err := s.Backfill(ctx, user, project.ID, flow.ID, "* * * * * *", "UTC", from, to, false)
	if err != nil || len(preview) != 50 || preview[0] != stamp(from) {
		t.Fatal(preview, err)
	}
	if len(runsOf(t, s, user, project.ID)) != 0 {
		t.Fatal("preview created runs")
	}
	if _, err = s.Backfill(ctx, user, project.ID, flow.ID, "* * * * * *", "UTC", from, to, true); err != nil {
		t.Fatal(err)
	}
	// Resume through a new service; each instance must finish before its successor.
	restarted := New(s.store, s.apps, s.cfg, s.logger)
	for i := 0; i < 205; i++ {
		err = restarted.transaction(ctx, func(tx pgx.Tx) error {
			if err := restarted.generateBackfills(ctx, tx); err != nil {
				return err
			}
			// A second reconciliation must not create a second active instance.
			if err := restarted.generateBackfills(ctx, tx); err != nil {
				return err
			}
			var raw []byte
			var run domain.WorkflowRun
			if err := tx.QueryRow(ctx, `SELECT data FROM workflow_runs WHERE state='queued'`).Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &run); err != nil {
				return err
			}
			if run.ScheduledAt != stamp(from.Add(time.Duration(i)*time.Second)) {
				t.Fatalf("wrong logical time: %s", run.ScheduledAt)
			}
			run.Status = "succeeded"
			return writeRun(ctx, tx, run)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = restarted.transaction(ctx, func(tx pgx.Tx) error { return restarted.generateBackfills(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	batches, err := s.ListBackfills(ctx, user, project.ID)
	if err != nil || len(batches) != 1 || batches[0].CreatedCount != 205 || batches[0].Status != "completed" {
		t.Fatal(batches, err)
	}
	var count int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_runs`).Scan(&count)
	if count != 205 {
		t.Fatal(count)
	}
}

func TestPostgresBackfillReverseFailurePolicyAndCancel(t *testing.T) {
	for _, policy := range []string{"stop", "continue"} {
		t.Run(policy, func(t *testing.T) {
			s, user, project, _ := fixture(t)
			ctx := context.Background()
			flow := single(t, s, user, project)
			from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			to := from.Add(2 * time.Hour)
			preview, err := s.Backfill(ctx, user, project.ID, flow.ID, "0 0 * * * *", "UTC", from, to, true, BackfillOptions{Direction: "backward", FailurePolicy: policy})
			if err != nil || len(preview) != 3 || preview[0] != stamp(to) || preview[2] != stamp(from) {
				t.Fatal(preview, err)
			}
			if err = s.transaction(ctx, func(tx pgx.Tx) error { return s.generateBackfills(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
			runs := runsOf(t, s, user, project.ID)
			if len(runs) != 1 || runs[0].ScheduledAt != stamp(to) {
				t.Fatal(runs)
			}
			run := runs[0]
			run.Status = "failed"
			if err = s.transaction(ctx, func(tx pgx.Tx) error { return writeRun(ctx, tx, run) }); err != nil {
				t.Fatal(err)
			}
			if err = s.transaction(ctx, func(tx pgx.Tx) error { return s.generateBackfills(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
			batches, err := s.ListBackfills(ctx, user, project.ID)
			if err != nil {
				t.Fatal(err)
			}
			if policy == "stop" {
				if batches[0].Status != "failed" || batches[0].CreatedCount != 1 {
					t.Fatal(batches)
				}
			} else {
				if batches[0].Status != "queued" || batches[0].CreatedCount != 2 {
					t.Fatal(batches)
				}
				if err = s.CancelBackfill(ctx, user, project.ID, batches[0].ID); err != nil {
					t.Fatal(err)
				}
				if err = s.transaction(ctx, func(tx pgx.Tx) error { return s.generateBackfills(ctx, tx) }); err != nil {
					t.Fatal(err)
				}
				if len(runsOf(t, s, user, project.ID)) != 2 {
					t.Fatal("cancel generated more runs")
				}
			}
		})
	}
}

func TestPostgresBackfillYieldsToScheduleWithoutPreemption(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	flow.Strategy = "parallel"
	flow, err := s.SaveWorkflow(ctx, user, flow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err = s.Backfill(ctx, user, project.ID, flow.ID, "* * * * * *", "UTC", now, now.Add(2*time.Second), true); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, now); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 1 {
		t.Fatal(apps.submitted)
	}
	var normal domain.WorkflowRun
	if err = s.transaction(ctx, func(tx pgx.Tx) error {
		var e error
		normal, e = s.createRun(ctx, tx, user, flow, now, "schedule", "")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 1 || apps.kills != 0 {
		t.Fatal("preempted backfill", apps.submitted, apps.kills)
	}
	apps.finish("COMPLETED")
	if err = s.tickForTest(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, s, user, project.ID)
	found := false
	for _, r := range runs {
		if r.ID == normal.ID {
			found = r.Status == "running"
		}
	}
	if !found || apps.submitted != 2 {
		t.Fatal("schedule did not run first", runs, apps.submitted)
	}
	if err = s.tickForTest(ctx, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 2 {
		t.Fatal("backfill ran during scheduled instance")
	}
	apps.finish("COMPLETED")
	if err = s.tickForTest(ctx, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 3 {
		t.Fatal("backfill did not resume", apps.submitted)
	}
}
