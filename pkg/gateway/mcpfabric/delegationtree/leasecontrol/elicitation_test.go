// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// mutableClock is an advanceable clock for cool-off-window tests.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// scriptedElicitor is a test Elicitor. It records the number of Elicit
// calls and returns a scripted decision. When release is non-nil it
// blocks until the channel is closed, so a test can hold an elicitation
// open while a second concurrent request joins the batch.
type scriptedElicitor struct {
	mu      sync.Mutex
	calls   int
	approve bool
	err     error
	release chan struct{}
	started chan struct{}
}

func (e *scriptedElicitor) Elicit(ctx context.Context, _ /*tenantID*/, _ /*sessionID*/ string) (bool, error) {
	e.mu.Lock()
	e.calls++
	n := e.calls
	rel := e.release
	e.mu.Unlock()
	if n == 1 && e.started != nil {
		close(e.started)
	}
	if rel != nil {
		select {
		case <-rel:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return e.approve, e.err
}

func (e *scriptedElicitor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func elicitTree(t *testing.T, clock func() time.Time, cfg leasecontrol.TreeConfig) *leasecontrol.MemoryBudgetSource {
	t.Helper()
	b := leasecontrol.NewMemoryBudgetSource()
	if clock != nil {
		b.WithClock(clock)
	}
	b.RegisterTree("root-1", cfg)
	return b
}

func baseElicitConfig() leasecontrol.TreeConfig {
	return leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	}
}

// TestElicitationApprovalGrants_spec_8_6_line_720: in elicitation mode an
// approved elicitation lets the grant proceed and records the approver as
// the client. F-8.6.2.
func TestElicitationApprovalGrants_spec_8_6_line_720(t *testing.T) {
	budgets := elicitTree(t, nil, baseElicitConfig())
	el := &scriptedElicitor{approve: true}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != leasecontrol.StatusGranted {
		t.Fatalf("status = %v, want GRANTED", resp.Status)
	}
	if el.callCount() != 1 {
		t.Errorf("elicitations = %d, want 1", el.callCount())
	}
	if len(rec.entries) != 1 || rec.entries[0].Approver != "client" {
		t.Errorf("approver = %q, want client (user approved the elicitation)", rec.entries[0].Approver)
	}
}

// TestElicitationRejectionDeniesSubtree_spec_8_6_line_729: a user
// rejection returns REJECTED, persists the subtree extension-denial, and
// audits the outcome as denied by the client. F-8.6.2.
func TestElicitationRejectionDeniesSubtree_spec_8_6_line_729(t *testing.T) {
	budgets := elicitTree(t, nil, baseElicitConfig())
	el := &scriptedElicitor{approve: false}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != leasecontrol.StatusRejected {
		t.Fatalf("status = %v, want REJECTED", resp.Status)
	}
	// §8.6 line 729 — the subtree is now extension-denied; a follow-up
	// request is auto-rejected during the cool-off without re-eliciting.
	tb, err := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if !tb.ExtensionDenied {
		t.Errorf("ExtensionDenied = false, want true after a user rejection")
	}
	if len(rec.entries) != 1 || rec.entries[0].Outcome != leasecontrol.AuditOutcomeDenied {
		t.Errorf("audit outcome = %q, want denied", rec.entries[0].Outcome)
	}
	if rec.entries[0].Approver != "client" {
		t.Errorf("approver = %q, want client", rec.entries[0].Approver)
	}
}

// TestElicitationConcurrentBatchesSingleElicitation_spec_8_6_line_719:
// two concurrent requests in the same tree share one elicitation; no
// duplicate prompt is sent and both receive the approved outcome.
// F-8.6.2.
func TestElicitationConcurrentBatchesSingleElicitation_spec_8_6_line_719(t *testing.T) {
	budgets := elicitTree(t, nil, baseElicitConfig())
	budgets.AddSession("child-A", "root-1", "acme")
	el := &scriptedElicitor{approve: true, release: make(chan struct{}), started: make(chan struct{})}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets, Elicitor: el})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	type res struct {
		resp leasecontrol.ExtendResponse
		err  error
	}
	out := make(chan res, 2)
	// Request 1 opens the elicitation and blocks inside Elicit.
	go func() {
		r, e := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000))
		out <- res{r, e}
	}()
	<-el.started // request 1 has opened the elicitation (pending set)
	// Request 2 (a different session in the same tree) joins the batch.
	go func() {
		r, e := svc.ExtendLease(context.Background(), extendReq("child-A", 20_000))
		out <- res{r, e}
	}()
	// Give request 2 time to reach the join-and-wait state, then release.
	time.Sleep(20 * time.Millisecond)
	close(el.release)

	for i := 0; i < 2; i++ {
		r := <-out
		if r.err != nil {
			t.Fatalf("ExtendLease #%d: %v", i, r.err)
		}
		if r.resp.Status != leasecontrol.StatusGranted {
			t.Errorf("status = %v, want GRANTED", r.resp.Status)
		}
	}
	if el.callCount() != 1 {
		t.Errorf("elicitations = %d, want 1 (concurrent requests batch onto one prompt)", el.callCount())
	}
}

