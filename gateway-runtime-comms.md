> **Point-in-time record.** This document is a point-in-time reading of the working tree at
> `fcda83e3`. Sections 28 and 29 of the specification supersede it for all current behavior.
> The body below is unchanged from that reading and is not maintained.

# Gateway, agent pod, adapter, and runtime communication

A reference for how the gateway, the agent pod, the adapter container, the runtime container, and the
control plane communicate in the implementation as it stands on branch `proposal-b/c-22-eviction-trigger`
(working tree at `fcda83e3`, which merges proposal 0060 co-location and the integration tip).

## 1. Scope, and how to read this document

### 1.1 What this covers

Every channel that carries data or control between a gateway replica and an agent pod, between the two
containers inside an agent pod, and between a gateway replica and the shared stores that mediate
cross-replica coordination. For each channel the document states the direction, the transport, the
endpoint, the exclusivity guarantee, the implementation status, and the purpose. It then traces the
scenarios that use those channels end to end.

This document describes the implementation. Where the specification under `spec/` describes something
the implementation does not do, both are stated and the divergence is flagged in section 6b.

### 1.2 Status labels

Every mechanism carries one label per side of the boundary it crosses. A mechanism that is implemented
on one side and missing on the other carries a compound label naming both halves, such as
"server WIRED, client UNWIRED".

| Label | Meaning |
|:--|:--|
| **WIRED** | Implemented and reachable from production code. The production caller is named with a `file:line` citation. |
| **UNWIRED** | Implemented, but no production caller exists. Only tests, test-only export seams, or demo binaries reach it. The proving grep is shown. |
| **ABSENT** | Referenced by the specification, a proto comment, or a code comment, but not implemented. |
| **UNVERIFIED** | Could not be established from the source. Stated as such rather than guessed. |

A path can be WIRED inside one binary and UNWIRED at the deployment boundary. The adapter-to-runtime
lifecycle channel is the clearest case: it has production callers inside the adapter process, and the
podspec never enables it. Both halves are load-bearing, so both are stated.

### 1.3 Evidence rules used

Every factual statement carries a `file:line` citation that was opened directly. A code comment, a proto
comment, or a specification sentence is a lead rather than evidence. Where a comment and the code
disagree, the code is reported and the divergence is called out. Paths are relative to the repository
root at `/home/ec2-user/lenny`.

A citation shortened to a bare basename refers to the most recent full path carrying that basename in
the same section, and each section writes the path out in full the first time it uses one. Several
basenames name two or more distinct non-test files in this tree, so a reader crossing a section boundary
must resolve them against the section's own first mention rather than against the previous section's:

| Basename | Files it can name |
|:--|:--|
| `coordination.go` | `pkg/adapter/`, `pkg/gateway/coordination/coordination/` |
| `session.go` | `pkg/adapter/`, `pkg/api/v1/session/` |
| `usage.go` | `pkg/adapter/`, `pkg/gateway/sessionserver/` |
| `lifecycle.go` | `pkg/adapter/`, `pkg/gateway/sessionserver/` |
| `checkpoint.go` | `pkg/adapter/`, `pkg/checkpoint/` |
| `events.go` | `pkg/gateway/session/sessionevents/`, `pkg/gateway/sessionserver/` |
| `prestop.go` | `cmd/lenny-adapter/`, `pkg/gateway/podlifecycle/prestop/` |
| `main.go` | `cmd/lenny-adapter/`, `cmd/lenny-gateway/` |
| `client.go`, `transport.go`, `wiring.go`, `slot.go`, `manifest.go`, `staging.go`, `registry.go`, `tree.go`, `integrationlevel.go`, `toolapproval.go` | two or more each |

Where the ambiguity is load-bearing, the full path is repeated even mid-section.

`BUILD-GAPS.md` is a historical record and is not cited as current evidence. Its claim that
`--staging-dir` is never wired (`BUILD-GAPS.md:1656`, `:5440`) is stale: the podspec emits it on both
deployment models (`pkg/controller/sandbox/podspec/podspec.go:573` sidecar, `:674` embedded) and the
adapter consumes it (`cmd/lenny-adapter/main.go:126`, `:275`).

### 1.4 The naming trap: two different things are called a "lifecycle channel"

This is the single largest source of confusion on this surface. Two distinct mechanisms share the name,
and a third mechanism is easily conflated with the first.

| Short name used here | What it is | Transport | Definition | Status |
|:--|:--|:--|:--|:--|
| **runtime lifecycle channel** | adapter to runtime, intra-pod | Unix socket, JSON Lines frames | `pkg/adapter/lifecyclechannel.go:92` (type), `:134` (`NewLifecycleChannel`) | WIRED inside the adapter binary, never enabled by the podspec |
| **gRPC control stream** | gateway to adapter, cross-pod-boundary | gRPC bidirectional streaming RPC `Adapter/LifecycleChannel` | proto `schemas/lenny-adapter.proto:227`, handler `pkg/adapter/controlchannel.go:108` | server WIRED, client UNWIRED |
| **runtime message socket** | adapter to runtime, intra-pod | a *different* Unix socket, also JSON Lines | `pkg/adapter/socketruntime.go:60` (`SocketRuntimeProcess`) | WIRED |

The runtime lifecycle channel and the runtime message socket are two sockets inside the same pod
carrying two disjoint frame vocabularies. The message socket (`@lenny-runtime`, advertised through the
`LENNY_ADAPTER_SOCKET` environment variable at `pkg/controller/sandbox/podspec/podspec.go:615-617`)
carries `message`, `response`, `tool_call`, `tool_result`, `heartbeat`, `heartbeat_ack`, `shutdown`,
`status`, and `set_tracing_context`, schematized in `schemas/lenny-adapter-jsonl.schema.json`. The
lifecycle channel carries `checkpoint_request`, `checkpoint_ready`, `interrupt_request`, and the other
operational signals, schematized in `schemas/lifecycle-events.schema.json`.

Throughout this document each mention is qualified in the document's own prose. A bare "lifecycle
channel" or "control channel" appears only inside a quotation from a code comment, a JSON schema, or the
specification, where the source text is preserved verbatim.

### 1.5 Exclusivity vocabulary

Every channel section states whether two gateway replicas can hold the channel at once, and at what
granularity the exclusivity applies (per pod, per session, or per slot). Where the answer is "yes, two
replicas can hold it", the section names the guard that is missing.

---

## 2. The participants

### 2.1 Gateway replica

A pod in the `lenny-system` namespace running `cmd/lenny-gateway`, deployed with two replicas by default
(`charts/lenny/values.yaml:2121`). Each replica runs the full surface: the session REST API, the MCP
tool surface, the coordination sweeper, the watchdog, the periodic checkpointer, and the
`GatewayControl` gRPC server.

State that is process-local to one replica, and therefore invisible to its peers:

| State | Type | Citation |
|:--|:--|:--|
| Pod bindings | `podsession.Registry`, `map[sessionID]*BindResult` under a mutex | `pkg/gateway/podlifecycle/podsession/registry.go:14-17`, per-replica doc at `:11-13` |
| Cached Attach streams | `PodExecutor.streams`, `map[sessionID]*adapterclient.AttachStream` | `pkg/gateway/session/executor/pod.go:48-49` |
| Input-wait registry | `inputwait.Registry`, in-process map | `pkg/gateway/session/inputwait/inputwait.go:41-49` |
| Tool-approval waiters | `toolapproval.Registry`, in-process | `cmd/lenny-gateway/stores.go:2362` |
| Checkpoint single-flight locks | `sync.Map` of mutexes keyed `(session, slot)` | `pkg/gateway/checkpoint/checkpointer/uploaddriver.go:133-139` |
| Slot-health ledger | in-memory tracker, one per replica | `pkg/gateway/runtime/slothealth/slothealth.go:55-60`, constructed at `cmd/lenny-gateway/sessiondeps.go:261` |
| Recycle-boundary timers | in-process timer map | `pkg/gateway/session/recycle/recycleboundary.go:92-97`, `:331` |

State that is shared across replicas: the Postgres session store, the Postgres `coordination_lease`
mirror, the Redis coordination lease, the Redis slot counter, the Redis event relay, the Postgres
interaction store, and the Kubernetes API objects.

A replica's identity is `w.replica` (`cmd/lenny-gateway/stores.go:1351`, assigned at `:1563`), resolved
by `resolveReplicaID` (`cmd/lenny-gateway/main.go:1017-1028`). It is the `LENNY_REPLICA_ID` environment
variable when that variable is set (`main.go:1018-1019`), and otherwise the hostname plus four random
bytes (`:1021-1027`). The podspec and the chart set no such variable, so the fallback form is what a
chart-rendered gateway uses. The value is the coordination-lease holder string and the
`coordinator_replica` mirror value.

### 2.2 Agent pod

A pod in an agent namespace (`lenny-agents` by default), created by the Sandbox reconciler from a
Sandbox custom resource, itself created by the WarmPool reconciler
(`pkg/controller/warmpool/controller.go:509-545` then `pkg/controller/sandbox/controller.go:347-386`).
The pod's name equals its Sandbox's name.

Pod-level facts that govern communication:

| Field | Value | Citation |
|:--|:--|:--|
| `restartPolicy` | `Never` | `pkg/controller/sandbox/podspec/podspec.go:982` |
| `terminationGracePeriodSeconds` | 120 by default | `podspec.go:983`, default constant `:75` |
| `automountServiceAccountToken` | `false` | `podspec.go:997` |
| Readiness gate | `lenny.dev/sandbox-ready`, flipped to True by the Sandbox reconciler once the containers report ready | `podspec.go:991`, constant `:194`, writer `pkg/controller/sandbox/controller.go:727-748` |
| Kubelet probes | none on any container | `grep -n "ReadinessProbe\|LivenessProbe\|StartupProbe" pkg/controller/sandbox/podspec/podspec.go` returns nothing |
| `dnsPolicy` | `None`, pointing at a dedicated CoreDNS ClusterIP | `podspec.go:1084-1094` |
| Environment variables set on the adapter container | exactly `POD_NAME` from the Downward API | `podspec.go:598`, helper `:916-919` |

The pod never learns its own IP address from its spec, and it learns the gateway only as a Service DNS
name rather than as a specific replica (`podspec.go:833-841`).

Because no container carries a readiness, liveness, or startup probe, the kubelet never independently
detects a wedged adapter or a wedged runtime. The two liveness signals that do exist are the gateway's
connectivity-state probe on its own gRPC channel (`cmd/lenny-gateway/coordination_seams.go:58-67`) and
the adapter's intra-pod heartbeat against the runtime (section 3.10).

Two deployment models exist. The sidecar model (`podspec.go:529-639`) runs an `adapter` container and a
`runtime` container. The embedded model (`podspec.go:641-716`) runs one `runtime` container into which
the first-party runtime links the adapter code.

### 2.3 Adapter container

Runs `cmd/lenny-adapter`. It hosts a gRPC server on port 50051 implementing the `Adapter` service
(`pkg/adapter/transport.go:27-57`, listener at `cmd/lenny-adapter/main.go:404` and `:428`), and it dials
the gateway as a client of the `GatewayControl` service (`pkg/adapter/gatewaylink.go:36-79`, invoked at
`cmd/lenny-adapter/main.go:328`).

Inbound RPCs pass through an interceptor chain prepended at `pkg/adapter/transport.go:44-48`: an
OpenTelemetry stats handler, a credential-redaction unary interceptor, and the hold-state unary and
stream interceptors. Credential redaction is unary-only, which is currently harmless because both
credential-sensitive methods are unary (`pkg/adapter/credredact.go:26-29`).

The adapter's own SIGTERM handler is the whole of `cmd/lenny-adapter/main.go:411-424`. On signal it
calls `ShutdownDemoteSDK` (`:420`), closes the runtime lifecycle channel if one exists (`:421-423`), and
calls `srv.GracefulStop()` (`:424`). It does nothing else.

### 2.4 Runtime container

Runs a third-party or first-party agent process. In the sidecar model it discovers the adapter through
the `LENNY_ADAPTER_SOCKET` environment variable (`podspec.go:615-617`) and reads the adapter manifest at
`/run/lenny/adapter-manifest.json` (`pkg/adapter/manifest.go:28`, directory from
`cmd/lenny-adapter/main.go:344`, which assigns `ManifestDir` from `--credentials-dir`, defaulted to
`/run/lenny` at `main.go:130`).

The runtime container carries no `Lifecycle` block at all in the sidecar model: the struct literal at
`podspec.go:609-620` ends with `SecurityContext` and declares no preStop hook. Only the adapter
container has one (`podspec.go:607`).

The runtime's integration level is classified by the adapter and reported over
`GetObservedIntegrationLevel` (`pkg/adapter/integrationlevel.go:47-70`). Basic covers the message
socket alone, Standard adds the platform MCP server, and Full adds the runtime lifecycle channel.

### 2.5 Control plane

| Component | Role | Citation |
|:--|:--|:--|
| `lenny-controller` | Runs the WarmPool, Sandbox, and PodReconciler loops. Creates and deletes pods; the gateway cannot. | `cmd/lenny-controller/controllers.go:65`, `pkg/controller/sandbox/controller.go:215-219` |
| kube-apiserver | Holds Sandbox, SandboxClaim, and Pod objects, plus the gateway leader-election Lease. | gateway client at `cmd/lenny-gateway/stores.go:1977` |
| Redis | Coordination leases, slot counters, the session-event relay, and the terminate pub/sub channel. | `pkg/gateway/storage/leasestore/leasestore.go:90-92`, `pkg/gateway/storage/slotcounter/slotcounter.go:246-248` |
| Postgres | Session rows, the `coordination_lease` mirror, checkpoint manifests, interactions, and audit. | `cmd/lenny-gateway/stores.go:1015` |
| Object store | Checkpoint chunk storage, reached directly by the pod over presigned URLs. | `pkg/adapter/checkpointtransport.go:27-49` |
| Token Service | Mints and revokes the per-session credential leases the gateway then delivers to the pod over `Adapter/AssignCredentials`. A chart-deployed gRPC service on port 50052. | `charts/lenny/templates/token-service-deployment.yaml`, port at `charts/lenny/values.yaml:413`, gateway client at `pkg/gateway/credentials/credassign/client.go:196` (`AssignCredentials`) and `:309` (`RevokeCredentials`) |
| `RequestInterceptor` | An optional external gRPC policy service the gateway calls on the pre-input and post-output interceptor chains. Fails closed by default. | constructed at `cmd/lenny-gateway/interceptors.go:201` through `interceptor.NewExternal` (`pkg/gateway/policy/interceptor/external.go:85`), call at `:135`, fail-closed default at `:61-64` |

The gateway's ClusterRole is `charts/lenny/templates/gateway-deployment.yaml:125-188`. On pods it grants
`get`, `list`, `watch`, and `patch` (`:176-178`). There is no `delete` on pods and no `pods/eviction`, so
a gateway replica cannot evict or delete an agent pod. Retirement is requested by stamping
`lenny.dev/drain-request` on the pod and is executed by the WarmPool controller
(`pkg/controller/warmpool/pod_reconciler.go:610-640`), after which the Sandbox reconciler deletes the
pod (`pkg/controller/sandbox/controller.go:215-219`). The same ClusterRole also grants
`sandboxwarmpools`, `sandboxtemplates`, and `runtimes` get/list/watch (`:137-139`), `sandboxes` get/list
(`:146-148`), `sandboxclaims` get/create/delete (`:160-162`), `sandboxclaims/status` get/patch
(`:168-170`), and `tokenreviews` create (`:186-188`).

---

## 3. Channel inventory

### 3.1 The table

Exclusivity answers the question "can two gateway replicas hold this at once", and names the granularity.
The subsections that follow do not run in channel-number order: C6 and C9 are the two mechanisms
section 1.4 warns are conflated, so they are treated in adjacent subsections (3.7 and 3.9) with C8
following at 3.10. C20, C21, and C22 are covered where the scenarios that use them are traced: C20 in
3.2, C21 in 4.8, and C22 in 4.8.

| # | Channel | Direction | Protocol | Endpoint | Exclusivity | Status | Purpose |
|:--|:--|:--|:--|:--|:--|:--|:--|
| C1 | `Adapter` gRPC service | gateway to pod | gRPC unary and streaming | pod IP, TCP 50051 | **Two replicas can hold connections to one pod.** No peer-identity check at the pod. Per-session exclusivity comes from the gateway-side Redis lease only. | WIRED | All session control and content |
| C2 | `Adapter/Attach` content stream | gateway to pod, bidirectional | gRPC bidirectional stream on C1 | same | Per (session, slot) binding check only. **No pod-side single-consumer guard.** One cached stream per session per replica. | WIRED | Message delivery and agent output |
| C3 | `Adapter/Checkpoint` stream | gateway to pod, bidirectional | gRPC bidirectional stream on C1 | same | One running operation per pod, one pending per `slotId`, enforced by the pod operation lock. | WIRED | Workspace capture |
| C4 | `Adapter/CoordinatorFence` | gateway to pod | gRPC unary on C1 | same | Per pod: one monotonic `lastFenced` counter. | WIRED | Coordinator handoff fence |
| C5 | `Adapter/CheckpointBarrier` | gateway to pod | gRPC unary on C1 | same | One waiting barrier gate per pod, unenforced against a second concurrent barrier. | WIRED | Quiesce-and-hold during gateway drain |
| C6 | `Adapter/LifecycleChannel` gRPC control stream | gateway opens, adapter pushes | gRPC bidirectional stream on C1 | same | One stream per pod, process-wide. A second opener is rejected `FailedPrecondition`. | **server WIRED, client UNWIRED** | Adapter-to-gateway control events |
| C7 | `GatewayControl` gRPC service | pod to gateway | gRPC unary | `lenny-gateway.<ns>.svc:50051`, a ClusterIP VIP | None. One connection per pod process to an arbitrary replica. | WIRED | Platform and connector tool dispatch, scrub reports |
| C8 | Runtime message socket | adapter to runtime, intra-pod | Unix socket, JSON Lines | `@lenny-runtime` | One connection per pod. Output fans out to every subscriber. | WIRED | Agent message plane |
| C9 | Runtime lifecycle channel | adapter to runtime, intra-pod | Unix socket, JSON Lines | flag-supplied path; no in-code constant exists (3.9) | One listener per adapter process, one connection served at a time. | **UNWIRED at the deployment boundary** | Cooperative quiesce, interrupt acknowledgement, credential rotation |
| C10 | Platform MCP socket | runtime to adapter, intra-pod | Unix socket, JSON-RPC 2.0 | `@lenny-platform-mcp` | Unrestricted concurrent connections, scoped per session by nonce. | WIRED | Platform tool calls, forwarded over C7 |
| C11 | Connector MCP sockets | runtime to adapter, intra-pod | Unix socket, JSON-RPC 2.0 | `@lenny-connector-<id>` | One server per (session, connector). | WIRED | Connector tool calls, forwarded over C7 |
| C12 | LLM proxy | runtime to gateway | HTTP/1.1 and SSE, plaintext at the process | gateway TCP 8443, path `/llm-proxy/v1/messages` | **None.** Any number of concurrent requests per lease token, across replicas. | gateway half WIRED, pod-side client ABSENT in this repository, chart-gated off by default | Proxy-mode LLM calls |
| C13 | Object store | adapter to store | HTTPS PUT and GET with presigned URLs | MinIO TCP 9443 or cloud TCP 443 | One PUT per chunk index, bounded by the gateway grant window. | WIRED | Checkpoint chunk upload and restore |
| C14 | Redis coordination lease | gateway to Redis | Redis Lua compare-and-set | `t:<tenant>:lease:session:<session>` | **Per (tenant, session), never per pod.** Exactly one holder, TTL 60s. | WIRED | Cross-replica session coordination |
| C15 | Postgres `coordination_lease` mirror | gateway to Postgres | SQL upsert and select | table `coordination_lease` | Single-valued row per (tenant, session). A projection rather than an exclusion primitive. | write WIRED (sweeper only), routing read UNWIRED | Barrier-target enumeration |
| C16 | Redis event relay | gateway to Redis Streams | Redis `XADD` and `XRANGE` | per-session stream | Shared. | publish and history WIRED, live tail UNWIRED | Cross-replica SSE backlog |
| C17 | SandboxClaim | gateway to kube-apiserver | Kubernetes CREATE, GET, status PATCH, and DELETE | `claim-<podName>` | **Per pod, cluster-wide, on first acquisition only.** Subsequent slots on an occupied pod are not replica-exclusive. | WIRED | Pod acquisition |
| C18 | Redis slot counter | gateway to Redis | Redis Lua get-compare-increment | `lenny:pod:<pod>:active_slots` | Per pod, atomic across replicas. | WIRED | Concurrent-slot capacity ceiling |
| C19 | Gateway to gateway | replica to replica | none | none | Not applicable. | **ABSENT**, and blocked by NetworkPolicy | Would carry the inter-replica control forward |
| C20 | `grpc.health.v1.Health` service | would be gateway to pod | gRPC unary and streaming on C1's connection | same as C1 | No guard; the service is stateless. | **server WIRED, client ABSENT** | Would carry adapter liveness probing |
| C21 | Postgres `agent_pod_state` mirror | controller writes, gateway reads | SQL upsert and select | table `agent_pod_state` | Single row per pod. Written by the WarmPool controller only. | WIRED | Pod-phase input to the orphan-session reconciler |
| C22 | Admission webhook to gateway internal HTTP | webhook pod to gateway | HTTP/1.1 on the gateway internal port | `/internal/drain-readiness`, `/internal/runtime-upgrade/active`, `/internal/audit/node-drain-forced` | None; unauthenticated, scoped by NetworkPolicy. | WIRED, feature-gated off by default | Drain-readiness admission on `pods/eviction` |

### 3.2 C1: the `Adapter` gRPC service

The gateway is the client and the pod adapter is the server. The client is
`adapterclient.Dial(addr, dialOpt, keepaliveOpt)` at `cmd/lenny-gateway/stores.go:2097`, installed as
`podBinder.DialAdapter` at `stores.go:2096`. The address is
`net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))`
(`pkg/gateway/podlifecycle/podsession/binder.go:1683`). Keepalive is 10s with a 5s timeout and
`PermitWithoutStream: true` (`stores.go:2023-2026`), mirrored server-side at
`cmd/lenny-adapter/main.go:252-262`.

Every RPC declared on `service Adapter` (`schemas/lenny-adapter.proto:32-228`) has a server-side handler.
Two have no production gateway caller, and two more are reachable from the gateway but can only fail at
the deployment boundary (see the note after the table and 6a.7).

| RPC | Handler | Gateway client wrapper | Production caller | Status |
|:--|:--|:--|:--|:--|
| `PrepareWorkspace` | `pkg/adapter/staging.go:31` | `adapterclient/client.go:273`, `:280` | `binder.go:1315`, `slotbinder.go` staging, `upload_to_session.go:129` | WIRED |
| `FinalizeWorkspace` | `staging.go:157` | `client.go:349`, `:356` | `binder.go:904`, `slotbinder.go:283` | WIRED |
| `RunSetup` | `staging.go:317` | `client.go:385`, `:392` | `binder.go:916`, `slotbinder.go:291` | WIRED |
| `StartSession` | `session.go:76` | `client.go:138` | `binder.go:996`, `slotbinder.go:305` | WIRED |
| `ConfigureWorkspace` | `sdkwarm.go:199` | `client.go:160` | `binder.go:992` | **WIRED gateway-side, dead at the deployment boundary** |
| `SendMessage` | `session.go:175` | `client.go:426` | none | **UNWIRED** |
| `Attach` | `attach.go:28` | `client.go:841` | `pkg/gateway/session/executor/pod.go:154` | WIRED |
| `AssignCredentials` | `credentials.go:66` | `client.go:190`, `:198` | `binder.go:1240`, `slotbinder.go:400` | WIRED |
| `RotateCredentials` | `credentials.go:108` | `client.go:236` | `cmd/lenny-gateway/cred_fallback.go:76`, `cred_renewal.go:258` | WIRED |
| `ExtendCredentialLease` | `credentials.go:150` | `client.go:255` | `cmd/lenny-gateway/cred_renewal.go:328` | WIRED |
| `RevokeCredentials` | `credentials.go:326` | none | none | **UNWIRED** |
| `Interrupt` | `lifecycle.go:21` | `client.go:462` | `pkg/gateway/sessionserver/interrupt.go:134` | WIRED |
| `Checkpoint` | `checkpoint.go:76` | `client.go:515` | `checkpointer.go:522` | WIRED |
| `SignalDeadline` | `lifecycle.go:72` | `client.go:486` | `pkg/gateway/sessionserver/expiry_warning.go:66` | WIRED |
| `Resume` | `resume.go:25` | `client.go:696` | `binder.go:1591` | WIRED |
| `CoordinatorFence` | `coordination.go:85` | `coordinatorfence.go:48` | `coordfence.go:160`, reached from `start.go:4128` and `coordination_seams.go:234` | WIRED |
| `CheckpointBarrier` | `coordination.go:212` | `client.go:546` | `barrier/wiring.go:49` | WIRED |
| `ExportPaths` | `exportpaths.go:35` | `client.go:588` | `exportwire.go:68` | WIRED |
| `ReportUsage` | `usage.go:260` | `client.go:780`, `:814` | `cmd/lenny-gateway/direct_usage.go:275` | WIRED |
| `Shutdown` | `session.go:218` | `client.go:889`, `:896`, `:927`, `:973` | `binder.go:1945`, `:1951`, `slotbinder.go:539`, `user_revocation.go:129` | WIRED |
| `DemoteSDK` | `sdkwarm.go:266` | `client.go:176` | `binder.go:865` | **WIRED gateway-side, dead at the deployment boundary** |
| `NegotiateVersion` | `server.go:404` | `client.go:85` | `binder.go:1109`, `:1689`, `slotbinder.go:240`, `:462` | WIRED |
| `GetObservedIntegrationLevel` | `integrationlevel.go:65` | `client.go:98` | `podsession/integrationlevel.go:91` | WIRED |
| `LifecycleChannel` | `controlchannel.go:108` | none | none | **UNWIRED** (see 3.7) |

