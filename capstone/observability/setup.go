// Package observability — Metrics, Logging, Tracing Setup
// Module 8 Integration: Three pillars of observability
//
// 1. Metrics  — Prometheus counters, histograms, gauges
// 2. Logging  — Structured logging ด้วย log/slog + correlation IDs
// 3. Tracing  — Correlation ID propagation (production: add OpenTelemetry)
//
// NOTE: ต้องการ dependency: github.com/prometheus/client_golang
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// ============================================================
// METRICS DEFINITIONS
// ============================================================

// MetricNames — ชื่อ metrics ทั้งหมด (ใช้ constants ป้องกัน typo)
const (
	MetricInvoicesProcessedTotal      = "invoices_processed_total"
	MetricInvoiceProcessingDuration   = "invoice_processing_duration_seconds"
	MetricAIExtractionDuration        = "ai_extraction_duration_seconds"
	MetricInvoiceApprovalTotal        = "invoice_approval_total"
	MetricInvoiceRejectionTotal       = "invoice_rejection_total"
	MetricHTTPRequestsTotal           = "http_requests_total"
	MetricHTTPRequestDuration         = "http_request_duration_seconds"
	MetricWorkerPoolActiveWorkers     = "worker_pool_active_workers"
)

// ============================================================
// SIMPLE IN-PROCESS METRICS
// (ใช้ไปก่อนถ้าไม่มี prometheus dependency)
// ============================================================

// SimpleCounter — thread-safe counter
type SimpleCounter struct {
	mu    sync.Mutex
	value int64
	name  string
}

func (c *SimpleCounter) Inc() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

func (c *SimpleCounter) Add(n int64) {
	c.mu.Lock()
	c.value += n
	c.mu.Unlock()
}

