# Decisions: 0139 Runtime protocol

## Decisions

> **Revision 10.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). A statement in this section about connectors, `connector:<id>`, the connector client, connector links, `deferredToolDiscovery`, `tool_progress`, or `toolProgress` that the edits of revision 10 did not rewrite is withdrawn and kept as the record. The platform statements stand.

The owner proposed the move of the platform MCP server and the handshake on 2026-11-11, answered the follow-up questions the same day, settled the adapter-local tools on 2026-11-12, and later the same day removed pre-launched runtimes and settled the open decisions **[design]**.

| ID | Question | Owner's answer | Where |
| :-- | :-- | :-- | :-- |
| D1 | Where the platform MCP server and the connector MCP servers run. | The owner proposed: "Should we move the platform MCP server to the runtime SDK? It could run as an MCP server in the runtime container, using JSONL behind the scenes. That would simplify things significantly for the adapter, I think. A reference MCP interface provided by the SDK (or separate from the SDK but still included in the repo) would still be covered by the spec, since it's not straightforward, but in its own section." On the connector servers: "they should move to the runtime. If the SDK is the right place for an off-the-shelf, reference implementation, I'm fine with that." Tool calls travel as frames, and the SDKs ship the reference bridge. Revision 10: the connector half is superseded by the owner's decision of 2026-11-15 to remove connectors ("Yes, let's drop connectors."), which [`0124-connector-removal`](../0124-connector-removal/change.md) records; the platform half stands. | RP-3, RP-5 |
| D2 | Whether every runtime performs a handshake with a protocol version. | The owner proposed: "Should we add an adapter to runtime protocol version number as part of the/a handshake? For future backward compatibility with older runtimes if the protocol changes. It may exist already. The supported version could/should be in the runtime capabilities definition, but that could get out of sync so it would be good to have a handshake." Then: "I also think a handhsake should be needed even for the most basic runtimes. It's not that big of a deal to implement that on the runtime side." The handshake extends the runtime connection handshake of §4.7.11 item 1 and replaces `lifecycle_capabilities` and `lifecycle_support`. A refused handshake keeps the slot out of service. | RP-1, RP-7 |
| D3 | Whether the integration levels stay. | "yes to 2 and 3. In fact, w.r.t. #3: I don't like how today the docs and spec talk about Basic, Standard, and Full runtimes. I like the idea that runtimes publicize their capabilities, which is more of a spectrum. The only constraint is that some capabilities may depend on others." The levels leave the specification and the documentation, and capabilities with dependency rules take their place. | RP-2, RP-8 |
| D4 | Whether a runtime learns what its session may use, and of changes. | The owner proposed: "The handshake should also include informing the runtime of what is / is not available (for example child sessions, if the lease doesn't allow it). This was done, in part, by dropping MCP tools by the adapter. If what's available changes over time (equivalent of new/dropped MCP tools), then that should be communicated too." The owner answered "yes" to reopening DA O4 for it. Availability travels per session on `session_start` and in `availability_changed`, and the gateway keeps checking every call. | RP-4 |
| D5 | Whether the adapter-local tools stay under session isolation (2026-11-12, formerly O12). | The owner chose option (a): retire them. Under session isolation the adapter reaches a slot's entries only through the supervisor's broker (SIR-8), whose closed operation set (SIR-20) cannot back `write_file`'s overwrite or `delete_file`, and every runtime process already reads, writes, lists, and deletes its own workspace as the slot UID. `read_file`, `write_file`, `list_dir`, `delete_file`, custom adapter-local tools, the `adapterLocalTools` manifest field, and the **Adapter-Local Tool Reference** leave, and tool frames name only `platform` and `connector:<id>`. Revision 10: `connector:<id>` is withdrawn with connectors by [`0124-connector-removal`](../0124-connector-removal/change.md), so tool frames name `platform` alone. | RP-3, RP-6 |
| D6 | Whether runtimes may launch before their session (2026-11-12). | "Pre-launched runtimes (SDK-warm / `preConnect`) are removed entirely. Every runtime launches at bind, after materialization and setup." Removed with them: `preConnect`, `sdkWarmBlockingPaths`, the `sdk_connecting` state and its watchdog, `ConfigureWorkspace` and `DemoteSDK` as separate paths, the demotion circuit breaker and its admin override, the `preconnect-echo` test runtime, and the pod-warm and SDK-warm terminology. "Warm pod" means infrastructure ready. | Scope, "Process model", RP-1, RP-2, RP-5 |
| D7 | The open decisions O1 to O9 and O11 (2026-11-12). | "Go with your recommendations." O1 (a), O2 (d), O3 (b), O4 (a), O5 (a), O6 (e), O7 (a), O8 (b), O9 (a), and O11 (a). O2 (d) is applied with its bind check restated for runtimes that launch at bind (D6). | "Open decisions" |
| D8 | Where a handshake refusal lands now that the runtime launches at bind (2026-11-13). | "RP handshake at bind: accepted." A refusal ends the bind, the slot is released and stays in service, and a refusal that is not retryable stops claims of the pod set's pods. This replaces the consequence D2's row records, that a refused handshake keeps the slot out of service, which held only while runtime processes launched at warm-up. | RP-1, RPR-3 |
| D9 | The frame contract after non-agent runtimes leave (2026-11-13). | The owner removed `type: mcp` runtimes and service mode, and with them `startId` ([`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) D1 and D2), and settled AR's O5 as (b): "O5 (b), yes to all the others." No frame other than `session_start` carries `sessionId`. Asked why `session_start` keeps it, the owner answered "ok, let's keep it": the runtime reads it for log correlation, identity across process restarts, idempotency keys, and derived-session detection, and the adapter never acts on it (AR D5). | Scope, RP-3, RP-4, RP-6, RPR-8, RPR-18, RPR-21 |
| D10 | The state of a session whose resume or restore meets a handshake refusal that is not retryable (O13, 2026-11-13). | "ok" to the recommendation, in the owner's answers to the consolidated list of open decisions of 2026-11-13, which settle O13 and SL-Q2 of [`0146-session-lifecycle`](../0146-session-lifecycle/decisions.md#decisions) together: the session ends `failed`, as "SL-2" of that design states for every failure that is not retryable. | RP-1, RP-2, "Open decisions" |
| D11 | Whether a runtime learns that a long tool call still runs (2026-11-15, item 2.1). | **Superseded on 2026-11-15** by the owner's decision to remove connectors ("Yes, let's drop connectors."), which [`0124-connector-removal`](../0124-connector-removal/change.md) records: `tool_progress`, `toolProgress`, and `gateway.toolProgressMinIntervalMs` have no producer left and are withdrawn. The rule that a tool call has no duration a runtime may assume, and that `tool_cancel` stops waiting for one, stands. The decision as recorded: "2.1: ok": a new adapter-to-runtime frame, `tool_progress`, names the pending `tool_call` and carries the tool server's progress, under a new capability, `toolProgress`. The gateway forwards a connector's MCP progress notifications for a pending call through it, and the reference bridge maps it to MCP progress notifications toward the harness with the harness's `progressToken`. A tool call has no duration a runtime may assume, and a runtime stops waiting for one with `tool_cancel`. | RP-2, RP-3, RP-5, RP-8, RPR-19, RPR-22, RPR-23 |
| D12 | How a reader treats an unknown value of a known enum member (2026-11-15, item 2.2). | "2.2: ok": the value is handled as `other` and is never an error, as "MD-24" of [`0135-message-delivery`](../0135-message-delivery/change.md#md-24-runtime-frames-and-leniency) states under its U1. An added value therefore keeps the protocol version, and the conformance suite tests the rule. | RP-7, RP-8 |

**Gateway seam operations (0117, decision 8).** Settled by the owner on 2026-11-17 in [0117](../0117-gateway-package-layout/decisions.md), which raised the conflict between its seam interface page and this change: a member change that removes, reshapes, or adds a mechanism behind a gateway seam operation edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` in the step that changes the mechanism. In this change that is 0139.S2, whose entries in `steps.md` name the operations.

## Open decisions

> **Revision 10.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). In this section, every statement about `connector:<id>`, `CallConnectorTool`, `ListConnectorTools`, `ListSessionConnectors`, `CH-MCP-CONNECTOR`, connector links, `deferredToolDiscovery`, `tool_progress`, `toolProgress`, and `gateway.toolProgressMinIntervalMs` is withdrawn and kept as the record, and the removal design's own section of the same name governs those items. The platform statements stand.

No decision is open. The owner settled O1 to O9 and O11 on 2026-11-12 with "go with your recommendations" (D7), session isolation retired O10, the owner settled O12 as D5, and the owner settled O13 on 2026-11-13 as D10. Each settled entry keeps its options and its reasoning as a record, and states the option taken.

**O1. The versioning scheme.**

- (a) An integer protocol version, issued only for a change that breaks an existing frame. Additions travel as new optional members, new frame types, and new capabilities, which the leniency rules of "MD-24" already make safe: an unknown member or frame type is ignored. The adapter supports the current version and the previous one.
- (b) A semantic version string, as `lifecycle_capabilities` carries today (`"1.0"`) and as the gateway-to-adapter protocol uses ([§15.4.2](../../spec/15_external-api-surface.md#1542-rpc-lifecycle-state-machine), `INIT`).
- (c) No version, with capabilities alone.

**Recommendation: (a).** A minor version adds nothing that an optional member or a capability does not already say, and (c) leaves no way to retire a frame. The current protocol, with the frames of message delivery revision 14 and this design, becomes version 1, because no deployment exists to stay compatible with. "RP-7" states what happens to a session whose revision's image speaks only a retired version.

**Settled (2026-11-12): (a)**, with the owner's "go with your recommendations" (D7).

**O2. The role of the capabilities a Runtime definition declares.**

- (a) The handshake is the only source. The gateway reads capabilities from the pod after the handshake.
- (b) The runtime revision declares the capabilities, and the adapter checks them at the handshake against a declared set rendered into its configuration.
- (c) The revision declares, and the handshake is advisory.
- (d) The revision declares, the adapter checks only the version and the dependency rules at the handshake and reports the offered capabilities, and the session's effective capabilities are checked at bind: by the gateway against the handshake report stored on the pod's row, and by the adapter against the report of the slot the session binds to.

**Recommendation: (d).** The gateway decides several things before any pod exists, and each reads the declaration: the platform tool list reads `childSessions` ("DA-4"), the link check reads `deferredToolDiscovery` ("CA-4"), client tools read `clientTools.supported` (CT-2), and a message's default intent reads `injection` ("MD-2"). So the declaration stays, which rules out (a), and the owner's concern was drift between the definition and the binary, which rules out (c). (b) fails under RV-1 and RV-3: `capabilities` are gateway-read fields outside the pod hash, so revisions that differ only in capabilities share one pod set, and a set rendered into the pod could hold only one revision's capabilities. Revision 3 placed the gateway's check against a report stored on the pod's row, because runtime processes then launched at warm-up.

**Settled (2026-11-12): (d)** (D7), restated for runtimes that launch at bind (D6). No runtime process exists before its session binds, so no report exists on the pod before the bind. The adapter receives the session's effective capabilities with the bind and checks them against the `hello` of the runtime process it launched for the session, before it writes `session_start`. The gateway keeps the last report of each pod set and checks a session against it before the claim, so a pod set already known to lack a capability is never claimed for it. "RP-1" and "RP-2" state both checks.

**O3. How a stdio bridge reaches the adapter.**

Revision 2 recommended a tool multiplexer in the runtime process, because one connection then carried every frame of a session and because the adapter could not tell which session a second connection acted for. Session isolation changes the second reason: each slot has its own endpoint, the adapter binds every connection to the slot whose name it arrived on and checks the peer UID (SIR-3), every process in the slot container runs as the slot UID, and the slot holds at most one session at a time (D4, D5) **[source]**.

- (a) Through the runtime process. The runtime process runs a tool multiplexer on a filesystem socket in the slot's `/tmp`, which no other container mounts ("SI-2"). Each bridge connects to it, and the multiplexer writes the bridge's frames on the runtime connection, rewrites each frame `id` to one unique on that connection, because several bridges choose their ids independently, routes each answer back to its bridge, and forwards each `availability_changed` to every bridge. Session isolation removes the multiplexer's token, its stamping of `sessionId`, and its separation of sessions.
- (b) Directly. Each bridge opens a tool connection to its slot's `CH-MSGSOCK` endpoint, `@lenny-runtime-<n>`, named by `LENNY_ADAPTER_SOCKET`, as the runtime process does. The adapter binds it to the slot by name and peer UID, as it binds the runtime connection, and the session it acts for is the slot's session. Its `hello` declares `"role": "tools"`, and it carries tool frames alone.

**Recommendation: (b).** Under session isolation a tool connection is identified exactly as the runtime connection is, so the multiplexer adds nothing the adapter does not already know, and (b) removes it. Each tool connection scopes its own frame `id`s, so no rewriting is needed. A runtime written without an SDK, such as a shell script that wraps a CLI, then serves platform and connector tools to a stdio harness by starting the bridge, which (a) could not do without a multiplexer ("O4"). The activity window, the seal, and the cancellation of L24 already act per slot under session isolation, so they cover every connection of the slot. The cost is order: a frame on a tool connection and a frame on the runtime connection reach the adapter in the order they arrive, as `CH-MCP-PLATFORM` calls and stdout frames do today. "MD-22"'s H1 rule, which places a `send_update` after every runtime-connection frame the adapter has read and answers the call only then, keeps its guarantee that the update precedes the turn's end, because the harness cannot end the turn before the call returns (RPR-16). (a) gave no stronger guarantee: the multiplexer could order only what the runtime process had already read from the harness. The developer loop's executor serves stdin and stdout and has no socket for a bridge, so under (b) it binds a filesystem socket for tool connections and names it in `LENNY_ADAPTER_SOCKET` ("O5").

**Settled (2026-11-12): (b)** (D7). The multiplexer of (a) is not built.

**O4. Where the stdio bridge ships.**

- (a) With the runtime SDKs, inside the runtime image, and as a static release binary for runtimes written without an SDK. "CA-9"'s `lenny-tools` image, init container, volume, and webhook rule are withdrawn.
- (b) As "CA-9" ships it: the platform delivers `lenny-mcp-stdio` to every sidecar pod through an init container, mounted into every slot container.

**Recommendation: (a).** Under O3 (b) a bridge performs its own handshake, so the adapter and the bridge agree on a version whichever ships it, and the reason "CA-9" gave for platform delivery, keeping the bridge in step with the adapter, is met by the handshake. (a) saves the second of pod startup that "CA-9" measured for its init container **[measured]**, and the per-slot mounts that session isolation would add for its volume ("SI-7"). An image whose runtime process is a shell script needs only the release binary in its image.

**Settled (2026-11-12): (a)** (D7). "CA-9"'s `lenny-tools` image, init container, volume, and webhook rule are withdrawn.

**O5. The transport of `CH-MSGSOCK`.**

The `CH-MSGSOCK` card says both that the adapter "communicates with the agent binary over stdin and stdout" and that the sidecar deployment uses abstract Unix sockets, and it names no socket (§28.5.3) **[source]**. The tree uses both: the sidecar adapter binds an abstract socket that the runtime finds through `LENNY_ADAPTER_SOCKET` (`pkg/adapter/socketruntime.go`), and `executor.SubprocessExecutor` runs the runtime as a child over stdin and stdout (`pkg/gateway/session/executor/subprocess.go`, "the `make run` developer-loop executor and the substrate the conformance harness exercises"), which `cmd/lenny-gateway/runtime_select.go` and `cmd/lenny-adapter/main.go` construct **[source]**. Session isolation names the sidecar endpoint `@lenny-runtime-<n>` per slot ("SI-2") **[source]**.

- (a) State both transports: the per-slot abstract socket named by `LENNY_ADAPTER_SOCKET` in the sidecar model, and stdin and stdout in the developer loop and the conformance harness, where the executor also binds a filesystem socket for tool connections under O3 (b). The framed protocol and the handshake are the same on both, and only a socket carries the nonce line.
- (b) Make the socket the only transport and move the developer loop onto it.

**Recommendation: (a).** It states what the tree does. Moving tools and, under O7 (a), the lifecycle frames onto `CH-MSGSOCK` removes the transport obstacle that limits Source Mode on macOS to Basic today ([§15.4.3](../../spec/15_external-api-surface.md#1543-runtime-integration-levels), "Platform compatibility note"), because a filesystem socket works on macOS. Source Mode gains a capability only once the subprocess executor implements it: the handshake, tool forwarding and its tool socket, availability, and the checkpoint and interrupt frames. Proposal RP-C carries that work, and until it lands Source Mode states the capabilities its executor supports.

**Settled (2026-11-12): (a)** (D7).

**O6. Which running sessions a change of availability reaches, and when.**

A session's availability depends on the delegation settings ("DA-2"), the pool's `elicitationDepthPolicy` ("DA-4"), connector links ("CA-8"), and connector access ("CA-3"). Delegation availability revision 3 listed the cost of pushing changes: "a per-session list in the adapter, a gateway-to-adapter RPC, a fan-out from every admin write path and from the SO-2 applier" ("DA-5", **Blast radius.**).

- (a) Each write path finds the live sessions it affects and notifies each session's coordinating replica.
- (b) Each write path publishes one change event with its scope (tenant, environment, runtime, or pool) to every replica. Each replica recomputes the availability of the sessions it coordinates within that scope and notifies a session only when the result differs from what it last sent.
- (c) The coordinating replica recomputes the whole availability, tool servers included, at each turn's start and at each `session_start`, and notifies the runtime when it differs. A completed connector link also notifies at once.
- (d) At each turn's start the coordinating replica sends a hint naming every tool server, without computing availability, and the harness lists again, so "DA-4"'s live list decides.
- (e) At each turn's start the coordinating replica recomputes only `delegation` and `questions`, from the inputs "DA-4" already reads, and notifies when either differs, naming the `platform` server. Connector state changes only through the events "CA-8" already designs, which notify at once, and through each `session_start`.

**Recommendation: (e).** Recomputing `toolServers` each turn, as (c) does, would run "CA-4"'s link check for each connector each turn, and "CA-4" probes a connector for a user with no token. (d) costs a list of every server by every harness each turn, and a connector list reaches the external server. (e) reads per turn only what "DA-4" reads per list, touches no admin write path, and leaves connector changes to the events that exist for them. Under every option the notice cannot be guaranteed to precede the turn's first model call: a turn the runtime opens with its own output ("MD-4") has no first message for the gateway to write the notice before, and a runtime may make a model call before it reads a frame the adapter has written. RPR-11 states what the design guarantees.

**Settled (2026-11-12): (e)** (D7).

**O7. Whether `CH-RUNTIMEOPS` merges into `CH-MSGSOCK`.**

- (a) Merge. The frames of `CH-RUNTIMEOPS` travel on the runtime connection, sent only when the capability that needs them is effective for the session. The second socket, the `runtimeOps` manifest field, and the channel leave.
- (b) Keep the second socket, opened by a runtime that declares `checkpoint`, `interrupt`, or `credentialRotation`.

**Recommendation: (a), more firmly than in revision 2.** The handshake on `CH-MSGSOCK` already carries the capabilities that `lifecycle_support` carried, so the second socket keeps only a transport. Session isolation names per-slot endpoints for `CH-MSGSOCK` and `CH-SUPERVISE` only, and checks peers and closes the squatting windows for those names ("SI-2") **[source]**. Today's `@lenny-runtime-ops` is one pod-wide name, which under slot containers every slot could reach or bind. Under (b) session isolation would need a per-slot `@lenny-runtime-ops-<n>` with its own peer checks and its own drain between End and launch; under (a) none of that exists. One connection also gives a runtime one reader loop, works over stdin and stdout (O5), and orders a session's stop signal against its frames: under "SL-3" a runtime that acknowledges a suspension closes its turn with a `message` and answers `interrupt_acknowledged`, which today cross two connections in no stated order. Every runtime already reads its connection during a turn, because a `heartbeat` needs a `heartbeat_ack` within 10 seconds whatever the runtime is doing (§28.5.3, **Timing.**) **[source]**. The cost is that a large frame in flight delays a control frame behind it. A frame at the 50 MiB line limit takes tens of milliseconds on a local socket **[unverified]**, and the deadlines of `checkpoint_request` and `interrupt_request` are in seconds ("Validation owed" in `steps.md`).

**Settled (2026-11-12): (a)** (D7). `CH-RUNTIMEOPS` leaves, and its frames travel on the runtime connection.

**O8. How tool lists reach the runtime.**

- (a) `session_start` and `availability_changed` carry the full tool list of every tool server.
- (b) They carry the tool servers and their state. The runtime lists a server's tools with `list_tools` when it needs them, and a change notice names the servers whose lists changed.

**Recommendation: (b).** "DA-4" computes the platform list at each list, and a connector's list comes from the external server, which "CA-4" probes only for a user with no token. Pushing full lists would make the gateway list every connector at every session start and every change. (b) keeps both designs' listing paths and adds only the notice. A change to an external connector's own tool list produces no notice under either option ("Limits").

**Settled (2026-11-12): (b)** (D7).

**O9. Whether every runtime answers `session_start`.**

Today a runtime that keeps no per-session context and opens no `CH-RUNTIMEOPS` ignores `session_start` and writes no `session_started` (§28.5.3) **[source]**, and the adapter's wait for `session_started` depends on whether `CH-RUNTIMEOPS` completed its handshake first.

- (a) Every runtime answers each `session_start` with `session_started`, and every start path waits for it: the sidecar adapter and the subprocess executor.
- (b) Keep the conditional rule.

**Recommendation: (a).** D2 already asks every runtime to read and write frames beyond `message`, and one answer per session start removes a condition from each start path and from the conformance suite. A runtime that keeps no context answers at once.

**Settled (2026-11-12): (a)** (D7).

**O10. Platform and connector tools on a pod that serves several sessions (retired by session isolation).**

Revision 2 kept today's refusal of platform and connector calls on a pod with several sessions, because those sessions shared one runtime process and one container, and a process in Alice's session could write a frame naming Bob's session and act with Bob's connectors or under Bob's lease, while the gateway "dispatches under the session id the adapter presents" (`CallPlatformTool` in `pkg/gateway/mcpfabric/delegationtree/leasecontrol/platformtools.go`) **[source]**. Session isolation removes each premise: each session runs in its own slot container under its own UID with its own process namespace ("SI-1"), each slot has its own `CH-MSGSOCK` endpoint whose connections the adapter binds to the slot by name and peer UID, so that every frame on them acts for the slot's session (SIR-3), and each slot's manifest and nonce sit on a volume no other slot mounts ("SI-7") **[source]**. Session isolation states the consequence: "The adapter knows which session every frame acts for, so RP O10 can allow platform and connector tools on such pods once this design lands" ("SI-2", **Tools on pods with several slots.**) **[source]**. Under this design no frame other than `session_start` carries a session's identifier, so no frame can name another session ("RP-3", D9).

**Resolution.** Platform and connector tools are available on every pod, pods with several slots included, from proposal SI-A on. Since revision 10 the resolution covers platform tools alone, because [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors. Service mode, the one place where the premise remained, is removed (D9). The embedded deployment model, whose pools session isolation's O6 concerned, is removed ([`0118-agent-pod-layout`, "PL-2"](../0118-agent-pod-layout/change.md#pl-2-removal-of-the-embedded-deployment-model)). The decision needs no answer from the owner, so it is retired, and its label stays for the references session isolation makes to it.

**O11. How the specification retires a channel.**

The naming law has a naming table for renamed channels and an escape hatch for a retired spelling, and no rule for removing a channel ([§28.1](../../spec/28_communication-channels.md#281-naming-law)) **[source]**.

- (a) The retiring proposal removes each channel's register row, its card, its claim-register rows, and its rows in §28.6 and §28.8, and leaves a successor pointer under the channel-register heading that names the channel and the channel that now carries its conversation, as [`spec-citations.md`](../../.claude/rules/spec-citations.md) asks of a section that gives up content.
- (b) Amend the naming law first with a retirement rule, then retire the channels under it.

**Recommendation: (a), with a note to the owner that the naming law may need a retirement rule.** Naming-table rows record a spelling change, and a removed channel has no canonical spelling. Whether the law needs a rule is the owner's call, and (b) is the path if it does. A tool connection adds no channel: it carries a subset of `CH-MSGSOCK`'s frames, on its endpoint, between the same participants, so it takes `CH-MSGSOCK`'s identifier under the naming law's rule of one identifier per conversation ([§28.1](../../spec/28_communication-channels.md#281-naming-law), N4).

**Settled (2026-11-12): (a)** (D7). The owner did not ask for a retirement rule in the naming law, so the successor pointers of (a) carry each retirement.

**O12. The adapter-local tools under session isolation (settled as D5).**

The owner settled this decision on 2026-11-12 with option (a), which retires the adapter-local tools. "Decisions" records it as D5. The options were (a) retiring the tools, (b) keeping them with new broker operations that open an existing file for writing with truncation and remove an entry, and (c) keeping `read_file` and `list_dir` only. The label stays for the references session isolation may make to it.

**O13. The state of a session whose resume or restore meets a handshake refusal that is not retryable (settled as D10).**

Revision 7 of this design moved such a session to `awaiting_client_action` in "RP-1" and "RP-2" and staged that rule for [§7.3 Retry and Resume](../../spec/07_session-lifecycle.md#73-retry-and-resume). The session lifecycle design owns the session states, and its "SL-2" sends a session in any non-terminal state to `failed` on a failure that is not retryable ([`0146-session-lifecycle`, "SL-2"](../0146-session-lifecycle/change.md#sl-2-transitions)) **[source]**. The two rules disagree for this case, and both designs stage a §7.3 statement.

- (a) `awaiting_client_action`, as revision 7 of this design stated. The client can retry the resume once a revision with a fixed image builds a new pod set, or end the session.
- (b) `failed`, as "SL-2" states. A refusal that is not retryable recurs on every pod of the pod set (RV-3), so a resume of the same revision meets it again, and the session ends under the one rule the session lifecycle design applies to every failure that is not retryable.

**Recommendation: (b).** The session lifecycle design owns the states and their transitions, and one rule for every failure that is not retryable keeps §7.3 single-sourced. The session lifecycle design recorded the same question as SL-Q2.

**Settled (2026-11-13): (b)** (D10). "RP-1", "RP-2", and the §7.3 row move such a session to `failed`, and SL-Q2 is settled by the same answer.
