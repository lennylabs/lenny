// SPDX-License-Identifier: MIT

// Package runtimeupgrade drives the §10.5 RuntimeUpgrade state machine on
// top of a durable runtimeupgradestore. It is the operator-facing
// orchestrator the pkg/runtime/upgrade/state machine always wanted: the
// admin API and lenny-ctl call Start / Proceed / Pause / Resume /
// Rollback / Status here, the Manager loads the recorded phase for a
// pool, replays the linear transition through pkg/runtime/upgrade/state,
// persists the new phase under an optimistic version guard, and emits the
// lenny_runtime_upgrade_state family of gauges only after the durable
// write commits.
//
// Before this package the RuntimeUpgrade state machine had no production
// caller: a runtime image rollout had no tracked phase, no pause/rollback,
// no canary knob, and no durable previous_pool_spec for recovery
// (BUILD-GAPS F-10.5.1). This package delivers the operator surface
// (durable store, admin endpoints, CLI, metric emission). The Kubernetes
// reconcile side (RuntimeUpgrade CRD, controller, WarmPoolController
// SandboxTemplate-deletion guard) drives the cluster effects of each
// phase and remains a follow-on.
package runtimeupgrade

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradestore"
	"github.com/lennylabs/lenny/pkg/runtime/upgrade/state"
)

// Sentinel errors. The admin handler maps each to an HTTP status.
var (
	// ErrPoolNotFound is returned by Start when the target pool does not
	// exist (mapped to 404).
	ErrPoolNotFound = errors.New("runtimeupgrade: pool not found")

	// ErrUpgradeNotFound is returned by Proceed/Pause/Resume/Rollback when
	// no upgrade has been registered for the pool (mapped to 404).
	ErrUpgradeNotFound = errors.New("runtimeupgrade: no active upgrade for pool")

	// ErrUpgradeActive is returned by Start when a non-terminal upgrade
	// already exists for the pool (mapped to 409). The operator must
	// complete, pause, or roll back the in-flight upgrade first.
	ErrUpgradeActive = errors.New("runtimeupgrade: an upgrade is already in progress for this pool")

	// ErrInvalidImage is returned by Start with an empty newImage (400).
	ErrInvalidImage = errors.New("runtimeupgrade: newImage is required")

	// ErrTerminal is returned by Proceed when the upgrade is Complete
	// (409): no further transitions are legal.
	ErrTerminal = errors.New("runtimeupgrade: upgrade is complete; no further transitions")

	// ErrPaused is returned by Proceed when the upgrade is Paused (409):
	// the operator must resume before proceeding.
	ErrPaused = errors.New("runtimeupgrade: upgrade is paused; resume before proceeding")

	// ErrRollbackNotAllowed is returned by Rollback when the current phase
	// does not permit rollback, or when a restore-old-pool rollback is
	// requested but the previous pool spec is no longer preserved (409).
	ErrRollbackNotAllowed = errors.New("runtimeupgrade: rollback is not allowed from the current phase")
)

// defaultStabilizationWindowSeconds is the §10.5 line 481 default dwell.
const defaultStabilizationWindowSeconds = 120

// PoolReader resolves the current configuration of a pool so Start can
// confirm the pool exists and capture its spec as previousPoolSpec for
// rollback (§10.5 line 507).
type PoolReader interface {
	// PoolSpec returns the JSON-serialized current configuration of pool
	// and whether the pool exists.
	PoolSpec(ctx context.Context, pool string) (spec []byte, ok bool, err error)
}

// MetricsEmitter publishes the §16.1 runtime-upgrade gauges. The Manager
// calls it after each durable phase write so the gauge never describes a
// transition that failed to persist. gatewaymetrics.Metrics satisfies it;
// a nil emitter disables emission.
type MetricsEmitter interface {
	SetRuntimeUpgradeState(pool, phase string)
	SetRuntimeUpgradePhaseDuration(pool, phase string, seconds float64)
	SetRuntimeUpgradeDrainingSessions(pool string, n int)
}

