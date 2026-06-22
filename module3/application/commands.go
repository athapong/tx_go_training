// Package application — CQRS Command/Query Handlers
// Application layer:
// - รับ input จาก transport layer (HTTP, gRPC)
// - ใช้ domain objects ทำ business logic
// - ไม่รู้จัก transport details
package application

import (
	"context"
	"fmt"
	"log/slog"

	"tx_go_training/module3/domain"
)

// ============================================================
// COMMANDS — เปลี่ยน state
// ============================================================

// PlaceOrderCommand — input สำหรับ place order use case
type PlaceOrderCommand struct {
	OrderID    string
	CustomerID string
	Items      []OrderItemInput
}

type OrderItemInput struct {
	ProductID   string
	ProductName string
	UnitPrice   int64 // cents
	Currency    string
	Quantity    int
}

// ConfirmOrderCommand
type ConfirmOrderCommand struct {
	OrderID string
}

// CancelOrderCommand
type CancelOrderCommand struct {
	OrderID string
	Reason  string
}

// ============================================================
// QUERIES — ดึงข้อมูล (ไม่เปลี่ยน state)
// ============================================================

// GetOrderQuery
type GetOrderQuery struct {
	OrderID string
}

// GetOrderResult
type GetOrderResult struct {
	ID          string
	CustomerID  string
	Status      string
	TotalAmount string
	Items       []OrderItemResult
}

type OrderItemResult struct {
	ProductID   string
	ProductName string
	UnitPrice   string
	Quantity    int
	Subtotal    string
}

// ============================================================
// COMMAND HANDLERS
// ============================================================

// OrderCommandHandler — handles all order commands
type OrderCommandHandler struct {
	repo     domain.OrderRepository
	eventBus domain.EventBus
	logger   *slog.Logger
}

func NewOrderCommandHandler(
	repo domain.OrderRepository,
	eventBus domain.EventBus,
	logger *slog.Logger,
) *OrderCommandHandler {
	return &OrderCommandHandler{
		repo:     repo,
		eventBus: eventBus,
		logger:   logger,
	}
}

// HandlePlaceOrder — Place Order use case
func (h *OrderCommandHandler) HandlePlaceOrder(ctx context.Context, cmd PlaceOrderCommand) error {
	h.logger.Info("Placing order", "order_id", cmd.OrderID, "customer_id", cmd.CustomerID)

	// Step 1: สร้าง value objects
	orderID, err := domain.NewOrderID(cmd.OrderID)
	if err != nil {
		return fmt.Errorf("invalid order ID: %w", err)
	}

	customerID, err := domain.NewCustomerID(cmd.CustomerID)
	if err != nil {
		return fmt.Errorf("invalid customer ID: %w", err)
	}

	// Step 2: สร้าง items
	items := make([]domain.OrderItem, 0, len(cmd.Items))
	for _, itemInput := range cmd.Items {
		price, err := domain.NewMoney(itemInput.UnitPrice, itemInput.Currency)
		if err != nil {
			return fmt.Errorf("invalid price for product %s: %w", itemInput.ProductID, err)
		}

		item, err := domain.NewOrderItem(
			itemInput.ProductID,
			itemInput.ProductName,
			price,
			itemInput.Quantity,
		)
		if err != nil {
			return fmt.Errorf("invalid item %s: %w", itemInput.ProductID, err)
		}

		items = append(items, item)
	}

	// Step 3: สร้าง Order aggregate (business logic อยู่ใน domain)
	order, err := domain.PlaceOrder(orderID, customerID, items)
	if err != nil {
		return fmt.Errorf("placing order: %w", err)
	}

	// Step 4: บันทึกลง repository
	if err := h.repo.Save(ctx, order); err != nil {
		return fmt.Errorf("saving order: %w", err)
	}

	// Step 5: Publish domain events
	events := order.UncommittedEvents()
	if len(events) > 0 {
		if err := h.eventBus.Publish(ctx, events...); err != nil {
			// Log แต่ไม่ fail — events อาจ republish ได้
			h.logger.Error("Failed to publish events",
				"order_id", cmd.OrderID,
				"error", err)
		}
		order.ClearEvents()
	}

	h.logger.Info("Order placed successfully", "order_id", cmd.OrderID)
	return nil
}

