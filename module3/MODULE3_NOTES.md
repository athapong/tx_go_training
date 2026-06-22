# Module 3: Enterprise Architecture Patterns

## Core Concepts

### Domain-Driven Design (DDD) ใน Go

```
โครงสร้าง Package ที่แนะนำ:

myservice/
├── domain/              ← Pure business logic (ไม่ import อะไรจากข้างนอก)
│   ├── order.go         ← Aggregate Root
│   ├── repository.go    ← Repository Interface (port)
│   └── order_test.go    ← Unit tests
├── application/         ← Use cases (orchestrate domain)
│   ├── commands.go      ← Command handlers
│   └── queries.go       ← Query handlers
├── infrastructure/      ← Adapters (database, messaging)
│   ├── postgres_repo.go ← PostgreSQL implementation
│   └── kafka_bus.go     ← Kafka event bus
└── transport/           ← Delivery mechanism
    ├── grpc/
    └── http/

Dependencies:
  transport → application → domain
  infrastructure → domain (implements interfaces)
  
  domain ไม่รู้จัก infrastructure เลย!
```

### Aggregate คืออะไร?

```
Aggregate = cluster ของ objects ที่เปลี่ยน state ด้วยกัน

Order Aggregate:
  ┌─────────────────────────────────┐
  │ Order (Aggregate Root)          │
  │   - id: OrderID                 │
  │   - status: OrderStatus         │
  │   ┌─────────────────────────┐   │
  │   │ OrderItem (Value Object)│   │
  │   │ OrderItem (Value Object)│   │
  │   └─────────────────────────┘   │
  └─────────────────────────────────┘
  
  Rule: เข้าถึง OrderItem ผ่าน Order เท่านั้น
  ไม่ควรมี OrderItemRepository แยก!
```

### Value Object vs Entity

```
Value Object:
  - ไม่มี identity
  - Immutable
  - เปรียบเทียบด้วย value
  - ตัวอย่าง: Money, Address, Email

  Money{100, "THB"} == Money{100, "THB"} // true (same value)

Entity:
  - มี unique identity
  - Mutable (state เปลี่ยนได้)
  - เปรียบเทียบด้วย ID
  - ตัวอย่าง: Order, Customer, Product

  Order{id:"001"} == Order{id:"001"} // true (same ID)
  แม้ว่า status จะต่างกัน
```

### CQRS Pattern

```
Command (Write):
  Request → CommandHandler → Aggregate → Repository → Events
  
Query (Read):
  Request → QueryHandler → Repository/ReadModel → DTO → Response

ข้อดี:
  - Scale read/write independently
  - Read model optimized สำหรับ query
  - Write model optimized สำหรับ business rules
```

### Event Sourcing (Advanced)

```
แทนที่จะเก็บ current state → เก็บ events ทั้งหมด

Traditional:
  orders table: {id, status, total, ...}
  UPDATE orders SET status='CONFIRMED' WHERE id='001'

Event Sourcing:
  events table: {id, aggregate_id, type, data, version}
  INSERT events VALUES (1, '001', 'order.placed', {...}, 1)
  INSERT events VALUES (2, '001', 'order.confirmed', {...}, 2)
  
  สร้าง current state โดย replay events:
  events → apply one by one → current state

ข้อดี:
  - Full audit trail
  - Time travel (ดูสถานะเมื่อ t ที่แล้ว)
  - Event-driven integration
  
ข้อเสีย:
  - ซับซ้อนกว่า
  - ต้องจัดการ schema evolution ที่มากกว่า traditional schema
```

---

## Hexagonal Architecture (Ports & Adapters)

### แนวคิดหลัก

```
ปัญหาของ Layered Architecture แบบเก่า:
  HTTP Handler → Service → Repository → Database
  
  ปัญหา: Service รู้จัก HTTP และ Database → ทดสอบยาก
  เปลี่ยน Database? → แก้ทั้ง Service
  เปลี่ยน HTTP เป็น gRPC? → แก้ทั้ง Service

Hexagonal Architecture แก้:
  ทุกอย่างเชื่อมกับ Core ผ่าน Port (interface)
  Adapter แปลง external format → domain format
```

