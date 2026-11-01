# Summary: Enforce the §10.2 permission matrix on every session surface and bound reader reach and stream access

## Summary

**Problem statement.** The §10.2 permission step sits on the REST route table, so session creates through MCP and the built-in adapters, the direct-store MCP tools, `attach_session`, and Open Responses warm turns skip it. A single-tenant deployment admits a role-less principal to every session permission, while §10.2 rule 4 states a narrower role-less read and no matrix row covers the in-pod principal. A reader holding the create permission continues another user's conversation cold, `lenny/memory_query` under the read bypass recalls the owner's user-wide memories, and long-lived read streams check access only at open. This proposal discharges F-10.2.31, F-10.2.32, F-10.2.36, and F-10.2.37.

**What changes.**

- §10.2 **Session ownership.** gains the role-less refusal, the memory reach, the Open Responses coverage, the matrix refusal mapping, the in-pod principal, and the stream re-evaluation. §15, §9.4, §15.1, §27.3.1, §10.1.3, §12.3, §28.5.3, §9.1, §26.2, §14, §4.3, §4.7.11, §4.9, §5.1, §9.3, §13.3, and §23 align with it.
- `pkg/gateway/sessionserver` holds one context-based matrix predicate, dispatches create services through the service mux, and exposes one stream read evaluation.
- `pkg/gateway/mcpfabric/mcptools` and the `attach_session` authorizer run the matrix ahead of ownership, and `lenny/vcs_token` takes the in-pod test (CODE-1 step 8).
- `pkg/gateway/gatewaycontrol/platformtools` limits a pod to the `CH-MCP-PLATFORM` tools.
- `pkg/gateway/environment/translator` checks the matrix at the top of `POST` and `GET /v1/responses` and `POST /v1/chat/completions`, and refuses a non-owner continuation.
- `pkg/gateway/middleware/auth` records claim roles and exposes one role-refresh helper, and both dev identity paths carry `user`.
- The stream loops gain a ticker case, every attach frame carries its `SeqNum` as `params.seq`, and memory recall becomes owner-only on MCP and REST.

**Decisions.**

