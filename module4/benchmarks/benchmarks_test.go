// Module 4: High-Performance Go — Benchmark Comparisons
//
// รันทั้งหมด:
//   go test -bench=. -benchmem -benchtime=3s ./module4/benchmarks/
//
// รัน benchmark เฉพาะกลุ่ม:
//   go test -bench=BenchmarkString -benchmem ./module4/benchmarks/
//
// ดู memory profile จาก benchmark:
//   go test -bench=BenchmarkString -benchmem -memprofile=mem.out ./module4/benchmarks/
//   go tool pprof -http=:8080 mem.out
//
// ดู CPU profile จาก benchmark:
//   go test -bench=BenchmarkMap -cpuprofile=cpu.out ./module4/benchmarks/
//   go tool pprof -http=:8080 cpu.out

package benchmarks

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// ============================================================
// SECTION 1: String Concatenation
// ============================================================
//
// ทำไมเรื่อง string ถึงสำคัญในระบบ Enterprise?
// ระบบ enterprise มักสร้าง string เยอะมาก:
//   - SQL query building
//   - JSON/XML serialization
//   - Log message formatting
//   - HTTP response building
//
// ใน Go, string เป็น immutable
// ดังนั้น += ทุกครั้ง = allocate string ใหม่ = GC pressure

const sampleWords = 100 // จำนวน words ที่จะ concatenate

// BenchmarkStringPlusEquals วัดวิธีที่ BAD ที่สุด
// ทุกครั้งที่ += จะ allocate string ใหม่และ copy ทั้งหมด
// O(n²) time complexity, O(n²) allocations
//
// ผลที่คาดหวัง: ช้าที่สุด, allocs/op มากที่สุด
// ตัวเลขตัวอย่าง: ~5000 ns/op, ~100 allocs/op (100 words)
func BenchmarkStringPlusEquals(b *testing.B) {
	b.ReportAllocs() // บอกให้ benchmark รายงาน allocation ด้วย

	words := makeWords(sampleWords)

	b.ResetTimer() // reset timer หลัง setup เพื่อไม่นับเวลา setup
	for i := 0; i < b.N; i++ {
		result := ""
		for _, w := range words {
			result += w + " " // แต่ละ iteration = allocate string ใหม่
		}
		_ = result
	}
}

// BenchmarkStringsBuilder วัดวิธีที่ RECOMMENDED สำหรับ concatenation
// strings.Builder ใช้ internal []byte buffer
// รองรับ WriteString โดยตรง ไม่ต้องแปลง type
//
// ผลที่คาดหวัง: เร็วกว่า += มาก, allocs/op น้อย
// ตัวเลขตัวอย่าง: ~2000 ns/op, ~1-2 allocs/op (100 words)
func BenchmarkStringsBuilder(b *testing.B) {
	b.ReportAllocs()

	words := makeWords(sampleWords)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sb strings.Builder
		// Grow ล่วงหน้า ถ้ารู้ขนาดโดยประมาณ = ลด re-allocation
		sb.Grow(sampleWords * 10)
		for _, w := range words {
			sb.WriteString(w)
			sb.WriteByte(' ')
		}
		_ = sb.String()
	}
}

// BenchmarkStringsBuilderNoGrow เปรียบเทียบ Builder โดยไม่ pre-grow
// เพื่อแสดงผลของ Grow()
//
// ผลที่คาดหวัง: ช้ากว่า Builder+Grow เล็กน้อย, allocs/op มากกว่าเพราะ re-alloc
func BenchmarkStringsBuilderNoGrow(b *testing.B) {
	b.ReportAllocs()

	words := makeWords(sampleWords)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sb strings.Builder
		// ไม่มี Grow — buffer จะขยายแบบ exponential (1.25x ทุกครั้ง)
		for _, w := range words {
			sb.WriteString(w)
			sb.WriteByte(' ')
		}
		_ = sb.String()
	}
}

