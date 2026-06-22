// Package fuzz — Fuzz Testing สำหรับ Parser Functions
//
// Fuzz testing คือการทดสอบโดยให้ Go runtime สร้าง input แบบสุ่ม
// เพื่อหา edge cases ที่เราคิดไม่ถึง
//
// เหมาะสำหรับ:
//   - Parser functions (รับ string/bytes และแปลงเป็น structured data)
//   - Serialization/deserialization
//   - Cryptographic functions
//   - Any function ที่รับ untrusted input
//
// วิธีรัน:
//
//	# รัน fuzz test (ไม่หยุดเองจนกว่าจะเจอ bug หรือ Ctrl+C)
//	go test -fuzz=FuzzParseOrderID ./module6/fuzz/
//
//	# รัน 30 วินาที
//	go test -fuzz=FuzzParseOrderID -fuzztime=30s ./module6/fuzz/
//
//	# รัน corpus ที่มีอยู่ (regression test — รันได้ใน CI)
//	go test -run=FuzzParseOrderID ./module6/fuzz/
//
//	# ดู corpus files ที่ fuzzer สร้าง
//	ls testdata/fuzz/FuzzParseOrderID/
package fuzz

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// OrderID — typed ID สำหรับ order
type OrderID struct {
	value string
}

// String returns the string representation of OrderID
func (id OrderID) String() string { return id.value }

// IsZero returns true ถ้า ID ว่าง
func (id OrderID) IsZero() bool { return id.value == "" }

// ============================================================
// VERSION 1: BUGGY PARSER — มี bug ที่ fuzzing จะหา
// ============================================================
//
// ParseOrderID_Buggy — parser ที่มีข้อบกพร่อง
//
// Format ที่คาดหวัง: "ORD-{number}" เช่น "ORD-12345"
//
// Bug ที่ซ่อนอยู่:
//  1. ไม่ตรวจ empty string → index out of range panic
//  2. ไม่ validate UTF-8 → rune conversion อาจ panic
//  3. ใช้ string[0] โดยตรง → panic ถ้า string ว่าง
//
// Fuzzer จะหา input ที่ทำให้ panic ภายในไม่กี่วินาที
func ParseOrderID_Buggy(s string) (OrderID, error) {
	// BUG #1: ไม่ตรวจ empty string
	// ถ้า s == "" → s[0] panic: index out of range
	if s[0] != 'O' { // ⚠️ PANIC ถ้า s == ""
		return OrderID{}, fmt.Errorf("invalid prefix: must start with 'O'")
	}

	// BUG #2: ไม่ตรวจว่า string ยาวพอ
	// ถ้า s = "OR" → s[:4] panic: slice bounds out of range
	prefix := s[:4] // ⚠️ PANIC ถ้า len(s) < 4
	if prefix != "ORD-" {
		return OrderID{}, fmt.Errorf("invalid prefix: expected 'ORD-', got %q", prefix)
	}

	// BUG #3: ไม่ validate UTF-8 encoding
	// strings.ToUpper กับ invalid UTF-8 อาจทำให้เกิด unexpected behavior
	number := s[4:]
	if len(number) == 0 {
		return OrderID{}, fmt.Errorf("missing number part")
	}

	// BUG #4: ไม่จำกัด length — ทำให้สร้าง OrderID ยาวได้ไม่จำกัด
	// อาจทำให้ memory exhaustion ใน downstream system
	upper := strings.ToUpper(number)

	return OrderID{value: "ORD-" + upper}, nil
}

