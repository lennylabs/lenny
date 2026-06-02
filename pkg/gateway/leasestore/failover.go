// SPDX-License-Identifier: MIT

package leasestore

import (
	"context"
	"errors"
	"time"
)

// Failover routes §12.2 LeaseStore operations to a Redis-backed primary
// and, when the primary signals infrastructure unavailability, to a
// Postgres advisory-lock fallback. It is the §12.4 "Distributed session
// leases | Fall back to Postgres advisory locks (higher latency)" path:
// a Redis outage no longer breaks lease acquisition; it only raises
// latency by routing through Postgres.
//
// An error from the primary is classified as either a lease-domain
// outcome (ErrHeld, ErrNotHeld, ErrNotFound, ErrEmptyScope, or nil) —
// which a healthy Redis returns and which is forwarded to the caller
// unchanged — or an infrastructure failure (a connection error, a
// closed client, a timeout), which triggers the fallback. The
// classification is by exclusion: any non-domain error is treated as
// Redis being unavailable, so the fallback engages without needing a
// Redis-client-specific error taxonomy.
//
// Get returns the primary's ErrNotFound without consulting the
// fallback: that is a healthy "no lease" answer, not an outage. A lease
// written to Postgres during an outage is therefore not visible through
// Get once Redis recovers, but the coordination sweeper re-Acquires
// every non-terminal session each sweep (Acquire is idempotent), so the
// authoritative holder re-establishes in Redis on the next pass.
type Failover struct {
	primary    LeaseStore
	fallback   LeaseStore
	onFallback func()
}

var _ LeaseStore = (*Failover)(nil)

// NewFailover wraps primary (Redis) with fallback (Postgres advisory
// locks). onFallback, when non-nil, is invoked each time an operation
// routes to the fallback because the primary was unavailable — wire a
// metric counter there.
func NewFailover(primary, fallback LeaseStore, onFallback func()) *Failover {
	return &Failover{primary: primary, fallback: fallback, onFallback: onFallback}
}

// isDomainOutcome reports whether err is a lease-domain result the
// primary returned from a healthy Redis (so it is forwarded as-is)
// rather than an infrastructure failure that should trigger the
// fallback. A nil error is a domain outcome.
func isDomainOutcome(err error) bool {
	return err == nil ||
		errors.Is(err, ErrHeld) ||
		errors.Is(err, ErrNotHeld) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrEmptyScope)
}

func (f *Failover) routedToFallback() {
	if f.onFallback != nil {
		f.onFallback()
	}
}

// Acquire claims the lease via the primary, falling back to Postgres
// when the primary is unavailable.
func (f *Failover) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error) {
	lease, err := f.primary.Acquire(ctx, tenantID, sessionID, holder, ttl)
	if isDomainOutcome(err) {
		return lease, err
	}
	f.routedToFallback()
	return f.fallback.Acquire(ctx, tenantID, sessionID, holder, ttl)
}

// Renew extends the lease via the primary, falling back to Postgres
// when the primary is unavailable.
func (f *Failover) Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error) {
	lease, err := f.primary.Renew(ctx, tenantID, sessionID, holder, ttl)
	if isDomainOutcome(err) {
		return lease, err
	}
	f.routedToFallback()
	return f.fallback.Renew(ctx, tenantID, sessionID, holder, ttl)
}

// Release drops the lease via the primary, falling back to Postgres
// when the primary is unavailable.
func (f *Failover) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	err := f.primary.Release(ctx, tenantID, sessionID, holder)
	if isDomainOutcome(err) {
		return err
	}
	f.routedToFallback()
	return f.fallback.Release(ctx, tenantID, sessionID, holder)
}

// Get reads the lease via the primary, falling back to Postgres when the
// primary is unavailable. A primary ErrNotFound is a healthy answer and
// is forwarded without consulting the fallback.
func (f *Failover) Get(ctx context.Context, tenantID, sessionID string) (Lease, error) {
	lease, err := f.primary.Get(ctx, tenantID, sessionID)
	if isDomainOutcome(err) {
		return lease, err
	}
	f.routedToFallback()
	return f.fallback.Get(ctx, tenantID, sessionID)
}

// DeleteByUser runs the §12.1 erasure primitive on the primary, falling
// back to Postgres when the primary is unavailable.
func (f *Failover) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	n, err := f.primary.DeleteByUser(ctx, tenantID, userID)
	if isDomainOutcome(err) {
		return n, err
	}
	f.routedToFallback()
	return f.fallback.DeleteByUser(ctx, tenantID, userID)
}

// DeleteByTenant runs the §12.1 erasure primitive on the primary,
// falling back to Postgres when the primary is unavailable.
func (f *Failover) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	n, err := f.primary.DeleteByTenant(ctx, tenantID)
	if isDomainOutcome(err) {
		return n, err
	}
	f.routedToFallback()
	return f.fallback.DeleteByTenant(ctx, tenantID)
}
