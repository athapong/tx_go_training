// Module 5: Resilience Patterns Demo
//
// รูปแบบที่จะเรียนรู้ในไฟล์นี้:
//   1. Circuit Breaker (3 states: CLOSED/OPEN/HALF-OPEN)
//   2. Retry with Exponential Backoff + Jitter
//   3. Bulkhead (แยก goroutine pool สำหรับ downstream services ต่างๆ)
//   4. Rate Limiter (Token Bucket)
//   5. รวมทุก pattern เป็น ResilientHTTPClient
//
// แต่ละ pattern แก้ปัญหาคนละอย่าง — ใช้ร่วมกันเพื่อระบบที่ทนทาน
//
// วิธีรัน:
//   go run module5/resilience/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────
// 1. Circuit Breaker
//
// ป้องกันการเรียก downstream service ที่ล้มเหลวซ้ำๆ
// แทนที่จะรอ timeout ทุกครั้ง ให้ "fail fast" ทันที
//
// State Machine:
//   CLOSED ──failure threshold──► OPEN
//   OPEN   ──timeout expires──► HALF-OPEN
//   HALF-OPEN ──probe success──► CLOSED
//   HALF-OPEN ──probe fail──► OPEN
// ─────────────────────────────────────────────

// CBState สถานะของ Circuit Breaker
type CBState int

const (
	CBStateClosed   CBState = iota // ปกติ — ส่ง request
	CBStateOpen                    // เปิด — บล็อก request ทันที
	CBStateHalfOpen                // ทดสอบ — ส่ง request จำนวนน้อย
)

func (s CBState) String() string {
	switch s {
	case CBStateClosed:
		return "CLOSED"
	case CBStateOpen:
		return "OPEN"
	case CBStateHalfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

// ErrCircuitOpen เกิดเมื่อ circuit อยู่ใน OPEN state
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker implements Circuit Breaker pattern
type CircuitBreaker struct {
	mu sync.Mutex

	name  string
	state CBState

	// Thresholds สำหรับเปิด circuit
	failureThreshold     int           // จำนวน consecutive failures ก่อน OPEN
	successThreshold     int           // จำนวน consecutive successes ใน HALF-OPEN ก่อน CLOSED
	openTimeout          time.Duration // เวลาที่ circuit ค้าง OPEN ก่อน HALF-OPEN

	// Counters
	consecutiveFailures  int
	consecutiveSuccesses int

	// Timer
	openedAt time.Time

	log *slog.Logger
}

// NewCircuitBreaker สร้าง Circuit Breaker ใหม่
func NewCircuitBreaker(name string, failThreshold, successThreshold int, openTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		state:            CBStateClosed,
		failureThreshold: failThreshold,
		successThreshold: successThreshold,
		openTimeout:      openTimeout,
		log:              slog.Default(),
	}
}

// State คืนค่า state ปัจจุบัน (thread-safe)
func (cb *CircuitBreaker) State() CBState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState()
}

// currentState ตรวจสอบว่า OPEN timeout หมดหรือยัง (ไม่มี lock)
func (cb *CircuitBreaker) currentState() CBState {
	if cb.state == CBStateOpen {
		if time.Since(cb.openedAt) >= cb.openTimeout {
			// timeout หมดแล้ว → เปลี่ยนเป็น HALF-OPEN เพื่อทดสอบ
			cb.state = CBStateHalfOpen
			cb.consecutiveSuccesses = 0
			cb.log.Info("Circuit breaker → HALF-OPEN",
				"name", cb.name,
				"after_timeout", cb.openTimeout,
			)
		}
	}
	return cb.state
}

// Execute รัน fn ผ่าน circuit breaker
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()

	state := cb.currentState()
	if state == CBStateOpen {
		cb.mu.Unlock()
		// Fail fast — ไม่ต้องรอ timeout
		return ErrCircuitOpen
	}

	cb.mu.Unlock()

	// รัน actual operation
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.recordFailure()
	} else {
		cb.recordSuccess()
	}

	return err
}

