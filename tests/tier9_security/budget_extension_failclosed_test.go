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
//   - a missing tenant: the caller threads no tenant, so the dispatch
//     cannot read the session rows and refuses the request.
//   - a foreign tenant: the request carries globex for an acme session,
//     so the per-request row read misses under globex.
//   - a terminal requester: the session row is terminal, so the source
//     returns ErrSessionTerminal and no grant or elicitation follows.
//   - CEILING_REACHED (zero grant against a tree already at its ceiling).
//   - REJECTED (a fresh in-episode user denial): the elicitor returns
//     false for the in-flight request, so ExtendLease resolves
//     StatusRejected and the coordinator marks the subtree denied.
//   - cool-off active (a prior rejection persisted the extension-denied
//     flag and is still within rejectionCoolOff, so the request is
//     auto-rejected before the elicitor is consulted).
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
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// tier9Sessions is the in-memory session reader the store-backed source
// reads its rows from.
type tier9Sessions struct {
	mu   sync.Mutex
	rows map[string]sessionstore.Session
}

func (s *tier9Sessions) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok || r.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return r, nil
}

// grantRecordingSource wraps the store-backed budget source and records
// every ApplyGrant call so the test can assert the ceiling was never
// breached on a grant path and no grant ran at all on a deny path. It is
// the security probe: a silent grant on a deny path, or a grant that
// raised the budget past the effective ceiling, is a leak the recorder
// catches.
type grantRecordingSource struct {
	sessions *tier9Sessions
	denials  *leasecontrol.MemoryDenialStore
	inner    *leasecontrol.StoreBudgetSource

	mu     sync.Mutex
	grants []grantCall
}

type grantCall struct {
	rootSessionID       string
	requestingSessionID string
	granted             leasecontrol.Dimensions
}

func newGrantRecordingSource() *grantRecordingSource {
	s := &grantRecordingSource{
		sessions: &tier9Sessions{rows: map[string]sessionstore.Session{}},
		denials:  leasecontrol.NewMemoryDenialStore(nil),
	}
	s.inner = s.newInner(leasecontrol.ExtensionConfig{})
	return s
}

func (s *grantRecordingSource) newInner(cfg leasecontrol.ExtensionConfig) *leasecontrol.StoreBudgetSource {
	inner, err := leasecontrol.NewStoreBudgetSource(s.sessions, s.denials, cfg)
	if err != nil {
		panic(err)
	}
	return inner
}

// registerRoot writes the root row with a granted token budget of base
// under tenant and serves the tree with cfg.
func (s *grantRecordingSource) registerRoot(root, tenant string, base int64, state session.State, cfg leasecontrol.ExtensionConfig) {
	s.sessions.mu.Lock()
	s.sessions.rows[root] = sessionstore.Session{
		ID: root, TenantID: tenant, RootSessionID: root, State: state,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: base},
	}
	s.sessions.mu.Unlock()
	s.inner = s.newInner(cfg)
}

func (s *grantRecordingSource) TreeBudget(ctx context.Context, tenantID, sessionID string) (leasecontrol.TreeBudget, error) {
	return s.inner.TreeBudget(ctx, tenantID, sessionID)
}

