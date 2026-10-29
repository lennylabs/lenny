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
- 0108's files are not edited. SPEC-4 and part B land after 0108 (checklist S5, S10, and S11).

**Watch out for.**

- `sessionPermissionGranted` returns true for every permission for a role-less single-tenant principal. Reusing it for the read bypass reopens the confidentiality hole. CODE-2 step 2 names the helper to use.
- The permission gate stays outer. A `tenant-viewer` on an action route gets `403`, and only callers that the matrix admits reach the `401` or the `404`. Tests that expect `404` for every non-owner are wrong.
- Most existing session tests reach sessionserver with no principal or an empty `Subject`, and they get `401` once CODE-1 lands. Sending `X-Lenny-User-ID` fixes only callers behind the auth middleware (TEST-1 item 11).
- The `attach_session` SSE stream is authorized in `cmd/lenny-gateway/mcpsurface.go`, outside both the route table and the MCP tool handlers. Leaving it out keeps the main MCP read leak open.
- The translator's noop binder is the default in-memory path and writes rows with no gates. It needs its own `401` because it cannot import sessionserver (CODE-5).
- SPEC-4 applies only after 0108's SPEC-2 is in `spec/15_external-api-surface.md`. 0108 CODE-5 step 2 compares `req.User` with `row.UserID`, and CODE-4 replaces that comparison.
- Not-found codes change for a missing or another user's session on the tool-use and elicitation routes and the MCP elicitation tools (CODE-2 step 4, CODE-3 step 4). TEST-1 item 11 moves the existing assertions.

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

None. Decision 1 (role-less principals and the permission matrix) was decided on 2026-10-29 by the orchestrator under the owner's standing delegation, taking the recommendation: this proposal stages neither change, and the defects list below records the gap for a successor proposal. The `await_children` classification stays an act (owner only), because the tool begins deadlock tracking and stamps qualifying activity on the parent session.

## Defects in the shipped tree that this proposal does not stage

- **The single-tenant role-less admission has no specification statement.** `sessionPermissionGranted` (`pkg/gateway/sessionserver/rbac_gate.go`) admits a principal whose token carries no role to every session permission in a single-tenant deployment and refuses it in a multi-tenant one (the F-10.2.4 fall-through, pinned by `TestSessionEndpointAdmitsRolelessPrincipal`). The §10.2 matrix names only built-in roles, and no text states the admission. A successor proposal decides between stating it in §10.2 and refusing role-less principals in code. The ownership rule here does not depend on it, because **Session ownership.** rule 4 limits a role-less principal to its own sessions in every tenancy mode.

