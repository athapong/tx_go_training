# Module 5: Microservices Architecture with Go
---

## ขั้นตอนการติดตั้ง Dependencies (ทำก่อนเริ่ม Lab)

เนื่องจาก module5 ใช้ external packages ให้รันคำสั่งต่อไปนี้จาก root ของ project:

```bash
# gRPC core
go get google.golang.org/grpc@v1.68.0

# gRPC health check protocol
go get google.golang.org/grpc/health@v1.68.0

# Protocol Buffers runtime
go get google.golang.org/protobuf@v1.36.0

# Circuit Breaker (ใช้ใน grpc_demo/client และ resilience)
go get github.com/sony/gobreaker@v0.5.0

# tidy
go mod tidy
```

หลังจาก go mod tidy แล้ว go.mod จะมีลักษณะดังนี้:
```
module tx_go_training

go 1.23

require (
    github.com/sony/gobreaker v0.5.0
    google.golang.org/grpc v1.68.0
    google.golang.org/protobuf v1.36.0
)
```

### วิธีรัน Proto Compilation (สำหรับ production code)

ไฟล์ `module5/proto/orderpb/order.go` เขียนด้วยมือเพื่อการสอน
ในโปรดักชัน ให้ generate จาก proto file จริง:

```bash
# ติดตั้ง tools
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

mkdir -p module5/proto/google/rpc
curl -sSL https://raw.githubusercontent.com/googleapis/googleapis/master/google/rpc/status.proto -o module5/proto/google/rpc/status.proto
curl -sSL https://raw.githubusercontent.com/googleapis/googleapis/master/google/rpc/error_details.proto -o module5/proto/google/rpc/error_details.proto
# Generate (จาก project root)
protoc \
    --proto_path=module5/proto \
    --go_out=. \
    --go_opt=module=tx_go_training \
    --go-grpc_out=. \
    --go-grpc_opt=module=tx_go_training \
    module5/proto/order.proto
```

Output: `module5/proto/orderpb/order.pb.go` + `order_grpc.pb.go`

---

## gRPC vs REST Decision Matrix

| Criteria | gRPC | REST/JSON |
|----------|------|-----------|
| **Performance** | ดีกว่ามาก (binary Protobuf, HTTP/2) | ช้ากว่า (text JSON, HTTP/1.1) |
| **Streaming** | Built-in (4 patterns) | SSE / WebSocket (add-on) |
| **Type Safety** | Strong (proto schema) | อ่อนแอ (JSON schema optional) |
| **Browser Support** | ต้องใช้ grpc-web proxy | Native |
| **Tooling** | grpcurl, Postman (newer) | ดีกว่ามาก (Postman, curl, Swagger) |
| **Learning Curve** | สูง (proto, codegen, interceptors) | ต่ำ (ทุกคนรู้จัก HTTP) |
| **Debugging** | ยากกว่า (binary format) | ง่าย (อ่าน JSON ได้เลย) |
| **Service-to-Service** | เหมาะมาก | ใช้ได้ แต่ overhead สูงกว่า |
| **Public API** | ไม่เหมาะ | เหมาะ |
| **Versioning** | Proto field numbers (backward compat) | URL versioning (/v1/, /v2/) |

**Rule of Thumb:**
- Internal microservices → gRPC
- Public API / Mobile / Browser → REST
- Event streaming → Kafka/NATS + gRPC streams
- Simple CRUD → REST ยังดีกว่า

---

## Common gRPC Mistakes

### 1. ไม่ตั้ง Deadline/Timeout

```go
// ❌ BAD: ไม่มี deadline → request แขวนค้างตลอดกาล
resp, err := client.GetOrder(context.Background(), req)

// ✅ GOOD: ตั้ง deadline ทุกครั้ง
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
resp, err := client.GetOrder(ctx, req)
```

**เหตุผล:** gRPC ไม่มี default timeout ถ้า server ไม่ตอบ client จะ block ตลอดไป

### 2. ไม่ Handle gRPC Status Codes อย่างถูกต้อง

```go
// ❌ BAD: ตรวจสอบแค่ว่ามี error หรือไม่
if err != nil {
    return err
}

// ✅ GOOD: ดู status code เพื่อ handle ต่างกัน
if err != nil {
    st, _ := status.FromError(err)
    switch st.Code() {
    case codes.NotFound:
        return ErrOrderNotFound
    case codes.ResourceExhausted:
        // retry with backoff
    case codes.Unauthenticated:
        // refresh token
    case codes.DeadlineExceeded:
        // timeout — log and alert
    }
}
```