Two names in the specification's RPC table do not exist as RPCs. `Terminate` is a gateway-side helper
(`adapterclient/client.go:927`) that calls the `Shutdown` RPC with `reason` and `deadline_ms` set; its
own doc comment says so at `client.go:924-926`, and its only production caller is
`cmd/lenny-gateway/user_revocation.go:129`. `PreConnect` is an in-process adapter method
(`pkg/adapter/sdkwarm.go:151`) rather than an RPC, and it has no caller in `cmd/lenny-adapter`, so a
production SDK-warm pod never pre-connects and `Server.SDKWarmReady()` (`sdkwarm.go:177`) can only
report false there.

**The two SDK-warm RPCs cannot succeed in a chart-rendered pod.** Both handlers gate on
`s.sdkWarmRuntime()` and return `codes.Unimplemented` when the type assertion fails:
`pkg/adapter/sdkwarm.go:211-214` for `ConfigureWorkspace` and `:267-270` for `DemoteSDK`. The assertion
requires the runtime to implement `SDKWarmRuntime` (`sdkwarm.go:119-135`). `cmd/lenny-adapter` installs
only `adapter.NewSocketRuntimeProcess` (`main.go:354`, the sidecar model) or
`executor.NewSubprocessExecutor` (`:363`, the developer loop), and neither declares `PreConnect`,
`ConfigureWorkspace`, or `DemoteSDK`. The only `SDKWarmRuntime` implementation in the tree is
`SDKWarmInProcessRuntime` (`pkg/adapter/embedded_sdkwarm.go:32`), installed solely by the demo binary
`cmd/runtimes/preconnect-echo/main.go:116`. Section 6a.7 traces what that does to a `preConnect: true`
pool.

**A second gRPC service is registered on the same listener.** `pkg/adapter/transport.go:52-54`
registers `grpc.health.v1.Health` and sets it to `SERVING`. It is C20 in the inventory: the server half
is wired into every rendered pod and no client exists.

```
$ grep -rn "grpc_health_v1\|healthv1\|HealthClient" --include=*.go pkg/gateway cmd/lenny-gateway pkg/controller | grep -v _test
(no output)
```

The function's own doc comment at `pkg/adapter/transport.go:24-26` says "the gateway probes adapter
liveness through it (§4.7)". No gateway code does. The gateway's actual liveness signal is
`bind.Adapter.Alive()`, which reads the gRPC connectivity state
(`cmd/lenny-gateway/coordination_seams.go:58-67`). `Health/Check` and `Health/Watch` are also two of the
five methods the hold-state allowlist admits (`holdstate.go:54-60`), which section 6a.2 covers.

**Transport authentication.** `TLSServerOption(certFile, keyFile, clientCAFile, mods...)`
(`pkg/adapter/transport.go:92`) returns `(nil, nil)` when the certificate and key are both empty
(`transport.go:93-95`). The podspec passes no TLS flags on either deployment model and mounts no
certificate volume: `grep -n "tls-cert-file\|tls-key-file\|tls-client-ca" pkg/controller/sandbox/podspec/podspec.go`
returns nothing. The adapter's gRPC listener therefore serves plaintext in a chart-rendered pod, and
confidentiality on this hop rests on the NetworkPolicy alone. The proto header asserts mTLS
(`schemas/lenny-adapter.proto:26-31`) and so does `transport.go:80-86`; the code disagrees.

Even when a client CA is configured, `TLSServerOption` sets `ClientAuth = tls.RequireAndVerifyClientCert`
and a CA pool (`transport.go:108-119`) but installs no `VerifyPeerCertificate` callback, because the
production sidecar passes no `TLSConfigMod` (`cmd/lenny-adapter/main.go:238`). Any certificate signed by
the cluster CA is accepted. The pod's transport layer cannot tell one gateway replica from another, so
every exclusivity guarantee on this surface is application-level.

