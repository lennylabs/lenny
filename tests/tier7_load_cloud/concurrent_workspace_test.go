// SPDX-License-Identifier: MIT

//go:build load_cloud

// concurrent-workspace mode cloud-load tests. §5.2's `concurrent` +
// `concurrencyStyle: workspace` mode multiplexes multiple sessions
// onto one pod, each session bound to its own materialized
// workspace slot. The runtime fixture
// (agent-workload-load.yaml.tmpl::load-cworkspace-template) declares
// maxConcurrent=${LOAD_CWORKSPACE_SLOTS} so each warm pod hosts
// that many concurrent sessions. The scenarios stress the §4.6
// slot-claim path (vs. the session-mode pod-claim path) and the
// §5.2 concurrentWorkspacePolicy cleanup loop.

package tier7_load_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// runtimeRef targeting the concurrent-workspace pool.
const cworkspaceModeRuntime = "load-cworkspace-runtime"

// spec: 5.2 + 12.7 (concurrent-workspace slot claim throughput)
// diagnosis: TestCloudConcurrentWorkspaceSlotClaim drives sustained
// session creation against the concurrent-workspace pool. The
// gateway routes each session to a free slot on an existing warm
// pod (via §4.6 SlotClaimer's CAS) rather than claiming a new pod;
// pod density should match LOAD_CWORKSPACE_SLOTS per pod. A failure
// means slot-claim regressed past the per-claim SLO or the §5.2
// cleanupTimeoutSeconds budget exhausted under sustained slot
// churn.
func TestCloudConcurrentWorkspaceSlotClaim(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "cworkspace_slot_claim", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": cworkspaceModeRuntime,
	}))
	assertScenarioRan(t, "cworkspace_slot_claim", res)
}

// spec: 5.2 + 12.7 (concurrent-workspace fan-out under per-pod density)
// diagnosis: TestCloudConcurrentWorkspaceFanout drives the §8.2
// MCP delegate_task tool with the children landing on the
// concurrent-workspace pool. Each iteration's children compete for
// slots on the same pods, so the scenario exercises slot
// scheduling under burst arrival.
func TestCloudConcurrentWorkspaceFanout(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "cworkspace_fanout_mcp", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": cworkspaceModeRuntime,
	}))
	assertScenarioRan(t, "cworkspace_fanout_mcp", res)
}
