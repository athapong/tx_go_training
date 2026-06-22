// Package api — HTTP API Server
// RESTful API สำหรับ Smart Invoice Processing Service
//
// Endpoints:
//
//	POST /invoices              → submit single invoice
//	POST /invoices/batch        → submit batch (processed concurrently)
//	GET  /invoices              → list invoices (paginated)
//	GET  /invoices/{id}         → get invoice by ID
//	POST /invoices/{id}/approve → approve invoice
//	POST /invoices/{id}/reject  → reject invoice
//	GET  /metrics               → observability metrics
//	GET  /health                → health check
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tx_go_training/capstone/application"
	"tx_go_training/capstone/domain"
	"tx_go_training/capstone/observability"
	"tx_go_training/capstone/processing"
)

// ============================================================
// SERVER
// ============================================================

// Server — HTTP API server
type Server struct {
	cmdHandler   *application.InvoiceCommandHandler
	queryHandler *application.InvoiceQueryHandler
	processor    *processing.InvoiceProcessor
	obs          *observability.Observability
	logger       *slog.Logger
	mux          *http.ServeMux
	httpServer   *http.Server
}

// Config — server configuration
type Config struct {
	Addr         string // e.g. ":8080"
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func DefaultConfig() Config {
	return Config{
		Addr:         envOrDefault("SERVER_ADDR", ":8080"),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// NewServer สร้าง HTTP server พร้อม routes
func NewServer(
	cmdHandler *application.InvoiceCommandHandler,
	queryHandler *application.InvoiceQueryHandler,
	processor *processing.InvoiceProcessor,
	obs *observability.Observability,
	cfg Config,
) *Server {
	s := &Server{
		cmdHandler:   cmdHandler,
		queryHandler: queryHandler,
		processor:    processor,
		obs:          obs,
		logger:       obs.Logger,
	}

	s.mux = http.NewServeMux()
	s.registerRoutes()

	// Build middleware chain
	var handler http.Handler = s.mux
	handler = observability.RequestIDMiddleware(handler)
	handler = observability.LoggingMiddleware(s.logger, obs.Metrics)(handler)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return s
}

// registerRoutes ลงทะเบียน routes ทั้งหมด
func (s *Server) registerRoutes() {
	// Health + Metrics
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /metrics", observability.SimpleMetricsHandler(s.obs.Metrics))

	// Invoice CRUD
	s.mux.HandleFunc("POST /invoices", s.handleSubmitInvoice)
	s.mux.HandleFunc("POST /invoices/batch", s.handleSubmitBatch)
	s.mux.HandleFunc("GET /invoices", s.handleListInvoices)
	s.mux.HandleFunc("GET /invoices/{id}", s.handleGetInvoice)
	s.mux.HandleFunc("POST /invoices/{id}/approve", s.handleApproveInvoice)
	s.mux.HandleFunc("POST /invoices/{id}/reject", s.handleRejectInvoice)
}

// Start เริ่ม HTTP server (blocking)
func (s *Server) Start() error {
	s.logger.Info("Starting HTTP server", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown graceful shutdown
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}

// ============================================================
// REQUEST / RESPONSE TYPES
// ============================================================

// SubmitInvoiceRequest — POST /invoices body
type SubmitInvoiceRequest struct {
	InvoiceID string `json:"invoice_id"` // caller-generated ID (UUID recommended)
	VendorID  string `json:"vendor_id"`
	RawText   string `json:"raw_text"` // raw invoice text
	Source    string `json:"source"`   // "api", "email", "upload"
}

// SubmitBatchRequest — POST /invoices/batch body
type SubmitBatchRequest struct {
	Invoices []SubmitInvoiceRequest `json:"invoices"`
}

// ApproveRequest — POST /invoices/{id}/approve body
type ApproveRequest struct {
	ApprovedBy string `json:"approved_by"`
	IsManager  bool   `json:"is_manager"`
}

// RejectRequest — POST /invoices/{id}/reject body
type RejectRequest struct {
	RejectedBy string `json:"rejected_by"`
	Reason     string `json:"reason"`
}

// BatchSubmitResult — response สำหรับ batch submit
type BatchSubmitResult struct {
	TotalCount   int                  `json:"total_count"`
	SuccessCount int                  `json:"success_count"`
	FailureCount int                  `json:"failure_count"`
	DurationMs   int64                `json:"duration_ms"`
	Results      []SingleSubmitResult `json:"results"`
}

// SingleSubmitResult — ผลลัพธ์ของ invoice เดียวใน batch
type SingleSubmitResult struct {
	RawInvoiceID string `json:"raw_invoice_id"`
	InvoiceID    string `json:"invoice_id,omitempty"`
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
	DurationMs   int64  `json:"duration_ms"`
}

// APIError — standard error response
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HealthResponse
type HealthResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Metrics   map[string]interface{} `json:"metrics"`
}

// ============================================================
// HANDLERS
// ============================================================

// handleHealth — GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Metrics:   s.obs.Metrics.Snapshot(),
	})
}

