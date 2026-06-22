// Package main สาธิต Prometheus Metrics — Four Golden Signals + Business Metrics
// รัน: go run main.go
// เปิด browser: http://localhost:8080/metrics
// Scrape ด้วย Prometheus: http://localhost:8080/metrics
package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ============================================================
// FOUR GOLDEN SIGNALS (SRE Bible — Google)
// ============================================================
//
// 1. Latency   — ใช้เวลานานแค่ไหนในการตอบ request?
// 2. Traffic   — มี request เข้ามากี่ครั้งต่อวินาที?
// 3. Errors    — กี่ % ของ request ที่ fail?
// 4. Saturation — resource ถูกใช้งานเต็มแค่ไหน? (CPU, Memory, Connections)
//
// ถ้า alert ถูกต้อง ทั้ง 4 signals นี้ครอบคลุม symptom ทุกอย่าง
// ที่ users สามารถสังเกตเห็นได้

// ============================================================
// PROMETHEUS METRIC TYPES — เลือกให้ถูก!
// ============================================================
//
// Counter   — นับ monotonically increasing (เช่น requests, errors)
//             ดู rate() ใน PromQL: rate(http_requests_total[5m])
//
// Gauge     — ค่าที่ขึ้นลงได้ (เช่น connections, memory usage)
//             ดูค่าปัจจุบัน: active_connections
//
// Histogram — กระจาย latency เป็น buckets (เช่น <=0.1s, <=0.5s, <=1s)
//             ดู percentile: histogram_quantile(0.99, ...)
//             ** แนะนำ: ใช้ Histogram แทน Summary สำหรับ distributed systems
//
// Summary   — คล้าย Histogram แต่คำนวณ quantile ที่ client
//             ข้อเสีย: ไม่สามารถ aggregate หลาย instance ได้!
//             ใช้เมื่อ: ต้องการ quantile แม่นยำ, single instance only

var (
	// ============================================================
	// SIGNAL 1: Traffic — นับ HTTP requests ทั้งหมด
	// ============================================================
	// Labels: method, path, status_code
	//
	// ⚠️  CARDINALITY DANGER: อย่าใส่ user_id, session_id เป็น label!
	// ถ้า label cardinality สูง Prometheus memory จะพุ่งขึ้นมาก
	// Rule of thumb: label value ควรมีไม่เกิน ~100 ค่า
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status_code"},
	)

	// ============================================================
	// SIGNAL 2: Latency — กระจาย request duration
	// ============================================================
	// Histogram buckets: เลือก buckets ให้สอดคล้องกับ SLO
	// ถ้า SLO = p99 < 500ms ควรมี bucket ที่ 0.5 ด้วย
	//
	// Histogram vs Summary:
	// - Histogram: buckets อยู่ที่ server, ยืดหยุ่น, aggregate ได้
	// - Summary:   quantile อยู่ที่ client, แม่นยำกว่า แต่ aggregate ไม่ได้
	// → ใช้ Histogram เสมอใน microservices!
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "HTTP request duration in seconds",
			// Buckets ออกแบบสำหรับ web service
			// คิดว่า: 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s, 10s
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"method", "path"},
	)

	// ============================================================
	// SIGNAL 3: Errors — Error rate ดูจาก httpRequestsTotal
	// (status_code="5xx") / httpRequestsTotal ทั้งหมด
	// ไม่จำเป็นต้องมี metric แยก ใช้ PromQL แทน:
	//   rate(http_requests_total{status_code=~"5.."}[5m])
	//   /
	//   rate(http_requests_total[5m])
	// ============================================================

	// ============================================================
	// SIGNAL 4: Saturation — Active connections (Gauge)
	// ============================================================
	activeConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_connections",
			Help: "Number of currently active HTTP connections",
		},
	)

	// ============================================================
	// BUSINESS METRICS — สิ่งที่ engineering + business care about
	// ============================================================

	// นับ orders ที่สำเร็จ — business KPI
	ordersPlacedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orders_placed_total",
			Help: "Total number of orders successfully placed",
		},
		[]string{"product_category", "payment_method"},
	)

	// กระจาย order value — ใช้ histogram เพราะต้องการ percentile
	// ช่วยตอบว่า: "p50 order value คือเท่าไหร่?"
	orderValueBaht = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name: "order_value_baht",
			Help: "Order value in Thai Baht (THB)",
			// Buckets สำหรับ Thai e-commerce context
			// ≤100฿, ≤500฿, ≤1000฿, ≤2500฿, ≤5000฿, ≤10000฿, ≤25000฿, ≤50000฿
			Buckets: []float64{100, 500, 1000, 2500, 5000, 10000, 25000, 50000},
		},
	)

	// Queue depth — เตือนว่า system เริ่ม overloaded
	orderQueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "order_queue_depth",
			Help: "Number of orders waiting to be processed",
		},
	)
)

// ============================================================
// MIDDLEWARE: บันทึก metrics อัตโนมัติทุก request
// ============================================================

