# Module 8: Enterprise Observability — Instructor Guide

## Overview

Module 8 observability ใน production Go services ผ่าน three pillars:
**Metrics → Logs → Traces** — และที่สำคัญที่สุด: วิธีใช้ทั้งสามร่วมกัน
**Prerequisites**: Modules 1-7, Docker, basic HTTP knowledge

---

## Lab Setup (ทำก่อนเริ่ม class)

```bash
cd module8/

# Start observability stack
docker-compose up -d

# Verify services are running
docker-compose ps

# Check ports
curl http://localhost:9090/-/healthy   # Prometheus
curl http://localhost:3000/api/health  # Grafana
curl http://localhost:16686/           # Jaeger UI

# Install Go dependencies (แนะนำให้ทำล่วงหน้า)
go get github.com/prometheus/client_golang/prometheus
go get github.com/prometheus/client_golang/prometheus/promauto
go get github.com/prometheus/client_golang/prometheus/promhttp
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
go get go.opentelemetry.io/otel/sdk
go get go.opentelemetry.io/otel/sdk/resource
go get go.opentelemetry.io/otel/sdk/trace
go get go.opentelemetry.io/otel/semconv/v1.21.0
go get go.opentelemetry.io/otel/trace
go get go.opentelemetry.io/otel/baggage
go get go.opentelemetry.io/otel/propagation
```

**go.mod ต้องเพิ่ม dependencies เหล่านี้**:
```
require (
    github.com/prometheus/client_golang v1.19.1
    go.opentelemetry.io/otel v1.27.0
    go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.27.0
    go.opentelemetry.io/otel/sdk v1.27.0
    go.opentelemetry.io/otel/semconv/v1.21.0 v1.21.0
    go.opentelemetry.io/otel/trace v1.27.0
    // indirect deps will be added automatically
)
```

---

## Why Three Pillars?

### ปัญหาที่เกิดในระบบจริง

สมมติว่า alert บอกว่า "error rate > 5%"

**ถ้ามีแค่ Metrics**:
- รู้ว่ามี error — แต่ไม่รู้ว่าเกิดกับ request ไหน
- ไม่รู้ context ของ error
- ไม่รู้ว่า error เกิดตรงไหนใน code path

**ถ้ามีแค่ Logs**:
- รู้ว่า error message คืออะไร
- แต่ไม่รู้ trend — error rate เพิ่มขึ้นหรือลดลง?
- ไม่สามารถ aggregate ได้ง่าย
- ไม่รู้ว่า request ใช้เวลาเท่าไหร่ใน which component

**ถ้ามีแค่ Traces**:
- รู้ request flow ทั้งหมด
- แต่ถ้า trace sampling rate = 1% จะพลาด 99% ของ errors
- ไม่เห็น trend

**Three Pillars Together**:
```
Alert fired (metrics: error_rate > 5%)
    ↓
Grafana dashboard: error rate spike เวลา 14:35
    ↓
Loki/logs: ค้นหา level=ERROR ช่วงเวลานั้น
    ↓ พบ correlation_id และ trace_id ใน log
Jaeger: เปิด trace → เห็น payment.process span timeout
    ↓
Fix: เพิ่ม timeout หรือ circuit breaker ใน payment call
```

---

## Three Pillars — Deep Dive

### Pillar 1: Metrics (metrics/main.go)

**Core concept**: Metrics บอก "อะไร" และ "เท่าไหร่"

**Four Golden Signals (SRE Book — Google)**:

| Signal | Metric | PromQL |
|--------|--------|--------|
| Traffic | `http_requests_total` | `rate(...[5m])` |
| Latency | `http_request_duration_seconds` | `histogram_quantile(0.99, ...)` |
| Errors | `http_requests_total{status=~"5.."}` | error rate % |
| Saturation | `active_connections` | current value |

**สิ่งที่ต้องรู้**:

1. **Histogram vs Summary**
   - Histogram: buckets ที่ server, ยืดหยุ่น, ใช้ `histogram_quantile()` ใน PromQL
   - Summary: quantile ที่ client, แม่นยำกว่า แต่ **ไม่สามารถ aggregate** ข้าม instances ได้
   - **Rule**: ใช้ Histogram เสมอใน microservices (multiple instances)

2. **Cardinality Danger** — ปัญหาที่พบบ่อยมาก
   ```go
   // ผิด: user_id เป็น label = millions of time series!
   httpRequests.WithLabelValues(userID, path, status)

   // ถูก: แค่ categorical values
   httpRequests.WithLabelValues(method, normalizedPath, status)
   ```
   **Demo**: แสดง Prometheus memory ก่อน/หลัง high-cardinality labels

