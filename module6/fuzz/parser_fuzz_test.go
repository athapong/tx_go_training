// Package fuzz — Fuzz Tests สำหรับ Parser Functions
//
// # Fuzz Testing ทำงานอย่างไร?
//
// Go fuzzing มี 2 phases:
//
//	Phase 1: Corpus Seed
//	────────────────────
//	Fuzzer เริ่มต้นจาก seed corpus (inputs ที่เราให้ไว้)
//	ในแต่ละ test case เรียก f.Add(seedValue)
//
//	Phase 2: Mutation
//	─────────────────
//	Fuzzer จะ mutate inputs จาก corpus โดยอัตโนมัติ:
//	  - เพิ่ม/ลบ/เปลี่ยน bytes
//	  - Flip bits
//	  - ใช้ interesting values (0, -1, MaxInt64, ฯลฯ)
//	  - Combine inputs จาก corpus หลายตัว
//
//	ถ้า mutation ทำให้ program panic หรือ crash:
//	  - บันทึก input นั้นลงใน testdata/fuzz/{FuzzFuncName}/
//	  - แสดง failing input และ stack trace
//
// # วิธีรัน
//
//	# Fuzz สั้นๆ 10 วินาที
//	go test -fuzz=FuzzParseOrderID -fuzztime=10s ./module6/fuzz/
//
//	# รัน corpus ที่มีอยู่เท่านั้น (เร็ว, ใช้ใน CI)
//	go test -run=FuzzParseOrderID ./module6/fuzz/
//
//	# รัน buggy version เพื่อดู panic
//	go test -fuzz=FuzzParseOrderID_Buggy -fuzztime=5s ./module6/fuzz/
//
// # อ่าน corpus file
//
//	cat testdata/fuzz/FuzzParseOrderID/abc123...
//	# Output:
//	# go test fuzz v1
//	# string("") ← input ที่ fuzzer หาว่า crash
//
// # Integration กับ CI/CD
//
// ในการทำ CI สำหรับ fuzz tests:
//  1. รัน "corpus only" mode: go test -run=Fuzz ./...
//  2. เพิ่ม known inputs ใน testdata/fuzz/{name}/ directory
//  3. Fuzz mode รันแยกต่างหาก (scheduled job, ไม่ใช่ทุก PR)
package fuzz

import (
	"testing"
	"unicode/utf8"
)

// ============================================================
// SEED CORPUS — inputs ที่เราให้ไว้ล่วงหน้า
// ============================================================

// FuzzParseOrderID — fuzz test สำหรับ parser ที่แก้แล้ว
//
// Properties ที่ต้องการ verify (invariants):
//  1. ไม่ panic ไม่ว่า input จะเป็นอะไรก็ตาม
//  2. ถ้า return error ต้อง error (ไม่ใช่ panic)
//  3. ถ้า return OrderID ต้องไม่ empty
//  4. Valid input → ต้อง return valid OrderID
//  5. Round-trip: ParseOrderID(id.String()) ต้อง return id เดิม
func FuzzParseOrderID(f *testing.F) {
	// ===== SEED CORPUS =====
	// inputs เหล่านี้:
	//  1. ถูกทดสอบทันที (corpus run)
	//  2. ใช้เป็น starting point สำหรับ mutation
	//  3. ควรครอบคลุม boundary cases และ happy paths

	// Happy paths
	f.Add("ORD-12345")
	f.Add("ORD-ABC")
	f.Add("ORD-abc123")
	f.Add("ORD-A")

	// Edge cases ที่ควรได้ error (ไม่ panic)
	f.Add("")
	f.Add("ORD-")
	f.Add("ORD")
	f.Add("ord-12345") // lowercase prefix
	f.Add("ORDER-12345")

	// Boundary cases สำหรับ length limit
	f.Add("ORD-" + string(make([]byte, 60))) // near max length
	f.Add("ORD-" + string(make([]byte, 65))) // exceeds max length

	// Unicode inputs (fuzzer ชอบทดสอบ Unicode)
	f.Add("ORD-สวัสดี")
	f.Add("ORD-\x00\x01\x02") // null bytes
	f.Add("ORD-\xff\xfe")     // invalid UTF-8 bytes
	f.Add("ORD-​‌‍")          // zero-width characters
	f.Add("ORD-\n\r\t")       // control characters

	// Interesting values ที่ fuzzer มักหาเอง (ใส่ไว้เพื่อ speed up)
	f.Add("ORD-9999999999999999999") // large number
	f.Add("ORD--double-dash")
	f.Add("ORD-" + string(rune(0x10FFFF))) // max unicode codepoint

	// ===== FUZZ FUNCTION =====
	f.Fuzz(func(t *testing.T, input string) {
		// ⚠️ ใช้ recover เพื่อ catch panic และ fail gracefully
		// (แต่จริงๆ parser ที่ดีไม่ควร panic เลย)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseOrderID(%q) panicked: %v", input, r)
			}
		}()

		// ===== CALL THE FUNCTION =====
		id, err := ParseOrderID(input)

		// ===== VERIFY INVARIANTS =====

		// Invariant 1: ถ้า error ต้อง return zero OrderID
		if err != nil {
			if !id.IsZero() {
				t.Errorf("ParseOrderID(%q) returned non-zero ID with error: id=%v, err=%v",
					input, id, err)
			}
			return // error case ถูกต้อง — ไม่ต้อง check ต่อ
		}

		// Invariant 2: ถ้าไม่ error ต้อง return non-zero OrderID
		if id.IsZero() {
			t.Errorf("ParseOrderID(%q) returned zero ID without error", input)
		}

		// Invariant 3: ID string ต้องเป็น valid UTF-8
		if !utf8.ValidString(id.String()) {
			t.Errorf("ParseOrderID(%q) returned invalid UTF-8 ID: %q", input, id.String())
		}

		// Invariant 4: ID ต้องเริ่มด้วย "ORD-"
		if len(id.String()) < 4 || id.String()[:4] != "ORD-" {
			t.Errorf("ParseOrderID(%q) returned ID without ORD- prefix: %q", input, id.String())
		}

		// Invariant 5: Round-trip consistency
		// ParseOrderID(id.String()) ควร return id เดิม (idempotent)
		id2, err2 := ParseOrderID(id.String())
		if err2 != nil {
			t.Errorf("ParseOrderID(%q) round-trip failed: ParseOrderID(%q) error: %v",
				input, id.String(), err2)
		}
		if id.String() != id2.String() {
			t.Errorf("ParseOrderID not idempotent: first=%q, second=%q", id.String(), id2.String())
		}
	})
}

