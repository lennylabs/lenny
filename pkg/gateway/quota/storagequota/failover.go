// SPDX-License-Identifier: MIT

package storagequota

import (
	"context"
	"errors"
)

// ErrUnavailable is the §12.4 line 210 dual-store outage outcome: the
// Redis counter is unreachable AND the Postgres artifact_store sum that
// backs the fallback is also unreachable. The spec mandates that storage
// uploads fail closed (HTTP 503) in this state so the storage quota can
// never be bypassed by infrastructure degradation; the upload handler
// maps this sentinel to a 503 rather than admitting the write.
//
// spec: §12.4 line 210 — "If Postgres is also unavailable (dual-store
// outage), storage uploads are rejected with 503 (fail closed)".
var ErrUnavailable = errors.New("storagequota: counter unavailable (Redis and Postgres both unreachable)")

// Failover routes §11.2 storage-quota operations to a Redis-backed
// primary and, when the primary signals infrastructure unavailability,
// to a Postgres-derived fallback. It is the §12.4 line 210 "Storage
// quota counter (storage_bytes_used) | Postgres fallback, then fail
// closed" path: a Redis outage no longer breaks uploads outright;
// pre-checks instead read the authoritative sum of artifact_size_bytes
// across the tenant's active artifacts in Postgres, and only a
// simultaneous Postgres outage fails the upload closed with
// ErrUnavailable.
//
// An error from the primary is classified as either a domain outcome
// (nil or ErrQuotaExceeded — what a healthy Redis returns, forwarded
// unchanged) or an infrastructure failure (a connection error, a closed
// client, a timeout), which triggers the fallback. The classification is
// by exclusion: any non-domain error is treated as Redis being
// unavailable, so the fallback engages without a Redis-client-specific
// error taxonomy. This mirrors leasestore.Failover (F-12.4.10).
//
// During the outage window the fallback reservation reads the Postgres
// sum for the pre-check but performs no Redis increment — there is no
// reachable counter to advance. In production sizeOf is the
// reservation-aware seam ReservationAwareLiveBytes composes (live
// artifact_store bytes plus outstanding checkpoint reservations), so the
// during-outage pre-check counts a tenant's held checkpoint reservations
// against its quota rather than handing that reserved headroom back
// invisibly while Redis is down. The durable accounting is the
// artifact_store row the §12.5 cataloging decorator writes when the
// upload commits, which the next Postgres-derived read reflects. On
// Redis recovery, RecoveryReconciler writes the authoritative sum back
// to Redis and the atomic fast path resumes.
//
// spec: §12.4 line 222.
type Failover struct {
	primary    Counter
	sizeOf     LiveBytesSource
	onFallback func()
}

var _ Counter = (*Failover)(nil)

// NewFailover wraps primary (Redis) with a Postgres-derived fallback that
// reads each tenant's live artifact byte sum from sizeOf
// (artifactcatalog.Store.SumLiveBytes). onFallback, when non-nil, is
// invoked each time an operation routes to the fallback because the
// primary was unavailable — wire a degraded-mode signal there. A nil
// sizeOf disables the fallback (every infra error surfaces unchanged), so
// a deployment without a Postgres artifact catalog keeps the prior
// fail-on-Redis-error behavior.
func NewFailover(primary Counter, sizeOf LiveBytesSource, onFallback func()) *Failover {
	return &Failover{primary: primary, sizeOf: sizeOf, onFallback: onFallback}
}

// isDomainOutcome reports whether err is a storage-quota result a healthy
// Redis returned (forwarded as-is) rather than an infrastructure failure
// that should trigger the Postgres fallback. nil and ErrQuotaExceeded are
// domain outcomes.
func isDomainOutcome(err error) bool {
	return err == nil || errors.Is(err, ErrQuotaExceeded)
}

func (f *Failover) routedToFallback() {
	if f.onFallback != nil {
		f.onFallback()
	}
}

// Reserve runs the §11.2 pre-upload reservation on the primary, falling
// back to a Postgres-derived check when Redis is unavailable. On
// fallback it reads the tenant's durable byte total from Postgres (live
// artifact bytes plus outstanding checkpoint reservations, through the
// reservation-aware sizeOf seam) and admits the upload when sum+incoming
// fits the limit, returning the sum as the prior usage (so the caller's
// headroom math is exact). A simultaneous Postgres outage returns
// ErrUnavailable for the §12.4 fail-closed 503.
//
// spec: §12.4 line 222 — "Upload pre-checks ... use this reservation-aware
// Postgres-derived value ... during the outage window."
func (f *Failover) Reserve(ctx context.Context, tenantID string, incoming, limit int64) (int64, error) {
	prior, err := f.primary.Reserve(ctx, tenantID, incoming, limit)
	if isDomainOutcome(err) {
		return prior, err
	}
	f.routedToFallback()
	if f.sizeOf == nil {
		return 0, err
	}
	sum, serr := f.sizeOf(ctx, tenantID)
	if serr != nil {
		return 0, ErrUnavailable
	}
	if sum+incoming > limit {
		return sum, ErrQuotaExceeded
	}
	return sum, nil
}

// Adjust shifts the primary counter, swallowing an infrastructure error
// best-effort. During a Redis outage there is no reachable counter to
// shift; the Postgres-derived sum the fallback reads already excludes the
// uncommitted bytes a release would have removed, so a dropped Adjust
// does not over-count once Redis recovers and the reconciler writes the
// sum back. A nil error (success) is forwarded unchanged.
func (f *Failover) Adjust(ctx context.Context, tenantID string, delta int64) error {
	err := f.primary.Adjust(ctx, tenantID, delta)
	if err == nil {
		return nil
	}
	f.routedToFallback()
	return nil
}

// Used reads the primary counter, falling back to the Postgres-derived
// live byte sum when Redis is unavailable. A simultaneous Postgres outage
// returns ErrUnavailable.
func (f *Failover) Used(ctx context.Context, tenantID string) (int64, error) {
	v, err := f.primary.Used(ctx, tenantID)
	if err == nil {
		return v, nil
	}
	f.routedToFallback()
	if f.sizeOf == nil {
		return 0, err
	}
	sum, serr := f.sizeOf(ctx, tenantID)
	if serr != nil {
		return 0, ErrUnavailable
	}
	return sum, nil
}

// Set overwrites the primary counter. It is the §12.4 line 210 recovery
// write-back target (RecoveryReconciler calls it through Rehydrate); an
// infrastructure error is surfaced so the reconciler can log and retry on
// the next recovery edge rather than masking a still-down Redis.
func (f *Failover) Set(ctx context.Context, tenantID string, value int64) error {
	return f.primary.Set(ctx, tenantID, value)
}
