package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"spark-control-center/backend/internal/store"
)

type fakeApps struct {
	mu        sync.Mutex
	apps      map[string]domain.SparkApplication
	submitted int
	dryRuns   int
	kills     int
	crash     context.CancelFunc
}

func (f *fakeApps) GetWorkflowApplication(_ context.Context, ns, name string) (domain.SparkApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.apps[ns+"/"+name]
	if !ok {
		return x, &kube.APIError{StatusCode: 404, Message: "not found"}
	}
	return x, nil
}
func (f *fakeApps) DryRunApplication(_ context.Context, ns, manifest, operator string) (domain.ManifestPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dryRuns++
	return domain.ManifestPreview{ServerYAML: manifest, DryRunAccepted: true}, nil
}
func (f *fakeApps) SubmitApplication(_ context.Context, ns, manifest, operator string) (domain.SparkApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var object kube.Object
	_ = yaml.Unmarshal([]byte(manifest), &object)
	name := kube.String(object, "metadata", "name")
	key := ns + "/" + name
	if _, exists := f.apps[key]; exists {
		return domain.SparkApplication{}, &kube.APIError{StatusCode: 409, Message: "already exists"}
	}
	x := domain.SparkApplication{Name: name, Namespace: ns, State: "RUNNING", YAML: manifest}
	f.apps[key] = x
	f.submitted++
	if f.crash != nil {
		f.crash()
		f.crash = nil
		return x, context.Canceled
	}
	return x, nil
}
func (f *fakeApps) KillApplication(_ context.Context, ns, name, operator, reason string) (domain.OperationAudit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills++
	return domain.OperationAudit{}, nil
}
func (f *fakeApps) finish(state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, x := range f.apps {
		x.State = state
		f.apps[key] = x
	}
}
func fixture(t *testing.T) (*Service, domain.User, domain.Project, *fakeApps) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	// Each fixture gets a private schema: other packages may concurrently migrate
	// the integration database and must not interfere with scheduler lock tests.
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := id("workflow_test")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+quotedSchema+" CASCADE"); _ = admin.Close(ctx) })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	dsn = parsed.String()
	pg, err := store.Open(ctx, dsn, 6)
	if err != nil {
		t.Fatal(err)
	}
	apps := &fakeApps{apps: map[string]domain.SparkApplication{}}
	cfg := config.Config{Namespaces: []string{"spark"}, WorkflowsEnabled: true, SchedulerEnabled: true, SchedulerInterval: 5 * time.Second, WorkflowTimeZone: "UTC"}
	s := New(pg, apps, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := domain.User{ID: id("user"), Username: id("test"), DisplayName: "Test", Role: domain.RoleAdmin, AuthSource: "local", CreatedAt: stamp(time.Now()), UpdatedAt: stamp(time.Now())}
	if err = pg.CreateUser(ctx, user, user.Username, "unused-test-hash"); err != nil {
		t.Fatal(err)
	}
	x, err := s.SaveTenant(ctx, user, domain.Tenant{Name: id("test-tenant")})
	if err != nil {
		t.Fatal(err)
	}
	user.TenantID = x.ID
	if err = pg.UpdateUser(ctx, user, nil); err != nil {
		t.Fatal(err)
	}
	project, err := s.SaveProject(ctx, user, domain.Project{Name: "Data platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM workflow_resources WHERE tenant_id=$1;`, user.TenantID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM workflow_runs WHERE project_id=$1`, project.ID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM workflow_schedules WHERE project_id=$1`, project.ID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM workflows WHERE project_id=$1`, project.ID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM local_users WHERE id=$1`, user.ID)
		_, _ = pg.WorkflowPool().Exec(ctx, `DELETE FROM tenants WHERE id=$1`, user.TenantID)
		pg.Close()
	})
	return s, user, project, apps
}
func single(t *testing.T, s *Service, user domain.User, project domain.Project) domain.Workflow {
	t.Helper()
	flow, err := s.SaveWorkflow(context.Background(), user, domain.Workflow{ProjectID: project.ID, Name: "Daily pipeline", Enabled: true, TimeZone: "UTC", Strategy: "serial_wait", MaxParallel: 10, Nodes: []domain.WorkflowNode{{ID: "extract", Name: "Extract", Namespace: "spark", Manifest: testManifest, Strategy: "parallel"}}})
	if err != nil {
		t.Fatal(err)
	}
	return flow
}
func runsOf(t *testing.T, s *Service, user domain.User, project string) []domain.WorkflowRun {
	t.Helper()
	x, err := s.ListRuns(context.Background(), user, project, false)
	if err != nil {
		t.Fatal(err)
	}
	return x.([]domain.WorkflowRun)
}

