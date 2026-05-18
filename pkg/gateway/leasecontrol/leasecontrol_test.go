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
	if tb.CurrentTokenBudget != 300_000 {
		t.Errorf("current budget = %d, want 300000", tb.CurrentTokenBudget)
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

	// The denied request must not raise the budget.
	tb, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if tb.CurrentTokenBudget != 100_000 {
		t.Errorf("current budget = %d, want 100000 (rejection must not grant)", tb.CurrentTokenBudget)
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
// delegated child session resolves its tree's budget, not a per-session
// budget.
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
	tb, _ := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if tb.CurrentTokenBudget != 150_000 {
		t.Errorf("tree budget = %d, want 150000 — child grant applies to the tree", tb.CurrentTokenBudget)
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
	if e.Outcome != "PARTIALLY_GRANTED" {
		t.Errorf("audit outcome = %q, want PARTIALLY_GRANTED", e.Outcome)
	}
	if e.RequestedTokens != 200_000 || e.GrantedTokens != 50_000 {
		t.Errorf("audit amounts = (req %d, grant %d), want (200000, 50000)", e.RequestedTokens, e.GrantedTokens)
	}
	if e.RootSessionID != "root-1" || e.TenantID != "acme" {
		t.Errorf("audit identity = (%q, %q), want (root-1, acme)", e.RootSessionID, e.TenantID)
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
		Budgets: racing,
		Tenants: base,
		Clock:   clock,
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
		Budgets: racing,
		Tenants: base,
		Clock:   clock,
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
	entries []leasecontrol.ExtensionAudit
}

func (r *recordingAuditor) RecordExtension(_ context.Context, e leasecontrol.ExtensionAudit) {
	r.entries = append(r.entries, e)
}

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

func (r *raceBudgetSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID string, granted int64) (int64, error) {
	if r.denyOnApply {
		r.inner.MarkDenied(rootSessionID)
	}
	return r.inner.ApplyGrant(ctx, tenantID, rootSessionID, granted)
}

func (r *raceBudgetSource) RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	return r.inner.RejectionCoolOff(ctx, tenantID, rootSessionID)
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

func (e errBudgetSource) ApplyGrant(context.Context, string, string, int64) (int64, error) {
	return 0, e.err
}

func (e errBudgetSource) RejectionCoolOff(context.Context, string, string) time.Duration {
	return 0
}
