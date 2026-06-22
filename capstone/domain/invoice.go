// Package domain — Invoice DDD Aggregate
// โครงสร้าง DDD สำหรับ Invoice Processing:
//
// domain/          ← ไม่ขึ้นกับอะไรเลย (pure business logic)
//   invoice.go     ← Invoice Aggregate Root + Value Objects + Domain Events
//   repository.go  ← Repository + EventBus Interfaces
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ============================================================
// VALUE OBJECTS — Immutable, ไม่มี identity
// ============================================================

// InvoiceID — Typed ID ป้องกัน primitive obsession
// ทำไมไม่ใช้ string ตรงๆ? เพื่อป้องกันการเอา VendorID ไปใส่ตรงที่ต้องการ InvoiceID
type InvoiceID struct {
	value string
}

func NewInvoiceID(id string) (InvoiceID, error) {
	if id == "" {
		return InvoiceID{}, errors.New("invoice ID cannot be empty")
	}
	return InvoiceID{value: id}, nil
}

func (id InvoiceID) String() string { return id.value }
func (id InvoiceID) IsZero() bool   { return id.value == "" }

// VendorID — Typed ID สำหรับ Vendor
type VendorID struct {
	value string
}

func NewVendorID(id string) (VendorID, error) {
	if id == "" {
		return VendorID{}, errors.New("vendor ID cannot be empty")
	}
	return VendorID{value: id}, nil
}

func (id VendorID) String() string { return id.value }
func (id VendorID) IsZero() bool   { return id.value == "" }

// ----------------------------------------
// Money — Value Object สำหรับเงิน (รองรับ THB)
// ทำไมไม่ใช้ float64? เพราะ floating point ไม่แม่นยำสำหรับเงิน
// ใช้ int64 เก็บ satang (100 satang = 1 บาท)
type Money struct {
	amount   int64  // หน่วย: satang (1 บาท = 100 satang)
	currency string // ISO 4217: "THB", "USD", "EUR"
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, errors.New("amount cannot be negative")
	}
	if currency == "" {
		return Money{}, errors.New("currency is required")
	}
	return Money{amount: amount, currency: currency}, nil
}

// NewTHB สร้าง Money ในสกุลเงินบาท (สะดวกสำหรับ Thai context)
// รับ amount เป็น satang (เช่น 50000 satang = 500 บาท)
func NewTHB(satang int64) (Money, error) {
	return NewMoney(satang, "THB")
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.amount == 0 }

// String แสดงผลในรูปแบบ "500.00 THB"
func (m Money) String() string {
	return fmt.Sprintf("%.2f %s", float64(m.amount)/100, m.currency)
}

// Baht คืนค่าเป็นบาท (float64 สำหรับ display เท่านั้น ห้ามใช้คำนวณ)
func (m Money) Baht() float64 {
	return float64(m.amount) / 100
}

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("cannot add %s to %s", other.currency, m.currency)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

func (m Money) Multiply(factor int64) Money {
	return Money{amount: m.amount * factor, currency: m.currency}
}

func (m Money) Equals(other Money) bool {
	return m.amount == other.amount && m.currency == other.currency
}

func (m Money) GreaterThan(other Money) bool {
	return m.currency == other.currency && m.amount > other.amount
}

// ----------------------------------------
// InvoiceLineItem — Value Object (immutable รายการในใบแจ้งหนี้)
type InvoiceLineItem struct {
	description string
	quantity    int64
	unitPrice   Money
}

func NewInvoiceLineItem(description string, quantity int64, unitPrice Money) (InvoiceLineItem, error) {
	if description == "" {
		return InvoiceLineItem{}, errors.New("line item description is required")
	}
	if quantity <= 0 {
		return InvoiceLineItem{}, errors.New("quantity must be positive")
	}
	if unitPrice.IsZero() {
		return InvoiceLineItem{}, errors.New("unit price cannot be zero")
	}
	return InvoiceLineItem{
		description: description,
		quantity:    quantity,
		unitPrice:   unitPrice,
	}, nil
}

func (li InvoiceLineItem) Description() string { return li.description }
func (li InvoiceLineItem) Quantity() int64     { return li.quantity }
func (li InvoiceLineItem) UnitPrice() Money    { return li.unitPrice }

