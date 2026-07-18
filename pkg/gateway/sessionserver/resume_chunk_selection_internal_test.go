// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// recordingResolver records the checkpoint_id the resume path asks it to
// reassemble, so a test can pin which manifest row the selector resolved.
type recordingResolver struct {
	got            string
	grants         []adapterclient.ChunkGrant
	recovered      bool
	manifestReason string
	err            error
}

func (r *recordingResolver) Resolve(_ context.Context, _, _, checkpointID string) (resumechunks.ResolveResult, error) {
	r.got = checkpointID
	return resumechunks.ResolveResult{
		Grants:         r.grants,
		Recovered:      r.recovered,
		ManifestReason: r.manifestReason,
	}, r.err
}

// seedManifest records one manifest row via the intent-row model. When
// partial is false the row is finalised complete; when true it is left as an
// active drain attempt.
func seedManifest(t *testing.T, m *partialmanifeststore.MemoryStore, tenant, session, checkpointID string, gen int64, partial bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := m.Put(ctx, partialmanifeststore.Record{
		TenantID:               tenant,
		CheckpointID:           checkpointID,
		SessionID:              session,
		SlotID:                 partialmanifeststore.SlotDefault,
		CoordinationGeneration: gen,
		ChunkObjectKeyPrefix:   "/" + tenant + "/checkpoints/" + session + "/" + checkpointID + "/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTarGz,
		CheckpointStartedAt:    now,
		CheckpointTimeoutAt:    now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed manifest %s: %v", checkpointID, err)
	}
	if err := m.ConfirmChunk(ctx, tenant, checkpointID, 0, 4096); err != nil {
		t.Fatalf("confirm chunk %s: %v", checkpointID, err)
	}
	if !partial {
		if err := m.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
			t.Fatalf("finalise %s: %v", checkpointID, err)
		}
	}
}

// spec: §10.1 line 154 — the resume path selects the checkpoint to reassemble
// by MAX(coordination_generation) regardless of partial, with
// WorkspaceSnapshot.Ref as a validation input only. A newer partial = true
// drain row at a higher generation than the completed checkpoint the ref
// names must be the row resolved, so the partialRecoveryThresholdFraction
// gate can engage. A ref-first selector (the pre-fix behaviour) would always
// resolve the completed checkpoint and never reach the partial.
//
// diagnosis: if the resolver is asked for the ref (the completed checkpoint)
// instead of the higher-generation drain partial, the selection is ref-first
// and partial recovery is dead.
func TestResolveResumeChunksSelectsHighestGenerationPartial(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_sel"
		fullCk  = "ck_full"
		drainCk = "ck_drain"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, session, fullCk, 5, false) // completed checkpoint, gen 5
	seedManifest(t, mstore, tenant, session, drainCk, 6, true) // newer drain partial, gen 6

	resolver := &recordingResolver{grants: []adapterclient.ChunkGrant{{Index: 0}}}
	s := &Server{
		resumeChunkResolver: resolver,
		checkpointManifests: mstore,
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant,
		// The ref names the completed checkpoint (written only on success).
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fullCk},
	}

	grants := s.resolveResumeChunks(context.Background(), row)
	if resolver.got != drainCk {
		t.Fatalf("resolver asked to reassemble %q, want %q (highest-generation drain partial, not the ref)", resolver.got, drainCk)
	}
	if len(grants) != 1 {
		t.Fatalf("grants = %d, want 1 (the selected partial's chunk set)", len(grants))
	}
}

// spec: §10.1 line 154 — when no active manifest row is selectable (the
// completed checkpoint was rotated out and no drain partial exists), the
// resume falls back to the WorkspaceSnapshot.Ref identifier so a legacy or
// GC-pruned session still resolves its last completed checkpoint.
func TestResolveResumeChunksFallsBackToRefWhenNoActiveRow(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_ref"
		fullCk  = "ck_ref_full"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	resolver := &recordingResolver{grants: []adapterclient.ChunkGrant{{Index: 0}}}
	s := &Server{
		resumeChunkResolver: resolver,
		checkpointManifests: mstore, // empty: no active row for the session
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fullCk},
	}
	_ = s.resolveResumeChunks(context.Background(), row)
	if resolver.got != fullCk {
		t.Fatalf("resolver asked to reassemble %q, want %q (ref fallback)", resolver.got, fullCk)
	}
}

