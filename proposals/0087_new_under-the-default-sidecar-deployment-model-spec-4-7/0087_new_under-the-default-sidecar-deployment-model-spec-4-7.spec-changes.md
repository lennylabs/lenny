# Spec changes: Sidecar pods have no platform-controlled runtime restart

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

Each staged edit is written against the specification as it stands once 0078, 0079, and 0084 have landed. Where the target is a sentence that 0079 stages, the edit names 0079's deliverable and quotes the text. Every anchor is located by heading and quoted text. None is located by line.

Each statement has one home in the specification. Every other site cites that home.

| Statement | Home |
|:--|:--|
| Which runtimes are supervised, and the conditions on `command` | SPEC-1, §5.1 **`command`** |
| What the supervisor does, when it launches and ends a runtime process, liveness, and pod termination | SPEC-2, §4.7.10 **Supervised runtime process.** |
| Startup order on a supervised pod | SPEC-3, §4.7.9 steps 4 and 7 |
| Why the adapter trusts the supervisor's connection, and what no isolation property rests on | SPEC-4, §4.7.11 item 8 |
| What the scrub reaches on a supervised pod, and what persists there | SPEC-5, §5.2 **What the scrub reaches.** |
| The agent-UID container rule on a supervised pod | SPEC-6, §13.1 **Agent UID on a supervised pod.** |
| The `CH-SUPERVISE` endpoint, messages, single accept, and failure behavior | SPEC-7, the §28.5.3 card, with the §28.6 paragraph and the §28.8 row |

The SPEC-2 definitional sentence scopes every other statement, staged by 0079 or present in the tree, that the runtime process lives as long as the pod or is kept across sessions. It applies to runtimes that declare no `command`. No other site is edited for that reason. The acknowledgment and the tenant pin are unchanged.

## Edge cases and accepted failure modes

| Case | Observable outcome | Home |
|:--|:--|:--|
| The supervisor never connects | The adapter exits non-zero before READY, and the pod never becomes `idle` | SPEC-3 step 4, SPEC-7 card **Degradation.** |
| `launch` fails because argv[0] is missing or not executable | `launched` carries `error`, the session's start fails, and §5.2 retires the pod on the failed session | SPEC-2 **Launch.**, SPEC-7 card |
| The runtime process crashes while a session is bound | The session fails, no process is launched for it, and the pod is retired | SPEC-2 **Stopped process.** |
| The runtime process exits after its session ends and before the boundary | `end` confirms that no process remains, and the next session runs in a newly launched process. The exit alone does not retire the pod | SPEC-2, SPEC-5(f) |
| A process survives the kill, or the `/proc` proof fails | `ended` carries `error`, the adapter reports that the runtime cannot serve the next session, and the pod is retired | SPEC-2 **Liveness.**, SPEC-7 |
| Author code signals the supervisor and it exits | The runtime container ends, `CH-SUPERVISE` closes, and the pod is retired at the next boundary or fails its session | SPEC-2 **Liveness.**, SPEC-4 **Isolation scope.** |
| Concurrent pool | Sessions that start while a process is live join it. One process serves one occupancy cycle | SPEC-2 **Launch.** |
| Pod termination | The supervisor forwards SIGTERM, and the kubelet's SIGKILL bounds the wait | SPEC-2 **Pod termination.** |
| The coordinator hold times out | The adapter's pod-scope teardown closes `CH-SUPERVISE`, the supervisor exits, and the runtime container's processes end | SPEC-2 **Pod termination.** |
| A Runtime is edited to add `command` | Pods created afterwards are supervised. Earlier pods keep their spec, and the acknowledgment applies to both | SPEC-2(a), SPEC-5(e) |
| `command` on an embedded, `service`, or `preConnect` runtime | `Registered=False` (`InvalidRuntime`), and the pod builder renders no pod | SPEC-1 |
| Nonce-only mode | No peer check exists, and ordering alone identifies the supervisor | SPEC-4 **Why the adapter trusts the connection.** |
| Shared-memory enumeration is unavailable in the guest | The end fails, and the pod is retired | SPEC-2 **Liveness.** |

## Staged edits

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.1 Runtime

