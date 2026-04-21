# Perspective 14 — API Design & External Interface Quality (iter7)

**Scope.** Re-review of the external API surface (`spec/15_external-api-surface.md`), MCP consistency (`spec/09_mcp-integration.md`, `spec/25_agent-operability.md` §25.12), and doc parity (`docs/api/admin.md`, `docs/reference/error-catalog.md`, `docs/reference/workspace-plan.md`, `docs/runbooks/circuit-breaker-open.md`) against iter6's fix cycle on `main`.

**iter6 disposition summary (from `iter6/p14_api_design.md`).**

| iter6 finding | iter6 severity | iter6 status |
| --- | --- | --- |
| API-020 `/v1/admin/circuit-breakers/*` absent from §15.1 endpoint table | Medium | Fixed |
| API-021 `circuit_breaker` domain missing from scope taxonomy | Medium | Fixed |
| API-022 `PLATFORM_AUDIT_REGION_UNRESOLVABLE` category `POLICY` vs. sibling family | Medium | Fixed |
| API-023 `GIT_CLONE_REF_UNRESOLVABLE` conflated transient + permanent | Medium | Fixed |
| API-024 `GIT_CLONE_REF_UNRESOLVABLE` missing from §15.2.1 `RegisterAdapterUnderTest` matrix | Medium | Fixed |
| API-025 `sources[<n>].resolvedCommitSha` schema declaration ambiguity | Low | (carried forward for iter7 verification; candidate close given the iter6 CNT-020 schema-encoding fix at `spec/14_workspace-plan-schema.md:104`) |
| API-017 §15.4 catalog uniqueness invariant header | Low (carry-forward) | Not fixed |
| API-018 `UNREGISTERED_PART_TYPE` `WARNING` category outside canonical taxonomy | Low (carry-forward) | Not fixed |
| API-019 `RESTORE_ERASURE_RECONCILE_FAILED` `PERMANENT/500` for known operator-action failure | Low (carry-forward) | Not fixed |

**Calibration.** iter7 severities anchored to the iter1–iter6 rubric per `feedback_severity_calibration_iter5.md`. Editorial / documentation hardening items remain **Low**; per-endpoint contract declarations (scope bindings, dryRun table rows) are **Medium** consistent with how iter4 API-013, iter6 API-020, and iter6 API-021 were graded.

**Numbering.** iter6 P14 ended at API-025. This perspective uses API-026 onward.

---

## 1. iter6 fix verification on main

| iter6 fix | Expected spec/doc state | Verified on `main` |
| --- | --- | --- |
| API-020 (circuit-breakers endpoints in §15.1) | Four rows added between `/v1/admin/billing-correction-reasons/{code}` and `/v1/admin/preflight`; row for `.../open` names `INVALID_BREAKER_SCOPE`; each row cites `[Section 11.6]` | Verified at `spec/15_external-api-surface.md:884-887` |
| API-021 (scope taxonomy) | `circuit_breaker` added to closed domain list | Verified at `spec/15_external-api-surface.md:915` |
| API-022 (`PLATFORM_AUDIT_REGION_UNRESOLVABLE` → `PERMANENT`) | Category changed from `POLICY` to `PERMANENT` at 5 spec locations | Verified at `spec/15_external-api-surface.md:1042`, `spec/25_agent-operability.md:4339`, `spec/11_policy-and-controls.md` (spot-checked in §11.7), `spec/16_observability.md` (spot-checked in §16.5), `spec/12_storage-architecture.md` (spot-checked in §12.8); doc parity at `docs/reference/error-catalog.md:175` |
| API-023 (`GIT_CLONE_REF_UNRESOLVABLE` split) | `GIT_CLONE_REF_UNRESOLVABLE` retained as `PERMANENT/422` for `auth_failed`/`ref_not_found`; new sibling `GIT_CLONE_REF_RESOLVE_TRANSIENT` as `TRANSIENT/503` for `network_error` | Verified at `spec/15_external-api-surface.md:1063-1064`; `spec/14_workspace-plan-schema.md:104` resolution paragraph updated with both codes and sub-reasons; doc parity at `docs/reference/error-catalog.md:92-93` |
| API-024 (`RegisterAdapterUnderTest` matrix) | Both codes added to the session-creation rejection family enumeration | Verified at `spec/15_external-api-surface.md:1396` (both `GIT_CLONE_REF_UNRESOLVABLE` and `GIT_CLONE_REF_RESOLVE_TRANSIENT` listed) |