// Manager orchestrates the durable §10.5 RuntimeUpgrade procedure for all
// pools. It is safe for concurrent use: an in-process mutex serializes a
// replica's own transitions and the store's optimistic version guard
// serializes transitions across replicas.
type Manager struct {
	store   runtimeupgradestore.Store
	pools   PoolReader
	metrics MetricsEmitter
	now     func() time.Time
	mu      sync.Mutex
}

// Option configures a Manager.
type Option func(*Manager)

// WithPoolReader wires the pool-existence check and previousPoolSpec
// capture. Without it, Start neither confirms the pool exists nor
// captures its spec.
func WithPoolReader(p PoolReader) Option { return func(m *Manager) { m.pools = p } }

// WithMetrics wires the §16.1 gauge emitter.
func WithMetrics(e MetricsEmitter) Option { return func(m *Manager) { m.metrics = e } }

// WithClock overrides the wall clock. Tests substitute a fake clock so
// phase-duration and pause timestamps are deterministic.
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// NewManager returns a Manager over store.
func NewManager(store runtimeupgradestore.Store, opts ...Option) *Manager {
	m := &Manager{store: store, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// StartOptions carries the §10.5 per-upgrade knobs.
type StartOptions struct {
	NewImage                   string
	CanaryPercent              int
	SchemaVersion              string
	DrainFirst                 bool
	AutoAdvance                bool
	StabilizationWindowSeconds int64
	DrainTimeoutSeconds        int64
}

// Snapshot is the wire-and-metric view of a pool's upgrade record.
type Snapshot struct {
	Pool                       string
	Phase                      string
	PriorPhase                 string
	NewImage                   string
	SchemaVersion              string
	DrainFirst                 bool
	AutoAdvance                bool
	CanaryPercent              int
	StabilizationWindowSeconds int64
	DrainTimeoutSeconds        int64
	PauseReason                string
	PausedAt                   time.Time
	PhaseEnteredAt             time.Time
	PhaseDurationSeconds       float64
	DrainingSessions           int
	HasPreviousPoolSpec        bool
	Version                    int64
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

// Start registers a new upgrade for pool at the Pending phase, capturing
// the pool's current spec as previousPoolSpec for rollback. It rejects a
// pool with a non-terminal upgrade already in flight (ErrUpgradeActive)
// and an empty image (ErrInvalidImage). A prior Complete upgrade for the
// pool is overwritten by the new registration.
func (m *Manager) Start(ctx context.Context, pool string, opts StartOptions) (Snapshot, error) {
	if opts.NewImage == "" {
		return Snapshot{}, ErrInvalidImage
	}
	if opts.CanaryPercent < 0 || opts.CanaryPercent > 100 {
		return Snapshot{}, errors.New("runtimeupgrade: canaryPercent must be between 0 and 100")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var spec []byte
	if m.pools != nil {
		s, ok, err := m.pools.PoolSpec(ctx, pool)
		if err != nil {
			return Snapshot{}, err
		}
		if !ok {
			return Snapshot{}, ErrPoolNotFound
		}
		spec = s
	}

	existing, ok, err := m.store.Get(ctx, pool)
	if err != nil {
		return Snapshot{}, err
	}
	expectVersion := int64(0)
	if ok {
		if existing.Phase != string(state.Complete) {
			return Snapshot{}, ErrUpgradeActive
		}
		expectVersion = existing.Version
	}

	window := opts.StabilizationWindowSeconds
	if window <= 0 {
		window = defaultStabilizationWindowSeconds
	}
	now := m.now()
	rec := runtimeupgradestore.Record{
		Pool:                       pool,
		Phase:                      string(state.Pending),
		NewImage:                   opts.NewImage,
		PreviousPoolSpec:           spec,
		SchemaVersion:              opts.SchemaVersion,
		DrainFirst:                 opts.DrainFirst,
		AutoAdvance:                opts.AutoAdvance,
		CanaryPercent:              opts.CanaryPercent,
		StabilizationWindowSeconds: window,
		DrainTimeoutSeconds:        opts.DrainTimeoutSeconds,
		PhaseEnteredAt:             now,
	}
	stored, err := m.store.Put(ctx, rec, expectVersion)
	if err != nil {
		return Snapshot{}, err
	}
	return m.commit(stored), nil
}

// Proceed advances the upgrade one phase along the linear progression
// (Pending → Expanding → Draining → Contracting → Complete). It rejects a
// paused or complete upgrade.
func (m *Manager) Proceed(ctx context.Context, pool string) (Snapshot, error) {
	return m.mutate(ctx, pool, func(rec *runtimeupgradestore.Record) error {
		cur := state.State(rec.Phase)
		if cur == state.Paused {
			return ErrPaused
		}
		if cur == state.Complete {
			return ErrTerminal
		}
		next, ok := nextLinear(cur)
		if !ok {
			return ErrTerminal
		}
		if err := state.IsValid(cur, next); err != nil {
			return err
		}
		rec.Phase = string(next)
		rec.PhaseEnteredAt = m.now()
		if next != state.Draining {
			rec.DrainingSessions = 0
		}
		return nil
	})
}

// Pause halts the upgrade, capturing the current phase so Resume restores
// it. The reason and timestamp are stored on the record (§10.5 line 494).
func (m *Manager) Pause(ctx context.Context, pool, reason string) (Snapshot, error) {
	return m.mutate(ctx, pool, func(rec *runtimeupgradestore.Record) error {
		return m.pause(rec, reason)
	})
}

// Resume returns the upgrade to the phase captured at pause.
func (m *Manager) Resume(ctx context.Context, pool string) (Snapshot, error) {
	return m.mutate(ctx, pool, func(rec *runtimeupgradestore.Record) error {
		if state.State(rec.Phase) != state.Paused {
			return state.ErrNotPaused
		}
		rec.Phase = rec.PriorPhase
		rec.PriorPhase = ""
		rec.PauseReason = ""
		rec.PausedAt = time.Time{}
		rec.PhaseEnteredAt = m.now()
		return nil
	})
}

// Rollback halts a broken upgrade. From Expanding it always succeeds. From
// Draining or Contracting it requires restoreOldPool and a preserved
// previousPoolSpec. The upgrade transitions to Paused with a rollback
// reason (§10.5 lines 506-507); the operator then re-runs Start with a
// corrected image. The pool side effects (minWarm reset, routing
// restoration, recreate from previousPoolSpec) are the controller's job.
func (m *Manager) Rollback(ctx context.Context, pool string, restoreOldPool bool) (Snapshot, error) {
	return m.mutate(ctx, pool, func(rec *runtimeupgradestore.Record) error {
		cur := state.State(rec.Phase)
		reason := "rollback"
		switch cur {
		case state.Expanding:
			// Always allowed: set new pool minWarm 0, restore routing.
		case state.Draining, state.Contracting:
			if !restoreOldPool {
				return ErrRollbackNotAllowed
			}
			if len(rec.PreviousPoolSpec) == 0 {
				return ErrRollbackNotAllowed
			}
			reason = "rollback (restore-old-pool)"
		default:
			return ErrRollbackNotAllowed
		}
		return m.pause(rec, reason)
	})
}

// Status returns the current snapshot for pool. ok is false when no
// upgrade has been registered.
func (m *Manager) Status(ctx context.Context, pool string) (Snapshot, bool, error) {
	rec, ok, err := m.store.Get(ctx, pool)
	if err != nil || !ok {
		return Snapshot{}, ok, err
	}
	return m.snapshot(rec), true, nil
}

// EmitAll publishes the gauge family for every recorded upgrade. The
// gateway calls it at startup so the §16.5 RuntimeUpgradeStuck alert
// evaluates against the durable phase after a restart.
func (m *Manager) EmitAll(ctx context.Context) error {
	if m.metrics == nil {
		return nil
	}
	recs, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		m.emit(m.snapshot(rec))
	}
	return nil
}

// pause captures the current non-terminal phase and moves to Paused.
func (m *Manager) pause(rec *runtimeupgradestore.Record, reason string) error {
	cur := state.State(rec.Phase)
	if cur == state.Paused {
		return state.ErrAlreadyPaused
	}
	if cur == state.Complete {
		return state.ErrCannotPauseTerminal
	}
	rec.PriorPhase = rec.Phase
	rec.Phase = string(state.Paused)
	rec.PauseReason = reason
	rec.PausedAt = m.now()
	return nil
}

// mutate loads the record, applies fn, persists under the optimistic
// version guard, and emits the gauge family on a durable commit.
func (m *Manager) mutate(ctx context.Context, pool string, fn func(*runtimeupgradestore.Record) error) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.Get(ctx, pool)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		return Snapshot{}, ErrUpgradeNotFound
	}
	expectVersion := rec.Version
	if err := fn(&rec); err != nil {
		return Snapshot{}, err
	}
	stored, err := m.store.Put(ctx, rec, expectVersion)
	if err != nil {
		return Snapshot{}, err
	}
	return m.commit(stored), nil
}

// commit emits the gauge family and returns the snapshot.
func (m *Manager) commit(rec runtimeupgradestore.Record) Snapshot {
	snap := m.snapshot(rec)
	m.emit(snap)
	return snap
}

func (m *Manager) emit(snap Snapshot) {
	if m.metrics == nil {
		return
	}
	m.metrics.SetRuntimeUpgradeState(snap.Pool, snap.Phase)
	m.metrics.SetRuntimeUpgradePhaseDuration(snap.Pool, snap.Phase, snap.PhaseDurationSeconds)
	m.metrics.SetRuntimeUpgradeDrainingSessions(snap.Pool, snap.DrainingSessions)
}

func (m *Manager) snapshot(rec runtimeupgradestore.Record) Snapshot {
	var dur float64
	if !rec.PhaseEnteredAt.IsZero() {
		dur = m.now().Sub(rec.PhaseEnteredAt).Seconds()
		if dur < 0 {
			dur = 0
		}
	}
	return Snapshot{
		Pool:                       rec.Pool,
		Phase:                      rec.Phase,
		PriorPhase:                 rec.PriorPhase,
		NewImage:                   rec.NewImage,
		SchemaVersion:              rec.SchemaVersion,
		DrainFirst:                 rec.DrainFirst,
		AutoAdvance:                rec.AutoAdvance,
		CanaryPercent:              rec.CanaryPercent,
		StabilizationWindowSeconds: rec.StabilizationWindowSeconds,
		DrainTimeoutSeconds:        rec.DrainTimeoutSeconds,
		PauseReason:                rec.PauseReason,
		PausedAt:                   rec.PausedAt,
		PhaseEnteredAt:             rec.PhaseEnteredAt,
		PhaseDurationSeconds:       dur,
		DrainingSessions:           rec.DrainingSessions,
		HasPreviousPoolSpec:        len(rec.PreviousPoolSpec) > 0,
		Version:                    rec.Version,
		CreatedAt:                  rec.CreatedAt,
		UpdatedAt:                  rec.UpdatedAt,
	}
}

// nextLinear returns the next phase in the linear progression and whether
// one exists. Complete and Paused have no linear successor.
func nextLinear(s state.State) (state.State, bool) {
	switch s {
	case state.Pending:
		return state.Expanding, true
	case state.Expanding:
		return state.Draining, true
	case state.Draining:
		return state.Contracting, true
	case state.Contracting:
		return state.Complete, true
	}
	return "", false
}
