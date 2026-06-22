// Module 5: gRPC Server Demo — OrderService
//
// สิ่งที่จะเรียนรู้ในไฟล์นี้:
//  1. การ implement gRPC service (Unary + Server Streaming)
//  2. Interceptor Chain: logging → auth → metrics
//  3. Health Check Service (สำหรับ Kubernetes liveness/readiness probe)
//  4. Server Reflection (สำหรับ grpcurl)
//  5. Graceful Shutdown เมื่อรับ SIGTERM/SIGINT
//
// วิธีรัน:
//
//	go run module5/grpc_demo/server/main.go
//
// วิธีทดสอบด้วย grpcurl (ต้องติดตั้ง grpcurl ก่อน):
//
//	grpcurl -plaintext localhost:50051 list
//	grpcurl -plaintext -d '{"customer_id":"c1","items":[{"product_id":"p1","product_name":"Book","quantity":2,"unit_price":25000}]}' \
//	  localhost:50051 order.OrderService/PlaceOrder
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"tx_go_training/module5/proto/orderpb"
)

// ─────────────────────────────────────────────
// Metrics (atomic counters — ไม่ต้องใช้ Prometheus ในตัวอย่างนี้)
// ─────────────────────────────────────────────

// serverMetrics เก็บ metrics ของ gRPC server อย่างง่าย
// ในโปรดักชัน ใช้ github.com/grpc-ecosystem/go-grpc-prometheus แทน
type serverMetrics struct {
	requestCount   atomic.Int64
	errorCount     atomic.Int64
	latencyBuckets [10]atomic.Int64 // histogram buckets: <1ms,<5ms,<10ms,...
}

var metrics = &serverMetrics{}

// recordLatency บันทึก latency ลง histogram bucket
func (m *serverMetrics) recordLatency(d time.Duration) {
	ms := d.Milliseconds()
	idx := 0
	switch {
	case ms < 1:
		idx = 0
	case ms < 5:
		idx = 1
	case ms < 10:
		idx = 2
	case ms < 25:
		idx = 3
	case ms < 50:
		idx = 4
	case ms < 100:
		idx = 5
	case ms < 250:
		idx = 6
	case ms < 500:
		idx = 7
	case ms < 1000:
		idx = 8
	default:
		idx = 9
	}
	m.latencyBuckets[idx].Add(1)
}

// printMetrics แสดง metrics สรุป
func (m *serverMetrics) printMetrics() {
	bucketLabels := []string{"<1ms", "<5ms", "<10ms", "<25ms", "<50ms", "<100ms", "<250ms", "<500ms", "<1s", ">=1s"}
	slog.Info("=== Server Metrics ===",
		"total_requests", m.requestCount.Load(),
		"total_errors", m.errorCount.Load(),
	)
	for i, label := range bucketLabels {
		count := m.latencyBuckets[i].Load()
		if count > 0 {
			slog.Info("latency bucket", "range", label, "count", count)
		}
	}
}

// ─────────────────────────────────────────────
// In-memory Order Store (ใช้แทน database ในตัวอย่าง)
// ─────────────────────────────────────────────

type orderStore struct {
	mu     sync.RWMutex
	orders map[string]*orderpb.Order
}

func newOrderStore() *orderStore {
	return &orderStore{
		orders: make(map[string]*orderpb.Order),
	}
}

func (s *orderStore) save(o *orderpb.Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.OrderId] = o
}

