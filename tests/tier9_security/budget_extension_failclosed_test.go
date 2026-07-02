// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §8.6 budget-exhaustion lease-extension
// trigger's fail-closed contract (proposal 0023, F-8.6.6). The trigger
// moved from the (never-in-path) adapter into the gateway LLM Proxy,
// which calls leasecontrol.ExtendForBudget in-process at the exhaustion
// boundary of a proxy-mode session. Because the extension governs whether
// an over-budget session keeps spending tokens, every path that does not
// resolve to a genuine grant MUST deny: it MUST NOT silently grant tokens,
// MUST NOT raise the budget past the §8.6 ceiling, and MUST NOT loop on a
// single exhaustion event. This is the "fail closed on security-relevant
// paths" rule applied to the token-budget boundary.
//
// The suite enumerates the deny paths §8.6 names:
//   - transport error (the underlying dispatch cannot run): the tenant
//     resolver errors, so ExtendForBudget cannot even locate the tree.
//   - CEILING_REACHED (zero grant against a tree already at its ceiling).
//   - REJECTED (a user denial persisted, cool-off active).
//   - cool-off active (a prior rejection still within rejectionCoolOff).
//   - no-session (an unknown session id).
//
// Each path is driven through the genuine leasecontrol.Service (its real
// grant math, ceiling enforcement, elicitation gate, and cool-off logic)
// wrapped by a BudgetSource that records every ApplyGrant call, so the
// test asserts no grant was ever applied on a deny path and no grant
// exceeded the ceiling on the one path that does grant.
//
// The at-most-once-per-exhaustion bound is exercised through the real
// sessionbudget.Enforcer, whose Record path is where the §8.6 dispatch
// fires. A single exhaustion event that resolves CEILING_REACHED must
// terminate the session (fail closed) and must not re-dispatch the same
// exhaustion, so a runaway agent cannot loop zero-grant extensions while
// the session's own budget stays pinned at the ceiling.
//
// spec: §8.6 (the gateway LLM Proxy drives the budget-exhaustion trigger
// in-process and MUST NOT re-request on CEILING_REACHED/REJECTED),
// §11.2 (a proxy-mode session attempts the extension before termination
// and terminates only on a terminal outcome), §8.3 (the granted slice is
// a soft cap; over-run is settlement-bounded).

package tier9_security_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
)

// grantRecordingSource wraps a MemoryBudgetSource and records every
// ApplyGrant call so the test can assert the ceiling was never breached
// on a grant path and no grant ran at all on a deny path. It is the
// security probe: a silent grant on a deny path, or a grant that raised
// the budget past the effective ceiling, is a leak the recorder catches.
type grantRecordingSource struct {
	inner *leasecontrol.MemoryBudgetSource

	mu     sync.Mutex
	grants []grantCall
}

type grantCall struct {
	rootSessionID       string
	requestingSessionID string
	granted             leasecontrol.Dimensions
}

func newGrantRecordingSource() *grantRecordingSource {
	return &grantRecordingSource{inner: leasecontrol.NewMemoryBudgetSource()}
}

func (s *grantRecordingSource) TreeBudget(ctx context.Context, tenantID, sessionID string) (leasecontrol.TreeBudget, error) {
	return s.inner.TreeBudget(ctx, tenantID, sessionID)
}

func (s *grantRecordingSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted leasecontrol.Dimensions) (leasecontrol.NewLimits, error) {
	s.mu.Lock()
	s.grants = append(s.grants, grantCall{rootSessionID: rootSessionID, requestingSessionID: requestingSessionID, granted: granted})
	s.mu.Unlock()
	return s.inner.ApplyGrant(ctx, tenantID, rootSessionID, requestingSessionID, granted)
}

func (s *grantRecordingSource) RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	return s.inner.RejectionCoolOff(ctx, tenantID, rootSessionID)
}

func (s *grantRecordingSource) Deny(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) error {
	return s.inner.Deny(ctx, tenantID, rootSessionID, requestingSessionID)
}

func (s *grantRecordingSource) TenantOf(ctx context.Context, sessionID string) (string, error) {
	return s.inner.TenantOf(ctx, sessionID)
}

// grantsWithPositiveTokens returns the recorded ApplyGrant calls that
// raised the token budget. A deny path must produce none.
func (s *grantRecordingSource) grantsWithPositiveTokens() []grantCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []grantCall
	for _, g := range s.grants {
		if g.granted.Tokens > 0 {
			out = append(out, g)
		}
	}
	return out
}

