// Package integration — Integration Testing ด้วย testcontainers-go
//
// # ทำไม Integration Test สำคัญกว่า Unit Test สำหรับ Database Layer?
//
// Unit test ที่ mock database มีปัญหา:
//  1. Mock ไม่รู้จัก SQL syntax — ผ่าน test แต่ fail ใน production
//  2. Mock ไม่จำลอง transaction behavior ที่แท้จริง
//  3. Mock ไม่ตรวจ index, constraint, trigger
//  4. Migration script ที่ผิดพลาดไม่ถูกตรวจพบ
//
// testcontainers-go แก้ปัญหาเหล่านี้:
//  ✓ รัน PostgreSQL จริงๆ ใน Docker container
//  ✓ Schema เหมือน production ทุกประการ
//  ✓ ทดสอบ SQL query, constraint, transaction จริง
//  ✓ Container ถูก cleanup อัตโนมัติหลัง test
//
// # วิธีติดตั้ง testcontainers-go
//
//	go get github.com/testcontainers/testcontainers-go
//	go get github.com/testcontainers/testcontainers-go/modules/postgres
//
// # วิธีรัน integration tests
//
//	# ต้องมี Docker daemon รันอยู่
//	go test -v -tags=integration ./module6/integration/
//	go test -v -run TestOrderRepository ./module6/integration/
//
// # Build tag strategy
//
// ใช้ //go:build integration เพื่อให้ integration tests ไม่รันใน go test ./...
// นักพัฒนา run unit tests ได้ตลอดเวลา แต่ integration tests รันใน CI เท่านั้น
//
// NOTE: File นี้แสดง pattern สมบูรณ์ แต่ต้องเพิ่ม testcontainers-go ใน go.mod ก่อนรัน
//
//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	// testcontainers-go — ต้อง go get ก่อน
	// go get github.com/testcontainers/testcontainers-go
	// go get github.com/testcontainers/testcontainers-go/modules/postgres
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	// postgres driver
	// go get github.com/lib/pq
	_ "github.com/lib/pq"
)

// ============================================================
// DOMAIN TYPES (self-contained สำหรับ module นี้)
// ============================================================

type OrderID string
type CustomerID string
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "PENDING"
	OrderStatusConfirmed OrderStatus = "CONFIRMED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
)

type Order struct {
	ID         OrderID
	CustomerID CustomerID
	Status     OrderStatus
	TotalCents int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ============================================================
// TEST INFRASTRUCTURE — Package-level setup
// ============================================================

// testDB เป็น package-level variable ที่ share ระหว่าง tests
// สร้างครั้งเดียวใน TestMain เพื่อประหยัดเวลา (ไม่ต้อง spin up container ทุก test)
var testDB *sql.DB

// TestMain — lifecycle hook สำหรับ test package ทั้งหมด
//
// TestMain รันก่อน tests และหลัง tests ทั้งหมดใน package
// ใช้สำหรับ:
//  1. Spin up shared resources (database, cache, message broker)
//  2. Run database migrations
//  3. Cleanup หลัง tests ทั้งหมดเสร็จ
//
// ⚠️ ต้องเรียก m.Run() เพื่อรัน tests จริง
// ⚠️ os.Exit(m.Run()) ตรงๆ เพราะ defer ไม่ทำงานหลัง os.Exit
func TestMain(m *testing.M) {
	// context สำหรับ container lifecycle
	ctx := context.Background()

	// ===== SPIN UP POSTGRESQL CONTAINER =====
	//
	// testcontainers-go จะ:
	// 1. Pull image docker.io/postgres:15-alpine (ถ้ายังไม่มี)
	// 2. Start container
	// 3. รอจนกว่า postgres พร้อม (wait strategy)
	// 4. Return container handle

	pgContainer, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("docker.io/postgres:15-alpine"),
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		// WaitStrategy: รอจนกว่า PostgreSQL พร้อมรับ connection
		// ถ้าไม่ใส่จะ race กับ container startup
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2). // postgres log นี้ 2 ครั้งก่อน ready
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("failed to start postgres container: %v", err)
	}

	// ===== CLEANUP CONTAINER =====
	// ใช้ defer แบบนี้ได้เพราะเราไม่ได้ os.Exit โดยตรง
	// cleanup function จะรันหลัง m.Run() return
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			log.Printf("warning: failed to terminate postgres container: %v", err)
		}
	}()

	// ===== GET CONNECTION STRING =====
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("failed to get connection string: %v", err)
	}

	// ===== CONNECT TO DATABASE =====
	testDB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer testDB.Close()

	// ตรวจว่า connection ใช้ได้จริง
	if err := testDB.PingContext(ctx); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	// ===== RUN MIGRATIONS =====
	if err := runMigrations(testDB); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	// ===== RUN TESTS =====
	// m.Run() รัน tests ทั้งหมดและ return exit code
	exitCode := m.Run()

	// os.Exit ต้องรันหลัง defer เสร็จ (ไม่ได้เรียก os.Exit ตรงๆ)
	os.Exit(exitCode)
}

