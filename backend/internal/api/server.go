package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/service"
)

type ApplicationService interface {
	Ready(context.Context) error
	ListApplications(context.Context) ([]domain.SparkApplication, error)
	GetApplication(context.Context, string, string) (domain.SparkApplication, error)
	Summary(context.Context, time.Time, time.Time) (domain.DashboardSummary, error)
	KillApplication(context.Context, string, string, string) (domain.OperationAudit, error)
	DeleteApplication(context.Context, string, string, string) (domain.OperationAudit, error)
	ListAudit(context.Context) ([]domain.OperationAudit, error)
}

type Server struct {
	config   config.Config
	service  ApplicationService
	logger   *slog.Logger
	requests atomic.Uint64
	errors   atomic.Uint64
}

func New(cfg config.Config, applicationService ApplicationService, logger *slog.Logger) http.Handler {
	server := &Server{config: cfg, service: applicationService, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("GET /metrics", server.metrics)
	mux.HandleFunc("GET /v1/auth/me", server.me)
	mux.HandleFunc("GET /v1/auth/login", server.login)
	mux.HandleFunc("GET /v1/auth/logout", server.logout)
	mux.HandleFunc("GET /v1/applications", server.listApplications)
	mux.HandleFunc("GET /v1/dashboard/summary", server.summary)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}", server.getApplication)
	mux.HandleFunc("POST /v1/namespaces/{namespace}/applications/{name}/kill", server.killApplication)
	mux.HandleFunc("DELETE /v1/namespaces/{namespace}/applications/{name}", server.deleteApplication)
	mux.HandleFunc("GET /v1/audit", server.listAudit)
	return server.middleware(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.service.Ready(ctx); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP spark_control_center_http_requests_total Total HTTP requests.\n# TYPE spark_control_center_http_requests_total counter\nspark_control_center_http_requests_total %d\n", s.requests.Load())
	_, _ = fmt.Fprintf(w, "# HELP spark_control_center_http_errors_total Total HTTP 5xx responses.\n# TYPE spark_control_center_http_errors_total counter\nspark_control_center_http_errors_total %d\n", s.errors.Load())
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request) {
	if !s.config.AdminBypass {
		s.writeError(w, http.StatusNotImplemented, fmt.Errorf("OIDC verification is not implemented; enable AUTH_ADMIN_BYPASS only in a trusted test environment"))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"username": "admin", "role": "admin"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.config.AdminBypass {
		s.writeError(w, http.StatusNotImplemented, fmt.Errorf("OIDC login is not implemented"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "spark-console-session", Value: "admin-bypass", Path: "/", HttpOnly: true,
		Secure: requestScheme(r) == "https", SameSite: http.SameSiteLaxMode, MaxAge: 8 * 60 * 60,
	})
	http.Redirect(w, r, safeReturnURL(r, r.URL.Query().Get("returnUrl"), "/overview"), http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "spark-console-session", Value: "", Path: "/", HttpOnly: true,
		Secure: requestScheme(r) == "https", SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.Redirect(w, r, safeReturnURL(r, r.URL.Query().Get("returnUrl"), "/login"), http.StatusFound)
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.service.ListApplications(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	filters := r.URL.Query()
	filtered := make([]domain.SparkApplication, 0, len(apps))
	for _, app := range apps {
		if matches(app, filters.Get("keyword"), filters.Get("state"), filters.Get("owner"), filters.Get("namespace")) {
			filtered = append(filtered, app)
		}
	}
	s.writeJSON(w, http.StatusOK, filtered)
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	from, to, err := s.historyRange(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	summary, err := s.service.Summary(r.Context(), from, to)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, summary)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.service.GetApplication(r.Context(), r.PathValue("namespace"), r.PathValue("name"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, app)
}

func (s *Server) killApplication(w http.ResponseWriter, r *http.Request) {
	if !s.config.AdminBypass {
		s.writeError(w, http.StatusUnauthorized, fmt.Errorf("authentication is required"))
		return
	}
	reason, err := decodeOperationReason(w, r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	audit, err := s.service.KillApplication(r.Context(), r.PathValue("namespace"), r.PathValue("name"), reason)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, audit)
}

func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	if !s.config.AdminBypass {
		s.writeError(w, http.StatusUnauthorized, fmt.Errorf("authentication is required"))
		return
	}
	reason, err := decodeOperationReason(w, r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	audit, err := s.service.DeleteApplication(r.Context(), r.PathValue("namespace"), r.PathValue("name"), reason)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, audit)
}

func decodeOperationReason(w http.ResponseWriter, r *http.Request) (string, error) {
	var body struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("invalid JSON body")
	}
	return strings.TrimSpace(body.Reason), nil
}

func (s *Server) historyRange(r *http.Request) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	days := s.config.HistoryDefaultDays
	if days <= 0 {
		days = 7
	}
	from := to.Add(-time.Duration(days) * 24 * time.Hour)
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		from, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from timestamp; expected RFC3339")
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		to, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to timestamp; expected RFC3339")
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from timestamp must be before to timestamp")
	}
	return from.UTC(), to.UTC(), nil
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	if !s.config.AdminBypass {
		s.writeError(w, http.StatusUnauthorized, fmt.Errorf("authentication is required"))
		return
	}
	audits, err := s.service.ListAudit(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, audits)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		s.requests.Add(1)
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		wrapped.Header().Set("X-Content-Type-Options", "nosniff")
		wrapped.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recovered := recover(); recovered != nil {
				s.errors.Add(1)
				s.logger.Error("panic while serving request", "panic", recovered, "stack", string(debug.Stack()))
				if !wrapped.wroteHeader {
					s.writeError(wrapped, http.StatusInternalServerError, fmt.Errorf("internal server error"))
				}
			}
			if wrapped.status >= 500 {
				s.errors.Add(1)
			}
			s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "status", wrapped.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(wrapped, r)
	})
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	status := service.HTTPStatus(err)
	s.writeError(w, status, err)
}

func (s *Server) writeError(w http.ResponseWriter, status int, err error) {
	s.writeJSON(w, status, map[string]string{"message": err.Error()})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func matches(app domain.SparkApplication, keyword, state, owner, namespace string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword != "" && !strings.Contains(strings.ToLower(app.Name+" "+app.Owner+" "+app.Team), keyword) {
		return false
	}
	return (state == "" || app.State == state) && (owner == "" || app.Owner == owner) && (namespace == "" || app.Namespace == namespace)
}

func safeReturnURL(r *http.Request, value, fallback string) string {
	if value == "" {
		return fallback
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fallback
	}
	if !parsed.IsAbs() {
		if strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//") {
			return parsed.String()
		}
		return fallback
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if parsed.Host != host || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fallback
	}
	return parsed.String()
}

func requestScheme(r *http.Request) string {
	if value := r.Header.Get("X-Forwarded-Proto"); value != "" {
		return strings.TrimSpace(strings.Split(value, ",")[0])
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func parseLimit(r *http.Request, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
