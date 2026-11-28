---
id: APICONV-I19
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0183.S2
---

# A lenny-ops idempotency claim stays in progress after a client disconnect or a handler panic and blocks its key for 24 hours or 7 days

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency › Behavior during Postgres outage**, **Operation-status uncertainty after 503**: an agent retries with the same key after recovery, and the outcomes are the original response, `409 OPERATION_IN_PROGRESS` while the operation still runs, or a fresh execution. The long-running TTL class is 7 days by default.

## Evidence

- `serve` in `pkg/ops/opsidem/opsidem.go` calls `m.store.Fail(r.Context(), …)` after a 5xx and `m.store.Complete(r.Context(), …)` otherwise. When the client disconnects while the handler runs, `r.Context()` is cancelled, the Postgres store's `UPDATE` or `DELETE` fails, the middleware only logs it, and the row stays `in_progress` until `expires_at`.
- `serve` has no deferred release, so a panic in the handler leaves the row `in_progress` in the same way. A `Complete` or `Fail` that fails for any other reason does too.
- A retry with that key then gets `409 OPERATION_IN_PROGRESS` until the row expires: 24 hours, or 7 days for the upgrade and restore routes (`longRunningPaths` in `pkg/ops/opsidem/classify.go`).
- No test in `pkg/ops/opsidem` or `tests/component/opsidem` cancels the request context or panics in the handler.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F7), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

An agent whose connection drops while a mutation runs, which is when it retries, cannot complete the operation under its key for the record's whole lifetime, and the `409` tells it the operation is still running when it has finished or never will. The gateway has the same defect on its final write ([APICONV-I20](APICONV-I20-gateway-idempotency-final-write-uses-the-request-context.md)) and a related one after a crash ([APICONV-I9](APICONV-I9-a-stuck-idempotency-claim-blocks-its-key-for-24-hours.md)).

## Suggested resolution

Make the final `Complete` or `Fail` with `context.WithoutCancel(r.Context())` and a bounded timeout, and release the claim in a deferred function when the handler panics, as the gateway's middleware does. Give a pending claim a lease shorter than the TTL, as APICONV-I9 suggests for the gateway, so that a claim whose final write never lands can be taken over. Add unit tests that a cancelled request context and a panicking handler leave no `in_progress` row.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S2 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
