# Forensics and evidence preservation

Status: design for owner review, revision 35, 2026-11-15. Nothing in this document is built, staged, or validated. Revision 35 applies the owner's decision of 2026-11-15, "Yes, let's drop connectors.", which [`connector-removal-design.md`](connector-removal-design.md) revision 2 designs: the gateway's tool record covers platform tool calls alone, and a runtime's calls to an operator service reach the evidence as egress traffic. Revision 34 notes in the evidence-loss table that a project quota on a slot volume, under PL-10 of `agent-pod-layout-design.md`, closes the route by which an unfrozen agent fills the volume past its `sizeLimit` to trigger the kubelet's eviction, where the quota applies. It reads against [`egress-and-git-design.md`](egress-and-git-design.md) revision 92, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 40, [`user-git-identity-design.md`](user-git-identity-design.md) revision 45, [`github-credential-addon-design.md`](github-credential-addon-design.md) revision 7, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 21, [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 24, [`message-delivery-design.md`](message-delivery-design.md) revision 23, [`connector-removal-design.md`](connector-removal-design.md) revision 7, [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md), and the designs of 2026-11-12 that it treats as implemented: [`session-isolation-design.md`](session-isolation-design.md) revision 17, [`agent-pod-layout-design.md`](agent-pod-layout-design.md) revision 15, [`workspace-materialization-design.md`](workspace-materialization-design.md), [`runtime-protocol-design.md`](runtime-protocol-design.md) revision 12, [`placement-design.md`](placement-design.md) revision 11, and [`tenant-security-settings-design.md`](tenant-security-settings-design.md) revision 9. Every decision under "Decisions" is recorded as settled, including decision 28, which settles the question revision 31 raised as O1. No spike, test, benchmark, production component, or live provider write has run for this design, and every number under "Volume and cost" is a planning assumption. Claims about Lenny were checked against the specification and the tree on 2026-11-02 and 2026-11-03. Claims about external products come from the pages listed under "Sources" and were read rather than tested. Proposals carry the label FE-n. "Review history" at the end records how the design reached this form.

## Problem

A company runs Lenny as a multi-tenant SaaS, and its security forensics team investigates prompt-injection exfiltration, abuse, suspected cross-tenant breaches, and leaked credentials. Lenny records a great deal, but nothing can freeze evidence against the tenant under investigation, and nothing records what an agent did inside its pod. The current state, as read from the specification and the tree, follows. "Terms" and "Terms from other designs and the platform" below define the platform terms the table uses, such as the legal-hold ledger, the audit chain, the occupancy projection, and `draining`.

| Area | Current state | Where |
| :-- | :-- | :-- |
| Legal hold scope | The setter accepts a session or an artifact. `LegalHoldRequest` has the fields `tenantId`, `sessionId`, `artifactId`, `hold`, and `note`, and a tripwire test (a unit test that fails when the setter gains a new scope) refuses `audit_range`. | §12.8 **Legal hold**, `pkg/gateway/externalapi/admin/legalhold.go`, `legalhold_scope_tripwire_test.go` |
| Legal hold visibility | A tenant admin can set, list, and release holds on its own tenant. | §10.2 permission matrix, §15.1 `POST /v1/admin/legal-hold` |
| `audit_range` | The §12.8 preflight and Phase 3.5 read a ledger entry of this type, and nothing writes one. `tenantHoldEnumerator`, which lists a tenant's held resources for Phase 3.5, does not enumerate it, and the escrow migrator, which moves held resources to the escrow bucket during tenant deletion, records it with no blob body. | §12.8 step 0, `cmd/lenny-gateway/tenantdeletion.go`, `cmd/lenny-gateway/legalholdescrow.go` |
| Audit retention | The partition GC, the job that drops time partitions of the audit table past retention, checks the SIEM forwarder's last acknowledged sequence number (its high-water mark) and no hold. | §16.4 **Log retention and EventStore management.** |
| Tenant teardown | Phase 4 runs `DeleteByTenant` on the audit `EventStore`, the store that holds the `audit_log` table. The tripwire test's comment states that the eraser has no escrow skip. | §12.8 **Tenant deletion lifecycle.** |
| Force-terminate | The session moves to `failed` and the gateway releases the pod. | §24.11, `cmd/lenny-gateway/session_admin.go` |
| Tenant suspension | Suspension drains every session, and a terminal claim disposition takes the pod through `draining` to `terminated`. | §15.1 `POST /v1/admin/tenants/{id}/suspend`, §4.6.1 **Occupancy projection** |
| Volatile evidence | Every volume of a slot is an `emptyDir`. `/dev/shm` and `/run/lenny` are memory-backed, and `/workspace`, `/session`, and `/tmp` are disk-backed with their own size limits. The kubelet removes all of them when the pod terminates, and each slot release sweeps a slot's volumes when its session ends. | §6.4, [PL-3](agent-pod-layout-design.md#pl-3-runtime-visible-filesystem-layout) and [PL-5](agent-pod-layout-design.md#pl-5-volumes-size-limits-and-eviction), [SI-3](session-isolation-design.md#si-3-runtime-processes-slot-release-and-setup-commands) |
| Incident primitives | §11.8 lists credential revocation, user invalidation, legal hold, and the audit trail. It leaves procedures to the deployer. | §11.8 |
| Runbooks | §17.7 has no forensic runbook. | §17.7 |
| Agent actions | The adapter emits the `session.tool_call` span for adapter-local tools, with a name and an identifier and no arguments. [`runtime-protocol-design.md`](runtime-protocol-design.md) retires the adapter-local tools (its decision D5). Nothing records commands the agent runs or files it opens. | §16.3, `pkg/adapter/attach.go`, `pkg/adapter/toolcall.go` |

## Scope

This design covers cases and custody events (FE-0), quarantine of an agent pod (FE-1), holds on git mirror stores (FE-2), security holds that the tenant under investigation can neither see nor release and a retention class that survives tenant teardown (FE-3), a reconciler that compares evidence sources (FE-4), the capture runbook and the abuse workflow (FE-5), and agent activity streams (FE-6).

This design consumes the envelope, the provenance values, `lenny-evidence`, the held marker, the `quarantined` binding state, and the mirror's held flag from [`egress-and-git-design.md`](egress-and-git-design.md). It treats them as fixed inputs. Who may read evidence is owned by [`security-oversight-design.md`](security-oversight-design.md), so this document proposes default grants and marks them as decisions for that design.

## Terms

| Term | Meaning |
| :-- | :-- |
| Investigator | A member of the operator's forensics team, acting through a case under the `platform-forensics` role or as a `platform-admin`. |
| Case | A record in the regional platform Postgres that groups one investigation: its tenants, its justification, its state (`open` or `closed`), and an optional public key for capture encryption. Every forensic action names a case. See "Architecture". |
| Custody event | A platform-tenant audit event with the `forensics.*` prefix that each forensic action writes before it takes effect, naming the actor, the case, and the justification (FP5). |
| `platform-forensics` | The built-in role this design adds for investigators. See "Who can do what" under "Architecture". |
| Quarantine | FE-1's state for one agent pod: the pod does no tenant work and reaches nothing, and its memory, its volumes, and its pod object stay until a recorded release. The `lenny.dev/quarantine` label on the pod and a `pod_quarantines` row carry it. A released pod always terminates. |
| Break glass | A member of `forensics.breakGlassGroups` stamping the quarantine label with `kubectl` when the quarantine API is unavailable. |
| Preservation RPC | `CH-PRESERVE`, an RPC this design proposes from the gateway to the adapter. It puts the adapter into a state in which it never ends the runtime, sends it nothing, and never exits. |
| Freeze | Stopping every process in a slot container without killing it: SIGSTOP from the slot's runtime supervisor inside the pod, or `runsc pause` or `cgroup.freeze` from the node. See "Freezing the agent". |
| `keepalive` | A regular container this design adds to every agent pod. It sleeps until SIGTERM, so the pod stays non-terminal and the kubelet keeps its volumes. See "Keeping the volumes". |
| Session disposition | A quarantine request's `sessionDisposition`: `fail` ends the pod's sessions as a lost pod would, and `resume` recovers them on a replacement pod. |
| Forensics reconciler | A loop in `lenny-controller` that creates a quarantined pod's PodDisruptionBudget and a held store's VolumeSnapshot, writes the `pod_quarantines` row for a pod quarantined through the break-glass label, carries out a quarantine's release, and retries each incomplete step. |
| Evidence reconciler | `lenny-evidence-reconcile`, FE-4's service that compares evidence sources and writes a finding to `evidence.reconcile` when they disagree. Its rules carry names that start with `R-`. |
| Legal hold | The existing tenant-visible hold of §12.8, which a tenant admin can set, list, and release on its own sessions and artifacts. This design leaves it unchanged. |
| Security hold | FE-3's platform-only hold, which the tenant under investigation can neither see nor release. It covers a tenant, a user, a session, a pod, or a mirror store for a time range, and it copies or marks the held material. |
| Basis and deferring basis | A security hold's `basis` is its enumerated ground for retention (`legal_obligation`, `legal_claims`, `security_incident_response`, or `customer_instruction`), and `basisRef` names its approval. A deferring basis is one listed in `forensics.holds.deferErasureBases`, under which a hold keeps its material through user erasure and tenant deletion until release. |
| Escrow | The per-region bucket that §12.8 uses for legal-hold copies. FE-3 reuses it under its own `security-hold/` prefix and its own key encryption key (KEK). |
| Store hold | FE-2's hold on a git mirror store. It sets the store's held flag, snapshots the store's volume, and moves service to a fresh generation. |
| Security retention class | `retentionClass: security` on an evidence stream. Tenant teardown moves such a stream's records to escrow for `forensics.retention.afterTeardownDays`, whether or not a hold exists. |
| Capture | A copy of a pod's memory, process state, file systems, or logs taken during a case, encrypted to the case's public key and registered on the case. |
| Capture Job | The privileged Kubernetes Job in the `lenny-forensics` namespace that freezes and captures a pod on its node, run from an image Lenny publishes through the deployer's privileged access workflow. It is a named node agent of the placement design. See FE-5. |
| Activity streams | FE-6's evidence streams `agent.activity.tool`, `agent.activity.exec`, `agent.activity.exec.args`, and `agent.activity.file`, which record the tools an agent called, the processes it ran, and the files it opened. |
| Collector | `lenny-activity-collector`, FE-6b's per-node DaemonSet in the `lenny-node-agents` namespace that receives gVisor's trace points and writes activity streams. It is a named node agent of the placement design. gVisor runtime monitoring (`seccheck`) emits a trace point for each traced syscall or event, and its remote sink sends them to a Unix socket on the node, where the collector listens. |
| Monitored pool | A pool with `activityMonitoring: true`. Its pods run under the `gvisor-monitored` RuntimeClass and send trace points to the collector. |
| Coverage | Which producers recorded a pod's activity, over which intervals, and with which gaps. A response states `coverage` so that a missing record is never read as proof that nothing happened (R8). `coverage: tool_only` means that only tool activity exists for the pod. |
| Activity floor | The minimum activity collection, detail, and retention that a tenant's settings cannot go below, set by `forensics.activity.floor` for the deployment and `agentActivity.floor` for one tenant. |
| Fingerprint | A keyed hash (HMAC-SHA-256) that replaces a redacted value in an activity record, so an investigator holding a leaked value can test for it without the record storing it. See "Redaction". |
| Session key and pod-incarnation key | The keys that encrypt activity payloads. A record with one session uses that session's key. A record with no single session uses a key per tenant and pod incarnation. See "Keys". |
| The outline | The first version of this document, dated 2026-11-02, which listed the proposals and their open questions before the design was written. |
| Spike | A hands-on test this design names as owed before a proposal relies on it. "The spike" means the gVisor spike, item 1 under "Validation owed". No spike has run. |
| The stand-in | The test deployment that the egress design's validation round 9 ran on a laptop at 2,000 CONNECTs per second ([`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams)). |
| Review round and validation round | A review round is one pass of independent review of this design, recorded under "Review history". A validation round is one round of hands-on tests of the egress design. |
| Watchlist | The paths whose access the `file` stream records exactly rather than in aggregate, set by `forensics.activity.fileWatchlist` and extended by a tenant. See "Volume and cost" under FE-6. |

The settings, events, and permissions this design adds start with `forensics.`, the pod label and the quarantine table use `quarantine`, and the activity streams and tenant settings use `agent.activity.` and `agentActivity.`. The channel identifiers `CH-PRESERVE` and `CH-ACTIVITYREPORT` are proposals under the naming law of §28.

### Terms from other designs and the platform

