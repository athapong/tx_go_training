# Module 4: High-Performance Go
---

## Common Mistakes Developers Make

### 1. Premature Optimization (ปัญหาที่พบบ่อยที่สุด)

**อาการ**: เขียน code ซับซ้อนตั้งแต่แรก โดยยังไม่ได้วัดว่า bottleneck อยู่ที่ไหน

**ตัวอย่างที่พบบ่อย**:
- เพิ่ม sync.Pool ทุก struct โดยไม่ได้ benchmark ก่อน
- แยก goroutine ทุกงานโดยไม่คำนึงถึง overhead
- เขียน custom memory allocator ทั้งที่ GC เพียงพอแล้ว

**วิธีแก้**: ให้ทำ profiling ก่อนเสมอ ใช้ `go test -bench -benchmem` เป็น baseline

> "Donald Knuth บอกว่า premature optimization is the root of all evil — แต่ในระบบ enterprise ที่มี 10,000 req/sec ก็ต้อง optimize เมื่อ profile บอกว่าจำเป็น ไม่ใช่เมื่อรู้สึกว่าจำเป็น"

---

### 2. Profiling ผิดวิธี

**อาการ**: วัดด้วย `time.Now()` รอบๆ code แล้วสรุปว่านั่นคือ bottleneck

**ปัญหา**:
- wall time ไม่บอกว่าเสียเวลาไปกับอะไร (GC? IO? CPU?)
- ไม่เห็น allocation pattern
- ไม่เห็น goroutine blocking

**วิธีแก้**: แสดง pprof flame graph ให้เห็นชัดว่าข้อมูลต่างกันมากแค่ไหน

---

### 3. String Concatenation ด้วย +=

**อาการ**: เขียน SQL builder หรือ template builder โดยใช้ `result += " AND " + condition`

**ทำไมถึงเกิด**: นักพัฒนาที่มาจาก Python หรือ JavaScript คุ้นกับ immutable strings

**ผลกระทบ**: O(n²) allocations สำหรับ n conditions

**วิธีแก้**: benchmark ให้เห็นตัวเลขจริง แล้วแสดง strings.Builder

---

### 4. Goroutine Leak

**อาการ**: สร้าง goroutine แล้วไม่ cleanup เมื่อ request เสร็จ

**ตัวอย่าง**:
```go
// WRONG: goroutine นี้จะ leak ถ้า context cancel ก่อน ch ส่งค่า
go func() {
    result := doExpensiveWork()
    ch <- result // blocked ตลอดถ้าไม่มีใครรับ
}()
```

**วิธีตรวจสอบ**: `/debug/pprof/goroutine` — ถ้าจำนวน goroutines โตเรื่อยๆ คือ leak

---

### 5. ใช้ interface{} (any) โดยไม่จำเป็น

**อาการ**: function parameters เป็น `interface{}` ทั้งที่รู้ type ล่วงหน้า

**ปัญหา**:
- Boxing/unboxing overhead
- ทำลาย type safety
- GC ต้องจัดการ type descriptor เพิ่ม

**วิธีแก้**: ใช้ generics (Go 1.18+) สำหรับกรณีที่ต้องการ type flexibility

---

## pprof Workflow ครบวงจร — Capture → Analyze → Fix → Verify

### Step 0: เตรียม Server

```go
// เพิ่มแค่ 2 บรรทัดนี้ใน main.go
import _ "net/http/pprof"
go http.ListenAndServe(":6060", nil)
```

```bash
# รัน server
go run ./module4/profiling/

# สร้าง load เพื่อให้มีข้อมูล
# ใช้ hey (HTTP load tool) หรือ curl loop
hey -n 1000 -c 10 http://localhost:6060/work
# หรือ
for i in {1..100}; do curl -s http://localhost:6060/work > /dev/null; done
```

---

### Step 1: Capture CPU Profile

```bash
# วิธี A: ผ่าน HTTP (ขณะ server รันอยู่)
# เก็บ CPU profile 30 วินาที — ต้องมี load ระหว่างนี้!
go tool pprof -http=:8080 'http://localhost:6060/debug/pprof/profile?seconds=30'

# วิธี B: บันทึกไฟล์ก่อน แล้วค่อย analyze
curl -o /tmp/cpu.prof 'http://localhost:6060/debug/pprof/profile?seconds=30'
go tool pprof -http=:8080 /tmp/cpu.prof

# วิธี C: ใน benchmark (ไม่ต้องการ HTTP server)
go test -bench=BenchmarkWorkload -cpuprofile=/tmp/cpu.prof ./module4/benchmarks/
go tool pprof -http=:8080 /tmp/cpu.prof
```

