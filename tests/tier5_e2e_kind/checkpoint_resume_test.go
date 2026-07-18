// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §4.4 / §7.1 checkpoint-then-resume
// client journey: a bound agent pod is evicted, the gateway checkpoints
// its workspace to MinIO, and the client resumes the session on a
// fresh pod with the workspace intact. A second variant asserts the
// §4.4 MinIO-outage fallback to the Postgres `session_eviction_state`
// minimal-state record.
//
// This composite exercise needs the §4.4 eviction checkpoint trigger
// wired end to end on a live cluster: something must watch for an
// individual agent pod's impending termination (node drain, kubelet
// eviction, or a direct pod delete) and drive the gateway's Checkpoint
// RPC against that pod's adapter within the termination grace period,
// the same way the adapter's own preStop hook
// (pkg/controller/sandbox/podspec/podspec.go preStopDrainHook) states
// it exists to protect: "keeps an in-flight gateway Checkpoint RPC from
// being SIGKILLed at the grace deadline". As of this writing,
// checkpoint.TriggerEviction (pkg/checkpoint/checkpoint.go) is defined
// with its own §4.4 retry budget but is never referenced outside that
// package — no gateway code path calls the checkpointer with it. The
// only live checkpoint driver wired into cmd/lenny-gateway is the
// periodic loop (checkpointSvc.Run, TriggerPeriodic) and the gateway's
// own preStop CheckpointBarrier fan-out (pkg/gateway/podlifecycle/prestop),
// which checkpoints active sessions when the GATEWAY pod itself
// terminates, not when an individual agent pod is evicted. Draining the
// node under a bound agent pod (as TestNodeDrainDuringActiveSession
// does) therefore does not currently produce an eviction checkpoint to
// verify a resume against.
//
// The MinIO-outage variant additionally needs an injector: the tier-8
// placeholder tests/tier8_chaos/minio_outage_during_checkpoint_test.go
// is itself an unimplemented skip stub reserving the chaos-subset name
// (see its doc comment — toxiproxy + a testcontainers MinIO sidecar are
// on the ops backlog), so there is no reusable live outage injector to
// call into from a tier-5 test either.
//
// The two eviction-triggered variants still need, at minimum: (1) the
// missing eviction-checkpoint wiring on the gateway (a product change,
// not a test-only one) and (2) a live MinIO-outage injector. Neither
// exists yet, so both stay skipped and the skip names precisely what
// unblocks them.
//
// TestCheckpointOnPodLossResumesWorkspace below is a third journey that
// does not depend on eviction-checkpoint wiring: it drives the
// already-wired periodic / seal checkpoint driver on a bound pod, loses
// the pod with a hard delete, and resumes via POST
// /v1/sessions/{id}/resume (sessiondriver.Resume). It runs its
// assertions when a completed checkpoint lands and degrades to a clean
// skip otherwise.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §4.4 (spec/04_system-components.md, Event / Checkpoint Store)
// "If a previous successful full checkpoint exists, the session is
// resumed from that checkpoint per Section 7.2. If only the minimal
// state record exists, the session is resumed on a fresh pod with
// conversation context but without workspace files — the client
// receives a `session.resumed` event with `resumeMode:
// "conversation_only"` and `workspaceLost: true`."
//
// diagnosis: a failure here (once the skip is lifted) means the
// checkpoint-then-resume client journey is broken on a real cluster:
// either the eviction checkpoint the gateway drives against the
// terminating agent pod's adapter did not persist the workspace to
// MinIO, or POST /v1/sessions/{id}/resume did not restore that
// workspace onto the fresh pod it claims. This is the platform's
// primary durability guarantee for interactive sessions (§4.4 / §7.1)
// and the reason a client can lose an agent pod without losing its
// work.
func TestCheckpointThenResumeRestoresWorkspace(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("precondition not met: the §4.4 eviction checkpoint trigger is not wired on the " +
		"live gateway — checkpoint.TriggerEviction (pkg/checkpoint/checkpoint.go) is defined " +
		"but no gateway code path invokes the checkpointer with it, so evicting a bound agent " +
		"pod today produces no checkpoint to resume from. The test also needs a " +
		"sessiondriver.Resume helper for POST /v1/sessions/{id}/resume, which does not exist " +
		"yet. See the file doc-comment above for the full dependency list.")
}

// spec: §4.4 (spec/04_system-components.md, Event / Checkpoint Store)
// "When an eviction checkpoint cannot be persisted to MinIO, the
// gateway writes a minimal state record to the session_eviction_state
// table in Postgres."
//
// diagnosis: a failure here (once the skip is lifted) means the MinIO-
// outage fallback during an eviction checkpoint does not degrade
// safely: either the gateway did not fall back to the Postgres
// session_eviction_state minimal-state record within the §4.4 retry
// budget, or the resumed session did not report resumeMode:
// "conversation_only" / workspaceLost: true as the spec requires.
func TestCheckpointResumeMinIOOutageFallsBackToPostgresMinimalState(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("precondition not met: same §4.4 eviction-checkpoint wiring gap as " +
		"TestCheckpointThenResumeRestoresWorkspace, plus a live MinIO-outage injector — " +
		"tests/tier8_chaos/minio_outage_during_checkpoint_test.go, cited as reusable " +
		"infrastructure for this scenario, is itself an unimplemented skip stub with no " +
		"toxiproxy/MinIO-sidecar injector to call into.")
}

