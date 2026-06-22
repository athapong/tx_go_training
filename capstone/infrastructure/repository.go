// Package infrastructure — In-Memory Repository Implementations
// Thread-safe implementation สำหรับ development, testing, และ demo
//
// Production replacement:
// - สลับเป็น PostgreSQL repository โดยไม่ต้องแก้ domain หรือ application layer
// - นี่คือข้อดีของ Dependency Inversion Principle (DIP)
package infrastructure

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tx_go_training/capstone/domain"
)

// ============================================================
// IN-MEMORY INVOICE REPOSITORY
// ============================================================

// InMemoryInvoiceRepository — implements domain.InvoiceRepository
// thread-safe ด้วย sync.RWMutex:
// - RLock สำหรับ read operations (FindByID, FindAll, Count)
// - Lock สำหรับ write operations (Save)
type InMemoryInvoiceRepository struct {
	mu       sync.RWMutex
	invoices map[string]*invoiceSnapshot
}

// invoiceSnapshot เก็บ snapshot ของ invoice สำหรับ simulate database row
type invoiceSnapshot struct {
	invoice *domain.Invoice
	version int
}

func NewInMemoryInvoiceRepository() *InMemoryInvoiceRepository {
	return &InMemoryInvoiceRepository{
		invoices: make(map[string]*invoiceSnapshot),
	}
}

// Save สร้างหรืออัพเดท invoice
// Optimistic locking: ป้องกัน concurrent modification
func (r *InMemoryInvoiceRepository) Save(ctx context.Context, invoice *domain.Invoice) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := invoice.ID().String()

	// Optimistic locking check
	if existing, ok := r.invoices[id]; ok {
		// version ของ invoice ที่จะ save ต้องสูงกว่า version ที่อยู่ใน repo
		if existing.version >= invoice.Version() {
			return fmt.Errorf("invoice %s: %w", id, domain.ErrVersionConflict)
		}
	}

	r.invoices[id] = &invoiceSnapshot{
		invoice: invoice,
		version: invoice.Version(),
	}

	return nil
}

// FindByID ดึง invoice ด้วย ID
func (r *InMemoryInvoiceRepository) FindByID(ctx context.Context, id domain.InvoiceID) (*domain.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot, ok := r.invoices[id.String()]
	if !ok {
		return nil, fmt.Errorf("invoice %s: %w", id, domain.ErrNotFound)
	}

	return snapshot.invoice, nil
}

// FindAll ดึง invoices ทั้งหมด (with filter + pagination)
func (r *InMemoryInvoiceRepository) FindAll(ctx context.Context, filter domain.InvoiceFilter) ([]*domain.Invoice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []*domain.Invoice
	for _, snapshot := range r.invoices {
		inv := snapshot.invoice

		// Apply status filter
		if filter.Status != nil && inv.Status() != *filter.Status {
			continue
		}

		// Apply vendor filter
		if filter.VendorID != nil && inv.VendorID().String() != filter.VendorID.String() {
			continue
		}

		// Apply date range filter
		if filter.FromDate != nil && inv.CreatedAt().Before(*filter.FromDate) {
			continue
		}
		if filter.ToDate != nil && inv.CreatedAt().After(*filter.ToDate) {
			continue
		}

		results = append(results, inv)
	}

	// Sort by CreatedAt descending (newest first)
	sortInvoicesByCreatedAtDesc(results)

	// Apply pagination
	if filter.Offset >= len(results) {
		return []*domain.Invoice{}, nil
	}
	results = results[filter.Offset:]
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

// FindByVendor ดึง invoices ของ vendor
func (r *InMemoryInvoiceRepository) FindByVendor(
	ctx context.Context,
	vendorID domain.VendorID,
	filter domain.InvoiceFilter,
) ([]*domain.Invoice, error) {
	// สร้าง filter ที่มี vendorID
	vendorFilter := filter
	vendorFilter.VendorID = &vendorID
	return r.FindAll(ctx, vendorFilter)
}

// Count นับ invoices ตาม filter
func (r *InMemoryInvoiceRepository) Count(ctx context.Context, filter domain.InvoiceFilter) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var count int64
	for _, snapshot := range r.invoices {
		inv := snapshot.invoice

		if filter.Status != nil && inv.Status() != *filter.Status {
			continue
		}
		if filter.VendorID != nil && inv.VendorID().String() != filter.VendorID.String() {
			continue
		}

		count++
	}

	return count, nil
}

