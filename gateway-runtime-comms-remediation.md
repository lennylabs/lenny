# Remediation plan: gateway, agent pod, adapter, and runtime communication

Companion to `gateway-runtime-comms.md` (2,710 lines), which is the source of truth for every factual
claim about the current implementation. This document plans the work; it makes no spec, code, or queue
change. Line and file citations were re-verified against the working tree on branch
`proposal-b/c-22-eviction-trigger` at the time of writing. Where a figure quoted in the planning inputs
did not reproduce, the measured value is used and the discrepancy is recorded in section 9.

---

## 1. Purpose and scope

### 1.1 What this plan fixes

Three problems, in the order the user set them.

**The naming collision.** Two unrelated mechanisms are both called a lifecycle channel: the
adapter-to-runtime Unix-socket JSON Lines channel (`pkg/adapter/lifecyclechannel.go:92`, constructor at
`:134`) and the gateway-to-adapter gRPC streaming RPC `Adapter/LifecycleChannel`
(`schemas/lenny-adapter.proto:227`, handler `pkg/adapter/controlchannel.go:108`). A third mechanism, the
runtime message socket (`pkg/adapter/socketruntime.go:60`), is routinely conflated with the first. The
collision is not confined to prose. It is baked into shipped artifacts:
`schemas/lenny-adapter-jsonl.schema.json:5` describes the intra-pod checkpoint and credential frames as
riding "the gRPC LifecycleChannel stream", which is wrong on both the transport and the participant, and
`spec/15_external-api-surface.md:1462-1463` points the runtime-frame messages at the wrong schema file.

**The knowledge cost.** The reference document was produced from independent per-surface and per-scenario
derivations, then adversarially verified with two skeptics per load-bearing claim followed by three
audits. Seventeen of twenty verified claims required correction. That work is currently recoverable only
by reading 2,710 lines of reference plus roughly 700 KB of research notes. None of it lives in `spec/`,
so the next agent or engineer who needs it pays the derivation cost again.

**The gaps.** Reference section 6 records unwired or dead paths (6a), specification-versus-implementation
divergences (6b), and missing capabilities (6c). Section 8 records what the reference could not
establish. The most consequential are structural rather than incidental:

- The adapter-to-gateway control direction is unbuilt. The server handler is wired
  (`pkg/adapter/transport.go:50`), no production gateway client exists, and `emitControlEvent` takes the
  nil-sink branch in every deployed pod (`pkg/adapter/controlchannel.go:170-176`). Every
  adapter-to-gateway control event is dropped.
- Coordinator-loss hold state is fully implemented and interceptor-wired
  (`pkg/adapter/transport.go:46-47`), and its only arming path is a `defer` inside the control-stream
  handler that no production client opens (`pkg/adapter/controlchannel.go:118-128`, arming at `:125`).
- Comments and specification prose assert a kubelet-path SIGTERM handler and a checkpointing preStop hook
  that do not exist (`spec/04_system-components.md:489`, rendered hook at
  `cmd/lenny-adapter/prestop.go:68-94`).

### 1.2 Scope boundary

In scope: the records in reference section 6 and the items in reference section 8. Nothing else. No gap
is invented here. Where a step touches an adjacent surface, it does so because a record cannot be closed
without it, and the reason is stated in the step.

Out of scope: filing clusters into `PROPOSAL-QUEUE.md`, editing `spec/`, and editing code. This plan
states what the clusters should be. Filing them is a later decision.

### 1.3 Status vocabulary

Carried unchanged from reference §1.2 (`gateway-runtime-comms.md:20-36`), because every downstream
register and gate depends on the distinction.

| Label | Meaning |
|:--|:--|
| `WIRED` | Implemented and reachable from production code. A production caller is named with a `file:line` citation. |
| `UNWIRED` | Implemented, no production caller. Only tests, test-only export seams, or demo binaries reach it. |
| `ABSENT` | Referenced by the specification, a proto comment, or a code comment, and not implemented. |
| `UNVERIFIED` | Could not be established from the source. Stated as such rather than guessed. |

A mechanism can be `WIRED` inside one binary and `UNWIRED` at the deployment boundary. The runtime
lifecycle channel is the clearest case: it has production callers inside the adapter process, and the
podspec never enables it. Compound labels naming both halves are used throughout.

### 1.4 The end state in the user's terms

A human asking "how does the gateway talk to the pod, and does it work" opens one specification section,
reads one table and one contract card, and has the answer. The channel identifiers in that table appear
verbatim in the proto, in the Go package names, in the metric labels, and in the test names. A machine
asking the same question reads one JSON register that a tier-0 test proves consistent with the code. No
part of the answer requires reading the 2,710-line reference, and the reference is frozen as a
point-in-time record rather than maintained as a second authority.

---

## 2. The end state

### 2.1 The specification

A new top-level `spec/28_communication-channels.md` is the single normative home for this surface. It
carries the naming law, the link register, the channel register, the shared-state register, the contract
cards grouped by participant edge, the exclusivity model, the wire-contract artifact register, and the
failure and degradation matrix. A new `spec/29_communication-scenarios.md` carries the end-to-end traces,
each written as a numbered step list naming channels by identifier.

`spec/03_high-level-architecture.md` keeps its diagram, with the line
`Gateway <--mTLS--> Pods (gRPC control protocol)` corrected (the podspec emits no TLS material on either
container, and there is more than one protocol on that edge) and a one-line pointer to §28.
`spec/15_external-api-surface.md` §15.4 is reduced to the wire-artifact pointer it already claims to be
at `spec/15:1456`, with its channel prose superseded by §28. Sections 4, 7, 10, and 13 keep their
subjects and link to §28 for the channel contract.

Two properties hold and are machine-checked. First, every channel is named by exactly one canonical
identifier, and that identifier resolves in `git grep` across `spec/`, `schemas/`, `pkg/`, `cmd/`,
`sdks/`, `charts/`, `docs/`, and `tests/`. Second, no specification sentence asserts a mechanism that no
production caller reaches without a claim-register row recording the gap and naming the step that closes
it.

### 2.2 The code

The adapter package separates the two colliding concerns into files whose names say which channel they
serve. The gateway has a control-stream consumer, so the adapter-to-gateway direction carries traffic.
`pkg/gateway/sessionserver/start.go` (4,472 lines) and `pkg/controller/sandbox/podspec/podspec.go`
(1,351 lines) are decomposed by concern so that concurrent work does not serialize on one file. Every
declared RPC has a production caller or a register row explaining why it does not. Every adapter flag has
a production setter or a register row.

### 2.3 The test suite

Four assertion classes exist that do not exist today, and each closes a class of defect the behavioral
tiers structurally cannot see:

1. **Production reachability.** A declared surface with no production caller fails tier 0. This class
   covers 6a.1, 6a.2, 6a.6, 6a.7, 6b.3, 6b.13, and the rows of 6a.5 the register enumerates as
   reachability defects, which is ten of its eighteen.
2. **Deployment boundary.** A flag or environment variable a component reads, and the rendered podspec
   never sets, fails tier 0. This covers the post-mortem half of 6a.2, 6a.3, 6b.12, the chart half of
   6c.7, the OTLP row of 6a.5, and the PodDisruptionBudget and admission rows of 6a.4.
3. **Normative claim register.** Every normative statement about this surface carries a register row with
   a status, and a `WIRED` row must cite a surface the reachability gate reports reachable. This covers
   the specification-ahead-of-code records in 6b (6b.1, 6b.2, 6b.5, 6b.6, 6b.7, 6b.10, and 6b.11) and
   every record in 6c.
4. **Off-holder behavior.** A two-replica harness exercises every session-scoped mutating route against a
   replica that does not hold the binding. This is what makes reference §5.2 a suite rather than a table.

Every gate lands green by enumerating today's violations into a named register with an owner and an
expiry, and never by widening the gate's scope or by suppression. The registers become the work queue.

---

## 3. Step 1: channel identification and naming (R1)

This is the user's mandated first step and the root of the plan. It runs before anything else because
every later step names a channel.

### 3.1 Why it must run first, and now

Proposal `proposals/0062_new_build-the-4-4-4-6-1-individual-agent-pod-eviction-checkpoint.md` is live on
this branch. Its status line reads "Applied to spec (2026-07-24); revised 2026-07-26 against the
co-located coordination model and awaiting re-verification before implementation resumes"
(`proposals/0062...:3`). It names `LifecycleChannel` seventeen times, and its `eventAdapterEvicting`
constant is already present at `pkg/adapter/controlchannel.go:47-57` with a `// spec: §4.7` citation, so
the specification half has landed and the code half is imminent. Its CODE-3 builds the same gateway-side
control-stream consumer this plan builds as R12.

Renaming today touches one superseded proposal document and no shipped gateway code. Renaming after 0062
implements is a rename
across a new gateway package, a receive loop, a reconnect path, a new inter-replica gRPC service, a new
flag, and their tier-1, 2, 3, 4, 5, 7a, 8, 9, and 11 tests.

**R0, a queue-only precondition.** Before R1 opens, 0062's status must be moved to superseded and its
content re-scoped into R12, R13, R16, and R18. The repository ships a `build-gaps-spec-unblock` loop whose
job is to drain `BUILD-GAPS.md` findings backed by an approved proposal. If that loop picks 0062 up while
R1 is in flight, the collision is head-on. R0 involves no code and no spec text. It is the one action
this plan cannot take itself, because it requires a `PROPOSAL-QUEUE.md` edit, so it is listed in section 9
as a decision a human must make before execution.

### 3.2 The taxonomy

The flat `C1`..`C22` list in the reference mixes three different kinds of thing, which is why its own
exclusivity table (`gateway-runtime-comms.md:2647-2672`) writes "not applicable" five times: `C1` and
`C7` are whole gRPC services while `C2`, `C3`, `C4`, `C5`, and `C6` are individual RPCs on `C1`, and
`C14`, `C15`, `C18`, and `C21` are shared state with no live counterparty at all. Separating them makes
every column mean one thing.

| Class | Prefix | What it is | Columns it carries |
|:--|:--|:--|:--|
| Link | `LNK-` | A transport connection between two participants | Participants, dial direction, transport, endpoint, authentication, and lifetime |
| Channel | `CH-` | A typed conversation carried on one link | Link, plane, authority direction, message vocabulary, exclusivity guarantee, and enforcing guard |
| Register | `REG-` | Shared state mediating two participants with no live connection | Store, key or table, writer set, reader set, semantics, and exclusivity |

Five axes are recorded per channel. The split of axis 1 is the correction the reference's own naming trap
makes necessary.

| Axis | Values | Why it is separate |
|:--|:--|:--|
| 1a. Dial direction | Which participant opens the connection | The gateway dials `Adapter/LifecycleChannel` |
| 1b. Authority direction | Which participant originates the messages | The adapter produces every event on that same stream |
| 2. Plane | Control, content, or state | Distinguishes `CH-ATTACH` from `CH-EVENTSTREAM` |
| 3. Transport | gRPC, Unix socket JSONL, JSON-RPC, HTTP, SQL, Redis, or Kubernetes API | Closed set; a new value requires a specification change |
| 4. Boundary | `intra-pod`, `gateway-to-pod`, `pod-to-gateway`, `pod-egress`, `gateway-to-store`, `inter-replica`, or `control-plane` | Groups the contract cards. The same closed set names the §28.5 card groups and the Boundary column in §3.4, so a channel's boundary value and its card subsection are the same string. |
| 5. Exclusivity | Granularity plus the enforcing guard, or the missing guard named | Reference §1.5 vocabulary, made a required field |

The proto documents the axis 1a/1b inversion correctly for `GatewayControl`
(`schemas/lenny-adapter.proto:230-234`) and does not document it at all for `LifecycleChannel`. Making
the split a column means the next channel with the same inversion cannot be mis-described.

### 3.3 Naming law

Seven rules, normative in §28.1.

- **N1.** A channel's canonical name states the endpoint pair and the plane, in that order. It never
  states the transport, because the transport is a column.
- **N2.** Identifiers are mnemonic, uppercase, and hyphenated: `CH-EVENTSTREAM`, `LNK-POD-GRPC`,
  `REG-COORDLEASE`. Positional identifiers are not used, because a channel added between two others must
  not renumber its neighbours, and because an engineer says the identifier out loud.
- **N3.** The words `lifecycle` and `control` are reserved. Neither may appear as a bare noun phrase
  ("the lifecycle channel", "the control channel") anywhere in `spec/`, `docs/`, `schemas/`, or a Go doc
  comment. Both may appear as part of a canonical identifier.
- **N4.** One identifier per channel, everywhere. The identifier is the Go package or file name stem, the
  proto RPC name stem, the metric label value, and the test name fragment for a test scoped to one
  channel. A gate or a test that spans channels is named for the invariant it enforces and carries no
  channel identifier.
- **N5.** A link identifier and the channel identifiers it carries share no stem, so a grep for one never
  returns the other.
- **N6.** A register (`REG-`) names the store and the key, and never a verb.
- **N7.** A flag, an environment variable, or a manifest key that names a channel uses that channel's
  identifier in lowercase kebab or snake form.

`.claude/rules/channel-naming.md` states N1 through N7 for future agents, so a conforming name is the
default rather than a lint finding after the fact.

### 3.4 The naming table

Every channel, its name today, its name after R1, and every surface where the name changes. `LNK` rows
are new, because the reference has no link concept, so their "name today" is the service or socket they
were folded into.

| Ref | Canonical id after R1 | Name today | Class | Boundary | Status today | Where the name changes |
|:--|:--|:--|:--|:--|:--|:--|
| C1 | `LNK-POD-GRPC` | `Adapter` gRPC service | Link | gateway-to-pod | WIRED | `spec/28` and doc prose. The proto service name `Adapter` is kept. |
| C2 | `CH-ATTACH` | `Adapter/Attach` content stream | Channel | gateway-to-pod | WIRED | `spec/28`. No code change. |
| C3 | `CH-CHECKPOINT` | `Adapter/Checkpoint` stream | Channel | gateway-to-pod | WIRED | `spec/28`. No code change. |
| C4 | `CH-FENCE` | `Adapter/CoordinatorFence` | Channel | gateway-to-pod | WIRED | `spec/28`. No code change. |
| C5 | `CH-BARRIER` | `Adapter/CheckpointBarrier` | Channel | gateway-to-pod | WIRED | `spec/28`. Proto comment at `schemas/lenny-adapter.proto:170-172` corrected in R1b. |
| C6 | `CH-EVENTSTREAM` | `Adapter/LifecycleChannel` gRPC control stream | Channel | pod-to-gateway | server WIRED, client UNWIRED | Proto RPC and both message types, `pkg/adapter/controlchannel.go` file and symbols, metric names, `spec/04` §4.7, `spec/10`, and `docs/`. |
| C7 | `LNK-GWCONTROL` | `GatewayControl` gRPC service | Link | pod-to-gateway | WIRED | `spec/28`. The proto service name is kept. |
| C8 | `CH-MSGSOCK` | Runtime message socket, `@lenny-runtime` | Channel | intra-pod | WIRED | `spec/28`, and the title and description of `schemas/lenny-adapter-jsonl.schema.json`. The socket name is kept. |
| C9 | `CH-RUNTIMEOPS` | Runtime lifecycle channel | Channel | intra-pod | WIRED in-binary, UNWIRED at the deployment boundary | `pkg/adapter/lifecyclechannel.go` file and symbols, `schemas/lifecycle-events.schema.json`, manifest key `lifecycleChannel`, socket `@lenny-lifecycle`, flag `--lifecycle-socket`, three runtime SDKs, `spec/04` §4.7 Part B, `spec/15` §15.4.6, and `docs/`. |
| C10 | `CH-MCP-PLATFORM` | Platform MCP socket | Channel | intra-pod | WIRED | `spec/28`. No code change. |
| C11 | `CH-MCP-CONNECTOR` | Connector MCP sockets | Channel | intra-pod | WIRED | `spec/28`. No code change. |
| C12 | `CH-LLMPROXY` | LLM proxy | Channel | pod-egress | gateway half WIRED, pod client ABSENT | `spec/28`. No code change in R1. |
| C13 | `CH-OBJSTORE` | Object store | Channel | pod-egress | WIRED | `spec/28`. No code change. |
| C14 | `REG-COORDLEASE` | Redis coordination lease | Register | gateway-to-store | WIRED | `spec/28`. No code change. |
| C15 | `REG-COORDMIRROR` | Postgres `coordination_lease` mirror | Register | gateway-to-store | write WIRED, routing read UNWIRED | `spec/28`. No code change. |
| C16 | `CH-EVENTRELAY` | Redis event relay | Channel | gateway-to-store | publish WIRED, live tail UNWIRED | `spec/28`. No code change. |
| C17 | `REG-CLAIM` | SandboxClaim | Register | control-plane | WIRED | `spec/28`. No code change. |
| C18 | `REG-SLOTCOUNT` | Redis slot counter | Register | gateway-to-store | WIRED | `spec/28`. No code change. |
| C19 | `LNK-INTERREPLICA` | Gateway to gateway | Link | inter-replica | ABSENT | `spec/28` and `spec/04` §4.7. Built in R18. |
| C20 | `CH-PODHEALTH` | `grpc.health.v1.Health` | Channel | gateway-to-pod | server WIRED, client ABSENT | `spec/28`. No code change. |
| C21 | `REG-PODSTATE` | Postgres `agent_pod_state` mirror | Register | gateway-to-store | WIRED | `spec/28`. No code change. |
| C22 | `CH-ADMISSION` | Admission webhook to gateway internal HTTP | Channel | control-plane | WIRED, disabled by chart default | `spec/28`. No code change. |

