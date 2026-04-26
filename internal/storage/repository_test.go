package storage_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/slava-kov/monitoring-system/internal/storage"
)

func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

func startPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "testdb",
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(30 * time.Second),
	}

	pgc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	host, err := pgc.Host(ctx)
	require.NoError(t, err)
	port, err := pgc.MappedPort(ctx, "5432")
	require.NoError(t, err)

	return fmt.Sprintf("postgres://test:test@%s:%s/testdb?sslmode=disable", host, port.Port())
}

// openDB создаёт подключение с миграциями — вспомогательная функция для всех тестов.
func openDB(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	db, err := storage.Connect(ctx, dsn, migrationsDir())
	require.NoError(t, err)
	t.Cleanup(db.Close)
	return db
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func TestMetricRepository(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC().Truncate(time.Second)
	mp := storage.MetricPoint{
		ServiceName: "payment-service",
		MetricName:  "request_duration_seconds",
		Value:       0.123,
		Labels:      map[string]string{"method": "POST", "status": "200"},
		Timestamp:   now,
	}

	require.NoError(t, db.InsertMetric(ctx, mp))

	results, err := db.QueryMetrics(ctx, "payment-service", now.Add(-time.Minute), now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, mp.ServiceName, results[0].ServiceName)
	assert.Equal(t, mp.MetricName, results[0].MetricName)
	assert.InDelta(t, mp.Value, results[0].Value, 1e-9)
	assert.Equal(t, mp.Labels, results[0].Labels)
}

func TestMetricRepository_EmptyResult(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	results, err := db.QueryMetrics(ctx, "nonexistent-service",
		time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestMetricRepository_TimeRange(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
			ServiceName: "svc",
			MetricName:  "cpu_usage_percent",
			Value:       float64(i * 10),
			Labels:      map[string]string{},
			Timestamp:   base.Add(time.Duration(i) * time.Minute),
		}))
	}

	// должны попасть только точки с индексом 0 и 1 (не 2, он за пределом to)
	results, err := db.QueryMetrics(ctx, "svc",
		base.Add(-time.Second),
		base.Add(90*time.Second))
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestMetricRepository_MultipleServices(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	for _, svc := range []string{"svc-a", "svc-b", "svc-c"} {
		require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
			ServiceName: svc, MetricName: "cpu_usage_percent",
			Value: 1.0, Labels: map[string]string{}, Timestamp: now,
		}))
	}

	res, err := db.QueryMetrics(ctx, "svc-b", now.Add(-time.Second), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "svc-b", res[0].ServiceName)
}

// ── Logs ──────────────────────────────────────────────────────────────────────

func TestLogRepository(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	le := storage.LogEntry{
		ServiceName: "auth-service",
		Level:       "error",
		Message:     "invalid token",
		TraceID:     "trace-abc",
		Fields:      map[string]string{"user_id": "42"},
		Timestamp:   time.Now().UTC(),
	}
	require.NoError(t, db.InsertLog(ctx, le))

	results, err := db.QueryLogs(ctx, "auth-service", "error", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, le.Message, results[0].Message)
	assert.Equal(t, le.TraceID, results[0].TraceID)
	assert.Equal(t, le.Fields, results[0].Fields)
}

func TestLogRepository_LevelFilter(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	for _, lvl := range []string{"info", "warn", "error"} {
		require.NoError(t, db.InsertLog(ctx, storage.LogEntry{
			ServiceName: "svc", Level: lvl,
			Message: lvl + " message", TraceID: "", Fields: map[string]string{},
			Timestamp: now,
		}))
	}

	warn, err := db.QueryLogs(ctx, "svc", "warn", 100)
	require.NoError(t, err)
	require.Len(t, warn, 1)
	assert.Equal(t, "warn", warn[0].Level)

	all, err := db.QueryLogs(ctx, "svc", "", 100)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestLogRepository_Limit(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		require.NoError(t, db.InsertLog(ctx, storage.LogEntry{
			ServiceName: "svc", Level: "info",
			Message: fmt.Sprintf("msg %d", i), TraceID: "",
			Fields: map[string]string{}, Timestamp: now.Add(time.Duration(i) * time.Millisecond),
		}))
	}

	res, err := db.QueryLogs(ctx, "svc", "", 3)
	require.NoError(t, err)
	assert.Len(t, res, 3)
}

