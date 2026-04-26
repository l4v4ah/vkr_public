package main

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/slava-kov/monitoring-system/gen/telemetry"
)

var cryptoRandRead = cryptorand.Read

// grpcServer implements pb.CollectorServiceServer.
// It reuses the same publisher interface as the HTTP handler so all
// telemetry goes through the same NATS path regardless of transport.
type grpcServer struct {
	pb.UnimplementedCollectorServiceServer
	nc  publisher
	log *zap.Logger
}

func (s *grpcServer) SendMetrics(ctx context.Context, req *pb.SendMetricsRequest) (*pb.SendMetricsResponse, error) {
	// Extract distributed trace context from gRPC metadata.
	traceID, parentSpanID := extractTraceMeta(ctx)
	start := time.Now()

	// Generate collectorSpanID before publishing so every metric message
	// carries the correct parent reference for the aggregator span.
	collectorSpanID := ""
	if traceID != "" && len(req.Metrics) > 0 {
		collectorSpanID = newSpanID()
	}

	for _, m := range req.Metrics {
		msg := metricRequest{
			ServiceName:     m.ServiceName,
			MetricName:      m.MetricName,
			Value:           m.Value,
			Labels:          m.Labels,
			Timestamp:       protoTime(m.Timestamp),
			TraceID:         traceID,
			CollectorSpanID: collectorSpanID,
		}
		if err := s.nc.Publish(ctx, "telemetry.metrics", msg); err != nil {
			s.log.Error("grpc publish metric", zap.Error(err))
			return nil, status.Errorf(codes.Internal, "publish: %v", err)
		}
	}

	// Write collector span once per batch (only when trace context is present).
	if traceID != "" && len(req.Metrics) > 0 {
		span := spanRequest{
			TraceID:       traceID,
			SpanID:        collectorSpanID,
			ParentSpanID:  parentSpanID,
			ServiceName:   "collector",
			OperationName: "collector.receive_metrics",
			StartTime:     start,
			EndTime:       time.Now().UTC(),
			Status:        "ok",
			Attributes: map[string]string{
				"metrics_count": itoa(len(req.Metrics)),
				"service":       req.Metrics[0].ServiceName,
			},
		}
		if err := s.nc.Publish(ctx, "telemetry.spans", span); err != nil {
			s.log.Warn("grpc publish collector span", zap.Error(err))
		}
	}

	return &pb.SendMetricsResponse{Accepted: uint32(len(req.Metrics))}, nil
}

func extractTraceMeta(ctx context.Context) (traceID, parentSpanID string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ""
	}
	if v := md.Get("trace-id"); len(v) > 0 {
		traceID = v[0]
	}
	if v := md.Get("parent-span-id"); len(v) > 0 {
		parentSpanID = v[0]
	}
	return
}

func newSpanID() string {
	b := make([]byte, 8)
	_, _ = cryptoRandRead(b)
	return fmt.Sprintf("%x", b)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func (s *grpcServer) SendLogs(ctx context.Context, req *pb.SendLogsRequest) (*pb.SendLogsResponse, error) {
	for _, l := range req.Logs {
		msg := logRequest{
			ServiceName: l.ServiceName,
			Level:       l.Level,
			Message:     l.Message,
			TraceID:     l.TraceId,
			Fields:      l.Fields,
			Timestamp:   protoTime(l.Timestamp),
		}
		if err := s.nc.Publish(ctx, "telemetry.logs", msg); err != nil {
			s.log.Error("grpc publish log", zap.Error(err))
			return nil, status.Errorf(codes.Internal, "publish: %v", err)
		}
	}
	return &pb.SendLogsResponse{Accepted: uint32(len(req.Logs))}, nil
}

func (s *grpcServer) SendSpans(ctx context.Context, req *pb.SendSpansRequest) (*pb.SendSpansResponse, error) {
	for _, sp := range req.Spans {
		msg := spanRequest{
			TraceID:       sp.TraceId,
			SpanID:        sp.SpanId,
			ParentSpanID:  sp.ParentSpanId,
			ServiceName:   sp.ServiceName,
			OperationName: sp.OperationName,
			StartTime:     protoTime(sp.StartTime),
			EndTime:       protoTime(sp.EndTime),
			Status:        sp.Status,
			Attributes:    sp.Attributes,
		}
		if err := s.nc.Publish(ctx, "telemetry.spans", msg); err != nil {
			s.log.Error("grpc publish span", zap.Error(err))
			return nil, status.Errorf(codes.Internal, "publish: %v", err)
		}
	}
	return &pb.SendSpansResponse{Accepted: uint32(len(req.Spans))}, nil
}

func protoTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Now().UTC()
	}
	return ts.AsTime()
}
