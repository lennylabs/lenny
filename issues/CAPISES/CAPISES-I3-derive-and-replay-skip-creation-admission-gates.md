---
id: CAPISES-I3
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0144.S1
---

# Derive and replay create a session without the admission gates that session creation runs

## Specification

§15.1 (REST API) states the admission gates that precede a session create: the quota, concurrency, and admission-rate gates, the §4.8 policy chain, the §10.6 environment-admission gate, and the pool-drain backpressure of the `POST /v1/admin/pools/{name}/drain` row. §10.1.3 (Dual-Store Unavailability), item 2, refuses a session-creating request with `503` while both stores are unavailable, on the surfaces it lists. Derive (`POST /v1/sessions/{id}/derive`) and replay (`POST /v1/sessions/{id}/replay`) each create a new session on a pool, and neither section states whether those gates apply to them.

## Evidence

- `pkg/gateway/core/session/sessionserver/create.go`, `createAdmissionGates`: runs `requireStoresAvailable`, `requireActiveUser`, `requireCreationAllowed`, `requireSessionQuota`, `requireConcurrencyLimits`, `requireAdmissionRateLimit`, `requirePolicyChain`, and `requireEnvironmentAdmission`; `validateAndBuildCreateRow` runs `requirePoolNotDraining`.
- `pkg/gateway/core/session/sessionserver/derive.go`, `handleDerive`, and `replay.go`, `handleReplay`: run `requireActiveUser`, `requireSessionQuota`, `requirePolicyChain`, `requireSourceOwnerNotRestricted`, and `requirePoolSelectable`, and none of `requireStoresAvailable`, `requireConcurrencyLimits`, `requireAdmissionRateLimit`, `requireEnvironmentAdmission`, or `requirePoolNotDraining`.
- The security review of 0161.S1 on 2026-11-19 found the gap (finding F2). It predates that step.

## Gap

The owner of a terminal session can create new sessions through derive or replay past the per-user and per-runtime concurrency caps and the admission-rate limit, on a pool an operator is draining, in an environment whose admission rules refuse the runtime, and while both stores are unavailable.

## Suggested resolution

State in §15.1 (the sessions page CAPISES, when it lands) which admission gates derive and replay run, and run them in `handleDerive` and `handleReplay` at the points `create.go` runs them, with tests for each refusal.
