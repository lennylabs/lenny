// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// recordingResolver records the checkpoint_id the resume path asks it to
// reassemble, so a test can pin which manifest row the selector resolved.
type recordingResolver struct {
	got    string
	grants []adapterclient.ChunkGrant
	err    error
}

func (r *recordingResolver) Resolve(_ context.Context, _, _, checkpointID string) ([]adapterclient.ChunkGrant, error) {
	r.got = checkpointID
	return r.grants, r.err
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
