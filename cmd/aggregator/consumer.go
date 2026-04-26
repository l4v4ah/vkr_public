package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/metrics"
	natsclient "github.com/slava-kov/monitoring-system/internal/nats"
	"github.com/slava-kov/monitoring-system/internal/storage"
)

func newAggSpanID() string {
	b := make([]byte, 8)
	_, _ = cryptorand.Read(b)
	return fmt.Sprintf("%x", b)
}

// consumer subscribes to all NATS JetStream telemetry subjects and writes
// decoded records to PostgreSQL. Each signal type runs in its own goroutine
// so that a spike in one type does not block delivery of the others.
type consumer struct {
	db  *storage.DB
	nc  *natsclient.Client
	m   *metrics.ServiceMetrics
	log *zap.Logger
}

func newConsumer(db *storage.DB, nc *natsclient.Client, m *metrics.ServiceMetrics, log *zap.Logger) *consumer {
	return &consumer{db: db, nc: nc, m: m, log: log}
}

func (c *consumer) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(3)
	go func() { defer wg.Done(); c.consumeMetrics(ctx) }()
	go func() { defer wg.Done(); c.consumeLogs(ctx) }()
	go func() { defer wg.Done(); c.consumeSpans(ctx) }()

	wg.Wait()
}

// metricMsg mirrors the JSON published by the collector.
type metricMsg struct {
	ServiceName     string            `json:"service_name"`
	MetricName      string            `json:"metric_name"`
	Value           float64           `json:"value"`
	Labels          map[string]string `json:"labels"`
	Timestamp       time.Time         `json:"timestamp"`
	TraceID         string            `json:"trace_id,omitempty"`
	CollectorSpanID string            `json:"collector_span_id,omitempty"`
}

type logMsg struct {
	ServiceName string            `json:"service_name"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	TraceID     string            `json:"trace_id"`
	Fields      map[string]string `json:"fields"`
	Timestamp   time.Time         `json:"timestamp"`
}

type spanMsg struct {
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id"`
	ServiceName   string            `json:"service_name"`
	OperationName string            `json:"operation_name"`
	StartTime     time.Time         `json:"start_time"`
	EndTime       time.Time         `json:"end_time"`
	Status        string            `json:"status"`
	Attributes    map[string]string `json:"attributes"`
}

func (c *consumer) consumeMetrics(ctx context.Context) {
	if err := c.nc.Subscribe(ctx, "agg-metrics", natsclient.SubjectMetrics, func(data []byte) error {
		var msg metricMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.m.ErrorsTotal.WithLabelValues("decode_metric").Inc()
			return nil // bad message: ack and skip
		}
		insertStart := time.Now().UTC()
		err := c.db.InsertMetric(ctx, storage.MetricPoint{
			ServiceName: msg.ServiceName,
			MetricName:  msg.MetricName,
			Value:       msg.Value,
			Labels:      msg.Labels,
			Timestamp:   msg.Timestamp,
		})
		if err != nil {
			c.m.ErrorsTotal.WithLabelValues("insert_metric").Inc()
			c.log.Error("insert metric", zap.Error(err))
			return err // nack → retry
		}
		c.m.RequestsTotal.WithLabelValues("nats", "metrics", "200").Inc()
		if msg.TraceID != "" {
			_ = c.db.InsertSpan(ctx, storage.TraceSpan{
				TraceID:       msg.TraceID,
				SpanID:        newAggSpanID(),
				ParentSpanID:  msg.CollectorSpanID,
				ServiceName:   "aggregator",
				OperationName: "aggregator.insert_metric",
				StartTime:     insertStart,
				EndTime:       time.Now().UTC(),
				Status:        "ok",
				Attributes: map[string]string{
					"metric_name":  msg.MetricName,
					"service_name": msg.ServiceName,
				},
			})
		}
		return nil
	}); err != nil && ctx.Err() == nil {
		c.log.Error("metrics consumer exited", zap.Error(err))
	}
}

func (c *consumer) consumeLogs(ctx context.Context) {
	if err := c.nc.Subscribe(ctx, "agg-logs", natsclient.SubjectLogs, func(data []byte) error {
		var msg logMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil
		}
		err := c.db.InsertLog(ctx, storage.LogEntry{
			ServiceName: msg.ServiceName,
			Level:       msg.Level,
			Message:     msg.Message,
			TraceID:     msg.TraceID,
			Fields:      msg.Fields,
			Timestamp:   msg.Timestamp,
		})
		if err != nil {
			c.m.ErrorsTotal.WithLabelValues("insert_log").Inc()
			c.log.Error("insert log", zap.Error(err))
			return err
		}
		return nil
	}); err != nil && ctx.Err() == nil {
		c.log.Error("logs consumer exited", zap.Error(err))
	}
}

func (c *consumer) consumeSpans(ctx context.Context) {
	if err := c.nc.Subscribe(ctx, "agg-spans", natsclient.SubjectSpans, func(data []byte) error {
		var msg spanMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil
		}
		err := c.db.InsertSpan(ctx, storage.TraceSpan{
			TraceID:       msg.TraceID,
			SpanID:        msg.SpanID,
			ParentSpanID:  msg.ParentSpanID,
			ServiceName:   msg.ServiceName,
			OperationName: msg.OperationName,
			StartTime:     msg.StartTime,
			EndTime:       msg.EndTime,
			Status:        msg.Status,
			Attributes:    msg.Attributes,
		})
		if err != nil {
			c.m.ErrorsTotal.WithLabelValues("insert_span").Inc()
			c.log.Error("insert span", zap.Error(err))
			return err
		}
		return nil
	}); err != nil && ctx.Err() == nil {
		c.log.Error("spans consumer exited", zap.Error(err))
	}
}
