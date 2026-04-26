package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/metrics"
	natsclient "github.com/slava-kov/monitoring-system/internal/nats"
)

func newServer(addr string, nc *natsclient.Client, m *metrics.ServiceMetrics, tracer trace.Tracer, log *zap.Logger) *http.Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Middleware: structured logging + Prometheus instrumentation + rate limit.
	r.Use(loggingMiddleware(log))
	r.Use(metricsMiddleware(m))
	// 100 req/s per IP, burst 200 — защита от случайных flood-ов
	r.Use(rateLimitMiddleware(100, 200))

	h := &handler{nc: nc, tracer: tracer, log: log}

	api := r.Group("/api/v1")
	{
		api.POST("/metrics", h.receiveMetrics)
		api.POST("/logs", h.receiveLogs)
		api.POST("/traces", h.receiveSpans)
	}

	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/metrics", gin.WrapH(m.Handler()))

	return &http.Server{Addr: addr, Handler: r}
}

func loggingMiddleware(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}

func metricsMiddleware(m *metrics.ServiceMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := strconv.Itoa(c.Writer.Status())
		m.RequestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), status).Inc()
		m.RequestDuration.WithLabelValues(c.Request.Method, c.FullPath()).Observe(time.Since(start).Seconds())
	}
}
