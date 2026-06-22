// Package domain — DDD Order Aggregate
// โครงสร้าง DDD ใน Go:
//
// domain/          ← ไม่ขึ้นกับอะไรเลย (pure business logic)
//   order.go       ← Aggregate Root
//   events.go      ← Domain Events
//   repository.go  ← Repository Interface
// application/     ← Use cases (commands/queries)
// infrastructure/  ← Database, messaging implementations
package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// ============================================================
// VALUE OBJECTS — Immutable, ไม่มี identity
// ============================================================

// Money — Value Object สำหรับเงิน
// ทำไมไม่ใช้ float64? เพราะ floating point ไม่แม่นยำสำหรับเงิน
type Money struct {
	amount   int64  // ใช้ int64 เก็บ cents (100 = 1 บาท)
	currency string // ISO 4217: "THB", "USD"
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, errors.New("amount cannot be negative")
	}
	if currency == "" {
		return Money{}, errors.New("currency is required")
	}
	return Money{amount: amount, currency: currency}, nil
}

func (m Money) Amount() int64     { return m.amount }
func (m Money) Currency() string  { return m.currency }
func (m Money) IsZero() bool      { return m.amount == 0 }
func (m Money) String() string    { return fmt.Sprintf("%.2f %s", float64(m.amount)/100, m.currency) }

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("cannot add %s to %s", other.currency, m.currency)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

func (m Money) Multiply(factor int64) Money {
	return Money{amount: m.amount * factor, currency: m.currency}
}

func (m Money) Equals(other Money) bool {
	return m.amount == other.amount && m.currency == other.currency
}

// ----------------------------------------
// OrderID — Value Object (Typed ID)
// ทำไมไม่ใช้ string ตรงๆ? เพื่อป้องกัน primitive obsession
// เช่น ป้องกันการเอา CustomerID ไปใส่ตรงที่ต้องการ OrderID
type OrderID struct {
	value string
}

func NewOrderID(id string) (OrderID, error) {
	if id == "" {
		return OrderID{}, errors.New("order ID cannot be empty")
	}
	return OrderID{value: id}, nil
}

func (id OrderID) String() string { return id.value }
func (id OrderID) IsZero() bool   { return id.value == "" }

// ----------------------------------------
// CustomerID — ป้องกัน mix-up กับ OrderID
type CustomerID struct {
	value string
}

func NewCustomerID(id string) (CustomerID, error) {
	if id == "" {
		return CustomerID{}, errors.New("customer ID cannot be empty")
	}
	return CustomerID{value: id}, nil
}

func (id CustomerID) String() string { return id.value }

// ----------------------------------------
// OrderItem — Value Object (immutable line item)
type OrderItem struct {
	productID   string
	productName string
	unitPrice   Money
	quantity    int
}

func NewOrderItem(productID, productName string, unitPrice Money, quantity int) (OrderItem, error) {
	if productID == "" {
		return OrderItem{}, errors.New("product ID is required")
	}
	if quantity <= 0 {
		return OrderItem{}, errors.New("quantity must be positive")
	}
	if unitPrice.IsZero() {
		return OrderItem{}, errors.New("unit price cannot be zero")
	}

	return OrderItem{
		productID:   productID,
		productName: productName,
		unitPrice:   unitPrice,
		quantity:    quantity,
	}, nil
}

func (i OrderItem) ProductID() string   { return i.productID }
func (i OrderItem) ProductName() string { return i.productName }
func (i OrderItem) UnitPrice() Money    { return i.unitPrice }
func (i OrderItem) Quantity() int       { return i.quantity }

func (i OrderItem) Subtotal() Money {
	return i.unitPrice.Multiply(int64(i.quantity))
}

// ============================================================
// ORDER STATUS — State Machine
// ============================================================

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "PENDING"
	OrderStatusConfirmed OrderStatus = "CONFIRMED"
	OrderStatusShipped   OrderStatus = "SHIPPED"
	OrderStatusDelivered OrderStatus = "DELIVERED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
)