3. **Label Design Workshop**
   - Labels ควรมีค่าไม่เกิน ~100 unique values
   - ถูก: `method`, `path` (normalized), `status_code` (5 categories)
   - ผิด: `user_id`, `session_id`, `request_id`, `order_id`

4. **rate() vs irate()**
   - `rate()`: average rate ตลอด window (smooth, ดีสำหรับ alert)
   - `irate()`: instant rate (2 data points, ดีสำหรับ spike detection)

**Lab exercise**: เปิด Prometheus UI → ลอง PromQL queries จาก startup banner

### Pillar 2: Logging (logging/main.go)

**Core concept**: Logs บอก "อะไรเกิดขึ้น" ด้วย context

**สิ่งที่ต้องรู้**:

1. **Structured vs Unstructured**
   ```go
   // Unstructured (อย่าทำ!)
   fmt.Printf("User %d login failed at %s\n", userID, time.Now())

   // Structured (ทำแบบนี้)
   slog.Info("login failed",
       "user_id", userID,
       "timestamp", time.Now(),
       "reason", "invalid_password",
   )
   ```
   ข้อดี: ค้นหาได้, filter ได้, aggregate ได้ใน Loki/Elasticsearch

2. **Custom Handlers** — แสดง handler chain:
   ```
   SamplingHandler (10% Info)
       → CorrelationHandler (inject correlation_id)
           → MaskingHandler (redact sensitive data)
               → JSONHandler (write to stdout)
   ```

3. **Sensitive Data Masking** (สำคัญมากสำหรับ PDPA compliance)
   - Demo: log card_number → ดูผลลัพธ์ที่ถูก mask
   - สอน: ไม่ควร log sensitive data แม้แต่ในรูปแบบ encrypted

4. **Correlation ID Pattern**
   - สร้าง `X-Correlation-ID` ที่ edge
   - ส่งผ่าน context ทุก layer
   - Log ทุก request พร้อม correlation_id
   - เพิ่มเป็น span attribute ใน traces

5. **Log Levels สำหรับ Production**
   - DEBUG: dev only, ไม่เปิดใน prod (verbose มาก)
   - INFO: normal operations
   - WARN: recoverable issues, ควร investigate
   - ERROR: unrecoverable, ต้อง alert
   - Runtime level change ด้วย `slog.LevelVar` (ไม่ต้อง restart!)

**Lab exercise**:
```bash
cd logging/
go run main.go

# Terminal 2
curl http://localhost:8081/checkout
curl http://localhost:8081/error

# เปลี่ยน log level runtime
curl -X PUT 'http://localhost:8081/log-level?level=debug'
curl http://localhost:8081/checkout
```

### Pillar 3: Tracing (tracing/main.go)

**Core concept**: Traces บอก "ทำไม" และ "ที่ไหน"

**Concepts สำคัญ**:

```
Trace (trace_id: abc123)
├── Span: POST /orders (100ms total)
│   ├── Span: processOrder (95ms)
│   │   ├── Span: validateOrder (10ms) ← fast
│   │   ├── Span: checkInventory (15ms)
│   │   │   ├── Span: db.query product-1 (8ms)
│   │   │   └── Span: db.query product-2 (7ms)
│   │   ├── Span: processPayment (65ms) ← BOTTLENECK!
│   │   └── Span: db.insert (5ms)
```

**สิ่งที่ต้องรู้**:

1. **Span Attributes vs Events**
   ```go
   // Attributes: ข้อมูล static ที่ describe span
   span.SetAttributes(
       attribute.String("order.id", "ORD-123"),
       attribute.Float64("order.total_baht", 5000.00),
   )

   // Events: timestamped events ที่เกิดระหว่าง span
   span.AddEvent("payment approved",
       trace.WithAttributes(attribute.String("ref", "TXN-456")),
   )
   ```

2. **Error Recording**
   ```go
   span.RecordError(err, trace.WithStackTrace(true))
   span.SetStatus(codes.Error, err.Error())
   ```

3. **Baggage vs Span Attributes**
   - Span Attributes: อยู่ใน span นั้น ไม่ propagate ต่อ
   - Baggage: ส่งผ่าน HTTP headers ไปทุก downstream services
   - ใช้ Baggage: customer_tier, experiment_id, feature_flags

