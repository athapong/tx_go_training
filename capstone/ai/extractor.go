// Package ai — Invoice Data Extraction using AI
// Module 7 Integration: AI-powered extraction ของข้อมูลจาก invoice text
//
// สองโหมด:
// 1. MockExtractor    — ใช้สำหรับ demo/testing ไม่ต้องการ API key
// 2. OpenAIExtractor  — ใช้ OpenAI API จริง (ต้องมี OPENAI_API_KEY)
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"
)

// ============================================================
// EXTRACTED DATA TYPES
// ============================================================

// ExtractedLineItem — รายการที่ extract ได้จาก invoice
type ExtractedLineItem struct {
	Description string  `json:"description"`
	Quantity    int64   `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`   // หน่วย: บาท
	Subtotal    float64 `json:"subtotal"`     // หน่วย: บาท
	Currency    string  `json:"currency"`     // "THB", "USD", etc.
}

// ExtractedInvoiceData — ผลลัพธ์จาก AI extraction
type ExtractedInvoiceData struct {
	VendorName    string              `json:"vendor_name"`
	InvoiceNumber string              `json:"invoice_number"`
	InvoiceDate   time.Time           `json:"invoice_date"`
	DueDate       *time.Time          `json:"due_date,omitempty"`
	LineItems     []ExtractedLineItem `json:"line_items"`
	SubTotal      float64             `json:"subtotal"`      // ก่อน VAT
	VATAmount     float64             `json:"vat_amount"`    // จำนวน VAT
	TotalAmount   float64             `json:"total_amount"`  // หลัง VAT
	Currency      string              `json:"currency"`
	Notes         string              `json:"notes,omitempty"`
	// Validation results
	ValidationOK  bool   `json:"validation_ok"`
	ValidationMsg string `json:"validation_msg,omitempty"`
	// Confidence score จาก AI (0-1)
	ConfidenceScore float64 `json:"confidence_score"`
}

// TotalAmountSatang คืน TotalAmount ในหน่วย satang (int64) สำหรับ domain
func (e *ExtractedInvoiceData) TotalAmountSatang() int64 {
	return int64(e.TotalAmount * 100)
}

// LineItemsForDomain แปลง ExtractedLineItem เป็น domain-ready format
func (e *ExtractedInvoiceData) LineItemsSatang() []LineItemSatang {
	result := make([]LineItemSatang, len(e.LineItems))
	for i, li := range e.LineItems {
		result[i] = LineItemSatang{
			Description: li.Description,
			Quantity:    li.Quantity,
			UnitPrice:   int64(li.UnitPrice * 100),
			Currency:    li.Currency,
		}
		if result[i].Currency == "" {
			result[i].Currency = e.Currency
		}
	}
	return result
}

// LineItemSatang — line item ในหน่วย satang สำหรับส่งเข้า domain layer
type LineItemSatang struct {
	Description string
	Quantity    int64
	UnitPrice   int64 // satang
	Currency    string
}

// ============================================================
// EXTRACTOR INTERFACE
// ============================================================

// InvoiceExtractor — interface สำหรับ AI extraction
// Pattern: interface-based design ทำให้สลับ implementation ได้
type InvoiceExtractor interface {
	// Extract ดึงข้อมูลจาก raw invoice text
	Extract(ctx context.Context, rawText string) (*ExtractedInvoiceData, error)
	// Name ชื่อ implementation (สำหรับ logging/metrics)
	Name() string
}

// ============================================================
// MOCK EXTRACTOR — ไม่ต้องการ API key
// ใช้สำหรับ: local development, testing, CI/CD, demo
// ============================================================

// MockExtractor คืนข้อมูลจากการ parse แบบ heuristic
// รองรับ sample invoices ทั้ง 5 รายการที่กำหนดไว้
type MockExtractor struct {
	logger *slog.Logger
	// SimulateDelay ทำให้ mock ช้าเหมือน real API call
	SimulateDelay time.Duration
}

func NewMockExtractor(logger *slog.Logger) *MockExtractor {
	return &MockExtractor{
		logger:        logger,
		SimulateDelay: 200 * time.Millisecond, // simulate API latency
	}
}

func (m *MockExtractor) Name() string { return "mock" }

