# คู่มือ AI-Assisted Development สำหรับ Go

คู่มือนี้เป็น practical guide สำหรับ Go developer ที่อยากใช้ AI ให้ได้ประสิทธิภาพสูงสุด
เน้น **สิ่งที่ทำได้จริง** และ **สิ่งที่ต้องระวัง** จากประสบการณ์จริง

---

## ทำไม AI ถึงช่วย Go development ได้ดีเป็นพิเศษ

Go มี syntax ที่ verbose แต่ predictable — AI เรียนรู้ pattern ของ Go ได้ดีมาก:
- Boilerplate ที่ซ้ำๆ เช่น error handling, struct definitions, interface implementations
- Pattern ที่ชัดเจน เช่น worker pool, middleware chain, repository pattern
- Standard library ที่ stable และมีใช้บ่อย

**ข้อดีที่สุดของ AI ใน Go**: สร้าง scaffold ของ code เร็วมาก ให้เราโฟกัสที่ business logic

---

## Section 1: Prompt Templates สำหรับ Go

### Template A: สร้าง Code ใหม่

```
Context: [อธิบาย project และ package ที่กำลังทำ]

Task: สร้าง [ชื่อ component/function]

Requirements:
- [requirement 1]
- [requirement 2]

Constraints:
- Go version: 1.23
- ต้องใช้ package: [packages ที่มีอยู่แล้ว]
- ห้ามใช้ third-party package นอกจาก: [list]
- Error handling: ใช้ fmt.Errorf("%w", err) สำหรับ wrapping

Output format:
- Package: [package name]
- Include: interface definition, concrete implementation, unit tests
- Comments: ภาษาไทย อธิบาย "ทำไม" ไม่ใช่ "ทำอะไร"
```

**ตัวอย่างจริง:**
```
Context: บริษัท e-commerce โปรเจกต์ order management ใน package `internal/payment`

Task: สร้าง PaymentGateway interface และ Omise implementation

Requirements:
- Support charge, refund, และ check status
- ต้องมี timeout 30 วินาที
- Log ทุก request/response โดยไม่ log card data

Constraints:
- Go 1.23
- ใช้ net/http standard library
- Error ต้องมี transaction ID สำหรับ debugging

Output: interface + struct + unit test ด้วย httptest.NewServer
```

---

### Template B: Code Review

```
Review Go code นี้ให้ผม โฟกัสที่:

1. Correctness: มี bug หรือ logic error ไหม?
2. Go idioms: code เป็น idiomatic Go ไหม?
3. Concurrency: ถ้ามี goroutines — มี race condition ไหม?
4. Error handling: จัดการ error ครบและถูกต้องไหม?
5. Performance: มี obvious bottleneck ไหม?

สิ่งที่ไม่ต้องบอก:
- Style issues ที่ gofmt จัดการได้
- Comment ว่า "code ดีมาก" โดยไม่มีเหตุผล

[วาง code ที่นี่]
```

---

### Template C: Test Generation

```
สร้าง unit tests สำหรับ function นี้:

[วาง function/struct ที่นี่]

Test cases ที่ต้องครอบคลุม:
- Happy path: input ปกติ
- Edge cases: empty input, nil pointer, zero values
- Error cases: invalid input, external dependency fail
- Boundary: max/min values

Requirements:
- ใช้ testing package มาตรฐาน
- ใช้ table-driven tests ([]struct{})
- Mock external dependencies ด้วย interface
- ชื่อ test: TestFunctionName_ScenarioDescription

อย่าสร้าง test ที่ test implementation details — test behavior เท่านั้น
```

---

### Template D: Refactoring

```
Refactor Go code นี้โดย:

Goal: [อธิบาย goal เช่น "ให้ testable มากขึ้น" หรือ "แยก concerns ออกจากกัน"]

Constraints:
- รักษา public API เดิม (ไม่เปลี่ยน function signatures)
- ต้อง backward compatible
- ไม่เพิ่ม third-party dependencies ใหม่

[วาง code ที่นี่]

บอกด้วยว่า:
- เปลี่ยนอะไรบ้าง และทำไม
- สิ่งที่ยังคงเหมือนเดิมและทำไม
- trade-offs ของการ refactor นี้
```

---

### Template E: Debug / อธิบาย Error

```
Go program ของผมเจอ error นี้:

```
[วาง error message และ stack trace]
```

Code ที่เกี่ยวข้อง:
[วาง code]

สิ่งที่ผมลองแล้ว:
[อธิบาย]

ช่วย:
1. อธิบายว่า error นี้หมายความว่าอะไร
2. บอกสาเหตุที่เป็นไปได้ทั้งหมด
3. เสนอ fix พร้อม explain ว่าทำไม fix นั้นถูก
```

---

## Section 2: AI ทำได้ดี vs ต้องระวัง

### AI ทำได้ดีมาก (Delegate ได้)