// BenchmarkBytesBuffer วัด bytes.Buffer
// คล้าย strings.Builder แต่ implement io.Reader และ io.Writer ด้วย
// ใช้เมื่อต้องการ io.Writer interface (เช่น template.Execute)
//
// ผลที่คาดหวัง: ใกล้เคียง strings.Builder, อาจช้ากว่าเล็กน้อย
// ตัวเลขตัวอย่าง: ~2200 ns/op, ~1-2 allocs/op
func BenchmarkBytesBuffer(b *testing.B) {
	b.ReportAllocs()

	words := makeWords(sampleWords)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		buf.Grow(sampleWords * 10)
		for _, w := range words {
			buf.WriteString(w)
			buf.WriteByte(' ')
		}
		_ = buf.String()
	}
}

// BenchmarkFmtSprintf วัด fmt.Sprintf
// Sprintf ใช้ reflection + format parsing = overhead สูง
// เหมาะสำหรับ format string ที่ซับซ้อน ไม่เหมาะสำหรับ concatenation เดียว
//
// ผลที่คาดหวัง: ช้ากว่า Builder มาก เพราะ reflection overhead
// ตัวเลขตัวอย่าง: ~10000 ns/op, ~200 allocs/op (100 words)
func BenchmarkFmtSprintf(b *testing.B) {
	b.ReportAllocs()

	words := makeWords(sampleWords)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := ""
		for _, w := range words {
			result = fmt.Sprintf("%s%s ", result, w)
		}
		_ = result
	}
}

// BenchmarkStringsJoin วัด strings.Join ซึ่งเป็น single-pass
// ใช้เมื่อมี []string พร้อมแล้วและต้องการ join ครั้งเดียว
// ไม่เหมาะสำหรับกรณีที่ต้อง build ทีละ string
//
// ผลที่คาดหวัง: เร็วที่สุดในกลุ่มเพราะ single allocation
// ตัวเลขตัวอย่าง: ~800 ns/op, ~1 allocs/op
func BenchmarkStringsJoin(b *testing.B) {
	b.ReportAllocs()

	words := makeWords(sampleWords)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = strings.Join(words, " ")
	}
}

// ============================================================
// SECTION 2: Map Operations — map+mutex vs sync.Map
// ============================================================
//
// เมื่อไรควรใช้อะไร?
//   sync.Map:    ดีสำหรับ read-heavy workloads, key set ไม่ค่อยเปลี่ยน
//   map+Mutex:   ดีสำหรับ write-heavy workloads หรือ key set เปลี่ยนบ่อย
//
// sync.Map ใช้ "copy-on-write" technique:
//   - Read: lock-free โดยใช้ atomic operations
//   - Write: ต้อง lock และ copy internal map
//
// ข้อควรระวัง: ไม่มี silver bullet — ต้อง benchmark ตาม use case จริง

const mapSize = 1000
const readRatio = 0.9 // 90% read, 10% write

// mapWithMutex ห่อ map ด้วย RWMutex
// RWMutex ดีกว่า Mutex สำหรับ read-heavy เพราะ multiple reader ได้พร้อมกัน
type mapWithMutex struct {
	mu sync.RWMutex
	m  map[string]int
}

// BenchmarkMapMutexReadHeavy วัด map+RWMutex สำหรับ workload ที่ read เยอะ
//
// ผลที่คาดหวัง: ช้ากว่า sync.Map เล็กน้อยสำหรับ read-heavy
// เหตุผล: RLock() ยังมี atomic counter ซึ่งเป็น cache contention
func BenchmarkMapMutexReadHeavy(b *testing.B) {
	b.ReportAllocs()

	m := &mapWithMutex{m: make(map[string]int)}
	keys := make([]string, mapSize)
	for i := 0; i < mapSize; i++ {
		k := fmt.Sprintf("key-%d", i)
		keys[i] = k
		m.m[k] = i
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%mapSize]
			if float64(i%10)/10.0 < readRatio {
				// Read operation
				m.mu.RLock()
				_ = m.m[k]
				m.mu.RUnlock()
			} else {
				// Write operation
				m.mu.Lock()
				m.m[k] = i
				m.mu.Unlock()
			}
			i++
		}
	})
}