Two rows, C6 and C9, carry a code and wire rename. Two more, C5 and C8, carry a text correction inside a
shipped wire artifact. Every other row is a specification and documentation change only.

### 3.5 Disambiguating the two lifecycle channels

`CH-EVENTSTREAM` and `CH-RUNTIMEOPS` are the collision. After R1:

```
    +-----------------------------------------------------------+
    |                    Gateway replica                        |
    +-------------------------+---------------------------------+
                              |
                    LNK-POD-GRPC (gateway dials pod IP:50051)
                              |
    +-------------------------v---------------------------------+
    |  Agent pod                                                |
    |                                                           |
    |  +------------------ adapter container ----------------+  |
    |  |                                                     |  |
    |  |  CH-EVENTSTREAM : adapter-authored control events   |  |
    |  |    gateway dials, adapter pushes                    |  |
    |  |    rpc AdapterEventStream, bidi gRPC                |  |
    |  |                                                     |  |
    |  |  CH-RUNTIMEOPS  : adapter-to-runtime operations     |  |
    |  |    adapter listens, runtime dials                   |  |
    |  |    unix @lenny-runtime-ops, JSON Lines              |  |
    |  |                                                     |  |
    |  |  CH-MSGSOCK     : agent message plane               |  |
    |  |    adapter listens, runtime dials                   |  |
    |  |    unix @lenny-runtime, JSON Lines                  |  |
    |  +----+---------------------------+--------------------+  |
    |       | CH-RUNTIMEOPS             | CH-MSGSOCK           |
    |  +----v---------------------------v--------------------+  |
    |  |               runtime container                     |  |
    |  +-----------------------------------------------------+  |
    +-----------------------------------------------------------+
```

<!--
ASCII fallback for the diagram above (channel disambiguation):
Gateway replica ===(LNK-POD-GRPC, dials pod IP 50051)==> Agent pod.
Inside the agent pod, the adapter container hosts three distinct channels:
  CH-EVENTSTREAM  : gateway dials, adapter authors the events, bidi gRPC on LNK-POD-GRPC
  CH-RUNTIMEOPS   : adapter listens, runtime dials, unix socket @lenny-runtime-ops, JSON Lines
  CH-MSGSOCK      : adapter listens, runtime dials, unix socket @lenny-runtime, JSON Lines
CH-RUNTIMEOPS and CH-MSGSOCK both terminate in the runtime container.
-->

The three properties that distinguish them, stated once so they never have to be re-derived:

| Property | `CH-EVENTSTREAM` | `CH-RUNTIMEOPS` | `CH-MSGSOCK` |
|:--|:--|:--|:--|
| Boundary | crosses the pod boundary | intra-pod | intra-pod |
| Transport | gRPC bidirectional stream | Unix socket, JSON Lines | Unix socket, JSON Lines |
| Dialer | gateway | runtime | runtime |
| Author of the messages | adapter | both: the adapter drives the requests and the runtime replies | both |
| Vocabulary | `RATE_LIMITED`, `AUTH_EXPIRED`, `PROVIDER_UNAVAILABLE`, `LEASE_REJECTED`, `AdapterTerminating`, `AdapterEvicting`, `CheckpointBarrierAck`, and `FINAL_USAGE_REPORT` | handshake `lifecycle_capabilities` and `lifecycle_support`; adapter-to-runtime `checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, `terminate`, and `files_updated`; runtime-to-adapter `checkpoint_ready`, `interrupt_acknowledged`, `credentials_acknowledged`, `llm_request_started`, and `llm_request_completed` | `message`, `response`, `tool_call`, `tool_result`, `heartbeat`, `heartbeat_ack`, `shutdown`, `status`, and `set_tracing_context` |
| Schema | `schemas/lenny-adapter.proto` | `schemas/runtime-ops-events.schema.json`, named `schemas/lifecycle-events.schema.json` before R1b | `schemas/lenny-adapter-jsonl.schema.json` |
| Status | server WIRED, client UNWIRED | WIRED in-binary, UNWIRED at the deployment boundary | WIRED |

### 3.6 R1a: register, prose, and Go symbols

**Closes:** no record. R1a is the prerequisite for every other step, and its own closures are carried by
R1b (the shipped wire artifacts) and R2a (the registers).

**Scope.** Write the naming law and the three registers. Remove the colliding bare noun phrases from
prose: `spec/`, `docs/`, and Go doc comments. Correct the `spec/03` diagram line and add the pointer. The
Go file and symbol renames themselves are R1b's and are listed in its scope table, so exactly one step
moves each file.

**The three kubelet-path SIGTERM comments stay.** `pkg/adapter/session.go:355-364`, `:380-384`, and
`pkg/adapter/controlchannel.go:238-247` each describe a kubelet-path SIGTERM handler that does not exist.
None of them contains a colliding channel name, so none is a naming-collision artifact, and each is the
only in-tree description of the mechanism 6b.3 and the producer row of 6a.4 are about. Deleting them in a
step whose stated mitigation is "no behavior change" would remove the lead without leaving a record.
R1a leaves them in place and names them as three seed rows for R2a's claim register, each with
`status: ABSENT` and `deferral_id: R16`. R16 either implements the handler and makes the comments true or
deletes them.

**Where the register lives.** `spec/28_communication-channels.md` §28.1 through §28.4. The planning
inputs proposed a normative `spec/03` §3.1 as the register home on the grounds that `spec/03` is 47 lines,
is recorded as non-normative in `tests/spec-map.json:35-44`, and appending renumbers nothing. Two of those
three facts hold. The placement is rejected for two reasons. First, it produced two registers with two
identifier schemes and no declared authority on disagreement, so every channel would have carried three
identifiers. Second, `spec/README.md` lists §3 as a single line with no subsections, so a reader scanning
the table of contents for channel documentation does not find it there, while a top-level section named
"Communication channels" is exactly what a table-of-contents scan finds. `spec/03` gets the corrected
diagram line and a pointer. Note that the §3 entry in `tests/spec-map.json` already carries a test
(`tests/tier3_contract/workspaceplan/sources_test.go`) despite its "No tests required" note, so the entry
is amended rather than replaced.

**Not in scope.** Metric renames. `lenny_adapter_control_events_total` and `_dropped_total`
(`pkg/adapter/metrics.go:71`, `:79`) appear nowhere outside the adapter package: not in `spec/`, not in
`pkg/observability/metrics/catalog.go`, and not in any chart or alert rule. Renaming them changes nothing
observable and produces orphan churn. The rename is deferred to R12 rather than exempted: R12 adds the
adapter metrics endpoint and the catalog entries, so the metric label takes its N4 identifier at the
moment the metric first becomes observable. §28.1 states that N4 binds the metric-label namespace and N7
binds the flag, environment-variable, and manifest-key namespaces, and that R1a defers the metric half of
N4 to R12 with a claim-register row naming R12 as the step that discharges it.

**Tests.** A tier-0 naming lint enforcing N3 and N4 across `spec/`, `docs/`, `schemas/`, and Go doc
comments. It lands green by seeding an enumerated exception register, never by suppression, and every
exception names the step that retires it. Measured baseline: 60 occurrences of the reserved bare phrases
across eleven files in `spec/` (`spec/04`, `05`, `06`, `07`, `10`, `13`, `15`, `16`, `17`, `24`, and
`26`). The planning input's figure of 57 across nine files did not reproduce, and the two files it missed
(`spec/24_lenny-ctl-command-reference.md` and `spec/26_reference-runtime-catalog.md`) are operator-facing
and runtime-author-facing, which is where the ambiguity does the most damage.

**Risk.** The highest textual blast radius in the plan and one of two hard tree freezes. Mitigated by
keeping it short: no behavior change, no proto regeneration, and no spec renumbering.

**Size.** Large in diff, small in judgement.

### 3.7 R1b: the wire contract change

**Closes:** the shipped-artifact half of 6b.14, which is the wrong description at
`schemas/lenny-adapter-jsonl.schema.json:5` calling the intra-pod checkpoint and credential frames a
"gateway↔adapter lifecycle channel" riding "the gRPC LifecycleChannel stream". Both halves of that
sentence are wrong: the frames are adapter-to-runtime and they ride the Unix socket. Carries the proto
field additions 6b.1 and 6b.9 need.

This step exists because the planning inputs contained a contradiction that cannot ship. The step-1
design demoted the wire frame-string rename to deferred. The step-2 design independently specified
"prose names only", keeping the proto method `Adapter/LifecycleChannel` and the Go type file. R1's own
scope then said the opposite. Whichever reading wins, the machine-readable surfaces stay ambiguous, and
"step-3 code work" resolves to no step in the twenty-five.

The colliding word is load-bearing on surfaces a prose rename does not reach:

- Normative specification field tables at `spec/04_system-components.md:705` (Part B), `:742`
  (`"lifecycleChannel": { "socket": "@lenny-lifecycle" }`), `:793`, `:794`, and `:819`, and at
  `spec/15_external-api-surface.md:2305`.
- The adapter manifest emitter at `pkg/adapter/manifest.go:157`, documented at `:61` as "the §15.4.6
  `lifecycleChannel` manifest".
- Three runtime SDK public APIs: `sdks/runtime/go/runtime/types.go:136`,
  `sdks/runtime/typescript/src/types.ts`, and `sdks/runtime/python/lenny_runtime/types.py`.
- The adapter flag `--lifecycle-socket` (`cmd/lenny-adapter/main.go:151`).
- The gRPC method at `schemas/lenny-adapter.proto:227`.
- The third-party runtime contract in `docs/runtime-author-guide/integration-levels.md`, which instructs
  authors to read `manifest.lifecycleChannel.socket`.

Leaving these in place also breaks the lint. The `spec/04` field-table lines cannot be reworded while
`lifecycleChannel` is the wire field, so they enter the exception register, and the register becomes an
exhaustive index of exactly the surfaces where the ambiguity still lives. The gate would certify as
compliant a specification in which the collision is fully intact.

**Decision.** The wire rename lands as one coordinated change, in R1b, immediately after R1a and before
R12 and before 0062 resumes. `.claude/rules/code-best-practices.md` states the governing constraint
directly: "No backward-compatibility shims, dual modes, legacy flags, or migration paths for external
compatibility: the platform is pre-deployment and has no deployments in the wild."

**Scope, in one `make generate-proto`:**

| Surface | Today | After |
|:--|:--|:--|
| Proto RPC and messages | `rpc LifecycleChannel(stream LifecycleChannelRequest) returns (stream LifecycleChannelResponse)` | `rpc AdapterEventStream(stream AdapterEventStreamRequest) returns (stream AdapterEventStreamResponse)` |
| Manifest key | `lifecycleChannel` | `runtimeOps` |
| Socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| Adapter flag | `--lifecycle-socket` | `--runtime-ops-socket` |
| Go file and type | `pkg/adapter/lifecyclechannel.go`, `LifecycleChannel` | `pkg/adapter/runtimeops.go`, `RuntimeOps` |
| Go file | `pkg/adapter/controlchannel.go` | `pkg/adapter/eventstream.go` |
| Runtime-ops schema file | `schemas/lifecycle-events.schema.json` | `schemas/runtime-ops-events.schema.json` |
| JSONL schema description | "gateway-adapter lifecycle channel ... the gRPC LifecycleChannel stream" | the corrected `CH-MSGSOCK` description |
| `CheckpointBarrier` doc comment (`schemas/lenny-adapter.proto:170-172`) | "the control-stream emit is the canonical surface for the gateway's barrier-target reconciler" | the unary `CheckpointBarrierResponse` named as the canonical surface, with no reconciler claim |

The same window carries the proto field additions the plan needs later, so there is exactly one
regeneration and one `buf breaking` decision: `coordination_generation` on every operational
gateway-to-pod request message (6b.1), a slot identifier on `InterruptRequest`, `SignalDeadlineRequest`,
`ReportUsageRequest`, and `CheckpointBarrierRequest` (6b.9), and `ResumeRequest.slot_id` for R22. Fields
land unread. Each is recorded in the claim register as `UNWIRED` with the step that reads it named, so an
added-and-unread field is tracked rather than mistaken for a closure.

**The `buf breaking` gate.** `cmd/lenny-test/cmd_run.go:499-536` runs
`buf breaking schemas/ --against .git#branch=main`. Off `main` the finding is advisory; on `main` it
hard-fails. `buf.yaml` sets `ignore_unstable_packages: true`, which does not help, because the package is
`lenny.adapter.v1`, a stable version suffix. R1b must land with a recorded baseline decision: either
advance the baseline ref or add an enumerated exception with an expiry. R3 owns the register that decision
goes into, which is why R3 starts at t=0.

**Tests.** Round-trip conformance for all three runtime SDKs against the renamed manifest key. The
external-adapter compliance suite (`cmd/lenny-compliance/full.go:98`) writes the manifest itself and must
be updated in the same change. Tier-0 schema bijection.

**Risk.** This is the plan's most externally visible change and its most likely stall point. The residual
risk is a third-party Full-level runtime pinned to the old manifest key. The mitigation is that no such
runtime can currently reach Full level against a chart-rendered pod at all, because the channel is never
enabled (6a.3), so the population of affected runtimes is empty by construction. That reasoning is worth a
human confirmation before execution and is listed in section 9.

**Size.** Medium diff, high coordination.

### 3.8 Identifier migration

The reference's `C<n>` numbering is carried as a permanent provenance column in the §28 register, so a
reader holding the 2,710-line reference or the research notes can resolve any citation. It is a
non-normative column and is never used as a citable handle.

The reference document itself is frozen rather than maintained. R2a adds a header stating that it is a
point-in-time reading of the working tree at `fcda83e3` and that §28 and §29 supersede it for all current
behavior, and a tier-11 test asserts the header is present and the body is unmodified. Keeping it as a
living document produces two authoritative descriptions that drift, which is the defect this plan exists
to remove. Deleting it orphans the provenance column and the research notes. Freezing it keeps the
citations resolvable and makes the authority unambiguous.

---

## 4. Step 2: specification enhancement and modularization (R2)

The user's mandated second step. Its purpose is that the reference's content lives in `spec/` in a form
that does not need re-deriving.

### 4.1 Invariants

- **INV-1.** No renumbering, no file renames, and no cross-file content moves. New top-level sections
  append from 28. Verified: `spec/` ends at `27_web-playground.md`.
- **INV-2.** Additive sub-numbering only. A numbered heading, once assigned, never changes meaning. A
  heading is numbered only when code, a test, or a gap-closing proposal must cite it. Unnumbered headings
  remain the dominant convention.
- **INV-3.** Content is deleted only where this plan designates a successor owner. This is a deliberate
  amendment to the planning input's "deleted only where an owner already exists", which would have left
  three normative prose descriptions of the same contract in place (section 4.2).
- **INV-4.** No `file:line` code evidence in specification prose. Evidence lives in
  `tests/claim-map.json`, which is gated.

### 4.2 R2a: the new sections

**Closes:** the specification half of 6b.14, which is `spec/15:1462-1464` framing the adapter contract as
three machine-readable artifacts, attributing the runtime-frame messages to
`schemas/lenny-adapter-jsonl.schema.json`, and never naming the file that actually holds them. §28.7
supersedes that list. Substrate for every capability step.

`spec/28_communication-channels.md`, budget 900 lines:

- **28.1 Naming law and taxonomy.** The five axes, the three classes, N1 through N7, the reserved-word
  ban, and the status vocabulary. This must earn itself in under a page, because conceptual overhead is
  the cost this taxonomy charges against the "a human can easily understand" goal.
- **28.2 Link register** (`LNK-`), **28.3 Channel register** (`CH-`) with the full inventory table, and
  **28.4 Shared-state register** (`REG-`). One row per entry, with the provenance column.
