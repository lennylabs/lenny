// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
)

func newCounter(t *testing.T) *slotcounter.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return slotcounter.New(cl)
}

// spec: §5.2 — Reserve atomically increments the slot count, returning
// the new count, and rejects past maxConcurrent.
func TestReserveIncrementsUntilCap(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	for i := int32(1); i <= 3; i++ {
		got, err := c.Reserve(ctx, "pod-a", 3)
		if err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
		if got != i {
			t.Errorf("Reserve %d returned count %d, want %d", i, got, i)
		}
	}
	// 4th reserve must fail.
	if _, err := c.Reserve(ctx, "pod-a", 3); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("4th Reserve = %v, want ErrSlotsExhausted", err)
	}
}

// spec: §5.2 — atomic CAS prevents over-commit under racing reservers.
// 50 goroutines try to reserve on a pod whose maxConcurrent is 10; only
// 10 must succeed.
func TestReserveIsAtomicUnderRace(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	const n = 50
	const cap = int32(10)
	var wg sync.WaitGroup
	successes := int64(0)
	exhausted := int64(0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Reserve(ctx, "pod-r", cap); err == nil {
				atomic.AddInt64(&successes, 1)
			} else if errors.Is(err, slotcounter.ErrSlotsExhausted) {
				atomic.AddInt64(&exhausted, 1)
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&successes) != int64(cap) {
		t.Errorf("got %d successful reserves, want exactly %d (cap)", successes, cap)
	}
	if atomic.LoadInt64(&exhausted) != int64(n-int(cap)) {
		t.Errorf("got %d exhausted, want %d", exhausted, n-int(cap))
	}
}

// spec: §5.2 — Release decrements; a release on a zero counter clamps
// at zero (double-release-safe).
func TestReleaseDecrementsAndClampsAtZero(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "pod-d", 5); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	got, err := c.Release(ctx, "pod-d")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got != 0 {
		t.Errorf("Release returned %d, want 0", got)
	}
	// Double-release.
	got, err = c.Release(ctx, "pod-d")
	if err != nil {
		t.Fatalf("double Release: %v", err)
	}
	if got != 0 {
		t.Errorf("double Release returned %d, want 0 (clamped)", got)
	}
}

// Different pods have independent counters.
func TestReserveScopesPerPod(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "pod-x", 1); err != nil {
		t.Fatalf("Reserve pod-x: %v", err)
	}
	if _, err := c.Reserve(ctx, "pod-x", 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("second Reserve on pod-x = %v, want ErrSlotsExhausted", err)
	}
	if _, err := c.Reserve(ctx, "pod-y", 1); err != nil {
		t.Errorf("Reserve pod-y must succeed independently of pod-x: %v", err)
	}
}

func TestResetClearsCounter(t *testing.T) {
	c := newCounter(t)
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "pod-z", 2); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := c.Reset(ctx, "pod-z"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	// A fresh Reserve after Reset starts from zero.
	got, err := c.Reserve(ctx, "pod-z", 2)
	if err != nil {
		t.Fatalf("Reserve after Reset: %v", err)
	}
	if got != 1 {
		t.Errorf("Reserve after Reset returned %d, want 1", got)
	}
}
