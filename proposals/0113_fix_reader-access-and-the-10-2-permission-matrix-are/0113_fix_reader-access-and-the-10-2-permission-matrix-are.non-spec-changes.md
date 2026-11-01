# Non-spec changes — Enforce the §10.2 permission matrix on every session surface and bound reader reach and stream access

## Design (implementation-facing)

Every surface asks one predicate, `Server.SessionPermitted` (CODE-1), and then applies the existing `SessionAccessible`. REST routes keep `requireSessionPermission`, MCP direct-store tools reach the predicate through `lookupCallerSession` (CODE-4), the create services reach it through the service mux (CODE-3), and the built-in adapter handlers reach it through the `SessionAccess` seam (CODE-5).

Every open stream uses one evaluation, `Server.AuthorizeSessionRead` (CODE-7), on a ticker case in its existing `select` (CODE-8). The middleware and that evaluation share one role-precedence helper, `authmw.RefreshRoles` (CODE-7).

The refusal order on every surface is matrix (`403`), subject (`401`), row read, then ownership (`404`), as §10.2 rules 3 and 7 state.

## Staged code changes

### CODE-1 · One context-based matrix predicate, in-pod admission and tool set, and removal of the tenancy branch

Targets: `pkg/gateway/sessionserver/rbac_gate.go`, `pkg/gateway/middleware/auth/auth.go` (`Principal`), `pkg/gateway/gatewaycontrol/platformtools/platformtools.go` (`WithSessionPrincipal`, `Bridge`), `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`registerVCSTokenTool`), `pkg/apis/lenny/v1alpha1/runtime_types.go` (`Env` doc comment), `pkg/gateway/sessionserver/sessionserver.go` (`multiTenant`, `Options.MultiTenant`), `cmd/lenny-gateway/sessionsrv.go` (the `multiTenant` local and the `MultiTenant:` option), and comments in `pkg/gateway/sessionserver/route_table.go` and `service.go`.

1. Add to `authmw.Principal`:

   ```go
   // InPod marks the in-pod session principal of §10.2 (Session ownership)
   // rule 8. It is in-process only and no credential sets it.
   // WithSessionPrincipal is the only site that sets it.
   InPod bool
   ```

2. `WithSessionPrincipal` sets `InPod: true`. No other production site sets it.

3. Change `sessionPermissionGranted` to take a context, and delete the `len(p.Roles) == 0` branch:

   ```go
   // spec: §10.2 (Session ownership) rules 3, 4, 7, and 8.
   func (s *Server) sessionPermissionGranted(ctx context.Context, perm auth.Permission) bool {
       p, ok := authmw.FromContext(ctx)
       if !ok {
           return true // rule 3 refuses this request with 401 after the matrix.
       }
       if p.InPod {
           return perm == auth.PermManageOwnSessions || perm == auth.PermReadOwnSessions
       }
       return s.principalHolds(ctx, p, perm)
   }

   // SessionPermitted reports whether the matrix admits the principal on ctx
   // to a read (act == false) or to a session create or an act (act == true).
   func (s *Server) SessionPermitted(ctx context.Context, act bool) bool {
       if act {
           return s.sessionPermissionGranted(ctx, auth.PermManageOwnSessions)
       }
       return s.sessionPermissionGranted(ctx, auth.PermReadOwnSessions)
   }
   ```

   `requireSessionPermission` calls `s.sessionPermissionGranted(r.Context(), perm)`. The read bypass in `SessionAccessible` keeps calling `principalHolds`, so `InPod` never reaches it.

4. Delete `Server.multiTenant`, `Options.MultiTenant`, the `New` assignment, and the `cmd/lenny-gateway/sessionsrv.go` local and option. The `--multi-tenant` flag stays in use elsewhere.

5. Rewrite the `rbac_gate.go` header comment, the `sessionPermissionGranted` and `SessionAccessible` doc comments, and the `Handler` comment in `route_table.go`, each of which states the role-less admission.

6. Remove `MultiTenant` from the test constructors in `pkg/gateway/sessionserver/rbac_gate_test.go`, `session_access_test.go`, `session_owner_binding_test.go`, and `session_ownership_test.go`. The two tier 9 `MultiTenant` sites are `authmw.Options` on `opsserver` and stay.

7. In `platformtools.go`, add the unexported set `podTools`, holding the tool names the `CH-MCP-PLATFORM` card names (SPEC-8). `ListPlatformTools` skips a catalog entry outside the set. `CallPlatformTool` returns `leasecontrol.ErrPlatformToolNotFound` for a name outside the set, ahead of `WithSessionPrincipal`. Rewrite the package doc and the `ListPlatformTools` doc there, the `ListPlatformTools` doc in `pkg/gateway/mcpfabric/delegationtree/leasecontrol/platformtools.go`, the `ListPlatformTools` doc in `pkg/adapter/gatewaycontrol/platformtools.go`, and the `Catalog` doc in `pkg/gateway/mcpfabric/mcp/mcp.go`, each of which says a pod sees the gateway-edge catalog. Change `lenny/x` in `TestWithSessionPrincipalIsThePrincipalCallPlatformToolInstalls_spec_29_3` to a tool in the set.

8. In `registerVCSTokenTool`, replace the `callerSessionID` check with the in-pod test, under the decision in D2. The refusal code and message stay. Rewrite the comment above the check. Add `InPod: true` to every `vcs_token_test.go` principal that carries a `SessionID`, including the one in `TestVCSTokenRequiresHost_spec_26_2_119`, whose expected code the new check also returns.

   ```go
   // spec: §9.1 (Platform MCP Server Tools); §10.2 (Session ownership) rule 8.
   p, ok := authmw.FromContext(ctx)
   if !ok || !p.InPod {
       return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
           "lenny/vcs_token requires an in-pod session principal", nil)
   }
   sessionID := p.SessionID
   ```

9. In the `Env` doc comment of `pkg/apis/lenny/v1alpha1/runtime_types.go`, replace "secrets reach the runtime through its credential file" with "LLM credentials reach the runtime through its credential file", as SPEC-8 Edit (n) does for §5.1. Run `make generate` to rewrite the two generated CRD files.

`setForwardIdentity` is unchanged, because a bearer or dev principal always records a credential. The forward listener rebuilds the principal through `WithSessionPrincipal`, so `InPod` survives the hop.

### CODE-2 · Dev identity paths carry `user`

Targets: `pkg/gateway/middleware/auth/auth.go` (`serveDevHeaders`, the doc comments on `Principal.Roles` and `Options.AllowDevRoles`), `pkg/gateway/mcpfabric/playground/token.go` (`mintDev`), `tests/testinfra/ownerprincipal/ownerprincipal.go` (`Context`), and the fixtures below.

1. In `serveDevHeaders`, key the default on the header being absent. A request naming only invalid roles stays role-less and is refused, which matches `parseRolesHeader`'s comment.

   ```go
   if m.opts.AllowDevRoles {
       raw := r.Header.Get("X-Lenny-Roles")
       p.Roles = parseRolesHeader(raw)
       if raw == "" {
           p.Roles = []auth.Role{auth.RoleUser}
       }
       p.Groups = parseGroupsHeader(r.Header.Get("X-Lenny-Groups"))
   }
   ```

2. In `mintDev`, add `Roles: []auth.Role{auth.RoleUser}` to the subject claims. `completeMint` copies them into the minted token.

3. `ownerprincipal.Context` gains `Roles: []auth.Role{auth.RoleUser}`. `SessionAccess` gains `SessionPermitted` under CODE-4.

4. The following fixtures land in this step, so the tree stays green:
   - Add `Roles: []auth.Role{auth.RoleUser}` to `ownerCtx` in `pkg/gateway/sessionserver/owner_principal_test.go` and `owner_principal_internal_test.go`.
   - Add `AllowDevRoles: true` to fixtures that run session routes with `AllowDevHeaders` alone: `tests/tier9_security/session_owner_binding_test.go`, and `pkg/gateway/sessionserver/session_owner_binding_test.go`, `holder_forward_test.go`, `holder_forward_listener_test.go`, and `holder_forward_sites_test.go`. Also check `pkg/gateway/mcpfabric/mcptools/holder_forward_sites_test.go` and `cmd/lenny-gateway/holder_forward_wiring_test.go`.
   - Sweep the hand-built Subject-only `authmw.Principal` literals in tests that reach a matrix check, including `pkg/gateway/sessionserver/create_test.go`, `runtimes_test.go`, and the `mcptools` tests. The role-less agent fixtures in `holder_forward_test.go` and `holder_forward_listener_test.go` gain a role if they reach a matrix check.
   - Change `tests/tier9_security/mcp_management_authz_sweep_test.go` so its `noRole` caller stays role-less by sending an invalid `X-Lenny-Roles` value, and update its comment.

### CODE-3 · Create services dispatch through the service mux

Targets: `pkg/gateway/sessionserver/service.go` (`CreateSessionService`, `CreateAndStartService`) and `pkg/gateway/sessionserver/request_identity.go` (`guardTenant`).

1. Replace `s.guardTenant(s.handleCreate)(rec, httpReq)` and `s.guardTenant(s.handleCreateAndStart)(rec, httpReq)` with `s.serviceMux().ServeHTTP(rec, httpReq)`. This runs the create through the same route and gates as REST, as the `serviceMux` comment describes for §15.2.1 rule 1.

2. Delete `guardTenant`.

3. Land with or after CODE-1 and CODE-2. The outcomes after dispatch through the mux are:
   - No tenant stays `401 UNAUTHORIZED` with `reason: auth_required`.
   - A tenant with no principal stays `401`.
   - An empty-subject principal the matrix admits stays `401`.
   - An empty-subject principal the matrix refuses, including a role-less one, receives `403 FORBIDDEN` under rule 3 and matches REST.

4. Rewrite both service doc comments, which state that an empty Subject is always refused with `401`.

5. Update the tests:
   - Give the `/principal` case of `TestCreateServicesRequirePrincipalSubject_spec_10_2` the `user` role.
   - Give the principal in `TestCreateSessionServiceRefusesImpersonationBearer_spec_10_2` the `user` role, so the `403` still proves the impersonation check.
   - Split the `empty subject` case of `TestCreateWithoutPrincipalSubjectIsUnauthorized_spec_10_2` into admitted (`401`) and refused (`403`) cases.

### CODE-4 · Matrix step in `lookupCallerSession` and its refusal mapping

Targets: `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`lookupCallerSession`, `errCallerSessionLookup`, `isUnauthorized`, `discoverySessionID`), `client_tools.go` (`SessionService`), `elicitation.go` (`resolveElicitationTool`), `mcptools_register.go` (the `send_message`, `cancel_session`, and `cancel_child` pass-through sites), and the test doubles `tests/testinfra/ownerprincipal/ownerprincipal.go` (`SessionAccess`) and `pkg/gateway/mcpfabric/mcptools/client_tools_test.go` (`fakeService`).