**สิ่งที่จะเห็นใน Flame Graph:**
```
เปิด browser ที่ http://localhost:8080 → View → Flame Graph
สังเกต:
  - กล่องกว้างที่สุดที่ด้านบน = hot function (ใช้ CPU เยอะสุด)
  - กล่องสูง = call stack ลึก
  - ที่บน sidebar ซ้าย: "SAMPLE" = จำนวนครั้งที่ sampled ขณะรัน function นั้น
```

---

### Step 2: Capture Heap (Memory) Profile

```bash
# Heap profile: snapshot ของ live objects ณ ตอนนั้น
go tool pprof -http=:8080 http://localhost:6060/debug/pprof/heap

# ถ้าต้องการดู allocation ทั้งหมด (รวม freed objects)
go tool pprof -http=:8080 'http://localhost:6060/debug/pprof/heap?debug=2'

# Inuse vs Alloc:
# -inuse_space   = memory ที่ live อยู่ตอนนี้ (default)
# -alloc_space   = memory ที่ allocate ทั้งหมดตั้งแต่เริ่ม
# -alloc_objects = จำนวน objects ที่ allocate (นับ allocation ไม่ใช่ size)

# สลับโหมดใน pprof interactive shell:
go tool pprof http://localhost:6060/debug/pprof/heap
> (pprof) inuse_space
> (pprof) top10
> (pprof) alloc_objects
> (pprof) top10
```

**ค้นหา Memory Leak:**
```bash
# เรียก /memleak หลายๆ ครั้ง
for i in {1..20}; do curl http://localhost:6060/memleak; done

# เก็บ heap profile สองครั้ง ห่างกัน 30 วินาที
curl -o /tmp/heap1.prof http://localhost:6060/debug/pprof/heap
sleep 30
for i in {1..20}; do curl http://localhost:6060/memleak > /dev/null; done
curl -o /tmp/heap2.prof http://localhost:6060/debug/pprof/heap

# Compare! หา function ที่ memory เพิ่มขึ้น
go tool pprof -http=:8080 -base /tmp/heap1.prof /tmp/heap2.prof
# กล่องสีแดง = allocation เพิ่มขึ้น
# กล่องสีเขียว = allocation ลดลง (freed)
```

---

### Step 3: Capture Goroutine Profile

```bash
# Goroutine profile: แสดง goroutines ทั้งหมดและ stack trace
go tool pprof -http=:8080 http://localhost:6060/debug/pprof/goroutine

# ดู goroutine leak:
# เรียก /goroutineleak หลายครั้ง
for i in {1..10}; do curl http://localhost:6060/goroutineleak; done

# เปรียบเทียบ goroutine count:
curl -s http://localhost:6060/stats | python3 -m json.tool | grep goroutines

# ใน pprof UI: View → Source
# จะเห็น goroutines ที่ blocked ที่บรรทัดไหน
```

---

### Step 4: Execution Trace (ดู Timeline)

```bash
# Trace บันทึก timeline ของทุก event
curl -o /tmp/trace.out 'http://localhost:6060/debug/pprof/trace?seconds=5'
go tool trace /tmp/trace.out

# หน้าต่างที่ต้องดู:
# 1. "Goroutine analysis" — goroutines ไหน run นานที่สุด?
# 2. "Scheduler latency profile" — goroutine รอ scheduler นานแค่ไหน?
# 3. "Network blocking profile" — รอ network นานแค่ไหน?
# 4. "Synchronization blocking profile" — รอ mutex/channel นานแค่ไหน?
```

---

### Step 5: pprof CLI Commands (ที่ใช้บ่อย)

```bash
# เข้า interactive shell
go tool pprof http://localhost:6060/debug/pprof/heap

# Commands ที่ต้องรู้:
> top           # แสดง top 10 functions ตาม % CPU/memory
> top20         # top 20
> top -cum      # sort by cumulative (รวม callees)
> list funcName # แสดง source code พร้อม annotation
> web           # เปิด flame graph ใน browser (ต้องมี graphviz)
> pdf           # export เป็น PDF
> png           # export เป็น PNG
> svg           # export เป็น SVG

# ตัวอย่าง: ดู top 5 allocation functions
> top5 -alloc_space

# ตัวอย่าง: ดู source ของ generateProducts
> list generateProducts
```

---

### Step 6: Find → Fix → Verify Workflow

**ตัวอย่างจริง: แก้ memory allocation ใน hot path**