(a) In the **Standalone Runtime** YAML example, insert after the line that begins `requireSoPeercred: true # optional;`:

```yaml
command: ["/usr/local/bin/my-agent", "--serve"] # optional; sidecar session-mode only; see §4.7.10 Supervised runtime process
```

(b) Insert after the **`requireSoPeercred`** paragraph, which ends `so a runtime registered only through the admin API runs with the default of `true`.`, and before `#### Derived Runtime`:

```
**`command`** — names the argv of the runtime process. Optional. Its first element is an absolute path in the runtime image, and the API server rejects a `Runtime` whose first element is not absolute.

- When set, the runtime container runs the platform runtime supervisor in place of the image's `ENTRYPOINT` and `CMD`, neither of which is used, and the supervisor launches this argv as the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**).
- When unset, the runtime process has the lifetime [Section 4.7.10](04_system-components.md#4710-deployment-model) **Runtime process lifetime.** states.
- The field is valid only on a `deploymentModel: sidecar`, `executionMode: session` runtime that does not declare `capabilities.preConnect: true`. The RuntimeReconciler marks a runtime that violates any of these conditions `Registered=False` (`InvalidRuntime`) and does not mirror it, and the pod builder refuses to render an agent pod for it.
- The field is settable only through `Runtime` CRD registration. Like `deploymentModel`, it has no registry counterpart and takes no part in the derived-runtime merge. The admin registration payload, which is the only way to register a derived runtime, does not model it.
```

The field is not added to the **Never overridable on derived runtime:** list or to the **Normative Merge Algorithm** table.

### SPEC-2 · spec/04_system-components.md § 4.7.10 Deployment Model

(a) In the **Runtime process lifetime.** paragraph 0079 SPEC-2 stages, insert after its first sentence, `The runtime process lives as long as the pod.`:

```
On a pod whose runtime declares `command`
([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)), the sentences of
this paragraph that describe one process or one `CH-MSGSOCK` connection for the
pod's life do not apply; **Supervised runtime process.** below states that
lifetime.
```

In the same paragraph, append after the final sentence, which ends `"Deployer acknowledgment (runtime process kept across sessions)").`:

```
The acknowledgment applies whatever the pool's runtime declares.
```

(b) Insert after the **Runtime process lifetime.** paragraph and before `**Health check:**`:

```
**Supervised runtime process.** A runtime whose definition declares `command`
([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)) runs each runtime
process under the runtime supervisor, a platform process in the runtime
container. On a pod the pod builder rendered with the runtime supervisor, a
statement elsewhere in this specification that the runtime process lives as long
as the pod, is kept across sessions or across the recycle boundary, or stays
alive for the pod's life describes a runtime that declares no `command` and does
not apply; this paragraph states the lifetime on such a pod.

- **Staging.** The runtime container runs the runtime supervisor, which the
  adapter image carries, as its first process and the init of its process
  namespace, with `command` as the argv the supervisor launches. The
  supervisor's binary is on a read-only mount
  ([§13.1](13_security-model.md#131-pod-security), **Agent UID on a supervised
  pod.**).
- **Connection.** The supervisor launches no process until the adapter asks it
  to on `CH-SUPERVISE` ([§28.5.3](28_communication-channels.md#2853-intra-pod)).
- **Launch.** At startup step 7
  ([§4.7.9](#479-startup-sequence-for-type-agent-runtimes)) the adapter has the
  supervisor launch `command` when no runtime process is live, after step 6
  wrote the final manifest. A session that starts while a process is live uses
  that process. A launched process therefore serves the sessions of one
  occupancy cycle: one session on a pool with `maxConcurrentSessions: 1`, and
  the sessions that overlap until occupancy reaches zero on a concurrent pool.
- **End.** Scrub step 1 ends the process: the supervisor sends SIGKILL to every
  other process in the runtime container, with no preceding message on any
  runtime channel, and confirms that none remains
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  "What the scrub reaches"). After the supervisor confirms the end, the adapter
  discards every runtime connection left open or pending on `CH-MSGSOCK` and
  `CH-RUNTIMEOPS`, so a connection it accepts afterwards belongs to the next
  launched process.
- **Liveness.** The adapter reports such a runtime as able to serve the next
  session only while the supervisor is connected and its last end confirmed
  that no other process remained; otherwise the pod is retired
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  Pod retirement policy).
- **Stopped process.** A launched process that stops while a session is bound
  is not launched again for that session: the session fails, and the pod is
  retired
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
- **Generation.** Each launched process is a separate runtime process for the
  rule that counts the sessions the pod's shared runtime process has been given
  ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels),
  **Session address.**): the count starts at its launch.
- **Pod termination.** When the runtime container receives SIGTERM, the
  supervisor forwards it to every other process in the container and keeps its
  `CH-SUPERVISE` connection until those processes exit or the connection
  closes; the kubelet's SIGKILL bounds the wait. When the connection closes, the
  supervisor exits, and its exit as the init of the container's process
  namespace ends every process in the runtime container.
```

