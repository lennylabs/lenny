// SPDX-License-Identifier: MIT

// Package upgradeservice implements the §25.8 platform-upgrade
// orchestrator: the lenny-ops consumer that drives the §25.8 / §10.5
// phase state machine (pkg/upgrade) through its
// Preflight→OpsRoll→CRDUpdate→SchemaMigration→GatewayRoll→ControllerRoll
// →Verification→Complete progression, holds the singleton
// platform_upgrade_state, and emits the §16.7 platform-upgrade
// lifecycle audit events on every transition.
//
// The orchestrator is operator-paced, matching the §25.8 model: each
// POST /v1/admin/platform/upgrade/proceed advances exactly one phase,
// the §25.4 Operations Inventory reports the upgrade as `paused`
// (awaiting the operator's next proceed), and the actual cluster
// mutations (helm upgrade, kubectl apply CRDs, lenny-migrate) are
// performed by the operator or DevOps agent between calls. The
// orchestrator owns coordination and the audit trail, so an agent can
// reconstruct exactly which phase ran, when, and by whom.
//
// spec: §25.8 (Upgrade orchestration endpoints), §10.5 (platform
// upgrade procedure), §16.7 (platform.upgrade_* audit events).
package upgradeservice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// PlatformScope is the §25.8 upgrade scope identifier for a
// platform-wide upgrade. It is the `pool` value the pkg/upgrade phase
// machine stamps onto every upgrade_progressed event and the scope of
// the `upgrade:platform` remediation lock (§25.4 line 2120).
const PlatformScope = "platform"

// Error codes for the §25.8 upgrade-orchestration envelope. The
// opsserver handler maps each to an HTTP status. The canonical §25.8
// error-code table (spec line 3629) is declared in full in codes.go;
// the constants below are the subset the orchestrator state machine
// returns, named to match the spec table verbatim.
const (
	// CodeUpgradeInProgress rejects a second start while an upgrade is
	// active (§25.8: the platform_upgrade_state singleton holds one
	// upgrade at a time). spec: §25.8 UPGRADE_ALREADY_IN_PROGRESS.
	CodeUpgradeInProgress = CodeUpgradeAlreadyInProgress
	// CodeNoUpgrade reports that no upgrade is active for a
	// proceed/pause/rollback/verify/status call. spec: §25.8
	// UPGRADE_NOT_IN_PROGRESS.
	CodeNoUpgrade = CodeUpgradeNotInProgress
	// CodeUpgradeTerminal rejects an advance on a Complete or RolledBack
	// upgrade. This is an orchestrator extension to the §25.8 table; a
	// terminal upgrade is neither "in progress" nor rollbackable.
	CodeUpgradeTerminal = "UPGRADE_TERMINAL"
	// CodeNotRollbackable rejects a rollback past the §25.8 point of no
	// return (SchemaMigration onward). spec: §25.8
	// UPGRADE_ROLLBACK_UNAVAILABLE.
	CodeNotRollbackable = CodeUpgradeRollbackUnavailable
	// CodeNotVerifiable rejects a verify call outside the Verification
	// phase. Orchestrator extension to the §25.8 table.
	CodeNotVerifiable = "UPGRADE_NOT_AT_VERIFICATION"
	// CodeUnavailable reports the orchestrator is not configured (no
	// store), the lenny-ops cold-start posture. Orchestrator extension.
	CodeUnavailable = "UPGRADE_SERVICE_UNAVAILABLE"
)

