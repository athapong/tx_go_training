# Module 6: Testing Strategies for Enterprise Go

## Teaching Notes & Instructor Guide

---

## Overview

This module covers the full testing spectrum for production Go services. The goal is not merely "write more tests" but to understand **which test to write, when, and why** — and to recognize what each test type can and cannot verify.

Estimated delivery time: **3.5–4 hours** (see lab timing at the end).

---

## 1. The Testing Pyramid for Go Projects

```
           /\
          /  \
         / E2E\          ← Few, slow, high-confidence
        /──────\
       /  Integ  \       ← Some, medium speed, real I/O
      /────────────\
     /  Unit Tests  \    ← Many, fast, isolated behavior
    /────────────────\
   /  Static Analysis \  ← go vet, staticcheck, golangci-lint
  /────────────────────\
```

### The Pyramid in Numbers (Rough Guide)

| Layer            | Count     | Run time        | Blocks CI? |
|-----------------|-----------|-----------------|------------|
| Static analysis  | N/A       | < 30s           | Yes        |
| Unit tests       | Hundreds  | < 30s           | Yes        |
| Integration tests| Tens      | 1–5 min         | Yes (own job) |
| E2E tests        | Single digits | 5–30 min    | Staging only |

**Key insight**: The pyramid is about *cost per feedback*. Unit tests are cheap to run. E2E tests are expensive. Invert the pyramid and you pay for it in slow CI and flaky builds.

---

## 2. When to Write Each Type of Test

### Unit Tests (`module6/unit/`)

**Write unit tests when:**
- Testing business logic with multiple branches (order state machine, validation rules, calculation logic)
- Testing error handling paths — you need to inject specific errors reliably
- Testing boundary conditions — exact values at limits (max 20 items, max 100,000 baht)
- Testing sequence of calls — the spy pattern verifies *how* code interacts with dependencies

**Do NOT write unit tests for:**
- Simple getters/setters with no logic
- Code that is purely orchestrating other code (test the orchestration, not individual steps)
- Database queries — SQL correctness requires a real database

**Pattern used**: table-driven tests with `t.Run()` + `t.Parallel()`

```
go test -v -run TestOrderHandler ./module6/unit/
go test -v -run TestOrderHandler/valid_single_item_order ./module6/unit/
go test -race -count=5 ./module6/unit/
```

---

### Integration Tests (`module6/integration/`)

**Write integration tests when:**
- Testing SQL queries — does the SELECT actually return the right data?
- Testing database constraints — CHECK, UNIQUE, FOREIGN KEY
- Testing transaction behavior — does rollback actually rollback?
- Testing ORM/query builder behavior against real schema
- Testing migration scripts

**Why mocks fail for database tests:**
A mock that implements `OrderRepository` will happily accept `status = "INVALID_STATUS"` because the mock has no constraints. Only a real PostgreSQL will reject it with a `CHECK constraint violation`. This is precisely the class of bug that reaches production when teams mock their way through database tests.

**testcontainers-go pattern:**
1. `TestMain` starts container once per package
2. Each test gets a clean slice of data (via `t.Cleanup` deleting specific IDs, not truncating the whole table)
3. Tests can run in parallel safely if they use unique IDs (timestamp-based IDs work well)

**Running integration tests (requires Docker):**
```
go test -tags=integration -v ./module6/integration/
go test -tags=integration -run TestOrderRepository_Save_FindByID ./module6/integration/
```

---

### Fuzz Tests (`module6/fuzz/`)

**Write fuzz tests when:**
- Writing any parser (string → structured type)
- Handling untrusted external input
- Implementing serialization/deserialization
- Writing cryptographic or encoding functions

**What fuzzing finds that manual tests miss:**
- Panic on empty string (parser indexing `s[0]` without length check)
- Panic on short input (`s[:4]` when `len(s) < 4`)
- Integer overflow with large numbers
- Unexpected behavior with Unicode, control characters, null bytes
- Combinations of valid-looking inputs that produce wrong results

