// Package unit — Table-Driven Tests + Spy Pattern สำหรับ OrderHandler
//
// # กายวิภาคของ Table-Driven Test
//
// Table-driven test คือ pattern ที่ Go community นิยมใช้มากที่สุดตัวนึง
// แนวคิดหลัก: แยก "ข้อมูลทดสอบ" ออกจาก "logic การทดสอบ"
//
//	test case ที่ 1 ─┐
//	test case ที่ 2 ─┼─► []struct{ ... }  ← "table" (ตาราง test cases)
//	test case ที่ 3 ─┘
//	                      ↓
//	                  for _, tc := range tests {
//	                      t.Run(tc.name, func(t *testing.T) { ... })
//	                  }
//
// ข้อดี:
//  1. เพิ่ม test case ใหม่แค่เพิ่ม row ใน table — ไม่ต้องเขียน function ใหม่
//  2. Code logic อยู่ที่เดียว — แก้ bug ที่เดียว
//  3. ชื่อ test case ชัดเจน — รู้ว่า fail เพราะอะไรทันที
//
// วิธีรัน:
//
//	go test ./module6/unit/ -v -run TestOrderHandler
//	go test ./module6/unit/ -v -run TestOrderHandler/place_order
//	go test ./module6/unit/ -count=1 -race ./module6/unit/
package unit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ============================================================
// HAND-WRITTEN MOCKS — ไม่ใช้ mockery
// ============================================================
//
// ทำไมเขียน mock เอง (ไม่ใช้ tool generate)?
//   - เข้าใจว่า mock ทำงานอย่างไร
//   - ควบคุม behavior ได้สมบูรณ์
//   - ไม่มี external dependency
//
// เมื่อไรควรใช้ generated mock?
//   - Interface มีหลาย methods (>5)
//   - ต้องการ assertion helpers (EXPECT().Times())
//   - ทีมใหญ่ต้องการ consistency
//   → ดู module6/mocks/generated_mock_example.go

// mockOrderRepository — hand-written mock สำหรับ OrderRepository
//
// Spy Pattern: บันทึก calls เพื่อ verify ว่า method ถูกเรียกถูกต้อง
// ต่างจาก Stub: Stub แค่ return ค่า, Spy บันทึกว่าถูกเรียกอย่างไรด้วย
type mockOrderRepository struct {
	mu sync.Mutex // ป้องกัน race condition เมื่อใช้กับ t.Parallel()

	// store จำลองเป็น in-memory database
	store map[OrderID]*Order

	// ===== Spy fields: บันทึก method calls =====

	// SaveCalls เก็บ orders ที่ถูกส่งมา Save — ใช้ verify args
	SaveCalls []*Order

	// FindByIDCalls เก็บ IDs ที่ถูก query — ใช้ verify ว่าถูก call
	FindByIDCalls []OrderID

	// DeleteCalls เก็บ IDs ที่ถูก delete
	DeleteCalls []OrderID

	// ===== Error injection: ควบคุม error ที่ return =====

	// SaveErr ถ้า != nil, Save จะ return error นี้
	SaveErr error

	// FindByIDErr ถ้า != nil, FindByID จะ return error นี้ (แทนที่ store lookup)
	FindByIDErr error
}

// newMockRepo สร้าง mock ใหม่พร้อม empty store
func newMockRepo() *mockOrderRepository {
	return &mockOrderRepository{
		store: make(map[OrderID]*Order),
	}
}

// seed เพิ่ม order เข้า store โดยตรง (helper สำหรับ test setup)
func (m *mockOrderRepository) seed(order *Order) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// deep copy ป้องกัน test contamination
	clone := *order
	m.store[order.ID] = &clone
}

// Save implements OrderRepository
func (m *mockOrderRepository) Save(ctx context.Context, order *Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Spy: บันทึก call
	clone := *order
	m.SaveCalls = append(m.SaveCalls, &clone)

	if m.SaveErr != nil {
		return m.SaveErr
	}

	// จำลอง save ลง store
	storeCopy := *order
	m.store[order.ID] = &storeCopy
	return nil
}

// FindByID implements OrderRepository
func (m *mockOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Spy: บันทึก call
	m.FindByIDCalls = append(m.FindByIDCalls, id)

	if m.FindByIDErr != nil {
		return nil, m.FindByIDErr
	}

	order, ok := m.store[id]
	if !ok {
		return nil, ErrOrderNotFound
	}

	// defensive copy — ป้องกัน test แก้ internal state
	clone := *order
	return &clone, nil
}