4. **Sampling Strategies**
   - `AlwaysSample`: dev/staging only (ค่าใช้จ่ายสูง)
   - `TraceIDRatioBased(0.1)`: sample 10% → ประหยัด
   - `ParentBased`: ใช้ decision ของ parent span
   - Head-based vs Tail-based sampling

**Lab exercise**:
```bash
cd tracing/
go run main.go

# Terminal 2
for i in {1..10}; do
    curl -X POST http://localhost:8082/orders
    sleep 0.5
done

# เปิด Jaeger UI
open http://localhost:16686
# ค้นหา service: order-service
# ดู flame graph ของ spans
```

---

## SLI/SLO Design Workshop

### What is SLI/SLO?

**SLI (Service Level Indicator)**: metric ที่วัด user experience  
**SLO (Service Level Objective)**: target ของ SLI  
**SLA (Service Level Agreement)**: commitment ต่อ customer (มี penalty ถ้าไม่ได้)

### Good SLI Design Principles

**ต้องวัดสิ่งที่ users สังเกตเห็นได้**:

```
ผิด: CPU usage < 80%    (users ไม่รู้สึกถึง CPU)
ถูก: p99 latency < 500ms  (users รู้สึกถึง slowness)

ผิด: memory < 4GB       (users ไม่สนใจ memory)
ถูก: error rate < 0.1%    (users สังเกตเห็น errors)
```

### Workshop: Design SLOs สำหรับ Order Service

**Exercise**: แต่ละกลุ่มออกแบบ SLIs ให้กับ endpoint เหล่านี้

| Endpoint | SLI คืออะไร? | Target SLO |
|----------|--------------|------------|
| POST /orders | ? | ? |
| GET /orders/{id} | ? | ? |
| Payment processing | ? | ? |

**เฉลย**:

```
POST /orders:
  SLI: % ของ requests ที่สำเร็จ (status 201) ภายใน 1 วินาที
  SLO: 99.5% ของ requests ต้องผ่าน SLI
  PromQL: 
    sum(rate(http_requests_total{path="/orders",status_code="201"}[5m]))
    /
    sum(rate(http_requests_total{path="/orders"}[5m]))
    >= 0.995

GET /orders/{id}:
  SLI: p99 latency < 100ms
  SLO: measure over 30-day rolling window
  PromQL:
    histogram_quantile(0.99, 
      rate(http_request_duration_seconds_bucket{path="/orders/{id}"}[30d])
    ) <= 0.1

Payment processing:
  SLI: % ของ payment attempts ที่สำเร็จ (ไม่รวม user errors)
  SLO: 99.9% success rate
```

### Error Budget Concept

```
Error Budget = 100% - SLO target
ถ้า SLO = 99.5% → Error Budget = 0.5%

ใน 30 วัน:
  Total minutes = 43,200
  Error budget = 43,200 * 0.005 = 216 minutes

ถ้า burn rate สูง (error budget หมดเร็ว):
  → หยุด deploy feature ใหม่
  → โฟกัสที่ reliability
```

---

## Common Observability Mistakes in Go Services

### 1. High Cardinality Labels

```go
// ผิด — สร้าง millions of time series
counter.With(prometheus.Labels{
    "user_id":    userID,    // ← อันตราย!
    "session_id": sessionID, // ← อันตราย!
    "trace_id":   traceID,   // ← อันตราย!
})

// ถูก
counter.With(prometheus.Labels{
    "method":  r.Method,
    "status":  strconv.Itoa(statusCode),
    "service": "order-service",
})
```

**เหตุผล**: Prometheus เก็บ time series ใน memory แบบ O(n) ต่อ label combination  
ถ้า user_id มี 1 ล้านค่า → 1 ล้าน time series → OOM crash

### 2. fmt.Println ใน Production Code

```go
// ผิด
fmt.Printf("Error processing order %s: %v\n", orderID, err)

// ถูก
logger.Error("order processing failed",
    "order_id", orderID,
    "error", err,
    "correlation_id", correlationID,
)
```

**เหตุผล**: ค้นหาใน log aggregation system ไม่ได้, ไม่มี structured fields

### 3. Logging Sensitive Data

```go
// ผิด — PDPA violation!
logger.Info("payment request",
    "card_number", cardNumber, // log ใน plain text!
    "cvv", cvv,
)

// ถูก — mask ก่อน log
logger.Info("payment request",
    "card_last4", cardNumber[len(cardNumber)-4:],
    "payment_method", "credit_card",
)
```

### 4. Missing Context Propagation

```go
// ผิด — trace ขาดตอน, ไม่เห็น chain
go func() {
    processOrder(context.Background(), order) // ← สูญเสีย trace context!
}()

// ถูก — ส่ง ctx เสมอ
go func() {
    processOrder(ctx, order) // ← trace context ต่อเนื่อง
}()
```

