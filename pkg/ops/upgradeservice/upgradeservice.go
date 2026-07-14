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
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
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
	// ErrNotOpsRoll is returned by AdvanceOpsRoll outside the OpsRoll phase:
	// the new-pod self-advance is defined only when the upgrade is at OpsRoll
	// (§25.8 line 3508).
	ErrNotOpsRoll = errors.New("upgradeservice: upgrade is not at the OpsRoll phase")
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
	// Error is the §25.8 platform_upgrade_state.error column: the failure
	// code stamped when the watchdog auto-rolls-back (OPS_ROLL_TIMEOUT) or
	// a phase fails. Empty on a healthy upgrade.
	Error string `json:"error,omitempty"`
	// TargetImages is the §25.8 platform_upgrade_state.target_images map:
	// the resolved per-component image references the upgrade converges on.
	// For an air-gapped skip-channel start it is the operator-supplied set
	// (spec line 3422); otherwise the registry-resolved plan. The watchdog
	// reads previousImages/target_images to roll back to the prior ref.
	TargetImages map[string]string `json:"targetImages,omitempty"`
	// PreviousImages records the per-component image references in force
	// before this upgrade started, so the §25.8 OpsRoll watchdog can
	// re-patch a Deployment back to its previous reference on timeout
	// (spec line 3509, metadata.previousImages).
	PreviousImages map[string]string `json:"previousImages,omitempty"`
	// OpsHeartbeat is the §25.8 metadata.opsRollHeartbeat: the time the new
	// lenny-ops pod last wrote an ops_healthy heartbeat during OpsRoll
	// (spec line 3511). The watchdog suppresses the timeout rollback while
	// the heartbeat is fresh, since a live new pod means the roll
	// succeeded and the operator simply has not proceeded yet.
	OpsHeartbeat time.Time `json:"opsHeartbeat,omitempty"`
}

// Active reports whether the upgrade is still in flight (not terminal).
func (s State) Active() bool { return !upgrade.IsTerminal(s.Phase) }

// stepAndDetail returns the §25.2 1-based completedSteps index and the
// human-readable currentStepDetail for s, shared by Progress and
// FullProgress so the two envelopes never disagree on the descriptive
// fields.
func (s State) stepAndDetail() (step int, detail string) {
	step, ok := upgrade.StepNumber(s.Phase)
	if !ok {
		step = upgrade.TotalSteps
	}
	detail = fmt.Sprintf("phase %s", s.Phase)
	switch {
	case s.Phase == upgrade.Complete:
		detail = "upgrade complete"
	case s.Phase == upgrade.RolledBack:
		detail = "upgrade rolled back"
	case s.Paused:
		detail = "Waiting for operator to call /upgrade/proceed"
	}
	return step, detail
}

// Progress returns the §25.2 canonical progress object for the upgrade:
// the machine-readable phase identifier, the numeric step counts, and a
// human-readable detail. A terminal upgrade reports the full step count.
//
// spec: §25.2 (progress envelope), §25.8 (upgrade progress).
func (s State) Progress() map[string]any {
	step, detail := s.stepAndDetail()
	// §25.2: currentStep is the machine-readable step identifier — the
	// phase name (e.g. "OpsRoll"), matching the canonical Progress
	// envelope and the §25.4 Operations Inventory. The 1-based numeric
	// step index is completedSteps, the discrete step count the §25.2
	// envelope defines separately.
	return map[string]any{
		"currentStep":       string(s.Phase),
		"completedSteps":    step,
		"totalSteps":        upgrade.TotalSteps,
		"currentStepDetail": detail,
	}
}

