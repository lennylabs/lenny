// SPDX-License-Identifier: MIT

// Package proxylease is a tier-4 testinfra helper that stands up the
// §4.9 LLM reverse proxy delivery path against a stubbed upstream
// provider and provisions a pool-backed proxy-mode credential lease for
// it.
//
// It wires the same production components cmd/lenny-gateway's
// newLLMProxyServer wires — the credential-assignment service
// (pkg/gateway/credentials/credassign), the credential-lease store and
// in-memory upstream-credential cache the proxy reads, the native
// translator registry, and the llmproxy.Handler served at
// POST {proxyUrl}/v1/messages — so a test drives a live agent request
// through the proxy to an upstream stub and observes the §4.9 delivery
// contract: the lease token resolves to a lease, the translator injects
// the real upstream credential, the upstream response is translated back
// to the pod dialect, and the authoritative token usage is extracted.
//
// The upstream stub is the caller's tests/testinfra/stubs/llmprovider
// server; Options.UpstreamBaseURL points the proxy's translator at it.
// The real upstream key the assignment service caches is never returned
// to the pod, so a test can assert lease-token-to-real-key substitution
// by comparing the stub's received credential header against
// Fixture.UpstreamKey and Fixture.LeaseToken.
package proxylease

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// Options configures the proxy-lease fixture.
type Options struct {
	// UpstreamBaseURL is the base URL of the upstream provider stub the
	// proxy's translator forwards to (the llmprovider stub's URL).
	// Required.
	UpstreamBaseURL string
	// UpstreamKey is the real upstream credential the assignment service
	// caches for the pool and the translator injects on the upstream
	// call. It never leaves the gateway process, so the pod never sees
	// it. Empty selects a default sentinel key.
	UpstreamKey string
	// TenantID owns the minted lease. Empty selects "acme".
	TenantID string
	// SessionID the lease binds to. Empty selects "s-proxylease".
	SessionID string
}

// Fixture is a running §4.9 proxy delivery path with one provisioned
// pool-backed proxy lease.
type Fixture struct {
	// ProxyMessagesURL is the full URL an Anthropic-dialect agent pod
	// POSTs a Messages request to (POST {proxyUrl}/v1/messages).
	ProxyMessagesURL string
	// LeaseToken is the bearer lease token the pod presents as its SDK
	// API key. It resolves to the minted proxy lease; it is not the real
	// upstream key.
	LeaseToken string
	// UpstreamKey is the real upstream credential the proxy injects
	// upstream. A test asserts the stub received this value and not the
	// lease token.
	UpstreamKey string

	usage *captureRecorder
}

// Start provisions a proxy-mode credential pool whose single credential
// carries opts.UpstreamKey, mints a proxy lease from it through the
// production credential-assignment service, and serves the §4.9 proxy
// handler (wired as cmd/lenny-gateway wires it) on an httptest server
// pointed at opts.UpstreamBaseURL. It registers a t.Cleanup that closes
// the server.
func Start(t testing.TB, opts Options) *Fixture {
	t.Helper()
	if opts.UpstreamBaseURL == "" {
		t.Fatal("proxylease.Start: UpstreamBaseURL is required")
	}
	upstreamKey := opts.UpstreamKey
	if upstreamKey == "" {
		upstreamKey = "sk-upstream-real-secret-do-not-leak"
	}
	tenantID := opts.TenantID
	if tenantID == "" {
		tenantID = "acme"
	}
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = "s-proxylease"
	}

	// The lease store the proxy resolves a bearer token against and the
	// upstream-credential cache it injects from are the same two instances
	// the assignment service writes, exactly as buildExecutorAndCredentials
	// shares them in the gateway.
	leases := credleasestore.New()
	creds := credcache.New()
	assign := credassign.New(leases, creds)

	const poolName = "proxylease-pool"
	// ProxyURL is the pod-facing endpoint the mint records on the lease's
	// proxy config. The proxy forwards upstream via the translator's
	// BaseURL, so the recorded value does not affect forwarding; a
	// non-empty placeholder satisfies the mint's proxyUrl validation. The
	// fixture exposes the actually-served URL as ProxyMessagesURL.
	assign.RegisterPool(credassign.Pool{
		Name:         poolName,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://lenny-llm-proxy.internal/llm-proxy",
		ProxyDialect: string(credential.ProxyDialectAnthropic),
		Credentials: []credassign.PoolCredential{
			{ID: "cred-1", APIKey: upstreamKey, Healthy: true},
		},
	})

	lease, err := assign.Assign(poolName, sessionID, "", tenantID)
	if err != nil {
		t.Fatalf("proxylease.Start: mint proxy lease: %v", err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("proxylease.Start: minted lease carries no proxy token: %+v", lease)
	}

	// Point the anthropic_direct translator at the upstream stub. The
	// gateway's flag surface has no anthropic base-URL override, so the
	// helper constructs the registry directly rather than through
	// buildLLMTranslatorRegistry; the Handler wiring below otherwise
	// mirrors cmd/lenny-gateway's newLLMProxyServer.
	registry := llmproxy.NewTranslatorRegistry(
		&llmproxy.AnthropicDirectTranslator{
			BaseURL:                 opts.UpstreamBaseURL,
			DefaultAnthropicVersion: "2023-06-01",
		},
	)

	rec := &captureRecorder{}
	handler := &llmproxy.Handler{
		Leases:      leases,
		Translators: registry,
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: creds,
		Usage:       rec,
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &Fixture{
		ProxyMessagesURL: srv.URL + "/v1/messages",
		LeaseToken:       lease.Proxy.LeaseToken,
		UpstreamKey:      upstreamKey,
		usage:            rec,
	}
}

// LastUsage returns the authoritative token usage the proxy extracted
// from the most recent upstream response and recorded, and ok == false
// when the proxy has recorded no usage yet.
func (f *Fixture) LastUsage() (usage llmproxy.Usage, ok bool) {
	return f.usage.last()
}

// captureRecorder records the authoritative usage the proxy extracts so a
// test can assert the §4.9 extracted-count contract without the full
// billing pipeline. It never signals budget exhaustion.
type captureRecorder struct {
	mu     sync.Mutex
	usage  llmproxy.Usage
	called bool
}

func (c *captureRecorder) RecordUsage(_ context.Context, _ credential.Lease, usage llmproxy.Usage) (bool, llmproxy.Outcome) {
	c.mu.Lock()
	c.usage = usage
	c.called = true
	c.mu.Unlock()
	return false, llmproxy.OutcomeGranted
}

func (c *captureRecorder) last() (llmproxy.Usage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage, c.called
}
