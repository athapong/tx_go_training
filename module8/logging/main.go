// Package main สาธิต Structured Logging ด้วย log/slog (Go 1.21+)
// รัน: go run main.go
// ลอง: LOG_LEVEL=debug go run main.go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ============================================================
// ทำไม Structured Logging ถึงดีกว่า fmt.Println?
// ============================================================
//
// fmt.Println("User 123 login failed at 2024-01-15 15:04:05")
// → ค้นหาได้ยาก, parse ยาก, ไม่สามารถ filter ได้
//
// slog.Info("login failed", "user_id", 123, "time", time.Now())
// → JSON output: {"level":"INFO","user_id":123,"msg":"login failed",...}
// → ค้นหาได้ใน Loki/Elasticsearch: user_id=123 AND level=ERROR
// → ทำ aggregation ได้: count errors per user_id
// → trace correlation: เพิ่ม trace_id ได้ง่าย
//
// Structured logging = log ที่ machine อ่านได้เหมือน human อ่านได้

// ============================================================
// CONTEXT KEYS
// ============================================================

type contextKey string

const (
	correlationIDKey contextKey = "correlation_id"
	userIDKey        contextKey = "user_id"
)

// ============================================================
// CUSTOM HANDLER: CorrelationHandler — เพิ่ม correlation ID จาก context
// ============================================================
//
// ปัญหา: ถ้ามี goroutines หลายตัว log พร้อมกัน จะรู้ได้ยังไงว่า
//         log ไหนเป็นของ request ไหน?
//
// แก้ปัญหา: ใส่ correlation_id ใน context แล้ว inject เข้า log อัตโนมัติ

// CorrelationHandler เพิ่ม correlation_id จาก context เข้าทุก log record
type CorrelationHandler struct {
	inner slog.Handler
}

func NewCorrelationHandler(inner slog.Handler) *CorrelationHandler {
	return &CorrelationHandler{inner: inner}
}

// Enabled ตรวจสอบว่า log level นี้ถูก enable ไหม
func (h *CorrelationHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle เพิ่ม correlation_id และ user_id จาก context ก่อน log
func (h *CorrelationHandler) Handle(ctx context.Context, r slog.Record) error {
	// ดึง correlation_id จาก context
	if corrID, ok := ctx.Value(correlationIDKey).(string); ok && corrID != "" {
		r.AddAttrs(slog.String("correlation_id", corrID))
	}

	// ดึง user_id จาก context (ถ้ามี)
	if userID, ok := ctx.Value(userIDKey).(string); ok && userID != "" {
		r.AddAttrs(slog.String("user_id", userID))
	}

	return h.inner.Handle(ctx, r)
}

// WithAttrs สร้าง handler ใหม่ที่มี attributes เพิ่มเติม
func (h *CorrelationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup สร้าง handler ใหม่ที่ group attributes ด้วย prefix
func (h *CorrelationHandler) WithGroup(name string) slog.Handler {
	return &CorrelationHandler{inner: h.inner.WithGroup(name)}
}

// ============================================================
// MASKING HANDLER: ซ่อน sensitive data ใน logs
// ============================================================
//
// ปัญหาใน production logs ที่พบบ่อย:
//   - card_number: "4111-1111-1111-1111" ปรากฏใน logs!
//   - password: "mypassword123" ปรากฏใน logs!
//   - email: "user@example.com" อาจต้องซ่อน (PDPA)
//
// แก้ปัญหา: MaskingHandler ตรวจสอบ field name และ mask ค่า

var (
	// sensitiveFields คือ field names ที่ต้อง mask
	sensitiveFields = map[string]bool{
		"password":       true,
		"card_number":    true,
		"cvv":            true,
		"secret":         true,
		"access_token":   true,
		"refresh_token":  true,
		"api_key":        true,
	}

	// emailRegex สำหรับ detect และ mask email addresses
	emailRegex = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// cardRegex สำหรับ detect credit card numbers
	cardRegex = regexp.MustCompile(`\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b`)
)

// MaskingHandler ซ่อน sensitive fields ใน log records
type MaskingHandler struct {
	inner      slog.Handler
	maskEmails bool
}

func NewMaskingHandler(inner slog.Handler, maskEmails bool) *MaskingHandler {
	return &MaskingHandler{inner: inner, maskEmails: maskEmails}
}

func (h *MaskingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *MaskingHandler) Handle(ctx context.Context, r slog.Record) error {
	// สร้าง record ใหม่พร้อม masked attributes
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)

	r.Attrs(func(attr slog.Attr) bool {
		newRecord.AddAttrs(h.maskAttr(attr))
		return true
	})

	return h.inner.Handle(ctx, newRecord)
}

func (h *MaskingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	masked := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		masked[i] = h.maskAttr(attr)
	}
	return &MaskingHandler{inner: h.inner.WithAttrs(masked), maskEmails: h.maskEmails}
}

