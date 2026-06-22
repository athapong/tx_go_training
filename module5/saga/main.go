// Module 5: Saga Pattern — PlaceOrder Orchestration
//
// Saga Pattern แก้ปัญหา Distributed Transaction
// ─────────────────────────────────────────────────
// ใน Microservices เราไม่สามารถใช้ ACID transaction ข้าม service ได้
// เพราะแต่ละ service มี database ของตัวเอง
//
// Saga แก้ปัญหาโดย:
//   - แบ่ง transaction ออกเป็น steps เล็กๆ แต่ละ step มี local transaction
//   - ถ้า step ใด fail → ทำ compensating transaction ย้อนกลับ steps ก่อนหน้า
//
// สองรูปแบบหลัก:
//   1. Choreography: แต่ละ service publish event, service อื่น subscribe
//      + ง่าย, loosely coupled
//      - ยาก trace/debug, risk ของ cyclic dependency
//
//   2. Orchestration (ไฟล์นี้ใช้): มี orchestrator สั่งแต่ละ service โดยตรง
//      + ง่าย trace, centralized logic, ง่าย error handling
//      - orchestrator เป็น single point of failure, tightly coupled
//
// ตัวอย่างนี้: PlaceOrder Saga
//   Step 1: ReserveInventory → compensate: ReleaseInventory
//   Step 2: ChargePayment    → compensate: RefundPayment
//   Step 3: ConfirmOrder     → compensate: CancelOrder
//
// วิธีรัน:
//   go run module5/saga/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"
)

// ─────────────────────────────────────────────
// Domain Types
// ─────────────────────────────────────────────

// OrderRequest ข้อมูลที่ต้องการสร้าง Order
type OrderRequest struct {
	CustomerID string
	ProductID  string
	Quantity   int
	Amount     int64 // สตางค์
}

// SagaContext เก็บ state ระหว่าง saga execution
// ส่งผ่าน step ทุกตัว เพื่อให้แต่ละ step อ่าน/เขียน intermediate state ได้
type SagaContext struct {
	OrderID       string
	ReservationID string
	PaymentRef    string
	Confirmed     bool
}

// ─────────────────────────────────────────────
// Step Interface
// ─────────────────────────────────────────────

// SagaStep แต่ละขั้นตอนใน saga
// Execute: ทำ local transaction
// Compensate: ย้อนกลับ local transaction (ถ้า step ถัดไป fail)
type SagaStep struct {
	Name       string
	Execute    func(ctx context.Context, sc *SagaContext, req *OrderRequest) error
	Compensate func(ctx context.Context, sc *SagaContext, req *OrderRequest) error
}

// ─────────────────────────────────────────────
// Simulated Service Clients
// (ในโปรดักชัน นี่คือ HTTP/gRPC calls ไปยัง microservices อื่น)
// ─────────────────────────────────────────────

// InventoryService จำลอง inventory microservice
type InventoryService struct{}

// Reserve จองสินค้า (ลด available stock ชั่วคราว)
func (s *InventoryService) Reserve(productID string, qty int) (string, error) {
	slog.Info("[InventoryService] Reserving inventory",
		"product_id", productID,
		"quantity", qty,
	)
	// จำลอง latency ของ network call
	time.Sleep(100 * time.Millisecond)

	// จำลอง: inventory service ไม่ค่อย fail
	if rand.Float32() < 0.05 {
		return "", errors.New("inventory service unavailable")
	}

	reservationID := fmt.Sprintf("RES-%d", time.Now().UnixNano()%1000000)
	slog.Info("[InventoryService] Inventory reserved", "reservation_id", reservationID)
	return reservationID, nil
}

// Release คืนสินค้าที่จองไว้ (compensating action)
func (s *InventoryService) Release(reservationID string) error {
	slog.Info("[InventoryService] Releasing reservation",
		"reservation_id", reservationID,
	)
	time.Sleep(50 * time.Millisecond)
	slog.Info("[InventoryService] Reservation released")
	return nil
}

// PaymentService จำลอง payment microservice
type PaymentService struct {
	failProbability float32
}