- **D1.** The design reuses existing seams and adds no RPC, frame, API error code, or endpoint: the service mux, the platform tool bridge, one matrix predicate, the `attach_session` `Authorize` function, and one role-precedence helper.
- **D2.** The in-pod discriminator is a positive in-process `Principal.InPod` field set only in `WithSessionPrincipal`. `CallerType` and `SessionID` are copied from JWT claims and §25.1 issues `caller_type: "agent"` to external accounts, and an inference from an absent credential fails open where a positive field fails closed. This corrects the problem statement's prior-art note that no marker is needed. `registerVCSTokenTool` tests the same field: its `SessionID` guard is the inference this decision rejects, the Token Service copies a subject's `session_id` onto every exchanged bearer, and §13.3 lists that claim, so the guard holds today only because no mint originates the claim.
- **D3.** The in-pod admission covers "Create / cancel own sessions" and "Read own session history" only, inside the shared predicate. `WithSessionPrincipal` is the single construction site for the `LNK-GWCONTROL` path and the forward listener, so one branch covers the direct-store tools and the forwarded call. `PermSessionEvalWrite` and `PermReadTenantSessions` are refused.
- **D4.** A role-less principal holds no matrix permission in every tenancy mode. `Server.multiTenant` and `Options.MultiTenant` go with the deleted branch, because that branch is their only read. The no-principal admission stays, because rule 3 expects it. A single-tenant deployment whose IdP sends no role claim assigns roles through the claim or the platform-managed mapping, as multi-tenant deployments do today.
- **D5.** Bearers issued by `POST /v1/oauth/token` carry no roles claim (`mintToken`, `tokenexchange.Token`, and the §13.3 claim table), so a rotated, narrowed, or `a2a_delegation` bearer holds only a platform-managed role. Deleting the branch extends the multi-tenant refusal of such bearers to single-tenant mode. This is an accepted consequence, and the question of copying roles at exchange is filed below.
- **D6.** Both dev identity paths carry `user`: `serveDevHeaders` assigns it under `AllowDevRoles` when no `X-Lenny-Roles` header is present, and `mintDev` sets it. Deleting the role-less branch otherwise refuses every dev-header caller and the dev playground. The embedded `lenny up` bearer already carries `platform-admin`.
- **D7.** Every `POST /v1/responses` creates or acts on a session, so one matrix check at the top of `handleCreate` covers the warm, cold, and forwarded paths. It precedes `requireSubject`, because rule 3 places the matrix ahead of the `401`, and it precedes resolve, the forward, and the reservation, so a refused turn consumes no ordinal. A forwarded turn runs the same check on the coordinator. §15 warm condition 4 and the §29.3 row stay verbatim, because the reservation write re-checks row state and a role is not on the row.
- **D8.** The wider reading of the owner's sentence applies: a non-owner cannot continue a conversation even when it holds create and read. `handleCreate` applies the owner test after `resolve` and answers the native `404`. `resolve` stays a read, because `GET /v1/responses/{id}` and every rehydrate hop share it. Condition 4's owner term stays as a re-check a non-owner no longer reaches.
- **D9.** `GET /v1/responses/{id}` gets the read matrix check, because the owner decision is the matrix on every surface and an owner without "Read own session history" otherwise passes `SessionAccessible` on this route.
- **D10.** Memory recall is owner-only on both surfaces under one rule, as a refinement of the read: "Read own session history" stays the matrix row, followed by an owner test. A demoted owner holding read keeps recall, and the in-pod subject is the owner. On the session-scoped REST route this is a restriction rather than a leak fix. The route gets a §15.1 row because rule 3 and rule 7's ordering sentence attach to surfaces rule 6 covers, rule 6 reaches only endpoints in §15.1, and the route is listed nowhere in `spec/`.
- **D11.** Streams re-evaluate on a ticker case inside each existing `select`, with no goroutine. `Server.AuthorizeSessionRead` is the one evaluation, and the attach `Authorize` closure calls it at open and on each tick, so attach has no second authorizer. The 60-second interval is fixed in the specification, so no flag or config key is added.
- **D12.** `authmw.RefreshRoles` is the one role-precedence implementation. It needs `Principal.ClaimRoles`, because after `DELETE .../role` the resolver reports no assignment and frozen `Principal.Roles` may hold the removed mapped role.
- **D13.** A failed re-evaluation closes the stream, including an owner's stream, as the owner decision states and as the auth middleware fails closed on the same read. The session continues and the client reattaches with its cursor. §10.1.3 and §12.3 record the stream close and the reattach dependency on Postgres, because a close during a store outage is the observable effect and neither section would otherwise state it.
- **D14.** A refusal on WebSocket closes the whole connection with standard code `1008` and close reason `session_access_lost`, because later frames run on the principal frozen at upgrade and an ended attach alone leaves the client with no signal. Code `4401` stays bound to `bearer_revoked` and its re-authenticate action, since a role change does not invalidate the bearer. `session_access_lost` is a close reason rather than an API error code, which keeps D1 true.
- **D15.** New §10.2 text is appended as rules 8 and 9, so inbound citations to rules 1 to 7 stay valid. Rule 5 needs no edit: the rule 6 extension makes a continuation an act under its general clause.
- **D16.** Dropped events during an inline evaluation close the stream under rule 9, because the evaluation stops the subscriber channel from draining and a slow read that succeeds would otherwise lose events silently.
- **D17.** `Bridge` holds the pod tool set, and `ListPlatformTools` and `CallPlatformTool` both apply it, so a pod reaches the `CH-MCP-PLATFORM` tools in every tenancy mode. Ownership cannot bound a pod, because every session it can name has the same owner, and a root session created through another tool carries no delegation lease. The check sits on the bridge alone, because a forwarded pod call originates in a handler the bridge already dispatched. An excluded tool fails as an unregistered tool, which keeps D1 true. `lenny/vcs_token` joins the card because the `git` credential helper calls it. The helper runs in the pod on the runtime's manifest nonce, so a token reaches the pod. SPEC-8 corrects each statement this falsifies whatever the token's lifetime, §26.2 is the home of the fact, and no site states a replacement guarantee. A statement that holds only for a short-lived token stays and is named under F-4.9.44 by RECORDS-1. SPEC-8 moves the spec toward the code on a security statement: the spec barred a token in the pod, the tree delivers one, and this proposal changes the spec. After SPEC-8 no spec sentence bars a short-lived token in the pod, so the owner's open question on a token in the pod now has no bar behind it, and keeping tokens out of the pod would be a new requirement staged by a later proposal. This records shipped behavior, and F-4.9.44 and the owner's git and egress review still decide what token reaches a pod.
- **D18.** `params.seq` goes on every `SessionEvent` frame through the shared projection, on both transports. Rule 9 makes a WebSocket close routine, a WebSocket client has no `id:` line, and the §15 `SessionEvent` schema already requires the adapter to forward `SeqNum` into its envelope. A transport branch would give one event two wire forms.