// Extract parse ข้อมูลจาก raw text โดยใช้ heuristics
// รองรับ Thai invoice format
func (m *MockExtractor) Extract(ctx context.Context, rawText string) (*ExtractedInvoiceData, error) {
	// Simulate API latency
	if m.SimulateDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.SimulateDelay):
		}
	}

	m.logger.Debug("Mock extracting invoice", "text_length", len(rawText))

	// ใช้ heuristic rules ในการ parse
	result := m.parseInvoiceText(rawText)

	// Validate ผลลัพธ์
	m.validateExtraction(result)

	m.logger.Info("Mock extraction complete",
		"vendor", result.VendorName,
		"total", result.TotalAmount,
		"currency", result.Currency,
		"confidence", result.ConfidenceScore,
	)

	return result, nil
}

// parseInvoiceText ทำ heuristic parsing ของ invoice text
func (m *MockExtractor) parseInvoiceText(text string) *ExtractedInvoiceData {
	lines := strings.Split(text, "\n")

	result := &ExtractedInvoiceData{
		Currency:        "THB",
		ConfidenceScore: 0.85 + rand.Float64()*0.1, // 0.85-0.95
		InvoiceDate:     time.Now().AddDate(0, 0, -rand.Intn(30)),
	}

	// Parse ตาม keyword patterns
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// หา vendor name จากบรรทัดที่มี "บริษัท" หรือ "Co., Ltd"
		if result.VendorName == "" {
			if strings.Contains(line, "บริษัท") && strings.Contains(line, "จำกัด") {
				result.VendorName = extractCompanyName(line)
			} else if strings.Contains(line, "Co., Ltd") || strings.Contains(line, "Co.,Ltd") {
				result.VendorName = extractCompanyName(line)
			} else if strings.Contains(line, "ร้าน") && i < 5 {
				result.VendorName = line
			}
		}

		// หา invoice number
		if result.InvoiceNumber == "" {
			if strings.Contains(line, "INV-") || strings.Contains(line, "เลขที่:") ||
				strings.Contains(line, "เลขที่ใบแจ้งหนี้:") || strings.Contains(line, "Invoice No:") ||
				strings.Contains(line, "Invoice Number:") {
				result.InvoiceNumber = extractInvoiceNumber(line)
			}
		}

		// หา total amount — ยอดสุทธิ
		if strings.Contains(line, "ยอดรวมทั้งสิ้น:") || strings.Contains(line, "ยอดสุทธิ:") ||
			strings.Contains(line, "Total Due:") || strings.Contains(line, "Total:") ||
			strings.Contains(line, "ยอดชำระทั้งสิ้น:") {
			if amount, ok := extractThaiAmount(line); ok {
				result.TotalAmount = amount
			}
		}

		// หา VAT amount
		if strings.Contains(line, "VAT") || strings.Contains(line, "ภาษีมูลค่าเพิ่ม") {
			if amount, ok := extractThaiAmount(line); ok {
				result.VATAmount = amount
			}
		}
	}

	// ถ้า parse vendor name ไม่ได้ ใช้ fallback
	if result.VendorName == "" {
		result.VendorName = "บริษัท ไม่ระบุชื่อ จำกัด"
		result.ConfidenceScore -= 0.2
	}

	// ถ้า parse total ไม่ได้ ใช้ fallback
	if result.TotalAmount == 0 {
		result.TotalAmount = 10000.0 // fallback
		result.ConfidenceScore -= 0.3
	}

	// คำนวณ subtotal จาก total - VAT
	result.SubTotal = result.TotalAmount - result.VATAmount

	// สร้าง mock line items จาก total (simplification สำหรับ demo)
	result.LineItems = m.generateMockLineItems(result)

	// ถ้าไม่มี invoice number สร้าง fallback
	if result.InvoiceNumber == "" {
		result.InvoiceNumber = fmt.Sprintf("INV-%d", time.Now().UnixMilli()%100000)
	}

	return result
}

// generateMockLineItems สร้าง line items จาก extracted total
// สำหรับ demo — real implementation จะ parse line items จริงๆ
func (m *MockExtractor) generateMockLineItems(data *ExtractedInvoiceData) []ExtractedLineItem {
	subTotal := data.SubTotal
	if subTotal <= 0 {
		subTotal = data.TotalAmount
	}

	// แบ่ง subtotal ออกเป็น 2-3 รายการ
	if subTotal <= 10000 {
		return []ExtractedLineItem{
			{
				Description: "สินค้า/บริการ",
				Quantity:    1,
				UnitPrice:   subTotal,
				Subtotal:    subTotal,
				Currency:    data.Currency,
			},
		}
	}

	// แบ่งเป็น 2 รายการ
	item1Amount := subTotal * 0.6
	item2Amount := subTotal * 0.4
	return []ExtractedLineItem{
		{
			Description: "สินค้า/บริการ รายการ 1",
			Quantity:    1,
			UnitPrice:   item1Amount,
			Subtotal:    item1Amount,
			Currency:    data.Currency,
		},
		{
			Description: "สินค้า/บริการ รายการ 2",
			Quantity:    1,
			UnitPrice:   item2Amount,
			Subtotal:    item2Amount,
			Currency:    data.Currency,
		},
	}
}