// BenchmarkSyncMapReadHeavy วัด sync.Map สำหรับ workload ที่ read เยอะ
//
// ผลที่คาดหวัง: เร็วกว่า map+mutex สำหรับ read-heavy workload
// เหตุผล: Read ใช้ atomic load ไม่ต้องรอ lock
func BenchmarkSyncMapReadHeavy(b *testing.B) {
	b.ReportAllocs()

	var sm sync.Map
	keys := make([]string, mapSize)
	for i := 0; i < mapSize; i++ {
		k := fmt.Sprintf("key-%d", i)
		keys[i] = k
		sm.Store(k, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%mapSize]
			if float64(i%10)/10.0 < readRatio {
				sm.Load(k)
			} else {
				sm.Store(k, i)
			}
			i++
		}
	})
}

// BenchmarkMapMutexWriteHeavy วัด map+Mutex สำหรับ write-heavy workload
//
// ผลที่คาดหวัง: เร็วกว่า sync.Map สำหรับ write-heavy
// เหตุผล: sync.Map ต้อง copy internal dirty map เมื่อ write
func BenchmarkMapMutexWriteHeavy(b *testing.B) {
	b.ReportAllocs()

	m := &mapWithMutex{m: make(map[string]int)}
	keys := make([]string, mapSize)
	for i := 0; i < mapSize; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%mapSize]
			if i%10 < 2 { // 20% read
				m.mu.RLock()
				_ = m.m[k]
				m.mu.RUnlock()
			} else { // 80% write
				m.mu.Lock()
				m.m[k] = i
				m.mu.Unlock()
			}
			i++
		}
	})
}

// BenchmarkSyncMapWriteHeavy วัด sync.Map สำหรับ write-heavy workload
//
// ผลที่คาดหวัง: ช้ากว่า map+mutex สำหรับ write-heavy
// เหตุผล: sync.Map dirty promotion มี overhead สูงเมื่อ write บ่อย
func BenchmarkSyncMapWriteHeavy(b *testing.B) {
	b.ReportAllocs()

	var sm sync.Map
	keys := make([]string, mapSize)
	for i := 0; i < mapSize; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%mapSize]
			if i%10 < 2 { // 20% read
				sm.Load(k)
			} else { // 80% write
				sm.Store(k, i)
			}
			i++
		}
	})
}

// ============================================================
// SECTION 3: Interface Boxing Cost
// ============================================================
//
// ทำไม interface call ถึงช้ากว่า direct call?
// 1. Interface value ใน Go มี 2 word: (type pointer, data pointer)
// 2. Method call ต้องผ่าน virtual dispatch table (itab)
// 3. ถ้า value ไม่ fit ใน pointer size → allocate บน heap ("boxing")
//
// ผลกระทบในระบบ enterprise:
//   - Middleware chain ที่ wrap interface หลายชั้น
//   - Plugin systems ที่ใช้ interface
//   - Error wrapping ที่ใช้ error interface

// Adder interface สำหรับทดสอบ interface dispatch
type Adder interface {
	Add(a, b int) int
}

// ConcreteAdder implement Adder โดยตรง
type ConcreteAdder struct{}

func (c ConcreteAdder) Add(a, b int) int {
	return a + b
}

// BenchmarkConcreteCall วัดการเรียกฟังก์ชัน concrete type โดยตรง
// ไม่มี vtable lookup, compiler อาจ inline ได้
//
// ผลที่คาดหวัง: เร็วที่สุด, 0 allocs/op
// ตัวเลขตัวอย่าง: ~0.3 ns/op
func BenchmarkConcreteCall(b *testing.B) {
	b.ReportAllocs()

	adder := ConcreteAdder{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = adder.Add(i, i+1)
	}
}

// BenchmarkInterfaceCall วัดการเรียกผ่าน interface
// มี vtable lookup overhead
//
// ผลที่คาดหวัง: ช้ากว่า concrete เล็กน้อย, 0 allocs/op (ถ้า struct ไม่ใหญ่)
// ตัวเลขตัวอย่าง: ~1-2 ns/op
func BenchmarkInterfaceCall(b *testing.B) {
	b.ReportAllocs()

	var adder Adder = ConcreteAdder{} // boxing: ใส่ concrete value ลงใน interface
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = adder.Add(i, i+1)
	}
}

// LargeStruct struct ใหญ่ที่ต้อง allocate บน heap เมื่อใส่ใน interface
type LargeStruct struct {
	data [64]int64 // 512 bytes — ใหญ่กว่า pointer (8 bytes)
}

// LargeAdder implement Adder ด้วย LargeStruct
type LargeAdder struct {
	s LargeStruct
}

