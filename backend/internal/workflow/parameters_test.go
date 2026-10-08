package workflow

import (
	"spark-control-center/backend/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestDateExpressions(t *testing.T) {
	baseline := time.Date(2026, 3, 1, 0, 15, 12, 123000000, time.FixedZone("CST", 8*3600))
	for _, test := range []struct{ value, expected string }{
		{"$[yyyy-MM-dd-1]", "2026-02-28"}, {"$[yyyy-MM-dd HH:mm:ss-1/24]", "2026-02-28 23:15:12"}, {"$[yyyyMMddHHmmssSSS]", "20260301001512123"}, {"$[yyyy-MM-dd+1]", "2026-03-02"},
	} {
		got, err := ExpandDates(test.value, baseline, "Asia/Shanghai")
		if err != nil || got != test.expected {
			t.Fatalf("%s got=%s err=%v", test.value, got, err)
		}
	}
	for _, value := range []string{"$[yyyy-MM-dd-1/0]", "$[garbage]", "$[yyyy-MM-dd"} {
		if _, err := ExpandDates(value, baseline, "UTC"); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	// Calendar -1 retains local wall time across DST rather than subtracting 24h.
	loc, _ := time.LoadLocation("America/New_York")
	got, err := ExpandDates("$[yyyy-MM-dd HH:mm-1]", time.Date(2026, 3, 9, 1, 0, 0, 0, loc), loc.String())
	if err != nil || got != "2026-03-08 01:00" {
		t.Fatalf("DST got=%s err=%v", got, err)
	}
}
func TestCronTimezoneAndValidation(t *testing.T) {
	from := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	times, err := CronPreview("0 2 * * *", "Asia/Shanghai", from, 2)
	if err != nil || times[0] != "2026-02-28T18:00:00Z" {
		t.Fatalf("times=%v err=%v", times, err)
	}
	leap, err := CronPreview("0 0 29 2 *", "UTC", from, 1)
	if err != nil || leap[0] != "2028-02-29T00:00:00Z" {
		t.Fatalf("leap %v %v", leap, err)
	}
	for _, expression := range []string{"* * * * * *", "61 * * * *", "@hourly"} {
		if _, err := ParseCron(expression, "UTC"); err == nil {
			t.Errorf("accepted %s", expression)
		}
	}
	if _, err := ParseCron("* * * * *", "Mars/Base"); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}

const testManifest = "apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: example\nspec:\n  image: apache/spark:3.5.3\n  arguments: [\"$[yyyy-MM-dd-1]\"]\n  restartPolicy:\n    type: Never\n"

func TestNamesAndTypedRendering(t *testing.T) {
	node := domain.WorkflowNode{Namespace: "spark", Manifest: testManifest}
	at := time.Date(2026, 10, 8, 2, 0, 0, 123000000, time.UTC)
	manifest, name, err := RenderNode(node, at, "UTC", "task-12345678", 1)
	if err != nil || !strings.Contains(name, "20261008020000123-12345678-1") || !strings.Contains(manifest, "2026-10-07") {
		t.Fatalf("name=%s manifest=%s err=%v", name, manifest, err)
	}
	no := false
	node.NameSuffix = &no
	_, name, err = RenderNode(node, at, "UTC", "task-12345678", 1)
	if err != nil || name != "example" {
		t.Fatal(name, err)
	}
	node.Manifest = strings.Replace(testManifest, "example", strings.Repeat("a", 63), 1)
	node.NameSuffix = nil
	_, name, err = RenderNode(node, at, "UTC", "task-12345678", 1)
	if err != nil || len(name) > 63 {
		t.Fatal(name, err)
	}
}
func TestConcurrencyAndTenantVisibility(t *testing.T) {
	old := domain.WorkflowRun{ID: "older", WorkflowID: "flow", CreatedAt: "1", Status: "running", Tasks: []domain.TaskRun{{NodeID: "node", Status: "running"}}}
	next := domain.WorkflowRun{ID: "next", WorkflowID: "flow", CreatedAt: "2", Status: "queued"}
	if !busyWorkflow(&next, []domain.WorkflowRun{old, next}) || !busyNode(&next, "node", []domain.WorkflowRun{old, next}) {
		t.Fatal("active predecessor not detected")
	}
	old.Status = "succeeded"
	if busyWorkflow(&next, []domain.WorkflowRun{old, next}) {
		t.Fatal("terminal predecessor blocks")
	}
	flow := domain.Workflow{Nodes: []domain.WorkflowNode{{Namespace: "spark"}, {Namespace: "other"}}}
	if visible(domain.User{Namespaces: []string{"spark"}}, flow) {
		t.Fatal("mixed namespace DAG leaked")
	}
}
