// SPDX-License-Identifier: MIT

// Package state defines the RuntimeUpgrade state machine per spec §10.5.
//
// The machine drives a tracked, pauseable rollout of a new runtime image
// for a single pool:
//
//	Pending → Expanding → Draining → Contracting → Complete
//	                                ↘ Paused (from any non-terminal state)
//	                                ↗
//
// The state enum and ValidTransitions() form the authoritative contract.
// Pause is modeled as a side-state that captures the prior state so
// Resume restores it exactly. Once Complete is reached, no further
// transitions are legal — Complete is the only terminal state.
package state

import (
	"fmt"
	"sync"
)

// State is the RuntimeUpgrade phase, written to RuntimeUpgrade.status.phase.
type State string

const (
	// Pending: upgrade registered, not yet started. Old and new
	// SandboxTemplate CRDs exist; new pool minWarm = 0.
	Pending State = "pending"

	// Expanding: new pool ramping up to target minWarm; new sessions
	// route to the new pool (or canary-split when canaryPercent is set).
	Expanding State = "expanding"

	// Draining: old pool accepts no new sessions; existing sessions on
	// old pods run to completion or are checkpoint-terminated on
	// drainTimeoutSeconds.
	Draining State = "draining"

	// Contracting: old SandboxTemplate CRD and pool record being deleted.
	Contracting State = "contracting"

	// Complete: upgrade finished, only the new pool remains. Terminal.
	Complete State = "complete"

	// Paused: upgrade halted by operator. The prior state is captured
	// on the RuntimeUpgrade record so Resume returns to it. Side-state;
	// not part of the linear progression.
	Paused State = "paused"
)

// All returns every canonical state in spec order.
func All() []State {
	return []State{Pending, Expanding, Draining, Contracting, Complete, Paused}
}

// Terminal returns the terminal states. Only Complete is terminal —
// Paused is a holding state that can be resumed at any time.
func Terminal() []State {
	return []State{Complete}
}

// IsTerminal reports whether s is a terminal state.
func IsTerminal(s State) bool {
	return s == Complete
}

// Transition is one legal edge in the state machine.
type Transition struct {
	From State
	To   State
}

// ValidTransitions per spec §10.5. Pause edges from every non-terminal
// state are included; Resume edges are not — Resume restores the prior
// state via Record.Resume, not via a generic transition function.
func ValidTransitions() []Transition {
	return []Transition{
		{Pending, Expanding},
		{Expanding, Draining},
		{Draining, Contracting},
		{Contracting, Complete},

		// Pause edges from every non-terminal state.
		{Pending, Paused},
		{Expanding, Paused},
		{Draining, Paused},
		{Contracting, Paused},
	}
}

// InvalidTransitionError is returned by IsValid for any edge not present
// in ValidTransitions(). Callers can errors.As to retrieve From/To.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("runtime-upgrade: %q → %q is not a valid transition per spec §10.5", e.From, e.To)
}

var validSet = func() map[Transition]struct{} {
	m := make(map[Transition]struct{}, len(ValidTransitions()))
	for _, t := range ValidTransitions() {
		m[t] = struct{}{}
	}
	return m
}()

// IsValid reports whether from → to is a legal transition. Returns nil
// on a legal edge and *InvalidTransitionError otherwise. IsValid does
// not encode pause-and-resume semantics — Record encapsulates those.
func IsValid(from, to State) error {
	if _, ok := validSet[Transition{From: from, To: to}]; ok {
		return nil
	}
	return &InvalidTransitionError{From: from, To: to}
}

// Record is a goroutine-safe RuntimeUpgrade state container. It enforces
// the §10.5 transitions plus the pause/resume invariant: Pause from a
// non-terminal state captures the prior state; Resume restores it.
// Two Pause calls in a row are a no-op (the first captures, the second
// returns ErrAlreadyPaused so callers can detect concurrent pause
// attempts). Resume from a non-Paused state returns ErrNotPaused.
type Record struct {
	mu          sync.Mutex
	current     State
	priorPaused State // populated only while current == Paused
}

// NewRecord returns a Record initialised at Pending. Callers seed the
// initial state explicitly via NewRecordAt for non-Pending recoveries.
func NewRecord() *Record {
	return &Record{current: Pending}
}

// NewRecordAt returns a Record at the supplied state. Used to rehydrate
// a Record from a persisted RuntimeUpgrade CRD on controller startup.
// The state must be a valid State; if it is Paused, priorPaused
// captures the state to restore on Resume.
func NewRecordAt(s State, priorIfPaused State) (*Record, error) {
	if !isKnown(s) {
		return nil, fmt.Errorf("runtime-upgrade: %q is not a known state", s)
	}
	if s == Paused {
		if !isKnown(priorIfPaused) || priorIfPaused == Paused || priorIfPaused == Complete {
			return nil, fmt.Errorf("runtime-upgrade: priorIfPaused %q is not a valid resumable state", priorIfPaused)
		}
		return &Record{current: Paused, priorPaused: priorIfPaused}, nil
	}
	if priorIfPaused != "" {
		return nil, fmt.Errorf("runtime-upgrade: priorIfPaused must be empty unless current is Paused")
	}
	return &Record{current: s}, nil
}

// Current returns the current state.
func (r *Record) Current() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// PriorPaused returns the state to be restored on Resume. Returns ""
// when the record is not paused.
func (r *Record) PriorPaused() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != Paused {
		return ""
	}
	return r.priorPaused
}

// Transition advances the record from current → to. Returns
// *InvalidTransitionError when the edge is not in ValidTransitions().
// Transition cannot move the record into or out of Paused — use Pause
// and Resume for that.
func (r *Record) Transition(to State) error {
	if to == Paused {
		return fmt.Errorf("runtime-upgrade: use Pause to enter the Paused state")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == Paused {
		return ErrCurrentlyPaused
	}
	if err := IsValid(r.current, to); err != nil {
		return err
	}
	r.current = to
	return nil
}

// Pause moves the record from a non-terminal state to Paused, capturing
// the prior state so Resume can restore it. Returns ErrAlreadyPaused if
// already paused, ErrCannotPauseTerminal if the record is Complete.
func (r *Record) Pause() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == Paused {
		return ErrAlreadyPaused
	}
	if r.current == Complete {
		return ErrCannotPauseTerminal
	}
	if err := IsValid(r.current, Paused); err != nil {
		return err
	}
	r.priorPaused = r.current
	r.current = Paused
	return nil
}

// Resume restores the state captured by the prior Pause call. Returns
// ErrNotPaused if the record is not currently paused.
func (r *Record) Resume() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != Paused {
		return ErrNotPaused
	}
	r.current = r.priorPaused
	r.priorPaused = ""
	return nil
}

// Sentinel errors for the pause/resume invariants.
var (
	ErrAlreadyPaused       = fmt.Errorf("runtime-upgrade: already paused")
	ErrNotPaused           = fmt.Errorf("runtime-upgrade: not paused")
	ErrCannotPauseTerminal = fmt.Errorf("runtime-upgrade: cannot pause a Complete record")
	ErrCurrentlyPaused     = fmt.Errorf("runtime-upgrade: cannot transition while paused")
)

func isKnown(s State) bool {
	for _, x := range All() {
		if s == x {
			return true
		}
	}
	return false
}
