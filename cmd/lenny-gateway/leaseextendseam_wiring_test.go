// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// These tests pin the proposal 0023 S6 wiring seam that connects the §11.2
// sessionbudget enforcer to the §8.6 leasecontrol.Service in-process. Before
// this step the composition root built the enforcer with a nil extension seam
// and never called SetReclaimer, so a proxy-mode session that exhausted its
// token budget was terminated immediately — the §8.6-vs-§11.2 collision the
// proposal reconciles. leaseExtendSeam(svc) adapts Service.ExtendForBudget into
// the enforcer's ExtendOnExhaustion, mapping the leasecontrol tri-state onto
// the enforcer's own, and buildControlServer wires it via
// SetExtendOnExhaustion plus SetReclaimer(enforcer).
//
// A regression that reverts the wiring (nil seam, no reclaimer) fails
// TestLeaseExtendSeamGrantedContinuesSession: with no seam the enforcer's
// exhaustion path is Terminal and it terminates the session rather than
// attempting the §8.6 extension.
//
// spec: §8.6; §11.2; proposal 0023 S6.

// recordingTerminator records the sessions the enforcer terminates so a test
// can assert that a granted extension leaves the session alive.
type recordingTerminator struct {
	terminated []string
}

func (r *recordingTerminator) TerminateSession(sessionID, _ /*reason*/ string) {
	r.terminated = append(r.terminated, sessionID)
}

// storeBackedExtendService builds a real leasecontrol.Service over the
// store-backed budget source: a memstore session store holding the root row
// rootSessionID under acme, and a MemoryDenialStore. Auto mode resolves an
// extension synchronously with no elicitor, so ExtendForBudget grants up to
// the ceiling within the caller's in-path wait. When approvalMode is
// elicitation and no elicitor is wired the extension fails closed (no way to
// obtain consent), which the seam maps to Terminal.
func storeBackedExtendService(t *testing.T, rootSessionID string, mode leasecontrol.ApprovalMode, el leasecontrol.Elicitor) *leasecontrol.Service {
	t.Helper()
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: rootSessionID, TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	budgets, err := leasecontrol.NewStoreBudgetSource(sessions, leasecontrol.NewMemoryDenialStore(nil), leasecontrol.ExtensionConfig{
		DeploymentBudget:    500_000,
		DeploymentMaxBudget: 2_000_000,
		ApprovalMode:        mode,
	})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Elicitor: el})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// emptyLeaseBudgets returns a store-backed budget source over an empty
// session store, for wiring tests that need a BudgetSource but resolve no
// session through it.
func emptyLeaseBudgets(t *testing.T) *leasecontrol.StoreBudgetSource {
	t.Helper()
	budgets, err := leasecontrol.NewStoreBudgetSource(memstore.New(), leasecontrol.NewMemoryDenialStore(nil), leasecontrol.ExtensionConfig{})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	return budgets
}

// TestLeaseExtendSeamGrantedContinuesSession proves the S6 wiring drives the
// §8.6 extension in-process: an auto-mode tree with headroom under its ceiling
// grants the extension, so the enforcer raises the session's budget and leaves
// it alive rather than terminating it. The pre-fix composition root passed a
// nil seam, so this exhaustion would have been Terminal and the session
// terminated — this test would fail against that code.
func TestLeaseExtendSeamGrantedContinuesSession_spec_8_6_line_629(t *testing.T) {
	svc := storeBackedExtendService(t, "s_grant", leasecontrol.ApprovalModeAuto, nil)
	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	// Wire exactly as buildControlServer does.
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)

	ctx := context.Background()
	// Budget 200; record 250 tokens so the session exhausts on this call and
	// the enforcer consults the seam.
	exhausted, outcome := enforcer.Record(ctx, ctx, "acme", "s_grant", 200, 250)
	if !exhausted {
		t.Fatalf("recording 250 tokens against a 200 budget must cross the exhaustion boundary")
	}
	if outcome != sessionbudget.Granted {
		t.Fatalf("an auto-mode extension with headroom must resolve Granted, got %v", outcome)
	}
	if len(term.terminated) != 0 {
		t.Fatalf("a granted extension must not terminate the session, terminated=%v", term.terminated)
	}
	// The seam raised the budget and cleared the deny flag; the session's next
	// request is admitted by the pre-flight gate.
	if !enforcer.Allow("s_grant") {
		t.Fatalf("a granted-extension session must be admitted by the pre-flight gate")
	}

	// The in-path grant must have actually cleared the enforcer's exhausted
	// flag through RaiseBudget, not merely left the deny flag unset. Prove it:
	// a later settlement that crosses the raised budget is a NEW exhaustion
	// event that re-consults the seam. Against the pre-fix code the in-path
	// Granted path never raised the enforcer, so c.exhausted stayed true and
	// this second boundary crossing would never fire (the !c.exhausted guard
	// suppresses it), leaving the session running under an
	// effectively-unenforced budget. A fresh exhaustion here (exhausted2 true)
	// therefore proves the in-path grant cleared the exhausted flag. The second
	// Record passes a raised budget the recorder would compute (base plus the
	// granted delta); recording just over it re-crosses the boundary.
	exhausted2, _ := enforcer.Record(ctx, ctx, "acme", "s_grant", 100_000, 100_001)
	if !exhausted2 {
		t.Fatalf("exhausting the raised budget must be a fresh exhaustion event; the in-path grant must have cleared c.exhausted (pre-fix left it set, suppressing this boundary)")
	}
}

