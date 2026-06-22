// Package race — Race Condition Testing ใน Go
//
// Race condition คือ bug ที่เกิดขึ้นเมื่อ goroutines หลายตัว
// access shared data พร้อมกัน โดยที่ไม่มี synchronization
//
// Go race detector ช่วยหา race conditions ได้:
//
//	go test -race ./module6/race/
//	go build -race ./module6/race/
//	go run -race ./module6/race/main.go
//
// Race detector ทำงานอย่างไร?
//   - เพิ่ม instrumentation เข้าใน binary ตอน compile
//   - Track ทุก memory access (read/write)
//   - ถ้า 2 goroutines access same memory และมีอย่างน้อย 1 write → report race
//   - Overhead: ~2-20x slower, 5-10x more memory
//   - ไม่ false positive แต่อาจ false negative (race ที่ยังไม่เกิดขึ้น)
package race

import (
	"sync"
	"time"
)

// ============================================================
// VERSION 1: BUGGY CACHE — มี race condition
// ============================================================
//
// BuggyCache — simple in-memory cache ที่มี race condition
//
// Bug: map ใน Go ไม่ thread-safe
// ถ้า goroutines หลายตัว read/write พร้อมกัน → fatal error: concurrent map read and map write
//
// ⚠️ Race condition ที่ร้ายแรง: บางครั้ง data race ไม่ทำให้ crash ทันที
// แต่ทำให้ data corrupt อย่างเงียบๆ ซึ่งยากจะหาสาเหตุมาก
type BuggyCache struct {
	// ⚠️ BUG: map ที่ไม่มี mutex — เข้าถึงพร้อมกันได้ panic
	items map[string]CacheItem
}

// CacheItem เก็บ value และ expiry time
type CacheItem struct {
	Value     interface{}
	ExpiresAt time.Time
}

// NewBuggyCache สร้าง cache ที่มี bug
func NewBuggyCache() *BuggyCache {
	return &BuggyCache{
		items: make(map[string]CacheItem),
	}
}

// Set เขียน value เข้า cache
// ⚠️ BUG: ไม่ lock map ก่อนเขียน
func (c *BuggyCache) Set(key string, value interface{}, ttl time.Duration) {
	// ⚠️ RACE: goroutine อื่นอาจกำลัง Set/Get/Delete พร้อมกัน
	c.items[key] = CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Get อ่าน value จาก cache
// ⚠️ BUG: ไม่ lock map ก่อนอ่าน
func (c *BuggyCache) Get(key string) (interface{}, bool) {
	// ⚠️ RACE: goroutine อื่นอาจกำลัง Set/Delete พร้อมกัน
	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.ExpiresAt) {
		// ⚠️ RACE: ยังไม่ได้ lock แต่เรียก Delete
		delete(c.items, key) // ลบ expired item
		return nil, false
	}
	return item.Value, true
}

// Delete ลบ key จาก cache
// ⚠️ BUG: ไม่ lock map
func (c *BuggyCache) Delete(key string) {
	delete(c.items, key) // ⚠️ RACE
}

// Len returns จำนวน items ใน cache (ไม่นับ expired)
// ⚠️ BUG: อาจนับ stale data เพราะไม่ lock
func (c *BuggyCache) Len() int {
	return len(c.items) // ⚠️ RACE
}

// ============================================================
// VERSION 2: FIXED CACHE — ใช้ sync.RWMutex
// ============================================================
//
// ConcurrentCache — thread-safe cache ด้วย sync.RWMutex
//
// ทำไมใช้ RWMutex แทน Mutex?
//   - RLock() อนุญาตให้ goroutines หลายตัว read พร้อมกัน
//   - Lock() exclusive: ไม่มีใคร read หรือ write ระหว่างนี้
//   - Cache มัก read มากกว่า write → RWMutex ให้ throughput ดีกว่า Mutex
//   - Mutex: read และ write ทีละตัวเสมอ
//
// Performance comparison (approximate):
//   - Mutex: 1 goroutine ทำงานได้ในแต่ละเวลา
//   - RWMutex (read heavy): ~N goroutines read พร้อมกันได้
//   - sync.Map: ดีกว่าสำหรับ mostly-append workload, read once write once
type ConcurrentCache struct {
	mu    sync.RWMutex      // Read-Write Mutex สำหรับ thread safety
	items map[string]CacheItem

	// Stats สำหรับ monitoring (เป็น atomic แยกต่างหาก)
	stats CacheStats
}

// CacheStats เก็บ statistics ของ cache
type CacheStats struct {
	mu       sync.Mutex
	hits     int64
	misses   int64
	evictions int64
}

