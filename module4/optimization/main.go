// Module 4: High-Performance Go — Before/After Optimization Examples
//
// โปรแกรมนี้แสดง workflow การ optimize อย่างเป็นระบบ
// โดยใช้ data จริงและ profile-driven approach
//
// กระบวนการ optimize ที่ถูกต้อง:
//   1. Measure (วัดผล) — รู้ baseline ก่อน optimize
//   2. Profile (หาคอขวด) — ใช้ pprof ไม่ใช่ การเดา
//   3. Optimize (แก้ไข) — แก้จุดที่ profile ชี้ เท่านั้น
//   4. Verify (ตรวจสอบ) — วัดผลหลัง optimize และ test ว่า logic ยังถูก
//   5. Document (บันทึก) — อธิบายว่าทำไมถึง optimize แบบนี้
//
// วิธีรัน:
//   go run ./module4/optimization/

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ============================================================
// SECTION 1: JSON Processing Optimization
// ============================================================
//
// Use case: รับ JSON จาก external API แล้ว filter/transform
// ปัญหาที่พบบ่อย:
//   - Unmarshal ทั้งหมดก่อน แล้ว process → เปลืองหน่วยความจำ
//   - สร้าง intermediate slice เยอะ
//   - ไม่ reuse buffer

// Product entity สำหรับ JSON processing demo
type Product struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Price       float64  `json:"price"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	InStock     bool     `json:"in_stock"`
	Description string   `json:"description"` // field ใหญ่ที่เราไม่ค่อยใช้
}

// ProductSummary เก็บเฉพาะ field ที่ต้องการ
// การ define struct ย่อย ช่วยลด alloc เมื่อ unmarshal JSON ขนาดใหญ่
type ProductSummary struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Category string  `json:"category"`
	InStock  bool    `json:"in_stock"`
}

// ===== Version 1: Naive implementation =====

// processProductsNaive วิธีที่ไม่ optimize
// ปัญหา:
//   1. Unmarshal ทั้ง Product struct (รวม Description ที่ไม่ได้ใช้)
//   2. สร้าง filtered slice ชั่วคราว
//   3. Marshal ใหม่ทุกครั้ง
//   4. ไม่ reuse buffer
func processProductsNaive(data []byte, minPrice float64, category string) ([]byte, error) {
	// ALLOC 1: decode JSON ทั้งหมดเข้า []Product
	// ปัญหา: decode Description ด้วยทั้งที่ไม่ใช้
	var products []Product
	if err := json.Unmarshal(data, &products); err != nil {
		return nil, fmt.Errorf("unmarshal failed: %w", err)
	}

	// ALLOC 2: สร้าง slice ใหม่สำหรับ filtered results
	var filtered []Product
	for _, p := range products {
		if p.Price >= minPrice && (category == "" || p.Category == category) {
			filtered = append(filtered, p) // copy Product struct ทั้งหมด
		}
	}

	// ALLOC 3: marshal ผลลัพธ์
	return json.Marshal(filtered)
}

// ===== Version 2: Optimized implementation =====

// jsonEncoder pool สำหรับ reuse encoder
// หลีกเลี่ยงการสร้าง bytes.Buffer ใหม่ทุกครั้ง
var encoderBufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// processProductsOptimized วิธีที่ optimize แล้ว
// การปรับปรุง:
//   1. Decode เฉพาะ field ที่ต้องการ (ProductSummary)
//   2. เขียน output โดยตรง ไม่สร้าง intermediate slice
//   3. Reuse bytes.Buffer จาก pool
//   4. Streaming JSON encoding
func processProductsOptimized(data []byte, minPrice float64, category string) ([]byte, error) {
	// ALLOC 1 (ลดลง): decode เฉพาะ ProductSummary (ไม่มี Description)
	// ประหยัด memory ตามสัดส่วนของ Description size
	var summaries []ProductSummary
	if err := json.Unmarshal(data, &summaries); err != nil {
		return nil, fmt.Errorf("unmarshal failed: %w", err)
	}

	// ALLOC 2 (ลดลง): reuse buffer จาก pool
	buf := encoderBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer encoderBufferPool.Put(buf)

	// Streaming JSON: เขียน array โดยตรง ไม่สร้าง intermediate slice
	buf.WriteByte('[')
	enc := json.NewEncoder(buf)

	first := true
	for i := range summaries {
		p := &summaries[i] // ใช้ pointer แทน copy value
		if p.Price >= minPrice && (category == "" || p.Category == category) {
			if !first {
				buf.WriteByte(',')
			}
			if err := enc.Encode(p); err != nil {
				return nil, err
			}
			// ลบ trailing newline ที่ json.Encoder ใส่
			buf.Truncate(buf.Len() - 1)
			first = false
		}
	}
	buf.WriteByte(']')

	// Copy ออกจาก pool buffer
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

// generateProductJSON สร้าง JSON ทดสอบ
func generateProductJSON(count int) []byte {
	products := make([]Product, count)
	categories := []string{"Electronics", "Clothing", "Food", "Books"}

	for i := range products {
		products[i] = Product{
			ID:          i + 1,
			Name:        fmt.Sprintf("Product %d", i+1),
			Price:       float64(100+i*10) / 10.0,
			Category:    categories[i%len(categories)],
			Tags:        []string{"tag1", "tag2"},
			InStock:     i%3 != 0,
			Description: strings.Repeat("Long description text. ", 20), // field ใหญ่
		}
	}

	data, _ := json.Marshal(products)
	return data
}

// ============================================================
// SECTION 2: String Processing Optimization (Thai Text)
// ============================================================
//
// Use case: ระบบ CMS ที่ต้องประมวลผลข้อความภาษาไทย
// ปัญหา: Thai text ต้องใช้ rune operations ไม่ใช่ byte operations

// WordCount เก็บผลการนับคำ
type WordCount struct {
	Word  string
	Count int
}

// ===== Version 1: Naive Thai word counter =====

// countWordNaive วิธีที่ไม่ optimize
// ปัญหา:
//   1. แปลง string เป็น []rune ทั้งหมดก่อน (ALLOC)
//   2. สร้าง string ใหม่ทุกครั้งที่ extract word (ALLOC x N)
//   3. ไม่รองรับ Unicode normalization
func countWordsNaive(text string) map[string]int {
	counts := make(map[string]int)

	// ALLOC: แปลงทั้ง string เป็น []rune ก่อน
	// สำหรับข้อความยาวๆ = alloc ใหญ่
	runes := []rune(text)

	var word []rune // ALLOC ซ้ำในแต่ละ word
	for _, r := range runes {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if len(word) > 0 {
				// ALLOC: string(word) สร้าง string ใหม่
				counts[string(word)]++
				word = word[:0] // reset แต่ไม่ free capacity
			}
		} else {
			word = append(word, r)
		}
	}
	if len(word) > 0 {
		counts[string(word)]++
	}

	return counts
}

// ===== Version 2: Optimized Thai word counter =====

// countWordsOptimized วิธีที่ optimize แล้ว
// การปรับปรุง:
//   1. ใช้ range loop บน string โดยตรง (ไม่แปลงเป็น []rune)
//   2. ใช้ strings.Builder สำหรับ word accumulation
//   3. Reuse builder ระหว่าง words
func countWordsOptimized(text string) map[string]int {
	counts := make(map[string]int)
	var builder strings.Builder // reuse builder

	for _, r := range text { // range on string = UTF-8 decode ทีละ rune, ไม่ allocate
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if builder.Len() > 0 {
				counts[builder.String()]++ // สร้าง string เฉพาะตอนจะใส่ map
				builder.Reset()            // reset โดยไม่ free internal buffer
			}
		} else {
			builder.WriteRune(r)
		}
	}
	if builder.Len() > 0 {
		counts[builder.String()]++
	}

	return counts
}

// sampleThaiText ข้อความตัวอย่างสำหรับ Thai processing demo
var sampleThaiText = `
สวัสดีครับ ยินดีต้อนรับสู่คอร์ส Advanced Go Enterprise
ในบทเรียนนี้ เราจะเรียนรู้เกี่ยวกับ performance optimization
การทำ profiling และการเขียนโค้ดที่มีประสิทธิภาพสูง
สำหรับระบบ enterprise ที่ต้องรองรับ load จำนวนมาก
การเข้าใจ memory model และ garbage collector เป็นสิ่งสำคัญ
ที่จะช่วยให้นักพัฒนาสามารถเขียนโปรแกรมที่ดีขึ้นได้
`

// ============================================================
// SECTION 3: HTTP Handler with sync.Pool
// ============================================================
//
// Use case: HTTP handler ที่ต้อง process JSON request body
// โดย reuse request struct เพื่อลด GC pressure
//
// ปัญหาใน production:
//   - ทุก HTTP request = allocate struct ใหม่
//   - สำหรับ 10,000 req/sec = 10,000 alloc/sec
//   - GC ต้องทำงานบ่อย = latency spikes

// ProcessRequest struct สำหรับประมวลผล request
type ProcessRequest struct {
	UserID    int                    `json:"user_id"`
	Action    string                 `json:"action"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
	Tags      []string               `json:"tags"`
}