| งาน | เหตุผล |
|-----|--------|
| สร้าง struct และ interface | Pattern ชัดเจน, repetitive |
| CRUD repository layer | Boilerplate จำนวนมาก |
| Error type definitions | Standard pattern |
| Table-driven unit tests | Format ตายตัว |
| Middleware implementations | Pattern เดิมซ้ำๆ |
| JSON marshal/unmarshal | Mechanical task |
| Goroutine worker pool | Pattern ที่ดี แต่ verbose |
| HTTP handler boilerplate | Repetitive มาก |
| Protobuf/gRPC service stubs | Generated code |

### ต้องระวัง (ตรวจสอบเสมอ)

| งาน | ความเสี่ยง |
|-----|-----------|
| Business logic ซับซ้อน | AI ไม่รู้ domain rules |
| Concurrency design | Race conditions ละเอียดมาก |
| Database transactions | Atomicity, rollback logic |
| Security-critical code | Auth, encryption, input validation |
| Performance optimization | ไม่รู้ production workload |
| System architecture | ไม่รู้ constraints ทั้งหมด |

### AI มักทำผิดสำหรับ Go โดยเฉพาะ

```go
// ❌ AI ชอบ close channel ผิดที่
// AI อาจ close channel จาก receiver ซึ่งผิด
// Go convention: sender closes channel เท่านั้น

// ❌ Context propagation ที่ขาดหาย
func process(items []Item) error {  // ❌ ไม่รับ ctx
    resp, err := http.Get(url)       // ❌ ไม่ pass context
}

// ✅ ที่ถูกต้อง
func process(ctx context.Context, items []Item) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    resp, err := client.Do(req)
}

// ❌ Error handling ที่ AI ลืม
result, _ := someOperation()  // ❌ ignore error

// ❌ Mutex ที่ lock ไม่ถูก scope
func (s *Service) Update(id int) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    data := s.fetchFromDB(id)  // ❌ อย่า call external ขณะ lock!
    s.cache[id] = data
}
```

---

## Section 3: Workflow ที่แนะนำ

### Workflow: AI Draft → Human Review → AI Refine → Human Test

```
1. Human: เขียน interface/spec ที่ชัดเจน
         ↓
2. AI Draft: สร้าง implementation แรก
         ↓
3. Human Review: อ่านทุกบรรทัด ตั้งคำถาม
         ↓
4. AI Refine: แก้ตาม feedback (ส่ง diff ให้ review)
         ↓
5. Human Test: เขียน/รัน tests ด้วยตนเอง
         ↓
6. Production: Deploy
```

### เวลาที่ใช้จริง (estimate)

| งาน | Human Only | AI-Assisted |
|-----|-----------|-------------|
| CRUD endpoints (5 operations) | 4-6 ชั่วโมง | 45-90 นาที |
| Unit tests สำหรับ service layer | 2-3 ชั่วโมง | 30-45 นาที |
| Middleware implementation | 1-2 ชั่วโมง | 15-30 นาที |
| Complex business logic | 3-5 ชั่วโมง | 2-4 ชั่วโมง (ประหยัดน้อยกว่า) |

---
### คำสั่งที่ใช้บ่อยสำหรับ Go

```bash
# ให้ Claude อ่าน codebase ก่อน แล้วช่วยสร้าง feature
> อ่าน package internal/order ก่อน แล้วเพิ่ม method CancelOrder ใน OrderService

# Review code ที่เพิ่งเขียน
> review handler/payment_handler.go และบอก issues

# Fix failing test
> test TestPaymentService_Charge fail อยู่ ช่วย debug

# สร้าง tests
> สร้าง unit tests สำหรับ repository/user_repo.go ให้ coverage ≥ 80%

# Refactor
> refactor service/notification.go ให้ testable โดยใช้ interface สำหรับ email sender
```

### Tips สำหรับ Claude Code ใน Go Projects

**บอก context ให้ครบ:**
```
# ไม่ดี
> เพิ่ม cache

# ดี  
> เพิ่ม in-memory cache สำหรับ GetProduct ใน service/product.go
  ใช้ sync.Map, TTL 5 นาที, invalidate เมื่อ UpdateProduct ถูกเรียก
  ดู interface ใน internal/cache/cache.go ด้วย
```

**ให้ Claude รัน tests:**
```
> เพิ่ม feature แล้วรัน go test ./... ให้ด้วย ถ้า fail ให้ fix จนผ่าน
```

---

## Section 5: Red Flags — เมื่อ AI-Generated Go Code น่าสงสัย

### 1. Goroutine Leak

```go
// ❌ สัญญาณอันตราย: goroutine ที่ไม่มีทางออก
func processForever(data <-chan Item) {
    go func() {
        for item := range data {  // ถ้าไม่มีใคร close data → leak
            process(item)
        }
    }()
}

// ✅ ต้องมี context cancellation
func processWithContext(ctx context.Context, data <-chan Item) {
    go func() {
        for {
            select {
            case item, ok := <-data:
                if !ok { return }
                process(item)
            case <-ctx.Done():
                return
            }
        }
    }()
}
```

### 2. Mutex Misuse