// recordFailure บันทึก failure และเปิด circuit ถ้าถึง threshold
func (cb *CircuitBreaker) recordFailure() {
	cb.consecutiveFailures++
	cb.consecutiveSuccesses = 0

	switch cb.state {
	case CBStateClosed:
		if cb.consecutiveFailures >= cb.failureThreshold {
			cb.state = CBStateOpen
			cb.openedAt = time.Now()
			cb.log.Warn("Circuit breaker → OPEN",
				"name", cb.name,
				"consecutive_failures", cb.consecutiveFailures,
				"will_retry_after", cb.openTimeout,
			)
		}
	case CBStateHalfOpen:
		// HALF-OPEN probe ล้ม → กลับไป OPEN
		cb.state = CBStateOpen
		cb.openedAt = time.Now()
		cb.log.Warn("Circuit breaker HALF-OPEN → OPEN (probe failed)",
			"name", cb.name,
		)
	}
}

// recordSuccess บันทึก success และปิด circuit ถ้า HALF-OPEN
func (cb *CircuitBreaker) recordSuccess() {
	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses++

	if cb.state == CBStateHalfOpen {
		if cb.consecutiveSuccesses >= cb.successThreshold {
			cb.state = CBStateClosed
			cb.log.Info("Circuit breaker → CLOSED",
				"name", cb.name,
				"consecutive_successes", cb.consecutiveSuccesses,
			)
		}
	}
}

// ─────────────────────────────────────────────
// 2. Retry with Exponential Backoff + Jitter
//
// ป้องกัน "thundering herd" ที่เกิดเมื่อ clients หลายตัว
// retry พร้อมกัน ทำให้ server ยิ่ง overload
//
// Full Jitter = backoff * random(0, 1)
// ทำให้ retry กระจายตัวในเวลา ลด spike
// ─────────────────────────────────────────────

// RetryConfig กำหนด retry behavior
type RetryConfig struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64 // ตัวคูณ backoff (2.0 = double ทุกครั้ง)
	JitterFactor    float64 // 0.0 = no jitter, 1.0 = full jitter
}

// DefaultRetryConfig กำหนดค่า retry ที่เหมาะสำหรับ API calls
var DefaultRetryConfig = RetryConfig{
	MaxAttempts:     4,
	InitialInterval: 100 * time.Millisecond,
	MaxInterval:     2 * time.Second,
	Multiplier:      2.0,
	JitterFactor:    0.5, // ±50% jitter
}

// RetryableError marker interface สำหรับ error ที่ควร retry
type RetryableError interface {
	error
	IsRetryable() bool
}

// retryableErr implementation ง่าย
type retryableErr struct {
	msg string
}

func (e *retryableErr) Error() string     { return e.msg }
func (e *retryableErr) IsRetryable() bool { return true }

// NewRetryableError สร้าง retryable error
func NewRetryableError(msg string) error {
	return &retryableErr{msg: msg}
}

// WithRetry รัน fn ด้วย retry + exponential backoff
// คืน nil ถ้า fn สำเร็จ, คืน error สุดท้ายถ้า attempts หมด
func WithRetry(ctx context.Context, cfg RetryConfig, fn func(attempt int) error) error {
	var lastErr error
	interval := cfg.InitialInterval

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		// ตรวจสอบ context ก่อนทุก attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled after %d attempts: %w", attempt-1, ctx.Err())
		default:
		}

		lastErr = fn(attempt)
		if lastErr == nil {
			return nil // สำเร็จ
		}

		// ตรวจสอบว่า error นี้ควร retry หรือไม่
		var re RetryableError
		if errors.As(lastErr, &re) && !re.IsRetryable() {
			return lastErr // non-retryable error
		}

		if attempt == cfg.MaxAttempts {
			break // attempts หมดแล้ว
		}

		// คำนวณ backoff interval
		// Exponential backoff: interval = initialInterval * multiplier^(attempt-1)
		backoff := float64(interval) * math.Pow(cfg.Multiplier, float64(attempt-1))
		if backoff > float64(cfg.MaxInterval) {
			backoff = float64(cfg.MaxInterval)
		}

		// เพิ่ม jitter เพื่อกระจาย retry
		// Full Jitter: sleep = random(0, backoff)
		jitter := backoff * cfg.JitterFactor * rand.Float64()
		sleepDuration := time.Duration(backoff*(1-cfg.JitterFactor/2) + jitter)

		slog.Debug("Retry backoff",
			"attempt", attempt,
			"max_attempts", cfg.MaxAttempts,
			"sleep_ms", sleepDuration.Milliseconds(),
			"error", lastErr,
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
		case <-time.After(sleepDuration):
		}
	}

	return fmt.Errorf("all %d attempts failed, last error: %w", cfg.MaxAttempts, lastErr)
}

