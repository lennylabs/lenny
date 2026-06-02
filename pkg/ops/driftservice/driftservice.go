// SPDX-License-Identifier: MIT

// Package driftservice implements the §25.10 configuration drift
// detection service: it compares the running platform state against the
// desired state and reports the field-by-field differences with each
// drifted field classified by severity.
//
// §25.10 reads the running state from the gateway admin API and the
// desired state from the bootstrap_seed_snapshot Postgres table (or
// from a caller-supplied body for ad-hoc comparison). The v1 service
// implemented here owns the snapshot store — the live and target rows
// of bootstrap_seed_snapshot — and the report assembly; the pure
// field-by-field diff is pkg/drift. The Postgres-backed snapshot store
// reuses the SnapshotStore contract.
//
// The drift detector is read-only: it computes and reports drift but
// applies no change. Reconciliation (POST /v1/admin/drift/reconcile)
// follows the §25.2 dry-run/confirm pattern and calls the gateway admin
// API to apply the desired state.
package driftservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/drift"
)

// §25.10 snapshot row ids: the live snapshot is the desired state in
// effect; the target snapshot is the in-flight desired state an upgrade
// will promote.
const (
	SnapshotLive   = "live"
	SnapshotTarget = "target"
)

// §25.10 snapshot sources recorded on a bootstrap_seed_snapshot row.
const (
	SourceHelmValues      = "helm-values"
	SourceCallerSupplied  = "caller-supplied"
	SourceSnapshotRefresh = "snapshot-refresh"
)

// §25.10 canonical drift error codes. The HTTP layer maps each to its
// documented status code and §25.2 category.
const (
	// ErrCodeDesiredStateMissing is DRIFT_DESIRED_STATE_MISSING: no
	// snapshot exists and the caller supplied no desired state.
	ErrCodeDesiredStateMissing = "DRIFT_DESIRED_STATE_MISSING"
	// ErrCodeNoTargetSnapshot is DRIFT_NO_TARGET_SNAPSHOT: against=target
	// was requested but no upgrade is in flight.
	ErrCodeNoTargetSnapshot = "DRIFT_NO_TARGET_SNAPSHOT"
	// ErrCodeReconcilePartial is DRIFT_RECONCILE_PARTIAL: some resources
	// could not be reconciled.
	ErrCodeReconcilePartial = "DRIFT_RECONCILE_PARTIAL"
	// ErrCodeInvalid is the malformed-request rejection.
	ErrCodeInvalid = "DRIFT_INVALID"
)

// Error is a §25.10 drift failure carrying the canonical error code so
// the HTTP handler maps it to the documented status without
// re-classifying.
//
// HTTPStatus, when non-zero, overrides the default code→status mapping
// in the HTTP layer. §25.10 line 3866 maps DRIFT_DESIRED_STATE_MISSING
// to either 404 (cold-start: no snapshot exists at all) or 503
// (Postgres down). The default mapping is the conservative 503; the
// cold-start path sets HTTPStatus = 404 so a fresh install reads as a
// missing snapshot rather than a transient outage. F-25.10.10.
type Error struct {
	Code       string
	Message    string
	HTTPStatus int
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the §25.10 canonical error code carried by err, or the
// empty string when err is not a drift *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Snapshot is one §25.10 bootstrap_seed_snapshot row: a desired-state
// document with the provenance fields drift detection reports.
type Snapshot struct {
	ID           string         `json:"id"` // "live" or "target"
	DesiredState map[string]any `json:"desiredState"`
	Source       string         `json:"source"`
	UpgradeID    string         `json:"upgradeId,omitempty"`
	WrittenAt    time.Time      `json:"writtenAt"`
	WrittenBy    string         `json:"writtenBy"`
}

// SnapshotStore holds the §25.10 desired-state snapshots. The in-memory
// MemSnapshotStore implements it; the Postgres-backed bootstrap_seed_-
// snapshot store implements the same interface.
type SnapshotStore interface {
	// Get returns the snapshot row of the given id ("live" or "target").
	// ok is false when no such row exists.
	Get(ctx context.Context, id string) (snap Snapshot, ok bool, err error)
	// Put writes (replaces) the snapshot row.
	Put(ctx context.Context, snap Snapshot) error
}

// MemSnapshotStore is the §25.10 in-memory desired-state snapshot store.
// It is safe for concurrent use.
type MemSnapshotStore struct {
	mu   sync.Mutex
	byID map[string]Snapshot
}

// NewMemSnapshotStore returns an empty in-memory snapshot store.
func NewMemSnapshotStore() *MemSnapshotStore {
	return &MemSnapshotStore{byID: make(map[string]Snapshot)}
}

// Get returns the snapshot row of the given id.
func (s *MemSnapshotStore) Get(_ context.Context, id string) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.byID[id]
	return snap, ok, nil
}

