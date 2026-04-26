package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// streamMetrics handles GET /api/v1/stream?service=X[&api_key=...]
// It pushes new metric batches via Server-Sent Events every 3 seconds.
// EventSource doesn't support custom headers, so the API key is accepted
// as a query parameter for this endpoint.
func (h *apiHandler) streamMetrics(c *gin.Context, apiKey string) {
	service := c.Query("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service required"})
		return
	}
	if apiKey != "" && c.Query("api_key") != apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	ctx := c.Request.Context()

	// send initial snapshot — last 2 minutes so charts have data immediately
	since := time.Now().UTC().Add(-2 * time.Minute)
	h.pushMetrics(c, service, since, "snapshot")
	since = time.Now().UTC()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			h.pushMetrics(c, service, since, "update")
			since = now
		}
	}
}

// streamServices handles GET /api/v1/stream/services — pushes server list every 5s.
func (h *apiHandler) streamServices(c *gin.Context, apiKey string) {
	if apiKey != "" && c.Query("api_key") != apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ctx := c.Request.Context()

	push := func() {
		services, err := h.db.QueryServices(ctx)
		if err != nil {
			return
		}
		data, _ := json.Marshal(services)
		fmt.Fprintf(c.Writer, "event: services\ndata: %s\n\n", data)
		c.Writer.Flush()
	}

	push()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			push()
		}
	}
}

func (h *apiHandler) pushMetrics(c *gin.Context, service string, since time.Time, event string) {
	rows, err := h.db.QueryMetrics(c.Request.Context(), service, since, time.Now().UTC())
	if err != nil {
		h.log.Warn("sse query metrics", zap.Error(err))
		return
	}
	if len(rows) == 0 {
		// send heartbeat so client knows connection is alive
		fmt.Fprintf(c.Writer, ": heartbeat\n\n")
		c.Writer.Flush()
		return
	}

	data, _ := json.Marshal(rows)
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	c.Writer.Flush()
}
