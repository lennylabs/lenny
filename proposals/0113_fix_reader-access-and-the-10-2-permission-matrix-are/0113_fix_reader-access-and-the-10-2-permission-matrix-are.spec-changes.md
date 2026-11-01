# Spec changes — Enforce the §10.2 permission matrix on every session surface and bound reader reach and stream access

## Design (as the spec must state it)

§10.2 **Session ownership.** holds every access rule this proposal adds. SPEC-1 is the home of each rule below, and every other staged site cites it.

- Rule 4 states the role-less principal and the reach of a read grant over memory.
- Rule 6 extends coverage to the Open Responses surface, which makes a continuation an act under rule 5's general clause.
- Rule 7 maps a matrix refusal onto each surface.
- Rule 8 states the in-pod principal.
- Rule 9 states the stream re-evaluation, its interval, and its close.

§15 places the matrix check and the owner test ahead of the warm conditions, so conditions 1 to 4, the reservation text, and §29.3 stay verbatim. §9.4 states how the two recall surfaces differ. §27.3.1, §10.1.3, and §12.3 record the observable effect of rule 9. §28.5.3, §9.1, and §26.2 name the tool rule 8's bound must keep reachable, and §26.2 is the home of the fact that the `lenny/vcs_token` result reaches the pod. §14, §4.3, §4.7.11, §4.9, §5.1, §9.3, §13.3, and §23 narrow to that fact or cite it. §15.2 **Event-stream resume.** owns `params.seq`, the cursor a WebSocket client presents after a rule 9 close.

## Edge cases and accepted failure modes

- **Claim-only role.** A role conveyed only by an OIDC claim changes with a new token, so an open stream does not observe it. Rule 9 owns this limit.
- **Store failure during a re-evaluation.** The stream closes for every principal, and the reattach fails until the read succeeds. Rule 9, SPEC-6, and SPEC-7 own this case.
- **Drops during a re-evaluation.** A stream can close after a passing re-evaluation with no event lost. Rule 9 owns this case.
- **Cursor resume after a close.** A resume can report a gap, because the §10.4 **Event replay buffer.** is bounded. Rule 9 therefore promises evaluation of the reconnect rather than a lossless resume.
- **Empty-subject principal.** Rule 3 gives `401` only to a request the matrix admits, so a refused principal with an empty subject receives the matrix refusal.
- **Dev request with no user header.** A dev-mode request with no `X-Lenny-User-ID` names no principal, so rule 3's `401` applies to it.
- **Token Service bearers.** A `POST /v1/oauth/token` bearer whose subject has no platform-managed mapping is a role-less principal under rule 4.
- **Session end past the dual-store bound.** §10.1.3 item 4 governs whether the session still runs when a client reattaches.

## Staged edits

### SPEC-1 · spec/10_gateway-internals.md § 10.2 Authentication, Authorization and RBAC

Every edit targets the **Session ownership.** rules or **Role assignment:**. Each rule stays one physical line. Rule 5 is unedited.

**Edit (a), rule 4.** Replace the sentence "A principal with no assigned role reads only its own sessions." with:

```markdown
Apart from the in-pod principal of rule 8, a principal with no assigned role holds no permission in the matrix, in every tenancy mode, and rule 7 gives it the matrix refusal.
```

**Edit (b), rule 4.** Append to the end of the rule, after "through **Role assignment:** below.":

```markdown
Memory recall through `GET /v1/sessions/{id}/memory`, and through `lenny/memory_query` on the session the recall runs under, is limited to that session's owner. The gateway checks it against "Read own session history". A read grant over another user's session does not extend to recall, so any other caller receives the response rule 7 gives for a session that does not exist.
```

**Edit (c), rule 6.** Append to the end of the rule, after "which lists only the sessions the caller may read.":

```markdown
They also cover `GET /v1/responses/{id}` and a `POST /v1/responses` that carries `previous_response_id`, on the session the id names.
```

**Edit (d), rule 7.** Insert after the first sentence, which ends "and a caller the matrix refuses receives its refusal.":

```markdown
A refused caller receives `403 FORBIDDEN` on REST, the `FORBIDDEN` tool error on MCP, and the native `403` on a built-in adapter, on every session create as on a covered surface.
```

**Edit (e), rules 8 and 9.** Append after rule 7, as the next items of the same numbered list:

```markdown
8. A platform tool call from a session's pod runs under that session's in-pod principal, whose subject is the session's owner and which carries no role. The gateway builds this principal only for a call that arrives over `LNK-GWCONTROL` or that another replica forwards ([Section 29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 3). No credential yields it, so a bearer with `caller_type: agent` is an ordinary principal under these rules. For a pod's platform MCP server, the gateway lists and dispatches only the tools the `CH-MCP-PLATFORM` card in [Section 28.5.3](28_communication-channels.md#2853-intra-pod) names, and a platform tool call naming any other tool fails as an unknown tool. The matrix admits the in-pod principal to "Create / cancel own sessions" and "Read own session history" and to no other permission. The other rules apply to it as to any principal.
9. A long-lived read stream re-evaluates access on a 60-second interval while it is open. These streams are `attach_session` over SSE and over WebSocket, the SSE form of `GET /v1/sessions/{id}/events`, and the SSE form of `GET /v1/sessions/{id}/logs`. A re-evaluation reads the principal's current roles under **Role assignment:** below, checks "Read own session history", and applies rule 4, for an owner as for a reader. A role conveyed only by an OIDC claim changes when the client presents a new token, so a re-evaluation does not observe that change. When a re-evaluation refuses or cannot complete, the gateway closes the stream: an SSE response ends with no terminal frame, and a WebSocket connection closes with standard code `1008` and close reason `session_access_lost`. When a re-evaluation passes and events may have been dropped while it ran, the gateway closes the stream the same way, except that a WebSocket connection closes with standard code `1013` and no close reason. A client reconnects with its resume cursor, and the gateway evaluates the reconnect as a new request.
```

**Edit (f), Role assignment.** Append to the end of the **Role assignment:** paragraph, after "Custom roles are assignable through the same mechanisms as built-in roles.":

```markdown
In dev mode, a request carrying the `X-Lenny-User-ID` header and no `X-Lenny-Roles` header, and the `dev` playground principal, hold the built-in `user` role.
```

### SPEC-2 · spec/15_external-api-surface.md § Open Responses conversation sessions

The warm conditions, the reservation text, "Every other continuation is served cold", and §29.3 stay verbatim.

**Edit (a).** Append to the end of the paragraph beginning "**Open Responses conversation sessions.** An `OpenResponsesAdapter` request", after "and the adapter adds no timer of its own.":

```markdown
Every such request and every continuation creates or acts on a session, so the gateway checks "Create / cancel own sessions" under [Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.** rule 7 before it resolves `previous_response_id`. A refused caller receives the native `403`, and no forward or reservation occurs.
```

**Edit (b).** In the paragraph beginning "Each turn produces one response", insert after the sentence ending "returns a native `404`.":

```markdown
A caller refused by the "Read own session history" check of §10.2 **Session ownership.** rule 7 receives the native `403` ahead of that resolution.
```

**Edit (c).** In the paragraph beginning "On a continuation the gateway resolves `previous_response_id`", insert after "with no session created, no rehydration, and no dispatch.":

```markdown
An id naming a response of a session the caller does not own also receives the native `404`, with the same absence of effects, even when the caller may read that response through `GET /v1/responses/{id}`, because a continuation acts on the session (§10.2 **Session ownership.** rule 5).
```

### SPEC-3 · spec/09_mcp-integration.md § 9.4 Memory Store

Insert after the sentence "Accessed via `lenny/memory_write` and `lenny/memory_query` tools on platform MCP server.":

```markdown
Only the owner of the session a recall runs under recalls memories ([Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.** rule 4). `lenny/memory_query` recalls across all of the owner's sessions, and `GET /v1/sessions/{id}/memory` ([Section 15.1](15_external-api-surface.md#151-rest-api)) returns the memories written under the session the path names.
```

### SPEC-4 · spec/15_external-api-surface.md § 15.1 REST API, Artifacts and introspection

In the **Artifacts and introspection:** table, add this row after the `/v1/sessions/{id}/logs` row:

```markdown
| `GET`  | `/v1/sessions/{id}/memory`           | Query memories written under this session. See [Section 9.4](09_mcp-integration.md#94-memory-store).                                 |
```

### SPEC-5 · spec/27_web-playground.md § 27.3.1 OIDC cookie-to-MCP-bearer exchange, failure-modes summary

Add this row to the failure-modes summary table, after the row "Bearer presented on WebSocket after revocation". The **Silent refresh.** paragraph stays verbatim.

