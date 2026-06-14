// SPDX-License-Identifier: MIT

// Package memstore is an in-memory agentpodstate.Store. It backs unit
// tests for the gateway consumers that read and write the §4.6.1
// agent_pod_state mirror (the recycle-counter writers on the
// ReportSessionScrub / ReportPodScrub path and the §5.2 recycle
// disposition reader) so a unit can substitute the store without a
// Postgres instance.
//
// The store keeps the same platform-global, pod-id-keyed model as the
// Postgres backend (§12.6): tenant_id is a denormalized convenience
// field, not an isolation boundary. updated_at is governed by an injected
// clock so a test controls mirror staleness deterministically.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
)

// row is one mirror entry: the public PodState plus the gateway-written
// recycle counters and the clock-stamped updated_at the staleness gauge
// reads. The counters are pointers so a NULL (never-written) counter is
// distinguishable from a written 0, matching the nullable Postgres
// columns; a nil pointer reads back as 0.
type row struct {
	pod               agentpodstate.PodState
	sessionsServed    *int
	scrubFailureCount *int
	updatedAt         time.Time
}

// Store is an in-memory agentpodstate.Store. Construct with New. It is
// safe for concurrent use.
type Store struct {
	// now supplies updated_at on every write. Injected so a unit controls
	// mirror staleness; defaults to time.Now.
	now func() time.Time

	mu   sync.Mutex
	rows map[string]*row
}

var _ agentpodstate.Store = (*Store)(nil)

// New returns an empty Store. now stamps updated_at on every write; a nil
// now defaults to time.Now, so production-shaped callers need not supply
// one. A unit injects a fixed clock to drive MirrorLagSeconds
// deterministically.
func New(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{now: now, rows: make(map[string]*row)}
}

// Put seeds or replaces a pod's mirror row, stamping updated_at from the
// injected clock. It is a test-and-setup affordance, not part of the
// agentpodstate.Store contract: the production mirror is written through
// Sync and ReconcileAll. Recycle counters reset to NULL on Put, matching
// a fresh WarmPoolController-mirrored row before the gateway has written
// either counter.
func (s *Store) Put(p agentpodstate.PodState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[p.PodID] = &row{pod: p, updatedAt: s.now()}
}