CODE-4 lands in one step with CODE-1 and CODE-2. The attach half of this deliverable is CODE-7's `AuthorizeSessionRead`.

1. `SessionService` gains `SessionPermitted(ctx context.Context, act bool) bool`.

2. `lookupCallerSession` runs the matrix ahead of the subject test. A nil `SessionService` keeps today's outcome.

   ```go
   if deps.SessionService != nil && !deps.SessionService.SessionPermitted(ctx, act) {
       return sessionstore.Session{}, errCallerForbidden()
   }
   if p, ok := authmw.FromContext(ctx); !ok || p.Subject == "" {
       return sessionstore.Session{}, errCallerUnauthorized()
   }
   ```

   `errCallerForbidden` returns `mcp.NewToolError("FORBIDDEN", "session access requires the matrix permission for this operation", nil)`.

3. Rename `isUnauthorized` to `isCallerRefusal`, reporting `UNAUTHORIZED` or `FORBIDDEN`, and use it at `errCallerSessionLookup`, `resolveElicitationTool`, and the three `mcptools_register.go` sites. `FORBIDDEN` otherwise becomes `RESOURCE_NOT_FOUND`, `SCOPE_DENIED`, or `INTERNAL_ERROR` there.

4. In `resolveElicitationTool`, call the matrix check ahead of `requireActiveResolver`, so an invalidated, matrix-refused caller receives `FORBIDDEN` as on REST. Call `deps.SessionService.SessionPermitted(ctx, true)` directly, with the same nil guard and `errCallerForbidden`.

