# Summary: Session owner is the creating principal's subject, and session access is owner-scoped at the sessionserver route wrapper

## Summary

**Problem statement.** Every client-facing create surface takes the session owner from the request instead of from the authenticated principal. The affected surfaces are REST create and create-and-start, MCP `create_session` and its ungated fallback, derive, and both built-in OpenAI-dialect adapters. The per-user concurrency cap and the §12.8 creation fence therefore act on a value the client chooses. As a result, a suspended or erasure-pending user can still create sessions, and those sessions fall outside the user's erasure. On the access side, every session route checks only a role permission, and no code compares the caller with the session's owner. A `user` can list, read, message, cancel, terminate, derive from, and replay any session in its tenant. The specification promises owner scoping, but it does not define the tenant-admin "explicit grant", say who may act on another user's session, name the error a non-owner receives, or cover a request with no principal subject. Approved proposal 0108 stages a tenant-only read rule for `GET /v1/responses/{id}` that would contradict §10.2.

**What changes.**

- §10.2 gains the **Session ownership.** paragraph. The `user` bullet, the matrix cell, §29.3 step 2, §9.2, and the `ELICITATION_NOT_FOUND` row cite it, and 0108's staged §15 Open Responses text is amended to use it.
- The sessionserver create paths bind the owner from the principal. Every client owner field is deleted from the handlers, the delegation request, `openapi.json`, the SDKs, and the CLI.
- The `read` and `manage` route wrappers check ownership on every `{id}` route, and `handleList` filters by owner.
- The MCP tools that read the store directly, and the SSE attach authorizer, call the same predicate.
- After 0108 lands, the translators bind the owner from the principal, and the Open Responses resolve step and warm condition 4 apply the rule.
- Docs and records are aligned with the rule.

**Decisions.**

- The owner is the creating principal's subject. It never changes and no request field sets it. Client owner fields are deleted rather than validated.
- A derived or replayed session takes the source owner, and a delegated child takes the parent owner.
- A request with no principal subject that the matrix admits gets `401 UNAUTHORIZED`, in every mode, with no dev exception.
- The owner may read a session, and so may a principal SPEC-1 rules 4 and 7 admit. The read bypass never uses the role-less single-tenant fall-through.
- The tenant-admin grant is a custom-role assignment. It is not `tenant-viewer`, because the specification assigns one role per user.
- Actions are owner-only for every role. Cross-user termination stays on the admin force-terminate endpoint.
- A non-owner gets exactly the response for a session that does not exist.
- One seam: the route wrapper covers every `{id}` route except eval and `/usage`, with no exemption list. The ownership check precedes the §9.2 elicitation triple check (option A).
- The MCP `create_session` legacy direct-store fallback and `delegation.Request.UserID` are deleted.
- 0108's files are not edited. SPEC-4 amends its text after it lands, and CODE-4, CODE-5, and TEST-2 land after 0108 CODE-4 and CODE-5.

**Watch out for.**

- `sessionPermissionGranted` returns true for every permission for a role-less single-tenant principal. Reusing it for the read bypass reopens the confidentiality hole. CODE-2 step 2 names the helper to use.
- The permission gate stays outer. A `tenant-viewer` on an action route gets `403`, and only callers that the matrix admits reach the `401` or the `404`. Tests that expect `404` for every non-owner are wrong.
- The dev-header transport accepts an empty `X-Lenny-User-ID`. Many harnesses send only `X-Lenny-Tenant-ID` and start getting `401` once CODE-2 lands (TEST-1 item 11).
- The `attach_session` SSE stream is authorized in `cmd/lenny-gateway/mcpsurface.go`, outside both the route table and the MCP tool handlers. Leaving it out keeps the main MCP read leak open.
- The translator's noop binder is the default in-memory path and writes rows with no gates. It needs its own `401` because it cannot import sessionserver (CODE-5).
- SPEC-4 applies only after 0108's SPEC-2 is in `spec/15_external-api-surface.md`, and CODE-4, CODE-5, and TEST-2 apply only after 0108 CODE-5. 0108 CODE-5 step 2 compares `req.User` with `row.UserID`, and CODE-4 replaces that comparison.
- Missing-session codes change on the tool-use and elicitation routes and on the MCP elicitation tools. Existing assertions of `ELICITATION_NOT_FOUND` for a missing session move to `RESOURCE_NOT_FOUND`.

