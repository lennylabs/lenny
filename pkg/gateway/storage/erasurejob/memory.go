// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
)

// Memory is the in-memory Store, used in the memory and Source Mode
// persistence postures. It keeps parity with the Postgres store: one
// in-progress job per (tenant, user), Get's tenant filter, and job writes
// that set or clear the user's processing_restricted flag through the
// injected RestrictionWriter in the same step. Memory holds its own lock
// across each writer call, so a snapshot export of the jobs never observes
// a job write whose flag write has not finished.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Erasure job phase field (crash recovery)).
type Memory struct {
	// mu guards jobs and serializes every job write with its flag write.
	mu         sync.RWMutex
	jobs       map[string]Job
	restrictor RestrictionWriter
}

// MemoryOption configures a Memory store.
type MemoryOption func(*Memory)

// WithRestrictionWriter wires the processing_restricted write path. A
// Memory without one refuses Start, Restart, ClearRestriction, and
// Complete with ErrRestrictionWriterUnwired, because the job and the flag
// are one write.
func WithRestrictionWriter(w RestrictionWriter) MemoryOption {
	return func(m *Memory) { m.restrictor = w }
}

// NewMemory returns an empty Memory store.
func NewMemory(opts ...MemoryOption) *Memory {
	m := &Memory{jobs: map[string]Job{}}
	for _, o := range opts {
		o(m)
	}
	return m
}

var _ Store = (*Memory)(nil)

// Start implements Store.
func (m *Memory) Start(ctx context.Context, j Job) (Job, error) {
	if m.restrictor == nil {
		return Job{}, ErrRestrictionWriterUnwired
	}
	if j.ID == "" {
		return Job{}, ErrMissingID
	}
	if j.TenantID == "" || j.TenantID == AllTenants {
		return Job{}, ErrInvalidTenant
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.activeForUserLocked(j.TenantID, j.UserID, ""); ok {
		return Job{}, &ErrActiveJob{Job: cloneJob(active)}
	}
	if _, ok := m.jobs[j.ID]; ok {
		return Job{}, ErrAlreadyExists
	}
	if _, err := m.restrictor.SetErasureRestriction(ctx, j.TenantID, j.UserID, j.ID, true, ""); err != nil {
		return Job{}, err
	}
	m.jobs[j.ID] = cloneJob(j)
	return cloneJob(j), nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, id string) (Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok || (tenantID != AllTenants && j.TenantID != tenantID) {
		return Job{}, ErrNotFound
	}
	return cloneJob(j), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, id string, mutate func(*Job) error) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	j = cloneJob(j)
	if err := mutate(&j); err != nil {
		return Job{}, err
	}
	m.jobs[id] = cloneJob(j)
	return cloneJob(j), nil
}

