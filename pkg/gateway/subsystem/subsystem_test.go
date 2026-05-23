// SPDX-License-Identifier: MIT

package subsystem_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
)

// spec: §4.1 — Subsystem.Do gates fn through both breaker and
// limiter; a healthy subsystem invokes fn and records success.
func TestSubsystemDoHappyPath(t *testing.T) {
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 3},
		Limiter: &subsystem.Limiter{MaxConcurrent: 2},
	}
	called := 0
	err := s.Do(context.Background(), func(ctx context.Context) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if called != 1 {
		t.Fatalf("fn called %d times, want 1", called)
	}
	if s.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q", s.State(), subsystem.StateClosed)
	}
	if s.InFlight() != 0 {
		t.Fatalf("InFlight() = %d, want 0", s.InFlight())
	}
}

// spec: §4.1 — when the breaker is open, Do returns ErrCircuitOpen
// without invoking fn.
func TestSubsystemDoCircuitOpenRejects(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour, Now: clk.now},
		Limiter: &subsystem.Limiter{MaxConcurrent: 2},
	}
	// Trip the breaker via a failing call.
	failErr := errors.New("upstream down")
	if err := s.Do(context.Background(), func(ctx context.Context) error {
		return failErr
	}); !errors.Is(err, failErr) {
		t.Fatalf("first Do = %v, want %v", err, failErr)
	}
	if s.State() != subsystem.StateOpen {
		t.Fatalf("State() = %q, want %q after failure", s.State(), subsystem.StateOpen)
	}
	// Next call must short-circuit.
	called := 0
	err := s.Do(context.Background(), func(ctx context.Context) error {
		called++
		return nil
	})
	if !errors.Is(err, subsystem.ErrCircuitOpen) {
		t.Fatalf("Do under open breaker = %v, want ErrCircuitOpen", err)
	}
	if called != 0 {
		t.Fatalf("fn called %d times under open breaker, want 0", called)
	}
}

// spec: §4.1 — a saturated limiter under a cancelled context
// returns ErrLimiterStopped without invoking fn and without
// burning a breaker failure.
func TestSubsystemDoLimiterCancellationDoesNotTripBreaker(t *testing.T) {
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 1},
		Limiter: &subsystem.Limiter{MaxConcurrent: 1},
	}
	// Take the only slot.
	r, err := s.Limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("preload Acquire: %v", err)
	}
	defer r()

	// Issue Do under a context that cancels before the slot frees.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Do(ctx, func(ctx context.Context) error {
			t.Error("fn should not run when limiter is saturated and ctx cancels")
			return nil
		})
	}()
	// Wait until the call is queued, then cancel.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.Limiter.QueueDepth() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, subsystem.ErrLimiterStopped) {
			t.Fatalf("Do returned %v, want ErrLimiterStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Do did not return after cancellation")
	}

	// The breaker must stay closed — the limiter rejection was
	// not a downstream failure.
	if s.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q after limiter cancellation", s.State(), subsystem.StateClosed)
	}
}

// spec: §4.1 — a Subsystem with no breaker and no limiter is still
// callable; Do invokes fn directly.
func TestSubsystemDoZeroValue(t *testing.T) {
	var s subsystem.Subsystem
	called := 0
	err := s.Do(context.Background(), func(ctx context.Context) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("Do on zero-value Subsystem: %v", err)
	}
	if called != 1 {
		t.Fatalf("fn called %d times, want 1", called)
	}
}

// spec: §4.1 — repeated failures through Do trip the breaker.
func TestSubsystemDoAccumulatesFailures(t *testing.T) {
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 3},
	}
	failErr := errors.New("transient failure")
	for i := 0; i < 3; i++ {
		_ = s.Do(context.Background(), func(ctx context.Context) error { return failErr })
	}
	if s.State() != subsystem.StateOpen {
		t.Fatalf("State() = %q after 3 failures, want %q", s.State(), subsystem.StateOpen)
	}
}
