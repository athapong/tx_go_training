# Module 7: AI Integration for Go Applications

## Overview

Module นี้เป็นการ integrate AI/LLM เข้ากับ Go applications
เน้นทั้ง technical implementation และ practical workflow สำหรับ Go developer

**Prerequisites**: Go basics, HTTP, interfaces, goroutines, error handling

---

## API Key Setup

### การ demo โดยไม่ expose API key จริง

ทุก demo ใน module นี้รันได้โดยไม่ต้องใช้ API key จริง (mock mode)
การสาธิตบน projector ให้ทำดังนี้:

```bash
# รัน demo ทั้งหมดใน mock mode (default)
go run module7/llm_client/main.go
go run module7/document_processor/main.go
go run module7/rag/main.go
```

### ถ้าต้องการ demo ด้วย API จริง (optional)

```bash
# Set environment variables — ห้ามใส่ใน code!
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."

# แก้ไข NewOpenAIClient("") → NewOpenAIClient(os.Getenv("OPENAI_API_KEY"))
```

**สำคัญมาก: ห้าม commit API keys ไปใน git**
ถ้าต้องการใช้ key จริงสำหรับ lab:
- ใช้ key ที่มี rate limit ต่ำ (เช่น gpt-3.5-turbo เท่านั้น)
- ลบ key หลัง lab เสร็จ
- ใช้ `.gitignore` กัน `.env` file

### .env สำหรับ lab

```bash
# .env (อย่า commit!)
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
```

```go
// main.go — อ่าน key จาก env
import "os"
apiKey := os.Getenv("OPENAI_API_KEY")
client := NewOpenAIClient(apiKey)
// ถ้า apiKey == "" → mock mode อัตโนมัติ
```

---

## Mock Mode — รัน Lab โดยไม่ต้องมี API Key

### ทุก demo ใน Module 7 มี mock mode ในตัว

**llm_client/main.go**: ถ้า `apiKey == ""` → ใช้ `mockOpenAIResponse()` และ `mockOpenAIStream()`

**document_processor/main.go**: ใช้ `MockLLMClient` เสมอ ไม่มี real API calls เลย

**rag/main.go**: ใช้ `MockEmbeddingClient` และ `RAGMockLLM` เสมอ

### Mock ทำงานอย่างไร

```go
// MockLLMClient ดู keywords ใน system prompt และ return response ที่เหมาะสม
func (m *MockLLMClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
    time.Sleep(m.latency) // จำลอง network latency 100ms
    
    switch {
    case strings.Contains(systemPrompt, "classify"):
        return m.mockClassify(userPrompt), nil
    case strings.Contains(systemPrompt, "INVOICE"):
        return m.mockExtractInvoice(userPrompt), nil
    // ...
    }
}
```

Mock responses ถูก hardcode ให้ match กับ sample documents ที่มีให้ ดังนั้น output จะสมจริงและ consistent

---

## การเพิ่ม go-openai Package

ต้องเพิ่ม package ใน go.mod ก่อนสอน lab:

```bash
cd /path/to/tx_go_training
go get github.com/sashabaranov/go-openai
```

go.mod จะมี:
```
require (
    github.com/sashabaranov/go-openai v1.x.x
)
```

**หมายเหตุ**: llm_client/main.go implement HTTP โดยตรง (ไม่ใช้ go-openai SDK) เพื่อให้เข้าใจ internals
ในส่วน lab สามารถให้ refactor ไปใช้ go-openai SDK แทนได้

### ตัวอย่างการใช้ go-openai SDK จริง

```go
import openai "github.com/sashabaranov/go-openai"

client := openai.NewClient(apiKey)

resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
    Model: openai.GPT4o,
    Messages: []openai.ChatCompletionMessage{
        {Role: openai.ChatMessageRoleSystem, Content: "You are helpful"},
        {Role: openai.ChatMessageRoleUser, Content: "Hello"},
    },
    MaxTokens: 500,
})
// resp.Choices[0].Message.Content
```

---

## Prompt Engineering Tips สำหรับ Go Code Generation

### Tips ที่ได้ผลดีที่สุด (จากประสบการณ์จริง)

#### 1. ให้ context ครบ ก่อน task

