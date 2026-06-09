// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fixedClock returns a deterministic clock for cool-off tests.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newService builds a Service over a MemoryBudgetSource, with the
// budget source serving as the TenantResolver too.
func newService(t *testing.T, budgets *leasecontrol.MemoryBudgetSource, clock func() time.Time) *leasecontrol.Service {
	t.Helper()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets,
		Tenants: budgets,
		Clock:   clock,
		// Trees default to elicitation mode (§8.6 line 674); the
		// auto-approving elicitor lets the grant-math tests exercise the
		// consent path without a real client. F-8.6.2.
		Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func extendReq(sessionID string, tokens int64) *adapterv1.ExtendLeaseRequest {
	return &adapterv1.ExtendLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		RequestedTokens: tokens,
	}
}

// TestExtendLeaseGranted: a request that fits under the ceiling is
// granted in full and raises the tree budget.
func TestExtendLeaseGranted(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED", resp.Status)
	}
	if resp.GrantedTokens != 200_000 {
		t.Errorf("granted = %d, want 200000", resp.GrantedTokens)
	}

	// The budget rose by the granted amount; a second look confirms it.
	tb, err := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if tb.Current.Tokens != 300_000 {
		t.Errorf("current budget = %d, want 300000", tb.Current.Tokens)
	}
}

// TestExtendLeasePartiallyGranted: a request that exceeds the remaining
// headroom is capped to the headroom and reported PARTIALLY_GRANTED.
func TestExtendLeasePartiallyGranted(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 450_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)

	// Effective max resolves to the deployment base 500K; headroom is
	// 50K. A 200K request is capped to 50K.
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED {
		t.Errorf("status = %v, want PARTIALLY_GRANTED", resp.Status)
	}
	if resp.GrantedTokens != 50_000 {
		t.Errorf("granted = %d, want 50000", resp.GrantedTokens)
	}
}

// TestExtendLeaseCeilingReached: a request against a tree already at
// the ceiling is granted zero and reported CEILING_REACHED.
func TestExtendLeaseCeilingReached(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 500_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 100_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED {
		t.Errorf("status = %v, want CEILING_REACHED", resp.Status)
	}
	if resp.GrantedTokens != 0 {
		t.Errorf("granted = %d, want 0", resp.GrantedTokens)
	}
}

// TestExtendLeaseLayeredCeiling: the tenant ceiling caps the effective
// max below the deployment max, so a grant against a high deployment
// base is bounded by the tenant ceiling.
func TestExtendLeaseLayeredCeiling(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	// §8.6 example row: runtime sets 2.5M, tenant max 1M, deployment
	// max 2M. The effective max resolves to the tenant ceiling, 1M.
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 900_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		TenantMax:          1_000_000,
		RuntimeBase:        2_500_000,
	})
	svc := newService(t, budgets, nil)

	// Headroom against the 1M tenant ceiling is 100K; a 400K request is
	// capped to 100K.
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 400_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED {
		t.Errorf("status = %v, want PARTIALLY_GRANTED", resp.Status)
	}
	if resp.GrantedTokens != 100_000 {
		t.Errorf("granted = %d, want 100000 (capped by tenant ceiling)", resp.GrantedTokens)
	}
}

// TestExtendLeaseRejectedDuringCoolOff: when the subtree is
// extension-denied and the cool-off has not expired, the request is
// auto-rejected and the response carries the cool-off expiry.
func TestExtendLeaseRejectedDuringCoolOff(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, clock)

	// A user rejection sets the extension-denied flag and starts the
	// default 300s cool-off.
	budgets.MarkDenied("root-1")

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_REJECTED {
		t.Errorf("status = %v, want REJECTED", resp.Status)
	}
	if resp.GrantedTokens != 0 {
		t.Errorf("granted = %d, want 0", resp.GrantedTokens)
	}
	wantExpiry := now.Add(leasecontrol.DefaultRejectionCoolOff).UnixMilli()
	if resp.CoolOffExpiryUnixMs != wantExpiry {
		t.Errorf("cool-off expiry = %d, want %d", resp.CoolOffExpiryUnixMs, wantExpiry)
	}
	// spec: §15.1 line 1080 — REJECTED carries details.subtreeId and
	// details.coolOffExpiresAt (UTC RFC 3339) for the denied subtree.
	if resp.SubtreeId != "root-1" {
		t.Errorf("subtree_id = %q, want %q", resp.SubtreeId, "root-1")
	}
	wantISO := now.Add(leasecontrol.DefaultRejectionCoolOff).UTC().Format(time.RFC3339)
	if resp.CoolOffExpiresAt != wantISO {
		t.Errorf("cool_off_expires_at = %q, want %q", resp.CoolOffExpiresAt, wantISO)
	}

	// The denied request must not raise the budget.
	tb, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if tb.Current.Tokens != 100_000 {
		t.Errorf("current budget = %d, want 100000 (rejection must not grant)", tb.Current.Tokens)
	}
}

