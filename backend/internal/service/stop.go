package service

import (
	"context"
	"spark-control-center/backend/internal/kube"
	"time"
)

// Stop intents live on the CR, so any backend replica can recover them. Each
// mutation uses a fresh object and UID checks. No database lock spans Kubernetes.
func (s *Service) reconcileStops(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		for _, ns := range s.config.Namespaces {
			work, cancel := context.WithTimeout(ctx, 15*time.Second)
			objects, err := s.kubernetes.ListSparkApplications(work, ns)
			if err == nil {
				for _, obj := range objects {
					if kube.StringMap(obj, "metadata", "annotations")[kube.StopAnnotation] == "" {
						continue
					}
					if err = s.enforceStop(work, ns, kube.String(obj, "metadata", "name"), kube.String(obj, "metadata", "uid")); err != nil && ctx.Err() == nil {
						s.logger.Warn("stop reconciliation pending", "namespace", ns, "application", kube.String(obj, "metadata", "name"), "error", err)
					}
				}
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) enforceStop(ctx context.Context, ns, name, uid string) error {
	obj, err := s.kubernetes.GetSparkApplication(ctx, ns, name)
	if err != nil {
		if HTTPStatus(err) == 404 {
			return nil
		}
		return err
	}
	if kube.String(obj, "metadata", "uid") != uid || kube.StringMap(obj, "metadata", "annotations")[kube.StopAnnotation] == "" {
		return nil
	}
	if terminalState(normalizedState(kube.String(obj, "status", "applicationState", "state"))) && kube.String(obj, "spec", "restartPolicy", "type") == "Never" {
		return nil
	}
	if kube.String(obj, "spec", "restartPolicy", "type") != "Never" {
		if writer, ok := s.kubernetes.(interface {
			RequestApplicationStop(context.Context, string, kube.Object) error
		}); ok {
			if err = writer.RequestApplicationStop(ctx, ns, obj); err != nil {
				return err
			}
		}
	}
	pods, err := s.kubernetes.ListPods(ctx, ns)
	if err != nil {
		return err
	}
	for _, pod := range relatedPods(obj, pods) {
		if pod.Phase == "SUCCEEDED" || pod.Phase == "FAILED" {
			continue
		}
		if pod.Role == "driver" || pod.Role == "executor" {
			err = s.stopPod(ctx, ns, pod)
		}
		if err != nil && HTTPStatus(err) != 404 {
			return err
		}
	}
	return nil
}

func (s *Service) stopPod(ctx context.Context, ns string, pod kube.Pod) error {
	if writer, ok := s.kubernetes.(interface {
		StopPodInstance(context.Context, string, kube.Pod) error
	}); ok {
		return writer.StopPodInstance(ctx, ns, pod)
	}
	if pod.Role == "driver" {
		return s.kubernetes.TerminatePodByDeadline(ctx, ns, pod.Name)
	}
	return s.kubernetes.ForceDeletePod(ctx, ns, pod.Name)
}
