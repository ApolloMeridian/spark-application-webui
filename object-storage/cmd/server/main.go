package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spark-control-center/object-storage/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, e := storage.LoadConfig(os.Getenv("STORAGE_CONFIG_FILE"))
	if e != nil {
		logger.Error("invalid storage configuration")
		os.Exit(1)
	}
	key := []byte(os.Getenv("STORAGE_SIGNING_KEY"))
	if len(key) < 32 {
		logger.Error("storage signing key must be at least 32 bytes")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(os.Getenv("DATABASE_HOST"), fallback("DATABASE_PORT", "5432")), Path: "/" + os.Getenv("DATABASE_NAME"), User: url.UserPassword(os.Getenv("DATABASE_USERNAME"), os.Getenv("DATABASE_PASSWORD"))}
		q := u.Query()
		q.Set("sslmode", fallback("DATABASE_SSLMODE", "disable"))
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	init, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, e := storage.OpenStore(init, dsn)
	if e != nil {
		logger.Error("initialize storage database failed; check database configuration and connectivity")
		os.Exit(1)
	}
	defer store.Pool.Close()
	providers := map[string]storage.Provider{}
	for _, x := range c.Connections {
		var p storage.Provider
		if x.Provider == "s3" {
			p, e = storage.NewS3(x, c.PartSize)
		} else {
			p, e = storage.NewGCS(ctx, x, c.PartSize)
		}
		if e != nil {
			logger.Error("initialize storage provider", "connection", x.ID)
			os.Exit(1)
		}
		providers[x.ID] = p
	}
	c.Bootstrap, e = c.ExpandedBootstrap()
	if e != nil {
		logger.Error("invalid bootstrap grants", "error", e)
		os.Exit(1)
	}
	if e = store.Bootstrap(init, c.Bootstrap); e != nil {
		logger.Error("initialize storage grants", "error", e)
		os.Exit(1)
	}
	plugin := storage.NewServer(c, store, key, providers, logger)
	server := http.Server{Addr: fallback("HTTP_ADDR", ":8082"), Handler: plugin.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.Timeout(), WriteTimeout: c.Timeout(), IdleTimeout: 60 * time.Second, MaxHeaderBytes: 65536}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run, cancel := context.WithTimeout(ctx, 15*time.Second)
				e := store.Bootstrap(run, c.Bootstrap)
				if e == nil {
					e = store.Clean(run, c.RetentionDays)
				}
				if e != nil {
					logger.Warn("storage maintenance failed")
				}
				cancel()
			}
		}
	}()
	go func() {
		<-ctx.Done()
		run, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(run)
	}()
	logger.Info("object storage plugin listening", "address", server.Addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		logger.Error("storage HTTP server failed")
		os.Exit(1)
	}
}
func fallback(name, value string) string {
	if x := os.Getenv(name); x != "" {
		return x
	}
	return value
}