// NewConcurrentCache สร้าง cache ที่ thread-safe
func NewConcurrentCache() *ConcurrentCache {
	return &ConcurrentCache{
		items: make(map[string]CacheItem),
	}
}

// Set เขียน value เข้า cache (thread-safe)
//
// ใช้ Lock() ไม่ใช่ RLock() เพราะเป็น write operation
func (c *ConcurrentCache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()         // FIX: exclusive lock สำหรับ write
	defer c.mu.Unlock() // unlock เสมอ แม้จะ panic

	c.items[key] = CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Get อ่าน value จาก cache (thread-safe)
//
// ใช้ RLock() เพราะ read ส่วนใหญ่ แต่ถ้าเจอ expired item
// ต้อง upgrade เป็น Lock() เพื่อ delete
func (c *ConcurrentCache) Get(key string) (interface{}, bool) {
	// Phase 1: Read lock — อนุญาตหลาย goroutines อ่านพร้อมกัน
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock() // ปล่อย read lock ก่อน

	if !ok {
		c.stats.recordMiss()
		return nil, false
	}

	if time.Now().After(item.ExpiresAt) {
		// Phase 2: Write lock — ต้องการ exclusive access เพื่อ delete
		// Note: ต้อง check อีกครั้งหลัง upgrade (double-checked locking)
		c.mu.Lock()
		defer c.mu.Unlock()

		// Double-check: อาจมี goroutine อื่น delete แล้วก่อนเรา
		if _, stillExists := c.items[key]; stillExists {
			delete(c.items, key)
			c.stats.recordEviction()
		}

		c.stats.recordMiss()
		return nil, false
	}

	c.stats.recordHit()
	return item.Value, true
}

// Delete ลบ key จาก cache (thread-safe)
func (c *ConcurrentCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
}

// Len returns จำนวน items ใน cache (รวม expired ที่ยังไม่ถูกลบ)
func (c *ConcurrentCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// Keys returns ทุก keys ใน cache ณ เวลาที่เรียก
// สังเกต: ผลลัพธ์อาจ stale เล็กน้อยเพราะ concurrent modifications
// แต่ไม่มี race condition เพราะ snapshot ใต้ lock
func (c *ConcurrentCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}

// Flush ลบทุก items ใน cache
func (c *ConcurrentCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]CacheItem)
}

// Stats returns cache statistics snapshot
func (c *ConcurrentCache) Stats() (hits, misses, evictions int64) {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()

	return c.stats.hits, c.stats.misses, c.stats.evictions
}

// ===== CacheStats helpers =====

func (s *CacheStats) recordHit() {
	s.mu.Lock()
	s.hits++
	s.mu.Unlock()
}

func (s *CacheStats) recordMiss() {
	s.mu.Lock()
	s.misses++
	s.mu.Unlock()
}

func (s *CacheStats) recordEviction() {
	s.mu.Lock()
	s.evictions++
	s.mu.Unlock()
}

// ============================================================
// VERSION 3: ALTERNATIVE — ใช้ sync.Map
// ============================================================
//
// SyncMapCache — cache ที่ใช้ sync.Map built-in
//
// เหมาะสำหรับ:
//   - Write once, read many times
//   - Keys ที่ไม่ overlap ระหว่าง goroutines
//   - ไม่ต้องการ batch operations
//
// ไม่เหมาะสำหรับ:
//   - Counter / accumulator (ต้องการ atomic operations)
//   - Batch read (Keys(), Len() ต้องวน loop)
//   - TTL-based cache (ต้องการ external cleanup)
//
// สำหรับ general-purpose cache: ConcurrentCache (RWMutex) ดีกว่าในส่วนใหญ่
type SyncMapCache struct {
	m sync.Map
}

// Set stores a value (sync.Map version)
func (c *SyncMapCache) Set(key string, value interface{}) {
	c.m.Store(key, value)
}

// Get retrieves a value (sync.Map version)
func (c *SyncMapCache) Get(key string) (interface{}, bool) {
	return c.m.Load(key)
}

// Delete removes a key (sync.Map version)
func (c *SyncMapCache) Delete(key string) {
	c.m.Delete(key)
}

// Len counts items — O(n) ใน sync.Map
// นี่คือข้อเสียของ sync.Map: ไม่มี O(1) Len()
func (c *SyncMapCache) Len() int {
	count := 0
	c.m.Range(func(_, _ interface{}) bool {
		count++
		return true // continue iteration
	})
	return count
}