### 3. ไม่ Close Server Stream เมื่อ Client Disconnect

```go
// ❌ BAD: ไม่ตรวจสอบ context
func (s *Server) ShipOrder(req *pb.ShipOrderRequest, stream pb.OrderService_ShipOrderServer) error {
    for _, update := range updates {
        stream.Send(update)  // จะ error ถ้า client disconnect แต่ไม่ตรวจ
    }
}

// ✅ GOOD: ตรวจสอบ context.Done()
func (s *Server) ShipOrder(req *pb.ShipOrderRequest, stream pb.OrderService_ShipOrderServer) error {
    for _, update := range updates {
        select {
        case <-stream.Context().Done():
            return nil  // client disconnect → clean up
        default:
        }
        if err := stream.Send(update); err != nil {
            return err
        }
    }
    return nil
}
```

### 4. Interceptor ลำดับผิด

```
// ❌ BAD: metrics อยู่ก่อน auth → นับ unauthenticated requests ด้วย
grpc.ChainUnaryInterceptor(metricsInterceptor, authInterceptor, loggingInterceptor)

// ✅ GOOD: logging → auth → metrics
grpc.ChainUnaryInterceptor(loggingInterceptor, authInterceptor, metricsInterceptor)
```

**เหตุผล:**
- Logging อยู่นอกสุด: log ทุก request รวมถึง unauthenticated
- Auth กรองก่อน metrics: metrics นับเฉพาะ valid requests

### 5. ใช้ grpc.Dial() แทน grpc.NewClient() (deprecated)

```go
// ❌ BAD (deprecated in gRPC v1.68+)
conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(...))

// ✅ GOOD
conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(...))
```

---

## Saga Pattern Pitfalls

### 1. Compensating Actions ต้อง Idempotent

```go
// ❌ BAD: Compensate ทำซ้ำไม่ได้ (refund 2 ครั้ง = คืนเงินเกิน)
func refund(paymentRef string) error {
    return chargeGateway.Refund(paymentRef)  // ถ้ารันสองครั้ง = refund 2 ครั้ง!
}

// ✅ GOOD: ตรวจสอบ state ก่อน
func refund(paymentRef string) error {
    payment, _ := db.GetPayment(paymentRef)
    if payment.Status == "REFUNDED" {
        return nil  // already refunded, idempotent
    }
    return chargeGateway.Refund(paymentRef)
}
```

### 2. Compensation Order ต้อง LIFO

```
Forward:     Reserve → Charge → Confirm
Compensate:  Cancel  → Refund → Release  ← LIFO (inverse order)

ถ้าทำ FIFO (wrong order):
  Release (inventory) ก่อน → แต่ payment ยังถูกเรียก
  → สต็อกอาจถูก reserve โดยคนอื่นก่อนที่เราจะ refund
```

### 3. Saga ไม่ใช่ ACID Transaction

```
ระหว่าง saga execution:
  - ลูกค้าอาจเห็น Order อยู่ใน PENDING state
  - สต็อกอาจถูก reserve แต่ payment ยังไม่ถูกเรียก
  - นี่คือ "dirty read" / intermediate state

วิธีแก้:
  - Semantic Lock: ล็อก Order ด้วย state เช่น PENDING_PAYMENT
  - Version field: ใช้ optimistic locking
  - UI: แสดงสถานะที่ถูกต้อง ("กำลังดำเนินการ...")
```

### 4. Outbox Pattern สำหรับ Reliable Events

```
ปัญหา: ถ้า service crash หลัง save DB แต่ก่อน publish event → event หาย
แก้ด้วย Outbox Pattern:

BEGIN TRANSACTION
  INSERT INTO orders (...)
  INSERT INTO outbox (event_type, payload, ...)  ← save event ในธุรกรรมเดียวกัน
COMMIT

// Background worker: อ่าน outbox แล้ว publish ไปยัง message broker
```

---

## ASCII Diagrams

### Circuit Breaker State Machine