// Restart implements Store.
func (m *Memory) Restart(ctx context.Context, id string) (Job, error) {
	if m.restrictor == nil {
		return Job{}, ErrRestrictionWriterUnwired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if j.Phase != PhaseFailed {
		return Job{}, ErrNotFailed
	}
	if active, ok := m.activeForUserLocked(j.TenantID, j.UserID, id); ok {
		return Job{}, &ErrActiveJob{Job: cloneJob(active)}
	}
	if _, err := m.restrictor.SetErasureRestriction(ctx, j.TenantID, j.UserID, id, true, ""); err != nil {
		return Job{}, err
	}
	j = cloneJob(j)
	RestartJob(&j, time.Now().UTC())
	m.jobs[id] = cloneJob(j)
	return j, nil
}

// ClearRestriction implements Store.
func (m *Memory) ClearRestriction(ctx context.Context, id string) (Job, error) {
	if m.restrictor == nil {
		return Job{}, ErrRestrictionWriterUnwired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if j.Phase != PhaseFailed {
		return Job{}, ErrNotFailed
	}
	if err := m.restrictor.ClearProcessingRestrictionForJob(ctx, j.TenantID, j.UserID, id); err != nil {
		return Job{}, err
	}
	return cloneJob(j), nil
}

// Complete implements Store.
func (m *Memory) Complete(ctx context.Context, id string, completedAt time.Time, expect Job) (Job, error) {
	if m.restrictor == nil {
		return Job{}, ErrRestrictionWriterUnwired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if !SameAttempt(j, expect) {
		return Job{}, ErrJobMoved
	}
	// The erasure may have removed the users row, so a missing row
	// completes with no flag write, as a 0-row write does.
	if _, err := m.restrictor.SetErasureRestriction(ctx, j.TenantID, j.UserID, "", false, id); err != nil &&
		!errors.Is(err, userstore.ErrNotFound) {
		return Job{}, err
	}
	j = cloneJob(j)
	CompleteJob(&j, completedAt)
	m.jobs[id] = cloneJob(j)
	return j, nil
}

// ListActive implements Store.
func (m *Memory) ListActive(_ context.Context) ([]Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if !j.Phase.Terminal() {
			out = append(out, cloneJob(j))
		}
	}
	SortByStart(out)
	return out, nil
}

// ReassertRestrictions sets the processing_restricted flag, naming the
// job, for the user of every job ListActive returns, as Start does. It is
// the Source Mode restore rule: the erasure_jobs and users snapshots are
// two exports taken one after the other, so a restored pair can hold an
// in-progress job beside a clear flag. It clears no flag; a flag naming a
// terminal or absent job keeps its restored value, fail-closed. It returns
// the first writer error, including userstore.ErrNotFound for a job whose
// users row is absent.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §17.4 (Local Development Mode: Source Mode).
func (m *Memory) ReassertRestrictions(ctx context.Context) error {
	if m.restrictor == nil {
		return ErrRestrictionWriterUnwired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Phase.Terminal() {
			continue
		}
		if _, err := m.restrictor.SetErasureRestriction(ctx, j.TenantID, j.UserID, j.ID, true, ""); err != nil {
			return err
		}
	}
	return nil
}

// activeForUserLocked returns the user's in-progress job other than
// exceptID; m.mu must be held.
func (m *Memory) activeForUserLocked(tenantID, userID, exceptID string) (Job, bool) {
	for id, j := range m.jobs {
		if id != exceptID && j.TenantID == tenantID && j.UserID == userID && !j.Phase.Terminal() {
			return j, true
		}
	}
	return Job{}, false
}

// RestartJob applies the restart write both stores make: back to
// initiated with the per-attempt fields cleared and the transition logged.
// StartedAt and OverrideReceipt stay, so the job's age continues from its
// original start and the receipt still records the override.
func RestartJob(j *Job, at time.Time) {
	j.Phase = PhaseInitiated
	j.Failure = ""
	j.CompletedAt = time.Time{}
	j.ErasedAt = time.Time{}
	j.ReceiptAt = time.Time{}
	j.Billing = BillingErasureOutcome{}
	j.PhaseLog = append(j.PhaseLog, PhaseTransition{Phase: PhaseInitiated, At: at})
}

// CompleteJob applies the completion write both stores make: completed at
// the given instant, with the transition logged.
func CompleteJob(j *Job, at time.Time) {
	j.Phase = PhaseCompleted
	j.CompletedAt = at
	j.PhaseLog = append(j.PhaseLog, PhaseTransition{Phase: PhaseCompleted, At: at})
}

// SortByStart orders jobs oldest StartedAt first, then by ID, the order
// ListActive returns.
func SortByStart(jobs []Job) {
	sort.Slice(jobs, func(a, b int) bool {
		if !jobs[a].StartedAt.Equal(jobs[b].StartedAt) {
			return jobs[a].StartedAt.Before(jobs[b].StartedAt)
		}
		return jobs[a].ID < jobs[b].ID
	})
}

// cloneJob deep-copies the Deleted map, the PhaseLog slice, and the
// OverrideReceipt map so a stored job and a returned copy never share
// mutable state. OverrideReceipt values are copied shallowly; they are
// written once by the erase handler and never mutated.
func cloneJob(j Job) Job {
	if j.Deleted != nil {
		d := make(map[string]int, len(j.Deleted))
		for k, v := range j.Deleted {
			d[k] = v
		}
		j.Deleted = d
	}
	if j.PhaseLog != nil {
		pl := make([]PhaseTransition, len(j.PhaseLog))
		copy(pl, j.PhaseLog)
		j.PhaseLog = pl
	}
	if j.OverrideReceipt != nil {
		o := make(map[string]any, len(j.OverrideReceipt))
		for k, v := range j.OverrideReceipt {
			o[k] = v
		}
		j.OverrideReceipt = o
	}
	return j
}
