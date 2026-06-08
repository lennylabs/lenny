// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
)

// fakeRevoker records the pool credential IDs a credential-lease
// revocation drops on the renewal worker. It stands in for
// *credrenewal.Worker, whose Revoke has the same signature.
type fakeRevoker struct {
	mu      sync.Mutex
	revoked []string
}

func (f *fakeRevoker) Revoke(credentialID string) {
	f.mu.Lock()
	f.revoked = append(f.revoked, credentialID)
	f.mu.Unlock()
}

func (f *fakeRevoker) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

// poolKey builds a pool-backed §4.9 credential identity.
func poolKey(poolID, credID string) credential.CredentialKey {
	return credential.CredentialKey{Source: credential.SourcePool, PoolID: poolID, CredentialID: credID}
}

// userKey builds a user-backed §4.9 credential identity.
func userKey(tenantID, ref string) credential.CredentialKey {
	return credential.CredentialKey{Source: credential.SourceUser, TenantID: tenantID, CredentialRef: ref}
}

// mustEncode marshals a credential key the way Revoke publishes it onto
// the pub/sub channel.
func mustEncode(t *testing.T, key credential.CredentialKey) []byte {
	t.Helper()
	b, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal credential key: %v", err)
	}
	return b
}

// TestChannelMatchesCredentialDenyList confirms the credential-lease
// revocation propagator publishes on the same Redis channel the §4.9
// credential-deny-list propagator uses. A credential-lease revocation
// and a deny-list revocation are the same fleet-wide event; they share
// one channel rather than a second pub/sub mechanism.
func TestChannelMatchesCredentialDenyList(t *testing.T) {
	if Channel != "credential:denylist:events" {
		t.Errorf("Channel = %q, want the §4.9 credential-deny-list channel", Channel)
	}
}

// TestRevokeAppliesLocallyWithNilBus confirms Revoke adds the credential
// to the wrapped deny list and drops the renewal worker's tracked leases
// with no Bus wired, the single-replica mode.
func TestRevokeAppliesLocallyWithNilBus(t *testing.T) {
	local := denylist.New()
	worker := &fakeRevoker{}
	p := New(local, worker, nil)

	key := poolKey("pool-1", "cred-1")
	p.Revoke(key)

	if !local.Revoked(key) {
		t.Error("after Revoke, the wrapped deny list does not report the credential revoked")
	}
	if !p.Revoked(key) {
		t.Error("Propagator.Revoked should delegate to the wrapped deny list")
	}
	if got := worker.calls(); len(got) != 1 || got[0] != "cred-1" {
		t.Errorf("renewal worker revoked %v, want [cred-1]", got)
	}
}

// TestRevokeWithNilWorkerUpdatesDenyListOnly confirms a propagator built
// with no renewal worker — a gateway with no credential pools — still
// revokes onto the deny list and does not panic.
func TestRevokeWithNilWorkerUpdatesDenyListOnly(t *testing.T) {
	local := denylist.New()
	p := New(local, nil, nil)

	key := poolKey("pool-1", "cred-1")
	p.Revoke(key)
	if !local.Revoked(key) {
		t.Error("with no renewal worker the deny list should still be updated")
	}
}

// TestUserKeyDoesNotRevokeRenewalWorker confirms a user-backed
// credential revocation updates the deny list but does not call the
// renewal worker: the worker tracks leases by the pool credential id,
// which a user-backed key does not carry.
func TestUserKeyDoesNotRevokeRenewalWorker(t *testing.T) {
	local := denylist.New()
	worker := &fakeRevoker{}
	p := New(local, worker, nil)

	key := userKey("acme", "anthropic-key")
	p.Revoke(key)

	if !local.Revoked(key) {
		t.Error("a user-backed credential revocation should update the deny list")
	}
	if got := worker.calls(); len(got) != 0 {
		t.Errorf("renewal worker revoked %v for a user-backed key, want no calls", got)
	}
}

// TestRevocationFansOutToPeerReplica is the cross-replica fan-out: a
// credential-lease revocation on one replica's propagator is observed by
// a second replica's propagator. The publisher's Revoke encodes the
// credential key onto the channel; the peer's subscribe-side apply
// decodes it and revokes on its own deny list and renewal worker. With
// no Redis the encoded payload is fed directly, exercising the same
// encode/decode path the Bus carries verbatim.
func TestRevocationFansOutToPeerReplica(t *testing.T) {
	// Replica A: the revocation originates here.
	denyA := denylist.New()
	workerA := &fakeRevoker{}
	replicaA := New(denyA, workerA, nil)

	// Replica B: a peer that must converge on the revocation.
	denyB := denylist.New()
	workerB := &fakeRevoker{}
	replicaB := New(denyB, workerB, nil)

	key := poolKey("claude-prod", "key-2")

	// A revokes; the payload it would publish is the JSON encoding of
	// the credential key.
	replicaA.Revoke(key)
	if !denyA.Revoked(key) {
		t.Fatal("replica A did not revoke the credential locally")
	}

	// B has not seen the revocation yet.
	if denyB.Revoked(key) {
		t.Fatal("replica B revoked the credential before receiving the fan-out")
	}

	// The Bus delivers the published payload to B's subscribe handler.
	replicaB.apply(mustEncode(t, key))

	if !denyB.Revoked(key) {
		t.Error("replica B did not converge on the revoked credential after the fan-out")
	}
	if got := workerB.calls(); len(got) != 1 || got[0] != "key-2" {
		t.Errorf("replica B renewal worker revoked %v, want [key-2]", got)
	}
}