// Sync converges the mirror for poolID to observed: every observed row is
// upserted, then any row for poolID absent from observed is removed. An
// upsert preserves the existing recycle counters for a pod that is still
// present (the WarmPoolController mirror never writes them) and stamps a
// new row's counters as NULL. spec: §4.6.1 mirror Sync.
func (s *Store) Sync(_ context.Context, poolID string, observed []agentpodstate.PodState) error {
	if poolID == "" {
		return agentpodstate.ErrEmptyPoolID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	keep := make(map[string]struct{}, len(observed))
	for _, p := range observed {
		s.upsertLocked(p)
		keep[p.PodID] = struct{}{}
	}
	for id, r := range s.rows {
		if r.pod.PoolID == poolID {
			if _, ok := keep[id]; !ok {
				delete(s.rows, id)
			}
		}
	}
	return nil
}

// ReconcileAll converges the entire mirror to observed across all pools:
// every observed row is upserted, then every row absent from observed is
// removed. spec: §4.6.1 mirror reconciliation on recovery.
func (s *Store) ReconcileAll(_ context.Context, observed []agentpodstate.PodState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keep := make(map[string]struct{}, len(observed))
	for _, p := range observed {
		s.upsertLocked(p)
		keep[p.PodID] = struct{}{}
	}
	for id := range s.rows {
		if _, ok := keep[id]; !ok {
			delete(s.rows, id)
		}
	}
	return nil
}

// upsertLocked writes one observed row, preserving the recycle counters of
// an existing entry and stamping updated_at from the clock. The caller
// holds s.mu.
func (s *Store) upsertLocked(p agentpodstate.PodState) {
	r, ok := s.rows[p.PodID]
	if !ok {
		s.rows[p.PodID] = &row{pod: p, updatedAt: s.now()}
		return
	}
	r.pod = p
	r.updatedAt = s.now()
}

// MirrorLagSeconds returns now() - max(updated_at) over poolID's rows, the
// staleness of the mirror for that pool. A pool with no rows has no lag,
// so the result is 0.
func (s *Store) MirrorLagSeconds(_ context.Context, poolID string) (float64, error) {
	if poolID == "" {
		return 0, agentpodstate.ErrEmptyPoolID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest time.Time
	for _, r := range s.rows {
		if r.pod.PoolID == poolID && r.updatedAt.After(latest) {
			latest = r.updatedAt
		}
	}
	if latest.IsZero() {
		return 0, nil
	}
	lag := s.now().Sub(latest).Seconds()
	if lag < 0 {
		lag = 0
	}
	return lag, nil
}

// GetByPodID reads the single mirror row keyed on podID. The bool reports
// whether a row exists. An empty podID matches nothing.
func (s *Store) GetByPodID(_ context.Context, podID string) (agentpodstate.PodState, bool, error) {
	if podID == "" {
		return agentpodstate.PodState{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[podID]
	if !ok {
		return agentpodstate.PodState{}, false, nil
	}
	return r.pod, true, nil
}

// ClaimIdle selects the longest-idle row for poolID, marks it claimed for
// the session and tenant, and returns it. With no idle row it returns
// (agentpodstate.PodState{}, false, nil). Ordering by updated_at (oldest
// first) mirrors the Postgres FOR UPDATE SKIP LOCKED selection so the
// in-memory and Postgres backends claim the same pod from equal inventory.
func (s *Store) ClaimIdle(_ context.Context, poolID, sessionID, tenantID string) (agentpodstate.PodState, bool, error) {
	if poolID == "" {
		return agentpodstate.PodState{}, false, agentpodstate.ErrEmptyPoolID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	idle := make([]*row, 0)
	for _, r := range s.rows {
		if r.pod.PoolID == poolID && r.pod.State == "idle" {
			idle = append(idle, r)
		}
	}
	if len(idle) == 0 {
		return agentpodstate.PodState{}, false, nil
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].updatedAt.Before(idle[j].updatedAt) })

	r := idle[0]
	r.pod.State = "claimed"
	r.pod.SessionID = sessionID
	r.pod.TenantID = tenantID
	r.updatedAt = s.now()
	return r.pod, true, nil
}

// IncrementSessionsServed adds one to the pod's sessions_served counter
// and returns the new value, treating a NULL (never-written) counter as 0
// so the first increment returns 1. A missing pod row returns
// (0, false, nil) without writing. updated_at advances on the write.
// spec: §4.7 (ReportSessionScrub increments sessionsServed); §5.2.
func (s *Store) IncrementSessionsServed(_ context.Context, podID string) (int, bool, error) {
	if podID == "" {
		return 0, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[podID]
	if !ok {
		return 0, false, nil
	}
	r.sessionsServed = incr(r.sessionsServed)
	r.updatedAt = s.now()
	return *r.sessionsServed, true, nil
}

// IncrementScrubFailureCount adds one to the pod's scrub_failure_count
// counter and returns the new value, treating a NULL counter as 0. A
// missing pod row returns (0, false, nil) without writing.
// spec: §4.7 (ReportPodScrub increments scrubFailureCount); §5.2.
func (s *Store) IncrementScrubFailureCount(_ context.Context, podID string) (int, bool, error) {
	if podID == "" {
		return 0, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[podID]
	if !ok {
		return 0, false, nil
	}
	r.scrubFailureCount = incr(r.scrubFailureCount)
	r.updatedAt = s.now()
	return *r.scrubFailureCount, true, nil
}

// RecycleCounters reads both recycle counters back for the §5.2 recycle
// disposition. A NULL counter reads back as 0. A missing pod row returns
// (agentpodstate.RecycleCounters{}, false, nil).
// spec: §12.6 (agent_pod_state schema); §5.2 (recycle disposition).
func (s *Store) RecycleCounters(_ context.Context, podID string) (agentpodstate.RecycleCounters, bool, error) {
	if podID == "" {
		return agentpodstate.RecycleCounters{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[podID]
	if !ok {
		return agentpodstate.RecycleCounters{}, false, nil
	}
	return agentpodstate.RecycleCounters{
		SessionsServed:    deref(r.sessionsServed),
		ScrubFailureCount: deref(r.scrubFailureCount),
	}, true, nil
}

// incr returns a pointer to one more than the counter, treating a nil
// (NULL) counter as 0 so the first increment yields 1.
func incr(p *int) *int {
	v := deref(p) + 1
	return &v
}

// deref reads a nullable counter, mapping nil (NULL) to 0.
func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