- **Blob resolution checks only the tenant.** `GET /v1/blobs/{ref...}` (`handleBlob` in `upload.go`) checks only the tenant prefix through a tenant-scoped view, while §15.1 promises a check against the owning session. This entry succeeds closed F-15.1.32, which deferred the per-owner blob check to this §10.2 owner rule. Its resolution has to choose between §15.1's `403` and the non-revealing `404` that **Session ownership.** gives. It stays out of scope because the route is outside `/v1/sessions/{id}` and the validated problem.
- **The MCP and adapter session surfaces skip the permission matrix.** `CreateSessionService` and `CreateAndStartService` call `guardTenant(handleCreate*)` directly, so the `manage_own_sessions` gate does not run. This lets a `tenant-viewer` create sessions through MCP and the adapters. The direct-store MCP tools in the CODE-3 step 3 table run no permission check beyond `requirePrincipalTenant`, so after CODE-3 a `tenant-viewer` on an act row and a `billing-viewer` on any row receive `RESOURCE_NOT_FOUND` on another user's session and are admitted on their own, and an empty-`Subject` principal receives `UNAUTHORIZED`, where **Session ownership.** rule 7 gives the matrix refusal. It stays out of scope because it concerns permission gating rather than ownership.
- **Cross-principal idempotent replay.** §11.5 replay is keyed by (tenant, key) and runs before route authorization on every path in the middleware's `AllowedPaths` (`cmd/lenny-gateway/httpsurface.go`). A second principal that sends the same key and body therefore receives the cached response. On create, that response includes the `uploadToken`. On `POST /v1/sessions/{id}/finalize`, `/start`, `/resume`, `/derive`, and `/tool-use/...`, a same-tenant non-owner receives the owner's cached action response, which defeats the not-found outcome of **Session ownership.** rule 7. The defect predates this design and lives on a §11.5 surface outside the validated problem.
- **The §12.9 T4 "explicit grant" is undefined.** The §12.9 **Controls driven by classification tier:** table gives T4 access control as "RBAC role ≥ `admin` + explicit grant" (`spec/12_storage-architecture.md:1094`), and no section defines that grant. T4 controls cover session transcripts (`spec/12_storage-architecture.md:1085`), while the current §10.2 matrix already lets a `user` read its own session history and a tenant-admin read other users' sessions with no further grant, so the two sections disagree on who reads a T4 transcript. The disagreement predates this design, and **Session ownership.** rule 4 restates the existing matrix rather than creating it. It stays out of scope because T4 is a separate data-classification mechanism, and no staged text defines or relies on the T4 grant.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0108_fix_proposal-0061-re-derived-plan-item-gateway-runtime-comms | Approved (approved-date 2026-10-29; not yet implemented) | SPEC-4 amends the §15 **Open Responses conversation sessions.** text that 0108 SPEC-2 stages. Its two "in the caller's tenant" sentences become reads under §10.2 **Session ownership.**, and warm condition 4 compares the principal with the owner instead of the request `user`. CODE-4 adds the ownership check to the resolve step from 0108 CODE-4 and replaces 0108 CODE-5's `req.User` comparison. CODE-5 removes `UserID: req.User` from the files 0108 rewrites. TEST-2 items 5 and 6 rewrite 0108's condition-4 tier-1 row and its tier-9 condition-4 case, and TEST-2 item 7 updates 0108's Open Responses test fixtures. | Nothing. 0108 lands as staged, and its files are not edited. Checklist S5, S10, and S11 hold the landing order. |
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
- CODE-1 — pkg/gateway/sessionserver, pkg/gateway/mcpfabric/mcptools, pkg/gateway/mcpfabric/delegation, cmd/lenny-gateway/credentialsurface.go, pkg/gateway/externalapi/openapi/openapi.json, sdks/client, tests/tier3_contract/sdks, pkg/embedded/localcli/session.go, pkg/ctlcli/install_smoke.go — principal-bound owner on the sessionserver create paths and deletion of client owner inputs.
- CODE-2 — pkg/gateway/sessionserver/rbac_gate.go, route_table.go, session_read.go, service.go — access predicate, wrapper owner check, and owner-filtered list.
- CODE-3 — pkg/gateway/mcpfabric/mcptools, cmd/lenny-gateway/mcpsurface.go — MCP session-naming tools and the SSE attach authorizer call the predicate.
- CODE-4 — pkg/gateway/environment/translator, cmd/lenny-gateway/credentialsurface.go — Open Responses resolve-step check and warm condition 4.
- CODE-5 — pkg/gateway/environment/translator — translator owner binding and the noop binder `401`.
- DOCS-1 — docs/operator-guide/security.md, docs/api/openai-completions.md, docs/client-guide/sdk-examples — docs aligned with the owner rule.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — close findings, tick the plan entry, and file the unstaged defects.
- TEST-1 — pkg/gateway, cmd/lenny-gateway, tests/tier2_component, tests/tier3_contract, tests/tier4_integration, tests/tier7a_load_local, tests/tier8_chaos, tests/tier9_security, and tests/testinfra — part A tests.
- TEST-2 — pkg/gateway/environment/translator, tests/tier2_component, tests/tier3_contract, tests/tier7a_load_local, tests/tier9_security, and tests/testinfra — part B tests.