// runMigrations สร้าง schema สำหรับ tests
// ในโปรเจคจริงใช้ migration tool เช่น golang-migrate หรือ goose
func runMigrations(db *sql.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS orders (
			id          TEXT PRIMARY KEY,
			customer_id TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'PENDING',
			total_cents BIGINT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_orders_customer_id ON orders(customer_id);
		CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);

		-- ตรวจ constraint: status ต้องอยู่ใน list ที่กำหนด
		ALTER TABLE orders DROP CONSTRAINT IF EXISTS chk_orders_status;
		ALTER TABLE orders ADD CONSTRAINT chk_orders_status
			CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED'));
	`
	_, err := db.Exec(schema)
	return err
}

// ============================================================
// POSTGRES ORDER REPOSITORY — Implementation ที่ test จริง
// ============================================================

// postgresOrderRepository — implementation ที่ใช้ PostgreSQL จริง
// นี่คือ code ที่เราต้องการ integration test จริงๆ
type postgresOrderRepository struct {
	db *sql.DB
}

func newPostgresRepo(db *sql.DB) *postgresOrderRepository {
	return &postgresOrderRepository{db: db}
}

// Save insert หรือ update order
func (r *postgresOrderRepository) Save(ctx context.Context, order *Order) error {
	query := `
		INSERT INTO orders (id, customer_id, status, total_cents, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			status      = EXCLUDED.status,
			total_cents = EXCLUDED.total_cents,
			updated_at  = EXCLUDED.updated_at
	`
	_, err := r.db.ExecContext(ctx, query,
		order.ID,
		order.CustomerID,
		order.Status,
		order.TotalCents,
		order.CreatedAt,
		order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("save order: %w", err)
	}
	return nil
}

// FindByID ดึง order ด้วย ID
func (r *postgresOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	query := `
		SELECT id, customer_id, status, total_cents, created_at, updated_at
		FROM orders
		WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, query, id)

	var order Order
	err := row.Scan(
		&order.ID,
		&order.CustomerID,
		&order.Status,
		&order.TotalCents,
		&order.CreatedAt,
		&order.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("order %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("scan order: %w", err)
	}

	return &order, nil
}

// ListByCustomer ดึง orders ของ customer
func (r *postgresOrderRepository) ListByCustomer(ctx context.Context, customerID CustomerID) ([]*Order, error) {
	query := `
		SELECT id, customer_id, status, total_cents, created_at, updated_at
		FROM orders
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, customerID)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		var order Order
		if err := rows.Scan(
			&order.ID,
			&order.CustomerID,
			&order.Status,
			&order.TotalCents,
			&order.CreatedAt,
			&order.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order row: %w", err)
		}
		orders = append(orders, &order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}

	return orders, nil
}

// UpdateStatus เปลี่ยน status ของ order (atomic update)
func (r *postgresOrderRepository) UpdateStatus(ctx context.Context, id OrderID, status OrderStatus) error {
	query := `
		UPDATE orders
		SET status = $2, updated_at = NOW()
		WHERE id = $1
	`
	result, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("order %s: %w", id, ErrNotFound)
	}
	return nil
}

var ErrNotFound = fmt.Errorf("not found")

// ============================================================
// HELPER: cleanupOrders ลบ orders หลัง test
// ============================================================

// cleanupOrders ลบ orders ที่สร้างใน test นี้
// ใช้กับ t.Cleanup() เพื่อ restore state หลัง test
func cleanupOrders(t *testing.T, ids ...OrderID) func() {
	t.Helper()
	return func() {
		if len(ids) == 0 {
			return
		}
		// ลบ orders ทั้งหมดใน 1 query
		placeholders := make([]interface{}, len(ids))
		params := ""
		for i, id := range ids {
			placeholders[i] = id
			if i > 0 {
				params += ","
			}
			params += fmt.Sprintf("$%d", i+1)
		}

		_, err := testDB.Exec(fmt.Sprintf("DELETE FROM orders WHERE id IN (%s)", params), placeholders...)
		if err != nil {
			t.Logf("cleanup warning: failed to delete test orders: %v", err)
		}
	}
}

// ============================================================
// INTEGRATION TESTS
// ============================================================

// TestOrderRepository_Save_FindByID ทดสอบ round-trip: save แล้วค้นหาได้
//
// Integration test นี้ verify ว่า:
//  1. SQL INSERT ทำงานถูกต้อง
//  2. SQL SELECT ดึง data กลับมาถูกต้อง
//  3. Type mapping (Go → Postgres → Go) ถูกต้อง
//  4. Timestamp ถูก serialize/deserialize ถูกต้อง
func TestOrderRepository_Save_FindByID(t *testing.T) {
	if testDB == nil {
		t.Skip("testDB not initialized — run with -tags=integration")
	}

	repo := newPostgresRepo(testDB)
	ctx := context.Background()

	t.Run("save new order and retrieve it", func(t *testing.T) {
		// ===== ARRANGE =====
		orderID := OrderID(fmt.Sprintf("integ-test-%d", time.Now().UnixNano()))
		now := time.Now().UTC().Truncate(time.Millisecond) // postgres จำลอง millisecond precision

		order := &Order{
			ID:         orderID,
			CustomerID: "cust-integ-001",
			Status:     OrderStatusPending,
			TotalCents: 25000,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		// ลบ test data หลัง test เสร็จ
		t.Cleanup(cleanupOrders(t, orderID))

		// ===== ACT: Save =====
		if err := repo.Save(ctx, order); err != nil {
			t.Fatalf("Save() error: %v", err)
		}

		// ===== ACT: FindByID =====
		got, err := repo.FindByID(ctx, orderID)
		if err != nil {
			t.Fatalf("FindByID() error: %v", err)
		}

		// ===== ASSERT =====
		if got.ID != order.ID {
			t.Errorf("ID = %q, want %q", got.ID, order.ID)
		}
		if got.CustomerID != order.CustomerID {
			t.Errorf("CustomerID = %q, want %q", got.CustomerID, order.CustomerID)
		}
		if got.Status != OrderStatusPending {
			t.Errorf("Status = %q, want PENDING", got.Status)
		}
		if got.TotalCents != 25000 {
			t.Errorf("TotalCents = %d, want 25000", got.TotalCents)
		}
		// ตรวจ timestamp (ต้องคำนึงถึง timezone และ precision)
		if !got.CreatedAt.Equal(now) {
			t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
		}
	})

	t.Run("save same order twice updates existing record", func(t *testing.T) {
		orderID := OrderID(fmt.Sprintf("integ-upsert-%d", time.Now().UnixNano()))
		t.Cleanup(cleanupOrders(t, orderID))

		now := time.Now().UTC().Truncate(time.Millisecond)
		original := &Order{
			ID:         orderID,
			CustomerID: "cust-002",
			Status:     OrderStatusPending,
			TotalCents: 1000,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		// First save
		if err := repo.Save(ctx, original); err != nil {
			t.Fatalf("first Save() error: %v", err)
		}

		// Update: เปลี่ยน status และ total
		updated := *original
		updated.Status = OrderStatusConfirmed
		updated.TotalCents = 2000
		updated.UpdatedAt = now.Add(time.Minute)

		// Second save — ควร update ไม่ใช่ insert ใหม่
		if err := repo.Save(ctx, &updated); err != nil {
			t.Fatalf("second Save() error: %v", err)
		}

		// Verify update
		got, err := repo.FindByID(ctx, orderID)
		if err != nil {
			t.Fatalf("FindByID() error: %v", err)
		}

		if got.Status != OrderStatusConfirmed {
			t.Errorf("Status = %q, want CONFIRMED", got.Status)
		}
		if got.TotalCents != 2000 {
			t.Errorf("TotalCents = %d, want 2000", got.TotalCents)
		}
		// CreatedAt ไม่ควรเปลี่ยน (ON CONFLICT DO UPDATE ไม่แตะ created_at)
		if !got.CreatedAt.Equal(now) {
			t.Errorf("CreatedAt should not change on update: got %v, want %v", got.CreatedAt, now)
		}
	})
}

// TestOrderRepository_Constraint ทดสอบ database constraints
//
// ⚠️ นี่คือสิ่งที่ unit test ด้วย mock ทำไม่ได้:
// ตรวจสอบ CHECK constraint ที่ database enforce
func TestOrderRepository_Constraint(t *testing.T) {
	if testDB == nil {
		t.Skip("testDB not initialized — run with -tags=integration")
	}

	repo := newPostgresRepo(testDB)
	ctx := context.Background()

	t.Run("invalid status violates database constraint", func(t *testing.T) {
		orderID := OrderID(fmt.Sprintf("integ-constraint-%d", time.Now().UnixNano()))
		t.Cleanup(cleanupOrders(t, orderID))

		now := time.Now().UTC().Truncate(time.Millisecond)
		order := &Order{
			ID:         orderID,
			CustomerID: "cust-003",
			Status:     "INVALID_STATUS", // ไม่อยู่ใน CHECK constraint
			TotalCents: 100,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		// ควร fail เพราะ database CHECK constraint
		// mock จะไม่ catch error นี้เพราะ mock ไม่มี constraint
		err := repo.Save(ctx, order)
		if err == nil {
			t.Error("expected constraint violation error, got nil")
		}
	})
}

// TestOrderRepository_ListByCustomer ทดสอบ query หลาย records
func TestOrderRepository_ListByCustomer(t *testing.T) {
	if testDB == nil {
		t.Skip("testDB not initialized — run with -tags=integration")
	}

	repo := newPostgresRepo(testDB)
	ctx := context.Background()

	t.Run("list orders for customer returns all orders in descending date order", func(t *testing.T) {
		customerID := CustomerID(fmt.Sprintf("cust-list-%d", time.Now().UnixNano()))
		baseTime := time.Now().UTC().Truncate(time.Millisecond)

		// สร้าง 3 orders สำหรับ customer เดียวกัน
		orderIDs := []OrderID{
			OrderID(fmt.Sprintf("order-list-1-%d", time.Now().UnixNano())),
			OrderID(fmt.Sprintf("order-list-2-%d", time.Now().UnixNano())),
			OrderID(fmt.Sprintf("order-list-3-%d", time.Now().UnixNano())),
		}
		t.Cleanup(cleanupOrders(t, orderIDs...))

		for i, id := range orderIDs {
			order := &Order{
				ID:         id,
				CustomerID: customerID,
				Status:     OrderStatusPending,
				TotalCents: int64((i + 1) * 1000),
				CreatedAt:  baseTime.Add(time.Duration(i) * time.Minute), // ต่างกัน 1 นาที
				UpdatedAt:  baseTime.Add(time.Duration(i) * time.Minute),
			}
			if err := repo.Save(ctx, order); err != nil {
				t.Fatalf("Save order[%d] error: %v", i, err)
			}
		}

		// ===== ACT =====
		orders, err := repo.ListByCustomer(ctx, customerID)
		if err != nil {
			t.Fatalf("ListByCustomer() error: %v", err)
		}

		// ===== ASSERT =====
		if len(orders) != 3 {
			t.Errorf("got %d orders, want 3", len(orders))
		}

		// ตรวจว่าเรียงจากใหม่สุดไปเก่าสุด (DESC)
		for i := 1; i < len(orders); i++ {
			if orders[i-1].CreatedAt.Before(orders[i].CreatedAt) {
				t.Errorf("orders not sorted: orders[%d].CreatedAt < orders[%d].CreatedAt", i-1, i)
			}
		}
	})

	t.Run("list orders for customer with no orders returns empty slice", func(t *testing.T) {
		nonExistentCustomer := CustomerID("cust-nobody-" + fmt.Sprint(time.Now().UnixNano()))

		orders, err := repo.ListByCustomer(ctx, nonExistentCustomer)
		if err != nil {
			t.Fatalf("ListByCustomer() error: %v", err)
		}
		// ควร return empty slice ไม่ใช่ nil
		if orders == nil {
			t.Error("expected empty slice, got nil")
		}
		if len(orders) != 0 {
			t.Errorf("expected 0 orders, got %d", len(orders))
		}
	})
}

// TestOrderRepository_Transaction ทดสอบ transaction behavior
//
// Transaction testing คือสิ่งที่ mock ทำไม่ได้อย่างสมบูรณ์
// ต้อง test กับ database จริงเท่านั้น
func TestOrderRepository_Transaction(t *testing.T) {
	if testDB == nil {
		t.Skip("testDB not initialized — run with -tags=integration")
	}

	ctx := context.Background()

	t.Run("rolled back transaction leaves no data", func(t *testing.T) {
		orderID := OrderID(fmt.Sprintf("integ-tx-%d", time.Now().UnixNano()))
		// ไม่ต้อง cleanup เพราะ rollback แล้ว

		// เริ่ม transaction
		tx, err := testDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Millisecond)
		// Insert ใน transaction
		_, err = tx.ExecContext(ctx, `
			INSERT INTO orders (id, customer_id, status, total_cents, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, orderID, "cust-tx", OrderStatusPending, 999, now, now)
		if err != nil {
			tx.Rollback()
			t.Fatalf("insert in tx: %v", err)
		}

		// Rollback — data ไม่ควรอยู่ใน DB
		if err := tx.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}

		// ตรวจว่า data ถูก rollback จริง
		repo := newPostgresRepo(testDB)
		_, err = repo.FindByID(ctx, orderID)
		if err == nil {
			t.Error("expected ErrNotFound after rollback, got order")
		}
	})
}