All five iter6 fixes are present on `main` with correct text and cross-references. No fix regression observed.

---

## 2. iter6 Low carry-forward status (API-025)

**API-025 (iter6 Low).** The `resolvedCommitSha` schema declaration ambiguity identified in iter6 was addressed out-of-band by iter6 CNT-020, which added a full "Schema encoding of the request/response asymmetry" paragraph to `spec/14_workspace-plan-schema.md:104`. That paragraph now explicitly states:

- `resolvedCommitSha` is declared on the `gitClone` variant in the published JSON Schema with `"readOnly": true`;
- because `readOnly` is informational in JSON Schema 2020-12 and the variant sets `additionalProperties: false`, a strict validator would accept a client-supplied value;
- the gateway therefore performs a second request-time check that rejects `resolvedCommitSha` at the field-specific `WORKSPACE_PLAN_INVALID` / `gateway_written_field` code;
- this dual-schema pattern is identified as "the canonical encoding for gateway-written fields in Lenny" and is stated to be identical to the encoding for `last_used_at` on `GET /v1/credentials`.

Both of the two reasonable interpretations enumerated in API-025 — (a) declare as optional response-side field with `readOnly: true`, (b) split into request/response schemas — are resolved in favour of option (a), and tooling guidance ("Clients SHOULD omit `resolvedCommitSha` from request bodies; tooling that round-trips the response into a new request MUST strip it first") is explicit.

**Disposition: Resolved in iter6.** API-025 is not carried forward to iter7.

---

## 3. iter5 Low carry-forward verification (API-017, API-018, API-019)

All three remain unchanged from iter6 disposition. Severities held at Low per iter5/iter6 precedent.

| iter5 finding | iter7 evidence | Status |
| --- | --- | --- |
| API-017 §15.4 catalog uniqueness invariant not stated at header | `spec/15_external-api-surface.md:970-972` — "Fields: `code` (string, required) — machine-readable error code from the table below." describes the per-row tuple but does not assert "each `code` appears at most once in this table and carries a single `(category, httpStatus, retryable)` tuple". Grep of the §15.4 header neighbourhood for "uniqueness" / "at most once" returns no match. | **Not fixed** — Low (carry-forward) |
| API-018 `UNREGISTERED_PART_TYPE` row uses `WARNING` outside canonical `TRANSIENT\|PERMANENT\|POLICY\|UPSTREAM` taxonomy | `spec/15_external-api-surface.md:1045` still reads `\| UNREGISTERED_PART_TYPE \| WARNING \| — \|`; the canonical taxonomy at line 970 still enumerates four values excluding `WARNING`. | **Not fixed** — Low (carry-forward) |
| API-019 `RESTORE_ERASURE_RECONCILE_FAILED` `PERMANENT/500` for known operator-action failure path | `spec/25_agent-operability.md:4334` still lists `RESTORE_ERASURE_RECONCILE_FAILED \| PERMANENT \| 500`; description still enumerates the legal-hold-ledger-stale sub-reason (a POLICY-like gating failure) under the same code/status. | **Not fixed** — Low (carry-forward) |

Each remains a documentation / classification hardening item with no runtime contract impact; none blocks convergence.

---

## 4. New iter7 findings

### API-026. `docs/api/admin.md` operator-managed circuit-breakers GET row-role drifts from spec §15.1 fix [Medium]