// TestLeaseExtendSeamTerminalFailsClosed proves the wired seam fails closed:
// an elicitation-mode tree with no elicitor cannot obtain consent, so
// ExtendForBudget errors, the seam maps that to Terminal, and the enforcer
// denies and terminates the session. This distinguishes the wired-and-denied
// path from the wired-and-granted path, so the Granted test above is not
// trivially satisfied by a stub that always grants.
func TestLeaseExtendSeamTerminalFailsClosed_spec_8_6_line_712(t *testing.T) {
	svc := storeBackedExtendService(t, "s_deny", leasecontrol.ApprovalModeElicitation, nil)
	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)

	ctx := context.Background()
	exhausted, outcome := enforcer.Record(ctx, ctx, "acme", "s_deny", 200, 250)
	if !exhausted {
		t.Fatalf("recording 250 tokens against a 200 budget must cross the exhaustion boundary")
	}
	if outcome != sessionbudget.Terminal {
		t.Fatalf("an elicitation-mode extension with no elicitor must fail closed to Terminal, got %v", outcome)
	}
	if len(term.terminated) != 1 || term.terminated[0] != "s_deny" {
		t.Fatalf("a terminal extension must terminate the session, terminated=%v", term.terminated)
	}
	if enforcer.Allow("s_deny") {
		t.Fatalf("a terminated session must be denied by the pre-flight gate")
	}
}

// blockingElicitor holds every elicitation until release is closed, then
// returns the scripted approval. It lets a test drive the in-path wait past
// its deadline while the elicitation is still unresolved (the Pending path).
type blockingElicitor struct {
	release chan struct{}
	approve bool
}