// ── Spans ─────────────────────────────────────────────────────────────────────

func TestSpanRepository(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	s := storage.TraceSpan{
		TraceID:       "trace-xyz",
		SpanID:        "span-001",
		ServiceName:   "order-service",
		OperationName: "create_order",
		StartTime:     now,
		EndTime:       now.Add(50 * time.Millisecond),
		Status:        "ok",
		Attributes:    map[string]string{"db.system": "postgresql"},
	}
	require.NoError(t, db.InsertSpan(ctx, s))

	spans, err := db.QuerySpansByTrace(ctx, "trace-xyz")
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Equal(t, s.OperationName, spans[0].OperationName)
	assert.Equal(t, s.Attributes, spans[0].Attributes)
}

func TestSpanRepository_ParentChild(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	parent := storage.TraceSpan{
		TraceID: "trace-1", SpanID: "parent-span",
		ServiceName: "agent", OperationName: "metric_collection_interval",
		StartTime: now, EndTime: now.Add(60 * time.Second),
		Status: "ok", Attributes: map[string]string{},
	}
	child := storage.TraceSpan{
		TraceID: "trace-1", SpanID: "child-span", ParentSpanID: "parent-span",
		ServiceName: "collector", OperationName: "collector.receive_metrics",
		StartTime: now.Add(time.Millisecond), EndTime: now.Add(10 * time.Millisecond),
		Status: "ok", Attributes: map[string]string{"metrics_count": "4"},
	}

	require.NoError(t, db.InsertSpan(ctx, parent))
	require.NoError(t, db.InsertSpan(ctx, child))

	spans, err := db.QuerySpansByTrace(ctx, "trace-1")
	require.NoError(t, err)
	require.Len(t, spans, 2)

	// результаты отсортированы по start_time — parent идёт первым
	assert.Equal(t, "parent-span", spans[0].SpanID)
	assert.Equal(t, "child-span", spans[1].SpanID)
	assert.Equal(t, "parent-span", spans[1].ParentSpanID)
}

func TestSpanRepository_DuplicateSpanID(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	s := storage.TraceSpan{
		TraceID: "trace-dup", SpanID: "same-span-id",
		ServiceName: "svc", OperationName: "op",
		StartTime: now, EndTime: now.Add(time.Millisecond),
		Status: "ok", Attributes: map[string]string{},
	}
	require.NoError(t, db.InsertSpan(ctx, s))

	// второй INSERT с тем же span_id должен нарушить UNIQUE ограничение
	err := db.InsertSpan(ctx, s)
	assert.Error(t, err, "должна быть ошибка дублирования span_id")
}

func TestSpanRepository_EmptyTrace(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	spans, err := db.QuerySpansByTrace(ctx, "no-such-trace")
	require.NoError(t, err)
	assert.Empty(t, spans)
}

func TestSpanRepository_MultipleTraces(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		require.NoError(t, db.InsertSpan(ctx, storage.TraceSpan{
			TraceID:     fmt.Sprintf("trace-%d", i),
			SpanID:      fmt.Sprintf("span-%d", i),
			ServiceName: "svc", OperationName: "op",
			StartTime: now, EndTime: now.Add(time.Millisecond),
			Status: "ok", Attributes: map[string]string{},
		}))
	}

	// каждый трейс должен возвращать ровно один спан
	for i := 0; i < 3; i++ {
		spans, err := db.QuerySpansByTrace(ctx, fmt.Sprintf("trace-%d", i))
		require.NoError(t, err)
		assert.Len(t, spans, 1, "trace-%d должен содержать 1 спан", i)
	}
}

// ── QueryServices ─────────────────────────────────────────────────────────────

