// Package race — Race Condition Tests
//
// # การทดสอบ Race Conditions
//
// วิธีรัน:
//
//	# รัน tests ปกติ (อาจไม่เจอ race ทุกครั้ง)
//	go test ./module6/race/
//
//	# รันพร้อม race detector — จำเป็นมากสำหรับ concurrent code
//	go test -race ./module6/race/
//
//	# รัน test เฉพาะ race tests หลายๆ รอบ
//	go test -race -count=10 ./module6/race/
//
//	# รัน test ที่แสดง bug โดยเฉพาะ
//	go test -race -run TestBuggyCache_RaceCondition ./module6/race/
//
// # ทำไม Race Detector ถึงสำคัญ?
//
//  Problem: race condition ไม่ทำให้ test fail เสมอไป
//  เพราะขึ้นอยู่กับ timing ของ goroutines ซึ่ง non-deterministic
//
//  Race detector ทำให้ deterministic ขึ้นโดย:
//  1. Track ทุก memory access
//  2. Report ทันทีเมื่อเจอ concurrent access ที่ไม่ปลอดภัย
//  3. ไม่ต้องรอให้ crash จริงๆ
//
//  ข้อจำกัด:
//  - ต้องมี concurrent access เกิดขึ้นจริงระหว่าง test
//  - Race ที่ไม่ถูก trigger ใน test จะไม่ถูกตรวจพบ
//  - ดังนั้น: เขียน test ให้มี concurrency จริงๆ (ดูตัวอย่างข้างล่าง)
package race

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ============================================================
// TEST: BUGGY CACHE — แสดง race condition
// ============================================================

// TestBuggyCache_RaceCondition แสดงว่า BuggyCache มี race condition
//
// Test นี้ออกแบบมาเพื่อ detect race ผ่าน race detector:
//  - รัน: go test -race -run TestBuggyCache_RaceCondition ./module6/race/
//  - คาดว่า: "DATA RACE" จาก race detector → test FAIL
//  - รันโดยไม่มี -race: อาจผ่าน (non-deterministic) เพราะ no instrumentation
//
// ทำไม test นี้ไม่ใช้ goroutines จำนวนมาก?
// Go map race ที่รุนแรงทำให้ "fatal error: concurrent map read and map write"
// ซึ่งเป็น unrecoverable panic ที่ crash ทั้ง test process
// เราใช้ goroutines น้อยพอที่ race detector จะ catch ได้ แต่ crash ยาก
//
// ⚠️ Note for instructor: demo ด้วย go test -race เท่านั้น
// ไม่ควรรัน TestBuggyCache_RaceCondition_Crash ใน production
func TestBuggyCache_RaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race test in short mode")
	}

	cache := NewBuggyCache()

	// ใช้แค่ 2 goroutines — น้อยพอที่จะไม่ crash แต่ race detector จะเจอ
	const opsPerGoroutine = 50

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < opsPerGoroutine; j++ {
			key := fmt.Sprintf("write-key-%d", j)
			// ⚠️ RACE: writing to map while reader reads
			cache.Set(key, j, time.Minute)
		}
	}()

	// Reader goroutine — races with writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < opsPerGoroutine; j++ {
			key := fmt.Sprintf("write-key-%d", j)
			// ⚠️ RACE: reading while writer writes
			cache.Get(key)
		}
	}()

	wg.Wait()

	// Without -race: อาจผ่าน (timing-dependent)
	// With -race:    FAIL with DATA RACE report pointing to exact lines
	t.Log("BuggyCache: completed without crash (but race detector will report DATA RACE)")
	t.Log("Run: go test -race -run TestBuggyCache_RaceCondition ./module6/race/")
}

// ============================================================
// TEST: FIXED CACHE — ตรวจว่าแก้ race แล้ว
// ============================================================

