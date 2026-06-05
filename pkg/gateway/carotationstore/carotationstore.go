// SPDX-License-Identifier: MIT

// Package carotationstore persists the §10.3 CA-rotation state machine
// so an operator-driven rotation survives a gateway restart and resumes
// from the recorded stage (spec line 347: "Each stage is durable: an
// operator who interrupts the procedure ... resumes from the recorded
// stage rather than restarting"). The rotation is a platform-global
// procedure, so the store holds a single row; pgstore keys it with a
// constant id and Memory holds one value. The pkg/gateway/carotation
// Manager loads the row, drives pkg/mtls.CARotation, and writes the new
// stage back here under an optimistic version guard.
package carotationstore

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrConflict is returned by Put when the stored row's version does not
// match the expected version, signalling a concurrent rotation mutation
// (a second gateway replica or operator advanced the stage in between).
// The admin handler maps it to HTTP 409 so the operator retries against
// the fresh stage.
var ErrConflict = errors.New("carotationstore: version conflict")

// Record is the persisted §10.3 CA-rotation state. The fields mirror
// mtls.RestoredCARotation plus the optimistic-concurrency Version and
// the UpdatedAt audit timestamp.
type Record struct {
	// Stage is the §10.3 rotation stage: idle, new_ca_deployed,
	// promoted, or old_ca_retired.
	Stage string

	// CurrentCAID is the rotation's starting CA (mtls.RestoredCARotation
	// OldCAID): the trust anchor present at the idle stage.
	CurrentCAID string

	// NewCAID is the CA introduced by BeginNewCARotation. Empty at the
	// idle stage.
	NewCAID string

	// OverlapStartedAt is the instant the overlap window opened. Zero at
	// the idle stage.
	OverlapStartedAt time.Time

	// OverlapWindowSecs is the rotation's overlap window in seconds. Zero
	// takes mtls.DefaultCARotationOverlap at restore time.
	OverlapWindowSecs int64

	// Version is the optimistic-concurrency tag. Get returns the current
	// version; Put requires the caller to echo it and bumps it on a
	// successful write.
	Version int64

	// UpdatedAt is the instant the row was last written.
	UpdatedAt time.Time
}

// Store persists the singleton CA-rotation record.
type Store interface {
	// Get returns the singleton record. ok is false before the first
	// Put (no rotation has been initialized yet).
	Get(ctx context.Context) (rec Record, ok bool, err error)

	// Put writes rec. When a row already exists its stored version must
	// equal expectVersion or Put returns ErrConflict; the very first
	// write (initialization) passes expectVersion 0. The returned Record
	// carries the bumped Version and the write timestamp.
	Put(ctx context.Context, rec Record, expectVersion int64) (Record, error)
}

// Memory is an in-process Store for tests and single-replica or
// memory-backed deployments. It is safe for concurrent use.
type Memory struct {
	mu    sync.Mutex
	row   *Record
	clock func() time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the write-timestamp clock. Tests substitute a
// fake clock so UpdatedAt is deterministic.
func (m *Memory) WithClock(clock func() time.Time) *Memory {
	m.clock = clock
	return m
}

// Get implements Store.
func (m *Memory) Get(_ context.Context) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.row == nil {
		return Record{}, false, nil
	}
	return *m.row, true, nil
}

// Put implements Store with an optimistic version guard.
func (m *Memory) Put(_ context.Context, rec Record, expectVersion int64) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.row != nil {
		if m.row.Version != expectVersion {
			return Record{}, ErrConflict
		}
	} else if expectVersion != 0 {
		return Record{}, ErrConflict
	}
	rec.Version = expectVersion + 1
	rec.UpdatedAt = m.clock()
	stored := rec
	m.row = &stored
	return stored, nil
}

// Compile-time check.
var _ Store = (*Memory)(nil)