5. The test doubles implement `SessionPermitted` from the roles on the principal rather than returning true, so tier 1 `mcptools` tests observe the refusal.

6. Update in this edit the comments the change makes false: the `lookupCallerSession` header, `errCallerSessionLookup`, `isCallerRefusal`, the `discoverySessionID` doc comment (which now also returns `FORBIDDEN` through its default arm), the `cancel_session` best-effort comment, and the `resolveElicitationTool` step list.

### CODE-5 · Built-in adapters: matrix first, owner-only continuation, and read check on `GET`

Targets: `pkg/gateway/environment/translator/singleshot.go` (`SessionAccess`), `continuity.go`, `open_responses.go` (`handleCreate`, `handleGet`, `warmEligible` comment, `SessionAccess` field doc), `openai_chat.go` (`OpenAIChatOptions`, `NewOpenAIChatHandler`, `handleCreateCompletion`), `continuity_test.go`, `open_responses_ownership_test.go`, `tests/tier3_contract/rest_openai_responses/ownership_contract_test.go`, and `tests/tier9_security/open_responses_warm_user_test.go`. `cmd/lenny-gateway/credentialsurface.go` passes `SessionAccess: sessionSrv` to the chat handler. Its Open Responses option needs no change, because `*Server` gains the method through CODE-1.

1. `SessionAccess` gains `SessionPermitted(ctx context.Context, act bool) bool`. Rewrite its doc and the `OpenResponsesOptions.SessionAccess` field doc, which describe reads only.

2. Add to `continuity.go`:

   ```go
   // permitted is false on a nil access, so a handler wired without one
   // answers 403 to every POST and GET, including a first turn.
   func permitted(ctx context.Context, access SessionAccess, act bool) bool {
       return access != nil && access.SessionPermitted(ctx, act)
   }

   // owned runs after resolve, which has already refused a nil access.
   func (c *continuity) owned(ctx context.Context, row sessionstore.Session) bool {
       return c.access.SessionAccessible(ctx, row, true)
   }
   ```

3. The `handleCreate` order, stated also in its header comment, is: executor check, matrix, subject, `parseTurn`, resolve, owner test, forward, reserve, dispatch.

   ```go
   if !permitted(r.Context(), h.cont.access, true) {
       writeSingleShotError(w, errMatrixRefused())
       return
   }
   ```

   After `resolve`, `if !h.cont.owned(r.Context(), row) { writeResolveError(w, errContinuationNotFound); return }`. `errMatrixRefused` returns `&SingleShotError{HTTPStatus: http.StatusForbidden, Code: "FORBIDDEN", Message: ...}`, the same native rendering a binder refusal receives.

4. The `handleGet` order is: `permitted(ctx, h.cont.access, false)` (`403`), `requireSubject` (`401`), `resolveTenant`, `resolve`. Update its comment.

5. `OpenAIChatOptions` gains `SessionAccess SessionAccess`, and `NewOpenAIChatHandler` stores it on the handler. `handleCreateCompletion` runs step 3's check on that field after the executor check and ahead of `requireSubject`.

6. In the `warmEligible` comment, delete only "a principal that may read another user's session continues it cold, on a session it owns". Keep "The adapter's `user` request field plays no part".

7. Update tests that pin the old behavior:
   - Rewrite `TestOpenResponsesNilSessionAccessIsNative404_spec_10_2` under TEST-1's tier 1 table, and rewrite the `newOwnershipServer(t, false)` test in `ownership_contract_test.go` to seed the session row directly and expect `403`.
   - Invert `TestOpenResponsesTenantAdminContinuationGoesCold_spec_10_2` in `open_responses_ownership_test.go` and in `ownership_contract_test.go`, the double in the latter file, and the reader subtest of `open_responses_warm_user_test.go`.
   - Check any existing test that pins `404` for an empty-subject or no-principal `GET`.
   - Give a role to the role-less principals that reach the real server through `SessionPermitted`: `doAs` in `tests/tier9_security/open_responses_isolation_erasure_test.go`, `tests/tier7a_load_local/open_responses_conversation_invariant_test.go`, and `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go` where that fixture reaches the handler.
   - Check the tier 7a auditors in `open_responses_conversation_invariant_test.go` for reader continuation.
   - Pass a `SessionAccess` double to every `NewOpenAIChatHandler` fixture that reaches the check, in `openai_chat_test.go`, `release_marker_test.go`, `resolve_tenant_internal_test.go`, `open_responses_ownership_test.go`, `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go`, `openai_singleshot_disconnect_test.go`, `tests/tier3_contract/rest_openai_chat/fidelity_test.go`, `multiprotocol_journey/journey_test.go`, and `rest_openai_responses/ownership_contract_test.go`.

