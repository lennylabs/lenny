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
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
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
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
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
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_REJECTED {
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
		resp *adapterv1.ExtendLeaseResponse
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
		if r.resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
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