```bash
# 1. Find: ดู heap profile
go tool pprof http://localhost:6060/debug/pprof/heap
> top5 -alloc_objects
# Output:
#   flat  flat%   sum%        cum   cum%
#   850    42.5%  42.5%       850  42.5%  main.calculateRevenueByCategory
#   400    20.0%  62.5%       400  20.0%  main.generateProducts
```

```go
// 2. Fix: ดู source ของ calculateRevenueByCategory
// ปัญหา: สร้าง productMap ทุกครั้งที่ call
func calculateRevenueByCategory(products []Product, orders []Order) map[string]float64 {
    productMap := make(map[int]Product) // ALLOCATES EVERY CALL
    for _, p := range products {
        productMap[p.ID] = p
    }
    // ...
}

// แก้: รับ productMap เป็น parameter (pre-built once)
func calculateRevenueByCategory(productMap map[int]Product, orders []Order) map[string]float64 {
    // ไม่ต้อง build map ใหม่
}

// หรือใช้ sync.Pool สำหรับ map:
var mapPool = sync.Pool{
    New: func() any { return make(map[int]Product, 256) },
}

func calculateRevenueFast(products []Product, orders []Order) map[string]float64 {
    productMap := mapPool.Get().(map[int]Product)
    defer func() {
        for k := range productMap { delete(productMap, k) } // clear
        mapPool.Put(productMap)
    }()
    for _, p := range products { productMap[p.ID] = p }
    // ...
}
```

```bash
# 3. Verify: benchmark ก่อนและหลัง
go test -bench=BenchmarkCalculateRevenue -benchmem -count=5 ./module4/benchmarks/ > before.txt
# แก้ code
go test -bench=BenchmarkCalculateRevenue -benchmem -count=5 ./module4/benchmarks/ > after.txt
benchstat before.txt after.txt
# Output:
# name                        old allocs/op  new allocs/op  delta
# CalculateRevenue-8              850 ± 0%        2 ± 0%   -99.76%
```

---

## How to Read pprof Flame Graphs

### โครงสร้างของ Flame Graph

```
   ┌──────────────────────────────────────────────────────────────┐
   │  แกน X: สัดส่วนของ CPU time (กว้างกว่า = ใช้เวลามากกว่า)       │
   │  แกน Y: call stack depth (สูงกว่า = ถูกเรียกจาก function บน)   │
   └──────────────────────────────────────────────────────────────┘

HIGH (top of flame)
┌────────────────────────────────────────────────────────────────┐
│  calculateRevenueByCategory  │   fmt.Sprintf  │   runtime.GC  │
├──────────────────────────────┤                │               │
│  processOrders               │   strconv.Itoa │               │
├──────────────────────────────────────────────┤               │
│         generateProducts                      │               │
├───────────────────────────────────────────────────────────────┤
│                    workloadHandler                             │
├───────────────────────────────────────────────────────────────┤
│                    http.(*ServeMux).ServeHTTP                  │
├───────────────────────────────────────────────────────────────┤
│                    net/http.(*conn).serve                      │
└───────────────────────────────────────────────────────────────┘
LOW (bottom = entry point)
```

**วิธีอ่าน**:
1. **ดูที่ฐาน (bottom)**: นั่นคือ entry point (main, http handler, goroutine start)
2. **ดูที่ยอด (top plateaus)**: กล่องที่ "แบน" ที่ยอด = function ที่ใช้เวลาเอง ไม่ได้เรียก function อื่น
3. **มองหาช่องกว้าง**: column ที่กว้างที่สุด = hot path

**สัญญาณเตือน**:
- `runtime.GC` หรือ `runtime.mallocgc` กว้างมาก → allocation สูงเกินไป
- `sync.(*Mutex).Lock` กว้างมาก → lock contention
- `syscall.Read` หรือ `net.(*netFD).Read` → IO bound (ต้อง async แทน)

### ความแตกต่างระหว่าง Profile Types

| Profile Type | คำถามที่ตอบ | เมื่อใช้ |
|---|---|---|
| CPU profile | Function ไหนใช้ CPU เยอะสุด? | เมื่อ program ช้า แต่ CPU ยังสูง |
| Heap profile | Function ไหน allocate memory เยอะสุด? | เมื่อ memory สูงหรือ GC รันบ่อย |
| Goroutine profile | Goroutine ไหน blocked หรือรออะไรอยู่? | เมื่อสงสัย goroutine leak |
| Block profile | Goroutine ไหนรอ lock/channel นานสุด? | เมื่อ concurrency ไม่ scale |
| Mutex profile | Mutex ไหนมี contention สูงสุด? | เมื่อ lock เป็น bottleneck |
| Trace | Timeline ของทุก event ใน goroutine | เมื่อต้องการเห็น latency spike |