// Put writes (replaces) the snapshot row.
func (s *MemSnapshotStore) Put(_ context.Context, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[snap.ID] = snap
	return nil
}

// RunningStateReader collects the §25.10 running platform state from the
// gateway admin API (pools, runtimes, tenants, credential pools). v1
// wires a gateway-client implementation; tests supply a fixed map.
type RunningStateReader interface {
	// RunningState returns the current running state for the given §25.10
	// comparison scope ("all", "pools", "runtimes", "tenants", ...).
	RunningState(ctx context.Context, scope string) (map[string]any, error)
}

// RunningStateCache is the §25.10 line 3822 running-state cache: the
// gateway-aggregation result for a scope is held for
// ops.drift.runningStateCacheTTLSeconds so repeat drift reports skip
// the expensive scatter over 50+ pools. The HTTP layer honors
// ?fresh=true by setting ReportParams.Fresh, which bypasses Lookup and
// updates the entry via Store.
//
// The Redis-backed cache is a documented seam; tests and the v1
// single-process degraded mode use MemRunningStateCache. F-25.10.7.
type RunningStateCache interface {
	// Lookup returns the cached running state for the scope, if any.
	// ok=false signals a miss; the caller falls back to the reader.
	Lookup(ctx context.Context, scope string) (state map[string]any, ok bool, err error)
	// Store writes the running state for the scope with the configured
	// TTL. A Store failure is non-fatal — drift report assembly does not
	// depend on the cache persisting.
	Store(ctx context.Context, scope string, state map[string]any) error
}

// MemRunningStateCache is the §25.10 in-memory running-state cache used
// in the single-process degraded mode and in tests. It is safe for
// concurrent use; entries expire after the TTL configured at
// construction (zero TTL disables caching entirely — every Lookup
// reports a miss). F-25.10.7.
type MemRunningStateCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]memCacheEntry
	now     func() time.Time
}

// memCacheEntry is one cached running-state snapshot keyed by scope.
type memCacheEntry struct {
	state    map[string]any
	expireAt time.Time
}