// TestConcurrentCache_NoRace ตรวจว่า ConcurrentCache ไม่มี race condition
//
// Test นี้ทำสิ่งเดียวกับ TestBuggyCache_RaceCondition แต่:
//  - ใช้ ConcurrentCache แทน BuggyCache
//  - ควรผ่านโดยไม่มี DATA RACE แม้ใช้ -race flag
//
// รัน: go test -race -run TestConcurrentCache_NoRace ./module6/race/
// คาดว่า: PASS (ไม่มี race)
func TestConcurrentCache_NoRace(t *testing.T) {
	cache := NewConcurrentCache()

	const numGoroutines = 50
	const opsPerGoroutine = 200

	var wg sync.WaitGroup

	// Writers
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", i, j)
				// FIX: mutex ป้องกัน concurrent write
				cache.Set(key, j, time.Minute)
			}
		}()
	}

	// Readers
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", i, j)
				// FIX: RLock อนุญาตหลาย readers พร้อมกัน
				cache.Get(key)
			}
		}()
	}

	wg.Wait()

	// ตรวจว่า cache ยังทำงานถูกต้องหลัง concurrent operations
	cache.Set("final-check", "hello", time.Minute)
	val, ok := cache.Get("final-check")
	if !ok {
		t.Error("expected cache hit after Set")
	}
	if val != "hello" {
		t.Errorf("cache value = %v, want hello", val)
	}
}

// TestConcurrentCache_ConcurrentReadWrite ทดสอบ concurrent read/write/delete
//
// ครอบคลุมทุก operations: Set, Get, Delete, Len
func TestConcurrentCache_ConcurrentReadWrite(t *testing.T) {
	cache := NewConcurrentCache()

	const goroutines = 30
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()

			key := fmt.Sprintf("worker-%d", i)

			// Set
			cache.Set(key, i*100, 5*time.Second)

			// Get — verify value
			val, ok := cache.Get(key)
			if ok {
				// ถ้า ok ต้อง return value ที่ถูกต้อง
				if intVal, isInt := val.(int); isInt && intVal != i*100 {
					errCh <- fmt.Errorf("goroutine %d: got value %d, want %d", i, intVal, i*100)
				}
			}
			// ถ้า !ok แสดงว่า goroutine อื่น delete ไปแล้ว — acceptable

			// Len — ต้อง non-negative
			if cache.Len() < 0 {
				errCh <- fmt.Errorf("goroutine %d: Len() = %d, must be non-negative", i, cache.Len())
			}

			// Delete
			cache.Delete(key)

			// Get หลัง delete — ต้อง miss
			_, ok = cache.Get(key)
			if ok {
				// อาจเกิดขึ้นถ้า goroutine อื่น re-insert key เดียวกัน
				// ใน test นี้แต่ละ goroutine มี key ของตัวเอง
				errCh <- fmt.Errorf("goroutine %d: expected cache miss after delete", i)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// ตรวจ errors
	for err := range errCh {
		t.Error(err)
	}
}

// TestConcurrentCache_ExpiredItems ทดสอบ TTL expiry ใน concurrent environment
func TestConcurrentCache_ExpiredItems(t *testing.T) {
	cache := NewConcurrentCache()

	// Set item ที่หมดอายุทันที
	cache.Set("expire-soon", "value", 1*time.Millisecond)

	// รอให้ expire
	time.Sleep(10 * time.Millisecond)

	var wg sync.WaitGroup
	const readers = 20

	// หลาย goroutines Get expired key พร้อมกัน
	// ทุก goroutine ควร miss และ trigger cleanup พร้อมกัน
	// Race condition อาจเกิดตรงนี้ถ้าไม่มี proper locking
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, ok := cache.Get("expire-soon")
			if ok {
				t.Errorf("expected cache miss for expired item, got value=%v", val)
			}
		}()
	}

	wg.Wait()

	// ตรวจว่า expired item ถูกลบออกจาก cache แล้ว
	if cache.Len() != 0 {
		t.Errorf("expected cache to be empty after expiry cleanup, got %d items", cache.Len())
	}
}

// TestConcurrentCache_Flush ทดสอบ Flush ใน concurrent environment
func TestConcurrentCache_Flush(t *testing.T) {
	cache := NewConcurrentCache()

	// Pre-populate
	for i := 0; i < 100; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), i, time.Minute)
	}

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			cache.Set(fmt.Sprintf("writer-%d", i), i, time.Minute)
		}()
	}

	// Flush ระหว่างที่ writers กำลังทำงาน
	wg.Add(1)
	go func() {
		defer wg.Done()
		cache.Flush()
	}()

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Len() // ต้องไม่ panic
		}()
	}

	wg.Wait()
	// หลัง Flush cache ต้อง functional (ไม่ corrupt)
	cache.Set("after-flush", "ok", time.Minute)
	val, ok := cache.Get("after-flush")
	if !ok || val != "ok" {
		t.Error("cache not functional after concurrent Flush")
	}
}