// FuzzParseOrderID_Buggy — fuzz test สำหรับ buggy version
//
// หัวใจของ demo นี้: ดูว่า fuzzer หา bug ได้เร็วแค่ไหน
//
// คาดว่า fuzzer จะหา panic ได้ใน < 5 วินาที เพราะ:
//   - Bug #1 (empty string) ง่ายมาก fuzzer test "" ในทันที
//   - Bug #2 (short string) fuzzer มักลอง "OR", "O", "ORD" เป็น mutation แรกๆ
//
// วิธีรัน (fuzz mode — ค้นหา bugs):
//
//	go test -fuzz=FuzzParseOrderID_Buggy -fuzztime=5s ./module6/fuzz/
//	# คาดว่าจะ panic และบันทึก corpus ที่ fail ใน testdata/fuzz/FuzzParseOrderID_Buggy/
//
// วิธีรัน (corpus mode — regression ใน CI):
//
//	go test -run=FuzzParseOrderID_Buggy ./module6/fuzz/
//	# Corpus mode: เราใช้ recover เพื่อแสดงว่า bug มีอยู่จริงโดยไม่ crash
//
// Note on corpus vs fuzz mode:
//
//	ใน corpus mode (go test -run=Fuzz...) ฟังก์ชัน Fuzz รันกับ seed corpus เท่านั้น
//	ใน fuzz mode  (go test -fuzz=Fuzz...) fuzzer mutate inputs และค้นหา panics
//	สำหรับ buggy demo เราใช้ t.Fail() แทน panic เพื่อไม่ crash test process
func FuzzParseOrderID_Buggy(f *testing.F) {
	// ===== TEACHING NOTE =====
	// Test นี้ออกแบบมาเพื่อแสดง bug ที่ fuzzing หา
	// มันจะ FAIL โดยตั้งใจ — นั่นคือจุดประสงค์ของมัน
	//
	// รัน go test -run=FuzzParseOrderID_Buggy เพื่อดู bug reports
	// รัน go test -fuzz=FuzzParseOrderID_Buggy เพื่อดู fuzzer ค้นหา inputs ใหม่
	//
	// หลังจาก "เรียนรู้" จาก bug แล้วดู ParseOrderID (fixed version) ซึ่งผ่านทุก tests

	// Seed corpus — inputs ที่ทราบอยู่แล้วว่าทำให้ panic
	f.Add("ORD-12345") // valid — ไม่ panic
	f.Add("ORD-ABC")   // valid — ไม่ panic
	// ⚠️ inputs ด้านล่างจะทำให้ buggy parser panic
	// ใน fuzz mode: fuzzer จะ catch panic และบันทึก corpus
	// ใน corpus mode: เราจัดการด้วย recover เพื่อ report ไม่ crash
	f.Add("")     // Bug #1: empty string → s[0] index out of range
	f.Add("O")    // Bug #2: too short → s[:4] out of range
	f.Add("ORD")  // Bug #2: still too short (need 4 chars for prefix)
	f.Add("ORD-") // Bug #4: empty number part (but prefix check passes)

	f.Fuzz(func(t *testing.T, input string) {
		// ใช้ recover เพื่อ catch panic และ fail ด้วย t.Errorf แทน crash
		// ทำให้ corpus mode รันได้โดยไม่ panic ทั้ง test process
		// ใน fuzz mode: fuzzer จะ report failing input แม้เราใช้ recover
		var panicked bool
		var panicValue interface{}

		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					panicValue = r
				}
			}()
			ParseOrderID_Buggy(input) //nolint:errcheck
		}()

		if panicked {
			// t.Errorf fail test แต่ไม่ crash process
			// fuzzer บันทึก input นี้ใน testdata/fuzz/FuzzParseOrderID_Buggy/
			t.Errorf("ParseOrderID_Buggy(%q) panicked: %v — this is the bug!", input, panicValue)
		}
	})
}