func (li InvoiceLineItem) Subtotal() Money {
	return li.unitPrice.Multiply(li.quantity)
}

// ============================================================
// INVOICE STATUS — State Machine
// สถานะของ invoice ดำเนินตาม lifecycle:
// Received → Processing → Extracted → Approved/Rejected
// ============================================================

type InvoiceStatus string

const (
	// InvoiceStatusReceived — ได้รับ invoice เข้าระบบแล้ว รอ AI extraction
	InvoiceStatusReceived InvoiceStatus = "RECEIVED"
	// InvoiceStatusProcessing — กำลัง extract ข้อมูลด้วย AI
	InvoiceStatusProcessing InvoiceStatus = "PROCESSING"
	// InvoiceStatusExtracted — AI extraction สำเร็จแล้ว รอ approval
	InvoiceStatusExtracted InvoiceStatus = "EXTRACTED"
	// InvoiceStatusApproved — ผ่านการอนุมัติแล้ว
	InvoiceStatusApproved InvoiceStatus = "APPROVED"
	// InvoiceStatusRejected — ถูกปฏิเสธ
	InvoiceStatusRejected InvoiceStatus = "REJECTED"
)

// validInvoiceTransitions กำหนด state machine transitions
// Business rule: ต้องผ่าน Extracted ก่อนถึงจะ Approve/Reject ได้
var validInvoiceTransitions = map[InvoiceStatus][]InvoiceStatus{
	InvoiceStatusReceived:   {InvoiceStatusProcessing},
	InvoiceStatusProcessing: {InvoiceStatusExtracted, InvoiceStatusRejected},
	InvoiceStatusExtracted:  {InvoiceStatusApproved, InvoiceStatusRejected},
	InvoiceStatusApproved:   {}, // terminal state
	InvoiceStatusRejected:   {}, // terminal state
}

func (s InvoiceStatus) CanTransitionTo(next InvoiceStatus) bool {
	allowed, ok := validInvoiceTransitions[s]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == next {
			return true
		}
	}
	return false
}

// ============================================================
// DOMAIN EVENTS — สิ่งที่เกิดขึ้นใน domain
// ============================================================

// DomainEvent — interface สำหรับ events ทั้งหมด
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
	AggregateID() string
}

// BaseEvent — embed ใน events ทั้งหมด
type BaseEvent struct {
	occurredAt  time.Time
	aggregateID string
}

func newBaseEvent(aggregateID string) BaseEvent {
	return BaseEvent{
		occurredAt:  time.Now(),
		aggregateID: aggregateID,
	}
}

func (e BaseEvent) OccurredAt() time.Time { return e.occurredAt }
func (e BaseEvent) AggregateID() string   { return e.aggregateID }

// InvoiceReceivedEvent — fired เมื่อระบบได้รับ invoice ใหม่
type InvoiceReceivedEvent struct {
	BaseEvent
	InvoiceID InvoiceID
	VendorID  VendorID
	RawText   string
}

func (e InvoiceReceivedEvent) EventName() string { return "invoice.received" }

// InvoiceExtractedEvent — fired เมื่อ AI extraction สำเร็จ
type InvoiceExtractedEvent struct {
	BaseEvent
	InvoiceID   InvoiceID
	VendorName  string
	TotalAmount Money
	LineItems   int
}

func (e InvoiceExtractedEvent) EventName() string { return "invoice.extracted" }

// InvoiceApprovedEvent — fired เมื่อ invoice ได้รับการอนุมัติ
type InvoiceApprovedEvent struct {
	BaseEvent
	InvoiceID   InvoiceID
	ApprovedBy  string
	TotalAmount Money
}

func (e InvoiceApprovedEvent) EventName() string { return "invoice.approved" }

// InvoiceRejectedEvent — fired เมื่อ invoice ถูกปฏิเสธ
type InvoiceRejectedEvent struct {
	BaseEvent
	InvoiceID InvoiceID
	RejectedBy string
	Reason    string
}

func (e InvoiceRejectedEvent) EventName() string { return "invoice.rejected" }

// ============================================================
// INVOICE AGGREGATE ROOT
// ============================================================