### CODE-6 · Owner-only memory recall

CODE-6 lands with or after CODE-4, because `SessionAccessible`'s act branch stays a pure owner test and a matrix-checked act lookup would refuse a read-only owner.

Targets: `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`lenny/memory_query`), `pkg/gateway/sessionserver/route_table.go`, `export_test.go`, `session_ownership_test.go`, `route_owner_coverage_test.go`, and `pkg/gateway/mcpfabric/mcptools/session_ownership_test.go`.

1. In `lenny/memory_query`, after the `act=false` lookup:

   ```go
   // spec: §10.2 (Session ownership) rule 4.
   if !deps.SessionService.SessionAccessible(ctx, row, true) {
       return mcp.ToolResult{}, errCallerSessionLookup(sessionstore.ErrNotFound)
   }
   ```

   Update the §10.2 comment above the lookup.

2. Add a third wrapper in `route_table.go`, and mount `GET /v1/sessions/{id}/memory` through it. Update the comment that says "one of these two ownership wrappers".

   ```go
   // ownerReadSessionRoute wraps an owner-only read: the read_own_sessions
   // permission gate, then the owner test.
   // spec: §10.2 (Session ownership) rules 4, 6, and 7.
   func (s *Server) ownerReadSessionRoute(next http.HandlerFunc) http.HandlerFunc {
       return s.requireSessionPermission(auth.PermReadOwnSessions, s.requireSessionAccess(true, next))
   }
   ```

3. `export_test.go`: `OwnedRouteSpy` gains `case "ownerReadID": wrap = s.ownerReadSessionRoute`, and its comment is updated.

4. `session_ownership_test.go` (sessionserver):
   - `ownedSessionRoutes` accepts `ownerReadID`, so GET memory stays in `TestSessionRouteOwnershipMatrix_spec_10_2` and `TestRouteSpecificLookupsAnswerAfterOwnership_spec_10_2`.
   - `ownershipMatrixCallers` gains an `ownerRead` outcome column, selected for that wrapper. It holds alice with `user` granted, every non-owner holding read (`tenant-viewer`, `tenant-admin`, `platform-admin`, and custom `session-reader`) as the missing-session `404`, and the refused and no-subject rows unchanged.
   - A new caller row, alice as `tenant-viewer` (a demoted owner holding read only), asserts `ownerRead` granted and act forbidden. This row is the one that discriminates the read permission from `manageID`.

5. `route_owner_coverage_test.go` accepts `ownerReadID`.

6. `pkg/gateway/mcpfabric/mcptools/session_ownership_test.go`: remove `lenny/memory_query` from `readToolNames`, so the reader case asserts the same refusal as a missing session through `sameRefusal`, and add an admit case for an owner holding read only. Use `serverSessionService`, because the `ownerprincipal.SessionAccess` and `fakeService` doubles ignore `act`.

7. Leave the `SESSION_NOT_USER_SCOPED` branch in `handleMemoryQuery` in place. It becomes unreachable, as its write and delete counterparts already are behind `manageID`.

### CODE-7 · Shared role refresh and one read evaluation

Targets: `pkg/gateway/middleware/auth/auth.go` (`Principal.ClaimRoles`, `RefreshRoles`, `serveBearer`), `pkg/gateway/sessionserver/rbac_gate.go` (`AuthorizeSessionRead`, sentinels, `StreamAccessInterval`), `pkg/gateway/sessionserver/sessionserver.go` (`Options.StreamAccessInterval`), and `cmd/lenny-gateway/mcpsurface.go` (the `Authorize` closure).

1. Add to `authmw.Principal`:

   ```go
   // ClaimRoles is the roles claim of the verified bearer, in-process only.
   // The bearer path always sets it non-nil, even for an empty claim. A nil
   // value marks a principal the middleware never maps (service, dev, and
   // in-pod), and RefreshRoles leaves such a principal unchanged.
   ClaimRoles []auth.Role
   ```

   In the bearer path set `ClaimRoles: append([]auth.Role{}, claims.Roles...)`. The non-nil empty slice is required.

2. Add the helper, and replace the bearer path's inline block with a call under the existing `m.opts.PlatformRoles != nil` guard, keeping its `500` envelope:

   ```go
   // RefreshRoles returns p with its roles resolved under §10.2 (Role
   // assignment): a platform-managed assignment replaces the claim, and no
   // assignment restores the claim. A resolver error returns p and the error.
   func RefreshRoles(ctx context.Context, r PlatformRoleResolver, p Principal) (Principal, error) {
       if p.ClaimRoles == nil || p.Subject == "" {
           return p, nil
       }
       roles, found, err := r.ResolveRoles(ctx, p.TenantID, p.Subject)
       if err != nil {
           return p, err
       }
       if found {
           p.Roles = append([]auth.Role(nil), roles...)
       } else {
           p.Roles = append([]auth.Role(nil), p.ClaimRoles...)
       }
       return p, nil
   }
   ```

3. Add `StreamAccessInterval = 60 * time.Second`, `ErrSessionForbidden`, and `ErrSessionUnauthenticated` in `rbac_gate.go`. `Options.StreamAccessInterval` is a test seam only: `cmd/lenny-gateway` does not set it, no flag reads it, and `New` applies `if s.streamAccessInterval <= 0 { s.streamAccessInterval = StreamAccessInterval }`, following the `streamFailureReportTimeout` pattern.

4. Add the one evaluation. The refreshed principal governs both the matrix and the read bypass, and the frozen principal stays the base of every tick, so each tick restores from its `ClaimRoles`. `Options.Users` is already wired, and a nil `Store` resolves no assignment.

   ```go
   // spec: §10.2 (Session ownership) rules 3, 4, 7, and 9.
   func (s *Server) AuthorizeSessionRead(ctx context.Context, tenantID, id string) error {
       p, ok := authmw.FromContext(ctx)
       if ok {
           bounded, cancel := context.WithTimeout(ctx, sessionstore.PrimaryReadBound(s.primaryReadTimeout))
           refreshed, err := authmw.RefreshRoles(bounded, userstore.PlatformRoles{Store: s.users}, p)
           if err != nil {
               cancel()
               return err
           }
           ctx = authmw.WithPrincipal(ctx, refreshed)
           permitted := s.SessionPermitted(authmw.WithPrincipal(bounded, refreshed), false)
           cancel()
           if !permitted {
               return ErrSessionForbidden
           }
       }
       if !ok || p.Subject == "" {
           return ErrSessionUnauthenticated
       }
       row, err := s.accessRow(ctx, tenantID, id) // applies its own bounds.
       if err != nil {
           return err
       }
       if !s.SessionAccessible(ctx, row, false) {
           return sessionstore.ErrNotFound
       }
       return nil
   }
   ```

5. The `Authorize` closure keeps only its tenant check ahead of `sessionSrv.AuthorizeSessionRead`, and its own subject check is deleted. It maps `ErrSessionForbidden` to `FORBIDDEN`, `ErrSessionUnauthenticated` to `UNAUTHORIZED` with `details.reason` `principal_subject_required`, `sessionstore.ErrNotFound` to `RESOURCE_NOT_FOUND`, and any other error to `INTERNAL_ERROR`.

### CODE-8 · Ticker case in the stream loops

Targets: `pkg/gateway/sessionserver/events.go`, `logs.go` (`streamLogsSSE`), `pkg/gateway/mcpfabric/mcp/stream.go` (`AttachConfig`, `handleAttachStream`), `websocket_attach.go` (`startWSAttach` goroutine), and `cmd/lenny-gateway/mcpsurface.go` (`SetAttach`).

1. `AttachConfig` gains `AccessInterval time.Duration`. A non-positive value selects `defaultAttachAccessInterval = 60 * time.Second`, matching `startRevocationWatch`. The ticker channel is nil only when `Authorize` is nil. `SetAttach` passes `sessionserver.StreamAccessInterval`, and a tier 1 test asserts that the two constants agree.

2. Create each ticker ahead of backlog replay, so replay time counts toward the interval. `defer` its `Stop`.

3. Sessionserver SSE loops, with `s.streamAccessInterval` from CODE-7:

   ```go
   case <-access.C:
       if err := s.AuthorizeSessionRead(ctx, tenantID, id); err != nil {
           return
       }
       if len(sub.Events()) == cap(sub.Events()) {
           return // §10.2 rule 9: events may be lost; reconnect with the cursor.
       }
   ```

4. `handleAttachStream` has the same case, calling `s.attach.Authorize` with the request context.

5. WebSocket, inside the `startWSAttach` goroutine. A cancelled attach ends only its own goroutine, and the `ctx.Err()` test is the branch that preserves `attachCancel`'s re-attach contract:

   ```go
   case <-accessC:
       err := s.attach.Authorize(ctx, tenant, in.SessionID)
       if ctx.Err() != nil {
           return
       }
       if err != nil {
           conn.Close(websocket.StatusPolicyViolation, "session_access_lost")
           return
       }
       if len(sub.Events()) == cap(sub.Events()) {
           conn.Close(websocket.StatusTryAgainLater, "")
           return
       }
   ```

6. Update the `AttachConfig.Authorize` doc comment, which says it gates only the open.

### CODE-9 · `params.seq` on every attach frame

Target: `pkg/gateway/mcpfabric/mcp/projection.go`.

1. Add `"seq": ev.Seq` to the params of `projectStatusUpdate`, `projectOutput`, `projectElicitationCreate`, both `projectToolUse` branches, and `projectError`. `marshalGenericSessionEvent` already carries it.

2. In `projectError`, set it after the payload copy, so a payload key named `seq` cannot replace it.

3. Update the `projectMCPSessionEvent` doc comment, which credits the cursor to the SSE `id:` line alone, and the `projectStatusUpdate` and `projectOutput` doc comments, which state the rows SPEC-9 Edit (c) replaces.

## Staged schema, chart, and migration changes

CODE-1 step 9 regenerates `charts/lenny/crds/lenny.dev_runtimes.yaml` and `pkg/embedded/crds/lenny.dev_runtimes.yaml`, which changes one field description and no schema. The interval is fixed in the specification, so no chart value or flag is added.

## Staged docs changes

### DOCS-1 · Client, operator, API, and tutorial pages

This deliverable edits the pages below. Pages state behavior with no section numbers.

- `docs/client-guide/authentication.md` (Roles and Permissions) and `docs/operator-guide/security.md` (Platform Roles): a principal with no role is refused on session operations in every deployment. A deployer whose IdP sends no role claim assigns roles through a claim or the platform-managed mapping. Bearers from `POST /v1/oauth/token` carry no roles claim, so such a bearer needs a platform-managed mapping. A role carried only by the OIDC claim changes with a new token.
- `docs/client-guide/streaming.md` (Reconnection Handling) is the single statement that a stream closes when access is lost or cannot be evaluated, or when events may have been dropped during the evaluation, which a WebSocket attachment reports with close code `1013`. The client reconnects with its cursor. On a WebSocket attachment the cursor is the `seq` parameter of the last frame received, passed as `resumeFromSeq` on `attach_session`. A reconnect after access is lost receives the refusal, and any other reconnect resumes.
- `docs/api/rest.md` (`GET /v1/sessions/{id}/logs`) and `docs/api/mcp.md` (`attach_session`) link to that statement. `docs/api/mcp.md` also adds the optional `resumeFromSeq` argument to `attach_session`.
- `docs/api/rest.md`: add a `GET /v1/sessions/{id}/memory` entry, owner only.
- `docs/api/mcp.md`: add `FORBIDDEN` (the caller lacks the matrix permission) to the error-code table of `lenny/create_session`, `attach_session`, and each session-naming tool.
- `docs/api/open-responses.md` (POST /v1/responses, Multi-turn via `previous_response_id`, the "returns completed responses" sentence, and the error table): add the `403`, and state that continuation is owner-only (a non-owner receives `404`, even when it can read the response).
- `docs/api/openai-completions.md`: add the adapter `403`.
- `docs/tutorials/agent-memory.md` (Memory Scoping): state that recall is owner-only.
- `docs/runtime-author-guide/runtime-sdk.md` (Platform tools): replace "a generic `Call` for any other registered tool" with "a generic `Call` for any other tool the server lists".
- `docs/getting-started/concepts.md` (Workspace sources): delete " and never sees raw credentials", so the clause ends "the pod itself has no network access during setup".
- `docs/getting-started/concepts.md` (Credentials): replace "two categories of credentials, both following" with "LLM provider and connector credentials, both following".
- `docs/client-guide/authentication.md` (Delivery Modes): replace "Credentials reach agent pods" with "LLM provider credentials reach agent pods".
- `docs/getting-started/concepts.md` (Connector credentials): replace "Pods never hold downstream OAuth tokens:" with "Pods never hold connector OAuth tokens. A VCS token used by `git` is a separate credential that reaches the pod, as [Workspace sources](#workspace-sources) describes. Connector tokens are handled as follows:".
- `docs/index.md` (Credential delivery): after "the pod receives neither client tokens nor connector tokens.", add "A VCS token for a private `gitClone` source is a separate credential: the in-pod `git` credential helper fetches it from the gateway, so that token reaches the pod."
- `docs/operator-guide/security-principles.md` (1.3): before "Compromising a pod yields", add a paragraph: "The in-pod `git` credential helper fetches a VCS token from the gateway, and that token reaches the pod outside the lease flow above."
- `docs/operator-guide/security-principles.md` (4. What Lenny does not do): replace "It prevents them from escaping the pod, exfiltrating credentials, or affecting other tenants." with "It prevents them from escaping the pod or affecting other tenants. An agent can reach any credential the pod holds, such as a direct-mode lease or a VCS token."
- `docs/runtime-author-guide/lifecycle.md` (Runtime Supervisor): replace "credentials reach your runtime through its credential file" with "LLM credentials reach your runtime through its credential file".

### RECORDS-1 · Finding closures and the remediation plan

Targets: `BUILD-GAPS.md` and `gateway-runtime-comms-remediation.md`.

- Close F-10.2.31, F-10.2.32, F-10.2.36, and F-10.2.37, and tick them in the plan.
- Add a note to F-10.2.35, which stays OPEN: the default `user` role on dev identity paths landed here, and the dev headers' SDK option and default-user identity remain owed.
- Add a note to F-4.9.44, which stays OPEN, naming the statements that hold only for a short-lived, scoped token, so that its resolution reviews each one: §1 Core Design Principles item 6 "No broad credentials in pods"; §2 Goals "no standing credentials in pods"; §4.3 **Key rules:** "they receive short-lived access tokens"; §4.9 **Design principle:**; §4.9 **Credential Lease** "It never receives the pool's root secret or long-lived key."; §4.9 **Security Boundaries** "Long-lived credentials ... live **only** in the Token Service and Kubernetes Secrets", "materialized short-lived credentials", and, because §26.2 still calls the grant a lease, "Leases are **revocable**"; §13.1 Credentials row; §13.3 direct-mode flow "long-lived secrets"; §14 **`gitClone.auth` object.** "short-lived HTTPS bearer/basic token scoped to the repository"; §23 Feature Comparison Matrix **Pod security** row; §23.1 item 3 "No standing credentials"; §26.3 scope table Consumer cell; `docs/index.md` Credential delivery "Pods hold no standing credentials."; `docs/about/why-lenny.md` What Lenny is "agents receive short-lived leases scoped to the session", Isolation model "No standing credentials are mounted.", and Design principles item 3; `docs/getting-started/concepts.md` Workspace sources "fetches short-lived tokens" and Credentials "pods never hold long-lived secrets"; `docs/operator-guide/security-principles.md` 1.3 opening sentence and "Compromising a pod yields no usable long-term credential material."; `docs/operator-guide/security.md` Security Context Credentials row; `docs/getting-started/architecture.md` Pod security settings Credentials row and What pods can and cannot do "Hold long-lived API keys or secrets".

The standing records pass files the summary's `## Defects in the shipped tree that this proposal does not stage` at approval, so this deliverable files none of them.