// validTransitions กำหนดว่าสถานะไหนไปสถานะไหนได้บ้าง
var validTransitions = map[OrderStatus][]OrderStatus{
	OrderStatusPending:   {OrderStatusConfirmed, OrderStatusCancelled},
	OrderStatusConfirmed: {OrderStatusShipped, OrderStatusCancelled},
	OrderStatusShipped:   {OrderStatusDelivered},
	OrderStatusDelivered: {},
	OrderStatusCancelled: {},
}

func (s OrderStatus) CanTransitionTo(next OrderStatus) bool {
	allowed, ok := validTransitions[s]
	if !ok {
		return false
	}
	return slices.Contains(allowed, next)
}

// ============================================================
// DOMAIN EVENTS
// ============================================================

// DomainEvent — interface สำหรับ events ทั้งหมด
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
	AggregateID() string
}

// BaseEvent — embed ใน events ทั้งหมด
type BaseEvent struct {
	occurredAt  time.Time
	aggregateID string
}

func newBaseEvent(aggregateID string) BaseEvent {
	return BaseEvent{
		occurredAt:  time.Now(),
		aggregateID: aggregateID,
	}
}

func (e BaseEvent) OccurredAt() time.Time { return e.occurredAt }
func (e BaseEvent) AggregateID() string   { return e.aggregateID }

// OrderPlacedEvent — fired เมื่อ order ถูก place
type OrderPlacedEvent struct {
	BaseEvent
	OrderID    OrderID
	CustomerID CustomerID
	TotalAmount Money
}

func (e OrderPlacedEvent) EventName() string { return "order.placed" }

// OrderConfirmedEvent — fired เมื่อ order ถูก confirm
type OrderConfirmedEvent struct {
	BaseEvent
	OrderID OrderID
}

func (e OrderConfirmedEvent) EventName() string { return "order.confirmed" }

// OrderCancelledEvent — fired เมื่อ order ถูก cancel
type OrderCancelledEvent struct {
	BaseEvent
	OrderID OrderID
	Reason  string
}

func (e OrderCancelledEvent) EventName() string { return "order.cancelled" }

// ============================================================
// ORDER AGGREGATE ROOT
// ============================================================

// Order — Aggregate Root
// ทำไมเป็น pointer receiver? เพราะ aggregate มี state ที่ต้องเปลี่ยน
// ทำไมไม่ export fields? เพราะ business rules ต้องผ่าน methods เท่านั้น
type Order struct {
	// Identity
	id         OrderID
	customerID CustomerID

	// State
	status    OrderStatus
	items     []OrderItem
	createdAt time.Time
	updatedAt time.Time

	// Optimistic locking
	version int

	// Domain events ที่ยังไม่ได้ publish
	uncommittedEvents []DomainEvent
}

// ============================================================
// FACTORY METHOD — สร้าง Order ใหม่
// ============================================================

// PlaceOrder สร้าง Order ใหม่ (factory method)
// ชื่อ "PlaceOrder" เป็น ubiquitous language จาก business domain
func PlaceOrder(id OrderID, customerID CustomerID, items []OrderItem) (*Order, error) {
	if id.IsZero() {
		return nil, errors.New("order ID is required")
	}
	if len(items) == 0 {
		return nil, errors.New("order must have at least one item")
	}

	now := time.Now()
	order := &Order{
		id:         id,
		customerID: customerID,
		status:     OrderStatusPending,
		items:      items,
		createdAt:  now,
		updatedAt:  now,
		version:    1,
	}

	// คำนวณ total
	total, err := order.calculateTotal()
	if err != nil {
		return nil, fmt.Errorf("calculating total: %w", err)
	}

	// Business rule: ไม่รับ order ที่มีมูลค่าเกิน 1,000,000 บาท
	maxAmount, _ := NewMoney(100_000_000, "THB") // 1,000,000 บาท
	if total.Amount() > maxAmount.Amount() {
		return nil, fmt.Errorf("order total %s exceeds maximum allowed", total)
	}

	// Fire domain event
	order.addEvent(OrderPlacedEvent{
		BaseEvent:   newBaseEvent(id.String()),
		OrderID:     id,
		CustomerID:  customerID,
		TotalAmount: total,
	})

	return order, nil
}

