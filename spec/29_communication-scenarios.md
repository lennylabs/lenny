## 29. Communication Scenarios

This section carries the end-to-end traces of the platform's communication, each written as a numbered
step list that names every channel it uses by the canonical identifier §28 fixes. §28 states the contract
of one channel at a time; a trace here states the order in which several channels are used to carry one
operation from end to end, so a reader can follow a session start, a message send, or a drain across the
participants without reassembling it from the cards. §29.1 states the participants a trace may name and
fixes the notation every trace uses. The scenario traces follow in the subsections below it.

This section also carries the off-holder matrix, keyed by session-scoped client route, stating what happens
when the replica serving that route is not the replica holding the session's pod control stream. The
client-to-gateway session REST surface is not a channel in the §28 register, so no §28 card owns it, and
the matrix is the normative statement of off-holder behaviour for that surface. Its `delivery: immediate`
resume rows are the exception: they restate the forwarding and inbox-buffering requirement §7.2 states and
cite §7.2 as the section that owns it.

A trace restates behaviour the specification states elsewhere and cites the section that states it. Where
a trace and a cited section disagree, the cited section is the normative statement and the trace is the
defect.

### 29.1 Participants and trace notation

A trace names only the participants this subsection fixes, and writes each step in the form this
subsection fixes. The scenarios that follow are comparable step by step only when they are written the
same way, so the notation is fixed once here rather than restated per scenario.

This subsection states a convention rather than a behaviour. Every participant it names, and every
boundary a step is written against, is defined by the §28 register or by the specification section named
in the table, and this subsection introduces neither a participant nor a boundary of its own.

**Participants.** A step names one of these, in the spelling this table gives it.

