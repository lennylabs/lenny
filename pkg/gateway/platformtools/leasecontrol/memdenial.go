// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"sync"
	"time"
)

// MemoryDenialStore is the in-memory DenialStore for a gateway run
// without Postgres and for tests. It holds each tree's denial, cool-off
// expiry, and cumulative grants keyed by (tenant, root session). It is
// replica-local, so it gives none of the cross-replica durability the
// Postgres store gives.
// spec: §8.6 (Extension state)
type MemoryDenialStore struct {
	// mu guards trees. Grant reads the denial, clamps, and adds under one
	// hold, the in-memory analogue of the Postgres row lock.
	mu    sync.Mutex
	clock func() time.Time
	trees map[memDenialKey]*memDenialRow
}

type memDenialKey struct{ tenantID, rootSessionID string }

type memDenialRow struct {
	denied bool
	expiry time.Time
	grants Dimensions
}

// NewMemoryDenialStore returns an empty MemoryDenialStore. A nil clock
// selects the UTC wall clock.
func NewMemoryDenialStore(clock func() time.Time) *MemoryDenialStore {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryDenialStore{clock: clock, trees: map[memDenialKey]*memDenialRow{}}
}

var _ DenialStore = (*MemoryDenialStore)(nil)

// rowLocked returns the tree's row, creating it. The caller holds m.mu.
func (m *MemoryDenialStore) rowLocked(tenantID, rootSessionID string) *memDenialRow {
	k := memDenialKey{tenantID, rootSessionID}
	r, ok := m.trees[k]
	if !ok {
		r = &memDenialRow{}
		m.trees[k] = r
	}
	return r
}

// deniedLocked reports whether r is denied with an unexpired cool-off.
// The caller holds m.mu.
func (m *MemoryDenialStore) deniedLocked(r *memDenialRow) bool {
	return r.denied && !r.expiry.IsZero() && m.clock().Before(r.expiry)
}

// Deny sets the tree's denial with a cool-off coolOff from now.
func (m *MemoryDenialStore) Deny(_ context.Context, tenantID, rootSessionID string, coolOff time.Duration) error {
	if coolOff < 0 {
		coolOff = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rowLocked(tenantID, rootSessionID)
	r.denied = true
	r.expiry = m.clock().Add(coolOff)
	return nil
}

// Denied reports the tree's denial against the store clock.
func (m *MemoryDenialStore) Denied(_ context.Context, tenantID, rootSessionID string) (bool, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.trees[memDenialKey{tenantID, rootSessionID}]
	if !ok {
		return false, time.Time{}, nil
	}
	return m.deniedLocked(r), r.expiry, nil
}

// Grant re-checks the denial and adds the requested grant clamped so the
// cumulative grant stays within limit on every dimension, all under the
// mutex.
// spec: §8.6 (In-flight requests), §8.6 (Extension state)
func (m *MemoryDenialStore) Grant(_ context.Context, tenantID, rootSessionID string, requested, limit Dimensions) (GrantResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rowLocked(tenantID, rootSessionID)
	if m.deniedLocked(r) {
		return GrantResult{}, ErrExtensionDenied
	}
	applied := ClampGrant(r.grants, requested, limit)
	r.grants = r.grants.add(applied)
	return GrantResult{Applied: applied, Total: r.grants}, nil
}

// Grants returns the tree's cumulative grants, zero for an unknown tree.
func (m *MemoryDenialStore) Grants(_ context.Context, tenantID, rootSessionID string) (Dimensions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.trees[memDenialKey{tenantID, rootSessionID}]; ok {
		return r.grants, nil
	}
	return Dimensions{}, nil
}

// Clear clears the tree's denial and cool-off.
func (m *MemoryDenialStore) Clear(_ context.Context, tenantID, rootSessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.trees[memDenialKey{tenantID, rootSessionID}]; ok {
		r.denied = false
		r.expiry = time.Time{}
	}
	return nil
}

// ClampGrant returns, per dimension, the part of requested that keeps
// cumulative within limit: min(requested, max(0, limit − cumulative)). A
// non-positive request applies nothing. Both DenialStore backings call it
// under their row lock.
// spec: §8.6 (Extension state)
func ClampGrant(cumulative, requested, limit Dimensions) Dimensions {
	var out Dimensions
	for _, d := range allDims {
		req := d.get(requested)
		head := d.get(limit) - d.get(cumulative)
		switch {
		case req <= 0 || head <= 0:
			d.set(&out, 0)
		case req < head:
			d.set(&out, req)
		default:
			d.set(&out, head)
		}
	}
	return out
}
