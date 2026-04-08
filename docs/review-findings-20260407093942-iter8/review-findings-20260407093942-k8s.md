# Iteration 8 Review Findings — Kubernetes Infrastructure & Controller Design

**Reviewer perspective:** Kubernetes Infrastructure & Controller Design
**Spec:** `docs/technical-design.md` (8,649 lines)
**Iteration:** 8 (after 7 rounds of ~270 fixes)
**Categories start at:** K8S-025
**Prior K8S findings:** K8S-001 through K8S-024 — all Fixed or Skipped; not re-reported here.

---

## Summary

Four genuine findings. Two are correctness errors an implementor would build the wrong system from (High + Medium-critical). Two are stale cross-references in the alert table that would send an operator to the wrong section during an incident.

---

### K8S-025 WarmPoolController RBAC Missing `create`, `delete`, and `SandboxClaim` Access [High]

**Location:** §4.6.3, line 568

**The spec says:**

> The WarmPoolController ServiceAccount has `update` on `Sandbox` and `status` subresources of `SandboxTemplate` and `SandboxWarmPool`, and `get`/`list`/`watch` on the same.

**Why this is wrong:**

The WarmPoolController's own `PoolManager` interface (§4.6.1, lines 373–380) requires operations that `update`-only RBAC cannot satisfy:

1. **`create` on `Sandbox`** — `ReconcilePool` and `ReplacePod` both create new `Sandbox` resources (new warm pods). `update` permission does not grant the ability to create a resource from scratch; a `POST` to the API server requires the `create` verb. A controller built from this spec would get 403 Forbidden on every new pod creation attempt.

2. **`delete` on `Sandbox`** — `GarbageCollect` explicitly "covers orphaned `Sandbox` pods (no matching pool)" and deletes them. `ManagePDB` requires `create`/`update`/`delete` on `PodDisruptionBudget`. Neither is mentioned.

3. **`get`/`list`/`watch`/`delete` on `SandboxClaim`** — `GarbageCollect` (§4.6.1, line 478) "lists all `SandboxClaim` resources" every 60 seconds and deletes orphaned ones. The WPC has no `SandboxClaim` permissions at all in the RBAC spec.

4. **Missing `create`/`delete` on `Sandbox` status subresource** — Kubernetes requires the `update` verb on the `/status` subresource separately from the main resource. The spec conflates these.

**Impact:** An implementor following this RBAC spec literally would produce a controller that cannot create warm pods, cannot garbage-collect orphaned pods or claims, and cannot manage PDBs — all at runtime. The failures would appear as 403 errors from the Kubernetes API server.

**Fix:** Rewrite the WPC RBAC paragraph in §4.6.3 to enumerate the full required verb set:

| Resource | Verbs |
|---|---|
| `sandboxes` | `create`, `get`, `list`, `watch`, `update`, `patch`, `delete` |
| `sandboxes/status` | `get`, `update`, `patch` |
| `sandboxtemplates/status` | `get`, `update`, `patch` |
| `sandboxwarmpools/status` | `get`, `update`, `patch` |
| `sandboxtemplates`, `sandboxwarmpools` | `get`, `list`, `watch` |
| `sandboxclaims` | `get`, `list`, `watch`, `delete` |
| `poddisruptionbudgets` | `create`, `get`, `list`, `watch`, `update`, `patch`, `delete` |
| `leases` | `create`, `get`, `update`, `patch` (for leader election) |

---

### K8S-026 `target_minWarm` "Default Formula" Contains Undefined Variable `variant_weight` for Non-Variant Pools [Medium]

**Location:** §4.6.2, lines 509–517; also §5.2 "Adjusted Formula", line 1963

**The spec says (§4.6.2):**

```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor × (failover_seconds + pod_startup_seconds)
                      + burst_p99_claims × pod_warmup_seconds)
```

This is labeled the **"Default formula"** — the primary formula the PoolScalingController uses to size any pool.

**Why this is wrong:**

`variant_weight` is a per-experiment-variant concept (a fraction of total traffic diverted to a non-default runtime). It is only meaningful for variant pools created by experiments. For a **base pool** or a **standalone pool** (one not participating in any experiment), there is no `variant_weight` — the value is undefined.

The spec provides a separate **base pool adjustment formula** (lines 524–526) that uses `(1 - Σ variant_weights)`, implying that the "default formula" above is actually the **variant pool formula** where `variant_weight` is the fraction of traffic the variant receives. But this is never stated. An implementor writing the PoolScalingController sees `variant_weight` in the "default formula" and has no way to determine:

