## 28. Communication Channels

This section is the normative home for the communication channels between the gateway replicas, the agent
pod, the adapter container, the runtime container, and the control plane. §28.1 states the law that fixes
each channel's identifier. §28.2 states the classes an identifier is drawn from and the axes every channel
records. §28.3 carries the registers of links, channels, and register entries, together with the naming
table that records the spelling each carrier takes. §28.4 states the claim register, which records the
implementation status of every statement this section makes.

### 28.1 Naming law

**N1.** A channel's canonical identifier is a mnemonic for the conversation it carries, chosen so that no
two channels on the same boundary share a stem. The endpoint pair, the plane, the dial direction, the
authority direction, and the transport are register columns in §28.3, so an identifier is not required to
encode any of them and is never read as the authoritative statement of one.

**N2.** Identifiers are mnemonic, uppercase, and hyphenated, under one of the three class prefixes §28.2
states. Positional identifiers are not used, because a channel added between two others must not renumber
its neighbours.

**N3.** Two words are reserved and may not stand as a bare noun phrase naming a conversation on this
surface: the word the platform uses for a resource's phase transitions, and the word it uses for a command
plane. The prohibition covers the space-separated spelling and the hyphenated compound spelling, and a
matcher joins two consecutive comment lines before it applies either spelling, so a phrase wrapped across a
comment boundary is one site. The prohibition's domain is `spec/`, `docs/`, `schemas/`, a Go doc comment in
a tracked Go file, and a tracked root-level markdown document the exclusion list below leaves in scope, of
which `README.md` and `TESTING.md` are the two that carry the phrase today. Outside that
domain are the historical audit records `BUILD-GAPS.md` and `TEST-GAPS.md`, the two root planning documents
`gateway-runtime-comms.md` and `gateway-runtime-comms-remediation.md`, the build and queue records
`BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`, the `proposals/` directory, and every
`testdata/` directory, each of which records a finding, a plan, or a fixture as it was written rather than
the current contract. This section describes the two reserved words rather than reproducing the banned
spellings, because the section sits inside the prohibition's own domain. The literal spellings are held
outside that domain, in the naming lint's matcher and in the agent-facing naming rules, which land with the
lint. Either word may
appear inside a canonical identifier. A markdown anchor identifier is outside the prohibition in both
spellings, because a kramdown attribute value and the fragment of an intra-repo link are addressable link
targets rather than prose, and an anchor that has to change moves through the anchor-redirect map so that a
redirect exists for every inbound link. An identifier stem may not reuse a term the specification already
binds to an unrelated mechanism.

**N4.** Each channel uses one identifier everywhere: the Go package or file name stem, the proto RPC name
stem, the metric label value, and the test name fragment for a test scoped to one channel. A gate or a test
spanning channels is named for the invariant it enforces and carries no channel identifier. The metric half
of N4 is deferred. The remediation step that adds the adapter metrics endpoint and its catalog entries is
the step that discharges it, because the adapter process emits those metrics inside the agent pod and they
sit outside the default scrape target set until a deployer wires an adapter scrape target. The deferral
carries a claim-register row with status `ABSENT` naming that step, per §28.4.

**N5.** A link identifier and the channel identifiers it carries share no stem, so a search for one never
returns the other.

**N6.** A register is named for the store and the key rather than for a verb.

**N7.** A flag, environment variable, or manifest key naming a channel carries that channel's identifier in
the form its carrier already fixes: a flag uses lowercase kebab, an environment variable uses upper snake,
and a manifest key uses the camelCase convention the §4.7 adapter manifest field set establishes.

**N8.** A specification citation names a heading rather than a line. Citing a specification line number is
retired and may not be written, in any spelling. The prohibition is on the line number rather than on one
form of words, so a spelling a matcher does not yet recognize is a gap in the matcher rather than a
permitted citation. A section that gives up content carries a permanent successor pointer naming the
heading that now owns the content and the identifiers that moved. The citation resolver and the
line-citation ratchet are the gates that hold this rule.

### 28.2 Taxonomy and axes

An identifier is drawn from one of three classes, and the class fixes the columns the entry carries in
§28.3.

| Class | Prefix | What it is | Columns it carries |
|:--|:--|:--|:--|
| Link | `LNK-` | A transport connection between two participants | Participants, dial direction, transport, endpoint, and lifetime |
| Channel | `CH-` | A typed conversation carried on one transport connection | Link, boundary, plane, dial direction, authority direction, transport, and message vocabulary |
| Register | `REG-` | Shared state mediating two participants with no live connection | Store, key or table, writer set, reader set, and semantics |

Every channel records six axes.

| Axis | Values | Why it is recorded separately |
|:--|:--|:--|
| Dial direction | The participant that opens the connection | A stream one participant opens can carry messages the other originates |
| Authority direction | The participant that originates the messages | The boundary a channel is grouped under follows this axis rather than the dial direction |
| Plane | Control, content, or state | Separates a channel carrying agent input and output from one carrying operational commands and one carrying stored data |
| Transport | gRPC, Unix socket JSON Lines, JSON-RPC, HTTP, SQL, Redis, or Kubernetes API | Closed set. A new value requires a specification change rather than an undeclared extension |
| Boundary | `intra-pod`, `gateway-to-pod`, `pod-to-gateway`, `pod-egress`, `gateway-to-store`, `inter-replica`, or `control-plane` | Closed set, and the grouping key of the contract cards, so a channel's boundary value and its card subsection carry the same string |
| Exclusivity | The granularity plus the enforcing guard, or the guard named as missing | Records whether two gateway replicas can hold the channel at once. The per-channel values are stated with the contract cards |

### 28.3 Registers

Three registers carry the entries of the three classes, one row per entry. The provenance column carries
the entry number the channel inventory in `gateway-runtime-comms.md` assigns, so a reader can recover the
derivation of every column without the retired prose being reproduced here. The naming table below the
three registers records the spelling each carrier takes for a channel whose identifier changed, per N7.

The link register declares a transport connection that more than one channel row refers to, either as the
connection it is carried on or as the connection its calls are forwarded over, together with a connection
the specification states and no channel is carried on yet. A channel's Link column names that entry. It
reads `None` when the connection carrying the channel is referred to by that channel's row alone, in which
case the channel row's transport column and the endpoint stated with the contract cards describe the
connection, and the connection takes a link entry at the point a second channel row refers to it.

#### Link register

| Identifier | Participants | Dial direction | Transport | Endpoint | Lifetime | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `LNK-POD-GRPC` | Gateway replica and pod adapter | Gateway | gRPC | Pod IP, TCP 50051 | One connection per gateway replica per pod | C1 |
| `LNK-GWCONTROL` | Pod adapter and gateway replica | Pod adapter | gRPC | Gateway service ClusterIP, TCP 50051 | One connection per pod process to one replica | C7 |
| `LNK-INTERREPLICA` | Gateway replica and gateway replica | Forwarding replica | gRPC | The internal gRPC `ForwardMessage` RPC, whose address the specification does not state | One connection per forwarding replica to a session's coordinating replica | C19 |

`LNK-INTERREPLICA` carries no channel row, because the specification states the connection and the
cross-replica message routing it carries is not implemented. That status is recorded as an `ABSENT`
claim-register row per §28.4 rather than as an absent transport in this register.

#### Channel register

| Identifier | Link | Boundary | Plane | Dial direction | Authority direction | Transport | Message vocabulary | Provenance |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `CH-ATTACH` | `LNK-POD-GRPC` | `gateway-to-pod` | Content | Gateway | Both | gRPC | Message delivery and agent output | C2 |
| `CH-CHECKPOINT` | `LNK-POD-GRPC` | `gateway-to-pod` | State | Gateway | Both | gRPC | Workspace capture | C3 |
| `CH-FENCE` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Coordinator handoff fence | C4 |
| `CH-BARRIER` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Quiesce and hold during gateway drain | C5 |
| `CH-PODHEALTH` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Adapter liveness probing | C20 |
| `CH-ADAPTEREVENTS` | `LNK-POD-GRPC` | `pod-to-gateway` | Control | Gateway | Pod adapter | gRPC | Adapter-to-gateway operational events | C6 |
| `CH-MSGSOCK` | None | `intra-pod` | Content | Runtime | Both | Unix socket JSON Lines | Agent message plane | C8 |
| `CH-RUNTIMEOPS` | None | `intra-pod` | Control | Runtime | Both | Unix socket JSON Lines | Cooperative quiesce, interrupt acknowledgement, and credential rotation | C9 |
| `CH-MCP-PLATFORM` | None | `intra-pod` | Content | Runtime | Runtime | JSON-RPC | Platform tool calls, forwarded over `LNK-GWCONTROL` | C10 |
| `CH-MCP-CONNECTOR` | None | `intra-pod` | Content | Runtime | Runtime | JSON-RPC | Connector tool calls, forwarded over `LNK-GWCONTROL` | C11 |
| `CH-LLMPROXY` | None | `pod-egress` | Content | Runtime | Runtime | HTTP | Proxy-mode model calls | C12 |
| `CH-OBJSTORE` | None | `pod-egress` | State | Pod adapter | Pod adapter | HTTP | Checkpoint chunk upload and restore | C13 |
| `CH-EVENTRELAY` | None | `gateway-to-store` | State | Gateway | Gateway | Redis | Cross-replica session event backlog | C16 |
| `CH-ADMISSION` | None | `control-plane` | Control | Admission webhook | Admission webhook | HTTP | Drain-readiness admission on pod eviction | C22 |

#### Register-entry register

| Identifier | Store | Key or table | Writer set | Reader set | Semantics | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `REG-COORDLEASE` | Redis | `t:<tenant>:lease:session:<session>` | Gateway replicas | Gateway replicas | One holder per tenant and session, on a compare-and-set with a 60 second expiry | C14 |
| `REG-COORDMIRROR` | Postgres | `coordination_lease` | Gateway sweeper | Gateway replicas | A single-valued row per tenant and session, and a projection rather than an exclusion primitive | C15 |
| `REG-SLOTCOUNT` | Redis | `lenny:pod:<pod>:active_slots` | Gateway replicas | Gateway replicas | An atomic per-pod counter that ceilings concurrent slots | C18 |
| `REG-PODSTATE` | Postgres | `agent_pod_state` | WarmPoolController for the mirrored `Sandbox` status columns, and gateway replicas for `sessions_served` and `scrub_failure_count` | Gateway replicas | One row per pod. A read-optimized mirror carrying the pod phase the orphan-session reconciler reads, together with the gateway-written reuse counters the recycle disposition evaluates ([§12.6](12_storage-architecture.md#126-interface-design), [§4.7](04_system-components.md#47-runtime-adapter)) | C21 |
| `REG-CLAIM` | Kubernetes API | `SandboxClaim` named `claim-<podName>` | Gateway replicas for the create, the status-subresource binding-state writes, and the hold-expiry delete, and the WarmPoolController leader for the deletes at pod termination and orphan garbage collection | Gateway replicas and controllers | Cluster-wide per-pod acquisition on first claim. The object carries no owner reference, so the controller's delete is the reclamation path for a claim its holder did not remove ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) | C17 |

#### Naming table

The table records, per carrier, the spelling a channel whose identifier changed takes on that carrier. A
retired spelling standing in the row that retires it is the declaration of that spelling rather than a
reference to the channel.

| channel | carrier | retired spelling | canonical spelling |
|:--|:--|:--|:--|
| `CH-ADAPTEREVENTS` | proto-rpc | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | go-symbol | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | path | `controlchannel` | `adapterevents` |
| `CH-RUNTIMEOPS` | go-symbol | `LifecycleChannel` | `RuntimeOps` |
| `CH-RUNTIMEOPS` | manifest-key | `lifecycleChannel` | `runtimeOps` |
| `CH-RUNTIMEOPS` | flag | `lifecycle-socket` | `runtime-ops-socket` |
| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| `CH-RUNTIMEOPS` | path | `lifecycle-events` | `runtime-ops-events` |
| `CH-RUNTIMEOPS` | path | `lifecyclechannel` | `runtimeops` |

### 28.4 Claim register

Every normative statement this section makes about a mechanism carries a row in the claim register at
`tests/claim-map.json`, with a status drawn from a closed set. `WIRED` means the mechanism is reachable from production code. `UNWIRED`
means it is implemented and has no production caller. `ABSENT` means it is specified and not implemented.

A `WIRED` row names the production surface that reaches the mechanism. A row whose status is not `WIRED`
names, through a deferral identifier, the step that closes it, which makes the register the work queue for
the steps that follow.

The claim register carries its own schema rather than the entry schema the migration registers share,
because a `WIRED` row is a permanent statement about the tree and carries no expiry, while a migration
register's entry expires. The register file, its seed rows, and the validator that reads it land with the
contract cards.

### 28.5 Contract cards

A contract card states the contract of one channel in the §28.3 channel register. The cards are grouped by
the channel's boundary value, one subsection per value of the closed boundary set §28.2 states, so the
subsection heading and the register's boundary column carry the same string. A card's citable handle is its
subsection number together with the channel identifier, which stays stable when a card is added above it.

Every card states the same fields in the same order, so two cards are comparable field by field. The
template sets no length: a channel whose contract is long is stated in full rather than shortened to match
its neighbours.

| Field | What it states |
|:--|:--|
| Link | The §28.3 link register entry carrying the channel, or `None` when the channel's own register row describes the connection |
| Endpoint | The address the channel is reached at, and the transport protection stated for it |
| Axes | Plane, dial direction, authority direction, and transport, restated from the channel's §28.3 register row |
| Messages | The messages the channel carries, in the spelling the specification gives them |
| Preconditions | What has to hold before a participant opens the channel or sends on it |
| Timing | The deadlines, timeouts, retry counts, and intervals that bound the channel |
| Exclusivity | The granularity of the exclusivity constraint and the guard that enforces it, or the guard named as missing, per §28.2 |
| Degradation | What the channel does when its peer is absent and when its transport fails mid-stream |

Every field names the section that states its content. Where the specification states no value for a field
of a channel, the card records that the specification does not state it. A card never supplies a plausible
value for a field no section states, because a reader cannot afterwards distinguish a value the
specification fixes from one the card invented. A mechanism a card states that is specified and not
implemented carries its status in the §28.4 claim register, so the card states the contract and the
register states how far the tree has reached it.

#### 28.5.1 Gateway-to-pod

This boundary carries the channels a gateway replica opens to the runtime adapter inside an agent pod. The
§28.3 channel register places these channels on this boundary, and each is carried on the `LNK-POD-GRPC`
connection.

```
  gateway replica                                            agent pod
  +------------------+                                +--------------------+
  |                  |   CH-ATTACH                    |                    |
  |                  |   CH-CHECKPOINT                |                    |
  |  gateway replica |   CH-FENCE           =====>    |   runtime adapter  |
  |                  |   CH-BARRIER                   |                    |
  |                  |   CH-PODHEALTH                 |                    |
  +------------------+                                +--------------------+

  LNK-POD-GRPC: pod IP, TCP 50051, gRPC over mTLS
```

**`CH-ATTACH`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register). Gateway-to-pod communication runs over gRPC
  with mTLS ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)), whose
  certificate issuance is stated in [§10.3](10_gateway-internals.md#103-mtls-pki).
- **Axes.** Content plane, dialled by the gateway, message authority on both sides, gRPC transport
  (§28.3).
- **Messages.** The `Attach` RPC connects a client stream to a running session
  ([§4.7](04_system-components.md#47-runtime-adapter)). Its streaming messages are bidirectional and are
  declared in the published protobuf service definition
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The specification does not state
  the individual message names of the stream in prose.
- **Preconditions.** The pod validates the `coordination_generation` stamp on every gateway-to-pod RPC and
  rejects a stale one, and a replica that has just acquired coordination must receive a successful
  `CoordinatorFence` acknowledgement before it sends any operational RPC
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).
- **Timing.** The specification states no deadline for `Attach`. The connection carrying it uses gRPC
  keepalive probes at a 10s interval with a 5s timeout
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).
- **Exclusivity.** One coordinating replica per session. The guard is the session-coordination lease
  `REG-COORDLEASE`, held in Redis on a compare-and-set with a TTL and falling back to a Postgres
  `SELECT ... FOR UPDATE SKIP LOCKED` on the session row, together with the `coordination_generation`
  stamp the pod validates ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).
- **Degradation.** When the coordinating replica is lost, the pod's gRPC transport detects the broken
  connection within 15 seconds and the adapter enters hold state, in which every inbound RPC other than
  `CoordinatorFence` is rejected with `UNAVAILABLE` and a `coordinator_hold` error detail. A replica that
  receives a generation-stale rejection cancels all in-flight RPCs for the session without retrying and
  discards its cached in-memory streams ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). A gRPC
  error on the stream while the coordinator is unchanged and the session is `running` transitions the
  session to `resume_pending` while retries remain, and the session is re-attached on a replacement pod
  from its last checkpoint when a pod is allocated within `maxResumeWindowSeconds`, or reaches
  `awaiting_client_action` when that window elapses or the retries are exhausted
  ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
  [§7.3](07_session-lifecycle.md#73-retry-and-resume)). The specification does not state whether the
  gateway redials `Attach` against the same pod before treating the pod as lost.

**`CH-CHECKPOINT`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register), over gRPC with mTLS
  ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)).
