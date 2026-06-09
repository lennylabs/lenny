// SPDX-License-Identifier: MIT

package billingfanout_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// spec: §11.2.1 — delegation.spawned billing event pertains to the
// spawned child session; no child id ⇒ no event.
func TestDelegationSpawned_spec_11_2_1(t *testing.T) {
	ev, ok := billingfanout.DelegationSpawned("acme", "alice", map[string]any{
		"child_session_id":  "child-1",
		"parent_session_id": "parent-1",
	})
	if !ok {
		t.Fatal("expected ok for a detail with a child session id")
	}
	if ev.EventType != billingstore.EventDelegationSpawned {
		t.Fatalf("event type = %q", ev.EventType)
	}
	if ev.SessionID != "child-1" || ev.TenantID != "acme" || ev.UserID != "alice" {
		t.Fatalf("envelope = %+v", ev)
	}
	if ev.Conditional != nil {
		t.Fatalf("delegation.spawned carries no conditional fields, got %+v", ev.Conditional)
	}

	if _, ok := billingfanout.DelegationSpawned("acme", "alice", map[string]any{}); ok {
		t.Fatal("expected ok=false when child session id is absent")
	}
}

// spec: §11.2.1 — delegation.isolation_violation carries
// parent_isolation / target_isolation / matched_policy_rule.
func TestDelegationIsolationViolation_spec_11_2_1(t *testing.T) {
	ev, ok := billingfanout.DelegationIsolationViolation("acme", "alice", map[string]any{
		"parentSessionId":     "parent-1",
		"parent_isolation":    "microvm",
		"target_isolation":    "standard",
		"matched_policy_rule": "rule-3",
	})
	if !ok {
		t.Fatal("expected ok")
	}
	if ev.SessionID != "parent-1" {
		t.Fatalf("session id = %q", ev.SessionID)
	}
	c := ev.Conditional
	if c == nil || c.ParentIsolation != "microvm" || c.TargetIsolation != "standard" || c.MatchedPolicyRule != "rule-3" {
		t.Fatalf("conditional = %+v", c)
	}
}

// spec: §11.2.1 — interceptor weakened carries old/new policy +
// transition window; strengthened omits the cooldown; cooldown_active
// drops the old/new policy and reports the affected set + window.
func TestInterceptorFailPolicy_spec_11_2_1(t *testing.T) {
	weak := billingfanout.InterceptorFailPolicy(billingstore.EventInterceptorFailPolicyWeakened,
		"acme", "ic-1", "fail-closed", "fail-open", 2, []string{"p1", "p2"}, "2026-06-09T00:00:00Z", 600)
	if weak.Conditional.OldFailPolicy != "fail-closed" || weak.Conditional.NewFailPolicy != "fail-open" {
		t.Fatalf("weakened policies = %+v", weak.Conditional)
	}
	if weak.Conditional.TransitionTS == "" || weak.Conditional.CooldownSeconds != 600 {
		t.Fatalf("weakened window = %+v", weak.Conditional)
	}
	if weak.Conditional.AffectedPolicyCount != 2 || len(weak.Conditional.AffectedPolicyNames) != 2 {
		t.Fatalf("weakened affected = %+v", weak.Conditional)
	}

	strong := billingfanout.InterceptorFailPolicy(billingstore.EventInterceptorFailPolicyStrengthened,
		"acme", "ic-1", "fail-open", "fail-closed", 2, []string{"p1"}, "2026-06-09T00:00:00Z", 600)
	if strong.Conditional.CooldownSeconds != 0 || strong.Conditional.TransitionTS != "" {
		t.Fatalf("strengthened must omit cooldown/transition, got %+v", strong.Conditional)
	}

	cooldown := billingfanout.InterceptorFailPolicy(billingstore.EventInterceptorWeakeningCooldownActive,
		"acme", "ic-1", "fail-closed", "fail-open", 3, []string{"p1"}, "2026-06-09T00:00:00Z", 600)
	if cooldown.Conditional.OldFailPolicy != "" || cooldown.Conditional.NewFailPolicy != "" {
		t.Fatalf("cooldown_active must drop old/new policy, got %+v", cooldown.Conditional)
	}
	if cooldown.Conditional.CooldownSeconds != 600 || cooldown.Conditional.AffectedPolicyCount != 3 {
		t.Fatalf("cooldown_active window/affected = %+v", cooldown.Conditional)
	}
}

// spec: §11.2.1 — export-scan weakened carries cooldown; strengthened
// omits it; both carry the old/new boolean and policy name.
func TestDelegationPolicyExportScan_spec_11_2_1(t *testing.T) {
	weak := billingfanout.DelegationPolicyExportScan(billingstore.EventDelegationPolicyExportScanWeakened,
		"acme", "pol-1", true, false, "2026-06-09T00:00:00Z", 600)
	if weak.Conditional.OldScanExportedFiles == nil || *weak.Conditional.OldScanExportedFiles != true {
		t.Fatalf("weakened old scan = %+v", weak.Conditional)
	}
	if weak.Conditional.NewScanExportedFiles == nil || *weak.Conditional.NewScanExportedFiles != false {
		t.Fatalf("weakened new scan = %+v", weak.Conditional)
	}
	if weak.Conditional.CooldownSeconds != 600 {
		t.Fatalf("weakened cooldown = %d", weak.Conditional.CooldownSeconds)
	}

	strong := billingfanout.DelegationPolicyExportScan(billingstore.EventDelegationPolicyExportScanStrengthened,
		"acme", "pol-1", false, true, "2026-06-09T00:00:00Z", 600)
	if strong.Conditional.CooldownSeconds != 0 {
		t.Fatalf("strengthened must omit cooldown, got %d", strong.Conditional.CooldownSeconds)
	}
}

