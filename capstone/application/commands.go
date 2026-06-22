// Package application — CQRS Command/Query Handlers
// Application layer:
// - รับ input จาก transport layer (HTTP, gRPC)
// - ใช้ domain objects ทำ business logic
// - ไม่รู้จัก transport details (HTTP status codes, JSON marshaling)
// - ส่งผ่าน domain errors กลับไปยัง transport layer
package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tx_go_training/capstone/domain"
	"tx_go_training/capstone/processing"
)

// ============================================================
// COMMANDS — เปลี่ยน state (write side)
// ============================================================

// SubmitInvoiceCommand — input สำหรับการส่ง invoice เข้าระบบ
type SubmitInvoiceCommand struct {
	InvoiceID string // caller กำหนด ID (เช่น UUID) หรือระบบสร้างให้
	VendorID  string
	RawText   string // raw invoice text (Thai/English)
	Source    string // "api", "email", "upload"
}

// SubmitInvoiceResult — ผลลัพธ์จาก SubmitInvoice
type SubmitInvoiceResult struct {
	InvoiceID               string
	Status                  string
	VendorName              string  // จาก AI extraction
	ExtractedTotalAmount    float64 // จาก AI extraction
	Currency                string
	RequiresManagerApproval bool
	ProcessingDurationMs    int64
}

// ApproveInvoiceCommand — อนุมัติ invoice
type ApproveInvoiceCommand struct {
	InvoiceID  string
	ApprovedBy string
	IsManager  bool // manager flag สำหรับ invoice ยอดสูง
}

// RejectInvoiceCommand — ปฏิเสธ invoice
type RejectInvoiceCommand struct {
	InvoiceID  string
	RejectedBy string
	Reason     string
}

// ============================================================
// QUERIES — อ่านข้อมูล ไม่เปลี่ยน state (read side)
// ============================================================

// GetInvoiceQuery
type GetInvoiceQuery struct {
	InvoiceID string
}

// ListInvoicesQuery — pagination query
type ListInvoicesQuery struct {
	VendorID string // filter by vendor (optional)
	Status   string // filter by status (optional)
	Page     int    // 1-based
	PageSize int    // default 20
}

// ============================================================
// DTOs — Data Transfer Objects (สำหรับส่งออก transport layer)
// ============================================================

// InvoiceDTO — DTO สำหรับ API response
type InvoiceDTO struct {
	ID                      string        `json:"id"`
	VendorID                string        `json:"vendor_id"`
	VendorName              string        `json:"vendor_name"`
	Status                  string        `json:"status"`
	TotalAmount             float64       `json:"total_amount"`
	Currency                string        `json:"currency"`
	InvoiceDate             *time.Time    `json:"invoice_date,omitempty"`
	LineItems               []LineItemDTO `json:"line_items,omitempty"`
	RequiresManagerApproval bool          `json:"requires_manager_approval"`
	CreatedAt               time.Time     `json:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at"`
	Version                 int           `json:"version"`
}

type LineItemDTO struct {
	Description string  `json:"description"`
	Quantity    int64   `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	Subtotal    float64 `json:"subtotal"`
	Currency    string  `json:"currency"`
}

// ListInvoicesResult — paginated result
type ListInvoicesResult struct {
	Invoices   []InvoiceDTO `json:"invoices"`
	Total      int64        `json:"total"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
	TotalPages int          `json:"total_pages"`
}

// ============================================================
// COMMAND HANDLER
// ============================================================

// InvoiceCommandHandler — handles all invoice write operations
type InvoiceCommandHandler struct {
	repo      domain.InvoiceRepository
	eventBus  domain.EventBus
	processor *processing.InvoiceProcessor
	logger    *slog.Logger
}

func NewInvoiceCommandHandler(
	repo domain.InvoiceRepository,
	eventBus domain.EventBus,
	processor *processing.InvoiceProcessor,
	logger *slog.Logger,
) *InvoiceCommandHandler {
	return &InvoiceCommandHandler{
		repo:      repo,
		eventBus:  eventBus,
		processor: processor,
		logger:    logger,
	}
}

