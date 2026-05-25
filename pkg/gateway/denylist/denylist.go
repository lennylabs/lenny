// SPDX-License-Identifier: MIT

// Package denylist is the §4.9 credential deny list: the set of revoked
// credentials the LLM reverse proxy checks on every upstream request.
// A hit rejects the request with CREDENTIAL_REVOKED before any upstream
// call, so a revoked or compromised credential has no window in which
// it still reaches the provider.
//
// The deny list is keyed by the source-aware credential identity, so a
// pool-backed credential and a user-backed credential never alias. It
// is in-memory and per-replica; §4.9 propagates revocations across
// replicas over Redis pub/sub and rebuilds the list at startup from the
// stores' revoked entries. This package is the per-replica set those
// mechanisms populate.
package denylist

import (
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
)

// DenyList is the per-replica set of revoked credential identities.
// Every method is goroutine-safe; the §4.9 LLM proxy reads it
// concurrently on the upstream hot path.
type DenyList struct {
	mu      sync.RWMutex
	revoked map[credential.CredentialKey]struct{}
}

// New returns an empty DenyList.
func New() *DenyList {
	return &DenyList{revoked: make(map[credential.CredentialKey]struct{})}
}

// Revoke adds a credential identity to the deny list. A credential
// already on the list stays on it; revocation is not reversed.
func (d *DenyList) Revoke(key credential.CredentialKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revoked[key] = struct{}{}
}

// Reset replaces the deny list with exactly the credentials in keys.
// The §4.9 startup deny-list rebuild calls it once, at replica start,
// with the union of revoked entries from the stores so a freshly
// started replica holds precisely the currently-revoked set even if it
// missed the pub/sub revocations issued while it was down.
//
// Reset is authoritative: it constructs the full set rather than only
// adding to it. It is therefore safe only against an empty (or
// fully-store-derived) list — a periodic Reset would drop entries that
// the live §11.4 revocation path adds for credentials not yet reflected
// in the store query. The rebuild runs at startup, where the list is
// empty, so Reset seeds without clobbering a live entry.
//
// spec: §4.9 lines 1668-1673 — a newly started gateway replica rebuilds
// its deny list by executing a union across the credential stores.
func (d *DenyList) Reset(keys []credential.CredentialKey) {
	next := make(map[credential.CredentialKey]struct{}, len(keys))
	for _, k := range keys {
		next[k] = struct{}{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revoked = next
}

// Revoked reports whether the credential identified by key is on the
// deny list. It satisfies the §4.9 LLM proxy's DenyList interface.
func (d *DenyList) Revoked(key credential.CredentialKey) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.revoked[key]
	return ok
}

// Len reports how many credential identities are on the deny list.
func (d *DenyList) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.revoked)
}
