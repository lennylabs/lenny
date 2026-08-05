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