func TestQueryServices(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	metrics := []storage.MetricPoint{
		{ServiceName: "host-agent", MetricName: "cpu_usage_percent", Value: 42.5,
			Labels: map[string]string{"host": "server-01"}, Timestamp: now},
		{ServiceName: "host-agent", MetricName: "mem_usage_percent", Value: 61.0,
			Labels: map[string]string{"host": "server-01"}, Timestamp: now},
		{ServiceName: "host-agent", MetricName: "disk_usage_percent", Value: 55.0,
			Labels: map[string]string{"host": "server-01"}, Timestamp: now},
	}
	for _, m := range metrics {
		require.NoError(t, db.InsertMetric(ctx, m))
	}

	services, err := db.QueryServices(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)

	s := services[0]
	assert.Equal(t, "host-agent", s.ServiceName)
	assert.Equal(t, "server-01", s.Host)
	assert.InDelta(t, 42.5, s.CPU, 1e-6)
	assert.InDelta(t, 61.0, s.Mem, 1e-6)
	assert.InDelta(t, 55.0, s.Disk, 1e-6)
}

func TestQueryServices_MultipleServices(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	now := time.Now().UTC()
	for _, svc := range []string{"svc-a", "svc-b"} {
		require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
			ServiceName: svc, MetricName: "cpu_usage_percent",
			Value: 10.0, Labels: map[string]string{"host": svc + "-host"}, Timestamp: now,
		}))
	}

	services, err := db.QueryServices(ctx)
	require.NoError(t, err)
	assert.Len(t, services, 2)
}

func TestQueryServices_LastSeenUpdated(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	base := time.Now().UTC().Truncate(time.Second)
	older := base.Add(-30 * time.Second)

	require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
		ServiceName: "svc", MetricName: "cpu_usage_percent",
		Value: 1.0, Labels: map[string]string{}, Timestamp: older,
	}))
	require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
		ServiceName: "svc", MetricName: "cpu_usage_percent",
		Value: 99.0, Labels: map[string]string{}, Timestamp: base,
	}))

	services, err := db.QueryServices(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)
	// last_seen должен быть равен более новой метке времени
	assert.WithinDuration(t, base, services[0].LastSeen, time.Second)
}

// ── Retention ─────────────────────────────────────────────────────────────────

func TestRetentionCleanup(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	db, err := storage.Connect(ctx, dsn, migrationsDir())
	require.NoError(t, err)
	t.Cleanup(db.Close)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour) // 40 дней назад
	now := time.Now().UTC()

	// вставляем старую и новую метрику
	require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
		ServiceName: "svc", MetricName: "cpu_usage_percent",
		Value: 1.0, Labels: map[string]string{}, Timestamp: old,
	}))
	require.NoError(t, db.InsertMetric(ctx, storage.MetricPoint{
		ServiceName: "svc", MetricName: "cpu_usage_percent",
		Value: 2.0, Labels: map[string]string{}, Timestamp: now,
	}))

	// вставляем старый лог
	require.NoError(t, db.InsertLog(ctx, storage.LogEntry{
		ServiceName: "svc", Level: "info", Message: "old", TraceID: "",
		Fields: map[string]string{}, Timestamp: old,
	}))

	// вставляем старый спан
	require.NoError(t, db.InsertSpan(ctx, storage.TraceSpan{
		TraceID: "t1", SpanID: "s1", ServiceName: "svc", OperationName: "op",
		StartTime: old, EndTime: old.Add(time.Millisecond),
		Status: "ok", Attributes: map[string]string{},
	}))

	// вызываем функцию очистки данных старше 30 дней
	row := db.Pool().QueryRow(ctx, `SELECT * FROM cleanup_old_telemetry(30)`)
	var delMetrics, delLogs, delSpans int64
	require.NoError(t, row.Scan(&delMetrics, &delLogs, &delSpans))

	assert.Equal(t, int64(1), delMetrics, "должна удалиться 1 старая метрика")
	assert.Equal(t, int64(1), delLogs, "должен удалиться 1 старый лог")
	assert.Equal(t, int64(1), delSpans, "должен удалиться 1 старый спан")

	// новая метрика осталась
	res, err := db.QueryMetrics(ctx, "svc", now.Add(-time.Second), now.Add(time.Second))
	require.NoError(t, err)
	assert.Len(t, res, 1)
}