func (l *LargeAdder) Add(a, b int) int {
	return a + b + len(l.s.data)
}

// BenchmarkInterfaceBoxing วัดต้นทุนของ heap allocation เมื่อ box ค่าใหญ่
//
// ผลที่คาดหวัง: มี allocs/op เพราะต้อง escape to heap
// ตัวเลขตัวอย่าง: ~5 ns/op, 1 alloc/op
func BenchmarkInterfaceBoxing(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// สร้าง *LargeAdder ใหม่ทุกครั้ง แล้ว assign ให้ interface
		var adder Adder = &LargeAdder{}
		_ = adder.Add(i, i+1)
	}
}

// ============================================================
// SECTION 4: Struct Field Ordering — Cache Line Alignment
// ============================================================
//
// ทำไม field ordering ถึงสำคัญ?
//   - CPU cache line = 64 bytes
//   - ถ้า struct ใหญ่กว่า 64 bytes แต่ field ที่ใช้บ่อยอยู่ห่างกัน
//     → ต้อง load หลาย cache line → cache miss เพิ่มขึ้น
//   - Go compiler ไม่ reorder fields (ต่างจาก C++ struct optimization)
//
// เครื่องมือตรวจสอบ:
//   go vet ./...  (ตรวจ atomic alignment บน 32-bit)
//   fieldalignment (golang.org/x/tools/go/analysis/passes/fieldalignment)

// BadAlignedStruct field ordering ที่ไม่ดี — มี padding เยอะ
// bool (1 byte) + padding (7 bytes) + int64 (8 bytes) + bool (1 byte) + ...
// ขนาดรวม: ใหญ่กว่าที่จำเป็น
type BadAlignedStruct struct {
	Active    bool    // 1 byte + 7 padding
	Balance   float64 // 8 bytes
	Deleted   bool    // 1 byte + 7 padding
	UserID    int64   // 8 bytes
	Score     float32 // 4 bytes + 4 padding
	Name      string  // 16 bytes (ptr + len)
	CreatedAt int64   // 8 bytes
}

// GoodAlignedStruct field ordering ที่ดี — เรียงจากใหญ่ไปเล็ก
// ลด padding มาก ทำให้ struct เล็กลง = fit ใน cache line มากขึ้น
type GoodAlignedStruct struct {
	Name      string  // 16 bytes (ptr + len)
	Balance   float64 // 8 bytes
	UserID    int64   // 8 bytes
	CreatedAt int64   // 8 bytes
	Score     float32 // 4 bytes
	Active    bool    // 1 byte
	Deleted   bool    // 1 byte + 2 padding
}

