// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
)

// fakeDenialStore is a leasecontrol.DenialStore double that records the
// calls the store-backed source makes and lets a test script what Grant
// applies.
type fakeDenialStore struct {
	mu         sync.Mutex
	denied     bool
	expiry     time.Time
	coolOff    time.Duration
	grants     leasecontrol.Dimensions
	grantCalls int
	lastLimit  leasecontrol.Dimensions
	lastTenant string
	cleared    []string
	// applyFn, when set, decides the applied amount instead of the clamp.
	applyFn   func(requested leasecontrol.Dimensions) leasecontrol.Dimensions
	grantErr  error
	grantsErr error
	deniedErr error
}

func (f *fakeDenialStore) Deny(_ context.Context, _, _ string, coolOff time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denied, f.coolOff, f.expiry = true, coolOff, time.Unix(0, 0).Add(coolOff)
	return nil
}

func (f *fakeDenialStore) Denied(context.Context, string, string) (bool, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.denied, f.expiry, f.deniedErr
}

func (f *fakeDenialStore) Grant(_ context.Context, tenantID, _ string, requested, limit leasecontrol.Dimensions) (leasecontrol.GrantResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantCalls++
	f.lastLimit, f.lastTenant = limit, tenantID
	if f.grantErr != nil {
		return leasecontrol.GrantResult{}, f.grantErr
	}
	applied := leasecontrol.ClampGrant(f.grants, requested, limit)
	if f.applyFn != nil {
		applied = f.applyFn(requested)
	}
	f.grants = leasecontrol.Dimensions{
		Tokens: f.grants.Tokens + applied.Tokens, Seconds: f.grants.Seconds + applied.Seconds,
	}
	return leasecontrol.GrantResult{Applied: applied, Total: f.grants}, nil
}

func (f *fakeDenialStore) Grants(context.Context, string, string) (leasecontrol.Dimensions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.grants, f.grantsErr
}

func (f *fakeDenialStore) Clear(_ context.Context, tenantID, root string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, tenantID+"/"+root)
	f.denied = false
	return nil
}

// storeTree builds a fixture with root-1 under acme: base 100k tokens, a
// 500k effective max, and a 90 s rejection cool-off.
func storeTree(ds leasecontrol.DenialStore) *memBudget {
	b := newMemBudget()
	if ds != nil {
		b.WithDenialStore(ds)
	}
	b.seedRoot("root-1", treeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		RejectionCoolOff:   90 * time.Second,
		ApprovalMode:       leasecontrol.ApprovalModeAuto,
	})
	return b
}

// TestStoreBudgetSourceRequiresStores_spec_8_6 pins that the source refuses
// a missing session reader or denial store.
//
// spec: §8.6 (Extension state)
func TestStoreBudgetSourceRequiresStores_spec_8_6(t *testing.T) {
	if _, err := leasecontrol.NewStoreBudgetSource(nil, leasecontrol.NewMemoryDenialStore(nil), leasecontrol.ExtensionConfig{}); err == nil {
		t.Error("nil session reader accepted")
	}
	if _, err := leasecontrol.NewStoreBudgetSource(newMemBudget(), nil, leasecontrol.ExtensionConfig{}); err == nil {
		t.Error("nil denial store accepted")
	}
}

