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
