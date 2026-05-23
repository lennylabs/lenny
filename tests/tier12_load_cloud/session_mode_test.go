// SPDX-License-Identifier: MIT

//go:build load_cloud

// Session-mode cloud-load tests. §5.2's `session` mode binds one
// session to one pod for the session's lifetime; the pool releases
// the pod when the session terminates. The four scenarios mirror
// the §12.7 phase-gated targets the smoke tier-7 cannot reach:
// pod-claim latency at production arrival rate, REST + MCP
// delegation fan-out, and streaming throughput.

package tier12_load_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// runtimeRef every session-mode scenario targets. The cloud-load
// runtime fixture (tests/testinfra/k8s/agent-workload-load.yaml.tmpl)
// registers it with the load-session-pool whose warm count is sized
// for the active LENNY_LOAD_SCALE profile.
const sessionModeRuntime = "load-session-runtime"

// spec: 12.7 (pod_claim_latency — production-scale pod claims)
// diagnosis: TestCloudPodClaimLatency drives the §12.7 100-concurrent-
// claims target against a cloud cluster whose warm pool is sized for
// the spec arrival rate. A failure means either the §4.6 claim path
// regressed past the per-percentile SLO, the warm pool replenish
// loop did not keep up, or the cloud cluster is undersized for the
// active LENNY_LOAD_SCALE.
func TestCloudPodClaimLatency(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "session_pod_claim", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": sessionModeRuntime,
	}))
	assertScenarioRan(t, "session_pod_claim", res)
}

// spec: 12.7 (delegation_fanout — REST fan-out under load)
// diagnosis: TestCloudDelegationFanoutLoad drives the §12.7 50-child
// fan-out target through POST /v1/sessions/start. A failure means
// either child-spawn regressed past the per-call SLO or the cloud
// pool exhausted under the per-iteration fan-out.
func TestCloudDelegationFanoutLoad(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "session_delegation_fanout", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": sessionModeRuntime,
	}))
	assertScenarioRan(t, "session_delegation_fanout", res)
}

// spec: 12.7 (delegation_fanout_mcp — MCP fan-out under load)
// diagnosis: TestCloudDelegationFanoutMCP drives the §8.2
// lenny/delegate_task tool fan-out via JSON-RPC. A failure means
// the MCP delegation-spawn cost regressed past the per-call SLO or
// the tool returned an error envelope under the per-iteration
// fan-out.
func TestCloudDelegationFanoutMCP(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "session_delegation_fanout_mcp", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": sessionModeRuntime,
	}))
	assertScenarioRan(t, "session_delegation_fanout_mcp", res)
}

// spec: 12.7 (streaming_throughput — concurrent streaming sessions)
// diagnosis: TestCloudStreamingThroughput drives the §12.7 500-
// concurrent-sessions target through POST /v1/sessions/start +
// /v1/sessions/{id}/messages. A failure means the in-pod adapter
// inject-and-stream-back cost regressed past the per-message SLO
// or the streaming budget exhausted.
func TestCloudStreamingThroughput(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "session_streaming_throughput", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": sessionModeRuntime,
	}))
	assertScenarioRan(t, "session_streaming_throughput", res)
}
