package workflow

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"sync"
	"testing"
	"time"

	"spark-control-center/backend/internal/domain"
)

type gatedApps struct {
	*fakeApps
	entered, release chan struct{}
	gateSubmit       bool
	once             sync.Once
}

func (g *gatedApps) gate(ctx context.Context) error {
	var err error
	g.once.Do(func() {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}
func (g *gatedApps) DryRunApplication(ctx context.Context, ns, manifest, operator string) (domain.ManifestPreview, error) {
	if !g.gateSubmit {
		if err := g.gate(ctx); err != nil {
			return domain.ManifestPreview{}, err
		}
	}
	return g.fakeApps.DryRunApplication(ctx, ns, manifest, operator)
}
func (g *gatedApps) SubmitApplication(ctx context.Context, ns, manifest, operator string) (domain.SparkApplication, error) {
	app, err := g.fakeApps.SubmitApplication(ctx, ns, manifest, operator)
	if err == nil && g.gateSubmit {
		err = g.gate(ctx)
	}
	return app, err
}
func awaitGate(t *testing.T, g *gatedApps) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC did not start")
	}
}

func TestEffectRPCReleasesAllConnectionsAndPauseWinsDryRun(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &gatedApps{fakeApps: apps, entered: make(chan struct{}), release: make(chan struct{})}
	s.apps = g
	done := make(chan error, 1)
	go func() { done <- s.Tick(ctx, time.Now()) }()
	awaitGate(t, g)
	// Acquiring the entire pool proves no connection/transaction spans the RPC.
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	connections := make([]func(), 0)
	for i := int32(0); i < s.pool.Config().MaxConns; i++ {
		connection, acquireErr := s.pool.Acquire(bounded)
		if acquireErr != nil {
			for _, release := range connections {
				release()
			}
			close(g.release)
			<-done
			t.Fatal("RPC retained a DB connection", acquireErr)
		}
		connections = append(connections, connection.Release)
	}
	for _, release := range connections {
		release()
	}
	if err = s.RunAction(bounded, user, project.ID, run.ID, "pause"); err != nil {
		close(g.release)
		<-done
		t.Fatal("pause blocked behind RPC", err)
	}
	close(g.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 0 {
		t.Fatal("submitted after pause during dry-run")
	}
	current := runsOf(t, s, user, project.ID)[0]
	if current.Status != "paused" || current.Tasks[0].Status != "queued" {
		t.Fatal(current)
	}
	if err = s.RunAction(ctx, user, project.ID, run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx, time.Now()); err != nil || apps.submitted != 1 {
		t.Fatal("paused durable intent was not resumable", err, apps.submitted)
	}
}

func TestCancelDuringAcceptedCreateCompensatesWithoutOverwritingCancellation(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &gatedApps{fakeApps: apps, gateSubmit: true, entered: make(chan struct{}), release: make(chan struct{})}
	s.apps = g
	done := make(chan error, 1)
	go func() { done <- s.Tick(ctx, time.Now()) }()
	awaitGate(t, g)
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = s.RunAction(bounded, user, project.ID, run.ID, "cancel"); err != nil {
		close(g.release)
		<-done
		t.Fatal(err)
	}
	second := New(s.store, apps, s.cfg, s.logger)
	if err = second.Tick(bounded, time.Now()); err != nil {
		close(g.release)
		<-done
		t.Fatal(err)
	}
	if apps.submitted != 1 || apps.kills != 0 {
		t.Fatal("second replica stole an unexpired claim", apps.submitted, apps.kills)
	}
	close(g.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	current := runsOf(t, s, user, project.ID)[0]
	if current.Status != "cancelling" || !current.Tasks[0].CancelRequested || apps.kills != 1 {
		t.Fatal("cancellation lost or accepted CREATE was orphaned", current, apps.kills)
	}
	apps.finish("FAILED")
	if err = second.Tick(ctx, time.Now()); err != nil || runsOf(t, s, user, project.ID)[0].Status != "cancelled" {
		t.Fatal("cancellation not reconciled", err)
	}
}

func TestRevocationDuringDryRunPreventsCreate(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	if _, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	g := &gatedApps{fakeApps: apps, entered: make(chan struct{}), release: make(chan struct{})}
	s.apps = g
	done := make(chan error, 1)
	go func() { done <- s.Tick(ctx, time.Now()) }()
	awaitGate(t, g)
	user.Disabled = true
	backup := user
	backup.ID, backup.Username, backup.Disabled = id("backup"), id("backup"), false
	if err := s.store.CreateUser(ctx, backup, backup.Username, "unused-test-hash"); err != nil {
		close(g.release)
		<-done
		t.Fatal(err)
	}
	if err := s.store.UpdateUser(ctx, user, nil); err != nil {
		close(g.release)
		<-done
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 0 || apps.kills != 0 {
		t.Fatal("revoked account dispatched a workload", apps.submitted, apps.kills)
	}
	user.Namespaces = nil
	user.Disabled = false
	if runsOf(t, s, user, project.ID)[0].Status != "failed" {
		t.Fatal("revocation was not persisted")
	}
}

func TestExpiredEffectClaimFencesOldWorkerAndRecoversAcceptedCreate(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	if _, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.planTick(ctx, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	old, err := s.claimEffect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.performEffect(ctx, old)
	if err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	// Simulate process death after CREATE but before acknowledgement.
	if _, err = s.pool.Exec(ctx, `UPDATE workflow_effects SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.store, apps, s.cfg, s.logger)
	newClaim, err := restarted.claimEffect(ctx)
	if err != nil || newClaim.Token == old.Token {
		t.Fatal(newClaim, err)
	}
	if err = s.ackEffect(ctx, old, effectResult{TerminalFailure: true, Message: "stale worker"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runsOf(t, s, user, project.ID)[0].Tasks[0].Status != "submitting" {
		t.Fatal("stale worker overwrote the task")
	}
	result, err = restarted.performEffect(ctx, newClaim)
	if err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	if err = restarted.ackEffect(ctx, newClaim, result, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 1 || runsOf(t, s, user, project.ID)[0].Tasks[0].Status != "running" {
		t.Fatal("accepted CREATE was submitted twice", apps.submitted)
	}
	// Completed intents cannot be claimed again after a restart.
	if _, err = s.claimEffect(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("completed effect was reclaimed", err)
	}
}
