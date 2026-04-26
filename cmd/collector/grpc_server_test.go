package main

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	pb "github.com/slava-kov/monitoring-system/gen/telemetry"
)

// ── extractTraceMeta ──────────────────────────────────────────────────────────

func TestExtractTraceMeta_WithBothKeys(t *testing.T) {
	md := metadata.Pairs(
		"trace-id", "abc123",
		"parent-span-id", "def456",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	traceID, parentSpanID := extractTraceMeta(ctx)
	assert.Equal(t, "abc123", traceID)
	assert.Equal(t, "def456", parentSpanID)
}

func TestExtractTraceMeta_OnlyTraceID(t *testing.T) {
	md := metadata.Pairs("trace-id", "only-trace")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	traceID, parentSpanID := extractTraceMeta(ctx)
	assert.Equal(t, "only-trace", traceID)
	assert.Equal(t, "", parentSpanID)
}

func TestExtractTraceMeta_NoMetadata(t *testing.T) {
	traceID, parentSpanID := extractTraceMeta(context.Background())
	assert.Equal(t, "", traceID)
	assert.Equal(t, "", parentSpanID)
}

func TestExtractTraceMeta_EmptyMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	traceID, parentSpanID := extractTraceMeta(ctx)
	assert.Equal(t, "", traceID)
	assert.Equal(t, "", parentSpanID)
}

// ── newSpanID ─────────────────────────────────────────────────────────────────

func TestNewSpanID_Format(t *testing.T) {
	id := newSpanID()
	assert.Len(t, id, 16, "span ID должен быть 16 hex-символов (8 байт)")
	_, err := hex.DecodeString(id)
	assert.NoError(t, err, "span ID должен быть валидным hex")
}

func TestNewSpanID_Unique(t *testing.T) {
	ids := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := newSpanID()
		_, exists := ids[id]
		assert.False(t, exists, "сгенерированный span ID должен быть уникальным")
		ids[id] = struct{}{}
	}
}

// ── gRPC SendMetrics ──────────────────────────────────────────────────────────

func TestGRPCSendMetrics_NoTrace(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	req := &pb.SendMetricsRequest{
		Metrics: []*pb.MetricPoint{
			{ServiceName: "svc", MetricName: "cpu_usage_percent", Value: 42.0},
		},
	}
	resp, err := srv.SendMetrics(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), resp.Accepted)

	// без trace-id collector-спан не публикуется → только 1 subject (metrics)
	assert.Len(t, pub.subjects, 1)
	assert.Equal(t, "telemetry.metrics", pub.subjects[0])
}

func TestGRPCSendMetrics_WithTrace(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	md := metadata.Pairs("trace-id", "t1", "parent-span-id", "p1")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	req := &pb.SendMetricsRequest{
		Metrics: []*pb.MetricPoint{
			{ServiceName: "svc", MetricName: "cpu_usage_percent", Value: 42.0},
		},
	}
	resp, err := srv.SendMetrics(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), resp.Accepted)

	// с trace-id: 1 метрика + 1 collector-спан
	assert.Len(t, pub.subjects, 2)
	assert.Contains(t, pub.subjects, "telemetry.metrics")
	assert.Contains(t, pub.subjects, "telemetry.spans")
}

func TestGRPCSendMetrics_EmptyBatch(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	resp, err := srv.SendMetrics(context.Background(), &pb.SendMetricsRequest{})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), resp.Accepted)
	assert.Empty(t, pub.subjects)
}

func TestGRPCSendMetrics_MultipleBatch(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	req := &pb.SendMetricsRequest{
		Metrics: []*pb.MetricPoint{
			{ServiceName: "svc", MetricName: "cpu_usage_percent", Value: 10.0},
			{ServiceName: "svc", MetricName: "mem_usage_percent", Value: 20.0},
			{ServiceName: "svc", MetricName: "disk_usage_percent", Value: 30.0},
		},
	}
	resp, err := srv.SendMetrics(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), resp.Accepted)
	assert.Len(t, pub.subjects, 3)
}

// ── gRPC SendLogs ─────────────────────────────────────────────────────────────

func TestGRPCSendLogs(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	req := &pb.SendLogsRequest{
		Logs: []*pb.LogEntry{
			{ServiceName: "svc", Level: "warn", Message: "high cpu", TraceId: "t1"},
		},
	}
	resp, err := srv.SendLogs(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), resp.Accepted)
	assert.Equal(t, "telemetry.logs", pub.subjects[0])
}

func TestGRPCSendLogs_MultipleLogs(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	req := &pb.SendLogsRequest{
		Logs: []*pb.LogEntry{
			{ServiceName: "svc", Level: "info", Message: "a", TraceId: "t1"},
			{ServiceName: "svc", Level: "error", Message: "b", TraceId: "t1"},
		},
	}
	resp, err := srv.SendLogs(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), resp.Accepted)
	assert.Len(t, pub.subjects, 2)
}

// ── gRPC SendSpans ────────────────────────────────────────────────────────────

func TestGRPCSendSpans(t *testing.T) {
	pub := &fakePublisher{}
	srv := &grpcServer{nc: pub, log: zap.NewNop()}

	req := &pb.SendSpansRequest{
		Spans: []*pb.TraceSpan{
			{TraceId: "t1", SpanId: "s1", ServiceName: "svc", OperationName: "op", Status: "ok"},
		},
	}
	resp, err := srv.SendSpans(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), resp.Accepted)
	assert.Equal(t, "telemetry.spans", pub.subjects[0])
}
