// Package main สาธิต OpenTelemetry Distributed Tracing
// รัน: go run main.go
// ดู traces: http://localhost:16686 (Jaeger UI)
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
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
// DISTRIBUTED TRACING — ภาพรวม
// ============================================================
//
// Trace = เส้นทางของ request ตั้งแต่ต้นจนจบ (ข้ามหลาย services)
//
// Span = หน่วยงานย่อยใน trace
//   - มี start time, end time, duration
//   - มี attributes (key-value)
//   - มี events (timestamped messages)
//   - มี status (OK, Error)
//   - Parent-child relationship สร้าง tree structure
//
// Trace Context = ข้อมูล trace ที่ส่งระหว่าง services
//   - trace_id: ระบุ trace ทั้งหมด (unique per request)
//   - span_id: ระบุ span ปัจจุบัน
//   - trace_flags: sampling decision
//
// Baggage = key-value data ที่ propagate ข้าม services
//   ใช้สำหรับ: customer_tier, experiment_id, feature_flags
//   ⚠️  อย่าใส่ sensitive data ใน baggage!

// ============================================================
// TRACER NAME: ตั้งชื่อตาม package/service
// ============================================================
const serviceName = "order-service"

var tracer trace.Tracer

// ============================================================
// SETUP: ตั้งค่า OTel SDK และ OTLP Exporter
// ============================================================

func setupTracing(ctx context.Context) (func(context.Context) error, error) {
	// อ่าน Jaeger endpoint จาก env
	jaegerEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if jaegerEndpoint == "" {
		jaegerEndpoint = "localhost:4318" // default Jaeger OTLP HTTP
	}
	// WithEndpoint expects "host:port", strip scheme if provided
	jaegerEndpoint = strings.TrimPrefix(jaegerEndpoint, "https://")
	jaegerEndpoint = strings.TrimPrefix(jaegerEndpoint, "http://")

	// สร้าง OTLP exporter — ส่ง spans ไปยัง Jaeger (หรือ collector)
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(jaegerEndpoint),
		otlptracehttp.WithInsecure(), // ไม่ต้องใช้ TLS สำหรับ dev
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	// Resource = ข้อมูลของ service นี้ (ปรากฏใน Jaeger ทุก span)
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
			attribute.String("deployment.region", "ap-southeast-1"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// TracerProvider = ตัวสร้าง tracers
	// BatchSpanProcessor = ส่ง spans เป็น batch (efficient กว่า sync)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Sampling: AlwaysSample สำหรับ development
		// ใน production ใช้ TraceIDRatioBased(0.1) = sample 10%
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// ตั้ง global TracerProvider
	otel.SetTracerProvider(tp)

	// ตั้ง global Propagator (ใช้ W3C TraceContext + Baggage standard)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C Trace Context (traceparent header)
		propagation.Baggage{},     // W3C Baggage (baggage header)
	))

	// สร้าง tracer สำหรับ service นี้
	tracer = otel.Tracer(serviceName)

	return tp.Shutdown, nil
}

// ============================================================
// HTTP MIDDLEWARE: Auto-instrumentation
// ============================================================
//
// ทุก HTTP request จะมี span ถูกสร้างอัตโนมัติ
// ไม่ต้องเพิ่มโค้ดใน each handler

func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract trace context จาก incoming request headers
		// ถ้า upstream service ส่ง traceparent header มา เราจะเป็น child span
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// สร้าง span สำหรับ HTTP request นี้
		spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		ctx, span := tracer.Start(ctx, spanName,
			// Span kind = SERVER (รับ request จาก outside)
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.URLFull(r.URL.String()),
				attribute.String("http.host", r.Host),
				attribute.String("net.peer.addr", r.RemoteAddr),
			),
		)
		defer span.End()

		// Inject span context กลับเข้า request
		r = r.WithContext(ctx)

		// Wrap ResponseWriter เพื่อ capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		// บันทึก status code หลัง handler ทำงาน
		span.SetAttributes(semconv.HTTPResponseStatusCode(rw.statusCode))

		// Mark span as error ถ้า status >= 500
		if rw.statusCode >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", rw.statusCode))
		}
	})
}

// ============================================================
// BUSINESS LOGIC: Manual instrumentation
// ============================================================

// Order represents a customer order
type Order struct {
	ID           string
	CustomerID   string
	CustomerTier string // "standard", "gold", "platinum"
	Items        []OrderItem
	TotalBaht    float64
}

// OrderItem represents a single item in an order
type OrderItem struct {
	ProductID string
	Name      string
	Price     float64
	Quantity  int
}

