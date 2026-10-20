package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"spark-control-center/backend/internal/domain"
)

// Kubernetes has no line-offset API. Scan and discard preceding lines without
// buffering the entire log; stop as soon as this bounded window is complete.
func (c *Client) PodLogWindow(ctx context.Context, namespace, pod string, q domain.DriverLogQuery) (domain.DriverLogWindow, error) {
	out := domain.DriverLogWindow{Source: "kubernetes", Mode: q.Mode, Lines: []string{}, Limit: q.Limit}
	if err := c.ensureNamespace(namespace); err != nil {
		return out, err
	}
	if q.Limit < 1 || q.Limit > domain.MaxDriverLogLines || (q.Mode != "tail" && q.Mode != "range") || (q.Mode == "range" && (q.StartLine < 1 || q.EndLine < q.StartLine || q.EndLine-q.StartLine >= domain.MaxDriverLogLines)) {
		return out, &APIError{StatusCode: 400, Message: "invalid driver log window"}
	}
	if q.Mode == "range" {
		out.Limit = q.EndLine - q.StartLine + 1
	}
	params := url.Values{"timestamps": {"true"}, "follow": {"false"}}
	if q.Mode == "tail" {
		params.Set("tailLines", strconv.Itoa(q.Limit))
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(pod) + "/log?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return out, err
	}
	token, err := c.bearerToken()
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("query Kubernetes driver logs: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return out, &APIError{StatusCode: response.StatusCode, Message: "Driver container logs unavailable; use Loki for retained logs"}
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	line, size := 0, 0
	for scanner.Scan() {
		line++
		if q.Mode == "range" && line < q.StartLine {
			continue
		}
		if (q.Mode == "range" && line > q.EndLine) || (q.Mode == "tail" && line > q.Limit) {
			out.HasMore = true
			break
		}
		text := scanner.Text()
		if q.Mode == "tail" {
			for size+len(text)+1 > domain.MaxDriverLogBytes && len(out.Lines) > 0 {
				size -= len(out.Lines[0]) + 1
				out.Lines[0] = ""
				out.Lines = out.Lines[1:]
				out.ByteLimit = true
			}
		}
		if size+len(text)+1 > domain.MaxDriverLogBytes {
			out.ByteLimit, out.HasMore = true, true
			break
		}
		size += len(text) + 1
		out.Lines = append(out.Lines, text)
		if q.Mode == "range" {
			if out.StartLine == 0 {
				out.StartLine = line
			}
			out.EndLine = line
		}
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("read driver logs (individual lines must be under 1 MiB): %w", err)
	}
	return out, nil
}
