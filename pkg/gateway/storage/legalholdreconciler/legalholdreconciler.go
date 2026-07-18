// SPDX-License-Identifier: MIT

// Package legalholdreconciler implements the §12.8 line 739 background
// reconciler. The reconciler runs co-located with the §12.5 GC
// goroutine and scans for sessions where `legal_hold = true` and one or
// more retained checkpoints have already been rotated.
//
// A gap is a rotated retained checkpoint: a §12.5 checkpoint-retention
// catalog row whose checkpoint-typed artifact_store row is soft_deleted
// or tombstoned. Chunk rows that belong to a §10.1 checkpoint_manifest
// finalised `partial = true` are excluded, so an aborted, truncated,
// superseded, or quota-refused partial checkpoint does not trip the
// detector — nothing under hold was destroyed when a partial attempt
// was cleaned up. Only rotated chunks of a complete checkpoint
// (`partial = false`) count.
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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
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

// PartialManifestLookup reports the §10.1 finalisation disposition of a
// checkpoint's checkpoint_manifest row. detectGap consults it to exclude
// a rotated chunk row that belongs to a manifest finalised
// `partial = true` (an aborted, truncated, superseded, or quota-refused
// attempt) from the gap count: cleaning up such an attempt destroyed
// nothing under hold. A manifest that finalised `partial = false` is a
// complete, retained checkpoint, so its rotated chunk rows still count.
// partialmanifeststore.Store satisfies this via Get, which returns the
// row for (tenant, checkpoint_id) even after it has been soft-deleted.
//
// spec: §12.8 line 739.
type PartialManifestLookup interface {
	Get(ctx context.Context, tenantID, checkpointID string) (partialmanifeststore.Record, error)
}

// Reconciler drives the §12.8 line 739 legal-hold checkpoint-gap
// detection on a periodic cadence. Construct with New.
type Reconciler struct {
	catalog   artifactcatalog.Store
	audit     AuditAppender
	metrics   MetricsSink
	manifests PartialManifestLookup
	interval  time.Duration
	clock     func() time.Time
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
// catalog and audit must be non-nil; metrics is optional. manifests
// resolves the §10.1 checkpoint_manifest disposition of each rotated
// chunk row so that partial-attempt chunks are excluded from the gap
// count; production wires the Postgres store. When manifests is nil the
// reconciler cannot confirm any rotated chunk as a partial attempt and
// counts every rotated checkpoint row, matching the pre-exclusion
// behavior.
//
// spec: §12.8 line 739.
func New(catalog artifactcatalog.Store, audit AuditAppender, metrics MetricsSink, manifests PartialManifestLookup, opts Options) *Reconciler {
	r := &Reconciler{
		catalog:    catalog,
		audit:      audit,
		metrics:    metrics,
		manifests:  manifests,
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
// any retained checkpoint has been rotated (soft_deleted or
// tombstoned). The gap unit is one retained checkpoint (one §12.5
// retention-catalog row), so the many chunk rows of a single chunked
// checkpoint (all ArtifactType=checkpoint, part_id
// "{checkpoint_id}/chunk-{n}") collapse onto their shared checkpoint_id
// and count once. A rotated checkpoint only counts when it is complete:
// chunk rows of a §10.1 checkpoint_manifest finalised `partial = true`
// are excluded because cleaning up an aborted, truncated, superseded, or
// quota-refused partial attempt destroyed nothing under hold. The
// returned (live, rotated) counts are distinct-checkpoint counts that
// feed the audit payload so the compliance team can size the impact at a
// glance. A session with every checkpoint live and at least one entry is
// gap-free.
//
// spec: §12.8 line 739.
func (r *Reconciler) detectGap(ctx context.Context, ref artifactcatalog.SessionRef) (bool, checkpointSummary, error) {
	rows, err := r.catalog.ListBySession(ctx, ref.TenantID, ref.SessionID)
	if err != nil {
		return false, checkpointSummary{}, fmt.Errorf("legalholdreconciler: list session %s/%s: %w", ref.TenantID, ref.SessionID, err)
	}
	live := map[string]struct{}{}
	rotated := map[string]struct{}{}
	for _, row := range rows {
		if row.ArtifactType != artifactcatalog.ArtifactTypeCheckpoint {
			continue
		}
		key := checkpointGroupKey(row.PartID)
		switch row.State {
		case artifactcatalog.StateLive:
			live[key] = struct{}{}
		case artifactcatalog.StateSoftDeleted, artifactcatalog.StateTombstoned:
			partial, err := r.rowBelongsToPartialManifest(ctx, ref.TenantID, row)
			if err != nil {
				return false, checkpointSummary{}, err
			}
			if partial {
				continue
			}
			rotated[key] = struct{}{}
		}
	}
	s := checkpointSummary{live: len(live), rotated: len(rotated)}
	return s.rotated > 0, s, nil
}

type checkpointSummary struct {
	live    int
	rotated int
}

// rowBelongsToPartialManifest reports whether the rotated checkpoint row
// is a chunk of a §10.1 checkpoint_manifest finalised `partial = true`.
// The chunk object key is "{checkpoint_id}/chunk-{n}.{enc}", so the
// checkpoint_id is the row's leading part_id segment; the manifest's
// Partial flag settles the disposition. A complete checkpoint finalises
// `partial = false`, so a false result marks a genuine retained-checkpoint
// gap. When the manifest row is absent (already hard-pruned, or a row
// carrying no checkpoint segment) the reconciler cannot confirm the chunk
// as a partial attempt and counts it, failing toward detection so a
// possible gap is never silently suppressed.
//
// spec: §12.8 line 739; §10.1 line 141.
func (r *Reconciler) rowBelongsToPartialManifest(ctx context.Context, tenantID string, row artifactcatalog.Record) (bool, error) {
	if r.manifests == nil {
		return false, nil
	}
	checkpointID := checkpointIDFromPartID(row.PartID)
	if checkpointID == "" {
		return false, nil
	}
	m, err := r.manifests.Get(ctx, tenantID, checkpointID)
	if errors.Is(err, partialmanifeststore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("legalholdreconciler: lookup manifest %s/%s: %w", tenantID, checkpointID, err)
	}
	return m.Partial, nil
}

// checkpointGroupKey returns the identity that a checkpoint's
// artifact_store rows share for de-duplication. Chunk objects keyed
// "{checkpoint_id}/chunk-{n}.{enc}" collapse onto their checkpoint_id, so
// the many chunk rows of one checkpoint count as a single retained
// checkpoint. A row that carries no checkpoint segment groups under its
// full part_id so two distinct unlinkable checkpoints still count
// separately rather than collapsing onto an empty key.
//
// spec: §10.1 line 141.
func checkpointGroupKey(partID string) string {
	if id := checkpointIDFromPartID(partID); id != "" {
		return id
	}
	return partID
}

// checkpointIDFromPartID returns the §10.1 checkpoint_id segment of a
// checkpoint artifact_store row's part_id. Chunk objects are keyed
// "{checkpoint_id}/chunk-{n}.{enc}", so the id is the segment before the
// first slash. Returns "" when the part_id carries no checkpoint segment.
func checkpointIDFromPartID(partID string) string {
	if i := strings.IndexByte(partID, '/'); i >= 0 {
		return partID[:i]
	}
	return ""
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