- **28.5 Contract cards, grouped by participant edge.** One subsection per axis-4 boundary value, in the
  order §3.2 fixes: 28.5.1 gateway-to-pod, 28.5.2 pod-to-gateway, 28.5.3 intra-pod, 28.5.4 inter-replica,
  28.5.5 pod-egress, 28.5.6 control-plane, and 28.5.7 gateway-to-store. Each subsection
  opens with a one-edge ASCII figure and holds its cards as bolded blocks under a fixed eight-field
  template, capped at 25 lines per card. Grouping by edge is what makes the unbuilt adapter-to-gateway
  direction a visible block (§28.5.2, status `UNWIRED` end to end) rather than two rows in a twenty-two
  row table. The citable handle is `§28.5.2 CH-EVENTSTREAM`, a stable mnemonic anchor.
- **28.6 Exclusivity and concurrency model.** Reference §1.5 vocabulary made normative, with the missing
  guard named per channel.
- **28.7 Wire-contract artifact register.** Derived mechanically from `ls schemas/**` rather than
  hand-enumerated. Measured today: `lenny-adapter.proto`, `lenny-adapter-jsonl.schema.json`,
  `lenny-interceptor.proto`, `lenny-tokenservice.proto`, `lifecycle-events.schema.json` (renamed to
  `runtime-ops-events.schema.json` by R1b), `messagepart.schema.json`, `workspaceplan-v1.json`,
  `ocsf-mapping.yaml`, and `audit-events/v1.json`.
  The planning input specified six and omitted `workspaceplan-v1.json`, which the incomplete list it
  supersedes at `spec/18_build-sequence.md:92` already names. This register supersedes the three-artifact
  list at `spec/15:1462-1464`, the list at `spec/18:92`, and the three-artifact compliance-suite list at
  `spec/24_lenny-ctl-command-reference.md:114`, which today omits `lifecycle-events.schema.json` and so
  structurally prevents the shipped conformance suite from validating `CH-RUNTIMEOPS`.
- **28.8 Failure and degradation matrix.** No owner anywhere today.

`spec/29_communication-scenarios.md`, budget 1,200 lines: the reference's nine end-to-end scenarios as
numbered step lists naming channels by identifier, plus the cross-replica off-holder matrix from reference
§5.2 as the normative statement of required off-holder behavior per route.

Two files rather than one, because §28 is looked up and §29 is read once, and they have different
maintenance cadences. Combined they would reproduce the failure mode of `spec/15` (2,736 lines and
self-contradicting) and `spec/25` (5,296 lines).

**The disposition of `spec/15` §15.4.** `spec/15:1456` states "this section (15.4 and its subsections) is
the normative prose reference", reinforced at `:1467`: "any discrepancy between the artifacts and this
prose is a bug that must be reconciled before release". `spec/04` §4.7 (lines 637 to 972) holds the RPC
table, the manifest field table, and both channel definitions. Adding §28 without acting on these produces
a third normative prose description of one contract. Under INV-3 as amended, R2a designates §28 the
successor for the channel contract, reduces §15.4 to the wire-artifact pointer it already claims to be,
revises `spec/15:1456` and `:1467` accordingly, and reduces §4.7's channel prose to the component
description plus a link. Without this the specification grows by roughly 11 percent and the "crisp and
human-readable" goal is inverted rather than met.

**The reductions move content, so R2a ships a redirect map.** Measured on the tree: 1,009 occurrences of
`§15.4` and 1,443 of `§4.7` in Go files. Each names a section whose channel content R2a relocates into a
§28 card. Section 4.4's first mechanism, that new anchors are additive, holds for §28 and §29 as files and
does not hold for these two reductions. Leaving them unhandled would either hold the R3 citation resolver
red for six waves or push roughly 2,450 entries into the exception register, which section 4.3 rules out
as suppression. R2a therefore ships `tests/spec-anchor-moves.json`, a section-level map from each retired
anchor to its `§28.5.N CH-*` successor. The resolver consults the map, so a citation naming a retired
anchor resolves through it and stays green, and the map is a tracked redirect rather than a waiver:
tier 0 fails an entry whose successor anchor does not exist. Rewriting the 2,450 citations to their
successors and emptying the map is mechanical text substitution over Go comments, so it is R2b's job under
rule S-7 rather than a wave-2 change contending with R1b and R5 on the same Go files. `scripts/specshift`
therefore rewrites anchors as well as line numbers, and R3 builds both halves.

**The claim register.** `tests/claim-map.json`, schema
`{claim_id, channel_id, spec_anchor, status, surface, deferral_id, notes}`, seeded from the reference's
54-row status table (§7.1) and its 11-row checkpoint-drive table (§7.3). `surface` is required when
`status` is `WIRED` and carries a symbol reference rather than a bare line number. `deferral_id` is
required when `status` is `UNWIRED` or `ABSENT` and names the step that closes it. R2a lands the schema,
the seed rows, and a schema-only tier-0 validator. The load-bearing join, that every `WIRED` claim must
cite a surface the reachability gate reports reachable, lands in R9 once that gate exists. R2a cannot
build the join against a gate that does not exist, which is a dependency the planning input recorded as a
peer relationship.

**Spec-map handling.** `validateSpecMapCoverage` (`cmd/lenny-test/cmd_validate.go:424-457`) fails any
section whose `tests` array is empty and which has no entry in `tests/spec-map-exceptions.yaml`
(currently 46 entries, 167 lines). The §28 cards for `UNWIRED` and `ABSENT` channels have no tests by
definition. The existing reason vocabulary has no honest slot for "normative contract for an unimplemented
channel", and the file's schema carries no `owner`, `opened_at`, or `eta`. R3 adds those three fields and
a `pending-implementation` reason class before R2a needs them, so each §28 card exception names the step
that retires it and the exception list becomes part of R24's work queue rather than a set of silent
waivers. Note separately that `validateSpecMapCoverage` iterates only sections present in the map and does
not scan `spec/` for headings lacking an entry, despite the doc comment at `cmd_validate.go:20-22`. The
heading scanner is new tooling and belongs to R3.

**Dependencies.** R1a for the names, and R3 for the three new `tests/spec-map-exceptions.yaml` fields and
the `pending-implementation` reason class the §28 cards need. Not R1b: the new files can be drafted
against the post-rename names while R1b executes, and finalized on merge.

**Parallel with.** R1b, R5, and R7. R2a writes `spec/28`, `spec/29`, the reductions to `spec/04` §4.7 and
`spec/15` §15.4 described above, `tests/claim-map.json`, `tests/spec-anchor-moves.json`,
`tests/spec-map.json`, the frozen-reference
header on `gateway-runtime-comms.md`, and the tier-11 tests under `tests/tier11_docs/` that assert the
header and gate G9. None of those files is opened by R1b, R5, or R7.

**Risk.** The largest documentation change in the plan and the one most likely to drift from the code it
describes. The claim register plus R25's reconciliation test is the control. Without both, this is a
second document that goes stale.

**Size.** Large.

### 4.3 R2b: heading anchors and line-citation retirement

This is scheduled as a separate, late, exclusive step, and the reasoning is the sharpest correction the
adversarial review produced.

The planning input scheduled numbered-heading insertion into `spec/04` (§4.4.1 to .5 and §4.7.1 to .11),
`spec/10` (§10.1.1 to .8), and `spec/13` (§13.2.1 to .7), and declared R2 parallel with four other steps
and writing only spec files. Measured on the tree: **15,377 `§X line N` citations across 2,353 Go files**,
and `spec/10` §10.1 begins at line 3, so every one of the citations into it shifts. Inserting a heading at
`spec/04:220`, at `spec/10:3`, and at `spec/13:32` invalidates citations in Go files under `pkg/gateway`,
`pkg/adapter`, `cmd/lenny-gateway`, `pkg/controller`, and the test tiers. R2b therefore collides with every
step that adds or moves Go code carrying a `// spec:` citation, which is R1a, R1b, R4, R5, R6, R7, R9, and
R10 through R24. The alternative, not rewriting them, seeds several thousand entries
into a pending register, which is the suppression the plan forbids and which makes the citation resolver
vacuous at birth.

**Decision.** R2b is one exclusive tree freeze scheduled after every code-moving step has merged, and it
is executed mechanically by `scripts/specshift` (built in R3) with a proven dry run as its entry criterion.
No step's output depends on R2b: every step that needs a citable anchor cites a `§28.5.N CH-*` card, which
lives in a new file and needs no in-file surgery. Rule S-7 nonetheless places R2b after every code-moving
step, so it sits between wave 7 and R25 in the wave ordering. This is also what stops five later proposals
contending over one paragraph in §4.6.1.

R2b additionally rewrites the citations that R2a redirected, replacing each `§15.4` and `§4.7` occurrence
whose subject moved with its `§28.5.N CH-*` successor and emptying `tests/spec-anchor-moves.json`. It also
breaks the 3,778-character six-contract paragraph at `spec/04:489` into five unnumbered bolded paragraphs.
That paragraph alone anchors 6b.2, 6b.3, 6b.6, 6b.7, and 6c.5.

**Dependencies.** R3 for `specshift`, the citation resolver, and the heading walker. R2a for
`tests/spec-anchor-moves.json`, whose entries R2b discharges. Every step that adds or moves Go code
carrying a `// spec:` citation, which is R1a, R1b, R4, R5, R6, and R10 through R24.

**Parallel with.** Nothing. Second tree freeze.

**Risk.** A mechanical rewrite of 15,377 comment citations. The mitigation is that the resolver gate makes
a miss loud rather than silent, and that `specshift` carries its own `run_test.go`.

**Size.** Very large diff, low judgement, entirely mechanical.

### 4.4 How `// spec:` citations and `tests/spec-map.json` stay valid

Four mechanisms, in the order they engage:

1. **New anchors are additive, and the two reductions are redirected.** §28 and §29 are new files, so no
   existing citation moves when they land. R2a's reductions to `spec/15` §15.4 and `spec/04` §4.7 do move
   content, and the 1,009 `§15.4` and 1,443 `§4.7` occurrences in Go files resolve through
   `tests/spec-anchor-moves.json` until R2b rewrites them to their `§28.5.N CH-*` successors.
2. **The citation resolver** (R3, tier 0) asserts that every `§X.Y line N` in the tree resolves to a line
   still inside section X.Y. It generalizes
   `tests/tier0_static/degradation_lock_line_citation_test.go`, which today hard-codes two §25.4 checks.
3. **`scripts/specshift`** (R3) rewrites line citations mechanically when a spec file's line numbering
   changes, and is the only sanctioned way to perform R2b.
4. **The heading walker** (R3) fails a numbered heading with no `tests/spec-map.json` entry. Its
   enablement is sequenced after R1a so R1a's own new headings are seeded rather than reported.

The forward-looking convention, stated in §28.1 and in `.claude/rules/`, is that a new citation names an
anchor rather than a line. The 15,377 existing line citations are retired by R2b, which is the one job
that both adds the anchors and rewrites the citations, done once.

---

## 5. Remediation steps R3 through R25

Each subsection states what it closes, scope, specification changes, code changes, tests, dependencies
with reasons, parallelism with reasons, risk, and rough size. Sizes are S (days), M (one to two weeks),
and L (multiple weeks), for one worker.

### R3. Specification and test tooling

**Closes:** 8.1 as a standing mechanism (R25 discharges the item itself). Prerequisite tooling for R2b and
the feedback loop for every later gate.

**Scope.** The citation resolver, `scripts/specshift`, the heading walker, change-graph completeness, an
`UNVERIFIED` verdict state, the shared register contract, and the gate-integrity meta-gate.

Six facts about the current harness set the design and were verified:

1. **CI's lint gate is advisory.** `.github/workflows/pr.yml:46-89` never runs `golangci-lint` as its own
   step; it installs it at `:70` and invokes it only through `./bin/lenny-test --tier static` at `:79`,
   which downgrades any exit to a non-fatal warning at `cmd/lenny-test/cmd_run.go:583-591`. Nothing
   designed as a linter finding is a gate.
2. **Four tier-0 checks are non-fatal and fourteen pass silently when their script is absent**
   (`cmd_run.go:590`, `:686`, `:697`, `:713`, and fourteen `os.Stat`-guarded skip sites). A bash script
   under `scripts/` is not a durable place for a gate. The two hard-gated channels are
   `go test -count=1 ./tests/tier0_static/...` (`cmd_run.go:717-720`) and the `validate-maps` and
   `validate-diagnosis` subcommands, which `pr.yml:77-80` runs with their own exit codes. Every gate in
   this plan lands as a Go test under `tests/tier0_static/` or as a check in `runValidateMaps`.
3. **`scripts/check-flake-budget.sh` does not exist**, although `tests/flake-budget.yaml:12` names it as
   the tier-0 enforcement location. The real validator is `validateFlakeBudgetYAML`. A document asserts a
   mechanism, the mechanism is not at the named location, and nothing checks. That is the same failure as
   the whole remediation, inside the test infrastructure.
4. **The skip-reason convention is unenforced and bypassable.** `scripts/lint-test-conventions.sh:84-107`
   implements it, its exit is downgraded at `cmd_run.go:697`, and its allow pattern at `:101` matches any
   `t.Skipf` regardless of reason. Measured: 115 `t.Skipf("precondition not met` sites and 27
   `t.Skip("precondition not met` sites. The classifier must parse the AST rather than the string.
5. **Most of what is needed exists.** `verdict.go:30-32` already carries a third state (`INCONCLUSIVE`)
   with a promotion switch at `:245-254`, so `UNVERIFIED` is one constant and one branch.
6. **The register pattern exists in tree** as `tests/change-graph-pending.txt` and
   `tests/spec-map-pending.txt`.

**The register contract, stated once for every gate.** `tests/registers/<name>.yaml`, validated by a
`validate<Name>YAML` in `runValidateMaps`, modelled on `validateFlakeBudgetYAML` and
`validateParityMatrixYAML`. Shared entry schema: `subject`, `verdict` (one of `intentional`, `tracked`, or
`deferred`), `owner`, `opened_at`, `eta`, `blocker`, and `reason`. Three ratchet rules: an unregistered
violation fails; a passed `eta` fails; and a `blocker` that does not resolve to an open item fails. The
third rule is the escalation channel that `tests/tier5_e2e_kind/node_drain_checkpoint_test.go:67` lacked
for months.

**Code.** `cmd/lenny-test` (validator, verdict state, and change-graph resolution), `scripts/specshift`,
`tests/tier0_static` citation and heading tests, and the seeded registers. Also the three new fields on
`tests/spec-map-exceptions.yaml` and the `pending-implementation` reason class R2a needs.

**Tests.** `run_test.go` for `specshift`. Self-tests for the resolver and the walker. A gate-integrity
meta-gate asserting that no gate this plan adds can be disabled by deleting a script.

**Dependencies.** None. Starts at t=0.

**Parallel with.** Everything in waves 0 and 1. R3 writes `scripts/`, `cmd/lenny-test/`, and
`tests/registers/`, which no other step touches. The one hazard is the heading walker firing on R1a's new
headings, so its enablement is sequenced after R1a.

**Risk.** Low.

**Size.** M.

### R4. Bounded investigations that block downstream design

**Closes:** 8.3 (whether `srv.GracefulStop()` can hang past the grace deadline) and 8.5 (whether the
elicitation waiter mirrors the tool-approval store poll), and it converts 8.2 into a requirement rather
than an answer.

**Why it is in wave 0.** 8.3 determines whether any SIGTERM-driven or preStop-driven checkpoint has a time
budget at all. A long-lived `Attach` server stream is a pending RPC, `GracefulStop` blocks on pending RPCs,
and the call has no timeout and no fallback to `Stop()`. If the answer is that it can hang, R16's design
changes and its estimate grows. A reading cannot settle it; a timing test can. Discovering this at R16 is
the failure mode this step exists to prevent.

8.5 sizes R20 in the same way. Tool approval polls a shared Postgres-backed store every 25 milliseconds so
that a resolution landing on a non-coordinator wakes the blocked executor, and the request-input half has
no shared store at all (6c.4). If the elicitation waiter does not mirror the poll, R20 builds a second
cross-replica store rather than reusing one. The planning input protected this class of dependency for R16
and did not for R20; both get it here.

**8.2 is unanswerable from inside this repository.** Whether an out-of-tree overlay sets
`--lifecycle-socket`, `--post-mortem-dir`, or `OTEL_EXPORTER_OTLP_ENDPOINT` cannot be established here. It
converts to a compatibility requirement on R6 and R17: once the podspec renders these flags, an
out-of-tree setter conflicts, so the deliverable is a stated migration note plus a register row.

