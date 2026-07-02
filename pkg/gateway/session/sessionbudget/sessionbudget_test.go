// SPDX-License-Identifier: MIT

package sessionbudget

import (
	"context"
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

// bg is the two-context pair the proxy record boundary threads. Tests
// that do not exercise cancellation pass background for both.
func bg() (context.Context, context.Context) { return context.Background(), context.Background() }

// terminalSeam is a seam that always returns Terminal, the non-extendable
// posture (nil seam behaves identically). recordingSeam captures the
// contexts and arguments each call receives.
type recordingSeam struct {
	mu       sync.Mutex
	outcome  Outcome
	calls    int
	lastReq  context.Context
	lastWait context.Context
	lastSess string
	// applyRaise, when the seam returns Granted, mirrors the production
	// seam that raises the budget through the enforcer before returning, so
	// the in-path Granted branch leaves the session admitted.
	e          *Enforcer
	raiseDelta int64
}

func (s *recordingSeam) fn(reqCtx, waitCtx context.Context, _ /*tenantID*/, sessionID string, _, _ int64) Outcome {
	s.mu.Lock()
	s.calls++
	s.lastReq = reqCtx
	s.lastWait = waitCtx
	s.lastSess = sessionID
	out := s.outcome
	e := s.e
	delta := s.raiseDelta
	s.mu.Unlock()
	if out == Granted && e != nil {
		// The production seam raises the enforcer budget (clearing the deny /
		// exhausted flags) before it returns Granted; mirror that here.
		e.RaiseBudget(sessionID, delta)
	}
	return out
}

func (s *recordingSeam) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// spec: §11.2 line 44, §8.6 line 629 — with no extension seam wired a
// session whose cumulative proxy consumption reaches its token budget is
// denied and terminated immediately, and a later request is rejected by
// the pre-flight gate.
func TestRecordTerminatesOnBudgetExhaustion_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil, nil)
	req, wait := bg()

	// Under budget: no termination, request allowed. A call that does not
	// cross the boundary reports not-exhausted with the inert Granted outcome.
	if exhausted, outcome := e.Record(req, wait, "acme", "s1", 1000, 400); exhausted || outcome != Granted {
		t.Fatalf("under-budget Record = (%v, %v), want (false, GRANTED)", exhausted, outcome)
	}
	if !e.Allow("s1") {
		t.Fatalf("session under budget should be allowed")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("no termination expected under budget, got %v", got)
	}

	// Reaching the budget exhausts it with no seam wired: the returned outcome
	// is Terminal (fail closed), terminate once, deny further requests.
	exhausted, outcome := e.Record(req, wait, "acme", "s1", 1000, 700) // cumulative 1100 >= 1000
	if !exhausted || outcome != Terminal {
		t.Fatalf("nil-seam exhaustion = (%v, %v), want (true, TERMINAL)", exhausted, outcome)
	}
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
	e := New(term, nil, nil)
	req, wait := bg()
	e.Record(req, wait, "acme", "s1", 500, 500) // exactly at budget
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
	e := New(term, nil, func(_, _ string, _, _ int64) { hookCalls++ })
	req, wait := bg()
	e.Record(req, wait, "acme", "s1", 100, 150) // exhaust
	e.Record(req, wait, "acme", "s1", 100, 150) // already exhausted
	e.Record(req, wait, "acme", "s1", 100, 150)
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
	e := New(term, nil, nil)
	req, wait := bg()
	e.Record(req, wait, "acme", "s1", 0, 5000) // unbounded so far
	if !e.Allow("s1") {
		t.Fatalf("a session with no budget set must be allowed")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("no termination without a budget, got %v", got)
	}
	// A budget appears (e.g. a delegation lease resolves) below the
	// already-accumulated total: the next record exhausts immediately.
	e.Record(req, wait, "acme", "s1", 1000, 1)
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
	e := New(&recordingTerminator{}, nil, nil)
	req, wait := bg()
	if !e.Allow("never-seen") {
		t.Fatalf("an unseen session must be allowed (first request)")
	}
	if !e.Allow("") {
		t.Fatalf("an empty session id must be allowed")
	}
	// Record/Forget/RaiseBudget/TerminateSession with empty id are no-ops
	// and must not panic.
	e.Record(req, wait, "acme", "", 10, 100)
	e.Forget("")
	e.RaiseBudget("", 100)
	e.TerminateSession("")
}

