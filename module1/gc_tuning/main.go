// Package main สาธิต GC Tuning และ Escape Analysis
// Build tags สำหรับ escape analysis: go build -gcflags="-m" main.go
//
// วิธีรัน:
//
//	go run ./module1/gc_tuning/
//
// ทดสอบด้วย environment variables:
//
//	GOGC=200 go run ./module1/gc_tuning/
//	GOMEMLIMIT=64MiB go run ./module1/gc_tuning/
//	GOGC=off GOMEMLIMIT=128MiB go run ./module1/gc_tuning/
package main

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

// ============================================================
// ESCAPE ANALYSIS — Stack vs Heap allocation
// ============================================================

// stackAllocation — value อยู่บน stack (ไม่ถูก GC)
// go build -gcflags="-m" จะบอกว่า "does not escape"
func stackAllocation() int {
	// x อยู่บน stack เพราะไม่มีอะไร "capture" มัน
	x := 42
	return x // copy value กลับไป ไม่ใช่ pointer
}

// heapAllocation — value อยู่บน heap (ถูก GC)
// go build -gcflags="-m" จะบอกว่า "moved to heap"
func heapAllocation() *int {
	x := 42
	// เมื่อ return pointer ไปยัง local variable
	// x ต้อง "escape" ไปยัง heap เพื่อให้ caller ใช้ได้
	return &x
}

// interfaceEscape — interface boxing ทำให้ escape to heap
func interfaceEscape() {
	x := 42
	// การใส่ value ลงใน interface จะทำให้ escape
	var i interface{} = x // x escaped to heap!
	fmt.Println(i)
}

// ============================================================
// GC METRICS — ดู GC stats
// ============================================================

type GCStats struct {
	NumGC        uint32
	PauseTotalNs uint64
	LastPause    time.Duration
}

func collectGCStats() GCStats {
	var stats debug.GCStats
	debug.ReadGCStats(&stats)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var lastPause time.Duration
	if len(stats.Pause) > 0 {
		lastPause = stats.Pause[0]
	}

	return GCStats{
		NumGC:        ms.NumGC,
		PauseTotalNs: ms.PauseTotalNs,
		LastPause:    lastPause,
	}
}

func printMemStats(label string) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	fmt.Printf("\n=== %s ===\n", label)
	fmt.Printf("HeapAlloc:   %8d KB (ใช้งานอยู่จริง)\n", ms.HeapAlloc/1024)
	fmt.Printf("HeapSys:     %8d KB (ขอจาก OS)\n", ms.HeapSys/1024)
	fmt.Printf("HeapInuse:   %8d KB (spans ที่มี objects)\n", ms.HeapInuse/1024)
	fmt.Printf("HeapReleased:%8d KB (คืน OS แล้ว)\n", ms.HeapReleased/1024)
	fmt.Printf("NumGC:       %8d (จำนวนครั้ง GC)\n", ms.NumGC)
	fmt.Printf("PauseTotalNs:%8d ms (รวม pause time)\n", ms.PauseTotalNs/1e6)
	fmt.Printf("NextGC:      %8d KB (trigger ต่อไป)\n", ms.NextGC/1024)
}

// ============================================================
// GC TUNING DEMO
// ============================================================

// allocateLots สร้าง garbage เยอะๆ เพื่อดู GC behavior
func allocateLots(n int) {
	for i := 0; i < n; i++ {
		// สร้าง slice แล้วทิ้ง — เป็น garbage ทันที
		_ = make([]byte, 1024)
	}
}

func demonstrateGOGC() {
	fmt.Println("\n=== GOGC Tuning Demo ===")

	// GOGC=100 (default)
	debug.SetGCPercent(100)
	printMemStats("Before (GOGC=100)")
	allocateLots(10000)
	runtime.GC() // force GC เพื่อดูผล
	printMemStats("After allocations (GOGC=100)")

	// GOGC=200 — GC ช้าลง ใช้ memory มากขึ้น
	debug.SetGCPercent(200)
	allocateLots(10000)
	runtime.GC()
	printMemStats("After allocations (GOGC=200)")

	// GOGC=50 — GC บ่อยขึ้น ใช้ memory น้อยลง
	debug.SetGCPercent(50)
	allocateLots(10000)
	runtime.GC()
	printMemStats("After allocations (GOGC=50)")
}