---

## Benchmark Interpretation Guide

### ตัวอย่าง output จริงที่จะเห็น

```
BenchmarkStringPlusEquals-8        50000    24523 ns/op    5680 B/op    99 allocs/op
BenchmarkStringsBuilder-8         500000     2341 ns/op     128 B/op     2 allocs/op
BenchmarkStringsBuilderNoGrow-8   300000     3102 ns/op     256 B/op     5 allocs/op
BenchmarkStringsJoin-8           1000000      921 ns/op     128 B/op     1 allocs/op
```

### อธิบายแต่ละคอลัมน์

**`-8`**: จำนวน GOMAXPROCS (8 CPUs)

**`50000`**: จำนวนครั้งที่ loop รัน (b.N) — Go ปรับอัตโนมัติจนได้ผลที่ stable

**`24523 ns/op`**: เวลาเฉลี่ยต่อ 1 iteration (nanoseconds per operation)
- 1 ns = 0.000001 ms
- 24523 ns ≈ 0.025 ms ≈ 1/40,000 วินาที

**`5680 B/op`**: bytes ที่ allocate ต่อ 1 iteration
- รวม stack + heap allocations
- สูงกว่า = GC pressure มากกว่า

**`99 allocs/op`**: จำนวนครั้งที่ call `malloc` ต่อ 1 iteration
- นี่คือ **ตัวเลขที่สำคัญที่สุด** สำหรับ optimization
- 0 allocs/op = ไม่มี heap allocation = GC ไม่ต้องทำงาน
- สูงกว่า = GC ทำงานบ่อยกว่า = latency spike มากกว่า

### เมื่อไรควรสนใจ allocs/op

**สนใจมากเมื่อ**:
- Function อยู่ใน hot path (เรียกบ่อย เช่น ทุก HTTP request)
- allocs/op สูงมาก (>10 ต่อ business operation)
- Heap profile แสดงว่า function นี้ allocate เยอะ

**ไม่ต้องสนใจเมื่อ**:
- Function เรียกแค่ตอน startup
- allocs/op เป็น 1-2 ตัว แต่ไม่ได้อยู่ใน hot path
- ระบบไม่ได้ต้องการ low latency (<1ms)

### วิธีใช้ benchstat เปรียบเทียบ

```bash
# เก็บ baseline
go test -bench=. -benchmem -count=5 ./module4/benchmarks/ > before.txt

# หลัง optimize
go test -bench=. -benchmem -count=5 ./module4/benchmarks/ > after.txt

# เปรียบเทียบ
go install golang.org/x/perf/cmd/benchstat@latest
benchstat before.txt after.txt
```

**Output ตัวอย่าง**:
```
name                    old time/op    new time/op    delta
StringPlusEquals-8        24.5µs ± 2%    24.3µs ± 1%      ~  (p=0.421 n=5+5)
StringsBuilder-8          2.34µs ± 3%    2.01µs ± 2%   -14%  (p=0.008 n=5+5)
```

`p=0.008` หมายความว่า improvement มี statistical significance (p < 0.05 = ดี)

---

## Session Timing Guide

### ภาพรวม 4–5 ชั่วโมง

| Section | เวลา | เนื้อหา | กิจกรรม |
|---|---|---|---|
| Introduction + Why Performance Matters | 20 min | Enterprise context, profiling philosophy | Discussion |
| pprof Setup & Endpoints | 45 min | Demo รัน profiling/main.go, ดู flame graph | Hands-on |
| Break | 10 min | | |
| Benchmarks Walkthrough | 60 min | รัน benchmarks_test.go, อ่านผล | Hands-on |
| String & Map Deep Dive | 30 min | อธิบาย mechanics ของ string/map | Lecture + Q&A |
| Break | 10 min | | |
| sync.Pool & Struct Alignment | 40 min | Demo optimization/main.go | Hands-on |
| JSON Processing Optimization | 30 min | Before/after comparison | Code review |
| Thai Text Processing | 20 min | UTF-8, rune vs byte | Demo |
| Profile-Driven Workflow | 30 min | End-to-end exercise | Hands-on |
| Q&A + Wrap-up | 15 min | | |

### ส่วนที่ใช้เวลานานกว่าที่คิด

1. **Flame graph interpretation** (เพิ่ม 15-20 min) — ส่วนใหญ่ไม่เคยเห็น flame graph มาก่อน ต้องอธิบาย axes หลายรอบ

2. **sync.Pool reset pattern** (เพิ่ม 10 min) — มักเกิดคำถามว่า "ถ้า reset ไม่ครบจะเกิดอะไร?" — เตรียม bug demo ไว้

