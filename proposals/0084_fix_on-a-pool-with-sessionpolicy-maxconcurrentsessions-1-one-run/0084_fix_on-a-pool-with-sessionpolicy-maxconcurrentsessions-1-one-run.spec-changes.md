# Spec changes: Intra-pod MCP and direct-mode usage cannot attribute calls on concurrent pools

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The pod's intra-pod MCP servers stay pod-wide and started at most once per pod, and the manifest nonce stays the only credential on a connection. Two facts change.

First, a connection may name the session it acts for. The name travels in the optional top-level `_lennySessionId` field of the `initialize` request's `params` object, beside `_lennyNonce`, and the adapter validates and strips it with the nonce. The adapter attributes each `tools/list` and `tools/call` under the numbered rules of the §15.4.3 **Session address.** paragraph (SPEC-1). That paragraph is the one normative statement of the rule. §4.7.11, §28.5.3, §28.6, §28.8, §29.2, §29.10, and §5.2 cite it.

Second, the manifest's `mcpNonce` and `connectorServers` describe the pod's servers rather than the writing session. The §4.7.6 `mcpNonce` row is the home of the nonce's lifetime and the §4.7.6 `connectorServers` row is the home of the served connector set (SPEC-2). A runtime that opens a later session's connections therefore finds a nonce and sockets that running servers serve.

Direct-mode token attribution follows the same address-and-confirm pattern on one frame. `llm_request_completed` gains an optional `sessionId`, and the `CH-RUNTIMEOPS` message-schema table row is the home of its attribution rule (SPEC-4). `llm_request_started` takes no field.

The address is a runtime assertion. On the intra-pod MCP surface any process that has read the manifest can name any session the pod holds, which is a new within-tenant property. §5.2 adds it to the concurrent-sessions acknowledgment (SPEC-6). The direct-mode charging property is stated in §11.2 (SPEC-4), because `CH-RUNTIMEOPS` is one runtime-held connection.

## Edge cases and accepted failure modes

- **A named session that is not bound on the pod.** The adapter refuses the request under rule 1 of the §15.4.3 **Session address.** paragraph. This covers a session never held on the pod and a session already released. A connection opened for a session outlives that session, and its later requests are refused.
- **A named session that is bound, but belongs to a co-resident user.** The adapter forwards the request under that session. This is the accepted exposure SPEC-6 adds to the §5.2 acknowledgment.
- **A request admitted immediately before its session's release returns.** Rule 1 confirms the binding when the request enters the adapter. A request admitted before the release can reach the gateway after the release returns. The gateway-side membership check of a forwarded session (proposal 0070 §5) is the backstop.
- **An unaddressed connection on a process that has been given a second session.** Rule 2 refuses it until the process serves no session. The first-party runtime SDKs open unaddressed connections, so their platform and connector tool calls are refused while sessions overlap on a concurrent pod.
- **A `connectorServers` entry outside the reading session's policy.** The gateway refuses `tools/list` and `tools/call` on it for that session. The §4.7.6 `connectorServers` row states that a runtime treats the refusal as the connector being unavailable, and the §15.4.6 reachability row tests it.
- **`llm_request_completed` naming a session that is not bound, or naming none while the process has been given more than one session.** The adapter decrements the in-flight counter and drops the counts. The counts are lost, and the §11.2 `zero_delta` counter can still fire for a runtime that omits `sessionId` on an overlapped pod.
- **A `type: mcp` runtime.** The adapter arms no intra-pod MCP server. The manifest still carries an `mcpNonce`, which authenticates nothing, and `connectorServers` is empty.

## Staged edits

Several target paragraphs are wrapped across lines in the source. Each "replace" block below quotes the live text as it wraps. Replace only the quoted span, and leave every line outside it as it stands. The tier-11 gate `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` locates each nonce statement by the first line of an anchor sentence, so rewrapping the line that carries an anchor breaks the gate. Each edit below says where this applies.

### SPEC-1 · spec/15_external-api-surface.md § 15.4.3 Runtime Integration Levels, § 15.4.4 Sample Echo Runtime, § 15.4.6 Conformance Test Suite, § 15.7 Runtime Author SDKs