// metricsMiddleware wraps handler และบันทึก Four Golden Signals
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// เพิ่ม active connections
		activeConnections.Inc()
		defer activeConnections.Dec()

		// Wrap ResponseWriter เพื่อ capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// บันทึก start time
		start := time.Now()

		// เรียก handler จริง
		next.ServeHTTP(rw, r)

		// บันทึก duration และ request count หลัง handler ทำงานเสร็จ
		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(rw.statusCode)

		// Normalize path เพื่อป้องกัน cardinality explosion
		// ถ้าไม่ normalize: /users/1, /users/2, ... = infinite labels!
		path := normalizePath(r.URL.Path)

		httpRequestsTotal.WithLabelValues(r.Method, path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
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

// normalizePath แปลง dynamic paths เพื่อป้องกัน high cardinality
// /users/123 → /users/{id}
// /products/456 → /products/{id}
func normalizePath(path string) string {
	// ในระบบจริงควรใช้ router ที่รองรับ path pattern matching
	// หรือใช้ library เช่น chi ที่มี RouteContext
	pathMap := map[string]string{
		"/orders":  "/orders",
		"/metrics": "/metrics",
		"/health":  "/health",
	}
	if normalized, ok := pathMap[path]; ok {
		return normalized
	}
	// ถ้า path ไม่รู้จัก ให้ return /other
	// ป้องกัน: /very/long/unknown/path/12345 ทำให้ cardinality สูง
	return "/other"
}

// ============================================================
// DEMO HANDLERS — จำลอง traffic จริง
// ============================================================

// ordersHandler จำลองการสร้าง order พร้อม latency แบบสุ่ม
func ordersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// จำลอง processing time 10-300ms (แบบสุ่ม)
	latency := time.Duration(10+rand.Intn(290)) * time.Millisecond
	time.Sleep(latency)

	// จำลอง error 5% ของ requests
	if rand.Float64() < 0.05 {
		http.Error(w, "Internal server error (simulated)", http.StatusInternalServerError)
		return
	}

	// บันทึก business metrics
	categories := []string{"electronics", "clothing", "food", "books"}
	payments := []string{"credit_card", "promptpay", "bank_transfer"}

	category := categories[rand.Intn(len(categories))]
	payment := payments[rand.Intn(len(payments))]
	value := 100.0 + rand.Float64()*9900.0 // 100-10000 บาท

	ordersPlacedTotal.WithLabelValues(category, payment).Inc()
	orderValueBaht.Observe(value)

	// จำลอง queue depth เปลี่ยนแปลง
	orderQueueDepth.Set(float64(rand.Intn(50)))

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"order_id": "%d", "value": %.2f, "category": "%s"}`,
		rand.Intn(100000), value, category)
}

// healthHandler บอกว่า service ยังทำงานอยู่
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "ok", "time": "%s"}`, time.Now().Format(time.RFC3339))
}

// ============================================================
// TRAFFIC SIMULATOR — สร้าง synthetic load สำหรับ demo
// ============================================================

// simulateTraffic ส่ง requests ไปยัง server ตัวเองเพื่อสร้าง metrics
func simulateTraffic() {
	client := &http.Client{Timeout: 5 * time.Second}

	go func() {
		// รอให้ server พร้อม
		time.Sleep(1 * time.Second)

		fmt.Println("Traffic simulator started — generating synthetic load...")

		for {
			// POST /orders ทุก 200-500ms
			go func() {
				resp, err := client.Post("http://localhost:8080/orders",
					"application/json",
					nil)
				if err == nil {
					resp.Body.Close()
				}
			}()

			// GET /health ทุก request
			go func() {
				resp, err := client.Get("http://localhost:8080/health")
				if err == nil {
					resp.Body.Close()
				}
			}()

			time.Sleep(time.Duration(200+rand.Intn(300)) * time.Millisecond)
		}
	}()
}

// ============================================================
// MAIN — เริ่ม HTTP server
// ============================================================

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	// Business endpoints
	mux.HandleFunc("/orders", ordersHandler)
	mux.HandleFunc("/health", healthHandler)

	// Prometheus scrape endpoint
	// Prometheus จะ GET /metrics ทุก scrape interval (default 15s)
	mux.Handle("/metrics", promhttp.Handler())

	// Wrap ทุก handler ด้วย metrics middleware
	handler := metricsMiddleware(mux)

	// เริ่ม traffic simulator (สำหรับ demo)
	simulateTraffic()

	fmt.Printf("=== Prometheus Metrics Demo ===\n")
	fmt.Printf("Server: http://localhost:%s\n", port)
	fmt.Printf("Metrics: http://localhost:%s/metrics\n", port)
	fmt.Printf("\nPromQL examples:\n")
	fmt.Printf("  Traffic:   rate(http_requests_total[5m])\n")
	fmt.Printf("  Latency:   histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))\n")
	fmt.Printf("  Error rate: rate(http_requests_total{status_code=~\"5..\"}[5m]) / rate(http_requests_total[5m])\n")
	fmt.Printf("  Connections: active_connections\n")
	fmt.Printf("\nBusiness metrics:\n")
	fmt.Printf("  Orders/min: rate(orders_placed_total[1m]) * 60\n")
	fmt.Printf("  Revenue/min: rate(order_value_baht_sum[1m]) * 60\n")
	fmt.Printf("  p99 order value: histogram_quantile(0.99, rate(order_value_baht_bucket[5m]))\n\n")

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
