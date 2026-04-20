# Iteration 2 Recursive Delegation & Task Trees Review

**Date:** 2026-04-19
**Perspective:** Recursive Delegation & Task Trees
**Category:** DEL
**Scope:** policy propagation, resource budgets, tree lifecycle, depth edge cases

## Prior-finding verification

- **DEL-001 (inheritance)** — Fixed. §8.3 lines 175-181 now define the three-way rule (null-child-inherits-verbatim, non-null child must be at-least-as-restrictive, null-parent uncapped) and the new error `DELEGATION_POLICY_WEAKENING` is referenced from §8.3 line 178 with a working link to §15.1. Catalog entry present at `15_external-api-surface.md:618` with `details.parentPolicy` / `details.childPolicy`.
- **DEL-002 (interceptor snapshot scope)** — Fixed. §8.3 lines 161-165 ("No snapshotting, no caching") plus §8.10 line 945 ("Live interceptor configuration still applies") consistently define interceptor config as always live, never snapshotted.
- **DEL-003 (deep-tree recovery)** — Fixed. §8.10 line 941 explicitly states the gateway MUST NOT re-evaluate `delegationPolicyRef`, `maxDelegationPolicy`, `contentPolicy`, `minIsolationProfile`, or other lease-scoped fields during recovery, and recovery reads from the persisted lease record.
- **DEL-004 (cool-off handoff)** — Fix is present in §8.6 lines 635-638 (handoff-safe query, atomic transactional re-check, UTC-only clock) **but introduces a new regression** — see DEL-006 below.
- **DEL-005 (multi-hop credential)** — Fixed. §8.3 lines 377-393 define the "origin pool" rule and the worked multi-hop example. §10.6 contains no credential-propagation language that would contradict this; the bilateral declaration checks (§10.6 lines 524-532) operate orthogonally on runtimes, not credential pools. No contradictions found.

## New findings

### DEL-006 `EXTENSION_COOL_OFF_ACTIVE` error code undefined in §15.1 catalog [High]
**Files:** `08_recursive-delegation.md` §8.6 (lines 636-637); `15_external-api-surface.md` §15.1 (error table around lines 560-633)

The DEL-004 fix introduced the wire error code `EXTENSION_COOL_OFF_ACTIVE`, used in two distinct rejection paths:

- Line 636: handoff-safe path — "the gateway auto-rejects the request with `EXTENSION_COOL_OFF_ACTIVE` and does not enter the elicitation path."
- Line 637: atomic transaction path — "the gateway rolls back the budget increment and returns `EXTENSION_COOL_OFF_ACTIVE` instead of `GRANTED`."

However, `EXTENSION_COOL_OFF_ACTIVE` is **not** present in the §15.1 error catalog. A full-text search of `15_external-api-surface.md` returns zero matches for the identifier. This is a regression against the catalog-completeness convention used for every other delegation error (`DELEGATION_POLICY_WEAKENING`, `CONTENT_POLICY_WEAKENING`, `CREDENTIAL_PROVIDER_MISMATCH`, `BUDGET_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED`, `DELEGATION_CYCLE_DETECTED` are all catalogued with category, HTTP status, and `details` fields).

Consequences:

1. Clients and SDK generators have no canonical HTTP status, category (`POLICY` vs `TRANSIENT`), or `retryable` flag for this error.
2. Adapters cannot distinguish `EXTENSION_COOL_OFF_ACTIVE` from `BUDGET_EXHAUSTED` (both reject extensions) without spec-defined semantics — yet their retry behavior differs (cool-off is time-bounded; `BUDGET_EXHAUSTED` is terminal absent extension).
3. No `details` schema is defined, so clients cannot surface the `cool_off_expiry` timestamp to the end user ("retry in N seconds") that the durability section painstakingly establishes.

**Recommendation:** Add `EXTENSION_COOL_OFF_ACTIVE` to the §15.1 error catalog with:
- `category: POLICY`, `status: 429` (consistent with `BUDGET_EXHAUSTED` and `EVAL_QUOTA_EXCEEDED`, which are the closest rate-style siblings);
- `retryable: true` after `cool_off_expiry`;
- `details.rootSessionId`, `details.subtreeSessionId`, `details.coolOffExpiry` (RFC 3339 UTC timestamp), `details.rejectionCoolOffSeconds`;
- cross-link to §8.6 and to the admin clear endpoint `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial` (§15.1 line 441).

---

### DEL-007 Orphan tenant-cap fallback does not specify audit observability [Low]
**Files:** `08_recursive-delegation.md` §8.10 (line 1000, Note block)

When `maxOrphanTasksPerTenant` would be exceeded, the spec falls back from `detach` to `cancel_all` silently: "the gateway falls back to `cancel_all` for that delegation instead of detaching — the children are cancelled rather than orphaned." This is a policy-changing event (a deployer/tenant explicitly requested `detach` and got `cancel_all`) but the spec does not require an audit record or a `child_failed`-style notification to the parent. The only observable is the tenant gauge `lenny_orphan_tasks_active_per_tenant` and the `OrphanTasksPerTenantHigh` alert at 80%.

From a recursive-delegation correctness standpoint, a silent policy downgrade is surprising: an orchestrator that configured `detach` precisely so its sub-trees outlive it will instead see them cancelled with no signal in its own session stream.

**Recommendation:** Either (a) emit a dedicated audit event (e.g., `delegation.orphan_cap_fallback`) with `tenant_id`, `root_session_id`, `requested_policy: detach`, `applied_policy: cancel_all`, and `reason: tenant_orphan_cap_exceeded`; or (b) surface the fallback to the parent session as a terminal annotation on the affected children (e.g., `cascade.fallback` reason on the `child_failed` event), so the orchestrator can detect and react. Preference is (a) in addition to the parent-facing signal.

---

## Summary

Two new findings: one High (DEL-006, a direct regression from DEL-004's fix — the new error code is referenced but not catalogued in §15.1) and one Low (DEL-007, observability gap in the orphan cap fallback path). DEL-001, DEL-002, DEL-003, DEL-005 fixes verified clean with no regressions.