// TestExtendLeaseRejectedResponseCarriesSpec15ErrorDetails_spec_15_1_line_1080:
// an in-flight denial (the §8.6 atomic re-check converting the outcome
// to REJECTED) also carries the §15.1 details.subtreeId and
// details.coolOffExpiresAt fields.
func TestExtendLeaseRejectedResponseCarriesSpec15ErrorDetails_spec_15_1_line_1080(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	base := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	base.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		RejectionCoolOff:   90 * time.Second,
	})
	base.AddSession("child-2", "root-1", "acme")
	racing := &raceBudgetSource{inner: base, denyOnApply: true}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: racing, Tenants: base, Clock: clock, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	resp, err := svc.ExtendLease(context.Background(), extendReq("child-2", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_REJECTED {
		t.Fatalf("status = %v, want REJECTED", resp.Status)
	}
	// The subtree id is the requesting session, not the tree root.
	if resp.SubtreeId != "child-2" {
		t.Errorf("subtree_id = %q, want %q", resp.SubtreeId, "child-2")
	}
	wantISO := now.Add(90 * time.Second).UTC().Format(time.RFC3339)
	if resp.CoolOffExpiresAt != wantISO {
		t.Errorf("cool_off_expires_at = %q, want %q", resp.CoolOffExpiresAt, wantISO)
	}
	// Unix-ms cool-off mirror is preserved for legacy clients.
	wantUnixMs := now.Add(90 * time.Second).UnixMilli()
	if resp.CoolOffExpiryUnixMs != wantUnixMs {
		t.Errorf("cool_off_expiry_unix_ms = %d, want %d", resp.CoolOffExpiryUnixMs, wantUnixMs)
	}
}

// TestExtendLeaseGrantedAfterCoolOffExpiry: once the rejection cool-off
// has elapsed, a request is served normally again.
func TestExtendLeaseGrantedAfterCoolOffExpiry(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clk := &movableClock{t: now}
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clk.now)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		RejectionCoolOff:   60 * time.Second,
	})
	svc := newService(t, budgets, clk.now)

	budgets.MarkDenied("root-1")

	// Still inside the 60s cool-off: rejected.
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 100_000))
	if err != nil {
		t.Fatalf("ExtendLease (in cool-off): %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_REJECTED {
		t.Fatalf("status = %v, want REJECTED inside cool-off", resp.Status)
	}

	// Advance past the cool-off; the next request is granted.
	clk.advance(61 * time.Second)
	resp, err = svc.ExtendLease(context.Background(), extendReq("root-1", 100_000))
	if err != nil {
		t.Fatalf("ExtendLease (after cool-off): %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED after cool-off expiry", resp.Status)
	}
	if resp.GrantedTokens != 100_000 {
		t.Errorf("granted = %d, want 100000", resp.GrantedTokens)
	}
}

// TestExtendLeaseClearDenialEndsCoolOff: clearing the extension-denied
// flag (the §8.6 admin reset) restores normal extension behavior even
// before the cool-off elapses.
func TestExtendLeaseClearDenialEndsCoolOff(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, clock)

	budgets.MarkDenied("root-1")
	budgets.ClearDenial("root-1")

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 100_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED after denial cleared", resp.Status)
	}
}

// TestExtendLeaseChildSessionResolvesTree: an extension request from a
// delegated child session resolves the right tree. Per §8.6 line
// 737-741, the grant applies to the requesting session only — the
// child's view raises while the root's view stays at the base. The
// previous implementation bumped the tree-wide counter, violating the
// scope-isolation contract; F-8.6.12 corrected it. The new assertion
// pins both views so a regression that re-introduces tree-wide
// propagation fails loudly.
// spec: §8.6 line 737-741; F-8.6.12.
func TestExtendLeaseChildSessionResolvesTree(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	budgets.AddSession("child-7", "root-1", "acme")
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("child-7", 50_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED", resp.Status)
	}
	childView, _ := budgets.TreeBudget(context.Background(), "acme", "child-7")
	if childView.Current.Tokens != 150_000 {
		t.Errorf("child view = %d, want 150000 — the requester sees its own bump", childView.Current.Tokens)
	}
	rootView, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if rootView.Current.Tokens != 100_000 {
		t.Errorf("root view = %d, want 100000 — extensions apply to the requesting session only (§8.6 line 737)",
			rootView.Current.Tokens)
	}
}

// TestExtendLeaseExtensionScopedToRequestingSession_spec_8_6_line_737:
// directly anchors §8.6 line 737-741. Two siblings extend
// independently; each sibling's view of the tree budget rises only by
// its own grant, and a third "existing" sibling that did not extend
// continues to see the unchanged base. F-8.6.12.
func TestExtendLeaseExtensionScopedToRequestingSession_spec_8_6_line_737(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000,
		DeploymentMax:      5_000_000,
	})
	budgets.AddSession("sib-a", "root", "acme")
	budgets.AddSession("sib-b", "root", "acme")
	budgets.AddSession("sib-existing", "root", "acme")
	svc := newService(t, budgets, nil)

	if _, err := svc.ExtendLease(context.Background(), extendReq("sib-a", 50_000)); err != nil {
		t.Fatalf("sib-a ExtendLease: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("sib-b", 200_000)); err != nil {
		t.Fatalf("sib-b ExtendLease: %v", err)
	}

	ab, _ := budgets.TreeBudget(context.Background(), "acme", "sib-a")
	bb, _ := budgets.TreeBudget(context.Background(), "acme", "sib-b")
	eb, _ := budgets.TreeBudget(context.Background(), "acme", "sib-existing")
	if ab.Current.Tokens != 150_000 {
		t.Errorf("sib-a view = %d, want 150000 (base + own delta)", ab.Current.Tokens)
	}
	if bb.Current.Tokens != 300_000 {
		t.Errorf("sib-b view = %d, want 300000 (base + own delta)", bb.Current.Tokens)
	}
	if eb.Current.Tokens != 100_000 {
		t.Errorf("sib-existing view = %d, want 100000 — sibling extensions must not bleed through", eb.Current.Tokens)
	}
}

// TestExtendLeaseUnknownSession: a request for a session the gateway
// does not know returns an error rather than a grant.
func TestExtendLeaseUnknownSession(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc := newService(t, budgets, nil)

	_, err := svc.ExtendLease(context.Background(), extendReq("ghost", 100_000))
	if err == nil {
		t.Fatal("ExtendLease for an unknown session should error")
	}
}

// TestExtendLeaseEmptySessionID: a request with no session id is
// rejected — the handler cannot resolve a tree without it.
func TestExtendLeaseEmptySessionID(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc := newService(t, budgets, nil)

	_, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{})
	if err == nil {
		t.Fatal("ExtendLease with an empty session id should error")
	}
}

// TestExtendLeaseZeroRequestGranted: a non-positive request grants
// nothing and is reported GRANTED, matching leaseextension.Grant.
func TestExtendLeaseZeroRequestGranted(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 0))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED for a zero request", resp.Status)
	}
	if resp.GrantedTokens != 0 {
		t.Errorf("granted = %d, want 0", resp.GrantedTokens)
	}
}

