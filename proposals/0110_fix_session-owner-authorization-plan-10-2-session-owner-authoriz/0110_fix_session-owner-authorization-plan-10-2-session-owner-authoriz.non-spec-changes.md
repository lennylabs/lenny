# Non-spec changes — Session owner is client-assertable and user-role session reads are not owner-scoped

## Design (implementation-facing)

The code implements §10.2 **Session ownership.** (SPEC-1) at four seams, and each seam calls one predicate.

- **Owner binding.** `ownerFromPrincipal` in `pkg/gateway/sessionserver/request_identity.go` is the only source of a new session's owner on the sessionserver create paths, and the only place sessionserver writes the no-subject `401` (CODE-1, CODE-2). CODE-1 deletes every client owner input outside the translators. The translator's in-memory binder cannot import sessionserver and applies the same rule itself (CODE-5).
- **Access predicate.** `(*Server).SessionAccessible(ctx, row, act)` in `rbac_gate.go` decides SPEC-1 rules 4 and 5 (CODE-2). The route wrappers (CODE-2), the MCP tools that read the store directly and the SSE attach authorizer (CODE-3), and the Open Responses resolve step (CODE-4) all call it.
- **Single route seam.** Every route under `/v1/sessions/{id}` that `read` or `manage` wraps today gets the owner check in the wrapper, ahead of the handler (CODE-2).
- **List.** `handleList` filters by owner unless the caller holds the read bypass (CODE-2).

Part A is CODE-1, CODE-2, CODE-3, DOCS-1, and TEST-1, and it has no dependency on proposal 0108. Part B is CODE-5, CODE-4, and TEST-2, and it lands after 0108 because 0108 rewrites the translator files that part B edits (checklist S10 and S11).

## Staged code changes

### CODE-1 · Bind the owner from the principal in the sessionserver create paths; delete every client owner input outside the translators

Targets:

- `pkg/gateway/sessionserver/request_identity.go`: new `ownerFromPrincipal`, and a rewritten `lookupTenant` step-2 comment.
- `pkg/gateway/sessionserver/create_handler.go`: delete `CreateSessionRequest.UserID`.
- `pkg/gateway/sessionserver/create.go`: `requireCreationAllowed`, `requireConcurrencyLimits`, and the row's `UserID` use the owner. `concUser` is removed.
- `pkg/gateway/sessionserver/create_and_start.go`: delete `UserID string json:"userId,omitempty"`. The gates and every row construction use the owner.
- `pkg/gateway/sessionserver/derive.go`: delete `DeriveRequest.UserID` and its doc line. Set `userID := source.UserID`.
- `pkg/gateway/sessionserver/replay.go`: no behavior change. Add `// spec: §10.2 (Session ownership)` at the `UserID: source.UserID` stamp.
- `pkg/gateway/sessionserver/service.go`: rewrite the `CreateSessionService` and `CreateAndStartService` doc comments.
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: delete the legacy direct-store `create_session` fallback, and drop `userId` from the `create_session` schema comment (step 8).
- `pkg/gateway/mcpfabric/mcptools/client_tools.go`: the `lenny/create_and_start_session` `InputSchema` (step 8).
- `pkg/gateway/mcpfabric/delegation/service.go`: delete `Request.UserID` and its overrides.
- `cmd/lenny-gateway/credentialsurface.go`: `sessionSingleShotBinder` drops `UserID: spec.UserID`.
- `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/gateway/mcpfabric/mcptools/generated_schemas.go`.
- `sdks/client/go/lenny/types.go`, `sdks/client/go/lenny/mcp.go`, `sdks/client/go/lenny/mcp_test.go`, `sdks/client/python/lenny/types.py`, `sdks/client/python/lenny/mcp.py`, `sdks/client/typescript/src/types.ts`, `sdks/client/typescript/src/mcp.ts`, the MCP builder callers `sdks/client/python/tests/test_mcp.py`, `sdks/client/python/tests/mcp_probe.py`, `sdks/client/typescript/test/mcp.test.mjs`, and `sdks/client/typescript/test/mcp-probe.mjs`, the test helpers `sdks/client/go/test-helper/main.go`, `sdks/client/typescript/src/test-helper.ts`, and `sdks/client/python/test-helper`, and `tests/tier3_contract/sdks`.
- `pkg/embedded/localcli/session.go`, `pkg/ctlcli/install_smoke.go`.

Steps:

