package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spark-control-center/backend/internal/api"
	"spark-control-center/backend/internal/config"
	"spark-control-center/backend/internal/kube"
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
	prometheus := metrics.New(cfg.PrometheusURL, cfg.PrometheusTimeout, cfg.MetricsLookback)
	applicationService := service.New(cfg, kubernetes, prometheus, audits, logger)
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: api.New(cfg, applicationService, logger),
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
	logger.Info("Spark Control Center backend listening", "address", cfg.HTTPAddr, "namespaces", cfg.Namespaces, "admin_bypass", cfg.AdminBypass)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
