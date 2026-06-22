// Package main สาธิต AI-powered Document Processor สำหรับเอกสารธุรกิจไทย
// รันด้วย: go run main.go
//
// Features:
//   - DocumentClassifier: จำแนกประเภทเอกสาร (Invoice, Contract, Report)
//   - InformationExtractor: ดึงข้อมูลสำคัญออกมาเป็น struct
//   - Summarizer: สรุปเอกสารเป็นภาษาไทย
//   - Worker Pool: ประมวลผลหลายเอกสารพร้อมกันด้วย goroutines
//   - Cache: เก็บผล hash ของเอกสารที่เคยประมวลผลแล้ว
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ============================================================
// SECTION 1: Data Types
// ============================================================

// DocumentType — ประเภทเอกสารธุรกิจ
type DocumentType string

const (
	DocInvoice  DocumentType = "INVOICE"   // ใบแจ้งหนี้/ใบกำกับภาษี
	DocContract DocumentType = "CONTRACT"  // สัญญา
	DocReport   DocumentType = "REPORT"    // รายงาน
	DocUnknown  DocumentType = "UNKNOWN"   // ไม่รู้จัก
)

// Document — เอกสารที่ต้องการประมวลผล
type Document struct {
	ID      string
	Content string
	Type    DocumentType // อาจจะยังไม่รู้ ให้ classifier กำหนด
}

// InvoiceData — ข้อมูลที่ extract จากใบแจ้งหนี้
type InvoiceData struct {
	InvoiceNumber string  `json:"invoice_number"`
	Date          string  `json:"date"`
	DueDate       string  `json:"due_date"`
	SellerName    string  `json:"seller_name"`
	BuyerName     string  `json:"buyer_name"`
	Amount        float64 `json:"amount"`
	VAT           float64 `json:"vat"`
	TotalAmount   float64 `json:"total_amount"`
	Currency      string  `json:"currency"`
	Items         []InvoiceItem `json:"items"`
}

// InvoiceItem — รายการสินค้า/บริการในใบแจ้งหนี้
type InvoiceItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	Amount      float64 `json:"amount"`
}

// ContractData — ข้อมูลที่ extract จากสัญญา
type ContractData struct {
	ContractNumber string `json:"contract_number"`
	Title          string `json:"title"`
	PartyA         string `json:"party_a"`
	PartyB         string `json:"party_b"`
	StartDate      string `json:"start_date"`
	EndDate        string `json:"end_date"`
	Value          float64 `json:"value"`
	Currency       string  `json:"currency"`
	KeyTerms       []string `json:"key_terms"`
}

// ReportData — ข้อมูลที่ extract จากรายงาน
type ReportData struct {
	Title      string   `json:"title"`
	Period     string   `json:"period"`
	Author     string   `json:"author"`
	KeyMetrics []Metric `json:"key_metrics"`
	Summary    string   `json:"summary"`
}

// Metric — ตัวเลขสำคัญในรายงาน
type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// ProcessingResult — ผลการประมวลผลเอกสาร
type ProcessingResult struct {
	DocumentID   string
	DocumentType DocumentType
	ExtractedData interface{} // InvoiceData หรือ ContractData หรือ ReportData
	Summary      string
	ProcessedAt  time.Time
	Duration     time.Duration
	FromCache    bool
	Error        error
}

// ============================================================
// SECTION 2: Mock LLM Client
// ============================================================
// Mock LLM ที่ return realistic responses โดยไม่ต้องการ API key จริง
// สำคัญมาก: mock ต้องให้ข้อมูลสมจริงเพื่อให้ demo ทำงานได้

// MockLLMClient — จำลอง LLM responses
type MockLLMClient struct {
	latency time.Duration // จำลอง API latency
}

func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{latency: 100 * time.Millisecond}
}

