// Package main — Smart Invoice Processing Service
// Capstone Project: Advanced Go Enterprise + AI Integration
//
// โปรแกรมนี้รวม concepts จากทุก module:
//
//	Module 2: Concurrency (Worker Pool)
//	Module 3: DDD (Invoice Aggregate + Repository Pattern)
//	Module 4: CQRS (Command/Query Separation)
//	Module 7: AI Integration (Invoice Extraction)
//	Module 8: Observability (Metrics + Structured Logging)
//
// วิธีรัน:
//
//	go run ./capstone/
//
// Configuration (environment variables):
//
//	SERVER_ADDR=:8080    (default: :8080)
//	LOG_LEVEL=info       (default: info)
//	LOG_FORMAT=json      (default: json)
//	WORKER_COUNT=5       (default: 5)
//	ENVIRONMENT=development (default: development)
//	OPENAI_API_KEY=...   (optional: ใช้ MockExtractor ถ้าไม่มี)
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"tx_go_training/capstone/ai"
	"tx_go_training/capstone/api"
	"tx_go_training/capstone/application"
	"tx_go_training/capstone/domain"
	"tx_go_training/capstone/infrastructure"
	"tx_go_training/capstone/observability"
	"tx_go_training/capstone/processing"
)

func main() {
	// ── 1. Observability (ต้อง init ก่อนสุด) ──
	obsCfg := observability.Config{
		LogLevel:       envOrDefault("LOG_LEVEL", "info"),
		LogFormat:      envOrDefault("LOG_FORMAT", "json"),
		MetricsEnabled: true,
		ServiceName:    "smart-invoice-processor",
		Environment:    envOrDefault("ENVIRONMENT", "development"),
	}
	obs, cleanup := observability.InitObservability(obsCfg)
	defer cleanup()

	logger := obs.Logger
	logger.Info("Smart Invoice Processing Service starting",
		"version", "1.0.0",
		"environment", obsCfg.Environment,
	)

	// ── 2. Infrastructure: Repository + EventBus ──
	repo := infrastructure.NewInMemoryInvoiceRepository()
	eventBus := infrastructure.NewInMemoryEventBus()

	// Register domain event handlers (logging)
	eventBus.Subscribe("*", func(ctx context.Context, event domain.DomainEvent) error {
		logger.Info("Domain event",
			"event", event.EventName(),
			"aggregate_id", event.AggregateID(),
			"occurred_at", event.OccurredAt(),
		)
		return nil
	})

	// ── 3. AI Extractor ──
	// ถ้ามี OPENAI_API_KEY ใช้ RealExtractor, ถ้าไม่มีใช้ MockExtractor
	var extractor ai.InvoiceExtractor
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		logger.Info("Using OpenAI extractor", "model", "gpt-4o-mini")
		extractor = ai.NewOpenAIExtractor(ai.OpenAIExtractorConfig{
			APIKey:    apiKey,
			Model:     "gpt-4o-mini",
			MaxTokens: 2000,
		}, logger)
	} else {
		logger.Info("Using Mock extractor (set OPENAI_API_KEY for real AI extraction)")
		mockExtractor := ai.NewMockExtractor(logger)
		mockExtractor.SimulateDelay = 100 * time.Millisecond // เร็วขึ้นสำหรับ demo
		extractor = mockExtractor
	}

	// ── 4. Processing: Worker Pool ──
	workerCount := envIntOrDefault("WORKER_COUNT", 5)
	processor := processing.NewInvoiceProcessor(
		extractor,
		repo,
		eventBus,
		logger,
		processing.WithWorkerCount(workerCount),
	)
	logger.Info("Invoice processor ready", "workers", workerCount)

	// ── 5. Application: Command/Query Handlers ──
	cmdHandler := application.NewInvoiceCommandHandler(repo, eventBus, processor, logger)
	queryHandler := application.NewInvoiceQueryHandler(repo, logger)

	// ── 6. Pre-load Sample Data ──
	logger.Info("Loading sample invoices for demo...")
	if err := preloadSampleInvoices(context.Background(), processor, repo); err != nil {
		logger.Error("Failed to preload sample invoices", "error", err)
		// ไม่ fatal — server ยังรันได้
	}

	// ── 7. HTTP API Server ──
	serverCfg := api.Config{
		Addr:         envOrDefault("SERVER_ADDR", ":8080"),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	server := api.NewServer(cmdHandler, queryHandler, processor, obs, serverCfg)

	// ── 8. Graceful Shutdown ──
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	// Start server ใน goroutine
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening",
			"addr", serverCfg.Addr,
			"endpoints", []string{
				"POST /invoices",
				"POST /invoices/batch",
				"GET  /invoices",
				"GET  /invoices/{id}",
				"POST /invoices/{id}/approve",
				"POST /invoices/{id}/reject",
				"GET  /metrics",
				"GET  /health",
			},
		)
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// รอ signal
	select {
	case err := <-serverErr:
		logger.Error("Server error", "error", err)
		os.Exit(1)
	case sig := <-shutdownCh:
		logger.Info("Received shutdown signal", "signal", sig)
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger.Info("Shutting down gracefully...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Shutdown error", "error", err)
	}

	// Log final metrics
	finalMetrics := obs.Metrics.Snapshot()
	logger.Info("Final metrics", "metrics", finalMetrics)

	logger.Info("Goodbye! สวัสดี!")
}