```markdown
| Attached stream closed by a [§10.2](10_gateway-internals.md#102-authentication) **Session ownership.** rule 9 re-evaluation | WebSocket close code `1008` (close reason `session_access_lost`), or `1013` when the re-evaluation passed | none | Open a new connection and attach; the attach answers under rule 7, or fails while the store is unavailable |
```

### SPEC-6 · spec/10_gateway-internals.md § 10.1.3 Dual-Store Unavailability

Append to the end of item 5, after "in-progress sessions are unaffected while the degraded mode holds.":

```markdown
An open read stream closes when its access re-evaluation cannot read Postgres ([Section 10.2](#102-authentication) **Session ownership.** rule 9), so an attached client can lose its stream during this mode and cannot reattach until Postgres returns. Closing a stream does not affect the session. Item 4 governs whether the session is still running when the client reattaches with its cursor.
```

### SPEC-7 · spec/12_storage-architecture.md § 12.3 Postgres HA Requirements

Append to the end of the **Behavior during Postgres failover:** paragraph, after "are rejected or queued as specified below.":

```markdown
An open read stream whose access re-evaluation cannot read Postgres in this window closes ([Section 10.2](10_gateway-internals.md#102-authentication) **Session ownership.** rule 9), and the client reattaches with its cursor once the read succeeds.
```

### SPEC-8 · spec/28_communication-channels.md § 28.5.3 Intra-pod, spec/09_mcp-integration.md § Platform MCP Server Tools and § OAuth/OIDC Flow, spec/26_reference-runtime-catalog.md § 26.2 Shared Patterns for Coding-Agent Runtimes, spec/14_workspace-plan-schema.md § 14 Workspace Plan Schema, spec/04_system-components.md § 4.3 Token Service, § 4.7.11 Adapter-Agent Security Boundary, and § Security Boundaries, spec/05_runtime-registry-and-pool-model.md § Standalone Runtime, spec/13_security-model.md § 13.3 Credential Flow, and spec/23_competitive-landscape.md § Additional Platform Capabilities

**Edit (a), §28.5.3.** On the `CH-MCP-PLATFORM` card, in **Messages.**, insert after the sentence ending "`lenny/get_task_tree`, and `lenny/set_tracing_context` ([§9.1](09_mcp-integration.md#91-where-mcp-is-used)).":

```markdown
The server also carries `lenny/vcs_token`, which the in-pod `git` credential helper calls ([§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes)) and which no runtime SDK wraps.
```

**Edit (b), §9.1.** In the **Platform MCP Server Tools** table, add this row after the `lenny/set_tracing_context` row:

```markdown
| `lenny/vcs_token`           | Return a VCS token to the in-pod `git` credential helper | [§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes)    |
```

**Edit (c), §26.2.** In the `vcs.<provider>.read` / `vcs.<provider>.write` bullet of **Credential-lease scopes (shared).**, replace "The runtime never sees the raw token; `git` inside the pod uses an HTTPS credential helper that calls the gateway's token endpoint." with:

```markdown
`git` inside the pod uses an HTTPS credential helper that obtains the token by calling `lenny/vcs_token` on the platform MCP server ([§9.1](09_mcp-integration.md#91-where-mcp-is-used)). The token therefore reaches the pod over the platform MCP socket.
```

**Edit (d), §14 `gitClone` source row.** In the Purpose cell, delete ", so the runtime never sees raw credentials", so the cell ends "The gateway performs the clone using its network path, not the pod's." The row stays one physical line.

**Edit (e), §14 **`gitClone.auth` object.**** Replace "uses an in-pod HTTPS credential helper that calls the gateway's token endpoint for any subsequent operations (pull, push)" with:

```markdown
uses the in-pod HTTPS credential helper ([§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes)) for any subsequent operations (pull, push)
```

**Edit (f), §4.7.11 item 4.** Replace the label "**No credential material over socket:**" with "**No LLM credential material over socket:**". Replace "Credentials are never passed via environment variables (readable via `/proc`, persist in crash dumps, often captured by framework logging), Unix sockets, or MCP servers. The adapter never sends credential material to the agent post-startup." with:

```markdown
Credentials are never passed via environment variables (readable via `/proc`, persist in crash dumps, often captured by framework logging). LLM credentials are never passed via Unix sockets or MCP servers, and the adapter never sends LLM credential material to the agent post-startup.
```