1. Add `ownerFromPrincipal(ctx context.Context) (string, error)` in `request_identity.go`. It returns the `authmw.FromContext` principal's `Subject`. When the principal is absent or `Subject == ""`, it returns a sentinel `errNoPrincipalSubject`. Add a writer that maps the sentinel to `401 UNAUTHORIZED`. CODE-2's wrapper and `handleList` use the same helper and writer, so sessionserver writes the no-subject `401` in one place. Cite `// spec: §10.2 (Session ownership) rule 3`.
2. `createSession` (the body `handleCreate` and `handleEnvironmentSessions` both call) and `handleCreateAndStart` call `ownerFromPrincipal` before any gate, store write, or claim. On error they write the `401` and return. The returned owner is the value passed to `requireCreationAllowed` and to `requireConcurrencyLimits`, and it is the row's `UserID`. `concUser` is deleted, so the concurrency key, the §12.8 fence subject, and the stored owner are one variable.
3. Delete the request-side `UserID` fields named in the targets. The decoders stay non-strict, so a client that still sends `userId` has it ignored. Do not add a 400 for it.
4. `derive.go`: the derived row's owner is `source.UserID`. `requireSourceOwnerNotRestricted` is unchanged.
5. `mcptools_register.go`: delete the branch of the `create_session` handler that writes the session through `deps.Store` when `SessionCreator` is nil. A nil `SessionCreator` now returns an `INTERNAL_ERROR` tool error and writes no row. Production wiring in `cmd/lenny-gateway/mcpsurface.go` already sets it.
6. `delegation/service.go`: delete `Request.UserID`, the admission-path override, and the `buildChildSession` fallback. The child's owner is `parent.UserID` on the single remaining path. Keep the admission refusal of a parent with no user, and drop its comment about `req.UserID`. Cite `// spec: §10.2 (Session ownership) rule 2; §8.2 (Child identity)`.
7. Doc comments in `service.go` and `request_identity.go`: a caller with no principal still resolves a tenant through `lookupTenant`, but a create through `CreateSessionService` or `CreateAndStartService` is refused with `401` by `ownerFromPrincipal`. A service tenant alone no longer admits a create.
8. `openapi.json`: remove `userId` from the `CreateSessionRequest` and `DeriveRequest` schemas. It stays on `Session`. Regenerate `generated_schemas.go` with the existing generator. Do not hand-edit it. Regeneration does not touch the hand-written `InputSchema` literal of `lenny/create_and_start_session` in `client_tools.go`, so delete its `userId` property by hand, and drop `userId` from the field list in the `create_session` schema comment in `mcptools_register.go`.
9. Client SDKs: remove the request-side owner field from the create and derive request types, from the MCP `create_session` argument builders in every SDK language (Go `MCPClient.CreateSession`, Python `create_session` and its async wrapper, and TypeScript `createSession` each lose the owner parameter), and from the SDK conformance test helpers. Update every caller of those builders, which the targets list, delete `smokeTarget.userID` in `install_smoke.go`, and drop the `user_id` command argument from the tier-3 SDK tests. The response-side `Session` owner field stays.
10. `localcli/session.go`: delete the `--user` flag and its plumbing.

### CODE-2 · One access predicate; owner check in the `read` and `manage` wrappers; owner-filtered list

Targets: `pkg/gateway/sessionserver/rbac_gate.go`, `pkg/gateway/sessionserver/route_table.go`, `pkg/gateway/sessionserver/session_read.go` (`handleList`), `pkg/gateway/sessionserver/service.go` (the `ServiceCall` comment), and `pkg/gateway/session/sessionstore/primary_read.go` (step 7).

1. `rbac_gate.go`: split `principalHolds(ctx context.Context, p authmw.Principal, perm auth.Permission) bool` out of `sessionPermissionGranted`. It resolves built-in `RolesGrant` plus the custom-role registry for the tenant in `p.TenantID`. It has no role-less fall-through and no admit for a missing principal. `sessionPermissionGranted` keeps its current behavior and calls `principalHolds` for the role resolution.
2. Add the exported predicate:

   ```go
   // SessionAccessible reports whether the principal on ctx may read
   // (act == false) or act on (act == true) row. An absent principal or
   // an empty Subject is never granted. The owner is granted both. Any
   // other principal is granted a read only when principalHolds reports
   // PermReadTenantSessions, and is never granted an action.
   // spec: §10.2 (Session ownership) rules 4 and 5
   func (s *Server) SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool
   ```

   The trap here is reusing `sessionPermissionGranted` for the read bypass. Its role-less single-tenant fall-through returns true for every permission, so a deployment whose IdP sends no role claim would let every user read every session. The bypass calls `principalHolds` only.
