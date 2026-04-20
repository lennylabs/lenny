# Iter3 API Review

## Prior findings — regression check

- **API-001 (TENANT_SUSPENDED).** Holds. `spec/15_external-api-surface.md:826` — row present, `POLICY` / 403, wording matches the endpoint at line 584.
- **API-002 (If-Match on admin PUTs).** Holds. All 14 admin `PUT` endpoints (lines 543–617) and the one admin `PATCH` (line 611, experiments) explicitly state `(requires If-Match)`. The `PUT /v1/credentials/{credential_ref}` at line 480 is correctly excluded (non-admin). The `dryRun` table entries at 913–927 use the terse "checks etag" phrasing, which is unambiguous in context.
- **API-003 (ETAG_REQUIRED vs VALIDATION_ERROR vs ETAG_MISMATCH).** Holds. §15.1 at line 936 documents the three distinct cases (428 / 400 / 412) cleanly.
- **API-004 (EXTENSION_COOL_OFF_ACTIVE, CLASSIFICATION_CONTROL_VIOLATION).** Fixed. Rows present at line 814 (`POLICY` / 403) and line 812 (`POLICY` / 422) respectively. Wording aligns with their cross-references in §8.6 and §12.5 / §12.9.

The iter2 additions for `INVALID_CALLBACK_URL` (line 827), `DELEGATION_POLICY_WEAKENING` (line 810), `CONTENT_POLICY_WEAKENING` (line 808), and `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` (line 809) are all cleanly catalogued. SDK package names in §15.6 / §15.7 (lines 2094–2098, 2113–2117) and the Results API query parameters (line 613) are consistent.

---

## Findings

### API-005 Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows with divergent HTTP status [High]
**Files:** `spec/15_external-api-surface.md:771` (row 1), `spec/15_external-api-surface.md:800` (row 2); cross-refs at `spec/15_external-api-surface.md:514`, `spec/15_external-api-surface.md:1089`; `spec/07_session-lifecycle.md:98`; `spec/10_gateway-internals.md:537`; `spec/08_recursive-delegation.md:253`, `:255`.

The canonical §15.1 error catalog now contains **two rows for the same `code`** — `ISOLATION_MONOTONICITY_VIOLATED` — with **different HTTP statuses** and different `details` field contracts:

1. Line 771: `POLICY` | **403** | "Delegation rejected because the target pool's isolation profile is less restrictive than the calling session's `minIsolationProfile`. `details.parentIsolation` and `details.targetIsolation` identify the conflicting profiles."
2. Line 800: `POLICY` | **422** | "`POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` (with `replayMode: workspace_derive`) rejected because the target pool's `sessionIsolationLevel.isolationProfile` is weaker… `details.sourceIsolationProfile`, `details.targetIsolationProfile`, and `details.targetPool` are included."

This is a regression introduced by the iter2 SEC-001 fix: a new row was appended for the derive/replay path rather than merging into the existing delegation row. It creates two concrete problems:

**Problem 1 — error catalog non-uniqueness.** The catalog is the canonical reference mentioned at §15.2.1(d) ("same error `code`… for identical invalid inputs") and at `spec/15_external-api-surface.md:1089` which lists `ISOLATION_MONOTONICITY_VIOLATED` as a contract-test code exercised against "a canonical triggering input". With two rows there are two canonical triggering inputs and two HTTP statuses for the same `code`; the contract test cannot assert a single authoritative status without first choosing which row applies. Clients, SDK generators, OpenAPI derivers, and docs tooling that scrape the catalog will encounter an ambiguous duplicate key.

**Problem 2 — inconsistent HTTP semantics.** Both triggering contexts (delegation admission and derive/replay admission) are pre-execution policy rejections of the same rule (isolation monotonicity). Returning 403 in one context and 422 in another for identical `code`/`category`/`retryable` is not semantically justified:
- 403 (delegation path) is defensible — this is a policy/authz decision: "the caller is not allowed to target a pool with weaker isolation."
- 422 (derive/replay path) was introduced at line 514 ("rejected with `ISOLATION_MONOTONICITY_VIOLATED` (HTTP 422)") and at `spec/07_session-lifecycle.md:98`. 422 is usually reserved for semantic payload validation, which this is not — the request body is syntactically and semantically well-formed; the rejection is a policy check against the referenced pool.

