# Module 8 OpenTelemetry — Before / After Fix Evidence

ทดสอบเมื่อ 2026-10-29 บน macOS (Go 1.27, Docker 29.7) ด้วย `docker compose up -d` ตาม MODULE8_NOTES.md

- **before/** = โค้ดที่ commit `3d17d91` (HEAD เดิม)
- **after/** = โค้ดหลังแก้

Load generator เป็น Go client เล็กๆ ที่ยิง `POST /orders` N ครั้ง, `GET /orders`, `GET /orders/get?id=…`, path ขยะ 3 path แล้วรอ 7 วินาทีให้ batch exporter ส่ง span ก่อนจะ query Jaeger API

## สรุป

| # | ปัญหา | Before | After | ไฟล์ |
|---|-------|--------|-------|------|
| 1 | `go.sum` ขาด entry | `go build` fail: `missing go.sum entry … golang.org/x/net/trace` | build + vet ผ่าน | `01_build.txt` |
| 2 | OTLP port ไม่ตรง (compose map `4320:4318`, โค้ดใช้ `localhost:4318`) | `traces export: … connect: connection refused`, Jaeger มี 0 trace | ไม่มี export error, trace ขึ้นใน Jaeger | `03_service_console.txt`, `04_load_and_jaeger_query.txt`, `05_jaeger_search.png` |
| 3 | Span hierarchy ซ้อนกันเป็นสาย (reassign `ctx`) | ลึก 5 ชั้น: validate → inventory → payment ซ้อนกัน | ลึก 3 ชั้น: ทั้งสามเป็น sibling ใต้ `createOrder` | `10_jaeger_trace_span_tree.png`, `02_tests.txt` |
| 4 | `processOrdersInParallel` ไม่รอทุก goroutine | `WARNING: DATA RACE`, return เมื่อเสร็จ 19/20 | PASS ภายใต้ `-race` | `02_tests.txt` |
| 5 | tracing demo รับทุก HTTP method | `GET /orders` → 201 (สร้าง order) | `GET /orders` → 405 | `02_tests.txt`, `11_tracing_demo_load.txt` |
| 6 | Metric label ใช้ raw path (cardinality) | `/scan-0-1`, `/scan-0-2`, `/scan-0-3` กลายเป็น series ใหม่ทั้งหมด | รวมเป็น `path="unmatched"` | `07_prometheus_path_labels.png`, `06_grafana_dashboard.png` |
| 7 | ไม่ flush span ตอน shutdown | complete_service: `shutdown(ctx)` ใช้ ctx ที่ถูก cancel ไปแล้ว / tracing demo: ไม่มี graceful shutdown | flush ด้วย ctx ใหม่ที่มี timeout, tracing demo พิมพ์ `stopped, spans flushed` | `12_tracing_demo_console.txt` |
| 8 | `TotalBaht` ไม่ตรงกับ items | item ราคา 35,900 แต่ total สุ่ม 100–10,000 | total = price × quantity | `04_load_and_jaeger_query.txt` |

`before/08_workaround_span_tree.txt` คือโค้ดเดิมที่ตั้ง `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4320` เอง เพื่อพิสูจน์ว่าถ้าเลี่ยงข้อ 2 ได้ก็ยังเจอข้อ 3 อยู่ดี

## ข้อจำกัด

- ข้อ 7 ยืนยันได้จาก console log และ code path เท่านั้น ส่วนการทดสอบว่า "span สุดท้ายหายไหม" ใน harness นี้สรุปไม่ได้ เพราะระยะเวลาระหว่าง POST กับ SIGTERM นานเกิน batch interval 5 วินาที span เลยถูก export ไปก่อนแล้ว
- กราฟ Grafana ของ before กับ after ใช้ปริมาณ load ไม่เท่ากัน ควรเทียบที่ legend ของ Traffic panel (series ของ path) ไม่ใช่ที่ค่าตัวเลข
- `before/02_tests.txt` ไม่ได้รัน `TestRouteLabel` เพราะโค้ดเดิมยังไม่มีฟังก์ชัน `routeLabel` และต้อง `go mod tidy` ก่อน ถึงจะ compile โค้ดเดิมได้
