// SPDX-License-Identifier: MIT

package quotacheckpoint

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// Service drives the §11.2 token-usage checkpoint and the §11.2 line 48 /
// §24.6 MAX-rule reconcile. Its seams are wired in production over the
// quotastore Redis counter, the SessionStore, and the tenant registry; a
// missing required seam makes the corresponding method a no-op so a
// partial wiring degrades to the prior behaviour rather than panicking.
type Service struct {
	// Store persists and reads the Postgres checkpoint rows. Required.
	Store Store
	// Subjects enumerates the active (tenant, user) pairs to checkpoint.
	// Required by Checkpoint.
	Subjects SubjectLister
	// Periods resolves each tenant's reset period. Required.
	Periods PeriodResolver
	// Reader reads the current Redis window totals. Required.
	Reader WindowReader
	// Restorer applies the MAX rule on reconcile. Required by Reconcile.
	Restorer CounterRestorer
	// Tenants gates a per-tenant reconcile with a 404 for an unknown
	// tenant. Optional; a nil exister skips the existence check.
	Tenants TenantExister
	// FailOpen is the §12.4 / §11.2 line 48 MAX-rule source (2): the
	// in-memory token counter the recording path accumulated while the
	// shared Redis counter was unreachable. When set, Reconcile folds each
	// window's accumulated value into the MAX so usage that a Redis write
	// dropped during a fail-open window is restored — both for windows that
	// carry a Postgres checkpoint row and for windows that opened entirely
	// during the outage (no checkpoint row). Optional; a nil accumulator
	// reduces the rule to MAX(redis_current, postgres_checkpoint).
	FailOpen *quotafailopen.Accumulator
	// PodUsage is the §11.2 line 46 crash-recovery MAX-rule source: the
	// pod-reported cumulative token total each bound direct-mode session's
	// adapter re-reports on reconnection to a new gateway replica. When set,
	// Reconcile folds each window's pod-reported cumulative total into the
	// MAX — both for windows that carry a Postgres checkpoint row and for
	// windows that opened during the outage with no checkpoint row — so a
	// reconnected replica reconstructs a direct-mode counter as
	// MAX(redis_current, postgres_checkpoint, in_memory_failopen,
	// pod_reported_cumulative) per §11.2 line 46 rather than silently
	// under-counting a direct-mode session whose Redis usage was lost.
	// Optional; a nil reader preserves the exact prior behaviour (the MAX
	// omits the pod-reported source), guarded like FailOpen==nil.
	PodUsage PodUsageReader
	// Metrics records reconcile outcomes. Optional.
	Metrics MetricEmitter
	// Now is the injectable clock. Nil selects time.Now().UTC().
	Now func() time.Time
	// Logf, when set, receives a one-line diagnostic per sweep.
	Logf func(format string, args ...any)
}

// ReconcileScope selects what a reconcile pass covers. Exactly one of
// AllTenants or TenantID is set.
type ReconcileScope struct {
	AllTenants bool
	TenantID   string
}

// ReconcileResult summarizes a reconcile pass: the §11.2 line 48 MAX-rule
// inputs and the authoritative value written back to Redis per scope.
type ReconcileResult struct {
	TenantsReconciled int
	CountersWritten   int
	Counters          []CounterResult
}

// CounterResult records the MAX-rule inputs for one restored window so an
// operator can confirm the rule wrote the expected value rather than
// silently resetting a counter to a stale checkpoint.
type CounterResult struct {
	TenantID        string
	Scope           string
	SubjectID       string
	Period          string
	CheckpointValue int64
	// InMemoryValue is the live Redis value read before the restore (the
	// §11.2 line 48 redis_current input).
	InMemoryValue int64
	// FailOpenValue is the §12.4 source (2) in-memory fail-open accumulator
	// value folded into the MAX. Zero when no fail-open reader is wired or
	// the window accumulated nothing during an outage.
	FailOpenValue int64
	// PodUsageValue is the §11.2 line 46 pod-reported cumulative token total
	// folded into the MAX. Zero when no pod-usage reader is wired or no bound
	// direct-mode session contributed a total for the window.
	PodUsageValue int64
	WrittenValue  int64
}

