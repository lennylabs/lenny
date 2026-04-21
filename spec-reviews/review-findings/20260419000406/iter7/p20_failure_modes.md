# Iteration 7 — Perspective 20: Failure Modes & Resilience Engineering

- Category: **FMR**
- Spec root: `/Users/joan/projects/lenny/spec/`
- Prior review: `spec-reviews/review-findings/20260419000406/iter6/p20_failure.md`
- Iter6 fix commit under review: `c941492` (iter6 fixes), `8604ce9` (iter6 fix-pass). Scope prioritizes the two deltas the caller highlighted:
  1. `GIT_CLONE_REF_UNRESOLVABLE` split into PERMANENT + `GIT_CLONE_REF_RESOLVE_TRANSIENT` (TRANSIENT/503)
  2. Operator-managed circuit-breaker admin endpoints (§15.1 — list/get/open/close) with `INVALID_BREAKER_SCOPE`
- Severity calibration: anchored to iter4/iter5 rubric per `feedback_severity_calibration_iter5.md` — "alert referenced but not defined" → Low; "single broken cross-file anchor" → Low; "cascading-failure recovery path omitted" → High.

---

## 1. Iter6-fix verification (new failure semantics introduced?)

### 1.1 GIT_CLONE_REF split (PERMANENT vs TRANSIENT) — VERIFIED CLEAN

- `/Users/joan/projects/lenny/spec/15_external-api-surface.md` — `GIT_CLONE_REF_UNRESOLVABLE` now PERMANENT/422 covering `auth_failed | ref_not_found`; `GIT_CLONE_REF_RESOLVE_TRANSIENT` now TRANSIENT/503 with `Retry-After` covering `network_error`.
- Both codes are enumerated in §15.2.1 `RegisterAdapterUnderTest` error matrix with explicit retry/no-retry semantics. The TRANSIENT code emits `Retry-After` and follows the same exponential-backoff contract as other 503s (§15.4.3 external-error envelope).
- `/Users/joan/projects/lenny/docs/reference/error-catalog.md:92-93` synced. Workload-initiated git clones propagate the split consistently (no silent retry-on-permanent-auth-failure path reintroduced).
- **No new cascading-failure path**: previously a single code collapsed auth/ref/network errors; the split removes an ambiguity that previously forced clients to either retry everything (DoS risk against Git providers on permanent auth failures) or retry nothing (availability loss on transient network errors). The iter6 change is a strict failure-semantics improvement — it does **not** widen the retry surface; it narrows the retry set to genuinely transient conditions.

### 1.2 Operator-managed circuit-breaker endpoints — VERIFIED CLEAN

- `/Users/joan/projects/lenny/spec/15_external-api-surface.md` — four endpoints: `GET /v1/platform/circuit-breakers`, `GET /v1/platform/circuit-breakers/{name}`, `POST /v1/platform/circuit-breakers/{name}/open`, `POST /v1/platform/circuit-breakers/{name}/close`. Scope domain `circuit_breaker` added; new error `INVALID_BREAKER_SCOPE` (PERMANENT/403) for tenant-scope crossover.
- `/Users/joan/projects/lenny/spec/11_policy-and-controls.md §11.6` — admin mutations persist through Redis `cb:{name}` keys and are **read through the in-process cache** on the admission path (cache-only read discipline preserved from iter5 FMR-018 fix). The cold-start readiness gate (`CIRCUIT_BREAKER_CACHE_UNINITIALIZED`, Redis unavailable at startup) still forces `fail-closed` until the cache hydrates, preventing a fail-open bypass via admin-forced state.
- `POST …/open` and `POST …/close` pass through the same Redis propagation lag (≤ cacheRefreshIntervalSeconds, default 5s). This is documented in §11.6 and does **not** introduce a new failure mode — it is the same cache-staleness window that the gateway already tolerates for automatic breakers, and the bound is bounded (not unbounded) because Redis writes are primary.
- **No new cascading-failure path**: admin endpoints cannot force the breaker into a state that was not already reachable by the automatic controller path. The only net-new failure mode — operator mis-invocation across tenant scope — is caught by the dedicated `INVALID_BREAKER_SCOPE` error at request time (no system state mutation occurs).

### 1.3 Iter6 FMR-020 closure — VERIFIED FIXED

Iter6 FMR-020 (`QuotaFailOpenUserFractionInoperative` alert referenced in §12.4 but not defined in §16.5) is closed:
- `/Users/joan/projects/lenny/spec/16_observability.md:203` — `lenny_quota_user_failopen_fraction` gauge defined (§16.1).
- `/Users/joan/projects/lenny/spec/16_observability.md:451` — `QuotaFailOpenUserFractionInoperative` (Warning) alert defined in §16.5 with continuous-fire `for: 10m` semantics and explicit runbook link.
- `/Users/joan/projects/lenny/docs/reference/metrics.md:275,491` — docs synced.

---

## 2. Prior-iteration carry-forwards (still present, held at Low)

The following were Low in prior iterations and remain so under the calibration rubric (isolated doc-table/prose-vs-PromQL nits; no runtime failure-mode gap; no operator action blocked — each has a workable signal even if syntactic form is imperfect).

### FLR-014 (carry-forward, Low, 6th iteration) — `InboxDrainFailure` alert expression is prose, not PromQL
- Location: `/Users/joan/projects/lenny/spec/16_observability.md:509`
- Observation: the expression cell still reads `incremented (any non-zero increase over a 5-minute window)` rather than `increase(lenny_inbox_drain_failures_total[5m]) > 0`. Other §16.5 rows in the same table use executable PromQL.
- Impact: operators deploying the rule must translate prose → PromQL by hand. No cascading failure.
- Severity: **Low** (calibrated to iter4/iter5 "prose-instead-of-PromQL" class).