## Testing

Tier 1 rows land in the code step of the named deliverable, with the fixture changes CODE-1 and CODE-2 own. Every test carries a `// spec:` annotation naming §10.2 (Session ownership) and any other section its row names, and every test at tier 2 or above carries a `// diagnosis:` comment. Outcomes are those the cited rule gives.

### Tier 1

**Surface table.** A table-driven case runs each caller against each surface, in `pkg/gateway/sessionserver/session_ownership_test.go`, `session_access_test.go`, `session_owner_binding_test.go`, `pkg/gateway/mcpfabric/mcptools/session_ownership_test.go`, and `pkg/gateway/environment/translator/open_responses_ownership_test.go`.

- Callers: `tenant-viewer`, `billing-viewer`, a custom role without the permission, role-less, in-pod, an owner demoted to each refused role, empty subject with an admitted role, empty subject with a refused role, and no principal.
- Surfaces: REST session routes, `lenny/create_session`, each direct-store tool, `attach_session`, `POST /v1/responses`, `GET /v1/responses/{id}`, and chat completions.
- The chat completions cases run with an invalid body as well as a valid one, because the matrix precedes body validation.

| Behaviour | Deliverable | File | Case that discriminates |
|:--|:--|:--|:--|
| Role-less refusal, rule 4 | CODE-1 | `rbac_gate_test.go` | `TestSessionEndpointAdmitsRolelessPrincipal`: both existing cases are role-less through `asOwner`, so both invert to refused. Fold `TestSessionEndpointMultiTenantRejectsRolelessPrincipal` into it. A separate no-principal case on the bare `srv.Handler()` expects rule 3's `401`. |
| In-pod admission, rule 8 | CODE-1 | `platformtools_test.go`, `pkg/gateway/sessionserver/holder_forward_listener_test.go`, `tests/tier3_contract/holderforward/holderforward_test.go` | `InPod` is set by `WithSessionPrincipal` and survives the forward. A bearer with `caller_type: agent`, a `SessionID`, and no role is refused. `PermSessionEvalWrite` and `PermReadTenantSessions` are refused for `InPod`. |
| Pod tool set, rule 8 | CODE-1 | `platformtools_test.go` | Over a real `mcptools.Register` server with `SessionService` and every optional dependency that registers a card tool set (`Events`, `InputWaits`, `Interactions`, `Runtimes`, `Delegation`, `Memory`, and `VCSCreds`), `ListPlatformTools` returns exactly the card's names. `lenny/create_session`, `lenny/create_and_start_session`, and `lenny/get_session_logs` each return `ErrPlatformToolNotFound` and reach no handler. `lenny/vcs_token` dispatches. |
| `lenny/vcs_token` caller, CODE-1 step 8 | CODE-1 | `pkg/gateway/mcpfabric/mcptools/vcs_token_test.go` | `TestVCSTokenRefusesBearerWithSessionClaim_spec_26_2`: a principal with `SessionID`, `CallerType: agent`, and no `InPod` gets `VALIDATION_ERROR`, reaches no resolver, and records nothing through `VCSLeaseAuditor`. The same principal with `InPod: true` receives the token. |
| Dev default role | CODE-2 | `pkg/gateway/middleware/auth/auth_test.go` | Header absent gives `user`; a header naming only an invalid role gives no roles; a valid header is honored; `AllowDevRoles` false gives no roles. |
| Dev playground mint | CODE-2 | playground mint tests | The dev bearer carries `user` and satisfies `minted.roles ⊆ subject.roles`. |
| Token Service bearer | CODE-1 | `pkg/gateway/sessionserver/rbac_gate_test.go` | A principal built from a Token Service-issued bearer with no mapping row gets `403` on a session route, and the same bearer with a mapping row is admitted. |
| Create dispatch | CODE-3 | `pkg/gateway/sessionserver` create tests | The four outcomes in CODE-3. |
| Elicitation order | CODE-4 | `pkg/gateway/mcpfabric/mcptools` | An invalidated, matrix-refused caller receives `FORBIDDEN` ahead of `USER_INVALIDATED`. |
| Nil access | CODE-5 | `open_responses_ownership_test.go` | `TestOpenResponsesNilSessionAccessIsNative404_spec_10_2` is rewritten: with nil access, `POST /v1/responses` answers `403` and creates nothing, and `GET /v1/responses/{id}` answers `403`. |
| Refused turn has no effect | CODE-5 | `open_responses_ownership_test.go` | A warm-eligible and a cold-eligible refused turn each assert zero forwards, zero reservations, and zero rehydrates. |
| Reader continuation | CODE-5 | `open_responses_ownership_test.go` | A reader holding create receives the same body as an unknown id. |
| `GET` order | CODE-5 | `open_responses_ownership_test.go` | Empty-subject and no-principal callers answer the native `401`. |
| Role refresh | CODE-7 | `pkg/gateway/middleware/auth/platform_roles_test.go` | Mapping replaces the claim; no assignment restores the claim, including an empty non-nil claim that has a removed mapped role in `Roles`; nil `ClaimRoles` calls no resolver; a resolver error returns the error. |
| In-pod evaluation | CODE-7 | `rbac_gate_test.go` | `AuthorizeSessionRead` on an in-pod principal skips the resolver through nil `ClaimRoles`. The forwarded call sets `Accept: application/json`, so no in-pod path reaches a stream loop. |
| Bypass uses refreshed roles | CODE-7 | `rbac_gate_test.go` | A principal whose role grants `PermReadTenantSessions` and read-own loses only the tenant-read role through the mapping, and a tick on a non-owned session returns `sessionstore.ErrNotFound`. |
| Evaluation order | CODE-7 | `rbac_gate_test.go` and `cmd/lenny-gateway` attach tests | Each sentinel maps to its tool error, and an empty-subject refused-role caller receives `FORBIDDEN` on attach. |
| Stream close, each loop | CODE-8 | `pkg/gateway/sessionserver` event and log stream tests, `pkg/gateway/mcpfabric/mcp/stream_test.go`, and the WebSocket attach tests | With an injected interval: refusal closes; a store error closes, for an owner too; a passing tick leaves delivery intact. The interval default applies to a zero value without a panic. |
| Re-attach during a tick | CODE-8 | WebSocket attach tests | A re-attach during an in-flight `Authorize` leaves the connection open and the new attach delivering. |
| Drops during a tick | CODE-8 | same stream tests | With a blocked `Authorize` and more than the buffer's events published, the stream closes and a cursor resume returns every event. WebSocket closes with `1013`. |
| Frame cursor | CODE-9 | `projection_test.go`, `websocket_attach_test.go` | Every sample in the projected-frame test, extended with a non-approval `tool_use` phase, carries `params.seq` equal to `ev.Seq`, including an `error` payload holding its own `seq` key. A WebSocket client that re-attaches with the `params.seq` of its last output frame receives only later frames. |

