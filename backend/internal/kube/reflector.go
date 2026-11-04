package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"spark-control-center/backend/internal/domain"
)

// One LIST/WATCH reflector per namespace and Client, shared by all callers.
// Only complete lists are published; disconnects and 410 invalidate the cache.
type namespaceReflector struct {
	mu      sync.RWMutex
	objects map[string]Object
	ready   bool
	running bool
}

func (c *Client) reflector(ns string) *namespaceReflector {
	c.reflectMu.Lock()
	defer c.reflectMu.Unlock()
	if c.reflectors == nil {
		c.reflectors = map[string]*namespaceReflector{}
	}
	if c.reflectors[ns] == nil {
		c.reflectors[ns] = &namespaceReflector{}
	}
	return c.reflectors[ns]
}
func cloneObject(o Object) Object {
	b, _ := json.Marshal(o)
	var x Object
	_ = json.Unmarshal(b, &x)
	return x
}
func (c *Client) reflectedApplications(ns string) ([]Object, bool) {
	r := c.reflector(ns)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ready {
		return nil, false
	}
	result := make([]Object, 0, len(r.objects))
	for _, o := range r.objects {
		result = append(result, cloneObject(o))
	}
	return result, true
}
func (c *Client) listApplicationSnapshot(ctx context.Context, ns string) ([]Object, string, error) {
	items := []Object{}
	token, rv := "", ""
	seen := map[string]bool{}
	for {
		q := url.Values{"limit": {"500"}}
		if token != "" {
			q.Set("continue", token)
		}
		var page struct {
			Items    []Object `json:"items"`
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
				Continue        string `json:"continue"`
			} `json:"metadata"`
		}
		if err := c.doJSON(ctx, http.MethodGet, c.sparkCollectionPath(ns)+"?"+q.Encode(), nil, &page); err != nil {
			return nil, "", err
		}
		if rv != "" && page.Metadata.ResourceVersion != rv {
			return nil, "", fmt.Errorf("inconsistent Kubernetes list resourceVersion")
		}
		rv = page.Metadata.ResourceVersion
		items = append(items, page.Items...)
		token = page.Metadata.Continue
		if token == "" {
			return items, rv, nil
		}
		if seen[token] {
			return nil, "", fmt.Errorf("repeated Kubernetes continue token")
		}
		seen[token] = true
	}
}
func (c *Client) watchReflected(ctx context.Context, ns string, notify func(domain.ApplicationChange)) error {
	if err := c.ensureNamespace(ns); err != nil {
		return err
	}
	r := c.reflector(ns)
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return fmt.Errorf("namespace watch already running")
	}
	r.running = true
	r.ready = false
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.ready = false; r.running = false; r.mu.Unlock() }()
	items, rv, err := c.listApplicationSnapshot(ctx, ns)
	if err != nil {
		return err
	}
	objects := map[string]Object{}
	for _, o := range items {
		objects[String(o, "metadata", "name")] = o
	}
	r.mu.Lock()
	r.objects = objects
	r.ready = true
	r.mu.Unlock()
	// A relist may discover missed deletes: invalidate list consumers even if empty.
	notify(domain.ApplicationChange{Type: "SYNC", Namespace: ns, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
	q := url.Values{"watch": {"true"}, "allowWatchBookmarks": {"true"}, "timeoutSeconds": {"300"}}
	if rv != "" {
		q.Set("resourceVersion", rv)
	}
	step, cancel := context.WithTimeout(ctx, 310*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(step, http.MethodGet, c.baseURL+c.sparkCollectionPath(ns)+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	token, err := c.bearerToken()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Transport: c.http.Transport}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return &APIError{StatusCode: response.StatusCode, Message: "Kubernetes watch failed"}
	}
	dec := json.NewDecoder(response.Body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object Object `json:"object"`
		}
		if err = dec.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if event.Type == "ERROR" {
			return &APIError{StatusCode: int(Int64(event.Object, "code")), Message: "Kubernetes watch expired or failed"}
		}
		if event.Type == "BOOKMARK" {
			continue
		}
		name := String(event.Object, "metadata", "name")
		if name == "" {
			return fmt.Errorf("watch object missing name")
		}
		r.mu.Lock()
		switch event.Type {
		case "ADDED", "MODIFIED":
			r.objects[name] = event.Object
		case "DELETED":
			if String(r.objects[name], "metadata", "uid") == String(event.Object, "metadata", "uid") {
				delete(r.objects, name)
			}
		}
		r.mu.Unlock()
		notify(domain.ApplicationChange{Type: event.Type, Namespace: ns, Name: name, ResourceVersion: String(event.Object, "metadata", "resourceVersion"), Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
	}
}
