# Микросервисная система мониторинга на Go

Программный прототип к выпускной квалификационной работе  
**«Разработка микросервисной архитектуры системы мониторинга на языке Go»**  
Ковалев Вячеслав Дмитриевич, РГСУ, направление 09.03.04 Программная инженерия

---

## Содержание

- [Архитектура](#архитектура)
- [Технологический стек](#технологический-стек)
- [Структура проекта](#структура-проекта)
- [Быстрый старт](#быстрый-старт)
- [Веб-интерфейс](#веб-интерфейс)
- [API-интерфейс](#api-интерфейс)
- [Агент на удалённом сервере](#агент-на-удалённом-сервере)
- [Тестирование](#тестирование)
- [Развёртывание в Kubernetes](#развёртывание-в-kubernetes)
- [Наблюдаемость](#наблюдаемость)

---

## Архитектура

Система состоит из четырёх независимых компонентов, каждый из которых решает одну строго ограниченную задачу.

```
   Агенты (на каждом сервере)
         │
         │ gRPC :9090
         ▼
  ┌─────────────┐      NATS JetStream       ┌──────────────┐
  │  collector  │ ─── telemetry.* ────────▶ │  aggregator  │
  │  HTTP :8080 │                           │              │
  │  gRPC :9090 │                           └──────┬───────┘
  └─────────────┘                                  │ INSERT
                                                   ▼
                                           ┌──────────────┐
                                           │  PostgreSQL   │
                                           └──────┬───────┘
                                                  │ SELECT
                                                  ▼
                                           ┌──────────────┐
                                           │     api      │
                                           │  HTTP :8081  │
                                           │  Web + SSE   │
                                           └──────────────┘
```

**Поток данных:**
1. **Agent** непрерывно собирает системные метрики (CPU, память, диск, сеть) через `gopsutil` и отправляет их в **collector** по gRPC.
2. **Collector** немедленно публикует сообщения в **NATS JetStream** (асинхронный буфер), освобождая входящий поток.
3. **Aggregator** подписывается на все субъекты `telemetry.*`, декодирует сообщения и сохраняет их в **PostgreSQL**.
4. **API** предоставляет REST-эндпоинты для чтения данных и Server-Sent Events для браузерного дашборда в реальном времени.

Каждый сервис передаёт сквозной OpenTelemetry trace context и пишет структурированные JSON-логи через `zap`.  
Серверные сервисы (collector, aggregator, api) экспортируют Prometheus-метрики на `/metrics`; агент передаёт собственную телеметрию в collector и отдельного HTTP-эндпоинта не имеет.

---

## Технологический стек

| Роль | Инструмент | Обоснование |
|------|-----------|-------------|
| Язык | **Go 1.25** | Компактные сетевые сервисы, удобная модель конкурентности, простой деплой |
| HTTP-фреймворк | **Gin** | Баланс между скоростью, удобством middleware и совместимостью с Go-экосистемой |
| Асинхронный транспорт | **NATS JetStream** | Буферизация потока телеметрии, гарантированная доставка, low-overhead |
| Внутренний RPC-контракт | **gRPC + Protobuf** | Строгая типизация, бинарная сериализация, streaming |
| Сбор системных метрик | **gopsutil/v3** | CPU, память, диск, сеть без cgo зависимостей |
| Хранилище | **PostgreSQL 16** | Надёжное хранение временных рядов метрик, логов и трассировок |
| Миграции | **golang-migrate** | Воспроизводимое изменение схемы без ручных SQL-операций |
| Real-time пуш | **Server-Sent Events** | Однонаправленный поток из сервера в браузер без WebSocket |
| Фронтенд | **Chart.js** (CDN) | Живые графики без этапа сборки, встроен в бинарник через `go:embed` |
| Метрики | **Prometheus client** | Быстрый `/metrics` endpoint, интеграция с Kubernetes |
| Трассировка | **OpenTelemetry** | Vendor-neutral сквозной trace context через все сервисы |
| Логирование | **zap** | Структурированный JSON, пригодный для машинной корреляции |
| Контейнеризация | **Docker** | Воспроизводимая среда выполнения каждого сервиса |
| Оркестрация (локально) | **Docker Compose** | Единый контур запуска с зависимостями и healthcheck |
| Оркестрация (prod) | **Kubernetes** | Декларативное управление, HPA, DaemonSet для агентов, rolling update |
| CI | **GitHub Actions** | Линт → тесты → интеграционные тесты → сборка и публикация образов |
| Линтинг | **golangci-lint** | Раннее выявление дефектов конкурентности и обработки ошибок |
| Тесты | **testify + Testcontainers** | Unit-тесты с fake-заглушками и интеграционные тесты с реальным PostgreSQL |

---

## Структура проекта

```
monitoring-system/
├── cmd/
│   ├── collector/          # Приём телеметрии по HTTP + gRPC → NATS
│   │   ├── main.go
│   │   ├── server.go       # Gin-роутер + middleware
│   │   ├── handler.go      # HTTP-обработчики, интерфейс publisher
│   │   ├── grpc_server.go  # gRPC-сервер CollectorService
│   │   ├── ratelimit.go    # Per-IP rate limiter (golang.org/x/time/rate)
│   │   └── handler_test.go # Unit-тесты с fake NATS
│   ├── aggregator/         # NATS → PostgreSQL
│   │   ├── main.go         # HTTP health :8082 + atomic readiness flag
│   │   └── consumer.go     # Параллельные потребители по каждому типу сигнала
│   ├── api/                # REST API + веб-дашборд
│   │   ├── main.go
│   │   ├── router.go       # Gin-роутер, API-key middleware
│   │   ├── handler.go      # Обработчики с OTel-трассировкой
│   │   ├── sse.go          # Server-Sent Events: /stream, /stream/services
│   │   ├── static.go       # //go:embed static
│   │   └── static/
│   │       └── index.html  # SPA: сайдбар серверов, Overview, Dashboard с Chart.js
│   └── agent/              # Агент сбора системных метрик
│       ├── main.go         # Точка входа, signal context
│       ├── collector.go    # 4 горутины: CPU / память / диск / сеть
│       └── client.go       # gRPC-клиент CollectorService
├── internal/
│   ├── config/             # Конфигурация из переменных окружения
│   ├── logger/             # Инициализация zap
│   ├── metrics/            # Prometheus-регистры и HTTP-хендлер
│   ├── nats/               # JetStream: создание стрима, Publish, Subscribe
│   ├── otel/               # TracerProvider (stdout/OTLP), propagator
│   └── storage/
│       ├── postgres.go     # Пул соединений pgx + запуск миграций
│       ├── repository.go   # InsertMetric, QueryMetrics, QueryServices …
│       └── repository_test.go  # Интеграционные тесты через Testcontainers
├── proto/telemetry/
│   └── telemetry.proto     # gRPC-контракт: MetricPoint, LogEntry, TraceSpan
├── gen/telemetry/          # Сгенерированный Go-код из .proto
├── migrations/
│   ├── 000001_init.up.sql       # Таблицы metrics, logs, spans + индексы
│   ├── 000001_init.down.sql
│   ├── 000002_retention.up.sql  # Функция cleanup_old_telemetry()
│   └── 000002_retention.down.sql
├── deployments/
│   ├── docker-compose.yml  # Локальная среда: postgres, nats, все сервисы + агент
│   └── k8s/                # Kubernetes манифесты
│       ├── namespace.yaml
│       ├── secrets.yaml         # PostgreSQL URL, API key
│       ├── nats.yaml
│       ├── postgres.yaml
│       ├── collector.yaml       # Deployment + Service (HTTP :8080, gRPC :9090) + HPA
│       ├── aggregator.yaml
│       ├── api.yaml
│       ├── agent.yaml           # DaemonSet — агент на каждом узле кластера
│       ├── ingress.yaml         # NGINX Ingress + TLS
│       ├── migrations-job.yaml  # Job для запуска миграций
│       └── retention-cronjob.yaml  # CronJob еженедельной очистки старых данных
├── .github/workflows/
│   └── ci.yml              # lint → test → integration-test → build → deploy-k8s
├── .golangci.yml           # Конфигурация линтера
├── Dockerfile.collector
├── Dockerfile.aggregator
├── Dockerfile.api
├── Dockerfile.agent
├── Makefile
└── go.mod
```

---

## Быстрый старт

### Требования

- [Go 1.25+](https://go.dev/dl/)
- [Docker + Docker Compose](https://docs.docker.com/get-docker/)

### Локальный запуск

```bash
# 1. Перейти в директорию проекта
cd monitoring-system

# 2. Скачать зависимости
make tidy

# 3. Поднять инфраструктуру и все сервисы
make docker-up
```

После запуска:

| Сервис | URL |
|--------|-----|
| Веб-дашборд | http://localhost:8081 |
| Collector HTTP | http://localhost:8080 |
| Collector gRPC | localhost:9090 |
| API | http://localhost:8081 |
| NATS monitoring | http://localhost:8222 |

```bash
# Просмотр логов всех сервисов
make docker-logs

# Остановить и удалить тома
make docker-down
```

### Сборка без Docker

```bash
make build-collector
make build-aggregator
make build-api
make build-agent
```

### Генерация gRPC-кода из .proto

```bash
# Установить плагины:
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

make proto
```

---

## Веб-интерфейс

Дашборд встроен в бинарник `api` через `//go:embed static` и доступен по адресу **http://localhost:8081**.

### Возможности

- **Сайдбар серверов** — список всех подключённых агентов с мини-индикаторами CPU/RAM/Disk и статусом (онлайн / офлайн).
- **Overview** — сводная сетка карточек по всем серверам с ключевыми метриками.
- **Dashboard** — живые графики (Chart.js) CPU, памяти, диска и сети с бейджем «● Live»; обновление через SSE без перезагрузки страницы.
- **Metrics / Logs / Traces** — табличные представления с фильтрами по сервису, времени и уровню.
- **API-ключ** — авторизация через overlay при первом входе; ключ сохраняется в `localStorage`.

### Server-Sent Events

| Эндпоинт | Описание |
|----------|----------|
| `GET /api/v1/stream?api_key=…` | Поток метрик выбранного сервиса (`event: snapshot` + `event: update`) |
| `GET /api/v1/stream/services?api_key=…` | Поток списка серверов каждые 5 с |

---

## API-интерфейс

### Collector — приём телеметрии

#### POST /api/v1/metrics

```bash
curl -X POST http://localhost:8080/api/v1/metrics \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "payment-service",
    "metric_name":  "request_duration_seconds",
    "value":        0.123,
    "labels":       {"method": "POST", "status": "200"},
    "timestamp":    "2026-04-19T12:00:00Z"
  }'
```

#### POST /api/v1/logs

```bash
curl -X POST http://localhost:8080/api/v1/logs \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "auth-service",
    "level":        "error",
    "message":      "token validation failed",
    "trace_id":     "abc123def456",
    "fields":       {"user_id": "42", "ip": "10.0.0.1"}
  }'
```

#### POST /api/v1/traces

```bash
curl -X POST http://localhost:8080/api/v1/traces \
  -H "Content-Type: application/json" \
  -d '{
    "trace_id":       "abc123def456",
    "span_id":        "span-001",
    "service_name":   "order-service",
    "operation_name": "create_order",
    "start_time":     "2026-04-19T12:00:00Z",
    "end_time":       "2026-04-19T12:00:00.123Z",
    "status":         "ok",
    "attributes":     {"db.system": "postgresql"}
  }'
```

---

### API — чтение данных

Все эндпоинты защищены заголовком `X-API-Key` (если задана переменная `API_KEY`).

#### GET /api/v1/services

```bash
curl -H "X-API-Key: secret" http://localhost:8081/api/v1/services
```

Возвращает список подключённых серверов с последними значениями CPU, RAM, Disk.

#### GET /api/v1/metrics

```bash
curl -H "X-API-Key: secret" \
  "http://localhost:8081/api/v1/metrics?service=host-agent&from=2026-04-19T00:00:00Z&to=2026-04-19T23:59:59Z"
```

#### GET /api/v1/logs

```bash
curl -H "X-API-Key: secret" \
  "http://localhost:8081/api/v1/logs?service=auth-service&level=error&limit=50"
```

#### GET /api/v1/traces/:trace_id

```bash
curl -H "X-API-Key: secret" \
  "http://localhost:8081/api/v1/traces/abc123def456"
```

#### GET /health

```bash
curl http://localhost:8080/health
curl http://localhost:8081/health
```

---

## Агент на удалённом сервере

Агент — отдельный бинарник, который подключается к **collector** по gRPC и непрерывно стримит системные метрики (CPU каждые ~500 мс, RAM — 1 с, диск — 2 с, сеть — 500 мс). Каждый агент идентифицируется параметром `SERVICE_NAME`.

Порт `9090` collector уже открыт наружу в `docker-compose.yml`, поэтому удалённый агент подключается напрямую.

### Вариант 1 — бинарник

```bash
# На машине с кодом — собрать под Linux x86-64:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -o agent ./cmd/agent

# Скопировать на удалённый сервер:
scp agent user@remote-server:/usr/local/bin/agent

# На удалённом сервере запустить:
COLLECTOR_GRPC=<IP_основного_сервера>:9090 \
SERVICE_NAME=server-2 \
/usr/local/bin/agent
```

### Вариант 2 — Docker

```bash
docker run -d \
  -e COLLECTOR_GRPC=<IP_основного_сервера>:9090 \
  -e SERVICE_NAME=server-2 \
  --name monitoring-agent \
  --restart unless-stopped \
  ghcr.io/slava-kov/monitoring-system/agent:latest
```

### Вариант 3 — systemd (продакшен)

```ini
# /etc/systemd/system/monitoring-agent.service
[Unit]
Description=Monitoring Agent
After=network.target

[Service]
Environment=COLLECTOR_GRPC=<IP>:9090
Environment=SERVICE_NAME=server-2
ExecStart=/usr/local/bin/agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable --now monitoring-agent
```

После запуска агент появится в сайдбаре дашборда автоматически — без перезапуска остальных сервисов.

---

## Тестирование

### Unit-тесты (без внешних зависимостей)

```bash
make test
# или напрямую:
go test -race -count=1 ./cmd/...
```

Тесты collector используют интерфейс `publisher` с fake-заглушкой вместо реального NATS — сервис тестируется изолированно.

### Интеграционные тесты (Testcontainers)

```bash
make integration-test
# или:
go test -race -count=1 -timeout=120s -tags=integration ./internal/storage/...
```

Поднимает реальный PostgreSQL-контейнер, применяет миграции и проверяет полный цикл записи и чтения для метрик, логов и трассировок.

### Линтинг

```bash
make lint
```

---

## Развёртывание в Kubernetes

```bash
# Создать namespace и применить все манифесты
make k8s-apply

# Проверить статус
kubectl get pods -n monitoring
kubectl get hpa -n monitoring
kubectl get daemonset -n monitoring   # агент на каждом узле

# Удалить
make k8s-delete
```

Для каждого сервиса настроены:
- `Deployment` / `DaemonSet` (агент) с readiness/liveness probe
- `HorizontalPodAutoscaler` (CPU > 70% → масштабирование) для collector и api
- Prometheus-аннотации для автоматического scrape

Секреты создаются отдельно:

```bash
kubectl create secret generic monitoring-secrets \
  --from-literal=postgres-url="postgres://user:pass@host:5432/db?sslmode=require" \
  --from-literal=api-key="your-secret-key" \
  -n monitoring
```

---

## Наблюдаемость

### Prometheus-метрики

Серверные сервисы (collector, aggregator, api) экспортируют на `/metrics`:
- `monitoring_<service>_requests_total{method, path, status}` — счётчик запросов
- `monitoring_<service>_request_duration_seconds{method, path}` — гистограмма задержек
- `monitoring_<service>_errors_total{type}` — счётчик ошибок
- стандартные Go runtime метрики (goroutines, GC, heap)

### OpenTelemetry-трассировка

По умолчанию трассировки выводятся в stdout (режим разработки).  
Для отправки в Jaeger / OpenTelemetry Collector установите переменную:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
```

### Структурированные логи

Все сервисы пишут JSON в stdout:

```json
{
  "ts": "2026-04-19T12:00:00.000Z",
  "level": "info",
  "service": "collector",
  "msg": "request",
  "method": "POST",
  "path": "/api/v1/metrics",
  "status": 202,
  "latency": "1.2ms"
}
```

Уровень логирования задаётся через `LOG_LEVEL` (debug / info / warn / error).

---

## Переменные окружения

| Переменная | Сервис | Значение по умолчанию |
|-----------|--------|----------------------|
| `HTTP_ADDR` | collector, api | `:8080` / `:8081` |
| `GRPC_ADDR` | collector | `:9090` |
| `NATS_URL` | collector, aggregator | `nats://localhost:4222` |
| `POSTGRES_URL` | aggregator, api | `postgres://monitoring:monitoring@localhost:5432/monitoring?sslmode=disable` |
| `API_KEY` | api | не задан (авторизация отключена) |
| `HEALTH_ADDR` | aggregator | `:8082` |
| `COLLECTOR_GRPC` | agent | `localhost:9090` |
| `SERVICE_NAME` | agent | `host-agent` |
| `LOG_LEVEL` | все | `info` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | все | не задан (stdout) |