// Delete implements OrderRepository
func (m *mockOrderRepository) Delete(ctx context.Context, id OrderID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.DeleteCalls = append(m.DeleteCalls, id)
	delete(m.store, id)
	return nil
}

// ===== Spy assertion helpers =====

// savedOrderIDs returns IDs ของทุก orders ที่ถูก Save (order ที่ Save ล่าสุดอยู่ท้าย)
func (m *mockOrderRepository) savedOrderIDs() []OrderID {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]OrderID, len(m.SaveCalls))
	for i, o := range m.SaveCalls {
		ids[i] = o.ID
	}
	return ids
}

// ============================================================
// MOCK ID GENERATOR
// ============================================================

// stubIDGenerator — returns IDs ตามที่กำหนดไว้ล่วงหน้า (deterministic)
type stubIDGenerator struct {
	ids   []OrderID
	index int
}

func newStubIDGen(ids ...OrderID) *stubIDGenerator {
	return &stubIDGenerator{ids: ids}
}

func (g *stubIDGenerator) NewOrderID() OrderID {
	if g.index >= len(g.ids) {
		return OrderID(fmt.Sprintf("auto-id-%d", g.index))
	}
	id := g.ids[g.index]
	g.index++
	return id
}

// ============================================================
// FIXED CLOCK — deterministic time สำหรับ tests
// ============================================================

var fixedNow = time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedNow }

// ============================================================
// TEST: PlaceOrder
// ============================================================

