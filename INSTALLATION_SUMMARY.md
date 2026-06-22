# 📦 สรุปการติดตั้งและ Configure สำหรับ Advanced Enterprise AI with Go

## ✅ ไฟล์ที่สร้างแล้ว

```
tx_go_training/
├── README.md              # คู่มือหลักของโปรเจกต์
├── QUICK_START.md         # เริ่มต้นใช้งานใน
├── SETUP_GUIDE.md         # คู่มือการติดตั้งแบบละเอียด
├── check_setup.sh         # Script ตรวจสอบการติดตั้ง
├── Makefile               # คำสั่งสำหรับ automation
├── .vscode/
│   ├── settings.json      # VS Code settings
│   └── extensions.json    # แนะนำ extensions
└── [existing modules...]
```

---

## 🚀 วิธีใช้งาน (3 ขั้นตอน)

### ขั้นที่ 1: ตรวจสอบการติดตั้ง

```bash
# ให้ script รันได้
chmod +x check_setup.sh

# ตรวจสอบว่าติดตั้งเครื่องมือครบหรือยัง
./check_setup.sh
```

**ผลลัพธ์ที่ควรได้:**
- ✅ Go 1.25+ installed
- ✅ Git installed
- ✅ Protocol Buffers installed (สำหรับ Module 5)
- ✅ Docker installed (สำหรับ Module 8)

---

### ขั้นที่ 2: ติดตั้ง Tools และ Dependencies

```bash
# ใช้ Makefile (แนะนำ)
make setup

# หรือรันทีละคำสั่ง:
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install golang.org/x/tools/gopls@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install honnef.co/go/tools/cmd/staticcheck@latest

# Download dependencies
go mod download
go mod verify
```

---

### ขั้นที่ 3: ทดสอบว่าทำงานได้

```bash
# ทดสอบ Module 7: RAG (ไม่ต้อง API key)
make run-module7
# หรือ: cd module7/rag && go run main.go

# ทดสอบ Capstone
make run-capstone
# หรือ: cd capstone && go run .

# Run tests ทั้งหมด
make test

# ตรวจสอบอีกครั้ง
./check_setup.sh
```

---

## 📚 คู่มือแต่ละไฟล์

### 1. README.md
- ภาพรวมของโปรเจกต์
- โครงสร้างคอร์ส 8 Modules + Capstone
- Quick links ไปยัง documentation อื่นๆ

### 2. QUICK_START.md
- เริ่มต้นใช้งานใน
- คำสั่งย่อๆ สั้นๆ สำหรับแต่ละ module
- Troubleshooting ปัญหาทั่วไป

### 3. SETUP_GUIDE.md (ละเอียดสุด)
- วิธีติดตั้ง Go แต่ละ OS (macOS, Linux, Windows)
- วิธีติดตั้ง Protocol Buffers
- วิธีติดตั้ง Docker & Docker Compose
- Setup IDE (VS Code, GoLand)
- Configure environment variables
- แก้ปัญหาที่พบบ่อย
- Quick reference commands

### 4. check_setup.sh
- Script ตรวจสอบว่าติดตั้งเครื่องมือครบหรือยัง
- แสดงผล ✅ / ✗ / ⚠ สำหรับแต่ละเครื่องมือ
- บอก command ติดตั้งถ้าขาดหายไป

### 5. Makefile
- Automation สำหรับคำสั่งที่ใช้บ่อย
- `make help` - แสดงคำสั่งทั้งหมด
- `make setup` - ติดตั้ง tools และ dependencies
- `make test` - รัน tests
- `make proto` - Generate protobuf files
- `make docker-up` - Start observability stack
- `make run-capstone` - รัน capstone project

---

## 🎯 แนะนำลำดับการเรียน

### Week 1: พื้นฐาน
```bash
# Module 1: Runtime & Memory
cd module1
go run memory_model.go

# Module 2: Concurrency
cd ../module2/worker_pool
go run main.go
```

### Week 2: Architecture & Performance
```bash
# Module 3: DDD
cd ../module3
go test ./...

# Module 4: Performance
cd ../module4/profiling
go run main.go
go test -bench=. -benchmem
```