// TestStoreBudgetSourceReadsRequestingRow_spec_8_6 pins the per-request
// read: an unknown or cross-tenant session is ErrSessionNotFound, and a
// terminal session is ErrSessionTerminal from TreeBudget and ApplyGrant,
// with no grant reaching the store.
//
// spec: §8.6 (Extension state)
func TestStoreBudgetSourceReadsRequestingRow_spec_8_6(t *testing.T) {
	ds := &fakeDenialStore{}
	b := storeTree(ds)
	ctx := context.Background()
	if _, err := b.TreeBudget(ctx, "acme", "ghost"); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("unknown session err = %v, want ErrSessionNotFound", err)
	}
	if _, err := b.TreeBudget(ctx, "globex", "root-1"); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrSessionNotFound", err)
	}
	b.SetState("root-1", session.StateCompleted)
	if _, err := b.TreeBudget(ctx, "acme", "root-1"); !errors.Is(err, leasecontrol.ErrSessionTerminal) {
		t.Errorf("terminal TreeBudget err = %v, want ErrSessionTerminal", err)
	}
	if _, err := b.ApplyGrant(ctx, "acme", "root-1", "root-1", leasecontrol.Dimensions{Tokens: 1}); !errors.Is(err, leasecontrol.ErrSessionTerminal) {
		t.Errorf("terminal ApplyGrant err = %v, want ErrSessionTerminal", err)
	}
	if ds.grantCalls != 0 {
		t.Errorf("store Grant calls = %d for a terminal session, want 0", ds.grantCalls)
	}
}

// TestStoreBudgetSourceCurrentIsBasePlusGrants_spec_8_6 pins that Current
// is the root's lease plus the tree's cumulative grants, read for a
// delegated child from its root row.
//
// spec: §8.6 (Extension state)
func TestStoreBudgetSourceCurrentIsBasePlusGrants_spec_8_6(t *testing.T) {
	ds := &fakeDenialStore{grants: leasecontrol.Dimensions{Tokens: 7_000}}
	b := storeTree(ds)
	b.seedChild("child-1", "root-1", "acme")
	for _, id := range []string{"root-1", "child-1"} {
		tb, err := b.TreeBudget(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("TreeBudget(%s): %v", id, err)
		}
		if tb.RootSessionID != "root-1" || tb.Current.Tokens != 107_000 || tb.EffectiveMax.Tokens != 500_000 {
			t.Errorf("TreeBudget(%s) = root %q current %d max %d, want root-1 107000 500000",
				id, tb.RootSessionID, tb.Current.Tokens, tb.EffectiveMax.Tokens)
		}
	}
}

// TestStoreBudgetSourceParentCeilingOnlyForDelegatedChild_spec_8_6 pins
// that only a row with a non-zero delegation depth carries its parent's
// lease as a ceiling: a depth-0 row that names a parent, as a derived
// session can, gets none, and a delegated child whose parent row is gone
// is refused.
//
// spec: §8.6 (Extension state)
func TestStoreBudgetSourceParentCeilingOnlyForDelegatedChild_spec_8_6(t *testing.T) {
	b := storeTree(nil)
	b.seedChild("child-1", "root-1", "acme")
	b.seedParentLease("child-1", parentLease{TokenCeiling: 120_000})
	tb, err := b.TreeBudget(context.Background(), "acme", "child-1")
	if err != nil {
		t.Fatalf("TreeBudget(child): %v", err)
	}
	if tb.ParentCeiling.Tokens != 120_000 {
		t.Errorf("delegated child parent ceiling = %d, want 120000", tb.ParentCeiling.Tokens)
	}

	// A derived session: depth 0, a parent pointer, and its own root.
	b.seedChild("derived", "derived", "acme")
	b.seedParentLease("derived", parentLease{TokenCeiling: 1})
	b.mu.Lock()
	d := b.rows["derived"]
	d.DelegationDepth = 0
	b.rows["derived"] = d
	b.mu.Unlock()
	tb, err = b.TreeBudget(context.Background(), "acme", "derived")
	if err != nil {
		t.Fatalf("TreeBudget(derived): %v", err)
	}
	if tb.ParentCeiling != (leasecontrol.Dimensions{}) {
		t.Errorf("derived session parent ceiling = %+v, want none", tb.ParentCeiling)
	}

	b.mu.Lock()
	delete(b.rows, parentOf("child-1"))
	b.mu.Unlock()
	if _, err := b.TreeBudget(context.Background(), "acme", "child-1"); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("child with a missing parent row err = %v, want ErrSessionNotFound", err)
	}
}

