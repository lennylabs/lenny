// SPDX-License-Identifier: MIT

package sessionbudget

import (
	"sync"
	"testing"
)

// recordingTerminator captures TerminateSession calls for assertions.
type recordingTerminator struct {
	mu    sync.Mutex
	calls []termCall
}

type termCall struct {
	sessionID string
	reason    string
}

func (t *recordingTerminator) TerminateSession(sessionID, reason string) {
	t.mu.Lock()
	t.calls = append(t.calls, termCall{sessionID, reason})
	t.mu.Unlock()
}

func (t *recordingTerminator) snapshot() []termCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]termCall, len(t.calls))
	copy(out, t.calls)
	return out
}

// spec: §11.2 line 44 — a session whose cumulative proxy consumption
// reaches its token budget is terminated immediately, and a later
// request is rejected by the pre-flight gate.
func TestRecordTerminatesOnBudgetExhaustion_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil)

	// Under budget: no termination, request allowed.
	e.Record("acme", "s1", 1000, 400)
	if !e.Allow("s1") {
		t.Fatalf("session under budget should be allowed")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("no termination expected under budget, got %v", got)
	}

	// Reaching the budget exhausts it: terminate once, deny further
	// requests.
	e.Record("acme", "s1", 1000, 700) // cumulative 1100 >= 1000
	if e.Allow("s1") {
		t.Fatalf("exhausted session must be denied by the §8.10 gate")
	}
	got := term.snapshot()
	if len(got) != 1 {
		t.Fatalf("want exactly one termination, got %v", got)
	}
	if got[0].sessionID != "s1" || got[0].reason != ReasonBudgetExhausted {
		t.Fatalf("termination = %+v, want {s1, %s}", got[0], ReasonBudgetExhausted)
	}
}

// Exhaustion fires at the boundary (consumed == budget), matching §8.10
// line 1108 "token budget is exhausted".
func TestRecordExhaustsAtExactBoundary_spec_8_10(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil)
	e.Record("acme", "s1", 500, 500) // exactly at budget
	if e.Allow("s1") {
		t.Fatalf("a session at exactly its budget is exhausted and must be denied")
	}
	if got := term.snapshot(); len(got) != 1 {
		t.Fatalf("want one termination at the boundary, got %v", got)
	}
}

// Termination and the metric hook fire exactly once even when more
// over-budget usage lands after exhaustion (idempotent).
func TestRecordTerminatesOnce_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	var hookCalls int
	e := New(term, func(_, _ string, _, _ int64) { hookCalls++ })
	e.Record("acme", "s1", 100, 150) // exhaust
	e.Record("acme", "s1", 100, 150) // already exhausted
	e.Record("acme", "s1", 100, 150)
	if got := term.snapshot(); len(got) != 1 {
		t.Fatalf("termination must fire once, got %v", got)
	}
	if hookCalls != 1 {
		t.Fatalf("onExceeded must fire once, got %d", hookCalls)
	}
}

// A non-positive budget disables enforcement: the running total is still
// tracked, and a later positive budget resolution sees the accumulated
// consumption.
func TestRecordZeroBudgetDisablesUntilResolved_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil)
	e.Record("acme", "s1", 0, 5000) // unbounded so far
	if !e.Allow("s1") {
		t.Fatalf("a session with no budget set must be allowed")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("no termination without a budget, got %v", got)
	}
	// A budget appears (e.g. a delegation lease resolves) below the
	// already-accumulated total: the next record exhausts immediately.
	e.Record("acme", "s1", 1000, 1)
	if e.Allow("s1") {
		t.Fatalf("budget resolved below accumulated usage must exhaust")
	}
	if got := term.snapshot(); len(got) != 1 {
		t.Fatalf("want one termination after budget resolves, got %v", got)
	}
}

// Unknown and empty session ids are allowed; the gate only constrains
// attributable proxy sessions.
func TestAllowUnknownAndEmpty_spec_11_2(t *testing.T) {
	e := New(&recordingTerminator{}, nil)
	if !e.Allow("never-seen") {
		t.Fatalf("an unseen session must be allowed (first request)")
	}
	if !e.Allow("") {
		t.Fatalf("an empty session id must be allowed")
	}
	// Record/Forget with empty id are no-ops and must not panic.
	e.Record("acme", "", 10, 100)
	e.Forget("")
}

// Forget evicts a session's accounting so the map does not grow without
// bound; a re-seen session id starts fresh.
func TestForgetEvictsAccounting_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil)
	e.Record("acme", "s1", 100, 200) // exhaust
	if e.Allow("s1") {
		t.Fatalf("precondition: s1 should be exhausted")
	}
	e.Forget("s1")
	if !e.Allow("s1") {
		t.Fatalf("after Forget the session id is unknown and allowed")
	}
	// A fresh budget cycle on the re-seen id starts from zero consumption.
	e.Record("acme", "s1", 1000, 100)
	if !e.Allow("s1") {
		t.Fatalf("re-seen session under its fresh budget should be allowed")
	}
}

// The enforcer is safe under concurrent Record/Allow/Forget.
func TestConcurrentAccess_spec_11_2(t *testing.T) {
	e := New(&recordingTerminator{}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "s"
			e.Record("acme", id, 10_000, int64(n))
			_ = e.Allow(id)
			if n%7 == 0 {
				e.Forget(id)
			}
		}(i)
	}
	wg.Wait()
}
