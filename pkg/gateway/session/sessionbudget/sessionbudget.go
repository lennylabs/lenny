// SPDX-License-Identifier: MIT

// Package sessionbudget implements the §11.2 mid-session token-budget
// enforcement fast path. The §4.9 LLM proxy records the authoritative
// per-request token counts; this package tracks each proxy-mode
// session's cumulative consumption against the session's token budget,
// and gates further proxied requests for an over-budget session.
//
// A proxy-mode session that reaches its budget does not terminate
// unconditionally. At the exhaustion boundary the enforcer consults an
// injected §8.6 extension seam (extendOnExhaustion): a granted extension
// raises the session's budget and the session continues; a terminal
// outcome (ceiling reached, user-rejected, error, or a genuine request
// cancellation) terminates it; and an elicitation still pending at the
// caller's in-path deadline leaves the session alive but denying every
// subsequent request until the extension resolves out-of-band. §11.2's
// "terminate immediately" is preserved for the non-extendable and
// nil-seam paths (proposal 0023).
//
// spec: §11.2 line 44 ("The gateway enforces budget limits against
// Redis counters (fast path); if a session exceeds its token budget,
// the gateway terminates it immediately rather than waiting for session
// completion ... the gateway tracks usage in-memory per session and
// enforces the per-session budget locally"), §8.6 line 629 (the
// gateway LLM Proxy drives the budget-exhaustion lease-extension trigger
// in-process), and §8.10 line 1108 (over-budget proxy requests are
// rejected with BUDGET_EXHAUSTED in proxy mode).
package sessionbudget

import (
	"context"
	"sync"
)

// ReasonBudgetExhausted is the §8.8 line 867 FailureReason the enforcer
// stamps on a session it expires for token-budget exhaustion. The §8.8
// MCP adapter surfaces it to external clients as `failed` with error
// code `expired:budget`; the internal terminal state is `expired`
// (§7.1 line 175, "running → expired (lease/budget/deadline
// exhausted)").
const ReasonBudgetExhausted = "expired:budget"

// Outcome is the tri-state result of a §8.6 budget-exhaustion extension
// attempt, mirroring leasecontrol.Outcome but defined locally so this
// package does not import leasecontrol and no import cycle forms. The
// cmd/lenny-gateway wiring (proposal 0023 S6) maps leasecontrol.Outcome
// onto this enum when it constructs the extendOnExhaustion seam.
// spec: §8.6 line 629; proposal 0023.
type Outcome int

const (
	// Granted means the extension resolved GRANTED/PARTIALLY_GRANTED
	// within the caller's in-path wait and the seam raised the session's
	// budget. The session continues: no deny flag, no termination.
	Granted Outcome = iota
	// Pending means the caller's in-path wait deadline elapsed while an
	// elicitation-mode extension episode was still unresolved. The session
	// is not terminated; it denies every subsequent request through the
	// deny flag until the out-of-band episode later raises its budget
	// (RaiseBudget) or terminates it.
	Pending
	// Terminal means the extension resolved CEILING_REACHED/REJECTED, the
	// underlying dispatch errored, or the caller's own request context was
	// cancelled. The session denies and is terminated (fail closed).
	Terminal
)

func (o Outcome) String() string {
	switch o {
	case Granted:
		return "GRANTED"
	case Pending:
		return "PENDING"
	case Terminal:
		return "TERMINAL"
	default:
		return "UNKNOWN"
	}
}

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

