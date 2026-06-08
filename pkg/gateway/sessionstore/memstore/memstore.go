// SPDX-License-Identifier: MIT

// Package memstore is an in-memory SessionStore implementation used
// by the minimal gateway in cmd/lenny-gateway and by every tier-3
// REST-contract test. Production deployments use the Postgres-backed
// implementation that ships in a later phase; the wire-level
// behaviour of both backends matches the SessionStore contract from
// pkg/gateway/sessionstore, so swapping the backend changes no
// caller code.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// Store is the in-memory backend. The zero value is not usable;
// construct with New.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]sessionstore.Session
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{sessions: make(map[string]sessionstore.Session)}
}

// Create inserts the row. Returns ErrAlreadyExists when ID is taken.
func (s *Store) Create(_ context.Context, sess sessionstore.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.ID]; ok {
		return sessionstore.ErrAlreadyExists
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	// spec: §4.2 line 156 — v1 sessions are written at schema_version=1.
	if sess.SchemaVersion == 0 {
		sess.SchemaVersion = 1
	}
	// spec: §8.9 line 1010 — root_session_id identifies the
	// delegation-tree apex on every row in the tree. When the caller
	// did not stamp one, the store inherits the parent's
	// RootSessionID so children share the parent's root automatically;
	// a standalone session (no parent) becomes its own root. F-8.9.8.
	if sess.RootSessionID == "" {
		if sess.ParentSessionID != "" {
			if parent, ok := s.sessions[sess.ParentSessionID]; ok && parent.TenantID == sess.TenantID {
				sess.RootSessionID = parent.RootSessionID
				if sess.RootSessionID == "" {
					sess.RootSessionID = parent.ID
				}
			}
		}
		if sess.RootSessionID == "" {
			sess.RootSessionID = sess.ID
		}
	}
	s.sessions[sess.ID] = sess
	return nil
}

// Get returns the row keyed by (tenant, id). Returns ErrNotFound
// when the row is missing OR when the row's tenant_id does not match
// — cross-tenant misses look identical to "doesn't exist" by
// design (§4.2 tenant isolation).
func (s *Store) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// GetByID returns the session row whose ID equals id regardless of
// tenant, backing the §24.11 platform-admin session-investigation
// surface. spec: §24.11 lines 135-136.
func (s *Store) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.sessions[id]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// Update applies mutate to the row in-place under the store lock,
// then writes the resulting row back. Returns ErrNotFound when the
// row is missing or tenant-mismatched.
//
// UpdatedAt strictly advances on every successful Update — when the
// host wall-clock has not advanced since the prior write (common on
// fast machines), the new timestamp is clamped to the prior
// UpdatedAt + 1ns so callers can rely on strict monotonicity for
// change-detection and watch streams.
func (s *Store) Update(_ context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	prevUpdatedAt := row.UpdatedAt
	prevRecoveryGen := row.RecoveryGeneration
	prevCoordGen := row.CoordinationGeneration
	prevRetryCount := row.RetryCount
	prevLastSeq := row.LastSeq
	if err := mutate(&row); err != nil {
		return sessionstore.Session{}, err
	}
	// spec: §4.2 line 156 / line 158 — recovery_generation,
	// coordination_generation, and retry_count are monotonically
	// non-decreasing. Clamp the floor here so an accidental rollback
	// in the mutate callback cannot violate the invariant; the
	// pgstore enforces the same floor and the DB CHECK constraint
	// catches the impossible negative.
	if row.RecoveryGeneration < prevRecoveryGen {
		row.RecoveryGeneration = prevRecoveryGen
	}
	if row.CoordinationGeneration < prevCoordGen {
		row.CoordinationGeneration = prevCoordGen
	}
	if row.RetryCount < prevRetryCount {
		row.RetryCount = prevRetryCount
	}
	// spec: §7.3 line 397 — sessions.last_seq is monotonic;
	// GREATEST-floor semantics match the pgstore so a late writer from
	// a sibling replica cannot rewind a freshly published Seq.
	// F-7.3.3.
	if row.LastSeq < prevLastSeq {
		row.LastSeq = prevLastSeq
	}
	if row.SchemaVersion == 0 {
		row.SchemaVersion = 1
	}
	row.UpdatedAt = monotonicNext(prevUpdatedAt, time.Now().UTC())
	s.sessions[id] = row
	return row, nil
}

// monotonicNext returns now clamped to prev+1ns when the wall-clock
// has not advanced past prev. Used so UpdatedAt strictly advances on
// every Update.
func monotonicNext(prev, now time.Time) time.Time {
	if !now.After(prev) {
		return prev.Add(time.Nanosecond)
	}
	return now.UTC()
}