// Sentinel errors the Service returns; the handler classifies them into
// the codes above.
var (
	// ErrUpgradeInProgress is returned by Start when an upgrade is active.
	ErrUpgradeInProgress = errors.New("upgradeservice: an upgrade is already in progress")
	// ErrNoUpgrade is returned when no upgrade exists for the operation.
	ErrNoUpgrade = errors.New("upgradeservice: no active upgrade")
	// ErrUpgradeTerminal is returned by Proceed on a terminal upgrade.
	ErrUpgradeTerminal = errors.New("upgradeservice: upgrade has reached a terminal phase")
	// ErrNotRollbackable is returned by Rollback past the point of no return.
	ErrNotRollbackable = errors.New("upgradeservice: upgrade can no longer roll back")
	// ErrNotVerifiable is returned by Verify outside the Verification phase.
	ErrNotVerifiable = errors.New("upgradeservice: upgrade is not at the Verification phase")
)

// State is the §25.8 platform_upgrade_state singleton: the live record
// of the in-flight (or last completed) platform upgrade.
type State struct {
	// OperationID is the §25.4 Operations Inventory identifier
	// (`upgrade-<uuid>`). It correlates the upgrade with its audit
	// events (GET /v1/admin/audit-events?operationId=...).
	OperationID string `json:"operationId"`
	// Phase is the current §25.8 phase.
	Phase upgrade.Phase `json:"phase"`
	// TargetVersion is the version the upgrade is converging on.
	TargetVersion string `json:"targetVersion"`
	// ImageDigest is the target image digest stamped onto every
	// upgrade_progressed event per §4.0.
	ImageDigest string `json:"imageDigest,omitempty"`
	// StartedBy is the operator/agent identity that started the upgrade.
	StartedBy string `json:"startedBy"`
	// StartedAt is the upgrade start time.
	StartedAt time.Time `json:"startedAt"`
	// UpdatedAt is the time of the last phase transition.
	UpdatedAt time.Time `json:"updatedAt"`
	// Paused reports whether the upgrade is awaiting an explicit proceed
	// (§25.4 Operations Inventory `paused` status).
	Paused bool `json:"paused"`
	// Verified records that the operator ran POST .../verify at the
	// Verification phase. The final proceed (Verification→Complete) does
	// not require it, but the flag surfaces in status for the agent.
	Verified bool `json:"verified"`
	// Reason carries the operator justification for a pause or rollback.
	Reason string `json:"reason,omitempty"`
}

// Active reports whether the upgrade is still in flight (not terminal).
func (s State) Active() bool { return !upgrade.IsTerminal(s.Phase) }

// Progress returns the §25.8 line 357 uniform progress object for the
// upgrade: the 1-based step, the total step count, and a human-readable
// detail. A terminal upgrade reports the full step count.
func (s State) Progress() map[string]any {
	step, ok := upgrade.StepNumber(s.Phase)
	if !ok {
		step = upgrade.TotalSteps
	}
	detail := fmt.Sprintf("phase %s", s.Phase)
	switch {
	case s.Phase == upgrade.Complete:
		detail = "upgrade complete"
	case s.Phase == upgrade.RolledBack:
		detail = "upgrade rolled back"
	case s.Paused:
		detail = "Waiting for operator to call /upgrade/proceed"
	}
	return map[string]any{
		"currentStep":       step,
		"totalSteps":        upgrade.TotalSteps,
		"currentStepDetail": detail,
	}
}

// Store persists the §25.8 platform_upgrade_state singleton. lenny-ops
// supplies a Postgres-backed store in production; MemoryStore is the
// single-process / test implementation. The store holds at most one
// record: a new upgrade overwrites the prior terminal one.
type Store interface {
	// Load returns the current upgrade state. ok is false when no upgrade
	// has ever been recorded.
	Load(ctx context.Context) (state State, ok bool, err error)
	// Save persists state, replacing any prior record.
	Save(ctx context.Context, state State) error
}

// MemoryStore is the in-process Store. It is safe for concurrent use.
type MemoryStore struct {
	mu    sync.Mutex
	state *State
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Load returns the stored state.
func (m *MemoryStore) Load(context.Context) (State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return State{}, false, nil
	}
	return *m.state, true, nil
}

// Save records state.
func (m *MemoryStore) Save(_ context.Context, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := state
	m.state = &cp
	return nil
}

