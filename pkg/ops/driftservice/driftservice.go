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
	"errors"
	"fmt"
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
type Error struct {
	Code    string
	Message string
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
// the report/validate/refresh surface the HTTP handler calls.
type Service struct {
	snapshots SnapshotStore
	running   RunningStateReader
	// StaleWarningDays is ops.drift.snapshotStaleWarningDays (default 7);
	// a value of zero disables the staleness warning.
	StaleWarningDays int
	now              func() time.Time
}

// defaultStaleWarningDays is the §25.10 ops.drift.snapshotStaleWarningDays
// default.
const defaultStaleWarningDays = 7

// NewService returns a drift service over the given snapshot store and
// running-state reader. A nil running-state reader leaves Report unable
// to collect running state.
func NewService(snapshots SnapshotStore, running RunningStateReader) *Service {
	return &Service{
		snapshots:        snapshots,
		running:          running,
		StaleWarningDays: defaultStaleWarningDays,
		now:              func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the service clock; tests use it for deterministic
// staleness computation.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

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

	running, err := s.collectRunning(ctx, scope)
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
			return nil, snapErr
		}
		if !ok {
			if against == SnapshotTarget {
				return nil, &Error{
					Code:    ErrCodeNoTargetSnapshot,
					Message: "no target snapshot — no upgrade is in flight",
				}
			}
			return nil, &Error{
				Code:    ErrCodeDesiredStateMissing,
				Message: "no bootstrap_seed_snapshot and no caller-supplied desired state",
			}
		}
		desired = snap.DesiredState
		report.Against = against
		report.DesiredStateSource = "snapshot"
		s.applyStaleness(report, snap)
	}

	for _, c := range drift.Diff(desired, running) {
		report.Drift = append(report.Drift, toDriftEntry(c))
	}
	report.DriftCount = len(report.Drift)
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

// Validate computes the §25.10 POST /v1/admin/drift/validate diff
// between a caller-supplied desired state and the stored live snapshot,
// reporting a match/diverged verdict. It is read-only — no state is
// changed.
func (s *Service) Validate(ctx context.Context, desired map[string]any) (*ValidationResult, error) {
	if desired == nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "a desired-state body is required"}
	}
	snap, ok, err := s.snapshots.Get(ctx, SnapshotLive)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{
			Code:    ErrCodeDesiredStateMissing,
			Message: "no stored bootstrap_seed_snapshot to validate against",
		}
	}
	res := &ValidationResult{SnapshotValidationResult: "match"}
	for _, c := range drift.Diff(snap.DesiredState, desired) {
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
// event.
type RefreshResult struct {
	Replaced          bool       `json:"replaced"`
	NewWrittenAt      time.Time  `json:"newWrittenAt"`
	PreviousWrittenAt *time.Time `json:"previousWrittenAt,omitempty"`
	PreviousSource    string     `json:"previousSource,omitempty"`
	NewSource         string     `json:"newSource"`
}

// RefreshSnapshot replaces the §25.10 live bootstrap_seed_snapshot row
// with the caller-supplied desired state. §25.10 keeps refresh an
// explicit operator action — the HTTP layer requires confirm:true (the
// §25.2 dry-run/confirm pattern) before calling this.
func (s *Service) RefreshSnapshot(ctx context.Context, req RefreshRequest) (*RefreshResult, error) {
	if req.Desired == nil {
		return nil, &Error{Code: ErrCodeInvalid, Message: "a desired-state body is required"}
	}
	res := &RefreshResult{NewSource: SourceSnapshotRefresh}
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
	return res, nil
}

// collectRunning reads the running state for the scope. A nil
// running-state reader yields an empty running state so the report
// still assembles (every desired field reads as removed drift).
func (s *Service) collectRunning(ctx context.Context, scope string) (map[string]any, error) {
	if s.running == nil {
		return map[string]any{}, nil
	}
	return s.running.RunningState(ctx, scope)
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

// formatStaleWarning renders the §25.10 staleness warning for an age in
// seconds.
func formatStaleWarning(ageSeconds int) string {
	days := ageSeconds / 86400
	return fmt.Sprintf(staleWarningText, days)
}