// HandleSubmitInvoice — Submit Invoice use case
// ส่ง invoice เข้า processing pipeline (Extract → Validate → Save)
func (h *InvoiceCommandHandler) HandleSubmitInvoice(
	ctx context.Context,
	cmd SubmitInvoiceCommand,
) (*SubmitInvoiceResult, error) {
	h.logger.Info("Submitting invoice",
		"invoice_id", cmd.InvoiceID,
		"vendor_id", cmd.VendorID,
		"source", cmd.Source,
	)

	raw := processing.RawInvoice{
		ID:         cmd.InvoiceID,
		VendorID:   cmd.VendorID,
		RawText:    cmd.RawText,
		Source:     cmd.Source,
		ReceivedAt: time.Now(),
	}

	result := h.processor.ProcessSingle(ctx, raw)
	if !result.Success {
		return nil, fmt.Errorf("processing invoice: %w", result.Error)
	}

	// Load updated invoice from repo
	invoiceID, _ := domain.NewInvoiceID(result.InvoiceID)
	invoice, err := h.repo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("loading invoice after processing: %w", err)
	}

	resp := &SubmitInvoiceResult{
		InvoiceID:               result.InvoiceID,
		Status:                  string(invoice.Status()),
		RequiresManagerApproval: invoice.RequiresManagerApproval(),
		ProcessingDurationMs:    result.Duration().Milliseconds(),
	}

	if result.Extracted != nil {
		resp.VendorName = result.Extracted.VendorName
		resp.ExtractedTotalAmount = result.Extracted.TotalAmount
		resp.Currency = result.Extracted.Currency
	}

	return resp, nil
}

// HandleApproveInvoice — Approve Invoice use case
func (h *InvoiceCommandHandler) HandleApproveInvoice(
	ctx context.Context,
	cmd ApproveInvoiceCommand,
) error {
	h.logger.Info("Approving invoice",
		"invoice_id", cmd.InvoiceID,
		"approved_by", cmd.ApprovedBy,
		"is_manager", cmd.IsManager,
	)

	// ── Load aggregate ──
	invoiceID, err := domain.NewInvoiceID(cmd.InvoiceID)
	if err != nil {
		return fmt.Errorf("invalid invoice ID: %w", err)
	}

	invoice, err := h.repo.FindByID(ctx, invoiceID)
	if err != nil {
		return fmt.Errorf("finding invoice: %w", err)
	}

	// ── Execute domain operation (business rules in domain) ──
	if err := invoice.Approve(cmd.ApprovedBy, cmd.IsManager); err != nil {
		return fmt.Errorf("approving invoice: %w", err)
	}

	// ── Save ──
	if err := h.repo.Save(ctx, invoice); err != nil {
		return fmt.Errorf("saving approved invoice: %w", err)
	}

	// ── Publish events ──
	if err := h.eventBus.Publish(ctx, invoice.UncommittedEvents()...); err != nil {
		h.logger.Error("Failed to publish approval events", "error", err)
	}
	invoice.ClearEvents()

	h.logger.Info("Invoice approved successfully",
		"invoice_id", cmd.InvoiceID,
		"approved_by", cmd.ApprovedBy,
	)
	return nil
}

// HandleRejectInvoice — Reject Invoice use case
func (h *InvoiceCommandHandler) HandleRejectInvoice(
	ctx context.Context,
	cmd RejectInvoiceCommand,
) error {
	h.logger.Info("Rejecting invoice",
		"invoice_id", cmd.InvoiceID,
		"rejected_by", cmd.RejectedBy,
	)

	invoiceID, err := domain.NewInvoiceID(cmd.InvoiceID)
	if err != nil {
		return fmt.Errorf("invalid invoice ID: %w", err)
	}

	invoice, err := h.repo.FindByID(ctx, invoiceID)
	if err != nil {
		return fmt.Errorf("finding invoice: %w", err)
	}

	if err := invoice.Reject(cmd.RejectedBy, cmd.Reason); err != nil {
		return fmt.Errorf("rejecting invoice: %w", err)
	}

	if err := h.repo.Save(ctx, invoice); err != nil {
		return fmt.Errorf("saving rejected invoice: %w", err)
	}

	if err := h.eventBus.Publish(ctx, invoice.UncommittedEvents()...); err != nil {
		h.logger.Error("Failed to publish rejection events", "error", err)
	}
	invoice.ClearEvents()

	return nil
}

// ============================================================
// QUERY HANDLER
// ============================================================

