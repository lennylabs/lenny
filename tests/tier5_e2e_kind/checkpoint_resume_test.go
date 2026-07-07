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
// Building this test needs, at minimum: (1) the missing eviction-
// checkpoint wiring on the gateway (a product change, not a test-only
// one), (2) a sessiondriver.Resume method for POST
// /v1/sessions/{id}/resume, and (3) a live MinIO-outage injector. None
// of these exist yet, so the assertions below are not written; the
// skip names precisely what unblocks them.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
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
