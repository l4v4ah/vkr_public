package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/config"
	"github.com/slava-kov/monitoring-system/internal/logger"
	"github.com/slava-kov/monitoring-system/internal/metrics"
	natsclient "github.com/slava-kov/monitoring-system/internal/nats"
	"github.com/slava-kov/monitoring-system/internal/otel"
	"github.com/slava-kov/monitoring-system/internal/storage"
)

func main() {
	cfg := config.LoadAggregator()
	log := logger.New("aggregator")
	m := metrics.New("aggregator")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_, shutdown, err := otel.Setup(ctx, "aggregator", "1.0.0")
	if err != nil {
		log.Fatal("otel setup", zap.Error(err))
	}
	defer func() { _ = shutdown(context.Background()) }()

	db, err := storage.Connect(ctx, cfg.PostgresURL, "migrations")
	if err != nil {
		log.Fatal("postgres connect", zap.Error(err))
	}
	defer db.Close()

	nc, err := natsclient.Connect(cfg.NATSUrl)
	if err != nil {
		log.Fatal("nats connect", zap.Error(err))
	}
	defer nc.Close()

	// readiness flag: becomes 1 once consumers are running
	var ready atomic.Int32

	healthSrv := startHealthServer(cfg.HealthAddr, &ready, m, log)
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = healthSrv.Shutdown(shutCtx)
	}()

	c := newConsumer(db, nc, m, log)
	ready.Store(1)

	log.Info("aggregator started, consuming telemetry")
	c.Run(ctx)
	log.Info("aggregator stopped")
}

func startHealthServer(addr string, ready *atomic.Int32, m *metrics.ServiceMetrics, log *zap.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if ready.Load() == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.Handle("/metrics", m.Handler())

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Info("aggregator health listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server error", zap.Error(err))
		}
	}()
	return srv
}