func (s *orderStore) get(id string) (*orderpb.Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

// ─────────────────────────────────────────────
// OrderService Implementation
// ─────────────────────────────────────────────

// orderServiceServer implements orderpb.OrderServiceServer
type orderServiceServer struct {
	orderpb.UnimplementedOrderServiceServer
	store *orderStore
	log   *slog.Logger
}

func newOrderServiceServer() *orderServiceServer {
	return &orderServiceServer{
		store: newOrderStore(),
		log:   slog.Default(),
	}
}

// PlaceOrder สร้าง Order ใหม่ (Unary RPC)
// Flow: validate → calculate total → save → return
func (s *orderServiceServer) PlaceOrder(ctx context.Context, req *orderpb.PlaceOrderRequest) (*orderpb.PlaceOrderResponse, error) {
	s.log.InfoContext(ctx, "PlaceOrder called",
		"customer_id", req.CustomerId,
		"item_count", len(req.Items),
	)

	// Validate request
	if req.CustomerId == "" {
		return nil, status.Error(codes.InvalidArgument, "customer_id is required")
	}
	if len(req.Items) == 0 {
		return nil, status.Error(codes.InvalidArgument, "order must have at least one item")
	}
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			return nil, status.Errorf(codes.InvalidArgument,
				"item %s: quantity must be positive", item.ProductId)
		}
	}

	// คำนวณ total amount
	var total int64
	for _, item := range req.Items {
		total += item.UnitPrice * int64(item.Quantity)
	}

	// สร้าง Order object
	now := time.Now().Unix()
	order := &orderpb.Order{
		OrderId:       fmt.Sprintf("ORD-%d", now),
		CustomerId:    req.CustomerId,
		Items:         req.Items,
		Status:        orderpb.OrderStatus_ORDER_STATUS_PENDING,
		ShippingAddr:  req.ShippingAddr,
		PaymentMethod: req.PaymentMethod,
		TotalAmount:   total,
		Currency:      "THB",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	s.store.save(order)

	s.log.InfoContext(ctx, "Order created",
		"order_id", order.OrderId,
		"total_amount", order.TotalAmount,
	)

	return &orderpb.PlaceOrderResponse{
		Order:             order,
		PaymentRef:        fmt.Sprintf("PAY-%d", now),
		EstimatedDelivery: time.Now().Add(3 * 24 * time.Hour).Format("2006-01-02"),
	}, nil
}

// GetOrder ดึงข้อมูล Order (Unary RPC)
func (s *orderServiceServer) GetOrder(ctx context.Context, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	s.log.InfoContext(ctx, "GetOrder called", "order_id", req.OrderId)

	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	order, ok := s.store.get(req.OrderId)
	if !ok {
		// ส่ง NOT_FOUND พร้อม detail เพื่อให้ client รู้ว่า Order ไหนหาย
		return nil, status.Errorf(codes.NotFound,
			"order %q not found", req.OrderId)
	}

	// ตรวจสอบ ownership (ถ้ามี customer_id ใน request)
	if req.CustomerId != "" && order.CustomerId != req.CustomerId {
		// ใช้ PERMISSION_DENIED แทน NOT_FOUND เพื่อหลีกเลี่ยง information leakage
		return nil, status.Error(codes.PermissionDenied,
			"you do not have permission to view this order")
	}

	return &orderpb.GetOrderResponse{Order: order}, nil
}

// ShipOrder ติดตามสถานะการจัดส่ง (Server Streaming RPC)
// จำลองการส่ง event ทีละ step จนกว่าจะ delivered หรือ failed
func (s *orderServiceServer) ShipOrder(req *orderpb.ShipOrderRequest, stream orderpb.OrderService_ShipOrderServer) error {
	s.log.Info("ShipOrder stream started", "order_id", req.OrderId)

	order, ok := s.store.get(req.OrderId)
	if !ok {
		return status.Errorf(codes.NotFound, "order %q not found", req.OrderId)
	}

	// อัพเดต status ของ Order เป็น SHIPPED
	order.Status = orderpb.OrderStatus_ORDER_STATUS_SHIPPED
	order.UpdatedAt = time.Now().Unix()
	s.store.save(order)

	// กำหนด shipment events ที่จะส่ง
	events := []struct {
		eventType orderpb.ShipmentEventType
		location  string
		message   string
		delay     time.Duration
	}{
		{orderpb.ShipmentEventType_SHIPMENT_EVENT_PICKED_UP, "Bangkok Warehouse", "พัสดุถูกรับไปแล้ว", 500 * time.Millisecond},
		{orderpb.ShipmentEventType_SHIPMENT_EVENT_IN_TRANSIT, "Sorting Center BKK", "กำลังจัดเรียงสินค้า", 800 * time.Millisecond},
		{orderpb.ShipmentEventType_SHIPMENT_EVENT_IN_TRANSIT, "Regional Hub", "อยู่ระหว่างขนส่งไปยังพื้นที่ปลายทาง", 600 * time.Millisecond},
		{orderpb.ShipmentEventType_SHIPMENT_EVENT_OUT_FOR_DEL, "Local Delivery Center", "กำลังนำส่งถึงที่อยู่ของท่าน", 400 * time.Millisecond},
		{orderpb.ShipmentEventType_SHIPMENT_EVENT_DELIVERED, "Destination", "ส่งถึงปลายทางเรียบร้อย", 300 * time.Millisecond},
	}

	trackingNo := fmt.Sprintf("TH%d", time.Now().UnixNano()%1000000)

	for _, evt := range events {
		// ตรวจสอบว่า client ยังเชื่อมต่ออยู่ (context ยังไม่ถูก cancel)
		select {
		case <-stream.Context().Done():
			s.log.Info("Client disconnected", "order_id", req.OrderId)
			return nil
		default:
		}

		// จำลอง delay ระหว่าง events (ในโปรดักชันอาจเป็น event จาก message queue)
		time.Sleep(evt.delay)

		update := &orderpb.ShipmentUpdate{
			OrderId:    req.OrderId,
			EventType:  evt.eventType,
			Location:   evt.location,
			Message:    evt.message,
			Timestamp:  time.Now().Unix(),
			Carrier:    "Kerry Express",
			TrackingNo: trackingNo,
		}

		// ส่ง event ไปยัง client ผ่าน stream
		if err := stream.Send(update); err != nil {
			s.log.Error("Failed to send shipment update",
				"error", err,
				"order_id", req.OrderId,
			)
			return err
		}

		s.log.Info("Sent shipment update",
			"order_id", req.OrderId,
			"event", evt.eventType.String(),
			"location", evt.location,
		)
	}

	// อัพเดต Order status เป็น DELIVERED
	order.Status = orderpb.OrderStatus_ORDER_STATUS_DELIVERED
	order.UpdatedAt = time.Now().Unix()
	s.store.save(order)

	s.log.Info("ShipOrder stream completed", "order_id", req.OrderId)
	return nil // EOF — บอก client ว่า stream จบแล้ว
}

