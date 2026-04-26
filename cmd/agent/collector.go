package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"go.uber.org/zap"
)

// spanStatus levels in ascending severity order.
const (
	statusOK    = "ok"
	statusWarn  = "warn"
	statusError = "error"
)

type systemCollector struct {
	serviceName string
	client      *collectorClient
	log         *zap.Logger
	alert       *alerter
	sessionID   string // trace ID for this agent run — groups all heartbeat spans
	thresh      *thresholds
	failCount   atomic.Int32 // consecutive sendBatch failures; triggers error log at 3

	statusMu       sync.Mutex
	intervalStatus string // worst status seen since last heartbeat span
}

func newSystemCollector(serviceName string, client *collectorClient, log *zap.Logger, thresh *thresholds, alert *alerter) *systemCollector {
	return &systemCollector{
		serviceName:    serviceName,
		client:         client,
		log:            log,
		alert:          alert,
		sessionID:      newID(),
		thresh:         thresh,
		intervalStatus: statusOK,
	}
}

// raiseStatus upgrades the interval status if the new level is more severe.
func (s *systemCollector) raiseStatus(level string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	switch {
	case level == statusError:
		s.intervalStatus = statusError
	case level == statusWarn && s.intervalStatus == statusOK:
		s.intervalStatus = statusWarn
	}
}

// popStatus returns the current interval status and resets it to ok.
func (s *systemCollector) popStatus() string {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	st := s.intervalStatus
	s.intervalStatus = statusOK
	return st
}

// Run starts one goroutine per metric group and one for logs/traces,
// blocks until ctx is cancelled.
func (s *systemCollector) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); s.streamCPU(ctx) }()
	go func() { defer wg.Done(); s.streamMemory(ctx) }()
	go func() { defer wg.Done(); s.streamDisk(ctx) }()
	go func() { defer wg.Done(); s.streamNetwork(ctx) }()
	go func() { defer wg.Done(); s.streamLogs(ctx) }()
	wg.Wait()
}

// streamLogs sends a startup log entry, then a heartbeat log + span every 60 s.
func (s *systemCollector) streamLogs(ctx context.Context) {
	start := time.Now().UTC()

	s.sendLog(ctx, "info", "agent started", s.sessionID, map[string]string{
		"host":       hostname(),
		"session_id": s.sessionID,
	})

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Use a fresh context so the shutdown log is not immediately cancelled.
			shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			s.sendLog(shutCtx, "info", "agent stopping", s.sessionID, map[string]string{"host": hostname()})
			cancel()
			return
		case t := <-ticker.C:
			now := t.UTC()
			uptime := fmt.Sprintf("%.0fs", now.Sub(start).Seconds())
			spanStatus := s.popStatus() // worst level seen in this interval
			s.sendLog(ctx, "info", "agent heartbeat", s.sessionID, map[string]string{
				"host":        hostname(),
				"uptime":      uptime,
				"span_status": spanStatus,
			})
			// Span status reflects the worst event in the interval.
			s.sendSpan(ctx, spanEntry{
				TraceID:       s.sessionID,
				SpanID:        newID(),
				ServiceName:   s.serviceName,
				OperationName: "metric_collection_interval",
				StartTime:     now.Add(-60 * time.Second),
				EndTime:       now,
				Status:        spanStatus,
				Attributes: map[string]string{
					"host":     hostname(),
					"interval": "60s",
					"uptime":   uptime,
				},
			})
		}
	}
}

// streamCPU measures CPU by blocking for 500 ms per sample — that IS the interval.
// No sleep needed: the measurement window itself throttles the loop.
func (s *systemCollector) streamCPU(ctx context.Context) {
	base := map[string]string{"host": hostname()}
	counts, _ := cpu.CountsWithContext(ctx, false)
	var warnedHigh bool

	for {
		if ctx.Err() != nil {
			return
		}
		percents, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, false)
		if err != nil || len(percents) == 0 {
			continue
		}
		now := time.Now().UTC()
		s.send(ctx, metricBatch{
			s.pt("cpu_usage_percent", percents[0], base, now),
			s.pt("cpu_count", float64(counts), base, now),
		})
		if percents[0] >= s.thresh.CPU() && !warnedHigh {
			warnedHigh = true
			s.sendLog(ctx, "warn", fmt.Sprintf("high CPU usage: %.1f%%", percents[0]), s.sessionID,
				map[string]string{"host": hostname(), "value": fmt.Sprintf("%.1f", percents[0])})
		} else if percents[0] < s.thresh.CPU() {
			warnedHigh = false
		}
	}
}

// streamMemory polls every 1 s — memory doesn't need sub-second granularity.
func (s *systemCollector) streamMemory(ctx context.Context) {
	var warnedHigh bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
		v, err := mem.VirtualMemoryWithContext(ctx)
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		base := map[string]string{"host": hostname()}
		s.send(ctx, metricBatch{
			s.pt("mem_total_bytes", float64(v.Total), base, now),
			s.pt("mem_used_bytes", float64(v.Used), base, now),
			s.pt("mem_available_bytes", float64(v.Available), base, now),
			s.pt("mem_usage_percent", v.UsedPercent, base, now),
		})
		if v.UsedPercent >= s.thresh.Mem() && !warnedHigh {
			warnedHigh = true
			s.sendLog(ctx, "warn", fmt.Sprintf("high memory usage: %.1f%%", v.UsedPercent), s.sessionID,
				map[string]string{"host": hostname(), "value": fmt.Sprintf("%.1f", v.UsedPercent)})
		} else if v.UsedPercent < s.thresh.Mem() {
			warnedHigh = false
		}
	}
}

