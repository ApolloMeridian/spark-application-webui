package main

import (
	"context"
	"log"
	"net/http"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"spark-control-center/object-storage/internal/sqlquery"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	settings, err := sqlquery.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	guard := exec.CommandContext(ctx, "java", "-Xms64m", "-Xmx256m", "-XX:ActiveProcessorCount=2",
		"-cp", "/app/sql-guard.jar:/app/lib/*", "org.sparkconsole.SqlGuard")
	if err := guard.Start(); err != nil {
		log.Fatal("start Spark SQL parser")
	}
	go func() {
		_ = guard.Wait()
		cancel()
	}()
	server, err := sqlquery.Open(ctx, settings)
	if err != nil {
		cancel()
		log.Fatal(err)
	}
	defer server.Close()
	httpServer := &http.Server{
		Addr: ":8083", Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal("start SQL HTTP listener")
	}
}