// AuditEvent is one §16.7 platform-upgrade lifecycle audit event the
// Service emits on a transition. The Service hands each to the
// caller-supplied AuditSink so the audit trail integrates with the
// platform's §11.7 append-only log without coupling orchestration to an
// audit-store dependency, mirroring pkg/ops/backup's AuditSink seam.
//
// spec: §16.7 (platform.upgrade_* events), §10.5.
type AuditEvent struct {
	// Type is the audit.EventType string, e.g.
	// audit.EventPlatformUpgradeStarted.
	Type string
	// OperationID correlates the event with the platform_upgrade_state
	// row (the §25.4 `upgrade-<uuid>` operation id).
	OperationID string
	// Actor is the operator/agent identity recorded on the event.
	Actor string
	// OldPhase and NewPhase bracket the transition (empty for the start
	// event, which has no prior phase).
	OldPhase string
	// NewPhase is the phase reached.
	NewPhase string
	// TargetVersion is the version the upgrade converges on.
	TargetVersion string
	// Detail carries a failure reason or operator justification.
	Detail string
	// At is the event time; the Service stamps it from its clock when the
	// caller leaves it zero.
	At time.Time
}

// AuditSink receives §16.7 platform-upgrade audit events. A nil sink
// drops them, the cold-start posture for a deployment whose
// audit-append path is not yet wired (the events still fire and are
// observable in tests; only the durable destination is absent),
// matching pkg/ops/backup and pkg/ops/driftservice.
type AuditSink func(AuditEvent)

// DriftCleaner deletes the §25.10 target snapshot when a §25.8 upgrade
// rolls back (spec line 3551). The driftservice.Service satisfies it.
// A nil cleaner skips the cleanup, the cold-start posture for a
// deployment whose drift service is not wired.
type DriftCleaner interface {
	// DeleteTargetSnapshot removes the target snapshot written for the
	// given upgrade id. It returns whether a row was deleted.
	DeleteTargetSnapshot(ctx context.Context, upgradeID string) (bool, error)
}

// Service is the §25.8 platform-upgrade orchestrator.
type Service struct {
	store   Store
	emitter upgrade.Emitter // §16.6 upgrade_progressed operational events; nil is a no-op
	audit   AuditSink       // §16.7 audit events; nil drops
	drift   DriftCleaner    // §25.10 target-snapshot cleanup on rollback; nil skips
	now     func() time.Time
	newID   func() string

	mu sync.Mutex // serializes the read-modify-write of the singleton
}

// Options configures a Service.
type Options struct {
	// Store persists the singleton state. Required.
	Store Store
	// Emitter publishes §16.6 upgrade_progressed operational events. A nil
	// emitter is a no-op (the phase still advances and audit still fires).
	Emitter upgrade.Emitter
	// Audit receives §16.7 platform-upgrade audit events. A nil sink drops.
	Audit AuditSink
	// DriftCleaner deletes the §25.10 target snapshot on rollback (§25.8
	// line 3551). A nil cleaner skips the cleanup.
	DriftCleaner DriftCleaner
	// Now supplies the current time; nil defaults to time.Now.
	Now func() time.Time
	// NewID mints the operation id; nil defaults to `upgrade-<uuid>`.
	NewID func() string
}

