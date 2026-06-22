// Package mocks — ตัวอย่าง Generated Mock จาก mockery
//
// ⚠️ File นี้ต้องการ testify dependency ก่อน compile:
//
//	go get github.com/stretchr/testify/mock
//
// File นี้ใช้ build tag "mockery" เพื่อไม่ถูก compile โดย default
// เพิ่ม flag เมื่อ instructor ต้องการ demo:
//
//	go build -tags=mockery ./module6/mocks/
//	go test  -tags=mockery ./module6/mocks/
//
//go:build mockery

// # Mockery คืออะไร?
//
// mockery เป็น tool ที่ generate mock code จาก Go interface อัตโนมัติ
// คล้ายกับที่เราเขียน mock ด้วยมือใน unit package แต่ tool ทำให้แทน
//
// # วิธีติดตั้ง mockery
//
//	go install github.com/vektra/mockery/v2@latest
//
// # วิธีสร้าง mock สำหรับ interface นี้
//
// วิธีที่ 1: CLI ตรงๆ
//
//	mockery --name=OrderRepository --dir=./module6/unit --output=./module6/mocks --outpkg=mocks
//
// วิธีที่ 2: ใช้ .mockery.yaml config file (แนะนำสำหรับ project ใหญ่)
//
//	# .mockery.yaml
//	with-expecter: true
//	packages:
//	  tx_go_training/module6/unit:
//	    interfaces:
//	      OrderRepository:
//
//	mockery  # รันจาก root project
//
// วิธีที่ 3: go:generate directive ใน source file
//
//	//go:generate mockery --name=OrderRepository
//	go generate ./module6/unit/...
//
// # เมื่อไรควรใช้ generated mock vs hand-written mock?
//
// Generated mock (mockery):
//
//	✓ Interface มีหลาย methods (5+ methods)
//	✓ ต้องการ EXPECT() chains สำหรับ flexible assertions
//	✓ ทีมใหญ่ต้องการ consistency
//	✓ CI/CD regenerates ทุกครั้ง (ป้องกัน mock drift)
//
// Hand-written mock:
//
//	✓ Interface เล็ก (1-3 methods)
//	✓ ต้องการ custom spy logic ซับซ้อน
//	✓ ไม่อยากมี external tool dependency
//	✓ Learning/teaching context
//
// NOTE: File นี้เขียนขึ้นเพื่อ illustrate รูปแบบที่ mockery generate ออกมา
// ในโปรเจคจริงคุณจะไม่เขียนไฟล์นี้เอง — ให้ AI หรือ tool generate แทน
package mocks

import (
	"context"
	"sync"

	"github.com/stretchr/testify/mock"

	"tx_go_training/module6/unit"
)

// MockOrderRepository — mock ที่ mockery จะ generate ให้
//
// mockery version: v2.x
// source interface: tx_go_training/module6/unit.OrderRepository
//
// สิ่งที่ mockery เพิ่มให้นอกจาก mock เปล่าๆ:
//  1. EXPECT() — type-safe expectation builder
//  2. Called() — ตรวจว่า method ถูกเรียกหรือเปล่า
//  3. Times() — ตรวจจำนวนครั้งที่ถูกเรียก
//  4. Return() — กำหนด return value
//  5. Run() — execute side effect เมื่อ method ถูกเรียก
//  6. Maybe() — method อาจถูกเรียกหรือไม่ก็ได้ (ไม่ fail ถ้าไม่ถูกเรียก)
type MockOrderRepository struct {
	mock.Mock
	mu sync.Mutex // mockery ไม่ generate mutex ให้ แต่ถ้าจะใช้ Parallel ควรเพิ่ม
}

