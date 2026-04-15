package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestNilLimiterAlwaysAllows(t *testing.T) {
	var l *Limiter
	for i := 0; i < 100; i++ {
		if !l.Allow("x") {
			t.Fatal("nil limiter must allow")
		}
	}
}

func TestBucketRefills(t *testing.T) {
	l := New(10, 1, time.Minute) // 10 tokens/sec, burst 1
	if !l.Allow("k") {
		t.Fatal("first should pass")
	}
	if l.Allow("k") {
		t.Fatal("second immediately should fail (burst exhausted)")
	}
	time.Sleep(150 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("after refill should pass")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	l := New(1, 1, time.Minute)
	if !l.Allow("a") {
		t.Fatal("a first should pass")
	}
	if !l.Allow("b") {
		t.Fatal("b first should pass even if a is exhausted")
	}
	if l.Allow("a") {
		t.Fatal("a second should fail")
	}
}

func TestConcurrentAccess(t *testing.T) {
	l := New(1000, 1000, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = l.Allow("shared")
			}
		}()
	}
	wg.Wait()
}