// New returns a Service over opts. It panics when Store is nil, which is
// a wiring error rather than a runtime condition.
func New(opts Options) *Service {
	if opts.Store == nil {
		panic("upgradeservice: Options.Store is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	newID := opts.NewID
	if newID == nil {
		newID = func() string { return "upgrade-" + uuid.NewString() }
	}
	return &Service{store: opts.Store, emitter: opts.Emitter, audit: opts.Audit, drift: opts.DriftCleaner, now: now, newID: newID}
}

// StartRequest is the POST /v1/admin/platform/upgrade/start body.
type StartRequest struct {
	// TargetVersion is the version to upgrade to. Required.
	TargetVersion string `json:"version"`
	// ImageDigest is the target image digest for the upgrade_progressed
	// payload. Optional (resolved by digest only when requireDigest).
	ImageDigest string `json:"imageDigest,omitempty"`
	// StartedBy is the operator/agent identity; the handler fills it from
	// the verified principal.
	StartedBy string `json:"-"`
}

// Start begins a platform upgrade at the Preflight phase. It rejects a
// second start while an upgrade is active (ErrUpgradeInProgress) and
// emits platform.upgrade_started.
//
// spec: §25.8 POST /v1/admin/platform/upgrade/start.
func (s *Service) Start(ctx context.Context, req StartRequest) (State, error) {
	if req.TargetVersion == "" {
		return State{}, fmt.Errorf("upgradeservice: a target version is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok, err := s.store.Load(ctx); err != nil {
		return State{}, err
	} else if ok && prev.Active() {
		return State{}, ErrUpgradeInProgress
	}
	now := s.now()
	st := State{
		OperationID:   s.newID(),
		Phase:         upgrade.Preflight,
		TargetVersion: req.TargetVersion,
		ImageDigest:   req.ImageDigest,
		StartedBy:     req.StartedBy,
		StartedAt:     now,
		UpdatedAt:     now,
		Paused:        true, // awaiting the first proceed
	}
	if err := s.store.Save(ctx, st); err != nil {
		return State{}, err
	}
	s.emitAudit(AuditEvent{
		Type:          string(audit.EventPlatformUpgradeStarted),
		OperationID:   st.OperationID,
		Actor:         st.StartedBy,
		NewPhase:      string(upgrade.Preflight),
		TargetVersion: st.TargetVersion,
	})
	return st, nil
}

// phaseExitAudit maps the phase an upgrade is leaving to the §16.7
// audit event recorded on that transition. The events are past-tense:
// leaving OpsRoll records that the ops Deployment was rolled, and so on.
// Leaving Preflight has no dedicated "preflight passed" event, so it
// records the generic phase-advanced event; leaving Verification reaches
// Complete and records upgrade_completed.
var phaseExitAudit = map[upgrade.Phase]audit.EventType{
	upgrade.Preflight:       audit.EventPlatformUpgradePhaseAdvanced,
	upgrade.OpsRoll:         audit.EventPlatformUpgradeOpsRolled,
	upgrade.CRDUpdate:       audit.EventPlatformUpgradeCrdsUpdated,
	upgrade.SchemaMigration: audit.EventPlatformUpgradeSchemaMigrated,
	upgrade.GatewayRoll:     audit.EventPlatformUpgradeGatewayRolled,
	upgrade.ControllerRoll:  audit.EventPlatformUpgradeControllersRolled,
	upgrade.Verification:    audit.EventPlatformUpgradeCompleted,
}

// Proceed advances the active upgrade by one §25.8 phase. It emits the
// §16.6 upgrade_progressed operational event (via pkg/upgrade) and the
// §16.7 audit event for the phase that was just completed, then clears
// the paused flag's awaiting state for the new phase.
//
// spec: §25.8 POST /v1/admin/platform/upgrade/proceed.
func (s *Service) Proceed(ctx context.Context) (State, error) {
	return s.transition(ctx, func(st *State) error {
		if upgrade.IsTerminal(st.Phase) {
			return ErrUpgradeTerminal
		}
		exiting := st.Phase
		next, err := upgrade.Advance(ctx, s.emitter, PlatformScope, st.Phase, st.ImageDigest)
		if err != nil {
			return err
		}
		s.emitAudit(AuditEvent{
			Type:          string(phaseExitAudit[exiting]),
			OperationID:   st.OperationID,
			Actor:         st.StartedBy,
			OldPhase:      string(exiting),
			NewPhase:      string(next),
			TargetVersion: st.TargetVersion,
		})
		st.Phase = next
		// A non-terminal phase pauses again, awaiting the next proceed;
		// the terminal Complete is not paused.
		st.Paused = !upgrade.IsTerminal(next)
		return nil
	})
}

// Pause marks the active upgrade as awaiting an explicit proceed and
// emits platform.upgrade_paused. It is a no-op on a terminal upgrade
// (ErrUpgradeTerminal) and records the operator justification.
//
// spec: §25.8 POST /v1/admin/platform/upgrade/pause.
func (s *Service) Pause(ctx context.Context, reason string) (State, error) {
	return s.transition(ctx, func(st *State) error {
		if upgrade.IsTerminal(st.Phase) {
			return ErrUpgradeTerminal
		}
		st.Paused = true
		st.Reason = reason
		s.emitAudit(AuditEvent{
			Type:          string(audit.EventPlatformUpgradePaused),
			OperationID:   st.OperationID,
			Actor:         st.StartedBy,
			NewPhase:      string(st.Phase),
			TargetVersion: st.TargetVersion,
			Detail:        reason,
		})
		return nil
	})
}

// Rollback transitions a rollbackable upgrade to RolledBack and emits
// platform.upgrade_rolled_back plus the §16.6 upgrade_progressed event.
// It rejects a rollback past the §25.8 point of no return
// (ErrNotRollbackable: SchemaMigration onward).
//
// spec: §25.8 POST /v1/admin/platform/upgrade/rollback, §10.5 CanRollBack.
func (s *Service) Rollback(ctx context.Context, reason string) (State, error) {
	st, err := s.transition(ctx, func(st *State) error {
		if upgrade.IsTerminal(st.Phase) {
			return ErrUpgradeTerminal
		}
		if !upgrade.CanRollBack(st.Phase) {
			return ErrNotRollbackable
		}
		from := st.Phase
		next, err := upgrade.AdvanceRollback(ctx, s.emitter, PlatformScope, st.Phase, st.ImageDigest)
		if err != nil {
			return err
		}
		st.Phase = next
		st.Paused = false
		st.Reason = reason
		s.emitAudit(AuditEvent{
			Type:          string(audit.EventPlatformUpgradeRolledBack),
			OperationID:   st.OperationID,
			Actor:         st.StartedBy,
			OldPhase:      string(from),
			NewPhase:      string(next),
			TargetVersion: st.TargetVersion,
			Detail:        reason,
		})
		return nil
	})
	if err != nil {
		return State{}, err
	}
	// §25.8 line 3551: a completed rollback deletes the §25.10 target
	// snapshot for this upgrade. A rollback during Preflight (no target
	// written) deletes zero rows; a cleanup failure is non-fatal — the
	// rollback itself succeeded and the drift reconciler re-checks later.
	if s.drift != nil && st.Phase == upgrade.RolledBack {
		_, _ = s.drift.DeleteTargetSnapshot(ctx, st.OperationID)
	}
	return st, nil
}

// Verify records a successful post-upgrade health verification at the
// Verification phase and emits platform.upgrade_verified. It does not
// change the phase; the final Proceed (Verification→Complete) records
// upgrade_completed. Verify rejects any other phase (ErrNotVerifiable).
//
// spec: §25.8 POST /v1/admin/platform/upgrade/verify.
func (s *Service) Verify(ctx context.Context) (State, error) {
	return s.transition(ctx, func(st *State) error {
		if st.Phase != upgrade.Verification {
			return ErrNotVerifiable
		}
		st.Verified = true
		s.emitAudit(AuditEvent{
			Type:          string(audit.EventPlatformUpgradeVerified),
			OperationID:   st.OperationID,
			Actor:         st.StartedBy,
			NewPhase:      string(st.Phase),
			TargetVersion: st.TargetVersion,
		})
		return nil
	})
}

// Status returns the current upgrade state. ok is false when no upgrade
// has ever been recorded.
//
// spec: §25.8 GET /v1/admin/platform/upgrade/status.
func (s *Service) Status(ctx context.Context) (State, bool, error) {
	return s.store.Load(ctx)
}

// MetricsSnapshot is the point-in-time data the §25.8 upgrade metrics
// collector reports: the current phase encoded as an integer and the
// elapsed time since the upgrade started.
type MetricsSnapshot struct {
	// Present is false when no upgrade has ever been recorded; the
	// collector emits nothing in that case.
	Present bool
	// TargetVersion is the `target_version` label on both metrics.
	TargetVersion string
	// PhaseCode is the §25.8 lenny_platform_upgrade_phase value: the
	// 1-based working-phase step (Preflight=1, Verification=7) and 0 for a
	// terminal upgrade, so the PlatformUpgradeStuck alert (phase > 0) does
	// not fire on a completed or rolled-back upgrade.
	PhaseCode int
	// DurationSeconds is lenny_platform_upgrade_duration_seconds: the time
	// since the upgrade started. It grows while the upgrade is active and
	// freezes at the terminal-transition time once the upgrade completes
	// or rolls back.
	DurationSeconds float64
}

// MetricsSnapshot reads the singleton and computes the §25.8 upgrade
// metric values at call time. The collector calls it on every Prometheus
// scrape so lenny_platform_upgrade_duration_seconds advances without a
// background refresh.
func (s *Service) MetricsSnapshot(ctx context.Context) (MetricsSnapshot, error) {
	st, ok, err := s.store.Load(ctx)
	if err != nil {
		return MetricsSnapshot{}, err
	}
	if !ok {
		return MetricsSnapshot{}, nil
	}
	code, _ := upgrade.StepNumber(st.Phase) // 0 for the terminal phases
	end := s.now()
	if upgrade.IsTerminal(st.Phase) {
		end = st.UpdatedAt // freeze the duration at completion
	}
	dur := end.Sub(st.StartedAt).Seconds()
	if dur < 0 {
		dur = 0
	}
	return MetricsSnapshot{
		Present:         true,
		TargetVersion:   st.TargetVersion,
		PhaseCode:       code,
		DurationSeconds: dur,
	}, nil
}

// transition loads the singleton, requires an active upgrade, applies
// mutate, stamps UpdatedAt, and persists. It serializes the
// read-modify-write so concurrent proceed/pause/rollback calls cannot
// interleave a stale phase.
func (s *Service) transition(ctx context.Context, mutate func(*State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok, err := s.store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, ErrNoUpgrade
	}
	if err := mutate(&st); err != nil {
		return State{}, err
	}
	st.UpdatedAt = s.now()
	if err := s.store.Save(ctx, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// emitAudit hands ev to the configured AuditSink, stamping the event
// time from the Service clock when the caller left it zero. A nil sink
// drops the event.
func (s *Service) emitAudit(ev AuditEvent) {
	if s.audit == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	s.audit(ev)
}

// AuditEventTypes returns the §16.7 platform-upgrade lifecycle audit
// event types the Service emits. The package test asserts every type is
// catalogued in §16.7 (audit.Catalog).
func AuditEventTypes() []string {
	return []string{
		string(audit.EventPlatformUpgradeStarted),
		string(audit.EventPlatformUpgradePhaseAdvanced),
		string(audit.EventPlatformUpgradeOpsRolled),
		string(audit.EventPlatformUpgradeCrdsUpdated),
		string(audit.EventPlatformUpgradeSchemaMigrated),
		string(audit.EventPlatformUpgradeGatewayRolled),
		string(audit.EventPlatformUpgradeControllersRolled),
		string(audit.EventPlatformUpgradeCompleted),
		string(audit.EventPlatformUpgradeVerified),
		string(audit.EventPlatformUpgradePaused),
		string(audit.EventPlatformUpgradeRolledBack),
	}
}
