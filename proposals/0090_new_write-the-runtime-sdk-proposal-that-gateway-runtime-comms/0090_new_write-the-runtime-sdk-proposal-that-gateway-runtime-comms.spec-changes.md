# Spec changes: Runtime SDKs assume one session per process

## Design (as the spec must state it)

**Runtime lifetime contract (SPEC-1).** The §4.7.10 bold label **Runtime process lifetime.** is the single normative home of the contract. A `type: agent` runtime process serves any number of sessions, every session-scoped frame carries `sessionId`, a session's context arrives in its `session_start` frame on `CH-MSGSOCK`, and its `session_end` frame releases it. No runtime relies on process exit and no runtime declares a capability for this. Every other site links to §4.7.10.

**Session frames (SPEC-3).** The §28.5.3 `CH-MSGSOCK` card owns the `session_start` and `session_end` schemas, their write rules, and the session-error rule. Each `session_start` member is a former §4.7.6 manifest row, which SPEC-2 deletes from the manifest so that each field has one carrier. `shutdown` stays process-scoped.

**Session addressing on `CH-RUNTIMEOPS` (SPEC-4).** The session-scoped `CH-RUNTIMEOPS` frames carry a required `sessionId`. The `terminate` frame is deleted.

**SDK contract (SPEC-5) and conformance (SPEC-6).** §15.7 states per-session handler invocation and links to the card for the frames. §15.4.6 gains a Basic **session lifetime** category, retargets the Full **deadline signal handling** category onto `deadline_approaching`, and points the Full **credential rotation handling** category at the credential file that `credentials_rotated` names.

**Scenario traces (SPEC-7).** §29.2, §29.4, and §29.6 trace the frame writes the card states.

**Runtime connection handshake (SPEC-8, Part B).** §4.7.11 item 1 gains the wire lines of the nonce handshake on `CH-MSGSOCK` and `CH-RUNTIMEOPS`. The nonce's lifetime is left to proposal 0084.

## Edge cases and accepted failure modes