// ─────────────────────────────────────────────
// Interceptors (Middleware)
//
// gRPC Interceptor เทียบเท่ากับ HTTP middleware
// ทำงานในลำดับ: loggingInterceptor → authInterceptor → metricsInterceptor → handler
// ─────────────────────────────────────────────

// loggingInterceptor บันทึก request/response ทุกครั้ง
// เหตุผลที่ต้องมี: ต้องการ observability ของทุก request โดยไม่ต้องแก้ไข business logic
func loggingInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()

	// ดึง metadata (headers) จาก context
	md, _ := metadata.FromIncomingContext(ctx)
	requestID := ""
	if vals := md.Get("x-request-id"); len(vals) > 0 {
		requestID = vals[0]
	}

	slog.InfoContext(ctx, "gRPC request started",
		"method", info.FullMethod,
		"request_id", requestID,
	)

	// เรียก handler ถัดไปใน chain
	resp, err := handler(ctx, req)

	duration := time.Since(start)
	if err != nil {
		st, _ := status.FromError(err)
		slog.ErrorContext(ctx, "gRPC request failed",
			"method", info.FullMethod,
			"duration_ms", duration.Milliseconds(),
			"error_code", st.Code().String(),
			"error_message", st.Message(),
		)
	} else {
		slog.InfoContext(ctx, "gRPC request completed",
			"method", info.FullMethod,
			"duration_ms", duration.Milliseconds(),
		)
	}

	return resp, err
}

// authInterceptor ตรวจสอบ Bearer token ใน metadata
// เหตุผลที่ต้องมี: security ต้องอยู่ที่ transport layer ไม่ใช่ business logic
// ในโปรดักชัน ใช้ JWT validation หรือ mTLS แทน hardcoded token
func authInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// Health check ไม่ต้อง auth
	if info.FullMethod == "/grpc.health.v1.Health/Check" {
		return handler(ctx, req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata is not provided")
	}

	// ดึง Authorization header
	authHeader := md.Get("authorization")
	if len(authHeader) == 0 {
		return nil, status.Error(codes.Unauthenticated, "authorization token is not provided")
	}

	token := authHeader[0]
	// รูปแบบ: "Bearer <token>"
	const prefix = "Bearer "
	if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
		return nil, status.Error(codes.Unauthenticated, "invalid token format")
	}
	token = token[len(prefix):]

	// ในโปรดักชัน: ตรวจสอบ JWT signature, expiry, claims
	// ที่นี่ใช้ hardcoded token เพื่อการสอน
	const validToken = "demo-secret-token-2024"
	if token != validToken {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	// เพิ่ม user info เข้า context เพื่อให้ handler ใช้ได้
	ctx = context.WithValue(ctx, contextKeyUserID{}, "demo-user")

	return handler(ctx, req)
}

// contextKeyUserID ใช้เป็น key ใน context เพื่อหลีกเลี่ยง collision
type contextKeyUserID struct{}

// metricsInterceptor นับ request และบันทึก latency
// เหตุผลที่ต้องมี: ต้องการ SLA monitoring และ alerting โดยไม่กระทบ business logic
func metricsInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()

	// เพิ่ม request counter
	metrics.requestCount.Add(1)

	resp, err := handler(ctx, req)

	// บันทึก latency
	metrics.recordLatency(time.Since(start))

	if err != nil {
		metrics.errorCount.Add(1)
	}

	return resp, err
}