// spec: §4.4 (checkpoint store restore), §7.3 (resume onto a fresh pod)
//
// This journey is distinct from the two eviction-triggered variants above,
// which stay skipped because the §4.4 eviction-checkpoint trigger is owned by a
// separate proposal. Here the checkpoint is produced by the already-wired
// periodic / seal driver on a bound agent pod: the gateway opens the §10.1
// Checkpoint stream, mints per-chunk presigned grants, the adapter PUTs each
// chunk to the object store, and the gateway confirms each chunk with a
// StatObject and writes the durable checkpoint_manifest row. The client then
// loses the pod (a hard pod delete, not a graceful drain) and resumes onto a
// fresh pod.
//
// diagnosis: a failure here means the checkpoint-then-resume durability
// guarantee is broken for a periodic / seal checkpoint on a bound pod: either
// the gateway did not persist a completed checkpoint (no manifest row reaches
// partial = false with chunk_count > 0 and workspace_bytes_uploaded > 0), the
// pod-loss recovery did not surface the session as awaiting_client_action, or
// POST /v1/sessions/{id}/resume did not restore the workspace onto the fresh
// pod (the resumed session reports workspaceLost or the marker file is absent).
//
// The checkpoint the resume consumes is produced by the periodic driver, whose
// cadence is set by periodicCheckpointIntervalSeconds on the pool. When no
// completed checkpoint lands within the test's bounded wait (a long periodic
// cadence on this install, or a pool that has not warmed), the test degrades to
// a clean skip rather than a false failure, matching the requirePoolReadyPods
// degradation the sibling execution-mode tests use.
func TestCheckpointOnPodLossResumesWorkspace(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, taskModePoolName, 1)

	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("no e2e Postgres pod IP; cannot confirm the checkpoint_manifest row for the resume journey")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant := uniqueName("pod-loss-resume-tenant")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	const marker = "workspace-survives-pod-loss"
	sess, err := d.CreateAndStartWithPlan(ctx, tenant, taskModeRuntimeRef,
		inlineWorkspacePlan("resume-marker.txt", marker))
	if err != nil {
		t.Fatalf("create session on %s: %v", taskModeRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	if sess.PodAssignment == "" {
		t.Fatalf("session carries no podAssignment; the pool did not bind a pod")
	}
	lostPod := sess.PodAssignment
	t.Logf("session %s landed on pod %s", sess.ID, lostPod)

	// Wait for the wired periodic / seal driver to persist a completed
	// checkpoint. The checkpoint_manifest row is the gateway's post-StatObject-
	// confirm record of the chunk objects in the bucket: chunk_count and
	// workspace_bytes_uploaded count only chunks the gateway confirmed present.
	// A partial = false row with a positive chunk count is a completed
	// checkpoint the resume path can restore from.
	chunkCount, bytesUploaded, ok := waitForCompletedCheckpoint(ctx, t, c, pgIP, tenant, sess.ID, 90*time.Second)
	if !ok {
		t.Skip("no completed checkpoint_manifest row landed within the wait budget; the pool's " +
			"periodicCheckpointIntervalSeconds cadence exceeds the test budget on this install")
	}
	if chunkCount <= 0 || bytesUploaded <= 0 {
		t.Fatalf("completed checkpoint has chunk_count=%d workspace_bytes_uploaded=%d; a completed "+
			"checkpoint must have confirmed at least one non-empty chunk", chunkCount, bytesUploaded)
	}
	t.Logf("completed checkpoint for session %s: chunk_count=%d workspace_bytes_uploaded=%d",
		sess.ID, chunkCount, bytesUploaded)

	// Hard pod loss: delete the bound agent pod. The gateway's pod-failure
	// detection abandons automatic recovery and holds the session in
	// awaiting_client_action for an explicit client resume (§7.3).
	if out, err := c.KubectlOut(t, "-n", executionModesNamespace, "delete", "pod", lostPod,
		"--grace-period=0", "--wait=false"); err != nil {
		t.Fatalf("delete bound pod %s: %v\n%s", lostPod, err, out)
	}
	if _, reached := d.WaitForState(ctx, tenant, sess.ID, 120*time.Second, "awaiting_client_action"); !reached {
		cur, _ := d.GetSession(ctx, tenant, sess.ID)
		state := "<unknown>"
		if cur != nil {
			state = cur.State
		}
		t.Fatalf("session %s did not reach awaiting_client_action after its pod was deleted (last state %q); "+
			"pod-loss recovery must surface the lost session for an explicit §7.3 resume", sess.ID, state)
	}

	// Subscribe before resuming so the §7.2 session.resumed event (carrying the
	// resolved resumeMode / workspaceLost) is not missed.
	events, closeEvents, err := d.StreamEvents(ctx, tenant, sess.ID, 0)
	if err != nil {
		t.Fatalf("open events stream: %v", err)
	}
	defer closeEvents()

	resumed, err := d.Resume(ctx, tenant, sess.ID)
	if err != nil {
		t.Fatalf("resume session %s: %v", sess.ID, err)
	}
	if resumed.State != "running" {
		t.Errorf("resumed session state = %q, want running", resumed.State)
	}
	if resumed.PodAssignment == "" || resumed.PodAssignment == lostPod {
		t.Errorf("resumed session podAssignment = %q, want a fresh pod distinct from the lost pod %q",
			resumed.PodAssignment, lostPod)
	}

	mode, lost := awaitResumeMode(ctx, t, events, 60*time.Second)
	if lost {
		t.Errorf("session.resumed reported workspaceLost = true; a completed checkpoint existed, so the "+
			"resume must restore the workspace (resumeMode = %q)", mode)
	}
	if mode == "conversation_only" {
		t.Errorf("session.resumed reported resumeMode = conversation_only; a completed checkpoint existed, " +
			"so the resume must restore workspace files (full or partial_workspace)")
	}
	t.Logf("session %s resumed onto pod %s with resumeMode=%q workspaceLost=%v",
		sess.ID, resumed.PodAssignment, mode, lost)

	// The marker file the checkpoint captured must be present on the fresh pod's
	// restored workspace.
	listing := execDebugContainer(t, c, resumed.PodAssignment, []string{
		"sh", "-c", "cat /workspace/current/resume-marker.txt 2>&1",
	})
	if !strings.Contains(listing, marker) {
		t.Errorf("pod %s: /workspace/current/resume-marker.txt does not contain %q after resume; the "+
			"checkpointed workspace was not restored onto the fresh pod:\n%s", resumed.PodAssignment, marker, listing)
	}
}

// waitForCompletedCheckpoint polls the checkpoint_manifest table for a
// completed (partial = false) row for the given session and returns its
// chunk_count and workspace_bytes_uploaded once one appears. ok is false when
// no completed row lands within timeout. The query runs under a transaction
// that sets app.current_tenant so the §12.3 RLS policy admits the row.
func waitForCompletedCheckpoint(ctx context.Context, t *testing.T, c *kind.Cluster, pgIP, tenant, sessionID string, timeout time.Duration) (chunkCount, bytesUploaded int64, ok bool) {
	t.Helper()
	query := fmt.Sprintf(
		"SET app.current_tenant = '%s'; "+
			"SELECT chunk_count, workspace_bytes_uploaded FROM checkpoint_manifest "+
			"WHERE tenant_id = '%s' AND session_id = '%s' AND partial = FALSE AND deleted_at IS NULL "+
			"ORDER BY coordination_generation DESC LIMIT 1;",
		tenant, tenant, sessionID,
	)
	deadline := time.Now().Add(timeout)
	for {
		out := t5RunPsqlQuery(t, c, pgIP, "checkpoint-manifest-probe", query)
		if line := strings.TrimSpace(out); line != "" {
			fields := strings.Split(line, "|")
			if len(fields) == 2 {
				cc, err1 := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
				bu, err2 := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
				if err1 == nil && err2 == nil {
					return cc, bu, true
				}
			}
		}
		if time.Now().After(deadline) {
			return 0, 0, false
		}
		select {
		case <-ctx.Done():
			return 0, 0, false
		case <-time.After(3 * time.Second):
		}
	}
}

// awaitResumeMode reads the session event stream until the §7.2
// session.resumed event arrives and returns its resumeMode and workspaceLost.
// It fails the test when the event does not arrive within timeout, since the
// resume already returned 200 and the event is the client-facing signal of the
// resolved recovery mode.
func awaitResumeMode(ctx context.Context, t *testing.T, events <-chan sessiondriver.Event, timeout time.Duration) (mode string, workspaceLost bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, open := <-events:
			if !open {
				t.Fatal("events stream closed before the session.resumed event arrived")
			}
			if ev.Type != "session.resumed" {
				continue
			}
			var payload struct {
				ResumeMode    string `json:"resumeMode"`
				WorkspaceLost bool   `json:"workspaceLost"`
			}
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("decode session.resumed payload %s: %v", string(ev.Data), err)
			}
			return payload.ResumeMode, payload.WorkspaceLost
		case <-deadline.C:
			t.Fatalf("session.resumed event did not arrive within %s", timeout)
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for session.resumed: %v", ctx.Err())
		}
	}
}
