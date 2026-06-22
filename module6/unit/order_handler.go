// Package unit — Unit Testing Patterns for Enterprise Go
//
// แสดง patterns สำหรับ unit testing ใน enterprise codebase:
//   - Clean interfaces ที่ testable ได้ง่าย
//   - Dependency injection ผ่าน interface
//   - Business logic แยกออกจาก infrastructure
//
// หลักการ: "Don't test the framework, test YOUR code"
package unit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ============================================================
// DOMAIN TYPES — เพื่อความ self-contained ของ module นี้
// ============================================================

// OrderID — typed ID ป้องกัน primitive obsession
type OrderID string

func (id OrderID) String() string { return string(id) }
func (id OrderID) IsEmpty() bool  { return id == "" }

// CustomerID — typed ID สำหรับ customer
type CustomerID string

func (id CustomerID) String() string { return string(id) }

// OrderStatus — state machine สำหรับ order
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "PENDING"
	OrderStatusConfirmed OrderStatus = "CONFIRMED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
)

// Order — domain model
type Order struct {
	ID         OrderID
	CustomerID CustomerID
	Status     OrderStatus
	TotalCents int64 // เก็บเป็น cents ป้องกัน floating point error
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PlaceOrderRequest — input สำหรับ PlaceOrder
type PlaceOrderRequest struct {
	CustomerID CustomerID
	Items      []OrderItem
}

// OrderItem — line item ใน order
type OrderItem struct {
	ProductID   string
	ProductName string
	UnitCents   int64 // ราคาเป็น cents
	Quantity    int
}

// Validate ตรวจสอบ PlaceOrderRequest
func (r PlaceOrderRequest) Validate() error {
	if r.CustomerID == "" {
		return errors.New("customerID is required")
	}
	if len(r.Items) == 0 {
		return errors.New("order must have at least one item")
	}
	for i, item := range r.Items {
		if item.ProductID == "" {
			return fmt.Errorf("item[%d]: productID is required", i)
		}
		if item.Quantity <= 0 {
			return fmt.Errorf("item[%d]: quantity must be positive", i)
		}
		if item.UnitCents <= 0 {
			return fmt.Errorf("item[%d]: unit price must be positive", i)
		}
	}
	return nil
}

// CancelOrderRequest — input สำหรับ CancelOrder
type CancelOrderRequest struct {
	OrderID OrderID
	Reason  string
}

// ============================================================
// REPOSITORY INTERFACE — dependency ที่ inject เข้ามา
// ============================================================

// OrderRepository — interface สำหรับ data access
// ทำไมต้องเป็น interface?
//   1. ทำ unit test ได้โดยไม่ต้องมี DB
//   2. สามารถเปลี่ยน implementation ได้ (Postgres → MongoDB) โดยไม่กระทบ business logic
//   3. เป็น Dependency Inversion Principle ใน SOLID
type OrderRepository interface {
	Save(ctx context.Context, order *Order) error
	FindByID(ctx context.Context, id OrderID) (*Order, error)
	Delete(ctx context.Context, id OrderID) error
}

// IDGenerator — interface สำหรับสร้าง ID
// inject เข้ามาเพื่อให้ test ควบคุม ID ที่สร้างได้
type IDGenerator interface {
	NewOrderID() OrderID
}

// ============================================================
// SENTINEL ERRORS — ใช้ errors.Is() ตรวจสอบ
// ============================================================

// ErrOrderNotFound — order ไม่พบใน repository
var ErrOrderNotFound = errors.New("order not found")

// ErrOrderAlreadyCancelled — ไม่สามารถ cancel order ที่ cancelled แล้ว
var ErrOrderAlreadyCancelled = errors.New("order already cancelled")

// ErrOrderCannotBeCancelled — order ไม่อยู่ใน state ที่ cancel ได้
var ErrOrderCannotBeCancelled = errors.New("order cannot be cancelled in current status")

// ErrInvalidRequest — request มีข้อมูลไม่ถูกต้อง
var ErrInvalidRequest = errors.New("invalid request")

// ============================================================
// ORDER HANDLER — Application Service / Use Case
// ============================================================

// OrderHandler — application service ที่จัดการ order operations
// Thin layer ที่ orchestrate domain objects และ repositories
//
// สังเกตว่า struct ไม่ได้ hardcode dependencies แต่รับผ่าน constructor
// นี่คือ Constructor Injection — pattern ที่ Go นิยมใช้
type OrderHandler struct {
	repo      OrderRepository
	idGen     IDGenerator
	clock     func() time.Time // inject สำหรับ deterministic time ใน tests
}

// NewOrderHandler สร้าง OrderHandler ใหม่
// ถ้า clock == nil จะใช้ time.Now จริง
func NewOrderHandler(repo OrderRepository, idGen IDGenerator, clock func() time.Time) *OrderHandler {
	if clock == nil {
		clock = time.Now
	}
	return &OrderHandler{
		repo:  repo,
		idGen: idGen,
		clock: clock,
	}
}

// PlaceOrder สร้าง order ใหม่
//
// Business rules:
//   - Request ต้องผ่าน validation
//   - ไม่รับ order ที่มีมูลค่ารวมเกิน 10,000,000 cents (100,000 บาท)
//   - แต่ละ order ต้องมี item อย่างน้อย 1 ชิ้น และสูงสุด 20 ชิ้น
func (h *OrderHandler) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (*Order, error) {
	// Validate request ก่อน — fail fast
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, err)
	}

	// Business rule: max 20 items
	if len(req.Items) > 20 {
		return nil, fmt.Errorf("%w: order cannot have more than 20 items", ErrInvalidRequest)
	}

	// คำนวณ total
	var totalCents int64
	for _, item := range req.Items {
		totalCents += item.UnitCents * int64(item.Quantity)
	}

	// Business rule: max order value 100,000 บาท (10,000,000 cents)
	const maxOrderCents int64 = 10_000_000
	if totalCents > maxOrderCents {
		return nil, fmt.Errorf("%w: order total exceeds maximum allowed amount", ErrInvalidRequest)
	}

	now := h.clock()
	order := &Order{
		ID:         h.idGen.NewOrderID(),
		CustomerID: req.CustomerID,
		Status:     OrderStatusPending,
		TotalCents: totalCents,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := h.repo.Save(ctx, order); err != nil {
		return nil, fmt.Errorf("saving order: %w", err)
	}

	return order, nil
}