// ExtendOnExhaustion is the §8.6 extension seam the enforcer consults at
// the consumed-over-budget boundary. It carries BOTH the caller's own
// request context (reqCtx, r.Context()) and the derived in-path wait
// (waitCtx, context.WithTimeout(reqCtx, proxyExtensionWaitTimeout)) to
// match the landed two-context leasecontrol.ExtendForBudget: the
// extension attempt honors reqCtx for the Pending-vs-Terminal
// discrimination (a genuine request cancellation is Terminal) and waitCtx
// for the in-path deadline (a still-pending elicitation is Pending). It
// returns the tri-state Outcome the enforcer branches its
// deny/terminate/continue decision on. A nil seam is the non-extendable
// path: the enforcer denies and terminates immediately (§11.2 line 44).
// spec: §8.6 line 629; proposal 0023 S3/S4.
type ExtendOnExhaustion func(reqCtx, waitCtx context.Context, tenantID, sessionID string, budget, consumed int64) Outcome

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
	// extendOnExhaustion is the §8.6 extension seam consulted once per
	// session the first time it exhausts its budget. Nil disables the
	// extension attempt (the non-extendable path): the enforcer denies and
	// terminates immediately.
	extendOnExhaustion ExtendOnExhaustion
	// onExceeded fires once per session the first time it exhausts its
	// budget, under the enforcer lock. Nil disables the hook.
	onExceeded func(tenantID, sessionID string, budget, consumed int64)
}

// counter is one session's running token accounting.
type counter struct {
	budget   int64
	consumed int64
	// exhausted records that this session has already hit its budget once,
	// so the §8.6 extension seam is consulted at most once per distinct
	// exhaustion event and the onExceeded hook fires once. It is cleared by
	// RaiseBudget so a later exhaustion of the raised budget is a fresh
	// event that attempts a fresh extension.
	exhausted bool
	// deny is the deny-next-request flag Allow reads, decoupled from
	// termination. It is set on Pending and Terminal (and the
	// nil-seam/non-extendable path) so Allow rejects the session's next
	// request, and cleared by RaiseBudget on a grant so the session
	// continues. A Pending-but-alive session carries deny without ever
	// being terminated, which the single exhausted flag could not
	// represent. spec: §8.6 line 629; proposal 0023 S4.
	deny bool
}

// New returns an Enforcer that terminates over-budget sessions through
// t. extend, when non-nil, is the §8.6 extension seam consulted at the
// exhaustion boundary; a nil seam preserves the §11.2 line 44
// terminate-immediately behavior. onExceeded, when non-nil, is called
// once per session the first time it exhausts its budget (the metric /
// audit hook); it runs under the enforcer lock and must not block.
func New(t Terminator, extend ExtendOnExhaustion, onExceeded func(tenantID, sessionID string, budget, consumed int64)) *Enforcer {
	return &Enforcer{
		sessions:           make(map[string]*counter),
		terminate:          t,
		extendOnExhaustion: extend,
		onExceeded:         onExceeded,
	}
}

// Record adds tokens to sessionID's cumulative consumption under the
// given budget. The first time the session's cumulative consumption
// reaches or exceeds a positive budget, Record attempts the §8.6 lease
// extension through the injected seam before deciding the session's fate:
// on Granted the session continues, on Pending it is left alive but
// denying per request, and on Terminal (or a nil seam) it denies and is
// terminated through the Terminator — the §11.2 line 44 "terminates it
// immediately" for the non-extendable path. A budget <= 0 disables
// enforcement for the session (the running total is still tracked so a
// later positive budget resolution sees it); tokens <= 0 only refreshes
// the budget. tenantID is carried for the seam and the onExceeded hook.
// An empty sessionID is ignored.
//
// reqCtx is the caller's own request context (r.Context()); waitCtx is
// the derived in-path wait (context.WithTimeout(reqCtx,
// proxyExtensionWaitTimeout)). Both are threaded to the seam so the
// extension honors the request context for the Pending-vs-Terminal
// discrimination and the derived wait for the in-path deadline (proposal
// 0023 S3).
//
// "Exhausts" is consumed >= budget, matching §8.10 line 1108's
// "token budget is exhausted": once the cumulative count reaches the
// budget the allocation is fully spent and the next request would
// overshoot, so the extension is attempted at the boundary rather than
// after the first over-budget request.
func (e *Enforcer) Record(reqCtx, waitCtx context.Context, tenantID, sessionID string, budget, tokens int64) {
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
	consumed := c.consumed
	e.mu.Unlock()

	if !exhausted {
		return
	}

	// Attempt the §8.6 extension outside the lock: the seam blocks on the
	// extension episode's in-path wait and must not pin the enforcer's
	// per-request fast path or deadlock against a concurrent Forget driven
	// by the terminal pipeline, mirroring the terminate-outside-the-lock
	// pattern below. A nil seam is the non-extendable path and terminates
	// immediately.
	outcome := Terminal
	if e.extendOnExhaustion != nil {
		outcome = e.extendOnExhaustion(reqCtx, waitCtx, tenantID, sessionID, budget, consumed)
	}

	switch outcome {
	case Granted:
		// The seam raised the budget and cleared the deny flag through
		// RaiseBudget; the session continues. Nothing to deny or terminate.
		return
	case Pending:
		// The in-path deadline elapsed with an elicitation still unresolved.
		// Deny the session's next request but do NOT terminate it: the
		// out-of-band episode fan-out later raises its budget or terminates
		// it through the SessionReclaimer. spec: §8.6 line 629.
		e.setDeny(sessionID)
		return
	default:
		// Terminal, or a nil seam / non-extendable path. Deny and terminate.
		e.setDeny(sessionID)
		if e.terminate != nil {
			// Run the terminator outside the lock: it resolves the session
			// store and runs the terminal pipeline, which must not block the
			// enforcer's per-request fast path or deadlock against a
			// concurrent Forget driven by that pipeline.
			e.terminate.TerminateSession(sessionID, ReasonBudgetExhausted)
		}
	}
}