// TestExtendLeaseAuditRecorded: each decision is reported to the
// Auditor with the requested and granted amounts and the outcome.
func TestExtendLeaseAuditRecorded(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 450_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:  budgets,
		Tenants:  budgets,
		Auditing: rec,
		Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	// spec: §8.6 line 743 — PartiallyGranted maps to the "capped" audit
	// class because the ceiling reduced the grant below the requested
	// amount.
	if e.Outcome != leasecontrol.AuditOutcomeCapped {
		t.Errorf("audit outcome = %q, want capped", e.Outcome)
	}
	if e.Requested.Tokens != 200_000 || e.Granted.Tokens != 50_000 {
		t.Errorf("audit amounts = (req %d, grant %d), want (200000, 50000)", e.Requested.Tokens, e.Granted.Tokens)
	}
	if e.RootSessionID != "root-1" || e.TenantID != "acme" {
		t.Errorf("audit identity = (%q, %q), want (root-1, acme)", e.RootSessionID, e.TenantID)
	}
}

// TestExtendLeaseAuditOutcomeApproved_spec_8_6_line_743: a full grant
// is audited under the "approved" classification per §8.6 line 743.
func TestExtendLeaseAuditOutcomeApproved_spec_8_6_line_743(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 50_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got := rec.entries[0].Outcome; got != leasecontrol.AuditOutcomeApproved {
		t.Errorf("audit outcome = %q, want approved", got)
	}
}

// TestExtendLeaseAuditOutcomeDeniedDuringCoolOff_spec_8_6_line_743: an
// auto-rejection during the cool-off audits under "denied" per §8.6 line
// 743.
func TestExtendLeaseAuditOutcomeDeniedDuringCoolOff_spec_8_6_line_743(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	budgets.MarkDenied("root-1")
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 100_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got := rec.entries[0].Outcome; got != leasecontrol.AuditOutcomeDenied {
		t.Errorf("audit outcome = %q, want denied", got)
	}
}

// TestExtendLeaseAuditOutcomeCeilingReachedIsCapped_spec_8_6_line_743: a
// zero grant against an already-reached ceiling audits under "capped"
// (§8.6 line 743 treats CEILING_REACHED as a cap-to-zero).
func TestExtendLeaseAuditOutcomeCeilingReachedIsCapped_spec_8_6_line_743(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 500_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 100_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got := rec.entries[0].Outcome; got != leasecontrol.AuditOutcomeCapped {
		t.Errorf("audit outcome = %q, want capped", got)
	}
}

// TestExtendLeaseInFlightDenial: a denial that lands between the
// TreeBudget read and the ApplyGrant commit converts the outcome to
// REJECTED — the §8.6 in-flight atomic check.
func TestExtendLeaseInFlightDenial(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	base := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	base.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	// The racing source marks the tree denied between the TreeBudget
	// read and ApplyGrant, modelling a REJECTED outcome persisted
	// mid-request.
	racing := &raceBudgetSource{inner: base, denyOnApply: true}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:  racing,
		Tenants:  base,
		Clock:    clock,
		Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_REJECTED {
		t.Errorf("status = %v, want REJECTED — an in-flight denial must not grant", resp.Status)
	}
	if resp.GrantedTokens != 0 {
		t.Errorf("granted = %d, want 0", resp.GrantedTokens)
	}
}

// TestExtendLeaseInFlightDenialCustomCoolOff: the in-flight-denial
// REJECTED response carries the tree's configured rejection cool-off
// rather than the default.
func TestExtendLeaseInFlightDenialCustomCoolOff(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	base := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	base.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		RejectionCoolOff:   120 * time.Second,
	})
	racing := &raceBudgetSource{inner: base, denyOnApply: true}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:  racing,
		Tenants:  base,
		Clock:    clock,
		Elicitor: autoApproveElicitor{},
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
	wantExpiry := now.Add(120 * time.Second).UnixMilli()
	if resp.CoolOffExpiryUnixMs != wantExpiry {
		t.Errorf("cool-off expiry = %d, want %d (the tree's configured cool-off)", resp.CoolOffExpiryUnixMs, wantExpiry)
	}
}

// TestExtendLeaseTenantResolverError: an error from the tenant
// resolver that is not ErrSessionNotFound is surfaced rather than
// silently granting.
func TestExtendLeaseTenantResolverError(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets,
		Tenants: errTenantResolver{err: errors.New("tenant store down")},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.ExtendLease(context.Background(), extendReq("sess-1", 100_000)); err == nil {
		t.Fatal("ExtendLease should surface a tenant-resolver error")
	}
}

// TestExtendLeaseBudgetSourceError: a non-ErrSessionNotFound error from
// the budget source's TreeBudget read is surfaced.
func TestExtendLeaseBudgetSourceError(t *testing.T) {
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: errBudgetSource{err: errors.New("postgres unreachable")},
		Tenants: staticTenant("acme"),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.ExtendLease(context.Background(), extendReq("sess-1", 100_000)); err == nil {
		t.Fatal("ExtendLease should surface a budget-source error")
	}
}

