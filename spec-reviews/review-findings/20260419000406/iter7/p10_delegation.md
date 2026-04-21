# Perspective 10 — Recursive Delegation (iter7)

**Scope:** `spec/08_recursive-delegation.md` (primary), cross-checked against `spec/11_policy-and-controls.md` §11.7 audit catalog, `spec/15_external-api-surface.md` §15.1 error catalog, and `spec/16_observability.md` §16.1/§16.5.

**Iteration baseline note.** Iter6 was deferred for this perspective (`iter6/p10_delegation.md` records a rate-limit-induced deferral with no findings). Per the carry-forward convention, iter5 is treated as the convergence baseline (iter5 reported 0 findings across all severities with verdict **Y**). This iteration re-verifies iter5's claims against the current spec and anchors severity calibration to the iter5 rubric (Critical = correctness/safety violation; High = MUST fix pre-prod; Medium = SHOULD address, has workaround; Low/Info = polish/clarity).

## Prior-iteration carry-forward verification

All iter4 targeted fixes remain in place (re-verifying iter5's assertions):

- **DEL-011 (`treeVisibility` inheritance semantics).** `spec/08_recursive-delegation.md` §8.3 continues to define the three-valued ordering (`full → parent-and-self → self-only`), the inheritance cases, and rejection with `TREE_VISIBILITY_WEAKENING` when a child requests a broader value than the parent. Lease carries `treeVisibility` explicitly; it is excluded from `snapshotPolicyAtLease` and not extendable via §8.6. Fix remains landed.
- **DEL-012 (`treeVisibility` in lease schema).** `spec/08_recursive-delegation.md` §8.3 lease JSON example continues to include `treeVisibility` with its normative field description and default (`full`). Fix remains landed.
- **DEL-013 (`messagingScope` vs. `treeVisibility` delegation-time check).** Normative paragraph at §8.3 continues to specify the compatibility check inputs (effective `messagingScope` per §7.2 hierarchy, effective `treeVisibility` per §8.3 inheritance), reject with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` when `siblings` + non-`full`, and handle post-delegation `messagingScope` hierarchy changes (active children narrowed to `direct`). Fix remains landed.

iter5 (0 findings, verdict Y) therefore still stands as the effective baseline for this perspective.

## Outstanding Low-bar items (carry-forwards, unchanged; below iter5 severity bar)

These items were catalogued in iter2/iter3/iter4 as Low-severity residuals. They remain unfixed in the current spec but do not meet the iter5 correctness/safety calibration that would elevate them to Medium or higher; they are tracked here for continuity only.

- **DEL-008 (Orphan tenant-cap fallback audit event).** `spec/08_recursive-delegation.md` §8.10 `maxOrphanTasksPerTenant` cap still silently downgrades a requested `cascadeOnFailure: detach` to `cancel_all`. No `delegation.orphan_cap_fallback` audit event is catalogued in `spec/11_policy-and-controls.md` §11.7 (only `delegation.spawned` appears there), and no parent-facing event-stream signal is specified. Per-tenant gauge and cap-level threshold alerts remain the only observability. Status: Low carry-forward, unchanged from iter3/iter4.
- **DEL-011 (TRACING_CONTEXT_* error catalog gap).** `spec/08_recursive-delegation.md` §8.3 validation table still references `TRACING_CONTEXT_TOO_LARGE` (size/key-length/value-length/entry-count limits), `TRACING_CONTEXT_SENSITIVE_KEY` (key blocklist), and `TRACING_CONTEXT_URL_NOT_ALLOWED` (value URL blocklist). None of these codes appear in `spec/15_external-api-surface.md` §15.1 (confirmed: grep returns zero matches in the catalog). SDK authors therefore cannot consult a canonical HTTP status, category, retryability flag, or details-schema for these three codes. Status: Low carry-forward, unchanged from iter3/iter4.
- **DEL-014 (`DELEGATION_PARENT_REVOKED` `revocationReason` retryability nuance).** The §15.1 row (line 1034) bundles `token_rotated` and `recursive_revocation` under a single `POLICY`/403/non-retryable row. The two reasons remain semantically distinct (token rotation is recoverable by re-authentication; recursive revocation means the parent chain is terminated and no retry of any kind will succeed), but the catalog row does not split retryability guidance per-`revocationReason`. Clients continue to share a single "re-authenticate or parent terminated" guidance line. Status: Low carry-forward, unchanged from iter4.
- **DEL-015 (`DELEGATION_AUDIT_CONTENTION` duplicates retry-after).** The §15.1 row (line 1035) continues to populate both `Retry-After` header and `details.retryAfterSeconds`, while other TRANSIENT/503 entries in §15.1 (e.g., `POOL_DRAINING`) use `Retry-After` alone and §25.2's canonical admin envelope uses `suggestedRetryAfter` (string). Three conventions coexist across the catalog. Status: Low carry-forward, unchanged from iter4.

## New findings in §8 this iteration

After a full re-read of `spec/08_recursive-delegation.md` (§8.1–§8.10), cross-checked against §11.7 audit catalog, §15.1 error catalog, and §16.1/§16.5 metrics/traces catalogs, and evaluated against the iter5 severity rubric, no new Critical/High/Medium findings were identified.

Three Low/Info-grade observations below the iter5 correctness/safety bar are recorded for continuity:

- **(Low/Info) Audit catalog omits `delegation.budget_return_usage_lag`.** `spec/08_recursive-delegation.md` §8.3 line ~356 defines the `delegation.budget_return_usage_lag` warning event fired during budget-return when usage-counter quiescence times out (default 5s). `spec/16_observability.md` has a corresponding counter `lenny_delegation_budget_return_usage_lag_total`. The event name does not appear in `spec/11_policy-and-controls.md` §11.7 audit-event catalog. Operators reviewing §11.7 would not know the event exists. Not a correctness issue (the event is well-specified at its origin and has a telemetry counter); docs-completeness gap. Below the iter5 bar.
- **(Low/Info) Audit catalog omits `delegation.budget_keys_expired`.** `spec/08_recursive-delegation.md` §8.3 line ~345 defines a `delegation.budget_keys_expired` critical structured event emitted when TTL expiry is detected on the delegation-budget Redis keys, paired with a `DelegationBudgetKeysExpired` operator alert and counter `lenny_delegation_budget_keys_expired_total`. Same omission pattern as above — not listed in §11.7's catalog. Same severity assessment: docs-completeness gap, not a correctness issue, below the iter5 bar.
- **(Info) Trace span / audit event naming inconsistency: `delegation.spawn_child` vs. `delegation.spawned`.** `spec/16_observability.md` §16.5 line 340 names the delegation trace span `delegation.spawn_child`; `spec/11_policy-and-controls.md` §11.7 line 62 names the corresponding audit event `delegation.spawned`. The naming divergence is cosmetic (the identifiers serve different catalog surfaces) but means a single grep or cross-catalog join on "delegation spawn" must hit both spellings. Informational only; below the iter5 bar.

Must-check items from the task brief explicitly re-verified:

- **Rejection permanence rule.** The rule is **no longer "permanent"**; §8.2 specifies bounded `rejectionCoolOffSeconds` (default 300s) during which identical delegation targets yield `REJECTION_COOLOFF_ACTIVE`. The design is sound: bounded cool-off avoids the infinite-penalty failure mode while still throttling pathological retry loops. No finding.
- **Depth-5+ recovery.** §8.8 provides the bottom-up recovery formula with `maxLevelRecoverySeconds` (default 120s) and `maxTreeRecoverySeconds` (default 600s), plus handling for non-adjacent simultaneous failures. Formulaically complete; no finding.
- **Orphan cleanup interval.** §8.10 specifies the cleanup interval (default 60s, configurable) and the per-tenant cap (`maxOrphanTasksPerTenant` default 100). Both quantities are normative. No finding.
- **Credential propagation chains.** §8.3 defines `credentialPropagation` modes (`inherit`, `independent`, `deny`) and the multi-hop origin-pool invariant (`inherit` preserves the origin pool across hops; mixing modes mid-chain is governed by the documented rules). Chain semantics are specified; no finding.
- **Cross-environment delegation interaction.** `spec/10_gateway-internals.md` §10.6 (bilateral declaration model with `target_not_in_scope` errors) combined with §8.3's `ISOLATION_MONOTONICITY_VIOLATED` check gives the complete cross-environment-delegation contract. No finding.

## Convergence assessment

- Critical: 0
- High: 0
- Medium: 0
- Low: 0 new (4 carry-forwards from iter2–iter4: DEL-008, DEL-011, DEL-014, DEL-015; all below iter5 severity bar)
- Info: 3 (new this iteration; audit-catalog omissions for `budget_return_usage_lag` / `budget_keys_expired`; trace/audit span naming inconsistency; all below iter5 severity bar)

No new correctness or safety findings at Critical/High/Medium for this perspective in iter7. The iter4 targeted fixes (DEL-011, DEL-012, DEL-013) remain in place and internally consistent with §8.5 `get_task_tree`, §8.6 non-extendable-fields, and §15.1 error catalog references. Outstanding Low items are stable carry-forwards below the iter5 calibration bar.

Convergence (this perspective, this iteration): **Y**.
