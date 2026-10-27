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
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Store is the in-memory backend. The zero value is not usable;
// construct with New.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]sessionstore.Session

	// reservations holds the session-ID reservations of the creation fence,
	// keyed by reservationKey(tenantID, sessionID). Guarded by mu, so a
	// reservation and the session row that ends it change together.
	// spec: §12.8 (Lifecycle fence and drain).
	reservations map[string]reservation

	// markers reads the creation fence's marker rows. Nil admits every
	// insert. Called under mu (WithCreationMarkers).
	markers sessionstore.MarkerReader

	// copyTranscript copies a transcript inside CreateWithTranscript, under
	// mu. Nil makes CreateWithTranscript return ErrTranscriptCopierUnwired.
	copyTranscript TranscriptCopier

	// podLockMu guards podLocks. podLocks holds the per-pod mutex the
	// §12.4 Redis-outage capacity gate (ReserveSlotUnderLock) takes so the
	// in-memory backend serializes the count-and-decide the same way the
	// Postgres backend serializes it under a per-pod advisory xact lock.
	podLockMu sync.Mutex
	podLocks  map[string]*sync.Mutex

	// now is the clock Update stamps UpdatedAt from and accrues the §6.2
	// active session age against. New wires the wall clock; NewWithClock
	// lets a test step it.
	now func() time.Time

	// postTerminalCap caps the §7.1 post-terminal window the slot queries
	// count (WithPostTerminalCap). Zero counts no terminal row.
	postTerminalCap time.Duration
}

// Option configures a Store at construction.
type Option func(*Store)

// PostTerminalCap returns the §7.1 post-terminal cap the store's slot
// queries count, as WithPostTerminalCap set it. The composition roots' wiring
// tests read it to pin the cap each production construction passes.
// spec: §7.1 (Post-terminal window).
func (s *Store) PostTerminalCap() time.Duration {
	return s.postTerminalCap
}

// WithPostTerminalCap sets the cap of the §7.1 post-terminal window that
// GetActiveSlotsByPod, ReserveSlotUnderLock, and ListPodsWithActiveSlots
// count, on the store's clock. A non-positive cap counts no terminal row,
// matching the Postgres store's omitted terminal arm.
// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration atomicity)
func WithPostTerminalCap(d time.Duration) Option {
	return func(s *Store) {
		s.postTerminalCap = d
	}
}

// holdsPodLocked reports whether row holds the pod it names on the store's
// clock (sessionstore.HoldsPod). Callers hold s.mu.
func (s *Store) holdsPodLocked(row sessionstore.Session) bool {
	return sessionstore.HoldsPod(row, s.now(), s.postTerminalCap)
}

// New returns an empty in-memory store on the wall clock.
func New(opts ...Option) *Store {
	return NewWithClock(nil, opts...)
}

// NewWithClock returns an empty in-memory store whose Update reads now for
// the UpdatedAt stamp and the §6.2 active-age accrual, so a test can advance
// a session's active age deterministically. A nil now selects the wall clock.
func NewWithClock(now func() time.Time, opts ...Option) *Store {
	if now == nil {
		now = time.Now
	}
	s := &Store{
		sessions:     make(map[string]sessionstore.Session),
		reservations: make(map[string]reservation),
		podLocks:     make(map[string]*sync.Mutex),
		now:          now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create inserts the row. Returns ErrAlreadyExists when ID is taken. It
// runs the creation fence first and deletes the reservation whose ID and
// owner match the row (fence.go).
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) Create(ctx context.Context, sess sessionstore.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx, sess, nil)
}

// CreateIfGeneration implements Store: Create with the source row's
// coordination generation checked under the same lock as the insert, so no
// Update can advance it between the check and the insert. A missing source
// row returns ErrNotFound; a different generation returns
// ErrGenerationAdvanced. Neither inserts anything.
// spec: §7.1 (Derive session semantics).
func (s *Store) CreateIfGeneration(ctx context.Context, sess sessionstore.Session, sourceTenantID, sourceID string, wantGen int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx, sess, func(sessionstore.Session) error {
		src, ok := s.sessions[sourceID]
		if !ok || src.TenantID != sourceTenantID {
			return sessionstore.ErrNotFound
		}
		if src.CoordinationGeneration != wantGen {
			return sessionstore.ErrGenerationAdvanced
		}
		return nil
	})
}