**Section:** `docs/api/admin.md:712-713` (operator-managed circuit-breakers endpoint table) vs. `spec/15_external-api-surface.md:884-885` (§15.1 endpoint table, iter6 API-020 addition) and `spec/11_policy-and-controls.md:306` (authoritative role statement).

The iter6 fix for API-020 added four `/v1/admin/circuit-breakers/*` rows to the §15.1 endpoint table. Each of the two GET rows declares `Requires \`platform-admin\``:

```
spec/15_external-api-surface.md:884  | `GET`    | `/v1/admin/circuit-breakers`            | ... Requires `platform-admin`. See [Section 11.6]... |
spec/15_external-api-surface.md:885  | `GET`    | `/v1/admin/circuit-breakers/{name}`     | ... Requires `platform-admin`. See [Section 11.6]... |
```

`spec/11_policy-and-controls.md:306` is authoritative: "Circuit breakers are managed via the admin API (requires `platform-admin` role)." `spec/24_lenny-ctl-command-reference.md:105-107` similarly ties all four CLI wrappers to `platform-admin`.

The operator-facing documentation at `docs/api/admin.md:710-715` diverges:

```
docs/api/admin.md:712  | `GET /v1/admin/circuit-breakers`        | GET | platform-admin, tenant-admin | List all circuit breakers and their current state |
docs/api/admin.md:713  | `GET /v1/admin/circuit-breakers/{name}` | GET | platform-admin, tenant-admin | Get state for a single circuit breaker           |
docs/api/admin.md:714  | `POST /v1/admin/circuit-breakers/{name}/open`  | POST | platform-admin | Open (activate) a circuit breaker...              |
docs/api/admin.md:715  | `POST /v1/admin/circuit-breakers/{name}/close` | POST | platform-admin | Close (deactivate) a circuit breaker; body is empty |
```

The two GET rows advertise `platform-admin, tenant-admin` to third-party UI/CLI integrators, which contradicts the spec's single-role statement at §11.6 and §15.1. Operator-managed circuit breakers are platform-wide admission gates (`spec/11_policy-and-controls.md` §11.6 "platform-wide breakers") — the spec's fail-closed `platform-admin`-only model is the load-bearing authority boundary: a `tenant-admin` cannot see into another tenant's circuit-breaker state without crossing a privacy boundary (the breaker's `scope` matcher may carry another tenant's runtime or connector ID; see `spec/15_external-api-surface.md:886` request body `"scope": <tier-specific matcher>`). Advertising the tenant-admin role on the GET endpoints to docs readers is a security-boundary misstatement, not merely an editorial typo.

This drift appears to have been introduced when the docs rows for the four endpoints were authored in a prior iteration, and the iter6 API-020 fix updated the **spec** table (correctly, to `platform-admin` only) without back-propagating the correction into `docs/api/admin.md`. The iter6 disposition note at API-020 claims "Fixed — Added four `/v1/admin/circuit-breakers/*` rows … each row cross-references §11.6 and names `INVALID_BREAKER_SCOPE`" but does not assert doc-parity reconciliation.

Per the repository's standing `feedback_docs_sync_after_spec_changes.md` instruction ("Reconcile docs/ with spec changes after each review-fix iteration before declaring convergence"), this docs drift falls squarely inside the iter6 fix cycle's obligation envelope.