3. **benchstat interpretation** (เพิ่ม 15 min) — p-value และ statistical significance เป็นเรื่องใหม่สำหรับหลายคน

### จุดที่ควรเพิ่ม Break

- หลัง pprof demo (ต้อง process ข้อมูลมาก)
- ก่อน struct alignment section (เนื้อหาเปลี่ยน mental model)

---

## Key Talking Points Per Section

### Section 1: Why Profile First

**จุดสำคัญ**:
- "ถ้า benchmark ไม่บอกว่าช้า อย่า optimize" — ประหยัดเวลาได้มาก
- pprof เป็น built-in ไม่ต้องติดตั้ง third-party
- Production profiling ทำได้โดย overhead น้อยมาก (< 5% สำหรับ CPU sampling)

**ตัวอย่างที่ impact สูง**:
```
เพิ่มแค่ 2 บรรทัด:
    import _ "net/http/pprof"
    go http.ListenAndServe(":6060", nil)

ได้ profiling ครบทุก endpoint ทันที
```

---

### Section 2: String Concatenation

**จุดสำคัญ**:
- ใน Go string เป็น immutable — `s1 + s2` สร้าง string ใหม่เสมอ
- `+=` ใน loop = O(n²) — น่ากลัวสำหรับ SQL builder
- `strings.Builder.Grow()` ช่วยลด re-allocation แต่ต้องรู้ขนาดก่อน
- `strings.Join` เร็วที่สุดเมื่อ join สิ่งที่อยู่ใน slice แล้ว

**Demo ที่น่าประทับใจ**:
```bash
# รัน benchmark แล้วดูตัวเลข allocs/op
go test -bench=BenchmarkString -benchmem -benchtime=5s ./module4/benchmarks/

# ผลที่น่าตกใจ:
# PlusEquals: 99 allocs/op  ← เยอะมาก
# Builder:     2 allocs/op  ← ดีมาก
# Join:         1 alloc/op  ← ดีที่สุด
```

---

### Section 3: Map vs sync.Map

**จุดสำคัญ**:
- sync.Map ไม่ใช่ "always better" — มี trade-off
- ต้อง benchmark ตาม use case จริงของตัวเอง
- Read-heavy + stable keys → sync.Map ดีกว่า
- Write-heavy หรือ key set เปลี่ยนบ่อย → map+RWMutex ดีกว่า

**Analogy ที่ใช้อธิบาย**:
> sync.Map เหมือน whiteboard ที่ทุกคนอ่านได้พร้อมกัน แต่คนที่จะเขียนต้องรอให้ทุกคนออกไปก่อน  
> map+RWMutex เหมือนห้องประชุมที่อ่านได้หลายคนพร้อมกัน แต่เขียนได้ทีละคน

---

### Section 4: Interface Boxing

**จุดสำคัญ**:
- Interface call ไม่ได้ช้ามาก (1-2 ns) แต่ถ้าอยู่ใน tight loop ก็สะสมได้
- ปัญหาจริงคือ heap allocation เมื่อ value ใหญ่กว่า pointer
- Go 1.18+ generics ช่วยได้ในหลายกรณี

**เมื่อไรต้องใช้ interface**:
- Dependency injection (testing, plugins)
- Multiple implementations ที่สลับกันได้
- Public API ที่ต้องการ flexibility

**เมื่อไรควรหลีกเลี่ยง**:
- Hot path ที่รู้ concrete type ล่วงหน้า
- Internal code ที่ type ไม่เปลี่ยน

---

### Section 5: Struct Alignment

**จุดสำคัญ**:
- Go ไม่ reorder fields อัตโนมัติ (ต่างจาก C++ compiler optimization)
- Rule of thumb: เรียง fields จากใหญ่ไปเล็ก
- ผลกระทบชัดเจนที่สุดเมื่อ iterate slice ขนาดใหญ่

**Tool ที่แนะนำ**:
```bash
go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
fieldalignment ./...
```

**ตัวเลขที่น่าสนใจ**:
- BadAlignedStruct อาจใหญ่กว่า GoodAlignedStruct ถึง 25-30%
- สำหรับ slice 1M items → ประหยัด RAM ได้ significant amount

---

### Section 6: sync.Pool

**จุดสำคัญ**:
- Pool ถูก GC เคลียร์ทุก GC cycle — ไม่ใช่ cache ถาวร
- **ต้อง reset ทุก field ก่อน Put** — data ค้างจาก request ก่อนหน้าเป็น security bug
- เหมาะที่สุดสำหรับ: JSON buffers, HTTP request structs, temp byte slices

