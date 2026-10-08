package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"spark-control-center/backend/internal/domain"
)

func TestPostgresTemplateNodeDateDefaultsKeepConstraints(t *testing.T) {
	s, user, project, _ := fixture(t)
	ctx := context.Background()
	parameters := []domain.TemplateParameter{{Name: "businessDate", Type: "string", Default: "$[yyyy-MM-dd]", Pattern: "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", Required: true}}
	template, err := s.store.SaveTemplate(ctx, domain.ApplicationTemplate{Name: "Date template", Namespace: "spark", Manifest: strings.Replace(testManifest, "$[yyyy-MM-dd-1]", "{{businessDate}}", 1), Parameters: parameters, CreatedBy: user.Username})
	if err != nil {
		t.Fatal(err)
	}
	// A template node customizes defaults, not type/pattern/required constraints.
	flow, err := s.SaveWorkflow(ctx, user, domain.Workflow{ProjectID: project.ID, Name: "Date pipeline", Enabled: true, Strategy: "parallel", TimeZone: "UTC", MaxParallel: 1, Nodes: []domain.WorkflowNode{{ID: "source", Name: "Source", Namespace: "spark", TemplateID: template.ID, TemplateVersion: 1, Strategy: "parallel", Parameters: []domain.TemplateParameter{{Name: "businessDate", Type: "string", Default: "$[yyyy-MM-dd-1]"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if flow.Nodes[0].Parameters[0].Pattern != parameters[0].Pattern || !flow.Nodes[0].Parameters[0].Required {
		t.Fatal("template constraints lost", flow)
	}
	manifest, _, err := RenderNode(flow.Nodes[0], time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), "UTC", "preview-12345678", 1)
	if err != nil || !strings.Contains(manifest, "2026-10-07") {
		t.Fatal(manifest, err)
	}
}
