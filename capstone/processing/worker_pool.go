// Package processing — Concurrent Invoice Processing
// Module 2 Integration: Worker Pool สำหรับประมวลผล invoice หลายใบพร้อมกัน
//
// Pipeline ของแต่ละ invoice:
//
//	RawInvoice → Extract (AI) → Validate → Save to Repo → Publish Event
//
// Features:
// - Worker pool แบบ configurable
// - Error isolation: invoice ที่ fail ไม่กระทบ batch ที่เหลือ
// - Panic recovery ระดับ worker
// - Graceful shutdown
// - Processing metrics
package processing

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"tx_go_training/capstone/ai"
	"tx_go_training/capstone/domain"
)

// ============================================================
// TYPES
// ============================================================

// RawInvoice — input สำหรับ batch processing
type RawInvoice struct {
	ID       string // external reference ID
	VendorID string
	RawText  string
	// Metadata
	ReceivedAt time.Time
	Source     string // "api", "email", "upload"
}

// ProcessingResult — ผลลัพธ์จากการประมวลผล invoice หนึ่งใบ
type ProcessingResult struct {
	RawInvoice RawInvoice
	InvoiceID  string
	Success    bool
	Error      error
	// Timing
	StartedAt   time.Time
	CompletedAt time.Time
	// Extracted data (nil ถ้า extraction ล้มเหลว)
	Extracted *ai.ExtractedInvoiceData
}

func (r ProcessingResult) Duration() time.Duration {
	return r.CompletedAt.Sub(r.StartedAt)
}

// BatchResult — ผลลัพธ์จาก ProcessBatch
type BatchResult struct {
	Results       []ProcessingResult
	TotalCount    int
	SuccessCount  int
	FailureCount  int
	TotalDuration time.Duration
}

// ============================================================
// METRICS (ใช้ atomic สำหรับ thread safety)
// ============================================================

// ProcessorMetrics — metrics สำหรับ monitoring
type ProcessorMetrics struct {
	InvoicesSubmitted atomic.Int64
	InvoicesProcessed atomic.Int64
	InvoicesFailed    atomic.Int64
	ExtractionErrors  atomic.Int64
	ValidationErrors  atomic.Int64
	RepositoryErrors  atomic.Int64
	PanicsRecovered   atomic.Int64
	ActiveWorkers     atomic.Int32
	TotalProcessingMs atomic.Int64 // total milliseconds (หาร count ได้ average)
}

func (m *ProcessorMetrics) AverageProcessingMs() float64 {
	count := m.InvoicesProcessed.Load()
	if count == 0 {
		return 0
	}
	return float64(m.TotalProcessingMs.Load()) / float64(count)
}

func (m *ProcessorMetrics) ErrorRate() float64 {
	total := m.InvoicesSubmitted.Load()
	if total == 0 {
		return 0
	}
	return float64(m.InvoicesFailed.Load()) / float64(total)
}

// ============================================================
// INVOICE PROCESSOR
// ============================================================

// InvoiceProcessor — ประมวลผล invoice ด้วย worker pool
type InvoiceProcessor struct {
	workerCount int
	extractor   ai.InvoiceExtractor
	repo        domain.InvoiceRepository
	eventBus    domain.EventBus
	logger      *slog.Logger
	metrics     *ProcessorMetrics
}

// ProcessorOption — functional options
type ProcessorOption func(*InvoiceProcessor)

func WithWorkerCount(n int) ProcessorOption {
	return func(p *InvoiceProcessor) {
		if n > 0 {
			p.workerCount = n
		}
	}
}

