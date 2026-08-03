# 28. Communication Channels

## 28.3 Registers

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