// Checkpoint persists the current §11.2 window totals for every active
// (tenant, user) subject to Postgres. A subject whose tenant uses the
// rolling period is skipped (no single restorable window). A window with
// no recorded usage is skipped so the table tracks only counters with real
// state. A per-subject read error skips that subject rather than aborting
// the sweep.
//
// spec: §11.2 line 44 (durable checkpoint).
func (s *Service) Checkpoint(ctx context.Context) {
	if s == nil || s.Store == nil || s.Subjects == nil || s.Periods == nil || s.Reader == nil {
		return
	}
	subjects, err := s.Subjects.ListActiveSubjects(ctx)
	if err != nil {
		s.logf("quotacheckpoint: checkpoint: list active subjects: %v", err)
		return
	}
	now := s.now()
	var rows []Row
	// Dedup the per-tenant rollup so N active users of one tenant produce a
	// single tenant-scope row per (tenant, period, window).
	tenantSeen := make(map[string]struct{})
	for _, subj := range subjects {
		rows = append(rows, s.subjectRows(ctx, subj, now, tenantSeen)...)
	}
	if len(rows) == 0 {
		return
	}
	if err := s.Store.Write(ctx, rows); err != nil {
		s.logf("quotacheckpoint: checkpoint: write %d row(s): %v", len(rows), err)
		return
	}
	s.logf("quotacheckpoint: checkpointed %d token-usage window(s)", len(rows))
}

// CheckpointSubject persists the §11.2 line 44 "final reconciliation"
// checkpoint for one (tenant, user) at session completion: the final
// cumulative window total is written to Postgres as the authoritative
// value so a subsequent recovery has an accurate baseline. Best-effort —
// a missing seam or a read/write error returns nil so it never fails the
// terminal-state transition that triggered it.
//
// spec: §11.2 line 44 ("on session completion as final reconciliation";
// "the final cumulative token usage is always written to Postgres").
func (s *Service) CheckpointSubject(ctx context.Context, tenantID, userID string) error {
	if s == nil || s.Store == nil || s.Periods == nil || s.Reader == nil {
		return nil
	}
	rows := s.subjectRows(ctx, Subject{TenantID: tenantID, UserID: userID}, s.now(), nil)
	if len(rows) == 0 {
		return nil
	}
	if err := s.Store.Write(ctx, rows); err != nil {
		s.logf("quotacheckpoint: final checkpoint tenant=%q user=%q: %v", tenantID, userID, err)
		return err
	}
	return nil
}

// subjectRows builds the checkpoint rows for one subject at now: the
// per-user window (when the user has recorded usage) and the per-tenant
// rollup window (deduped via tenantSeen when non-nil). A rolling-period
// tenant, a label error, or a zero total contributes no row.
func (s *Service) subjectRows(ctx context.Context, subj Subject, now time.Time, tenantSeen map[string]struct{}) []Row {
	period, err := s.Periods.ResolvePeriod(ctx, subj.TenantID)
	if err != nil {
		return nil
	}
	label, err := quotastore.WindowLabel(period, now)
	if err != nil {
		// Rolling period: no single restorable window. Documented skip.
		return nil
	}
	var rows []Row
	if subj.UserID != "" {
		if total, err := s.Reader.Usage(ctx, subj.TenantID, subj.UserID, period, now); err == nil && total > 0 {
			rows = append(rows, Row{
				TenantID:    subj.TenantID,
				Scope:       ScopeUser,
				SubjectID:   subj.UserID,
				Period:      string(period),
				WindowLabel: label,
				TokenTotal:  total,
			})
		}
	}
	tenantKey := subj.TenantID + "\x00" + string(period) + "\x00" + label
	if tenantSeen != nil {
		if _, dup := tenantSeen[tenantKey]; dup {
			return rows
		}
		tenantSeen[tenantKey] = struct{}{}
	}
	if total, err := s.Reader.TenantRollupUsage(ctx, subj.TenantID, period, now); err == nil && total > 0 {
		rows = append(rows, Row{
			TenantID:    subj.TenantID,
			Scope:       ScopeTenant,
			SubjectID:   "",
			Period:      string(period),
			WindowLabel: label,
			TokenTotal:  total,
		})
	}
	return rows
}

