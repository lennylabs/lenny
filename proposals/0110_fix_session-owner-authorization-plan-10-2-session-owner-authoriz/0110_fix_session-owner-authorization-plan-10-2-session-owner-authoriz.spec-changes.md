# Spec changes — Session owner is client-assertable and user-role session reads are not owner-scoped

## Design (as the spec must state it)

§10.2 gains one normative home for session ownership, the bold paragraph **Session ownership.** that SPEC-1 inserts after the **Permission matrix:** table. Its numbered rules state where the owner comes from, how derived, replayed, and delegated sessions get theirs, what a request with no principal subject receives, who reads and who acts on a session, which surfaces the rule covers, and the order and outcome of the check. Every other staged edit cites that paragraph by heading and rule number rather than restating it:

- SPEC-2 makes the `user` role bullet and the "Read other users' sessions (same tenant)" matrix cell point at **Session ownership.**, and defines the tenant-admin grant as an existing custom-role assignment.
- SPEC-3 makes §29.3 step 2 cite **Session ownership.**
- SPEC-5 and SPEC-6 place the §9.2 elicitation triple check after the ownership check, so a single request carries one normative error code.
- SPEC-4 amends the §15 **Open Responses conversation sessions.** paragraph that proposal 0108 stages, so its read rule and its warm condition 4 use the §10.2 rule. SPEC-4 applies only after 0108's SPEC-2 has landed in `spec/15_external-api-surface.md`.

The design adds no permission, matrix row, request field, error code, endpoint, or stored column.

## Edge cases and accepted failure modes

Each case below is owned by the staged text named on it.

- A `tenant-viewer` calling an action endpoint on any session receives the permission matrix's refusal, because the matrix is checked before **Session ownership.** (SPEC-1 rule 7). It does not receive `404`.
- A caller that may read another user's session (SPEC-1 rules 4 and 7) receives `404 RESOURCE_NOT_FOUND` on every action against it (SPEC-1 rule 5). This includes a `tenant-admin` and a `platform-admin` on the §15.1 session surface. Cross-user termination remains on the admin force-terminate endpoint.
- A user given cross-user reads by a tenant-admin keeps the ability to create sessions only when the assigned custom role also grants "Create / cancel own sessions" (SPEC-2). Assigning `tenant-viewer` replaces the `user` role and removes that ability, because the specification assigns one role per user.
- A principal with no assigned role reads only its own sessions (SPEC-1 rule 4), whatever the deployment's tenancy mode.
- A derive or replay request always comes from the source owner, because derive and replay are actions (SPEC-1 rule 5). The derived or replayed session therefore stays in that owner's §12.8 erasure scope (SPEC-1 rule 2).
- A delegated child's in-pod calls act under the tree's single owner, so traffic inside a delegation tree passes the check (SPEC-1 rules 2 and 5).
- A non-owner resolving a tool call or elicitation on another user's session receives `404 RESOURCE_NOT_FOUND` before any elicitation lookup. The owner of the session receives `404 ELICITATION_NOT_FOUND` for an unknown elicitation id (SPEC-1 rule 7, SPEC-5, SPEC-6). Both are `404 PERMANENT`.
- A request with no principal subject that the permission matrix admits receives `401 UNAUTHORIZED` and leaves no session row (SPEC-1 rule 3).
- After SPEC-4: a reader-bypass principal that continues another user's response goes cold and creates a session it owns. It never takes a warm turn on the other user's held pod (SPEC-4 condition 4).
- Accepted: a stale client that still sends `userId` or the adapter `user` field has the field ignored rather than rejected (SPEC-1 rule 1).

## Staged edits

### SPEC-1 · spec/10_gateway-internals.md § 10.2 Authentication

Under `#### Authorization and RBAC`, insert the following paragraph directly after the **Permission matrix:** table (whose last row begins "| Access other tenants' data") and before the paragraph that begins "**Platform roles vs. environment-level roles:**".

```markdown
**Session ownership.** Every session has one owner, and the following rules govern who reads and who acts on it.

1. A session's owner is the subject of the authenticated principal whose request created it. The gateway records the owner when it creates the session, and the owner never changes. No request field sets the owner: a `userId` on a create or derive body and the `user` field of a built-in adapter request ([Section 15](15_external-api-surface.md#15-external-api-surface)) are ignored.
2. A derived or replayed session takes its source session's owner. A delegated child takes its parent's owner ([Section 8.2](08_recursive-delegation.md#82-delegation-mechanism) **Child identity.**).
3. A request to a surface rule 6 covers, or a session-create request, that the permission matrix admits and whose principal is absent or has an empty subject is refused with `401 UNAUTHORIZED`. It creates, reads, and changes no session.
4. A principal reads a session it owns. It also reads another user's session when a role assigned to it, built-in or custom, grants "Read other users' sessions (same tenant)". A principal with no assigned role reads only its own sessions. A tenant-admin gives a principal this permission only by assigning such a role through **Role assignment:** below.
5. Only a session's owner acts on it, whatever the caller's role. A request to a surface rule 6 covers acts on the session unless it only retrieves the session's state or content, and deriving from or replaying a session, or replying to an `elicitation/create` request on its `attach_session` stream, acts on it. An operator ends another user's session through `POST /v1/admin/sessions/{id}/force-terminate` ([Section 24.11](24_lenny-ctl-command-reference.md#2411-session-investigation)).
6. The rules cover every `/v1/sessions/{id}` endpoint in [Section 15.1](15_external-api-surface.md#151-rest-api) except `POST /v1/sessions/{id}/eval`, whose caller rule [Section 10.7](#107-experiment-primitives) states, and `GET /v1/sessions/{id}/usage` and the `get_token_usage` tool, which the "View usage / metering" row governs. They also cover the [Section 15.2](15_external-api-surface.md#152-mcp-api) tools that name a session, including the `attach_session` stream, and `GET /v1/sessions`, which lists only the sessions the caller may read.
7. The gateway applies these rules after the permission matrix admits the operation, checking a read against "Read own session history" and a session create or an act (rule 5) against "Create / cancel own sessions", and a caller the matrix refuses receives its refusal. On a covered endpoint the session check precedes every route-specific lookup, so a caller that may not act on the session receives `404 RESOURCE_NOT_FOUND` before any tool-call or elicitation lookup runs. A caller that may not read or act on a session receives exactly the response for a session that does not exist: `404 RESOURCE_NOT_FOUND` on REST and MCP, and the native `404` on a built-in adapter. This hides the session's existence, consistent with the existence-hiding rationale of [Section 9.2](09_mcp-integration.md#92-elicitation-chain) **`respond_to_elicitation` authorization:**.
```