// ─────────────────────────────────────────────
// chainUnaryInterceptors รวม interceptors หลายตัวเป็น chain
// gRPC v1.13+ มี grpc.ChainUnaryInterceptor() built-in แล้ว
// ฟังก์ชันนี้แสดงให้เห็นว่า chain ทำงานอย่างไรภายใน
// ─────────────────────────────────────────────
func chainUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// สร้าง handler chain จาก inner → outer
		// ลำดับการทำงาน: interceptors[0] → interceptors[1] → ... → handler
		chained := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			interceptor := interceptors[i]
			next := chained
			chained = func(ctx context.Context, req interface{}) (interface{}, error) {
				return interceptor(ctx, req, info, next)
			}
		}
		return chained(ctx, req)
	}
}

// ─────────────────────────────────────────────
// main
// ─────────────────────────────────────────────

func main() {
	// ตั้งค่า structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Go 1.20+ auto-seeds global rand แล้ว ไม่ต้อง seed เพิ่ม
	// สำหรับ reproducible tests: rand.New(rand.NewSource(fixedSeed))
	rand.Seed(time.Now().UnixNano()) //nolint:staticcheck // Go 1.20 deprecated แต่ยังใช้ได้

	const addr = ":50050"
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("Failed to listen", "error", err)
		os.Exit(1)
	}

	// ─── สร้าง gRPC Server ด้วย Interceptor Chain ───
	//
	// ลำดับ interceptor สำคัญมาก:
	// Request:  logging → auth → metrics → handler
	// Response: handler → metrics → auth → logging
	//
	// auth ต้องอยู่หลัง logging เพื่อให้ log ทุก request แม้แต่ที่ unauthorized
	// metrics ต้องอยู่หลัง auth เพื่อนับเฉพาะ authenticated request
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			loggingInterceptor, // Interceptor 1: บันทึก log ทุก request
			authInterceptor,    // Interceptor 2: ตรวจสอบ token
			metricsInterceptor, // Interceptor 3: บันทึก metrics
		),
	)

	// ─── ลงทะเบียน OrderService ───
	orderpb.RegisterOrderServiceServer(grpcServer, newOrderServiceServer())

	// ─── ลงทะเบียน Health Check Service ───
	// Kubernetes ใช้ /healthz หรือ gRPC health check เพื่อตรวจสอบ pod status
	// grpc-health-probe: https://github.com/grpc-ecosystem/grpc-health-probe
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	// ตั้งค่า initial status
	healthServer.SetServingStatus("order.OrderService", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING) // overall

	// ─── เปิด Server Reflection ───
	// ช่วยให้ grpcurl และ tools อื่นๆ สามารถ discover services ได้
	// ไม่ควรเปิดใน production (เปิดเผย schema)
	reflection.Register(grpcServer)

	slog.Info("gRPC server starting",
		"addr", addr,
		"services", []string{"order.OrderService", "grpc.health.v1.Health"},
	)

	// ─── Goroutine สำหรับ Graceful Shutdown ───
	//
	// Graceful Shutdown = ปิด server โดย:
	//   1. หยุดรับ connection ใหม่
	//   2. รอให้ in-flight requests เสร็จสิ้น
	//   3. ปิด connection ทั้งหมด
	//
	// สำคัญมากใน Kubernetes เพื่อหลีกเลี่ยง request drop ระหว่าง rolling deploy
	stopCh := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		sig := <-sigCh

		slog.Info("Received signal, initiating graceful shutdown", "signal", sig)

		// แจ้ง health check ว่ากำลังปิด (Kubernetes จะหยุดส่ง traffic มา)
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

		// รอสักครู่เพื่อให้ load balancer หยุดส่ง traffic
		time.Sleep(2 * time.Second)

		// Graceful stop: รอ in-flight requests เสร็จ (timeout 30s)
		done := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(done)
		}()

		select {
		case <-done:
			slog.Info("Server stopped gracefully")
		case <-time.After(30 * time.Second):
			slog.Warn("Graceful stop timed out, forcing shutdown")
			grpcServer.Stop()
		}

		// แสดง metrics ก่อนปิด
		metrics.printMetrics()
		close(stopCh)
	}()

	// ─── เริ่ม Serve ───
	if err := grpcServer.Serve(lis); err != nil {
		// หลังจาก GracefulStop(), Serve() จะ return nil หรือ ErrServerStopped
		slog.Info("Server stopped", "reason", err)
	}

	<-stopCh
	slog.Info("Shutdown complete")
}