// Invoice — Aggregate Root สำหรับ Invoice Processing
// Fields เป็น unexported ทั้งหมด — business rules ต้องผ่าน methods
type Invoice struct {
	// Identity
	id       InvoiceID
	vendorID VendorID

	// State
	status    InvoiceStatus
	rawText   string // ข้อความดิบจาก invoice
	createdAt time.Time
	updatedAt time.Time

	// Optimistic locking
	version int

	// Extracted data (populated หลัง AI extraction)
	vendorName    string
	invoiceDate   time.Time
	lineItems     []InvoiceLineItem
	totalAmount   Money
	currency      string
	extractedAt   *time.Time
	requiresManagerApproval bool // ต้องการ manager อนุมัติสำหรับใบที่มีมูลค่าสูง

	// Domain events ที่ยังไม่ได้ publish
	uncommittedEvents []DomainEvent
}

// ============================================================
// FACTORY METHOD
// ============================================================

// ReceiveInvoice สร้าง Invoice ใหม่เมื่อได้รับเข้าระบบ
// Business rule: rawText ต้องไม่ว่าง
func ReceiveInvoice(id InvoiceID, vendorID VendorID, rawText string) (*Invoice, error) {
	if id.IsZero() {
		return nil, errors.New("invoice ID is required")
	}
	if vendorID.IsZero() {
		return nil, errors.New("vendor ID is required")
	}
	if rawText == "" {
		return nil, errors.New("raw invoice text is required")
	}

	now := time.Now()
	inv := &Invoice{
		id:        id,
		vendorID:  vendorID,
		status:    InvoiceStatusReceived,
		rawText:   rawText,
		createdAt: now,
		updatedAt: now,
		version:   1,
	}

	// Fire domain event — ระบบรับ invoice เข้าแล้ว
	inv.addEvent(InvoiceReceivedEvent{
		BaseEvent: newBaseEvent(id.String()),
		InvoiceID: id,
		VendorID:  vendorID,
		RawText:   rawText,
	})

	return inv, nil
}

// ============================================================
// AGGREGATE METHODS — Business Operations
// ============================================================

// StartProcessing เปลี่ยนสถานะเป็น Processing (AI กำลังทำงาน)
func (inv *Invoice) StartProcessing() error {
	// Business rule: ต้องอยู่ใน Received ก่อนถึงจะ process ได้
	if !inv.status.CanTransitionTo(InvoiceStatusProcessing) {
		return fmt.Errorf("cannot start processing invoice in status %s", inv.status)
	}

	inv.status = InvoiceStatusProcessing
	inv.updatedAt = time.Now()
	inv.version++

	return nil
}

// ApplyExtraction บันทึกผลการ extract ข้อมูลจาก AI
// Business rule: ตรวจสอบว่า total ตรงกับผลรวมของ line items
func (inv *Invoice) ApplyExtraction(
	vendorName string,
	invoiceDate time.Time,
	lineItems []InvoiceLineItem,
	totalAmount Money,
) error {
	// Business rule: ต้องอยู่ใน Processing ก่อน
	if !inv.status.CanTransitionTo(InvoiceStatusExtracted) {
		return fmt.Errorf("cannot apply extraction to invoice in status %s", inv.status)
	}

	if vendorName == "" {
		return errors.New("vendor name is required from extraction")
	}

	if len(lineItems) == 0 {
		return errors.New("at least one line item is required")
	}

	// Business rule: ตรวจสอบว่า total ตรงกับผลรวม line items
	calculatedTotal, err := calculateLineItemsTotal(lineItems)
	if err != nil {
		return fmt.Errorf("calculating line items total: %w", err)
	}

	// อนุญาตให้มีความคลาดเคลื่อนได้ ±1 satang (เพราะ rounding)
	diff := calculatedTotal.Amount() - totalAmount.Amount()
	if diff < -1 || diff > 1 {
		return fmt.Errorf(
			"extraction total mismatch: line items sum %s but stated total %s",
			calculatedTotal, totalAmount,
		)
	}

	// Business rule: invoice มูลค่า > 1,000,000 บาท ต้องการ manager approval
	// 1,000,000 บาท = 100,000,000 satang
	managerThreshold, _ := NewTHB(100_000_000)
	inv.requiresManagerApproval = totalAmount.GreaterThan(managerThreshold)

	now := time.Now()
	inv.vendorName = vendorName
	inv.invoiceDate = invoiceDate
	inv.lineItems = lineItems
	inv.totalAmount = totalAmount
	inv.currency = totalAmount.Currency()
	inv.extractedAt = &now
	inv.status = InvoiceStatusExtracted
	inv.updatedAt = now
	inv.version++

	inv.addEvent(InvoiceExtractedEvent{
		BaseEvent:   newBaseEvent(inv.id.String()),
		InvoiceID:   inv.id,
		VendorName:  vendorName,
		TotalAmount: totalAmount,
		LineItems:   len(lineItems),
	})

	return nil
}