### TEST-1 · Cross-tier tests

Targets: the directories below, in `tests/`.

**Tier 3.**

- Put the denial parity in `tests/tier3_contract/rest_mcp_consistency`, extending `caller_check_parity_test.go` and `error_matrix_test.go`, with adapter cases in `rest_openai_chat` and `rest_openai_responses`. Assert identical `code`, `category`, and `retryable` per §15.2.1 item 5(c) and (d).
- Keep `rest_sessions/session_ownership_contract_test.go` for the REST-only `403` and `404` wire cases.
- Add a wire-level case that a WebSocket re-evaluation refusal closes with `1008` and reason `session_access_lost`.
- Add a wire-level case that a WebSocket attach frame of each kind carries `params.seq` per SPEC-9.

**Tier 4**, in `tests/tier4_integration/session_ownership_test.go`.

- Boot the gateway with `--postgres-dsn` and `--bearer-trust-hmac-key-file`, and authenticate with bearer tokens.
- Assign a built-in role through the platform-managed mapping, then `DELETE .../role`, and assert the open stream closes and the reconnect receives rule 7's answer.
- Bound each wait by `StreamAccessInterval` plus a margin, because the binary has no interval injection. The existing 30-second helper does not apply.

**Tier 7a**, a new stream race test in `tests/tier7a_load_local`. It runs role changes, ticks, event publishes, and re-attach under `-race` with randomized interleavings, and an invariant checker asserts that no event published after a tick that refused reaches the client, and that no event is lost across a close and cursor resume.

