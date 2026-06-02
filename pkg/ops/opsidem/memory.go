// SPDX-License-Identifier: MIT

package opsidem

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is the in-process §25.4 idempotency store. lenny-ops runs
// it in single-process degraded mode (and dev); the Postgres-backed
// pgstore is the durable store that survives a restart and coordinates
// across replicas. The MemoryStore never returns ErrStoreUnavailable —
// there is no remote dependency to lose.
type MemoryStore struct {
	mu      sync.Mutex
	records map[memKey]Record
}

type memKey struct {
	key      string
	callerID string
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[memKey]Record)}
}

// Claim implements Store. It applies lazy cleanup of the claimed key's
// expired row before deciding the outcome, then inserts an in-progress
// row when none is live.
func (m *MemoryStore) Claim(_ context.Context, key, callerID, endpoint string, ttl time.Duration, now time.Time) (Record, ClaimResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := memKey{key, callerID}
	if rec, ok := m.records[k]; ok {
		if now.Before(rec.ExpiresAt) {
			if rec.Status == StatusInProgress {
				return rec, ClaimInProgress, nil
			}
			return rec, ClaimReplay, nil
		}
		// Expired: lazy cleanup, fall through to a fresh insert.
		delete(m.records, k)
	}

	// §25.4 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER: a live row exists for
	// this key under a different caller_id (accidental cross-caller reuse).
	for ek, er := range m.records {
		if ek.key == key && ek.callerID != callerID && now.Before(er.ExpiresAt) {
			return Record{}, ClaimOwnedByOther, nil
		}
	}

	rec := Record{
		Key:       key,
		CallerID:  callerID,
		Endpoint:  endpoint,
		Status:    StatusInProgress,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	m.records[k] = rec
	return rec, ClaimInserted, nil
}

// Complete implements Store.
func (m *MemoryStore) Complete(_ context.Context, key, callerID string, statusCode int, response []byte, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memKey{key, callerID}
	rec, ok := m.records[k]
	if !ok {
		return nil
	}
	rec.Status = StatusCompleted
	rec.StatusCode = statusCode
	rec.Response = append([]byte(nil), response...)
	m.records[k] = rec
	return nil
}

// Fail implements Store.
func (m *MemoryStore) Fail(_ context.Context, key, callerID string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// §25.4: a failed (server-error) mutation must be retryable, so drop
	// the row entirely rather than leaving a failed terminal record that
	// would replay the error for the TTL window.
	delete(m.records, memKey{key, callerID})
	return nil
}

// PruneExpired implements Store.
func (m *MemoryStore) PruneExpired(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, rec := range m.records {
		if !now.Before(rec.ExpiresAt) {
			delete(m.records, k)
			n++
		}
	}
	return n, nil
}

// Compile-time guard.
var _ Store = (*MemoryStore)(nil)