// HandleConfirmOrder — Confirm Order use case
func (h *OrderCommandHandler) HandleConfirmOrder(ctx context.Context, cmd ConfirmOrderCommand) error {
	// Step 1: Load aggregate
	orderID, err := domain.NewOrderID(cmd.OrderID)
	if err != nil {
		return fmt.Errorf("invalid order ID: %w", err)
	}

	order, err := h.repo.FindByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("finding order: %w", err)
	}

	// Step 2: Execute domain method (business logic ใน domain)
	if err := order.Confirm(); err != nil {
		return fmt.Errorf("confirming order: %w", err)
	}

	// Step 3: บันทึก
	if err := h.repo.Save(ctx, order); err != nil {
		return fmt.Errorf("saving order: %w", err)
	}

	// Step 4: Publish events
	if err := h.eventBus.Publish(ctx, order.UncommittedEvents()...); err != nil {
		h.logger.Error("Failed to publish events", "error", err)
	}
	order.ClearEvents()

	return nil
}

// HandleCancelOrder — Cancel Order use case
func (h *OrderCommandHandler) HandleCancelOrder(ctx context.Context, cmd CancelOrderCommand) error {
	orderID, err := domain.NewOrderID(cmd.OrderID)
	if err != nil {
		return fmt.Errorf("invalid order ID: %w", err)
	}

	order, err := h.repo.FindByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("finding order: %w", err)
	}

	if err := order.Cancel(cmd.Reason); err != nil {
		return fmt.Errorf("cancelling order: %w", err)
	}

	if err := h.repo.Save(ctx, order); err != nil {
		return fmt.Errorf("saving order: %w", err)
	}

	if err := h.eventBus.Publish(ctx, order.UncommittedEvents()...); err != nil {
		h.logger.Error("Failed to publish events", "error", err)
	}
	order.ClearEvents()

	return nil
}

// ============================================================
// QUERY HANDLERS
// ============================================================

// OrderQueryHandler — handles order queries
type OrderQueryHandler struct {
	repo   domain.OrderRepository
	logger *slog.Logger
}

func NewOrderQueryHandler(repo domain.OrderRepository, logger *slog.Logger) *OrderQueryHandler {
	return &OrderQueryHandler{repo: repo, logger: logger}
}

// HandleGetOrder — Get single order
func (h *OrderQueryHandler) HandleGetOrder(ctx context.Context, query GetOrderQuery) (*GetOrderResult, error) {
	orderID, err := domain.NewOrderID(query.OrderID)
	if err != nil {
		return nil, fmt.Errorf("invalid order ID: %w", err)
	}

	order, err := h.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("finding order: %w", err)
	}

	// Map domain object → DTO
	return mapOrderToResult(order)
}

// mapOrderToResult แปลง domain object เป็น DTO
func mapOrderToResult(order *domain.Order) (*GetOrderResult, error) {
	total, err := order.TotalAmount()
	if err != nil {
		return nil, err
	}

	itemResults := make([]OrderItemResult, 0, order.ItemCount())
	for _, item := range order.Items() {
		itemResults = append(itemResults, OrderItemResult{
			ProductID:   item.ProductID(),
			ProductName: item.ProductName(),
			UnitPrice:   item.UnitPrice().String(),
			Quantity:    item.Quantity(),
			Subtotal:    item.Subtotal().String(),
		})
	}

	return &GetOrderResult{
		ID:          order.ID().String(),
		CustomerID:  order.CustomerID().String(),
		Status:      string(order.Status()),
		TotalAmount: total.String(),
		Items:       itemResults,
	}, nil
}