// processOrder จัดการ order พร้อม manual tracing
// แสดงให้เห็น: nested spans, attributes, events, error recording
func processOrder(ctx context.Context, order Order) error {
	// สร้าง span สำหรับ processOrder
	// ctx ที่ได้มาจาก HTTP middleware มี parent span อยู่แล้ว
	ctx, span := tracer.Start(ctx, "processOrder",
		trace.WithAttributes(
			attribute.String("order.id", order.ID),
			attribute.String("customer.id", order.CustomerID),
			attribute.String("customer.tier", order.CustomerTier),
			attribute.Int("order.item_count", len(order.Items)),
			attribute.Float64("order.total_baht", order.TotalBaht),
		),
	)
	defer span.End()

	// Event: บันทึก timestamp ของ event สำคัญ
	span.AddEvent("order validation started")

	// Step 1: Validate order
	if err := validateOrder(ctx, order); err != nil {
		// บันทึก error ใน span
		span.RecordError(err)
		span.SetStatus(codes.Error, "order validation failed")
		return fmt.Errorf("validation: %w", err)
	}
	span.AddEvent("order validation passed")

	// Step 2: Check inventory
	if err := checkInventory(ctx, order.Items); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "inventory check failed")
		return fmt.Errorf("inventory: %w", err)
	}
	span.AddEvent("inventory confirmed")

	// Step 3: Process payment
	if err := processPayment(ctx, order); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "payment processing failed")
		return fmt.Errorf("payment: %w", err)
	}
	span.AddEvent("payment completed",
		trace.WithAttributes(
			attribute.Float64("payment.amount_baht", order.TotalBaht),
			attribute.String("payment.method", "promptpay"),
		),
	)

	// Step 4: Save to database
	if err := saveOrderToDB(ctx, order); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "database save failed")
		return fmt.Errorf("database: %w", err)
	}

	span.SetStatus(codes.Ok, "order processed successfully")
	return nil
}

// validateOrder สร้าง child span สำหรับ validation
func validateOrder(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "validateOrder")
	defer span.End()

	span.SetAttributes(
		attribute.String("order.id", order.ID),
		attribute.Float64("order.total_baht", order.TotalBaht),
	)

	// จำลอง validation
	time.Sleep(time.Duration(5+rand.Intn(15)) * time.Millisecond)

	if order.TotalBaht <= 0 {
		return errors.New("order total must be positive")
	}

	if len(order.Items) == 0 {
		return errors.New("order must have at least one item")
	}

	return nil
}

// checkInventory สร้าง span สำหรับ inventory check พร้อม database query spans
func checkInventory(ctx context.Context, items []OrderItem) error {
	ctx, span := tracer.Start(ctx, "checkInventory",
		trace.WithAttributes(
			attribute.Int("inventory.check_count", len(items)),
		),
	)
	defer span.End()

	for _, item := range items {
		// สร้าง span สำหรับแต่ละ DB query
		_, dbSpan := tracer.Start(ctx, "db.query",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				// Database semantic conventions
				semconv.DBSystemNameKey.String("postgresql"),
				semconv.DBNamespace("inventory_db"),
				semconv.DBQueryText("SELECT quantity FROM inventory WHERE product_id = $1"),
				attribute.String("db.product_id", item.ProductID),
			),
		)

		// จำลอง DB query time 5-30ms
		time.Sleep(time.Duration(5+rand.Intn(25)) * time.Millisecond)

		dbSpan.SetAttributes(
			attribute.Int("db.rows_returned", 1),
			attribute.Int("inventory.available", rand.Intn(100)+10),
		)
		dbSpan.End()
	}

	return nil
}

// processPayment สร้าง span สำหรับ external payment API call
func processPayment(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "processPayment",
		trace.WithSpanKind(trace.SpanKindClient), // เรียก external service
		trace.WithAttributes(
			attribute.String("payment.gateway", "stripe"),
			attribute.Float64("payment.amount_baht", order.TotalBaht),
			attribute.String("payment.currency", "THB"),
		),
	)
	defer span.End()

	// จำลอง external API call 50-200ms
	time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)

	// 3% failure rate (จำลอง)
	if rand.Float64() < 0.03 {
		err := errors.New("payment gateway timeout")
		span.RecordError(err, trace.WithStackTrace(true)) // ส่ง stack trace ด้วย
		span.SetStatus(codes.Error, "payment gateway timeout")
		return err
	}

	span.SetAttributes(
		attribute.String("payment.transaction_id", fmt.Sprintf("TXN-%d", rand.Intn(999999))),
		attribute.String("payment.status", "approved"),
	)
	span.SetStatus(codes.Ok, "payment approved")

	return nil
}

// saveOrderToDB จำลอง database write
func saveOrderToDB(ctx context.Context, order Order) error {
	_, span := tracer.Start(ctx, "db.insert",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemNameKey.String("postgresql"),
			semconv.DBNamespace("orders_db"),
			semconv.DBQueryText("INSERT INTO orders (id, customer_id, total) VALUES ($1, $2, $3)"),
			attribute.String("db.order_id", order.ID),
		),
	)
	defer span.End()

	// จำลอง DB write 10-50ms
	time.Sleep(time.Duration(10+rand.Intn(40)) * time.Millisecond)

	span.SetAttributes(attribute.Bool("db.rows_affected", true))
	return nil
}