(c) In the sidecar-versus-embedded trade-off table, in the `Runtime process lifetime` row 0079 SPEC-2 adds, replace the sidecar cell `The pod's lifetime; one connection serves every session` with the text below. The row stays one physical line.

```
The pod's lifetime; one connection serves every session. With `command` declared, one occupancy cycle (see **Supervised runtime process.**)
```

### SPEC-3 · spec/04_system-components.md § 4.7.9 Startup Sequence for `type: agent` Runtimes

(a) Append to step 4, `4. Adapter signals READY to gateway — pod enters warm pool`, on the same list item:

```
 On a pod whose runtime declares `command` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)), the adapter signals READY only after the runtime supervisor has connected on `CH-SUPERVISE` ([§4.7.10](#4710-deployment-model), **Supervised runtime process.**).
```

(b) Replace the step 7 item that 0079 SPEC-1 stages, which begins `7. The runtime process becomes live for the pod's first session.` and ends `In the embedded deployment model the adapter runs the runtime loop in its own process for each session.`, with this single item:

```
7. The runtime process becomes live for the pod's first session. In the sidecar
   deployment model ([§4.7.10](#4710-deployment-model)), on a pod whose runtime
   declares no `command` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)),
   the kubelet started the runtime container when the pod started; the runtime
   dials the adapter on `CH-MSGSOCK`
   ([§28.5.3](28_communication-channels.md#2853-intra-pod)), and the adapter
   accepts that connection here, and a later session on the same pod skips this
   step and steps 8 and 9 and uses the connections the runtime opened on the
   pod's first session. On a pod whose runtime declares `command`, the adapter
   has the runtime supervisor launch the runtime process here, after step 6
   wrote the final manifest and only when no runtime process is live, and
   accepts that process's `CH-MSGSOCK` connection; the pod's first session and
   the first session after each whole-pod scrub take this step and steps 8 and
   9, and a session that starts while a process is live skips them
   ([§4.7.10](#4710-deployment-model), **Supervised runtime process.**). In
   either case the adapter does not itself start a process in the runtime
   container: the two containers share no process namespace
   ([§13.1](13_security-model.md#131-pod-security)) and the runtime binary
   exists only in the runtime container's image. In the embedded deployment
   model the adapter runs the runtime loop in its own process for each session.
```

The replacement item contains none of the phrases 0079's TEST-16 bans: "Adapter spawns runtime binary", "Adapter starts agent binary", and "The adapter spawns the runtime".

### SPEC-4 · spec/04_system-components.md § 4.7.11 Adapter-Agent Security Boundary

Insert after item 7, **MCP server security:**, which ends `by directly connecting to the MCP socket without manifest access.`, and before `### 4.8 Gateway Policy Engine`:

