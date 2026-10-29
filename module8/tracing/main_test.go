package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func useRecorder() *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	return recorder
}

// TestProcessOrdersInParallel_WaitsForAll ตรวจว่าฟังก์ชัน return หลังทุก order เสร็จจริง
// รันด้วย -race เพื่อจับ data race บน errs slice
func TestProcessOrdersInParallel_WaitsForAll(t *testing.T) {
	recorder := useRecorder()

	const n = 20
	orders := make([]Order, n)
	for i := range orders {
		orders[i] = Order{
			ID:        fmt.Sprintf("ORD-%d", i),
			Items:     []OrderItem{{ProductID: "P1", Price: 100, Quantity: 1}},
			TotalBaht: 100,
		}
	}

	errs := processOrdersInParallel(context.Background(), orders)
	if len(errs) != n {
		t.Fatalf("len(errs) = %d, want %d", len(errs), n)
	}

	done := 0
	for _, s := range recorder.Ended() {
		if s.Name() == "processOrder" {
			done++
		}
	}
	if done != n {
		t.Errorf("processOrder spans ended before return = %d, want %d", done, n)
	}
}

func TestCreateOrderHandler_RejectsNonPost(t *testing.T) {
	useRecorder()

	rec := httptest.NewRecorder()
	createOrderHandler(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /orders status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
