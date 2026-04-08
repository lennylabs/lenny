# Technical Design Review Findings — 2026-04-07 (Iteration 6)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 6
**Total findings:** 7 (after deduplication)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 7     |

### Comparison Across All Iterations

| Severity | Iter 1 | Iter 2 | Iter 3 | Iter 4 | Iter 5 | Iter 6 |
|----------|--------|--------|--------|--------|--------|--------|
| Critical | 30     | 4      | 1      | 0      | 0      | 0      |
| High     | 105    | 26     | 2      | 11     | 0      | 0      |
| Medium   | 138    | 61     | 0*     | 63     | 18     | 7      |
| Total    | 353    | 137    | 3      | 74     | 18     | 7      |

All 7 findings are completeness/consistency fixes — stale references, missing catalog entries, lenny-ctl gaps. No new design, security, or correctness issues.

---

## Detailed Findings

### API-033 Four Stale `experiments/{id}` References Remain [Medium]
**Section:** 10.7, 15.1
**Status:** Fixed

Iter5 API-032 fix corrected the primary path but missed four stale references: `PATCH /v1/admin/experiments/{id}` ×2 in §10.7, `GET /v1/experiments/{id}/results` ×1 in §10.7 rollback table, ×1 in §15.1 pagination list.

**Fix applied:** All four replaced with `{name}` and the results path corrected to `/v1/admin/experiments/{name}/results`.

### API-034 Error Catalog Missing 8 Normative Error Codes [Medium]
**Section:** 15.1
**Status:** Fixed

Eight codes from iter1-2 fixes outside iter3/iter5 catalog additions: `DERIVE_ON_LIVE_SESSION` (409), `DERIVE_LOCK_CONTENTION` (429), `REGION_CONSTRAINT_VIOLATED` (403), `REGION_CONSTRAINT_UNRESOLVABLE` (422), `REGION_UNAVAILABLE` (503), `KMS_REGION_UNRESOLVABLE` (422), `LEASE_SPIFFE_MISMATCH` (403), `ENV_VAR_BLOCKLISTED` (400).

**Fix applied:** All 8 added to the error catalog in §15.1 with category and retryable flag. `DERIVE_LOCK_CONTENTION` classified `POLICY`/429/retryable:true; `REGION_UNAVAILABLE` classified `TRANSIENT`/503/retryable:true; `LEASE_SPIFFE_MISMATCH` and `REGION_CONSTRAINT_VIOLATED` classified `POLICY`/403/retryable:false; `DERIVE_ON_LIVE_SESSION`, `REGION_CONSTRAINT_UNRESOLVABLE`, `KMS_REGION_UNRESOLVABLE` classified `PERMANENT`/409 or 422/retryable:false; `ENV_VAR_BLOCKLISTED` classified `PERMANENT`/400/retryable:false.

### POL-031 DelegationPolicyEvaluator Absent from Summary Priority Table [Medium]
**Section:** 4.8
**Status:** Fixed

Iter5 POL-030 added evaluators to the MODIFY interaction table but not to the summary "Built-in interceptors (with default priorities)" table.

**Fix applied:** Added `DelegationPolicyEvaluator (250)` and `RetryPolicyEvaluator (600)` to the summary table with their phases and activation notes.

### NET-025 `CIRCUIT_BREAKER_OPEN` Category `TRANSIENT` Contradicts `retryable: false` [Medium]
**Section:** 11.6, 15.1
**Status:** Fixed

`TRANSIENT` implies retryable but the description says "not retryable — wait for operator." An operator-declared circuit breaker is a policy action, not transient.

**Fix applied:** Category changed from `TRANSIENT` to `POLICY` in the §15.1 error catalog. `retryable: false` confirmed.

### OPS-028 `lenny-ctl policy audit-isolation` Absent from §24 [Medium]
**Section:** 24, 8.3, 17.6
**Status:** Fixed

Referenced as a first-class operator command in §8.3 and §17.6 but missing from the §24 command reference.

**Fix applied:** Added new §24.9 Policy Management section with the `lenny-ctl policy audit-isolation` command entry, including description, API mapping, and minimum role.

### OPS-029 `lenny-ctl admin pools drain` and `--restore-old-pool` Absent from §24 [Medium]
**Section:** 24, 15.1, 10.5
**Status:** Fixed

Pool drain endpoint is in §15.1 and rollback flag is in §10.5 but neither has a lenny-ctl entry in §24.3.

**Fix applied:** Added `lenny-ctl admin pools drain --pool <name>` to §24.3 (mapping to `POST /v1/admin/pools/{name}/drain`). Updated the existing `upgrade rollback` entry to distinguish base rollback from `--restore-old-pool` (late-stage rollback from `Draining`/`Contracting` states), with reference to §10.5.

### STR-025 Billing WAB Metric Undefined + 3 Billing Alerts Not in §16.5 [Medium]
**Section:** 12.3, 16.5
**Status:** Fixed

`billing_write_ahead_buffer_utilization` referenced as required monitoring but has no formal definition. Three billing alerts in §12.3 prose are absent from §16.5 inventory.

**Fix applied:** Added formal `billing_write_ahead_buffer_utilization` Gauge metric definition to §16.1 (with semantics, labels, and threshold reference). Added companion `lenny_billing_redis_stream_depth` metric. Added `BillingStreamBackpressure`, `BillingCorrectionApprovalBacklog`, and `BillingWriteAheadBufferHigh` alerts to §16.5 warning table (note: `BillingCorrectionRateHigh` was already present).
