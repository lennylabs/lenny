// SPDX-License-Identifier: MIT

// Package sessionbudget implements the §11.2 mid-session token-budget
// enforcement fast path. The §4.9 LLM proxy records the authoritative
// per-request token counts; this package tracks each proxy-mode
// session's cumulative consumption against the session's token budget,
// terminates a session the moment its consumption exhausts the budget
// (rather than waiting for session completion), and rejects further
// proxied requests for an exhausted session.
//
// spec: §11.2 line 44 ("The gateway enforces budget limits against
// Redis counters (fast path); if a session exceeds its token budget,
// the gateway terminates it immediately rather than waiting for session
// completion ... the gateway tracks usage in-memory per session and
// enforces the per-session budget locally") and §8.10 line 1108
// (over-budget proxy requests are rejected with BUDGET_EXHAUSTED in
// proxy mode).
package sessionbudget

import "sync"

// ReasonBudgetExhausted is the §8.8 line 867 FailureReason the enforcer
// stamps on a session it expires for token-budget exhaustion. The §8.8
// MCP adapter surfaces it to external clients as `failed` with error
// code `expired:budget`; the internal terminal state is `expired`
// (§7.1 line 175, "running → expired (lease/budget/deadline
// exhausted)").
const ReasonBudgetExhausted = "expired:budget"

// Terminator forces an over-budget session to its §7.1 `expired`
// terminal state. The gateway wires it to the same force-terminate path
// the §24.11 operator force-terminate and the §4.9 fallback-exhausted
// terminator use, so the pod is released and the terminal audit / billing
// / SSE signals fire exactly once.
type Terminator interface {
	// TerminateSession transitions sessionID to a terminal state with
	// the given §8.8 FailureReason. It is idempotent: a session already
	// terminal is a no-op.
	TerminateSession(sessionID, reason string)
}

// Enforcer is the §11.2 mid-session token-budget fast path. The
// per-session counters live in the proxy replica's memory: a session's
// proxy traffic is pinned to its coordinating replica, so the replica
// observes every token the session consumes. The §15.1 usage store is
// the durable rollup; this enforcer is the low-latency gate, and the
// in-memory per-session tracking is the same path §11.2 line 44 names
// for the Redis fail-open window.
//
// The zero value is not usable; construct with New. Every method is
// safe for concurrent use.
type Enforcer struct {
	mu        sync.Mutex
	sessions  map[string]*counter
	terminate Terminator
	// onExceeded fires once per session the first time it exhausts its
	// budget, under the enforcer lock. Nil disables the hook.
	onExceeded func(tenantID, sessionID string, budget, consumed int64)
}

// counter is one session's running token accounting.
type counter struct {
	budget    int64
	consumed  int64
	exhausted bool
}

// New returns an Enforcer that terminates over-budget sessions through
// t. onExceeded, when non-nil, is called once per session the first
// time it exhausts its budget (the metric / audit hook); it runs under
// the enforcer lock and must not block.
func New(t Terminator, onExceeded func(tenantID, sessionID string, budget, consumed int64)) *Enforcer {
	return &Enforcer{
		sessions:   make(map[string]*counter),
		terminate:  t,
		onExceeded: onExceeded,
	}
}

// Record adds tokens to sessionID's cumulative consumption under the
// given budget. The first time the session's cumulative consumption
// reaches or exceeds a positive budget, Record terminates it through the
// Terminator — the §11.2 line 44 "terminates it immediately". A
// budget <= 0 disables enforcement for the session (the running total is
// still tracked so a later positive budget resolution sees it);
// tokens <= 0 only refreshes the budget. tenantID is carried for the
// onExceeded hook. An empty sessionID is ignored.
//
// "Exhausts" is consumed >= budget, matching §8.10 line 1108's
// "token budget is exhausted": once the cumulative count reaches the
// budget the allocation is fully spent and the next request would
// overshoot, so the session is terminated at the boundary rather than
// after the first over-budget request.
func (e *Enforcer) Record(tenantID, sessionID string, budget, tokens int64) {
	if sessionID == "" {
		return
	}
	e.mu.Lock()
	c := e.sessions[sessionID]
	if c == nil {
		c = &counter{}
		e.sessions[sessionID] = c
	}
	c.budget = budget
	if tokens > 0 {
		c.consumed += tokens
	}
	exhausted := false
	if !c.exhausted && c.budget > 0 && c.consumed >= c.budget {
		c.exhausted = true
		exhausted = true
		if e.onExceeded != nil {
			e.onExceeded(tenantID, sessionID, c.budget, c.consumed)
		}
	}
	e.mu.Unlock()

	if exhausted && e.terminate != nil {
		// Run the terminator outside the lock: it resolves the session
		// store and runs the terminal pipeline, which must not block the
		// enforcer's per-request fast path or deadlock against a
		// concurrent Forget driven by that pipeline.
		e.terminate.TerminateSession(sessionID, ReasonBudgetExhausted)
	}
}

// Allow reports whether sessionID may issue another proxied request. A
// session not yet seen, or one still under its budget, is allowed; a
// session that has exhausted its budget is denied — the §8.10 line 1108
// BUDGET_EXHAUSTED rejection. An empty sessionID is allowed (the gate
// only applies to attributable proxy sessions).
func (e *Enforcer) Allow(sessionID string) bool {
	if sessionID == "" {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.sessions[sessionID]
	return c == nil || !c.exhausted
}

// Forget drops sessionID's accounting. The gateway calls it from the
// terminal-side-effects pipeline so the per-session map does not grow
// without bound as sessions settle. Forgetting an unknown session is a
// no-op.
func (e *Enforcer) Forget(sessionID string) {
	if sessionID == "" {
		return
	}
	e.mu.Lock()
	delete(e.sessions, sessionID)
	e.mu.Unlock()
}
