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

### 29.3 Interactive message send

This trace follows one client message from the `POST /v1/sessions/{id}/messages` call that carries it to
the delivery receipt the gateway returns for it. It covers the direct-delivery path, which
[§7.2](07_session-lifecycle.md#72-interactive-session-model) states as path 2: the message carries no
`inReplyTo` that matches an outstanding request, and the target runtime is available because its adapter
reports `ready_for_input`. §7.2 owns the delivery precedence chain and states every other path, together
with the deployment, pool, and session-state conditions under which each one is selected. This trace cites
§7.2 for that branching rather than reproducing it, and names the alternative in one sentence at each step
whose behaviour differs under another path. The steps are numbered and written in the form §29.1 fixes.

**Preconditions.** The session is `running`, its pod is claimed and bound, and the startup sequence §29.2
traces has completed, so the adapter has delivered a first `message` on `CH-MSGSOCK` and the runtime is
reading from it ([§7.1](07_session-lifecycle.md#71-normal-flow),
[§7.2](07_session-lifecycle.md#72-interactive-session-model)).

1. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
   `POST /v1/sessions/{id}/messages`, which is the unified endpoint for all content delivery
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)). The §28.3 registers carry no entry for this surface,
   so the step names the surface in place of a channel identifier and a boundary value, per §29.1. The
   replica the call lands on is selected by the load balancer, and sticky routing is an optimization
   rather than a guarantee: a client can reach any replica
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). When the replica serving the call is not the
   session's coordinating replica and the message carries `delivery: "immediate"` against a `suspended`
   session, §7.2 requires the serving replica to forward the message to the coordinator and states the
   inbox-buffering fallback when the coordinator is unreachable
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model)).

2. `gateway`, `internal`. The gateway authenticates the caller and authorizes the operation against the
   RBAC permission matrix, under which a caller holding the `user` role reaches only their own sessions
   unless a tenant-admin has granted otherwise
   ([§10.2](10_gateway-internals.md#102-authentication)).

3. `gateway`, `internal`. The gateway loads the target session and applies the state and runtime
   preconditions the endpoint states. The endpoint accepts any non-terminal state, and delivery semantics
   vary by state ([§15.1](15_external-api-surface.md#151-rest-api)). An external client message against a
   pre-running session (`created`, `ready`, `starting`, or `finalizing`) is rejected with
   `TARGET_NOT_READY`, HTTP 409, because such a session has no inbox; an inter-session message against the
   same session is instead buffered in the target's dead-letter queue
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)). A message against a session whose runtime declares
   `injection.supported: false` is rejected with `INJECTION_REJECTED`, HTTP 403
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)).

4. `gateway`, `internal`. The gateway validates the message envelope, which is a `MessageEnvelope`
   ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The `delivery` field is a
   closed enum whose defined values are `immediate` and `queued`, an absent value is the same as `queued`,
   and an unrecognized value is rejected with HTTP 400 `INVALID_DELIVERY_VALUE`
   ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification),
   [§15.1](15_external-api-surface.md#151-rest-api)).

5. `gateway`, `internal`. The gateway evaluates the delivery paths in the order §7.2 lists them, and the
   first matching path wins ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). This trace
   follows path 2, under which no pending request matches and the runtime is available because its adapter
   reports `ready_for_input`. On a message whose `inReplyTo` matches an outstanding `lenny/request_input`
   call on the target session, path 1 is selected instead, and the gateway resolves the blocked tool call
   directly, with no delivery to the pod and no interrupt, a receipt status of `delivered`, and none of
   steps 6 through 11 below ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). Every other path is stated
   in §7.2, which names the conditions that select it, the interrupt or buffering behaviour it carries, and
   the receipt status it produces ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). On a pod
   whose pool sets `sessionPolicy.maxConcurrentSessions > 1`, path evaluation is performed per slot rather
   than per pod, the target slot is resolved from the addressed session before routing proceeds, and a
   message that does not resolve to an active slot fails closed internally and is never routed
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

