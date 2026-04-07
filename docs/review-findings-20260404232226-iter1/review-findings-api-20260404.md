# Technical Design Review Findings — API Design & External Interface Quality
**Perspective:** 14. API Design & External Interface Quality
**Document reviewed:** `docs/technical-design.md`
**Date:** 2026-04-04
**Category code:** API

---

## Findings Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High | 5 |
| Medium | 7 |
| Low | 4 |
| Info | 2 |

---

### API-001 REST/MCP Parity Contract Has No Runtime Enforcement Path [Critical] — VALIDATED/FIXED
**Section:** 15.2.1

The REST/MCP consistency contract (Section 15.2.1) is well-articulated in prose with five named rules, but only rule 4 (OpenAPI→MCP schema generation) and rule 5 (CI contract tests) create enforceable guarantees. Rules 1–3 are human-auditable principles without machine verification: "semantic equivalence" is stated but not tested at the semantic level, only structural. The CI contract tests assert "identical response payloads modulo transport envelope," which catches schema divergence but not behavioral divergence (e.g., different pagination behavior, different default field values, different error messages for identical invalid inputs, or divergent state machine transitions returned on edge cases). Furthermore, the contract test harness entry point for third-party adapters (`RegisterAdapterUnderTest`) is mentioned but its scope is undefined: does it cover the full operation matrix or only the basic success path? A future adapter that passes structural checks but returns different `retryable` semantics or different `category` classifications for the same error would break client error-handling logic without tripping any test.

The requirement that future adapters "pass the same contract test suite before being enabled in production" is unenforceable at the admin API level — `POST /v1/admin/external-adapters` has no mechanism to gate registration on test passage.

**Recommendation:** (1) Extend contract tests to cover behavioral equivalence: same `retryable` flag and `category` for identical error conditions, same session state returned on `GET /v1/sessions/{id}` vs `get_session_status` MCP tool after identical operations, same pagination behavior. (2) Define the full test matrix for `RegisterAdapterUnderTest` explicitly in the spec (which operations, which error cases, which edge cases). (3) Add a `PUT /v1/admin/external-adapters/{name}/validate` endpoint that runs the contract test suite against a registered adapter in a dry-run mode and returns the results — this makes compliance testable without requiring out-of-band coordination. (4) Document which categories of behavioral divergence are intentional (e.g., MCP streaming granularity differs from REST polling) vs. prohibited.

**Resolution:** Section 15.2.1 Rule 5 was expanded from structural to behavioral equivalence: added sub-items (d) `retryable`/`category` flag identity across surfaces, (e) session state transition identity via fixed operation sequences, and (f) pagination behavior identity (page size, cursor, empty-result shape). The `RegisterAdapterUnderTest` test matrix was defined explicitly: all session lifecycle operations, six error classes, three state transition sequences, and multi-page pagination. The adapter registration gate was added: `POST /v1/admin/external-adapters` creates adapters in `status: pending_validation` (no traffic); `PUT /v1/admin/external-adapters/{name}/validate` runs the suite and transitions to `active` or `validation_failed`. Adapters not in `active` are excluded from routing.

---

### API-002 Admin API Resource Endpoints Mix Singular and Collective HTTP Method Notation [High] — Fixed
**Section:** 15.1

The admin API table lists endpoints as `POST/PUT/GET/DELETE` on a collection path like `/v1/admin/runtimes`, conflating CRUD semantics onto one row. This notation is ambiguous for third-party UI/CLI developers: does `PUT /v1/admin/runtimes` update the entire collection or a single resource? What is the URL for a single runtime — `/v1/admin/runtimes/{name}` or `/v1/admin/runtimes/{id}`? The spec never explicitly defines singleton resource paths. The ETag section implies `PUT` operates on a specific resource (it requires a prior `GET` to obtain the ETag), but the endpoint table suggests `PUT /v1/admin/runtimes` (no path parameter). This ambiguity would cause a UI developer to guess at the URL structure.

