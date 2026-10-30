// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// steppedStamp is the stepped-clock view of one stored write of a row: the
// updated_at the write stamped on the clock the harness steps, the active
// session age accrued against that clock, and the state-entry instant on
// that clock.
type steppedStamp struct {
	updatedAt      time.Time
	ageSecs        float64
	stateEnteredAt time.Time
}

// steppedRow is the stepped-clock history of one row, keyed by the
// updated_at the store wrote, with the most recent write kept as the
// fallback for a write the decorator has not recorded yet.
type steppedRow struct {
	byStored map[time.Time]steppedStamp
	latest   steppedStamp
}

// SteppedClockStore presents a session store that stamps rows from the wall
// clock as though it stamped them from the harness's stepped clock. A
// scenario on the compose Postgres steps the clock by sweep intervals while
// little wall time passes, and the gateway's timers compare the stepped now
// with a row's updated_at, state_entered_at, and active session age, so
// wall-clock stamps would expire windows early. The decorator keeps the
// production store free of a clock seam: Create stamps created_at and
// updated_at from the stepped clock and records created_at as the stepped
// state_entered_at, Update records the stepped updated_at, the active age
// accrued against it, and the stepped state_entered_at for the updated_at
// the store wrote, and every read, including
// the row a mutator sees, carries the recorded stepped values in place of
// the stored ones.
//
// A row written outside the decorator keeps its stored values. A read that
// meets an updated_at the decorator has not recorded yet, because a
// concurrent Update has committed and not yet returned, waits briefly for
// the record and then uses the row's latest recorded stamp.
//
// spec: §6.2 (maxSessionAge timer behavior across states), §6.2 (Pod State
// Machine), §7.3 (Retry and Resume)
type SteppedClockStore struct {
	sessionstore.Store
	now func() time.Time

	mu       sync.Mutex
	rows     map[string]*steppedRow
	inFlight map[string]int
}

// steppedRecordWait bounds how long a read waits for a concurrent Update to
// record the stepped stamp of the write it committed.
const steppedRecordWait = 50 * time.Millisecond

// NewSteppedClockStore wraps inner so its rows read as stamped from now.
func NewSteppedClockStore(inner sessionstore.Store, now func() time.Time) *SteppedClockStore {
	return &SteppedClockStore{Store: inner, now: now, rows: map[string]*steppedRow{}, inFlight: map[string]int{}}
}

// record stores the stepped stamp of the write that stored updated_at.
func (s *SteppedClockStore) record(id string, stored time.Time, st steppedStamp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	if r == nil {
		r = &steppedRow{byStored: map[time.Time]steppedStamp{}}
		s.rows[id] = r
	}
	r.byStored[stored.UTC()] = st
	r.latest = st
}

// lookup returns the stepped stamp recorded for the write that stored
// updated_at, whether the row is known, and whether the stamp is final: an
// exact match, or the latest stamp when no Update of the row other than the
// caller's own (own is 1 inside a mutator and 0 for a read) is in flight.
func (s *SteppedClockStore) lookup(id string, stored time.Time, own int) (steppedStamp, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	if r == nil {
		return steppedStamp{}, false, false
	}
	if st, ok := r.byStored[stored.UTC()]; ok {
		return st, true, true
	}
	return r.latest, true, s.inFlight[id] <= own
}

