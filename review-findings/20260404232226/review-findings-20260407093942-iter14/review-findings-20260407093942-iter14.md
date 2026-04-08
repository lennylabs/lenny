# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 16 — Warm Pool & Pod Lifecycle Management
**Iteration:** 14
**Prior findings carried forward:** WPL-030 (still present)
**New findings:** 1 (WPL-031)
**Total findings this iteration:** 2 (0 Critical, 0 High, 2 Medium)

## Carried Forward

| # | ID | Severity | Finding | Section |
|---|------|----------|---------|---------|
| 1 | WPL-030 | MEDIUM | **Failover duration formula is wrong: 25s should be ~17s.** The spec states worst-case crash failover is `leaseDuration + renewDeadline = 15s + 10s = 25s` (lines 409, 502, 513, 4019, 8197). This is incorrect. In Kubernetes leader election, `renewDeadline` is the maximum time the *current* leader spends attempting to renew before voluntarily stepping down — it does not delay lease availability to other candidates. The lease becomes acquirable by other candidates after `leaseDuration` (15s) from the last successful renewal, and a new leader can acquire it within at most one `retryPeriod` (2s). Worst-case failover is therefore `leaseDuration + retryPeriod = 15s + 2s = 17s`. The 25s figure is used in the `failover_seconds` default throughout all sizing formulas (Sections 4.6.1, 4.6.2, 17.8), the `podClaimQueueTimeout` margin calculation ("60s provides a 35-second margin above the 25s worst-case" — should be 43s margin above 17s), and the worked example in Section 4.6.1. All instances must be corrected. The over-estimate is conservative (pools are slightly oversized, not undersized), so this is not a correctness risk but is a factual error that will mislead operators tuning their deployments. | 4.6.1, 4.6.2, 12.3, 17.8 |

## New Findings

| # | ID | Severity | Finding | Section |
|---|------|----------|---------|---------|
| 2 | WPL-031 | MEDIUM | **Recommended per-tier minWarm values omit `safety_factor`, contradicting the formula they claim to apply.** Section 17.8 states the formula `minWarm >= claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds` and lists per-tier safety factors (1.5 for Tier 1/2, 1.2 for Tier 3). However, the recommended minWarm values in the table are computed as `claim_rate * (failover_seconds + pod_startup_seconds)` *without* the safety factor: Tier 2 = `5 * (25+10) = 175` (matches table), but applying safety_factor gives `5 * 1.5 * 35 = 262.5`; Tier 3 = `30 * (25+10) = 1050` (matches table), but with safety_factor gives `30 * 1.2 * 35 = 1260`. The burst term (which would increase the values further) is also absent. The recommended values are exactly `claim_rate * 35` with no safety factor and no burst term, yet the accompanying text says the formula includes both. Either the table values should be recomputed with safety_factor and a stated burst assumption, or the text should clarify that the table values are base estimates before safety_factor/burst adjustment. | 17.8 |

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **SDK-warm mode design** (Section 6.1): preConnect capability, demotion mechanism, sdkWarmBlockingPaths matching contract, demotion rate thresholds and circuit-breaker (90% hard threshold) are all coherent and well-specified.
- **Pod eviction during sdk_connecting** (Section 6.1-6.2): SIGTERM handling during sdk_connecting is explicitly defined — DemoteSDK with bounded timeout, then exit as `terminated` (not `failed`). terminationGracePeriodSeconds requirement (>= LENNY_DEMOTE_TIMEOUT_SECONDS + 5s) is correctly stated.
- **sdk_connecting watchdog** (Section 6.1): 60s default timeout, transition to `failed`, counter and alert defined.
- **Experiment variant pool sizing** (Section 4.6.2): base pool adjustment formula correctly uses `(1 - Sigma variant_weights)`, clamping to `[0, 1)`, with admission-time rejection at >= 1.
- **PoolScalingController experiment transitions** (Section 10.7): all three transition paths (active->paused, paused->active, concluded) correctly adjust both variant and base pool minWarm.
- **Bootstrap mode** (Section 17.8): five convergence criteria, operator override API, gauge/alert, and first-week monitoring workflow are internally consistent.
- **Pool taxonomy** (Section 5.2): hot/cold/disallowed tiers are well-defined with sensible Tier 1-3 pool count guidance.
- **Reconciliation drift detection** (Section 4.6.2): PoolConfigDrift alert, config-generation annotation, sync-status endpoint all coherent.
- **CRD field ownership** (Section 4.6.3): SSA enforcement with named field managers, conflict retry policy, and RBAC backstop are well-designed.
- **podClaimQueueTimeout** (Section 4.6.1): 60s default with Postgres fallback claim path before exhaustion error — design is sound (though the margin calculation references the incorrect 25s value per WPL-030).