### โครงสร้าง Hexagon

```
                  ┌─────────────────────────────┐
   [HTTP]         │                             │         [PostgreSQL]
   [gRPC]  ──────►│  Driving Adapters           │◄─────── [MySQL]
   [CLI]          │  (Primary / Left side)      │         [DynamoDB]
                  │                             │
                  │  ┌───────────────────────┐  │
                  │  │   APPLICATION CORE    │  │
                  │  │                       │  │
                  │  │   Ports (Interfaces): │  │
                  │  │   - OrderPort (in)    │  │
                  │  │   - RepoPort (out)    │  │
                  │  │   - EventPort (out)   │  │
                  │  └───────────────────────┘  │
                  │                             │
                  │  Driven Adapters            │
                  │  (Secondary / Right side)   │◄─────── [Kafka]
                  │                             │         [Email]
                  └─────────────────────────────┘
```

### Ports — Interface กำหนดใน Core

```go
// package domain หรือ application

// Driving Port (Primary) — คนภายนอกเรียกเข้ามา
type OrderService interface {
    PlaceOrder(ctx context.Context, cmd PlaceOrderCommand) (OrderID, error)
    GetOrder(ctx context.Context, id OrderID) (*Order, error)
    CancelOrder(ctx context.Context, id OrderID, reason string) error
}

// Driven Port (Secondary) — Core เรียกออกไปข้างนอก
type OrderRepository interface {
    Save(ctx context.Context, order *Order) error
    FindByID(ctx context.Context, id OrderID) (*Order, error)
    FindByCustomer(ctx context.Context, customerID string) ([]*Order, error)
}

type EventPublisher interface {
    Publish(ctx context.Context, events ...DomainEvent) error
}

type PaymentGateway interface {
    Charge(ctx context.Context, amount Money, token string) (string, error)
    Refund(ctx context.Context, paymentRef string) error
}
```

### Adapters — Implement Ports

```go
// Driving Adapter: HTTP Handler implement ไม่มี port
// แต่เรียก OrderService (Driving Port)
type HTTPOrderHandler struct {
    svc OrderService // inject via constructor
}

func (h *HTTPOrderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    var req PlaceOrderRequest
    json.NewDecoder(r.Body).Decode(&req)
    
    id, err := h.svc.PlaceOrder(r.Context(), req.toCommand())
    // HTTP-specific: encode response
    json.NewEncoder(w).Encode(PlaceOrderResponse{OrderID: id.String()})
}

// Driven Adapter: PostgreSQL implement OrderRepository
type PostgresOrderRepo struct {
    db *sql.DB
}

func (r *PostgresOrderRepo) FindByID(ctx context.Context, id OrderID) (*Order, error) {
    row := r.db.QueryRowContext(ctx,
        "SELECT id, status, total FROM orders WHERE id = $1", id)
    // PostgreSQL-specific: scan, map to domain
    var o Order
    row.Scan(&o.id, &o.status, &o.total)
    return &o, nil
}

// Driven Adapter: In-Memory สำหรับ testing
type InMemoryOrderRepo struct {
    mu    sync.RWMutex
    store map[OrderID]*Order
}

func (r *InMemoryOrderRepo) FindByID(_ context.Context, id OrderID) (*Order, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    o, ok := r.store[id]
    if !ok {
        return nil, ErrOrderNotFound
    }
    clone := *o
    return &clone, nil
}
```

### ข้อดีที่จับต้องได้

```
1. เปลี่ยน Database โดยไม่แตะ Core:
   PostgresOrderRepo → DynamoDBOrderRepo
   แค่สลับใน dependency injection

2. ทดสอบ Core โดยไม่ต้อง Docker:
   ใช้ InMemoryOrderRepo แทน PostgresOrderRepo
   Core business logic test ทำงานใน microseconds

3. เพิ่ม transport ใหม่ไม่กระทบ Core:
   HTTP handler → เพิ่ม gRPC handler อีกตัว
   ทั้งสองเรียก OrderService interface เดียวกัน

4. Test Driven Adapter แยกอิสระ:
   TestPostgresOrderRepo: integration test กับ real DB
   TestHTTPHandler: unit test ด้วย mock OrderService
```