// TestExtendLeaseAuditCarriesSpec_8_6_line_743_fields: every §8.6 line
// 743 field rides on the recorded ExtensionAudit row — ApprovalMode,
// Approver, BatchID, ServiceInstanceID, ClientIP, and the post-grant
// NewLimits. The handler is wired with deterministic BatchIDGen +
// PeerIPFn so the test pins the exact strings. F-8.6.10.
func TestExtendLeaseAuditCarriesSpec_8_6_line_743_fields(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-X", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:           budgets,
		Tenants:           budgets,
		Auditing:          rec,
		ServiceInstanceID: "gw-7",
		BatchIDGen:        func() string { return "batch-stub-1" },
		PeerIPFn:          func(context.Context) string { return "203.0.113.5" },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := svc.ExtendLease(context.Background(), extendReq("root-X", 50_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	if e.ApprovalMode != leasecontrol.ApprovalModeAuto {
		t.Errorf("ApprovalMode = %q, want auto", e.ApprovalMode)
	}
	if e.Approver != "gateway-auto" {
		t.Errorf("Approver = %q, want gateway-auto (auto mode + non-denied outcome)", e.Approver)
	}
	if e.BatchID != "batch-stub-1" {
		t.Errorf("BatchID = %q, want batch-stub-1 (BatchIDGen plumbed)", e.BatchID)
	}
	if e.ServiceInstanceID != "gw-7" {
		t.Errorf("ServiceInstanceID = %q, want gw-7", e.ServiceInstanceID)
	}
	if e.ClientIP != "203.0.113.5" {
		t.Errorf("ClientIP = %q, want 203.0.113.5", e.ClientIP)
	}
	if e.NewLimits.Tokens != 150_000 {
		t.Errorf("NewLimits.TokenBudget = %d, want 150000 (100000 base + 50000 grant)", e.NewLimits.Tokens)
	}
}

// TestExtendLeaseAuditDeniedCoolOffApproverIsClient_spec_8_6_line_743:
// the cool-off rejection records the approver as `client` because
// the rejection is the user's prior denial echoed back; the resolved
// approval mode is also stamped. F-8.6.10.
func TestExtendLeaseAuditDeniedCoolOffApproverIsClient_spec_8_6_line_743(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-D", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeElicitation,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:  budgets,
		Tenants:  budgets,
		Auditing: rec,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	budgets.MarkDenied("root-D")
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-D", 100_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	e := rec.entries[0]
	if e.Outcome != leasecontrol.AuditOutcomeDenied {
		t.Fatalf("Outcome = %q, want denied", e.Outcome)
	}
	if e.Approver != "client" {
		t.Errorf("Approver = %q, want client (cool-off is the user's prior denial)", e.Approver)
	}
	if e.ApprovalMode != leasecontrol.ApprovalModeElicitation {
		t.Errorf("ApprovalMode = %q, want elicitation", e.ApprovalMode)
	}
	if e.NewLimits.Tokens != 100_000 {
		t.Errorf("NewLimits.TokenBudget = %d, want 100000 (no grant applied)", e.NewLimits.Tokens)
	}
}

// TestExtendLeaseDefaultBatchIDGenIsChronological_spec_8_6_line_743:
// successive calls without a BatchIDGen override produce
// chronologically-sortable BatchIDs (the millis prefix advances).
// F-8.6.10.
func TestExtendLeaseDefaultBatchIDGenIsChronological_spec_8_6_line_743(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-T", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-T", 1)); err != nil {
		t.Fatalf("ExtendLease #1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-T", 1)); err != nil {
		t.Fatalf("ExtendLease #2: %v", err)
	}
	if len(rec.entries) != 2 {
		t.Fatalf("audits = %d, want 2", len(rec.entries))
	}
	a, b := rec.entries[0].BatchID, rec.entries[1].BatchID
	if a == "" || b == "" {
		t.Fatalf("BatchIDs empty: a=%q b=%q", a, b)
	}
	if a >= b {
		t.Errorf("BatchIDs not chronologically sortable: a=%q !< b=%q", a, b)
	}
}

// TestExtendLeaseOverGRPC exercises the full GatewayControl gRPC path:
// register the Service on a gRPC server, dial it as a GatewayControl
// client, and confirm a grant round-trips.
func TestExtendLeaseOverGRPC(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := adapterv1.NewGatewayControlClient(conn)
	resp, err := client.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease over gRPC: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Errorf("status = %v, want GRANTED", resp.Status)
	}
	if resp.GrantedTokens != 200_000 {
		t.Errorf("granted = %d, want 200000", resp.GrantedTokens)
	}
}

// TestNewServiceRejectsMissingDeps: the constructor refuses a Service
// without a BudgetSource or a TenantResolver.
func TestNewServiceRejectsMissingDeps(t *testing.T) {
	if _, err := leasecontrol.NewService(leasecontrol.Options{}); err == nil {
		t.Error("NewService with no Budgets should error")
	}
	if _, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: leasecontrol.NewMemoryBudgetSource(),
	}); err == nil {
		t.Error("NewService with no Tenants should error")
	}
}

// movableClock is a test clock that can be advanced.
type movableClock struct {
	t time.Time
}

func (c *movableClock) now() time.Time          { return c.t }
func (c *movableClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// recordingAuditor captures the §8.6 audit entries the Service emits.
type recordingAuditor struct {
	entries   []leasecontrol.ExtensionAudit
	rateLimit []leasecontrol.AutoRateLimitAudit
}

func (r *recordingAuditor) RecordExtension(_ context.Context, e leasecontrol.ExtensionAudit) {
	r.entries = append(r.entries, e)
}

func (r *recordingAuditor) RecordAutoRateLimitExceeded(_ context.Context, e leasecontrol.AutoRateLimitAudit) {
	r.rateLimit = append(r.rateLimit, e)
}

// autoApproveElicitor is an Elicitor that approves every elicitation
// immediately, so the elicitation-mode grant-math tests exercise the
// §8.6 consent path without a real client. Tests needing a rejecting or
// blocking elicitor use the fakes in elicitation_test.go.
type autoApproveElicitor struct{}

func (autoApproveElicitor) Elicit(context.Context, string, string) (bool, error) { return true, nil }

// raceBudgetSource wraps a MemoryBudgetSource and, when denyOnApply is
// set, marks the tree denied just before ApplyGrant runs — modelling a
// REJECTED outcome persisted between the TreeBudget read and the grant
// commit (§8.6 in-flight atomic check).
type raceBudgetSource struct {
	inner       *leasecontrol.MemoryBudgetSource
	denyOnApply bool
}

func (r *raceBudgetSource) TreeBudget(ctx context.Context, tenantID, sessionID string) (leasecontrol.TreeBudget, error) {
	return r.inner.TreeBudget(ctx, tenantID, sessionID)
}

func (r *raceBudgetSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted leasecontrol.Dimensions) (leasecontrol.NewLimits, error) {
	if r.denyOnApply {
		r.inner.MarkDenied(rootSessionID)
	}
	return r.inner.ApplyGrant(ctx, tenantID, rootSessionID, requestingSessionID, granted)
}

func (r *raceBudgetSource) RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	return r.inner.RejectionCoolOff(ctx, tenantID, rootSessionID)
}