## Goals

- Every session's owner is the authenticated subject that created it, or the source or parent owner for derive, replay, and delegation, on every create surface.
- The per-user concurrency key, the §12.8 creation fence subject, and the stored owner are one value, and an empty owner can no longer be written.
- A `user` reads and acts only on its own sessions on the §15.1 session surface, the §15.2 session tools, the `attach_session` stream, and the Open Responses read and continuation paths.
- The specification states the ownership rule once in §10.2, defines the grant, names the non-owner response, covers the no-subject case, and has no conflicting read rule after 0108 lands.

## Non-goals

- A new permission and matrix row "Manage other users' sessions (same tenant)". It adds a permission, a matrix row, and custom-role validation for a capability the validated problem does not require. The matrix grants every role only "Create / cancel own sessions".
- An action bypass from `read_tenant_sessions` combined with `manage_own_sessions`. A custom role holding both, such as the specification's `session-manager` example, would gain write access to other users' sessions from a read grant.
- Hard-coding `tenant-admin` and `platform-admin` role names as an action bypass. It sidesteps the permission and custom-role model.
- A service-principal bypass keyed on `caller_type`. On the bearer path `caller_type` comes from token claims, and service accounts already carry roles, so they read through those roles.
- Recording an empty owner for a request with no principal subject. It keeps the §12.8 marker skip reachable, needs a separate owner-scoped list flag, and departs from the `/v1/credentials` `401` precedent.
- A `ListFilter.OwnerScoped` field. It is unnecessary, because a caller with no subject is refused before any list runs.
- A dev-mode exception that admits a request with no subject. It is a tier-dependent path. The spec-defined dev paths synthesize a subject, and dev-header callers send `X-Lenny-User-ID`.
- A derived session owned by the caller. Owner-only actions make the caller and the source owner the same principal, and the rule would move a copy of a workspace out of its owner's erasure scope under any future bypass.
- Recording the adapter `user` field or the body `userId` as untrusted metadata. Nothing would read it.
- Rejecting a body that carries `userId` with `400`. The decoders ignore unknown fields, no deployments exist, and the field leaves the schema.
- A store-level write-once owner. No mutator assigns `UserID`, the specification states immutability, and the wrapper is correct without it.
- An owner-scoped store view for MCP `deps.Store` or the translator store. That store also serves delegation, parent lookups, and agent tools.
- An owner check inside each handler or in `sessionstore.Get`. Per-handler checks are the endpoint survey the problem rules out, and a store-level check breaks the GC, erasure, and delegation readers.
- Folding the caller's subject into the §11.5 idempotency hash. The cross-principal replay predates this design and is filed as a defect.
- Exempting the elicitation routes from the wrapper so that §9.2 answers first. The seam stays uniform, and SPEC-5 orders the two checks instead.
- A §15.1 pointer sentence. §10.2 is the single home, and the `RESOURCE_NOT_FOUND` row already reads "not visible to the caller".
- Edits to §12.8, the §12.9 T4 "explicit grant", §15.2, or the §24 lenny-ctl table. Each stays true once §10.2 defines the owner, and T4 is a separate data-classification mechanism.
- Caching the owner in replica memory or Redis, or passing the wrapper's row to handlers. The second read is safe because the owner never changes.
- Owner-scoping `GET /v1/blobs/{ref...}`, and changing the §10.7 eval caller rule. The blob gap is filed, and eval is open by §10.7.
- Splitting into two proposals now. The A/B split remains the fallback, and the sequence already lands part A first.
- Making no spec change. Three points cannot be settled from the current text: who may act across owners (the `tenant-admin` bullet and the matrix conflict), the no-subject case, and 0108's tenant-only text.

## Open decisions for human to make

