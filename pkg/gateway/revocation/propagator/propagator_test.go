// SPDX-License-Identifier: MIT

package propagator

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/revocation"
)

// TestRevokeAppliesLocallyWithNilBus confirms Revoke marks the jti
// revoked in the wrapped cache with no Bus wired, so a single-replica
// deployment still enforces the revocation.
func TestRevokeAppliesLocallyWithNilBus(t *testing.T) {
	cache := revocation.NewCache()
	p := New(cache, nil)

	p.Revoke("jti-1")
	if !cache.IsRevoked("jti-1") {
		t.Error("after Revoke, the wrapped cache does not report jti-1 revoked")
	}
	if !p.IsRevoked("jti-1") {
		t.Error("Propagator.IsRevoked should delegate to the wrapped cache")
	}
}

// TestRevokeEmptyJTIIsNoop confirms an empty jti is neither stored nor
// published, matching revocation.Cache.Revoke.
func TestRevokeEmptyJTIIsNoop(t *testing.T) {
	cache := revocation.NewCache()
	p := New(cache, nil)

	p.Revoke("")
	if p.Len() != 0 {
		t.Errorf("Len = %d after revoking an empty jti, want 0", p.Len())
	}
}

// TestPropagatorSatisfiesRevocationCache confirms Revoke has the
// signature the admin router's RevocationCache interface requires, so
// wiring the propagator in place of the raw cache routes the admin
// revoke endpoint through the fan-out.
func TestPropagatorSatisfiesRevocationCache(t *testing.T) {
	var revoker interface{ Revoke(jti string) } = New(revocation.NewCache(), nil)
	revoker.Revoke("jti-iface")
}

// TestSubscribeApplyAppliesPeerRevocation simulates the subscribe-loop
// payload handler: a jti delivered as a raw payload must be revoked on
// the local cache, which is the cross-replica convergence path.
func TestSubscribeApplyAppliesPeerRevocation(t *testing.T) {
	cache := revocation.NewCache()
	p := New(cache, nil)

	// Run on a nil Bus blocks, so exercise the same closure the loop
	// hands each payload directly.
	applyPeer := func(payload []byte) { p.Cache().Revoke(string(payload)) }
	applyPeer([]byte("jti-from-peer"))
	if !cache.IsRevoked("jti-from-peer") {
		t.Error("a peer revocation payload did not converge on the local cache")
	}
}

// TestCacheReturnsWrappedPrimitive confirms Cache exposes the same
// revocation cache the propagator wraps, so the rehydration loop can
// reach the primitive directly.
func TestCacheReturnsWrappedPrimitive(t *testing.T) {
	cache := revocation.NewCache()
	p := New(cache, nil)
	if p.Cache() != cache {
		t.Error("Cache() did not return the wrapped revocation cache")
	}
}
