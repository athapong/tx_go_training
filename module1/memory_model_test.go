package main

import (
	"sync"
	"testing"
)

// Benchmark: Atomic vs Mutex สำหรับ counter
// รัน: go test -bench=. -benchmem -count=3

func BenchmarkAtomicIncrement(b *testing.B) {
	c := &AtomicCounter{}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Increment()
		}
	})
}

func BenchmarkMutexIncrement(b *testing.B) {
	c := &MutexCounter{}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Increment()
		}
	})
}

// Test: ตรวจสอบว่า atomic counter ถูกต้อง
func TestAtomicCounterConcurrent(t *testing.T) {
	const goroutines = 1000
	const iterations = 100

	c := &AtomicCounter{}
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.Increment()
			}
		}()
	}

	wg.Wait()

	expected := int64(goroutines * iterations)
	if c.Value() != expected {
		t.Errorf("Expected %d, got %d", expected, c.Value())
	}
}

func TestMutexCounterConcurrent(t *testing.T) {
	const goroutines = 1000
	const iterations = 100

	c := &MutexCounter{}
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.Increment()
			}
		}()
	}

	wg.Wait()

	expected := goroutines * iterations
	if c.Value() != expected {
		t.Errorf("Expected %d, got %d", expected, c.Value())
	}
}