// List returns every row for the tenant in created-at descending
// order, after applying the supplied filter.
func (s *Store) List(_ context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]sessionstore.Session, 0)
	for _, row := range s.sessions {
		if row.TenantID != tenantID {
			continue
		}
		if f.State != "" && row.State != f.State {
			continue
		}
		if f.RuntimeRef != "" && row.RuntimeRef != f.RuntimeRef {
			continue
		}
		if f.FailureClass != "" && row.FailureClass != f.FailureClass {
			continue
		}
		// spec: §11.4 line 256 — full_revoke step 1 narrows to the
		// invalidation subject.
		if f.UserID != "" && row.UserID != f.UserID {
			continue
		}
		// spec: §15.1 line 598 — labels filter is AND-containment over
		// the row's §14 Labels map. F-15.1.15.
		if !labelsContain(row.Labels, f.Labels) {
			continue
		}
		// spec: §15.1 lines 652, 661 — `?includeDeriveFailures=false`
		// drops the audit-only derive_failure rows. F-15.1.14.
		if f.ExcludeDeriveFailures && row.FailureClass == session.FailureClassDeriveFailure {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// labelsContain reports whether have contains every key=value pair in
// want (AND-containment). An empty want matches every row. spec: §15.1
// line 598. F-15.1.15.
func labelsContain(have, want map[string]string) bool {
	for k, v := range want {
		if hv, ok := have[k]; !ok || hv != v {
			return false
		}
	}
	return true
}

// ListByRoot implements Store — every row whose RootSessionID equals
// rootSessionID within tenantID, ordered by CreatedAt ascending so a
// caller can rebuild the §8.9 tree by walking ParentSessionID. An empty
// rootSessionID returns no rows. spec: §8.9 line 1010. F-8.9.7.
func (s *Store) ListByRoot(_ context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error) {
	if rootSessionID == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]sessionstore.Session, 0)
	for _, row := range s.sessions {
		if row.TenantID != tenantID {
			continue
		}
		rsid := row.RootSessionID
		if rsid == "" {
			rsid = row.ID
		}
		if rsid != rootSessionID {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Delete removes the row. Returns ErrNotFound when missing or
// tenant-mismatched.
func (s *Store) Delete(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.ErrNotFound
	}
	delete(s.sessions, id)
	return nil
}

// DeleteByUser implements Store — the §12.8 GDPR-erasure adapter.
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, row := range s.sessions {
		if row.TenantID == tenantID && row.UserID == userID {
			delete(s.sessions, id)
			deleted++
		}
	}
	return deleted, nil
}

// GetActiveSlotsByPod implements Store — the §5.2 rehydration seed
// source. It counts live (non-terminal) sessions bound to podID across
// every tenant. An empty podID matches no slot (sessions with no pod
// binding carry an empty pod_assignment and are never counted).
func (s *Store) GetActiveSlotsByPod(_ context.Context, podID string) (int, error) {
	if podID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.PodAssignment == podID && !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// PoolDrainStats implements the §15.1 line 797 pool-drain accounting:
// the count of live (non-terminal) sessions bound to poolRef across
// every tenant and the create time of the longest-running such session
// (the oldest created_at). An empty poolRef matches no session.
func (s *Store) PoolDrainStats(_ context.Context, poolRef string) (int, time.Time, error) {
	if poolRef == "" {
		return 0, time.Time{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	var oldest time.Time
	for _, row := range s.sessions {
		if row.PoolRef != poolRef || session.IsTerminal(row.State) {
			continue
		}
		count++
		if oldest.IsZero() || row.CreatedAt.Before(oldest) {
			oldest = row.CreatedAt
		}
	}
	return count, oldest, nil
}

// CountActiveSessions implements the §11.2 per-tenant concurrent-session
// quota count: the number of live (non-terminal) sessions for tenantID.
func (s *Store) CountActiveSessions(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsByUser implements the §11.1 per-user
// concurrent-session admission count: live (non-terminal) sessions
// owned by userID within tenantID.
func (s *Store) CountActiveSessionsByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && row.UserID == userID && !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsByRuntime implements the §11.1 per-runtime
// concurrent-session admission count: live (non-terminal) sessions
// targeting runtimeRef within tenantID.
func (s *Store) CountActiveSessionsByRuntime(_ context.Context, tenantID, runtimeRef string) (int, error) {
	if tenantID == "" || runtimeRef == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && row.RuntimeRef == runtimeRef && !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsGlobal implements the §11.1 global
// concurrent-session admission count: live (non-terminal) sessions
// across every tenant.
func (s *Store) CountActiveSessionsGlobal(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsInRecoveryGlobal implements the §16.5 Session
// availability SLI numerator: live sessions across every tenant in a
// retry/recovery state. F-16.5.3.
func (s *Store) CountActiveSessionsInRecoveryGlobal(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if session.IsRecovery(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveDelegatedChildrenByUser implements the §11.1 per-user
// active-delegated-children admission count: live (non-terminal)
// sessions owned by userID within tenantID that carry a non-empty
// ParentSessionID (i.e. they were spawned via delegation).
func (s *Store) CountActiveDelegatedChildrenByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && row.UserID == userID &&
			row.ParentSessionID != "" && !session.IsTerminal(row.State) {
			count++
		}
	}
	return count, nil
}

// DeleteByTenant implements the §12.1 / §14.10 mandatory-erasure
// interface. Removes every session belonging to tenantID.
func (s *Store) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, row := range s.sessions {
		if row.TenantID == tenantID {
			delete(s.sessions, id)
			deleted++
		}
	}
	return deleted, nil
}