// Tick deliberately skips a contended HA lock. Wait for a committed tick in
// tests, since concurrent package migrations use the same database-level lock.
func (s *Service) tickForTest(ctx context.Context, at time.Time) error {
	var before, after int64
	if err := s.pool.QueryRow(ctx, `SELECT epoch FROM workflow_scheduler_state WHERE id=1`).Scan(&before); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.Tick(ctx, at); err != nil {
			return err
		}
		if err := s.pool.QueryRow(ctx, `SELECT epoch FROM workflow_scheduler_state WHERE id=1`).Scan(&after); err != nil {
			return err
		}
		if after > before {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("scheduler test lock timed out")
}
func TestPostgresVersionsTenantAndRecovery(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	other := user
	other.TenantID = "default"
	if _, err := s.ListWorkflows(ctx, other, project.ID); err == nil {
		t.Fatal("cross tenant admin access allowed")
	}
	viewer := user
	viewer.Role = domain.RoleViewer
	if _, err := s.SaveWorkflow(ctx, viewer, flow); err == nil {
		t.Fatal("viewer mutation allowed")
	}
	flow.Name = "Daily pipeline edited"
	flow, err := s.SaveWorkflow(ctx, user, flow)
	if err != nil || flow.Version != 2 {
		t.Fatal(flow, err)
	}
	restored, err := s.VersionAction(ctx, user, project.ID, flow.ID, 1, false)
	if err != nil || restored.Version != 1 || restored.Name != "Daily pipeline" {
		t.Fatal(restored, err)
	}
	if _, err = s.VersionAction(ctx, user, project.ID, flow.ID, 1, true); err == nil {
		t.Fatal("deleted current version")
	}
	if _, err = s.VersionAction(ctx, user, project.ID, flow.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_versions WHERE workflow_id=$1 AND version=2`, flow.ID).Scan(&count)
	if count != 0 {
		t.Fatal("version not physically removed")
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_versions WHERE workflow_id=$1`, flow.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("restore created a version", count, err)
	}
	flow, err = s.SaveWorkflow(ctx, user, restored)
	if err != nil || flow.Version != 3 {
		t.Fatal("historical edit reused a deleted version number", flow, err)
	}
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	crashCtx, cancel := context.WithCancel(ctx)
	apps.crash = cancel
	if err = s.tickForTest(crashCtx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("crash tick err=%v", err)
	}
	// A different service instance has no process-local execution state.
	restarted := New(s.store, apps, s.cfg, s.logger)
	if err = restarted.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 1 {
		t.Fatalf("duplicate submission=%d", apps.submitted)
	}
	rows := runsOf(t, restarted, user, project.ID)
	if rows[0].ID != run.ID || rows[0].Status != "running" || rows[0].Version != 3 {
		t.Fatal(rows)
	}
	apps.finish("COMPLETED")
	if err = restarted.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runsOf(t, s, user, project.ID)[0].Status != "succeeded" {
		t.Fatal("not completed")
	}
	if err = s.RunAction(ctx, user, project.ID, run.ID, "delete"); err != nil {
		t.Fatal(err)
	}
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM workflow_task_runs WHERE run_id=$1`, run.ID).Scan(&count)
	if count != 0 {
		t.Fatal("tasks not cascade deleted")
	}
}

func TestPostgresCancelAtGlobalTaskLimit(t *testing.T) {
	s, user, project, apps := fixture(t)
	s.cfg.WorkflowMaxTasks = 1
	ctx := context.Background()
	flow := single(t, s, user, project)
	first, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.RunAction(ctx, user, project.ID, first.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.kills != 1 || apps.submitted != 1 {
		t.Fatalf("cancel must work at capacity: kills=%d submitted=%d", apps.kills, apps.submitted)
	}
	apps.finish("FAILED")
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 2 {
		t.Fatalf("released slot not reused: submitted=%d", apps.submitted)
	}
}
func TestPostgresDAGParallelPauseCancelAndHA(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	for i := 1; i < 20; i++ {
		deps := []string{}
		if i >= 10 {
			deps = []string{fmt.Sprintf("task-%d", i-10)}
		}
		if i == 10 {
			deps = []string{"extract"}
		}
		flow.Nodes = append(flow.Nodes, domain.WorkflowNode{ID: fmt.Sprintf("task-%d", i), Name: fmt.Sprintf("Task %d", i), Namespace: "spark", Manifest: testManifest, DependsOn: deps, Strategy: "parallel"})
	}
	flow, err := s.SaveWorkflow(ctx, user, flow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); errs <- s.tickForTest(ctx, time.Now()) }()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if apps.submitted != 10 {
		t.Fatalf("parallel roots=%d", apps.submitted)
	}
	if err = s.RunAction(ctx, user, project.ID, run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	apps.finish("COMPLETED")
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 10 {
		t.Fatal("dispatched while paused")
	}
	if err = s.RunAction(ctx, user, project.ID, run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 20 {
		t.Fatalf("dependent submissions=%d", apps.submitted)
	}
	if err = s.RunAction(ctx, user, project.ID, run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runsOf(t, s, user, project.ID)[0].Status != "cancelling" {
		t.Fatal("cancel falsely terminal before Kubernetes")
	}
	apps.finish("FAILED")
	if err = s.tickForTest(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runsOf(t, s, user, project.ID)[0].Status != "cancelled" {
		t.Fatal("cancel not completed")
	}
}
func TestPostgresCronDedupBackfillAndRoleRevocation(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	schedule, err := s.SaveSchedule(ctx, user, domain.WorkflowSchedule{ProjectID: project.ID, WorkflowID: flow.ID, Name: "Daily", Cron: "* * * * *", TimeZone: "UTC", Enabled: true, Misfire: "catch_up"})
	if err != nil {
		t.Fatal(err)
	}
	due, _ := time.Parse(time.RFC3339Nano, schedule.NextRunAt)
	if err = s.tickForTest(ctx, due); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, due); err != nil {
		t.Fatal(err)
	}
	rows := runsOf(t, s, user, project.ID)
	if len(rows) != 1 || rows[0].ScheduledAt != stamp(due) {
		t.Fatal("duplicate schedule", rows)
	}
	preview, err := s.Backfill(ctx, user, project.ID, flow.ID, "* * * * *", "UTC", due, due.Add(time.Minute), false)
	if err != nil || len(preview) != 2 || len(runsOf(t, s, user, project.ID)) != 1 {
		t.Fatal(preview, err)
	}
	if _, err = s.Backfill(ctx, user, project.ID, flow.ID, "* * * * *", "UTC", due, due.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	user.Disabled = true
	backup := user
	backup.ID = id("user")
	backup.Username = id("backup")
	backup.Disabled = false
	backup.TenantID = "default"
	if err = s.store.CreateUser(ctx, backup, backup.Username, "unused-test-hash"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(ctx, `DELETE FROM local_users WHERE id=$1`, backup.ID) })
	if err = s.store.UpdateUser(ctx, user, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, due.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	schedules, err := s.ListSchedules(ctx, user, project.ID)
	if err != nil || schedules[0].Enabled {
		t.Fatal("revoked owner schedule not blocked", err)
	}
	backfills, err := s.ListBackfills(ctx, user, project.ID)
	if err != nil || len(backfills) != 1 || backfills[0].Status != "blocked" {
		t.Fatal("revoked owner backfill not blocked", backfills, err)
	}
}

func TestPostgresSecondCronFireOnceAndWindow(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	start := time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
	end := start.Add(12 * time.Second)
	schedule, err := s.SaveSchedule(ctx, user, domain.WorkflowSchedule{ProjectID: project.ID, WorkflowID: flow.ID, Name: "Second schedule", Cron: "*/5 * * * * *", TimeZone: "UTC", Enabled: true, Misfire: "fire_once", StartsAt: stamp(start), EndsAt: stamp(end)})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.NextRunAt != stamp(start) {
		t.Fatalf("inclusive start lost: %s", schedule.NextRunAt)
	}
	// Years of downtime must select only the latest occurrence inside the window.
	now := start.AddDate(5, 0, 0)
	if err = s.tickForTest(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, now); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, s, user, project.ID)
	if len(runs) != 1 || runs[0].ScheduledAt != stamp(start.Add(10*time.Second)) {
		t.Fatalf("unexpected second-level occurrences: %#v", runs)
	}
	schedules, err := s.ListSchedules(ctx, user, project.ID)
	if err != nil || len(schedules) != 1 || schedules[0].Enabled {
		t.Fatalf("expired schedule still enabled: %#v, %v", schedules, err)
	}
}

func TestPostgresTemplateHistoryAndFrozenWorkflow(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	template, err := s.store.SaveTemplate(ctx, domain.ApplicationTemplate{Name: "Reusable", Namespace: "spark", Manifest: testManifest, Parameters: []domain.TemplateParameter{}, CreatedBy: user.Username})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := s.SaveWorkflow(ctx, user, domain.Workflow{ProjectID: project.ID, Name: "Frozen template", Enabled: true, Strategy: "parallel", TimeZone: "UTC", MaxParallel: 1, Nodes: []domain.WorkflowNode{{ID: "source", Name: "Source", Namespace: "spark", TemplateID: template.ID, TemplateVersion: 1, Strategy: "parallel"}}})
	if err != nil {
		t.Fatal(err)
	}
	template.Manifest = testManifest + "  sparkVersion: 3.5.3\n"
	template, err = s.store.SaveTemplate(ctx, template)
	if err != nil || template.Version != 2 {
		t.Fatal(template, err)
	}
	restored, err := s.TemplateVersionAction(ctx, user, template.ID, 1, false)
	if err != nil || restored.Version != 1 || restored.Manifest != testManifest || len(restored.Versions) != 2 {
		t.Fatal(restored, err)
	}
	if _, err = s.TemplateVersionAction(ctx, user, template.ID, 1, true); err == nil {
		t.Fatal("deleted current template version")
	}
	if _, err = s.TemplateVersionAction(ctx, user, template.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	template, err = s.store.SaveTemplate(ctx, restored)
	if err != nil || template.Version != 3 {
		t.Fatal("template edit reused a deleted version number", template, err)
	}
	if _, err = s.TemplateVersionAction(ctx, user, template.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM application_template_versions WHERE template_id=$1 AND version=1`, template.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = s.TemplateVersionAction(ctx, user, template.ID, 3, true); err == nil {
		t.Fatal("deleted current template version")
	}
	if _, err = s.Trigger(ctx, user, project.ID, flow.ID, time.Now(), nil); err != nil {
		t.Fatalf("deleted library version changed frozen workflow: %v", err)
	}
	viewer := user
	viewer.Role = domain.RoleViewer
	if _, err = s.TemplateVersionAction(ctx, viewer, template.ID, 2, true); err == nil {
		t.Fatal("viewer deleted version")
	}
}

func TestPostgresRetryAndForeignCollision(t *testing.T) {
	s, user, project, apps := fixture(t)
	ctx := context.Background()
	flow := single(t, s, user, project)
	flow.Nodes[0].Retries = 1
	flow, err := s.SaveWorkflow(ctx, user, flow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = s.Trigger(ctx, user, project.ID, flow.ID, now, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, now); err != nil {
		t.Fatal(err)
	}
	apps.finish("FAILED")
	if err = s.tickForTest(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 1 {
		t.Fatal("retry ignored backoff")
	}
	if err = s.tickForTest(ctx, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 2 {
		t.Fatal("retry not dispatched")
	}
	apps.finish("COMPLETED")
	if err = s.tickForTest(ctx, now.Add(13*time.Second)); err != nil {
		t.Fatal(err)
	}
	noSuffix := false
	flow.Nodes[0].NameSuffix = &noSuffix
	flow.Nodes[0].Retries = 0
	flow, err = s.SaveWorkflow(ctx, user, flow)
	if err != nil {
		t.Fatal(err)
	}
	apps.apps["spark/example"] = domain.SparkApplication{Name: "example", Namespace: "spark", State: "RUNNING", YAML: testManifest}
	if _, err = s.Trigger(ctx, user, project.ID, flow.ID, now.Add(14*time.Second), nil); err != nil {
		t.Fatal(err)
	}
	if err = s.tickForTest(ctx, now.Add(14*time.Second)); err != nil {
		t.Fatal(err)
	}
	if apps.submitted != 2 || apps.kills != 0 {
		t.Fatal("foreign application was modified")
	}
	rows := runsOf(t, s, user, project.ID)
	if rows[0].Status != "failed" {
		t.Fatal("collision not reported", rows[0])
	}
}
