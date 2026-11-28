---
id: APICONV-I17
status: open
kind: build-gap
severity: medium
requirements: []
---

# lenny-ops stores idempotency records for unauthenticated requests under a caller the client names

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: `lenny-ops` keys a record by `(key, caller_id)`, where `caller_id` is the OIDC `sub` claim of the requesting service account. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) **Mechanism.** refuses a keyed request whose principal has no subject with `UNAUTHORIZED`, reason `principal_subject_required`, before the gateway records the key. [§25.5](../../spec/25_agent-operability.md#255-operational-event-stream) sends the `subscription_cache_invalidate` internal RPC to every `lenny-ops` replica over the headless Service.

## Evidence

- `withOpsAuth` in `pkg/ops/opsserver/auth.go` sends every request whose path `isUnauthenticatedPath` accepts (`/healthz`, `/readyz`, `/metrics`, and `/internal/v1/event-subscriptions/cache/invalidate`) to the handler it wraps, whatever the method, without the bearer verifier and without the rate limiter, which wraps only the authenticated chain.
- The handler it wraps is the `opsidem`-wrapped mux: `New` in `pkg/ops/opsserver/opsserver.go` passes `s.idem.Wrap(s.mux)` to `withOpsAuth`. A POST to one of these paths that carries an `Idempotency-Key` header is therefore claimed and stored.
- With no principal on the request, `callerIdentity` in `pkg/ops/opsserver/backup.go` takes `caller_id` from the client's `X-Lenny-Caller` header, and falls back to `operator`.
- `handleCacheInvalidate` in `pkg/ops/opsserver/event_subscriptions.go` answers `401 UNAUTHORIZED` to a wrong token. When no invalidator or token is configured the route is not registered and the mux answers `404`, and a POST to a probe path answers `405`. `opsidem` stores every response below 500, so each of these is stored for the 24-hour window.
- The endpoint is not compared on a match ([APICONV-I16](APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md)), so a record stored under key `K` and caller `C` replays to a later authenticated POST with key `K` from the caller whose subject is `C`, on any path. A live record under another caller makes `Claim` in `pkg/ops/opsidem/pgstore/pgstore.go` answer `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER`, which tells the sender that some caller holds `K`.
- With `ops.ingress.enabled`, the ops Ingress routes these paths from outside the cluster ([LOPSKRES-I1](../LOPSKRES/LOPSKRES-I1-ops-ingress-routes-the-unauthenticated-paths-from-outside-the-cluster.md)).
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F2), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

An unauthenticated client writes rows into `ops_idempotency_keys` at any rate, each kept for 24 hours. A client that knows or guesses a caller's key can store a `401` or `404` that the caller's later request with that key receives in place of its operation, or make the caller's request fail with the owned-by-other `403`. Keys are caller-generated UUIDs, so poisoning or blocking a caller's key needs that key, and filling the table needs nothing.

## Suggested resolution

Apply `opsidem` inside the authenticated chain only, so that a request to an unauthenticated path is never keyed, and refuse a keyed request that reaches `opsidem` with no verified principal, as §11.5 does with `principal_subject_required`. Never take `caller_id` from `X-Lenny-Caller` when an authentication configuration is wired. Do not store a `401`. State in §25.4 that only an authenticated request is keyed. Add unit tests that a keyed POST to each unauthenticated path stores no record, and a security-tier test that the invalidate path is not a write path into `ops_idempotency_keys`.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