// ============================================================
// GOMEMLIMIT — Soft Memory Limit (Go 1.19+)
// ============================================================

// demonstrateGOMEMLIMIT แสดงการใช้ GOMEMLIMIT ร่วมกับ GOGC
// GOMEMLIMIT กำหนด ceiling ของ memory ที่ Go runtime ใช้ได้
// เมื่อ heap ใกล้ limit → GC pacer จะ aggressive มากขึ้นโดยอัตโนมัติ
func demonstrateGOMEMLIMIT() {
	fmt.Println("\n=== GOMEMLIMIT Demo ===")

	// อ่าน GOMEMLIMIT ปัจจุบัน
	currentLimit := debug.SetMemoryLimit(-1) // -1 = อ่านค่า ไม่เปลี่ยน
	if currentLimit == math.MaxInt64 {
		fmt.Println("GOMEMLIMIT: ไม่ได้ตั้ง (unlimited)")
	} else {
		fmt.Printf("GOMEMLIMIT: %d MB\n", currentLimit/1024/1024)
	}

	// ตั้ง soft limit = 64 MB (สำหรับ demo)
	// ใน production ตั้งผ่าน env var: GOMEMLIMIT=512MiB ./myapp
	const limitMB = 64
	debug.SetMemoryLimit(limitMB * 1024 * 1024)
	fmt.Printf("ตั้ง GOMEMLIMIT = %d MB\n", limitMB)

	// ทดสอบ: allocate ใกล้ limit แล้วดูว่า GC ทำงานอย่างไร
	printMemStats("Before heavy allocation")

	// Allocate ประมาณ 50 MB — GC จะทำงานบ่อยขึ้นเพื่อรักษา limit
	for batch := 0; batch < 50; batch++ {
		_ = make([]byte, 1*1024*1024) // 1 MB per batch
	}
	runtime.GC()
	printMemStats("After 50 MB allocation (GOMEMLIMIT=64MB)")

	// Reset limit กลับเป็น unlimited
	debug.SetMemoryLimit(0)
}

// ============================================================
// runtime/metrics — GC Metrics ละเอียด (Go 1.16+)
// ============================================================

// demonstrateRuntimeMetrics แสดงการใช้ runtime/metrics package
// ได้ข้อมูลละเอียดกว่า runtime.MemStats เช่น GC goal, pacer info
func demonstrateRuntimeMetrics() {
	fmt.Println("\n=== runtime/metrics Demo ===")

	// กำหนด metrics ที่ต้องการอ่าน
	desiredMetrics := []metrics.Sample{
		{Name: "/gc/heap/goal:bytes"},                // GC จะ trigger เมื่อ heap ถึงค่านี้
		{Name: "/gc/heap/live:bytes"},                // live objects ตอนนี้
		{Name: "/gc/cycles/total:gc-cycles"},         // จำนวน GC cycles ทั้งหมด
		{Name: "/gc/pause/total-ns:ns"},              // รวม pause time (nanoseconds)
		{Name: "/memory/classes/heap/objects:bytes"}, // heap object bytes
		{Name: "/sched/goroutines:goroutines"},       // จำนวน goroutines
	}

	metrics.Read(desiredMetrics)

	for _, m := range desiredMetrics {
		switch m.Value.Kind() {
		case metrics.KindUint64:
			val := m.Value.Uint64()
			switch {
			case val > 1024*1024:
				fmt.Printf("  %-45s %10.2f MB\n", m.Name, float64(val)/1024/1024)
			case val > 1024:
				fmt.Printf("  %-45s %10.2f KB\n", m.Name, float64(val)/1024)
			default:
				fmt.Printf("  %-45s %10d\n", m.Name, val)
			}
		case metrics.KindFloat64:
			fmt.Printf("  %-45s %10.4f\n", m.Name, m.Value.Float64())
		}
	}
}