**SPEC-1a. §15.4.3, the `**Authentication.**` bullet.** In the bullet that begins "- **Authentication.** Intra-pod MCP connections require a manifest-nonce handshake", replace

```markdown
The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string, rewritten by each session's start alongside the rest of the manifest). The nonce a server validates against is the one the manifest carried at the start that bound that server, and a later session's manifest write does not re-arm a running server. Because the intra-pod MCP servers are pod-wide rather than scoped to a session, the adapter resolves the calling session at call time and refuses a `tools/list` or `tools/call` unless the pod's shared runtime process has been given exactly one session and that session is the caller.
```

with

```markdown
The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string whose lifetime the `mcpNonce` field of [Section 4.7](04_system-components.md#47-runtime-adapter) states). The nonce a server validates against is the one the manifest carried at the start that bound that server, and a later session's manifest write does not re-arm a running server.

  **Session address.** The nonce does not name a session. A connection may name the session it acts for in the top-level `_lennySessionId` field of its `initialize` request's `params` object (see the nonce wire format below). The adapter attributes each `tools/list` and `tools/call` on a connection to a session under these rules, applied when the request enters the adapter:

  1. On a connection that names a session, the adapter forwards the request under that session when its slot registry holds an entry bound to that session, and refuses the request otherwise.
  2. On a connection that names no session, the adapter forwards the request under the single session the pod's shared runtime process has been given since it last served none, and refuses the request while that process has been given more than one session since it last served none.

  The connections a runtime opens at startup, before the adapter delivers any `CH-MSGSOCK` frame, name no session, and rule 2 governs them. To keep its tools once the process is given a second session, a runtime opens a further set of intra-pod MCP connections for each session, the first session included, when that session's first `message` arrives. It names the session on each connection of the set, taking the identifier from the `sessionId` of that session's `CH-MSGSOCK` frames, and re-reads the manifest's `mcpNonce` and `connectorServers` before it connects. The named session is asserted by whichever process opened the connection. The adapter confirms only that the session is bound on the pod, so any process that has read the manifest can name any session the pod holds ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

The first paragraph of the replacement continues the bullet's first line, so the bullet keeps "started at most once per pod", "carried at the start that bound", and "does not re-arm" in the block the gate reads. The **Session address.** paragraph and its list are continuation blocks of the same list item, indented two spaces, placed before the `**Nonce wire format (v1 — intra-pod only).**` paragraph.

**SPEC-1b. §15.4.3, the `**Nonce wire format (v1 — intra-pod only).**` paragraph.** Leave the JSON example and the sentence that begins "The adapter validates the `_lennyNonce` value before processing any tool dispatch" unchanged. In the paragraph after the JSON example, replace

```markdown
After successful validation, the adapter **strips** the `_lennyNonce` field from `params` before dispatching the `initialize` request to its internal MCP server implementation, ensuring the MCP server never sees the non-standard field. This stripping is required because the adapter's MCP server validates `initialize` params against the MCP schema, which does not include `_lennyNonce`.
```

with

```markdown
A connection that names a session carries the session identifier as a string in the top-level `_lennySessionId` field of the same `params` object. The field is optional. The adapter closes, before any tool dispatch, a connection whose `_lennySessionId` is present and is not a non-empty string. After successful validation, the adapter **strips** the `_lennyNonce` field and any `_lennySessionId` field from `params` before dispatching the `initialize` request to its internal MCP server implementation, ensuring the MCP server never sees a non-standard field. This stripping is required because the adapter's MCP server validates `initialize` params against the MCP schema, which includes neither field.
```

**SPEC-1c. §15.4.3, the `**Strict MCP client libraries.**` note.** Replace the note

```markdown
  > **Strict MCP client libraries.** Some MCP client libraries enforce schema validation on outgoing requests and may reject the `_lennyNonce` field in `params`. Runtime authors using such libraries should either (a) add `_lennyNonce` to the `initialize` params after the library constructs the request but before it is serialized to the socket, or (b) disable outbound schema validation for the `initialize` call only. The adapter accepts the field regardless of its position relative to other `params` keys.