func (r *raceBudgetSource) Deny(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) error {
	return r.inner.Deny(ctx, tenantID, rootSessionID, requestingSessionID)
}

// errTenantResolver is a TenantResolver that always fails, exercising
// the handler's tenant-resolution error path.
type errTenantResolver struct {
	err error
}

func (e errTenantResolver) TenantOf(context.Context, string) (string, error) {
	return "", e.err
}

// staticTenant is a TenantResolver that resolves every session to a
// fixed tenant.
type staticTenant string

func (s staticTenant) TenantOf(context.Context, string) (string, error) {
	return string(s), nil
}

// errBudgetSource is a BudgetSource whose TreeBudget read always
// fails, exercising the handler's budget-load error path.
type errBudgetSource struct {
	err error
}

func (e errBudgetSource) TreeBudget(context.Context, string, string) (leasecontrol.TreeBudget, error) {
	return leasecontrol.TreeBudget{}, e.err
}

func (e errBudgetSource) ApplyGrant(context.Context, string, string, string, leasecontrol.Dimensions) (leasecontrol.NewLimits, error) {
	return leasecontrol.NewLimits{}, e.err
}

func (e errBudgetSource) RejectionCoolOff(context.Context, string, string) time.Duration {
	return 0
}

func (e errBudgetSource) Deny(context.Context, string, string, string) error {
	return e.err
}

// TestResolveApprovalMode_spec_8_6_line_654: the
// deployment→tenant→runtime layering picks the most specific
// non-unspecified value, falling back to DefaultApprovalMode when the
// whole stack is empty.
func TestResolveApprovalMode_spec_8_6_line_654(t *testing.T) {
	cases := []struct {
		name                              string
		deployment, tenant, runtime, want leasecontrol.ApprovalMode
	}{
		{"all unspecified falls back to default", "", "", "", leasecontrol.DefaultApprovalMode},
		{"deployment only", leasecontrol.ApprovalModeAuto, "", "", leasecontrol.ApprovalModeAuto},
		{"tenant overrides deployment", leasecontrol.ApprovalModeAuto, leasecontrol.ApprovalModeElicitation, "", leasecontrol.ApprovalModeElicitation},
		{"runtime overrides tenant", leasecontrol.ApprovalModeAuto, leasecontrol.ApprovalModeElicitation, leasecontrol.ApprovalModeAuto, leasecontrol.ApprovalModeAuto},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := leasecontrol.ResolveApprovalMode(c.deployment, c.tenant, c.runtime); got != c.want {
				t.Errorf("ResolveApprovalMode = %q, want %q", got, c.want)
			}
		})
	}
}

// TestResolveSuccessCoolOff_spec_8_6_line_675: the
// deployment→tenant→runtime layering picks the most specific positive
// duration, falling back to DefaultSuccessCoolOff when none is set.
func TestResolveSuccessCoolOff_spec_8_6_line_675(t *testing.T) {
	cases := []struct {
		name                              string
		deployment, tenant, runtime, want time.Duration
	}{
		{"all zero falls back to default", 0, 0, 0, leasecontrol.DefaultSuccessCoolOff},
		{"deployment only", 7 * time.Second, 0, 0, 7 * time.Second},
		{"tenant overrides deployment", 7 * time.Second, 12 * time.Second, 0, 12 * time.Second},
		{"runtime overrides tenant", 7 * time.Second, 12 * time.Second, 3 * time.Second, 3 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := leasecontrol.ResolveSuccessCoolOff(c.deployment, c.tenant, c.runtime); got != c.want {
				t.Errorf("ResolveSuccessCoolOff = %v, want %v", got, c.want)
			}
		})
	}
}

// TestMemoryBudgetSourceApprovalModeDefault_spec_8_6_line_674: a tree
// registered without an explicit ApprovalMode reports the spec-default
// elicitation mode, satisfying §8.6 line 674 even before the
// dispatcher lands.
func TestMemoryBudgetSourceApprovalModeDefault_spec_8_6_line_674(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	got := budgets.ApprovalMode(context.Background(), "acme", "root-1")
	if got != leasecontrol.DefaultApprovalMode {
		t.Errorf("default ApprovalMode = %q, want %q (elicitation)", got, leasecontrol.DefaultApprovalMode)
	}
	if got != leasecontrol.ApprovalModeElicitation {
		t.Errorf("DefaultApprovalMode is %q, want elicitation per §8.6 line 674", got)
	}
}

// TestMemoryBudgetSourceApprovalModeExplicit_spec_8_6_line_674: an
// explicitly registered mode round-trips unchanged.
func TestMemoryBudgetSourceApprovalModeExplicit_spec_8_6_line_674(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
		SuccessCoolOff:     12 * time.Second,
	})
	if got := budgets.ApprovalMode(context.Background(), "acme", "root-1"); got != leasecontrol.ApprovalModeAuto {
		t.Errorf("ApprovalMode = %q, want auto", got)
	}
	if got := budgets.SuccessCoolOff(context.Background(), "acme", "root-1"); got != 12*time.Second {
		t.Errorf("SuccessCoolOff = %v, want 12s", got)
	}
}

// TestDefaultSuccessCoolOff_spec_8_6_line_675: the constant equals the
// spec-default 5 seconds.
func TestDefaultSuccessCoolOff_spec_8_6_line_675(t *testing.T) {
	if leasecontrol.DefaultSuccessCoolOff != 5*time.Second {
		t.Errorf("DefaultSuccessCoolOff = %v, want 5s per §8.6 line 675", leasecontrol.DefaultSuccessCoolOff)
	}
}

