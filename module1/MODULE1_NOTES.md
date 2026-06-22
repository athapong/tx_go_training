# Module 1: Go Runtime Internals
---

## Core Concepts

### 1.1 Go Memory Model — ทำไม Data Race ถึงอันตราย

#### Happens-Before Guarantee คืออะไร?

Go Memory Model กำหนดว่า operations ระหว่าง goroutines จะมี "visibility" เมื่อไหร่
กฎง่ายๆ คือ: **ถ้าไม่มี synchronization ระหว่างสอง goroutines, ไม่มีการรับประกันว่าจะเห็น value ล่าสุด**

```
Goroutine A writes x=1          Goroutine B reads x
      |                                |
      v                                v
[Write x=1] ----NO GUARANTEE----> [Read x] = ??? (อาจได้ 0 หรือ 1)

กับ synchronization:
[Write x=1] --> [mutex.Unlock()] --> [mutex.Lock()] --> [Read x] = 1 (รับประกัน)
```

**Happens-before ใน Go มาจาก:**
1. Channel send happens-before channel receive
2. `sync.Mutex` Unlock happens-before next Lock
3. `sync/atomic` operations (ขึ้นกับ memory order)
4. `goroutine creation` happens-before goroutine start
5. `goroutine exit` — ไม่รับประกัน! ต้องใช้ WaitGroup

#### ทำไม Data Race ถึงเป็น Undefined Behavior?

อาจจะคิดว่า data race แค่ทำให้ได้ค่าเก่า — แต่ความจริงแย่กว่านั้น:

1. **Compiler optimization**: Compiler สามารถ reorder instructions ได้
2. **CPU cache**: แต่ละ CPU core มี cache ของตัวเอง
3. **Memory reordering**: CPU สามารถทำ out-of-order execution ได้
4. **Compiler may eliminate reads**: ถ้า compiler เห็นว่า value ไม่มีการ synchronize มัน อาจ optimize ออกไปเลย

---

### 1.2 GC Deep Dive — Tri-Color Mark-and-Sweep

```
การทำงานของ GC:

Phase 1: Mark Start (STW ~0.1ms)
  - หยุด goroutines ทั้งหมดชั่วคราว
  - กำหนด root set (global vars, stack vars)
  
Phase 2: Concurrent Mark (ไม่หยุดการทำงาน)
  ┌─────────────────────────────────────┐
  │  White: ยังไม่ได้ scan (อาจเป็น garbage) │
  │  Grey:  scan แล้ว แต่ children ยังไม่ได้ │
  │  Black: scan เสร็จ + children scan แล้ว │
  └─────────────────────────────────────┘
  
  Root objects → Grey
  Grey objects → scan children → Black
  ทำซ้ำจนไม่มี Grey เหลือ
  
Phase 3: Mark Termination (STW ~0.1ms)
  - ตรวจสอบ final state
  
Phase 4: Concurrent Sweep
  - White objects ที่เหลือ = garbage → reclaim
  - ไม่หยุด goroutines

ตัวอย่างภาพ:
    Step 1:          Step 2:          Step 3:
    [Root]           [Root★]          [Root■]
      |               |                |
    [A] [B]         [A★][B○]         [A■][B★]
      |                |                |
    [C]              [C○]             [C○]
    
    ★ = Grey  ■ = Black  ○ = White (garbage)
```

**GOGC Tuning:**
```
GOGC=100 (default): GC เมื่อ heap เพิ่มขึ้น 100% จาก live set
GOGC=200: GC ช้าลง ใช้ memory มากขึ้น แต่ throughput ดีขึ้น
GOGC=50:  GC บ่อยขึ้น ใช้ memory น้อยลง แต่ latency สูงขึ้น
GOGC=off: GC ไม่ทำงาน! อันตรายมากใน production
```

---

### 1.3 GMP Scheduler

```
G = Goroutine (unit of concurrency)
M = Machine Thread (OS thread)
P = Processor (virtual processor, controls concurrency)

โครงสร้าง:
┌─────────────────────────────────────┐
│           Global Run Queue           │
│     [G5] [G6] [G7] ...              │
└─────────────────────────────────────┘
         ↑                ↑
    steal work        overflow
         |                |
┌────────┴────┐    ┌──────┴──────┐
│     P1      │    │     P2      │
│  Local Q:   │    │  Local Q:   │
│  [G2][G3]   │    │  [G4]       │
│      |      │    │      |      │
│      M1     │    │      M2     │
│   (OS Thread│    │   (OS Thread│
└─────────────┘    └─────────────┘

GOMAXPROCS = จำนวน P (default = จำนวน CPU)

เมื่อ G บล็อค I/O:
  G (blocking) → M หยุดรอ syscall
  P ย้ายไป M อื่น (หรือสร้าง M ใหม่)
  ไม่กระทบ goroutines อื่น!
```

---

## Memory Layout ขั้นสูง — Cache Line และ False Sharing

### CPU Cache Line คืออะไร?

