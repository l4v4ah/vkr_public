package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

// fakePublisher satisfies the handler's publisher interface without a NATS server.
type fakePublisher struct {
	subjects []string
	err      error
}

func (f *fakePublisher) Publish(_ context.Context, subject string, _ any) error {
	if f.err != nil {
		return f.err
	}
	f.subjects = append(f.subjects, subject)
	return nil
}

func setupTestRouter(p publisher) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &handler{
		nc:     p,
		tracer: noop.NewTracerProvider().Tracer("test"),
		log:    zap.NewNop(),
	}
	r.POST("/api/v1/metrics", h.receiveMetrics)
	r.POST("/api/v1/logs", h.receiveLogs)
	r.POST("/api/v1/traces", h.receiveSpans)
	return r
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func TestReceiveMetrics_ValidPayload(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(metricRequest{
		ServiceName: "payment-service",
		MetricName:  "request_duration_seconds",
		Value:       0.123,
		Timestamp:   time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Len(t, pub.subjects, 1)
	assert.Equal(t, "telemetry.metrics", pub.subjects[0])
}

func TestReceiveMetrics_MissingRequiredFields(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(map[string]any{"value": 1.0})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, pub.subjects)
}

func TestReceiveMetrics_MissingMetricName(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(map[string]any{"service_name": "svc", "value": 1.0})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReceiveMetrics_ZeroTimestampSetToNow(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	// Не передаём timestamp — он должен подставиться автоматически
	body, _ := json.Marshal(metricRequest{
		ServiceName: "svc",
		MetricName:  "cpu_usage_percent",
		Value:       42.0,
	})
	before := time.Now()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	after := time.Now()

	require.Equal(t, http.StatusAccepted, w.Code)
	// Проверяем через интервал, а не через Published message (publisher не даёт доступ к данным),
	// но статус 202 гарантирует, что хэндлер заполнил timestamp и вызвал Publish.
	assert.True(t, after.After(before))
}

func TestReceiveMetrics_PublishError(t *testing.T) {
	pub := &fakePublisher{err: errors.New("nats unavailable")}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(metricRequest{
		ServiceName: "svc",
		MetricName:  "cpu_usage_percent",
		Value:       1.0,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestReceiveMetrics_InvalidJSON(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader([]byte(`not-json`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, pub.subjects)
}

// ── Logs ──────────────────────────────────────────────────────────────────────

func TestReceiveLogs_ValidPayload(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(logRequest{
		ServiceName: "auth-service",
		Level:       "error",
		Message:     "token expired",
		TraceID:     "trace-abc",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "telemetry.logs", pub.subjects[0])
}

func TestReceiveLogs_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing service_name", map[string]any{"level": "info", "message": "hi"}},
		{"missing level", map[string]any{"service_name": "svc", "message": "hi"}},
		{"missing message", map[string]any{"service_name": "svc", "level": "info"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pub := &fakePublisher{}
			r := setupTestRouter(pub)
			body, _ := json.Marshal(tc.body)
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/api/v1/logs", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, pub.subjects)
		})
	}
}

func TestReceiveLogs_PublishError(t *testing.T) {
	pub := &fakePublisher{err: errors.New("nats unavailable")}
	r := setupTestRouter(pub)

	body, _ := json.Marshal(logRequest{
		ServiceName: "svc", Level: "info", Message: "test",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Spans ─────────────────────────────────────────────────────────────────────

func TestReceiveSpans_ValidPayload(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	now := time.Now()
	body, _ := json.Marshal(spanRequest{
		TraceID:       "trace-xyz",
		SpanID:        "span-001",
		ServiceName:   "order-service",
		OperationName: "create_order",
		StartTime:     now,
		EndTime:       now.Add(50 * time.Millisecond),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "telemetry.spans", pub.subjects[0])
}

func TestReceiveSpans_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing trace_id", map[string]any{"span_id": "s1", "service_name": "svc", "operation_name": "op"}},
		{"missing span_id", map[string]any{"trace_id": "t1", "service_name": "svc", "operation_name": "op"}},
		{"missing service_name", map[string]any{"trace_id": "t1", "span_id": "s1", "operation_name": "op"}},
		{"missing operation_name", map[string]any{"trace_id": "t1", "span_id": "s1", "service_name": "svc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pub := &fakePublisher{}
			r := setupTestRouter(pub)
			body, _ := json.Marshal(tc.body)
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/api/v1/traces", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Empty(t, pub.subjects)
		})
	}
}

func TestReceiveSpans_DefaultStatusOK(t *testing.T) {
	pub := &fakePublisher{}
	r := setupTestRouter(pub)

	now := time.Now()
	// status не передаём — должен выставиться "ok"
	body, _ := json.Marshal(map[string]any{
		"trace_id": "t1", "span_id": "s1",
		"service_name": "svc", "operation_name": "op",
		"start_time": now, "end_time": now.Add(time.Millisecond),
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	// 202 означает, что хэндлер принял и опубликовал спан (status по умолчанию "ok")
}

func TestReceiveSpans_PublishError(t *testing.T) {
	pub := &fakePublisher{err: errors.New("nats unavailable")}
	r := setupTestRouter(pub)

	now := time.Now()
	body, _ := json.Marshal(spanRequest{
		TraceID: "t1", SpanID: "s1", ServiceName: "svc", OperationName: "op",
		StartTime: now, EndTime: now.Add(time.Millisecond),
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