// TestExtendLeaseRejectedCoolOffActiveError_spec_15_1_line_1080: the
// pre-grant rejection-cool-off path surfaces the §15.1 typed
// EXTENSION_COOL_OFF_ACTIVE error envelope, with category POLICY and
// retryable=false per §15.1 line 1080. F-8.6.9.
func TestExtendLeaseRejectedCoolOffActiveError_spec_15_1_line_1080(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, clock)
	budgets.MarkDenied("root-1")

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.GetError() == nil {
		t.Fatal("REJECTED response must carry a §15.1 Error envelope per F-8.6.9")
	}
	if got, want := resp.GetError().GetCode(), adapterv1.Error_ERROR_CODE_EXTENSION_COOL_OFF_ACTIVE; got != want {
		t.Errorf("Error.Code = %v, want %v", got, want)
	}
	if got, want := resp.GetError().GetCategory(), adapterv1.Error_CATEGORY_POLICY; got != want {
		t.Errorf("Error.Category = %v, want POLICY", got)
	}
	if resp.GetError().GetRetryable() {
		t.Error("Error.Retryable must be false during cool-off (loop tripper)")
	}
}

// TestExtendLeaseInFlightDenialCoolOffActiveError_spec_15_1_line_1080: the
// §8.6 line 731 in-flight atomic re-check converts the outcome to
// REJECTED with the same EXTENSION_COOL_OFF_ACTIVE envelope as the
// pre-grant path, so admin tooling can route both rejections through
// one error code. F-8.6.9.
func TestExtendLeaseInFlightDenialCoolOffActiveError_spec_15_1_line_1080(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	base := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	base.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	racing := &raceBudgetSource{inner: base, denyOnApply: true}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: racing, Tenants: base, Clock: clock, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.GetError() == nil {
		t.Fatal("in-flight REJECTED response must carry a §15.1 Error envelope per F-8.6.9")
	}
	if got, want := resp.GetError().GetCode(), adapterv1.Error_ERROR_CODE_EXTENSION_COOL_OFF_ACTIVE; got != want {
		t.Errorf("Error.Code = %v, want %v", got, want)
	}
}

// TestExtendLeaseGrantedHasNoErrorEnvelope: non-REJECTED outcomes
// MUST NOT carry the §15.1 Error envelope; a GRANTED response is a
// success and operator tooling treats Error.Code as authoritative.
// F-8.6.9.
func TestExtendLeaseGrantedHasNoErrorEnvelope(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := newService(t, budgets, nil)
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 50_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.GetError() != nil {
		t.Errorf("GRANTED response must not carry Error envelope, got %+v", resp.GetError())
	}
}

// TestExtendLeaseSecondsDimensionGranted_spec_8_6_line_643: a request
// that asks only for additionalMaxAge (requested_seconds) is granted
// against the seconds-dimension ceiling, raises the tree's
// CurrentMaxAgeSeconds, and surfaces granted_seconds on the response.
// F-8.6.11.
func TestExtendLeaseSecondsDimensionGranted_spec_8_6_line_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:               "acme",
		CurrentTokenBudget:     100_000,
		DeploymentBase:         500_000,
		DeploymentMax:          2_000_000,
		CurrentMaxAgeSeconds:   600,
		EffectiveMaxAgeSeconds: 3600,
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:        &adapterv1.SessionId{Value: "root-1"},
		RequestedSeconds: 900,
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_GRANTED; got != want {
		t.Errorf("status = %v, want GRANTED for seconds-only grant", got)
	}
	if got, want := resp.GetGrantedSeconds(), int32(900); got != want {
		t.Errorf("granted_seconds = %d, want %d", got, want)
	}
	tb, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if tb.Current.Seconds != 1500 {
		t.Errorf("current max age = %d, want 1500 (600 + 900)", tb.Current.Seconds)
	}
}

// TestExtendLeaseSecondsDimensionPartiallyGranted_spec_8_6_line_643: a
// request that exceeds the additionalMaxAge ceiling is capped to the
// remaining headroom and reported via the seconds dimension; combined
// outcome rolls up to PARTIALLY_GRANTED. F-8.6.11.
func TestExtendLeaseSecondsDimensionPartiallyGranted_spec_8_6_line_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:               "acme",
		CurrentTokenBudget:     100_000,
		DeploymentBase:         500_000,
		DeploymentMax:          2_000_000,
		CurrentMaxAgeSeconds:   3500,
		EffectiveMaxAgeSeconds: 3600,
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:        &adapterv1.SessionId{Value: "root-1"},
		RequestedSeconds: 600, // headroom is 100s.
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED; got != want {
		t.Errorf("status = %v, want PARTIALLY_GRANTED", got)
	}
	if got, want := resp.GetGrantedSeconds(), int32(100); got != want {
		t.Errorf("granted_seconds = %d, want 100", got)
	}
}

// TestExtendLeaseTokensAndSecondsCombinedOutcome_spec_8_6_line_643: a
// request that hits one dimension's ceiling but fits under the other
// rolls up to PARTIALLY_GRANTED across both dimensions. F-8.6.11.
func TestExtendLeaseTokensAndSecondsCombinedOutcome_spec_8_6_line_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:               "acme",
		CurrentTokenBudget:     100_000,
		DeploymentBase:         500_000,
		DeploymentMax:          2_000_000,
		CurrentMaxAgeSeconds:   3500,
		EffectiveMaxAgeSeconds: 3600, // headroom 100s
	})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:        &adapterv1.SessionId{Value: "root-1"},
		RequestedTokens:  50_000, // fully under tokens ceiling
		RequestedSeconds: 600,    // partially capped
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED; got != want {
		t.Errorf("combined status = %v, want PARTIALLY_GRANTED", got)
	}
	if resp.GetGrantedTokens() != 50_000 || resp.GetGrantedSeconds() != 100 {
		t.Errorf("granted = (%d tokens, %ds), want (50000, 100)", resp.GetGrantedTokens(), resp.GetGrantedSeconds())
	}
}