**Bug ที่ต้องเตือน**:
```go
// DANGEROUS: ลืม reset
buf := pool.Get().(*bytes.Buffer)
buf.Write(sensitiveData)
pool.Put(buf)  // WRONG: sensitiveData ยังอยู่ใน buf!

// CORRECT:
buf.Reset()    // ล้างก่อน
pool.Put(buf)
```

---

## Function Inlining — ให้ Compiler ช่วย Optimize

### Inlining คืออะไร?

```
ไม่มี Inlining:
  caller()  ─call─►  add(a, b)  ─return─►  caller

มี Inlining (compiler แทน call ด้วย body โดยตรง):
  caller():
    result = a + b  ← body ของ add() อยู่ที่นี่เลย
    
ผล: ไม่มี function call overhead, stack frame ไม่ถูกสร้าง
```

### ตรวจว่า Function ถูก Inline ไหม

```bash
# -m แสดง inlining decisions
go build -gcflags="-m" ./...

# -m -m แสดงรายละเอียดมากขึ้น
go build -gcflags="-m -m" ./...
```

**Output ตัวอย่าง:**
```
./hot_path.go:12:6: can inline add
./hot_path.go:18:6: cannot inline processOrder: too complex (cost 100 > 80)
./hot_path.go:35:8: inlining call to add
./main.go:22:14: add escapes to heap
```

### Inlining Budget

Go กำหนด "cost" สำหรับแต่ละ operation:
- ถ้า cost ของ function > 80 → ไม่ถูก inline
- `if/for/switch`: cost 1 ต่ออัน
- function call: cost เพิ่มตาม complexity ของ callee

### เทคนิคที่ช่วยให้ Inline ได้

```go
// ผิด: function ใหญ่เกินไป → ไม่ inline
func processOrder(o *Order) error {
    // 50 บรรทัด ← compiler บอก "too complex"
}

// ถูก: แยก hot path ออก และทำให้ inline ได้
func validateAmount(amount int64) bool {
    return amount > 0 && amount <= maxAmount // simple → inline ได้
}

func processOrder(o *Order) error {
    if !validateAmount(o.Amount) { // validateAmount inline มา → zero call overhead
        return ErrInvalidAmount
    }
    // ... complex logic
}
```

```go
// เทคนิค: ใช้ defer น้อยลงใน hot path
// defer มี cost เล็กน้อยและป้องกัน inlining บางกรณี

// แทนที่:
func getFromCache(key string) (Value, bool) {
    mu.RLock()
    defer mu.RUnlock()   // ป้องกัน inline
    return cache[key]
}

// ใช้:
func getFromCache(key string) (Value, bool) {
    mu.RLock()
    v, ok := cache[key]
    mu.RUnlock()         // ไม่มี defer → inline ได้
    return v, ok
}
```

### Escape Analysis ร่วมกับ Inlining

```bash
# ดูว่าตัวแปรไหน escape ไป heap
go build -gcflags="-m=2" ./...
```

```go
// ถ้า function ที่ถูก inline มีตัวแปร escape → ตัวแปรนั้น allocate บน heap
// แม้จะ inline แล้วก็ตาม

// Bad: pointer return ทำให้ escape
func newPoint() *Point {
    p := Point{1, 2} // escape ไป heap เพราะ return pointer
    return &p
}

// Good: return value ไม่ escape
func newPoint() Point {
    return Point{1, 2} // อยู่บน stack ของ caller
}
```

### Demo: Benchmark Inline vs No-Inline

```go
// ทดสอบ: BenchmarkInlineEffect
//go:noinline  // force ไม่ให้ inline (สำหรับ benchmark เท่านั้น)
func addNoInline(a, b int) int { return a + b }

func addNormal(a, b int) int { return a + b } // จะถูก inline

func BenchmarkInlineEffect(b *testing.B) {
    x, y := 1, 2
    for i := 0; i < b.N; i++ {
        _ = addNoInline(x, y) // call overhead
    }
}

func BenchmarkInlineNormal(b *testing.B) {
    x, y := 1, 2
    for i := 0; i < b.N; i++ {
        _ = addNormal(x, y) // inlined → ไม่มี call
    }
}
// ผล: Normal เร็วกว่า 20-40% บน tight loop
```

---

## Algorithm Selection — เลือก Algorithm ที่ Cache-Friendly

### ทำไม Cache Line ถึงสำคัญ?

