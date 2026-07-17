// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §4.9 lines 1640-1652 — the gateway-side emergency-revocation
// lease terminator.

// credLease returns a pool-backed proxy lease against credID.
func credLease(leaseID, credID string) credential.Lease {
	l := poolLease(leaseID, "s_"+leaseID)
	l.CredentialID = credID
	return l
}

func TestPoolCredentialRevokerRevokesAndDenies(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	_ = prop // wired below for the §11.4 step-6 cross-replica marker
	_ = leases.Put(credLease("l1", "key-1"))
	_ = leases.Put(credLease("l2", "key-1")) // same credential
	_ = leases.Put(credLease("l3", "key-2")) // a different credential

	rev := &poolCredentialRevoker{leases: leases, denyList: prop}
	n := rev.RevokePoolCredentials(context.Background(), "claude-prod", []string{"key-1"})
	if n != 2 {
		t.Fatalf("RevokePoolCredentials affected %d leases, want 2", n)
	}
	// spec: §4.9 — the revoked leases are retained and resolvable so the
	// proxy denies each in place via the deny-list entry.
	for _, id := range []string{"l1", "l2"} {
		if _, ok := leases.GetByID(id); !ok {
			t.Errorf("revoked lease %s must be retained for the deny-list check", id)
		}
	}
	if _, ok := leases.GetByID("l3"); !ok {
		t.Error("lease l3 against a different credential was dropped")
	}
	key1 := credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"}
	if !deny.Revoked(key1) {
		t.Error("key-1 is not on the deny list after revoke")
	}
	key2 := credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-2"}
	if deny.Revoked(key2) {
		t.Error("key-2 was wrongly added to the deny list")
	}
}

// TestPoolCredentialRevokerPoolWide revokes every credential id in one
// call (the §4.9 pool-wide force-rotate), summing the leases affected. The
// leases are retained and denied in place, so the store keeps them.
//
// spec: §4.9 (retained-and-denied; the count is leases-affected).
func TestPoolCredentialRevokerPoolWide(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	_ = prop // wired below for the §11.4 step-6 cross-replica marker
	_ = leases.Put(credLease("l1", "key-1"))
	_ = leases.Put(credLease("l2", "key-2"))

	rev := &poolCredentialRevoker{leases: leases, denyList: prop}
	n := rev.RevokePoolCredentials(context.Background(), "claude-prod", []string{"key-1", "key-2"})
	if n != 2 {
		t.Fatalf("RevokePoolCredentials affected %d leases, want 2", n)
	}
	if deny.Len() != 2 {
		t.Errorf("deny list has %d entries, want 2", deny.Len())
	}
	if leases.Len() != 2 {
		t.Errorf("lease store has %d leases after pool-wide revoke, want 2 (retained and denied in place)", leases.Len())
	}
}

// TestPoolCredentialHealthReaderCountsLeases asserts the §24.5 row-2
// health reader returns the active lease count keyed by credential id,
// counting only the supplied ids and omitting credentials with no lease.
func TestPoolCredentialHealthReaderCountsLeases(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(credLease("l1", "key-1"))
	_ = leases.Put(credLease("l2", "key-1"))
	_ = leases.Put(credLease("l3", "key-2"))

	h := &poolCredentialHealthReader{leases: leases}
	counts := h.PoolCredentialLeaseCounts("claude-prod", []string{"key-1", "key-2", "key-3"})
	if counts["key-1"] != 2 {
		t.Errorf("key-1 lease count = %d, want 2", counts["key-1"])
	}
	if counts["key-2"] != 1 {
		t.Errorf("key-2 lease count = %d, want 1", counts["key-2"])
	}
	if _, ok := counts["key-3"]; ok {
		t.Errorf("key-3 has no lease but appears in counts: %v", counts)
	}
}

// TestPoolCredentialRevokerNoLeasesStillDenies confirms a credential
// with no live lease on this replica is still added to the deny list, so
// a peer replica's cached lease for it is rejected (§4.9 step 3/4).
func TestPoolCredentialRevokerNoLeasesStillDenies(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	_ = prop // wired below for the §11.4 step-6 cross-replica marker
	rev := &poolCredentialRevoker{leases: leases, denyList: prop}
	n := rev.RevokePoolCredentials(context.Background(), "claude-prod", []string{"key-1"})
	if n != 0 {
		t.Fatalf("RevokePoolCredentials terminated %d leases, want 0", n)
	}
	if !deny.Revoked(credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"}) {
		t.Error("a credential with no live lease was not added to the deny list")
	}
}

// The revoker satisfies the admin-router interface it is wired against
// in WithPoolCredentialRevocation, and its deny-list dependency is
// satisfied by the cross-replica propagator as well as the raw list.
var _ admin.PoolCredentialRevoker = (*poolCredentialRevoker)(nil)

// directCredLease returns a direct-delivery pool lease against credID.
func directCredLease(leaseID, credID string) credential.Lease {
	l := credLease(leaseID, credID)
	l.DeliveryMode = credential.DeliveryDirect
	return l
}

// rotateCall records one directModeRevocationRotator rotate invocation.
type rotateCall struct {
	lease    credential.Lease
	nextPool string
	trigger  credential.RotationTrigger
}

// newSyncRotator builds a rotator with synchronous dispatch so a test
// observes every rotate without goroutine timing.
func newSyncRotator(leases poolLeaseStore) (*directModeRevocationRotator, *[]rotateCall, *[][2]string) {
	var rotates []rotateCall
	var marks [][2]string
	r := &directModeRevocationRotator{
		leases: leases,
		markRevoked: func(poolID, credID string) {
			marks = append(marks, [2]string{poolID, credID})
		},
		rotate: func(l credential.Lease, nextPool string, trigger credential.RotationTrigger) {
			rotates = append(rotates, rotateCall{lease: l, nextPool: nextPool, trigger: trigger})
		},
		dispatch: func(fn func()) { fn() },
	}
	return r, &rotates, &marks
}

