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
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
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

// defaultMaxStaleness bounds how long a replica trusts its in-memory
// revocation set after the last successful rehydration. The rehydration
// loop runs every 30s; three missed cycles (90s) means the replica
// cannot reach Postgres, at which point §13.3 line 601 requires it to
// refuse validation rather than honor a possibly-revoked token from a
// stale cache.
const defaultMaxStaleness = 90 * time.Second

// Cache holds the set of revoked token JTIs. The zero value is not
// usable; construct with NewCache.
type Cache struct {
	mu      sync.RWMutex
	revoked map[string]struct{}

	// lastSuccess is the instant the most recent Rehydrate completed
	// against Postgres. The §13.3 fail-closed validation gate keys on its
	// age. A zero value means no rehydration has ever succeeded.
	lastSuccess  time.Time
	maxStaleness time.Duration
	clock        func() time.Time
}

// Option configures a Cache at construction.
type Option func(*Cache)

// WithClock overrides the wall clock the staleness gate reads. Tests
// inject a controllable clock; production leaves it at time.Now.
func WithClock(fn func() time.Time) Option {
	return func(c *Cache) { c.clock = fn }
}

// WithMaxStaleness overrides the freshness window the §13.3 validation
// gate enforces. A non-positive value restores the default.
func WithMaxStaleness(d time.Duration) Option {
	return func(c *Cache) {
		if d > 0 {
			c.maxStaleness = d
		}
	}
}

// NewCache returns an empty revocation cache.
func NewCache(opts ...Option) *Cache {
	c := &Cache{
		revoked:      map[string]struct{}{},
		maxStaleness: defaultMaxStaleness,
		clock:        time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
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
	// Record the successful read so the §13.3 line 601 validation gate
	// knows the in-memory set reflects Postgres as of this instant.
	c.lastSuccess = c.clock()
	return nil
}

// Stale reports whether the in-memory revocation set is too old to
// trust because the replica cannot reach Postgres. §13.3 line 601: a
// gateway replica that cannot reach Postgres refuses to validate tokens
// and returns 503 token_validation_unavailable rather than honoring a
// possibly-revoked token from a stale cache. The set is stale when no
// rehydration has ever succeeded, or when the most recent success is
// older than the freshness window. spec: §13.3 line 601. F-13.3.4.
func (c *Cache) Stale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lastSuccess.IsZero() {
		return true
	}
	return c.clock().Sub(c.lastSuccess) > c.maxStaleness
}