**Workflow:**
1. Write the parser
2. Write `FuzzMyParser` with a seed corpus of typical + boundary inputs
3. Run: `go test -fuzz=FuzzMyParser -fuzztime=60s ./module6/fuzz/`
4. Fuzzer finds a failing input → saved to `testdata/fuzz/FuzzMyParser/`
5. Fix the bug
6. Commit the `testdata/` corpus — it now runs as a regression test in CI

**In CI (no Docker needed, runs fast):**
```
go test -run=Fuzz ./module6/fuzz/    # runs corpus only, no mutation
```

---

### Race Condition Tests (`module6/race/`)

**Always use `-race` for:**
- Any code using goroutines
- Shared data structures (caches, pools, registries)
- HTTP handlers (each request is a goroutine)
- Background workers

**The Go race detector:**
- Instruments binary at compile time
- Tracks every memory read/write across goroutines
- Reports the exact goroutines and lines that cause the race
- Zero false positives — every reported race is real
- Some false negatives — a race not triggered during the test run is not detected

**Running with race detector:**
```
go test -race ./module6/race/                  # all tests
go test -race -count=10 ./module6/race/        # repeat 10x for flaky races
go test -race -run TestBuggyCache ./module6/race/   # specific test
```

**Make races reliably triggerable** by using many goroutines and tight loops — see `TestBuggyCache_RaceCondition`. A test with 2 goroutines may not trigger the race. A test with 20+ goroutines almost always will.

---

## 3. Mock vs Fake vs Stub — When to Use Each

These terms are often used interchangeably but they have distinct meanings:

### Stub
Returns hardcoded data. No verification of how it was called.

```go
// Stub: always returns the same pre-programmed response
type stubRepo struct{}
func (s *stubRepo) FindByID(_ context.Context, _ OrderID) (*Order, error) {
    return &Order{ID: "fixed-id", Status: OrderStatusPending}, nil
}
```