```
CPU อ่าน memory เป็น "line" ขนาด 64 bytes ต่อครั้ง

ตัวอย่าง: อ่าน int64 ที่ address 0x100
  → CPU โหลด bytes 0x100 ถึง 0x13F (64 bytes) เข้า L1 cache
  → ถ้าอ่าน int64 ถัดไปที่ 0x108 → cache HIT (อยู่ใน line เดิม)
  → ถ้าอ่าน int64 ที่ 0x200 → cache MISS (ต้องโหลด line ใหม่)

L1 cache hit:  ~4 cycles  (~1 ns)
L2 cache hit:  ~12 cycles (~3 ns)
L3 cache hit:  ~40 cycles (~10 ns)
RAM access:    ~200 cycles (~50-100 ns)
```

### False Sharing — ปัญหาที่มองไม่เห็น

```
ปัญหา: 2 goroutines แก้ตัวแปรคนละตัว แต่อยู่ใน cache line เดียวกัน

var counters [2]int64  // ทั้งคู่อยู่ใน 64 bytes เดียวกัน

goroutine A: counters[0]++  → invalidate cache line ทั้ง line
goroutine B: counters[1]++  → ต้อง reload cache line ใหม่

ผล: 2 goroutines "fight" กันที่ hardware level แม้จะเขียน address ต่างกัน
    → throughput ลดลงอย่างมาก (บางครั้ง ช้ากว่า single-threaded)
```

**ตรวจพิสูจน์ด้วย Benchmark:**
```go
// False sharing (ช้า)
type BadCounters struct {
    a int64
    b int64 // ห่างกัน 8 bytes → อยู่ใน cache line เดียวกัน
}

// No false sharing (เร็ว)
type GoodCounters struct {
    a   int64
    _   [56]byte // padding ให้ b ไปอยู่ cache line ใหม่
    b   int64
}

// หรือใช้ atomic + padding standard pattern:
type PaddedCounter struct {
    value int64
    _     [56]byte // 64 - 8 = 56
}
counters := [2]PaddedCounter{}

// goroutine A: atomic.AddInt64(&counters[0].value, 1)
// goroutine B: atomic.AddInt64(&counters[1].value, 1)
// → ไม่มี false sharing แล้ว
```

### การตรวจสอบ Memory Layout ด้วย unsafe

```go
import (
    "fmt"
    "unsafe"
)

type Order struct {
    ID       int64   // 8 bytes, offset 0
    Status   int8    // 1 byte,  offset 8
    // padding 7 bytes (เพื่อ align Quantity ที่ 8-byte boundary)
    Quantity int64   // 8 bytes, offset 16
    Price    float32 // 4 bytes, offset 24
    // padding 4 bytes
    Total    float64 // 8 bytes, offset 32
}                   // total: 40 bytes (เปลือง 11 bytes padding)

// ตรวจสอบ:
fmt.Println(unsafe.Sizeof(Order{}))           // 40
fmt.Println(unsafe.Offsetof(Order{}.Status))  // 8
fmt.Println(unsafe.Alignof(Order{}))          // 8

// Optimized: เรียง fields จากใหญ่ไปเล็ก
type OrderOptimized struct {
    ID       int64   // 8 bytes, offset 0
    Quantity int64   // 8 bytes, offset 8
    Total    float64 // 8 bytes, offset 16
    Price    float32 // 4 bytes, offset 24
    Status   int8    // 1 byte,  offset 28
    // padding 3 bytes เท่านั้น
}                   // total: 32 bytes (ประหยัด 8 bytes = 20%)
```

### fieldalignment Tool

```bash
# ตรวจทุก struct ใน project
go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest

fieldalignment ./...
# Output:
# module1/memory_model.go:15:12: struct with 40 pointer bytes could be 32

# Fix อัตโนมัติ:
fieldalignment -fix ./...
```

**ผลกระทบที่วัดได้:**
```
Slice of 1,000,000 Orders:
  BadOrder (40 bytes):    40 MB
  GoodOrder (32 bytes):   32 MB → ประหยัด 8 MB (20%)
  
  L3 cache throughput เพิ่มขึ้น ~15-25%
  เพราะ cache line เดียวบรรจุ records ได้มากขึ้น
```

### Struct Packing สำหรับ Network/Binary

```go
// สำหรับ packet parsing: ต้องการ exact layout, ไม่มี padding
// ใช้ unsafe หรือ binary.Read แทน

import "encoding/binary"

type PacketHeader struct {
    Version  uint8
    Flags    uint8
    Length   uint16
    Checksum uint32
} // ใน Go มี padding ถ้าไม่ระวัง!

// อ่านจาก binary stream อย่างปลอดภัย:
func parseHeader(data []byte) PacketHeader {
    return PacketHeader{
        Version:  data[0],
        Flags:    data[1],
        Length:   binary.BigEndian.Uint16(data[2:4]),
        Checksum: binary.BigEndian.Uint32(data[4:8]),
    }
}
```
