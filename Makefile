SHELL  := /bin/bash
MODULE := github.com/slava-kov/monitoring-system

PROTO_DIR := proto/telemetry
GEN_DIR   := gen/telemetry

BIN_DIR   := bin
SERVICES  := collector aggregator api agent

# Переменные для Docker/K8s
COMPOSE   := docker-compose -f deployments/docker-compose.yml
DB_URL   ?= postgres://monitoring:monitoring@localhost:5432/monitoring?sslmode=disable
REGISTRY ?= ghcr.io/slava-kov/monitoring-system
TAG      ?= latest

.PHONY: all tidy proto \
        build $(addprefix build-,$(SERVICES)) build-all \
        test test-coverage integration-test \
        lint \
        migrate-up migrate-down migrate-status \
        docker-up docker-down docker-restart docker-logs docker-ps \
        docker-build $(addprefix docker-build-,$(SERVICES)) \
        k8s-apply k8s-delete k8s-status k8s-rollout \
        run-agent run-api \
        clean clean-bin help

# ── По умолчанию ──────────────────────────────────────────────────────────────

all: tidy build test

# ── Зависимости ───────────────────────────────────────────────────────────────

tidy:
	go mod tidy

# ── Protobuf ──────────────────────────────────────────────────────────────────
# Требует: protoc, protoc-gen-go, protoc-gen-go-grpc
# Установка:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

proto:
	mkdir -p $(GEN_DIR)
	protoc \
		--proto_path=$(PROTO_DIR) \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/telemetry.proto

# ── Сборка ────────────────────────────────────────────────────────────────────

build:
	CGO_ENABLED=0 go build ./...

$(addprefix build-,$(SERVICES)):
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o $(BIN_DIR)/$(patsubst build-%,%,$@) \
		./cmd/$(patsubst build-%,%,$@)

build-all: $(addprefix build-,$(SERVICES))

# ── Тесты ─────────────────────────────────────────────────────────────────────

# Unit-тесты (быстрые, без Docker)
test:
	go test -count=1 -race -timeout=60s ./cmd/...

# Тесты с отчётом покрытия
test-coverage:
	go test -count=1 -race -timeout=60s -coverprofile=coverage.out ./cmd/...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html
	@echo "Отчёт: coverage.html"

# Интеграционные тесты (требуют запущенный Docker)
integration-test:
	go test -count=1 -race -timeout=120s ./internal/storage/...

# Все тесты
test-all: test integration-test

# ── Линтинг ───────────────────────────────────────────────────────────────────
# Требует: golangci-lint  https://golangci-lint.run/usage/install/

lint:
	golangci-lint run --timeout=5m ./...

# ── Миграции БД ───────────────────────────────────────────────────────────────
# Требует: migrate CLI  https://github.com/golang-migrate/migrate

migrate-up:
	migrate -path migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path migrations -database "$(DB_URL)" down 1

migrate-status:
	migrate -path migrations -database "$(DB_URL)" version

# ── Docker Compose ────────────────────────────────────────────────────────────

docker-up:
	$(COMPOSE) up -d --build --remove-orphans

docker-down:
	$(COMPOSE) down -v

docker-restart:
	$(COMPOSE) restart

docker-logs:
	$(COMPOSE) logs -f

docker-ps:
	$(COMPOSE) ps

docker-build:
	$(COMPOSE) build --no-cache

$(addprefix docker-build-,$(SERVICES)):
	docker build -f Dockerfile.$(patsubst docker-build-%,%,$@) \
		-t $(REGISTRY)/$(patsubst docker-build-%,%,$@):$(TAG) .

# ── Запуск сервисов локально (вне Docker) ────────────────────────────────────

# Запустить агент на хосте (нужно для Telegram-алертов из-за сетевой изоляции Docker)
run-agent:
	COLLECTOR_GRPC=localhost:9090 \
	API_URL=http://localhost:8081 \
	SERVICE_NAME=$$(hostname) \
	go run ./cmd/agent

# Запустить API-сервер локально (БД и NATS берёт из docker-compose)
run-api:
	HTTP_ADDR=:8081 \
	POSTGRES_URL=$(DB_URL) \
	go run ./cmd/api

# ── Kubernetes ────────────────────────────────────────────────────────────────

k8s-apply:
	kubectl apply -f deployments/k8s/

k8s-delete:
	kubectl delete -f deployments/k8s/

k8s-status:
	kubectl get pods,svc,ingress -n monitoring

k8s-rollout:
	kubectl rollout status deployment/collector  -n monitoring
	kubectl rollout status deployment/aggregator -n monitoring
	kubectl rollout status deployment/api        -n monitoring
	kubectl rollout status daemonset/agent       -n monitoring

# ── Очистка ───────────────────────────────────────────────────────────────────

clean:
	rm -rf $(BIN_DIR)/ coverage.out coverage.html

clean-bin:
	rm -f $(addprefix $(BIN_DIR)/,$(SERVICES))

# ── Справка ───────────────────────────────────────────────────────────────────

help:
	@echo ""
	@echo "  Monitoring System — доступные цели:"
	@echo ""
	@echo "  Сборка:"
	@echo "    make build              — проверить компиляцию всех пакетов"
	@echo "    make build-all          — собрать все 4 бинаря в bin/"
	@echo "    make build-agent        — собрать только агент"
	@echo "    make build-collector    — собрать только collector"
	@echo "    make build-aggregator   — собрать только aggregator"
	@echo "    make build-api          — собрать только api"
	@echo ""
	@echo "  Тесты:"
	@echo "    make test               — unit-тесты (без Docker)"
	@echo "    make test-coverage      — unit-тесты + HTML отчёт покрытия"
	@echo "    make integration-test   — интеграционные тесты (нужен Docker)"
	@echo "    make test-all           — все тесты"
	@echo ""
	@echo "  Разработка:"
	@echo "    make run-agent          — запустить агент на хосте"
	@echo "    make run-api            — запустить API локально"
	@echo "    make lint               — запустить golangci-lint"
	@echo "    make tidy               — go mod tidy"
	@echo "    make proto              — сгенерировать protobuf"
	@echo ""
	@echo "  Docker:"
	@echo "    make docker-up          — поднять весь стек"
	@echo "    make docker-down        — остановить и удалить тома"
	@echo "    make docker-restart     — перезапустить сервисы"
	@echo "    make docker-logs        — следить за логами"
	@echo "    make docker-ps          — статус контейнеров"
	@echo ""
	@echo "  БД:"
	@echo "    make migrate-up         — применить миграции"
	@echo "    make migrate-down       — откатить последнюю миграцию"
	@echo "    make migrate-status     — текущая версия миграций"
	@echo ""
	@echo "  Kubernetes:"
	@echo "    make k8s-apply          — применить манифесты"
	@echo "    make k8s-status         — статус подов и сервисов"
	@echo "    make k8s-rollout        — ждать завершения rollout"
	@echo ""
	@echo "  make clean                — удалить бинари + отчёты покрытия"
	@echo "  make clean-bin            — удалить только бинари (collector/aggregator/api/agent)"
	@echo ""
