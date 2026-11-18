// SPDX-License-Identifier: MIT

package subsystem_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam/subsystem"
)

// The tests of Admit carry no specification annotation of their own: Admit
// is the admission half of Do, and the tests of Do in subsystem_test.go
// verify the §4.1 isolation behavior that both share.

func TestAdmitHoldsALimiterSlotUntilDone(t *testing.T) {
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 3},
		Limiter: &subsystem.Limiter{MaxConcurrent: 1},
	}
	done, err := s.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if s.InFlight() != 1 {
		t.Fatalf("InFlight() = %d after Admit, want 1", s.InFlight())
	}
	done(nil)
	if s.InFlight() != 0 {
		t.Fatalf("InFlight() = %d after done, want 0", s.InFlight())
	}
	if s.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q, want %q", s.State(), subsystem.StateClosed)
	}
}

func TestAdmitRecordsAFailedCallAgainstTheBreaker(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour, Now: clk.now},
	}
	done, err := s.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	done(errors.New("upstream down"))
	if s.State() != subsystem.StateOpen {
		t.Fatalf("State() = %q after a failed call, want %q", s.State(), subsystem.StateOpen)
	}
	if _, err := s.Admit(context.Background()); !errors.Is(err, subsystem.ErrCircuitOpen) {
		t.Fatalf("Admit under an open breaker = %v, want ErrCircuitOpen", err)
	}
}

func TestAdmitRefusesOnACancelledContextWithoutTrippingTheBreaker(t *testing.T) {
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 1},
		Limiter: &subsystem.Limiter{MaxConcurrent: 1},
	}
	held, err := s.Admit(context.Background())
	if err != nil {
		t.Fatalf("first Admit: %v", err)
	}
	defer held(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done, err := s.Admit(ctx)
	if !errors.Is(err, subsystem.ErrLimiterStopped) {
		t.Fatalf("Admit on a full limiter with a cancelled context = %v, want ErrLimiterStopped", err)
	}
	if done != nil {
		t.Fatal("Admit returned a done function on refusal")
	}
	if s.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q after a limiter refusal, want %q", s.State(), subsystem.StateClosed)
	}
}

func TestAdmitDoneIsIdempotent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 2, Cooldown: time.Hour, Now: clk.now},
		Limiter: &subsystem.Limiter{MaxConcurrent: 2},
	}
	done, err := s.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	failure := errors.New("upstream down")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done(failure)
		}()
	}
	wg.Wait()
	if s.InFlight() != 0 {
		t.Fatalf("InFlight() = %d, want 0", s.InFlight())
	}
	// One failure against a threshold of two leaves the breaker closed; a
	// second recording would have opened it.
	if s.State() != subsystem.StateClosed {
		t.Fatalf("State() = %q after repeated done calls, want %q", s.State(), subsystem.StateClosed)
	}
}

func TestAdmitOnAZeroSubsystemAdmitsEveryCall(t *testing.T) {
	var s subsystem.Subsystem
	done, err := s.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit on a zero Subsystem: %v", err)
	}
	done(errors.New("ignored"))
}