// ============================================================
// AGGREGATE METHODS — Business Operations
// ============================================================

// Confirm — ยืนยัน order
func (o *Order) Confirm() error {
	if !o.status.CanTransitionTo(OrderStatusConfirmed) {
		return fmt.Errorf("cannot confirm order in status %s", o.status)
	}

	o.status = OrderStatusConfirmed
	o.updatedAt = time.Now()
	o.version++

	o.addEvent(OrderConfirmedEvent{
		BaseEvent: newBaseEvent(o.id.String()),
		OrderID:   o.id,
	})

	return nil
}

// Cancel — ยกเลิก order
func (o *Order) Cancel(reason string) error {
	if !o.status.CanTransitionTo(OrderStatusCancelled) {
		return fmt.Errorf("cannot cancel order in status %s", o.status)
	}
	if reason == "" {
		return errors.New("cancellation reason is required")
	}

	o.status = OrderStatusCancelled
	o.updatedAt = time.Now()
	o.version++

	o.addEvent(OrderCancelledEvent{
		BaseEvent: newBaseEvent(o.id.String()),
		OrderID:   o.id,
		Reason:    reason,
	})

	return nil
}

// AddItem — เพิ่ม item (เฉพาะ pending orders)
func (o *Order) AddItem(item OrderItem) error {
	if o.status != OrderStatusPending {
		return fmt.Errorf("cannot add item to order in status %s", o.status)
	}

	// Business rule: max 50 items per order
	if len(o.items) >= 50 {
		return errors.New("order cannot have more than 50 items")
	}

	o.items = append(o.items, item)
	o.updatedAt = time.Now()
	o.version++

	return nil
}

// ============================================================
// QUERY METHODS (เข้าถึง state แบบ read-only)
// ============================================================

func (o *Order) ID() OrderID            { return o.id }
func (o *Order) CustomerID() CustomerID { return o.customerID }
func (o *Order) Status() OrderStatus    { return o.status }
func (o *Order) Items() []OrderItem     { return append([]OrderItem{}, o.items...) } // defensive copy
func (o *Order) CreatedAt() time.Time   { return o.createdAt }
func (o *Order) UpdatedAt() time.Time   { return o.updatedAt }
func (o *Order) Version() int           { return o.version }

func (o *Order) TotalAmount() (Money, error) {
	return o.calculateTotal()
}

func (o *Order) ItemCount() int { return len(o.items) }

// ============================================================
// DOMAIN EVENTS MANAGEMENT
// ============================================================

// addEvent เพิ่ม event ลงใน uncommitted list
func (o *Order) addEvent(event DomainEvent) {
	o.uncommittedEvents = append(o.uncommittedEvents, event)
}

// UncommittedEvents returns events ที่ยังไม่ได้ publish
func (o *Order) UncommittedEvents() []DomainEvent {
	return o.uncommittedEvents
}

// ClearEvents ล้าง events หลังจาก publish แล้ว
func (o *Order) ClearEvents() {
	o.uncommittedEvents = nil
}

// ============================================================
// PRIVATE HELPERS
// ============================================================

func (o *Order) calculateTotal() (Money, error) {
	if len(o.items) == 0 {
		return NewMoney(0, "THB")
	}

	// ใช้ currency ของ item แรก
	firstItemSubtotal := o.items[0].Subtotal()
	total, err := NewMoney(0, firstItemSubtotal.Currency())
	if err != nil {
		return Money{}, err
	}

	for _, item := range o.items {
		subtotal := item.Subtotal()
		total, err = total.Add(subtotal)
		if err != nil {
			return Money{}, fmt.Errorf("item %s: %w", item.ProductID(), err)
		}
	}

	return total, nil
}