func (h *MaskingHandler) WithGroup(name string) slog.Handler {
	return &MaskingHandler{inner: h.inner.WithGroup(name), maskEmails: h.maskEmails}
}

// maskAttr ตรวจสอบ attribute แล้วซ่อนค่าถ้าจำเป็น
func (h *MaskingHandler) maskAttr(attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)

	// ตรวจ field name
	if sensitiveFields[key] {
		return slog.String(attr.Key, "[REDACTED]")
	}

	// ตรวจ value สำหรับ card numbers
	if attr.Value.Kind() == slog.KindString {
		val := attr.Value.String()
		if cardRegex.MatchString(val) {
			masked := cardRegex.ReplaceAllStringFunc(val, func(s string) string {
				// แสดงแค่ 4 หลักสุดท้าย
				clean := strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), " ", "")
				if len(clean) >= 4 {
					return "****-****-****-" + clean[len(clean)-4:]
				}
				return "****"
			})
			return slog.String(attr.Key, masked)
		}

		// ตรวจ email (ถ้า enable)
		if h.maskEmails && emailRegex.MatchString(val) {
			masked := emailRegex.ReplaceAllStringFunc(val, func(s string) string {
				parts := strings.Split(s, "@")
				if len(parts) == 2 && len(parts[0]) > 2 {
					return parts[0][:2] + "***@" + parts[1]
				}
				return "***@" + parts[len(parts)-1]
			})
			return slog.String(attr.Key, masked)
		}
	}

	return attr
}

// ============================================================
// SAMPLING HANDLER: บันทึก log แค่บางส่วน
// ============================================================
//
// ปัญหา: ถ้า log ทุก successful request ใน high-traffic service
//         → ค่า storage พุ่งสูง
//         → Loki/Elasticsearch รับไม่ไหว
//
// แก้ปัญหา: log 1% ของ successful requests, 100% ของ errors

// SamplingHandler log แค่ fraction ของ low-level logs
type SamplingHandler struct {
	inner      slog.Handler
	sampleRate float64 // 0.0-1.0 (1.0 = 100%)
}

func NewSamplingHandler(inner slog.Handler, sampleRate float64) *SamplingHandler {
	return &SamplingHandler{inner: inner, sampleRate: sampleRate}
}

func (h *SamplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *SamplingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Error และ Warn: log ทุก record (100%)
	if r.Level >= slog.LevelWarn {
		return h.inner.Handle(ctx, r)
	}

	// Info และ Debug: sample ตาม rate
	n, err := rand.Int(rand.Reader, big.NewInt(1000))
	if err != nil {
		return h.inner.Handle(ctx, r) // fallback: log ถ้า error
	}

	threshold := int64(h.sampleRate * 1000)
	if n.Int64() < threshold {
		// เพิ่ม attribute บอกว่า record นี้ถูก sample
		r.AddAttrs(slog.Bool("sampled", true))
		return h.inner.Handle(ctx, r)
	}

	// Skip log record นี้
	return nil
}

func (h *SamplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SamplingHandler{inner: h.inner.WithAttrs(attrs), sampleRate: h.sampleRate}
}

func (h *SamplingHandler) WithGroup(name string) slog.Handler {
	return &SamplingHandler{inner: h.inner.WithGroup(name), sampleRate: h.sampleRate}
}

// ============================================================
// LOG LEVEL CONFIGURATION
// ============================================================

// LevelVar ทำให้เปลี่ยน log level ได้ runtime โดยไม่ต้อง restart
var globalLevel = new(slog.LevelVar)

// parseLevelFromEnv อ่าน LOG_LEVEL จาก environment variable
func parseLevelFromEnv() slog.Level {
	levelStr := os.Getenv("LOG_LEVEL")
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo // default
	}
}

// ============================================================
// REQUEST MIDDLEWARE: inject correlation ID และ log request/response
// ============================================================

