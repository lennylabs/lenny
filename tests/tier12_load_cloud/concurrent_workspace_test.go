// SPDX-License-Identifier: MIT

//go:build load_cloud

// concurrent-workspace cloud-load tests. §5.2's session mode with
// sessionPolicy.maxConcurrentSessions > 1 multiplexes multiple
// sessions onto one pod, each session bound to its own materialized
// workspace slot. The runtime fixture
// (agent-workload-load.yaml.tmpl::load-cworkspace-template) declares
// executionMode: session; the per-pod slot count
// (maxConcurrentSessions) and the acknowledgeProcessLevelIsolation
// acknowledgment live in the gateway poolstore per the §4.6.3
// ownership boundary rather than on the CRD template. The scenarios
// stress the §4.6 slot-claim path (the per-slot claim that the
// gateway's SlotClaimer drives when maxConcurrentSessions > 1, vs.
// the one-session-per-pod claim path) and the §5.2 per-slot cleanup
// loop.

package tier12_load_cloud_test

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
// pod density should match the pool's maxConcurrentSessions per pod.
// A failure means slot-claim regressed past the per-claim SLO or the
// §5.2 per-slot cleanup budget exhausted under sustained slot churn.
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