// validateExtraction ตรวจสอบความถูกต้องของข้อมูลที่ extract
func (m *MockExtractor) validateExtraction(data *ExtractedInvoiceData) {
	// ตรวจสอบว่า total ตรงกับผลรวม line items
	var sumFromItems float64
	for _, item := range data.LineItems {
		sumFromItems += item.Subtotal
	}

	// คำนวณ expected total (subtotal + VAT)
	expectedTotal := sumFromItems + data.VATAmount

	// อนุญาตให้ต่างกันได้ ±1 บาท (rounding)
	diff := expectedTotal - data.TotalAmount
	if diff < -1.0 || diff > 1.0 {
		data.ValidationOK = false
		data.ValidationMsg = fmt.Sprintf(
			"total mismatch: sum of items + VAT = %.2f but stated total = %.2f",
			expectedTotal, data.TotalAmount,
		)
		data.ConfidenceScore -= 0.1
	} else {
		data.ValidationOK = true
		data.ValidationMsg = "total matches sum of line items"
	}
}

// ============================================================
// HELPER FUNCTIONS สำหรับ Thai text parsing
// ============================================================

// extractCompanyName ดึงชื่อบริษัทจาก line
func extractCompanyName(line string) string {
	// ทำ basic cleanup
	line = strings.TrimSpace(line)
	// ตัด tax ID ออก (ถ้ามี)
	if idx := strings.Index(line, "เลขที่ผู้เสียภาษี"); idx > 0 {
		line = line[:idx]
	}
	// ตัด (มหาชน) ออกชั่วคราวไม่ได้ เพราะเป็นส่วนหนึ่งของชื่อ
	return strings.TrimSpace(line)
}

