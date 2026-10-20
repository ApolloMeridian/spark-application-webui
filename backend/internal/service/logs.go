package service

import (
	"context"
	"errors"
	"time"

	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/kube"
)

func (s *Service) GetDriverLogs(ctx context.Context, namespace, name string, q domain.DriverLogQuery) (domain.DriverLogWindow, error) {
	out := domain.DriverLogWindow{Source: q.Source, Mode: q.Mode, Lines: []string{}, Limit: q.Limit}
	if !s.config.NamespaceAllowed(namespace) {
		return out, &kube.APIError{StatusCode: 403, Message: "namespace is not configured"}
	}
	if (q.Source != "kubernetes" && q.Source != "loki") || q.Limit < 1 || q.Limit > domain.MaxDriverLogLines {
		return out, &kube.APIError{StatusCode: 400, Message: "invalid driver log source or limit"}
	}
	from, fromErr := time.Parse(time.RFC3339Nano, q.From)
	to, toErr := time.Parse(time.RFC3339Nano, q.To)
	if q.Source == "loki" && (fromErr != nil || toErr != nil || !from.Before(to) || (q.Direction != "forward" && q.Direction != "backward")) {
		return out, &kube.APIError{StatusCode: 400, Message: "invalid Loki time range or direction"}
	}
	object, err := s.kubernetes.GetSparkApplication(ctx, namespace, name)
	pod := kube.String(object, "status", "driverInfo", "podName")
	if err != nil {
		var apiErr *kube.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
			return out, err
		}
		app, snapshotErr := s.audits.GetApplicationSnapshot(ctx, namespace, name)
		if snapshotErr != nil {
			return out, err
		}
		pod = app.DriverPod
	} else if pod == "" {
		pods, listErr := s.kubernetes.ListPods(ctx, namespace)
		if listErr != nil {
			return out, listErr
		}
		for _, p := range relatedPods(object, pods) {
			if p.Role == "driver" {
				pod = p.Name
				break
			}
		}
	}
	if pod == "" {
		return out, &kube.APIError{StatusCode: 404, Message: "Driver pod is not available yet"}
	}
	if q.Source == "kubernetes" {
		reader, ok := s.kubernetes.(interface {
			PodLogWindow(context.Context, string, string, domain.DriverLogQuery) (domain.DriverLogWindow, error)
		})
		if !ok {
			return out, &kube.APIError{StatusCode: 503, Message: "Driver log window reader unavailable"}
		}
		return reader.PodLogWindow(ctx, namespace, pod, q)
	}
	reader, ok := s.logs.(interface {
		QueryRoleLogs(context.Context, string, string, string, time.Time, time.Time, string, int) ([]domain.LogEntry, error)
	})
	if !ok {
		return out, &kube.APIError{StatusCode: 503, Message: "Loki is not configured"}
	}
	if s.config.LokiMaxEntries > 0 && out.Limit > s.config.LokiMaxEntries {
		out.Limit = s.config.LokiMaxEntries
	}
	entries, err := reader.QueryRoleLogs(ctx, namespace, pod, "driver", from, to, q.Direction, out.Limit)
	if err != nil {
		return out, err
	}
	out.HasMore = len(entries) >= out.Limit
	size := 0
	for _, entry := range entries {
		line := entry.Timestamp + " " + entry.Line
		if size+len(line)+1 > domain.MaxDriverLogBytes {
			out.ByteLimit, out.HasMore = true, true
			break
		}
		out.Lines = append(out.Lines, line)
		size += len(line) + 1
	}
	return out, nil
}
