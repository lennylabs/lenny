# Perspective 24 — Policy Engine & Admission Control (Iteration 14)

## Remaining from Iteration 13

### POL-041 — Cross-phase priority ordering error (MEDIUM) — STILL PRESENT

**Location:** Section 4.8, line 918

**The problem:** The built-in interceptor field dependencies paragraph states: "External interceptors registered between 201 and 249 run after `QuotaEvaluator` but before `DelegationPolicyEvaluator`." This implies a single linear priority chain across all phases. However, `QuotaEvaluator` fires at the `PostAuth` phase (priority 200) and `DelegationPolicyEvaluator` fires at the `PreDelegation` phase (priority 250). These are different phases in the request lifecycle — phases execute sequentially, and within each phase, interceptors execute in priority order. An external interceptor at priority 225 registered for `PostAuth` would run after `QuotaEvaluator` at that phase but would never interact with `DelegationPolicyEvaluator` (which runs at a different phase entirely). The statement is only true if the external interceptor is registered for **both** phases, which is not stated.

**Why it matters:** Implementers may configure external interceptors at priority 225 expecting them to "slot between" QuotaEvaluator and DelegationPolicyEvaluator, when in reality priority ordering is per-phase, not global.

**Fix:** Clarify that priority ordering applies within each phase independently. Rewrite the cross-built-in priority guidance to specify phase-by-phase behavior rather than implying a single linear chain.

---

## New Findings

### POL-042 — `INTERCEPTOR_TIMEOUT` return behavior self-contradiction (MEDIUM)

**Location:** Section 4.8, line 895

**The problem:** The `INTERCEPTOR_TIMEOUT` error code paragraph begins with: "When an interceptor times out (**regardless of `failPolicy`**), the gateway returns `INTERCEPTOR_TIMEOUT` to the caller." The same paragraph later states: "When `failPolicy: fail-open`, the request proceeds with an `interceptor_bypassed` flag in the audit event, and **no `INTERCEPTOR_TIMEOUT` error is returned to the caller**."

These two statements directly contradict each other. The first says the error is returned regardless of `failPolicy`; the second says it is not returned when `failPolicy: fail-open`.

The error catalog (Section 15.1, `INTERCEPTOR_TIMEOUT` entry) agrees with the second statement: "Returned when `failPolicy: fail-closed`; suppressed (request proceeds) when `failPolicy: fail-open`."

**Fix:** Remove "regardless of `failPolicy`" from the opening sentence. Replace with: "When an interceptor times out and its `failPolicy` is `fail-closed`, the gateway returns `INTERCEPTOR_TIMEOUT` to the caller."

### POL-043 — Timeout table (Section 11.3) missing policy-relevant configurable timeouts (MEDIUM)

**Location:** Section 11.3, lines 4724–4748

**The problem:** The "Timeouts and Cancellation" table is presented as the comprehensive reference for configurable timeouts (24 entries, including non-timeout items like gRPC keepalive intervals and DNS resolution timeouts). However, it omits at least six configurable, policy-relevant timeouts that are defined with explicit defaults elsewhere in the spec:

| Missing timeout | Default | Defined in |
|---|---|---|
| `rateLimitFailOpenMaxSeconds` | 60s | Section 12.4 |
| `quotaFailOpenCumulativeMaxSeconds` | 300s | Section 12.4 |
| `delegation.usageQuiescenceTimeoutSeconds` | 5s | Section 8.3 |
| `delegation.cascadeTimeoutSeconds` | 3600s | Section 8.3 |
| `delegation.maxTreeRecoverySeconds` | 600s | Section 8.10 |
| `delegation.maxLevelRecoverySeconds` | 120s | Section 8.10 |

These are all deployer-configurable via Helm values and directly affect policy enforcement behavior (fail-open windows, budget return accuracy, orphan cleanup bounds, tree recovery limits).

**Why it matters:** Deployers using Section 11.3 as the authoritative timeout reference will miss six configurable timeouts that control fail-open windows and delegation tree lifecycle — the exact timeouts most likely to need tuning for security and cost control.

**Fix:** Add the six missing timeouts to the Section 11.3 table with their defaults, Helm paths, and source section references.
