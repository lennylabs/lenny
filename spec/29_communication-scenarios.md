## 29. Communication Scenarios

This section carries the end-to-end traces of the platform's communication, each written as a numbered
step list that names every channel it uses by the canonical identifier §28 fixes. §28 states the contract
of one channel at a time; a trace here states the order in which several channels are used to carry one
operation from end to end, so a reader can follow a session start, a message send, or a drain across the
participants without reassembling it from the cards. §29.1 states the participants a trace may name and
fixes the notation every trace uses. The scenario traces follow in the subsections below it.

This section also carries the off-holder matrix, keyed by session-scoped client route, stating what happens
when the replica serving that route is not the replica holding the session's pod control stream
`CH-ATTACH`. The client-to-gateway session REST surface is not a channel in the §28 register, so no §28
card owns it, and the matrix is the normative statement of off-holder behaviour for that surface. Its `delivery: immediate`
resume rows are the exception: they restate the forwarding and inbox-buffering requirement §7.2 states and
cite §7.2 as the section that owns it.

This section also carries §29.10, the structural analysis of the concurrent-session pod, which states which
part of a pod serving more than one session is partitioned per slot and which part is shared by the whole
pod. It is stated outside the traced form and carries no numbered steps, because a pod serving several
sessions is a condition under which the traces run rather than an operation carried from end to end.

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
in the form §29.1 fixes. The trace follows the route that creates the session with `POST /v1/sessions` and
then calls finalize and start separately. Two other entry points reach the same pod-facing sequence and are
outside this trace: the one-call convenience route `POST /v1/sessions/start`
([§15.1](15_external-api-surface.md#151-rest-api)), and the delegated-child materialization of
[§8.2](08_recursive-delegation.md#82-delegation-mechanism). A delegated child is committed to `created`
before its warm pod is claimed and claims its pod during the §8.2 materialization step
([§15.1](15_external-api-surface.md#151-rest-api)), so the order in which steps 6 through 9 claim the pod
and then persist the session row is the top-level order alone.

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
    19, 22, and 23, so each of those steps names the internal control API in place of a channel identifier and
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

21. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `POST /v1/sessions/{id}/start`, which the endpoint's precondition table admits in `ready` and which
    moves the session through `starting` to `running`
    ([§7.1](07_session-lifecycle.md#71-normal-flow), [§15.1](15_external-api-surface.md#151-rest-api)).

22. On a pod-warm pod: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The
    `StartSession` RPC starts the agent runtime with the final `cwd`
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)).

23. On an SDK-warm pod: `gateway` → `adapter`, no register entry, the internal control API
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). Step 22 does not occur,
    because the session is already connected; the gateway sends the `ConfigureWorkspace` RPC to point the
    pre-connected session at the finalized `cwd`, with a 10s timeout and idempotent semantics for the same
    path. On failure the gateway calls `DemoteSDK` with a 5s timeout and falls back to pod-warm
    materialization, and when `DemoteSDK` also fails the pod transitions to `failed` and a replacement is
    claimed ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)). Steps 24 through 29 are the pod-warm startup
    sequence for `type: agent` runtimes and are stated for that path
    ([§4.7](04_system-components.md#47-runtime-adapter)).

24. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. The adapter writes the final
    manifest, whose connector server entries are now known from the assigned leases
    ([§4.7](04_system-components.md#47-runtime-adapter)). The manifest
    advertises `runtimeOps.socket` for `CH-RUNTIMEOPS`, `platformMcpServer.socket` and `mcpNonce` for
    `CH-MCP-PLATFORM`, and the `connectorServers` array for `CH-MCP-CONNECTOR`, which is empty when no
    connector is authorized and is never absent (§28.5.3,
    [§4.7](04_system-components.md#47-runtime-adapter)).

25. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. The adapter spawns the runtime
    binary ([§4.7](04_system-components.md#47-runtime-adapter)).

26a. On a pod-warm pod with a Standard-level or Full-level `type: agent` runtime: `runtime` → `adapter`,
    `CH-MCP-PLATFORM`, `intra-pod`. The runtime reads the manifest and connects to the platform MCP
    server, presenting the manifest's
    `mcpNonce` as the top-level `_lennyNonce` field of the `initialize` request's `params` object; the
    adapter validates it before any tool dispatch and closes a connection that does not present a valid
    nonce (§28.5.3, [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). A Basic-level runtime connects
    to no MCP server and this step does not occur
    ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).

26b. On a pod-warm pod with a Standard-level or Full-level `type: agent` runtime and at least one
    authorized connector: `runtime` → `adapter`, `CH-MCP-CONNECTOR`, `intra-pod`. The runtime connects to
    each authorized connector's own MCP server, presenting the nonce on each connection separately
    (§28.5.3, [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).

26c. On a pod-warm pod with a Full-level `type: agent` runtime: `runtime` → `adapter`, `CH-RUNTIMEOPS`,
    `intra-pod`. The runtime opens the channel on the abstract Unix socket `@lenny-runtime-ops` the
    manifest advertises, presenting the
    manifest nonce as the first message on the socket; the adapter checks the peer UID with `SO_PEERCRED`
    against the expected agent UID and validates the nonce. A runtime that does
    not open the channel operates in fallback-only mode (§28.5.3,
    [§4.7](04_system-components.md#47-runtime-adapter),
    [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The specification states steps
    26a, 26b, and 26c as one startup step and does not fix their relative order
    ([§4.7](04_system-components.md#47-runtime-adapter)).

27. On a pod-warm pod with a Full-level `type: agent` runtime: `adapter` → `runtime`, `CH-RUNTIMEOPS`,
    `intra-pod`. The adapter sends `lifecycle_capabilities` as the first message on channel open
    ([§4.7](04_system-components.md#47-runtime-adapter)).

28. On a pod-warm pod with a Full-level `type: agent` runtime: `runtime` → `adapter`, `CH-RUNTIMEOPS`,
    `intra-pod`. The runtime replies with `lifecycle_support`, which is the handshake the gateway reads
    to select the credential-rotation
    strategy for the session ([§4.7](04_system-components.md#47-runtime-adapter)).

29. On a pod-warm pod with a `type: agent` runtime: `unstated`. The specification does not state when the
    runtime opens `CH-MSGSOCK`. §28.3 records the
    runtime as the dialling participant on that channel and §28.5.3 states the transport protections for
    the adapter-agent boundary, while the startup sequence names the manifest read, the MCP connections,
    and the `CH-RUNTIMEOPS` open without naming this channel's open
    ([§4.7](04_system-components.md#47-runtime-adapter), §28.3).

30. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `AttachSession` with the session identifier
    ([§7.1](07_session-lifecycle.md#71-normal-flow), [§15.1](15_external-api-surface.md#151-rest-api)).
    §7.1 places this call after session start and states no ordering between it and the adapter's own
    startup sequence of steps 24 through 29.

31. `gateway` → `adapter`, `CH-ATTACH`, `gateway-to-pod`. The `Attach` RPC connects the client stream to
    the running session over the `LNK-POD-GRPC` connection, and the gateway proxies the bidirectional
    stream between the client and the pod (§28.5.1 `CH-ATTACH`,
    [§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)).

32. `adapter` → `runtime`, `CH-MSGSOCK`, `intra-pod`. The adapter delivers the first `message`, which is
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

**Off-holder matrix.** Step 1 states that the replica a client call lands on is selected by the load
balancer, so any session-scoped route can be served by a replica other than the one holding that session's
pod control stream `CH-ATTACH`. The holder is the session's coordinating replica, which is the replica
holding the coordination lease `REG-COORDLEASE` (§28.3,
[§10.1](10_gateway-internals.md#101-horizontal-scaling)). The matrix below states the required behaviour of
the serving replica in that case for the session-scoped client routes its rows name, and it carries one row
per route and session state where a route has more than one off-holder outcome. A session-scoped route that
no row names and that the store-only row does not cover is a condition this matrix does not state. The client-to-gateway session
REST surface is not a channel in the §28 register, so no §28 card owns it and this matrix is the normative
statement of its off-holder behaviour. The matrix applies to the message-send trace above and to every
other trace in this section that begins on that surface.

Two requirements govern the rows whose required outcome is a forward to the coordinator. The serving
replica forwards the request to the session's coordinating replica and the coordinator performs the
effect. On the message-send rows that forward is carried over `LNK-INTERREPLICA`, which is the
inter-replica link the §28.3 link register carries and whose conversation §28.5.4 states as the
cross-replica message routing RPC. On the rows whose forwarded request is not a message send, which are
the interrupt, terminate, delete, resume, interaction-resolution, upload, and events JSON rows below, the
specification states no inter-replica
conversation that carries the request (§28.3, §28.5.4), so those rows require the forward and the
coordinator-performed effect without naming a carrier for them. A serving replica never reports a successful outcome for an effect that
did not reach the pod, and never writes a session state transition that depends on an effect at the pod it
did not perform. When the coordinator is unreachable on one of those rows, a message send falls back to
inbox buffering with a `queued` delivery receipt so the message is not dropped, and every other forwarding
row fails closed with `TARGET_NOT_READY`, HTTP 409, and the typed refusal carries the identity of the
session's coordinating replica. This section states that code for the condition of an
unreachable coordinator; the pre-running condition §15.1 states for the same code is a separate condition.
The rows whose outcome is not a forward are stated by the rows themselves: the
`POST /v1/sessions/{id}/start` row addresses a state for which the specification establishes no holder, so
the serving replica performs the start itself, the `POST /v1/sessions/{id}/finalize` row addresses
`created` for the same reason, the events streaming row is served
from the shared session-event relay, the logs streaming row is served from the Postgres `EventStore`, the
built-in adapter row addresses no pre-existing session and so has no holder to forward to, and the
store-only row requires no forwarding at all.

| Route | Session state | Required off-holder outcome | Owning section |
|:--|:--|:--|:--|
| `POST /v1/sessions/{id}/messages`, no matching `inReplyTo` | `running` | Forward to the coordinator, which evaluates the §7.2 delivery paths and produces the receipt. On an unreachable coordinator the serving replica buffers the message in the session inbox and answers `queued` | §29 |
| `POST /v1/sessions/{id}/messages` carrying `inReplyTo` | `input_required` | Forward to the coordinator, which resolves the outstanding `lenny/request_input` call. The serving replica does not resolve the call against its own pending-request state and does not answer `delivered`. On an unreachable coordinator it buffers the message and answers `queued` | §29 |
| `POST /v1/sessions/{id}/messages` carrying `delivery: immediate` | `suspended` | Forward to the coordinator, which performs the atomic resume-and-deliver when the pod is still held and the `suspended` to `resume_pending` transition when the pod has been released. On an unreachable coordinator the serving replica falls back to inbox buffering with a `queued` receipt. The serving replica writes no state transition of its own | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
| `POST /v1/sessions/{id}/interrupt` | `running` | Forward to the coordinator, which issues the interrupt to the adapter. The serving replica does not write `suspended` and does not report an interrupt acknowledgement. On an unreachable coordinator it fails closed | §29 |
| `POST /v1/sessions/{id}/terminate` | `starting`, `running`, `suspended`, `resume_pending`, and `awaiting_client_action`, the non-terminal states for which the specification establishes a coordinating replica holding `REG-COORDLEASE` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), §28.3) | Forward to the coordinator, which performs the termination sequence [§15.1](15_external-api-surface.md#151-rest-api) states for the session's current state and writes `completed`. The serving replica does not write the terminal state itself and does not report a termination outcome for a sequence it did not perform. On an unreachable coordinator it fails closed. In the remaining non-terminal states the route accepts, which are `created`, `finalizing`, and `ready`, the session's pod control stream `CH-ATTACH` is not open and the specification states no point at which the coordination lease `REG-COORDLEASE` is acquired for the session (§28.3, §28.5.1), so the specification establishes no coordinating replica for those states and no off-holder condition arises there | §29 |
| `DELETE /v1/sessions/{id}` | The same states as `POST /v1/sessions/{id}/terminate` | The same requirement as `POST /v1/sessions/{id}/terminate`, with the terminal state `cancelled` | §29 |
| `POST /v1/sessions/{id}/resume` | `awaiting_client_action` | Forward to the coordinator, which performs the restore and the delegation-tree recovery traversal. The serving replica does not run the traversal, because its view of a descendant held by another replica is not evidence that the descendant is orphaned. On an unreachable coordinator it fails closed | §29 |
| `POST /v1/sessions/{id}/start` | `ready` | No forwarding is required. In `ready` the specification establishes no holder, because the session's pod control stream `CH-ATTACH` is not open and the specification states no point at which the coordination lease `REG-COORDLEASE` is acquired for the session (§28.3, §28.5.1), so the serving replica performs the start | §29 |
| `POST /v1/sessions/{id}/finalize` | `created`, the only state the endpoint's precondition table admits ([§15.1](15_external-api-surface.md#151-rest-api)) | No forwarding is required. In `created` the specification establishes no holder, because the session's pod control stream `CH-ATTACH` is not open and the specification states no point at which the coordination lease `REG-COORDLEASE` is acquired for the session (§28.3, §28.5.1), so the serving replica resolves the pod the session is bound to from the session row's `pod_assignment` column ([§4.2](04_system-components.md#42-session-manager), [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)) and performs the workspace, setup, and credential-assignment sequence of §29.2 steps 15 through 20 against that pod itself | §29 |
| `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve`, `POST /v1/sessions/{id}/tool-use/{toolCallId}/deny`, `POST /v1/sessions/{id}/elicitations/{elicitationId}/respond`, and `POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss` | Any non-terminal state | Forward the resolution to the coordinator, which resolves the pending tool-call approval or elicitation the pod is blocked on. The serving replica does not resolve it against its own pending-request state and does not report a resolution it did not deliver. The pending state a reattaching client reads is re-synthesized from durable Postgres state ([§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract), [§10.4](10_gateway-internals.md#104-gateway-reliability)), and holding that state is not evidence that the blocked call can be resolved locally. On an unreachable coordinator it fails closed | §29 |
| `POST /v1/sessions/{id}/upload` | `running` | Forward to the coordinator, which performs the mid-session upload against the pod ([§7.4](07_session-lifecycle.md#74-upload-safety)). On an unreachable coordinator it fails closed with `TARGET_NOT_READY`, HTTP 409, whose code and status [§15.1](15_external-api-surface.md#151-rest-api) states and whose replica condition §29 states | §29 |
| `GET /v1/sessions/{id}/events`, streaming form | Any non-terminal state | Serve both the backlog and the live tail from the shared session-event relay `CH-EVENTRELAY`, which the serving replica reads directly, so no forwarding to the coordinator is required (§28.5.7, [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). What a reader receives when the relay is absent, or when the requested entry is absent from the relay stream, is a condition the specification does not state (§28.5.7, §28.8) | §29 |
| `GET /v1/sessions/{id}/events`, `Accept: application/json` form | Any non-terminal state | This form returns the paginated list envelope `{items, cursor, hasMore}` over the retained event replay buffer ([§15.1](15_external-api-surface.md#151-rest-api)), and that buffer is per-session and held in process by the session's coordinating replica ([§10.4](10_gateway-internals.md#104-gateway-reliability)), so a serving replica that is not the holder retains no entries for the session. Forward the request to the coordinator, which serves the envelope from the buffer it holds. The serving replica does not answer the request from its own buffer, because an empty or partial page returned from a buffer that never held the session's events reports an event history the session does not have. On an unreachable coordinator it fails closed | §29 |
| `GET /v1/sessions/{id}/logs`, streaming form | Any non-terminal state | Serve from the Postgres `EventStore` ([§12.2](12_storage-architecture.md#122-storage-roles)), which every replica reads, so no forwarding to the coordinator is required | §29 |
| `POST /v1/chat/completions` and `POST /v1/responses` | No pre-existing session is addressed | No forwarding is required. Each call runs an implicit single-shot session that the serving replica creates, claims a warm pod for, dispatches, and releases within the call ([§15](15_external-api-surface.md#15-external-api-surface)), so the serving replica is the coordinating replica by construction and no off-holder condition arises. A creation or claim failure fails closed as `SESSION_CREATION_FAILED` or `CREDENTIAL_POOL_EXHAUSTED`, HTTP 503 ([§15.1](15_external-api-surface.md#151-rest-api)), rendered into the adapter's native error envelope (§15); no inbox buffering and no `queued` receipt applies | §29 |
| The MCP tool surface, `lenny/send_message` carrying `delivery: immediate` | `suspended` | The same requirement as the `suspended` message-send row, which §7.2 states for both of its message sources | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
| The MCP tool surface, every other session-addressed tool call | Any non-terminal state | The requirement stated for the REST route the tool maps onto ([§15.2](15_external-api-surface.md#152-mcp-api)) | §29 |
| Every session-scoped route that reads or writes the `SessionStore`, the `ArtifactStore`, or the `EventStore` alone ([§12.2](12_storage-architecture.md#122-storage-roles)) | Any state | No forwarding is required, because the route touches neither the pod control stream `CH-ATTACH` nor the session inbox and every replica reads the same stores | §29 |

Two requirements govern the paths that write a session terminal without a client route. The orphan session
reconciler runs on a single replica under the leader election its reconcile loop is gated by, so two
replicas do not both force a session terminal for the same terminated pod
([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The session-terminal callback the gateway
invokes when a session reaches a terminal state (the `callbackUrl` webhook the gateway POSTs on that
transition, [§14](14_workspace-plan-schema.md)) is invoked once per terminal transition: a second terminal
write for a session already in that terminal state produces no second callback invocation and no further
effect. How a single invocation is retried, and what happens when its retry attempts are exhausted, is a
separate mechanism, which
[§14](14_workspace-plan-schema.md) states.

A per-RPC statement of what an off-holder replica does with a gateway-to-pod operational request is card
content under §28.5.1 rather than a key of this matrix, because keying the matrix by RPC leaves no row for
the outcomes that lose a message or corrupt durable state without reaching a pod at all.

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

### 29.5 Checkpoint capture

This trace follows one checkpoint attempt from the point at which the gateway decides to take it to the
point at which the session row records it. Every trigger converges on the one gateway-driven
`CH-CHECKPOINT` stream: the periodic schedule that enforces the checkpoint freshness requirement, the
eviction path a terminating agent pod raises, the drain path a `CH-BARRIER` message opens, and the
pre-scale-down path ([§4.4](04_system-components.md#44-event--checkpoint-store),
[§10.1](10_gateway-internals.md#101-horizontal-scaling)). The trigger selects the abort disposition, while
the upload-retry budget and whether the agent is resumed afterwards turn on whether the pod is terminating
on the preStop eviction path of §29.9; every step below holds for all of them unless it names a trigger or
that path. The steps are numbered and written in the form §29.1 fixes.

The two stores this trace writes carry different roles. The chunk bytes are written to the object store,
which is where the `ArtifactStore` role places checkpoints and workspace snapshots
([§12.2](12_storage-architecture.md#122-storage-roles)). The manifest row, the artifact catalog row, and
the session row are written to Postgres, which is where the `SessionStore` and Event / Checkpoint Store
roles sit ([§4.4](04_system-components.md#44-event--checkpoint-store),
[§12.2](12_storage-architecture.md#122-storage-roles)). A chunk object is uploadable only once its
manifest row is durable in Postgres, so no chunk object exists without a Postgres row that references it
([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The per-tenant storage counter the reservation
and the confirmations move is held in Redis and is rehydrated from the Postgres sum on restart, so it is
a derived counter rather than the record of what was stored
([§4.4](04_system-components.md#44-event--checkpoint-store),
[§11.2](11_policy-and-controls.md#112-budgets-and-quotas)).

**Preconditions.** The session is bound to a claimed pod and the gateway replica that drives the
checkpoint holds the session's coordination lease `REG-COORDLEASE`, because `CH-CHECKPOINT` admits one
holder per session and the guard is that lease together with the `coordination_generation` stamp the pod
validates on every gateway-to-pod RPC (§28.5.1 `CH-CHECKPOINT`, §28.6,
[§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). On the eviction path the agent pod
cannot open the stream itself: its preStop hook signals its coordinating replica on `CH-ADAPTEREVENTS`,
and that replica drives the stream under its held lease (§28.5.1 `CH-CHECKPOINT`, §28.5.2
`CH-ADAPTEREVENTS`, [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

1. `gateway`, `internal`. The coordinating replica selects the session to checkpoint. On the periodic
   trigger the schedule is the freshness requirement that every active session have a successful
   checkpoint within `periodicCheckpointIntervalSeconds`, default 600s, with each session's first
   periodic checkpoint offset by a jitter of up to
   `periodicCheckpointIntervalSeconds × periodicCheckpointJitterFraction`, default fraction 0.2, and
   subsequent checkpoints scheduled at the fixed interval from the previous one
   ([§4.4](04_system-components.md#44-event--checkpoint-store)).

2. `gateway`, `internal`. Two checkpoint attempts against the same session and slot serialize on the
   intent-row write of step 7: that transaction supersedes any prior active partial manifest row for the
   same session and slot, and two concurrent writers either serialise on the partial-manifest unique
   index or one of them observes no affected rows on the conditional `UPDATE` and rolls back, so the
   database never holds two active partial rows for the same session and slot at once
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Above that, the adapter's pod-level
   operation lock admits one pending checkpoint per distinct `slotId` and coalesces a checkpoint whose
   `slotId` is already pending, and the coordination lease and the generation stamp exclude a second
   replica (§28.5.1 `CH-CHECKPOINT`, §28.6,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

3. `gateway` → `adapter`, `CH-CHECKPOINT`, `gateway-to-pod`. The gateway opens the bidirectional
   `Checkpoint` stream, on which it mints a per-chunk upload capability and confirms each uploaded chunk
   while the adapter streams the workspace archive to object storage. The pod validates the
   `coordination_generation` stamp and rejects a stale one, and a replica that has just acquired
   coordination must have received a successful `CoordinatorFence` acknowledgement before it sends any
   operational RPC (§28.5.1 `CH-CHECKPOINT`, [§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§4.7](04_system-components.md#47-runtime-adapter)).

4. `gateway` → `adapter`, `CH-CHECKPOINT`, `gateway-to-pod`. The gateway sends the parameters the attempt
   runs under: the gateway-minted `checkpoint_id`, the trigger, the `chunk_size_bytes` the gateway chose,
   default 16 MiB, and the `chunk_encoding` it chose, which is `tar` or `tar.gz` and is fixed for every
   chunk of the attempt ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)). It carries them in
   the stream's `Start` message ([§11.2](11_policy-and-controls.md#112-budgets-and-quotas),
   [§13.2](13_security-model.md#132-network-isolation)). Every checkpoint path enforces
   a 60-second timeout measured from the initial quiescence request of step 8 to completion, and a
   checkpoint the gateway drives against a barrier-held pod is additionally bounded by
   `checkpointBarrierAckTimeoutSeconds`, default 90s
   ([§4.4](04_system-components.md#44-event--checkpoint-store),
   [§4.7](04_system-components.md#47-runtime-adapter),
   [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).

5. `agent pod`, `internal`. The adapter takes its pod-level operation lock, which serializes `Checkpoint`
   and `Interrupt` across the pod's slots and must be free or must promote this checkpoint from its
   queue, and resolves the workspace roots the attempt covers: `/workspace/current`, and
   `/workspace/slots/{slotId}/current/` on a pod whose pool sets `sessionPolicy.maxConcurrentSessions`
   greater than 1 (§28.5.1 `CH-CHECKPOINT`, §28.6,
   [§4.4](04_system-components.md#44-event--checkpoint-store),
   [§4.7](04_system-components.md#47-runtime-adapter),
   [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

6. `adapter` → `gateway`, `CH-CHECKPOINT`, `gateway-to-pod`. The adapter runs the pre-checkpoint
   workspace size probe, which stats the total size of the resolved roots before the quiescence handshake
   of step 8, and reports the measured size. When the measured size exceeds `workspaceSizeLimitBytes`,
   pool-level configuration with a default of 512Mi, the checkpoint is aborted immediately without
   quiescing the runtime, `lenny_checkpoint_size_exceeded_total` is incremented, a `WorkspaceSizeExceeded`
   warning is logged, and the session emits a `checkpoint.skipped` event with
   `reason: "workspace_size_limit"`, so none of the steps below occur
   ([§4.4](04_system-components.md#44-event--checkpoint-store)).

7. `gateway` → `postgres`, no register entry, the Event / Checkpoint Store
   ([§4.4](04_system-components.md#44-event--checkpoint-store),
   [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway takes a storage reservation from
   the probed size, which is the atomic increment of the tenant's Redis `storage_bytes_used` counter
   ([§11.2](11_policy-and-controls.md#112-budgets-and-quotas)), and then supersedes any prior partial
   manifest for the same session and slot by soft-deleting its confirmed chunks' catalog rows, deleting
   those chunk objects, and releasing its reservation, all of which run before the transaction below
   commits and do not themselves participate in Postgres atomicity
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). The supersede-on-write `UPDATE` that
   soft-deletes the superseded row and the `INSERT` of the intent row commit in one Postgres transaction
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The intent row carries `partial = true`,
   `manifest_reason = 'in_progress'`, `chunk_count = 0`, `workspace_bytes_uploaded = 0`, the chosen
   `chunk_size_bytes` and `chunk_encoding`, and
   `chunk_object_key_prefix = /{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/`. The gateway mints
   no chunk-upload capability before that INSERT commits, which is what keeps every chunk object
   referenced by a Postgres row (§28.5.5 `CH-OBJSTORE`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)). When Postgres is unreachable at this step no
   chunk upload begins, and the specification does not state what happens to the checkpoint attempt: the
   eviction fallback of [§4.4](04_system-components.md#44-event--checkpoint-store) is keyed on the object
   store being unreachable and itself writes to Postgres, and the crash semantics of
   [§10.1](10_gateway-internals.md#101-horizontal-scaling) cover only a crash after the intent row is
   already durable.

8. On a Full-level runtime: `adapter` → `runtime`, `CH-RUNTIMEOPS`, `intra-pod`. The adapter sends
   `checkpoint_request` and the runtime quiesces and replies `checkpoint_ready`, which is the cooperative
   handshake that produces a consistent checkpoint under every isolation profile (§28.5.3
   `CH-RUNTIMEOPS`, [§4.4](04_system-components.md#44-event--checkpoint-store),
   [§4.7](04_system-components.md#47-runtime-adapter)). A Basic-level or Standard-level runtime opens no
   `CH-RUNTIMEOPS` channel, so this step does not occur and the snapshot is taken best-effort without
   pausing the runtime and tagged `consistency: best-effort`
   ([§4.4](04_system-components.md#44-event--checkpoint-store),
   [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).

9. `agent pod`, `internal`. The adapter captures the checkpoint's contents, which are a tar of the
   resolved workspace roots and a copy of the `/sessions/` contents, and splits the stream into
   fixed-size chunks of `chunk_size_bytes` under the attempt's `chunk_encoding`
   ([§4.4](04_system-components.md#44-event--checkpoint-store),
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

10. `adapter` → `gateway`, `CH-CHECKPOINT`, `gateway-to-pod`. The adapter declares one chunk by its index
    and its exact byte length. The gateway rejects a declared length outside `(0, chunk_size_bytes]` and
    aborts the attempt with `manifest_reason = 'stream_truncated'` before it signs anything and before any
    quota arithmetic runs (§28.5.5 `CH-OBJSTORE`,
    [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

11. `gateway` → `adapter`, `CH-CHECKPOINT`, `gateway-to-pod`. The gateway signs a presigned single-part
    `PUT` capability for `chunk_object_key_prefix/chunk-{n}.{chunk_encoding}`, where `{n}` is a
    zero-padded five-digit monotonic index starting at `00000`, at that exact `Content-Length`, and
    returns it with the signed header values the adapter is to replay. The capability expires after
    `checkpointCapabilityTTLSeconds`, default 30. At most `checkpointGrantWindow` capabilities, default 4,
    are outstanding at a time, and the gateway refuses to sign a capability whose `Content-Length` would
    carry the attempt past its reservation plus the remaining tenant headroom, aborting with
    `STORAGE_QUOTA_EXCEEDED` before signing (§28.5.5 `CH-OBJSTORE`,
    [§10.1](10_gateway-internals.md#101-horizontal-scaling),
    [§13.2](13_security-model.md#132-network-isolation),
    [§11.2](11_policy-and-controls.md#112-budgets-and-quotas),
    [§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)).

12. `adapter` → `object store`, `CH-OBJSTORE`, `pod-egress`. The adapter issues one single-part `PUT` per
    chunk against the capability, replaying the signed header values verbatim on the SigV4 backends. The
    pod holds no object-store credential and no `LIST`, `DELETE`, or multipart capability. On the
    non-eviction triggers a failed upload is retried with exponential backoff from 200ms at factor 2 for
    up to about 5 seconds in total; on the preStop eviction path of §29.9 the budget is 500ms at factor 2,
    capped at 5s per attempt, for up to 30 seconds in total
    ([§4.4](04_system-components.md#44-event--checkpoint-store)). The barrier-driven drain checkpoint of
    §29.7 carries the eviction trigger on a pod that is not terminating, so it falls under neither of those
    two budgets, and the specification does not state an upload-retry budget for it. A retry that outlives its grant's expiry requests a fresh
    grant for the same chunk index on the open stream, which the gateway re-signs at the same key and
    length (§28.5.5 `CH-OBJSTORE`, [§4.4](04_system-components.md#44-event--checkpoint-store),
    [§13.2](13_security-model.md#132-network-isolation)).

13. `adapter` → `gateway`, `CH-CHECKPOINT`, `gateway-to-pod`. The adapter acknowledges the committed
    chunk on the stream, which is what lets the gateway mint the next capability within the grant window
    (§28.5.1 `CH-CHECKPOINT`, [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

14. `gateway` → `object store`, no register entry, the Artifact Store
    ([§4.5](04_system-components.md#45-artifact-store),
    [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway confirms the acknowledged chunk
    with a `StatObject` and reads back its confirmed size. A chunk whose confirmed size exceeds the size
    signed into its grant aborts the attempt ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
    [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). The §28.3 channel register places
    `CH-OBJSTORE` on the pod-egress boundary with the pod adapter as its dialling participant, so no
    register entry covers this gateway-originated request and the step names the store instead, per §29.1.

15. `gateway` → `postgres`, no register entry, the Event / Checkpoint Store
    ([§4.4](04_system-components.md#44-event--checkpoint-store),
    [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway inserts the chunk's
    `artifact_store` catalog row, which is what carries the chunk's bytes into the tenant's storage-quota
    accounting, and advances the manifest row's `chunk_count` and `workspace_bytes_uploaded` under a
    guard that makes the counters monotonic, so an out-of-order acknowledgement cannot decrement them.
    Neither the confirmation of step 14 nor this update gates the next grant, and both must complete
    before finalisation and before quota reconciliation
    ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
    [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). Steps 10 through 15 repeat per chunk, with
    at most `checkpointGrantWindow` grants outstanding at a time
    ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

16. `adapter` → `gateway`, `CH-CHECKPOINT`, `gateway-to-pod`. The adapter declares the archive complete
    with the stream's terminal summary. When all upload retries have instead failed on a non-eviction
    checkpoint, the adapter reports the retry-exhausted failure on this stream in place of the summary,
    and the gateway increments `lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}`
    (§28.5.1 `CH-CHECKPOINT`, [§4.4](04_system-components.md#44-event--checkpoint-store),
    [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

17. `gateway` → `postgres`, no register entry, the Event / Checkpoint Store
    ([§4.4](04_system-components.md#44-event--checkpoint-store),
    [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway finalises the manifest row with a
    final update that flushes the last observed `chunk_count` and `workspace_bytes_uploaded` and releases
    the part of the reservation the attempt did not keep. A terminal summary whose every declared byte is
    confirmed finalises `partial = false` and `manifest_reason = 'complete'`, which is the point at which
    the attempt becomes a valid checkpoint; every other terminal arm finalises `partial = true` with the
    reason that names it, which is `timeout` for a deadline fire, `stream_truncated` for a truncated
    stream or an adapter crash, `superseded`, or `quota_exceeded`
    ([§4.4](04_system-components.md#44-event--checkpoint-store),
    [§10.1](10_gateway-internals.md#101-horizontal-scaling),
    [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)).

18. On a Full-level runtime: `adapter` → `runtime`, `CH-RUNTIMEOPS`, `intra-pod`. The adapter resumes the
    runtime with `checkpoint_complete`. On a non-eviction checkpoint whose upload retries were exhausted
    the adapter resumes the runtime immediately by the same message. When the runtime has sent
    `checkpoint_ready` and no `checkpoint_complete` arrives within 60 seconds, it autonomously resumes
    normal operation and logs a `checkpoint_timeout` warning (§28.5.3 `CH-RUNTIMEOPS`,
    [§4.4](04_system-components.md#44-event--checkpoint-store)). A Basic-level or Standard-level runtime
    opens no `CH-RUNTIMEOPS` channel, so this step does not occur and there is no runtime to resume,
    because step 8 did not pause it
    ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3). On the preStop
    eviction path of §29.9 the pod is terminating and there is no agent to resume
    ([§4.4](04_system-components.md#44-event--checkpoint-store)).

19. On a checkpoint that finalised `partial = false`: `gateway` → `postgres`, no register entry, the
    Postgres `SessionStore` role ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway
    updates `last_successful_checkpoint_at` on the session record, which it
    tracks for every successful checkpoint regardless of trigger and against which the freshness
    requirement of step 1 is evaluated ([§4.4](04_system-components.md#44-event--checkpoint-store)).

### 29.6 Restore and resume

This trace follows one client-driven resume, from the `POST /v1/sessions/{id}/resume` call that requests
it to the point at which the session is `running` again on a replacement pod with its workspace restored
from a checkpoint. It traces that entry point alone. A session also reaches the same restore without a
client call: the gateway's own recovery after a retryable pod failure, of which an eviction is the case
that carries its own teardown checkpoint, transitions the session to `resume_pending` and rebuilds it onto
a replacement pod under the session's retry policy
([§7.3](07_session-lifecycle.md#73-retry-and-resume),
[§7.2](07_session-lifecycle.md#72-interactive-session-model),
[§4.4](04_system-components.md#44-event--checkpoint-store)), and a `delivery: "immediate"` message
addressed to a `suspended` session whose pod was already released drives the same transition
([§7.2](07_session-lifecycle.md#72-interactive-session-model)). Those entry points are named here and are
not traced. The steps are numbered and written in the form §29.1 fixes.

**Preconditions.** The session is in `awaiting_client_action`, the only state
`POST /v1/sessions/{id}/resume` admits, which a session reaches through auto-retry exhaustion or through
`maxResumeWindowSeconds` elapsing in `resume_pending` while no pod was available
([§15.1](15_external-api-surface.md#151-rest-api),
[§7.3](07_session-lifecycle.md#73-retry-and-resume)). The gateway replica that drives the restore holds
the session's coordination lease `REG-COORDLEASE`, and the pod validates the `coordination_generation`
stamp on every gateway-to-pod RPC and rejects a stale one
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).

1. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
   `POST /v1/sessions/{id}/resume`, which is the explicit resume available once automatic retries are
   exhausted ([§15.1](15_external-api-surface.md#151-rest-api),
   [§7.3](07_session-lifecycle.md#73-retry-and-resume)). The §28.3 registers carry no entry for this
   surface, so the step names the surface in place of a channel identifier and a boundary value, per
   §29.1.

2. `gateway`, `internal`. The gateway authenticates the caller and authorizes the operation against the
   RBAC permission matrix ([§10.2](10_gateway-internals.md#102-authentication)), then applies the
   endpoint's precondition table, which admits `awaiting_client_action` alone. The call is not valid in
   `suspended`, for which message delivery or `resume_session` is the path, and a call against a terminal
   row is rejected with `409 INVALID_STATE_TRANSITION`
   ([§15.1](15_external-api-surface.md#151-rest-api)).

3. `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
   ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway writes the
   `awaiting_client_action → resume_pending` transition on the session row
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)). A session in `resume_pending` moves on to the
   internal `resuming` state when a pod is allocated within `maxResumeWindowSeconds`, and returns to
   `awaiting_client_action` when that window elapses with no pod available; the API reports the whole
   sequence as `resume_pending → running`
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)).

4. `gateway` → `control plane`, `REG-CLAIM`, Kubernetes API. The gateway allocates a replacement warm pod,
   which it acquires by creating a `SandboxClaim` with the deterministic name `claim-<podName>` carrying
   `sandboxRef` and `tenantId` in its spec, and may wait when the pool is temporarily exhausted
   ([§7.3](07_session-lifecycle.md#73-retry-and-resume),
   [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.3). A pool or credential
   exhaustion, a Token Service outage, or a transient setup-time transport failure on this path is
   returned to the caller as `RESUME_FAILED` with `Retry-After` set, and the session row returns to
   `awaiting_client_action` so an explicit retry of the same call succeeds once the condition clears
   ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
   [§15.1](15_external-api-surface.md#151-rest-api)).

5. `gateway` → `postgres`, no register entry, the Event / Checkpoint Store
   ([§4.4](04_system-components.md#44-event--checkpoint-store),
   [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway selects the checkpoint to restore:
   the active manifest row for the session and slot under `deleted_at IS NULL` at the highest
   `coordination_generation`, and among the rows at that generation the most recently created one, with
   the session's `WorkspaceSnapshot.Ref` as a validation input rather than the key of the selection. A row
   that is `partial = false` is restored whole. For a row that is `partial = true` the threshold is
   `baseline_full_checkpoint_bytes` multiplied by `gateway.partialRecoveryThresholdFraction`, default 0.5,
   when that baseline is not null and zero when it is null, and the row is restored only when
   `workspace_bytes_uploaded` is at least that threshold and `workspace_bytes_uploaded` and
   `chunk_count` are both greater than zero; below that threshold the session falls back to the last
   successful full checkpoint ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)).

6. `gateway` → `object store`, no register entry, the Artifact Store
   ([§4.5](04_system-components.md#45-artifact-store),
   [§12.2](12_storage-architecture.md#122-storage-roles)). The gateway lists the objects under the
   manifest's `chunk_object_key_prefix` and verifies that every index in the contiguous prefix
   `[0, chunk_count)` is present. An index at or beyond `chunk_count` is expected residue and is ignored,
   while a missing intermediate index or an out-of-order index below `chunk_count` fails reassembly before
   any chunk body is fetched and the session falls back to the last successful full checkpoint
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The §28.3 channel register places
   `CH-OBJSTORE` on the pod-egress boundary with the pod adapter as its dialling participant, so no
   register entry covers this gateway-originated request and the step names the store instead, per §29.1.

7. `gateway`, `internal`. The gateway mints one presigned single-key `GET` capability per index in
   `[0, chunk_count)`, each naming one method and one object key, at
   `chunk_object_key_prefix/chunk-{n}.{chunk_encoding}` with `{n}` the zero-padded five-digit index. Each
   capability expires after `checkpointCapabilityTTLSeconds`, default 30, and the `ArtifactStore` mints no
   capability for a key outside the caller's authenticated tenant prefix
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§12.5](12_storage-architecture.md#125-artifact-store),
   [§13.2](13_security-model.md#132-network-isolation),
   [§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)).

8. `gateway` → `adapter`, no register entry, the internal control API
   ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway calls the
   unary `Resume` RPC, which restores the session from its checkpoint on the replacement pod, and passes
   the restore capabilities on that call ([§4.7](04_system-components.md#47-runtime-adapter),
   [§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.5 `CH-OBJSTORE`). The §28.3 channel
   register places the channels `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, and `CH-PODHEALTH`
   on the gateway-to-pod boundary (§28.5.1) and carries no entry for this RPC, so the step names the
   internal control API in place of a channel identifier and a boundary value, per §29.1.

9. `adapter` → `object store`, `CH-OBJSTORE`, `pod-egress`. The adapter fetches one chunk per index in
   ascending order against the capabilities the `Resume` call carried, holding no object-store credential
   and no `LIST`, `DELETE`, or multipart capability, and concatenates the bodies into a single byte stream
   fed end to end into one decompress-and-untar pipeline whose decoder is selected from the manifest's
   `chunk_encoding` column. The pipeline writes into the staging directory `/workspace/current.partial`,
   which is renamed onto `/workspace/current` only once end truncation is the sole observed error; a fetch
   error on a non-final chunk, a decode error away from the end of the stream, or a mid-stream tar header
   parse error aborts reassembly, deletes the staging directory whole, and falls back to the last
   successful full checkpoint (§28.5.5 `CH-OBJSTORE`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§13.2](13_security-model.md#132-network-isolation)).

10. `agent pod`, `internal`. The restored runtime resumes its conversation as of the checkpoint, which
    bundles the native-SDK session file, so the replay window spans from that checkpoint to the moment of
    the failure that ended the previous pod. The platform guarantees at-least-once semantics for external
    side effects across the restore: an effect performed within the replay window may be issued again, the
    platform provides no automatic deduplication on this path, and the §11.5 idempotency mechanism does
    not close it ([§7.3](07_session-lifecycle.md#73-retry-and-resume),
    [§4.4](04_system-components.md#44-event--checkpoint-store),
    [§11.5](11_policy-and-controls.md#115-idempotency)). Under
    `messaging.durableInbox: true` the checkpoint state carries the adapter's `delivered_message_ids` set,
    bounded to the last 1000 message identifiers, which the adapter checks on a re-delivery and against
    which it suppresses a duplicate, incrementing `lenny_inbox_duplicate_suppressed_total`
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model)).

11. `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
    ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway writes the session to `running`.
    The recovery mints a new `recovery_generation` of the same logical session and the client continues to
    see one session identifier, and the row's `last_seq` counter advances without rewinds or duplicates
    across the recovery ([§7.3](07_session-lifecycle.md#73-retry-and-resume),
    [§7.2](07_session-lifecycle.md#72-interactive-session-model)).

12. `gateway` → `client`, no register entry, the client-to-gateway session REST surface. The gateway emits
    `session.resumed` on the session's event stream, carrying `resumeMode` and `workspaceLost`. The mode is
    `full` when the workspace was restored whole from the checkpoint, and `partial_workspace` when it was
    reconstructed from a partial manifest, in which case the event also carries
    `workspaceRecoveryFraction`, computed as the post-extraction on-disk total over the manifest's
    `baseline_full_checkpoint_bytes` and omitted when that baseline is null
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
    [§10.1](10_gateway-internals.md#101-horizontal-scaling)). When the resuming session has one or more
    active children in the delegation tree, the gateway emits `children_reattached` as a single event
    immediately after `session.resumed`, once per parent resume
    ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
    [§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)).

### 29.7 Gateway drain

This trace follows one gateway replica's graceful drain, from the preStop hook that starts the staged
drain sequence to the SIGKILL deadline that ends it. It traces the path on which every pod the replica
coordinates acknowledges its barrier within the deadline. A pod that does not acknowledge within
`checkpointBarrierAckTimeoutSeconds` is carried by the BarrierAck-timeout partial-capture path, which
finalises the session's active partial-manifest intent row as `manifest_reason = "timeout"` when that row
carries committed chunks and otherwise falls back to the session's last successful periodic checkpoint
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1 `CH-BARRIER`). A barrier addressed to a
session this replica no longer coordinates is rejected by the pod as a generation-stale RPC under the
fencing rules ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Those outcomes are named here and
are not traced, and the acquisition of the drained replica's sessions by a peer replica is the coordinator
handoff protocol ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) rather than part of this trace.
The agent pod's own termination is a separate path, on which the pod signals its coordinating replica and
that replica drives the eviction checkpoint
([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.5.2 `CH-ADAPTEREVENTS`). The
steps are numbered and written in the form §29.1 fixes.

**Preconditions.** The replica coordinates one or more sessions, holding each session's coordination lease
`REG-COORDLEASE` and appearing as the `coordinator_replica` of that session's `REG-COORDMIRROR` row
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). The gateway pod's
`terminationGracePeriodSeconds` satisfies
`max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30`, which with the defaults is 210
seconds, and the Helm chart sets the gateway pod's `terminationGracePeriodSeconds` to 240 by default to
leave a 30-second margin, with the per-tier value fixed in §17.8
([§10.1](10_gateway-internals.md#101-horizontal-scaling),
[§17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)).

1. `gateway`, `internal`. The replica's preStop hook runs the staged graceful drain sequence within
   `terminationGracePeriodSeconds` ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

2. `gateway`, `internal`. The hook sets the pod's readiness probe to `false` before any drain logic
   begins, which removes the pod from the Service's endpoints list and stops the load balancer routing new
   requests to it. No new sessions or streams are accepted after this point
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

3. `gateway` → `postgres`, `REG-COORDMIRROR`, Postgres. The replica reads its barrier-target set as the
   `coordination_lease` rows whose `coordinator_replica` is this replica under `released_at IS NULL`,
   bounded by a 2-second deadline. On a failed read or on deadline expiry the replica falls back to its
   in-memory lease cache so the barrier still fires, and emits
   `lenny_prestop_barrier_target_source_total` with `source="cache_fallback"` in place of the healthy
   path's `source="postgres"` ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).

4. `gateway` → `adapter`, `CH-BARRIER`, `gateway-to-pod`. At the readiness flip the replica sends
   `CheckpointBarrier`, carrying the session's current `coordination_generation` and a `barrier_id` that
   is monotonically increasing per session, to every pod in the barrier-target set simultaneously, and
   waits for the acknowledgements of all of them under a single wall-clock deadline of
   `checkpointBarrierAckTimeoutSeconds`, default 90s, rather than per pod (§28.5.1 `CH-BARRIER`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).

5. `agent pod`, `internal`. The adapter finishes the tool call it is executing, stops accepting new
   tool-call dispatches, records the `barrier_id` in the session's checkpoint metadata, and holds the
   quiesced state open rather than driving its own checkpoint (§28.5.1 `CH-BARRIER`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

6. `gateway` → `adapter`, `CH-CHECKPOINT`, `gateway-to-pod`. The replica's barrier dispatcher opens the
   `Checkpoint` stream for each quiesced session concurrently with the in-flight `CheckpointBarrier` RPC
   to that session, drives the upload against the quiesced pod inside the
   `checkpointBarrierAckTimeoutSeconds` deadline, and finalises the manifest row. The capture the stream
   carries is traced in §29.5, and the drain driver stamps the eviction trigger on the finalisation's
   trigger label. The pod is not terminating on this path, so the trigger-conditioned statements §29.5
   makes about a terminating pod do not hold here and the adapter releases quiescence per step 7
   (§28.5.1 `CH-CHECKPOINT`, [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

7. `adapter` → `gateway`, `CH-ADAPTEREVENTS`, `pod-to-gateway`. The adapter sends
   `CheckpointBarrierAck`, carrying the `barrier_id`, the `checkpoint_ref` echoing the gateway-minted
   `checkpoint_id` it received in the stream's `Start` message, and `quiesced_ms` as the
   time-to-quiescence measured inside the ack window, only after the gateway-driven stream has terminated,
   and then releases quiescence (§28.5.2 `CH-ADAPTEREVENTS`,
   [§4.7](04_system-components.md#47-runtime-adapter),
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

8. `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
   ([§12.2](12_storage-architecture.md#122-storage-roles)). For each session that still has a checkpoint
   in progress, the hook reads `last_checkpoint_workspace_bytes` from the session record to select the
   cap tier the wait of step 9 runs under, which is 30s at or below 100 MB, 60s from 101 MB to 300 MB, and
   90s from 301 MB to the 512 MB hard limit. A `NULL` value selects the 90s maximum tier. A failed read
   falls back to the in-replica cache that mirrors the field for the sessions this replica coordinates
   without blocking on Postgres, and a cache miss selects the 90s maximum tier. The selection emits
   `lenny_prestop_cap_selection_total` once, labelled `source` with the value that names how it was
   obtained, which is `postgres`, `postgres_null`, `cache_hit`, or `cache_miss_max_tier`
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§16.1](16_observability.md#161-metrics)).

9. `gateway`, `internal`. The hook waits for the checkpoints in progress for the sessions this replica
   coordinates to complete before proceeding, so that SIGKILL does not interrupt a checkpoint upload and
   leave checkpoint state inconsistent. The wait runs under the tier selected in step 8, clamped to
   `terminationGracePeriodSeconds` minus 30 seconds so that at least 30 seconds remains for the stream
   drain ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

10. `gateway`, `internal`. The hook polls `active_streams > 0` at 1-second intervals for the remainder of
    `terminationGracePeriodSeconds`, which gives in-flight streams time to complete naturally and gives
    clients time to detect the closing connection, through a gRPC `GOAWAY` or an SSE stream close, and
    reconnect to another replica through the load balancer
    ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

11. `gateway`, `internal`. When active streams have not drained by the grace-period deadline the process
    receives SIGKILL and the remaining clients must reconnect. Together with the one-pod-at-a-time
    scale-down policy, this bounds the fleet to at most one replica draining at any moment
    ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

### 29.8 Coordinator handoff and crash takeover

This trace follows one session's coordination moving from the replica that held it to a peer replica after
that holder crashes or is partitioned away, from that holder's crash or partition to the point at which the
acquiring replica may send operational RPCs to the pod. It traces the crash-takeover entry point. The
orderly entry point, on which a peer replica acquires the sessions of a replica that has completed its
graceful drain through the same lease acquisition, reaches the same handoff protocol and is named rather
than traced here ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §29.7). Two further branches are named and not
traced: coordination rights acquired through the Postgres fallback
`SELECT ... FOR UPDATE SKIP LOCKED` on the session row when Redis is unavailable, and the path on which no
replica fences within `coordinatorHoldTimeoutSeconds`, default 120s, on which the adapter begins graceful
session termination with reason `coordinator_lost` and sends `AdapterTerminating` to the gateway
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.2 `CH-ADAPTEREVENTS`). The steps are
numbered and written in the form §29.1 fixes.

**Preconditions.** `replica A` coordinates the session, holding the session's coordination lease
`REG-COORDLEASE`, which admits one holder per tenant and session on a compare-and-set with a 60-second
expiry, and the session's `coordination_generation` is the generation the pod last fenced
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). `replica B` is a peer replica that holds
no binding for the session. Every gateway-to-pod RPC carries the coordinator's generation stamp, and the
pod rejects a stale one ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

1. `replica A`, `internal`. The replica crashes or becomes network-partitioned and stops extending the
   session's coordination lease ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

2. `adapter`, `internal`. The pod's gRPC transport detects the broken gateway-to-pod connection within 15
   seconds, one keepalive interval of 10 seconds plus one keepalive timeout of 5 seconds. With no active
   coordinator the adapter enters hold state: it pauses runtime activity, leaves the runtime process
   running with no new instructions, rejects every inbound RPC other than `CoordinatorFence` with
   `UNAVAILABLE` and a `coordinator_hold` error detail, emits the `lenny_adapter_coordinator_hold` gauge at
   1, and logs a `coordinator_connection_lost` event carrying the last known generation
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§16.1](16_observability.md#161-metrics)).

3. `replica B` → `redis`, `REG-COORDLEASE`, Redis. Once the prior holder's 60-second expiry lapses, the
   replica acquires the session's coordination lease on the compare-and-set that admits one holder per
   tenant and session ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).

4. `replica B` → `postgres`, no register entry, the Postgres `SessionStore` role
   ([§12.2](12_storage-architecture.md#122-storage-roles)). The replica reads `tenant_id`,
   `coordination_generation`, and `last_checkpoint_workspace_bytes` from the session row under row-level
   security, on the shard the routing prefix embedded in the session id names, and primes its in-replica
   `last_checkpoint_workspace_bytes` cache with a non-null value. When the read returns no row the replica
   relinquishes the lease and emits a `session_not_found_on_handoff` structured event, which is a
   non-retryable failure. A failure of the cache priming write alone does not abort the acquisition
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§12.6](12_storage-architecture.md#126-interface-design)).

5. `replica B` → `postgres`, no register entry, the Postgres `SessionStore` role
   ([§12.2](12_storage-architecture.md#122-storage-roles)). The replica increments
   `coordination_generation` on the session row with a compare-and-swap predicated on the session id, the
   tenant id, and the expected generation read from that row, and the returned value becomes its local
   generation stamp. When the update matches no row the replica re-reads `coordination_generation`,
   discards its lease claim, and restarts from lease acquisition; when the re-read returns a `tenant_id`
   differing from the value used in the failed compare-and-swap the replica logs a
   `coordinator_handoff_tenant_mismatch` critical structured event, aborts without retry, and relinquishes
   the lease ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

6. `replica B` → `adapter`, `CH-FENCE`, `gateway-to-pod`. The replica sends
   `CoordinatorFence(session_id, new_generation)` carrying its local generation stamp, under a 5-second
   deadline (§28.5.1 `CH-FENCE`, [§10.1](10_gateway-internals.md#101-horizontal-scaling),
   [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).

7. `adapter`, `internal`. The adapter records the announced generation, from that point rejects any RPC
   carrying an older one, and acknowledges the fence, which is the only exit from hold state. When the
   announced generation exceeds `last_fenced_generation` by more than one, the adapter first cancels and
   discards every in-flight RPC received after `last_fenced_generation`, resets the transient tool-call and
   lifecycle state accumulated since that generation, and logs a `coordinator_generation_gap` event
   recording both generations, then acknowledges normally (§28.5.1 `CH-FENCE`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

8. When the fence fails or its deadline expires: `replica B` → `adapter`, `CH-FENCE`, `gateway-to-pod`. The
   replica retries the fence with the same generation value, up to 3 attempts with 1-second backoff. On
   exhaustion it relinquishes the lease, stopping its extension of the Redis expiry, and backs off with a
   jittered delay from an initial 2 seconds to a 16-second maximum before reconsidering coordination. The generation increment stays in Postgres and the next replica to
   acquire the lease increments it again to a value that supersedes both (§28.5.1 `CH-FENCE`,
   [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

9. When the fence has returned a successful acknowledgement: `replica B`, `internal`. The replica begins
   coordinating the session and stamps its local generation on every gateway-to-pod RPC it sends for the
   session. The acknowledgement is the hard precondition for this step, so no operational RPC reaches the
   pod before the fence closes the window in which the prior coordinator's RPCs are still accepted
   ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1 `CH-FENCE`, §28.6).

10. When the prior coordinator resumes and receives a generation-stale rejection for the session, from the
    pod or from a failed compare-and-swap on the session row: `replica A`, `internal`. It cancels every
    in-flight RPC for the session without retrying, discards its cached in-memory streams, pending tool calls, and
    buffered events for the session, and, while it still holds an unexpired lease it believes entitles it
    to re-acquire coordination, backs off with a jittered exponential delay from an initial 500 milliseconds
    to an 8-second maximum before re-checking the generation in Postgres, releasing the lease and ceasing to
    contend once it observes a generation above its own
    ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.6).

11. `unstated`. The specification does not state at what point of a takeover the `REG-COORDMIRROR` row is
    updated to name the acquiring replica as the session's `coordinator_replica`. The register names the
    gateway sweeper as that row's writer set and the row as a projection rather than an exclusion primitive
    (§28.3), and a draining replica reads the row as its barrier-target set (§29.7), so a reader cannot
    determine from the specification whether a barrier fan-out concurrent with this takeover observes the
    prior holder or the acquiring one.

### 29.9 Agent pod eviction

This trace follows the eviction of one agent pod that holds a live session, from the eviction request the
Kubernetes eviction API admits to the point at which the session is resumable on a replacement pod. It
traces the voluntary-disruption entry point, on which the eviction traverses the `pods/eviction`
subresource and the pod's preStop hook runs before the pod is allowed to terminate
([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.5.6 `CH-ADMISSION`). Other
entry points and outcomes are named here and are not traced. A spontaneous node failure, an OOM kill, and
a preemption bypass the eviction API and open no `CH-ADMISSION` channel (§28.5.6). The eviction of a warm
pod carries no session, and the per-`SandboxTemplate` pod disruption budget selects idle pods alone, so an
active session pod has no voluntary-disruption protection beyond its preStop hook
([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). The drain of a gateway replica
is §29.7 rather than part of this trace. When the coordinating replica is unreachable no replica drives
the eviction checkpoint until the session's coordination lease lapses and a new holder has fenced the pod
through the TTL-driven coordinator handoff, which is §29.8
([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.5.1 `CH-CHECKPOINT`). When
the object store is unreachable and its retries are exhausted the gateway falls back to a minimal session
state record in Postgres, and when that write is also exhausted it enters the total-loss path and emits
`session.lost` with reason `eviction_total_loss`
([§4.4](04_system-components.md#44-event--checkpoint-store)). When the pod's signal reaches no replica at
all the orphan session reconciler forces the session to `failed` with reason `orphan_pod_terminated`
within one 60-second reconcile interval
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.2 `CH-ADAPTEREVENTS`). The steps are
numbered and written in the form §29.1 fixes.

**Preconditions.** The pod is claimed and carries a live session, and the replica holding that session's
coordination lease `REG-COORDLEASE` is its coordinating replica
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). Every agent pod carries a preStop hook
that triggers a checkpoint through the runtime adapter before termination, and the pod's
`terminationGracePeriodSeconds`, default 120s, is set high enough to give that checkpoint time to complete
and be persisted to object storage
([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

1. When the chart's `features.drainReadiness` flag is `true` and the eviction traverses the Kubernetes
   eviction API: `control plane` → `gateway`, `CH-ADMISSION`, `control-plane`. The `lenny-drain-readiness`
   validating admission webhook fires on the `CREATE` against the `pods/eviction` resource in the agent
   namespaces and issues `GET /internal/drain-readiness` against the gateway's internal HTTP port. The
   gateway answers `HTTP 200` when the drain may proceed and `HTTP 503` when the artifact store is
   degraded, and the webhook rejects the eviction on a `503` and on an unreachable endpoint. An operator
   bypasses the check for an emergency drain by annotating the node with `lenny.dev/drain-force: "true"`
   with justification, in which case the webhook permits the eviction and emits the `node.drain.forced`
   critical audit event (§28.5.6 `CH-ADMISSION`,
   [§12.5](12_storage-architecture.md#125-artifact-store)). The flag defaults to `false`, in which case the
   webhook is not rendered and the eviction is admitted with no such check (§28.5.6 `CH-ADMISSION`).

2. `agent pod`, `internal`. The pod's preStop hook runs before the pod is allowed to terminate, bounded by
   `terminationGracePeriodSeconds`
   ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

3. `agent pod` → `gateway`, `CH-ADAPTEREVENTS`, `pod-to-gateway`. The hook cannot open the gateway-driven
   `Checkpoint` stream itself, so it signals the session's coordinating replica, which is the holder of
   `REG-COORDLEASE`, to have that replica drive the eviction checkpoint. The specification states no
   message name for this signal (§28.5.2 `CH-ADAPTEREVENTS`,
   [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.3).

4a. `gateway` → `adapter`, `CH-CHECKPOINT`, `gateway-to-pod`. The coordinating replica drives the eviction
    checkpoint on the `Checkpoint` stream under its held lease, with the `TriggerEviction` trigger. The
    trigger selects the eviction retry budget of exponential backoff from 500ms at factor 2, capped at 5
    seconds per attempt and 30 seconds in total, and the checkpoint path is bounded by the 60-second
    checkpoint timeout every checkpoint path enforces from the initial quiescence request to completion.
    The capture the stream carries is traced in §29.5
    (§28.5.1 `CH-CHECKPOINT`, [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle),
    [§4.4](04_system-components.md#44-event--checkpoint-store)).

4b. `unstated`. The specification names `eviction` among the reasons the adapter's `terminate` frame
    carries to the runtime on `CH-RUNTIMEOPS`, and states that the adapter sends SIGTERM when the runtime
    has not exited by that frame's `deadlineMs` ([§4.7](04_system-components.md#47-runtime-adapter),
    §28.5.3 `CH-RUNTIMEOPS`). It does not state at what point of this path the adapter sends that frame,
    and it does not fix the relative order of steps 4a and 4b.

5. `agent pod`, `internal`. The pod terminates
   ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

6. On a session whose pod was checkpointed: `gateway`, `internal`. The session retry mechanism transitions
   the session to `resume_pending` and rebuilds it onto a replacement pod under the session's retry policy,
   which §29.6 names as an entry point it does not trace, reaching the same restore §29.6 traces from the
   client-driven `POST /v1/sessions/{id}/resume` call
   ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle),
   [§7.2](07_session-lifecycle.md#72-interactive-session-model)).

### 29.10 The concurrent-session pod

This subsection departs from the traced form the rest of §29 carries, and states no numbered steps. A pod
serving more than one concurrent session is a condition under which the traces above run rather than an
operation carried from end to end: each of §29.2 through §29.9 holds on such a pod, with part of the
pod's state, part of its channels, and part of its resources partitioned per slot and the rest shared by
the whole pod. What a reader of those traces needs is which part is which, so this subsection states the
partition and what follows from each half. The step notation §29.1 fixes governs traces and does not
apply here; each statement below cites the section that states it. Where the specification does not state
whether something is partitioned per slot, this subsection says so rather than inferring it from the
things around it that are partitioned.

**The condition.** The pod's pool sets `sessionPolicy.maxConcurrentSessions` above 1, which allows
multiple simultaneous sessions of the same tenant on one pod and requires the deployer to set
`sessionPolicy.acknowledgeProcessLevelIsolation: true`. The pool controller rejects a pool definition
that sets the first without the second
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
[§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). Each simultaneous session occupies one
slot, identified by a `slotId` the adapter assigns, and every mechanism below is keyed by that identifier
or is not. On a pod whose pool sets `maxConcurrentSessions` to 1 no message carries
`slotId`, so nothing in this subsection applies to it
([§15.4](15_external-api-surface.md#154-runtime-adapter-specification), §28.6). SDK-warm mode is not
available under this condition: a pool that combines `capabilities.preConnect: true` with
`maxConcurrentSessions` above 1 is rejected at validation time, because each slot requires independent
workspace materialization and independent agent initialization
([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).

**Partitioned per slot.** The following are stated per slot, and a reader may treat two slots on one pod
as independent in each of them.

- The workspace subtree. The adapter creates and removes a per-slot directory tree at
  `/workspace/slots/{slotId}/`, `/sessions/{slotId}/`, and `/artifacts/{slotId}/`, the runtime derives
  each slot's `cwd` from its `slotId` and may not assume the single-slot `/workspace/current` layout, and
  the gateway addresses workspace finalization and checkpoint export by the slot-qualified path
  ([§6.4](06_warm-pod-model.md#64-pod-filesystem-layout)). The plan behind those trees is not
  partitioned: the `WorkspacePlan` serves as a shared template whose sources, setup commands, and options
  are materialized independently for every slot, and per-slot workspace differentiation is out of scope,
  so all slots on one pod are assigned sessions that share one plan
  ([§14](14_workspace-plan-schema.md)).
- The credential lease. Each active slot holds an independent lease obtained by its own
  `AssignCredentials` call at slot assignment, written to a per-slot credential file at
  `/run/lenny/slots/{slotId}/credentials.json`, revoked independently when the slot completes or fails,
  and rotated independently, so the in-flight gate and the `credentials_rotated` acknowledgement apply to
  the slot being rotated and a sibling slot's model calls are unaffected. Each lease counts separately
  against the credential pool's concurrency limit
  ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like),
  [§4.9](04_system-components.md#49-credential-leasing-service)). What a slot may read is not partitioned
  with it, which the shared list below states.
- The slot's lifecycle state. Beneath the pod-level coarse phase, per-slot sub-states track each slot
  through workspace materialization, execution, and cleanup, and the gateway applies the per-slot retry
  policy independently for each failed slot ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).
- The session inbox and the delivery-path evaluation. Each active slot maintains its own independent inbox
  on the coordinating gateway replica, the `slotId` on the `MessageEnvelope` selects which slot's inbox
  receives a message, and the §7.2 delivery paths are evaluated per slot, with `ready_for_input`,
  `input_required`, and `await_children` tracked for each slot rather than for the pod. A message that does
  not resolve to an active slot fails closed internally and is never routed, and the `delivery: immediate`
  interrupt targets the specific slot's tool-call context rather than the whole pod
  ([§7.2](07_session-lifecycle.md#72-interactive-session-model)).
- The addressing key on the agent message plane. Every `message`, `tool_result`, `response`, `tool_call`,
  and `set_tracing_context` on `CH-MSGSOCK` carries the slot's `slotId`, and a runtime serving such a
  pool implements a dispatch loop keyed on it (§28.5.3). The key is per slot and the channel it rides is
  not.
- Admission of a checkpoint to the adapter's operation lock. The lock admits one pending checkpoint per
  distinct `slotId`, coalesces a checkpoint whose `slotId` is already pending, and promotes the pending
  checkpoints in slot-ID order ([§4.7](04_system-components.md#47-runtime-adapter), §28.6). The lock
  itself is not partitioned, which the shared list below states.
- The coordination lease and the fence that guards it. `REG-COORDLEASE` admits one holder per tenant and
  session (§28.3), and the exclusivity constraint on `CH-CHECKPOINT`, `CH-ATTACH`, `CH-FENCE`, and
  `CH-BARRIER` is one coordinating replica per session, guarded by that lease together with the
  generation stamp (§28.5.1, §28.6). Both units are the session, so each slot's session carries its own
  lease and its own generation. Whether the sessions occupying two slots on one pod may be coordinated by
  two different replicas is not stated, which the list of what the specification does not state records
  below.

**Shared by the whole pod.** The following carry the pod as their unit, so two slots on one pod are not
independent in them.

- The transport to the gateway. `LNK-POD-GRPC` carries one connection per gateway replica per pod and
  `LNK-GWCONTROL` one connection per pod process to one replica (§28.3), so the connections beneath
  `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, `CH-PODHEALTH`, and `CH-ADAPTEREVENTS` are
  established per replica and pod, with no per-slot connection stated.
- The agent message plane itself. `CH-MSGSOCK` is one channel over which a pod serving more than one
  concurrent session multiplexes every slot's stream, keyed by `slotId` (§28.5.3, §28.6), which §28.5.3
  states as multiple independent concurrent session streams through a single stdin channel. It is a
  scoping constraint rather than an exclusivity constraint, and the specification states no exclusivity
  constraint on this channel and names no guard that enforces one (§28.6).
- The adapter's operation lock. It is pod-level and serializes `Checkpoint` and `Interrupt` across the
  pod's slots, and while an interrupt is pending it holds the whole-pod queue, so any further checkpoint
  or interrupt is dropped with a `BUSY` status ([§4.7](04_system-components.md#47-runtime-adapter),
  §28.6). One slot's operation therefore delays or refuses another slot's. It is also the one guard that
  spans boundaries, bounding `CH-CHECKPOINT` on the gateway-to-pod boundary, the `checkpoint_request` and
  `interrupt_request` frames of `CH-RUNTIMEOPS` on the intra-pod boundary, and the transfer `CH-OBJSTORE`
  carries on the pod-egress boundary (§28.6). The specification states no pod-level barrier lock beyond
  it, and no retry rule for a checkpoint dropped with `BUSY` on a concurrent-session pod (§28.6).
- The process-level co-tenancy the deployer acknowledged. Concurrent slots share the pod's process
  namespace, `/tmp`, cgroup memory, and network stack, and each slot's credential file is group-readable
  by every slot's agent process through the shared `lenny-cred-readers` supplementary group, which is not
  mitigated at the pod level. The shared network namespace carries cross-slot traffic observation, port
  binding conflicts, DNS resolver cache effects, and observable timing patterns, and the agent
  container's `securityContext` must drop `CAP_NET_RAW`
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  [§13.1](13_security-model.md#131-pod-security)). A client sees the condition as
  `sessionIsolationLevel.residualStateWarning` on the session creation response
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  [§7.1](07_session-lifecycle.md#71-normal-flow)).
- The occupancy and claim primitives. `REG-SLOTCOUNT` is an atomic per-pod counter that ceilings
  concurrent slots and is the occupancy authority, and `REG-CLAIM` is a cluster-wide per-pod acquisition
  on first claim (§28.3, [§6.2](06_warm-pod-model.md#62-pod-state-machine)). The specification states no
  per-slot claim object.
- The shared asset tree. `/workspace/shared/` is populated by the gateway during pod initialization
  before any slot is assigned, is mounted read-only at the container level so a write returns `EROFS`,
  and is not modified by the adapter afterwards
  ([§6.4](06_warm-pod-model.md#64-pod-filesystem-layout)).
- The pod's health and its disposition. The pod-level phase is the coarse `claimed` whenever occupancy is
  nonzero, a pod crash, node eviction, or OOM kill fails all active slots simultaneously, and the
  rolling-window failed slots plus the persistent leaked slots are counted against one whole-pod
  threshold of `ceil(maxConcurrentSessions/2)` that moves the pod to `draining`
  ([§6.2](06_warm-pod-model.md#62-pod-state-machine)).
- The pod's egress identity. `CH-LLMPROXY` binds its lease token to the issuing pod's SPIFFE identity and
  rejects a request whose peer SPIFFE URI does not match the lease record, which is a cross-pod replay
  control whose unit is the pod (§28.6,
  [§4.9](04_system-components.md#49-credential-leasing-service)).

**What the specification does not state.** Each of the following is a question a reader of the traces
above reaches on a concurrent-session pod and the specification does not answer. None of them is answered
here by inference from the partitioned or the shared list.

- Whether the adapter's hold state is partitioned per slot. The specification states that while the
  adapter is in hold state every inbound RPC other than `CoordinatorFence` is rejected with `UNAVAILABLE`
  and a `coordinator_hold` error detail, and it states that of the adapter rather than of a slot
  (§28.5.1, §28.6, [§10.1](10_gateway-internals.md#101-horizontal-scaling)). It does not state whether a
  fence driven for one slot's session holds the RPCs of a sibling slot's session.
- Whether the adapter's `Interrupt` RPC under the operation lock and the drain barrier are addressed to a
  slot. The specification qualifies checkpoint admission by `slotId` and states that the lock serializes
  `Interrupt` across the pod's slots ([§4.7](04_system-components.md#47-runtime-adapter), §28.6). §7.2
  does state the slot qualification for the `delivery: immediate` interrupt, which targets the specific
  slot's tool-call context ([§7.2](07_session-lifecycle.md#72-interactive-session-model)). The
  specification states no slot qualification for the `Interrupt` RPC the operation lock admits or for the
  drain barrier `CH-BARRIER` carries (§28.5.1).
- Which replica's connection carries an event on `CH-ADAPTEREVENTS` when more than one replica holds a
  connection to the pod. `CH-ADAPTEREVENTS` addresses its events to the session's coordinating replica
  while `LNK-POD-GRPC` states one connection per gateway replica per pod, and the specification does not
  resolve the two (§28.5.2, §28.6, §28.3).
- Whether the sessions occupying two slots on one pod may be coordinated by two different replicas.
  `REG-COORDLEASE` is keyed per tenant and session and `REG-CLAIM` is per pod (§28.3), so no register
  entry ties one slot's holder to another's, and the specification states no rule requiring the slots of
  one pod to share a coordinating replica.
- A buffering or replay policy for a message the adapter holds on `CH-MSGSOCK` while the runtime is
  absent, which the specification does not state on a pod of either kind (§28.5.3, §28.8).