```
CPU Cache Line = 64 bytes

เมื่อ CPU อ่าน memory:
  - โหลด 64 bytes ทีเดียว (ไม่ใช่แค่ 8 bytes ที่ขอ)
  - ถ้า data ถัดไปอยู่ใน 64 bytes เดิม → cache hit (fast)
  - ถ้า data ถัดไปอยู่ที่อื่น → cache miss (slow, ~100ns)

Sequential access: cache hit rate สูง
Random access:    cache hit rate ต่ำ → ช้ากว่ามาก
```

### Slice vs Map — ไม่ใช่แค่เรื่อง Complexity

```go
// Map: O(1) lookup แต่ random memory access
type UserMap map[UserID]*User

// Slice: O(n) lookup แต่ sequential access → cache-friendly
type UserSlice []User

// สำหรับ n < 1000 ทดสอบแล้ว Slice เร็วกว่า Map บน lookup!
// เพราะ cache miss penalty ของ Map > linear scan penalty ของ Slice

// ทดสอบด้วย benchmark ก่อนตัดสินใจเสมอ
```

### Binary Search แทน Linear Search

```go
// Linear: O(n) — cache-friendly แต่ช้าสำหรับ n ใหญ่
func findLinear(sorted []int, target int) int {
    for i, v := range sorted {
        if v == target {
            return i
        }
    }
    return -1
}

// Binary: O(log n) — standard library ready
func findBinary(sorted []int, target int) int {
    i := sort.SearchInts(sorted, target)
    if i < len(sorted) && sorted[i] == target {
        return i
    }
    return -1
}

// สำหรับ n = 1000: binary search เร็วกว่า ~10x
// สำหรับ n = 10:  linear อาจเร็วกว่า (cache warmup)
```

### Struct of Arrays vs Array of Structs (SoA vs AoS)

```go
// AoS (Array of Structs) — ทั่วไป แต่ inefficient ถ้าใช้แค่บางฟิลด์
type Particle struct {
    X, Y, Z float32 // 12 bytes
    VX, VY, VZ float32 // 12 bytes
    Mass float32    // 4 bytes
    // total 28 bytes per particle
}
particles := []Particle{...}

// ถ้า hot loop ใช้แค่ X, Y, Z:
for i := range particles {
    particles[i].X += particles[i].VX // โหลด 28 bytes แต่ใช้ 12
}

// SoA (Struct of Arrays) — cache-friendly สำหรับ SIMD และ partial access
type Particles struct {
    X, Y, Z   []float32 // position
    VX, VY, VZ []float32 // velocity
    Mass       []float32
}

// hot loop ใช้แค่ position:
for i := range p.X {
    p.X[i] += p.VX[i] // โหลดเฉพาะ X slice → 100% cache utilization
}
// เร็วกว่า AoS ได้ถึง 3-5x สำหรับ large datasets
```

### Decision Guide: เลือก Algorithm อย่างไร

| Scenario | เลือก | เหตุผล |
|----------|-------|--------|
| n < 20, lookup บ่อย | Linear scan ใน slice | Cache-friendly, simple |
| n > 1000, sorted, lookup บ่อย | Binary search | O(log n) ไม่ต้องแตะ map |
| Key อะไรก็ได้, insert/delete บ่อย | map | O(1) amortized |
| Sort slice ขนาดใหญ่ (n > 1000) | sort.Slice | Timsort, cache-aware |
| Numeric range queries | Sorted slice + binary | Better cache than tree |
| String matching ใน document | strings.Index (Boyer-Moore) | ดีกว่า naive O(nm) |

### Lab: วัดผลก่อนตัดสินใจ

```bash
# สร้าง benchmark เปรียบเทียบ
go test -bench=BenchmarkLookup -benchmem -benchtime=5s ./module4/benchmarks/

# Profile ด้วย perf (Linux) หรือ Instruments (macOS)
# เพื่อดู cache miss rate จริงๆ
```

---

## 5 "Gotcha" Questions for Students

### Question 1: String Loop

**ถาม**: Code ต่อไปนี้มีปัญหาอะไร?
```go
func buildQuery(conditions []string) string {
    result := "SELECT * FROM users WHERE "
    for i, cond := range conditions {
        if i > 0 {
            result += " AND "
        }
        result += cond
    }
    return result
}
```

**คำตอบที่ต้องการ**: 
- ใช้ `+=` ใน loop = O(n²) allocations
- สำหรับ 100 conditions = 100 string copies
- ควรใช้ `strings.Builder` + `strings.Join(conditions, " AND ")`

**Follow-up**: แล้วถ้า conditions เป็น empty slice ล่ะ? (ตรวจสอบ edge case)

---

### Question 2: sync.Pool Safety