### Wiring (Dependency Injection ที่ main.go)

```go
func main() {
    // Infrastructure
    db := connectDB()
    
    // Driven Adapters (right side)
    orderRepo := postgres.NewOrderRepo(db)
    eventPub  := kafka.NewPublisher(kafkaBroker)
    payment   := stripe.NewGateway(stripeKey)
    
    // Application Core
    orderSvc := application.NewOrderService(orderRepo, eventPub, payment)
    
    // Driving Adapters (left side)
    httpHandler := httpAdapter.NewOrderHandler(orderSvc)
    grpcServer  := grpcAdapter.NewOrderServer(orderSvc)
    
    // Serve
    go httpHandler.Listen(":8080")
    grpcServer.Listen(":9090")
}
```

---

## การจัดการ Error อย่างเป็นระบบ (Systematic Error Management)

### 3 ระดับของ Error

```
1. Sentinel Errors  — ค่าที่เปรียบเทียบได้ด้วย ==
2. Error Types      — struct ที่ implement error, พกข้อมูลเพิ่ม
3. Error Wrapping   — chain errors ด้วย %w เพื่อรักษา context
```

### ระดับ 1: Sentinel Errors

```go
// กำหนดใน package ที่ error เกิด (ไม่ใช่ package อื่น)
var (
    ErrOrderNotFound    = errors.New("order not found")
    ErrOrderAlreadyPaid = errors.New("order already paid")
    ErrInvalidStatus    = errors.New("invalid order status")
)

// เรียกใช้
order, err := repo.FindByID(ctx, id)
if errors.Is(err, ErrOrderNotFound) {
    // handle specifically
    return nil, ErrOrderNotFound
}
```

**ใช้ errors.Is ไม่ใช่ == โดยตรง** เพราะ errors.Is unwrap chain

### ระดับ 2: Error Types (พกข้อมูลเพิ่ม)

```go
// Error type สำหรับ validation failures
type ValidationError struct {
    Field   string
    Message string
    Value   any
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed: %s=%v: %s", e.Field, e.Value, e.Message)
}

// Error type สำหรับ business rule violations
type BusinessRuleError struct {
    Rule    string
    OrderID OrderID
    Current OrderStatus
    Reason  string
}

func (e *BusinessRuleError) Error() string {
    return fmt.Sprintf("business rule violated: rule=%s order=%s status=%s: %s",
        e.Rule, e.OrderID, e.Current, e.Reason)
}

// เรียกใช้ด้วย errors.As
var valErr *ValidationError
if errors.As(err, &valErr) {
    // เข้าถึง valErr.Field, valErr.Message
    http.Error(w, valErr.Message, http.StatusBadRequest)
}
```

### ระดับ 3: Error Wrapping

```go
// Pattern: "package: context: original error"
func (r *PostgresOrderRepo) FindByID(ctx context.Context, id OrderID) (*Order, error) {
    row := r.db.QueryRowContext(ctx, query, id)
    if err := row.Scan(&o); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, ErrOrderNotFound // sentinel, ไม่ wrap
        }
        return nil, fmt.Errorf("postgres.FindByID id=%s: %w", id, err)
    }
    return &o, nil
}

func (s *orderService) GetOrder(ctx context.Context, id OrderID) (*Order, error) {
    order, err := s.repo.FindByID(ctx, id)
    if err != nil {
        return nil, fmt.Errorf("orderService.GetOrder: %w", err) // wrap ต่อ
    }
    return order, nil
}

// error chain ที่ได้:
// "orderService.GetOrder: postgres.FindByID id=xxx: connection reset by peer"
// → ทราบทันทีว่า error เกิดที่ไหน ในระดับไหน
```

