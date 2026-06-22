// Package domain — Repository + EventBus Interfaces
// Repository อยู่ใน domain layer แต่ implementation อยู่ใน infrastructure
// นี่คือ Dependency Inversion Principle (DIP):
// domain ไม่รู้จัก database แต่ database ต้อง implement interface ของ domain
package domain

import (
	"context"
	"errors"
	"time"
)

// ============================================================
// DOMAIN ERRORS
// ============================================================

// ErrNotFound — invoice ไม่พบใน repository
var ErrNotFound = errors.New("not found")

// ErrVersionConflict — optimistic locking: มีคนแก้ไข invoice ก่อนเรา
var ErrVersionConflict = errors.New("version conflict: invoice was modified by another process")

// ErrDuplicateInvoice — invoice ซ้ำ (same vendor + same date + same amount)
var ErrDuplicateInvoice = errors.New("duplicate invoice detected")

// ErrInvalidStatus — สถานะไม่ถูกต้องสำหรับ operation นี้
var ErrInvalidStatus = errors.New("invalid invoice status for this operation")

// ============================================================
// FILTER / PAGINATION
// ============================================================

// InvoiceFilter — criteria สำหรับ query invoices
type InvoiceFilter struct {
	VendorID *VendorID
	Status   *InvoiceStatus
	FromDate *time.Time
	ToDate   *time.Time
	// Pagination
	Limit  int
	Offset int
}

// ============================================================
// REPOSITORY INTERFACE
// ============================================================

// InvoiceRepository — interface ที่ domain กำหนด
// Infrastructure ต้อง implement interface นี้ (Dependency Inversion)
type InvoiceRepository interface {
	// Save สร้างหรืออัพเดท invoice
	// ใช้ version สำหรับ optimistic locking
	Save(ctx context.Context, invoice *Invoice) error

	// FindByID ดึง invoice ด้วย ID
	FindByID(ctx context.Context, id InvoiceID) (*Invoice, error)

	// FindAll ดึง invoices ทั้งหมด (with pagination)
	FindAll(ctx context.Context, filter InvoiceFilter) ([]*Invoice, error)

	// FindByVendor ดึง invoices ของ vendor
	FindByVendor(ctx context.Context, vendorID VendorID, filter InvoiceFilter) ([]*Invoice, error)

	// Count นับ invoices ตาม filter
	Count(ctx context.Context, filter InvoiceFilter) (int64, error)

	// ExistsWithSameVendorDateAmount ตรวจสอบ duplicate invoice
	// ใช้สำหรับ business rule: ห้าม invoice ซ้ำ (same vendor + date + amount)
	ExistsWithSameVendorDateAmount(
		ctx context.Context,
		vendorID VendorID,
		invoiceDate time.Time,
		amount Money,
		excludeID *InvoiceID, // ยกเว้น invoice ปัจจุบัน (สำหรับ update)
	) (bool, error)
}

// ============================================================
// EVENT BUS INTERFACE
// ============================================================

// EventBus — สำหรับ publish domain events
type EventBus interface {
	// Publish ส่ง events ไปยัง subscribers
	Publish(ctx context.Context, events ...DomainEvent) error

	// Subscribe ลงทะเบียนรับ events ตาม event name
	// ใช้ "*" สำหรับรับทุก events
	Subscribe(eventName string, handler EventHandler)
}

// EventHandler — function signature สำหรับ event handlers
type EventHandler func(ctx context.Context, event DomainEvent) error