- **Axes.** State plane, dialled by the gateway, message authority on both sides, gRPC transport (§28.3).
- **Messages.** The `Checkpoint` RPC is a gateway-driven bidirectional grant and confirm stream: the
  gateway mints a per-chunk upload capability and confirms each uploaded chunk, and the adapter streams the
  workspace archive as chunks directly to object storage
  ([§4.7](04_system-components.md#47-runtime-adapter)). An upload retry that outlives its grant's expiry
  requests a fresh grant for the same chunk index on the open stream, and the adapter reports a
  retry-exhausted failure on the stream ([§4.4](04_system-components.md#44-event--checkpoint-store)).
- **Preconditions.** The generation stamp and the fence acknowledgement that govern every gateway-to-pod
  RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The adapter's pod-level operation lock
  must be free or must promote this checkpoint from its queue
  ([§4.7](04_system-components.md#47-runtime-adapter)). On the eviction path the agent pod cannot open the
  stream itself: it signals its coordinating replica on `CH-ADAPTEREVENTS`, and that replica drives the
  stream under its held lease
  ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).
- **Timing.** Every checkpoint path enforces a 60-second timeout measured from the initial quiescence
  request to completion ([§4.4](04_system-components.md#44-event--checkpoint-store)). A checkpoint the
  gateway drives against a barrier-held pod is additionally bounded by
  `checkpointBarrierAckTimeoutSeconds`, default 90s ([§4.7](04_system-components.md#47-runtime-adapter),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). Non-eviction upload retries use
  exponential backoff from 200ms at factor 2 for up to about 5 seconds in total
  ([§4.4](04_system-components.md#44-event--checkpoint-store)). Scheduling is bounded by the freshness
  requirement that every active session have a successful checkpoint within
  `periodicCheckpointIntervalSeconds`, default 600s
  ([§4.4](04_system-components.md#44-event--checkpoint-store)).
- **Exclusivity.** Pod-level. The adapter maintains an operation lock that serializes `Checkpoint` and
  `Interrupt` across the pod's slots, admitting one pending checkpoint per distinct `slotId` and coalescing
  a checkpoint whose `slotId` is already pending; on a single-session pod at most one operation may be
  queued ([§4.7](04_system-components.md#47-runtime-adapter)). Above that lock, the session-coordination
  lease `REG-COORDLEASE` and the generation stamp restrict the channel to the coordinating replica
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).
- **Degradation.** An attempt that ends before every declared byte is confirmed leaves a manifest row
  flagged `partial = true`, which is not a valid checkpoint. A deadline fire on a drain, preStop, or
  barrier path retains its chunks as a recovery aid the resume path reassembles, while a stream truncation,
  an adapter crash, a supersession, or a quota refusal leaves no resume candidate and the gateway sweeps
  the chunk prefix. When all upload retries fail on a non-eviction checkpoint the adapter resumes the agent
  immediately, the attempt is discarded, and the gateway increments
  `lenny_checkpoint_storage_failure_total` ([§4.4](04_system-components.md#44-event--checkpoint-store)).
  While the adapter is in hold state the RPC is rejected with `UNAVAILABLE` and a `coordinator_hold` error
  detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).

**`CH-FENCE`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register), over gRPC with mTLS
  ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)).
- **Axes.** Control plane, dialled by the gateway, message authority with the gateway, gRPC transport
  (§28.3).
- **Messages.** `CoordinatorFence(session_id, new_generation)` announces the new `coordination_generation`
  to the pod on coordinator handoff. The pod records the generation and from that point rejects any RPC
  carrying an older one ([§4.7](04_system-components.md#47-runtime-adapter),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)).
- **Preconditions.** The acquiring replica reads the session row, then increments
  `coordination_generation` with a compare-and-swap that returns the replica's local generation stamp,
  before it sends the fence. The fence is itself the hard precondition for every other operational RPC to
  the pod ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§4.7](04_system-components.md#47-runtime-adapter)).
- **Timing.** A 5-second deadline, hard-coded and not configurable
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). On failure or timeout the new
  coordinator retries the same generation value up to 3 attempts with 1-second backoff; when the retries
  are exhausted it relinquishes the lease and backs off with jittered delay from 2s to a 16s maximum before
  reconsidering coordination ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).
- **Exclusivity.** One coordinating replica per session, established by this channel. The guard is the
  session-coordination lease `REG-COORDLEASE` with its Postgres fallback, and the acknowledgement of this
  fence is what closes the window in which the prior coordinator's RPCs are still accepted
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).
- **Degradation.** When the announced generation exceeds the last fenced generation by more than one, the
  adapter cancels and discards every in-flight RPC received after the last fenced generation, resets the
  transient state accumulated since it, logs a `coordinator_generation_gap` event, and acknowledges the
  fence normally. This is the one channel the adapter still accepts in hold state, and it is the only exit
  from it. When no successful fence arrives within `coordinatorHoldTimeoutSeconds`, default 120s, the
  adapter begins graceful session termination with reason `coordinator_lost`
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).

**`CH-BARRIER`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register), over gRPC with mTLS
  ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)).
- **Axes.** Control plane, dialled by the gateway, message authority with the gateway, gRPC transport
  (§28.3).
- **Messages.** `CheckpointBarrier` carries `coordination_generation` and `barrier_id`. The adapter
  quiesces tool-call dispatch and holds quiescence while the gateway drives the `Checkpoint` stream against
  the held pod. The acknowledgement is not carried on this channel: the adapter replies with
  `CheckpointBarrierAck`, carrying `barrier_id` and the gateway-minted `checkpoint_ref`, on
  `CH-ADAPTEREVENTS`, whose card is in §28.5.2 ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Preconditions.** The generation stamp and the fence acknowledgement that govern every gateway-to-pod
  RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The barrier is dispatched during the
  gateway replica's graceful drain ([§4.7](04_system-components.md#47-runtime-adapter),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)).
- **Timing.** A single wall-clock deadline across all the pods a draining replica coordinates,
  `checkpointBarrierAckTimeoutSeconds`, default 90s ([§4.7](04_system-components.md#47-runtime-adapter),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).
- **Exclusivity.** One coordinating replica per session, on the same `REG-COORDLEASE` lease and
  `coordination_generation` stamp as the other gateway-to-pod channels; the barrier carries that generation
  in its own message ([§4.7](04_system-components.md#47-runtime-adapter),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). The specification does not state a
  separate pod-level barrier lock beyond the operation lock `CH-CHECKPOINT` states.
- **Degradation.** When the deadline fires before every declared byte of the barrier-driven checkpoint is
  confirmed and chunks were already committed for the session, the gateway finalises the partial
  checkpoint manifest with `manifest_reason = "timeout"`, and that row and its chunks are retained as the
  recovery aid the resume path reassembles ([§4.4](04_system-components.md#44-event--checkpoint-store),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)). When no chunks were committed, no intent row
  exists, or the read of that row fails, the gateway soft-deletes any such row and falls back to the
  session's last successful periodic checkpoint
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). While the adapter is in hold state the RPC
  is rejected with `UNAVAILABLE` and a `coordinator_hold` error detail
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state what the
  adapter does with a held quiescence whose barrier is never followed by a `Checkpoint` stream.

**`CH-PODHEALTH`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register), over gRPC with mTLS
  ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)).
- **Axes.** Control plane, dialled by the gateway, message authority with the gateway, gRPC transport
  (§28.3).
- **Messages.** The gRPC Health Checking Protocol
  ([§4.7](04_system-components.md#47-runtime-adapter)), whose binding is declared in the published protobuf
  service definition ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The
  specification does not state which service names are probed.
- **Preconditions.** The specification states no precondition for the probe itself. It states that pods
  validate the `coordination_generation` on every gateway-to-pod RPC
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) and does not state how that rule applies to a
  probe against a pod that is not yet serving a coordinated session.
- **Timing.** The specification does not state a probe interval, a probe deadline, or a failure threshold
  for this channel. The connection carrying it uses gRPC keepalive probes at a 10s interval with a 5s
  timeout ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).
- **Exclusivity.** The specification states no exclusivity constraint and names no enforcing guard for this
  channel. §28.3 records the gateway as the dialling participant, while the consumer of the result is the
  warm pool controller, which marks a pod `idle` only once the health check passes
  ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Degradation.** A pod whose health check has not passed is not marked `idle` and so is not offered to a
  session ([§4.7](04_system-components.md#47-runtime-adapter)). Loss of the underlying connection is
  detected by the gRPC transport within 15 seconds and puts the adapter into hold state
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state what the
  gateway does with a pod whose health check fails after the pod has been marked `idle`.

#### 28.5.2 Pod-to-gateway

This boundary carries the channel whose messages the runtime adapter inside an agent pod originates and the
gateway replica consumes. The §28.3 channel register places one channel on this boundary,
`CH-ADAPTEREVENTS`, and it is carried on the same `LNK-POD-GRPC` connection the gateway dials for the
§28.5.1 channels, so the boundary follows the authority direction rather than the dial direction.

```
  gateway replica                                            agent pod
  +------------------+                                +--------------------+
  |                  |                                |                    |
  |  gateway replica |   CH-ADAPTEREVENTS   <=====    |   runtime adapter  |
  |                  |                                |                    |
  +------------------+                                +--------------------+

  LNK-POD-GRPC: pod IP, TCP 50051, gRPC over mTLS, dialled by the gateway
```

**`CH-ADAPTEREVENTS`**

- **Link.** `LNK-POD-GRPC` (§28.3).
- **Endpoint.** The pod IP on TCP 50051 (§28.3 link register). §10.1 names the same port 50051 for this
  channel ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Gateway-to-pod communication runs over
  gRPC with mTLS ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)), whose
  certificate issuance is stated in [§10.3](10_gateway-internals.md#103-mtls-pki).
- **Axes.** Control plane, dialled by the gateway, message authority with the pod adapter, gRPC transport
  (§28.3).
- **Messages.** The adapter-to-gateway events `RATE_LIMITED` (the current credential is rate-limited and a
  fallback is requested), `AUTH_EXPIRED` (the credential lease expired or was rejected by the provider),
  `PROVIDER_UNAVAILABLE` (the provider endpoint is unreachable), `LEASE_REJECTED` (the runtime cannot use
  the assigned credential), `CheckpointBarrierAck` carrying `barrier_id` and `checkpoint_ref`,
  `AdapterTerminating` carrying `session_id` and `reason`, and `FINAL_USAGE_REPORT` as the last message
  before the stream closes ([§4.7](04_system-components.md#47-runtime-adapter)). The channel additionally
  carries the slot-failure event a concurrent-session pod emits when a slot's session fails
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) and the signal
  an agent pod's preStop hook sends to its coordinating replica to have that replica drive the eviction
  checkpoint ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). The specification
  states no message name for either of those two, and it does not state the stream's message envelope in
  prose; the binding is declared in the published protobuf service definition
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).
- **Preconditions.** The specification states no fence or generation precondition for the adapter's own
  messages; the `coordination_generation` stamp it states is validated on gateway-to-pod RPCs
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Per message it states these conditions:
  `CheckpointBarrierAck` is sent only after quiescence is reached and after the gateway-driven `Checkpoint`
  stream terminates, echoing the gateway-minted `checkpoint_id`
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)); `FINAL_USAGE_REPORT` is sent after every
  in-flight `ReportUsage` pull has settled ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease));
  `AdapterTerminating` is sent when the adapter initiates its own termination, such as at the
  `coordinatorHoldTimeoutSeconds` expiry ([§10.1](10_gateway-internals.md#101-horizontal-scaling)); and in
  direct delivery mode `AUTH_EXPIRED` is reported when a lease's local expiry timer fires with no
  replacement lease delivered, after the adapter deletes the credential file for that provider's entry
  ([§4.9](04_system-components.md#49-credential-leasing-service)).
- **Timing.** The specification states no send deadline, retry count, or interval for the channel itself.
  The connection carrying it uses gRPC keepalive probes at a 10s interval with a 5s timeout
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). Two messages are bounded by a
  gateway-side wait: `CheckpointBarrierAck` by the single wall-clock deadline
  `checkpointBarrierAckTimeoutSeconds`, default 90s, across all the pods a draining replica coordinates
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)), and `FINAL_USAGE_REPORT` by the usage
  quiescence timeout, default 5s and configurable through `delegation.usageQuiescenceTimeoutSeconds`
  ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).
- **Exclusivity.** The specification states no exclusivity constraint on the channel itself and names no
  guard that enforces one. The events are addressed to the session's coordinating replica: the preStop
  eviction signal goes to the coordinating replica, which drives the eviction checkpoint under its held
  lease ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)), and the coordinating
  replica is the holder of the session-coordination lease `REG-COORDLEASE`
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3). The `LNK-POD-GRPC` register row states
  one connection per gateway replica per pod (§28.3), so the specification does not state which replica's
  connection carries an event when more than one replica holds a connection to the pod.
- **Degradation.** While the adapter is in hold state it pauses runtime activity and originates no new
  operational messages on this channel, until either a new coordinator fences it or
  `coordinatorHoldTimeoutSeconds` expires and it sends the final `AdapterTerminating`
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). When the channel is unavailable at that point,
  because the coordinating replica has itself crashed, the adapter logs the delivery failure and writes the
  post-mortem to local disk, and the orphan session reconciler detects the terminated pod within one
  60-second reconcile interval and forcibly transitions the session to `failed` with reason
  `orphan_pod_terminated`, a worst-case 60-second detection delay against a delivered `AdapterTerminating`
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). A stream close is a terminal signal in its own
  right on the delegation path: the gateway waits for `FINAL_USAGE_REPORT` or for the stream to close,
  whichever comes first, and when neither occurs within the usage quiescence timeout it proceeds with the
  last known usage counter and emits a `delegation.budget_return_usage_lag` warning event
  ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)). Loss of the underlying connection is
  detected by the pod's gRPC transport within 15 seconds and puts the adapter into hold state
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state a retry or
  buffering policy for an event other than `AdapterTerminating` whose delivery fails.

#### 28.5.3 Intra-pod

This boundary carries the channels between the runtime adapter and the runtime binary inside one agent
pod. The §28.3 channel register places these channels on this boundary, and each carries `None` in its
Link column, so each channel's own register row together with the endpoint stated in its card describes
the connection it runs on. The runtime is the dialling participant on every channel here.

```
  agent pod
  +----------------------------------------------------------------------+
  |                                                                      |
  |  +------------------+                          +------------------+  |
  |  |                  |   CH-MSGSOCK             |                  |  |
  |  |                  |   CH-RUNTIMEOPS          |                  |  |
  |  | runtime adapter  |                  <=====  |  runtime binary  |  |
  |  |                  |   CH-MCP-PLATFORM        |                  |  |
  |  |                  |   CH-MCP-CONNECTOR       |                  |  |
  |  +------------------+                          +------------------+  |
  |                                                                      |
  +----------------------------------------------------------------------+

  The runtime-ops and MCP channels run on abstract Unix sockets in the Linux
  abstract namespace. The runtime dials every channel drawn above.
```