### SPEC-2 · spec/10_gateway-internals.md § 10.2 Authentication

(a) Under `#### Authorization and RBAC`, replace the role bullet that reads "- **`user`**: Can create and manage their own sessions. Cannot access other users' sessions (even within the same tenant) unless explicitly granted by a tenant-admin." with:

```markdown
- **`user`**: Can create and manage their own sessions. Cannot read or act on other users' sessions, even within the same tenant. A tenant-admin gives a user read access to other users' sessions in the same tenant by assigning a custom role that grants "Create / cancel own sessions", "Read own session history", and "Read other users' sessions (same tenant)" (**Custom roles (tenant-scoped).** and **Role assignment:** below). **Session ownership.** states the rule.
```

(b) In the **Permission matrix:** table, replace the row that begins "| Read other users' sessions (same tenant)" and ends "| Only with explicit grant |" with the following row. The row stays one physical line.

```markdown
| Read other users' sessions (same tenant)       |        Yes        |       Yes        |    Yes (read-only)       |         No         |            No            |
```

### SPEC-3 · spec/29_communication-scenarios.md § 29.3 Interactive message send

Replace step 2, which begins "2. `gateway`, `internal`. The gateway authenticates the caller and authorizes the operation against the RBAC permission matrix, under which a caller holding the `user` role reaches only their own sessions" and ends "unless a tenant-admin has granted otherwise ([§10.2](10_gateway-internals.md#102-authentication)).", with:

```markdown
2. `gateway`, `internal`. The gateway authenticates the caller and authorizes the operation against the
   RBAC permission matrix, and applies [§10.2](10_gateway-internals.md#102-authentication)
   **Session ownership.**, under which only the session's owner sends it a message and a caller that may
   not act on the session receives the response for a session that does not exist.
```

### SPEC-4 · spec/15_external-api-surface.md § 15 External API Surface

Apply after proposal 0108's SPEC-2 has landed. The target is the bold paragraph **Open Responses conversation sessions.** and the continuation paragraph that follows it, as 0108 SPEC-2 writes them. Locate each edit by the quoted words, whatever the line wrapping is after 0108 lands. This proposal does not edit 0108's files.

(a) Replace "`GET /v1/responses/{id}` returns a completed response of a session in the caller's tenant and echoes its" with:

```markdown
`GET /v1/responses/{id}` returns a completed response of a session the caller may read under [Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.** and echoes its
```

(b) Replace "An id that names no completed response of a session in the caller's tenant returns a native `404`." with:

```markdown
An id that names no completed response of a session the caller may read returns a native `404`.
```

(c) In the numbered warm conditions, replace condition "4. This request asks for the user, runtime, and environment that the request which created `S` asked for." with:

```markdown
4. This request's principal is `S`'s owner, and the request asks for the runtime and environment that the
   request which created `S` asked for.
```

### SPEC-5 · spec/09_mcp-integration.md § 9.2 Elicitation Chain

In the paragraph that begins "**`respond_to_elicitation` authorization:**", replace the sentence "If the triple does not match — because the ID is unknown, belongs to a different session, or belongs to a different user — the gateway returns a `404 ELICITATION_NOT_FOUND` error." with:

```markdown
The gateway first applies [Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.**, so a call naming a session the caller may not act on receives `404 RESOURCE_NOT_FOUND`. On a session the caller may act on, if the triple does not match — because the ID is unknown, belongs to a different session, or belongs to a different user — the gateway returns a `404 ELICITATION_NOT_FOUND` error.
```

### SPEC-6 · spec/15_external-api-surface.md § 15.1 REST API (error code catalog)

Replace the catalog row that begins "| `ELICITATION_NOT_FOUND`" with the following row. The row stays one physical line.

```markdown
| `ELICITATION_NOT_FOUND`       | `PERMANENT` | 404         | `respond_to_elicitation` or `dismiss_elicitation` on a session the caller may act on ([Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.**) rejected because the `(session_id, user_id, elicitation_id)` triple does not match any pending elicitation. The ID is unknown, belongs to a different session, or belongs to a different user. 404 is returned in all mismatch cases to avoid leaking the existence of elicitations in other sessions. A call naming a session the caller may not act on receives `RESOURCE_NOT_FOUND`. See [Section 9.2](09_mcp-integration.md#92-elicitation-chain). |
```

## Spec files touched

- spec/10_gateway-internals.md (SPEC-1, SPEC-2)
- spec/29_communication-scenarios.md (SPEC-3)
- spec/15_external-api-surface.md (SPEC-4, SPEC-6)
- spec/09_mcp-integration.md (SPEC-5)
