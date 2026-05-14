// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/runtime/upgrade/state"
)

func TestAllAndTerminal(t *testing.T) {
	if len(state.All()) != 6 {
		t.Errorf("All() expected 6 states, got %d", len(state.All()))
	}
	if !state.IsTerminal(state.Complete) {
		t.Errorf("Complete must be terminal")
	}
	for _, s := range []state.State{state.Pending, state.Expanding, state.Draining, state.Contracting, state.Paused} {
		if state.IsTerminal(s) {
			t.Errorf("%q must not be terminal", s)
		}
	}
}

func TestIsValidCanonicalTransitions(t *testing.T) {
	for _, tr := range state.ValidTransitions() {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil", tr.From, tr.To, err)
			}
		})
	}
}

func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	illegal := []state.Transition{
		{state.Pending, state.Draining},   // skip Expanding
		{state.Pending, state.Complete},   // skip everything
		{state.Complete, state.Expanding}, // terminal is a sink
		{state.Complete, state.Paused},    // terminal cannot be paused
		{state.Expanding, state.Pending},  // no reverse edge
		{state.Draining, state.Expanding}, // no reverse edge
		{state.Paused, state.Expanding},   // Resume restores; not a transition
	}
	for _, tr := range illegal {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			err := state.IsValid(tr.From, tr.To)
			if err == nil {
				t.Errorf("IsValid(%q, %q) returned nil, expected error", tr.From, tr.To)
			}
			var ite *state.InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Errorf("expected *InvalidTransitionError, got %T", err)
			}
		})
	}
}

func TestRecordHappyPath(t *testing.T) {
	r := state.NewRecord()
	if r.Current() != state.Pending {
		t.Fatalf("initial state: want Pending, got %q", r.Current())
	}
	for _, target := range []state.State{state.Expanding, state.Draining, state.Contracting, state.Complete} {
		if err := r.Transition(target); err != nil {
			t.Fatalf("Transition(%q): %v", target, err)
		}
		if r.Current() != target {
			t.Fatalf("Current after Transition(%q): want %q, got %q", target, target, r.Current())
		}
	}
}

func TestRecordPauseAndResumePreservesPrior(t *testing.T) {
	r := state.NewRecord()
	for _, target := range []state.State{state.Expanding, state.Draining} {
		if err := r.Transition(target); err != nil {
			t.Fatalf("setup Transition(%q): %v", target, err)
		}
	}
	if err := r.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if r.Current() != state.Paused {
		t.Fatalf("after Pause, Current = %q, want Paused", r.Current())
	}
	if r.PriorPaused() != state.Draining {
		t.Fatalf("PriorPaused: want Draining, got %q", r.PriorPaused())
	}
	if err := r.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if r.Current() != state.Draining {
		t.Fatalf("after Resume, Current = %q, want Draining", r.Current())
	}
	if r.PriorPaused() != "" {
		t.Fatalf("PriorPaused after Resume: want empty, got %q", r.PriorPaused())
	}
}

func TestRecordPauseRejectsTerminal(t *testing.T) {
	r := state.NewRecord()
	for _, target := range []state.State{state.Expanding, state.Draining, state.Contracting, state.Complete} {
		if err := r.Transition(target); err != nil {
			t.Fatalf("setup Transition(%q): %v", target, err)
		}
	}
	if err := r.Pause(); !errors.Is(err, state.ErrCannotPauseTerminal) {
		t.Errorf("Pause on Complete: want ErrCannotPauseTerminal, got %v", err)
	}
}

func TestRecordDoubleResumeIsError(t *testing.T) {
	r := state.NewRecord()
	if err := r.Resume(); !errors.Is(err, state.ErrNotPaused) {
		t.Errorf("Resume on non-paused record: want ErrNotPaused, got %v", err)
	}
}

func TestRecordDoublePauseIsError(t *testing.T) {
	r := state.NewRecord()
	if err := r.Pause(); err != nil {
		t.Fatalf("first Pause: %v", err)
	}
	if err := r.Pause(); !errors.Is(err, state.ErrAlreadyPaused) {
		t.Errorf("second Pause: want ErrAlreadyPaused, got %v", err)
	}
}

func TestRecordTransitionRejectsExplicitPaused(t *testing.T) {
	r := state.NewRecord()
	if err := r.Transition(state.Paused); err == nil {
		t.Errorf("Transition(Paused) should be rejected; use Pause()")
	}
}

func TestRecordTransitionWhilePausedFails(t *testing.T) {
	r := state.NewRecord()
	if err := r.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := r.Transition(state.Expanding); !errors.Is(err, state.ErrCurrentlyPaused) {
		t.Errorf("Transition while paused: want ErrCurrentlyPaused, got %v", err)
	}
}

func TestNewRecordAtRehydratesPausedRecord(t *testing.T) {
	r, err := state.NewRecordAt(state.Paused, state.Expanding)
	if err != nil {
		t.Fatalf("NewRecordAt: %v", err)
	}
	if r.Current() != state.Paused || r.PriorPaused() != state.Expanding {
		t.Fatalf("rehydrated record: Current=%q PriorPaused=%q", r.Current(), r.PriorPaused())
	}
	if err := r.Resume(); err != nil {
		t.Fatalf("Resume on rehydrated record: %v", err)
	}
	if r.Current() != state.Expanding {
		t.Errorf("after Resume: want Expanding, got %q", r.Current())
	}
}

func TestNewRecordAtRejectsBadInputs(t *testing.T) {
	cases := []struct {
		name        string
		current     state.State
		priorPaused state.State
	}{
		{"unknown current", "bogus", ""},
		{"paused without prior", state.Paused, ""},
		{"paused with bad prior", state.Paused, state.Complete},
		{"non-paused with prior set", state.Expanding, state.Pending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := state.NewRecordAt(c.current, c.priorPaused); err == nil {
				t.Errorf("expected error for (%q, %q)", c.current, c.priorPaused)
			}
		})
	}
}

// Sanity check that concurrent Pause/Resume/Transition do not corrupt
// the state.
func TestRecordConcurrentAccessSafe(t *testing.T) {
	r := state.NewRecord()
	if err := r.Transition(state.Expanding); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Pause()
			_ = r.Resume()
		}()
	}
	wg.Wait()
	// After many Pause/Resume pairs, Current must be either Expanding
	// (if the last operation was Resume) or Paused (if Pause raced
	// past the final Resume). In either case PriorPaused is consistent.
	cur := r.Current()
	if cur != state.Expanding && cur != state.Paused {
		t.Errorf("final state corrupt: %q", cur)
	}
}
