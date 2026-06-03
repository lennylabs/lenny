// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
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

// poolCredentialRevoker terminates the §4.9 credential leases backed by
// a revoked pool credential. For each credential it adds the source-aware
// identity to the deny list and drops the leases this replica holds,
// returning the count terminated. It satisfies admin.PoolCredentialRevoker.
//
// The deny-list write is what stops an already-materialized proxy-mode
// lease token from reaching the provider — §4.9 step 4 — and, when the
// deny list is wrapped by a propagator, the revocation fans out to every
// replica so a peer that still holds the lease rejects it on the next
// upstream request. spec: §4.9 lines 1640-1652.
type poolCredentialRevoker struct {
	leases   poolLeaseStore
	denyList credentialDenyList
}

// RevokePoolCredentials revokes each credentialID in poolID: it adds the
// pool-backed credential identity to the deny list (propagated across
// replicas) and removes every lease this replica holds against it,
// returning the total leases terminated. A credential with no live lease
// on this replica still lands on the deny list so a future request that
// resolves a cached lease for it is rejected.
func (p *poolCredentialRevoker) RevokePoolCredentials(_ context.Context, poolID string, credentialIDs []string) int {
	total := 0
	for _, credID := range credentialIDs {
		key := credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       poolID,
			CredentialID: credID,
		}
		p.denyList.Revoke(key)
		for _, lease := range p.leases.LeasesByCredential(key) {
			p.leases.Remove(lease.LeaseID)
			total++
		}
	}
	return total
}

var _ admin.PoolCredentialRevoker = (*poolCredentialRevoker)(nil)

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