```

with

```markdown
  > **Strict MCP client libraries.** Some MCP client libraries enforce schema validation on outgoing requests and may reject the `_lennyNonce` and `_lennySessionId` fields in `params`. Runtime authors using such libraries should either (a) add the fields to the `initialize` params after the library constructs the request but before it is serialized to the socket, or (b) disable outbound schema validation for the `initialize` call only. The adapter accepts either field regardless of its position relative to other `params` keys.
```

Leave the `**Deprecated location.**` note and the `**Migration path — v2 out-of-band handshake.**` note unchanged.

**SPEC-1d. §15.4.4, the Standard-level pseudocode block** ("Pseudocode (Standard-level addition — nonce + MCP):"). Make three edits inside the block.

Replace the line

```
    nonce    = manifest.mcpNonce     // 256-bit hex string, regenerated each task
```

with

```
    nonce    = manifest.mcpNonce     // 256-bit hex string, fixed while the pod's MCP servers run
```

In the connector loop, after the line `        conn.recv()   // wait for initialize response`, insert

```
        // A connector the session's delegation policy does not permit answers tools/list
        // with an error. Treat it as unavailable to that session and keep the other connections.
```

In the main loop, after the line `                seq += 1` inside `case "message":`, insert

```
                // On a pool with maxConcurrentSessions > 1: on the first message for a msg.sessionId
                // this process has not served, re-read the manifest and open a connection set whose
                // initialize params carry "_lennySessionId": msg.sessionId; use that set for this
                // session's tool calls.
```

Leave the startup `initialize` objects without `_lennySessionId`, because `msg` is not in scope at startup. Leave the Full-level pseudocode block unchanged.

**SPEC-1e. §15.4.6, the conformance table row `connector MCP server reachability`.** Replace the row

```markdown
| **Standard** | **connector MCP server reachability** | If `manifest.connectorServers` is non-empty, the runtime connects to each with the same nonce and completes the `initialize` handshake. Test uses two fake connector servers. |
```

with

```markdown
| **Standard** | **connector MCP server reachability** | If `manifest.connectorServers` is non-empty, the runtime connects to each with the same nonce and completes the `initialize` handshake. A connector server that answers `tools/list` with an error is unavailable to the session, and the runtime keeps its platform connection and its other connector connections open. Test uses two fake connector servers, one of which answers `tools/list` with an error. |
```

The row stays one physical line.

**SPEC-1f. §15.7, the `**Intra-pod authentication.**` bullet.** At the end of the bullet, after "SDKs read the nonce from the manifest and attach it automatically.", append

```markdown
 An SDK names no session on its intra-pod MCP connections, so the adapter attributes their requests under rule 2 of the [§15.4.3](#1543-runtime-integration-levels) **Session address.** paragraph.
```

The appended sentence joins the bullet's existing line after one space.

### SPEC-2 · spec/04_system-components.md § 4.7.5 Adapter Manifest, § 4.7.6 Adapter Manifest Field Reference, § 4.7.11 Adapter-Agent Security Boundary

**SPEC-2a. §4.7.5, the `**Adapter manifest:**` paragraph.** Replace the sentence

```markdown
On a pod holding more than one bound session, a later start replaces the manifest's `sessionId`, `mcpNonce`, and `credentialsPath` while an earlier session's runtime is still processing.
```

with

```markdown
On a pod holding more than one bound session, a later start replaces the manifest's `sessionId` and `credentialsPath` while an earlier session's runtime is still processing. The `mcpNonce` and `connectorServers` fields describe the pod's intra-pod MCP servers rather than the writing session, and apply to every session the pod holds while those servers run.
```

Keep "One pod-global file" and "authoritative for the session whose start last wrote it" unchanged; the tier-11 gate pins both.

**SPEC-2b. §4.7.6, the field-table row `mcpNonce`.** Replace the row that begins "| `mcpNonce`" with

