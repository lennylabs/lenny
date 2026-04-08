# Review Findings — Iteration 9, Perspective 16: Warm Pool & Pod Lifecycle Management

**Spec:** `docs/technical-design.md`
**Date:** 2026-04-07
**Category prefix:** WPL-026+

---

### WPL-026 Default `target_minWarm` Formula Has `variant_weight` on First Term Only — Burst Term Is Not Scaled, and `variant_weight` Is Undefined for Non-Experiment Pools [Medium]

**Section:** 4.6.2

The default `target_minWarm` formula (§4.6.2, line 509–510):

```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor × (failover_seconds + pod_startup_seconds)
                      + burst_p99_claims × pod_warmup_seconds)
```

contains `variant_weight` in the steady-state term but **not** in the burst term. The adjusted base pool formula (§4.6.2, line 524–525) applies `(1 - Σ variant_weights)` to both terms:

```
base_minWarm = ceil(base_demand_p95 × (1 - Σ variant_weights) × safety_factor × (failover_seconds + pod_startup_seconds)
                    + burst_p99_claims × (1 - Σ variant_weights) × pod_warmup_seconds)
```

This creates two concrete problems:

**Problem 1: `variant_weight` is undefined for a pool not participating in an experiment.** The default formula is presented as the general formula for any pool, but `variant_weight` has no documented value when no experiment is active. The implicit assumption is `variant_weight = 1.0`, but this is never stated. A reader implementing the PoolScalingController cannot determine what value to use for `variant_weight` on a plain pool. The formula should either omit `variant_weight` entirely for the general case (since `× 1.0` is a no-op) or document its default value explicitly.

**Problem 2: The burst term is not scaled by `variant_weight`, making the formula internally inconsistent.** When the formula is used for a variant pool (where `variant_weight < 1.0`, e.g., 0.1 for a 10% traffic variant), the steady-state term is correctly scaled down to `base_demand_p95 × 0.1 × safety_factor × ...`, but the burst headroom remains `burst_p99_claims × pod_warmup_seconds` — computed against the full demand signal, not the variant's fraction of demand. The adjusted base pool formula (the authoritative formulation) correctly scales both terms. The default formula should be consistent with it.

The same bug is reproduced in the mode-adjusted formula in §5.2 (line 1972–1973):

```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor × (failover_seconds + pod_startup_seconds) / mode_factor
                      + burst_p99_claims × pod_warmup_seconds / mode_factor)
```

Again `variant_weight` appears only in the first term.

**Impact:** A PoolScalingController implementation that faithfully implements the default formula as written will over-provision burst headroom for variant pools by a factor of `1 / variant_weight` relative to the adjusted base formula. For a 10% traffic variant (`variant_weight = 0.1`), the burst headroom is 10× higher than correct. At scale this is a significant cost error. For the base/non-variant case, the ambiguity of `variant_weight` may cause implementors to hard-code an incorrect value.

**Recommendation:**

1. Remove `variant_weight` from the default formula (§4.6.2 lines 509–510). The general formula applies to a single pool receiving all demand assigned to it — `variant_weight` is not a variable at that level. The formula should be:

   ```
   target_minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_startup_seconds)
                         + burst_p99_claims × pod_warmup_seconds)
   ```

2. Keep the adjusted base pool formula (lines 524–525) as-is — it correctly derives `base_minWarm` as a function of `(1 - Σ variant_weights)` applied to both terms.

3. Add a variant pool formula for completeness:

   ```
   variant_minWarm = ceil(base_demand_p95 × variant_weight_i × safety_factor × (failover_seconds + pod_startup_seconds)
                          + burst_p99_claims × variant_weight_i × pod_warmup_seconds)
   ```

   where `variant_weight_i` is the individual variant's traffic fraction. This is the formula the PoolScalingController uses when provisioning a specific variant pool.

4. Apply the same fix to the mode-adjusted formula in §5.2 (line 1972–1973): remove `variant_weight` from the first term.

---

### WPL-027 SDK-Warm Path State Machine Diagram Is Ambiguous — `finalizing_workspace` Appears to Have Two Exit Paths, Implying Setup Commands Can Be Bypassed [Low]

**Section:** 6.2

The SDK-warm path state machine diagram (§6.2, line 2153–2157) reads:

```
SDK-warm path (preConnect: true):
  warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                           │
                                                           ▼
                           attached ←── finalizing_workspace ──→ running_setup
```

The line `attached ←── finalizing_workspace ──→ running_setup` uses `──→` pointing in both directions from `finalizing_workspace`. This renders as a visual fork: `finalizing_workspace` appears to have two exit transitions — one going left to `attached` and one going right to `running_setup`. A reader interpreting this as a fork would conclude that `finalizing_workspace` can bypass `running_setup` and transition directly to `attached`, meaning setup commands (`RunSetup` RPC) are skipped for some SDK-warm sessions.

The pod-warm path diagram (line 2147–2151) renders the equivalent chain unambiguously:

```
                           attached ←── starting_session ←── running_setup
```

All arrows point in the same direction (right-to-left), forming a clear sequential chain.

Two independent sources confirm the intended SDK-warm flow includes `running_setup`:
1. The pre-attached failure transitions (§6.2, lines 2159–2166) list `running_setup → failed` but do NOT list `finalizing_workspace → attached` as a valid transition, confirming `finalizing_workspace` has only one exit: to `running_setup`.
2. The §7 session flow prose (line 2436) states "Run setup commands (bounded, logged)" before the SDK-warm-specific `ConfigureWorkspace` step, confirming setup commands execute for SDK-warm pods.

**Impact:** The ambiguous diagram could cause a runtime adapter implementor or controller developer to incorrectly conclude that `finalizing_workspace → attached` is a valid shortcut in SDK-warm mode, resulting in setup commands being skipped. Skipped setup commands can leave the workspace in an incorrect state (missing installed dependencies, missing required configuration) that the agent binary then operates against.

**Recommendation:** Redraw the SDK-warm path diagram to match the pod-warm path's right-to-left chain convention, making the sequential flow explicit and eliminating the apparent fork:

```
SDK-warm path (preConnect: true):
  warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                           │
                                                           ▼
                                                   finalizing_workspace
                                                           │
                                                           ▼
                           attached ←── running_setup (then ConfigureWorkspace, skip StartSession)
```

Alternatively, add a comment inline: `finalizing_workspace ──→ running_setup ──→ attached (via ConfigureWorkspace; StartSession skipped)` to clarify the distinction from the pod-warm path.

---

## Summary Table

| ID      | Section | Severity | Description |
|---------|---------|----------|-------------|
| WPL-026 | 4.6.2, 5.2 | Medium | `variant_weight` in default formula first term only — burst term unscaled; `variant_weight` undefined for non-experiment pools |
| WPL-027 | 6.2 | Low | SDK-warm state machine diagram draws `finalizing_workspace` as a fork, implying setup commands can be bypassed |