### 5. Missing Graceful Shutdown Metrics Flush

```go
// ผิด — spans ที่ยังไม่ export จะหาย
os.Exit(1)

// ถูก — ให้ TracerProvider flush spans ก่อน shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
tp.Shutdown(ctx) // flush remaining spans
```

### 6. alert ที่ไม่สื่อถึง user impact

```go
// ผิด — CPU alert ไม่บอกว่า users ได้รับผลกระทบ
"CPU usage > 80%"

// ถูก — alert ที่ user-facing
"p99 latency > 1s for 5 minutes"
"error rate > 1% for 2 minutes"
"order success rate < 99% for 10 minutes"
```

---

## How to Correlate: Finding a Request Across All Three Pillars

### Scenario: Customer report ว่า order ไม่สำเร็จ เวลา ~14:35

**Step 1: Metrics** → ดู Grafana dashboard

```promql
# ดู error rate ช่วง 14:30-14:40
rate(http_requests_total{status_code=~"5.."}[5m])

# ดู latency spike
histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))
```

พบ: error rate spike จาก 0.1% → 8% ช่วง 14:33-14:37

**Step 2: Logs** → ค้นหาใน Grafana Loki หรือ log file

```
level=ERROR time=14:35:22 msg="order processing failed" 
  order_id=ORD-78123 
  correlation_id=abc123def 
  trace_id=4bf92f3577b34da6a3ce929d0e0e4736
  error="payment gateway timeout"
```

พบ: correlation_id = `abc123def`, trace_id = `4bf92f3577b34da6...`

**Step 3: Traces** → ค้นหาใน Jaeger

เปิด Jaeger → ค้นหา trace_id `4bf92f3577b34da6a3ce929d0e0e4736`  
ดู flame graph:
```
POST /orders (843ms total)
└── processOrder (840ms)
    ├── validateOrder (8ms)   ✓
    ├── checkInventory (12ms)  ✓
    ├── processPayment (805ms) ← SLOW! timeout ที่ 800ms
    │   └── ERROR: payment gateway timeout
    └── (never reached db.insert)
```

**Conclusion**: payment gateway timeout → เพิ่ม retry logic หรือ fallback

---

## Request Flow Through Observability Pipeline

```
                         User Request
                              │
                              ▼
                    ┌─────────────────────┐
                    │  HTTP Server        │
                    │  (port 8080)        │
                    └─────────────────────┘
                              │
                    ┌─────────▼─────────────────────────────────┐
                    │  observabilityMiddleware                   │
                    │                                           │
                    │  1. Extract trace context (W3C header)    │
                    │  2. Start HTTP span                       │
                    │  3. Generate/extract correlation_id       │
                    │  4. Increment active_connections gauge    │
                    │  5. Log "request started" with trace_id   │
                    └─────────┬─────────────────────────────────┘
                              │ ctx with span + correlation_id
                              ▼
                    ┌─────────────────────┐
                    │  Business Handler   │
                    │  createOrderHandler │◄──── Business Logic Spans
                    └─────────────────────┘      (processOrder, db.query, etc.)
                              │
                    ┌─────────▼─────────────────────────────────┐
                    │  After response:                          │
                    │  1. Record http_requests_total (counter)  │
                    │  2. Record http_request_duration (histo)  │
                    │  3. Set span status + attributes          │
                    │  4. Log "request completed" with status   │
                    │  5. Decrement active_connections gauge    │
                    └──────────────────────────────────────────-┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
              ▼               ▼               ▼
    ┌──────────────┐  ┌─────────────┐  ┌──────────────┐
    │  Prometheus  │  │  Log file   │  │   Jaeger     │
    │  (scrapes    │  │  (stdout    │  │  (OTLP push  │
    │   /metrics)  │  │   → Loki)   │  │   port 4318) │
    └──────┬───────┘  └──────┬──────┘  └──────┬───────┘
           │                 │                 │
           ▼                 ▼                 ▼
    ┌──────────────────────────────────────────────────┐
    │                    Grafana                       │
    │   Dashboard │  Log explorer │  Trace viewer     │
    └──────────────────────────────────────────────────┘
```

---

## Alerting Design Principles

### Alerting Philosophy: Symptom-Based, Not Cause-Based