```markdown
| `mcpNonce`                  | `string`           | Yes      | Random 256-bit hex nonce that authenticates a connection to the pod's intra-pod MCP servers (platform MCP and all connector MCP servers), which are pod-wide and started at most once per pod. The start that arms those servers mints it, every manifest written while they run carries the same value, and a start that arms them again mints a fresh value. The nonce a server validates against is the one the manifest carried at the start that bound that server, and a later session's manifest write does not re-arm a running server. The nonce does not name a session ([Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The nonce must be presented as the first message of the MCP `initialize` handshake on every intra-pod MCP connection, and the adapter rejects connections that do not present a valid nonce. On a pod whose adapter runs no intra-pod MCP server (a `type: mcp` runtime), the value is present and authenticates nothing. | Standard, Full            |
```

The row stays one physical line and keeps the three pinned phrases.

**SPEC-2c. §4.7.6, the field-table row `connectorServers`.** Replace the row that begins "| `connectorServers`" with

```markdown
| `connectorServers`          | `array`            | Yes      | Per-connector MCP servers the pod serves: one entry for each connector server the adapter has opened since the pod's intra-pod MCP servers were armed, opened by the first start whose session's effective delegation policy permits that connector. While the servers run, a later manifest lists every entry an earlier one listed. An entry may name a connector the reading session's policy does not permit. The gateway refuses `tools/list` and `tools/call` on it for that session ([Section 9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)), and a runtime treats that refusal as the connector being unavailable to that session rather than as a failure of its other connections. Empty array `[]` if none. Never absent. | Standard, Full            |
```

The row stays one physical line.

**SPEC-2d. §4.7.6, the `**Level reading requirements:**` list, `**Standard**` bullet.** Replace

```markdown
- **Standard** — The runtime reads `platformMcpServer.socket`, `connectorServers`, and `mcpNonce` to connect to and authenticate with local MCP servers.
```

with

```markdown
- **Standard** — The runtime reads `platformMcpServer.socket`, `connectorServers`, and `mcpNonce` to connect to and authenticate with local MCP servers. On each per-session intra-pod MCP connection it opens, it names the session that connection acts for, taken from that session's `CH-MSGSOCK` frames ([Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).
```

**SPEC-2e. §4.7.11, item 1, the `**Nonce-only fallback: challenge-response supplement and escalation gate.**` paragraph.** Replace

```markdown
Because the manifest nonce is static for the lifetime of a task execution,
```

with

```markdown
Because the manifest nonce is static while the pod's intra-pod MCP servers run,
```

**SPEC-2f. §4.7.11, item 7 `**MCP server security:**`.** Replace the sentence

```markdown
The manifest nonce authenticates a connection to the pod's intra-pod MCP servers rather than scoping it to a session: the servers resolve the calling session at call time and refuse the call unless the pod's shared runtime process has been given exactly one session and that session is the caller.
```

with

```markdown
The manifest nonce authenticates a connection to the pod's intra-pod MCP servers and does not name a session; the servers attribute each `tools/list` and `tools/call` to a session under the session-address rule of [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels).
```

Leave "started at most once per pod", "the nonce the manifest carried at the start that bound the server it connects to", and "A later session's manifest write does not re-arm a running server." unchanged in the item.

### SPEC-3 · spec/28_communication-channels.md § 28.5.3 Intra-pod (`CH-MCP-PLATFORM`, `CH-MCP-CONNECTOR`), § 28.6 Exclusivity and concurrency model, § 28.8 Failure and degradation matrix

**SPEC-3a. `CH-MCP-PLATFORM`, `**Endpoint.**` bullet.** After the sentence that ends "and a later session's manifest write does not re-arm a running server ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), [§4.7](04_system-components.md#47-runtime-adapter))." append

```markdown
A connection may also name the session it acts for in the top-level `_lennySessionId` field of the same
  `params` object ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).
```

as the bullet's closing sentence, wrapped at the card's line width. Leave the line that carries "**Endpoint.** An abstract Unix socket whose name the adapter manifest advertises under" unwrapped; it is a gate anchor.

**SPEC-3b. `CH-MCP-PLATFORM`, `**Exclusivity.**` bullet.** Replace the span

```markdown
The adapter resolves the calling session to the single session the pod's shared runtime
  process has been given, and refuses the call unless that process has been given exactly one session and
  that session is the caller ([§4.7](04_system-components.md#47-runtime-adapter)).
```