// TestElicitationSuccessCoolOffAutoGrants_spec_8_6_line_723: after an
// approval opens the cool-off window, a follow-up request is auto-granted
// without a fresh elicitation and is attributed to gateway-auto. After
// the window expires, the next request opens a new elicitation cycle
// (§8.6 line 726). F-8.6.2.
func TestElicitationSuccessCoolOffAutoGrants_spec_8_6_line_723(t *testing.T) {
	clk := &mutableClock{now: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	cfg := baseElicitConfig()
	cfg.SuccessCoolOff = 5 * time.Second
	budgets := elicitTree(t, clk.Now, cfg)
	el := &scriptedElicitor{approve: true}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el, Clock: clk.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// First request elicits and approves.
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000)); err != nil {
		t.Fatalf("ExtendLease #1: %v", err)
	}
	// Second request, still inside the 5s window: auto-granted, no second
	// elicitation, attributed to gateway-auto.
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000)); err != nil {
		t.Fatalf("ExtendLease #2: %v", err)
	}
	if el.callCount() != 1 {
		t.Fatalf("elicitations = %d, want 1 during cool-off", el.callCount())
	}
	if got := rec.entries[1].Approver; got != "gateway-auto" {
		t.Errorf("cool-off grant approver = %q, want gateway-auto", got)
	}
	// Advance past the window: the next request opens a new elicitation.
	clk.advance(6 * time.Second)
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000)); err != nil {
		t.Fatalf("ExtendLease #3: %v", err)
	}
	if el.callCount() != 2 {
		t.Errorf("elicitations = %d, want 2 after the cool-off window expired", el.callCount())
	}
}

// TestElicitationNonDecisionErrorsWithoutDenial_spec_8_6_line_727: an
// Elicit transport failure (or timeout) is a non-decision — ExtendLease
// returns an error and the subtree is NOT marked denied. F-8.6.2.
func TestElicitationNonDecisionErrorsWithoutDenial_spec_8_6_line_727(t *testing.T) {
	budgets := elicitTree(t, nil, baseElicitConfig())
	el := &scriptedElicitor{err: errors.New("client stream gone")}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets, Elicitor: el})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000)); err == nil {
		t.Fatalf("ExtendLease err = nil, want a non-decision error")
	}
	tb, err := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if tb.ExtensionDenied {
		t.Errorf("ExtensionDenied = true, want false (a timeout must not persist a denial)")
	}
}

// TestElicitationModeWithoutElicitorFailsClosed_spec_8_6_line_714: an
// elicitation-mode tree with no Elicitor wired fails closed rather than
// silently auto-granting, which is the bug F-8.6.2 fixes.
func TestElicitationModeWithoutElicitorFailsClosed_spec_8_6_line_714(t *testing.T) {
	budgets := elicitTree(t, nil, baseElicitConfig())
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 10_000)); err == nil {
		t.Fatalf("ExtendLease err = nil, want fail-closed error for elicitation mode with no elicitor")
	}
}

// TestAutoModeUnderLimitGrantsWithoutElicitation_spec_8_6_line_712: with
// a positive limit, auto-mode requests under the cap are granted
// independently and never call the elicitor. F-8.6.7.
func TestAutoModeUnderLimitGrantsWithoutElicitation_spec_8_6_line_712(t *testing.T) {
	cfg := baseElicitConfig()
	cfg.ApprovalMode = leasecontrol.ApprovalModeAuto
	cfg.AutoMaxPerMinute = 3
	budgets := elicitTree(t, nil, cfg)
	el := &scriptedElicitor{approve: true}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: el, AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 1_000)); err != nil {
			t.Fatalf("ExtendLease #%d: %v", i, err)
		}
	}
	if el.callCount() != 0 {
		t.Errorf("elicitations = %d, want 0 (under the auto rate limit)", el.callCount())
	}
}

// TestAutoModeRateLimitFallsBackToElicitation_spec_8_6_line_712: once a
// tree exceeds maxAutoExtensionsPerMinute, the gateway pauses
// auto-approval, logs the fallback, and routes the request through
// elicitation. F-8.6.7.
func TestAutoModeRateLimitFallsBackToElicitation_spec_8_6_line_712(t *testing.T) {
	clk := &mutableClock{now: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	cfg := baseElicitConfig()
	cfg.ApprovalMode = leasecontrol.ApprovalModeAuto
	cfg.AutoMaxPerMinute = 2
	budgets := elicitTree(t, clk.Now, cfg)
	el := &scriptedElicitor{approve: true}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el,
		AutoExtensionCounter: ratelimit.NewMemory(), Clock: clk.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// Two auto-grants are within the limit; the third trips it.
	for i := 0; i < 3; i++ {
		if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 1_000)); err != nil {
			t.Fatalf("ExtendLease #%d: %v", i, err)
		}
	}
	if el.callCount() != 1 {
		t.Errorf("elicitations = %d, want 1 (only the over-limit request falls back)", el.callCount())
	}
	if len(rec.rateLimit) != 1 {
		t.Fatalf("rate-limit audits = %d, want 1", len(rec.rateLimit))
	}
	if rec.rateLimit[0].MaxPerMinute != 2 {
		t.Errorf("audit MaxPerMinute = %d, want 2", rec.rateLimit[0].MaxPerMinute)
	}
}