// ProcessResponse struct สำหรับ response
type ProcessResponse struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"request_id"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Duration  string      `json:"duration"`
}

// reset ล้าง ProcessRequest สำหรับ reuse
// สำคัญมาก: ต้อง reset ทุก field เพื่อป้องกัน data leak ระหว่าง requests
func (r *ProcessRequest) reset() {
	r.UserID = 0
	r.Action = ""
	r.Timestamp = time.Time{}
	r.Tags = r.Tags[:0]
	// ล้าง map โดยไม่ allocate ใหม่
	for k := range r.Payload {
		delete(r.Payload, k)
	}
}

// requestPool pool ของ ProcessRequest structs
var requestPool = sync.Pool{
	New: func() interface{} {
		return &ProcessRequest{
			Payload: make(map[string]interface{}),
			Tags:    make([]string, 0, 8),
		}
	},
}

// responseBufferPool pool ของ response buffers
var responseBufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// processHandler HTTP handler ที่ใช้ sync.Pool
// ลด allocation ต่อ request อย่างมีนัยสำคัญ
func processHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// ดึง request struct จาก pool
	req := requestPool.Get().(*ProcessRequest)
	defer func() {
		req.reset() // ต้อง reset ก่อน return กลับ pool เสมอ
		requestPool.Put(req)
	}()

	// Decode JSON body เข้า pooled struct
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// ดึง response buffer จาก pool
	buf := responseBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		responseBufferPool.Put(buf)
	}()

	// Process request (ตรรกะจำลอง)
	resp := ProcessResponse{
		Success:   true,
		RequestID: fmt.Sprintf("req-%d-%d", req.UserID, time.Now().UnixNano()),
		Result: map[string]interface{}{
			"user_id":     req.UserID,
			"action":      req.Action,
			"tags_count":  len(req.Tags),
			"payload_len": len(req.Payload),
		},
		Duration: time.Since(start).String(),
	}

	// Encode response โดยตรงลง buffer จาก pool
	enc := json.NewEncoder(buf)
	if err := enc.Encode(resp); err != nil {
		http.Error(w, "encoding failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

// processHandlerNaive HTTP handler แบบ naive (สำหรับเปรียบเทียบ)
// สร้าง struct และ buffer ใหม่ทุก request
func processHandlerNaive(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// ALLOC ทุก request: สร้าง struct ใหม่
	req := &ProcessRequest{
		Payload: make(map[string]interface{}),
	}

	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	resp := ProcessResponse{
		Success:   true,
		RequestID: fmt.Sprintf("req-%d-%d", req.UserID, time.Now().UnixNano()),
		Result: map[string]interface{}{
			"user_id":     req.UserID,
			"action":      req.Action,
			"tags_count":  len(req.Tags),
			"payload_len": len(req.Payload),
		},
		Duration: time.Since(start).String(),
	}

	// ALLOC ทุก request: json.Marshal สร้าง []byte ใหม่
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "encoding failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ============================================================
// SECTION 4: Profile-Driven Optimization Workflow
// ============================================================
//
// ขั้นตอนที่ถูกต้องในการ optimize:
//
// Step 1: Establish baseline
//   go test -bench=. -benchmem -count=5 ./... > baseline.txt
//
// Step 2: Identify hotspot ด้วย CPU profile
//   go test -bench=BenchmarkYourFunc -cpuprofile=cpu.out ./...
//   go tool pprof -http=:8080 cpu.out
//   → ดู flame graph: top ของ flame = ใช้เวลามากสุด
//
// Step 3: Identify memory hotspot ด้วย heap profile
//   go test -bench=BenchmarkYourFunc -memprofile=mem.out ./...
//   go tool pprof -http=:8080 mem.out
//   → ดู alloc_space: ฟังก์ชันไหน allocate เยอะสุด
//
// Step 4: Fix the actual bottleneck
//   → อย่า optimize สิ่งที่ profile ไม่ได้ชี้!
//
// Step 5: Verify improvement
//   go test -bench=. -benchmem -count=5 ./... > after.txt
//   benchstat baseline.txt after.txt
//
// Step 6: Run regression tests
//   go test ./...

// demonstrateOptimizationFlow แสดง workflow การ optimize แบบครบวงจร
func demonstrateOptimizationFlow() {
	fmt.Println("=== Profile-Driven Optimization Workflow Demo ===")
	fmt.Println()

	// Step 1: สร้าง test data
	fmt.Println("Step 1: Generating test data...")
	jsonData := generateProductJSON(1000)
	fmt.Printf("  Generated %d bytes of JSON data\n", len(jsonData))
	fmt.Println()

	// Step 2: วัด naive approach
	fmt.Println("Step 2: Measuring NAIVE approach...")
	const iterations = 100
	naiveStart := time.Now()
	var naiveResult []byte
	for i := 0; i < iterations; i++ {
		var err error
		naiveResult, err = processProductsNaive(jsonData, 500.0, "Electronics")
		if err != nil {
			log.Fatalf("naive failed: %v", err)
		}
	}
	naiveDuration := time.Since(naiveStart)
	fmt.Printf("  Naive: %d iterations in %v\n", iterations, naiveDuration)
	fmt.Printf("  Naive avg: %v/op\n", naiveDuration/iterations)
	fmt.Printf("  Naive output: %d bytes\n", len(naiveResult))
	fmt.Println()

	// Step 3: วัด optimized approach
	fmt.Println("Step 3: Measuring OPTIMIZED approach...")
	optStart := time.Now()
	var optResult []byte
	for i := 0; i < iterations; i++ {
		var err error
		optResult, err = processProductsOptimized(jsonData, 500.0, "Electronics")
		if err != nil {
			log.Fatalf("optimized failed: %v", err)
		}
	}
	optDuration := time.Since(optStart)
	fmt.Printf("  Optimized: %d iterations in %v\n", iterations, optDuration)
	fmt.Printf("  Optimized avg: %v/op\n", optDuration/iterations)
	fmt.Printf("  Optimized output: %d bytes\n", len(optResult))
	fmt.Println()

	// Step 4: แสดงผลการเปรียบเทียบ
	fmt.Println("Step 4: Comparison Results")
	speedup := float64(naiveDuration) / float64(optDuration)
	fmt.Printf("  Speedup: %.2fx faster\n", speedup)
	fmt.Println()
	fmt.Println("  NOTE: ตัวเลขนี้เป็นแค่ wall time ไม่ใช่ benchmark จริง")
	fmt.Println("  ใช้ go test -bench= -benchmem สำหรับ accurate results")
	fmt.Println()

	// Step 5: แสดง Thai text processing
	fmt.Println("Step 5: Thai Text Processing Demo")
	words := countWordsOptimized(sampleThaiText)
	fmt.Printf("  Unique words in sample text: %d\n", len(words))
	fmt.Println("  Top words:")
	// แสดงแค่ 5 คำแรก
	count := 0
	for word, freq := range words {
		if count >= 5 {
			break
		}
		fmt.Printf("    '%s': %d times\n", word, freq)
		count++
	}
	fmt.Println()
}

// ============================================================
// SECTION 5: HTTP Server with Pool Demo
// ============================================================

func setupHTTPServer() {
	mux := http.NewServeMux()

	// Naive handler (สำหรับเปรียบเทียบ)
	mux.HandleFunc("/process/naive", processHandlerNaive)

	// Optimized handler ที่ใช้ pool
	mux.HandleFunc("/process/optimized", processHandler)

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	fmt.Println("=== HTTP Server Demo (sync.Pool) ===")
	fmt.Println("Server กำลังรันที่ http://localhost:6061")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  POST /process/naive      — handler แบบ naive (สร้าง struct ใหม่ทุก request)")
	fmt.Println("  POST /process/optimized  — handler แบบ optimized (ใช้ sync.Pool)")
	fmt.Println("  GET  /health             — health check")
	fmt.Println()
	fmt.Println("ทดสอบด้วย:")
	fmt.Println(`  curl -X POST http://localhost:6061/process/optimized \`)
	fmt.Println(`    -H "Content-Type: application/json" \`)
	fmt.Println(`    -d '{"user_id": 1, "action": "buy", "tags": ["vip"], "payload": {"item": "go-book"}}'`)
	fmt.Println()
	fmt.Println("เปรียบเทียบ load test:")
	fmt.Println("  hey -n 10000 -c 100 -m POST -H 'Content-Type: application/json' \\")
	fmt.Println("    -d '{\"user_id\":1,\"action\":\"buy\"}' http://localhost:6061/process/naive")
	fmt.Println("  hey -n 10000 -c 100 -m POST -H 'Content-Type: application/json' \\")
	fmt.Println("    -d '{\"user_id\":1,\"action\":\"buy\"}' http://localhost:6061/process/optimized")
	fmt.Println()

	if err := http.ListenAndServe(":6061", mux); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}

func main() {
	// แสดง optimization workflow demo
	demonstrateOptimizationFlow()

	fmt.Println("=== สรุป Optimization Techniques ===")
	fmt.Println()
	fmt.Println("1. JSON Processing:")
	fmt.Println("   - ใช้ struct เฉพาะ field ที่ต้องการ (ไม่ decode ทั้งหมด)")
	fmt.Println("   - Reuse bytes.Buffer จาก sync.Pool")
	fmt.Println("   - Streaming encode แทน marshal ทั้งหมดแล้วค่อย write")
	fmt.Println()
	fmt.Println("2. Thai String Processing:")
	fmt.Println("   - ใช้ range บน string โดยตรง (ไม่แปลง []rune ก่อน)")
	fmt.Println("   - Reuse strings.Builder ระหว่าง words")
	fmt.Println("   - หลีกเลี่ยง string conversion ที่ไม่จำเป็น")
	fmt.Println()
	fmt.Println("3. HTTP Handler Pool:")
	fmt.Println("   - Reuse request/response structs ด้วย sync.Pool")
	fmt.Println("   - Reset ทุก field ก่อน return กลับ pool")
	fmt.Println("   - ลด GC pressure ลงอย่างมีนัยสำคัญ")
	fmt.Println()
	fmt.Println("4. หลักการสำคัญ:")
	fmt.Println("   - MEASURE before optimize")
	fmt.Println("   - PROFILE to find bottleneck")
	fmt.Println("   - VERIFY improvement ด้วย benchmark")
	fmt.Println("   - อย่า over-optimize code ที่ไม่ใช่ hot path")
	fmt.Println()

	// เริ่ม HTTP server
	go setupHTTPServer()

	// รอให้ server พร้อม แล้วบอก user ว่าสามารถทดสอบได้
	time.Sleep(100 * time.Millisecond)
	fmt.Println("กด Ctrl+C เพื่อหยุด หรือทดสอบ HTTP endpoints ด้านบน")

	// Block ไม่ให้ program exit
	select {}
}
