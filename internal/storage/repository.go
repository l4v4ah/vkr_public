package storage

import (
	"context"
	"encoding/json"
	"time"
)

// Settings key for thresholds.
const KeyThresholds = "thresholds"

// LoadSettings reads a JSON value from the settings table by key.
func (db *DB) LoadSettings(ctx context.Context, key string, dst any) error {
	var raw []byte
	err := db.pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key,
	).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// SaveSettings upserts a JSON value into the settings table.
func (db *DB) SaveSettings(ctx context.Context, key string, src any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE
		  SET value = EXCLUDED.value, updated_at = NOW()`,
		key, data,
	)
	return err
}

// MetricPoint is the domain object written by the aggregator and read by the API.
type MetricPoint struct {
	ID          int64
	ServiceName string
	MetricName  string
	Value       float64
	Labels      map[string]string
	Timestamp   time.Time
}

// LogEntry is a structured log record stored from the telemetry stream.
type LogEntry struct {
	ID          int64
	ServiceName string
	Level       string
	Message     string
	TraceID     string
	Fields      map[string]string
	Timestamp   time.Time
}

// TraceSpan is a distributed tracing span stored by the aggregator.
type TraceSpan struct {
	ID            int64
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

// ServiceInfo summarises a monitored server.
type ServiceInfo struct {
	ServiceName string
	Host        string
	LastSeen    time.Time
	CPU         float64
	Mem         float64
	Disk        float64
}

// QueryServices returns one row per distinct service_name with latest stats.
func (db *DB) QueryServices(ctx context.Context) ([]ServiceInfo, error) {
	rows, err := db.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (service_name, metric_name)
				service_name, metric_name, value, labels, timestamp
			FROM metrics
			ORDER BY service_name, metric_name, timestamp DESC
		)
		SELECT
			service_name,
			MAX(timestamp)                                           AS last_seen,
			COALESCE(MAX(labels->>'host'), '')                       AS host,
			COALESCE(MAX(value) FILTER (WHERE metric_name='cpu_usage_percent'),  0) AS cpu,
			COALESCE(MAX(value) FILTER (WHERE metric_name='mem_usage_percent'),  0) AS mem,
			COALESCE(MAX(value) FILTER (WHERE metric_name='disk_usage_percent'), 0) AS disk
		FROM latest
		GROUP BY service_name
		ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ServiceInfo
	for rows.Next() {
		var s ServiceInfo
		if err := rows.Scan(&s.ServiceName, &s.LastSeen, &s.Host, &s.CPU, &s.Mem, &s.Disk); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// InsertMetric persists a metric data point.
func (db *DB) InsertMetric(ctx context.Context, m MetricPoint) error {
	labels, _ := json.Marshal(m.Labels)
	_, err := db.pool.Exec(ctx, `
		INSERT INTO metrics (service_name, metric_name, value, labels, timestamp)
		VALUES ($1, $2, $3, $4, $5)`,
		m.ServiceName, m.MetricName, m.Value, labels, m.Timestamp,
	)
	return err
}

// QueryMetrics returns metric points filtered by service name and time range.
func (db *DB) QueryMetrics(ctx context.Context, service string, from, to time.Time) ([]MetricPoint, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, service_name, metric_name, value, labels, timestamp
		FROM metrics
		WHERE service_name = $1 AND timestamp BETWEEN $2 AND $3
		ORDER BY timestamp DESC
		LIMIT 1000`,
		service, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MetricPoint
	for rows.Next() {
		var mp MetricPoint
		var labelsRaw []byte
		if err := rows.Scan(&mp.ID, &mp.ServiceName, &mp.MetricName, &mp.Value, &labelsRaw, &mp.Timestamp); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(labelsRaw, &mp.Labels)
		result = append(result, mp)
	}
	return result, rows.Err()
}

// InsertLog persists a structured log entry.
func (db *DB) InsertLog(ctx context.Context, l LogEntry) error {
	fields, _ := json.Marshal(l.Fields)
	_, err := db.pool.Exec(ctx, `
		INSERT INTO logs (service_name, level, message, trace_id, fields, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		l.ServiceName, l.Level, l.Message, l.TraceID, fields, l.Timestamp,
	)
	return err
}

// QueryLogs returns log entries filtered by service and optional level.
func (db *DB) QueryLogs(ctx context.Context, service, level string, limit int) ([]LogEntry, error) {
	query := `
		SELECT id, service_name, level, message, trace_id, fields, timestamp
		FROM logs WHERE service_name = $1`
	args := []any{service}

	if level != "" {
		query += ` AND level = $2 ORDER BY timestamp DESC LIMIT $3`
		args = append(args, level, limit)
	} else {
		query += ` ORDER BY timestamp DESC LIMIT $2`
		args = append(args, limit)
	}

	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []LogEntry
	for rows.Next() {
		var le LogEntry
		var fieldsRaw []byte
		if err := rows.Scan(&le.ID, &le.ServiceName, &le.Level, &le.Message, &le.TraceID, &fieldsRaw, &le.Timestamp); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(fieldsRaw, &le.Fields)
		result = append(result, le)
	}
	return result, rows.Err()
}

// InsertSpan persists a distributed tracing span.
func (db *DB) InsertSpan(ctx context.Context, s TraceSpan) error {
	attrs, _ := json.Marshal(s.Attributes)
	_, err := db.pool.Exec(ctx, `
		INSERT INTO spans (trace_id, span_id, parent_span_id, service_name, operation_name,
		                   start_time, end_time, status, attributes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.TraceID, s.SpanID, s.ParentSpanID, s.ServiceName, s.OperationName,
		s.StartTime, s.EndTime, s.Status, attrs,
	)
	return err
}

// QuerySpansByTrace returns all spans belonging to a given trace ID.
func (db *DB) QuerySpansByTrace(ctx context.Context, traceID string) ([]TraceSpan, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, trace_id, span_id, parent_span_id, service_name, operation_name,
		       start_time, end_time, status, attributes
		FROM spans WHERE trace_id = $1 ORDER BY start_time`,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TraceSpan
	for rows.Next() {
		var s TraceSpan
		var attrsRaw []byte
		if err := rows.Scan(&s.ID, &s.TraceID, &s.SpanID, &s.ParentSpanID,
			&s.ServiceName, &s.OperationName, &s.StartTime, &s.EndTime, &s.Status, &attrsRaw,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(attrsRaw, &s.Attributes)
		result = append(result, s)
	}
	return result, rows.Err()
}
