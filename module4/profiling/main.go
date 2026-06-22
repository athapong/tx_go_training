// Module 4: High-Performance Go — Profiling Demo
//
// โปรแกรมนี้แสดงวิธีการทำ profiling ใน Go อย่างครบวงจร
// ตั้งแต่การ setup pprof endpoint ไปจนถึงการ generate trace
//
// วิธีรัน:
//   go run ./module4/profiling/
//
// วิธีเก็บ profile:
//   # CPU profile (30 วินาที)
//   go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
//
//   # Memory profile
//   go tool pprof http://localhost:6060/debug/pprof/heap
//
//   # Goroutine profile
//   go tool pprof http://localhost:6060/debug/pprof/goroutine
//
//   # Trace (5 วินาที)
//   curl -o trace.out http://localhost:6060/debug/pprof/trace?seconds=5
//   go tool trace trace.out

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	_ "net/http/pprof" // สำคัญ: import แบบ blank เพื่อ register pprof handlers อัตโนมัติ
	"os"
	"os/signal"
	"runtime"
	"runtime/trace"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ===== SECTION 1: pprof HTTP Endpoints =====
//
// เมื่อ import "net/http/pprof" แบบ blank (_) Go จะ register endpoints เหล่านี้
// บน DefaultServeMux โดยอัตโนมัติ:
//
//   GET /debug/pprof/              — index หน้ารวมลิงก์ทั้งหมด
//   GET /debug/pprof/cmdline       — command line ที่ใช้รัน process
//   GET /debug/pprof/profile       — CPU profile (ต้อง query param ?seconds=N)
//   GET /debug/pprof/symbol        — แปลง PC address เป็น symbol name
//   GET /debug/pprof/trace         — execution trace (ต้อง query param ?seconds=N)
//   GET /debug/pprof/allocs        — memory allocation profile
//   GET /debug/pprof/block         — goroutine blocking profile
//   GET /debug/pprof/goroutine     — stack traces ของ goroutine ทั้งหมด
//   GET /debug/pprof/heap          — heap memory profile
//   GET /debug/pprof/mutex         — mutex contention profile
//   GET /debug/pprof/threadcreate  — OS thread creation profile

