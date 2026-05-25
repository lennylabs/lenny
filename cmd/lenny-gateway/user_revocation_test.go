// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

// spec: §11.4 full_revoke — the gateway-side fan-out adapters.

// poolLease returns a valid pool-backed proxy lease bound to session.
func poolLease(leaseID, sessionID string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-" + leaseID,
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   "lt-" + leaseID,
		},
	}
}

func TestUserLeaseRevokerRevokesAndDenies(t *testing.T) {
	leases := credleasestore.New()
	deny := denylist.New()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_a"))
	_ = leases.Put(poolLease("l3", "run_b")) // a session not being revoked

	revoker := &userLeaseRevoker{leases: leases, denyList: deny}
	n := revoker.RevokeUserLeases("acme", "alice@acme.com", []string{"run_a"})
	if n != 2 {
		t.Fatalf("RevokeUserLeases revoked %d leases, want 2", n)
	}
	// The revoked leases are dropped from the store.
	if _, ok := leases.GetByID("l1"); ok {
		t.Error("revoked lease l1 still in the store")
	}
	if _, ok := leases.GetByID("l2"); ok {
		t.Error("revoked lease l2 still in the store")
	}
	// The untouched session's lease survives.
	if _, ok := leases.GetByID("l3"); !ok {
		t.Error("lease l3 for an unrevoked session was dropped")
	}
	// The revoked leases' credentials are on the §4.9 deny list, so the
	// LLM proxy rejects an in-flight request still carrying the token.
	if !deny.Revoked(poolLease("l1", "run_a").CredentialKey()) {
		t.Error("lease l1's credential is not on the deny list")
	}
	if !deny.Revoked(poolLease("l2", "run_a").CredentialKey()) {
		t.Error("lease l2's credential is not on the deny list")
	}
	if deny.Revoked(poolLease("l3", "run_b").CredentialKey()) {
		t.Error("lease l3's credential was wrongly added to the deny list")
	}
}

func TestUserLeaseRevokerNoLeases(t *testing.T) {
	leases := credleasestore.New()
	deny := denylist.New()
	revoker := &userLeaseRevoker{leases: leases, denyList: deny}
	if n := revoker.RevokeUserLeases("acme", "bob@acme.com", []string{"run_x"}); n != 0 {
		t.Errorf("RevokeUserLeases for a session with no leases revoked %d, want 0", n)
	}
	if deny.Len() != 0 {
		t.Errorf("the deny list gained %d entries for a no-lease user, want 0", deny.Len())
	}
}

func TestUserLeaseRevokerEmptySessionSet(t *testing.T) {
	leases := credleasestore.New()
	deny := denylist.New()
	_ = leases.Put(poolLease("l1", "run_a"))
	revoker := &userLeaseRevoker{leases: leases, denyList: deny}
	if n := revoker.RevokeUserLeases("acme", "carol@acme.com", nil); n != 0 {
		t.Errorf("RevokeUserLeases with no sessions revoked %d, want 0", n)
	}
	if _, ok := leases.GetByID("l1"); !ok {
		t.Error("an unrelated lease was dropped for an empty session set")
	}
}

// userLeaseRevoker's deny-list dependency is satisfied by the
// cross-replica propagator as well as the raw deny list.
func TestUserLeaseRevokerAcceptsADenyListInterface(t *testing.T) {
	var _ credentialDenyList = denylist.New()
}

func TestPodTerminateFanOutSkipsUnboundSessions(t *testing.T) {
	// The registry holds a binding for run_a but not run_b. A session
	// with no binding on this replica is skipped and not counted a
	// failure — its pod is bound elsewhere or already released.
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"}) // nil Adapter

	fan := &podTerminateFanOut{registry: reg}
	res := fan.TerminateUserSessions(context.Background(), "acme", "alice@acme.com",
		[]string{"run_a", "run_b"})
	// run_a has a nil Adapter (no live connection) and run_b has no
	// binding; neither terminates and neither is a recorded failure.
	if res.PodsTerminated != 0 {
		t.Errorf("PodsTerminated = %d, want 0", res.PodsTerminated)
	}
	if len(res.FailedSessions) != 0 {
		t.Errorf("FailedSessions = %v, want none — an absent binding is not a failure", res.FailedSessions)
	}
}

func TestPodTerminateFanOutEmptySessionSet(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"})
	fan := &podTerminateFanOut{registry: reg}
	res := fan.TerminateUserSessions(context.Background(), "acme", "bob@acme.com", nil)
	if res.PodsTerminated != 0 || len(res.FailedSessions) != 0 {
		t.Errorf("an empty session set produced %+v, want a zero result", res)
	}
}

// The fan-out adapters satisfy the §11.4 admin-router interfaces they
// are wired against in WithUserRevocation.
var (
	_ admin.UserPodTerminator = (*podTerminateFanOut)(nil)
	_ admin.UserLeaseRevoker  = (*userLeaseRevoker)(nil)
	_ admin.UserTokenRevoker  = (*userTokenRevoker)(nil)
)