// TestAutoModeRateLimitDeploymentDefault_spec_8_6_line_712: a tree with
// no per-tree limit inherits the deployment default, so the safety valve
// is operable from a single knob. F-8.6.7.
func TestAutoModeRateLimitDeploymentDefault_spec_8_6_line_712(t *testing.T) {
	cfg := baseElicitConfig()
	cfg.ApprovalMode = leasecontrol.ApprovalModeAuto // no AutoMaxPerMinute set
	budgets := elicitTree(t, nil, cfg)
	el := &scriptedElicitor{approve: true}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el,
		AutoExtensionCounter: ratelimit.NewMemory(), DefaultAutoMaxPerMinute: 1,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 1_000)); err != nil {
			t.Fatalf("ExtendLease #%d: %v", i, err)
		}
	}
	if len(rec.rateLimit) != 1 {
		t.Errorf("rate-limit audits = %d, want 1 (deployment default of 1 tripped on the 2nd request)", len(rec.rateLimit))
	}
}

// TestAutoModeNoLimitNeverFallsBack_spec_8_6_line_712: the spec default
// (no limit) leaves auto mode fully independent — no elicitation, no
// rate-limit audit, regardless of request volume. F-8.6.7.
func TestAutoModeNoLimitNeverFallsBack_spec_8_6_line_712(t *testing.T) {
	cfg := baseElicitConfig()
	cfg.ApprovalMode = leasecontrol.ApprovalModeAuto
	budgets := elicitTree(t, nil, cfg)
	el := &scriptedElicitor{approve: true}
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: el,
		AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for i := 0; i < 25; i++ {
		if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 1)); err != nil {
			t.Fatalf("ExtendLease #%d: %v", i, err)
		}
	}
	if el.callCount() != 0 || len(rec.rateLimit) != 0 {
		t.Errorf("elicitations=%d rateLimitAudits=%d, want 0/0 (no limit configured)", el.callCount(), len(rec.rateLimit))
	}
}

// fakeReclaimer records the §8.6 episode fan-out's per-session
// RaiseBudget and TerminateSession calls so a test can assert which
// joined session was raised and which was terminated. It is safe for the
// concurrent fan-out.
type fakeReclaimer struct {
	mu         sync.Mutex
	raised     map[string]int64
	terminated map[string]int
}

func newFakeReclaimer() *fakeReclaimer {
	return &fakeReclaimer{raised: map[string]int64{}, terminated: map[string]int{}}
}

func (r *fakeReclaimer) RaiseBudget(sessionID string, delta int64) {
	r.mu.Lock()
	r.raised[sessionID] += delta
	r.mu.Unlock()
}

func (r *fakeReclaimer) TerminateSession(sessionID string) {
	r.mu.Lock()
	r.terminated[sessionID]++
	r.mu.Unlock()
}

func (r *fakeReclaimer) raiseCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.raised)
}

func (r *fakeReclaimer) terminateCount(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminated[sessionID]
}

// TestExtendForBudgetPerTreeEpisodeFanOut_spec_8_6_line_719: two sessions
// in one tree exhaust concurrently and join one pending per-tree
// extension episode (§8.6 line 719 batching). Both detach at the in-path
// deadline (returning Pending), then the single episode's per-session
// fan-out resolves each independently: the session with token headroom is
// raised, and the session already at its ceiling is terminated (fail
// closed). Exactly one elicitation is opened for the whole tree, and the
// fan-out reclaims every joined session so none is left denied. This is
// the batching + fan-out regression proposal 0023 requires.
func TestExtendForBudgetPerTreeEpisodeFanOut_spec_8_6_line_719(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	// A tree with a 1M token ceiling. Session A (the root) has headroom.
	// Session B's parent lease caps its extension at its own current
	// budget, so B's per-session Grant math returns CEILING_REACHED
	// (terminal) while A's returns GRANTED — the shared elicitation
	// resolves once, the grant math resolves per session (§8.6 line
	// 737-741).
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	budgets.AddSession("child-B", "root-1", "acme")
	// child-B's parent granted only 100_000 tokens, equal to its current
	// budget, so it has zero headroom and its extension is CEILING_REACHED.
	budgets.SetParentLease("child-B", leasecontrol.SessionLease{TokenCeiling: 100_000})

	// scriptedElicitor approves once, and blocks inside Elicit until
	// released so both requests batch onto the one pending elicitation
	// and both detach at the short in-path deadline.
	el := &scriptedElicitor{approve: true, release: make(chan struct{}), started: make(chan struct{})}
	// The episode dispatches its elicitation on this background context, so
	// the caller's cancelled in-path wait does not cancel the elicitation.
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        budgets,
		Tenants:        budgets,
		Elicitor:       el,
		EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	// Both sessions exhaust and call ExtendForBudget with a short in-path
	// deadline, so each detaches at Pending while the shared elicitation
	// blocks.
	type outcomeRes struct {
		sess    string
		outcome leasecontrol.Outcome
		err     error
	}
	out := make(chan outcomeRes, 2)
	call := func(sess string) {
		reqCtx := context.Background()
		waitCtx, cancel := context.WithTimeout(reqCtx, 30*time.Millisecond)
		defer cancel()
		o, e := svc.ExtendForBudget(reqCtx, waitCtx, sess)
		out <- outcomeRes{sess, o, e}
	}
	go call("root-1")
	<-el.started // the first request opened the episode's elicitation.
	go call("child-B")

	// Collect both in-path results: both must be Pending (the deadline
	// elapsed while the elicitation was still blocked).
	for i := 0; i < 2; i++ {
		r := <-out
		if r.err != nil {
			t.Fatalf("ExtendForBudget(%s): %v", r.sess, r.err)
		}
		if r.outcome != leasecontrol.OutcomePending {
			t.Errorf("ExtendForBudget(%s) outcome = %v, want Pending (in-path deadline)", r.sess, r.outcome)
		}
	}

	// Exactly one elicitation was opened for the whole tree (§8.6 line 719).
	if el.callCount() != 1 {
		t.Errorf("elicitations = %d, want 1 (both sessions batch onto one per-tree episode)", el.callCount())
	}

	// Release the elicitation; the single episode goroutine resolves both
	// sessions and fans out.
	close(el.release)

	// Wait for the fan-out to reclaim both sessions.
	deadline := time.Now().Add(2 * time.Second)
	for reclaimer.raiseCount() < 1 || reclaimer.terminateCount("child-B") < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("fan-out did not reclaim both sessions: raised=%d terminated(child-B)=%d",
				reclaimer.raiseCount(), reclaimer.terminateCount("child-B"))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Session A (headroom) was raised; session B (ceiling) was terminated.
	if reclaimer.raised["root-1"] <= 0 {
		t.Errorf("root-1 raised delta = %d, want > 0 (headroom session raised)", reclaimer.raised["root-1"])
	}
	if reclaimer.terminateCount("child-B") != 1 {
		t.Errorf("child-B terminations = %d, want exactly 1 (ceiling session terminated once)", reclaimer.terminateCount("child-B"))
	}
	if reclaimer.terminateCount("root-1") != 0 {
		t.Errorf("root-1 terminations = %d, want 0 (a granted session must not be terminated)", reclaimer.terminateCount("root-1"))
	}

	// child-B was raised for exactly one session (root-1), confirming the
	// fan-out did not raise the ceiling-reached session.
	if reclaimer.raiseCount() != 1 {
		t.Errorf("sessions raised = %d, want 1 (only the headroom session)", reclaimer.raiseCount())
	}
}