// handleSubmitInvoice — POST /invoices
// รับ raw invoice text → ส่ง AI extraction pipeline → ส่ง result กลับ
func (s *Server) handleSubmitInvoice(w http.ResponseWriter, r *http.Request) {
	var req SubmitInvoiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validateSubmitRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	cmd := application.SubmitInvoiceCommand{
		InvoiceID: req.InvoiceID,
		VendorID:  req.VendorID,
		RawText:   req.RawText,
		Source:    req.Source,
	}

	result, err := s.cmdHandler.HandleSubmitInvoice(r.Context(), cmd)
	if err != nil {
		s.logger.Error("Submit invoice failed",
			"invoice_id", req.InvoiceID,
			"error", err,
		)
		statusCode := httpStatusFromError(err)
		writeError(w, statusCode, "PROCESSING_ERROR", err.Error())
		return
	}

	s.obs.Metrics.InvoicesProcessed.Inc()

	writeJSON(w, http.StatusCreated, result)
}

// handleSubmitBatch — POST /invoices/batch
// ประมวลผล invoices หลายใบพร้อมกันด้วย worker pool
func (s *Server) handleSubmitBatch(w http.ResponseWriter, r *http.Request) {
	var req SubmitBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if len(req.Invoices) == 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "batch must contain at least one invoice")
		return
	}
	if len(req.Invoices) > 100 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "batch cannot exceed 100 invoices")
		return
	}

	// แปลง request เป็น RawInvoice
	rawInvoices := make([]processing.RawInvoice, len(req.Invoices))
	for i, inv := range req.Invoices {
		rawInvoices[i] = processing.RawInvoice{
			ID:         inv.InvoiceID,
			VendorID:   inv.VendorID,
			RawText:    inv.RawText,
			Source:     inv.Source,
			ReceivedAt: time.Now(),
		}
	}

	// ProcessBatch ใช้ worker pool internally
	batchResult := s.processor.ProcessBatch(r.Context(), rawInvoices)

	// Map results
	results := make([]SingleSubmitResult, len(batchResult.Results))
	for i, pr := range batchResult.Results {
		r := SingleSubmitResult{
			RawInvoiceID: pr.RawInvoice.ID,
			InvoiceID:    pr.InvoiceID,
			Success:      pr.Success,
			DurationMs:   pr.Duration().Milliseconds(),
		}
		if pr.Error != nil {
			r.Error = pr.Error.Error()
		}
		results[i] = r
	}

	writeJSON(w, http.StatusOK, BatchSubmitResult{
		TotalCount:   batchResult.TotalCount,
		SuccessCount: batchResult.SuccessCount,
		FailureCount: batchResult.FailureCount,
		DurationMs:   batchResult.TotalDuration.Milliseconds(),
		Results:      results,
	})
}