**Use when:** You need a dependency to exist but its behavior in this test is irrelevant (e.g., you're testing error handling from a *different* dependency).

---

### Spy (hand-written mock)
Records calls so you can assert on them. Used in `module6/unit/`.

```go
// Spy: records what was called and with what arguments
type spyRepo struct {
    SaveCalls []*Order  // recorded calls
    SaveErr   error     // injectable error
}
func (s *spyRepo) Save(_ context.Context, order *Order) error {
    s.SaveCalls = append(s.SaveCalls, order)
    return s.SaveErr
}
```

**Use when:** You need to verify that code calls its dependencies correctly — correct arguments, correct number of times, in the correct order.

---

### Generated Mock (mockery / testify/mock)
Programmatically set expectations before the test runs. Used in `module6/mocks/`.

```go
mockRepo := NewMockOrderRepository(t)
mockRepo.EXPECT().
    Save(mock.Anything, mock.MatchedBy(func(o *Order) bool {
        return o.CustomerID == "cust-001"
    })).
    Return(nil).
    Once()
```

**Use when:**
- Interface has many methods (5+)
- You need flexible argument matching (not just exact values)
- You want automatic "all expectations were met" assertion via `t.Cleanup`

**Avoid when:**
- Interface is small and simple
- The generated code obscures what the test is actually checking
- The team is not familiar with the mock library's DSL

---

### Fake
A real working implementation, simpler than production. Example: an in-memory repository instead of PostgreSQL.

```go
// Fake: real working implementation, simplified
type inMemoryRepo struct {
    mu    sync.RWMutex
    store map[OrderID]*Order
}
func (r *inMemoryRepo) Save(_ context.Context, order *Order) error {
    r.mu.Lock(); defer r.mu.Unlock()
    clone := *order
    r.store[order.ID] = &clone
    return nil
}
```

**Use when:**
- Multiple tests need the same repository behavior, not just presence
- You want to test that the *application* code works correctly without focusing on specific interactions
- Running integration-style tests without Docker (e.g., in constrained CI)

**Fakes are more expensive to write** but more robust to interface changes than mocks.

---

## 4. The Coverage Trap: 100% Line Coverage ≠ Good Tests

Consider this function:

```go
func Divide(a, b int) int {
    if b == 0 {
        return 0
    }
    return a / b
}
```

This test achieves 100% line coverage:

```go
func TestDivide(t *testing.T) {
    Divide(10, 0)  // covers the if branch
    Divide(10, 2)  // covers the return branch
}
```

But it tests nothing. No assertions. It would pass even if `Divide(10, 2)` returned `999`.

**What 100% coverage actually means:**
- Every line was executed at least once
- It says nothing about whether the output was verified
- It says nothing about edge cases being tested
- It says nothing about the test being meaningful

**Better metrics to pursue:**
- **Mutation testing score** — do tests detect artificial bugs? (See section 6)
- **Branch coverage** — are all `if/else` paths tested? (`go test -covermode=atomic -coverprofile=cov.out`)
- **Behavior coverage** — are all documented business rules tested?

**Practical rule:** Aim for 80% line coverage as a minimum bar to ensure untested code is a deliberate decision, not an oversight. Do not mandate 100%.

---

## 5. Common Mistake: Testing Implementation Details, Not Behavior

### The wrong approach (testing implementation)

```go
// ❌ This test breaks every time you refactor the cache internals
func TestCache_Internal(t *testing.T) {
    c := &ConcurrentCache{}
    c.items["key"] = CacheItem{Value: "v"}  // accessing internal field
    if len(c.items) != 1 {
        t.Error("expected 1 item in internal map")
    }
}
```

Problems:
- The test accesses unexported fields, coupling the test to implementation
- If you change `items` from `map` to a sorted slice for performance, the test breaks even though behavior is unchanged
- You can't run this from a `_test` package (black-box testing)

### The right approach (testing behavior)

```go
// ✓ This test survives any internal refactor
func TestCache_SetAndGet(t *testing.T) {
    c := NewConcurrentCache()
    c.Set("key", "value", time.Minute)

    got, ok := c.Get("key")
    if !ok {
        t.Fatal("expected cache hit")
    }
    if got != "value" {
        t.Errorf("got %v, want value", got)
    }
}
```

**Rule of thumb:** If your test would break after a pure refactor (same external behavior, different internal implementation), the test is testing implementation, not behavior.

---

## 6. Mutation Testing Introduction

Mutation testing answers the question: **"If I introduce a bug, will my tests catch it?"**

A mutation testing tool modifies your code in small ways ("mutations") and checks if tests fail:

| Mutation | Example | Good tests should... |
|----------|---------|---------------------|
| Change operator | `>` → `>=` | Fail — boundary test exists |
| Negate condition | `if err != nil` → `if err == nil` | Fail — error path tested |
| Remove return | Remove `return nil` | Fail — compiler catches it usually |
| Change constant | `maxItems = 20` → `maxItems = 21` | Fail — boundary test exists |

**Go mutation testing tool:**
```
go install github.com/zimmski/go-mutesting@latest
go-mutesting ./module6/unit/...
```

**Interpreting results:**
- **Killed mutant**: test suite detected the mutation (good)
- **Survived mutant**: mutation was not caught by any test (gap in tests)
- **Mutation score**: killed / total. Aim for 70%+

**Practical use:** Run mutation testing on critical business logic (order calculations, permission checks, pricing rules). Not necessary for every package.

---

## 7. Parallel Test Guidelines

```go
func TestSomething(t *testing.T) {
    tests := []struct{ name string }{...}

    for _, tc := range tests {
        tc := tc          // ← Required in Go < 1.22 to capture loop variable
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()  // ← Tell Go this subtest can run concurrently

            // Your test code here
        })
    }
}
```

**When to use `t.Parallel()`:**
- Unit tests with no shared state — always
- Tests using `time.Sleep` — definitely (parallelism cuts total test time)
- Integration tests — yes, but use unique IDs per test to avoid data conflicts

**When NOT to use `t.Parallel()`:**
- Tests that modify package-level variables
- Tests using `os.Setenv` (global state)
- Tests relying on a specific database state (use `t.Cleanup` isolation instead)

---

## 8. Build Tags Strategy for Test Organization

```go
//go:build integration

package integration
```

```
# Run unit tests only (fast, no Docker)
go test ./...

# Run unit + integration tests
go test -tags=integration ./...

# Run just integration tests
go test -tags=integration -run TestOrderRepository ./module6/integration/
```

**Recommended structure for CI pipelines:**

```yaml
# .github/workflows/ci.yml
jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - run: go test -race ./...    # fast, seconds

  integration-tests:
    runs-on: ubuntu-latest
    services:
      docker: {}
    steps:
      - run: go test -tags=integration -race ./...   # slower, requires Docker
```

---

## 9. Lab Guide

| Activity | Time | Notes |
|----------|------|-------|
| **Introduction & pyramid overview** | Show where each file lives |
| **Lab 1: Unit tests** | Students extend `order_handler_test.go` with 3 new cases |
| **Lab 2: Hand-written mock vs generated mock** | Compare `unit/` mock with `mocks/` pattern |
| **Break** | | |
| **Lab 3: Integration test** | Spin up container, write 1 new repository test |
| **Lab 4: Fuzz testing** | Run fuzzer, observe failing input, fix parser |
| **Lab 5: Race detector** | Run `-race` on buggy cache, observe report, see fix |
| **Coverage & mutation discussion** | Demo `go test -cover`, discuss mutation concept |
| **Q&A / wrap-up** | | |

**Total: ~0.5 hours** (adjust based on student experience level)

---

## 10. Key Commands Reference

```bash
# Unit tests
go test -v ./module6/unit/
go test -v -run TestOrderHandler/cancel_pending_order ./module6/unit/
go test -race -count=5 ./module6/unit/

# Coverage
go test -cover ./module6/unit/
go test -coverprofile=cov.out ./module6/unit/ && go tool cover -html=cov.out

# Fuzz tests (corpus only — fast, for CI)
go test -run=Fuzz ./module6/fuzz/

# Fuzz tests (mutation mode — slow, for discovery)
go test -fuzz=FuzzParseOrderID -fuzztime=30s ./module6/fuzz/
go test -fuzz=FuzzParseOrderID_Buggy -fuzztime=5s ./module6/fuzz/

# Race detector
go test -race ./module6/race/
go test -race -count=10 -run TestConcurrentCache_NoRace ./module6/race/

# Integration tests (requires Docker)
go test -tags=integration -v ./module6/integration/

# Run everything
go test -race -tags=integration ./module6/...

# Benchmarks
go test -bench=. -benchmem ./module6/race/
```

---

## 11. Instructor Notes — Common Student Questions

**Q: Why not just use mocks everywhere and skip integration tests?**

A: Mocks verify that your code *calls* the database correctly. They cannot verify that the SQL is correct, that constraints are enforced, that indexes are used, or that transaction semantics work. The "testing trophy" school (Kent C. Dodds) argues for more integration tests because they give higher confidence per test written. For Go enterprise systems with PostgreSQL, integration tests for the data layer are non-negotiable.

**Q: When should I use testify vs standard library?**

A: For unit tests: the standard library (`testing` package) is sufficient and preferred for simplicity. For integration tests and complex assertion scenarios: `testify/assert` and `testify/require` reduce boilerplate significantly. For mocks: `testify/mock` is the de facto standard when using generated mocks.

**Q: How do I test code that calls `time.Now()`?**

A: Inject the clock as a function parameter or interface. See `NewOrderHandler(..., clock func() time.Time)` in `module6/unit/order_handler.go`. This pattern is idiomatic Go and avoids global state or monkey-patching.

**Q: Is fuzzing only for security-critical code?**

A: No. Any code that accepts string or byte input benefits from fuzzing. A JSON parser, an order ID parser, an address parser — all are candidates. The fuzzer is especially good at finding panics in parsers that would cause service outages in production even without malicious intent.

**Q: My race test passes without `-race` but fails with it. Which is correct?**

A: The `-race` result is correct. Without `-race`, race conditions are non-deterministic — the test might pass a thousand times and then fail on the 1001st. The race detector makes the detection deterministic by instrumenting every memory access.
