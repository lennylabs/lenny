// SPDX-License-Identifier: MIT

package credassign_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
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

	lease, err := svc.Assign("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1", "")
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
	if _, err := svc.Assign("no-such-pool", "s_1", "", ""); !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Errorf("error = %v, want ErrPoolNotFound", err)
	}
}

func TestAssignExhaustedPool(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("drained", credential.StrategyLeastLoaded,
		credassign.PoolCredential{ID: "key-1", APIKey: "sk", Healthy: false}))

	if _, err := svc.Assign("drained", "s_1", "", ""); !errors.Is(err, credential.ErrPoolExhausted) {
		t.Errorf("error = %v, want ErrPoolExhausted", err)
	}
}

func TestAssignLeastLoadedSpreadsAcrossCredentials(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-one"),
		healthyCred("key-2", "sk-two")))

	first, err := svc.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign 1: %v", err)
	}
	second, err := svc.Assign("claude-prod", "s_2", "", "")
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

	first, _ := svc.Assign("claude-prod", "s_1", "", "") // key-1, active{key-1:1}
	_, _ = svc.Assign("claude-prod", "s_2", "", "")      // key-2, active{key-2:1}
	svc.Release(first.LeaseID)                           // active{key-1:0}

	// key-1 is now the least loaded, so the next assignment picks it.
	third, err := svc.Assign("claude-prod", "s_3", "", "")
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

	lease, _ := svc.Assign("claude-prod", "s_1", "", "")
	svc.Release(lease.LeaseID)

	if _, ok := leases.GetByID(lease.LeaseID); ok {
		t.Error("the lease store still holds a released lease")
	}
}

func TestReleaseUnknownLeaseIsNoOp(t *testing.T) {
	svc, _, _ := newService(t)
	svc.Release("cl-absent") // must not panic
}

// TestReleaseSessionReturnsEveryLeaseToPool_spec_7_1 asserts the §7.1
// step 23 session-teardown release: every lease the session holds is
// removed from the store and its credential's slot freed, so a completed
// session does not leak pool capacity. A session may hold more than one
// lease (one per provider), so ReleaseSession must free them all.
func TestReleaseSessionReturnsEveryLeaseToPool_spec_7_1(t *testing.T) {
	svc, leases, _ := newService(t)
	svc.RegisterPool(proxyPool("anthropic", credential.StrategyLeastLoaded,
		healthyCred("key-a", "sk-a")))
	svc.RegisterPool(proxyPool("openai", credential.StrategyLeastLoaded,
		healthyCred("key-o", "sk-o")))

	// One session leases from two pools; a sibling session leases too.
	la, _ := svc.Assign("anthropic", "s_1", "", "")
	lo, _ := svc.Assign("openai", "s_1", "", "")
	other, _ := svc.Assign("anthropic", "s_2", "", "")
	if leases.Len() != 3 {
		t.Fatalf("lease store holds %d leases, want 3", leases.Len())
	}

	svc.ReleaseSession("s_1")

	// Both of s_1's leases are gone; s_2's lease is untouched.
	if _, ok := leases.GetByID(la.LeaseID); ok {
		t.Error("anthropic lease for s_1 still present after ReleaseSession")
	}
	if _, ok := leases.GetByID(lo.LeaseID); ok {
		t.Error("openai lease for s_1 still present after ReleaseSession")
	}
	if _, ok := leases.GetByID(other.LeaseID); !ok {
		t.Error("ReleaseSession(s_1) wrongly removed s_2's lease")
	}
	if leases.Len() != 1 {
		t.Errorf("lease store holds %d leases after release, want 1 (s_2's)", leases.Len())
	}

	// The anthropic pool's freed slot is reusable: the next assignment for
	// a new session succeeds against the same single credential, which it
	// could not if the active counter had leaked.
	if _, err := svc.Assign("anthropic", "s_3", "", ""); err != nil {
		t.Errorf("Assign after ReleaseSession: %v — the released slot did not return to the pool", err)
	}
}

