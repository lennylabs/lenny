// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
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
		t.Fatalf("RevokePoolCredentials terminated %d leases, want 2", n)
	}
	for _, id := range []string{"l1", "l2"} {
		if _, ok := leases.GetByID(id); ok {
			t.Errorf("revoked lease %s still in the store", id)
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
// call (the §4.9 pool-wide force-rotate), summing the terminated leases.
func TestPoolCredentialRevokerPoolWide(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	_ = prop // wired below for the §11.4 step-6 cross-replica marker
	_ = leases.Put(credLease("l1", "key-1"))
	_ = leases.Put(credLease("l2", "key-2"))

	rev := &poolCredentialRevoker{leases: leases, denyList: prop}
	n := rev.RevokePoolCredentials(context.Background(), "claude-prod", []string{"key-1", "key-2"})
	if n != 2 {
		t.Fatalf("RevokePoolCredentials terminated %d leases, want 2", n)
	}
	if deny.Len() != 2 {
		t.Errorf("deny list has %d entries, want 2", deny.Len())
	}
	if leases.Len() != 0 {
		t.Errorf("lease store has %d leases after pool-wide revoke, want 0", leases.Len())
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