The spec loop routed the following decisions to the human and derived no recommendation for any of them. The open-decisions-and-impact-review phase supplies the recommendations.

1. **Role-less principals and the permission matrix.** In single-tenant mode, `sessionPermissionGranted` in `pkg/gateway/sessionserver/rbac_gate.go` admits a principal that has no assigned role for every session permission. No specification text states this admission: the matrix columns name only built-in roles. Should §10.2 state the single-tenant admission of role-less principals, or should the code drop it so that a role-less principal is refused? This proposal stages neither. **Session ownership.** rule 4 already limits a role-less principal to its own sessions, and rule 7 says only that "a caller the matrix refuses receives its refusal". The spec loop confirmed that the §25 conformance test asserting `403` for a role-less token covers the MCP Management Server rather than the §15.2 session tools, so the current text does not contradict the code. (Review log spec.4.fix-G1.1, spec.4.fix-design-G1.1, and spec.6.review-mechanism.1.)
2. **Scope of the cross-principal idempotent replay defect.** The summary files cross-principal §11.5 idempotent replay as an unstaged defect for session create only. The same ordering, with replay keyed by (tenant, key) and run before route authorization, lets a same-tenant non-owner replay a cached response for an action on `/v1/sessions/{id}/finalize`, `/start`, `/resume`, `/derive`, or `/tool-use/...`. That breaks the "exactly the response for a session that does not exist" outcome of **Session ownership.** rule 7. Should the filed defect entry be widened to cover these `{id}`-route actions? The security lens declined to file it against the spec, because §11.5 states no ordering relative to authorization and folding the subject into the idempotency hash is a non-goal. (Review log spec.1.review-reliability.1 and spec.6.review-security.1.)
3. **The MCP `list_sessions` tool in rule 6.** **Session ownership.** rule 6 names `GET /v1/sessions` as listing only the sessions the caller may read, but it does not name the MCP `list_sessions` tool. In code, `list_sessions` dispatches to `GET /v1/sessions`, and §15.2.1 rule 1 (semantic equivalence) arguably covers it by transitivity. Should rule 6 name `list_sessions` explicitly? (Review log spec.1.review-applicability.1.)
4. **`await_children` classified as an act.** CODE-3 gives `await_children` `act=true`. Under **Session ownership.** rule 5, a request that only retrieves a session's state or content is a read, and `await_children` only retrieves. The value is harmless today because every in-tree caller shares the session's owner, but rule 5 does not support it. Should CODE-3 change `await_children` to `act=false`? (Review log spec.1.fix-design-G1.1.)
5. **A test for a read-others-only custom role.** TEST-1's custom-role principal holds all three session permissions. No case pins that a custom role granting only "Read other users' sessions (same tenant)" receives `403` on a session read, which follows from rule 7 checking a read against "Read own session history". Should TEST-1 add that case to its role-by-route matrix? (Review log spec.3.fix-design-G1.1.)

## Defects in the shipped tree that this proposal does not stage