func TestReleaseSessionUnknownSessionIsNoOp_spec_7_1(t *testing.T) {
	svc, leases, _ := newService(t)
	svc.RegisterPool(proxyPool("anthropic", credential.StrategyLeastLoaded,
		healthyCred("key-a", "sk-a")))
	lease, _ := svc.Assign("anthropic", "s_1", "", "")

	svc.ReleaseSession("")         // empty id: no panic, no effect
	svc.ReleaseSession("s_absent") // unknown session: no effect
	if _, ok := leases.GetByID(lease.LeaseID); !ok {
		t.Error("ReleaseSession for an unrelated/empty session removed a live lease")
	}
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

	lease, err := svc.Assign("direct-pool", "s_1", "", "")
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

// spec: §4.9 Proactive Lease Renewal — the renewal worker tracks each
// minted lease via the OnAssigned observer.

func TestOnAssignedObservesEachMintedLease(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	var observed []credassign.LeaseAssignment
	svc.OnAssigned(func(a credassign.LeaseAssignment) {
		observed = append(observed, a)
	})

	lease, err := svc.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if len(observed) != 1 {
		t.Fatalf("OnAssigned observer fired %d times, want 1", len(observed))
	}
	if observed[0].PoolName != "claude-prod" {
		t.Errorf("observed pool name = %q, want claude-prod", observed[0].PoolName)
	}
	if observed[0].Lease.LeaseID != lease.LeaseID {
		t.Errorf("observed lease id = %q, want the minted lease %q", observed[0].Lease.LeaseID, lease.LeaseID)
	}
}

func TestOnAssignedNotFiredOnFailedAssign(t *testing.T) {
	svc, _, _ := newService(t)
	fired := false
	svc.OnAssigned(func(credassign.LeaseAssignment) { fired = true })

	// An unknown pool fails before any lease is minted.
	if _, err := svc.Assign("no-such-pool", "s_1", "", ""); err == nil {
		t.Fatal("Assign of an unknown pool succeeded, want ErrPoolNotFound")
	}
	if fired {
		t.Error("the OnAssigned observer fired for a failed Assign")
	}
}

func TestProtoLeaseByIDResolvesARecordedLease(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	lease, err := svc.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	wire, err := svc.ProtoLeaseByID(lease.LeaseID)
	if err != nil {
		t.Fatalf("ProtoLeaseByID: %v", err)
	}
	if wire.GetLeaseId() != lease.LeaseID {
		t.Errorf("wire lease id = %q, want %q", wire.GetLeaseId(), lease.LeaseID)
	}
}

func TestProtoLeaseByIDUnknownLease(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.ProtoLeaseByID("absent"); err == nil {
		t.Error("ProtoLeaseByID for an unrecorded lease succeeded, want an error")
	}
}

// spec: §4.9 lines 1645, 1649 — RevokeCredential marks a credential
// unselectable so a §4.9 emergency-revocation step-5 replacement mint
// draws a different credential from the same pool, never the one revoked.

func TestRevokeCredentialSkipsRevokedOnReassign(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-1"), healthyCred("key-2", "sk-ant-2")))

	// The sticky-free least-loaded strategy ties toward the earlier
	// candidate, so a fresh pool assigns key-1 first.
	first, err := svc.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if first.CredentialID != "key-1" {
		t.Fatalf("first assignment = %q, want key-1", first.CredentialID)
	}

	// Revoking key-1 must force every later mint onto key-2 even though
	// key-1 carries fewer active leases (the least-loaded preference).
	svc.RevokeCredential("claude-prod", "key-1")
	for i := 0; i < 3; i++ {
		got, err := svc.Assign("claude-prod", "s_after", "", "")
		if err != nil {
			t.Fatalf("Assign after revoke: %v", err)
		}
		if got.CredentialID != "key-2" {
			t.Fatalf("post-revoke assignment %d = %q, want key-2 (key-1 revoked)", i, got.CredentialID)
		}
		svc.Release(got.LeaseID)
	}
}

func TestRevokeCredentialExhaustsSingleCredentialPool(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-1")))

	svc.RevokeCredential("claude-prod", "key-1")
	if _, err := svc.Assign("claude-prod", "s_1", "", ""); !errors.Is(err, credential.ErrPoolExhausted) {
		t.Fatalf("Assign from a pool whose only credential is revoked = %v, want ErrPoolExhausted", err)
	}
}

func TestRevokeCredentialUnknownPoolIsNoOp(t *testing.T) {
	svc, _, _ := newService(t)
	// Neither an unknown pool nor empty identifiers may panic.
	svc.RevokeCredential("absent", "key-1")
	svc.RevokeCredential("", "")
}
