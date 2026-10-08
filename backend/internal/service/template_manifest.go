package service

import (
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
	"strconv"
)

// NormalizeManifest and RenderManifest are shared by immutable workflow snapshots.
func NormalizeManifest(manifest, namespace string) (string, error) {
	return normalizeTemplateManifest(manifest, namespace)
}
func ValidateWorkflowTemplate(manifest, namespace string, parameters []domain.TemplateParameter) error {
	return validateTemplate(domain.ApplicationTemplate{Name: "workflow task", Manifest: manifest, Namespace: namespace, Parameters: parameters})
}
func RenderManifest(manifest, namespace string, parameters []domain.TemplateParameter, values map[string]string) (string, error) {
	manifest, err := normalizeTemplateManifest(manifest, namespace)
	if err != nil {
		return "", err
	}
	replacements := map[string]any{}
	for _, p := range parameters {
		value, ok := values[p.Name]
		if !ok {
			value = p.Default
		}
		if err := validateParameter(p, value); err != nil {
			return "", err
		}
		var typed any = value
		if value != "" {
			switch p.Type {
			case "integer":
				typed, _ = strconv.Atoi(value)
			case "number":
				typed, _ = strconv.ParseFloat(value, 64)
			case "boolean":
				typed, _ = strconv.ParseBool(value)
			}
		}
		replacements["{{"+p.Name+"}}"] = typed
	}
	var object map[string]any
	if err := yaml.Unmarshal([]byte(manifest), &object); err != nil {
		return "", err
	}
	encoded, err := yaml.Marshal(renderTemplateValue(object, replacements))
	if strings.Contains(string(encoded), "{{") {
		return "", fmt.Errorf("unresolved parameter placeholder")
	}
	return string(encoded), err
}

// Quote standalone placeholders before parsing: YAML otherwise treats {{name}} as a map.
var standalonePlaceholder = regexp.MustCompile(`(?m)(:\s*|^-\s*|^\s+-\s*)(\{\{[A-Za-z][A-Za-z0-9_]*\}\})(\s*(?:#.*)?$)`)

func normalizeTemplateManifest(manifest, namespace string) (string, error) {
	manifest = standalonePlaceholder.ReplaceAllString(manifest, `$1"$2"$3`)
	var object kube.Object
	if err := yaml.UnmarshalStrict([]byte(manifest), &object); err != nil {
		return "", fmt.Errorf("invalid template manifest: %w", err)
	}
	if kube.String(object, "kind") != "SparkApplication" || strings.TrimSpace(kube.String(object, "apiVersion")) == "" {
		return "", fmt.Errorf("template must contain a SparkApplication with an apiVersion")
	}
	name := kube.String(object, "metadata", "name")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("template metadata.name is required")
	}
	if ns := kube.String(object, "metadata", "namespace"); ns != "" && ns != namespace {
		return "", fmt.Errorf("template namespace must match its selected namespace")
	}
	cleanObjectForSubmission(object, namespace, name, "")
	for key := range object {
		if key != "apiVersion" && key != "kind" && key != "metadata" && key != "spec" {
			delete(object, key)
		}
	}
	encoded, err := yaml.Marshal(object)
	return string(encoded), err
}

func renderTemplateValue(value any, replacements map[string]any) any {
	switch x := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range x {
			result[key] = renderTemplateValue(child, replacements)
		}
		return result
	case []any:
		result := make([]any, len(x))
		for i, child := range x {
			result[i] = renderTemplateValue(child, replacements)
		}
		return result
	case string:
		if replacement, ok := replacements[x]; ok {
			return replacement
		}
		for placeholder, replacement := range replacements {
			x = strings.ReplaceAll(x, placeholder, fmt.Sprint(replacement))
		}
		return x
	default:
		return value
	}
}