**Code.** Only if 8.3 is positive: a bounded `GracefulStop` with a `Stop()` fallback, in
`cmd/lenny-adapter/main.go:410-424`.

**Tests.** `TestAdapterExitsWithinGraceBudget`, tier 7a: open a long-lived `Attach` stream, send SIGTERM,
and assert the process exits within the measured budget.

**Dependencies.** None for the investigation, which is read-only plus isolated new tests.

**Parallel with.** Everything in wave 0. The planning input serialized R4's fix behind R1 on the grounds
that R1 splits `cmd/lenny-adapter/main.go`. R1's stated scope is confined to `pkg/adapter`, and
`cmd/lenny-adapter/main.go` is 461 lines, so splitting it is neither stated nor motivated by a rename and
contradicts R1's own "no behavior changes" mitigation. The edge is dropped and the fix lands against
today's `main.go` in wave 0, which is where the plan wants the answer.

**Risk.** A positive 8.3 answer grows R16.

**Size.** S.

### R5. Session-server and composition-root carve-up

**Closes:** no record. This is the decoupling investment that every wave-3 and wave-5 parallelism claim in
this plan is conditional on.

**Scope.** `pkg/gateway/sessionserver/start.go` is 4,472 lines and `sessionserver.go` is 3,539. R10, R12,
R13, R19, R20, and R22 all append to them. Split by concern so each downstream step's surface lives in a
file no sibling touches. `cmd/lenny-gateway/main.go` (2,726 lines) and `workers.go` (1,817) get the same
treatment for the composition root and the worker set.

The planning input scoped this as a `start.go` carve-up plus `workers.go`. That is insufficient. R10 wraps
the session-scoped mutating routes, which include the handlers in
`pkg/gateway/sessionserver/interactions.go` and `messages.go` (894 lines), and R20 owns inbox redelivery
and the cross-replica input wait, which are the same two files. Splitting `messages.go` and
`interactions.go` into a routing-and-gating layer and a resolution layer is an explicit R5 exit criterion.

**Code.** File moves and package-internal reorganization only. No signature changes and no behavior
changes.

**Tests.** No new behavioral tests. The exit criterion is that the existing suite passes unchanged and
that each downstream step's surface lives in a file no sibling step touches, stated explicitly in the
commit message so the parallelism claims are auditable.

**Dependencies.** R1a, so the new per-concern file and symbol names conform to the naming law rather than
being renamed again later. R5 carves `pkg/gateway` and `cmd/lenny-gateway`, which R1b does not open, so it
needs nothing from R1b.

**Parallel with.** R1b (disjoint: R1b touches `schemas/`, `pkg/adapter`, `sdks/`, and `cmd/lenny-adapter`),
R2a, and R7.

**Risk.** A refactor of a 4,472-line file is where silent behavior changes hide. Mitigation: no logic edits
in the same change, and the reviewer diffs by moved block rather than by line.

**Size.** M.

### R6. Podspec and chart decoupling, and the deployment-boundary gate

**Closes:** the PodDisruptionBudget and eviction-API admission rows of 6a.4, and the chart half of 6c.7.
Provides G3a, the deployment-boundary recurrence guard, for the whole class in section 7.1: 6a.3 (R17
renders it), the `--post-mortem-dir` half of 6a.2 (R12), the OTLP row of 6a.5 (R12), 6b.12 (R17), and the
mTLS material for 6c.7 (R14). Verdict owner for 8.2, whose compatibility note this step carries.

**Two rows of 6a.4 that the planning inputs left with no owner anywhere.** 6a.4 is a heading over an
eleven-row table (`gateway-runtime-comms.md:1780-1792`), and it was assigned whole to R16 while 6a.5 and
6b.15 were expanded into row tables. Two of its rows are owned by nothing:

- **PodDisruptionBudget protection for a busy pod.** `pkg/controller/warmpool/pdb.go:70-76` sets
  `Selector.MatchLabels[state.LabelState] = string(state.Idle)`, and a claimed pod resolves to the coarse
  `active` state (`pkg/sandbox/state/state.go:78-88`), so every session-holding pod sits outside every
  PDB.
- **The eviction-API admission gate's default and its gated condition.** `charts/lenny/values.yaml:1696`
  sets `drainReadiness: false`, and the webhook renders only under that flag
  (`charts/lenny/templates/admission-policies/drain-readiness-webhook.yaml:11`). The gate also evaluates
  artifact-store health rather than session liveness.

Both land here, because both are chart and controller surfaces and neither is an adapter change. Without
them R16 lands, 6a.4 is marked closed, and a busy pod is still evictable with no budget and no gate.

**Scope.** Split `pkg/controller/sandbox/podspec/podspec.go` (1,351 lines, the only non-test file in its
package) into per-concern builders. Convert the chart test's index-keyed and `lengthEqual` env assertions
(`charts/lenny/tests/gateway-deployment_test.yaml:1033`, `:3240`) to name-keyed lookup, so a step can add
an environment variable without renumbering a sibling's assertions. Fix the PDB selector. Flip the
drain-readiness default and change what the gate evaluates.

**The gate.** A tier-0 bijection: every flag or environment variable a component reads must be set by the
rendered podspec or carry a register row. Measured baseline: 27 adapter flags declared at
`cmd/lenny-adapter/main.go:112-192`, of which 16 are not set by the rendered podspec. Thirteen are
"defaults are correct" and three are gaps 6a.3, 6a.2, and 6c.7. The register seeds with those three, each
naming the step that renders it: R17 for `--runtime-ops-socket`, R12 for `--post-mortem-dir`, and R14 for
the mTLS material. `OTEL_EXPORTER_OTLP_ENDPOINT` is an environment variable rather than a flag and seeds
as a fourth row, also naming R12. The gate is green on arrival and the debt is countable.

**Dependencies.** R1a for the register's names, and R1b because rule N7 renames `--lifecycle-socket`.

**Parallel with.** R5 (`pkg/gateway` and `cmd/lenny-gateway`), R2a, and R7 (`tests/testinfra` and
`e2e-values.yaml`, which this step does not touch).

**Risk.** Converting index-keyed chart assertions is mechanical and wide, and a missed conversion surfaces
as a false green rather than a failure. Mitigation: assert the conversion by adding one environment
variable in the same change and confirming the affected assertions still hold.

**Size.** M.

### R7. Multi-replica and concurrent-slot harness, and default-values parity

**Closes:** no record. This is the test-infrastructure prerequisite without which R10, R12, R19, R20, R21,
and R22 land asserted rather than verified. It is the verification substrate nine records depend on:
6c.1 through 6c.5, 6b.6, 6b.7, 6b.10, and 8.7. Each of those nine names R7 in the contributing column of
section 8.2.

**Scope.** A two-replica harness plus a concurrent-slot dimension. This is an assembly of existing parts:
`gateway.StartWith` (`tests/testinfra/gateway/gateway.go:58`) already boots the gateway subprocess with
arbitrary flags, `tests/tier4_integration/lenny_ctl_ops_test.go:247` and `:286` already start two,
`resolveReplicaID` (`cmd/lenny-gateway/main.go:1014-1027`) honors `LENNY_REPLICA_ID`,
`containers.StartPostgres` and `StartRedis` supply shared stores, and `tests/testinfra/matrix/matrix.go`
is already a cross-product runner.

Also default-values parity: a tier-0 check that the e2e overlay does not silently disable a chart default,
plus a nightly stock-defaults tier-5 lane. This matters because
`tests/testinfra/kind/e2e-values.yaml:42-43` sets `mtls.enabled: false` against a chart default of `true`,
and `tests/tier9_security/tls_test.go:84` self-skips the entire internal-mTLS suite as a result. The
catching tier for 6c.7 is disabled by the configuration it runs under.

**Tests.** The harness itself, plus a smoke case proving a request can be routed to a deliberate
non-holder. Reference §5.2 is already a written 25-row test matrix; R10 turns it into a table-driven suite
on this harness.

**Dependencies.** None as a hard build dependency. The planning input made R7 depend on R3 for
change-graph completeness. `tests/change-graph.json` is a data file of path-to-tier globs
(`cmd/lenny-test/paths.go:42-44`), and adding a lane is a JSON entry rather than a tooling change, so the
edge is dropped. R3 remains a soft co-requisite for the skip budget, which can be back-filled. R7 has the
highest fan-out in the plan, so a spurious predecessor here is expensive.

**Parallel with.** Everything in waves 0 through 2. Touches `tests/testinfra/**`, new tier-4 and tier-5
lanes, and `e2e-values.yaml`.

**Risk.** Multi-process harnesses are flake sources and six steps depend on this one, so the stress budget
(`lenny-test stress`) is load-bearing rather than optional. Budget a stress run on the new lane before any
step depends on it.

**Size.** L.

### R8. Reciprocal host-conformance battery

**Closes:** 8.6 (the runtime side of the `checkpoint_ready` contract for third-party Full-level runtimes),
and it is the precondition for R17 asserting anything about third-party conformance.

**Why it is its own step.** Every existing harness synthesizes the precondition it should be testing.
`cmd/lenny-compliance/full.go:98` writes `{"lifecycleChannel": {"socket": ...}}` itself, so Full-level
conformance never sees the rendered podspec and the tier-10 battery validates against a fake host. The
planning inputs identified this need and gave it two conflicting owners, or none. It is comparable in size
to R7 and needs a step.

**Scope.** A `tests/tier2_component/hostconformance/` harness that boots the rendered container and drives
a reference runtime against the real adapter host, in the direction opposite to the existing conformance
battery. The existing battery asserts that a runtime satisfies the adapter's expectations; this one
asserts that the adapter host satisfies a runtime's expectations, including the `checkpoint_ready`
contract, the frame-level requirements of `schemas/runtime-ops-events.schema.json`, and the manifest key the
SDKs read.

**Dependencies.** R1b, because it exercises the renamed manifest key and socket. R6, because it boots the
rendered podspec rather than a synthesized manifest.

**Parallel with.** Every wave-3 step except R6, which it depends on, so R8 sits in wave 3b. Touches only
`tests/tier2_component/hostconformance/`, `cmd/lenny-compliance`, and `cmd/runtimes/`.

**Risk.** Booting a real container in tier 2 is slower than the tier's norm; it may need to be a tier-10
lane instead. Decide on measurement rather than in advance.

**Size.** L.

### R9. Production-reachability and wiring-bijection gates

**Closes:** no record directly. This is the recurrence guard for the production-reachability class in
section 7.1: 6a.1, 6a.2, 6a.6, 6a.7, 6b.3, 6b.13, and the rows of 6a.5 the register below enumerates as
reachability defects. It is not the guard for 6a.3, which is a deployment-boundary defect R6's G3a covers,
and it does not reach every row of 6a.5. It produces the machine-maintained work queue R24 drains.

**Why the behavioral tiers cannot catch this class.** The test is the missing caller.
`pkg/adapter/holdstate_test.go:111` calls `s.enterHoldState("s1")` directly, and `go test` cannot
distinguish a double standing in for an expensive collaborator from one standing in for a collaborator
that does not exist.

**G1, the unreachable-surface detector, in three stages.** Stage 1 grows `opsDeadCodePackages`
(`tests/tier0_static/ops_dead_code_test.go:32-37`) from four packages to a roster covering
`pkg/adapter/...`, `pkg/gateway/coordination/...`, `pkg/gateway/podlifecycle/...`,
`pkg/controller/sandbox/...`, `pkg/gateway/session/...`, and `pkg/credential/...`. The machinery already
counts non-test references only (`:92-129`) and already models the escape hatch
(`trackedUnwiredDeclarations`, `:62-67`).

Stage 2 is required, because stage 1 misses the flagship case. 6a.2 is a four-hop chain: `onHoldTimeout`
is called by `enterHoldState`, which is called by `onCoordinatorChannelClosed`
(`pkg/adapter/holdstate.go:91-97`), which is called by a `defer` inside the control-stream handler
(`pkg/adapter/controlchannel.go:125`), which no client opens. Every hop has a production reference, so
identifier counting passes all four today. Stage 2 replaces counting with a name closure rooted at the
`cmd/` mains. Use a standard-library `go/ast` name closure rather than `golang.org/x/tools/callgraph`:
`x/tools` is not a current direct dependency, and the name closure over-approximates reachability, so its
errors are missed dead code rather than false accusations, which is the right posture for a ratchet whose
credibility is what makes it survive.

Stage 3 is an assignment-and-value-flow check, and it exists because a name closure is blind to a defect in
the value a reachable reference carries. It reports a struct field that is read and never assigned, a flag
that is declared and threaded and never consumed, and a call site that passes a literal where a variable is
expected. Four rows of 6a.5 need it and nothing else in the plan supplies it. Stage 3 is deliberately
narrow: it analyses only the identifiers the register names, rather than the whole tree, because a general
value-flow analysis over a repository this size produces more false accusations than a ratchet survives.

**G2, wire bijections.** G2a asserts each proto RPC has both a client method and a handler. G2b asserts a
three-way bijection between the `spec/04:679-690` control-event table (8 rows, a well-formed markdown
table), the constants at `pkg/adapter/controlchannel.go:18-57` (8 constants), and a consumer. G2b carries
a named trap: the only `AUTH_EXPIRED` and `RATE_LIMITED` hits in `pkg/gateway` are the unrelated HTTP 429
envelope at `pkg/gateway/middleware/ratelimit/ratelimit.go:510`, so the consumer check must resolve the
constant identifier through type information rather than the string literal, or it reports a false
consumer for two of eight events. Write the trap into the test's header. G2c asserts a
collector-to-catalog-to-scrape-path triple: nine of ten adapter collectors are absent from
`pkg/observability/metrics/catalog.go` so the existing "no unspecified metrics" check cannot see them, and
the adapter serves no metrics endpoint at all.

**Eight rows of 6a.5 that G1 structurally cannot cover.** A name closure rooted at the `cmd/` mains reports
a symbol reachable as soon as any production reference exists, so it is blind to a defect in the value that
reference carries. Ten of the eighteen rows are ordinary unreferenced-symbol cases G1 catches. The other
eight need a different detector, and the register records which:

- **`Adapter/Interrupt` hard mode.** The call site is reachable; the defect is the literal argument
  (`pkg/gateway/sessionserver/interrupt.go:134` passes `false`). Needs an argument-value check.
- **`Adapter/Terminate` on the session paths.** The client method has a production caller
  (`cmd/lenny-gateway/user_revocation.go:129`); the defect is which paths call it. Needs a per-route
  assertion, which R10 supplies.
- **`Options.InterReplicaAddress`.** Read at
  `pkg/gateway/coordination/coordination/coordination.go:573`; never assigned. Needs an assignment check.
- **The LLM-proxy SPIFFE lease binding.** `PodSpiffeURI` is referenced and never set. Same detector.
- **`maxSuspendedPodHoldSeconds`.** Declared and threaded at `cmd/lenny-gateway/controlserver.go:229`; no
  sweep reads it. Same detector.
- **OTLP trace export.** `cmd/lenny-adapter/main.go:208` is reachable; the podspec never sets the
  environment variable. This is G3a's, and it belongs to the deployment-boundary class rather than this
  one.
- **`git-credential-lenny`.** Never built into the image, so it is inside no call graph. Needs a tier-5
  image assertion.
- **The egress-capture sidecar default.** A values-file entry. Needs a podspec render case.

Stage 3 above is the detector for the four value-defect rows. Do not claim G1's name closure covers all
eighteen rows of 6a.5.

**Dependencies.** R1b and R5, because the tracked register is keyed to symbol and package names those two
move. R3, because without change-graph completeness a violation fires in CI rather than on the developer's
machine.

**Parallel with.** Every wave-3a step. Not R23, which drains R9's register and therefore follows it in
wave 3b. R9 writes only `tests/tier0_static/production_reachability_test.go`,
`tests/tier3_contract/wire_bijection/`, `pkg/observability/metrics/catalog_test.go`, and the register
files, and it reads the packages it analyses rather than editing them. R9 is a co-requisite of R12 that
defines its exit condition rather than a build blocker; making it a hard dependency inflates the critical
path for no correctness gain.

**Risk.** False positives on reflection and interface dispatch. The register with per-entry reasons is the
escape hatch, and each entry is countable rather than anonymous.

**Size.** M.

### R10. Coordinator gate on the session REST surface, and the duplicate terminal

**Closes:** 6c.1 and 8.7. Retires the whole of reference §5.2. Executes the `Adapter/Terminate` row of
6a.5, whose defect is that the session terminate route never reaches the pod.

