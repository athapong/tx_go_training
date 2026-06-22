// Package workerpool — Production-grade Worker Pool implementation
// รัน: go run worker_pool.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// CORE TYPES
// ============================================================

// Job คือ unit of work ที่ส่งเข้า pool
type Job struct {
	ID      string
	Payload interface{}
	// context สำหรับ cancellation per-job
	Ctx context.Context
}

// Result คือผลลัพธ์จากการทำ job
type Result struct {
	JobID string
	Value interface{}
	Err   error
	// tracking metrics
	StartTime time.Time
	EndTime   time.Time
	WorkerID  int
}

func (r Result) Duration() time.Duration {
	return r.EndTime.Sub(r.StartTime)
}

// WorkerFunc คือ function ที่ worker จะเรียก
type WorkerFunc func(ctx context.Context, job Job) (interface{}, error)

// ============================================================
// WORKER POOL METRICS
// ============================================================

type PoolMetrics struct {
	JobsSubmitted  atomic.Int64
	JobsCompleted  atomic.Int64
	JobsFailed     atomic.Int64
	JobsDropped    atomic.Int64 // ตกหล่นเพราะ backpressure
	PanicRecovered atomic.Int64
	ActiveWorkers  atomic.Int32
}

func (m *PoolMetrics) Print() {
	fmt.Printf("=== Pool Metrics ===\n")
	fmt.Printf("Submitted:  %d\n", m.JobsSubmitted.Load())
	fmt.Printf("Completed:  %d\n", m.JobsCompleted.Load())
	fmt.Printf("Failed:     %d\n", m.JobsFailed.Load())
	fmt.Printf("Dropped:    %d\n", m.JobsDropped.Load())
	fmt.Printf("Panics:     %d\n", m.PanicRecovered.Load())
	fmt.Printf("Active:     %d\n", m.ActiveWorkers.Load())
}

// ============================================================
// WORKER POOL — Production Grade
// ============================================================

type WorkerPool struct {
	// Configuration
	workerCount int
	fn          WorkerFunc
	logger      *slog.Logger

	// Channels
	// jobChan มี buffer สำหรับ backpressure
	jobChan    chan Job
	resultChan chan Result

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Metrics
	metrics PoolMetrics

	// State
	running atomic.Bool
}

// PoolOption สำหรับ functional options pattern
type PoolOption func(*WorkerPool)

func WithLogger(logger *slog.Logger) PoolOption {
	return func(p *WorkerPool) {
		p.logger = logger
	}
}

func WithBufferSize(size int) PoolOption {
	return func(p *WorkerPool) {
		// สร้าง channel ใหม่ด้วย buffer size ที่ต้องการ
		p.jobChan = make(chan Job, size)
	}
}

// NewWorkerPool สร้าง pool ใหม่
// workerCount: จำนวน workers
// bufferSize: ขนาด job queue (backpressure buffer)
// fn: function ที่จะ execute แต่ละ job
func NewWorkerPool(
	workerCount int,
	bufferSize int,
	fn WorkerFunc,
	opts ...PoolOption,
) *WorkerPool {
	if workerCount <= 0 {
		workerCount = 1
	}
	if bufferSize <= 0 {
		bufferSize = workerCount * 10 // default buffer
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		workerCount: workerCount,
		fn:          fn,
		logger:      slog.Default(),
		jobChan:     make(chan Job, bufferSize),
		resultChan:  make(chan Result, bufferSize),
		ctx:         ctx,
		cancel:      cancel,
	}

	for _, opt := range opts {
		opt(pool)
	}

	return pool
}

// Start เริ่มต้น workers
func (p *WorkerPool) Start() {
	if !p.running.CompareAndSwap(false, true) {
		// ป้องกัน double start
		return
	}

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		// แต่ละ worker มี ID ของตัวเอง
		go p.worker(i)
	}

	p.logger.Info("Worker pool started",
		"workers", p.workerCount,
		"buffer", cap(p.jobChan))
}

// worker คือ goroutine ที่ทำงานจริง
func (p *WorkerPool) worker(id int) {
	defer func() {
		p.metrics.ActiveWorkers.Add(-1)
		p.wg.Done()
	}()

	p.metrics.ActiveWorkers.Add(1)
	p.logger.Debug("Worker started", "worker_id", id)

	for {
		select {
		case <-p.ctx.Done():
			// Pool ถูก shutdown
			p.logger.Debug("Worker stopping (context cancelled)", "worker_id", id)
			return

		case job, ok := <-p.jobChan:
			if !ok {
				// Channel closed
				return
			}

			// ทำงาน job พร้อม panic recovery
			result := p.executeWithRecovery(id, job)

			// ส่ง result กลับ
			select {
			case p.resultChan <- result:
				// ส่งสำเร็จ
			case <-p.ctx.Done():
				return
			}
		}
	}
}

