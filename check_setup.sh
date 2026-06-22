#!/bin/bash

# ============================================================
# Setup Checker for Advanced Enterprise AI with Go
# Usage: ./check_setup.sh
# ============================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Checking Setup for Advanced Enterprise AI with Go ===${NC}"
echo ""

# Counters
PASSED=0
FAILED=0
WARNINGS=0

# Helper functions
check_command() {
    local cmd=$1
    local name=$2
    local required_version=$3
    
    if command -v "$cmd" &> /dev/null; then
        local version=$($cmd --version 2>&1 | head -n 1)
        echo -e "${GREEN}✓${NC} $name: ${GREEN}installed${NC}"
        echo "  Version: $version"
        PASSED=$((PASSED + 1))
        return 0
    else
        echo -e "${RED}✗${NC} $name: ${RED}NOT FOUND${NC}"
        if [ -n "$required_version" ]; then
            echo "  Required: $required_version"
        fi
        FAILED=$((FAILED + 1))
        return 1
    fi
}

check_go_version() {
    if command -v go &> /dev/null; then
        local version=$(go version | awk '{print $3}' | sed 's/go//')
        local major=$(echo $version | cut -d. -f1)
        local minor=$(echo $version | cut -d. -f2)
        
        echo -e "${GREEN}✓${NC} Go: ${GREEN}installed${NC}"
        echo "  Version: go$version"
        
        if [ "$major" -ge 1 ] && [ "$minor" -ge 22 ]; then
            echo -e "  ${GREEN}Version OK (>= 1.22)${NC}"
            PASSED=$((PASSED + 1))
        else
            echo -e "  ${YELLOW}Warning: Version 1.22+ recommended${NC}"
            WARNINGS=$((WARNINGS + 1))
        fi
    else
        echo -e "${RED}✗${NC} Go: ${RED}NOT FOUND${NC}"
        echo "  Install from: https://go.dev/dl/"
        FAILED=$((FAILED + 1))
    fi
}

check_go_tools() {
    local tool=$1
    local name=$2
    
    if command -v "$tool" &> /dev/null; then
        echo -e "${GREEN}✓${NC} $name: ${GREEN}installed${NC}"
        PASSED=$((PASSED + 1))
        return 0
    else
        echo -e "${YELLOW}⚠${NC} $name: ${YELLOW}NOT FOUND${NC}"
        echo "  Install: go install $3"
        WARNINGS=$((WARNINGS + 1))
        return 1
    fi
}

# ============================================================
# Check Core Tools
# ============================================================

echo -e "${BLUE}=== Core Tools ===${NC}"
check_go_version
echo ""

check_command git "Git" "2.x"
echo ""

# ============================================================
# Check Go Tools
# ============================================================

echo -e "${BLUE}=== Go Tools ===${NC}"
check_go_tools "gopls" "Go Language Server" "golang.org/x/tools/gopls@latest"
echo ""

check_go_tools "dlv" "Delve Debugger" "github.com/go-delve/delve/cmd/dlv@latest"
echo ""

check_go_tools "staticcheck" "Staticcheck Linter" "honnef.co/go/tools/cmd/staticcheck@latest"
echo ""

# ============================================================
# Check Protocol Buffers (Module 5)
# ============================================================

echo -e "${BLUE}=== Protocol Buffers (Module 5) ===${NC}"
check_command "protoc" "Protocol Buffer Compiler" "3.x"
echo ""

check_go_tools "protoc-gen-go" "protoc-gen-go" "google.golang.org/protobuf/cmd/protoc-gen-go@latest"
echo ""

check_go_tools "protoc-gen-go-grpc" "protoc-gen-go-grpc" "google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
echo ""

# ============================================================
# Check Docker (Module 8)
# ============================================================

echo -e "${BLUE}=== Docker (Module 8) ===${NC}"
check_command "docker" "Docker" "20.x"
echo ""

if command -v docker &> /dev/null; then
    if docker compose version &> /dev/null; then
        echo -e "${GREEN}✓${NC} Docker Compose: ${GREEN}installed${NC}"
        local compose_version=$(docker compose version | head -n 1)
        echo "  Version: $compose_version"
        PASSED=$((PASSED + 1))
    else
        echo -e "${YELLOW}⚠${NC} Docker Compose: ${YELLOW}NOT FOUND${NC}"
        echo "  Install Docker Compose plugin"
        WARNINGS=$((WARNINGS + 1))
    fi
else
    echo -e "${YELLOW}⚠${NC} Docker Compose: ${YELLOW}SKIPPED (Docker not found)${NC}"
fi
echo ""

# ============================================================
# Check Project Dependencies
# ============================================================

echo -e "${BLUE}=== Project Dependencies ===${NC}"
if [ -f "go.mod" ]; then
    echo -e "${GREEN}✓${NC} go.mod: ${GREEN}found${NC}"
    PASSED=$((PASSED + 1))
    
    echo "  Verifying dependencies..."
    if go mod verify &> /dev/null; then
        echo -e "${GREEN}✓${NC} Dependencies: ${GREEN}verified${NC}"
        PASSED=$((PASSED + 1))
    else
        echo -e "${YELLOW}⚠${NC} Dependencies: ${YELLOW}need to run 'go mod download'${NC}"
        WARNINGS=$((WARNINGS + 1))
    fi
else
    echo -e "${RED}✗${NC} go.mod: ${RED}NOT FOUND${NC}"
    echo "  Are you in the project root directory?"
    FAILED=$((FAILED + 1))
fi
echo ""

# ============================================================
# Check Optional Tools
# ============================================================

echo -e "${BLUE}=== Optional Tools ===${NC}"

if command -v curl &> /dev/null; then
    echo -e "${GREEN}✓${NC} curl: ${GREEN}installed${NC} (for API testing)"
else
    echo -e "${YELLOW}⚠${NC} curl: ${YELLOW}NOT FOUND${NC} (recommended for API testing)"
fi

if command -v code &> /dev/null; then
    echo -e "${GREEN}✓${NC} VS Code: ${GREEN}installed${NC}"
else
    echo -e "${YELLOW}⚠${NC} VS Code: ${YELLOW}NOT FOUND${NC} (recommended IDE)"
fi

echo ""

# ============================================================
# Summary
# ============================================================

echo -e "${BLUE}=== Summary ===${NC}"
echo -e "Passed:   ${GREEN}${PASSED}${NC}"
echo -e "Failed:   ${RED}${FAILED}${NC}"
echo -e "Warnings: ${YELLOW}${WARNINGS}${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ All required tools are installed!${NC}"
    echo ""
    echo -e "Next steps:"
    echo "  1. Run 'go mod download' to download dependencies"
    echo "  2. Run 'go test ./...' to verify everything works"
    echo "  3. Start with Module 1: cd module1 && go run memory_model.go"
    echo ""
    echo -e "Quick start: ${BLUE}./QUICK_START.md${NC}"
    exit 0
else
    echo -e "${RED}✗ Some required tools are missing.${NC}"
    echo ""
    echo "Please install missing tools and run this script again."
    echo "See SETUP_GUIDE.md for detailed installation instructions."
    exit 1
fi