// Forget evicts a session's accounting so the map does not grow without
// bound; a re-seen session id starts fresh.
func TestForgetEvictsAccounting_spec_11_2(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil, nil)
	req, wait := bg()
	e.Record(req, wait, "acme", "s1", 100, 200) // exhaust
	if e.Allow("s1") {
		t.Fatalf("precondition: s1 should be exhausted")
	}
	e.Forget("s1")
	if !e.Allow("s1") {
		t.Fatalf("after Forget the session id is unknown and allowed")
	}
	// A fresh budget cycle on the re-seen id starts from zero consumption.
	e.Record(req, wait, "acme", "s1", 1000, 100)
	if !e.Allow("s1") {
		t.Fatalf("re-seen session under its fresh budget should be allowed")
	}
}

// spec: §8.6 line 629 — a Granted extension at the exhaustion boundary
// continues the session: no termination, no deny flag, the session stays
// admitted (the transparent path). The pre-fix code terminated
// unconditionally, so this fails against it.
func TestRecordGrantedSeamContinues_spec_8_6(t *testing.T) {
	term := &recordingTerminator{}
	seam := &recordingSeam{outcome: Granted, raiseDelta: 500}
	e := New(term, seam.fn, nil)
	seam.e = e
	req, wait := bg()

	exhausted, outcome := e.Record(req, wait, "acme", "s1", 1000, 1000) // exhausts, seam grants
	if !exhausted {
		t.Fatalf("crossing the budget boundary must report exhausted")
	}
	// The returned Outcome is what the recorder surfaces to the proxy so the
	// proxy delivers the held response without a second extension dispatch.
	if outcome != Granted {
		t.Fatalf("a Granted seam resolution must be returned as Granted, got %v", outcome)
	}
	if seam.count() != 1 {
		t.Fatalf("the extension seam must be consulted once at the exhaustion boundary, got %d calls", seam.count())
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("a Granted extension must not terminate the session, got %v", got)
	}
	if !e.Allow("s1") {
		t.Fatalf("a Granted extension raised the budget and cleared the deny flag: the session must be admitted")
	}
}

// spec: §8.6 line 629 — a Pending extension (the in-path deadline elapsed
// with an elicitation still unresolved) leaves the session ALIVE but
// denying per request: it sets the deny flag so Allow rejects, and it does
// NOT call TerminateSession. The out-of-band episode later reclaims it.
// The pre-fix code coupled deny to termination, so it would have
// terminated here; this asserts the decoupled Pending state.
func TestRecordPendingSeamDeniesButDoesNotTerminate_spec_8_6(t *testing.T) {
	term := &recordingTerminator{}
	seam := &recordingSeam{outcome: Pending}
	e := New(term, seam.fn, nil)
	req, wait := bg()

	exhausted, outcome := e.Record(req, wait, "acme", "s1", 1000, 1000) // exhausts, seam pends
	if !exhausted {
		t.Fatalf("crossing the budget boundary must report exhausted")
	}
	// The returned Outcome is Pending: the recorder surfaces it so the proxy
	// denies the current non-streaming request while the episode resolves.
	if outcome != Pending {
		t.Fatalf("a Pending seam resolution must be returned as Pending, got %v", outcome)
	}
	if seam.count() != 1 {
		t.Fatalf("the extension seam must be consulted once, got %d calls", seam.count())
	}
	if e.Allow("s1") {
		t.Fatalf("a Pending extension must deny the session's next request")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("a Pending extension must NOT terminate the session, got %v", got)
	}
}

// spec: §8.6 line 629, line 719 — after a Pending detach the out-of-band
// episode fan-out resolves the session. RaiseBudget (a grant) raises the
// budget and clears the deny flag so Allow passes again; a subsequent
// Record against the raised budget does not re-exhaust and does not
// re-consult the seam.
func TestRaiseBudgetClearsDenyAndSurvivesNextRecord_spec_8_6(t *testing.T) {
	term := &recordingTerminator{}
	seam := &recordingSeam{outcome: Pending}
	e := New(term, seam.fn, nil)
	req, wait := bg()

	e.Record(req, wait, "acme", "s1", 1000, 1000) // exhaust -> Pending -> deny
	if e.Allow("s1") {
		t.Fatalf("precondition: the Pending session must be denied")
	}

	// The deferred episode fan-out raises this session's budget by its
	// granted delta and clears the deny flag.
	e.RaiseBudget("s1", 1000)
	if !e.Allow("s1") {
		t.Fatalf("RaiseBudget must clear the deny flag so Allow admits the session")
	}

	// The next request settles usage under the raised budget. The caller
	// (the S4 recorder) passes the raised budget (base 1000 plus the granted
	// delta 1000 = 2000) as Record's budget argument, so cumulative 1500 <
	// 2000 does not re-exhaust, the seam is not consulted again, and the
	// session is not terminated. Passing the stale base budget instead would
	// clobber the raise, which is the §8.6 grant-survival hazard the S4
	// recorder resolves by computing base + accumulated delta.
	seamBefore := seam.count()
	e.Record(req, wait, "acme", "s1", 2000, 500) // cumulative 1500 < raised 2000
	if !e.Allow("s1") {
		t.Fatalf("a request under the raised budget must be admitted")
	}
	if seam.count() != seamBefore {
		t.Fatalf("a request under the raised budget must not re-consult the extension seam")
	}
	if got := term.snapshot(); len(got) != 0 {
		t.Fatalf("no termination expected after a successful raise, got %v", got)
	}
}

