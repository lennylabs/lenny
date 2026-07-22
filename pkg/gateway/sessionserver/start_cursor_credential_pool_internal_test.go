// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// cursorRuntime returns the §26.6 cursor-cli reference runtime's
// credential-relevant fields: its sole supportedProviders entry
// (cursor_direct) and its declared credentialCapabilities.proxyDialect
// (cursor). spec: §26.6 — "supportedProviders: [cursor_direct] ...
// credentialCapabilities.proxyDialect: [cursor]".
func cursorRuntime() runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:               "cursor-cli",
		SupportedProviders: []string{"cursor_direct"},
		CredentialCapabilities: &runtimestore.CredentialCapabilities{
			ProxyDialect: []string{"cursor"},
		},
	}
}

// cursorSessionRow is a session bound to the cursor-cli runtime, mirroring
// sessionRow() for the other §4.9 pre-claim fixtures in this package.
func cursorSessionRow() sessionstore.Session {
	return sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "cursor-cli"}
}

// cursorFixture builds a Server wired with a tenant whose credentialPolicy
// names a defaultPool for cursor_direct, the §26.6 cursor-cli runtime, and
// whatever credential pools are supplied (zero pools stands in for "no
// Cursor credential pool configured" — the deployer never created the
// CredentialPool object the policy's defaultPool names).
func cursorFixture(t *testing.T, pools ...credentialpoolstore.CredentialPool) *Server {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"cursor_direct": {DefaultPool: "cursor-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, cursorRuntime()); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	for _, p := range pools {
		if err := credPools.Create(ctx, p); err != nil {
			t.Fatalf("create pool %s: %v", p.Name, err)
		}
	}
	return &Server{
		tenants:    tenants,
		runtimes:   runtimes,
		credPools:  credPools,
		credRouter: credrouter.NewDefault(),
	}
}

// TestResolveCredentialPoolsCursorNoPoolConfiguredRejects pins the §26.6
// consequence of registering the cursor-cli runtime without a Cursor
// credential pool: the tenant's credentialPolicy names a defaultPool for
// cursor_direct, but no CredentialPool object with that name has been
// created (the deployer never configured one), so pre-claim resolves zero
// assignable pools for the provider and fails closed with
// ErrNoCredentialAvailable, matching the §4.9 line 1218 "every pool
// exhausted ... rejects with ErrNoCredentialAvailable before a pod is
// claimed" fail-closed contract extended to a pool that was never created
// at all.
//
// spec: §26.6 — "Deployers who do not configure a Cursor credential pool
// can only register this runtime in direct-delivery mode."; §4.9 lines
// 1216-1218 (Pre-Claim check).
func TestResolveCredentialPoolsCursorNoPoolConfiguredRejects(t *testing.T) {
	s := cursorFixture(t) // no credential pools created
	_, _, _, err := s.resolveCredentialPools(context.Background(), cursorSessionRow())
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Errorf("resolveCredentialPools = %v, want ErrNoCredentialAvailable (no Cursor credential pool configured)", err)
	}
}

// TestResolveCredentialPoolsCursorProxyPoolConfiguredSucceeds pins the
// complementary path: once the deployer configures a Cursor credential
// pool declaring deliveryMode: proxy and proxyDialect: cursor (matching
// the cursor-cli runtime's declared credentialCapabilities.proxyDialect),
// pre-claim resolves the pool and reports its effective proxy delivery
// mode, so proxy-mode credential delivery for cursor-cli becomes available
// once a Cursor pool exists.
//
// spec: §26.6 — "Deployers who do not configure a Cursor credential pool
// can only register this runtime in direct-delivery mode." (implying proxy
// delivery becomes available once one is configured); §4.9 line 1476
// (runtime↔pool proxy-dialect admission boundary).
func TestResolveCredentialPoolsCursorProxyPoolConfiguredSucceeds(t *testing.T) {
	pool := poolFixture("cursor-prod", "cursor_direct", credentialpoolstore.CredentialActive)
	pool.DeliveryMode = "proxy"
	pool.ProxyDialect = "cursor"
	s := cursorFixture(t, pool)

	got, deliveryModes, _, err := s.resolveCredentialPools(context.Background(), cursorSessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["cursor_direct"] != "cursor-prod" {
		t.Errorf("CredentialPools = %v, want cursor_direct→cursor-prod", got)
	}
	if deliveryModes["cursor-prod"] != "proxy" {
		t.Errorf("deliveryModes = %v, want cursor-prod→proxy", deliveryModes)
	}
}

// TestResolveCredentialPoolsCursorDirectPoolConfiguredSucceeds pins the
// direct-delivery counterpart named in the same §26.6 sentence: a Cursor
// credential pool configured with deliveryMode: direct resolves without
// requiring the runtime to declare any credentialCapabilities.proxyDialect
// entry, since direct-mode delivery does not route through the LLM proxy's
// dialect boundary (§4.9 line 1476 applies only to proxy-mode pools).
//
// spec: §26.6 — "Deployers who do not configure a Cursor credential pool
// can only register this runtime in direct-delivery mode."; §4.9 line 1476.
func TestResolveCredentialPoolsCursorDirectPoolConfiguredSucceeds(t *testing.T) {
	pool := poolFixture("cursor-prod", "cursor_direct", credentialpoolstore.CredentialActive)
	pool.DeliveryMode = "direct"
	s := cursorFixture(t, pool)

	got, deliveryModes, _, err := s.resolveCredentialPools(context.Background(), cursorSessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["cursor_direct"] != "cursor-prod" {
		t.Errorf("CredentialPools = %v, want cursor_direct→cursor-prod", got)
	}
	if deliveryModes["cursor-prod"] != "direct" {
		t.Errorf("deliveryModes = %v, want cursor-prod→direct", deliveryModes)
	}
}