// InvoiceQueryHandler — handles all invoice read operations
type InvoiceQueryHandler struct {
	repo   domain.InvoiceRepository
	logger *slog.Logger
}

func NewInvoiceQueryHandler(
	repo domain.InvoiceRepository,
	logger *slog.Logger,
) *InvoiceQueryHandler {
	return &InvoiceQueryHandler{
		repo:   repo,
		logger: logger,
	}
}

// HandleGetInvoice — ดึง invoice เดียว
func (h *InvoiceQueryHandler) HandleGetInvoice(
	ctx context.Context,
	query GetInvoiceQuery,
) (*InvoiceDTO, error) {
	invoiceID, err := domain.NewInvoiceID(query.InvoiceID)
	if err != nil {
		return nil, fmt.Errorf("invalid invoice ID: %w", err)
	}

	invoice, err := h.repo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("finding invoice: %w", err)
	}

	return mapInvoiceToDTO(invoice), nil
}

// HandleListInvoices — ดึง invoices แบบ paginated
func (h *InvoiceQueryHandler) HandleListInvoices(
	ctx context.Context,
	query ListInvoicesQuery,
) (*ListInvoicesResult, error) {
	// Normalize pagination
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		query.PageSize = 20
	}
	offset := (query.Page - 1) * query.PageSize

	filter := domain.InvoiceFilter{
		Limit:  query.PageSize,
		Offset: offset,
	}

	// Apply optional filters
	if query.Status != "" {
		s := domain.InvoiceStatus(query.Status)
		filter.Status = &s
	}
	if query.VendorID != "" {
		vid, err := domain.NewVendorID(query.VendorID)
		if err == nil {
			filter.VendorID = &vid
		}
	}

	invoices, err := h.repo.FindAll(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("listing invoices: %w", err)
	}

	total, err := h.repo.Count(ctx, domain.InvoiceFilter{
		Status:   filter.Status,
		VendorID: filter.VendorID,
	})
	if err != nil {
		return nil, fmt.Errorf("counting invoices: %w", err)
	}

	dtos := make([]InvoiceDTO, 0, len(invoices))
	for _, inv := range invoices {
		dtos = append(dtos, *mapInvoiceToDTO(inv))
	}

	totalPages := int(total) / query.PageSize
	if int(total)%query.PageSize > 0 {
		totalPages++
	}

	return &ListInvoicesResult{
		Invoices:   dtos,
		Total:      total,
		Page:       query.Page,
		PageSize:   query.PageSize,
		TotalPages: totalPages,
	}, nil
}

// ============================================================
// MAPPER — domain → DTO
// ============================================================

// mapInvoiceToDTO แปลง domain Invoice เป็น DTO (ไม่มี business logic)
func mapInvoiceToDTO(inv *domain.Invoice) *InvoiceDTO {
	dto := &InvoiceDTO{
		ID:                      inv.ID().String(),
		VendorID:                inv.VendorID().String(),
		VendorName:              inv.VendorName(),
		Status:                  string(inv.Status()),
		TotalAmount:             inv.TotalAmount().Baht(),
		Currency:                inv.Currency(),
		RequiresManagerApproval: inv.RequiresManagerApproval(),
		CreatedAt:               inv.CreatedAt(),
		UpdatedAt:               inv.UpdatedAt(),
		Version:                 inv.Version(),
	}

	// ใส่ invoice date เฉพาะตอน extracted แล้ว
	if inv.Status() != domain.InvoiceStatusReceived && inv.Status() != domain.InvoiceStatusProcessing {
		if !inv.InvoiceDate().IsZero() {
			t := inv.InvoiceDate()
			dto.InvoiceDate = &t
		}
	}

	// ถ้า currency ยังว่าง (before extraction)
	if dto.Currency == "" {
		dto.Currency = "THB"
	}

	// Map line items
	domainLineItems := inv.LineItems()
	if len(domainLineItems) > 0 {
		dto.LineItems = make([]LineItemDTO, len(domainLineItems))
		for i, li := range domainLineItems {
			dto.LineItems[i] = LineItemDTO{
				Description: li.Description(),
				Quantity:    li.Quantity(),
				UnitPrice:   li.UnitPrice().Baht(),
				Subtotal:    li.Subtotal().Baht(),
				Currency:    li.UnitPrice().Currency(),
			}
		}
	}

	return dto
}