// TestExtendForBudgetFanOutTerminatesOnDispatchError_spec_8_6_line_719:
// a session that detaches at the in-path deadline (returning Pending) and
// whose out-of-band episode dispatch then ERRORS (a non-decision
// elicitation fault, not a rejection) is terminated by the fan-out, not
// raised. This pins the fail-closed fan-out branch for a dispatch error:
// a transport/elicitation fault during the deferred resolution tears the
// over-budget session down rather than leaving it alive with unraised
// budget.
//
// diagnosis: a §8.6 extension episode that errored out-of-band failed
// OPEN — the detached over-budget session was neither raised nor
// terminated, so it kept its deny flag with nothing to clear it, or worse
// was left admittable. The fan-out must terminate a session whose
// deferred dispatch errors.
func TestExtendForBudgetFanOutTerminatesOnDispatchError_spec_8_6_line_719(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	// The elicitor blocks until released, then returns a non-decision error,
	// so ExtendLease errors and dispatchOne returns sessionResult{err}. The
	// caller detaches at its short in-path deadline before the release, so
	// the fan-out (not the in-path caller) applies the terminal resolution.
	el := &scriptedElicitor{err: errors.New("elicitation stream gone"), release: make(chan struct{}), started: make(chan struct{})}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: el, EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	out := make(chan leasecontrol.Outcome, 1)
	go func() {
		reqCtx := context.Background()
		waitCtx, cancel := context.WithTimeout(reqCtx, 30*time.Millisecond)
		defer cancel()
		o, _ := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
		out <- o
	}()
	<-el.started // the episode opened its elicitation and is blocked in it.

	// The in-path caller detaches at Pending (the elicitation is still
	// blocked when the 30ms deadline elapses).
	if got := <-out; got != leasecontrol.OutcomePending {
		t.Fatalf("in-path outcome = %v, want Pending (deadline elapsed while dispatch blocked)", got)
	}

	// Release the elicitation; it now returns its non-decision error, the
	// dispatch resolves to an error, and the fan-out terminates the detached
	// session out-of-band.
	close(el.release)

	deadline := time.Now().Add(2 * time.Second)
	for reclaimer.terminateCount("root-1") < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("fan-out did not terminate the dispatch-errored session (fail OPEN): terminated=%d raised=%d",
				reclaimer.terminateCount("root-1"), reclaimer.raiseCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reclaimer.raiseCount() != 0 {
		t.Errorf("dispatch-errored session was raised %d times, want 0 (a dispatch error must not grant budget)", reclaimer.raiseCount())
	}
}

// treeBudgetErrSource wraps a MemoryBudgetSource and forces TreeBudget to
// error after tenant resolution succeeds, so a test can drive the
// ExtendForBudget budget-load failure branch (a resolvable session whose
// tree budget cannot be read). Every other method delegates.
type treeBudgetErrSource struct {
	inner *leasecontrol.MemoryBudgetSource
	err   error
}

func (s treeBudgetErrSource) TreeBudget(context.Context, string, string) (leasecontrol.TreeBudget, error) {
	return leasecontrol.TreeBudget{}, s.err
}

func (s treeBudgetErrSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted leasecontrol.Dimensions) (leasecontrol.NewLimits, error) {
	return s.inner.ApplyGrant(ctx, tenantID, rootSessionID, requestingSessionID, granted)
}

func (s treeBudgetErrSource) RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	return s.inner.RejectionCoolOff(ctx, tenantID, rootSessionID)
}

func (s treeBudgetErrSource) Deny(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) error {
	return s.inner.Deny(ctx, tenantID, rootSessionID, requestingSessionID)
}