func (e *blockingElicitor) Elicit(ctx context.Context, _ /*tenantID*/, _ /*sessionID*/ string) (bool, error) {
	select {
	case <-e.release:
		return e.approve, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// TestLeaseExtendSeamPendingDeniesWithoutTerminating proves the seam maps a
// still-pending elicitation (the in-path wait deadline elapsed while the
// episode is unresolved) onto sessionbudget.Pending: the enforcer denies the
// session's next request but does NOT terminate it, and when the out-of-band
// episode later grants, the fan-out reclaims the detached session through the
// SessionReclaimer (the enforcer) so its budget is raised and its next request
// is admitted. This covers the Pending mapping and the SetReclaimer fan-out
// that the buildControlServer wiring installs.
func TestLeaseExtendSeamPendingDeniesWithoutTerminating_spec_8_6_line_629(t *testing.T) {
	el := &blockingElicitor{release: make(chan struct{}), approve: true}
	// An elicitation-mode tree with a blocking elicitor: the elicitation stays
	// unresolved until the test releases it, so the in-path wait deadline
	// elapses first and ExtendForBudget returns Pending.
	svc := storeBackedExtendService(t, "s_pend", leasecontrol.ApprovalModeElicitation, el)
	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)

	// Both contexts are the same short-lived context, so the in-path wait
	// elapses while the elicitation is still blocked: ExtendForBudget returns
	// Pending (reqCtx is not cancelled by the caller; only the derived wait
	// deadline fired). The seam threads reqCtx and waitCtx as one here, which
	// is the Pending discrimination the two-context path draws on.
	waitCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	exhausted, outcome := enforcer.Record(context.Background(), waitCtx, "acme", "s_pend", 200, 250)
	if !exhausted {
		t.Fatalf("recording 250 tokens against a 200 budget must cross the exhaustion boundary")
	}
	if outcome != sessionbudget.Pending {
		t.Fatalf("a still-pending elicitation at the in-path deadline must resolve Pending, got %v", outcome)
	}
	if len(term.terminated) != 0 {
		t.Fatalf("a Pending outcome must NOT terminate the session, terminated=%v", term.terminated)
	}
	if enforcer.Allow("s_pend") {
		t.Fatalf("a Pending session must be denied per request until the episode resolves")
	}

	// Release the elicitation: the episode resolves GRANTED out-of-band and its
	// fan-out reclaims the detached session through the reclaimer (RaiseBudget),
	// clearing the deny flag so the next request is admitted.
	close(el.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if enforcer.Allow("s_pend") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the episode fan-out must reclaim the detached session and clear its deny flag via RaiseBudget")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(term.terminated) != 0 {
		t.Fatalf("a granted out-of-band resolution must not terminate the session, terminated=%v", term.terminated)
	}
}

// TestLeaseExtendSeamThreadsTenant_spec_8_6 proves the seam threads the
// tenant the enforcer holds into ExtendRequest.TenantID: the source reads the
// session rows under that tenant, so the acme session is granted under acme,
// and the same session id recorded under globex finds no row and fails
// closed to Terminal with no grant.
//
// spec: §8.6 (Extension state)
func TestLeaseExtendSeamThreadsTenant_spec_8_6(t *testing.T) {
	svc := storeBackedExtendService(t, "s_tenant", leasecontrol.ApprovalModeAuto, nil)
	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)
	ctx := context.Background()

	if _, outcome := enforcer.Record(ctx, ctx, "globex", "s_tenant", 200, 250); outcome != sessionbudget.Terminal {
		t.Fatalf("a session recorded under another tenant must fail closed, got %v", outcome)
	}
	if len(term.terminated) != 1 {
		t.Fatalf("the cross-tenant exhaustion must terminate, terminated=%v", term.terminated)
	}

	svc2 := storeBackedExtendService(t, "s_tenant2", leasecontrol.ApprovalModeAuto, nil)
	enforcer2 := sessionbudget.New(&recordingTerminator{}, nil, nil)
	enforcer2.SetExtendOnExhaustion(leaseExtendSeam(svc2))
	svc2.SetReclaimer(enforcer2)
	if _, outcome := enforcer2.Record(ctx, ctx, "acme", "s_tenant2", 200, 250); outcome != sessionbudget.Granted {
		t.Fatalf("the session recorded under its own tenant must be granted, got %v", outcome)
	}
}

// TestLeaseExtendSeamNilOnLocalDevPath documents the local-development posture
// the buildControlServer nil-guard preserves: without a leasecontrol.Service
// (no --grpc-addr) the composition root never calls SetExtendOnExhaustion, so
// the enforcer keeps its nil seam and terminates immediately on exhaustion, the
// §11.2 behavior. This is the fail-closed default a missing seam must
// not weaken.
func TestLeaseExtendSeamNilOnLocalDevPath_spec_11_2_line_44(t *testing.T) {
	term := &recordingTerminator{}
	// New with a nil seam is exactly the sessiondeps.go posture before the
	// control-server wiring runs, and the posture that stands when no
	// GatewayControl listener is enabled.
	enforcer := sessionbudget.New(term, nil, nil)

	ctx := context.Background()
	exhausted, outcome := enforcer.Record(ctx, ctx, "acme", "s_local", 200, 250)
	if !exhausted {
		t.Fatalf("recording 250 tokens against a 200 budget must cross the exhaustion boundary")
	}
	if outcome != sessionbudget.Terminal {
		t.Fatalf("a nil seam must fail closed to Terminal, got %v", outcome)
	}
	if len(term.terminated) != 1 || term.terminated[0] != "s_local" {
		t.Fatalf("the nil-seam path must terminate immediately (§11.2), terminated=%v", term.terminated)
	}
}
