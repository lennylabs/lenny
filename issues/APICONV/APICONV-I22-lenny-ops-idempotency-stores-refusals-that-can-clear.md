---
id: APICONV-I22
status: planned
kind: build-gap
severity: low
requirements: []
change: 0183.S2
---

# lenny-ops idempotency stores refusals whose condition can clear and replays them for the record's lifetime

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: a completed request's response is stored and returned without executing again. Decision 8 of change 0178 states that the gateway does not store a refusal whose condition can clear ([APICONV-I8](APICONV-I8-idempotency-stores-refusals-that-can-clear.md)); §25.4 states no such rule.

## Evidence

- `serve` in `pkg/ops/opsidem/opsidem.go` stores every response below 500.
- Stored refusals include `403 LOCK_SCOPE_FORBIDDEN` (`pkg/ops/opsserver/locks.go`), a tool refusal that `/mcp/management` returns inside an HTTP 200 ([OPSMCP-I1](../OPSMCP/OPSMCP-I1-required-key-management-tools-fail-through-the-mcp-bridge.md)), the `409` lock conflict that `pkg/ops/opsserver/locks.go` maps from `coordination.ErrCodeConflict`, and `409 UPGRADE_NOT_IN_PROGRESS` (`pkg/ops/upgradeservice/codes.go`).
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F9), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

An agent whose scope, lock, or upgrade state changes keeps receiving the stored refusal under its key for 24 hours, or 7 days on the long-running routes, and has to pick a new key to make the operation run.

## Suggested resolution

List in §25.4 the refusals that are not stored, `401` among them, aligned with the gateway's list of decision 8 of change 0178 and extended with the `lenny-ops` codes whose condition can clear, and have `opsidem` release the claim for them. Add a unit test per listed code.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S2 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