```
ผิด: "CPU > 80% for 5 minutes"
     (users ไม่สังเกตเห็น CPU)

ถูก: "p99 latency > 500ms for 5 minutes"
     (users รู้สึกทันที)

ผิด: "memory > 6GB"
     (อาจเป็น normal หลัง load spike)

ถูก: "error rate > 1% for 2 minutes"
     (users ได้รับ errors)
```

### Alert Severity Levels

| Severity | ตัวอย่าง | Action |
|----------|----------|--------|
| Critical | Error rate > 5%, service down | Wake up on-call ทันที |
| Warning | Error rate > 1%, latency p99 > 1s | Notify ทีมในเวลาทำการ |
| Info | Error rate > 0.5% | Create ticket, ดูพรุ่งนี้ |

### Avoid Alert Fatigue

**Alert fatigue**: alert เยอะเกินไปจนทีมเริ่ม ignore

Solutions:
1. Alert เฉพาะ user-facing symptoms (ไม่ใช่ internal metrics)
2. ใช้ `for` duration เพื่อกรอง transient spikes
3. Group related alerts
4. มี runbook สำหรับทุก alert (ทำอะไร ดูอะไร)

### ตัวอย่าง Prometheus Alert Rules

```yaml
# alerts.yml
groups:
  - name: order-service
    rules:
      - alert: HighErrorRate
        expr: |
          rate(http_requests_total{status_code=~"5..",job="order-service"}[5m])
          /
          rate(http_requests_total{job="order-service"}[5m])
          > 0.01
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "High error rate on order-service"
          description: "Error rate is {{ $value | humanizePercentage }}"
          runbook: "https://wiki.company.com/runbooks/order-service-errors"

      - alert: SlowOrders
        expr: |
          histogram_quantile(0.99,
            rate(http_request_duration_seconds_bucket{
              job="order-service",path="/orders"
            }[5m])
          ) > 1.0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Order creation is slow"
          description: "p99 latency is {{ $value }}s (SLO: 1s)"
```

---

## Complete Lab: End-to-End Observability

### Setup

```bash
# Terminal 1: Start observability stack
cd module8/
docker-compose up -d

# Terminal 2: Start complete service
cd complete_service/
go run main.go

# Terminal 3: Generate load
while true; do
    curl -s -X POST http://localhost:8080/orders > /dev/null
    curl -s http://localhost:8080/health > /dev/null
    sleep 0.3
done
```

### Exercises

**Exercise 1: Metrics**
1. เปิด Grafana: http://localhost:3000
2. ไปที่ Dashboard "Module 8: Four Golden Signals"
3. ดู Four Golden Signals panels ทั้งหมด
4. ตอบ: "p99 latency ของ POST /orders ตอนนี้คือเท่าไหร่?"

**Exercise 2: Logs**
1. ดู structured logs ใน terminal
2. หา correlation_id ของ request ที่ fail
3. ค้นหา log ทุก entry ที่มี correlation_id เดิม

**Exercise 3: Traces**
1. เปิด Jaeger: http://localhost:16686
2. ค้นหา service: "order-service"
3. ดู trace ที่มี error
4. ตอบ: "span ไหนที่ใช้เวลานานที่สุด?"

**Exercise 4: Correlation**
1. ดู error log ใน terminal → copy trace_id
2. ค้นหา trace_id นั้นใน Jaeger
3. ระบุ root cause ของ error

**Exercise 5: SLO Design**
1. ออกแบบ SLO สำหรับ POST /orders
2. เขียน PromQL query วัด SLO compliance
3. อธิบาย: ถ้า error budget หมด ควรทำอะไร?

---

## Key Takeaways

1. **Three Pillars ต้องใช้ร่วมกัน** — แต่ละ pillar ตอบ question ที่ต่างกัน

2. **Correlation ID เป็น glue** — ทำให้หา request ข้าม pillars ได้

3. **Alert ต้อง user-facing** — CPU/memory เป็น cause, ไม่ใช่ symptom

4. **Cardinality คือ enemy** — label design ผิดพัง Prometheus ทั้งระบบ

5. **Sampling trade-off** — log/trace ทุก request ค่าสูง, sampling ทำให้พลาด edge cases

6. **Structured logging is non-negotiable** — `fmt.Println` ใน production = ต้องแก้ทันที

7. **Graceful shutdown สำคัญ** — flush metrics/spans ก่อน terminate ไม่งั้นข้อมูลหาย

---

## Appendix: Package Imports Reference

```go
// Prometheus
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

// OpenTelemetry
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/baggage"
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
    "go.opentelemetry.io/otel/trace"
)

// Structured logging (Go standard library, 1.21+)
import "log/slog"
```
