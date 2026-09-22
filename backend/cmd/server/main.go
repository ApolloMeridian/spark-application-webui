package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spark-control-center/backend/internal/api"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/kube"
	"spark-control-center/backend/internal/localauth"
	"spark-control-center/backend/internal/loki"
	"spark-control-center/backend/internal/oidcauth"
	metrics "spark-control-center/backend/internal/prometheus"
	"spark-control-center/backend/internal/service"
	"spark-control-center/backend/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	kubernetes, err := kube.New(cfg)
	if err != nil {
		logger.Error("initialize Kubernetes client", "error", err)
		os.Exit(1)
	}
	audits, err := store.Open(ctx, cfg.PostgresDSN(), cfg.DatabaseMaxOpenConns)
	if err != nil {
		logger.Error("initialize PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer audits.Close()
	authService := localauth.New(audits, cfg.AuthSessionTTL, cfg.AuthBCryptCost)
	if cfg.AuthMode == "local" {
		if err := authService.Bootstrap(ctx, cfg.InitialAdminUsername, cfg.InitialAdminPassword, cfg.InitialAdminName); err != nil {
			logger.Error("bootstrap local administrator", "error", err)
			os.Exit(1)
		}
	}
	var oidcService *oidcauth.Service
	if cfg.OIDCEnabled {
		oidcHTTPClient, err := newOIDCHTTPClient(cfg.OIDCCAFile)
		if err != nil {
			logger.Error("configure OIDC TLS", "error", err)
			os.Exit(1)
		}
		oidcService, err = oidcauth.New(ctx, oidcauth.Config{
			IssuerURL: cfg.OIDCIssuerURL, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			Scopes: cfg.OIDCScopes, GroupsClaim: cfg.OIDCGroupsClaim, AdminGroups: cfg.OIDCAdminGroups,
			UsernameClaim: cfg.OIDCUsernameClaim, AutoCreate: cfg.OIDCAutoCreate, StateTTL: cfg.OIDCStateTTL,
		}, audits, authService, oidcHTTPClient)
		if err != nil {
			logger.Error("initialize OIDC provider", "error", err)
			os.Exit(1)
		}
	}
	prometheus := metrics.New(cfg.PrometheusURL, cfg.PrometheusTimeout, cfg.MetricsLookback, cfg.MetricsQueryStep, cfg.MetricsCPURateWindow)
	var logSource service.LogSource
	if cfg.LokiURL != "" {
		logSource = loki.New(cfg.LokiURL, cfg.LokiTimeout, cfg.LokiMaxEntries)
	}
	applicationService := service.New(cfg, kubernetes, prometheus, audits, logger, logSource)
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: api.New(cfg, applicationService, authService, oidcService, logger),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 120 * time.Second,
	}

	shutdownSignals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownSignals.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()
	logger.Info("Spark Control Center backend listening", "address", cfg.HTTPAddr, "namespaces", cfg.Namespaces, "auth_mode", cfg.AuthMode, "oidc_enabled", cfg.OIDCEnabled)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}

func newOIDCHTTPClient(caFile string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport has unexpected type")
	}
	clone := transport.Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("OIDC CA file does not contain a valid PEM certificate")
		}
		clone.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: clone, Timeout: 20 * time.Second}, nil
}