```
                    failure_count >= threshold
   ┌─────────┐     ─────────────────────────►    ┌──────────┐
   │  CLOSED  │                                   │   OPEN   │
   │ (normal) │◄─────────────────────────────────│ (blocked)│
   └─────────┘     success_count >= threshold     └──────────┘
        ▲                                               │
        │                                               │ timeout expires
        │           ┌──────────────┐                   │
        └───────────│  HALF-OPEN   │◄──────────────────┘
     probe success  │  (testing)   │
                    └──────────────┘
                          │
                          │ probe fails
                          ▼
                    ┌──────────┐
                    │   OPEN   │  (reset timer)
                    └──────────┘

State Transitions:
  CLOSED → OPEN:      consecutive_failures >= failureThreshold
  OPEN → HALF-OPEN:   openTimeout expires
  HALF-OPEN → CLOSED: consecutive_successes >= successThreshold
  HALF-OPEN → OPEN:   any failure during probe
```

### Saga Compensation Flow

```
  PlaceOrder Saga — Happy Path:
  ───────────────────────────────────────────────
  Step 1: ReserveInventory  ──►  SUCCESS  ──►  sc.ReservationID = "RES-123"
  Step 2: ChargePayment     ──►  SUCCESS  ──►  sc.PaymentRef = "PAY-456"
  Step 3: ConfirmOrder      ──►  SUCCESS  ──►  sc.Confirmed = true
                                               Order Status: CONFIRMED ✓

  PlaceOrder Saga — Payment Failure:
  ───────────────────────────────────────────────
  Step 1: ReserveInventory  ──►  SUCCESS  ──►  sc.ReservationID = "RES-123"
  Step 2: ChargePayment     ──►  FAILED   ──►  "payment declined"
                                │
                                ▼ COMPENSATION (LIFO)
  Compensate Step 1: ReleaseInventory("RES-123")  ──►  SUCCESS
                                │
                                ▼
                         Order Status: CANCELLED
                         System in consistent state ✓

  Compensation Detail (LIFO Order):
  ───────────────────────────────────────────────
  Forward:     [1. Reserve]  [2. Charge]  [3. Confirm]
                    ↓             ↓             ↓
  Executed:      RES-123       PAY-456        N/A (failed here)
                    ↑             ↑
  Compensate:  Release(RES-123) ← Refund(PAY-456)
               (step 1)           (step 2, runs first LIFO)
```

### gRPC Interceptor Chain

```
  Client Request
       │
       ▼
  ┌─────────────────────────────────────┐
  │         gRPC Server                 │
  │                                     │
  │  ┌─────────────────────────────┐    │
  │  │  loggingInterceptor         │    │
  │  │  (log start + end)          │    │
  │  │  ┌─────────────────────┐   │    │
  │  │  │  authInterceptor    │   │    │
  │  │  │  (verify token)     │   │    │
  │  │  │  ┌───────────────┐  │   │    │
  │  │  │  │ metricsInter  │  │   │    │
  │  │  │  │ (count+timer) │  │   │    │
  │  │  │  │ ┌───────────┐ │  │   │    │
  │  │  │  │ │  handler  │ │  │   │    │
  │  │  │  │ │ (business │ │  │   │    │
  │  │  │  │ │  logic)   │ │  │   │    │
  │  │  │  │ └───────────┘ │  │   │    │
  │  │  │  └───────────────┘  │   │    │
  │  │  └─────────────────────┘   │    │
  │  └─────────────────────────────┘   │
  │                                     │
  └─────────────────────────────────────┘
       │
       ▼
  Client Response
```

---

## Lab Timing & Checkpoints

### Session 1: gRPC Fundamentals

| เวลา | กิจกรรม | Checkpoint |
|------|---------|-----------|
| 0-10 | อธิบาย proto file + แนวคิด gRPC | อ่าน order.proto ได้ |
| 10-20 | Walk through server/main.go | เข้าใจ interceptor chain |
| 20-35 | Walk through client/main.go | เข้าใจ retry policy + circuit breaker |
| 35-45 | รัน demo + ทดสอบด้วย grpcurl | ส่ง request ผ่านได้ |

**Checkpoint คำถาม:**
1. ทำไม gRPC จึงใช้ Protocol Buffers แทน JSON?
2. ความแตกต่างระหว่าง Unary และ Server Streaming RPC คืออะไร?
3. Interceptor รันในลำดับอะไร? ทำไมลำดับจึงสำคัญ?

### Session 2: Saga Pattern