// watchGCMetrics monitor GC metrics ต่อเนื่อง (สำหรับ real-time monitoring)
func watchGCMetrics(duration time.Duration, interval time.Duration) {
	fmt.Printf("\n=== GC Monitoring (%v, interval=%v) ===\n", duration, interval)
	fmt.Printf("%-5s %-12s %-12s %-10s %-12s\n",
		"T(s)", "HeapLive(MB)", "HeapGoal(MB)", "GCCycles", "Pause(ms)")

	samples := []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/gc/pause/total-ns:ns"},
	}

	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for t := range ticker.C {
		metrics.Read(samples)
		elapsed := t.Sub(start).Seconds()
		live := float64(samples[0].Value.Uint64()) / 1024 / 1024
		goal := float64(samples[1].Value.Uint64()) / 1024 / 1024
		cycles := samples[2].Value.Uint64()
		pauseMs := float64(samples[3].Value.Uint64()) / 1e6
		fmt.Printf("%-5.1f %-12.2f %-12.2f %-10d %-12.2f\n",
			elapsed, live, goal, cycles, pauseMs)
		if time.Since(start) >= duration {
			return
		}
	}
}

// ============================================================
// GOGC OFF — อันตราย! ตัวอย่างให้รู้ว่าไม่ควรทำ
// ============================================================

func demonstrateGOGCOff() {
	fmt.Println("\n=== DANGER: GOGC=off Demo ===")
	fmt.Println("GOGC=off หมายความว่า GC ไม่ทำงานเลย!")
	fmt.Println("Memory จะเพิ่มขึ้นเรื่อยๆ จนหมด")

	// ตัวอย่างที่ถูกต้องสำหรับการใช้ GOGC=off:
	// - Short-lived batch job ที่รู้ว่า memory ไม่เกิน limit
	// - Load test เพื่อวัด throughput สูงสุด
	// - ไม่ควรใช้ใน long-running services!

	// เปิด GC ปกติ
	debug.SetGCPercent(100)
}

// ============================================================
// RUNTIME GOROUTINE INFO
// ============================================================

func showRuntimeInfo() {
	fmt.Println("\n=== Runtime Information ===")
	fmt.Printf("GOMAXPROCS: %d (จำนวน P)\n", runtime.GOMAXPROCS(0))
	fmt.Printf("NumCPU:     %d (จำนวน CPU จริง)\n", runtime.NumCPU())
	fmt.Printf("NumGoroutine: %d (goroutines ตอนนี้)\n", runtime.NumGoroutine())
	fmt.Printf("GOARCH: %s\n", runtime.GOARCH)
	fmt.Printf("GOOS:   %s\n", runtime.GOOS)
}

// ============================================================
// GMP SCHEDULER DEMO
// ============================================================

// demonstrateLockOSThread แสดงการใช้ LockOSThread
// ใช้เมื่อ: CGO calls, thread-local storage, OS thread affinity
func demonstrateLockOSThread() {
	done := make(chan struct{})

	go func() {
		// Lock goroutine นี้ไว้กับ OS thread เดิม
		// ใช้สำหรับ: CGO, OpenGL, GUI libraries ที่ต้องการ thread affinity
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		fmt.Println("Goroutine locked to OS thread")
		// ทำ thread-specific operations ที่นี่
		// เช่น: C.some_thread_local_func()

		close(done)
	}()

	<-done
}

func main() {
	showRuntimeInfo()
	demonstrateGOGC()
	demonstrateGOMEMLIMIT()
	demonstrateGOGCOff()
	demonstrateLockOSThread()

	// Escape analysis examples
	v := stackAllocation()
	p := heapAllocation()
	fmt.Printf("\nStack value: %d, Heap value: %d\n", v, *p)

	// ดู GC stats ผ่าน runtime/metrics
	fmt.Println("\n=== GC Metrics (runtime/metrics) ===")
	demonstrateRuntimeMetrics()

	// Watch GC ขณะ allocate (5 วินาที, ทุก 500ms)
	go func() {
		for i := 0; i < 20; i++ {
			allocateLots(5000)
			time.Sleep(200 * time.Millisecond)
		}
	}()
	watchGCMetrics(5*time.Second, 500*time.Millisecond)

	// ดู GC stats (legacy)
	stats := collectGCStats()
	fmt.Printf("\nGC Stats (legacy): NumGC=%d, TotalPause=%dms\n",
		stats.NumGC, stats.PauseTotalNs/1e6)
}