// CancelOrder ยกเลิก order
//
// Business rules:
//   - Order ต้องมีอยู่จริง
//   - Order ต้องอยู่ใน state PENDING หรือ CONFIRMED เท่านั้น
//   - Reason ต้องไม่ว่างเปล่า
func (h *OrderHandler) CancelOrder(ctx context.Context, req CancelOrderRequest) error {
	if req.OrderID.IsEmpty() {
		return fmt.Errorf("%w: orderID is required", ErrInvalidRequest)
	}
	if req.Reason == "" {
		return fmt.Errorf("%w: cancellation reason is required", ErrInvalidRequest)
	}

	order, err := h.repo.FindByID(ctx, req.OrderID)
	if err != nil {
		// wrap เพื่อให้ caller ใช้ errors.Is ตรวจได้
		if errors.Is(err, ErrOrderNotFound) {
			return err
		}
		return fmt.Errorf("finding order: %w", err)
	}

	switch order.Status {
	case OrderStatusCancelled:
		return ErrOrderAlreadyCancelled
	case OrderStatusPending, OrderStatusConfirmed:
		// allowed — ดำเนินการต่อ
	default:
		return fmt.Errorf("%w: status=%s", ErrOrderCannotBeCancelled, order.Status)
	}

	order.Status = OrderStatusCancelled
	order.UpdatedAt = h.clock()

	if err := h.repo.Save(ctx, order); err != nil {
		return fmt.Errorf("saving cancelled order: %w", err)
	}

	return nil
}

// GetOrder ดึงข้อมูล order ด้วย ID
func (h *OrderHandler) GetOrder(ctx context.Context, id OrderID) (*Order, error) {
	if id.IsEmpty() {
		return nil, fmt.Errorf("%w: orderID is required", ErrInvalidRequest)
	}

	order, err := h.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err // ส่ง error ต้นทางออกไปตรงๆ (รวมถึง ErrOrderNotFound)
	}

	return order, nil
}
