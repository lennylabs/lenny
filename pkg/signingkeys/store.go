// SPDX-License-Identifier: MIT

package signingkeys

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Record is one stored signing key: the row of platform_signing_keys. The
// secret is held only in its wrapped form. RetireAfter and DeleteAfter are
// zero while the key is its purpose's newest key.
type Record struct {
	Purpose       string
	KeyID         string
	Algorithm     string
	KEKAlias      string
	WrappedSecret []byte
	PublicKey     []byte
	RotationSlot  int64
	ActiveFrom    time.Time
	RetireAfter   time.Time
	DeleteAfter   time.Time
}

// Store persists the wrapped signing keys of every purpose. A Store is
// shared by every replica, so each method is one atomic operation against
// the shared state. Implementations are safe for concurrent use.
type Store interface {
	// List returns the purpose's keys whose DeleteAfter is unset or after
	// now, in ascending rotation-slot order.
	List(ctx context.Context, purpose string, now time.Time) ([]Record, error)

	// Insert writes rec as its purpose's key at rec.RotationSlot and, in
	// the same transaction, retires the key at the slot before it: that
	// key's RetireAfter becomes rec.ActiveFrom and its DeleteAfter becomes
	// rec.ActiveFrom plus verifyFor. It reports false, with no error and
	// no change, when another writer already holds the slot.
	Insert(ctx context.Context, rec Record, verifyFor time.Duration) (bool, error)

	// DeleteExpired deletes the purpose's keys whose DeleteAfter is at or
	// before now and returns how many it deleted.
	DeleteExpired(ctx context.Context, purpose string, now time.Time) (int, error)
}

// MemoryStore is an in-process Store. It serves the tests and a gateway
// that runs without Postgres, which is a single replica, so no other
// replica needs its keys.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string][]Record // by purpose, ascending slot
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string][]Record{}}
}

// List implements Store.
func (m *MemoryStore) List(_ context.Context, purpose string, now time.Time) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.rows[purpose] {
		if r.DeleteAfter.IsZero() || r.DeleteAfter.After(now) {
			out = append(out, copyRecord(r))
		}
	}
	return out, nil
}

// Insert implements Store.
func (m *MemoryStore) Insert(_ context.Context, rec Record, verifyFor time.Duration) (bool, error) {
	if rec.Purpose == "" || rec.KeyID == "" || rec.RotationSlot < 1 {
		return false, errors.New("signingkeys: record needs a purpose, a key id, and a rotation slot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[rec.Purpose]
	for _, r := range rows {
		if r.RotationSlot == rec.RotationSlot {
			return false, nil
		}
		if r.KeyID == rec.KeyID {
			return false, errors.New("signingkeys: duplicate key id " + rec.KeyID)
		}
	}
	for i := range rows {
		if rows[i].RotationSlot == rec.RotationSlot-1 {
			rows[i].RetireAfter = rec.ActiveFrom
			rows[i].DeleteAfter = rec.ActiveFrom.Add(verifyFor)
		}
	}
	rows = append(rows, copyRecord(rec))
	sort.Slice(rows, func(i, j int) bool { return rows[i].RotationSlot < rows[j].RotationSlot })
	m.rows[rec.Purpose] = rows
	return true, nil
}

// DeleteExpired implements Store.
func (m *MemoryStore) DeleteExpired(_ context.Context, purpose string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[purpose]
	kept := rows[:0]
	n := 0
	for _, r := range rows {
		if !r.DeleteAfter.IsZero() && !r.DeleteAfter.After(now) {
			n++
			continue
		}
		kept = append(kept, r)
	}
	m.rows[purpose] = kept
	return n, nil
}

// copyRecord returns rec with its byte slices copied, so a caller cannot
// change the stored row through the returned value.
func copyRecord(rec Record) Record {
	rec.WrappedSecret = append([]byte(nil), rec.WrappedSecret...)
	if rec.PublicKey != nil {
		rec.PublicKey = append([]byte(nil), rec.PublicKey...)
	}
	return rec
}
