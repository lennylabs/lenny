// SPDX-License-Identifier: MIT

// Package runtimeupgradestore persists the §10.5 RuntimeUpgrade state
// machine so an operator-driven runtime image rollout survives a gateway
// restart and resumes from the recorded phase (spec/10_gateway-internals.md
// §10.5 lines 466-540). One row per pool: the upgrade targets a single
// SandboxWarmPool, so the table is keyed by pool name. The
// pkg/gateway/runtimeupgrade Manager loads the row, drives the
// pkg/runtime/upgrade/state linear state machine, and writes the new
// phase back here under an optimistic version guard. The durable
// previous_pool_spec column preserves the old pool configuration for
// rollback (§10.5 line 507) until the upgrade reaches Complete.
package runtimeupgradestore

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrConflict is returned by Put when the stored row's version does not
// match the expected version, signalling a concurrent upgrade mutation
// (a second gateway replica or operator advanced the phase in between).
// The admin handler maps it to HTTP 409 so the operator retries against
// the fresh phase. spec: §10.5 line 468.
var ErrConflict = errors.New("runtimeupgradestore: version conflict")

// Record is the persisted §10.5 RuntimeUpgrade state for one pool. The
// fields mirror the RuntimeUpgrade CRD spec block (§10.5 lines 480-508):
// the current phase, the captured previous pool spec for rollback, and
// the per-upgrade knobs (canary, drain-first, schema-version, timeouts,
// auto-advance).
type Record struct {
	// Pool is the SandboxWarmPool name the upgrade targets. Primary key.
	Pool string

	// Phase is the §10.5 phase: pending, expanding, draining,
	// contracting, complete, or paused.
	Phase string

	// PriorPhase is the phase captured when Phase == paused so a resume
	// restores it. Empty unless paused.
	PriorPhase string

	// NewImage is the digest-pinned runtime image the upgrade rolls out
	// (§10.5 line 480 `--new-image <digest>`).
	NewImage string

	// PreviousPoolSpec is the JSON-serialized old pool configuration,
	// preserved so a rollback can recreate the old pool (§10.5 line 507).
	// It is retained until the upgrade reaches Complete.
	PreviousPoolSpec []byte

	// SchemaVersion gates Phase 3 schema migration: while it is set and
	// the upgrade is not Complete, the gateway blocks Phase 3 (§10.5
	// line 502). Empty when the image needs no schema migration.
	SchemaVersion string

	// DrainFirst forces Draining to fully complete (old pool
	// activePodCount == 0) before Contracting (§10.5 line 499).
	DrainFirst bool

	// CanaryPercent splits new-session routing to the new pool during
	// Expanding (§10.5 line 481). Zero routes all new sessions to the
	// new pool.
	CanaryPercent int

	// StabilizationWindowSeconds is the dwell the new pool must hold
	// idlePodCount >= minWarm before Expanding may exit (§10.5 line 481,
	// default 120).
	StabilizationWindowSeconds int64

	// DrainTimeoutSeconds bounds Draining; on expiry remaining sessions
	// are force-terminated with checkpoint (§10.5 line 482, default
	// maxSessionAge).
	DrainTimeoutSeconds int64

	// AutoAdvance auto-proceeds through phases without an operator
	// proceed call (§10.5 line 480).
	AutoAdvance bool

	// PauseReason and PausedAt capture the §10.5 line 494 pause
	// provenance. Both zero unless Phase == paused.
	PauseReason string
	PausedAt    time.Time

	// PhaseEnteredAt is the instant the current phase began, the basis
	// for the lenny_runtime_upgrade_phase_duration_seconds gauge.
	PhaseEnteredAt time.Time

	// DrainingSessions is the live count of sessions still draining on
	// the old pool, surfaced as lenny_runtime_upgrade_draining_sessions.
	DrainingSessions int

	// Version is the optimistic-concurrency tag. Get returns the current
	// version; Put requires the caller to echo it and bumps it on a
	// successful write.
	Version int64

	// CreatedAt and UpdatedAt are the row audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store persists per-pool RuntimeUpgrade records.
type Store interface {
	// Get returns the record for pool. ok is false when no upgrade has
	// been registered for the pool.
	Get(ctx context.Context, pool string) (rec Record, ok bool, err error)

	// Put writes rec keyed by rec.Pool. When a row already exists its
	// stored version must equal expectVersion or Put returns ErrConflict;
	// the first write (registration) passes expectVersion 0. The returned
	// Record carries the bumped Version and the write timestamp.
	Put(ctx context.Context, rec Record, expectVersion int64) (Record, error)

	// List returns every upgrade record. Used by the metrics emitter to
	// publish lenny_runtime_upgrade_state across all pools at startup.
	List(ctx context.Context) ([]Record, error)
}

// Memory is an in-process Store for tests and single-replica or
// memory-backed deployments. It is safe for concurrent use.
type Memory struct {
	mu    sync.Mutex
	rows  map[string]*Record
	clock func() time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{rows: map[string]*Record{}, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the write-timestamp clock. Tests substitute a
// fake clock so UpdatedAt is deterministic.
func (m *Memory) WithClock(clock func() time.Time) *Memory {
	m.clock = clock
	return m
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, pool string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[pool]
	if !ok {
		return Record{}, false, nil
	}
	return cloneRecord(*row), true, nil
}

// Put implements Store with an optimistic version guard.
func (m *Memory) Put(_ context.Context, rec Record, expectVersion int64) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	existing, ok := m.rows[rec.Pool]
	if ok {
		if existing.Version != expectVersion {
			return Record{}, ErrConflict
		}
		rec.CreatedAt = existing.CreatedAt
	} else {
		if expectVersion != 0 {
			return Record{}, ErrConflict
		}
		rec.CreatedAt = now
	}
	rec.Version = expectVersion + 1
	rec.UpdatedAt = now
	stored := cloneRecord(rec)
	m.rows[rec.Pool] = &stored
	return cloneRecord(stored), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.rows))
	for _, row := range m.rows {
		out = append(out, cloneRecord(*row))
	}
	return out, nil
}

func cloneRecord(r Record) Record {
	if r.PreviousPoolSpec != nil {
		spec := make([]byte, len(r.PreviousPoolSpec))
		copy(spec, r.PreviousPoolSpec)
		r.PreviousPoolSpec = spec
	}
	return r
}

// Compile-time check.
var _ Store = (*Memory)(nil)
