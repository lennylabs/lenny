// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// tombstone creates a tenant row and soft-deletes it.
func tombstone(t *testing.T, tenants tenantstore.Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: id, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	if _, err := tenants.Update(ctx, id, func(row *tenantstore.Tenant) error {
		row.State = tenantstore.TenantStateDeleted
		return nil
	}); err != nil {
		t.Fatalf("mark %s deleted: %v", id, err)
	}
	if err := tenants.SoftDelete(ctx, id, time.Now()); err != nil {
		t.Fatalf("tombstone %s: %v", id, err)
	}
}

// getFailingTenants fails every Get with a fixed error.
type getFailingTenants struct {
	tenantstore.Store
	err error
}

func (g getFailingTenants) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, g.err
}

// TestTenantListersAfterTheTombstone_spec_12_8 asserts which tenants each
// background-worker lister returns: allTenantsLister keeps a tombstoned
// tenant that tenantsLister drops; a tombstoned `default` row leaves
// tenantsLister and appears once in allTenantsLister; with no `default` row
// both return `default`; and a `Get` error other than not-found is
// returned by tenantsLister rather than read as a missing row.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 13.3 (Credential Flow: Authoritative durability for revocation)
func TestTenantListersAfterTheTombstone_spec_12_8(t *testing.T) {
	ctx := context.Background()
	list := func(t *testing.T, l tenantIDLister) string {
		t.Helper()
		ids, err := l.ListTenants(ctx)
		if err != nil {
			t.Fatalf("ListTenants: %v", err)
		}
		return strings.Join(sortedCopy(ids), ",")
	}

	t.Run("no default row", func(t *testing.T) {
		tenants := tenantstore.NewMemory()
		seedTenants(t, tenants, "acme")
		tombstone(t, tenants, "globex")
		if got := list(t, tenantsLister{tenants}); got != "acme,default" {
			t.Errorf("tenantsLister = %s, want acme,default", got)
		}
		if got := list(t, allTenantsLister{tenants}); got != "acme,default,globex" {
			t.Errorf("allTenantsLister = %s, want acme,default,globex", got)
		}
	})

	t.Run("tombstoned default row", func(t *testing.T) {
		tenants := tenantstore.NewMemory()
		seedTenants(t, tenants, "acme")
		tombstone(t, tenants, "default")
		if got := list(t, tenantsLister{tenants}); got != "acme" {
			t.Errorf("tenantsLister = %s, want acme (a deleted default leaves the workers)", got)
		}
		if got := list(t, allTenantsLister{tenants}); got != "acme,default" {
			t.Errorf("allTenantsLister = %s, want acme,default once", got)
		}
	})

	t.Run("live default row listed once", func(t *testing.T) {
		tenants := tenantstore.NewMemory()
		seedTenants(t, tenants, "default", "acme")
		if got := list(t, tenantsLister{tenants}); got != "acme,default" {
			t.Errorf("tenantsLister = %s, want acme,default", got)
		}
	})

	t.Run("get error is returned", func(t *testing.T) {
		readErr := errors.New("tenant row unreadable")
		tenants := getFailingTenants{Store: tenantstore.NewMemory(), err: readErr}
		if _, err := (tenantsLister{tenants}).ListTenants(ctx); !errors.Is(err, readErr) {
			t.Errorf("tenantsLister error = %v, want the Get error", err)
		}
		if _, err := (allTenantsLister{tenants}).ListTenants(ctx); err != nil {
			t.Errorf("allTenantsLister reads no row, got %v", err)
		}
	})
}

// recordingExpirer records DeleteExpired calls and fails for one tenant.
type recordingExpirer struct {
	mu      sync.Mutex
	tenants []string
	cutoffs []time.Time
	failFor string
}

func (r *recordingExpirer) DeleteExpired(_ context.Context, tenantID string, cutoff time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenants = append(r.tenants, tenantID)
	r.cutoffs = append(r.cutoffs, cutoff)
	if tenantID == r.failFor {
		return 0, errors.New("delete failed")
	}
	return 1, nil
}