with

```markdown
The adapter attributes each `tools/list` and `tools/call` to a session under the
  session-address rule of [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels).
```

Leave the line that carries "The manifest nonce authenticates a connection to the pod's intra-pod MCP servers, which" unwrapped; it is a gate anchor.

**SPEC-3c. `CH-MCP-CONNECTOR`, `**Endpoint.**` bullet.** Replace

```markdown
One abstract Unix socket per authorized connector,
```

with

```markdown
One abstract Unix socket per connector the pod serves
  ([§4.7](04_system-components.md#47-runtime-adapter), `connectorServers`),
```

and rewrap the bullet's remaining lines to the card's line width.

**SPEC-3d. `CH-MCP-CONNECTOR`, `**Messages.**` bullet.** Replace the span

```markdown
Each authorized connector in
  the session's delegation policy gets its own independent server,
```

with

```markdown
Each connector the pod serves
  gets its own independent pod-wide server,
```

**SPEC-3e. `CH-MCP-CONNECTOR`, `**Preconditions.**` bullet.** Make three replacements in the bullet. Replace

```markdown
which is empty when no connector is
  authorized and is never absent
```

with

```markdown
which is empty when the pod serves no
  connector and is never absent
```

Replace

```markdown
The set of authorized connectors is fixed by the
  session's `DelegationPolicy`,
```

with

```markdown
The connectors a session may call are fixed by
  that session's `DelegationPolicy`,
```

Replace

```markdown
against the calling pod's effective policy before proxying it
```

with

```markdown
against the effective policy of the session the request is attributed to before proxying it
```

**SPEC-3f. `CH-MCP-CONNECTOR`, `**Exclusivity.**` bullet.** Replace the span

```markdown
, and the adapter resolves the calling session to the
  single session the pod's shared runtime process has been given, refusing the call unless that process
  has been given exactly one session and that session is the caller
```

with

```markdown
, and the adapter attributes each request as it does on `CH-MCP-PLATFORM`
```

Leave the two lines that carry "The same manifest nonce that authenticates a connection to `CH-MCP-PLATFORM`" and "authenticates a connection here" wrapped exactly as they are; together they are a gate anchor.

**SPEC-3g. §28.6, the paragraph that begins "Others in that set carry a scoping constraint that is not exclusivity."** Replace the span

```markdown
; the adapter resolves the calling
session to the single session the pod's shared runtime process has been given and refuses the call unless
that process has been given exactly one session and that session is the caller
```

with

```markdown
; the adapter attributes each request
to a session under the session-address rule of
[§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)
```

The `([§4.7](04_system-components.md#47-runtime-adapter))` citation that follows stays.

**SPEC-3h. §28.8, the `CH-MCP-PLATFORM` row, exclusivity column.** Replace the span

```markdown
, and the adapter resolves the calling session to the single session the pod's shared runtime process has been given, refusing the call unless that process has been given exactly one session and that session is the caller
```

with

```markdown
, and the adapter attributes each request to a session under the session-address rule of [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)
```

The row stays one physical line. Leave the §28.8 `CH-MCP-CONNECTOR` row unchanged.

### SPEC-4 · spec/28_communication-channels.md § 28.5.3 Intra-pod (`CH-RUNTIMEOPS` message-schema table), spec/11_policy-and-controls.md § 11.2 Budgets and Quotas

**SPEC-4a. §28.5.3, `CH-RUNTIMEOPS` message-schema table, row `llm_request_completed`.** Replace the row that begins "| `llm_request_completed`" with

