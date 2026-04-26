-- Telemetry database schema for the monitoring system.
-- Managed by golang-migrate; run `make migrate-up` to apply.

CREATE TABLE IF NOT EXISTS metrics (
    id           BIGSERIAL PRIMARY KEY,
    service_name TEXT             NOT NULL,
    metric_name  TEXT             NOT NULL,
    value        DOUBLE PRECISION NOT NULL,
    labels       JSONB            NOT NULL DEFAULT '{}',
    timestamp    TIMESTAMPTZ      NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_metrics_service_ts ON metrics (service_name, timestamp DESC);
CREATE INDEX idx_metrics_name       ON metrics (metric_name);

CREATE TABLE IF NOT EXISTS logs (
    id           BIGSERIAL PRIMARY KEY,
    service_name TEXT        NOT NULL,
    level        TEXT        NOT NULL,
    message      TEXT        NOT NULL,
    trace_id     TEXT        NOT NULL DEFAULT '',
    fields       JSONB       NOT NULL DEFAULT '{}',
    timestamp    TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_logs_service_ts ON logs (service_name, timestamp DESC);
CREATE INDEX idx_logs_level      ON logs (level);
CREATE INDEX idx_logs_trace      ON logs (trace_id) WHERE trace_id <> '';

CREATE TABLE IF NOT EXISTS spans (
    id             BIGSERIAL PRIMARY KEY,
    trace_id       TEXT        NOT NULL,
    span_id        TEXT        NOT NULL,
    parent_span_id TEXT        NOT NULL DEFAULT '',
    service_name   TEXT        NOT NULL,
    operation_name TEXT        NOT NULL,
    start_time     TIMESTAMPTZ NOT NULL,
    end_time       TIMESTAMPTZ NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'ok',
    attributes     JSONB       NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_spans_trace ON spans (trace_id);
CREATE INDEX idx_spans_service ON spans (service_name, start_time DESC);
CREATE UNIQUE INDEX idx_spans_span_id ON spans (span_id);