- A reclaim teardown can race ahead of a start's `session_start`. The runtime ignores the teardown's `session_end` (SPEC-3 **Inbound: `session_end`** rule 3), and SPEC-3 **Inbound: `session_start`** rule 1 and **Inbound: `session_end`** rule 1 govern the rule-8 rollback that follows.
- On a sidecar pod the coordinator hold timeout ends the `CH-MSGSOCK` connection before the per-session teardown runs, so no `session_end` is written there. SPEC-3 **Inbound: `session_end`** rule 1 covers this case, and the end of the connection ends every session.
- A message can arrive for a session whose `session_start` the runtime has not received, or whose context it failed to create. SPEC-3 **Session errors.** governs the answer.
- A Basic-level runtime that keeps no per-session context ignores both frames under the unknown-type rule that SPEC-3 adds to the card preamble, and it still passes SPEC-6 **session lifetime**.
- `deadline_approaching` can arrive with `trigger: idle` while no message is in flight. SPEC-4's `deadline_approaching` row governs the answer.
- An interrupt leaves the session suspended and resumable, so it writes no `session_end`.
- A `type: mcp` runtime speaks no `CH-MSGSOCK` frame and is outside the contract (SPEC-1).
- A connection that fails the SPEC-8 handshake is closed with no protocol response, and the adapter keeps accepting, so a failed or hostile dial cannot hold the single accept; a runtime holding a nonce that a later manifest write replaced (the INIT placeholder, or a concurrent start's write) is closed the same way and redials under SPEC-8 **Runtime connection handshake.**

## Staged edits

Several target passages are hard-wrapped across lines (§4.7.9, §4.7.10, the §28.5.3 cards, and §29). Match each quoted anchor with line breaks ignored and keep the surrounding wrap width. Every table row named below is one physical line, and it stays one physical line.

### SPEC-1 · spec/04_system-components.md § 4.7.10 Deployment Model, § 4.7.9, § 4.7.1; spec/15_external-api-surface.md § 15.4.2

**Edit 1 (§4.7.10 **Runtime process lifetime.**).** Append the following text to the end of the paragraph, after its closing text `"Deployer acknowledgment (runtime process kept across sessions)").`:

```markdown
A runtime process serves any number of sessions over its life, one after
another and, on a pool whose `sessionPolicy.maxConcurrentSessions` is greater
than one, at once. Every session-scoped frame on `CH-MSGSOCK` and
`CH-RUNTIMEOPS` carries the `sessionId` of the session it concerns
([Section 28.5.3](28_communication-channels.md#2853-intra-pod)). A session's
own context reaches the runtime in that session's `session_start` frame on
`CH-MSGSOCK`, and the session's `session_end` frame releases it. No runtime
relies on its process exiting at a session's end, and a runtime declares no
capability to serve more than one session. The contract covers `type: agent`
runtimes in both deployment models. A `type: mcp` runtime is driven over MCP
by the adapter and exchanges no `CH-MSGSOCK` frame, so the contract does not
apply to it.
```

Proposal 0087 adds its `restart` sentence to this paragraph when it defines the `restart` selector; this proposal does not write it.

**Edit 2 (§4.7.10 trade-off table).** Replace the row whose first cell is `Runtime process lifetime` with:

```markdown
| Runtime process lifetime | The pod's lifetime; one connection serves every session, each opened by `session_start` and released by `session_end` | The pod's lifetime (the adapter process); one loop per session, opened by `session_start` and released by `session_end` |
```

**Edit 3 (§4.7.9 step 7).** In step 7, replace `A later session on the same pod skips this step and steps 8 and 9, and uses the connections the runtime opened on the pod's first session.` with:

```markdown
A later session on the same pod skips this step and steps 8 and 9, uses the `CH-MSGSOCK` and `CH-RUNTIMEOPS` connections the runtime opened on the pod's first session, and takes step 10.
```

**Edit 4 (§4.7.9 new step 10).** Insert between the current step 9 (`Adapter sends \`lifecycle_capabilities\` (Full); receives \`lifecycle_support\``) and the current step 10:

```markdown
10. The adapter writes the session's `session_start` on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)). This step runs for every session on the pod.
```

**Edit 5 (§4.7.9 current step 10).** Renumber the current step 10 to 11 and replace it with:

```markdown
11. Adapter delivers first `{type: "message"}` on `CH-MSGSOCK`
```

**Edit 6 (§4.7.1 gateway RPC table, `Shutdown` row).** Inside the row, replace `It flushes the session's final usage report and then ends the session's use of the pod's runtime process` with:

```markdown
It flushes the session's final usage report, writes the session's `session_start`-paired `session_end` on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)), and then ends the session's use of the pod's runtime process
```

The edit sits inside the runtime-teardown clause. In the following sentence, `The teardown writes no \`CH-RUNTIMEOPS\` frame`, replace the citation `([Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine))` with `([Section 28.5.3](28_communication-channels.md#2853-intra-pod))`, and nothing attaches `session_end` to the slot release.

**Edit 7 (§15.4.2 `DRAINING` row).** In the row whose first cell is `` `DRAINING` ``, replace `No drain coordination exists at pod exit at any integration level: the adapter writes no \`CH-RUNTIMEOPS\` \`terminate\` frame, and the runtime process ends with the pod` with:

```markdown
No drain coordination exists at pod exit at any integration level: the runtime process ends with the pod
```

### SPEC-2 · spec/04_system-components.md § 4.7.5, § 4.7.6, § 4.7.11, § 4.9; spec/06_warm-pod-model.md § 6.1; spec/08_recursive-delegation.md § 8.3; spec/10_gateway-internals.md § 10.7; spec/15_external-api-surface.md § 15.7; spec/16_observability.md § 16.3; spec/28_communication-channels.md § 28.5 `CH-LLMPROXY`

**Edit 1 (§4.7.5 **Adapter manifest:**).** Keep the first sentence (`One pod-global file written to … complete and authoritative when the binary starts.`) unchanged. Replace the remainder of the paragraph, from `The adapter writes it before each session's runtime start` to `while an earlier session's runtime is still processing.`, with:

```markdown
The adapter rewrites it before each session's runtime start, including each session on a recycling pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The manifest carries only pod-scoped fields. A session's own identifier, credential path, experiment and tracing context, and LLM configuration reach the runtime in that session's `session_start` frame on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)), so a later start's rewrite changes none of them for an earlier session. On a pod holding more than one bound session, a later start replaces the manifest's `mcpNonce` while an earlier session's runtime is still processing ([Section 4.7.6](#476-adapter-manifest-field-reference), `mcpNonce` row).
```

**Edit 2 (§4.7.5 JSON example).** Delete these lines from the example: `"sessionId": "sess_abc",`, `"taskId": "sess_abc",`, the four-line `"experimentContext": { … },` member, and `"tracingContext": null,`. The remaining members keep their order and the document stays valid JSON.

**Edit 3 (§4.7.6 field table).** Delete the rows whose first cell is `` `sessionId` ``, `` `taskId` ``, `` `credentialsPath` ``, `` `experimentContext` ``, `` `tracingContext` ``, `` `llm` ``, `` `llm.deliveryMode` ``, `` `llm.dialect` ``, `` `llm.baseUrl` ``, and `` `llm.apiKeyEnv` ``. Insert this paragraph immediately after the table and before **Level reading requirements:**:

```markdown
**Per-session fields.** The per-session fields `sessionId`, `credentialsPath`, `experimentContext`, `tracingContext`, and `llm`, with its members `llm.deliveryMode`, `llm.dialect`, `llm.apiKeyEnv`, and `llm.headers`, are carried by each session's `session_start` frame on `CH-MSGSOCK` rather than by the manifest; the `CH-MSGSOCK` card in [Section 28.5.3](28_communication-channels.md#2853-intra-pod) defines them. `taskId` has no successor field, because a session's task identifier equals its `sessionId` ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)).
```

**Edit 4 (§4.7.6 **Level reading requirements:**, Basic bullet).** Replace the bullet's last sentence, `A Basic-level runtime that reads a credential file reads the manifest for its path: the file is written per session at the location the \`credentialsPath\` field names, so a runtime that assumes a fixed location does not find it.`, with:

```markdown
A Basic-level runtime that reads a credential file reads its path from the `credentialsPath` member of the session's `session_start` frame ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)): the file is written per session, so a runtime that assumes a fixed location does not find it.
```

**Edit 5 (§4.7.11 item 4 **No credential material over socket:**).** Replace `written by the adapter before spawning the runtime binary.` with:

```markdown
written by the adapter before it writes the session's `session_start` frame on `CH-MSGSOCK`, which names the file's path ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)).
```

**Edit 6 (§4.9, `anthropic` dialect bullet).** Replace `The injected default is advertised to runtimes via the adapter manifest's \`llm.headers\` field so SDKs that require an explicit version can read it at startup.` with:

```markdown
The injected default is advertised to runtimes in the `llm.headers` member of each session's `session_start` frame ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)), so an SDK that requires an explicit version reads it when the session starts.
```

**Edit 7 (§28.5 `CH-LLMPROXY` card, **Messages.** bullet).** Replace `advertising that default to runtimes through the adapter manifest's \`llm.headers\` field` with:

```markdown
advertising that default to runtimes in the `llm.headers` member of each session's `session_start` frame (§28.5.3 `CH-MSGSOCK`)
```

**Edit 8 (§6.1 **Per-session credential lease lifecycle.**).** Replace `The adapter rewrites the pod-global adapter manifest before each session's runtime start ([Section 4.7](04_system-components.md#47-runtime-adapter)), and the session's credential file is written with the new lease before that session's binary is spawned.` with:

```markdown
The session's credential file is written with the new lease before the adapter writes the session's `session_start` frame on `CH-MSGSOCK`, which names the file's path ([Section 28.5.3](28_communication-channels.md#2853-intra-pod)).
```

**Edit 9 (§8.3 **`experimentContext`**).** Replace `The \`experimentContext\` is delivered to the runtime in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification))` with:

```markdown
The `experimentContext` is delivered to the runtime in the session's `session_start` frame on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))
```

**Edit 10 (§8.3 **`tracingContext`**).** Replace `The child runtime receives it in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification))` with:

```markdown
The child runtime receives it in the child session's `session_start` frame on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))
```

**Edit 11 (§10.7 **Experiment context delivery to runtimes.**).** Replace `is delivered to the runtime in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification))` with:

```markdown
is delivered to the runtime in the session's `session_start` frame on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))
```

In item 1 **Runtime-native tracing and scoring (primary).** of the same subsection, replace `is also delivered in the adapter manifest` with `is also delivered to the runtime`.

**Edit 12 (§10.7 responsibility table).** Replace the row whose first cell is `Experiment context delivery` with:

```markdown
| Experiment context delivery | Lenny | `experimentContext` in the session's `session_start` frame on `CH-MSGSOCK`; runtime reads `experimentId` and `variantId` |
```

In the row whose first cell is `Trace-level scoring and observability`, delete ` from adapter manifest`. The row stays one physical line.

**Edit 13 (§16.3 **Tier 2: Runtime-level tracing (runtime-managed).**).** Replace `Child runtimes receive the parent's \`tracingContext\` in the adapter manifest` with:

```markdown
Child runtimes receive the parent's `tracingContext` in the child session's `session_start` frame on `CH-MSGSOCK` ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))
```

**Edit 14 (§15.7 **What the SDKs provide.**, **Credential delivery.** bullet).** Replace `(present under both proxy and direct delivery modes per [§4.7](04_system-components.md#47-runtime-adapter) manifest \`llm\` fields)` with `(present under both proxy and direct delivery modes per the \`llm\` fields of the session's \`session_start\` frame, [§28.5.3](28_communication-channels.md#2853-intra-pod))`, and replace `the env-var export (\`llm.apiKeyEnv\`) for proxy mode` with `the per-session API key that \`llm.apiKeyEnv\` names for proxy mode`.

**Edit 15 (§15.7 **Credential access.** bullet).** Replace `Direct mode env-var refresh` with `Direct mode per-session API-key refresh`.

In the commit that lands Edits 1 and 8, `intraPodNonceSites` in `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` pins `carries only pod-scoped fields` in place of `authoritative for the session whose start last wrote it` at its §4.7.5 adapter-manifest-lead site, and drops its §6.1 credential-lease site.

### SPEC-3 · spec/28_communication-channels.md § 28.5.3 `CH-MSGSOCK`; spec/15_external-api-surface.md § 15.4, § 15.4.1, § 15.4.3

**Edit 1 (card **Messages.** bullet).** Make three replacements inside the bullet:
- `Adapter to runtime: \`message\`, \`tool_result\`, \`heartbeat\`, and \`shutdown\`.` becomes `Adapter to runtime: \`session_start\`, \`message\`, \`tool_result\`, \`heartbeat\`, \`session_end\`, and \`shutdown\`.`
- `` `heartbeat` and `shutdown` use their own minimal schemas `` becomes `` `session_start`, `session_end`, `heartbeat`, and `shutdown` use their own minimal schemas ``.
- `` `message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, and `status` carry a `sessionId` `` becomes `` `session_start`, `session_end`, `message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, and `status` carry a `sessionId` ``.

**Edit 2 (card **Preconditions.** bullet).** Replace the bullet's first sentence, `The adapter writes the final adapter manifest and spawns the runtime binary before it delivers the first \`message\`, with the runtime's connection to the MCP servers and the \`CH-RUNTIMEOPS\` capability handshake in between ([§4.7](04_system-components.md#47-runtime-adapter)).`, with:

```markdown
The adapter delivers a session's first `message` after the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) startup steps that precede that delivery and after the session's `session_start` (**Inbound: `session_start`** rule 1).
```

The bullet's second sentence (the adapter is the protocol initiator) stays unchanged.

**Edit 3 (card **Timing.** bullet).** Replace `A \`shutdown\` carries a \`deadline_ms\` by which the runtime must finish its current work and exit` with:

```markdown
`session_start` and `session_end` are not acknowledged and carry no deadline; each travels in order on the one stream ahead of or behind the session's other frames. A `shutdown` carries a `deadline_ms` by which the runtime must finish its current work and exit
```

**Edit 4 (**Message schemas** preamble).** In the paragraph that begins `All **content** messages on stdin`, replace `Lifecycle messages (\`heartbeat\`, \`shutdown\`) use their own minimal schemas defined below and are not \`MessageEnvelope\` instances. Runtimes MUST ignore unrecognized fields.` with:

```markdown
Lifecycle messages (`session_start`, `session_end`, `heartbeat`, and `shutdown`) use their own minimal schemas defined below and are not `MessageEnvelope` instances. Runtimes MUST ignore unrecognized fields. Runtimes ignore a frame whose type they do not recognize.
```

**Edit 5 (new frame sections).** Insert the following after the **Inbound: `heartbeat`** section (after its sentence ending `escalates as the \`CH-MSGSOCK\` **Timing.** bullet states.`) and before **Inbound: `shutdown`**:

````markdown
**Inbound: `session_start`**

```json
{
  "type": "session_start",
  "sessionId": "sess_abc123",
  "credentialsPath": "/run/lenny/slots/sess_abc123/credentials.json",
  "experimentContext": { "experimentId": "claude-v2-rollout", "variantId": "treatment", "inherited": false },
  "tracingContext": null,
  "llm": { "deliveryMode": "proxy", "dialect": "anthropic", "apiKeyEnv": "ANTHROPIC_API_KEY" }
}
```

`session_start` opens a session on the runtime and carries the session's own context ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime"). The following rules govern it:

1. The adapter writes a session's `session_start` once for each start and each resume of the session, before any other frame addressed to the session and before the [§4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) rule-8 confirmation, on every `type: agent` start path: pod-warm, SDK-warm, and resume, in both deployment models.
2. A runtime that keeps per-session context creates the session's context from this frame. A runtime that keeps none may ignore the frame under the unknown-type rule in the preamble above.
3. A runtime ignores a `session_start` for a session it already holds.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `type` | `string` | Yes | `"session_start"`. |
| `sessionId` | `string` | Yes | The session the frame opens. The adapter populates it on every pod. A session's task identifier equals its `sessionId` ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). |
| `credentialsPath` | `string` | No | Absolute path to this session's credential file, `/run/lenny/slots/{sessionId}/credentials.json` (see item 4 of [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary)). The adapter writes the file before it writes this frame and rewrites it in place on a rotation for this session. A runtime that reads credential material reads its path from this field rather than assuming a fixed location. Present whenever the adapter provisioned a credential file for the session and absent otherwise; a runtime given no path loads no credential bundle for the session. |
| `experimentContext` | `object` or `null` | No | Experiment enrollment context for this session. Contains `experimentId` (string), `variantId` (string), and `inherited` (boolean — `true` when propagated from a parent via delegation). Absent or `null` when the session is not enrolled in any experiment. When present, runtimes can use this to tag traces with variant metadata for filtering and grouping in their eval platform. See [§10.7](10_gateway-internals.md#107-experiment-primitives). |
| `tracingContext` | `object` or `null` | No | Tracing identifiers propagated from the parent runtime via delegation; absent or `null` for top-level sessions. An opaque key-value map of strings (e.g., `{"langsmith_run_id": "run_abc123", "otel_trace_id": "0af7651916cd43dd"}`). Child runtimes use this to stitch their native traces into the parent's trace tree. Contains only non-sensitive identifiers — never endpoint URLs or credentials (see [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) for validation rules). See [§16.3](16_observability.md#163-distributed-tracing) for the two-tier tracing model. |
| `llm` | `object` or `null` | No | LLM provider configuration for the session. Absent or `null` if the session does not have an active LLM credential lease. |
| `llm.deliveryMode` | `string` | Yes | `direct` \| `proxy`. Tells the runtime which credential delivery mode is active for this session's LLM provider pool ([§4.9](04_system-components.md#49-credential-leasing-service)). |
| `llm.dialect` | `string` | No (direct) / Yes (proxy) | `openai` \| `anthropic`. In proxy mode, the runtime's LLM SDK must speak this dialect to the `materializedConfig.proxyUrl` in this session's credential file (item 4 of [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary)). Omitted in direct mode where the runtime uses the upstream provider's native SDK. |
| `llm.apiKeyEnv` | `string` | No | Canonical env var name the runtime's LLM SDK reads for its API key. Convention: `"ANTHROPIC_API_KEY"` for `dialect: anthropic`; `"OPENAI_API_KEY"` for `dialect: openai`. The runtime supplies the lease token from this session's credential file as this session's API key: it passes the token to the LLM client it builds for the session, or sets this variable only in the environment of a subprocess it starts for this session. It does not set the variable in its own process environment, which every session the process serves shares. The pod-level env (§14 `env` field) is NOT used for credential material; blocklisted sensitive names still cannot appear in pod-level env. |
| `llm.headers` | `object` | No | Map of header names to string values the runtime's LLM SDK sends to the LLM Proxy in proxy mode, such as the `anthropic-version` default the LLM Proxy injects when the runtime sends none ([§4.9](04_system-components.md#49-credential-leasing-service)). Omitted when no header is advertised. |

**Inbound: `session_end`**

```json
{ "type": "session_end", "sessionId": "sess_abc123" }
```

`session_end` releases a session on the runtime. The following rules govern it:

1. The adapter writes it on every path that ends a session's use of the runtime while the runtime's connection stays open: session teardown, a start rolled back under [§4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) rule 8 when the adapter then holds no entry for the session's slot identifier, and, where the coordinator hold timeout leaves the connection open, that timeout. When the connection ends, every session the runtime holds ends, and no `session_end` follows. A rule-8 rollback that finds an entry for the identifier writes no `session_end`, because the attempt holding that entry owns the session on the runtime. An interrupt does not end a session and writes no `session_end`.
2. On `session_end` the runtime releases the session's context, writes no further frame addressed to the session other than the `llm_request_completed` of a request whose `llm_request_started` preceded the `session_end`, and keeps serving the pod's other sessions.
3. A runtime ignores a `session_end` for a session it does not hold.

**Session errors.** A runtime that keeps per-session context and receives a `message` for a session whose `session_start` it has not received, or whose context it failed to create, answers with a `response` carrying `error` for that `sessionId` and keeps running.
````

**Edit 6 (**Inbound: `shutdown`**).** Replace the paragraph `Agent must finish current work and exit within \`deadline_ms\`.` (through `then SIGKILL after 10 seconds.`) by prefixing it with `Process-scoped: the adapter never writes \`shutdown\` at a session boundary. ` so that it reads:

```markdown
Process-scoped: the adapter never writes `shutdown` at a session boundary. Agent must finish current work and exit within `deadline_ms`. No acknowledgment required — the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.
```

**Edit 7 (**Error reporting via `response`.**).** Replace `When \`error\` is absent and the process exits zero, the task completes successfully.` with:

```markdown
When `error` is absent, the `response` completes the session's task successfully.
```

**Edit 8 (§15.4 artifact bullet for `schemas/lenny-adapter-jsonl.schema.json`).** Delete the parenthetical frame list `` (`message`, `tool_result`, `heartbeat`, `shutdown`, `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`)``, so that the bullet names no frame and points to the card through its existing Section 28.5.3 reference.

**Edit 9 (§15.4.3 **Basic** bullet list).** Insert after the bullet `Must handle \`{type: "shutdown"}\` by exiting within the specified \`deadline_ms\``:

```markdown
- Handles `{type: "session_start"}` and `{type: "session_end"}` as the `CH-MSGSOCK` card in [Section 28.5.3](28_communication-channels.md#2853-intra-pod) states; a runtime that keeps no per-session context ignores both
```

**Edit 10 (§15.4.1 frame enumerations).** In the sentence beginning `All inbound **content** messages`, replace ``Non-content lifecycle messages (`heartbeat`, `shutdown`, `heartbeat_ack`) use their own minimal schemas`` with `Non-content lifecycle messages use their own minimal schemas`. In the paragraph beginning `The message schemas of the adapter↔binary stdin and stdout messages`, delete ``, which are `message`, `heartbeat`, `shutdown`, `tool_result`, `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`,`` so that the paragraph names no frame and points to the card. That paragraph is hard-wrapped, so match the deleted text with line breaks ignored and rewrap the result.

**Edit 11 (card **Annotated Protocol Trace — Basic-Level Session**).** Inside the fenced block, make these changes and keep the three-space indentation of the frame lines:

- Insert before the step `2. Adapter writes to stdin:` that carries the `message` frame:

  ```
  2. Adapter writes to stdin (a runtime that keeps no per-session context ignores it, **Inbound: `session_start`** rule 2):
     {"type": "session_start", "sessionId": "sess_abc123"}
  ```

- Insert before the step `8. Gateway initiates shutdown. Adapter writes:`:

  ```
  9. The session ends. Adapter writes:
     {"type": "session_end", "sessionId": "sess_abc123"}
  ```

- In the step that carries the `shutdown` frame, replace `Gateway initiates shutdown. Adapter writes:` with `The pod drains. Adapter writes:`.
- Renumber the steps so that they run from 1 through 12 in order. Step 1 and every frame line other than the inserted ones are unchanged.

### SPEC-4 · spec/28_communication-channels.md § 28.5.3 `CH-RUNTIMEOPS`; spec/15_external-api-surface.md § 15.4.4, § 15.7

**Edit 1 (card **Messages.** bullet, frame list).** Replace `Adapter to runtime: \`lifecycle_capabilities\`, \`checkpoint_request\`, \`checkpoint_complete\`, \`interrupt_request\`, \`credentials_rotated\`, \`terminate\`, and \`deadline_approaching\`.` with:

```markdown
Adapter to runtime: `lifecycle_capabilities`, `checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, and `files_updated`.
```

**Edit 2 (card **Messages.** bullet, addressing sentence).** Insert after the sentence that ends `the field set of each is the message-schema table below.`:

```markdown
`checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, `files_updated`, and `llm_request_completed` are session-scoped and carry the `sessionId` of the session they concern ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime"); the runtime's replies stay correlated by `checkpointId`, `interruptId`, and `leaseId`. `lifecycle_capabilities`, `lifecycle_support`, and `llm_request_started` are process-scoped and carry no `sessionId`.
```

**Edit 3 (message-schema table).** Replace the five rows below, delete the `terminate` row, and insert the `files_updated` row after the `deadline_approaching` row. Each row stays one physical line with the table's two-space indent:

```markdown
  | `checkpoint_request`        | Adapter → Runtime  | `type`, `sessionId` (string), `checkpointId` (string), `deadlineMs` (integer — ms until adapter times out waiting)                                                 | Adapter requests the named session's runtime work quiesce and signal readiness. Runtime must reply with `checkpoint_ready` within `deadlineMs`. |
  | `checkpoint_complete`       | Adapter → Runtime  | `type`, `sessionId` (string), `checkpointId` (string), `status` (`"ok"` \| `"failed"`), `reason` (string, present when `status: "failed"`)                        | Confirms snapshot upload result; the named session's work may resume.       |
  | `interrupt_request`         | Adapter → Runtime  | `type`, `sessionId` (string), `interruptId` (string), `deadlineMs` (integer)                                                                                       | Requests the named session's work reach a safe stop point within `deadlineMs`. **Timeout behavior:** if `interrupt_acknowledged` is not received within `deadlineMs`, the adapter transitions the session to `suspended` anyway (best-effort — the deadline has elapsed so the runtime is assumed to have stopped making progress) and returns an `INTERRUPT_TIMEOUT` status in the `Interrupt` RPC response to the gateway. The gateway logs the timeout and proceeds with the `suspended` state normally. The session is NOT left in `running` on timeout. |
  | `credentials_rotated`       | Adapter → Runtime  | `type`, `sessionId` (string), `provider` (string), `credentialsPath` (string — path to updated `/run/lenny/slots/{sessionId}/credentials.json`), `leaseId` (string) | New credentials written for the named session; runtime must rebind that session and reply with `credentials_acknowledged`. |
  | `deadline_approaching`      | Adapter → Runtime  | `type`, `sessionId` (string), `remainingMs` (integer — ms until session expiry or budget exhaustion), `trigger` (`"session_age"` \| `"budget"` \| `"idle"`)        | Advance warning before the named session's forced termination. Runtime should wrap up that session's work. A runtime writes no `response` for the session in reply to this frame when no `message` for the session is in flight. |
  | `files_updated`             | Adapter → Runtime  | `type`, `sessionId` (string)                                                                                                                                       | A mid-session upload has promoted new files into the named session's workspace ([§7.4](07_session-lifecycle.md#74-upload-safety)). Sent after the atomic overlay completes; one-way, with no acknowledgement. |
```

In the `llm_request_completed` row, insert `` `sessionId` (string), `` after `` `type`, `` in the field cell, and replace `accumulates them into a per-session cumulative total internally and reports` with ``accumulates them into the cumulative total of the session `sessionId` names, drops them when the pod holds no binding for that session, and reports``. The row stays one physical line.

**Edit 4 (card **Timing.** bullet).** Replace `\`checkpoint_request\`, \`interrupt_request\`, \`terminate\`, and \`deadline_approaching\` each carry a millisecond field that bounds the runtime's reply, its exit, or the remaining session time` with:

```markdown
`checkpoint_request`, `interrupt_request`, and `deadline_approaching` each carry a millisecond field that bounds the runtime's reply or the remaining session time
```

**Edit 5 (card **Degradation.** bullet).** Delete the sentence `When the runtime has not exited by the \`terminate\` frame's \`deadlineMs\` the adapter sends SIGTERM, as the message-schema table above states.`

**Edit 6 (§15.7 **Graceful shutdown.** bullet).** Replace the bullet with:

```markdown
    - **Graceful shutdown.** SIGTERM handling and the `shutdown` deadline contract from the `CH-MSGSOCK` card in [§28.5.3](28_communication-channels.md#2853-intra-pod), and per-session routing of `CH-RUNTIMEOPS` events by `sessionId` ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
```

**Edit 7 (§15.4.4 Full-level sample, lifecycle and main loops).** In the lifecycle loop, delete the `case "terminate":` arm (its `// Ordered shutdown — exit within deadlineMs` comment and its `cleanup_and_exit(0)` line) and the blank line after it. In the main loop's `case "shutdown":` arm, replace the two-line comment beginning `// shutdown arrives on stdin even for Full-level; lifecycle terminate` with the single line `// shutdown arrives on stdin even for Full-level`.

### SPEC-5 · spec/15_external-api-surface.md § 15.7 Runtime Author SDKs

**Edit 1 (**API surface (Go).** code block).** Replace the `Handler` declaration and the `Run` doc comment, from `// Handler is the single interface runtime authors implement.` through `func Run(h Handler, opts ...Option) error`, with:

```go
// Handler is the single interface runtime authors implement. One runtime
// process serves any number of sessions
// ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime
// process lifetime"). The SDK invokes OnCreate once per session, when the
// session's `session_start` arrives; OnMessage for each of the session's
// messages; and OnTerminate once when the session ends, on its
// `session_end` or at the end of the connection. Calls for different
// sessions run concurrently, so an implementation keeps per-session state
// keyed by session and is safe for concurrent use. A session whose OnCreate
// fails is answered as the `CH-MSGSOCK` card's **Session errors.** rule in
// [§28.5.3](28_communication-channels.md#2853-intra-pod) states.
type Handler interface {
    OnCreate(ctx context.Context, req CreateRequest) error
    OnMessage(ctx context.Context, msg Message) (Reply, error)
    OnTerminate(ctx context.Context, sessionID string, reason TerminationReason) error
}

// Run wires up stdin/stdout framing, dials the manifest-advertised
// platform MCP, connector MCP, and CH-RUNTIMEOPS sockets once per process
// with the manifest-nonce handshake, loads each session's credentials from
// the path its `session_start` names, and drives the lenny.runtime.*
// dispatch loop.
// Blocks until the adapter closes the connection or sends `shutdown`, then
// ends every session the process holds.
func Run(h Handler, opts ...Option) error
```

**Edit 2 (**SDK Handler types.** paragraph).** Replace `the lower-level wire contracts already defined in this spec: the adapter manifest ([§4.7](04_system-components.md#47-runtime-adapter)),` with `the lower-level wire contracts already defined in this spec: the \`session_start\` frame ([§28.5.3](28_communication-channels.md#2853-intra-pod)), the pod-scoped adapter manifest ([§4.7](04_system-components.md#47-runtime-adapter)),`, and replace `the SDK parses the manifest, stdin framing, and credential file into these structs` with `the SDK parses the \`session_start\` frame, the pod-scoped manifest, stdin framing, and the credential file into these structs`.

**Edit 3 (`CreateRequest` type comment).** Replace the comment block from `// CreateRequest is the snapshot of task-scoped context handed to` through `// Credentials) in place on rotation events without re-invoking OnCreate.` with:

```go
// CreateRequest is the snapshot of session-scoped context handed to
// Handler.OnCreate when the session's `session_start` arrives and before the
// session's first Message is delivered. The SDK assembles this value from
// (a) the session's `session_start` frame on CH-MSGSOCK
// ([§28.5.3](28_communication-channels.md#2853-intra-pod)), (b) the
// credential file at the path that frame names
// ([§4.7](04_system-components.md#47-runtime-adapter) item 4), (c) the
// pod-scoped adapter manifest at /run/lenny/adapter-manifest.json
// ([§4.7](04_system-components.md#47-runtime-adapter)), and (d) the
// StartSession RPC parameters the gateway forwarded to the adapter (see the
// Startup Sequence in [§4.7](04_system-components.md#47-runtime-adapter)).
// Handler implementations MUST treat CreateRequest as read-only — the wire
// sources are authoritative and the SDK will refresh derived fields (notably
// Credentials) in place on rotation events without re-invoking OnCreate.
```

**Edit 4 (`CreateRequest.SessionID` and `TaskID`).** Replace the two field comments with:

```go
    // SessionID is the session this request opens. Matches `sessionId` in
    // the session's `session_start` and `SessionMetadata.SessionID`
    // ([§15 Shared Adapter Types](#shared-adapter-types)).
    SessionID string `json:"sessionId"`

    // TaskID is the session's external-protocol task identifier. Each
    // session has exactly one execution, and external protocols surface that
    // execution as a Task ([§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)),
    // so TaskID equals the session id; the SDK derives it from `sessionId`.
    // OnCreate is invoked once per session with this value.
    TaskID string `json:"taskId"`
```

**Edit 5 (`CreateRequest.Credentials`).** In the field comment, replace `Nil only when the runtime's provider pool has no active lease (matches \`llm: null\` in the manifest).` with `Nil when the session's provider pool has no active lease (matches \`llm: null\` in the session's \`session_start\`) or when the session's \`session_start\` names no \`credentialsPath\`.`

**Edit 6 (new `CreateRequest` fields).** Insert after the `Credentials` field and before `ManifestSnapshot`:

```go
    // ExperimentContext is the session's experiment enrollment from the
    // session's `session_start` (`experimentContext`); nil when the session
    // is not enrolled.
    ExperimentContext *ExperimentContext `json:"experimentContext,omitempty"`

    // TracingContext is the session's inherited tracing identifiers from the
    // session's `session_start` (`tracingContext`); nil for a top-level
    // session.
    TracingContext map[string]string `json:"tracingContext,omitempty"`

    // LLM is the session's LLM provider configuration from the session's
    // `session_start` (`llm`); nil when the session has no active LLM lease.
    LLM *LLMConfig `json:"llm,omitempty"`
```

**Edit 7 (`CreateRequest.ManifestSnapshot`).** Replace the field comment with:

```go
    // ManifestSnapshot is the parsed pod-scoped adapter manifest
    // ([§4.7](04_system-components.md#47-runtime-adapter) "Adapter manifest
    // field reference"). It carries only pod-scoped fields; the session's
    // own context is in the fields above. Authors MAY consult it for the
    // platform MCP socket, CH-RUNTIMEOPS socket, and connector servers. The
    // SDK has dialed the advertised sockets and attached the `mcpNonce`
    // before OnCreate is invoked; authors who only use
    // SDK-provided MCP helpers do not need to read this field directly.
```

**Edit 8 (`Message` type comment).** Replace `It wraps the canonical MessageEnvelope with the session/task IDs the SDK resolved from the adapter manifest, so Handler implementations do not have to correlate against the manifest on every turn.` with `It wraps the canonical MessageEnvelope with the session and task IDs the SDK resolved from the frame's \`sessionId\`.`

**Edit 9 (`Message.SessionID` and `TaskID`).** Replace the two field comments with:

```go
    // SessionID is the session the message was delivered to. Populated
    // from the inbound frame's `sessionId`; equals the CreateRequest.SessionID
    // of that session's OnCreate.
    SessionID string `json:"sessionId"`

    // TaskID is the external-protocol task identifier of the session the
    // message belongs to. It equals SessionID and the CreateRequest.TaskID
    // of that session's OnCreate.
    TaskID string `json:"taskId"`
```

**Edit 10 (`Message.Sequence`).** In the field comment, replace `Sequence is a local per-process counter` with `Sequence is a local per-session counter`.

The shared §15 `TerminationReason` struct stays unchanged.

### SPEC-6 · spec/15_external-api-surface.md § 15.4.6 Conformance Test Suite

**Edit 1 (new Basic row).** Insert after the row whose test category is `**per-session identifier echo**`:

```markdown
| **Basic** | **session lifetime** | On one connection the harness writes `session_start`, a `message`, and `session_end` for session A; then the same three frames for session B; then `session_start` for sessions C and D followed by alternating `message` frames for C and D before it reads any response. The binary answers every `message` with a `response`, answers a `heartbeat` written after each `session_end` with `heartbeat_ack`, and neither exits nor closes its stdout before the harness closes stdin. |
```

**Edit 2 (Full row **deadline signal handling**).** Replace the row with:

```markdown
| **Full** | **deadline signal handling** | If the runtime declares the `deadline_signal` capability in `lifecycle_support`, the harness writes `session_start` and a `message` for a session, then `deadline_approaching` with that session's `sessionId` before reading the response. The runtime writes the response to that `message` (possibly with `error.code: "DEADLINE_EXCEEDED"`) before `remainingMs` elapses, writes no other `response` for that session, and still answers a later `heartbeat` with `heartbeat_ack`. |
```

**Edit 3 (Full row **credential rotation handling**).** In the row, replace `` re-reads refreshed credentials from the manifest or env on `credential_rotated` `` with `` re-reads the credential file at the `credentialsPath` that `credentials_rotated` carries for the named session ``.

The Basic **shutdown within `deadline_ms`** row stays unchanged.

### SPEC-7 · spec/29_communication-scenarios.md § 29.2, § 29.4, § 29.6

**Edit 1 (§29.2 new step 30).** Insert after step 29 and renumber the current steps 30, 31, and 32 to 31, 32, and 33:

```markdown
30. On a pod-warm pod with a `type: agent` runtime: `adapter` → `runtime`, `CH-MSGSOCK`, `intra-pod`. The
    adapter writes the session's `session_start`, carrying its `sessionId`, credential path, and
    experiment, tracing, and LLM context. It does so on every session, including each later session on
    the pod (§28.5.3 `CH-MSGSOCK`,
    [§4.7.10](04_system-components.md#4710-deployment-model)).
```

**Edit 2 (§29.2 ranges).** In step 23 replace `Steps 24 through 29 are the pod-warm startup sequence` with `Steps 24 through 30 are the pod-warm startup sequence`. In the renumbered step 31 (formerly 30) replace `startup sequence of steps 24 through 29` with `startup sequence of steps 24 through 30`.

**Edit 3 (§29.2 renumbered step 33).** Replace `the adapter has written the final manifest and spawned the runtime binary, with the runtime's connection to the MCP servers and the \`CH-RUNTIMEOPS\` capability handshake in between` with `steps 24 through 30 have run`. Replace the step's SDK-warm sentence, `On an SDK-warm pod that startup sequence does not occur, because the session is already connected and the gateway has pointed it at the finalized \`cwd\``, with:

```markdown
On an SDK-warm pod that startup sequence does not occur, because the session is already connected and the gateway has pointed it at the finalized `cwd`; the adapter writes the session's `session_start` while it handles that `ConfigureWorkspace` call (§28.5.3 `CH-MSGSOCK`, **Inbound: `session_start`** rule 1)
```

**Edit 4 (§29.4 step 13).** Replace step 13 with:

```markdown
13. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or
    by an expiry timer, for a session whose start the adapter admitted: `adapter` → `runtime`,
    `CH-MSGSOCK`, `intra-pod`. The adapter writes the session's `session_end`, writes no `CH-RUNTIMEOPS`
    frame, and sends the runtime process no signal. The runtime process stays alive for the pod's life
    ([§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row,
    [§4.7.10](04_system-components.md#4710-deployment-model), §28.5.3 `CH-MSGSOCK`).
```

**Edit 5 (§29.6 new step 10).** Insert after step 9 and renumber the current steps 10, 11, and 12 to 11, 12, and 13:

```markdown
10. For a `type: agent` runtime: `adapter` → `runtime`, `CH-MSGSOCK`, `intra-pod`. The adapter writes the
    resumed session's `session_start` on the replacement pod before any `message` for the session
    (§28.5.3 `CH-MSGSOCK`, [§4.7.10](04_system-components.md#4710-deployment-model)).
```

### SPEC-8 · spec/04_system-components.md § 4.7.11, § 4.7.6, § 4.7.7; spec/15_external-api-surface.md § 15.4.3, § 15.4.4, § 15.7; spec/28_communication-channels.md § 28.5.3; spec/29_communication-scenarios.md § 29.2 (Part B)

**Edit 1 (§4.7.11 item 1, new paragraph).** Insert after the **Nonce-only fallback: challenge-response supplement and escalation gate.** list (after its sentence ending `gated on the adapter's ephemeral challenge.`) and before **Activation.**, indented three spaces as part of item 1:

```markdown
   **Runtime connection handshake.** On a `CH-MSGSOCK` or `CH-RUNTIMEOPS` socket connection the runtime's first line is the nonce line `{"_lennyNonce":"<nonce_hex>"}`, due within 500 ms of the accepted connection. The adapter compares it in constant time with the `mcpNonce` the manifest carries at that moment. In nonce-only mode the adapter then writes `{"_lennyChallenge":"<hex>"}`, and the runtime answers with `{"_lennyChallengeResponse":"<hex HMAC>"}` within 500 ms, computed as the steps above state. A connection that fails either check is closed with no protocol response, and the adapter keeps accepting. The handshake lines precede the channel's framed protocol and are outside its frame schema. A runtime reads `mcpNonce` from the manifest before each dial, answers a challenge whenever one arrives before its first protocol frame, and reads the manifest again and dials again when the adapter closes the connection before that frame.
```

**Edit 2 (§4.7.6 `mcpNonce` row).** Append to the end of the row's description cell, after `the adapter rejects connections that do not present a valid nonce.`, the sentence:

```markdown
It also authenticates the `CH-MSGSOCK` and `CH-RUNTIMEOPS` connections, checked once when each is accepted ([§4.7.11](#4711-adapter-agent-security-boundary) item 1, **Runtime connection handshake.**).
```

In the same row replace the Level relevance cell `Standard, Full` with ``All (socket `CH-MSGSOCK`); Standard, Full (MCP)``. The row stays one physical line, and its existing sentences stay byte-identical; proposal 0084 rewrites them and its tier-11 gate pins their phrases.

**Edit 3 (§28.5.3 `CH-MSGSOCK` **Endpoint.** bullet).** Replace `and the manifest-nonce handshake presented as the first message on the socket. When \`Runtime.spec.requireSoPeercred\` is \`false\` the peer check is unavailable and the adapter supplements the static nonce with a per-connection 128-bit challenge whose \`HMAC-SHA256\` response it validates` with:

```markdown
and the connection handshake that [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1 **Runtime connection handshake.** states, which in nonce-only mode includes the per-connection challenge
```

**Edit 4 (§28.5.3 `CH-RUNTIMEOPS` **Endpoint.** bullet).** Replace `and the manifest-nonce handshake, which the runtime presents as the first message on the socket. When \`Runtime.spec.requireSoPeercred\` is \`false\` the peer check is unavailable and the adapter supplements the static nonce with a per-connection 128-bit challenge whose \`HMAC-SHA256\` response it validates` with:

```markdown
and the connection handshake that [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1 **Runtime connection handshake.** states, which in nonce-only mode includes the per-connection challenge
```

**Edit 5 (§28.5.3 `CH-RUNTIMEOPS` **Preconditions.** and table).** In the **Preconditions.** bullet replace `\`lifecycle_capabilities\` is the first message sent on channel open` with `After the connection handshake ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1), \`lifecycle_capabilities\` is the first frame sent on the channel`. In the `lifecycle_capabilities` table row replace the Notes text `First message sent on channel open.` with `First frame after the connection handshake.`

**Edit 6 (§15.7 **Intra-pod authentication.** bullet).** Replace `(injected as \`params._lennyNonce\` on the MCP \`initialize\` request and on the CH-RUNTIMEOPS)` with `(injected as \`params._lennyNonce\` on the MCP \`initialize\` request, and sent as the nonce line that opens \`CH-MSGSOCK\` and \`CH-RUNTIMEOPS\`, [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1)`, and replace `SDKs read the nonce from the manifest and attach it automatically.` with `SDKs read the nonce from the manifest, attach it automatically, and answer the nonce-only challenge.`

**Edit 7 (§4.7.6 **Level reading requirements:**, Basic bullet, first sentence).** Replace `The runtime does not need to read the adapter manifest for core operation — it operates purely on stdin/stdout.` with the sentence below. SPEC-2 Edit 4 rewrites the same bullet's last sentence, and the two edits do not overlap.

```markdown
For core operation the runtime reads only `mcpNonce`, and only when it dials `CH-MSGSOCK` as a socket ([§4.7.11](#4711-adapter-agent-security-boundary) item 1, **Runtime connection handshake.**).
```

**Edit 8 (§4.7.7 **Basic** bullet).** Delete the sentence `Zero Lenny knowledge required.`

**Edit 9 (§4.7.11 item 7 **MCP server security:**).** Replace `The same manifest-nonce handshake used on the CH-RUNTIMEOPS (see item 1) is required` with `A manifest-nonce handshake is required`.

**Edit 10 (§15.4.3 **Basic** bullet list).** Replace the bullet `Zero Lenny knowledge required beyond the above message types` with:

```markdown
- Zero Lenny knowledge required beyond the above message types and, on a socket connection, the connection handshake ([Section 4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1)
```

**Edit 11 (§15.4.3 **Authentication.** bullet).** Delete `, identical in mechanism to the CH-RUNTIMEOPS handshake ([Section 4.7](04_system-components.md#47-runtime-adapter), item 1)`.

**Edit 12 (§15.4.4 Full-level pseudocode).** After the line `lc = unix_connect(manifest.runtimeOps.socket)  // @lenny-runtime-ops`, insert:

```
    lc.send_line(json({"_lennyNonce": nonce}))  // connection handshake, §4.7.11 item 1
```

**Edit 13 (§29.2 step 27).** Replace `as the first message on channel open ([§4.7](04_system-components.md#47-runtime-adapter))` with `as the first frame after the connection handshake ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1)`, keeping the hard wrap.

## Spec files touched

- `spec/04_system-components.md` (SPEC-1, SPEC-2, SPEC-8)
- `spec/06_warm-pod-model.md` (SPEC-2)
- `spec/08_recursive-delegation.md` (SPEC-2)
- `spec/10_gateway-internals.md` (SPEC-2)
- `spec/15_external-api-surface.md` (SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-6, SPEC-8)
- `spec/16_observability.md` (SPEC-2)
- `spec/28_communication-channels.md` (SPEC-2, SPEC-3, SPEC-4, SPEC-8)
- `spec/29_communication-scenarios.md` (SPEC-7, SPEC-8)
