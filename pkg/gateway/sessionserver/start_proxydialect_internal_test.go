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
)

// proxyDialectFixture builds a Server whose runtime declares the given
// credentialCapabilities.proxyDialect set (nil = no credentialCapabilities
// block) and whose single anthropic_direct pool carries deliveryMode +
// poolDialect, so resolveCredentialPools exercises the §4.9 line 1476
// runtime↔pool proxy-dialect admission boundary.
func proxyDialectFixture(t *testing.T, runtimeDialects []string, deliveryMode, poolDialect string) *Server {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	policy := credential.CredentialPolicy{
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
	pool := poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive)
	pool.DeliveryMode = deliveryMode
	pool.ProxyDialect = poolDialect
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, pool); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return &Server{
		tenants:    tenants,
		runtimes:   runtimes,
		credPools:  credPools,
		credRouter: credrouter.NewDefault(),
	}
}

// spec: §4.9 line 1476 — a proxy-mode pool whose proxyDialect the session
// runtime declares is accepted; the pool is assigned for its provider.
func TestResolveCredentialPoolsProxyDialectMatch_spec_4_9_1476(t *testing.T) {
	s := proxyDialectFixture(t, []string{"anthropic", "openai"}, "proxy", "anthropic")
	got, _, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "claude-prod" {
		t.Errorf("CredentialPools = %v, want anthropic_direct→claude-prod", got)
	}
}

// spec: §4.9 line 1476 — a proxy-mode pool whose proxyDialect the session
// runtime does NOT declare is rejected before a pod is claimed, with the
// verbatim INVALID_POOL_PROXY_DIALECT message.
func TestResolveCredentialPoolsProxyDialectMismatch_spec_4_9_1476(t *testing.T) {
	s := proxyDialectFixture(t, []string{"anthropic"}, "proxy", "openai")
	_, _, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	var dialectErr *PoolProxyDialectError
	if !errors.As(err, &dialectErr) {
		t.Fatalf("resolveCredentialPools err = %v, want *PoolProxyDialectError", err)
	}
	if dialectErr.Dialect != "openai" || dialectErr.Pool != "claude-prod" {
		t.Errorf("PoolProxyDialectError = %+v, want pool=claude-prod dialect=openai", dialectErr)
	}
	want := "pool proxyDialect openai is not declared in runtime credentialCapabilities.proxyDialect"
	if dialectErr.Error() != want {
		t.Errorf("Error() = %q, want %q", dialectErr.Error(), want)
	}
}

// spec: §4.9 line 1476 — a runtime that declares no credentialCapabilities
// block speaks no proxy dialect, so any proxy-mode pool is rejected.
func TestResolveCredentialPoolsProxyDialectNoCapabilities_spec_4_9_1476(t *testing.T) {
	s := proxyDialectFixture(t, nil, "proxy", "anthropic")
	_, _, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	var dialectErr *PoolProxyDialectError
	if !errors.As(err, &dialectErr) {
		t.Fatalf("resolveCredentialPools err = %v, want *PoolProxyDialectError", err)
	}
}

// spec: §4.9 line 1476 — a direct-mode pool declares no proxyDialect, so
// the dialect boundary does not apply and the pool is assigned even though
// the runtime declares no proxy dialect.
func TestResolveCredentialPoolsDirectModeSkipsDialect_spec_4_9_1476(t *testing.T) {
	s := proxyDialectFixture(t, nil, "direct", "")
	got, _, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "claude-prod" {
		t.Errorf("CredentialPools = %v, want anthropic_direct→claude-prod (direct mode, no dialect check)", got)
	}
}
