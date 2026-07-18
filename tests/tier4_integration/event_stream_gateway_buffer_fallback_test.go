//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.5 read-surface Redis-down /
// gateway-up degradation case (degradation case 1): when Redis is
// unreachable but the gateway is up, the SSE/polling read surface must
// fall back to the gateway's in-memory event buffer (§25.3) and serve
// the gateway-originated events (alert_fired, pool_state_changed,
// session_failed, etc.) it holds, labelled with the canonical
// degradation envelope actualSource: "gateway-buffer".
//
// This test is currently a placeholder. The lenny-ops read surface
// (pkg/ops/events.Service, behind GET /v1/admin/events and
// /v1/admin/events/stream) never fetches events from the gateway
// buffer. Its SourceHealth-driven degradation path
// (pkg/ops/events/degradation.go streamState) only relabels which
// source a response is reported as coming from — during a Redis outage
// it attaches an actualSource: "gateway-buffer" envelope while still
// serving exclusively from its own local in-memory ring buffer, which
// only ever holds lenny-ops-originated events (escalations, drift,
// ops self-health), never gateway-originated ones. cmd/lenny-ops's
// sourceHealthProbe likewise only flips the redisUp/gatewayUp booleans
// consumed by that envelope; it never polls the gateway event buffer
// endpoint (GET /v1/admin/events/buffer, §25.3) for events. So during
// a Redis outage a gateway-originated alert emitted into the gateway
// buffer has no path into a lenny-ops poll/SSE response — the
// "gateway-buffer" label is present but the data behind it is not.
//
// Building the cross-process read source this asserts (gateway pod
// discovery via the lenny-gateway-pods headless Service, per-replica
// buffer poll, eventKey-based merge, mid-stream source switching, and
// cross-source cursor translation) is the same unbuilt read-side
// source tracked against the §25.5 operational event stream as a
// design-heavy feature for the proposal/build pipeline. Until that
// consumer exists, the assertion below cannot run against the live
// binaries, so the test skips rather than assert a path the current
// lenny-ops binary cannot reach.
package tier4_integration_test

import "testing"

// spec: §25.5 (spec/25_agent-operability.md, Degradation, "Redis
// unreachable") "SSE, polling, and webhook delivery fall back to the
// gateway's in-memory event buffer (Section 25.3). Responses include
// the canonical `degradation` envelope with `actualSource:
// \"gateway-buffer\"`." + (Polling Delivery, "Source fallback is
// transparent") "When Redis is unreachable, the endpoint serves from
// the gateway event buffer."
//
// diagnosis: a failure means the §25.5 case-1 read-surface fallback is
// broken: during a Redis outage with the gateway up, a poll of GET
// /v1/admin/events returns only lenny-ops-originated events (or none)
// instead of the gateway-originated events resident in the gateway
// event buffer, even though the response is labelled actualSource:
// "gateway-buffer". This test is skipped until the read-side
// gateway-buffer fetch exists to reach that failure mode; see the file
// comment.
func TestOpsEventStreamServesGatewayEventsFromGatewayBufferWhenRedisDown(t *testing.T) {
	t.Skip("the lenny-ops read surface (pkg/ops/events.Service) never fetches gateway-originated " +
		"events from the gateway event buffer during a Redis outage; its Redis-down degradation path " +
		"only attaches an actualSource: \"gateway-buffer\" label while still serving its own local ring " +
		"buffer (lenny-ops-originated events only), so the case-1 gateway-buffer data fallback cannot be " +
		"exercised against the live binaries yet — the cross-process read source is unbuilt")
}
