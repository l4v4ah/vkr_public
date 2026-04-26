package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// publisher is the minimal interface the handler needs from the NATS client.
// Using an interface keeps the handler unit-testable without a live NATS server.
type publisher interface {
	Publish(ctx context.Context, subject string, v any) error
}

type handler struct {
	nc     publisher
	tracer trace.Tracer
	log    *zap.Logger
}

// metricRequest is the JSON body for POST /api/v1/metrics.
type metricRequest struct {
	ServiceName     string            `json:"service_name" binding:"required"`
	MetricName      string            `json:"metric_name"  binding:"required"`
	Value           float64           `json:"value"`
	Labels          map[string]string `json:"labels"`
	Timestamp       time.Time         `json:"timestamp"`
	TraceID         string            `json:"trace_id,omitempty"`
	CollectorSpanID string            `json:"collector_span_id,omitempty"`
}

// logRequest is the JSON body for POST /api/v1/logs.
type logRequest struct {
	ServiceName string            `json:"service_name" binding:"required"`
	Level       string            `json:"level"        binding:"required"`
	Message     string            `json:"message"      binding:"required"`
	TraceID     string            `json:"trace_id"`
	Fields      map[string]string `json:"fields"`
	Timestamp   time.Time         `json:"timestamp"`
}

// spanRequest is the JSON body for POST /api/v1/traces.
type spanRequest struct {
	TraceID       string            `json:"trace_id"        binding:"required"`
	SpanID        string            `json:"span_id"         binding:"required"`
	ParentSpanID  string            `json:"parent_span_id"`
	ServiceName   string            `json:"service_name"    binding:"required"`
	OperationName string            `json:"operation_name"  binding:"required"`
	StartTime     time.Time         `json:"start_time"`
	EndTime       time.Time         `json:"end_time"`
	Status        string            `json:"status"`
	Attributes    map[string]string `json:"attributes"`
}

func (h *handler) receiveMetrics(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "receiveMetrics",
		trace.WithAttributes(attribute.String("transport", "http")))
	defer span.End()

	var req metricRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}

	if err := h.nc.Publish(ctx, "telemetry.metrics", req); err != nil {
		h.log.Error("publish metric", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "publish failed"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}

func (h *handler) receiveLogs(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "receiveLogs")
	defer span.End()

	var req logRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}

	if err := h.nc.Publish(ctx, "telemetry.logs", req); err != nil {
		h.log.Error("publish log", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "publish failed"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}

func (h *handler) receiveSpans(c *gin.Context) {
	_, span := h.tracer.Start(c.Request.Context(), "receiveSpans")
	defer span.End()

	var req spanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Status == "" {
		req.Status = "ok"
	}

	if err := h.nc.Publish(c.Request.Context(), "telemetry.spans", req); err != nil {
		h.log.Error("publish span", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "publish failed"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}
