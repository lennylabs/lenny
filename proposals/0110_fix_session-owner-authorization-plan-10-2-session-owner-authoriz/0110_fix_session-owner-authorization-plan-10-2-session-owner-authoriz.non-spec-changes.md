# Non-spec changes — Session owner is client-assertable and user-role session reads are not owner-scoped

## Design (implementation-facing)

The code implements §10.2 **Session ownership.** (SPEC-1) at four seams, and each seam calls one predicate.

- **Owner binding.** `ownerFromPrincipal` in `pkg/gateway/sessionserver/request_identity.go` is the only source of a new session's owner on the sessionserver create paths, and the only place sessionserver writes the no-subject `401` (CODE-1, CODE-2). Every client owner input is deleted, so the compiler lists the remaining sites. The translator's in-memory binder cannot import sessionserver and applies the same rule itself (CODE-5).
- **Access predicate.** `(*Server).SessionAccessible(ctx, row, act)` in `rbac_gate.go` decides SPEC-1 rules 4 and 5 (CODE-2). The route wrappers (CODE-2), the MCP tools that read the store directly and the SSE attach authorizer (CODE-3), and the Open Responses resolve step (CODE-4) all call it.
- **Single route seam.** Every route under `/v1/sessions/{id}` that `read` or `manage` wraps today gets the owner check in the wrapper, ahead of the handler (CODE-2).
- **List.** `handleList` filters by owner unless the caller holds the read bypass (CODE-2).

Part A is CODE-1, CODE-2, CODE-3, DOCS-1, and TEST-1, and it has no dependency on proposal 0108. Part B is CODE-5, CODE-4, and TEST-2, and it lands after 0108's CODE-4 and CODE-5, because those rewrite the translator files that part B edits.

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
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: delete the legacy direct-store `create_session` fallback.
- `pkg/gateway/mcpfabric/delegation/service.go`: delete `Request.UserID` and its overrides.
- `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/gateway/mcpfabric/mcptools/generated_schemas.go`.
- `sdks/client/go/lenny/types.go`, `sdks/client/python/lenny/types.py`, `sdks/client/python/lenny/mcp.py`, `sdks/client/typescript/src/types.ts`, `sdks/client/typescript/src/mcp.ts`.
- `pkg/embedded/localcli/session.go`.

Steps:

1. Add `ownerFromPrincipal(ctx context.Context) (string, error)` in `request_identity.go`. It returns the `authmw.FromContext` principal's `Subject`. When the principal is absent or `Subject == ""`, it returns a sentinel `errNoPrincipalSubject`. Add a writer that maps the sentinel to `401 UNAUTHORIZED`. CODE-2's wrapper and `handleList` use the same helper and writer, so sessionserver writes the no-subject `401` in one place. Cite `// spec: §10.2 (Session ownership) rule 3`.
2. `handleCreate` and `handleCreateAndStart` call `ownerFromPrincipal` before any gate, store write, or claim. On error they write the `401` and return. The `401` is checked before the §12.8 fence, the concurrency limits, and the idempotency store write. The returned owner is the value passed to `requireCreationAllowed` and to `requireConcurrencyLimits`, and it is the row's `UserID`. `concUser` is deleted, so the concurrency key, the §12.8 fence subject, and the stored owner are one variable.
3. Delete the request-side `UserID` fields named in the targets. The decoders stay non-strict, so a client that still sends `userId` has it ignored. Do not add a 400 for it.
4. `derive.go`: the derived row's owner is `source.UserID`. `requireSourceOwnerNotRestricted` is unchanged.
5. `mcptools_register.go`: delete the branch of the `create_session` handler that writes the session through `deps.Store` when `SessionCreator` is nil. A nil `SessionCreator` now returns an `INTERNAL_ERROR` tool error and writes no row. Production wiring in `cmd/lenny-gateway/mcpsurface.go` already sets it.
6. `delegation/service.go`: delete `Request.UserID`, the admission-path override, and the `buildChildSession` fallback. The child's owner is `parent.UserID` on the single remaining path. Keep the admission refusal of a parent with no user, and drop its comment about `req.UserID`. Cite `// spec: §10.2 (Session ownership) rule 2; §8.2 (Child identity)`.
7. Doc comments in `service.go` and `request_identity.go`: a caller with no principal still resolves a tenant through `lookupTenant`, but a create through `CreateSessionService` or `CreateAndStartService` is refused with `401` by `ownerFromPrincipal`. A service tenant alone no longer admits a create.
8. `openapi.json`: remove `userId` from the `CreateSessionRequest` and `DeriveRequest` schemas. It stays on `Session`. Regenerate `generated_schemas.go` with the existing generator. Do not hand-edit it.
9. Client SDKs: remove the request-side owner field from the create and derive request types, and from the MCP `create_session` argument builders. The response-side `Session` owner field stays.
10. `localcli/session.go`: delete the `--user` flag and its plumbing.

