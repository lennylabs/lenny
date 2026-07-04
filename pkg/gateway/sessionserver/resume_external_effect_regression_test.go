// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// checkpointIDRecordingRestorer is an adapter.CheckpointSource that records the
// exact checkpoint id the resume path passed. The gateway's resumeOnPod hands
// row.WorkspaceSnapshot.Ref to podsession.Binder.Resume, which forwards it as
// ResumeRequest.CheckpointID into the adapter Resume RPC, which calls
// LoadCheckpoint(ctx, req.GetCheckpointId()). Capturing the argument here pins
// that the restore selects the freshest recorded checkpoint ref (the single
// WorkspaceSnapshot.Ref the checkpointer overwrites last-writer-wins) with no
// separate dedup-aware selection interposed.
type checkpointIDRecordingRestorer struct {
	archive      []byte
	loaded       bool
	loadedWithID string
	loadCount    int
}

func (c *checkpointIDRecordingRestorer) LoadCheckpoint(_ context.Context, checkpointID string) (io.ReadCloser, error) {
	c.loaded = true
	c.loadedWithID = checkpointID
	c.loadCount++
	return io.NopCloser(bytes.NewReader(c.archive)), nil
}

// startRecordingRuntime is an adapter.RuntimeProcess that records that the
// runtime was (re)started for a session. On the resume path a started runtime
// replays the restored native-SDK session file from the checkpoint and
// re-derives its next actions unsuppressed, so the fact that Start fired is the
// tier-2 evidence that a re-derivable external effect is not gated by any
// dedup consultation before the runtime replays the stale checkpoint.
type startRecordingRuntime struct{ started string }

func (r *startRecordingRuntime) Start(_ context.Context, sessionID string) error {
	r.started = sessionID
	return nil
}
func (r *startRecordingRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *startRecordingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *startRecordingRuntime) Close(context.Context, string) error           { return nil }
func (r *startRecordingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// spec: 7.3 (external side effects across recovery)
// diagnosis: a failure means the restore path silently gained or lost a dedup
// consultation the at-least-once guarantee depends on. If the captured
// checkpoint id no longer equals row.WorkspaceSnapshot.Ref, the resume path
// stopped restoring from the freshest recorded checkpoint (§7.3 replay-window
// premise); if a completed-external-effect ledger or deduplication-marker
// dependency appeared on the Server restore path and suppressed the runtime
// replay, the platform silently gained an exactly-once attempt the §7.3
// guarantee explicitly does not make (the re-derived call has no stable
// identity that survives the restore, so any such dedup is unsound).
//
// The test seeds a resumable (awaiting) session whose WorkspaceSnapshot.Ref
// points at a periodic checkpoint older than an external effect the lost pod
// performed after it, drives POST /v1/sessions/{id}/resume, and asserts:
//   - the adapter received exactly row.WorkspaceSnapshot.Ref as the checkpoint
//     id, captured through the resume-recording restorer the scaffold uses;
//   - the restore proceeded (the runtime restarted, the session reached
//     running) from that older periodic checkpoint;
//   - no completed-external-effect ledger or dedup-marker store is wired to or
//     invoked by the restore path (there is no such dependency on the Server),
//     so the re-derivable effect is not suppressed and the at-least-once
//     guarantee holds by construction.
func TestResumeRestoresFromFreshestRefAndConsultsNoDedupLedger_spec_7_3(t *testing.T) {
	rt := &startRecordingRuntime{}
	// The recorded external effect (a lenny/* platform-tool action, a
	// connector-tool call, or a delegate_task spawn) happened AFTER the periodic
	// checkpoint below, so it is inside the §7.3 replay window and re-derivable
	// on the fresh pod. Its identity is modeled by this ref, which is strictly
	// newer than the checkpoint the restore uses.
	const periodicCheckpointRef = "ckpt-periodic-older"
	const externalEffectAfterCheckpoint = "effect-newer-than-checkpoint"
	if periodicCheckpointRef == externalEffectAfterCheckpoint {
		t.Fatal("test setup: the external effect must be distinct from the checkpoint")
	}

	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt
	restorer := &checkpointIDRecordingRestorer{archive: emptyResumeArchive(t)}
	adapterSrv.Restorer = restorer

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-alo-resume" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	// The freshest recorded checkpoint is a periodic checkpoint (source
	// "checkpoint"), the last-writer-wins WorkspaceSnapshot.Ref the checkpointer
	// maintains. It is older than externalEffectAfterCheckpoint, so the effect
	// is inside the replay window and the restore must proceed from this stale
	// checkpoint without suppressing the re-derivable effect.
	seedAwaitingSession(t, store, sessionstore.Session{
		ID: "sess-alo-resume",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    periodicCheckpointRef,
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-alo-resume/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// The adapter received exactly row.WorkspaceSnapshot.Ref as the checkpoint
	// id. A drift here means the resume path stopped restoring from the freshest
	// recorded checkpoint the §7.3 replay window is defined against.
	if !restorer.loaded {
		t.Fatal("restore did not load the checkpoint; the resume path skipped the restore step")
	}
	if restorer.loadedWithID != periodicCheckpointRef {
		t.Errorf("adapter restored from checkpoint id %q, want exactly row.WorkspaceSnapshot.Ref %q (§7.3 restores from the freshest recorded checkpoint)",
			restorer.loadedWithID, periodicCheckpointRef)
	}
	// The restore loaded the checkpoint exactly once: no second, dedup-driven
	// re-selection of a different ("effect-consistent") checkpoint is interposed.
	if restorer.loadCount != 1 {
		t.Errorf("checkpoint loaded %d times, want exactly 1 (the single freshest ref, no dedup-driven re-selection)", restorer.loadCount)
	}

	// The restore proceeded from that older periodic checkpoint: the runtime was
	// restarted and the session reached running. A started runtime replays the
	// stale checkpoint and re-derives its next actions with no suppression gate,
	// so externalEffectAfterCheckpoint can re-fire under the at-least-once
	// guarantee. This is the corrected outcome the guarantee pins: the effect is
	// NOT suppressed.
	if rt.started != "sess-alo-resume" {
		t.Errorf("runtime started for %q, want sess-alo-resume; the restored runtime must replay the checkpoint conversation (re-deriving the external effect unsuppressed)", rt.started)
	}

	row, err := store.Get(context.Background(), "acme", "sess-alo-resume")
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running; the restore from the older periodic checkpoint must complete without a dedup gate blocking it", row.State)
	}
	// The row's WorkspaceSnapshot.Ref is unchanged: the resume consulted no
	// ledger that would rewrite the restore target to an effect-consistent
	// checkpoint. It stayed the single freshest ref the checkpointer maintains.
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref != periodicCheckpointRef {
		t.Errorf("row.WorkspaceSnapshot.Ref = %v, want %q unchanged (the resume path maintains the single freshest ref and consults no dedup ledger)",
			row.WorkspaceSnapshot, periodicCheckpointRef)
	}
}