**Watch out for.**

- CODE-1, CODE-2, and CODE-4 land together. Without the `InPod` admission every pod tool call receives `FORBIDDEN`, and without the `user` role fixtures every dev-header test receives `403`. CODE-3 lands with or after them.
- The bearer path sets a non-nil `ClaimRoles` even for an empty claim. `append([]auth.Role(nil), claims.Roles...)` yields nil on an empty claim, and nil is the marker `RefreshRoles` skips, so a mapped role on a role-less bearer would never be re-read.
- `AuthorizeSessionRead` carries the refreshed principal through both evaluations. `SessionAccessible` reads the principal from its context, so a frozen context keeps the read bypass alive after the role is removed.
- `time.NewTicker` panics on a non-positive duration, so every interval has a positive default.
- A WebSocket tick that sees a cancelled context returns without closing the connection. `attachCancel` cancels that context on re-attach, and a close there kills the socket the new attach starts on.
- A role-less or refused principal with an empty subject moves from `401` to `403` on creates and attach. Only an empty-subject principal the matrix admits and an absent principal stay `401`.
- `permitted` is false on nil `SessionAccess`, so a handler with no access answers `403` to first turns as well.
- `ownerprincipal.Context` does not reach the sessionserver `ownerCtx` and `asOwner` helpers, which have their own role-less principal. Both cases of `TestSessionEndpointAdmitsRolelessPrincipal` are role-less through `asOwner`.
- `tests/tier9_security/mcp_management_authz_sweep_test.go` keeps passing after CODE-2 while silently testing `user` unless its `noRole` caller stays role-less.
- `ownedSessionRoutes` filters on the wrapper name, so GET memory silently leaves two matrix tests unless the filter accepts `ownerReadID`. `ownerprincipal.SessionAccess` and `fakeService` ignore `act`, so a reader-refusal case on them passes vacuously.
- A dev-header principal has nil `ClaimRoles` and cannot be demoted, and custom roles cannot be mapped on Postgres (F-10.2.34). Tiers 4 and 9 therefore use bearers and a built-in role, and they wait out the real 60-second interval.
- The playground client does not reconnect on close, so the §27.3.1 client action is a user action there.

## Goals

- The §10.2 matrix applies on every session surface: REST, MCP, the built-in adapters, `attach_session`, and session creates through the service layer.
- A refused caller receives the same denial on each surface.
- The in-pod principal has an explicit, positive matrix treatment.
- A role-less principal holds no matrix permission in any tenancy mode.
- A demoted owner cannot send warm turns, and a non-owner cannot continue a conversation.
- Memory recall is owner-only.
- An open read stream observes a platform-managed role change at its next re-evaluation and closes when access is lost or cannot be evaluated.

## Non-goals

