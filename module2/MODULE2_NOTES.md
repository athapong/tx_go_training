# Module 2: Advanced Concurrency Patterns

## Core Concepts

### Worker Pool — เมื่อไหร่ควรใช้?

```
ปัญหาที่ Worker Pool แก้:
  
  ไม่ดี: สร้าง goroutine ต่อ request ไปเรื่อยๆ
  for _, item := range 100000items {
      go process(item) // 100,000 goroutines! จนทำให้ memory leak
  }
  
  ดีกว่า: Worker Pool
  pool := NewWorkerPool(workers=10, buffer=1000, fn=process)
  for _, item := range 100000items {
      pool.Submit(job)
  }
  
  ผล:
  - Goroutines: 10 (ควบคุมได้)
  - Memory: คงที่
  - Backpressure: job ที่เกิน drop ได้ controlled
```

### Pipeline Pattern — Data Flow Architecture

```
Input → [Stage A] → [Stage B] → [Stage B] → Output
         ↓               ↓Fan-Out↓
        [G1]           [G1][G2][G3]
        [G2]              ↓Fan-In↓
                        [merged]
```

### Backpressure คืออะไร?

```
ไม่มี Backpressure:
  Producer (เร็ว): →→→→→→→→→→→→→→ Consumer (ช้า)
  Memory เพิ่มขึ้นเรื่อยๆ จน OOM

มี Backpressure:
  Producer (เร็ว): → [buffer limit] → Consumer (ช้า)
  เมื่อ buffer เต็ม Producer ต้องรอหรือ drop
  Memory คงที่
```

---

## ข้อผิดพลาดที่พบบ่อย

### 1. Goroutine Leaks — 5 รูปแบบหลัก

```go
// LEAK 1: Channel ที่ไม่มีใครส่ง
go func() {
    val := <-ch // block ตลอดไป!
}()

// FIX: ใช้ select + context
go func() {
    select {
    case val := <-ch:
        process(val)
    case <-ctx.Done():
        return
    }
}()
```

```go
// LEAK 2: WaitGroup.Add ผิดที่
for i := 0; i < 5; i++ {
    go func() {
        wg.Add(1) // ผิด! อาจ Add หลัง Wait() แล้ว
        defer wg.Done()
    }()
}

// FIX: Add ก่อน go เสมอ
for i := 0; i < 5; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
    }()
}
```

```go
// LEAK 3: Infinite goroutine ไม่มี stop
go func() {
    for {
        doWork()
        time.Sleep(time.Second)
    }
}()

// FIX: ใช้ context
go func() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            doWork()
        case <-ctx.Done():
            return
        }
    }
}()
```

```go
// LEAK 4: Channel Deadlock — ส่งลง unbuffered channel แต่ไม่มีคนรับ
ch := make(chan int)
go func() {
    ch <- 1 // block! caller ไม่รับ
    ch <- 2 // ไม่มีวันถึงบรรทัดนี้
}()
// ไม่มี <-ch ที่นี่

// FIX: buffered channel หรือ receive ก่อน close
ch := make(chan int, 2)
go func() {
    ch <- 1
    ch <- 2
}()
```

```go
// LEAK 5: Close channel สองครั้ง → panic
close(ch)
close(ch) // PANIC: close of closed channel

// FIX: ใช้ sync.Once
var once sync.Once
close := func() {
    once.Do(func() { close(ch) })
}
```

### 2. Channel Direction ที่ถูกต้อง

```go
// ดีกว่า: กำหนด direction ใน function signature
// <-chan = receive only (อ่านได้อย่างเดียว)
// chan<- = send only (เขียนได้อย่างเดียว)

func producer(ctx context.Context) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        // ส่งได้
        out <- 1
    }()
    return out // return receive-only
}

func consumer(ctx context.Context, in <-chan int) {
    for v := range in {
        // รับได้
        fmt.Println(v)
        // in <- 1 // ERROR: cannot send to receive-only channel
    }
}
```

---

## Lab Solution — Complete Worker Pool with Metrics

