// SPDX-License-Identifier: MIT

// Package carotation drives the §10.3 CA-rotation state machine on top
// of a durable carotationstore. It is the operator-facing orchestrator
// the pkg/mtls.CARotation tracker always wanted: the admin API and
// lenny-ctl call Begin / Promote / Retire here, the Manager loads the
// recorded stage, replays the linear transition through pkg/mtls,
// persists the new stage under an optimistic version guard, and emits
// the `platform.ca_rotated` audit row (via the supplied
// mtls.CARotationObserver) only after the durable write commits.
//
// Before this package the rotation state machine had no production
// caller: a real CA rotation ran with no audit, no overlap-window
// enforcement, and no record of which CA signed versus which CAs were
// trusted (BUILD-GAPS F-10.3.21).
package carotation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/carotationstore"
	"github.com/lennylabs/lenny/pkg/mtls"
)

// ErrNotInitialized is returned by the mutating transitions when no
// rotation row exists yet. The gateway seeds the idle row at startup via
// EnsureInitialized; a deployment with mTLS disabled never seeds one, so
// the admin handler maps this to HTTP 409.
var ErrNotInitialized = errors.New("carotation: rotation not initialized")

// Manager orchestrates the durable §10.3 CA-rotation procedure. It is
// safe for concurrent use: an in-process mutex serializes a replica's
// own transitions and the store's optimistic version guard serializes
// transitions across replicas.
type Manager struct {
	store    carotationstore.Store
	observer mtls.CARotationObserver
	overlap  time.Duration
	now      func() time.Time
	mu       sync.Mutex
}

// Option configures a Manager.
type Option func(*Manager)

// WithObserver supplies the mtls.CARotationObserver that commits the
// `platform.ca_rotated` audit row per stage transition. The Manager
// fires it only after the durable store write commits, so an audit row
// never describes a transition that failed to persist. Nil disables
// audit emission.
func WithObserver(o mtls.CARotationObserver) Option {
	return func(m *Manager) { m.observer = o }
}

// WithOverlapWindow overrides the §10.3 overlap window applied to a new
// rotation (the minimum time the old and new CA coexist in the trust
// bundle before RetireOldCA may run). A value <= 0 takes
// mtls.DefaultCARotationOverlap.
func WithOverlapWindow(d time.Duration) Option {
	return func(m *Manager) { m.overlap = d }
}

