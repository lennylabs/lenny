# API Design & External Interface Quality Review

## Findings

### API-001 Missing Error Code in Catalog [Critical]
**Files:** `15_external-api-surface.md` (line 394, error catalog lines 532-606)

The `/v1/admin/tenants/{id}/suspend` endpoint explicitly documents that session creation and message injection are "rejected with `TENANT_SUSPENDED`", but this error code is **not defined in the error code catalog table** (Section 15.1, lines 532-606). 

The error code `TENANT_SUSPENDED` is mentioned in:
- Line 394: endpoint description for `POST /v1/admin/tenants/{id}/suspend`

But it does not appear in the canonical error code table. This violates the contract test requirement (Section 15.2.1(d), line 871) that all error responses must use codes from the shared taxonomy and be testable for identical `code`, `category`, and `retryable` values across REST and MCP surfaces.

**Details:** Without the error code defined in the catalog, clients cannot reliably handle this error condition, and contract tests cannot assert behavioral equivalence across adapters.

**Recommendation:** Add `TENANT_SUSPENDED` to the error code catalog table with category `POLICY`, HTTP status `403`, and description: "Tenant is suspended. New session creation and message injection are rejected. The suspension is recorded in the audit trail. Wait for tenant resumption or contact administrators."

---

### API-002 Inconsistent If-Match Requirement Documentation [High]
**Files:** `15_external-api-surface.md` (lines 376-377, 396, general rule at line 730)

Section 15.1 states at line 730: "Every admin `PUT` request **must** include an `If-Match` header." However, three PUT endpoints do **not mention this requirement** in their endpoint descriptions:

1. **Line 376:** `PUT /v1/admin/pools/{name}/warm-count` — "Adjust minWarm/maxWarm at runtime" (no `If-Match` mentioned)
2. **Line 377:** `PUT /v1/admin/pools/{name}/circuit-breaker` — describes override behavior (no `If-Match` mentioned)  
3. **Line 396:** `PUT /v1/admin/tenants/{id}/rbac-config` — "Set tenant RBAC configuration" (no `If-Match` mentioned)

In contrast, other PUT endpoints explicitly state `requires `If-Match``:
- Line 353: `PUT /v1/admin/runtimes/{name}` — "(requires `If-Match`; ...)"
- Line 361: `PUT /v1/admin/delegation-policies/{name}` — "(requires `If-Match`)"
- Line 372: `PUT /v1/admin/pools/{name}` — "(requires `If-Match`; ...)"

This asymmetry creates ambiguity: third-party clients and SDK generators reading the endpoint tables cannot confidently determine whether these three endpoints require `If-Match` or are exceptions to the rule.

**Impact:** REST/MCP contract consistency (Section 15.2.1(d)) requires identical validation and error behavior across surfaces. Clients that infer requirements from endpoint descriptions will behave inconsistently.

**Recommendation:** Explicitly add "(requires `If-Match`)" to the endpoint descriptions for `warm-count`, `circuit-breaker`, and `rbac-config` PUT endpoints to match the pattern of other mutable endpoints, or document in a note that these are exceptions to the global If-Match requirement with justification for why optimistic concurrency is not needed for these specific operations.

---

### API-003 Missing HTTP Status Code for ETAG_REQUIRED Error [Medium]
**Files:** `15_external-api-surface.md` (line 542)

The error code `ETAG_REQUIRED` at line 542 maps to HTTP status `428 Precondition Required`. However, line 730 states the gateway "returns `428 Precondition Required`" but the OpenAPI spec / REST contract does not appear to document whether this status is auto-generated or whether clients must be prepared for other edge cases (e.g., missing header parsing errors returning `400` instead). This is a minor documentation gap but affects API consumer expectations.

**Recommendation:** Confirm the HTTP status mapping in the error code table is the only possible status for missing `If-Match`, or document the edge cases in a note below the error code table.

---

## No Additional Real Issues Found

The spec demonstrates strong API design discipline:
- Error taxonomy is comprehensive and well-categorized (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`)
- REST/MCP consistency contract is well-specified with testable rules (Section 15.2.1)
- ETag-based optimistic concurrency is cleanly designed with clear retry patterns
- dryRun behavior is documented with explicit exceptions (warm-count, drain, force-terminate, DELETE)
- Pagination, rate-limiting, and validation error formats are consistent across endpoints

The three issues above are genuinely fixable inconsistencies in documentation rather than architectural flaws.

---

## Summary

**Critical (1):** Missing error code definition blocks contract testing and client error handling.
**High (1):** Inconsistent If-Match documentation creates ambiguity for SDK generators and clients.
**Medium (1):** HTTP status mapping edge case needs clarification.

All are narrow, documentation-level fixes with no code changes required. Recommend batch fix before next iteration.