### ตาราง: errors.Is vs errors.As vs เปรียบเทียบโดยตรง

| Method | ใช้เมื่อ | ตัวอย่าง |
|--------|---------|---------|
| `err == ErrFoo` | ผิด! ใช้ไม่ได้กับ wrapped errors | อย่าใช้ |
| `errors.Is(err, ErrFoo)` | ตรวจว่า chain มี sentinel นั้นไหม | `errors.Is(err, ErrOrderNotFound)` |
| `errors.As(err, &target)` | ดึง error type ออกจาก chain | `errors.As(err, &valErr)` |
| `err.Error()` | แค่ log message (ไม่ใช้ compare) | `log.Error(err.Error())` |

### Error ที่ Layer Boundary — HTTP Transport

```go
// แปลง domain errors → HTTP status codes ที่ transport layer เท่านั้น
func (h *HTTPOrderHandler) getOrder(w http.ResponseWriter, r *http.Request) {
    id := parseOrderID(r)
    order, err := h.svc.GetOrder(r.Context(), id)
    if err != nil {
        h.handleError(w, err)
        return
    }
    json.NewEncoder(w).Encode(order)
}

func (h *HTTPOrderHandler) handleError(w http.ResponseWriter, err error) {
    var valErr *ValidationError
    var bizErr *BusinessRuleError
    
    switch {
    case errors.Is(err, ErrOrderNotFound):
        http.Error(w, "order not found", http.StatusNotFound)
    case errors.As(err, &valErr):
        http.Error(w, valErr.Message, http.StatusBadRequest)
    case errors.As(err, &bizErr):
        http.Error(w, bizErr.Reason, http.StatusUnprocessableEntity)
    default:
        // log unexpected errors, return generic message
        slog.Error("unexpected error", "err", err)
        http.Error(w, "internal server error", http.StatusInternalServerError)
    }
}
```

### Common Mistakes

```go
// ผิด 1: ไม่ wrap — สูญเสีย context
func (s *service) doWork() error {
    err := db.Query(...)
    if err != nil {
        return err // แค่นี้ไม่พอ ไม่รู้ว่า error มาจากไหน
    }
}

// ผิด 2: wrap แต่ไม่ใช้ %w — ทำลาย chain
func (s *service) doWork() error {
    err := db.Query(...)
    return fmt.Errorf("doWork failed: %v", err) // %v แทน %w = ไม่สามารถ errors.Is/As ได้
}

// ผิด 3: wrap ซ้ำเกินไป
func (s *service) doWork() error {
    err := db.Query(...)
    err = fmt.Errorf("layer1: %w", err)
    err = fmt.Errorf("layer2: %w", err)
    err = fmt.Errorf("layer3: %w", err) // stack trace ยาวเกิน ไม่มีประโยชน์
    return err
}
// แนะนำ: wrap ครั้งเดียวต่อ layer boundary
```

---

## ข้อผิดพลาดที่พบบ่อย (Common Mistakes)

### 1. Anemic Domain Model (ผิดพลาดที่พบบ่อยที่สุด)

```go
// ผิด: Anemic Domain Model
// Order เป็นแค่ data bag, business logic อยู่ใน service
type Order struct {
    ID     string
    Status string
    Items  []Item
}

// Business logic อยู่ที่นี่แทน → ผิด!
type OrderService struct {
    repo OrderRepo
}

func (s *OrderService) ConfirmOrder(id string) error {
    order, _ := s.repo.Find(id)
    if order.Status != "PENDING" { // business rule ใน service!
        return errors.New("invalid status")
    }
    order.Status = "CONFIRMED"
    return s.repo.Save(order)
}
```

```go
// ถูก: Rich Domain Model
// Business logic อยู่ใน Aggregate
func (o *Order) Confirm() error {
    if !o.status.CanTransitionTo(OrderStatusConfirmed) {
        return fmt.Errorf("cannot confirm order in status %s", o.status)
    }
    o.status = OrderStatusConfirmed
    o.addEvent(OrderConfirmedEvent{...})
    return nil
}

// Service เป็นแค่ orchestrator
func (h *OrderCommandHandler) HandleConfirmOrder(ctx context.Context, cmd ConfirmOrderCommand) error {
    order, _ := h.repo.FindByID(ctx, orderID)
    return order.Confirm() // delegate to domain
}
```

