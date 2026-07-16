// SPDX-License-Identifier: MIT

// Package storagequota is the §11.2 per-tenant storage byte counter.
// It backs the gateway's pre-upload quota reservation: before any
// workspace upload or checkpoint write, the gateway reserves the
// declared incoming byte count against the tenant's storageQuotaBytes
// limit, rejecting the request with STORAGE_QUOTA_EXCEEDED when the
// projected usage would exceed it.
//
// The in-memory Counter here backs tests and the minimal gateway;
// storagequota/redisstore is the cross-replica Redis implementation
// whose reservation is atomic via a Lua script.
package storagequota

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrQuotaExceeded — the reservation would push the tenant's
// reserved-plus-committed bytes past its storage quota.
var ErrQuotaExceeded = errors.New("storagequota: tenant storage quota exceeded")

// Counter is the §11.2 per-tenant storage byte counter.
type Counter interface {
	// Reserve atomically reserves incoming bytes against the tenant's
	// quota. It returns the tenant's usage as it stood before the
	// reservation so the caller can cap the inbound stream at the
	// headroom (limit minus priorUsed). ErrQuotaExceeded is returned,
	// and nothing reserved, when priorUsed plus incoming exceeds limit.
	Reserve(ctx context.Context, tenantID string, incoming, limit int64) (priorUsed int64, err error)

	// Adjust shifts the tenant's counter by delta, never below zero. A
	// negative delta releases a reservation (a failed upload) or
	// reconciles an upload that wrote fewer bytes than were declared.
	Adjust(ctx context.Context, tenantID string, delta int64) error

	// Used returns the tenant's current reserved-plus-committed bytes.
	Used(ctx context.Context, tenantID string) (int64, error)

	// Set overwrites the tenant's counter with value, clamped at zero.
	// It is the §11 line 37 rehydration primitive: on a Redis restart the
	// counter is reconstructed from the authoritative sum of
	// `artifact_size_bytes` across the tenant's active (non-deleted)
	// artifacts in Postgres, which is an absolute write rather than a
	// relative Adjust.
	//
	// spec: §11 line 37 — rehydrate per-tenant storage counters on Redis
	// restart.
	Set(ctx context.Context, tenantID string, value int64) error
}

// LiveBytesSource returns a tenant's durable storage byte total from
// Postgres. In production it is the reservation-aware sum
// ReservationAwareLiveBytes composes: the authoritative sum of active
// (non-deleted) §12.5 artifact_store bytes plus the outstanding checkpoint
// reservations that have no artifact_store row yet. Rehydrate and the
// §12.4 during-outage Failover read both go through this one seam so the
// enforcement path and the rebuild path agree.
//
// spec: §11.2 rehydrate formula; §12.4 during-outage enforcement read.
type LiveBytesSource func(ctx context.Context, tenantID string) (int64, error)

// ReservationAwareLiveBytes folds a tenant's outstanding checkpoint
// reservations into its durable artifact_store byte sum, yielding the one
// reservation-aware LiveBytesSource seam the §12.4 during-outage Failover
// read, the RecoveryReconciler recovery-edge rebuild, and the startup
// Rehydrate all share. An outstanding reservation has no artifact_store row,
// so a rebuild that summed liveBytes alone would drop it, and the guarded
// relative Adjust that later releases the reservation would then remove bytes
// belonging to the tenant's other live artifacts and fail the quota gate
// open. Both component reads target Postgres; either error surfaces so the
// caller can fail the enforcement read closed rather than under-count.
// artifactcatalog.Store.SumLiveBytes and the manifest store's
// SumOutstandingReservations satisfy the two arguments. A nil component
// degrades to the other alone.
//
// spec: §11.2 reservation-aware rebuild; §12.4 line 222.
func ReservationAwareLiveBytes(liveBytes, outstandingReservations LiveBytesSource) LiveBytesSource {
	switch {
	case liveBytes == nil:
		return outstandingReservations
	case outstandingReservations == nil:
		return liveBytes
	}
	return func(ctx context.Context, tenantID string) (int64, error) {
		live, err := liveBytes(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("storagequota: live bytes %s: %w", tenantID, err)
		}
		reserved, err := outstandingReservations(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("storagequota: outstanding reservations %s: %w", tenantID, err)
		}
		return live + reserved, nil
	}
}

// Rehydrate reconstructs per-tenant storage counters from the durable
// Postgres byte sums after a Redis restart. For each tenant it reads
// sizeOf and Sets the counter to it. When sizeOf is the reservation-aware
// seam ReservationAwareLiveBytes composes, the absolute value written is
// SUM(artifact_size_bytes over active artifact_store rows) plus the
// outstanding checkpoint reservations, so the counter holds every
// reservation that has not been released and the guarded relative Adjust
// that later releases one is correct after the rebuild. A per-tenant read
// or write failure is collected and the sweep continues so one tenant's
// fault does not abort the rest; the joined error is returned for the
// caller to log.
//
// spec: §11.2 reservation-aware rebuild — same recovery path as token
// quota counters.
func Rehydrate(ctx context.Context, c Counter, tenants []string, sizeOf LiveBytesSource) error {
	if c == nil || sizeOf == nil {
		return nil
	}
	var errs []error
	for _, tenantID := range tenants {
		if tenantID == "" {
			continue
		}
		sum, err := sizeOf(ctx, tenantID)
		if err != nil {
			errs = append(errs, fmt.Errorf("storagequota: rehydrate sum %s: %w", tenantID, err))
			continue
		}
		if err := c.Set(ctx, tenantID, sum); err != nil {
			errs = append(errs, fmt.Errorf("storagequota: rehydrate set %s: %w", tenantID, err))
		}
	}
	return errors.Join(errs...)
}

// Memory is the in-memory Counter. The byte total per tenant is held
// in a map guarded by a mutex.
type Memory struct {
	mu    sync.Mutex
	bytes map[string]int64
}

// NewMemory returns an empty in-memory storage counter.
func NewMemory() *Memory {
	return &Memory{bytes: map[string]int64{}}
}

// Reserve implements Counter.
func (m *Memory) Reserve(_ context.Context, tenantID string, incoming, limit int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prior := m.bytes[tenantID]
	if prior+incoming > limit {
		return prior, ErrQuotaExceeded
	}
	m.bytes[tenantID] = prior + incoming
	return prior, nil
}

// Adjust implements Counter.
func (m *Memory) Adjust(_ context.Context, tenantID string, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.bytes[tenantID] + delta
	if next < 0 {
		next = 0
	}
	m.bytes[tenantID] = next
	return nil
}

// Used implements Counter.
func (m *Memory) Used(_ context.Context, tenantID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes[tenantID], nil
}

// Set implements Counter. A negative value clamps to zero so the
// counter never holds a negative byte total.
func (m *Memory) Set(_ context.Context, tenantID string, value int64) error {
	if value < 0 {
		value = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytes[tenantID] = value
	return nil
}

var _ Counter = (*Memory)(nil)