// TestSweepIssuedTokensVisitsEveryTenant_spec_13_3 runs one issued-token GC
// pass over allTenantsLister and asserts it visits every tenant, the
// tombstoned one included, with a cutoff one hour before the pass's clock
// read, and that one tenant's failure does not stop the pass.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation), 12.2 (Storage Roles)
func TestSweepIssuedTokensVisitsEveryTenant_spec_13_3(t *testing.T) {
	tenants := tenantstore.NewMemory()
	seedTenants(t, tenants, "acme", "initech")
	tombstone(t, tenants, "globex")
	gc := &recordingExpirer{failFor: "acme"}
	before := time.Now()
	sweepIssuedTokens(context.Background(), gc, allTenantsLister{tenants}, time.Now)
	after := time.Now()

	if got := strings.Join(sortedCopy(gc.tenants), ","); got != "acme,default,globex,initech" {
		t.Fatalf("visited %s, want acme,default,globex,initech", got)
	}
	for _, c := range gc.cutoffs {
		if c.Before(before.Add(-time.Hour)) || c.After(after.Add(-time.Hour)) {
			t.Errorf("cutoff %v outside [%v, %v]", c, before.Add(-time.Hour), after.Add(-time.Hour))
		}
	}
}

// revokedSource returns one revoked jti for one tenant.
type revokedSource struct{ tenant, jti string }

func (r revokedSource) ListRevoked(_ context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	if tenantID != r.tenant {
		return nil, nil
	}
	return []issuedtokenstore.IssuedToken{{JTI: r.jti, TenantID: tenantID, ExpiresAt: time.Now().Add(-30 * time.Minute)}}, nil
}

// TestRevocationRehydrationReachesTombstonedTenants_spec_13_3 asserts that
// the revocation cache rehydrated through allTenantsLister loads a revoked
// token of a tombstoned tenant that is still inside the trailing hour, and
// that rehydrating through tenantsLister misses it, which is why the
// rehydration uses the former.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation)
func TestRevocationRehydrationReachesTombstonedTenants_spec_13_3(t *testing.T) {
	tenants := tenantstore.NewMemory()
	seedTenants(t, tenants, "acme")
	tombstone(t, tenants, "globex")
	src := revokedSource{tenant: "globex", jti: "jti-globex-1"}

	all := revocation.NewCache()
	if err := all.Rehydrate(context.Background(), allTenantsLister{tenants}, src); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if !all.IsRevoked("jti-globex-1") {
		t.Fatal("rehydration through allTenantsLister missed the tombstoned tenant's revoked token")
	}
	listed := revocation.NewCache()
	if err := listed.Rehydrate(context.Background(), tenantsLister{tenants}, src); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if listed.IsRevoked("jti-globex-1") {
		t.Fatal("tenantsLister unexpectedly listed the tombstoned tenant")
	}
}

// failingTenantIDs fails every tenant listing.
type failingTenantIDs struct{ err error }

func (f failingTenantIDs) ListTenants(context.Context) ([]string, error) { return nil, f.err }

// TestIssuedTokenGCStartAndListingError_spec_13_3 asserts the GC start runs
// one pass immediately over every tenant row, with a non-positive interval
// falling back to hourly, and stops its loop with the watchdog context, and
// that a pass whose tenant listing fails deletes nothing.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation), 12.2 (Storage Roles)
func TestIssuedTokenGCStartAndListingError_spec_13_3(t *testing.T) {
	tenants := tenantstore.NewMemory()
	tombstone(t, tenants, "globex")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &gatewayWiring{}
	w.tenants, w.watchdogCtx = tenants, ctx
	gc := &recordingExpirer{}
	w.startIssuedTokenGC(gc, 0)
	gc.mu.Lock()
	visited := strings.Join(sortedCopy(gc.tenants), ",")
	gc.mu.Unlock()
	if visited != "default,globex" {
		t.Fatalf("first pass visited %s, want default,globex", visited)
	}

	idle := &recordingExpirer{}
	sweepIssuedTokens(context.Background(), idle, failingTenantIDs{err: errors.New("tenant store down")}, time.Now)
	if len(idle.tenants) != 0 {
		t.Fatalf("a failed listing still deleted for %v", idle.tenants)
	}
}
