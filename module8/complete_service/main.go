// Package main สาธิต Complete Observability Service — Metrics + Logging + Tracing
// รัน: go run main.go
// เปิด: http://localhost:8080/metrics, http://localhost:16686 (Jaeger)
//
// ============================================================
// THE THREE PILLARS OF OBSERVABILITY
// ============================================================
//
// 1. METRICS  → "อะไรผิดปกติ?" (alert, dashboard)
// 2. LOGS     → "อะไรเกิดขึ้นบ้าง?" (audit trail, debug)
// 3. TRACES   → "ทำไมมันช้า/พัง?" (request flow, bottleneck)
//
// Correlation Pattern:
//   Alert fired (metrics) → ดู Grafana dashboard → สังเกต request spike
//   → ค้นหา logs ด้วย correlation_id + trace_id
//   → เปิด trace ใน Jaeger → เห็น span ไหนช้า
//   → แก้ไข bottleneck
//
// ถ้ามีแค่ 1-2 pillar:
//   Metrics only: รู้ว่าพัง แต่ไม่รู้ทำไม
//   Logs only:    รู้ event แต่ไม่รู้ภาพรวม
//   Traces only:  รู้ flow แต่ไม่รู้ trend
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// ============================================================
// CONSTANTS & SERVICE INFO
// ============================================================

const (
	serviceName    = "order-service"
	serviceVersion = "1.0.0"
)

// ============================================================
// PROMETHEUS METRICS
// ============================================================

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"method", "path", "status_code"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"method", "path"},
	)

	activeConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_connections",
			Help: "Active HTTP connections",
		},
	)

	ordersTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orders_total",
			Help: "Total orders by status",
		},
		[]string{"status"},
	)

	orderProcessingDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "order_processing_duration_seconds",
			Help:    "Time to process an order end-to-end",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
	)

	orderValueBaht = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "order_value_baht",
			Help:    "Order value in Thai Baht",
			Buckets: []float64{100, 500, 1000, 2500, 5000, 10000, 25000},
		},
	)
)

// ============================================================
// TRACER
// ============================================================

var tracer trace.Tracer

// ============================================================
// LOGGER (global, set in main)
// ============================================================

var logger *slog.Logger

// ============================================================
// CONTEXT KEYS
// ============================================================

type contextKey string

const (
	correlationIDKey contextKey = "correlation_id"
	requestIDKey     contextKey = "request_id"
)

// ============================================================
// IN-MEMORY ORDER STORE
// ============================================================