**Recommendation:** Change the two GET rows at `docs/api/admin.md:712-713` to advertise only `platform-admin`, matching §15.1, §11.6, and §24.13. If the docs authors intend for `tenant-admin` to have visibility into operator-managed circuit-breaker state for operator-operability reasons (e.g., delegated observation), that is a new design decision requiring a spec change at §11.6 and §15.1 first, not a unilateral docs addition. In either case, a spec ↔ docs single source-of-truth invariant must be stated in `docs/api/admin.md` (or in the repository's doc-generation policy) so that future spec-role changes block until the docs table is regenerated.

**Severity: Medium** — authority-boundary misstatement in operator-facing docs for endpoints that reveal cross-tenant admission-gate state. Calibrated to the same level as iter6 API-020/021 (single-source-of-truth invariant breakages on admin-API surfaces).

---

### API-027. Per-endpoint `x-lenny-scope` declarations missing for the four `/v1/admin/circuit-breakers/*` endpoints despite iter6 taxonomy addition [Medium]

**Section:** `spec/15_external-api-surface.md:911-919` (scope-taxonomy source-of-truth rule) and §15.1 MCP extension contract; `spec/25_agent-operability.md` §25.12.

iter6 API-021 correctly added `circuit_breaker` to the closed scope-taxonomy domain list at `spec/15_external-api-surface.md:915`. The same paragraph declares:

```
line 917: Enforcement: every admin-API endpoint declares its `x-lenny-scope` (see MCP extension below). Mismatch → `403 SCOPE_FORBIDDEN` with `requiredScope` and `activeScope` in the response (§25.12).
line 919: This list is the source-of-truth; new domains must be added here before being introduced in handlers.
```

A domain is added "before being introduced in handlers" — but the four new handler rows at `spec/15_external-api-surface.md:884-887` do not themselves declare an `x-lenny-scope` value or cross-reference one. Grep of the entire `spec/` tree for `tools:circuit_breaker:read`, `tools:circuit_breaker:write`, `tools:circuit_breaker:open`, `tools:circuit_breaker:close`, or any other concrete binding to the newly-added `circuit_breaker` domain returns **no matches**. The MCP extension contract at §15.1 asserts (line 917–918):

> every admin-API endpoint declares its `x-lenny-scope`

and §25.12 enumerates the scope values for the catalogued MCP management tools. The §25.12 tool inventory likewise has no `circuit_breaker` entries (spot-checked: no tool uses a domain matching `circuit_breaker`).

The three-way inconsistency from iter6 API-021 recurs in a subtler form:

1. §15.1 line 919 says domains must be added to the taxonomy before handlers use them (done — `circuit_breaker` is in the list).
2. §11.6 and §15.1 define handlers for the four `/v1/admin/circuit-breakers/*` endpoints (done — the endpoints exist on `main`).
3. **No location binds the domain to the handlers via a concrete `x-lenny-scope` value.**

The CI contract at §15.1 line 927 ("every `x-lenny-scope` value conforms to `tools:<domain>:<action>` syntax and its domain is in the taxonomy above") will not trip because there is no value to check. But the **inverse** contract — that every admin-API endpoint **has** an `x-lenny-scope` — is violated silently: the four endpoints reach production without any declared scope binding, or the binding is invented ad-hoc at OpenAPI-generation time with no spec cite.

iter6 API-021's recommendation paragraph (in iter6 p14, lines 66) explicitly called this out and recommended adding the bindings:

> Then document in §11.6 or §15.1 the specific per-endpoint scopes: `tools:circuit_breaker:read` for the two GET endpoints and `tools:circuit_breaker:write` for `.../open` and `.../close`. (Optionally split `open` and `close` into distinct action names — e.g., `tools:circuit_breaker:open` / `tools:circuit_breaker:close`…). Update the §25.12 MCP management surface tool inventory (§25.12 "Admin API MCP extension contract") to declare the corresponding `x-lenny-scope` values at that granularity.

The iter6 fix applied only the first half (adding `circuit_breaker` to the taxonomy) and not the second half (declaring per-endpoint scope bindings). This is a partial-fix regression of iter6 API-021.

**Recommendation:** Declare per-endpoint scope bindings in §11.6 or §15.1 (preferably inside §11.6 where the endpoints are first introduced, cross-referenced from the §15.1 rows). Suggested bindings:

- `GET /v1/admin/circuit-breakers` → `tools:circuit_breaker:read`
- `GET /v1/admin/circuit-breakers/{name}` → `tools:circuit_breaker:read`
- `POST /v1/admin/circuit-breakers/{name}/open` → `tools:circuit_breaker:open` (dedicated action, consistent with §15.1 line 916 "a specific tool action name" guidance for destructive actions; mirrors `locks:steal`)
- `POST /v1/admin/circuit-breakers/{name}/close` → `tools:circuit_breaker:close`

Then update the §25.12 MCP tool inventory to include the four tools and their `x-lenny-scope` values. Add the four tools to any contract test matrix that asserts "every admin-API endpoint has an `x-lenny-scope`".

**Severity: Medium** — contract-invariant incompleteness identical in kind to iter6 API-020/021 (endpoint table and scope-taxonomy source-of-truth omissions). The absence of concrete bindings means the `403 SCOPE_FORBIDDEN` enforcement path at §25.12 cannot be exercised or asserted for these endpoints.

---

### API-028. `POST /v1/admin/circuit-breakers/{name}/open` and `.../close` are silent about `dryRun` support — neither in the §15.1 supported-endpoints table nor in the excluded-actions list [Medium]

**Section:** `spec/15_external-api-surface.md:1172-1193` (dryRun support table and exclusions).

§15.1 specifies `dryRun` semantics for admin side-effecting operations via a table of supported endpoints (lines 1172-1189) followed by an excluded-actions statement (line 1193):

```
line 1193: `DELETE` endpoints do not support `dryRun` — deletion validation is trivial (existence + authorization) and does not benefit from a preview. Action endpoints (`drain`, `force-terminate`, `warm-count`) do not support `dryRun` because their value is in the side effect, not validation.
```

The supported-endpoints table enumerates every `POST`/`PUT` admin endpoint that accepts `dryRun=true`. The excluded-actions list names three specific action endpoints (`drain`, `force-terminate`, `warm-count`).

The iter6 API-020 fix added four `/v1/admin/circuit-breakers/*` endpoints to the §15.1 endpoint table (lines 884-887), but neither:

1. Added `POST /v1/admin/circuit-breakers/{name}/open` or `.../close` to the dryRun supported-endpoints table at lines 1172-1189, nor
2. Added `open` / `close` to the excluded-actions list at line 1193 alongside `drain`, `force-terminate`, `warm-count`.

Third-party UI/CLI authors reading §15.1 to determine whether `POST /v1/admin/circuit-breakers/{name}/open?dryRun=true` is a supported pattern will find an ambiguous catalog. The endpoint is neither listed as supported nor excluded, which is a catalog-integrity gap. Since the open endpoint accepts a request body with `reason`, `limit_tier`, and `scope` — all of which benefit from validation-only dry runs, and since `INVALID_BREAKER_SCOPE` is a structurally-validatable error code — a case can be made for including it in the supported set. Conversely, `open`/`close` are fundamentally action endpoints whose value is in the side effect (global admission-gate activation), which maps closely to the `drain` case. The correct answer is not obvious from the current spec text, and the §15.1 iter6 edit did not make a decision.

This is the same class of gap that iter4 API-013 fixed when a new admin endpoint was added without updating the §15.1 catalog rules in lockstep.

**Recommendation:** Make an explicit decision and edit the spec accordingly:

- **Option (a):** treat `open`/`close` as action endpoints (preferred, consistent with the existing exclusion of `drain`/`force-terminate`/`warm-count`): update line 1193 to read `Action endpoints (\`drain\`, \`force-terminate\`, \`warm-count\`, \`circuit-breakers/{name}/open\`, \`circuit-breakers/{name}/close\`) do not support \`dryRun\` because their value is in the side effect, not validation.` Alternatively, rewrite the clause more generally (e.g., "Action endpoints — suffixed with an imperative verb such as `/drain`, `/force-terminate`, `/open`, `/close`, `/warm-count` — do not support `dryRun` …").
- **Option (b):** include `POST /v1/admin/circuit-breakers/{name}/open` in the dryRun supported-endpoints table at lines 1172-1189 with Notes column "Validates body, checks limit_tier/scope consistency with persisted scope". This requires defining the `dryRun=true` response shape for the endpoint (likely the existing 200 OK with no audit emission). Rationale: opening a circuit breaker is one of the few destructive admin actions where validation-before-commit has non-trivial value, and the validation cost of `INVALID_BREAKER_SCOPE` is observable in production. Close is excluded (empty body, nothing to validate).

Option (a) is editorially simpler and consistent with existing action-endpoint handling. Option (b) adds operator value at the cost of a new contract surface.

**Severity: Medium** — catalog-integrity gap on a security-sensitive endpoint. Calibrated to the same level as iter6 API-020 (`/v1/admin/circuit-breakers/*` absent from the §15.1 endpoint table was Medium; this finding is the same class of incomplete-lockstep-edit on the dryRun catalog). The ambiguity affects third-party tooling (per-endpoint feature-detection fails or leaks inconsistent behaviour across adapters).

---

## 5. Convergence assessment

- **Iter6 fixes for the five new API findings (API-020..024) verified present on `main`** with correct text and all five doc parity points reconciled. No fix regression.

- **iter6 API-025 (Low)** is resolved by the iter6 CNT-020 schema-encoding paragraph at `spec/14_workspace-plan-schema.md:104`. Not carried forward.

- **iter5 Low carry-forwards persist unchanged (API-017, API-018, API-019).** All three are editorial/classification hardening items, stable across iter5–iter7. No severity drift.

- **Three new iter7 API findings, all Medium** (API-026 operator-docs role drift on circuit-breakers GET; API-027 missing per-endpoint `x-lenny-scope` bindings for the four `/v1/admin/circuit-breakers/*` endpoints; API-028 silent `dryRun` catalog gap on `open`/`close`). Each is an iter6-fix-adjacent lockstep-maintenance defect: API-026 is a docs-sync gap inside the iter6 API-020 fix envelope (and violates `feedback_docs_sync_after_spec_changes.md`); API-027 is a partial-fix regression of the iter6 API-021 recommendation (the taxonomy domain was added but the per-endpoint bindings were not); API-028 is the same class of incomplete-lockstep-edit on the dryRun catalog that iter4 API-013 fixed for endpoint enumeration.

- **Iter7 severity tally for the API perspective:** Critical 0, High 0, Medium 3 (API-026, API-027, API-028), Low 3 (API-017, API-018, API-019), Info 0.

- **Convergence: Not converged.** The three Medium findings are contract-invariant incompleteness in the iter6 fix cycle. None is load-bearing for v1 launch blocking, but each is the same class of defect the iter6 cycle was intended to eliminate. Severity relative to iter6 (4 Medium) is trending downward (3 Medium), and the new findings are all scoped to the same subsystem — suggesting a single-commit iter7 fix covering `docs/api/admin.md:712-713` role correction, per-endpoint `x-lenny-scope` bindings at §11.6 / §25.12, and a decision + edit on the dryRun catalog for `open`/`close` would close them together.

- **Recommendation for iter7 fix cycle:** single commit covering (1) `docs/api/admin.md:712-713` role → `platform-admin` only; (2) new paragraph in §11.6 (or §15.1) listing the four `tools:circuit_breaker:<action>` bindings with cross-reference to §25.12 tool inventory entries; (3) edit to `spec/15_external-api-surface.md:1193` to include `open` / `close` in the excluded-actions list (preferred, option a). Doc parity updates in `docs/api/admin.md`, `docs/mcp/tools.md` (if any tool inventory exists there), and `docs/runbooks/circuit-breaker-open.md` in the same commit.

---

**Perspective:** 14 — API Design & External Interface Quality
**Category:** API
**Count:** 6 (3 Medium — API-026, API-027, API-028; 3 Low carry-forwards — API-017, API-018, API-019)
**Path:** `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter7/p14_api_design.md`
**Verdict:** Not converged (Medium findings persist; three new iter7 Medium findings inside the iter6 fix envelope)
