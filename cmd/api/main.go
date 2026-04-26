package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/config"
	"github.com/slava-kov/monitoring-system/internal/logger"
	"github.com/slava-kov/monitoring-system/internal/metrics"
	"github.com/slava-kov/monitoring-system/internal/otel"
	"github.com/slava-kov/monitoring-system/internal/storage"
)

func main() {
	cfg := config.LoadAPI()
	log := logger.New("api")
	m := metrics.New("api")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracer, shutdown, err := otel.Setup(ctx, "api", "1.0.0")
	if err != nil {
		log.Fatal("otel setup", zap.Error(err))
	}
	defer func() { _ = shutdown(context.Background()) }()

	db, err := storage.Connect(ctx, cfg.PostgresURL, "")
	if err != nil {
		log.Fatal("postgres connect", zap.Error(err))
	}
	defer db.Close()

	thresh := newThresholdStore(db, log)
	srv := newServer(cfg.HTTPAddr, db, m, tracer, log, cfg.APIKey, thresh)

	go func() {
		log.Info("api listening", zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down api")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