// Charge เรียกเก็บเงิน
func (s *PaymentService) Charge(customerID string, amount int64) (string, error) {
	slog.Info("[PaymentService] Charging customer",
		"customer_id", customerID,
		"amount_thb", fmt.Sprintf("%.2f", float64(amount)/100),
	)
	time.Sleep(200 * time.Millisecond) // payment gateway ช้ากว่า services อื่น

	// จำลอง payment failure 50% เพื่อแสดง compensation
	if rand.Float32() < s.failProbability {
		return "", errors.New("payment declined: insufficient funds")
	}

	paymentRef := fmt.Sprintf("PAY-%d", time.Now().UnixNano()%1000000)
	slog.Info("[PaymentService] Payment charged", "payment_ref", paymentRef)
	return paymentRef, nil
}

// Refund คืนเงิน (compensating action)
func (s *PaymentService) Refund(paymentRef string) error {
	slog.Info("[PaymentService] Refunding payment", "payment_ref", paymentRef)
	time.Sleep(150 * time.Millisecond)
	slog.Info("[PaymentService] Payment refunded")
	return nil
}

// OrderService จำลอง order microservice
type OrderService struct{}

// Confirm ยืนยัน Order (final step)
func (s *OrderService) Confirm(orderID string) error {
	slog.Info("[OrderService] Confirming order", "order_id", orderID)
	time.Sleep(50 * time.Millisecond)

	if rand.Float32() < 0.02 { // 2% failure
		return errors.New("order service database error")
	}

	slog.Info("[OrderService] Order confirmed", "order_id", orderID)
	return nil
}

// Cancel ยกเลิก Order (compensating action)
func (s *OrderService) Cancel(orderID string, reason string) error {
	slog.Info("[OrderService] Cancelling order",
		"order_id", orderID,
		"reason", reason,
	)
	time.Sleep(50 * time.Millisecond)
	slog.Info("[OrderService] Order cancelled")
	return nil
}

// ─────────────────────────────────────────────
// Saga Orchestrator
// ─────────────────────────────────────────────

// OrderSaga orchestrates PlaceOrder saga
// ทำงานดังนี้:
//   1. รัน steps ตามลำดับ
//   2. ถ้า step ใด fail → รัน compensating steps ย้อนกลับ
//   3. compensating steps รันตามลำดับ inverse (LIFO)
type OrderSaga struct {
	steps    []SagaStep
	log      *slog.Logger
	maxRetry int // จำนวนครั้งที่ลอง compensate ถ้า compensate fail ด้วย
}

// NewOrderSaga สร้าง saga พร้อม steps
func NewOrderSaga(inv *InventoryService, pay *PaymentService, ord *OrderService) *OrderSaga {
	steps := []SagaStep{
		// ─── Step 1: Reserve Inventory ───
		{
			Name: "ReserveInventory",
			Execute: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				reservationID, err := inv.Reserve(req.ProductID, req.Quantity)
				if err != nil {
					return fmt.Errorf("reserve inventory: %w", err)
				}
				// บันทึก reservation ID เพื่อใช้ใน compensate
				sc.ReservationID = reservationID
				return nil
			},
			Compensate: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				if sc.ReservationID == "" {
					return nil // ไม่เคย reserve สำเร็จ ไม่ต้อง compensate
				}
				return inv.Release(sc.ReservationID)
			},
		},

		// ─── Step 2: Charge Payment ───
		{
			Name: "ChargePayment",
			Execute: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				paymentRef, err := pay.Charge(req.CustomerID, req.Amount)
				if err != nil {
					return fmt.Errorf("charge payment: %w", err)
				}
				sc.PaymentRef = paymentRef
				return nil
			},
			Compensate: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				if sc.PaymentRef == "" {
					return nil
				}
				return pay.Refund(sc.PaymentRef)
			},
		},

		// ─── Step 3: Confirm Order ───
		{
			Name: "ConfirmOrder",
			Execute: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				if err := ord.Confirm(sc.OrderID); err != nil {
					return fmt.Errorf("confirm order: %w", err)
				}
				sc.Confirmed = true
				return nil
			},
			Compensate: func(ctx context.Context, sc *SagaContext, req *OrderRequest) error {
				if !sc.Confirmed {
					return nil
				}
				return ord.Cancel(sc.OrderID, "saga compensation")
			},
		},
	}

	return &OrderSaga{
		steps:    steps,
		log:      slog.Default(),
		maxRetry: 3,
	}
}

