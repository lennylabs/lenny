// SPDX-License-Identifier: MIT

// Package revocation is the §12.4 in-memory token-revocation cache.
// The auth middleware consults it on every Bearer-token request, so a
// revoked token is rejected without a per-request database lookup.
// The cache is rehydrated from the issued_tokens index, so an
// operator's revocation survives a gateway restart and propagates to
// every replica.
package revocation

import (
	"context"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
)

// Source supplies the revoked tokens for a tenant. *issuedtokenstore.Store
// satisfies it.
type Source interface {
	ListRevoked(ctx context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error)
}

// TenantLister enumerates the tenants whose revocations are loaded.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// Cache holds the set of revoked token JTIs. The zero value is not
// usable; construct with NewCache.
type Cache struct {
	mu      sync.RWMutex
	revoked map[string]struct{}
}

// NewCache returns an empty revocation cache.
func NewCache() *Cache {
	return &Cache{revoked: map[string]struct{}{}}
}

// IsRevoked reports whether the token identified by jti has been
// revoked. An empty jti is never revoked.
func (c *Cache) IsRevoked(jti string) bool {
	if jti == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.revoked[jti]
	return ok
}

// Revoke marks jti revoked in the cache immediately, so the local
// replica enforces the revocation without waiting for the next
// rehydration. The durable record is written separately to the
// issued-token index.
func (c *Cache) Revoke(jti string) {
	if jti == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoked[jti] = struct{}{}
}

// Len returns the number of cached revocations.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.revoked)
}

// Rehydrate rebuilds the cache from the issued-token index across
// every tenant. It is called at startup and on a periodic ticker so
// revocations recorded by other replicas are picked up. A revocation
// added via Revoke between the index read and the swap is also
// re-applied, so a concurrent operator revocation is not dropped.
func (c *Cache) Rehydrate(ctx context.Context, tenants TenantLister, src Source) error {
	tenantIDs, err := tenants.ListTenants(ctx)
	if err != nil {
		return err
	}
	fresh := map[string]struct{}{}
	for _, tenantID := range tenantIDs {
		revoked, err := src.ListRevoked(ctx, tenantID)
		if err != nil {
			return err
		}
		for _, tok := range revoked {
			fresh[tok.JTI] = struct{}{}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Carry forward any revocation added locally during the rehydration
	// so a concurrent Revoke is not lost by the swap.
	for jti := range c.revoked {
		fresh[jti] = struct{}{}
	}
	c.revoked = fresh
	return nil
}