The action endpoints `POST /v1/admin/pools/{name}/drain` and `PUT /v1/admin/pools/{name}/warm-count` use `{name}` as the path parameter, confirming name-based addressing — but this is not stated as the universal convention, and other resources may use IDs. The inconsistency between the `drain` endpoint's `{name}` and the general resource endpoints' ambiguity creates a hole in the interface contract.

**Recommendation:** Enumerate all resource endpoints explicitly with full paths: `GET /v1/admin/runtimes` (list), `POST /v1/admin/runtimes` (create), `GET /v1/admin/runtimes/{name}` (get), `PUT /v1/admin/runtimes/{name}` (update), `DELETE /v1/admin/runtimes/{name}` (delete). Declare that all admin resources use `{name}` as the path identifier (or `{id}` if preferred — choose one and state it). Apply this pattern consistently to every admin resource type in the table. This is foundational for the OpenAPI spec and for any CLI/UI developer reading the spec.

**Resolution:** The admin API table in Section 15.1 was fully expanded: every collapsed `POST/PUT/GET/DELETE` row was replaced with individual rows showing explicit collection paths (`GET /v1/admin/runtimes` for list, `POST /v1/admin/runtimes` for create) and singleton paths (`GET /v1/admin/runtimes/{name}`, `PUT /v1/admin/runtimes/{name}`, `DELETE /v1/admin/runtimes/{name}`). A preamble was added declaring the universal convention: all admin resources use `{name}` as the path identifier (human-readable, unique within scope), with the single exception of tenants which use `{id}` (opaque UUID) because tenant names are mutable display labels. The dryRun endpoint table was also updated so that all `PUT` rows reference the singleton `{name}` path. The credential-pool action endpoints were updated from `{poolId}` to `{name}` in the admin API table for consistency with the declared convention.

---

### API-003 `dryRun` Behavior Is Underspecified for Side-Effect-Adjacent Operations [High] — Fixed
**Section:** 15.1

The `dryRun` description states the gateway performs "full request validation — schema, field constraints, referential integrity, policy checks, and quota evaluation — but does not persist the result, emit audit events, or trigger any side effects." Two ambiguities affect UI/CLI builders:

First, "referential integrity" for `POST /v1/admin/connectors` states it "checks endpoint reachability format" — but does `dryRun` actually attempt a network connection to the connector's `mcpServerUrl` to verify reachability, or does it only validate the URL format? If `dryRun` makes outbound network calls, it has side effects (consumed rate limits at the target, auth challenges triggered, audit logs at the remote end). If it does not, the spec's claim to validate "referential integrity" is incomplete for connectors.

Second, for `POST /v1/admin/experiments`, `dryRun` "validates experiment definition and variant weights" — but variant weights affect pool sizing (PoolScalingController reads them). Does `dryRun` validate that the implied pool demand is within current capacity? If yes, this involves a real quota/capacity read that has observable state effects (locks held, cache warmed). The spec does not address this.

Third, `dryRun` on `PUT /v1/admin/environments` promises to "preview selector matches" (Section 21.5) — this requires evaluating runtime labels against the selector, which is a read of the full runtime catalog. This is the most useful dryRun behavior for a UI building an environment editor, but the spec does not document what the preview response looks like (how are matches returned? as a list of matched runtime names?).

**Recommendation:** (1) State explicitly that `dryRun` never makes outbound network calls — connector URL format validation is syntactic only. Add a separate `POST /v1/admin/connectors/{name}/test` endpoint for live connectivity checks. (2) Document whether capacity validation is included in experiment `dryRun`. (3) Specify the response body shape for `PUT /v1/admin/environments` `dryRun`: add a `preview` object containing `matchedRuntimes: []string` and `matchedConnectors: []string` alongside the standard resource representation. This is the primary use case for the environment `dryRun` and is entirely absent from the spec.

