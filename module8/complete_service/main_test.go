package main

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSimulateOrderProcessing_SpansAreSiblings ตรวจว่า validateOrder, db.checkInventory,
// payment.process เป็นลูกของ span เดียวกัน (createOrder) ไม่ซ้อนกันเป็นสาย
func TestSimulateOrderProcessing_SpansAreSiblings(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")

	ctx, parent := tracer.Start(context.Background(), "createOrder")
	_ = simulateOrderProcessing(ctx, &Order{ID: "ORD-test", TotalBaht: 100})
	parent.End()

	want := map[string]bool{"validateOrder": true, "db.checkInventory": true, "payment.process": true}
	for _, s := range recorder.Ended() {
		if !want[s.Name()] {
			continue
		}
		delete(want, s.Name())
		if got := s.Parent().SpanID(); got != parent.SpanContext().SpanID() {
			t.Errorf("span %q parent = %s, want createOrder (%s)", s.Name(), got, parent.SpanContext().SpanID())
		}
	}
	for name := range want {
		t.Errorf("span %q not recorded", name)
	}
}

func TestRouteLabel(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/orders", "/orders"},
		{"/orders/get", "/orders/get"},
		{"/health", "/health"},
		{"/metrics", "/metrics"},
		{"/random/abc123", "unmatched"},
		{"/wp-admin", "unmatched"},
	}
	for _, tt := range tests {
		if got := routeLabel(tt.path); got != tt.want {
			t.Errorf("routeLabel(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
