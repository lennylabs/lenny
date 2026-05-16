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