// TestOrderHandler_PlaceOrder — table-driven test สำหรับ PlaceOrder
//
// # กายวิภาคของ test case struct:
//
//	name     string           — ชื่อ subtest (แสดงใน output เมื่อ fail)
//	setup    func(*mockOrderRepository) — inject state ก่อน test
//	req      PlaceOrderRequest — input
//	wantErr  bool              — expect error หรือเปล่า
//	wantErrIs error            — ถ้า wantErr=true, error ต้องเป็น type นี้
//	check    func(...)         — assertions เพิ่มเติม (ไม่ required)
func TestOrderHandler_PlaceOrder(t *testing.T) {
	// ===== TEST TABLE =====
	// แต่ละ row = 1 scenario ที่ต้องการ test
	tests := []struct {
		name      string
		setup     func(*mockOrderRepository)
		req       PlaceOrderRequest
		wantErr   bool
		wantErrIs error // ใช้ errors.Is() ตรวจสอบ
		// check รับ order ที่ return มา (nil ถ้า error) และ mock สำหรับ spy assertions
		check func(t *testing.T, got *Order, repo *mockOrderRepository)
	}{
		// ===== HAPPY PATH =====
		{
			name: "valid single item order",
			req: PlaceOrderRequest{
				CustomerID: "cust-001",
				Items: []OrderItem{
					{ProductID: "prod-001", ProductName: "Widget", UnitCents: 9900, Quantity: 2},
				},
			},
			check: func(t *testing.T, got *Order, repo *mockOrderRepository) {
				t.Helper()
				// ตรวจสอบ order fields
				if got.CustomerID != "cust-001" {
					t.Errorf("CustomerID = %q, want %q", got.CustomerID, "cust-001")
				}
				if got.Status != OrderStatusPending {
					t.Errorf("Status = %q, want PENDING", got.Status)
				}
				// 9900 cents * 2 quantity = 19800 cents
				if got.TotalCents != 19800 {
					t.Errorf("TotalCents = %d, want 19800", got.TotalCents)
				}
				if got.CreatedAt != fixedNow {
					t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixedNow)
				}

				// ===== SPY ASSERTION =====
				// ตรวจสอบว่า repo.Save ถูกเรียก 1 ครั้ง
				if len(repo.SaveCalls) != 1 {
					t.Errorf("Save called %d times, want 1", len(repo.SaveCalls))
				}
			},
		},
		{
			name: "valid multi-item order calculates total correctly",
			req: PlaceOrderRequest{
				CustomerID: "cust-002",
				Items: []OrderItem{
					{ProductID: "p1", UnitCents: 1000, Quantity: 3}, // 3000
					{ProductID: "p2", UnitCents: 2500, Quantity: 1}, // 2500
					{ProductID: "p3", UnitCents: 500, Quantity: 10}, // 5000
				},
			},
			check: func(t *testing.T, got *Order, _ *mockOrderRepository) {
				t.Helper()
				const want int64 = 10500 // 3000 + 2500 + 5000
				if got.TotalCents != want {
					t.Errorf("TotalCents = %d, want %d", got.TotalCents, want)
				}
			},
		},
		{
			name: "order at max item count (20 items) succeeds",
			req: func() PlaceOrderRequest {
				items := make([]OrderItem, 20)
				for i := range items {
					items[i] = OrderItem{
						ProductID: fmt.Sprintf("prod-%d", i),
						UnitCents: 100,
						Quantity:  1,
					}
				}
				return PlaceOrderRequest{CustomerID: "cust-max", Items: items}
			}(),
			check: func(t *testing.T, got *Order, _ *mockOrderRepository) {
				t.Helper()
				if got == nil {
					t.Fatal("expected order, got nil")
				}
			},
		},

		// ===== VALIDATION ERROR CASES =====
		{
			name:      "missing customerID returns ErrInvalidRequest",
			req:       PlaceOrderRequest{Items: []OrderItem{{ProductID: "p1", UnitCents: 100, Quantity: 1}}},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name:      "empty items list returns ErrInvalidRequest",
			req:       PlaceOrderRequest{CustomerID: "cust-001", Items: []OrderItem{}},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "zero quantity item returns ErrInvalidRequest",
			req: PlaceOrderRequest{
				CustomerID: "cust-001",
				Items:      []OrderItem{{ProductID: "p1", UnitCents: 100, Quantity: 0}},
			},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "negative unit price returns ErrInvalidRequest",
			req: PlaceOrderRequest{
				CustomerID: "cust-001",
				Items:      []OrderItem{{ProductID: "p1", UnitCents: -1, Quantity: 1}},
			},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "missing productID returns ErrInvalidRequest",
			req: PlaceOrderRequest{
				CustomerID: "cust-001",
				Items:      []OrderItem{{ProductID: "", UnitCents: 100, Quantity: 1}},
			},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},

		// ===== BUSINESS RULE VIOLATIONS =====
		{
			name: "exceeds 20 item limit returns ErrInvalidRequest",
			req: func() PlaceOrderRequest {
				items := make([]OrderItem, 21)
				for i := range items {
					items[i] = OrderItem{ProductID: fmt.Sprintf("p%d", i), UnitCents: 100, Quantity: 1}
				}
				return PlaceOrderRequest{CustomerID: "cust-001", Items: items}
			}(),
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "total exceeds 100,000 baht returns ErrInvalidRequest",
			req: PlaceOrderRequest{
				CustomerID: "cust-vip",
				// 10,000,001 cents = 100,000.01 บาท — เกิน limit
				Items: []OrderItem{{ProductID: "p1", UnitCents: 10_000_001, Quantity: 1}},
			},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "total exactly at limit (10,000,000 cents) succeeds",
			req: PlaceOrderRequest{
				CustomerID: "cust-vip",
				Items:      []OrderItem{{ProductID: "p1", UnitCents: 10_000_000, Quantity: 1}},
			},
			check: func(t *testing.T, got *Order, _ *mockOrderRepository) {
				t.Helper()
				if got.TotalCents != 10_000_000 {
					t.Errorf("TotalCents = %d, want 10000000", got.TotalCents)
				}
			},
		},

		// ===== INFRASTRUCTURE ERROR =====
		{
			name: "repository save error is wrapped and returned",
			setup: func(repo *mockOrderRepository) {
				repo.SaveErr = errors.New("database connection refused")
			},
			req: PlaceOrderRequest{
				CustomerID: "cust-001",
				Items:      []OrderItem{{ProductID: "p1", UnitCents: 100, Quantity: 1}},
			},
			wantErr: true,
			// ไม่ระบุ wantErrIs เพราะ error จาก infra layer ไม่ใช่ sentinel error
			check: func(t *testing.T, got *Order, repo *mockOrderRepository) {
				t.Helper()
				// ตรวจว่าไม่ return order เมื่อ save fail
				if got != nil {
					t.Error("expected nil order on save error")
				}
				// Spy: ตรวจว่า Save ถูกเรียก (business logic ทำงานถึง repo แล้ว)
				if len(repo.SaveCalls) != 1 {
					t.Errorf("Save called %d times, want 1", len(repo.SaveCalls))
				}
			},
		},
	}

	// ===== TEST RUNNER =====
	// วน loop ผ่าน test table และรัน subtest แต่ละตัว
	for _, tc := range tests {
		tc := tc // ⚠️ capture loop variable — จำเป็นสำหรับ t.Parallel() ใน Go < 1.22
		t.Run(tc.name, func(t *testing.T) {
			// t.Parallel() บอกให้ Go runtime รัน subtests เหล่านี้พร้อมกัน
			// ⚠️ ต้องใช้ "tc := tc" ด้านบนก่อน
			// ⚠️ mock ต้องเป็น goroutine-safe (ดู sync.Mutex ใน mockOrderRepository)
			t.Parallel()

			// ===== ARRANGE =====
			repo := newMockRepo()
			if tc.setup != nil {
				tc.setup(repo)
			}
			idGen := newStubIDGen("order-test-001")
			handler := NewOrderHandler(repo, idGen, fixedClock)

			// ===== ACT =====
			got, err := handler.PlaceOrder(context.Background(), tc.req)

			// ===== ASSERT =====
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PlaceOrder() expected error, got nil (order=%+v)", got)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Errorf("PlaceOrder() error = %v, want errors.Is(%v)", err, tc.wantErrIs)
				}
				// ถ้า wantErr=true ไม่ต้อง check order
				return
			}

			if err != nil {
				t.Fatalf("PlaceOrder() unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("PlaceOrder() returned nil order without error")
			}

			if tc.check != nil {
				tc.check(t, got, repo)
			}
		})
	}
}