**Scope correction.** The planning input scoped this as "seven handlers". Counting session-scoped mutating
routes at `pkg/gateway/sessionserver/sessionserver.go:2038-2104` gives twenty: `DELETE /{id}`, `finalize`,
`start`, `interrupt`, `terminate`, `resume`, `derive`, `replay`, `extend-retention`, `eval`,
`POST memory`, `DELETE memory/{memoryId}`, `upload`, `upload-archive`, `upload-to-session`, `messages`,
`tool-use/{toolCallId}/approve`, `tool-use/{toolCallId}/deny`, `elicitations/{id}/respond`, and
`elicitations/{id}/dismiss`. Exactly one gates today (`upload-to-session`, at
`pkg/gateway/sessionserver/upload_to_session.go:113-124`, returning 409 `TARGET_NOT_READY`). Twenty routes
is consistent with reference §5.2's 25-row matrix; seven is not. This matters beyond estimation, because
R10's non-collision with R12, R13, R15, and R20 was argued from a seven-handler footprint that does not
exist.

**The `Adapter/Terminate` row of 6a.5 is a work item in this step.** The client method exists
(`adapterclient/client.go:927`) and its only production caller is
`cmd/lenny-gateway/user_revocation.go:129`, so a user-initiated session terminate transitions the row and
never tells the pod. Gating the route on the coordinator does not by itself change that. R10 therefore
adds one behavior: on the replica that holds the binding, `POST /v1/sessions/{id}/terminate` invokes
`Adapter/Terminate` against the bound pod before the row transition, with the same deadline and
best-effort error handling `user_revocation.go` uses.

**R10 splits into two merge units.** R10a is the gate wrapper plus the routes whose handlers live in files
no other step touches. R10b is the routes in `messages.go` and `interactions.go`, and it follows R20 on
those two files. Section 7.2 and the coverage matrix name R10 as the record owner; R10a and R10b are its
merge units and appear as separate nodes only in the sequencing artifacts.

**8.7, the duplicate terminal.** The orphan-session reconciler is launched ungated at
`cmd/lenny-gateway/workers.go:1088`, and its post-condition at `orphansession.go:356-360` passes for a row
a peer already failed, so both replicas reach `OnSessionTerminal` and sink idempotency is unverified. Same
defect class as 6c.1, same harness, and it edits `workers.go`, which R5 carves.

**Spec.** §29's off-holder matrix states, per route, the required off-holder behavior and the typed
refusal carrying coordinator identity, and it is the normative home of the gate. The client-to-gateway
session REST surface is not a channel in the §28 register, so no §28 card owns it. The reconciler's leader
gating and the terminal callback's idempotency requirement become normative in the same matrix.

**Tests.** Reference §5.2 as a table-driven suite on R7's two-replica harness: each session-scoped mutating
route exercised on a non-coordinator replica, asserting the documented outcome. A tier-4 assertion that a
holder-side terminate is observed as an `Adapter/Terminate` call at the pod. A duplicate-terminal case
driving both replicas at once. A new-route coverage ratchet, so the next route added without a gate fails.

**Dependencies.** R5 (the handler split, which now includes `messages.go` and `interactions.go`), R7 (the
only way to verify any of this), and R2a (the off-holder matrix is the normative contract this
implements).

**Parallel with.** R11, R13, R14, R15, and R23. Not parallel with R20 (shared files, resolved by the
R10a/R10b split), and not parallel with R22, which rewrites the resume path this step gates.

**Risk.** Failing closed on twenty routes is a visible availability change. It ships with the typed refusal
and a clear operator-facing message, and §29's matrix names R19 as the successor so the interim behavior
is documented as interim.

**Size.** L.

### R11. LLM proxy dialects reach a route

**Closes:** 6c.8.

**Scope.** `credential.ProxyDialect` admits `anthropic`, `openai`, `google`, and `cursor`
(`pkg/credential/lease.go:58-91`), and the gateway registers exactly one route,
`POST /llm-proxy/v1/messages` (`cmd/lenny-gateway/main.go:475`), so a pool minted with any other dialect
produces a 404 from the mux and nothing validates the dialect at mint time. Either register the routes or
narrow the admitted set. The §28.5.5 card enumerates the served dialects, and the admitted set in code
derives from or is asserted against it, so the two cannot drift again.

**Tests.** Tier 3 bijection between the admitted dialect enum and the registered mux routes. Tier 1 on
mint-time rejection.

**Dependencies.** R2a for the card. R5, because the route registration site is in the composition root
that R5 restructures. The planning input stated "collides with nothing" while its own code scope named
"route registration in the carved composition root", which presupposes R5.

**Parallel with.** Every other wave-3 step, given the R5 edge.

**Risk.** Minimal. The only judgement is which direction to take, and the card forces it to be recorded.

**Size.** S.

### R12. The adapter-to-gateway control direction, with hold state

**Closes:** 6a.1 (both sides), 6a.2's arming and consumption sides, including the concurrent-slot case and
the `--post-mortem-dir` render that makes the post-mortem writable, 6b.4, 6b.5, 6b.8, and the transport
and consumer rows of 6a.4. Executes the OTLP row of 6a.5. R6 owns the G3a register rows that keep the two
deployment-boundary halves from recurring.

**Why hold state is bundled rather than split.** This is the sharpest correction in the planning material
and it is confirmed. `pkg/adapter/holdstate.go:91-97` shows `onCoordinatorChannelClosed` arming hold state
on any stream close, with no distinction between coordinator loss and an ordinary gateway rolling restart,
and the arm is a bare `defer` at `pkg/adapter/controlchannel.go:125`. It is unreachable today only because
no client opens the stream. Shipping the client alone arms hold state on every pod during every gateway
restart, and each pod self-terminates at the 120-second timeout (`holdstate.go:122`, default at `:23`).
The client and a correct entry condition land together.

**A second correction: the concurrent-slot case.** `onCoordinatorChannelClosed` returns early when the
pod-global session id is empty, which is always the case on a concurrent-slot pod (reference 6a.2, final
paragraph). Without a per-slot hold state, 6a.2 would be recorded closed with hold state permanently
unarmable on every multi-slot pod, which is the deployment mode where coordinator loss is most likely,
because one pod's slots fan out across replicas. Per-slot hold state is an R12 exit criterion, and R7 is
therefore an R12 dependency.

**Code.** A new `pkg/gateway/runtime` stream-owner package. The stream-open method on
`pkg/gateway/runtime/adapterclient`. Per-event fan-out into the credential fallback path, the delegation
budget path, and the session failure path. The adapter metrics endpoint, the catalog entries, and the
metric rename deferred from R1a. A one-owner-per-pod rule, reconnect, and `FailedPrecondition`
arbitration. The podspec renders `--post-mortem-dir` and `OTEL_EXPORTER_OTLP_ENDPOINT` on the adapter
container, in the podspec builder R6 split out, which discharges the post-mortem half of 6a.2 and the OTLP
row of 6a.5 and clears their two G3a register rows. Arming hold state without rendering the post-mortem
directory would make the timeout path reachable and its only durable output still undeliverable.

**Spec.** §28.5.2 becomes normative: per-event delivery guarantee, reconnect and re-registration
semantics, one-stream-per-pod ownership, the 64-event buffer overflow drop the specification does not
acknowledge, and the hold-state entry and exit conditions. Correct `spec/10_gateway-internals.md:47`,
which makes hold-state entry a consequence of gRPC transport-layer detection within roughly fifteen
seconds while the code makes it a consequence of an application-stream close. Reconcile the barrier
acknowledgement surface (6b.8): declare the unary `CheckpointBarrierResponse` normative, correct
`spec/04:656`, `spec/04:687`, and `spec/10:167`, which all place `CheckpointBarrierAck` on the control
stream, and either wire the control-stream emit at `pkg/adapter/coordination.go:282` to a real consumer or
delete it, since it drops unconditionally today. R1b corrects the inverse claim in the proto doc comment;
R12 corrects the specification and settles the emit.

**Tests.** Tier 3 on the stream contract including reconnect and arbitration. Tier 3 asserting that the
barrier acknowledgement surface the specification names is the one the gateway consumes. Tier 4 on each
consumer's effect. Tier 4 asserting the rendered adapter container carries both `--post-mortem-dir` and
`OTEL_EXPORTER_OTLP_ENDPOINT`. Tier 7a on hold-state entry and exit under a simulated rolling restart,
with a concurrent-slot row.
Two existing tests must be retired rather than kept, because they codify the gap as intended behavior and
will assert the gap is correct after it closes: `TestControlEventDroppedWhenNoStream_spec_4_7`
(`pkg/adapter/controlchannel_test.go:264`) and, in R16, `TestEvictingFlag_spec_4_6_1` (`:250`).

**Dependencies.** R1b (final wire names and the extracted event envelope), R2a (§28.5.2 is the contract),
R5 (composition-root wiring), R6 (the podspec builder split, so the metrics port and the OTLP environment
variable land without colliding), and R7 (the concurrent-slot dimension). R9 is a co-requisite defining
the exit condition.

**Parallel with.** The wave-3b steps R8, R14, R20, and R23, since R12 can open as soon as R6 merges. R13
and R11 have merged from wave 3a by then. The shared surface with R11, R13, and R20 is the composition
root, where all four append into per-concern files after R5.

**Risk.** The highest behavior-change surface in the plan. The rolling-restart hazard is the named one and
is why hold state is in scope. Secondary: the one-owner-per-pod rule must be correct or two replicas fight
for the stream.

**Size.** L.

### R13. Bind-time coordinator resolution

**Closes:** 6b.7. Executes the `coordlease.GetBySession` and `Options.InterReplicaAddress` rows of 6a.5.

**Scope.** `spec/10_gateway-internals.md:167` and `spec/04_system-components.md:489` both state that the
session server seeds the mirror row at bind with the binding replica as the initial coordinator, on both
the session-start and the snapshot-resume rebind paths. The only writer is the sweeper
(`pkg/gateway/coordination/coordination/coordination.go:569`), and `pkg/gateway/sessionserver` never
references `coordlease`. `Options.InterReplicaAddress` is declared, consumed at `coordination.go:573`, and
populated by nobody, because `NewSweeper` is built without it at `cmd/lenny-gateway/stores.go:1489-1498`.

**One deferral inside this step.** What a valid inter-replica address is (pod IP plus port, headless
service DNS, or a SPIFFE-bearing authority) is determined by R18's service definition and its
NetworkPolicy. R13 therefore lands the at-bind seed and the routing read against an address whose format
is an explicit output of an R18 design note that lands first. The alternative is to move address
population into R18. Either is acceptable; the plan requires that the edge be recorded rather than
discovered.

**Tests.** Tier 4 on both bind paths seeding the row. Tier 2 on the routing read.

**Dependencies.** R5 (the bind and lease helper split, without which this collides with R10 on one
4,472-line file), R2a (the `REG-COORDMIRROR` register defines the writer and reader sets), and an R18
design note for the address format.

**Parallel with.** R10, R11, R12, R14, R15, and R20.

**Risk.** Low, given the address-format edge is honored.

**Size.** M.

### R14. Agent-pod mTLS client identity

**Closes:** 6c.7 (verdict owner; R6 owns the chart half and R7 the defaults half), 8.4a, and the three
security rows of 6a.5.

**Scope.** The podspec emits no TLS flags and mounts no certificate material on either container, and
`podVolumes()` contains no certificate volume. `charts/lenny/templates/mtls-pki.yaml:24-27` states that
per-pod certificates are issued at pod-creation time and are intentionally absent as static chart
resources, and no such producer exists: `pkg/controller/warmpool/pod_reconciler.go:41-49` calls the
certificate annotation "the forward path for a per-pod cert producer", and
`cmd/lenny-controller/flags.go:197` defaults the corresponding enforcement to false for the same reason.
The chart default is `mtls.enabled: true` (`charts/lenny/values.yaml:1956`), so in a default install the
agent pod dials the gateway's `GatewayControl` listener in plaintext while that listener requires a
verified client certificate, and every platform-tool, connector-tool, and scrub-report call fails.

**The latent second defect.** `GatewayDNSName` is hardcoded to the `lenny-system` namespace
(`pkg/adapter/gatewaycontrol/gatewaycontrol.go:36`) while the controller stamps a namespace-parameterized
target, so a release installed elsewhere pins a SAN the gateway certificate does not carry. It becomes
live the moment agent-pod certificates are wired, so it is in scope here.

**Three security rows moved in from R24.** `Adapter/RevokeCredentials`, the LLM-proxy SPIFFE lease binding,
and the service-account token on `GatewayControl` are security controls rather than dispositional
questions. Upstream lease revocation stops future minting and cannot remove material already written to
`/run/lenny/credentials.json`, the gateway-side proxy deny list does not cover direct mode, and the
admission webhook rejects `spiffeBinding: disabled` on a cross-pod-replay rationale
(`pkg/admission/direct_mode_isolation/guard.go:149-154`) that the never-populated `PodSpiffeURI` never
delivers. Each gets a tier-9 exit test.

**8.4a.** NetworkPolicy behavior under a live CNI for the existing pod-egress and `GatewayControl`
policies. Reference §8 scopes this item to sections 3.17 and 6c.7, and 6c.7 is this step, so assigning the
whole of 8.4 to R18 would leave this consequence chain resting on an unverified reading.

**Tests.** Tier 9 mTLS, with "the tier-9 mTLS suite no longer skips" as the exit criterion. That suite
self-skips today at `tests/tier9_security/tls_test.go:84` because the e2e overlay disables the PKI, which
is why R7 is a dependency rather than a nicety.

**Dependencies.** R6 (per-pod certificate volumes are a podspec builder change) and R7 (default-values
parity and the stock-defaults lane, without which this lands green against a harness that never exercises
it).

**Parallel with.** R10, R11, R13, R15, and R23. Not parallel with R17, since both exercise the
`GatewayControl` handshake.

**Risk.** Turning on client certificates in a default install is a visible change with a rollback path
through the values file. The namespace defect must land in the same change, or one broken install mode is
converted into another.

**Size.** L.

### R15. Runtime-frame schema conformance

**Closes:** 6b.15 (all seven rows), and the frame-and-emitter third of 6b.11.

**Scope.** `schemas/runtime-ops-events.schema.json`, named `schemas/lifecycle-events.schema.json` before
R1b, is stricter than the emitter, and nothing validates
emitted bytes against it. Every frame shares one Go struct in which each field is `omitempty`
(`pkg/adapter/lifecyclechannel.go:59-83`, `pkg/adapter/runtimeops.go` after R1b), so a zero value is
omitted where the schema requires it. The seven divergences are enumerated in reference 6b.15 and each
gets a row in the coverage matrix. Tier 0 today validates only the static example fixtures
(`tests/tier0_static/schemas_test.go:146-172`), and three schema members have no example fixture at all:
`credentials_acknowledged`, `llm_request_started`, and `files_updated`.

**6b.11's first third.** `ready_for_input` exists in no schema (`grep -rn "ready_for_input" schemas/`
returns nothing). The frame and the emitter belong here, with `CH-RUNTIMEOPS` as their home. The gateway
consumer and the configurable 30-second `delivered` receipt timeout belong to R20, and enablement belongs
to R17. The planning input assigned 6b.11 whole to R17, which is scoped as enablement and cannot define a
frame or a delivery timeout.

**Tests.** A schema-member bijection at tier 0 (every schema member has a fixture, and every emitted frame
type has a schema member) and an emitter round-trip at tier 1 that validates emitted bytes rather than
fixtures.

**Dependencies.** R1b, because the schema file, the socket, and the manifest key all move.

**Parallel with.** R10, R11, R13, R14, and R23. R15 touches `pkg/adapter/runtimeops.go` and
`pkg/adapter/lifecycle.go`, both adapter handler files, so it must have merged before any later proto
window opens (serialization rule S-2).

**Risk.** Low. Making an emitter match a schema is bounded work.

**Size.** M.

### R16. Eviction, the holder path

**Closes:** 6a.6, 6b.2, 6b.3, 6b.13, the enforcement half of 6b.1, and the producer,
best-effort-eviction-snapshot, and preStop rows of 6a.4. Verdict owner for 6a.4, whose closure
additionally requires R6, R12, R13, and R18. R16 also settles the three kubelet-path SIGTERM comments
R1a left in place, either by making them true or by deleting them, and clears their claim-register rows.

**Scope.** The adapter emits `AdapterEvicting` on kubelet-driven termination over `CH-EVENTSTREAM`. The
preStop hook drives a checkpoint (`cmd/lenny-adapter/prestop.go:68-94` contains no checkpoint logic
today). `setEvicting` gains a production caller, which arms the two best-effort branches at
`pkg/adapter/checkpoint.go:179` and `:192`. The replica holding the binding drives
`Checkpointer.CheckpointWithTrigger` with `TriggerEviction`. Generation validation on the operational RPCs
uses the field R1b added.