// generateCorrelationID สร้าง unique ID สำหรับแต่ละ request
func generateCorrelationID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// loggingMiddleware inject correlation ID และ log request/response
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// ดึง correlation ID จาก header (ถ้า upstream ส่งมา)
		// หรือสร้างใหม่ถ้าไม่มี
		corrID := r.Header.Get("X-Correlation-ID")
		if corrID == "" {
			corrID = generateCorrelationID()
		}

		// inject correlation ID เข้า context
		ctx := context.WithValue(r.Context(), correlationIDKey, corrID)
		r = r.WithContext(ctx)

		// ส่ง correlation ID กลับไปใน response header
		w.Header().Set("X-Correlation-ID", corrID)

		// Wrap ResponseWriter เพื่อ capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Log request เข้ามา
		logger.InfoContext(ctx, "request started",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)

		// เรียก handler
		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		// เลือก log level ตาม status code
		if rw.statusCode >= 500 {
			logger.ErrorContext(ctx, "request completed with server error",
				"method", r.Method,
				"path", r.URL.Path,
				"status_code", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
			)
		} else if rw.statusCode >= 400 {
			logger.WarnContext(ctx, "request completed with client error",
				"method", r.Method,
				"path", r.URL.Path,
				"status_code", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
			)
		} else {
			logger.InfoContext(ctx, "request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status_code", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
			)
		}
	})
}

// responseWriter wraps http.ResponseWriter เพื่อ capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ============================================================
// DEMO HANDLERS
// ============================================================

func checkoutHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		logger.InfoContext(ctx, "processing checkout",
			"cart_items", 3,
			"subtotal_baht", 1500.00,
		)

		// ตัวอย่าง: ข้อมูล sensitive ถูก mask อัตโนมัติ
		logger.InfoContext(ctx, "payment details received",
			"payment_method", "credit_card",
			"card_number", "4111-1111-1111-1111", // จะถูก mask!
			"amount_baht", 1500.00,
		)

		// ตัวอย่าง: email ถูก mask
		logger.InfoContext(ctx, "order confirmation",
			"email", "customer@example.com", // จะถูก mask!
			"order_id", "ORD-12345",
		)

		// ตัวอย่าง: sensitive field name ถูก redact
		logger.InfoContext(ctx, "auth check",
			"user_id", "USER-789",
			"password", "super_secret_123", // จะถูก REDACTED!
		)

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"order_id": "ORD-12345", "status": "confirmed"}`)
	}
}

func errorHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		logger.ErrorContext(ctx, "database connection failed",
			"db_host", "db.internal",
			"retry_count", 3,
			"error", "connection refused",
		)

		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
	}
}

// levelChangeHandler เปลี่ยน log level runtime (ไม่ต้อง restart!)
// curl -X PUT http://localhost:8081/log-level?level=debug
func levelChangeHandler(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	var newLevel slog.Level

	switch strings.ToLower(level) {
	case "debug":
		newLevel = slog.LevelDebug
	case "info":
		newLevel = slog.LevelInfo
	case "warn":
		newLevel = slog.LevelWarn
	case "error":
		newLevel = slog.LevelError
	default:
		http.Error(w, "invalid level: use debug|info|warn|error", http.StatusBadRequest)
		return
	}

	globalLevel.Set(newLevel)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"log_level": "%s", "changed": true}`, level)
}

// ============================================================
// SETUP: สร้าง logger chain
// ============================================================

