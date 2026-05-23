// SPDX-License-Identifier: MIT

//go:build load_cloud

// concurrent-stateless mode cloud-load tests. §5.2's `concurrent` +
// `concurrencyStyle: stateless` mode routes multiple sessions
// through one pod with no per-session workspace materialization;
// the gateway dispatches each request through a Service in front
// of the pool. The mode trades isolation for throughput: per-slot
// cost is dominated by request routing rather than workspace
// setup, so the SLO is tighter.

package tier12_load_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

const cstatelessModeRuntime = "load-cstateless-runtime"

// spec: 5.2 + 12.7 (concurrent-stateless dispatch throughput)
// diagnosis: TestCloudConcurrentStatelessDispatch drives sustained
// session creation against the concurrent-stateless pool. The
// gateway routes each session through the pool's Service. A
// failure means stateless dispatch regressed past the
// per-request SLO or the Service-fronted concurrency saturated
// before the §13.1 maxConcurrent per-pod ceiling.
func TestCloudConcurrentStatelessDispatch(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "cstateless_dispatch", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": cstatelessModeRuntime,
	}))
	assertScenarioRan(t, "cstateless_dispatch", res)
}

// spec: 5.2 + 12.7 (concurrent-stateless burst absorbtion)
// diagnosis: TestCloudConcurrentStatelessBurst drives a tight burst
// of session creates against the concurrent-stateless pool, then
// observes the pool's Service-level latency profile. A regression
// here usually points at the §10.1 HPA settling slower than the
// burst or at request-routing contention.
func TestCloudConcurrentStatelessBurst(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "cstateless_burst", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": cstatelessModeRuntime,
	}))
	assertScenarioRan(t, "cstateless_burst", res)
}