```
8. **Runtime supervisor:**
   - **Where it runs.** On a pod whose runtime declares `command`
     ([§4.7.10](#4710-deployment-model), **Supervised runtime process.**), the
     runtime supervisor runs at the agent UID on the untrusted side of this
     boundary. The adapter sends every request on `CH-SUPERVISE`, and the
     supervisor only replies, so item 2 holds.
   - **Why the adapter trusts the connection.** The adapter accepts the
     supervisor's connection only on the terms the `CH-SUPERVISE` card states
     ([§28.5.3](28_communication-channels.md#2853-intra-pod)). That connection
     is made before any author code exists in the pod, for three reasons: the
     supervisor is the runtime container's first process, it launches nothing
     before `launch`, and no other container on such a pod runs at the agent
     UID ([§13.1](13_security-model.md#131-pod-security), **Agent UID on a
     supervised pod.**). In nonce-only mode, that ordering alone identifies the
     supervisor.
   - **Protection from author code.** The supervisor marks itself
     non-dumpable. Because every capability is dropped, code at the agent UID
     cannot attach to it or read its memory or descriptors. The supervisor's
     binary is on a read-only mount.
   - **Use of its reports.** The adapter uses the supervisor's reports only to
     decide whether the next session gets a newly launched runtime process.
   - **Isolation scope.** The supervisor is not a session or tenant isolation
     boundary. The pool acknowledgment and the tenant pin apply by pool
     configuration whatever the runtime declares
     ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
     "Deployer acknowledgment (runtime process kept across sessions)"). A
     defeated or failed supervisor leaves a pod on the acknowledged same-tenant
     keep baseline.
   - **Validation.** The gVisor and Kata security integration tests that
     validate `SO_PEERCRED` semantics (item 1) also validate the non-dumpable
     mark and the reach of the supervisor's kill. Unlike `SO_PEERCRED`, these
     properties have no startup self-test, because no isolation property rests
     on them. A kill that fails to reach every process fails the end report,
     and the pod retires.
```

Item 8 states neither the single-accept, peer-UID-check, and listener-close mechanics nor the messages the channel carries. Those stay in the SPEC-7 card and the §28.6 paragraph.

### SPEC-5 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool Configuration and Execution Modes

(a) In **Lenny scrub procedure**, step 1, replace the sentence 0079 SPEC-6(a) appends, `The kill runs in the container that executes the scrub and does not end the runtime process; see "What the scrub reaches" below.`, with:

```
The kill runs in the container that executes the scrub. On a pod whose runtime declares no `command` it does not end the runtime process. On a pod whose runtime declares `command`, the adapter then has the runtime supervisor end every other process in the runtime container ([§4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**). See "What the scrub reaches" below.
```

(b) Append to step 1b, after the sentence that ends `but the step executes unconditionally for consistency.`:

```
On a pod whose runtime declares `command`, the supervisor removes the segments the agent UID owns or created during step 1.
```