### CODE-2 · One access predicate; owner check in the `read` and `manage` wrappers; owner-filtered list

Targets: `pkg/gateway/sessionserver/rbac_gate.go`, `pkg/gateway/sessionserver/route_table.go`, `pkg/gateway/sessionserver/session_read.go` (`handleList`), and `pkg/gateway/sessionserver/service.go` (the `ServiceCall` comment).

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
   2. `s.store.Get(ctx, s.resolveTenant(r), r.PathValue("id"))`.
   3. On `sessionstore.ErrNotFound`, or when `!s.SessionAccessible(ctx, row, act)`, it writes `404 RESOURCE_NOT_FOUND` with message `"session not found"`, byte-identical to `handleGet`'s miss.
   4. On any other store error it writes `500 INTERNAL_ERROR`, matching `handleGet`.
   5. Otherwise it calls `next`.

   `next` is never called on a refusal. Because the permission gate is outer, a caller whose roles lack the route's permission still receives `403 FORBIDDEN`.
5. `handleList`: call `ownerFromPrincipal` first, and write the `401` on error. Set `ListFilter.UserID = p.Subject` unless `principalHolds(ctx, p, auth.PermReadTenantSessions)`. This also applies on the platform-admin `?tenant=` path, where platform-admin passes through its role.
6. Rewrite the doc comments in `rbac_gate.go` that describe a request with no principal as admitted, and the `ServiceCall` comment in `service.go` that describes a role-less dev principal as admitted. Each states that the permission gate may admit such a request, and that the ownership step then refuses it on every `{id}` route, on list, and on create. Cite `// spec: §10.2 (Session ownership)`.

The dev-header transport (`serveDevHeaders` in `pkg/gateway/middleware/auth/auth.go`) builds a principal whose `Subject` is the raw `X-Lenny-User-ID` header and accepts an empty value. It is not changed. A harness or test that sends `X-Lenny-Tenant-ID` without `X-Lenny-User-ID` to a session route now receives `401`, and TEST-1 item 11 updates those callers.

### CODE-3 · MCP session-naming tools and the SSE attach authorizer call the same predicate