// TestDirectModeRotatorRotatesDirectLeasesOnly asserts the §4.9 line 1649
// step-5 rotate fires for direct-delivery leases against a revoked
// credential and skips proxy-mode leases (the deny list handles those).
func TestDirectModeRotatorRotatesDirectLeasesOnly(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(directCredLease("d1", "key-1")) // direct, revoked credential
	_ = leases.Put(directCredLease("d2", "key-1")) // direct, revoked credential
	_ = leases.Put(credLease("p1", "key-1"))       // proxy, revoked credential
	_ = leases.Put(directCredLease("d3", "key-2")) // direct, a different credential

	r, rotates, marks := newSyncRotator(leases)
	key := credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"}
	r.onRevoke(key)

	if len(*marks) != 1 || (*marks)[0] != [2]string{"claude-prod", "key-1"} {
		t.Fatalf("markRevoked calls = %v, want one (claude-prod,key-1)", *marks)
	}
	got := map[string]rotateCall{}
	for _, c := range *rotates {
		got[c.lease.LeaseID] = c
	}
	if len(got) != 2 {
		t.Fatalf("rotated %d leases, want 2 (the direct leases against key-1)", len(got))
	}
	for _, id := range []string{"d1", "d2"} {
		c, ok := got[id]
		if !ok {
			t.Errorf("direct lease %s against the revoked credential was not rotated", id)
			continue
		}
		if c.nextPool != "claude-prod" {
			t.Errorf("lease %s rotated from pool %q, want its own pool claude-prod", id, c.nextPool)
		}
		if c.trigger != credential.TriggerEmergencyRevocation {
			t.Errorf("lease %s rotated under trigger %q, want emergency_revocation", id, c.trigger)
		}
	}
	if _, ok := got["p1"]; ok {
		t.Error("proxy-mode lease p1 was rotated; the deny list should handle proxy mode")
	}
	if _, ok := got["d3"]; ok {
		t.Error("direct lease d3 against a different credential was rotated")
	}
}

// TestDirectModeRotatorMarksBeforeRotate asserts the credential is marked
// unselectable before any rotate is dispatched, so a replacement mint
// (§4.9 line 1649 "a different credential in the pool") never re-selects
// the credential just revoked.
func TestDirectModeRotatorMarksBeforeRotate(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(directCredLease("d1", "key-1"))

	var order []string
	r := &directModeRevocationRotator{
		leases:      leases,
		markRevoked: func(string, string) { order = append(order, "mark") },
		rotate: func(credential.Lease, string, credential.RotationTrigger) {
			order = append(order, "rotate")
		},
		dispatch: func(fn func()) { fn() },
	}
	r.onRevoke(credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"})

	if len(order) != 2 || order[0] != "mark" || order[1] != "rotate" {
		t.Fatalf("call order = %v, want [mark rotate]", order)
	}
}

// TestDirectModeRotatorIgnoresUserKey asserts a §11.4 user-backed
// revocation carries no pool credential and triggers no rotate.
func TestDirectModeRotatorIgnoresUserKey(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(directCredLease("d1", "key-1"))

	r, rotates, marks := newSyncRotator(leases)
	r.onRevoke(credential.CredentialKey{Source: credential.SourceUser, TenantID: "acme", CredentialRef: "user-cred"})

	if len(*rotates) != 0 || len(*marks) != 0 {
		t.Fatalf("user-backed revocation drove rotates=%v marks=%v, want none", *rotates, *marks)
	}
}

// TestRevocationRotateThroughPropagatorPicksDifferentCredential wires the
// three new pieces together — the credential-lease propagator's revoke
// hook, the direct-mode rotator, and a real credassign.Service — and
// asserts that a §4.9 emergency revocation of a credential drives a
// direct-delivery lease's replacement mint onto a *different* credential
// in the same pool (line 1649), proving the rotate does not hand the
// revoked credential back out.
func TestRevocationRotateThroughPropagatorPicksDifferentCredential(t *testing.T) {
	leases := credleasestore.New()
	svc := credassign.New(leases, credcache.New())
	svc.RegisterPool(credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-1", Healthy: true},
			{ID: "key-2", APIKey: "sk-ant-2", Healthy: true},
		},
	})

	// Seed a direct-mode lease against key-1, the way a session start would.
	seed, err := svc.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("seed Assign: %v", err)
	}
	if seed.CredentialID != "key-1" {
		t.Fatalf("seed lease credential = %q, want key-1", seed.CredentialID)
	}

	var replacement string
	rotator := &directModeRevocationRotator{
		leases:      leases,
		markRevoked: svc.RevokeCredential,
		rotate: func(l credential.Lease, nextPool string, trigger credential.RotationTrigger) {
			// The production rotate is proxyFallbackRotator.Rotate, which
			// mints from nextPool via the same Assign path used here.
			next, aerr := svc.Assign(nextPool, l.SessionID, "", "")
			if aerr != nil {
				t.Errorf("replacement Assign: %v", aerr)
				return
			}
			replacement = next.CredentialID
		},
		dispatch: func(fn func()) { fn() },
	}

	prop := credrenewalprop.New(denylist.New(), nil, nil,
		credrenewalprop.WithRevokeHook(rotator.onRevoke))

	prop.Revoke(credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"})

	if replacement != "key-2" {
		t.Fatalf("replacement lease credential = %q, want key-2 (key-1 revoked)", replacement)
	}
}