func (c *SimpleCounter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// SimpleHistogram — tracks sum + count สำหรับ average calculation
type SimpleHistogram struct {
	mu       sync.Mutex
	count    int64
	sum      float64
	name     string
}

func (h *SimpleHistogram) Observe(v float64) {
	h.mu.Lock()
	h.count++
	h.sum += v
	h.mu.Unlock()
}

func (h *SimpleHistogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

func (h *SimpleHistogram) Average() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}

// ============================================================
// OBSERVABILITY CONFIG
// ============================================================

// Config — observability configuration
type Config struct {
	// Logging
	LogLevel  string // "debug", "info", "warn", "error"
	LogFormat string // "json", "text"
	// Metrics
	MetricsEnabled bool
	// ServiceName สำหรับ structured logs
	ServiceName string
	// Environment
	Environment string // "development", "staging", "production"
}

func DefaultConfig() Config {
	return Config{
		LogLevel:        envOrDefault("LOG_LEVEL", "info"),
		LogFormat:       envOrDefault("LOG_FORMAT", "json"),
		MetricsEnabled:  true,
		ServiceName:     "smart-invoice-processor",
		Environment:     envOrDefault("ENVIRONMENT", "development"),
	}
}

// ============================================================
// OBSERVABILITY SETUP
// ============================================================

// Observability — container สำหรับ observability components
type Observability struct {
	Logger *slog.Logger
	Metrics *AppMetrics
	Config Config
}

// AppMetrics — application-level metrics
type AppMetrics struct {
	// Invoice processing
	InvoicesProcessed    *SimpleCounter
	InvoicesFailed       *SimpleCounter
	InvoicesApproved     *SimpleCounter
	InvoicesRejected     *SimpleCounter

	// AI extraction
	ExtractionSuccesses  *SimpleCounter
	ExtractionFailures   *SimpleCounter
	ExtractionDuration   *SimpleHistogram // seconds

	// Processing duration
	ProcessingDuration   *SimpleHistogram // seconds

	// HTTP
	HTTPRequests         *SimpleCounter
	HTTPDuration         *SimpleHistogram // seconds
	HTTPErrors           *SimpleCounter

	// Approval rate (computed: approved / (approved + rejected))
}

// ApprovalRate คำนวณ approval rate
func (m *AppMetrics) ApprovalRate() float64 {
	approved := float64(m.InvoicesApproved.Value())
	rejected := float64(m.InvoicesRejected.Value())
	total := approved + rejected
	if total == 0 {
		return 0
	}
	return approved / total
}

// Snapshot คืน current metrics ใน map format (สำหรับ /metrics endpoint)
func (m *AppMetrics) Snapshot() map[string]interface{} {
	return map[string]interface{}{
		"invoices_processed_total":      m.InvoicesProcessed.Value(),
		"invoices_failed_total":         m.InvoicesFailed.Value(),
		"invoices_approved_total":       m.InvoicesApproved.Value(),
		"invoices_rejected_total":       m.InvoicesRejected.Value(),
		"extraction_successes_total":    m.ExtractionSuccesses.Value(),
		"extraction_failures_total":     m.ExtractionFailures.Value(),
		"extraction_duration_avg_sec":   m.ExtractionDuration.Average(),
		"processing_duration_avg_sec":   m.ProcessingDuration.Average(),
		"http_requests_total":           m.HTTPRequests.Value(),
		"http_errors_total":             m.HTTPErrors.Value(),
		"http_duration_avg_sec":         m.HTTPDuration.Average(),
		"invoice_approval_rate":         m.ApprovalRate(),
	}
}

// InitObservability เริ่มต้น observability components
// คืน *Observability และ cleanup function
func InitObservability(cfg Config) (*Observability, func()) {
	// ── Setup Logger ──
	logger := setupLogger(cfg)
	logger.Info("Initializing observability",
		"service", cfg.ServiceName,
		"environment", cfg.Environment,
		"log_level", cfg.LogLevel,
	)

	// ── Setup Metrics ──
	metrics := &AppMetrics{
		InvoicesProcessed:   &SimpleCounter{name: MetricInvoicesProcessedTotal},
		InvoicesFailed:      &SimpleCounter{name: MetricInvoicesProcessedTotal + "_failed"},
		InvoicesApproved:    &SimpleCounter{name: MetricInvoiceApprovalTotal},
		InvoicesRejected:    &SimpleCounter{name: MetricInvoiceRejectionTotal},
		ExtractionSuccesses: &SimpleCounter{name: "ai_extraction_successes_total"},
		ExtractionFailures:  &SimpleCounter{name: "ai_extraction_failures_total"},
		ExtractionDuration:  &SimpleHistogram{name: MetricAIExtractionDuration},
		ProcessingDuration:  &SimpleHistogram{name: MetricInvoiceProcessingDuration},
		HTTPRequests:        &SimpleCounter{name: MetricHTTPRequestsTotal},
		HTTPDuration:        &SimpleHistogram{name: MetricHTTPRequestDuration},
		HTTPErrors:          &SimpleCounter{name: MetricHTTPRequestsTotal + "_errors"},
	}

	obs := &Observability{
		Logger:  logger,
		Metrics: metrics,
		Config:  cfg,
	}

	// Cleanup function
	cleanup := func() {
		logger.Info("Observability cleanup complete")
	}

	return obs, cleanup
}

// ============================================================
// LOGGER SETUP
// ============================================================

func setupLogger(cfg Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler

	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	// เพิ่ม service metadata ทุก log line
	logger := slog.New(handler).With(
		"service", cfg.ServiceName,
		"env", cfg.Environment,
	)

	return logger
}

// ============================================================
// CORRELATION ID MIDDLEWARE
// ============================================================

// ContextKey — type สำหรับ context keys (ป้องกัน collision)
type ContextKey string

const (
	ContextKeyRequestID   ContextKey = "request_id"
	ContextKeyCorrelation ContextKey = "correlation_id"
)

// WithRequestID เพิ่ม request ID เข้า context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ContextKeyRequestID, requestID)
}