// WithClock overrides the wall clock. Tests substitute a fake clock so
// the overlap window can be traversed deterministically.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// NewManager returns a Manager over store.
func NewManager(store carotationstore.Store, opts ...Option) *Manager {
	m := &Manager{store: store, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// EnsureInitialized seeds the idle singleton row from currentCAID when
// no rotation has been recorded yet. It is idempotent: an existing row
// is left untouched (including one mid-rotation after a restart). The
// gateway calls it at startup so the operator API always reaches a real
// rotation. A concurrent initializer that loses the insert race is
// treated as success.
func (m *Manager) EnsureInitialized(ctx context.Context, currentCAID string) error {
	if currentCAID == "" {
		return &mtls.RotationError{Kind: "invalid_argument", Detail: "currentCAID is empty"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok, err := m.store.Get(ctx)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = m.store.Put(ctx, carotationstore.Record{
		Stage:             string(mtls.CAStageIdle),
		CurrentCAID:       currentCAID,
		OverlapWindowSecs: int64(m.overlapWindow() / time.Second),
	}, 0)
	if errors.Is(err, carotationstore.ErrConflict) {
		return nil // a peer initialized concurrently
	}
	return err
}

// Status returns a snapshot of the current rotation. ok is false before
// EnsureInitialized has seeded the row.
func (m *Manager) Status(ctx context.Context) (snap mtls.CARotationSnapshot, ok bool, err error) {
	rec, ok, err := m.store.Get(ctx)
	if err != nil || !ok {
		return mtls.CARotationSnapshot{}, ok, err
	}
	rot, err := m.restore(rec)
	if err != nil {
		return mtls.CARotationSnapshot{}, true, err
	}
	return rot.Snapshot(), true, nil
}

// Begin advances the rotation from idle to new_ca_deployed, introducing
// newCAID into the trust bundle and opening the overlap window. It
// returns the post-transition snapshot.
func (m *Manager) Begin(ctx context.Context, newCAID string) (mtls.CARotationSnapshot, error) {
	return m.transition(ctx, func(r *mtls.CARotation) error {
		return r.BeginNewCARotation(newCAID)
	})
}

// Promote advances the rotation from new_ca_deployed to promoted,
// swapping the issuer role to the new CA. Both CAs stay trusted.
func (m *Manager) Promote(ctx context.Context) (mtls.CARotationSnapshot, error) {
	return m.transition(ctx, func(r *mtls.CARotation) error {
		return r.PromoteNewCA()
	})
}

// Retire advances the rotation from promoted to old_ca_retired, dropping
// the old CA from the trust bundle. It returns mtls.IsOverlapOpen-true
// error when the overlap window has not yet closed.
func (m *Manager) Retire(ctx context.Context) (mtls.CARotationSnapshot, error) {
	return m.transition(ctx, func(r *mtls.CARotation) error {
		return r.RetireOldCA()
	})
}

// transition loads the recorded stage, applies apply through the linear
// pkg/mtls state machine, persists the result under the optimistic
// version guard, and (on a durable commit) emits the audit event. The
// rotation is reconstructed without an observer so the audit row is
// fired by the Manager after the store write, never before.
func (m *Manager) transition(ctx context.Context, apply func(*mtls.CARotation) error) (mtls.CARotationSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.Get(ctx)
	if err != nil {
		return mtls.CARotationSnapshot{}, err
	}
	if !ok {
		return mtls.CARotationSnapshot{}, ErrNotInitialized
	}
	rot, err := m.restore(rec)
	if err != nil {
		return mtls.CARotationSnapshot{}, err
	}
	prev := rot.Snapshot().Stage
	if err := apply(rot); err != nil {
		return mtls.CARotationSnapshot{}, err
	}
	snap := rot.Snapshot()
	if _, err := m.store.Put(ctx, carotationstore.Record{
		Stage:             string(snap.Stage),
		CurrentCAID:       rot.OldCAID(),
		NewCAID:           rot.NewCAID(),
		OverlapStartedAt:  snap.OverlapStartedAt,
		OverlapWindowSecs: rec.OverlapWindowSecs,
	}, rec.Version); err != nil {
		return mtls.CARotationSnapshot{}, err
	}
	if m.observer != nil {
		m.observer.OnCARotation(mtls.CARotationEvent{
			From:         prev,
			To:           snap.Stage,
			CurrentCAID:  snap.CurrentCAID,
			TrustedCAIDs: snap.TrustedCAIDs,
			At:           m.now(),
		})
	}
	return snap, nil
}

// restore rebuilds the linear state machine from a persisted record.
func (m *Manager) restore(rec carotationstore.Record) (*mtls.CARotation, error) {
	window := time.Duration(rec.OverlapWindowSecs) * time.Second
	if window <= 0 {
		window = m.overlapWindow()
	}
	return mtls.RestoreCARotation(mtls.RestoredCARotation{
		Stage:            mtls.CARotationStage(rec.Stage),
		OldCAID:          rec.CurrentCAID,
		NewCAID:          rec.NewCAID,
		OverlapStartedAt: rec.OverlapStartedAt,
	}, mtls.CARotationOptions{OverlapWindow: window, Now: m.now})
}

// overlapWindow returns the configured overlap, defaulting to the §10.3
// 24h window.
func (m *Manager) overlapWindow() time.Duration {
	if m.overlap > 0 {
		return m.overlap
	}
	return mtls.DefaultCARotationOverlap
}