// ─────────────────────────────────────────────
// 3. Bulkhead Pattern
//
// แยก resources (goroutines) สำหรับ downstream services ต่างๆ
// ป้องกัน "one service takes all resources"
//
// เหมือน compartment ของเรือ — ถ้าห้องใดน้ำท่วม จะไม่ลามไปห้องอื่น
// ─────────────────────────────────────────────

// Bulkhead จำกัดจำนวน concurrent calls ไปยัง downstream service
type Bulkhead struct {
	name     string
	sem      chan struct{} // semaphore — buffered channel
	log      *slog.Logger

	// stats
	totalCalls    atomic.Int64
	rejectedCalls atomic.Int64
}

// NewBulkhead สร้าง bulkhead ที่ยอมรับ concurrent calls ไม่เกิน maxConcurrent
func NewBulkhead(name string, maxConcurrent int) *Bulkhead {
	return &Bulkhead{
		name: name,
		sem:  make(chan struct{}, maxConcurrent),
		log:  slog.Default(),
	}
}

// ErrBulkheadFull เกิดเมื่อ bulkhead เต็ม
var ErrBulkheadFull = errors.New("bulkhead is full: too many concurrent requests")

// Execute รัน fn ภายใต้ bulkhead
// ถ้า bulkhead เต็ม จะ return ErrBulkheadFull ทันที (non-blocking)
func (b *Bulkhead) Execute(fn func() error) error {
	b.totalCalls.Add(1)

	// ลอง acquire semaphore (non-blocking)
	select {
	case b.sem <- struct{}{}: // ได้ slot
		defer func() { <-b.sem }() // release เมื่อเสร็จ
	default:
		// bulkhead เต็ม
		b.rejectedCalls.Add(1)
		return ErrBulkheadFull
	}

	return fn()
}

// Stats คืนสถิติของ bulkhead
func (b *Bulkhead) Stats() (total, rejected, active int64) {
	total = b.totalCalls.Load()
	rejected = b.rejectedCalls.Load()
	active = int64(len(b.sem))
	return
}

// ─────────────────────────────────────────────
// 4. Rate Limiter (Token Bucket)
//
// ควบคุม rate ของ requests เพื่อป้องกัน overload
// ทั้งฝั่ง client (ไม่ส่งเร็วเกิน) และ server (ปกป้อง resources)
//
// Token Bucket Algorithm:
//   - Bucket มี capacity สูงสุด N tokens
//   - เพิ่ม token ทุกๆ 1/rate วินาที (refill)
//   - แต่ละ request ใช้ 1 token
//   - ถ้าไม่มี token → reject หรือ wait
// ─────────────────────────────────────────────

// TokenBucket implements token bucket rate limiter
type TokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64 // tokens สูงสุด (burst size)
	rate     float64 // tokens ต่อวินาที
	lastFill time.Time
	log      *slog.Logger
}

// NewTokenBucket สร้าง rate limiter
// rate: requests per second
// burst: สูงสุดที่ยอมรับต่อช่วงเวลา (capacity ของ bucket)
func NewTokenBucket(rate, burst float64) *TokenBucket {
	return &TokenBucket{
		tokens:   burst, // เริ่มต้น bucket เต็ม
		capacity: burst,
		rate:     rate,
		lastFill: time.Now(),
		log:      slog.Default(),
	}
}