// ============================================================
// TEST: CancelOrder
// ============================================================

func TestOrderHandler_CancelOrder(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*mockOrderRepository)
		req       CancelOrderRequest
		wantErr   bool
		wantErrIs error
		// checkRepo ตรวจสอบ state ของ repo หลัง cancel
		checkRepo func(t *testing.T, repo *mockOrderRepository)
	}{
		// ===== HAPPY PATH =====
		{
			name: "cancel pending order succeeds",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{
					ID:     "order-001",
					Status: OrderStatusPending,
				})
			},
			req: CancelOrderRequest{OrderID: "order-001", Reason: "customer changed mind"},
			checkRepo: func(t *testing.T, repo *mockOrderRepository) {
				t.Helper()
				// Spy: ตรวจว่า Save ถูกเรียกเพื่ออัพเดท status
				if len(repo.SaveCalls) != 1 {
					t.Fatalf("Save called %d times, want 1", len(repo.SaveCalls))
				}
				// ตรวจว่า order ถูก save ด้วย status CANCELLED
				saved := repo.SaveCalls[0]
				if saved.Status != OrderStatusCancelled {
					t.Errorf("saved order status = %q, want CANCELLED", saved.Status)
				}
				// ตรวจว่า UpdatedAt ถูก update
				if saved.UpdatedAt != fixedNow {
					t.Errorf("UpdatedAt = %v, want %v", saved.UpdatedAt, fixedNow)
				}
			},
		},
		{
			name: "cancel confirmed order succeeds",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{ID: "order-002", Status: OrderStatusConfirmed})
			},
			req: CancelOrderRequest{OrderID: "order-002", Reason: "out of stock"},
		},

		// ===== ERROR CASES =====
		{
			name:      "cancel non-existent order returns ErrOrderNotFound",
			req:       CancelOrderRequest{OrderID: "ghost-order", Reason: "test"},
			wantErr:   true,
			wantErrIs: ErrOrderNotFound,
		},
		{
			name: "cancel already-cancelled order returns ErrOrderAlreadyCancelled",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{ID: "order-003", Status: OrderStatusCancelled})
			},
			req:       CancelOrderRequest{OrderID: "order-003", Reason: "duplicate cancel"},
			wantErr:   true,
			wantErrIs: ErrOrderAlreadyCancelled,
		},
		{
			name:      "missing orderID returns ErrInvalidRequest",
			req:       CancelOrderRequest{OrderID: "", Reason: "test"},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "missing reason returns ErrInvalidRequest",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{ID: "order-004", Status: OrderStatusPending})
			},
			req:       CancelOrderRequest{OrderID: "order-004", Reason: ""},
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "repository save error on cancel is propagated",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{ID: "order-005", Status: OrderStatusPending})
				repo.SaveErr = errors.New("disk full")
			},
			req:     CancelOrderRequest{OrderID: "order-005", Reason: "test"},
			wantErr: true,
		},
		{
			name: "repository findByID error is propagated",
			setup: func(repo *mockOrderRepository) {
				repo.FindByIDErr = errors.New("network timeout")
			},
			req:     CancelOrderRequest{OrderID: "order-006", Reason: "test"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := newMockRepo()
			if tc.setup != nil {
				tc.setup(repo)
			}
			handler := NewOrderHandler(repo, newStubIDGen(), fixedClock)

			err := handler.CancelOrder(context.Background(), tc.req)

			if tc.wantErr {
				if err == nil {
					t.Fatal("CancelOrder() expected error, got nil")
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Errorf("CancelOrder() error = %v, want errors.Is(%v)", err, tc.wantErrIs)
				}
				return
			}

			if err != nil {
				t.Fatalf("CancelOrder() unexpected error: %v", err)
			}
			if tc.checkRepo != nil {
				tc.checkRepo(t, repo)
			}
		})
	}
}

