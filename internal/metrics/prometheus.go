// Package metrics registers and exposes Prometheus metrics for microservices.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServiceMetrics holds Prometheus counters and histograms for a microservice.
type ServiceMetrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ErrorsTotal     *prometheus.CounterVec
	registry        *prometheus.Registry
}

// New registers service-specific metrics and standard Go runtime metrics.
func New(service string) *ServiceMetrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &ServiceMetrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "monitoring",
			Subsystem: service,
			Name:      "requests_total",
			Help:      "Total number of requests handled by the service.",
		}, []string{"method", "path", "status"}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "monitoring",
			Subsystem: service,
			Name:      "request_duration_seconds",
			Help:      "Histogram of request latencies.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "monitoring",
			Subsystem: service,
			Name:      "errors_total",
			Help:      "Total number of errors encountered.",
		}, []string{"type"}),

		registry: reg,
	}

	reg.MustRegister(m.RequestsTotal, m.RequestDuration, m.ErrorsTotal)
	return m
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func (m *ServiceMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}