// SagaResult ผลลัพธ์การ execute saga
type SagaResult struct {
	Success       bool
	OrderID       string
	CompletedAt   time.Time
	Error         error
	CompErrors    []error // errors ระหว่าง compensation (ถ้ามี)
}

// Execute รัน saga ทั้งหมด
// ถ้า step ล้ม จะ compensate steps ที่สำเร็จแล้วย้อนกลับ
func (s *OrderSaga) Execute(ctx context.Context, req *OrderRequest) SagaResult {
	sc := &SagaContext{
		OrderID: fmt.Sprintf("ORD-%d", time.Now().UnixNano()%1000000),
	}

	s.log.Info("▶ Saga started",
		"order_id", sc.OrderID,
		"customer_id", req.CustomerID,
		"product_id", req.ProductID,
		"quantity", req.Quantity,
		"amount_thb", fmt.Sprintf("%.2f", float64(req.Amount)/100),
	)
	fmt.Println(strings.Repeat("─", 50))

	var executedSteps []int // index ของ steps ที่สำเร็จแล้ว

	// ─── Forward Phase: Execute Steps ───
	for i, step := range s.steps {
		s.log.Info(fmt.Sprintf("  ✦ Step %d/%d: %s", i+1, len(s.steps), step.Name))

		if err := step.Execute(ctx, sc, req); err != nil {
			s.log.Error(fmt.Sprintf("  ✗ Step %d failed: %s", i+1, step.Name),
				"error", err,
			)

			// ─── Compensation Phase ───
			s.log.Warn("  ↩ Starting compensation (LIFO order)...")
			compErrors := s.compensate(ctx, sc, req, executedSteps)

			return SagaResult{
				Success:    false,
				OrderID:    sc.OrderID,
				Error:      fmt.Errorf("saga failed at step %q: %w", step.Name, err),
				CompErrors: compErrors,
			}
		}

		executedSteps = append(executedSteps, i)
		s.log.Info(fmt.Sprintf("  ✓ Step %d succeeded: %s", i+1, step.Name))
	}

	fmt.Println(strings.Repeat("─", 50))
	s.log.Info("▶ Saga completed successfully", "order_id", sc.OrderID)

	return SagaResult{
		Success:     true,
		OrderID:     sc.OrderID,
		CompletedAt: time.Now(),
	}
}

// compensate รัน compensating actions ย้อนกลับ (LIFO)
// LIFO สำคัญ: ต้องย้อนกลับ state ในลำดับ inverse กับที่สร้าง
// เช่น: Reserve → Charge → Confirm
// compensate: Cancel → Refund → Release
func (s *OrderSaga) compensate(
	ctx context.Context,
	sc *SagaContext,
	req *OrderRequest,
	executedSteps []int,
) []error {
	var compErrors []error

	// วนย้อนกลับ (LIFO)
	for j := len(executedSteps) - 1; j >= 0; j-- {
		stepIdx := executedSteps[j]
		step := s.steps[stepIdx]

		s.log.Info(fmt.Sprintf("    ↩ Compensating: %s", step.Name))

		var compErr error
		// Retry compensation ถ้า fail (compensation ต้อง idempotent)
		for attempt := 1; attempt <= s.maxRetry; attempt++ {
			compErr = step.Compensate(ctx, sc, req)
			if compErr == nil {
				s.log.Info(fmt.Sprintf("    ✓ Compensated: %s", step.Name))
				break
			}
			s.log.Warn(fmt.Sprintf("    ⚠ Compensation failed (attempt %d/%d): %s",
				attempt, s.maxRetry, step.Name),
				"error", compErr,
			)
			if attempt < s.maxRetry {
				time.Sleep(time.Duration(attempt*100) * time.Millisecond)
			}
		}

		if compErr != nil {
			// ถ้า compensate ไม่สำเร็จแม้ retry: บันทึก error เพื่อ manual intervention
			// ในโปรดักชัน: ส่ง alert, บันทึกลง dead letter queue
			s.log.Error(fmt.Sprintf("    ✗ COMPENSATION FAILED (requires manual intervention): %s", step.Name),
				"error", compErr,
			)
			compErrors = append(compErrors, fmt.Errorf("compensate %q: %w", step.Name, compErr))
		}
	}

	return compErrors
}