**Why the holder path is separated from the off-holder leg.** Proposal 0062's own revision text records
that under 0060's co-location "the driver leg collapses to the binding-gated
`Checkpointer.CheckpointWithTrigger`, the forward leg survives on the concurrent-slot case alone". R16
ships the holder path with no inter-replica transport, and R21 adds the off-holder leg on R18.

**Tests.** A tier-5 test that terminates a pod and asserts a checkpoint, which no test does today. A
tier-8 eviction-edge matrix over the eleven rows of reference §7.3. Retire `TestEvictingFlag_spec_4_6_1`
(`pkg/adapter/controlchannel_test.go:250`), today the only caller of `setEvicting`.

**Dependencies.** R12 (the transport `AdapterEvicting` rides), R4's 8.3 verdict before the design commits,
since R16 reorders exactly the `GracefulStop` call 8.3 investigates, and R6 (the PDB and admission rows of
6a.4 must be in place or a busy pod remains evictable regardless).

**Parallel with.** R18 and R22. Not parallel with R17 (S-5), and not parallel with R21, which extends it.

**Risk.** This is where a wrong shutdown budget turns a best-effort checkpoint into a hang. R4 is the
control.

**Size.** L.

### R17. Enabling `CH-RUNTIMEOPS` at the deployment boundary

**Closes:** 6a.3, 6b.12, and the enablement third of 6b.11. Carries 8.2's compatibility note and 8.6's
assertion surface.

**The ordering, and why it is the reverse of the intuitive one.** Enabling the channel while nothing arms
the evicting flag introduces a failure rather than removing one. The whole cooperative quiesce block sits
inside `if s.Lifecycle != nil` (`pkg/adapter/checkpoint.go:159`), and both escape branches are gated on
`s.isEvicting()` (`:179`, `:192`). With the channel off, an eviction checkpoint already falls through to
`streamChunks` and archives the workspace. With the channel on and no production `setEvicting` caller,
`RequestCheckpoint` blocks against a runtime the sidecar SIGTERMed at t=0 and returns
`Internal "checkpoint quiesce handshake"` at `:196`. R17 therefore depends on R16 (serialization rule
S-5).

**R16 does not discharge the whole hazard, and this is an addition to the planning material.** R16 arms
`setEvicting` on the pod's own SIGTERM or eviction. It does not cover a pod that is alive with a dead
runtime, and that case is reachable today: the heartbeat-hung path at `pkg/adapter/attach.go:141-147`
SIGTERMs the runtime and ends the `Attach` stream while the pod keeps running. Today a subsequent periodic
checkpoint on that pod skips the whole block and archives the workspace. After R17, with
`RuntimeConnected()` false and `isEvicting()` false, it fails closed. R17 must therefore add an explicit
`!RuntimeConnected()` best-effort branch independent of `isEvicting()`, with a tier-1 case for the
heartbeat-hung-then-periodic-checkpoint sequence, and the §28 card must record that the eviction gate and
the runtime-liveness gate are separate conditions.

**6a.3's silent-billing consequence needs a named test.** `WireDirectModeUsage` is called unconditionally
(`cmd/lenny-adapter/main.go:394`) so `ReportUsage` no longer returns unimplemented, and its only feed is
the `llm_request_completed` frame (`pkg/adapter/usage.go:248`), so every direct-mode usage pull returns
zero. That is silent billing loss. `TestDirectModeUsagePullIsNonZero`, tier 4.

**Dependencies.** R16 (S-5), R15 (the frames must match the schema before they carry production traffic),
R6 (the podspec must render the socket flag), and R8 (the host-conformance battery is the only mechanism
that establishes 8.6).

**Parallel with.** R19 and R21. Not parallel with R14 (both exercise the `GatewayControl` handshake), and
not parallel with R16.

**Risk.** This step changes what a rendered pod does at every checkpoint. The two gates above are the
control, and the tier-8 eviction-edge matrix from R16 is the regression net.

**Size.** L.

### R18. The inter-replica transport

**Closes:** 6b.6, 8.4b, and the transport layer of 6c.2 and 6c.5.

**Scope.** A new `schemas/lenny-gateway-interreplica.proto` service, a dedicated gateway inter-replica
port, a NetworkPolicy self-peer admitting the flow, and the address format R13 depends on.

**The NetworkPolicy is explicitly in scope and explicitly owned here.** `charts/lenny/templates/` contains
`agent-network-policies.yaml`, `self-managed-backend-network-policies.yaml`, and
`gateway-llm-upstream-egress.yaml`, and no gateway-to-gateway policy. 6c.2 states the absence has three
layers and the missing policy is one of them, and 6b.6 restates it. Being a chart-touching step, R18 falls
under serialization rule S-4.

**Serialization exemption.** The new proto is a new file with its own `go_package` and generated package,
so it does not collide with `schemas/lenny-adapter.pb.go` and needs no `schemas/buf.gen.yaml` change. Buf
configuration verified at `./buf.yaml` and `./schemas/buf.gen.yaml`.

**Tests.** Tier 3 on the service contract. Tier 5 on the NetworkPolicy under a live CNI, which is 8.4b.

**Dependencies.** R7 (verification requires two replicas) and R5 (the composition root).

**Parallel with.** R16 and R22. Independent of the adapter chain, which is what lets the largest new
subsystem be built alongside the largest existing-code chain.

**Risk.** A new listening port on the gateway is a security surface. Tier 9 coverage is not optional here.

**Size.** L.

### R19. Cross-replica message forwarding

**Closes:** 6b.10 and the routing layer of 6c.2.

**Scope.** `spec/07_session-lifecycle.md:330` states that a `delivery: immediate` message landing on a
non-coordinator replica is forwarded to the coordinator, and that an unreachable coordinator degrades to
inbox buffering with a `queued` receipt. Neither branch exists. Build `ForwardMessage` on R18's transport,
route by the coordinator recorded in `REG-COORDMIRROR`, and degrade to the inbox that R20 made
redeliverable. R19 is the successor R10's interim typed refusal names.

**Dependencies.** R18 (the transport), R13 (the mirror must carry a resolvable coordinator and address),
R20 (an inbox degrade path that drops messages is not a degrade path), and R7.

**Parallel with.** R17 and R21.

**Risk.** Forwarding introduces a second hop on the hottest path in the product. The latency budget and
circuit-breaking belong in the §28 card.

**Size.** L.

### R20. Message-plane durability and cross-replica input wait

**Closes:** 6c.3, 6c.4, and the consumer-and-timeout third of 6b.11.

**Scope.** The session inbox is write-only: the only two callers of `inbox.Drain` are `MigrateInboxToDLQ`
(`pkg/gateway/session/sessioninbox/coordinator.go:121`) and `DrainOnTerminal` (`:201`), so every 200
response carrying a `queued` receipt is a terminal drop rather than a deferral. Build redelivery.
Separately, tool approval polls a shared Postgres-backed store every 25 milliseconds so a resolution
landing on a non-coordinator wakes the blocked executor, and the request-input half has no shared store at
all; give `inputwait` the same treatment. Add the `ready_for_input` consumer and the configurable
30-second `delivered` receipt timeout that `spec/07:320` defines.

**A verbatim in-repo template exists** for the cross-replica case:
`tests/tier7a_load_local/toolapproval_xreplica/xreplica_wake_test.go`, including its `approvalTimeout` and
`wakeBudget` structure. 8.5's elicitation question folds in as a third case.

**Dependencies.** R4's 8.5 answer before the design commits, because a negative answer means a second
cross-replica store rather than a reuse. R5 (the `messages.go` and `interactions.go` split), R7, and R15
(the `ready_for_input` frame).

**Parallel with.** R11, R13, and R14. Sequenced against R10b on the shared handler files.

**Risk.** Redelivery introduces duplicate-delivery semantics that the §28 card must state and a test must
pin.

**Size.** L.

### R21. The off-holder eviction leg

**Closes:** 6c.5. The inter-replica control-forward row of 6a.4 is R18's to close, since its recorded
absence is the missing gateway-to-gateway service and the missing NetworkPolicy. R21 is that transport's
first consumer and adds no 6a.4 row of its own.

**Scope.** `spec/04_system-components.md:489` requires the coordinator to drive against a connection dialed
to the pod when it does not already hold the session's binding. No such capability exists: the barrier
dispatcher is connection-only by deliberate removal (commit `21032008`, rationale at
`pkg/gateway/coordination/barrier/wiring.go:29-31`), and the one remaining pod-dial path is
`Binder.ReadoptConnect`, reached only from crash-takeover re-adopt. Under 0060 co-location this leg
survives on the concurrent-slot case alone, which is why it is separated from R16.

**Dependencies.** R18 (the transport), R16 (the holder path and the drive itself), R13 (a resolvable
coordinator address), and R7 (the concurrent-slot dimension).

**Parallel with.** R17 and R19.

**Risk.** This is the path where two replicas can drive one pod. The exclusivity guarantee in the §28.5.4
card must be enforced rather than documented.

**Size.** M.

### R22. Concurrent-slot resume and restore

**Closes:** 6c.6 and the handler-and-caller layers of 6b.9.

**Scope.** A concurrent-session pool can be checkpointed and cannot be restored onto a concurrent pod. The
slot-aware handlers gate on a pod-global session id the slot-start path never sets. The proto fields
landed in R1b; this step makes the handlers read them and makes the four gateway call sites populate them.
Adding a field nothing populates closes nothing, so the gateway callers are explicitly in scope here and
6b.9 stays open until they land.

**Dependencies.** R1b (the fields), R7 (the concurrent-slot harness), and R5 (the resume path lives in the
carved `start.go`).

**Parallel with.** R16 and R18. Not parallel with R10, which gates the resume route this step rewrites.

**Risk.** Restore correctness on a shared pod is the highest data-loss surface in the plan.

**Size.** L.

### R23. Pre-connect and SDK-warm at the deployment boundary

**Closes:** 6a.7, and the `Server.PreConnect` row of 6a.5.

**Scope.** A `preConnect: true` pool cannot start a session against the shipped podspec. The gateway half
is reachable and data-driven; the pod half returns `Unimplemented` for both SDK-warm RPCs in every
chart-rendered pod, and there is no fallback on the Launch leg, so `binder.go:993-994` calls `reclaim()`
and the bind fails. The handler's own doc comment at `pkg/adapter/sdkwarm.go:197-198` says the gateway
runs the `DemoteSDK` fallback; it does not, and `DemoteSDK` would return `Unimplemented` for the same
reason if it did. Either wire `Server.PreConnect` into the production sidecar or make the gateway fall
back; the §28 card records which.

**Dependencies.** R6 (the deployment boundary) and R9 (the register row this drains).

**Parallel with.** Every wave-3b step, and wave 4. Not R6 or R9, which it depends on. This step is off the
critical path, which is a deliberate
correction: the planning input placed R23 on the stated critical path and simultaneously listed it as a
safe compression, and both cannot be true. It is a leaf.

**Risk.** Low.

**Size.** M.

### R24. Disposition of the remaining unwired surfaces

**Closes:** the remaining rows of 6a.5, each with a recorded verdict.

**Scope.** Drain R9's register row by row. Each row gets one of three verdicts: wire it, delete it, or
record it as intentional with an owner and an expiry. The rows are cited by name rather than by index,
because the index numbering used across the planning inputs does not match the reference table's order
(the table's first four rows are `Adapter/SendMessage`, `Adapter/RevokeCredentials`, `Adapter/Interrupt`
hard mode, and `Adapter/Terminate`, and the inputs variously called row 3 the service-account token and
row 5 `Server.PreConnect`).

The three security rows moved to R14 are out of scope here. The remaining rows are the hot routing cache,
`RedisRelay.LiveFromCursor`, `maxSuspendedPodHoldSeconds`, the embedded SIGSTOP checkpoint,
`Checkpointer.JitterFraction` and `FirstCheckpointDelay`, `Checkpointer.Deadline` and
`checkpoint.CheckpointTimeout`, `Adapter/SendMessage`, `Adapter/Interrupt` hard mode,
`git-credential-lenny`, and the egress-capture sidecar default.

**Dependencies.** R9 (the register). Scheduled row by row against the register, and only where the row's
file is not in flight, rather than as one change.

**Parallel with.** Everything after R9 and before R25, subject to the file-in-flight rule.

**Risk.** Low individually, wide collectively.

**Size.** M, spread thin.

### R25. Closure and reconciliation

**Closes:** 8.1, and it is the standing guard against every closure in this plan going stale.

**Scope.** The communication-status reconciliation test: for each channel labelled `UNWIRED` or `ABSENT`,
assert the named production caller is still absent and fail when one appears; for each `WIRED` channel,
assert the named caller symbol exists. The label then goes stale only in the safe direction. Seed cases are
the event-stream handler at `pkg/adapter/controlchannel.go:108` with no gateway client, and the sole
hold-state arm at `:125`. This test should be built early with its two seed cases even though it is
populated late.

Also in scope: a suite-wide sweep for tests that codify a gap as intended behavior, which is the pattern
`TestControlEventDroppedWhenNoStream_spec_4_7` and `TestEvictingFlag_spec_4_6_1` exhibit; a tier-11 change
converting the roughly forty reconciliation files that grep source as text away from wiring certification,
which runs after R2b's freeze and therefore writes anchor citations only, never a `§X line N` form,
since that is exactly what gave the dead eviction route a green light
(`eviction_coordinator_route_consistency_test.go:97-108`, four substring probes, all present, all
unreachable); and running every tier, which is 8.1's actual discharge.

**Dependencies.** R9, R24, and every capability step.

**Parallel with.** Nothing. Terminal.

**Risk.** If this step is dropped, the plan's closures are assertions.

**Size.** M.

---

## 6. Sequencing

### 6.1 Dependency graph

Each node lists its own predecessors, because the edges do not form a clean trunk and a trunk drawing
invents edges that section 6.2 does not record.

```
  t=0   R3  tooling                    no prerequisites, runs to completion
        R4  investigations 8.3, 8.5    no prerequisites, answers feed R16 and R20
        R7  multi-replica harness      no prerequisites, stays available to every later wave

  W1    R1a  naming law, registers, prose      [EXCLUSIVE TREE FREEZE, S-1]
             from R0

  W2    R1b  wire change  [S-2]        from R1a
        R2a  spec 28 and 29            from R1a, R3
        R5   composition-root carve    from R1a

  W3a   R6   podspec and G3a           from R1a, R1b
        R9   reachability gates        from R1b, R5, R3
        R10a coordinator gate          from R5, R7, R2a
        R11  llm proxy dialects        from R2a, R5
        R13  bind-time resolution      from R5, R2a, R18 design note
        R15  frame conformance         from R1b

  W3b   R8   host conformance          from R1b, R6
        R14  agent-pod mTLS            from R6, R7
        R20  message plane             from R4, R5, R7, R15
        R23  pre-connect               from R6, R9

  W4    R12  adapter to gateway        from R1b, R2a, R5, R6, R7; R9 co-requisite
        R10b gated message routes      from R10a, R20

  W5    R16  eviction, holder path     from R12, R4, R6
        R18  inter-replica transport   from R7, R5
        R22  slot resume               from R1b, R7, R5

  W6    R17  enable CH-RUNTIMEOPS      from R16, R15, R6, R8
        R19  cross-replica forward     from R18, R13, R20, R7
        R21  off-holder eviction       from R18, R16, R13, R7

  W7    R24  disposition, row by row   from R9; the drain overlaps waves 4 through 7

  W8    R2b  citation and heading surgery      [EXCLUSIVE TREE FREEZE, S-7]
             from R3, R2a, and every code-moving step

  W9    R25  closure and reconciliation        from R9, R24, and every capability step
```