// ============================================================
// SAMPLE DATA PRELOADER
// ============================================================

// preloadSampleInvoices โหลด invoice ตัวอย่าง 5 ใบสำหรับ demo
// สามารถดู invoices เหล่านี้ได้ทันที
func preloadSampleInvoices(ctx context.Context, processor *processing.InvoiceProcessor, repo domain.InvoiceRepository) error {
	sampleInvoices := []processing.RawInvoice{
		{
			ID:         "INV-SAMPLE-001",
			VendorID:   "VENDOR-ABC-TECH",
			RawText:    domain.SampleInvoice1,
			Source:     "preload",
			ReceivedAt: time.Now().Add(-4 * 24 * time.Hour),
		},
		{
			ID:         "INV-SAMPLE-002",
			VendorID:   "VENDOR-SIAM-OFFICE",
			RawText:    domain.SampleInvoice2,
			Source:     "preload",
			ReceivedAt: time.Now().Add(-3 * 24 * time.Hour),
		},
		{
			ID:         "INV-SAMPLE-003",
			VendorID:   "VENDOR-THAI-CLOUD",
			RawText:    domain.SampleInvoice3,
			Source:     "preload",
			ReceivedAt: time.Now().Add(-2 * 24 * time.Hour),
		},
		{
			ID:         "INV-SAMPLE-004",
			VendorID:   "VENDOR-CHEFBOM",
			RawText:    domain.SampleInvoice4,
			Source:     "preload",
			ReceivedAt: time.Now().Add(-1 * 24 * time.Hour),
		},
		{
			ID:         "INV-SAMPLE-005",
			VendorID:   "VENDOR-CREATIVE-MEDIA",
			RawText:    domain.SampleInvoice5,
			Source:     "preload",
			ReceivedAt: time.Now(),
		},
	}

	// Process สำหรับ 4 invoices ด้วย batch (concurrent)
	batchResult := processor.ProcessBatch(ctx, sampleInvoices[:4])

	successCount := 0
	for _, r := range batchResult.Results {
		if r.Success {
			successCount++
		} else {
			log.Printf("WARN: sample invoice %s failed: %v", r.RawInvoice.ID, r.Error)
		}
	}

	// Process invoice ที่ 5 แยก + auto-approve สำหรับ demo
	result5 := processor.ProcessSingle(ctx, sampleInvoices[4])
	if result5.Success {
		successCount++
		// Auto-approve invoice 5 สำหรับ demo
		invoiceID, _ := domain.NewInvoiceID("INV-SAMPLE-005")
		if inv, err := repo.FindByID(ctx, invoiceID); err == nil {
			if inv.Status() == domain.InvoiceStatusExtracted {
				if err := inv.Approve("demo-admin", false); err == nil {
					_ = repo.Save(ctx, inv)
				}
			}
		}
	}

	fmt.Printf("Preloaded %d/%d sample invoices\n", successCount, len(sampleInvoices))

	// Auto-approve invoice 1 สำหรับ demo variety
	if batchResult.Results[0].Success {
		invoiceID, _ := domain.NewInvoiceID("INV-SAMPLE-001")
		if inv, err := repo.FindByID(ctx, invoiceID); err == nil {
			if inv.Status() == domain.InvoiceStatusExtracted {
				_ = inv.Approve("demo-manager", true)
				_ = repo.Save(ctx, inv)
			}
		}
	}

	return nil
}

// ============================================================
// HELPERS
// ============================================================

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func envIntOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
