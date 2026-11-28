---
id: APICONV-I23
status: planned
kind: build-gap
severity: low
requirements: []
change: 0183.S2
---

# A lenny-ops idempotency replay drops the original response headers and adds marker headers the specification does not define

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: a found and completed record returns the stored response without executing again. The specification defines no `X-Lenny-Idempotent-Replay` or `X-Lenny-Idempotency-Degraded` header.

## Evidence

- `responseEnvelope` in `pkg/ops/opsidem/pgstore/pgstore.go` stores the status code and the body only.
- On `ClaimReplay`, `serve` in `pkg/ops/opsidem/opsidem.go` sets `Content-Type: application/json` whatever the original type, adds `X-Lenny-Idempotent-Replay: true`, and writes the stored status and body. `Location`, `Retry-After`, and every other header of the original response are lost.
- `degradingWriter` in `pkg/ops/opsidem/writers.go` adds `X-Lenny-Idempotency-Degraded: true` when the store is unavailable.
- The gateway's middleware stores the response headers and replays them (`replayResponse` in `pkg/idempotency/idempotencymw/idempotency.go`).
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F10), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

A replayed response differs from the original in its headers, so a client that reads `Location` or `Retry-After` behaves differently on a retry, and two response headers exist that no client can rely on.

## Suggested resolution

Store the response headers with the record and replay them, as the gateway does. Either specify the two marker headers in §25.4 or remove them, and keep the `degradation.warnings` envelope that §25.4 specifies. Add a unit test that a replay returns the original headers.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S2 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
