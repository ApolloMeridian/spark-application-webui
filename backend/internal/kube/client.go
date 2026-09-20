package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
)

type Object map[string]any

type Pod struct {
	Name      string
	Namespace string
	Labels    map[string]string
	Node      string
	NodePool  string
	Role      string
	Phase     string
	StartedAt string
	Restarts  int64
	Request   domain.ResourceAmount
}

type Client struct {
	baseURL       string
	token         string
	tokenFile     string
	http          *http.Client
	apiGroup      string
	apiVersion    string
	resourcePath  string
	allowedLookup map[string]bool
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

func New(cfg config.Config) (*Client, error) {
	token := cfg.KubernetesToken
	tokenFile := ""
	if token == "" {
		tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		value, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes service account token: %w", err)
		}
		if strings.TrimSpace(string(value)) == "" {
			return nil, errors.New("Kubernetes service account token is empty")
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.HasPrefix(cfg.KubernetesAPIURL, "https://") {
		caData, err := os.ReadFile(cfg.KubernetesCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes CA file: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caData) {
			return nil, errors.New("Kubernetes CA file contains no valid certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	parts := strings.SplitN(cfg.SparkAPIVersion, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid SPARKAPPLICATION_API_VERSION %q", cfg.SparkAPIVersion)
	}
	allowed := make(map[string]bool, len(cfg.Namespaces))
	for _, namespace := range cfg.Namespaces {
		allowed[namespace] = true
	}
	return &Client{
		baseURL:   cfg.KubernetesAPIURL,
		token:     token,
		tokenFile: tokenFile,
		http:      &http.Client{Transport: transport, Timeout: cfg.KubernetesTimeout},
		apiGroup:  parts[0], apiVersion: parts[1], resourcePath: "sparkapplications",
		allowedLookup: allowed,
	}, nil
}

func (c *Client) Ready(ctx context.Context, namespace string) error {
	var response struct {
		Items []Object `json:"items"`
	}
	path := c.sparkCollectionPath(namespace) + "?limit=1"
	return c.doJSON(ctx, http.MethodGet, path, nil, &response)
}

func (c *Client) ListSparkApplications(ctx context.Context, namespace string) ([]Object, error) {
	if err := c.ensureNamespace(namespace); err != nil {
		return nil, err
	}
	var response struct {
		Items []Object `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.sparkCollectionPath(namespace), nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) GetSparkApplication(ctx context.Context, namespace, name string) (Object, error) {
	if err := c.ensureNamespace(namespace); err != nil {
		return nil, err
	}
	var response Object
	if err := c.doJSON(ctx, http.MethodGet, c.sparkObjectPath(namespace, name), nil, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) DeleteSparkApplication(ctx context.Context, namespace, name string) error {
	if err := c.ensureNamespace(namespace); err != nil {
		return err
	}
	body := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground"}
	var response Object
	return c.doJSON(ctx, http.MethodDelete, c.sparkObjectPath(namespace, name), body, &response)
}

func (c *Client) ListPods(ctx context.Context, namespace string) ([]Pod, error) {
	if err := c.ensureNamespace(namespace); err != nil {
		return nil, err
	}
	var response struct {
		Items []Object `json:"items"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	pods := make([]Pod, 0, len(response.Items))
	for _, item := range response.Items {
		pods = append(pods, ParsePod(item))
	}
	return pods, nil
}

func (c *Client) PodLog(ctx context.Context, namespace, podName string, tailLines int) ([]string, error) {
	if err := c.ensureNamespace(namespace); err != nil {
		return nil, err
	}
	if podName == "" {
		return []string{}, nil
	}
	if tailLines <= 0 {
		tailLines = 5000
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(podName) +
		"/log?timestamps=true&tailLines=" + strconv.Itoa(tailLines)
	data, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return []string{}, nil
		}
		return nil, err
	}
	trimmed := strings.TrimRight(string(data), "\r\n")
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n"), nil
}

func (c *Client) ListEvents(ctx context.Context, namespace string) ([]domain.KubernetesEvent, error) {
	if err := c.ensureNamespace(namespace); err != nil {
		return nil, err
	}
	var response struct {
		Items []Object `json:"items"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/events"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	result := make([]domain.KubernetesEvent, 0, len(response.Items))
	for _, item := range response.Items {
		event := domain.KubernetesEvent{
			ID: String(item, "metadata", "uid"), Type: String(item, "type"), Reason: String(item, "reason"),
			Message: String(item, "message"), Source: firstNonEmpty(String(item, "reportingComponent"), String(item, "source", "component")), Count: Int64(item, "count"),
			Timestamp:      firstNonEmpty(String(item, "eventTime"), String(item, "lastTimestamp"), String(item, "firstTimestamp"), String(item, "metadata", "creationTimestamp")),
			InvolvedObject: String(item, "involvedObject", "name"),
		}
		if event.ID == "" {
			event.ID = event.Reason + "-" + event.Timestamp
		}
		result = append(result, event)
	}
	return result, nil
}

func (c *Client) sparkCollectionPath(namespace string) string {
	return "/apis/" + url.PathEscape(c.apiGroup) + "/" + url.PathEscape(c.apiVersion) +
		"/namespaces/" + url.PathEscape(namespace) + "/" + c.resourcePath
}

func (c *Client) sparkObjectPath(namespace, name string) string {
	return c.sparkCollectionPath(namespace) + "/" + url.PathEscape(name)
}

func (c *Client) ensureNamespace(namespace string) error {
	if !c.allowedLookup[namespace] {
		return &APIError{StatusCode: http.StatusForbidden, Message: "namespace is not configured for this service"}
	}
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, target any) error {
	data, err := c.do(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode Kubernetes response: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, accept string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	token, err := c.bearerToken()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Kubernetes API request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		var status struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &status) == nil && status.Message != "" {
			message = status.Message
		}
		if message == "" {
			message = resp.Status
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: message}
	}
	return data, nil
}

func (c *Client) bearerToken() (string, error) {
	if c.tokenFile == "" {
		return c.token, nil
	}
	value, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("refresh Kubernetes service account token: %w", err)
	}
	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", errors.New("Kubernetes service account token is empty")
	}
	return token, nil
}

func ParsePod(item Object) Pod {
	pod := Pod{
		Name: String(item, "metadata", "name"), Namespace: String(item, "metadata", "namespace"),
		Labels: StringMap(item, "metadata", "labels"), Node: String(item, "spec", "nodeName"),
		Phase: strings.ToUpper(String(item, "status", "phase")), StartedAt: String(item, "status", "startTime"),
	}
	pod.Role = firstNonEmpty(pod.Labels["spark-role"], pod.Labels["sparkoperator.k8s.io/role"])
	pod.NodePool = detectNodePool(item, pod)
	containers, _ := Slice(item, "spec", "containers")
	for _, raw := range containers {
		container, _ := raw.(map[string]any)
		pod.Request.CPU += ParseCPU(String(Object(container), "resources", "requests", "cpu"))
		pod.Request.MemoryGiB += ParseMemoryGiB(String(Object(container), "resources", "requests", "memory"))
	}
	statuses, _ := Slice(item, "status", "containerStatuses")
	for _, raw := range statuses {
		status, _ := raw.(map[string]any)
		pod.Restarts += Int64(Object(status), "restartCount")
	}
	return pod
}

func ParseCPU(quantity string) float64 {
	quantity = strings.TrimSpace(quantity)
	units := []struct {
		suffix string
		factor float64
	}{{"n", 1e-9}, {"u", 1e-6}, {"m", 1e-3}}
	for _, unit := range units {
		if strings.HasSuffix(quantity, unit.suffix) {
			value, _ := strconv.ParseFloat(strings.TrimSuffix(quantity, unit.suffix), 64)
			return value * unit.factor
		}
	}
	value, _ := strconv.ParseFloat(quantity, 64)
	return value
}

func ParseMemoryGiB(quantity string) float64 {
	quantity = strings.TrimSpace(quantity)
	units := []struct {
		suffix string
		bytes  float64
	}{
		{"Ei", 1 << 60}, {"Pi", 1 << 50}, {"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"E", 1e18}, {"P", 1e15}, {"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, unit := range units {
		if strings.HasSuffix(quantity, unit.suffix) {
			value, _ := strconv.ParseFloat(strings.TrimSuffix(quantity, unit.suffix), 64)
			return value * unit.bytes / float64(uint64(1)<<30)
		}
	}
	value, _ := strconv.ParseFloat(quantity, 64)
	return value / float64(uint64(1)<<30)
}

func String(object Object, path ...string) string {
	value, ok := nested(object, path...)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func Int64(object Object, path ...string) int64 {
	value, ok := nested(object, path...)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		result, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return result
	}
}

func StringMap(object Object, path ...string) map[string]string {
	value, ok := nested(object, path...)
	result := map[string]string{}
	if !ok {
		return result
	}
	if source, ok := value.(map[string]any); ok {
		for key, item := range source {
			result[key] = fmt.Sprint(item)
		}
	}
	return result
}

func Slice(object Object, path ...string) ([]any, bool) {
	value, ok := nested(object, path...)
	if !ok {
		return nil, false
	}
	result, ok := value.([]any)
	return result, ok
}

func Map(object Object, path ...string) (map[string]any, bool) {
	value, ok := nested(object, path...)
	if !ok {
		return nil, false
	}
	result, ok := value.(map[string]any)
	return result, ok
}

func nested(object Object, path ...string) (any, bool) {
	var current any = map[string]any(object)
	for _, part := range path {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapping[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func detectNodePool(item Object, pod Pod) string {
	labels := pod.Labels
	nodeSelector := StringMap(item, "spec", "nodeSelector")
	keys := []string{"cloud.google.com/gke-nodepool", "karpenter.sh/nodepool", "eks.amazonaws.com/nodegroup", "nodepool", "node-pool", "agentpool"}
	for _, key := range keys {
		value := firstNonEmpty(labels[key], nodeSelector[key])
		if value != "" {
			lower := strings.ToLower(value)
			if strings.Contains(lower, "auto") || strings.Contains(lower, "spot") || strings.Contains(lower, "karpenter") {
				return "autoscale"
			}
			return "baseline"
		}
	}
	if pod.Node != "" {
		return "baseline"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