| เวลา | กิจกรรม | Checkpoint |
|------|---------|-----------|
| 0-10 | อธิบาย Distributed Transaction problem | เข้าใจว่าทำไม 2PC ไม่ work |
| 10-20 | Walk through saga/main.go | เข้าใจ forward + compensation |
| 20-35 | รัน demo หลายรอบ สังเกต compensation | เห็น output ของ compensation |
| 35-40 | อภิปราย choreography vs orchestration | เปรียบเทียบ tradeoffs |

**Checkpoint คำถาม:**
1. ทำไม compensation ต้องทำใน LIFO order?
2. Idempotency ใน compensation หมายความว่าอะไร?
3. เมื่อไหร่ควรใช้ Choreography vs Orchestration?

### Session 3: Resilience Patterns

| เวลา | กิจกรรม | Checkpoint |
|------|---------|-----------|
| 0-10 | อธิบาย Circuit Breaker state machine | วาด state machine ได้ |
| 10-15 | อธิบาย Retry + Jitter | เข้าใจว่า jitter ช่วยอะไร |
| 15-20 | อธิบาย Bulkhead + Rate Limiter | เข้าใจ failure isolation |
| 20-35 | Walk through resilience/main.go | เข้าใจ layered composition |
| 35-45 | รัน demo + สังเกต output | เห็น circuit state changes |

**Checkpoint คำถาม:**
1. Circuit Breaker OPEN state ป้องกันอะไร? ต่างจาก timeout อย่างไร?
2. Full Jitter คำนวณอย่างไร? ทำไมต้องมี jitter?
3. Bulkhead ต่างจาก Circuit Breaker อย่างไร?

---

## Lab Exercises

### Exercise 1: เพิ่ม Method ใน OrderService
เพิ่ม `CancelOrder` RPC ใน proto และ implement ใน server
- Unary RPC
- ตรวจสอบว่า Order อยู่ใน state ที่ cancel ได้
- เพิ่ม compensating action ใน saga

### Exercise 2: Custom Interceptor
สร้าง interceptor ใหม่ที่:
- ตรวจสอบ request payload size (reject ถ้า > 1MB)
- เพิ่มเข้าใน interceptor chain ในตำแหน่งที่เหมาะสม

### Exercise 3: Saga ด้วย Choreography
แปลง OrderSaga จาก Orchestration เป็น Choreography:
- ใช้ channel แทน direct function calls
- แต่ละ "service" subscribe event และ publish event ต่อ
- สังเกต complexity ที่เพิ่มขึ้น

### Exercise 4: Circuit Breaker Metrics
เพิ่ม metrics ใน CircuitBreaker:
- นับจำนวน times ที่ circuit เปิด/ปิด
- บันทึก timestamp ล่าสุดของแต่ละ state change
- แสดง summary เมื่อ program จบ

---

## Production Considerations

### gRPC
- ใช้ mTLS สำหรับ service-to-service authentication
- ใช้ `google.golang.org/grpc/keepalive` ป้องกัน idle connection drop
- Monitor: `grpc_server_handled_total`, `grpc_server_handling_seconds`
- ใช้ `grpc-ecosystem/go-grpc-prometheus` สำหรับ Prometheus metrics

### Saga
- บันทึก saga state ลง database (durable) ไม่ใช่ in-memory
- ใช้ Outbox Pattern สำหรับ reliable event publishing
- Monitor: saga completion rate, compensation rate, time-to-complete

### Circuit Breaker
- ใช้ library ที่ battle-tested เช่น `gobreaker`, `hystrix-go`, `go-resilience`
- ตั้ง threshold จาก production metrics (p99 latency, error rate SLA)
- Alert เมื่อ circuit เปิด (PagerDuty/Slack)

### Resilience ทั่วไป
- Chaos Engineering: ทดสอบ resilience โดย inject failures ด้วย Chaos Monkey
- Load Testing: ใช้ k6 หรือ Gatling ทดสอบ bulkhead/rate limiter
- Observability: Distributed Tracing ด้วย OpenTelemetry + Jaeger

---

## File Structure Summary

```
module5/
├── proto/
│   ├── order.proto                    # Proto definition (source of truth)
│   └── orderpb/
│       └── order.go                   # Hand-written types (แทน protoc output)
├── grpc_demo/
│   ├── server/
│   │   └── main.go                    # OrderService server + interceptors
│   └── client/
│       └── main.go                    # Resilient gRPC client
├── saga/
│   └── main.go                        # PlaceOrder orchestration saga
├── resilience/
│   └── main.go                        # CB + Retry + Bulkhead + RateLimiter
└── MODULE5_NOTES.md                   # This file
```