// handleGetInvoice — GET /invoices/{id}
func (s *Server) handleGetInvoice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "invoice ID is required")
		return
	}

	result, err := s.queryHandler.HandleGetInvoice(r.Context(), application.GetInvoiceQuery{
		InvoiceID: id,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("invoice %s not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleListInvoices — GET /invoices?page=1&page_size=20&status=EXTRACTED&vendor_id=V001
func (s *Server) handleListInvoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := application.ListInvoicesQuery{
		VendorID: q.Get("vendor_id"),
		Status:   q.Get("status"),
		Page:     parseIntOrDefault(q.Get("page"), 1),
		PageSize: parseIntOrDefault(q.Get("page_size"), 20),
	}

	result, err := s.queryHandler.HandleListInvoices(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleApproveInvoice — POST /invoices/{id}/approve
func (s *Server) handleApproveInvoice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "invoice ID is required")
		return
	}

	var req ApproveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if req.ApprovedBy == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "approved_by is required")
		return
	}

	err := s.cmdHandler.HandleApproveInvoice(r.Context(), application.ApproveInvoiceCommand{
		InvoiceID:  id,
		ApprovedBy: req.ApprovedBy,
		IsManager:  req.IsManager,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("invoice %s not found", id))
			return
		}
		statusCode := httpStatusFromError(err)
		writeError(w, statusCode, "APPROVAL_ERROR", err.Error())
		return
	}

	s.obs.Metrics.InvoicesApproved.Inc()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "approved",
		"message": fmt.Sprintf("Invoice %s approved successfully", id),
	})
}

// handleRejectInvoice — POST /invoices/{id}/reject
func (s *Server) handleRejectInvoice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "invoice ID is required")
		return
	}

	var req RejectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "reason is required")
		return
	}
	if req.RejectedBy == "" {
		req.RejectedBy = "api-user"
	}

	err := s.cmdHandler.HandleRejectInvoice(r.Context(), application.RejectInvoiceCommand{
		InvoiceID:  id,
		RejectedBy: req.RejectedBy,
		Reason:     req.Reason,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("invoice %s not found", id))
			return
		}
		statusCode := httpStatusFromError(err)
		writeError(w, statusCode, "REJECTION_ERROR", err.Error())
		return
	}

	s.obs.Metrics.InvoicesRejected.Inc()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "rejected",
		"message": fmt.Sprintf("Invoice %s rejected", id),
	})
}

// ============================================================
// HELPERS
// ============================================================

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, APIError{Code: code, Message: message})
}

func decodeJSON(r *http.Request, v interface{}) error {
	if r.ContentLength > 10*1024*1024 { // 10MB limit
		return fmt.Errorf("request body too large")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func validateSubmitRequest(req SubmitInvoiceRequest) error {
	if req.InvoiceID == "" {
		return fmt.Errorf("invoice_id is required")
	}
	if req.VendorID == "" {
		return fmt.Errorf("vendor_id is required")
	}
	if strings.TrimSpace(req.RawText) == "" {
		return fmt.Errorf("raw_text is required")
	}
	if len(req.RawText) > 50000 {
		return fmt.Errorf("raw_text exceeds maximum length of 50000 characters")
	}
	return nil
}

// httpStatusFromError แปลง domain error เป็น HTTP status
func httpStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrVersionConflict):
		return http.StatusConflict
	case errors.Is(err, domain.ErrDuplicateInvoice):
		return http.StatusConflict
	case errors.Is(err, domain.ErrInvalidStatus):
		return http.StatusUnprocessableEntity
	default:
		// ตรวจ business rule messages
		if isBusinessRuleError(err) {
			return http.StatusUnprocessableEntity
		}
		return http.StatusInternalServerError
	}
}

// isBusinessRuleError ตรวจว่าเป็น business rule violation
func isBusinessRuleError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	businessPhrases := []string{
		"cannot approve",
		"cannot reject",
		"requires manager approval",
		"must extract first",
		"cannot add",
		"mismatch",
	}
	for _, phrase := range businessPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 1 {
		return defaultVal
	}
	return n
}

func envOrDefault(key, defaultVal string) string {
	_ = key
	// Production: ใช้ os.Getenv(key) โดยตรงจาก main.go config
	// หรือ inject Config struct ผ่าน constructor
	return defaultVal
}