// setDeny sets sessionID's deny-next-request flag under the lock so the
// pre-flight Allow gate rejects its next request. A session dropped by a
// concurrent Forget is a no-op.
func (e *Enforcer) setDeny(sessionID string) {
	e.mu.Lock()
	if c := e.sessions[sessionID]; c != nil {
		c.deny = true
	}
	e.mu.Unlock()
}

// RaiseBudget raises sessionID's budget by delta and clears its deny
// flag so the pre-flight Allow gate admits its next request. It also
// clears the exhausted flag so a later exhaustion of the raised budget is
// a fresh event that attempts a fresh §8.6 extension rather than being
// suppressed as an already-seen exhaustion. delta is the granted token
// amount for the session. It is called on a GRANTED/PARTIALLY_GRANTED
// extension: by the in-path seam on an in-path Granted outcome, and by the
// per-tree episode's completion fan-out (through the SessionReclaimer)
// once per joined session on a deferred grant after a Pending outcome.
// Raising an unknown or already-forgotten session is a no-op. spec: §8.6
// line 629, line 719; proposal 0023 S4.
func (e *Enforcer) RaiseBudget(sessionID string, delta int64) {
	if sessionID == "" {
		return
	}
	e.mu.Lock()
	if c := e.sessions[sessionID]; c != nil {
		if delta > 0 {
			c.budget += delta
		}
		c.deny = false
		c.exhausted = false
	}
	e.mu.Unlock()
}

// TerminateSession terminates sessionID for budget exhaustion (fail
// closed), the path the per-tree episode's completion fan-out takes for a
// joined session whose deferred extension outcome is terminal. It sets the
// deny flag and delegates to the wired Terminator with
// ReasonBudgetExhausted, so the Enforcer structurally satisfies
// leasecontrol.SessionReclaimer and can be passed directly to
// Service.SetReclaimer (proposal 0023 S6). A nil Terminator or an empty
// sessionID is a no-op on the terminator. spec: §8.6 line 629, line 719.
func (e *Enforcer) TerminateSession(sessionID string) {
	if sessionID == "" {
		return
	}
	e.setDeny(sessionID)
	if e.terminate != nil {
		e.terminate.TerminateSession(sessionID, ReasonBudgetExhausted)
	}
}

// Allow reports whether sessionID may issue another proxied request. A
// session not yet seen, or one still under its budget, is allowed; a
// session whose deny-next-request flag is set is denied — the §8.10 line
// 1108 BUDGET_EXHAUSTED rejection, covering both a terminated session and
// a Pending-but-alive session awaiting an out-of-band extension. An empty
// sessionID is allowed (the gate only applies to attributable proxy
// sessions).
func (e *Enforcer) Allow(sessionID string) bool {
	if sessionID == "" {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.sessions[sessionID]
	return c == nil || !c.deny
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
