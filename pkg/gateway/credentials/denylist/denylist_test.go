// SPDX-License-Identifier: MIT

package denylist_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// spec: §4.9 — the credential deny list the LLM proxy checks on every
// upstream request.

// DenyList satisfies the §4.9 LLM proxy's DenyList interface.
var _ llmproxy.DenyList = (*denylist.DenyList)(nil)

func poolKey(poolID, credID string) credential.CredentialKey {
	return credential.CredentialKey{
		Source: credential.SourcePool, PoolID: poolID, CredentialID: credID,
	}
}

func userKey(tenantID, credRef string) credential.CredentialKey {
	return credential.CredentialKey{
		Source: credential.SourceUser, TenantID: tenantID, CredentialRef: credRef,
	}
}

func TestRevokeAndCheck(t *testing.T) {
	d := denylist.New()
	key := poolKey("claude-prod", "key-1")
	if d.Revoked(key) {
		t.Fatal("a fresh deny list reported a credential revoked")
	}
	d.Revoke(key)
	if !d.Revoked(key) {
		t.Error("a revoked credential is not reported revoked")
	}
}

func TestUnrelatedCredentialIsNotRevoked(t *testing.T) {
	d := denylist.New()
	d.Revoke(poolKey("claude-prod", "key-1"))
	if d.Revoked(poolKey("claude-prod", "key-2")) {
		t.Error("revoking key-1 also revoked key-2")
	}
	if d.Revoked(userKey("acme", "cred-1")) {
		t.Error("revoking a pool credential also revoked a user credential")
	}
}

func TestPoolAndUserKeyspacesDoNotCollide(t *testing.T) {
	// A pool and a user credential that share string values must not
	// alias on the deny list; Source discriminates the keyspaces.
	d := denylist.New()
	d.Revoke(poolKey("shared", "shared"))
	if !d.Revoked(poolKey("shared", "shared")) {
		t.Error("the revoked pool credential is not reported revoked")
	}
	if d.Revoked(userKey("shared", "shared")) {
		t.Error("revoking a pool credential revoked the same-named user credential")
	}
}

func TestRevokeIsIdempotent(t *testing.T) {
	d := denylist.New()
	key := poolKey("claude-prod", "key-1")
	d.Revoke(key)
	d.Revoke(key)
	if d.Len() != 1 {
		t.Errorf("deny list holds %d entries after a double Revoke, want 1", d.Len())
	}
}

// spec: §4.9 line 1671 — a deny-list entry expires when the credential's
// last lease lapses; the sweep calls Remove to drop it.
func TestRemoveDeletesKey(t *testing.T) {
	d := denylist.New()
	key := poolKey("claude-prod", "key-1")
	d.Revoke(key)
	if !d.Revoked(key) {
		t.Fatal("the revoked credential is not reported revoked before Remove")
	}
	d.Remove(key)
	if d.Revoked(key) {
		t.Error("the credential is still reported revoked after Remove")
	}
	if d.Len() != 0 {
		t.Errorf("deny list holds %d entries after removing the only key, want 0", d.Len())
	}
}

func TestRemoveOnlyDeletesNamedKey(t *testing.T) {
	d := denylist.New()
	keep := poolKey("claude-prod", "key-1")
	drop := userKey("acme", "cred-1")
	d.Revoke(keep)
	d.Revoke(drop)
	d.Remove(drop)
	if d.Revoked(drop) {
		t.Error("the removed key is still reported revoked")
	}
	if !d.Revoked(keep) {
		t.Error("removing one key also dropped an unrelated key")
	}
}

func TestRemoveAbsentKeyIsNoop(t *testing.T) {
	d := denylist.New()
	d.Revoke(poolKey("claude-prod", "key-1"))
	d.Remove(userKey("acme", "never-added"))
	if d.Len() != 1 {
		t.Errorf("removing an absent key changed the deny list size to %d, want 1", d.Len())
	}
}

func TestKeysSnapshot(t *testing.T) {
	d := denylist.New()
	if got := d.Keys(); len(got) != 0 {
		t.Errorf("a fresh deny list returned %d keys, want 0", len(got))
	}
	want := map[credential.CredentialKey]struct{}{
		poolKey("p", "c1"):     {},
		poolKey("p", "c2"):     {},
		userKey("acme", "cr1"): {},
	}
	for k := range want {
		d.Revoke(k)
	}
	got := d.Keys()
	if len(got) != len(want) {
		t.Fatalf("Keys returned %d entries, want %d", len(got), len(want))
	}
	seen := make(map[credential.CredentialKey]struct{}, len(got))
	for _, k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("Keys returned unexpected entry %+v", k)
		}
		if _, dup := seen[k]; dup {
			t.Errorf("Keys returned duplicate entry %+v", k)
		}
		seen[k] = struct{}{}
	}
}

// TestKeysReflectsRemoval pins that Keys returns the current-entry
// snapshot rather than a stale set: a key dropped via Remove is absent.
func TestKeysReflectsRemoval(t *testing.T) {
	d := denylist.New()
	stay := poolKey("p", "c1")
	gone := poolKey("p", "c2")
	d.Revoke(stay)
	d.Revoke(gone)
	d.Remove(gone)
	got := d.Keys()
	if len(got) != 1 || got[0] != stay {
		t.Errorf("Keys after Remove = %+v, want exactly [%+v]", got, stay)
	}
}

func TestLen(t *testing.T) {
	d := denylist.New()
	if d.Len() != 0 {
		t.Errorf("a fresh deny list has %d entries, want 0", d.Len())
	}
	d.Revoke(poolKey("p", "c1"))
	d.Revoke(poolKey("p", "c2"))
	d.Revoke(userKey("acme", "cr"))
	if d.Len() != 3 {
		t.Errorf("deny list holds %d entries, want 3", d.Len())
	}
}
