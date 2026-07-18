// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// This file wires the §4.9 Emergency Credential Revocation lease
// terminator the admin router consumes. The revoke handlers mark the
// credential revoked in the CredentialPoolStore on their own; the
// adapter here carries the deny-list propagation onto infrastructure the
// admin layer does not own — the per-replica credential deny list and
// the credential-lease store. It mirrors the §11.4 full_revoke
// userLeaseRevoker and is independently optional: a minimal gateway that
// wires neither still marks the store and emits the §4.9.2 audit event.

// poolLeaseStore is the slice of the §4.9 credential-lease store the
// emergency-revocation fan-out needs: enumerate the leases backed by a
// credential and drop a lease by id. The in-memory credleasestore.Store
// and the Postgres-backed credleasestore/pgstore.Store both satisfy it.
type poolLeaseStore interface {
	LeasesByCredential(key credential.CredentialKey) []credential.Lease
	Remove(leaseID string)
}

// poolCredentialRevoker denies the §4.9 credential leases backed by a
// revoked pool credential. For each credential it adds the source-aware
// identity to the deny list and retains the leases this replica holds so
// the proxy rejects each in place, returning the count of leases affected.
// It satisfies admin.PoolCredentialRevoker.
//
// The deny-list write is what stops an already-materialized proxy-mode
// lease token from reaching the provider — §4.9 step 4 — and, when the
// deny list is wrapped by a propagator, the revocation fans out to every
// replica so a peer that still holds the lease rejects it on the next
// upstream request. The lease is retained rather than removed because under
// the shared-Postgres lease store a delete would remove the row every
// replica reads, making the deny-list check unreachable and degrading the
// rejection to LEASE_TOKEN_INVALID. spec: §4.9 lines 1640-1652, 1671.
type poolCredentialRevoker struct {
	leases   poolLeaseStore
	denyList credentialDenyList
}

// RevokePoolCredentials revokes each credentialID in poolID: it adds the
// pool-backed credential identity to the deny list (propagated across
// replicas) and retains every lease this replica holds against it, denied
// in place by the deny-list entry, returning the count of leases affected.
// A credential with no live lease on this replica still lands on the deny
// list so a future request that resolves a cached lease for it is rejected.
func (p *poolCredentialRevoker) RevokePoolCredentials(_ context.Context, poolID string, credentialIDs []string) int {
	total := 0
	for _, credID := range credentialIDs {
		key := credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       poolID,
			CredentialID: credID,
		}
		p.denyList.Revoke(key)
		// spec: §4.9 — the lease is retained and denied in place; the
		// returned count is leases-affected, not leases-removed.
		for range p.leases.LeasesByCredential(key) {
			total++
		}
	}
	return total
}

var _ admin.PoolCredentialRevoker = (*poolCredentialRevoker)(nil)

// directModeRevocationRotator implements §4.9 emergency revocation step 5
// (spec/04_system-components.md line 1649): for a revoked pool credential
// it sends a RotateCredentials RPC to every direct-delivery pod holding a
// lease against the credential, so the pod is proactively pushed off the
// materialized key rather than waiting for the lease's natural TTL.
//
// It is the per-replica revoke hook the credential-lease revocation
// propagator fires (propagator.WithRevokeHook). The hook runs on every
// replica that applies the revocation — the originating replica and every
// peer that receives the pub/sub fan-out — so the rotate reaches a
// direct-mode pod regardless of which replica holds its binding, with no
// second cross-replica substrate.
//
// Proxy-mode leases are not rotated here: the deny list rejects them on
// the next upstream request (§4.9 step 4), so the proactive push is a
// direct-delivery-only concern.
type directModeRevocationRotator struct {
	// leases enumerates this replica's leases against a credential.
	leases poolLeaseStore
	// markRevoked marks the credential unselectable in the credential-
	// assignment service so the replacement mint draws a *different*
	// credential from the pool (§4.9 line 1649: "a different credential in
	// the pool"). It runs before the rotate is dispatched.
	markRevoked func(poolID, credentialID string)
	// rotate mints a replacement lease from nextPool and pushes it to the
	// lease's pod via the §4.7 RotateCredentials RPC under the given
	// trigger. proxyFallbackRotator.Rotate satisfies it.
	rotate func(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger)
	// dispatch runs one per-lease rotate. The default runs each in its own
	// goroutine so a slow RotateCredentials RPC does not block the
	// revocation propagator's subscribe loop; tests inject a synchronous
	// dispatch.
	dispatch func(func())
}

// onRevoke rotates every direct-delivery pod on this replica off a
// revoked pool credential. It first marks the credential unselectable so
// the replacement mint skips it, then re-mints from the lease's own pool
// (the §4.9 line 1649 "different credential in the pool" path) under the
// emergency_revocation trigger and pushes it to the pod. A user-backed
// revocation (§11.4 full_revoke) carries no pool credential and is
// ignored. When the pool has no other assignable credential the rotate
// mints nothing and the pod retains its now-revoked key until provider-
// side rotation, the §4.9 direct-mode residual risk the spec documents.
func (d *directModeRevocationRotator) onRevoke(key credential.CredentialKey) {
	if d == nil || key.Source != credential.SourcePool || key.PoolID == "" || key.CredentialID == "" {
		return
	}
	if d.markRevoked != nil {
		d.markRevoked(key.PoolID, key.CredentialID)
	}
	dispatch := d.dispatch
	if dispatch == nil {
		dispatch = func(fn func()) { go fn() }
	}
	for _, lease := range d.leases.LeasesByCredential(key) {
		if lease.DeliveryMode != credential.DeliveryDirect {
			continue
		}
		lease := lease
		dispatch(func() {
			d.rotate(lease, lease.PoolID, credential.TriggerEmergencyRevocation)
		})
	}
}

// poolCredentialHealthReader serves the §24.5 row-2 per-credential lease
// counts the admin GET handler surfaces. It reads the credential-lease
// store this replica holds; the count is per-replica (matching the
// `leasesTerminated` semantics of the revoker), so an operator reading a
// pool's health sees the leases live on the replica that served the
// request. It satisfies admin.PoolCredentialHealthReader.
type poolCredentialHealthReader struct {
	leases poolLeaseStore
}

// PoolCredentialLeaseCounts returns the active lease count keyed by
// credential id for the named pool, counting only the credential ids the
// caller supplies (the pool's current credential set). A credential with
// no live lease on this replica is omitted from the map.
func (h *poolCredentialHealthReader) PoolCredentialLeaseCounts(poolName string, credentialIDs []string) map[string]int {
	out := make(map[string]int, len(credentialIDs))
	for _, credID := range credentialIDs {
		key := credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       poolName,
			CredentialID: credID,
		}
		if n := len(h.leases.LeasesByCredential(key)); n > 0 {
			out[credID] = n
		}
	}
	return out
}

var _ admin.PoolCredentialHealthReader = (*poolCredentialHealthReader)(nil)