- **Blob resolution checks only the tenant.** `GET /v1/blobs/{ref...}` (`handleBlob` in `upload.go`) checks only the tenant prefix through a tenant-scoped view, while §15.1 promises a check against the owning session. This entry succeeds closed F-15.1.32, which deferred the per-owner blob check to this §10.2 owner rule. Its resolution has to choose between §15.1's `403` and the non-revealing `404` that **Session ownership.** gives. It stays out of scope because the route is outside `/v1/sessions/{id}` and the validated problem.
- **The MCP and adapter create services skip the manage gate.** `CreateSessionService` and `CreateAndStartService` call `guardTenant(handleCreate*)` directly, so the `manage_own_sessions` gate does not run. This lets a `tenant-viewer` create sessions through MCP and the adapters. It stays out of scope because it concerns permission gating rather than ownership.
- **Cross-principal idempotent replay.** §11.5 replay is keyed by (tenant, key) and runs before route authorization, so a second principal that sends the same key and body receives the cached create response, including the `uploadToken`. The defect predates this design and lives on a §11.5 surface outside the validated problem.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0108_fix_proposal-0061-re-derived-plan-item-gateway-runtime-comms | Approved | SPEC-4 amends the §15 **Open Responses conversation sessions.** text that 0108 SPEC-2 stages. Its two "in the caller's tenant" sentences become reads under §10.2 **Session ownership.**, and warm condition 4 compares the principal with the owner instead of the request `user`. CODE-4 adds the ownership check to the resolve step from 0108 CODE-4 and replaces 0108 CODE-5's `req.User` comparison. CODE-5 removes `UserID: req.User` from the files 0108 rewrites. | Nothing. 0108 lands as staged, and its files are not edited. This proposal's SPEC-4, CODE-4, CODE-5, and TEST-2 apply after 0108's S1, S7, and S8. |
| 0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of | Approved | Both edit `create_and_start.go`, `delegation/service.go`, and `mcptools_register.go`, at different symbols. No semantic dependency. | Whichever lands second rebases textually. |
| 0107_fix_the-delegation-tree-s-budget-lifecycle-is-under-specified | Approved | Both edit `delegation/service.go`, `derive.go`, `replay.go`, and `mcptools_register.go`, at different symbols. No semantic dependency. | Whichever lands second rebases textually. |
| 0109_fix_a-derived-or-replayed-session-starts-without-its-source | Approved | Both edit `derive.go`. 0109 changes the snapshot copy, and this proposal changes the derived row's owner. Both edit `docs/operator-guide/security.md` in different sections. No semantic dependency. | Whichever lands second rebases textually. |

## Deliverable index

- SPEC-1 — spec/10_gateway-internals.md — new §10.2 **Session ownership.** paragraph after the **Permission matrix:** table.
- SPEC-2 — spec/10_gateway-internals.md — §10.2 `user` role bullet and the "Read other users' sessions (same tenant)" matrix row.
- SPEC-3 — spec/29_communication-scenarios.md — §29.3 step 2 cites **Session ownership.**
- SPEC-4 — spec/15_external-api-surface.md — amends 0108's **Open Responses conversation sessions.** read sentences and warm condition 4, after 0108 SPEC-2 lands.
- SPEC-5 — spec/09_mcp-integration.md — §9.2 **`respond_to_elicitation` authorization:** applies the ownership check before the triple check.
- SPEC-6 — spec/15_external-api-surface.md — §15.1 `ELICITATION_NOT_FOUND` catalog row scoped to a session the caller may act on.
- CODE-1 — pkg/gateway/sessionserver, pkg/gateway/mcpfabric/mcptools, pkg/gateway/mcpfabric/delegation, pkg/gateway/externalapi/openapi/openapi.json, sdks/client, pkg/embedded/localcli/session.go — principal-bound owner on the sessionserver create paths and deletion of client owner inputs.
- CODE-2 — pkg/gateway/sessionserver/rbac_gate.go, route_table.go, session_read.go, service.go — access predicate, wrapper owner check, and owner-filtered list.
- CODE-3 — pkg/gateway/mcpfabric/mcptools, cmd/lenny-gateway/mcpsurface.go — MCP session-naming tools and the SSE attach authorizer call the predicate.
- CODE-4 — pkg/gateway/environment/translator, cmd/lenny-gateway/credentialsurface.go — Open Responses resolve-step check and warm condition 4, after 0108 CODE-4 and CODE-5.
- CODE-5 — pkg/gateway/environment/translator, cmd/lenny-gateway/credentialsurface.go — translator owner binding and the noop binder `401`, after 0108 CODE-5.
- DOCS-1 — docs/operator-guide/security.md, docs/api/openai-completions.md, docs/client-guide/sdk-examples — docs aligned with the owner rule.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — close findings, tick the plan entry, and file the unstaged defects.
- TEST-1 — pkg/gateway, tests/tier3_contract, tests/tier4_integration, tests/tier9_security, and the dev-header harnesses — part A tests.
- TEST-2 — pkg/gateway/environment/translator, tests/tier3_contract/rest_openai_responses — part B tests.
