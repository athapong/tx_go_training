# ============================================================
# Makefile for Advanced Enterprise AI with Go
# ============================================================

.PHONY: help setup check test bench clean proto docker-up docker-down run-capstone

# Default target
help:
	@echo "Available targets:"
	@echo "  make setup         - Install Go tools and dependencies"
	@echo "  make check         - Check if all tools are installed"
	@echo "  make test          - Run all tests"
	@echo "  make test-race     - Run tests with race detector"
	@echo "  make test-cover    - Run tests with coverage"
	@echo "  make bench         - Run benchmarks"
	@echo "  make proto         - Generate protobuf files"
	@echo "  make docker-up     - Start observability stack (Module 8)"
	@echo "  make docker-down   - Stop observability stack"
	@echo "  make run-capstone  - Run capstone project"
	@echo "  make fmt           - Format all Go files"
	@echo "  make lint          - Run linters"
	@echo "  make clean         - Clean build artifacts"

# ============================================================
# Setup & Verification
# ============================================================

setup:
	@echo "=== Installing Go tools ==="
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install golang.org/x/tools/gopls@latest
	go install github.com/go-delve/delve/cmd/dlv@latest
	go install honnef.co/go/tools/cmd/staticcheck@latest
	@echo ""
	@echo "=== Downloading dependencies ==="
	go mod download
	go mod verify
	@echo ""
	@echo "✓ Setup complete!"

check:
	@./check_setup.sh

# ============================================================
# Testing
# ============================================================

test:
	@echo "=== Running tests ==="
	go test ./...

test-race:
	@echo "=== Running tests with race detector ==="
	go test -race ./...

test-cover:
	@echo "=== Running tests with coverage ==="
	go test -cover ./...
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-verbose:
	@echo "=== Running tests (verbose) ==="
	go test -v ./...

# ============================================================
# Benchmarks
# ============================================================

bench:
	@echo "=== Running benchmarks ==="
	cd module4/benchmarks && go test -bench=. -benchmem

# ============================================================
# Protocol Buffers (Module 5)
# ============================================================

proto:
	@echo "=== Generating protobuf files ==="
	cd module5/proto && \
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       order.proto
	@echo "✓ Protobuf files generated"

# ============================================================
# Docker (Module 8)
# ============================================================

docker-up:
	@echo "=== Starting observability stack ==="
	cd module8 && docker compose up -d
	@echo ""
	@echo "Services started:"
	@echo "  Grafana:    http://localhost:3000 (admin/admin)"
	@echo "  Prometheus: http://localhost:9090"
	@echo "  Jaeger:     http://localhost:16686"
	@echo ""
	@echo "Run 'make docker-down' to stop"

docker-down:
	@echo "=== Stopping observability stack ==="
	cd module8 && docker compose down

docker-logs:
	cd module8 && docker compose logs -f

# ============================================================
# Run Projects
# ============================================================

run-capstone:
	@echo "=== Running Capstone project ==="
	cd capstone && go run .

run-rag:
	@echo "=== Running RAG demo (Module 7) ==="
	cd module7/rag && go run main.go

run-grpc-server:
	@echo "=== Running gRPC server (Module 5) ==="
	cd module5/grpc_demo && go run server/main.go

run-grpc-client:
	@echo "=== Running gRPC client (Module 5) ==="
	cd module5/grpc_demo && go run client/main.go

# ============================================================
# Code Quality
# ============================================================

fmt:
	@echo "=== Formatting Go files ==="
	go fmt ./...
	gofmt -s -w .

lint:
	@echo "=== Running linters ==="
	go vet ./...
	staticcheck ./...

# ============================================================
# Build
# ============================================================

build-capstone:
	@echo "=== Building capstone binary ==="
	cd capstone && go build -o ../bin/capstone .
	@echo "✓ Binary created: bin/capstone"

build-all:
	@echo "=== Building all modules ==="
	mkdir -p bin
	@echo "Building capstone..."
	cd capstone && go build -o ../bin/capstone .
	@echo "✓ All binaries created in bin/"

# ============================================================
# Clean
# ============================================================

clean:
	@echo "=== Cleaning build artifacts ==="
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean -cache -testcache
	@echo "✓ Clean complete"

# ============================================================
# Run Labs by Module
# ============================================================

run-module1:
	@echo "=== Module 1: Runtime & Memory ==="
	cd module1 && go run memory_model.go

run-module2:
	@echo "=== Module 2: Concurrency ==="
	cd module2/worker_pool && go run main.go

run-module3:
	@echo "=== Module 3: DDD ==="
	cd module3 && go test ./...

run-module4:
	@echo "=== Module 4: Performance ==="
	cd module4/profiling && go run main.go

run-module7:
	@echo "=== Module 7: AI Integration ==="
	cd module7/rag && go run main.go

# ============================================================
# Development
# ============================================================

dev-watch:
	@echo "=== Running capstone with auto-reload ==="
	@echo "Install air: go install github.com/cosmtrek/air@latest"
	cd capstone && air

tidy:
	@echo "=== Tidying dependencies ==="
	go mod tidy

update:
	@echo "=== Updating dependencies ==="
	go get -u ./...
	go mod tidy