// spec: §8.6 line 719 — TerminateSession is the SessionReclaimer terminal
// path the episode fan-out takes for a joined session whose deferred
// outcome is terminal. It denies the session and delegates to the wired
// Terminator with ReasonBudgetExhausted.
func TestTerminateSessionReclaimerPath_spec_8_6(t *testing.T) {
	term := &recordingTerminator{}
	e := New(term, nil, nil)
	req, wait := bg()
	// A session the episode joined and that is being reclaimed as terminal.
	e.Record(req, wait, "acme", "s1", 0, 100) // seed the counter, no budget
	e.TerminateSession("s1")
	if e.Allow("s1") {
		t.Fatalf("TerminateSession must deny the session")
	}
	got := term.snapshot()
	if len(got) != 1 || got[0].sessionID != "s1" || got[0].reason != ReasonBudgetExhausted {
		t.Fatalf("TerminateSession = %v, want one {s1, %s}", got, ReasonBudgetExhausted)
	}
}

// spec: §8.6 line 629 — both context parameters reach the seam (reqCtx
// and waitCtx), and a reqCtx cancellation is observable at the seam. This
// pins the two-context threading that matches leasecontrol.ExtendForBudget.
func TestRecordThreadsBothContextsToSeam_spec_8_6(t *testing.T) {
	seam := &recordingSeam{outcome: Terminal}
	e := New(&recordingTerminator{}, seam.fn, nil)

	req, cancelReq := context.WithCancel(context.Background())
	wait, cancelWait := context.WithCancel(req)
	defer cancelWait()
	cancelReq() // cancel the request context before recording

	e.Record(req, wait, "acme", "s1", 100, 100) // exhausts, consults seam

	seam.mu.Lock()
	gotReq, gotWait, sess := seam.lastReq, seam.lastWait, seam.lastSess
	seam.mu.Unlock()
	if sess != "s1" {
		t.Fatalf("seam saw session %q, want s1", sess)
	}
	if gotReq == nil || gotWait == nil {
		t.Fatalf("both contexts must reach the seam: reqCtx=%v waitCtx=%v", gotReq, gotWait)
	}
	if gotReq.Err() == nil {
		t.Fatalf("the seam must observe the reqCtx cancellation (reqCtx.Err() != nil)")
	}
	// waitCtx derives from reqCtx, so it is cancelled too.
	if gotWait.Err() == nil {
		t.Fatalf("the derived waitCtx must observe the cancellation")
	}
}

// The enforcer structurally satisfies leasecontrol.SessionReclaimer:
// RaiseBudget(string, int64) and TerminateSession(string) are both
// present with the reclaimer signatures. This compiles the assignment to
// a local interface identical to leasecontrol.SessionReclaimer so a
// signature drift breaks the build here rather than in cmd/lenny-gateway.
// spec: §8.6 line 719; proposal 0023 S6.
func TestEnforcerSatisfiesSessionReclaimer_spec_8_6(t *testing.T) {
	type sessionReclaimer interface {
		RaiseBudget(sessionID string, delta int64)
		TerminateSession(sessionID string)
	}
	var _ sessionReclaimer = New(&recordingTerminator{}, nil, nil)
}

// The enforcer is safe under concurrent Record/Allow/Forget/RaiseBudget/
// TerminateSession, mirroring the tier-7a scenario's in-process race
// coverage at unit scale.
func TestConcurrentAccess_spec_11_2(t *testing.T) {
	seam := &recordingSeam{outcome: Pending}
	e := New(&recordingTerminator{}, seam.fn, nil)
	seam.e = e
	req, wait := bg()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "s"
			e.Record(req, wait, "acme", id, 10_000, int64(n))
			_ = e.Allow(id)
			switch n % 3 {
			case 0:
				e.RaiseBudget(id, 100)
			case 1:
				e.TerminateSession(id)
			case 2:
				if n%7 == 0 {
					e.Forget(id)
				}
			}
		}(i)
	}
	wg.Wait()
}