Targets: `pkg/gateway/mcpfabric/mcptools/client_tools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `pkg/gateway/mcpfabric/mcptools/elicitation.go` (`resolveElicitationTool`), and `cmd/lenny-gateway/mcpsurface.go` (the `AttachConfig.Authorize` closure).

1. The `SessionService` consumer interface in `client_tools.go` gains `SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool`. `*sessionserver.Server` already satisfies it, and `mcpsurface.go` already wires it.
2. Add `lookupCallerSession(ctx, deps, tenant, id string, act bool) (sessionstore.Session, error)` beside `errSessionLookup` in `mcptools.go`. It calls `deps.Store.Get`. When the read fails, it returns that error. When `deps.SessionService` is nil, or when `SessionAccessible` returns false, it returns `sessionstore.ErrNotFound`, so `errSessionLookup` maps a denied row to the same `RESOURCE_NOT_FOUND` as a miss. A nil `SessionService` fails closed.
3. Replace `deps.Store.Get` with `lookupCallerSession` at each handler that loads a session whose id comes from tool arguments, either directly or through the `callerSessionID` argument fallback:

   | Tool | `act` |
   |:--|:--|
   | `attach_session` snapshot | false |
   | `send_message` target | true |
   | `cancel_session` (a denied row takes the same best-effort branch as a miss) | true |
   | `await_children` | true |
   | `set_tracing_context` | true |
   | `output` | true |
   | `request_input` | true |
   | memory write | true |
   | memory query | false |
   | `respond_to_elicitation` and `dismiss_elicitation` (`resolveElicitationTool`) | true |

4. `resolveElicitationTool`: keep `requireActiveResolver` first. Replace its `deps.Store.Get` with `lookupCallerSession(..., act=true)`, then run the triple check. A missing or foreign session now answers `RESOURCE_NOT_FOUND` where the missing case answered `ELICITATION_NOT_FOUND`. An unknown elicitation id on the caller's own session still answers `ELICITATION_NOT_FOUND` (SPEC-5). Rewrite the function's doc comment to state the order.
5. `mcpsurface.go`, `AttachConfig.Authorize`: after `w.sessions.Get`, return `RESOURCE_NOT_FOUND` `"session not found"` when `!sessionSrv.SessionAccessible(ctx, row, false)`, before any SSE byte is written. A denied caller and a miss receive the same response.
6. Unchanged: lookups of a parent, a child, a sender, or an origin that the tool makes on behalf of the calling session itself. These include delegation, `get_task_tree`, `cancel_child`, the `send_message` sender lookup, and `environmentDefaultPolicy`, and they keep the plain store.

### CODE-4 · Open Responses resolve step and warm condition 4 (after 0108 CODE-4 and CODE-5)

Targets: `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go` (0108's resolve step and 0108 CODE-5's warm eligibility and reservation re-check), and `cmd/lenny-gateway/credentialsurface.go`.

1. `singleshot.go` declares the consumer interface `SessionAccess { SessionAccessible(ctx context.Context, row sessionstore.Session, act bool) bool }`. `OpenResponsesOptions` gains `SessionAccess SessionAccess`.
2. In 0108's resolve step, after the tenant-scoped `Get`, return `errContinuationNotFound` when `!opts.SessionAccess.SessionAccessible(ctx, row, false)`. That is the native `404`, the same response as an unknown id. A nil `SessionAccess` is treated as denying. `GET /v1/responses/{id}` and `previous_response_id` both pass through this step.
3. Warm condition 4, in the eligibility check and in the reservation mutator's re-check, compares the request principal's `Subject` with `row.UserID` instead of `req.User`. A request that fails the condition goes cold, and the cold create is an ordinary create that the caller owns. Cite `// spec: §15 (Open Responses conversation sessions); §10.2 (Session ownership)`.
4. `credentialsurface.go` passes the `*sessionserver.Server` as `OpenResponsesOptions.SessionAccess`. Tests wire a fake.

### CODE-5 · Translator owner binding (after 0108 CODE-5)

Targets: `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go`, `pkg/gateway/environment/translator/openai_chat.go`, and `cmd/lenny-gateway/credentialsurface.go` (`sessionSingleShotBinder`).

1. Delete `SingleShotSpec.UserID`. Both adapters drop `UserID: req.User`. The `user` request field still decodes and is ignored.
2. `noopSingleShotBinder.BindSingleShot` reads the owner from `authmw.FromContext`. When the principal is absent or its `Subject` is empty, it returns `&SingleShotError{HTTPStatus: 401, Code: "UNAUTHORIZED"}` and writes no row. Otherwise the owner is the row's `UserID`. Cite `// spec: §10.2 (Session ownership) rules 1 and 3`.
3. `sessionSingleShotBinder` in `credentialsurface.go` drops its `UserID` mapping. The production binder reaches `CreateAndStartService`, which binds the owner through CODE-1.

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
3. File the three entries listed under the summary's "Defects in the shipped tree that this proposal does not stage" as new BUILD-GAPS findings. Record the blob-route entry as the successor of closed F-15.1.32, and state there that its resolution has to choose between §15.1's `403` and the `404` that §10.2 **Session ownership.** rule 7 gives.

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

### TEST-1 · Part A (lands with or after CODE-3)

