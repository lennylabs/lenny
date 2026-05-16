// SPDX-License-Identifier: MIT

// Package credcache is the §4.9 Token Service in-memory credential
// cache. It holds the real upstream LLM provider credentials inside the
// gateway process, keyed by the source-aware credential identity. The
// §4.9 LLM reverse proxy reads the current credential from this cache
// on every upstream call and injects it into the translated request.
//
// Real upstream keys live only here: never on disk, never on tmpfs,
// never shared with another process. Rotation overwrites a cache entry
// in place, so the next proxy request reads the new credential with no
// reload signal, file replacement, or restart.
package credcache

import (
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
)

// Cache is the in-memory upstream-credential cache. Every method is
// goroutine-safe; the §4.9 LLM proxy reads it concurrently on the
// upstream hot path.
type Cache struct {
	mu    sync.RWMutex
	creds map[credential.CredentialKey]string
}

// New returns an empty Cache.
func New() *Cache {
	return &Cache{creds: make(map[credential.CredentialKey]string)}
}

// Put stores the real upstream credential for a credential identity,
// replacing any prior value. A credential rotation is a Put with the
// same key and the new secret; it takes effect on the next read.
func (c *Cache) Put(key credential.CredentialKey, apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creds[key] = apiKey
}

// UpstreamCredential returns the real upstream credential for a lease's
// backing credential. ok is false when the cache holds no credential
// for the lease. It satisfies the §4.9 LLM proxy's CredentialResolver.
func (c *Cache) UpstreamCredential(lease credential.Lease) (apiKey string, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	apiKey, ok = c.creds[lease.CredentialKey()]
	return apiKey, ok
}

// Remove drops the cached credential for a credential identity. It is a
// no-op when the cache holds no such credential.
func (c *Cache) Remove(key credential.CredentialKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.creds, key)
}

// Len reports how many credentials the cache holds.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.creds)
}