**Tier 8**, a new test in `tests/tier8_chaos`.

- Use `lifecycleharness.PostgresStore` with an injected `StreamAccessInterval`.
- Cover Postgres loss alone and dual-store loss. In the dual-store case, the stream receives `PLATFORM_DEGRADED` within 1 second under §10.1.3 item 5 and closes at the next tick.
- Assert that the session's row and pod are unaffected after recovery, and that a cursor resume replays with no gap.

**Tier 9**, in `tests/tier9_security/session_owner_isolation_test.go` and `open_responses_warm_user_test.go`.

- Run on the live gateway helper with a built-in reader role assigned through the mapping.
- A `tenant-viewer` creates through neither MCP nor an adapter, and a demoted owner sends no warm turn.
- A platform tool call through `platformtools.New` over a registered server creates no session through `lenny/create_session` or `lenny/create_and_start_session`, and reads no other session through `lenny/get_session_logs`, on a tenant with a demoted owner.
- On a `mcptools.Register` server with a stub `VCSCreds` behind the auth middleware, a bearer whose JWT carries a `session_id` claim calls `lenny/vcs_token` and is refused as CODE-1 step 8 states, and a call through `platformtools.New` over that server returns the token. The server runs in-process, because the live gateway registers the tool only when it holds a Token Service connection.
- Wait out the real interval, and assert that no event after the removal reaches the client beyond the close.

