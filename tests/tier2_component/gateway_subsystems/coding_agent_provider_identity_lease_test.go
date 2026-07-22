// SPDX-License-Identifier: MIT

//go:build component

// Component-tier coverage for the §26.4 gemini-cli and §26.5 codex
// per-provider-identity credential-lease scoping. Both reference
// runtimes declare more than one supportedProviders entry sharing a
// single credentialCapabilities.proxyDialect value, and §26.4/§26.5 both
// state the minted lease's provider scope tracks "the pool's selected
// provider identity" rather than the runtime name. This suite drives the
// real §4.9 credential-assignment service (pkg/gateway/credentials/credassign)
// against the real Postgres-backed lease store to confirm a pool
// configured for each declared identity mints (and persists) a lease
// scoped to that identity, not to its sibling.
package gateway_subsystems_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
)

// spec: §26.4 (reference-runtime-catalog.md) lines 252-254 —
// "supportedProviders: [gcp_vertex_gemini, google_ai_studio]." /
// "credentialCapabilities.proxyDialect: [google]." / "Provider scope:
// `llm.provider.google.inference` or `gcp.vertex.gemini.inference`
// depending on the pool's selected provider identity."; §26.5 lines
// 274-276 — "supportedProviders: [openai_direct, azure_openai]." /
// "credentialCapabilities.proxyDialect: [openai]." / "Provider scope:
// `llm.provider.openai.inference` or `azure.openai.inference`."
//
// diagnosis: a failure means the §4.9 credential-assignment service
// stopped threading a credential pool's configured Provider and
// ProxyDialect through into the CredentialLease it mints and persists in
// the real store, so a session bound to a pool declaring one of
// gemini-cli's or codex's supported provider identities would receive a
// lease scoped to the wrong upstream provider identity, even though the
// runtime-facing proxy dialect ("google" or "openai") looks unchanged.
// That is the exact deployment-combination gap §26.4/§26.5 describe: two
// provider identities share one declared dialect, so only the minted
// lease's Provider field (not the dialect) distinguishes them.
func TestCodingAgentProviderIdentitySelectsMatchingLeaseScope(t *testing.T) {
	store := realStore(t)
	svc := credassign.New(store, credcache.New())

	cases := []struct {
		name         string
		provider     credential.Provider
		proxyDialect string
	}{
		{"gemini-cli pool configured for gcp_vertex_gemini", credential.Provider("gcp_vertex_gemini"), "google"},
		{"gemini-cli pool configured for google_ai_studio", credential.Provider("google_ai_studio"), "google"},
		{"codex pool configured for openai_direct", credential.Provider("openai_direct"), "openai"},
		{"codex pool configured for azure_openai", credential.Provider("azure_openai"), "openai"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			poolName := "pool-" + newUUID(t)
			svc.RegisterPool(credassign.Pool{
				Name:         poolName,
				Provider:     tc.provider,
				DeliveryMode: credential.DeliveryProxy,
				Strategy:     credential.StrategyLeastLoaded,
				ProxyURL:     "https://gateway-internal:8443/llm-proxy",
				ProxyDialect: tc.proxyDialect,
				Credentials: []credassign.PoolCredential{
					{ID: "key-1", APIKey: "sk-real-" + newUUID(t), Healthy: true},
				},
			})

			lease, err := svc.Assign(poolName, "s-"+newUUID(t), "", "acme")
			if err != nil {
				t.Fatalf("Assign: %v", err)
			}
			if lease.Provider != tc.provider {
				t.Errorf("minted lease Provider = %q, want the pool's configured identity %q", lease.Provider, tc.provider)
			}
			if lease.Proxy == nil {
				t.Fatalf("minted lease carries no proxy materializedConfig for a proxy-mode pool")
			}
			if lease.Proxy.ProxyDialect != tc.proxyDialect {
				t.Errorf("minted lease ProxyDialect = %q, want %q", lease.Proxy.ProxyDialect, tc.proxyDialect)
			}

			// The §4.9 LLM reverse proxy resolves every request by
			// looking the bearer lease token up in the real store; the
			// provider identity must round-trip through that hot-path
			// lookup unchanged.
			resolved, ok := store.GetByToken(lease.Proxy.LeaseToken)
			if !ok {
				t.Fatalf("lease token did not resolve through the real store")
			}
			if resolved.Provider != tc.provider {
				t.Errorf("store-resolved lease Provider = %q, want %q", resolved.Provider, tc.provider)
			}
		})
	}
}
