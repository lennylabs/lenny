# API Design & External Interface Quality Review — Iteration 2

## Prior findings (iter1) — verification

- **API-001 (TENANT_SUSPENDED).** Fixed. Row present in §15.1 catalog at line 632 with category `POLICY`, HTTP 403, and description aligned with the endpoint at line 394.
- **API-002 (If-Match on four PUT endpoints).** Fixed. All admin PUT endpoints now explicitly state "(requires `If-Match`)": `warm-count` (376), `circuit-breaker` (377), `rbac-config` (396), plus the new `users/{user_id}/role` (401) and `roles/{name}` (406) added this cycle.
- **API-003 (ETAG_REQUIRED 428 vs 400).** Fixed. §15.1 at line 742 clearly documents three cases: missing header → `428 ETAG_REQUIRED`, malformed header → `400 VALIDATION_ERROR` with `details.fields`, mismatched version → `412 ETAG_MISMATCH` with `details.currentEtag`.

New error codes called out in the task:
- **`INVALID_CALLBACK_URL`** — present at line 633, `PERMANENT` / 400, clearly scoped to A2A post-V1 and distinguished from `WEBHOOK_VALIDATION_FAILED` (§25.5). Good.
- **`DELEGATION_POLICY_WEAKENING`** — present at line 618, `POLICY` / 403, with `details.parentPolicy` / `details.childPolicy`. Good.
- **`TENANT_SUSPENDED`** — present, see API-001 above.
- **`EXTENSION_COOL_OFF_ACTIVE`** — **NOT present in §15.1.** See API-004 below.
- **`CLASSIFICATION_CONTROL_VIOLATION`** — **NOT present in §15.1.** See API-004 below.

---

## Findings

### API-004 Two Cross-Referenced Error Codes Missing From §15.1 Catalog [Critical]
**Files:** `spec/15_external-api-surface.md` (catalog lines 543–633), `spec/08_recursive-delegation.md` (lines 636–637), `spec/12_storage-architecture.md` (lines 297, 301, 303, 978).

Two error codes are returned by `/v1/*` endpoints (and/or internal flows that surface errors to clients) but are **not listed** in the canonical error-code catalog in §15.1:

1. **`EXTENSION_COOL_OFF_ACTIVE`** — emitted by the extension-request path (`08_recursive-delegation.md` lines 636–637: "the gateway auto-rejects the request with `EXTENSION_COOL_OFF_ACTIVE`"; returned inside a transaction that commits/rolls back budget counters). This is a client-visible rejection reason that appears on lease-extension responses. It has no catalog row, no documented category, no HTTP status, and no `retryable` flag.

2. **`CLASSIFICATION_CONTROL_VIOLATION`** — emitted by:
   - `PUT /v1/admin/tenants/{id}` when the T4 KMS availability probe fails (`12_storage-architecture.md` line 301: "if the probe fails, the update is rejected with `CLASSIFICATION_CONTROL_VIOLATION`").
   - Artifact/checkpoint writes when the tenant-scoped KMS key is unavailable (line 303).
   - Storage-interface boundary when tier mismatches occur (line 978: "rejected at write time with a `CLASSIFICATION_CONTROL_VIOLATION` error").

   This is a first-class admin-API error on `PUT /v1/admin/tenants/{id}` (a visible, operator-facing failure path) and is not listed anywhere in §15.1.

**Impact:** Identical to API-001 in iter1. Section 15.2.1(d) mandates that every error response use a code from the shared taxonomy with identical `code`, `category`, and `retryable` values across REST and every adapter surface; contract tests cannot assert equivalence for codes that have no canonical catalog row. Third-party UIs, SDK generators, and operator runbooks reading §15.1 will not know these codes exist, their HTTP status, or whether they are retryable — leading to divergent client behavior across adapters.

**Recommendation:** Add two rows to the §15.1 error-code catalog, placed next to logically related entries:

- `EXTENSION_COOL_OFF_ACTIVE` | `POLICY` | 403 | "Lease-extension request auto-rejected because the requesting subtree is within its rejection cool-off window after a prior user-denied extension elicitation. `details.subtreeId` and `details.coolOffExpiresAt` are included. Not retryable until cool-off expires or an operator clears the extension-denied flag via `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial`. See [Section 8.x](08_recursive-delegation.md)." Place near `BUDGET_EXHAUSTED` (other delegation/budget rejection).

- `CLASSIFICATION_CONTROL_VIOLATION` | `POLICY` | 422 | "Operation rejected because a storage-tier classification control cannot be satisfied (e.g., tenant T4 KMS key unavailable during the admin-time availability probe or at artifact-write time; T4 data would be written to a store not configured for envelope encryption). Not retryable at the API layer — operator must restore KMS key availability or correct the tier/store configuration. See [Section 12.9](12_storage-architecture.md#129-data-classification)." Place near `COMPLIANCE_PGAUDIT_REQUIRED` / `COMPLIANCE_SIEM_REQUIRED` (other compliance-profile errors).

The HTTP status for `CLASSIFICATION_CONTROL_VIOLATION` at admin time should match the surrounding compliance family (422 for config-level rejection); the artifact-write path is internal and surfaces through the critical alert `CheckpointStorageUnavailable` rather than a client HTTP response, so a single 422 status row is sufficient.

---

## No additional real issues found

- **REST/MCP parity (§15.2.1).** The contract-test matrix at line 895 lists canonical error classes exercised across adapters. Both of the missing codes above should be eligible for inclusion there once they are cataloged.
- **If-Match coverage.** Every admin PUT/PATCH endpoint in the endpoint tables (16 total) now carries "(requires `If-Match`)" — the prior asymmetry is gone. Non-admin PUT (`/v1/credentials/{credential_ref}`) is correctly out of scope since §15.1 scopes the rule to admin resources.
- **`dryRun` semantics.** Clean: exceptions are explicitly enumerated at line 737 (`DELETE`, `drain`, `force-terminate`, `warm-count`), ETag interaction is documented at line 735, and per-endpoint `dryRun` behavior is covered for connectors, experiments, and environments.
- **Error envelope and validation.** Canonical envelope shape, field definitions, and `VALIDATION_ERROR` details structure are consistent and clear.

---

## Summary

**Critical (1):** API-004 — Two error codes (`EXTENSION_COOL_OFF_ACTIVE`, `CLASSIFICATION_CONTROL_VIOLATION`) referenced across the spec but missing from the §15.1 canonical catalog. Same class of regression as iter1 API-001, on newly added error codes that were not backfilled into the catalog when introduced.

No other regressions detected. iter1 API-001 / API-002 / API-003 fixes all hold.
