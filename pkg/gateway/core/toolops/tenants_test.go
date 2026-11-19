// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

func seedTenant(t *testing.T, store tenantstore.Store, tn tenantstore.Tenant) {
	t.Helper()
	if err := store.Create(context.Background(), tn); err != nil {
		t.Fatalf("seed tenant %s: %v", tn.ID, err)
	}
}

// TestTenantFactsReadTheTenantStore checks that the elicitation integrity
// mode and the credential providers are the tenant row's, that a missing
// tenant is an error the caller treats as no fact, and that no tenant
// store reads nothing.
// spec: GWSEAM-TenantElicitationIntegrity, GWSEAM-TenantCredentialProviders
func TestTenantFactsReadTheTenantStore(t *testing.T) {
	ctx := context.Background()
	store := tenantstore.NewMemory()
	seedTenant(t, store, tenantstore.Tenant{
		ID: "acme", ElicitationContentIntegrity: "detect-only",
		CredentialPolicy: credential.CredentialPolicy{ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "pool-a"},
		}},
	})
	tenants := NewTenants(store, nil, nil)
	if mode, err := tenants.TenantElicitationIntegrity(ctx, "acme"); err != nil || mode != "detect-only" {
		t.Errorf("integrity = %q, %v; want detect-only", mode, err)
	}
	if providers, err := tenants.TenantCredentialProviders(ctx, "acme"); err != nil || len(providers) != 1 || providers[0] != "anthropic_direct" {
		t.Errorf("providers = %v, %v; want [anthropic_direct]", providers, err)
	}
	if _, err := tenants.TenantElicitationIntegrity(ctx, "globex"); err == nil {
		t.Error("a missing tenant read no error")
	}
	if _, err := tenants.TenantCredentialProviders(ctx, "globex"); err == nil {
		t.Error("a missing tenant read no error")
	}
	empty := NewTenants(nil, nil, nil)
	if mode, err := empty.TenantElicitationIntegrity(ctx, "acme"); err != nil || mode != "" {
		t.Errorf("no store: integrity = %q, %v", mode, err)
	}
	if providers, err := empty.TenantCredentialProviders(ctx, "acme"); err != nil || providers != nil {
		t.Errorf("no store: providers = %v, %v", providers, err)
	}
}

// TestDelegatorRefusalAnswersTheCreationFence checks that a suspended
// tenant and a deleted owner refuse a delegated child with the fence's
// codes, that a soft-disabled owner keeps delegating, and that an active
// owner and tenant, or no stores at all, are admitted.
// spec: GWSEAM-DelegatorRefusal
func TestDelegatorRefusalAnswersTheCreationFence(t *testing.T) {
	ctx := context.Background()
	store := tenantstore.NewMemory()
	seedTenant(t, store, tenantstore.Tenant{ID: "acme"})
	seedTenant(t, store, tenantstore.Tenant{ID: "globex", Suspended: true})
	users := userstore.NewMemory()
	for _, u := range []userstore.User{
		{Subject: "alice", TenantID: "acme"},
		{Subject: "bob", TenantID: "acme", Disabled: true},
	} {
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	tenants := NewTenants(store, users, nil)
	for _, tc := range []struct {
		tenant, user, wantCode string
	}{
		{"acme", "alice", ""},
		{"acme", "bob", ""},
		{"globex", "carol", sessionstore.RefusalTenantSuspended},
	} {
		ref, err := tenants.DelegatorRefusal(ctx, tc.tenant, tc.user)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.tenant, tc.user, err)
		}
		switch {
		case tc.wantCode == "" && ref != nil:
			t.Errorf("%s/%s refused with %+v, want admitted", tc.tenant, tc.user, ref)
		case tc.wantCode != "" && (ref == nil || ref.Code != tc.wantCode || ref.Message == ""):
			t.Errorf("%s/%s = %+v, want a %s refusal", tc.tenant, tc.user, ref, tc.wantCode)
		}
	}
	if ref, err := NewTenants(nil, nil, nil).DelegatorRefusal(ctx, "acme", "alice"); err != nil || ref != nil {
		t.Errorf("no stores = %+v, %v; want admitted", ref, err)
	}
}

// TestListTenantsReadsTheListing checks that the tenants come from the
// listing the composition root hands in, and none without one.
// spec: GWSEAM-ListTenants
func TestListTenantsReadsTheListing(t *testing.T) {
	want := errors.New("listing down")
	list := func(context.Context) ([]string, error) { return []string{"default", "acme"}, nil }
	if ids, err := NewTenants(nil, nil, list).ListTenants(context.Background()); err != nil || len(ids) != 2 || ids[1] != "acme" {
		t.Errorf("ListTenants = %v, %v", ids, err)
	}
	failing := func(context.Context) ([]string, error) { return nil, want }
	if _, err := NewTenants(nil, nil, failing).ListTenants(context.Background()); !errors.Is(err, want) {
		t.Errorf("a failed listing = %v, want %v", err, want)
	}
	if ids, err := NewTenants(nil, nil, nil).ListTenants(context.Background()); err != nil || ids != nil {
		t.Errorf("no listing = %v, %v", ids, err)
	}
}

// TestResolveEnvironmentsAttachesTheCallersResolution checks that the
// resolution of the context's principal, with its membership and the
// tenant's environments, reaches the context the environment conventions
// read, and that no principal or no registry leaves the context unchanged.
// spec: GWSEAM-ResolveEnvironments
func TestResolveEnvironmentsAttachesTheCallersResolution(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	envs := environmentstore.NewMemory()
	for _, env := range []environmentstore.Environment{
		{Name: "dev", TenantID: "acme", Members: []environmentstore.Member{{Identity: environmentstore.Identity{Type: "oidc-user", Value: "alice"}, Role: environment.RoleCreator}}},
		{Name: "prod", TenantID: "acme"},
	} {
		if err := envs.Create(ctx, env); err != nil {
			t.Fatalf("seed environment: %v", err)
		}
	}
	resolver := NewEnvironments(envs, tenants, "deny-all")
	alice := authmw.WithPrincipal(ctx, authmw.Principal{Subject: "alice", TenantID: "acme"})
	got, err := resolver.ResolveEnvironments(alice)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	res := environmentmw.FromContext(got)
	if !res.Configured || res.TenantID != "acme" || res.Policy != "deny-all" || len(res.AllEnvironments) != 2 ||
		len(res.MemberEnvironments) != 1 || res.MemberEnvironments[0].Name != "dev" {
		t.Fatalf("resolution = %+v", res)
	}
	if got, err := resolver.ResolveEnvironments(ctx); err != nil || environmentmw.FromContext(got).Configured {
		t.Errorf("no principal resolved %+v, %v", environmentmw.FromContext(got), err)
	}
	if got, err := NewEnvironments(nil, tenants, "").ResolveEnvironments(alice); err != nil || environmentmw.FromContext(got).Configured {
		t.Errorf("no environment registry resolved %+v, %v", environmentmw.FromContext(got), err)
	}
	if got, err := NewEnvironments(envs, tenants, "").ResolveEnvironments(authmw.WithPrincipal(ctx, authmw.Principal{Subject: "alice"})); err != nil || environmentmw.FromContext(got).Configured {
		t.Errorf("a principal with no tenant resolved %+v, %v", environmentmw.FromContext(got), err)
	}
}