**Impact:** Medium-to-high for SDK generators and third-party runbooks (OpenAPI derivation from the catalog will emit a duplicate definition or silently keep only the last row). Low for well-behaved handwritten clients that key on `code` rather than HTTP status, but those clients will still see inconsistent statuses depending on which endpoint rejected them.

**Recommendation:** Choose **one** HTTP status for `ISOLATION_MONOTONICITY_VIOLATED` and merge the two rows. Two viable options:

- **Option A (preferred): collapse to a single 403 row.** This matches the delegation precedent and the `POLICY` category. Update line 514 ("HTTP 422") and `spec/07_session-lifecycle.md:98` ("HTTP 422") to say "HTTP 403". Merge the `details` schema to enumerate all fields: `details.sourceIsolationProfile`, `details.targetIsolationProfile`, `details.targetPool`, with `details.parentIsolation`/`details.targetIsolation` kept as aliases for the delegation path if preserving delegation-context wire compatibility matters.

- **Option B: keep 422 for derive/replay if 422 is deliberate.** Use a different error `code` for the derive/replay path (e.g., `DERIVE_ISOLATION_MONOTONICITY_VIOLATED`) so the catalog retains unique codes. This is the less preferred option because the rule being enforced is identical in both contexts and a second code adds client-side branching for no semantic gain.

Whichever is chosen, ensure §15.2.1(d)'s contract-test matrix at line 1089 exercises exactly one canonical triggering input per catalog row.

---

### API-006 Duplicate-key key-ordering in §15.1 catalog has no invariant [Low]
**Files:** `spec/15_external-api-surface.md:771`, `:800`.

Related to API-005: the catalog is a markdown table with no primary-key statement. Iter1 established `code` as implicit primary key ("Every error response must use a code from the shared taxonomy"), but neither the catalog header (line 745 area) nor §15.2.1(d) explicitly says the `code` column must be unique within the catalog. If the catalog is meant to allow per-endpoint context rows for the same code (as it now does for `ISOLATION_MONOTONICITY_VIOLATED`), that needs to be stated. Otherwise duplicates will recur.

**Recommendation:** Add a single sentence near the catalog header (around line 745 / start of the error-code table) stating the invariant, e.g.: "Each `code` appears at most once in this table; the `httpStatus`, `category`, and `retryable` columns are per-code invariants. Per-endpoint descriptions of the same code live in the endpoint table and in the referenced section, not here." This makes the uniqueness contract explicit and protects against future regressions of the API-005 class.

---

## No additional issues

- **If-Match coverage, ETag semantics, `dryRun` exceptions** all remain consistent with iter2.
- **Rate-limit headers** (lines 859–866) are unchanged and correct.
- **Cursor-based pagination envelope** (lines 954–977) is clean; aggregated-endpoint exceptions (`/v1/usage`, `/v1/admin/experiments/{name}/results`) are explicitly called out at lines 954 and 981.
- **§15.4 Runtime Adapter Specification** machine-readable artifacts (lines 1101–1107) are consistent; the `OutputPart`, adapter-JSONL, and gRPC proto schemas are named and versioned.
- **§15.5 versioning** (lines 2042–2086) is unchanged — REST path-prefix, 6-month deprecation window, live vs durable consumer forward-read rules are intact.
- **§15.6 / §15.7 SDK package references** are canonical and distinct (Client SDKs vs Runtime Author SDKs explicitly contrasted at line 2099 and 2105).
- **Audit-family error codes** (`AUDIT_STORE_UNAVAILABLE`, `AUDIT_QUERY_TOO_BROAD`, `AUDIT_EVENT_NOT_FOUND`, `AUDIT_PARTIAL_RESULTS`) legitimately live in §25.5 (agent-operability), consistent with the "Section 25 adds to per subsection" clause at line 701.

---

## Summary

**High (1):** API-005 — Duplicate `ISOLATION_MONOTONICITY_VIOLATED` rows in §15.1 with different HTTP statuses (403 vs 422). Introduced by the iter2 SEC-001 fix when the derive/replay path was added as a new row instead of merging into the existing delegation row. Breaks the implicit uniqueness invariant of the catalog and creates ambiguous contract-test targets.

**Low (1):** API-006 — Catalog uniqueness invariant is not stated; one-line fix to prevent future regressions of the same class.

Iter1 (API-001/002/003) and iter2 (API-004) fixes all hold.
