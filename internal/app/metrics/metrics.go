// Package metrics provides Prometheus metrics for Obsidian WAF.
// This implementation addresses the audit finding for proper observability
// and provides comprehensive WAF performance monitoring.
package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "obsidian"
	subsystem = "waf"
)

var (
	// Singleton metrics instance
	instance *Metrics
	once     sync.Once
)

// Metrics holds all Prometheus metrics
type Metrics struct {
	// Request metrics
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	RequestSize     *prometheus.HistogramVec
	ResponseSize    *prometheus.HistogramVec
	ActiveRequests  prometheus.Gauge

	// WAF metrics
	WAFRulesEvaluated  *prometheus.CounterVec
	WAFRuleMatches     *prometheus.CounterVec
	WAFBlockedRequests *prometheus.CounterVec
	WAFEvalDuration    *prometheus.HistogramVec

	// Rate limiting metrics
	RateLimitBlocks  *prometheus.CounterVec
	RateLimitAllowed *prometheus.CounterVec
	ActiveVisitors   prometheus.Gauge

	// Authentication metrics
	AuthAttempts   *prometheus.CounterVec
	AuthFailures   *prometheus.CounterVec
	ActiveSessions prometheus.Gauge

	// Threat Intelligence metrics
	ThreatBlocks   *prometheus.CounterVec
	ThreatListSize prometheus.Gauge

	// System metrics
	Uptime      prometheus.Counter
	GoRoutines  prometheus.Gauge
	MemoryAlloc prometheus.Gauge
	MemoryTotal prometheus.Gauge

	// Database metrics
	DBConnections   prometheus.Gauge
	DBQueryDuration *prometheus.HistogramVec
	DBErrors        *prometheus.CounterVec

	registry *prometheus.Registry
}

// New creates a new Metrics instance
func New() *Metrics {
	once.Do(func() {
		instance = &Metrics{
			registry: prometheus.NewRegistry(),
		}
		instance.initialize()
	})
	return instance
}

// Get returns the singleton Metrics instance
func Get() *Metrics {
	return New()
}

func (m *Metrics) initialize() {
	// Request metrics
	m.RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_total",
			Help:      "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	m.RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path", "status"},
	)

	m.RequestSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "request_size_bytes",
			Help:      "HTTP request size in bytes",
			Buckets:   prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method"},
	)

	m.ResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "response_size_bytes",
			Help:      "HTTP response size in bytes",
			Buckets:   prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method"},
	)

	m.ActiveRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "active_requests",
			Help:      "Number of currently active requests",
		},
	)

	// WAF metrics
	m.WAFRulesEvaluated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rules_evaluated_total",
			Help:      "Total number of WAF rules evaluated",
		},
		[]string{"phase"},
	)

	m.WAFRuleMatches = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rule_matches_total",
			Help:      "Total number of WAF rule matches",
		},
		[]string{"rule_id", "severity", "category"},
	)

	m.WAFBlockedRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "blocked_requests_total",
			Help:      "Total number of requests blocked by WAF",
		},
		[]string{"reason"},
	)

	m.WAFEvalDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "evaluation_duration_seconds",
			Help:      "WAF rule evaluation duration in seconds",
			Buckets:   []float64{.0001, .0005, .001, .005, .01, .05, .1, .5},
		},
		[]string{"phase"},
	)

	// Rate limiting metrics
	m.RateLimitBlocks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rate_limit_blocks_total",
			Help:      "Total number of requests blocked by rate limiting",
		},
		[]string{"endpoint"},
	)

	m.RateLimitAllowed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rate_limit_allowed_total",
			Help:      "Total number of requests allowed by rate limiting",
		},
		[]string{"endpoint"},
	)

	m.ActiveVisitors = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "rate_limit_active_visitors",
			Help:      "Number of active visitors being tracked",
		},
	)

	// Authentication metrics
	m.AuthAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "auth_attempts_total",
			Help:      "Total number of authentication attempts",
		},
		[]string{"result"},
	)

	m.AuthFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "auth_failures_total",
			Help:      "Total number of authentication failures",
		},
		[]string{"reason"},
	)

	m.ActiveSessions = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "active_sessions",
			Help:      "Number of active user sessions",
		},
	)

	// Threat Intelligence metrics
	m.ThreatBlocks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "threat_blocks_total",
			Help:      "Total number of requests blocked by threat intelligence",
		},
		[]string{"category"},
	)

	m.ThreatListSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "threat_list_size",
			Help:      "Number of entries in threat intelligence list",
		},
	)

	// System metrics
	m.Uptime = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "uptime_seconds_total",
			Help:      "Total uptime in seconds",
		},
	)

	m.GoRoutines = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "goroutines",
			Help:      "Number of goroutines",
		},
	)

	m.MemoryAlloc = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "memory_alloc_bytes",
			Help:      "Memory allocated in bytes",
		},
	)

	m.MemoryTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "memory_total_bytes",
			Help:      "Total memory in bytes",
		},
	)

	// Database metrics
	m.DBConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "db_connections",
			Help:      "Number of active database connections",
		},
	)

	m.DBQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "db_query_duration_seconds",
			Help:      "Database query duration in seconds",
			Buckets:   []float64{.001, .005, .01, .05, .1, .5, 1, 5},
		},
		[]string{"operation"},
	)

	m.DBErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "db_errors_total",
			Help:      "Total number of database errors",
		},
		[]string{"operation"},
	)

	// Register all metrics
	m.registry.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.RequestSize,
		m.ResponseSize,
		m.ActiveRequests,
		m.WAFRulesEvaluated,
		m.WAFRuleMatches,
		m.WAFBlockedRequests,
		m.WAFEvalDuration,
		m.RateLimitBlocks,
		m.RateLimitAllowed,
		m.ActiveVisitors,
		m.AuthAttempts,
		m.AuthFailures,
		m.ActiveSessions,
		m.ThreatBlocks,
		m.ThreatListSize,
		m.Uptime,
		m.GoRoutines,
		m.MemoryAlloc,
		m.MemoryTotal,
		m.DBConnections,
		m.DBQueryDuration,
		m.DBErrors,
	)

	// Also register default Go collectors
	m.registry.MustRegister(prometheus.NewGoCollector())
	m.registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
}