```markdown
  | `llm_request_completed`     | Runtime → Adapter  | `type`, `requestId` (string — matches the corresponding `llm_request_started`), `provider` (string), `status` (`"ok"` \| `"error"`), `inputTokens` (integer, optional), `outputTokens` (integer, optional), `sessionId` (string, optional; the session whose total the token counts join) | Runtime's outbound LLM request has completed or errored. Adapter decrements the in-flight counter for every frame. When the counter reaches zero and a credential rotation is pending, the adapter proceeds to send `credentials_rotated`. In direct mode the runtime SHOULD populate `inputTokens` and `outputTokens` from the completed provider response when it can extract them; the adapter accumulates them into a per-session cumulative total internally and reports the incremental delta since the last read over the [§4.7](04_system-components.md#47-runtime-adapter) `ReportUsage` RPC (see [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). A runtime that cannot extract counts omits both fields, and the session has no direct-mode token source. In direct mode the adapter folds the token counts into the total of the session `sessionId` names when the adapter's slot registry holds an entry bound to that session, and drops them otherwise. It folds the counts of a frame that names no session into the total of the single session the pod's shared runtime process has been given since it last served none, and drops them while that process has been given more than one session since it last served none. |
```

The row stays one physical line. Leave the `**Messages.**` bullet and the `llm_request_started` row unchanged.

**SPEC-4b. §11.2, the `**Direct mode (`deliveryMode: direct`):**` bullet.** Keep the sentence "The runtime adapter extracts token counts ... accumulates a per-session cumulative total from the `llm_request_completed` lifecycle frames the runtime sends ..." verbatim; `tests/tier11_docs/direct_usage_reportusage_consistency_test.go` pins it. Replace

```markdown
Because the gateway has no independent view of LLM responses in this mode, a malicious runtime could underreport token consumption.
```

with

```markdown
Because the gateway has no independent view of LLM responses in this mode, a malicious runtime could underreport token consumption, and on a pool with `maxConcurrentSessions > 1` it could charge one session's consumption to another session on the same pod, because the adapter attributes each `llm_request_completed` frame to the session the runtime names on it (the `CH-RUNTIMEOPS` card in [Section 28.5.3](28_communication-channels.md#2853-intra-pod)).
```

The bullet stays one physical line.

### SPEC-5 · spec/29_communication-scenarios.md § 29.2 Session start, § 29.10 Co-tenancy on a concurrent-session pod

**SPEC-5a. §29.2, step 24.** Replace

```markdown
which is empty when no
    connector is authorized
```

with

```markdown
which is empty when the pod
    serves no connector
```

**SPEC-5b. §29.2, step 26a.** Leave the line that carries "The runtime reads the manifest and connects to the platform MCP" unwrapped; it is a gate anchor, and the step keeps "started at most once per pod", "carried at the start that bound the server", and "does not re-arm". After the sentence that ends "`mcpNonce` as the top-level `_lennyNonce` field of the `initialize` request's `params` object.", insert

```markdown
This startup connection names no session. A runtime opens its
    per-session connections, each naming its session in the top-level `_lennySessionId` field, after that
    session's first `message` (step 32).
```

Replace the span

```markdown
The calling session is resolved at call time, and the call is
    refused unless the pod's shared runtime process has been given exactly one session and that session is
    the caller
```

with

```markdown
Each `tools/list` and `tools/call` is attributed to a session at call
    time under the session-address rule of
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)
```

The parenthesized citation list that follows stays.

**SPEC-5c. §29.2, step 26b.** Replace

```markdown
and at least one
    authorized connector:
```

with

```markdown
and a non-empty
    `connectorServers` array:
```

Replace

```markdown
The runtime connects to
    each authorized connector's own MCP server, presenting the nonce on each connection separately
```

with

```markdown
The runtime connects to
    each connector server the manifest lists, presenting the nonce, and the session it connects for when it
    names one, on each connection separately. The gateway refuses a connector the attributed session's
    policy does not permit
```

**SPEC-5d. §29.10, the `**Shared by the whole pod.**` list, bullet "The process-level co-tenancy the deployer acknowledged."** After the sentence that ends "which is not mitigated at the pod level.", insert

```markdown
The deployer also accepts runtime-asserted session attribution on the
  intra-pod MCP surface, which the intra-pod MCP surface entry below states.
```

**SPEC-5e. §29.10, the same list, bullet "The intra-pod MCP surface."** Replace the whole bullet with