// NewMemRunningStateCache returns an in-memory cache with the given TTL.
// A non-positive TTL disables caching: Lookup always misses and Store is
// a no-op (the §25.10 line 3824 "0 disables" posture).
func NewMemRunningStateCache(ttl time.Duration) *MemRunningStateCache {
	return &MemRunningStateCache{
		ttl:     ttl,
		entries: make(map[string]memCacheEntry),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the cache clock; tests use it for deterministic
// TTL expiration.
func (c *MemRunningStateCache) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Lookup returns the cached running state for the scope. ok=false on a
// miss or an expired entry; on expiry the entry is dropped so a later
// Store does not race with the still-stale value.
func (c *MemRunningStateCache) Lookup(_ context.Context, scope string) (map[string]any, bool, error) {
	if c.ttl <= 0 {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[scope]
	if !ok {
		return nil, false, nil
	}
	if c.now().After(entry.expireAt) {
		delete(c.entries, scope)
		return nil, false, nil
	}
	return entry.state, true, nil
}

// Store writes the running state for the scope with the configured TTL.
// A zero TTL is a no-op: caching is disabled.
func (c *MemRunningStateCache) Store(_ context.Context, scope string, state map[string]any) error {
	if c.ttl <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[scope] = memCacheEntry{state: state, expireAt: c.now().Add(c.ttl)}
	return nil
}

// DriftEntry is one drifted field in a §25.10 drift report: the dotted
// field path, how it drifted, the desired and actual values, and the
// §25.10 severity classification.
type DriftEntry struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Desired  any    `json:"desired,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Severity string `json:"severity"`
}

// DriftReport is the §25.10 GET /v1/admin/drift response: the drift
// entries plus the desired-state provenance and staleness fields.
type DriftReport struct {
	Scope              string       `json:"scope"`
	Against            string       `json:"against"` // "live", "target", or "caller"
	Drift              []DriftEntry `json:"drift"`
	DriftCount         int          `json:"driftCount"`
	DesiredStateSource string       `json:"desiredStateSource"` // "snapshot" | "caller"
	// SnapshotWrittenAt and the staleness fields are populated only when
	// the comparison ran against a stored snapshot (§25.10).
	SnapshotWrittenAt    *time.Time `json:"snapshot_written_at,omitempty"`
	SnapshotAgeSeconds   *int       `json:"snapshot_age_seconds,omitempty"`
	SnapshotStale        bool       `json:"snapshot_stale"`
	SnapshotStaleWarning string     `json:"snapshot_stale_warning,omitempty"`
	GeneratedAt          time.Time  `json:"generatedAt"`
}

// ValidationResult is the §25.10 POST /v1/admin/drift/validate response:
// the diff between a caller-supplied desired state and the stored
// snapshot, with a match/diverged verdict.
type ValidationResult struct {
	SnapshotValidationResult string       `json:"snapshotValidationResult"` // "match" | "diverged"
	Differences              []DriftEntry `json:"differences"`
	DifferenceCount          int          `json:"differenceCount"`
}

// staleWarningText is the §25.10 verbatim snapshot-staleness warning.
const staleWarningText = "The bootstrap_seed_snapshot is %d days old. " +
	"If any admin-API changes (runtime, pool, tenant, credential-pool, or " +
	"delegation-policy mutations) were made since then without a corresponding " +
	"upgrade, the comparison below may report intentional changes as drift. " +
	"Call POST /v1/admin/drift/snapshot/refresh with the current desired state " +
	"to reconcile, then re-run drift detection."

// Service is the §25.10 configuration drift detection service. It is
// the report/validate/refresh/reconcile surface the HTTP handler calls.
type Service struct {
	snapshots SnapshotStore
	running   RunningStateReader
	// runningCache is the optional §25.10 line 3822 running-state cache.
	// A nil cache disables caching and every report reads fresh.
	runningCache RunningStateCache
	// StaleWarningDays is ops.drift.snapshotStaleWarningDays (default 7);
	// a value of zero disables the staleness warning.
	StaleWarningDays int
	now              func() time.Time

	// metrics is the §25.10 line 3858-3859 drift-metric sink. A nil sink
	// is the no-op default; deps wires a Prometheus-backed one. F-25.10.3.
	metrics Metrics
	// audit is the §25.10 line 3871 drift audit-event sink. A nil sink is
	// the no-op default; deps wires a sink that emits the §16.7 chain
	// rows (logged until the lenny-ops audit-store client lands).
	// F-25.10.2.
	audit AuditSink
	// applier applies a single drifted resource through the gateway admin
	// API during reconciliation (§25.10 line 3842). A nil applier leaves
	// reconcile able to dry-run but not confirm. F-25.10.1.
	applier ResourceApplier
	// progress emits the §25.10 line 3844 operation_progressed event on
	// each resource reconciliation. A nil emitter skips emission.
	// F-25.10.1.
	progress ProgressEmitter
	// reconciles tracks in-flight §25.10 reconciliations so they surface
	// in the §25.4 Operations Inventory with kind drift_reconciliation.
	// F-25.10.1.
	reconciles *ReconcileTracker
}

// DefaultStaleWarningDays is the §25.10 ops.drift.snapshotStaleWarningDays
// spec default (line 3801, 3809).
const DefaultStaleWarningDays = 7

// DefaultRunningStateCacheTTL is the §25.10 line 3824
// ops.drift.runningStateCacheTTLSeconds default.
const DefaultRunningStateCacheTTL = 60 * time.Second

// NewService returns a drift service over the given snapshot store and
// running-state reader. A nil running-state reader leaves Report unable
// to collect running state.
func NewService(snapshots SnapshotStore, running RunningStateReader) *Service {
	return &Service{
		snapshots:        snapshots,
		running:          running,
		StaleWarningDays: DefaultStaleWarningDays,
		now:              func() time.Time { return time.Now().UTC() },
		metrics:          noopMetrics{},
		audit:            noopAuditSink{},
		reconciles:       NewReconcileTracker(),
	}
}

// SetRunningStateCache installs the §25.10 line 3822 running-state
// cache. A nil cache disables caching (the §25.10 line 3824 "0
// disables" posture). F-25.10.7.
func (s *Service) SetRunningStateCache(c RunningStateCache) { s.runningCache = c }

// SetClock overrides the service clock; tests use it for deterministic
// staleness computation.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetMetrics installs the §25.10 drift-metric sink (lenny_drift_-
// detected_total / lenny_drift_reconciled_total). A nil sink restores
// the no-op default. F-25.10.3.
func (s *Service) SetMetrics(m Metrics) {
	if m == nil {
		m = noopMetrics{}
	}
	s.metrics = m
}

// SetAuditSink installs the §25.10 line 3871 drift audit-event sink. A
// nil sink restores the no-op default. F-25.10.2.
func (s *Service) SetAuditSink(a AuditSink) {
	if a == nil {
		a = noopAuditSink{}
	}
	s.audit = a
}

// SetApplier installs the §25.10 line 3842 gateway-side resource
// applier reconciliation calls through. A nil applier leaves reconcile
// able to dry-run but rejects a confirm. F-25.10.1.
func (s *Service) SetApplier(a ResourceApplier) { s.applier = a }

// SetProgressEmitter installs the §25.10 line 3844 operation_progressed
// emitter. A nil emitter skips emission. F-25.10.1.
func (s *Service) SetProgressEmitter(p ProgressEmitter) { s.progress = p }

// ReconcileSource returns the §25.4 Operations Inventory source that
// projects in-flight drift reconciliations onto the canonical Operation
// envelope (kind drift_reconciliation). The host registers it with the
// unified Operations Inventory. F-25.10.1.
func (s *Service) ReconcileSource() *ReconcileTracker { return s.reconciles }

// ReportParams is the §25.10 GET /v1/admin/drift query.
type ReportParams struct {
	// Scope limits which resource types are compared ("all", "pools",
	// "runtimes", "tenants", "credential-pools"). Empty means "all".
	Scope string
	// Against selects the snapshot row to compare against: "live"
	// (default) or "target".
	Against string
	// Desired, when non-nil, is a caller-supplied desired state used in
	// place of the stored snapshot for ad-hoc comparison.
	Desired map[string]any
	// Fresh, when true, bypasses the §25.10 line 3822 running-state
	// cache. The HTTP layer sets this from ?fresh=true. Reconciliation
	// always passes Fresh=true so it never acts on stale state.
	// F-25.10.7.
	Fresh bool
}

// Report computes the §25.10 drift report: it collects the running
// state, resolves the desired state (caller-supplied body or stored
// snapshot), diffs the two, and assembles the report with the §25.10
// provenance and staleness fields.
//
// §25.10 degradation: when the caller supplies a desired-state body the
// report runs without the snapshot store; with no body and no stored
// snapshot the call fails DRIFT_DESIRED_STATE_MISSING. An against=target
// request with no target row fails DRIFT_NO_TARGET_SNAPSHOT.
func (s *Service) Report(ctx context.Context, p ReportParams) (*DriftReport, error) {
	scope := p.Scope
	if scope == "" {
		scope = "all"
	}
	against := p.Against
	if against == "" {
		against = SnapshotLive
	}
	if against != SnapshotLive && against != SnapshotTarget {
		return nil, &Error{Code: ErrCodeInvalid, Message: "against must be live or target"}
	}

	running, err := s.collectRunning(ctx, scope, p.Fresh)
	if err != nil {
		return nil, err
	}

	report := &DriftReport{
		Scope:       scope,
		GeneratedAt: s.now(),
	}

	var desired map[string]any
	if p.Desired != nil {
		// §25.10: a caller-supplied desired state needs no snapshot store
		// — this is the GitOps path that survives a Postgres outage.
		desired = p.Desired
		report.Against = "caller"
		report.DesiredStateSource = "caller"
	} else {
		snap, ok, snapErr := s.snapshots.Get(ctx, against)
		if snapErr != nil {
			// §25.10 line 3866: a snapshot-store failure (Postgres down)
			// surfaces as DRIFT_DESIRED_STATE_MISSING with the default
			// 503 mapping. The underlying store error is preserved in the
			// message so operators can correlate with §25.4 healthz output.
			// F-25.10.10.
			return nil, &Error{
				Code:    ErrCodeDesiredStateMissing,
				Message: "snapshot store unavailable: " + snapErr.Error(),
			}
		}
		if !ok {
			if against == SnapshotTarget {
				return nil, &Error{
					Code:    ErrCodeNoTargetSnapshot,
					Message: "no target snapshot — no upgrade is in flight",
				}
			}
			// §25.10 line 3866: the cold-start "no snapshot exists" case is
			// 404, distinct from the 503 "Postgres down" case above. The
			// explicit HTTPStatus override pins the status without changing
			// the canonical error code. F-25.10.10.
			return nil, &Error{
				Code:       ErrCodeDesiredStateMissing,
				Message:    "no bootstrap_seed_snapshot and no caller-supplied desired state",
				HTTPStatus: 404,
			}
		}
		desired = snap.DesiredState
		report.Against = against
		report.DesiredStateSource = "snapshot"
		s.applyStaleness(report, snap)
	}

	for _, c := range drift.Diff(desired, running) {
		entry := toDriftEntry(c)
		report.Drift = append(report.Drift, entry)
		// §25.10 line 3858: lenny_drift_detected_total{resource_type,
		// severity}. The resource_type label is the top path segment
		// (pools, runtimes, tenants, credential-pools, ...). F-25.10.3.
		s.metrics.DriftDetected(resourceTypeOf(entry.Path), entry.Severity)
	}
	report.DriftCount = len(report.Drift)
	// §25.10 line 3871: drift.report_generated carries the scope, the
	// row compared against, and the drift count so the audit trail
	// records every drift read. F-25.10.2.
	s.audit.Emit(AuditEvent{
		Type: EventReportGenerated,
		Details: map[string]any{
			"scope":              report.Scope,
			"against":            report.Against,
			"driftCount":         report.DriftCount,
			"desiredStateSource": report.DesiredStateSource,
		},
	})
	return report, nil
}

// applyStaleness fills the §25.10 snapshot-staleness fields on the
// report from the snapshot's written_at timestamp.
func (s *Service) applyStaleness(report *DriftReport, snap Snapshot) {
	writtenAt := snap.WrittenAt
	report.SnapshotWrittenAt = &writtenAt
	age := int(s.now().Sub(writtenAt).Seconds())
	if age < 0 {
		age = 0
	}
	report.SnapshotAgeSeconds = &age
	if drift.SnapshotStale(age, s.StaleWarningDays) {
		report.SnapshotStale = true
		report.SnapshotStaleWarning = formatStaleWarning(age)
	}
}

// BothReport is the §25.10 line 3791 GET /v1/admin/drift?against=both
// response: the running state diffed against both the live snapshot
// (pre-upgrade drift) and the in-flight target snapshot (what the
// upgrade will change), in a single response. F-25.10.6.
type BothReport struct {
	Scope       string       `json:"scope"`
	Against     string       `json:"against"` // always "both"
	Live        *DriftReport `json:"live"`
	Target      *DriftReport `json:"target"`
	GeneratedAt time.Time    `json:"generatedAt"`
}

// ReportBoth computes the §25.10 against=both report. It collects the
// running state once and diffs it against both the live and the target
// snapshot. A caller-supplied desired body is rejected — both mode is
// defined only over the two stored snapshots (§25.10 line 3791,
// "during an active upgrade"). A missing target row fails
// DRIFT_NO_TARGET_SNAPSHOT, matching the against=target contract.
// F-25.10.6.
func (s *Service) ReportBoth(ctx context.Context, p ReportParams) (*BothReport, error) {
	if p.Desired != nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "against=both compares the stored live and target snapshots; a caller-supplied desired body is not allowed"}
	}
	scope := p.Scope
	if scope == "" {
		scope = "all"
	}
	// §25.10 line 3791: both mode is meaningful only when a target row
	// exists. Fail closed before the expensive running-state read so a
	// non-upgrade call gets the documented DRIFT_NO_TARGET_SNAPSHOT.
	if _, ok, err := s.snapshots.Get(ctx, SnapshotTarget); err != nil {
		return nil, &Error{Code: ErrCodeDesiredStateMissing, Message: "snapshot store unavailable: " + err.Error()}
	} else if !ok {
		return nil, &Error{Code: ErrCodeNoTargetSnapshot, Message: "no target snapshot — no upgrade is in flight"}
	}
	live, err := s.Report(ctx, ReportParams{Scope: scope, Against: SnapshotLive, Fresh: p.Fresh})
	if err != nil {
		return nil, err
	}
	// §25.10 line 3824: the live report already populated the cache (when
	// configured), so the target read reuses it rather than re-scattering
	// over the gateway.
	target, err := s.Report(ctx, ReportParams{Scope: scope, Against: SnapshotTarget, Fresh: false})
	if err != nil {
		return nil, err
	}
	return &BothReport{
		Scope:       scope,
		Against:     "both",
		Live:        live,
		Target:      target,
		GeneratedAt: s.now(),
	}, nil
}

// ValidateParams is the §25.10 POST /v1/admin/drift/validate request
// shape. Desired is the externally-supplied source-of-truth (typically a
// Helm values document); Stored, when non-nil, supplies the stored
// snapshot side of the diff in place of bootstrap_seed_snapshot — the
// §25.10 "Postgres down, two caller-supplied snapshots" offline-
// validation path that lets GitOps agents diff their pre- and post-
// edit snapshots without a working snapshot store. F-25.10.12.
type ValidateParams struct {
	Desired map[string]any
	Stored  map[string]any
}

// Validate computes the §25.10 POST /v1/admin/drift/validate diff
// between a caller-supplied desired state and the stored live snapshot,
// reporting a match/diverged verdict. It is read-only — no state is
// changed.
//
// When p.Stored is non-nil the snapshot store is not consulted: the diff
// is computed between p.Stored and p.Desired. This is the §25.10
// degradation path for `validate` during a Postgres outage —
// F-25.10.12.
func (s *Service) Validate(ctx context.Context, p ValidateParams) (*ValidationResult, error) {
	if p.Desired == nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "a desired-state body is required"}
	}
	stored := p.Stored
	if stored == nil {
		snap, ok, err := s.snapshots.Get(ctx, SnapshotLive)
		if err != nil {
			// §25.10 line 3866 Postgres-down case → 503 via the default
			// DRIFT_DESIRED_STATE_MISSING mapping (HTTPStatus left zero).
			return nil, &Error{
				Code:    ErrCodeDesiredStateMissing,
				Message: "snapshot store unavailable: " + err.Error(),
			}
		}
		if !ok {
			// §25.10 line 3866 cold-start path: no snapshot exists at all
			// → 404. The caller may retry by supplying a stored snapshot
			// in the request body for offline validation. F-25.10.10 /
			// F-25.10.12.
			return nil, &Error{
				Code:       ErrCodeDesiredStateMissing,
				Message:    "no stored bootstrap_seed_snapshot to validate against",
				HTTPStatus: 404,
			}
		}
		stored = snap.DesiredState
	}
	res := &ValidationResult{SnapshotValidationResult: "match"}
	for _, c := range drift.Diff(stored, p.Desired) {
		res.Differences = append(res.Differences, toDriftEntry(c))
	}
	res.DifferenceCount = len(res.Differences)
	if res.DifferenceCount > 0 {
		res.SnapshotValidationResult = "diverged"
	}
	return res, nil
}

// RefreshRequest is the §25.10 POST /v1/admin/drift/snapshot/refresh
// request body.
type RefreshRequest struct {
	Desired map[string]any `json:"desired"`
	Confirm bool           `json:"confirm"`
	// WrittenBy is the caller identity; the HTTP layer fills it.
	WrittenBy string `json:"-"`
}

// RefreshResult is the §25.10 snapshot-refresh outcome, carrying the
// previous-snapshot provenance for the drift.snapshot_refreshed audit
// event. The §25.10 line 3871 event details require
// {previous_written_at, previous_source, new_source, byteSize}; this
// struct populates all four so the audit emitter can render the event
// without re-marshalling. F-25.10.8.
type RefreshResult struct {
	Replaced          bool       `json:"replaced"`
	NewWrittenAt      time.Time  `json:"newWrittenAt"`
	PreviousWrittenAt *time.Time `json:"previousWrittenAt,omitempty"`
	PreviousSource    string     `json:"previousSource,omitempty"`
	NewSource         string     `json:"newSource"`
	// ByteSize is the JSON-encoded length of the new desired state. §25.10
	// line 3871 carries this on the drift.snapshot_refreshed audit event
	// so operators can correlate snapshot growth with downstream Postgres
	// row size and the §25.10 line 3824 cache pressure. F-25.10.8.
	ByteSize int `json:"byteSize"`
}

// RefreshSnapshot replaces the §25.10 live bootstrap_seed_snapshot row
// with the caller-supplied desired state. §25.10 keeps refresh an
// explicit operator action — the HTTP layer requires confirm:true (the
// §25.2 dry-run/confirm pattern) before calling this.
//
// The returned RefreshResult carries the §25.10 line 3871
// drift.snapshot_refreshed audit-event details: previous_written_at,
// previous_source, new_source, and the JSON-encoded byteSize of the
// new desired state.
func (s *Service) RefreshSnapshot(ctx context.Context, req RefreshRequest) (*RefreshResult, error) {
	if req.Desired == nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "a desired-state body is required"}
	}
	// §25.10 line 3871: byteSize is the JSON-encoded length of the new
	// desired state. The JSON-encoding choice matches how the snapshot is
	// stored in Postgres (`bootstrap_seed_snapshot.desired_state JSONB`)
	// so the audit event reports the persisted row size, not the
	// in-memory Go map. F-25.10.8.
	encoded, err := json.Marshal(req.Desired)
	if err != nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "desired state is not JSON-encodable: " + err.Error()}
	}
	res := &RefreshResult{NewSource: SourceSnapshotRefresh, ByteSize: len(encoded)}
	if prev, ok, err := s.snapshots.Get(ctx, SnapshotLive); err != nil {
		return nil, err
	} else if ok {
		pw := prev.WrittenAt
		res.PreviousWrittenAt = &pw
		res.PreviousSource = prev.Source
	}
	now := s.now()
	if err := s.snapshots.Put(ctx, Snapshot{
		ID:           SnapshotLive,
		DesiredState: req.Desired,
		Source:       SourceSnapshotRefresh,
		WrittenAt:    now,
		WrittenBy:    req.WrittenBy,
	}); err != nil {
		return nil, err
	}
	res.Replaced = true
	res.NewWrittenAt = now
	// §25.10 line 3871: drift.snapshot_refreshed carries the previous and
	// new provenance plus the JSON byteSize of the new desired state.
	// F-25.10.2 / F-25.10.8.
	details := map[string]any{
		"new_source": res.NewSource,
		"byteSize":   res.ByteSize,
	}
	if res.PreviousWrittenAt != nil {
		details["previous_written_at"] = res.PreviousWrittenAt.UTC().Format(time.RFC3339)
		details["previous_source"] = res.PreviousSource
	}
	s.audit.Emit(AuditEvent{Type: EventSnapshotRefreshed, Actor: req.WrittenBy, Details: details})
	return res, nil
}

// collectRunning reads the running state for the scope. A nil
// running-state reader yields an empty running state so the report
// still assembles (every desired field reads as removed drift).
//
// When a §25.10 line 3822 running-state cache is configured and
// fresh=false, the cache is consulted first and a fresh read updates
// the cache for the next call. fresh=true bypasses the cache entirely
// and does not update it — §25.10 line 3824 reserves the cache for the
// non-bypass path so a ?fresh=true probe cannot crowd out the baseline
// value other callers rely on. Cache failures are non-fatal: a Lookup
// error degrades to a fresh read, a Store error is silently ignored
// (the report has already assembled and the next call will retry).
// F-25.10.7.
func (s *Service) collectRunning(ctx context.Context, scope string, fresh bool) (map[string]any, error) {
	if s.running == nil {
		return map[string]any{}, nil
	}
	if s.runningCache != nil && !fresh {
		if cached, ok, err := s.runningCache.Lookup(ctx, scope); err == nil && ok {
			return cached, nil
		}
	}
	state, err := s.running.RunningState(ctx, scope)
	if err != nil {
		return nil, err
	}
	if s.runningCache != nil && !fresh {
		_ = s.runningCache.Store(ctx, scope, state)
	}
	return state, nil
}

// toDriftEntry projects a pkg/drift Change into the §25.10 wire entry.
func toDriftEntry(c drift.Change) DriftEntry {
	return DriftEntry{
		Path:     c.Path,
		Kind:     string(c.Kind),
		Desired:  c.Desired,
		Actual:   c.Actual,
		Severity: string(c.Severity),
	}
}

// resourceTypeOf returns the §25.10 resource_type metric label for a
// drifted field path: the top dotted segment (pools, runtimes, tenants,
// credential-pools, ...). A path with no dot is its own resource type;
// an empty path reports "unknown" so the metric label is never empty.
func resourceTypeOf(path string) string {
	if path == "" {
		return "unknown"
	}
	top, _, _ := strings.Cut(path, ".")
	return top
}

// formatStaleWarning renders the §25.10 staleness warning for an age in
// seconds.
func formatStaleWarning(ageSeconds int) string {
	days := ageSeconds / 86400
	return fmt.Sprintf(staleWarningText, days)
}