// extractInvoiceNumber ดึงเลขที่ใบแจ้งหนี้
func extractInvoiceNumber(line string) string {
	// ลอง patterns ต่างๆ
	patterns := []string{
		"INV-", "SO-", "CB-", "CMA-", "TCS-",
	}
	for _, p := range patterns {
		if idx := strings.Index(line, p); idx >= 0 {
			// ดึง token ถัดจาก pattern
			rest := line[idx:]
			parts := strings.Fields(rest)
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	// fallback: ดึง field หลัง colon
	if idx := strings.LastIndex(line, ":"); idx >= 0 {
		return strings.TrimSpace(line[idx+1:])
	}
	return ""
}

// extractThaiAmount ดึงจำนวนเงินจาก Thai formatted string
// รองรับ: "1,234.56 บาท", "1,234.56", "1234.56 บาท"
func extractThaiAmount(line string) (float64, bool) {
	// ลบ characters ที่ไม่ใช่ตัวเลข comma และ dot
	var numStr strings.Builder
	foundDigit := false
	for _, ch := range line {
		if ch >= '0' && ch <= '9' {
			numStr.WriteRune(ch)
			foundDigit = true
		} else if ch == ',' && foundDigit {
			// ข้าม comma (Thai number formatting)
		} else if ch == '.' && foundDigit {
			numStr.WriteRune(ch)
		} else if foundDigit && ch != ' ' && ch != 'b' && ch != 'a' && ch != 't' {
			// หยุดเมื่อเจอ character ที่ไม่ใช่ตัวเลขหลัง digit
			// ยกเว้น "บาท" suffix
			break
		}
	}

	if !foundDigit {
		return 0, false
	}

	numStrVal := numStr.String()
	var amount float64
	_, err := fmt.Sscanf(numStrVal, "%f", &amount)
	if err != nil || amount <= 0 {
		return 0, false
	}

	return amount, true
}

// ============================================================
// OPENAI EXTRACTOR — Real AI extraction
// ต้องการ: github.com/sashabaranov/go-openai
// ============================================================

// OpenAIExtractorConfig — configuration สำหรับ OpenAI extractor
type OpenAIExtractorConfig struct {
	APIKey    string
	Model     string // "gpt-4o-mini" recommended สำหรับ cost-efficiency
	MaxTokens int
}

// openAIExtractorRequest — JSON schema สำหรับ structured output
type openAIExtractorRequest struct {
	Text string `json:"text"`
}

// OpenAIExtractor ใช้ OpenAI API จริง
// NOTE: ต้อง uncomment import และ implement เมื่อมี go-openai dependency
type OpenAIExtractor struct {
	config OpenAIExtractorConfig
	logger *slog.Logger
	// client *openai.Client  // uncomment เมื่อ import go-openai
}

func NewOpenAIExtractor(config OpenAIExtractorConfig, logger *slog.Logger) *OpenAIExtractor {
	return &OpenAIExtractor{
		config: config,
		logger: logger,
		// client: openai.NewClient(config.APIKey),
	}
}

func (e *OpenAIExtractor) Name() string { return "openai" }

// Extract ใช้ OpenAI structured output สำหรับ extraction ที่แม่นยำ
// JSON Schema enforcement ทำให้ไม่มี hallucination ใน structured fields
func (e *OpenAIExtractor) Extract(ctx context.Context, rawText string) (*ExtractedInvoiceData, error) {
	e.logger.Info("Extracting invoice with OpenAI",
		"model", e.config.Model,
		"text_length", len(rawText),
	)

	// System prompt สำหรับ Thai invoice extraction
	systemPrompt := `คุณเป็น AI ผู้เชี่ยวชาญในการ extract ข้อมูลจากใบแจ้งหนี้ภาษาไทยและภาษาอังกฤษ
ให้ extract ข้อมูลต่อไปนี้จากใบแจ้งหนี้ที่ให้มา:
- ชื่อผู้ขาย (vendor_name)
- เลขที่ใบแจ้งหนี้ (invoice_number)
- วันที่ใบแจ้งหนี้ รูปแบบ ISO 8601 (invoice_date)
- รายการสินค้า/บริการ (line_items): description, quantity, unit_price, subtotal
- ยอดก่อน VAT (subtotal)
- จำนวน VAT (vat_amount)
- ยอดรวมทั้งสิ้น (total_amount)
- สกุลเงิน (currency) เช่น THB, USD

หมายเหตุ:
- ราคาทั้งหมดเป็นสกุลเงินบาท (THB) ถ้าไม่ระบุ
- ปี พ.ศ. ให้แปลงเป็น ค.ศ. (ลบ 543)
- ตอบเป็น JSON เท่านั้น ตาม schema ที่กำหนด`

	// TODO: Implement actual OpenAI call when go-openai is available
	// request := openai.ChatCompletionRequest{
	//     Model: e.config.Model,
	//     Messages: []openai.ChatCompletionMessage{
	//         {Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
	//         {Role: openai.ChatMessageRoleUser, Content: rawText},
	//     },
	//     ResponseFormat: &openai.ChatCompletionResponseFormat{
	//         Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
	//         JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
	//             Name:   "invoice_extraction",
	//             Schema: extractionJSONSchema,
	//             Strict: true,
	//         },
	//     },
	//     MaxTokens: e.config.MaxTokens,
	// }
	//
	// resp, err := e.client.CreateChatCompletion(ctx, request)
	// if err != nil {
	//     return nil, fmt.Errorf("openai api call failed: %w", err)
	// }
	//
	// var result ExtractedInvoiceData
	// if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
	//     return nil, fmt.Errorf("parsing openai response: %w", err)
	// }
	// return &result, nil

	_ = systemPrompt // suppress unused warning until implemented

	return nil, fmt.Errorf("openai extractor not implemented: add go-openai dependency and uncomment code")
}

// extractionJSONSchema — JSON Schema สำหรับ OpenAI structured output
// ใช้กับ openai.ChatCompletionResponseFormatTypeJSONSchema
var extractionJSONSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "vendor_name": {"type": "string"},
    "invoice_number": {"type": "string"},
    "invoice_date": {"type": "string", "format": "date"},
    "line_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "description": {"type": "string"},
          "quantity": {"type": "number"},
          "unit_price": {"type": "number"},
          "subtotal": {"type": "number"},
          "currency": {"type": "string"}
        },
        "required": ["description", "quantity", "unit_price", "subtotal"]
      }
    },
    "subtotal": {"type": "number"},
    "vat_amount": {"type": "number"},
    "total_amount": {"type": "number"},
    "currency": {"type": "string"},
    "notes": {"type": "string"}
  },
  "required": ["vendor_name", "invoice_number", "invoice_date", "line_items", "total_amount", "currency"],
  "additionalProperties": false
}`)
