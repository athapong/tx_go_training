// Package infrastructure — In-Memory Repository Implementation
// สำหรับ testing และ development
package infrastructure

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"tx_go_training/module3/domain"
)

// ============================================================
// IN-MEMORY ORDER REPOSITORY
// ============================================================

// InMemoryOrderRepository — implements domain.OrderRepository
// ใช้สำหรับ: unit tests, local development, demos
type InMemoryOrderRepository struct {
	mu     sync.RWMutex
	orders map[string]*orderSnapshot
}

// orderSnapshot เก็บ snapshot ของ order (เพื่อ simulate database)
type orderSnapshot struct {
	order   *domain.Order
	version int
}

func NewInMemoryOrderRepository() *InMemoryOrderRepository {
	return &InMemoryOrderRepository{
		orders: make(map[string]*orderSnapshot),
	}
}

// Save สร้างหรืออัพเดท order
func (r *InMemoryOrderRepository) Save(ctx context.Context, order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := order.ID().String()

	// Optimistic locking check
	if existing, ok := r.orders[id]; ok {
		if existing.version >= order.Version() {
			return domain.ErrVersionConflict
		}
	}

	r.orders[id] = &orderSnapshot{
		order:   order,
		version: order.Version(),
	}

	return nil
}

// FindByID ดึง order ด้วย ID
func (r *InMemoryOrderRepository) FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot, ok := r.orders[id.String()]
	if !ok {
		return nil, fmt.Errorf("order %s: %w", id, domain.ErrNotFound)
	}

	return snapshot.order, nil
}

// FindByCustomer ดึง orders ของ customer
func (r *InMemoryOrderRepository) FindByCustomer(
	ctx context.Context,
	customerID domain.CustomerID,
	filter domain.OrderFilter,
) ([]*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []*domain.Order
	for _, snapshot := range r.orders {
		order := snapshot.order
		if order.CustomerID().String() != customerID.String() {
			continue
		}

		// Apply status filter
		if filter.Status != nil && order.Status() != *filter.Status {
			continue
		}

		// Apply date filter
		if filter.FromDate != nil && order.CreatedAt().Before(*filter.FromDate) {
			continue
		}
		if filter.ToDate != nil && order.CreatedAt().After(*filter.ToDate) {
			continue
		}

		results = append(results, order)
	}

	// Apply pagination
	if filter.Offset >= len(results) {
		return nil, nil
	}
	results = results[filter.Offset:]
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

// Delete ลบ order
func (r *InMemoryOrderRepository) Delete(ctx context.Context, id domain.OrderID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.orders[id.String()]; !ok {
		return fmt.Errorf("order %s: %w", id, domain.ErrNotFound)
	}

	delete(r.orders, id.String())
	return nil
}

// Count นับ orders
func (r *InMemoryOrderRepository) Count(ctx context.Context, filter domain.OrderFilter) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var count int64
	for _, snapshot := range r.orders {
		if filter.Status != nil && snapshot.order.Status() != *filter.Status {
			continue
		}
		count++
	}

	return count, nil
}

// ============================================================
// IN-MEMORY EVENT BUS
// ============================================================

type InMemoryEventBus struct {
	mu       sync.RWMutex
	handlers map[string][]domain.EventHandler
	// เก็บ events สำหรับ testing
	published []domain.DomainEvent
}

func NewInMemoryEventBus() *InMemoryEventBus {
	return &InMemoryEventBus{
		handlers: make(map[string][]domain.EventHandler),
	}
}

func (b *InMemoryEventBus) Publish(ctx context.Context, events ...domain.DomainEvent) error {
	b.mu.Lock()
	b.published = append(b.published, events...)
	handlers := make(map[string][]domain.EventHandler, len(b.handlers))
	maps.Copy(handlers, b.handlers)
	b.mu.Unlock()

	for _, event := range events {
		if h, ok := handlers[event.EventName()]; ok {
			for _, handler := range h {
				if err := handler(ctx, event); err != nil {
					return fmt.Errorf("event handler for %s: %w", event.EventName(), err)
				}
			}
		}
		// Wildcard handlers
		if h, ok := handlers["*"]; ok {
			for _, handler := range h {
				if err := handler(ctx, event); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (b *InMemoryEventBus) Subscribe(eventName string, handler domain.EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventName] = append(b.handlers[eventName], handler)
}

// PublishedEvents returns events สำหรับ testing
func (b *InMemoryEventBus) PublishedEvents() []domain.DomainEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]domain.DomainEvent, len(b.published))
	copy(result, b.published)
	return result
}

// ClearEvents ล้าง events สำหรับ testing
func (b *InMemoryEventBus) ClearEvents() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = nil
}