// Approve อนุมัติ invoice
// Business rule: ต้อง Extract ก่อนจึงจะ Approve ได้
// Business rule: invoice > 1,000,000 บาท ต้องมี managerApproval = true
func (inv *Invoice) Approve(approvedBy string, isManager bool) error {
	// Business rule: ต้องอยู่ใน Extracted ก่อน (ห้าม Approve จาก Received โดยตรง)
	if !inv.status.CanTransitionTo(InvoiceStatusApproved) {
		return fmt.Errorf("cannot approve invoice in status %s (must extract first)", inv.status)
	}

	if approvedBy == "" {
		return errors.New("approver name is required")
	}

	// Business rule: invoice มูลค่าสูงต้องการ manager
	if inv.requiresManagerApproval && !isManager {
		return fmt.Errorf(
			"invoice %s requires manager approval (amount: %s)",
			inv.id, inv.totalAmount,
		)
	}

	inv.status = InvoiceStatusApproved
	inv.updatedAt = time.Now()
	inv.version++

	inv.addEvent(InvoiceApprovedEvent{
		BaseEvent:   newBaseEvent(inv.id.String()),
		InvoiceID:   inv.id,
		ApprovedBy:  approvedBy,
		TotalAmount: inv.totalAmount,
	})

	return nil
}

// Reject ปฏิเสธ invoice (สามารถทำได้ตั้งแต่ Processing หรือ Extracted)
func (inv *Invoice) Reject(rejectedBy, reason string) error {
	if !inv.status.CanTransitionTo(InvoiceStatusRejected) {
		return fmt.Errorf("cannot reject invoice in status %s", inv.status)
	}
	if reason == "" {
		return errors.New("rejection reason is required")
	}
	if rejectedBy == "" {
		return errors.New("rejector name is required")
	}

	inv.status = InvoiceStatusRejected
	inv.updatedAt = time.Now()
	inv.version++

	inv.addEvent(InvoiceRejectedEvent{
		BaseEvent:  newBaseEvent(inv.id.String()),
		InvoiceID:  inv.id,
		RejectedBy: rejectedBy,
		Reason:     reason,
	})

	return nil
}

// ============================================================
// QUERY METHODS (read-only access to state)
// ============================================================

func (inv *Invoice) ID() InvoiceID        { return inv.id }
func (inv *Invoice) VendorID() VendorID   { return inv.vendorID }
func (inv *Invoice) Status() InvoiceStatus { return inv.status }
func (inv *Invoice) RawText() string      { return inv.rawText }
func (inv *Invoice) CreatedAt() time.Time { return inv.createdAt }
func (inv *Invoice) UpdatedAt() time.Time { return inv.updatedAt }
func (inv *Invoice) Version() int         { return inv.version }
func (inv *Invoice) VendorName() string   { return inv.vendorName }
func (inv *Invoice) InvoiceDate() time.Time { return inv.invoiceDate }
func (inv *Invoice) TotalAmount() Money   { return inv.totalAmount }
func (inv *Invoice) Currency() string     { return inv.currency }
func (inv *Invoice) RequiresManagerApproval() bool { return inv.requiresManagerApproval }

// LineItems คืน defensive copy ของ line items
func (inv *Invoice) LineItems() []InvoiceLineItem {
	result := make([]InvoiceLineItem, len(inv.lineItems))
	copy(result, inv.lineItems)
	return result
}

func (inv *Invoice) ExtractedAt() *time.Time {
	if inv.extractedAt == nil {
		return nil
	}
	t := *inv.extractedAt
	return &t
}

