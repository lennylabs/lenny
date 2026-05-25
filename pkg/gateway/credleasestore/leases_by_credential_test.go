// SPDX-License-Identifier: MIT

package credleasestore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
)

// spec: §4.9 lines 1640-1652 — look up all active leases backed by a
// revoked credential so the emergency-revocation handler can terminate
// them and report the count.

// TestLeasesByCredentialMatchesSourceAwareKey confirms LeasesByCredential
// returns exactly the leases whose source-aware credential identity
// equals the key, and that a pool key never aliases a user key.
func TestLeasesByCredentialMatchesSourceAwareKey(t *testing.T) {
	s := credleasestore.New()
	// Two leases against (claude-prod, key-1), one against a different
	// credential in the same pool, and one user-backed lease that shares
	// no keyspace with the pool entries.
	for _, l := range []credential.Lease{
		proxyLease("cl_a", "lt-a"),
		proxyLease("cl_b", "lt-b"),
	} {
		if err := s.Put(l); err != nil {
			t.Fatalf("Put %s: %v", l.LeaseID, err)
		}
	}
	other := proxyLease("cl_c", "lt-c")
	other.CredentialID = "key-2"
	if err := s.Put(other); err != nil {
		t.Fatalf("Put other: %v", err)
	}

	key := credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"}
	got := s.LeasesByCredential(key)
	if len(got) != 2 {
		t.Fatalf("LeasesByCredential returned %d leases, want 2: %+v", len(got), got)
	}
	for _, l := range got {
		if l.CredentialKey() != key {
			t.Errorf("returned lease %s has key %+v, want %+v", l.LeaseID, l.CredentialKey(), key)
		}
	}

	// A different credential in the same pool yields its own lease only.
	if k2 := s.LeasesByCredential(credential.CredentialKey{
		Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-2",
	}); len(k2) != 1 || k2[0].LeaseID != "cl_c" {
		t.Errorf("LeasesByCredential(key-2) = %+v, want [cl_c]", k2)
	}

	// A user key in the (vacuous today) user keyspace matches nothing.
	if u := s.LeasesByCredential(credential.CredentialKey{
		Source: credential.SourceUser, TenantID: "acme", CredentialRef: "ref-1",
	}); len(u) != 0 {
		t.Errorf("LeasesByCredential(user key) = %+v, want none", u)
	}
}

// TestLeasesByCredentialUnknownReturnsNone confirms a key with no leases
// yields an empty result rather than a panic.
func TestLeasesByCredentialUnknownReturnsNone(t *testing.T) {
	s := credleasestore.New()
	if got := s.LeasesByCredential(credential.CredentialKey{
		Source: credential.SourcePool, PoolID: "nope", CredentialID: "nope",
	}); len(got) != 0 {
		t.Errorf("LeasesByCredential on empty store = %+v, want none", got)
	}
}
