# Problem: Reader access and the §10.2 permission matrix are not enforced on every surface

## Statement

The §10.2 permission step sits on the REST route table, so a path that does not pass through that table skips it. Those paths are `CreateSessionService`, `CreateAndStartService`, the direct-store MCP tools, and the Open Responses warm path. Separately, no text bounds how far the read permission reaches, and no text states when a long-lived stream re-evaluates access. Findings: F-10.2.31, F-10.2.32 (including its two 0110 notes), F-10.2.36, and F-10.2.37. All of them stay in one proposal, as the remediation plan records, because every decision lands in the §10.2 **Session ownership.** rules.

The gaps fall into the following groups.

**Group A: matrix enforcement.**

1. `CreateSessionService` and `CreateAndStartService` call `guardTenant` around the bare handler and skip the `requireSessionPermission` gate that wraps the REST routes. Any principal whose role lacks "Create / cancel own sessions", including `tenant-viewer`, `billing-viewer`, and a custom role without the permission, therefore creates and starts sessions through MCP `lenny/create_session` and through the built-in adapters' single-shot binder (Open Responses and chat completions). `lenny/create_and_start_session` dispatches through `ServiceCall` and is gated, so this gap does not cover every MCP create.
2. The direct-store MCP session tools run `lookupCallerSession`, which applies the 0110 ownership check with no matrix check ahead of it. A principal lacking the permission acts on or reads a session it owns, and a principal that the matrix refuses receives `RESOURCE_NOT_FOUND` on MCP where REST answers `403`. The `attach_session` `Authorize` closure bypasses `lookupCallerSession` and is a separate site for the same fix. Tools dispatched through `ServiceCall` are outside this gap.
3. An owner demoted out of "Create / cancel own sessions" passes `warmEligible` and sends warm turns. A warm turn never reaches `CreateAndStartService`, and `SessionAccessible` grants an owner regardless of role, so neither existing check closes this path. §15 warm condition 4 names ownership only. The check must run before the forward to the coordinator, which happens ahead of `serveWarm`, and the translator has no permission seam today.
4. A role-less principal in a single-tenant deployment passes every session permission gate, because `sessionPermissionGranted` returns `!s.multiTenant` for empty roles (F-10.2.31). The matrix has no such row. This branch is a prerequisite of gaps 1 to 3: moving the existing gate onto these surfaces unchanged carries the admission with it, and refuses the in-pod principal in multi-tenant mode.
5. The in-pod session principal (`CallerType` `agent`, subject equal to the session owner, no roles) has no matrix treatment. In multi-tenant mode it already receives `403 FORBIDDEN` from `ServiceCall`-backed tools, while it is admitted by `lenny/create_session` and the direct-store tools. The in-pod treatment must therefore cover the `ServiceCall` path as well as the tools gaps 1 and 2 name.

**Group B: reach of the read permission.**

6. A non-owner reader cold-continues another user's Open Responses conversation. `resolve` admits a read, the warm check fails, and `serveCold` rehydrates the transcript and binds through `CreateAndStartService`. For `tenant-viewer` this is a consequence of gap 1, and the create gate closes it. A principal holding both "Create / cancel own sessions" and "Read other users' sessions" (a `tenant-admin`, a `platform-admin`, or the custom role the §10.2 `user` bullet describes) passes that gate and continues cold, which §15 currently directs: every continuation failing a warm condition is served cold. The recorded decision that a reader cannot continue a conversation therefore needs a §15 edit for that principal, and §10.2 rule 5, which counts deriving as an act, sits in tension with the current §15 text.
7. `lenny/memory_query` under the read bypass returns the owner's user-wide memories, because it queries with no `SessionID`. No text states whether a reader's permission on one session extends to the owner's user-scoped memory. `GET /v1/sessions/{id}/memory` differs: it scopes the query to the named session and returns only memories written under that session. That route appears in the OpenAPI document and in no section of `spec/` or `docs/`.

**Group C: access on long-lived streams.**

8. Long-lived read streams check access only at open: `attach_session` over SSE, `attach_session` over WebSocket, the REST `GET /v1/sessions/{id}/events` tail, and the SSE form of `GET /v1/sessions/{id}/logs`. Each runs until disconnect. A reader whose role is removed keeps receiving events. A re-evaluation must re-resolve roles, because `Principal.Roles` is fixed once per HTTP request in the auth middleware and is frozen on the stream's context, so calling `SessionAccessible` again on that context observes only a custom role's definition. A role delivered solely through the OIDC claim stays unobservable until a new token arrives, and the proposal must state that limit. §27.3.1 says an upgraded WebSocket continues under its token until the connection drops or the token's session expiry, so that text must be reconciled.

