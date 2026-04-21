# Perspective 14 — API Design & External Interface Quality (iter6)

**Scope.** Re-review of the external API surface (`spec/15_external-api-surface.md`) and MCP consistency (`spec/09_mcp-integration.md`, `spec/25_agent-operability.md` §25.12) focused on iter5's five new admin-API additions:

1. POL-023 — `POST /v1/admin/circuit-breakers/{name}/open` (plus companion list/get/close) with `INVALID_BREAKER_SCOPE` (PERMANENT/422).
2. CMP-054 — `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (PERMANENT/422), raised by the Phase 3.5 tenant force-delete lifecycle.
3. CMP-057 — `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` (POLICY/422) on `PUT /v1/admin/tenants/{id}` and the new decommission endpoint `POST /v1/admin/tenants/{id}/compliance-profile/decommission`.
4. CMP-058 — `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (POLICY/422), raised on platform-tenant audit writes targeting a regulated tenant.
5. CNT-015 — `GIT_CLONE_REF_UNRESOLVABLE` (PERMANENT/422) on session create, paired with the gateway-written `sources[<n>].resolvedCommitSha` field surfaced in `GET /v1/sessions/{id}`.

Verified consistency with existing API style (`/v1/admin/*` path prefix, RBAC role labelling, ETag semantics, dry-run semantics), canonical `{code, category, retryable, httpStatus}` error-tuple discipline, the §15.2.1 REST/MCP consistency contract, the `RegisterAdapterUnderTest` session-creation rejection matrix (§15.2.1), the `x-lenny-*` OpenAPI extension contract (§15.1 "Admin API MCP extension contract"), the closed-set scope taxonomy (§15.1 "Scope taxonomy"), and OpenAPI/docs parity in `docs/api/admin.md`, `docs/reference/error-catalog.md`, `docs/reference/workspace-plan.md`, and `docs/runbooks/circuit-breaker-open.md`.

**Calibration.** iter6 severities anchored to the iter1–iter5 rubric per `feedback_severity_calibration_iter5.md`. No severity drift on carry-forwards; editorial / documentation hardening items remain **Low**; contract/test-matrix invariants and scope-taxonomy closure are **Medium** consistent with how iter4 API-013 and iter3 API-006 were graded.

**Numbering.** iter5 P14 ended at API-019 (three Low carry-forwards). This perspective uses API-020 onward.

---

## 1. Carry-forward verification (iter5 Low)