This design builds on mechanisms that other designs and the specification define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Operator, deployer, platform admin, and cluster admin | The operator is the company that runs Lenny for its tenants, and its forensics team runs investigations. The deployer is whoever installs and configures Lenny, usually the operator. A platform admin holds the `platform-admin` role across tenants. A cluster admin administers the Kubernetes cluster directly, outside Lenny's roles. |
| Gateway | Lenny's API gateway, through which clients and admins reach the platform and which coordinates each session with its pod. |
| Runtime and adapter | In each agent pod, the runtime is the agent's own process, and the adapter is Lenny's process that connects it to the gateway. |
| Slot and slot container | Every agent pod has the adapter container, `netagent`, `keepalive`, and one slot container `runtime-<n>` for each slot, in which the runtime supervisor runs as PID 1 at the slot's own UID and launches the runtime process ([PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint)). A pod with one slot has the same layout. The embedded deployment model, in which the adapter ran in the agent's container, is removed ([PL-2](agent-pod-layout-design.md#pl-2-removal-of-the-embedded-deployment-model)). |
| Runtime supervisor | The process that runs as PID 1 in each slot container. It launches the runtime process for each session and ends it, serves the file-descriptor broker of [SI-5](session-isolation-design.md#si-5-the-supervisor-file-descriptor-broker), runs setup commands and the materialization helper `lenny-materialize` through its worker, and answers the adapter on that slot's `CH-SUPERVISE`. It runs at the slot UID, so §4.7.11 item 8 places it on the untrusted side. |
| Slot release | The sequence that ends each session in a slot: End, which kills every process of the slot, the supervisor's Sweep of the slot's volumes, the pool's cleanup commands, a second End, a second Sweep, and the adapter's removal step and emptiness check ([SI-3](session-isolation-design.md#si-3-runtime-processes-slot-release-and-setup-commands)). It leaves the slot's volumes empty. |
| Setup run and cancel pipe | A setup command or a `lenny-materialize` run that the adapter starts with a `setup` request on the slot's `CH-SUPERVISE`. At most one run is outstanding per slot. While it runs, the supervisor serves only `fs`, `ping`, and `end`, and answers every other request with `worker_busy`. The adapter cancels a run by closing its end of the run's cancel pipe, and the supervisor replies `setupdone`. The supervisor enforces the run's `timeoutMs`, plus a 5-second status wait ([WM-3](workspace-materialization-design.md#wm-3-the-protocol)). |
| Channel identifiers | Names from the channel registers of §28. `LNK-POD-GRPC` is the gateway's gRPC connection to a pod's adapter, and `LNK-GWCONTROL` is the adapter's connection to the gateway. `CH-MSGSOCK` is the Unix socket on which the adapter and a slot exchange framed messages, including the `tool_call` frames that carry the runtime's platform tool calls ([RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames)). Lenny has no connectors, so `platform` is the one tool server on these frames ([`connector-removal-design.md`, "CR-1 Removal"](connector-removal-design.md#cr-1-removal)). `CH-SUPERVISE` carries the adapter's control of a slot's runtime supervisor, on `@lenny-supervise-<n>`. |
| Hold state and coordinator hold timeout | One gateway replica coordinates each session. Hold state is the adapter's state after it loses that coordinating replica (§10.1): the runtime keeps running and receives nothing. A new coordinator takes over by sending `CoordinatorFence`, and if none does within `coordinatorHoldTimeoutSeconds` (120 seconds), the coordinator hold times out and the adapter ends the sessions. |
| Warm pod, claim, and binding state | A warm pod is a pre-started agent pod in a pool's warm-pod inventory. A claim, recorded as a `SandboxClaim`, reserves a warm pod for a session, and its binding state records the claim's phase (§4.6.3). A `Sandbox` is the resource for the pod itself, whose phase the WarmPoolController writes (§6.2). |
| Occupancy projection and its edges | The WarmPoolController computes each `Sandbox` phase (`idle`, `reserved`, `claimed`, `draining`, `terminated`, and others) from the pod's claim and its binding state (§4.6.1 **Occupancy projection**). An edge is one allowed transition of that phase, such as `claimed → draining` when a claim reaches a terminal disposition (released or failed). `draining` leads to `terminated`. |
| Reserved hold | On a recycling pod, the gateway keeps the claim in the `reserved` binding state between two sessions of the same tenant, until `gateway.claimHoldTTLSeconds` expires and the claim is deleted (§4.6.1 **Reserved hold**). It is unrelated to a legal hold or a security hold. |
| Orphan claim GC and orphan session reconciler | Periodic cleanup loops. The orphan claim GC deletes stale `SandboxClaim` resources, such as a claim whose binding state has not changed for `claimOrphanTimeout` (§4.6.1). The orphan session reconciler moves a session to a terminal state when its pod ended without recording one (§10.1). |
| WarmPoolController | The controller in `lenny-controller` that keeps each pool's warm pods and projects each `Sandbox`'s phase. |
| Admission webhooks and informers | Lenny's admission webhooks check Kubernetes writes before they apply. `lenny-label-immutability` guards pod labels, `lenny-sandboxclaim-guard` guards claims, and `lenny-ephemeral-container-cred-guard` guards ephemeral containers. These are baseline webhooks, which Lenny's Helm chart (the chart) always renders (§17.2). An informer is a Kubernetes client cache that watches resources and serves reads from memory. |
| Retirement | A pod leaving service through `draining` to `terminated`. A terminal claim, a recycle limit, a failed container, a drain request, a deletion, or a runtime rollout retires a pod (see "Retirement paths" under FE-1). It is unrelated to a base runtime's retirement under [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) and to a revision that leaves traffic under [`runtime-revisions-design.md`](runtime-revisions-design.md). |
| Runtime rollout, pod set, and `Contracting` | Under [`runtime-revisions-design.md`](runtime-revisions-design.md), a runtime has numbered revisions, and a pool keeps a pod set, its warm pods built from one revision, for each revision in traffic. A rollout moves traffic from one revision to another, and its `Contracting` phase deletes the old revision's pod sets ([RV-4](runtime-revisions-design.md#rv-4-rollout)). |
| Concurrent-slot pod | A pod of a pool with `sessionPolicy.maxConcurrentSessionsPerPod` above 1 and `acknowledgeSharedPod: true`, which serves several sessions of one tenant at once. Each session runs in its own slot container under its own UID, and the slots share the pod's network and IPC namespaces, the adapter, and `netagent` ([PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint)). |
| Delegation tree | A session can start child sessions, which form a delegation tree under a root session. |
| Runtime SDK and capabilities | The runtime SDKs are libraries that a runtime author uses to talk to the adapter (§15.7). A runtime declares capabilities, the behaviours the platform must know of before it acts, which replace the removed integration levels ([RP-2](runtime-protocol-design.md#rp-2-capabilities-and-their-dependencies)). |
| LLM proxy, LLM upstream, and proxy token | The gateway's LLM proxy forwards a runtime's model calls to an LLM upstream, a record that names the provider endpoint and its credential, which the gateway alone holds. A session's runtime presents a proxy token, minted per session at bind and revoked when the session ends or its pod is released, so the pod holds no provider credential ([`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md), [LU-3](llm-upstreams-and-cost-budgets-design.md#lu-3-the-metering-proxy) and [LU-8](llm-upstreams-and-cost-budgets-design.md#lu-8-security)). That design removes LLM credential leases and direct delivery mode ([LU-4](llm-upstreams-and-cost-budgets-design.md#lu-4-removal)). A runtime that holds its own provider key calls the provider over egress, outside the proxy ([LU-10](llm-upstreams-and-cost-budgets-design.md#lu-10-calls-outside-the-proxy)). |
| Isolation profile | A pool's `isolationProfile`. `standard` runs pods under runc, which shares the host kernel and gives no protection against kernel exploits. Its blast radius is bounded by the node classes that keep other members off its nodes and by user namespaces, which the class that holds runc pods renders by default and an operator may disable only with a loud warning, and the choice of profile is the operator's, through `allowStandardIsolation` ([PM-1](placement-design.md#pm-1-node-classes-and-presets)). `sandboxed`, the default, runs pods under gVisor. `microvm` runs them under Kata Containers. |
| Named node agent | A Lenny workload that runs on agent nodes by design, with tolerations the admission webhook allows by namespace and component label: the collector and the capture Job ([PM-5](placement-design.md#pm-5-rendering)). |
| gVisor, `runsc`, Sentry, and gofer | gVisor runs a container's processes against its own user-space kernel, the Sentry, with a separate gofer process for file access. `runsc` is its runtime binary. A host-kernel sensor sees the Sentry and the gofer rather than the agent's processes. |
| Kata Containers | A runtime that runs each pod in a lightweight virtual machine with its own guest kernel. |
| Host eBPF sensor | A tool such as Tetragon or Falco that traces processes and file access in a node's kernel through eBPF programs. It runs as a privileged DaemonSet. |
| RuntimeClass | The Kubernetes object that names the container runtime handler a pod runs under. |
| Token Service | The Lenny service that stores credentials and mints short-lived tokens from them ([§4.3](../spec/04_system-components.md#43-token-service)). It makes its own mint check of a pod, independently of the decision service. |
| Mint checks | The independent checks that every mint passes: the Token Service's own check of the pod, its bindings, and its writable set, and the scope decision the Token Service asks of the decision service. A mint happens only when both agree ([`egress-and-git-design.md`, "Terms"](egress-and-git-design.md#terms)). |
| `netagent` | A Lenny container in every agent pod that uses the local egress endpoint, running under its own UID. It holds the pod's service account tokens for egress, terminates TLS for the hosts it must inspect, attaches credentials, runs the git path, applies request rules, and opens tunnels through `lenny-egress`. |
| Local egress endpoint | The loopback HTTP CONNECT endpoint on which tools in the pod reach `netagent`. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I1. |
| `lenny-egress` and Envoy | The central egress proxy, built on Envoy, through which agent traffic leaves the cluster. A tunnel is one CONNECT through it. |
| Decision service | `lenny-egress-authz`, which decides whether each connection is allowed from the session's effective policy, and writes `egress.decision`. |
| Pod credential API | The API on which `netagent`, and the git mirror, ask the Token Service for a binding's token. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I8. |
| Credential binding and pod binding | "Binding" has two senses here. A credential binding is the egress design's object that gives sessions a secret for named hosts, which a `userCredential` binding extends. A pod binding is `pkg/binding`'s resolution of a pod to its session, tenant, and bindings at a given time. |
| `pkg/binding` | The package the Token Service, the decision service, and the mirror each use to resolve a pod to its session, user, tenant, and bindings, each from its own read. |
| `quarantined` binding state | The state the egress design reserves in `pkg/binding` for this design ([`egress-and-git-design.md`, "Reserved for other designs"](egress-and-git-design.md#reserved-for-other-designs)). A pod is in it when it carries the `lenny.dev/quarantine` label or has an active `pod_quarantines` row, and it takes precedence over every other state: the decision service refuses all egress, the Token Service mints nothing, and the mirror serves nothing. |
| Covered host | A git host that a VCS pool or a git route of the tenant names, or an alias host of that provider. `netagent` terminates TLS for it and runs the git path. Git on any other host passes through an opaque tunnel. |
| Response scrub and pool recycle scrub | The response scrub is `netagent` removing a bound secret from responses on bound hosts, and its hits are recorded in `egress.local`. The pool recycle scrub of §5.2 cleans a pod between sessions. They are different mechanisms. |
| Mint | A call in which the Token Service obtains or releases a token for a pod. Each one writes a `credential.mint` record. |
| Git mirror and stores | `lenny-git-mirror` serves git fetches from stores, of kind `public` or `tenant`. Its parent process keeps the store index. A store's on-disk path includes a generation, so a held generation and its successor coexist. |
| Fetch UID and sweep | Each mirror fetch runs under a UID taken from a pool of UIDs. After the fetch, a sweep re-chowns the files the fetch wrote, and the UID then returns to the pool. |
| Held flag | The flag in the mirror's store index that stops the sweep's re-chown, repacks, and purge for a store. |
| `egressProfile` | A pool setting: `restricted`, `proxied`, or `internet`. Under `internet`, the pod also has raw sockets that bypass `netagent` and `lenny-egress`. |
| Level 1 | An egress replacement level: a deployment that replaces `lenny-egress` with the deployer's own proxy, which enforces a host list Lenny publishes and makes no call to the decision service per connection. |
| Tunnel cap | A pool's maximum tunnel lifetime, which Envoy enforces as `max_stream_duration`. |
| `lenny-evidence` and evidence streams | `lenny-evidence` is the one service that writes evidence. Evidence is a set of typed streams sharing one envelope, stored per tenant and hour in the object store with a batch manifest row in Postgres. Compaction merges a tenant-hour's batches. The egress design's streams are `egress.decision`, `egress.tunnel`, `egress.accesslog` (Envoy's own measurements), `egress.local` (`netagent`'s reports), `git.operation`, and `credential.mint`. |
| Evidence envelope | The fields every evidence record carries: the stream, the verified producer, its incarnation and sequence number, the time, attribution (tenant, pool, pod UID, and session), `traceId`, `provenance`, `payloadKeyRef`, and the payload ([`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams)). A record can carry a chunk, a run of one session's payloads under one sequence range (`sequenceEnd`). |
| Incarnation and pod incarnation | A producer's incarnation is a value that changes each time a producer instance starts. A producer ends an incarnation with a closing record, so an incarnation with no closing record may have lost its unflushed records. A pod incarnation is one agent pod's lifetime, which the pod-incarnation key of "Keys" follows. |
| Tag | An attribution value, such as the pod or tenant, that the decision service returns with a decision. Envoy logs the tags it receives, so they are only as trustworthy as the decision service. |
| Provenance | An envelope field. `enforced` marks a record from a component outside the pod that the action passed through, `observed` one from a component outside the pod that watched without being in the path, and `self_reported` one from inside a pod or from outside the platform's control. A `self_reported` record is evidence only while its producer is uncompromised. |
| Held marker | A mark on an evidence manifest row that stops retention sweeps and compaction from deleting the row. |
| Platform prefix | The evidence location for records that no tenant reads: a warm pod's records, which have no tenant, and a quarantined pod's records. |
| Sequence gap and gap record | Each producer numbers its records per stream and tenant, so a consumer detects lost records as a gap. A gap record states possible loss. |
| Audit chain and anchor | §11.7 stores each tenant's audit events as a hash chain: each `audit_log` row carries a `sequence_number` and the hash of the row before it (`prev_hash`), so a deleted or altered row breaks the chain. An anchor is an audit event in that chain that records the digests of a tenant's new or compacted evidence batches, so the chain vouches for the evidence. |
| Legal-hold ledger | The §12.8 record of legal holds: the `legal_hold` flag on session and artifact rows with the `legal_hold.set` and `legal_hold.cleared` audit events. Step 0 of user erasure and Phase 3.5 of tenant deletion read it. §12.8 also names ledger entry types `audit_range` and `workspace_snapshot`. |
| Platform-tenant audit event | An audit event written under the platform tenant that names the affected tenant in `unmapped.lenny.target_tenant_id`. A tenant admin cannot read it, and it survives tenant teardown. CMP-058, the requirement under §11.7 **Platform-tenant audit event residency**, routes it to the target tenant's region and fails closed when the region cannot be resolved. |
| Tenant deletion phases | §12.8 runs tenant deletion in phases. This design names Phase 3.5, legal-hold segregation before any destructive step; Phase 4, which runs `DeleteByTenant` on every store; Phase 4a, which schedules deletion of the tenant key when its state is `active` or `retained`; Phase 5, which removes the tenant's Kubernetes resources; and Phase 6, which writes the erasure receipt. |
| `DeleteByUser` and step 0 | `DeleteByUser` is the user erasure operation of §12.8. Its step 0 is the preflight that blocks on a legal hold. |
| Dead-lettered rows and `RedactionReceipt` | A dead-lettered row is an audit row that the OCSF translator could not deliver to the SIEM. User erasure redacts personal data from such rows, and a signed `RedactionReceipt` records each redaction and its legal basis (§12.8). |
| Capacity tier and test tier | A capacity tier is a deployment size of §17.8.2, and §16.4 sizes Tier 3 at 10,000 sessions per day. A test tier is one level of Lenny's test suite, from tier 0 (static checks) to tier 11 (documentation). |
| KMS, KEK, and data keys | A key management service (KMS) holds key encryption keys (KEKs), which wrap the data keys that encrypt stored data. |
| Pack, platform chunk set, and platform pack | Under [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md), a checkpoint snapshot and an output file are lists of chunks stored in packs, which are objects in the object store. A session's packs belong to its tenant. A platform chunk set holds the chunks of content the platform placed on its pods from one source, a warm repository store or a runtime's shared assets, in platform packs that the snapshots of many sessions may reference ([IC-4, "Platform chunk sets"](incremental-checkpoint-design.md#platform-chunk-sets), IC decision D1, settled 2026-11-13). A set of a source an operator declares is platform-owned and stored under the deployment key, and a set of a tenant's own source is owned by that tenant. Each platform pack holds one source's chunks, and a platform contribution records the pod that added it. |
| Data classes | The classes of §12.9, Public, Internal, Confidential, and Secret, formerly T1 to T4. Confidential covers workspace files, snapshots, and transcripts, and Secret covers credentials and tokens. A data class is a property of a kind of data and never a tenant choice ([TSS-8](tenant-security-settings-design.md#tss-8-data-classes)). |
| Tenant key and tenant key state | The KMS key with alias `tenant:{tenant_id}` that encrypts a tenant's object-store data while the tenant's security setting `encryption.tenantKey` is `required`. Its state is `none`, `active` while the setting is `required`, or `retained` after a relaxation while objects under the key remain ([TSS-4](tenant-security-settings-design.md#tss-4-tenant-key)). |
| Tenant deletion deadline | The tenant's `deletion.tenantDeadlineHours` security setting, recorded on the tenant at Phase 1 as the time by which the deletion lifecycle reaches `deleted` ([TSS-5](tenant-security-settings-design.md#tss-5-deadlines-and-retention)). |
| Regulated tenant | A tenant whose `complianceProfile` is `soc2`, `fedramp`, or `hipaa` (§11.7). |
| SIEM copy | The copy of audit and evidence records that the deployer, or a tenant under SO-3, delivers to a security information and event management system (SIEM). |
| OCSF | The Open Cybersecurity Schema Framework, the event schema §11.7 uses for audit export. |
| `security-admin`, `security-auditor`, and `auditorStreamGrants` | Tenant security roles from SO-1. A `security-admin` grants a `security-auditor` read access to a stream by listing it in `auditorStreamGrants`, and the grant takes effect after a delay. |
| `userCredential` binding | A credential binding that delivers each session its own user's token ([`user-credential-delivery-design.md`](user-credential-delivery-design.md)). Under `delivery: inject`, `netagent` attaches the token to requests and the agent never reads it. Under `delivery: file`, `netagent` writes it to a file the agent can read. |
| `registered` and `oauth2Link` sources | Where a user's token comes from: a tenant backend writes a `registered` token, and an `oauth2Link` token comes from the user's linked account at an OAuth2 provider. A child token is a narrowed per-session token the GitHub add-on derives from a link. |
| Token trust record | The credential design's record of everything that decides where a stored user token can go. A token is sealed to its record, and a change to the record makes the token unusable until the record reverts or the user acts again. |
| Exposure rows | Durable rows the credential design writes under `delivery: file`, naming each token generation a runtime held, so a tenant's backend can revoke it ([`user-credential-delivery-design.md`, "Exposure records"](user-credential-delivery-design.md#exposure-records)). |
| `leakScan` | The credential design's optional leak scanning, which redacts a known token value from the traffic the gateway carries ([`user-credential-delivery-design.md`, "Leak scanning (UC-6)"](user-credential-delivery-design.md#leak-scanning-uc-6)). |
| PodDisruptionBudget (PDB) | A Kubernetes object that refuses an API-initiated eviction of the pods it selects. The kubelet's node-pressure eviction ignores it. |
| CNI | The cluster's container network interface plugin, such as Calico or Cilium, which enforces NetworkPolicies. |
| CSI VolumeSnapshot | A point-in-time copy of a volume, taken through the storage driver's Container Storage Interface (CSI). |
| `BUILD-GAPS.md` findings | Identifiers of the form F-n.n.n name defects filed in `BUILD-GAPS.md` at the repository root. |

### Labels from other designs

Proposals and properties in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-2, EG-3, EG-5, EG-7, EG-8, EG-9, EG-10, and EG-11 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | Proposals of the egress design: EG-2 the pod network agent and proxied egress, EG-3 the pod git path, EG-5 the git mirror and warm fill, EG-7 level 1 replacement, EG-8 tenant visibility, EG-9 egress policy objects with the single write path for tenant opt-ins, EG-10 evidence ingest, and EG-11 the egress public key infrastructure (PKI). |
| RV-3 and RV-4 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | [RV-3](runtime-revisions-design.md#rv-3-pods) the pod sets a pool keeps per revision, and [RV-4](runtime-revisions-design.md#rv-4-rollout) the rollout that moves traffic between revisions and deletes the old pod sets. |
| IC-4, IC-6, and IC-9 | [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) | [IC-4](incremental-checkpoint-design.md#ic-4-catalog-reference-closure-and-quota) the pack catalog and the platform chunk sets, [IC-6](incremental-checkpoint-design.md#ic-6-retention-garbage-collection-erasure-and-legal-hold) retention and deletion of checkpoint snapshots, session packs, and platform packs, including their copy under a security hold and the §12.8 legal-hold escrow, and [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) the removal of the `SIGSTOP` checkpoint path with its `checkpointStuck` flag and liveness rule. |
| MD-18 and A16 | [`message-delivery-design.md`](message-delivery-design.md) | [MD-18](message-delivery-design.md#md-18-files) output files, the files an agent produces for the user, each recorded by an `output_files` row whose chunks sit in checkpoint packs, and [A16](message-delivery-design.md#decisions) the owner's decision of 2026-11-10 that a security hold's `include: artifacts` covers them. |
| P2, P4, and P7 | [`egress-and-git-design.md`, "Security properties"](egress-and-git-design.md#security-properties) | Egress security properties: P2 keeps long-lived credentials out of pods, P4 keeps one tenant's data and credentials away from another tenant's pods, and P7 keeps one pod's load from removing another tenant's egress. |
| SO-1, SO-3, SO-4, and SO-7 | [`security-oversight-design.md`](security-oversight-design.md) | [SO-1](security-oversight-design.md#so-1-security-roles) security roles, [SO-3](security-oversight-design.md#so-3-per-tenant-siem-destination) the per-tenant SIEM destination, [SO-4](security-oversight-design.md#so-4-operator-access-transparency) operator access transparency, which mirrors operator events to the target tenant, and [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) the narrowing of `lenny-ops` rights. "SO decision 19" is [decision 19](security-oversight-design.md#decisions) of that design. |
| TS-1 | [`tenant-scoping-identity-design.md`, "TS-1: Environment scoping"](tenant-scoping-identity-design.md#ts-1-environment-scoping) | Environment scoping. |
| DR-6 | [`tenant-derived-runtimes-design.md`, "DR-6: Base changes"](tenant-derived-runtimes-design.md#dr-6-base-changes) | How a tenant-derived runtime reacts to a platform write to its base, including the base's retirement. |
| UC-2 | [`user-credential-delivery-design.md`, "UC-2"](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking) | The `oauth2Link` source and account linking. |
| GH-2 | [`github-credential-addon-design.md`, "GH-2"](github-credential-addon-design.md#gh-2-pull-request-authoring-on-github) | Pull request authoring on GitHub, which issues per-session child tokens. |
| SI-3, SI-5, and SI-7 | [`session-isolation-design.md`](session-isolation-design.md) | [SI-3](session-isolation-design.md#si-3-runtime-processes-slot-release-and-setup-commands) runtime processes, slot release, and setup commands, [SI-5](session-isolation-design.md#si-5-the-supervisor-file-descriptor-broker) the supervisor's file-descriptor broker, and [SI-7](session-isolation-design.md#si-7-credentials-manifests-and-other-shared-files) credentials, manifests, and other shared files. |
| PL-1, PL-2, PL-3, and PL-5 | [`agent-pod-layout-design.md`](agent-pod-layout-design.md) | [PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint) the containers of an agent pod, [PL-2](agent-pod-layout-design.md#pl-2-removal-of-the-embedded-deployment-model) the removal of the embedded deployment model, [PL-3](agent-pod-layout-design.md#pl-3-runtime-visible-filesystem-layout) the paths a runtime sees, and [PL-5](agent-pod-layout-design.md#pl-5-volumes-size-limits-and-eviction) the volumes with their media, size limits, and eviction. |
| WM-3 | [`workspace-materialization-design.md`](workspace-materialization-design.md#wm-3-the-protocol) | The `setup` request, its log and result pipes, the cancel pipe, and the `setupdone` reply through which the slot's supervisor runs `lenny-materialize` and setup commands as the slot UID. |
| RP-2 and RP-3 | [`runtime-protocol-design.md`](runtime-protocol-design.md) | [RP-2](runtime-protocol-design.md#rp-2-capabilities-and-their-dependencies) runtime capabilities, and [RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames) platform tool calls carried as frames on `CH-MSGSOCK`. |
| CR-1 and CR-3 | [`connector-removal-design.md`](connector-removal-design.md) | [CR-1](connector-removal-design.md#cr-1-removal) removes connectors, with the connector tool path and the `PreConnectorRequest` interceptor phase. [CR-3](connector-removal-design.md#cr-3-external-services) is the guidance by which a runtime reaches an operator service, such as an MCP server, an agent service, or a memory service, from its pod through `netagent` and egress, where Lenny records the connection and its bytes and the service records the tool calls inside it. |
| PM-1, PM-5, PM-6, and PM-9 | [`placement-design.md`](placement-design.md) | [PM-1](placement-design.md#pm-1-node-classes-and-presets) node classes and the user namespace requirement on runc, [PM-5](placement-design.md#pm-5-rendering) rendering, including the named node agents, [PM-6](placement-design.md#pm-6-enforcement) enforcement, including the node check's drift drain, and [PM-9](placement-design.md#pm-9-namespaces) namespaces. |
| TSS-4, TSS-5, and TSS-8 | [`tenant-security-settings-design.md`](tenant-security-settings-design.md) | [TSS-4](tenant-security-settings-design.md#tss-4-tenant-key) the tenant key and its state, [TSS-5](tenant-security-settings-design.md#tss-5-deadlines-and-retention) the recorded erasure and tenant deletion deadlines, and [TSS-8](tenant-security-settings-design.md#tss-8-data-classes) the data classes. |

## High-level design

When an operator's security team investigates an incident on Lenny, such as an agent tricked into sending data out, an abuse report, a suspected breach between tenants, or a leaked credential, it needs the evidence to stay as it was. Today nothing can freeze it. The only hold is one the tenant under investigation can see and release, offboarding a tenant deletes its records, the platform retires a suspect pod and its memory and files with it, and nothing records what an agent ran or opened inside its pod.

This design lets the operator's forensics team stop a suspect pod while keeping its memory and files, keep records and copies where the tenant can neither see nor remove them and where offboarding cannot delete them, compare records from components that cannot vouch for each other so that one which lies is caught, capture a pod's memory by a written procedure, and record the commands and files an agent touches. The main limit is that the design preserves only what still exists when an investigation starts. A pod usually lives for one session, a compromised component can lie in its own records, and on a pool without process monitoring the activity records show only tool calls, as the gateway carried them and as the agent reports them.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Cases and custody (FE-0).** Every forensic action names a case and writes a custody event to the platform tenant's audit chain before it takes effect. A new `platform-forensics` role holds the forensic permissions, and a tenant sees no sign of a case, a quarantine, or a hold unless the case discloses it (FP3).

**Quarantine (FE-1).** An investigator quarantines a pod through the admin API, or a cluster admin uses break glass when the API is down. The `quarantined` binding state stops egress, mints, and mirror reads, the `lenny.dev/quarantine` label removes the pod from every agent NetworkPolicy and from the warm-pod inventory, the pod's sessions end, and every retirement path skips the pod until a recorded release. The preservation RPC stops the adapter from ending the runtime or releasing any slot, a freeze stops the processes of every slot, and `keepalive` keeps the pod's volumes.

**Mirror store hold (FE-2).** A store hold seals a git mirror store's current generation with its file owners intact, snapshots its volume, and serves later fetches from a fresh generation.

**Security holds and the retention class (FE-3).** A security hold copies artifacts, checkpoints, transcripts, and a segment of the tenant's audit chain to escrow, and marks evidence manifest rows, for a scope and a time range. Only a hold with a deferring basis keeps its material through user erasure and tenant deletion. The security retention class keeps chosen evidence streams for a set period after tenant teardown.

**Evidence reconciler (FE-4).** The evidence reconciler compares records from independent sources, such as Envoy's own access log against the decision service's records, and writes a finding when they disagree.

**Capture and abuse workflow (FE-5).** Runbooks state the capture order, the memory capture method for each isolation profile, and the path from an abuse report's address and time to a tenant and a session. Capture uses an unbound role (a ClusterRole the chart ships with no binding, which the deployer binds for a case) and a published image through the deployer's privileged access, so Lenny runs no standing privileged component for it.

**Agent activity streams (FE-6).** FE-6a records tool activity on every pod, from the gateway, the adapter, and an optional report from the runtime SDK. FE-6b records processes and file access as `observed` evidence on monitored pools, through the collector. Activity records are redacted before they leave the node, encrypted under keys that erasure can destroy, and readable under named permissions. "Delivered user tokens" states how quarantine and evidence treat a person's token in a pod.

"Architecture" below shows the components. "Residual risks" states what a compromised component, the kubelet, or a tenant acting before a hold exists can still destroy, and a missing activity record is never proof that nothing happened, because every response states its coverage.

## Requirements

- R1. An investigator can stop a pod from doing tenant work and from reaching anything, while its memory, its volumes, and its pod object stay intact, against every path that retires or evicts a pod.
- R2. An investigator can freeze a mirror store and copy it, including the ownership and mode bits that say which fetch wrote each file.
- R3. A hold can cover a tenant, a user, a session, or a pod for a time range, across audit rows, evidence streams, and session artifacts. The tenant under investigation can neither see nor release it.
- R4. Designated evidence survives tenant teardown for a set period, because a compromised or malicious tenant can otherwise destroy evidence by offboarding.
- R5. A reconciler compares evidence sources and reports disagreement, using at least one source that a compromised decision service does not control.
- R6. The operations documentation gives a capture procedure for each isolation profile, a procedure from an abuse report to a tenant and a session, and a chain of custody.
- R7. Lenny records commands the agent runs and files it opens as typed evidence streams, with a stated provenance, redaction, per-session keys, a cost per tenant, and a named read permission.
- R8. Each record states what produced it and what that producer could have missed, so an investigator never reads a missing record as proof that nothing happened.

## Security properties

- FP1. A quarantined pod receives no new work, obtains no credential, and opens no new connection. Enforcement sits outside the pod, because a compromised runtime controls its slot container, including the supervisor, which runs at the slot UID (§4.7.11 item 8).
- FP2. No Lenny retirement path removes a quarantined pod. The pod leaves quarantine only through a recorded release, and it always terminates afterwards. It never returns to inventory.
- FP3. A security hold and a quarantine produce no signal in any tenant-readable surface: no audit row, list entry, quota change, retention change, or distinct error code. Disclosure is an explicit case action.
- FP4. Held material is retained independently of the tenant's keys and the tenant's lifecycle, in the tenant's region (§12.8 **Data residency**).
- FP5. Every forensic action writes a custody event to the audit chain before it takes effect, with the actor, the case, and the justification.
- FP6. A reconciler finding never depends on a tag that the component under suspicion supplied.
- FP7. A secret that appears in a command line never reaches storage in plaintext where redaction recognizes it, and `DeleteByUser` makes stored activity unreadable by destroying a key, subject to the rules under "Keys".
- FP8. A record's `provenance` is set by verified identity at ingest. A producer inside a pod is `self_reported` in every isolation profile.
- FP9. Collecting activity adds no privileged container to agent nodes in the default isolation profile.

## Architecture

The design adds one small record, the case, and no new control-plane service.

```
 investigator --> gateway admin API --> Postgres: cases, pod_quarantines, security_holds
                       |  custody events (platform tenant, target tenant's region)
                       |
                       +--> pod label lenny.dev/quarantine ---> pkg/binding informers (decision service,
                       |                                        Token Service, mirror), NetworkPolicy,
                       |                                        lenny-label-immutability, WarmPoolController
                       +--> mirror parent (held flag)
                       +--> lenny-evidence (held markers)

 lenny-controller: forensics reconciler (PDBs, VolumeSnapshots, break-glass rows, release, retrying each step)
 lenny-evidence-reconcile: reads evidence, writes findings through lenny-evidence
 lenny-activity-collector (DaemonSet in lenny-node-agents, FE-6b): gVisor remote sink --> lenny-evidence
```

**Case.** A case is a row in the regional platform Postgres with an identifier, a title, the tenants it names (none at first when an abuse report names no tenant, and `POST /v1/admin/cases/{id}/tenants` adds one with a justification as the investigation finds it), the opening principal, a justification, a state (`open` or `closed`), and an optional public key for capture encryption. A case spanning regions has one row per region under one identifier, because §11.7 **Platform-tenant audit event residency** already routes facts about a tenant to that tenant's region. Every operation below requires a `caseId`, and an action on a tenant requires that the case names it, so an investigator can enumerate what one investigation froze. A closed case refuses new actions.

**Custody events.** Each action writes a platform-tenant audit event with `unmapped.lenny.target_tenant_id` and the fields §11.7 requires, using the `forensics.*` prefix: `forensics.case_opened`, `forensics.quarantine_set`, `forensics.quarantine_released`, `forensics.hold_set`, `forensics.hold_released`, `forensics.store_held`, `forensics.capture_registered`, `forensics.evidence_read`, `forensics.evidence_exported`, and `forensics.evidence_lost`. An unresolvable region fails the action with `PLATFORM_AUDIT_REGION_UNRESOLVABLE`, as CMP-058 requires. A platform-tenant row is invisible to a tenant admin, and it survives tenant teardown, which gives FP3 and FP5 one mechanism. [SO-4](security-oversight-design.md#so-4-operator-access-transparency) of [`security-oversight-design.md`](security-oversight-design.md) mirrors operator events to the target tenant, so SO-4 must exclude this prefix (see "Proposals and dependencies").

**Who can do what.**

| Action | `platform-forensics` (new) | `platform-admin` | `tenant-admin` | `security-auditor` ([SO-1](security-oversight-design.md#so-1-security-roles)) | Cluster admin |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Open or close a case | Yes | Yes | No | No | No |
| Set a quarantine or a security hold, or hold a store | Yes | Yes | No | No | Label only (break glass, FE-1) |
| Release a quarantine, hold, or store | Yes. Under decision 12, a release must come from a principal other than the one who set it when two-person release applies, which is by default for regulated tenants. | Yes, under the same rule | No | No | No |
| Register a capture, read a capture | Yes | No, unless also granted the role | No | No | Performs the node-side capture |
| Query across tenants, run the evidence reconciler for a case | Yes | Yes (`Access other tenants' data`, §10.2) | No | No | No |
| Read own tenant's streams | Not applicable | Yes | Per stream, see FE-6 | Per stream, see FE-6 | No |
| Set own tenant's §12.8 legal hold | No, it uses security holds | Yes | Yes, unchanged | No | No |
| See a security hold, quarantine, or case | Yes | Yes | No, unless disclosed | No, unless disclosed | Sees the label |

`platform-forensics` is a new built-in role in the §10.2 matrix, because custom roles cannot exceed `tenant-admin`. Its permissions are named (`forensics.case.write`, `forensics.quarantine.write`, `forensics.hold.write`, `forensics.capture.read`, and `forensics.query`), following the named-permission rule of [EG-9](egress-and-git-design.md#proposal-split), the egress design's proposal for egress policy objects. A platform admin can grant itself the role, and that grant is an audited role change. None of these operations appears as a Model Context Protocol (MCP) management tool (§25.12), the MCP tool server through which an agent manages Lenny, in the first version, because a release or an export driven by an operations agent would need its own approval model.

## FE-1: Quarantine

### API

| Method and path | Behaviour |
| :-- | :-- |
| `POST /v1/admin/quarantines` | Body: `caseId`, one of `podName` with `namespace` or `sessionId`, optional `includeDelegationTree`, `sessionDisposition` (`fail` by default, or `resume`), `preserve` and `freeze` (each `true` by default, see "Order and failure behaviour" and "Freezing the agent"), and `reason`. Returns `202` with the quarantine identifier, `affectedSessions`, and a per-step status including `preserve` and `freeze`. `dryRun` is refused, as on other action endpoints (§15.1). |
| `GET /v1/admin/quarantines` and `GET /v1/admin/quarantines/{id}` | Lists every quarantined pod across tenants for `platform-forensics` and `platform-admin`, oldest first, with filters `caseId`, `tenantId`, `state`, and `olderThan`. Returns each step's status, the node, and the age of the quarantine. A pod quarantined through the break-glass label appears too. The forensics reconciler writes a row with `origin: break_glass` and a custody event when it sees the label with no row, once the session store (Postgres) is reachable, and until then `GET` reports such pods from the Sandbox informer with `state: unregistered` (decision 27). |
| `POST /v1/admin/quarantines/{id}/release` | Body: `justification` and `acknowledgeEvidenceLoss`, required when no capture is registered. Moves the pod to `draining`. |

`lenny-ctl admin forensics quarantine set|get|list|release` maps to these, in a new §24 subsection beside §24.11. A request naming a session whose pod is gone returns `409 QUARANTINE_NO_POD`, and the response points to a security hold. Each pod has at most one active quarantine, and a repeat request adds the new case to it.

### Data model

| Carrier | Fields | Authority |
| :-- | :-- | :-- |
| `pod_quarantines` row (session store, platform-scoped, because a lookup by pod UID has no tenant context) | Identifier, pod UID, name, namespace, node, pool, tenant, sessions, case, origin (`api` or `break_glass`), state (`applying`, `active`, `released`; `GET` reports a labelled pod with no row as `unregistered`), step status, set and release principals and times | The record of the quarantine |
| Pod label `lenny.dev/quarantine=<id>` | Quarantine identifier | The enforcement signal, read from informers with no database call |
| `SandboxClaim.status.phase = quarantined` | New gateway-owned binding state (§4.6.3) | Excludes the claim from orphan GC and hold expiry |
| Regular container `keepalive` in every agent pod (decision 25) | A static binary that sleeps and exits on SIGTERM, with its own UID, no volume mounts, and a minimal request | Keeps the pod non-terminal, so the kubelet keeps its volumes (below) |
| `Sandbox.status.phase = quarantined` | New phase, written by the WarmPoolController as a projection of the label (§6.2) | Excludes the pod from inventory and from every drain edge |
| Per-pod `PodDisruptionBudget` | `minAvailable: 1`, selecting the quarantine label | Refuses API-initiated eviction |

The label and the row are both read, so either outage leaves one path. `pkg/binding` treats a pod as quarantined when the informer shows the label or when its re-read of the store finds an active row, and the `quarantined` hook of [`egress-and-git-design.md`, "Reserved for other designs"](egress-and-git-design.md#reserved-for-other-designs) fixes both inputs, under EG-2 and, for level 1's tenant policies, EG-7. The PDB uses an integer `minAvailable` because §4.6.1's warm-pod PDB rule states that a `maxUnavailable` budget cannot compute `expectedPods` on a `Sandbox`-owned pod.

### Enforcement points

| Control | Enforced by | Mechanism |
| :-- | :-- | :-- |
| New egress, mints, and mirror reads | Decision service, Token Service, and mirror | The `quarantined` state in `pkg/binding`, each instance reading independently ([EG-2](egress-and-git-design.md#proposal-split), the egress design's proposal for the pod network agent, and [EG-7](egress-and-git-design.md#proposal-split) for level 1's tenant policies). A compromised decision service cannot make the Token Service's own mint check pass. |
| Release of a user's token | Token Service and decision service | The `quarantined` state stops a `userCredential` release on the pod credential API from each instance's own read, with no new work (see "Delivered user tokens"). |
| Raw network paths | CNI (the cluster's network plugin) | Every allow policy selecting agent pods gains `lenny.dev/quarantine DoesNotExist` in its pod selector, so the namespace's `default-deny-all` applies. Today that means `allow-gateway-ingress`, `allow-pod-egress-base`, `allow-pod-egress-llm-proxy`, `allow-pod-egress-otlp`, `allow-pod-egress-provider-direct` until [EG-2](egress-and-git-design.md#proposal-split) removes it, `allow-pod-egress-internet`, `allow-pod-egress-kube-dns`, and `allow-pod-egress-objectstore` (`charts/lenny/templates/agent-network-policies.yaml`), plus the policies EG-2 adds for pools with `egressProfile: proxied` and pools with `gitAccess`. A static test (test tier 0) enumerates every rendered policy in agent namespaces, so a later policy without the exclusion fails. Network policies are additive, so a deny policy cannot express this. |
| Open tunnels | `netagent` and the pool's tunnel cap | `netagent` closes tunnels at the binding change. A compromised `netagent` keeps them until the pool's tunnel cap (`max_stream_duration`), the residual that [the egress design](egress-and-git-design.md) states. Kubernetes leaves it to the network plugin whether a policy change affects an existing connection, so the policy change is no guarantee either. FE-5 gives the reset procedure. |
| New sessions and claims | Gateway and `lenny-sandboxclaim-guard` | The phase leaves inventory, and the webhook refuses a claim whose pod carries the label. |
| Session work | Gateway | The sessions end (below). The gateway mints no presigned capability, delivers no message, and runs no checkpoint or pool recycle scrub for the pod. |
| Deletion | `lenny-label-immutability` (baseline, `failurePolicy: Fail`) | Refuses `DELETE` of a labelled Pod or Sandbox unless the caller is the controller's ServiceAccount after release. |
| Label writes | Same webhook | The gateway and the groups in `forensics.breakGlassGroups` can set the label, and only the controller can remove it. |
| Eviction | Kubernetes eviction API | The per-pod PDB. |

**Retirement paths.** Each existing path was read from §4.6.1, §4.6.3, §4.7.10, §5.2, §6.2, §10.1, §12.8, and §24.11, and the rollout path from [RV-3](runtime-revisions-design.md#rv-3-pods) and [RV-4](runtime-revisions-design.md#rv-4-rollout). Each checks the label or phase before it acts:

| Path | Today | Under quarantine |
| :-- | :-- | :-- |
| Terminal claim disposition, or claim deletion | `claimed → draining → terminated` | The claim holds `quarantined`, and the projection stays at `quarantined`. |
| `recycle.maxSessionsPerPod`, `maxScrubFailures`, `maxPodUptimeSeconds`, and `runtime_not_live` | Retire | Not evaluated. |
| `lenny.dev/drain-request` annotation | Drain | Ignored while the label is present. |
| The node check's drift drain ([PM-6](placement-design.md#pm-6-enforcement)) | The WarmPoolController stamps the drain request on an idle pod whose node no longer matches its placement | Skipped: the WarmPoolController never stamps it on a quarantined pod, as PM-6 states. |
| Slot release at a session's end ([SI-3](session-isolation-design.md#si-3-runtime-processes-slot-release-and-setup-commands)) | The adapter runs End, the Sweep, the cleanup commands, a second End and a second Sweep, and its removal step, which empty the slot's volumes | Not run. The preservation state stops the adapter from ending the runtime or releasing any slot, so the sessions end with their slots' processes and files in place. Without the preservation state, which a quarantine with `preserve: false` or an unacknowledged RPC leaves, the adapter ends the sessions when the coordinator hold times out at 120 seconds and runs each slot's release, so the runbook copies the slot volumes before then. |
| Orphan claim GC (each predicate) | Reclaims a stale claim | The new binding state matches no predicate. |
| Reserved hold expiry | Claim deleted | The hold no longer applies. |
| Unschedulable host retire on the recycle edge | Retire | Not evaluated. |
| Adapter or slot container exit, under **Liveness.** | `observePod` reduces it to `PodFailed`, and the pod retires | The label check precedes that reduction, so the pod stays. `restartPolicy` is `Never`, so the exited container's memory is gone, and the volumes stay while `keepalive` runs. |
| Force-terminate, tenant suspension drain, user invalidation, and erasure drain | Session ends and the pod is released | Session ends, and the release step skips a labelled pod. |
| Tenant deletion Phase 5, and pool deletion | Custom resources (CRDs) removed | The labelled Sandbox is skipped, and the deletion reports it to the forensics reconciler. |
| A runtime rollout's `Contracting` phase ([RV-4](runtime-revisions-design.md#rv-4-rollout)), and the deletion of a pod set whose revision has left traffic ([RV-3](runtime-revisions-design.md#rv-3-pods)) | The old revision's `SandboxWarmPool` and `SandboxTemplate` pair and its pods are deleted | The warm pool controller skips a Sandbox whose pod carries the label, keeps its pair while such a Sandbox exists, and reports it to the forensics reconciler, as for pool deletion (RVR-10 of that design). The rollout completes for every other pod, and the pair goes once the quarantine's release terminates the pod. |
| Node drain | preStop checkpoint, then eviction | The PDB refuses eviction, so the drain waits. |
| Node-pressure eviction | Kubelet evicts | No Lenny control applies, because the kubelet honours neither PodDisruptionBudgets nor the grace period. See "Residual risks". |
| A slot volume over its `sizeLimit` (§4.4.4 **Hard workspace size limit.**, [PL-5](agent-pod-layout-design.md#pl-5-volumes-size-limits-and-eviction)) | `/workspace`, `/session`, and `/tmp` are disk-backed `emptyDir` volumes, each with its own `sizeLimit`. The adapter's per-slot disk watch fails the slot's session at `adapter.diskWatchThresholdPercent` of a limit, 90% by default, and runs its release, and past the limit the kubelet evicts the whole pod. | The preservation state stops the watch's release. The kubelet's eviction has no Lenny control: it kills `keepalive` and removes the volumes, and a running pod's `sizeLimit` cannot change. An unfrozen agent can trigger it at will, which is any pod whose quarantine request set `freeze: false`, whose freeze failed, or whose freeze still waits behind a setup run. Where a project quota bounds the volume ([PL-10](agent-pod-layout-design.md#pl-10-project-quotas)), on runc with user namespaces, gVisor's default mount for `/tmp`, and Kata virtio-fs, the quota's hard limit sits just below `sizeLimit` and a write past it fails with `ENOSPC` or `EDQUOT`, so the slot's own writes cannot reach `sizeLimit` and this route to eviction is closed for that volume. It stays open on the volumes no quota bounds. FE-1 adds a statement to §4.4.4 that this eviction applies to a quarantined pod. |
| Liveness probe | §4.4.3's liveness rule for the `SIGSTOP` checkpoint path, which [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) removes with the path and its `checkpointStuck` flag. The tree renders no liveness probe today. | A freeze stops every process in a slot container, the supervisor included, so a liveness probe on that container would have the kubelet kill it and lose its memory, and a running pod's probes cannot change. Agent pods therefore carry no liveness probe (decision 26), which matches the tree and which no specification rule contradicts once IC-9 lands. Without a probe, a failed adapter on a pod with no quarantine is detected by its container exit, the row on container exit above, or by the gateway's gRPC keepalive on `LNK-POD-GRPC` and the orphan session reconciler, as decision 26 states. |
| Coordinator hold timeout (§10.1) | After `coordinatorHoldTimeoutSeconds` (120 s), the adapter ends sessions and closes the runtime's `CH-MSGSOCK` | The gateway first sends the preservation RPC, proposed as `CH-PRESERVE`, on `LNK-POD-GRPC`. It puts the adapter into a state that never ends the runtime, sends it nothing, and never exits. §10.1's hold state accepts this RPC as well as `CoordinatorFence`. |

### Keeping the volumes

A pod's object existing is not enough to keep its volumes. The kubelet's `SyncTerminatedPod` "cleans up a pod that has terminated (has no running containers)" and waits for volumes to unmount, and the emptyDir plugin's teardown ends with `os.RemoveAll`. With `restartPolicy: Never`, a pod whose regular containers have all exited therefore loses every slot's `/workspace`, `/session`, `/tmp`, `/dev/shm`, and `/run/lenny`, and the adapter's spool ([PL-5](agent-pod-layout-design.md#pl-5-volumes-size-limits-and-eviction)). The removal of data for this exact case is inferred from that source and is untested. A native sidecar, a Kubernetes init container that keeps running beside the regular containers, does not prevent it, because the kubelet stops sidecars after the regular containers, so `netagent` gives no protection.

An agent can therefore destroy the volumes on a pod whose adapter has exited, by ending its own slot container: under gVisor the supervisor, as PID 1, is not immune to SIGKILL from the slot's own processes, as the asynchronous supervisor worker spike of 2026-11-12 found. The pod spec cannot gain a container after creation, and whether a running ephemeral container keeps a pod non-terminal is unverified, so every agent pod carries `keepalive` from creation (decision 25). `agentContainerTerminated` consults only the adapter and slot containers, so `keepalive` does not change retirement of a pod with no quarantine: the controller still retires it by deletion, and `keepalive` exits on SIGTERM. No slot UID can signal it, because it has another UID and another PID namespace. `keepalive` protects against container exit only, and an eviction or a compromised guest or Sentry removes it with everything else.

### Freezing the agent

Quarantine cuts the agent's reach, and the runtime process keeps running. §10.1 states that in hold state the runtime "continues running". It can overwrite its workspace, delete files, and scrub its memory. A freeze follows the acknowledged preservation RPC (decision 22):

- **In the pod.** After acknowledging `CH-PRESERVE`, the adapter sends `freeze` on each slot's `CH-SUPERVISE`, and each slot's supervisor sends SIGSTOP to every other process in its slot container's PID namespace. The supervisor keeps answering the adapter. A quarantine acts on the whole pod, so every slot is frozen, although the protocol can freeze one session's slot alone. The freeze is automatic, and it is best effort, because §4.7.11 item 8 places the supervisor on the untrusted side. Under gVisor the supervisor, as PID 1, is not immune to SIGSTOP or SIGKILL from the slot's own processes, as the asynchronous supervisor worker spike of 2026-11-12 found, so an agent that stops its supervisor first leaves a slot that answers no `freeze`. The adapter's liveness deadline on `CH-SUPERVISE` is the only in-pod signal of it, and the node-side freeze below reaches such a slot.
- **Behind a setup run.** A slot's supervisor runs at most one setup command or `lenny-materialize` run at a time, and during a run it answers `freeze` with `worker_busy` ([WM-3](workspace-materialization-design.md#wm-3-the-protocol)). When a slot has a run outstanding, the adapter cancels it by closing its end of the run's cancel pipe, waits for `setupdone`, and then sends `freeze`. The delay is bounded by the run's `timeoutMs` plus the supervisor's 5-second status wait. The cancellation kills the run's process group, so a running setup command is lost from memory. An investigator who needs it sets `freeze: false` and freezes from the node, which reaches the run's processes without the supervisor.
- **From the node.** The FE-5 capture Job freezes each slot container: `runsc pause <id>` on gVisor (the subcommand exists in `runsc/cmd/pause.go`, and its behaviour on a Lenny pod is untested) and `cgroup.freeze` on runc, whose kernel documentation states that a frozen process stays halted and can still be killed by a fatal signal. No Kata method has been identified.

The freeze slightly alters what it preserves: SIGSTOP changes process state and sends SIGCHLD to parents. Whether a frozen process can then be checkpointed is a validation item. An investigator who needs the agent observed live sets `freeze: false` on the request.

**Slot-reported evidence.** A `lenny-materialize` run's result and the log pipes of a setup command or a helper run are written by processes at the slot UID, and the supervisor that relays them sits on the untrusted side. A case records them as slot-reported evidence, with provenance `self_reported`, as it records the setup output under FE-6.

### Sessions on the pod

`sessionDisposition: fail` writes `failed` with the existing reason `orphan_pod_terminated` (§10.1), so the client sees the failure a lost pod produces, and the true cause sits only in the platform-tenant row (FP3). The handler forces no auto-retry, and §7.3 treats a reason on neither retry list as non-retryable. Reporting an accurate but generic reason is part of decision 6, under which the tenant receives no signal of a quarantine or a hold, and disclosure is a recorded case action. The session's proxy token is revoked when the session ends or its pod is released, which at once stops LLM access through the gateway's LLM proxy, and the pod holds no LLM provider credential. A token already delivered to the pod, such as a `userCredential` file, stays usable until it is revoked or expires, which step 7 of FE-5's capture order and "Delivered user tokens" under FE-6 cover.

`sessionDisposition: resume` uses the `pod_evicted` path, the retryable failure under which the gateway restores the session from its last checkpoint on a replacement pod (§7.3), so the client sees a recovery on a replacement pod and no failure. The default is `fail`, because a resume restores the session from a checkpoint the compromised pod may have written, and it continues any injected instruction in the transcript. An investigator chooses `resume` when the suspect is the pod rather than the session.

The API returns every session on the pod. A concurrent-slot pod can hold several sessions of one tenant, because §5.2 forbids cross-tenant slot sharing, and all of them end. A warm pod with no claim is quarantined through the label alone. `includeDelegationTree` resolves the pods of the session's tree, and each pod gets its own quarantine under the same case.

### Order and failure behaviour

1. The handler commits the row and the custody event in one step. A failure returns `503`, and nothing has changed. It then pushes the binding change, so egress, mints, and mirror reads stop through the row while the network path to the adapter stays open.
2. Unless `preserve: false`, it sends `CH-PRESERVE` and waits for the acknowledgement, bounded by `forensics.quarantine.preserveTimeout` (5 seconds). The adapter then starts the freeze, after cancelling any setup run outstanding on a slot.
3. It stamps the label, which cuts the network and removes the pod from inventory, and it sets the claim phase. The label follows the RPC, because the RPC needs `allow-gateway-ingress`, and because a pod cut off first enters hold state and closes `CH-MSGSOCK` at 120 seconds.
4. It ends the sessions.
5. The forensics reconciler creates the PDB and retries any incomplete step, and it reports per-step status on the row.

| Failure | Behaviour |
| :-- | :-- |
| `CH-PRESERVE` unacknowledged at the timeout | The sequence continues, and `preserve: unacknowledged` is reported. The runtime may lose `CH-MSGSOCK` when the coordinator hold times out, so the runbook captures memory at once. `keepalive` still protects the volumes. |
| Freeze fails or is unavailable | `freeze: failed` or `unavailable` is reported, and the capture Job freezes from the node. |
| Postgres is down | The API refuses. A cluster admin in `forensics.breakGlassGroups` can stamp the label with `kubectl`, which denies egress, mints, and network paths at once. The webhook logs the requesting user. Until Postgres returns, `GET /v1/admin/quarantines` reports the pod as `unregistered`. When Postgres returns, the forensics reconciler creates the row with `origin: break_glass` and a custody event (decision 27), and the Kubernetes audit log is the record of who stamped it. |
| API server is down | The row is committed and the label step retries. `pkg/binding`'s store re-read applies the state within its 30-second bound. The informers serve their last cache. |
| Webhook unavailable | Every labelled write and delete is refused, which is the fail-closed direction. |
| Pod already terminating | The API returns `409 QUARANTINE_NO_POD`. |
| Node lost, or kubelet evicts | The forensics reconciler records `forensics.evidence_lost` with what remains (registered captures, evidence streams, and held artifacts) and leaves the row visible. |
| Quarantine ages past `forensics.quarantine.alertAfter` | The `QuarantineAged` alert fires, naming each pod, its node, and its case, and `lenny_quarantined_pods` reports the count by age. Nothing is released automatically (decision 11). |

### Cost, quota, and billing

A quarantined pod consumes node capacity and a slot in the namespace `ResourceQuota`. The pool replaces it, because it no longer counts as warm. `pod_minutes` for a session stops at the terminal transition, so the tenant is not billed for retention and quota does not reveal it. `lenny_quarantined_pods` (labelled by pool and without a tenant label on the public dashboard) and `forensics.quarantine.maxPods` give the operator a warning threshold, which alerts and never refuses.

## FE-2: Mirror store hold

### API

| Method and path | Behaviour |
| :-- | :-- |
| `GET /v1/admin/mirror/stores` | Filters: `tenantId`, `upstream`, `kind` (`public` or `tenant`), and `held`. Returns store identity, generation, replica, volume, and last fetch. |
| `POST /v1/admin/mirror/stores/{id}/hold` | Body: `caseId`, `basis`, `basisRef`, and `note`. It writes a `security_holds` row with `scope.kind: mirror_store`, so the basis rules in FE-3 apply. Returns the sealed generation, the snapshot reference, and the flag state. |
| `POST /v1/admin/mirror/stores/{id}/release` | Body: `justification`. Clears the flag and allows repack and purge. |

### Mechanism

- **Flag.** The gateway sets the held flag in the store index ([EG-5](egress-and-git-design.md#proposal-split), the egress design's git mirror proposal) on the mirror's parent, authenticated by the gateway's identity. The flag stops the sweep's re-chown, repacks, and purge, as the held-flag hook in [`egress-and-git-design.md`, "Reserved for other designs"](egress-and-git-design.md#reserved-for-other-designs) states. It seals the store's current generation: no fetch or `upload-pack` touches it once the in-flight fetch has ended and its sweep has run. The re-chown is skipped so that on-disk owners keep saying which fetch UID wrote each file.
- **Fetch UIDs.** The egress design returns a fetch UID to the pool only after the sweep and the re-chown. A skipped re-chown leaves files owned by that UID, and a public store's directories are mode `2775`, so a later fetch reusing that UID could traverse and write them. The UIDs that own files in a held store therefore stay out of the pool until release, and the index persists that set so a restart honours it, as the hook states. The range bounds concurrent fetches, so `gitMirror.heldUidBudget` caps how many UIDs held stores may retire, and a hold past the cap re-chowns after recording an ownership manifest, a record of each file's numeric owner and mode. The owner settled this exception on 2026-11-09. The egress design's held-flag hook and the **Reserved hooks.** test of its tier 4 tests state today that a held store is skipped by every re-chown, so this design requests that both adopt the budgeted exception (see "Changes requested of egress-and-git-design.md").
- **Snapshot.** The forensics reconciler creates a CSI `VolumeSnapshot` of the replica's volume, after the flag is acknowledged or after 10 seconds, whichever comes first, using a snapshot class with `deletionPolicy: Retain`. The snapshot does not depend on the parent, so a compromised parent cannot withhold it. The controller gains `create` and `get` on `volumesnapshots` in the mirror's namespace, and no `delete`.
- **Extraction.** A volume holds several stores, including other tenants', so the snapshot is an interim artifact. The FE-5 procedure mounts a restored copy in a job, archives only the held store with numeric owners, modes, and ACLs, hashes each pack, and registers the archive as a capture. The snapshot is then deleted through the cluster admin's rights. A deployment without CSI snapshots skips this step, and the archive runs against the live store while the flag holds, which a compromised parent can affect. `gitMirror.forensics.volumeSnapshotClass` selects the behaviour.
- **Continued service.** Every hold moves service to a fresh generation under the same store identity, using the hook's on-disk generation, and the mirror fills it from the upstream on the next request. A frozen store that kept serving would stop refreshing, so the egress design's read-after-push wait, in which `netagent` holds a pod's next fetch of a repository after a push until the mirror refreshes that repository, could not complete and tenants would see stale refs. Serving a sealed generation would also keep a poisoned store in use. A hold therefore costs one refetch, and tenants see a slower first fetch and no failure, including on a public store. Read-after-push targets the current generation.
- **Tenant view.** Held bytes are excluded from the per-tenant mirror storage quota ([P7](egress-and-git-design.md#security-properties)), and neither the quota read nor the prefetch API exposes the flag (FP3).
- **Tenant deletion.** The §12.8 mirror purge skips a store whose index or `security_holds` row says it is held, and it fails closed when it cannot read them. A tenant store held under a deferring basis keeps its generation through teardown until release, and one under any other basis is purged with a `forensics.evidence_lost` event. A public store holds no tenant's data, so purge does not reach it. The tenant-readable receipt follows the rule in FE-3, and the platform-tenant case record lists the store.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Mirror unreachable, or the replica lost | The row stays `pending`, the snapshot proceeds, and the forensics reconciler retries the flag with an alert. Purge reads the row, so it cannot run in the gap. |
| Snapshot fails or no class is configured | The hold stands, `snapshot: unavailable` is reported, and the case shows it. |
| Compromised parent ignores or forges the flag | The snapshot and the fetch records (`git.operation`, `enforced` from the mirror) are the evidence. The limit is stated under "Residual risks". |
| Volume full while held | The mirror's capacity alert fires, a held generation is never deleted to make room, and fresh generations fail over to the `direct` route, which fetches from the upstream host without the mirror, when the volume cannot hold them. |

## FE-3: Platform-only holds and a security retention class

### Security holds

A security hold is a separate object from the §12.8 `legal_hold` flag. The tenant-visible legal hold keeps its current behaviour for a tenant's own e-discovery.

| Method and path | Behaviour |
| :-- | :-- |
| `POST /v1/admin/security-holds` | Body: `caseId`, `tenantId`, `scope`, `include`, `basis`, `basisRef`, `note`, and `reviewAt`. Returns `202` with the identifier and `materializing`. |
| `GET /v1/admin/security-holds` | Filters: `caseId`, `tenantId`, and `state`. Requires `forensics.hold.write` or `forensics.query`. Returns item counts and materialization progress. |
| `POST /v1/admin/security-holds/{id}/release` | Body: `justification`. Runs deferred erasures (below). |

`scope.kind` is `session` (with `sessionIds`), `user` (with `userId`), `pod` (with `podUids`), `tenant` (the tenant), or `mirror_store` (written by FE-2). Each takes `from` and an optional `to`. An absent `to` keeps holding new records until release. `include` lists `audit`, `evidence` (with an optional list of streams), `artifacts`, `checkpoints`, and `transcripts`. A `tenant` scope accepts `audit` and `evidence` only, because copying every artifact of a tenant is unbounded, so artifacts need named sessions or a user.

| `security_holds` field | Meaning |
| :-- | :-- |
| Identifier, case, tenant, region | Regional platform Postgres, following CMP-058 |
| Scope, include, and range | As above |
| State | `materializing`, `active`, `failed`, or `released` |
| Items | Per-class counts, escrow object keys, and digests |
| `basis` and `basisRef` | The enumerated ground for retention and the reference to its legal approval (below) |
| `auditCursor` | The last `sequence_number` copied, per tenant |
| Set and release principals, note, and `reviewAt` | `HoldReviewDue` alerts at `reviewAt`, and nothing expires automatically |
| Pending erasures | Erasure jobs and users deferred by this hold |

### Copy on hold

The tenant's data is copied or marked so that nothing in the tenant's own stores changes. The alternative is an in-place flag like `legal_hold`, which alters checkpoint rotation, counts against `storageQuotaBytes`, and raises `CheckpointStorageHigh`, each of which a tenant can observe (decision 18).

| Class | Mechanism | Retention |
| :-- | :-- | :-- |
| `artifacts`, `checkpoints`, and `transcripts` | Copied to `security-hold/{tenant}/{case}/...` in the region's escrow bucket, using the existing `storage.regions.<region>.legalHoldEscrow` entry and a new `securityHoldKekId`. FE-3 reuses `pkg/legalholdescrow`'s read and re-encrypt path and keeps its own records (below). The originals follow normal rotation and deletion. Under [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md), a checkpoint is a snapshot whose content sits in shared packs, and [IC-6](incremental-checkpoint-design.md#ic-6-retention-garbage-collection-erasure-and-legal-hold), "Security holds", defines its copy as every pack the snapshot references, with the snapshot root recorded among the hold's items. `artifacts` also covers the output files of [`message-delivery-design.md`, "MD-18 Files"](message-delivery-design.md#md-18-files), the files an agent produces for the user, which are stored as chunks in the same packs: the hold copies every pack an output file's chunk list references and records its `output_files` row among the hold's items. Both copies include every platform pack the snapshot or the output file references ("Platform packs" below). | Until release |
| `audit` | A contiguous segment of the tenant's chain for the time range, as canonical tuples with `prev_hash`, `sequence_number`, and `event_schema_version`, plus the tuple before the range. Filtering would break the per-tenant chain, and §12.8 step 13 states that the table has no `user_id` or `session_id` column, so every scope copies every row of the tenant in range, and a reader filters afterwards. Anchor events for held evidence fall in that segment, and a release cannot leave them behind. | Until release |
| `evidence` | `lenny-evidence` sets the held marker on every manifest row in range, and on new rows while `to` is absent, and reports the rows to the hold. A release recomputes the marker from the remaining holds, since several holds can cover one row. | Until release, after which normal stream retention applies |

**Separate records.** Clearing a session's legal hold deletes every escrow object recorded for that session, whether or not a legal hold was ever set (F-12.8.64, a defect filed in `BUILD-GAPS.md`, see "Defects found while verifying"). In the code, `POST /v1/admin/legal-hold` with `hold: false` calls `ReleaseForSession`, which lists `Records.ActiveForSession` and deletes each object, and the handler returns `escrowReleased` when the count is above zero. Security copies therefore never enter those records. They live in `security_hold_items` under the hold, in their own prefix and under their own KEK, and `Releaser` has no path to them, so a tenant admin can neither delete one nor learn of one through that count.

**Platform packs.** A held snapshot or output file can reference packs of a platform chunk set rather than holding those chunks in its own packs ([IC-4, "Platform chunk sets"](incremental-checkpoint-design.md#platform-chunk-sets)). The escrow copy is incomplete without them, so a hold copies every platform pack a held item references into the hold's escrow prefix, re-encrypted under `securityHoldKekId` as the item's own packs are, and the copy references no pack outside that prefix ([IC-6](incremental-checkpoint-design.md#ic-6-retention-garbage-collection-erasure-and-legal-hold), "Platform chunk sets") **[design]**. A platform pack holds one source's chunks, so the copy brings in content of a warm repository or a runtime's assets that the held session could read, and nothing of another tenant. The §12.8 legal-hold escrow on a forced tenant deletion copies the platform packs of a held item in the same way, under its own records. A platform pack is shared across sessions and often across tenants, so pack reclamation, the operator's deletion of a set's packs when content must be purged, and `DeleteByTenant` of a tenant-owned set each call `EnsureCopied` against the hold index before they soft-delete a platform pack row, and a copy failure refuses the deletion, which the deleter retries. An operator who purges a source's content, such as a secret committed to a warm-list repository, therefore leaves the escrowed copy in place under the hold, and the case records that the escrow holds that content. A platform contribution records the pod that added it, so an investigation of a poisoned platform pack traces the pack to that pod. Held platform packs are charged to the platform, as held bytes are under decision 16.

**Open-ended holds and retention races.** The handler marks manifest rows and starts the copy before it returns, and the sweeper and compactor read the marker in the transaction that deletes or merges. While a hold is active, a worker continues copying:

- **Audit.** At `forensics.holds.auditCopyInterval` (5 minutes), the worker extends the segment for an open range and advances `auditCursor`. The §16.4 partition GC gains a second guard: it holds a partition containing any tenant row in a hold's range above that cursor, and it emits `AuditPartitionDropBlocked` with `reason: security_hold`. `POST /v1/admin/audit-partitions/{partition}/drop` refuses that partition while the guard is active. Once the tuples are written the partition can drop, so a hold does not retain other tenants' rows past their retention.
- **Artifacts.** The worker enumerates new artifacts, output files among them, checkpoints, and transcripts for named sessions and users. Every path that deletes one (the GC, checkpoint rotation, checkpoint pack reclamation under IC-6, the reclamation or purge of a platform pack, the deletion of an output file at the end of the session's retention or after a `PostAgentOutput` `REJECT`, `DeleteByUser`, and `DeleteByTenant`) first calls `EnsureCopied` against the hold index, which closes the interval between the worker's runs. A copy failure refuses the deletion, and the deleter retries.
- **Teardown.** Phase 3.5 flushes the audit segment to the tail and runs `EnsureCopied` for every in-scope item before Phase 4, because the audit eraser has no escrow skip and no later rows exist to copy afterwards.

**The §12.8 ledger.** Security holds stay outside the legal-hold ledger. Step 0(c) and Phase 3.5(c) block on `audit_range` and `workspace_snapshot` entries and return 409s that list holds, so placing security holds there would both block erasure and reveal them. Nothing sets those two types today, so §12.8 and §15.1 remove them, the `ERASURE_BLOCKED_BY_LEGAL_HOLD` and `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` text narrows to sessions and artifacts, and the tripwire test on the setter stays as it is. The tenant deletion controller reads `security_holds` directly, in the separate step under "Interaction with erasure and teardown". This resolves the gap the outline named by removing the unused ledger types.

**Tenant visibility.** `GET /v1/admin/legal-holds` excludes these holds for every caller, and `lenny_tenant_legal_hold_active_count` does not count them. Tenant reads of a stream apply the stream's normal retention window, so a held row past that window reads as expired. Held bytes are charged to the platform and excluded from `storageQuotaBytes`. Tenant reads of a quarantined pod's later records are covered under "Records after a quarantine".

### Interaction with erasure and teardown

**`DeleteByUser`.** Step 0 does not block on a security hold, because a `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` lists holds to a tenant-admin caller. The job runs and deletes the originals in the tenant's stores. A hold whose `basis` is in `forensics.holds.deferErasureBases` keeps its escrow copies and held evidence, skips destroying the per-session key, and adds the job to its pending erasures. At release, the worker deletes the copies, destroys the keys unless another hold covers them, and writes a `gdpr.erasure_deferred_completed` platform-tenant event. A hold with any other `basis` does not defer: the job deletes its in-scope copies and destroys the keys, and a `forensics.evidence_lost` event tells the investigator.

**Basis.** `basis` is `legal_obligation`, `legal_claims`, `security_incident_response`, or `customer_instruction`, and `basisRef` names the approval, which the design does not supply. Article 17(3)(b) and (e) of the GDPR name legal obligations and legal claims as exceptions to the right to erasure. GDPR Recital 49 describes network and information security as a legitimate interest and is not a 17(3) exception, so `security_incident_response` is outside the default `deferErasureBases`. §11.8 states that Lenny operates as a data processor, so retaining against a controller's instruction needs the operator's own ground, and which ground fits an investigation is each operator's own legal assessment, on which this design makes no claim (decision 24). A hold cannot be created without both fields.

**Receipt.** The `legal_basis` enum in §12.8 belongs to the `RedactionReceipt`, the signed record of a redaction of a dead-lettered audit row, so this design leaves it alone. The tenant-readable `gdpr.erasure_completed` event and job status gain a `retentionStatement` field, which `forensics.erasure.retentionStatement` controls (decision 7). Under `generic`, the default, every receipt carries the same text, `forensics.erasure.retentionStatementText`, whether or not anything was retained, so the text is never false and reveals nothing about a case. The default text is "Records may be retained where a security or legal hold applies, as the operator's agreement permits." Under `accurate`, a receipt states whether items were retained under a hold, with their categories and count, and names no case or basis. The retained items are recorded in the platform-tenant case either way. A dead-lettered row in a held audit segment keeps its unredacted copy under the same deferral.

**Tenant deletion.** Phase 3.5 enumerates security holds and does not block on them, since the originals are already segregated. `deferErasureBases` applies here as it does under `DeleteByUser`, because a basis that is no ground to retain against an erasure request is no stronger against a deletion: a hold outside the list loses its copies, its keys, and any held store, with a `forensics.evidence_lost` event, and offboarding evidence on that basis then rests on the retention class. For a hold in the list, it waits for any hold still `materializing`, and it escrows held evidence: `EvidenceStore` moves marked batches to the escrow prefix, unwraps per-session keys under the tenant KEK, and rewraps them under the escrow KEK before Phase 4a destroys the tenant key. Batch bytes do not change, so anchored digests still verify. The audit segment carries the anchors the Phase 4 audit eraser deletes. The Phase 6 receipt keeps its normal fields, and the case records what was retained. If materialization cannot finish before the §12.8 deadline, the phase blocks fail closed, and the existing `TenantDeletionOverdue` alert fires on the operator side with no tenant-visible reason.

**Restore.** The post-restore reconciler is the Job that replays erasure receipts after a database restore, so that erased data stays erased. Its ledger gate (§25.11 `confirm-legal-hold-ledger`) gains `security_holds`, so a restored database does not replay an erasure that a hold deferred.

### Security retention class

A stream can carry `retentionClass: security` at the deployment level. For those streams, tenant teardown moves batches inside the stream's retention window into the escrow prefix, where they stay for `forensics.retention.afterTeardownDays`, regardless of any hold. The class exists because a tenant that offboards after a compromise otherwise removes the evidence (R4), and [the egress design](egress-and-git-design.md) states that tenant deletion removes these streams today. The escrow operation of the egress design's `EvidenceStore` moves only batches that carry the held marker ([`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams), **Erasure and holds.**), and a retention-class batch outside any hold carries none. This design therefore requests that `EvidenceStore.DeleteByTenant` also move an unmarked batch of a stream with `retentionClass: security` that falls inside the stream's retention window, and rewrap its keys, by the same operation (see "Changes requested of egress-and-git-design.md").

The recommended members are `credential.mint`, `egress.decision`, `egress.tunnel`, and `git.operation`, because they carry session identifiers and no content. `agent.activity.*` stays out unless a hold covers it, because of its sensitivity. The chart default is 0 days, and a SaaS operator sets a value that its customer agreement states (decision 8). The design makes no claim that this retention meets an Article 17(3) exception. Recital 49 is one argument an operator can weigh, limited to what is strictly necessary and proportionate. §12.9 gains a row for retained evidence.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Region has no escrow entry | `SECURITY_HOLD_REGION_UNRESOLVABLE`, with the same fail-closed rule as `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`, and `DataResidencyViolationAttempt`. |
| A source object is missing | The hold continues, and the item is listed as `missing` with a `forensics.evidence_lost` event. This differs from the §12.8 legal-hold override path, which migrates held resources to escrow on a forced tenant deletion and treats a missing source as a hard error, because a hold has to preserve whatever remains. |
| `lenny-evidence` is unavailable | The hold stays `materializing`, retries, and alerts. The sweeper's marker read fails closed, so nothing is deleted under a read error. |
| Compromised `lenny_erasure` role or a database superuser | The escrow bucket and the SIEM copy are outside that role. §11.7 states the superuser limit, which still applies. |

## FE-4: Reconciler

### Independent sources

A compromised decision service writes `egress.decision` and receives Envoy's access log stream, and it supplies the tags Envoy logs. A reconciler reading only those records would compare one component with itself. Okta's root cause report shows the general risk: scoping missed activity that produced "an entirely different log event", so this design compares sources rather than trusting one. The sources below are outside its control, and the evidence reconciler uses a tag from a suspect component only to look up a record, never to attribute one (FP6).

| Source | What it measures | Independent of | Status |
| :-- | :-- | :-- | :-- |
| Envoy-measured fields, sent by a second access logger directly to `lenny-evidence` | Downstream peer address, CONNECT authority, upstream address and source port, times, bytes, and the Envoy replica | The decision service | `egress.accesslog`, from [EG-10](egress-and-git-design.md#proposal-split) |
| `credential.mint` from the Token Service | Each mint, with its own `pkg/binding` read | The decision service | From [EG-2](egress-and-git-design.md#proposal-split) |
| `git.operation` from the mirror | Fetches through the mirror, with its own binding read | The decision service | From [EG-5](egress-and-git-design.md#proposal-split) |
| `pod.address`, from the controller's watch | Pod UID, addresses, node, tenant label, and egress profile over time, which resolves a peer address at a time to a pod and names the pods that bypass Envoy | The decision service and `netagent` | New stream, provenance `observed` |
| `agent.activity.exec` | Processes and their loopback connections | The agent and the runtime | FE-6b |
| Cloud NAT or VPC flow logs, deployer supplied | Destination, source address, port, and bytes at the cluster edge | Every Lenny component | Optional import from an object store prefix |
| Provider audit logs (for example a git host's), deployer supplied | Use of a token, by the token hash or `jti` in `credential.mint` | Every Lenny component | Optional import |
| Kubernetes audit log, deployer supplied | TokenRequest, exec, and ephemeral container writes | Every Lenny component | Optional import |

### Rules

| Rule | Compares | Disagreement means |
| :-- | :-- | :-- |
| R-TUNNEL-NO-DECISION | `egress.accesslog` with `egress.decision` | A tunnel exists with no matching decision record, so records were dropped or a decision was forged. |
| R-ATTRIBUTION | The decision's pod and tenant with the pod owning the access log's peer address in `pod.address` | A decision or a tag named the wrong pod or tenant. Critical. |
| R-TUNNEL-AFTER-END | Tunnel end with the end of the pod's binding to its session, from `pod.address` and the session row | A tunnel outlived the pod's binding to its session past the grace. This is the visible sign of a compromised `netagent`. |
| R-QUARANTINE-BREACH | Any allow, mint, or mirror read after a quarantine's set time plus the 30-second bound of the `pkg/binding` store re-read | An enforcement point did not apply the state. Critical. |
| R-MINT-NO-SCOPE | `credential.mint` with the decision service's scope decisions, its answers to the Token Service's scope check before each mint | One mint check lacks a record of the other. |
| R-MINT-WRONG-TENANT | The tenant in `credential.mint` with the tenant that owns the minted credential binding | A tenant's credential reached another tenant's pod ([P4](egress-and-git-design.md#security-properties)). Critical. |
| R-GIT-CLAIM | `netagent`'s `git.operation` with the mirror's record, the access log, and any provider log | A self-reported operation has no enforced counterpart, or the reverse. Git on a host that is not a covered host is an opaque tunnel and writes no `git.operation`, so a missing record there does not mean no push, and `egress.tunnel` is the only trace. |
| R-LOCAL-CLAIM | `egress.local` tunnel closes with the access log's end times | `netagent` reported a close that Envoy never saw. |
| R-SEQUENCE | Sequence gaps, incarnations without a closing record, compaction anchors, and §11.7 chain state | Loss or tampering, using the detection [the egress design](egress-and-git-design.md) specifies. A gap alone is a low-severity finding. |
| R-EDGE | The access log's byte counts with NAT or flow logs, per replica and time bucket | Traffic left the cluster outside the proxy, or the proxy under-reported. |
| R-ACTIVITY | `agent.activity.tool` commands with `agent.activity.exec` | A reported command with no process, or a process with no tool call in its ancestry. Informational, because scripts legitimately spawn children. |

A lossy input produces low-severity findings on its own, because `egress.tunnel` can lack close records during an Envoy crash. A rule raises severity when two independent sources agree against a third. At level 1, Envoy is absent, so `egress.accesslog` is empty and the rules that read it do not run. `egress.decision` is `self_reported` there, the deployer's proxy log is an imported source, and `coverage` on a finding states this. The raw sockets of a pool with `egressProfile: internet` also pass no proxy, so only R-EDGE and `connect` records (the `kind: connect` records of `agent.activity.exec`, FE-6) observe them.

### Component, API, and failure behaviour

`lenny-evidence-reconcile` is a Deployment in `lenny-system` with its own identity: read-only rights on the evidence bucket, a read-only manifest role, decrypt rights on the regional evidence key and on the tenant keys of its region, because a session's data key is wrapped by the tenant key while its state is `active` ("Keys" under FE-6), with a producer SAN (the subject alternative name in its certificate, which ingest binds to the streams it may write) allowed to write one stream. It carries required anti-affinity against the decision service, for the reason the egress design gives for the mirror: a container escape from one does not land on the node of a component that holds policy. It runs each tenant-hour after compaction, on a staggered schedule, over `egress.*`, `credential.mint`, and `git.operation`, and it scans objects, so its cost grows with evidence size. `POST /v1/admin/evidence/reconcile` runs a case's range on demand, including activity streams and imported sources.

Findings go to the new stream `evidence.reconcile`, with provenance `observed`, under the platform prefix when a finding spans tenants. `GET /v1/admin/evidence/reconcile/findings` reads them and requires `forensics.query`. Critical findings raise `EvidenceDisagreement`, and `ReconcilerStale` fires when a tenant-hour has gone unprocessed past a bound. Evidence reconciler failure affects no traffic. A compromised evidence reconciler can suppress findings, and the deployer's SIEM copy of the underlying streams ([SO-3](security-oversight-design.md#so-3-per-tenant-siem-destination), the per-tenant SIEM destination) is the check on it. A compromised `lenny-evidence` can drop any stream before it is anchored, which the imported sources and the SIEM copy reveal when they are configured.

## FE-5: Capture runbook and abuse workflow

### Capture commands and storage

The design adds no standing privileged Lenny component for capture (decision 13). The chart ships an unbound ClusterRole, `lenny-forensics-capture`, holding `nodes/checkpoint`, `get` on `pods` in agent namespaces, and `create` on Jobs in a `lenny-forensics` namespace, for a deployer's privileged access workflow to bind for the duration of a case. The Kubernetes documentation warns that `nodes/proxy` authorizes executing commands in any container on the node, and it maps `/checkpoint/*` to `nodes/checkpoint`, so the role never holds `nodes/proxy`. The capture Job runs privileged on the target node, under `lenny-forensics` admission rules separate from those of agent namespaces, using a digest-pinned image Lenny publishes. The namespace carries the Pod Security level `privileged`, which [PM-9](placement-design.md#pm-9-namespaces) records. The Job is a named node agent of [PM-5](placement-design.md#pm-5-rendering): it selects the target pod's node by `nodeName` and tolerates that node's taints, and `lenny-pod-security` admits those tolerations only for a Job in `lenny-forensics` with the capture component label.

| Command | API | Behaviour |
| :-- | :-- | :-- |
| `lenny-ctl admin forensics case open\|close\|get` | `POST /v1/admin/cases`, and related | Case record |
| `lenny-ctl admin forensics capture register` | `POST /v1/admin/cases/{id}/captures` | Records the pod, kind, collector, tool version, digest, size, and time, and returns a gateway-minted presigned `PUT` with a single key, a single method, an exact `Content-Length`, and a short expiry, on the rule that NET-071 sets. NET-071 is the specification's requirement under which a pod moves checkpoint data to object storage only through gateway-minted presigned URLs (§13.1 and §13.2.2). |
| `lenny-ctl admin forensics capture get` | `GET /v1/admin/cases/{id}/captures/{captureId}` | Requires `forensics.capture.read`, returns a presigned `GET`, and writes a custody event. |
| `lenny-ctl admin forensics attribute` | The cross-tenant query of [`egress-and-git-design.md`, "Evidence streams"](egress-and-git-design.md#evidence-streams) | Takes address, port, target, and time, and returns candidates (below). |
| `lenny-ctl admin forensics export` | `POST /v1/admin/cases/{id}/export` | Writes a bundle manifest of captures, held items, digests, anchors, and custody events, and `lenny-ctl admin forensics verify` recomputes them. |

### Capture order

The runbook `docs/runbooks/forensic-capture.md` follows the §25.7 format, and the order follows RFC 3227: memory and process state first, then temporary file systems, then disk, then remote logs. RFC 3227 also warns against shutting a system down before collection, because evidence is lost. Quarantine comes first, in tension with that order, since quarantining changes network state. The runbook takes quarantine first because stopping an active exfiltration outweighs the changed network state, and it records the pod's network state before the label where the operator has time.

1. Open a case. Quarantine the pod, and read `preserve` and `freeze` from its status. If `lenny-ctl` is unavailable, use the break-glass label, which reaches the adapter with no RPC, so the runtime may lose its socket after 120 seconds.
2. Set a security hold covering the tenant and range as soon as a basis is approved. Quarantine does not wait for it, and normal retention applies to anything outside the pod until it lands.
3. Freeze from the node where the in-pod freeze failed, was declined, or still waits behind a setup run, then capture memory and process state (table below). Each archive contains every memory page of the checkpointed processes, including keys (Kubernetes warning), so the Job encrypts it to the case's public key before it leaves the node and removes the node copy.
4. For each slot, copy `/dev/shm`, `/tmp`, and `/run/lenny` (the manifest and the credential file), then `/session` and `/workspace`, then container logs. Setup and helper output, which the adapter relays from the slot's log pipes, is slot-reported evidence wherever a capture includes it. The paths are those of the slot container `runtime-<n>` ([PL-3](agent-pod-layout-design.md#pl-3-runtime-visible-filesystem-layout)), and the Job reads each slot's volumes (`slot-<n>-dshm`, `slot-<n>-tmp`, `slot-<n>-run`, `slot-<n>-session`, and `slot-<n>-workspace`) from the node. It does not use the slot's file-descriptor broker ([SI-5](session-isolation-design.md#si-5-the-supervisor-file-descriptor-broker)), because a capture must not depend on a supervisor that sits on the untrusted side. The Job hashes each file.
5. Register each capture.
6. Reset open tunnels if R-TUNNEL-AFTER-END fires or `netagent` is suspect. Envoy has no per-tunnel kill, so the procedure runs the chart's graceful drain against the replicas named in `egress.accesslog`, which interrupts other tenants' tunnels on those replicas until they reconnect. On a shared replica set, an investigator weighs that against the pool's remaining tunnel cap.
7. Rotate credentials. `credential.mint` lists every token and secret version the pod received. The pod holds no LLM provider credential: the gateway holds each upstream's credential, and the session's proxy token is revoked when the session ends or the pod is released. A provider key that the runtime held itself and used outside the proxy is revoked at the provider by whoever issued it. For a `userCredential` binding, follow the limits under "Delivered user tokens": remove the user's link for a token from an `oauth2Link` source or a child token derived from one, which revokes its grant when its token trust record still matches, and for a token from a `registered` source act on the exposure rows through the tenant's backend. Changing a token trust record because it was compromised is not enough, because restoring the earlier record makes its sealed tokens usable again, so the case revokes at the provider or deletes the rows.
8. If a container escape is suspected, cordon the node and keep the pods running, so evidence is preserved before workloads move, as Wiz's guidance states. Drain only after collection.
9. Release the quarantine.

Memory capture by isolation profile, each marked as untested here:

| Profile | Method | What the sources establish | Validation owed |
| :-- | :-- | :-- | :-- |
| `standard` (runc) | Kubelet `POST /checkpoint/{namespace}/{pod}/{container}`, per container. | Beta and on by default since Kubernetes 1.30. Returns 500 when the container runtime behind the kubelet's Container Runtime Interface (CRI) does not implement it. KEP-2008, the Kubernetes enhancement proposal for container checkpointing, states the container continues without knowing. CRI-O implements it, and containerd's CRI plugin has a handler that requires CRIU (Checkpoint/Restore In Userspace) on the node. | Run on the deployment's runtime version. |
| `sandboxed` (gVisor, the default) | `runsc checkpoint --leave-running --image-path=<path> <id>` from a Job on the node. | The gVisor guide documents raw `runsc` and Docker usage and says containers stop after a checkpoint unless `--leave-running` is set. It does not cover Kubernetes. | Whether the CRI path works, how to locate the sandbox root on containerd, and which containers in the pod the sandbox checkpoint covers. |
| `microvm` (Kata) | Hypervisor-level guest memory dump. | No source found documents a procedure. An old copy of Kata's limitations page states that the runtime provides no checkpoint and restore commands. | The whole method, per hypervisor. |

An attempt that fails falls back to step 4. `kubectl debug` is a poor substitute for memory capture, because `lenny-ephemeral-container-cred-guard` blocks the identities that could reach credentials, and adding a container changes the pod.

### Abuse workflow

`docs/runbooks/abuse-report.md`:

1. Record the report in a case: source address, port when given, destination, time, and the reporter's clock source. RFC 7422 states that identifying a subscriber behind a shared address depends on source port and accurate timekeeping, and that a report often lacks a port.
2. On a shared cloud NAT, map the public address and port to the `lenny-egress` replica with the NAT log. Without that log, the query returns every tunnel from the egress address in the window, ranked by target match. The operations guide requires the deployer to retain NAT logs for at least the longest evidence retention.
3. Run `attribute` with a window of plus or minus 60 seconds. It reads `egress.tunnel`, joins `egress.decision`, and checks attribution against `egress.accesslog` and `pod.address`, so it reports a mismatch beside each candidate. It also searches `connect` records in `agent.activity.exec` by destination. An empty result is never a clearance: the response always carries `coverage`, listing from `pod.address` each pod in the window whose traffic Envoy does not see (pods of pools with `egressProfile: internet`, and every pod at level 1) and whether it had `connect` coverage. On a NAT log or node address, the list narrows to the pods on that node. At level 1 the deployer's proxy log is the record of the tunnel, and the runbook reads it.
4. For each candidate and each pod under `coverage` that the list narrows to, read the session's owner, then the `connect` records in `agent.activity.exec` in the same pod around the tunnel's start, when coverage exists. That join is a time-based correlation and is labelled so.
5. Classify: tenant-directed abuse, a prompt-injected agent, or a compromised credential or component. Each class names its next action. An abusive tenant gets quarantine before suspension, because suspension drains sessions and a pod with no label is released.
6. Reply to the reporter with the action taken and no tenant identity. Contacting the tenant is a recorded case action, under decision 6's rule that the tenant receives no signal of an investigation until a case discloses it.

## FE-6: Agent activity streams

### What can observe an agent

Lenny's default isolation is gVisor. `runc` shares the host kernel and gives no protection against kernel exploits, its blast radius is bounded by node classes and user namespaces, and an operator chooses it through `allowStandardIsolation` ([PM-1](placement-design.md#pm-1-node-classes-and-presets)). §5.3 lists Kata for higher-risk multi-tenant workloads. A host eBPF sensor, which the outline named as its example, therefore sees the Sentry and the gofer on the default profile rather than the agent's processes.

| Source | What it sees | What it cannot see | runc | gVisor | Kata | Provenance | Privilege |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Gateway: platform tool calls, which the runtime sends as `tool_call` frames on `CH-MSGSOCK` from its runtime connection or a tool connection and the adapter forwards on `LNK-GWCONTROL` ([RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames)) | Every call that crosses `LNK-GWCONTROL`, with arguments | Anything local to the pod | Yes | Yes | Yes | `enforced` | None |
| Session transcript (§7.1) | Tool calls as the runtime recorded them | Everything the runtime omits or rewrites | Yes | Yes | Yes | `self_reported` | None. Retained 7 days by default. |
| `PreLLMRequest` interceptor (LLM proxy, §4.8) | Message history, including earlier tool-use blocks in provider format | Calls the runtime sends outside the proxy with its own provider key, the response stream (`PostLLMResponse` fires once on metadata), and anything the runtime omits from history | Yes | Yes | Yes | `self_reported` in substance | None. Provider-specific parsing. |
| `GET /v1/sessions/{id}/setup-output` | The output of setup commands and of `lenny-materialize` runs, which the slot's supervisor passes on log pipes, and the helper's result | Anything after setup | Yes | Yes | Yes | `self_reported`, because processes at the slot UID write it | None |
| `egress.*`, `git.operation`, and `credential.mint` | Network, git, and credentials, including each connection to an operator service such as an external MCP server, and each binding secret or identity assertion `netagent` attached to it | Process and file activity, and the tool calls inside an operator service's traffic, which the service records | Yes | Yes | Yes | As stated in [the egress design](egress-and-git-design.md) | None |
| Runtime SDK producer (new) | Tool name, intent, command text as the agent stated it, the model turn (the LLM response that asked for the tool) and `toolCallId`, and files the tool opened | Children of a command, anything a compromised or uninstrumented runtime omits, and a runtime with no SDK | Yes | Yes | Yes | `self_reported` | None |
| Supervisor tap (seccomp user notification or ptrace from the namespace init) | `execve` and `openat` in the slot container | Tampering by a process that attacks the supervisor, which §4.7.11 item 8 places on the untrusted side | Yes | Unverified | Yes | `self_reported` | None, but a trap on each traced syscall |
| Host eBPF sensor (Tetragon, Falco) | Under runc, every process with arguments and pod identity; file access through kprobes with in-kernel path filters | Under gVisor, syscalls the Sentry handles, so the host sees the Sentry and gofer. Under Kata, the guest kernel. Files reached through a hard link or another bind mount, per Tetragon's documentation | Yes | No | No | `observed` | Privileged DaemonSet |
| gVisor runtime monitoring (`seccheck` remote sink) | Trace points for all syscalls and for events such as container start, with context fields such as PID, UID, and container ID | Other profiles. Anything after a sink disconnect, per third-party trials. A compromised Sentry. | No | Yes | No | `observed` | None in the sandbox. A node process owning a Unix socket, and node configuration of `runsc`. |
| In-guest sensor under Kata (eBPF in the VM kernel, as Ant Group's AntCWPP does) | Guest processes and files | A compromised guest kernel | No | No | Yes | `self_reported` under the envelope's rule, because it runs inside the pod's VM | Guest kernel build and a node agent |

The adapter-local tools that the adapter once served are retired ([`runtime-protocol-design.md`](runtime-protocol-design.md), decision D5), so no source records them, and a runtime's own shell and file tools reach the record only through the SDK producer, the transcript, and the collector.

Evidence for the host-sensor limit comes from several directions. Google's documentation states that Container Threat Detection is not compatible with GKE Sandbox, Google Kubernetes Engine's managed gVisor. AWS documents that GuardDuty Runtime Monitoring does not cover Amazon Elastic Kubernetes Service (EKS) on Fargate. The Kubescape proposal for gVisor states that its node agent sees host kernel activity while many application system calls are handled inside the Sentry. The Sysdig press release and gVisor's tutorial show Falco moving to gVisor's own trace points for this reason. No source states the Tetragon limit in those words, so this design records it as inference from the architecture, and the first spike tests it.

Each product surveyed separates the two concerns. Claude Code's documented telemetry emits `claude_code.tool_result`, withholds command text unless `OTEL_LOG_TOOL_DETAILS=1` is set, and offers `PreToolUse` and `PostToolUse` hooks that receive `tool_input.command`. Those are tool-level records, and the page does not address child processes. Gemini's agent hooks are similar. These are the semantic layer, which says why a command ran, and none of them claims to see what the command then did.

### Recommendation

Use layered producers, joined by `podUid`, `sessionId`, `traceId`, and time.

1. **FE-6a, tool activity, on every profile.** The gateway records platform tool calls as `enforced`. A runtime's calls to an operator service, such as an external MCP server, leave the pod through `netagent` and egress, so Lenny records them in `egress.tunnel` (`enforced`), `egress.local` (`self_reported`), and `credential.mint`, with no per-call record; the service keeps its own ([CR-3](connector-removal-design.md#cr-3-external-services)). The runtime SDK gains an `activity` report with a tool name, `toolCallId`, input, and outcome. These are `self_reported`, and they give an investigator the agent's account of what it did, including which model turn asked for it.
2. **FE-6b, process and file activity, from a `lenny-activity-collector` DaemonSet reading gVisor's remote sink.** The default profile gets `observed` evidence with no eBPF and no privileged container (FP9), because the Sentry dials out to a Unix socket on the node. The collector is a named node agent of [PM-5](placement-design.md#pm-5-rendering) in the `lenny-node-agents` namespace. It runs on the nodes of the node class that contains `sandboxed`, narrowed by `forensics.activity.nodeSelector` to the nodes whose `runsc` handler names the sink, and it tolerates that class's taint and `lenny.dev/tenant` with `operator: Exists`, so it also runs on dedicated gVisor nodes. Its socket directory is a `hostPath` volume, which the Pod Security levels `restricted` and `baseline` both refuse, so `lenny-node-agents` carries the level `privileged`, and `lenny-pod-security` admits in that namespace only the collector's pod template: non-root, every capability dropped, no privilege escalation, a read-only root filesystem, and one `hostPath` directory for the socket. The container itself is not privileged.

The egress design rejects a per-node DaemonSet for its central components ([`egress-and-git-design.md`, "Network policy and placement"](egress-and-git-design.md#network-policy-and-placement)), and its reasons apply to the collector as follows:

- **Reach after a container escape.** An escape on an agent node would reach the mirror's repository contents and the decision service's policy for every tenant. The collector holds neither. It holds the trace points and the fingerprint keys of the pods on its own node, and ingest accepts its records for those pods alone, so an escape that reaches it gains what the node's pods already expose.
- **Class taints.** A DaemonSet on agent nodes needs a toleration of the node class's taint, which the placement design admits only for named node agents. The collector runs only on nodes of the class that contains `sandboxed` that run monitored pools, so it needs no Kata exception or microVM per node.
- **Capacity, outage, and updates.** A DaemonSet reserves capacity on every node it runs on, which lowers warm-pod density, and an outage or a rolling update affects every pod on its node. These apply to the collector. "Volume and cost" states the capacity, and "Failure behaviour" states that an outage loses coverage, fails the start of a monitored sandbox, and drops no traffic.

The other profiles differ. Pools with `isolationProfile: standard` get FE-6a, plus a published producer contract, so a deployer running Tetragon or Falco can forward to `lenny-evidence` under its own identity, and a reference forwarder for Tetragon is tested on Kind, a local cluster that runs Kubernetes nodes in containers (decision 3). Pools with `isolationProfile: microvm` get FE-6a and the network evidence in the first version, and the API reports `coverage: tool_only` for them, so investigators see the gap (decision 2). The supervisor tap in the table above is the first follow-up for `isolationProfile: microvm`, because it needs no guest kernel work, and its records are `self_reported`. An in-guest sensor is later work: a guest kernel with BPF and BTF (BPF Type Format) support per Kata release and hypervisor, a sensor in every pod with BPF rights inside the VM, and an admission exception, as Ant Group's AntCWPP runs. Under the envelope's rule it would still be `self_reported`. A file-only watch on a pod's workspace volume from the node was considered for pools with `isolationProfile: standard` and `microvm` and is not used: it records that a file changed, with no process and no reads, and it needs read access to every pod's volume directory on the node, including mounted secrets.

A new pool field, `activityMonitoring: true`, selects coverage, and it is valid only with `isolationProfile: sandboxed`. A profile maps to one RuntimeClass through `runtimeClasses.profiles.<profile>.name` (§17.5, `RuntimeClassNameOverrides`), so the profile cannot distinguish the two. The controller renders the class named in `runtimeClasses.monitored.name`, by default `gvisor-monitored`, whose runtime handler points at a `runsc` configuration naming the sink. An unmonitored gVisor pool on the same node has no trace session and no cost. Admission policies keyed on RuntimeClass (§17.2) gain the name. Configuring the handler is node provisioning, which the cluster admin owns, as with RuntimeClass installation today (§17). GKE Sandbox preconfigures gVisor, and the Falco Helm chart installs there through a values file, so the managed path needs its own check.

### Streams

| Stream | Producer | Provenance | Payload | Default retention |
| :-- | :-- | :-- | :-- | :-- |
| `agent.activity.tool` | Gateway, including the SDK reports that the adapter forwards | `enforced` for calls the gateway carried, `self_reported` otherwise | `toolCallId`, tool, server (`platform` for a call the gateway carried, and the name the runtime reports otherwise), phase, outcome, bounded redacted input, duration, and `reportedBy` | 90 days |
| `agent.activity.exec` | Collector | `observed` | `kind` (`exec`, `exit`, or `connect`), PID, parent PID, process start time, binary path, `argv[0]`, argument count, UID, container name, working directory class, exit status, and the destination of a `connect` | 30 days |
| `agent.activity.exec.args` | Collector | `observed` | The process key and its redacted, bounded arguments | 14 days |
| `agent.activity.file` | Collector | `observed` | Per process and directory prefix: operation and count per window. Exact records for the watchlist (`forensics.activity.fileWatchlist`, under "Volume and cost") and for writes outside the workspace | 30 days |

Separate streams carry different permissions, retention, and keys, which answers the outline's question about differing audiences: a customer's security team can hold `exec` while only forensics holds `args`. Attribution comes from ingest. The collector supplies the pod and container and the time, and `lenny-evidence` sets tenant and session from the binding at that time. Materialization and setup commands run in the slot container at the slot UID through the supervisor's worker ([WM-3](workspace-materialization-design.md#wm-3-the-protocol)), so their processes appear in `runtime-<n>`, and the record's binding state (warm-up, materializing, or running) separates them from the runtime's own work. Warm-pod records have no tenant and use the platform prefix, as the envelope states. Attribution comes from the binding, as the envelope requires. A pod with one bound session gets that session. On a concurrent-slot pod, each slot runs in its own container under its own UID, so a record from `runtime-<n>` takes the session the binding holds in slot `n` at that time, and records from the `adapter` and `netagent` containers carry `candidateSessions` with no `sessionId`. A record also carries a `workdirHint` field that the agent controls and nothing else reads, so a process cannot choose its key by changing directory.

**Joining a command to a tunnel.** Prompt-injection exfiltration is the case an investigator most needs answered: which command opened this connection. gVisor's `Connect` trace point has `fd`, `fd_path`, and one `address`, with no local port, so `agent.activity.exec` cannot supply the port that `egress.local` now records. On gVisor the join is a time-based correlation of a process's `connect` to the local egress endpoint with `netagent`'s tunnel open in the same pod, and the query returns it with `join: time_correlated` and every candidate process. A busy pod can yield several candidates, and a `connect` may reuse an HTTP connection kept open from an earlier request. The spike checks whether a trace point or an upstream gVisor change can supply the port. A host sensor under `isolationProfile: standard` may see the socket's local port, which would make `join: exact` there, and that is also unverified. A `connect` to any address other than the local egress endpoints is recorded with its destination. On a pool with `egressProfile: proxied` or `restricted` the NetworkPolicy drops it, so the attempt is a signal. On a pool with `egressProfile: internet` it succeeds and passes no proxy, so this record is the one Lenny source for that connection, which `attribute` searches by destination.

**Trace point fields.** gVisor's `Execve` carries `pathname`, `argv`, and `envv`, and `envv` stays off. `Open` carries a raw `pathname` that the source describes as not canonicalized, and an `fd_path` for the `openat` directory. The collector resolves it against the process's working directory before the watchlist check. It cannot resolve symlinks, so the watchlist is weaker than a canonical path filter, and the residual is stated below.

**Records after a quarantine.** Tenant reads must not show `quarantined` denials or activity after a session's failure (FP3). Records from a quarantined pod, from the moment the state applies, go under the platform prefix, as the egress design's attribution row under ["Evidence streams"](egress-and-git-design.md#evidence-streams) states, so they appear in no tenant read or export, and the tenant's sequence numbers show no gap. That row gives a warm pod's records no tenant and does not say whether a quarantined pod's records keep theirs. This design needs the tenant on each such record, in the payload rather than in the tenant prefix, so that a case can read them by tenant and tenant deletion can find them, and it requests that the egress design state it (see "Changes requested of egress-and-git-design.md"). They stay until the quarantine's release plus `forensics.quarantine.recordRetentionDays` (90 by default), or longer under a hold, and tenant deletion removes them when no deferring hold covers them. Activity payloads keep their session or pod-incarnation key, and that key follows the rules under "Keys", so an erasure with no deferring hold makes them unreadable.

### Producer contract and channels

- **SDK.** The runtime writes an `activity` frame on `CH-MSGSOCK`, or calls the SDK's `reportActivity`. The adapter validates, sizes, and rate-limits it (§4.7.11 item 3), and sends it on a new channel proposed as `CH-ACTIVITYREPORT`. The frame names no session ([`agent-runtimes-only-design.md`](agent-runtimes-only-design.md), O5): the adapter takes its session from the slot whose connection carries it, and a `sessionId` member a runtime still sends is an unknown member that the adapter ignores under the leniency rule of [MD-24](message-delivery-design.md#md-24-runtime-frames-and-leniency). The gateway sets session and tenant from the binding of that slot, forces `self_reported`, and forwards to `lenny-evidence`, as the decision service does for `netagent`. Reporting is optional for every runtime and needs no capability, and an absent report shows as `coverage` rather than as an error.
- **Collector.** One unprivileged process per monitored node, running as non-root, with a hostPath directory for its socket, an informer limited to its own node's pods, and a `lenny-evidence` client. gVisor allows one trace session, named `Default`, with several sinks, so a deployer already running Falco on these nodes merges the two configurations. It filters the fields that gVisor always sends (a third-party trial reports that `container/start` carries arguments and the working directory regardless of optional fields) before anything leaves memory. It authenticates with a projected token carrying pod and node claims, and ingest accepts a record only for a pod on that node, as the egress design states, so a compromised collector can forge records for pods on its node and no others.
- **Public contract.** A deployer's sensor is a producer with an allowlisted identity and the same node restriction, writing `agent.activity.exec` and `agent.activity.file`. Provenance stays `observed`, and `producer` names it.

### Redaction

Redaction runs in the collector and in the adapter before a record leaves the node. Plaintext exists only in those processes' memory.

| Layer | Rule |
| :-- | :-- |
| Never collected | Environment variables (the optional field stays off), file contents, and stdin and stdout |
| Structural | `--token=`, `--password`, `Authorization:`, URL userinfo, `KEY=value` forms for secret-named keys, and the known prefixes in `forensics.activity.redactPatterns`. The value is replaced with a keyed fingerprint. |
| Entropy | A high-entropy argument above a set length is treated the same way. |
| Bounds | At most 64 arguments and 4 KiB per record. Longer input is cut with a `truncated` count. Paths are bounded and normalized. |
| Credential paths | `/run/lenny` and `/run/netagent` are recorded as path classes, never as file names beyond the fixed layout. |
| Fingerprints | HMAC-SHA-256 under a per-pod-incarnation fingerprint key that `lenny-evidence` issues to the collector and to the adapter's path at coverage start, because the redacting process runs before ingest and holds no session key. An investigator holding a leaked value can test it pod by pod, through `forensics.query`, without the record storing it. The key is stored wrapped beside the data keys and is destroyed with them. |
| Detail setting (`agentActivity.detail`) | `metadata` writes no `args` stream, `redacted` is the default, and `full` writes arguments with the structural layer off, and needs a platform gate and a tenant opt-in. |

Redaction misses secrets with no recognizable form, so §12.9 classifies every activity stream as Confidential on every tenant ([TSS-8](tenant-security-settings-design.md#tss-8-data-classes)). Encryption and named read permissions are the control, and redaction reduces exposure. Tool input from the SDK passes the same rules, in the adapter, because a compromised runtime controls that input.

### Keys

Each session has a data key, generated by `lenny-evidence` at the first record, wrapped by the tenant key while the tenant key state is `active` ([TSS-4](tenant-security-settings-design.md#tss-4-tenant-key)) and by a regional evidence key otherwise, and stored in `evidence_session_keys` (tenant, session, user, wrapped key, state, and timestamps). `payloadKeyRef` names it. `lenny-evidence` encrypts on ingest, so digests are computed over ciphertext and still verify after destruction. A record with no single session (a concurrent-slot pod's `adapter` and `netagent` records) uses a key per tenant and pod incarnation, selected by the binding. Its data key is wrapped as a session's is.

| Event | Effect |
| :-- | :-- |
| Read | The gateway's handler asks `lenny-evidence` to decrypt, after its permission check. A cross-tenant platform read writes `forensics.evidence_read`. |
| `DeleteByUser` | Tenant reads of that user's sessions stop at once. The job schedules destruction of their keys after `forensics.activity.keyDestructionDelay` (0 by default), so a hold set in that window can still stop it, and it then records a tombstone. A key under a hold with a deferring `basis` is queued in pending erasures. A pod-incarnation key is destroyed when every user whose session ran on that pod has been erased, or at retention expiry, because destroying it on one user's request would let a tenant erase other sessions' evidence. The erased user's records on that pod stay encrypted until then, which is stated under "Residual risks". |
| Retention expiry | The sweeper deletes batches, and it destroys a session's key once its last batch is gone. |
| Tenant teardown | Keys are deleted with the tenant, except those under a hold or the retention class, which are rewrapped under the escrow KEK. |
| Tenant key state changes | A key generated while the state is `active` is wrapped by the tenant key, and one generated otherwise by the regional evidence key. When the state moves from `active` to `retained`, existing keys stay wrapped by the tenant key (decision 28), `lenny-evidence` reads them through it as long as the key exists, and the state reaches `none` only once no data key is wrapped by it. |

`lenny-evidence` and `lenny-evidence-reconcile` hold decrypt rights on the tenant keys of their region, and `lenny-evidence` also holds encrypt rights, so that it can wrap and unwrap the data keys of tenants whose key state is `active` or `retained`.

A compacted object holds several sessions' ciphertext, so a hold on one session's manifests keeps other sessions' ciphertext in the same tenant-hour. Their keys follow normal retention, so those records become unreadable on schedule.

### Who may read

The streams are customer data about the customer's own agents. A tenant admin can already read session content (§10.2 `Read other users' sessions`), so a tenant read of `tool` and `exec` grants nothing new. An operator read is the new exposure. The defaults are as follows, and [`security-oversight-design.md` decision 19](security-oversight-design.md#decisions) settles that no security role holds one by default. A `security-admin` grants a stream to `security-auditor` through `auditorStreamGrants`, under the delay:

| Permission | Default holders | Operator access |
| :-- | :-- | :-- |
| `evidence.read.agent.activity.tool` | `tenant-admin` | `forensics.query` with an open case naming the tenant |
| `evidence.read.agent.activity.exec` | `tenant-admin` | Same |
| `evidence.read.agent.activity.file` | `tenant-admin` | Same |
| `evidence.read.agent.activity.exec.args` | None until granted by a tenant admin | Same, and each read writes a custody event |

`tenant-viewer` holds none of these by default, because an activity stream can expose paths and commands of other users' sessions in a form the viewer role's session read does not present today. A tenant admin who is the subject of an investigation can read these streams, which is why the operator's copy is the one under hold.

**Activity floor.** A tenant admin could otherwise turn every activity stream off or erase a user before an investigation starts, and a later hold cannot recover a record that was never written or a key already destroyed. `forensics.activity.floor` sets a deployment minimum for each stream (which streams must stay on), detail, and retention, `agentActivity.floor` raises it per tenant under the platform admin's control, and the tenant's settings can only go above the floor. A floor that keeps `exec` or `file` on means sessions need a monitored pool, and session creation on another pool is refused. Lowering a tenant's own setting above the floor is an audited write (decision 23).

### Volume and cost

Process and file events exceed egress decisions by orders of magnitude. The figures below are planning assumptions for a 30-minute coding session, to be replaced by the spike's measurements. Daily figures use the §16.4 estimate for capacity tier 3 (§17.8.2) of 10,000 sessions per day, and they are uncompressed.

| Stream | Records per session | Size | Per session | Per day at Tier 3 | Average records per second | Retention | Stored at steady state |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `tool` | 200 | 1 KiB | 0.2 MB | 2 GB | 23 | 90 days | 180 GB |
| `exec` | 2,000 | 300 B | 0.6 MB | 6 GB | 231 | 30 days | 180 GB |
| `exec.args` | 2,000 | 500 B | 1 MB | 10 GB | 231 | 14 days | 140 GB |
| `file`, aggregated | 5,000 | 200 B | 1 MB | 10 GB | 579 | 30 days | 300 GB |
| `file`, every open | 1,000,000 | 150 B | 150 MB | 1.5 TB | 115,741 | Not offered | Not offered |

For comparison, §16.4 estimates about 10 KB of audit and 50 KB of logs per session, and the egress design's stand-in measured evidence at 2,000 CONNECTs per second. The recommended streams total about 2.8 MB per session, or 28 GB per day at Tier 3. Every open would multiply storage by about 50 and reach about 58 times the rate the stand-in ran at, on average and before peaks, which is why it is not offered.

- **Sampling.** `exec` is not sampled, on the precedent that denied egress decisions are never sampled, because an omitted process is what an attacker wants. A per-pod rate limit (`forensics.activity.execRatePerPod`, 200 per second) applies, and above it the collector collapses records by binary and parent with a count, and it marks the record. `file` is aggregated per process and directory prefix per 10-second window, with exact records for the watchlist.
- **Watchlist.** `forensics.activity.fileWatchlist` covers paths whose access is the event: `/run/lenny`, `/run/netagent`, `/proc/*/environ`, `.ssh`, `.git/config`, `.npmrc`, and `.netrc`. A tenant can extend it.
- **Isolation between tenants ([P7](egress-and-git-design.md#security-properties)).** The collector bounds each pod, and ingest bounds each tenant. Past a bound, loss appears as a sequence gap and a gap record, as the egress design specifies, and a flood is itself recorded by the count.
- **Effect on the agent.** gVisor's documentation warns that a high number of remote sink retries can significantly delay application execution, so the configuration sets low retries and accepts a drop. An attacker can cause drops by flooding, which is visible as gaps and a rate-limit record. Tracing `openat` adds work on each open in the Sentry, by an amount no gVisor source states, so the `file` stream can be turned off per pool, and the spike measures both. For scale, Falco's project documentation gives a rule of thumb for its kernel path: below about 1,500 kernel events per second per CPU keeping up is rarely a problem, and above about 3,000 it becomes harder, with event rates depending on the workload.
- **Manifest rows.** The compaction bound of one row per tenant-hour per stream gives 120,000 rows per day per stream at 5,000 tenants. The egress design's bound for its streams is 720,000. The activity streams and `pod.address` raise that to at most 1,320,000.

**Per-tenant cost.** A tenant's stored bytes are `sessions per day × Σ (MB per session × retention days)`. At the assumed sizes, that is 80 MB per daily session, so a tenant with 100 sessions per day holds about 8 GB. `lenny_evidence_bytes_total{tenant_id, stream}` and a daily `evidence.usage` billing event in §11.2.1 let an operator charge for it, and the tenant's activity bytes count against a separate `evidenceQuotaBytes`, so a full quota produces gap records and never refuses a session. The operator bears the collector's node cost, because it runs as a DaemonSet outside every agent pod, and no billing event reports a DaemonSet's capacity ([`billing-resource-attribution-design.md`, "Scope"](billing-resource-attribution-design.md#scope)).

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Collector absent when a sandbox starts | `ignore_setup_error: false` on the monitored class fails the sandbox, so the pod fails its warm fill, and the alert names the node. |
| Collector restarts with sandboxes running | A third-party plugin's README states that `runsc` connects to the sink once at sandbox start, does not reconnect, and needs a sandbox restart after the receiver restarts. A Kubescape trial states that its restart run does not establish recovery of the existing connection. Running pods therefore go dark until they retire, unless the spike finds a reattach path. The collector records `coverage: lost` for each, and `ActivityCoverageLost` fires. The DaemonSet uses `OnDelete` updates during node rotation. Whether `runsc trace create` can reattach from a Job is a validation item. |
| Coverage lost on a bound pod | `forensics.activity.onCoverageLoss`: `continue` (default) records the gap, `retire` refuses new sessions and retires the pod after its session, and `quarantine` applies on regulated tenants only by explicit choice. |
| Sentry drops a trace point | The sink's sender drop count, where reported, becomes a gap record. A third-party trial notes that zero reported drops does not prove zero loss, so absence of a record is never proof. |
| `lenny-evidence` unavailable | The collector buffers per tenant, then records a gap. Activity never blocks the agent, including on a regulated tenant, because the write-ahead rule for egress decisions exists to make a record precede a connection and the volume here would make it an availability risk. |
| Runtime does not report | `agent.activity.tool` holds only the gateway's records, and `GET` reports `coverage`. |
| Node loss | Unflushed records are lost, which the incarnation rule detects. |

**Coverage API.** `GET /v1/admin/sessions/{id}/activity-coverage` and a tenant equivalent return, per pod incarnation, which producers ran, from when to when, and any gap (R8).

### Read API and export

`GET /v1/agent-activity` takes `sessionId`, `stream`, `kind`, a binary or path prefix, and a time range, and it needs the stream's permission. `GET /v1/admin/evidence/{stream}` is the platform form. Export uses the egress design's cursor path, in which each export consumer holds a cursor of the record ranges it has received, with OCSF translation at delivery: Process Activity (class 1007) for `exec` and File System Activity (class 1001) for `file`, both read from the OCSF v1.1.0 schema, and `exec.args` in `unmapped.lenny.*` only when the subscription holds its permission. A SIEM copy is outside key destruction, so the export subscription, through which a SIEM receives the stream, states this and §12.8 lists it as a sink to notify.

### Delivered user tokens

[`user-credential-delivery-design.md`](user-credential-delivery-design.md) can place a person's token in a pod. `delivery: inject` keeps it in `netagent` memory, and `delivery: file` writes it to `/run/netagent/credentials/<name>/value` on a per-slot volume that only `netagent` writes and only the session's slot container mounts ([SI-7](session-isolation-design.md#si-7-credentials-manifests-and-other-shared-files)), readable by any process at that slot's UID.

- **Quarantine.** `quarantined` in `pkg/binding` stops release on the pod credential API from each instance's own read, so a quarantined pod receives no new token and no refreshed one. It does not depend on `netagent`.
- **What a quarantined pod keeps.** It depends on `netagent`. An honest `netagent` removes the file on the binding change, so the quarantined pod has no file, and any copy the agent made remains. A compromised `netagent`, or one frozen or stopped before the change, leaves the file in an emptyDir that `keepalive` keeps with the pod. In that case, and for any copy, the token stays valid at the provider, so **a quarantined pod can hold a live credential for a person**. The capture commands in FE-5 do not copy `/run/netagent` by default, and a capture that includes it holds Secret-class data and needs its own case entry. The identifier in `binding.json`, the file beside the token that names the binding, its hosts, and its expiry, and the hash in `credential.mint` serve most investigations with no copy.
- **Rotation limits.**
  - A token from an `oauth2Link` source: remove the user's link, which revokes its grant at the provider's endpoint when one exists. Revoking one child needs the token value, and no route takes a hash, so the case works by link. That can break the user's other sessions.
  - A token from a `registered` source: Lenny holds no way to revoke. The case states this, and the tenant's backend acts, from the durable exposure rows, which name each generation and the writer's `tokenId`, or on the investigator's notice.
  - Copies: nothing Lenny does reaches a copy the agent made. A token that reached a provider through an opaque tunnel shows in that provider's logs and not in Lenny's.
- **Where the token can be in evidence.** Under `delivery: file`, an agent can print it or store it, so it can reach a transcript, an LLM request, a workspace, a checkpoint, an export, or an `agent.activity` record. Sessions holding such a binding are marked `agentHeldCredential`. For these sessions, investigators treat captures and transcripts as Secret-class data, and a report to a tenant names the session rather than quoting. Under `delivery: inject` the agent cannot read the token, except through a reflection the egress design's response scrub misses.
- **Redaction.** FE-6's structural and entropy layers apply, and they miss a token with no recognizable form. Known values do not reach FE-6's collector or adapter, because the collector is a node-level process shared across tenants, and `leakScan` works in the gateway's interceptors instead. An investigator holding a leaked value tests it pod by pod through `forensics.query`, as FE-6 states.
- **Attribution.** `credential.mint` carries `tokenSource` and an opaque `credentialRef` or `linkRef`, with no user identifier, a token hash for an OAuth2 token or child, and `generation` with a per-tenant keyed hash for a token from a `registered` source, which keeps its §12.8 treatment. That join lives in rows erasure deletes, so an investigation that needs the person after erasure works from a security hold, under FE-3's rules.
- **Reconciler.** FE-4 can add R-USER-CREDENTIAL-POD, comparing a `userCredential` mint with the pod's session model, since a release to a pod that can serve another session is a defect.

## Configuration by scope

| Setting | Description | Applies to | Managed by | Notes |
| :-- | :-- | :-- | :-- | :-- |
| `forensics.enabled` | Turns on the case, quarantine, and hold APIs and the `platform-forensics` role. | Deployment | Platform admin (Helm) | |
| `forensics.breakGlassGroups` | Lists the Kubernetes groups that may stamp the quarantine label directly when the API is unavailable. | Deployment | Platform admin (Helm) | Empty disables the path. |
| `forensics.quarantine.alertAfter` | Sets the age at which `QuarantineAged` fires. | Deployment | Platform admin (Helm) | 24 hours by default. It never releases. |
| `forensics.quarantine.maxPods` | Sets the count of quarantined pods at which an alert fires. | Deployment | Platform admin (Helm) | 10 by default (decision 29). It alerts and does not refuse. |
| `forensics.quarantine.preserveTimeout` | Bounds how long the handler waits for the adapter's acknowledgement before stamping the label. | Deployment | Platform admin (Helm) | 5 seconds by default. |
| `forensics.erasure.retentionStatement` and `retentionStatementText` | Whether an erasure receipt carries the same retention text on every receipt or states whether items were retained, and the text used. | Deployment | Platform admin (Helm) | `generic`, with the default text under "Receipt". Decision 7. |
| `forensics.holds.deferErasureBases` | Lists the hold bases under which user erasure and tenant deletion defer, for copies, keys, and held stores. | Deployment | Platform admin (Helm), on the operator's own legal assessment | `legal_obligation` and `legal_claims` by default. Decision 24. |
| `forensics.holds.auditCopyInterval` | Sets how often an open-ended hold copies new audit rows. | Deployment | Platform admin (Helm) | 5 minutes by default. |
| `forensics.quarantine.recordRetentionDays` | Sets how long a quarantined pod's later records remain after release. | Deployment | Platform admin (Helm) | 90 days by default. |
| `forensics.twoPersonRelease` | Requires a release to come from a different principal than the one who set it. | Deployment | Platform admin (Helm) | Decision 12. |
| `gitMirror.forensics.volumeSnapshotClass` | Names the CSI snapshot class for store holds. | Deployment | Platform admin (Helm) | Empty means no snapshot, and the API reports it. |
| `gitMirror.heldUidBudget` | Caps the fetch UIDs that held stores may keep out of the pool. | Deployment | Platform admin (Helm) | A quarter of the mirror's fetch-UID range by default ([`egress-and-git-design.md`, decision 66](egress-and-git-design.md#decisions); decision 29). |
| `storage.regions.<region>.legalHoldEscrow` | Names the escrow endpoint, bucket, KMS key, and KEK, reused for security holds. | Region | Platform admin (Helm) | Existing setting (§12.8). |
| Stream `retentionClass: security` and `forensics.retention.afterTeardownDays` | Selects the streams retained after tenant teardown, and for how long. | Deployment | Platform admin (Helm) | 0 days by default. Decision 8. |
| `lenny-evidence-reconcile` schedule and imported sources | Sets the evidence reconciler's cadence and the object store prefixes holding NAT, flow, provider, and Kubernetes audit logs. | Deployment | Platform admin (Helm) | Hourly by default (decision 29). Imports are optional. |
| `forensics.activity.tool.enabled`, `forensics.activity.exec.enabled`, and `forensics.activity.file.enabled`, with per-stream retention | Turn on each kind of activity collection separately: tool calls (`agent.activity.tool`), processes and commands (`agent.activity.exec` and `.args`), and file access (`agent.activity.file`), and set the ceiling on detail and retention. | Deployment | Platform admin (Helm) | Each off by default. Decision 4. |
| `forensics.activity.floor` | Sets which streams must stay on, and the minimum detail and retention, that a tenant's settings cannot go below. | Deployment | Platform admin (Helm) | `none` by default. A SaaS operator raises it. Decision 23. |
| `forensics.activity.keyDestructionDelay` | Delays destruction of an erased user's keys, so a hold set in that window can stop it. | Deployment | Platform admin (Helm), on the operator's own legal assessment | 0 by default. Decision 23. |
| `agentActivity.floor` | Raises the floor for one tenant, per stream. | Tenant | Platform admin (admin API) | The tenant cannot lower it. |
| `forensics.activity.fullDetailGate` | Grants a tenant the right to choose `agentActivity.detail: full`. | Deployment and tenant | Platform admin (admin API) | Same pattern as the per-tenant gates of [the egress design](egress-and-git-design.md). |
| `forensics.activity.redactPatterns` and `execRatePerPod` | Extends the redaction patterns and sets the per-pod rate limit. | Deployment | Platform admin (Helm) | Patterns can be added and shipped ones cannot be removed. |
| `lenny-activity-collector` image, resources, and producer allowlist | Installs the collector, and lists deployer sensors allowed to write activity streams. | Deployment | Platform admin (Helm) | The collector runs in `lenny-node-agents`. |
| `forensics.activity.nodeSelector` | Narrows the collector to the nodes of the `sandboxed` member's class whose `runsc` handler names the sink. | Deployment | Platform admin (Helm) | [PM-5](placement-design.md#pm-5-rendering) renders it. |
| `runtimeClasses.monitored.name` | Names the RuntimeClass for monitored pools. | Deployment | Platform admin (Helm) | `gvisor-monitored` by default. |
| Monitored runtime handler and sink configuration | Points `runsc` at the collector's socket, on nodes that run monitored pods. | Node pool | Cluster admin | Node provisioning, with a preflight check. |
| `activityMonitoring` | Chooses monitored gVisor for a pool. | Pool | Platform admin | Valid only with `isolationProfile: sandboxed`. |
| `forensics.activity.fileStream` | Turns on the `file` stream for a pool. | Pool | Platform admin | Off by default until the spike reports the Sentry's cost. |
| `forensics.activity.onCoverageLoss` | Chooses what happens to a bound pod that loses coverage. | Pool | Platform admin | Decision 19. |
| `agentActivity.tool`, `agentActivity.exec`, and `agentActivity.file` | Turn each stream on or off independently, between the floor and the deployment's setting for that stream, within the pool's coverage. `exec` and `file` need a monitored pool. | Tenant | Tenant admin | A session cannot turn one off. |
| `agentActivity.detail` | Chooses `metadata`, `redacted`, or `full`. | Tenant | Tenant admin | `full` needs the gate. |
| `agentActivity.retentionDays` per stream | Shortens or lengthens retention within the deployment's limits. | Tenant | Tenant admin | Subject to the activity floor and any regulatory floor. |
| `agentActivity.fileWatchlist` | Adds paths whose access is recorded exactly. | Tenant | Tenant admin | |
| `evidenceQuotaBytes` | Bounds the tenant's activity bytes. | Tenant | Platform admin | Past it, records become gaps. |
| Evidence read permissions | Grants named read permissions on each activity stream. | Tenant | Tenant admin, subject to [`security-oversight-design.md`](security-oversight-design.md) | Defaults under "Who may read". |
| Activity reporting by the runtime | Reports tool calls through the SDK. | Runtime | Runtime author | Optional for every runtime, and no capability gates it. |

No setting exists at session scope, because a prompt-injected or malicious client could use one to reduce evidence.

## Scenarios

### An abuse report from an IP and a timestamp

A hosting provider reports that 203.0.113.7 scanned its host 198.51.100.20 on port 443 at 2026-11-20T14:03:11Z. It gives no source port.

1. Frank, on the operator's forensics team, opens a case. The address is the shared NAT for the default `lenny-egress` Deployment.
2. `lenny-ctl admin forensics attribute --address 203.0.113.7 --target 198.51.100.20:443 --at 2026-11-20T14:03:11Z --window 60s` reads `egress.tunnel` by time and target, and returns three tunnels. All three carry the same session in `acme`, owned by `alice@acme.com`. The attribution check against `egress.accesslog` and `pod.address` reports no mismatch.
3. `agent.activity.exec`, read under the case, shows a `python` process in that session's slot container with `connect` records to the local egress endpoint within a second of each tunnel's start, returned as `join: time_correlated`, and no other process connected in those windows. `agent.activity.tool` shows that the command ran after a fetch of a web page whose text instructed it. This suggests prompt injection rather than tenant-directed abuse, and it is a correlation rather than a proof.
4. The session is still running, so Frank quarantines the pod with `sessionDisposition: fail`, sets a security hold on the session with `evidence`, `audit`, `artifacts`, and `transcripts` and an approved `basis`, and captures memory with `runsc checkpoint --leave-running`.
5. Without activity coverage (a Kata pool, for example), step 3 has only `agent.activity.tool` and the network streams, and `activity-coverage` says so.
6. Frank answers the reporter, and contacts `acme` through the operator's security contact, recording the disclosure on the case. The tenant admin can then read its own streams.

The source port would have narrowed step 2 to one tunnel. On a large shared address with no port and no NAT log, the query returns every tunnel to that target in the window, which is the limit RFC 7422 describes.

### A tenant reports a leaked secret

Bob, a tenant admin at `globex`, reports that the API key from the credential binding `globex-crm` appeared in a public paste. The binding uses `delivery: inject`, so the agent should never have held the value.

1. Bob reads `credential.mint` for the binding through the tenant read. It lists each pod and session that received secret version 7, and all of them are `globex` pods.
2. Bob rotates the secret, which the 15-minute cache lifetime that the Token Service gives a stored secret (`cacheUntil`) bounds in pods, and he checks the host's logs, using the token hash where the provider exposes it. For a stored secret the provider's own records are the sole evidence of use, which is the limit CircleCI described: "there is no way for us to know if your secrets were used".
3. Bob's own reads show `egress.local` response scrub hits, and `egress.decision` records for an unexpected host, in one session. He files a report.
4. Frank, on the operator's forensics team, opens a case naming `globex`. He runs R-MINT-WRONG-TENANT over the range to test for a cross-tenant breach, and it finds no record of the binding minted to another tenant's pod.
5. Bob supplies the key's value through the case's encrypted channel. A fingerprint test over `agent.activity.exec.args` and `agent.activity.tool` in the range finds a match in one session, in a `curl` command line, as the session's tool report also shows. So the value reached the agent's context through a path outside `delivery: inject`, which is a different finding from a platform leak.
6. Frank sets a security hold for the session and the user, with `basis: customer_instruction` and the reference Bob's written request gives, and reports the result to Bob. Where the fingerprint search finds nothing, the investigation has no conclusion on that point, because redaction patterns, coverage gaps, and pods with `tool_only` coverage leave room for a miss.

### A compromised `netagent`

`ActivityCoverageLost` is silent, but R-TUNNEL-AFTER-END fires in a tenant-hour for a pod in `acme`: a tunnel remains open 40 minutes after its session ended, and `netagent`'s `egress.local` reported the close. R-LOCAL-CLAIM confirms it.

1. Frank treats `netagent`'s records as unreliable, because `self_reported` records are evidence only while the pod is uncompromised. The access log, `credential.mint`, and `pod.address` remain, because ingest set their producer from verified identity.
2. He quarantines the pod. The Token Service and the mirror refuse at once, each from its own `pkg/binding` read, and the decision service refuses new CONNECTs. A compromised `netagent` does not close its tunnels, so the tunnel survives. The pool's cap is 2 hours, so he reads `egress.accesslog` for the replica, and he runs the graceful drain on that replica after the checkpoint, accepting that other tenants' tunnels on it reconnect.
3. He checkpoints the `netagent` container. A native sidecar restarts when it exits, which would lose its memory, so this comes first. The archive holds the upstream tokens in that memory (P2), and it is encrypted to the case key.
4. `credential.mint` lists every token minted for the pod with its hash or `jti`, and Frank revokes or waits out those tokens, and rotates any stored secret version listed.
5. A compromised `netagent` could not write another tenant's records, because the decision service overwrites identity, and it could request credentials only within the pod's binding. R-MINT-NO-SCOPE and R-ATTRIBUTION run over the case range to confirm this.
6. A compromised `netagent` that sits on a quarantined pod could still attempt raw connections, so `agent.activity.exec` `connect` records and the NetworkPolicy's drops are checked for the attempt. Because `netagent` runs in the pod's Sentry, its own `exec` and `connect` trace points are `observed` too.

### A tenant erasure request during an open investigation

Carol, a tenant admin at `acme`, submits `POST /v1/admin/users/alice/erase` while Frank's case has a security hold covering Alice's sessions.

1. Step 0 finds no §12.8 legal hold, so the job starts. The security hold sits outside that ledger, and the response is the normal one.
2. The job drains Alice's sessions and deletes the originals in the tenant's stores. A quarantined pod remains, and the release step skips it.
3. The hold's `basis` is `legal_claims`, approved under the operator's own process, so its escrow copies and held evidence manifests stay, its per-session keys are skipped, and the job joins the pending erasures. Keys for her other sessions are destroyed, after any configured delay, so those records become unreadable while their digests still verify. Had the basis been outside `deferErasureBases`, the copies and keys would go too, and Frank would see `forensics.evidence_lost`.
4. The tenant-readable events have the normal form, under decision 7's default, and the case records the retention.
5. When Frank releases the hold, the worker deletes the copies and destroys the keys, unless another hold covers them, writes `gdpr.erasure_deferred_completed`, and terminates the quarantined pod, removing its volumes.

If `acme` asks for tenant deletion instead, Phase 3.5 waits for the hold to finish materializing, escrows held evidence and rewraps its keys, and continues, so the lifecycle reaches `deleted` within the tenant deletion deadline that Phase 1 recorded from `acme`'s `deletion.tenantDeadlineHours` ([TSS-5](tenant-security-settings-design.md#tss-5-deadlines-and-retention)), and Phase 4a schedules deletion of the tenant key when its state is `active` or `retained`. A quarantined pod keeps tenant data on its node's disk past that deadline, which the case states, and which the operator's agreement has to allow.

## Residual risks

- **Lost before quarantine.** Memory and the slot volumes from before the first alert are gone unless the pod is still running, and the default pod lifetime is one session (§6.1). On a pod that serves several sessions in turn, each slot release ends the slot's processes and empties its volumes ([SI-3](session-isolation-design.md#si-3-runtime-processes-slot-release-and-setup-commands)), so an earlier session's files are gone while the pod runs. Many investigations start from records alone. A tenant admin can lengthen activity retention with `agentActivity.retentionDays` within the deployment's limits, and the deployment's `retentionClass: security` keeps chosen streams after tenant teardown. Nothing extends a pod's life in advance.
- **A person's token in a quarantined pod.** A quarantined pod under `delivery: file`, whose `netagent` did not remove the file, holds a token valid at its provider until someone revokes it there, and Lenny cannot revoke a token from a `registered` source. A copy an agent made is out of reach.
- **Tokens in evidence.** Redaction misses a token with no recognizable form, so stored evidence can hold one for its retention, under the Confidential class of the stream or capture that holds it, although the token itself is Secret-class data.
- **Eviction and node loss.** The kubelet's eviction ignores PodDisruptionBudgets and grace periods, and a hard threshold terminates immediately, so a quarantined pod can be lost. An unfrozen agent can trigger this itself by filling a slot volume past its `sizeLimit`, so on a pod whose freeze failed, was declined, or still waits behind a setup run, the volumes last only until the agent chooses otherwise or the node-side freeze lands. A quarantined pod needs capturing at once, and the loss is recorded.
- **A compromised runtime destroys its own state.** Nothing stops a process from exiting or from overwriting its workspace before the quarantine lands, or while its slot's freeze waits behind a setup run. The freeze is best effort in the pod. A compromised `netagent` restarts on exit, which loses its memory. `keepalive` protects volumes against container exit, and it does not protect them against eviction or a compromised guest or Sentry.
- **Open tunnels.** Quarantine does not end an open tunnel through a compromised `netagent` until the pool's cap or a replica drain, because Envoy has no per-tunnel kill. A drain interrupts other tenants. Exfiltration can continue in that window, and `egress.accesslog` records it.
- **Kernel or Sentry compromise.** An attacker who controls the Sentry can stop trace points and forge them, and an escape reaches the collector and any forensic Job on that node. A Kata pool has no `observed` activity evidence in the first version.
- **Coverage gaps.** Drops, a disconnected sink, redaction misses, and a runtime that reports nothing all leave holes. `coverage` and gap records state what was missed, as far as they can tell, and absence of a record is never proof.
- **Agent evasion by interpretation.** An agent can read a file through a long-running interpreter with no new `exec`, and a `file` aggregate shows a count per directory rather than each file. A symlink or a raw relative path can evade the watchlist, because `Open` carries a raw `pathname`, and a bind mount or hard link can evade a path filter on a host sensor.
- **Process-to-tunnel attribution.** On gVisor the join is a time-based correlation, so a busy pod or a reused connection leaves the answer ambiguous.
- **Erasure on a shared pod.** Records under a pod-incarnation key, which are a concurrent-slot pod's `adapter` and `netagent` records, stay encrypted after one user's erasure until every user on that pod is erased or retention ends. Each operator assesses whether that retention is acceptable on those pools.
- **Evidence before a hold.** Under a floor of `none` and a key delay of 0, a tenant can stop collection or erase a user before any hold exists, and that evidence is gone. A deployment choosing those defaults accepts this loss.
- **Privacy of the evidence itself.** Activity streams record what developers and agents did, including paths and commands that identify people, and redaction is imperfect. A held key survives an erasure request. Captures contain all process memory. The operator assesses its own basis, and its customer agreement should state collection, retention, and operator access.
- **Traffic outside Envoy.** The raw sockets of a pool with `egressProfile: internet` and every connection at level 1 have no Envoy record. Without activity coverage or an imported proxy or flow log, `attribute` can only list the pods it cannot speak for.
- **Tipping off.** FP3 hides holds from tenant-readable surfaces. A pattern of sessions failing with a generic reason, and a slower first fetch after a store hold, remain observable, and a tenant could infer an investigation from them.
- **Platform trust.** A database superuser, a cluster admin, a compromised `lenny-evidence`, and a platform admin who grants itself a role are all inside the boundary. The audit chain, pgaudit, and the SIEM copy detect some of this after the fact, as §11.7 states. A snapshot volume holds other tenants' stores until extraction.
- **Compromised mirror parent.** It can forge `enforced` `git.operation` records (the limit [the egress design](egress-and-git-design.md) states), and it can ignore the held flag. The snapshot is the check, taken up to 10 seconds later.
- **Capture methods untested.** The gVisor and Kata memory captures are unproven on Lenny's node configuration, and an operator should test them before needing them.

## Defects found while verifying

These exist in today's tree independent of this design and are filed in `BUILD-GAPS.md`. No FE proposal closes them.

| Finding | Severity | Defect |
| :-- | :-- | :-- |
| F-12.8.64 | Medium | Clearing a session hold deletes escrow objects with no check that a hold covered them, including an artifact held under its own separate hold. FE-3 keeps its security copies out of these records. |
| F-15.1.73 | Medium | The `POST /v1/admin/legal-hold` body differs between §15.1 and the code, so a client that follows §15.1 is refused. |
| F-11.7.27 | Medium | §11.7 and the OCSF translator pair four class numbers with the wrong class names. |

The `audit_range` row under "Problem" is already recorded: F-12.8.7 and F-12.8.24 defer the ledger types, and F-12.8.45 records the preflight's coverage.

## Specification sections affected

- §4.4.4: the statement that the size-limit eviction reaches a quarantined pod. §4.4.3 takes no edit from this design, because [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) removes its embedded `SIGSTOP` checkpoint path and liveness rule.
- §4.7.10 and §28.5.3: the `keepalive` container, the statement that agent pods carry no liveness probe, the `freeze` request on each slot's `CH-SUPERVISE` with the rule that the adapter cancels an outstanding setup run and sends `freeze` after `setupdone`, and the preservation state, which suppresses every slot release.
- §4.6.1, §4.6.3, and §6.2: the `quarantined` binding state and phase, the projection and drain edges, the rollout's skip of a quarantined Sandbox, which [`runtime-revisions-design.md`](runtime-revisions-design.md) stages, orphan GC exclusion, the label check in `lenny-sandboxclaim-guard`, the forensics reconciler's PDB and VolumeSnapshot grants and its write of break-glass `pod_quarantines` rows, and role-based access control (RBAC).
- §4.7, §28.3, and §28.5: `CH-PRESERVE`, `CH-ACTIVITYREPORT`, and the `activity` frame on `CH-MSGSOCK`, with register rows. These identifiers are proposals under the naming law.
- §4.7.11: the adapter's validation of `activity` frames.
- §5.2, §5.3, §17.2, and §17.5: the `activityMonitoring` pool field and the monitored RuntimeClass name, with admission inventory and preflight checks. The collector's `lenny-node-agents` namespace and the capture Job's `lenny-forensics` namespace, with their Pod Security levels and the named node agent tolerations, land in the placement section that [PM-5](placement-design.md#pm-5-rendering) and [PM-9](placement-design.md#pm-9-namespaces) stage, and this design supplies their rows.
- §7.3 and §10.1: `orphan_pod_terminated` as the surfaced reason, and the `resume` disposition.
- §10.1: the preservation state beside hold state, and `CH-PRESERVE` accepted during hold.
- §10.2: the `platform-forensics` role and permission rows.
- §11.2.1: the `evidence.usage` billing event and `evidenceQuotaBytes`.
- §11.7: the `forensics.*` events under CMP-058, the anchor retention rule, and a correction of the "6004 File System Activity" class name (OCSF v1.1.0 lists File System Activity as class 1001, and Datastore Activity as 6005), filed with the paragraph's other wrong class names as F-11.7.27.
- §11.8: quarantine, security holds, the evidence reconciler, and the capture runbook as first-responder primitives.
- §12.2 and §12.6: `pod_quarantines` with its `origin` column (`api` or `break_glass`), `security_holds`, cases, `evidence_session_keys`, and `EvidenceStore`'s escrow operation.
- §12.8: removal of `audit_range` and `workspace_snapshot` from the ledger in step 0(c) and Phase 3.5(c), a separate security-hold step in teardown, `EnsureCopied` on every deletion path, including the reclamation and purge of a platform pack, the copy of every platform pack a held snapshot or output file references, hold basis and deferred erasure, key destruction, mirror purge, the retention class, and the post-restore gate.
- §12.9: rows for activity streams (Confidential on every tenant), captures, and retained evidence, under the class names of [TSS-8](tenant-security-settings-design.md#tss-8-data-classes).
- §12.5 and the tenant security settings section that [TSS-4](tenant-security-settings-design.md#tss-4-tenant-key) stages: evidence data keys are wrapped by the tenant key while its state is `active`, keep that wrapping when the state becomes `retained` (decision 28), and count toward the move from `retained` to `none`, and `lenny-evidence` and `lenny-evidence-reconcile` hold rights on tenant keys.
- §13.1 and §13.2: the `keepalive` container identity, selector exclusions on every agent policy, the collector and evidence reconciler rows, and the deletion rule in `lenny-label-immutability`.
- §15.1: the new admin endpoints, error codes (`QUARANTINE_NO_POD` and `SECURITY_HOLD_REGION_UNRESOLVABLE`), the narrowed legal-hold error text, and the correction of the `POST /v1/admin/legal-hold` body, which the specification gives as `resourceType` and `resourceId` while the code takes `tenantId`, `sessionId`, and `artifactId`.
- §15.4 and §15.7: the `activity` report in the adapter specification and the SDKs.
- §16.1, §16.4, §16.5, and §16.7: new metrics, the partition GC guard, the alerts (`QuarantineAged`, `HoldReviewDue`, `EvidenceDisagreement`, `ReconcilerStale`, and `ActivityCoverageLost`), and the event catalog.
- §17.7 and §25.7: `forensic-capture` and `abuse-report` runbooks.
- §17.8: collector and evidence reconciler sizing, and the updated manifest bound.
- §24: a forensics subsection beside §24.11.
- §25.9 and §25.11: the partition drop guard and the restore gate.
- §28.4: claim-register rows for parts of the contract that do not yet hold in code.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| FE-0 | Cases and custody | The case record, the `forensics.*` events, the `platform-forensics` role, and the `lenny-ctl` subsection. |
| FE-1 | Quarantine | The API, the row and label, the binding state's writer, the phase, every retirement path, the preservation RPC, the `keepalive` container, the freeze, the policy selectors, the webhook rules, the PDB, and release. |
| FE-2 | Mirror store hold | The store APIs, the flag's setter, sealing with a fresh generation, the snapshot, and purge. |
| FE-3 | Security holds and the retention class | The hold API and basis, separate records, continuous copy, the audit segments, the marker's setter, the GC guard and `EnsureCopied`, the §12.8 ledger change, deferred erasure, escrow of evidence and keys, and the retention class. |
| FE-4 | Reconciler | `pod.address`, `lenny-evidence-reconcile`, the rules, `evidence.reconcile`, imports, and the API. |
| FE-5 | Capture runbook and abuse workflow | The unbound role, the published capture image, capture registration, `attribute`, export and verify, and the runbooks. |
| FE-6a | Tool activity | `agent.activity.tool`, the key store, the SDK and adapter report, the coverage API, the read API, and redaction. |
| FE-6b | Process and file activity | The collector, `activityMonitoring` and the monitored class, `agent.activity.exec`, `.exec.args`, and `.file`, the producer contract, and the Tetragon reference forwarder. It starts with the spike under "Validation owed". |

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| FE-0 | None | It adds records and a role. |
| FE-1 | FE-0, [EG-2](egress-and-git-design.md#proposal-split), [EG-7](egress-and-git-design.md#proposal-split), [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path), the slot containers and slot release of [`session-isolation-design.md`](session-isolation-design.md), and the `setup` protocol of [WM-3](workspace-materialization-design.md#wm-3-the-protocol) | The case record, the `quarantined` binding state, which EG-2 adds and EG-7 carries to level 1's tenant policies, the removal of the §4.4.3 liveness rule of the `SIGSTOP` checkpoint path, the per-slot `CH-SUPERVISE` that carries `freeze`, the slot release that the preservation state suppresses, and the cancel pipe |
| FE-2 | FE-0, FE-3, [EG-5](egress-and-git-design.md#proposal-split) | The case record, the `security_holds` row that purge reads, and the held flag |
| FE-3 | FE-0, [EG-10](egress-and-git-design.md#proposal-split) | The case record, the held marker, and `EvidenceStore` |
| FE-4 | [EG-2](egress-and-git-design.md#proposal-split), [EG-3](egress-and-git-design.md#proposal-split), [EG-5](egress-and-git-design.md#proposal-split), [EG-10](egress-and-git-design.md#proposal-split), FE-1 | The streams it reconciles, and the quarantine rule |
| FE-5 | FE-0, FE-1, FE-2, FE-3, FE-4, [EG-8](egress-and-git-design.md#proposal-split) | The procedures it documents, the `pod.address` and `egress.accesslog` checks in `attribute`, and the cross-tenant query |
| FE-6a | [EG-9](egress-and-git-design.md#proposal-split), [EG-10](egress-and-git-design.md#proposal-split), FE-3 for its hold behaviour | The named permissions, the envelope, ingest, the key field, and the marker. It needs FE-3 only for the hold rule, and a release before FE-3 ships without that rule. |
| FE-6b | FE-6a, [EG-11](egress-and-git-design.md#proposal-split), the named node agents of [PM-5](placement-design.md#pm-5-rendering) and the `lenny-node-agents` namespace of [PM-9](placement-design.md#pm-9-namespaces) | The streams and keys, the PKI, and the collector's tolerations and namespace |
| FE-4's `R-ACTIVITY` rule | FE-6b | It lands with FE-6b, so FE-4 does not wait. |

The table in [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) lists FE-1, FE-2, FE-3, FE-4, and FE-6 with their EG dependencies, and the rows above add FE-5, FE-6a, and FE-6b to it. That table lists EG-2 alone for FE-1, while the egress design's `quarantined` hook names EG-2 and EG-7, so the FE-1 row above adds EG-7. No shared queue file exists, and proposals from different designs interleave as long as these hold.

| Other design | Relationship |
| :-- | :-- |
| [`security-oversight-design.md`](security-oversight-design.md) [SO-1](security-oversight-design.md#so-1-security-roles) | SO-1's `security-auditor` holds an activity read permission only through `auditorStreamGrants`. Neither waits for the other, because `tenant-admin` holds them throughout. |
| [SO-3](security-oversight-design.md#so-3-per-tenant-siem-destination) | A per-tenant SIEM receives activity streams through its export. FE-6a states what crosses it, and SO-3 states the destination. |
| [SO-4](security-oversight-design.md#so-4-operator-access-transparency) | SO-4 must exclude the `forensics.*` prefix from what it mirrors to a tenant, unless a case discloses it. If SO-4 lands first, FE-0 adds the exclusion. |
| [SO-7](security-oversight-design.md#so-7-f-25440-lenny-ops-rights) | FE-5 adds no right to `lenny-ops`, so it does not conflict with SO-7's narrowing. |
| [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) | Holds and streams use the tenant, and an environment filter can be added to reads later. No dependency. |
| [`user-credential-delivery-design.md`](user-credential-delivery-design.md) | Its `quarantined` handling needs nothing here. FE-6 takes nothing from `leakScan`. A link's `link_ref`, under ["Stored links"](user-credential-delivery-design.md#stored-links) in [UC-2](user-credential-delivery-design.md#uc-2-the-oauth2link-source-and-account-linking), follows "Delivered user tokens". |
| [`github-credential-addon-design.md`](github-credential-addon-design.md) | Child tokens, under [GH-2](github-credential-addon-design.md#gh-2-pull-request-authoring-on-github), follow "Delivered user tokens". No dependency in either direction. |
| [`runtime-revisions-design.md`](runtime-revisions-design.md) [RV-3](runtime-revisions-design.md#rv-3-pods) and [RV-4](runtime-revisions-design.md#rv-4-rollout) | A rollout's deletion of a pod set skips a quarantined Sandbox and its pair, which that design stages as its RVR-10, and the "Retirement paths" row for `Contracting` records it here. Neither waits for the other, because the skip reads a label that nothing sets until FE-1 lands. |
| [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) [IC-6](incremental-checkpoint-design.md#ic-6-retention-garbage-collection-erasure-and-legal-hold) and [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) | IC-6 defines a security hold's copy of a checkpoint snapshot, with every session pack and platform pack it references, and calls `EnsureCopied` before it deletes a snapshot, a session pack, or a platform pack. FE-1 depends on IC-9, as the table above states. |
| [`session-isolation-design.md`](session-isolation-design.md), [`agent-pod-layout-design.md`](agent-pod-layout-design.md), and [`workspace-materialization-design.md`](workspace-materialization-design.md) | They define the slot containers, the per-slot `CH-SUPERVISE` and its slot release, the volumes FE-5 copies, `keepalive`'s place in the pod blueprint, and the `setup` protocol with its cancel pipe. FE-1 and FE-5 read them as fixed inputs. |
| [`placement-design.md`](placement-design.md) | It places the collector and the capture Job as named node agents, owns their namespaces, and skips a quarantined pod in its drift drain. FE-6b depends on it, as the table above states. |
| [`tenant-security-settings-design.md`](tenant-security-settings-design.md) | Its tenant key state decides which key wraps an evidence data key, its recorded tenant deletion deadline bounds the worked example's teardown, and its data classes name the classes this design uses. Decision 28 asked it for one change, which its revision 4 applies. Its `storage.platformChunkReuse` decides whether a tenant's snapshots reference platform packs that a hold must copy. |

## Changes requested of egress-and-git-design.md

That document carries every earlier request this design made: `egress.accesslog`, `netagent`'s local client port, `sequenceEnd`, the pod-incarnation key in `payloadKeyRef`, the per-node producer identity with `spec.nodeName`, the marker in `EvidenceStore.DeleteByTenant` with its escrow operation, the persisted fetch UIDs, store generations, the `quarantined` inputs and precedence, a quarantined pod's records under the platform prefix, a refresh against the current generation, and the manifest bound. This design consumes them as fixed inputs. Its `userCredential` kind reaches this design through `quarantined` with no change. "Review history" at the end of that document records the revisions that applied them.

This design requests the following further changes:

| Request | Where in that document | Why |
| :-- | :-- | :-- |
| The held-flag hook keeps the fetch UIDs of a held store out of the pool up to `gitMirror.heldUidBudget`, and past that budget a hold re-chowns the store after recording an ownership manifest. The **Reserved hooks.** test under its tier 4 tests pins both. | "Reserved for other designs", the held-flag row, and "Tier 4: integration" | FE-2, "Fetch UIDs". The owner settled the budgeted exception on 2026-11-09. |
| `EvidenceStore.DeleteByTenant` escrows an unmarked batch of a stream with `retentionClass: security` that falls inside the stream's retention window, and rewraps its keys, by the operation that escrows marked batches. | "Evidence streams", **Erasure and holds.** | FE-3, "Security retention class". |
| The attribution row of the envelope states that a quarantined pod's records under the platform prefix keep their tenant in the payload. | "Evidence streams", the attribution row of the envelope | FE-6, **Records after a quarantine.**, and tenant deletion of those records. |
| `pkg/binding` resolves a pod's slot index to the session bound in that slot at a given time, so that ingest attributes a record from the container `runtime-<n>` to one session. | "Evidence streams", the attribution row of the envelope | FE-6, attribution on a concurrent-slot pod. |

The local client port in `egress.local` gives no exact join on gVisor, because `Connect` has no local port (see FE-6). This design keeps the field for a sensor that can supply the port.

## Changes requested of other designs

| Design | Request | Why |
| :-- | :-- | :-- |
| [`tenant-security-settings-design.md`](tenant-security-settings-design.md) | Applied in its revision 4 under decision 28. Under O1 (a), count the evidence data keys in `evidence_session_keys` that the tenant key wraps toward the move from `retained` to `none`, beside the objects under the tenant key, and name `lenny-evidence` and `lenny-evidence-reconcile` in **Other users of the key.** of TSS-4 as holders of rights on tenant keys. | "Keys" under FE-6. A key state of `none` must not leave evidence keys wrapped by a key that is no longer kept. |
| [`session-isolation-design.md`](session-isolation-design.md) | Applied in its revision 5, whose SI-3 paragraph **A quarantined pod.** gives a quarantined pod no slot release and whose **Runs.** paragraph answers `freeze` with `worker_busy` during a run. The request was: state in SI-3 that an adapter in the preservation state of FE-1 runs no slot release and sends no `end`, `sweep`, or `cleanup`, and that `freeze` is a request the supervisor answers with `worker_busy` during a setup run. | "Retirement paths" and "Freezing the agent" under FE-1. A release would empty the slot's volumes. |
| [`workspace-materialization-design.md`](workspace-materialization-design.md) | Applied in its revision 2. Restate WM-3 and WM-11 for the asynchronous supervisor worker that the owner approved on 2026-11-12: during a run the supervisor serves `fs`, `ping`, and `end` and answers every other request, `freeze` included, with `worker_busy`, and the cancel pipe is passed descriptor index 2 with no `cancelFd` field. | "Freezing the agent" under FE-1 relies on that protocol. |

The placement design already carries this design's other requests: the named node agents of PM-5 with `forensics.activity.nodeSelector`, the `lenny-forensics` and `lenny-node-agents` rows of PM-9, which this design fills with the level `privileged` and the admission narrowing of FE-5 and FE-6, and the drift drain's skip of a quarantined pod in PM-6.

## Documentation

Each proposal carries the changes for its own surface, and spec changes land first, as [the egress design](egress-and-git-design.md) states. Pages describe behaviour with no section numbers, under `doc-content.md`.

| Location | Change | Proposal |
| :-- | :-- | :-- |
| New operator guide page "Security investigations" | Owns cases, quarantine, security holds and how they differ from legal holds, mirror holds, the evidence reconciler's findings, and custody. It states what the tenant can and cannot see, and what the operator must disclose in its agreement. | FE-0 to FE-4 |
| New operator guide page "Agent activity evidence" | Owns the streams, coverage by isolation profile and pod model, the `gvisor-monitored` setup, redaction and its limits, keys, cost and the sizing formula, the producer contract, and how to read `observed` against `self_reported`. | FE-6a, FE-6b |
| Operator guide "Evidence and visibility" (a page in the egress design's chapter) | Links to the two pages above, and gains `egress.accesslog` and `pod.address`. | FE-4 |
| `docs/runbooks/forensic-capture.md`, `abuse-report.md`, and `leaked-secret.md` | New runbooks in the §25.7 format, with the capture table per profile. | FE-5 |
| `docs/runbooks/legal-hold-quota-pressure.md` | States that security holds do not count here. | FE-3 |
| Operator guide "Security investigations", holds section | States that a held snapshot or output file is copied with every platform pack it references, that the deletion or purge of a platform pack waits for `EnsureCopied` and fails while a copy fails, that a purge of a warm repository's or a runtime's content leaves the escrowed copy under the hold, and that a platform contribution names the pod that added it. | FE-3 |
| Operator guide "Security investigations", quarantine section | States the in-pod freeze's limits: a setup run is cancelled first, so its processes are lost from memory, the delay is bounded by the run's `timeoutMs` plus 5 seconds, and under gVisor a slot process can stop or kill its supervisor first, which leaves only the node-side freeze. | FE-1 |
| Operator guide `security.md` and `audit-ocsf.md` | `security.md` summarizes incident primitives and links out. `audit-ocsf.md` gains the new classes and corrects the File System Activity class. | FE-0, FE-6a |
| Operator guide `namespace-and-isolation.md` | States which evidence each isolation profile yields, because that now informs the choice of profile, and documents `activityMonitoring`. | FE-6b |
| Operator guide `lenny-ctl.md` and `docs/reference` | The new commands, endpoints, errors, metrics, and alerts. | Each |
| Runtime author guide `platform-tools.md` | States that platform tool calls are recorded as `enforced`, that the SDK's `activity` report records the runtime's other tool calls as `self_reported`, and that a call to an operator service reaches the record as egress traffic, with no per-call record in Lenny. | FE-6a |
| Runtime author guide `runtime-sdk.md` | The `activity` report and `reportActivity`, what to report for a shell tool, the redaction applied, and that reporting is optional, needs no capability, and is self-reported. | FE-6a |
| Runtime author guide `runtime-configuration.md` | States that monitored pools record the runtime's processes and files, and that a process-heavy runtime pays the file stream's cost. | FE-6b |
| Runtime author guide `lifecycle.md` | States that a runtime can stop receiving messages and be stopped with SIGSTOP at any time, with no notice, and that a setup command or materialization run can be cancelled first. | FE-1 |
| Operator guide `namespace-and-isolation.md` and the placement page | The `lenny-node-agents` and `lenny-forensics` namespaces, their Pod Security level `privileged`, and the admission rules that narrow each to its named node agent. | FE-5, FE-6b |

The pages list every setting of "Configuration by scope" with its default, every alert with its runbook, every error code, and each item of "Residual risks" as a limit. The page states the defaults of decision 29: `forensics.quarantine.maxPods` 10, `gitMirror.heldUidBudget` a quarter of the mirror's fetch-UID range, and an hourly `lenny-evidence-reconcile` schedule.

## Tests

These describe behaviour to pin. None exists yet. Every test carries its `// spec:` annotation, and tests at test tier 2 and above carry `// diagnosis:`, a comment stating what a failure means.

### Tier 0: static

- Every rendered NetworkPolicy in an agent namespace other than `default-deny-all` carries the quarantine exclusion, in every chart configuration that renders one, including `internet`, `kube-dns`, `llm-proxy`, and `otlp`. A new policy without it fails.
- The `lenny-forensics-capture` ClusterRole holds no `nodes/proxy` and is bound by nothing the chart renders.
- The collector's pod template passes `pkg/podsecurity` and holds no capability, and `lenny-pod-security` refuses any other pod template in `lenny-node-agents` and any pod in `lenny-forensics` other than a capture Job.
- `schemas/` and the OCSF mapping cover each new event type and stream.

### Tier 1: unit

- **Projection.** Each row of the retirement table, with and without the label, computes the stated phase.
- **Binding.** The label alone, the row alone, both, and neither yield the stated state, and quarantine takes precedence over every other state.
- **Holds.** Marker recomputation on release covers overlapping holds, an open-ended range, and a release while another hold still covers a row.
- **Redaction.** Each structural pattern, entropy threshold, truncation boundary, and credential path class, with values from a corpus of known secret forms and a corpus of harmless arguments.
- **Fingerprints.** A value matches its own fingerprint under the pod-incarnation key, and not under another key. A collector with no issued key writes the value as unfingerprinted redaction.
- **Attribution.** On a concurrent-slot pod, a record from `runtime-<n>` takes the session bound in slot `n`, a record from `adapter` or `netagent` takes `candidateSessions` and the pod-incarnation key, a process that changes directory selects no other session or key, and `workdirHint` feeds nothing else.
- **Key wrapping.** A data key generated while the tenant key state is `active` is wrapped by the tenant key, one generated under `none` or `retained` by the regional evidence key, and a key wrapped by the tenant key stays readable after the state moves to `retained`.
- **Floor.** A tenant write below the floor is refused, and an erasure with a delay can be stopped by a hold set inside it.
- **Hold basis.** A hold without both basis fields is refused, and each basis defers erasure or does not, per the configured list.
- **Aggregation.** Windows, counts, watchlist exact records, and collapse above the rate limit, including the empty window and the boundary at the limit.
- **Reconciler rules.** Each rule on matching inputs, on a disagreement, on a lossy input, and on a suspect tag that must not attribute.
- **Coverage.** A timeline with a start, a loss, and no producer returns the stated intervals.

### Tier 2: component

- The WarmPoolController projects `quarantined` from the label, excludes the pod from warm counts, and releases to `draining` only after the row's release.
- `lenny-label-immutability` refuses a deletion and a label removal from every caller except the controller after release, and it admits a break-glass group's stamp.
- `lenny-sandboxclaim-guard` refuses a claim for a labelled pod, including a race with a stamp.
- The WarmPoolController's drift drain stamps no drain request on a quarantined pod whose node no longer matches its placement, and stamps one on an idle pod that is not quarantined.
- The forensics reconciler creates a `VolumeSnapshot`, reports an absent class, and retries an incomplete step after a restart. The forensics reconciler creates the PDB, and writes a break-glass row with `origin: break_glass` once Postgres is reachable.

### Tier 3: contract

- `CH-PRESERVE`, `CH-ACTIVITYREPORT`, and the `activity` frame against their schemas, including oversized frames and a frame that carries a `sessionId` member, which is ignored, so the record takes the session of the slot that sent it.
- The gVisor remote sink's wire format against the collector, across each `runsc` version the chart allows.
- The capture registration's presigned `PUT` refuses another key, another method, and a different length.
- Each new stream's envelope, including a chunk and a pod-incarnation key.
- The producer contract with an allowlisted identity, an unknown identity, and a record naming a pod on another node.

### Tier 4: integration

- **Holds on the compose stack (the test tier 4 environment of Postgres, Redis, and MinIO).** A hold's audit segment verifies as a chain after the live rows are deleted, for a tenant scope and for a session scope, each copying every tenant row in range. A held artifact survives rotation and deletion of its original, and the tenant's quota and legal-hold list are unchanged.
- **Attribution coverage.** On a pool with `egressProfile: internet` and no tunnel record, `attribute` returns no candidate from Envoy, lists the pod under `coverage`, and finds it through a `connect` record by destination. At level 1 it lists every pod. An empty result always carries `coverage`.
- **Basis across paths.** A `security_incident_response` hold loses its copies, keys, and store on `DeleteByUser` and at Phase 3.5 alike, and a `legal_claims` hold keeps all three on both, with the configured default list.
- **Records after a quarantine.** They stay out of tenant reads, carry their tenant in the payload, are found and removed by tenant deletion when no deferring hold covers them, remain for the set period after release, and a key destroyed by erasure makes activity payloads unreadable.
- **Open-ended holds.** Audit rows, artifacts, checkpoints, and transcripts created after the hold are copied, the partition GC waits for rows above the cursor and drops afterwards, every deletion path copies first, and a copy failure refuses the deletion. Phase 3.5 flushes before Phase 4.
- **Platform packs.** A held snapshot whose files match a warm store's and a runtime's platform chunk sets is copied with every platform pack it references, and the escrowed snapshot restores from the escrow prefix alone. Pack reclamation, an operator's purge of a source's packs, and `DeleteByTenant` of a tenant-owned set each wait for `EnsureCopied` on a held platform pack, a copy failure refuses the deletion, and the escrowed copy survives the purge.
- **Ledger.** A security hold produces no `ERASURE_BLOCKED_BY_LEGAL_HOLD` or `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`.
- **Separate records.** `POST /v1/admin/legal-hold` with `hold: false` on a session under a security hold deletes no security copy and returns no `escrowReleased` count, on a live session and on a tombstoned tenant.
- **Erasure.** `DeleteByUser` during a hold returns the normal response, deletes the originals, skips held keys, and completes at release, with the stated receipt fields, and a user with no hold follows the existing path.
- **Teardown.** Phase 3.5 escrows held evidence and rewraps its keys, Phase 4a destroys the tenant key, digests and anchors verify afterwards, and decryption works under the escrow KEK. A region with no escrow entry fails closed.
- **Retention class.** Batches of a stream with `retentionClass: security` survive teardown for the set period whether or not a hold marked them, and are deleted afterwards. Batches of other streams are removed.
- **Keys.** Key destruction makes a payload unreadable while its digest verifies, and a retention expiry destroys the key after the last batch.
- **Sessions.** `sessionDisposition` `fail` and `resume`, on a single-session, a concurrent-slot, and a warm pod, and a delegation tree. The `fail` session shows `orphan_pod_terminated` and does not retry.
- **Order.** With a test adapter, the row and binding push precede `CH-PRESERVE`, `CH-PRESERVE` precedes the label, and an unacknowledged RPC continues at the timeout.
- **Freeze behind a setup run.** With a test supervisor running a setup command or a helper run, the adapter closes the cancel pipe, sends `freeze` only after `setupdone`, and reports the delay, which stays within the run's `timeoutMs` plus 5 seconds. A `freeze` sent during the run receives `worker_busy` and freezes nothing.
- **Restore.** A restored database replays no erasure that a hold deferred.

### Tier 5: end to end on Kind

- A quarantined pod keeps its volumes through a force-terminate, a tenant suspension, a recycle limit, an uptime limit, a pool deletion, a tenant deletion's Phase 5, a coordinator hold timeout, and a `kubectl delete`, and each is refused or skipped as stated.
- With `keepalive`, exiting the adapter and every slot container leaves each slot's `/workspace`, `/session`, and `/tmp` readable on the node. Without it, the same exits remove them, which pins the kubelet inference. A pod with no quarantine retires as before when its adapter exits.
- In-pod freeze stops every process in every slot container of a pod with one slot and of a pod with several slots, and each slot's `CH-SUPERVISE` keeps working.
- A quarantined pod's sessions end with no slot release: each slot's processes stay frozen, and its volumes keep their files.
- No agent pod renders a liveness probe, and a node-side freeze of a slot container does not lead the kubelet to kill it.
- The capture Job's step 4 copies each slot's `/dev/shm`, `/tmp`, `/run/lenny`, `/session`, and `/workspace` from the node, by volume name, on a pod with several slots.
- A runtime rollout's `Contracting` phase on a pool holding a quarantined Sandbox deletes every other pod of the old revision's pod set, keeps the quarantined Sandbox and its pair, and reports it to the forensics reconciler. The release then terminates the pod, and the pair goes.
- A quarantined pod's workspace filled past its `sizeLimit` is evicted, and the forensics reconciler records `forensics.evidence_lost`, which pins the residual.
- `kubectl drain` waits on the PDB, and the release lets it complete.
- The agent policies apply after the label on Calico or Cilium, because kindnet, Kind's default network plugin, is not evidence of production enforcement, and new connections to the gateway, object store, and mirror fail.
- A mirror store hold stops re-chown, repack, purge, and fetches on the sealed generation, and a held public store's files stay unwritable by a later fetch. A fresh generation serves fetches and read-after-push at once, and refs stay current. With `gitMirror.heldUidBudget` exhausted, a further hold records an ownership manifest that matches the on-disk owners and modes before it re-chowns, and its retired UIDs return to the pool.
- The kubelet checkpoint path under `isolationProfile: standard`, with CRI-O or containerd with CRIU, produces an archive, and a runtime without it returns the documented error, which the runbook handles.
- The Tetragon reference forwarder writes `agent.activity.exec` for a pod of a pool with `isolationProfile: standard`.

### Tier 6: end to end on cloud

- On gVisor nodes: the collector receives `exec` and `file` records with correct attribution on a pod with one slot and on a pod with several slots, including setup and materialization processes in a slot container, runs on a dedicated gVisor node through its tenant toleration, `ignore_setup_error: false` fails a sandbox with no collector, a collector restart produces `coverage: lost`, `activityMonitoring` selects the monitored class, and an unmonitored pool on the same node produces nothing. The join reports `time_correlated` on `Connect` records.
- A host eBPF sensor on the same node records no process inside the gVisor and Kata pods, which pins the coverage table.
- `runsc pause` and `runsc checkpoint --leave-running` through the published image, on an unfrozen and a frozen pod, produce a registered archive. The Kata capture method, once chosen, gets the same test.
- GKE Sandbox as a managed configuration of the monitored class, where it is available.

### Tier 7: load

- `exec` at the rate limit on many pods, with the collector and ingest at the assumed capacity tier 3 average and at a peak multiple, and the measured effect on a build workload against an unmonitored pool, with and without `openat` traced.
- A flood from one tenant's pod produces gaps for that tenant alone.
- Concurrent holds, retention sweeps, and compaction lose no marked row.
- The evidence reconciler completes a tenant-hour within its hourly schedule at the sized evidence volume, and its run duration and object store request cost are recorded (decision 29).

### Tier 8: chaos

- Quarantine during a Postgres outage (break glass), where `GET /v1/admin/quarantines` reports the pod as `unregistered` until Postgres returns and the forensics reconciler then writes one row with `origin: break_glass` and one custody event, during an API server outage (store re-read), and with a lost binding push (the 30-second bound).
- A node eviction of a quarantined pod records `forensics.evidence_lost`.
- A crash of the gateway between each step of the quarantine order leaves the forensics reconciler to finish it, with no pod retired. An unreachable adapter at `CH-PRESERVE` produces the stated status, and the volumes survive the hold timeout.
- `lenny-evidence` loss during materialization, during activity ingest, and during a sweep leaves nothing deleted.

### Tier 9: security

- A tenant admin's every read (audit query, legal-hold list and clear, quota, retention, error codes, session fields, every stream read and export including sequence continuity, and the erasure and teardown receipts) shows no sign of a hold, quarantine, or case, which pins FP3.
- A process at a slot UID cannot signal `keepalive`. Raw egress on a pool with `egressProfile: internet` stops for new connections after the label.
- A compromised `netagent` stand-in leaves its tunnels open under quarantine, and R-TUNNEL-AFTER-END fires. Its forged reports for another session are rejected.
- A compromised decision service stand-in drops records and forges tags, and R-TUNNEL-NO-DECISION and R-ATTRIBUTION fire, and the Token Service refuses a quarantined pod regardless.
- A forged collector record for a pod on another node is rejected.
- A process at a slot UID cannot read the collector's socket, or reach the capture Job's image, and no secret from a seeded corpus reaches a stored record in plaintext.
- A capture cannot be read without `forensics.capture.read`, and an archive is encrypted when it leaves the node.
- A retired fetch UID under a hold cannot write a held public store.

### Tier 10: conformance

- A runtime that sends `activity` frames, one that sends none, and a misbehaving one that sends oversized frames or frames naming another session in a `sessionId` member each conform, and the adapter's behaviour matches the `CH-MSGSOCK` contract card in §28.5.3.
- Quarantine leaves each reference runtime with no message and no `session_end`.

### Tier 11: documentation

- Each new alert resolves to a runbook, and the runbooks pass the §25.7 front-matter checks.
- The documented coverage table matches the tier 6 results, and each documented example command parses.

## Open decisions

No decision is open. O1 is kept below as the record of the choice the owner settled as decision 28.

**O1. Evidence keys wrapped by the tenant key when the key state becomes `retained`** (raised by [`tenant-security-settings-design.md`](tenant-security-settings-design.md) as I12).

While a tenant's key state is `active`, `lenny-evidence` wraps each new session and pod-incarnation data key with the tenant key ("Keys" under FE-6). A relaxation of `encryption.tenantKey` moves the state to `retained` ([TSS-4](tenant-security-settings-design.md#tss-4-tenant-key)), and new data keys are then wrapped by the regional evidence key.

- (a) Keep. Data keys wrapped by the tenant key stay so wrapped, and `lenny-evidence` unwraps them through the tenant key while it exists. The tenant settings design counts them, with the tenant's objects under the key, before the state moves from `retained` to `none`.
- (b) Rewrap. When the state becomes `retained`, a job in `lenny-evidence` rewraps every data key of the tenant under the regional evidence key, with one KMS decrypt and one encrypt per key.

**Recommendation: (a).** It matches TSS-4's rule that objects written under the tenant key keep it, so a tenant's evidence and its other data follow one rule, and Phase 4a's deletion of the tenant key still makes every evidence record wrapped under it unreadable. (b) costs a KMS call pair per data key and takes those records out of reach of the tenant key's deletion. (a) needs the one change to the tenant settings design listed under "Changes requested of other designs".

**Settled (2026-11-13): (a)**, in the owner's words "FE O1: keep" (decision 28).

## Decisions

Revision 24 recorded every decision as settled. Decision numbers are referenced above. Decisions 14, 20, and 21 are stated in this list, and the others are in the table:

- **Decision 14: network cut on quarantine.** Every agent allow policy excludes the label, including `allow-gateway-ingress`, and the label follows the acknowledged `CH-PRESERVE`. The cost is that a pod whose adapter did not acknowledge can lose `CH-MSGSOCK` at 120 seconds.
- **Decision 20: selecting monitored pods.** The `activityMonitoring` pool field and `runtimeClasses.monitored.name`, because one profile maps to one RuntimeClass. A new isolation profile would touch each profile-keyed rule with no change in isolation. A node-wide setting would charge unmonitored pools and fail every gVisor pod on a collector outage.
- **Decision 21: the `file` stream.** Off per pool until the spike reports the Sentry's cost, so early adopters have `exec` and tool evidence alone.

| # | Decision | Recommendation | Cost of the recommendation | Main alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Producer for agent activity | **Settled**, in the owner's words "go with your recommendations". Two layers: tool activity on every profile, and gVisor runtime monitoring for `observed` process and file evidence. | A new DaemonSet, node configuration, and a dependency on gVisor's trace points, whose compatibility and reconnection behaviour need a spike. | SDK only: no node component, and everything is `self_reported`. A host eBPF sensor sees nothing inside the default profile. |
| 2 | Kata coverage in the first version | **Settled**, in the owner's words "go with your recommendations". Supervisor tracing is the first follow-up. `tool_only`, stated in the API. | The highest-risk profile lacks process evidence, and `microvm` is what §5.3 recommends for that risk. | An in-guest sensor: a custom guest kernel and agent, as Ant Group built, with records that remain `self_reported`. |
| 3 | `isolationProfile: standard` coverage | **Settled**, in the owner's words "go with your recommendations". A public producer contract and a tested Tetragon forwarder, with no sensor in the chart. | A deployer installs a privileged DaemonSet itself. | A chart option, which ships a privileged DaemonSet on agent nodes in Lenny's chart. |
| 4 | Default state | **Settled.** Off at the deployment level, with a separate setting for tool and connector activity, for processes and commands, and for file access, in the owner's words "ok (separate config values for tool and connector activity, processes/commands, and file access)". A SaaS operator turns each on, and tenants choose within it. Under [`connector-removal-design.md`](connector-removal-design.md) (2026-11-15), connector activity no longer exists, and the tool setting covers platform tool calls and the SDK's reports. | No evidence exists for a tenant until someone chooses it, and an incident before that has none. | On by default at `tool` level, which is cheap and needs a customer agreement to state it. |
| 5 | Who reads activity | Settled by the owner with [SO decision 19](security-oversight-design.md#decisions): `tool`, `exec`, and `file` to `tenant-admin`, with `security-auditor` only through `auditorStreamGrants`, `args` only on grant, and the operator only under an open case. The `security-auditor` default is withdrawn. | A subject tenant admin can read its own streams, a customer's security team takes an explicit step and a delay, and the case requirement slows the operator. | Operator-only: simpler, and it removes the customer's evidence, which [`security-oversight-design.md`](security-oversight-design.md) exists to provide. |
| 6 | Does the tenant learn of a quarantine or hold | **Settled**, in the owner's words "no setting. Operators can decide to alert their customers by themselves (they can build the automation themselves, if they want it automated)". No signal, a generic existing failure reason, and disclosure as a recorded case action. An operator that wants to notify customers builds it on the `forensics.*` platform-tenant events, which reach its own SIEM copy and its §25.5 subscriptions. | A tenant sees a failure whose reason does not state the cause, and an operator can misuse silence. SO-4 must carve it out. | Immediate notice or a distinct reason: simple and transparent, and it warns a malicious tenant. |
| 7 | Erasure during a hold | **Settled**, in the owner's words "setting that controls whether a generic sentence or accurate result (generic sentence by default, configurable text)". Proceed, defer only under an approved basis, and carry `forensics.erasure.retentionStatement`, `generic` by default with configurable text on every receipt, or `accurate`. | The generic text appears on receipts where nothing was retained, and `accurate` tells a tenant that a hold exists. §11.8 calls the operator a processor, so the choice is each operator's legal assessment. | A receipt stating a retention, which hints at an investigation. Blocking erasure, which reveals the hold and delays the tenant's obligation. |
| 8 | Retention class default | **Settled**, in the owner's word "ok". 0 days in the chart, and a SaaS operator sets a period in its agreement, 90 days being a starting value. | Without it, offboarding removes evidence. With it, data outlives the tenant, on a basis the operator assesses. | A non-zero chart default, which makes the promise for every deployer. |
| 9 | Who may set a quarantine | **Settled**, in the owner's word "ok". `platform-forensics` and `platform-admin`. Tenants ask through the operator. | A customer's team cannot freeze its own pod quickly. | A tenant request permission limited to an exclusive pod, once the audit and cost rules are proven. |
| 10 | Default session disposition | **Settled**, in the owner's word "ok". `fail`. | A false positive ends a customer's session. | `resume`, which keeps work going and restores possibly tainted state. |
| 11 | Quarantine lifetime | **Settled**, in the owner's word "ok". A platform admin lists every quarantined pod through `GET /v1/admin/quarantines`, including break-glass ones. No automatic release, with an alert at 24 hours. | A forgotten quarantine holds a node from upgrading. Cluster upgrades can stall. | An automatic release at a deadline, which destroys evidence on a timer. |
| 12 | Release control | **Settled**, in the owner's word "ok". Two-person release and capture reads on by default for regulated tenants, and off elsewhere. | A small team needs a second person at release. | One person, with the audit event as the control. |
| 13 | Capture automation | **Settled**, in the owner's word "ok". No privileged Lenny controller. An unbound role and a published image, used through the deployer's privileged access. | Capture is slower and manual, and needs a practised operator. | An API-driven capture controller holding `nodes/checkpoint`, and privileged Job creation, at the cost of a standing high-value target. |
| 15 | Argument detail default | **Settled**, in the owner's word "ok". `redacted`. | Secrets with no recognizable form are stored, encrypted. | `metadata`, which loses the arguments an exfiltration investigation needs. |
| 16 | Who pays | **Settled**, in the owner's word "ok". The operator pays for quarantined pods and held bytes. Tenants are metered for activity bytes. | Operators carry investigation costs. | Charging the tenant, which reveals the hold. |
| 17 | Source independent of the decision service | **Settled**, in the owner's word "ok". Envoy's second logger to `lenny-evidence`, with the deployer's log pipeline and flow logs as an optional outside source. | A second stream of tunnel records, and a compromised `lenny-evidence` remains a common point. | A log shipper in `lenny-egress` with its own bucket credential, which is stronger against `lenny-evidence` and breaks the rule that no producer holds a store credential. |
| 18 | How a hold protects artifacts | **Settled**, in the owner's word "ok". Copy on hold to escrow. | Copy time and storage at set time. Later writes to a session need a named range. | An in-place flag, which is cheaper and changes rotation and quota in ways a tenant can see. |
| 19 | `onCoverageLoss` default | **Settled**, in the owner's word "ok". `continue`, with an alert and a gap record. | A dark pod keeps running. | `retire`, which turns a collector rollout into capacity loss. |
| 22 | Post-quarantine freeze | **Settled**, in the owner's word "ok". Automatic in-pod SIGSTOP on sidecar pods after the acknowledgement, with `freeze: false` to opt out, and a node-side freeze in the runbook. Since revision 31 every agent pod has slot containers, so the in-pod freeze applies to every slot, after any setup run is cancelled. | It changes process state, and a frozen process may affect checkpoint methods, untested. A slot with a setup run outstanding is frozen only after the run ends. | No freeze: the agent keeps running and can destroy evidence, though it is observed live. |
| 23 | Activity floor and key delay | **Settled**, in the owner's word "ok". `forensics.activity.floor` and `keyDestructionDelay`, with chart defaults of `none` and 0, and a SaaS operator raising them in its agreement. | A floor reduces the tenant's control over collection, and a delay postpones erasure of a key, on a period the operator assesses. With the defaults, a tenant can erase evidence before a hold exists. | No floor, leaving this to the tenant. A non-zero chart default, which makes the promise for every deployer. |
| 24 | Hold basis | **Settled**, in the owner's word "ok". A required enumerated `basis` and `basisRef` per hold, with erasure deferred only for `legal_obligation` and `legal_claims` by default. | Each hold needs the operator's own judgement of its basis, which slows the first minutes, so an investigator quarantines first and sets the hold once a basis exists. An incident-response basis alone loses material to erasure and to tenant deletion, and a store hold is treated the same way. | Treat security response as deferring by default, which asserts a legal conclusion in code. |
| 25 | `keepalive` container | **Settled**, in the owner's word "ok". A regular container in every agent pod, at a minimal request. | One more container per pod, including warm pods, a small density cost, a reserved UID, and an image to pin. | Rely on the adapter staying alive, which leaves a pod with an exited adapter losing the volumes. An ephemeral container at quarantine time, which is unverified and is subject to the cred-guard webhook. |
| 26 | Liveness of agent pods | **Settled**, in the owner's word "ok", and amended by the owner on 2026-11-09. Agent pods carry no liveness probe, which matches the tree. The amendment of §4.4.3 that this decision first carried, under which the adapter exits on `checkpointStuck`, is dropped, because [IC-9](incremental-checkpoint-design.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path) removes the `SIGSTOP` checkpoint path and the flag. | A deadlocked adapter goes undetected by the kubelet, until the gateway's gRPC keepalive probes on its pod connection and the orphan session reconciler act. | Keep a probe, so a freeze of a slot container leads the kubelet to kill it, or route it through `keepalive`, which adds a proxy to a component meant to do nothing. |
| 27 | Writer of a break-glass quarantine row | **Settled**, in the owner's word "ok". The forensics reconciler writes the row with `origin: break_glass` and a custody event once the session store (Postgres) is reachable, and `GET` reports the labelled pod as `unregistered` until then. The WarmPoolController projects the label and creates no row. | The row's principal is the forensics reconciler, so the Kubernetes audit log remains the record of who stamped the label. | The WarmPoolController writes the row when it projects the label, which gives a second writer of `pod_quarantines` and a controller loop that otherwise reads only informers. |
| 29 | Defaults of the quarantine alert, the held-UID budget, and the reconciler schedule | **Settled** on 2026-11-13, when the owner accepted the proposed values in the answers to the consolidated list of open decisions and asked to "record any owed validations". `forensics.quarantine.maxPods` defaults to 10, `gitMirror.heldUidBudget` to a quarter of the mirror's fetch-UID range, as [`egress-and-git-design.md`, decision 66](egress-and-git-design.md#decisions) also records, and the `lenny-evidence-reconcile` schedule to hourly. Each has an owed validation ("Validation owed", item 7). | An alert at 10 pods can fire late on a small deployment and early during a large incident, a quarter of the range limits concurrent fetches while stores are held, and an hourly reconciler finds a disagreement up to an hour late. | No chart default, which revision 32 left to the chart. |
| 28 | Evidence keys wrapped by the tenant key when its state becomes `retained` (O1) | **Settled** on 2026-11-13, in the owner's words "FE O1: keep". Data keys wrapped by the tenant key stay so wrapped, `lenny-evidence` unwraps them through the tenant key while it exists, and the tenant security settings count them with the tenant's objects under the key before the state moves from `retained` to `none` ([TSS-4](tenant-security-settings-design.md#tss-4-tenant-key)). | The key state stays `retained` while any evidence data key of the tenant is wrapped by the tenant key, which can be for the longest stream retention. | Rewrap every data key under the regional evidence key when the state becomes `retained`, at a KMS call pair per key, which takes those records out of reach of the tenant key's deletion. |

## Validation owed

No validation has run. These come first, because the recommendation rests on them, and each is an input to a proposal.

1. **gVisor spike, before FE-6b.** On containerd and on GKE Sandbox: what `Execve` and `Open` deliver in practice, what a build workload costs with each enabled, what the sender drop count reports under flood, whether a sink can reattach to a running sandbox, whether a per-handler configuration selects monitored pods, and whether any trace point can supply a connection's local port.
2. **Host sensor confirmation.** Tetragon and Falco on the same nodes, against gVisor and Kata pods, to pin the coverage table's inference.
3. **Volume.** Measured records per session on representative workloads, replacing the assumed figures, and the compression that a chunked envelope gains.
4. **Capture.** The kubelet path on the deployment's containerd or CRI-O, `runsc checkpoint --leave-running` on a Lenny pod including its native sidecar, and a Kata hypervisor dump for each hypervisor a deployment uses.
5. **Quarantine on CNIs.** What happens to existing flows after the selector change on Calico and Cilium, and how managed node upgrades treat a PDB that refuses eviction indefinitely.
5a. **Volumes and freeze.** That emptyDir data is removed once only native sidecars remain, that `keepalive` prevents it, whether a running ephemeral container would, whether a frozen process can be checkpointed by each method, and how fast an agent reaches the size-limit eviction against the time a node-side freeze takes.
5b. **Slots.** How long `freeze` waits behind a cancelled `lenny-materialize` run and a cancelled setup command on runc, gVisor, and Kata, against the run's `timeoutMs` plus 5 seconds; that gVisor's trace points carry the container identity that maps a record to `runtime-<n>`; and that the capture Job finds each slot volume on the node by name on a pod with several slots.
5c. **User namespaces on runc.** Pods of the class that holds runc pods render `hostUsers: false` unless the operator disabled user namespaces on that class ([PM-1](placement-design.md#pm-1-node-classes-and-presets)). Whether the kubelet checkpoint path with CRI-O or with containerd and CRIU captures a container in a user namespace, and whether the Job's copy of the volumes keeps the owners and modes the slot saw, given the idmapped mounts.
5d. **Collector admission.** That `lenny-node-agents` at the Pod Security level `privileged`, with `lenny-pod-security` narrowing it to the collector's template, admits the collector's `hostPath` socket directory and refuses every other pod, on each managed Kubernetes offering a deployment uses.
6. **Legal assessment.** The project has no counsel, so this design makes no legal claim. Decisions 6, 7, 8, 23, and 24 set mechanisms and cautious defaults, and the operator documentation states that each operator assesses its own role against a tenant's instruction and what its customer agreement must state.
7. **Defaults of decision 29.** Run an incident drill that quarantines pods across several pools and confirm that `forensics.quarantine.maxPods` at 10 fires at a useful point. Run concurrent mirror fetches while stores are held and confirm that `gitMirror.heldUidBudget` at a quarter of the fetch-UID range leaves enough UIDs for the fetches. Measure the `lenny-evidence-reconcile` run duration and its object store request cost at test Tier 7 on the hourly schedule.

## Sources

Vendor pages describe the vendor's own product. Read on 2026-11-02, and none was tested.

This document has no evidence folder, because no test ran for it. The stand-in measurement of [`egress-and-git-design.md`](egress-and-git-design.md) that "Volume and cost" compares against is under `evidence/egress-and-git-design/spike-evidence/`.

**Standards and law**

- [RFC 3227](https://www.rfc-editor.org/rfc/rfc3227.txt): order of volatility, not shutting down before collection, chain of custody, and privacy.
- [RFC 7422](https://www.rfc-editor.org/rfc/rfc7422.html): source port and timestamp logging behind shared addresses, and timekeeping.
- [GDPR Article 17](https://gdpr-info.eu/art-17-gdpr/) (paragraph 3), and [Recital 49](https://gdpr-info.eu/recitals/no-49/).
- [OCSF v1.1.0 Process Activity](https://schema.ocsf.io/1.1.0/classes/process_activity), [File System Activity](https://schema.ocsf.io/1.1.0/classes/file_activity), and [Datastore Activity](https://schema.ocsf.io/1.1.0/classes/datastore_activity).

**Kubernetes**

- [Kubelet checkpoint API](https://kubernetes.io/docs/reference/node/kubelet-checkpoint-api) and [KEP-2008](https://www.kubernetes.dev/resources/keps/2008/).
- [Kubelet authentication and authorization](https://kubernetes.io/docs/reference/access-authn-authz/kubelet-authn-authz/) for `nodes/checkpoint` and the `nodes/proxy` warning.
- [Network policies](https://kubernetes.io/docs/concepts/services-networking/network-policies/) on additivity and on existing connections.
- [Node-pressure eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/).
- [Kubelet `SyncTerminatedPod`](https://raw.githubusercontent.com/kubernetes/kubernetes/master/pkg/kubelet/kubelet.go), [emptyDir teardown](https://raw.githubusercontent.com/kubernetes/kubernetes/master/pkg/volume/emptydir/empty_dir.go), and [sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/). The kubelet's stopping of sidecars after regular containers on a non-Job pod is from the documentation's termination description and general behaviour, and is untested here.
- [Linux cgroup v2 `cgroup.freeze`](https://docs.kernel.org/admin-guide/cgroup-v2.html).
- [containerd's CRI checkpoint handler](https://github.com/containerd/containerd/blob/v2.0.0-rc.1/internal/cri/server/container_checkpoint_linux.go) (source code at v2.0.0-rc.1).

**gVisor and Kata (project documentation unless stated)**

- [gVisor runtime monitoring](https://raw.githubusercontent.com/google/gvisor/master/g3doc/user_guide/runtime_monitoring.md), [seccheck](https://raw.githubusercontent.com/google/gvisor/master/pkg/sentry/seccheck/README.md) on sessions, optional fields, `ignore_setup_error`, and retries, and the [remote sink example](https://raw.githubusercontent.com/google/gvisor/master/examples/seccheck/README.md).
- [gVisor's trace point messages](https://raw.githubusercontent.com/google/gvisor/master/pkg/sentry/seccheck/points/syscall.proto) for `Connect`, `Execve`, and `Open`, and [`runsc pause`](https://github.com/google/gvisor/blob/master/runsc/cmd/pause.go) (source code).
- [Configuring Falco with gVisor](https://gvisor.dev/docs/tutorials/falco) and [gVisor checkpoint and restore](https://gvisor.dev/docs/user_guide/checkpoint_restore/).
- [falco-gvisor-plugin](https://github.com/gucchisk/falco-gvisor-plugin) (third party) for no reconnection after sandbox start, and [Kubescape's gVisor proposal](https://github.com/kubescape/designs-and-proposals/pull/23) with its [node-agent trial](https://github.com/kubescape/node-agent/pull/1051) (third-party project) for always-sent fields, sender drops, and receiver restarts.
- [Ant Group's Kata and eBPF case study](https://ebpf.foundation/ant-group-secures-their-platform-with-kata-containers-and-ebpf-for-fine-grained-control/) (eBPF Foundation).
- [A copy of Kata's limitations page](https://github.com/Jakob-Naucke/kata-containers-CCv0/blob/CCv0/docs/Limitations.md) (a fork, possibly out of date).

**Sensors (project or vendor documentation)**

- Tetragon (project, Isovalent-backed): [overview](https://tetragon.io/docs/overview/), [process lifecycle](https://tetragon.io/docs/use-cases/process-lifecycle/), and [file access](https://tetragon.io/docs/use-cases/filename-access/).
- [Falco on dropped events](https://falco.org/docs/troubleshooting/dropping/) (project): rule-of-thumb kernel event rates per CPU.
- [Sysdig: Falco extends to gVisor](https://www.sysdig.com/press-releases/falco-extends-to-gvisor) (vendor).
- [Google: Container Threat Detection](https://docs.cloud.google.com/security-command-center/docs/how-to-use-container-threat-detection) (vendor): not compatible with GKE Sandbox.
- [AWS: GuardDuty Runtime Monitoring on EKS](https://docs.aws.amazon.com/guardduty/latest/ug/how-runtime-monitoring-works-eks.html) (vendor): no EKS on Fargate.

**Agent products (vendor documentation)**

- [Claude Code monitoring](https://code.claude.com/docs/en/monitoring-usage) and [hooks](https://code.claude.com/docs/en/hooks).
- [Gemini agent hooks](https://ai.google.dev/gemini-api/docs/agent-hooks).

**Incident practice**

- [Wiz: Kubernetes incident response](https://www.wiz.io/api/md/academy/container-security/kubernetes-incident-response) (vendor): "Preserve forensic evidence before moving workloads."
- [CircleCI incident report](https://circleci.com/blog/jan-4-2023-incident-report/) (vendor post-mortem): "there is no way for us to know if your secrets were used".
- [Okta root cause](https://sec.okta.com/articles/2023/11/unauthorized-access-oktas-support-case-management-system-root-cause) (vendor post-mortem): missing scope because access produced "an entirely different log event".

Reddit was unavailable to the outline's research tools, and this revision's searches found no first-hand account of investigating an agent incident, so the agent-specific evidence is thin. No primary source stated a Tetragon limit under gVisor or Kata, and no source documented a Kata guest memory procedure.

## Review history

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 4 added delivered user tokens, and revision 5 corrects that text after review.
- Revisions 12 to 14, 16, and 17 change only these pointers, and revision 15 also withdraws the `security-auditor` default under the owner's decision on SO decision 19.
- Revision 24 records every decision as settled, replaces every reliance on counsel with each operator's own legal assessment, decision 4 with separate settings per stream, after two independent reviews of earlier revisions. Revision 24 followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read, and no production component or live provider write has run.
- Revision 25 changes only pointers after the user credential documents split.
- Revision 26 followed a pass for a reader new to the project. It added "Scope", "Terms", "Terms from other designs and the platform", "Labels from other designs", and "High-level design", moved the revision notes out of the status line and the review records to the end of the document, linked every citation of another design, named the object and the setting behind each configuration value, replaced the egress design's interface label with the name "pod credential API", and split "the reconciler" into the forensics reconciler and the evidence reconciler. An independent reader review followed, and this revision also applied its findings: it added Terms rows for the audit chain and anchor, the legal-hold ledger, the evidence envelope, incarnations, tags, roles, admission webhooks, the runtime SDK, and the watchlist, stated the rules of decisions 6 and 12 where they are cited, and qualified the uses of hold, binding, store, model, keepalive, and tier. After a validation pass, it named the operator's forensics investigator Frank in every scenario, so that the name has one role across the designs, and replaced the **Lost before quarantine.** residual risk's pool-level retention claim with the tenant's `agentActivity.retentionDays` and the deployment's `retentionClass: security`, and changed no rule.
- Revision 27 applied decision 27, which the owner settled on 2026-11-08. The forensics reconciler is the writer of a break-glass `pod_quarantines` row, with `origin: break_glass` and a custody event, once Postgres is reachable. It replaced the `GET /v1/admin/quarantines` row's `setBy: label` row written by the WarmPoolController, added `origin` (`api` or `break_glass`) and the `unregistered` report to the data model, moved PDB creation in the Tier 2 tests from the WarmPoolController to the forensics reconciler, and aligned "Terms", "Architecture", the failure table, "Specification sections affected", and the Tier 8 break-glass test.
- Revision 28 updated the sentence after the dependency table, which said that the egress design's "Proposal split" lists FE-1 to FE-6, to name the rows that table holds (FE-1, FE-2, FE-3, FE-4, and FE-6), after a re-read of `egress-and-git-design.md`, `user-credential-delivery-design.md`, `user-git-identity-design.md`, and `github-credential-addon-design.md` at their current revisions, and changed no rule.
- Revision 29 applied the cross-design sync review of 2026-11-08 and the owner's answers of 2026-11-09. Under the owner's answer on the embedded `SIGSTOP` checkpoint path, `incremental-checkpoint-design.md` removes the path, its `checkpointStuck` flag, and its liveness rule (IC-9), so this revision dropped decision 26's amendment of §4.4.3, keyed the liveness row of "Retirement paths" on the absence of a probe and the remaining signals, made FE-1 depend on IC-9, and removed the stuck-checkpoint exit from the tier 5 test. It added the "Retirement paths" row for a runtime rollout's `Contracting` from `runtime-revisions-design.md` revision 3, with Terms, labels, a dependency row, and a tier 5 test. Under the owner's acceptance of `gitMirror.heldUidBudget`, it recorded the budgeted re-chown as a request to the egress design's held-flag hook and tier 4 test, and added a tier 5 test. It requested that the egress design escrow unmarked batches of a retention-class stream at teardown and state that a quarantined pod's records keep their tenant in the payload, and it stopped attributing the second to that design. It restated the egress design's objection to a per-node DaemonSet as that design states it, added EG-7 to FE-1's dependencies, replaced "gate" for the mint checks with the term the egress design defines, aligned the covered host and `netagent` glosses with that design's Terms, separated a pod's retirement from a base runtime's retirement and a revision leaving traffic, and pointed the `checkpoints` class of a security hold at IC-6's definition of a snapshot's copy. It removed a stale egress revision number from the body and the "Changes requested" section, a double comma, a sentence fragment, and explicit counts.
- Revision 30, dated 2026-11-10, applies decision A16 of `message-delivery-design.md` revision 5, which the owner settled that day in the words "go with all your recommendations", and changes this rule. A security hold's `include: artifacts` covers output files, the files an agent produces for the user, which that design stores as chunks in the packs of `incremental-checkpoint-design.md`. The hold copies every pack an output file references and its `output_files` row to escrow, and the deletion of an output file at the end of the session's retention or after a `PostAgentOutput` `REJECT` calls `EnsureCopied` first. "Copy on hold" and **Open-ended holds and retention races.** state it, and the status line reads against `message-delivery-design.md` revision 5 and `runtime-revisions-design.md` revision 8.
- Revision 31, dated 2026-11-12, applies the consistency pass of that day to the owner's decisions recorded in the designs of 2026-11-12. Under the removal of the embedded deployment model ([`agent-pod-layout-design.md`](agent-pod-layout-design.md), D1, which is SI D11), it removed the "Sidecar and embedded models" term, the embedded freeze bullet and its node-side-first rule, the embedded rows and columns of "Retirement paths", the coverage table, the residual risks, and the tests, and the embedded volume destruction of "Keeping the volumes". Under the fixed layout and the removal of `/artifacts` (PL D2 and D3), FE-5's step 4 copies each slot's `/dev/shm`, `/tmp`, and `/run/lenny`, then `/session` and `/workspace`, and the "Volatile evidence" row states that `/session` and `/tmp` are disk-backed. Under the slot containers and per-slot release of [`session-isolation-design.md`](session-isolation-design.md) (D1 and D4), the freeze reaches each slot's supervisor, the preservation state suppresses every slot release, and a record from `runtime-<n>` is attributed to the session in slot `n`. Under the asynchronous supervisor worker that the owner approved, a `freeze` waits behind an outstanding setup run, which the adapter cancels through the cancel pipe, and a helper's result and logs are slot-reported evidence. Under [`placement-design.md`](placement-design.md) (D2 and D4), the capture Job and the collector are named node agents, the collector runs in `lenny-node-agents` at the level `privileged` with admission narrowing, runc is stated factually, and the drift drain's skip of a quarantined pod joined "Retirement paths". Under [`tenant-security-settings-design.md`](tenant-security-settings-design.md) (D1 and the rename N1 of the top data class to Secret), evidence data keys are wrapped by the tenant key while its state is `active`, activity streams are Confidential on every tenant, `lenny-evidence-reconcile` holds rights on tenant keys, the "T3 and T4" term gave way to the data classes, the tenant key, and the tenant deletion deadline, and the worked example's 4-hour deadline gave way to the recorded deadline. It added O1 on keys wrapped by the tenant key at `retained`. Under [`runtime-protocol-design.md`](runtime-protocol-design.md) (D5 and the removal of integration levels), the coverage table's gateway row reads the tool frames, the adapter-local tool row left, and the integration-level statements were restated. It renamed `maxConcurrentSessions` to `maxConcurrentSessionsPerPod` with `acknowledgeSharedPod`, added "Changes requested of other designs", and added tests and validation items for each change.
- Revision 32, dated 2026-11-13, applies the owner's decisions of 2026-11-13 in the cross-design review. Decision 28 settles O1 as option (a), keep, in the owner's words "FE O1: keep", and the tenant security settings design's revision 4 applies the change it asked for. Under decision D1 of `incremental-checkpoint-design.md`, FE-3 gains "Platform packs": a hold's escrow copy of a snapshot or an output file includes every platform pack it references, the §12.8 legal-hold escrow does the same, and the reclamation or purge of a platform pack calls `EnsureCopied` first, with a tier 4 test and a documentation row. Under `agent-runtimes-only-design.md`, the "Service-mode pod" term, the service-mode router in "Enforcement points", the service-mode sentence of "Sessions on the pod", service mode in the pod-incarnation keys of "Keys" and in "Residual risks", and the service-mode case of the tier 4 sessions test leave, and the `activity` frame names no session, so its session is the slot's (that design's O5). The slot release gains its second Sweep (SI O12 (a)). The user namespace wording follows the placement design's default with an operator's `disabled` setting. "Freezing the agent" records that under gVisor a slot process can stop or kill its supervisor. The collector's node cost is stated against the billing design's scope, in place of `applyResources`. After the cross-design review, the row of "Changes requested of other designs" for the session isolation design records that its revision 5 applied the request, and, under [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md), which removes LLM credential leases and direct delivery mode, the "Credential lease, proxy mode, and direct mode" term becomes "LLM proxy, LLM upstream, and proxy token", step 7 of the capture order no longer revokes a direct-mode LLM credential, and the interceptor row of the coverage table names calls outside the proxy in place of direct mode. These follow decisions already settled in that design and change no rule of this one. The read-only cross-design check of the same day changes no rule: "Sessions on the pod" under FE-1 states that the session's proxy token is revoked at the session's end or the pod's release and that a token already delivered to the pod, such as a `userCredential` file, stays usable until it is revoked or expires, in place of the removed credential leases, proxy mode, and direct mode; the "Slot and slot container" term no longer qualifies the agent pod as session-mode; and the "reads against" list names each cited design's current revision.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
- Revision 33, dated 2026-11-13, applies the owner's answers of 2026-11-13 to the consolidated list of open decisions. Decision 29 sets the defaults that revision 32 left to the chart: `forensics.quarantine.maxPods` 10, `gitMirror.heldUidBudget` a quarter of the mirror's fetch-UID range, as decision 66 of `egress-and-git-design.md` records, and an hourly `lenny-evidence-reconcile` schedule. "Configuration by scope", "Documentation", the tier 7 test, and "Validation owed", whose new item 7 lists the incident drill, the concurrent fetches under holds, and the reconciler's duration and cost at Tier 7, change to match. No decision is open. The status line's reads-against list names the current revision of each cited design. Wording fixes, which change no rule: the volume row names the disk watch's threshold as the chart value `adapter.diskWatchThresholdPercent`, 90% by default, which `agent-pod-layout-design.md` revision 8 adds under the owner's "7: yes" of 2026-11-13, and the reads-against list names the current revisions of the egress, session isolation, and pod layout designs.
- Revision 34, dated 2026-11-14, follows `agent-pod-layout-design.md` revision 10, which adopts automatic project quotas as PL-10, and changes no rule. The evidence-loss row "A slot volume over its `sizeLimit`" states that a project quota closes the route to the kubelet's eviction on the volumes it bounds, because the slot's writes fail at the quota's hard limit below `sizeLimit`, and that the route stays open on the volumes no quota bounds. The status line's reads-against list names the current revision of each cited design.
- Revision 35, dated 2026-11-15, applies the owner's decision of that day, in the owner's words "Yes, let's drop connectors.", which [`connector-removal-design.md`](connector-removal-design.md) revision 2 designs, and changes these rules. FE-6a's gateway record covers platform tool calls alone, and the gateway row of "What can observe an agent" loses connector calls and `PreConnectorRequest`. A runtime's calls to an operator service reach the evidence through `egress.tunnel`, `egress.local`, and `credential.mint`, which the `egress.*` row now states, with no per-call record in Lenny. The `agent.activity.tool` payload names the server in place of the connector, and the `forensics.activity.tool.enabled` row, the runtime author guide row of "Documentation", the channel identifiers term, and the RP-3 label follow. "Labels from other designs" gains CR-1 and CR-3. Decision 4 keeps the owner's words and gains a note that connector activity no longer exists. The status line reads against `message-delivery-design.md` revision 23, `runtime-protocol-design.md` revision 11, and `connector-removal-design.md` revision 2.

### Review of revision 7

Round 4 of the reviews raised m7: git on an uncovered host produces no `git.operation`. It holds, and R-GIT-CLAIM states it. None was rejected.

### Review of revision 6

Round 3 of the reviews raised the revert case on `user-credential-delivery-design.md`: a record replayed after an incident restores old tokens. It holds, and step 7 now states it. None was rejected.

### Review of revision 5

Round 2 of the reviews raised N29 on this document: step 7 said to revoke a child through its link provider, against this design's own limit. It holds, and step 7 now removes the link for `oauth2Link` and acts on exposure rows for `registered`. None was rejected.

### Review of revision 4

Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Known values reaching FE-6's redaction | Holds. FE-6 redacts in the collector and adapter, and that design gave them no such value. | They do not reach it. `leakScan` stays in the gateway. |
| "Revoking child tokens by hash" | Holds. Revocation needs the token value. | The case removes the link. |
| A held pod's file, stated two ways | Holds | Two cases, stated. |
| Exposure events are best effort | Holds | Durable rows, in that design. |

None was rejected.