// ExistsWithSameVendorDateAmount ตรวจสอบ duplicate invoice
// Business rule: vendor + date + amount ซ้ำกันถือว่าเป็น duplicate
func (r *InMemoryInvoiceRepository) ExistsWithSameVendorDateAmount(
	ctx context.Context,
	vendorID domain.VendorID,
	invoiceDate time.Time,
	amount domain.Money,
	excludeID *domain.InvoiceID,
) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, snapshot := range r.invoices {
		inv := snapshot.invoice

		// ยกเว้น invoice ปัจจุบัน (สำหรับ update)
		if excludeID != nil && inv.ID().String() == excludeID.String() {
			continue
		}

		// ตรวจสอบ vendor
		if inv.VendorID().String() != vendorID.String() {
			continue
		}

		// ตรวจสอบ amount
		if !inv.TotalAmount().Equals(amount) {
			continue
		}

		// ตรวจสอบ date (เฉพาะ invoices ที่ extracted แล้ว)
		if inv.ExtractedAt() == nil {
			continue
		}

		// เปรียบเทียบวันที่ (ไม่สนใจ time)
		iy, im, id := inv.InvoiceDate().Date()
		ty, tm, td := invoiceDate.Date()
		if iy == ty && im == tm && id == td {
			return true, nil
		}
	}

	return false, nil
}

// ============================================================
// SORTING HELPER
// ============================================================

// sortInvoicesByCreatedAtDesc เรียง invoices ตาม CreatedAt (ใหม่ก่อน)
// ใช้ insertion sort สำหรับ in-memory (สำหรับ production ใช้ ORDER BY)
func sortInvoicesByCreatedAtDesc(invoices []*domain.Invoice) {
	for i := 1; i < len(invoices); i++ {
		key := invoices[i]
		j := i - 1
		for j >= 0 && invoices[j].CreatedAt().Before(key.CreatedAt()) {
			invoices[j+1] = invoices[j]
			j--
		}
		invoices[j+1] = key
	}
}

// ============================================================
// IN-MEMORY EVENT BUS
// ============================================================

// InMemoryEventBus — implements domain.EventBus
// สำหรับ development/testing, publish events synchronously
// Production: ใช้ Kafka, RabbitMQ, หรือ GCP Pub/Sub แทน
type InMemoryEventBus struct {
	mu       sync.RWMutex
	handlers map[string][]domain.EventHandler
	// เก็บ events ทั้งหมดสำหรับ debugging/testing
	published []domain.DomainEvent
}

func NewInMemoryEventBus() *InMemoryEventBus {
	return &InMemoryEventBus{
		handlers: make(map[string][]domain.EventHandler),
	}
}

// Publish ส่ง events ไปยัง handlers ที่ subscribe
func (b *InMemoryEventBus) Publish(ctx context.Context, events ...domain.DomainEvent) error {
	b.mu.Lock()
	b.published = append(b.published, events...)
	// Copy handlers เพื่อเรียกนอก lock
	handlers := make(map[string][]domain.EventHandler, len(b.handlers))
	for k, v := range b.handlers {
		cp := make([]domain.EventHandler, len(v))
		copy(cp, v)
		handlers[k] = cp
	}
	b.mu.Unlock()

	for _, event := range events {
		// Specific handlers
		if h, ok := handlers[event.EventName()]; ok {
			for _, handler := range h {
				if err := handler(ctx, event); err != nil {
					return fmt.Errorf("event handler for %q: %w", event.EventName(), err)
				}
			}
		}
		// Wildcard handlers ("*")
		if h, ok := handlers["*"]; ok {
			for _, handler := range h {
				if err := handler(ctx, event); err != nil {
					return fmt.Errorf("wildcard handler: %w", err)
				}
			}
		}
	}

	return nil
}

// Subscribe ลงทะเบียนรับ events
// eventName = "*" สำหรับรับทุก events
func (b *InMemoryEventBus) Subscribe(eventName string, handler domain.EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventName] = append(b.handlers[eventName], handler)
}

// PublishedEvents คืน events ที่ publish แล้ว (สำหรับ testing)
func (b *InMemoryEventBus) PublishedEvents() []domain.DomainEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]domain.DomainEvent, len(b.published))
	copy(result, b.published)
	return result
}

// ClearEvents ล้าง published events (สำหรับ testing)
func (b *InMemoryEventBus) ClearEvents() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = nil
}