func (s treeBudgetErrSource) TenantOf(ctx context.Context, sessionID string) (string, error) {
	return s.inner.TenantOf(ctx, sessionID)
}

// TestExtendForBudgetTreeBudgetErrorIsTerminal_spec_8_6_line_629: a
// session that resolves to a tenant but whose tree budget cannot be read
// fails closed with Terminal and a wrapped error rather than proceeding to
// an extension. The proxy must not extend a session whose ceiling it
// cannot even load.
func TestExtendForBudgetTreeBudgetErrorIsTerminal_spec_8_6_line_629(t *testing.T) {
	inner := leasecontrol.NewMemoryBudgetSource()
	inner.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID: "acme", CurrentTokenBudget: 100_000,
		DeploymentBase: 1_000_000, DeploymentMax: 2_000_000,
		ApprovalMode: leasecontrol.ApprovalModeAuto,
	})
	src := treeBudgetErrSource{inner: inner, err: errors.New("budget store down")}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: src, Tenants: src, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.ExtendForBudget(context.Background(), context.Background(), "root-1")
	if err == nil {
		t.Fatal("ExtendForBudget should error when the tree budget cannot be loaded (fail closed)")
	}
	if got != leasecontrol.OutcomeTerminal {
		t.Errorf("outcome = %v, want Terminal on a tree-budget load failure", got)
	}
}

// subtreeDenialSource is a BudgetSource whose extension-denied flag is
// scoped per requesting subtree, matching the production Postgres source
// that keys the flag with a per-row subtree id (§8.6 line 729/730). It
// wraps a MemoryBudgetSource for the budget math and only overrides the
// denial scope: Deny(requestingSessionID) marks that subtree alone, and
// TreeBudget(sessionID) reports ExtensionDenied only when that session's
// own subtree was denied. This is the double the §8.6 line 719 batching
// regression needs: it lets one session's rejection deny only its own
// subtree, so a sibling that failed to join the live elicitation batch
// would re-elicit (a second prompt), which the tree-wide MemoryBudgetSource
// would silently mask. spec: §8.6 line 719, line 729.
type subtreeDenialSource struct {
	inner  *leasecontrol.MemoryBudgetSource
	clock  func() time.Time
	mu     sync.Mutex
	denied map[string]time.Time // requestingSessionID -> cool-off expiry
}

func newSubtreeDenialSource(clock func() time.Time) *subtreeDenialSource {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &subtreeDenialSource{
		inner:  leasecontrol.NewMemoryBudgetSource().WithClock(clock),
		clock:  clock,
		denied: map[string]time.Time{},
	}
}

func (s *subtreeDenialSource) TreeBudget(ctx context.Context, tenantID, sessionID string) (leasecontrol.TreeBudget, error) {
	tb, err := s.inner.TreeBudget(ctx, tenantID, sessionID)
	if err != nil {
		return tb, err
	}
	s.mu.Lock()
	expiry, denied := s.denied[sessionID]
	s.mu.Unlock()
	// Only the requesting subtree's own denial applies; a sibling's
	// rejection leaves this subtree extendable.
	tb.ExtensionDenied = denied && s.clock().Before(expiry)
	if denied {
		tb.CoolOffExpiry = expiry
	}
	return tb, nil
}

func (s *subtreeDenialSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted leasecontrol.Dimensions) (leasecontrol.NewLimits, error) {
	return s.inner.ApplyGrant(ctx, tenantID, rootSessionID, requestingSessionID, granted)
}

func (s *subtreeDenialSource) RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	return s.inner.RejectionCoolOff(ctx, tenantID, rootSessionID)
}

func (s *subtreeDenialSource) Deny(_ context.Context, _ /*tenantID*/, _ /*rootSessionID*/, requestingSessionID string) error {
	s.mu.Lock()
	s.denied[requestingSessionID] = s.clock().Add(leasecontrol.DefaultRejectionCoolOff)
	s.mu.Unlock()
	return nil
}

func (s *subtreeDenialSource) TenantOf(ctx context.Context, sessionID string) (string, error) {
	return s.inner.TenantOf(ctx, sessionID)
}