// IsDuplicate ตรวจสอบ duplicate: same vendor + same date + same amount
// Business rule: ป้องกันการส่ง invoice ซ้ำ
func (inv *Invoice) IsDuplicate(other *Invoice) bool {
	if inv.vendorID.String() != other.vendorID.String() {
		return false
	}
	// เปรียบเทียบวันที่ (ไม่สนใจ time ใน day)
	if !sameDate(inv.invoiceDate, other.invoiceDate) {
		return false
	}
	// เปรียบเทียบ amount
	return inv.totalAmount.Equals(other.totalAmount)
}

// ============================================================
// DOMAIN EVENTS MANAGEMENT
// ============================================================

func (inv *Invoice) addEvent(event DomainEvent) {
	inv.uncommittedEvents = append(inv.uncommittedEvents, event)
}

func (inv *Invoice) UncommittedEvents() []DomainEvent {
	return inv.uncommittedEvents
}

func (inv *Invoice) ClearEvents() {
	inv.uncommittedEvents = nil
}

// ============================================================
// PRIVATE HELPERS
// ============================================================

// calculateLineItemsTotal คำนวณผลรวมของ line items
func calculateLineItemsTotal(items []InvoiceLineItem) (Money, error) {
	if len(items) == 0 {
		return NewTHB(0)
	}

	// ใช้ currency ของ item แรก
	firstSubtotal := items[0].Subtotal()
	total, err := NewMoney(0, firstSubtotal.Currency())
	if err != nil {
		return Money{}, err
	}

	for _, item := range items {
		subtotal := item.Subtotal()
		total, err = total.Add(subtotal)
		if err != nil {
			return Money{}, fmt.Errorf("item %q: %w", item.Description(), err)
		}
	}

	return total, nil
}

// sameDate เปรียบเทียบวันที่ (ไม่สนใจ time)
func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// ============================================================
// SAMPLE THAI INVOICE FIXTURES
// ใช้ใน demo และ testing — ไม่ต้องการ API key
// ============================================================

// SampleInvoice1 — บริษัท ABC Technology จำกัด (Software License)
const SampleInvoice1 = `
ใบแจ้งหนี้ / TAX INVOICE
========================================
บริษัท ABC Technology จำกัด
เลขที่ผู้เสียภาษี: 0105565012345
99/1 ถนนสุขุมวิท แขวงคลองเตย เขตคลองเตย กรุงเทพฯ 10110
โทร: 02-123-4567

เรียน: บริษัท XYZ Retail จำกัด
เลขที่ใบแจ้งหนี้: INV-2567-00123
วันที่: 15 มีนาคม 2567 (15 March 2024)
ครบกำหนดชำระ: 14 เมษายน 2567

รายการ:
1. Microsoft Office 365 Business Standard (5 licenses)
   ราคา/หน่วย: 3,500 บาท x 5 = 17,500 บาท
2. Adobe Creative Cloud (2 licenses)
   ราคา/หน่วย: 8,000 บาท x 2 = 16,000 บาท
3. บริการติดตั้งและอบรม
   ราคา/หน่วย: 15,000 บาท x 1 = 15,000 บาท

รวมเงิน: 48,500 บาท
ภาษีมูลค่าเพิ่ม 7%: 3,395 บาท
ยอดรวมทั้งสิ้น: 51,895 บาท

วิธีการชำระเงิน: โอนเข้าบัญชี
ธนาคารกสิกรไทย เลขที่ 123-4-56789-0
`

// SampleInvoice2 — บริษัท สยามออฟฟิศ จำกัด (Office Supplies)
const SampleInvoice2 = `
ใบแจ้งหนี้
========================================
บริษัท สยามออฟฟิศ ซัพพลาย จำกัด
เลขที่ผู้เสียภาษี: 0105560054321
456 ถนนพระราม 4 แขวงสีลม เขตบางรัก กรุงเทพฯ 10500

เรียน: บริษัท XYZ Retail จำกัด
เลขที่: SO-2567-0456
วันที่: 20 มีนาคม 2567

รายการสั่งซื้อ:
- กระดาษ A4 80 แกรม (10 รีม)    ราคา 120 บาท/รีม    1,200 บาท
- ปากกาลูกลื่น (50 ด้าม)         ราคา 15 บาท/ด้าม       750 บาท
- แฟ้มเอกสาร (20 แฟ้ม)          ราคา 35 บาท/แฟ้ม       700 บาท
- หมึกพริ้นเตอร์ HP (4 ตลับ)    ราคา 850 บาท/ตลับ    3,400 บาท

รวม: 6,050 บาท
VAT 7%: 423.50 บาท
ยอดสุทธิ: 6,473.50 บาท
`

