// SPDX-License-Identifier: MIT

// Package erasurejob tracks §12.8 GDPR erasure jobs: the DeleteByUser
// deletion job initiated by POST /v1/admin/users/{user_id}/erase and
// queried via GET /v1/admin/erasure-jobs/{job_id}.
//
// A Job records the §12.8 phase field so a controller restart resumes
// from the persisted safe point rather than re-running completed
// steps. The Runner (runner.go) drives a job through its phases while
// the erasure.Orchestrator deletes the user's data from every store.
package erasurejob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Phase is the §12.8 erasure-job lifecycle state. It is persisted with
// every step transition so a controller restart resumes from the safe
// point: a job in store_deleting re-runs the idempotent DeleteByUser
// sequence, and a job in a terminal phase does no further work.
type Phase string

const (
	// PhaseInitiated — the job record exists; no store deletion has run.
	PhaseInitiated Phase = "initiated"
	// PhaseStoreDeleting — the dependency-ordered store deletion is in
	// progress.
	PhaseStoreDeleting Phase = "store_deleting"
	// PhasePseudonymizing — billing events are being pseudonymized.
	PhasePseudonymizing Phase = "pseudonymizing"
	// PhaseVerifying — the post-deletion salt-removal check is running.
	PhaseVerifying Phase = "verifying"
	// PhaseCompleted — every step finished and the erasure receipt is
	// written.
	PhaseCompleted Phase = "completed"
	// PhaseFailed — a step errored; Job.Failure carries the reason and
	// the job may be retried.
	PhaseFailed Phase = "failed"
)

// Terminal reports whether p is an end state. A job in a terminal
// phase does no further work, so re-running it is a no-op.
func (p Phase) Terminal() bool {
	return p == PhaseCompleted || p == PhaseFailed
}

// Job is one §12.8 DeleteByUser erasure job.
type Job struct {
	// ID is the opaque job identifier returned to the admin caller.
	ID string
	// TenantID + UserID identify the erasure target.
	TenantID string
	UserID   string
	// Phase is the current §12.8 lifecycle state.
	Phase Phase
	// StartedAt is the instant the job record was created.
	StartedAt time.Time
	// CompletedAt is set when Phase becomes completed or failed.
	CompletedAt time.Time
	// Deleted maps each store's name to its deleted-row count, for the
	// stores the job has erased so far.
	Deleted map[string]int
	// Total is the sum of Deleted across stores.
	Total int
	// Billing records the §12.8 billing-pseudonymization outcome: the
	// disposition, the count of events rewritten, and whether the
	// post-erasure verification passed. Zero value when the job has
	// not reached the pseudonymizing phase or no BillingEraser is
	// wired into the Runner.
	Billing BillingErasureOutcome
	// Failure carries the error reason when Phase is failed.
	Failure string
}

// Sentinel errors.
var (
	// ErrNotFound — no job with the requested id exists.
	ErrNotFound = errors.New("erasurejob: job not found")
	// ErrAlreadyExists — a job with the same id already exists.
	ErrAlreadyExists = errors.New("erasurejob: job already exists")
	// ErrMissingID — Create was called with an empty job id.
	ErrMissingID = errors.New("erasurejob: job ID required")
)

// Store is the erasure-job registry contract. Every method is
// goroutine-safe.
type Store interface {
	// Create persists a fresh job record. Returns ErrAlreadyExists when
	// a job with the same ID already exists.
	Create(ctx context.Context, j Job) error
	// Get returns the job whose ID equals id. Returns ErrNotFound when
	// no matching job exists.
	Get(ctx context.Context, id string) (Job, error)
	// Update applies mutate to a copy of the job and persists the
	// result. Returns ErrNotFound when the job is missing.
	Update(ctx context.Context, id string, mutate func(*Job) error) (Job, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu   sync.RWMutex
	jobs map[string]Job
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{jobs: map[string]Job{}}
}

// Create implements Store.
func (m *Memory) Create(_ context.Context, j Job) error {
	if j.ID == "" {
		return ErrMissingID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; ok {
		return ErrAlreadyExists
	}
	m.jobs[j.ID] = cloneJob(j)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, id string) (Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
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

// cloneJob deep-copies the Deleted map so a stored job and a returned
// copy never share mutable state.
func cloneJob(j Job) Job {
	if j.Deleted != nil {
		d := make(map[string]int, len(j.Deleted))
		for k, v := range j.Deleted {
			d[k] = v
		}
		j.Deleted = d
	}
	return j
}

// NewID returns a fresh erasure-job identifier — 8 random bytes
// hex-encoded with an `erasure_` prefix.
func NewID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "erasure_" + hex.EncodeToString(buf[:])
}