**Exclusivity.** Two gateway replicas can hold simultaneous connections to the same pod. Nothing in
`pkg/adapter` records the peer identity of a connection. What the pod does enforce, per session, is
`claimSession` (`pkg/adapter/session.go:416-425`), which rejects a `StartSession` against a non-idle pod
with `codes.Unavailable`, and `checkSession` (`session.go:401-411`), which compares the requested session
id against the pod's assigned one. `checkSession` is applied by `SendMessage` (`session.go:198`),
`Attach` (`attach.go:45`), `Shutdown` (`session.go:247`), `Interrupt` (`lifecycle.go:30`),
`SignalDeadline` (`lifecycle.go:77`), `ReportUsage` (`usage.go:265`), `CoordinatorFence`
(`coordination.go:90`), and `CheckpointBarrier` (`coordination.go:217`). It is not applied by
`Checkpoint`, `ExportPaths`, `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `NegotiateVersion`, or
`GetObservedIntegrationLevel`. So two replicas both holding a connection to a pod that has claimed
session `S` can both successfully issue `Attach(S)`, `Interrupt(S)`, `Checkpoint`, and `ExportPaths(S)`.

**Hold-state interceptors.** `pkg/adapter/transport.go:46-47` chains `s.holdStateUnaryInterceptor` and
`s.holdStateStreamInterceptor`, so every inbound RPC consults `inHoldState()`
(`pkg/adapter/holdstate.go:245-263`), and a non-allowlisted method would get `codes.Unavailable` with a
`coordinator_hold:` prefix (`holdstate.go:267-270`). The allowlist is `CoordinatorFence`,
`NegotiateVersion`, `LifecycleChannel`, `Health/Check`, and `Health/Watch` (`holdstate.go:54-60`). The
enforcement is wired; nothing arms it in production (see 6a.2).

### 3.3 C2: the `Attach` content stream

`PodExecutor.streamFor` (`pkg/gateway/session/executor/pod.go:128-160`) opens
`bind.Adapter.Attach(ctx, sessionID, bind.SlotID)` at `pod.go:154` lazily on the first `Send`, caches it
in `e.streams[sessionID]` at `pod.go:158`, and holds `e.mu` across the open so one replica never races
two streams into existence (`pod.go:129-130`). The client sends a binding frame carrying `session_id`
and an optional `slot_id` immediately (`adapterclient/client.go:841-856`).

The pod handler (`pkg/adapter/attach.go:28-73`) binds on the first frame, validates only the session id
through `checkSlotSession` (`attach.go:42`) or `checkSession` (`attach.go:45`), checks for a nil runtime
(`attach.go:48-51`), and subscribes to runtime output at `attach.go:60`. A slot-qualified stream
demultiplexes the shared runtime output by `slotId` (`attach.go:71-73`, filter at `:278-306`); a
no-slot stream reads the raw stream unfiltered (`attach.go:70`), which on a concurrent pod would see
every slot's frames. The gateway fails closed against that case: a bind with
`MaxConcurrentSessions > 1` and an empty `SlotID` returns `ErrSlotIDRequired` (`pod.go:145-148`).

**Exclusivity: none at the pod.** The whole of `attach.go` is 306 lines and contains no stream registry,
no counter, and no duplicate rejection. Two concurrent `Attach` streams for the same session are both
admitted, and what happens next depends on the runtime implementation.

| Runtime | Status | `Output` behavior | Effect of two Attach streams |
|:--|:--|:--|:--|
| `SocketRuntimeProcess`, the sidecar model | WIRED, `cmd/lenny-adapter/main.go:354` | creates a new subscriber per call and registers it (`pkg/adapter/socketruntime.go:343-359`) | both streams receive every frame, so the same `response` envelope is delivered twice |
| `SubprocessExecutor`, the developer loop under `--runtime-bin` | WIRED, `cmd/lenny-adapter/main.go:363` | spawns a goroutine per call over the shared `sess.stdout` scanner (`pkg/gateway/session/executor/subprocess.go:234-253`) | the two goroutines split frames off one scanner, against the single-caller contract at `subprocess.go:232-233` |
| `InProcessRuntime`, the embedded model | UNWIRED in `cmd/lenny-adapter`; constructed only by `cmd/runtimes/echo-embedded/main.go:144` and `cmd/runtimes/preconnect-echo/main.go:116` | creates a new scanner over the same reader (`pkg/adapter/embedded.go:113-138`) | interleaved reads on one pipe |
| `MCPRuntime` | **UNWIRED**; `grep -rn "MCPRuntime" pkg/adapter/*.go cmd/lenny-adapter/*.go \| grep -v _test` returns the type, its methods, and two comments at `pkg/adapter/server.go:54` and `:210` instructing a caller to wire it. Nothing does. | returns the same channel (`pkg/adapter/mcpruntime.go:226-235`) | the two consumers steal frames from each other |

Both streams can also write to the runtime concurrently through `attachRecvLoop` and `writeSlotEnvelope`
(`attach.go:237-266`). The single-consumer property named in the comment at `pod.go:36-37` lives
entirely in one gateway replica's memory, and the coordination lease is what keeps a second replica from
opening a competing stream.

Two further properties of the cache are load-bearing and are covered in section 4.2: the stream is
opened with the first HTTP request's context, and concurrent sends on one session share the stream with
no serialization.

### 3.4 C3: the `Checkpoint` stream

Granularity is one running checkpoint per pod, with one pending checkpoint per distinct `slot_id`. The
adapter takes `s.ops.Begin(ctx, opCheckpoint, start.GetSlotId().GetValue())` at
`pkg/adapter/checkpoint.go:113`, and a busy lock returns `codes.Aborted` (`checkpoint.go:115-118`). The
operation lock (`pkg/adapter/oplock.go:74-114`) returns `errOpCoalesced` when the same slot is already
pending (`:103-106`), queues a distinct slot (`:107-113`), and returns `errOpBusy` behind a pending
interrupt (`:84-87`). Promotion order is a pending interrupt first, otherwise the lowest slot id
(`oplock.go:162-184`).

The gateway mints the `checkpoint_id` (`checkpointer.go:526`); the adapter never mints one. The
per-(session, slot) attempt is additionally serialized inside one replica by
`c.lockFlight(sessionID, slotID)` (`checkpointer.go:508-509`), which is a process-local `sync.Map` and
excludes nothing across replicas.

`CheckpointStart` carries no `coordination_generation`
(`schemas/lenny-adapter.proto:1101-1121`), so two gateway replicas racing a checkpoint against one pod
are separated by the operation lock alone rather than by a fencing token.

### 3.5 C4: `CoordinatorFence`

Per pod, monotonic, and exempt on the first fence. The handler requires the pod to already hold the
session through `checkSession` (`pkg/adapter/coordination.go:90`) and a strictly positive generation
(`:94-96`). Once initialized, a generation at or below `lastFenced` returns both a
`FailedPrecondition` status and a non-nil response body carrying `Accepted: false`
(`coordination.go:100-108`); the gateway ignores the body on error
(`adapterclient/coordinatorfence.go:55-57`). A skipped generation logs `coordinator_generation_gap` and
sets `GapDetected` (`coordination.go:109-119`) without cancelling in-flight RPCs; the handler's own doc
at `coordination.go:81-82` states that the cancellation requirement is unimplemented. A successful
fence calls `s.exitHoldState()` (`coordination.go:130`), the only exit from hold state.

The gateway side is bounded by a hard five-second per-call timeout
(`adapterclient/coordinatorfence.go:20`, applied at `:49`).

### 3.6 C5: `CheckpointBarrier`

The handler's ordered guards are: empty session id, then `checkSession` (`coordination.go:217`), then
empty `barrier_id`, then `gen <= 0` returning `codes.InvalidArgument` (`coordination.go:225-227`), and
only then the equality gate `!initialized || gen != fenced` returning `codes.FailedPrecondition` with
`coordinator_handoff_stale` (`coordination.go:237-240`). The ordering matters, because
`pkg/gateway/coordination/barrier/wiring.go:51-53` converts only `codes.FailedPrecondition` into
`ErrGenerationStale`. A never-fenced session on the ordinary start path carries generation 0 and is
therefore rejected `InvalidArgument`, which the dispatcher records as a plain error rather than as a
benign stale outcome.

On acceptance the handler sets `s.coord.quiesced = true` (`coordination.go:248`), captures
`quiescedMs` immediately so it measures time-to-quiescence rather than the upload
(`coordination.go:253`), blocks on `s.barrier.open()` until the linked Checkpoint stream terminates or
the context expires (`coordination.go:265-270`), and returns
`CheckpointBarrierResponse{barrier_id, checkpoint_ref, quiesced_ms}` (`coordination.go:272-284`).

**The quiesce flag is advisory.** `s.coord.quiesced` is written at `coordination.go:248` and `:256` and
read only by `isQuiescedForBarrier` (`coordination.go:52`), whose doc says it is exposed for tests. No
RPC handler consults it, and the struct comment concedes the point at `coordination.go:35-37`. The
barrier therefore holds the acknowledgement without blocking any operational RPC.

**The gate is per pod.** `barrierGate` is a single field on the adapter `Server`
(`pkg/adapter/server.go:318`, type at `coordination.go:149-155`), and `open()` unconditionally resets
`waiting`, `checkpointID`, `signaled`, and `done` (`coordination.go:159-167`). A second concurrent
barrier overwrites the first's channel, and the first blocks until its own context expires.

### 3.7 C6: the gRPC control stream `Adapter/LifecycleChannel`

This is the gateway-to-adapter streaming RPC, distinct from the runtime lifecycle channel of 3.9.

**The server half is fully wired into the production adapter binary.** The handler is
`pkg/adapter/controlchannel.go:108`, registered by `adapterv1.RegisterAdapterServer(gs, s)` at
`pkg/adapter/transport.go:50` inside `NewGRPCServer` (`transport.go:27`), which
`cmd/lenny-adapter/main.go:404` calls and `main.go:428` serves.

**The client half does not exist.**

```
$ grep -rn "LifecycleChannel" pkg/gateway cmd/lenny-gateway --include=*.go | grep -v _test.go
pkg/gateway/runtime/adapterclient/client.go:543:// emits on the LifecycleChannel control stream.
```

One hit, and it is a prose comment. `adapterclient.Client` declares no `LifecycleChannel` method. The
generated client alias `Adapter_LifecycleChannelClient`
(`pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:507`) has zero users.

**Consequence.** `s.controlSink` (`pkg/adapter/server.go:336-338`) is nil in every deployed pod, so
`emitControlEvent` (`controlchannel.go:167-183`) takes the `sink == nil` branch at `:174-176`,
increments `lenny_adapter_control_events_dropped_total{reason="no_stream"}`, and returns. Events split
into those with a production producer and those with none.

| Event | Production producer | Fate |
|:--|:--|:--|
| `AUTH_EXPIRED` | `pkg/adapter/credexpiry.go:182`, `pkg/adapter/slotcreds.go:247` | emitted, dropped |
| `LEASE_REJECTED` | `pkg/adapter/credentials.go:237` | emitted, dropped |
| `CheckpointBarrierAck` | `pkg/adapter/coordination.go:282`, inside the `CheckpointBarrier` handler | emitted, dropped; the synchronous return at `:284` is the live path |
| `FINAL_USAGE_REPORT` | `pkg/adapter/session.go:343` | emitted, dropped |
| `AdapterTerminating` | only `pkg/adapter/holdstate.go:174`, itself unreachable | never produced |
| `AdapterEvicting` | none | never produced |
| `RATE_LIMITED` | none | never produced |
| `PROVIDER_UNAVAILABLE` | none | never produced |

**Exclusivity if it were wired: one stream per pod, process-wide.**
`pkg/adapter/controlchannel.go:110-115` takes `s.controlMu` and returns
`codes.FailedPrecondition, "lifecycle control channel already open for this pod"` when the sink is
non-nil. The guard is not per session and not per slot: one `Server`-level sink fans events for every
co-tenant session on a concurrent pod, discriminated only by an optional `slotId` on the envelope
(`controlchannel.go:73-78`). Two gateway replicas could therefore not both hold it, which is in tension
with the shared-pod model where different replicas coordinate different slots on one pod. The buffer is
64 events (`controlchannel.go:63`); overflow drops with `reason="buffer_full"` (`:178-182`).

The stream's close is also the only entry into hold state: the handler's defer at
`controlchannel.go:118-128` calls `s.onCoordinatorChannelClosed()` at `:125`. Section 6a.2 traces the
consequence.

### 3.8 C7: the `GatewayControl` service

The reverse direction. The gateway hosts the server and the adapter dials it as a client
(`schemas/lenny-adapter.proto:230-246` states the direction; client construction at
`pkg/adapter/gatewaylink.go:36-79`). Its RPCs are `ListPlatformTools`, `CallPlatformTool`,
`ListSessionConnectors`, `ListConnectorTools`, `CallConnectorTool`, `ReportSessionScrub`, and
`ReportPodScrub` (`schemas/lenny-adapter.proto:254-321`). All are implemented on both sides and all have
production callers.

Lease extension is deliberately not on this service. The proto states so at
`schemas/lenny-adapter.proto:244-246`, and the gateway drives budget-exhaustion extension in process
through `leaseExtendSeam` (`cmd/lenny-gateway/controlserver.go:188`, definition `:283`). The adapter's
own `--gateway-grpc-addr` help text still advertises the address as the one it dials "to forward
platform tool calls (and lease extensions)" (`cmd/lenny-adapter/main.go:157-158`). The help text is
stale; the proto and the code agree with each other.

| RPC | Adapter-side production caller | Gateway handler |
|:--|:--|:--|
| `ListPlatformTools` | `pkg/adapter/platformtoolprovider.go:33` from `pkg/adapter/mcp/server.go:294` | `leasecontrol/platformtools.go:60` |
| `CallPlatformTool` | `platformtoolprovider.go:37`, and `pkg/adapter/tracingcontext.go:56` | `leasecontrol/platformtools.go:97` |
| `ListSessionConnectors` | `pkg/adapter/connectormcp.go:36` | `leasecontrol/connectortools.go:66` |
| `ListConnectorTools` | `pkg/adapter/connectortoolprovider.go:37` | `leasecontrol/connectortools.go:96` |
| `CallConnectorTool` | `connectortoolprovider.go:41` | `leasecontrol/connectortools.go:138` |
| `ReportSessionScrub` | `pkg/adapter/sessionscrubreporter.go:80` from `slotsession.go:94` and `session.go:284` | `leasecontrol/scrubreport_server.go:75` |
| `ReportPodScrub` | `pkg/adapter/podscrub.go:69` from `session.go:244` and `:285` | `leasecontrol/scrubreport_server.go:113` |

**The target is a load-balanced ClusterIP Service.** The controller stamps
`--gateway-grpc-addr=lenny-gateway.<namespace>.svc:50051`
(`charts/lenny/templates/controller-deployment.yaml:94`), plumbed through
`cmd/lenny-controller/flags.go:103`, `cmd/lenny-controller/controllers.go:65`,
`pkg/controller/sandbox/controller.go:392`, and `podspec.go:833-841`. `lenny-gateway` is a normal
selector Service of type ClusterIP declaring no `sessionAffinity`
(`charts/lenny/templates/gateway-deployment.yaml:1628-1663`).

`ConnectGateway` runs once at adapter process start (`cmd/lenny-adapter/main.go:328`), before any
session exists, and stores one client on four `Server` fields (`gatewaylink.go:58-77`). So one
`grpc.ClientConn` per pod process serves every session, slot, connector socket, and scrub report for
the pod's lifetime, and kube-proxy selects the backend replica at connection establishment. **A pod's
scrub report lands on an arbitrary replica, which on a shared pod cannot be the replica that holds
every slot.** There is no keepalive on this dial (`gatewaycontrol.go:61-68`, `gatewaylink.go:49-54`),
in contrast with the gateway-to-pod direction.

**No exclusivity guard exists anywhere on this surface.** The handlers contain no lease check, no
generation fence, no per-pod token, and no stream registry. Two gateway replicas can serve
`GatewayControl` calls for the same pod, session, and slot concurrently.

**Authentication is asymmetric and currently broken under the default chart.** The gateway's listener
requires mTLS with client-certificate verification, a SPIFFE URI SAN verifier, an in-process
verified-peer gate, and optionally a projected service-account token
(`cmd/lenny-gateway/main.go:667`, `:650-666`, `:692`, `:693`). The adapter presents none of it: the
podspec emits no TLS material (3.2), so `TLSClientOption("","","")` returns insecure credentials at
`pkg/adapter/transport.go:138-140` before the modifier loop at `:163-165`, and
`gatewaycontrol.WithSAToken` (`pkg/adapter/gatewaycontrol/satoken.go:64`) has no caller outside its own
test. The podspec does project the token volume (`podspec.go:196-209`, `:628`), so the token is mounted
and unread.

### 3.9 C9: the runtime lifecycle channel (adapter to runtime, Unix socket)

This is the §4.7 Part B channel. It is a different mechanism from C6 and carries a different frame
vocabulary from C8.

**Implemented and wired inside the adapter binary, conditionally.** `cmd/lenny-adapter/main.go:373`
calls `adapter.NewLifecycleChannel(*lifecycleSocket)` and assigns `adapterSrv.Lifecycle` at `:377`, only
when the flag is non-empty. The flag is declared at `main.go:151-152` with default `""` and the help
text "empty disables it". The channel's goroutine runs at `main.go:396-401`.

**No component ever passes the flag.**

```
$ grep -rn "lifecycle-socket" . --exclude-dir=.git
cmd/lenny-adapter/main.go:151:	lifecycleSocket := flag.String("lifecycle-socket", "",
BUILD-GAPS.md:1656   (historical narrative)
BUILD-GAPS.md:5440   (historical narrative)
```

Both podspec argument builders were read in full. The sidecar `adapterArgs`
(`pkg/controller/sandbox/podspec/podspec.go:562-584`) emits `--addr`, `--workspace-root`,
`--staging-dir`, `--runtime-uid`, and `--runtime-socket`, plus four appenders (`sharedAssetsArgs`
`:818-824`, `platformMCPArgs` `:833-841`, `nonceOnlyArgs` `:849-855`, `checkpointProbeArgs` `:865-875`).
The embedded `embeddedArgs` (`podspec.go:663-678`) emits the same set minus the runtime socket. Neither
emits `--lifecycle-socket`, and there is no `LifecycleSocketName` constant beside `RuntimeSocketName`
(`podspec.go:159`) and `PlatformMCPSocketName` (`podspec.go:184`).

**The conventional name has no source in code.** `@lenny-lifecycle` appears only in prose artifacts:
`docs/reference/adapter-contract.md:452`, `:508`, `docs/api/internal.md:320`,
`docs/runtime-author-guide/integration-levels.md:137`, `spec/15_external-api-surface.md:2450`, and
`TESTING.md:1996`. Under the evidence rule of 1.3 those are leads. The socket path is whatever
`--lifecycle-socket` carries, and nothing sets it.

**Status: WIRED inside the binary, UNWIRED at the deployment boundary.** `Server.Lifecycle` is nil in
every chart-rendered pod.

**Protocol.** The adapter listens (`lifecyclechannel.go:135`) and the runtime dials. The adapter speaks
first with `lifecycle_capabilities` carrying protocol version `1.0`
(`lifecyclechannel.go:30`, `:301-305`) and the fixed capability set
`["checkpoint", "interrupt", "credential_rotation", "deadline_signal"]` (`lifecyclechannel.go:48`). The
runtime replies `lifecycle_support` with a subset (`:308-333`). Adapter-to-runtime frames after that are
`checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`,
`deadline_approaching`, `terminate`, and `files_updated`. Runtime-to-adapter frames are
`checkpoint_ready`, `interrupt_acknowledged`, `credentials_acknowledged`, `llm_request_started`, and
`llm_request_completed`. Unknown frame types are silently dropped (`:386-407`, no default case).

No frame carries a `sessionId` or a `slotId` (`lifecyclechannel.go:59-83`). The channel is pod-scoped,
and on a concurrent pod every slot shares it.

**Advertisement.** The socket is published only through the adapter manifest, and only when the channel
exists: `pkg/adapter/manifest.go:271-273` sets `m.LifecycleChannel` when `s.Lifecycle != nil`, and the
field is `omitempty` (`manifest.go:154-157`). A runtime reading the manifest in a chart-rendered pod
correctly concludes the channel is unavailable and never dials.

**Exclusivity.** One listener per adapter process, and connections are served strictly serially:
`Run` (`lifecyclechannel.go:180-198`) calls `Accept`, then `serveConn`, which blocks for the whole life
of the connection, then `resetConn`, then loops. A second runtime dialing concurrently sits in the
kernel backlog.

**A capability gate is missing.** `Supports` (`lifecyclechannel.go:536`) has three production call
sites: `credentials.go:128` for `credential_rotation`, `lifecycle.go:51` for `interrupt`, and
`lifecycle.go:80` for `deadline_signal`. None gates `checkpoint`; `checkpoint.go:159` tests only
`s.Lifecycle != nil`. A runtime that legally omits `checkpoint` from its `lifecycle_support` subset
(schema `schemas/lifecycle-events.schema.json:52-63`) is still sent `checkpoint_request` and the RPC
blocks to its deadline.

### 3.10 C8: the runtime message socket

`SocketRuntimeProcess` (`pkg/adapter/socketruntime.go:60`), constructed at
`cmd/lenny-adapter/main.go:354` from `--runtime-socket`. `WriteEnvelope` writes the envelope plus a
newline to the single connection (`socketruntime.go:324-335`) and ignores the session id. Inbound frames
are scanned with a 50 MB cap (`socketruntime.go:20`, `:216`) and broadcast to every subscriber by
`fanOut` (`socketruntime.go:240-245`). `Output` appends a new subscriber per call
(`socketruntime.go:343-360`), which is what makes two concurrent Attach streams duplicate frames.

**The adapter heartbeats the runtime on this socket and escalates to SIGTERM.** `startHeartbeat` runs
one `heartbeatMonitor` (`pkg/adapter/heartbeat.go:34-45`) per Attach stream, started at
`pkg/adapter/attach.go:78`. The monitor writes a `{type:heartbeat,ts}` frame every
`HeartbeatInterval` and expects a `heartbeat_ack` within `HeartbeatAckTimeout`. Both are set
unconditionally in the production sidecar from `--heartbeat-interval-seconds`, defaulting to 30, and
`--heartbeat-ack-timeout-seconds`, defaulting to 10 (`cmd/lenny-adapter/main.go:183-188`, assigned at
`:301-302`). On a missed acknowledgement the Attach select loop calls `s.onHeartbeatHung`, which
SIGTERMs the runtime process, and returns `codes.DeadlineExceeded` to the gateway
(`attach.go:141-147`). This is a live failure mode: it terminates the content stream and kills the
agent process with no checkpoint. Section 7.3 records the edge.

Because the monitor is per Attach stream rather than per pod, the two concurrent Attach streams the
exclusivity finding above admits also start two heartbeat monitors against one runtime.

### 3.11 C10 and C11: the platform and connector MCP sockets

These are the two intra-pod sockets that carry the tool plane, in the reverse direction from C8: the
runtime dials and the adapter serves.

C10 is the platform MCP server, bound on `@lenny-platform-mcp`
(`pkg/controller/sandbox/podspec/podspec.go:184`, emitted onto the adapter's `--mcp-socket` by
`platformMCPArgs` at `podspec.go:833-841`). `startPlatformMCP` (`pkg/adapter/platformmcp.go:16-51`) is
called from `StartSession` (`pkg/adapter/session.go:150`) and serves the catalog through
`platformToolProvider` (`platformmcp.go:43`), which forwards every `tools/call` over C7. The server
runs until `releaseSession` cancels it (`platformmcp.go:48`).

C11 is one MCP server per (session, connector), bound on `@lenny-connector-<id>`
(`pkg/adapter/connectormcp.go:121-126`). `startConnectorMCPServers` (`connectormcp.go:59-69`) is called
from `StartSession` (`session.go:158`) and starts one server per admitted connector, best-effort: a
listen failure is logged and the remaining connectors still serve (`connectormcp.go:64-67`). Each
forwards over C7 scoped to the session and that one connector (`connectormcp.go:85`).

**Authentication is the manifest nonce plus an optional peer-credential check.** Both sockets bind
through `listenIntraPodMCP` (`connectormcp.go:94` onward), which applies an `SO_PEERCRED` check against
the configured runtime UID. When the peer check is disabled the servers add a per-connection
challenge-response instead, because a static nonce is otherwise replayable
(`platformmcp.go:28-30`, `connectormcp.go:81-84`).

**Exclusivity.** Neither socket restricts concurrent connections. Scoping is per session through the
nonce written into the manifest, so on a concurrent pod every slot's runtime connection reaches the same
pod-global `s.sessionID` captured at `platformmcp.go:41`.

### 3.12 C12: the LLM proxy

The gateway serves `POST /llm-proxy/v1/messages` on its own mux (`cmd/lenny-gateway/main.go:475`,
constructor `:470`). Authorization is the per-session lease token carried on the request.

**The pod-side client is not in this repository.** The runtime is expected to supply it; no first-party
outbound LLM client exists in `pkg/adapter`. The chart gates the surface off by default
(`charts/lenny/values.yaml:1695` for `features.llmProxy`, rendered at
`charts/lenny/templates/gateway-deployment.yaml:645-646`, read at `cmd/lenny-gateway/main.go:471-472`).

**Exclusivity: none.** Any number of concurrent requests can carry one lease token, across any number of
replicas, and no request is bound to the pod that holds the session. The SPIFFE binding that would tie a
request to its pod is never armed (6a.5), and the listener is plaintext at the process, so `r.TLS` is
always nil. Section 6c.7 traces the consequence.

### 3.13 C16: the Redis event relay

Session SSE events are published to a per-session Redis stream so a client that reconnects to a
different replica can replay what it missed. The relay is attached to the event bus only when Redis is
configured (`cmd/lenny-gateway/stores.go:2270`).

The publish path and the backlog read are both wired. `History` issues `XRANGE` over the whole stream
(`pkg/gateway/session/sessionevents/redisrelay.go:127`, the call at `:131`) and supplies the
cross-replica backlog at `events.go:420-425`. The live-tail reader `LiveFromCursor` is implemented and
has no caller. Section 5.6 traces what that costs a client attached to an off-holder replica.

### 3.14 C13: the object store

The only first-party outbound network client that runs inside the pod. `httpCheckpointTransport`
(`pkg/adapter/checkpointtransport.go:43-49`), constructed at `cmd/lenny-adapter/main.go:282-286`, with a
five-minute client timeout (`:72`), system roots plus an optional CA bundle from
`--objectstore-ca-bundle` (`:58-74`), and every signed header replayed verbatim on the PUT (`:76-97`).

The pod holds no standing object-store credential. Its authorization is the gateway-signed presigned URL
plus the signed header set. The MinIO backend folds the tenant server-side-encryption headers and the
exact `Content-Length` into the SigV4 signature (`pkg/blobstore/miniostore/miniostore.go:511-531`), so a
pod that strips a header or sends a different body length is rejected before any byte lands.

### 3.15 C14 and C15: the coordination lease and its mirror

**The Redis lease is authoritative.** Key `t:<tenant_id>:lease:session:<session_id>`
(`pkg/gateway/storage/leasestore/leasestore.go:10-11`, `:90-92`). `Acquire` is a Lua compare-and-set
that succeeds when the key is absent or already equals the caller's holder string, refreshing the
expiry (`leasestore.go:98-105`), and returns `ErrHeld` for a different holder (`:138-140`). It is
therefore idempotent for the current holder, which is what makes renew-on-sweep work. `Release` is
holder-checked (`:117-123`). Default TTL is 60 seconds
(`pkg/gateway/sessionserver/sessionserver.go:673`, applied at `:1948-1951`).

**Exclusivity: per (tenant, session), cross-replica, and never per pod.** The key contains no pod
identifier, so on a concurrent pod one lease exists per slot session and several replicas legitimately
hold leases against the same pod.

**The Postgres mirror is a projection.** `pkg/gateway/coordination/coordlease/coordlease.go:5-9` states
that the authoritative lease is in Redis and the sweeper mirrors it. The row carries `TenantID`,
`SessionID`, `CoordinatorReplica`, `CoordinatorAddress`, and `CoordinationGeneration`
(`coordlease.go:47-61`). `Upsert` is called only from
`pkg/gateway/coordination/coordination/coordination.go:569` inside the sweeper's per-session loop;
`Release` only from `coordination.go:586`; and `ListHeldByReplica` only from the barrier's target
lister (`barrier/wiring.go:104`, wired at `cmd/lenny-gateway/httpsurface.go:586-588`).

`GetBySession` (declared at `coordlease.go:91`, documented at `:83-90` as the eviction-drive routing
read) has no production caller. `CoordinatorAddress` is written from `s.interReplicaAddress` (`coordination.go:573`), which
comes from `Options.InterReplicaAddress` (`coordination.go:218`), which the only production
`NewSweeper` call never sets: `cmd/lenny-gateway/stores.go:1490-1497` passes exactly `ReplicaID`,
`Interval`, `Mirror`, `Bindings`, and `Readopter`. Every production mirror row therefore carries an
empty address.

**The mirror is never seeded at bind.** `pkg/gateway/sessionserver/` contains no reference to
`coordlease` at all. `acquireCoordinationLease` (`start.go:2890-2901`) touches only the Redis store. A
freshly bound session's mirror row first appears on the next sweeper tick, up to 15 seconds later
(interval default at `cmd/lenny-gateway/flags.go:528`).

### 3.16 C17 and C18: the Kubernetes claim and the slot counter

The per-pod `SandboxClaim` is named deterministically `claim-<podName>`
(`pkg/gateway/podlifecycle/podclaim/claimer.go:347-349`), so a second claim for the same pod collides at
CREATE (`claimer.go:335-338`), backed by the `lenny-sandboxclaim-guard` validating webhook
(`pkg/admission/sandboxclaim_guard/guard.go:15-33`). That guard is what makes the *first* acquisition of
an idle pod exclusive cluster-wide.

**The claim carries no holder identity.** `SandboxClaimSpec` has exactly `SandboxRef` and `TenantID`
(`pkg/apis/lenny/v1alpha1/sandboxclaim_types.go:20-32`). The slot placement Pass 1
(`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:412-470`) checks only that a claim exists, is
non-terminal, and matches the tenant. A claim created by one replica is a valid Pass-1 target for
another. The capacity ceiling is the shared Redis counter keyed on the pod alone
(`pkg/gateway/storage/slotcounter/slotcounter.go:196-205`, key `:246-248`). Nothing in the placement path
records which replica opened a slot.

### 3.17 C19: gateway to gateway

No gateway-to-gateway channel exists. The repository declares four gRPC services, and none is
gateway-to-gateway:

```
$ grep -rn "^service " schemas/*.proto
schemas/lenny-tokenservice.proto:28:service TokenService {
schemas/lenny-interceptor.proto:36:service RequestInterceptor {
schemas/lenny-adapter.proto:32:service Adapter {
schemas/lenny-adapter.proto:247:service GatewayControl {
```

The network layer blocks it as well. `charts/lenny/templates/system-network-policies.yaml:11-20` renders
`lenny-system/default-deny-all` selecting every pod with `policyTypes: [Ingress, Egress]`, and no
rendered policy names a gateway pod as a peer of another gateway pod.

Cross-replica coordination therefore runs entirely through shared backends: the Redis coordination
lease and slot counter, the Redis pub/sub channel `lenny:session:terminate`
(`pkg/gateway/podlifecycle/podterminate/propagator/propagator.go:33-46`, wired only to the
administrative full-revoke path at `cmd/lenny-gateway/adminrouter.go:904-917`), the kube-apiserver
leader-election Lease (`pkg/gateway/coordination/gatewayleader/gatewayleader.go:41-55`), and the
Postgres session store and `coordination_lease` mirror.

---

## 4. End-to-end scenarios

Scenarios 4.1 through 4.8 give an ASCII sequence diagram followed by a numbered hop list. Every hop
names the initiator, the channel from section 3, the process that handles it, and a citation. Two
scenarios depart from that structure and say so where they do: 4.1 traces only variant (a) as a hop
list and gives variants (b) and (c) as prose deltas, and 4.9 is a structural analysis of what is and is
not partitioned per slot rather than a traced scenario. Legend used throughout the diagrams:

```
  GW    gateway replica serving the request
  K8S   kube-apiserver
  RDS   Redis
  PG    Postgres
  AD    the pod's adapter container
  RT    the pod's runtime container
  REG   the serving replica's in-process podsession.Registry
  OS    object store
```

### 4.1 Session start

Three variants exist. All three converge on the same pod-facing RPC sequence and differ in where the
coordination lease is acquired relative to the running-state commit.

| Variant | Entry point | Handler |
|:--|:--|:--|
| (a) single call | `POST /v1/sessions/start` | `sessionserver.go:2048` then `start.go:424` |
| (b) three steps | `POST /v1/sessions`, `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/{id}/start` | `create.go:27`, `sessionserver.go:3079`, `start.go:1122` |
| (c) delegated child | MCP tool `lenny/delegate_task` | `mcptools_register.go:2607` then `start.go:960` |

Variant (c) has no REST route. Its only production caller is the MCP tool at
`mcptools_register.go:2607`, guarded on a non-nil materializer, and the field is populated with the live
session server at `cmd/lenny-gateway/mcpsurface.go:246`.

```
 VARIANT (a)  POST /v1/sessions/start
 Client        GW                                  K8S      AD/RT     PG      RDS    REG
   |            |                                   |        |         |       |      |
   |==POST=====>| decode          start.go:437      |        |         |       |      |
   |            | gates           start.go:442      |        |         |       |      |
   |            | build row (State=running) :672    |        |         |       |      |
   |            | 1 mint upload token     :773      |        |         |       |      |
   |            | 2 claimAtCreate         :799      |        |         |       |      |
   |            |   ResolvePool          :2073 ====>|        |         |       |      |
   |            |   credential pre-check :2094 ------------------------------->|      |
   |            |   Claim                :2157 ====>| CREATE claim-<pod>       |      |
   |            |   resolve Sandbox   binder:1678 ==>|        |         |       |      |
   |            |   dial + NegotiateVersion #1 ==============>|         |       |      |
   |            |   close                binder:806          |         |       |      |
   |            | 3 row.PodAssignment      :821     |        |         |       |      |
   |            | 4 startOnPod             :825     |        |         |       |      |
   |            |   ResolvePool #2        :2275 ===>|        |         |       |      |
   |            |   prepareAndLaunch      :2349     |        |         |       |      |
   |            |     Prepare: NegVer #2, PrepareWorkspace, FinalizeWorkspace, |      |
   |            |              RunSetup, AssignCredentials ==>| CREDENTIALS     |      |
   |            |     Launch:  NegVer #3, StartSession ======>| claimSession    |      |
   |            |                                    |        | writeManifest   |      |
   |            |                                    |        | startPlatformMCP|      |
   |            |                                    |        | Runtime.Start ==> RT   |
   |            |              GetObservedIntegrationLevel ==>|         |       |      |
   |            | 5 acquireCoordinationLease :858 ---------------------------->| CAS  |
   |            | 6 store.Create (RUNNING)   :865 --------------->|        |    |      |
   |            | 7 recordSessionCreated     :880   |        |         |       |      |
   |            | 8 registerLeaseTree        :885   |        |         |       |      |
   |            | 9 publishBinding           :894   |        |         |       |  Put |
   |<==201======| writeCreateSessionResponse :461   |        |         |       |      |

   The Attach content stream is NOT opened here. The first Send opens it.
   The gRPC control stream is never opened at all.
```

**Hops, variant (a):**

1. Client to gateway. HTTP `POST /v1/sessions/start`, routed at
   `pkg/gateway/sessionserver/sessionserver.go:2048`, wrapped in the manage permission gate at
   `:2026-2028`. Handler `start.go:424-462`.
2. Gateway, in process. Decode (`start.go:437`), then the admission gates in order: active user, tenant
   state, tenant classification, session quota, concurrency limits, admission rate limit, policy chain,
   and environment admission (`start.go:481-533`). None has a side effect to roll back.
3. Gateway, in process. Row build (`start.go:584-751`), with `State: session.StateRunning` set up front
   at `:672`.
4. Gateway to kube-apiserver and Redis. `claimAtCreate` (`start.go:2067-2182`) resolves the pool
   (`:2073`), runs the credential pre-check (`:2094`, body `:1675-1840`), and claims a pod. An exclusive
   pool goes through `podBinder.Claim` (`:2157`); a concurrent pool through `podBinder.ClaimSlot`
   (`:2117-2146`). The claim is the `SandboxClaim` CREATE at `podclaim/claimer.go:136`.
5. Gateway to pod, C1. `Binder.connect` (`binder.go:1645-1715`) resolves the Sandbox (`:1678`), dials
   (`:1683-1687`), calls `NegotiateVersion` (`:1689`), and closes the connection at `binder.go:806`.
6. Gateway to pod, C1. `startOnPod` (`start.go:2251-2370`) calls `prepareAndLaunch` (`:2349`).
   `Binder.Prepare` (`binder.go:826-948`) opens a fresh connection with `NegotiateVersion` (`:827` then
   `:1109`), then issues `DemoteSDK` when applicable (`:865`), `PrepareWorkspace` (`:899`),
   `FinalizeWorkspace` (`:904`), `RunSetup` (`:916`), and `AssignCredentials` (`:932`), then closes
   (`:940`). The credential material `AssignCredentials` carries was minted upstream by the Token
   Service over its own gRPC call (`pkg/gateway/credentials/credassign/client.go:196`), resolved during
   the credential pre-check of hop 4.
7. Gateway to pod, C1. `Binder.Launch` (`binder.go:959-1039`) opens a third connection with
   `NegotiateVersion` (`:960`), issues `ConfigureWorkspace` (`:992`) or `StartSession` (`:996`), probes
   the integration level (`:1014`), and returns a `BindResult` holding the open connection (`:1035`).
8. Pod, in process. `StartSession` (`pkg/adapter/session.go:76-168`) claims the session (`:117`),
   resolves connectors (`:127`), writes the manifest (`:129`), starts the platform MCP server (`:150`),
   starts connector MCP servers (`:158`), and starts the runtime (`:160`).
9. Gateway to Redis, C14. `acquireCoordinationLease` (`start.go:858`, body `:2890-2901`) runs before the
   running-state commit. On `ErrHeld` the handler rolls the binding back (`:859`) and returns
   `SESSION_CREATION_FAILED` with reason `coordination_lease_held` (`:860`).
10. Gateway to Postgres. `store.Create` commits the row already in `running` with `PodAssignment` set
    (`start.go:865`). On failure the handler releases the lease (`:874`) and then rolls the binding back
    (`:876`).
11. Gateway, in process. `publishBinding` (`start.go:894`, body `:2848-2868`) writes the registry entry
    (`:2852`), persists the pod assignment (`:2853`) and workspace root (`:2860`), and publishes
    workspace warnings (`:2867`). It deliberately does not re-acquire the lease; the rationale is at
    `start.go:2836-2844`.
12. Gateway to client. Status change emitted at `start.go:460`, HTTP 201 written at `:461`.

**Ordering rationale.** The lease acquire is hoisted ahead of the running commit because the moment the
row is `running` with a non-empty `pod_assignment`, a peer sweep's adoption predicate becomes true:
`adoptable := leaseUnheld && isRunningPod(row) && !inAdoptionBackoff` at
`pkg/gateway/coordination/coordination/coordination.go:319`, with `isRunningPod` at `:257-259`.

**Variant (b) differences.** `POST /v1/sessions` claims the pod (`create.go:332`), mints the upload
token (`:372`), and commits the row as `created` (`:389`). It acquires no lease and publishes no
binding; a grep for `acquireCoordinationLease`, `registerBinding`, or `publishBinding` in `create.go`
and `finalize.go` returns nothing. That is safe against the sweeper because a `created` row fails
`isRunningPod`. Credential delivery happens at finalize rather than at start: `prepareAtFinalize`
(`finalize.go:208-291`) calls `podBinder.Prepare` at `:280`, which reaches `AssignCredentials` at
`binder.go:932`. `POST /v1/sessions/{id}/start` then calls `launchOnPod` (`start.go:1157`),
`registerBinding` (`:1174`, which acquires the lease at `:2825` and publishes at `:2828`), an optional
setup-output persist (`:1187`), and the running commit (`:1199`).

**Variant (c) differences.** `MaterializeDelegatedChild` (`start.go:960-1098`) runs synchronously inside
the parent's MCP tool call on whichever replica served it. It guards on `State == created` (`:969-971`),
claims (`:995`), starts on the pod (`:1024`), acquires the lease (`:1042`), commits `running` (`:1058`),
and publishes the binding (`:1096`). The task input is then delivered with `deps.Executor.Send`
(`mcptools_register.go:2616`), which is where the first Attach stream opens.

**Rollback matrix.**

| Variant | Failure point | Rollback | Citation |
|:--|:--|:--|:--|
| (a) | `startOnPod` | `rollbackClaim` when `createClaimNeedsRollback` | `start.go:826-839`, predicate `:3128-3142` |
| (a) | lease acquire | `rollbackBinding` | `start.go:858-862` |
| (a) | `store.Create` | release lease, then `rollbackBinding` | `start.go:865-879` |
| (b) | create mint or persist | `rollbackClaim` | `create.go:377`, `:398` |
| (b) | finalize, pre-Prepare | `reclaimFinalizedPod` | `finalize.go:231`, `:251`, `:271` |
| (b) | **start, setup-output persist** | **none** | `start.go:1187-1195` |
| (b) | **start, running commit** | **none** | `start.go:1199-1206` |
| (c) | `store.Update` | release lease, then `rollbackBinding` | `start.go:1074-1080` |

The two gaps on variant (b) are real. After `registerBinding` has taken the lease and published the
binding, a failure of either subsequent `store.Update` returns HTTP 500 with no `rollbackBinding` and no
`releaseCoordinationLease`. The pod is launched, the binding is in the registry, the lease is held, and
the row is still `ready`. The sweeper sees `bound == true` and renews the lease indefinitely
(`pkg/gateway/coordination/coordination/coordination.go:352`, `:357`), and `isRunningPod` is false so no
peer can adopt. A client retry of
`POST /start` re-runs `Launch`, whose `StartSession` the adapter rejects `Unavailable` because
`s.sessionID != ""` (`pkg/adapter/session.go:419-422`), and that rejection routes through `Launch`'s
reclaim (`binder.go:980-983`) into `failPhase` (`binder.go:1055-1065`), which revokes the credential
lease and drains the pod.

**Also observed.** The session state `starting` is never written. `session.StateStarting` exists and is
matched in several sweeps, but `grep -rn "State = session.StateStarting\|State:.*StateStarting" --include=*.go pkg cmd | grep -v _test`
returns nothing; `transitionStart` writes `running` directly (`sessionserver.go:3257`). The dual-store
availability gate runs on `POST /v1/sessions` (`create.go:81-87`) and has no equivalent in
`createAndStartGates` (`start.go:481-533`).

### 4.2 Interactive message send

```
 CLIENT        GW (holds the binding)                     POD: adapter        POD: runtime
   |                    |                                      |                   |
   |==POST /v1/sessions/{id}/messages==>|                       |                   |
   |           route sessionserver.go:2072                       |                   |
   |           handleMessages messages.go:263                    |                   |
   |             loadMessageTarget    :303  (store.Get, gates)   |                   |
   |             parseMessageBatch    :426                       |                   |
   |             inputWaits.Resolve   :462  (path-1 short circuit)                   |
   |             Classify             :543  ==> ActionDeliver    |                   |
   |             executor.Send        :546                       |                   |
   |                    |  streamFor pod.go:128                  |                   |
   |                    |    cache hit? e.streams :131           |                   |
   |                    |    registry.Get         :134           |                   |
   |                    |    SLOT_ID_REQUIRED     :146           |                   |
   |                    |==Attach open + bind frame :154========>| attach.go:28      |
   |                    |                                        | checkSession :45  |
   |                    |                                        | rt.Output    :60  |
   |                    |==AttachRequest{envelope_json} :111====>| attachRecvLoop:237|
   |                    |                                        | writeSlotEnvelope |
   |                    |                                        |==unix socket=====>|
   |                    |                                        |                   |
   |                    |                                        |==heartbeat (30s)=>|
   |                    |                                        |   heartbeat.go:34 |
   |                    |                                        | miss ack (10s) ==> SIGTERM runtime
   |                    |<==DeadlineExceeded, stream ends========| attach.go:141-147 |
   |                    |                                        |                   |
   |                    |<==AttachResponse frames :129===========| fanOut            |
   |                    |   heartbeat_ack     consumed pod-side  |                   |
   |                    |   set_tracing_ctx   consumed pod-side  |                   |
   |                    |   tool_call(approvalRequired) ==> gate (see below)          |
   |                    |   {"type":"response"} ==> ingestResponse ingest.go:24       |
   |             recordDeliveredResponse :704                    |                   |
   |<==200 deliveryReceipt status=delivered==|                    |                   |

 TOOL-APPROVAL SUB-FLOW, inside the same HTTP request
   |                    |<==tool_call{approvalRequired:true}=====|                   |
   |                    | maybeGateToolCall pod.go:248           |                   |
   |                    | AwaitApproval toolapproval.go:76       |                   |
   |                    |   interactions.Put(Pending) :104       |                   |
   |<==SSE tool_use_requested :119                                |                   |
   |==POST .../tool-use/{id}/approve  (MAY LAND ON ANY REPLICA)==>|                   |
   |                    | interactions.Resolve (shared store) :116                    |
   |                    | local waiter :152 OR 25ms store poll :158-167                |
   |                    |==tool_call with the flag cleared :269=>| forwarded to RT   |
```

**Hops:**

1. Client to gateway. `POST /v1/sessions/{id}/messages`, routed at `sessionserver.go:2072` behind the
   manage permission gate (`rbac_gate.go:58-67`). **The load balancer picks the replica.** There is no
   session affinity: the gateway Service declares none
   (`charts/lenny/templates/gateway-deployment.yaml:1629-1663`) and the ingress carries no sticky
   annotation.
2. Gateway, in process. `handleMessages` (`messages.go:263`) rejects a nil executor with 503
   (`:264-268`), opens the `session.prompt` span (`:274`), and runs `loadMessageTarget` (`:303`):
   `store.Get` with 404 on miss (`:305-313`), the tenant-suspend gate (`:317`), the precondition table
   (`:320-326`), a 409 `TARGET_NOT_READY` for pre-running states (`:334-339`), and the injection-support
   gate (`:355-417`).
3. Gateway, in process. `parseMessageBatch` (`:426`) caps the body, rejects an empty batch with 400, and
   rejects an unknown `delivery` value with 400 `INVALID_DELIVERY_VALUE` (`:445-452`).
4. Gateway, in process. The `inReplyTo` short circuit resolves against `s.inputWaits` (`:462`), a
   per-replica in-process registry (`pkg/gateway/session/inputwait/inputwait.go:41-49`). A miss falls
   through to ordinary delivery (`:465-467`).
5. Gateway, in process. `messagerouting.Classify` (`:543`, body `messagerouting.go:153-182`) returns
   `ActionDeliver` for a `running` session, `ActionBufferInbox` for `input_required` or plain
   `suspended`, `ActionResumeAndDeliver` for `suspended` with `delivery: immediate`,
   `ActionBufferDLQ` for `resume_pending` and `awaiting_client_action`, and rejections for terminal and
   pre-running states.
6. Gateway to pod, C2. `executor.Send` (`messages.go:546`) reaches `PodExecutor.Send`
   (`pod.go:79`) and `streamFor` (`pod.go:128`). The registry miss error is
   `podexec: session %s is not bound to a pod` (`pod.go:136`).
7. Gateway to pod, C2. One `messageEnvelope` per message is marshalled (`pod.go:100-108`) and sent
   (`pod.go:111`). The envelope is `{schemaVersion:1, type:"message", id:"msg_...", from, input, slotId}`
   (`pkg/gateway/session/executor/subprocess.go:371-384`). The slot id and tenant id come from a fresh
   registry read at `pod.go:93-96`. A client cannot supply a slot id: `MessagePayload` has no such field
   (`messages.go:158-195`).
8. Pod, in process, C8. `writeSlotEnvelope` (`attach.go:257-266`) calls `rt.WriteEnvelope`, which writes
   the envelope and a newline to the runtime socket (`socketruntime.go:324-335`).
9. Pod to gateway, C2. The adapter's select loop (`attach.go:93-151`) consumes `heartbeat_ack`
   (`:103-106`), `set_tracing_context` (`:114-117`), and adapter-local tool calls (`:123-128`) pod-side,
   and relays everything else with a runtime-set `from` stripped (`:129`, `stripRuntimeFrom` at
   `:214-231`). The acknowledgements answer the adapter's own heartbeat, started per Attach stream at
   `attach.go:78` (section 3.10). A missed acknowledgement takes the `<-hbHung` arm at `attach.go:141`,
   which SIGTERMs the runtime and ends the stream with `codes.DeadlineExceeded` (`:146-147`).
10. Gateway, in process. `readAttachResponse` (`pod.go:192-220`) loops on `Recv`, skips non-`response`
    frames, and returns on the first `{"type":"response"}` through `ingestResponse`
    (`pkg/gateway/session/executor/ingest.go:24-51`).
11. Gateway, in process. `recordDeliveredResponse` (`messages.go:704`) runs the post-output interceptor
    chain (`:719-725`), which when an external `RequestInterceptor` is configured is a gRPC call to that
    service (`cmd/lenny-gateway/interceptors.go:201`, client `pkg/gateway/policy/interceptor/external.go:135`,
    fail-closed default at `:61-64`), appends to the transcript (`:730-746`), and publishes `message_delivered`
    (`:751`), `response` (`:756`), and `response_degraded` (`:771`). Cross-replica SSE fan-out is wired
    through the Redis relay (`cmd/lenny-gateway/stores.go:2270`).
12. Gateway to client. `writeDeliveryReceipt` (`messages.go:782-816`) writes HTTP 200 with the receipt
    and output parts.

**Tool-call approval.** Triggered inside `readAttachResponse` (`pod.go:201-209`) and handled by
`maybeGateToolCall` (`pod.go:248-299`), wired in production at `cmd/lenny-gateway/stores.go:2364`.
`AwaitApproval` (`toolapproval.go:76`) registers a local waiter (`:92`), writes a pending interaction to
the shared store (`:104-113`), publishes the `tool_use_requested` SSE event (`:119`), and then blocks on
four wake paths: the local channel, a timeout returning `approval_timeout` (`:151`), context
cancellation (`:152-154`), and a 25-millisecond poll of the shared store (`:155-167`, `pollResolution`
at `:180-204`). The poll is what makes a resolution landing on another replica work. Approval
re-marshals the `tool_call` with the flag cleared and forwards it (`pod.go:269-281`); denial sends a
`tool_result` with `isError` set (`pod.go:286-297`).

**Two properties of the cached stream that change behavior.**

First, the stream is opened with the caller's context. `streamFor` passes the request context straight
into `Attach` (`pod.go:154`), and every production caller passes a request-scoped context
(`messages.go:546`, `:610`, `openai_chat.go:273`, `open_responses.go:334`,
`mcptools_register.go:480`, `:2616`). When the HTTP handler returns, `net/http` cancels that context and
the gRPC stream with it, while the stream stays in `e.streams`. The next `Send` on that session reuses
the cancelled stream. The existing unit test does not catch this because it uses
`context.Background()` for every send (`pkg/gateway/session/executor/pod_test.go:88-105`).

Second, there is no per-session serialization on the content path. `PodExecutor.mu` is held only inside
`streamFor` (`pod.go:129-130`) and `EvictStream` (`pod.go:174-175`), never across `Send` and `Recv`. Two
concurrent requests for the same session on the same replica call `AttachStream.Send` and
`AttachStream.Recv` on one gRPC stream from two goroutines, which gRPC forbids. Beyond the race,
`readAttachResponse` returns the first `response` frame it sees, so responses can be cross-delivered to
the wrong request.

**Buffered outcomes never drain.** `ActionBufferInbox` and `ActionBufferDLQ` call
`bufferIncomingMessages` (`messages.go:837-871`) and return HTTP 200 with `status: "queued"`
(`messages.go:810-815`). No code dequeues the inbox and delivers it. The only two callers of
`inbox.Drain` are `MigrateInboxToDLQ` (`pkg/gateway/session/sessioninbox/coordinator.go:121`) and
`DrainOnTerminal` (`:201`), which emits `message_expired` with reason `target_terminated`. The comments
at `messages.go:587` and `:608` claiming the inbox "drains on the next `ready_for_input`" describe
behavior that does not exist, and no `ready_for_input` producer or consumer exists either: every
non-test hit in the Go tree is a prose comment (`messages.go:514`, `:522`, `:587`, `:608`;
`start.go:3792`; `pkg/gateway/session/messagerouting/messagerouting.go:44`, `:49`, `:141`).

### 4.3 Interrupt, terminate, and delete

```
 INTERRUPT on the binding replica
 client        GW                          AD                    RT
   |            |                           |                     |
   |==POST /interrupt=>| store.Get + Validate interrupt.go:48-63   |
   |            | podRegistry.Get    :125    |                     |
   |            |==Adapter.Interrupt========>| checkSession :30    |
   |            |  {MODE_CLEAN, 5000ms} :134 | ops.Begin(interrupt)|
   |            |                            |==interrupt_request=>|  (C9, disabled
   |            |                            |   lifecyclechannel  |   in a rendered pod)
   |            |                            |<=interrupt_ack======|
   |            |<==InterruptResponse========| lifecycle.go:109    |
   |            | store.Update suspended,    |                     |
   |            |   SuspendedReason          |                     |
   |            |   interrupt.go:85-96       |                     |
   |            | emitStatusChange  :99      |                     |
   |<==200======|                            |                     |

 TERMINATE, DELETE, or watchdog expiry on the binding replica
 trigger       GW                          AD                RT           K8S
   |            | store.Update terminal      |                 |            |
   |            | recordSessionCompleted usage.go:375           |            |
   |            |   sealWorkspace     :394    |                 |            |
   |            |   ops event, callback, SSE, audit, retention  |            |
   |            | PodExecutor.Release pod.go:325                |            |
   |            |   EvictStream + registry.Remove               |            |
   |            | binder.Release      binder.go:1865            |            |
   |            |   releaseCredentials       :1871              |            |
   |  recycle   |==patch claim bound to recycling===========================>|
   |            |==Adapter.Shutdown{recycle} =>|                 |            |
   |  retire    |==Adapter.Shutdown{}=========>| emitFinalUsage  |            |
   |            |                              | drainViaLifecycle           |
   |            |                              |==terminate frame=>|          |
   |            |                              | Runtime.Close    |          |
   |            |                              | releaseSession   |          |
   |  retire    |==podclaim.DeleteClaim====================================>|
   |<==200======|                              |                 |            |
```

**Interrupt hops:**

1. Client to gateway. `POST /v1/sessions/{id}/interrupt`, routed at `sessionserver.go:2058`.
2. Gateway, in process. `store.Get` and the precondition table, which admits only `running` and
   `input_required` (`interrupt.go:48-63`, table `pkg/api/v1/session/session.go:291-297`).
3. Gateway, in process. `s.podRegistry.Get(row.ID)`, a local map lookup (`interrupt.go:122-128`).
4. Gateway to pod, C1. `bind.Adapter.Interrupt(ctx, row.ID, false, deadline)` (`interrupt.go:134`), with
   a five-second `deadline_ms` (`interrupt.go:23`, applied `:168-170`). The `hard` argument is the
   literal `false` at the only call site, so `MODE_HARD` is unreachable in production.
5. Pod, in process. `checkSession` (`lifecycle.go:30`), then `ops.Begin(ctx, opInterrupt, "")`
   (`lifecycle.go:38`), which takes the whole-pod queue and returns `STATUS_BUSY` on contention
   (`:40-42`).
6. Pod to runtime, C9 when available. `interruptViaLifecycle` (`lifecycle.go:100-124`) writes
   `interrupt_request` and blocks for `interrupt_acknowledged`. In a chart-rendered pod `s.Lifecycle` is
   nil, so the handler falls through to `s.Runtime.Interrupt(ctx, sessionID, hard)`
   (`lifecycle.go:54-60`).
7. Pod to gateway. `InterruptResponse{acknowledged, status}` (`lifecycle.go:109-123`).
8. Gateway to Postgres. `store.Update` sets `suspended`, `SuspendedAt`, and `SuspendedReason`
   (`interrupt.go:85-96`), where the reason is the literal `"InterruptAcknowledged"` when `timedOut` is
   false (`:161-166`).
9. Gateway to client. HTTP 200 with the session response (`:108`), or the timeout variant (`:172-192`).

**What interrupt does not do.** It opens no Checkpoint stream: `handleInterrupt` (`interrupt.go:45-109`)
contains no checkpoint call, despite the proto describing interrupt as optionally checkpointing
(`schemas/lenny-adapter.proto:108-109`). It releases no pod, so the session holds its pod while
suspended. The `maxSuspendedPodHoldSeconds` cap that would bound that hold is configured
(`pkg/gateway/runtime/watchdog/watchdog.go:199-204`), flagged
(`cmd/lenny-gateway/flags.go:1060-1062`), and threaded into the watchdog
(`cmd/lenny-gateway/controlserver.go:229`), and no sweep reads it. Because `suspended` is excluded from
the idle clock (`watchdog.go:979-983`), the effective bound is `sweepMaxAge` at `maxSessionAge` from
creation, 7200 seconds by default (`watchdog.go:113`).

**A transport error is reported as a successful acknowledgement.** On an RPC error
`signalAdapterInterrupt` logs and returns `(Unspecified, timedOut=false, attempted=true)`
(`interrupt.go:135-145`). The caller then takes the normal path and stamps
`SuspendedReason = "InterruptAcknowledged"`.

**Terminate and delete hops:**

1. Client to gateway. `POST /v1/sessions/{id}/terminate` is the generic no-body transition
   (`sessionserver.go:2059-2060`, `transitionTerminate` writing `completed` at `:3265`).
   `DELETE /v1/sessions/{id}` is `handleDelete` (`:2051`, handler `:2936-2974`) writing `cancelled`
   (`:2957`), plus a coordination-generation bump when the prior state was `resuming` (`:2969-2971`).
2. Gateway to Postgres. The row goes terminal before any pod work (`sessionserver.go:2999-3007`).
3. Gateway, in process. `recordSessionCompleted` (`pkg/gateway/sessionserver/usage.go:375-540`) runs the seal (`:394-398`), the
   operations event, the callback, the SSE events, the audit row, and the retention roll (`:403-432`).
4. Gateway, in process. `terminalReclaimPreRunning` (`usage.go:585-609`) handles a pre-running
   termination by name from `sess.PodAssignment` (`:600`). For a `running` session it returns false at
   `:586-588`, deliberately, so a handed-off session is not reclaimed by name.
5. Gateway, in process. `releaseExecutor` calls `PodExecutor.Release` (`pod.go:325-341`), which evicts
   the cached stream (`:326`), removes the registry entry (`:328`), and branches on `bind.SlotID` to
   `binder.ReleaseSlot` (`:333`) or `binder.Release` (`:340`).
6. Gateway to pod and kube-apiserver. `binder.Release` (`binder.go:1865-1916`) releases credentials
   (`:1871`), then either patches the claim from `bound` to `recycling` and arms the missing-report
   timer before `Adapter.Shutdown` with the recycle scrub (`:1882-1903`), or issues a plain
   `Adapter.Shutdown` and deletes the claim (`:1910-1915`, `:1940-1954`).
7. Pod, in process. `Shutdown` (`session.go:218-288`) runs `checkSession` (`:247`), emits the final
   usage report onto the control stream (`:253`, dropped), and calls `drainViaLifecycle` (`:262`), which
   writes the `terminate` frame on C9 when it exists (`session.go:295-303`).
8. Pod, in process. `Runtime.Close` (`pkg/adapter/session.go:264`) closes the runtime socket, waits the
   grace period, and sends SIGKILL on timeout (`pkg/adapter/socketruntime.go:439-470`), then
   `releaseSession` (`pkg/adapter/session.go:266`).
9. Gateway to client. HTTP 200 with the terminal row.

**The full `Terminate` RPC is not used on these paths.** `adapterclient.Terminate` sets `reason` and
`deadline_ms` (`client.go:927-936`), and its only production caller is
`cmd/lenny-gateway/user_revocation.go:129`. The session paths call `Shutdown` with an empty reason and a
zero deadline (`binder.go:1951`), so `drainReason("")` defaults to `session_complete`
(`pkg/adapter/session.go:310-317`) and `contextWithGraceDeadline(ctx, 0)` returns the parent context
unchanged (`session.go:324-329`).

**Expiry follows the same funnel.** The watchdog runs on every replica un-gated by leader election
(`cmd/lenny-gateway/workers.go:101`, outside the leader gate at `:628`), on a five-second tick
(`watchdog.go:166-168`). `sweepMaxAge` (`watchdog.go:1085-1128`), `sweepIdle` (`:1007-1059`), the
pre-running budget sweeps (`:605-643`), and `sweepAwaitingClientAction` (`:917`) all write a terminal
state and then call `recordCompleted` (`:1124`), which reaches `recordSessionCompleted`. A pre-expiry
warning fires 300 seconds ahead (`:1149-1181`) and, on the binding replica only, sends
`Adapter.SignalDeadline` (`expiry_warning.go:53-69`).

`sweepMaxAge` has a loose single-fire guard. The mutator no-ops on an already-terminal row
(`watchdog.go:1099-1102`), but the post-condition is only `updated.State == session.StateExpired`
(`:1115`), which is true for a row another replica has already expired. Both replicas then run the terminal
funnel, producing duplicate counters, SSE events, callbacks, audit rows, and billing events.
`sweepIdle` additionally requires a matching failure reason (`:1048`), and the pre-running sweep does
the same (`:634`).

**Two MCP tools bypass the adapter entirely.** `lenny/interrupt_session`
(`mcptools_register.go:525-561`) does `Store.Get`, `Validate`, and `Store.Update{State: suspended}`, and
returns. It never calls `signalAdapterInterrupt`, never stamps `SuspendedAt` or `SuspendedReason`, and
emits no status change. `lenny/cancel_session` (`mcptools_register.go:563-620`) does the same for
`cancelled`, and never calls `recordSessionCompleted`, so it produces no seal, callback, audit, billing,
SSE, or pod release. By contrast `lenny/terminate_session` routes through the REST handler
(`client_tools.go:101-104` then `pkg/gateway/sessionserver/service.go:86-104`) and behaves correctly.

### 4.4 Checkpoint capture

Every production trigger converges on one bidirectional stream. The trigger set is:

| Trigger | Site | Trigger value | Status |
|:--|:--|:--|:--|
| Periodic sweep | `checkpointer.go:287-298`, started at `cmd/lenny-gateway/workers.go:1421` | `periodic` | WIRED |
| Seal on completion | `pkg/gateway/sessionserver/usage.go:395` then `pkg/gateway/sessionserver/seal.go:118` then `checkpointer.go:397-403` | `periodic` | WIRED |
| Gateway drain barrier | `pkg/gateway/coordination/barrier/barrier.go:225` | `eviction` | WIRED |
| Gateway preStop per-session loop | `pkg/gateway/podlifecycle/prestop/checkpoint_adapter.go:39` | `eviction` | WIRED |
| Agent-pod eviction | none | `eviction` | **ABSENT** (section 4.8) |
| Pre-scale-down | none | `pre_scale_down` | **ABSENT** |
| Embedded SIGSTOP and SIGCONT | `pkg/adapter/embeddedcheckpoint` | n/a | **UNWIRED** |

```
 GATEWAY (coordinating replica)          ADAPTER                RT        OS       PG
   |                                        |                    |         |        |
   | lockFlight(session, slot) uploaddriver.go:133                |         |        |
   | mint checkpoint_id (UUID) checkpointer.go:526                |         |        |
   |==Checkpoint bidi stream opened :522===>|                     |         |        |
   |--CheckpointStart{id, trigger, 16MiB,   |                     |         |        |
   |    "tar", deadline_ms=0, slot_id?}====>| checkpoint.go:82    |         |        |
   |                                        | rootsForSlot  :101  |         |        |
   |                                        | ops.Begin     :113  |         |        |
   |                                        | barrier.link  :124  |         |        |
   |                                        | probe + limit :137  |         |        |
   |<==CheckpointProbe{workspace_bytes}=====| checkpoint.go:144   |         |        |
   | onProbe uploaddriver.go:301            |                     |         |        |
   |   reserve quota          :310 --------------------------------------------->|   |
   |   supersedePriorAttempts :313 --------------------------------------------->|   |
   |   Manifests.Put(partial) :342 --------------------------------------------->|   |
   |   intentWritten = true   :351          |                     |         |        |
   |                                        |==checkpoint_request=>| (C9)   |        |
   |                                        |<=checkpoint_ready====|        |        |
   |                                        | ArchiveTree -> tar.gz pipe    |        |
   |<==ChunkReady{i, len}===================| checkpoint.go:287   |         |        |
   |   gate on intentWritten  :504          |                     |         |        |
   |   quota cap + PresignPut :563, :575    |                     |         |        |
   |--CheckpointGrant{i, url, len, headers, |                     |         |        |
   |    expires_at}========================>| awaitGrant :365     |         |        |
   |                                        |==HTTPS PUT presigned==========>|       |
   |<==ChunkCommitted{i}====================| checkpoint.go:315   |         |        |
   |   free window slot, mint queued :636   |                     |         |        |
   |   go confirmChunk :654 (Stat, catalog, ConfirmChunk) ---------------------->|   |
   |            ... loop ...                |                     |         |        |
   |<==CheckpointSummary====================| checkpoint.go:272   |         |        |
   |   verify confirmed == declared :707    |                     |         |        |
   |   Finalise(partial=false)      :714 --------------------------------------->|   |
   |                                        |==checkpoint_complete=>|        |        |
   | Sessions.Update{WorkspaceSnapshot} checkpointer.go:440 -------------------->|   |
   | recordRetention + Rotate           :475, :699 ----------------------------->|   |
```

**Hops:**

1. Gateway, in process. `snapshot` opens with `c.Registry.Get(sessionID)` and returns `ErrNoBinding`
   when this replica holds no binding (`checkpointer.go:424-427`). Under co-location that means the
   coordinator is the only replica that checkpoints.
2. Gateway, in process. `lockFlight(sessionID, slotID)` (`checkpointer.go:508-509`), a process-local
   `sync.Map` of mutexes (`uploaddriver.go:133-139`). It excludes nothing across replicas.
3. Gateway to pod, C3. `binding.Adapter.Checkpoint(ctx)` (`checkpointer.go:522`). The deadline is
   `context.WithCancel` rather than `WithTimeout` because `Checkpointer.Deadline` is never set in
   production: the struct literal at `cmd/lenny-gateway/stores.go:2157-2203` omits it and the only
   post-construction writes are metrics fields (`cmd/lenny-gateway/metricsbackfill.go:229`, `:235`,
   `:244`). `DeadlineMs: 0` therefore goes on the wire (`checkpointer.go:534`), and the intent row's
   timeout equals its start time (`uploaddriver.go:339-340`).
4. Gateway to pod, C3. `CheckpointStart` (`checkpointer.go:527-551`) carries the gateway-minted
   `checkpoint_id` (`:526`), the trigger, a 16 MiB chunk size (`uploaddriver.go:31`), `chunk_encoding`
   `"tar"` (`uploaddriver.go:152-157`), and `slot_id` when the bind carries one (`checkpointer.go:545`).
5. Pod, in process. The handler validates the first frame, requires a workspace root and a transport
   (`checkpoint.go:82-94`), resolves the slot-scoped roots (`:101`, body `pkg/adapter/slot.go:147-173`),
   takes the operation lock (`:113`), and links the barrier gate (`:124`).
6. Pod to gateway, C3. `probeWorkspaceBytes` sums every resolved root (`checkpoint.go:47-57`),
   `WorkspaceSizePreCheck` rejects an over-limit workspace with `FailedPrecondition` before any grant
   (`:141-143`), and `CheckpointProbe` is sent (`:144-150`).
7. Gateway to Postgres. `onProbe` (`uploaddriver.go:301-353`) reserves quota (`:310`), supersedes prior
   attempts for the same slot (`:313`, body `:402-473`), and writes the partial manifest intent row
   (`:318-342`) before setting `intentWritten` (`:351`). No grant is minted before that flag is set
   (`:504-508`), which is the no-orphan-object gate.
8. Pod to runtime, C9 when available. `RequestCheckpoint` (`checkpoint.go:183`) writes
   `checkpoint_request` and blocks for `checkpoint_ready`. In a chart-rendered pod `s.Lifecycle` is nil,
   so the entire quiesce block at `checkpoint.go:159-201` is skipped and the archive runs without a
   cooperative quiesce.
9. Pod, in process. `ArchiveTree` writes tar wrapped in gzip unconditionally
   (`pkg/adapter/workspace/tree.go:55-85`, `gzip.NewWriter` at `:57`) into a pipe, and `readChunk`
   buffers each chunk, spilling above 8 MiB to the staging directory
   (`pkg/adapter/checkpointchunk.go:41-85`).
10. Pod to object store, C13. `attemptPut` (`checkpoint.go:388-405`) PUTs the chunk against the
    presigned URL, replaying every signed header. A 5xx or transport error is retriable; a 4xx is
    terminal (`:396-404`). The retry budget comes from the trigger: five seconds normally and thirty
    seconds for eviction (`pkg/checkpoint/checkpoint.go:161-176`).
11. Gateway, in process. `onChunkCommitted` (`uploaddriver.go:626-655`) frees the window slot on the
    acknowledgement, mints the oldest queued declaration, and spawns `confirmChunk` (`:658-688`), which
    stats the object, records the catalog row, and advances the manifest's confirmed counter.
12. Gateway to Postgres. `onSummary` (`uploaddriver.go:693-721`) verifies that the confirmed byte count
    and chunk count match the summary and finalizes the manifest as complete.
13. Gateway to Postgres. `checkpointer.go:438-476` writes `WorkspaceSnapshot` and
    `LastSuccessfulCheckpointAt` on the session row and records retention with the latest-two rotation
    (`:475`, body `:679-718`).

**Notable behaviors on this path.**

The declared `chunk_encoding` disagrees with the bytes. The gateway declares `"tar"`
(`uploaddriver.go:152-157`) and the adapter writes gzip (`workspace/tree.go:57`), so object keys read
`chunk-00000.tar` and hold tar.gz. Restore works because `ExtractTree` gunzips unconditionally
(`tree.go:156`) and `resumechunks` builds keys from the same column
(`resumechunks.go:153-156`), so the write and read paths agree on the name. The migration comment at
`migrations/0178_checkpoint_manifest.up.sql:71-72` states that the resume path selects the decoder
strictly from that column, which is what makes the mismatch a latent hazard for any other consumer.

Periodic jitter is a dead field. `Checkpointer.JitterFraction` is set in production
(`cmd/lenny-gateway/stores.go:2161`) and never read; the helper that would apply it,
`FirstCheckpointDelay` (`checkpointer.go:311`), has only test callers. Every session a replica
coordinates is checkpointed on the same ten-minute tick (`cmd/lenny-gateway/flags.go:990`), one after
another, because `Sweep` iterates the registry snapshot sequentially (`checkpointer.go:339-343`).

`FailedPrecondition` is over-classified. `isSizeLimitReject` (`uploaddriver.go:1006-1008`) tests only
the gRPC code, so "adapter is not configured with a workspace root" (`checkpoint.go:88`), "not
configured with a checkpoint transport" (`:92`), and "slot has no assigned session"
(`pkg/adapter/slot.go:161`) are all reported to operators as a workspace-size rejection, with the
`lenny_checkpoint_size_exceeded_total` counter and a `checkpoint.skipped` event.

`errOpCoalesced` contradicts its own contract. `oplock.go:33-36` says a coalesced checkpoint is a
successful no-op for the caller; `checkpoint.go:114-118` maps every `Begin` error, including that one,
to `codes.Aborted`, which the gateway finalizes as a truncated stream.

The quiescence handshake is not slot-scoped. No runtime lifecycle channel frame carries a slot id
(`lifecyclechannel.go:59-83`) and `runtimeForSlot` returns the same pod-global runtime for every slot
(`pkg/adapter/slot.go:187-197`), so checkpointing one slot would quiesce the shared runtime for all of
them if the channel were enabled.

### 4.5 Restore and resume

```
 CLIENT       GATEWAY                                ADAPTER (fresh pod)     OS      PG
   |             |                                        |                   |       |
   |==POST /resume=>| handleResume start.go:3346           |                   |       |
   |             |   precondition awaiting_client_action   |                   |       |
   |             |   row.State = resuming     :3386 ------------------------------->|
   |             | resumeOnPod                :3845        |                   |       |
   |             |   no snapshot -> startOnPod rebuild :3854                    |       |
   |             |   ResolvePool              :3885 ====>K8S                    |       |
   |             |   resolveResumeChunks      :3906        |                   |       |
   |             |     selectResumeCheckpoint :4038 -------------------------------->|
   |             |     resumechunks.Resolve   :121         |                   |       |
   |             |       partial threshold gate :136       |                   |       |
   |             |       ListByPrefix + contiguity :163 ==================>|    |       |
   |             |       PresignGet per index      :180 ==================>|    |       |
   |             |   podBinder.Resume  binder.go:1586      |                   |       |
   |             |     connect -> podclaim.Claimer  (EXCLUSIVE whole-pod claim)|       |
   |             |==Adapter.Resume{session, checkpoint_id, |                   |       |
   |             |    expected bytes/limit/root, chunks[]}=>| resume.go:25      |       |
   |             |      (NO slot_id field on the wire)      | claimSession :47  |       |
   |             |                                          | root assert  :61  |       |
   |             |                                          | size precheck:74  |       |
   |             |                                          |==GET chunk 0====>|       |
   |             |                                          |==GET chunk 1====>|       |
   |             |                                          | ExtractTree :180  |       |
   |             |                                          |   (POD-GLOBAL roots)      |
   |             |<==ResumeResponse{restored_bytes, mode="full" always}=======|       |
   |             | acquireCoordinationLease  :3941 ------------------------> RDS       |
   |             | podRegistry.Put           :3945  (BindResult has no SlotID)|       |
   |             | bumpRecoveryGeneration    :3950 -------------------------------->|
   |             | fenceResumedPod           :3955 =========>| CoordinatorFence |       |
   |             | store.Update running      :3424 -------------------------------->|
   |<==200 + SSE session.resumed==|                          |                   |       |
```

**Hops:**

1. Client to gateway. `POST /v1/sessions/{id}/resume` (`start.go:3346`), admissible only from
   `awaiting_client_action` (`pkg/api/v1/session/session.go:307`). The second entry point is delegation
   tree recovery through `sessionNodeReattacher.ReattachNode` (`treerecovery.go:28-43`). There is no
   background worker that resumes a `resume_pending` row.
2. Gateway, in process. `resumeOnPod` (`start.go:3845`) branches: with no `WorkspaceSnapshot.Ref` it
   rebuilds from the recorded workspace plan through `startOnPod` (`:3854`, branch `:3846-3880`);
   otherwise it takes the checkpoint-restore branch (`:3882` onward).
3. Gateway to Postgres and the object store. `resolveResumeChunks` (`start.go:3906`, body `:3975-4019`)
   selects the checkpoint through `LatestActiveAny` (`:4042`), which is session-scoped rather than
   slot-scoped, then resolves the chunk set (`resumechunks.go:121-197`): a partial row must clear the
   recovery-threshold fraction (`:136-147`), the chunk indices must be contiguous and ascending before
   any body is fetched (`:162-171`), and one GET capability is signed per index (`:176-190`). A
   contiguity or threshold failure falls back to `LatestFull` (`start.go:4005-4013`).
4. Gateway to kube-apiserver. `Binder.Resume` (`binder.go:1586-1626`) claims a fresh pod through
   `b.connect` (`:1587`), which builds a `podclaim.Claimer` (`:1637-1647`). It never uses the slot
   claimer.
5. Gateway to pod, C1. `Adapter.Resume` unary (`client.go:696-720`). `ResumeRequest` carries no slot id
   (`schemas/lenny-adapter.proto:1224-1292`).
6. Pod, in process. `claimSession` sets the pod-global session id (`resume.go:47`), the expected
   workspace root is asserted (`:61-66`), the size pre-check runs (`:74-84`), and `restoreChunks`
   (`:95`, body `:154-183`) sorts by index, GETs each chunk into one pipe, and feeds the concatenation
   into `ExtractTree(s.checkpointRoots(), pr)` at `:180`.
7. Pod, in process. The manifest is rewritten, the platform and connector MCP servers restart, and the
   runtime starts (`resume.go:104-129`). The response reports `mode = "full"` unconditionally
   (`:130-141`).
8. Gateway to Redis and Postgres. `acquireCoordinationLease` fails closed on `ErrHeld`
   (`start.go:3941-3943`), the binding is published (`:3945`), the recovery generation is bumped
   (`:3950`), and `fenceResumedPod` sends `CoordinatorFence` (`:3955`, body `:4124-4136`).
9. Gateway to client. `classifyResume` (`start.go:4162-4176`) labels the resume, the row moves to
   `running` through `transitionResume` (`:3424-3425`), and the `session.resumed` SSE event is emitted.

**Restore onto a concurrent-session pod is absent.** Four independent facts each make it impossible.
`ResumeRequest` has no `slot_id` field, while `StartSessionRequest` (`:918`),
`PrepareWorkspaceRequest` (`:682`), and `CheckpointStart` (`:1120`) all do. `podsession.ResumeRequest`
(`binder.go:613-660`) has no `SlotID`. `Binder.Resume` routes through the exclusive claimer rather than
the slot claimer. And `resume.go:180` extracts into `s.checkpointRoots()` rather than
`checkpointRootsForSlot`, while `resume.go:47` claims the pod globally. A slot-captured checkpoint is
written correctly and is resolvable, and the archive's namespace prefixes are slot-independent
(`pkg/adapter/workspace/tree.go:41-42`), so the bytes land in the right place, on a freshly claimed
exclusive pod. The resumed session then loses its slot identity in the registry, because
`Binder.Resume` builds a `BindResult` with no `SlotID` and no `MaxConcurrentSessions`
(`binder.go:1614-1622`), and its subsequent manifest rows key on the `"default"` slot sentinel while its
earlier rows key on the session id.

**`ResumeConversationOnly` is unreachable.** `classifyResume` consults `evictionStateLookup` first
(`start.go:4163-4168`), and no `cmd/` site sets that field. The writer it would read,
`pkg/gateway/storage/evictionfallback`, has no production caller either. Since the adapter also always
reports `"full"`, `workspaceLost: true` never appears on `session.resumed`.

### 4.6 Gateway drain

This is the gateway pod's own termination, and it is the only path on which an eviction-triggered
checkpoint is driven today.

```
 kubelet          GW-A (draining)                       pod adapter        runtime
   |                    |                                   |                 |
   |==GET /internal/prestop==>| httpsurface.go:643           |                 |
   |                    | fired.CAS(false,true)  prestop.go:344                |
   |                    | draining.Store(true)   prestop.go:361                |
   |                    |   (/readyz now 503 "draining"  readiness.go:61)      |
   |                    | ctx = WithTimeout(grace=240s) prestop.go:382         |
   |                    |   [ barrier fan-out budget 90s  prestop.go:505 ]     |
   |                    | ListHeldByReplica(2s)  wiring.go:104 ==> PG          |
   |                    |   targets = [(tenant, session, generation), ...]     |
   |                    |                                   |                 |
   |   per target, CONCURRENTLY (barrier.go:219-229):        |                 |
   |               (a) |==CheckpointStart{trigger=EVICTION}=>| link(ckptID)    |
   |                    |                                   |==checkpoint_req=>|
   |                    |<==chunks, grants, Summary=========|                 |
   |                    |                                   | complete()      |
   |               (b) |==CheckpointBarrier{gen, barrierID}=>| gen == lastFenced?
   |                    |                                   | quiesced = true |
   |                    |                                   | gate.open(); wait|
   |                    |<==CheckpointBarrierResponse=======|                 |
   |                    |                                   | EmitAck ==> DROPPED
   |                    | meta.Upsert(barrierID, ref)  barrier.go:248          |
   |                    |                                   |                 |
   |   post-barrier loop, SEQUENTIAL (prestop.go:390-451):   |                 |
   |                    | acked   ==> skip                  |                 |
   |                    | unacked ==> tier cap, clamp(grace-30s),              |
   |                    |             CheckpointWithTrigger(EVICTION)          |
   |<==200 summary======|                                   |                 |
   |==SIGTERM==========>| runserver.go:212, shutdown :213-252                  |
   |                    | NO lease release, NO mirror release                  |
```

**Hops:**

1. Kubelet to gateway. The chart declares `lifecycle.preStop.httpGet` on `/internal/prestop`
   (`charts/lenny/templates/gateway-deployment.yaml:1607-1611`) with a 240-second grace
   (`:1626`). The handler is registered for both GET and POST at
   `cmd/lenny-gateway/httpsurface.go:642-643`; the kubelet issues GET.
2. Gateway, in process. A compare-and-swap on an atomic flag prevents a second drain
   (`prestop.go:344-350`), and `run` sets the draining flag first (`:361`), which flips `/readyz` to
   503 (`cmd/lenny-gateway/readiness.go:61-63`) and removes the replica from Service endpoints.
3. Gateway to Postgres, C15. Three nested budgets apply, and only the outermost bounds the hook.
   `Hook.run` first wraps everything in the termination grace period at
   `pkg/gateway/podlifecycle/prestop/prestop.go:382`, taken from `h.gracePeriod()` (`:530-535`), which
   the chart drives to 240 seconds through `LENNY_TERMINATION_GRACE_SECONDS`
   (`charts/lenny/templates/gateway-deployment.yaml:757`, `:1626`, read at
   `cmd/lenny-gateway/main.go:2541`). Inside it, `fireBarrier` (`prestop.go:500-517`) wraps the whole
   barrier fan-out in one wall-clock budget of `BarrierAckTimeout`, 90 seconds by default
   (`cmd/lenny-gateway/flags.go:530-532`). Hop 8 derives a third budget from the same grace value.
   `MirrorTargetLister.Targets`
   (`barrier/wiring.go:97-124`) reads `coordinator_replica = <this replica> AND released_at IS NULL`
   under a two-second deadline (`:99-105`), falling back to the in-memory registry snapshot on any
   error (`:120-123`).
4. Gateway, per target. `dispatchOne` (`barrier.go:209-258`) allocates a barrier id (`:267-275`), then
   launches `CheckpointWithTrigger(ctx, tenant, session, TriggerEviction)` in a goroutine at `:225`
   **before** calling `c.dispatch.Send` at `:228`, and waits at `:229`. Starting the stream first is
   deliberate: the adapter holds the acknowledgement until the stream terminates, so sending the barrier
   first would deadlock. The checkpoint error is recorded on the outcome at `:230`, strictly before the
   barrier error branches at `:231-238`, so a rejected barrier does not suppress the checkpoint.
5. Gateway to pod, C5. `PodDispatcher.Send` (`wiring.go:44-59`) resolves the connection from the
   in-process registry (`:62-69`, wired at `cmd/lenny-gateway/httpsurface.go:606-613`). There is no
   fresh-dial fallback; the retirement is recorded in commit `21032008` and the rationale is at
   `wiring.go:29-31`. A session with no local binding produces
   `barrier: no adapter connection for session %s`.
6. Pod, in process. The barrier handler validates and then quiesces and holds (section 3.6).
7. Gateway to Postgres. On acknowledgement, `barrier.go:239-257` upserts the barrier record. A store
   write error is recorded on the outcome without clearing `Acked` (`:248-256`).
8. Gateway, sequentially. The post-barrier loop (`prestop.go:390-451`) skips every acked session
   (`:397-400`), and for the rest selects a tier budget from the recorded workspace size
   (`SelectTier` `:129-139`, tiers `:89-93`), clamps it to the grace period minus thirty seconds
   (`:149-161`), and drives `CheckpointWithTrigger` with the eviction trigger
   (`prestop/checkpoint_adapter.go:32-41`). A deadline overrun increments
   `lenny_gateway_sigkill_streams_total` (`prestop.go:429-437`).
9. Kubelet to gateway. SIGTERM after the hook returns. `runserver.go:213-252` closes the HTTP servers,
   the Postgres pool, and Redis. It releases no coordination lease and no mirror row, and the sweeper's
   context is cancelled only after `runServer` returns (`cmd/lenny-gateway/main.go:347`), so the sweeper
   keeps renewing the draining replica's leases throughout the drain. Handoff to a peer therefore waits
   for the 60-second Redis TTL rather than starting at drain time.

**Barrier behaviors worth stating.** The `quiesced` flag blocks nothing (section 3.6). The barrier gate
is per pod, so a second concurrent barrier overwrites the first's channel. The gate link is racy: the
gateway starts the Checkpoint stream before sending the barrier, and if `CheckpointStart` reaches the
adapter first then `link` returns false (`coordination.go:184-186`), no completion is armed, and the
barrier that opens afterward blocks until the shared 90-second budget expires. The adapter's own tests
avoid the race by waiting for the gate before linking (`pkg/adapter/coordination_test.go:279`, `:283`).

**A never-fenced session cannot be barriered.** `CoordinatorFence` is issued only from `fenceResumedPod`
(`start.go:4124-4136`, reached from the two resume branches at `:3877` and `:3955`) and from the
crash-takeover re-adopt (`coordination_seams.go:234`). The ordinary start path never fences. For such a
session the mirror row's generation is 0, so the barrier request carries 0 and the adapter rejects it
`InvalidArgument` at `coordination.go:225-227`, before the equality gate. Since `wiring.go:51-53`
converts only `FailedPrecondition` into `ErrGenerationStale`, that lands on `Outcome.Err` rather than
`Outcome.Stale`. The concurrently-driven checkpoint still runs, so a capture is still taken; the
quiesce-and-hold contract is what is lost.

**The degraded target path issues an unbounded read.** The fallback closure calls
`w.sessions.Get(context.Background(), ...)` once per binding
(`cmd/lenny-gateway/httpsurface.go:594`), outside both the two-second mirror deadline and the
90-second acknowledgement budget.

**Barrier observability is declared and not emitted.** `lenny_checkpoint_barrier_ack_duration_seconds`
and `lenny_checkpoint_barrier_ack_total` exist in the metric catalog
(`pkg/observability/metrics/catalog.go:128`, `catalog_test.go:31`) with no emitter, and `quiesced_ms` is
decoded by the client (`adapterclient/client.go:558`) and then discarded by the dispatcher
(`wiring.go:56-58`). `WorkspaceRecoveryFraction` on the persisted record is always nil, because the
proto response carries no such field (`schemas/lenny-adapter.proto:1382-1386`).

### 4.7 Coordinator handoff and crash takeover

```
 GW-A (dead)   Redis lease      GW-B sweeper (15s)     Postgres      pod adapter
     x              |                  |                   |               |
     |  lease TTL 60s lapses           |                   |               |
     |              |<==Get(tenant, session)===| coordination.go:306        |
     |              |==ErrNotFound (priorHolder empty)==>|                  |
     |              |                  | bound? NO           :313           |
     |              |                  | isRunningPod? YES   :257           |
     |              |                  | inAdoptionBackoff? NO :319         |
     |              |<==Acquire(GW-B, ttl)=====| coordination.go:357        |
     |              |                  |==Update gen++====>| RecordHandoff  |
     |              |                  |<==generation N+1==|                |
     |              |                  |   (0 ==> Release + skip :411)      |
     |              |                  |==Get(PodAssignment)==>|            |
     |              |                  |==ReadoptConnect: dial, NO handshake==>|
     |              |                  |==CoordinatorFence(session, N+1)=====>|
     |              |                  |                   | gen > lastFenced
     |              |                  |                   | exitHoldState  |
     |              |                  |<==Accepted=========================|
     |              |                  | publish() ==> registry.Put         |
     |              |                  | clearAdoptionBackoff               |
     |              |                  |==mirror Upsert(GW-B, gen = OLD N)=>|  (lags by one)
 FAILURE BRANCH
     |              |<==Release(GW-B)==| coordfence.go:198 or seams.go:173  |
     |              |                  | recordAdoptionBackoff 2s to 16s    |
```

**Hops:**

1. Gateway, in process. The sweeper runs on a 15-second tick
   (`cmd/lenny-gateway/flags.go:528`, loop at
   `pkg/gateway/coordination/coordination/coordination.go:594-607`), started at
   `cmd/lenny-gateway/workers.go:1298-1300` only when Redis is configured.
2. Gateway, per row. A terminal row releases the mirror row and continues (`coordination.go:293-299`).
   No eviction and no lease release happen on that branch.
3. Gateway to Redis. `leases.Get` observes the prior holder before any acquire (`:305-311`). A
   non-not-found error aborts the whole sweep pass.
4. Gateway, in process. `bound := s.boundHere(row.ID)` reads the local registry
   (`:313`, seam at `cmd/lenny-gateway/coordination_seams.go:45-51`).
5. Gateway, in process. **Dead-connection eviction**: `bound && !s.connAlive(row.ID)` evicts the binding
   and releases the lease, then continues (`:332-338`). `connAlive` probes `bind.Adapter.Alive()`
   (`coordination_seams.go:58-67`), which reports false only for the `Shutdown` and `TransientFailure`
   connectivity states (`adapterclient/client.go:74-81`), so `Idle` and `Connecting` report alive and a
   fresh lazy binding is never evicted. `EvictBinding` drops both the registry entry and the executor's
   cached Attach stream (`coordination_seams.go:75-82`). **This branch takes no checkpoint.**
6. Gateway, in process. `eligible := bound || priorHolder == s.replicaID || adoptable` (`:352`).
   Everything else is skipped without an acquire attempt, which is the co-location invariant: a peer
   sweep never lands the lease on a replica that holds no binding.
7. Gateway to Redis. `leases.Acquire` (`:357`). `ErrHeld` skips the session (`:358-361`).
8. Gateway, in process. The takeover predicate is `!bound && priorHolder != s.replicaID` (`:387`).
9. Gateway to Postgres. `RecordHandoff` (`:388`, body `:480-498`) atomically increments
   `coordination_generation`, refusing when the row went terminal. It returns 0 on refusal or transient
   error, in which case the sweeper releases the lease and continues (`:411-414`), deliberately not
   self-holding so the takeover predicate can fire again.
10. Gateway to kube-apiserver and the pod. `readoptAndFence` (`coordination_seams.go:199-251`) reads the
    session row (`:208`), calls `dialer.ReadoptConnect` (`:219`), which dials without any handshake
    (`binder.go:1161-1168` then `:1129-1140`), and calls `fencer.Fence`
    (`coordination_seams.go:234`) so `CoordinatorFence` is the first RPC on the connection. The
    generation the sweeper computed is discarded at
    `coordination_seams.go:156`; the fencer re-reads it from the row (`coordfence.go:144`).
11. Gateway, in process. `publish()` is invoked only after the fence acknowledges
    (`coordination.go:441`), and it does `registry.Put(bind)` (`coordination_seams.go:250`). Nothing
    enters the registry before the fence, so no operational RPC can reach the pod pre-fence.
12. Gateway to Postgres, C15. `upsertMirror` (`:447`).

**The fence retry and relinquish policy.** `coordfence.Fence` (`coordfence.go:143-190`) reads the
authoritative generation, clamps a non-positive value up to 1, and retries up to three attempts
(`:53`). A `FailedPrecondition` or a non-accepted response is treated as stale: the generation is
re-read, and if it advanced the attempt is retried at the new value, otherwise the fencer relinquishes
(`:165-180`). Relinquish releases the Redis lease itself (`:196-202`) and returns `ErrRelinquished`,
which `start.go:3569-3574` classifies as a transient pod-claim error so the client's retry routes to the
rightful coordinator. The sweeper then records a jittered adoption backoff between two and sixteen
seconds (`coordination.go:432`, bounds `:109-112`).

**What the pod observes on coordinator crash: nothing.** Its gRPC channel to the crashed replica moves
to `TransientFailure`, and the adapter has no wired notification path. The pod does advertise a
`grpc.health.v1.Health` service on the same listener (C20, section 3.2), but no gateway code is a client
of it, so the health service plays no part in detection in either direction. Hold state cannot be entered
(section 6a.2), so the pod keeps `lastFenced` at its old value, keeps serving every RPC to anyone who
dials it, and keeps the runtime running. Recovery is entirely gateway-driven, and the fence at
generation N+1 is accepted because N+1 exceeds N. `exitHoldState` then clears a hold that was never
entered, which is a no-op (`holdstate.go:134-136`).

**Defects on this path.**

The re-adopted `BindResult` is incomplete. `coordination_seams.go:243-249` sets only `SessionID`,
`TenantID`, `SandboxName`, `PodIP`, and `Adapter`. It leaves `SlotID`, `MaxConcurrentSessions`
(`binder.go:495`, `:505`), `Recycle` (`:516`), `CleanupCommands`, and `WorkspaceRoot` unset. The
consequence for a recycling pool is that a taken-over session drains its pod instead of recycling it,
because `binder.Release` branches on `result.Recycle` at `binder.go:1882`. The consequence for a slot
session would be a bypassed `SLOT_ID_REQUIRED` guard and a slot-less Attach, but that outcome is
currently unreachable, because `CoordinatorFence` itself calls `checkSession` (`coordination.go:90`) and
a concurrent pod never sets the pod-global session id, so the fence fails and the takeover relinquishes
before any binding is published.

The mirror lags one generation. `coordination.go:447` passes `row.CoordinationGeneration` from the
pre-handoff list snapshot taken at `:288` rather than the value `RecordHandoff` returned at `:388`. For
up to one sweep interval after a takeover the mirror advertises N-1 while the pod is fenced to N. A
drain inside that window barriers at the stale generation and is rejected. It self-corrects on the next
sweep.

A losing coordinator is not fenced out of its open streams. `Attach` carries no generation field
(`adapterclient/client.go:846-855`, `attach.go:28-57`), no interceptor checks one
(`pkg/adapter/transport.go:46-47`), and the adapter performs no in-flight cancellation
(`coordination.go:81-82`). Combined with the sweep having no "I lost the lease, drop my binding" branch
(the only eviction trigger is a dead connection at `:332`) and no pod-side single-consumer guard on
Attach, a genuine split-brain leaves two live content consumers on one runtime.

The nil-collaborator guard leaks the lease permanently, in a partially-wired posture. The guard at
`cmd/lenny-gateway/coordination_seams.go:157-159` fires only when one of `podBinder`, `podRegistry`,
`coordFencer`, `sessions`, or `coordLeaseStore` is nil. All five are set in a chart-rendered gateway
(`cmd/lenny-gateway/stores.go:1227` sessions, `:1466` lease store, `:1570` fencer, `:2217-2218` binder
and registry), and the sweeper that reaches the guard is constructed inside the same Redis-gated block
that sets the lease store (`stores.go:1466`, `:1489-1497`) and started only when non-nil
(`cmd/lenny-gateway/workers.go:1298-1300`). The branch is therefore reachable only with Redis wired and
a Kubernetes client absent. In that posture the guard returns an error before `readoptAndFence` runs, so
`releaseAfterReadoptFailure` (`:172-177`) never executes and the lease acquired at
`pkg/gateway/coordination/coordination/coordination.go:357` stays held. On the next sweep `bound` is
still false but `priorHolder == s.replicaID`, so `eligible` is true and the lease is renewed, while the
takeover predicate can never fire again. The session's lease is pinned to a replica that holds no
binding, and every peer's acquire returns `ErrHeld`. The doc comment at `coordination_seams.go:153-154`
claims a peer replica takes over in that case, which the code contradicts.

### 4.8 Kubelet eviction of an agent pod

**No checkpoint of any kind is driven when the kubelet terminates an agent pod that holds a live
session.** Every link in the intended chain is unwired or absent.

```
 kubelet          adapter container            runtime container      gateway replica
   |              (PID 1 = lenny-adapter)      (agent process)        (holds binding + lease)
   |   t=0 terminate pod                              |                       |
   |==SIGTERM (no preStop) =========================> |                       |
   |   podspec.go:609-620 has no Lifecycle     [agent dies immediately]       |
   |                      |                           x                       |
   |==exec preStop=======>| main.go:104-110                                   |
   |   lenny-adapter prestop --timeout=110s                                   |
   |              [prestop proc] ==kill(1, SIGTERM)==> [adapter]              |
   |                      |                              main.go:414          |
   |                      |                              ShutdownDemoteSDK    |
   |                      |                              lifecycle.Close (nil)|
   |                      |                              srv.GracefulStop     |
   |                      |                              x adapter exits      |
   |                                                                          |
   |            ####  NOTHING IS SENT TO THE GATEWAY  ####                    |
   |     x EmitAdapterEvicting   UNWIRED, no production caller                |
   |     x AdapterTerminating    would drop, no control stream open           |
   |     x FINAL_USAGE_REPORT    would drop, no control stream open           |
   |     x hold state            unarmable, arm is the close of a stream      |
   |     |                       nobody opens                                 |
   |     x eviction checkpoint   ABSENT on this edge                          |
   |                                                                          |
   |                                            t+<=15s coordination sweep:   |
   |                                            ConnAlive false ==>           |
   |                                            EvictBinding + lease Release  |
   |                                            NO CHECKPOINT                 |
   |                                            coordination.go:332-338       |
   |                                                                          |
   |                                            t+<=60s orphan reconciler:    |
   |                                            reads agent_pod_state (C21)   |
   |                                            session ==> failed            |
   |                                            reason orphan_pod_terminated  |
   |                                            orphansession.go:45, :55, :58 |
```

**Hops:**

1. Kubelet to the runtime container. SIGTERM with no preStop hook. The container literal at
   `podspec.go:609-620` ends with `SecurityContext` and declares no `Lifecycle`. The agent process dies
   while the adapter is still up.
2. Kubelet to the adapter container. The preStop exec runs `lenny-adapter prestop --timeout=110s`
   (`podspec.go:607`, hook rendered at `:1336-1351`, margin constant `:82`), dispatched before any gRPC
   server starts (`cmd/lenny-adapter/main.go:104-110`).
3. preStop process to the adapter process. `runPreStop` (`cmd/lenny-adapter/prestop.go:68-94`) checks
   that PID 1 is alive (`:69`), sends SIGTERM (`:74`, implementation `:100`), polls every 200
   milliseconds until it exits or the timeout elapses (`:84-90`), and always returns 0 (`:94`). It opens
   no gRPC connection, mints no checkpoint id, and names no session.
4. Adapter, in process. The SIGTERM handler (`cmd/lenny-adapter/main.go:411-424`) calls
   `ShutdownDemoteSDK` (`:420`), `lifecycle.Close()` if the runtime lifecycle channel exists
   (`:421-423`, nil in a rendered pod), and `srv.GracefulStop()` (`:424`). `ShutdownDemoteSDK`
   (`pkg/adapter/sdkwarm.go:69`) is a no-op in every chart-rendered pod. It returns at `:72` because the
   production runtime is `SocketRuntimeProcess` (`cmd/lenny-adapter/main.go:354`), which does not
   implement `SDKWarmRuntime` (`sdkwarm.go:119-135`), and it would return again at `:78` because
   `s.sdkConnected` is never set: the only writer is `Server.PreConnect` (`sdkwarm.go:166`), whose sole
   caller is the demo binary `cmd/runtimes/preconnect-echo/main.go:142`. Nothing clears `s.sessionID` on
   this path.
5. Nothing crosses to the gateway.
6. Gateway, up to 15 seconds later. The coordination sweep observes a dead connection and evicts the
   binding and the lease (`pkg/gateway/coordination/coordination/coordination.go:332-338`). No
   checkpoint is driven.
7. Gateway, up to 60 seconds later, C21. The orphan-session reconciler
   (`pkg/gateway/session/orphansession/orphansession.go`, 60-second cadence at `:45`, wired at
   `cmd/lenny-gateway/workers.go:1086-1088`) observes the pod's `terminated` phase in the
   `agent_pod_state` mirror (`:50-55`) and forces the session to `failed` with reason
   `orphan_pod_terminated` (`:58`).

   The mirror it reads is written by the WarmPool controller rather than by any gateway:
   `Reconciler.syncMirror` converges it per pool (`pkg/controller/warmpool/controller.go:248`, body
   `:432-447`), and `MirrorReconciler` re-derives the whole row set on startup and on each
   leader-election acquisition (`pkg/controller/warmpool/mirror_recovery.go:18-33`). The mirror carries
   its own staleness contract: past `DefaultStaleMirrorThreshold`, 60 seconds
   (`orphansession.go:46-49`), the reconciler stops trusting the mirror for a pool and falls back to a
   direct Kubernetes read, wired in production as `sandboxPhaseReader`
   (`cmd/lenny-gateway/workers.go:1075-1085`).

   The reconciler is not leader-gated. `cmd/lenny-gateway/workers.go:1088` launches it with a bare
   `go orphanSessionReconciler.Run(...)`, outside the `leaderGate` collected at `workers.go:628` and run
   at `:1010`, despite `orphansession.go:43-44` describing the cadence as "the same leader-only pattern
   as orphan claim detection". The wiring's own comment states the choice is deliberate on the grounds
   that the transition is idempotent across replicas (`workers.go:1066-1067`). The row write is indeed
   idempotent: the mutator no-ops on an already-terminal row (`orphansession.go:344-346`). The terminal
   funnel is not gated by that. The post-condition at `:356-360` passes whenever the row reads
   `failed` with the orphan reason, which is true for a row a peer replica already failed with the same
   reason, so both replicas reach `r.terminal.OnSessionTerminal` at `:369`. This is the same
   duplicate-funnel exposure section 4.3 raises for `sweepMaxAge`.

**Every intended link, and its status:**

| Link | Status | Proof |
|:--|:--|:--|
| Producer `setEvicting`, `liveBoundSessions`, `EmitAdapterEvicting` | **UNWIRED** | definitions at `pkg/adapter/session.go:365`, `:385`, `pkg/adapter/controlchannel.go:248`; callers are only in-package tests plus the test-only export `pkg/adapter/export_test.go:9` |
| Transport, the gRPC control stream | **UNWIRED** | no gateway client (section 3.7) |
| Consumer, a gateway `AdapterEvicting` handler | **ABSENT** | `grep -rn "AdapterEvicting" --include=*.go . \| grep -v "^./pkg/adapter/"` returns nothing |
| Routing read `coordlease.GetBySession` | **UNWIRED** | definitions and tests only |
| `coordinator_address` population | **UNWIRED** | `InterReplicaAddress` never set in production (`cmd/lenny-gateway/stores.go:1490-1497`) |
| At-bind mirror seed | **ABSENT** | the only `Upsert` is `coordination.go:569` inside the sweeper |
| Inter-replica control forward | **ABSENT** | no gateway-to-gateway service, and NetworkPolicy denies the flow |
| Best-effort eviction snapshot | **UNWIRED, doubly** | `checkpoint.go:179` and `:192` gate on `isEvicting()`, which is never true, and on `s.Lifecycle`, which is nil |
| Agent-pod preStop checkpoint | **ABSENT** | `cmd/lenny-adapter/prestop.go:68-94` signals and polls |
| PodDisruptionBudget protection for a busy pod | **ABSENT by design** | `pkg/controller/warmpool/pdb.go:70-76` selects the idle state label; a claimed pod is labelled active (`pkg/sandbox/state/state.go:84-85`) |
| Eviction-API admission gate on `pods/eviction` | **WIRED, feature-gated off by default** | `charts/lenny/templates/admission-policies/drain-readiness-webhook.yaml:3`, gated at `:11`, and `charts/lenny/values.yaml:1696` sets `features.drainReadiness: false` |

**One control does exist on this edge, and it gates a different condition.** The
`lenny-drain-readiness` ValidatingAdmissionWebhook intercepts CREATE on the `pods/eviction` subresource
in agent namespaces and rejects the eviction while the artifact store is degraded, fail-closed
(`charts/lenny/templates/admission-policies/drain-readiness-webhook.yaml:1-11`). It queries the
gateway's `GET /internal/drain-readiness` endpoint over C22
(`cmd/lenny-gateway/httpsurface.go:530`, client at `pkg/admission/webhook/drain_readiness.go:34-50`),
and the gateway serves two further internal endpoints on the same surface,
`GET /internal/runtime-upgrade/active` (`httpsurface.go:539`) and
`POST /internal/audit/node-drain-forced` (`:549`). Two limits apply. The webhook renders only under
`admissionWebhooks.enabled` and `features.drainReadiness`
(`drain-readiness-webhook.yaml:11`), and the chart default for the second is false
(`charts/lenny/values.yaml:1696`). And what it gates is artifact-store health rather than session
liveness, so even when enabled it drives no checkpoint. It also covers only the Eviction API: a direct
pod `DELETE` and a kubelet node-pressure eviction do not traverse `pods/eviction` and cannot be
admission-gated at all.

**Three code comments assert a caller that is not in the tree.** `pkg/adapter/session.go:355-357`,
`session.go:380-384`, and `pkg/adapter/controlchannel.go:241-243` all describe a "kubelet-path SIGTERM
handler" that emits one `AdapterEvicting` per live bound session. No such handler exists. A reader of
`pkg/adapter` in isolation would conclude the producer side is complete and only the gateway consumer
is missing; the producer has no trigger either.

**Net effect.** The workspace of an evicted busy pod is lost down to the last periodic checkpoint, which
can be up to ten minutes stale, or entirely when none was taken. The two production eviction-trigger
drive sites both hang off the gateway pod's own preStop
(`pkg/gateway/coordination/barrier/barrier.go:225` and
`pkg/gateway/podlifecycle/prestop/checkpoint_adapter.go:39`).

### 4.9 The concurrent-session (multi-slot) pod

This section is a structural analysis rather than a traced scenario: it gives a diagram and then states
what is partitioned per slot and what is not. The diagram's steps use C17, C18, C1, C14, C2, C3, and C4
in that order.

Setup: a pool with `sessionPolicy.maxConcurrentSessions` above one, two sessions of the same tenant on
the same pod as two slots, and two gateway replicas. The slot identifier equals the session identifier
(`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:683`).

```
 client-A   client-B     rep-1          rep-2        Redis/K8s      pod-1 adapter
    |           |          |              |              |               |
    |==POST /sessions=====>|              |              |               |
    |           |          |==INCR active_slots=========>| 1             |
    |           |          |==CREATE claim-pod-1========>| (acquisition) |
    |           |          |==dial + NegotiateVersion===================>|
    |           |          |<==close====================================|
    |           |==POST /sessions=========>|              |               |
    |           |          |              |==Pass 1: claim-pod-1 exists  |
    |           |          |              |==INCR active_slots=========>| 2
    |           |          |              |   (no claim write)           |
    |           |          |              |==dial + Negotiate===========>|
    |==POST /start========>|              |              |               |
    |           |          |==dial #2, Prepare/Setup/AssignCreds(slot=A)=>|
    |           |          |==StartSession{slot=A}======================>| st.sessionID=A
    |           |          |==Acquire lease sess-A======>| holder rep-1  | s.sessionID=""
    |           |          |==podRegistry.Put(sess-A)     |              |   (stays empty)
    |           |==POST /start============>|              |               |
    |           |          |              |==StartSession{slot=B}=======>| st.sessionID=B
    |           |          |              |==Acquire lease sess-B======>| holder rep-2
    |==POST /sess-A/messages==>| (on the holder)          |               |
    |           |          |==Attach{sess-A, slot=A}====================>| demux by slotId
    |==POST /sess-A/messages==================>| (on rep-2, the non-holder)
    |           |          |              |  registry.Get(sess-A) MISS   |
    |<==500 EXECUTOR_FAILURE ==============|                              |
    |           |          |==Checkpoint{slot=A}========================>| ops.Begin(cp, A)
    |           |          |              |==Checkpoint{slot=B}=========>| QUEUED behind A
    |           |          |   X gRPC control stream never opened         |
    |    crash-takeover of sess-A after rep-1 dies                        |
    |           |          x              |==CoordinatorFence==========>| checkSession FAILS
    |           |                         |<==FailedPrecondition========|
    |           |                         |  read as STALE, relinquish  |
```

**What is correctly per-slot:** the adapter's `slotState` holding the slot's session id, workspace
subtree, credential set, and expiry timers (`pkg/adapter/slot.go:22-35`, registry at `:74-95`); the
slot-qualified workspace, setup, credential, and start RPCs driven from `materializeSlot`
(`slotbinder.go:263-348`); the slot-qualified Attach with output demultiplexing (`attach.go:41-44`,
`:71-73`); and the slot-qualified Checkpoint with per-slot roots, per-slot probe, and per-slot manifest
key (`checkpoint.go:101`, `slot.go:147-173`, `uploaddriver.go:409`).

**What is shared per pod with no per-slot partition:** the single runtime process and its one socket
(`slot.go:187-197`, `socketruntime.go:319-335`); the operation lock (`oplock.go:39-63`); the
coordination state holding one `lastFenced` (`coordination.go:25-39`); the hold state
(`holdstate.go:39-45`); the barrier gate (`coordination.go:149-155`); the control sink
(`controlchannel.go:108-117`); the eviction flag (`session.go:385-397`); the `SandboxClaim`; and the
Redis slot counter.

**Two different replicas can hold two slots on the same pod.** The `SandboxClaim` carries no replica
identity (`pkg/apis/lenny/v1alpha1/sandboxclaim_types.go:20-32`), Pass 1 of the placement loop checks
only existence, non-terminality, and tenant match (`slotclaimer.go:418-433`), the capacity gate is a
shared Redis counter keyed on the pod alone (`slotcounter.go:246-248`), and a grep for replica identity
across `pkg/gateway/podlifecycle` and `pkg/gateway/storage/slotcounter` returns nothing. The coordination
lease is keyed per session, so two leases with two different holders legitimately coexist against one
pod. There is also no session affinity at the edge, so the two `/start` requests land on arbitrary
replicas.

**The root cause of most multi-slot breakage.** `startSessionSlot` sets `st.sessionID` on the slot
(`pkg/adapter/slotsession.go:45`) and never sets the pod-global `s.sessionID`. That field has two
setters, neither on the slot-start path: `claimSession` (`pkg/adapter/session.go:416-425`), called from
`session.go:117` and `pkg/adapter/resume.go:47`, and `claimSessionForConfigure`
(`pkg/adapter/sdkwarm.go:290-301`), called from the `ConfigureWorkspace` RPC at `sdkwarm.go:217`. The
adapter states the consequence itself at `session.go:234-236`. So `checkSession` can never pass on a
concurrent pod, and every RPC gated by it fails `FailedPrecondition "pod has no assigned session"`.

| RPC gated by `checkSession` with no slot branch | Handler | Consequence on a concurrent pod |
|:--|:--|:--|
| `Interrupt` | `lifecycle.go:30` | the gateway logs the error and still writes `suspended` with reason `InterruptAcknowledged` |
| `SignalDeadline` | `lifecycle.go:77` | the pre-expiry warning never reaches the pod |
| `ReportUsage` | `usage.go:265` | the direct-mode usage pull fails |
| `CoordinatorFence` | `coordination.go:90` | crash takeover can never succeed |
| `CheckpointBarrier` | `coordination.go:217` | the drain barrier is silently recorded as a benign stale outcome |

None of `InterruptRequest` (`schemas/lenny-adapter.proto:1046`), `SignalDeadlineRequest` (`:1207`),
`ReportUsageRequest` (`:1435`), or `CheckpointBarrierRequest` (`:1361-1370`) carries a `slot_id` field,
so the defect is at the wire contract rather than only in the handlers. Contrast `ShutdownRequest`
(`:1464`), `CheckpointStart` (`:1120`), and `StartSessionRequest` (`:918`), which do.

**Crash takeover of a slot session cannot succeed.** The sweeper detects the lapsed lease, bumps the
generation, and calls `CoordinatorFence`, which fails `FailedPrecondition`. `coordfence` reads that as a
generation-stale rejection (`coordfence.go:165-166`), re-reads the generation, sees no advance, and
relinquishes (`:180`), incrementing `lenny_coordinator_handoff_stale_total` (`:171`) for what is
actually a routing defect. The sweeper records an adoption backoff and the cycle repeats.

**Cross-replica head-of-line blocking on checkpoints.** The operation lock is pod-global. A checkpoint
against a distinct slot is admitted into the pending set and blocks in `wait` until promotion, rather
than being rejected, so one replica's slot checkpoint head-of-line blocks another replica's
(`oplock.go:107-113`, `:119-123`, promotion at `:173-181`). Neither replica can observe the other's queue depth, and the gateway's own attempt deadline
can expire while queued. An interrupt is refused whenever any checkpoint is pending (`oplock.go:88-94`),
and vice versa (`:84-87`).

**The unhealthy-slot threshold under-counts.** The slot-health tracker is per-replica and in-memory
(`slothealth.go:55-60`, one per replica at `cmd/lenny-gateway/sessiondeps.go:261`). Bind-time slot
failures are recorded on the binding replica (`start.go:2760-2788`), while adapter-reported leaks arrive
over `ReportSessionScrub`, which the adapter sends to the Service address and therefore to an arbitrary
replica (`gatewaylink.go:37-79`). A pod whose slots span replicas can accumulate leaks without any
single replica reaching the threshold, so the drain-request stamp never fires. The
`sessions_served` counter is unaffected, because it is Postgres-backed
(`leasecontrol/scrubreport_server.go:460`).

**Slot release is correct across replicas.** `PodExecutor.Release` branches on `SlotID`
(`pod.go:332-334`) into `binder.ReleaseSlot` (`slotbinder.go:525`), `ShutdownSlot` (`:539`), and
`SlotClaimer.ReleaseSlot` (`slotclaimer.go:740`). The Redis decrement is atomic (`:762`), only the
occupancy-zero edge touches the claim (`:767-807`), and a leaked slot deliberately skips the decrement
(`:752-759`). The recycle-boundary timer armed at that edge is per-replica
(`slotclaimer.go:793-795`), and the `ReportPodScrub` that cancels it arrives at a Service-routed
replica, so the timer and the cancel are generally on different replicas. The design tolerates that: the
timer callback re-reads the claim before retiring and returns without action when the claim already
advanced (`recycleboundary.go:378-396`).

---

## 5. Cross-replica behavior: the off-holder endpoint matrix

### 5.1 What "holds the binding" means, and why an off-holder request cannot recover

The binding is process-local memory (`pkg/gateway/podlifecycle/podsession/registry.go:14-17`, per-replica
doc at `:8-13`). The coordination lease that marks the coordinating replica is acquired at exactly four
bind sites (`start.go:858`, `:1042`, `:2825`, `:3941`) and is never consulted by any operational
endpoint: a grep for `s.leaseStore.` across `pkg/gateway/sessionserver` returns only the acquire at
`start.go:2894` and the rollback release at `:2930`.

There is no mechanism by which an off-holder request reaches the coordinator.

- `ForwardMessage` is ABSENT. It exists nowhere as a Go identifier or a proto RPC; the only repository
  hits outside `proposals/` are deferral comments at `messages.go:241` and `:524`.
- No handler emits a redirect. A grep for `StatusTemporaryRedirect`, `StatusPermanentRedirect`,
  `coordinatorFor`, `forwardTo`, or `affinity` across `pkg/gateway` and `cmd/lenny-gateway` returns only
  `pkg/gateway/runtime/tenantaffinity`, which routes gateway to pod for stateless pools rather than
  client to replica.
- The Redis hot routing cache that would make affinity possible is implemented and has zero production
  callers: `grep -rn "routingcache" --include=*.go . | grep -v "^./pkg/gateway/session/routingcache/"`
  returns nothing.
- The sweeper refuses to move a live lease. `eligible := bound || priorHolder == s.replicaID || adoptable`
  (`coordination.go:352`) and `adoptable` requires an unheld lease (`:314`), which a live holder
  falsifies by renewing every sweep. Adoption is possible only after the holder dies and the 60-second
  TTL lapses.

An off-holder outcome is therefore terminal for the client. Retrying is a coin flip across replicas, and
no error envelope carries a hint that a different replica would succeed.

### 5.2 The matrix

Route table at `pkg/gateway/sessionserver/sessionserver.go:2038-2104`. The matrix covers the REST
session surface registered there. Three other production entry points reach the same `PodExecutor.Send`
and take the same registry miss, with a different error envelope: `POST /v1/chat/completions`
(registered at `pkg/gateway/environment/translator/openai_chat.go:198`, send at `:273`, rendered as
HTTP 500 `server_error` "executor failure: …" at `:274-277`), `POST /v1/responses`
(`pkg/gateway/environment/translator/open_responses.go:334`, same envelope at `:335-338`), and the MCP
tool surface (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:480` and `:2616`, the second being
the delegated-child task delivery variant (c) of 4.1 depends on). None of them is in the tables below.

Verdicts: **CORRECT** means the
same observable outcome as on the holder or a fail-closed rejection; **DEGRADED** means success is
reported while the intended effect did not happen; **BROKEN** means an error or state corruption the
holder would not produce.

#### Broken or degraded off-holder

| Route | Line | Off-holder behavior | Response | Durable side effect | Verdict |
|:--|:--|:--|:--|:--|:--|
| `POST /v1/sessions/{id}/messages`, state `running` | `:2072` | `streamFor` registry miss (`pod.go:134-137`) | **500 `EXECUTOR_FAILURE`**, `details.reason` carries `podexec: session <id> is not bound to a pod` | none | **BROKEN** |
| `POST /v1/sessions/{id}/messages` with `inReplyTo`, state `input_required` | `:2072` | local `inputWaits.Resolve` misses (`inputwait.go:152-163`), falls through to inbox | **200** with `status: "queued"` | message buffered and never redelivered | **DEGRADED**, silent loss |
| `POST /v1/sessions/{id}/messages` with `delivery: immediate`, state `suspended` | `:2072` | `resumeHeldPod` flips the row, then `Send` fails and the payload is buffered | **200** with `status: "queued"` | **row flipped `suspended` to `running`** with no pod resumed, plus a `session.resumed` audit row | **DEGRADED**, state corruption |
| `POST /v1/sessions/{id}/interrupt` | `:2058` | adapter never called (`interrupt.go:120-127`) | **200** plain session response, no `interruptStatus` | row `suspended` with `SuspendedReason = "InterruptAcknowledged"` | **DEGRADED**, false acknowledgement |
| `POST /v1/sessions/{id}/terminate` | `:2059-2060` | seal no-ops, executor release no-ops | **200** | row `completed`, no final snapshot, pod left running | **DEGRADED** |
| `DELETE /v1/sessions/{id}` | `:2051` | identical | **200** | row `cancelled`, same misses | **DEGRADED** |
| `GET /v1/sessions/{id}/events`, SSE form | `:2082` | backlog then permanent silence | **200** `text/event-stream` | none | **DEGRADED** |
| `GET /v1/sessions/{id}/logs`, SSE form | `:2086` | identical | **200** `text/event-stream` | none | **DEGRADED** |
| `POST /v1/sessions/{id}/resume`, tree-recovery side effect | `:2061` | descendants on peer replicas judged orphaned | **200**, the traversal is detached | descendant rows resumed onto fresh pods or marked terminal | **BROKEN**, secondary |

#### Fail closed off-holder

| Route | Line | Mechanism | Response | Verdict |
|:--|:--|:--|:--|:--|
| `POST /v1/sessions/{id}/upload-to-session` | `:2071` | explicit registry check (`upload_to_session.go:113-124`) | **409 `TARGET_NOT_READY`** naming the replica condition | **CORRECT**, the only endpoint with a deliberate off-holder branch |
| `POST /v1/sessions/{id}/start` | `:2053` | `registerBinding` acquires and gets `ErrHeld` | **503 `STARTING_FAILED`** | **CORRECT** |
| `POST /v1/sessions/start` and `POST /v1/environments/{name}/sessions` | `:2048`, `:2047` | acquire ahead of the running commit | **503 `SESSION_CREATION_FAILED`**, reason `coordination_lease_held` | **CORRECT** |
| `POST /v1/sessions/{id}/resume`, primary path | `:2061` | acquire then rollback the fresh pod | **503 `RESUME_FAILED`** | **CORRECT** |

#### Replica-agnostic by construction

These read the session store, the blob store, the interaction store, or the memory, evaluation, and
transcript stores, and touch neither the registry, the executor, nor the binder.

| Route | Line | Note |
|:--|:--|:--|
| `POST /v1/sessions` | `:2038` | creates the row and claims a pod; acquires no lease and publishes no binding (4.1, variant (b)) |
| `GET /v1/sessions`, `GET /v1/sessions/{id}` | `:2049`, `:2050` | row projection only |
| `POST /v1/sessions/{id}/finalize` | `:2052` | reconnects by name from `row.PodAssignment` (`finalize.go:218-219`, `:279-281`) |
| `POST /v1/sessions/{id}/derive`, `/replay`, `/extend-retention`, `/eval` | `:2062-2065` | stored artifacts and row writes |
| memory, upload, and upload-archive routes | `:2066-2070` | memory and blob stores |
| `GET .../messages`, `/transcript`, `/tree` | `:2077-2079` | transcript and tree stores |
| `GET .../events` and `.../logs`, JSON form | `:2082`, `:2086` | the Redis relay `History` read supplies the cross-replica backlog |
| `GET .../artifacts`, `/usage`, `/workspace`, `/setup-output`, `/webhook-events` | `:2091-2098` | row plus blob store |
| `POST .../tool-use/{id}/approve` and `/deny` | `:2100-2101` | correct through a shared-store poll, see 5.4 |
| `POST .../elicitations/{id}/respond` and `/dismiss` | `:2102-2103` | same shared interaction store |

### 5.3 The three distinct message-send outcomes

The classifier at `pkg/gateway/session/messagerouting/messagerouting.go:154-181` selects among three
off-holder behaviors, so "the message endpoint fails with 500" is true only for the first.

**Path A, `running` with no input required.** `ActionDeliver` (`messagerouting.go:178-180`) reaches
`executor.Send` (`messages.go:546`) and the registry miss (`pod.go:134-137`), rendered as
`writeError(500, "EXECUTOR_FAILURE", ...)` with the internal Go error string in `details.reason`
(`messages.go:553-555`). The envelope's category and retryable flag come from the status-code fallback,
because `EXECUTOR_FAILURE` is not in the classification table
(`pkg/gateway/externalapi/errorclassify/errorclassify.go:72-74`). No row write occurs, so this outcome
is at least not corrupting, but the status class is wrong and the internal error text is leaked. The
same string also covers a session that genuinely has no pod, so nothing distinguishes "wrong replica"
from "never started". `EXECUTOR_FAILURE` appears in neither the specification's error catalog nor the
OpenAPI document.

The two other exits from `streamFor` are distinct conditions and must not be conflated with this one:
`ErrSlotIDRequired` (`pod.go:147`) and `podexec: open attach stream` (`pod.go:156`).

**Path B, `input_required` with `inReplyTo`.** `inputwait.Registry` is a process-local map guarded by a
mutex (`inputwait.go:41-49`), constructed once per replica at `cmd/lenny-gateway/sessiondeps.go:95`, and
its imports are `encoding/json`, `errors`, `sort`, `sync`, and `time` (`:11-17`), so there is no shared
store to poll. `Resolve` returns not-found (`:152-163`) and the payload falls through to ordinary
delivery (`messages.go:464-466`), where `Classify` buffers it to the inbox (`messagerouting.go:167-168`)
and the client receives 200 with `queued`. The runtime's blocked `lenny/request_input` waits out its
timeout.

**Path C, `suspended` with `delivery: immediate`.** `resumeHeldPod` (`start.go:3795-3822`) performs a
`store.Update` guarded only on `row.State != StateSuspended` (`:3797-3799`) and then
`transitionResume(row)` (`:3800`). It never consults the registry, the coordination lease, or any
binding. The row moves globally to `running`, an audit row is written (`:3809-3819`), and a status change
is published (`:3820`). The following `executor.Send` (`messages.go:610`) fails, the payload is buffered
(`:613-620`), and the client receives 200 with `queued`. The classifier's own doc describes the caller as
the coordinating replica (`messagerouting.go:130-135`), an assumption the caller does not enforce.

### 5.4 Interaction resolution is the one genuinely cross-replica path

`resolveInteraction` (`pkg/gateway/sessionserver/interactions.go:84`) writes to the shared interaction store
(`interactions.go:116-127`), which is Postgres-backed in production
(`cmd/lenny-gateway/stores.go:2349-2351`). The local waiter delivery at `interactions.go:150-155` misses
off-holder and is ignored. The blocked executor on the holder still wakes, because `AwaitApproval` runs a
25-millisecond store poll alongside the channel (`toolapproval.go:27`, `:138-139`, `:158-167`,
`pollResolution` at `:180-204`), and a dismissed phase fails closed as a denial (`:193-200`). With no
Postgres the store falls back to an in-memory implementation and the cross-replica wake disappears; that
is the development configuration.

This is the standard the `inputwait` half of the same blocking-interaction model does not meet.

### 5.5 What the terminal paths silently skip off-holder

**The final workspace seal is skipped and recorded as a success.** `recordSessionCompleted`
(`pkg/gateway/sessionserver/usage.go:375`) calls `sealWorkspace` (`:394-398`), which calls the
checkpointer's `Seal` from `pkg/gateway/sessionserver/seal.go:118`. `Seal` swallows `ErrNoBinding` and
returns nil (`pkg/gateway/checkpoint/checkpointer/checkpointer.go:397-403`), and `snapshot` returns
`ErrNoBinding` on a registry miss (`:424-427`). Back in `pkg/gateway/sessionserver/seal.go:119-121` the
nil return triggers
`observeWorkspaceSeal(sess, start, sealOutcomeSuccess)`. The seal metric records a success for a seal
that never ran, no timeout relabel fires, and the session settles terminal with a stale checkpoint or
none.

**The pod is not released through the executor.** `releaseExecutor` (`usage.go:450-451`) calls
`PodExecutor.Release`, whose first substantive statement is
`bind, ok := e.registry.Remove(sessionID); if !ok { return nil }` (`pod.go:325-330`). No
`binder.Release`, no `binder.ReleaseSlot`, no recycle disposition, and no slot decrement.
`terminalReclaimPreRunning` does not cover it, returning false at `usage.go:586-588` because the prior
state is `running`; `usage.go:441-447` documents that gating on the absence of a local binding was
deliberately rejected. The backstop is the orphan-claim garbage collector in the controller, which
classifies an aged bound claim as a drain (`pkg/controller/warmpool/gc.go:274-283`) once
`PodHasActiveSession` is false (`:231-239`), with a five-minute orphan timeout (`:33`) and a 60-second
interval (`:51`). The pod is force-drained with no graceful runtime shutdown and no eviction checkpoint.

**The true holder leaks its binding, stream cache, and lease.** The sweep's first branch short-circuits
on any terminal row (`coordination.go:293-299`): it releases the mirror row and continues, before the
dead-connection eviction at `:332-338` and before any acquire at `:357`. So the holder never runs
`evictBinding` and never runs `leases.Release` for a session a peer terminated. Its registry entry and
its cached Attach stream are never removed, and the Redis lease merely lapses on its TTL.

### 5.6 SSE live tail off-holder

`Bus.subscribe` (`pkg/gateway/session/sessionevents/events.go:393-427`) succeeds on an off-holder
replica: the tenant guard at `:398-403` rejects only a mismatched registered owner, and a replica that
never published the session has no entry to mismatch. The backlog is filled cross-replica through
`relay.History` (`pkg/gateway/session/sessionevents/events.go:420-425`, declared at
`pkg/gateway/session/sessionevents/redisrelay.go:127` with the Redis `XRANGE` call at `:131`). The live
loop then reads only `sub.Events()` (`pkg/gateway/sessionserver/events.go:168-180`, `logs.go:174-185`), which is fed
exclusively by local publishes.

The relay's live reader exists and has zero callers:
`grep -rn "LiveFromCursor" --include=*.go .` returns only its doc comment at `redisrelay.go:148` and its
definition at `:157`. **UNWIRED.** A client attached to an off-holder replica receives everything up to
the moment it connected and then nothing, and because neither loop has a keep-alive ticker the stream is
indistinguishable from an idle session. The JSON forms of the same endpoints are unaffected.

A second-order effect: every event a degraded handler publishes, such as the false status change from
`interrupt.go:99` or the `session.resumed` from `start.go:3820`, does reach Redis through the publish
fan-out (`events.go:274`), so peers see the misleading events on their next reconnect.

### 5.7 Delegation-tree recovery uses a per-replica liveness oracle

`POST /v1/sessions/{id}/resume` fires `recoverDelegationTree` at `start.go:3496`. The traversal's
recoverability predicate is wired at `sessionserver.go:1986` to `nodeNeedsRecovery`
(`treerecovery.go:72-77`), which returns `!live` from a bare `s.podRegistry.Get(node.ID)`. A descendant
coordinated by a peer replica reads as not live and is treated as an orphan needing reattach: resumed
onto a fresh pod, or marked terminal by `FailNode` (`treerecovery.go:55-62`). The traversal runs in a
detached goroutine (`:93-97`), so nothing surfaces to the caller and the resume returns 200.

---

## 6. Gaps

### 6a. Unwired or dead paths

#### 6a.1 The gRPC control stream has no gateway client, so every adapter-to-gateway event is dropped

**Status: server WIRED, client UNWIRED.** Evidence and consequences are in section 3.7.

Concrete example. A direct-mode credential lease expires. The adapter's expiry timer fires, deletes that
provider's entry from `/run/lenny/credentials.json`, and calls `EmitAuthExpired`
(`pkg/adapter/credexpiry.go:157-183`, emit at `:182`). `emitControlEvent` finds a nil sink, increments
`lenny_adapter_control_events_dropped_total{reason="no_stream"}`, and returns
(`controlchannel.go:174-176`). The gateway is never told, so the credential fallback flow does not fire
for a direct-mode expiry. The pod's own comment at `pkg/adapter/credexpiry.go:20-22` says the adapter
"reports AUTH_EXPIRED on the control channel, triggering the standard fallback flow"; the report is
dropped.

The adapter also serves no metrics endpoint, so the dropped counter is not observable from outside the
pod: `grep -rn "promhttp\|ListenAndServe" cmd/lenny-adapter/ pkg/adapter/ | grep -v _test.go` returns
nothing.

#### 6a.2 Coordinator-loss hold state is enforced, and nothing in production can arm it

**Status: enforcement WIRED, arming unreachable.**

Both interceptors are chained into the production server at `pkg/adapter/transport.go:46-47`, so every
inbound RPC consults `inHoldState()` (`holdstate.go:245-263`) and a non-allowlisted method would get
`codes.Unavailable` with a `coordinator_hold:` prefix (`:267-270`). The only writer of `s.hold.active`
is `enterHoldState` (`holdstate.go:105`), whose only caller is `onCoordinatorChannelClosed`
(`:91`, `:96`), whose only caller is the deferred close inside the gRPC control-stream handler
(`controlchannel.go:118-128`, specifically `:125`).

```
$ grep -rn "onCoordinatorChannelClosed\|enterHoldState" --include=*.go .
pkg/adapter/controlchannel.go:125     inside the LifecycleChannel handler's defer
pkg/adapter/holdstate.go:91,96,105    definition plus its own call
pkg/adapter/holdstate_test.go:...     tests only
```

Since no production gateway opens that stream, the defer never runs and both interceptors are permanent
no-ops. Everything downstream is unreachable: the 120-second timer (`holdstate.go:122`, default `:23`),
`onHoldTimeout` (`:154-183`), the only production `EmitAdapterTerminating` call (`:174`), the
best-effort runtime close (`:178-182`), and the disk post-mortem (`:198-224`). The post-mortem is doubly
dead: `s.PostMortemDir` comes from `--post-mortem-dir`, defaulting to an environment variable
(`cmd/lenny-adapter/main.go:180-182`), and the podspec sets exactly one environment variable on the
adapter container (`podspec.go:598`) and emits no such flag on either argument list.

Even if the stream were opened, `onCoordinatorChannelClosed` returns early when the pod-global session
id is empty (`holdstate.go:92-95`), which is always the case on a concurrent pod.

Concrete example. A gateway replica is killed while coordinating a session. The pod's channel to it goes
to `TransientFailure`. The pod keeps serving every RPC to any caller indefinitely, never self-terminates,
and never writes a post-mortem. Since no pod-side peer-identity check exists either (section 3.2), a
stale replica that still holds a live connection can keep driving that pod after the coordination lease
has moved away. The only recovery is the 60-second orphan reconciler marking the session failed, which
drives no checkpoint and preserves nothing.

#### 6a.3 The runtime lifecycle channel is never enabled in a chart-rendered pod

**Status: WIRED inside the adapter binary, UNWIRED at the deployment boundary.** Evidence is in
section 3.9.

Concrete example. A Runtime resource declares `integrationLevel: full`. The pod starts, the runtime
reads the adapter manifest, finds no `lifecycleChannel` key (omitted because `s.Lifecycle` is nil,
`manifest.go:271-273` with `omitempty` at `:157`), and correctly never dials. The gateway then probes
`GetObservedIntegrationLevel`, the adapter cannot return `full`
(`pkg/adapter/integrationlevel.go:47-57`), and the bind is rejected `RUNTIME_LEVEL_UNDERPERFORMS`
(`pkg/gateway/podlifecycle/podsession/integrationlevel.go:102-109`). The rejection is not recorded as
verified, so every subsequent assignment re-rejects.

Downstream, every Full-level behavior is inert in a rendered pod: the cooperative checkpoint quiesce
(`checkpoint.go:159`), mid-session credential rebind (`credentials.go:128`), interrupt acknowledgement
(`lifecycle.go:51`), the deadline signal (`lifecycle.go:80`), the terminate frame
(`session.go:296-298`), the files-updated signal (`staging.go:264`), and the direct-mode in-flight token
counter. The last one has a further consequence: `ReportUsage` no longer returns unimplemented because
`WireDirectModeUsage` is called unconditionally (`cmd/lenny-adapter/main.go:394`), but the only feed into
the meter is the runtime lifecycle channel's `llm_request_completed` frame
(`pkg/adapter/usage.go:248`, handler `lifecyclechannel.go:405`), so every direct-mode usage pull returns
zero.

#### 6a.4 The whole agent-pod eviction chain

**Status: producer UNWIRED, transport UNWIRED, consumer ABSENT, routing UNWIRED, at-bind seed ABSENT.**
The full table and the trace are in section 4.8.

Concrete example. A node is cordoned and drained during a cluster upgrade. A pod with a two-hour-old
session and a workspace last checkpointed nine minutes ago is evicted. The runtime container is
SIGTERMed with no preStop. The adapter's preStop signals PID 1 and polls. The adapter runs a no-op SDK
demotion, closes a nil channel, and stops its gRPC server. No event leaves the pod. Fifteen seconds later the
coordination sweep drops the binding and the lease without a checkpoint. Sixty seconds later the orphan
reconciler marks the session failed. Nine minutes of agent work are gone.

#### 6a.5 Other implemented-but-uncalled paths

| Path | Status | Proof |
|:--|:--|:--|
| `Adapter/SendMessage` | UNWIRED | handler `pkg/adapter/session.go:175`, wrapper `adapterclient/client.go:426`; the only invoker of the wrapper is `client_test.go`. Content delivery uses `Attach` (`pod.go:154`). |
| `Adapter/RevokeCredentials` | UNWIRED | handler `credentials.go:326` and slot variant `slotcreds.go:113`; `grep -rn "RevokeCredentials" pkg/gateway/runtime/adapterclient/` returns nothing, so no client method exists. The token-service RPC of the same name (`credassign/client.go:297-310`) is a different service. |
| `Adapter/Interrupt` hard mode | UNWIRED | the single call site passes the literal `false` (`interrupt.go:134`) |
| `Adapter/Terminate` on the session paths | UNWIRED | client method at `client.go:927`; only production caller is `cmd/lenny-gateway/user_revocation.go:129` |
| `Server.PreConnect` in the production sidecar | UNWIRED | no `PreConnect` reference in `cmd/lenny-adapter`; the only invoker is the demo runtime `cmd/runtimes/preconnect-echo/main.go:142` |
| Redis hot routing cache | UNWIRED | zero callers outside its own package |
| `RedisRelay.LiveFromCursor` | UNWIRED | definition and doc comment only |
| `coordlease.GetBySession` | UNWIRED | definitions and tests only |
| `Options.InterReplicaAddress` | UNWIRED | never set in production (`cmd/lenny-gateway/stores.go:1490-1497`) |
| `maxSuspendedPodHoldSeconds` | UNWIRED knob | flag at `cmd/lenny-gateway/flags.go:1060`, threaded at `controlserver.go:229`, and no sweep reads it |
| Embedded SIGSTOP and SIGCONT checkpoint | UNWIRED | `grep -rn "embeddedcheckpoint" --include=*.go pkg cmd` outside its own package returns nothing |
| `Checkpointer.JitterFraction` and `FirstCheckpointDelay` | UNWIRED | set in production and never read; the helper has only test callers |
| `Checkpointer.Deadline` and `checkpoint.CheckpointTimeout` | UNWIRED | never set (`stores.go:2157-2203`); the 60-second constant has no production consumer |
| Service-account token credential on `GatewayControl` | UNWIRED | `WithSAToken` has only a definition and a test; the podspec mounts the token and nothing reads it |
| LLM-proxy SPIFFE lease binding | UNWIRED | `PodSpiffeURI` is never set by either production `BindRequest` constructor (`start.go:2467-2500`, `:2508-2540`), and the listener is plaintext so `r.TLS` is always nil |
| OTLP trace export from the adapter | UNWIRED | `cmd/lenny-adapter/main.go:208` reads an environment variable the podspec never sets (`podspec.go:598`) |
| `git-credential-lenny` in-pod helper | UNWIRED | the gateway half is wired (`mcptools.go:829-856`, `mcpsurface.go:343-352`); the binary is never built into an image |
| Egress-capture sidecar | WIRED, off by default | chart default is empty; the only non-empty value is a test fixture |

Several of these read as live safety controls. Credential revocation to a direct-mode pod has no
in-pod delivery path at all: the gateway can revoke the lease upstream at the Token Service
(`pkg/gateway/credentials/credassign/client.go:309`), which stops future minting, and it cannot tell the
pod to drop the credential material already written into `/run/lenny/credentials.json`. The only other
compensating control is the gateway-side proxy deny list, which does not cover direct mode. The SPIFFE lease binding never fires, so per-request proxy authentication is the
bearer token plus the NetworkPolicy, while the admission webhook rejects `spiffeBinding: disabled` on
the rationale that it guards cross-pod replay
(`pkg/admission/direct_mode_isolation/guard.go:149-154`, `:25-27`).

#### 6a.6 Dead branches gated on flags nothing sets

The best-effort eviction snapshot inside `Checkpoint` is the clearest case:

```
pkg/adapter/checkpoint.go:179   if s.isEvicting() && !s.Lifecycle.RuntimeConnected() { ... }
pkg/adapter/checkpoint.go:192   if s.isEvicting() && lifecycleConnectionDropped(rerr) { ... }
```

`isEvicting()` reads an atomic flag (`session.go:395-397`) that only `setEvicting` writes
(`session.go:385-387`), and `setEvicting` has no production caller. Both branches are unreachable, so a
checkpoint against a pod whose runtime connection dropped always fails closed with
`Internal "checkpoint quiesce handshake: ..."` (`checkpoint.go:196`). The branch is doubly unreachable,
because `s.Lifecycle` is nil in a rendered pod.

#### 6a.7 A `preConnect: true` pool cannot start a session against the shipped podspec

**Status: WIRED gateway-side, dead at the deployment boundary.**

The gateway half is reachable and data-driven. `runtimeSDKWarm` reads
`Capabilities.PreConnect` off the tenant-resolved Runtime custom resource
(`pkg/gateway/sessionserver/envelope.go:274-286`; the equivalent predicate is
`poolstore.RuntimePreConnect` at `pkg/gateway/runtime/poolstore/poolstore.go:700`), is called at
`start.go:2466`, and lands on `BindRequest.PreConnect` at `:2482`. `Binder.Launch` then takes the
`ConfigureWorkspace` branch whenever `req.PreConnect && !req.Demoted` (`binder.go:991-995`).

The pod half returns `codes.Unimplemented` for both SDK-warm RPCs in every chart-rendered pod, for the
reasons in section 3.2. There is no fallback on the Launch leg: `binder.go:993-994` calls `reclaim()`
and returns `podsession: configure SDK-warm workspace on pod %s: %w`, so the bind fails and the pod is
released. On the Prepare leg, when the workspace plan matches the runtime's `sdkWarmBlockingPaths`
(`binder.go:862-863`), the same `Unimplemented` maps to `SDKDemotionNotSupported`
(`binder.go:865-872`), which also fails the session.

The handler's own doc comment at `pkg/adapter/sdkwarm.go:197-198` states that a `ConfigureWorkspace`
error causes "the gateway [to run] the DemoteSDK fallback". The gateway does not; and `DemoteSDK` would
return `Unimplemented` for the same reason if it did.

### 6b. Specification versus implementation

Each entry states what the specification says, what the code does, and the classification.

#### 6b.1 Generation validation on every gateway-to-pod RPC

`spec/10_gateway-internals.md:28` states that pods validate the coordination generation on every
gateway-to-pod RPC, that a stale generation is rejected, and that this prevents split-brain even under
lease race conditions. `spec/04_system-components.md:657` restates `CoordinatorFence` as a precondition
for any subsequent operational RPC.

The wire contract carries `coordination_generation` on exactly two messages,
`CoordinatorFenceRequest` (`schemas/lenny-adapter.proto:1338`) and `CheckpointBarrierRequest`
(`:1366`). No other gateway-to-pod request message has the field, so no other RPC can be validated. On
the adapter, `lastFenced` is read only inside `CoordinatorFence` (`coordination.go:100-107`) and
`CheckpointBarrier` (`:233-240`), plus an accessor and a log line. Everything else is gated by session-id
equality and the hold-state interceptor, which keys on hold state rather than on a generation. The
adapter's own comment concedes that the quiesce check is advisory (`coordination.go:35-37`).

**Classification: specification ahead of code, at the proto level.** Closing it requires a field on every
operational request message. The split-brain protection the specification asserts does not exist at the
pod, and cross-replica exclusivity rests entirely on the gateway-side Redis lease.

#### 6b.2 The agent pod's preStop hook

`spec/04_system-components.md:489` states that the primary protection against voluntary disruption is a
preStop hook on every agent pod that triggers a checkpoint through the adapter's `Checkpoint` RPC before
allowing termination. The rendered hook contains no checkpoint logic
(`cmd/lenny-adapter/prestop.go:68-94`), the runtime container has no hook at all
(`podspec.go:609-620`), and the podspec's own comment repeats the specification's claim
(`podspec.go:602-607`).

**Classification: genuine conflict.** The whole coordinator-direct eviction design rests on a mechanism
that does not exist.

#### 6b.3 `AdapterEvicting` has no producer

`spec/04_system-components.md:489` and `:689` describe the adapter emitting `AdapterEvicting` on
kubelet-driven termination. The primitives exist and have no production caller (section 4.8).

**Classification: specification ahead of code, producer absent.**

#### 6b.4 Adapter-to-gateway events over the gRPC control stream

`spec/04_system-components.md:679-690` presents the event table as a live surface. The server half is
wired and the client half does not exist (section 3.7). Downstream specification statements that depend
on it include `spec/13_security-model.md:684`, which says the gateway rotates a credential on
`RATE_LIMITED`, and `spec/08_recursive-delegation.md:425`, which says the gateway waits for
`FINAL_USAGE_REPORT` before returning budget.

**Classification: specification ahead of code, transport unwired on the client side.**

#### 6b.5 Hold-state entry condition

`spec/10_gateway-internals.md:47` makes hold-state entry a consequence of gRPC transport-layer detection
on the gateway-to-pod channel within roughly fifteen seconds. The code makes entry conditional on the
close of an application-level stream that no production gateway opens (section 6a.2). Nothing in the
adapter hooks the transport: not the Attach handler, and not the keepalive parameters
(`cmd/lenny-adapter/main.go:250-262`).

**Classification: genuine conflict on the entry condition,** compounded by the undeliverable terminal
`AdapterTerminating`.

A downstream consequence worth recording: proposal 0060's crash-takeover fence-first ordering is
justified by the claim that a crash-takeover pod is already in hold state and rejects everything except
`CoordinatorFence`. Against the current build the pod is not in hold state, so that ordering is
defensive rather than load-bearing.

#### 6b.6 The inter-replica control-forward path and `coordinator_address`

`spec/04_system-components.md:489` requires a general coordinator-forward transport keyed on the
session's coordinator, dialing the coordinator's recorded inter-replica address, of which the eviction
drive is the first consumer. `spec/10_gateway-internals.md:167` requires the mirror row to carry that
address.

Four layers, each short of the specification. The column and the store methods are wired
(`migrations/0180_coordination_lease_address.up.sql:19`, `coordlease.go:52-57`,
`pkg/gateway/coordination/coordlease/pgstore/pgstore.go:50-59` and `:109-112`). The routing read `GetBySession` has no production caller. The address value is never
populated, so every production row carries an empty address. And no gateway-to-gateway service exists,
nor any NetworkPolicy permitting the flow (section 3.17).

**Classification: specification ahead of code.**

#### 6b.7 Bind-time mirror seeding

`spec/10_gateway-internals.md:167` and `spec/04_system-components.md:489` both state that the session
server seeds the mirror row at bind with the binding replica as the initial coordinator, on both the
session-start and snapshot-resume rebind paths, so that a session evicted before its first sweep is
still resolvable and routable. No such seeding exists: the only writer is the sweeper
(`coordination.go:569`), and `pkg/gateway/sessionserver/` never references `coordlease`.

**Classification: specification ahead of code.** The pre-first-sweep window the specification says it
closes stays open, and it also affects the barrier-target set, which reads the same table.

#### 6b.8 The barrier acknowledgement surface

`spec/04_system-components.md:656` and `:687` place `CheckpointBarrierAck` on the gRPC control stream, and
`spec/10_gateway-internals.md:167` restates it. The implementation emits the acknowledgement twice, and
only the copy the specification does not define is consumed: the unary `CheckpointBarrierResponse`
(`schemas/lenny-adapter.proto:177`, `:1382-1386`) built at `coordination.go:272-276` and returned at
`:284`, consumed by the gateway at `adapterclient/client.go:546` and
`pkg/gateway/coordination/barrier/barrier.go:228`. The control-stream emit at `coordination.go:282`
always drops. The proto comment at `schemas/lenny-adapter.proto:170-172` asserts the inverse of reality,
calling the control-stream emit the canonical surface for a barrier-target reconciler that does not
exist.

**Classification: code ahead of specification.** A third-party adapter written against the normative
prose would emit the acknowledgement only on the gRPC control stream, and the gateway would hang until the
barrier acknowledgement timeout.

#### 6b.9 Slot-aware control RPCs

`spec/04_system-components.md:677` requires per-slot checkpoint admission and promotion on a concurrent
pod, `spec/10_gateway-internals.md:143` scopes the partial manifest on the session and slot, `:153`
requires at most one partial upload per slot in flight, and `spec/15_external-api-surface.md:1478`
requires a slot identifier on every JSONL message on such a pod.

The operation lock does implement per-slot promotion (`oplock.go:74`, `:119`, `:142`). The defect is at
the RPC surface: `InterruptRequest`, `SignalDeadlineRequest`, `ReportUsageRequest`, and
`CheckpointBarrierRequest` carry no slot identifier, and their handlers gate on the pod-global session
id that the slot-start path never sets (section 4.9).

**Classification: genuine conflict.**

#### 6b.10 `ForwardMessage` and cross-replica message routing

`spec/07_session-lifecycle.md:330` states that a `delivery: immediate` message landing on a
non-coordinator replica is forwarded to the coordinator, and that an unreachable coordinator degrades to
inbox buffering with a `queued` receipt so the message is not silently dropped. Neither branch exists
(section 5.1), and the inbox never redelivers (section 4.2).

**Classification: specification ahead of code.** It is a known deferral tracked in
`proposals/0060_…:121`, and the specification prose reads as present-tense behavior.

#### 6b.11 The `ready_for_input` availability signal

`spec/07_session-lifecycle.md:320` defines a runtime as available when its adapter reports
`ready_for_input`, and defines the `delivered` receipt status as confirmed stdin consumption within a
configurable 30-second delivery timeout. Neither the signal nor the timeout exists. Every non-test hit
in the Go tree is a prose comment (`pkg/gateway/sessionserver/messages.go:514`, `:522`, `:587`, `:608`;
`pkg/gateway/sessionserver/start.go:3792`;
`pkg/gateway/session/messagerouting/messagerouting.go:44`, `:49`, `:141`), and the string appears in
neither JSON schema.

**Classification: specification ahead of code.** The `delivered` status is produced on a weaker
condition than the specification defines.

#### 6b.12 Part B, the runtime lifecycle channel, in a rendered pod

`spec/04_system-components.md:705` defines Part B as a JSON Lines stream over an abstract Unix socket
advertised in the manifest. The channel is implemented and never enabled (sections 3.9 and 6a.3).

**Classification: specification ahead of code at the deployment layer.** The cooperative handshake that
`spec/04_system-components.md:241` calls the only mechanism producing consistent checkpoints under all
isolation profiles is unreachable in a rendered pod, and Full-level conformance is not exercisable
against the shipped podspec.

#### 6b.13 The best-effort eviction snapshot

`spec/04_system-components.md:241` requires the adapter to capture a best-effort snapshot of the
still-mounted workspace when a checkpoint runs while the pod is itself terminating and the cooperative
handshake cannot complete. The branch exists and is gated on a flag with no production setter
(section 6a.6).

**Classification: specification ahead of code.**

#### 6b.14 Where the runtime lifecycle channel schema lives

`spec/15_external-api-surface.md:1462-1463` frames the adapter contract as three machine-readable
artifacts and states that `schemas/lenny-adapter-jsonl.schema.json` covers the stdin and stdout messages
and every runtime lifecycle channel message. That file's definitions are the message-plane types only.
The runtime lifecycle channel messages live in `schemas/lifecycle-events.schema.json`, a fourth artifact §15.4 never
names; it is named only at `spec/18_build-sequence.md:165`.

The shipped JSONL schema also contains a wrong description. Its own text at
`schemas/lenny-adapter-jsonl.schema.json:5` calls the checkpoint and credential frames a
"gateway↔adapter lifecycle channel" riding "the gRPC LifecycleChannel stream". Both halves are wrong:
those frames are adapter-to-runtime and they ride the Unix socket. This is the naming conflation of
section 1.4 baked into a shipped artifact.

**Classification: code ahead of specification, plus an internal specification inconsistency.**

#### 6b.15 Field-level schema divergences on the runtime lifecycle channel

`schemas/lifecycle-events.schema.json` is stricter than the emitter, and nothing validates emitted bytes
against it. Every frame shares one Go struct in which each field is `omitempty`
(`lifecyclechannel.go:59-83`), so a zero value is omitted where the schema requires it.

| Divergence | Schema | Emitter |
|:--|:--|:--|
| `deadline_approaching.remainingMs` required with a minimum of 0 (`:158`, `:161`) | required | omitted when zero (`lifecyclechannel.go:66`), and the RPC forwards the value unchecked (`lifecycle.go:89`) |
| `checkpoint_request.deadlineMs` required with a minimum of 100 (`:75`, `:77`) | required | omitted when zero (`:65`) |
| `interrupt_request.deadlineMs` required with a minimum of 100 (`:111`, `:113`) | required | omitted when zero |
| `terminate.deadlineMs` required with a minimum of 100 (`:180`, `:183`) | required | omitted when zero |
| `deadline_approaching.trigger` is a closed enum (`:159`) | enum | a free string, only defaulted (`lifecycle.go:86-88`) |
| `credentials_acknowledged.provider` required (`:149`) | required | the adapter reads only the lease id (`:392`) |
| `llm_request_*` require a request id, and completion requires a status (`:195`, `:210`) | required | the adapter reads neither (`:393-396`) |

Tier 0 validates only the static example fixtures
(`tests/tier0_static/schemas_test.go:146-172`), and three schema members have no example fixture at all:
`credentials_acknowledged`, `llm_request_started`, and `files_updated`.

### 6c. Missing capabilities

#### 6c.1 No coordinator gate on the session REST surface

Of the session-scoped mutating routes registered at `sessionserver.go:2038-2104`, exactly one tests
whether this replica holds the binding before acting: `upload-to-session`
(`upload_to_session.go:113-124`), which returns 409 `TARGET_NOT_READY`. Every other handler either acts
on the row alone or discovers the miss deep inside a helper. The coordination lease is acquired at four
bind sites and never consulted by an operational endpoint (section 5.1). This is the root cause of the
entire off-holder matrix in section 5.2.

#### 6c.2 No forwarding, redirect, or affinity to reach the coordinator

Covered in section 5.1. The absence has three layers: no `ForwardMessage` RPC, no gateway-to-gateway
transport of any kind to carry one, and no NetworkPolicy that would permit the flow.

Concrete example. Behind a round-robin Service with N gateway replicas, roughly (N-1)/N of
`POST /v1/sessions/{id}/messages` for a running session return HTTP 500. The same ratio applies to
`POST /v1/chat/completions`, `POST /v1/responses`, and the MCP tool surface, which reach the same
registry miss with a different error envelope (section 5.2). On a concurrent-session pod the
fan-out of one pod's sessions across replicas makes the miss structural rather than incidental.

#### 6c.3 The session inbox is write-only

There is no redelivery path. The only two callers of `inbox.Drain` are `MigrateInboxToDLQ`
(`pkg/gateway/session/sessioninbox/coordinator.go:121`) and `DrainOnTerminal` (`:201`, which emits
`message_expired` with reason `target_terminated`). A grep for `redeliver` outside tests returns only
comments. Every 200 response carrying a `queued` receipt is therefore a terminal drop rather than a
deferral, which compounds each off-holder path that falls back to inbox buffering.

#### 6c.4 `inputwait` has no cross-replica fallback

Covered in sections 5.3 and 5.4. The two halves of the same blocking-interaction model were built to
different standards: tool approval polls a shared Postgres-backed store every 25 milliseconds precisely
so a resolution landing on a non-coordinator wakes the blocked executor, and the request-input half has
no shared store at all.

#### 6c.5 The coordinator cannot reach a pod it does not already hold a binding for

`spec/04_system-components.md:489` requires the coordinator to drive against a connection dialed to the
pod when it does not already hold the session's binding. No such capability exists on any eviction or
checkpoint path: the barrier dispatcher is connection-only by deliberate removal (commit `21032008`,
rationale at `wiring.go:29-31`), and `grep -rni "dial" pkg/gateway/coordination/ --include=*.go` outside
tests returns only comments. The one remaining pod-dial path is `Binder.ReadoptConnect`
(`binder.go:1161` then `:1135`), reached only from the crash-takeover re-adopt
(`coordination_seams.go:219`).

Combined with the absent at-bind mirror seed, a session evicted in the pre-first-sweep window has
neither a resolvable coordinator nor a way for one to reach the pod.

#### 6c.6 Checkpoint restore onto a concurrent-session pod

Covered in section 4.5. A concurrent-session pool can be checkpointed and cannot be restored onto a
concurrent pod.

#### 6c.7 Agent-pod mTLS client identity

The podspec emits no TLS flags and mounts no certificate material on either container, and
`podVolumes()` contains no certificate volume. The chart states the intent explicitly:
`charts/lenny/templates/mtls-pki.yaml:24-27` says per-pod certificates are issued at pod-creation time
and are intentionally absent as static chart resources. No such per-pod producer exists in the tree:
`pkg/controller/warmpool/pod_reconciler.go:41-49` calls the certificate annotation "the forward path for
a per-pod cert producer", and `cmd/lenny-controller/flags.go:197` defaults the corresponding enforcement
to false for the same reason.

Consequence with `mtls.enabled: true`, which is the chart default: the agent pod dials the gateway's
`GatewayControl` listener in plaintext while that listener requires a verified client certificate, so
the handshake cannot complete and every platform-tool, connector-tool, and scrub-report call fails. In
the other direction the adapter's own listener serves plaintext. A latent second defect sits behind the
first: `GatewayDNSName` is hardcoded to the `lenny-system` namespace
(`pkg/adapter/gatewaycontrol/gatewaycontrol.go:36`) while the controller stamps a
namespace-parameterized target, so a release installed elsewhere would pin a SAN the gateway certificate
does not carry. That becomes live the moment agent-pod certificates are wired.

#### 6c.8 Non-Anthropic LLM proxy dialects

`credential.ProxyDialect` admits `anthropic`, `openai`, `google`, and `cursor`
(`pkg/credential/lease.go:58-91`), and the gateway registers exactly one route,
`POST /llm-proxy/v1/messages` (`cmd/lenny-gateway/main.go:475`). A pool minted with any other dialect
produces a 404 from the mux. There is also no pod-side proxy client in this repository; that half is a
contract handed to a runtime the deployer supplies.

#### 6c.9 The proxy dialect is defined by two enums that disagree

Two Go types both enumerate the §4.9 proxy dialect, they hold different value sets, and neither is derived
from the other.

| Type | Values |
|:--|:--|
| `credential.ProxyDialect` (`pkg/credential/lease.go:58-72`) | `anthropic`, `openai`, `google`, `cursor` |
| `llmproxy.Dialect` (`pkg/gateway/llmproxy/llmproxy/translator.go:25-38`) | `anthropic`, `openai`, `openai_responses` |

The first governs admission. `ProxyDialect.IsValid` (`lease.go:83-90`) is what rejects a credential pool
with `INVALID_POOL_PROXY_DIALECT`. The second governs routing, because a `Translator` selects on
`Request.Dialect`. Set arithmetic over the two:

- **Admitted but unroutable: `google` and `cursor`.** A pool carrying either passes admission and can
  never be served. This is 6c.8 restated, with the cause identified as the enum split rather than a
  missing route alone.
- **Routable but unmintable: `openai_responses`.** `OpenAIResponsesTranslator`
  (`pkg/gateway/llmproxy/llmproxy/openai_responses_translator.go`) is fully implemented and has no
  production constructor, because no pool can carry the value that would select it. It is UNWIRED, and
  unreachable by construction rather than by omission.
- **Usable: `anthropic` and `openai`.** The intersection is the real supported set.

A third divergence sits under both. Only `POST /llm-proxy/v1/messages` is registered
(`cmd/lenny-gateway/main.go:475`), which is the `anthropic` dialect, so `openai` is admitted, is present in
the routing enum, and still has no inbound route. The set of dialects that work end to end today is
`anthropic` alone.

§4.9 permits two values and no more. The `proxyDialect` lease field is typed "`openai` | `anthropic`"
(`spec/04_system-components.md:1288`), the proxy-mode delivery step repeats it (`:1488`), and the pool
example comments it (`:1533`). `google` and `cursor` therefore exceed the specification that governs them.
They were introduced by the reference runtime catalog (§26.5, §26.8, and §26.9 for `google`; §26.6 line
297 for `cursor`), and `spec/26:297` states that "Lenny's LLM proxy gains a `cursor` dialect (§4.9)",
which §4.9 does not grant. The catalog contradicts §4.9, and the code follows the catalog.

The citation on `ProxyDialect` itself is also stale. `lease.go:53` cites "§4.9 lines 1473-1476" for the
dialect rules, and those lines are the rotation-mode resolution prose, which says nothing about dialects. Because the dialect is the **inbound**
wire format a runtime's SDK speaks to the proxy (`translator.go:22-23`), and the lease token is handed to
that SDK in place of its API key (`spec/04_system-components.md:1289`), every runtime whose SDK speaks a
new format implies a new dialect. The proxy's inbound surface is therefore a function of the runtime
catalog, which is an open-ended surface on the component that holds tenant credentials. §26.6 line 303
states the consequence plainly for `cursor`: the API is proprietary, the dialect implements a public
subset, and operators must pin runtime and proxy versions together against drift.

#### 6c.10 Open Responses and the OpenAI Responses API are distinct, and the names are used interchangeably

Three concepts share overlapping names across two unrelated layers.

| Concept | Layer | Direction | Artifact |
|:--|:--|:--|:--|
| Open Responses Specification | External API surface | client to gateway | `OpenResponsesAdapter`, `POST /v1/responses` (`spec/15_external-api-surface.md:577`) |
| OpenAI Responses API | External API surface | client to gateway | served by the same adapter, since §15.1 records it as a proper superset (`spec/15:581`) |
| OpenAI Responses API | LLM proxy | runtime SDK to proxy | `llmproxy.DialectOpenAIResponses` (`translator.go:33-37`) |

The first two are one adapter by deliberate design, and `spec/15:581` states the superset relationship
that justifies it. The third is a different layer entirely, travelling the opposite direction, and shares
only a name.

The naming is not held consistently. `spec/18_build-sequence.md:271` writes "OpenAI Open Responses",
merging the open specification and OpenAI's API into one name that denotes neither. Elsewhere the spec
alternates between "Open Responses" (`spec/03:7`, `spec/04:47`, `spec/09:40`, `spec/15:567`,
`spec/23:28`) and "OpenAI Responses API" (`spec/26:281`) without stating which sense is meant at each
site, and the two differ by three characters.

The practical risk is on the proxy side. A reader who takes `openai_responses` for the external adapter
concludes the Responses surface is served, when the proxy dialect it names is unreachable per 6c.9.

---

## 7. Quick reference

### 7.1 Status of every mechanism named in this document

| Mechanism | Status | Anchor citation |
|:--|:--|:--|
| `Adapter` gRPC service, gateway to pod | WIRED | `pkg/adapter/transport.go:50`, client `cmd/lenny-gateway/stores.go:2097` |
| `grpc.health.v1.Health` service on the pod | server WIRED, client ABSENT | `pkg/adapter/transport.go:52-54`; no health client anywhere in the gateway or the controller |
| `Attach` content stream | WIRED | `pkg/gateway/session/executor/pod.go:154`, handler `pkg/adapter/attach.go:28` |
| Single-consumer enforcement on `Attach` at the pod | ABSENT | no registry or counter anywhere in `pkg/adapter/attach.go` |
| `Checkpoint` stream and the pod operation lock | WIRED | `pkg/gateway/checkpoint/checkpointer/checkpointer.go:522`, `pkg/adapter/oplock.go:74` |
| `CoordinatorFence` | WIRED | `pkg/gateway/coordination/coordfence/coordfence.go:160`, handler `pkg/adapter/coordination.go:85` |
| In-flight RPC cancellation on a generation gap | ABSENT | conceded at `pkg/adapter/coordination.go:81-82` |
| `CheckpointBarrier` | WIRED | `pkg/gateway/coordination/barrier/wiring.go:49`, handler `pkg/adapter/coordination.go:212` |
| Quiesce enforcement against operational RPCs | ABSENT | `coord.quiesced` has no reader in any handler |
| gRPC control stream `Adapter/LifecycleChannel` | server WIRED, client UNWIRED | handler `pkg/adapter/controlchannel.go:108`; one comment in `pkg/gateway` |
| Coordinator-loss hold state | enforcement WIRED, arming unreachable | interceptors `pkg/adapter/transport.go:46-47`; sole arm `pkg/adapter/controlchannel.go:125` |
| `GatewayControl` service | WIRED | `pkg/adapter/gatewaylink.go:36-79`, handlers under `leasecontrol/` |
| Service-account token credential on `GatewayControl` | UNWIRED | `gatewaycontrol/satoken.go:64` |
| Agent-pod mTLS client identity | ABSENT | no TLS flags in `podspec.go` |
| Runtime message socket | WIRED | `pkg/adapter/socketruntime.go:60`, `cmd/lenny-adapter/main.go:354` |
| Runtime lifecycle channel | WIRED in the binary, UNWIRED at deployment | `cmd/lenny-adapter/main.go:373`; no `--lifecycle-socket` setter |
| Platform and connector MCP sockets | WIRED | `pkg/adapter/platformmcp.go:16-51`, `connectormcp.go:59-92` |
| LLM proxy | gateway WIRED, pod client ABSENT, chart-gated off | `cmd/lenny-gateway/main.go:475`, `charts/lenny/values.yaml:1695` |
| Proxy SPIFFE lease binding | UNWIRED | `pkg/credential/lease.go:335`; `PodSpiffeURI` never set |
| Object-store chunk upload and restore | WIRED | `pkg/adapter/checkpointtransport.go:76-117` |
| Redis coordination lease | WIRED | `leasestore.go:98-105`, acquired at four bind sites |
| Postgres `coordination_lease` mirror write | WIRED, sweeper only | `pkg/gateway/coordination/coordination/coordination.go:569` |
| Mirror seed at bind | ABSENT | no `coordlease` reference in `pkg/gateway/sessionserver/` |
| `coordinator_address` population | UNWIRED | `InterReplicaAddress` never set |
| `coordlease.GetBySession` routing read | UNWIRED | definitions and tests only |
| Barrier target set from the mirror, with cache fallback | WIRED | `barrier/wiring.go:97-124` |
| Barrier fresh-dial fallback | retired by design | commit `21032008`, `wiring.go:29-31` |
| Coordination sweeper and crash-takeover re-adopt | WIRED | `cmd/lenny-gateway/workers.go:1298-1300`, `coordination_seams.go:199-251` |
| Dead-connection binding eviction | WIRED, drives no checkpoint | `pkg/gateway/coordination/coordination/coordination.go:332-338` |
| Gateway preStop drain, barrier plus tiered per-session loop | WIRED | `cmd/lenny-gateway/httpsurface.go:642-643`, `pkg/gateway/podlifecycle/prestop/prestop.go:390-451` |
| Lease or mirror release on gateway drain | ABSENT | `cmd/lenny-gateway/runserver.go:213-252` closes stores only |
| Agent-pod preStop hook | WIRED, signals and polls only | `podspec.go:607`, `cmd/lenny-adapter/prestop.go:68-94` |
| Adapter-to-runtime heartbeat with SIGTERM escalation | WIRED | `pkg/adapter/attach.go:78`, escalation `:141-147`, monitor `pkg/adapter/heartbeat.go:34-45` |
| Kubelet probes on agent-pod containers | ABSENT | no probe field anywhere in `podspec.go` |
| Agent-pod eviction checkpoint | ABSENT | no trigger on that edge |
| Eviction-API admission gate on `pods/eviction` | WIRED, feature-gated off | `charts/lenny/templates/admission-policies/drain-readiness-webhook.yaml:11`, `charts/lenny/values.yaml:1696` |
| `agent_pod_state` mirror, controller write and gateway read | WIRED | `pkg/controller/warmpool/controller.go:248`, read at `pkg/gateway/session/orphansession/orphansession.go:50-55` |
| SDK-warm `ConfigureWorkspace` and `DemoteSDK` at the pod | WIRED gateway-side, dead at the deployment boundary | `pkg/adapter/sdkwarm.go:211-214`, `:267-270`; no `SDKWarmRuntime` in a rendered pod |
| `AdapterEvicting` producer and consumer | UNWIRED producer, ABSENT consumer | `pkg/adapter/controlchannel.go:248`; no hit outside `pkg/adapter` |
| Best-effort eviction snapshot | UNWIRED, doubly | `pkg/adapter/checkpoint.go:179`, `:192` |
| PodDisruptionBudget for a busy pod | ABSENT by design | `pkg/controller/warmpool/pdb.go:70-76` |
| Orphan-session reconciler | WIRED, not leader-gated | `pkg/gateway/session/orphansession/orphansession.go:45`, `:58`, launched ungated at `cmd/lenny-gateway/workers.go:1088` |
| Orphan-claim garbage collector | WIRED | `pkg/controller/warmpool/gc.go:231-283` |
| Cross-replica message forward | ABSENT | no identifier, no RPC, no transport |
| Redis hot routing cache | UNWIRED | zero callers outside its package |
| Session inbox redelivery | ABSENT | no dequeue path |
| `ready_for_input` signal | ABSENT | comments only |
| SSE backlog across replicas | WIRED | `pkg/gateway/session/sessionevents/events.go:420-425`, relay at `cmd/lenny-gateway/stores.go:2270` |
| SSE live tail across replicas | UNWIRED | `pkg/gateway/session/sessionevents/redisrelay.go:157` has no caller |
| Tool-approval resolution across replicas | WIRED | `pkg/gateway/sessionserver/toolapproval.go:158-167` store poll |
| Input-wait resolution across replicas | ABSENT | `pkg/gateway/session/inputwait/inputwait.go:41-49` is process-local |
| Checkpoint restore onto a concurrent pod | ABSENT | no `slot_id` on `ResumeRequest` |
| Slot-qualified interrupt, deadline, usage, and barrier | ABSENT at the wire | no `slot_id` on those request messages |
| Gateway-to-gateway transport | ABSENT | four services declared, none gateway-to-gateway |

### 7.2 Exclusivity at a glance

| Channel or resource | Granularity | Two gateway replicas at once? | Guard |
|:--|:--|:--|:--|
| gRPC connection to a pod adapter | none | **Yes** | none; mTLS accepts any cluster-CA certificate and the podspec supplies none |
| `Attach` stream | per (session, slot) identity check only | **Yes**; the pod fans out or splits frames | gateway-side cache under `pod.go:129-130` |
| `Checkpoint`, same slot | one running per pod, one pending per slot | No; the second attempt coalesces and surfaces as `Aborted` | `pkg/adapter/oplock.go:103-105` then `pkg/adapter/checkpoint.go:114-118` |
| `Checkpoint`, distinct slots | one running per pod, one pending per slot | Both are admitted; the second blocks in the pending queue until promotion, so a second replica is head-of-line blocked rather than rejected | `pkg/adapter/oplock.go:107-113`, `:119-123`, promotion `:173-181` |
| `Interrupt` | whole-pod queue | No | `pkg/adapter/lifecycle.go:38` |
| `CheckpointBarrier` gate | one waiting per pod, unenforced | a second barrier clobbers the first | `pkg/adapter/coordination.go:159-167` |
| `CoordinatorFence` generation | per pod, monotonic | a stale fence is rejected | `pkg/adapter/coordination.go:100-108` |
| gRPC control stream | **per pod**, process-wide | No, by rejection | `pkg/adapter/controlchannel.go:110-115` |
| Runtime lifecycle channel | per adapter process, one connection served at a time | not applicable | `pkg/adapter/lifecyclechannel.go:135`, `:180-198` |
| Runtime message socket | per pod, output fans out to all subscribers | not applicable | `pkg/adapter/socketruntime.go:343-360` |
| `GatewayControl` | none | **Yes**, and the pod reaches an arbitrary replica | none |
| `podsession.Registry` entry | per session, **per replica** | structurally yes; prevented only by the lease | `pkg/gateway/podlifecycle/podsession/registry.go:28`, unconditional overwrite |
| Redis coordination lease | **per (tenant, session)**, never per pod | No | `pkg/gateway/storage/leasestore/leasestore.go:98-105`, `:138-140` |
| Postgres mirror row | per (tenant, session), a projection | not an exclusion primitive | `pkg/gateway/coordination/coordlease/pgstore/pgstore.go:50-59` |
| `SandboxClaim` | **per pod**, first acquisition only | No on acquisition; subsequent slots are not replica-exclusive | `podclaim/claimer.go:347-349` plus the guard webhook |
| Redis slot counter | per pod | atomic across replicas | `pkg/gateway/storage/slotcounter/slotcounter.go:196-205` |
| Adapter pod-global session | per pod | No | two setters, `pkg/adapter/session.go:416-425` and `pkg/adapter/sdkwarm.go:290-301`, both mutex-guarded and both rejecting a different assigned session |
| Adapter slot session | per slot | No | `pkg/adapter/slotsession.go:40-44` |
| Credential session on the pod | per pod, or per slot when a slot id is supplied | No; sticky and never cleared on release | `pkg/adapter/credentials.go:84-87`, `pkg/adapter/session.go:430-438` |
| LLM proxy request | none | **Yes**, unbounded per lease token | none |
| Gateway preStop hook | per gateway process, once | not applicable | atomic compare-and-swap at `pkg/gateway/podlifecycle/prestop/prestop.go:344` |

### 7.3 Where a checkpoint is driven, and where it is not

| Edge | Eviction checkpoint driven? | Citation |
|:--|:--|:--|
| Gateway replica drains, barrier path | Yes | `pkg/gateway/coordination/barrier/barrier.go:225` |
| Gateway replica drains, post-barrier per-session loop | Yes | `pkg/gateway/podlifecycle/prestop/checkpoint_adapter.go:39` |
| Session completes, is cancelled, or expires, on the holder | Seal only, with the periodic trigger | `pkg/gateway/sessionserver/usage.go:395` then `checkpointer.go:397` |
| Session completes, is cancelled, or expires, off-holder | No, and a success metric is recorded | `checkpointer.go:397-403`, `pkg/gateway/sessionserver/seal.go:119-121` |
| Periodic sweep on the holder | Yes, ten-minute cadence with no jitter | `cmd/lenny-gateway/workers.go:1421`, `checkpointer.go:287-298` |
| **Kubelet terminates the agent pod** | **No** | section 4.8 |
| Node drain or cluster upgrade through the Eviction API | **No.** An admission gate exists on this edge and gates artifact-store health rather than session liveness, and the chart disables it by default | `charts/lenny/templates/admission-policies/drain-readiness-webhook.yaml:3`, `:11`, `charts/lenny/values.yaml:1696` |
| Direct pod `DELETE` or kubelet node-pressure eviction | **No**, and no admission gate is possible: neither traverses the `pods/eviction` subresource | section 4.8 |
| Pool scale-down | **No** | `TriggerPreScaleDown` has no drive site |
| Interrupt | No | `handleInterrupt` opens no stream |
| Runtime misses the adapter heartbeat | **No.** The adapter SIGTERMs the runtime and ends the Attach stream with `DeadlineExceeded`; the workspace is lost down to the last periodic checkpoint | `pkg/adapter/attach.go:141-147` |

---

## 8. Items this document does not establish

- No test tier was run. Every claim is a static reading of the working tree at `fcda83e3`.
- Whether any deployment outside this repository sets `--lifecycle-socket`, `--post-mortem-dir`, or
  `OTEL_EXPORTER_OTLP_ENDPOINT` on the adapter through a mutating webhook, an overlay, or an operator
  fork. Within the repository each has zero setters. **UNVERIFIED.**
- Whether `srv.GracefulStop()` in the adapter's SIGTERM handler (`cmd/lenny-adapter/main.go:424`) can
  hang past the grace deadline. A long-lived `Attach` server stream is a pending RPC, `GracefulStop`
  blocks on pending RPCs, and the call has no timeout and no fallback to `Stop()`. Whether the Attach
  handler always returns promptly once the runtime container dies depends on socket-runtime teardown
  that was not read to the bottom. **UNVERIFIED**, and a real risk.
- NetworkPolicy behavior under a live CNI. The statements in sections 3.17 and 6c.7 derive from the
  rendered policy objects plus additive-allow semantics.
- Whether the elicitation waiter in `pkg/gateway/mcpfabric/mcptools` mirrors the tool-approval store
  poll. The doc comment at `toolapproval.go:135-137` says it does; the await loop was not opened.
  **UNVERIFIED.**
- The runtime side of the `checkpoint_ready` contract for third-party Full-level runtimes. The adapter's
  socket protocol was verified; third-party frame handling was not.
- Whether the terminal callback and audit sinks deduplicate a second `recordSessionCompleted` for the
  same session. The enqueue sites were read; each sink's idempotency was not. **UNVERIFIED.**
