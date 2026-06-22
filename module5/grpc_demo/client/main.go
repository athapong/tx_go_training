// Module 5: gRPC Client Demo — Resilience Patterns
//
// สิ่งที่จะเรียนรู้ในไฟล์นี้:
//  1. Connection Setup ด้วย Retry Policy (exponential backoff)
//  2. Timeout / Deadline Propagation
//  3. Circuit Breaker ด้วย gobreaker
//  4. Demo scenarios: normal call, timeout, circuit breaker
//
// วิธีรัน (ต้องรัน server ก่อน):
//
//	go run module5/grpc_demo/server/main.go &
//	go run module5/grpc_demo/client/main.go
//
// NOTE เรื่อง gobreaker:
//
//	ก่อนรันต้องเพิ่ม dependency:
//	  go get github.com/sony/gobreaker@v0.5.0
//	หรือใน go.mod:
//	  require github.com/sony/gobreaker v0.5.0
//
// เนื่องจากไฟล์นี้ใช้ gobreaker จาก external package
// ไฟล์นี้จึงแสดง pattern ที่ครบถ้วน แต่ต้อง go get ก่อน
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/sony/gobreaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"tx_go_training/module5/proto/orderpb"
)

// ─────────────────────────────────────────────
// ResilientOrderClient — client wrapper ที่มี resilience patterns
//
// Pattern ที่ใช้:
//   - Circuit Breaker: ป้องกันการส่ง request ไปยัง service ที่ล้มเหลว
//   - Retry with backoff: ลองใหม่เมื่อ error ชั่วคราว
//   - Deadline/Timeout: จำกัดเวลาแต่ละ call
// ─────────────────────────────────────────────

// ResilientOrderClient ห่อ gRPC client ด้วย resilience patterns
type ResilientOrderClient struct {
	conn    *grpc.ClientConn
	client  orderpb.OrderServiceClient
	cb      *gobreaker.CircuitBreaker
	log     *slog.Logger
	timeout time.Duration
}

// newResilientOrderClient สร้าง client พร้อม circuit breaker
func newResilientOrderClient(addr string) (*ResilientOrderClient, error) {
	// ─── กำหนด Retry Policy ใน Service Config ───
	//
	// gRPC retry policy ถูกกำหนดเป็น JSON ใน service config
	// maxAttempts: 4 หมายถึง 1 initial + 3 retries
	// retryableStatusCodes: status codes ที่จะ retry (UNAVAILABLE = service down ชั่วคราว)
	//
	// หลักการ: retry เฉพาะ idempotent operation หรือ error ที่ไม่ใช่ client fault
	// ไม่ควร retry: INVALID_ARGUMENT, NOT_FOUND, PERMISSION_DENIED (client fault)
	// ควร retry: UNAVAILABLE, RESOURCE_EXHAUSTED (transient server fault)
	serviceConfig := `{
		"methodConfig": [{
			"name": [{"service": "order.OrderService"}],
			"retryPolicy": {
				"maxAttempts": 4,
				"initialBackoff": "0.1s",
				"maxBackoff": "2s",
				"backoffMultiplier": 2,
				"retryableStatusCodes": ["UNAVAILABLE"]
			},
			"timeout": "5s"
		}]
	}`

	// ─── สร้าง Connection ───
	//
	// insecure.NewCredentials(): ไม่ใช้ TLS (สำหรับ dev เท่านั้น)
	// Production: ใช้ credentials.NewClientTLSFromFile() หรือ mTLS
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// ─── กำหนด Circuit Breaker Settings ───
	//
	// Circuit Breaker มี 3 สถานะ:
	//   CLOSED   → ส่ง request ปกติ (สถานะเริ่มต้น)
	//   OPEN     → บล็อก request ทันที (เกิดเมื่อ failure เกิน threshold)
	//   HALF-OPEN → ทดสอบส่ง request จำนวนน้อยเพื่อเช็กว่า service ฟื้นหรือยัง
	cbSettings := gobreaker.Settings{
		Name: "OrderService",

		// จำนวน failure ที่ยอมรับได้ก่อน OPEN
		// ถ้า 3 request ใน 10 request ล้ม จะเปิด circuit
		MaxRequests: 3, // ใน HALF-OPEN: ส่งได้สูงสุด 3 request

		// ระยะเวลาที่ circuit จะ OPEN ก่อนลอง HALF-OPEN
		Timeout: 10 * time.Second,

		// ฟังก์ชันตัดสินใจว่าเมื่อไหร่จะ OPEN circuit
		// ReadyToTrip: เรียกเมื่อ request ล้มเหลว
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// OPEN เมื่อมี failure ติดต่อกัน >= 3 ครั้ง
			// หรือ failure ratio > 60%
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.ConsecutiveFailures >= 3 || (counts.Requests >= 5 && failureRatio >= 0.6)
		},

		// Callback เมื่อ state เปลี่ยน (ใช้สำหรับ alerting)
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			slog.Warn("Circuit breaker state changed",
				"name", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	}

	cb := gobreaker.NewCircuitBreaker(cbSettings)

	return &ResilientOrderClient{
		conn:    conn,
		client:  newOrderServiceClient(conn),
		cb:      cb,
		log:     slog.Default(),
		timeout: 5 * time.Second,
	}, nil
}

