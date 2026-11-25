# Steps: 0139 Runtime protocol

## Validation owed

- The time to write a line at the 50 MiB limit on a local socket, and whether it fits inside the shortest `interrupt_request` deadline (O7). 0139.S1 waits on it.
- Whether the Claude Agent SDK serves in-process MCP servers that a wrapper can back with frames ("RP-5"). 0139.S3 waits on it.
- Whether Claude Code and Codex list the platform server and the `client` server again on `notifications/tools/list_changed` on a stdio server, including for a tool removed that way. The "CA-9" spike saw Claude Code list again for an added tool only and could not measure Codex. The result sizes the stale-list window of "Limits" and changes no enforcement, because the gateway checks every call (RPR-9); [`0126-delegation-availability`](../0126-delegation-availability/steps.md#validation-owed) moved its item here. 0139.S3 waits on it.
- Whether a harness sends MCP `notifications/cancelled` for a call it abandons, so the bridge can write `tool_cancel`. 0139.S3 waits on it.
- Whether Claude Code and Codex apply a timeout to a long platform tool call, such as `events.wait` behind the reference tool `await_sessions`, under which the bridge sees `notifications/cancelled` (D11). 0139.S3 waits on it.
- How many tool connections a real harness opens per slot, to set the per-slot bound `adapter.maxToolConnectionsPerSlot` ("RP-1"). 0139.S2 waits on it.

## Steps

### 0139.S1 Handshake, capabilities, and versioning
- status: Planned
- aliases: [RP-A, RP-1, RP-2, RP-6, RP-7, RP-8]
- lanes: [specification, code, documentation]
- depends-on: [0135.S5, 0146.S1, 0145.S1, 0145.S3, 0118.S2, 0119.S1, 0119.S2, 0147.S6]
- closes: [F-5.1.25, F-5.1.37, T-5.1.1, T-15.4.6.3, T-5.2.16, T-4.7.18]

The handshake per slot with its refusal handling, the capability catalog with its dependency rules and the coverage rule, the bind check at the gateway and the slot, versioning, conformance by capability, the removal of the integration levels from the specification, and the session-from-connection rule of RPR-18 with `sessionId` on `session_start` alone, with O7 and O9 as decided. It also lands the `external_limit` frame of "RP-9" and its `CH-ADAPTEREVENTS` event, whose suspension uses 0146.S1's states (D15). AR's proposal AR-C (0119.S3) removes `startId` and depends on this step. Manifest `version: 2` lands here with the removal of `runtimeOps` ("RP-6", **The version 2 field set and where each change lands.**).

The step carries the runtime-side change for each wire change it makes, so `main` stays green at its merge: `hello`, the lifecycle frames on `CH-MSGSOCK`, `session_started` for every `session_start` (O9), and manifest version 2 in the Go, Python, and TypeScript SDKs, `pkg/runtimekit/handshake.go`, the sample runtimes under `cmd/runtimes/`, and the subprocess executor in `pkg/runtimeexec/subprocess.go`. It adds the `runtime_handshake` event to the gateway's `CH-ADAPTEREVENTS` consumer that 0145.S1 lands (issue CHPODGW-I3). Which step builds that consumer is O15 of [`0145-session-isolation`](../0145-session-isolation/decisions.md), and this sentence follows its answer: under its option (c) this step builds the consumer. The removal of `integrationLevel` closes the findings on the observed level and its cache (F-5.1.25, F-5.1.37, T-5.1.1, T-15.4.6.3, and T-5.2.16), and the merge of `CH-RUNTIMEOPS` removes `RuntimeOps.WaitHandshake` in `pkg/adapter/runtimeops.go`, the subject of T-4.7.18.

The step converts what the steps of [`0135-message-delivery`](../0135-message-delivery/steps.md#steps) build against before it lands, as "MD-2", "MD-4", and 0135.S1, 0135.S4, and 0135.S5 state: the `integrationLevel: full` gate on `interrupt` becomes the `interrupt` capability of "RP-2", `interrupt_request` moves from `CH-RUNTIMEOPS` to `CH-MSGSOCK` (O7 (a)), and the tree's routing of session-scoped frames (an addressed frame to its session, an unaddressed one to the stream's binding while the pod holds at most one slot) becomes RPR-18. Whether 0135.S1 instead waits for 0145.S1 and 0145.S3 and lands the adapter half of RPR-18 for its own frames is O6 of 0135 and O21 here.

It lands after 0135.S5 (MD-E), so the frame tables change once; after 0146.S1 (SL-A), so the lifecycle's level statements are restated once; after 0145.S1 (SI-A), for per-slot endpoints, the slot binding, and per-slot manifests; after 0145.S3 (SI-C), for one session per runtime process, on which RPR-18 and the refusal handling rest; after 0118.S2 (PL-D), which removes `capabilities.preConnect` and lands the refusal of a write that names it, so this step's catalog has no `preConnect`; and after 0119.S1 and 0119.S2, which remove `type: mcp` with `runtimeMcpServers` and service mode, on whose removal RPR-18, D9, and manifest version 2 rest; and after [0147.S6](../0147-tenant-derived-runtimes/steps.md#steps), which adds `cwd` to `session_start` and `StartSessionRequest` under "DR-12". If the owner splits 0146.S1 under SL-Q10 of [`0146-session-lifecycle`](../0146-session-lifecycle/decisions.md), the edge on 0146.S1 moves to the split step that restates the level statements.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step changes no gateway seam operation: the claim-time check, the bind check, the handshake report, and the removal of `GetObservedIntegrationLevel` sit in the session core (`pkg/gateway/core/session/podsession` and `pkg/gateway/core/session/adapterclient`), and the capability validation in `pkg/gateway/api/admin` reads the runtime store, a concern package. No operation of `spec/interfaces/gateway-seams.md` names an integration level or a capability.

### 0139.S2 Tool calls on frames and the reference bridge
- status: Planned
- aliases: [RP-B, RP-3, RP-4, RP-5]
- lanes: [specification, code, documentation]
- depends-on: [0139.S1, 0145.S1, 0126.S1, 0127.S3]
- closes: [F-15.4.8, F-7.2.46]

Tool calls on frames, tool connections under O3, the platform tools on every pod with the removal of the `CH-MCP-PLATFORM` card's "only one session" refusal and its code in `pkg/adapter/platformtoolprovider.go` (O10), availability with `UpdateAvailability`, the removal of `platformMcpServer` and `adapterLocalTools` from manifest version 2, the retirement of `CH-MCP-PLATFORM` under O11, the removal of the adapter-local tools under D5, and the reference bridge section. [0124.S1](../0124-connector-removal/steps.md#steps) owns every connector-specific removal, `CH-MCP-CONNECTOR` and the manifest's `connectorServers` included, and this step lands the `UNKNOWN_SERVER` rule of "RP-3".

The step lands the `delegation` and `questions` values of the availability computation of "RP-4", with the `contract` cases for the `list_tools` frame and `TOOL_NOT_FOUND` and the `e2e_kind` notice test that [0126.S1](../0126-delegation-availability/steps.md#steps) leaves to it. The removal of the adapter's platform MCP server and the bridge's mapping of `TOOL_NOT_FOUND` to `-32602` close F-15.4.8, and the retirement of the adapter-local tools under D5 removes the path of F-7.2.46, an adapter-local `tool_call` whose `approvalRequired` flag `HandleToolCall` does not read. Whether the flag survives on platform calls is O18.

The step carries the runtime-side change for its wire change: the tool-frame helpers in the SDKs in place of their MCP clients, `delegation-echo` and `elicitation-echo` under `cmd/runtimes/` on tool frames, and the subprocess executor's tool socket.

Revision 15 adds the frame rules that [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-7-what-each-change-takes-on) assigns to this change: `server: "client"` with `group` and the adapter's answer of `list_tools` for `client`, the `client` entry of `toolServers` and its `availability_changed` notice, `internal: true` on the `tool_list` entries of the platform operations, `lineage` and `tags` on `session_start` in place of `tracingContext`, and the capability `childEvents` in the catalog. 0166.S1 fills `lineage` and `tags` and marks its operations `internal`, and depends on the part of this step that adds the platform tool server and the listing attribute. The gateway's side of a `client` call lands with the step of [`0122-client-tools`](../0122-client-tools/steps.md#steps) that builds the blocking interface, which depends on this step.

It supersedes the intra-pod part of MP-5 (0134.S3). CA-E, CA-H, and CA-I are withdrawn with connectors by [`0124-connector-removal`](../0124-connector-removal/change.md).

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes, reshapes, and adds no existing operation for the tool frames: the adapter still forwards a platform call with `CallPlatformTool`, and the tool bridge resolves the calling session through `GWSEAM-AttachCallingSession` as today. Per-session tool availability (RP-4) is computed in the session core, which reads "may delegate" from the resolver in `pkg/delegation` that [0126.S1](../0126-delegation-availability/steps.md#steps) adds, a concern package that needs no operation, and reuses for `delegation` the session core operation that 0126.S1 adds to the platform tools seam, so the step adds no operation for `delegation`. It computes `questions` from the `ask_question` rule of "DA-4". When that rule is reachable only inside the platform tools seam, the step adds one operation served by the platform tools seam to the session core that returns a session's `questions` value, and names it in `spec/interfaces/gateway-seams.md`.

### 0139.S3 SDKs, executor, and reference runtimes
- status: Planned
- aliases: [RP-C]
- lanes: [code, documentation]
- depends-on: [0139.S1, 0139.S2]

The in-process bridge in the SDKs, the `lenny-mcp-bridge` binary, the reference runtimes' wrappers, and the documentation of the bridge. The bridge serves the `platform` and `client` servers, hides internal entries (RPR-24), and marks call groups when it runs in process. The session tools of the reference extension, the `sessions` library, `--session-tools`, and `--session-defaults` land in the bridge with [0166.S3](../0166-delegation-primitives/steps.md#steps), which depends on 0139.S2. The SDKs' handshake, the tool-frame helpers, the sample runtimes, and the subprocess executor land with 0139.S1 and 0139.S2, whose wire changes they follow. [0134.S3](../0134-mcp-protocol-upgrade/steps.md#steps) lands on this step's branch and adds the era selection, the version refusal, and the result format of "MP-5" to the bridges of the Go, Python, and TypeScript runtime SDKs and to `lenny-mcp-bridge`, so the bridge never ships at a single MCP version.