// TestStoreBudgetSourceStoreErrorsPropagate_spec_8_6 pins that a denial or
// grants read failure fails the TreeBudget read rather than reporting an
// undenied, ungranted tree.
//
// spec: §8.6 (Extension state), §8.6 (Durability)
func TestStoreBudgetSourceStoreErrorsPropagate_spec_8_6(t *testing.T) {
	boom := errors.New("postgres down")
	for name, ds := range map[string]*fakeDenialStore{
		"denied": {deniedErr: boom},
		"grants": {grantsErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := storeTree(ds).TreeBudget(context.Background(), "acme", "root-1"); !errors.Is(err, boom) {
				t.Errorf("TreeBudget err = %v, want the store error", err)
			}
		})
	}
}

// TestStoreBudgetSourceApplyGrantClampLimit_spec_8_6 pins that ApplyGrant
// hands the store the effective max less the root's base, capped by the
// parent ceiling, and returns the store's applied grant.
//
// spec: §8.6 (Extension state), §8.6 (In-flight requests)
func TestStoreBudgetSourceApplyGrantClampLimit_spec_8_6(t *testing.T) {
	ds := &fakeDenialStore{}
	b := storeTree(ds)
	got, err := b.ApplyGrant(context.Background(), "acme", "root-1", "root-1", leasecontrol.Dimensions{Tokens: 50_000})
	if err != nil {
		t.Fatalf("ApplyGrant: %v", err)
	}
	if ds.lastLimit.Tokens != 400_000 || ds.lastTenant != "acme" {
		t.Errorf("clamp limit = %d under %q, want 400000 under acme", ds.lastLimit.Tokens, ds.lastTenant)
	}
	if got.Granted.Tokens != 50_000 || got.NewLimits.Tokens != 150_000 {
		t.Errorf("applied = %+v, want 50000 granted and a 150000 limit", got)
	}

	b.seedChild("child-1", "root-1", "acme")
	b.seedParentLease("child-1", parentLease{TokenCeiling: 200_000})
	if _, err := b.ApplyGrant(context.Background(), "acme", "root-1", "child-1", leasecontrol.Dimensions{Tokens: 1}); err != nil {
		t.Fatalf("ApplyGrant(child): %v", err)
	}
	if ds.lastLimit.Tokens != 100_000 {
		t.Errorf("child clamp limit = %d, want the parent ceiling less the base, 100000", ds.lastLimit.Tokens)
	}
	if _, err := b.ApplyGrant(context.Background(), "acme", "other-root", "child-1", leasecontrol.Dimensions{Tokens: 1}); !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("ApplyGrant with a foreign root err = %v, want ErrSessionNotFound", err)
	}
}

