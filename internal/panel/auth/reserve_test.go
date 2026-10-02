package auth

import (
	"sync"
	"testing"
	"time"
)

// Reserve counts the attempt as it checks: of any number of callers at once only maxFails
// get through, however slow they are afterwards.
func TestReserveLetsOnlyTheAllowanceThrough(t *testing.T) {
	l := NewLimiter(5, time.Minute, 15*time.Minute, time.Hour)
	now := time.Unix(1_000_000, 0)
	var mu sync.Mutex
	through, blocking := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, blocked := l.Reserve("k", now)
			mu.Lock()
			defer mu.Unlock()
			if ok {
				through++
			}
			if ok && blocked {
				blocking++
			}
		}()
	}
	wg.Wait()
	if through != 5 || blocking != 1 {
		t.Fatalf("%d got through (want 5), %d of them used up the allowance (want 1)", through, blocking)
	}
	if ok, wait, _ := l.Reserve("k", now); ok || wait != 15*time.Minute {
		t.Fatalf("after the allowance: ok=%v wait=%v", ok, wait)
	}
	// A success hands the attempts back.
	l.Reset("k")
	if ok, _, _ := l.Reserve("k", now); !ok {
		t.Fatal("still blocked after Reset")
	}
}

func TestReserveEscalatesLikeFail(t *testing.T) {
	l := NewLimiter(2, time.Minute, 10*time.Second, time.Hour)
	now := time.Unix(1_000_000, 0)
	l.Reserve("k", now)
	if ok, _, blocked := l.Reserve("k", now); !ok || !blocked {
		t.Fatalf("second attempt: ok=%v blocked=%v, want it through and blocking", ok, blocked)
	}
	now = now.Add(11 * time.Second)
	l.Reserve("k", now)
	l.Reserve("k", now)
	if _, wait, _ := l.Reserve("k", now); wait != 20*time.Second {
		t.Fatalf("second block = %v, want doubled 20s", wait)
	}
}