```go
// ดู worker_pool.go สำหรับ complete implementation
// Lab exercise: เพิ่ม retry logic เข้าไปใน pool

type RetryConfig struct {
    MaxRetries int
    Backoff    time.Duration
}

func (p *WorkerPool) executeWithRetry(workerID int, job Job, retry RetryConfig) Result {
    var lastErr error
    for attempt := 0; attempt <= retry.MaxRetries; attempt++ {
        result := p.executeWithRecovery(workerID, job)
        if result.Err == nil {
            return result
        }
        lastErr = result.Err
        
        if attempt < retry.MaxRetries {
            backoff := retry.Backoff * time.Duration(1<<uint(attempt))
            select {
            case <-time.After(backoff):
            case <-p.ctx.Done():
                return Result{JobID: job.ID, Err: p.ctx.Err()}
            }
        }
    }
    return Result{JobID: job.ID, Err: lastErr}
}
```

---

## Dynamic Worker Pool — Pool ที่ขยาย/หดตัวได้เอง

### ปัญหาของ Static Worker Pool

```
Static Pool (10 workers ตลอดเวลา):
  00:00 น. — มี 10,000 req/sec  → 10 workers ไม่พอ, queue ยาว
  03:00 น. — มี 10 req/sec       → 10 workers ว่างงาน, เสีย memory

Dynamic Pool แก้ปัญหา:
  00:00 น. — spawn workers เพิ่ม จนถึง maxWorkers
  03:00 น. — workers ที่ idle นานเกิน idleTimeout → terminate ตัวเองได้ ไม่เปลือง memory
```

### โครงสร้าง Dynamic Pool

```go
type DynamicPool struct {
    minWorkers  int
    maxWorkers  int
    idleTimeout time.Duration
    jobs        chan Job
    
    mu      sync.Mutex
    current int  // จำนวน workers ที่ active อยู่ตอนนี้
}

func (p *DynamicPool) Submit(job Job) {
    select {
    case p.jobs <- job:
        // job เข้า queue สำเร็จ
    default:
        // queue เต็ม → spawn worker ใหม่ถ้ายังไม่ถึง max
        p.mu.Lock()
        if p.current < p.maxWorkers {
            p.current++
            p.mu.Unlock()
            go p.runWorker()
        } else {
            p.mu.Unlock()
            // ถึง max แล้ว → block หรือ drop (ขึ้นกับ policy)
            p.jobs <- job // block จนมีที่ว่าง
        }
    }
}

func (p *DynamicPool) runWorker() {
    defer func() {
        p.mu.Lock()
        p.current--
        p.mu.Unlock()
    }()
    
    timer := time.NewTimer(p.idleTimeout)
    defer timer.Stop()
    
    for {
        select {
        case job, ok := <-p.jobs:
            if !ok {
                return // pool ปิดแล้ว
            }
            timer.Reset(p.idleTimeout)
            job.Execute()
        case <-timer.C:
            // idle เกิน timeout → worker terminate ตัวเอง
            // แต่รักษา minWorkers ไว้
            p.mu.Lock()
            if p.current > p.minWorkers {
                p.mu.Unlock()
                return
            }
            p.mu.Unlock()
            timer.Reset(p.idleTimeout) // reset แล้วรอต่อ
        }
    }
}
```

### เมื่อไหร่ใช้ Dynamic vs Static

| Situation | เลือก |
|-----------|-------|
| Load คงที่ predictable | Static Pool |
| Load spike ช่วงสั้นๆ (เช่น ตีห้า) | Dynamic Pool |
| Memory budget เข้มงวด | Static Pool |
| Latency-sensitive (ไม่ยอมให้ queue ยาว) | Dynamic Pool |

---

## Channel Patterns ขั้นสูง

### Pattern 1: Semaphore ด้วย Buffered Channel

```go
// จำกัด concurrency สูงสุด N goroutines พร้อมกัน
// (เหมือน worker pool แต่ไม่มี persistent goroutines)
sem := make(chan struct{}, 5) // semaphore ขนาด 5

for _, item := range items {
    sem <- struct{}{} // acquire slot (block ถ้าเต็ม)
    go func(i Item) {
        defer func() { <-sem }() // release slot
        process(i)
    }(item)
}
// drain semaphore เพื่อรอ goroutines ที่เหลือ
for i := 0; i < cap(sem); i++ {
    sem <- struct{}{}
}
```

**ใช้เมื่อ**: ต้องการ limit concurrent I/O calls (เช่น max 5 HTTP requests พร้อมกัน)

### Pattern 2: errgroup — Cancel เมื่อมี Error คนแรก