1. **Route table matrix** (tier 1, `pkg/gateway/sessionserver/session_ownership_test.go`). Rows are every pattern taken from `Handler()` that contains `{id}` and is wrapped by `readID` or `manageID`. Exclude eval and `/usage` explicitly. Assert the following outcomes:
   - Reads succeed for alice, bob/`tenant-viewer`, bob/`tenant-admin`, bob/`platform-admin`, and the custom-role bob.
   - Manage succeeds only for alice.
   - bob/`tenant-viewer` on a manage route receives the unchanged `403 FORBIDDEN`.
   - A no-principal request, or an empty `Subject` that passes the permission gate, receives `401 UNAUTHORIZED`.
   - Every other refusal receives a `404` body byte-identical to a missing id.
   - A handler spy records zero calls on every refusal, and a store error on the owner read reaches no handler and returns `500`.

   The discriminating assertion is that role-less single-tenant bob receives `404` on alice's row. It fails if the bypass reuses `sessionPermissionGranted`.
2. **Coverage guard** (tier 1, `route_owner_coverage_test.go`). Every `/v1/sessions/{id}` pattern except eval and `/usage` answers `404` to bob/`user` on alice's row, so a route mounted outside the wrappers fails the test. The tool-use, elicitation, and memory routes return `RESOURCE_NOT_FOUND` from the wrapper for a missing session and for a foreign one.
3. **List** (tier 1). A `user` sees only its own rows. `tenant-viewer` and the custom-role bob see every row in the tenant. Role-less single-tenant bob sees only its own rows. A no-subject request receives `401`.
4. **Create** (tier 1, the create, create_and_start, environment session, and `user_gate` tests). Cover REST create, start, environment sessions, and MCP `create_session`:
   - When alice sends body `userId=bob`, the stored owner is alice.
   - A no-subject request, including a dev-header request with an empty `X-Lenny-User-ID`, receives `401` and writes no row and no idempotency entry.
   - The concurrency key equals the stored owner.
   - An erasure-pending alice who names bob in the body is refused by the §12.8 fence. Add `// spec: §12.8 (Lifecycle fence and drain)`.
   - The custom-role bob can still create his own session.
   - A nil `SessionCreator` on MCP `create_session` returns an error and writes no row.
5. **Inheritance** (tier 1). Derive and replay store the source owner when the body names another user. In `pkg/gateway/mcpfabric/delegation/service_test.go` and `child_identity_test.go`, a delegated child stores the parent owner. A compile-level check confirms that `delegation.Request` has no `UserID` field. Add `// spec: §8.2 (Child identity)`.
6. **MCP tools** (tier 1, `pkg/gateway/mcpfabric/mcptools/session_ownership_test.go`). Every row of the CODE-3 step 3 table returns `RESOURCE_NOT_FOUND` across owners, both for a read row and for an act row against a reader-bypass caller. The following cases are also covered:
   - An in-pod agent acting on a same-owner child succeeds.
   - A nil `SessionService` fails closed.
   - A memory write by a principal with no `SessionID` that names bob's session writes nothing.
   - The `attach_session` SSE stream for alice's session, opened by bob, returns `RESOURCE_NOT_FOUND` before any SSE byte is written.
   - `respond_to_elicitation` on bob's session returns `RESOURCE_NOT_FOUND`, and an unknown id on alice's own session returns `ELICITATION_NOT_FOUND`. Add `// spec: §9.2 (respond_to_elicitation authorization)`.
7. **Eval stays open** (tier 1). A non-owner holding `session:eval:write` still reaches `POST /v1/sessions/{id}/eval`. Add `// spec: §10.7 (Experiment Primitives)`.
8. **Schema** (tier 3, `tests/tier3_contract/rest_sessions/openapi_document_test.go`). Assert that `CreateSessionRequest` and `DeriveRequest` carry no `userId`, and that `Session` keeps it. The existing tier-0 `api_codegen_drift` and `published_schema` tests cover the generated MCP schema.
9. **REST and MCP parity** (tier 3, `tests/tier3_contract/rest_mcp_consistency/caller_check_parity_test.go`). A non-owner receives the same `404` on the REST route and on its MCP tool.
10. **Isolation invariant** (tiers 1 and 9):
    - Tier 1: a randomized driver runs create, derive, replay, get, and list for N principals over an in-process server and a fake store. The checker asserts that every row's owner is its creating subject or its source owner, and that no response exposes a row `SessionAccessible` rejects.
    - Tier 9 (`tests/tier9_security/session_owner_isolation_test.go`): a deterministic black-box test against the real gateway. alice creates a session, and the test checks:
      - bob/`user` receives `404` on get, messages, and terminate, and does not see the session in list.
      - The custom-role bob reads the session, receives `404` on messages and terminate, and creates his own session.
      - A body `userId=bob` is ignored.
