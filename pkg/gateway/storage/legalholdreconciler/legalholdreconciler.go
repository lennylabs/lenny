// SPDX-License-Identifier: MIT

// Package legalholdreconciler implements the §12.8 line 739 background
// reconciler. The reconciler runs co-located with the §12.5 GC
// goroutine and scans for sessions where `legal_hold = true` and one or
// more checkpoints have already been rotated (the artifact_store
// table records a soft_deleted or tombstoned row alongside the
// session's live checkpoints).
//
// When a gap is detected the reconciler emits a §16.7
// `legal_hold.checkpoint_gap_detected` critical audit event and
// increments the §12.5 line 321
// `lenny_legal_hold_checkpoint_gaps_total` counter. The reconciler
// does not attempt to recover deleted checkpoints — it provides
// detection and audit trail only.
//
// spec: §12.8 line 739; §12.5 line 321.
package legalholdreconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// DefaultSweepInterval matches the §12.8 line 739 cadence ("every 15
// minutes") and the §12.5 GC sweep cadence.
const DefaultSweepInterval = 15 * time.Minute

// AuditAppender is the §11.7 per-tenant audit hash-chain surface the
// reconciler commits the `legal_hold.checkpoint_gap_detected` event to.
//
// spec: §12.8 line 739.
type AuditAppender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// MetricsSink emits the §12.5 line 321 / §16.5
// `lenny_legal_hold_checkpoint_gaps_total` counter.
//
// spec: §12.8 line 739.
type MetricsSink interface {
	IncLegalHoldCheckpointGap(tenantID string)
}

// Reconciler drives the §12.8 line 739 legal-hold checkpoint-gap
// detection on a periodic cadence. Construct with New.
type Reconciler struct {
	catalog  artifactcatalog.Store
	audit    AuditAppender
	metrics  MetricsSink
	interval time.Duration
	clock    func() time.Time
	// dedupe records the most recently emitted (tenant, session) gap
	// so a chronic condition does not flood the audit chain on every
	// 15-minute sweep. A session that was previously reported with a
	// gap remains in the dedupe map until the next process restart;
	// fresh gaps from new sessions still surface immediately.
	dedupe map[gapKey]time.Time
	// emitWindow bounds how often a single (tenant, session) gap can
	// re-emit. A value of zero emits on every sweep.
	emitWindow time.Duration
}

// gapKey is the dedupe map key.
type gapKey struct{ tenantID, sessionID string }

// Options configures a Reconciler. A zero field selects its default.
type Options struct {
	// Interval overrides DefaultSweepInterval.
	Interval time.Duration
	// Clock overrides time.Now for tests.
	Clock func() time.Time
	// EmitWindow bounds how often a (tenant, session) gap re-emits.
	// A non-positive value selects 24 hours so a chronic gap fires
	// daily rather than every 15 minutes.
	EmitWindow time.Duration
}

// New returns a Reconciler that scans through the given catalog. Both
// catalog and audit must be non-nil; metrics is optional.
//
// spec: §12.8 line 739.
func New(catalog artifactcatalog.Store, audit AuditAppender, metrics MetricsSink, opts Options) *Reconciler {
	r := &Reconciler{
		catalog:    catalog,
		audit:      audit,
		metrics:    metrics,
		interval:   opts.Interval,
		clock:      opts.Clock,
		emitWindow: opts.EmitWindow,
		dedupe:     map[gapKey]time.Time{},
	}
	if r.interval <= 0 {
		r.interval = DefaultSweepInterval
	}
	if r.clock == nil {
		r.clock = func() time.Time { return time.Now().UTC() }
	}
	if r.emitWindow <= 0 {
		r.emitWindow = 24 * time.Hour
	}
	return r
}

// Tick runs one sweep at now and returns the number of (tenant,
// session) pairs the sweep newly identified as carrying a gap and
// emitted an audit event for. A pair whose dedupe window has not
// elapsed is observed but does not emit; it is not counted in the
// returned total.
func (r *Reconciler) Tick(ctx context.Context) (int, error) {
	if r.catalog == nil {
		return 0, nil
	}
	refs, err := r.catalog.SessionsWithLegalHoldAndCheckpoints(ctx)
	if err != nil {
		return 0, fmt.Errorf("legalholdreconciler: list candidate sessions: %w", err)
	}
	emitted := 0
	for _, ref := range refs {
		hasGap, ckpts, err := r.detectGap(ctx, ref)
		if err != nil {
			return emitted, err
		}
		if !hasGap {
			continue
		}
		if !r.shouldEmit(ref) {
			continue
		}
		if err := r.emit(ctx, ref, ckpts); err != nil {
			return emitted, err
		}
		emitted++
	}
	return emitted, nil
}

// detectGap inspects the per-session catalog rows and reports whether
// any checkpoint has been rotated (soft_deleted or tombstoned). The
// returned (live, rotated) counts feed the audit payload so the
// compliance team can size the impact at a glance. A session with
// every checkpoint live and at least one entry is gap-free.
func (r *Reconciler) detectGap(ctx context.Context, ref artifactcatalog.SessionRef) (bool, checkpointSummary, error) {
	rows, err := r.catalog.ListBySession(ctx, ref.TenantID, ref.SessionID)
	if err != nil {
		return false, checkpointSummary{}, fmt.Errorf("legalholdreconciler: list session %s/%s: %w", ref.TenantID, ref.SessionID, err)
	}
	var s checkpointSummary
	for _, row := range rows {
		if row.ArtifactType != artifactcatalog.ArtifactTypeCheckpoint {
			continue
		}
		switch row.State {
		case artifactcatalog.StateLive:
			s.live++
		case artifactcatalog.StateSoftDeleted, artifactcatalog.StateTombstoned:
			s.rotated++
		}
	}
	return s.rotated > 0, s, nil
}

type checkpointSummary struct {
	live    int
	rotated int
}

// shouldEmit reports whether the (tenant, session) pair has not been
// re-emitted within the configured dedupe window.
func (r *Reconciler) shouldEmit(ref artifactcatalog.SessionRef) bool {
	key := gapKey{tenantID: ref.TenantID, sessionID: ref.SessionID}
	last, ok := r.dedupe[key]
	if !ok {
		return true
	}
	return r.clock().Sub(last) >= r.emitWindow
}

// emit commits the §16.7 legal_hold.checkpoint_gap_detected audit row
// and increments the §12.5 line 321 metric counter. The dedupe stamp
// is set after a successful append.
func (r *Reconciler) emit(ctx context.Context, ref artifactcatalog.SessionRef, s checkpointSummary) error {
	payload, err := json.Marshal(map[string]any{
		"tenant_id":           ref.TenantID,
		"session_id":          ref.SessionID,
		"live_checkpoints":    s.live,
		"rotated_checkpoints": s.rotated,
		"detected_at":         r.clock().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("legalholdreconciler: marshal payload: %w", err)
	}
	if _, err := r.audit.Append(ctx, ref.TenantID, string(obsaudit.EventLegalHoldCheckpointGapDetected), payload, r.clock().UTC()); err != nil {
		return fmt.Errorf("legalholdreconciler: append audit: %w", err)
	}
	if r.metrics != nil {
		r.metrics.IncLegalHoldCheckpointGap(ref.TenantID)
	}
	r.dedupe[gapKey{tenantID: ref.TenantID, sessionID: ref.SessionID}] = r.clock()
	return nil
}

// Run drives the reconciler on the configured interval until ctx is
// done. onTick, when non-nil, receives each sweep's emit-count and
// error.
//
// spec: §12.8 line 739.
func (r *Reconciler) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := r.Tick(ctx)
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}
