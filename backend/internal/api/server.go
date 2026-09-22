package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"spark-control-center/backend/internal/oidcauth"
	"spark-control-center/backend/internal/service"
)

type ApplicationService interface {
	Ready(context.Context) error
	ListApplications(context.Context) ([]domain.SparkApplication, error)
	GetApplication(context.Context, string, string) (domain.SparkApplication, error)
	GetApplicationMetrics(context.Context, string, string, time.Time, time.Time) ([]domain.MetricPoint, error)
	SubmitApplication(context.Context, string, string, string) (domain.SparkApplication, error)
	SparkUIProxyTarget(context.Context, string, string) (string, error)
	GetExecutorLogs(context.Context, string, string, string, time.Time, time.Time, string, int) ([]domain.LogEntry, error)
	Summary(context.Context, time.Time, time.Time) (domain.DashboardSummary, error)
	KillApplication(context.Context, string, string, string, string) (domain.OperationAudit, error)
	DeleteApplication(context.Context, string, string, string, string) (domain.OperationAudit, error)
	ListAudit(context.Context) ([]domain.OperationAudit, error)
}

type Server struct {
	config   config.Config
	service  ApplicationService
	auth     *localauth.Service
	oidc     *oidcauth.Service
	logger   *slog.Logger
	requests atomic.Uint64
	errors   atomic.Uint64
}

func New(cfg config.Config, applicationService ApplicationService, authService *localauth.Service, oidcService *oidcauth.Service, logger *slog.Logger) http.Handler {
	server := &Server{config: cfg, service: applicationService, auth: authService, oidc: oidcService, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("GET /metrics", server.metrics)
	mux.HandleFunc("GET /v1/auth/me", server.me)
	mux.HandleFunc("POST /v1/auth/login", server.login)
	mux.HandleFunc("POST /v1/auth/logout", server.logout)
	mux.HandleFunc("GET /v1/auth/oidc/login", server.oidcLogin)
	mux.HandleFunc("GET /v1/auth/oidc/callback", server.oidcCallback)
	mux.HandleFunc("GET /v1/profile", server.profile)
	mux.HandleFunc("PATCH /v1/profile", server.updateProfile)
	mux.HandleFunc("GET /v1/users", server.listUsers)
	mux.HandleFunc("POST /v1/users", server.createUser)
	mux.HandleFunc("PATCH /v1/users/{id}", server.updateUser)
	mux.HandleFunc("DELETE /v1/users/{id}", server.deleteUser)
	mux.HandleFunc("GET /v1/applications", server.listApplications)
	mux.HandleFunc("GET /v1/dashboard/summary", server.summary)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}", server.getApplication)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}/metrics", server.applicationMetrics)
	mux.HandleFunc("POST /v1/namespaces/{namespace}/applications", server.submitApplication)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}/spark-ui", server.sparkUIProxy)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}", server.sparkUIProxy)
	mux.HandleFunc("GET /v1/namespaces/{namespace}/applications/{name}/executors/{pod}/logs", server.executorLogs)
	mux.HandleFunc("POST /v1/namespaces/{namespace}/applications/{name}/kill", server.killApplication)
	mux.HandleFunc("DELETE /v1/namespaces/{namespace}/applications/{name}", server.deleteApplication)
	mux.HandleFunc("GET /v1/audit", server.listAudit)
	return server.middleware(server.sameOrigin(server.authenticate(mux)))
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

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.config.AuthMode != "local" {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("local login is not enabled"))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	user, token, expiresAt, err := s.auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	http.SetCookie(w, sessionCookie(r, token, expiresAt, int(time.Until(expiresAt).Seconds())))
	s.writeJSON(w, http.StatusOK, user)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("spark-console-session")
	if cookie != nil {
		_ = s.auth.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, sessionCookie(r, "", time.Unix(1, 0), -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("OIDC login is not enabled"))
		return
	}
	redirectURL := s.config.OIDCRedirectURL
	if redirectURL == "" {
		redirectURL = externalBaseURL(r) + strings.TrimRight(r.Header.Get("X-Forwarded-Prefix"), "/") + "/v1/auth/oidc/callback"
	}
	returnURL := safeReturnURL(r, r.URL.Query().Get("returnUrl"), "/overview")
	authorizationURL, err := s.oidc.Begin(r.Context(), redirectURL, returnURL)
	if err != nil {
		s.logger.Error("begin OIDC login", "error", err)
		s.redirectOIDCError(w, r, "login_start_failed")
		return
	}
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("OIDC login is not enabled"))
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		s.logger.Warn("OIDC provider rejected login", "error", providerError)
		s.redirectOIDCError(w, r, "provider_rejected")
		return
	}
	_, token, expiresAt, returnURL, err := s.oidc.Callback(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		s.logger.Warn("complete OIDC login", "error", err)
		s.redirectOIDCError(w, r, "login_failed")
		return
	}
	http.SetCookie(w, sessionCookie(r, token, expiresAt, int(time.Until(expiresAt).Seconds())))
	http.Redirect(w, r, safeReturnURL(r, returnURL, "/overview"), http.StatusFound)
}

