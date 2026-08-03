# 28. Communication Channels

This section is the normative home for the communication channels between the gateway replicas, the agent
pod, the adapter container, the runtime container, and the control plane. §28.1 states the law that fixes
each channel's identifier. §28.2 states the classes an identifier is drawn from and the axes every channel
records. §28.3 carries the registers of links, channels, and register entries, together with the naming
table that records the spelling each carrier takes. §28.4 states the claim register, which records the
implementation status of every statement this section makes.

## 28.1 Naming law

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

## 28.2 Taxonomy and axes

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

## 28.3 Registers

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

### Link register

| Identifier | Participants | Dial direction | Transport | Endpoint | Lifetime | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `LNK-POD-GRPC` | Gateway replica and pod adapter | Gateway | gRPC | Pod IP, TCP 50051 | One connection per gateway replica per pod | C1 |
| `LNK-GWCONTROL` | Pod adapter and gateway replica | Pod adapter | gRPC | Gateway service ClusterIP, TCP 50051 | One connection per pod process to one replica | C7 |
| `LNK-INTERREPLICA` | Gateway replica and gateway replica | Forwarding replica | gRPC | The internal gRPC `ForwardMessage` RPC, whose address the specification does not state | One connection per forwarding replica to a session's coordinating replica | C19 |

`LNK-INTERREPLICA` carries no channel row, because the specification states the connection and the
cross-replica message routing it carries is not implemented. That status is recorded as an `ABSENT`
claim-register row per §28.4 rather than as an absent transport in this register.

### Channel register

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

### Register-entry register

| Identifier | Store | Key or table | Writer set | Reader set | Semantics | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `REG-COORDLEASE` | Redis | `t:<tenant>:lease:session:<session>` | Gateway replicas | Gateway replicas | One holder per tenant and session, on a compare-and-set with a 60 second expiry | C14 |
| `REG-COORDMIRROR` | Postgres | `coordination_lease` | Gateway sweeper | Gateway replicas | A single-valued row per tenant and session, and a projection rather than an exclusion primitive | C15 |
| `REG-SLOTCOUNT` | Redis | `lenny:pod:<pod>:active_slots` | Gateway replicas | Gateway replicas | An atomic per-pod counter that ceilings concurrent slots | C18 |
| `REG-PODSTATE` | Postgres | `agent_pod_state` | WarmPoolController for the mirrored `Sandbox` status columns, and gateway replicas for `sessions_served` and `scrub_failure_count` | Gateway replicas | One row per pod. A read-optimized mirror carrying the pod phase the orphan-session reconciler reads, together with the gateway-written reuse counters the recycle disposition evaluates ([§12.6](12_storage-architecture.md#126-interface-design), [§4.7](04_system-components.md#47-runtime-adapter)) | C21 |
| `REG-CLAIM` | Kubernetes API | `SandboxClaim` named `claim-<podName>` | Gateway replicas for the create, the status-subresource binding-state writes, and the hold-expiry delete, and the WarmPoolController leader for the deletes at pod termination and orphan garbage collection | Gateway replicas and controllers | Cluster-wide per-pod acquisition on first claim. The object carries no owner reference, so the controller's delete is the reclamation path for a claim its holder did not remove ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) | C17 |

### Naming table

The table records, per carrier, the spelling a channel whose identifier changed takes on that carrier. A
retired spelling standing in the row that retires it is the declaration of that spelling rather than a
reference to the channel.

| channel | carrier | retired spelling | canonical spelling |
|:--|:--|:--|:--|
| `CH-ADAPTEREVENTS` | proto-rpc | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | go-symbol | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | path | `controlchannel` | `adapterevents` |
| `CH-RUNTIMEOPS` | manifest-key | `lifecycleChannel` | `runtimeOps` |
| `CH-RUNTIMEOPS` | flag | `lifecycle-socket` | `runtime-ops-socket` |
| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| `CH-RUNTIMEOPS` | path | `lifecycle-events` | `runtime-ops-events` |

## 28.4 Claim register

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
