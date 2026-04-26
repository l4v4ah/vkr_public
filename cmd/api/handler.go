package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/storage"
)

type apiHandler struct {
	db     *storage.DB
	tracer trace.Tracer
	log    *zap.Logger
}

// getServices handles GET /api/v1/services — returns all known servers with latest stats.
func (h *apiHandler) getServices(c *gin.Context) {
	services, err := h.db.QueryServices(c.Request.Context())
	if err != nil {
		h.log.Error("query services", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": services})
}

// getMetrics handles GET /api/v1/metrics?service=X&from=RFC3339&to=RFC3339
func (h *apiHandler) getMetrics(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "getMetrics",
		trace.WithAttributes(attribute.String("query.service", c.Query("service"))))
	defer span.End()

	service := c.Query("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service query parameter is required"})
		return
	}

	from, to := parseTimeRange(c)

	results, err := h.db.QueryMetrics(ctx, service, from, to)
	if err != nil {
		h.log.Error("query metrics", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"service": service, "count": len(results), "data": results})
}

// getLogs handles GET /api/v1/logs?service=X&level=error&limit=100
func (h *apiHandler) getLogs(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "getLogs")
	defer span.End()

	service := c.Query("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service query parameter is required"})
		return
	}

	level := c.Query("level")
	limit := 100
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	results, err := h.db.QueryLogs(ctx, service, level, limit)
	if err != nil {
		h.log.Error("query logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"service": service, "count": len(results), "data": results})
}

// getTrace handles GET /api/v1/traces/:trace_id
func (h *apiHandler) getTrace(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "getTrace",
		trace.WithAttributes(attribute.String("trace_id", c.Param("trace_id"))))
	defer span.End()

	traceID := c.Param("trace_id")
	spans, err := h.db.QuerySpansByTrace(ctx, traceID)
	if err != nil {
		h.log.Error("query spans", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	if len(spans) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "trace not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"trace_id": traceID, "span_count": len(spans), "spans": spans})
}

func parseTimeRange(c *gin.Context) (time.Time, time.Time) {
	to := time.Now().UTC()
	from := to.Add(-1 * time.Hour)

	if f := c.Query("from"); f != "" {
		if t, err := time.Parse(time.RFC3339, f); err == nil {
			from = t
		}
	}
	if t := c.Query("to"); t != "" {
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			to = parsed
		}
	}
	return from, to
}
