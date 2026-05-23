// SPDX-License-Identifier: MIT

//go:build load_kind

// Tier-7 Phase 9.5 incremental load test (delegation). The §13.21
// phase gate baselines the §8.2 `lenny/delegate_task` MCP tool — the
// production delegation surface — under a rate-bounded executor. The
// PR-cadence smoke form drives the delegation_fanout_mcp k6 scenario
// through the e2e Kind gateway and asserts the run completed within
// the smoke health gate.
//
// The companion §12.7 delegation_fanout scenario (in scaffolds_test.go)
// drives POST /v1/sessions/start directly — the REST surface for the
// child-spawn primitive. The §9.5 phase gate covers the JSON-RPC
// /mcp tools/call path that production callers use; the two scenarios
// exercise the same §8.2 spawn primitive through the two surfaces it
// is reachable from.

package tier7b_load_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// spec: 13.21 (Phase 9.5 — incremental load test, delegation)
// diagnosis: the §13.21 delegation_fanout_mcp smoke run regressed or
// failed. The scenario calls `lenny/delegate_task` through POST /mcp
// against the e2e gateway; a failure means the MCP delegation
// hot path errored under load (a 4xx/5xx transport response, or an
// `isError: true` tool result for which the k6 check fails) or its
// latency regressed beyond the baseline budget. The §9.5 phase gate
// compares the smoke run against the committed baseline JSON. Inspect
// the k6 output for the failing check, the MCP error envelope, and
// the gateway logs for the delegation error.
func TestDelegationFanout(t *testing.T) {
	requireCloudLoad(t, "delegation_fanout_mcp",
		"a single root with N=50 concurrent children spawned through the MCP "+
			"lenny/delegate_task tool, depth=10, completing within the §12.7 30s budget")
	_, baseURL := prepareGateway(t)
	res := load.RunScenario(t, "delegation_fanout_mcp", smokeOptions(baseURL, map[string]string{
		"LENNY_FANOUT": "5",
	}))
	assertScenarioRan(t, "delegation_fanout_mcp", res)
}
