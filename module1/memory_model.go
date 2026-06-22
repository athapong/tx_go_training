// Package main สาธิต Go Memory Model, Happens-Before, และ Data Race
// รันด้วย: go run -race memory_model.go
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// EXAMPLE 1: Data Race ที่ "ดูเหมือนจะถูก" แต่ผิด
// ============================================================

// BrokenCounter — มี data race!
// ปัญหา: goroutines หลายตัวเขียน count พร้อมกัน
type BrokenCounter struct {
	count int // ไม่มี synchronization
}

func (c *BrokenCounter) Increment() {
	// การทำงานจริงของ CPU:
	// 1. LOAD: อ่าน c.count จาก memory → register
	// 2. ADD:  register += 1
	// 3. STORE: เขียน register → c.count
	// ถ้า 2 goroutines ทำพร้อมกัน:
	// G1: LOAD(0) → ADD(1) → [G2 preempt] → STORE(1)
	// G2: LOAD(0) → ADD(1) → STORE(1)
	// ผล: count = 1 แทนที่จะเป็น 2!
	c.count++
}

func (c *BrokenCounter) Value() int {
	return c.count
}

// ============================================================
// EXAMPLE 2: Mutex Solution — ถูกต้อง แต่ใช้ lock/unlock
// ============================================================

type MutexCounter struct {
	mu    sync.Mutex
	count int
}

func (c *MutexCounter) Increment() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// happens-before: Lock() รับประกันว่าเห็น write ล่าสุด
	c.count++
}

func (c *MutexCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// ============================================================
// EXAMPLE 3: Atomic Solution — เร็วกว่า Mutex สำหรับ simple counter
// ============================================================

type AtomicCounter struct {
	// int64 ต้องอยู่บน 64-bit aligned address
	// ใช้ int64 ไม่ใช่ int เพื่อให้ใช้ atomic ได้บน 32-bit systems
	count int64
}

func (c *AtomicCounter) Increment() {
	// atomic.AddInt64 เป็น single CPU instruction (LOCK XADD)
	// ไม่ต้องการ mutex — เร็วกว่ามาก
	atomic.AddInt64(&c.count, 1)
}

func (c *AtomicCounter) Value() int64 {
	// ต้องใช้ atomic.LoadInt64 ไม่ใช่อ่าน c.count โดยตรง
	// เพราะ read ก็ต้องมี memory ordering guarantee
	return atomic.LoadInt64(&c.count)
}

// ============================================================
// EXAMPLE 4: Happens-Before ผ่าน Channel
// ============================================================

// demonstrateHappensBefore แสดงว่า channel send happens-before receive
func demonstrateHappensBefore() {
	var data string
	done := make(chan struct{})

	go func() {
		// Write happens BEFORE send to channel
		data = "hello from goroutine"
		// Channel send เป็น synchronization point
		done <- struct{}{}
	}()

	// Channel receive happens AFTER send
	// ดังนั้น รับประกันว่า read data เห็น "hello from goroutine"
	<-done
	fmt.Println(data) // รับประกัน: พิมพ์ "hello from goroutine"
}

// demonstrateWrongHappensBefore — ผิด! ไม่มี happens-before
func demonstrateWrongHappensBefore() {
	var data string
	var ready bool

	go func() {
		data = "hello"
		ready = true // ไม่มี synchronization!
	}()

	// Busy wait — อันตราย!
	// 1. Compiler อาจ optimize loop นี้ออกไป (เพราะ ready ดูเหมือนไม่เปลี่ยน)
	// 2. CPU อาจไม่เห็น ready=true เพราะ cache coherence
	// 3. แม้ ready=true แต่ data อาจยังไม่ถูก update
	for !ready {
		time.Sleep(time.Millisecond)
	}
	fmt.Println(data) // UNDEFINED BEHAVIOR! อาจได้ "" หรือ "hello"
}

// ============================================================
// EXAMPLE 5: เมื่อใช้ Atomic vs Mutex
// ============================================================

// ใช้ Atomic เมื่อ:
// - ต้องการ simple read/write/add บน single value
// - ต้องการ performance สูงสุด (lock-free)
// - เช่น: counter, flag, pointer swap

// ใช้ Mutex เมื่อ:
// - ต้องการ protect ข้อมูลหลาย fields พร้อมกัน
// - ต้องการ read-modify-write ที่ซับซ้อน
// - เช่น: map update, struct update หลาย fields

type Config struct {
	mu       sync.RWMutex
	settings map[string]string
}

func (c *Config) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// atomic ไม่พอ เพราะต้องทำหลาย operations:
	// 1. อ่าน map
	// 2. เขียน map
	// ทั้งหมดต้องเป็น atomic group
	c.settings[key] = value
}

func (c *Config) Get(key string) string {
	// RLock: อนุญาตให้หลาย goroutines อ่านพร้อมกัน
	// แต่บล็อคถ้ามีใครกำลัง write
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings[key]
}

// ============================================================
// EXAMPLE 6: sync.Once — Initialization ที่ปลอดภัย
// ============================================================

type Singleton struct {
	value int
}

var (
	instance *Singleton
	once     sync.Once
)

func GetInstance() *Singleton {
	// once.Do รับประกันว่า function ถูกเรียกครั้งเดียว
	// แม้จะมี 1000 goroutines เรียกพร้อมกัน
	// Happens-before: Do() ครั้งแรก happens-before Do() ครั้งต่อไป
	once.Do(func() {
		instance = &Singleton{value: 42}
		fmt.Println("Singleton initialized")
	})
	return instance
}

// ============================================================
// BENCHMARK COMPARISON: Atomic vs Mutex
// ============================================================
// รัน: go test -bench=. -benchmem ./module1/
// ผล (ตัวอย่าง):
// BenchmarkAtomicIncrement-8    200000000    6.5 ns/op    0 B/op
// BenchmarkMutexIncrement-8     50000000    28.3 ns/op    0 B/op
// Atomic เร็วกว่า Mutex ~4x สำหรับ simple counter

// ============================================================
// DEMO: Race Detector
// ============================================================

func runRaceDemo() {
	fmt.Println("\n=== Demonstrating Race Detector ===")
	fmt.Println("Run with: go run -race memory_model.go")

	// ตัวอย่าง: broken counter
	broken := &BrokenCounter{}
	var wg sync.WaitGroup

	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			broken.Increment() // DATA RACE! race detector จะตรวจพบ
		}()
	}
	wg.Wait()
	fmt.Printf("Broken counter (ผลไม่แน่นอน): %d\n", broken.Value())

	// ตัวอย่าง: atomic counter
	atomic_c := &AtomicCounter{}
	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic_c.Increment()
		}()
	}
	wg.Wait()
	fmt.Printf("Atomic counter (ถูกต้องเสมอ): %d\n", atomic_c.Value())

	// ตัวอย่าง: mutex counter
	mutex_c := &MutexCounter{}
	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mutex_c.Increment()
		}()
	}
	wg.Wait()
	fmt.Printf("Mutex counter (ถูกต้องเสมอ): %d\n", mutex_c.Value())
}

func main() {
	demonstrateHappensBefore()
	runRaceDemo()

	// ตัวอย่าง Config
	cfg := &Config{settings: make(map[string]string)}
	cfg.Set("host", "localhost")
	cfg.Set("port", "8080")
	fmt.Printf("Config: host=%s, port=%s\n",
		cfg.Get("host"), cfg.Get("port"))

	// Singleton
	s1 := GetInstance()
	s2 := GetInstance()
	fmt.Printf("Same instance: %v\n", s1 == s2)
}