- Stating the single-tenant role-less admission in §10.2: it writes a tenancy-dependent path into the matrix and lets role revocation raise a user's permissions.
- Treating every role-less principal as `user`, or adding a configurable default role: this reverses the F-10.2.4 multi-tenant fail-closed and is a new mechanism beyond the problem.
- Keeping the admission under dev mode at the gate: this moves the branch rather than removing it. The role is assigned at the identity boundary instead.
- Handling dev callers only by adding `X-Lenny-Roles` to test fixtures: the dev playground and SDK dev clients have no way to send a role, so dev mode would stay broken.
- Exempting the in-pod principal on `CallerType == agent`, alone, with a non-empty `SessionID`, or with empty `Roles`: all of these derive from claims, so an external token passes them.
- Inferring in-pod from an absent `CredentialFrom`: a future principal built without a credential would be exempted, whereas a positive field fails closed.
- Giving the in-pod principal `Roles: [user]`: `user` also grants `PermSessionEvalWrite` and `PermManageOwnCredentials`, so an untrusted runtime would hold rows the decision does not grant.
- Exempting the in-pod principal from every permission: this admits `session:eval:write`, which rule 8 keeps refused.
- Wrapping the create handlers in `requireSessionPermission` beside `guardTenant`: this keeps a second dispatch path beside the service mux.
- Adding the check to `ToolCallGate`: it receives no tool name, so it cannot separate a read from an act.
- Per-tool permission checks at each direct-store site: `lookupCallerSession` is the single site and already receives `act`.
- Folding the matrix into `SessionAccessible`: this collapses rule 7's `403` into `404`.
- Adding a fifth warm condition, a permission term in condition 4 or `warmEligible`, or a re-check under the reservation lock: a role is not on the row, the reservation text would become false, and a failed warm check would still reach rehydrate or the forward.
- A role change racing a reservation, and a single request admitted just before a demotion: the limit is one in-flight request.
- The narrow reading of "a reader cannot continue", refusing only callers without the create permission: a tenant-admin would still run a runtime over another user's transcript, which conflicts with rule 5.
- Changing `continuity.resolve` to act=true: `GET /v1/responses/{id}` and every rehydrate hop share it and are reads.
- Answering a reader's continuation with `403`: rule 7 gives a caller that may not act the existence-hiding `404`, as derive does.
- Placing the Open Responses check after `requireSubject` or `parseTurn`: rule 3 places the matrix first, and a refused caller's answer would depend on the body.
- Passing act=true to `lookupCallerSession` for `memory_query`: this demands the manage permission and removes recall from a read-only owner.
- Scoping a reader's `memory_query` to the session it runs under: the decision is owner-only, and this keeps two recall behaviors.
- Leaving `GET /v1/sessions/{id}/memory` readable by readers: it is defensible because the route is session-scoped, but it requires a carve-out from the recorded owner-only sentence.
- A goroutine context watcher around each stream: this adds a concurrency path and a cancellation cause, where a `select` case does the same work.
- Skipping the session row re-read on a tick: `Authorize` already reads it, and reuse keeps attach to one authorizer. The cost is one point read per minute per stream.
- Exempting owner streams from re-evaluation: rule 7 requires read of an owner too, and a demotion to `billing-viewer` removes read.
- Tolerating store misses or sparing owners on a store error: this needs an owner test on a failed read and a counter, and it exposes a failure-tolerance rule for a Low finding. The recorded decision closes on failure, and a reconnect with a cursor recovers.
- Failing open on a store error as `startRevocationWatch` does: that watch is secondary to a per-request check, while this one is the only check on an open stream.
- Splitting `principalHolds` to classify custom-role errors: any custom-role read error already refuses, and a refusal closes the stream.
- Staggering the first tick: ticks already follow each stream's open time.
- Reusing 4401 or adding a Lenny close code: §27 binds 4401 to re-authentication, and a new code is new protocol surface.
- A new SSE terminal frame, event kind, or metric: a reconnect already returns the authoritative answer.
- Ending only the attach goroutine on WebSocket: the client is left silent on an open socket whose later frames run on frozen roles.
- A configurable interval, Helm value, or `streamAccessRecheckSeconds` key: the specification fixes the interval.
- Pushing role changes across replicas, or a cross-stream role cache: re-evaluation is the recorded decision, and a cache adds an invalidation path.
- Closing streams at token expiry to bound claim-only roles, or re-validating the bearer on each tick: the problem asks only that the limit be stated.
- Capping non-owner stream lifetime and relying on reconnect: this closes healthy streams every minute and leaves demoted owners uncovered.
- Refreshing roles on each tool call on an upgraded WebSocket: this is outside the stream gap and is filed below.
- Carrying the OIDC role claim into the playground session record: single-tenant playground `oidc` users with no mapping row move from admitted to refused, which is the behavior multi-tenant has today and has the same remedy as an IdP sending no role claim. The fix needs a claim-name decision and a record field, so it is filed below.
- Rule 6 or spec text for F-10.2.33, F-10.2.34, F-15.1.64, F-15.2.30, F-12.9.23, and the SDK and default-user work of F-10.2.35: these are owned elsewhere. Rule 4's read grant is unchanged, so F-12.9.23 is not contradicted.
- Listing `POST` and `DELETE /v1/sessions/{id}/memory` in §15.1: both are already owner-gated through `manageID`, and the omission is filed below.

## Open decisions for human to make

None. The review loops write this section, and it starts empty.

## Defects in the shipped tree that this proposal does not stage