// Reconcile runs the §11.2 line 48 / §24.6 MAX-rule reconstruction: it
// reads the durable checkpoint for the requested scope and, for each
// counter whose window is still current, restores the Redis value to
// MAX(redis_current, postgres_checkpoint). A checkpoint whose window has
// already rolled over is skipped rather than reviving a stale bucket. The
// per-tenant scope returns ErrTenantNotFound for an unknown tenant.
//
// spec: §11.2 line 48; §24.6 line 99.
func (s *Service) Reconcile(ctx context.Context, scope ReconcileScope) (ReconcileResult, error) {
	if s == nil || s.Store == nil || s.Reader == nil || s.Restorer == nil {
		return ReconcileResult{}, nil
	}
	var (
		rows []Row
		err  error
	)
	if scope.AllTenants {
		rows, err = s.Store.ListActive(ctx)
	} else {
		if s.Tenants != nil {
			ok, exErr := s.Tenants.TenantExists(ctx, scope.TenantID)
			if exErr != nil {
				return ReconcileResult{}, exErr
			}
			if !ok {
				return ReconcileResult{}, ErrTenantNotFound
			}
		}
		rows, err = s.Store.ListByTenant(ctx, scope.TenantID)
	}
	if err != nil {
		return ReconcileResult{}, err
	}

	now := s.now()
	var res ReconcileResult
	tenants := make(map[string]struct{})
	// Track the windows the checkpoint-row pass already restored so the
	// fail-open-only pass below does not restore the same window twice.
	restored := make(map[string]struct{})
	for _, row := range rows {
		period := quota.ResetPeriod(row.Period)
		curLabel, labelErr := quotastore.WindowLabel(period, now)
		if labelErr != nil || curLabel != row.WindowLabel {
			// Stale or non-fixed window — restoring it would revive a bucket
			// that has already rolled over.
			s.inc(OutcomeSkipped)
			continue
		}
		rv, restoreErr := s.restoreRow(ctx, row, period, now)
		if restoreErr != nil {
			s.logf("quotacheckpoint: reconcile: restore %s/%s tenant=%q: %v",
				row.Scope, row.SubjectID, row.TenantID, restoreErr)
			continue
		}
		res.CountersWritten++
		tenants[row.TenantID] = struct{}{}
		restored[windowKey(row.Scope, row.TenantID, row.SubjectID, row.Period)] = struct{}{}
		res.Counters = append(res.Counters, CounterResult{
			TenantID:        row.TenantID,
			Scope:           row.Scope,
			SubjectID:       row.SubjectID,
			Period:          row.Period,
			CheckpointValue: row.TokenTotal,
			InMemoryValue:   rv.live,
			FailOpenValue:   rv.failOpen,
			PodUsageValue:   rv.podUsage,
			WrittenValue:    rv.written,
		})
		s.inc(OutcomeRestored)
	}
	// §12.4 source (2) / §11.2 line 46 no-checkpoint windows: a window that
	// opened entirely during the outage has no checkpoint row above, so the
	// row pass never restores it. Restore each such window directly from the
	// in-memory fail-open accumulator and the pod-reported cumulative total
	// (MAX(redis_current, in_memory_failopen, pod_reported_cumulative)).
	s.reconcileNoCheckpointWindows(ctx, scope, now, restored, tenants, &res)
	res.TenantsReconciled = len(tenants)
	s.logf("quotacheckpoint: reconciled %d counter(s) across %d tenant(s)", res.CountersWritten, res.TenantsReconciled)
	return res, nil
}

// noCheckpointWindow accumulates the fail-open and pod-reported cumulative
// inputs for one (scope, tenant, subject, period) window that carries no
// Postgres checkpoint row, so the pass restores each such window once from
// the union of the two source snapshots.
type noCheckpointWindow struct {
	scope     string
	tenantID  string
	subjectID string
	period    quota.ResetPeriod
	failOpen  int64
	podUsage  int64
}

// reconcileNoCheckpointWindows restores every still-current window that the
// checkpoint-row pass did not already handle but for which a fail-open
// accumulator entry (§12.4 source (2)) or a pod-reported cumulative total
// (§11.2 line 46 source) exists. The two source snapshots are unioned so a
// window carried by both is restored once from MAX(fail_open,
// pod_reported_cumulative); a per-tenant reconcile is scoped to its tenant.
// spec: §12.4 source (2); §11.2 line 46; §11.2 line 48.
func (s *Service) reconcileNoCheckpointWindows(ctx context.Context, scope ReconcileScope, now time.Time, restored, tenants map[string]struct{}, res *ReconcileResult) {
	if s.Restorer == nil {
		return
	}
	windows := make(map[string]*noCheckpointWindow)
	// key builds the union key and records the window; the empty user id
	// addresses the per-tenant rollup scope, matching the row pass.
	key := func(tenantID, userID string, period quota.ResetPeriod) (*noCheckpointWindow, bool) {
		if !scope.AllTenants && tenantID != scope.TenantID {
			return nil, false
		}
		scopeName := ScopeUser
		if userID == "" {
			scopeName = ScopeTenant
		}
		if _, done := restored[windowKey(scopeName, tenantID, userID, string(period))]; done {
			// The checkpoint-row pass already restored this window (folding both
			// sources in), so it must not be restored a second time.
			return nil, false
		}
		k := windowKey(scopeName, tenantID, userID, string(period))
		w := windows[k]
		if w == nil {
			w = &noCheckpointWindow{scope: scopeName, tenantID: tenantID, subjectID: userID, period: period}
			windows[k] = w
		}
		return w, true
	}
	if s.FailOpen != nil {
		for _, samp := range s.FailOpen.Snapshot(now) {
			if w, ok := key(samp.TenantID, samp.UserID, samp.Period); ok {
				w.failOpen = samp.Tokens
			}
		}
	}
	if s.PodUsage != nil {
		for _, samp := range s.PodUsage.Snapshot(ctx, now) {
			if w, ok := key(samp.TenantID, samp.UserID, samp.Period); ok {
				w.podUsage = samp.Tokens
			}
		}
	}
	for _, w := range windows {
		seed := maxInt64(w.failOpen, w.podUsage)
		var written int64
		var err error
		if w.scope == ScopeUser {
			written, err = s.Restorer.RestoreUserWindow(ctx, w.tenantID, w.subjectID, w.period, now, seed)
		} else {
			written, err = s.Restorer.RestoreTenantRollupWindow(ctx, w.tenantID, w.period, now, seed)
		}
		if err != nil {
			s.logf("quotacheckpoint: reconcile: no-checkpoint restore %s/%s tenant=%q: %v",
				w.scope, w.subjectID, w.tenantID, err)
			continue
		}
		res.CountersWritten++
		tenants[w.tenantID] = struct{}{}
		res.Counters = append(res.Counters, CounterResult{
			TenantID:      w.tenantID,
			Scope:         w.scope,
			SubjectID:     w.subjectID,
			Period:        string(w.period),
			FailOpenValue: w.failOpen,
			PodUsageValue: w.podUsage,
			WrittenValue:  written,
		})
		s.inc(OutcomeRestored)
	}
}

