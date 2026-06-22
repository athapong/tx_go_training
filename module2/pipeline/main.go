// Package main สาธิต Pipeline Pattern ด้วย Fan-Out/Fan-In
// รัน: go run pipeline.go
package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// ============================================================
// PIPELINE PATTERN — Fan-Out / Fan-In
// ============================================================
// Pipeline: Input → Stage1 → Stage2 → Stage3 → Output
//
// Fan-Out: หนึ่ง channel input, หลาย goroutines process
// Fan-In:  หลาย channels input, รวมเป็น channel เดียว
//
// เหมาะสำหรับ: batch processing, data transformation
// Example: อ่านไฟล์ → parse → validate → save → report

// Record คือ data unit ที่ไหลผ่าน pipeline
type Record struct {
	ID    int
	Data  string
	Value float64
	// metadata สำหรับ tracing
	CreatedAt   time.Time
	ProcessedBy []string
}

// ============================================================
// STAGE 1: Generator — สร้าง records
// ============================================================

func generateRecords(ctx context.Context, count int) <-chan Record {
	out := make(chan Record, 100) // buffer ป้องกัน generator block

	go func() {
		defer close(out)

		for i := 0; i < count; i++ {
			select {
			case <-ctx.Done():
				return
			case out <- Record{
				ID:        i,
				Data:      fmt.Sprintf("record-%d", i),
				Value:     rand.Float64() * 100,
				CreatedAt: time.Now(),
			}:
			}
		}
	}()

	return out
}

// ============================================================
// STAGE 2: Validator — ตรวจสอบ records
// ============================================================

func validateRecord(ctx context.Context, in <-chan Record) <-chan Record {
	out := make(chan Record, 100)

	go func() {
		defer close(out)

		for record := range in {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Validation logic
			if record.Value < 0 || record.Value > 100 {
				// Skip invalid records
				fmt.Printf("Skipping invalid record %d: value=%.2f\n",
					record.ID, record.Value)
				continue
			}

			record.ProcessedBy = append(record.ProcessedBy, "validator")

			select {
			case <-ctx.Done():
				return
			case out <- record:
			}
		}
	}()

	return out
}

// ============================================================
// STAGE 3: Transformer — แปลง records (Fan-Out)
// ============================================================