11. **Dev-header callers.** Update every harness and test that sends `X-Lenny-Tenant-ID` to a session route without `X-Lenny-User-ID` so that it sends a non-empty `X-Lenny-User-ID`. These include `tests/testinfra/lifecycleharness`, `tests/testinfra/gateway/gateway.go`, `tests/tier3_contract/multiprotocol_journey`, `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go`, and the tier-3, tier-4, tier-7a, and tier-8 files that `grep -rL X-Lenny-User-ID $(grep -rl X-Lenny-Tenant-ID tests pkg cmd --include=*.go)` lists. A caller that sets the header through a shared helper is already covered. Tier-3 and tier-4 tests that asserted `ELICITATION_NOT_FOUND` or another not-found code for a missing session on a wrapped route now assert `RESOURCE_NOT_FOUND`.
12. **Integration** (tier 4, `tests/tier4_integration/session_ownership_test.go`). On the compose stack, alice's session is invisible to bob/`user` over REST get, MCP `attach_session`, and list.

### TEST-2 · Part B (lands with or after CODE-4)

In `pkg/gateway/environment/translator/open_responses_ownership_test.go` (tier 1) and `tests/tier3_contract/rest_openai_responses` (tier 3):

1. On both adapters, `user=bob` sent by alice stores alice. A no-subject request through the noop binder returns `401` and writes no row.
2. `GET /v1/responses/{id}` and `previous_response_id` for bob's response, requested by alice/`user`, return the native `404` with no bind and no store write.
3. A `tenant-admin` continuation of another user's response goes cold and creates a session that the `tenant-admin` owns. The held pod receives no dispatch. Use `tenant-admin` rather than `tenant-viewer`, so the test does not depend on the filed create-service gate defect.
4. A nil `SessionAccess` returns the native `404`.

## Edge cases and accepted failure modes

- A store error on the wrapper's owner read returns `500 INTERNAL_ERROR` and reaches no handler (CODE-2 step 4).
- The wrapper and the handler each read the row, and nothing caches the owner between them. Neither read can observe a different owner, because the owner never changes after creation (SPEC-1 rule 1).
- Accepted: on a missing session, the tool-use and elicitation routes and the MCP elicitation tools now return `RESOURCE_NOT_FOUND` where they returned a route-specific not-found code (CODE-2 step 4, CODE-3 step 4).
- Accepted: dev-header callers that omit `X-Lenny-User-ID` receive `401` on session routes (TEST-1 item 11).

## Files touched on application (non-spec)

- pkg/gateway/sessionserver/request_identity.go, create_handler.go, create.go, create_and_start.go, derive.go, replay.go, service.go, rbac_gate.go, route_table.go, session_read.go, and their tests
- pkg/gateway/mcpfabric/mcptools/client_tools.go, mcptools.go, mcptools_register.go, elicitation.go, generated_schemas.go, and tests
- pkg/gateway/mcpfabric/delegation/service.go and service_test.go
- pkg/gateway/environment/translator/singleshot.go, open_responses.go, openai_chat.go, and tests
- cmd/lenny-gateway/mcpsurface.go, credentialsurface.go
- pkg/gateway/externalapi/openapi/openapi.json
- sdks/client/go/lenny/types.go; sdks/client/python/lenny/types.py, mcp.py; sdks/client/typescript/src/types.ts, mcp.ts
- pkg/embedded/localcli/session.go
- docs/operator-guide/security.md, docs/api/openai-completions.md, docs/client-guide/sdk-examples/typescript.md, go.md, python.md
- tests/tier3_contract (rest_sessions, rest_mcp_consistency, rest_openai_responses, and the dev-header callers), tests/tier4_integration, tests/tier9_security, tests/testinfra harnesses
- BUILD-GAPS.md, gateway-runtime-comms-remediation.md