**ถาม**: Code นี้ถูกต้องหรือเปล่า?
```go
type RequestCtx struct {
    UserID int
    Token  string
    Data   []byte
}

var pool = sync.Pool{New: func() any { return &RequestCtx{} }}

func handler(w http.ResponseWriter, r *http.Request) {
    ctx := pool.Get().(*RequestCtx)
    defer pool.Put(ctx)  // return ทันทีโดยไม่ reset
    
    ctx.UserID = getUserID(r)
    ctx.Token = r.Header.Get("Authorization")
    // ...
}
```

**คำตอบที่ต้องการ**: 
- มี security bug! Token จาก request A อาจถูก request B เห็นได้
- ต้องเพิ่ม `defer func() { ctx.Token = ""; ctx.UserID = 0; ctx.Data = nil; pool.Put(ctx) }()`

---

### Question 3: Goroutine Profile

**ถาม**: ดู goroutine profile แล้วเห็น goroutines 10,000 ตัวที่ stuck บน `<-ch` ทั้งหมด เป็นปัญหาหรือเปล่า?

**คำตอบที่ต้องการ**: 
- ขึ้นอยู่กับว่า channels เหล่านั้นควร receive หรือเปล่า
- ถ้า goroutines โตเรื่อยๆ เมื่อเวลาผ่านไป = goroutine leak
- ถ้า stable = อาจเป็น worker pool ที่ idle อยู่ (ปกติ)
- ดู stack trace ว่า blocking ที่บรรทัดไหน ในไฟล์อะไร

---

### Question 4: Benchmark Validity

**ถาม**: Benchmark นี้วัดอะไรได้ถูกต้องหรือเปล่า?
```go
func BenchmarkFoo(b *testing.B) {
    data := setupExpensiveData() // ใช้เวลา 2 วินาที
    for i := 0; i < b.N; i++ {
        result := processData(data)
        _ = result
    }
}
```

**คำตอบที่ต้องการ**: 
- ผิด! `setupExpensiveData()` ถูกนับใน benchmark time
- ต้องเพิ่ม `b.ResetTimer()` หลัง setup:
```go
data := setupExpensiveData()
b.ResetTimer()
for i := 0; i < b.N; i++ { ... }
```

---

### Question 5: Interface vs Concrete

**ถาม**: Function ไหนเร็วกว่า และทำไม?
```go
// Version A
func sumA(values []int) int {
    sum := 0
    for _, v := range values {
        sum += v
    }
    return sum
}

// Version B  
type Summer interface { Sum() int }
func sumB(s Summer) int { return s.Sum() }
```

**คำตอบที่ต้องการ**:
- Version A เร็วกว่าแน่นอน เพราะ:
  1. Direct function call ไม่มี vtable lookup
  2. Loop body อาจถูก inline และ vectorize ได้ (SIMD)
  3. ไม่มี boxing/unboxing
- Version B มี overhead จาก interface dispatch ทุกครั้ง
- **แต่**: ถ้า `Summer` มีหลาย implementations และ hot path ไม่ใช่ tight loop ก็อาจยอมรับได้

---

## Additional Resources

**Official Tools**:
- `go tool pprof` — built-in profiler
- `go tool trace` — execution tracer
- `go test -bench -benchmem` — micro-benchmarking
- `benchstat` — statistical benchmark comparison

**Recommended Reading**:
- "The Go Programming Language" — Chapter on performance
- Dave Cheney's blog: https://dave.cheney.net (performance posts)
- Go Blog: "Profiling Go Programs" — https://go.dev/blog/pprof

**Tools to Install**:
```bash
# benchmark comparison
go install golang.org/x/perf/cmd/benchstat@latest

# struct field alignment checker
go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest

# load testing (ทดสอบ HTTP pool)
go install github.com/rakyll/hey@latest
```

**Commands for This Module**:
```bash
# รัน profiling server
go run ./module4/profiling/

# รัน benchmarks ทั้งหมด
go test -bench=. -benchmem -benchtime=3s ./module4/benchmarks/

# รัน benchmarks เฉพาะกลุ่ม
go test -bench=BenchmarkString -benchmem ./module4/benchmarks/
go test -bench=BenchmarkMap -benchmem ./module4/benchmarks/
go test -bench=BenchmarkSyncPool -benchmem ./module4/benchmarks/

# รัน optimization demo
go run ./module4/optimization/

# generate CPU profile จาก benchmark
go test -bench=BenchmarkStringPlusEquals -cpuprofile=cpu.out ./module4/benchmarks/
go tool pprof -http=:8080 cpu.out

# generate memory profile
go test -bench=BenchmarkSliceAppendOnly -memprofile=mem.out ./module4/benchmarks/
go tool pprof -http=:8080 mem.out
```