// NewMockOrderRepository สร้าง mock ใหม่
// mockery generate constructor นี้ให้อัตโนมัติ
func NewMockOrderRepository(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockOrderRepository {
	mock := &MockOrderRepository{}
	mock.Mock.Test(t)
	t.Cleanup(func() { mock.AssertExpectations(t) })
	return mock
}

// Save implements unit.OrderRepository
func (m *MockOrderRepository) Save(ctx context.Context, order *unit.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(ctx, order)
	return args.Error(0)
}

// FindByID implements unit.OrderRepository
func (m *MockOrderRepository) FindByID(ctx context.Context, id unit.OrderID) (*unit.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(ctx, id)
	return args.Get(0).(*unit.Order), args.Error(1)
}

// Delete implements unit.OrderRepository
func (m *MockOrderRepository) Delete(ctx context.Context, id unit.OrderID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(ctx, id)
	return args.Error(0)
}

// ============================================================
// EXPECT() PATTERN — Type-safe expectation builder
// ============================================================
//
// mockery --with-expecter=true จะ generate EXPECT() builder เพิ่มเติม
// ทำให้ assertion มี type safety และ autocomplete ได้ใน IDE

// MockOrderRepository_Expecter — builder สำหรับ set expectations
// (mockery generate struct นี้ให้เมื่อใช้ --with-expecter flag)
type MockOrderRepository_Expecter struct {
	mock *mock.Mock
}

// EXPECT returns expecter builder
// ใช้แบบนี้:  mockRepo.EXPECT().Save(ctx, order).Return(nil)
func (m *MockOrderRepository) EXPECT() *MockOrderRepository_Expecter {
	return &MockOrderRepository_Expecter{mock: &m.Mock}
}

// MockOrderRepository_Save_Call — call object สำหรับ Save method
type MockOrderRepository_Save_Call struct {
	*mock.Call
}

// Save ใน Expecter ส่งกลับ call builder
func (_e *MockOrderRepository_Expecter) Save(ctx, order interface{}) *MockOrderRepository_Save_Call {
	return &MockOrderRepository_Save_Call{
		Call: _e.mock.On("Save", ctx, order),
	}
}

// Return กำหนด return value สำหรับ Save
func (_c *MockOrderRepository_Save_Call) Return(err error) *MockOrderRepository_Save_Call {
	_c.Call.Return(err)
	return _c
}

// Times กำหนดว่า method ต้องถูกเรียกกี่ครั้ง
func (_c *MockOrderRepository_Save_Call) Times(n int) *MockOrderRepository_Save_Call {
	_c.Call.Times(n)
	return _c
}

// Once เหมือน Times(1)
func (_c *MockOrderRepository_Save_Call) Once() *MockOrderRepository_Save_Call {
	_c.Call.Once()
	return _c
}

// Maybe บอกว่า method อาจถูกเรียกหรือไม่ก็ได้ (ไม่ fail ถ้าไม่ถูกเรียก)
func (_c *MockOrderRepository_Save_Call) Maybe() *MockOrderRepository_Save_Call {
	_c.Call.Maybe()
	return _c
}

// Run execute side effect เมื่อ Save ถูกเรียก
// เช่น: .Run(func(ctx context.Context, o *Order) { capturedOrder = o })
func (_c *MockOrderRepository_Save_Call) Run(fn func(ctx context.Context, order *unit.Order)) *MockOrderRepository_Save_Call {
	_c.Call.Run(func(args mock.Arguments) {
		fn(args[0].(context.Context), args[1].(*unit.Order))
	})
	return _c
}

// MockOrderRepository_FindByID_Call — call object สำหรับ FindByID
type MockOrderRepository_FindByID_Call struct {
	*mock.Call
}

// FindByID ใน Expecter
func (_e *MockOrderRepository_Expecter) FindByID(ctx, id interface{}) *MockOrderRepository_FindByID_Call {
	return &MockOrderRepository_FindByID_Call{
		Call: _e.mock.On("FindByID", ctx, id),
	}
}

// Return กำหนด return values สำหรับ FindByID
func (_c *MockOrderRepository_FindByID_Call) Return(order *unit.Order, err error) *MockOrderRepository_FindByID_Call {
	_c.Call.Return(order, err)
	return _c
}

// Once เหมือน Times(1)
func (_c *MockOrderRepository_FindByID_Call) Once() *MockOrderRepository_FindByID_Call {
	_c.Call.Once()
	return _c
}

// Maybe บอกว่าอาจถูกเรียกหรือไม่ก็ได้
func (_c *MockOrderRepository_FindByID_Call) Maybe() *MockOrderRepository_FindByID_Call {
	_c.Call.Maybe()
	return _c
}

// MockOrderRepository_Delete_Call — call object สำหรับ Delete
type MockOrderRepository_Delete_Call struct {
	*mock.Call
}

// Delete ใน Expecter
func (_e *MockOrderRepository_Expecter) Delete(ctx, id interface{}) *MockOrderRepository_Delete_Call {
	return &MockOrderRepository_Delete_Call{
		Call: _e.mock.On("Delete", ctx, id),
	}
}

// Return กำหนด return value สำหรับ Delete
func (_c *MockOrderRepository_Delete_Call) Return(err error) *MockOrderRepository_Delete_Call {
	_c.Call.Return(err)
	return _c
}

// ============================================================
// EXAMPLE TESTS USING GENERATED MOCK
// ============================================================
//
// ดู generated_mock_example_test.go สำหรับตัวอย่างการใช้งานจริง
//
// Pattern ที่ใช้ในทีม enterprise:
//
//  func TestPlaceOrder_WithGeneratedMock(t *testing.T) {
//      // 1. สร้าง mock — mock.AssertExpectations เรียกอัตโนมัติใน Cleanup
//      mockRepo := NewMockOrderRepository(t)
//
//      // 2. Set expectations ด้วย EXPECT() — type-safe, IDE autocomplete
//      mockRepo.EXPECT().
//          Save(mock.Anything, mock.MatchedBy(func(o *unit.Order) bool {
//              return o.CustomerID == "cust-001"
//          })).
//          Return(nil).
//          Once()
//
//      // 3. Inject mock เข้า handler
//      handler := unit.NewOrderHandler(mockRepo, stubIDGen, nil)
//
//      // 4. Act
//      _, err := handler.PlaceOrder(context.Background(), req)
//
//      // 5. Assert — AssertExpectations ถูกเรียกอัตโนมัติผ่าน t.Cleanup()
//      assert.NoError(t, err)
//  }
//
// ข้อดีของ EXPECT() เหนือ hand-written spy:
//   - Save(mock.Anything, mock.MatchedBy(...)) — match pattern ยืดหยุ่น
//   - .Times(n) — assert จำนวนครั้งที่ถูกเรียก
//   - AssertExpectations ตรวจ unmet expectations อัตโนมัติ
//   - .Run(...) inject side effects (เช่น populate DB ใน test)
//
// ข้อเสีย:
//   - ต้องมี github.com/stretchr/testify/mock dependency
//   - Error messages จาก mock บางทีอ่านยาก
//   - mock drift ถ้าไม่ regenerate หลัง interface เปลี่ยน