- **WebSocket tool calls run under roles frozen at upgrade.** A frame on an upgraded MCP WebSocket runs on the principal the auth middleware built at upgrade, so a later role change reaches an open connection only through rule 9's close of an attached stream. It stays out of scope because the validated problem covers read streams.
- **Memory routes and codes are missing from §15.1.** `POST` and `DELETE /v1/sessions/{id}/memory` appear in no spec section, and `MEMORY_UNAVAILABLE` and `SESSION_NOT_USER_SCOPED` are absent from the §15.1 error catalog. It stays out of scope because both routes are already owner-gated through `manageID`, and this proposal lists only the route rule 4 changes.
- **The playground `oidc` path drops the role claim.** The `OIDCSubject` struct has no roles field, so a minted bearer carries no OIDC role and the principal holds only a platform-managed role. It stays out of scope because the fix needs a claim-name decision and a record field.
- **`lenny/delegate_task` and `lenny/await_children` take the acting session from their arguments.** Both handlers read the session from the call rather than through `callerSessionID`, so an in-pod call can name another session with the same owner and delegate under that session's lease. It stays out of scope because the tool bound neither causes nor changes this reach.
- **Exchanged bearers drop the subject's roles.** `mintToken` builds claims with no `Roles`, `toExchangeToken` and `tokenexchange.Token` carry none, and §13.3 states no roles row. A decision is owed on whether exchange copies roles as a subset under `minted.roles ⊆ subject.roles`. It stays out of scope because it is a token-contract decision, and D5 records the consequence.
- **§10.7 and §10.2 disagree on the runtime's session credential.** §10.7 says the runtime may submit eval "via its session credential", while §10.2 says a pod never holds a `session_capability`. It stays out of scope because the tension predates this proposal and the in-pod path reaches no eval tool.
- **The streaming guide names a reconnect parameter the SSE routes do not read.** `docs/client-guide/streaming.md` passes `?cursor=` on `GET /v1/sessions/{id}/logs`, while `resumeCursor` reads `Last-Event-ID` and `?afterSeq=` only, so that reconnect starts from the beginning of the retained backlog. It stays out of scope because the page's SSE cursor format is a separate documentation correction.
- **Comments on the VCS path keep credential claims the tree does not hold.** The `vcscred` package doc, `gitref.Clone`, `CloneArchive`, `extractGitCloneSource`, `rewriteExtractedSources`, and the `gitClone` case in `pkg/adapter/workspace/materialize.go` say the pod or runtime never sees VCS credentials. The `git-credential-lenny` package doc and the `VCSCreds` field doc say the runtime never holds a long-lived credential, which a stored personal access token (F-4.9.44) breaks. It stays out of scope because this proposal changes only the caller check on that path (CODE-1) and the owner's git and egress review may replace that code.
- **`lenny/vcs_token` ignores the session's plan.** §26.2 and §26.3 say the scope is issued only when the session's plan holds a private `gitClone`, while the handler takes any host and mode from the call and checks only the in-pod principal and the tenant's pool, so a pod can request a write token for any pool host. It stays out of scope because the owner's git and egress review decides what token reaches a pod.
- **The VCS token crosses paths the credential rules do not name.** The tool result passes the `PreToolResult` chain, and `CallPlatformTool` carries it across `LNK-GWCONTROL`, while §16.4 and §4.7.11 item 6 omit that RPC from the credential-sensitive list and §4.9 **Why credential elicitation via MCP is not supported** names interceptor inspection as the exposure. It stays out of scope for the same reason.
- **A slow client loses live events with no close.** The session event `Bus` drops a live delivery when a subscriber channel is full, and no stream loop closes on it, while the §7.2 **SSE back-pressure policy:** says the gateway drops the connection. It stays out of scope because rule 9's check covers only the pause a re-evaluation adds, and the general case needs a drop signal from the bus to each stream loop.
- **Six filed findings on adjacent permission surfaces stay open.** F-10.2.33 keeps `tenant-viewer` off the credential-pool reads (`pkg/gateway/externalapi/admin/routes.go:597`, `BUILD-GAPS.md:15193`). F-10.2.34 refuses a custom role in the Postgres user store (`pkg/gateway/environment/userstore/pgstore/pgstore.go:380`, `BUILD-GAPS.md:15165`). F-15.1.64 checks only the tenant on `GET /v1/blobs/{ref...}` (`pkg/gateway/sessionserver/upload.go:796`, `BUILD-GAPS.md:30704`). F-15.2.30 leaves the native MCP elicitation reply with no consumer (`pkg/gateway/mcpfabric/mcp/mcp.go:315`, `BUILD-GAPS.md:31067`). F-12.9.23 leaves the §12.9 T4 "explicit grant" undefined (`spec/12_storage-architecture.md:1094`, `BUILD-GAPS.md:26467`). The SDK option and default-user identity of F-10.2.35 stay owed (`sdks/client/go/lenny/client.go:58`, `BUILD-GAPS.md:15172`). They stay out of scope because each has its own `BUILD-GAPS.md` entry and resolution, and no staged rule depends on any of them. F-15.1.64's entry names the next proposal that edits §10.2 **Session ownership.** as a possible owner, and this proposal still leaves it open, because the blob route is outside the surfaces rule 6 lists and the fix needs a choice of the non-owner outcome on that route.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0110_fix_session-owner-authorization-plan-10-2-session-owner-authoriz | Implemented | This proposal reverses 0110's reader cold continuation (§15 and the `warmEligible` comment) and changes its role-less read sentence in rule 4. | Nothing. A landed proposal is not edited, and this proposal records the reversal in its own text. |
| 0108_fix_proposal-0061-re-derived-plan-item-gateway-runtime-comms | Implemented, 2026-10-30 | This proposal narrows 0108's rule that every continuation not served warm runs cold: the rule now covers only a caller that holds the create permission and owns the session. A caller the matrix refuses receives the native `403` ahead of resolution, and a non-owner receives the native `404` after it, with no forward, reservation, or cold session (SPEC-2 Edit (a) and Edit (c)). The warm conditions, the reservation, off-holder forwarding, the cold path for a permitted owner, and §29.3 stay verbatim, and condition 4's owner term stays as a re-check (D8). | Nothing. A landed proposal is not edited, and this proposal records the narrowing in its own text. |