// NewInvoiceProcessor สร้าง processor ใหม่
func NewInvoiceProcessor(
	extractor ai.InvoiceExtractor,
	repo domain.InvoiceRepository,
	eventBus domain.EventBus,
	logger *slog.Logger,
	opts ...ProcessorOption,
) *InvoiceProcessor {
	p := &InvoiceProcessor{
		workerCount: 5, // default
		extractor:   extractor,
		repo:        repo,
		eventBus:    eventBus,
		logger:      logger,
		metrics:     &ProcessorMetrics{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Metrics คืน metrics snapshot สำหรับ observability
func (p *InvoiceProcessor) Metrics() *ProcessorMetrics {
	return p.metrics
}

// ============================================================
// BATCH PROCESSING
// ============================================================

// ProcessBatch ประมวลผล invoices หลายใบพร้อมกัน
// ใช้ worker pool — จำนวน workers กำหนดโดย workerCount
//
// Error handling:
// - แต่ละ invoice fail ได้โดยไม่กระทบ batch
// - Panic ใน worker ถูก recover และบันทึกเป็น error
// - Context cancellation หยุด batch ทั้งหมด
func (p *InvoiceProcessor) ProcessBatch(ctx context.Context, invoices []RawInvoice) BatchResult {
	startTime := time.Now()
	p.logger.Info("Starting batch processing",
		"count", len(invoices),
		"workers", p.workerCount,
		"extractor", p.extractor.Name(),
	)

	if len(invoices) == 0 {
		return BatchResult{}
	}

	// Channels
	jobChan := make(chan RawInvoice, len(invoices))
	resultChan := make(chan ProcessingResult, len(invoices))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < p.workerCount; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			p.worker(ctx, workerID, jobChan, resultChan)
		}()
	}

	// Submit all invoices
	for _, inv := range invoices {
		p.metrics.InvoicesSubmitted.Add(1)
		select {
		case jobChan <- inv:
		case <-ctx.Done():
			p.logger.Warn("Batch cancelled while submitting jobs")
			break
		}
	}
	close(jobChan) // signal workers: no more jobs

	// Collect results in background
	results := make([]ProcessingResult, 0, len(invoices))
	var resultMu sync.Mutex
	var collectWg sync.WaitGroup
	collectWg.Add(1)
	go func() {
		defer collectWg.Done()
		for result := range resultChan {
			resultMu.Lock()
			results = append(results, result)
			resultMu.Unlock()
		}
	}()

	// Wait for all workers, then close result channel
	wg.Wait()
	close(resultChan)
	collectWg.Wait()

	// Build batch result
	batchResult := BatchResult{
		Results:       results,
		TotalCount:    len(results),
		TotalDuration: time.Since(startTime),
	}
	for _, r := range results {
		if r.Success {
			batchResult.SuccessCount++
		} else {
			batchResult.FailureCount++
		}
	}

	p.logger.Info("Batch processing complete",
		"total", batchResult.TotalCount,
		"success", batchResult.SuccessCount,
		"failed", batchResult.FailureCount,
		"duration", batchResult.TotalDuration,
	)

	return batchResult
}

// ProcessSingle ประมวลผล invoice เดียว (สำหรับ API endpoint)
func (p *InvoiceProcessor) ProcessSingle(ctx context.Context, raw RawInvoice) ProcessingResult {
	p.metrics.InvoicesSubmitted.Add(1)
	return p.processOne(ctx, 0, raw)
}

// ============================================================
// WORKER INTERNALS
// ============================================================

// worker คือ goroutine ที่ทำงานจริง — รับ jobs จาก channel
func (p *InvoiceProcessor) worker(
	ctx context.Context,
	workerID int,
	jobs <-chan RawInvoice,
	results chan<- ProcessingResult,
) {
	p.metrics.ActiveWorkers.Add(1)
	defer p.metrics.ActiveWorkers.Add(-1)

	p.logger.Debug("Invoice worker started", "worker_id", workerID)

	for {
		select {
		case <-ctx.Done():
			p.logger.Debug("Worker stopping: context cancelled", "worker_id", workerID)
			return
		case raw, ok := <-jobs:
			if !ok {
				// Channel closed — no more jobs
				p.logger.Debug("Worker stopping: no more jobs", "worker_id", workerID)
				return
			}

			result := p.processWithRecovery(ctx, workerID, raw)

			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		}
	}
}

// processWithRecovery ครอบ processOne ด้วย panic recovery
func (p *InvoiceProcessor) processWithRecovery(ctx context.Context, workerID int, raw RawInvoice) (result ProcessingResult) {
	result = ProcessingResult{
		RawInvoice: raw,
		StartedAt:  time.Now(),
	}

	defer func() {
		result.CompletedAt = time.Now()

		if r := recover(); r != nil {
			p.metrics.PanicsRecovered.Add(1)
			p.metrics.InvoicesFailed.Add(1)

			stack := debug.Stack()
			p.logger.Error("Panic recovered in invoice worker",
				"worker_id", workerID,
				"invoice_id", raw.ID,
				"panic", r,
				"stack", string(stack),
			)

			result.Success = false
			result.Error = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	return p.processOne(ctx, workerID, raw)
}

// processOne ทำ pipeline สำหรับ invoice เดียว:
// Receive → Extract → Validate → Save → PublishEvent
func (p *InvoiceProcessor) processOne(ctx context.Context, workerID int, raw RawInvoice) ProcessingResult {
	result := ProcessingResult{
		RawInvoice: raw,
		StartedAt:  time.Now(),
	}

	log := p.logger.With(
		"worker_id", workerID,
		"raw_invoice_id", raw.ID,
		"vendor_id", raw.VendorID,
	)

	log.Info("Processing invoice")

	// ── Step 1: สร้าง domain Invoice aggregate ──
	invoiceID, err := domain.NewInvoiceID(raw.ID)
	if err != nil {
		return p.failResult(result, fmt.Errorf("invalid invoice ID: %w", err))
	}

	vendorID, err := domain.NewVendorID(raw.VendorID)
	if err != nil {
		return p.failResult(result, fmt.Errorf("invalid vendor ID: %w", err))
	}

	invoice, err := domain.ReceiveInvoice(invoiceID, vendorID, raw.RawText)
	if err != nil {
		return p.failResult(result, fmt.Errorf("creating invoice: %w", err))
	}

	// ── Step 2: Save initial state (Received) ──
	if err := p.repo.Save(ctx, invoice); err != nil {
		p.metrics.RepositoryErrors.Add(1)
		return p.failResult(result, fmt.Errorf("saving received invoice: %w", err))
	}

	// Publish InvoiceReceived events
	p.publishEvents(ctx, invoice)

	// ── Step 3: Transition to Processing ──
	if err := invoice.StartProcessing(); err != nil {
		return p.failResult(result, fmt.Errorf("starting processing: %w", err))
	}
	if err := p.repo.Save(ctx, invoice); err != nil {
		p.metrics.RepositoryErrors.Add(1)
		return p.failResult(result, fmt.Errorf("saving processing state: %w", err))
	}

	// ── Step 4: AI Extraction ──
	extractStart := time.Now()
	extracted, err := p.extractor.Extract(ctx, raw.RawText)
	extractDuration := time.Since(extractStart)

	log.Info("Extraction complete",
		"extractor", p.extractor.Name(),
		"duration", extractDuration,
		"vendor", func() string {
			if extracted != nil {
				return extracted.VendorName
			}
			return "N/A"
		}(),
	)

	if err != nil {
		p.metrics.ExtractionErrors.Add(1)
		// Reject invoice เมื่อ extraction ล้มเหลว
		_ = invoice.Reject("system", fmt.Sprintf("AI extraction failed: %v", err))
		_ = p.repo.Save(ctx, invoice)
		p.publishEvents(ctx, invoice)
		return p.failResult(result, fmt.Errorf("ai extraction failed: %w", err))
	}

	result.Extracted = extracted

	// ── Step 5: Apply extraction to domain aggregate ──
	lineItems, err := p.buildDomainLineItems(extracted)
	if err != nil {
		p.metrics.ValidationErrors.Add(1)
		_ = invoice.Reject("system", fmt.Sprintf("line items invalid: %v", err))
		_ = p.repo.Save(ctx, invoice)
		p.publishEvents(ctx, invoice)
		return p.failResult(result, fmt.Errorf("building line items: %w", err))
	}

	totalMoney, err := domain.NewTHB(extracted.TotalAmountSatang())
	if err != nil {
		return p.failResult(result, fmt.Errorf("invalid total amount: %w", err))
	}

	if err := invoice.ApplyExtraction(
		extracted.VendorName,
		extracted.InvoiceDate,
		lineItems,
		totalMoney,
	); err != nil {
		p.metrics.ValidationErrors.Add(1)
		_ = invoice.Reject("system", fmt.Sprintf("extraction validation failed: %v", err))
		_ = p.repo.Save(ctx, invoice)
		p.publishEvents(ctx, invoice)
		return p.failResult(result, fmt.Errorf("applying extraction: %w", err))
	}

	// ── Step 6: Save Extracted state ──
	if err := p.repo.Save(ctx, invoice); err != nil {
		p.metrics.RepositoryErrors.Add(1)
		return p.failResult(result, fmt.Errorf("saving extracted invoice: %w", err))
	}

	// Publish InvoiceExtracted event
	p.publishEvents(ctx, invoice)

	// ── Step 7: Record metrics ──
	durationMs := time.Since(result.StartedAt).Milliseconds()
	p.metrics.InvoicesProcessed.Add(1)
	p.metrics.TotalProcessingMs.Add(durationMs)

	result.InvoiceID = invoiceID.String()
	result.Success = true
	result.CompletedAt = time.Now()

	log.Info("Invoice processed successfully",
		"invoice_id", result.InvoiceID,
		"vendor", extracted.VendorName,
		"total", extracted.TotalAmount,
		"duration_ms", durationMs,
	)

	return result
}

// ============================================================
// HELPERS
// ============================================================

func (p *InvoiceProcessor) failResult(result ProcessingResult, err error) ProcessingResult {
	p.metrics.InvoicesFailed.Add(1)
	result.Success = false
	result.Error = err
	result.CompletedAt = time.Now()
	p.logger.Error("Invoice processing failed",
		"raw_invoice_id", result.RawInvoice.ID,
		"error", err,
	)
	return result
}

// publishEvents ส่ง domain events และ clear uncommitted
func (p *InvoiceProcessor) publishEvents(ctx context.Context, invoice *domain.Invoice) {
	events := invoice.UncommittedEvents()
	if len(events) == 0 {
		return
	}
	if err := p.eventBus.Publish(ctx, events...); err != nil {
		p.logger.Error("Failed to publish events",
			"invoice_id", invoice.ID().String(),
			"error", err,
		)
	}
	invoice.ClearEvents()
}

// buildDomainLineItems แปลง ai.ExtractedLineItem เป็น domain.InvoiceLineItem
func (p *InvoiceProcessor) buildDomainLineItems(extracted *ai.ExtractedInvoiceData) ([]domain.InvoiceLineItem, error) {
	items := extracted.LineItemsSatang()
	result := make([]domain.InvoiceLineItem, 0, len(items))

	for _, item := range items {
		currency := item.Currency
		if currency == "" {
			currency = "THB"
		}
		price, err := domain.NewMoney(item.UnitPrice, currency)
		if err != nil {
			return nil, fmt.Errorf("invalid unit price for %q: %w", item.Description, err)
		}

		lineItem, err := domain.NewInvoiceLineItem(item.Description, item.Quantity, price)
		if err != nil {
			return nil, fmt.Errorf("invalid line item %q: %w", item.Description, err)
		}

		result = append(result, lineItem)
	}

	return result, nil
}