### 2. Import cycle — Infrastructure import Domain (ถูก) แต่ Domain import Infrastructure (ผิด)

```go
// ผิด: domain import database package
package domain

import "database/sql" // NO! domain ไม่รู้จัก database

type Order struct {
    db *sql.DB // NO!
}

// ถูก: domain กำหนด interface, infrastructure implement
package domain

// Repository interface ใน domain
type OrderRepository interface {
    Save(ctx context.Context, order *Order) error
    FindByID(ctx context.Context, id OrderID) (*Order, error)
}
```

### 3. Value Object ที่ mutable

```go
// ผิด: Money มี setter
type Money struct {
    Amount   int64
    Currency string
}
func (m *Money) SetAmount(a int64) { m.Amount = a } // NO!

// ถูก: Money เป็น immutable
type Money struct {
    amount   int64  // unexported!
    currency string
}
// ไม่มี setter, operations สร้าง Money ใหม่เสมอ
func (m Money) Add(other Money) (Money, error) {
    // return ใหม่ ไม่แก้ไข receiver
    return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}
```

### 4. ลืม publish domain events

```go
// ผิด: save แล้วลืม publish events
func (h *Handler) PlaceOrder(ctx context.Context, cmd PlaceOrderCommand) error {
    order, _ := domain.PlaceOrder(...)
    h.repo.Save(ctx, order) // events ยังอยู่ใน order.uncommittedEvents
    // ลืม publish! downstream systems ไม่รู้ว่ามี order ใหม่
    return nil
}

// ถูก:
func (h *Handler) PlaceOrder(ctx context.Context, cmd PlaceOrderCommand) error {
    order, _ := domain.PlaceOrder(...)
    if err := h.repo.Save(ctx, order); err != nil {
        return err
    }
    // Publish เสมอหลัง save สำเร็จ
    if err := h.eventBus.Publish(ctx, order.UncommittedEvents()...); err != nil {
        h.logger.Error("event publish failed", "error", err)
        // อาจ schedule retry หรือ outbox pattern
    }
    order.ClearEvents()
    return nil
}
```

---

## Notes
- DDD concepts + diagram
- Live coding Order aggregate
- CQRS + Event Sourcing
- Lab

### Opening — ทำไมต้องใช้ DDD?

เล่า story:
"มีทีม 10 คน ทำ e-commerce ด้วย transaction script
code ทุกอย่างอยู่ใน OrderService.go ที่มี 3,000 บรรทัด
มีคน join ใหม่ต้องใช้เวลา 2 สัปดาห์เพื่อเข้าใจ codebase
bug rate สูงมากเพราะ business rules กระจายอยู่ทุกที่"

DDD แก้ปัญหาเหล่านี้:
- Business rules อยู่ใน domain ที่เดียว
- เข้าใจง่าย เพราะ code พูดภาษา business
- Test ง่ายเพราะ domain ไม่มี dependencies

### Key Analogy
- Aggregate = ลูกค้า checkout ที่ร้าน ต้องผ่าน cashier เท่านั้น
- Value Object = ธนบัตร 100 บาท ใบไหนก็เหมือนกัน
- Domain Event = ใบเสร็จ บอกว่า "เกิดอะไรขึ้น"

### Live Coding Tips
1. เริ่มด้วย test ก่อน (TDD)
2. ให้เดาว่า business rule ควรอยู่ที่ไหน
3. แสดงว่า domain test เขียนง่ายมาก (ไม่ต้องมี mock)

### Lab: เพิ่ม Shipping Domain
```
โจทย์: เพิ่ม ShippingAddress Value Object
- ต้องมี street, city, country
- country ต้องเป็น ISO 3166-1 alpha-2
- เพิ่ม Ship() method ใน Order ที่ต้องการ address
```
