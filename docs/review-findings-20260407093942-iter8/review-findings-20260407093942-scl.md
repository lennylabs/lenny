# Technical Design Review Findings — 2026-04-07 (Iteration 8, Perspective 4: Scalability & Performance Engineering)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Scalability & Performance Engineering
**Iteration:** 8
**Category prefix:** SCL (starting at 029)
**Total findings:** 1

Prior SCL findings reviewed: SCL-001 through SCL-028. No regressions. SCL-027 (§12.4 Tier 3 session count corrected to 10,000) and SCL-028 (§16.5 `PodClaimQueueSaturated` alert threshold corrected to `0.25 × pool.minWarm`) are both confirmed fixed.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 1     |

---

## Detailed Findings

---

### SCL-029 §17.8.2 "Quota Drift Bound" Column Uses Wrong Formula Basis — Tier 3 Figure Misaligned with Co-Located `quotaSyncIntervalSeconds` Parameter [Medium]

**Section:** 17.8.2

**Description:**

The §17.8.2 operational-defaults-by-tier table includes three rows that are presented as causally related:

| Parameter                      | Tier 1   | Tier 2     | Tier 3      |
| ------------------------------ | -------- | ---------- | ----------- |
| Rate limit fail-open window    | 60s      | 60s        | 30s         |
| Quota sync interval            | 30s      | 30s        | 10s         |
| Quota drift bound (worst case) | ~600 req | ~6,000 req | ~30,000 req |

The table label "Quota drift bound (worst case)" and its co-location with `quotaSyncIntervalSeconds` strongly imply that the drift figure is derived from the sync-interval formula given in §11.2:

> `max_overshoot = quotaSyncIntervalSeconds × max_tokens_per_second × active_sessions_per_tenant`

However, verifying the Tier 3 figure against this formula reveals a large inconsistency. At Tier 3: `quotaSyncIntervalSeconds = 10s`, 500 active tenants, 10,000 total sessions → 20 sessions/tenant. If the per-session request intensity is held constant across tiers (as implied by the consistent Tier 1 and Tier 2 figures):

- Tier 1: 600 = 30s × X → X = 20 req/s/tenant (100 sessions / 5 tenants = 20 sessions/tenant, 1 req/s/session)
- Tier 2: 6,000 = 30s × Y → Y = 200 req/s/tenant (1,000/50 = 20 sessions/tenant, 10 req/s/session — 10× increase, consistent with gateway RPS scaling 10×)
- Tier 3 (expected): 10s × Z. To match Tier 2's per-session rate (10 req/s), Z = 20 × 10 = 200 req/s/tenant → drift = 10 × 200 = **2,000 req**. The table says **30,000 req** — 15× higher.

For the table's Tier 3 figure to be correct under the sync-interval formula, Tier 3 sessions would need to generate 150 req/s per session — 15× more intensive than Tier 2, with no explanation for this jump.

**What the numbers actually represent:** The drift figures are internally consistent if interpreted as **fail-open drift** (the §12.4 per-replica ceiling formula): `N_replicas × (tenant_limit / cached_replica_count)`. With the maximum Tier 3 gateway replica count of 30 (§17.8.2) and a per-replica hard ceiling of 1,000 requests per tenant: 30 × 1,000 = 30,000. The Tier 1 and Tier 2 values are also consistent with this formula (4 × 150 = 600; 10 × 600 = 6,000). This interpretation is legitimate — fail-open drift is a genuine worst-case exposure — but it is a **different quantity** from the sync-interval drift.

**Why this matters operationally:**

1. An operator reading the table will see `quotaSyncIntervalSeconds: 10s` adjacent to `Quota drift bound: 30,000 req` and conclude that their worst-case drift from a checkpoint gap is 30,000 requests. The actual sync-interval drift at Tier 3 is approximately 2,000 requests/tenant — 15× lower. The operator may adopt a looser `quotaSyncIntervalSeconds` or a higher `per_replica_hard_cap` than necessary, under the false belief that baseline drift is already 30,000.

2. Conversely, if an operator understands the 30,000 figure as fail-open drift and tries to validate it against the sync-interval formula, they will conclude the spec is arithmetically incorrect and distrust both figures.

3. The §11.2 "Maximum Overshoot Formula" section describes both sources of drift (sync-interval and fail-open) without cross-referencing the §17.8.2 table. There is no canonical statement of which formula produced the table values.

**Fix:** One of two approaches:

**Option A (recommended):** Split the "Quota drift bound (worst case)" row into two rows with explicit labels:

| Parameter                                          | Tier 1    | Tier 2     | Tier 3     |
| -------------------------------------------------- | --------- | ---------- | ---------- |
| Quota drift (sync-interval, normal operation)      | ~600 req  | ~6,000 req | ~2,000 req |
| Quota drift (fail-open, Redis outage worst case)   | ~600 req  | ~6,000 req | ~30,000 req |

Add a footnote: "Sync-interval drift = `quotaSyncIntervalSeconds × max_requests_per_second_per_tenant`. Fail-open drift = `max_replicas × per_replica_hard_cap`. At Tier 3, the Quota sync interval drops from 30s to 10s, which reduces normal-operation drift despite the larger session count; fail-open drift grows due to higher replica counts."

**Option B:** Retain a single "Quota drift bound (worst case)" row but add a parenthetical clarifying which formula was used: `~30,000 req (fail-open path: 30 replicas × ~1,000/tenant per-replica ceiling)`.

Either fix eliminates the apparent arithmetic inconsistency and clarifies the source of the Tier 3 figure for operators.

---

## Prior SCL Findings Status

All SCL-001 through SCL-028 reviewed. No regressions detected. The fixes applied in iterations 1–7 are correctly reflected in the spec.

- SCL-027: §12.4 now correctly reads "~10,000 concurrent sessions, 2,000 new sessions/s" — confirmed fixed.
- SCL-028: §16.5 `PodClaimQueueSaturated` alert condition now reads `lenny_pod_claim_queue_depth > 0.25 × pool.minWarm for > 30s AND lenny_warmpool_idle_pods > 0` — confirmed fixed.
- All prior SCL-001 through SCL-026 verified present and unmodified.