// BenchmarkBadAlignment วัด access pattern บน struct ที่มี padding เยอะ
//
// ผลที่คาดหวัง: ช้ากว่า GoodAlignment เพราะ struct ใหญ่กว่า
// ผลจะเห็นชัดขึ้นเมื่อ iterate slice ขนาดใหญ่
func BenchmarkBadAlignment(b *testing.B) {
	b.ReportAllocs()

	const n = 10000
	items := make([]BadAlignedStruct, n)
	for i := range items {
		items[i] = BadAlignedStruct{
			Active:    true,
			Balance:   float64(i) * 1.5,
			UserID:    int64(i),
			Name:      fmt.Sprintf("user-%d", i),
			CreatedAt: int64(i),
			Score:     float32(i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sum := 0.0
		for j := range items {
			if items[j].Active && !items[j].Deleted {
				sum += items[j].Balance
			}
		}
		_ = sum
	}
}

// BenchmarkGoodAlignment วัด access pattern บน struct ที่ optimize แล้ว
//
// ผลที่คาดหวัง: เร็วกว่า BadAlignment ~10-20%
// เพราะ struct เล็กกว่า → fit ใน cache line มากขึ้น → cache miss น้อยลง
func BenchmarkGoodAlignment(b *testing.B) {
	b.ReportAllocs()

	const n = 10000
	items := make([]GoodAlignedStruct, n)
	for i := range items {
		items[i] = GoodAlignedStruct{
			Active:    true,
			Balance:   float64(i) * 1.5,
			UserID:    int64(i),
			Name:      fmt.Sprintf("user-%d", i),
			CreatedAt: int64(i),
			Score:     float32(i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sum := 0.0
		for j := range items {
			if items[j].Active && !items[j].Deleted {
				sum += items[j].Balance
			}
		}
		_ = sum
	}
}

// ============================================================
// SECTION 5: sync.Pool vs Fresh Allocation
// ============================================================
//
// sync.Pool คืออะไร?
//   Pool ของ objects ที่ reuse ได้ ลด GC pressure
//   GC อาจเคลียร์ pool ทุก cycle — ไม่ใช่ cache ถาวร
//
// ใช้เมื่อไร?
//   - Objects ที่ allocate และ free บ่อย (request structs, buffers)
//   - ต้นทุน allocation สูง (ตรวจสอบจาก benchmark allocs/op)
//
// ไม่ควรใช้เมื่อ:
//   - Objects มีอายุยาวนาน (เก็บไว้นาน = pool ไม่คุ้ม)
//   - Objects มีสถานะซับซ้อนที่ต้อง reset อย่างระมัดระวัง

// RequestBuffer จำลอง buffer ที่ใช้สำหรับ process HTTP request
type RequestBuffer struct {
	Headers map[string]string
	Body    []byte
	Query   map[string][]string
	scratch [512]byte // fixed-size scratch space
}

// reset ล้างค่าใน RequestBuffer เพื่อ reuse
// สำคัญ: ต้อง reset ทุก field ก่อน return จาก pool
func (r *RequestBuffer) reset() {
	for k := range r.Headers {
		delete(r.Headers, k)
	}
	for k := range r.Query {
		delete(r.Query, k)
	}
	r.Body = r.Body[:0] // reset slice length แต่ keep capacity
}

// bufferPool pool ของ RequestBuffer
var bufferPool = sync.Pool{
	New: func() interface{} {
		// New เรียกเมื่อ pool ว่าง — allocate object ใหม่
		return &RequestBuffer{
			Headers: make(map[string]string),
			Body:    make([]byte, 0, 4096),
			Query:   make(map[string][]string),
		}
	},
}

// BenchmarkAllocateNew วัดการสร้าง RequestBuffer ใหม่ทุกครั้ง
//
// ผลที่คาดหวัง: มี allocs/op มาก เพราะสร้าง map และ slice ใหม่ทุกครั้ง
// ตัวเลขตัวอย่าง: ~300 ns/op, 3-4 allocs/op
func BenchmarkAllocateNew(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := &RequestBuffer{
			Headers: make(map[string]string),
			Body:    make([]byte, 0, 4096),
			Query:   make(map[string][]string),
		}
		// จำลองการใช้งาน
		buf.Headers["Content-Type"] = "application/json"
		buf.Body = append(buf.Body, []byte(`{"key":"value"}`)...)
		_ = buf
	}
}

// BenchmarkSyncPool วัดการใช้ sync.Pool
//
// ผลที่คาดหวัง: เร็วกว่า AllocateNew มาก, allocs/op ใกล้ 0
// ตัวเลขตัวอย่าง: ~50 ns/op, 0-1 allocs/op (pool warm)
func BenchmarkSyncPool(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := bufferPool.Get().(*RequestBuffer)
		// จำลองการใช้งาน
		buf.Headers["Content-Type"] = "application/json"
		buf.Body = append(buf.Body, []byte(`{"key":"value"}`)...)
		// ต้อง reset ก่อน Put กลับ!
		buf.reset()
		bufferPool.Put(buf)
	}
}

// ============================================================
// SECTION 6: Slice Pre-allocation
// ============================================================
//
// ปัญหาของ append โดยไม่ pre-allocate:
//   - Go double capacity เมื่อ slice เต็ม (1→2→4→8→16...)
//   - ทุกครั้งที่ double = copy ทั้ง slice ไปที่ memory ใหม่
//   - สำหรับ 1000 elements = ~10 re-allocations
//
// ใช้ make([]T, 0, n) เมื่อรู้ขนาดล่วงหน้า
// ใช้ make([]T, n) เมื่อต้องการ default values ทันที

const sliceSize = 10000

// BenchmarkSliceAppendOnly วัดการ append โดยไม่ pre-allocate
//
// ผลที่คาดหวัง: มี allocs/op หลายครั้งจากการ grow
// ตัวเลขตัวอย่าง: ~60000 ns/op, 24 allocs/op (10000 items)
func BenchmarkSliceAppendOnly(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := []int{}
		for j := 0; j < sliceSize; j++ {
			s = append(s, j*2)
		}
		_ = s
	}
}

// BenchmarkSlicePreAllocate วัดการ pre-allocate ด้วย make
//
// ผลที่คาดหวัง: 1 allocs/op เสมอ ไม่มี re-allocation
// ตัวเลขตัวอย่าง: ~30000 ns/op, 1 alloc/op
func BenchmarkSlicePreAllocate(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := make([]int, 0, sliceSize) // pre-allocate capacity
		for j := 0; j < sliceSize; j++ {
			s = append(s, j*2)
		}
		_ = s
	}
}

