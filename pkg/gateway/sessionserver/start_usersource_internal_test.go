// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// userSourceFixture builds a Server whose tenant policy resolves a
// provider to the user source and whose runtime declares the supplied
// proxy dialects, wiring a userCredChecker that reports the named provider
// available. It exercises the §4.9 user-source resolution + the line 1476
// runtime↔dialect boundary for a user provider.
func userSourceFixture(t *testing.T, preferred credential.PreferredSource, runtimeDialects []string, availableProvider string, pools ...credentialpoolstore.CredentialPool) *Server {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
		PreferredSource: preferred,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	rt := runtimestore.Runtime{Name: "claude-code", SupportedProviders: []string{"anthropic_direct"}}
	if runtimeDialects != nil {
		rt.CredentialCapabilities = &runtimestore.CredentialCapabilities{ProxyDialect: runtimeDialects}
	}
	if err := runtimes.Create(ctx, rt); err != nil {
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
		userCredChecker: func(_ context.Context, tenantID, userID, provider string) bool {
			return tenantID == "acme" && userID == "alice@acme.com" && provider == availableProvider
		},
	}
}

// TestResolveUserSource_spec_4_9_1347 confirms a provider resolved to the
// user source is returned in the userProviders list (not the pool map) and
// passes the runtime dialect check when the runtime speaks the provider's
// canonical proxy dialect.
func TestResolveUserSource_spec_4_9_1347(t *testing.T) {
	s := userSourceFixture(t, credential.PreferredSourceUser, []string{"anthropic"}, "anthropic_direct")
	pools, users, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(pools) != 0 {
		t.Errorf("pool map = %v, want empty (user source)", pools)
	}
	if len(users) != 1 || users[0] != "anthropic_direct" {
		t.Fatalf("userProviders = %v, want [anthropic_direct]", users)
	}
}

// TestResolveUserSourceDialectMismatch_spec_4_9_1476 confirms a user
// provider whose canonical dialect the runtime does not declare rejects the
// session with the proxy-dialect error before any pod is claimed.
func TestResolveUserSourceDialectMismatch_spec_4_9_1476(t *testing.T) {
	// Runtime speaks only "openai"; the anthropic_direct user credential
	// needs the "anthropic" dialect.
	s := userSourceFixture(t, credential.PreferredSourceUser, []string{"openai"}, "anthropic_direct")
	_, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	var dialectErr *PoolProxyDialectError
	if err == nil || !asPoolProxyDialectError(err, &dialectErr) {
		t.Fatalf("err = %v, want *PoolProxyDialectError", err)
	}
	if dialectErr.Dialect != "anthropic" {
		t.Errorf("dialect = %q, want anthropic", dialectErr.Dialect)
	}
}

// TestResolveUserSourceFallsThroughToPool_spec_4_9_1372 confirms that with
// prefer-user-then-pool and no user credential available, resolution falls
// through to the pool source.
func TestResolveUserSourceFallsThroughToPool_spec_4_9_1372(t *testing.T) {
	// availableProvider is a name the checker never matches, so the user
	// source is unavailable and prefer-user-then-pool routes to the pool.
	s := userSourceFixture(
		t, credential.PreferUserThenPool, []string{"anthropic"}, "none",
		poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive),
	)
	pools, users, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("userProviders = %v, want empty (no user credential)", users)
	}
	if pools["anthropic_direct"] != "claude-prod" {
		t.Errorf("pool map = %v, want anthropic_direct→claude-prod fallthrough", pools)
	}
}

func asPoolProxyDialectError(err error, target **PoolProxyDialectError) bool {
	e, ok := err.(*PoolProxyDialectError)
	if ok {
		*target = e
	}
	return ok
}