// executeWithRecovery รัน job พร้อม panic recovery
func (p *WorkerPool) executeWithRecovery(workerID int, job Job) (result Result) {
	result = Result{
		JobID:     job.ID,
		StartTime: time.Now(),
		WorkerID:  workerID,
	}

	// Defer สำหรับ panic recovery
	defer func() {
		result.EndTime = time.Now()

		if r := recover(); r != nil {
			// Panic ถูก recover!
			p.metrics.PanicRecovered.Add(1)
			p.metrics.JobsFailed.Add(1)

			stack := debug.Stack()
			p.logger.Error("Panic recovered in worker",
				"worker_id", workerID,
				"job_id", job.ID,
				"panic", r,
				"stack", string(stack))

			result.Err = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	// ตรวจสอบ context ของ job ก่อนทำงาน
	if job.Ctx != nil {
		select {
		case <-job.Ctx.Done():
			result.Err = fmt.Errorf("job context cancelled before execution: %w", job.Ctx.Err())
			p.metrics.JobsFailed.Add(1)
			return
		default:
		}
	}

	// ทำงานจริง
	ctx := p.ctx
	if job.Ctx != nil {
		// ใช้ job context ถ้ามี (อาจมี timeout/deadline)
		ctx = job.Ctx
	}

	value, err := p.fn(ctx, job)
	result.Value = value
	result.Err = err

	if err != nil {
		p.metrics.JobsFailed.Add(1)
	} else {
		p.metrics.JobsCompleted.Add(1)
	}

	return
}

// Submit ส่ง job เข้า pool
// Returns: true ถ้าส่งสำเร็จ, false ถ้า queue เต็ม (backpressure)
func (p *WorkerPool) Submit(job Job) bool {
	p.metrics.JobsSubmitted.Add(1)

	select {
	case p.jobChan <- job:
		return true
	default:
		// Queue เต็ม! ทำ backpressure
		p.metrics.JobsDropped.Add(1)
		p.logger.Warn("Job dropped due to backpressure",
			"job_id", job.ID,
			"queue_len", len(p.jobChan),
			"queue_cap", cap(p.jobChan))
		return false
	}
}

// SubmitWait ส่ง job และรอจนกว่าจะสำเร็จหรือ context cancel
func (p *WorkerPool) SubmitWait(ctx context.Context, job Job) error {
	p.metrics.JobsSubmitted.Add(1)

	select {
	case p.jobChan <- job:
		return nil
	case <-ctx.Done():
		p.metrics.JobsDropped.Add(1)
		return ctx.Err()
	case <-p.ctx.Done():
		return errors.New("pool is shutting down")
	}
}

// Results returns channel สำหรับอ่าน results
func (p *WorkerPool) Results() <-chan Result {
	return p.resultChan
}

// Shutdown หยุด pool อย่างสุภาพ
// รอให้ jobs ที่กำลังทำอยู่เสร็จก่อน
func (p *WorkerPool) Shutdown(ctx context.Context) error {
	if !p.running.CompareAndSwap(true, false) {
		return nil // already stopped
	}

	p.logger.Info("Initiating graceful shutdown")

	// ปิด job channel เพื่อบอก workers ว่าไม่มี job ใหม่แล้ว
	close(p.jobChan)

	// รอให้ workers เสร็จ
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("All workers finished")
		p.cancel()
		close(p.resultChan)
		return nil

	case <-ctx.Done():
		// Timeout! บังคับหยุด
		p.logger.Warn("Graceful shutdown timeout, forcing stop")
		p.cancel() // บังคับยกเลิก context
		return fmt.Errorf("shutdown timeout: %w", ctx.Err())
	}
}

// Metrics returns metrics snapshot
func (p *WorkerPool) Metrics() *PoolMetrics {
	return &p.metrics
}

// ============================================================
// EXAMPLE USAGE
// ============================================================

func runWorkerPoolDemo() {
	logger := slog.Default()

	// สร้าง pool: 5 workers, buffer 20 jobs
	pool := NewWorkerPool(
		5,  // workers
		20, // buffer
		func(ctx context.Context, job Job) (interface{}, error) {
			// Simulate work
			payload := job.Payload.(string)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
				return fmt.Sprintf("processed: %s", payload), nil
			}
		},
		WithLogger(logger),
	)

	pool.Start()

	// Consumer goroutine — อ่าน results
	var resultWg sync.WaitGroup
	resultWg.Add(1)
	go func() {
		defer resultWg.Done()
		for result := range pool.Results() {
			if result.Err != nil {
				fmt.Printf("Job %s FAILED: %v\n", result.JobID, result.Err)
			} else {
				fmt.Printf("Job %s done in %v: %v\n",
					result.JobID, result.Duration(), result.Value)
			}
		}
	}()

	// ส่ง 30 jobs
	for i := 0; i < 30; i++ {
		job := Job{
			ID:      fmt.Sprintf("job-%03d", i),
			Payload: fmt.Sprintf("data-%d", i),
			Ctx:     context.Background(),
		}

		if !pool.Submit(job) {
			fmt.Printf("Job %s dropped (backpressure)\n", job.ID)
		}
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("Shutdown error: %v\n", err)
	}

	// รอ consumer เสร็จ
	resultWg.Wait()

	// แสดง metrics
	pool.Metrics().Print()
}

func main() {
	runWorkerPoolDemo()
}