// TestStoreBudgetSourceDenyAndClear_spec_8_6 pins that Deny persists the
// configured rejection cool-off and ClearSubtreeDenial clears the row
// under the tenant the caller resolved.
//
// spec: §8.6 (Durability), §15.1
func TestStoreBudgetSourceDenyAndClear_spec_8_6(t *testing.T) {
	ds := &fakeDenialStore{}
	b := storeTree(ds)
	if err := b.Deny(context.Background(), "acme", "root-1", "child-9"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if !ds.denied || ds.coolOff != 90*time.Second {
		t.Errorf("Deny reached the store with denied=%v coolOff=%v, want true 90s", ds.denied, ds.coolOff)
	}
	if err := b.ClearSubtreeDenial(context.Background(), "acme", "root-1", "child-9"); err != nil {
		t.Fatalf("ClearSubtreeDenial: %v", err)
	}
	if len(ds.cleared) != 1 || ds.cleared[0] != "acme/root-1" {
		t.Errorf("cleared = %v, want [acme/root-1]", ds.cleared)
	}

	// The default cool-off applies when none is configured.
	src, err := leasecontrol.NewStoreBudgetSource(newMemBudget(), ds, leasecontrol.ExtensionConfig{})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	if err := src.Deny(context.Background(), "acme", "root-1", "root-1"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if ds.coolOff != leasecontrol.DefaultRejectionCoolOff {
		t.Errorf("default cool-off = %v, want %v", ds.coolOff, leasecontrol.DefaultRejectionCoolOff)
	}
	if src.ApprovalMode(context.Background(), "acme", "root-1") != leasecontrol.DefaultApprovalMode {
		t.Error("unspecified approval mode did not resolve to the default")
	}
}

// TestMemoryDenialStoreConcurrentGrantsStayWithinHeadroom_spec_8_6 pins
// that concurrent Grant calls on one tree never take the cumulative grant
// past the limit, and that the applied amounts sum to the cumulative.
//
// spec: §8.6 (Extension state), §8.6 (In-flight requests)
func TestMemoryDenialStoreConcurrentGrantsStayWithinHeadroom_spec_8_6(t *testing.T) {
	ds := leasecontrol.NewMemoryDenialStore(nil)
	limit := leasecontrol.Dimensions{Tokens: 1_000}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var sum int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := ds.Grant(context.Background(), "acme", "root-1", leasecontrol.Dimensions{Tokens: int64(17 + i)}, limit)
			if err != nil {
				t.Errorf("Grant: %v", err)
				return
			}
			mu.Lock()
			sum += res.Applied.Tokens
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	got, err := ds.Grants(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if got.Tokens != 1_000 || sum != 1_000 {
		t.Errorf("cumulative = %d, applied sum = %d, want both 1000", got.Tokens, sum)
	}
}

// TestMemoryDenialStoreDenialAndClear_spec_8_6 pins that a denial in its
// cool-off blocks Grant with ErrExtensionDenied, expires on the store
// clock, and that an unknown tree has zero grants and is not denied.
//
// spec: §8.6 (In-flight requests), §8.6 (Durability)
func TestMemoryDenialStoreDenialAndClear_spec_8_6(t *testing.T) {
	clk := &movableClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ds := leasecontrol.NewMemoryDenialStore(clk.now)
	ctx := context.Background()
	if d, _, _ := ds.Denied(ctx, "acme", "root-1"); d {
		t.Error("unknown tree reported denied")
	}
	if g, _ := ds.Grants(ctx, "acme", "root-1"); g != (leasecontrol.Dimensions{}) {
		t.Errorf("unknown tree grants = %+v, want zero", g)
	}
	if err := ds.Deny(ctx, "acme", "root-1", time.Minute); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if _, err := ds.Grant(ctx, "acme", "root-1", leasecontrol.Dimensions{Tokens: 1}, leasecontrol.Dimensions{Tokens: 10}); !errors.Is(err, leasecontrol.ErrExtensionDenied) {
		t.Errorf("denied Grant err = %v, want ErrExtensionDenied", err)
	}
	clk.advance(2 * time.Minute)
	if d, _, _ := ds.Denied(ctx, "acme", "root-1"); d {
		t.Error("denial still in force after the cool-off")
	}
	if err := ds.Deny(ctx, "acme", "root-1", time.Minute); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if err := ds.Clear(ctx, "acme", "root-1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if res, err := ds.Grant(ctx, "acme", "root-1", leasecontrol.Dimensions{Tokens: 4}, leasecontrol.Dimensions{Tokens: 10}); err != nil || res.Applied.Tokens != 4 {
		t.Errorf("Grant after Clear = %+v, %v, want 4 applied", res, err)
	}
}

// TestClampGrant_spec_8_6 pins the per-dimension clamp.
//
// spec: §8.6 (Extension state)
func TestClampGrant_spec_8_6(t *testing.T) {
	cases := []struct {
		name                   string
		cumulative, req, limit int64
		want                   int64
	}{
		{"fits", 0, 5, 10, 5},
		{"partial", 7, 5, 10, 3},
		{"exhausted", 10, 5, 10, 0},
		{"over limit", 12, 5, 10, 0},
		{"no request", 0, 0, 10, 0},
		{"negative request", 0, -3, 10, 0},
	}
	for _, c := range cases {
		got := leasecontrol.ClampGrant(
			leasecontrol.Dimensions{Seconds: c.cumulative},
			leasecontrol.Dimensions{Seconds: c.req},
			leasecontrol.Dimensions{Seconds: c.limit},
		)
		if got.Seconds != c.want {
			t.Errorf("%s: clamp = %d, want %d", c.name, got.Seconds, c.want)
		}
	}
}

// TestExtendLeaseOutcomeFromAppliedGrant_spec_8_6 pins that the response
// is taken from the grant the store applied: a grant clamped below the
// computed amount answers PARTIALLY_GRANTED with the applied amount, and a
// grant clamped to zero answers CEILING_REACHED, and ExtendForBudget
// raises the enforcer by the applied amount.
//
// spec: §8.6 (In-flight requests), §8.6 (Extension state)
func TestExtendLeaseOutcomeFromAppliedGrant_spec_8_6(t *testing.T) {
	cases := []struct {
		name       string
		apply      int64
		wantStatus leasecontrol.ExtendStatus
	}{
		{"partial", 30_000, leasecontrol.StatusPartiallyGranted},
		{"zero", 0, leasecontrol.StatusCeilingReached},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := &fakeDenialStore{applyFn: func(leasecontrol.Dimensions) leasecontrol.Dimensions {
				return leasecontrol.Dimensions{Tokens: c.apply}
			}}
			b := storeTree(ds)
			svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: b})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			resp, err := svc.ExtendLease(context.Background(), extendReq("root-1", 50_000))
			if err != nil {
				t.Fatalf("ExtendLease: %v", err)
			}
			if resp.Status != c.wantStatus || resp.Granted.Tokens != c.apply {
				t.Errorf("response = %v granted %d, want %v granted %d", resp.Status, resp.Granted.Tokens, c.wantStatus, c.apply)
			}
		})
	}

	ds := &fakeDenialStore{applyFn: func(leasecontrol.Dimensions) leasecontrol.Dimensions {
		return leasecontrol.Dimensions{Tokens: 1_234}
	}}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: storeTree(ds)})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rec := newFakeReclaimer()
	svc.SetReclaimer(rec)
	out, err := svc.ExtendForBudget(context.Background(), context.Background(), "acme", "root-1")
	if err != nil || out != leasecontrol.OutcomeGranted {
		t.Fatalf("ExtendForBudget = %v, %v, want Granted", out, err)
	}
	if rec.raised["root-1"] != 1_234 {
		t.Errorf("enforcer raised by %d, want the applied 1234", rec.raised["root-1"])
	}
}

