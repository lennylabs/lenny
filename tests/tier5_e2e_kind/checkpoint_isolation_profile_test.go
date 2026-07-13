// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the §4.4 checkpoint-quiescence-by-level
// claims that are specific to the isolation profile the agent pod runs
// under (runc, gVisor, Kata). Two behaviours are in scope:
//
//   - Positive (spec line 241): the cooperative
//     checkpoint_request/checkpoint_ready handshake via the lifecycle
//     channel is the only mechanism that produces a *consistent*
//     checkpoint under a sandboxed isolation profile. A Full-level agent
//     pod running under the gVisor RuntimeClass, checkpointed through the
//     lifecycle channel, must produce a checkpoint record tagged
//     consistency: consistent.
//   - Negative (spec line 243): signal-based (SIGSTOP/SIGCONT)
//     checkpointing is "not supported under gVisor or Kata". A pod that
//     would take the embedded-adapter SIGSTOP path under a sandboxed
//     profile must be refused rather than silently produce an
//     inconsistent snapshot.
//
// The gVisor leg is exercisable on Kind: tests/testinfra/kind/install-
// gvisor.sh installs runsc + the containerd-shim onto the sandbox-gvisor
// worker (the same node TestGvisorIsolation runs against). The Kata leg
// is NOT exercisable on Kind — Kata requires hardware virtualization the
// Kind substrate cannot nest (spec/17_deployment-topology.md "Local
// isolation fidelity": the microvm profile degrades to runc locally) —
// so the Kata variant belongs to tier6 cloud on a Kata-capable node pool
// (the parity-matrix.yaml kata_isolation row).
//
// Both assertions are blocked on product observables that do not yet
// exist, independently of the eviction-checkpoint / resume wiring gap
// that tests/tier5_e2e_kind/checkpoint_resume_test.go already documents:
//
//   1. No checkpoint record or event carries a `consistency` field. The
//      §4.4 consistency tag is computed by the pure helper
//      checkpoint.ConsistencyForLevel (pkg/checkpoint/checkpoint.go), but
//      that helper has no non-test caller: the gateway checkpointer
//      (pkg/gateway/checkpoint/checkpointer/checkpointer.go) stores only
//      {Ref, Source, Timestamp, Bytes} on the session's WorkspaceSnapshot
//      and the session_checkpoint_meta table (migration 0148) carries no
//      consistency column, so there is nothing to assert consistency:
//      consistent against on a live cluster.
//   2. No product code refuses the embedded SIGSTOP path under a
//      sandboxed profile. pkg/adapter/embeddedcheckpoint gates only on
//      host platform (Linux vs. ErrNotSupported elsewhere), not on the
//      pod's isolation profile, and no admission rule rejects a Runtime
//      whose deploymentModel is embedded together with an isolationProfile
//      of sandboxed/microvm, so the negative has no enforcement point to
//      verify.
//
// Both are product gaps rather than test-infrastructure gaps; the skips
// below name exactly what unblocks each assertion.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: §4.4 (spec/04_system-components.md, "Checkpoint quiescence
// strategy by level", Full-level runtimes) — "Cooperative
// checkpoint_request/checkpoint_ready handshake via the lifecycle
// channel ... is the only mechanism that produces consistent checkpoints
// under all isolation profiles (runc, gVisor, Kata)."
//
// diagnosis: a failure here (once the skip is lifted) means the
// cooperative lifecycle-channel handshake does not produce a consistent
// checkpoint when the agent pod runs under a real gVisor sandbox: either
// the quiescence handshake did not complete against a runsc-sandboxed
// pod, or the resulting checkpoint record was not tagged consistency:
// consistent. This is the §4.4 guarantee that the sandboxed isolation
// profile — the default production profile (§5.3) — does not degrade the
// checkpoint-consistency contract that runc pods get.
func TestCheckpointHandshakeUnderGvisorTagsConsistent(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("precondition not met: no checkpoint record or event carries a §4.4 consistency tag. " +
		"checkpoint.ConsistencyForLevel (pkg/checkpoint/checkpoint.go) computes consistent/best-effort " +
		"but has no non-test caller — the gateway checkpointer stores only {Ref, Source, Timestamp, " +
		"Bytes} on the session WorkspaceSnapshot and session_checkpoint_meta (migration 0148) has no " +
		"consistency column, so there is no observable to assert consistency: consistent against under " +
		"gVisor. Wiring the level-derived consistency tag onto the persisted checkpoint record unblocks " +
		"this test. See the file doc-comment for the full dependency list.")
}

// spec: §4.4 (spec/04_system-components.md, "Checkpoint quiescence
// strategy by level", Embedded adapter mode only) — "This is not
// supported under gVisor or Kata — signal-based checkpointing under
// sandboxed runtimes is unsupported; use the lifecycle channel instead."
//
// diagnosis: a failure here (once the skip is lifted) means the
// embedded-adapter SIGSTOP/SIGCONT checkpoint path was allowed to run
// under a sandboxed isolation profile instead of being refused. §4.4
// declares signal-based checkpointing unsupported under gVisor/Kata
// because the frozen-process snapshot gives no quiescence handshake to
// wait for in-flight tool calls to settle; allowing it there would
// silently produce an inconsistent checkpoint under the profile the
// platform defaults to.
func TestSignalCheckpointingRefusedUnderSandboxedProfiles(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("precondition not met: no product code refuses the embedded SIGSTOP path under a sandboxed " +
		"isolation profile. pkg/adapter/embeddedcheckpoint gates only on host platform (Linux vs. " +
		"ErrNotSupported), not on the pod's isolation profile, and no admission rule rejects a Runtime " +
		"whose deploymentModel is embedded combined with isolationProfile sandboxed/microvm, so there is " +
		"no enforcement point to verify the §4.4 refusal against. Adding that refusal (admission " +
		"rejection or a runtime-level guard) unblocks this test.")
}