// FullProgress returns the §25.2 canonical progress envelope for the
// upgrade, including the fields Progress leaves out: percent, etaSeconds,
// etaMethod, lastProgressAt, and stalledForSeconds. It is the envelope
// GET /v1/admin/platform/upgrade/status serves (§25.8 line 3496).
//
// percent is always derived from the step count (§25.2 line 387). The
// ETA and stall fields are computed only while the upgrade is active
// (not paused, not terminal): a paused upgrade is awaiting an explicit
// operator proceed by design and a terminal one has finished, so neither
// has a meaningful remaining-time estimate or stall signal (§25.2 line
// 391; compare the §25.8 line 1733 paused example, which reports
// etaMethod "none" and stalledForSeconds null despite a populated
// lastProgressAt). While active, the ETA prefers historical_p50 (once
// ops_operation_baselines has samples for platform_upgrade) and
// otherwise falls back to fixed_phase_durations — the two methods §25.8
// line 3496 names for this endpoint. The step-count percent is deliberately
// kept out of the operations.Compute() ETA-selection inputs: Compute's
// generic fallback chain prefers linear_extrapolation over
// fixed_phase_durations whenever a percent and a start time are both
// present, which is correct for the size/rate-based kinds that chain
// serves (backup, restore) but not for platform_upgrade, whose spec'd
// method chain is historical_p50 then fixed_phase_durations only.
//
// spec: §25.2 (progress envelope, lines 357-401), §25.8 line 3496
// (platform-upgrade progress).
func (s *Service) FullProgress(ctx context.Context, st State) conventions.Progress {
	step, detail := st.stepAndDetail()
	total := upgrade.TotalSteps
	percent := float64(step) * 100.0 / float64(total)

	p := conventions.Progress{
		CurrentStep:       string(st.Phase),
		CurrentStepDetail: detail,
		CompletedSteps:    &step,
		TotalSteps:        &total,
		Percent:           &percent,
		EtaMethod:         conventions.EtaNone,
	}
	if !st.StartedAt.IsZero() {
		p.StartedAt = st.StartedAt.UTC().Format(time.RFC3339)
	}
	if !st.UpdatedAt.IsZero() {
		p.LastProgressAt = st.UpdatedAt.UTC().Format(time.RFC3339)
	}

	active := st.Active() && !st.Paused
	if !active {
		return p
	}

	in := operations.ETAInputs{
		Now:             s.now().UTC(),
		StartedAt:       st.StartedAt,
		LastProgressAt:  st.UpdatedAt,
		ExpectedCadence: operations.ExpectedCadence(operations.KindPlatformUpgrade),
		FixedPhaseDuration: operations.FixedPhaseDuration(
			operations.KindPlatformUpgrade, string(st.Phase),
		),
	}
	if s.baselineReader != nil {
		if p50, sampleSize, found, err := s.baselineReader.Lookup(ctx, string(operations.KindPlatformUpgrade)); err == nil && found {
			in.HistoricalP50 = p50
			in.SampleSize = sampleSize
		}
	}
	eta := operations.Compute(in)
	p.EtaSeconds = eta.EtaSeconds
	p.EtaConfidence = eta.EtaConfidence
	p.EtaMethod = eta.EtaMethod
	p.StalledForSeconds = eta.StalledForSeconds
	return p
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

// DriftManager is the §25.10 target-snapshot seam the upgrade
// orchestrator drives across an upgrade's lifecycle: it deletes the
// target snapshot on rollback (spec line 3551) and promotes the target
// snapshot into the live snapshot at Verification-phase completion (spec
// line 3789). The driftservice.Service satisfies it. A nil manager skips
// both, the cold-start posture for a deployment whose drift service is
// not wired (the OpsRoll target-snapshot write itself is driven by the
// new-pod startup hook in cmd/lenny-ops, not by this orchestrator,
// because only the new binary can compute the snapshot — spec line 3788).
type DriftManager interface {
	// DeleteTargetSnapshot removes the target snapshot written for the
	// given upgrade id. It returns whether a row was deleted.
	DeleteTargetSnapshot(ctx context.Context, upgradeID string) (bool, error)
	// PromoteTargetToLive atomically promotes the target snapshot into the
	// live snapshot for the given upgrade id at Verification completion.
	PromoteTargetToLive(ctx context.Context, upgradeID string) error
}

// Service is the §25.8 platform-upgrade orchestrator.
type Service struct {
	store   Store
	emitter upgrade.Emitter // §16.6 upgrade_progressed operational events; nil is a no-op
	audit   AuditSink       // §16.7 audit events; nil drops
	drift   DriftManager    // §25.10 target-snapshot promote/cleanup; nil skips
	now     func() time.Time
	newID   func() string
	// baselines folds a completed upgrade's wall-clock duration into the
	// §25.2 ops_operation_baselines table; nil skips the record. spec:
	// §25.2 line 393.
	baselines BaselineRecorder
	// baselineReader looks up the current platform_upgrade baseline for
	// the FullProgress historical_p50 ETA selection; nil leaves the ETA
	// chain at fixed_phase_durations (or none).
	baselineReader BaselineReader

	mu sync.Mutex // serializes the read-modify-write of the singleton
}

// BaselineRecorder records a completed operation's wall-clock duration
// into the §25.2 historical baseline table so subsequent operations of
// the kind receive a historical_p50 ETA. The upgrade service records the
// platform_upgrade kind on each Complete transition.
//
// spec: §25.2 line 393 ("ops_operation_baselines ... is updated on each
// operation completion").
type BaselineRecorder interface {
	RecordCompletion(ctx context.Context, kind string, dur time.Duration) error
}

// BaselineReader looks up the current §25.2 historical baseline for a
// kind (the same string-kind seam as BaselineRecorder) so the direct
// upgrade-status envelope can select the historical_p50 ETA method once
// ops_operation_baselines has enough platform_upgrade samples. found is
// false when no completion of the kind has been recorded yet, in which
// case the caller falls through to fixed_phase_durations.
//
// spec: §25.2 line 394 ("a kind with sample_size >= 3 receive[s]
// etaMethod historical_p50").
type BaselineReader interface {
	Lookup(ctx context.Context, kind string) (p50 time.Duration, sampleSize int, found bool, err error)
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
	// DriftManager promotes the §25.10 target snapshot into live at
	// Verification completion (§25.8 line 3789) and deletes it on rollback
	// (§25.8 line 3551). A nil manager skips both.
	DriftManager DriftManager
	// Now supplies the current time; nil defaults to time.Now.
	Now func() time.Time
	// NewID mints the operation id; nil defaults to `upgrade-<uuid>`.
	NewID func() string
	// Baselines records the upgrade's completion duration into the §25.2
	// historical baseline table. A nil recorder skips it.
	Baselines BaselineRecorder
	// BaselineReader looks up the current platform_upgrade baseline so
	// FullProgress can select historical_p50 once samples exist. A nil
	// reader leaves the ETA chain at fixed_phase_durations (or none).
	BaselineReader BaselineReader
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
	return &Service{
		store: opts.Store, emitter: opts.Emitter, audit: opts.Audit, drift: opts.DriftManager,
		now: now, newID: newID, baselines: opts.Baselines, baselineReader: opts.BaselineReader,
	}
}

// StartRequest is the POST /v1/admin/platform/upgrade/start body.
type StartRequest struct {
	// TargetVersion is the version to upgrade to. Required.
	TargetVersion string `json:"version"`
	// ImageDigest is the target image digest for the upgrade_progressed
	// payload. Optional (resolved by digest only when requireDigest).
	ImageDigest string `json:"imageDigest,omitempty"`
	// Images is the §25.8 air-gap skip-channel image set (spec line 3422):
	// when the release channel is disabled (platform.upgradeChannel: "")
	// the operator passes the explicit per-component image references here
	// rather than having them resolved from a channel manifest. Keyed by
	// component short name (gateway, ops, controllers, backup). When empty
	// the caller resolves the plan through the registry service.
	Images map[string]string `json:"images,omitempty"`
	// PreviousImages records the image references in force before this
	// upgrade so the watchdog can roll a Deployment back on timeout. The
	// handler fills it from the version aggregator / running deployment.
	PreviousImages map[string]string `json:"previousImages,omitempty"`
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
		OperationID:    s.newID(),
		Phase:          upgrade.Preflight,
		TargetVersion:  req.TargetVersion,
		ImageDigest:    req.ImageDigest,
		TargetImages:   copyImageMap(req.Images),
		PreviousImages: copyImageMap(req.PreviousImages),
		StartedBy:      req.StartedBy,
		StartedAt:      now,
		UpdatedAt:      now,
		Paused:         true, // awaiting the first proceed
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
		// §25.10 line 3789: the Verification→Complete proceed promotes the
		// in-flight target snapshot into the live snapshot atomically, so
		// from Complete onward GET /v1/admin/drift compares against the new
		// desired state by default. The promote runs before the phase is
		// marked Complete so a promote failure aborts the transition and the
		// upgrade stays at Verification rather than completing with a stale
		// live snapshot (fail closed). The new lenny-ops binary serves this
		// transition, so it can compute over the target row the OpsRoll
		// startup hook wrote.
		if s.drift != nil && exiting == upgrade.Verification && next == upgrade.Complete {
			if err := s.drift.PromoteTargetToLive(ctx, st.OperationID); err != nil {
				return fmt.Errorf("promote target snapshot to live: %w", err)
			}
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

// AdvanceOpsRoll is the §25.8 line 3508 new-pod self-advance: when the new
// lenny-ops pod becomes Ready during OpsRoll, it advances the upgrade from
// OpsRoll to CRDUpdate itself, without an operator proceed. Unlike Proceed,
// which advances whatever phase is active, AdvanceOpsRoll advances only
// from OpsRoll and rejects any other phase with ErrNotOpsRoll, so a startup
// hook that fires outside an in-flight OpsRoll cannot drive an unrelated
// transition. It emits the same platform.upgrade_ops_rolled audit event
// the operator path emits when leaving OpsRoll, and pauses at CRDUpdate
// awaiting the operator's next proceed.
//
// spec: §25.8 line 3508 (new pod self-advances OpsRoll→CRDUpdate on
// startup).
func (s *Service) AdvanceOpsRoll(ctx context.Context) (State, error) {
	return s.transition(ctx, func(st *State) error {
		if st.Phase != upgrade.OpsRoll {
			return ErrNotOpsRoll
		}
		next, err := upgrade.Advance(ctx, s.emitter, PlatformScope, st.Phase, st.ImageDigest)
		if err != nil {
			return err
		}
		s.emitAudit(AuditEvent{
			Type:          string(phaseExitAudit[upgrade.OpsRoll]),
			OperationID:   st.OperationID,
			Actor:         "new-ops-pod",
			OldPhase:      string(upgrade.OpsRoll),
			NewPhase:      string(next),
			TargetVersion: st.TargetVersion,
		})
		st.Phase = next
		// CRDUpdate is non-terminal: it pauses again awaiting the operator's
		// next proceed.
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

// RecordOpsHeartbeat stamps metadata.opsRollHeartbeat (spec line 3511):
// the new lenny-ops pod calls it on startup while the upgrade is in
// OpsRoll to signal it is alive. The §25.8 watchdog suppresses its
// timeout rollback while the heartbeat is fresh. The call is a no-op when
// no upgrade is active or the phase is not OpsRoll, so a stale heartbeat
// write after a completed upgrade does not mutate terminal state.
//
// spec: §25.8 line 3511 (metadata.opsRollHeartbeat).
func (s *Service) RecordOpsHeartbeat(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok, err := s.store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, ErrNoUpgrade
	}
	if st.Phase != upgrade.OpsRoll {
		// A heartbeat outside OpsRoll is a no-op; the new pod only beats
		// while the roll is in flight. Return the current state unchanged.
		return st, nil
	}
	// The heartbeat is not a phase transition, so UpdatedAt (the watchdog's
	// phase-entry clock) is left untouched; only the heartbeat stamp moves.
	st.OpsHeartbeat = s.now().UTC()
	if err := s.store.Save(ctx, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// RollbackOnTimeout is the §25.8 OpsRoll watchdog rollback (spec line
// 3509): it transitions a rollbackable upgrade to RolledBack, stamps the
// failure code (OPS_ROLL_TIMEOUT) into State.Error, emits
// platform.upgrade_rolled_back, and runs the §25.10 target-snapshot
// cleanup. It returns ErrNotRollbackable when the upgrade has passed the
// point of no return (SchemaMigration onward), so a post-migration stall
// is left for the operator (the spec keeps post-migration rollback a
// restore-from-backup decision).
//
// spec: §25.8 line 3509 (OpsRoll timeout auto-rollback), line 3551.
func (s *Service) RollbackOnTimeout(ctx context.Context, code, detail string) (State, error) {
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
		st.Error = code
		st.Reason = detail
		s.emitAudit(AuditEvent{
			Type:          string(audit.EventPlatformUpgradeRolledBack),
			OperationID:   st.OperationID,
			Actor:         "watchdog",
			OldPhase:      string(from),
			NewPhase:      string(next),
			TargetVersion: st.TargetVersion,
			Detail:        code + ": " + detail,
		})
		return nil
	})
	if err != nil {
		return State{}, err
	}
	if s.drift != nil && st.Phase == upgrade.RolledBack {
		_, _ = s.drift.DeleteTargetSnapshot(ctx, st.OperationID)
	}
	return st, nil
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
	// §25.2 line 393: fold the completed upgrade's wall-clock duration into
	// the historical baseline table on the Complete transition. The state
	// machine rejects any further transition once terminal, so this fires
	// exactly once per upgrade. A rollback is not a successful completion
	// and is not recorded.
	if s.baselines != nil && st.Phase == upgrade.Complete {
		dur := st.UpdatedAt.Sub(st.StartedAt)
		if dur < 0 {
			dur = 0
		}
		_ = s.baselines.RecordCompletion(ctx, string(operations.KindPlatformUpgrade), dur)
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

// copyImageMap returns a defensive copy of a per-component image map, or
// nil for an empty input so the State field stays omitempty.
func copyImageMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
