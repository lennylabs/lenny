# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 14 — API Design & External Interface Quality
**Iteration:** 14
**Prior findings carried forward:** 0 (API-048 and API-049 from iter13 are both fixed)
**New findings:** 4
**Total findings this iteration:** 4 (0 Critical, 0 High, 4 Medium)

## Prior findings — resolved

| ID | Status | Verification |
|----|--------|-------------|
| API-048 | FIXED | `DEADLOCK_TIMEOUT` now correctly maps to HTTP 504 (line 6390), not 408. |
| API-049 | FIXED | `ETAG_REQUIRED` appears only once in the error catalog (line 6361); no duplicate entry. |

## New Findings

| # | ID | Severity | Finding | Section |
|---|------|----------|---------|---------|
| 1 | API-050 | MEDIUM | **`PUT` used for side-effecting action endpoint `validate`.** `PUT /v1/admin/external-adapters/{name}/validate` (line 6305, repeated at line 8670) runs the `RegisterAdapterUnderTest` compliance test suite, transitions adapter status (`pending_validation` -> `active` or `validation_failed`), and is neither idempotent nor a resource replacement — it is an action with side effects. Every other action endpoint in the admin API uses POST (drain, force-terminate, revoke, test, rotate-token, etc.). This endpoint should be `POST /v1/admin/external-adapters/{name}/validate` for consistency with the spec's own conventions and HTTP method semantics. | 15.1 |
| 2 | API-051 | MEDIUM | **`resuming` missing from external session state table but used in endpoint preconditions.** The external session state table (lines 6127-6140) does not list `resuming` as an externally visible state. Line 6142 explicitly lists `resuming` among "internal-only states" that are "never returned in external API responses." However, the `derive` endpoint precondition table (line 6122) lists `resuming` as a valid precondition state for non-terminal derives, and the `resume` endpoint (line 6120) shows a resulting transition through `resuming`. The session state model in Section 7.2 (line 2180) clearly defines `resuming` as a session-level state with its own failure transitions (lines 2194-2197). If `resuming` is externally visible (as the endpoint tables imply), it must be added to the external state table and removed from the internal-only list at line 6142. If it is truly internal-only, then the `derive` precondition must not reference it, and the `resume` resulting-transition column should show only the client-visible bookend states. | 15.1, 7.2 |
| 3 | API-052 | MEDIUM | **`terminate` endpoint precondition list omits `starting`, contradicting its own description.** The `terminate` endpoint (line 6119) describes itself as "Valid in any non-terminal, non-setup state" but the precondition column lists only `running`, `suspended`, `resume_pending`, `awaiting_client_action`. The external state table lists `starting` as a non-terminal, non-setup state ("Agent runtime is launching"), so the description implies `starting` should be a valid precondition. Either add `starting` to the precondition list, or change the description to explicitly enumerate valid states instead of using the "non-setup" shorthand. | 15.1 |
| 4 | API-053 | MEDIUM | **`dryRun` blanket claim "all admin POST and PUT" contradicts explicit exclusions.** Line 6470 states "All admin `POST` and `PUT` endpoints accept `?dryRun=true`." Line 6533 then says "`DELETE` endpoints do not support `dryRun` ... Action endpoints (`drain`, `force-terminate`, `warm-count`) do not support `dryRun`." The drain endpoint is POST; force-terminate is POST; warm-count is PUT. The blanket "all" is false. Additionally, several other action-style admin POST endpoints (e.g., `rotate-erasure-salt`, `billing-corrections`, `legal-hold`, `force-delete`, `preflight`, `quota/reconcile`, `rotate-token`, credential revoke endpoints) are not listed in the dryRun support table (lines 6514-6529) and likely should not support dryRun (their value is the side effect). The blanket statement should be narrowed to "All admin CRUD `POST` (create) and `PUT` (update) endpoints accept `?dryRun=true`" to match the actual support table and explicit exclusions. | 15.1 |

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **Error catalog completeness:** All error codes referenced in prose (INCOMPATIBLE_RUNTIME, REPLAY_ON_LIVE_SESSION, DERIVE_ON_LIVE_SESSION, POOL_DRAINING, CIRCUIT_BREAKER_OPEN, etc.) have matching entries in the error catalog table.
- **Error code HTTP status consistency:** HTTP status codes match their semantic intent (400 for client errors, 403 for authz, 404 for not found, 409 for conflicts, 412/428 for conditional requests, 422 for unprocessable, 429 for rate/quota, 500-504 for server errors).
- **ETag-based concurrency:** Consistent description across ETag rules, admin PUT requirements, dryRun+If-Match interaction, and DELETE optional etag.
- **Cursor-based pagination:** Envelope schema, parameter defaults, expiry, and listed endpoints are consistent.
- **REST/MCP consistency contract (Section 15.2.1):** Five rules are well-specified with concrete test matrix and machine-enforceable gate.
- **Error response envelope:** Schema is fully specified with all required fields and per-code details structures.
- **Rate-limit headers:** Standard set with Retry-After on 429.
- **OpenAPI spec endpoint:** Canonical source correctly identified.
- **API versioning (Section 15.5):** Clear versioning rules for REST, MCP, CRDs, and runtime adapter protocol.
- **Webhook callback security (Section 14):** SSRF mitigations (URL validation, DNS pinning, redirect blocking, domain allowlist) are thorough.
- **Connector test endpoint:** Properly separated from dryRun validation with its own rate limit.
- **Deletion semantics:** RESOURCE_HAS_DEPENDENTS with per-type blocking rules and truncated ID arrays are well-designed for third-party UIs.
