// SPDX-License-Identifier: MIT

//go:build load

// Tier-7 Phase 6.5 incremental load test (streaming). The §13.17 phase
// gate baselines the §15.1 message round-trip surface as the gateway
// transitions from in-memory to multi-replica Redis-coordinated
// streaming. The PR-cadence smoke form drives the streaming_throughput
// k6 scenario through the e2e Kind gateway and asserts the run
// completed within the smoke health gate.
//
// The companion §12.7 streaming_reconnect scenario lives in
// scaffolds_test.go and stays a t.Skip blocker until the gatewaymetrics
// middleware response-writer wrapper forwards http.Flusher; until that
// lands, the SSE consumption path cannot be load-tested. The §6.5
// streaming_throughput smoke run drives the synchronous message
// round-trip (POST /v1/sessions/{id}/messages), which is the
// inject-and-stream-back path the executor publishes events through —
// it is the §6.5 phase-gate substrate the SSE consumption sits on.

package tier7_load_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// spec: 13.17 (Phase 6.5 — incremental load test, streaming)
// diagnosis: the §13.17 streaming_throughput smoke run regressed or
// failed. The scenario POSTs /v1/sessions/{id}/messages against the
// e2e gateway; a failure means the synchronous executor round-trip
// errored under load or its latency regressed beyond the baseline
// budget. The §6.5 phase gate compares the smoke run against the
// committed baseline JSON; the cloud tier runs the strict §13.29
// regression comparison. Inspect the k6 output for the failing check
// and the gateway logs for the executor error.
func TestStreamingThroughput(t *testing.T) {
	requireCloudLoad(t, "streaming_throughput",
		"500 concurrent streaming sessions with reconnect P95 < 500ms and zero event loss")
	_, baseURL := prepareGateway(t)
	res := load.RunScenario(t, "streaming_throughput", smokeOptions(baseURL, nil))
	assertScenarioRan(t, "streaming_throughput", res)
}