<!--
ASCII fallback for the diagram above (dependency graph). Each line reads "predecessors ===> node".
Wave 0 (t=0, no prerequisites): R3 tooling, R4 investigations, R7 multi-replica harness. All three stay
available to every later wave.
Wave 1: R0 ===> R1a naming law, registers, and prose, alone on a quiesced tree (rule S-1).
Wave 2: R1a ===> R1b wire change (exclusive proto window, rule S-2). R1a and R3 ===> R2a new spec
sections. R1a ===> R5 composition-root carve-up. The three run in parallel.
Wave 3a: R1a and R1b ===> R6 podspec and the deployment-boundary gate. R1b, R5, and R3 ===> R9 gates.
R5, R7, and R2a ===> R10a coordinator gate. R2a and R5 ===> R11 llm proxy. R5, R2a, and an R18 design
note ===> R13 bind-time resolution. R1b ===> R15 frame conformance.
Wave 3b: R1b and R6 ===> R8 host conformance. R6 and R7 ===> R14 mtls. R4, R5, R7, and R15 ===> R20
message plane. R6 and R9 ===> R23 pre-connect.
Wave 4: R1b, R2a, R5, R6, and R7 ===> R12 adapter-to-gateway control direction with hold state, with R9 as
a co-requisite. R10a and R20 ===> R10b, the gated message and interaction routes.
Wave 5: R12, R4, and R6 ===> R16 eviction holder path. R7 and R5 ===> R18 inter-replica transport. R1b,
R7, and R5 ===> R22 slot resume.
Wave 6: R16, R15, R6, and R8 ===> R17 enablement. R18, R13, R20, and R7 ===> R19 forwarding. R18, R16,
R13, and R7 ===> R21 off-holder.
Wave 7: R9 ===> R24 disposition, row by row, whose drain overlaps waves 4 through 7.
Wave 8: R3, R2a, and every code-moving step ===> R2b citation and heading surgery, a second exclusive
tree freeze.
Wave 9: R9, R24, and every capability step ===> R25 closure and reconciliation. R2b has no successor edge;
rule S-7 places it before R25 in the wave ordering.
-->

### 6.2 Explicit edge list

| Step | Depends on | Reason |
|:--|:--|:--|
| R0 | none | Queue-only. Must precede R1a. |
| R1a | R0 | 0062 must be superseded before the tree is frozen. |
| R1b | R1a | Register names fix the wire names. |
| R2a | R1a, R3 | Every card names a channel by identifier, and R3 adds the `spec-map-exceptions` fields and reason class the §28 cards need. |
| R2b | R3, R2a, and every step that adds or moves Go code carrying a `// spec:` citation (R1a, R1b, R4, R5, R6, R10 through R24) | Mechanical citation rewrite runs after every file move, and it discharges R2a's anchor redirect map. |
| R3 | none | Tooling. |
| R4 | none | Read-only investigation. |
| R5 | R1a | The new per-concern file names must conform to the naming law. |
| R6 | R1a, R1b | The gate names adapter flags, and N7 renames one. |
| R7 | none | R3 is a soft co-requisite for the skip budget only. |
| R8 | R1b, R6 | Boots the rendered podspec with the renamed manifest key. |
| R9 | R1b, R5, R3 | Register keyed to post-rename and post-carve symbol names; needs local feedback. |
| R10a | R5, R7, R2a | Handler split, two-replica harness, and the normative matrix. |
| R10b | R10a, R20 | The gate wrapper, and the shared `messages.go` and `interactions.go` files R20 rewrites. |
| R11 | R2a, R5 | The card enumerates dialects; registration is in the composition root. |
| R12 | R1b, R2a, R5, R6, R7 | Wire names, contract, wiring, podspec, and the slot dimension. R9 co-requisite. |
| R13 | R5, R2a, R18 design note | Bind helper split, the register, and the address format. |
| R14 | R6, R7 | Certificate volumes and a harness that does not disable the PKI. |
| R15 | R1b | The schema file and socket move. |
| R16 | R12, R4, R6 | Transport, shutdown budget, and the PDB and admission rows. |
| R17 | R16, R15, R6, R8 | S-5, schema conformance, podspec rendering, and host conformance. |
| R18 | R7, R5 | Two replicas and the composition root. |
| R19 | R18, R13, R20, R7 | Transport, resolvable coordinator, a real degrade path, and verification. |
| R20 | R4 (8.5), R5, R7, R15 | Design input, file split, harness, and the frame. |
| R21 | R18, R16, R13, R7 | Transport, holder drive, address, and the slot dimension. |
| R22 | R1b, R7, R5 | Fields, harness, and the carved resume path. |
| R23 | R6, R9 | Deployment boundary and the register row. |
| R24 | R9 | The register is the work queue. |
| R25 | R9, R24, all capability steps | Terminal reconciliation. |

### 6.3 The critical path

`R1a -> R1b -> R12 -> R16 -> R17 -> R25`, six nodes and five links.

- **R1a gates everything** because every later step names a channel, and because it must land before
  proposal 0062 resumes.
- **R1b follows immediately** and carries the one wire change, so R12 builds the gateway consumer against
  final names. The planning input placed the proto step after R12, which would have renamed the RPC that
  R12 was just built against, paying exactly the bill R1's own rationale used to justify running first.
- **R12 is the transport** every adapter-to-gateway event needs.
- **R16 requires R4's `GracefulStop` verdict** before its design commits, since it reorders that call.
- **R17 requires R16** because arming the evicting flag is what prevents enablement from introducing a
  fail-closed checkpoint path, and R17 additionally adds the runtime-liveness gate R16 does not cover.
- **R25 is terminal.**

R2a, R5, and R7 are prerequisites of R12 and run concurrently with R1b. R6 also gates R12 and follows R1b,
because rule N7 renames a flag R6's gate enumerates, so R6 sits on a second five-link path
`R1a -> R1b -> R6 -> R12 -> R16 -> R17 -> R25` that is one node longer than the stated one. Treat R6 as
on the path rather than one link off it.

**R23 is a leaf.** The planning material placed it on the stated critical path and simultaneously listed
it as a safe compression. It hangs off R6 and R9 and nothing depends on it.

**R24 is a predecessor of the terminal node** and therefore not off the path, which is a correction to the
planning material and to an earlier reading of this plan. What is true is that R24 is a row-by-row drain
rather than one serialized node: its rows open as soon as R9's register exists in wave 3 and close against
whichever files are not in flight, so the drain overlaps waves 4 through 7 and only its tail sits on the
path.

**R2b has no successor edge** and is scheduled as a late exclusive freeze under rule S-7, for the reason in
section 4.3.

### 6.4 What runs in parallel, and why

Three off-path chains run wide alongside the critical path, and they are why the wall-clock cost is
tolerable.

- **Gates.** R3 from t=0, then R7, R8, and R9. R9 alone is a co-requisite rather than a build dependency,
  and it defines R12's exit condition. R7 is a hard dependency of eight capability steps (R10, R12, R14,
  R18, R19, R20, R21, and R22), and R8 is a hard dependency of R17, so neither is off the dependency graph
  even though neither closes a record.
- **Correctness.** R15 starts once R1b lands. R11 and R13 start once R2a and R5 land. R10 adds R7. R14
  adds R6 and R7. R20 adds R7 and R15. R23 adds R6 and R9. Between them they close three of the
  missing-capability records (6c.1, 6c.7, and 6c.8) before the critical path reaches R12.
- **Cross-replica.** R18 is independent of the adapter chain, which is what lets the largest new subsystem
  be built alongside the largest existing-code chain. Its two consumers are not: R19 waits on R20 and R21
  waits on R16.

Maximum useful concurrency is roughly six workers in wave 3a, four in wave 3b, and three in wave 5, against
two if R5, R6, and R7 were skipped. Compressing R4 or R5 is what makes the plan fail: R4's shutdown-budget
answer determines whether R16 has a time budget at all, and R5's carve-up is the precondition for every
wave-3 parallelism claim, without which R10, R13, and R20 serialize on one 4,472-line file and R22
serializes behind them in wave 5.

### 6.5 Serialization rules

Any reshuffling must preserve these.

- **S-1.** R1a is alone on a quiesced tree. Only R3's tooling half and R4's read-only investigation run
  alongside. The blast-radius matrix records the R1 rename as a whole, R1a's prose plus R1b's identifiers,
  as spanning `pkg/adapter`, `schemas/`, three runtime SDKs, `pkg/runtimekit`, eleven spec files, fourteen
  documentation pages, a diagram, and a chart test.
- **S-2.** Exactly one step (R1b) edits `schemas/lenny-adapter.proto` and runs `make generate-proto`. Its
  window is wave 2, before any step in waves 3 through 6 opens a `pkg/adapter` handler file, and no step
  in wave 2 or later may touch the proto or the generated `.pb.go` until R1b has merged. The covered files
  are `session.go`, `lifecycle.go`, `checkpoint.go`, `coordination.go`, `credentials.go`, `slotcreds.go`,
  `attach.go`, `sdkwarm.go`, and the two renamed channel files. Steps after R1b (R12, R15, R16, R17, R22,
  and R23) edit those handler bodies freely against the regenerated types. A later step needing a field the
  plan did not enumerate opens a second narrow window, and that window requires every in-flight
  `pkg/adapter` handler edit to have merged first.
- **S-3.** `schemas/lenny-gateway-interreplica.proto` is a new file with its own `go_package` and generated
  package, so R18 is exempt from S-2 and needs no `schemas/buf.gen.yaml` change.
- **S-4.** After R6, each podspec-touching or chart-touching step edits its own builder file and re-runs
  the generator on merge. Before R6, podspec-touching steps serialize. R18 is a chart-touching step.
- **S-5.** R15 and R16 both precede R17.
- **S-6.** Every step that regenerates a derived artifact re-runs the full derived set on merge, including
  `pkg/embedded/manifests/manifests.yaml`, which `tests/tier11_docs/embedded_manifests_sync_test.go`
  byte-diffs.
- **S-7.** R2b is a second exclusive tree freeze and runs after every code-moving step has merged, with one
  carve-out: R25's tier-11 conversion touches roughly forty reconciliation files after the freeze, so it
  writes anchor citations only and never a `§X line N` form. Everything else in R25 is additive.

### 6.6 Wave-by-wave execution order

| Wave | Steps | Notes |
|:--|:--|:--|
| 0 | R3, R4, R7 | No prerequisites. R4's 8.3 and 8.5 answers must be in hand before R16 and R20 design. |
| 1 | R1a | Exclusive tree freeze. R0 must have happened. |
| 2 | R1b, R2a, R5 | R1b holds the proto window. R7 continues. |
| 3a | R6, R9, R10a, R11, R13, R15 | Widest wave. Mutually disjoint once R1b and R5 have merged. |
| 3b | R8, R14, R20, R23 | Each consumes a wave-3a output: R8 and R14 and R23 need R6, R23 needs R9, and R20 needs R15. |
| 4 | R12, R10b | R12 needs R1b, R2a, R5, R6, and R7, and can open as soon as R6 merges. R10b follows R20 on `messages.go` and `interactions.go`. |
| 5 | R16, R18, R22 | R16 on the path; R18 and R22 alongside. |
| 6 | R17, R19, R21 | R17 on the path. |
| 7 | R24 | Row by row against R9's register. Rows open from wave 4 onward; wave 7 is where the drain closes. |
| 8 | R2b | Second exclusive tree freeze. |
| 9 | R25 | Terminal. |

**Safe compressions.** Pull R11, R23, and R24 into slack (all off-path and self-contained), and start
R25's reconciliation test early with its two seed cases.

---

## 7. Test and infrastructure strategy

### 7.1 Why most of these gaps are invisible to every behavioral tier

Three classes are invisible by construction, and no amount of added behavioral coverage reaches them.

| Class | Records | Why the tiers are blind | The mechanism that catches it |
|:--|:--|:--|:--|
| `UNWIRED` | 6a.1, 6a.2, 6a.6, 6a.7, 6b.3, 6b.13, the rows of 6a.5 the register enumerates as reachability defects, and the consumer halves of 6b.4 and 6b.8 | The test is the missing caller. `pkg/adapter/holdstate_test.go:111` calls `s.enterHoldState("s1")` directly, and `go test` cannot distinguish a double standing in for an expensive collaborator from one standing in for a nonexistent one. | Static wiring assertion (tier 0), or boot-surface observation (tier 4, real binary, no test-supplied caller) |
| `ABSENT` | 6b.1, 6b.2, 6b.5, 6b.6, 6b.7, 6b.10, 6b.11, and 6c.1 through 6c.8 | There is nothing to call. A missing test and a missing feature are the same absence, and the suite has no travel direction from a specification sentence to code. | Normative-claim register (`tests/claim-map.json`), tier-0 gated. The register is what makes the absence visible; the behavioral tier named per record in section 7.2 verifies each closure |
| Deployment boundary | 6a.2 (post-mortem half), 6a.3, 6b.12, 6c.7 (chart half), the OTLP row of 6a.5, and the PodDisruptionBudget and admission rows of 6a.4 | Every harness synthesizes the precondition. `cmd/lenny-compliance/full.go:98` writes the manifest itself, so Full-level conformance never sees the rendered podspec. | Flag-to-podspec bijection (tier 0) plus the reciprocal host-conformance battery (R8) |

### 7.2 Per-gap detection

Owner is the step that closes the record; detector is the mechanism that would have caught it and that
prevents recurrence.

| Record | Detector | Tier | Owner |
|:--|:--|:--|:--|
| 6a.1 | G1 reachability closure, G2a RPC bijection, and G2b event bijection | 0, 3 | R12 |
| 6a.2 | G1 transitive closure over the four-hop chain, and a tier-7a rolling restart with a concurrent-slot row | 0, 7a | R12 |
| 6a.3 | G3a flag-to-podspec bijection, and `TestDirectModeUsagePullIsNonZero` | 0, 4 | R17 |
| 6a.4 (11 rows) | G3a for the PDB and admission rows, G1 for the producer and transport rows, and a tier-5 terminate-and-checkpoint test | 0, 5 | R16, with R6, R12, R13, and R18 |
| 6a.5 (18 rows) | G1's name closure for 10 rows; G3a for the OTLP row; G1 stage 3, the assignment-and-value-flow check, for `Options.InterReplicaAddress`, the LLM-proxy SPIFFE binding, `maxSuspendedPodHoldSeconds`, and `Adapter/Interrupt` hard mode; a tier-4 per-route assertion for `Adapter/Terminate` on the session paths; a tier-5 image assertion for `git-credential-lenny`; and a podspec render case for the egress sidecar default | 0, 4, 5 | R24, with R14 for the three security rows and R10 for `Adapter/Terminate` |
| 6a.6 | G1, plus the tier-8 eviction-edge matrix | 0, 8 | R16 |
| 6a.7 | G1 plus a tier-5 `preConnect: true` pool start | 0, 5 | R23 |
| 6b.1 | G5 claim register, and a tier-3 proto field bijection | 0, 3 | R16, with R1b for the wire |
| 6b.2 | G5, and a tier-5 pod-termination checkpoint assertion | 0, 5 | R16 |
| 6b.3 | G1 and G2b | 0, 3 | R16 |
| 6b.4 | G2b three-way bijection with the constant-identifier trap | 3 | R12 |
| 6b.5 | G5, and tier-7a hold-state entry under transport loss | 0, 7a | R12 |
| 6b.6 | G5, and tier-5 NetworkPolicy under a live CNI | 0, 5 | R18 |
| 6b.7 | Tier-4 assertion that both bind paths seed the row | 4 | R13 |
| 6b.8 | G2b, plus a tier-3 assertion that the documented surface is the consumed one | 3 | R12 |
| 6b.9 | Tier-3 field bijection plus tier-4 slot-scoped handler cases | 3, 4 | R22, with R1b for the fields |
| 6b.10 | Off-holder matrix on the two-replica harness | 4 | R19 |
| 6b.11 | G7 schema-member bijection for the frame, and a tier-4 receipt-timeout case for the consumer | 0, 4 | R20, with R15 and R17 |
| 6b.12 | G3a and the host-conformance battery | 0, 2 | R17 |
| 6b.13 | G1 and the tier-8 eviction-edge matrix | 0, 8 | R16 |
| 6b.14 | G7 wire-artifact register derived from `ls schemas/**`, and tier-11 doc drift | 0, 11 | R2a, with R1b for the shipped JSONL description |
| 6b.15 (7 rows) | G7 schema-member bijection plus an emitter round-trip | 0, 1 | R15 |
| 6c.1 | Off-holder matrix over 20 routes, and G11 | 4, 0 | R10 |
| 6c.2 | Off-holder matrix plus tier-5 CNI | 4, 5 | R19, transport R18 |
| 6c.3 | Tier-4 redelivery case | 4 | R20 |
| 6c.4 | Tier-7a cross-replica wake, templated on `xreplica_wake_test.go` | 7a | R20 |
| 6c.5 | Tier-4 off-holder eviction drive | 4 | R21 |
| 6c.6 | Tier-4 restore onto a concurrent pod | 4 | R22 |
| 6c.7 | G6 default-values parity (the tier-9 suite must stop skipping), and tier-9 mTLS | 0, 9 | R14, with R6 and R7 |
| 6c.8 | Tier-3 dialect-to-route bijection | 3 | R11 |
| 8.1 | Full-tier run plus the `UNVERIFIED` verdict state | all | R25, with R3 as prerequisite |
| 8.2 | Compatibility note plus a register row | 0 | R6, with R17 |
| 8.3 | `TestAdapterExitsWithinGraceBudget` | 7a | R4 |
| 8.4a | Tier-5 NetworkPolicy under a live CNI, existing policies | 5 | R14 |
| 8.4b | Tier-5 NetworkPolicy under a live CNI, inter-replica policy | 5 | R18 |
| 8.5 | Reading plus a tier-7a elicitation wake case | 7a | R4, feeding R20 |
| 8.6 | Reciprocal host-conformance battery | 2 or 10 | R8, consumed by R17 |
| 8.7 | Duplicate-terminal case on the two-replica harness | 4 | R10 |

