// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCallSucceedsBelowCap(t *testing.T) {
	s := New(Config{MaxConcurrent: 2})
	if err := s.Call(context.Background()); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if s.TotalCalls() != 1 {
		t.Errorf("TotalCalls=%d want 1", s.TotalCalls())
	}
}

func TestCallRejectsAtCap(t *testing.T) {
	s := New(Config{MaxConcurrent: 1, ResponseLatency: 100 * time.Millisecond})
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.Call(context.Background())
		}()
	}
	wg.Wait()
	gotCap := 0
	for _, e := range results {
		if errors.Is(e, ErrAtCapacity) {
			gotCap++
		}
	}
	if gotCap != 1 {
		t.Errorf("expected exactly 1 cap rejection, got %d", gotCap)
	}
}

func TestErrorRateInjects(t *testing.T) {
	s := New(Config{ErrorRate: 0.5})
	// 100 calls with ErrorRate=0.5 should produce roughly 50 errors.
	for i := 0; i < 100; i++ {
		_ = s.Call(context.Background())
	}
	got := s.TotalErrors()
	if got < 30 || got > 70 {
		t.Errorf("TotalErrors=%d, want roughly 50 (deterministic counter sees rate ~0.5)", got)
	}
}

func TestRespectsContextCancel(t *testing.T) {
	s := New(Config{ResponseLatency: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := s.Call(ctx)
	if err == nil {
		t.Fatal("expected context.DeadlineExceeded")
	}
}