// spec: §11.2.1 — export-file-scan rejected/failed-open conditional set.
func TestExportFileScan_spec_11_2_1(t *testing.T) {
	ev := billingfanout.ExportFileScan(billingstore.EventDelegationExportScanFailedOpen,
		"acme", "pol-1", "ic-1", "out/report.pdf", 4096, "timeout")
	c := ev.Conditional
	if c.PolicyName != "pol-1" || c.InterceptorRef != "ic-1" || c.FilePath != "out/report.pdf" || c.FileSize != 4096 || c.Reason != "timeout" {
		t.Fatalf("conditional = %+v", c)
	}
}

// spec: §11.2.1 — credential.leased / credential.revoked conditional set.
func TestCredentialEvents_spec_11_2_1(t *testing.T) {
	leased := billingfanout.CredentialLeased("acme", "sess-1", "pool-a", "cred-7", "proxy")
	if leased.SessionID != "sess-1" || leased.Conditional.CredentialPoolID != "pool-a" ||
		leased.Conditional.CredentialID != "cred-7" || leased.Conditional.DeliveryMode != "proxy" {
		t.Fatalf("leased = %+v / %+v", leased, leased.Conditional)
	}
	revoked := billingfanout.CredentialRevoked("acme", "pool-a", "cred-7", "bob", "compromised", 3)
	if revoked.Conditional.RevokedBy != "bob" || revoked.Conditional.RevocationReason != "compromised" ||
		revoked.Conditional.LeasesTerminated != 3 {
		t.Fatalf("revoked = %+v", revoked.Conditional)
	}
}

// spec: §11.2.1 — derive.isolation_downgrade conditional set.
func TestDeriveIsolationDowngrade_spec_11_2_1(t *testing.T) {
	ev := billingfanout.DeriveIsolationDowngrade("acme", "src-1", "microvm", "pool-weak", "standard", "admin-sub", "TICKET-9")
	if ev.SessionID != "src-1" {
		t.Fatalf("session id = %q", ev.SessionID)
	}
	c := ev.Conditional
	if c.SourceSessionID != "src-1" || c.SourceIsolationProfile != "microvm" || c.TargetPool != "pool-weak" ||
		c.TargetIsolationProfile != "standard" || c.AuthorizingUserSub != "admin-sub" || c.TicketID != "TICKET-9" {
		t.Fatalf("conditional = %+v", c)
	}
}

// spec: §11.2.1 — pool.isolation_warning conditional set.
func TestPoolIsolationWarning_spec_11_2_1(t *testing.T) {
	ev := billingfanout.PoolIsolationWarning("acme", "pool-new", "standard", "rule-1", "pool-parent", "microvm")
	c := ev.Conditional
	if c.PoolName != "pool-new" || c.PoolIsolation != "standard" || c.MatchedPolicyRule != "rule-1" ||
		c.ConflictingPoolName != "pool-parent" || c.ConflictingIsolation != "microvm" {
		t.Fatalf("conditional = %+v", c)
	}
}

// spec: §11.2.1 — token_usage.checkpoint carries token counts on the
// common envelope and no event-specific conditional.
func TestTokenUsageCheckpoint_spec_11_2_1(t *testing.T) {
	ev := billingfanout.TokenUsageCheckpoint("acme", "sess-1", "alice", 100, 250)
	if ev.TokensInput != 100 || ev.TokensOutput != 250 || ev.SessionID != "sess-1" || ev.UserID != "alice" {
		t.Fatalf("envelope = %+v", ev)
	}
	if ev.Conditional != nil {
		t.Fatalf("token checkpoint carries no conditional, got %+v", ev.Conditional)
	}
}

// spec: §11.2.1 — a constructed event commits to the ledger and is
// readable back through the per-tenant sequence.
func TestEmitterAppendsToLedger_spec_11_2_1(t *testing.T) {
	store := billingstore.NewMemory()
	em := billingfanout.NewEmitter(store)
	ev := billingfanout.TokenUsageCheckpoint("acme", "sess-1", "alice", 10, 20)
	em.Emit(context.Background(), ev)

	got, err := store.Since(context.Background(), "acme", 0, 10)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 1 || got[0].EventType != billingstore.EventTokenUsageCheckpoint || got[0].SequenceNumber != 1 {
		t.Fatalf("ledger = %+v", got)
	}
}

// A nil Emitter and a nil-store Emitter both drop Emit without panicking
// (the no-store minimal gateway path).
func TestEmitterNilSafe(t *testing.T) {
	var nilEm *billingfanout.Emitter
	nilEm.Emit(context.Background(), billingstore.Event{TenantID: "acme", EventType: billingstore.EventDelegationSpawned})

	noStore := billingfanout.NewEmitter(nil)
	if noStore != nil {
		t.Fatal("NewEmitter(nil) must return a nil Emitter")
	}
	noStore.Emit(context.Background(), billingstore.Event{TenantID: "acme", EventType: billingstore.EventDelegationSpawned})
}

// An empty-tenant event is dropped (the §11.2.1 stream is per-tenant);
// emission is best-effort so a store error never surfaces.
func TestEmitterDropsAndSwallows(t *testing.T) {
	em := billingfanout.NewEmitter(failingStore{})
	// Empty tenant ⇒ dropped before the store is touched.
	em.Emit(context.Background(), billingstore.Event{EventType: billingstore.EventDelegationSpawned})
	// A store error is swallowed (best-effort tee).
	em.Emit(context.Background(), billingstore.Event{TenantID: "acme", EventType: billingstore.EventDelegationSpawned})
}

type failingStore struct{ billingstore.Store }

func (failingStore) Append(context.Context, billingstore.Event) (billingstore.Event, error) {
	return billingstore.Event{}, errors.New("boom")
}