### 7.3 Durable CI gates

| Gate | Location | What it stops | Step |
|:--|:--|:--|:--|
| G0 change-graph completeness | `runValidateMaps` | A gate that fires in CI rather than on the developer's machine | R3 |
| G1 unreachable-surface detector | `tests/tier0_static/production_reachability_test.go` | A declared surface with no production caller. Three stages: reference counting, a name closure rooted at the `cmd/` mains, and an assignment-and-value-flow check over the identifiers the register names | R9 |
| G2a RPC to client to handler bijection | new `tests/tier3_contract/wire_bijection/` | A declared RPC with no client method | R9 |
| G2b event table to constant to consumer | same | A control event with a producer and no consumer | R9 |
| G2c collector to catalog to scrape path | `pkg/observability/metrics/catalog_test.go` | An unscrapable or uncatalogued metric | R9 |
| G3a flag and environment to rendered podspec | `tests/tier0_static/deployment_boundary_test.go` | A flag a component reads that the podspec never sets | R6 |
| G3b reciprocal host-conformance battery | `tests/tier2_component/hostconformance/` | Conformance validated against a synthesized host | R8 |
| G4 skip budget and `UNVERIFIED` verdict | `cmd/lenny-test`, `tests/registers/skip-budget.yaml` | A green suite that ran nothing. The classifier parses the AST, because the existing allow pattern matches any `t.Skipf` and 115 of 142 skip sites are invisible to it | R3 |
| G5 normative-claim register | `tests/claim-map.json` plus a tier-0 join | A specification sentence with no implementation and no tracked gap. Its load-bearing clause is that a `WIRED` claim must cite a surface G1 reports reachable | R2a schema, R9 join |
| G6 default-values parity | Tier 0 plus a nightly stock-defaults tier-5 lane | A test overlay silently disabling the behavior under test | R7 |
| G7 schema-member bijection and emitter round-trip | `tests/tier0_static/schemas_test.go` | A schema member with no fixture, and an emitter that does not satisfy its own schema | R15, register in R2a |
| G8 spec-map granularity | `runValidateMaps` plus `spec-map.json` | A numbered heading with no map entry. New tooling, because the current validator iterates only sections present in the map | R3 |
| G9 doc-versus-code drift | `tests/tier11_docs/` | Register header staleness and reference-document drift | R2a |
| G10 test-double honesty register | `validate-diagnosis` | A double standing in for a collaborator that does not exist | R24 |
| G11 new-route coverage ratchet | Tier 4 plus a register | The next 6c.1 | R10 |
| G12 gate integrity | `tests/tier0_static/gate_integrity_test.go` | A gate disabled by deleting a script | R3 |

### 7.4 Structural changes to how the suite certifies wiring

**Tier 11 loses wiring certification.** Roughly forty reconciliation files grep source as text, which is
exactly what gave the dead eviction route a green light
(`eviction_coordinator_route_consistency_test.go:97-108`: four substring probes, all present, all
unreachable). Wiring claims convert to G1 and G5 assertions, and tier 11 keeps prose reconciliation.
Owner: R25.

**Two existing tests codify gaps as intended behavior** and will assert the gap is correct after the gap
closes: `TestControlEventDroppedWhenNoStream_spec_4_7` (`pkg/adapter/controlchannel_test.go:264`) and
`TestEvictingFlag_spec_4_6_1` (`:250`, the only caller of `setEvicting`). Retirement is a named obligation
on R12 and R16, and the suite-wide sweep for the pattern belongs to R25.

**Five already-skipped tests are ready-made exit conditions** for the steps that make them runnable, of
which `tests/tier9_security/tls_test.go:84` (R14) and
`tests/tier5_e2e_kind/node_drain_checkpoint_test.go:67` (R16) are the two most consequential.

---

## 8. Coverage matrix

### 8.1 The count

Reference section 6 contains 30 numbered records (`gateway-runtime-comms.md:2143-2585`): 6a.1 through
6a.7 (7), 6b.1 through 6b.15 (15), and 6c.1 through 6c.8 (8). The framing figure of 34 matches no heading
count in the document. Three records are composites over tables: 6a.4 (11 rows at `:1782-1792`), 6a.5
(18 rows at `:2235-2252`), and 6b.15 (7 rows at `:2490-2496`). Section 8 lists 7 items (`:2693-2710`),
with 8.4 split here into 8.4a and 8.4b.

30 records plus 8 section-8 items is 38 units, each assigned below to exactly one verdict owner. Replacing
the three composites with their 36 rows gives 71 tracked units, a strict superset of any reading of 34.

### 8.2 Matrix

| Id | Title | Verdict owner | Contributing | Verified by |
|:--|:--|:--|:--|:--|
| 6a.1 | gRPC control stream has no gateway client | R12 | R9 | G1, G2a, G2b, and the tier-3 stream contract |
| 6a.2 | Coordinator-loss hold state unarmable | R12 | R6, R7, R9 | G1 transitive closure, tier-7a rolling restart, concurrent-slot row |
| 6a.3 | Runtime lifecycle channel never enabled | R17 | R6, R8, R15, R16 | G3a, G3b, tier-4 direct-mode usage |
| 6a.4 | The whole agent-pod eviction chain (11 rows) | R16 | R6 (PDB and admission), R12, R13, R18 | G3a, G1, tier-5 terminate-and-checkpoint, tier-8 matrix |
| 6a.5 | Other implemented-but-uncalled paths (18 rows) | R24 | R14 (3 security rows), R10 (`Adapter/Terminate`), R6 (the G3a guard on the OTLP row), R9, R12 (OTLP), R13 (2 rows), R23 (`Server.PreConnect`) | G1's name closure for 10 rows, G3a for the OTLP row, G1 stage 3 for 4 rows, a tier-4 per-route assertion for `Adapter/Terminate`, a tier-5 image assertion, and a podspec render case |
| 6a.6 | Dead branches gated on flags nothing sets | R16 | R17 | G1, tier-8 eviction-edge matrix |
| 6a.7 | `preConnect: true` pool cannot start a session | R23 | R6, R9 | G1, tier-5 pool start |
| 6b.1 | Generation validation on every gateway-to-pod RPC | R16 | R1b | G5, tier-3 field bijection |
| 6b.2 | The agent pod's preStop hook | R16 | R4 | G5, tier 5 |
| 6b.3 | `AdapterEvicting` has no producer | R16 | R12 | G1, G2b |
| 6b.4 | Adapter-to-gateway events over the control stream | R12 | R9 | G2b with the constant-identifier trap |
| 6b.5 | Hold-state entry condition | R12 | R2a | G5, tier 7a |
| 6b.6 | Inter-replica control forward and `coordinator_address` | R18 | R7, R13 | G5, tier-5 CNI |
| 6b.7 | Bind-time mirror seeding | R13 | R7 | Tier 4 on both bind paths |
| 6b.8 | The barrier acknowledgement surface | R12 | R1b (proto comment) | G2b, and a tier-3 assertion that the documented surface is the consumed one |
| 6b.9 | Slot-aware control RPCs | R22 | R1b (fields) | Tier-3 field bijection, tier-4 slot handlers |
| 6b.10 | `ForwardMessage` and cross-replica routing | R19 | R7, R18, R20 | Off-holder matrix |
| 6b.11 | The `ready_for_input` availability signal | R20 | R15 (frame), R17 (enablement) | G7, tier-4 receipt timeout |
| 6b.12 | Part B in a rendered pod | R17 | R6, R8 | G3a, G3b |
| 6b.13 | The best-effort eviction snapshot | R16 | R17 | G1, tier 8 |
| 6b.14 | Where the runtime channel schema lives | R2a | R1b (shipped JSONL description and schema-file rename) | G7 register derived from `ls schemas/**`, tier 11 |
| 6b.15 | Field-level schema divergences (7 rows) | R15 | R1b | G7 plus emitter round-trip |
| 6c.1 | No coordinator gate on the session REST surface | R10 | R5, R7 | Off-holder matrix over 20 routes, G11 |
| 6c.2 | No forwarding, redirect, or affinity | R19 | R7, R18 (transport and NetworkPolicy) | Off-holder matrix, tier-5 CNI |
| 6c.3 | The session inbox is write-only | R20 | R5, R7 | Tier-4 redelivery |
| 6c.4 | `inputwait` has no cross-replica fallback | R20 | R4 (8.5), R7 | Tier-7a cross-replica wake |
| 6c.5 | Coordinator cannot reach an unheld pod | R21 | R7, R18, R16, R13 | Tier-4 off-holder drive |
| 6c.6 | Checkpoint restore onto a concurrent pod | R22 | R7 | Tier-4 restore |
| 6c.7 | Agent-pod mTLS client identity | R14 | R6 (chart), R7 (defaults) | G6, tier-9 mTLS with no skip |
| 6c.8 | Non-Anthropic LLM proxy dialects | R11 | R2a | Tier-3 dialect-to-route bijection |
| 8.1 | No test tier was run | R25 | R3 | Full-tier run, `UNVERIFIED` verdict |
| 8.2 | Out-of-tree flag setters | R6 | R4, R17 | Compatibility note plus a register row |
| 8.3 | `GracefulStop` past the grace deadline | R4 | R16 | Tier-7a grace-budget test |
| 8.4a | NetworkPolicy under a live CNI, existing policies | R14 | R7 | Tier-5 CNI |
| 8.4b | NetworkPolicy under a live CNI, inter-replica policy | R18 | R7 | Tier-5 CNI |
| 8.5 | Elicitation waiter versus tool-approval poll | R4 | R20 | Reading plus a tier-7a case |
| 8.6 | Runtime side of the `checkpoint_ready` contract | R8 | R17 | Reciprocal host-conformance battery |
| 8.7 | Duplicate terminal from the orphan reconciler | R10 | R7 | Tier-4 duplicate-terminal case |

### 8.3 Crosswalk to the planning inputs

The planning material used a different step numbering. This plan renumbers so that step 1 is R1 and step 2
is R2, matching the user's framing.

| This plan | Planning inputs |
|:--|:--|
| R1a, R1b | R1, plus the deferred wire rename, plus R15's proto content |
| R2a, R2b | R7 |
| R3 | R2 |
| R4 | R3 |
| R5 | R4 |
| R6 | R5 |
| R7 | R6 |
| R8 | New. The reciprocal host-conformance battery, which had no owner. |
| R9 | R8 |
| R10 | R9 |
| R11 | R10 |
| R12 | R11 |
| R13 | R12 |
| R14 | R13 |
| R15 | R14 |
| R16 through R25 | R16 through R25, unchanged. The planning input's standalone R15 proto step is folded into R1b. |

---

## 9. Risks, open questions, and decisions a human must make

### 9.1 Decisions required before execution

**D1. Re-scope proposal 0062 (R0).** 0062's specification edits are already in `spec/`, which R1a and R2a
rewrite, and its CODE-3 builds the same gateway consumer as R12. The repository ships a
`build-gaps-spec-unblock` loop that drains findings backed by an approved proposal. If that loop picks 0062
up while R1a is in flight, the collision is head-on and S-1 is unenforceable. 0062's status must be moved
to superseded and its content re-scoped into R12, R13, R16, and R18 before R1a opens. This plan cannot make
that change, because it requires a `PROPOSAL-QUEUE.md` edit.

**D2. Confirm the wire rename (R1b).** Section 3.7 argues that the rename must reach the proto RPC, the
manifest key, the socket, the flag, and the three SDKs, and that the population of affected third-party
runtimes is empty by construction, because no runtime can currently reach Full level against a
chart-rendered pod. If that reasoning is wrong (for instance, a deployment out of tree does enable the
channel, which is what 8.2 could not establish), the rename becomes a breaking change with real consumers,
and the alternative is to accept that the wire keeps the colliding name and to drop the claim that step 1
fully delivers the user's first goal. The plan does not recommend that alternative, and it does not conceal
that it is a choice.

**D3. Confirm the `buf breaking` baseline treatment for R1b.** Advance the baseline ref, or add an
enumerated exception with an expiry. Decide before R1b opens rather than during.

**D4. Confirm that R1b may add proto fields whose readers are designed later.** Landing
`coordination_generation` and the slot identifiers in one window is what collapses S-2 to a single
exclusive window and removes the rename inversion. The cost is that a field's semantics are fixed before
its consumer (R16 and R22) is designed. The mitigation is that both field sets are fully enumerated in
reference 6b.1 and 6b.9, and that a second narrow window remains available. The alternative is two proto
windows and one more critical-path link.

**D5. Decide who owns `Options.InterReplicaAddress` population.** R13 populates it in wave 3, and what a
valid value is (pod IP plus port, headless-service DNS, or a SPIFFE-bearing authority) is determined by
R18's service in wave 5. Either R18 lands a design note before R13 starts, or address population moves
into R18 and R13 lands only the at-bind seed.

**D6. Decide R8's tier.** A battery that boots the rendered container is slower than the tier-2 norm and
may belong in tier 10. Decide on measurement.

**D7. Confirm the R10 availability change.** Failing closed on twenty session-scoped mutating routes when
the replica does not hold the binding is a visible availability change in every multi-replica deployment
until R19 lands. The alternative is to ship R10 in report-only mode first, which weakens the gate and
delays the closure of the whole §5.2 matrix.

**D8. Confirm the drain-readiness default flip (R6).** Turning `drainReadiness` on by default and changing
what it evaluates from artifact-store health to session liveness changes the behavior of every node drain
in an existing install. It is required for 6a.4, and it is an operational change rather than a code fix.

### 9.2 Open questions the plan cannot answer

- **8.2 is unanswerable from inside this repository.** Whether an out-of-tree overlay sets the three
  adapter flags cannot be established here. It converts into a compatibility requirement rather than
  closing.
- **Whether `srv.GracefulStop()` can hang** is R4's question, and it is the one answer that changes another
  step's design. If it comes back positive and unbounded, R16 grows.
- **Whether the terminal callback and audit sinks deduplicate** a second `recordSessionCompleted` for the
  same session was read only at the enqueue sites. R10 must establish it before it can claim 8.7 closed.

### 9.3 What this plan cannot promise

**Estimates are relative rather than absolute.** Sizes are S, M, and L for one worker. No calendar dates
are given, because the plan has no measurement of throughput on this codebase.

**The parallelism claims are conditional on R5 and R6 landing as scoped.** Every wave-3 concurrency claim
assumes each step's surface lives in a file no sibling touches. If R5's carve-up does not fully separate
`start.go`, `sessionserver.go`, `messages.go`, and `interactions.go`, then R10, R13, and R20 serialize
inside wave 3, R22 serializes behind them in wave 5, and wave 3 collapses to roughly two workers.

**Closing 6a.4 requires five steps.** It is a composite over eleven rows. Marking it closed when R16 lands
would leave a busy pod evictable with no PodDisruptionBudget and no admission gate. Its closure condition
is R6, R12, R13, R16, and R18 all landed. R21 consumes the inter-replica forward row that R18 closes and
owns no row of the table.

**The reachability gate over-approximates.** A name closure reports reachable more often than a true call
graph would, so its errors are missed dead code rather than false accusations. That is the correct posture
for a ratchet, and it means G1 is a floor. R9 enumerates the eight rows of 6a.5 that fall through it and
names the detector each one needs instead, so the floor is measured rather than asserted.

**Three planning figures did not reproduce and were corrected here.** The reserved-phrase sweep in `spec/`
measures 60 occurrences across eleven files rather than 57 across nine; the line-citation count measures
15,377 across 2,353 Go files rather than 15,307 across 2,299; and the `// spec:` count measures 12,701
rather than 12,634. The direction of each error is safe. Re-run all three as the first action of R1a and
R2b and record the current values in the step, because a planning-time figure is not an execution-time
figure.

**The reference is frozen, and freezing is itself a risk.** After R2a it is a point-in-time record of the
working tree at `fcda83e3`. Anyone reading it after §28 and §29 land must treat §28 and §29 as
authoritative for current behavior. The tier-11 header test makes that instruction unavoidable rather than
optional, and it does not make the frozen content correct.