The spec already requires the matrix on most of these surfaces. §10.2 rules 3, 6, and 7 apply it to session creates and to the §15.2 tools that name a session, rule 7 names the MCP and adapter outcomes, and §15.2.1 rules 1 and 5(c) require a shared service layer and the same authorization denial across surfaces. Gaps 1 and 2 are therefore code defects against existing text for external callers. The spec is silent on the following items only: the in-pod principal, the role-less principal, the warm check's permission condition, a cold continuation by a reader that holds the create permission, the reach of a read grant over user-scoped memory, and the re-evaluation bound on an open stream.

Out of scope: F-10.2.33, F-10.2.34, F-15.1.64, and F-15.2.30 are routed elsewhere. F-10.2.35 is a separate proposal, and it interacts with gap 4 as stated below. F-12.9.23 touches the same read rule and stays separately owned; this proposal must not contradict it.

## Evidence

Each item below was verified against the tree at the worktree's base unless marked otherwise.

- `verified` Gap 1: `pkg/gateway/sessionserver/service.go:180` and `:257` call `s.guardTenant(s.handleCreate)` and `s.guardTenant(s.handleCreateAndStart)`. `pkg/gateway/sessionserver/route_table.go:26-28`, `:42`, and `:52` wrap the REST routes in `manage`. `pkg/gateway/mcpfabric/mcptools/mcptools_register.go:177` and `cmd/lenny-gateway/credentialsurface.go:292` are the only production callers of the two services. `pkg/auth/auth.go:218-223` gives `tenant-viewer` no `PermManageOwnSessions` and `billing-viewer` only `PermViewUsage`.
- `verified` Gap 1 exception: `lenny/create_and_start_session` dispatches `POST /v1/sessions/start` through `ServiceCall` (`pkg/gateway/mcpfabric/mcptools/client_tools.go:98-108`), and `serviceMux` is `Handler()` (`pkg/gateway/sessionserver/service.go:59-64`).
- `verified` Gap 2: `lookupCallerSession` checks the subject and `SessionAccessible` only (`pkg/gateway/mcpfabric/mcptools/mcptools.go:198-224`). `SessionAccessible` grants an owner with no permission check (`pkg/gateway/sessionserver/rbac_gate.go:152-164`). The `attach_session` `Authorize` closure is at `cmd/lenny-gateway/mcpsurface.go:389-414`.
- `verified` Gap 3: `warmEligible` compares `row.UserID` with `t.subject` and reads no role (`pkg/gateway/environment/translator/open_responses.go:482-492`). The forward runs at `:385-390`. §15 warm condition 4 states ownership only (`spec/15_external-api-surface.md:652-653`).
- `verified` Gap 4: `pkg/gateway/sessionserver/rbac_gate.go:86-106`. The same function admits a request with no principal, and §10.2 rule 3 refuses such a request with `401` afterwards, so that branch conforms when the ownership step runs. A gate placed on a new surface must keep that ordering.
- `verified` Gap 5: `WithSessionPrincipal` sets `Subject`, `TenantID`, `SessionID`, and `CallerType` only (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:131-158`). `AgentCallerType` is defined at `:125-129`. An exact search of `spec/` for `in-pod (agent|principal|session|caller)` returned no hits.
- `verified` Gap 6: `pkg/gateway/environment/translator/open_responses.go:376-394` and `:472-479`, where the `warmEligible` comment states the cold continuation. `spec/15_external-api-surface.md:657-667` states that every other continuation is served cold. `spec/10_gateway-internals.md:326` (rule 5) counts deriving as an act.
- `verified` Gap 7: `pkg/gateway/mcpfabric/mcptools/mcptools.go:1416-1426` queries with `TenantID` and `UserID`. `pkg/gateway/sessionserver/memory.go:141-145` adds `SessionID: id`, and both stores narrow on it (`pkg/gateway/session/memorystore/memorystore.go:453-464`, `pkg/gateway/session/memorystore/pgstore/pgstore.go:332-345`). A search of `spec/` and `docs/` for the route returned no hits. `spec/09_mcp-integration.md:203` states that memories are user-scoped.
- `verified` Gap 8: `pkg/gateway/sessionserver/route_table.go:86` and `:90` mount `/events` and `/logs` through `readID`. The read loops at `pkg/gateway/sessionserver/events.go:169-182` and `pkg/gateway/sessionserver/logs.go:141-186` re-evaluate nothing. The attach loops are in `pkg/gateway/mcpfabric/mcp/stream.go` and `websocket_attach.go`, which authorize once. `pkg/gateway/middleware/auth/auth.go:605-615` sets `p.Roles` per request. `spec/27_web-playground.md:149` is the §27.3.1 text.
- `verified` Spec coverage: `spec/10_gateway-internals.md:324`, `:327`, and `:328` are rules 3, 6, and 7. `spec/10_gateway-internals.md:290` states that `tenant-viewer` cannot create. `spec/15_external-api-surface.md:1503` and `:1511-1515` are §15.2.1 rules 1 and 5(c).
- `verified` Decision record: `gateway-runtime-comms-remediation.md:4522` records one proposal, owner-only memory recall, periodic stream re-evaluation with 60 seconds as an example bound, the matrix on every surface with an explicit in-pod exemption, and the statements that a reader cannot continue a conversation and a demoted owner cannot send warm turns. This is the plan's record of the decision, and the statement treats it as such.
- `verified` Records: F-10.2.31, F-10.2.32, F-10.2.36, and F-10.2.37 are OPEN in `BUILD-GAPS.md:15147`, `:15155`, `:15179`, and `:15186`. The status file of proposal 0110 reads `Implemented`.

## Who observes it

- A `tenant-viewer`, a `billing-viewer`, or a holder of a custom role without "Create / cancel own sessions" observes successful session creation through MCP and the adapters where REST answers `403`. Tenant administrators observe sessions, pod claims, and quota consumption attributed to a role §10.2 declares unable to create resources.
- An MCP client observes a different outcome from REST for the same refused operation.
- A tenant-admin who demotes a user observes that user continuing to drive a running held session through warm turns until the session ends.
- A deployer of a single-tenant installation whose IdP sends no role claim observes every user holding owner-level powers on that user's own sessions. The read bypass does not take this fall-through, so no other user's session is reached.
- Holders of `read_tenant_sessions` are the only observers of the memory recall reach.
- A non-owner reader who stays attached across a role change observes continued events.

## What breaks if nothing changes

- A read-only or billing role consumes tenant quota and runs runtimes, so the matrix states an authorization the deployment does not enforce. This is the highest-impact item.
- A `tenant-viewer` runs a runtime over another user's rehydrated transcript through a cold continuation.
- Demotion fails to contain a user with a running held session on the Open Responses surface.
- `lenny/memory_query` returns content whose source session the reader may no longer be able to read, because memories have no default TTL and outlive their sessions. This is a question of permission reach rather than a crossing of a role boundary, and the same reader already reads each of the owner's existing sessions.
- A stream outlives a role removal with no time bound other than disconnect. BUILD-GAPS rates this Low, and the reach is limited to a reader attached across a role change.
- `sessionPermissionGranted` keeps a tenancy-dependent admission no specification text describes, and any new gate that reuses it inherits that admission.

## Findings this unblocks

- Closing this proposal discharges F-10.2.31, F-10.2.32, F-10.2.36, and F-10.2.37.
- F-10.2.35 must be coordinated with the role-less decision. A dev-header principal carries no roles unless `AllowDevRoles` is set (`pkg/gateway/middleware/auth/auth.go:716-722`), so refusing role-less principals in every tenancy mode also refuses the dev path that finding repairs.

## Prior art considered

- Proposal 0110 is implemented and built the ownership check, `SessionAccessible`, `lookupCallerSession`, and the ordering in rule 7. It explicitly left F-10.2.31, F-10.2.32, F-10.2.36, and F-10.2.37 open, and BUILD-GAPS states that its implementation does not discharge them. Across `proposals/`, these findings appear only in 0110 and 0113. No landed or staged proposal covers this work.
- Reusable enforcement exists. `requireSessionPermission` and `sessionPermissionGranted` take an `http.Request`, and `principalHolds` is the context-based predicate. `ServiceCall` tools already pass the gate. `lookupCallerSession` already receives `act` and is the single site for most direct-store tools. The shared `callercheck.Check` used by `user_gate.go` and `mcptools.go` is a precedent for one check on both REST and MCP.
- `ToolCallGate` (`requirePrincipalTenant`) is the single `tools/call` choke point, and it receives no tool name, so a read-versus-act check cannot live there as it stands.
- `platformtools.AgentCallerType` is an existing discriminator for the in-pod principal, already branched on in `pkg/gateway/sessionserver/holder_forward.go`. No new marker is needed.
- The WebSocket revocation watch (`pkg/gateway/mcpfabric/mcp/websocket.go`, `startRevocationWatch`) polls per connection and closes with code 4401. It covers playground bearers only, and it is prior art for a periodic per-connection check. No spec text states a re-evaluation bound for a stream. The nearest "re-evaluation" language, in §8, concerns delegation and is unrelated.
- F-12.9.23 (T4 "explicit grant" against the §10.2 reader rows) touches the same read rule and stays separate.

## Validated premises

Verdicts: all six lenses returned `revise`. The verified outcome of each premise follows.

**Refuted or corrected**

- REFUTED, gap 5 of the original statement (REST half): `GET /v1/sessions/{id}/memory` does not return user-wide memories. It sets `MemoryScope.SessionID`, and both stores filter on it. F-10.2.36's own evidence names only the MCP tool, and the REST claim entered through its suggested resolution. Reported by the premise, evidence, prior-art, impact, and alternatives lenses, and verified directly. The scope lens noted the same difference. The owner-only decision can still reach the route, but as a restriction on a session-scoped read rather than as a fix of a user-wide leak.
- REFUTED, closing sentence of the original statement: "the spec does not state how the §10.2 matrix applies to the MCP tool surface [or] the built-in adapters" overstates the silence. Rules 3, 6, and 7 and §15.2.1 rules 1 and 5(c) apply it. Reported by the premise, evidence, prior-art, and alternatives lenses. The impact lens's revision kept the sentence, and its finding that the spec silence "is correct" holds for the in-pod principal only, so the repository evidence overrides that sentence. One limit applies: §15.2.1 rule 5 is a contract-test obligation, and rule 7 is the direct behavioral statement.
- REFUTED, implicit premise of gap 6 of the original statement: re-running `SessionAccessible` on the stream's context does not observe a role removal, because roles are fixed per request in the auth middleware. Reported by the premise, evidence, scope, impact, and alternatives lenses, and verified.
- CORRECTED, gap 6 of the original statement: the check-at-open behaviour is not limited to `attach_session`. REST `/events` has it, as all those lenses reported, and the premise lens added `/logs`. Verified in `logs.go`. §27.3.1 needs reconciling, per the alternatives lens.
- CORRECTED, gap 1: not every MCP create is ungated, because `lenny/create_and_start_session` is gated (premise lens). The observer set is wider than `tenant-viewer`, and chat completions reach the same binder (impact lens).
- CORRECTED, findings list: F-10.2.31 was cited with no gap stating it. All six lenses flagged this. It is now gap 4.
- CORRECTED, in-pod premise: the in-pod treatment must cover `ServiceCall`-backed tools, which already refuse the in-pod principal in multi-tenant mode (premise lens). Verified through `serviceMux`, which is `Handler()`.
- CORRECTED, gap 3 of the original statement: it is two problems. The `tenant-viewer` half follows from gap 1 (premise, evidence, alternatives, impact, and scope lenses). The half for a reader holding the create permission is sanctioned by §15 today (evidence, scope, and impact lenses).

**Disagreement resolved**

- The lenses split on whether "a reader cannot continue" means a matrix fix. The alternatives lens said to target callers lacking the create permission, and the scope lens said the decision requires a §15 and rule 5 change. The repository settles the mechanics: a create gate refuses only the caller lacking the create permission, and §15 serves the remaining reader cold. The statement therefore keeps both halves and records the wider reading as requiring a §15 edit. Which reading the plan's sentence means is left open below.
- The premise lens listed the principal-less admission at the gate alongside the role-less defect. Rule 3 anticipates a principal that "the permission matrix admits" and refuses it with `401`, so that branch conforms. Only the role-less branch is a defect.
- The alternatives lens named gap 6 as the best candidate to defer, and the scope lens named it the clean cut if convergence stalls, while keeping it in by default. The plan records one proposal, so it stays in as Group C.

**Confirmed**

- CONFIRMED, gap 1 (premise, evidence, scope, impact, and alternatives lenses): no other `PermManageOwnSessions` check exists on these paths. Production enforcement of that constant is confined to `route_table.go`.
- CONFIRMED, gap 2 (premise, evidence, scope, and alternatives lenses): `lookupCallerSession` is the chokepoint, and `attach_session` is a separate site. The impact lens added that this gap is mainly a divergence of outcomes, and that most of its exposure falls away once gap 1 closes, because a viewer owns a session only by demotion or through gap 1.
- CONFIRMED, gap 4 of the original statement, now gap 3 (premise, evidence, scope, impact, and alternatives lenses): it needs a seam of its own, the check must precede the forward, and a failed check falling through to cold needs the create gate to refuse it.
- CONFIRMED, memory recall on MCP (premise, evidence, prior-art, and impact lenses): it is user-wide, the impact is a question of permission reach, and severity is Medium at most. The in-pod path is unaffected by an owner-only rule, because the in-pod subject is the owner (scope and alternatives lenses). The alternatives lens noted that no role boundary is crossed, and that the "no fix" option is closed by the recorded decision rather than by the defect.
- CONFIRMED, scope (scope lens): one proposal, because the decisions land in one rule block and split proposals would stage conflicting edits. Groups A and B must not be separated, because cold continuation straddles them.
- CONFIRMED, prior art (prior-art lens): nothing retires the problem. 0110 deliberately left these findings open.
- CONFIRMED, ordering (premise lens): rule 7 places the matrix ahead of ownership, and the 0110 route wrappers do the same.
- CONFIRMED, boundary (scope lens): F-10.2.35 interacts with the role-less decision through the dev-header principal.