```
# ไม่ดี
> สร้าง cache สำหรับ user service

# ดี
> Project: e-commerce platform
> Package: internal/service/user (อ่านไฟล์แนบ)
> Current: UserService อ่าน DB ทุก request
> Goal: เพิ่ม cache ด้วย sync.Map, TTL 5 min
> Constraint: ต้อง thread-safe, invalidate เมื่อ user อัปเดต
```

#### 2. ระบุ style conventions ของ project

- Error wrapping: `fmt.Errorf("funcName: %w", err)` หรือ custom error types?
- Logging: `log/slog`, `logrus`, `zap`?
- Testing: standard library หรือ `testify`?
- Context: ทุก function รับ `ctx context.Context` เป็น parameter แรกไหม?

#### 3. ให้ตัวอย่าง existing code

วิธีที่ดีที่สุดคือให้ Claude อ่าน existing code ก่อน:
```
> อ่าน internal/service/product.go ก่อน แล้วสร้าง OrderService ในรูปแบบเดียวกัน
```

#### 4. ขอ explanation พร้อม code

```
> อธิบายด้วยว่า:
> 1. ทำไมเลือก approach นี้
> 2. trade-offs ที่ควรรู้
> 3. สิ่งที่ต้องระวังเมื่อ deploy
```

#### 5. Iterative refinement

```
# Round 1: ขอ skeleton
> สร้าง PaymentService interface และ struct

# Round 2: เพิ่ม details
> เพิ่ม retry logic และ circuit breaker ใน Charge method

# Round 3: Tests
> สร้าง unit tests ครอบคลุม edge cases: timeout, network error, invalid card
```

---

## RAG Common Pitfalls

### 1. Chunk Size ที่ไม่เหมาะสม

**ปัญหา: Chunk เล็กเกินไป (< 100 tokens)**
- Embedding ขาด context → similarity search แย่ลง
- ต้องส่ง chunks จำนวนมากให้ LLM → เพิ่ม cost
- ตัวอย่าง: แบ่งเป็นประโยค → แต่ละประโยคไม่มี context พอ

**ปัญหา: Chunk ใหญ่เกินไป (> 1000 tokens)**
- Embedding ถูก "dilute" → ความหมายหลายอย่างรวมกัน
- Retrieval ดึง irrelevant information มาด้วย
- อาจ exceed context window ของ LLM

**แนะนำ: 200-500 tokens พร้อม 50-100 token overlap**

```go
// ตัวอย่างการ tune ใน module7/rag/main.go
chunker := NewDocumentChunker(
    500,  // maxTokens — ลองปรับ 200, 300, 500 ดูผลต่าง
    50,   // overlap  — ช่วย maintain context ระหว่าง chunks
)
```

**Lab activity**: เปลี่ยน maxTokens และดูว่า retrieval quality เปลี่ยนอย่างไร

---

### 2. Embedding Drift

**คืออะไร?** เมื่อ embedding model เปลี่ยน version (เช่น OpenAI เปลี่ยนจาก `text-embedding-ada-002` → `text-embedding-3-small`) embeddings เก่าและใหม่ไม่ compatible กัน

**ผลกระทบ**: Similarity scores ผิดพลาด → ดึง chunks ผิด → คำตอบแย่ลง

**วิธีป้องกัน**:
```go
// เก็บ model version พร้อม embedding ใน metadata
type Chunk struct {
    Embedding      []float64
    EmbeddingModel string    // เช่น "text-embedding-3-small"
    EmbeddedAt     time.Time
}

// ก่อน search: ตรวจว่า query ใช้ model เดียวกับ stored embeddings
func (vs *VectorStore) Search(ctx context.Context, q []float64, model string) {
    // ถ้า model ไม่ตรง → log warning หรือ reject
}
```

**Mitigation**: Re-embed เอกสารทั้งหมดเมื่อ upgrade model version

---

### 3. Hallucination ใน RAG

**ความเข้าใจผิดที่พบบ่อย**: "RAG กำจัด hallucination ได้"

**ความจริง**: RAG ลด hallucination แต่ไม่ทั้งหมด สาเหตุที่ยังเกิดคือ:
- Chunks ที่ดึงมาไม่มีคำตอบที่ถามจริงๆ → LLM เดา
- LLM ผสม context จาก chunks กับ training data
- Context window เต็ม → LLM ไม่เห็น chunks ที่เกี่ยวข้อง

**วิธีลด Hallucination ใน RAG**:

```go
// 1. Threshold: ถ้า similarity score ต่ำเกินไป → บอกว่าหาไม่พบ
const minSimilarityThreshold = 0.70

results, _ := store.Search(ctx, queryEmbedding, topK)
if len(results) == 0 || results[0].Score < minSimilarityThreshold {
    return "ไม่พบข้อมูลที่เกี่ยวข้องในฐานข้อมูล", nil, nil
}

// 2. Grounding instruction ใน system prompt
systemPrompt := `ตอบโดยใช้ข้อมูลจาก context ด้านล่างเท่านั้น
ถ้าข้อมูลใน context ไม่เพียงพอ ให้บอกว่า "ไม่มีข้อมูลเพียงพอสำหรับคำถามนี้"
ห้ามเพิ่มข้อมูลจากความรู้ทั่วไปของตัวเอง

Context:
{{chunks}}`

// 3. Citation verification: แสดง source chunks ให้ user ตรวจสอบได้
answer += fmt.Sprintf("\n\nอ้างอิง:\n%s", formatSources(results))
```

---

### 4. Context Window Management

**ปัญหา**: ดึง top-K chunks มาเยอะเกิน → context window ของ LLM เต็ม

**ตัวเลข reference**:
- GPT-4o: 128K tokens context window
- Claude claude-opus-4-5: 200K tokens context window
- ค่าแนะนำ: ใช้ไม่เกิน 50% ของ context window เพื่อให้มีที่สำหรับ output

```
ถ้า chunk = 400 tokens และ topK = 5 → 2,000 tokens สำหรับ context
ระวัง: system prompt + question + chunks + output ต้องไม่เกิน limit
```

---

## Part 6: Cost Estimation

### ราคา API (อาจเปลี่ยนแปลง)

| Model | Input ($/1M tokens) | Output ($/1M tokens) |
|-------|--------------------|--------------------|
| GPT-4o | $2.50 | $10.00 |
| GPT-4o-mini | $0.15 | $0.60 |
| GPT-3.5-turbo | $0.50 | $1.50 |
| Claude claude-opus-4-5 | $3.00 | $15.00 |
| Claude claude-haiku-3-5 | $0.25 | $1.25 |
| text-embedding-3-small | $0.02 | — |

### ประมาณการค่าใช้จ่ายต่อคน (กรณีใช้ API จริง)

**Lab 1 (llm_client)**: ~50 requests × 500 tokens avg = ~25,000 tokens
- GPT-4o-mini: $0.015 (ถูกที่สุด, ดีสำหรับ lab)
- GPT-4o: ~$0.25

**Lab 2 (document_processor)**: ~20 documents × 3 LLM calls × 800 tokens avg = ~48,000 tokens
- GPT-4o-mini: $0.03
- GPT-4o: ~$0.48

**Lab 3 (RAG)**: ~500 embeddings + ~10 queries × 1500 tokens
- Embeddings (text-embedding-3-small): $0.001
- Queries (GPT-4o-mini): $0.015

- **ที่ดีที่สุด**: ใช้ mock mode สำหรับ lab ปกติ, ใช้ real API แค่ demo สั้นๆ

---


### AI Dev Workflow

— อ่านและ discuss WORKFLOW.md ร่วมกัน

— Hands-on: ให้แต่ละคนใช้ Claude Code สร้าง feature เล็กๆ
Task: "ใช้ Claude Code เพิ่ม pagination ให้กับ RAG query results"
- กำหนด spec เอง
- ใช้ AI draft code
- Review และ test

— Share ประสบการณ์: อะไรที่ AI ช่วยได้ vs ทำผิด

---

## Discussion Questions

### สำหรับ group discussion หลัง lab

**คำถามที่ 1: เมื่อไหร่ที่ไม่ควรใช้ AI ใน Go Service?**

ตัวอย่าง answers ที่ดี:
- Payment processing ที่ต้องการ auditability 100%
- Safety-critical systems (ระบบการแพทย์, การบิน)
- Real-time systems ที่ latency เป็น milliseconds
- Code ที่ทีมไม่เข้าใจพอจะ review

**คำถามที่ 2: Hallucination ใน Production RAG จะจัดการอย่างไร?**