## Edge cases and accepted failure modes

- **Slow tick.** The evaluation runs inline on the delivery goroutine, so delivery pauses for at most the read bound on each tick. CODE-8's drop check owns the only data consequence.
- **Stalled client.** A stalled write delays the tick until the write returns. The attach legs bound that write and end the stream; the sessionserver SSE loops set no write deadline.
- **Custom-role read error.** `principalHolds` treats the error as a miss, so the matrix refuses and the stream closes.
- **Refused `playground_client_closed` cancel.** The hint is fire-and-forget, so a `FORBIDDEN` answer has no client effect.

## Files touched on application (non-spec)

- `cmd/lenny-gateway/credentialsurface.go`, `mcpsurface.go`, `sessionsrv.go`
- `pkg/adapter/gatewaycontrol/platformtools.go`
- `pkg/apis/lenny/v1alpha1/runtime_types.go`
- `charts/lenny/crds/lenny.dev_runtimes.yaml`, `pkg/embedded/crds/lenny.dev_runtimes.yaml`
- `pkg/gateway/environment/translator/continuity.go`, `open_responses.go`, `openai_chat.go`, `singleshot.go`
- `pkg/gateway/gatewaycontrol/platformtools/platformtools.go`
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/platformtools.go`
- `pkg/gateway/mcpfabric/mcp/mcp.go`, `projection.go`, `stream.go`, `websocket_attach.go`
- `pkg/gateway/mcpfabric/mcptools/client_tools.go`, `elicitation.go`, `mcptools.go`, `mcptools_register.go`
- `pkg/gateway/mcpfabric/playground/token.go`
- `pkg/gateway/middleware/auth/auth.go`
- `pkg/gateway/sessionserver/events.go`, `logs.go`, `rbac_gate.go`, `request_identity.go`, `route_table.go`, `service.go`, `sessionserver.go`
- The test files CODE-1 to CODE-9 and TEST-1 name, and `tests/testinfra/ownerprincipal/ownerprincipal.go`
- `docs/api/mcp.md`, `open-responses.md`, `openai-completions.md`, `rest.md`
- `docs/client-guide/authentication.md`, `streaming.md`
- `docs/getting-started/concepts.md`
- `docs/index.md`
- `docs/operator-guide/security-principles.md`, `security.md`
- `docs/runtime-author-guide/lifecycle.md`, `runtime-sdk.md`
- `docs/tutorials/agent-memory.md`
- `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`
