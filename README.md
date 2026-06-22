# Advanced Enterprise AI with Go 🚀

หลักสูตรการพัฒนา Enterprise Applications ด้วย Golang พร้อม AI Integration

## 📚 โครงสร้างคอร์ส

| Module | หัวข้อ | เนื้อหาหลัก |
|--------|--------|------------|
| **Module 1** | Runtime & Memory Internals | Goroutine scheduler, Memory model, GC tuning |
| **Module 2** | Advanced Concurrency | Worker pools, Pipelines, Context patterns |
| **Module 3** | Domain-Driven Design | DDD patterns, Clean Architecture, Repository pattern |
| **Module 4** | Performance & Optimization | Profiling, Benchmarking, CPU/Memory optimization |
| **Module 5** | gRPC & Distributed Systems | Protocol Buffers, gRPC patterns, Circuit breakers |
| **Module 6** | Advanced Testing | Unit, Integration, Fuzz testing, Race detection |
| **Module 7** | AI Integration | LLM clients, RAG systems, Embeddings, AI workflows |
| **Module 8** | Enterprise Observability | Prometheus, Grafana, Jaeger, OpenTelemetry |
| **Capstone** | Smart Invoice Processing | Full integration project |

---

## 🎯 เป้าหมายการเรียนรู้

หลังจบคอร์สนี้ คุณจะสามารถ:

- ✅ เข้าใจ Go runtime และ optimize performance
- ✅ เขียน concurrent applications ที่ปลอดภัยและมีประสิทธิภาพ
- ✅ ออกแบบ enterprise architecture ด้วย DDD
- ✅ สร้าง gRPC services และ distributed systems
- ✅ Integrate AI/LLM เข้ากับ Go applications
- ✅ ติดตั้ง observability stack สำหรับ production
- ✅ เขียน tests ที่ครอบคลุมและมีคุณภาพ

---

## ⚡ Quick Start

### 1. ตรวจสอบความพร้อม

```bash
# Clone repository แล้ว cd เข้ามา
cd tx_go_training

# ตรวจสอบว่าติดตั้งเครื่องมือครบหรือยัง
chmod +x check_setup.sh
./check_setup.sh
```

### 2. ติดตั้ง Dependencies

```bash
# ดาวน์โหลด Go dependencies
go mod download

# ตรวจสอบว่าดาวน์โหลดครบ
go mod verify
```

### 3. รัน Labs แรก

```bash
# Module 1: Runtime Internals
cd module1
go run memory_model.go

# Module 7: RAG System (ไม่ต้อง OpenAI key)
cd ../module7/rag
go run main.go

# Capstone: Smart Invoice Processing
cd ../../capstone
go run .
```

---

## 📖 Documentation

- **[QUICK_START.md](./QUICK_START.md)** - เริ่มต้นใช้งาน
- **[SETUP_GUIDE.md](./SETUP_GUIDE.md)** - คู่มือการติดตั้งแบบละเอียด
- **[check_setup.sh](./check_setup.sh)** - Script ตรวจสอบการติดตั้ง

### Documentation แต่ละ Module

- [Module 1: Runtime Internals](./module1/MODULE1_RUNTIME_INTERNALS.md)
- [Module 2: Concurrency](./module2/MODULE2_TEACHING_NOTES.md)
- [Module 3: DDD](./module3/MODULE3_CONCEPTS.md)
- [Module 4: Performance](./module4/MODULE4_TEACHING_NOTES.md)
- [Module 5: gRPC](./module5/MODULE5_TEACHING_NOTES.md)
- [Module 6: Testing](./module6/MODULE6_TEACHING_NOTES.md)
- [Module 7: AI Integration](./module7/MODULE7_TEACHING_NOTES.md)
- [Module 8: Observability](./module8/MODULE8_TEACHING_NOTES.md)
- [Capstone Guide](./capstone/CAPSTONE_GUIDE.md)

---

## 🛠️ เครื่องมือที่ต้องใช้

