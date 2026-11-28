---
id: APICONV-I26
status: planned
kind: build-gap
severity: low
requirements: []
change: 0183.S2
---

# pkg/ops/opsidem duplicates the gateway's idempotency middleware

## Specification

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) Idempotency and [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service, **Idempotency**) describe one mechanism, a stored response keyed by the caller and a client key, that two packages implement.

## Evidence

- `pkg/ops/opsidem` and its Postgres store implement the `lenny-ops` records, and `pkg/idempotency` with `pkg/idempotency/idempotencymw` implements the gateway's. Each fix that aligns `lenny-ops` with the gateway, such as [APICONV-I16](APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md) and APICONV-I17 to APICONV-I25, is written twice while both exist.
- `idempotencymw` cannot serve `lenny-ops` as it stands: `Record.IsExpired` uses the fixed `idempotency.TTL` of 24 hours where `lenny-ops` needs a 7-day class; errors go out in the §15.1 envelope through `errorclassify`, where `lenny-ops` writes its `conventions` envelope; a store outage answers `500`, where §25.4 requires `503 IDEMPOTENCY_STORE_UNAVAILABLE` on required-key routes and a degradation warning elsewhere; it has no required-key classification; and its store targets the tenant-scoped `idempotency_keys` table, where `ops_idempotency_keys` is platform-scoped.
- Steps 0178.S3 and 0178.S4 rework `idempotency.Key` and its validation, so coupling `opsidem` to the gateway package before 0178.S4 lands would make step 0178.S6 wait on them.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (its section "Should S6 reuse the gateway package?"), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

Two implementations of one mechanism drift apart, as the findings of the review show, and each rule has to be stated, built, and tested twice.

## Suggested resolution

After step 0178.S4 lands, fold `pkg/ops/opsidem` onto `idempotencymw`. That needs an expiry carried by each record, an error writer the caller injects, and an outage policy the caller injects, so that `lenny-ops` keeps its envelope, its TTL classes, its required-key routes, and its §25.4 outage behavior over its own platform-scoped store. Open decision O2 of change 0183 asks whether the fold comes first, as steps 0183.S1 and 0183.S2 plan, or whether `opsidem` is fixed in place first, importing only the parts that do not depend on the rework, such as the body hash, the key-length check, and the no-store gates.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S2 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