// ErrRateLimitExceeded เกิดเมื่อ rate limit เกิน
var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// Allow ตรวจสอบว่า request นี้ผ่านได้หรือไม่ (non-blocking)
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// เติม tokens ตามเวลาที่ผ่านไป
	now := time.Now()
	elapsed := now.Sub(tb.lastFill).Seconds()
	tb.tokens = math.Min(tb.capacity, tb.tokens+elapsed*tb.rate)
	tb.lastFill = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}

	return false
}

// Wait รอจนกว่าจะได้ token (blocking ถ้า rate limit)
func (tb *TokenBucket) Wait(ctx context.Context) error {
	for {
		if tb.Allow() {
			return nil
		}

		// คำนวณว่าต้องรอนานแค่ไหน
		tb.mu.Lock()
		waitTime := time.Duration((1 - tb.tokens) / tb.rate * float64(time.Second))
		tb.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
		}
	}
}

// ─────────────────────────────────────────────
// 5. ResilientHTTPClient — รวมทุก patterns
//
// ใช้งานแบบ layered:
//   Rate Limiter → Circuit Breaker → Bulkhead → Retry → Actual Call
//
// ลำดับนี้สำคัญ:
//   - Rate Limiter อยู่นอกสุด: ตัดก่อน waste resources อื่น
//   - Circuit Breaker: ถ้าเปิดอยู่ skip ทุกอย่าง
//   - Bulkhead: จำกัด concurrency
//   - Retry: ลองใหม่เมื่อ transient failure
// ─────────────────────────────────────────────

// ResilientHTTPClient HTTP client ที่มี resilience patterns ครบชุด
type ResilientHTTPClient struct {
	name        string
	cb          *CircuitBreaker
	bulkhead    *Bulkhead
	rateLimiter *TokenBucket
	retryCfg    RetryConfig
	log         *slog.Logger
}

// NewResilientHTTPClient สร้าง resilient client สำหรับ downstream service
func NewResilientHTTPClient(name string) *ResilientHTTPClient {
	return &ResilientHTTPClient{
		name: name,
		cb: NewCircuitBreaker(
			name,
			3,                // open หลัง 3 consecutive failures
			2,                // close หลัง 2 consecutive successes ใน HALF-OPEN
			10*time.Second,   // timeout ก่อน HALF-OPEN
		),
		bulkhead: NewBulkhead(
			name,
			5, // max 5 concurrent calls
		),
		rateLimiter: NewTokenBucket(
			10, // 10 requests per second
			20, // burst up to 20
		),
		retryCfg: DefaultRetryConfig,
		log:      slog.Default(),
	}
}

// Do ส่ง request ผ่าน resilience layers ทั้งหมด
func (c *ResilientHTTPClient) Do(ctx context.Context, operation func() error) error {
	// Layer 1: Rate Limiter
	if !c.rateLimiter.Allow() {
		return fmt.Errorf("%s: %w", c.name, ErrRateLimitExceeded)
	}

	// Layer 2: Circuit Breaker
	return c.cb.Execute(func() error {

		// Layer 3: Bulkhead
		return c.bulkhead.Execute(func() error {

			// Layer 4: Retry
			return WithRetry(ctx, c.retryCfg, func(attempt int) error {
				if attempt > 1 {
					c.log.Debug("Retrying request",
						"service", c.name,
						"attempt", attempt,
					)
				}

				// Layer 5: Actual operation
				return operation()
			})
		})
	})
}

// ─────────────────────────────────────────────
// Demo Scenarios
// ─────────────────────────────────────────────

