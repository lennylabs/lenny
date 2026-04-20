# Iter3 DEL Review

## Prior-finding verification

- **DEL-001, DEL-002, DEL-003, DEL-005** — Verified fixed in iter2; no regressions. Spec references at `08_recursive-delegation.md:175-181` (inheritance), `08:161-165` and `08:945` (interceptor snapshot scope), `08:941` (recovery policy invariance), `08:377-393` (multi-hop origin pool).
- **DEL-004 (cool-off durability)** — Fix still present at `08:635-638`. No new regression observed; `EXTENSION_COOL_OFF_ACTIVE` error catalog entry is now in place at `15_external-api-surface.md:814` (HTTP 403, `POLICY`, `details.subtreeId`, `details.coolOffExpiresAt`), closing the iter2 DEL-006 gap.
- **DEL-006 (EXTENSION_COOL_OFF_ACTIVE catalogue)** — Fixed in iter2 commit 2a46fb6. The §15.1 entry at line 814 carries the correct category (`POLICY`), status (`403`), details fields, and cross-link to §8.6 and the admin extension-denial clear endpoint. Consumers can now distinguish it from `BUDGET_EXHAUSTED`. **Minor note:** the status is `403` rather than the iter2-recommended `429` — this is a defensible choice (cool-off is a denial, not a rate limit) and is internally consistent; not flagged as a regression.
- **DEL-007 (orphan tenant-cap fallback audit observability)** — **Not fixed.** `08:1000` still describes a silent `detach → cancel_all` fallback with no dedicated audit event and no parent-facing signal on the affected children. Carried forward as DEL-008 below with refined recommendation.

---

## New findings

### DEL-008 Orphan tenant-cap fallback still emits no audit event or parent signal [Low]
**Files:** `08_recursive-delegation.md` §8.10 (line 1000, Note block); `11_policy-and-controls.md` §11.7 (audit event catalog); `16_observability.md` (alerting rules)

Carried over from iter2 DEL-007 — verified unfixed. The `maxOrphanTasksPerTenant` cap silently downgrades a deployer-requested `cascadeOnFailure: detach` to `cancel_all` without (a) an audit event in §11.7 (only a gauge `lenny_orphan_tasks_active_per_tenant` and an `OrphanTasksPerTenantHigh` threshold alert exist, both of which fire on the *cap*, not on the *individual fallback decision*), and (b) any signal to the parent session's event stream. An orchestrator that configured `detach` because it intends its sub-trees to outlive it will observe its children being cancelled with no correlated reason in its own stream — only a `child_failed` / `child_cancelled` event with no `reason: tenant_orphan_cap_exceeded` annotation. From the DEL perspective this is a correctness gap in how a policy-changing gateway decision is communicated across the delegation boundary.

**Recommendation:** Add both signals:
1. Emit a `delegation.orphan_cap_fallback` audit event (category `POLICY`, per §11.7) with `tenant_id`, `root_session_id`, `parent_session_id`, `requested_policy: detach`, `applied_policy: cancel_all`, `reason: tenant_orphan_cap_exceeded`, `orphan_count_at_fallback`, `orphan_cap`.
2. Annotate the parent-facing `child_cancelled` / cascade-notification events for the affected children with `cascade.reason: tenant_orphan_cap_exceeded` (new enum value) so the orchestrator agent can programmatically detect and respond (e.g., by halting its next `detach`-mode delegation).

---

### DEL-009 `DELEGATION_PARENT_REVOKED` and `DELEGATION_AUDIT_CONTENTION` not catalogued in §15.1 [High]
**Files:** `08_recursive-delegation.md` §8.2 (lines 61, 63); `15_external-api-surface.md` §15.1 error catalog (~ lines 740-828)

Section 8.2's iter2 hardening of the delegation token-exchange flow introduced two wire-level error codes returned by `lenny/delegate_task`:

- Line 61: **`DELEGATION_PARENT_REVOKED`** — returned when the parent's `actor_token` resolves to a revoked `jti` inside the token-exchange transaction (closes the race where a stale parent token mints a child that outlives the parent).
- Line 63: **`DELEGATION_AUDIT_CONTENTION`** (labelled retriable) — returned when the per-tenant audit advisory lock times out during the child-token exchange. Spec explicitly instructs clients to retry the **entire** `lenny/delegate_task` call.

Neither identifier is present in the §15.1 error catalog. This is the same regression pattern as the iter2 DEL-006 finding (`EXTENSION_COOL_OFF_ACTIVE` referenced but not catalogued) and has the same consequences for SDKs and adapters:

1. No canonical HTTP status, `category`, or `retryable` flag available to SDK code generators.
2. Clients cannot reliably distinguish `DELEGATION_PARENT_REVOKED` (terminal — the parent truly is revoked; no retry makes sense) from `DELEGATION_AUDIT_CONTENTION` (transient — spec mandates retry) without spec-defined semantics. A client that maps both to a generic "delegation failed" will either miss the required retry or retry endlessly on a revocation.
3. The `details` schema is undefined — clients cannot surface which `jti` was revoked, nor the contention window, to operators.