ให้ช่วยกันคิด mitigation:
- Confidence score threshold
- Human review สำหรับ high-stakes decisions
- Citation + source display ให้ user ตรวจสอบ
- Fallback to "I don't know" ดีกว่า hallucinate

**คำถามที่ 3: Cost vs Accuracy Trade-off ใน LLM Selection**

Scenario: ระบบ classify เอกสาร 100,000 เอกสาร/วัน
- GPT-4o: accuracy 97%, cost $250/วัน
- GPT-4o-mini: accuracy 91%, cost $15/วัน
- Fine-tuned small model: accuracy 93%, cost $5/วัน + training cost

คำถาม: "ถ้าเป็น production system คุณจะเลือกอะไร และทำไม?"

**คำถามที่ 4: Vendor Lock-in ใน AI Integration**

"ถ้า OpenAI หยุดให้บริการหรือขึ้นราคา 10x คุณจะทำอย่างไร?"
เชื่อมกับ LLMClient interface design ในโค้ด — interface ช่วยลด coupling ได้จริงแค่ไหน?

**คำถามที่ 5: Data Privacy ใน Thai Enterprise**

"ข้อมูลลูกค้า (ชื่อ, ที่อยู่, ข้อมูลทางการเงิน) ส่งให้ LLM API ภายนอกได้ไหม?"
ประเด็น: PDPA, data residency, PCI DSS
แนวทาง: PII redaction ก่อนส่ง, on-premise LLM (Ollama), synthetic data สำหรับ dev/test

---

## Common Mistakes จาก Lab

### Mistake ที่พบบ่อยที่สุด

**1. Copy mock code ไปใช้ใน production**
```go
// ❌ นักศึกษาบางคนเอา mock โดยตรง
client := NewMockLLMClient() // ใช้ใน production!
```
สอนให้เข้าใจ: mock ใช้ใน tests และ local dev เท่านั้น

**2. ไม่ handle LLM response errors ครบ**
```go
resp, _ := llm.Complete(ctx, req) // ❌ ignore error
```
LLM API มี rate limits, timeouts — ต้อง handle เสมอ

**3. ส่ง PII ให้ LLM โดยไม่คิด**
ให้นักศึกษา classify ข้อมูลก่อน: อะไรส่งได้ อะไรต้อง redact

**4. ไม่ validate structured output**
```go
var result InvoiceData
json.Unmarshal(llmResponse, &result)
// ❌ ไม่ตรวจว่า result valid หรือเปล่า
if result.TotalAmount <= 0 { // ควรตรวจแบบนี้
    return nil, errors.New("invalid invoice: zero amount")
}
```

**5. RAG chunk size ไม่เหมาะกับเนื้อหา**
ให้ทดลอง chunk sizes ต่างกัน และ measure retrieval quality

---

## Resources สำหรับ Instructor

### ลิงก์สำคัญ

- OpenAI API Reference: https://platform.openai.com/docs/api-reference
- Anthropic API Reference: https://docs.anthropic.com/en/api/
- go-openai SDK: https://github.com/sashabaranov/go-openai
- OpenAI Tokenizer (นับ tokens): https://platform.openai.com/tokenizer
- Prompt engineering guide: https://platform.openai.com/docs/guides/prompt-engineering

### Tools สำหรับ Demo

```bash
# ดู token count ของ text
# ติดตั้ง: pip install tiktoken
python3 -c "
import tiktoken
enc = tiktoken.encoding_for_model('gpt-4o')
text = 'ใบกำกับภาษีจากบริษัท เทคโนโลยีไทย จำกัด ลงวันที่ 15 พฤศจิกายน 2567'
print(f'Tokens: {len(enc.encode(text))}')
"
```

### Mock Mode สำหรับ Continuous Demo

ถ้าต้องการ loop demo ไม่หยุด:

```bash
while true; do
    go run module7/document_processor/main.go
    sleep 5
done
```

---

## Quick Checklist

- [ ] Go 1.23 ติดตั้งแล้ว
- [ ] รัน `go run module7/llm_client/main.go` แล้ว output ถูกต้อง
- [ ] รัน `go run module7/document_processor/main.go` แล้ว output ถูกต้อง
- [ ] รัน `go run module7/rag/main.go` แล้ว output ถูกต้อง
- [ ] เตรียม discussion questions สำหรับแต่ละ session
- [ ] ถ้าใช้ real API: ทดสอบ API key