// TestRevocationFansOutDropsPeerRenewalWorkerLeases drives the fan-out
// against a real credrenewal.Worker on the peer replica: a peer's
// credential-lease revocation must drop every lease the peer's renewal
// worker tracks against the revoked credential, so no replica
// proactively renews a credential that is no longer trustworthy.
func TestRevocationFansOutDropsPeerRenewalWorkerLeases(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Replica B runs a renewal worker tracking two leases on key-2 and
	// one on an unrelated credential.
	peerWorker := credrenewal.New(noopRenewer{}, credrenewal.Options{})
	peerWorker.Track(credrenewal.Lease{
		LeaseID: "lease-a", CredentialID: "key-2",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	peerWorker.Track(credrenewal.Lease{
		LeaseID: "lease-b", CredentialID: "key-2",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	peerWorker.Track(credrenewal.Lease{
		LeaseID: "lease-c", CredentialID: "key-9",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	replicaB := New(denylist.New(), peerWorker, nil)

	// Replica A revokes key-2; replica B receives the fan-out.
	key := poolKey("claude-prod", "key-2")
	replicaA := New(denylist.New(), &fakeRevoker{}, nil)
	replicaA.Revoke(key)
	replicaB.apply(mustEncode(t, key))

	// The peer renewal worker's next sweep drops both key-2 leases; the
	// key-9 lease survives.
	peerWorker.Tick(context.Background(), base)
	if peerWorker.Tracked() != 1 {
		t.Errorf("peer renewal worker tracks %d leases after the fan-out, want 1 (only the unrevoked credential)",
			peerWorker.Tracked())
	}
}

// TestApplyIgnoresMalformedPayload confirms a payload that does not
// decode is dropped without panicking, so a malformed message cannot
// stall the subscribe loop.
func TestApplyIgnoresMalformedPayload(t *testing.T) {
	worker := &fakeRevoker{}
	p := New(denylist.New(), worker, nil)

	p.apply([]byte("{not json"))
	p.apply(nil)

	if p.Len() != 0 {
		t.Errorf("malformed payloads mutated the deny list; Len = %d, want 0", p.Len())
	}
	if got := worker.calls(); len(got) != 0 {
		t.Errorf("malformed payloads revoked %v on the renewal worker, want no calls", got)
	}
}

// TestRevokeWithNilBusReportsNoError confirms a single-replica Revoke
// (nil Bus) reports nothing to the error handler: there is no publish
// to fail, and the credential is revoked locally regardless.
func TestRevokeWithNilBusReportsNoError(t *testing.T) {
	var handlerErr error
	p := New(denylist.New(), &fakeRevoker{}, nil,
		WithErrorHandler(func(err error) { handlerErr = err }))

	p.Revoke(poolKey("pool-1", "cred-1"))
	if handlerErr != nil {
		t.Errorf("a nil-Bus Revoke reported error %v, want none", handlerErr)
	}
}

// TestRunWithNilBusBlocksUntilCancel confirms Run on a propagator with a
// nil Bus blocks until the context is cancelled, so the gateway can
// start the subscribe goroutine unconditionally.
func TestRunWithNilBusBlocksUntilCancel(t *testing.T) {
	p := New(denylist.New(), &fakeRevoker{}, nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Run with a nil Bus returned before the context was cancelled")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with a nil Bus did not return after the context was cancelled")
	}
}

// noopRenewer is a credrenewal.Renewer that is never expected to run;
// the fan-out tests drive Revoke and Tick, not a renewal.
type noopRenewer struct{}

func (noopRenewer) Renew(context.Context, credrenewal.Lease) (credrenewal.Lease, error) {
	return credrenewal.Lease{}, nil
}

// TestRevokeHookFiresOnLocalRevoke confirms WithRevokeHook runs the §4.9
// line 1649 per-replica side effect on a local Revoke, with the credential
// key the revocation carried.
func TestRevokeHookFiresOnLocalRevoke(t *testing.T) {
	var got []credential.CredentialKey
	p := New(denylist.New(), nil, nil, WithRevokeHook(func(key credential.CredentialKey) {
		got = append(got, key)
	}))

	key := poolKey("claude-prod", "key-1")
	p.Revoke(key)

	if len(got) != 1 || got[0] != key {
		t.Fatalf("revoke hook keys = %v, want exactly %v", got, key)
	}
}

// TestRevokeHookFiresOnPeerApply confirms the hook also runs when a peer
// replica's revocation arrives over the subscribe loop, so the §4.9
// direct-mode rotate fans out fleet-wide and not only on the originating
// replica.
func TestRevokeHookFiresOnPeerApply(t *testing.T) {
	var got []credential.CredentialKey
	p := New(denylist.New(), nil, nil, WithRevokeHook(func(key credential.CredentialKey) {
		got = append(got, key)
	}))

	key := poolKey("claude-prod", "key-7")
	p.apply(mustEncode(t, key))

	if len(got) != 1 || got[0] != key {
		t.Fatalf("peer-apply hook keys = %v, want exactly %v", got, key)
	}
}

// TestRevokeHookReceivesUserKey confirms the hook is handed the raw key
// for both sources; the direct-mode rotate itself filters to pool-backed
// keys, so the propagator stays source-agnostic.
func TestRevokeHookReceivesUserKey(t *testing.T) {
	var got []credential.CredentialKey
	p := New(denylist.New(), nil, nil, WithRevokeHook(func(key credential.CredentialKey) {
		got = append(got, key)
	}))

	key := userKey("acme", "user-cred-1")
	p.Revoke(key)

	if len(got) != 1 || got[0] != key {
		t.Fatalf("revoke hook keys = %v, want exactly %v", got, key)
	}
}
