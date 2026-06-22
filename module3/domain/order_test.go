// Package domain — Unit Tests สำหรับ Order Aggregate
package domain

import (
	"testing"
)

// ============================================================
// HELPER FUNCTIONS
// ============================================================

func mustNewMoney(amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

func makeTestItems() []OrderItem {
	item1, _ := NewOrderItem("P001", "MacBook Pro", mustNewMoney(10000000, "THB"), 1)
	item2, _ := NewOrderItem("P002", "iPhone 15", mustNewMoney(3500000, "THB"), 2)
	return []OrderItem{item1, item2}
}

// ============================================================
// ORDER CREATION TESTS
// ============================================================

func TestPlaceOrder_Success(t *testing.T) {
	orderID, _ := NewOrderID("ORD-001")
	customerID, _ := NewCustomerID("CUST-001")
	items := makeTestItems()

	order, err := PlaceOrder(orderID, customerID, items)

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	if order.Status() != OrderStatusPending {
		t.Errorf("Expected PENDING, got %s", order.Status())
	}

	if order.ItemCount() != 2 {
		t.Errorf("Expected 2 items, got %d", order.ItemCount())
	}

	// ตรวจสอบ domain event ถูก fire
	events := order.UncommittedEvents()
	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}
	if events[0].EventName() != "order.placed" {
		t.Errorf("Expected order.placed event, got %s", events[0].EventName())
	}
}

func TestPlaceOrder_EmptyItems(t *testing.T) {
	orderID, _ := NewOrderID("ORD-001")
	customerID, _ := NewCustomerID("CUST-001")

	_, err := PlaceOrder(orderID, customerID, []OrderItem{})

	if err == nil {
		t.Error("Expected error for empty items")
	}
}

func TestPlaceOrder_EmptyOrderID(t *testing.T) {
	_, err := NewOrderID("")
	if err == nil {
		t.Error("Expected error for empty order ID")
	}
}

// ============================================================
// STATE MACHINE TESTS
// ============================================================

func TestOrderConfirm_FromPending(t *testing.T) {
	orderID, _ := NewOrderID("ORD-001")
	customerID, _ := NewCustomerID("CUST-001")
	order, _ := PlaceOrder(orderID, customerID, makeTestItems())

	err := order.Confirm()

	if err != nil {
		t.Fatalf("Expected success, got: %v", err)
	}
	if order.Status() != OrderStatusConfirmed {
		t.Errorf("Expected CONFIRMED, got %s", order.Status())
	}
}

func TestOrderConfirm_FromCancelled_Fails(t *testing.T) {
	orderID, _ := NewOrderID("ORD-001")
	customerID, _ := NewCustomerID("CUST-001")
	order, _ := PlaceOrder(orderID, customerID, makeTestItems())

	_ = order.Cancel("test reason")

	err := order.Confirm()

	if err == nil {
		t.Error("Expected error when confirming cancelled order")
	}
}

func TestOrderCancel_RequiresReason(t *testing.T) {
	orderID, _ := NewOrderID("ORD-001")
	customerID, _ := NewCustomerID("CUST-001")
	order, _ := PlaceOrder(orderID, customerID, makeTestItems())

	err := order.Cancel("")

	if err == nil {
		t.Error("Expected error for empty cancellation reason")
	}
}

// ============================================================
// MONEY TESTS
// ============================================================

func TestMoney_Add_SameCurrency(t *testing.T) {
	m1 := mustNewMoney(100, "THB")
	m2 := mustNewMoney(200, "THB")

	result, err := m1.Add(m2)

	if err != nil {
		t.Fatalf("Expected success: %v", err)
	}
	if result.Amount() != 300 {
		t.Errorf("Expected 300, got %d", result.Amount())
	}
}

func TestMoney_Add_DifferentCurrency_Fails(t *testing.T) {
	m1 := mustNewMoney(100, "THB")
	m2 := mustNewMoney(200, "USD")

	_, err := m1.Add(m2)

	if err == nil {
		t.Error("Expected error for different currencies")
	}
}

func TestMoney_Negative_Fails(t *testing.T) {
	_, err := NewMoney(-100, "THB")
	if err == nil {
		t.Error("Expected error for negative amount")
	}
}

// ============================================================
// TABLE-DRIVEN TESTS
// ============================================================

func TestOrderStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name     string
		from     OrderStatus
		to       OrderStatus
		expected bool
	}{
		{"Pending to Confirmed", OrderStatusPending, OrderStatusConfirmed, true},
		{"Pending to Cancelled", OrderStatusPending, OrderStatusCancelled, true},
		{"Confirmed to Shipped", OrderStatusConfirmed, OrderStatusShipped, true},
		{"Confirmed to Pending", OrderStatusConfirmed, OrderStatusPending, false},
		{"Delivered to Cancelled", OrderStatusDelivered, OrderStatusCancelled, false},
		{"Cancelled to Confirmed", OrderStatusCancelled, OrderStatusConfirmed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.from.CanTransitionTo(tt.to)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