func setupLogger(w io.Writer) *slog.Logger {
	// ตั้งค่า log level จาก environment
	globalLevel.Set(parseLevelFromEnv())

	// Base handler: JSON output (เหมาะสำหรับ production)
	// ใช้ Text handler สำหรับ development ถ้าต้องการ human-readable
	jsonHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: globalLevel, // dynamic level จาก LevelVar
		// AddSource: true, // เพิ่ม source file:line ใน development
	})

	// Layer 1: Masking — ซ่อน sensitive data (ชั้นใน)
	maskingHandler := NewMaskingHandler(jsonHandler, true)

	// Layer 2: Correlation — เพิ่ม correlation_id จาก context
	correlationHandler := NewCorrelationHandler(maskingHandler)

	// Layer 3: Sampling — log แค่ 10% ของ Info (ชั้นนอก)
	// ในตัวอย่างนี้ใช้ 10% เพื่อให้เห็นผล (production ใช้ 1%)
	samplingHandler := NewSamplingHandler(correlationHandler, 0.10)

	return slog.New(samplingHandler)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	// ตั้งค่า logger
	logger := setupLogger(os.Stdout)

	// Set เป็น default logger ด้วย
	slog.SetDefault(logger)

	logger.Info("server starting",
		"log_level", globalLevel.Level().String(),
		"go_version", "1.21+",
	)

	mux := http.NewServeMux()

	// Demo endpoints
	mux.HandleFunc("/checkout", checkoutHandler(logger))
	mux.HandleFunc("/error", errorHandler(logger))
	mux.HandleFunc("/log-level", levelChangeHandler) // runtime level change

	// Wrap ด้วย logging middleware
	handler := loggingMiddleware(logger, mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	fmt.Printf("=== Structured Logging Demo ===\n")
	fmt.Printf("Server: http://localhost:%s\n", port)
	fmt.Printf("\nDemo endpoints:\n")
	fmt.Printf("  curl http://localhost:%s/checkout\n", port)
	fmt.Printf("  curl http://localhost:%s/error\n", port)
	fmt.Printf("  curl -X PUT 'http://localhost:%s/log-level?level=debug'\n", port)
	fmt.Printf("\nLog level (from LOG_LEVEL env): %s\n", globalLevel.Level())
	fmt.Printf("Sampling: 10%% of Info, 100%% of Warn/Error\n")
	fmt.Printf("Sensitive fields masked: password, card_number, email\n\n")

	// Demo: log ตัวอย่างโดยตรง
	demoDirectLogging(logger)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	if err := server.ListenAndServe(); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

// demoDirectLogging แสดงตัวอย่าง logging patterns ต่างๆ
func demoDirectLogging(logger *slog.Logger) {
	// สร้าง context พร้อม correlation ID
	ctx := context.WithValue(context.Background(), correlationIDKey, "demo-corr-001")
	ctx = context.WithValue(ctx, userIDKey, "user-999")

	fmt.Println("--- Structured Logging Examples ---")

	// ตัวอย่าง 1: Basic structured log
	logger.InfoContext(ctx, "order created",
		"order_id", "ORD-001",
		"amount_baht", 2500.00,
		"items", 3,
	)

	// ตัวอย่าง 2: Group attributes (nested JSON)
	logger.InfoContext(ctx, "payment processed",
		slog.Group("payment",
			"method", "promptpay",
			"amount_baht", 2500.00,
			"ref_number", "PPY-12345",
		),
		slog.Group("customer",
			"id", "CUST-789",
			"tier", "gold",
		),
	)

	// ตัวอย่าง 3: Error with stack context
	logger.ErrorContext(ctx, "payment gateway timeout",
		"gateway", "stripe",
		"timeout_ms", 5000,
		"retry_count", 3,
		"order_id", "ORD-002",
	)

	// ตัวอย่าง 4: Sensitive data — ถูก mask อัตโนมัติ
	logger.InfoContext(ctx, "user credentials (will be masked)",
		"username", "john.doe",
		"password", "secret123",  // → [REDACTED]
		"card_number", "4111111111111111", // → ****-****-****-1111
		"email", "john@example.com",       // → jo***@example.com
	)

	// ตัวอย่าง 5: Logger ที่ bind กับ component
	dbLogger := logger.With("component", "database", "db", "orders_db")
	dbLogger.InfoContext(ctx, "query executed",
		"query", "SELECT * FROM orders WHERE id = $1",
		"duration_ms", 12,
		"rows", 1,
	)

	fmt.Println("--- End of Demo ---")
	fmt.Println()

	// แสดง JSON output ตัวอย่าง
	demoJSONOutput()
}

// demoJSONOutput แสดงตัวอย่าง JSON log ที่ human อ่านได้
func demoJSONOutput() {
	example := map[string]interface{}{
		"time":           "2024-01-15T10:30:45.123456Z",
		"level":          "INFO",
		"msg":            "order created",
		"order_id":       "ORD-001",
		"amount_baht":    2500.00,
		"correlation_id": "abc123def456",
		"user_id":        "user-999",
		"sampled":        true,
	}

	b, _ := json.MarshalIndent(example, "", "  ")
	fmt.Printf("Example JSON log:\n%s\n\n", string(b))

	fmt.Println("Log query examples (Loki/Grafana):")
	fmt.Println(`  {app="order-service"} | json | level="ERROR"`)
	fmt.Println(`  {app="order-service"} | json | correlation_id="abc123def456"`)
	fmt.Println(`  {app="order-service"} | json | amount_baht > 10000`)
	fmt.Println()
}
