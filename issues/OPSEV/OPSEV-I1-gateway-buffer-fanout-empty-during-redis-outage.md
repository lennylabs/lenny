---
id: OPSEV-I1
status: open
kind: build-gap
severity: medium
requirements: []
legacy: T-25.5.27
---

# The gateway-buffer fan-out returned no events during a Redis outage on the July cluster

## Specification

§25.5 (Operational Event Stream), **Redis unreachable:** "SSE, polling, and webhook delivery fall back to the gateway's in-memory event buffer (Section 25.3). Responses include the canonical `degradation` envelope with `actualSource: "gateway-buffer"`."

## Evidence

- Recorded on branch `theme-t-bed` as T-25.5.27. On the July Kind cluster, `lenny-ops` switched its read surface to `gateway-buffer` during a Redis outage and returned zero events from either gateway replica, so the degradation label sat on an empty page.
- The test is `TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey` in `tests/e2e_kind/event_buffer_fanout_test.go`. It registers four precondition skips in `tests/registers/skip-reasons.yaml`, so a run that skips proves nothing.
- Since 2026-07-25 the fan-out code in `pkg/ops/events/gatewayfallback.go` and `pkg/ops/gateway/client.go` has changed only through package moves and citation rewrites. T-25.5.24 (the `lenny-ops` service account had no `platform-admin` grant at the gateway admin API) is resolved, so it does not explain the empty page.
- Checked against `main` at `092ef92f6` on 2026-11-19. The finding cannot be confirmed or dismissed without a run.

## Gap

If the finding reproduces, the §25.5 degraded read path labels a response `gateway-buffer` while serving none of the events the gateway buffered. Whether the product behaves this way on the current tree is unconfirmed, and a validation run settles whether this issue stays open.

## Suggested resolution

In the validation pass that runs the e2e_kind tier for M3, run `TestEventBufferFanOutDeduplicatesAcrossReplicasByEventKey` unskipped on a two-replica Kind install. If it fails, keep this issue open, add the run's output to the evidence, and fix the fan-out. If it passes, close this issue as obsolete with the run identifier.
