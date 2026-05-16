// SPDX-License-Identifier: MIT

package credassign_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
)

// spec: §4.9 — the credential-assignment service: select a pool
// credential, mint a lease, and populate the lease store and the
// credential cache.

// newService returns a credential-assignment service with fresh stores.
func newService(t *testing.T) (*credassign.Service, *credleasestore.Store, *credcache.Cache) {
	t.Helper()
	leases := credleasestore.New()
	creds := credcache.New()
	return credassign.New(leases, creds), leases, creds
}

// proxyPool returns a proxy-mode pool with the given credentials.
func proxyPool(name string, strategy credential.AssignmentStrategy, creds ...credassign.PoolCredential) credassign.Pool {
	return credassign.Pool{
		Name:         name,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     strategy,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials:  creds,
	}
}

func healthyCred(id, apiKey string) credassign.PoolCredential {
	return credassign.PoolCredential{ID: id, APIKey: apiKey, Healthy: true}
}

func TestAssignRecordsLeaseAndCachesCredential(t *testing.T) {
	svc, leases, creds := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	lease, err := svc.Assign("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if lease.PoolID != "claude-prod" || lease.CredentialID != "key-1" {
		t.Errorf("lease identity = %s/%s, want claude-prod/key-1", lease.PoolID, lease.CredentialID)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatal("proxy-mode lease has no lease token")
	}
	if lease.SpiffeURI != "spiffe://lenny.test/agent/claude-prod/pod-1" {
		t.Errorf("lease SpiffeURI = %q, want the issuing pod identity", lease.SpiffeURI)
	}

	// The lease is resolvable by its bearer token, the way the LLM
	// proxy resolves an inbound request.
	if got, ok := leases.GetByToken(lease.Proxy.LeaseToken); !ok || got.LeaseID != lease.LeaseID {
		t.Error("the minted lease is not resolvable by its token in the lease store")
	}
	// The real upstream credential is cached for the LLM proxy.
	if key, ok := creds.UpstreamCredential(lease); !ok || key != "sk-ant-real" {
		t.Errorf("cached credential = %q ok=%v, want sk-ant-real", key, ok)
	}
}

func TestAssignUnknownPool(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Assign("no-such-pool", "s_1", ""); !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Errorf("error = %v, want ErrPoolNotFound", err)
	}
}

func TestAssignExhaustedPool(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("drained", credential.StrategyLeastLoaded,
		credassign.PoolCredential{ID: "key-1", APIKey: "sk", Healthy: false}))

	if _, err := svc.Assign("drained", "s_1", ""); !errors.Is(err, credential.ErrPoolExhausted) {
		t.Errorf("error = %v, want ErrPoolExhausted", err)
	}
}

func TestAssignLeastLoadedSpreadsAcrossCredentials(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-one"),
		healthyCred("key-2", "sk-two")))

	first, err := svc.Assign("claude-prod", "s_1", "")
	if err != nil {
		t.Fatalf("Assign 1: %v", err)
	}
	second, err := svc.Assign("claude-prod", "s_2", "")
	if err != nil {
		t.Fatalf("Assign 2: %v", err)
	}
	if first.CredentialID == second.CredentialID {
		t.Errorf("both assignments used %q — least-loaded did not spread the load", first.CredentialID)
	}
}

func TestReleaseFreesTheCredentialSlot(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-one"),
		healthyCred("key-2", "sk-two")))

	first, _ := svc.Assign("claude-prod", "s_1", "")  // key-1, active{key-1:1}
	_, _ = svc.Assign("claude-prod", "s_2", "")       // key-2, active{key-2:1}
	svc.Release(first.LeaseID)                        // active{key-1:0}

	// key-1 is now the least loaded, so the next assignment picks it.
	third, err := svc.Assign("claude-prod", "s_3", "")
	if err != nil {
		t.Fatalf("Assign 3: %v", err)
	}
	if third.CredentialID != "key-1" {
		t.Errorf("after releasing key-1's lease, assignment picked %q, want key-1", third.CredentialID)
	}
}

func TestReleaseRemovesLeaseFromStore(t *testing.T) {
	svc, leases, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-one")))

	lease, _ := svc.Assign("claude-prod", "s_1", "")
	svc.Release(lease.LeaseID)

	if _, ok := leases.GetByID(lease.LeaseID); ok {
		t.Error("the lease store still holds a released lease")
	}
}

func TestReleaseUnknownLeaseIsNoOp(t *testing.T) {
	svc, _, _ := newService(t)
	svc.Release("cl-absent") // must not panic
}

func TestAssignDirectModePoolCachesCredential(t *testing.T) {
	svc, _, creds := newService(t)
	svc.RegisterPool(credassign.Pool{
		Name:         "direct-pool",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials:  []credassign.PoolCredential{healthyCred("key-1", "sk-direct")},
	})

	lease, err := svc.Assign("direct-pool", "s_1", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if lease.Proxy != nil {
		t.Errorf("direct-mode lease carries a proxy config: %+v", lease.Proxy)
	}
	if key, ok := creds.UpstreamCredential(lease); !ok || key != "sk-direct" {
		t.Errorf("cached credential = %q ok=%v, want sk-direct", key, ok)
	}
}