// Order represents a customer order
type Order struct {
	ID           string    `json:"id"`
	CustomerID   string    `json:"customer_id"`
	CustomerTier string    `json:"customer_tier"`
	Items        []Item    `json:"items"`
	TotalBaht    float64   `json:"total_baht"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// Item represents an item in an order
type Item struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Quantity  int     `json:"quantity"`
}

// OrderStore is a thread-safe in-memory order store
type OrderStore struct {
	mu     sync.RWMutex
	orders map[string]*Order
}

func NewOrderStore() *OrderStore {
	return &OrderStore{orders: make(map[string]*Order)}
}

func (s *OrderStore) Save(order *Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[order.ID] = order
}

func (s *OrderStore) Get(id string) (*Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

func (s *OrderStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.orders)
}

// ============================================================
// RESPONSE WRITER WRAPPER (capture status code)
// ============================================================

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// ============================================================
// OBSERVABILITY MIDDLEWARE
// ============================================================
//
// Middleware นี้ทำทั้ง 3 pillars พร้อมกัน:
// 1. สร้าง Prometheus metrics (latency, count, status)
// 2. Log request/response ด้วย structured logging
// 3. สร้าง trace span และ propagate context
//
// KEY INSIGHT: correlation_id เดียวกันปรากฏใน logs + trace_id
// ทำให้หา request ข้าม pillars ได้

func observabilityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// ── 1. TRACE: Extract or create trace context ──
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		spanName := r.Method + " " + r.URL.Path
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.URLFull(r.URL.String()),
			),
		)
		defer span.End()

		// ── 2. CORRELATION ID: ตัวเชื่อมระหว่าง logs และ traces ──
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = generateID()
		}
		w.Header().Set("X-Correlation-ID", correlationID)

		// เพิ่ม correlation_id เป็น span attribute ด้วย
		// ทำให้ค้นหา trace ด้วย correlation_id ได้ใน Jaeger
		span.SetAttributes(attribute.String("correlation_id", correlationID))

		// Inject เข้า context
		ctx = context.WithValue(ctx, correlationIDKey, correlationID)
		r = r.WithContext(ctx)

		// ── 3. METRICS: เพิ่ม active connections ──
		activeConnections.Inc()
		defer activeConnections.Dec()

		// Wrap ResponseWriter
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// ── 4. LOG: บันทึก request เข้า ──
		// trace_id ปรากฏใน log เพื่อ correlation ไปยัง Jaeger
		spanCtx := span.SpanContext()
		logger.InfoContext(ctx, "request started",
			"method", r.Method,
			"path", r.URL.Path,
			"correlation_id", correlationID,
			"trace_id", spanCtx.TraceID().String(),
			"span_id", spanCtx.SpanID().String(),
		)

		// เรียก handler จริง
		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		statusStr := strconv.Itoa(rw.statusCode)
		path := r.URL.Path

		// ── 5. METRICS: บันทึก metrics หลัง handler ──
		httpRequestsTotal.WithLabelValues(r.Method, path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())

		// ── 6. TRACE: บันทึก status code ──
		span.SetAttributes(semconv.HTTPResponseStatusCode(rw.statusCode))
		if rw.statusCode >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", rw.statusCode))
		}

		// ── 7. LOG: บันทึก response พร้อม duration ──
		// ใช้ log level ตาม status code
		logFields := []any{
			"method", r.Method,
			"path", path,
			"status_code", rw.statusCode,
			"duration_ms", duration.Milliseconds(),
			"correlation_id", correlationID,
			"trace_id", spanCtx.TraceID().String(),
		}

		if rw.statusCode >= 500 {
			logger.ErrorContext(ctx, "request failed", logFields...)
		} else if rw.statusCode >= 400 {
			logger.WarnContext(ctx, "request client error", logFields...)
		} else {
			logger.InfoContext(ctx, "request completed", logFields...)
		}
	})
}

// ============================================================
// HANDLERS
// ============================================================

func createOrderHandler(store *OrderStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		correlationID, _ := ctx.Value(correlationIDKey).(string)

		// สร้าง child span สำหรับ business logic
		ctx, span := tracer.Start(ctx, "createOrder",
			trace.WithAttributes(
				attribute.String("correlation_id", correlationID),
			),
		)
		defer span.End()

		processingStart := time.Now()

		// จำลอง order data
		orderID := "ORD-" + generateID()[:8]
		order := &Order{
			ID:           orderID,
			CustomerID:   fmt.Sprintf("CUST-%d", rand.Intn(9999)),
			CustomerTier: randomTier(),
			TotalBaht:    100.0 + rand.Float64()*9900.0,
			Status:       "pending",
			CreatedAt:    time.Now(),
			Items: []Item{
				{
					ProductID: "PROD-001",
					Name:      "iPhone 15",
					Price:     35900.0,
					Quantity:  1,
				},
			},
		}

		span.SetAttributes(
			attribute.String("order.id", order.ID),
			attribute.String("customer.tier", order.CustomerTier),
			attribute.Float64("order.total_baht", order.TotalBaht),
		)

		logger.InfoContext(ctx, "processing order",
			"order_id", order.ID,
			"customer_tier", order.CustomerTier,
			"total_baht", order.TotalBaht,
			"correlation_id", correlationID,
		)

		// จำลอง processing (validation, payment, etc.)
		if err := simulateOrderProcessing(ctx, order); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			logger.ErrorContext(ctx, "order processing failed",
				"order_id", order.ID,
				"error", err.Error(),
				"correlation_id", correlationID,
			)

			ordersTotal.WithLabelValues("failed").Inc()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		order.Status = "confirmed"
		store.Save(order)

		processingDuration := time.Since(processingStart)
		orderProcessingDuration.Observe(processingDuration.Seconds())
		ordersTotal.WithLabelValues("success").Inc()
		orderValueBaht.Observe(order.TotalBaht)

		logger.InfoContext(ctx, "order confirmed",
			"order_id", order.ID,
			"duration_ms", processingDuration.Milliseconds(),
			"correlation_id", correlationID,
		)

		span.AddEvent("order confirmed",
			trace.WithAttributes(
				attribute.String("order.status", "confirmed"),
				attribute.Int64("processing_ms", processingDuration.Milliseconds()),
			),
		)
		span.SetStatus(codes.Ok, "order created")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(order)
	}
}

func getOrderHandler(store *OrderStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		correlationID, _ := ctx.Value(correlationIDKey).(string)

		// ดึง order ID จาก path /orders/{id}
		orderID := r.URL.Query().Get("id")
		if orderID == "" {
			// ใน production ใช้ path parameter แทน query param
			http.Error(w, "order id required", http.StatusBadRequest)
			return
		}

		ctx, span := tracer.Start(ctx, "getOrder",
			trace.WithAttributes(
				attribute.String("order.id", orderID),
			),
		)
		defer span.End()

		logger.InfoContext(ctx, "fetching order",
			"order_id", orderID,
			"correlation_id", correlationID,
		)

		order, ok := store.Get(orderID)
		if !ok {
			span.SetStatus(codes.Error, "order not found")
			logger.WarnContext(ctx, "order not found",
				"order_id", orderID,
				"correlation_id", correlationID,
			)
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}

		span.SetStatus(codes.Ok, "order retrieved")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(order)
	}
}

// healthHandler บอก liveness + readiness
// Liveness: service ยังทำงานอยู่ไหม? (ถ้า false → restart)
// Readiness: service พร้อมรับ traffic ไหม? (ถ้า false → หยุดส่ง traffic)
func healthHandler(store *OrderStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := map[string]interface{}{
			"status":      "ok",
			"service":     serviceName,
			"version":     serviceVersion,
			"order_count": store.Count(),
			"time":        time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(health)
	}
}

// ============================================================
// SIMULATE ORDER PROCESSING (จำลอง latency และ errors)
// ============================================================

func simulateOrderProcessing(ctx context.Context, order *Order) error {
	// Validate
	ctx, validateSpan := tracer.Start(ctx, "validateOrder")
	time.Sleep(time.Duration(5+rand.Intn(20)) * time.Millisecond)
	validateSpan.End()

	// Check inventory (DB call simulation)
	ctx, inventorySpan := tracer.Start(ctx, "db.checkInventory",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemNameKey.String("postgresql"),
			semconv.DBNamespace("inventory_db"),
		),
	)
	time.Sleep(time.Duration(10+rand.Intn(30)) * time.Millisecond)
	inventorySpan.End()

	// Process payment (external API simulation)
	_, paymentSpan := tracer.Start(ctx, "payment.process",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("payment.gateway", "stripe"),
			attribute.Float64("payment.amount", order.TotalBaht),
		),
	)
	time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)

	// 5% failure rate
	if rand.Float64() < 0.05 {
		err := errors.New("payment gateway timeout")
		paymentSpan.RecordError(err)
		paymentSpan.SetStatus(codes.Error, "payment failed")
		paymentSpan.End()
		return err
	}

	paymentSpan.SetStatus(codes.Ok, "payment approved")
	paymentSpan.End()

	return nil
}

// ============================================================
// TRACING SETUP
// ============================================================

func setupTracing(ctx context.Context) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4318"
	}
	// WithEndpoint expects "host:port", strip scheme if provided
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracer = otel.Tracer(serviceName)
	return tp.Shutdown, nil
}

// ============================================================
// LOGGING SETUP
// ============================================================

func setupLogger() *slog.Logger {
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	if os.Getenv("LOG_LEVEL") == "debug" {
		level.Set(slog.LevelDebug)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler).With(
		"service", serviceName,
		"version", serviceVersion,
	)
}

// ============================================================
// HELPERS
// ============================================================

func generateID() string {
	return fmt.Sprintf("%016x", rand.Int63())
}

func randomTier() string {
	tiers := []string{"standard", "standard", "standard", "gold", "gold", "platinum"}
	return tiers[rand.Intn(len(tiers))]
}

// ============================================================
// MAIN — เริ่ม service พร้อม graceful shutdown
// ============================================================

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Setup Three Pillars ──

	// Pillar 1: Logging
	logger = setupLogger()
	slog.SetDefault(logger)

	// Pillar 2: Tracing
	shutdown, err := setupTracing(ctx)
	if err != nil {
		logger.Warn("tracing setup failed, continuing without traces",
			"error", err,
			"hint", "run docker-compose up to start Jaeger",
		)
		tracer = nooptrace.NewTracerProvider().Tracer(serviceName)
	} else {
		defer shutdown(ctx)
	}

	// Pillar 3: Metrics (auto-setup via promauto)
	logger.Info("prometheus metrics registered")

	// ── In-memory store ──
	store := NewOrderStore()

	// ── Routes ──
	mux := http.NewServeMux()
	mux.HandleFunc("/orders", createOrderHandler(store))
	mux.HandleFunc("/orders/get", getOrderHandler(store))  // GET /orders/get?id=ORD-xxx
	mux.HandleFunc("/health", healthHandler(store))
	mux.Handle("/metrics", promhttp.Handler())

	// ── Wrap with observability middleware ──
	handler := observabilityMiddleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── Start server ──
	go func() {
		logger.Info("server started",
			"addr", server.Addr,
			"metrics", "http://localhost:"+port+"/metrics",
			"health", "http://localhost:"+port+"/health",
			"jaeger", "http://localhost:16686",
		)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server listen error", "error", err)
			os.Exit(1)
		}
	}()

	printStartupBanner(port)

	// รอ shutdown signal
	<-ctx.Done()

	logger.Info("shutdown signal received, draining connections...")

	// Graceful shutdown: รอให้ active requests เสร็จ (max 30s)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown error", "error", err)
	}

	logger.Info("server stopped gracefully")
}

func printStartupBanner(port string) {
	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  Module 8: Enterprise Observability — Complete Service       ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("Endpoints:\n")
	fmt.Printf("  POST http://localhost:%s/orders           Create order\n", port)
	fmt.Printf("  GET  http://localhost:%s/orders/get?id=X  Get order\n", port)
	fmt.Printf("  GET  http://localhost:%s/health           Health check\n", port)
	fmt.Printf("  GET  http://localhost:%s/metrics          Prometheus metrics\n", port)
	fmt.Printf("\n")
	fmt.Printf("Observability stack (after docker-compose up):\n")
	fmt.Printf("  Grafana:    http://localhost:3000  (admin/admin)\n")
	fmt.Printf("  Prometheus: http://localhost:9090\n")
	fmt.Printf("  Jaeger:     http://localhost:16686\n")
	fmt.Printf("\n")
	fmt.Printf("Correlation flow:\n")
	fmt.Printf("  Alert fires → Grafana dashboard → logs (correlation_id)\n")
	fmt.Printf("              → Jaeger trace (trace_id) → fix bottleneck\n")
	fmt.Printf("\n")
	fmt.Printf("Press Ctrl+C to stop\n\n")
}