## Deliverable index

- SPEC-1 — spec/10_gateway-internals.md — §10.2 **Session ownership.** rules 4, 6, and 7, appended rules 8 and 9, and **Role assignment:**.
- SPEC-2 — spec/15_external-api-surface.md — Open Responses conversation sessions: matrix check, `GET` refusal, and owner-only continuation.
- SPEC-3 — spec/09_mcp-integration.md — §9.4 recall reach.
- SPEC-4 — spec/15_external-api-surface.md — §15.1 row for `GET /v1/sessions/{id}/memory`.
- SPEC-5 — spec/27_web-playground.md — §27.3.1 failure-modes row for the rule 9 close.
- SPEC-6 — spec/10_gateway-internals.md — §10.1.3 item 5 stream close during dual-store loss.
- SPEC-7 — spec/12_storage-architecture.md — §12.3 stream close during Postgres failover.
- SPEC-8 — spec/28_communication-channels.md, spec/09_mcp-integration.md, spec/26_reference-runtime-catalog.md, spec/14_workspace-plan-schema.md, spec/04_system-components.md, spec/05_runtime-registry-and-pool-model.md, spec/13_security-model.md, spec/23_competitive-landscape.md — `lenny/vcs_token` on the `CH-MCP-PLATFORM` card and in the §9.1 table, and the statements a token in the pod falsifies, with §26.2 as the home.
- SPEC-9 — spec/15_external-api-surface.md — §15.2 **Event-stream resume.** `params.seq`, and the per-kind table's output and terminated rows.
- CODE-1 — pkg/gateway/sessionserver/rbac_gate.go — One context-based matrix predicate, `Principal.InPod`, the pod tool set, the `lenny/vcs_token` caller check, the Runtime `env` comment, and removal of the tenancy branch.
- CODE-2 — pkg/gateway/middleware/auth/auth.go — Dev identity paths carry `user`, and the fixtures that follow from it.
- CODE-3 — pkg/gateway/sessionserver/service.go — Create services dispatch through the service mux.
- CODE-4 — pkg/gateway/mcpfabric/mcptools/mcptools.go — Matrix step in `lookupCallerSession` and its refusal mapping.
- CODE-5 — pkg/gateway/environment/translator/open_responses.go — Matrix first on the built-in adapters, owner-only continuation, and read check on `GET`.
- CODE-6 — pkg/gateway/sessionserver/route_table.go — Owner-only memory recall on REST and MCP.
- CODE-7 — pkg/gateway/sessionserver/rbac_gate.go — `RefreshRoles`, `ClaimRoles`, and `AuthorizeSessionRead`.
- CODE-8 — pkg/gateway/mcpfabric/mcp/stream.go — Ticker case in the stream loops.
- CODE-9 — pkg/gateway/mcpfabric/mcp/projection.go — `params.seq` on every attach frame.
- TEST-1 — tests/ — Cross-tier tests.
- DOCS-1 — docs/ — Client, operator, API, and tutorial pages.
- RECORDS-1 — BUILD-GAPS.md — Finding closures and the remediation plan.