// Product จำลอง entity จาก database
// ในระบบจริง struct นี้อาจมี field มากกว่านี้มาก
type Product struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Price       float64   `json:"price"`
	Stock       int       `json:"stock"`
	Category    string    `json:"category"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OrderLine แสดง 1 บรรทัดของ order
type OrderLine struct {
	ProductID int     `json:"product_id"`
	Quantity  int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	Subtotal  float64 `json:"subtotal"`
}

// Order คือ order ของ customer
type Order struct {
	ID         int         `json:"id"`
	CustomerID int         `json:"customer_id"`
	Lines      []OrderLine `json:"lines"`
	Total      float64     `json:"total"`
	Status     string      `json:"status"`
	CreatedAt  time.Time   `json:"created_at"`
}

// ===== SECTION 2: Workload Generator =====
//
// ทำไมต้องมี workload จำลอง?
// pprof จะแสดงผลที่มีความหมายก็ต่อเมื่อโปรแกรมกำลัง "ทำงานจริง"
// ถ้า server ว่างเปล่า CPU profile จะไม่มีข้อมูลอะไรให้ดู

// generateProducts สร้างสินค้าจำลองจำนวนมาก
// ฟังก์ชันนี้จงใจ allocate memory เยอะเพื่อให้เห็นชัดใน heap profile
func generateProducts(count int) []Product {
	categories := []string{"Electronics", "Clothing", "Food", "Books", "Sports"}
	products := make([]Product, count)

	for i := range products {
		// สร้าง tags หลายตัวเพื่อให้ใช้ memory มากขึ้น
		tags := make([]string, rand.Intn(5)+1)
		for j := range tags {
			tags[j] = fmt.Sprintf("tag-%d", rand.Intn(100))
		}

		products[i] = Product{
			ID:          i + 1,
			Name:        fmt.Sprintf("Product %d", i+1),
			Description: strings.Repeat("Lorem ipsum dolor sit amet. ", 5),
			Price:       float64(rand.Intn(10000)) / 100.0,
			Stock:       rand.Intn(1000),
			Category:    categories[rand.Intn(len(categories))],
			Tags:        tags,
			CreatedAt:   time.Now().Add(-time.Duration(rand.Intn(365*24)) * time.Hour),
			UpdatedAt:   time.Now(),
		}
	}
	return products
}

// processOrders จำลองการ process orders — CPU-intensive
// ฟังก์ชันนี้จงใจไม่ optimize เพื่อให้เห็นปัญหาใน CPU profile
func processOrders(products []Product, orderCount int) []Order {
	orders := make([]Order, orderCount)

	for i := range orders {
		lineCount := rand.Intn(5) + 1
		lines := make([]OrderLine, lineCount)
		total := 0.0

		for j := range lines {
			// จงใจเลือก product แบบ random ทุกครั้ง (ไม่ cache)
			// ใน production จริงควรทำ index lookup แทน
			p := products[rand.Intn(len(products))]
			qty := rand.Intn(10) + 1
			subtotal := p.Price * float64(qty)

			lines[j] = OrderLine{
				ProductID: p.ID,
				Quantity:  qty,
				UnitPrice: p.Price,
				Subtotal:  subtotal,
			}
			total += subtotal
		}

		orders[i] = Order{
			ID:         i + 1,
			CustomerID: rand.Intn(10000) + 1,
			Lines:      lines,
			Total:      total,
			Status:     "pending",
			CreatedAt:  time.Now(),
		}
	}
	return orders
}

// calculateRevenueByCategory รวม revenue ตาม category
// ฟังก์ชันนี้ใช้ map สร้างใหม่ทุกครั้ง (จงใจ allocate เยอะ)
func calculateRevenueByCategory(products []Product, orders []Order) map[string]float64 {
	// สร้าง product index — แต่ทำซ้ำทุก call (ไม่ cache)
	productMap := make(map[int]Product)
	for _, p := range products {
		productMap[p.ID] = p
	}

	revenue := make(map[string]float64)
	for _, order := range orders {
		for _, line := range order.Lines {
			if p, ok := productMap[line.ProductID]; ok {
				revenue[p.Category] += line.Subtotal
			}
		}
	}
	return revenue
}

// ===== SECTION 3: HTTP Handlers =====

// workloadHandler จำลองงานหนักเพื่อให้ profile เห็นชัด
// ทุกครั้งที่ GET /work จะ generate + process data
func workloadHandler(w http.ResponseWriter, r *http.Request) {
	// ใช้ trace task เพื่อให้เห็นใน execution trace
	// Task จะปรากฏเป็น block สีในหน้า trace viewer
	ctx, task := trace.NewTask(r.Context(), "workload-handler")
	defer task.End()

	// Region: generate
	trace.WithRegion(ctx, "generate-products", func() {
		_ = generateProducts(500)
	})

	var products []Product
	var orders []Order

	// Region: process
	trace.WithRegion(ctx, "process-orders", func() {
		products = generateProducts(200)
		orders = processOrders(products, 100)
	})

	// Region: aggregate
	var revenue map[string]float64
	trace.WithRegion(ctx, "calculate-revenue", func() {
		revenue = calculateRevenueByCategory(products, orders)
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(revenue)
}

// memoryLeakSimulator จำลอง memory leak แบบที่พบบ่อย
// เก็บ reference ไปยัง slice ที่โตเรื่อยๆ โดยไม่ตั้งใจ
// ดูใน heap profile: จะเห็น allocation ที่โตขึ้นเรื่อยๆ
var leakyCache [][]byte // จงใจ: global variable ที่โตเรื่อยๆ

func memoryPressureHandler(w http.ResponseWriter, r *http.Request) {
	// จงใจ: เพิ่ม memory ทุกครั้งที่ call โดยไม่ release
	// ใน production จริงอาจเกิดจาก cache ที่ไม่มี eviction policy
	chunk := make([]byte, 1*1024*1024) // 1 MB ต่อ request
	rand.Read(chunk)
	leakyCache = append(leakyCache, chunk)

	fmt.Fprintf(w, "Total cached: %d MB\n", len(leakyCache))
}

// goroutineLeakHandler จำลอง goroutine leak
// สร้าง goroutine ที่รอ channel ซึ่งไม่มีใครส่งข้อมูล
// ดูใน goroutine profile: จะเห็น goroutines ที่ blocked อยู่
var leakyGoroutines []chan struct{} // เก็บ reference เพื่อป้องกัน GC

func goroutineLeakHandler(w http.ResponseWriter, r *http.Request) {
	ch := make(chan struct{}) // channel ที่ไม่มีใครส่ง
	leakyGoroutines = append(leakyGoroutines, ch)

	// goroutine นี้จะ block อยู่ตลอดไป
	go func() {
		<-ch // รอตลอดกาล
	}()

	fmt.Fprintf(w, "Total leaked goroutines: %d\n", len(leakyGoroutines))
}

// ===== SECTION 4: CPU Profile Demo =====
//
// ทำไม CPU profiling ถึงสำคัญ?
// CPU profile ตอบคำถาม "โปรแกรมใช้เวลาอยู่ที่ไหน?"
// ทำให้รู้ว่าควร optimize ฟังก์ชันไหน ไม่ใช่ optimize แบบสุ่ม

// heavyCPUWork จำลองงานที่ใช้ CPU เยอะ
// เหมาะสำหรับ CPU profiling demo
func heavyCPUWork(n int) int {
	// จงใจ: ไม่ใช้ cache สำหรับ fibonacci (exponential time)
	// ใน production ควรใช้ dynamic programming แทน
	if n <= 1 {
		return n
	}
	return heavyCPUWork(n-1) + heavyCPUWork(n-2)
}

func cpuWorkHandler(w http.ResponseWriter, r *http.Request) {
	ctx, task := trace.NewTask(r.Context(), "cpu-work")
	defer task.End()

	var wg sync.WaitGroup
	results := make([]int, 10)

	// ใช้ goroutines ขนาน เพื่อให้เห็น concurrency ใน trace
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			trace.WithRegion(ctx, fmt.Sprintf("fib-%d", idx), func() {
				results[idx] = heavyCPUWork(30 + idx)
			})
		}(i)
	}
	wg.Wait()

	fmt.Fprintf(w, "Results computed: %v\n", results)
}

// ===== SECTION 5: File-based Profiling =====
//
// บางครั้งไม่ต้องการ HTTP server
// สามารถเขียน profile ลงไฟล์ได้โดยตรง
// เหมาะสำหรับ batch jobs หรือ tests

// writeFileTrace เขียน execution trace ลงไฟล์
// จะได้ไฟล์ trace.out ที่เปิดด้วย `go tool trace trace.out`
func writeFileTrace() {
	f, err := os.Create("/tmp/module4_trace.out")
	if err != nil {
		log.Printf("ไม่สามารถสร้างไฟล์ trace: %v", err)
		return
	}
	defer f.Close()

	// เริ่ม trace — record ทุก goroutine event
	if err := trace.Start(f); err != nil {
		log.Printf("ไม่สามารถเริ่ม trace: %v", err)
		return
	}

	// ทำงานที่ต้องการ trace
	ctx := context.Background()
	_, task := trace.NewTask(ctx, "file-trace-demo")

	trace.WithRegion(ctx, "file-trace-work", func() {
		products := generateProducts(100)
		orders := processOrders(products, 50)
		_ = calculateRevenueByCategory(products, orders)
	})

	task.End()
	trace.Stop()

	log.Println("Trace เขียนลงไฟล์ /tmp/module4_trace.out")
	log.Println("ดูผลด้วย: go tool trace /tmp/module4_trace.out")
}

// ===== SECTION 6: Runtime Stats =====
//
// runtime.ReadMemStats ให้ข้อมูล GC และ memory โดยตรง
// ใช้ใน metrics endpoint เพื่อ monitor production

// memStatsHandler แสดง runtime memory statistics
func memStatsHandler(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	// ReadMemStats หยุด "the world" ชั่วคราว — ควรเรียกแค่ตอน debug
	// ไม่ควรเรียกใน hot path
	runtime.ReadMemStats(&ms)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		// HeapAlloc: bytes ที่ allocate และยังใช้อยู่ (live objects)
		"heap_alloc_mb": float64(ms.HeapAlloc) / 1024 / 1024,

		// HeapSys: bytes ที่ขอจาก OS (รวม freed objects ที่ยังไม่คืน OS)
		"heap_sys_mb": float64(ms.HeapSys) / 1024 / 1024,

		// HeapObjects: จำนวน objects ที่ยังมีชีวิตอยู่
		"heap_objects": ms.HeapObjects,

		// NumGC: จำนวนครั้งที่ GC ทำงานแล้ว
		"num_gc": ms.NumGC,

		// PauseNs: ค่าล่าสุดของ GC pause time (nanoseconds)
		"last_gc_pause_ms": float64(ms.PauseNs[(ms.NumGC+255)%256]) / 1e6,

		// Mallocs vs Frees: จำนวน allocation ทั้งหมด
		"total_alloc_mb": float64(ms.TotalAlloc) / 1024 / 1024,

		// Goroutines: จำนวน goroutine ที่ live อยู่ตอนนี้
		"goroutines": runtime.NumGoroutine(),
	})
}

func main() {
	// เขียน trace file ตอนเริ่มต้น สำหรับ demo
	go writeFileTrace()

	// ===== Setup pprof server =====
	//
	// ข้อควรระวัง: ใน production ควรทำ 1 ใน 2 อย่าง:
	// 1. เปิด pprof เฉพาะ localhost (ไม่ expose ออก internet)
	// 2. ใช้ BasicAuth หรือ network policy คุ้มครอง
	//
	// เหตุผล: pprof endpoint เปิดเผย source code ชื่อฟังก์ชัน
	// และข้อมูล memory ซึ่งอาจเป็นช่องโหว่ด้าน security

	mux := http.NewServeMux()

	// ===== Application endpoints =====
	mux.HandleFunc("/work", workloadHandler)
	mux.HandleFunc("/cpu", cpuWorkHandler)
	mux.HandleFunc("/memleak", memoryPressureHandler)
	mux.HandleFunc("/goroutineleak", goroutineLeakHandler)
	mux.HandleFunc("/stats", memStatsHandler)

	// ===== pprof endpoints =====
	// ต้อง register บน mux ใหม่เพราะเราไม่ใช้ DefaultServeMux
	// DefaultServeMux จะถูก register โดย import "net/http/pprof"
	// แต่เราใช้ custom mux จึงต้อง forward ด้วยตัวเอง

	// วิธีที่ 1: ใช้ DefaultServeMux เป็น handler สำหรับ /debug/pprof/
	// ง่ายที่สุด แต่ต้องแน่ใจว่า DefaultServeMux ไม่มี handler อื่น
	mux.Handle("/debug/pprof/", http.DefaultServeMux)

	// วิธีที่ 2 (alternative): Register แต่ละ handler เอง
	// import "net/http/pprof" แล้วทำ:
	//   mux.HandleFunc("/debug/pprof/", pprof.Index)
	//   mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	//   mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	//   mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	//   mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	//   mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	//   mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	//   mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	//   mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	//   mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))

	// ===== Enable mutex and block profiling =====
	//
	// ค่า default คือ 0 (disabled) เพราะมี overhead
	// เปิดเฉพาะตอน debug เท่านั้น
	//
	// SetMutexProfileFraction: 1 = record ทุก mutex event
	// (ใน production ใช้ 10-100 แทน เพื่อลด overhead)
	runtime.SetMutexProfileFraction(1)

	// SetBlockProfileRate: 1 = record ทุก blocking event (nanoseconds)
	// (ใน production ใช้ค่าสูงกว่า เช่น 1000000 = 1ms)
	runtime.SetBlockProfileRate(1)

	server := &http.Server{
		Addr:    ":6060",
		Handler: mux,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Println("=== Module 4: Profiling Demo ===")
		log.Println("Server กำลังรันที่ http://localhost:6060")
		log.Println("")
		log.Println("Application Endpoints:")
		log.Println("  GET /work         — workload จำลอง (JSON response)")
		log.Println("  GET /cpu          — CPU-intensive work (fibonacci)")
		log.Println("  GET /memleak      — จำลอง memory leak (เรียกซ้ำๆ)")
		log.Println("  GET /goroutineleak — จำลอง goroutine leak (เรียกซ้ำๆ)")
		log.Println("  GET /stats        — runtime memory statistics")
		log.Println("")
		log.Println("pprof Endpoints:")
		log.Println("  GET /debug/pprof/              — index")
		log.Println("  GET /debug/pprof/heap          — heap memory (snapshot)")
		log.Println("  GET /debug/pprof/goroutine     — goroutines ที่ live อยู่")
		log.Println("  GET /debug/pprof/allocs        — allocation profile")
		log.Println("  GET /debug/pprof/block         — blocking events")
		log.Println("  GET /debug/pprof/mutex         — mutex contention")
		log.Println("  GET /debug/pprof/profile?seconds=30 — CPU profile")
		log.Println("  GET /debug/pprof/trace?seconds=5    — execution trace")
		log.Println("")
		log.Println("ตัวอย่างคำสั่ง profiling:")
		log.Println("  # เปิด pprof UI (CPU profile 30s)")
		log.Println("  go tool pprof -http=:8080 http://localhost:6060/debug/pprof/profile?seconds=30")
		log.Println("")
		log.Println("  # Heap profile")
		log.Println("  go tool pprof -http=:8080 http://localhost:6060/debug/pprof/heap")
		log.Println("")
		log.Println("  # Trace")
		log.Println("  curl -o /tmp/trace.out 'http://localhost:6060/debug/pprof/trace?seconds=5'")
		log.Println("  go tool trace /tmp/trace.out")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-quit
	log.Println("กำลัง shutdown server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Server หยุดทำงานแล้ว")
}