(c) Replace the **What the scrub reaches.** paragraph that 0079 SPEC-6(b) inserts, from `**What the scrub reaches.** The scrub executes in the adapter's process` through `process ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, with the text below. This paragraph is the one statement of the state that persists on a supervised pod.

```
**What the scrub reaches.** The scrub executes in the adapter's process and
reaches what that process can reach, and on a pod whose runtime declares
`command` ([§5.1](#51-runtime)) also what the runtime supervisor reaches. In the
sidecar deployment model
([§4.7.10](04_system-components.md#4710-deployment-model)) steps 0, 2, 4, and 6
operate on volumes mounted into both containers, so they clear and verify the
runtime container's view of those paths. Step 1 itself signals only processes
in the adapter container, because `shareProcessNamespace` is forbidden
([§13.1](13_security-model.md#131-pod-security)). Steps 3 and 5 act on the
adapter's own environment and log buffers. In the embedded model the runtime is
the adapter's process, which step 1 spares, and step 1 ends the processes the
runtime started. On a pod whose runtime declares `command`, step 1 also ends
every process in the runtime container other than the supervisor, and the
agent UID's shared-memory segments are removed, so no runtime process persists
across the recycle boundary. The adapter process and the pod's System V
semaphores and message queues still persist. Otherwise, in both models the
runtime process and its memory persist across the recycle boundary, and in the
sidecar model so do the processes it started, because the pod keeps the runtime
process ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime
process lifetime").
```

(d) In the best-effort paragraph, replace the clause 0079 SPEC-6(c) adds, `and the kept runtime process (see "What the scrub reaches" above).`, with:

```
and, on a pod whose runtime declares no `command`, the kept runtime process (see "What the scrub reaches" above).
```

(e) In the **Deployer acknowledgment (runtime process kept across sessions).** paragraph that 0079 SPEC-8(d) inserts, replace the opening words `The runtime process lives as long as the pod`, which 0079's block wraps after `runtime`, with `On a pod whose runtime declares no `command`, the runtime process lives as long as the pod`. Leave the rest of that sentence, which ends `(see "What the scrub reaches" above).`, unchanged. Insert immediately after that sentence, before `Deployers must set`:

```
On a pod whose runtime declares `command`, the later session runs in a newly launched runtime process, and the state "What the scrub reaches" lists as persisting still carries across the boundary.
```

(f) In the **Recycling and integration levels** paragraph that 0079 SPEC-5(b) stages, append after `Recycling is admitted at every integration level (Basic, Standard, and Full).`:

```
On a pod whose runtime declares `command` ([Section 5.1](#51-runtime)), the
runtime process is not kept: the session after each whole-pod scrub runs in a
newly launched process, and a runtime that exits after its session does not by
that alone retire the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model),
**Supervised runtime process.**).
```

No SPEC-5 text names the §5.2 Pod retirement policy item that 0079 SPEC-7(a) adds, and none names its reason value. 0079's `TestKeptRuntimeRetireConditionHasOneSpecHome` enforces that.

### SPEC-6 · spec/13_security-model.md § 13.1 Pod Security

Insert after the **`lenny-cred-readers` membership boundary.** paragraph and before the **Per-slot credential-read scope (`maxConcurrentSessions > 1`).** paragraph:

```
**Agent UID on a supervised pod.** On a pod whose runtime declares `command` ([§4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**), the pod builder renders the runtime container as the only container that runs at the agent UID. The init container that stages the runtime supervisor runs at the adapter UID under the per-container controls above.
```

### SPEC-7 · spec/28_communication-channels.md § 28.3, § 28.5.3, § 28.6, and § 28.8

(a) In §28.3, after the sentence `The provenance column carries the entry number the channel inventory in `gateway-runtime-comms.md` assigns, so a reader can recover the derivation of every column without the retired prose being reproduced here.`, insert:

```
A channel added after that inventory carries `None` in the column.
```

(b) In the §28.3 **Channel register**, insert after the `CH-MCP-CONNECTOR` row, as one physical line:

```
| `CH-SUPERVISE` | None | `intra-pod` | Control | Runtime supervisor | Pod adapter | Unix socket JSON Lines | Runtime process launch and end | None |
```

(c) In §28.5.3, replace the intro paragraph, from `This boundary carries the channels between the runtime adapter and the runtime binary inside one agent` through `The runtime is the dialling participant on every channel here.`, with:

```
This boundary carries the channels between the runtime adapter and the runtime binary inside one agent
pod, and on a pod whose runtime declares `command` the channel between the runtime adapter and the runtime
supervisor ([§4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**).
The §28.3 channel register places these channels on this boundary, and each carries `None` in its Link
column, so each channel's own register row together with the endpoint stated in its card describes the
connection it runs on. The runtime binary is the dialling participant on every channel here except
`CH-SUPERVISE`, which the runtime supervisor dials.
```

Replace the diagram block that follows with:

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
  |  |                  |                          +------------------+  |
  |  |                  |                          +------------------+  |
  |  |                  |   CH-SUPERVISE   <=====  | runtime          |  |
  |  |                  |                          | supervisor       |  |
  |  +------------------+                          +------------------+  |
  |                                                                      |
  +----------------------------------------------------------------------+

  The runtime-ops, MCP, and supervisor channels run on abstract Unix sockets
  in the Linux abstract namespace. The runtime binary dials every channel
  drawn above except CH-SUPERVISE, which the runtime supervisor dials. The
  runtime supervisor is present only on a pod whose runtime declares command.
```

(d) Insert after the **`CH-MCP-CONNECTOR`** card and before `#### 28.5.4 Inter-replica`:

```
**`CH-SUPERVISE`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** The abstract Unix socket `@lenny-supervise` in the Linux abstract namespace, present only
  on a pod whose runtime declares `command`
  ([§4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**). The
  adapter listens and the runtime supervisor dials. The adapter applies the
  [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1 peer-UID check when
  `SO_PEERCRED` is enforced and performs no manifest-nonce handshake or challenge-response on this
  channel, because the supervisor connects before the manifest exists
  ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8).
- **Axes.** Control plane, dialled by the runtime supervisor, message authority with the pod adapter,
  Unix socket JSON Lines transport (§28.3).
- **Messages.** Adapter to supervisor: `launch` and `end`. Supervisor to adapter: `launched`, which
  answers `launch`, and `ended`, which answers `end`. Each message is a single JSON object terminated by
  `\n` whose `type` member carries the message name. A `launched` or `ended` carries an optional string
  member `error`, present when the request failed and absent when it succeeded. `launch` asks the
  supervisor to launch `command` as the runtime process. `end` asks it to end every other process in the
  runtime container, confirm that none remains, and remove the System V shared-memory segments the agent
  UID owns or created ([§4.7.10](04_system-components.md#4710-deployment-model),
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "What the
  scrub reaches"). No message carries a credential, manifest content, an environment value, or a session
  identifier.
- **Preconditions.** The adapter accepts the connection after its `SO_PEERCRED` self-test and before it
  signals READY ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4).
  The adapter sends `launch` at startup step 7, after step 6 wrote the final manifest and only when no
  runtime process is live, and sends `end` during scrub step 1
  ([§4.7.10](04_system-components.md#4710-deployment-model),
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The adapter
  has at most one request outstanding, and the supervisor sends nothing that is not a reply.
- **Timing.** The adapter waits a bounded time for the connection before READY, and the specification
  states no value. `end` is bounded by the scrub's context
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The
  specification states no other deadline, retry count, or interval for this channel.
- **Exclusivity.** One connection per pod. The adapter accepts the first connection before READY and
  then closes the listener (§28.6).
- **Degradation.** When no supervisor connects within the adapter's wait, the adapter exits non-zero and
  the pod is never marked `idle`
  ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4). A `launched`
  that carries `error` fails the session's start. An `ended` that carries `error`, a reply that answers
  no outstanding request, and a closed connection each make the adapter report the runtime as not able
  to serve the next session, and the pod is retired
  ([§4.7.10](04_system-components.md#4710-deployment-model), **Supervised runtime process.**,
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), Pod
  retirement policy). When the connection closes, the supervisor exits, which ends every process in the
  runtime container ([§4.7.10](04_system-components.md#4710-deployment-model)).
```

(e) In §28.6, insert after the **One operation per pod.** paragraph and before **Channels the specification states no constraint on.**:

```
**One connection per pod.** `CH-SUPERVISE` admits one connection for the pod's life: the first
connection the adapter accepts before READY, which passes the
[§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1 peer-UID check when
`SO_PEERCRED` is enforced. The guard is the adapter's closing of the listener after that accept
(§28.5.3). The constraint bounds the connections inside one pod; no gateway replica holds the channel.
```

The **Channels the specification states no constraint on.** list is unchanged.

(f) In the §28.8 matrix, insert after the `CH-MCP-CONNECTOR` row, as one physical line:

```
| `CH-SUPERVISE` | When no supervisor connects within the adapter's wait before READY, the adapter exits non-zero and the pod is never marked `idle` ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4, §28.5.3) | A closed connection or an `ended` that carries `error` makes the adapter report the runtime as not able to serve the next session, and the pod is retired; the supervisor exits on the close, which ends every process in the runtime container ([§4.7.10](04_system-components.md#4710-deployment-model), [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), §28.5.3) | One connection per pod; the adapter closes the listener after its one accept, so no second connection is accepted, and no gateway replica holds the channel (§28.5.3, §28.6) | A pod that exits before READY, or a pod retired under the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) Pod retirement policy. The specification states no metric or alert for this channel (§28.5.3) |
```

The claim-register rows for this card are WIRED rows, which name the surfaces CODE-2 and CODE-3 create. CODE-3 lands them.

## Spec files touched

- `spec/04_system-components.md` (SPEC-2, SPEC-3, SPEC-4)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1, SPEC-5)
- `spec/13_security-model.md` (SPEC-6)
- `spec/28_communication-channels.md` (SPEC-7)
