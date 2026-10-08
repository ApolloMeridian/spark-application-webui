package service

import (
	"encoding/json"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/kube"
)

func TestNormalizeTemplateRemovesRuntimeMetadataFromJSON(t *testing.T) {
	object := runningObject()
	object["metadata"].(map[string]any)["managedFields"] = []any{map[string]any{"f:executorState": "old-executors"}}
	object["metadata"].(map[string]any)["annotations"] = map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "old-manifest", consoleOwnerAnnotation: "previous-user", "custom/config": "keep"}
	raw, _ := json.Marshal(object)
	manifest, err := normalizeTemplateManifest(string(raw), "spark")
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"managedFields", "status:", "last-applied", "previous-user", "old-executors"} {
		if strings.Contains(manifest, removed) {
			t.Fatalf("template retained %q: %s", removed, manifest)
		}
	}
	if !strings.Contains(manifest, "custom/config: keep") || !strings.Contains(manifest, "kind: SparkApplication") {
		t.Fatalf("configuration was lost: %s", manifest)
	}
}

func TestTemplateRenderingPreservesTypesAndEscapesValues(t *testing.T) {
	manifest, err := normalizeTemplateManifest("apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: {{name}}\nspec:\n  executor:\n    instances: {{count}}\n  dynamicAllocation:\n    enabled: '{{enabled}}'\n  arguments: ['{{argument}}']\n", "spark")
	if err != nil {
		t.Fatal(err)
	}
	var object kube.Object
	if err := yaml.Unmarshal([]byte(manifest), &object); err != nil {
		t.Fatal(err)
	}
	rendered := renderTemplateValue(map[string]any(object), map[string]any{"{{name}}": "rendered", "{{count}}": 3, "{{enabled}}": true, "{{argument}}": "a\nstatus: injected"}).(map[string]any)
	encoded, _ := yaml.Marshal(rendered)
	var result kube.Object
	if err := yaml.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if kube.Int64(result, "spec", "executor", "instances") != 3 || kube.String(result, "metadata", "name") != "rendered" || result["status"] != nil {
		t.Fatalf("unexpected rendered template: %#v", result)
	}
	if enabled, _ := kube.Map(result, "spec", "dynamicAllocation"); enabled["enabled"] != true {
		t.Fatalf("boolean parameter lost its type: %#v", enabled)
	}
}
