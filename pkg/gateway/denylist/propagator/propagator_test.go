// SPDX-License-Identifier: MIT

package propagator

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
)

// poolKey builds a pool-backed §4.9 credential identity.
func poolKey(poolID, credID string) credential.CredentialKey {
	return credential.CredentialKey{Source: credential.SourcePool, PoolID: poolID, CredentialID: credID}
}

// userKey builds a user-backed §4.9 credential identity.
func userKey(tenantID, ref string) credential.CredentialKey {
	return credential.CredentialKey{Source: credential.SourceUser, TenantID: tenantID, CredentialRef: ref}
}

// TestRevokeAppliesLocallyWithNilBus confirms Revoke adds the credential
// to the wrapped deny list with no Bus wired, so a single-replica
// deployment still rejects the revoked credential.
func TestRevokeAppliesLocallyWithNilBus(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	key := poolKey("pool-1", "cred-1")
	p.Revoke(key)
	if !local.Revoked(key) {
		t.Error("after Revoke, the wrapped deny list does not report the credential revoked")
	}
	if !p.Revoked(key) {
		t.Error("Propagator.Revoked should delegate to the wrapped deny list")
	}
}

// TestPoolAndUserKeyspacesDoNotAlias confirms a revoked pool-backed
// credential does not mark an unrelated user-backed credential revoked,
// once the key round-trips through the pub/sub encoding.
func TestPoolAndUserKeyspacesDoNotAlias(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	revoked := poolKey("pool-x", "cred-x")
	p.apply(mustEncode(t, revoked))

	other := userKey("acme", "anthropic-key")
	if local.Revoked(other) {
		t.Error("a pool-backed revocation aliased a user-backed credential")
	}
	if !local.Revoked(revoked) {
		t.Error("the pool-backed credential should be revoked after apply")
	}
}

// TestApplyRevokesPeerCredential confirms the subscribe-side apply
// decodes a peer replica's credential key and revokes it locally, the
// cross-replica convergence path.
func TestApplyRevokesPeerCredential(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	key := userKey("globex", "openai-key")
	p.apply(mustEncode(t, key))
	if !local.Revoked(key) {
		t.Errorf("apply did not revoke the peer credential %+v", key)
	}
}

// TestApplyIgnoresMalformedPayload confirms a payload that does not
// decode is dropped without panicking, so a malformed message cannot
// stall the subscribe loop.
func TestApplyIgnoresMalformedPayload(t *testing.T) {
	p := New(denylist.New(), nil)
	p.apply([]byte("{not json"))
	p.apply(nil)
	if p.Len() != 0 {
		t.Errorf("malformed payloads mutated the deny list; Len = %d, want 0", p.Len())
	}
}

// TestRevokeRoundTripsThroughApply confirms a credential revoked on one
// Propagator decodes and revokes on a second Propagator's deny list.
func TestRevokeRoundTripsThroughApply(t *testing.T) {
	publisher := New(denylist.New(), nil)
	key := poolKey("pool-rt", "cred-rt")
	encoded := mustEncode(t, key)
	publisher.Revoke(key)

	peer := New(denylist.New(), nil)
	peer.apply(encoded)
	if !peer.Revoked(key) {
		t.Errorf("the peer Propagator did not converge on the revoked credential %+v", key)
	}
}

// mustEncode marshals a credential key the way Revoke publishes it.
func mustEncode(t *testing.T, key credential.CredentialKey) []byte {
	t.Helper()
	b, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal credential key: %v", err)
	}
	return b
}
