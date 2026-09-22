package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"spark-control-center/backend/internal/domain"
)

type Client struct {
	baseURL    string
	http       *http.Client
	maxEntries int
}

func New(baseURL string, timeout time.Duration, maxEntries int) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}, maxEntries: maxEntries}
}

func (c *Client) QueryLogs(ctx context.Context, namespace, pod string, from, to time.Time, direction string, limit int) ([]domain.LogEntry, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("Loki is not configured")
	}
	if direction != "forward" && direction != "backward" {
		return nil, fmt.Errorf("invalid Loki direction")
	}
	if limit <= 0 || limit > c.maxEntries {
		limit = c.maxEntries
	}
	params := url.Values{}
	params.Set("query", fmt.Sprintf(`{namespace=%q,pod=%q,spark_role="executor"}`, namespace, pod))
	params.Set("start", strconv.FormatInt(from.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(to.UnixNano(), 10))
	params.Set("direction", direction)
	params.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/loki/api/v1/query_range?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Loki query: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read Loki response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Loki query failed: %s", message)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Loki response: %w", err)
	}
	if payload.Status != "success" || payload.Data.ResultType != "streams" {
		return nil, fmt.Errorf("unexpected Loki response")
	}
	entries := make([]domain.LogEntry, 0)
	for _, stream := range payload.Data.Result {
		for _, value := range stream.Values {
			if len(value) != 2 {
				continue
			}
			nanoseconds, parseErr := strconv.ParseInt(value[0], 10, 64)
			if parseErr != nil {
				continue
			}
			labels := make(map[string]string, len(stream.Stream))
			for key, item := range stream.Stream {
				labels[key] = item
			}
			entries = append(entries, domain.LogEntry{Timestamp: time.Unix(0, nanoseconds).UTC().Format(time.RFC3339Nano), Line: value[1], Labels: labels})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if direction == "backward" {
			return entries[i].Timestamp > entries[j].Timestamp
		}
		return entries[i].Timestamp < entries[j].Timestamp
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}
