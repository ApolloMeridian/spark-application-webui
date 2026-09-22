package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr             string
	ClusterName          string
	Namespaces           []string
	SparkAPIVersion      string
	KubernetesAPIURL     string
	KubernetesToken      string
	KubernetesCAFile     string
	KubernetesTimeout    time.Duration
	PrometheusURL        string
	PrometheusTimeout    time.Duration
	MetricsLookback      time.Duration
	MetricsQueryStep     time.Duration
	MetricsCPURateWindow time.Duration
	HistoryDefaultDays   int
	LokiURL              string
	LokiTimeout          time.Duration
	LokiMaxEntries       int
	DatabaseURL          string
	DatabaseHost         string
	DatabasePort         int
	DatabaseName         string
	DatabaseSSLMode      string
	DatabaseUsername     string
	DatabasePassword     string
	DatabaseMaxOpenConns int32
	AuthMode             string
	AuthSessionTTL       time.Duration
	AuthBCryptCost       int
	InitialAdminUsername string
	InitialAdminPassword string
	InitialAdminName     string
	OIDCEnabled          bool
	OIDCIssuerURL        string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCScopes           []string
	OIDCGroupsClaim      string
	OIDCAdminGroups      []string
	OIDCUsernameClaim    string
	OIDCAutoCreate       bool
	OIDCRedirectURL      string
	OIDCStateTTL         time.Duration
	OIDCCAFile           string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:             env("HTTP_ADDR", ":8081"),
		ClusterName:          env("KUBERNETES_CLUSTER_NAME", "kubernetes"),
		Namespaces:           splitCSV(env("WATCH_NAMESPACES", "spark")),
		SparkAPIVersion:      env("SPARKAPPLICATION_API_VERSION", "sparkoperator.k8s.io/v1beta2"),
		KubernetesAPIURL:     strings.TrimRight(os.Getenv("KUBERNETES_API_URL"), "/"),
		KubernetesToken:      os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesCAFile:     env("KUBERNETES_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		KubernetesTimeout:    durationEnv("KUBERNETES_REQUEST_TIMEOUT", 15*time.Second),
		PrometheusURL:        strings.TrimRight(env("PROMETHEUS_URL", "http://prometheus-server.prometheus.svc:80"), "/"),
		PrometheusTimeout:    durationEnv("PROMETHEUS_QUERY_TIMEOUT", 15*time.Second),
		MetricsLookback:      durationEnv("PROMETHEUS_LOOKBACK", time.Hour),
		MetricsQueryStep:     durationEnv("PROMETHEUS_QUERY_STEP", 15*time.Second),
		MetricsCPURateWindow: durationEnv("PROMETHEUS_CPU_RATE_WINDOW", time.Minute),
		HistoryDefaultDays:   intEnv("HISTORY_DEFAULT_DAYS", 7),
		LokiURL:              strings.TrimRight(os.Getenv("LOKI_URL"), "/"),
		LokiTimeout:          durationEnv("LOKI_QUERY_TIMEOUT", 15*time.Second),
		LokiMaxEntries:       intEnv("LOKI_MAX_ENTRIES", 5000),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		DatabaseHost:         env("DATABASE_HOST", "spark-postgres.spark-console.svc"),
		DatabasePort:         intEnv("DATABASE_PORT", 5432),
		DatabaseName:         env("DATABASE_NAME", "spark_console_db"),
		DatabaseSSLMode:      env("DATABASE_SSLMODE", "disable"),
		DatabaseUsername:     os.Getenv("DATABASE_USERNAME"),
		DatabasePassword:     os.Getenv("DATABASE_PASSWORD"),
		DatabaseMaxOpenConns: int32(intEnv("DATABASE_MAX_OPEN_CONNECTIONS", 20)),
		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthSessionTTL:       durationEnv("AUTH_SESSION_TTL", 8*time.Hour),
		AuthBCryptCost:       intEnv("AUTH_BCRYPT_COST", 12),
		InitialAdminUsername: strings.TrimSpace(env("AUTH_INITIAL_ADMIN_USERNAME", "admin")),
		InitialAdminPassword: os.Getenv("AUTH_INITIAL_ADMIN_PASSWORD"),
		InitialAdminName:     strings.TrimSpace(env("AUTH_INITIAL_ADMIN_DISPLAY_NAME", "Initial Administrator")),
		OIDCEnabled:          boolEnv("OIDC_ENABLED", false),
		OIDCIssuerURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("OIDC_ISSUER_URL")), "/"),
		OIDCClientID:         strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID")),
		OIDCClientSecret:     os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCScopes:           splitCSV(env("OIDC_SCOPES", "openid,profile,email,groups")),
		OIDCGroupsClaim:      env("OIDC_GROUPS_CLAIM", "groups"),
		OIDCAdminGroups:      splitCSV(os.Getenv("OIDC_ADMIN_GROUPS")),
		OIDCUsernameClaim:    env("OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCAutoCreate:       boolEnv("OIDC_AUTO_CREATE", true),
		OIDCRedirectURL:      strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URL")),
		OIDCStateTTL:         durationEnv("OIDC_STATE_TTL", 10*time.Minute),
		OIDCCAFile:           strings.TrimSpace(os.Getenv("OIDC_CA_FILE")),
	}
	if len(cfg.Namespaces) == 0 {
		return Config{}, fmt.Errorf("WATCH_NAMESPACES must contain at least one namespace")
	}
	if cfg.MetricsQueryStep <= 0 || cfg.MetricsCPURateWindow <= 0 || cfg.MetricsLookback <= 0 {
		return Config{}, fmt.Errorf("Prometheus lookback, query step, and CPU rate window must be positive durations")
	}
	if cfg.HistoryDefaultDays <= 0 {
		return Config{}, fmt.Errorf("HISTORY_DEFAULT_DAYS must be positive")
	}
	if cfg.LokiTimeout <= 0 || cfg.LokiMaxEntries <= 0 {
		return Config{}, fmt.Errorf("Loki query timeout and max entries must be positive")
	}
	if cfg.DatabaseURL == "" && (cfg.DatabaseUsername == "" || cfg.DatabasePassword == "") {
		return Config{}, fmt.Errorf("DATABASE_USERNAME and DATABASE_PASSWORD are required when DATABASE_URL is empty")
	}
	if cfg.AuthMode != "local" && cfg.AuthMode != "oidc" {
		return Config{}, fmt.Errorf("AUTH_MODE must be local or oidc")
	}
	if cfg.AuthSessionTTL <= 0 {
		return Config{}, fmt.Errorf("AUTH_SESSION_TTL must be positive")
	}
	if cfg.AuthBCryptCost < 10 || cfg.AuthBCryptCost > 15 {
		return Config{}, fmt.Errorf("AUTH_BCRYPT_COST must be between 10 and 15")
	}
	if cfg.AuthMode == "local" && (cfg.InitialAdminUsername == "" || cfg.InitialAdminPassword == "") {
		return Config{}, fmt.Errorf("AUTH_INITIAL_ADMIN_USERNAME and AUTH_INITIAL_ADMIN_PASSWORD are required")
	}
	if cfg.AuthMode == "oidc" && !cfg.OIDCEnabled {
		return Config{}, fmt.Errorf("OIDC_ENABLED must be true when AUTH_MODE=oidc")
	}
	if cfg.OIDCEnabled {
		if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" || cfg.OIDCClientSecret == "" {
			return Config{}, fmt.Errorf("OIDC_ISSUER_URL, OIDC_CLIENT_ID, and OIDC_CLIENT_SECRET are required when OIDC_ENABLED=true")
		}
		if cfg.OIDCUsernameClaim == "" || cfg.OIDCGroupsClaim == "" || cfg.OIDCStateTTL <= 0 {
			return Config{}, fmt.Errorf("OIDC username claim, groups claim, and state TTL must be configured")
		}
		if !contains(cfg.OIDCScopes, "openid") {
			return Config{}, fmt.Errorf("OIDC_SCOPES must include openid")
		}
	}
	if cfg.KubernetesAPIURL == "" {
		host := os.Getenv("KUBERNETES_SERVICE_HOST")
		port := env("KUBERNETES_SERVICE_PORT_HTTPS", "443")
		if host == "" {
			return Config{}, fmt.Errorf("KUBERNETES_SERVICE_HOST or KUBERNETES_API_URL is required")
		}
		cfg.KubernetesAPIURL = "https://" + host + ":" + port
	}
	return cfg, nil
}

func (c Config) PostgresDSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	dsn := &url.URL{
		Scheme: "postgres", User: url.UserPassword(c.DatabaseUsername, c.DatabasePassword),
		Host: net.JoinHostPort(c.DatabaseHost, strconv.Itoa(c.DatabasePort)), Path: "/" + c.DatabaseName,
	}
	query := dsn.Query()
	query.Set("sslmode", c.DatabaseSSLMode)
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func (c Config) NamespaceAllowed(namespace string) bool {
	for _, allowed := range c.Namespaces {
		if namespace == allowed {
			return true
		}
	}
	return false
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, raw := range strings.Split(value, ",") {
		item := strings.TrimSpace(raw)
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

func intEnv(key string, fallback int) int {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	value, err := strconv.ParseBool(env(key, strconv.FormatBool(fallback)))
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil {
		return fallback
	}
	return value
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