// BenchmarkSliceIndexAssign วัดการ assign โดยตรงด้วย index
// เร็วที่สุดเพราะไม่ต้องตรวจสอบ capacity
//
// ผลที่คาดหวัง: เร็วที่สุด, 1 allocs/op
// ตัวเลขตัวอย่าง: ~25000 ns/op, 1 alloc/op
func BenchmarkSliceIndexAssign(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := make([]int, sliceSize) // allocate พร้อม length
		for j := 0; j < sliceSize; j++ {
			s[j] = j * 2 // direct index assignment — ไม่ต้องตรวจ capacity
		}
		_ = s
	}
}

// ============================================================
// Helper Functions
// ============================================================

// makeWords สร้าง slice ของ words จำลอง
func makeWords(n int) []string {
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("word%d", i)
	}
	return words
}

// ============================================================
// BONUS: Thai Text Processing Benchmark
// ============================================================
//
// ข้อควรระวังสำหรับ Thai text ใน Go:
//   - Go string เป็น UTF-8 byte sequence
//   - ภาษาไทย 1 ตัวอักษร = 3 bytes (UTF-8)
//   - การ iterate ด้วย index จะได้ bytes ไม่ใช่ characters
//   - ต้องใช้ range loop หรือ []rune สำหรับ character operations

var thaiSample = "สวัสดีครับ ยินดีต้อนรับสู่คอร์ส Advanced Go สำหรับนักพัฒนาระดับ Enterprise"

// BenchmarkThaiStringBytes วัดการ iterate string แบบ byte (WRONG for Thai)
// แต่ละ iteration ได้ byte ไม่ใช่ Thai character
//
// ผลที่คาดหวัง: เร็ว แต่นับตัวอักษรผิด (นับ bytes แทน runes)
func BenchmarkThaiStringBytes(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		for range []byte(thaiSample) { // iterate as bytes
			count++
		}
		_ = count
	}
}

// BenchmarkThaiStringRunes วัดการ iterate string แบบ rune (CORRECT for Thai)
// range loop บน string ใน Go จะ decode UTF-8 และให้ rune (Unicode code point)
//
// ผลที่คาดหวัง: ช้ากว่า byte iteration เล็กน้อย เพราะต้อง decode UTF-8
func BenchmarkThaiStringRunes(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		for range thaiSample { // range on string → iterates runes
			count++
		}
		_ = count
	}
}

// BenchmarkThaiCountLetters วัดการนับตัวอักษรภาษาไทยโดยเฉพาะ
// ใช้ unicode.IsLetter เพื่อกรอง space และ punctuation ออก
//
// ผลที่คาดหวัง: ช้ากว่า rune iteration เพราะมี unicode.IsLetter check
func BenchmarkThaiCountLetters(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		for _, r := range thaiSample {
			if unicode.IsLetter(r) {
				count++
			}
		}
		_ = count
	}
}

// BenchmarkThaiToRuneSlice วัดการแปลง string เป็น []rune
// ใช้เมื่อต้องการ random access หรือ modify characters
// มีต้นทุน: allocate []rune ทั้งหมดก่อน
//
// ผลที่คาดหวัง: มี 1 alloc/op จาก []rune conversion
func BenchmarkThaiToRuneSlice(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runes := []rune(thaiSample) // allocate rune slice
		count := 0
		for _, r := range runes {
			if unicode.IsLetter(r) {
				count++
			}
		}
		_ = count
	}
}