### Required (จำเป็น)
- **Go 1.25+** - [ดาวน์โหลด](https://go.dev/dl/)
- **Git** - Version control
- **Protocol Buffers** - สำหรับ gRPC (Module 5)
- **Docker & Docker Compose** - สำหรับ Observability (Module 8)

### Recommended (แนะนำ)
- **Visual Studio Code** + Go extension
- **curl** - ทดสอบ APIs
- **Postman/Insomnia** - API testing

### Optional (เสริม)
- **OpenAI API Key** - สำหรับ Module 7 (มี mock version)
- **GoLand IDE** - Alternative IDE

---

## 📁 โครงสร้างโปรเจกต์

```
tx_go_training/
├── module1/           # Runtime & Memory
│   ├── memory_model.go
│   └── gc_tuning/
├── module2/           # Concurrency
│   ├── worker_pool/
│   └── pipeline/
├── module3/           # DDD
│   ├── domain/
│   ├── application/
│   └── infrastructure/
├── module4/           # Performance
│   ├── profiling/
│   ├── benchmarks/
│   └── optimization/
├── module5/           # gRPC
│   ├── proto/
│   ├── grpc_demo/
│   └── resilience/
├── module6/           # Testing
│   ├── unit/
│   ├── integration/
│   ├── fuzz/
│   └── race/
├── module7/           # AI Integration
│   ├── rag/
│   ├── llm_client/
│   └── document_processor/
├── module8/           # Observability
│   ├── docker-compose.yml
│   ├── prometheus/
│   ├── grafana/
│   └── complete_service/
├── capstone/          # Full Integration
│   ├── ai/
│   ├── api/
│   ├── domain/
│   ├── processing/
│   └── observability/
├── go.mod
├── README.md
├── QUICK_START.md
├── SETUP_GUIDE.md
└── check_setup.sh
```

---

## 🎓 แนะนำวิธีเรียน

### สำหรับผู้เริ่มต้น Go
```
Week 1: Module 1-2   → พื้นฐาน Go และ Concurrency
Week 2: Module 3-4   → Architecture และ Performance
Week 3: Module 5-6   → gRPC และ Testing
Week 4: Module 7     → AI Integration
Week 5: Module 8     → Observability
Week 6: Capstone     → Project สุดท้าย
```

### สำหรับผู้มีประสบการณ์ Go
```
Week 1: Module 4-5   → Performance & gRPC
Week 2: Module 7-8   → AI & Observability
Week 3: Capstone     → Full Integration
```

---

## 🧪 การรัน Tests

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run with coverage
go test -cover ./...

# Run specific module
cd module6/unit
go test -v ./...

# Run benchmarks
cd module4/benchmarks
go test -bench=. -benchmem
```

---

## 🚀 การรัน Capstone Project

```bash
cd capstone

# ใช้ Mock AI (ไม่ต้อง OpenAI key)
go run .

# หรือใช้ OpenAI จริง
OPENAI_API_KEY=sk-... go run .

# Build binary
go build -o bin/capstone .
./bin/capstone

# Test endpoints
curl http://localhost:8080/health
curl http://localhost:8080/metrics
```

---

## 📊 Observability Stack (Module 8)

```bash
cd module8

# Start Prometheus + Grafana + Jaeger
docker compose up -d

# Access dashboards
open http://localhost:3000   # Grafana (admin/admin)
open http://localhost:9090   # Prometheus
open http://localhost:16686  # Jaeger

# Stop stack
docker compose down
```

---

## 🔧 Troubleshooting

### Go command not found
```bash
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
```

### Protoc not found
```bash
# macOS
brew install protobuf

# Linux
sudo apt-get install protobuf-compiler
```

### Docker permission denied
```bash
sudo usermod -aG docker $USER
newgrp docker
```

ดู [SETUP_GUIDE.md](./SETUP_GUIDE.md) สำหรับรายละเอียดเพิ่มเติม

---

## 📚 แหล่งข้อมูลเพิ่มเติม

- [Go Official Docs](https://go.dev/doc/)
- [Effective Go](https://go.dev/doc/effective_go)
- [Go by Example](https://gobyexample.com/)
- [gRPC Go Tutorial](https://grpc.io/docs/languages/go/)
- [Prometheus Go Client](https://prometheus.io/docs/guides/go-application/)

---

## 📝 License

This project is for educational purposes.

---

## 🎉 Happy Learning!

เริ่มเรียนได้เลยที่ → **[QUICK_START.md](./QUICK_START.md)**