// fanOut แจก records ไปยัง workers หลายตัว
func fanOut(ctx context.Context, in <-chan Record, workerCount int) []<-chan Record {
	channels := make([]<-chan Record, workerCount)

	for i := 0; i < workerCount; i++ {
		out := make(chan Record, 50)
		channels[i] = out

		workerID := i
		go func() {
			defer close(out)

			for record := range in {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Simulate processing time (แตกต่างกันต่าม worker)
				processingTime := time.Duration(rand.Intn(5)) * time.Millisecond
				time.Sleep(processingTime)

				record.Value = record.Value * 1.1 // ตัวอย่าง transformation
				record.ProcessedBy = append(record.ProcessedBy,
					fmt.Sprintf("transformer-%d", workerID))

				select {
				case out <- record:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	return channels
}

// ============================================================
// Fan-In — รวม channels หลายตัวเป็น channel เดียว
// ============================================================

func fanIn(ctx context.Context, channels ...<-chan Record) <-chan Record {
	var wg sync.WaitGroup
	merged := make(chan Record, 200)

	// output function อ่านจาก channel และส่งไป merged
	output := func(c <-chan Record) {
		defer wg.Done()
		for record := range c {
			select {
			case merged <- record:
			case <-ctx.Done():
				return
			}
		}
	}

	// เริ่ม goroutine สำหรับแต่ละ channel
	wg.Add(len(channels))
	for _, c := range channels {
		go output(c)
	}

	// ปิด merged channel เมื่อทุก input เสร็จ
	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged
}

// ============================================================
// STAGE 4: Aggregator — รวม results
// ============================================================

type AggregatedResult struct {
	TotalRecords    int
	TotalValue      float64
	AverageValue    float64
	ProcessingTime  time.Duration
	WorkersUsed     map[string]int
}

func aggregateResults(ctx context.Context, in <-chan Record) AggregatedResult {
	result := AggregatedResult{
		WorkersUsed: make(map[string]int),
	}
	start := time.Now()

	for record := range in {
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		result.TotalRecords++
		result.TotalValue += record.Value

		for _, worker := range record.ProcessedBy {
			result.WorkersUsed[worker]++
		}
	}

done:
	if result.TotalRecords > 0 {
		result.AverageValue = result.TotalValue / float64(result.TotalRecords)
	}
	result.ProcessingTime = time.Since(start)

	return result
}

// ============================================================
// BACKPRESSURE EXAMPLE
// ============================================================

// WithBackpressure เพิ่ม rate limiting ลงใน pipeline
func withBackpressure(ctx context.Context, in <-chan Record, rate int) <-chan Record {
	out := make(chan Record)
	// rate = records per second
	ticker := time.NewTicker(time.Second / time.Duration(rate))

	go func() {
		defer close(out)
		defer ticker.Stop()

		for record := range in {
			// รอ tick ก่อนส่ง (backpressure)
			select {
			case <-ticker.C:
				select {
				case out <- record:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// ============================================================
// COMPLETE PIPELINE DEMO
// ============================================================

func runPipelineDemo() {
	const totalRecords = 100
	const workers = 4

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	fmt.Printf("Starting pipeline: %d records, %d workers\n", totalRecords, workers)

	// Stage 1: Generate
	generated := generateRecords(ctx, totalRecords)

	// Stage 2: Validate
	validated := validateRecord(ctx, generated)

	// Stage 3: Fan-Out (4 workers)
	workerChannels := fanOut(ctx, validated, workers)

	// Stage 3b: Fan-In (รวมกลับ)
	processed := fanIn(ctx, workerChannels...)

	// Stage 4: Aggregate
	result := aggregateResults(ctx, processed)

	elapsed := time.Since(start)

	fmt.Printf("\n=== Pipeline Results ===\n")
	fmt.Printf("Total Records:   %d\n", result.TotalRecords)
	fmt.Printf("Total Value:     %.2f\n", result.TotalValue)
	fmt.Printf("Average Value:   %.2f\n", result.AverageValue)
	fmt.Printf("Processing Time: %v\n", result.ProcessingTime)
	fmt.Printf("Wall Time:       %v\n", elapsed)
	fmt.Printf("Throughput:      %.0f records/sec\n",
		float64(result.TotalRecords)/elapsed.Seconds())
	fmt.Printf("\nWorker Usage:\n")
	for worker, count := range result.WorkersUsed {
		fmt.Printf("  %s: %d records\n", worker, count)
	}
}

// ============================================================
// GOROUTINE LEAK EXAMPLES — สิ่งที่ไม่ควรทำ
// ============================================================

// LEAK 1: Goroutine รอ channel ที่ไม่มีใครส่งให้
func goroutineLeak1() {
	ch := make(chan int) // unbuffered channel
	go func() {
		// goroutine นี้จะ block ตลอดไป!
		// ไม่มีใครส่งค่าเข้า ch
		val := <-ch
		fmt.Println(val)
	}()
	// function return แต่ goroutine ยังรันอยู่!
}

// LEAK 1 FIX: ใช้ context สำหรับ cancellation
func goroutineLeak1Fixed(ctx context.Context) {
	ch := make(chan int, 1) // buffered หรือใช้ context
	go func() {
		select {
		case val := <-ch:
			fmt.Println(val)
		case <-ctx.Done():
			// Clean exit เมื่อ context cancel
			return
		}
	}()
}

// LEAK 2: Goroutine ส่ง channel ที่ไม่มีใครรับ
func goroutineLeak2() {
	ch := make(chan int) // unbuffered
	go func() {
		// block ตลอดไป เพราะไม่มีใครรับ
		ch <- 42
	}()
	// ถ้า caller ไม่อ่าน ch, goroutine จะ stuck
}

// LEAK 2 FIX: ใช้ context หรือ buffered channel
func goroutineLeak2Fixed() {
	ch := make(chan int, 1) // buffer 1 ทำให้ send ไม่บล็อค
	go func() {
		ch <- 42 // ไม่บล็อค เพราะ buffer มีที่ว่าง
	}()
}

// LEAK 3: HTTP server handler ที่ไม่จัดการ timeout
// func leakyHTTPHandler(w http.ResponseWriter, r *http.Request) {
//     // ถ้า downstream service ช้า request นี้จะ hang ตลอดไป
//     result := slowDownstreamCall() // ไม่มี timeout!
//     w.Write([]byte(result))
// }

// LEAK 3 FIX: ใช้ request context
// func fixedHTTPHandler(w http.ResponseWriter, r *http.Request) {
//     ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
//     defer cancel()
//     result, err := slowDownstreamCallWithCtx(ctx)
//     if err != nil {
//         http.Error(w, err.Error(), http.StatusServiceUnavailable)
//         return
//     }
//     w.Write([]byte(result))
// }

// LEAK 4: WaitGroup.Add ผิดที่
func goroutineLeak4() {
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		go func() {
			wg.Add(1) // ผิด! Add ต้องทำก่อน go
			defer wg.Done()
			time.Sleep(100 * time.Millisecond)
		}()
	}
	wg.Wait() // อาจ return ก่อน goroutines เสร็จ!
}

// LEAK 4 FIX:
func goroutineLeak4Fixed() {
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1) // Add ก่อน go!
		go func() {
			defer wg.Done()
			time.Sleep(100 * time.Millisecond)
		}()
	}
	wg.Wait() // รอจน goroutines ทั้งหมดเสร็จ
}

// LEAK 5: goroutine ที่ไม่มีวันหยุด
func goroutineLeak5() {
	// ไม่มี stop mechanism!
	go func() {
		for {
			// poll บางอย่าง
			time.Sleep(time.Second)
			fmt.Println("still running...")
		}
	}()
}

// LEAK 5 FIX:
func goroutineLeak5Fixed(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				fmt.Println("still running (properly)...")
			case <-ctx.Done():
				fmt.Println("goroutine stopped cleanly")
				return
			}
		}
	}()
}

func main() {
	runPipelineDemo()

	fmt.Println("\n=== Goroutine Leak Demo ===")
	fmt.Println("(Leaks are shown in code comments, not executed)")

	// ตัวอย่าง fixed versions
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	goroutineLeak1Fixed(ctx)
	goroutineLeak2Fixed()
	goroutineLeak4Fixed()
	goroutineLeak5Fixed(ctx)

	// รอ context expire
	<-ctx.Done()
	fmt.Println("All goroutines cleaned up via context")
}
