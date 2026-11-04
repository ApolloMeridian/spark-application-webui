package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func applicationOwnerUID(object Object) string {
	metadata, _ := Map(object, "metadata")
	owners, _ := metadata["ownerReferences"].([]any)
	for _, value := range owners {
		owner, _ := value.(map[string]any)
		if owner["kind"] == "SparkApplication" {
			uid, _ := owner["uid"].(string)
			return uid
		}
	}
	return ""
}

// A CR status field is not authority to proxy arbitrary Services. Require a
// normal ClusterIP Service owned/labeled by this application and selecting its
// actual Driver Pod. ExternalName and endpoint-only Services are never accepted.
func (c *Client) ValidateSparkUIService(ctx context.Context, ns, name string, port int, app Object) error {
	if err := c.ensureNamespace(ns); err != nil {
		return err
	}
	var svc, pod Object
	base := "/api/v1/namespaces/" + url.PathEscape(ns)
	if err := c.doJSON(ctx, http.MethodGet, base+"/services/"+url.PathEscape(name), nil, &svc); err != nil {
		return err
	}
	denied := &APIError{StatusCode: 502, Message: "Spark UI Service does not select the application's Driver"}
	labels := StringMap(svc, "metadata", "labels")
	uid := String(app, "metadata", "uid")
	if owner := applicationOwnerUID(svc); owner != "" && owner != uid {
		return denied
	}
	if String(svc, "spec", "type") == "ExternalName" || (applicationOwnerUID(svc) != uid || uid == "") && labels["sparkoperator.k8s.io/app-name"] != String(app, "metadata", "name") {
		return denied
	}
	driver := String(app, "status", "driverInfo", "podName")
	if driver == "" {
		return denied
	}
	if err := c.doJSON(ctx, http.MethodGet, base+"/pods/"+url.PathEscape(driver), nil, &pod); err != nil {
		return err
	}
	podLabels := StringMap(pod, "metadata", "labels")
	if owner := applicationOwnerUID(pod); owner != "" && owner != uid {
		return denied
	}
	if podLabels["spark-role"] != "driver" || (applicationOwnerUID(pod) != uid || uid == "") && podLabels["sparkoperator.k8s.io/app-name"] != String(app, "metadata", "name") {
		return denied
	}
	selector := StringMap(svc, "spec", "selector")
	if len(selector) == 0 || selector["spark-role"] != "driver" {
		return denied
	}
	identity := selector["spark-app-selector"] != "" || selector["sparkoperator.k8s.io/app-name"] == String(app, "metadata", "name")
	if !identity {
		return denied
	}
	for k, v := range selector {
		if podLabels[k] != v {
			return denied
		}
	}
	spec, _ := Map(svc, "spec")
	ports, _ := spec["ports"].([]any)
	for _, value := range ports {
		p, _ := value.(map[string]any)
		if int(Int64(Object(p), "port")) == port {
			return nil
		}
	}
	return fmt.Errorf("%w: UI port is not exposed by the Service", denied)
}