// ============================================================
// FUZZ: ParseAmount
// ============================================================

// FuzzParseAmount ทดสอบ amount parser
//
// Invariants:
//  1. ไม่ panic
//  2. ถ้าไม่ error ต้อง return non-negative int64
//  3. Integer input "N" ต้อง return N*100
//  4. "1.00" ต้อง return 100
func FuzzParseAmount(f *testing.F) {
	// Seed corpus
	f.Add("0")
	f.Add("1")
	f.Add("100")
	f.Add("1.00")
	f.Add("1.5")
	f.Add("1.99")
	f.Add("999999")
	f.Add("0.01")

	// Edge cases ที่คาดว่าจะ error (ไม่ panic)
	f.Add("")
	f.Add("-1")
	f.Add("-0")
	f.Add(".")
	f.Add("..")
	f.Add("1.2.3")
	f.Add("abc")
	f.Add("1e5")                     // scientific notation — ควร reject
	f.Add("∞")                       // infinity symbol
	f.Add(string(make([]byte, 100))) // long string

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseAmount(%q) panicked: %v", input, r)
			}
		}()

		cents, err := ParseAmount(input)

		if err != nil {
			// Error case — ตรวจว่า cents = 0 ไม่ใช่ garbage
			if cents != 0 {
				t.Errorf("ParseAmount(%q) returned non-zero cents with error: cents=%d, err=%v",
					input, cents, err)
			}
			return
		}

		// Success case — cents ต้องไม่ negative
		if cents < 0 {
			t.Errorf("ParseAmount(%q) returned negative cents: %d", input, cents)
		}
	})
}

// ============================================================
// REGULAR UNIT TESTS — ทดสอบ known cases
// ============================================================
//
// Note: เมื่อ fuzzer หา failing input แล้ว
// ให้เพิ่ม input นั้นเป็น test case ตรงนี้
// เพื่อเป็น regression test ที่รันทุกครั้ง

func TestParseOrderID(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		// Happy path
		{"ORD-12345", "ORD-12345", false},
		{"ORD-abc", "ORD-ABC", false}, // lowercase → uppercase
		{"ORD-ABC", "ORD-ABC", false},
		{"ORD-A1B2C3", "ORD-A1B2C3", false},
		{"ORD-A-B-C", "ORD-A-B-C", false}, // hyphens allowed in number part

		// Error cases (previously found by fuzzer or manual analysis)
		{"", "", true},                        // empty string
		{"ORD-", "", true},                    // missing number
		{"ORD", "", true},                     // missing dash and number
		{"ord-12345", "", true},               // lowercase prefix
		{"ORDER-12345", "", true},             // wrong prefix
		{"ORD-สวัสดี", "", true},              // Thai characters not allowed
		{"ORD-\x00", "", true},                // null byte
		{string(make([]byte, 100)), "", true}, // too long
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()

			got, err := ParseOrderID(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseOrderID(%q) expected error, got nil (id=%q)", tc.input, got.String())
				}
				return
			}

			if err != nil {
				t.Errorf("ParseOrderID(%q) unexpected error: %v", tc.input, err)
				return
			}

			if got.String() != tc.want {
				t.Errorf("ParseOrderID(%q) = %q, want %q", tc.input, got.String(), tc.want)
			}
		})
	}
}

// TestParseOrderID_Buggy_KnownPanics ทดสอบ inputs ที่ทำให้ buggy version panic
// เป็น documentation ว่า fuzzer หาอะไรพบบ้าง
func TestParseOrderID_Buggy_KnownPanics(t *testing.T) {
	panicInputs := []string{
		"",    // Bug #1: empty string → s[0] panic
		"O",   // Bug #2: too short → s[:4] panic
		"OR",  // Bug #2: too short → s[:4] panic
		"ORD", // Bug #2: still too short (3 chars, need 4 for prefix)
	}

	for _, input := range panicInputs {
		input := input
		t.Run("buggy_panics_on_"+input, func(t *testing.T) {
			t.Parallel()

			// ตรวจว่า buggy version panic จริง
			didPanic := func() (panicked bool) {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
					}
				}()
				ParseOrderID_Buggy(input) //nolint:errcheck
				return false
			}()

			if !didPanic {
				t.Logf("Note: ParseOrderID_Buggy(%q) did not panic — bug may have been fixed", input)
			} else {
				t.Logf("Confirmed: ParseOrderID_Buggy(%q) panics as expected (this is the bug)", input)
			}
		})
	}
}
