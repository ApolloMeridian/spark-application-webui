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
	DatabaseURL          string
	DatabaseHost         string
	DatabasePort         int
	DatabaseName         string
	DatabaseSSLMode      string
	DatabaseUsername     string
	DatabasePassword     string
	DatabaseMaxOpenConns int32
	AdminBypass          bool
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
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		DatabaseHost:         env("DATABASE_HOST", "spark-postgres.spark-console.svc"),
		DatabasePort:         intEnv("DATABASE_PORT", 5432),
		DatabaseName:         env("DATABASE_NAME", "spark_console_db"),
		DatabaseSSLMode:      env("DATABASE_SSLMODE", "disable"),
		DatabaseUsername:     os.Getenv("DATABASE_USERNAME"),
		DatabasePassword:     os.Getenv("DATABASE_PASSWORD"),
		DatabaseMaxOpenConns: int32(intEnv("DATABASE_MAX_OPEN_CONNECTIONS", 20)),
		AdminBypass:          boolEnv("AUTH_ADMIN_BYPASS", true),
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
	if cfg.DatabaseURL == "" && (cfg.DatabaseUsername == "" || cfg.DatabasePassword == "") {
		return Config{}, fmt.Errorf("DATABASE_USERNAME and DATABASE_PASSWORD are required when DATABASE_URL is empty")
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
