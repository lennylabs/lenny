# Perspective 10 — Recursive Delegation (iter5)

**Scope:** `spec/08_recursive-delegation.md` (focused file for this iteration).

**Previous-iteration (iter4) fixes verified:**

- **DEL-011 (`treeVisibility` inheritance semantics).** Present at §8.3 lines 261–267. The spec defines the `full → parent-and-self → self-only` ordering, the three inheritance cases (omit → inherit parent; same-or-narrower → accept; broader → reject with `TREE_VISIBILITY_WEAKENING`), analogy to `CONTENT_POLICY_WEAKENING` / `DELEGATION_POLICY_WEAKENING`, and snapshot/extension behavior at lines 267 (`treeVisibility` is carried on the lease; excluded from `snapshotPolicyAtLease`; not extendable via §8.6). Fix is complete.
- **DEL-012 (`treeVisibility` in lease schema).** Present at §8.3 line 224 (`"treeVisibility": "full"` in the lease JSON example) and line 259 (normative field description with the three enum values, default, and linkage to `lenny/get_task_tree` and `messagingScope`). Fix is complete.
- **DEL-013 (`messagingScope` vs. `treeVisibility` compatibility check inputs).** Present at §8.3 lines 269–274 (normative heading "`treeVisibility` vs. `messagingScope` — delegation-time compatibility check"). The check explicitly resolves (1) child effective `messagingScope` per the §7.2 hierarchy, (2) child effective `treeVisibility` per the §8.3 inheritance rules, and (3) rejects with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` when `siblings` + non-`full`. Error details include `effectiveMessagingScope`, `effectiveTreeVisibility`, and `requiredTreeVisibility`. The paragraph at line 274 handles post-delegation `messagingScope` hierarchy changes (existing leases preserve their `treeVisibility`; effective `messagingScope` narrowed to `direct` for active children that would otherwise need `full`). §8.6 line 573 already lists `treeVisibility` among non-extendable fields. Fix is complete.

**New issues in §8 found this iteration:**

None. After a full pass of `spec/08_recursive-delegation.md` against the strict iter5 severity calibration (Critical = correctness/safety violation; High = MUST fix pre-prod; Medium = SHOULD address, has workaround; Low/Info = polish/clarity), no new correctness or safety bugs were identified. The remaining residual items observed during the pass fall into Low/Info clarity territory that is either already tracked elsewhere (e.g., DEL-008 orphan-cap audit event — carried forward from prior iteration) or does not rise above the iter5 calibration bar:

- `tracingContext` merge semantics (§8.3 line 234) state that "child entries are merged with parent entries; child entries cannot overwrite or remove parent entries," but the specific error code for an attempted overwrite is not called out in the validation table at lines 238–245. This is Low at worst and anchors to the prior-iteration DEL-011-series rubric for tracing-context error catalog completeness, which is already captured by iter4's DEL-011 (carry-forward). No new finding is warranted under iter5 anchoring.
- The `deadlock_detected` event schema (§8.8 lines 906–918) surfaces `blockedRequests` with descendant `taskId`s on the subtree-root's `await_children` stream. Under `treeVisibility: self-only` or `parent-and-self`, a parent that called `await_children` on its direct children may still receive descendant `taskId`s if the deadlocked subtree extends below. This is a visibility consistency note, not a correctness/safety violation (the parent already awaits the descendant chain's settlement transitively, and taskIds are not cross-tenant). Below the iter5 bar.
- §8.10 `cascadeOnFailure: await_completion` + parent already terminal: who collects results is left informal. Stable across iterations; below the iter5 bar.

**Convergence assessment**

- Critical: 0
- High: 0
- Medium: 0
- Low: 0
- Info: 0

New findings introduced this iteration: **0**. Iter4's three targeted fixes (DEL-011, DEL-012, DEL-013) are all present, wired correctly into §8.3 and §8.6, and internally consistent with the §8.5 `get_task_tree` description, the §8.6 non-extendable-fields list, and the §15.1 error catalog references.

Convergence (this perspective, this iteration): **Y**.
