package kube

import (
	"context"
	"net/http"
	"time"
)

const StopAnnotation = "spark-control-center.io/stop-requested-at"

// Atomic identity and revision tests prevent mutating a same-name replacement.
// The annotation survives backend restarts and fences operator resubmissions.
func (c *Client) RequestApplicationStop(ctx context.Context, ns string, object Object) error {
	if err := c.ensureNamespace(ns); err != nil {
		return err
	}
	uid, rv := String(object, "metadata", "uid"), String(object, "metadata", "resourceVersion")
	if uid == "" || rv == "" {
		return &APIError{StatusCode: 409, Message: "application identity or revision is unavailable; retry"}
	}
	ops := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": uid}, {"op": "test", "path": "/metadata/resourceVersion", "value": rv}}
	annotations := StringMap(object, "metadata", "annotations")
	if annotations[StopAnnotation] == "" {
		annotations[StopAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	ops = append(ops, map[string]any{"op": "add", "path": "/metadata/annotations", "value": annotations}, map[string]any{"op": "add", "path": "/spec/restartPolicy", "value": map[string]any{"type": "Never"}})
	_, err := c.do(ctx, http.MethodPatch, c.sparkObjectPath(ns, String(object, "metadata", "name")), ops, "application/json-patch+json")
	return err
}

func (c *Client) DeleteApplicationInstance(ctx context.Context, ns, name, uid string) error {
	if err := c.ensureNamespace(ns); err != nil {
		return err
	}
	if uid == "" {
		return &APIError{StatusCode: 409, Message: "application identity is unavailable"}
	}
	body := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": uid}}
	return c.doJSON(ctx, http.MethodDelete, c.sparkObjectPath(ns, name), body, nil)
}

func (c *Client) StopPodInstance(ctx context.Context, ns string, pod Pod) error {
	if err := c.ensureNamespace(ns); err != nil {
		return err
	}
	if pod.UID == "" {
		return &APIError{StatusCode: 409, Message: "Pod identity is unavailable; retry"}
	}
	path := "/api/v1/namespaces/" + ns + "/pods/" + pod.Name
	if pod.Role == "driver" {
		ops := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": pod.UID}, {"op": "add", "path": "/spec/activeDeadlineSeconds", "value": 1}}
		_, err := c.do(ctx, http.MethodPatch, path, ops, "application/json-patch+json")
		return err
	}
	body := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "gracePeriodSeconds": 0, "propagationPolicy": "Background", "preconditions": map[string]any{"uid": pod.UID}}
	return c.doJSON(ctx, http.MethodDelete, path, body, nil)
}