```markdown
- The intra-pod MCP surface. The pod exposes one platform socket, and one socket for each connector
  permitted to a session started since the surface was armed, for the whole pod. All of them are
  authenticated by one pod nonce. Each request is attributed to a session under the session-address rule
  of [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels). The session a connection names
  is asserted by the connecting process, and the adapter confirms only that the session is bound on the
  pod. Any slot's process that reads the manifest can therefore name any session the pod holds
  ([§4.7](04_system-components.md#47-runtime-adapter), §28.5.3,
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

After applying SPEC-5, search spec/29 for "has been given exactly one session" and confirm no occurrence remains.

### SPEC-6 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool Configuration and Execution Modes, spec/09_mcp-integration.md § 9.3 Connector Definition and OAuth/OIDC, spec/13_security-model.md § 13.1 Pod Security

**SPEC-6a. §5.2, the `**Concurrent sessions (`maxConcurrentSessions > 1`).**` paragraph.** Before the sentence "Cross-slot isolation is process-level and filesystem-level, which is weaker than the one-session-per-pod configuration.", insert

```markdown
A Standard- or Full-level runtime on such a pool names the session on each intra-pod MCP connection it opens for a session, and a direct-mode runtime names it on each `llm_request_completed` frame; the adapter attributes an unaddressed request or frame under the rules of [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) and the `CH-RUNTIMEOPS` card ([§28.5.3](28_communication-channels.md#2853-intra-pod)).
```

**SPEC-6b. §5.2, the `**Deployer acknowledgment (concurrent sessions).**` paragraph.** Replace the enumerated list

```markdown
shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack, and **shared credential-file group-read access** (each slot's `/run/lenny/slots/{sessionId}/credentials.json` is readable by every slot's agent process via the shared `lenny-cred-readers` supplementary group — see [§13.1](13_security-model.md)) between concurrent slots.
```

with

```markdown
shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack, **shared credential-file group-read access** (each slot's `/run/lenny/slots/{sessionId}/credentials.json` is readable by every slot's agent process via the shared `lenny-cred-readers` supplementary group — see [§13.1](13_security-model.md)), and **runtime-asserted session attribution** (the adapter attributes intra-pod MCP calls to the session the connecting process names, confirming only that the session is bound on the pod, so any slot's process that reads the adapter manifest can call platform and connector tools as any session the pod holds; see [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) between concurrent slots.
```

The item name is "runtime-asserted session attribution", and CODE-5 and DOCS-1 repeat it in those words. Under fallback (b) of the pending attribution decision (see the summary's **Watch out for.**), land this block in place of the one above, and drop SPEC-6a:

```markdown
shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack, **shared credential-file group-read access** (each slot's `/run/lenny/slots/{sessionId}/credentials.json` is readable by every slot's agent process via the shared `lenny-cred-readers` supplementary group — see [§13.1](13_security-model.md)), and **intra-pod MCP tool loss** (the adapter refuses intra-pod platform and connector MCP calls while the pod's shared runtime process has been given more than one session since it last served none; see [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) between concurrent slots.
```

**SPEC-6c. §9.3, the bullet `**Connector access is scoped per delegation level.**`.** Replace

```markdown
against the calling pod's effective delegation policy before proxying.
```

with

```markdown
against the calling session's effective delegation policy before proxying.
```

**SPEC-6d. §13.1, the `**Per-slot credential-read scope (`maxConcurrentSessions > 1`).**` paragraph.** Replace

```markdown
alongside shared process namespace, `/tmp`, cgroup memory, and network stack;
```

with

```markdown
alongside shared process namespace, `/tmp`, cgroup memory, network stack, and runtime-asserted session attribution on the intra-pod MCP surface;
```

Under fallback (b), use "intra-pod MCP tool loss" in place of "runtime-asserted session attribution on the intra-pod MCP surface".

## Spec files touched

- `spec/04_system-components.md` (SPEC-2)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-6)
- `spec/09_mcp-integration.md` (SPEC-6)
- `spec/11_policy-and-controls.md` (SPEC-4)
- `spec/13_security-model.md` (SPEC-6)
- `spec/15_external-api-surface.md` (SPEC-1)
- `spec/28_communication-channels.md` (SPEC-3, SPEC-4)
- `spec/29_communication-scenarios.md` (SPEC-5)