// TestExtendRequiresTenant_spec_8_6 pins that the dispatch refuses a
// request with no tenant, and that ExtendForBudget under another tenant
// finds no session and applies nothing.
//
// spec: §8.6 (Extension state)
func TestExtendRequiresTenant_spec_8_6(t *testing.T) {
	ds := &fakeDenialStore{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: storeTree(ds)})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rec := newFakeReclaimer()
	svc.SetReclaimer(rec)
	req := extendReq("root-1", 10)
	req.TenantID = ""
	if _, err := svc.ExtendLease(context.Background(), req); err == nil {
		t.Error("ExtendLease with no tenant succeeded")
	}
	if out, err := svc.ExtendForBudget(context.Background(), context.Background(), "", "root-1"); err == nil || out != leasecontrol.OutcomeTerminal {
		t.Errorf("ExtendForBudget with no tenant = %v, %v, want Terminal with an error", out, err)
	}
	if out, err := svc.ExtendForBudget(context.Background(), context.Background(), "globex", "root-1"); !errors.Is(err, leasecontrol.ErrSessionNotFound) || out != leasecontrol.OutcomeTerminal {
		t.Errorf("cross-tenant ExtendForBudget = %v, %v, want Terminal ErrSessionNotFound", out, err)
	}
	if ds.grantCalls != 0 || rec.raiseCount() != 0 {
		t.Errorf("grants = %d, raises = %d, want none", ds.grantCalls, rec.raiseCount())
	}
}