func (s *Server) redirectOIDCError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?oidcError="+url.QueryEscape(code), http.StatusFound)
}

func externalBaseURL(r *http.Request) string {
	host := r.Host
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	return requestScheme(r) + "://" + host
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

func (s *Server) applicationMetrics(w http.ResponseWriter, r *http.Request) {
	from, to, err := s.metricRange(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	points, err := s.service.GetApplicationMetrics(r.Context(), r.PathValue("namespace"), r.PathValue("name"), from, to)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, points)
}

func (s *Server) submitApplication(w http.ResponseWriter, r *http.Request) {
	principal := currentUser(r)
	if !s.requireAdmin(w, principal) {
		return
	}
	var body struct {
		YAML string `json:"yaml"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	if strings.TrimSpace(body.YAML) == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("yaml is required"))
		return
	}
	app, err := s.service.SubmitApplication(r.Context(), r.PathValue("namespace"), body.YAML, principal.Username)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, app)
}

func (s *Server) sparkUIProxy(w http.ResponseWriter, r *http.Request) {
	targetValue, err := s.service.SparkUIProxyTarget(r.Context(), r.PathValue("namespace"), r.PathValue("name"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	target, err := url.Parse(targetValue)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Errorf("invalid Spark UI target"))
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/"+r.PathValue("path"))
	if r.PathValue("path") == "" {
		basePath = strings.TrimSuffix(r.URL.Path, "/")
	}
	externalPrefix := strings.TrimRight(r.Header.Get("X-Forwarded-Prefix"), "/") + basePath
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.URL.Path = "/" + r.PathValue("path")
		request.URL.RawPath = ""
		request.Host = target.Host
		request.Header.Del("Accept-Encoding")
		request.Header.Set("X-Forwarded-Prefix", externalPrefix)
		request.Header.Set("X-Forwarded-Context", externalPrefix)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if location := response.Header.Get("Location"); location != "" {
			response.Header.Set("Location", rewriteSparkUILocation(location, target, externalPrefix))
		}
		if !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		if readErr != nil {
			return readErr
		}
		_ = response.Body.Close()
		data = rewriteSparkUIHTML(data, target, externalPrefix)
		response.Body = io.NopCloser(bytes.NewReader(data))
		response.ContentLength = int64(len(data))
		response.Header.Set("Content-Length", strconv.Itoa(len(data)))
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		s.logger.Warn("Spark UI proxy failed", "namespace", r.PathValue("namespace"), "application", r.PathValue("name"), "error", proxyErr)
		s.writeError(writer, http.StatusBadGateway, fmt.Errorf("Spark driver UI is unavailable"))
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) executorLogs(w http.ResponseWriter, r *http.Request) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		from, err = time.Parse(time.RFC3339, value)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid from timestamp; expected RFC3339"))
			return
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		to, err = time.Parse(time.RFC3339, value)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid to timestamp; expected RFC3339"))
			return
		}
	}
	if !from.Before(to) {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("from timestamp must be before to timestamp"))
		return
	}
	direction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if direction == "" {
		direction = "backward"
	}
	if direction != "forward" && direction != "backward" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("direction must be forward or backward"))
		return
	}
	limit := parseLimit(r, 2000)
	if limit > 5000 {
		limit = 5000
	}
	entries, err := s.service.GetExecutorLogs(r.Context(), r.PathValue("namespace"), r.PathValue("name"), r.PathValue("pod"), from.UTC(), to.UTC(), direction, limit)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, entries)
}

func rewriteSparkUILocation(location string, target *url.URL, externalPrefix string) string {
	reference, err := url.Parse(location)
	if err != nil {
		return location
	}
	if reference.Host != "" {
		if !strings.EqualFold(reference.Host, target.Host) {
			return location
		}
		path := reference.EscapedPath()
		if path == "" {
			path = "/"
		}
		rewritten := externalPrefix + path
		if reference.RawQuery != "" {
			rewritten += "?" + reference.RawQuery
		}
		if reference.Fragment != "" {
			rewritten += "#" + reference.Fragment
		}
		return rewritten
	}
	if strings.HasPrefix(location, "/") && location != externalPrefix && !strings.HasPrefix(location, externalPrefix+"/") {
		return externalPrefix + location
	}
	return location
}

func rewriteSparkUIHTML(data []byte, target *url.URL, externalPrefix string) []byte {
	const protectedDouble = "__SPARK_UI_PROXY_DOUBLE__"
	const protectedSingle = "__SPARK_UI_PROXY_SINGLE__"
	for _, attribute := range []string{"href", "src", "action"} {
		data = bytes.ReplaceAll(data, []byte(attribute+"=\""+externalPrefix+"/"), []byte(attribute+"=\""+protectedDouble))
		data = bytes.ReplaceAll(data, []byte(attribute+"='"+externalPrefix+"/"), []byte(attribute+"='"+protectedSingle))
		data = bytes.ReplaceAll(data, []byte(attribute+"=\"/"), []byte(attribute+"=\""+externalPrefix+"/"))
		data = bytes.ReplaceAll(data, []byte(attribute+"='/"), []byte(attribute+"='"+externalPrefix+"/"))
		data = bytes.ReplaceAll(data, []byte(attribute+"=\""+protectedDouble), []byte(attribute+"=\""+externalPrefix+"/"))
		data = bytes.ReplaceAll(data, []byte(attribute+"='"+protectedSingle), []byte(attribute+"='"+externalPrefix+"/"))
	}
	for _, internalBase := range []string{"http://" + target.Host, "https://" + target.Host, "//" + target.Host} {
		data = bytes.ReplaceAll(data, []byte(internalBase+"/"), []byte(externalPrefix+"/"))
		data = bytes.ReplaceAll(data, []byte(internalBase+"\""), []byte(externalPrefix+"\""))
		data = bytes.ReplaceAll(data, []byte(internalBase+"'"), []byte(externalPrefix+"'"))
	}
	return data
}

func (s *Server) killApplication(w http.ResponseWriter, r *http.Request) {
	principal := currentUser(r)
	if !s.requireAdmin(w, principal) {
		return
	}
	reason, err := decodeOperationReason(w, r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	audit, err := s.service.KillApplication(r.Context(), r.PathValue("namespace"), r.PathValue("name"), principal.Username, reason)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, audit)
}

func (s *Server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	principal := currentUser(r)
	if !s.requireAdmin(w, principal) {
		return
	}
	reason, err := decodeOperationReason(w, r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	audit, err := s.service.DeleteApplication(r.Context(), r.PathValue("namespace"), r.PathValue("name"), principal.Username, reason)
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

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName     *string `json:"displayName"`
		Email           *string `json:"email"`
		CurrentPassword string  `json:"currentPassword"`
		NewPassword     string  `json:"newPassword"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	user, passwordChanged, err := s.auth.UpdateProfile(r.Context(), currentUser(r), localauth.UpdateProfileInput{
		DisplayName: body.DisplayName, Email: body.Email, CurrentPassword: body.CurrentPassword, NewPassword: body.NewPassword,
	})
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	if passwordChanged {
		http.SetCookie(w, sessionCookie(r, "", time.Unix(1, 0), -1))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user, "passwordChanged": passwordChanged})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers(r.Context(), currentUser(r))
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string          `json:"username"`
		DisplayName string          `json:"displayName"`
		Email       string          `json:"email"`
		Role        domain.UserRole `json:"role"`
		Password    string          `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	user, err := s.auth.CreateUser(r.Context(), currentUser(r), localauth.CreateUserInput{
		Username: body.Username, DisplayName: body.DisplayName, Email: body.Email, Role: body.Role, Password: body.Password,
	})
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName *string          `json:"displayName"`
		Email       *string          `json:"email"`
		Role        *domain.UserRole `json:"role"`
		Disabled    *bool            `json:"disabled"`
		Password    *string          `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	user, err := s.auth.UpdateUser(r.Context(), currentUser(r), r.PathValue("id"), localauth.UpdateUserInput{
		DisplayName: body.DisplayName, Email: body.Email, Role: body.Role, Disabled: body.Disabled, Password: body.Password,
	})
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, user)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteUser(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) metricRange(r *http.Request) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	lookback := s.config.MetricsLookback
	if lookback <= 0 {
		lookback = time.Hour
	}
	from := to.Add(-lookback)
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
	if !s.requireAdmin(w, currentUser(r)) {
		return
	}
	audits, err := s.service.ListAudit(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, audits)
}

