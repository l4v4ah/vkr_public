package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/config"
	"github.com/slava-kov/monitoring-system/internal/logger"
)

func main() {
	cfg := config.LoadAgent()
	log := logger.New("agent")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := newCollectorClient(cfg.CollectorGRPC)
	if err != nil {
		log.Fatal("grpc connect", zap.Error(err))
	}

	thresh := newThresholds()
	go pollConfig(ctx, cfg.APIURL, thresh)

	alert := newAlerter(cfg.TelegramToken, cfg.TelegramChatID, log)

	log.Info("agent started",
		zap.String("collector_grpc", cfg.CollectorGRPC),
		zap.String("service_name", cfg.ServiceName),
		zap.String("api_url", cfg.APIURL),
		zap.Bool("telegram_alerts", alert.enabled()),
		zap.String("mode", "continuous"),
	)

	newSystemCollector(cfg.ServiceName, client, log, thresh, alert).Run(ctx)
	log.Info("agent stopped")
}