// spec: §10.1 line 155 — the WorkspaceSnapshot.Ref is kept in the four-segment
// object-path form (/{tenant}/checkpoints/{session}/{checkpoint_id}). When no
// active manifest row is selectable and the resume falls back to the ref, the
// ref must be normalized to its checkpoint_id (the last segment) before it is
// handed to the checkpoint_id-keyed resolver, matching the workspace-download
// and derive paths. Passing the whole four-segment path as the checkpoint_id
// resolves nothing.
//
// diagnosis: a derived session carries the four-segment ref form; if the
// fallback branch forwards the raw path to Resolve, the checkpoint_id lookup
// misses and the documented last-completed-checkpoint fallback restores
// nothing.
func TestResolveResumeChunksNormalizesFourSegmentRefOnFallback(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_derived"
		checkID = "ck_derived"
	)
	fourSegmentRef := "/" + tenant + "/checkpoints/" + session + "/" + checkID
	mstore := partialmanifeststore.NewMemoryStore(nil)
	resolver := &recordingResolver{grants: []adapterclient.ChunkGrant{{Index: 0}}}
	s := &Server{
		resumeChunkResolver: resolver,
		checkpointManifests: mstore, // empty: no active row, forces the ref fallback
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fourSegmentRef},
	}
	_ = s.resolveResumeChunks(context.Background(), row)
	if resolver.got != checkID {
		t.Fatalf("resolver asked to reassemble %q, want %q (normalized checkpoint_id, not the four-segment path)", resolver.got, checkID)
	}
}

// spec: §10.1 lines 154-155 — reassembly is manifest-driven and ref-independent.
// A partial = true drain whose session never completed a full checkpoint writes
// no WorkspaceSnapshot.Ref, yet LatestActiveAny still selects that partial row
// and its NULL-baseline threshold is 0, so the resume must reassemble it. A
// gate on a non-empty ref would short-circuit that partial-only session to zero
// chunks even though classifyResume reports partial_workspace for it.
//
// diagnosis: if resolveResumeChunks returns nil whenever the ref is empty, a
// session whose only surviving checkpoint is a partial drain is handed no
// chunks and restores nothing while the client still receives a
// partial_workspace event.
func TestResolveResumeChunksReassemblesPartialWithNoRef(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_partial_only"
		drainCk = "ck_partial_only"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, session, drainCk, 3, true) // partial drain, no prior full

	resolver := &recordingResolver{grants: []adapterclient.ChunkGrant{{Index: 0}}}
	s := &Server{
		resumeChunkResolver: resolver,
		checkpointManifests: mstore,
	}
	// No completed checkpoint: WorkspaceSnapshot carries an empty ref.
	row := sessionstore.Session{
		ID: session, TenantID: tenant,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: ""},
	}

	grants := s.resolveResumeChunks(context.Background(), row)
	if resolver.got != drainCk {
		t.Fatalf("resolver asked to reassemble %q, want %q (the ref-independent partial drain)", resolver.got, drainCk)
	}
	if len(grants) != 1 {
		t.Fatalf("grants = %d, want 1 (the partial drain's chunk set)", len(grants))
	}
}

// capturingRecoveryMetrics records every lenny_checkpoint_partial_total
// emission so a test can assert the resume path fires it exactly once, with
// recovered = true and a trigger inside the closed checkpoint.Trigger enum.
type capturingRecoveryMetrics struct {
	calls []recoveryEmit
}

type recoveryEmit struct {
	pool           string
	recovered      bool
	manifestReason string
	trigger        checkpoint.Trigger
}

func (c *capturingRecoveryMetrics) IncCheckpointPartial(pool string, recovered bool, manifestReason string, trigger checkpoint.Trigger) {
	c.calls = append(c.calls, recoveryEmit{pool, recovered, manifestReason, trigger})
}

// spec: §16.1 line 195 — a resume that reassembles an above-threshold partial
// checkpoint stamps lenny_checkpoint_partial_total{recovered=true} exactly
// once, carrying the session's pool, the recovered row's manifest_reason, and
// a trigger inside the closed checkpoint.Trigger enum. The §10.1 manifest
// column set does not persist the originating trigger, so the resume stamps
// eviction as the enum-valid representative (the preStop drain is the recovery
// mechanism this counter primarily serves, §10.1 line 172). A complete
// (partial = false) restore emits nothing.
//
// diagnosis: a missing emission means the resume-side recovery signal is
// unwired and operators cannot compute a partial-checkpoint recovery rate; an
// emission on a complete restore mislabels an ordinary full restore as a
// partial recovery; a trigger outside the closed enum would be a value no
// IsValid gate admits.
func TestResolveResumeChunksEmitsRecoveredOnAboveThresholdPartial(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_recovered"
		drainCk = "ck_recovered"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, session, drainCk, 4, true)

	metrics := &capturingRecoveryMetrics{}
	resolver := &recordingResolver{
		grants:         []adapterclient.ChunkGrant{{Index: 0}},
		recovered:      true,
		manifestReason: partialmanifeststore.ReasonTimeout,
	}
	s := &Server{
		resumeChunkResolver:       resolver,
		checkpointManifests:       mstore,
		checkpointRecoveryMetrics: metrics,
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant, PoolRef: "warm-a",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: ""},
	}

	_ = s.resolveResumeChunks(context.Background(), row)
	if len(metrics.calls) != 1 {
		t.Fatalf("recovery emissions = %d, want 1", len(metrics.calls))
	}
	got := metrics.calls[0]
	if !got.recovered {
		t.Errorf("recovered = false, want true")
	}
	if got.pool != "warm-a" {
		t.Errorf("pool = %q, want %q", got.pool, "warm-a")
	}
	if got.manifestReason != partialmanifeststore.ReasonTimeout {
		t.Errorf("manifest_reason = %q, want %q", got.manifestReason, partialmanifeststore.ReasonTimeout)
	}
	if !got.trigger.IsValid() {
		t.Errorf("trigger = %q, want a member of the closed checkpoint.Trigger enum", got.trigger)
	}
	// spec: §16.1 line 195 — the resume stamps eviction as the enum-valid
	// representative because the §10.1 manifest column set does not persist the
	// originating trigger, and the preStop drain (§10.1 line 172) is the
	// recovery mechanism this counter primarily serves.
	if got.trigger != checkpoint.TriggerEviction {
		t.Errorf("trigger = %q, want %q (the enum-valid representative the resume stamps)",
			got.trigger, checkpoint.TriggerEviction)
	}
}