// SampleInvoice3 — บริษัท ไอทีโซลูชั่น จำกัด (Cloud Services - ยอดสูง)
const SampleInvoice3 = `
TAX INVOICE / ใบกำกับภาษี
========================================
บริษัท ไทยคลาวด์โซลูชั่น จำกัด (มหาชน)
เลขที่ผู้เสียภาษี: 0107558011111
200 อาคารเจดีย์ ถนนรัชดาภิเษก กรุงเทพฯ 10310

Invoice No: TCS-2567-0789
Invoice Date: 1 เมษายน 2567
Due Date: 30 เมษายน 2567

Bill To: บริษัท Enterprise Corp จำกัด

Service Description:
1. AWS Cloud Infrastructure (Monthly)      250,000 บาท
2. Managed Kubernetes Service (3 nodes)    180,000 บาท
3. Data Backup & DR Service               120,000 บาท
4. 24/7 NOC Monitoring Service             90,000 บาท
5. Professional Services (40 hours)       360,000 บาท
   @ 9,000 บาท/hour

Subtotal:    1,000,000 บาท
VAT 7%:         70,000 บาท
Total:       1,070,000 บาท

** หมายเหตุ: ใบแจ้งหนี้นี้มูลค่าเกิน 1,000,000 บาท
   ต้องการลายเซ็น Manager อนุมัติ **
`

// SampleInvoice4 — บริษัท เชฟบอม จำกัด (Catering)
const SampleInvoice4 = `
ใบแจ้งหนี้ค่าบริการ
========================================
ร้านอาหาร เชฟบอม เคเทอริ่ง
เลขที่: 0105562098765
112 ซอยสาทร 12 แขวงยานนาวา เขตสาทร กรุงเทพฯ 10120
Line: @chefbom_catering

ใบแจ้งหนี้เลขที่: CB-2567-0221
วันที่: 5 เมษายน 2567

รายการบริการจัดเลี้ยง (งานสัมมนา 50 คน วันที่ 10 เม.ย. 2567):
- อาหารกลางวัน Set Menu A (50 ที่)    @ 350 บาท/ที่    17,500 บาท
- Coffee Break ช่วงเช้า (50 คน)       @ 80 บาท/คน      4,000 บาท
- Coffee Break ช่วงบ่าย (50 คน)       @ 80 บาท/คน      4,000 บาท
- น้ำดื่ม + เครื่องดื่ม (ทั้งวัน)     @ 60 บาท/คน      3,000 บาท
- ค่าบริการและอุปกรณ์                                   2,500 บาท

รวมค่าบริการ: 31,000 บาท
ภาษีมูลค่าเพิ่ม 7%: 2,170 บาท
ยอดชำระทั้งสิ้น: 33,170 บาท
`

// SampleInvoice5 — บริษัท แอดเวอร์ไทซิ่ง จำกัด (Marketing)
const SampleInvoice5 = `
INVOICE
========================================
Creative Media Agency Co., Ltd.
บริษัท ครีเอทีฟ มีเดีย เอเจนซี่ จำกัด
Tax ID: 0105561077890
88/8 อาคารไทม์สแควร์ ชั้น 15 ถนนสุขุมวิท 12-14
กรุงเทพฯ 10110

Invoice Number: CMA-APR-2567-045
Date: 10 เมษายน 2567 / 10 April 2024
Payment Terms: Net 30

Client: บริษัท TechStartup Thailand จำกัด

Services Rendered (Q1 2024 Campaign):
1. Social Media Management (3 platforms, 1 month)
   Facebook, Instagram, TikTok                      25,000 บาท
2. Content Creation (16 posts + 4 videos)           40,000 บาท
3. Google Ads Management + Ad Spend Budget          55,000 บาท
4. Influencer Campaign Coordination (5 KOLs)        30,000 บาท
5. Monthly Report + Analytics                        5,000 บาท

Net Amount:    155,000 บาท
VAT (7%):       10,850 บาท
Total Due:     165,850 บาท

Bank: ธนาคารไทยพาณิชย์ / SCB
Account: 123-456789-0 (บจก. ครีเอทีฟ มีเดีย เอเจนซี่)
`
