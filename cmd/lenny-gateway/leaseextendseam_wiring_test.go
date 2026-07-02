// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
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
// spec: §8.6 line 629; §11.2 line 44; proposal 0023 S6.

// recordingTerminator records the sessions the enforcer terminates so a test
// can assert that a granted extension leaves the session alive.
type recordingTerminator struct {
	terminated []string
}

func (r *recordingTerminator) TerminateSession(sessionID, _ /*reason*/ string) {
	r.terminated = append(r.terminated, sessionID)
}

// autoExtendService builds a real leasecontrol.Service in auto mode with a
// single registered tree keyed on rootSessionID, and returns the service and
// its budget source. Auto mode resolves an extension synchronously with no
// elicitor, so ExtendForBudget grants up to the ceiling within the caller's
// in-path wait. When approvalMode is elicitation and no elicitor is wired the
// extension fails closed (no way to obtain consent), which the seam maps to
// Terminal.
func autoExtendService(t *testing.T, rootSessionID string, mode leasecontrol.ApprovalMode) (*leasecontrol.Service, *leasecontrol.MemoryBudgetSource) {
	t.Helper()
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree(rootSessionID, leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       mode,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets,
		Tenants: budgets,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, budgets
}

// TestLeaseExtendSeamGrantedContinuesSession proves the S6 wiring drives the
// §8.6 extension in-process: an auto-mode tree with headroom under its ceiling
// grants the extension, so the enforcer raises the session's budget and leaves
// it alive rather than terminating it. The pre-fix composition root passed a
// nil seam, so this exhaustion would have been Terminal and the session
// terminated — this test would fail against that code.
func TestLeaseExtendSeamGrantedContinuesSession_spec_8_6_line_629(t *testing.T) {
	svc, _ := autoExtendService(t, "s_grant", leasecontrol.ApprovalModeAuto)
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
}

// TestLeaseExtendSeamTerminalFailsClosed proves the wired seam fails closed:
// an elicitation-mode tree with no elicitor cannot obtain consent, so
// ExtendForBudget errors, the seam maps that to Terminal, and the enforcer
// denies and terminates the session. This distinguishes the wired-and-denied
// path from the wired-and-granted path, so the Granted test above is not
// trivially satisfied by a stub that always grants.
func TestLeaseExtendSeamTerminalFailsClosed_spec_8_6_line_712(t *testing.T) {
	svc, _ := autoExtendService(t, "s_deny", leasecontrol.ApprovalModeElicitation)
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
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("s_pend", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets, Elicitor: el})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
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

// TestLeaseExtendSeamNilOnLocalDevPath documents the local-development posture
// the buildControlServer nil-guard preserves: without a leasecontrol.Service
// (no --grpc-addr) the composition root never calls SetExtendOnExhaustion, so
// the enforcer keeps its nil seam and terminates immediately on exhaustion, the
// §11.2 line 44 behavior. This is the fail-closed default a missing seam must
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
		t.Fatalf("the nil-seam path must terminate immediately (§11.2 line 44), terminated=%v", term.terminated)
	}
}