// Complete — return mock response ตาม prompt ที่ได้รับ
func (m *MockLLMClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// จำลอง network latency
	select {
	case <-time.After(m.latency):
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// ดูว่า prompt ต้องการอะไร และ return response ที่เหมาะสม
	switch {
	case strings.Contains(systemPrompt, "classify") || strings.Contains(systemPrompt, "จำแนกประเภท"):
		return m.mockClassify(userPrompt), nil
	case strings.Contains(systemPrompt, "INVOICE") || strings.Contains(systemPrompt, "ใบแจ้งหนี้"):
		return m.mockExtractInvoice(userPrompt), nil
	case strings.Contains(systemPrompt, "CONTRACT") || strings.Contains(systemPrompt, "สัญญา"):
		return m.mockExtractContract(userPrompt), nil
	case strings.Contains(systemPrompt, "REPORT") || strings.Contains(systemPrompt, "รายงาน"):
		return m.mockExtractReport(userPrompt), nil
	case strings.Contains(systemPrompt, "สรุป") || strings.Contains(systemPrompt, "summarize"):
		return m.mockSummarize(userPrompt), nil
	default:
		return `{"type": "UNKNOWN"}`, nil
	}
}

func (m *MockLLMClient) mockClassify(text string) string {
	text = strings.ToLower(text)
	if strings.Contains(text, "invoice") || strings.Contains(text, "ใบแจ้งหนี้") ||
		strings.Contains(text, "ใบกำกับ") || strings.Contains(text, "invoice no") {
		return `{"document_type": "INVOICE", "confidence": 0.97}`
	}
	if strings.Contains(text, "สัญญา") || strings.Contains(text, "contract") ||
		strings.Contains(text, "ข้อตกลง") || strings.Contains(text, "คู่สัญญา") {
		return `{"document_type": "CONTRACT", "confidence": 0.95}`
	}
	if strings.Contains(text, "รายงาน") || strings.Contains(text, "report") ||
		strings.Contains(text, "ผลการดำเนินงาน") || strings.Contains(text, "สรุปผล") {
		return `{"document_type": "REPORT", "confidence": 0.93}`
	}
	return `{"document_type": "UNKNOWN", "confidence": 0.50}`
}

func (m *MockLLMClient) mockExtractInvoice(_ string) string {
	return `{
		"invoice_number": "INV-2024-00142",
		"date": "2024-11-15",
		"due_date": "2024-12-15",
		"seller_name": "บริษัท เทคโนโลยีไทย จำกัด",
		"buyer_name": "บริษัท นวัตกรรมไทย จำกัด (มหาชน)",
		"amount": 150000.00,
		"vat": 10500.00,
		"total_amount": 160500.00,
		"currency": "THB",
		"items": [
			{"description": "บริการพัฒนาซอฟต์แวร์ระบบ ERP", "quantity": 1, "unit_price": 100000.00, "amount": 100000.00},
			{"description": "ค่าบำรุงรักษาระบบรายเดือน", "quantity": 2, "unit_price": 25000.00, "amount": 50000.00}
		]
	}`
}

func (m *MockLLMClient) mockExtractContract(_ string) string {
	return `{
		"contract_number": "CTR-2024-0089",
		"title": "สัญญาจ้างพัฒนาระบบสารสนเทศ",
		"party_a": "บริษัท ดิจิทัลไทย จำกัด",
		"party_b": "บริษัท ซอฟต์แวร์เฮ้าส์ จำกัด",
		"start_date": "2024-12-01",
		"end_date": "2025-11-30",
		"value": 2400000.00,
		"currency": "THB",
		"key_terms": ["ส่งมอบงานทุก 30 วัน", "ชำระเงิน 30 วันหลังส่งใบแจ้งหนี้", "ค่าปรับ 0.1% ต่อวันที่ล่าช้า", "ความลับทางการค้า 2 ปี"]
	}`
}

func (m *MockLLMClient) mockExtractReport(_ string) string {
	return `{
		"title": "รายงานผลการดำเนินงานประจำไตรมาส Q3/2567",
		"period": "กรกฎาคม - กันยายน 2567",
		"author": "ฝ่ายการเงินและบัญชี",
		"key_metrics": [
			{"name": "รายได้รวม", "value": 45200000.00, "unit": "บาท"},
			{"name": "กำไรสุทธิ", "value": 8900000.00, "unit": "บาท"},
			{"name": "อัตรากำไร", "value": 19.7, "unit": "%"},
			{"name": "จำนวนลูกค้าใหม่", "value": 127, "unit": "ราย"}
		],
		"summary": "ผลการดำเนินงานไตรมาส Q3 เติบโต 15% เมื่อเทียบกับปีก่อน"
	}`
}

func (m *MockLLMClient) mockSummarize(text string) string {
	// สร้าง summary สั้นๆ จาก text
	words := strings.Fields(text)
	if len(words) > 20 {
		words = words[:20]
	}
	preview := strings.Join(words, " ")
	return fmt.Sprintf("สรุป: เอกสารนี้เกี่ยวข้องกับ %s... (ข้อมูลสำคัญถูก extract แล้ว สามารถนำไปใช้งานต่อได้)", preview)
}

// ============================================================
// SECTION 3: Document Classifier
// ============================================================
// ใช้ LLM ในการจำแนกประเภทเอกสาร
// Prompt engineering: บอก LLM ให้ตอบ JSON ที่มี document_type และ confidence
//
// ทำไมต้องส่ง examples ใน prompt?
//   - Few-shot learning ช่วยให้ LLM เข้าใจ format ที่ต้องการ
//   - ลด hallucination (LLM ไม่แต่งข้อมูลขึ้นมาเอง)
//   - เพิ่ม accuracy สำหรับ domain-specific documents

// DocumentClassifier — จำแนกประเภทเอกสาร
type DocumentClassifier struct {
	llm *MockLLMClient
}

func NewDocumentClassifier(llm *MockLLMClient) *DocumentClassifier {
	return &DocumentClassifier{llm: llm}
}

// Classify — จำแนกประเภทเอกสาร
func (dc *DocumentClassifier) Classify(ctx context.Context, content string) (DocumentType, float64, error) {
	systemPrompt := `คุณคือ document classification expert ที่เชี่ยวชาญเอกสารธุรกิจไทย

จำแนกประเภทเอกสารเป็น 1 ใน 4 ประเภท:
- INVOICE: ใบแจ้งหนี้, ใบกำกับภาษี, ใบเสร็จรับเงิน
- CONTRACT: สัญญา, ข้อตกลง, MOU
- REPORT: รายงาน, สรุปผล, บันทึกการประชุม
- UNKNOWN: ไม่สามารถจำแนกได้

ตอบเป็น JSON เท่านั้น:
{"document_type": "INVOICE|CONTRACT|REPORT|UNKNOWN", "confidence": 0.0-1.0}

ห้ามมีข้อความอื่นนอกจาก JSON`

	// ส่งแค่ 500 ตัวอักษรแรก เพื่อประหยัด tokens
	// ปกติแล้วหัวเอกสารเพียงพอสำหรับการจำแนก
	preview := content
	if len(preview) > 500 {
		preview = preview[:500]
	}

	userPrompt := fmt.Sprintf("จำแนกประเภทเอกสารนี้:\n\n%s", preview)

	resp, err := dc.llm.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return DocUnknown, 0, fmt.Errorf("classify: %w", err)
	}

	// Parse JSON response
	var result struct {
		DocumentType string  `json:"document_type"`
		Confidence   float64 `json:"confidence"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return DocUnknown, 0, fmt.Errorf("parse classify response: %w (raw: %s)", err, resp)
	}

	return DocumentType(result.DocumentType), result.Confidence, nil
}

// ============================================================
// SECTION 4: Information Extractor
// ============================================================
// Extract ข้อมูลสำคัญออกมาเป็น struct ตามประเภทเอกสาร
//
// Prompt engineering tips:
//   1. ส่ง JSON schema แบบ annotated (อธิบาย field แต่ละตัว)
//   2. ใส่ตัวอย่าง (example) ใน prompt
//   3. บอกให้ตอบ null สำหรับ field ที่หาไม่พบ (ดีกว่าให้ hallucinate)
//   4. อธิบาย format วันที่และตัวเลขที่ต้องการ

// InformationExtractor — extract ข้อมูลจากเอกสาร
type InformationExtractor struct {
	llm *MockLLMClient
}

func NewInformationExtractor(llm *MockLLMClient) *InformationExtractor {
	return &InformationExtractor{llm: llm}
}

// Extract — ดึงข้อมูลจากเอกสาร ตามประเภทที่กำหนด
func (ie *InformationExtractor) Extract(ctx context.Context, content string, docType DocumentType) (interface{}, error) {
	switch docType {
	case DocInvoice:
		return ie.extractInvoice(ctx, content)
	case DocContract:
		return ie.extractContract(ctx, content)
	case DocReport:
		return ie.extractReport(ctx, content)
	default:
		return nil, fmt.Errorf("unsupported document type: %s", docType)
	}
}

func (ie *InformationExtractor) extractInvoice(ctx context.Context, content string) (*InvoiceData, error) {
	// System prompt พร้อม JSON schema อธิบาย
	// เทคนิค: อธิบาย format วันที่และตัวเลขให้ชัดเจน
	systemPrompt := `คุณคือ invoice data extraction expert

Extract ข้อมูลจาก INVOICE และตอบเป็น JSON ตาม schema ต่อไปนี้:
{
  "invoice_number": "string — เลขที่ใบแจ้งหนี้",
  "date": "string — วันที่ออกใบแจ้งหนี้ format YYYY-MM-DD",
  "due_date": "string — วันครบกำหนดชำระ format YYYY-MM-DD หรือ null",
  "seller_name": "string — ชื่อผู้ขาย/ผู้ให้บริการ",
  "buyer_name": "string — ชื่อผู้ซื้อ/ลูกค้า",
  "amount": "number — ยอดก่อน VAT",
  "vat": "number — ภาษีมูลค่าเพิ่ม",
  "total_amount": "number — ยอดรวมทั้งหมด",
  "currency": "string — สกุลเงิน เช่น THB, USD",
  "items": [{"description": "string", "quantity": "number", "unit_price": "number", "amount": "number"}]
}

หมายเหตุ:
- ตัวเลขให้เป็น number ไม่ใส่ comma หรือสัญลักษณ์สกุลเงิน
- ถ้าหาข้อมูลบางอย่างไม่พบ ให้ใส่ null
- ตอบ JSON เท่านั้น`

	resp, err := ie.llm.Complete(ctx, systemPrompt, "Extract invoice data from:\n\n"+content)
	if err != nil {
		return nil, err
	}

	var data InvoiceData
	if err := json.Unmarshal([]byte(resp), &data); err != nil {
		return nil, fmt.Errorf("parse invoice data: %w", err)
	}
	return &data, nil
}

func (ie *InformationExtractor) extractContract(ctx context.Context, content string) (*ContractData, error) {
	systemPrompt := `คุณคือ contract data extraction expert

Extract ข้อมูลจาก CONTRACT และตอบเป็น JSON:
{
  "contract_number": "string — เลขที่สัญญา",
  "title": "string — ชื่อ/หัวข้อสัญญา",
  "party_a": "string — คู่สัญญาฝ่าย ก",
  "party_b": "string — คู่สัญญาฝ่าย ข",
  "start_date": "string — วันเริ่มต้น YYYY-MM-DD",
  "end_date": "string — วันสิ้นสุด YYYY-MM-DD",
  "value": "number — มูลค่าสัญญา",
  "currency": "string — สกุลเงิน",
  "key_terms": ["string — เงื่อนไขสำคัญ"]
}

ตอบ JSON เท่านั้น`

	resp, err := ie.llm.Complete(ctx, systemPrompt, "Extract contract data from:\n\n"+content)
	if err != nil {
		return nil, err
	}

	var data ContractData
	if err := json.Unmarshal([]byte(resp), &data); err != nil {
		return nil, fmt.Errorf("parse contract data: %w", err)
	}
	return &data, nil
}

func (ie *InformationExtractor) extractReport(ctx context.Context, content string) (*ReportData, error) {
	systemPrompt := `คุณคือ report data extraction expert

Extract ข้อมูลจาก REPORT และตอบเป็น JSON:
{
  "title": "string — ชื่อรายงาน",
  "period": "string — ช่วงเวลาของรายงาน",
  "author": "string — ผู้จัดทำ/ฝ่ายที่รับผิดชอบ",
  "key_metrics": [{"name": "string", "value": "number", "unit": "string"}],
  "summary": "string — สรุปสั้นๆ"
}

ตอบ JSON เท่านั้น`

	resp, err := ie.llm.Complete(ctx, systemPrompt, "Extract report data from:\n\n"+content)
	if err != nil {
		return nil, err
	}

	var data ReportData
	if err := json.Unmarshal([]byte(resp), &data); err != nil {
		return nil, fmt.Errorf("parse report data: %w", err)
	}
	return &data, nil
}

// ============================================================
// SECTION 5: Summarizer
// ============================================================

// Summarizer — สร้าง summary ภาษาไทยจากเอกสาร
type Summarizer struct {
	llm *MockLLMClient
}

func NewSummarizer(llm *MockLLMClient) *Summarizer {
	return &Summarizer{llm: llm}
}

// Summarize — สร้าง summary
// Prompt engineering สำหรับ summarization:
//   - บอกความยาวที่ต้องการ (ไม่เกิน X ประโยค)
//   - บอก audience (ผู้บริหาร vs นักบัญชี)
//   - บอก style (formal vs informal)
func (s *Summarizer) Summarize(ctx context.Context, content string, docType DocumentType) (string, error) {
	// ปรับ prompt ตามประเภทเอกสาร
	var focusInstructions string
	switch docType {
	case DocInvoice:
		focusInstructions = "เน้น: ยอดเงิน, วันครบกำหนด, คู่ค้า"
	case DocContract:
		focusInstructions = "เน้น: คู่สัญญา, มูลค่า, ระยะเวลา, เงื่อนไขสำคัญ"
	case DocReport:
		focusInstructions = "เน้น: ตัวเลขสำคัญ, แนวโน้ม, ข้อสังเกต"
	default:
		focusInstructions = "เน้นประเด็นสำคัญ"
	}

	systemPrompt := fmt.Sprintf(`คุณคือ document summarization expert
สรุปเอกสารเป็นภาษาไทย ไม่เกิน 3 ประโยค เหมาะสำหรับผู้บริหาร
%s
ตอบเป็น plain text ไม่ใช่ JSON`, focusInstructions)

	// ส่งแค่ 1000 ตัวอักษรเพื่อประหยัด tokens
	preview := content
	if len(preview) > 1000 {
		preview = preview[:1000] + "..."
	}

	return s.llm.Complete(ctx, systemPrompt, preview)
}

// ============================================================
// SECTION 6: Document Cache
// ============================================================
// Cache เพื่อหลีกเลี่ยงการ process เอกสารซ้ำ
// ใช้ SHA256 hash ของ content เป็น key
//
// ทำไม hash content ไม่ใช่ document ID?
//   - เอกสาร ID เดียวกันอาจมีหลาย version
//   - เอกสารต่าง ID แต่ content เดียวกัน ควร share cache

// DocumentCache — in-memory cache สำหรับผลการประมวลผล
type DocumentCache struct {
	mu      sync.RWMutex
	entries map[string]*ProcessingResult
	hits    int
	misses  int
}

func NewDocumentCache() *DocumentCache {
	return &DocumentCache{
		entries: make(map[string]*ProcessingResult),
	}
}

// hashContent — สร้าง hash จาก content
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:8]) // ใช้แค่ 8 bytes แรก (16 hex chars)
}

func (dc *DocumentCache) Get(content string) (*ProcessingResult, bool) {
	key := hashContent(content)
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	result, ok := dc.entries[key]
	if ok {
		dc.hits++
		return result, true
	}
	dc.misses++
	return nil, false
}

func (dc *DocumentCache) Set(content string, result *ProcessingResult) {
	key := hashContent(content)
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.entries[key] = result
}

func (dc *DocumentCache) Stats() (hits, misses int) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.hits, dc.misses
}

// ============================================================
// SECTION 7: Document Processor (orchestrator)
// ============================================================

// DocumentProcessor — orchestrate การประมวลผลเอกสาร
type DocumentProcessor struct {
	classifier *DocumentClassifier
	extractor  *InformationExtractor
	summarizer *Summarizer
	cache      *DocumentCache
}

func NewDocumentProcessor(llm *MockLLMClient) *DocumentProcessor {
	return &DocumentProcessor{
		classifier: NewDocumentClassifier(llm),
		extractor:  NewInformationExtractor(llm),
		summarizer: NewSummarizer(llm),
		cache:      NewDocumentCache(),
	}
}

// Process — ประมวลผลเอกสารเดียว
func (dp *DocumentProcessor) Process(ctx context.Context, doc Document) ProcessingResult {
	start := time.Now()

	// ตรวจ cache ก่อน
	if cached, ok := dp.cache.Get(doc.Content); ok {
		result := *cached
		result.FromCache = true
		log.Printf("[cache HIT] doc=%s type=%s", doc.ID, result.DocumentType)
		return result
	}

	result := ProcessingResult{
		DocumentID:  doc.ID,
		ProcessedAt: start,
	}

	// Step 1: จำแนกประเภท
	docType, confidence, err := dp.classifier.Classify(ctx, doc.Content)
	if err != nil {
		result.Error = fmt.Errorf("classify: %w", err)
		return result
	}
	result.DocumentType = docType
	log.Printf("[classify] doc=%s type=%s confidence=%.2f", doc.ID, docType, confidence)

	// Step 2: Extract ข้อมูล
	if docType != DocUnknown {
		extracted, err := dp.extractor.Extract(ctx, doc.Content, docType)
		if err != nil {
			log.Printf("[extract] doc=%s error: %v", doc.ID, err)
			// ไม่ return error — ยังทำ summary ต่อได้
		} else {
			result.ExtractedData = extracted
		}
	}

	// Step 3: สรุป
	summary, err := dp.summarizer.Summarize(ctx, doc.Content, docType)
	if err != nil {
		log.Printf("[summarize] doc=%s error: %v", doc.ID, err)
	} else {
		result.Summary = summary
	}

	result.Duration = time.Since(start)

	// เก็บ cache
	dp.cache.Set(doc.Content, &result)

	return result
}

// ProcessBatch — ประมวลผลหลายเอกสารพร้อมกันด้วย worker pool
//
// Worker Pool pattern:
//   1. สร้าง jobs channel ใส่งาน
//   2. สร้าง results channel รับผล
//   3. spawn N workers (goroutines) แต่ละตัวดึงงานจาก jobs
//   4. รอให้ทุก worker เสร็จด้วย WaitGroup
//   5. ปิด results channel เมื่อทุก worker เสร็จ
func (dp *DocumentProcessor) ProcessBatch(ctx context.Context, docs []Document, workers int) []ProcessingResult {
	if workers <= 0 {
		workers = 3
	}

	jobs := make(chan Document, len(docs))
	results := make(chan ProcessingResult, len(docs))
	var wg sync.WaitGroup

	// spawn workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for doc := range jobs {
				log.Printf("[worker %d] processing doc=%s", workerID, doc.ID)
				result := dp.Process(ctx, doc)
				results <- result
			}
		}(i + 1)
	}

	// ส่งงานเข้า channel
	for _, doc := range docs {
		jobs <- doc
	}
	close(jobs) // บอก workers ว่าไม่มีงานเพิ่มแล้ว

	// รอทุก worker เสร็จ แล้ว close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// รวบรวมผลลัพธ์
	var processed []ProcessingResult
	for result := range results {
		processed = append(processed, result)
	}

	return processed
}

// ============================================================
// SECTION 8: Sample Documents
// ============================================================
// เอกสารตัวอย่างสำหรับ demo — context ธุรกิจไทย

var sampleDocuments = []Document{
	{
		ID: "doc-001",
		Content: `ใบกำกับภาษี / TAX INVOICE
Invoice No: INV-2024-00142
วันที่: 15 พฤศจิกายน 2567

ผู้ขาย: บริษัท เทคโนโลยีไทย จำกัด
เลขประจำตัวผู้เสียภาษี: 0105565123456
ที่อยู่: 999 ถนนพระราม 4 แขวงสีลม เขตบางรัก กรุงเทพฯ 10500

ผู้ซื้อ: บริษัท นวัตกรรมไทย จำกัด (มหาชน)
เลขประจำตัวผู้เสียภาษี: 0107567891234
ที่อยู่: 200 ถนนวิภาวดีรังสิต แขวงจตุจักร กรุงเทพฯ 10900

รายการ:
1. บริการพัฒนาซอฟต์แวร์ระบบ ERP             100,000.00 บาท
2. ค่าบำรุงรักษาระบบรายเดือน (ต.ค.-พ.ย.)      50,000.00 บาท

ยอดรวมก่อน VAT:     150,000.00 บาท
ภาษีมูลค่าเพิ่ม 7%:   10,500.00 บาท
ยอดรวมทั้งสิ้น:      160,500.00 บาท

กำหนดชำระ: 15 ธันวาคม 2567
โอนเงินมาที่: ธนาคารกสิกรไทย สาขาสีลม เลขที่บัญชี 123-4-56789-0`,
	},
	{
		ID: "doc-002",
		Content: `สัญญาจ้างพัฒนาระบบสารสนเทศ
เลขที่สัญญา: CTR-2024-0089

สัญญานี้ทำขึ้นระหว่าง:
คู่สัญญาฝ่าย ก: บริษัท ดิจิทัลไทย จำกัด
ที่อยู่: 88 อาคาร Empire Tower ชั้น 30 ถนนสาทรใต้ กรุงเทพฯ

คู่สัญญาฝ่าย ข: บริษัท ซอฟต์แวร์เฮ้าส์ จำกัด
ที่อยู่: 55/5 อาคาร ITEL ถนนแจ้งวัฒนะ นนทบุรี

เรื่อง: สัญญาจ้างพัฒนาระบบบริหารจัดการคลังสินค้า (WMS)
มูลค่าสัญญา: 2,400,000 บาท (สองล้านสี่แสนบาทถ้วน)
ระยะเวลา: 1 ธันวาคม 2567 ถึง 30 พฤศจิกายน 2568

เงื่อนไขหลัก:
- ส่งมอบงานทุก 30 วัน ตาม milestone ที่กำหนด
- ชำระเงิน 30 วันหลังได้รับใบแจ้งหนี้และยืนยันการส่งมอบ
- ค่าปรับกรณีส่งมอบล่าช้า 0.1% ต่อวัน
- ทรัพย์สินทางปัญญาเป็นของฝ่าย ก เมื่อชำระเงินครบ
- ข้อมูลลับทางการค้า คุ้มครอง 2 ปีหลังสิ้นสุดสัญญา

ลงนามโดยผู้มีอำนาจทั้งสองฝ่าย ณ วันที่ 28 พฤศจิกายน 2567`,
	},
	{
		ID: "doc-003",
		Content: `รายงานผลการดำเนินงานประจำไตรมาส Q3/2567
บริษัท อินโนเวท โซลูชั่น จำกัด

ช่วงเวลา: กรกฎาคม - กันยายน 2567
จัดทำโดย: ฝ่ายการเงินและบัญชี
วันที่รายงาน: 15 ตุลาคม 2567

ผลการดำเนินงานสรุป:
- รายได้รวม: 45,200,000 บาท (+15% YoY)
- ต้นทุนรวม: 28,900,000 บาท
- กำไรขั้นต้น: 16,300,000 บาท (อัตรา 36%)
- ค่าใช้จ่าย SG&A: 7,400,000 บาท
- กำไรสุทธิ: 8,900,000 บาท (อัตรา 19.7%)

ลูกค้า:
- ลูกค้าใหม่ที่ได้: 127 ราย
- อัตราการต่อสัญญา (Renewal): 87%
- Net Promoter Score (NPS): 72

สรุปและแนวโน้ม:
ไตรมาส Q3 เติบโตต่อเนื่องทั้งด้านรายได้และกำไร กลุ่มลูกค้า Enterprise ขยายตัว 25%
คาดการณ์ Q4 รายได้ 48-52 ล้านบาท จากการเปิดตัวผลิตภัณฑ์ใหม่และ pipeline ที่แข็งแกร่ง`,
	},
	{
		ID: "doc-004",
		// เอกสารเดียวกับ doc-001 เพื่อทดสอบ cache hit
		Content: `ใบกำกับภาษี / TAX INVOICE
Invoice No: INV-2024-00142
วันที่: 15 พฤศจิกายน 2567

ผู้ขาย: บริษัท เทคโนโลยีไทย จำกัด
เลขประจำตัวผู้เสียภาษี: 0105565123456
ที่อยู่: 999 ถนนพระราม 4 แขวงสีลม เขตบางรัก กรุงเทพฯ 10500

ผู้ซื้อ: บริษัท นวัตกรรมไทย จำกัด (มหาชน)
เลขประจำตัวผู้เสียภาษี: 0107567891234
ที่อยู่: 200 ถนนวิภาวดีรังสิต แขวงจตุจักร กรุงเทพฯ 10900

รายการ:
1. บริการพัฒนาซอฟต์แวร์ระบบ ERP             100,000.00 บาท
2. ค่าบำรุงรักษาระบบรายเดือน (ต.ค.-พ.ย.)      50,000.00 บาท

ยอดรวมก่อน VAT:     150,000.00 บาท
ภาษีมูลค่าเพิ่ม 7%:   10,500.00 บาท
ยอดรวมทั้งสิ้น:      160,500.00 บาท

กำหนดชำระ: 15 ธันวาคม 2567
โอนเงินมาที่: ธนาคารกสิกรไทย สาขาสีลม เลขที่บัญชี 123-4-56789-0`,
	},
	{
		ID: "doc-005",
		Content: `บันทึกข้อตกลงความร่วมมือ (MOU)
ระหว่าง: มหาวิทยาลัยเทคโนโลยีพระจอมเกล้า กับ บริษัท เอไอ ไทย จำกัด

วัตถุประสงค์: ร่วมมือด้านการวิจัยและพัฒนา AI สำหรับภาษาไทย
ระยะเวลา: 3 ปี (2567-2570)
มูลค่าโครงการ: 15,000,000 บาท

ขอบเขตความร่วมมือ:
1. พัฒนา Thai Language Model ขนาด 7B parameters
2. แลกเปลี่ยนนักวิจัยและนักศึกษา
3. ตีพิมพ์ผลงานวิจัยร่วมกัน
4. Transfer technology สู่ภาคอุตสาหกรรม`,
	},
}

// ============================================================
// SECTION 9: Main Demo
// ============================================================

func main() {
	fmt.Println("=== Module 7: AI Document Processor Demo ===")
	fmt.Println("(รันด้วย Mock LLM — ไม่ต้องใช้ API key จริง)")
	fmt.Println()

	ctx := context.Background()

	llm := NewMockLLMClient()
	processor := NewDocumentProcessor(llm)

	// ─── Demo 1: Process เอกสารเดียว ─────────────────────────
	fmt.Println("=== Demo 1: Process Single Document ===")
	result := processor.Process(ctx, sampleDocuments[0])
	printResult(result)

	// ─── Demo 2: Worker Pool — process หลายเอกสารพร้อมกัน ────
	fmt.Println("\n=== Demo 2: Batch Processing with Worker Pool (3 workers) ===")
	start := time.Now()
	results := processor.ProcessBatch(ctx, sampleDocuments, 3)
	elapsed := time.Since(start)

	fmt.Printf("ประมวลผล %d เอกสาร ใช้เวลา %v\n\n", len(results), elapsed)
	for _, r := range results {
		printResult(r)
	}

	// ─── Demo 3: Cache stats ────────────────────────────────
	fmt.Println("\n=== Demo 3: Cache Statistics ===")
	hits, misses := processor.cache.Stats()
	fmt.Printf("Cache hits:   %d\n", hits)
	fmt.Printf("Cache misses: %d\n", misses)
	if hits+misses > 0 {
		fmt.Printf("Hit rate:     %.1f%%\n", float64(hits)/float64(hits+misses)*100)
	}
}

// printResult — แสดงผลการประมวลผลเอกสาร
func printResult(r ProcessingResult) {
	cacheStr := ""
	if r.FromCache {
		cacheStr = " [CACHE HIT]"
	}

	fmt.Printf("--- Document: %s%s ---\n", r.DocumentID, cacheStr)

	if r.Error != nil {
		fmt.Printf("  Error: %v\n", r.Error)
		return
	}

	fmt.Printf("  Type: %s\n", r.DocumentType)
	fmt.Printf("  Duration: %v\n", r.Duration)
	fmt.Printf("  Summary: %s\n", r.Summary)

	// แสดงข้อมูลที่ extract ตามประเภท
	switch data := r.ExtractedData.(type) {
	case *InvoiceData:
		fmt.Printf("  Invoice: %s | Total: %.2f %s | Due: %s\n",
			data.InvoiceNumber, data.TotalAmount, data.Currency, data.DueDate)
		fmt.Printf("  Seller: %s → Buyer: %s\n", data.SellerName, data.BuyerName)
	case *ContractData:
		fmt.Printf("  Contract: %s | Value: %.2f %s\n",
			data.ContractNumber, data.Value, data.Currency)
		fmt.Printf("  Parties: %s ↔ %s\n", data.PartyA, data.PartyB)
	case *ReportData:
		fmt.Printf("  Report: %s | Period: %s\n", data.Title, data.Period)
		if len(data.KeyMetrics) > 0 {
			fmt.Printf("  Key metric: %s = %.2f %s\n",
				data.KeyMetrics[0].Name, data.KeyMetrics[0].Value, data.KeyMetrics[0].Unit)
		}
	}
	fmt.Println()
}