// translate replaces row's stored updated_at, active age, and state-entry
// instant with their stepped values. own counts the caller's own in-flight Update of the row.
func (s *SteppedClockStore) translate(row *sessionstore.Session, own int) {
	deadline := time.Now().Add(steppedRecordWait)
	for {
		st, known, settled := s.lookup(row.ID, row.UpdatedAt, own)
		if !known {
			return
		}
		if settled || time.Now().After(deadline) {
			row.UpdatedAt = st.updatedAt
			row.AccumulatedSessionAgeSeconds = st.ageSecs
			row.StateEnteredAt = st.stateEnteredAt
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// translateAll translates each row in place.
func (s *SteppedClockStore) translateAll(rows []sessionstore.Session) []sessionstore.Session {
	for i := range rows {
		s.translate(&rows[i], 0)
	}
	return rows
}

// Create stamps created_at and updated_at from the stepped clock when the
// caller left them unset, and records the row's first stepped stamp, whose
// state-entry instant is created_at as the store sets it.
func (s *SteppedClockStore) Create(ctx context.Context, row sessionstore.Session) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = s.now().UTC().Truncate(time.Microsecond)
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if err := s.Store.Create(ctx, row); err != nil {
		return err
	}
	s.record(row.ID, row.UpdatedAt, steppedStamp{
		updatedAt:      row.UpdatedAt,
		ageSecs:        row.AccumulatedSessionAgeSeconds,
		stateEnteredAt: row.CreatedAt,
	})
	return nil
}

// Update runs mut on the row as stamped from the stepped clock, then
// records the stepped updated_at of the write, the active age accrued
// against it, and the stepped state-entry instant, with the store's own
// accrual and stamp rules.
func (s *SteppedClockStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	s.inFlight[id]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inFlight[id]--
		s.mu.Unlock()
	}()
	var prev sessionstore.Session
	updated, err := s.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		s.translate(r, 1)
		prev = *r
		return mut(r)
	})
	if err != nil {
		return updated, err
	}
	stored := updated.UpdatedAt
	at := pgtenant.MonotonicNext(prev.UpdatedAt, s.now())
	updated.AccumulatedSessionAgeSeconds = prev.AccumulatedSessionAgeSeconds
	sessionstore.AccrueSessionAge(prev.State, prev.UpdatedAt, &updated, at)
	sessionstore.StampStateEntry(prev.State, prev.StateEnteredAt, &updated, at)
	updated.UpdatedAt = at
	s.record(id, stored, steppedStamp{
		updatedAt:      at,
		ageSecs:        updated.AccumulatedSessionAgeSeconds,
		stateEnteredAt: updated.StateEnteredAt,
	})
	return updated, nil
}

// Get reads a row with its stepped stamps.
func (s *SteppedClockStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := s.Store.Get(ctx, tenantID, id)
	if err == nil {
		s.translate(&row, 0)
	}
	return row, err
}

// GetPrimary reads a row from the primary with its stepped stamps.
func (s *SteppedClockStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	pr, ok := s.Store.(sessionstore.PrimaryReader)
	if !ok {
		return s.Get(ctx, tenantID, id)
	}
	row, err := pr.GetPrimary(ctx, tenantID, id)
	if err == nil {
		s.translate(&row, 0)
	}
	return row, err
}

// GetByID reads a row by ID with its stepped stamps.
func (s *SteppedClockStore) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	row, err := s.Store.GetByID(ctx, id)
	if err == nil {
		s.translate(&row, 0)
	}
	return row, err
}

// List lists rows with their stepped stamps.
func (s *SteppedClockStore) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	rows, err := s.Store.List(ctx, tenantID, filter)
	return s.translateAll(rows), err
}

// ListByRoot lists a tree's rows with their stepped stamps.
func (s *SteppedClockStore) ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error) {
	rows, err := s.Store.ListByRoot(ctx, tenantID, rootSessionID)
	return s.translateAll(rows), err
}

// ListPodlessSuspended lists one page of podless suspended rows with their
// stepped stamps, for the gateway leader's backlog re-check.
func (s *SteppedClockStore) ListPodlessSuspended(ctx context.Context, tenantID, afterID string, limit int) ([]sessionstore.Session, error) {
	lister, ok := s.Store.(interface {
		ListPodlessSuspended(ctx context.Context, tenantID, afterID string, limit int) ([]sessionstore.Session, error)
	})
	if !ok {
		return nil, fmt.Errorf("lifecycleharness: the session store lists no podless suspended rows")
	}
	rows, err := lister.ListPodlessSuspended(ctx, tenantID, afterID, limit)
	return s.translateAll(rows), err
}