// streamDisk polls every 2 s — disk usage changes slowly.
func (s *systemCollector) streamDisk(ctx context.Context) {
	warnedMounts := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		partitions, err := disk.PartitionsWithContext(ctx, false)
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		var batch metricBatch
		for _, p := range partitions {
			usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
			if err != nil {
				continue
			}
			labels := map[string]string{
				"host": hostname(), "mountpoint": p.Mountpoint, "device": p.Device,
			}
			batch = append(batch,
				s.pt("disk_total_bytes", float64(usage.Total), labels, now),
				s.pt("disk_used_bytes", float64(usage.Used), labels, now),
				s.pt("disk_free_bytes", float64(usage.Free), labels, now),
				s.pt("disk_usage_percent", usage.UsedPercent, labels, now),
			)
			if usage.UsedPercent >= s.thresh.Disk() && !warnedMounts[p.Mountpoint] {
				warnedMounts[p.Mountpoint] = true
				s.sendLog(ctx, "warn", fmt.Sprintf("low disk space on %s: %.1f%% used", p.Mountpoint, usage.UsedPercent),
					s.sessionID, map[string]string{
						"host":       hostname(),
						"mountpoint": p.Mountpoint,
						"value":      fmt.Sprintf("%.1f", usage.UsedPercent),
					})
			} else if usage.UsedPercent < s.thresh.Disk() {
				warnedMounts[p.Mountpoint] = false
			}
		}
		if len(batch) > 0 {
			s.send(ctx, batch)
		}
	}
}

// streamNetwork calculates per-second rates by taking two snapshots 500 ms apart.
// The delta divided by elapsed gives bytes/s with high resolution.
func (s *systemCollector) streamNetwork(ctx context.Context) {
	prev, prevTime := netSnapshot(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}

		cur, curTime := netSnapshot(ctx)
		if cur == nil {
			continue
		}
		elapsed := curTime.Sub(prevTime).Seconds()
		if elapsed <= 0 {
			prev, prevTime = cur, curTime
			continue
		}

		now := time.Now().UTC()
		var batch metricBatch
		for name, c := range cur {
			p, ok := prev[name]
			if !ok {
				continue
			}
			labels := map[string]string{"host": hostname(), "interface": name}
			batch = append(batch,
				s.pt("net_bytes_sent_total", float64(c.BytesSent), labels, now),
				s.pt("net_bytes_recv_total", float64(c.BytesRecv), labels, now),
				s.pt("net_bytes_sent_per_sec", float64(c.BytesSent-p.BytesSent)/elapsed, labels, now),
				s.pt("net_bytes_recv_per_sec", float64(c.BytesRecv-p.BytesRecv)/elapsed, labels, now),
			)
		}
		if len(batch) > 0 {
			s.send(ctx, batch)
		}
		prev, prevTime = cur, curTime
	}
}

func netSnapshot(ctx context.Context) (map[string]net.IOCountersStat, time.Time) {
	stats, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, time.Time{}
	}
	m := make(map[string]net.IOCountersStat, len(stats))
	for _, s := range stats {
		if s.Name != "lo" {
			m[s.Name] = s
		}
	}
	return m, time.Now().UTC()
}

func (s *systemCollector) pt(name string, value float64, labels map[string]string, t time.Time) metricPoint {
	return metricPoint{ServiceName: s.serviceName, MetricName: name, Value: value, Labels: labels, Timestamp: t}
}

func (s *systemCollector) send(ctx context.Context, batch metricBatch) {
	if err := s.client.sendBatchTraced(ctx, batch, s.sessionID, newID()); err != nil {
		fc := s.failCount.Add(1)
		s.log.Warn("send batch", zap.String("metric", batch[0].MetricName), zap.Error(err))
		level := "warn"
		msg := "failed to send metric batch: " + err.Error()
		if fc == 3 {
			level = "error"
			msg = "collector unreachable, metric loss in progress"
		} else if fc > 3 {
			return // already logged the error, don't spam
		}
		s.sendLog(ctx, level, msg, s.sessionID, map[string]string{
			"metric":     batch[0].MetricName,
			"host":       hostname(),
			"fail_count": fmt.Sprintf("%d", fc),
		})
	} else if fc := s.failCount.Load(); fc >= 3 {
		s.sendLog(ctx, "info", fmt.Sprintf("connectivity restored after %d failures", fc),
			s.sessionID, map[string]string{"host": hostname()})
		s.failCount.Store(0)
	} else {
		s.failCount.Store(0)
	}
}

func (s *systemCollector) sendLog(ctx context.Context, level, message, traceID string, fields map[string]string) {
	if level == statusWarn || level == statusError {
		s.raiseStatus(level)
		s.alert.send(ctx, level, message)
	}
	if err := s.client.sendLog(ctx, logEntry{
		ServiceName: s.serviceName,
		Level:       level,
		Message:     message,
		TraceID:     traceID,
		Fields:      fields,
		Timestamp:   time.Now().UTC(),
	}); err != nil {
		s.log.Warn("send log", zap.String("level", level), zap.Error(err))
	}
}

func (s *systemCollector) sendSpan(ctx context.Context, sp spanEntry) {
	if err := s.client.sendSpan(ctx, sp); err != nil {
		s.log.Warn("send span", zap.String("op", sp.OperationName), zap.Error(err))
	}
}
