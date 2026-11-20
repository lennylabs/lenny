# Steps: 0128 Forensics and evidence preservation

## Steps

### 0128.S1 Cases and custody
- status: Planned
- aliases: [FE-0]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2]

The case record, the `forensics.*` events, the `platform-forensics` role, and the `lenny-ctl` subsection. It depends on the IdP role boundary of [TS-2](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) (0149.S2), which keeps a platform role out of an identity provider's roles claim, out of a `role_map`, and out of role rows outside the operator tenant, so that `platform-forensics` cannot be conferred on a customer's principal from the moment this step lands.

[SO-4a](../0142-security-oversight/change.md#so-4a-mirror-to-the-tenant-chain) of [`0142-security-oversight`](../0142-security-oversight/change.md) (0142.S4) must exclude the `forensics.*` prefix from what it mirrors to a tenant, unless a case discloses it. If SO-4a lands first, this step adds the exclusion.

### 0128.S2 Quarantine
- status: Planned
- aliases: [FE-1]
- lanes: [specification, code, documentation]
- depends-on: [0128.S1, 0127.S2, 0127.S7, 0131.S9, 0145.S1, 0145.S3, 0153.S1]

The API, the row and label, the binding state's writer, the phase, every retirement path, the preservation RPC, the `keepalive` container, the freeze, the policy selectors, the webhook rules, the PDB, and release.

This step renders `keepalive` in every agent pod. [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) lists it among the containers of PLR-1 and states that PLR-1 holds in full once this step lands. The resource class mapping of [0121.S1](../0121-billing-resource-attribution/steps.md#steps) subtracts the requests of the platform containers present when it lands, so if this step lands after 0121.S1, it adds `keepalive`'s request to that subtraction and to the check at load.

Which controller keeps and deletes a `PodSet` that holds a quarantined `AgentPod` is O5 of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/decisions.md), and this step builds the retention against that answer.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-ForceTerminate`, `GWSEAM-TerminateUserSessions`, and `GWSEAM-ReclaimRevokedPreRunning`, whose pod release skips a labelled pod; `GWSEAM-InspectForRecycle`, `GWSEAM-RetireOnSessionCount`, `GWSEAM-RecyclePod`, and `GWSEAM-RetirePod`, which evaluate no recycle limit and retire nothing for a labelled pod; and it adds the operation through which the API front's quarantine handler has the session core push the binding change, send `CH-PRESERVE`, and end the pod's sessions. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

It needs the case record; the `quarantined` binding state, which [EG-2](../0127-egress-and-git/steps.md#steps) adds and [EG-7](../0127-egress-and-git/steps.md#steps) carries to level 1's tenant policies; the removal of the §4.4.3 liveness rule of the `SIGSTOP` checkpoint path by [IC-9](../0131-incremental-checkpoint/change.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path); the slot containers and the per-slot `CH-SUPERVISE` of [`0145-session-isolation`](../0145-session-isolation/change.md), which carries `freeze`; that design's slot release, which the preservation state suppresses; and the cancel pipe of the `setup` protocol of [WM-3](../0153-workspace-materialization/change.md#wm-3-the-protocol). The table in [`0127-egress-and-git`, "Steps"](../0127-egress-and-git/steps.md#steps) lists EG-2 alone for FE-1, while the egress design's `quarantined` hook names EG-2 and EG-7, so this step also depends on EG-7.

A rollout's deletion of a pod set under [RV-3](../0140-runtime-revisions/change.md#rv-3-pods) and [RV-4](../0140-runtime-revisions/change.md#rv-4-rollout) of [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) skips a quarantined `AgentPod` and its `PodSet`, which that design stages as its RVR-10, and the "Retirement paths" row for `Contracting` records it here. Neither this step nor 0140.S1 waits for the other, because the skip reads a label that nothing sets until this step lands.

### 0128.S3 Mirror store hold
- status: Planned
- aliases: [FE-2]
- lanes: [specification, code, documentation]
- depends-on: [0128.S1, 0128.S4, 0127.S5]

The store APIs, the flag's setter, sealing with a fresh generation, the snapshot, and purge. It needs the case record, the `security_holds` row that purge reads, and the held flag of [EG-5](../0127-egress-and-git/steps.md#steps).

### 0128.S4 Security holds and the retention class
- status: Planned
- aliases: [FE-3]
- lanes: [specification, code, documentation]
- depends-on: [0128.S1, 0127.S10, 0131.S4, 0135.S4 (part), 0150.S1 (part)]

The hold API and basis, separate records, continuous copy, the audit segments, the marker's setter, the GC guard and `EnsureCopied`, the §12.8 ledger change, deferred erasure, escrow of evidence and keys, and the retention class. It needs the case record, and the held marker and `EvidenceStore` of [EG-10](../0127-egress-and-git/steps.md#steps).

[IC-6](../0131-incremental-checkpoint/change.md#ic-6-retention-garbage-collection-erasure-and-legal-hold) of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md) defines a security hold's copy of a checkpoint snapshot, with every session pack and platform pack it references, and calls `EnsureCopied` before it deletes a snapshot, a session pack, or a platform pack.

This step copies checkpoints as pack snapshots, with the platform packs they reference, so it depends on the catalog and the platform chunk sets of [IC-4](../0131-incremental-checkpoint/change.md#platform-chunk-sets) (0131.S4). 0131.S6 calls the `EnsureCopied` and the escrow prefix this step defines, so 0131.S6 lands with or after this step. The copy of output files waits for [MD-18](../0135-message-delivery/change.md#md-18-files) (0135.S4), so that part lands with or after 0135.S4, and the rest of the step can land first. If 0135.S4 is split under its O9, the edge names the file export step, as O12 records.

### 0128.S5 Reconciler
- status: Planned
- aliases: [FE-4]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S3, 0127.S5, 0127.S10, 0128.S2]

`pod.address`, `lenny-evidence-reconcile`, the rules, `evidence.reconcile`, imports, and the API. It needs the streams it reconciles, from [EG-2](../0127-egress-and-git/steps.md#steps), [EG-3](../0127-egress-and-git/steps.md#steps), [EG-5](../0127-egress-and-git/steps.md#steps), and [EG-10](../0127-egress-and-git/steps.md#steps), and the quarantine rule of 0128.S2. Its `R-ACTIVITY` rule lands with 0128.S8, so this step does not wait for 0128.S8.

### 0128.S6 Capture runbook and abuse workflow
- status: Planned
- aliases: [FE-5]
- lanes: [specification, code, documentation]
- depends-on: [0128.S1, 0128.S2, 0128.S3, 0128.S4, 0128.S5, 0127.S8, 0136.S2, 0136.S7, 0118.S4, 0150.S1 (part)]

The unbound role, the published capture image, capture registration, `attribute`, export and verify, and the runbooks. It needs the procedures it documents, the `pod.address` and `egress.accesslog` checks in `attribute`, and the cross-tenant query of [EG-8](../0127-egress-and-git/steps.md#steps). The capture Job runs in the `lenny-forensics` namespace of [PM-9](../0136-placement/change.md#pm-9-namespaces) (0136.S2) as a named node agent of [PM-5](../0136-placement/change.md#pm-5-rendering), whose pinned template `POD_NAMED_AGENT_ONLY` admits by the capture image's digest and the fixed set of host paths that FE-5 names (0136.S7), and it copies the slot volumes by the names that [PL-5](../0118-agent-pod-layout/change.md#pl-5-volumes-size-limits-and-eviction) (0118.S4) gives them.

This step adds no right to `lenny-ops`, so it does not conflict with the narrowing of [SO-7](../0142-security-oversight/change.md#so-7-f-25440-lenny-ops-rights) (0142.S9).

### 0128.S7 Tool activity
- status: Planned
- aliases: [FE-6a, FE-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0127.S9, 0127.S10, 0139.S2, 0145.S1, 0135.S5, 0121.S2 (part), 0150.S1 (part)]

`agent.activity.tool`, the key store, the SDK and adapter report, the coverage API, the read API, and redaction. It needs the named permissions of [EG-9](../0127-egress-and-git/steps.md#steps), and the envelope, ingest, the key field, and the marker of [EG-10](../0127-egress-and-git/steps.md#steps). It uses 0128.S4 for its hold behaviour only, and a release before 0128.S4 ships without that rule. The gateway record reads the `tool_call` frames of RP-3 (0139.S2), the adapter takes a report's session from the slot whose connection carries it, which needs the per-slot connections of [`0145-session-isolation`](../0145-session-isolation/change.md) (0145.S1), and a `sessionId` member in a report is ignored under the leniency rule of [MD-24](../0135-message-delivery/change.md#md-24-runtime-frames-and-leniency) (0135.S5).

The step also adds the `evidence.usage` billing event and `evidenceQuotaBytes`. The event follows "Writing periodic events" of [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md#writing-periodic-events), so that part depends on 0121.S2, which lands `usage_window_key`, the read-back emitter, and the idempotent insert, and the rest of the step can land first.

The key store's `CountTenantWrappedKeys` ("Keys" under FE-6) serves the read route's `evidenceKeysUnderTenantKey` and the `TenantKeyProbe` worker of [TSS-4](../0150-tenant-security-settings/change.md#tss-4-tenant-key). Of this step and [0150.S2](../0150-tenant-security-settings/steps.md#steps), whichever lands second adds that count to the move from `retained` to `none`.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. The gateway record of platform tool calls sits in the platform tools seam's tool-call bridge, which resolves the calling session through `GWSEAM-AttachCallingSession`, and the step reshapes that operation when the record needs a fact the operation does not return. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

[SO-1](../0142-security-oversight/change.md#so-1-security-roles)'s `security-auditor` holds an activity read permission only through `auditorStreamGrants`. Neither this step nor 0142.S1 waits for the other, because `tenant-admin` holds the read permissions throughout. A per-tenant SIEM under [SO-3](../0142-security-oversight/change.md#so-3-per-tenant-siem-destination) receives activity streams through its export. This step states what crosses it, and SO-3 states the destination.

### 0128.S8 Process and file activity
- status: Planned
- aliases: [FE-6b, FE-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0128.S7, 0127.S11, 0136.S7, 0136.S2, 0150.S1 (part)]

The collector, `activityMonitoring` and the monitored class, `agent.activity.exec`, `.exec.args`, and `.file`, the producer contract, and the Tetragon reference forwarder. It starts with the spike under "Validation owed". It needs the streams and keys of 0128.S7, the PKI of [EG-11](../0127-egress-and-git/steps.md#steps), and the collector's tolerations and namespace from the named node agents of [PM-5](../0136-placement/change.md#pm-5-rendering) and the `lenny-node-agents` namespace of [PM-9](../0136-placement/change.md#pm-9-namespaces), with `POD_NAMED_AGENT_ONLY`, which 0136.S7 lands. The `R-ACTIVITY` rule of 0128.S5 lands with this step.

### Notes on the steps

The table in [`0127-egress-and-git`, "Steps"](../0127-egress-and-git/steps.md#steps) lists FE-1, FE-2, FE-3, FE-4, and FE-6 with their EG dependencies, and the steps above add FE-5, FE-6a, and FE-6b to it. No shared queue file exists, and steps from different changes interleave as long as these dependencies hold.

0128.S4, 0128.S6, 0128.S7, and 0128.S8 write §12.9 rows for retained evidence, captures, and activity streams under the class names Public, Internal, Confidential, and Secret, so each depends on 0150.S1 (part) of [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps), which defines those names.

| Other design | Relationship |
| :-- | :-- |
| [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) | Holds and streams use the tenant, and an environment filter can be added to reads later. No dependency. |
| [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | Its `quarantined` handling needs nothing here. FE-6 takes nothing from `leakScan`. A link's `link_ref`, under ["Stored links"](../0151-user-credential-delivery/change.md#stored-links) in [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), follows "Delivered user tokens". |
| [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) | Child tokens, under [GH-2](../0129-github-credential-addon/change.md#gh-2-pull-request-authoring-on-github), follow "Delivered user tokens". No dependency in either direction. |
| [`0145-session-isolation`](../0145-session-isolation/change.md), [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md), and [`0153-workspace-materialization`](../0153-workspace-materialization/change.md) | They define the slot containers, the per-slot `CH-SUPERVISE` and its slot release, the volumes FE-5 copies, `keepalive`'s place in the pod blueprint, and the `setup` protocol with its cancel pipe. FE-1 (0128.S2) and FE-5 (0128.S6) read them as fixed inputs. |
| [`0136-placement`](../0136-placement/change.md) | It places the collector and the capture Job as named node agents, owns their namespaces, and skips a quarantined pod in its drift drain. 0128.S6 and 0128.S8 depend on 0136.S2 and on 0136.S7, which lands the `lenny-forensics` and `lenny-node-agents` namespaces, the named node agents' tolerations, and `POD_NAMED_AGENT_ONLY`. |
| [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) | Its tenant key state decides which key wraps an evidence data key, its recorded tenant deletion deadline bounds the worked example's teardown, and its data classes name the classes this design uses. Decision 28 asked it for one change, which its revision 4 applies. Its `storage.platformChunkReuse` decides whether a tenant's snapshots reference platform packs that a hold must copy. |

## Validation owed

No validation has run. These come first, because the recommendation rests on them, and each is an input to a proposal.

1. **gVisor spike, before FE-6b.** On containerd and on GKE Sandbox: what `Execve` and `Open` deliver in practice, what a build workload costs with each enabled, what the sender drop count reports under flood, whether a sink can reattach to a running sandbox, whether a per-handler configuration selects monitored pods, and whether any trace point can supply a connection's local port.
2. **Host sensor confirmation.** Tetragon and Falco on the same nodes, against gVisor and Kata pods, to pin the coverage table's inference.
3. **Volume.** Measured records per session on representative workloads, replacing the assumed figures, and the compression that a chunked envelope gains.
4. **Capture.** The kubelet path on the deployment's containerd or CRI-O, `runsc checkpoint --leave-running` on a Lenny pod including its native sidecar, and a Kata hypervisor dump for each hypervisor a deployment uses. That the capture Job's fixed set of host paths under FE-5 covers every capture step on each container runtime, so that `POD_NAMED_AGENT_ONLY` admits the Job with no other path.
5. **Quarantine on CNIs.** What happens to existing flows after the selector change on Calico and Cilium, and how managed node upgrades treat a PDB that refuses eviction indefinitely.
5a. **Volumes and freeze.** That emptyDir data is removed once only native sidecars remain, that `keepalive` prevents it, whether a running ephemeral container would, whether a frozen process can be checkpointed by each method, and how fast an agent reaches the size-limit eviction against the time a node-side freeze takes.
5b. **Slots.** How long `freeze` waits behind a cancelled `lenny-materialize` run and a cancelled setup command on runc, gVisor, and Kata, against the run's `timeoutMs` plus 5 seconds; that gVisor's trace points carry the container identity that maps a record to `runtime-<n>`; and that the capture Job finds each slot volume on the node by name on a pod with several slots.
5c. **User namespaces on runc.** Pods of the class that holds runc pods render `hostUsers: false` unless the operator disabled user namespaces on that class ([PM-1](../0136-placement/change.md#pm-1-node-classes-and-presets)). Whether the kubelet checkpoint path with CRI-O or with containerd and CRIU captures a container in a user namespace, and whether the Job's copy of the volumes keeps the owners and modes the slot saw, given the idmapped mounts.
5d. **Collector admission.** That `lenny-node-agents` at the Pod Security level `privileged`, with `lenny-pod-security` narrowing it to the templates of the named node agents that PM-9 places there, admits the collector's `hostPath` socket directory and refuses every other pod, on each managed Kubernetes offering a deployment uses.
6. **Legal assessment.** The project has no counsel, so this design makes no legal claim. Decisions 6, 7, 8, 23, and 24 set mechanisms and cautious defaults, and the operator documentation states that each operator assesses its own role against a tenant's instruction and what its customer agreement must state.
7. **Defaults of decision 29.** Run an incident drill that quarantines pods across several pools and confirm that `forensics.quarantine.maxPods` at 10 fires at a useful point. Run concurrent mirror fetches while stores are held and confirm that `gitMirror.heldUidBudget` at a quarter of the fetch-UID range leaves enough UIDs for the fetches. Measure the `lenny-evidence-reconcile` run duration and its object store request cost in the `load_cloud` tier on the hourly schedule.