- What to substitute for `variant_weight` when sizing a base or standalone pool (the correct answer is `1.0`, but this is never said).
- Whether the "default formula" is different from the "base pool adjustment formula" and when each applies.

The same undefined variable appears in the task/concurrent mode "adjusted formula" at §5.2 line 1963.

**Impact:** An implementor would likely either leave `variant_weight` as a required input parameter (breaking base pool sizing) or guess that `1.0` is correct — but guessing is not "would build the right thing." The PoolScalingController implementation path forks on an undefined variable.

**Fix:** Clarify that the "default formula" is the **variant pool formula** and that `variant_weight` defaults to `1.0` for base and standalone pools (i.e., pool receives 100% of its traffic). Add a one-line note: _"For base pools and standalone pools not participating in any experiment, `variant_weight = 1.0`."_ Alternatively, rename the formula to "variant pool formula" and separately state the base pool formula. The §5.2 adjusted formula needs the same fix.

---

### K8S-027 §4.6.2 Claims Variant-Pool and Base-Pool CRD Updates Are "a Single Atomic Write" — Factually Wrong [Medium]

**Location:** §4.6.2, line 530

**The spec says:**

> The PoolScalingController applies this recomputation as a **single atomic write**: both the base pool's `SandboxWarmPool` CRD and the new variant pool's `SandboxWarmPool` CRD are updated in the same reconciliation cycle, so the total warm pod count **never exceeds the target for more than one cycle**.

**Why this is wrong:**

Kubernetes provides no mechanism to update two separate CRD resources atomically. Each `SandboxWarmPool` update is a distinct API server call. If the PoolScalingController crashes after writing the first CRD but before writing the second, the cluster is left with one pool at the new value and the other at the old value — for an indefinite period until the next successful reconciliation (up to 25s failover + reconciliation delay).

The "never exceeds the target for more than one cycle" guarantee is therefore wrong. The combined warm pod count can exceed the target for multiple reconciliation cycles if the controller crashes mid-write, and there is no compensating rollback or idempotency key specified.

**Impact:** An implementor trusting this guarantee would not add the necessary idempotency logic (e.g., annotating both CRDs with a common `transactionID` so a restarted controller can detect and complete partial writes). The overprovisioning window is bounded by the next successful reconciliation, but this is not the same as "one cycle" and the spec's guarantee is false.

**Fix:** Replace "a single atomic write" with accurate language: _"The PoolScalingController applies both updates in the same reconciliation cycle, using a common `lenny.dev/recomputation-generation` annotation on both CRDs. If the controller crashes after writing the first CRD, the second write is completed on the next reconciliation (detected by the generation mismatch). The window of overprovisioning is bounded by controller recovery time (up to 25s on crash failover) plus one reconciliation cycle — not a single atomic operation."_

---

### K8S-028 Three §16.5 Alert Descriptions Cross-Reference Wrong Section (§5.3 Instead of §4.6.1) [Medium]

**Location:** §16.5 alert table, lines 7601–7603

**The spec says:**

| Alert | Wrong cross-reference |
|---|---|
| `SandboxClaimOrphanRateHigh` (line 7601) | "See Section 5.3 (orphaned claim detection)" |
| `EtcdQuotaNearLimit` (line 7602) | "See Section 5.3 (etcd quota monitoring)" |
| `FinalizerStuck` (line 7603) | "See Section 5.3 (sandbox finalizers)" |

**Why this is wrong:**

Section 5.3 is "Isolation Profiles" (line 2038). It contains nothing about orphaned claim detection, etcd quota monitoring, or sandbox finalizers.

- Orphaned `SandboxClaim` detection is documented in §4.6.1 (line 478).
- etcd quota monitoring is documented in §4.6.1 (line 444 etcd operational tuning block).
- Sandbox finalizers are documented in §4.6.1 (line 476).

**Impact:** An on-call operator following the alert description during an incident would navigate to §5.3 (Isolation Profiles) and find nothing relevant. All three runbook cross-references point to the wrong place.

**Fix:** Change all three alert descriptions to reference §4.6.1 instead of §5.3:
- `SandboxClaimOrphanRateHigh`: "See Section 4.6.1 (orphaned claim detection)."
- `EtcdQuotaNearLimit`: "See Section 4.6.1 (etcd quota monitoring)."
- `FinalizerStuck`: "See Section 4.6.1 (sandbox finalizers)."

---

*End of K8S iteration 8 findings. Prior K8S-001 through K8S-024 not re-reported.*