// Handler returns an HTTP handler for the /metrics endpoint
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// RecordRequest records metrics for an HTTP request
func (m *Metrics) RecordRequest(method, path string, statusCode int, duration time.Duration, requestSize, responseSize int64) {
	status := strconv.Itoa(statusCode)

	m.RequestsTotal.WithLabelValues(method, path, status).Inc()
	m.RequestDuration.WithLabelValues(method, path, status).Observe(duration.Seconds())
	m.RequestSize.WithLabelValues(method).Observe(float64(requestSize))
	m.ResponseSize.WithLabelValues(method).Observe(float64(responseSize))
}

// RecordWAFRuleEvaluation records WAF rule evaluation
func (m *Metrics) RecordWAFRuleEvaluation(phase string, duration time.Duration) {
	m.WAFRulesEvaluated.WithLabelValues(phase).Inc()
	m.WAFEvalDuration.WithLabelValues(phase).Observe(duration.Seconds())
}

// RecordWAFRuleMatch records a WAF rule match
func (m *Metrics) RecordWAFRuleMatch(ruleID int, severity, category string) {
	m.WAFRuleMatches.WithLabelValues(strconv.Itoa(ruleID), severity, category).Inc()
}

// RecordWAFBlock records a WAF block
func (m *Metrics) RecordWAFBlock(reason string) {
	m.WAFBlockedRequests.WithLabelValues(reason).Inc()
}

// RecordRateLimitDecision records a rate limiting decision
func (m *Metrics) RecordRateLimitDecision(endpoint string, allowed bool) {
	if allowed {
		m.RateLimitAllowed.WithLabelValues(endpoint).Inc()
	} else {
		m.RateLimitBlocks.WithLabelValues(endpoint).Inc()
	}
}

// RecordAuthAttempt records an authentication attempt
func (m *Metrics) RecordAuthAttempt(success bool, failureReason string) {
	if success {
		m.AuthAttempts.WithLabelValues("success").Inc()
	} else {
		m.AuthAttempts.WithLabelValues("failure").Inc()
		m.AuthFailures.WithLabelValues(failureReason).Inc()
	}
}

// RecordThreatBlock records a threat intelligence block
func (m *Metrics) RecordThreatBlock(category string) {
	m.ThreatBlocks.WithLabelValues(category).Inc()
}

// RecordDBQuery records a database query
func (m *Metrics) RecordDBQuery(operation string, duration time.Duration, err error) {
	m.DBQueryDuration.WithLabelValues(operation).Observe(duration.Seconds())
	if err != nil {
		m.DBErrors.WithLabelValues(operation).Inc()
	}
}

// SetActiveRequests sets the number of active requests
func (m *Metrics) SetActiveRequests(count float64) {
	m.ActiveRequests.Set(count)
}

// SetActiveVisitors sets the number of active rate limit visitors
func (m *Metrics) SetActiveVisitors(count float64) {
	m.ActiveVisitors.Set(count)
}

// SetActiveSessions sets the number of active sessions
func (m *Metrics) SetActiveSessions(count float64) {
	m.ActiveSessions.Set(count)
}

// SetThreatListSize sets the size of the threat list
func (m *Metrics) SetThreatListSize(size float64) {
	m.ThreatListSize.Set(size)
}

// SetDBConnections sets the number of active DB connections
func (m *Metrics) SetDBConnections(count float64) {
	m.DBConnections.Set(count)
}

// SetSystemMetrics sets system-level metrics
func (m *Metrics) SetSystemMetrics(goroutines int, allocBytes, totalBytes uint64) {
	m.GoRoutines.Set(float64(goroutines))
	m.MemoryAlloc.Set(float64(allocBytes))
	m.MemoryTotal.Set(float64(totalBytes))
}

// IncrementUptime increments the uptime counter
func (m *Metrics) IncrementUptime() {
	m.Uptime.Inc()
}

// Middleware returns HTTP middleware that records request metrics
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics wrapping for WebSocket connections to avoid hijack issues
		if r.URL.Path == "/api/ws" {
			m.ActiveRequests.Inc()
			defer m.ActiveRequests.Dec()
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Track active requests
		m.ActiveRequests.Inc()
		defer m.ActiveRequests.Dec()

		// Wrap response writer to capture status code and size
		wrapped := &responseWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		// Record metrics
		duration := time.Since(start)
		m.RecordRequest(
			r.Method,
			normalizePath(r.URL.Path),
			wrapped.statusCode,
			duration,
			r.ContentLength,
			int64(wrapped.size),
		)
	})
}

// responseWrapper wraps http.ResponseWriter to capture response info
type responseWrapper struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (w *responseWrapper) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWrapper) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// Hijack implements http.Hijacker interface for WebSocket support
func (w *responseWrapper) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

// Flush implements http.Flusher interface
func (w *responseWrapper) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// normalizePath normalizes URL paths for metric labels to prevent cardinality explosion
func normalizePath(path string) string {
	// Normalize common patterns
	// /api/logs/123 -> /api/logs/:id
	// This prevents high cardinality in metrics

	// For now, return the path as-is
	// In production, implement path normalization based on your API routes
	return path
}