// errTenants is a TenantResolver that always errors, standing in for the
// §8.6 "transport fault, no session" path where the dispatch cannot even
// resolve the tree. autoApproveElicitor would grant if it were ever
// reached; it never is, because the resolve fails first.
type errTenants struct{ err error }

func (e errTenants) TenantOf(context.Context, string) (string, error) { return "", e.err }

// tier9AutoElicitor approves every elicitation. It exists so an
// elicitation-mode tree resolves in-path without a human, isolating the
// deny paths under test (transport, ceiling, rejection, cool-off,
// no-session) from an elicitation timeout. A silent grant here would
// still be caught by the ApplyGrant recorder.
type tier9AutoElicitor struct{}

func (tier9AutoElicitor) Elicit(context.Context, string, string) (bool, error) { return true, nil }

// newExtendService builds a leasecontrol.Service over the recording
// source with a background episode context (so no in-path cancellation
// severs the dispatch) and an auto-approving elicitor.
func newExtendService(t *testing.T, src *grantRecordingSource, tenants leasecontrol.TenantResolver) *leasecontrol.Service {
	t.Helper()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        src,
		Tenants:        tenants,
		Elicitor:       tier9AutoElicitor{},
		EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// spec: 8.6, 11.2, 8.3
// diagnosis: the §8.6 budget-exhaustion extension failed OPEN on a deny
// path. A transport error, a tree already at its ceiling, a persisted
// user rejection, an active cool-off, or an unknown session must all
// return a non-Granted Outcome and must apply no token grant. A failure
// here means a proxy-mode session that reached its budget kept spending
// tokens without a genuine grant, or the ceiling was raised past the
// §8.6 effective max, either of which lets an over-budget agent overrun
// its allocation. Fail closed: every deny path denies, and only a real
// grant applies a bounded ApplyGrant.
func TestExtensionDenyPathsFailClosed_F866(t *testing.T) {
	const root = "root-1"
	const tenant = "acme"

	// The tree sits AT its effective ceiling (current == deployment base),
	// so a fresh, extendable request grants zero: CEILING_REACHED. A tree
	// with headroom is registered separately for the rejection/cool-off
	// paths, which must deny for a reason other than the ceiling.
	type denyCase struct {
		name string
		// build returns the service, the recording source, the request
		// context, and the session id the case drives. Each case registers
		// its own tree so the deny reasons do not bleed across cases.
		build func(t *testing.T) (*leasecontrol.Service, *grantRecordingSource, string)
	}

	cases := []denyCase{
		{
			name: "transport_error_no_tree_resolvable",
			build: func(t *testing.T) (*leasecontrol.Service, *grantRecordingSource, string) {
				src := newGrantRecordingSource()
				// The tree HAS headroom, so if the dispatch ever ran it would
				// grant. It never runs: the tenant resolver errors first, the
				// §8.6 "transport fault" fail-closed path.
				src.inner.RegisterTree(root, leasecontrol.TreeConfig{
					TenantID: tenant, CurrentTokenBudget: 100_000,
					DeploymentBase: 2_000_000, DeploymentMax: 4_000_000,
					ApprovalMode: leasecontrol.ApprovalModeAuto,
				})
				svc := newExtendService(t, src, errTenants{err: errors.New("gateway control channel unreachable")})
				return svc, src, root
			},
		},
		{
			name: "ceiling_reached_zero_headroom",
			build: func(t *testing.T) (*leasecontrol.Service, *grantRecordingSource, string) {
				src := newGrantRecordingSource()
				src.inner.RegisterTree(root, leasecontrol.TreeConfig{
					TenantID: tenant, CurrentTokenBudget: 500_000,
					DeploymentBase: 500_000, DeploymentMax: 2_000_000,
					ApprovalMode: leasecontrol.ApprovalModeAuto,
				})
				svc := newExtendService(t, src, src)
				return svc, src, root
			},
		},
		{
			name: "rejected_extension_denied_flag_set",
			build: func(t *testing.T) (*leasecontrol.Service, *grantRecordingSource, string) {
				src := newGrantRecordingSource()
				src.inner.RegisterTree(root, leasecontrol.TreeConfig{
					TenantID: tenant, CurrentTokenBudget: 100_000,
					DeploymentBase: 2_000_000, DeploymentMax: 4_000_000,
					ApprovalMode: leasecontrol.ApprovalModeElicitation,
				})
				// A prior user rejection persisted the extension-denied flag;
				// the request is auto-rejected during the cool-off. §8.6 line
				// 729. The tree HAS headroom, so a deny here is the rejection,
				// not the ceiling.
				src.inner.MarkDenied(root)
				svc := newExtendService(t, src, src)
				return svc, src, root
			},
		},
		{
			name: "no_session_unknown_id",
			build: func(t *testing.T) (*leasecontrol.Service, *grantRecordingSource, string) {
				src := newGrantRecordingSource()
				// No tree registered: the session id resolves to nothing.
				svc := newExtendService(t, src, src)
				return svc, src, "unknown-session"
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc, src, sid := tc.build(t)

			reqCtx := context.Background()
			// A generous in-path wait: auto/rejection/ceiling resolve
			// immediately, so a Pending here would itself be a bug (nothing
			// blocks). The deny paths must resolve Terminal, not Pending.
			waitCtx := context.Background()
			outcome, err := svc.ExtendForBudget(reqCtx, waitCtx, sid)

			// Every deny path resolves Terminal. The transport-error and
			// no-session paths additionally surface an error; the ceiling and
			// rejection paths resolve Terminal with no error (a legitimate
			// non-grant). None resolve Granted.
			if outcome == leasecontrol.OutcomeGranted {
				t.Fatalf("deny path %q resolved Granted (fail OPEN): a non-grant path silently granted tokens; err=%v", tc.name, err)
			}
			if outcome == leasecontrol.OutcomePending {
				t.Fatalf("deny path %q resolved Pending: a resolvable deny must be Terminal, not left hanging; err=%v", tc.name, err)
			}
			if outcome != leasecontrol.OutcomeTerminal {
				t.Fatalf("deny path %q outcome = %v, want Terminal (fail closed)", tc.name, outcome)
			}

			// The load-bearing security assertion: no token grant was ever
			// applied on a deny path. The recorder captures every ApplyGrant;
			// a silent grant on a rejected/ceiling/transport/no-session path
			// is a budget leak.
			if g := src.grantsWithPositiveTokens(); len(g) != 0 {
				t.Fatalf("deny path %q applied a positive token grant (fail OPEN): %+v — an over-budget session gained tokens on a deny path", tc.name, g)
			}
		})
	}
}

// spec: 8.6, 8.3
// diagnosis: a §8.6 extension raised the budget PAST the effective
// ceiling. A grant is bounded by min(layered effective max, parent
// lease); a grant that lands more than the headroom breaches the §8.6
// ceiling and lets a session spend beyond its deployer/tenant cap. A
// failure here means the ceiling math is unwired or the grant is not
// clamped, so an over-budget agent can extend past its allocation.
func TestExtensionGrantNeverExceedsCeiling_F866(t *testing.T) {
	const root = "root-1"
	const tenant = "acme"

	src := newGrantRecordingSource()
	// Effective max resolves to the deployment base (500K); current is
	// 450K, so headroom is 50K. A request for far more than the headroom
	// must be CAPPED to the 50K headroom (PARTIALLY_GRANTED), never
	// granted in full.
	src.inner.RegisterTree(root, leasecontrol.TreeConfig{
		TenantID: tenant, CurrentTokenBudget: 450_000,
		DeploymentBase: 500_000, DeploymentMax: 2_000_000,
		ApprovalMode: leasecontrol.ApprovalModeAuto,
	})
	svc := newExtendService(t, src, src)

	// ExtendForBudget requests the token dimension only (as the in-path
	// trigger does). A grant lands, so the outcome is Granted, but the
	// applied grant must not exceed the 50K headroom.
	outcome, err := svc.ExtendForBudget(context.Background(), context.Background(), root)
	if err != nil {
		t.Fatalf("ExtendForBudget: %v", err)
	}
	if outcome != leasecontrol.OutcomeGranted {
		t.Fatalf("outcome = %v, want Granted (a capped grant still recovers budget)", outcome)
	}

	// The recorded grant is clamped to the headroom: the ceiling is never
	// breached. A grant above 50K would raise the budget past the §8.6
	// effective max.
	const headroom = 50_000
	grants := src.grantsWithPositiveTokens()
	if len(grants) == 0 {
		t.Fatalf("expected one bounded ApplyGrant, got none")
	}
	for _, g := range grants {
		if g.granted.Tokens > headroom {
			t.Fatalf("ApplyGrant raised the budget by %d tokens, past the %d headroom (ceiling breached): %+v", g.granted.Tokens, headroom, g)
		}
	}

	// The tree budget after the grant is at (not above) the effective max.
	tb, err := src.TreeBudget(context.Background(), tenant, root)
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if tb.Current.Tokens > 500_000 {
		t.Fatalf("post-grant budget = %d, past the 500000 effective ceiling", tb.Current.Tokens)
	}
}

// terminatorSpy records TerminateSession calls so a test can assert a
// fail-closed terminal outcome tore the session down exactly once.
type terminatorSpy struct {
	mu    sync.Mutex
	calls []string
}

func (s *terminatorSpy) TerminateSession(sessionID, _ /*reason*/ string) {
	s.mu.Lock()
	s.calls = append(s.calls, sessionID)
	s.mu.Unlock()
}

func (s *terminatorSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// spec: 8.6, 11.2
// diagnosis: a single budget exhaustion looped the §8.6 extension. §8.6
// requires the gateway LLM Proxy to attempt the extension AT MOST ONCE
// per distinct exhaustion event and to terminate (not re-request) on
// CEILING_REACHED. A failure here means one exhaustion re-dispatched the
// extension repeatedly — the infinite-retry loop §8.6 warns of (each
// request returns zero grant, each call fails, the proxy requests again)
// — or a session that hit the ceiling was NOT terminated, so an
// over-budget agent kept issuing requests. The bound holds: one dispatch
// per exhaustion, and a ceiling-reached session terminates.
func TestExhaustionExtensionAtMostOncePerEvent_CeilingTerminates_F866(t *testing.T) {
	const sid = "sess-1"
	const tenant = "acme"

	// Count how many times the exhaustion seam is invoked for one
	// exhaustion event, and what it returned. The seam stands in for the
	// leasecontrol dispatch the enforcer's Record path calls at the
	// exhaustion boundary; a Terminal outcome models CEILING_REACHED.
	var seamCalls int
	var seamMu sync.Mutex
	seam := func(_, _ context.Context, _ /*tenantID*/, _ /*sessionID*/ string, _, _ int64) sessionbudget.Outcome {
		seamMu.Lock()
		seamCalls++
		seamMu.Unlock()
		return sessionbudget.Terminal // CEILING_REACHED / REJECTED
	}

	term := &terminatorSpy{}
	enf := sessionbudget.New(term, seam, nil)

	reqCtx := context.Background()
	waitCtx := context.Background()

	// First Record crosses the exhaustion boundary (consumed == budget):
	// the seam fires exactly once, resolves Terminal, and the session is
	// denied and terminated.
	exhausted, outcome := enf.Record(reqCtx, waitCtx, tenant, sid, 1000, 1000)
	if !exhausted {
		t.Fatalf("first Record did not report exhaustion at consumed==budget")
	}
	if outcome != sessionbudget.Terminal {
		t.Fatalf("first exhaustion outcome = %v, want Terminal", outcome)
	}

	// The pre-flight gate now denies the session: it terminated on the
	// ceiling. §8.6 / §11.2.
	if enf.Allow(sid) {
		t.Fatalf("session admitted after a CEILING_REACHED termination (fail OPEN): the over-budget session was not gated")
	}
	if term.count() != 1 {
		t.Fatalf("TerminateSession called %d times, want exactly 1 on a terminal exhaustion", term.count())
	}

	// A subsequent Record for the SAME exhaustion (further over-budget
	// tokens, the session not raised) must NOT re-dispatch the seam: the
	// exhaustion is already seen, so the extension is not re-requested.
	// This is the §8.6 no-loop bound — a ceiling-reached session cannot
	// spin the extension.
	exhausted2, _ := enf.Record(reqCtx, waitCtx, tenant, sid, 1000, 500)
	if exhausted2 {
		t.Fatalf("a repeat over-budget Record re-reported exhaustion (would re-dispatch the extension): §8.6 at-most-once bound violated")
	}

	seamMu.Lock()
	got := seamCalls
	seamMu.Unlock()
	if got != 1 {
		t.Fatalf("extension seam invoked %d times for one exhaustion event, want exactly 1 (§8.6 no-loop bound): a single exhaustion looped the extension", got)
	}
}