This gap is load-bearing because §8.2 instructs the *caller* to implement retry behavior based on these codes; without a catalog, SDK authors have no compliant reference.

**Recommendation:** Add two entries to the §15.1 error catalog adjacent to `DELEGATION_CYCLE_DETECTED` and `DELEGATION_POLICY_WEAKENING`:

- `DELEGATION_PARENT_REVOKED` — `category: POLICY`, `status: 401` (authentication/authorisation failure — the `actor_token` is no longer valid), `retryable: false`. `details.parentSessionId`, `details.parentJti`, `details.revocationReason` (optional: `rotated`, `admin_revoked`, `recursive_revocation`). Cross-link to §8.2 and §13.3 token rotation/revocation.
- `DELEGATION_AUDIT_CONTENTION` — `category: TRANSIENT`, `status: 503`, `retryable: true`. `details.rootSessionId`, `details.retryAfterMs`. `Retry-After` header populated per §15.1 convention. Cross-link to §8.2 and §11.7 audit logging.

---

### DEL-010 `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` not catalogued in §15.1 [Medium]
**Files:** `08_recursive-delegation.md` §8.5 (line 445, `lenny/get_task_tree` description); `15_external-api-surface.md` §15.1 error catalog

The delegation-lease validation rule — `messagingScope: siblings` requires `treeVisibility: full`; the gateway rejects the combination at delegation time with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` — is described only inline in §8.5's `lenny/get_task_tree` table row. The error code appears nowhere else in the spec: no §15.1 catalog entry, no `details` fields, no retryability classification.

This is a DEL concern because it is a **child-lease construction rejection** raised by the gateway as part of `lenny/delegate_task` admission (same admission pipeline as `DELEGATION_POLICY_WEAKENING` and `CONTENT_POLICY_WEAKENING`, both of which are catalogued). Clients that request `messagingScope: siblings` without realising that the parent's lease has `treeVisibility: self-only` or `parent-and-self` have no spec-canonical way to handle the rejection.

**Recommendation:** Add an entry to §15.1:

`TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` — `category: PERMANENT`, `status: 400`, `retryable: false`. `details.requestedMessagingScope` (`siblings`), `details.effectiveTreeVisibility` (`self-only` or `parent-and-self`), `details.requiredTreeVisibility` (`full`). Cross-link to §8.5 and §7.2 (messaging scope table).

---

### DEL-011 `tracingContext` validation error codes not catalogued in §15.1 [Low]
**Files:** `08_recursive-delegation.md` §8.3 (lines 232-238); `15_external-api-surface.md` §15.1 error catalog

The `tracingContext` validation table at `08:232-238` defines three error codes — `TRACING_CONTEXT_TOO_LARGE`, `TRACING_CONTEXT_SENSITIVE_KEY`, `TRACING_CONTEXT_URL_NOT_ALLOWED` — that the gateway raises when processing `lenny/set_tracing_context` or validating the propagated context on `lenny/delegate_task`. None of the three codes appears in the §15.1 catalog. Because tracing context propagation is a DEL platform primitive (the gateway auto-attaches parent context to the child lease, and parent-child merge rules are DEL-enforced), this is in-scope for DEL review.

Severity is Low because these errors are operator-facing (runtime authors, not LLM-directed) and the in-section table already gives enough information to implement, but consistency with the catalog-completeness convention still warrants action.

**Recommendation:** Add three entries to §15.1:

- `TRACING_CONTEXT_TOO_LARGE` — `PERMANENT`, 413, not retryable. `details.limit` (one of `size_bytes`, `key_length`, `value_length`, `entry_count`), `details.observed`, `details.max`.
- `TRACING_CONTEXT_SENSITIVE_KEY` — `PERMANENT`, 400, not retryable. `details.key` (the offending key name), `details.matchedPattern`.
- `TRACING_CONTEXT_URL_NOT_ALLOWED` — `PERMANENT`, 400, not retryable. `details.key`, `details.valuePrefix` (first 32 chars, for debugging).

Cross-link all three to §8.3 tracingContext validation table and §16.3 distributed tracing.

---

## Summary

Four findings total. One Low carried forward from iter2 (DEL-008 = iter2 DEL-007, orphan fallback observability — still unaddressed). Three new: one High (DEL-009, two uncatalogued delegation error codes — same regression class as the iter2 DEL-006 finding, indicating the iter2 fix did not sweep the broader §8.2 surface), one Medium (DEL-010, `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`), one Low (DEL-011, three `TRACING_CONTEXT_*` codes). Iter2 fixes for DEL-001 through DEL-006 are all verified clean. No semantic regressions in inheritance, budget propagation, cycle detection, isolation monotonicity, or recovery policy invariance were observed. The cross-spec isolation monotonicity alignment between §8.3 and §7.1 / §15.1 (derive and replay paths) is consistent.
