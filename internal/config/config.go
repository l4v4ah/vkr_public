// Package config loads service configuration from environment variables.
package config

import "os"

// Collector holds configuration for the collector service.
type Collector struct {
	HTTPAddr string // e.g. ":8080"
	GRPCAddr string // e.g. ":9090"
	NATSUrl  string // e.g. "nats://nats:4222"
}

// Aggregator holds configuration for the aggregator service.
type Aggregator struct {
	NATSUrl     string
	PostgresURL string
	HealthAddr  string
}

// API holds configuration for the API gateway service.
type API struct {
	HTTPAddr    string
	PostgresURL string
	APIKey      string // empty = auth disabled
}

func LoadCollector() Collector {
	return Collector{
		HTTPAddr: env("HTTP_ADDR", ":8080"),
		GRPCAddr: env("GRPC_ADDR", ":9090"),
		NATSUrl:  env("NATS_URL", "nats://localhost:4222"),
	}
}

func LoadAggregator() Aggregator {
	return Aggregator{
		NATSUrl:     env("NATS_URL", "nats://localhost:4222"),
		PostgresURL: env("POSTGRES_URL", "postgres://monitoring:monitoring@localhost:5432/monitoring?sslmode=disable"),
		HealthAddr:  env("HEALTH_ADDR", ":8082"),
	}
}

func LoadAPI() API {
	return API{
		HTTPAddr:    env("HTTP_ADDR", ":8081"),
		PostgresURL: env("POSTGRES_URL", "postgres://monitoring:monitoring@localhost:5432/monitoring?sslmode=disable"),
		APIKey:      env("API_KEY", ""),
	}
}

// Agent holds configuration for the system metrics agent.
type Agent struct {
	CollectorGRPC  string
	ServiceName    string
	APIURL         string // optional: poll threshold config from the API service
	TelegramToken  string // optional: Telegram bot token for alerts
	TelegramChatID string // optional: Telegram chat ID for alerts
}

func LoadAgent() Agent {
	return Agent{
		CollectorGRPC:  env("COLLECTOR_GRPC", "localhost:9090"),
		ServiceName:    env("SERVICE_NAME", "host-agent"),
		APIURL:         env("API_URL", ""),
		TelegramToken:  env("TELEGRAM_TOKEN", ""),
		TelegramChatID: env("TELEGRAM_CHAT_ID", ""),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