// newOrderServiceClient สร้าง gRPC client stub
// ในโปรดักชัน (protoc generated): orderpb.NewOrderServiceClient(conn)
// ที่นี่เราสร้าง minimal client wrapper เพราะไม่ได้ใช้ protoc
func newOrderServiceClient(conn *grpc.ClientConn) orderpb.OrderServiceClient {
	return &grpcOrderClient{conn: conn}
}

// grpcOrderClient implements orderpb.OrderServiceClient โดยใช้ conn.Invoke()
// ในโปรดักชัน protoc จะ generate code นี้ให้อัตโนมัติ
type grpcOrderClient struct {
	conn *grpc.ClientConn
}

func (c *grpcOrderClient) PlaceOrder(ctx context.Context, in *orderpb.PlaceOrderRequest, opts ...grpc.CallOption) (*orderpb.PlaceOrderResponse, error) {
	out := new(orderpb.PlaceOrderResponse)
	err := c.conn.Invoke(ctx, "/order.OrderService/PlaceOrder", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *grpcOrderClient) GetOrder(ctx context.Context, in *orderpb.GetOrderRequest, opts ...grpc.CallOption) (*orderpb.GetOrderResponse, error) {
	out := new(orderpb.GetOrderResponse)
	err := c.conn.Invoke(ctx, "/order.OrderService/GetOrder", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *grpcOrderClient) ShipOrder(ctx context.Context, in *orderpb.ShipOrderRequest, opts ...grpc.CallOption) (orderpb.OrderService_ShipOrderClient, error) {
	stream, err := c.conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/order.OrderService/ShipOrder", opts...)
	if err != nil {
		return nil, err
	}
	x := &grpcOrderShipOrderClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// grpcOrderShipOrderClient implements OrderService_ShipOrderClient
type grpcOrderShipOrderClient struct {
	grpc.ClientStream
}

func (x *grpcOrderShipOrderClient) Recv() (*orderpb.ShipmentUpdate, error) {
	m := new(orderpb.ShipmentUpdate)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// Close ปิด connection
func (c *ResilientOrderClient) Close() error {
	return c.conn.Close()
}

// ─────────────────────────────────────────────
// Helper: addAuthMetadata เพิ่ม Bearer token ใน outgoing metadata
// ─────────────────────────────────────────────
func addAuthMetadata(ctx context.Context, token string) context.Context {
	md := metadata.Pairs(
		"authorization", "Bearer "+token,
		"x-request-id", fmt.Sprintf("req-%d", time.Now().UnixNano()),
	)
	return metadata.NewOutgoingContext(ctx, md)
}

// ─────────────────────────────────────────────
// Demo Scenarios
// ─────────────────────────────────────────────

// scenarioNormalCall แสดง PlaceOrder + GetOrder + ShipOrder ปกติ
func scenarioNormalCall(c *ResilientOrderClient) {
	fmt.Println("\n" + separator("Scenario 1: Normal Call"))

	// ─── 1. PlaceOrder ───
	// Deadline propagation: ตั้ง timeout ที่ context level
	// กฎ: deadline ควรครอบคลุม total operation (network + server processing + retries)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = addAuthMetadata(ctx, "demo-secret-token-2024")

	slog.Info("Placing order...")
	placeResp, err := c.client.PlaceOrder(ctx, &orderpb.PlaceOrderRequest{
		CustomerId: "customer-001",
		Items: []*orderpb.OrderItem{
			{
				ProductId:   "BOOK-001",
				ProductName: "Clean Code",
				Quantity:    1,
				UnitPrice:   59000, // 590 บาท
				Currency:    "THB",
			},
			{
				ProductId:   "BOOK-002",
				ProductName: "The Go Programming Language",
				Quantity:    2,
				UnitPrice:   72000, // 720 บาท
				Currency:    "THB",
			},
		},
		ShippingAddr: &orderpb.Address{
			Street:     "123 Sukhumvit Rd",
			City:       "Bangkok",
			Province:   "Bangkok",
			PostalCode: "10110",
			Country:    "TH",
		},
		PaymentMethod: orderpb.PaymentMethod_PAYMENT_METHOD_PROMPTPAY,
	})
	if err != nil {
		slog.Error("PlaceOrder failed", "error", err)
		return
	}

	orderID := placeResp.Order.OrderId
	slog.Info("Order placed successfully",
		"order_id", orderID,
		"total_amount", fmt.Sprintf("%.2f THB", float64(placeResp.Order.TotalAmount)/100),
		"payment_ref", placeResp.PaymentRef,
		"estimated_delivery", placeResp.EstimatedDelivery,
	)

	// ─── 2. GetOrder ───
	slog.Info("Getting order details...")
	getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer getCancel()
	getCtx = addAuthMetadata(getCtx, "demo-secret-token-2024")

	getResp, err := c.client.GetOrder(getCtx, &orderpb.GetOrderRequest{
		OrderId:    orderID,
		CustomerId: "customer-001",
	})
	if err != nil {
		slog.Error("GetOrder failed", "error", err)
		return
	}
	slog.Info("Order retrieved",
		"order_id", getResp.Order.OrderId,
		"status", getResp.Order.Status.String(),
	)

	// ─── 3. ShipOrder (Server Streaming) ───
	// Deadline สำหรับ streaming: ต้องนานพอสำหรับทุก events
	// หรือใช้ context.Background() กับ cancel() แทน timeout
	slog.Info("Starting shipment tracking stream...")
	shipCtx, shipCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shipCancel()
	shipCtx = addAuthMetadata(shipCtx, "demo-secret-token-2024")

	stream, err := c.client.ShipOrder(shipCtx, &orderpb.ShipOrderRequest{
		OrderId:    orderID,
		CustomerId: "customer-001",
	})
	if err != nil {
		slog.Error("ShipOrder failed to start", "error", err)
		return
	}

	// รับ events จาก stream จนกว่าจะ EOF
	for {
		update, err := stream.Recv()
		if err == io.EOF {
			// Server ปิด stream — ปกติ
			slog.Info("Shipment tracking complete")
			break
		}
		if err != nil {
			st, _ := status.FromError(err)
			slog.Error("Stream error",
				"code", st.Code().String(),
				"message", st.Message(),
			)
			break
		}

		slog.Info("Shipment update received",
			"event", update.EventType.String(),
			"location", update.Location,
			"message", update.Message,
			"tracking_no", update.TrackingNo,
		)
	}
}

// scenarioTimeout แสดง deadline propagation และ timeout handling
func scenarioTimeout(c *ResilientOrderClient) {
	fmt.Println("\n" + separator("Scenario 2: Timeout / Deadline"))

	slog.Info("Calling GetOrder with very short timeout (1ms)...")

	// ตั้ง deadline ที่สั้นมากจนแน่ใจว่า timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	ctx = addAuthMetadata(ctx, "demo-secret-token-2024")

	_, err := c.client.GetOrder(ctx, &orderpb.GetOrderRequest{
		OrderId:    "ORD-999999",
		CustomerId: "customer-001",
	})

	if err != nil {
		st, _ := status.FromError(err)

		// DEADLINE_EXCEEDED = context timeout ถึง
		// CANCELLED = context ถูก cancel (context.WithCancel)
		switch st.Code() {
		case codes.DeadlineExceeded:
			slog.Warn("Request timed out (expected)",
				"code", st.Code().String(),
				"tip", "ตั้ง deadline ที่เหมาะสม: p99 latency + buffer",
			)
		case codes.Canceled:
			slog.Warn("Request was cancelled")
		default:
			slog.Error("Unexpected error", "code", st.Code().String(), "msg", st.Message())
		}
	}

	slog.Info("Best practices for deadlines:",
		"rule1", "ตั้ง deadline ทุกครั้งที่ call (ไม่มี default timeout ใน gRPC)",
		"rule2", "ส่ง deadline ผ่าน context ไปยัง downstream calls",
		"rule3", "ใช้ p99 latency + 2x safety margin เป็น deadline",
	)
}

// scenarioCircuitBreaker แสดง circuit breaker states
func scenarioCircuitBreaker(c *ResilientOrderClient) {
	fmt.Println("\n" + separator("Scenario 3: Circuit Breaker"))

	slog.Info("Circuit breaker demo: จะส่ง requests ที่จะ fail เพื่อ trigger circuit open")

	// จำลองการส่ง request ที่จะล้มเหลวซ้ำๆ (wrong token = UNAUTHENTICATED)
	// gobreaker จะนับ failure และเปิด circuit หลังจาก threshold
	for i := 1; i <= 7; i++ {
		cbState := c.cb.State()

		_, err := c.cb.Execute(func() (interface{}, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// ใส่ wrong token เพื่อทำให้ fail
			ctx = addAuthMetadata(ctx, "wrong-token")

			resp, err := c.client.GetOrder(ctx, &orderpb.GetOrderRequest{
				OrderId:    "ORD-123",
				CustomerId: "customer-001",
			})
			if err != nil {
				// gobreaker นับ failure จาก error ที่ return
				return nil, err
			}
			return resp, nil
		})

		if err != nil {
			// ErrOpenState = circuit เปิดอยู่ ไม่ส่ง request ไปเลย
			if err == gobreaker.ErrOpenState {
				slog.Warn("Circuit is OPEN — request blocked immediately",
					"attempt", i,
					"state", cbState.String(),
					"benefit", "ประหยัด latency + ลด load บน server ที่ล้มเหลว",
				)
			} else if err == gobreaker.ErrTooManyRequests {
				slog.Warn("Circuit is HALF-OPEN — too many probe requests",
					"attempt", i,
				)
			} else {
				st, _ := status.FromError(err)
				slog.Info("Request failed (counting toward circuit open)",
					"attempt", i,
					"state", cbState.String(),
					"error_code", st.Code().String(),
				)
			}
		} else {
			slog.Info("Request succeeded",
				"attempt", i,
				"state", cbState.String(),
			)
		}

		time.Sleep(100 * time.Millisecond)
	}

	slog.Info("Circuit breaker final state", "state", c.cb.State().String())
	slog.Info("Circuit will enter HALF-OPEN after timeout (10s) to test recovery")

	// แสดงว่าถ้าส่ง request ขณะ OPEN จะได้อะไร
	slog.Info("\nSummary of Circuit Breaker States:",
		"CLOSED", "ส่ง request ปกติ นับ failure/success",
		"OPEN", "บล็อกทันที ไม่ส่งไปยัง server",
		"HALF-OPEN", "ส่งแค่บางส่วน ถ้า success ปิด circuit กลับ",
	)
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	slog.Info("gRPC Client starting, connecting to server...")

	client, err := newResilientOrderClient("localhost:50050")
	if err != nil {
		slog.Error("Failed to create client", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// รัน demo scenarios
	scenarioNormalCall(client)
	scenarioTimeout(client)
	scenarioCircuitBreaker(client)

	slog.Info("\nClient demo complete")
}

// separator สร้าง section header สำหรับ output
func separator(title string) string {
	line := "─────────────────────────────────────────────"
	return fmt.Sprintf("%s\n  %s\n%s", line, title, line)
}
