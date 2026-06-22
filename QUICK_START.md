# 🚀 Quick Start Guide - Advanced Enterprise AI with Go

เริ่มต้นใช้งานใน!

---

## ✅ Checklist การติดตั้ง

```bash
# 1. ติดตั้ง Go 1.25+
go version

# 2. ติดตั้ง Protoc (สำหรับ gRPC)
protoc --version

# 3. ติดตั้ง Docker
docker --version

# 4. ติดตั้ง Go tools
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

---

## 🏃 เริ่มต้นใช้งาน

### 1. Clone และ Setup

```bash
# Clone repository
cd tx_go_training

# Download dependencies
go mod download

# Verify
go mod verify
```

### 2. ทดสอบแต่ละ Module

```bash
# Module 1: Memory & Runtime
cd module1
go run memory_model.go
cd ..

# Module 2: Concurrency
cd module2/worker_pool
go run main.go
cd ../..

# Module 7: RAG System (ไม่ต้อง OpenAI key)
cd module7/rag
go run main.go
cd ../..

# Capstone: Full App
cd capstone
go run .
# เปิดอีก terminal: curl http://localhost:8080/health
```

### 3. รัน Observability Stack (Module 8)

```bash
cd module8

# Start Prometheus + Grafana + Jaeger
docker compose up -d

# รอ 10 วินาที
sleep 10

# เปิด browser
open http://localhost:3000   # Grafana (admin/admin)
open http://localhost:9090   # Prometheus
open http://localhost:16686  # Jaeger

# Run demo service
cd complete_service
go run main.go

# Test
curl http://localhost:8080/orders

# Cleanup
cd ..
docker compose down
```

---

## 📝 รัน Labs แต่ละ Module

### Module 1: Runtime Internals
```bash
cd module1
go test -v
go run memory_model.go
```

### Module 2: Concurrency
```bash
cd module2/worker_pool
go run main.go
```

### Module 3: Domain-Driven Design
```bash
cd module3
go test ./...
```

### Module 4: Performance & Profiling
```bash
cd module4/profiling
go run main.go

# Benchmarks
cd ../benchmarks
go test -bench=. -benchmem
```

### Module 5: gRPC
```bash
# Generate proto (ครั้งเดียว)
cd module5/proto
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       order.proto

# Run server (terminal 1)
cd ../grpc_demo
go run server/main.go

# Run client (terminal 2)
go run client/main.go
```

### Module 6: Testing
```bash
cd module6

# Unit tests
cd unit
go test -v ./...

# Race detector
go test -race ./...

# Fuzzing
cd ../fuzz
go test -fuzz=FuzzParseAmount -fuzztime=10s

# Integration tests
cd ../integration
go test -v
```

### Module 7: AI Integration
```bash
cd module7

# RAG System (ไม่ต้อง API key)
cd rag
go run main.go

# LLM Client (ต้อง OpenAI key)
cd ../llm_client
OPENAI_API_KEY=sk-... go run main.go

# Document Processor
cd ../document_processor
go run main.go
```

### Module 8: Observability
```bash
cd module8

# Start stack
docker compose up -d

# Access dashboards
open http://localhost:3000   # Grafana
open http://localhost:9090   # Prometheus  
open http://localhost:16686  # Jaeger

# Run demo
cd complete_service
go run main.go

# Generate traffic
for i in {1..10}; do curl http://localhost:8080/orders; done

# View metrics
open http://localhost:8080/metrics

# Cleanup
cd ..
docker compose down
```

### Capstone: Smart Invoice Processing
```bash
cd capstone

# Run service (mock AI - ไม่ต้อง API key)
go run .

# Or with OpenAI
OPENAI_API_KEY=sk-... go run .

# Test endpoints
curl http://localhost:8080/health
curl -X POST http://localhost:8080/invoices \
  -H "Content-Type: application/json" \
  -d '{"filename":"test.pdf","data":"base64..."}'

# Build binary
go build -o bin/capstone .
./bin/capstone
```

---

## 🐛 Troubleshooting

### Go ไม่เจอ
```bash
# macOS
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.zshrc
source ~/.zshrc

# Linux
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
```

### Protoc ไม่เจอ
```bash
# macOS
brew install protobuf

# Linux
sudo apt-get install protobuf-compiler
```

### Docker permission denied
```bash
# Linux only
sudo usermod -aG docker $USER
newgrp docker
```

### Port already in use
```bash
# หา process
lsof -i :8080

# Kill
kill -9 <PID>
```

---

## 📚 Next Steps

1. ✅ อ่าน `SETUP_GUIDE.md` สำหรับ detailed instructions
2. ✅ ดู teaching notes ในแต่ละ module
3. ✅ ทำ exercises ตามลำดับ
4. ✅ Build Capstone project

---

## 🎯 Learning Path

```
Week 1: Module 1-2  → Go Runtime & Concurrency
Week 2: Module 3-4  → DDD & Performance  
Week 3: Module 5-6  → gRPC & Testing
Week 4: Module 7-8  → AI & Observability
Week 5: Capstone    → Full Integration
```

---

## 💡 Tips

- ใช้ VS Code + Go extension
- Enable `gopls` language server
- Run `go test ./...` บ่อยๆ
- ใช้ `go fmt` ก่อน commit
- อ่าน error messages อย่างละเอียด
- ลองแก้ code และ experiment!

---

🎉 **พร้อมแล้ว! เริ่มเรียน Go ได้เลย**

ดู full documentation: [SETUP_GUIDE.md](./SETUP_GUIDE.md)