// createLocked is the shared insert of Create, CreateReserved, and
// CreateWithTranscript. Callers hold s.mu. before runs after every check
// and before the row is stored, so its error stores nothing.
func (s *Store) createLocked(ctx context.Context, sess sessionstore.Session, before func(sessionstore.Session) error) error {
	if err := s.fenceLocked(ctx, sess.TenantID, sess.UserID, sess.DelegationDepth > 0, sess.ParentSessionID); err != nil {
		return err
	}
	if _, ok := s.sessions[sess.ID]; ok {
		return sessionstore.ErrAlreadyExists
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = s.now().UTC()
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	// spec: §4.2 — v1 sessions are written at schema_version=1.
	if sess.SchemaVersion == 0 {
		sess.SchemaVersion = 1
	}
	// spec: §4.2, §10.1.1 — a newly created session row carries
	// coordination_generation = 1, so the value a replica holds for a
	// session no takeover and no resume driver write has advanced is
	// positive and each §10.1.2 step 1 compare-and-swap mints a value
	// strictly above the one it fences.
	if sess.CoordinationGeneration == 0 {
		sess.CoordinationGeneration = 1
	}
	// spec: §8.9 — root_session_id identifies the
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
	if before != nil {
		if err := before(sess); err != nil {
			return err
		}
	}
	s.deleteOwnReservationLocked(sess)
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

// GetPrimary implements sessionstore.PrimaryReader. The in-memory store has
// no read pool, so it is Get. spec: §12.3 (Postgres HA Requirements).
func (s *Store) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	return s.Get(ctx, tenantID, id)
}

// GetByID returns the session row whose ID equals id regardless of
// tenant, backing the §24.11 platform-admin session-investigation
// surface. spec: §24.11.
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
	prevState := row.State
	prevAge := row.AccumulatedSessionAgeSeconds
	prevRecoveryGen := row.RecoveryGeneration
	prevCoordGen := row.CoordinationGeneration
	prevRetryCount := row.RetryCount
	prevLastSeq := row.LastSeq
	if err := mutate(&row); err != nil {
		return sessionstore.Session{}, err
	}
	// spec: §4.2 — recovery_generation,
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
	// spec: §7.3 — sessions.last_seq is monotonic;
	// GREATEST-floor semantics match the pgstore so a late writer from
	// a sibling replica cannot rewind a freshly published Seq.
	// F-7.3.3.
	if row.LastSeq < prevLastSeq {
		row.LastSeq = prevLastSeq
	}
	if row.SchemaVersion == 0 {
		row.SchemaVersion = 1
	}
	now := monotonicNext(prevUpdatedAt, s.now().UTC())
	// spec: §6.2 (maxSessionAge timer behavior across states) — the store is
	// the only writer of the accumulated active age: discard any value the
	// mutator set, then accrue the active age the row gained since its
	// previous write, against the same instant the UpdatedAt stamp records.
	row.AccumulatedSessionAgeSeconds = prevAge
	sessionstore.AccrueSessionAge(prevState, prevUpdatedAt, &row, now)
	row.UpdatedAt = now
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
// order, after applying the supplied filter. The store has one backing,
// so f.Primary changes nothing: every read already sees every committed
// write. spec: §12.8 (Lifecycle fence and drain).
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
		// spec: §11.4 — full_revoke step 1 narrows to the
		// invalidation subject.
		if f.UserID != "" && row.UserID != f.UserID {
			continue
		}
		// spec: §15.1 — labels filter is AND-containment over
		// the row's §14 Labels map. F-15.1.15.
		if !labelsContain(row.Labels, f.Labels) {
			continue
		}
		// spec: §15.1 — `?includeDeriveFailures=false`
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
// want (AND-containment). An empty want matches every row. spec: §15.1. F-15.1.15.
func labelsContain(have, want map[string]string) bool {
	for k, v := range want {
		if hv, ok := have[k]; !ok || hv != v {
			return false
		}
	}
	return true
}

// ListPodlessSuspended returns at most limit of the tenant's suspended
// sessions that name no pod, with an id after afterID (every id when afterID
// is empty), in id order. It backs the gateway leader's §7.2 backlog
// re-check, which pages through the podless suspended population by id.
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (s *Store) ListPodlessSuspended(_ context.Context, tenantID, afterID string, limit int) ([]sessionstore.Session, error) {
	s.mu.RLock()
	out := make([]sessionstore.Session, 0)
	for _, row := range s.sessions {
		if row.TenantID != tenantID || row.State != session.StateSuspended || row.PodAssignment != "" {
			continue
		}
		if afterID != "" && row.ID <= afterID {
			continue
		}
		out = append(out, row)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListByRoot implements Store — every row whose RootSessionID equals
// rootSessionID within tenantID, ordered by CreatedAt ascending so a
// caller can rebuild the §8.9 tree by walking ParentSessionID. An empty
// rootSessionID returns no rows. spec: §8.9. F-8.9.7.
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
	// spec: §12.8 (Lifecycle fence and drain) — the erasure ends the
	// user's reservations with the session rows.
	for k, r := range s.reservations {
		if r.TenantID == tenantID && r.UserID == userID {
			delete(s.reservations, k)
		}
	}
	return deleted, nil
}

// GetActiveSlotsByPod implements Store: the §5.2 rehydration seed source.
// It returns the identifiers of the sessions that hold podID across every
// tenant: the non-terminal sessions whose pod_assignment names it and the
// terminal sessions in their §7.1 post-terminal window under the store's
// cap. An empty podID matches no session.
// spec: §5.2 (Post-recovery rehydration atomicity), §7.1 (Post-terminal window).
func (s *Store) GetActiveSlotsByPod(_ context.Context, podID string) ([]string, error) {
	if podID == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for _, row := range s.sessions {
		if row.PodAssignment == podID && s.holdsPodLocked(row) {
			ids = append(ids, row.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ListPodsWithActiveSlots implements Store: the distinct pods that a
// best-effort-per-slot session row holds across tenants, non-terminal or in
// its §7.1 post-terminal window under the store's cap.
// spec: §12.4 (Redis failure behavior), §7.1 (Post-terminal window).
func (s *Store) ListPodsWithActiveSlots(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	var pods []string
	for _, row := range s.sessions {
		if row.ScrubPolicy != "best-effort-per-slot" || !s.holdsPodLocked(row) {
			continue
		}
		if !seen[row.PodAssignment] {
			seen[row.PodAssignment] = true
			pods = append(pods, row.PodAssignment)
		}
	}
	sort.Strings(pods)
	return pods, nil
}

// ListStaleTerminalSessions implements Store: the identifiers in ids whose
// row is terminal with a TerminatedAt earlier than terminatedBefore, across
// tenants, with the pgstore filter that drops identifiers that are not
// UUIDs, because sessions.id is a UUID column there.
// spec: §5.2 (Slot assignment atomicity).
func (s *Store) ListStaleTerminalSessions(_ context.Context, ids []string, terminatedBefore time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var stale []string
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		row, ok := s.sessions[id]
		if !ok || !session.IsTerminal(row.State) || row.TerminatedAt.IsZero() {
			continue
		}
		if row.TerminatedAt.Before(terminatedBefore) {
			stale = append(stale, id)
		}
	}
	return stale, nil
}

// ReserveSlotUnderLock implements Store — the §12.4 Redis-outage capacity
// gate. It serializes the count-and-decide for podID under a per-pod mutex
// (the in-memory analogue of the Postgres per-pod advisory xact lock) so two
// concurrent admissions during a Redis outage cannot both observe the same
// free slot. The count predicate matches GetActiveSlotsByPod verbatim, so a
// session in its §7.1 post-terminal window keeps its slot.
// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity),
// §7.1 (Post-terminal window).
func (s *Store) ReserveSlotUnderLock(_ context.Context, podID string, maxConcurrent int32) (int32, bool, error) {
	if podID == "" {
		return 0, false, nil
	}
	if maxConcurrent < 1 {
		return 0, false, fmt.Errorf("memstore: maxConcurrent must be >= 1, got %d", maxConcurrent)
	}
	lock := s.podLock(podID)
	lock.Lock()
	defer lock.Unlock()

	s.mu.RLock()
	current := int32(0)
	for _, row := range s.sessions {
		if row.PodAssignment == podID && s.holdsPodLocked(row) {
			current++
		}
	}
	s.mu.RUnlock()
	if current >= maxConcurrent {
		return current, false, nil
	}
	return current + 1, true, nil
}

// podLock returns the per-pod mutex for the §12.4 fallback gate, creating
// it on first use.
func (s *Store) podLock(podID string) *sync.Mutex {
	s.podLockMu.Lock()
	defer s.podLockMu.Unlock()
	lock, ok := s.podLocks[podID]
	if !ok {
		lock = &sync.Mutex{}
		s.podLocks[podID] = lock
	}
	return lock
}

// PoolDrainStats implements the §15.1 pool-drain accounting:
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
// quota count: the number of non-terminal sessions other than suspended
// ones for tenantID, as session.CountsTowardConcurrencyCaps defines.
// spec: §11.1 (Concurrent-session count)
func (s *Store) CountActiveSessions(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && session.CountsTowardConcurrencyCaps(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsByUser implements the §11.1 per-user
// concurrent-session admission count: non-terminal sessions other than
// suspended ones (session.CountsTowardConcurrencyCaps) owned by userID
// within tenantID. spec: §11.1 (Concurrent-session count)
func (s *Store) CountActiveSessionsByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && row.UserID == userID && session.CountsTowardConcurrencyCaps(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsByRuntime implements the §11.1 per-runtime
// concurrent-session admission count: non-terminal sessions other than
// suspended ones (session.CountsTowardConcurrencyCaps) targeting
// runtimeRef within tenantID. spec: §11.1 (Concurrent-session count)
func (s *Store) CountActiveSessionsByRuntime(_ context.Context, tenantID, runtimeRef string) (int, error) {
	if tenantID == "" || runtimeRef == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if row.TenantID == tenantID && row.RuntimeRef == runtimeRef && session.CountsTowardConcurrencyCaps(row.State) {
			count++
		}
	}
	return count, nil
}

// CountActiveSessionsGlobal implements the §11.1 global
// concurrent-session admission count: non-terminal sessions other than
// suspended ones (session.CountsTowardConcurrencyCaps) across every
// tenant. spec: §11.1 (Concurrent-session count)
func (s *Store) CountActiveSessionsGlobal(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, row := range s.sessions {
		if session.CountsTowardConcurrencyCaps(row.State) {
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

// DeleteByTenant implements the §12.1 mandatory-erasure
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
	// spec: §12.8 (Lifecycle fence and drain) — tenant deletion ends the
	// tenant's reservations with its session rows.
	for k, r := range s.reservations {
		if r.TenantID == tenantID {
			delete(s.reservations, k)
		}
	}
	return deleted, nil
}