### FLR-015 (carry-forward, Low, 6th iteration) — PgBouncer readiness probe tuning rationale omitted
- Location: `/Users/joan/projects/lenny/spec/12_storage-architecture.md:45`
- Observation: `periodSeconds: 5, failureThreshold: 2, timeoutSeconds: 3` is specified without documenting the failover-window relationship (10s detection vs ~30s Patroni promotion → up to 20s of gateway requests hitting a dead primary).
- Impact: operators tuning for sub-10s detection have no guidance; the default behavior is documented but not justified.
- Severity: **Low** (documentation-guidance gap; runtime failure behavior is bounded by the gateway's own Postgres-error retry logic in §10.3).

### FLR-016 (carry-forward, Low, 6th iteration) — `GatewayNoHealthyReplicas` alert family mapping
- Location: `/Users/joan/projects/lenny/spec/16_observability.md` §16.5
- Observation: the alert is defined but the §16.5 "Family" column does not explicitly place it under the availability vs. saturation family, making triage-by-family queries slightly asymmetric.
- Impact: triage ambiguity, not runtime ambiguity.
- Severity: **Low**.

### FLR-017 (carry-forward, Low, 6th iteration) — preStop drain timeout row vs §10.1 formula
- Location: `/Users/joan/projects/lenny/spec/17_deployment-topology.md:910` vs `/Users/joan/projects/lenny/spec/10_gateway-internals.md:97-181`
- Observation: §17 shows fixed tier caps (60s/60s/120s for the `workspaceBytesThresholdMedium`/`-Large` tiers plus checkpoint barrier ack) while §10.1 derives the total from `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30s`. The arithmetic is consistent but the two sites do not cross-reference.
- Impact: Helm-chart operators editing §17 values may not realize the §10.1 formula must be re-validated. No direct runtime gap because `terminationGracePeriodSeconds` (210s default) absorbs the variance.
- Severity: **Low** (doc-sync; iter6 did not touch either site).

---

## 3. New findings (iteration 7)

### FMR-021 (NEW, Low) — Broken cross-file anchor introduced by iter6 OBS-037 fix [FIXED — closed by DOC-031]
- Location: `/Users/joan/projects/lenny/spec/16_observability.md:203`
- Observation: the iter6 fix that added the `lenny_quota_user_failopen_fraction` gauge row includes this cross-file link in its description:

  ```
  see [Section 12.4](12_storage-architecture.md#124-quota-and-rate-limiting)
  ```

  `/Users/joan/projects/lenny/spec/12_storage-architecture.md` has **no** `### 12.4 Quota and Rate Limiting` heading. The actual §12.4 heading is `### 12.4 Redis HA and Failure Modes` (anchor `#124-redis-ha-and-failure-modes`), and it is the correct target for the fail-open fraction semantics being referenced. Rate-limit-specific content lives under the same section (`#### 12.4.1 …` subheadings) but the top-level §12.4 anchor is the Redis-HA one.
- Regression context: iter6 closed 12 broken-anchor sites (iter5 DOC-024/025). The OBS-037 fix that added this line is the single regression — it reintroduces the same class of defect in exactly one site.
- Impact: readers navigating from the §16.1 fail-open gauge to the §12.4 semantics land on a 404 (static-site build) or on the page top (GitHub rendering). No runtime failure.
- Severity: **Low** — calibrated to the iter4/iter5 "single broken cross-file anchor" precedent. The DOC-024/025 Medium class was only Medium because of *volume* (12 sites); a single-site regression is Low per the calibration rule against severity inflation.
- Suggested fix: replace `#124-quota-and-rate-limiting` with `#124-redis-ha-and-failure-modes` in `16_observability.md:203`.

---

## 4. Convergence assessment

- **New C/H/M findings: 0.**
- **New Low findings: 1** (FMR-021, doc anchor).
- **Carry-forward Low findings: 4** (FLR-014 / FLR-015 / FLR-016 / FLR-017), all unchanged since iter4–iter5.
- **Iter5 FMR-018 (Medium) + FMR-019 (Low) + iter6 FMR-020 (Low): all remain FIXED.**
- No new cascading-failure paths introduced by the `GIT_CLONE_REF_*` split or by the circuit-breaker admin endpoints.
- Redis fail-open cumulative budget (`quotaFailOpenCumulativeMaxSeconds`), per-user fail-open ceiling (`userFailOpenFraction` × effective), cache-only admission read discipline, cold-start readiness gate, and the preStop CheckpointBarrier protocol all remain fully specified.
- Postgres failover window consistency (PgBouncer detection → Patroni promotion → gateway retry) is bounded; the only gap (FLR-015) is guidance, not behavior.

**Verdict: Converged for Perspective 20.**

---

## 5. Findings index

| ID      | Severity | Status           | Location                                             |
|---------|----------|------------------|------------------------------------------------------|
| FMR-021 | Low      | NEW              | `spec/16_observability.md:203` (broken anchor)       |
| FLR-014 | Low      | Carry-forward #6 | `spec/16_observability.md:509` (prose expr)          |
| FLR-015 | Low      | Carry-forward #6 | `spec/12_storage-architecture.md:45` (probe tuning)  |
| FLR-016 | Low      | Carry-forward #6 | `spec/16_observability.md` §16.5 (family mapping)    |
| FLR-017 | Low      | Carry-forward #6 | `spec/17_deployment-topology.md:910` (preStop row)   |
| FMR-018 | Medium   | FIXED (iter6)    | (operator breaker cache-only read discipline)        |
| FMR-019 | Low      | FIXED (iter6)    | (quota fail-open fraction doc)                       |
| FMR-020 | Low      | FIXED (iter6)    | `QuotaFailOpenUserFractionInoperative` defined       |
