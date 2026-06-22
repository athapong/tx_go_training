// Package domain — Repository Interface
// Repository อยู่ใน domain layer แต่ implementation อยู่ใน infrastructure
// นี่คือ Dependency Inversion Principle:
// domain ไม่รู้จัก database แต่ database รู้จัก domain
package domain

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound — standard error สำหรับ not found
var ErrNotFound = errors.New("not found")

// ErrVersionConflict — optimistic locking conflict
var ErrVersionConflict = errors.New("version conflict: order was modified by another process")

// OrderFilter — criteria สำหรับ query
type OrderFilter struct {
	CustomerID *CustomerID
	Status     *OrderStatus
	FromDate   *time.Time
	ToDate     *time.Time
	// Pagination
	Limit  int
	Offset int
}

// OrderRepository — interface ที่ domain กำหนด
// Infrastructure ต้อง implement interface นี้
type OrderRepository interface {
	// Save สร้างหรืออัพเดท order
	// ใช้ version สำหรับ optimistic locking
	Save(ctx context.Context, order *Order) error

	// FindByID ดึง order ด้วย ID
	FindByID(ctx context.Context, id OrderID) (*Order, error)

	// FindByCustomer ดึง orders ของ customer
	FindByCustomer(ctx context.Context, customerID CustomerID, filter OrderFilter) ([]*Order, error)

	// Delete ลบ order
	Delete(ctx context.Context, id OrderID) error

	// Count นับ orders ตาม filter
	Count(ctx context.Context, filter OrderFilter) (int64, error)
}

// EventStore — สำหรับ Event Sourcing
type EventStore interface {
	// Append เพิ่ม events สำหรับ aggregate
	Append(ctx context.Context, aggregateID string, events []DomainEvent, expectedVersion int) error

	// Load ดึง events ทั้งหมดของ aggregate
	Load(ctx context.Context, aggregateID string) ([]DomainEvent, error)

	// LoadSince ดึง events หลังจาก version หนึ่ง
	LoadSince(ctx context.Context, aggregateID string, version int) ([]DomainEvent, error)
}

// EventBus — สำหรับ publish domain events
type EventBus interface {
	// Publish ส่ง events ไปยัง subscribers
	Publish(ctx context.Context, events ...DomainEvent) error

	// Subscribe ลงทะเบียนรับ events
	Subscribe(eventName string, handler EventHandler)
}

// EventHandler — function signature สำหรับ event handlers
type EventHandler func(ctx context.Context, event DomainEvent) error