```go
// ❌ Lock ในขอบเขตที่ใหญ่เกินไป หรือ deadlock
func (s *Store) GetOrCreate(key string) Value {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if val, ok := s.data[key]; ok {
        return val
    }
    
    // ❌ ถ้า create() call s.GetOrCreate ด้านใน → deadlock!
    val := s.create(key)  
    s.data[key] = val
    return val
}
```

### 3. Error Swallowing

```go
// ❌ AI บางครั้ง ignore errors ที่สำคัญ
func saveUser(u User) {
    db.Save(u)         // ❌ ไม่ check error
    sendEmail(u.Email) // ❌ ไม่ check error  
    log.Println("done")
}
```

### 4. Interface ที่ใหญ่เกินไป

```go
// ❌ AI มักสร้าง interface ที่ต้อง implement ทุกอย่าง
type UserRepository interface {
    Create(u User) error
    Read(id int) (User, error)
    Update(u User) error
    Delete(id int) error
    List(filter Filter) ([]User, error)
    Count(filter Filter) (int, error)
    Exists(id int) (bool, error)
    // ... 10 methods อีก
}

// ✅ ควรแยกตาม use case (Interface Segregation)
type UserCreator interface { Create(u User) error }
type UserReader interface { Read(id int) (User, error) }
// แต่ละ service ใช้ interface ที่ตัวเองต้องการเท่านั้น
```

### 5. Missing Context Propagation

```go
// ❌ สัญญาณ: ไม่มี ctx ใน function chain
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    order := parseOrder(r)    // ❌ ไม่ pass r.Context()
    result := h.svc.Create(order)  // ❌ service ไม่รับ ctx
    json.NewEncoder(w).Encode(result)
}

// ✅ Context ต้องไหลจาก HTTP request ลงไปถึง DB
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    order := parseOrder(r)
    result, err := h.svc.Create(ctx, order)  // ✅ pass ctx
    // ...
}
```

---

## Section 6: Prompt Engineering Tips เฉพาะ Go

### ระบุ error handling style

```
ใช้ error wrapping แบบ Go 1.13+:
  return fmt.Errorf("context: %w", err)  // ✅ สามารถ unwrap ได้
ไม่ใช้:
  return errors.New(err.Error())  // ❌ ทำให้ type information หาย
```

### ระบุ logging approach

```
ใช้ structured logging ด้วย log/slog (Go 1.21+):
  slog.Info("user created", "id", userID, "email", email)
ไม่ใช้:
  log.Printf("user created: id=%d email=%s", userID, email)
```

### ระบุ testing style

```
ใช้ testify สำหรับ assertions:
  assert.Equal(t, expected, actual)
  require.NoError(t, err)  // ถ้า fail ให้หยุดทันที

หรือถ้าใช้ standard library เท่านั้น:
  if got != want { t.Errorf("got %v, want %v", got, want) }
```

### บอก package structure ที่ใช้

```
Project ใช้ Clean Architecture:
  cmd/         — main packages
  internal/    — private packages
    domain/    — entities, value objects
    service/   — business logic
    repository/— data access
    handler/   — HTTP handlers
  pkg/         — public packages

AI จะสร้าง code ใน package ที่ถูกต้องและ import path ที่ถูก
```

---

## Section 7: เมื่อไหร่ที่ไม่ควรใช้ AI

AI ไม่ใช่คำตอบสำหรับทุกอย่าง ต่อไปนี้คือสถานการณ์ที่ควรระวัง:

### อย่าใช้ AI เมื่อ...

1. **ไม่เข้าใจ domain เลย**: ถ้า spec ยังไม่ชัด AI จะ amplify ความสับสน
   สิ่งที่ควรทำ: คุยกับ stakeholder ก่อน เขียน spec ด้วยตนเอง

2. **Security-critical path**: Authentication, authorization, encryption
   เหตุผล: Bug ใน security code มีผลกระทบสูง AI อาจ miss subtle vulnerabilities

3. **Complex data migration**: DB schema changes ที่มี existing data
   เหตุผล: Rollback ยาก data loss ร้ายแรง ต้องเข้าใจ data ดีมาก

4. **เมื่อทีมยังไม่ review ได้**: ถ้าไม่มีคนในทีมที่เข้าใจ code ดีพอจะ review
   เหตุผล: AI code ที่ไม่ถูก review คือ technical debt ที่ซ่อนอยู่

5. **Deadline กระชั้นชิด + unfamiliar domain**: AI เร็วขึ้นก็จริง แต่ bug จาก AI code ก็ debug ยากกว่า
   เหตุผล: เวลาที่ประหยัดตอนเขียน อาจหายไปกับ debug ทีหลัง

### คำถามที่ควรถามตัวเองก่อนใช้ AI

- ฉันสามารถอธิบาย code นี้ให้คนอื่นเข้าใจได้ไหม?
- ถ้า bug เกิดขึ้น ฉันจะรู้ว่าต้อง debug ที่ไหน?
- Code นี้มี test ที่จะ catch regression ไหม?
- มีคนในทีมที่จะ review และเข้าใจ code นี้ไหม?

ถ้าตอบ "ไม่" ในข้อไหน → ต้องเข้าใจมากกว่านี้ก่อน