// simulateCall จำลอง HTTP call ไปยัง downstream service
// failRate: 0.0 = ไม่ fail เลย, 1.0 = fail ทุกครั้ง
// latency: delay ของ call
func simulateCall(name string, failRate float64, latency time.Duration) func() error {
	return func() error {
		time.Sleep(latency)
		if rand.Float64() < failRate {
			return NewRetryableError(fmt.Sprintf("%s: service temporarily unavailable", name))
		}
		return nil
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	rand.Seed(time.Now().UnixNano()) //nolint:staticcheck

	fmt.Println(header("MODULE 5: Resilience Patterns Demo"))

	// ─── Demo 1: Circuit Breaker ───
	fmt.Println(section("1. Circuit Breaker"))
	demoCircuitBreaker()

	// ─── Demo 2: Retry with Backoff ───
	fmt.Println(section("2. Retry with Exponential Backoff + Jitter"))
	demoRetry()

	// ─── Demo 3: Bulkhead ───
	fmt.Println(section("3. Bulkhead"))
	demoBulkhead()

	// ─── Demo 4: Rate Limiter ───
	fmt.Println(section("4. Rate Limiter (Token Bucket)"))
	demoRateLimiter()

	// ─── Demo 5: Combined Resilient Client ───
	fmt.Println(section("5. Combined: ResilientHTTPClient"))
	demoCombined()
}

// demoCircuitBreaker แสดง state transitions ของ circuit breaker
func demoCircuitBreaker() {
	cb := NewCircuitBreaker("payment-service",
		3,              // 3 failures → OPEN
		2,              // 2 successes → CLOSED
		3*time.Second,  // 3s timeout
	)

	slog.Info("Circuit breaker demo: จะส่ง failure จนถึง threshold แล้วรอ recovery")

	// Phase 1: ส่ง failure จนเปิด circuit
	for i := 1; i <= 5; i++ {
		err := cb.Execute(func() error {
			return errors.New("connection refused")
		})
		slog.Info(fmt.Sprintf("  Call %d: state=%s err=%v", i, cb.State(), err != nil))
		time.Sleep(100 * time.Millisecond)
	}

	// Phase 2: รอ timeout แล้วส่ง success เพื่อปิด circuit
	slog.Info("Waiting for circuit to enter HALF-OPEN (3s)...")
	time.Sleep(3500 * time.Millisecond)

	// Phase 3: ส่ง success ใน HALF-OPEN
	for i := 1; i <= 3; i++ {
		err := cb.Execute(func() error {
			return nil // success
		})
		slog.Info(fmt.Sprintf("  Probe %d: state=%s err=%v", i, cb.State(), err != nil))
	}

	slog.Info("Circuit breaker final state", "state", cb.State())
}

// demoRetry แสดง retry พร้อม backoff
func demoRetry() {
	attemptCount := 0
	ctx := context.Background()

	slog.Info("Retry demo: service fails 3 ครั้งแรก แล้ว succeed")

	err := WithRetry(ctx, DefaultRetryConfig, func(attempt int) error {
		attemptCount++
		slog.Info(fmt.Sprintf("  Attempt %d/%d", attempt, DefaultRetryConfig.MaxAttempts))

		if attempt < 3 {
			return NewRetryableError("service temporarily unavailable")
		}
		return nil // สำเร็จในครั้งที่ 3
	})

	if err != nil {
		slog.Error("All retries failed", "error", err)
	} else {
		slog.Info("Succeeded after retries", "total_attempts", attemptCount)
	}
}

// demoBulkhead แสดง bulkhead ปฏิเสธ concurrent calls เกิน limit
func demoBulkhead() {
	bh := NewBulkhead("inventory-service", 3) // max 3 concurrent

	slog.Info("Bulkhead demo: ส่ง 8 concurrent calls, bulkhead รองรับแค่ 3")

	var wg sync.WaitGroup
	results := make([]error, 8)

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = bh.Execute(func() error {
				time.Sleep(300 * time.Millisecond) // simulate work
				return nil
			})
		}(i)
	}

	wg.Wait()

	total, rejected, _ := bh.Stats()
	successes := total - rejected
	slog.Info("Bulkhead results",
		"total_calls", total,
		"accepted", successes,
		"rejected", rejected,
	)
	slog.Info("ผลลัพธ์: rejected calls ป้องกัน resource exhaustion ฝั่ง inventory service")
}