// spec: §16.1 line 195 — the resume-side recovered=true emission fires on ANY
// above-threshold partial the generation selector picks, regardless of the
// trigger the aborting attempt was started with. finaliseAbort retains every
// manifest_reason = "timeout" row for the resume path, so a partial whose
// origin was a periodic or pre_scale_down deadline fire (not an eviction
// drain) is reassembled and recovered the same as an eviction partial. The
// origin trigger is not persisted in the manifest, so the emission carries the
// eviction representative in every case; the counter-level recovery signal is
// aggregated across triggers and stays correct.
//
// diagnosis: were the resume-side emission gated on the partial having an
// eviction origin (a value the manifest cannot carry), a periodic- or
// pre_scale_down-originated recovery would fire no recovered=true counter at
// all and the aggregate partial-recovery rate would under-count every
// non-eviction-drain recovery.
func TestResolveResumeChunksRecoversNonEvictionOriginPartial(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_periodic_origin"
		drainCk = "ck_periodic_origin"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, session, drainCk, 3, true)

	metrics := &capturingRecoveryMetrics{}
	// The resolver reports a recovered above-threshold partial that carries the
	// timeout reason a periodic or pre_scale_down deadline fire finalises with;
	// the manifest never records which trigger produced it.
	resolver := &recordingResolver{
		grants:         []adapterclient.ChunkGrant{{Index: 0}},
		recovered:      true,
		manifestReason: partialmanifeststore.ReasonTimeout,
	}
	s := &Server{
		resumeChunkResolver:       resolver,
		checkpointManifests:       mstore,
		checkpointRecoveryMetrics: metrics,
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant, PoolRef: "warm-b",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: ""},
	}

	_ = s.resolveResumeChunks(context.Background(), row)
	if len(metrics.calls) != 1 {
		t.Fatalf("recovery emissions = %d, want 1 (a non-eviction-origin partial is still recovered)", len(metrics.calls))
	}
	got := metrics.calls[0]
	if !got.recovered {
		t.Errorf("recovered = false, want true (the reassembled partial is a recovery regardless of origin)")
	}
	if got.trigger != checkpoint.TriggerEviction {
		t.Errorf("trigger = %q, want %q (the enum-valid representative stamped when the origin is not persisted)",
			got.trigger, checkpoint.TriggerEviction)
	}
}

// spec: §16.1 line 195 — a resume that restores a complete (partial = false)
// checkpoint is not a partial recovery, so the resume emits no recovered=true
// counter.
func TestResolveResumeChunksEmitsNothingOnCompleteRestore(t *testing.T) {
	const (
		tenant  = "acme"
		session = "sess_complete"
		fullCk  = "ck_complete"
	)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, session, fullCk, 2, false)

	metrics := &capturingRecoveryMetrics{}
	resolver := &recordingResolver{
		grants:    []adapterclient.ChunkGrant{{Index: 0}},
		recovered: false,
	}
	s := &Server{
		resumeChunkResolver:       resolver,
		checkpointManifests:       mstore,
		checkpointRecoveryMetrics: metrics,
	}
	row := sessionstore.Session{
		ID: session, TenantID: tenant, PoolRef: "warm-a",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: fullCk},
	}

	_ = s.resolveResumeChunks(context.Background(), row)
	if len(metrics.calls) != 0 {
		t.Fatalf("recovery emissions = %d, want 0 (a complete restore is not a recovery)", len(metrics.calls))
	}
}