// ============================================================
// BAGGAGE PROPAGATION: ส่ง customer tier ข้าม services
// ============================================================
//
// Baggage ต่างจาก Span Attributes ตรงที่:
// - Span Attributes: อยู่ใน span เดียว ไม่ส่งต่อ
// - Baggage: ส่งผ่าน HTTP headers ไปทุก downstream services
//
// ใช้ Baggage เมื่อต้องการข้อมูลใน downstream spans
// เช่น: customer.tier ใน payment service, shipping service

func extractCustomerTier(ctx context.Context) string {
	bag := baggage.FromContext(ctx)
	member := bag.Member("customer.tier")
	if member.Key() == "customer.tier" {
		return member.Value()
	}
	return "standard"
}

func injectCustomerTier(ctx context.Context, tier string) context.Context {
	m, err := baggage.NewMember("customer.tier", tier)
	if err != nil {
		return ctx
	}
	bag, err := baggage.New(m)
	if err != nil {
		return ctx
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

// ============================================================
// CONTEXT PROPAGATION ข้าม goroutines
// ============================================================
//
// ⚠️  ข้อควรระวัง: context ไม่ thread-safe สำหรับ write
// แต่ trace context ใน context.Context ปลอดภัย (read-only หลัง inject)
//
// Pattern: ส่ง ctx เข้า goroutine เมื่อ spawn

func processOrdersInParallel(ctx context.Context, orders []Order) []error {
	// สร้าง parent span สำหรับ parallel processing
	ctx, span := tracer.Start(ctx, "processOrdersInParallel",
		trace.WithAttributes(
			attribute.Int("batch.size", len(orders)),
		),
	)
	defer span.End()

	errs := make([]error, len(orders))
	done := make(chan struct{})

	for i, order := range orders {
		i, order := i, order // capture loop variables

		go func() {
			// ส่ง ctx ที่มี parent span เข้า goroutine
			// goroutine จะสร้าง child span ที่ต่อจาก parent
			errs[i] = processOrder(ctx, order)
			if i == len(orders)-1 {
				close(done)
			}
		}()
	}

	<-done
	return errs
}

// ============================================================
// HTTP HANDLERS
// ============================================================

func createOrderHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ดึง customer tier จาก baggage (ถ้า upstream ส่งมา)
	customerTier := extractCustomerTier(ctx)

	// จำลอง order
	order := Order{
		ID:           fmt.Sprintf("ORD-%d", rand.Intn(99999)),
		CustomerID:   fmt.Sprintf("CUST-%d", rand.Intn(9999)),
		CustomerTier: customerTier,
		Items: []OrderItem{
			{
				ProductID: "PROD-001",
				Name:      "iPhone 15",
				Price:     35900.00,
				Quantity:  1,
			},
		},
		TotalBaht: 35900.00,
	}

	// Inject customer tier เข้า context สำหรับ downstream calls
	ctx = injectCustomerTier(ctx, order.CustomerTier)

	if err := processOrder(ctx, order); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// ดึง trace ID สำหรับ logging correlation
	spanCtx := trace.SpanFromContext(ctx).SpanContext()

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"order_id": "%s", "trace_id": "%s"}`,
		order.ID, spanCtx.TraceID().String())
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "ok"}`)
}

// ============================================================
// RESPONSE WRITER WRAPPER
// ============================================================

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	ctx := context.Background()

	// ตั้งค่า tracing
	shutdown, err := setupTracing(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup tracing: %v\n", err)
		fmt.Fprintln(os.Stderr, "Note: Make sure Jaeger is running (docker-compose up)")
		fmt.Fprintln(os.Stderr, "Continuing with no-op tracer...")
		tracer = nooptrace.NewTracerProvider().Tracer(serviceName)
	} else {
		defer shutdown(ctx)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/orders", createOrderHandler)
	mux.HandleFunc("/health", healthHandler)

	// Wrap ด้วย tracing middleware
	handler := tracingMiddleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	fmt.Printf("=== OpenTelemetry Tracing Demo ===\n")
	fmt.Printf("Server: http://localhost:%s\n", port)
	fmt.Printf("Jaeger UI: http://localhost:16686\n")
	fmt.Printf("\nTest:\n")
	fmt.Printf("  curl -X POST http://localhost:%s/orders\n", port)
	fmt.Printf("\nSpan hierarchy per request:\n")
	fmt.Printf("  POST /orders (HTTP span)\n")
	fmt.Printf("  └─ processOrder\n")
	fmt.Printf("     ├─ validateOrder\n")
	fmt.Printf("     ├─ checkInventory\n")
	fmt.Printf("     │  └─ db.query (x N items)\n")
	fmt.Printf("     ├─ processPayment (external call)\n")
	fmt.Printf("     └─ db.insert\n\n")

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