// RequestIDFromContext ดึง request ID จาก context
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// LoggerFromContext คืน logger พร้อม correlation fields จาก context
func LoggerFromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if requestID := RequestIDFromContext(ctx); requestID != "" {
		return base.With("request_id", requestID)
	}
	return base
}

// ============================================================
// HTTP MIDDLEWARE (สำหรับ net/http)
// ============================================================

// RequestIDMiddleware inject request ID เข้าทุก request
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ดึงจาก header ก่อน (สำหรับ distributed tracing)
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set ใน response header ด้วย
		w.Header().Set("X-Request-ID", requestID)

		// Inject เข้า context
		ctx := WithRequestID(r.Context(), requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggingMiddleware log ทุก HTTP request พร้อม duration
func LoggingMiddleware(logger *slog.Logger, metrics *AppMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer เพื่อดัก status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			durationSec := duration.Seconds()

			// Record metrics
			metrics.HTTPRequests.Inc()
			metrics.HTTPDuration.Observe(durationSec)
			if wrapped.statusCode >= 500 {
				metrics.HTTPErrors.Inc()
			}

			// Structured log
			requestID := RequestIDFromContext(r.Context())
			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration_ms", duration.Milliseconds(),
				"request_id", requestID,
				"user_agent", r.Header.Get("User-Agent"),
			)
		})
	}
}

// responseWriter wraps http.ResponseWriter เพื่อดัก status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// ============================================================
// PROMETHEUS INTEGRATION
// (uncomment เมื่อ add github.com/prometheus/client_golang)
// ============================================================

// PrometheusMetricsHandler คืน http.Handler สำหรับ /metrics endpoint
// ที่ expose Prometheus metrics
//
// ตัวอย่าง Prometheus metrics ที่ควร register:
//
//   var invoicesProcessedCounter = prometheus.NewCounterVec(
//       prometheus.CounterOpts{
//           Name: "invoices_processed_total",
//           Help: "Total number of invoices processed",
//       },
//       []string{"status", "vendor_id"},
//   )
//
//   var processingDuration = prometheus.NewHistogramVec(
//       prometheus.HistogramOpts{
//           Name:    "invoice_processing_duration_seconds",
//           Help:    "Invoice processing duration in seconds",
//           Buckets: prometheus.DefBuckets,
//       },
//       []string{"extractor"},
//   )
//
//   var aiExtractionDuration = prometheus.NewHistogramVec(
//       prometheus.HistogramOpts{
//           Name:    "ai_extraction_duration_seconds",
//           Help:    "AI extraction duration in seconds",
//           Buckets: []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
//       },
//       []string{"extractor", "success"},
//   )
//
//   var approvalRateGauge = prometheus.NewGauge(prometheus.GaugeOpts{
//       Name: "invoice_approval_rate",
//       Help: "Current invoice approval rate (approved / total decisions)",
//   })
//
// เมื่อ add dependency:
//   import "github.com/prometheus/client_golang/prometheus/promhttp"
//   func PrometheusMetricsHandler() http.Handler {
//       return promhttp.Handler()
//   }

// SimpleMetricsHandler คืน handler สำหรับ /metrics ที่ใช้ simple metrics
// (ใช้ไปก่อนถ้าไม่มี Prometheus dependency)
func SimpleMetricsHandler(metrics *AppMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := metrics.Snapshot()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		// Output ในรูปแบบ Prometheus text format
		for key, val := range snapshot {
			switch v := val.(type) {
			case int64:
				_, _ = w.Write([]byte(fmt.Sprintf("# HELP %s Auto-generated metric\n# TYPE %s counter\n%s %d\n", key, key, key, v)))
			case float64:
				_, _ = w.Write([]byte(fmt.Sprintf("# HELP %s Auto-generated metric\n# TYPE %s gauge\n%s %.4f\n", key, key, key, v)))
			}
		}
	}
}

// ============================================================
// HELPERS
// ============================================================

// generateRequestID สร้าง simple request ID
// Production: ใช้ github.com/google/uuid แทน
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