| iter5 finding | iter6 disposition | Evidence |
| --- | --- | --- |
| API-017 catalog uniqueness invariant not stated at §15.4 header [Low] | **Not fixed** — carry-forward (Low). The error-code catalog header at `spec/15_external-api-surface.md:964` does not carry the one-sentence invariant ("each `code` appears at most once in this table and carries a single `(category, httpStatus, retryable)` tuple") that API-017 recommended. The §15.2.1 contract still assumes the invariant (rule 3, line ~1368) but it is not asserted at the point-of-edit. Grep of §15.4 for "Each `code` appears at most once" or "uniqueness" returns no match against the header neighbourhood. |
| API-018 `UNREGISTERED_PART_TYPE` row uses `WARNING` category outside canonical taxonomy [Low] | **Not fixed** — carry-forward (Low). `spec/15_external-api-surface.md:1040` still reads `\| UNREGISTERED_PART_TYPE \| WARNING \| — \|`. The canonical taxonomy stated at line 965 still restricts `category` to `TRANSIENT \| PERMANENT \| POLICY \| UPSTREAM`, so the row remains taxonomically out-of-set. No footnote or relocation to §15.4.1 was added. |
| API-019 `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 PERMANENT for a known operator-action failure path [Low] | **Not fixed** — carry-forward (Low). `spec/25_agent-operability.md:4334` still lists `RESTORE_ERASURE_RECONCILE_FAILED \| PERMANENT \| 500`. The description still enumerates the legal-hold-ledger-stale sub-reason as the same code/status, preserving the POLICY-vs-INTERNAL conflation. |

Each carry-forward remains a documentation / classification hardening item with no runtime contract impact under the iter5 test matrix; none blocks convergence.

---

## 2. New findings (iter6)

### API-020. `/v1/admin/circuit-breakers/*` endpoints absent from the §15.1 endpoint table despite being the sole referent of `INVALID_BREAKER_SCOPE` [Medium]

**Status: Fixed** — Added four `/v1/admin/circuit-breakers/*` rows (list/get/open/close) to the §15.1 endpoint table in `spec/15_external-api-surface.md` before `POST /v1/admin/preflight`; each row cross-references §11.6 and names `INVALID_BREAKER_SCOPE` for the open endpoint.

**Section:** `spec/15_external-api-surface.md` §15.1 endpoint table (lines 773–886) and the "The table above includes all endpoints" claim at line 888.

The §15.4 catalog row for `INVALID_BREAKER_SCOPE` at line 1026 names the endpoint `POST /v1/admin/circuit-breakers/{name}/open` as its sole source. The `CIRCUIT_BREAKER_OPEN` row at line 1025 references the breaker machinery generally. The admin-API circuit-breaker surface has four endpoints — `GET /v1/admin/circuit-breakers`, `GET /v1/admin/circuit-breakers/{name}`, `POST /v1/admin/circuit-breakers/{name}/open`, `POST /v1/admin/circuit-breakers/{name}/close` — defined in `spec/11_policy-and-controls.md:308–313` and documented in `docs/api/admin.md:708–752`. None of these four rows appear in the §15.1 REST API endpoint table (verified by grep of `spec/15_external-api-surface.md` for `circuit-breaker` — only the SDK-warm pool breaker override `PUT /v1/admin/pools/{name}/circuit-breaker` at line 801 is catalogued; the operator-managed breaker class is absent).

Line 888 explicitly asserts: "**Additional operational endpoints** are defined in [Section 24](24_lenny-ctl-command-reference.md) … The table above includes all endpoints". This statement is now false as written — iter5 introduced admin endpoints that are neither in the §15.1 table nor forward-referenced from it to §11.6. The endpoint is reachable, OIDC-scope-gated, and produces a catalogued error code (`INVALID_BREAKER_SCOPE`) whose description points readers to `POST /v1/admin/circuit-breakers/{name}/open` — so readers of §15 cannot trace the error back to its endpoint without jumping to §11.6.

This also breaks the §15.1 `x-lenny-*` MCP extension contract at line 917 ("Every admin-API endpoint with documented RBAC MUST be exposed as an MCP tool on `/mcp/management`") — the contract is evaluated per OpenAPI entry, and its CI check at line 927 ("A build-time check fails the build if any admin-API endpoint lacks `x-lenny-mcp-tool`") needs a single source-of-truth for what "admin-API endpoint" means. Today that is the §15.1 table. Silent endpoints are invisible to the contract check.

**Recommendation:** Add the four `/v1/admin/circuit-breakers/*` rows to the §15.1 admin-API endpoint table at lines 773–886, alongside the existing `PUT /v1/admin/pools/{name}/circuit-breaker` row (line 801). Each row should cite `[Section 11.6](11_policy-and-controls.md#116-circuit-breakers)` and name the error code it produces (`INVALID_BREAKER_SCOPE` on `POST .../open`). Either (a) update line 888 to read "The table above includes all REST API endpoints except the web-playground auth and MCP-management surfaces enumerated below" and align the table with the assertion, or (b) keep the assertion and make it true by adding the missing rows. Option (b) is preferred because it preserves the single-source-of-truth pattern iter4 API-013 established for §15.1 as the MCP-extension contract's input.

---

### API-021. `circuit_breaker` domain missing from the §15.1 closed scope taxonomy despite being required to invoke the new endpoints [Medium]

**Status: Fixed** — Added `circuit_breaker` to the closed scope taxonomy at `spec/15_external-api-surface.md:911`, consistent with `credential_pool` / `tenant` naming convention.

**Section:** `spec/15_external-api-surface.md:911` (scope taxonomy domain list) and line 927 (CI contract).

Line 911 states the closed scope taxonomy: `pool, health, diagnostics, recommendations, runbooks, events, audit, drift, backup, restore, upgrade, locks, escalation, logs, me, operations, tenant, credential_pool, credential, runtime, quota, config`. Line 915 declares: "This list is the source-of-truth; new domains must be added here before being introduced in handlers." Line 927 asserts a CI contract: "An additional check asserts that every `x-lenny-scope` value conforms to `tools:<domain>:<action>` syntax and its domain is in the taxonomy above."

The four iter5 circuit-breaker admin endpoints (per API-020 above) are admin-API endpoints requiring a `platform-admin` role — which means they MUST carry an `x-lenny-scope` claim per the line 917 "MCP extension contract" ("Every admin-API endpoint with documented RBAC MUST be exposed as an MCP tool"). Any plausible scope name is `tools:circuit_breaker:read` (list/get) and `tools:circuit_breaker:write` (open/close), or `tools:breaker:*`, or `tools:admission:*` — none of these domains appears in the closed taxonomy at line 911. The CI contract at line 927 would reject any `x-lenny-scope` that the OpenAPI document declares for these handlers.

This creates a three-way inconsistency:
1. §15.1 line 915 says domains must be added to the taxonomy before handlers use them.
2. §11.6 defines handlers for `POST /v1/admin/circuit-breakers/{name}/open` (iter5).
3. §15.1 line 911 does not list a domain to cover those handlers.

The CI contract will break the build the first time someone writes the OpenAPI entry for `/v1/admin/circuit-breakers/*` with any scope string, because no in-taxonomy domain fits the concept of "operator-managed circuit breakers". (The existing `config` domain covers platform configuration; `tenant` covers tenant operations; neither fits the admission-gate scope of circuit breakers.)

**Recommendation:** Add `circuit_breaker` (singular, underscored — consistent with `credential_pool` and `tenant` conventions) to the closed scope-taxonomy list at line 911. Then document in §11.6 or §15.1 the specific per-endpoint scopes: `tools:circuit_breaker:read` for the two GET endpoints and `tools:circuit_breaker:write` for `.../open` and `.../close`. (Optionally split `open` and `close` into distinct action names — e.g., `tools:circuit_breaker:open` / `tools:circuit_breaker:close` — per the §15.1 line 912 guidance "a specific tool action name" for high-risk operations; opening a breaker is a destructive admission-gate action and may warrant a dedicated scope, mirroring how `steal` is a dedicated action for `locks`.) Update the §25.12 MCP management surface tool inventory (§25.12 "Admin API MCP extension contract") to declare the corresponding `x-lenny-scope` values at that granularity.

---

### API-022. `PLATFORM_AUDIT_REGION_UNRESOLVABLE` categorized `POLICY` breaks the "fail-closed mirror" family-category convention established by its three siblings [Medium]

**Status: Fixed** — Changed category from `POLICY` to `PERMANENT` at `spec/15_external-api-surface.md:1041`, `spec/25_agent-operability.md:1495`, `spec/11_policy-and-controls.md:423`, `spec/16_observability.md:426`, and `spec/12_storage-architecture.md:885`; docs already carried `PERMANENT` via the earlier iter5 sync, updated `docs/operator-guide/configuration.md:541` as well.

**Section:** `spec/15_external-api-surface.md:1037` and `spec/25_agent-operability.md:4339`.

The §15.4 catalog rows for the four fail-closed region-unresolvable codes are:

| Code | Category | HTTP | Section line |
|------|----------|------|--------------|
| `BACKUP_REGION_UNRESOLVABLE` | **PERMANENT** | 422 | §25.11 line 4336 |
| `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` | **PERMANENT** | 422 | §15.4 (grep: "ARTIFACT_REPLICATION_REGION_UNRESOLVABLE"), §25.11 line 4337 |
| `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` | **PERMANENT** | 422 | §15.4 line 1036, §25.11 line 4338 |
| `PLATFORM_AUDIT_REGION_UNRESOLVABLE` | **POLICY** | 422 | §15.4 line 1037, §25.11 line 4339 |

The `PLATFORM_AUDIT_REGION_UNRESOLVABLE` row's own description at line 1037 explicitly calls itself a "Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE`, `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`, and `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` applied to the platform-tenant audit-write surface (CMP-058)." The §25.11 copy at line 4339 repeats the same mirror language. A code that names itself a fail-closed mirror of three sibling codes all categorized `PERMANENT` yet is itself `POLICY` presents a classification contradiction readers of §15.4 and §25.11 will trip on.

The §15.4 category split at lines 964–965 ("`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`") and §16.3 taxonomy place region-residency configuration-absence failures in the `PERMANENT` family (the configuration is not a policy; it is a deployment state that cannot be satisfied) — that is exactly the rationale the three siblings above use. `POLICY` is reserved in the catalog for well-formed requests rejected by a configured rule evaluation (e.g., `CIRCUIT_BREAKER_OPEN`, `ISOLATION_MONOTONICITY_VIOLATED`, `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`). A missing `storage.regions.<region>.postgresEndpoint` configuration entry is not a policy evaluation result; it is the same configuration-absence class that makes `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` a `PERMANENT` error.

A knock-on effect: the `retryable` flag derived from the category (per §15.2.1 rule 5d — retryable equivalence in the REST/MCP contract test) may diverge between the three siblings and `PLATFORM_AUDIT_REGION_UNRESOLVABLE`. In iter5 `PERMANENT`/422 errors are `retryable: false`; the `POLICY`/422 family also defaults to `retryable: false` in this catalog, so there is no behavioural drift *today*, but the category-inconsistency seeds a divergence risk: if the §25.12 MCP tool-surface or the `RegisterAdapterUnderTest` contract later derives retry-classification from the category (rather than the explicit `retryable` field), all four codes would need to be in the same family.

Further knock-on: the `DataResidencyViolationAttempt` audit event (§25.11 line 4343 and §12.8) is emitted uniformly for all four codes — the audit pipeline treats them as a single family, but the error catalog does not.

**Recommendation:** Change `PLATFORM_AUDIT_REGION_UNRESOLVABLE`'s category from `POLICY` to `PERMANENT` at both `spec/15_external-api-surface.md:1037` and `spec/25_agent-operability.md:4339`, aligning with its three self-declared sibling mirrors. Keep HTTP 422 unchanged. Confirm retryability as `false` (already implicit). Update `docs/reference/error-catalog.md:173` correspondingly.

---

### API-023. `GIT_CLONE_REF_UNRESOLVABLE` conflates transient (`network_error`) and permanent (`auth_failed`, `ref_not_found`) sub-reasons under a single `PERMANENT`/422 code, breaking the iter4 `*_UNAVAILABLE` / `*_MISSING` split pattern [Medium]

**Status: Fixed** — Split the code in `spec/15_external-api-surface.md` error catalog: `GIT_CLONE_REF_UNRESOLVABLE` retains `PERMANENT/422` for `auth_failed`/`ref_not_found`; new sibling `GIT_CLONE_REF_RESOLVE_TRANSIENT` added as `TRANSIENT/503` for `network_error`. Updated `spec/14_workspace-plan-schema.md:102` resolution language, `docs/reference/error-catalog.md`, and `docs/reference/workspace-plan.md:77` accordingly.

**Section:** `spec/15_external-api-surface.md:1058` (`GIT_CLONE_REF_UNRESOLVABLE`); cross-references in `spec/14_workspace-plan-schema.md:102` and `docs/reference/error-catalog.md:91`.

Line 1058 defines the code with its sub-reason enumeration: "`details.reason` (e.g. `network_error`, `auth_failed`, `ref_not_found`) are included." It is classified `PERMANENT`/422, `retryable: false`.

`network_error` on a `git ls-remote` pre-session-creation probe is structurally a transient failure: the downstream Git host is temporarily unreachable or responded with a 5xx, retry-after-backoff is the correct client behaviour. `auth_failed` and `ref_not_found` are genuinely permanent: the caller must reconfigure the request (supply a different credential pool, correct the ref). Collapsing both classes under a single `PERMANENT`/422 row prevents:

1. A client from using the `retryable` field to drive retry behaviour (the field is the canonical signal per §15.2.1 rule 5d). The row is `retryable: false`, so clients back off regardless — even on `network_error`.
2. The REST/MCP contract test matrix in §15.2.1 (`RegisterAdapterUnderTest`) from exercising the pattern consistently with iter4 precedent: `REGION_CONSTRAINT_UNRESOLVABLE` (PERMANENT/422 — configuration-missing) vs. `REGION_UNAVAILABLE` (TRANSIENT/503 — temporary reach failure) split the analogous concern cleanly.
3. The `retry-after-backoff` signal for the canonical Git-host flake.

The existing catalog uses this split pattern in several places:
- `REGION_CONSTRAINT_UNRESOLVABLE` (PERMANENT/422) vs. `REGION_UNAVAILABLE` (TRANSIENT/503) — spec/15 lines 1051, 1052.
- `KMS_REGION_UNRESOLVABLE` (PERMANENT/422) vs. `KMS_UNAVAILABLE` (TRANSIENT/503 — grep verified absent under that name but the `CREDENTIAL_RENEWAL_FAILED` TRANSIENT/503 at line 1018 plays the sibling role).
- `DERIVE_SNAPSHOT_UNAVAILABLE` (TRANSIENT/503) at line 1047 — transient sibling of the permanent snapshot-missing path (rejected at derive preflight).

`GIT_CLONE_REF_UNRESOLVABLE` does not split its transient sub-reason into a sibling code, so the pattern is inconsistent with iter4 precedent.

**Recommendation:** Split the code into two rows:
- `GIT_CLONE_REF_UNAVAILABLE` (TRANSIENT/503) covering `details.reason: network_error` — the downstream remote was unreachable at session-creation. `retryable: true`, `Retry-After` guidance in §14's `gitClone.ref` resolution paragraph.
- `GIT_CLONE_REF_UNRESOLVABLE` (PERMANENT/422) covering `details.reason: auth_failed` and `ref_not_found` — the caller must reconfigure. `retryable: false`.

Update both `spec/15_external-api-surface.md:1058` and the §14 cross-reference at `spec/14_workspace-plan-schema.md:102` ("If `ls-remote` fails … the gateway rejects the request with `422 GIT_CLONE_REF_UNRESOLVABLE` … `details.reason`"). Update `docs/reference/error-catalog.md:91` to list both codes and the sub-reason mapping. Update `docs/reference/workspace-plan.md:77` accordingly.

(Secondary option: keep the single code but classify it `UPSTREAM`/502 — which iter4 reserved for downstream-originated failures — and flip `retryable` to `true` for the `network_error` sub-reason only. This is less clean than the split and leaves the catalog-invariant "single tuple per code" ambiguous, so the split is preferred.)

---

### API-024. `GIT_CLONE_REF_UNRESOLVABLE` missing from the §15.2.1 `RegisterAdapterUnderTest` session-creation rejection matrix despite being a §15.4 session-creation rejection [Medium]

**Status: Fixed** — Added both `GIT_CLONE_REF_UNRESOLVABLE` and the new `GIT_CLONE_REF_RESOLVE_TRANSIENT` (from API-023) to the §15.2.1 `RegisterAdapterUnderTest` session-creation rejection family enumeration at `spec/15_external-api-surface.md:1395`.

**Section:** `spec/15_external-api-surface.md:1390` (`RegisterAdapterUnderTest` test matrix).

Line 1390 lists the session-creation rejection family exercised by `RegisterAdapterUnderTest`: "the session-creation rejection family catalogued in §15.4 (`VARIANT_ISOLATION_UNAVAILABLE`, `REGION_CONSTRAINT_UNRESOLVABLE`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `ENV_VAR_BLOCKLISTED`, `SDK_DEMOTION_NOT_SUPPORTED`, `POOL_DRAINING`, `CIRCUIT_BREAKER_OPEN`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`)". The same line goes on to bind future additions to this matrix: "any future session-creation rejection added to §15.4 MUST be added to this list in the same change."

`GIT_CLONE_REF_UNRESOLVABLE` at §15.4 line 1058 was added by iter5 (CNT-015). Its description explicitly states: "Session creation rejected because the gateway could not resolve a `gitClone.ref` to an immutable commit SHA via `git ls-remote` at session creation." It is therefore a session-creation rejection code, and per the line 1390 rule it must be added to the test matrix — it is not.

This is the same class of defect iter4 API-013 fixed for the then-existing rejection family (`GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `GIT_CLONE_AUTH_HOST_AMBIGUOUS` / `ENV_VAR_BLOCKLISTED` / `SDK_DEMOTION_NOT_SUPPORTED` etc.) — a new session-creation rejection was added to §15.4 without the §15.2.1 matrix being updated in lockstep. The "in the same change" rule is self-documenting; its purpose is to prevent silent REST/MCP divergence on a new rejection code. Missing this matrix entry means adapter authors running `RegisterAdapterUnderTest` against a third-party MCP adapter will not have their implementation's handling of `GIT_CLONE_REF_UNRESOLVABLE` exercised, and the REST vs MCP wire-projection mapping for the code is not asserted.

**Recommendation:** Add `GIT_CLONE_REF_UNRESOLVABLE` to the session-creation rejection family enumeration at `spec/15_external-api-surface.md:1390`. If API-023 above is adopted, add both `GIT_CLONE_REF_UNRESOLVABLE` and the new sibling `GIT_CLONE_REF_UNAVAILABLE` (latter exercised in a retry-after-backoff assertion). Any `RegisterAdapterUnderTest` fixture already covers `GIT_CLONE_AUTH_*` via a misconfigured credential pool — the new fixture should submit a `gitClone` source with a ref that the mock remote does not resolve.

---

### API-025. `sources[<n>].resolvedCommitSha` field is gateway-written but not declared on the §14 `gitClone` variant schema, creating ambiguous `additionalProperties: false` semantics on response bodies [Low]

**Section:** `spec/14_workspace-plan-schema.md:85` (sources table) / line 102 (resolution paragraph) / line 334 (`additionalProperties: false` per-variant field strictness) / `spec/15_external-api-surface.md:597` (`GET /v1/sessions/{id}` response includes `workspacePlan`).

Line 85's `gitClone` row lists required fields `url`, `ref` and optional fields `path`, `depth`, `submodules`, `auth`. `resolvedCommitSha` is not declared as a field on the variant. Line 102 states it is a gateway-written read-only field populated on the persisted plan, and that clients MUST NOT set it in the `CreateSessionRequest` (rejected with `WORKSPACE_PLAN_INVALID` reason `gateway_written_field`). Line 334 asserts: "Within a known `source.type` variant … the published JSON Schema sets `additionalProperties: false`; unknown fields on a known type are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`". Line 597 of §15 declares that `GET /v1/sessions/{id}` returns the stored `workspacePlan` with `resolvedCommitSha` populated.

The ambiguity is this: clients performing client-side validation of the `GET` response body against the published JSON Schema at `https://schemas.lenny.dev/workspaceplan/v1.json` (referenced at `docs/reference/workspace-plan.md` and `spec/15_external-api-surface.md:880`) will encounter an "unknown field" (`resolvedCommitSha`) on each `gitClone` source entry. With `additionalProperties: false` in force, schema validators reject the response. The §14 line 102 text describes the field as part of the stored plan but does not explicitly extend the published JSON Schema to include it, and line 85's optional-fields column still omits it.

Two reasonable interpretations:
1. The schema is intended to cover `CreateSessionRequest` bodies only (where `resolvedCommitSha` is rightly absent), and the `GET` response carries an **augmented shape** not validated against the same schema — but line 334's strictness statement does not scope itself to requests.
2. The schema covers both shapes and `resolvedCommitSha` must be declared as an optional response-side field with validation relaxed to `readOnly: true`.

Either interpretation should be made explicit in §14 so that client-generator tooling (e.g., the reference-runtime SDKs in §26 or third-party adapters) does not emit validators that reject otherwise-valid gateway responses.

This is Low severity because all known clients today receive `resolvedCommitSha` as an informational field for audit trails (§14 line 102: "MAY be surfaced to clients in `GET /v1/sessions/{id}` for audit purposes") — no runtime logic depends on the field, so a validator rejection is a non-blocking audit-trail gap. But it is catalog-taxonomy hardening consistent with iter4 API-014's concern about silent schema divergence.

**Recommendation:** In `spec/14_workspace-plan-schema.md` either (a) extend the `gitClone` variant's optional-fields column at line 85 to list `resolvedCommitSha` with the note "(gateway-written on response; clients MUST NOT set in `CreateSessionRequest`)", and update the published JSON Schema to mark it `"readOnly": true`; or (b) split the schema publication into two documents — `workspaceplan-request/v1.json` (`additionalProperties: false`, no `resolvedCommitSha`) and `workspaceplan-response/v1.json` (adds `resolvedCommitSha` on `gitClone`) — and update `docs/reference/workspace-plan.md:77` and `spec/15_external-api-surface.md:597` to reference the response-side schema. Option (a) is simpler and preferred; option (b) is more principled for future gateway-written fields (e.g., on other source types where a write-time resolution is desired).

---

## 3. Convergence assessment

- **Iter5 fixes for the five new-surface items verified present in spec and docs:**
  - POL-023: `POST /v1/admin/circuit-breakers/{name}/open` body schema and `INVALID_BREAKER_SCOPE` are documented in both `spec/11_policy-and-controls.md:308–313` and `docs/api/admin.md:708–752`; catalog row present at `spec/15_external-api-surface.md:1026`.
  - CMP-054: `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` present in `spec/15:1036`, `spec/25:4338`, `docs/reference/error-catalog.md:172`.
  - CMP-057: `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` present in `spec/15:1065`, `docs/reference/error-catalog.md:108`; `POST /v1/admin/tenants/{id}/compliance-profile/decommission` endpoint present in `spec/15:865` and `docs/api/admin.md:491`.
  - CMP-058: `PLATFORM_AUDIT_REGION_UNRESOLVABLE` present in `spec/15:1037`, `spec/25:4339`, `docs/reference/error-catalog.md:173`.
  - CNT-015: `GIT_CLONE_REF_UNRESOLVABLE` present in `spec/15:1058`, `spec/14:102`, `docs/reference/error-catalog.md:91`; `resolvedCommitSha` response field documented in `spec/15:597`, `spec/14:324`, `docs/reference/workspace-plan.md:77`.

- **Six new iter6 API findings:** four Medium (API-020 endpoint-table omission, API-021 scope-taxonomy omission, API-022 `PLATFORM_AUDIT_REGION_UNRESOLVABLE` category mismatch, API-023 `GIT_CLONE_REF_UNRESOLVABLE` transient/permanent conflation, API-024 `RegisterAdapterUnderTest` matrix omission) and one Low (API-025 `resolvedCommitSha` schema declaration). API-020, API-021, and API-024 are contract-invariant breakages (§15.1 endpoint table / scope taxonomy / §15.2.1 test matrix) that iter5 introduced silently without updating their respective contract surfaces. API-022 and API-023 are category-taxonomy inconsistencies equivalent in severity to iter3 API-005 and iter4 API-011 (Medium at first catalog-audit).

- **Three iter5 Low carry-forwards persist (API-017, API-018, API-019)** with no change in state. They remain editorial / documentation hardening items.

- **Total iter6 severity tally for the API perspective:** Critical 0, High 0, Medium 4 (API-020, API-021, API-022, API-023, API-024), Low 4 (API-017, API-018, API-019, API-025).

- **Convergence:** **Not converged.** The four Medium findings are contract-invariant breakages (API-020 violates §15.1 "table includes all endpoints" assertion; API-021 violates §15.1 scope-taxonomy source-of-truth rule at line 915; API-022 violates the self-declared fail-closed-mirror family convention; API-023 violates the iter4 TRANSIENT/PERMANENT split pattern for remote-lookup failures; API-024 violates the explicit "in the same change" maintenance rule at line 1390). Each of API-020/021/024 is a lockstep-maintenance defect iter5 reintroduced despite iter4 API-013 having fixed a textually analogous case; API-022/023 are category-taxonomy drift that needs a single-commit fix. The three Low carry-forwards remain separately trackable.

Recommend addressing API-020 through API-024 in iter6's fix cycle (single commit covering §15.1 endpoint table rows, §15.1 scope taxonomy addition, §15.4 line 1037 category change, §15.4 line 1058 code split, §15.2.1 matrix addition, `docs/api/admin.md` / `docs/reference/error-catalog.md` / `docs/reference/workspace-plan.md` parity). API-025 can land as an editorial fix alongside or be deferred with the other Low carry-forwards.
