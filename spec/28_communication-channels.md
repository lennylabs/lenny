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
| `CH-CHECKPOINT` | `LNK-POD-GRPC` | `gateway-to-pod` | State | Gateway | Both | gRPC | Workspace capture and restore | C3 |
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
- **Endpoint.** §28.3 records the transport as Unix socket JSON Lines.
  [§15.4](15_external-api-surface.md#154-runtime-adapter-specification) states that the adapter
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
  adapter: `response`, `tool_call`, `heartbeat_ack`, `status`, and `set_tracing_context`
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). Each message is a single JSON
  object terminated by `\n`. A `message` carries an `input` field holding a `MessagePart` array, and a
  `response` carries its parts in an `output` array; `heartbeat` and `shutdown` use their own minimal
  schemas and are not `MessageEnvelope` instances
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). Every `tool_result.id` matches
  the `id` of a previously emitted `tool_call`, results may arrive in any order, and a `tool_result`
  whose `id` is unknown is dropped and logged as a protocol error
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)). On a pod whose pool sets
  `sessionPolicy.maxConcurrentSessions > 1`, `message`, `tool_result`, `response`, and `tool_call` carry
  a `slotId` assigned by the adapter, and on a pod that sets it to 1 no message carries `slotId`
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification),
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
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
  enforcing guard. A pod serving more than one concurrent session multiplexes every slot's stream over
  the one channel, keyed by `slotId`
  ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)).
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

**`CH-RUNTIMEOPS`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** The abstract Unix socket `@lenny-runtime-ops`, advertised in the adapter manifest as
  `runtimeOps.socket`. The runtime connects as a client and the adapter listens
  ([§4.7](04_system-components.md#47-runtime-adapter)). The protections stated for the socket are the
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
  discriminator, and the field set of each is stated with the message-schema table in
  [§4.7](04_system-components.md#47-runtime-adapter). An unknown message is silently ignored on both
  sides ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Preconditions.** The channel is optional and is opened by Full-level runtimes; a runtime that does
  not open it operates in fallback-only mode ([§4.7](04_system-components.md#47-runtime-adapter),
  [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The runtime reads
  `runtimeOps.socket` from the manifest the adapter writes before it spawns the runtime binary
  ([§4.7](04_system-components.md#47-runtime-adapter)). `lifecycle_capabilities` is the first message
  sent on channel open and the runtime replies with `lifecycle_support`, which is the handshake the
  gateway reads to select the credential-rotation strategy for the session
  ([§4.7](04_system-components.md#47-runtime-adapter)). Before it sends `credentials_rotated` the adapter
  rewrites `/run/lenny/credentials.json` and waits for the in-flight LLM request gate to clear
  ([§4.7](04_system-components.md#47-runtime-adapter)).
- **Timing.** `checkpoint_request`, `interrupt_request`, `terminate`, and `deadline_approaching` each
  carry a millisecond field that bounds the runtime's reply, its exit, or the remaining session time
  ([§4.7](04_system-components.md#47-runtime-adapter)). Every checkpoint path is bounded by a 60-second
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
  `Interrupt` RPC response; the session is not left in `running`
  ([§4.7](04_system-components.md#47-runtime-adapter)). When `credentials_acknowledged` does not arrive
  within 60 seconds, the adapter emits a `credential_rotation_timeout` warning event, increments
  `lenny_credential_rotation_timeout_total`, and falls back to the Standard-level rotation path of
  checkpoint, pod termination, replacement pod, `AssignCredentials`, and `Resume`
  ([§4.7](04_system-components.md#47-runtime-adapter)). When the runtime has not exited by the
  `terminate` frame's `deadlineMs` the adapter sends SIGTERM
  ([§4.7](04_system-components.md#47-runtime-adapter)). When the peer is absent, because the runtime
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
  a connection that does not present a valid nonce is closed immediately
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels),
  [§4.7](04_system-components.md#47-runtime-adapter)).
- **Axes.** Content plane, dialled by the runtime, message authority with the runtime, JSON-RPC transport
  (§28.3).
- **Messages.** MCP. The runtime calls `tools/list` to discover the server's tools and `tools/call` to
  invoke one ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)). The platform tool
  set is `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`,
  `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`,
  `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, and `lenny/set_tracing_context`
  ([§4.7](04_system-components.md#47-runtime-adapter),
  [§9.1](09_mcp-integration.md#91-where-mcp-is-used)). Lease extension is an internal gateway operation
  and is not exposed as a tool on this channel
  ([§4.7](04_system-components.md#47-runtime-adapter), [§8.6](08_recursive-delegation.md#86-lease-extension)).
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
  enforcing guard. It scopes the connection per session through the `mcpNonce` the adapter regenerates
  for each session and rewrites into the manifest before each session's runtime start
  ([§4.7](04_system-components.md#47-runtime-adapter)).
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
  exists, because aggregation is not lossless under the MCP specification
  ([§4.7](04_system-components.md#47-runtime-adapter)). The call is served by the gateway acting as the
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
  enforcing guard. It scopes the connection per session through the same per-session `mcpNonce` that
  scopes `CH-MCP-PLATFORM` ([§4.7](04_system-components.md#47-runtime-adapter)).
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