// TestExtendForBudgetPerTreeEpisodeRejectionSinglePrompt_spec_8_6_line_719:
// two distinct subtrees in one tree exhaust concurrently, join one
// per-tree episode, and share a SINGLE elicitation prompt even on the
// rejection path. The denial source scopes the extension-denied flag per
// subtree (as production does), so if the second session had NOT joined
// the live batch through treeConsent.pending, it would open a second
// prompt (el.callCount() == 2) once the first session's rejection resolved
// and cleared the batch. Because the episode dispatches its joined members
// concurrently, the second session observes the live tc.pending and awaits
// the one prompt, so el.callCount() == 1. This pins the §8.6 line 719
// "one elicitation at a time, concurrent requests batched" invariant on
// the rejection path, which the sequential (pre-fix) dispatch broke and
// the success cool-off could not mask.
func TestExtendForBudgetPerTreeEpisodeRejectionSinglePrompt_spec_8_6_line_719(t *testing.T) {
	budgets := newSubtreeDenialSource(nil)
	budgets.inner.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	budgets.inner.AddSession("child-B", "root-1", "acme")

	// The elicitor rejects, and blocks inside Elicit until released so both
	// concurrent requests reach requestConsent and the second batches onto
	// the first's live prompt (§8.6 line 719) rather than opening a second.
	el := &scriptedElicitor{approve: false, release: make(chan struct{}), started: make(chan struct{})}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        budgets,
		Tenants:        budgets,
		Elicitor:       el,
		EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	type outcomeRes struct {
		sess    string
		outcome leasecontrol.Outcome
		err     error
	}
	out := make(chan outcomeRes, 2)
	call := func(sess string) {
		reqCtx := context.Background()
		waitCtx, cancel := context.WithTimeout(reqCtx, 30*time.Millisecond)
		defer cancel()
		o, e := svc.ExtendForBudget(reqCtx, waitCtx, sess)
		out <- outcomeRes{sess, o, e}
	}
	go call("root-1")
	<-el.started // the first request opened the one elicitation.
	go call("child-B")

	// Both in-path callers detach at Pending while the elicitation blocks.
	for i := 0; i < 2; i++ {
		r := <-out
		if r.err != nil {
			t.Fatalf("ExtendForBudget(%s): %v", r.sess, r.err)
		}
		if r.outcome != leasecontrol.OutcomePending {
			t.Errorf("ExtendForBudget(%s) outcome = %v, want Pending (in-path deadline)", r.sess, r.outcome)
		}
	}

	// The batching invariant: exactly one prompt for the whole tree even
	// though both subtrees exhausted. Under the pre-fix sequential dispatch,
	// child-B would not have joined the live batch (the episode goroutine
	// was blocked inside root-1's Elicit), so after root-1's rejection
	// cleared tc.pending, child-B — a distinct, still-extendable subtree —
	// would open a SECOND prompt and this assertion would read 2.
	if el.callCount() != 1 {
		t.Errorf("elicitations = %d, want 1 (both subtrees batch onto one prompt on the rejection path, §8.6 line 719)", el.callCount())
	}

	// Release the rejection; the episode fans out and terminates both
	// sessions (fail closed — a rejected extension is terminal).
	close(el.release)
	deadline := time.Now().Add(2 * time.Second)
	for reclaimer.terminateCount("root-1") < 1 || reclaimer.terminateCount("child-B") < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("fan-out did not terminate both rejected sessions: root-1=%d child-B=%d",
				reclaimer.terminateCount("root-1"), reclaimer.terminateCount("child-B"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A rejected extension raises no session's budget.
	if reclaimer.raiseCount() != 0 {
		t.Errorf("sessions raised = %d, want 0 (a rejected extension grants nothing)", reclaimer.raiseCount())
	}
	// Still exactly one prompt after resolution.
	if el.callCount() != 1 {
		t.Errorf("elicitations after resolution = %d, want 1 (no second prompt opened)", el.callCount())
	}
}

// TestExtendForBudgetAutoModeGrantedInPath_spec_8_6_line_629: in auto
// mode the extension episode resolves without a human, so it completes
// within the caller's in-path wait and ExtendForBudget returns Granted
// (the transparent path). The corrected behavior after proposal 0023:
// the gateway LLM Proxy drives this in-process trigger, and a fast
// (auto) extension keeps the session alive rather than terminating it.
func TestExtendForBudgetAutoModeGrantedInPath_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
		AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reqCtx := context.Background()
	waitCtx, cancel := context.WithTimeout(reqCtx, 2*time.Second)
	defer cancel()
	got, err := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
	if err != nil {
		t.Fatalf("ExtendForBudget: %v", err)
	}
	if got != leasecontrol.OutcomeGranted {
		t.Errorf("outcome = %v, want Granted (auto-mode extension resolves in-path)", got)
	}
	// The raise landed: the session's per-session budget rose toward the
	// 1M ceiling.
	tb, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if tb.Current.Tokens <= 100_000 {
		t.Errorf("post-extension budget = %d, want > 100000 (extension raised it)", tb.Current.Tokens)
	}
}

// TestExtendForBudgetInPathGrantRaisesEnforcer_spec_8_6_line_629 is the
// regression for proposal 0023 findings 1 and 3: an in-path GRANTED extension
// (the auto-mode fast path that resolves within the caller's in-path wait) must
// raise the enforcer's per-session budget through the SessionReclaimer, not only
// the leasecontrol view. Against the pre-fix code the in-path Granted branch
// returned OutcomeGranted without touching the reclaimer — RaiseBudget fired only
// on the detached/Pending fan-out path — so the enforcer's c.budget stayed at the
// pre-extension value and c.exhausted stayed set, leaving the session effectively
// unenforced. This test wires a fake reclaimer, drives an in-path auto-mode grant,
// and asserts RaiseBudget was called exactly once for the granting session with a
// positive delta and TerminateSession never fired. It fails against the pre-fix
// code because the reclaimer records no in-path raise.
//
// spec: 8.6 (in-process budget-exhaustion extension raises the enforcer budget)
// diagnosis: an in-path GRANTED extension does not raise the enforcer's per-session budget or clear its exhausted flag — RaiseBudget is unreachable on the non-detached path, so the session continues with a stale, effectively-unenforced budget (proposal 0023 S3/S4 in-path raise broken).
func TestExtendForBudgetInPathGrantRaisesEnforcer_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
		AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	reqCtx := context.Background()
	waitCtx, cancel := context.WithTimeout(reqCtx, 2*time.Second)
	defer cancel()
	got, err := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
	if err != nil {
		t.Fatalf("ExtendForBudget: %v", err)
	}
	if got != leasecontrol.OutcomeGranted {
		t.Fatalf("outcome = %v, want Granted (auto-mode extension resolves in-path)", got)
	}
	// The in-path Granted path must reflect the grant on the enforcer through
	// the reclaimer, exactly as the deferred fan-out does for a detached
	// session. Without this the enforcer's budget is never raised in-path.
	if reclaimer.raiseCount() != 1 {
		t.Fatalf("in-path grant must raise exactly one session through the reclaimer, raiseCount=%d", reclaimer.raiseCount())
	}
	if reclaimer.raised["root-1"] <= 0 {
		t.Fatalf("in-path grant must raise root-1 by a positive granted delta, got %d", reclaimer.raised["root-1"])
	}
	if reclaimer.terminateCount("root-1") != 0 {
		t.Fatalf("an in-path grant must not terminate the session, terminations=%d", reclaimer.terminateCount("root-1"))
	}
}