// TestExtendLeaseSecondsDimensionAudited_spec_8_6_line_743: the audit
// record carries the additionalMaxAge requested/granted figures so
// forensic reconstruction sees both dimensions. F-8.6.11.
func TestExtendLeaseSecondsDimensionAudited_spec_8_6_line_743(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:               "acme",
		CurrentTokenBudget:     100_000,
		DeploymentBase:         500_000,
		DeploymentMax:          2_000_000,
		CurrentMaxAgeSeconds:   600,
		EffectiveMaxAgeSeconds: 3600,
	})
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:        &adapterv1.SessionId{Value: "root-1"},
		RequestedTokens:  50_000,
		RequestedSeconds: 900,
	}); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Requested.Seconds != 900 || e.Granted.Seconds != 900 {
		t.Errorf("audit seconds = (req %d, grant %d), want (900, 900)", e.Requested.Seconds, e.Granted.Seconds)
	}
}

// TestExtendLeaseDrivesMetricEmitter_spec_16_line_66: every ExtendLease
// decision drives the §16 line 66 lenny_delegation_lease_extension_total
// counter via the Options.Metrics callback, labelled with the audit
// outcome class. F-8.6.13.
func TestExtendLeaseDrivesMetricEmitter_spec_16_line_66(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingMetrics{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Metrics: rec, Elicitor: autoApproveElicitor{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 50_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("metric emissions = %d, want 1", len(rec.entries))
	}
	if rec.entries[0] != (metricEntry{tenantID: "acme", outcome: string(leasecontrol.AuditOutcomeApproved)}) {
		t.Errorf("emitted entry = %+v, want {acme, approved}", rec.entries[0])
	}
}

// TestExtendLeaseDrivesMetricOnCoolOffRejection_spec_16_line_66: the
// cool-off REJECTED path also bumps the counter, with outcome=denied.
// F-8.6.13.
func TestExtendLeaseDrivesMetricOnCoolOffRejection_spec_16_line_66(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := fixedClock(now)
	budgets := leasecontrol.NewMemoryBudgetSource().WithClock(clock)
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	rec := &recordingMetrics{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Metrics: rec, Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	budgets.MarkDenied("root-1")
	if _, err := svc.ExtendLease(context.Background(), extendReq("root-1", 50_000)); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("metric emissions = %d, want 1", len(rec.entries))
	}
	if rec.entries[0].outcome != string(leasecontrol.AuditOutcomeDenied) {
		t.Errorf("cool-off metric outcome = %q, want denied", rec.entries[0].outcome)
	}
}

// TestExtendLeaseParentLeaseCeilingCaps_spec_8_6_line_648: a child
// session whose parent lease grants fewer tokens than the layered
// deployment/tenant/runtime ceiling has its grant capped at the
// parent value, not the layered ceiling. F-8.6.15.
func TestExtendLeaseParentLeaseCeilingCaps_spec_8_6_line_648(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     1_000_000, // layered ceiling 1M
		DeploymentMax:      2_000_000,
	})
	budgets.AddSession("child-2", "root-1", "acme")
	// Parent's lease only granted 300K tokens — the child cannot
	// extend beyond that, even though the layered ceiling is 1M and
	// the tree's current budget would have permitted a much larger
	// grant. F-8.6.15.
	budgets.SetParentLease("child-2", leasecontrol.SessionLease{TokenCeiling: 300_000})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("child-2", 500_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED; got != want {
		t.Errorf("status = %v, want PARTIALLY_GRANTED capped by parent ceiling", got)
	}
	if resp.GetGrantedTokens() != 200_000 {
		t.Errorf("granted = %d, want 200000 (parent ceiling 300K minus current 100K)", resp.GetGrantedTokens())
	}
}

// TestExtendLeaseParentLeaseCeilingHonoursLayeredCap_spec_8_6_line_648:
// when the parent's lease ceiling exceeds the layered ceiling, the
// layered ceiling still binds — both hard ceilings apply. F-8.6.15.
func TestExtendLeaseParentLeaseCeilingHonoursLayeredCap_spec_8_6_line_648(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000, // layered ceiling 500K
		DeploymentMax:      2_000_000,
	})
	budgets.AddSession("child-2", "root-1", "acme")
	budgets.SetParentLease("child-2", leasecontrol.SessionLease{TokenCeiling: 5_000_000})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("child-2", 1_000_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED; got != want {
		t.Errorf("status = %v, want PARTIALLY_GRANTED", got)
	}
	if resp.GetGrantedTokens() != 400_000 {
		t.Errorf("granted = %d, want 400000 (layered ceiling 500K minus current 100K)", resp.GetGrantedTokens())
	}
}

// TestExtendLeaseParentLeaseCeilingMaxAge_spec_8_6_line_648: the parent
// ceiling applies to the additionalMaxAge dimension too. F-8.6.15.
func TestExtendLeaseParentLeaseCeilingMaxAge_spec_8_6_line_648(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:               "acme",
		CurrentTokenBudget:     100_000,
		DeploymentBase:         500_000,
		DeploymentMax:          2_000_000,
		CurrentMaxAgeSeconds:   600,
		EffectiveMaxAgeSeconds: 7200, // layered seconds ceiling
	})
	budgets.AddSession("child-2", "root-1", "acme")
	// Parent's perChildMaxAge grant was 1800s; child can't extend
	// beyond that even though the layered ceiling is 7200.
	budgets.SetParentLease("child-2", leasecontrol.SessionLease{MaxAgeCeiling: 1800})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:        &adapterv1.SessionId{Value: "child-2"},
		RequestedSeconds: 5000,
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetGrantedSeconds(), int32(1200); got != want {
		t.Errorf("granted_seconds = %d, want 1200 (parent ceiling 1800 minus current 600)", got)
	}
}

