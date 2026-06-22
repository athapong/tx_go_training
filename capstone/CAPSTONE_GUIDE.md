# Smart Invoice Processing Service — Capstone Guide

## Overview

โปรเจกต์ capstone นี้รวม concepts จากทุก module ของคอร์ส Advanced Go Enterprise + AI:

| Module | Concept | ไฟล์ใน Capstone |
|--------|---------|-----------------|
| 2 | Concurrency / Worker Pool | `processing/worker_pool.go` |
| 3 | DDD (Domain-Driven Design) | `domain/invoice.go`, `domain/repository.go` |
| 4 | CQRS | `application/commands.go` |
| 7 | AI Integration | `ai/extractor.go` |
| 8 | Observability | `observability/setup.go` |

---

## How to Run

### Prerequisites

- Go 1.22+ (ใช้ `go 1.23` ตาม go.mod)
- ไม่ต้องการ database หรือ external services (ใช้ in-memory)

### Quick Start

```bash
# จาก root ของ project
go run ./capstone/

# หรือ build แล้วรัน
go build -o ./bin/capstone ./capstone/ && ./bin/capstone
```

### With OpenAI (optional)

```bash
OPENAI_API_KEY=sk-... go run ./capstone/
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_ADDR` | `:8080` | HTTP server address |
| `LOG_LEVEL` | `info` | debug / info / warn / error |
| `LOG_FORMAT` | `json` | json / text |
| `WORKER_COUNT` | `5` | Number of concurrent workers |
| `ENVIRONMENT` | `development` | development / staging / production |
| `OPENAI_API_KEY` | (empty) | ถ้าไม่ใส่จะใช้ MockExtractor |

---

## Sample curl Commands

### 1. Health Check

```bash
curl http://localhost:8080/health | jq
```

### 2. Submit Invoice (Single)

```bash
curl -X POST http://localhost:8080/invoices \
  -H "Content-Type: application/json" \
  -d '{
    "invoice_id": "INV-2567-TEST-001",
    "vendor_id": "VENDOR-TEST-001",
    "source": "api",
    "raw_text": "ใบแจ้งหนี้\nบริษัท เทสต์ จำกัด\nรายการ: Website Development\nจำนวน 1 รายการ ราคา 50,000 บาท\nVAT 7%: 3,500 บาท\nยอดรวมทั้งสิ้น: 53,500 บาท"
  }' | jq
```

### 3. Submit Thai Software License Invoice

```bash
curl -X POST http://localhost:8080/invoices \
  -H "Content-Type: application/json" \
  -d '{
    "invoice_id": "INV-ABC-2567-001",
    "vendor_id": "VENDOR-ABC-TECH",
    "source": "api",
    "raw_text": "ใบแจ้งหนี้\nบริษัท ABC Technology จำกัด\nINV-2567-00999\nวันที่: 20 เมษายน 2567\n1. Microsoft Office 365 x5 ราคา 3,500 บาท = 17,500 บาท\n2. บริการติดตั้ง 15,000 บาท\nรวม 32,500 บาท\nVAT 7%: 2,275 บาท\nยอดสุทธิ: 34,775 บาท"
  }' | jq
```

### 4. List All Invoices

```bash
curl "http://localhost:8080/invoices?page=1&page_size=10" | jq
```

### 5. Filter by Status

```bash
# ดู invoices ที่ extracted แล้ว รอ approve
curl "http://localhost:8080/invoices?status=EXTRACTED" | jq

# ดู invoices ที่ approved แล้ว
curl "http://localhost:8080/invoices?status=APPROVED" | jq
```

### 6. Get Specific Invoice

```bash
# ดู sample invoice ที่ preload มา
curl http://localhost:8080/invoices/INV-SAMPLE-001 | jq
curl http://localhost:8080/invoices/INV-SAMPLE-002 | jq
curl http://localhost:8080/invoices/INV-SAMPLE-003 | jq
```

### 7. Approve Invoice

```bash
# Approve invoice ปกติ
curl -X POST http://localhost:8080/invoices/INV-SAMPLE-002/approve \
  -H "Content-Type: application/json" \
  -d '{"approved_by": "สมชาย ผู้จัดการ", "is_manager": false}' | jq

# Approve invoice ยอดสูง > 1,000,000 (ต้องเป็น manager)
curl -X POST http://localhost:8080/invoices/INV-SAMPLE-003/approve \
  -H "Content-Type: application/json" \
  -d '{"approved_by": "วิชัย ผู้อำนวยการ", "is_manager": true}' | jq

# ลองไม่ใส่ is_manager = true สำหรับ invoice ยอดสูง (ต้อง error)
curl -X POST http://localhost:8080/invoices/INV-SAMPLE-003/approve \
  -H "Content-Type: application/json" \
  -d '{"approved_by": "สมหมาย พนักงาน", "is_manager": false}' | jq
```

### 8. Reject Invoice

```bash
curl -X POST http://localhost:8080/invoices/INV-SAMPLE-004/reject \
  -H "Content-Type: application/json" \
  -d '{"rejected_by": "สุมาลี ฝ่ายบัญชี", "reason": "รายการไม่ตรงกับใบสั่งซื้อ PO-2567-111"}' | jq
```

### 9. Batch Submit (Concurrent Processing)

