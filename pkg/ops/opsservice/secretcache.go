// SPDX-License-Identifier: MIT

package opsservice

import "sync"

// SecretCache is the §25.5 in-memory webhook-secret reveal cache. The
// subscription store persists only the SHA-256 hash of each signing
// secret, so the delivery worker cannot recover the plaintext from the
// store. The eventsubscription.Service emits the plaintext exactly once
// when it is generated (create/rotate); SecretCache captures it in
// memory so the worker can sign deliveries with X-Lenny-Signature. The
// plaintext is never persisted or logged, and is dropped when the
// subscription is deleted or when a refresh observes it is gone.
//
// In a multi-replica deployment each replica caches only the secrets
// generated on that replica; a secret generated on a peer is unknown
// until that subscription is rotated locally, so a delivery for it goes
// out unsigned. The leader-only delivery worker therefore signs every
// subscription it created or rotated, which is the common case.
//
// spec: §25.5 lines 2715-2733, 2747-2756.
type SecretCache struct {
	mu      sync.RWMutex
	secrets map[string][]byte
}

// NewSecretCache returns an empty reveal cache.
func NewSecretCache() *SecretCache {
	return &SecretCache{secrets: map[string][]byte{}}
}

// Put records the plaintext signing secret for subID. It matches the
// eventsubscription.Service.OnSecret hook signature so it can be wired
// directly; the generation argument is unused (the cache always holds
// the most recent secret).
func (c *SecretCache) Put(subID, secret string, _ int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secrets[subID] = []byte(secret)
}

// Remove drops the cached secret for subID. It matches the
// eventsubscription.Service.OnRemove hook signature.
func (c *SecretCache) Remove(subID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.secrets, subID)
}

// Secret implements SecretResolver: it returns the cached plaintext
// signing secret for subID. A returned slice is a copy so the caller
// cannot mutate the cache.
func (c *SecretCache) Secret(subID string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.secrets[subID]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(s))
	copy(out, s)
	return out, true
}

// Retain implements SecretResolver: it drops every cached secret whose
// subscription id is not in activeIDs, so a deleted subscription's
// secret does not linger after a refresh.
func (c *SecretCache) Retain(activeIDs []string) {
	keep := make(map[string]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		keep[id] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.secrets {
		if _, ok := keep[id]; !ok {
			delete(c.secrets, id)
		}
	}
}

var _ SecretResolver = (*SecretCache)(nil)