func (s *grantRecordingSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted leasecontrol.Dimensions) (leasecontrol.AppliedGrant, error) {
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

func (s *grantRecordingSource) ApprovalMode(ctx context.Context, tenantID, rootSessionID string) leasecontrol.ApprovalMode {
	return s.inner.ApprovalMode(ctx, tenantID, rootSessionID)
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

// tier9Reclaimer counts the episode's RaiseBudget calls, so a deny path
// can assert the enforcer was never raised.
type tier9Reclaimer struct {
	mu     sync.Mutex
	raises int
}

func (r *tier9Reclaimer) RaiseBudget(string, int64) {
	r.mu.Lock()
	r.raises++
	r.mu.Unlock()
}

func (r *tier9Reclaimer) TerminateSession(string) {}

func (r *tier9Reclaimer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raises
}

// countingElicitor approves every elicitation and counts them, so a deny
// path that must present no elicitation can assert none was presented.
type countingElicitor struct {
	mu    sync.Mutex
	calls int
}

func (e *countingElicitor) Elicit(context.Context, string, string) (bool, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return true, nil
}

func (e *countingElicitor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// tier9AutoElicitor approves every elicitation. It exists so an
// elicitation-mode tree resolves in-path without a human, isolating the
// deny paths under test (transport, ceiling, cool-off, no-session) from
// an elicitation timeout. A silent grant here would still be caught by
// the ApplyGrant recorder.
type tier9AutoElicitor struct{}

func (tier9AutoElicitor) Elicit(context.Context, string, string) (bool, error) { return true, nil }

// tier9RejectElicitor returns (false, nil) for every elicitation: an
// explicit user rejection of the in-flight request. This drives the
// fresh in-episode REJECTED deny path (the coordinator marks the subtree
// extension-denied and ExtendLease resolves StatusRejected), distinct
// from the pre-persisted cool-off state MarkDenied models. §8.6.
type tier9RejectElicitor struct{}

func (tier9RejectElicitor) Elicit(context.Context, string, string) (bool, error) {
	return false, nil
}

// newExtendService builds a leasecontrol.Service over the recording
// source with a background episode context (so no in-path cancellation
// severs the dispatch) and an auto-approving elicitor.
func newExtendService(t *testing.T, src *grantRecordingSource) *leasecontrol.Service {
	t.Helper()
	return newExtendServiceWithElicitor(t, src, tier9AutoElicitor{})
}

// newExtendServiceWithElicitor builds a leasecontrol.Service over the
// recording source with a background episode context and the given
// elicitor, so a case can inject a rejecting elicitor to exercise the
// fresh in-episode REJECTED path.
func newExtendServiceWithElicitor(t *testing.T, src *grantRecordingSource, elicitor leasecontrol.Elicitor) *leasecontrol.Service {
	t.Helper()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        src,
		Elicitor:       elicitor,
		EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// spec: 8.6, 11.2, 8.3
// diagnosis: the §8.6 budget-exhaustion extension failed OPEN on a deny
// path. A missing or foreign tenant, a terminal requester, a tree already
// at its ceiling, a user rejection, an active cool-off, or an unknown
// session must all
// return a non-Granted Outcome and must apply no token grant. A failure
// here means a proxy-mode session that reached its budget kept spending
// tokens without a genuine grant, or the ceiling was raised past the
// §8.6 effective max, either of which lets an over-budget agent overrun
// its allocation. Fail closed: every deny path denies, and only a real
// grant applies a bounded ApplyGrant.
func TestExtensionDenyPathsFailClosed_F866(t *testing.T) {
	const root = "root-1"
	const tenant = "acme"

	headroomCfg := func(mode leasecontrol.ApprovalMode) leasecontrol.ExtensionConfig {
		return leasecontrol.ExtensionConfig{DeploymentBudget: 2_000_000, DeploymentMaxBudget: 4_000_000, ApprovalMode: mode}
	}

	// Each case builds its own tree so the deny reasons do not bleed
	// across cases. tenant is the tenant the caller threads into
	// ExtendForBudget. noElicit marks a case that must present no
	// elicitation at all: the request is refused before consent.
	type denyCase struct {
		name     string
		tenant   string
		noElicit bool
		build    func(t *testing.T, src *grantRecordingSource) string
	}

	cases := []denyCase{
		{
			name: "missing_tenant_dispatch_refused", tenant: "", noElicit: true,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// The tree HAS headroom, so the dispatch would grant if it ran.
				// The caller threads no tenant, so the dispatch refuses it.
				src.registerRoot(root, tenant, 100_000, session.StateRunning, headroomCfg(leasecontrol.ApprovalModeElicitation))
				return root
			},
		},
		{
			name: "foreign_tenant_reads_no_row", tenant: "globex", noElicit: true,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// An acme session with headroom, requested under globex: the
				// per-request row read runs under globex and misses.
				src.registerRoot(root, tenant, 100_000, session.StateRunning, headroomCfg(leasecontrol.ApprovalModeElicitation))
				return root
			},
		},
		{
			name: "terminal_requester_no_grant", tenant: tenant, noElicit: true,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// The tree has headroom but the requesting session is
				// terminal: the source returns ErrSessionTerminal.
				src.registerRoot(root, tenant, 100_000, session.StateCompleted, headroomCfg(leasecontrol.ApprovalModeElicitation))
				return root
			},
		},
		{
			name: "ceiling_reached_zero_headroom", tenant: tenant,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// The tree sits AT its effective ceiling (current == deployment
				// base), so a fresh request grants zero: CEILING_REACHED.
				src.registerRoot(root, tenant, 500_000, session.StateRunning,
					leasecontrol.ExtensionConfig{DeploymentBudget: 500_000, DeploymentMaxBudget: 2_000_000, ApprovalMode: leasecontrol.ApprovalModeAuto})
				return root
			},
		},
		{
			name: "cool_off_active_prior_rejection_persisted", tenant: tenant, noElicit: true,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// A prior user rejection persisted the extension-denied flag;
				// the request is auto-rejected during the cool-off before the
				// elicitor is ever consulted. §8.6. The tree HAS headroom, so a
				// deny here is the active cool-off, not the ceiling.
				src.registerRoot(root, tenant, 100_000, session.StateRunning, headroomCfg(leasecontrol.ApprovalModeElicitation))
				if err := src.Deny(context.Background(), tenant, root, root); err != nil {
					t.Fatalf("Deny: %v", err)
				}
				return root
			},
		},
		{
			name: "no_session_unknown_id", tenant: tenant, noElicit: true,
			build: func(t *testing.T, src *grantRecordingSource) string {
				// No row exists: the session id resolves to nothing.
				return "unknown-session"
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := newGrantRecordingSource()
			sid := tc.build(t, src)
			el := &countingElicitor{}
			svc := newExtendServiceWithElicitor(t, src, el)
			rec := &tier9Reclaimer{}
			svc.SetReclaimer(rec)

			// A generous in-path wait: every case resolves immediately, so a
			// Pending here would itself be a bug (nothing blocks).
			outcome, err := svc.ExtendForBudget(context.Background(), context.Background(), tc.tenant, sid)

			if outcome != leasecontrol.OutcomeTerminal {
				t.Fatalf("deny path %q outcome = %v, want Terminal (fail closed); err=%v", tc.name, outcome, err)
			}
			// The load-bearing security assertion: no token grant was ever
			// applied and the enforcer was never raised on a deny path.
			if g := src.grantsWithPositiveTokens(); len(g) != 0 {
				t.Fatalf("deny path %q applied a positive token grant (fail OPEN): %+v", tc.name, g)
			}
			if n := rec.count(); n != 0 {
				t.Fatalf("deny path %q raised the enforcer %d times (fail OPEN)", tc.name, n)
			}
			if tc.noElicit && el.count() != 0 {
				t.Fatalf("deny path %q presented %d elicitations, want none", tc.name, el.count())
			}
		})
	}

	// The fresh in-episode rejection: elicitation mode, headroom available,
	// no prior denial. The user rejects the in-flight request, so the
	// coordinator marks the subtree denied and ExtendLease resolves
	// REJECTED with no grant.
	t.Run("rejected_fresh_user_denial_in_episode", func(t *testing.T) {
		src := newGrantRecordingSource()
		src.registerRoot(root, tenant, 100_000, session.StateRunning, headroomCfg(leasecontrol.ApprovalModeElicitation))
		svc := newExtendServiceWithElicitor(t, src, tier9RejectElicitor{})
		outcome, err := svc.ExtendForBudget(context.Background(), context.Background(), tenant, root)
		if outcome != leasecontrol.OutcomeTerminal {
			t.Fatalf("rejected outcome = %v, want Terminal; err=%v", outcome, err)
		}
		if g := src.grantsWithPositiveTokens(); len(g) != 0 {
			t.Fatalf("rejected path applied a positive token grant (fail OPEN): %+v", g)
		}
	})

	// The direct dispatch with an empty TenantID is refused before any
	// read, grant, or elicitation.
	t.Run("dispatch_empty_tenant_refused", func(t *testing.T) {
		src := newGrantRecordingSource()
		src.registerRoot(root, tenant, 100_000, session.StateRunning, headroomCfg(leasecontrol.ApprovalModeElicitation))
		el := &countingElicitor{}
		svc := newExtendServiceWithElicitor(t, src, el)
		_, err := svc.ExtendLease(context.Background(), leasecontrol.ExtendRequest{
			SessionID: root, Requested: leasecontrol.Dimensions{Tokens: 1_000},
		})
		if err == nil {
			t.Fatal("ExtendLease with an empty TenantID succeeded (fail OPEN)")
		}
		if len(src.grantsWithPositiveTokens()) != 0 || el.count() != 0 {
			t.Fatalf("empty-tenant dispatch ran a grant or an elicitation: grants=%v elicitations=%d", src.grantsWithPositiveTokens(), el.count())
		}
	})
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
	src.registerRoot(root, tenant, 450_000, session.StateRunning,
		leasecontrol.ExtensionConfig{DeploymentBudget: 500_000, DeploymentMaxBudget: 2_000_000, ApprovalMode: leasecontrol.ApprovalModeAuto})
	svc := newExtendService(t, src)

	// ExtendForBudget requests the token dimension only (as the in-path
	// trigger does). A grant lands, so the outcome is Granted, but the
	// applied grant must not exceed the 50K headroom.
	outcome, err := svc.ExtendForBudget(context.Background(), context.Background(), tenant, root)
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