**Edit (g), §4.7.11 opening.** Replace "extract credentials, or manipulate the adapter" with:

```markdown
extract credentials other than the credential files the agent UID can read ([§13.1](13_security-model.md#131-pod-security)) and the VCS token of [§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes), or manipulate the adapter
```

**Edit (h), §4.3 **Key rules:**.** Replace "Pods never hold downstream OAuth tokens — the Token Service does" with:

```markdown
Pods never hold connector OAuth tokens — the Token Service does. The `lenny/vcs_token` result reaches the pod as [§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) states
```

**Edit (i), §4.9 **Security Boundaries**.** At the end of the bullet opening "Pods receive **materialized short-lived credentials**", after "— never via environment variables", append:

```markdown
. The `lenny/vcs_token` result reaches the pod outside this path ([§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes))
```

**Edit (j), §9.3 **Key invariants:**.** Replace "Tokens never transit through pods. The gateway owns all downstream credential state." with:

```markdown
Connector tokens never transit through pods. The gateway owns all connector credential state.
```

**Edit (k), §13.3 **Connector credentials (OAuth):**.** In the code block, replace "Gateway holds all downstream OAuth tokens" with "Gateway holds all connector OAuth tokens", and replace "Pod never receives: client tokens, downstream OAuth tokens" with "Pod never receives: client tokens, connector OAuth tokens".

**Edit (l), §13.3 **Key distinction:**.** Insert after the sentence ending "for details on both modes.":

```markdown
The `lenny/vcs_token` result follows neither path and reaches the pod as [§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes) states.
```

**Edit (m), §23 **Additional Platform Capabilities** item 9.** Replace "on behalf of pods — pods never see raw credentials." with:

```markdown
on behalf of pods, so a proxy-mode pod never sees the provider key.
```

**Edit (n), §5.1 **`env`**.** Replace "are not a channel for secrets, which reach the runtime through its credential file ([Section 4.7](04_system-components.md#47-runtime-adapter))." with:

```markdown
are not a channel for secrets. LLM credentials reach the runtime through its credential file ([Section 4.7](04_system-components.md#47-runtime-adapter)).
```

### SPEC-9 · spec/15_external-api-surface.md § 15.2 MCP API

**Edit (a).** In the **Event-stream resume.** paragraph, insert after the sentence ending "parallel to the ops-event stream in [Section 25.5](25_agent-operability.md#255-operational-event-stream).":

```markdown
Each such frame also carries its `SeqNum` as `params.seq` on both the Streamable HTTP and the WebSocket transports, and that field is the cursor of a WebSocket client, which receives no `id:` line.
```

**Edit (b).** In **Cross-reference and replay.**, delete the parenthetical " (SSE `id:` line, `resumeFromSeq`, `gap_detected`)", so the sentence reads "The [Event-stream resume](#152-mcp-api) contract applies uniformly to every frame in the table regardless of method name".

**Edit (c).** In the table under **Per-kind MCP wire projection.**, replace the projection cell and the method cell of the rows below. Each row stays one physical line, and the Notes cells stay verbatim.

In the `SessionEventOutput` row, whose projection cell reads "MCP streaming content block(s) appended to the `attach_session` task response.", the two cells become:

```markdown
| MCP task status notification with `status: working`, carrying the streaming content block(s) in `params.content`. | `notifications/tasks/statusUpdate` |
```

In the `SessionEventTerminated` row, whose projection cell opens "MCP task completion frame on the `attach_session` task,", the two cells become:

```markdown
| MCP Tasks final-state frame: a task status notification with `params.final: true`, with `status` mapped from `TerminationCode` through the [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema) protocol mapping and the `TerminationReason.Detail` surfaced in `params.metadata.terminationDetail`. | `notifications/tasks/statusUpdate` |
```

## Spec files touched

- `spec/04_system-components.md`
- `spec/05_runtime-registry-and-pool-model.md`
- `spec/09_mcp-integration.md`
- `spec/10_gateway-internals.md`
- `spec/12_storage-architecture.md`
- `spec/13_security-model.md`
- `spec/14_workspace-plan-schema.md`
- `spec/15_external-api-surface.md`
- `spec/23_competitive-landscape.md`
- `spec/26_reference-runtime-catalog.md`
- `spec/27_web-playground.md`
- `spec/28_communication-channels.md`