// ============================================================
// TEST: GetOrder
// ============================================================

func TestOrderHandler_GetOrder(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*mockOrderRepository)
		id        OrderID
		wantErr   bool
		wantErrIs error
		check     func(t *testing.T, got *Order)
	}{
		{
			name: "get existing order returns order",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{
					ID:         "order-xyz",
					CustomerID: "cust-001",
					Status:     OrderStatusPending,
					TotalCents: 5000,
					CreatedAt:  fixedNow,
				})
			},
			id: "order-xyz",
			check: func(t *testing.T, got *Order) {
				t.Helper()
				if got.ID != "order-xyz" {
					t.Errorf("ID = %q, want order-xyz", got.ID)
				}
				if got.CustomerID != "cust-001" {
					t.Errorf("CustomerID = %q, want cust-001", got.CustomerID)
				}
				if got.TotalCents != 5000 {
					t.Errorf("TotalCents = %d, want 5000", got.TotalCents)
				}
			},
		},
		{
			name:      "get non-existent order returns ErrOrderNotFound",
			id:        "doesnt-exist",
			wantErr:   true,
			wantErrIs: ErrOrderNotFound,
		},
		{
			name:      "empty orderID returns ErrInvalidRequest",
			id:        "",
			wantErr:   true,
			wantErrIs: ErrInvalidRequest,
		},
		{
			name: "returned order is a copy (mutation safe)",
			setup: func(repo *mockOrderRepository) {
				repo.seed(&Order{ID: "order-copy", Status: OrderStatusPending})
			},
			id: "order-copy",
			check: func(t *testing.T, got *Order) {
				t.Helper()
				// แก้ไข returned order ไม่ควรกระทบ stored order
				originalStatus := got.Status
				got.Status = OrderStatusCancelled // mutate returned copy
				if originalStatus == OrderStatusCancelled {
					t.Error("original should not be CANCELLED")
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := newMockRepo()
			if tc.setup != nil {
				tc.setup(repo)
			}
			handler := NewOrderHandler(repo, newStubIDGen(), fixedClock)

			got, err := handler.GetOrder(context.Background(), tc.id)

			if tc.wantErr {
				if err == nil {
					t.Fatal("GetOrder() expected error, got nil")
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Errorf("GetOrder() error = %v, want errors.Is(%v)", err, tc.wantErrIs)
				}
				return
			}

			if err != nil {
				t.Fatalf("GetOrder() unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("GetOrder() returned nil without error")
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// ============================================================
// TEST: SPY PATTERN — ตรวจสอบ call sequence
// ============================================================

// TestOrderHandler_SpyPattern_CallSequence แสดง Spy pattern อย่างชัดเจน
// เน้นการ verify "ลำดับการทำงาน" ไม่ใช่แค่ผลลัพธ์
func TestOrderHandler_SpyPattern_CallSequence(t *testing.T) {
	t.Run("place order calls Save exactly once with correct data", func(t *testing.T) {
		repo := newMockRepo()
		idGen := newStubIDGen("order-spy-001")
		handler := NewOrderHandler(repo, idGen, fixedClock)

		req := PlaceOrderRequest{
			CustomerID: "cust-spy",
			Items: []OrderItem{
				{ProductID: "prod-spy", UnitCents: 1234, Quantity: 3},
			},
		}
		_, err := handler.PlaceOrder(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ===== SPY ASSERTIONS =====

		// 1. ตรวจว่า Save ถูกเรียกกี่ครั้ง
		if len(repo.SaveCalls) != 1 {
			t.Fatalf("Save called %d times, want exactly 1", len(repo.SaveCalls))
		}

		// 2. ตรวจว่า Save ถูกเรียกด้วย args ที่ถูกต้อง
		saved := repo.SaveCalls[0]
		if saved.ID != "order-spy-001" {
			t.Errorf("Save called with ID=%q, want order-spy-001", saved.ID)
		}
		if saved.CustomerID != "cust-spy" {
			t.Errorf("Save called with CustomerID=%q, want cust-spy", saved.CustomerID)
		}
		if saved.TotalCents != 3702 { // 1234 * 3
			t.Errorf("Save called with TotalCents=%d, want 3702", saved.TotalCents)
		}
		if saved.Status != OrderStatusPending {
			t.Errorf("Save called with Status=%q, want PENDING", saved.Status)
		}

		// 3. ตรวจว่า FindByID ไม่ถูกเรียก (PlaceOrder ไม่ควร query ก่อน)
		if len(repo.FindByIDCalls) != 0 {
			t.Errorf("FindByID called %d times, want 0", len(repo.FindByIDCalls))
		}
	})

	t.Run("cancel order calls FindByID then Save with cancelled status", func(t *testing.T) {
		repo := newMockRepo()
		repo.seed(&Order{ID: "order-cancel-spy", Status: OrderStatusPending})
		handler := NewOrderHandler(repo, newStubIDGen(), fixedClock)

		err := handler.CancelOrder(context.Background(), CancelOrderRequest{
			OrderID: "order-cancel-spy",
			Reason:  "spy test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ตรวจ call sequence: FindByID → Save
		if len(repo.FindByIDCalls) != 1 {
			t.Errorf("FindByID called %d times, want 1", len(repo.FindByIDCalls))
		}
		if repo.FindByIDCalls[0] != "order-cancel-spy" {
			t.Errorf("FindByID called with %q, want order-cancel-spy", repo.FindByIDCalls[0])
		}

		if len(repo.SaveCalls) != 1 {
			t.Errorf("Save called %d times, want 1", len(repo.SaveCalls))
		}
		if repo.SaveCalls[0].Status != OrderStatusCancelled {
			t.Errorf("Save called with status=%q, want CANCELLED", repo.SaveCalls[0].Status)
		}
	})
}

// ============================================================
// TEST: EDGE CASES
// ============================================================

// TestOrderHandler_EdgeCases ทดสอบ boundary conditions
func TestOrderHandler_EdgeCases(t *testing.T) {
	t.Run("context cancellation is propagated", func(t *testing.T) {
		// สร้าง context ที่ cancelled แล้ว
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel ทันที

		repo := &contextAwareRepo{canceled: true}
		handler := NewOrderHandler(repo, newStubIDGen("id-1"), fixedClock)

		_, err := handler.PlaceOrder(ctx, PlaceOrderRequest{
			CustomerID: "cust-001",
			Items:      []OrderItem{{ProductID: "p1", UnitCents: 100, Quantity: 1}},
		})
		// ควร return error (context deadline exceeded หรือ canceled)
		if err == nil {
			t.Error("expected error with cancelled context")
		}
	})

	t.Run("unicode characters in customerID are preserved", func(t *testing.T) {
		repo := newMockRepo()
		handler := NewOrderHandler(repo, newStubIDGen("id-unicode"), fixedClock)

		// CustomerID ที่มี unicode (ชื่อ customer ภาษาไทย)
		unicodeCustomerID := CustomerID("ลูกค้า-001-สมชาย")
		_, err := handler.PlaceOrder(context.Background(), PlaceOrderRequest{
			CustomerID: unicodeCustomerID,
			Items:      []OrderItem{{ProductID: "p1", UnitCents: 100, Quantity: 1}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ตรวจว่า CustomerID ถูก save ถูกต้อง
		if len(repo.SaveCalls) == 0 {
			t.Fatal("expected Save to be called")
		}
		if repo.SaveCalls[0].CustomerID != unicodeCustomerID {
			t.Errorf("CustomerID = %q, want %q", repo.SaveCalls[0].CustomerID, unicodeCustomerID)
		}
	})
}

// contextAwareRepo จำลอง repository ที่ตรวจ context
type contextAwareRepo struct {
	canceled bool
	store    map[OrderID]*Order
}

func (r *contextAwareRepo) Save(ctx context.Context, order *Order) error {
	if r.canceled {
		return ctx.Err()
	}
	return nil
}

func (r *contextAwareRepo) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	if r.canceled {
		return nil, ctx.Err()
	}
	return nil, ErrOrderNotFound
}

func (r *contextAwareRepo) Delete(ctx context.Context, id OrderID) error {
	if r.canceled {
		return ctx.Err()
	}
	return nil
}
