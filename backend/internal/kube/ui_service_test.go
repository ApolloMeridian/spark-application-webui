package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSparkUIServiceOwnershipAndSelector(t *testing.T) {
	for _, kind := range []string{"valid", "external", "foreign-owner", "broad-selector", "wrong-driver", "wrong-port"} {
		t.Run(kind, func(t *testing.T) {
			app := Object{"metadata": map[string]any{"name": "etl", "uid": "app-uid"}, "status": map[string]any{"driverInfo": map[string]any{"podName": "etl-driver"}}}
			service := Object{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"kind": "SparkApplication", "uid": "app-uid"}}}, "spec": map[string]any{"type": "ClusterIP", "selector": map[string]any{"spark-role": "driver", "spark-app-selector": "spark-id"}, "ports": []any{map[string]any{"port": float64(4040)}}}}
			pod := Object{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"kind": "SparkApplication", "uid": "app-uid"}}, "labels": map[string]any{"spark-role": "driver", "spark-app-selector": "spark-id"}}}
			switch kind {
			case "external":
				service["spec"].(map[string]any)["type"] = "ExternalName"
			case "foreign-owner":
				service["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"kind": "SparkApplication", "uid": "other"}}
			case "broad-selector":
				service["spec"].(map[string]any)["selector"] = map[string]any{"spark-role": "driver"}
			case "wrong-driver":
				pod["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"kind": "SparkApplication", "uid": "other"}}
			case "wrong-port":
				service["spec"].(map[string]any)["ports"] = []any{map[string]any{"port": float64(8080)}}
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/namespaces/spark/services/etl-ui" {
					json.NewEncoder(w).Encode(service)
				} else {
					json.NewEncoder(w).Encode(pod)
				}
			}))
			defer srv.Close()
			client := &Client{baseURL: srv.URL, http: srv.Client(), allowedLookup: map[string]bool{"spark": true}}
			err := client.ValidateSparkUIService(context.Background(), "spark", "etl-ui", 4040, app)
			if (err == nil) != (kind == "valid") {
				t.Fatal(kind, err)
			}
		})
	}
}