// windowKey identifies a single (scope, tenant, subject, period) window for
// the reconcile's dedup set.
func windowKey(scope, tenantID, subjectID, period string) string {
	return scope + "\x00" + tenantID + "\x00" + subjectID + "\x00" + period
}

// restoreValues carries the MAX-rule inputs and result for one restored
// window so the caller can populate the CounterResult without a long return
// tuple.
type restoreValues struct {
	live     int64
	failOpen int64
	podUsage int64
	written  int64
}

// restoreRow reads the live Redis value (the §11.2 line 48 redis_current
// input) and applies the MAX rule for one row. The §12.4 source (2)
// in-memory fail-open accumulator and the §11.2 line 46 source pod-reported
// cumulative total are folded into the checkpoint seed so the Restorer
// writes MAX(redis_current, postgres_checkpoint, in_memory_failopen,
// pod_reported_cumulative). spec: §11.2 line 46; §12.4 source (2).
func (s *Service) restoreRow(ctx context.Context, row Row, period quota.ResetPeriod, now time.Time) (restoreValues, error) {
	switch row.Scope {
	case ScopeUser:
		live, err := s.Reader.Usage(ctx, row.TenantID, row.SubjectID, period, now)
		if err != nil {
			return restoreValues{}, err
		}
		failOpen := int64(0)
		if s.FailOpen != nil {
			failOpen = s.FailOpen.UserWindow(row.TenantID, row.SubjectID, period, now)
		}
		podUsage := int64(0)
		if s.PodUsage != nil {
			podUsage = s.PodUsage.UserWindow(ctx, row.TenantID, row.SubjectID, period, now)
		}
		seed := maxInt64(maxInt64(row.TokenTotal, failOpen), podUsage)
		written, err := s.Restorer.RestoreUserWindow(ctx, row.TenantID, row.SubjectID, period, now, seed)
		return restoreValues{live: live, failOpen: failOpen, podUsage: podUsage, written: written}, err
	case ScopeTenant:
		live, err := s.Reader.TenantRollupUsage(ctx, row.TenantID, period, now)
		if err != nil {
			return restoreValues{}, err
		}
		failOpen := int64(0)
		if s.FailOpen != nil {
			failOpen = s.FailOpen.TenantRollup(row.TenantID, period, now)
		}
		podUsage := int64(0)
		if s.PodUsage != nil {
			podUsage = s.PodUsage.TenantRollup(ctx, row.TenantID, period, now)
		}
		seed := maxInt64(maxInt64(row.TokenTotal, failOpen), podUsage)
		written, err := s.Restorer.RestoreTenantRollupWindow(ctx, row.TenantID, period, now, seed)
		return restoreValues{live: live, failOpen: failOpen, podUsage: podUsage, written: written}, err
	default:
		return restoreValues{}, nil
	}
}

// maxInt64 returns the larger of a and b.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

func (s *Service) inc(outcome string) {
	if s.Metrics != nil {
		s.Metrics.IncQuotaCheckpointReconcile(outcome)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf == nil {
		return
	}
	s.Logf(format, args...)
}