```bash
curl -X POST http://localhost:8080/invoices/batch \
  -H "Content-Type: application/json" \
  -d '{
    "invoices": [
      {
        "invoice_id": "BATCH-001",
        "vendor_id": "VENDOR-A",
        "source": "batch",
        "raw_text": "ใบแจ้งหนี้\nบริษัท อัลฟ่า จำกัด\nรายการ: Training Service\nยอดรวมทั้งสิ้น: 25,000 บาท"
      },
      {
        "invoice_id": "BATCH-002",
        "vendor_id": "VENDOR-B",
        "source": "batch",
        "raw_text": "Invoice\nBeta Co., Ltd\nCloud Service Monthly Fee\nTotal: 45,000 THB"
      },
      {
        "invoice_id": "BATCH-003",
        "vendor_id": "VENDOR-C",
        "source": "batch",
        "raw_text": "ใบแจ้งหนี้\nบริษัท แกมม่า จำกัด\nค่าเช่าสำนักงาน\nยอดสุทธิ: 80,000 บาท"
      }
    ]
  }' | jq
```

### 10. Metrics

```bash
curl http://localhost:8080/metrics
```

---

## Flow Diagram

```
POST /invoices
    │
    ▼
InvoiceCommandHandler.HandleSubmitInvoice()
    │
    ▼
InvoiceProcessor.ProcessSingle()
    │
    ├── domain.ReceiveInvoice()        ← สร้าง Invoice aggregate
    ├── repo.Save() [status=RECEIVED]  ← persist
    ├── invoice.StartProcessing()      ← state transition
    ├── ai.Extractor.Extract()         ← AI extraction
    ├── invoice.ApplyExtraction()      ← domain validation
    └── repo.Save() [status=EXTRACTED] ← persist
    │
    ▼
POST /invoices/{id}/approve
    │
    ▼
InvoiceCommandHandler.HandleApproveInvoice()
    │
    ├── repo.FindByID()
    ├── invoice.Approve()              ← domain business rule
    └── repo.Save() [status=APPROVED]
```

---

## Architecture Decisions

### 1. ทำไม DDD สำหรับ Invoice?
Invoice มี business rules ที่ซับซ้อน:
- State machine (5 states)
- Manager approval threshold
- Duplicate detection
- Total validation vs line items

การใส่ rules เหล่านี้ใน domain layer ทำให้ test ได้ง่ายและแยก business logic ออกจาก infrastructure

### 2. ทำไม CQRS?
- Commands (write): ต้องการ validation + domain events
- Queries (read): ต้องการ pagination, filtering — logic ต่างกันมาก
- ในอนาคต: สามารถ scale read/write แยกกันได้

### 3. ทำไม Worker Pool?
- Invoice processing ช้า (AI API call 200ms-2s)
- Batch ต้องการ concurrency
- Worker pool จำกัด resource usage (resource จำกัด -> เราไม่สร้าง goroutine โดยไม่ควบคุม)

### 4. MockExtractor vs RealExtractor?
- Development/Testing: MockExtractor — เร็ว, ไม่เสียเงิน, deterministic
- Production: OpenAIExtractor — แม่นยำ, รองรับ edge cases
- Interface ทำให้สลับได้โดยไม่แก้ business logic

---

## Extension Challenges

### Level 1 (Beginner)
1. เพิ่ม `DELETE /invoices/{id}` endpoint (soft delete — เพิ่ม `Cancelled` status)
2. เพิ่ม `PUT /invoices/{id}` สำหรับ update raw text (เฉพาะ `RECEIVED` status)
3. เพิ่ม filter by date range ใน `GET /invoices`

### Level 2 (Intermediate)
4. เพิ่ม `ExistsWithSameVendorDateAmount` check ใน `HandleSubmitInvoice` เพื่อ detect duplicates
5. เพิ่ม rate limiting middleware (X requests/minute per vendor)
6. เพิ่ม `GET /invoices/stats` endpoint ที่แสดง aggregated statistics

### Level 3 (Advanced)
7. แทน InMemoryRepository ด้วย PostgreSQL repository (ใช้ `database/sql` หรือ `pgx`)
8. เพิ่ม Event Sourcing: เก็บ domain events แทน state snapshots
9. แทน InMemoryEventBus ด้วย Redis Pub/Sub
10. เพิ่ม OpenTelemetry tracing (distributed tracing)
11. เพิ่ม retry logic ใน OpenAIExtractor ด้วย exponential backoff
12. สร้าง WebSocket endpoint `/invoices/stream` ที่ stream events แบบ realtime

### Level 4 (Expert)
13. Implement Event-Driven webhook notifications (call external URL เมื่อ invoice approved)
14. Multi-tenant support: แต่ละ tenant มี repository และ event bus แยกกัน
15. สร้าง CLI tool สำหรับ bulk import invoices จาก CSV

---

## Grading Rubric

### ความเข้าใจ DDD (20 คะแนน)
- [ ] อธิบาย Aggregate Root, Value Objects, Domain Events ได้
- [ ] บอกได้ว่าทำไม `Invoice.Approve()` อยู่ใน domain ไม่ใช่ application
- [ ] อธิบาย Dependency Inversion ระหว่าง domain และ infrastructure

### CQRS (15 คะแนน)
- [ ] อธิบายความแตกต่างระหว่าง Command และ Query
- [ ] บอกประโยชน์ของการแยก read/write path

### Concurrency (20 คะแนน)
- [ ] อธิบาย Worker Pool pattern
- [ ] บอกได้ว่า panic recovery ทำงานอย่างไร
- [ ] อธิบาย graceful shutdown

### AI Integration (20 คะแนน)
- [ ] อธิบาย MockExtractor vs RealExtractor
- [ ] บอก trade-offs ของ structured output (JSON schema) ใน OpenAI
- [ ] อธิบาย confidence score

### Observability (15 คะแนน)
- [ ] อธิบาย 3 pillars: Metrics, Logs, Traces
- [ ] บอกได้ว่า correlation ID ช่วยอะไร
- [ ] อธิบาย structured logging

### Extension (10 คะแนน)
- [ ] ทำ extension challenge อย่างน้อย 1 ข้อจาก Level 2+
- [ ] มี test coverage ≥ 60% สำหรับ domain layer
