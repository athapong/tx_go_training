# คู่มือการติดตั้งและ Configure สำหรับ Advanced Enterprise AI with Go

## 📋 สารบัญ
1. [ติดตั้ง Go](#1-ติดตั้ง-go)
2. [ติดตั้ง Tools พื้นฐาน](#2-ติดตั้ง-tools-พื้นฐาน)
3. [ติดตั้ง Protocol Buffers](#3-ติดตั้ง-protocol-buffers-สำหรับ-module-5)
4. [ติดตั้ง Docker](#4-ติดตั้ง-docker--docker-compose-สำหรับ-module-8)
5. [Setup IDE](#5-setup-ide)
6. [ติดตั้ง Go Dependencies](#6-ติดตั้ง-go-dependencies)
7. [ทดสอบการติดตั้ง](#7-ทดสอบการติดตั้ง)
8. [Environment Variables](#8-environment-variables)

---

## 1. ติดตั้ง Go

### macOS
```bash
# ใช้ Homebrew (แนะนำ)
brew install go

# หรือดาวน์โหลดจาก https://go.dev/dl/
```

### ตรวจสอบการติดตั้ง
```bash
go version
# ควรแสดง: go version go1.25.0 หรือสูงกว่า

go env
# ตรวจสอบ GOPATH และ GOROOT
```

---

## 2. ติดตั้ง Protocol Buffers (สำหรับ Module 5)

### ติดตั้ง protoc compiler

#### macOS
```bash
brew install protobuf
protoc --version  # ควรเป็น libprotoc 3.x หรือสูงกว่า
```

### ติดตั้ง Go plugins สำหรับ protoc
```bash
# Plugin สำหรับ generate Go code จาก .proto files
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# ตรวจสอบว่าติดตั้งสำเร็จ
which protoc-gen-go
which protoc-gen-go-grpc
```

### Generate Go code จาก proto files
```bash
# รันจาก root directory ของโปรเจกต์
cd module5/proto
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       order.proto

# ควรสร้างไฟล์:
# - orderpb/order.pb.go
# - orderpb/order_grpc.pb.go
```

---

## 3. ติดตั้ง Docker & Docker Compose (สำหรับ Module 8)

### macOS
```bash
# ติดตั้ง Docker Desktop
brew install --cask docker

# เปิด Docker Desktop แล้วรอจนรันเสร็จ
```
---

## 4. Setup IDE

### Visual Studio Code (แนะนำ)
```bash
# ติดตั้ง VS Code
# macOS
brew install --cask visual-studio-code

```

#### Extensions ที่จำเป็น
เปิด VS Code แล้วติดตั้ง Extensions เหล่านี้:
```bash
# ติดตั้งผ่าน command line
code --install-extension golang.go
code --install-extension ms-azuretools.vscode-docker
code --install-extension zxh404.vscode-proto3

# หรือติดตั้งผ่าน VS Code:
# Ctrl+Shift+X (Cmd+Shift+X บน macOS) → ค้นหาและติดตั้ง:
# 1. Go (by Go Team at Google)
# 2. Docker (by Microsoft)
# 3. vscode-proto3 (by zxh404)
# 4. Error Lens (by Alexander) - แนะนำ
```

#### Configure Go Extension
1. เปิด VS Code
2. กด `Cmd+Shift+P` (macOS) หรือ `Ctrl+Shift+P` (Windows/Linux)
3. พิมพ์ `Go: Install/Update Tools`
4. เลือก **All** → กด OK
5. รอจนติดตั้ง tools ทั้งหมดเสร็จ:
   - gopls (Language Server)
   - dlv (Debugger)
   - staticcheck (Linter)
   - goplay, impl, etc.

### GoLand (ทางเลือก - เสียเงิน)
```bash
# macOS
brew install --cask goland

# ดาวน์โหลดจาก https://www.jetbrains.com/go/
# มี Free trial 30 วัน หรือใช้ Education license
```

---

## 5. ติดตั้ง Go Dependencies

```bash
# 1. Clone โปรเจกต์ (ถ้ายังไม่ได้)
git clone <repository-url>
cd tx_go_training

# 2. ดาวน์โหลด dependencies ทั้งหมด
go mod download

# 3. ตรวจสอบว่า dependencies ครบ
go mod verify

# 4. Tidy up (ลบ dependencies ที่ไม่ได้ใช้)
go mod tidy
```

---

## 6. ทดสอบการติดตั้ง

### ทดสอบแต่ละ Module

#### Module 1: Runtime & Memory
```bash
cd module1
go test -v
go run memory_model.go
```

#### Module 2: Concurrency
```bash
cd module2/worker_pool
go run main.go
```

#### Module 3: DDD
```bash
cd module3
go test ./...
```

#### Module 4: Performance
```bash
cd module4/profiling
go run main.go
go test -bench=. -benchmem
```

#### Module 5: gRPC
```bash
cd module5/grpc_demo

# Generate proto files (ถ้ายังไม่ได้ generate)
cd ../proto
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       order.proto

# รัน server (terminal 1)
cd ../grpc_demo
go run server/main.go

# รัน client (terminal 2)
go run client/main.go
```

#### Module 6: Testing
```bash
cd module6/unit
go test -v ./...

# Test with race detector
go test -race ./...

# Fuzzing
cd ../fuzz
go test -fuzz=FuzzParseAmount -fuzztime=10s
```

#### Module 7: AI/RAG
```bash
cd module7/rag
go run main.go

# ถ้ามี OpenAI API key (optional)
OPENAI_API_KEY=sk-... go run main.go
```

#### Module 8: Observability
```bash
cd module8

# 1. Start observability stack
docker compose up -d

# 2. Wait for services to be ready
sleep 10

# 3. Check services
docker compose ps

# 4. Access UIs
open http://localhost:3000  # Grafana (admin/admin)
open http://localhost:9090  # Prometheus
open http://localhost:16686 # Jaeger

# 5. Run demo service
cd complete_service
go run main.go

# 6. Generate some load
curl http://localhost:8080/orders

# 7. Cleanup
cd ..
docker compose down
```

#### Capstone: Full Integration
```bash
cd capstone

# Run without OpenAI (uses mock)
go run .

# Run with OpenAI (optional)
OPENAI_API_KEY=sk-... go run .

# Test endpoints
curl http://localhost:8080/health

# สร้าง binary
go build -o bin/capstone .
./bin/capstone
```

---

## 7. Environment Variables

สร้างไฟล์ `.env` (optional) สำหรับ development:

```bash
# สร้างไฟล์ .env
cat > .env << 'EOF'
# Capstone Configuration
SERVER_ADDR=:8080
LOG_LEVEL=info
LOG_FORMAT=json
WORKER_COUNT=5
ENVIRONMENT=development

# AI Configuration (Optional)
OPENAI_API_KEY=

# Observability (Module 8)
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
OTEL_SERVICE_NAME=order-service

# Database (ถ้าใช้)
DATABASE_URL=postgres://user:pass@localhost:5432/dbname

# Redis (ถ้าใช้)
REDIS_ADDR=localhost:6379
EOF

# โหลด env variables
export $(cat .env | xargs)
```

---

## 8. Common Issues & Solutions

### Issue 1: `go: command not found`
```bash
# เพิ่ม Go binary ใน PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export PATH=$PATH:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
```

### Issue 2: `protoc: command not found`
```bash
# ติดตั้ง protobuf compiler ตาม Section 3
brew install protobuf  # macOS
```

### Issue 3: Docker permission denied
```bash
# Linux: เพิ่ม user เข้า docker group
sudo usermod -aG docker $USER
newgrp docker
```

### Issue 4: Port already in use
```bash
# หา process ที่ใช้ port
lsof -i :8080  # macOS/Linux
netstat -ano | findstr :8080  # Windows

# Kill process
kill -9 <PID>  # macOS/Linux
```

### Issue 5: Module dependencies error
```bash
# Clear module cache และ download ใหม่
go clean -modcache
go mod download
go mod tidy
```

---

## 9. Quick Reference Commands

```bash
# Build
go build -o bin/app .

# Run
go run main.go

# Test
go test ./...
go test -v -race ./...
go test -cover ./...

# Benchmark
go test -bench=. -benchmem

# Format code
go fmt ./...
gofmt -s -w .

# Lint
go vet ./...
staticcheck ./...

# Update dependencies
go get -u ./...
go mod tidy

# Profile
go test -cpuprofile=cpu.prof
go tool pprof cpu.prof

# Generate proto
protoc --go_out=. --go-grpc_out=. *.proto

# Docker
docker compose up -d
docker compose logs -f
docker compose down
docker compose ps
```

---

## 10. VS Code Workspace Settings

สร้างไฟล์ `.vscode/settings.json`:

```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "staticcheck",
  "go.lintOnSave": "workspace",
  "go.formatTool": "gofmt",
  "go.formatFlags": ["-s"],
  "go.testOnSave": false,
  "go.coverOnSave": false,
  "go.testFlags": ["-v", "-race"],
  "go.buildTags": "",
  "editor.formatOnSave": true,
  "editor.codeActionsOnSave": {
    "source.organizeImports": true
  },
  "[go]": {
    "editor.defaultFormatter": "golang.go",
    "editor.tabSize": 4,
    "editor.insertSpaces": false
  },
  "files.exclude": {
    "**/.git": true,
    "**/.DS_Store": true,
    "**/bin": true
  }
}
```

---

## 📚 Additional Resources

- [Go Official Documentation](https://go.dev/doc/)
- [Effective Go](https://go.dev/doc/effective_go)
- [Go by Example](https://gobyexample.com/)
- [gRPC Go Quick Start](https://grpc.io/docs/languages/go/quickstart/)
- [Prometheus Go Client](https://prometheus.io/docs/guides/go-application/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)