**Resolution:** All three ambiguities resolved in Section 15.1. (1) The `dryRun` paragraph now explicitly states "dryRun never makes outbound network calls" and that all referential integrity checks are syntactic/against locally cached state. A new `POST /v1/admin/connectors/{name}/test` endpoint was added (both in the admin API endpoint table and with a dedicated specification block) for live connectivity checks with staged pass/fail reporting (DNS, TLS, MCP handshake, auth). (2) Experiment `dryRun` semantics now explicitly state capacity validation is **not** included; capacity feasibility is evaluated asynchronously by PoolScalingController on activation, with guidance to use `GET /v1/admin/pools/{name}` for pre-checking. (3) Environment `dryRun` response now specifies a `preview` object containing `matchedRuntimes`, `matchedConnectors`, and `unmatchedSelectorTerms` arrays, with a JSON example. Section 21.5 was also updated to reference the preview response shape. The dryRun endpoint table notes were updated for connectors (no outbound calls), experiments (no capacity check), and environments (returns preview object).

---

### API-004 ETag Semantics Gap: List Endpoints Return Per-Item ETags in Body But No Guidance on Race Window [High] — Fixed
**Section:** 15.1

The ETag section specifies that `GET` list responses include per-item ETags in the response body (`"etag": "3"` on each object). This is correct for pre-fetching ETags before subsequent updates, but the spec does not address the race window between listing and updating: if a client lists 50 runtimes, processes them, and then issues a `PUT` for item #50 several seconds later, the ETag obtained from the list may already be stale. The spec correctly returns `412 ETAG_MISMATCH` in this case, but does not provide guidance on the retry pattern — specifically, should the client re-GET the single resource to refresh the ETag, or re-list? For a UI building a "bulk update" workflow (common in admin dashboards), this gap causes either over-fetching or silent lost updates.

Additionally, the `DELETE` endpoint makes `If-Match` optional with "last-writer-wins" semantics when omitted. This is correct for simple cases, but for admin resources that have dependents (e.g., deleting a Runtime that is referenced by active pools or sessions), the spec does not state whether the delete is rejected, cascaded, or performed regardless. A CLI developer handling a `DELETE` on a runtime that has active sessions needs to know whether to expect a `409 INVALID_STATE_TRANSITION` or a `412 ETAG_MISMATCH` or a plain `200` with cascading effects.

**Recommendation:** (1) Add a note stating that the recommended pattern after a `412 ETAG_MISMATCH` on `PUT` is to re-GET the specific resource (not re-list) and retry. (2) Document the deletion semantics for resources with dependents: does deleting a Runtime with active sessions return `409 INVALID_STATE_TRANSITION` with a `details.activeSessionCount` field? Enumerate for each resource type whether deletion is blocked, cascaded, or soft-deleted. (3) Consider adding a `details.currentEtag` field in the `412 ETAG_MISMATCH` error response so clients can refresh without a round-trip GET.

**Resolution:** All three recommendations addressed in Section 15.1. (1) A new "Retry pattern after `412 ETAG_MISMATCH`" bullet was added to the ETag section documenting the recommended flow: use `details.currentEtag` from the error response if present, or re-`GET` the specific resource (not re-list); clients performing bulk updates should re-`GET` only the conflicted resource. (2) A new "Deletion semantics for resources with dependents" bullet enumerates per-resource-type deletion rules: all deletions are **blocked** (never cascaded) when active dependents exist, returning `409 RESOURCE_HAS_DEPENDENTS` with a `details.dependents` array listing blocking references by type, name, and count. Specific rules documented for Runtime, Pool, Delegation Policy, Connector, Credential Pool, Tenant, Environment, Experiment, and External Adapter. (3) The `ETAG_MISMATCH` error catalog entry and the PUT bullet now specify that the `412` response includes `details.currentEtag` with the resource's current ETag, allowing clients to retry without an additional `GET`. A new `RESOURCE_HAS_DEPENDENTS` error code (409, PERMANENT) was added to the error catalog.

---

### API-005 Error Catalog Gaps: Missing Codes for Common Admin API Failure Modes [High] — Fixed
**Section:** 15.1

The error code catalog (Section 15.1) covers session and MCP interaction errors well, but is incomplete for admin API operations. Specific gaps that affect third-party UI/CLI development:

1. **Dependent resource blocking deletion:** No error code for "resource has dependents and cannot be deleted" (e.g., Runtime has active pools, Pool has active sessions). Clients would receive `INVALID_STATE_TRANSITION` (designed for session state machine errors) or `VALIDATION_ERROR` (designed for request field errors), both of which are semantically wrong.

2. **Runtime image validation failure:** No error code for "container image reference is invalid or unreachable" when creating/updating a Runtime with an unresolvable `image` field. `VALIDATION_ERROR` would be used, but a CLI displaying this to a user needs to differentiate "your JSON is malformed" from "your image digest doesn't resolve."

3. **Configuration conflict:** No error code for "the requested configuration would conflict with existing state" — for example, creating a Pool that references a Runtime with `executionMode: session` while requesting `concurrencyStyle: workspace` (invalid combination). This is different from a field-level `VALIDATION_ERROR` and different from `RESOURCE_ALREADY_EXISTS`.

4. **Bootstrap/seed conflicts:** The `POST /v1/admin/bootstrap` endpoint (idempotent upsert) has no error path documented. What happens when a seed entry conflicts with an existing resource in a non-idempotent way?

**Recommendation:** Add the following error codes to the catalog: `RESOURCE_HAS_DEPENDENTS` (409, PERMANENT) for deletion blocked by references; `IMAGE_RESOLUTION_FAILED` (422, PERMANENT) for unresolvable image references; `CONFIGURATION_CONFLICT` (422, PERMANENT) for mutually incompatible field combinations; `SEED_CONFLICT` (409, PERMANENT) for bootstrap upsert conflicts where `--force-update` is not set. Each code should specify the `details` shape (e.g., `RESOURCE_HAS_DEPENDENTS.details.dependents: [{type, name, count}]`).

**Resolution:** All four error codes are now present in the error catalog in Section 15.1. `RESOURCE_HAS_DEPENDENTS` (409, PERMANENT) was already added as part of the API-004 fix, with `details.dependents` listing blocking references by type, name, and count. Three new codes were added: `IMAGE_RESOLUTION_FAILED` (422, PERMANENT) with `details.image` and `details.reason` fields describing the unresolvable reference; `CONFIGURATION_CONFLICT` (422, PERMANENT) with `details.conflicts` array containing per-incompatibility entries identifying the conflicting fields and a human-readable message; `SEED_CONFLICT` (409, PERMANENT) with `details.resource` (type and name) and `details.conflictingFields` listing the fields that differ from the existing resource when `--force-update` is not set.

---

### API-006 Session Lifecycle REST Endpoints Have Ambiguous State Machine Constraints [High] — Fixed
**Section:** 15.1

The REST session lifecycle table lists endpoints like `POST /v1/sessions/{id}/interrupt`, `POST /v1/sessions/{id}/terminate`, `POST /v1/sessions/{id}/resume` without documenting which session states each endpoint is valid in. A UI developer building session controls (e.g., showing an "Interrupt" button) needs to know: is `interrupt` valid in state `running` only, or also in `starting_session`, `finalizing_workspace`, `suspended`? Is `resume` only valid in `awaiting_client_action`, or also in `suspended`? Is `terminate` valid in all non-terminal states?

The pod state machine is documented (Section 6.2), and the session state machine events are listed (Section 7.2), but there is no cross-reference connecting REST endpoint calls to state machine transition preconditions. The error code `INVALID_STATE_TRANSITION` exists but a UI developer cannot know in advance which states to disable which buttons.

This problem is amplified by the fact that the spec describes two overlapping state machine representations — the pod state machine (Section 6.2) and the session/task state machine (Section 7.2, 8.9) — with no explicit mapping of which states are visible to external REST API callers vs. which are internal.

**Recommendation:** (1) Add a table to Section 15.1 listing each state-mutating session endpoint with its valid precondition states and the resulting state transition. E.g., `POST interrupt` → valid in `{running}` → results in `suspended`; invalid in `{suspended, completed, failed, cancelled, expired}` → returns `INVALID_STATE_TRANSITION`. (2) Explicitly declare which states are externally visible (returned in `GET /v1/sessions/{id}`) vs. internal-only. The external state model should be the session/task states from Section 8.9, not the pod states from Section 6.2 — clarify this.