// ============================================================
// VERSION 2: FIXED PARSER — แก้ bugs ที่ fuzzing หา
// ============================================================
//
// ParseOrderID — parser ที่แก้ bugs ทั้งหมดแล้ว
//
// Lessons from fuzzing:
//  1. Always check empty string first
//  2. Check length before indexing
//  3. Validate UTF-8 explicitly
//  4. Limit input length
//  5. Use strings.HasPrefix ไม่ใช่ direct indexing
func ParseOrderID(s string) (OrderID, error) {
	// FIX #1: ตรวจ empty string ก่อนอื่น
	if s == "" {
		return OrderID{}, fmt.Errorf("orderID cannot be empty")
	}

	// FIX #2: จำกัด length ป้องกัน resource exhaustion
	const maxLength = 64
	if len(s) > maxLength {
		return OrderID{}, fmt.Errorf("orderID too long: max %d chars, got %d", maxLength, len(s))
	}

	// FIX #3: ตรวจ UTF-8 validity ก่อนประมวลผล
	if !utf8.ValidString(s) {
		return OrderID{}, fmt.Errorf("orderID contains invalid UTF-8 sequence")
	}

	// FIX #4: ใช้ strings.HasPrefix แทน direct indexing
	const prefix = "ORD-"
	if !strings.HasPrefix(s, prefix) {
		return OrderID{}, fmt.Errorf("invalid format: must start with %q, got %q", prefix, s)
	}

	// FIX #5: ตรวจ number part
	number := s[len(prefix):]
	if len(number) == 0 {
		return OrderID{}, fmt.Errorf("invalid format: missing number after prefix")
	}

	// ตรวจว่า number มีเฉพาะตัวเลขและตัวอักษร ASCII (ป้องกัน injection)
	for i, r := range number {
		if !isValidIDRune(r) {
			return OrderID{}, fmt.Errorf("invalid character %q at position %d", r, i+len(prefix))
		}
	}

	// ใช้ ASCII upper ไม่ใช่ strings.ToUpper
	// เพื่อ predictable behavior กับ ASCII-only input
	return OrderID{value: "ORD-" + strings.ToUpper(number)}, nil
}

// isValidIDRune ตรวจว่า rune เป็น valid character สำหรับ OrderID
// รับเฉพาะ: A-Z, a-z, 0-9, hyphen (-)
func isValidIDRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '-'
}

// ============================================================
// ADDITIONAL PARSERS สำหรับ fuzz targets เพิ่มเติม
// ============================================================

// ParseAmount — parse string เป็น amount ใน cents
// Format: "1234" หรือ "1234.56" (จะ round เป็น cents)
//
// Fuzzing จะหา:
//   - Negative numbers
//   - Numbers ที่ overflow int64
//   - Non-numeric strings
//   - Leading zeros
//   - Multiple decimal points
func ParseAmount(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("amount cannot be empty")
	}

	if len(s) > 20 {
		return 0, fmt.Errorf("amount string too long")
	}

	if !utf8.ValidString(s) {
		return 0, fmt.Errorf("invalid UTF-8 in amount")
	}

	// ตรวจว่ามีแค่ตัวเลขและ decimal point
	dotCount := 0
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			// OK
		case r == '.' && i > 0:
			dotCount++
			if dotCount > 1 {
				return 0, fmt.Errorf("invalid amount: multiple decimal points")
			}
		case r == '-' && i == 0:
			return 0, fmt.Errorf("amount cannot be negative")
		default:
			return 0, fmt.Errorf("invalid character %q in amount", r)
		}
	}

	// Parse integer part
	var intPart, fracPart int64
	dotIdx := strings.Index(s, ".")
	if dotIdx == -1 {
		// ไม่มี decimal point — ทั้งหมดเป็น integer
		intPart = parseDigits(s)
		return intPart * 100, nil
	}

	intPart = parseDigits(s[:dotIdx])
	fracStr := s[dotIdx+1:]

	// normalize fractional part เป็น 2 digits (cents)
	switch len(fracStr) {
	case 0:
		fracPart = 0
	case 1:
		fracPart = parseDigits(fracStr) * 10
	case 2:
		fracPart = parseDigits(fracStr)
	default:
		// truncate ที่ 2 decimal places
		fracPart = parseDigits(fracStr[:2])
	}

	// ตรวจ overflow
	const maxCents int64 = 1<<62 - 1
	cents := intPart*100 + fracPart
	if cents < 0 || intPart < 0 { // overflow check
		return 0, fmt.Errorf("amount overflow")
	}
	if cents > maxCents {
		return 0, fmt.Errorf("amount exceeds maximum: %d", maxCents)
	}

	return cents, nil
}

// parseDigits แปลง string of digits เป็น int64
// assumes s มีแค่ digits 0-9 (ตรวจแล้วจากผู้เรียก)
func parseDigits(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	for _, r := range s {
		n = n*10 + int64(r-'0')
	}
	return n
}