```go
import "golang.org/x/sync/errgroup"

g, ctx := errgroup.WithContext(context.Background())

for _, url := range urls {
    url := url
    g.Go(func() error {
        resp, err := fetch(ctx, url)
        if err != nil {
            return err // triggers ctx.Done() ให้ goroutines อื่น
        }
        process(resp)
        return nil
    })
}

if err := g.Wait(); err != nil {
    // error แรกที่เกิด, goroutines อื่น cancel แล้ว
    log.Fatal(err)
}
```

**ต่าง WaitGroup อย่างไร**: WaitGroup แค่รอ, errgroup cancel goroutines อื่นทันทีที่ใดมี error

### Pattern 3: Done Channel — Broadcast Stop Signal

```go
// done channel broadcast signal หยุดให้ทุก goroutine
done := make(chan struct{})

worker := func(id int) {
    for {
        select {
        case <-done:
            fmt.Printf("worker %d stopping\n", id)
            return
        case job := <-jobs:
            process(job)
        }
    }
}

// spawn workers
for i := 0; i < 10; i++ {
    go worker(i)
}

// หยุดทุก goroutine พร้อมกัน — close channel broadcast ให้ทุกคน
close(done) // ทุก goroutine รับ zero value ทันที
```

**สำคัญ**: close(channel) broadcast แต่ send(channel) ส่งให้คนเดียว

### Pattern 4: Timeout Channel ด้วย select

```go
// Pattern: ส่ง job แต่ไม่ยอม block นานกว่า 100ms
func submitWithTimeout(jobs chan<- Job, job Job, timeout time.Duration) error {
    select {
    case jobs <- job:
        return nil
    case <-time.After(timeout):
        return fmt.Errorf("job queue full after %v", timeout)
    }
}

// Pattern: รับผล แต่ถ้าช้าเกิน → ใช้ default value
func getWithFallback(ch <-chan Result, fallback Result, timeout time.Duration) Result {
    select {
    case r := <-ch:
        return r
    case <-time.After(timeout):
        return fallback
    }
}
```

### Pattern 5: Fan-Out with Rate Limiting

```go
// Dispatch jobs ไปหลาย workers แต่ limit rate
func fanOutWithRateLimit(ctx context.Context, items []Item, limit rate.Limit) {
    limiter := rate.NewLimiter(limit, int(limit)) // burst = rate
    g, ctx := errgroup.WithContext(ctx)
    
    for _, item := range items {
        item := item
        if err := limiter.Wait(ctx); err != nil {
            break // context cancelled
        }
        g.Go(func() error {
            return process(ctx, item)
        })
    }
    g.Wait()
}
```

---

## Teaching Notes (Instructor Only)
### Opening Demo (ทำสดในห้อง)
```bash
# แสดงปัญหา goroutine leak
# 1. เขียน code ที่มี leak
# 2. ใช้ pprof ดู goroutines
go tool pprof http://localhost:6060/debug/pprof/goroutine

# จะเห็น goroutines เพิ่มขึ้น → aha moment!
```

### Key Points ที่ต้องเน้น
1. **Buffered vs Unbuffered**: เหมือนกล่องจดหมาย vs โทรศัพท์
   - Unbuffered: ต้องมีคนรับพร้อมๆ กัน (synchronous)
   - Buffered: วางไว้ก่อน คนรับมาเอาทีหลัง

2. **Fan-Out/Fan-In**: แสดงด้วย ASCII diagram บนกระดาน
   - Fan-Out = parallel processing
   - Fan-In = aggregation

3. **Backpressure**: เปรียบเหมือน traffic light
   - ไม่มี backpressure = ไม่มี traffic light → รถชน
   - มี backpressure = หยุดรถชั่วคราว → ระบบ stable

### Common Questions
Q: "ใช้ goroutines เยอะเท่าไหร่ถือว่า 'มากเกินไป'?"
A: Go รองรับ goroutines หลักล้านตัว แต่ rule of thumb คือ:
   - I/O bound: goroutines = 10x CPU cores ได้
   - CPU bound: goroutines ≈ CPU cores
   - แต่ละ goroutine ใช้ memory ~2KB minimum

Q: "Worker Pool vs sync.WorkerGroup ต่างกันอย่างไร?"
A: sync.WaitGroup แค่รอให้ goroutines เสร็จ
   Worker Pool ควบคุม concurrency + backpressure + metrics

### Energy Check: ทำ interactive exercise
ให้ trace การไหลของ data ผ่าน pipeline บนกระดาน
"Record #42 เดินทางอย่างไรจาก generator ถึง aggregator?"