**`CH-MSGSOCK`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** §28.3 records the transport as Unix socket JSON Lines. The adapter
  communicates with the agent binary over stdin and stdout using newline-delimited JSON, and
  [§4.7](04_system-components.md#47-runtime-adapter) states that the default sidecar deployment
  communicates with the agent binary over abstract Unix sockets in the Linux abstract namespace, which
  carry no filesystem path. The specification states no socket name for this channel. The transport
  protections it states for the adapter-agent boundary are the `SO_PEERCRED` peer-UID check against the
  expected agent UID and the manifest-nonce handshake presented as the first message on the socket. When
  `Runtime.spec.requireSoPeercred` is `false` the peer check is unavailable and the adapter supplements
  the static nonce with a per-connection 128-bit challenge whose `HMAC-SHA256` response it validates
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§5.1](05_runtime-registry-and-pool-model.md#51-runtime)).
- **Axes.** Content plane, dialled by the runtime, message authority on both sides, Unix socket JSON
  Lines transport (§28.3).
- **Messages.** Adapter to runtime: `message`, `tool_result`, `heartbeat`, and `shutdown`. Runtime to
  adapter: `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`. Each message
  is a single JSON object terminated by `\n`. A `message` carries an `input` field holding a
  `MessagePart` array, and a `response` carries its parts in an `output` array;
  `heartbeat` and `shutdown` use their own minimal schemas and are not `MessageEnvelope` instances
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). Every `tool_result.id` matches
  the `id` of a previously emitted `tool_call`, results may arrive in any order, and a `tool_result`
  whose `id` is unknown is dropped and logged as a protocol error
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). `message`, `tool_result`,
  `response`, `tool_call`, `set_tracing_context`, and `status` carry a `sessionId` naming the session the
  frame is addressed to, on every pod whatever its pool's `sessionPolicy.maxConcurrentSessions`. The gateway mints
  that identifier at claim time, the adapter populates it on the frames it emits, and a pod multiplexes
  every session's stream over the one channel keyed on it
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
- **Preconditions.** The adapter writes the final adapter manifest and spawns the runtime binary before
  it delivers the first `message`, with the runtime's connection to the MCP servers and the
  `CH-RUNTIMEOPS` capability handshake in between
  ([§4.7](04_system-components.md#47-runtime-adapter)). The adapter is the protocol initiator: it sends
  messages and tool-call instructions and receives responses, and the agent cannot initiate an arbitrary
  request to the adapter ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Timing.** The adapter sends a periodic `heartbeat`, and a runtime that does not answer with
  `heartbeat_ack` within 10 seconds is treated as hung and is sent SIGTERM
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The specification does not
  state the heartbeat interval. A `shutdown` carries a `deadline_ms` by which the runtime must finish its
  current work and exit, with no acknowledgement required; a runtime that has not exited by the deadline
  is sent SIGTERM and then SIGKILL 10 seconds later
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). In nonce-only mode the
  challenge response is due within 500 ms ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Exclusivity.** The specification states no exclusivity constraint on this channel and names no
  enforcing guard. A pod multiplexes every session's stream over the one channel, keyed by `sessionId`,
  whatever its pool's `sessionPolicy.maxConcurrentSessions`. A runtime implements a dispatch loop keyed on
  `sessionId` on every pod, because each session's messages carry a distinct `sessionId` that the gateway
  mints at claim time and the adapter populates, which is what carries multiple independent concurrent
  session streams through the one channel.
- **Degradation.** Every outbound message is flushed before the runtime blocks on its next read; a
  runtime that leaves the message in a buffer never reaches the adapter and the session hangs silently
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). When the agent process
  crashes, the adapter detects the socket EOF, reports the failure to the gateway, and does not restart
  the agent; retry is handled by the gateway at the session level
  ([§4.7](04_system-components.md#47-runtime-adapter)). When the process exits non-zero without emitting
  a `response`, the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr, and when a
  `response` carries an `error` field the adapter maps the task to `failed` and populates
  `TaskResult.error` from it ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The
  specification does not state a buffering or replay policy for a message the adapter holds while the
  runtime is absent.

This card owns the schema of each `CH-MSGSOCK` message and the schema of the `MessagePart` content
envelope those messages carry. Both are stated below.

**Message schemas**

All **content** messages on stdin (type `message`) use the full `MessageEnvelope` format ([Section 15.4](15_external-api-surface.md#messageenvelope--unified-message-format)). Lifecycle messages (`heartbeat`, `shutdown`) use their own minimal schemas defined below and are not `MessageEnvelope` instances. Runtimes MUST ignore unrecognized fields. Basic-level runtimes need only read `type`, `id`, and `input` — all other envelope fields (`from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `slotId`) can be safely ignored.

**Inbound: `message`**

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [{ "type": "text", "inline": "What is 2+2?" }],
  "from": { "kind": "client", "id": "client_8f3a2b" },
  "threadId": "t_01",
  "delivery": "queued",
  "slotId": "slot_01"
}
```

Basic-level: read `type`, `id`, `input`. Ignore all other fields. `sessionId` names the session this message is addressed to, and the adapter populates it on every pod.

**Inbound: `heartbeat`**

```json
{ "type": "heartbeat", "ts": 1717430400 }
```

Agent must respond with `heartbeat_ack` (see below). If no ack within 10 seconds, the adapter considers the process hung and sends SIGTERM.

**Inbound: `shutdown`**

```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

Agent must finish current work and exit within `deadline_ms`. No acknowledgment required — the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.

**Inbound: `tool_result`**

Schema:

```json
{
  "type": "tool_result",
  "id": "<string, required — matches the tool_call.id this result responds to>",
  "content": ["<MessagePart[], required — result content>"],
  "isError": "<boolean, optional — true if tool execution failed; defaults to false>",
  "sessionId": "<string, required — the session this frame is addressed to; the adapter populates it on every pod>"
}
```

Example:

```json
{
  "type": "tool_result",
  "id": "tc_001",
  "content": [{ "type": "text", "inline": "file contents here" }],
  "isError": false
}
```

**Correlation:** Every `tool_result.id` MUST match the `id` of a previously emitted `tool_call`. The adapter validates this — a `tool_result` with an unknown `id` is dropped and logged as a protocol error. Agents may have multiple outstanding `tool_call` requests; results may arrive in any order.

**Delivery semantics:** Tool calls use synchronous request/response semantics within the stdin/stdout channel. The agent emits a `tool_call`, then continues reading stdin until it receives the matching `tool_result` (identified by `id`). Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` — the agent must handle interleaved delivery. There is no async callback or webhook mechanism; all tool results are delivered inline on stdin.

**Tool access by level:**

| Level        | Tool access                                                                                                        | `tool_call` / `tool_result` behavior                                                                                                                                                                                                                               |
| ------------ | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Basic**    | No MCP tools available. The agent binary has no platform MCP server or connector MCP servers.                      | Agents MAY still emit `tool_call` for adapter-local tools (e.g., `read_file`, `write_file` provided by the adapter's local sandbox tooling). The adapter resolves these locally and returns `tool_result` on stdin. No platform or connector tools are accessible. |
| **Standard** | Platform MCP server tools (`lenny/delegate_task`, `lenny/request_input`, etc.) and per-connector MCP server tools. | The agent calls MCP tools via the MCP client connection to the adapter's local servers (not via `tool_call` on stdin). The stdin `tool_call`/`tool_result` channel is used for adapter-local tools only.                                                           |
| **Full**     | Same as Standard plus CH-RUNTIMEOPS capabilities.                                                              | Same as Standard.                                                                                                                                                                                                                                                  |

**Outbound: `response`**

```json
{
  "type": "response",
  "output": [{ "type": "text", "inline": "The answer is 4." }],
  "sessionId": "<string, optional — the session this frame is addressed to; the runtime echoes the identifier it was handed, and an absent identifier resolves to the receiving stream's binding on a pod holding at most one slot and is rejected on a pod holding more>"
}
```

Basic-level shorthand (adapter normalizes to canonical form above):

```json
{ "type": "response", "text": "The answer is 4." }
```

**Error reporting via `response`.** The `response` message supports an optional `error` field for structured error reporting: `{"code": string, "message": string}`, matching the `TaskResult.error` shape. When `error` is present, the adapter maps the task to `failed` state and populates `TaskResult.error` from the response error. This allows runtimes to report failure details while still delivering partial output in the `output` array, without relying solely on non-zero exit codes (which lose error context). When `error` is absent and the process exits zero, the task completes successfully. When the process exits non-zero without emitting a `response`, the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr.

**Relationship between `lenny/output` and stdout `response`:** At Standard and Full levels, runtimes may emit output parts incrementally via the `lenny/output` platform MCP tool. The stdout `{type: "response"}` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`; runtimes that have already emitted all output parts via `lenny/output` send an empty `output` array (`[])`. The adapter concatenates `lenny/output` parts (in call order) with the final `response.output` parts to form the complete task output. Basic-level runtimes, which have no access to `lenny/output`, must include all output in the stdout `response.output` array. Standard-level runtimes may use either delivery path or both.

**Outbound: `tool_call`**

Schema:

```json
{
  "type": "tool_call",
  "id": "<string, required — unique call identifier; used to correlate the inbound tool_result>",
  "name": "<string, required — tool name>",
  "arguments": "<object, required — tool-specific parameters; validated by the adapter against the tool's input schema>",
  "sessionId": "<string, optional — the session this frame is addressed to; the runtime echoes the identifier it was handed, and an absent identifier resolves to the receiving stream's binding on a pod holding at most one slot and is rejected on a pod holding more>"
}
```

Example:

```json
{
  "type": "tool_call",
  "id": "tc_001",
  "name": "read_file",
  "arguments": { "path": "/workspace/foo.txt" }
}
```

The `id` field is generated by the agent and must be unique within the session. Recommended format: `tc_` prefix with a monotonic counter or random suffix (e.g., `tc_001`, `tc_a7f3b`). The adapter uses this `id` to route the corresponding `tool_result` back on stdin.

**Adapter-Local Tool Reference**

Adapter-local tools are resolved entirely within the adapter process — no MCP server, no platform access, and no network call is required. They are available at all levels (Basic, Standard, Full). The following tools are built into every adapter:

| Tool name     | Description                                                       |
| ------------- | ----------------------------------------------------------------- |
| `read_file`   | Read the contents of a file in the workspace                      |
| `write_file`  | Create or overwrite a file in the workspace                       |
| `list_dir`    | List the entries of a directory in the workspace                  |
| `delete_file` | Delete a file or empty directory from the workspace               |

Discovery: agents discover adapter-local tools by inspecting the `adapterLocalTools` array in the adapter manifest (`/run/lenny/adapter-manifest.json`). Each entry contains the tool `name`, a human-readable `description`, and a JSON Schema `inputSchema` for its `arguments` object. Adapters MUST populate this array before spawning the runtime; the set is fixed for the lifetime of the pod.

Schemas for the four built-in tools:

```json
[
  {
    "name": "read_file",
    "description": "Read the contents of a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the file." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "write_file",
    "description": "Create or overwrite a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path":    { "type": "string", "description": "Workspace-relative or absolute path to the file." },
        "content": { "type": "string", "description": "UTF-8 text content to write." }
      },
      "required": ["path", "content"]
    }
  },
  {
    "name": "list_dir",
    "description": "List the entries of a directory in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the directory." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "delete_file",
    "description": "Delete a file or empty directory from the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the target." }
      },
      "required": ["path"]
    }
  }
]
```

All `read_file` / `write_file` / `list_dir` / `delete_file` calls are confined to the pod's workspace volume (`/workspace`). The adapter rejects any path that resolves outside `/workspace` with a `tool_result` carrying `isError: true` and `content[0].inline` set to the string `"path_outside_workspace"`. Custom adapters MAY extend this list with additional adapter-local tools; they MUST declare all custom tools in `adapterLocalTools` before spawning the runtime.

**Outbound: `heartbeat_ack`**

```json
{ "type": "heartbeat_ack" }
```

**Outbound: `status` (optional)**

Schema:

```json
{
  "type": "status",
  "state": "<string, required — the state the runtime reports>",
  "message": "<string, optional — human-readable detail>",
  "sessionId": "<string, optional — the session this frame is addressed to; the runtime echoes the identifier it was handed, and an absent identifier resolves to the receiving stream's binding on a pod holding at most one slot and is rejected on a pod holding more>"
}
```

Example:

```json
{ "type": "status", "state": "thinking", "message": "Analyzing code...", "sessionId": "sess_abc123" }
```

`status` is a session-scoped frame. The runtime populates `sessionId` on every pod, and the adapter
resolves an absent identifier to the receiving stream's binding on a pod holding at most one slot and
rejects the frame, relaying it to no stream, on a pod holding more.

**Outbound: `set_tracing_context`**

Registers tracing identifiers for propagation through delegation.

Schema:

```json
{
  "type": "set_tracing_context",
  "context": "<map<string, string>, required — opaque tracing identifiers>",
  "sessionId": "<string, optional — the session this frame is addressed to; the runtime echoes the identifier it was handed, and an absent identifier resolves to the receiving stream's binding on a pod holding at most one slot and is rejected on a pod holding more>"
}
```

Example:

```json
{
  "type": "set_tracing_context",
  "context": { "langsmith_run_id": "run_abc123" }
}
```

The frame is available at all integration levels.

**Addressing.** The adapter resolves the frame against the Attach stream that delivered it. A stream is
bound to a session and, on a pod serving more than one concurrent session, to that session's slot. The
adapter handles the frame if and only if both of the following hold.

1. **Address equality.** The frame's `slotId` equals the stream's `slotId`, compared as exact string
   equality, with an absent or empty `slotId` counting as the empty string on both sides. A frame
   carrying a `slotId` on a stream that holds none is therefore not handled, and neither is an untagged
   frame on a stream bound to a slot.
2. **Live-binding confirmation.** The adapter's registry still binds that address to this stream's
   session, and the address is unambiguous. When the stream carries no `slotId`, the pod's session is
   still this stream's session and the pod holds no registered slot. When the stream carries a `slotId`,
   the registry entry for that slot still names this stream's session.

Otherwise the adapter drops the frame, counts it, and logs a protocol error. It relays nothing onward and
returns nothing to the runtime, because the inbound message set on this channel admits no report frame,
which is the same outcome this card states for a `tool_result` whose `id` is unknown.

Address equality is the only condition that names a session. Live-binding confirmation reads state that
changes over the session's lifetime and may only reject a frame, so a released binding drops the frame
rather than routing it elsewhere.

**Registration.** The adapter registers the identifiers by calling the platform tool with the addressed
session's id injected. The gateway merges the submitted context into that session's recorded context and
validates the result against the rules in
[Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) when the identifiers are
registered, and it attaches the registered context to the child's delegation lease. See
[Section 16.3](16_observability.md#163-distributed-tracing) for the two-tier tracing model.

**Non-guarantee.** One runtime process serves every slot on a concurrent pod, so the `slotId` a frame
carries is whatever that process stamped on it. The addressing rule removes ambiguity between the slots
of one pod. It does not detect a frame the runtime process itself addressed to the wrong slot, and it is
not an isolation guarantee against a misbehaving runtime.

**Exit Codes**

| Code | Meaning                                                            |
| ---- | ------------------------------------------------------------------ |
| 0    | Normal completion — session ended cleanly or shutdown honored      |
| 1    | Runtime error — adapter logs stderr and reports failure to gateway |
| 2    | Protocol error — agent could not parse inbound messages            |
| 137  | SIGKILL (set by OS) — adapter treats as crash, pod is not reused   |

Any non-zero exit during an active session causes the gateway to report a session error to the client. During draining, exit code 0 confirms graceful shutdown; non-zero triggers an alert but the session result (if any) is still delivered.

**Annotated Protocol Trace — Basic-Level Session**

```
1. Adapter starts agent binary, stdin/stdout pipes open.
2. Adapter writes to stdin:
   {"type": "message", "id": "msg_001", "input": [{"type": "text", "inline": "Hello"}], "from": {"kind": "client", "id": "client_8f3a2b"}, "threadId": "t_01"}
3. Agent reads line from stdin, parses JSON, reads type/id/input (ignores other fields).
4. Agent writes to stdout (either form is valid):
   {"type": "response", "text": "Echo: Hello"}
   — or equivalently —
   {"type": "response", "output": [{"type": "text", "inline": "Echo: Hello"}]}
5. Adapter reads line from stdout, delivers response to gateway.
6. [Heartbeat interval] Adapter writes:
   {"type": "heartbeat", "ts": 1717430410}
7. Agent writes:
   {"type": "heartbeat_ack"}
8. Gateway initiates shutdown. Adapter writes:
   {"type": "shutdown", "reason": "drain", "deadline_ms": 10000}
9. Agent finishes, exits with code 0.
10. Adapter reports clean termination to gateway.
```

**Internal `MessagePart` format**

`agent_text` streaming event is replaced by `agent_output` carrying `MessagePart` array. `TaskResult` and `TaskSpec` use `MessagePart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "schemaVersion": 1,
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "lenny-blob://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

**Properties:**

- **`schemaVersion` is an integer identifying the MessagePart schema revision (default `1`).** Present on every persisted `MessagePart`. The forward-compatibility contract has obligations on both sides:
  - **Producer obligation:** Producers MUST set `schemaVersion` to the highest version required by the fields they emit. When a schema version introduces semantically important fields (e.g., `citations` in v2), the producer MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.
  - **Consumer obligation — streaming/live delivery:** Consumers MUST NOT reject a `MessagePart` solely because its `schemaVersion` is higher than the consumer understands. When a consumer encounters a `schemaVersion` it does not recognize, it processes the fields it does understand and MUST surface a **degradation signal**: a `schema_version_ahead` annotation on the parent `MessageEnvelope` (with `"knownVersion"` and `"encounteredVersion"` fields) so the end user or upstream caller is informed that the response may be incomplete. Consumers MUST NOT silently discard unknown fields without this signal. This ensures data loss from schema mismatch is always visible rather than hidden. `schema_version_ahead` is scoped specifically to the "new writer, old reader" direction on the `schemaVersion` field of a record; it is one of several distinct degradation annotation kinds catalogued in [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7 (Degradation annotation catalog) and MUST NOT be reused for unrelated signals such as retired MCP protocol versions.
  - **Consumer obligation — durable storage (TaskRecord):** When `MessagePart` arrays are persisted as part of a `TaskRecord` ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), the forward-read rule from [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7 applies: if a reader encounters a `MessagePart` with a `schemaVersion` it does not recognize, it MUST **forward-read** — process all fields it understands and preserve all unknown fields verbatim (pass-through) — rather than rejecting the record. Billing and audit records retained for 13 months will span multiple schema revisions; silent data loss or outright rejection in these records is unacceptable. If a durable consumer cannot safely pass through unknown fields (e.g., it writes to a schema-strict sink), it MUST emit a `durable_schema_version_ahead` structured error to an operator alert channel and queue the record for manual review rather than dropping it. This rule is consistent with the general durable-consumer rule in [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7 and extends it explicitly to `MessagePart` arrays embedded within persisted `TaskRecord` objects.
- **`type` is an open string — not a closed enum — with a versioned canonical type registry.** The registry defines platform-defined types and their guaranteed translation behavior per adapter. Unprefixed names are reserved for the platform registry; third-party extensibility uses the `x-<vendor>/` namespace (see namespace convention below). Any type not in the current registry version is treated as a custom type and falls back to `text` with the original type preserved in `annotations.originalType`. Types may be added to the registry in minor releases; removing a type or changing its translation behavior is a breaking change requiring a major version bump. To preserve forward-compatibility across minor releases, unknown unprefixed types are **not** rejected at ingress — they are passed through with the same custom-type fallback, plus an `unregistered_platform_type` warning annotation, so that a newly registered type can be emitted by an updated runtime before all gateways have been upgraded. This retains open-string extensibility while making translation deterministic across adapter implementations.

  **Canonical Type Registry (v1):**

  | Type               | Description                                   | MCP Translation                                              | OpenAI Translation                           | A2A Translation                                      |
  | ------------------ | --------------------------------------------- | ------------------------------------------------------------ | -------------------------------------------- | ---------------------------------------------------- |
  | `text`             | Plain or formatted text                       | `TextContent` block                                          | `text` content                               | A2A `TextPart`                                       |
  | `code`             | Source code with optional language annotation | `TextContent` with `language` annotation                     | `text` content                               | A2A `TextPart` with `mimeType`                       |
  | `reasoning_trace`  | Model reasoning/chain-of-thought              | `TextContent` with `thinking` annotation                     | `text` content (reasoning not representable) | A2A `TextPart` with `metadata.semantic: "reasoning"` |
  | `citation`         | Source citation or reference                  | `TextContent` with citation annotation                       | `text` content                               | A2A `TextPart` with `metadata.semantic: "citation"`  |
  | `screenshot`       | Screen capture image                          | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `image`            | General image content                         | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `diff`             | Code diff / patch                             | `TextContent` with `language: "diff"`                        | `text` content                               | A2A `TextPart` with `mimeType: "text/x-diff"`        |
  | `file`             | File content (binary or text)                 | `ResourceContent` block                                      | Resolved to inline `text` or dropped         | A2A `FilePart`                                       |
  | `execution_result` | Compound output from code execution           | Flattened to sequential `TextContent` blocks with `parentId` | Flattened to sequential `text` entries       | A2A composite part                                   |
  | `error`            | Error or diagnostic message                   | `TextContent` with `isError: true`                           | `text` content                               | A2A `TextPart` with `metadata.semantic: "error"`     |

  **Custom types** (any `type` value not listed above): collapsed to `text` with `annotations.originalType` set to the original type string. Runtimes may emit any custom type; the gateway passes them through internally but adapters apply the fallback rule at the protocol boundary. The registry is published as part of the runtime adapter specification and versioned alongside the adapter protocol.

  **Namespace convention for third-party types.** To avoid collisions with future platform-defined types, all vendor- or community-defined custom types MUST use a reverse-DNS namespace prefix in the form `x-<vendor>/<typeName>` (e.g., `x-acme/heatmap`, `x-myorg/audio-transcript`). Unprefixed names are reserved for platform-defined registry types. The gateway logs and annotates unknown unprefixed types at ingress (adding an `unregistered_platform_type` warning annotation with the unrecognized type string) but does **not** reject them — they fall through to the standard custom-type-to-`text` collapse so that newly registered types introduced in a minor release are forward-compatible across gateway versions that have not yet been upgraded.

  **`schemaVersion` per-type contract.** The `schemaVersion` field on a `MessagePart` is scoped to the envelope schema (field set, semantics of existing fields). The stable field set guaranteed at each registry version is:

  | Type               | `schemaVersion` 1 — guaranteed fields                                               | Notes on future versions                                              |
  | ------------------ | ----------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
  | `text`             | `type`, `inline`, `mimeType` (`text/plain`)                                         | v2 may add `citations[]`                                              |
  | `code`             | `type`, `inline`, `mimeType`, `annotations.language`                                | —                                                                     |
  | `reasoning_trace`  | `type`, `inline`                                                                    | v2 may add structured `steps[]`                                       |
  | `citation`         | `type`, `inline`, `annotations.source`                                              | —                                                                     |
  | `screenshot`       | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `image`            | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `diff`             | `type`, `inline`, `annotations.language` (`diff`)                                   | —                                                                     |
  | `file`             | `type`, `inline` or `ref`, `mimeType`                                               | —                                                                     |
  | `execution_result` | `type`, `parts[]` (each part is a full `MessagePart`)                                | v2 may add `exitCode`, `duration`                                     |
  | `error`            | `type`, `inline` (human-readable message), `annotations.errorCode` (optional)       | —                                                                     |

  A producer emitting fields that were introduced in a later schema version MUST set `schemaVersion` to that version. Consumers that encounter a `(type, schemaVersion)` combination they do not recognize apply the forward-compatibility rules defined above: degradation signal for live delivery; forward-read with unknown-field preservation for durable storage (see [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7).

- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` — resolution protocol.** A part either contains bytes inline (`inline` field set, base64 for binary content) or references external blob storage (`ref` field set). The two fields are mutually exclusive on any given part; setting both is a validation error (`400 MESSAGEPART_INLINE_REF_CONFLICT`). The gateway selects the representation automatically based on part size:

  | Part size | Gateway action | Consumer sees |
  | --- | --- | --- |
  | ≤ 64 KB | Store inline (base64 for binary, UTF-8 for text) | `inline` field populated; `ref` absent |
  | > 64 KB and ≤ 50 MB | Stage to blob store; set `ref` to `LennyBlobURI` | `ref` populated; `inline` absent |
  | > 50 MB | Rejected at ingress | `413 MESSAGEPART_TOO_LARGE` |

  **`LennyBlobURI` scheme.** Blob references use the URI scheme `lenny-blob://`:

  ```
  lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl={seconds}&enc=aes256gcm
  ```

  | Component | Description |
  | --- | --- |
  | `tenant_id` | Tenant namespace — prevents cross-tenant dereference |
  | `session_id` | Originating session — scopes the blob to one session |
  | `part_id` | Stable part identifier (matches `MessagePart.id`) |
  | `ttl` | Seconds until the blob expires in storage (see TTL table below) |
  | `enc` | Encryption indicator; always `aes256gcm` for stored blobs |

  **Immutability guarantee.** Blob storage is write-once per `(tenant_id, session_id, part_id)` triple. The gateway writes a blob exactly once when staging a `MessagePart`; subsequent reads always return the same bytes. No `generation` component is needed in the URI because part IDs are globally unique within a session — the internal `coordination_generation` counter ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) is used only for coordinator fencing and never causes part IDs to be reused or existing blobs to be overwritten. A `lenny-blob://` URI is safe to cache and share for the duration of its `ttl`.

  **TTL policy by context:**

  | Context | Default TTL | Configurable? |
  | --- | --- | --- |
  | Live streaming delivery (session active) | 3 600 s (1 h) | Yes — `blobStore.liveDeliveryTtlSeconds` |
  | Persisted in `TaskRecord` | 2 592 000 s (30 d) | Yes — `blobStore.taskRecordTtlSeconds` |
  | Audit / billing event payload | 34 128 000 s (13 months) | Yes — `blobStore.auditTtlSeconds` |
  | Delegation export (parent → child) | Duration of child session + 1 h | Fixed |

  **Consumer fallback obligation.** When a consumer encounters a `ref` it cannot dereference (blob expired, storage unavailable, network partition), it MUST:
  1. Surface a `blob_ref_unresolvable` degradation annotation on the `MessageEnvelope` (fields: `partId`, `ref`, `reason`).
  2. Substitute a placeholder `MessagePart` of type `error` with `inline: "Blob reference unresolvable: {reason}"`.
  3. Never silently drop the part.

  **Adapter dereference obligation.** External protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields before serializing outbound messages to external clients — external protocols do not speak `lenny-blob://`. The REST adapter passes `ref` values through as-is (REST clients may dereference directly via `GET /v1/blobs/{ref}`).
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched.

**MCP content block → MessagePart mapping (inbound translation):** When the gateway receives MCP content blocks from a client and delivers them to a runtime, the adapter translates each MCP block to a `MessagePart` as follows:

| MCP content block type | → `MessagePart.type` | `MessagePart.inline` source                  | `MessagePart.mimeType`          | `MessagePart.ref` source   | Notes                                                             |
| ---------------------- | ------------------- | ------------------------------------------- | ------------------------------ | ------------------------- | ----------------------------------------------------------------- |
| `TextContent`          | `text`              | `text` field                                | `text/plain`                   | —                         | `language` annotation → `annotations.language` if present        |
| `ImageContent` (url)   | `image`             | —                                           | from `mimeType` if present     | `url.url`                 | URL set as `ref`; inline not populated                            |
| `ImageContent` (base64)| `image`             | base64 data string                          | `mimeType`                     | —                         | Stored inline                                                     |
| `EmbeddedResource` (text blob) | `file`    | resource text content                       | `text/plain` or resource MIME  | —                         | Stored inline when small; large blobs staged to artifact store    |
| `EmbeddedResource` (blob)      | `file`    | —                                           | resource MIME type             | artifact URI              | Staged to artifact store; `ref` set to `lenny-blob://` URI        |
| `EmbeddedResource` (uri)       | `file`    | —                                           | resource MIME type             | resource URI              | `ref` set directly from resource URI                              |
| MCP `isError: true` annotation | `error`   | inherited from enclosing block              | —                              | —                         | `type` overridden to `error`; `annotations.errorCode` populated if present |

Runtime authors who produce output using MCP-familiar content block objects can use the `from_mcp_content()` helper (see below) to perform this translation without manual field mapping.

**Minimum required fields for Basic-level runtimes:** Only `type` and `inline` are required. All other fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional and have sensible defaults — `schemaVersion` defaults to `1` if absent, `id` is generated by the adapter if absent, `mimeType` defaults to `text/plain` for `type: "text"`, `status` defaults to `complete` for non-streaming responses. A minimal valid `MessagePart` is `{"type": "text", "inline": "hello"}`.

**Simplified text-only response shorthand:** Basic-level runtimes may emit a simplified response form with a top-level `text` field instead of an `output` array:

```json
{ "type": "response", "text": "The answer is 4." }
```

The adapter normalizes this to the canonical form `{"type": "response", "output": [{"type": "text", "inline": "The answer is 4."}]}` before forwarding to the gateway. This shorthand is strictly equivalent — runtimes that need structured output (multiple parts, non-text types, annotations) use the full `output` array form.

**Optional SDK helper `from_mcp_content(blocks)`** converts MCP content blocks to `MessagePart` arrays for runtime authors who want to produce output using familiar MCP formats. Availability:

- **Go:** Ships in the `github.com/lennylabs/runtime-sdk-go/messagepart` sub-package of the Runtime Author SDK ([§15.7](15_external-api-surface.md#157-runtime-author-sdks)) (Phase 2 deliverable). Import the package and call `messagepart.FromMCPContent(blocks)`.
- **Other languages:** Not yet published as a library. Use the mapping table above to implement the conversion inline — the logic is a straightforward switch on `content.type`. A copy-paste reference implementation is distributed alongside the runtime adapter specification artifacts ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification)).
- **No SDK required:** Runtimes can construct `MessagePart` objects directly without any Lenny SDK dependency. The SDK helper is a convenience only.

**`CH-RUNTIMEOPS`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** The abstract Unix socket `@lenny-runtime-ops`, advertised in the adapter manifest as
  `runtimeOps.socket` ([§4.7](04_system-components.md#47-runtime-adapter)). The runtime connects as a
  client and the adapter listens. The protections stated for the socket are the
  `SO_PEERCRED` peer-UID check against the expected agent UID and the manifest-nonce handshake, which the
  runtime presents as the first message on the socket. When `Runtime.spec.requireSoPeercred` is `false`
  the peer check is unavailable and the adapter supplements the static nonce with a per-connection
  128-bit challenge whose `HMAC-SHA256` response it validates
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§5.1](05_runtime-registry-and-pool-model.md#51-runtime)).
- **Axes.** Control plane, dialled by the runtime, message authority on both sides, Unix socket JSON
  Lines transport (§28.3).
- **Messages.** Adapter to runtime: `lifecycle_capabilities`, `checkpoint_request`,
  `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `terminate`, and
  `deadline_approaching`. Runtime to adapter: `lifecycle_support`, `checkpoint_ready`,
  `interrupt_acknowledged`, `credentials_acknowledged`, `llm_request_started`, and
  `llm_request_completed`. Each message is a single JSON object terminated by `\n` with `type` as its
  discriminator, and the field set of each is the message-schema table below. The channel is versioned by
  the capability negotiation at its top, where `lifecycle_capabilities` carries the `protocolVersion` the
  adapter offers. An unknown message is silently ignored on both sides. In direct mode
  `llm_request_completed` optionally carries the per-call `inputTokens` and `outputTokens` counts that
  supply the direct-mode usage source
  ([§11.2](11_policy-and-controls.md#112-budgets-and-quotas)).

  | `type`                      | Direction          | Fields                                                                                                                                                              | Notes                                                                       |
  | --------------------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
  | `lifecycle_capabilities`    | Adapter → Runtime  | `type`, `protocolVersion` (string, e.g., `"1.0"`), `capabilities` (array of strings: `"checkpoint"`, `"interrupt"`, `"credential_rotation"`, `"deadline_signal"`) | First message sent on channel open. Runtime must reply with `lifecycle_support`. |
  | `lifecycle_support`         | Runtime → Adapter  | `type`, `capabilities` (array of strings — subset of offered capabilities the runtime supports)                                                                    | Runtime's capability handshake reply.                                       |
  | `checkpoint_request`        | Adapter → Runtime  | `type`, `checkpointId` (string), `deadlineMs` (integer — ms until adapter times out waiting)                                                                       | Adapter requests runtime quiesce and signal readiness. Runtime must reply with `checkpoint_ready` within `deadlineMs`. |
  | `checkpoint_complete`       | Adapter → Runtime  | `type`, `checkpointId` (string), `status` (`"ok"` \| `"failed"`), `reason` (string, present when `status: "failed"`)                                              | Confirms snapshot upload result; runtime may resume.                        |
  | `interrupt_request`         | Adapter → Runtime  | `type`, `interruptId` (string), `deadlineMs` (integer)                                                                                                             | Requests the runtime reach a safe stop point within `deadlineMs`. **Timeout behavior:** if `interrupt_acknowledged` is not received within `deadlineMs`, the adapter transitions the session to `suspended` anyway (best-effort — the deadline has elapsed so the runtime is assumed to have stopped making progress) and returns an `INTERRUPT_TIMEOUT` status in the `Interrupt` RPC response to the gateway. The gateway logs the timeout and proceeds with the `suspended` state normally. The session is NOT left in `running` on timeout. |
  | `credentials_rotated`       | Adapter → Runtime  | `type`, `provider` (string), `credentialsPath` (string — path to updated `/run/lenny/credentials.json`), `leaseId` (string)                                        | New credentials written; runtime must rebind and reply with `credentials_acknowledged`. |
  | `terminate`                 | Adapter → Runtime  | `type`, `deadlineMs` (integer), `reason` (string: `"session_complete"` \| `"budget_exhausted"` \| `"eviction"` \| `"operator"`)                                    | Graceful shutdown signal. Runtime must exit within `deadlineMs`; adapter sends SIGTERM on timeout. Receipt always means process exit. |
  | `deadline_approaching`      | Adapter → Runtime  | `type`, `remainingMs` (integer — ms until session expiry or budget exhaustion), `trigger` (`"session_age"` \| `"budget"` \| `"idle"`)                              | Advance warning before forced termination. Runtime should wrap up work.     |
  | `checkpoint_ready`          | Runtime → Adapter  | `type`, `checkpointId` (string)                                                                                                                                    | Runtime has quiesced and is ready for snapshot.                             |
  | `interrupt_acknowledged`    | Runtime → Adapter  | `type`, `interruptId` (string)                                                                                                                                     | Runtime has reached a safe stop point.                                      |
  | `credentials_acknowledged`  | Runtime → Adapter  | `type`, `leaseId` (string), `provider` (string)                                                                                                                    | Runtime has rebound to the new credential. Adapter releases queued LLM requests with new credential. |
  | `llm_request_started`       | Runtime → Adapter  | `type`, `requestId` (string — opaque, runtime-generated), `provider` (string)                                                                                      | Runtime is about to send an outbound LLM request directly to the provider (direct mode only). Adapter increments the in-flight counter for this provider. Only required when the runtime calls the LLM API directly (not via the adapter proxy). |
  | `llm_request_completed`     | Runtime → Adapter  | `type`, `requestId` (string — matches the corresponding `llm_request_started`), `provider` (string), `status` (`"ok"` \| `"error"`), `inputTokens` (integer, optional), `outputTokens` (integer, optional)                               | Runtime's outbound LLM request has completed or errored. Adapter decrements the in-flight counter. When the counter reaches zero and a credential rotation is pending, the adapter proceeds to send `credentials_rotated`. In direct mode the runtime SHOULD populate `inputTokens` and `outputTokens` from the completed provider response when it can extract them; the adapter accumulates them into a per-session cumulative total internally and reports the incremental delta since the last read over the [§4.7](04_system-components.md#47-runtime-adapter) `ReportUsage` RPC (see [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). A runtime that cannot extract counts omits both fields, and the session has no direct-mode token source. |
- **Preconditions.** The channel is optional and is opened by Full-level runtimes; a runtime that does
  not open it operates in fallback-only mode ([§4.7](04_system-components.md#47-runtime-adapter),
  [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The runtime reads
  `runtimeOps.socket` from the manifest the adapter writes before it spawns the runtime binary
  ([§4.7](04_system-components.md#47-runtime-adapter)). `lifecycle_capabilities` is the first message
  sent on channel open and the runtime replies with `lifecycle_support`, which is the handshake the
  gateway reads to select the credential-rotation strategy for the session (the message-schema table
  above, [§4.7](04_system-components.md#47-runtime-adapter)). Before it sends `credentials_rotated` the adapter
  rewrites `/run/lenny/credentials.json` and waits for the in-flight LLM request gate to clear
  ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Timing.** `checkpoint_request`, `interrupt_request`, `terminate`, and `deadline_approaching` each
  carry a millisecond field that bounds the runtime's reply, its exit, or the remaining session time, as
  the message-schema table above states. Every checkpoint path is bounded by a 60-second
  timeout measured from the initial quiescence request to completion
  ([§4.4](04_system-components.md#44-event--checkpoint-store)). The adapter enforces a 60-second timeout
  on `credentials_acknowledged`, starting when `credentials_rotated` is sent, and the old credential is
  not returned to the pool until that reply arrives or the timeout elapses
  ([§4.7](04_system-components.md#47-runtime-adapter)). The in-flight gate before
  `credentials_rotated` is unbounded for `rotationTrigger: proactive_renewal` and capped at 300 seconds
  for every other trigger, and a wait beyond 60 seconds emits a
  `credential_rotation_inflight_wait_long` warning event
  ([§4.7](04_system-components.md#47-runtime-adapter)). In nonce-only mode the challenge response is due
  within 500 ms ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Exclusivity.** The specification states no exclusivity constraint on this channel and names no
  enforcing guard. It bounds the operations the frames carry rather than the channel: the adapter's
  pod-level operation lock serializes `Checkpoint` and `Interrupt` across the pod's slots, so a
  `checkpoint_request` and an `interrupt_request` are not outstanding at the same time
  ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Degradation.** When `interrupt_acknowledged` does not arrive within the frame's `deadlineMs`, the
  adapter transitions the session to `suspended` anyway and returns an `INTERRUPT_TIMEOUT` status in the
  `Interrupt` RPC response; the session is not left in `running`, as the message-schema table above
  states. When `credentials_acknowledged` does not arrive
  within 60 seconds, the adapter emits a `credential_rotation_timeout` warning event, increments
  `lenny_credential_rotation_timeout_total`, and falls back to the Standard-level rotation path of
  checkpoint, pod termination, replacement pod, `AssignCredentials`, and `Resume`
  ([§4.7](04_system-components.md#47-runtime-adapter)). When the runtime has not exited by the
  `terminate` frame's `deadlineMs` the adapter sends SIGTERM, as the message-schema table above
  states. When the peer is absent, because the runtime
  never opened the channel, interrupt degrades to SIGTERM-based termination and the deadline warning and
  the drain coordination signal are not delivered, at both Basic level and Standard level. At Standard
  level a checkpoint degrades to a best-effort snapshot without a runtime pause and credential rotation
  degrades to the checkpoint and restart path. At Basic level there is no checkpoint support: pod failure
  loses the in-flight context, the gateway restarts the session from its last gateway-persisted state,
  and credential rotation over the same restart path loses the in-flight context
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). When the runtime has sent
  `checkpoint_ready` and no `checkpoint_complete` arrives within 60 seconds, the runtime autonomously
  resumes normal operation and logs a `checkpoint_timeout` warning, which is the stated protection
  against an adapter crash or a partition during the snapshot phase
  ([§4.4](04_system-components.md#44-event--checkpoint-store)). The specification does not
  state what the adapter does when the socket fails mid-session while the runtime process is still
  running.

**`CH-MCP-PLATFORM`**

- **Link.** `None` (§28.3). §28.3 records the tool calls this channel carries as forwarded over
  `LNK-GWCONTROL`.
- **Endpoint.** An abstract Unix socket whose name the adapter manifest advertises under
  `platformMcpServer.socket`, for example `@lenny-platform-mcp`
  ([§4.7](04_system-components.md#47-runtime-adapter)). Intra-pod MCP servers use abstract Unix sockets
  exclusively, and there is no stdio transport for intra-pod MCP
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The protection stated for the
  connection is the manifest-nonce handshake: the runtime presents the manifest's `mcpNonce` as the
  top-level `_lennyNonce` field of the MCP `initialize` request's `params` object, the adapter validates
  it before any tool dispatch and strips it before the request reaches its MCP server implementation, and
  a connection that does not present a valid nonce is closed immediately. The nonce a server validates
  against is the one the manifest carried at the start that bound that server, and a later session's
  manifest write does not re-arm a running server
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels),
  [§4.7](04_system-components.md#47-runtime-adapter)).
- **Axes.** Content plane, dialled by the runtime, message authority with the runtime, JSON-RPC transport
  (§28.3).
- **Messages.** MCP. The runtime calls `tools/list` to discover the server's tools and `tools/call` to
  invoke one ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The platform tool
  set is `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`,
  `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`,
  `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, and `lenny/set_tracing_context`
  ([§9.1](09_mcp-integration.md#91-where-mcp-is-used)). Lease extension is an internal gateway operation
  and is not exposed as a tool on this channel
  ([§8.6](08_recursive-delegation.md#86-lease-extension)). There is no workspace MCP server: the
  workspace is materialized to `/workspace/current` before the runtime starts and the runtime reaches it
  through the filesystem directly ([§4.7](04_system-components.md#47-runtime-adapter)).
  The servers speak MCP 2025-03-26 and also accept MCP 2024-11-05, and the adapter never advertises the
  `sampling` MCP capability to the local server
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels),
  [§4.7](04_system-components.md#47-runtime-adapter)).
- **Preconditions.** The adapter writes the manifest carrying `platformMcpServer.socket` and `mcpNonce`
  before it spawns the runtime binary, and a Standard-level or Full-level runtime reads both from it
  ([§4.7](04_system-components.md#47-runtime-adapter)). The nonce is validated before any tool is
  dispatched ([§4.7](04_system-components.md#47-runtime-adapter)). A Basic-level runtime has no platform
  MCP server and reaches none of these tools
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)).
- **Timing.** The specification states no deadline, retry count, or interval for the channel itself. It
  states gateway-side bounds on individual tools this channel carries. `lenny/request_input` blocks until
  an answer arrives and is bounded by `maxRequestInputWaitSeconds`, after which the gateway delivers a
  `REQUEST_INPUT_TIMEOUT` tool-call error ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). `lenny/request_elicitation` is
  bounded by `maxElicitationWaitSeconds`, and each hop that forwards an elicitation is bounded by a
  30-second forwarding timeout ([§9.2](09_mcp-integration.md#92-elicitation-chain),
  [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).
- **Exclusivity.** The specification states no exclusivity constraint on this channel and names no
  enforcing guard. The manifest nonce authenticates a connection to the pod's intra-pod MCP servers, which
  are pod-wide and started at most once per pod, so the nonce a server validates against is the one the
  manifest carried at the start that bound that server and a later session's manifest write does not
  re-arm it. The adapter resolves the calling session to the single session the pod's shared runtime
  process has been given, and refuses the call unless that process has been given exactly one session and
  that session is the caller ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Degradation.** A connection that does not present a valid nonce is rejected before any tool is
  dispatched, which is what prevents a pod-local process without manifest access from calling a
  privileged platform tool ([§4.7](04_system-components.md#47-runtime-adapter),
  [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). When the peer is absent,
  because the runtime is Basic-level and connects to no MCP server, delegation, discovery, elicitation,
  inter-session messaging, and blocking input requests are unavailable with no fallback, and the runtime
  produces all of its output on `CH-MSGSOCK`
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The specification does not
  state what the adapter does when the server fails to bind its socket or when the connection drops
  mid-session.

**`CH-MCP-CONNECTOR`**

- **Link.** `None` (§28.3). §28.3 records the tool calls this channel carries as forwarded over
  `LNK-GWCONTROL`.
- **Endpoint.** One abstract Unix socket per authorized connector, whose name the adapter manifest
  advertises in the `connectorServers` array alongside the connector's `id`, for example
  `@lenny-connector-github` ([§4.7](04_system-components.md#47-runtime-adapter)). The transport rule and
  the manifest-nonce handshake are the ones stated for every intra-pod MCP connection, and the adapter
  requires the nonce on each connector server's connection separately
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels),
  [§4.7](04_system-components.md#47-runtime-adapter)).
- **Axes.** Content plane, dialled by the runtime, message authority with the runtime, JSON-RPC transport
  (§28.3).
- **Messages.** MCP. The runtime calls `tools/list` on each connector server to discover that connector's
  tools and `tools/call` to invoke one
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). Each authorized connector in
  the session's delegation policy gets its own independent server, and no aggregated connector proxy
  exists, because aggregation is not lossless under the MCP specification: capability negotiation is
  per-server, sampling breaks, tool names collide, and resource URIs collide. The call is served by the gateway acting as the
  MCP client to the external tool, so the tokens the external tool requires never enter the pod
  ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)).
- **Preconditions.** The manifest carries a `connectorServers` array, which is empty when no connector is
  authorized and is never absent, and the adapter writes it before it spawns the runtime binary
  ([§4.7](04_system-components.md#47-runtime-adapter)). The set of authorized connectors is fixed by the
  session's `DelegationPolicy`, and the gateway validates the `connector_id` of every external tool call
  against the calling pod's effective policy before proxying it
  ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc),
  [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)). The nonce is validated before any
  tool is dispatched ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Timing.** The specification states no deadline, retry count, or interval for this channel.
- **Exclusivity.** The specification states no exclusivity constraint on this channel and names no
  enforcing guard. The same manifest nonce that authenticates a connection to `CH-MCP-PLATFORM`
  authenticates a connection here, required on each connector server's connection separately. Each
  connector server is pod-wide and started at most once per pod, the nonce it validates against is the one
  the manifest carried at the start that bound it, and the adapter resolves the calling session to the
  single session the pod's shared runtime process has been given, refusing the call unless that process
  has been given exactly one session and that session is the caller
  ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Degradation.** A connection that does not present a valid nonce is rejected before any tool is
  dispatched ([§4.7](04_system-components.md#47-runtime-adapter),
  [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). A tool call that requires user
  authorization is answered by an auth challenge the gateway turns into a URL-mode elicitation carried
  hop by hop up to the client, after which the gateway stores the resulting tokens and later calls from
  pods authorized for that connector use the gateway-held connector state
  ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)). The specification does not state
  what happens to the call that raised the challenge. When the peer is absent, because
  the runtime is Basic-level and connects to no MCP server, no connector tool is reachable and there is
  no fallback ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The specification
  does not state what the adapter does when one connector server fails to bind its socket while the
  others bind, and it does not state what happens to the channel when a connector's authorization is
  revoked mid-session.

#### 28.5.4 Inter-replica

This boundary carries the channels between two gateway replicas. The §28.3 channel register places no
channel on this boundary, so this subsection carries no card. The §28.3 link register declares the
connection such a channel would be carried on, `LNK-INTERREPLICA`, between a forwarding gateway replica
and a session's coordinating gateway replica, dialled by the forwarding replica over gRPC, with one
connection per forwarding replica to a session's coordinating replica. The specification does not state
the address that connection is reached at (§28.3).

```
  forwarding replica                                 coordinating replica
  +----------------------+                    +----------------------+
  |                      |                    |                      |
  |   gateway replica    |  LNK-INTERREPLICA  |   gateway replica    |
  |                      |        =====>      |                      |
  +----------------------+                    +----------------------+

  LNK-INTERREPLICA: gRPC, dialled by the forwarding replica, at an address the
  specification does not state. The §28.3 channel register places no channel on
  this boundary, so this subsection carries no card.
```

The conversation the connection carries is the internal gRPC `ForwardMessage` RPC used for cross-replica
message routing. A message carrying `delivery: immediate` that lands on a replica that is not the
session's coordinator is forwarded to that session's coordinator, which is identified through the
coordination lease. The coordinator executes the atomic resume-and-deliver sequence when the session
still holds its pod, and the `suspended` to `resume_pending` transition with the message held in the
session inbox when the pod has been released. When the coordinator is
unreachable the forwarding replica falls back to inbox buffering with a `queued` delivery receipt status
rather than dropping the message ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
[§10.1](10_gateway-internals.md#101-horizontal-scaling)). §28.3 records that this cross-replica message
routing is not implemented and that the status is carried as an `ABSENT` claim-register row per §28.4.
This subsection gains a card at the point the §28.3 channel register places a channel on this boundary.

#### 28.5.5 Pod-egress

This boundary carries the channels an agent pod originates to a destination outside the pod. The §28.3
channel register places two channels on this boundary, `CH-LLMPROXY` and `CH-OBJSTORE`, and each carries
`None` in its Link column, so each channel's own register row together with the endpoint stated in its
card describes the connection. Both are governed by the default-deny egress posture of
[§13.2](13_security-model.md#132-network-isolation): an agent pod reaches no egress port beyond those
named by the base policy `allow-pod-egress-base` and whichever supplemental policies its pool selects, and
each of these two channels is permitted by a supplemental policy rather than by the base policy.

```
  agent pod                                              egress destinations
  +--------------------+                            +----------------------+
  |                    |   CH-LLMPROXY    =====>    |  gateway LLM proxy   |
  |   agent pod        |                            +----------------------+
  |                    |                            +----------------------+
  |                    |   CH-OBJSTORE    =====>    |  object storage      |
  +--------------------+                            +----------------------+

  CH-LLMPROXY: gateway, TCP 8443, HTTPS, permitted by allow-pod-egress-llm-proxy
  CH-OBJSTORE: object store, TLS, permitted by allow-pod-egress-objectstore
```

**`CH-LLMPROXY`**

- **Link.** `None` (§28.3).
- **Endpoint.** The gateway's LLM proxy port, TCP 8443, reached at the `proxyUrl` the pod receives in its
  `CredentialLease`; a pool declares the address as its `proxyEndpoint`
  ([§4.9](04_system-components.md#49-credential-leasing-service),
  [§13.2](13_security-model.md#132-network-isolation)). The endpoint must use TLS: a pool registration
  whose `proxyEndpoint` uses an `http://` scheme is rejected with the validation error
  `InvalidProxyEndpointScheme`, and the endpoint shares the mTLS certificate infrastructure of the internal
  gRPC control plane ([§4.9](04_system-components.md#49-credential-leasing-service)), whose certificate
  issuance is stated in [§10.3](10_gateway-internals.md#103-mtls-pki).
- **Axes.** Content plane, dialled by the runtime, message authority with the runtime, HTTP transport
  (§28.3).
- **Messages.** The requests of the pool's declared `proxyDialect`. The `openai` dialect exposes
  `POST {proxyUrl}/v1/chat/completions`, `POST {proxyUrl}/v1/embeddings`, and the streaming SSE variant,
  and accepts the OpenAI request body with a `model` field the gateway's translator maps to the upstream
  provider's model ID. The `anthropic` dialect exposes `POST {proxyUrl}/v1/messages` and its streaming
  variant, and accepts the Anthropic Messages API request body; the proxy accepts the `anthropic-version`
  header from the runtime and injects the configured default when the header is absent, advertising that
  default to runtimes through the adapter manifest's `llm.headers` field
  ([§4.9](04_system-components.md#49-credential-leasing-service)). Responses are the upstream provider's
  responses translated back into the dialect the pod speaks, streamed to the pod
  ([§4.9](04_system-components.md#49-credential-leasing-service)).
- **Preconditions.** The pod's pool runs `deliveryMode: proxy`, so the pod receives a lease token, a
  `proxyUrl`, and a `proxyDialect` in its `CredentialLease` rather than a materialized upstream API key
  ([§4.9](04_system-components.md#49-credential-leasing-service)). The pool's `proxyDialect` must match a
  dialect the Runtime declares in `credentialCapabilities.proxyDialect`, which admission control enforces
  by rejecting a mismatched pool registration or update with `422 INVALID_POOL_PROXY_DIALECT`
  ([§4.9](04_system-components.md#49-credential-leasing-service),
  [§5.1](05_runtime-registry-and-pool-model.md#51-runtime)). The pod must carry the
  `lenny.dev/delivery-mode: proxy` label the WarmPoolController sets on pods of proxy-mode pools, because
  port 8443 is excluded from the base egress policy and is permitted only by the supplemental
  `allow-pod-egress-llm-proxy` policy that selects on that label; a direct-mode pod carries no such label
  and cannot reach the port ([§13.2](13_security-model.md#132-network-isolation)). Per request, the
  gateway validates the lease token against `TokenStore`, verifies the peer SPIFFE URI from the
  authenticated mTLS connection against the SPIFFE URI recorded in the lease record for multi-tenant
  deployments, and runs the `PreLLMRequest` interceptor chain before any upstream call
  ([§4.9](04_system-components.md#49-credential-leasing-service),
  [§4.8](04_system-components.md#48-gateway-policy-engine)).
- **Timing.** The specification states no request deadline, retry count, or interval for the channel
  itself. Two gateway-side bounds sit on the hop: the `PreLLMRequest` and `PostLLMResponse` interceptor
  phases default to a 100ms timeout, overridable through the `timeout` field on the interceptor
  registration ([§4.8](04_system-components.md#48-gateway-policy-engine)), and the LLM Proxy circuit
  breaker's half-open cooldown defaults to 30s, configurable through
  `llmProxy.circuitBreaker.halfOpenInterval`
  ([§4.9](04_system-components.md#49-credential-leasing-service)).
- **Exclusivity.** The specification states no exclusivity constraint on the channel and names no guard
  that enforces one. The nearest constraint is per-pod rather than exclusive: the lease token is bound to
  the issuing pod's SPIFFE identity, and a request whose peer SPIFFE URI does not match the lease record is
  rejected with `LEASE_SPIFFE_MISMATCH` and emits the audit event `credential.lease_spiffe_mismatch`, which
  is a cross-pod replay control ([§4.9](04_system-components.md#49-credential-leasing-service)).
- **Degradation.** When the LLM Proxy circuit breaker is open, every new request is rejected immediately
  with `PROVIDER_UNAVAILABLE` rather than hanging, the adapter receives a `PROVIDER_UNAVAILABLE` event and
  relays it to the runtime as a tool-result error of the same code, streams established before the circuit
  opened continue to completion or upstream failure, and a single probe request is allowed at each
  half-open transition ([§4.9](04_system-components.md#49-credential-leasing-service)). When the lease
  expires or is revoked the proxy rejects requests before any upstream call, returning `CREDENTIAL_REVOKED`
  on a deny-list hit ([§4.9](04_system-components.md#49-credential-leasing-service)). A `PreLLMRequest`
  interceptor returning `REJECT` yields `LLM_REQUEST_REJECTED` to the pod and a `PostLLMResponse` rejection
  yields `LLM_RESPONSE_REJECTED` ([§4.8](04_system-components.md#48-gateway-policy-engine)). When the peer
  is absent because the pod's pool is not a proxy-mode pool, the supplemental egress policy does not select
  the pod and the default-deny policy drops the connection
  ([§13.2](13_security-model.md#132-network-isolation)). The specification does not state a retry,
  resumption, or buffering policy for a proxy response stream that fails mid-stream.

**`CH-OBJSTORE`**

- **Link.** `None` (§28.3).
- **Endpoint.** The configured object store. The supplemental `allow-pod-egress-objectstore` policy renders
  an in-cluster arm reaching the self-managed MinIO in the release namespace on `minio.tlsPort`, default
  9443, and a cloud-managed arm reaching an operator-enumerated CIDR list on TCP 443
  ([§13.2](13_security-model.md#132-network-isolation)). Object-store TLS is required: the object store's
  server certificate must be signed by a CA the agent pods trust, either the deployer's cluster-wide trust
  bundle or a deployer-supplied CA bundle projected into agent pods when `objectStorage.caBundle` is set,
  and the certificate's SAN must cover the configured object-store endpoint hostname
  ([§13.2](13_security-model.md#132-network-isolation)). The specification does not state a fixed hostname
  for the endpoint; it is the configured object-store endpoint of the deployment.
- **Axes.** State plane, dialled by the pod adapter, message authority with the pod adapter, HTTP transport
  (§28.3).
- **Messages.** On the capture path, one single-part `PUT` per chunk against a gateway-minted presigned
  capability that names one HTTP method, one object key under
  `/{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/`, and one exact `Content-Length`; the object key
  is `chunk_object_key_prefix/chunk-{n}.{chunk_encoding}`
  ([§13.2](13_security-model.md#132-network-isolation),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)). On the SigV4 backends the adapter replays the
  signed header values the gateway returns in `Grant.headers` verbatim, because the signed set names the
  headers rather than their values ([§13.2](13_security-model.md#132-network-isolation)). On the restore
  path, one single-key `GET` per chunk index in the contiguous prefix `[0, chunk_count)`, against
  capabilities the gateway mints and passes on the unary `Resume` call
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The pod holds no `LIST`, `DELETE`, or
  multipart capability, so it enumerates no key it was not handed, deletes nothing, and opens no multipart
  upload ([§13.2](13_security-model.md#132-network-isolation),
  [§4.4](04_system-components.md#44-event--checkpoint-store)).
- **Preconditions.** Every request is made against a capability the gateway has already minted:
  capture-path capabilities are minted on `CH-CHECKPOINT`, and restore-path capabilities are minted by the
  gateway and passed on the unary `Resume` call
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). On neither path does the pod hold an
  object-store credential ([§13.2](13_security-model.md#132-network-isolation)). On the capture path the gateway mints chunk-upload
  capabilities only after the intent-row INSERT commits; the adapter declares each chunk by index and exact
  byte length, and the gateway rejects a declared length outside `(0, chunk_size_bytes]` and aborts the
  attempt with `manifest_reason = 'stream_truncated'` before it signs anything. The gateway keeps at most
  `checkpointGrantWindow` grants outstanding, default 4, and refuses to sign a capability whose
  `Content-Length` would carry the attempt past its reservation plus remaining tenant headroom, aborting
  with `STORAGE_QUOTA_EXCEEDED` before signing
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling),
  [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)). The `ArtifactStore` validates the
  `tenant_id` prefix of every key it is asked to sign and mints no capability for a key outside the
  caller's authenticated tenant prefix ([§12.5](12_storage-architecture.md#125-artifact-store)). On the
  restore path the gateway lists the objects under the prefix and verifies contiguity of `[0, chunk_count)`
  before it mints a single `GET` capability per index
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The egress itself is permitted only for pools
  that write checkpoints, for which the supplemental `allow-pod-egress-objectstore` policy is rendered
  ([§13.2](13_security-model.md#132-network-isolation)).
- **Timing.** Every capability expires after `checkpointCapabilityTTLSeconds`, default 30
  ([§13.2](13_security-model.md#132-network-isolation),
  [§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)). Non-eviction uploads
  are retried with exponential backoff from 200ms at factor 2 for up to about 5 seconds in total, and a
  retry that outlives its grant's expiry requests a fresh grant for the same chunk index on the open
  `Checkpoint` stream, which the gateway re-signs at the same key and length
  ([§4.4](04_system-components.md#44-event--checkpoint-store)). The whole checkpoint the transfer serves is
  bounded by the 60-second timeout every checkpoint path enforces from the initial quiescence request to
  completion ([§4.4](04_system-components.md#44-event--checkpoint-store)). The specification states no
  per-request deadline for an individual `PUT` or `GET`.
- **Exclusivity.** Per capability rather than per connection. An upload capability names one method, one
  key, and one exact `Content-Length`; a restore capability names one method and one key. The tenant
  prefix, the method, and the key are bound into the
  signature on every backend, so a request that alters any of them is rejected by the object store before a
  byte is written or read ([§13.2](13_security-model.md#132-network-isolation)). At most
  `checkpointGrantWindow` capabilities are outstanding for an attempt at a time
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Above the transfer, the adapter's pod-level
  operation lock serializes `Checkpoint` across the pod's slots
  ([§4.7](04_system-components.md#47-runtime-adapter)). The specification states no exclusivity constraint
  on the connection itself and names no guard that enforces one.
- **Degradation.** When upload retries are exhausted on a non-eviction checkpoint, the adapter resumes the
  agent immediately, reports the retry-exhausted failure on the `Checkpoint` stream, and the gateway
  increments `lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}`; the failed checkpoint is
  discarded and the next scheduled checkpoint retries normally
  ([§4.4](04_system-components.md#44-event--checkpoint-store)). An attempt that ends before every declared
  byte is confirmed leaves a manifest row flagged `partial = true`, which is not a valid checkpoint; a
  deadline fire retains its chunks as a recovery aid the resume path reassembles, while a stream
  truncation, an adapter crash, a supersession, or a quota refusal leaves no resume candidate and the
  gateway sweeps the prefix by listing it, which also reclaims a chunk written by a grant still outstanding
  when the abort was observed ([§4.4](04_system-components.md#44-event--checkpoint-store)). When the object
  store is unreachable on the eviction path and its retries are exhausted, the gateway falls back to
  writing a minimal session-state record to Postgres and the `CheckpointStorageUnavailable` critical alert
  fires; when the Postgres write is also exhausted the gateway logs the committed object keys, the chunk
  encoding, and the error summary at `WARN`, so an operator can reconstruct the workspace by hand, before
  entering the total-loss path ([§4.4](04_system-components.md#44-event--checkpoint-store)). On the restore path, a contiguity failure,
  a fetch error on a non-final chunk, or a decode error away from the end of the stream aborts reassembly,
  discards the staging directory, and falls back to the last successful full checkpoint
  ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state a retry
  policy for a restore `GET` that fails mid-stream.

#### 28.5.6 Control-plane

This boundary carries the channels between a Lenny control-plane component and another control-plane
surface, where the conversation is about the cluster's own admission and lifecycle decisions rather than
about a session. The §28.3 channel register places one channel on this boundary, `CH-ADMISSION`, and it
carries `None` in its Link column, so its own register row together with the endpoint stated in its card
describes the connection. No agent pod participates on this boundary: the base egress policy
`allow-pod-egress-base` permits an agent pod the gateway gRPC port and DNS alone, so an agent pod reaches
neither the kube-apiserver nor the gateway's internal HTTP port
([§13.2](13_security-model.md#132-network-isolation)). The Kubernetes API entries of the §28.3
register-entry register, such as `REG-CLAIM`, are shared state rather than channels per §28.2, and their
writer sets are the gateway replicas and the WarmPoolController leader
([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), so they carry no card
here.

```
  kube-apiserver                admission webhook                 gateway replica
  +------------------+        +--------------------+        +--------------------+
  |                  |        |                    |        |                    |
  |  kube-apiserver  | =====> | lenny-drain-       | =====> |  gateway internal  |
  |                  |        | readiness          |        |  HTTP port         |
  +------------------+        +--------------------+        +--------------------+
        admission callback,        CH-ADMISSION, gateway internalPort, default TCP 8080,
        TCP 443                    GET /internal/drain-readiness

  CH-ADMISSION: dialled by the admission webhook, permitted by the drain-readiness
  sub-rule of the lenny-system admission-webhook egress policy.
```

**`CH-ADMISSION`**

- **Link.** `None` (§28.3).
- **Endpoint.** The gateway's internal HTTP port, `gateway.internalPort`, default TCP 8080, reached at the
  drain readiness endpoint `GET /internal/drain-readiness`
  ([§12.5](12_storage-architecture.md#125-artifact-store),
  [§13.2](13_security-model.md#132-network-isolation)). The specification names this hop as the gateway's
  internal HTTP port and states no transport protection for it; the TLS variant of the gateway's internal
  admin surface, `gateway.internalTLSPort`, is stated for the `lenny-ops` flows rather than for this
  callback ([§13.2](13_security-model.md#132-network-isolation)). The hop is permitted by a dedicated
  egress sub-rule of the `lenny-system` admission-webhook NetworkPolicy that selects on the canonical
  `lenny.dev/component: admission-webhook` label together with the additive
  `lenny.dev/webhook-name: drain-readiness` label, which the `lenny-drain-readiness` Deployment alone
  carries, so the sub-rule confines this reachability to that Deployment and no in-process validator
  inherits it
  ([§13.2](13_security-model.md#132-network-isolation),
  [§17.2](17_deployment-topology.md#172-namespace-layout)).
- **Axes.** Control plane, dialled by the admission webhook, message authority with the admission webhook,
  HTTP transport (§28.3).
- **Messages.** One `GET /internal/drain-readiness` request per webhook invocation, answered by
  `HTTP 200 {"status": "ready", "minio": "healthy"}` when the drain may proceed and by
  `HTTP 503 {"status": "not_ready", "minio": "unhealthy", "reason": "<error>"}` when it must be deferred
  ([§12.5](12_storage-architecture.md#125-artifact-store)). The webhook's admission verdict is not carried
  on this channel: the rejection travels back to the kube-apiserver on the admission callback that invoked
  the webhook, with the message `"Node drain blocked: MinIO health check failed — defer drain until MinIO
  is healthy (STR-006)"` ([§12.5](12_storage-architecture.md#125-artifact-store)).
- **Preconditions.** The `lenny-drain-readiness` `ValidatingAdmissionWebhook` is rendered only when the
  chart's `features.drainReadiness` flag is `true`, which defaults to `false`, and the feature is first
  deployed in the checkpoint and resume phase of the build sequence
  ([§17.2](17_deployment-topology.md#172-namespace-layout),
  [§18.22](18_build-sequence.md#1822-phase-8--checkpointresume-drain-readiness-webhook)). The channel opens when the webhook fires,
  which is on a `CREATE` operation against the `pods/eviction` resource in the agent namespaces, reaching
  the webhook pods from the kube-apiserver on TCP 443 within `webhookIngressCIDR`
  ([§12.5](12_storage-architecture.md#125-artifact-store),
  [§13.2](13_security-model.md#132-network-isolation)). The check covers evictions that pass through the
  Kubernetes eviction API, including operator-initiated drains and cluster-autoscaler scale-downs; a
  spontaneous node failure, an OOM kill, or a preemption bypasses the eviction API and opens no channel
  ([§12.5](12_storage-architecture.md#125-artifact-store)). An operator can bypass the check for an
  emergency drain by annotating the node with `lenny.dev/drain-force: "true"` and providing justification,
  in which case the webhook permits the drain and emits the `node.drain.forced` critical audit event
  ([§12.5](12_storage-architecture.md#125-artifact-store)).
- **Timing.** The MinIO liveness probe the endpoint performs, an S3 `HeadBucket` against the checkpoints
  bucket, carries a 2 second timeout ([§12.5](12_storage-architecture.md#125-artifact-store)). The
  specification states no request deadline, retry count, or interval for the callback itself. The webhook
  invocation that triggers it is bounded by the admission webhook timeout, default 5 seconds and tunable
  via `admissionWebhook.timeoutSeconds`
  ([§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)).
- **Exclusivity.** The specification states no exclusivity constraint on the channel and names no guard
  that enforces one. The nearest constraint runs the other way: the webhook Deployment carries the uniform
  admission-plane high-availability contract of two replicas with a pod disruption budget of one available
  pod, so more than one webhook pod may hold the channel at the same time
  ([§17.2](17_deployment-topology.md#172-namespace-layout)).
- **Degradation.** The webhook rejects the eviction when the endpoint answers `503` and when the endpoint
  is unreachable, so both outcomes block the drain. Separately, the webhook is deployed with
  `failurePolicy: Fail`, so webhook unavailability blocks the drain as well, which is stated as the
  deliberate posture against silent data loss
  ([§12.5](12_storage-architecture.md#125-artifact-store)). Outcomes are counted by
  `lenny_drain_readiness_checks_total`, labeled by `outcome` with the values `allowed`, `blocked`, and
  `forced` ([§12.5](12_storage-architecture.md#125-artifact-store)). A webhook unreachable for more than
  five minutes raises the `DrainReadinessWebhookUnavailable` warning alert, whose stated consequence is
  that node drains and rolling updates may stall
  ([§16.5](16_observability.md#165-alerting-rules-and-slos)). When the feature flag is turned off after the
  webhook has been deployed, the channel disappears together with the webhook and its per-webhook alert;
  that drift is the case the `AdmissionPlaneFeatureFlagDowngrade` warning alert covers, and the flag
  downgrade is prohibited and enforced at the chart, preflight, and runtime layers
  ([§16.5](16_observability.md#165-alerting-rules-and-slos),
  [§17.2](17_deployment-topology.md#172-namespace-layout)). The specification does not state a retry,
  buffering, or resumption policy for a callback whose transport fails mid-response.

#### 28.5.7 Gateway-to-store

This boundary carries the channels a gateway replica opens to a datastore that mediates state between
gateway replicas. The §28.3 channel register places one channel on this boundary, `CH-EVENTRELAY`, and it
carries `None` in its Link column, so its own register row together with the endpoint stated in its card
describes the connection. The stores on this boundary divide by role: Postgres carries the authoritative
session and event state through the `SessionStore` and `EventStore` roles, while Redis data is treated as
ephemeral, is not a system of record, and carries a durable fallback or reconstruction path for every role
placed on it, so a total loss of Redis data is recoverable
([§12.2](12_storage-architecture.md#122-storage-roles),
[§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The entries of the §28.3
register-entry register, which are `REG-COORDLEASE`, `REG-COORDMIRROR`, `REG-SLOTCOUNT`, `REG-PODSTATE`,
and `REG-CLAIM`, are shared state mediating two participants with no live connection rather than channels
per §28.2, so they carry no card here. No agent pod participates on this boundary: the base egress policy
`allow-pod-egress-base` is an allowlist carrying the gateway gRPC port and DNS alone, so an agent pod
reaches neither Postgres nor Redis ([§13.2](13_security-model.md#132-network-isolation)).

```
  gateway replica                              gateway replica
  +----------------------+                     +----------------------+
  |  in-memory           |                     |  replica serving a   |
  |  session-event bus   |                     |  reconnected client  |
  +----------------------+                     +----------------------+
             |                                            ^
             |  CH-EVENTRELAY, XADD                       |  XRANGE, XREAD BLOCK
             v                                            |
  +-----------------------------------------------------------------------+
  |          Redis stream lenny:events:{session_id}                       |
  +-----------------------------------------------------------------------+

  CH-EVENTRELAY: Redis, dialled by the gateway, on a stream per session, wired
  only when Redis is present.
```

**`CH-EVENTRELAY`**

- **Link.** `None` (§28.3).
- **Endpoint.** The Redis Streams key `lenny:events:{session_id}`, one stream per session
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The key is session-scoped and
  carries no tenant prefix; the `lenny:` prefix scopes the stream to the gateway namespace, and the Redis
  wrapper layer passes the key through unvalidated because it leads with no prefix the wrapper recognizes,
  so its isolation rests on the session-scoped stream key rather than on the leading-prefix rule
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The gateway reaches Redis on the
  TLS listener port `redis.tlsPort`, default 6380, which the gateway egress rule permits
  ([§13.2](13_security-model.md#132-network-isolation)); Redis must run with `tls-port` set to that
  listener port and the plaintext `port` set to 0
  ([§10.3](10_gateway-internals.md#103-mtls-pki)). Redis AUTH through ACLs and TLS
  are required on the connection ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)), and
  Redis must be configured with `tls-auth-clients yes`, so the connection carries a client certificate
  ([§10.3](10_gateway-internals.md#103-mtls-pki)). The specification does not state a fixed hostname for
  the Redis service; it is the configured Redis endpoint of the deployment.
- **Axes.** State plane, dialled by the gateway, message authority with the gateway, Redis transport
  (§28.3).
- **Messages.** The session events a gateway replica's in-memory session-event bus fans out, appended with
  `XADD`. A reader consumes the stream with `XRANGE` for history and `XREAD BLOCK` for live delivery
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The events carried are the
  `SessionEvent` frames the gateway dispatches on a session's event stream, each carrying the monotonic
  `SeqNum` the gateway assigns at dispatch time
  ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
  [§15.2](15_external-api-surface.md#152-mcp-api)). The specification does not state the field layout of a
  stream entry, and it states no trim, `MAXLEN`, or retention bound for the stream.
- **Preconditions.** The channel is wired only when Redis is present in the deployment. Single-replica dev
  mode keeps the session-event bus in memory and writes no stream
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). A reader opens the stream when a
  client reconnects with `Last-Event-ID`, which the SSE transport carries as the implicit equivalent of
  `resumeFromSeq`, to a replica other than the one that produced the earlier events, per the §15.2
  event-stream resume contract ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes),
  [§15.2](15_external-api-surface.md#152-mcp-api)).
- **Timing.** The live read is a blocking `XREAD BLOCK`
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The specification states no block
  timeout, request deadline, retry count, or publication interval for the channel.
- **Exclusivity.** The specification states no exclusivity constraint on the channel and names no guard
  that enforces one. Every gateway replica serving an attached client for the session may read the stream
  at the same time, which is the reconnection case the stream exists for
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). The session-coordination lease
  `REG-COORDLEASE`, which admits one holder per tenant and session, is a register entry governing session
  coordination rather than a guard on this channel (§28.3).
- **Degradation.** The failure-behavior table of
  [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) carries no row for this stream, so the
  specification states no per-use-case fallback for it; the general Redis posture that section states
  applies, under which Redis is not a system of record and the authoritative record of session events is
  the Postgres `EventStore` ([§12.2](12_storage-architecture.md#122-storage-roles),
  [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). A resume request is served from the
  per-session event replay buffer the coordinating replica holds in process, sized by
  `gateway.sessionEventReplayBufferDepth`, and a request for a sequence that buffer has evicted yields a
  single protocol-level `gap_detected` frame carrying `{"lastSeenSeq": N, "nextSeq": M}` before live
  delivery resumes ([§10.4](10_gateway-internals.md#104-gateway-reliability),
  [§15.2](15_external-api-surface.md#152-mcp-api)). The specification does not state what a reader does
  when an entry it requests is absent from this stream, and it states no buffering or resumption policy for
  an `XADD` or a blocking read whose transport fails mid-stream. When both Postgres and Redis are
  unavailable the platform enters dual-store degraded mode: new sessions are rejected with `503`, in-flight
  sessions continue on cached coordination state, a `PLATFORM_DEGRADED` event is emitted to clients, and
  the `DualStoreUnavailable` alert fires
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes),
  [§10.1](10_gateway-internals.md#101-horizontal-scaling)).

### 28.6 Exclusivity and concurrency model

Each contract card in §28.5 states its channel's exclusivity constraint and the guard that enforces it, or
records that the specification states neither. This subsection states the pattern those fields form across
the channel set: which channels admit one holder at a time, what the unit of the constraint is on each such
channel, and what the specification states happens to a second opener. It states no constraint the cards do
not carry, and where a card records that the specification names no exclusivity guard for a channel, that is
the position here as well.

**One holder per session.** The gateway-to-pod channels `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, and
`CH-BARRIER` admit one holder at a time, and the unit is the session (§28.5.1). The guard is the
session-coordination lease `REG-COORDLEASE`, which admits one holder per tenant and session (§28.3),
together with the `coordination_generation` stamp the pod validates on every gateway-to-pod RPC
([§10.1](10_gateway-internals.md#101-horizontal-scaling)). `CH-FENCE` establishes the constraint: a replica
that has just acquired coordination must receive a successful fence acknowledgement before it sends any
operational RPC, and from the fence onward the pod rejects every RPC carrying an older generation
([§10.1](10_gateway-internals.md#101-horizontal-scaling),
[§4.7](04_system-components.md#47-runtime-adapter)).

**The second opener on those channels.** A replica that opens one of those four channels without holding the
current generation is rejected on the generation stamp, and it cancels every in-flight RPC for the session
without retrying and discards its cached in-memory streams. While the adapter is in hold state, every
inbound RPC other than `CoordinatorFence` is rejected with `UNAVAILABLE` and a `coordinator_hold` error
detail, so `CH-FENCE` is the one channel a second opener can use. A replica becomes the holder by acquiring
`REG-COORDLEASE` and winning the generation compare-and-swap, and the fence acknowledgement closes the
window in which the prior coordinator's RPCs are still accepted (§28.5.1,
[§10.1](10_gateway-internals.md#101-horizontal-scaling)). The constraint excludes a second replica. The
specification states no limit on how many `CH-ATTACH` streams one holding replica opens at the same time;
the `Checkpoint` operation `CH-CHECKPOINT` carries is serialized against the `Interrupt` operation the
specification states outside the channel register by the adapter's pod-level operation lock stated below
([§4.7](04_system-components.md#47-runtime-adapter)).

**One operation per pod.** Below the per-session constraint sits one per-pod constraint. The adapter's
operation lock serializes `Checkpoint` and `Interrupt` across the pod's slots, admits one pending checkpoint
per distinct `slotId`, coalesces a checkpoint whose `slotId` is already pending, and on a single-session pod
holds at most one queued operation ([§4.7](04_system-components.md#47-runtime-adapter)). Its unit is the
pod, and admission to its queue is counted per slot. It is the one guard that spans boundaries: it bounds
`CH-CHECKPOINT` on the gateway-to-pod boundary (§28.5.1), the `checkpoint_request` and `interrupt_request`
frames of `CH-RUNTIMEOPS` on the intra-pod boundary (§28.5.3), and the transfer `CH-OBJSTORE` carries on the
pod-egress boundary (§28.5.5). A checkpoint whose `slotId` is already pending coalesces, while an interrupt
arriving while another interrupt is already queued, and any checkpoint or interrupt arriving while an
interrupt is pending on a concurrent-session pod, is dropped with a `BUSY` status
([§4.7](04_system-components.md#47-runtime-adapter)). The gateway retries a dropped interrupt with backoff
([§4.7](04_system-components.md#47-runtime-adapter)). The specification states no retry rule for a
checkpoint dropped with `BUSY` on a concurrent-session pod. The specification states no pod-level barrier lock
beyond this operation lock (§28.5.1).

**Channels the specification states no constraint on.** For `CH-PODHEALTH` (§28.5.1), `CH-ADAPTEREVENTS`
(§28.5.2), `CH-MSGSOCK`, `CH-RUNTIMEOPS`, `CH-MCP-PLATFORM`, and `CH-MCP-CONNECTOR` (§28.5.3),
`CH-LLMPROXY` and the connection `CH-OBJSTORE` runs on (§28.5.5), `CH-ADMISSION` (§28.5.6), and
`CH-EVENTRELAY` (§28.5.7), the specification states no exclusivity constraint on the channel and names no
guard that enforces one. It therefore states no rejection or precedence rule for an additional holder on any
of them, and this subsection supplies none.

Two of those cards record a constraint that runs the other way, so more than one holder is possible.
`CH-EVENTRELAY` admits every gateway replica serving an attached client for the session as a concurrent
reader, which is the reconnection case the stream exists for (§28.5.7,
[§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). `CH-ADMISSION` is served by a webhook
Deployment carrying the uniform admission-plane high-availability contract of two replicas with a pod
disruption budget of one available pod, so more than one webhook pod may hold the channel at the same time
(§28.5.6, [§17.2](17_deployment-topology.md#172-namespace-layout)).

Others in that set carry a scoping constraint that is not exclusivity. `CH-MSGSOCK` multiplexes every
session's stream over the one channel, keyed by `sessionId`, on every pod whatever its pool's
`maxConcurrentSessions` (§28.5.3). On `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` the manifest nonce
authenticates a connection to the pod's intra-pod MCP servers, which are pod-wide and started at most once
per pod, so the nonce a server validates against is the one the manifest carried at the start that bound
that server and a later session's manifest write does not re-arm it; the adapter resolves the calling
session to the single session the pod's shared runtime process has been given and refuses the call unless
that process has been given exactly one session and that session is the caller
([§4.7](04_system-components.md#47-runtime-adapter)). `CH-LLMPROXY` binds its lease token to the issuing
pod's SPIFFE identity and rejects a request whose peer SPIFFE URI does not match the lease record with
`LEASE_SPIFFE_MISMATCH`, which is a cross-pod replay control
([§4.9](04_system-components.md#49-credential-leasing-service)). `CH-OBJSTORE` is constrained per
capability, where a capability names one method and one key, an upload capability additionally names one
exact `Content-Length`, and at most `checkpointGrantWindow` capabilities are outstanding for an attempt at a
time (§28.5.5). Scoping by session, slot, pod, or capability restricts what a connection may carry rather
than how many holders it admits.

`CH-ADAPTEREVENTS` addresses its events to the session's coordinating replica, which is the holder of
`REG-COORDLEASE`, while the `LNK-POD-GRPC` register row states one connection per gateway replica per pod,
so the specification does not state which replica's connection carries an event when more than one replica
holds a connection to the pod (§28.5.2, §28.3).

**Units carried outside the channel register.** The exclusion primitives themselves are register entries and
link lifetimes rather than channels. `REG-COORDLEASE` admits one holder per tenant and session,
`REG-SLOTCOUNT` is an atomic per-pod counter that ceilings concurrent slots, and `REG-CLAIM` is a
cluster-wide per-pod acquisition on first claim, while `REG-COORDMIRROR` is a projection rather than an
exclusion primitive (§28.3). At the link level, `LNK-POD-GRPC` carries one connection per gateway replica
per pod and `LNK-GWCONTROL` one connection per pod process to one replica, so the per-replica unit is a
property of the connection rather than of a channel carried on it (§28.3).

### 28.7 Wire-contract artifact register

This register carries one row per published wire-contract artifact under `schemas/`, naming the artifact,
the channel or channels whose contract it carries, and what consumes it. It is derived from the artifact
set that directory holds rather than hand-enumerated, so it is the complete set. A reader takes the
artifact set from this register rather than from any shorter list, and an enumeration elsewhere that
stands for the same set and names fewer artifacts is superseded by it. An enumeration scoped to the
artifacts one named consumer asserts against states a subset deliberately and is not one of those
enumerations. An enumeration that names the artifacts the enumerating section's own prose documents, and
an enumeration that names what a build phase delivers, likewise state a subset deliberately and are not
superseded.

The directory also holds files that carry no wire contract, which are its own README, the example
fixtures the artifacts are exercised against, the code-generation configuration, and the embedding that
makes the artifacts available to the build. Those files carry no row.

Every claim a row makes derives from a specification section or from the artifact itself, and the row
cites the source. Where the specification names no consumer for an artifact, the row records that rather
than supplying one. A Channels cell reading `None` means the artifact carries a contract on a surface the
§28.3 channel register holds no entry for, and the cell names that surface.

| Artifact | Channels whose contract it carries | Consumers |
|:--|:--|:--|
| `schemas/lenny-adapter.proto` | `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, and `CH-PODHEALTH` on `LNK-POD-GRPC` (§28.5.1), `CH-ADAPTEREVENTS` on the same connection (§28.5.2), and the `GatewayControl` service over which the `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` tool calls are forwarded on `LNK-GWCONTROL` (§28.3, §28.5.3). The specification states the artifact as the protobuf service and message definitions for the gateway-to-adapter gRPC surface ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) | Runtime authors implementing the adapter contract ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)), and the external-adapter compliance suite, whose assertions are generated from the published artifacts ([§24.8](24_lenny-ctl-command-reference.md#248-external-adapter-management)) |
| `schemas/lenny-adapter-jsonl.schema.json` | `CH-MSGSOCK` (§28.5.3). The specification states the artifact as the JSON Schema for the adapter and runtime stdin/stdout frames in both directions and attributes the `CH-RUNTIMEOPS` frames to `schemas/runtime-ops-events.schema.json` instead ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) | Runtime authors ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)), the external-adapter compliance suite ([§24.8](24_lenny-ctl-command-reference.md#248-external-adapter-management)), and the conformance test suite, whose Basic-level round-trip category validates a `response` against it ([§15.4.6](15_external-api-surface.md#1546-conformance-test-suite)) |
| `schemas/messagepart.schema.json` | The `MessagePart` envelope carried in the `input` and `output` arrays of the `CH-MSGSOCK` frames (§28.5.3). The specification states the same envelope for the client-facing surfaces the §28.2 boundary set does not cover ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) | Runtime authors ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)), the external-adapter compliance suite ([§24.8](24_lenny-ctl-command-reference.md#248-external-adapter-management)), and the conformance test suite, whose Basic-level `MessagePart` category validates every part the runtime produces against it ([§15.4.6](15_external-api-surface.md#1546-conformance-test-suite)) |
| `schemas/runtime-ops-events.schema.json` | `CH-RUNTIMEOPS` (§28.5.3, [§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) | The runtime adapter and a Full-level runtime, which exchange the frames it schematizes ([§4.7](04_system-components.md#47-runtime-adapter), [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). Whether the schema-driven external-adapter compliance suite asserts against it is stated with that suite ([§24.8](24_lenny-ctl-command-reference.md#248-external-adapter-management)) |
| `schemas/lenny-interceptor.proto` | `None`. The artifact carries the gateway-to-external-interceptor gRPC contract, a surface the §28.3 channel register holds no entry for. The specification states that an external interceptor is invoked over gRPC with the gateway as the client ([§4.8](04_system-components.md#48-gateway-policy-engine)) and that, when the parent lease's `contentPolicy.scanExportedFiles` is `true`, the `PreExportMaterialization` phase runs that contract once per delegation-exported file ([§8.7](08_recursive-delegation.md#87-file-export-model)). The specification does not name the artifact, so this row derives from the artifact | A deployer-supplied `RequestInterceptor` service, which implements the contract, and the gateway, which invokes the service at each phase the interceptor is registered for, with the per-phase call cardinality stated by the section that owns the phase ([§4.8](04_system-components.md#48-gateway-policy-engine)) |
| `schemas/lenny-tokenservice.proto` | `None`. The artifact carries the gateway-to-Token-Service gRPC contract over mTLS, a surface the §28.3 channel register holds no entry for ([§4.3](04_system-components.md#43-token-service), [§4.9](04_system-components.md#49-credential-leasing-service)). The specification does not name the artifact, so this row derives from the artifact | The gateway as the client and the Token Service as the server for the credential materialization, rotation, and revocation calls, and for the admin-time probe of the Token Service's own read access to a referenced Kubernetes Secret ([§4.3](04_system-components.md#43-token-service), [§4.9](04_system-components.md#49-credential-leasing-service)) |
| `schemas/workspaceplan-v1.json` | `None`. The artifact schematizes the inner `WorkspacePlan` sub-object of the session-creation request body on the client-facing REST surface, which is outside the boundary set §28.2 fixes ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning), [§15.1](15_external-api-surface.md#151-rest-api)) | Clients, which may reference it through the optional `$schema` keyword on their `workspacePlan` object to validate the plan sub-object locally ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning)) |
| `schemas/ocsf-mapping.yaml` | `None`. The artifact mirrors the mapping from the event-type catalog to OCSF classes and activities for the audit projection, which the §28.3 channel register holds no entry for ([§11.7](11_policy-and-controls.md#117-audit-logging)) | The specification states the file as the maintained mapping, committed alongside the specification and regenerated in continuous integration from the event-type catalog, and names no reader of the file at run time ([§11.7](11_policy-and-controls.md#117-audit-logging)) |
| `schemas/audit-events/v1.json` | `None`. The artifact is the per-version registry of the canonical hash-chained audit record, which the §28.3 channel register holds no entry for ([§11.7](11_policy-and-controls.md#117-audit-logging)) | External verifiers recomputing a tenant's hash chain, which use the schema version that was current when the hash was computed and read the version echoed on the wire to locate it ([§11.7](11_policy-and-controls.md#117-audit-logging)) |

### 28.8 Failure and degradation matrix

This matrix states, per channel, what the channel does when its peer is absent, what it does when its
transport fails mid-stream, what happens when the holder of its exclusivity constraint changes, and what an
operator observes in each case. It carries exactly one row per channel identifier in the §28.3 channel
register: no identifier is missing, and no row names an identifier the register does not carry.

Every cell derives from a specification section or from the channel's own §28.5 contract card, and the cell
cites the source. Where neither states a behaviour, the cell records that the specification does not state
it. The matrix supplies no plausible failure mode for a case no section states, because an operator acts on
what this matrix says and cannot afterwards distinguish a behaviour the specification fixes from one the
matrix invented.

Where a channel carries no exclusivity constraint, the change-of-holder column records that the
specification states neither a constraint nor a guard for the channel, which is the position §28.5 and §28.6
carry for it. A scoping constraint that restricts what a connection may carry rather than how many holders
it admits is named in that column as a scoping constraint (§28.6).

| Channel | Peer absent | Transport fails mid-stream | Holder of the exclusivity constraint changes | Operator observable |
|:--|:--|:--|:--|:--|
| `CH-ATTACH` | Loss of the coordinating replica is detected by the pod's gRPC transport within 15 seconds, after which the adapter enters hold state and rejects every inbound RPC other than `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` error detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1). The specification does not state whether the gateway redials `Attach` against the same pod before treating the pod as lost (§28.5.1) | A gRPC error on the stream while the coordinator is unchanged and the session is `running` transitions the session to `resume_pending` while retries remain. The session is re-attached on a replacement pod from its last checkpoint when a pod is allocated within `maxResumeWindowSeconds`, and reaches `awaiting_client_action` when that window elapses or the retries are exhausted ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§7.3](07_session-lifecycle.md#73-retry-and-resume)) | The constraint is one coordinating replica per session, guarded by `REG-COORDLEASE` and the `coordination_generation` stamp (§28.5.1, §28.3). A replica that receives a generation-stale rejection cancels all in-flight RPCs for the session without retrying and discards its cached in-memory streams, and the acquiring replica may not send this RPC until its `CH-FENCE` acknowledgement returns ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) | The session state transitions `resume_pending` and `awaiting_client_action` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§7.3](07_session-lifecycle.md#73-retry-and-resume)), and the `coordinator_hold` error detail on a rejected RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification names no metric or alert scoped to this channel |
| `CH-CHECKPOINT` | While the adapter is in hold state the RPC is rejected with `UNAVAILABLE` and a `coordinator_hold` error detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). On the eviction path the agent pod cannot open the stream itself and signals its coordinating replica on `CH-ADAPTEREVENTS`; when that replica is unreachable no replica drives the eviction checkpoint until the coordination lease lapses and a new holder has fenced the pod through the [§10.1](10_gateway-internals.md#101-horizontal-scaling) TTL-driven coordinator handoff, at which point the new holder drives it under its held lease ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), §28.5.1) | An attempt that ends before every declared byte is confirmed leaves a manifest row flagged `partial = true`, which is not a valid checkpoint. A deadline fire on a drain, preStop, or barrier path retains its chunks as a recovery aid the resume path reassembles, while a stream truncation, an adapter crash, a supersession, or a quota refusal leaves no resume candidate and the gateway sweeps the chunk prefix. When all upload retries fail on a non-eviction checkpoint the adapter resumes the agent immediately and the attempt is discarded ([§4.4](04_system-components.md#44-event--checkpoint-store)) | The constraint is one coordinating replica per session, guarded by `REG-COORDLEASE` and the generation stamp, above a pod-level operation lock that serializes `Checkpoint` and `Interrupt` across the pod's slots (§28.5.1, [§4.7](04_system-components.md#47-runtime-adapter), [§10.1](10_gateway-internals.md#101-horizontal-scaling)). A stream opened by a replica that no longer holds the current generation is rejected on the stamp, and the new holder must complete its fence before it opens one ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) | The manifest row flagged `partial = true`, and the `lenny_checkpoint_storage_failure_total` counter the gateway increments when all upload retries fail ([§4.4](04_system-components.md#44-event--checkpoint-store)) |
| `CH-FENCE` | On failure or timeout the new coordinator retries the same generation value up to 3 attempts with 1-second backoff, and when the retries are exhausted it relinquishes the lease and backs off with jittered delay from 2s to a 16s maximum before reconsidering coordination. When no successful fence arrives at the adapter within `coordinatorHoldTimeoutSeconds`, default 120s, the adapter begins graceful session termination with reason `coordinator_lost` ([§10.1](10_gateway-internals.md#101-horizontal-scaling), [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)) | The RPC carries a 5-second deadline that is hard-coded and not configurable, and a failure or a timeout takes the retry and relinquish path above ([§4.7](04_system-components.md#47-runtime-adapter), [§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification states no separate mid-stream recovery policy for this announcement beyond that deadline and its retries | This channel is what changes the holder. The acknowledgement closes the window in which the prior coordinator's RPCs are still accepted, and it is the one channel the adapter still accepts in hold state and the only exit from it. When the announced generation exceeds the last fenced generation by more than one, the adapter cancels and discards every in-flight RPC received after the last fenced generation, resets the transient state accumulated since it, logs a `coordinator_generation_gap` event, and acknowledges the fence normally ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1) | The `coordinator_generation_gap` event, and the graceful session termination carrying reason `coordinator_lost` at the hold-timeout expiry ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) |
| `CH-BARRIER` | While the adapter is in hold state the RPC is rejected with `UNAVAILABLE` and a `coordinator_hold` error detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The acknowledgement travels on `CH-ADAPTEREVENTS` rather than on this channel, so an adapter that never answers is bounded by the gateway-side `checkpointBarrierAckTimeoutSeconds` deadline, default 90s, taken as one wall-clock deadline across all the pods a draining replica coordinates ([§4.7](04_system-components.md#47-runtime-adapter), §28.5.2) | When the deadline fires before every declared byte of the barrier-driven checkpoint is confirmed and chunks were already committed for the session, the gateway finalises the partial manifest with `manifest_reason = "timeout"` and retains that row and its chunks as a recovery aid. When no chunks were committed, no intent row exists, or the read of that row fails, the gateway soft-deletes any such row and falls back to the session's last successful periodic checkpoint ([§4.4](04_system-components.md#44-event--checkpoint-store), [§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state what the adapter does with a held quiescence whose barrier is never followed by a `Checkpoint` stream (§28.5.1) | The constraint is one coordinating replica per session on the same `REG-COORDLEASE` lease and generation stamp as the other gateway-to-pod channels, and the barrier carries that generation in its own message, so a barrier from a superseded replica is rejected on the stamp ([§4.7](04_system-components.md#47-runtime-adapter), [§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1). The specification states no separate pod-level barrier lock beyond the operation lock (§28.5.1) | The partial checkpoint manifest carrying `manifest_reason = "timeout"` ([§4.4](04_system-components.md#44-event--checkpoint-store)), the `lenny_checkpoint_barrier_ack_total` counter and its `timeout` and `partial_captured` outcomes, the `lenny_checkpoint_barrier_ack_duration_seconds` histogram, and the `lenny_prestop_barrier_target_source_total` counter emitted once per preStop `CheckpointBarrier` fan-out ([§16.1](16_observability.md#161-metrics), [§10.1](10_gateway-internals.md#101-horizontal-scaling)) |
| `CH-PODHEALTH` | A pod whose health check has not passed is not marked `idle` and so is not offered to a session ([§4.7](04_system-components.md#47-runtime-adapter)) | Loss of the underlying connection is detected by the gRPC transport within 15 seconds and puts the adapter into hold state ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state what the gateway does with a pod whose health check fails after the pod has been marked `idle` (§28.5.1) | The specification states no exclusivity constraint on this channel and names no enforcing guard, so it states nothing about a change of holder (§28.5.1, §28.6) | The pod's `idle` marking withheld, so the pod is not offered to a session ([§4.7](04_system-components.md#47-runtime-adapter)). The specification states no probe interval, deadline, failure threshold, metric, or alert for this channel (§28.5.1) |
| `CH-ADAPTEREVENTS` | When the coordinating replica has itself crashed the adapter logs the delivery failure and writes the post-mortem to local disk, and the orphan session reconciler detects the terminated pod within one 60-second reconcile interval and forcibly transitions the session to `failed` with reason `orphan_pod_terminated`, a worst-case 60-second detection delay against a delivered `AdapterTerminating` ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) | A stream close is a terminal signal in its own right on the delegation path: the gateway waits for `FINAL_USAGE_REPORT` or for the stream to close, whichever comes first, and when neither occurs within the usage quiescence timeout it proceeds with the last known usage counter and emits a `delegation.budget_return_usage_lag` warning event ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)). Loss of the underlying connection is detected within 15 seconds and puts the adapter into hold state ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state a retry or buffering policy for an event other than `AdapterTerminating` whose delivery fails (§28.5.2) | The specification states no exclusivity constraint on the channel and names no guard that enforces one (§28.5.2, §28.6). The events are addressed to the session's coordinating replica, which is the holder of `REG-COORDLEASE`, and while the adapter is in hold state it originates no new operational messages until either a new coordinator fences it or `coordinatorHoldTimeoutSeconds` expires and it sends the final `AdapterTerminating` ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The `LNK-POD-GRPC` row states one connection per gateway replica per pod, so the specification does not state which replica's connection carries an event when more than one replica holds a connection to the pod (§28.3, §28.5.2) | The session forced to `failed` with reason `orphan_pod_terminated` ([§10.1](10_gateway-internals.md#101-horizontal-scaling)), and the `delegation.budget_return_usage_lag` warning event on the delegation path ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) |
| `CH-MSGSOCK` | When the agent process crashes the adapter detects the socket EOF, reports the failure to the gateway, and does not restart the agent; retry is handled by the gateway at the session level ([§4.7](04_system-components.md#47-runtime-adapter)). When the process exits non-zero without emitting a `response` the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The specification does not state a buffering or replay policy for a message the adapter holds while the runtime is absent (§28.5.3) | The socket EOF path above is the stated mid-stream failure of this channel ([§4.7](04_system-components.md#47-runtime-adapter)). A runtime that does not answer a `heartbeat` with `heartbeat_ack` within 10 seconds is treated as hung and is sent SIGTERM, and a runtime that leaves an outbound message in a buffer rather than flushing it never reaches the adapter and the session hangs silently ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) | The specification states no exclusivity constraint on this channel and names no enforcing guard, so it states nothing about a change of holder (§28.5.3, §28.6). The `sessionId` keying by which a pod multiplexes every session's stream over the one channel, whatever its concurrency, is a scoping constraint (§28.5.3, §28.6) | The `RUNTIME_CRASH` error the adapter synthesizes, and a task mapped to `failed` with `TaskResult.error` populated from a `response` carrying an `error` field ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). The specification states no observable for the unflushed-message hang (§28.5.3) |
| `CH-RUNTIMEOPS` | When the runtime never opened the channel, interrupt degrades to SIGTERM-based termination and the deadline warning and the drain coordination signal are not delivered, at both Basic level and Standard level. At Standard level a checkpoint degrades to a best-effort snapshot without a runtime pause and credential rotation degrades to the checkpoint and restart path. At Basic level there is no checkpoint support, pod failure loses the in-flight context, and the gateway restarts the session from its last gateway-persisted state ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) | The specification does not state what the adapter does when the socket fails mid-session while the runtime process is still running (§28.5.3). It states the per-frame timeouts that bound a reply that does not arrive: a missing `interrupt_acknowledged` transitions the session to `suspended` anyway and returns an `INTERRUPT_TIMEOUT` status in the `Interrupt` RPC response; a missing `credentials_acknowledged` within 60 seconds falls back to the Standard-level rotation path of checkpoint, pod termination, replacement pod, `AssignCredentials`, and `Resume`; and a runtime that has sent `checkpoint_ready` with no `checkpoint_complete` within 60 seconds autonomously resumes normal operation ([§4.7](04_system-components.md#47-runtime-adapter), [§4.4](04_system-components.md#44-event--checkpoint-store)) | The specification states no exclusivity constraint on this channel and names no enforcing guard, so it states nothing about a change of holder (§28.5.3, §28.6). The adapter's pod-level operation lock bounds the operations the frames carry rather than the channel, so a `checkpoint_request` and an `interrupt_request` are not outstanding at the same time ([§4.7](04_system-components.md#47-runtime-adapter)) | The `credential_rotation_timeout` warning event and the `lenny_credential_rotation_timeout_total` counter, the `credential_rotation_inflight_wait_long` warning event on a gate wait beyond 60 seconds ([§4.7](04_system-components.md#47-runtime-adapter)), the `INTERRUPT_TIMEOUT` status on the `Interrupt` RPC response ([§4.7](04_system-components.md#47-runtime-adapter)), and the `checkpoint_timeout` warning the runtime logs ([§4.4](04_system-components.md#44-event--checkpoint-store)) |
| `CH-MCP-PLATFORM` | When the runtime is Basic-level and connects to no MCP server, delegation, discovery, elicitation, inter-session messaging, and blocking input requests are unavailable with no fallback, and the runtime produces all of its output on `CH-MSGSOCK` ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) | The specification does not state what the adapter does when the server fails to bind its socket or when the connection drops mid-session (§28.5.3). It states gateway-side bounds on individual tools the channel carries: `lenny/request_input` is bounded by `maxRequestInputWaitSeconds`, after which the gateway delivers a `REQUEST_INPUT_TIMEOUT` tool-call error, and `lenny/request_elicitation` is bounded by `maxElicitationWaitSeconds` with a 30-second timeout on each forwarding hop ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime), [§9.2](09_mcp-integration.md#92-elicitation-chain), [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)) | The specification states no exclusivity constraint on this channel and names no enforcing guard, so it states nothing about a change of holder (§28.5.3, §28.6). The manifest nonce that authenticates a connection to the pod's intra-pod MCP servers is a scoping constraint, and a connection that does not present a valid nonce is closed before any tool is dispatched. The servers are pod-wide and started at most once per pod, the nonce a server validates against is the one the manifest carried at the start that bound that server and a later session's manifest write does not re-arm it, and the adapter resolves the calling session to the single session the pod's shared runtime process has been given, refusing the call unless that process has been given exactly one session and that session is the caller ([§4.7](04_system-components.md#47-runtime-adapter), [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.6) | The `REQUEST_INPUT_TIMEOUT` tool-call error the gateway delivers on a blocking input request that is not answered ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)). The specification names no metric or alert scoped to this channel |
| `CH-MCP-CONNECTOR` | When the runtime is Basic-level and connects to no MCP server, no connector tool is reachable and there is no fallback ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) | The specification does not state what the adapter does when one connector server fails to bind its socket while the others bind, and it does not state what happens to the channel when a connector's authorization is revoked mid-session (§28.5.3). On the authorization path a tool call that requires user authorization is answered by an auth challenge the gateway turns into a URL-mode elicitation carried hop by hop up to the client, and the specification does not state what happens to the call that raised the challenge ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc), §28.5.3) | The specification states no exclusivity constraint on this channel and names no enforcing guard, so it states nothing about a change of holder (§28.5.3, §28.6). The same manifest nonce that authenticates a connection to `CH-MCP-PLATFORM` is a scoping constraint here, required on each connector server's connection separately, with each connector server pod-wide and started at most once per pod and the calling session resolved as it is there ([§4.7](04_system-components.md#47-runtime-adapter), §28.6) | The URL-mode elicitation that reaches the client on the authorization path ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)). The specification names no metric or alert scoped to this channel |
| `CH-LLMPROXY` | When the pod's pool is not a proxy-mode pool the supplemental egress policy does not select the pod and the default-deny policy drops the connection ([§13.2](13_security-model.md#132-network-isolation)) | The specification does not state a retry, resumption, or buffering policy for a proxy response stream that fails mid-stream (§28.5.5). When the LLM Proxy circuit breaker is open every new request is rejected immediately with `PROVIDER_UNAVAILABLE` rather than hanging, the adapter receives a `PROVIDER_UNAVAILABLE` event and relays it to the runtime as a tool-result error of the same code, streams established before the circuit opened continue to completion or upstream failure, and a single probe request is allowed at each half-open transition. When the lease expires or is revoked the proxy rejects requests before any upstream call ([§4.9](04_system-components.md#49-credential-leasing-service)) | The specification states no exclusivity constraint on the channel and names no guard that enforces one (§28.5.5, §28.6). The nearest constraint is a per-pod scoping constraint: the lease token is bound to the issuing pod's SPIFFE identity, and a request whose peer SPIFFE URI does not match the lease record is rejected with `LEASE_SPIFFE_MISMATCH` ([§4.9](04_system-components.md#49-credential-leasing-service), §28.6) | The `PROVIDER_UNAVAILABLE` and `CREDENTIAL_REVOKED` errors, the `credential.lease_spiffe_mismatch` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)), and the `LLM_REQUEST_REJECTED` and `LLM_RESPONSE_REJECTED` outcomes of an interceptor rejection ([§4.8](04_system-components.md#48-gateway-policy-engine)) |
| `CH-OBJSTORE` | When the object store is unreachable on the eviction path and its retries are exhausted, the gateway falls back to writing a minimal session-state record to Postgres. When the Postgres write is also exhausted the gateway logs the committed object keys, the chunk encoding, and the error summary at `WARN`, so an operator can reconstruct the workspace by hand, before entering the total-loss path ([§4.4](04_system-components.md#44-event--checkpoint-store)) | An attempt that ends before every declared byte is confirmed leaves a manifest row flagged `partial = true`; a deadline fire retains its chunks as a recovery aid the resume path reassembles, while a stream truncation, an adapter crash, a supersession, or a quota refusal leaves no resume candidate and the gateway sweeps the prefix by listing it. On the restore path a contiguity failure, a fetch error on a non-final chunk, or a decode error away from the end of the stream aborts reassembly, discards the staging directory, and falls back to the last successful full checkpoint ([§4.4](04_system-components.md#44-event--checkpoint-store), [§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification does not state a retry policy for a restore `GET` that fails mid-stream (§28.5.5) | The constraint is per capability rather than per connection: a capability names one method and one key, an upload capability additionally names one exact `Content-Length`, the tenant prefix, the method, and the key are bound into the signature, and at most `checkpointGrantWindow` capabilities are outstanding for an attempt at a time, so a request that alters any of them is rejected by the object store before a byte is written or read ([§13.2](13_security-model.md#132-network-isolation), [§10.1](10_gateway-internals.md#101-horizontal-scaling)). Above the transfer the adapter's pod-level operation lock serializes `Checkpoint` across the pod's slots ([§4.7](04_system-components.md#47-runtime-adapter)). The specification states no exclusivity constraint on the connection itself (§28.5.5) | The `lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}` counter and the `CheckpointStorageUnavailable` critical alert ([§4.4](04_system-components.md#44-event--checkpoint-store)), and the manifest row flagged `partial = true` ([§4.4](04_system-components.md#44-event--checkpoint-store)) |
| `CH-EVENTRELAY` | The channel is wired only when Redis is present in the deployment. Single-replica dev mode keeps the session-event bus in memory and writes no stream ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) | The specification states no buffering or resumption policy for an `XADD` or a blocking read whose transport fails mid-stream, and it does not state what a reader does when an entry it requests is absent from the stream (§28.5.7). The §12.4 failure-behavior table carries no row for this stream, so the general Redis posture applies, under which Redis is not a system of record and the authoritative record of session events is the Postgres `EventStore` ([§12.2](12_storage-architecture.md#122-storage-roles), [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). When both Postgres and Redis are unavailable the platform enters dual-store degraded mode: new sessions are rejected with `503` and in-flight sessions continue on cached coordination state ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes), [§10.1](10_gateway-internals.md#101-horizontal-scaling)) | The specification states no exclusivity constraint on the channel and names no guard that enforces one (§28.5.7, §28.6). The constraint runs the other way: every gateway replica serving an attached client for the session may read the stream at the same time, which is the reconnection case the stream exists for ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). `REG-COORDLEASE` governs session coordination rather than this channel (§28.3) | The `PLATFORM_DEGRADED` event emitted to clients and the `DualStoreUnavailable` alert in dual-store degraded mode ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes), [§10.1](10_gateway-internals.md#101-horizontal-scaling)), and the single protocol-level `gap_detected` frame carrying `{"lastSeenSeq": N, "nextSeq": M}` a resume request receives for a sequence the replay buffer has evicted ([§10.4](10_gateway-internals.md#104-gateway-reliability), [§15.2](15_external-api-surface.md#152-mcp-api)) |
| `CH-ADMISSION` | The webhook rejects the eviction when the endpoint is unreachable, so the drain is blocked. The webhook is deployed with `failurePolicy: Fail`, so unavailability of the webhook itself blocks the drain as well, which is stated as the deliberate posture against silent data loss ([§12.5](12_storage-architecture.md#125-artifact-store)) | The specification does not state a retry, buffering, or resumption policy for a callback whose transport fails mid-response (§28.5.6). The webhook invocation that carries it is bounded by the admission webhook timeout, default 5 seconds and tunable via `admissionWebhook.timeoutSeconds` ([§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)) | The specification states no exclusivity constraint on the channel and names no guard that enforces one (§28.5.6, §28.6). The constraint runs the other way: the webhook Deployment carries the uniform admission-plane high-availability contract of two replicas with a pod disruption budget of one available pod, so more than one webhook pod may hold the channel at the same time ([§17.2](17_deployment-topology.md#172-namespace-layout)) | The `lenny_drain_readiness_checks_total` counter, labeled by `outcome` with the values `allowed`, `blocked`, and `forced`, and the `node.drain.forced` critical audit event on an annotated emergency bypass ([§12.5](12_storage-architecture.md#125-artifact-store)). A webhook unreachable for more than five minutes raises the `DrainReadinessWebhookUnavailable` warning alert, whose stated consequence is that node drains and rolling updates may stall, and a feature-flag downgrade after deployment is covered by the `AdmissionPlaneFeatureFlagDowngrade` warning alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)) |

The register places no channel on the inter-replica boundary, so the matrix carries no row for it. The
connection `LNK-INTERREPLICA` is a link register entry rather than a channel, and §28.5.4 states what the
specification carries for it (§28.3, §28.5.4). A register entry in the §28.3 register-entry register is
likewise not a channel and carries no row here.