// ─────────────────────────────────────────────
// main — รัน demo หลายครั้งเพื่อแสดงทั้ง success และ compensation
// ─────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	}))
	slog.SetDefault(logger)

	// สุ่ม seed (Go 1.20+ auto-seeds แล้ว; ใช้ rand.Seed สำหรับ Go 1.19 compatibility)
	rand.Seed(time.Now().UnixNano()) //nolint:staticcheck

	// สร้าง service clients (จำลอง)
	invService := &InventoryService{}
	payService := &PaymentService{failProbability: 0.5} // 50% payment failure เพื่อ demo
	ordService := &OrderService{}

	// สร้าง saga orchestrator
	saga := NewOrderSaga(invService, payService, ordService)

	// Order request
	req := &OrderRequest{
		CustomerID: "customer-001",
		ProductID:  "BOOK-001",
		Quantity:   2,
		Amount:     118000, // 1,180 บาท
	}

	fmt.Println("\n" + strings.Repeat("═", 50))
	fmt.Println("  MODULE 5: Saga Pattern Demo")
	fmt.Println("  PlaceOrder Orchestration Saga")
	fmt.Println(strings.Repeat("═", 50))

	// รัน saga หลายครั้งเพื่อแสดงทั้ง success และ compensation
	successCount, failCount := 0, 0
	const runs = 5

	for i := 1; i <= runs; i++ {
		fmt.Printf("\n\n%s\n  RUN %d/%d\n%s\n",
			strings.Repeat("━", 50), i, runs, strings.Repeat("━", 50))

		ctx := context.Background()
		result := saga.Execute(ctx, req)

		fmt.Println()
		if result.Success {
			successCount++
			slog.Info("🎉 SAGA SUCCESS",
				"order_id", result.OrderID,
				"completed_at", result.CompletedAt.Format(time.RFC3339),
			)
		} else {
			failCount++
			slog.Warn("⚠ SAGA FAILED WITH COMPENSATION",
				"order_id", result.OrderID,
				"error", result.Error,
			)
			if len(result.CompErrors) > 0 {
				slog.Error("⛔ COMPENSATION ERRORS (manual intervention needed)",
					"errors", result.CompErrors,
				)
			} else {
				slog.Info("✓ Compensation completed successfully — system in consistent state")
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	// สรุป
	fmt.Println("\n" + strings.Repeat("═", 50))
	fmt.Printf("  SUMMARY: %d success, %d compensated (out of %d)\n",
		successCount, failCount, runs)
	fmt.Println(strings.Repeat("═", 50))

	// ─── Key Takeaways ───
	fmt.Println("\nKey Takeaways:")
	fmt.Println()
	fmt.Println("1. Idempotency (สำคัญมาก)")
	fmt.Println("   - แต่ละ step และ compensate ต้องรัน N ครั้งได้ผลเดิม")
	fmt.Println("   - ใช้ idempotency key หรือ check state ก่อน execute")
	fmt.Println()
	fmt.Println("2. Compensation Order (LIFO)")
	fmt.Println("   - Execute:    Reserve → Charge → Confirm")
	fmt.Println("   - Compensate: Cancel → Refund → Release")
	fmt.Println("   - ลำดับ inverse เพื่อคืน dependency อย่างถูกต้อง")
	fmt.Println()
	fmt.Println("3. Choreography vs Orchestration")
	fmt.Println("   - Orchestration (ไฟล์นี้): orchestrator เรียก service โดยตรง")
	fmt.Println("     + ง่าย debug, centralized logic")
	fmt.Println("     - orchestrator เป็น single point of failure")
	fmt.Println("   - Choreography: service publish events, อื่น subscribe")
	fmt.Println("     + ไม่มี SPOF, loosely coupled")
	fmt.Println("     - ยาก trace, risk cyclic dependency")
	fmt.Println()
	fmt.Println("4. Saga กับ Eventual Consistency")
	fmt.Println("   - ระหว่าง saga มี intermediate state (partial update)")
	fmt.Println("   - ต้องออกแบบ business logic รับมือกับ dirty read")
	fmt.Println("   - ใช้ Semantic Lock หรือ Pessimistic Lock ถ้าจำเป็น")
}
