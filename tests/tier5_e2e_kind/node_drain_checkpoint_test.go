// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §4.6 voluntary-disruption protection
// contract: when the node hosting a bound agent pod is drained, the
// preStop hook on that pod triggers a checkpoint via the runtime
// adapter's Checkpoint RPC before the pod is allowed to terminate, and
// the session then resumes on a replacement pod.
//
// TestNodeDrainDuringActiveSession (scaffolds_test.go) already drives a
// live session onto a bound agent pod, drains the host node, and
// asserts the WarmPoolController replenishes the pool to minWarm. That
// covers the replenishment half. This file covers the half that
// distinguishes a graceful drain from a hard pod loss: the preStop
// checkpoint-before-eviction and the subsequent resume.
//
// That half is not exercisable today because the gateway never drives
// the §4.4 eviction-checkpoint trigger on an individual agent pod's
// termination. checkpoint.TriggerEviction (pkg/checkpoint/checkpoint.go)
// is defined with its own §4.4 retry budget, and the agent pod's
// preStop hook (pkg/controller/sandbox/podspec/podspec.go
// preStopDrainHook) drains the adapter so an in-flight gateway
// Checkpoint RPC is not SIGKILLed at the grace deadline — but no
// gateway code path invokes the checkpointer with TriggerEviction, so
// draining the node under a bound agent pod produces no eviction
// checkpoint to assert against. The production CheckpointSink is also
// unwired (adapter.Checkpoint returns codes.Unimplemented,
// pkg/adapter/checkpoint.go) and the production Resume path is unwired
// (adapter.Resume returns codes.Unimplemented, pkg/adapter/resume.go),
// and there is no POST /v1/sessions/{id}/resume driver helper. The
// eviction-checkpoint trigger mechanism is an undecided product-design
// decision (a gateway-side pod-eviction watcher that drives the
// Checkpoint RPC, an adapter self-checkpoint from its own preStop hook,
// or another design), routed through the change-proposal pipeline. Once
// that wiring lands, the skip below lifts and the assertions are
// written; the skip names the exact blocker.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: §4.6 (spec/04_system-components.md, "Disruption protection for
// agent pods") "The primary protection against voluntary disruption
// (node drains, cluster upgrades) is a preStop hook on every agent pod
// that triggers a checkpoint via the runtime adapter's Checkpoint RPC
// before allowing termination. ... For active pods whose sessions have
// been checkpointed (or that have no session at all), the session retry
// mechanism described in Section 7.2 handles resumption on a
// replacement pod."
//
// diagnosis: a failure here (once the skip is lifted) means the §4.6
// voluntary-disruption protection is broken on a real cluster: draining
// the node under a bound agent pod did not drive the preStop Checkpoint
// RPC to persist the workspace before the pod terminated, or the
// session did not resume on the replacement pod the WarmPoolController
// schedules. This is the guarantee that lets a node drain or cluster
// upgrade evict an active session's pod without losing the session's
// work, and it is what distinguishes a graceful drain from a hard pod
// loss.
func TestNodeDrainCheckpointsActiveSession(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("precondition not met: the §4.4 eviction-checkpoint trigger is not wired on the " +
		"live gateway — checkpoint.TriggerEviction (pkg/checkpoint/checkpoint.go) is defined " +
		"but no gateway code path invokes the checkpointer with it, so draining the node under " +
		"a bound agent pod produces no preStop Checkpoint to assert against. The production " +
		"CheckpointSink (pkg/adapter/checkpoint.go) and Resume path (pkg/adapter/resume.go) are " +
		"both Unimplemented, and there is no POST /v1/sessions/{id}/resume driver helper. The " +
		"eviction-checkpoint trigger mechanism is an undecided product-design decision routed " +
		"through the change-proposal pipeline; see the file doc-comment above for the full " +
		"dependency list.")
}