### Week 3: gRPC & Testing
```bash
# Module 5: gRPC
make proto
make run-grpc-server  # Terminal 1
make run-grpc-client  # Terminal 2

# Module 6: Testing
cd module6
go test -v ./...
go test -race ./...
```

### Week 4: AI Integration
```bash
# Module 7: AI & RAG
make run-module7
# หรือ
cd module7/rag
go run main.go
```

### Week 5: Observability
```bash
# Module 8: Observability Stack
make docker-up
# เปิด browser:
# - http://localhost:3000 (Grafana)
# - http://localhost:9090 (Prometheus)
# - http://localhost:16686 (Jaeger)

cd module8/complete_service
go run main.go
```

### Week 6: Full Integration
```bash
# Capstone Project
make run-capstone
# Test:
curl http://localhost:8080/health
```

---

## 🔧 Makefile Commands

```bash
# Setup & Check
make setup         # ติดตั้งทุกอย่าง
make check         # ตรวจสอบการติดตั้ง

# Testing
make test          # Run all tests
make test-race     # With race detector
make test-cover    # With coverage
make bench         # Run benchmarks

# Build & Run
make run-capstone  # Run capstone
make run-module1   # Run specific module
make proto         # Generate protobuf

# Docker (Module 8)
make docker-up     # Start stack
make docker-down   # Stop stack
make docker-logs   # View logs

# Code Quality
make fmt           # Format code
make lint          # Run linters
make clean         # Clean artifacts
```

---

## 🛠️ เครื่องมือที่จำเป็น

| Tool | Version | ใช้ใน | ติดตั้ง |
|------|---------|-------|---------|
| **Go** | 1.25+ | ทุก Module | [go.dev](https://go.dev/dl/) |
| **Git** | 2.x | Version control | [git-scm.com](https://git-scm.com/) |
| **protoc** | 3.x+ | Module 5 (gRPC) | `brew install protobuf` |
| **Docker** | 20.x+ | Module 8 (Observability) | [docker.com](https://www.docker.com/) |
| **VS Code** | Latest | IDE (recommended) | [code.visualstudio.com](https://code.visualstudio.com/) |

---

## 📦 Go Tools ที่จะติดตั้งโดย `make setup`

```bash
# Protocol Buffers
google.golang.org/protobuf/cmd/protoc-gen-go
google.golang.org/grpc/cmd/protoc-gen-go-grpc

# Development
golang.org/x/tools/gopls          # Language Server
github.com/go-delve/delve/cmd/dlv # Debugger
honnef.co/go/tools/cmd/staticcheck # Linter
```

---

## 🎓 VS Code Setup

หลังจากติดตั้ง VS Code:

1. เปิด VS Code ในโปรเจกต์
2. กด `Cmd+Shift+X` (macOS) หรือ `Ctrl+Shift+X` (Windows/Linux)
3. ติดตั้ง extensions ที่แนะนำ (จาก `.vscode/extensions.json`):
   - Go (golang.go)
   - Docker (ms-azuretools.vscode-docker)
   - vscode-proto3 (zxh404.vscode-proto3)
   - Error Lens (usernamehw.errorlens)

4. กด `Cmd+Shift+P` → `Go: Install/Update Tools` → เลือก All

---

## 🆘 แก้ปัญหาที่พบบ่อย

### 1. Go command not found
```bash
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
```

### 2. protoc-gen-go not found
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
export PATH=$PATH:$HOME/go/bin
```

### 3. Docker permission denied (Linux)
```bash
sudo usermod -aG docker $USER
newgrp docker
```

### 4. Port already in use
```bash
lsof -i :8080
kill -9 <PID>
```

---

## ✨ Next Steps

หลังจากติดตั้งครบแล้ว:

1. ✅ อ่าน [QUICK_START.md](./QUICK_START.md)
2. ✅ เริ่มจาก Module 1: `cd module1 && go run memory_model.go`
3. ✅ อ่าน teaching notes ในแต่ละ module
4. ✅ ทำ labs ตามลำดับ
5. ✅ Build Capstone project