// TestExtendForBudgetInPathTerminalNoRaise_spec_8_6_line_712 guards the
// complementary in-path terminal branch so the raise test above is not
// trivially satisfied by a reclaimer that always raises: an in-path
// CEILING_REACHED resolution returns Terminal and must NOT raise the enforcer
// budget through the reclaimer (fail closed).
//
// spec: 8.6 (ceiling-reached in-path outcome is terminal, no raise)
// diagnosis: an in-path CEILING_REACHED extension incorrectly raised the enforcer budget instead of failing closed, letting a session at its ceiling continue with a fabricated budget increase (proposal 0023 fail-closed in-path posture broken).
func TestExtendForBudgetInPathTerminalNoRaise_spec_8_6_line_712(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 500_000,
		DeploymentBase:     500_000, // no headroom: ceiling reached
		DeploymentMax:      500_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
		AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	reqCtx := context.Background()
	waitCtx, cancel := context.WithTimeout(reqCtx, 2*time.Second)
	defer cancel()
	got, err := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
	if err != nil {
		t.Fatalf("ExtendForBudget: %v", err)
	}
	if got != leasecontrol.OutcomeTerminal {
		t.Fatalf("outcome = %v, want Terminal (ceiling reached, fail closed)", got)
	}
	if reclaimer.raiseCount() != 0 {
		t.Fatalf("an in-path ceiling-reached outcome must not raise any session, raiseCount=%d", reclaimer.raiseCount())
	}
}

// TestExtendForBudgetCeilingReachedTerminal_spec_8_6_line_712: a session
// whose tree is already at its token ceiling extends to CEILING_REACHED,
// which ExtendForBudget maps to Terminal (fail closed): the proxy
// terminates the session and returns BUDGET_EXHAUSTED. This pins the
// terminal branch so a regression that treats a zero grant as recoverable
// (an infinite retry loop, §8.6 line 712) fails.
func TestExtendForBudgetCeilingReachedTerminal_spec_8_6_line_712(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 500_000,
		DeploymentBase:     500_000, // effective ceiling equals current: no headroom
		DeploymentMax:      500_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
		AutoExtensionCounter: ratelimit.NewMemory(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reqCtx := context.Background()
	waitCtx, cancel := context.WithTimeout(reqCtx, 2*time.Second)
	defer cancel()
	got, err := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
	if err != nil {
		t.Fatalf("ExtendForBudget: %v", err)
	}
	if got != leasecontrol.OutcomeTerminal {
		t.Errorf("outcome = %v, want Terminal (ceiling reached, fail closed)", got)
	}
}

// TestExtendForBudgetClientDisconnectIsTerminal_spec_8_6_line_629: when
// the caller's own request context is cancelled (a client disconnect)
// while an elicitation is still blocked, ExtendForBudget returns Terminal
// rather than Pending — a genuine parent cancellation fails closed,
// distinguished from the in-path deadline. Proposal 0023 S3.
func TestExtendForBudgetClientDisconnectIsTerminal_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	el := &scriptedElicitor{approve: true, release: make(chan struct{}), started: make(chan struct{})}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: el, EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	// The in-path wait derives from reqCtx with a long timeout, so the wait
	// deadline cannot fire first. This isolates the parent-cancellation path:
	// only the client disconnect (reqCtx cancellation) can unblock the caller,
	// and the Terminal outcome must come from inspecting reqCtx.Err(), not
	// from any wait-deadline expiry. An implementation that inspected the
	// derived wait context's error type instead of reqCtx would still see a
	// Canceled here (cancellation propagates), so this test also guards that
	// the wait context is derived from reqCtx rather than an independent
	// timeout that would surface DeadlineExceeded (Pending) on a disconnect.
	waitCtx, waitCancel := context.WithTimeout(reqCtx, time.Hour)
	defer waitCancel()
	type outcomeRes struct {
		outcome leasecontrol.Outcome
		err     error
	}
	out := make(chan outcomeRes, 1)
	go func() {
		o, e := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
		out <- outcomeRes{o, e}
	}()
	<-el.started // the elicitation is open and blocked inside the episode.
	// The client disconnects: cancel the request context. The elicitation
	// is still blocked (release is not closed), so the caller unblocks via
	// waitCtx.Done() (cancellation propagates from reqCtx) and returns
	// Terminal (fail closed) rather than Pending, because reqCtx.Err() is
	// non-nil — a genuine parent cancellation. Deterministic because the
	// episode cannot resolve while Elicit is still blocked.
	cancel()
	r := <-out
	if r.err != nil {
		t.Fatalf("ExtendForBudget: %v", r.err)
	}
	if r.outcome != leasecontrol.OutcomeTerminal {
		t.Errorf("outcome = %v, want Terminal (client disconnect fails closed)", r.outcome)
	}
	// Release the still-open elicitation so the episode goroutine resolves
	// and exits rather than leaking after the test returns.
	close(el.release)
}

