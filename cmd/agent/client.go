package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/slava-kov/monitoring-system/gen/telemetry"
)

type collectorClient struct {
	pb pb.CollectorServiceClient
}

func newCollectorClient(target string) (*collectorClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &collectorClient{pb: pb.NewCollectorServiceClient(conn)}, nil
}

// metricBatch is a batch of metrics collected during one interval.
type metricBatch []metricPoint

type metricPoint struct {
	ServiceName string
	MetricName  string
	Value       float64
	Labels      map[string]string
	Timestamp   time.Time
}

type logEntry struct {
	ServiceName string
	Level       string
	Message     string
	TraceID     string
	Fields      map[string]string
	Timestamp   time.Time
}

type spanEntry struct {
	TraceID       string
	SpanID        string
	ParentSpanID  string
	ServiceName   string
	OperationName string
	StartTime     time.Time
	EndTime       time.Time
	Status        string
	Attributes    map[string]string
}

// sendBatchTraced sends metrics with distributed trace context in gRPC metadata.
func (c *collectorClient) sendBatchTraced(ctx context.Context, batch metricBatch, traceID, parentSpanID string) error {
	md := metadata.Pairs("trace-id", traceID, "parent-span-id", parentSpanID)
	return c.sendBatch(metadata.NewOutgoingContext(ctx, md), batch)
}

func (c *collectorClient) sendBatch(ctx context.Context, batch metricBatch) error {
	pbMetrics := make([]*pb.MetricPoint, 0, len(batch))
	for _, m := range batch {
		pbMetrics = append(pbMetrics, &pb.MetricPoint{
			ServiceName: m.ServiceName,
			MetricName:  m.MetricName,
			Value:       m.Value,
			Labels:      m.Labels,
			Timestamp:   timestamppb.New(m.Timestamp),
		})
	}
	_, err := c.pb.SendMetrics(ctx, &pb.SendMetricsRequest{Metrics: pbMetrics})
	return err
}

func (c *collectorClient) sendLog(ctx context.Context, e logEntry) error {
	_, err := c.pb.SendLogs(ctx, &pb.SendLogsRequest{Logs: []*pb.LogEntry{{
		ServiceName: e.ServiceName,
		Level:       e.Level,
		Message:     e.Message,
		TraceId:     e.TraceID,
		Fields:      e.Fields,
		Timestamp:   timestamppb.New(e.Timestamp),
	}}})
	return err
}

func (c *collectorClient) sendSpan(ctx context.Context, s spanEntry) error {
	_, err := c.pb.SendSpans(ctx, &pb.SendSpansRequest{Spans: []*pb.TraceSpan{{
		TraceId:       s.TraceID,
		SpanId:        s.SpanID,
		ParentSpanId:  s.ParentSpanID,
		ServiceName:   s.ServiceName,
		OperationName: s.OperationName,
		StartTime:     timestamppb.New(s.StartTime),
		EndTime:       timestamppb.New(s.EndTime),
		Status:        s.Status,
		Attributes:    s.Attributes,
	}}})
	return err
}

// newID generates a random 32-char hex ID for trace/span identifiers.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

var _hostname string

func hostname() string {
	if _hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			h = "unknown"
		}
		_hostname = h
	}
	return _hostname
}
