package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"spark-control-center/backend/internal/domain"
)

type PodUsage struct {
	Current domain.ResourceAmount
	Peak    domain.ResourceAmount
}

type Client struct {
	baseURL  string
	http     *http.Client
	lookback time.Duration
}

type apiResponse struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func New(baseURL string, timeout, lookback time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}, lookback: lookback}
}

func (c *Client) Usage(ctx context.Context, namespace string, podNames []string) (map[string]PodUsage, []domain.MetricPoint, error) {
	usage := make(map[string]PodUsage, len(podNames))
	if len(podNames) == 0 {
		return usage, []domain.MetricPoint{}, nil
	}
	podRegex := make([]string, 0, len(podNames))
	for _, podName := range podNames {
		podRegex = append(podRegex, regexp.QuoteMeta(podName))
	}
	selector := fmt.Sprintf(`namespace=%q,pod=~%q,container!="",container!="POD"`, namespace, strings.Join(podRegex, "|"))
	queries := []string{
		fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{%s}[5m]))`, selector),
		fmt.Sprintf(`sum by (pod) (container_memory_working_set_bytes{%s}) / 1073741824`, selector),
	}
	type result struct {
		index int
		data  apiResponse
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for index, query := range queries {
		wg.Add(1)
		go func(index int, query string) {
			defer wg.Done()
			response, err := c.queryRange(ctx, query, time.Now().Add(-c.lookback), time.Now())
			results <- result{index: index, data: response, err: err}
		}(index, query)
	}
	wg.Wait()
	close(results)
	series := make([]apiResponse, 2)
	for result := range results {
		if result.err != nil {
			return nil, nil, result.err
		}
		series[result.index] = result.data
	}

	aggregate := map[int64]*domain.MetricPoint{}
	for index, response := range series {
		for _, item := range response.Data.Result {
			podName := item.Metric["pod"]
			podUsage := usage[podName]
			for _, sample := range item.Values {
				timestamp, value, ok := parseSample(sample)
				if !ok {
					continue
				}
				if index == 0 {
					podUsage.Current.CPU = value
					if value > podUsage.Peak.CPU {
						podUsage.Peak.CPU = value
					}
				} else {
					podUsage.Current.MemoryGiB = value
					if value > podUsage.Peak.MemoryGiB {
						podUsage.Peak.MemoryGiB = value
					}
				}
				bucket := timestamp.Unix()
				point := aggregate[bucket]
				if point == nil {
					point = &domain.MetricPoint{Time: timestamp.UTC().Format(time.RFC3339)}
					aggregate[bucket] = point
				}
				if index == 0 {
					point.CPU += value
				} else {
					point.MemoryGiB += value
				}
			}
			usage[podName] = podUsage
		}
	}
	timestamps := make([]int64, 0, len(aggregate))
	for timestamp := range aggregate {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	metrics := make([]domain.MetricPoint, 0, len(timestamps))
	for _, timestamp := range timestamps {
		point := *aggregate[timestamp]
		point.CPU = round(point.CPU, 3)
		point.MemoryGiB = round(point.MemoryGiB, 3)
		metrics = append(metrics, point)
	}
	return usage, metrics, nil
}

func (c *Client) Capacity(ctx context.Context) (domain.ResourceAmount, error) {
	queries := []string{
		`sum(kube_node_status_allocatable{resource="cpu",unit="core"})`,
		`sum(kube_node_status_allocatable{resource="memory",unit="byte"}) / 1073741824`,
	}
	var capacity domain.ResourceAmount
	for index, query := range queries {
		response, err := c.query(ctx, query)
		if err != nil {
			return domain.ResourceAmount{}, err
		}
		if len(response.Data.Result) == 0 {
			continue
		}
		_, value, ok := parseSample(response.Data.Result[0].Value)
		if !ok {
			continue
		}
		if index == 0 {
			capacity.CPU = value
		} else {
			capacity.MemoryGiB = value
		}
	}
	return capacity, nil
}

func (c *Client) query(ctx context.Context, query string) (apiResponse, error) {
	params := url.Values{"query": {query}}
	return c.get(ctx, "/api/v1/query?"+params.Encode())
}

func (c *Client) queryRange(ctx context.Context, query string, start, end time.Time) (apiResponse, error) {
	step := c.lookback / 60
	if step < 15*time.Second {
		step = 15 * time.Second
	}
	params := url.Values{
		"query": {query}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)},
		"step": {strconv.FormatInt(int64(step.Seconds()), 10)},
	}
	return c.get(ctx, "/api/v1/query_range?"+params.Encode())
}

func (c *Client) get(ctx context.Context, path string) (apiResponse, error) {
	var result apiResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return result, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return result, fmt.Errorf("Prometheus request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return result, fmt.Errorf("read Prometheus response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("Prometheus returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode Prometheus response: %w", err)
	}
	if result.Status != "success" {
		return result, fmt.Errorf("Prometheus %s: %s", result.ErrorType, result.Error)
	}
	return result, nil
}

func parseSample(sample []any) (time.Time, float64, bool) {
	if len(sample) != 2 {
		return time.Time{}, 0, false
	}
	seconds, ok := sample[0].(float64)
	if !ok {
		return time.Time{}, 0, false
	}
	valueText, ok := sample[1].(string)
	if !ok {
		return time.Time{}, 0, false
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return time.Time{}, 0, false
	}
	return time.Unix(int64(seconds), 0), value, true
}

func round(value float64, places int) float64 {
	text := strconv.FormatFloat(value, 'f', places, 64)
	result, _ := strconv.ParseFloat(text, 64)
	return result
}