// ============================================================
// TEST: CORRECTNESS — ทดสอบ business logic ถูกต้อง
// ============================================================

// TestConcurrentCache_TTL ทดสอบ TTL mechanism
func TestConcurrentCache_TTL(t *testing.T) {
	t.Parallel()

	cache := NewConcurrentCache()

	t.Run("item expires after TTL", func(t *testing.T) {
		t.Parallel()

		cache.Set("ttl-test", "value", 50*time.Millisecond)

		// ก่อน expire — ต้อง hit
		val, ok := cache.Get("ttl-test")
		if !ok {
			t.Fatal("expected cache hit before TTL expires")
		}
		if val != "value" {
			t.Errorf("value = %v, want value", val)
		}

		// รอให้ expire
		time.Sleep(100 * time.Millisecond)

		// หลัง expire — ต้อง miss
		_, ok = cache.Get("ttl-test")
		if ok {
			t.Error("expected cache miss after TTL expires")
		}
	})

	t.Run("item with long TTL stays accessible", func(t *testing.T) {
		t.Parallel()

		cache.Set("long-ttl", 42, time.Hour)

		val, ok := cache.Get("long-ttl")
		if !ok {
			t.Fatal("expected cache hit for long-TTL item")
		}
		if val.(int) != 42 {
			t.Errorf("value = %v, want 42", val)
		}
	})

	t.Run("overwrite resets TTL", func(t *testing.T) {
		t.Parallel()

		// Set ด้วย TTL สั้น
		cache.Set("overwrite-test", "first", 50*time.Millisecond)

		// Overwrite ด้วย TTL ยาว
		cache.Set("overwrite-test", "second", time.Hour)

		// รอนานกว่า TTL แรก
		time.Sleep(100 * time.Millisecond)

		// ควรยังอยู่เพราะ TTL ถูก reset
		val, ok := cache.Get("overwrite-test")
		if !ok {
			t.Fatal("expected cache hit after overwrite with longer TTL")
		}
		if val != "second" {
			t.Errorf("value = %v, want second", val)
		}
	})
}

// TestConcurrentCache_Stats ทดสอบ statistics tracking
func TestConcurrentCache_Stats(t *testing.T) {
	t.Parallel()

	cache := NewConcurrentCache()

	// 3 hits
	cache.Set("a", 1, time.Minute)
	cache.Set("b", 2, time.Minute)
	cache.Set("c", 3, time.Minute)
	cache.Get("a")
	cache.Get("b")
	cache.Get("c")

	// 2 misses
	cache.Get("x")
	cache.Get("y")

	// 1 eviction
	cache.Set("expire", "v", 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	cache.Get("expire") // trigger eviction

	hits, misses, evictions := cache.Stats()

	if hits != 3 {
		t.Errorf("hits = %d, want 3", hits)
	}
	if misses != 3 { // 2 misses + 1 from expired
		t.Errorf("misses = %d, want 3", misses)
	}
	if evictions != 1 {
		t.Errorf("evictions = %d, want 1", evictions)
	}
}

// ============================================================
// BENCHMARK — เปรียบเทียบ performance
// ============================================================

// BenchmarkConcurrentCache_ReadHeavy วัด throughput สำหรับ read-heavy workload
// รัน: go test -bench=. ./module6/race/
func BenchmarkConcurrentCache_ReadHeavy(b *testing.B) {
	cache := NewConcurrentCache()

	// Pre-populate 1000 items
	for i := 0; i < 1000; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), i, time.Hour)
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%1000)
			cache.Get(key)
			i++
		}
	})
}

// BenchmarkBuggyCache_ReadHeavy ใช้เปรียบเทียบ (ไม่ควรรันใน production)
// ⚠️ Race condition test — อาจ panic ถ้า goroutines มากพอ
// รัน: go test -bench=BenchmarkBuggyCache -run=^$ ./module6/race/
func BenchmarkBuggyCache_ReadHeavy(b *testing.B) {
	cache := NewBuggyCache()

	// Pre-populate (single goroutine ก่อน benchmark)
	for i := 0; i < 1000; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), i, time.Hour)
	}

	// ⚠️ Race condition ตรงนี้ — แต่ benchmark ต้องการ parallelism
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%1000)
			cache.Get(key)
			i++
		}
	})
}