3. `route_table.go`: build two variants of each closure. `readID` and `manageID` compose `requireSessionPermission(perm, requireSessionAccess(act, next))`, with `act` false for `readID` and true for `manageID`. Mount them on every pattern that contains `{id}` and is wrapped by `read` or `manage` today. Keep the existing `read` and `manage` for `POST /v1/sessions`, the environment-session create routes, `POST /v1/sessions/start`, and `GET /v1/sessions`. Leave `POST /v1/sessions/{id}/eval` (`evalWrite`), `GET /v1/sessions/{id}/usage`, and `GET /v1/blobs/{ref...}` untouched. The tool-use, elicitation, and memory routes are inside the wrapper, and there is no exemption list.
4. `requireSessionAccess(act bool, next http.HandlerFunc) http.HandlerFunc` runs in this order:
   1. `ownerFromPrincipal` (CODE-1 step 1). On error it writes the `401` and returns.
   2. It reads the row for `s.resolveTenant(r)` and `r.PathValue("id")` with `s.store.Get`, under `context.WithTimeout(r.Context(), sessionstore.PrimaryReadBound(s.primaryReadTimeout))`, which is the bound `s.readPrimary` applies (step 7). That context scopes this `Get` only, and `pgstore.Store.Get` passes it to pgx, so the bound ends a hung read-pool query. When the `Get` returns an error, the wrapper re-reads the row once with `s.readPrimary` under `r.Context()`. In the table, a granted row is one for which `s.SessionAccessible(r.Context(), row, act)` returns true, and a denied row is one for which it returns false.

      | Rule | `s.store.Get` result | `s.readPrimary` result | Response |
      |:--|:--|:--|:--|
      | 1 | a granted row | not issued | call `next` |
      | 2 | a denied row | not issued | `404` |
      | 3 | `sessionstore.ErrNotFound`, any other error, or expiry of the bound | a granted row | call `next` |
      | 4 | as rule 3 | a denied row | `404` |
      | 5 | as rule 3 | `sessionstore.ErrNotFound` | `404` |
      | 6 | as rule 3 | any other error | `500` |

      `404` is the body `handleGet` writes for `ErrNotFound` (`RESOURCE_NOT_FOUND`, message `"session not found"`), and `500` is the body it writes for another store error (`INTERNAL_ERROR` with the error's text). A replica row decides alone because the owner never changes (SPEC-1 rule 1). A replica error is re-read because the read pool can lag a row the primary already holds, such as a session created moments earlier.

   `next` is never called on a refusal. Because the permission gate is outer, a caller whose roles lack the route's permission still receives `403 FORBIDDEN`.
5. `handleList`: call `ownerFromPrincipal` first, and write the `401` on error. Set `ListFilter.UserID = p.Subject` unless `principalHolds(ctx, p, auth.PermReadTenantSessions)`. This also applies on the platform-admin `?tenant=` path, where platform-admin passes through its role.
6. Rewrite the doc comments in `rbac_gate.go` that describe a request with no principal as admitted, and the `ServiceCall` comment in `service.go` that describes a role-less dev principal as admitted. Each states that the permission gate may admit such a request, and that the ownership step then refuses it on every `{id}` route, on list, and on create. Cite `// spec: §10.2 (Session ownership)`.
7. `primary_read.go`: move `ReadPrimary`'s non-positive-timeout default into an exported `PrimaryReadBound(timeout time.Duration) time.Duration`, and have `ReadPrimary` call it. Behavior does not change, and the existing `ReadPrimary` subtest "zero timeout uses the default" covers it. Cite `// spec: §12.3 (Postgres HA Requirements)`.

The dev-header transport (`serveDevHeaders` in `pkg/gateway/middleware/auth/auth.go`) builds a principal whose `Subject` is the raw `X-Lenny-User-ID` header and accepts an empty value. It is not changed.

### CODE-3 · MCP session-naming tools and the SSE attach authorizer call the same predicate

Targets: `pkg/gateway/mcpfabric/mcptools/client_tools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `pkg/gateway/mcpfabric/mcptools/elicitation.go` (`resolveElicitationTool`), and `cmd/lenny-gateway/mcpsurface.go` (the `AttachConfig.Authorize` closure).

1. The `SessionService` consumer interface in `client_tools.go` gains `SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool`. `*sessionserver.Server` already satisfies it, and `mcpsurface.go` already wires it.
2. Add `lookupCallerSession(ctx, deps, tenant, id string, act, primary bool) (sessionstore.Session, error)` beside `errSessionLookup` in `mcptools.go`, citing `// spec: §10.2 (Session ownership) rules 3 and 7`. It runs in this order:
   1. When the principal on `ctx` is absent or its `Subject` is empty, it returns an `UNAUTHORIZED` tool error and reads nothing.
   2. It reads the row with `deps.Store.Get`, or with `sessionstore.ReadPrimary` when `primary` is true, which only the step 3 table's `request_elicitation` row sets. When the read fails, it returns that error.
   3. When `deps.SessionService` is nil, or when `SessionAccessible` returns false, it returns `sessionstore.ErrNotFound`, so a denied row takes the handler's miss path. A nil `SessionService` fails closed.

   Every caller returns the `UNAUTHORIZED` tool error unchanged, ahead of its own miss mapping and ahead of `cancel_session`'s best-effort branch, because `errSessionLookup` turns any error into `RESOURCE_NOT_FOUND`.
3. At each handler that names a session from tool arguments, directly or through the `callerSessionID` argument fallback, `lookupCallerSession` replaces that session's store read, or runs where the row's note says:

   | Tool | `act` |
   |:--|:--|
   | `attach_session` snapshot | false |
   | `send_message` target | true |
   | `send_message` sender, when present (before the per-sender limit) | true |
   | `cancel_session` (a denied row takes the same best-effort branch as a miss) | true |
   | `get_task_tree` | false |
   | `cancel_child` child (the child has the parent's owner, SPEC-1 rule 2) | true |
   | `await_children` | true |
   | `set_tracing_context` | true |
   | `output` | true |
   | `request_input` | true |
   | memory write | true |
   | memory query | false |
   | `respond_to_elicitation` and `dismiss_elicitation` (`resolveElicitationTool`) | true |
   | `delegate_task` `parentSessionId` (after `requireDelegatingBearerAllowed`, before `requireActiveDelegator`) | true |
   | `discover_agents` (a denied session filters as a missing one) | false |
   | `request_elicitation` (read with `sessionstore.ReadPrimary`) | true |

4. `resolveElicitationTool`: keep `requireActiveResolver` first. Replace its `deps.Store.Get` with `lookupCallerSession(..., act=true)`, then run the triple check. A missing or foreign session now answers `RESOURCE_NOT_FOUND` where the missing case answered `ELICITATION_NOT_FOUND`. An unknown elicitation id on the caller's own session still answers `ELICITATION_NOT_FOUND` (SPEC-5). Rewrite the function's doc comment to state the order.
5. `mcpsurface.go`, `AttachConfig.Authorize`: first return `UNAUTHORIZED` when the principal on `ctx` is absent or its `Subject` is empty. Then, after `w.sessions.Get`, return `RESOURCE_NOT_FOUND` `"session not found"` when `!sessionSrv.SessionAccessible(ctx, row, false)`, before any SSE byte is written. A denied caller and a miss receive the same response.
6. Unchanged: a lookup whose id comes from a stored row, or names a session the handler has already passed through `lookupCallerSession`, keeps the plain store.

### CODE-4 · Open Responses resolve step and warm condition 4

Targets: `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go` (0108's resolve step and 0108 CODE-5's warm eligibility and reservation re-check), and `cmd/lenny-gateway/credentialsurface.go`.

1. `singleshot.go` declares the consumer interface `SessionAccess { SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool }`. `OpenResponsesOptions` gains `SessionAccess SessionAccess`.
2. In 0108's resolve step, after the tenant-scoped `Get`, return `errContinuationNotFound` when `!opts.SessionAccess.SessionAccessible(ctx, row, false)`. That is the native `404`, the same response as an unknown id. A nil `SessionAccess` is treated as denying. `GET /v1/responses/{id}` and `previous_response_id` both pass through this step.
3. Warm condition 4, in the eligibility check and in the reservation mutator's re-check, compares the request principal's `Subject` with `row.UserID` instead of `req.User`. A request that fails the condition goes cold, and the cold create is an ordinary create that the caller owns. Cite `// spec: §15 (Open Responses conversation sessions); §10.2 (Session ownership)`.
4. `credentialsurface.go` passes the `*sessionserver.Server` as `OpenResponsesOptions.SessionAccess`.

### CODE-5 · Translator owner binding

Targets: `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go`, and `pkg/gateway/environment/translator/openai_chat.go`.

1. Delete `SingleShotSpec.UserID`. Both adapters drop `UserID: req.User`. The `user` request field still decodes and is ignored.
2. `noopSingleShotBinder.BindSingleShot` reads the owner from `authmw.FromContext`. When the principal is absent or its `Subject` is empty, it returns `&SingleShotError{HTTPStatus: 401, Code: "UNAUTHORIZED"}` and writes no row. Otherwise the owner is the row's `UserID`. Cite `// spec: §10.2 (Session ownership) rules 1 and 3`.

## Staged schema, chart, and migration changes

None. CODE-1 owns the `openapi.json` edit.

## Staged docs changes

### DOCS-1 · Align docs with the owner rule

Targets: `docs/operator-guide/security.md`, `docs/api/openai-completions.md`, `docs/client-guide/sdk-examples/typescript.md`, `docs/client-guide/sdk-examples/go.md`, and `docs/client-guide/sdk-examples/python.md`.

1. `security.md`, RBAC platform roles table, `user` row Capabilities cell: replace "Create and manage own sessions; no access to other users' sessions without explicit grant" with:

   ```markdown
   Create and manage own sessions; read other users' sessions only through a custom role a tenant-admin assigns
   ```

   Do not name `tenant-viewer` as the grant. Assigning it replaces the `user` role and removes session creation.
2. `openai-completions.md`, request parameter `user` row, Description cell: replace "End-user identifier for tracking." with:

   ```markdown
   Accepted and ignored. The session owner is the authenticated caller.
   ```

3. In `typescript.md`, delete `userId: 'alice@acme.com'` from the `createSession` example. In `go.md`, delete `UserID: "alice@acme.com",` from the `CreateSession` example. In `python.md`, delete `user_id="alice@acme.com"` from the `CreateSessionRequest` example. Leave the rest of each example unchanged, and keep it compiling against the CODE-1 SDK types.

### REC-1 · Records

Targets: `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` (§10.2 entry "Session-owner authorization proposal"), and this proposal's summary list under "Defects in the shipped tree that this proposal does not stage".

1. Close F-15.1.55 when CODE-5 lands, because CODE-5 removes the last client-owner site. Close F-10.2.30 when TEST-1 lands and F-15.1.54 when TEST-2 lands, each citing this proposal.
2. Tick the plan §10.2 entry when part B lands. If the A/B split fallback is taken, rewrite the entry so it names the open part.
3. File every entry listed under the summary's "Defects in the shipped tree that this proposal does not stage" as new BUILD-GAPS findings. Record the blob-route entry as the successor of closed F-15.1.32.

## Testing

Every test carries `// spec: §10.2 (Session ownership)`, plus the section of any further rule it asserts. Every tier-2-and-higher test carries a `// diagnosis:` comment. The caller set in item 1 is the shared fixture for items 1 through 3:

- alice (owner, `user`)
- bob/`user`
- bob/`tenant-viewer`
- bob/`tenant-admin`
- bob/`platform-admin`
- bob holding a custom role with `read_tenant_sessions`, `read_own_sessions`, and `manage_own_sessions`
- role-less single-tenant bob
- a request with no principal
- a principal with an empty `Subject`, once with roles that grant the route's permission and once role-less single-tenant

### TEST-1 · Part A

1. **Route table matrix** (tier 1, `pkg/gateway/sessionserver/session_ownership_test.go`). Rows are every pattern taken from `Handler()` that contains `{id}` and is wrapped by `readID` or `manageID`. Exclude eval and `/usage` explicitly. Assert the following outcomes:
   - Reads succeed for alice, bob/`tenant-viewer`, bob/`tenant-admin`, bob/`platform-admin`, and the custom-role bob.
   - Manage succeeds only for alice.
   - bob/`tenant-viewer` on a manage route receives the unchanged `403 FORBIDDEN`.
   - A no-principal request, or an empty `Subject` that passes the permission gate, receives `401 UNAUTHORIZED`.
   - Every other refusal receives a `404` body byte-identical to a missing id.
   - A handler spy records zero calls on every refusal.
   - **Read table** (`pkg/gateway/sessionserver/session_access_read_internal_test.go`, package `sessionserver`, `TestSessionAccessWrapperReadTable_spec_10_2`). The server is built with `PrimaryReadTimeout` of 100ms over a store fake that embeds `*memstore.Store` and overrides both `Get` and `GetPrimary`, so it satisfies `sessionstore.PrimaryReader` and `s.readPrimary` reaches the fake. The fake's `Get` returns its context's error when that context has already ended. Otherwise it returns alice's row, `sessionstore.ErrNotFound`, or an error, or it blocks until its context ends and returns the context's error. Its `GetPrimary` returns its context's error when that context has ended, and otherwise returns alice's row, `sessionstore.ErrNotFound`, or an error, and it counts its calls. The CODE-2 step 3 `manage` composition wraps the spy and is mounted on the `POST /v1/sessions/{id}/start` and `POST /v1/sessions/{id}/resume` patterns. The cases are every combination of `Get` result, `GetPrimary` result, caller (alice or bob/`user`, on alice's session id), and pattern. Each case asserts the response of the CODE-2 step 4 substep 2 rule it reaches, a spy call count of 1 for "call `next`" and 0 otherwise, and a `GetPrimary` count of 0 for "not issued" and 1 otherwise. One further case builds the server with `PrimaryReadTimeout` 0, has `Get` return alice's row, and has alice call. It asserts a spy call count of 1 and a `GetPrimary` count of 0, which pins the wrapper's use of `sessionstore.PrimaryReadBound` (CODE-2 step 7). The discriminating assertions are that a blocking `Get` returns within the test's deadline, and that a blocking `Get` followed by alice's primary row reaches the spy instead of answering `500`.

   The discriminating assertion is that role-less single-tenant bob receives `404` on alice's row. It fails if the bypass reuses `sessionPermissionGranted`.
2. **Coverage guard** (tier 1, `route_owner_coverage_test.go`). Every `/v1/sessions/{id}` pattern except eval and `/usage` answers `404` to bob/`user` on alice's row, so a route mounted outside the wrappers fails the test. The tool-use, elicitation, and memory routes return `RESOURCE_NOT_FOUND` from the wrapper for a missing session and for a foreign one.
3. **List** (tier 1). A `user` sees only its own rows. `tenant-viewer`, `tenant-admin`, and the custom-role bob see every row in the tenant. bob/`platform-admin` with `?tenant=` naming a second tenant sees every row of that tenant, including rows alice and carol own there. Role-less single-tenant bob sees only its own rows. A no-subject request receives `401`.
4. **Create** (tier 1, the create, create_and_start, environment session, and `user_gate` tests). Cover REST create, start, environment sessions, and MCP `create_session`:
   - When alice sends body `userId=bob`, the stored owner is alice.
   - A no-subject request, including a dev-header request with an empty `X-Lenny-User-ID`, receives `401` and writes no row.
   - The concurrency key equals the stored owner.
   - An erasure-pending alice who names bob in the body is refused by the §12.8 fence. Add `// spec: §12.8 (Lifecycle fence and drain)`.
   - The custom-role bob can still create his own session.
   - A nil `SessionCreator` on MCP `create_session` returns an error and writes no row.
5. **Inheritance** (tier 1). Derive and replay store the source owner when the body names another user. In `pkg/gateway/mcpfabric/delegation/service_test.go` and `child_identity_test.go`, a delegated child stores the parent owner. A compile-level check confirms that `delegation.Request` has no `UserID` field. Add `// spec: §8.2 (Child identity)`.
6. **MCP tools** (tier 1, `pkg/gateway/mcpfabric/mcptools/session_ownership_test.go`, with a `*sessionserver.Server` as `SessionService`). Every row of the CODE-3 step 3 table answers bob/`user`, and on an act row the custom-role bob, exactly as it answers a missing id (SPEC-1 rule 7), and on a read row the custom-role bob receives the same result as alice. The following cases are also covered:
   - The empty-`Subject` principal receives `UNAUTHORIZED` on a read row, on an act row, and on `cancel_session` with reason `playground_client_closed`.
   - `cancel_session` with reason `playground_client_closed` from bob on alice's session returns the accepted body, and alice's row keeps its state. The row's state is the discriminating assertion, because a miss also answers accepted.
   - With `MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1}`, bob sends `send_message` once to his own session with `fromSessionId` naming alice's session, and alice's next send from that session is not `rate_limited`. alice's receipt status is the discriminating assertion, because bob's response is the same under either ordering.
   - The `delegate_task` row runs with `deps.Users` wired and alice's user row `ProcessingRestricted`, because `requireActiveDelegator` reads nothing when `deps.Users` and `deps.Tenants` are both nil.
   - An in-pod agent acting on a same-owner child succeeds.
   - A nil `SessionService` fails closed.
   - A memory write by a principal with no `SessionID` that names bob's session writes nothing.
   - `respond_to_elicitation` on bob's session returns `RESOURCE_NOT_FOUND`, and an unknown id on alice's own session returns `ELICITATION_NOT_FOUND`. Add `// spec: §9.2 (respond_to_elicitation authorization)`.
7. **Eval stays open** (tier 1). A non-owner holding `session:eval:write` still reaches `POST /v1/sessions/{id}/eval`. Add `// spec: §10.7 (Experiment Primitives)`.
8. **Schema** (tiers 1 and 3). In `tests/tier3_contract/rest_sessions/openapi_document_test.go`, assert that `CreateSessionRequest` and `DeriveRequest` carry no `userId`, and that `Session` keeps it. In `pkg/gateway/mcpfabric/mcptools`, assert that neither the `create_session` nor the `lenny/create_and_start_session` inputSchema lists `userId`.
9. **REST and MCP parity** (tier 3, `tests/tier3_contract/rest_mcp_consistency/caller_check_parity_test.go`). A non-owner receives the same `404` on the REST route and on its MCP tool.
10. **Isolation invariant** (tiers 1 and 9):
    - Tier 1: a randomized driver runs create, derive, replay, get, and list for N principals over an in-process server and a fake store. The checker asserts that every row's owner is its creating subject or its source owner, and that no response exposes a row `SessionAccessible` rejects.
    - Tier 9 (`tests/tier9_security/session_owner_isolation_test.go`): a deterministic black-box test against the real gateway. alice creates a session, and the test checks:
      - bob/`user` receives `404` on get, messages, and terminate, and does not see the session in list.
      - The custom-role bob reads the session, receives `404` on messages and terminate, and creates his own session.
      - A body `userId=bob` is ignored.
11. **Existing callers.** Each group lands with the code deliverable named in its lead.
    - With CODE-1. A test that reaches a session route needs a principal whose `Subject` is non-empty and equals the `UserID` of every row it seeds and then reads or acts on. A caller behind the auth middleware's dev-header transport sends `X-Lenny-User-ID`. `grep -rL X-Lenny-User-ID $(grep -rl X-Lenny-Tenant-ID tests pkg cmd --include=*.go)` lists the candidates, and a caller that sets the header through a shared helper is already covered. A test that mounts a sessionserver, translator, or MCP handler without the auth middleware gets no principal from any header. `grep -rlE 'DevTenant(Header|Fallback): *true' tests pkg cmd --include=*.go` lists these tests. Wrap the handler in `authmw.Wrap` with the dev-header options, as `tests/testinfra/lifecycleharness` does, or set the principal with `authmw.WithPrincipal`. `tests/tier3_contract/sdks` needs the second form, because the SDK clients send only `X-Lenny-Tenant-ID`. A test that calls `CreateSessionService` or `CreateAndStartService` directly, or through its own `translator.SingleShotBinder`, calls under `authmw.WithPrincipal` with the seeded owner as `Subject`, including the `component`-tagged tier-2 `ssSingleShotBinder` and the `load_local`-tagged tier-7a `singleshot_admission_ordering` scenario, whose `Run` also drops `UserID` from its `SingleShotSpec`. Every reference to a `UserID` field that CODE-1 step 3 or step 6 deletes, whether a literal key or a selector such as the forwarded-`userId` assertion in `pkg/gateway/mcpfabric/mcptools/create_session_service_test.go`, is removed from every Go file under `tests` and every `_test.go` file under `pkg`, `cmd`, and `sdks`, whatever the file's build tag. An untagged `go vet` does not compile a tagged file. Every test `mcptools.Deps` that calls `create_session` sets `SessionCreator`. Drop `userId` from the expected fields in `pkg/gateway/mcpfabric/mcptools/generated_schemas_test.go`. In `pkg/gateway/sessionserver/creation_fence_test.go`, `TestCreationGateMatchesFencePredicate` posts as bob, whose markers vary; `TestDeriveSourceOwnerRestrictedAfterAdmission` drops its "new owner is an active user" case; `TestDeriveAndReplaySourceOwnerRestricted` keeps one derive case and the replay case, both requested by the restricted source owner alice, and drops its "source session" message assertion, because the caller check refuses her first; and `TestSourceOwnerCheckAdmitsAndFailsClosed` drops its "restricted caller replaying an active user's session" case. Drop the `--user` argument from `lenny session new` in `tests/tier4_integration/embedded_mode_smoke_test.go` and `tests/tier9_security/embedded_gateway_loopback_test.go`.
    - With CODE-2 for a wrapped route, and with CODE-3 for an MCP elicitation tool. Tests at every tier that asserted `ELICITATION_NOT_FOUND` or another not-found code for a missing session or another user's session, including `tests/tier9_security/erasure_isolation_test.go`, now assert `RESOURCE_NOT_FOUND`. Tests that name the caller's own session keep `ELICITATION_NOT_FOUND`. A test that expects a wrapped `{id}` route to reach its handler seeds that session row in the store its server mounts, owned as the CODE-1 group above requires.
    - With CODE-3. Every `mcptools.Deps` fixture whose test calls a CODE-3 step 3 tool, including `tests/testinfra/inputwaitrig/rig.go`, wires a `SessionService` fake whose `SessionAccessible` grants the row's owner, unless it already wires a `*sessionserver.Server`. It calls under a principal and seeds rows as the CODE-1 group above requires. The ServiceCall-only `fakeService` double in `pkg/gateway/mcpfabric/mcptools/client_tools_test.go` gains a `SessionAccessible` method so it still satisfies `SessionService`.
12. **Integration** (tier 4, `tests/tier4_integration/session_ownership_test.go`). On the compose stack, alice's session is invisible to bob/`user` over REST get, MCP `attach_session`, and list. The `attach_session` SSE stream opened by bob returns `RESOURCE_NOT_FOUND` before any SSE byte is written, bob/`tenant-viewer` opens it, and a dev-header request with an empty `X-Lenny-User-ID` receives `UNAUTHORIZED` on it. These are the only tests of the CODE-3 step 5 closure, because it is in `package main`.

### TEST-2 · Part B

Items 1 through 4 are in `pkg/gateway/environment/translator/open_responses_ownership_test.go` (tier 1) and `tests/tier3_contract/rest_openai_responses` (tier 3):

1. On both adapters, `user=bob` sent by alice stores alice. A no-subject request through the noop binder returns `401` and writes no row.
2. `GET /v1/responses/{id}` and `previous_response_id` for bob's response, requested by alice/`user`, return the native `404` with no bind and no store write.
3. A `tenant-admin` continuation of another user's response goes cold and creates a session that the `tenant-admin` owns. The held pod receives no dispatch. Use `tenant-admin` rather than `tenant-viewer`, so the test does not depend on the filed permission-matrix defect.
4. A nil `SessionAccess` returns the native `404`.
5. **0108's condition-4 row** (tier 1, `pkg/gateway/environment/translator/open_responses_test.go`). In 0108's warm-conditions table, replace the "different user" break with item 3's `tenant-admin` caller. Add a row in which the owner sends a `user` different from the creating request's and goes warm. The discriminating assertion is bind count 0.
6. **Isolation** (tier 9, `tests/tier9_security/session_owner_isolation_test.go`). bob/`user` receives the native `404` with no bind on `GET /v1/responses/{id}` for alice's held response, and on a `previous_response_id` continuation of it sent with `user=alice`. A `tenant-admin` continuation of that response goes cold, and alice's held pod receives no dispatch. This item replaces 0108's tier-9 case "A warm continuation sent with a different `user` goes cold (condition 4)."
7. **Existing callers.** Each group lands with the code deliverable named in its lead.
   - With CODE-5 (tiers 1 and 3). Tests that call the noop binder directly or reach it as a handler's default binder, including `pkg/gateway/environment/translator/singleshot_test.go`, drop `SingleShotSpec.UserID`, call under a principal whose `Subject` is non-empty, and assert that `Subject` as the row owner.
   - With CODE-4. Every `OpenResponsesOptions` fixture outside items 1 through 6 whose test resolves a response id, including 0108's tier-2 `openai_singleshot_lifecycle_test.go`, its tier-7a randomized checker, and its `tests/testinfra/lifecycleharness` handler, sets `SessionAccess` to the test's `*sessionserver.Server` where it has one and otherwise to a fake that grants the row's owner, and calls under a principal whose `Subject` is that owner.

## Edge cases and accepted failure modes

- The wrapper and the handler each read the row, and nothing caches the owner between them. Neither read can observe a different owner, because the owner never changes after creation (SPEC-1 rule 1).
- Accepted: on a missing session, the tool-use and elicitation routes and the MCP elicitation tools now return `RESOURCE_NOT_FOUND` where they returned a route-specific not-found code (CODE-2 step 4, CODE-3 step 4).
- Accepted: dev-header callers that omit `X-Lenny-User-ID` receive `401` on session routes (TEST-1 item 11).
- Accepted: while the read pool hangs, every wrapped request answers up to one primary-read bound later, because its replica read runs to that bound before the primary re-read (CODE-2 step 4 substep 2).

## Files touched on application (non-spec)

- pkg/gateway/sessionserver/request_identity.go, create_handler.go, create.go, create_and_start.go, derive.go, replay.go, service.go, rbac_gate.go, route_table.go, session_read.go, and their tests
- pkg/gateway/session/sessionstore/primary_read.go
- pkg/gateway/mcpfabric/mcptools/client_tools.go, mcptools.go, mcptools_register.go, elicitation.go, generated_schemas.go, and tests
- pkg/gateway/mcpfabric/delegation/service.go and service_test.go
- pkg/gateway/environment/translator/singleshot.go, open_responses.go, openai_chat.go, and tests
- cmd/lenny-gateway/mcpsurface.go, credentialsurface.go
- pkg/gateway/externalapi/openapi/openapi.json
- sdks/client/go/lenny/types.go, mcp.go, mcp_test.go; sdks/client/go/test-helper/main.go; sdks/client/python/lenny/types.py, mcp.py; sdks/client/python/test-helper; sdks/client/typescript/src/types.ts, mcp.ts, test-helper.ts; sdks/client/python/tests/test_mcp.py, mcp_probe.py; sdks/client/typescript/test/mcp.test.mjs, mcp-probe.mjs
- pkg/embedded/localcli/session.go, pkg/ctlcli/install_smoke.go
- docs/operator-guide/security.md, docs/api/openai-completions.md, docs/client-guide/sdk-examples/typescript.md, go.md, python.md
- tests/tier2_component, tests/tier3_contract, tests/tier4_integration, tests/tier7a_load_local, tests/tier8_chaos, tests/tier9_security, tests/testinfra, and the cmd/lenny-gateway tests that TEST-1 item 11 selects
- BUILD-GAPS.md, gateway-runtime-comms-remediation.md