// TestExtendLeaseRootSessionHasNoParentCeiling_spec_8_6_line_648: the
// root session has no parent lease and therefore no extra cap. The
// layered ceiling alone binds. F-8.6.15.
func TestExtendLeaseRootSessionHasNoParentCeiling_spec_8_6_line_648(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	// No SetParentLease for "root-1" — root sessions have no parent.
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if got, want := resp.GetStatus(), adapterv1.ExtendLeaseResponse_STATUS_GRANTED; got != want {
		t.Errorf("status = %v, want GRANTED for root session with no parent cap", got)
	}
	if resp.GetGrantedTokens() != 200_000 {
		t.Errorf("granted = %d, want 200000", resp.GetGrantedTokens())
	}
}

// TestExtendLeaseRequestRejectsNonExtendableFields_spec_8_6_line_643:
// the §8.6 line 643 non-extendable fields (maxDepth,
// minIsolationProfile, delegationPolicyRef, perChildRetryBudget,
// treeVisibility, allowSelfRecursion) MUST NOT appear on the
// ExtendLeaseRequest proto. A regression test on the descriptor
// prevents a future proto bump from accidentally exposing them.
// F-8.6.14.
func TestExtendLeaseRequestRejectsNonExtendableFields_spec_8_6_line_643(t *testing.T) {
	descriptor := (&adapterv1.ExtendLeaseRequest{}).ProtoReflect().Descriptor()
	forbidden := []string{
		"max_depth",
		"min_isolation_profile",
		"delegation_policy_ref",
		"per_child_retry_budget",
		"tree_visibility",
		"allow_self_recursion",
	}
	fields := descriptor.Fields()
	present := map[string]bool{}
	for i := 0; i < fields.Len(); i++ {
		present[string(fields.Get(i).Name())] = true
	}
	for _, f := range forbidden {
		if present[f] {
			t.Errorf("ExtendLeaseRequest schema exposes non-extendable field %q — §8.6 line 643 lists it as not extendable", f)
		}
	}
}

// metricEntry records one IncDelegationLeaseExtension call for the
// recording MetricEmitter.
type metricEntry struct {
	tenantID string
	outcome  string
}

// recordingMetrics is a MetricEmitter that captures every counter bump
// in order so tests can assert on emitted (tenant, outcome) labels.
type recordingMetrics struct {
	entries []metricEntry
}

func (r *recordingMetrics) IncDelegationLeaseExtension(tenantID, outcome string) {
	r.entries = append(r.entries, metricEntry{tenantID: tenantID, outcome: outcome})
}

// recordingTreeGranter records every GrantTokenBudget bridge call so the
// F-8.6.3 budget-side-effect test can assert the granted token delta
// reaches the §8.2 per-tree budget counter.
type recordingTreeGranter struct {
	roots  []string
	deltas []int64
	err    error
}

func (g *recordingTreeGranter) GrantTokenBudget(_ context.Context, rootSessionID string, delta int64) error {
	g.roots = append(g.roots, rootSessionID)
	g.deltas = append(g.deltas, delta)
	return g.err
}

// TestExtendLeaseBridgesTokenGrantToTreeBudget: a GRANTED token-budget
// extension propagates the granted delta onto the §8.2 per-tree
// delegation budget counter via the TreeBudgetGranter seam, closing the
// F-8.6.3 gap where the grant raised only the leasecontrol view.
// spec: §8.6 line 643.
func TestExtendLeaseBridgesTokenGrantToTreeBudget_spec_8_6_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-9", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	granter := &recordingTreeGranter{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:    budgets,
		Tenants:    budgets,
		Elicitor:   autoApproveElicitor{},
		TreeBudget: granter,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	resp, err := svc.ExtendLease(context.Background(), extendReq("root-9", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Fatalf("status = %v, want GRANTED", resp.Status)
	}
	if len(granter.deltas) != 1 || granter.deltas[0] != 200_000 {
		t.Fatalf("bridge deltas = %v, want one 200000 grant", granter.deltas)
	}
	// The bridge keys the grant by the tree root, not the requesting
	// session id.
	if len(granter.roots) != 1 || granter.roots[0] != "root-9" {
		t.Fatalf("bridge roots = %v, want [root-9]", granter.roots)
	}
}

// TestExtendLeaseNoTokenGrantNoBridge: an extension that grants zero
// tokens (a non-token dimension, or a ceiling-reached request) must not
// drive the token-budget bridge. spec: §8.6 line 643.
func TestExtendLeaseNoTokenGrantNoBridge_spec_8_6_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	// A tree already at its token ceiling: a token request is CEILING_REACHED
	// and grants nothing.
	budgets.RegisterTree("root-10", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 500_000,
		DeploymentBase:     500_000,
		DeploymentMax:      500_000,
	})
	granter := &recordingTreeGranter{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:    budgets,
		Tenants:    budgets,
		Elicitor:   autoApproveElicitor{},
		TreeBudget: granter,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-10", 100_000))
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED {
		t.Fatalf("status = %v, want CEILING_REACHED", resp.Status)
	}
	if len(granter.deltas) != 0 {
		t.Fatalf("bridge called %d time(s) for a zero-token grant, want 0", len(granter.deltas))
	}
}

// TestExtendLeaseTreeGranterFailureDoesNotFailGrant: the control-plane
// grant has already committed when the bridge runs, so a transient
// treebudget fault must not turn a GRANTED response into an error.
// spec: §8.6 line 643.
func TestExtendLeaseTreeGranterFailureDoesNotFailGrant_spec_8_6_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-11", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	granter := &recordingTreeGranter{err: errors.New("redis down")}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:    budgets,
		Tenants:    budgets,
		Elicitor:   autoApproveElicitor{},
		TreeBudget: granter,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resp, err := svc.ExtendLease(context.Background(), extendReq("root-11", 200_000))
	if err != nil {
		t.Fatalf("ExtendLease should not surface the bridge fault: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Fatalf("status = %v, want GRANTED despite the bridge fault", resp.Status)
	}
}