| Participant | What it is | Where the specification defines it |
|:--|:--|:--|
| `client` | The external caller that reaches the platform on the REST API or on the MCP API | [§15.1](15_external-api-surface.md#151-rest-api), [§15.2](15_external-api-surface.md#152-mcp-api) |
| `gateway` | The edge gateway as one logical component, named when a step does not turn on which replica performs it | [§4.1](04_system-components.md#41-edge-gateway-replicas) |
| `replica A`, `replica B` | One gateway replica each, named when a step turns on which replica performs it. `replica A` is the replica holding the session's coordination lease `REG-COORDLEASE` unless the trace states otherwise | [§4.1](04_system-components.md#41-edge-gateway-replicas), [§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3 |
| `agent pod` | The pod carrying the adapter container and the runtime container for a session, named when a step concerns the pod as a whole rather than one of its processes | [§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) |
| `adapter` | The runtime adapter process inside the agent pod | [§4.7](04_system-components.md#47-runtime-adapter) |
| `runtime` | The runtime process inside the agent pod | [§4.7](04_system-components.md#47-runtime-adapter), [§5.1](05_runtime-registry-and-pool-model.md#51-runtime) |
| `control plane` | The Kubernetes API server together with the pod lifecycle controllers and the drain-readiness admission webhook | [§4.6](04_system-components.md#46-pod-lifecycle-controllers), [§17.2](17_deployment-topology.md#172-namespace-layout), [§12.5](12_storage-architecture.md#125-artifact-store), §28.5.6 |
| `postgres`, `redis`, `object store` | The three stores, each named by the backend the §12.2 storage roles are placed on | [§12.2](12_storage-architecture.md#122-storage-roles) |

A trace that needs two agent pods labels them `agent pod 1` and `agent pod 2`, on the same rule that
distinguishes `replica A` from `replica B`. A trace that names more than one session labels the sessions
`session 1` and `session 2`, and where those sessions are served by one agent pod each label is keyed to
the `slotId` by which that pod multiplexes every slot's stream over the one `CH-MSGSOCK` channel (§28.5.3),
so the labels separate two sessions on one pod as well as two sessions on two pods. No other participant
may be named: a mechanism
that is not one of these is named as a mechanism of the participant that carries it, rather than as a
participant of its own.

**Step numbering.** The steps of a trace are numbered from 1 in one flat sequence, in the order they
occur. Two steps whose relative order the specification does not fix carry the same number with a letter
suffix, as `4a` and `4b`, and the step text states that their order is unfixed. A step that occurs only
under a condition is numbered in sequence and opens with the condition. Numbering restarts at 1 in each
scenario, so a step is cited as the scenario's subsection number together with its step number.

**Conditional steps.** A step that holds only under a condition states that condition in full in its own
text. No step inherits a condition from another step: a step that would otherwise stand under a condition
an earlier step introduced restates that condition rather than relying on its position in the sequence. A
reader of one step alone can therefore tell every condition under which that step holds.

**Step form.** A step that carries a message between two participants is written as the originating
participant, the receiving participant, the identifier of the channel the message is carried on, and the
boundary value that channel carries in the §28.3 channel register, followed by a sentence stating what the
step does and the section that states it. The boundary value is one of the closed set §28.2 fixes, and it
is written in the same spelling the register's boundary column and the §28.5 card subsection carry, so a
step and the card that owns its channel are searchable by the same string.

**Steps that cross no boundary.** A step that happens inside one participant names that participant alone,
names no channel identifier, and is marked `internal`. Marking it distinguishes work a participant does on
its own from work that puts a message on a channel, which is what the failure and degradation behaviour
in §28.8 is keyed by: an `internal` step has no peer to be absent and no transport to fail. A step that
reads or writes a §28.3 register entry mediates two participants with no live connection, so it is written
with the `REG-` identifier of the entry in place of a channel identifier and is not marked `internal`. It
carries no boundary value, because boundary is an axis §28.2 records for channels alone and the §28.3
register-entry register carries no boundary column; it names the entry's store in place of the boundary
value, in the spelling that register's store column carries.

**Identifiers.** A channel is written as its canonical `CH-` identifier, a transport connection as its
`LNK-` identifier, and a register entry as its `REG-` identifier, each in the spelling the §28.3 registers
carry and each in the same spelling everywhere it appears (§28.1 N1, N2, and N4). A channel is never named
by a paraphrase or by a bare noun phrase, including the two phrases §28.1 N3 reserves. An identifier a
step names must resolve to a row of a §28.3 register; a step that needs a conversation no register carries
states that no register entry exists for it and names the surface instead. Such a step carries no boundary
value either, because boundary is an axis §28.2 fixes for the channels the register carries, and the closed
set §28.2 fixes holds no value for a surface outside it; the surface the step names stands in place of both
the channel identifier and the boundary value.

**Citations.** Every step cites the section that states its behaviour, by section number or by a link to
the heading that owns it, and never by a line number (§28.1 N8). A step that restates a §28.5 contract
card cites the card by its subsection number together with the channel identifier, which is the card's
citable handle.

**Steps the specification does not state.** Where a trace reaches a point at which no section states what
happens, the step records that the specification does not state it, in those words, and is marked
`unstated`. A trace supplies no plausible behaviour for such a step, on the same rule the §28.5 cards and
the §28.8 matrix carry: a reader cannot afterwards distinguish a behaviour the specification fixes from one
a trace invented. An `unstated` step keeps its number and its position in the sequence, so the gap is
visible where it occurs rather than omitted from the trace. A step whose mechanism is specified and not
implemented is stated normally and carries its implementation status in the §28.4 claim register, which is
a separate case from an `unstated` step.

### 29.2 Session start

This trace follows one session from the client request that creates it to the point at which the agent is
running inside its pod and able to receive a message. It covers a session-mode pool
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); service mode
creates no claim, materializes no workspace, and is outside this trace. The steps are numbered and written
in the form §29.1 fixes.

**Preconditions.** The pod the session is dispatched onto is already in the warm pool. Kubernetes created
it, its adapter opened the `LNK-GWCONTROL` connection to a gateway replica, wrote a placeholder manifest,
and signalled READY, at which point the pod entered the warm pool
([§4.7](04_system-components.md#47-runtime-adapter),
[§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). The §28.3 channel register carries no
channel row for the READY signal, and §28.5.2 places one channel on the pod-to-gateway boundary,
`CH-ADAPTEREVENTS`, whose card states the adapter-to-gateway events it carries; the specification does not
state that the READY signal is one of them.

1. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
   `CreateSession` with the runtime, the pool, the retry policy, and metadata
   ([§7.1](07_session-lifecycle.md#71-normal-flow),
   [§15.1](15_external-api-surface.md#151-rest-api)). The §28.3 registers carry no entry for this surface,
   so the step names the surface in place of a channel identifier and a boundary value, per §29.1.

2. `gateway`, `internal`. The gateway authenticates the caller, authorizes the request, and evaluates
   policy ([§7.1](07_session-lifecycle.md#71-normal-flow)).

3. `gateway`, `internal`. The gateway runs the pre-claim credential availability check, computing the
   intersection of the runtime's supported providers and the tenant credential policy's provider pools and
   verifying that at least one provider in the intersection has an assignable credential. When none has, it
   rejects the request with `CREDENTIAL_POOL_EXHAUSTED` and claims no pod
   ([§7.1](07_session-lifecycle.md#71-normal-flow)).

4. `gateway`, `internal`. The experiment router assigns the session to a variant or to control and
   populates the session's experiment context; an assigned variant's pool overrides the default pool
   selection of step 5 ([§7.1](07_session-lifecycle.md#71-normal-flow)).

5. `gateway`, `internal`. The gateway selects the pool and scans its idle inventory for a pod to acquire.
   A pod held under a reserved claim is excluded from that scan
   ([§7.1](07_session-lifecycle.md#71-normal-flow),
   [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

6. On a pod not held for the same tenant under a `reserved` claim within its hold window: `gateway` →
   `control plane`, `REG-CLAIM`, Kubernetes API. The gateway acquires the pod by creating a
   `SandboxClaim` with the deterministic name `claim-<podName>`, carrying `sandboxRef` and `tenantId` in
   its spec. Exactly one replica's `CREATE` succeeds; another receives an `AlreadyExists` conflict or an
   admission rejection from the `lenny-sandboxclaim-guard` webhook, re-reads the idle inventory, and
   retries with a different pod ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle),
   §28.3).

7. `gateway` → `control plane`, `REG-CLAIM`, Kubernetes API. On a pod not held for the same tenant under a
   `reserved` claim within its hold window, the gateway writes the first binding state,
   `bound`, as a status patch, because the status subresource is not writable by the create call
   ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). When the pod is instead
   held for the same tenant under a `reserved` claim within its hold window, this step is a
   `reserved` → `bound` status patch and step 6 does not occur, because the pod is dispatched onto
   with no acquisition round trip
   ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

8. On a pool whose `sessionPolicy.maxConcurrentSessions` is greater than 1: `gateway` → `redis`,
   `REG-SLOTCOUNT`, Redis. The gateway reserves the intra-pod slot with a Lua script that checks the
   counter and increments it only when the result would not exceed `maxConcurrentSessions`, returning a
   slot-unavailable result otherwise. The counter is the intra-pod capacity gate and the per-pod claim
   `REG-CLAIM` guards pod acquisition; a pod whose slots are all taken is skipped and the gateway falls
   through to the next pod in the pool, claiming a further warm pod when every pod is at its limit
   ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), §28.3). During
   a Redis outage the gateway gates capacity on a Postgres active-slot check under a per-pod advisory lock
   ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
   [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)).

9. `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
   ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway persists the session row carrying
   the session identifier, the state, and the `pod_assignment` column that records the session-to-pod
   binding ([§7.1](07_session-lifecycle.md#71-normal-flow),
   [§4.2](04_system-components.md#42-session-manager),
   [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). The §28.3 channel register
   places one channel on the gateway-to-store boundary, `CH-EVENTRELAY`, which carries the session event
   backlog on Redis (§28.5.7), so no register entry covers this write and the step names the store role.

10. `gateway` → `client`, no register entry, the client-to-gateway session REST surface. The gateway
    returns the session identifier, the upload token, and `sessionIsolationLevel`
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§15.1](15_external-api-surface.md#151-rest-api)). Steps 2 through 10 are atomic from the client's
    perspective: a failure at any of them rolls back the pod claim, persists no session row, and returns a
    retryable error, so the client never holds a session identifier for a session that failed to
    initialize ([§7.1](07_session-lifecycle.md#71-normal-flow)).

11. `unstated`. The specification does not state when the replica creating a session acquires that
    session's coordination lease `REG-COORDLEASE`, and it does not state whether that replica announces a
    generation on `CH-FENCE` before its first gateway-to-pod message for a pod it has just claimed.
    [§10.1](10_gateway-internals.md#101-horizontal-scaling) states the acquire-increment-fence sequence for
    a replica that acquires the lease, and the §28.5.1 `CH-ATTACH` and §28.5.2 `CH-ADAPTEREVENTS` cards
    state the coordinating replica as the holder of that lease, without a section stating the initial
    acquisition at session creation.

12. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client uploads
    workspace files and archives with the upload token, which expires at the session creation time plus
    `maxCreatedStateTimeoutSeconds`, default 300s
    ([§7.1](07_session-lifecycle.md#71-normal-flow), [§15.1](15_external-api-surface.md#151-rest-api)).

13. `gateway` → `object store`, no register entry, the Artifact Store
    ([§4.5](04_system-components.md#45-artifact-store),
    [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway buffers the uploaded content for
    the duration of the `created` window, and no pod input or output occurs during it
    ([§7.1](07_session-lifecycle.md#71-normal-flow)).

14. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `FinalizeWorkspace`, which invalidates the upload token on success
    ([§7.1](07_session-lifecycle.md#71-normal-flow)).

15. `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway streams the
    buffered content into the claimed pod with the `PrepareWorkspace` RPC, which accepts the streamed files
    into the staging area ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)). The §28.3 channel register places the channels
    `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, and `CH-PODHEALTH` on the gateway-to-pod
    boundary (§28.5.1) and carries no entry for the workspace and session-start RPCs of steps 15, 16, 17,
    19, and 21, so each of those steps names the internal control API in place of a channel identifier and
    a boundary value, per §29.1.

16. `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The `FinalizeWorkspace`
    RPC validates the staging area and materializes it to `/workspace/current`
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)).

17. `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The `RunSetup` RPC
    carries the setup commands the gateway has validated against the runtime's `setupCommandPolicy`
    ([§4.7](04_system-components.md#47-runtime-adapter),
    [§7.5](07_session-lifecycle.md#75-setup-commands)).

18. `agent pod`, `internal`. The setup commands run after workspace finalization and before session start,
    bounded in time and resources, fully logged, and with network access blocked by default through a
    static NetworkPolicy ([§7.5](07_session-lifecycle.md#75-setup-commands)).

19. `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The
    `AssignCredentials` RPC pushes the per-provider credential map, one lease per authorized provider,
    before session start ([§4.7](04_system-components.md#47-runtime-adapter),
    [§4.9](04_system-components.md#49-credential-leasing-service)). Finalize returns to the client only
    once the session is ready ([§7.1](07_session-lifecycle.md#71-normal-flow)).

20. `agent pod`, `internal`. The credential map is delivered to the agent as a tmpfs-backed file at
    `/run/lenny/credentials.json`, mode `0440`, rather than through environment variables
    ([§4.7](04_system-components.md#47-runtime-adapter),
    [§4.9](04_system-components.md#49-credential-leasing-service)).

21. On a pod-warm pod: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The
    `StartSession` RPC starts the agent runtime with the final `cwd`
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)).

22. On an SDK-warm pod: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). Step 21 does not occur,
    because the session is already connected; the gateway sends the `ConfigureWorkspace` RPC to point the
    pre-connected session at the finalized `cwd`, with a 10s timeout and idempotent semantics for the same
    path. On failure the gateway calls `DemoteSDK` with a 5s timeout and falls back to pod-warm
    materialization, and when `DemoteSDK` also fails the pod transitions to `failed` and a replacement is
    claimed ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)). Steps 23 through 28 are the pod-warm startup
    sequence for `type: agent` runtimes and are stated for that path
    ([§4.7](04_system-components.md#47-runtime-adapter)).

23. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. The adapter writes the final
    manifest, whose connector server entries are now known from the assigned leases
    ([§4.7](04_system-components.md#47-runtime-adapter)). The manifest
    advertises `runtimeOps.socket` for `CH-RUNTIMEOPS`, `platformMcpServer.socket` and `mcpNonce` for
    `CH-MCP-PLATFORM`, and the `connectorServers` array for `CH-MCP-CONNECTOR`, which is empty when no
    connector is authorized and is never absent (§28.5.3,
    [§4.7](04_system-components.md#47-runtime-adapter)).

24. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. The adapter spawns the runtime
    binary ([§4.7](04_system-components.md#47-runtime-adapter)).

25a. On a pod-warm pod with a Standard-level or Full-level `type: agent` runtime: `runtime` → `adapter`,
    `CH-MCP-PLATFORM`, `intra-pod`. The runtime reads the manifest and connects to the platform MCP
    server, presenting the manifest's
    `mcpNonce` as the top-level `_lennyNonce` field of the `initialize` request's `params` object; the
    adapter validates it before any tool dispatch and closes a connection that does not present a valid
    nonce (§28.5.3, [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). A Basic-level runtime connects
    to no MCP server and this step does not occur
    ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).

25b. On a pod-warm pod with a Standard-level or Full-level `type: agent` runtime and at least one
    authorized connector: `runtime` → `adapter`, `CH-MCP-CONNECTOR`, `intra-pod`. The runtime connects to
    each authorized connector's own MCP server, presenting the nonce on each connection separately
    (§28.5.3, [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).

25c. On a pod-warm pod with a Full-level `type: agent` runtime: `runtime` → `adapter`, `CH-RUNTIMEOPS`,
    `intra-pod`. The runtime opens the channel on the abstract Unix socket `@lenny-runtime-ops` the
    manifest advertises, presenting the
    manifest nonce as the first message on the socket; the adapter checks the peer UID with `SO_PEERCRED`
    against the expected agent UID and validates the nonce. A runtime that does
    not open the channel operates in fallback-only mode (§28.5.3,
    [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The specification states steps
    25a, 25b, and 25c as one startup step and does not fix their relative order
    ([§4.7](04_system-components.md#47-runtime-adapter)).

26. On a pod-warm pod with a Full-level `type: agent` runtime: `adapter` → `runtime`, `CH-RUNTIMEOPS`,
    `intra-pod`. The adapter sends `lifecycle_capabilities` as the first message on channel open
    ([§4.7](04_system-components.md#47-runtime-adapter)).

27. On a pod-warm pod with a Full-level `type: agent` runtime: `runtime` → `adapter`, `CH-RUNTIMEOPS`,
    `intra-pod`. The runtime replies with `lifecycle_support`, which is the handshake the gateway reads
    to select the credential-rotation
    strategy for the session ([§4.7](04_system-components.md#47-runtime-adapter)).

28. On a pod-warm pod with a `type: agent` runtime: `unstated`. The specification does not state when the
    runtime opens `CH-MSGSOCK`. §28.3 records the
    runtime as the dialling participant on that channel and §28.5.3 states the transport protections for
    the adapter-agent boundary, while the startup sequence names the manifest read, the MCP connections,
    and the `CH-RUNTIMEOPS` open without naming this channel's open
    ([§4.7](04_system-components.md#47-runtime-adapter), §28.3).

29. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `AttachSession` with the session identifier
    ([§7.1](07_session-lifecycle.md#71-normal-flow), [§15.1](15_external-api-surface.md#151-rest-api)).
    §7.1 places this call after session start and states no ordering between it and the adapter's own
    startup sequence of steps 23 through 28.

30. `gateway` → `adapter`, `CH-ATTACH`, `gateway-to-pod`. The `Attach` RPC connects the client stream to
    the running session over the `LNK-POD-GRPC` connection, and the gateway proxies the bidirectional
    stream between the client and the pod (§28.5.1 `CH-ATTACH`,
    [§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)).

31. `adapter` → `runtime`, `CH-MSGSOCK`, `intra-pod`. The adapter delivers the first `message`, which is
    the last step of the startup sequence and the point at which the agent is running and able to receive
    a message. The preconditions the §28.5.3 `CH-MSGSOCK` card states for that delivery are met at this
    point on a pod-warm pod with a `type: agent` runtime: the adapter has written the final manifest and
    spawned the runtime binary, with the runtime's connection to the MCP servers and the
    `CH-RUNTIMEOPS` capability handshake in between. A Basic-level runtime connects to no MCP server and
    a runtime below Full level opens no `CH-RUNTIMEOPS` channel. On an SDK-warm pod that startup
    sequence does not occur, because the session is already
    connected and the gateway has pointed it at the finalized `cwd` (§28.5.3,
    [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).
