---
id: CAPISES-I2
status: closed
kind: spec-defect
severity: medium
requirements: []
change: 0161.S1
closed: fixed
closed-by: 0161.S1
---

# `POST /v1/sessions/start` skips the pool-drain and dual-store degraded gates that `POST /v1/sessions` runs

## Specification

§15.1, the `POST /v1/admin/pools/{name}/drain` row, states the drain backpressure: while a pool is `draining`, "any new `POST /v1/sessions` (or `create_session` MCP call) that would have selected this pool returns `503 POOL_DRAINING`". It names neither `POST /v1/sessions/start` nor the MCP tool `lenny/create_and_start_session`, which also create a session on a pool. The §15.2.1 error-parity list exercises `POOL_DRAINING` only on `POST /v1/sessions` and `create_session`.

## Evidence

- `pkg/gateway/core/session/sessionserver/create.go`: `createAdmissionGates` refuses a create with `PLATFORM_DEGRADED` when `s.dualStore` reports both stores unavailable, and `validateAndBuildCreateRow` calls `requirePoolNotDraining` before the claim and again after the experiment and PreRoute rewrite.
- `pkg/gateway/core/session/sessionserver/create_and_start.go`: `createAndStartGates` and `buildCreateAndStartRow` call neither gate. `pool_drain_gate.go` and `create.go` are the only sources of `POOL_DRAINING`, and the pod-claim code does not read the pool phase.
- `lenny session new` without `--workspace` creates its session through `lenny/create_and_start_session` from change 0160 (step 0160.S1), so the CLI's default path reaches the handler without these gates. The review of 0160.S1 on 2026-11-19 found the gap.

## Gap

A `user` caller can keep creating sessions on a pool an operator is draining, and, while Postgres and Redis are both unreachable, each create-and-start claims a warm pod and runs setup and launch before it fails and rolls back, where the two-step create refuses up front.

## Suggested resolution

State in §15.1 (the sessions page CAPISES, when it lands) that the drain backpressure and the degraded refusal apply to every session-creating call, including `POST /v1/sessions/start` and `lenny/create_and_start_session`, and add both calls to the §15.2.1 parity list for `POOL_DRAINING`. Make `handleCreateAndStart` run the dual-store gate in `createAndStartGates` and `requirePoolNotDraining` in `buildCreateAndStartRow` at the same points `create.go` runs them, with tests.