type principalContextKey struct{}

func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				parsed, err := url.Parse(origin)
				host := r.Host
				if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
					host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
				}
				if err != nil || !strings.EqualFold(parsed.Host, host) || !strings.EqualFold(parsed.Scheme, requestScheme(r)) {
					s.writeError(w, http.StatusForbidden, fmt.Errorf("cross-origin state-changing request rejected"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1/auth/login" || r.URL.Path == "/v1/auth/logout" || r.URL.Path == "/v1/auth/oidc/login" || r.URL.Path == "/v1/auth/oidc/callback" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("spark-console-session")
		if err != nil {
			s.writeAuthError(w, localauth.ErrUnauthorized)
			return
		}
		user, _, err := s.auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			http.SetCookie(w, sessionCookie(r, "", time.Unix(1, 0), -1))
			s.writeAuthError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, user)))
	})
}

func currentUser(r *http.Request) domain.User {
	user, _ := r.Context().Value(principalContextKey{}).(domain.User)
	return user
}

func (s *Server) requireAdmin(w http.ResponseWriter, user domain.User) bool {
	if user.Role != domain.RoleAdmin {
		s.writeAuthError(w, localauth.ErrForbidden)
		return false
	}
	return true
}

func sessionCookie(r *http.Request, value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: "spark-console-session", Value: value, Path: "/", HttpOnly: true,
		Secure: requestScheme(r) == "https", SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge,
	}
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

func (s *Server) writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, localauth.ErrInvalidCredentials), errors.Is(err, localauth.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, localauth.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, localauth.ErrUserNotFound):
		status = http.StatusNotFound
	case errors.Is(err, localauth.ErrUsernameExists), errors.Is(err, localauth.ErrLastAdmin), errors.Is(err, localauth.ErrSelfDelete):
		status = http.StatusConflict
	case errors.Is(err, localauth.ErrInvalidInput):
		status = http.StatusBadRequest
	}
	if status == http.StatusInternalServerError {
		s.logger.Error("local authentication request failed", "error", err)
		s.writeError(w, status, fmt.Errorf("internal authentication error"))
		return
	}
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
	if keyword != "" && !strings.Contains(strings.ToLower(app.Name+" "+app.Owner), keyword) {
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
