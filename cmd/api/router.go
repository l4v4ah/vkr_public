package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/metrics"
	"github.com/slava-kov/monitoring-system/internal/storage"
)

func newServer(addr string, db *storage.DB, m *metrics.ServiceMetrics, tracer trace.Tracer, log *zap.Logger, apiKey string, thresh *thresholdStore) *http.Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := strconv.Itoa(c.Writer.Status())
		m.RequestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), status).Inc()
		m.RequestDuration.WithLabelValues(c.Request.Method, c.FullPath()).Observe(time.Since(start).Seconds())
	})

	h := &apiHandler{db: db, tracer: tracer, log: log}

	// Config endpoints: GET is public (agents poll without auth), PUT is protected.
	r.GET("/api/v1/config/thresholds", thresh.handleGet)

	v1 := r.Group("/api/v1")
	if apiKey != "" {
		v1.Use(apiKeyMiddleware(apiKey))
	}
	{
		v1.GET("/services", h.getServices)
		v1.GET("/metrics", h.getMetrics)
		v1.GET("/logs", h.getLogs)
		v1.GET("/traces/:trace_id", h.getTrace)
		v1.PUT("/config/thresholds", thresh.handleSet)
	}

	// SSE endpoints — auth via query param because EventSource can't set headers.
	r.GET("/api/v1/stream", func(c *gin.Context) {
		h.streamMetrics(c, apiKey)
	})
	r.GET("/api/v1/stream/services", func(c *gin.Context) {
		h.streamServices(c, apiKey)
	})

	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/metrics", gin.WrapH(m.Handler()))

	// Serve embedded frontend at /.
	r.GET("/", func(c *gin.Context) {
		data, _ := staticFiles.ReadFile("static/index.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	return &http.Server{Addr: addr, Handler: r}
}

func apiKeyMiddleware(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-API-Key") != key {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing API key"})
			return
		}
		c.Next()
	}
}