6. `gateway` → `adapter`, `CH-ATTACH`, `gateway-to-pod`. The gateway sends the message envelope to the pod
   on the attach stream, whose §28.3 register row carries message delivery and agent output as its message
   vocabulary and whose messages are bidirectional (§28.5.1 `CH-ATTACH`, §28.3). The envelope carries the
   gateway-injected `schemaVersion`, and it carries `slotId` only on a pod whose pool sets
   `sessionPolicy.maxConcurrentSessions > 1`
   ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification),
   [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). On an
   inter-session message carried by `lenny/send_message` rather than by the client REST surface, the
   `PreMessageDelivery` interceptor phase fires before this delivery, with the serialized message body as
   its content, and the target session's effective `contentPolicy.maxInputSize` limit is enforced at the
   same point ([§4.8](04_system-components.md#48-gateway-policy-engine)). The channel is
   restricted to the session's coordinating replica by the coordination lease `REG-COORDLEASE` and the
   `coordination_generation` stamp the pod validates on every gateway-to-pod RPC (§28.5.1 `CH-ATTACH`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

7. `adapter` → `runtime`, `CH-MSGSOCK`, `intra-pod`. The adapter writes the `message` as a single JSON
   object terminated by a newline and flushes it before the runtime blocks on its next read, populating
   `from.kind` and `from.id` from execution context and overwriting any runtime-supplied `from` (§28.5.3
   `CH-MSGSOCK`, [§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).

8. `unstated`. The specification does not state which channel and which message carry the adapter's report
   that the runtime is `ready_for_input` and its acknowledgement that the runtime consumed the written
   message, which are the two signals path 2 turns on and against which the delivery timeout of step 13 is
   measured. §7.2 requires the adapter to emit `ready_for_input` only once every dispatched tool call has
   settled and the runtime is reading from its input
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model)), while the §28.5.3 `CH-MSGSOCK` card and
   the §28.5.2 `CH-ADAPTEREVENTS` card state the messages those channels carry and neither states a
   message carrying either signal (§28.5.2, §28.5.3).

9. `runtime` → `adapter`, `CH-MSGSOCK`, `intra-pod`. The runtime emits a `response` carrying its parts in
   an `output` array. A `response` may be preceded by one or more `tool_call` messages, each of which the
   adapter answers with a `tool_result` whose `id` matches the emitted `tool_call`; results may arrive in
   any order, and a `tool_result` whose `id` is unknown is dropped and logged as a protocol error
   (§28.5.3 `CH-MSGSOCK`, [§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).

10. `adapter` → `gateway`, `CH-ATTACH`, `gateway-to-pod`. The adapter returns the agent output to the
    gateway on the same attach stream, whose §28.3 row records message authority on both sides and which
    the gateway proxies between the client and the pod (§28.5.1 `CH-ATTACH`, §28.3,
    [§7.1](07_session-lifecycle.md#71-normal-flow)). The step names the boundary value the §28.3 register
    carries for the channel, which is `gateway-to-pod` and records the boundary the channel sits on rather
    than the direction of this step, per §29.1.

11. `gateway`, `internal`. The `PostAgentOutput` interceptor phase fires on the agent's output before the
    gateway delivers it to the client or to the parent session in a delegation. An interceptor at that
    phase may modify, redact, or truncate the output, may remove or replace individual `MessagePart`
    entries, and may not suppress delivery entirely; suppression is expressed as a rejection
    ([§4.8](04_system-components.md#48-gateway-policy-engine)).

12. `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
    ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway records the delivered message in
    the session's message DAG store, the `session_messages` table, which clients read through
    `GET /v1/sessions/{id}/messages`
    ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification),
    [§15.1](15_external-api-surface.md#151-rest-api)).

13. `gateway` → `client`, no register entry, the client-to-gateway session REST surface. The gateway
    returns the delivery receipt, which carries the message identifier, the status, and the fields the
    status selects ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). On this path
    the status is `delivered` only after the runtime's consumption is confirmed within the delivery
    timeout, default 30 seconds; when it is not confirmed within that timeout the message falls through to
    inbox buffering and the status is `queued` instead
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). The remaining status values, and the
    paths that produce each of them, are stated in §7.2 and in §15.4
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
    [§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).

### 29.4 Interrupt, terminate, and delete

This trace follows the three verbs that stop agent work: the interrupt signal that pauses a running
session, and the terminate and delete calls that end one. The interrupt path suspends the session and
leaves the pod held, so steps 1 through 9 end with the session in `suspended`. The session-end path is
the same funnel for `POST /v1/sessions/{id}/terminate`, for `DELETE /v1/sessions/{id}`, and for the
platform timers that expire a session, which differ in the terminal state they write and in whether a
client is waiting on a response; steps 10 through 18 trace it. The two paths are alternatives rather
than a sequence, so every step opens with the verb under which it holds. The steps are numbered and
written in the form §29.1 fixes.

**Preconditions.** The session is attached to a claimed pod and the startup sequence §29.2 traces has
completed, so the runtime is running and the gateway replica coordinating the session holds the session's
coordination lease `REG-COORDLEASE` ([§7.1](07_session-lifecycle.md#71-normal-flow),
[§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). The interrupt path additionally requires
the session to be `running`, which is the only state the interrupt endpoint's precondition table admits
([§15.1](15_external-api-surface.md#151-rest-api)).

1. On `POST /v1/sessions/{id}/interrupt`: `client` → `gateway`, no register entry, the
   client-to-gateway session REST surface. The client interrupts the agent's current work, which is a
   lifecycle signal rather than content delivery
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)). The §28.3 registers carry no entry for this surface,
   so the step names the surface in place of a channel identifier and a boundary value, per §29.1. The
   same verb is reachable on the MCP API as the `interrupt_session` tool
   ([§15.2](15_external-api-surface.md#152-mcp-api)).

2. On `POST /v1/sessions/{id}/interrupt`: `gateway`, `internal`. The gateway authenticates the caller and
   authorizes the operation against the RBAC permission matrix
   ([§10.2](10_gateway-internals.md#102-authentication)), then applies the endpoint's precondition table,
   which admits `running` and produces `suspended`. The call is not valid in `suspended`, `starting`,
   `finalizing`, or any terminal state, and is rejected with `409 INVALID_STATE_TRANSITION` against a
   terminal row ([§15.1](15_external-api-surface.md#151-rest-api)).

3. On `POST /v1/sessions/{id}/interrupt`: `gateway`, `internal`. The gateway resolves the pod the session
   is bound to from the session row's `pod_assignment` column, which records the session-to-pod binding
   ([§4.2](04_system-components.md#42-session-manager),
   [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

4. On `POST /v1/sessions/{id}/interrupt`: `gateway` → `adapter`, no register entry, the internal control
   API ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway calls
   the `Interrupt` RPC, which interrupts the agent's current work
   ([§4.7](04_system-components.md#47-runtime-adapter),
   [§7.2](07_session-lifecycle.md#72-interactive-session-model)). The §28.3 channel register places the
   channels `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, and `CH-PODHEALTH` on the
   gateway-to-pod boundary (§28.5.1) and carries no entry for this RPC, so the step names the internal
   control API in place of a channel identifier and a boundary value, per §29.1. The pod validates the
   `coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one, and a replica that
   has just acquired coordination must receive a successful `CoordinatorFence` acknowledgement before it
   sends any operational RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1).

5. On `POST /v1/sessions/{id}/interrupt`: `agent pod`, `internal`. The adapter takes its pod-level
   operation lock, which serializes `Checkpoint` and `Interrupt` across the pod's slots. An interrupt that
   arrives during a checkpoint is queued until the checkpoint completes or times out; an interrupt that
   arrives while another interrupt is already queued is dropped with a `BUSY` status and the gateway
   retries it with backoff. While an interrupt is pending on a concurrent-session pod it holds the
   whole-pod queue, so any further checkpoint or interrupt is dropped with a `BUSY` status
   ([§4.7](04_system-components.md#47-runtime-adapter), §28.6).

6. On `POST /v1/sessions/{id}/interrupt` against a Full-level runtime: `adapter` → `runtime`,
   `CH-RUNTIMEOPS`, `intra-pod`. The adapter writes `interrupt_request`, carrying `interruptId` and
   `deadlineMs`, which asks the runtime to reach a safe stop point within `deadlineMs`, and the runtime
   replies with `interrupt_acknowledged` carrying the same `interruptId` on the same channel (§28.5.3
   `CH-RUNTIMEOPS`, [§4.7](04_system-components.md#47-runtime-adapter),
   [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). When
   `interrupt_acknowledged` does not arrive within `deadlineMs`, the adapter transitions the session to
   `suspended` anyway and reports an `INTERRUPT_TIMEOUT` status
   ([§4.7](04_system-components.md#47-runtime-adapter), §28.5.3). A Basic-level or Standard-level runtime
   opens no `CH-RUNTIMEOPS` channel, so this step does not occur and the interrupt degrades to
   SIGTERM-based termination with no opportunity for the runtime to reach a safe stop point
   ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).

7. On `POST /v1/sessions/{id}/interrupt`: `adapter` → `gateway`, no register entry, the internal control
   API ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The adapter returns
   the `Interrupt` RPC response, which carries an `INTERRUPT_TIMEOUT` status when the runtime did not
   acknowledge within the frame's `deadlineMs`. The gateway logs the timeout and proceeds with the
   `suspended` state normally ([§4.7](04_system-components.md#47-runtime-adapter), §28.5.3).

8. On `POST /v1/sessions/{id}/interrupt`: `gateway` → `postgres`, no register entry, the Postgres
   `SessionStore` role ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway writes the
   `running → suspended` transition on the session row, which is where the session lifecycle state lives
   and from which the session API reads it
   ([§7.1](07_session-lifecycle.md#71-normal-flow),
   [§7.2](07_session-lifecycle.md#72-interactive-session-model)). Entering `suspended` pauses the
   `maxSessionAge` timer and the idle clock, and the gateway persists the accumulated age on the
   transition ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).

9. On `POST /v1/sessions/{id}/interrupt`: `gateway` → `client`, no register entry, the client-to-gateway
   session REST surface. The gateway returns the session in `suspended`
   ([§15.1](15_external-api-surface.md#151-rest-api)). The pod is held and the workspace is preserved
   while the session is suspended; the pod is released when `maxSuspendedPodHoldSeconds` fires, after
   which the session stays `suspended` without a pod until the client acts
   ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).

10. On `POST /v1/sessions/{id}/terminate` or `DELETE /v1/sessions/{id}`: `client` → `gateway`, no register
    entry, the client-to-gateway session REST surface. `POST /v1/sessions/{id}/terminate` is the graceful
    end and is valid in any non-terminal state, and it records `completed`; `DELETE /v1/sessions/{id}`
    force-cancels the session from any non-terminal state and records `cancelled` for audit and billing
    ([§15.1](15_external-api-surface.md#151-rest-api),
    [§7.2](07_session-lifecycle.md#72-interactive-session-model)). The same two verbs are reachable on the
    MCP API as the `terminate_session` and `cancel_session` tools
    ([§15.2](15_external-api-surface.md#152-mcp-api)). On an expiry there is no client call: the gateway's
    own evaluation of `maxSessionAge` or of the client idle clock fires the transition to `expired`, and
    the session end proceeds from step 11 with that terminal state in place of `completed` or `cancelled`
    ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).

11. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway seals the
    workspace, exporting the final workspace snapshot to the Artifact Store before the pod is released
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.5](04_system-components.md#45-artifact-store)). When the export fails the pod is held in
    `draining` and the export is retried with exponential backoff within
    `maxWorkspaceSealDurationSeconds`, after which the session goes to `failed` with reason
    `workspace_seal_timeout`, the pod is terminated anyway, and the `WorkspaceSealStuck` alert fires
    ([§7.1](07_session-lifecycle.md#71-normal-flow)). The specification states the seal as a
    gateway-to-pod step of the normal flow and does not state which RPC or channel carries it.

12. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway calls
    `Terminate`, the graceful end-of-session shutdown of the pod's runtime. On the default disposition the
    adapter closes the session runtime and the pod is replaced. On the recycle disposition, which applies
    when occupancy reaches zero on a recycling pod, the request also carries the pod identity and the
    whole-pod scrub parameters, and the adapter keeps the pod process alive across the recycle boundary
    and runs the scrub asynchronously ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter),
    [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

13. On a session end against a Full-level runtime, triggered by `POST /v1/sessions/{id}/terminate`, by
    `DELETE /v1/sessions/{id}`, or by an expiry timer: `adapter` → `runtime`, `CH-RUNTIMEOPS`,
    `intra-pod`. The adapter writes the `terminate` frame, carrying `deadlineMs` and a `reason` drawn from
    `session_complete`, `budget_exhausted`, `eviction`, and `operator`. The runtime must exit within
    `deadlineMs`, and the adapter sends SIGTERM on timeout (§28.5.3 `CH-RUNTIMEOPS`,
    [§4.7](04_system-components.md#47-runtime-adapter)). A Basic-level or Standard-level runtime opens no
    `CH-RUNTIMEOPS` channel, so this step does not occur and shutdown is SIGTERM-based
    ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).

14. On a session end of a delegation child session, triggered by `POST /v1/sessions/{id}/terminate`, by
    `DELETE /v1/sessions/{id}`, or by an expiry timer: `adapter` → `gateway`, `CH-ADAPTEREVENTS`,
    `pod-to-gateway`. The child's adapter pushes `FINAL_USAGE_REPORT` once every in-flight `ReportUsage`
    pull has settled, as the final message before the stream closes, and the gateway waits for it or for
    the stream close, whichever comes first, before it returns the child's delegation budget (§28.5.2
    `CH-ADAPTEREVENTS`, [§4.7](04_system-components.md#47-runtime-adapter),
    [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).

15. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
    ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway marks the session terminal,
    persists the final state, and records the artifact references
    ([§7.1](07_session-lifecycle.md#71-normal-flow)). The terminal state is `completed` for the terminate
    verb, `cancelled` for the delete verb, and `expired` for an expiry timer
    ([§15.1](15_external-api-surface.md#151-rest-api),
    [§7.2](07_session-lifecycle.md#72-interactive-session-model)). On the terminal transition the gateway
    drains the session's inbox and its dead-letter queue, emitting a `message_expired` event with
    `reason: "target_terminated"` on each sender's event stream, evaluates `cascadeOnFailure` for the
    session's children, and emits the session's terminal event
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
    [§7.3](07_session-lifecycle.md#73-retry-and-resume)).

16. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway`, `internal`. The gateway releases the session's credential lease back to
    the pool ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.9](04_system-components.md#49-credential-leasing-service)). Credentials are leased per session
    rather than per pod, so a recycling pod releases its lease before the next session begins
    ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).

17. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway` → `control plane`, `REG-CLAIM`, Kubernetes API. The gateway releases the
    pod. On the default disposition the claim is deleted and the pod projects `draining` and then
    `terminated` ([§6.2](06_warm-pod-model.md#62-pod-state-machine),
    [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.3). On the recycle
    disposition the gateway patches the claim's binding state from `bound` to `recycling`, the whole-pod
    scrub runs while the pod projects `claimed`, the adapter reports its outcome with `ReportPodScrub`,
    and the gateway then patches the claim to `reserved`, coordinates the SDK re-warm on a preConnect
    pool, or retires the pod when the recycle limits or the host-node schedulability check say so
    ([§6.2](06_warm-pod-model.md#62-pod-state-machine),
    [§4.7](04_system-components.md#47-runtime-adapter),
    [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

18. On a session end triggered by `POST /v1/sessions/{id}/terminate` or by `DELETE /v1/sessions/{id}`:
    `gateway` → `client`, no register entry, the client-to-gateway session REST surface. The gateway
    returns the terminal session row ([§15.1](15_external-api-surface.md#151-rest-api)). On an expiry
    timer no client call is outstanding, so this step does not occur and the terminal state reaches the
    client through the session API and the session's terminal event instead
    ([§6.2](06_warm-pod-model.md#62-pod-state-machine),
    [§7.2](07_session-lifecycle.md#72-interactive-session-model)).
