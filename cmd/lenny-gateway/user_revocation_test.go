// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// newLocalRevocationDenyList builds the §11.4 step-6 deny list the
// userLeaseRevoker requires: a *credrenewalprop.Propagator wrapping a
// fresh single-replica DenyList with a nil bus. The propagator is a
// local-only pass-through in this configuration (no pub/sub) but still
// satisfies the cross-replica marker so the §11.4 wiring contract is
// honored. The returned denylist.DenyList is the underlying state for
// assertions; the propagator is what the userLeaseRevoker drives.
// spec: §11.4 step 6.
func newLocalRevocationDenyList() (*credrenewalprop.Propagator, *denylist.DenyList) {
	dl := denylist.New()
	return credrenewalprop.New(dl, nil, nil), dl
}

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
	prop, deny := newLocalRevocationDenyList()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_a"))
	_ = leases.Put(poolLease("l3", "run_b")) // a session not being revoked

	revoker := &userLeaseRevoker{leases: leases, denyList: prop}
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
	prop, deny := newLocalRevocationDenyList()
	revoker := &userLeaseRevoker{leases: leases, denyList: prop}
	if n := revoker.RevokeUserLeases("acme", "bob@acme.com", []string{"run_x"}); n != 0 {
		t.Errorf("RevokeUserLeases for a session with no leases revoked %d, want 0", n)
	}
	if deny.Len() != 0 {
		t.Errorf("the deny list gained %d entries for a no-lease user, want 0", deny.Len())
	}
}

func TestUserLeaseRevokerEmptySessionSet(t *testing.T) {
	leases := credleasestore.New()
	prop, _ := newLocalRevocationDenyList()
	_ = leases.Put(poolLease("l1", "run_a"))
	revoker := &userLeaseRevoker{leases: leases, denyList: prop}
	if n := revoker.RevokeUserLeases("acme", "carol@acme.com", nil); n != 0 {
		t.Errorf("RevokeUserLeases with no sessions revoked %d, want 0", n)
	}
	if _, ok := leases.GetByID("l1"); !ok {
		t.Error("an unrelated lease was dropped for an empty session set")
	}
}

// TestUserLeaseRevokerRequiresCrossReplicaPropagator asserts the §11.4
// step-6 wiring contract: only *credrenewalprop.Propagator satisfies
// credentialDenyList. A bare *denylist.DenyList carries the revocation
// only on the calling replica and is no longer accepted by the
// interface, so an accidental future wiring downgrade fails at compile
// time. spec: §11.4 step 6.
func TestUserLeaseRevokerRequiresCrossReplicaPropagator_spec_11_4_step_6(t *testing.T) {
	// Compile-time assertion that the propagator satisfies the
	// interface; the file would not compile if it did not.
	var _ credentialDenyList = (*credrenewalprop.Propagator)(nil)
	// Sibling assertion in a runtime form so the test names the bare
	// DenyList path explicitly. The line is dead code — the if check
	// is unreachable — but `denylist.DenyList` not satisfying the
	// interface is the entire point.
	var d any = denylist.New()
	if _, ok := d.(credentialDenyList); ok {
		t.Fatalf("bare *denylist.DenyList satisfies credentialDenyList; the cross-replica marker is missing — §11.4 step 6 would be replica-local")
	}
}

func TestPodTerminateFanOutSkipsUnboundSessions(t *testing.T) {
	// The registry holds a binding for run_a but not run_b. A session
	// with no binding on this replica is skipped and not counted a
	// failure — its pod is bound elsewhere (a peer replica terminates it
	// on its own §11.4 step-2 subscriber, F-11.4.3) or already released.
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

// TestPodTerminateFanOutPublishesCrossReplica is the §11.4 step-2 fix:
// the handling replica's full_revoke fans the Terminate request out over
// Redis pub/sub so peer replicas terminate the pods they coordinate.
// Without the publish, a pod bound on a peer replica survives until the
// §8.10 orphan sweep. F-11.4.3.
func TestPodTerminateFanOutPublishesCrossReplica_spec_11_4_step_2(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	// Replica A is the handling replica and holds none of the revoked
	// sessions locally. Replica B coordinates run_b; its local terminator
	// must observe the request A publishes. A concrete pod adapter needs
	// a live gRPC connection, so replica B is exercised through a
	// recording LocalTerminator (the §4.7 Terminate RPC itself is covered
	// by the propagator package and the local-only fan-out tests above).
	peerB := &recordingPeer{}
	propB := podterminateprop.New(peerB, pubsub.New(clientB), "replica-B")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go propB.Run(ctx)

	fanA := &podTerminateFanOut{registry: podsession.NewRegistry()}
	fanA.prop = podterminateprop.New(fanA, pubsub.New(clientA), "replica-A")

	// miniredis drops a publish that lands before B's SUBSCRIBE registers,
	// so re-issue the full_revoke until B observes the request.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fanA.TerminateUserSessions(ctx, "acme", "alice@acme.com", []string{"run_b"})
		if peerB.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := peerB.last()
	if peerB.count() == 0 {
		t.Fatal("replica B never received the §11.4 cross-replica Terminate request")
	}
	if got.UserID != "alice@acme.com" || got.Reason != userRevokeReason || got.Origin != "replica-A" {
		t.Errorf("replica B received %+v, want acme/alice USER_REVOKED from replica-A", got)
	}
	if len(got.SessionIDs) != 1 || got.SessionIDs[0] != "run_b" {
		t.Errorf("replica B received sessions %v, want [run_b]", got.SessionIDs)
	}
}

// recordingPeer is a podterminateprop.LocalTerminator that records the
// requests a peer replica's subscriber applies, standing in for the pod
// adapter fan-out on the receiving replica.
type recordingPeer struct {
	mu       sync.Mutex
	requests []podterminateprop.Request
}

func (r *recordingPeer) TerminateLocal(_ context.Context, req podterminateprop.Request) podterminateprop.Result {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return podterminateprop.Result{}
}

func (r *recordingPeer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recordingPeer) last() podterminateprop.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return podterminateprop.Request{}
	}
	return r.requests[len(r.requests)-1]
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