// TestExtendForBudgetInPathDeadlineIsPending_spec_8_6_line_629: when the
// in-path wait deadline elapses while the request context reqCtx stays
// live (the elicitation is still blocked), ExtendForBudget returns
// Pending rather than Terminal. This pins the Pending-vs-Terminal
// discriminator on reqCtx.Err() (proposal 0023 S3 line 148): the wait
// deadline fires with DeadlineExceeded on the derived wait context, but
// because reqCtx is not cancelled the outcome is Pending, leaving the
// episode running for the fan-out. A regression that treated the wait
// context's own DeadlineExceeded (or that inspected the wait context
// rather than reqCtx) as anything but Pending would flip this to Terminal
// and terminate a recoverable session. Proposal 0023 S3.
func TestExtendForBudgetInPathDeadlineIsPending_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	el := &scriptedElicitor{approve: true, release: make(chan struct{}), started: make(chan struct{})}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: el, EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reclaimer := newFakeReclaimer()
	svc.SetReclaimer(reclaimer)

	// reqCtx stays live for the whole call; only the derived wait context
	// carries the short in-path deadline. A disconnect never happens, so the
	// caller unblocks solely on the wait deadline (DeadlineExceeded) while
	// reqCtx.Err() is nil.
	reqCtx := context.Background()
	waitCtx, cancel := context.WithTimeout(reqCtx, 30*time.Millisecond)
	defer cancel()
	type outcomeRes struct {
		outcome leasecontrol.Outcome
		err     error
	}
	out := make(chan outcomeRes, 1)
	go func() {
		o, e := svc.ExtendForBudget(reqCtx, waitCtx, "root-1")
		out <- outcomeRes{o, e}
	}()
	<-el.started // the elicitation is open and blocked inside the episode.
	r := <-out
	if r.err != nil {
		t.Fatalf("ExtendForBudget: %v", r.err)
	}
	if r.outcome != leasecontrol.OutcomePending {
		t.Errorf("outcome = %v, want Pending (in-path deadline with a live reqCtx)", r.outcome)
	}
	// Release the still-open elicitation so the episode goroutine resolves
	// and exits rather than leaking after the test returns.
	close(el.release)
}

// TestExtendForBudgetUnknownSessionIsTerminal_spec_8_6_line_629: an
// unresolvable session cannot be extended, so ExtendForBudget fails
// closed with Terminal and a wrapped error rather than granting.
func TestExtendForBudgetUnknownSessionIsTerminal_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.ExtendForBudget(context.Background(), context.Background(), "ghost")
	if err == nil {
		t.Fatal("ExtendForBudget for an unknown session should error (fail closed)")
	}
	if got != leasecontrol.OutcomeTerminal {
		t.Errorf("outcome = %v, want Terminal for an unknown session", got)
	}
}

// TestExtendForBudgetEmptySessionIsTerminal_spec_8_6_line_629: an empty
// session id is rejected with Terminal and an error; the extension
// entry cannot resolve a tree without it.
func TestExtendForBudgetEmptySessionIsTerminal_spec_8_6_line_629(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.ExtendForBudget(context.Background(), context.Background(), "")
	if err == nil {
		t.Fatal("ExtendForBudget with an empty session id should error")
	}
	if got != leasecontrol.OutcomeTerminal {
		t.Errorf("outcome = %v, want Terminal for an empty session id", got)
	}
}

// TestOutcomeString_spec_8_6_line_629: the tri-state Outcome renders its
// spec-facing name so logs and diagnostics read the extension decision.
// An out-of-range value renders UNKNOWN so a corrupted outcome cannot
// masquerade as a valid one in an operator log.
func TestOutcomeString_spec_8_6_line_629(t *testing.T) {
	cases := map[leasecontrol.Outcome]string{
		leasecontrol.OutcomeGranted:  "GRANTED",
		leasecontrol.OutcomePending:  "PENDING",
		leasecontrol.OutcomeTerminal: "TERMINAL",
		leasecontrol.Outcome(99):     "UNKNOWN",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}

// TestExtendStatusString_spec_8_6_line_743: the §8.6 line 743 extension
// status renders its spec-facing name (GRANTED / PARTIALLY_GRANTED /
// CEILING_REACHED / REJECTED), and the zero value and any out-of-range
// value render UNSPECIFIED so a malformed dispatch status cannot be
// mistaken for a real outcome in a log or audit record.
func TestExtendStatusString_spec_8_6_line_743(t *testing.T) {
	cases := map[leasecontrol.ExtendStatus]string{
		leasecontrol.StatusGranted:          "GRANTED",
		leasecontrol.StatusPartiallyGranted: "PARTIALLY_GRANTED",
		leasecontrol.StatusCeilingReached:   "CEILING_REACHED",
		leasecontrol.StatusRejected:         "REJECTED",
		leasecontrol.StatusUnspecified:      "UNSPECIFIED",
		leasecontrol.ExtendStatus(99):       "UNSPECIFIED",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ExtendStatus(%d).String() = %q, want %q", s, got, want)
		}
	}
}
