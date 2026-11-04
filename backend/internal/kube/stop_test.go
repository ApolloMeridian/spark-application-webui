package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStopUsesAtomicUIDRevisionAndPodPreconditions(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == "PATCH" {
			var ops []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
				t.Error(err)
			}
			if r.Header.Get("Content-Type") != "application/json-patch+json" || ops[0]["op"] != "test" || ops[0]["path"] != "/metadata/uid" {
				t.Error(ops)
			}
			if requests == 1 && (ops[1]["path"] != "/metadata/resourceVersion" || ops[3]["path"] != "/spec/restartPolicy") {
				t.Error(ops)
			}
		} else {
			var body Object
			_ = json.NewDecoder(r.Body).Decode(&body)
			if String(body, "preconditions", "uid") == "" {
				t.Error("missing UID delete precondition")
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, http: server.Client(), allowedLookup: map[string]bool{"spark": true}}
	obj := Object{"metadata": map[string]any{"uid": "app-uid", "resourceVersion": "42", "name": "demo"}}
	if err := c.RequestApplicationStop(context.Background(), "spark", obj); err != nil {
		t.Fatal(err)
	}
	if err := c.StopPodInstance(context.Background(), "spark", Pod{UID: "driver-uid", Name: "demo-driver", Role: "driver"}); err != nil {
		t.Fatal(err)
	}
	if err := c.StopPodInstance(context.Background(), "spark", Pod{UID: "exec-uid", Name: "demo-exec", Role: "executor"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteApplicationInstance(context.Background(), "spark", "demo", "app-uid"); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatal(requests)
	}
	obj["metadata"].(map[string]any)["resourceVersion"] = ""
	if err := c.RequestApplicationStop(context.Background(), "spark", obj); err == nil {
		t.Fatal("unidentified mutation allowed")
	}
}