**Resolution:** Both recommendations addressed in Section 15.1. (1) A "State-mutating endpoint preconditions" table was added immediately after the session lifecycle endpoint table, mapping each state-mutating endpoint (`upload`, `finalize`, `start`, `interrupt`, `terminate`, `resume`, `messages`, `derive`, `DELETE`) to its valid precondition states, resulting state transition, and notes. The table specifies that invalid-state calls return `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`. (2) An "Externally visible vs. internal-only states" section was added with two tables: one enumerating all 12 external session states returned by `GET /v1/sessions/{id}` (from `created` through the four terminal states), and a declaration that pod state machine states from Section 6.2 (`warming`, `idle`, `claimed`, `receiving_uploads`, `running_setup`, `sdk_connecting`, `resuming`) are internal-only and never returned in external API responses.

---

### API-007 Pagination Cursor Expiry Creates Unrecoverable List Operations for UI Builders [Medium]
**Section:** 15.1

Pagination cursors expire after 24 hours, and expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`. This is technically correct but creates a problematic UX for UI builders: if a user leaves an admin dashboard tab open overnight and returns the next morning to fetch the next page of sessions (e.g., in an audit log viewer), the operation fails with a validation error that looks identical to "your pagination request is malformed" rather than "your cursor is stale."

More critically, there is no guidance on what to do when a cursor expires mid-traversal: should the client restart from the beginning, or is there a way to resume from an approximate position using other filter parameters? For audit log viewers processing large result sets (common for billing reconciliation — 100M rows at Tier 3), cursor expiry during processing could mean restarting a multi-hour traversal.

Additionally, the `cursor_expired` rule name is buried inside a generic `VALIDATION_ERROR`, making programmatic detection fragile (clients must string-match `rule: "cursor_expired"` inside `details.fields`).

**Recommendation:** (1) Promote cursor expiry to a first-class error code: `CURSOR_EXPIRED` (410 Gone, PERMANENT) with `details.restartHint` pointing to equivalent filter parameters to resume from an approximate position (e.g., `{"after": "2026-01-15T00:00:00Z"}` for time-ordered lists). (2) Document the recommended recovery pattern: on `CURSOR_EXPIRED`, restart with the same filters and a `created_at_after` (or equivalent) parameter set to the last-seen item's timestamp. (3) Consider extending cursor TTL or using cursor-less keyset pagination (stable `?after={id}` parameter) as an alternative for long-running traversals.

---

### API-008 `GET /v1/usage` Response Schema Lacks Delegation-Tree Breakdown [Medium]
**Section:** 15.1

The `GET /v1/usage` response schema includes `byTenant` and `byRuntime` breakdowns, but no breakdown by delegation depth or tree. The spec states that `GET /v1/sessions/{id}/usage` "returns tree-aggregated usage (including all descendant tasks)," but the top-level usage endpoint has no equivalent aggregation. For a billing UI building cost attribution, the distinction between "token spend by the root agent" and "token spend by all children" is important: a session that spawned 50 child tasks with `credentialPropagation: inherit` would show all tokens under the parent's credential pool, making it impossible to determine how much the orchestrator itself consumed vs. its workers.

Additionally, the `byRuntime` breakdown does not distinguish between `executionMode: session` and `executionMode: task` runtimes. A pod in task mode may serve 10 tasks; billing by runtime name conflates pod-time cost (charged by pod-minutes regardless of tasks) with token cost (charged per task).

**Recommendation:** (1) Add a `byDelegationDepth` breakdown to `GET /v1/usage`: `[{"depth": 0, "sessions": N, "tokens": {...}}, {"depth": 1, ...}]` to show orchestrator vs. worker spend. (2) Add an optional `include=tree` query parameter that expands `bySession` to include `treeUsage` (sum of session + all descendants). (3) Add `executionMode` as a dimension in the `byRuntime` breakdown so operators can separate session-mode from task-mode cost attribution.

---

### API-009 Webhook Delivery Model Lacks a Queryable Delivery Status for Specific Events [Medium]
**Section:** 14 (WorkspacePlan), 15.1

The webhook retry behavior is specified (5 attempts with backoff), and failed events are "queryable via `GET /v1/sessions/{id}/webhook-events`." However, this endpoint is not listed in any of the REST API tables (Section 15.1). It appears only in a note inside the WorkspacePlan field documentation. The spec does not document:

- The response schema for `GET /v1/sessions/{id}/webhook-events`
- Whether the endpoint returns all events or only failed/pending ones
- Whether there is a retry-on-demand mechanism (e.g., `POST /v1/sessions/{id}/webhook-events/{event_id}/retry`)
- What happens to webhook events for sessions in `awaiting_client_action` state where the webhook fires but the client may not be listening

For a CI/CD system polling for session completion (a stated use case), the absence of a retry mechanism means that a missed `session.completed` webhook cannot be recovered without re-implementing event delivery out-of-band.

**Recommendation:** (1) Add `GET /v1/sessions/{id}/webhook-events` to the REST API table with a documented response schema: `[{"event": "session.completed", "status": "delivered|failed|pending", "attempts": N, "lastAttemptAt": "...", "idempotencyKey": "..."}]`. (2) Add `POST /v1/sessions/{id}/webhook-events/{event_id}/retry` to force re-delivery of a failed event. (3) Specify that the endpoint returns all events (not only failed) to enable audit of delivery history.

---

### API-010 `POST /v1/sessions/start` Convenience Endpoint Is Underspecified [Medium]
**Section:** 15.1

Two endpoints both named `POST /v1/sessions/start` appear in different tables in Section 15.1: once in the session lifecycle table ("Create, upload inline files, and start in one call (convenience)") and once in the async job support table ("Accepts optional `callbackUrl` for completion notification"). These appear to be the same endpoint described in two different contexts, but they are listed as separate rows without cross-reference. The async job support table notes `callbackUrl` support but the lifecycle table does not. A client SDK author cannot determine whether `callbackUrl` is supported on the basic `POST /v1/sessions/start`, or whether there is a separate "async" variant.

Additionally, the WorkspacePlan schema (Section 14) includes `callbackUrl` as a top-level field, suggesting `callbackUrl` is part of the session creation payload for any `POST /v1/sessions` call — not just `start`. The relationship between `callbackUrl` in the WorkspacePlan and the `callbackUrl` mentioned in the async job support table is undefined.

**Recommendation:** (1) Consolidate the two `POST /v1/sessions/start` rows into one with a note that it "accepts all WorkspacePlan fields including `callbackUrl`." (2) Clarify that `callbackUrl` is part of the WorkspacePlan and therefore available on both `POST /v1/sessions` (create) and `POST /v1/sessions/start` (create+start). (3) Specify what inline file content is accepted in the `start` endpoint (is it the full WorkspacePlan `sources` array, or only `inlineFile` sources, or something else?).

---

### API-011 Admin API Has No Field-Level Patch Support; Full-Document PUT Requires Always-Sending All Fields [Medium]
**Section:** 15.1

The admin API uses `PUT` for updates with ETag-based concurrency. Full-document `PUT` semantics require the client to send all fields, including fields the client did not intend to change. For large resources like Runtime definitions (which can have 20+ fields: `image`, `type`, `capabilities`, `executionMode`, `isolationProfile`, `allowedResourceClasses`, `delegationPolicyRef`, `supportedProviders`, `credentialCapabilities`, `limits`, `setupCommandPolicy`, `setupPolicy`, `runtimeOptionsSchema`, `defaultPoolConfig`, `labels`, `agentInterface`, `publishedMetadata`, `taskPolicy`), this creates two problems for UI builders:

1. The UI must first `GET` the resource to populate all fields before allowing the user to change one field — otherwise the `PUT` inadvertently clears unset fields.
2. When two admin users are editing different fields of the same runtime concurrently, the second `PUT` succeeds (assuming ETags are managed correctly) but silently overwrites the first user's changes to fields they both sent.

**Recommendation:** Add `PATCH` support to admin resource endpoints using JSON Merge Patch (RFC 7396) semantics: only fields present in the request body are updated; absent fields are left unchanged. `PATCH` should also require `If-Match` to maintain concurrency safety. This is a significant improvement for UI builders who want "change just the `labels`" or "change just the `defaultPoolConfig.warmCount`" without touching other fields. Note in the spec that `PATCH` is the preferred update method for partial updates while `PUT` is for full resource replacement.

---

### API-012 Runtime Discovery Response Does Not Declare Which Fields Are Filterable [Medium]
**Section:** 15.1, 9.1

`GET /v1/runtimes` is described as returning "full `agentInterface`, `mcpEndpoint`, `mcpCapabilities`, capabilities, and labels." The endpoint accepts `?environmentId=` and presumably other filter parameters (the spec mentions sessions are filterable "by status, runtime, tenant, labels" but uses different filter parameter names than those implied for runtimes). There is no documented list of query parameters for `GET /v1/runtimes`.

Similarly, `GET /v1/sessions` is described as "filterable by status, runtime, tenant, labels" without naming the actual query parameter keys (`?status=`, `?runtime=`, `?tenant_id=`, `?labels[team]=`?). The label-based filtering (which is a stated key feature: "Labels are indexed in the session store and filterable in all query APIs") has no documented syntax. Kubernetes label selector syntax? Simple key-value pairs? AND semantics or OR?

This is a material gap for a CLI or UI developer who needs to build a "show me all running sessions for the research team" filter.

**Recommendation:** Add a query parameters table to each list endpoint specifying the exact parameter names, types, and semantics. For `GET /v1/sessions`: `?state=running|completed|...` (multi-value), `?runtime=<name>`, `?tenant_id=<id>`, `?label.<key>=<value>` (Kubernetes label-selector style, AND semantics). For `GET /v1/runtimes`: `?type=agent|mcp`, `?label.<key>=<value>`, `?environmentId=<name>`. Explicit parameters are required for the OpenAPI spec to be complete and for SDK code generation to work correctly.

---

### API-013 `lenny-ctl` CLI Specification Is Insufficiently Detailed for Third-Party Implementation [Low]
**Section:** 17.6, 21.8

Section 21.8 states "Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic." Section 17.6 documents `lenny-ctl bootstrap` with exit codes and behavior. However, no other `lenny-ctl` commands are documented, and there is no specification of the command structure, output format, or authentication mechanism. A third-party CLI developer (or a team building on top of Lenny) cannot determine from the spec:

- How `lenny-ctl` authenticates (OIDC device flow? Service account token from env var? Flag?)
- What the top-level command structure is (e.g., `lenny-ctl sessions list`, `lenny-ctl admin runtimes create`)
- Whether `lenny-ctl` supports machine-readable output (`--output json`)
- Whether `lenny-ctl` is a single binary or a plugin system

**Recommendation:** Add a Section 21.8.1 documenting the `lenny-ctl` command taxonomy: top-level command groups (`sessions`, `admin`, `bootstrap`, `preflight`), authentication flags (`--token`, `--config`, env var `LENNY_TOKEN`), and output format flag (`--output text|json|yaml`). This section need not be exhaustive but should establish the conventions so third-party tools can follow the same patterns.

---

### API-014 OpenAPI Spec Generation Process Is Described But Its Output Is Not Committed or Versioned [Low]
**Section:** 15.2.1, 15.6

Section 15.2.1 states "A code generation step in the build pipeline produces MCP tool JSON schemas from OpenAPI operation definitions." Section 15.6 states "SDKs are generated from the OpenAPI spec." But neither the location of the OpenAPI spec nor its versioning strategy is documented. There is no reference to a `openapi.yaml` or `openapi.json` file in the repository structure. Questions a third-party SDK author would have:

- Is the OpenAPI spec committed to the repository or generated on-the-fly at build time?
- Is it published at a stable URL (e.g., `GET /openapi.json` on the gateway)?
- Does the gateway serve a live spec at runtime (reflecting registered external adapters)?
- Is the spec versioned alongside the API version (`/v1/openapi.json`)?

**Recommendation:** (1) State that the OpenAPI spec is generated from code (or hand-authored) and committed to `api/openapi/v1.yaml` in the repository. (2) The gateway MUST serve the spec at `GET /openapi.json` (or `GET /v1/openapi.json`). (3) Specify that the live spec reflects only the built-in adapters, not runtime-registered external adapters (which would require dynamic spec generation). (4) Document the spec versioning policy: new fields in v1 are additive; removal triggers a v2 at `/v2/`.

---

### API-015 `callbackSecret` Field in WorkspacePlan Has No Documented Validation or Storage Constraints [Low]
**Section:** 14

The WorkspacePlan documents `callbackUrl` with extensive SSRF mitigation and the webhook signing mechanism (`X-Lenny-Signature` header, HMAC-SHA256), but `callbackSecret` (the signing secret provided by the client) has no documented constraints:

- Is there a minimum entropy requirement (e.g., at least 16 bytes)?
- Is there a maximum length?
- Is the secret stored in plaintext in the session record, or encrypted?
- Is the secret returned in `GET /v1/sessions/{id}` (would be a security issue) or redacted?
- What happens if the client omits `callbackSecret` — are webhooks unsigned, or is `callbackUrl` rejected without a secret?

A client developer implementing webhook verification cannot write correct code without knowing whether unsigned webhooks are possible.

**Recommendation:** (1) State that `callbackSecret` is required when `callbackUrl` is set (gateway returns `VALIDATION_ERROR` if `callbackUrl` is present without `callbackSecret`). (2) Specify minimum length (e.g., 16 characters) and that the gateway rejects secrets below this threshold. (3) State that `callbackSecret` is stored encrypted (same T4-tier controls as OAuth tokens) and is never returned in any API response — `GET /v1/sessions/{id}` returns a redacted marker (e.g., `"callbackSecretSet": true`) rather than the value. (4) Reference the secret storage in the data classification table (Section 12.9).

---

### API-016 MCP Version Deprecation Warning Mechanism Is Incomplete [Info]
**Section:** 15.2

The spec states: "The gateway emits a `mcp_version_deprecated` warning header on connections using the deprecated version." This is a good practice, but the header name is not specified (`X-Lenny-MCP-Deprecated`? `Warning`?), its format is undefined, and there is no guidance on how clients should surface this to end users. For a client SDK author implementing automatic version negotiation, the absence of a header name means they cannot reliably detect deprecation warnings.

**Recommendation:** Specify the exact header name and format: `Warning: 299 - "MCP version 2024-11-05 is deprecated and will be removed after 2026-10-01. Upgrade to 2025-03-26."` (RFC 7234 Warning header format). Alternatively, use a custom header `X-Lenny-Protocol-Warning: deprecated-mcp-version` with a companion `X-Lenny-Protocol-Warning-Expires` header. Document this in the MCP compatibility policy section.

---

### API-017 `POST /v1/admin/bootstrap` Is Listed in the Admin API Table Without an OpenAPI Contract [Info]
**Section:** 15.1, 17.6

`POST /v1/admin/bootstrap` accepts "a seed file (idempotent upsert of runtimes, pools, tenants, etc.)" with the note "Same schema as `bootstrap` Helm values." The spec documents `lenny-ctl bootstrap` behavior (exit codes, `--force-update`, `--dry-run`) but does not document the HTTP API contract for `POST /v1/admin/bootstrap`: request body schema, response body schema, authentication requirements, or idempotency key behavior. A team building a GitOps operator that calls the bootstrap API directly (without `lenny-ctl`) has no contract to program against.

**Recommendation:** Document `POST /v1/admin/bootstrap` with a request body schema reference (pointing to the seed file schema in Section 17.6), a response schema (`{"created": N, "updated": N, "skipped": N, "errors": [...]}`), and a note that the endpoint is idempotent (safe to call on every GitOps sync). Clarify whether it requires `platform-admin` role or whether `tenant-admin` can bootstrap their own tenant's resources.