// demoRateLimiter แสดง token bucket rate limiting
func demoRateLimiter() {
	// 5 requests/sec, burst 3
	tb := NewTokenBucket(5, 3)

	slog.Info("Rate limiter demo: ส่ง 10 requests ติดๆ กัน (rate limit = 5/sec, burst = 3)")

	allowed, rejected := 0, 0
	for i := 1; i <= 10; i++ {
		if tb.Allow() {
			allowed++
			slog.Info(fmt.Sprintf("  Request %2d: ALLOWED  (total_allowed=%d)", i, allowed))
		} else {
			rejected++
			slog.Warn(fmt.Sprintf("  Request %2d: REJECTED (total_rejected=%d)", i, rejected))
		}
		time.Sleep(50 * time.Millisecond) // 50ms gap = 20 requests/sec > limit
	}

	slog.Info("Rate limiter summary",
		"allowed", allowed,
		"rejected", rejected,
		"rate_limit", "5/sec",
		"burst", 3,
	)
}

// demoCombined แสดงการใช้ ResilientHTTPClient
func demoCombined() {
	paymentClient := NewResilientHTTPClient("payment-gateway")
	inventoryClient := NewResilientHTTPClient("inventory-service")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("Combined resilience demo: PlaceOrder ที่ใช้ 2 downstream services")

	var wg sync.WaitGroup
	var paymentErr, inventoryErr error

	// ส่ง requests แบบ concurrent ไปทั้ง 2 services (ใช้ goroutine แยก)
	wg.Add(2)

	go func() {
		defer wg.Done()
		paymentErr = paymentClient.Do(ctx, simulateCall("payment", 0.3, 50*time.Millisecond))
		if paymentErr != nil {
			slog.Warn("Payment failed", "error", paymentErr)
		} else {
			slog.Info("Payment succeeded")
		}
	}()

	go func() {
		defer wg.Done()
		inventoryErr = inventoryClient.Do(ctx, simulateCall("inventory", 0.1, 20*time.Millisecond))
		if inventoryErr != nil {
			slog.Warn("Inventory check failed", "error", inventoryErr)
		} else {
			slog.Info("Inventory check succeeded")
		}
	}()

	wg.Wait()

	if paymentErr != nil || inventoryErr != nil {
		slog.Error("Order placement failed",
			"payment_err", paymentErr,
			"inventory_err", inventoryErr,
		)
	} else {
		slog.Info("Order placed successfully with all resilience patterns")
	}

	// สรุป patterns
	fmt.Println()
	slog.Info("=== Resilience Patterns Summary ===")
	slog.Info("Pattern 1: Circuit Breaker",
		"prevents", "cascading failures จาก downstream service ที่ล้มเหลว",
		"mechanism", "fail fast แทนที่จะรอ timeout",
	)
	slog.Info("Pattern 2: Retry + Backoff",
		"prevents", "transient failures ทำให้ operation ล้มเหลวโดยไม่จำเป็น",
		"mechanism", "exponential backoff + jitter กระจาย load",
	)
	slog.Info("Pattern 3: Bulkhead",
		"prevents", "one slow service ดึง goroutines/connections ทั้งหมด",
		"mechanism", "semaphore จำกัด concurrency ต่อ service",
	)
	slog.Info("Pattern 4: Rate Limiter",
		"prevents", "overloading downstream service หรือถูก throttle",
		"mechanism", "token bucket ควบคุม throughput",
	)
}

// ─── Helper functions สำหรับ output formatting ───

func header(title string) string {
	line := strings.Repeat("═", 55)
	return fmt.Sprintf("\n%s\n  %s\n%s", line, title, line)
}

func section(title string) string {
	line := strings.Repeat("─", 50)
	return fmt.Sprintf("\n%s\n  %s\n%s\n", line, title, line)
}
