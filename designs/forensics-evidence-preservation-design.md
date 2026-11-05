# Forensics and evidence preservation

Status: design for owner review, revision 19, 2026-11-05, which records decisions 1 to 4, decision 4 with separate settings per stream, after two independent reviews of earlier revisions, against `egress-and-git-design.md` revision 56, `user-credential-delivery-design.md` revision 15, and `user-git-identity-design.md` revision 17. Revisions 12 to 14, 16, and 17 change only these pointers, and revision 15 also withdraws the `security-auditor` default under the owner's decision on SO decision 19. Revision 4 added delivered user tokens, and revision 5 corrects that text after review. Nothing in this document is built, staged, or validated. No spike, test, or benchmark has run for it, and every number under "Volume and cost" is a planning assumption. Claims about Lenny were checked against the specification and the tree on 2026-11-02 and 2026-11-03. Claims about external products come from the pages listed under "Sources" and were read rather than tested. Proposals carry the label FE-n. This revision followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read, and no production component or live provider write has run.

## Problem

A company runs Lenny as a multi-tenant SaaS, and its security forensics team investigates prompt-injection exfiltration, abuse, suspected cross-tenant breaches, and leaked credentials. Lenny records a great deal, but nothing can freeze evidence against the tenant under investigation, and nothing records what an agent did inside its pod. The current state, as read from the specification and the tree:

| Area | Current state | Where |
| :-- | :-- | :-- |
| Legal hold scope | The setter accepts a session or an artifact. `LegalHoldRequest` has the fields `tenantId`, `sessionId`, `artifactId`, `hold`, and `note`, and a tripwire test refuses `audit_range`. | §12.8 **Legal hold**, `pkg/gateway/externalapi/admin/legalhold.go`, `legalhold_scope_tripwire_test.go` |
| Legal hold visibility | A tenant admin can set, list, and release holds on its own tenant. | §10.2 permission matrix, §15.1 `POST /v1/admin/legal-hold` |
| `audit_range` | The §12.8 preflight and Phase 3.5 read a ledger entry of this type, and nothing writes one. `tenantHoldEnumerator` does not enumerate it, and the escrow migrator records it with no blob body. | §12.8 step 0, `cmd/lenny-gateway/tenantdeletion.go`, `cmd/lenny-gateway/legalholdescrow.go` |
| Audit retention | The partition GC checks the SIEM high-water mark and no hold. | §16.4 **Log retention and EventStore management.** |
| Tenant teardown | Phase 4 runs `DeleteByTenant` on the audit `EventStore`. The tripwire test's comment states that the eraser has no escrow skip. | §12.8 **Tenant deletion lifecycle.** |
| Force-terminate | The session moves to `failed` and the gateway releases the pod. | §24.11, `cmd/lenny-gateway/session_admin.go` |
| Tenant suspension | Suspension drains every session, and a terminal claim disposition takes the pod through `draining` to `terminated`. | §15.1 `POST /v1/admin/tenants/{id}/suspend`, §4.6.1 **Occupancy projection** |
| Volatile evidence | `/sessions` and `/tmp` are tmpfs, and §6.4 states that their data is gone when the pod terminates. | §6.4 |
| Incident primitives | §11.8 lists credential revocation, user invalidation, legal hold, and the audit trail. It leaves procedures to the deployer. | §11.8 |
| Runbooks | §17.7 has no forensic runbook. | §17.7 |
| Agent actions | The adapter emits the `session.tool_call` span for adapter-local tools, with a name and an identifier and no arguments. Nothing records commands the agent runs or files it opens. | §16.3, `pkg/adapter/attach.go`, `pkg/adapter/toolcall.go` |

## Requirements

- R1. An investigator can stop a pod from doing tenant work and from reaching anything, while its memory, its volumes, and its pod object stay intact, against every path that retires or evicts a pod.
- R2. An investigator can freeze a mirror store and copy it, including the ownership and mode bits that say which fetch wrote each file.
- R3. A hold can cover a tenant, a user, a session, or a pod for a time range, across audit rows, evidence streams, and session artifacts. The tenant under investigation can neither see nor release it.
- R4. Designated evidence survives tenant teardown for a set period, because a compromised or malicious tenant can otherwise destroy evidence by offboarding.
- R5. A reconciler compares evidence sources and reports disagreement, using at least one source that a compromised decision service does not control.
- R6. The operations documentation gives a capture procedure for each isolation profile, a procedure from an abuse report to a tenant and a session, and a chain of custody.
- R7. Lenny records commands the agent runs and files it opens as typed evidence streams, with a stated provenance, redaction, per-session keys, a cost per tenant, and a named read permission.
- R8. Each record states what produced it and what that producer could have missed, so an investigator never reads a missing record as proof that nothing happened.

This design consumes the envelope, the provenance values, `lenny-evidence`, the held marker, the `quarantined` binding state, and the mirror's held flag from `egress-and-git-design.md`. It treats them as fixed inputs. Who may read evidence is owned by `security-oversight-design.md`, so this document proposes default grants and marks them as decisions for that design.

## Security properties

- FP1. A quarantined pod receives no new work, obtains no credential, and opens no new connection. Enforcement sits outside the pod, because the adapter is the agent process in the embedded model (§4.7.10) and the supervisor runs at the agent UID (§4.7.11 item 8).
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

 lenny-controller: forensics reconciler (PDBs, VolumeSnapshots, release, retrying each step)
 lenny-evidence-reconcile: reads evidence, writes findings through lenny-evidence
 lenny-activity-collector (DaemonSet, FE-6b): gVisor remote sink --> lenny-evidence
```

**Case.** A case is a row in the regional platform Postgres with an identifier, a title, the tenants it names (none at first when an abuse report names no tenant, and `POST /v1/admin/cases/{id}/tenants` adds one with a justification as the investigation finds it), the opening principal, a justification, a state (`open` or `closed`), and an optional public key for capture encryption. A case spanning regions has one row per region under one identifier, because §11.7 **Platform-tenant audit event residency** already routes facts about a tenant to that tenant's region. Every operation below requires a `caseId`, and an action on a tenant requires that the case names it, so an investigator can enumerate what one investigation froze. A closed case refuses new actions.

**Custody events.** Each action writes a platform-tenant audit event with `unmapped.lenny.target_tenant_id` and the fields §11.7 requires, using the `forensics.*` prefix: `forensics.case_opened`, `forensics.quarantine_set`, `forensics.quarantine_released`, `forensics.hold_set`, `forensics.hold_released`, `forensics.store_held`, `forensics.capture_registered`, `forensics.evidence_read`, `forensics.evidence_exported`, and `forensics.evidence_lost`. An unresolvable region fails the action with `PLATFORM_AUDIT_REGION_UNRESOLVABLE`, as CMP-058 requires. A platform-tenant row is invisible to a tenant admin, and it survives tenant teardown, which gives FP3 and FP5 one mechanism. `security-oversight-design.md` SO-4 mirrors operator events to the target tenant, so SO-4 must exclude this prefix (see "Proposals and dependencies").

**Who can do what.**

| Action | `platform-forensics` (new) | `platform-admin` | `tenant-admin` | `security-auditor` (SO-1) | Cluster admin |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Open or close a case | Yes | Yes | No | No | No |
| Set a quarantine or a security hold, or hold a store | Yes | Yes | No | No | Label only (break glass, FE-1) |
| Release a quarantine, hold, or store | Yes, subject to decision 12 | Yes, subject to decision 12 | No | No | No |
| Register a capture, read a capture | Yes | No, unless also granted the role | No | No | Performs the node-side capture |
| Query across tenants, run the reconciler for a case | Yes | Yes (`Access other tenants' data`, §10.2) | No | No | No |
| Read own tenant's streams | Not applicable | Yes | Per stream, see FE-6 | Per stream, see FE-6 | No |
| Set own tenant's §12.8 legal hold | No, it uses security holds | Yes | Yes, unchanged | No | No |
| See a security hold, quarantine, or case | Yes | Yes | No, unless disclosed | No, unless disclosed | Sees the label |

`platform-forensics` is a new built-in role in the §10.2 matrix, because custom roles cannot exceed `tenant-admin`. Its permissions are named (`forensics.case.write`, `forensics.quarantine.write`, `forensics.hold.write`, `forensics.capture.read`, and `forensics.query`), following the named-permission rule from EG-9. A platform admin can grant itself the role, and that grant is an audited role change. None of these operations appears as an MCP management tool (§25.12) in the first version, because a release or an export driven by an operations agent would need its own approval model.

## FE-1: Quarantine

### API

| Method and path | Behaviour |
| :-- | :-- |
| `POST /v1/admin/quarantines` | Body: `caseId`, one of `podName` with `namespace` or `sessionId`, optional `includeDelegationTree`, `sessionDisposition` (`fail` by default, or `resume`), `preserve` and `freeze` (each `true` by default), and `reason`. Returns `202` with the quarantine identifier, `affectedSessions`, and a per-step status including `preserve` and `freeze`. `dryRun` is refused, as on other action endpoints (§15.1). |
| `GET /v1/admin/quarantines` and `GET /v1/admin/quarantines/{id}` | Filters: `caseId`, `tenantId`, and `state`. Returns each step's status and the age of the quarantine. |
| `POST /v1/admin/quarantines/{id}/release` | Body: `justification` and `acknowledgeEvidenceLoss`, required when no capture is registered. Moves the pod to `draining`. |

`lenny-ctl admin forensics quarantine set|get|list|release` maps to these, in a new §24 subsection beside §24.11. A request naming a session whose pod is gone returns `409 QUARANTINE_NO_POD`, and the response points to a security hold. Each pod has at most one active quarantine, and a repeat request adds the new case to it.

### Data model

| Carrier | Fields | Authority |
| :-- | :-- | :-- |
| `pod_quarantines` row (session store, platform-scoped, because a lookup by pod UID has no tenant context) | Identifier, pod UID, name, namespace, node, pool, tenant, sessions, case, state (`applying`, `active`, `released`), step status, set and release principals and times | The record of the quarantine |
| Pod label `lenny.dev/quarantine=<id>` | Quarantine identifier | The enforcement signal, read from informers with no database call |
| `SandboxClaim.status.phase = quarantined` | New gateway-owned binding state (§4.6.3) | Excludes the claim from orphan GC and hold expiry |
| Regular container `keepalive` in every agent pod | A static binary that sleeps and exits on SIGTERM, with its own UID, no volume mounts, and a minimal request | Keeps the pod non-terminal, so the kubelet keeps its volumes (below) |
| `Sandbox.status.phase = quarantined` | New phase, written by the WarmPoolController as a projection of the label (§6.2) | Excludes the pod from inventory and from every drain edge |
| Per-pod `PodDisruptionBudget` | `minAvailable: 1`, selecting the quarantine label | Refuses API-initiated eviction |

The label and the row are both read, so either outage leaves one path. `pkg/binding` treats a pod as quarantined when the informer shows the label or when its re-read of the store finds an active row, and egress revision 38 fixes both inputs in the hook. The PDB uses an integer `minAvailable` because §4.6.1's warm-pod PDB rule states that a `maxUnavailable` budget cannot compute `expectedPods` on a `Sandbox`-owned pod.

### Enforcement points

| Control | Enforced by | Mechanism |
| :-- | :-- | :-- |
| New egress, mints, and mirror reads | Decision service, Token Service, and mirror | The `quarantined` state in `pkg/binding`, each instance reading independently (EG-2). A compromised decision service cannot release the Token Service gate. |
| Release of a user's token | Token Service and decision service | The `quarantined` state stops a `userCredential` release on I8 from each instance's own read, with no new work (see "Delivered user tokens"). |
| Raw network paths | CNI | Every allow policy selecting agent pods gains `lenny.dev/quarantine DoesNotExist` in its pod selector, so the namespace's `default-deny-all` applies. Today that means `allow-gateway-ingress`, `allow-pod-egress-base`, `allow-pod-egress-llm-proxy`, `allow-pod-egress-otlp`, `allow-pod-egress-provider-direct` until EG-2 removes it, `allow-pod-egress-internet`, `allow-pod-egress-kube-dns`, and `allow-pod-egress-objectstore` (`charts/lenny/templates/agent-network-policies.yaml`), plus the policies EG-2 adds for `proxied` and `gitAccess`. A tier 0 test enumerates every rendered policy in agent namespaces, so a later policy without the exclusion fails. Network policies are additive, so a deny policy cannot express this. |
| Open tunnels | `netagent` and the pool's tunnel cap | `netagent` closes tunnels at the binding change. A compromised `netagent` keeps them until the pool's `max_stream_duration`, the residual the egress design states. Kubernetes leaves it to the network plugin whether a policy change affects an existing connection, so the policy change is no guarantee either. FE-5 gives the reset procedure. |
| New sessions and claims | Gateway, `lenny-sandboxclaim-guard`, and the service-mode router | The phase leaves inventory, the webhook refuses a claim whose pod carries the label, and `tenantaffinity.Router` never selects a labelled pod. |
| Session work | Gateway | The sessions end (below). The gateway mints no presigned capability, delivers no message, and runs no checkpoint or scrub for the pod. |
| Deletion | `lenny-label-immutability` (baseline, `failurePolicy: Fail`) | Refuses `DELETE` of a labelled Pod or Sandbox unless the caller is the controller's ServiceAccount after release. |
| Label writes | Same webhook | The gateway and the groups in `forensics.breakGlassGroups` can set the label, and only the controller can remove it. |
| Eviction | Kubernetes eviction API | The per-pod PDB. |

**Retirement paths.** Each existing path was read from §4.6.1, §4.6.3, §4.7.10, §5.2, §6.2, §10.1, §12.8, and §24.11, and each checks the label or phase before it acts:

| Path | Today | Under quarantine |
| :-- | :-- | :-- |
| Terminal claim disposition, or claim deletion | `claimed → draining → terminated` | The claim holds `quarantined`, and the projection stays at `quarantined`. |
| `recycle.maxSessionsPerPod`, `maxScrubFailures`, `maxPodUptimeSeconds`, and `runtime_not_live` | Retire | Not evaluated. |
| `lenny.dev/drain-request` annotation | Drain | Ignored while the label is present. |
| Orphan claim GC (each predicate) | Reclaims a stale claim | The new binding state matches no predicate. |
| Reserved hold expiry | Claim deleted | The hold no longer applies. |
| Unschedulable host retire on the recycle edge | Retire | Not evaluated. |
| Adapter or runtime container exit, under **Liveness.** | `observePod` reduces it to `PodFailed`, and the pod retires | The label check precedes that reduction, so the pod stays. `restartPolicy` is `Never`, so the exited container's memory is gone, and the volumes stay while `keepalive` runs. |
| Force-terminate, tenant suspension drain, user invalidation, and erasure drain | Session ends and the pod is released | Session ends, and the release step skips a labelled pod. |
| Tenant deletion Phase 5, and pool deletion | CRDs removed | The labelled Sandbox is skipped, and the deletion reports it to the forensics reconciler. |
| Node drain | preStop checkpoint, then eviction | The PDB refuses eviction, so the drain waits. |
| Node-pressure eviction | Kubelet evicts | No Lenny control applies, because the kubelet honours neither PodDisruptionBudgets nor the grace period. See "Residual risks". |
| Workspace over `emptyDir.sizeLimit` (§4.4.4 **Hard workspace size limit.**) | The kubelet evicts the pod. The tree sets no limit on the workspace volume today, and EG-2 adds it, because the egress design relies on it. | No Lenny control applies, and the eviction kills `keepalive` and removes the volumes. A running pod's `sizeLimit` cannot change. An unfrozen agent can trigger it at will, which is an embedded pod before the node-side freeze, or any pod under `freeze: false` or a failed freeze. FE-1 adds a statement to §4.4.4 that this eviction applies to a quarantined pod. |
| Liveness probe (§4.4.3 **Embedded adapter liveness integration:**) | The specification requires `/healthz` to fail a liveness probe on `checkpointStuck`, and the tree renders no liveness probe today. | Any node-side freeze of an embedded container stops `/healthz`, so such a probe would have the kubelet kill the container and lose its memory. A running pod's probes cannot change. FE-1 therefore amends §4.4.3: the adapter exits non-zero when `checkpointStuck` is set, which with `restartPolicy: Never` retires the pod as the probe would have, and agent pods carry no liveness probe (decision 26). |
| Coordinator hold timeout (§10.1) | After `coordinatorHoldTimeoutSeconds` (120 s), the adapter ends sessions and closes the runtime's `CH-MSGSOCK` | The gateway first sends a new RPC, proposed as `CH-PRESERVE`, on `LNK-POD-GRPC`. It puts the adapter into a state that never ends the runtime, sends it nothing, and never exits. §10.1's hold state accepts this RPC as well as `CoordinatorFence`. An embedded adapter is the agent, so its compliance is best effort. |

### Keeping the volumes

A pod's object existing is not enough to keep its volumes. The kubelet's `SyncTerminatedPod` "cleans up a pod that has terminated (has no running containers)" and waits for volumes to unmount, and the emptyDir plugin's teardown ends with `os.RemoveAll`. With `restartPolicy: Never`, a pod whose regular containers have all exited therefore loses `/workspace`, `/artifacts`, `/sessions`, and `/tmp`. The removal of data for this exact case is inferred from that source and is untested. A native sidecar does not prevent it, because the kubelet stops sidecars after the regular containers, so `netagent` gives no protection.

An exiting agent can therefore destroy the volumes on an embedded pod, where the adapter is the agent, and on a sidecar pod whose adapter has exited. The pod spec cannot gain a container after creation, and whether a running ephemeral container keeps a pod non-terminal is unverified, so every agent pod carries `keepalive` from creation (decision 25). `agentContainerTerminated` consults only the adapter and runtime containers, so `keepalive` does not change retirement of a pod with no quarantine: the controller still retires it by deletion, and `keepalive` exits on SIGTERM. The agent UID cannot signal it, because it has another UID and another PID namespace. `keepalive` protects against container exit only, and an eviction or a compromised guest or Sentry removes it with everything else.

### Freezing the agent

Quarantine cuts the agent's reach, and the runtime process keeps running. §10.1 states that in hold state the runtime "continues running". It can overwrite its workspace, delete files, and scrub its memory. A freeze follows the acknowledged preservation RPC (decision 22):

- **In the pod, sidecar model.** After acknowledging `CH-PRESERVE`, the adapter sends `freeze` on `CH-SUPERVISE`, and the supervisor sends SIGSTOP to every other process in the runtime container's PID namespace. The supervisor keeps answering the adapter. Automatic, immediate, and best effort, because §4.7.11 item 8 places the supervisor on the untrusted side.
- **In the pod, embedded model.** No supervisor exists, and the adapter is the agent, so no in-pod freeze applies.
- **From the node.** The FE-5 capture Job freezes the container: `runsc pause <id>` on gVisor (the subcommand exists in `runsc/cmd/pause.go`, and its behaviour on a Lenny pod is untested) and `cgroup.freeze` on runc, whose kernel documentation states that a frozen process stays halted and can still be killed by a fatal signal. No Kata method has been identified.

The node-side freeze is the sole control over an embedded agent, which can otherwise fill the workspace and trigger the eviction above, so the runbook applies it first on those pods. The freeze slightly alters what it preserves: SIGSTOP changes process state and sends SIGCHLD to parents. Whether a frozen process can then be checkpointed is a validation item. An investigator who needs the agent observed live sets `freeze: false` on the request.

### Sessions on the pod

`sessionDisposition: fail` writes `failed` with the existing reason `orphan_pod_terminated` (§10.1), so the client sees the failure a lost pod produces, and the true cause sits only in the platform-tenant row (FP3). The handler forces no auto-retry, and §7.3 treats a reason on neither retry list as non-retryable. Reporting an accurate but generic reason is part of decision 6. Leases return through the §11.4 lease step, which stops proxy-mode LLM access at once. A direct-mode credential stays usable until it is revoked, which FE-5 covers.

`sessionDisposition: resume` uses the `pod_evicted` path, so the client sees a recovery on a replacement pod and no failure. The default is `fail`, because a resume restores the session from a checkpoint the compromised pod may have written, and it continues any injected instruction in the transcript. An investigator chooses `resume` when the suspect is the pod rather than the session.

The API returns every session on the pod. A concurrent-slot pod can hold several sessions of one tenant, because §5.2 forbids cross-tenant slot sharing, and all of them end. A service-mode pod has no session, so the pin stops routing, and the router sends the tenant to another pod. A warm pod with no claim is quarantined through the label alone. `includeDelegationTree` resolves the pods of the session's tree, and each pod gets its own quarantine under the same case.

### Order and failure behaviour

1. The handler commits the row and the custody event in one step. A failure returns `503`, and nothing has changed. It then pushes the binding change, so egress, mints, and mirror reads stop through the row while the network path to the adapter stays open.
2. Unless `preserve: false`, it sends `CH-PRESERVE` and waits for the acknowledgement, bounded by `forensics.quarantine.preserveTimeout` (5 seconds). The adapter then starts the freeze. On an embedded pod the RPC reaches the agent itself and warns it before the cut, so an investigator who suspects the runtime sets `preserve: false` and accepts the hold timeout at 120 seconds.
3. It stamps the label, which cuts the network and removes the pod from inventory, and it sets the claim phase. The label follows the RPC, because the RPC needs `allow-gateway-ingress`, and because a pod cut off first enters hold state and closes `CH-MSGSOCK` at 120 seconds.
4. It ends the sessions.
5. The forensics reconciler creates the PDB and retries any incomplete step, and it reports per-step status on the row.

| Failure | Behaviour |
| :-- | :-- |
| `CH-PRESERVE` unacknowledged at the timeout | The sequence continues, and `preserve: unacknowledged` is reported. The runtime may lose `CH-MSGSOCK` when the hold times out, so the runbook captures memory at once. `keepalive` still protects the volumes. |
| Freeze fails or is unavailable | `freeze: failed` or `unavailable` is reported, and the capture Job freezes from the node. |
| Postgres is down | The API refuses. A cluster admin in `forensics.breakGlassGroups` can stamp the label with `kubectl`, which denies egress, mints, and network paths at once. The webhook logs the requesting user. When Postgres returns, the reconciler creates the row with origin `break_glass` and a custody event, and the Kubernetes audit log is the record of who stamped it. |
| API server is down | The row is committed and the label step retries. `pkg/binding`'s store re-read applies the state within its 30-second bound. The informers serve their last cache. |
| Webhook unavailable | Every labelled write and delete is refused, which is the fail-closed direction. |
| Pod already terminating | The API returns `409 QUARANTINE_NO_POD`. |
| Node lost, or kubelet evicts | The reconciler records `forensics.evidence_lost` with what remains (registered captures, evidence streams, and held artifacts) and leaves the row visible. |
| Quarantine ages past `forensics.quarantine.alertAfter` | The `QuarantineAged` alert fires. Nothing is released automatically (decision 11). |

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

- **Flag.** The gateway sets the held flag in the store index (EG-5) on the mirror's parent, authenticated by the gateway's identity. The flag stops the sweep's re-chown, repacks, and purge, as the hook states. It seals the store's current generation: no fetch or `upload-pack` touches it once the in-flight fetch has ended and its sweep has run. The re-chown is skipped so that on-disk owners keep saying which fetch UID wrote each file.
- **Fetch UIDs.** The egress design returns a fetch UID to the pool only after the sweep and the re-chown. A skipped re-chown leaves files owned by that UID, and a public store's directories are mode `2775`, so a later fetch reusing that UID could traverse and write them. The UIDs that own files in a held store therefore stay out of the pool until release, and the index persists that set so a restart honours it, as the hook states. The range bounds concurrent fetches, so `gitMirror.heldUidBudget` caps how many UIDs held stores may retire, and a hold past the cap re-chowns after recording an ownership manifest.
- **Snapshot.** The forensics reconciler creates a CSI `VolumeSnapshot` of the replica's volume, after the flag is acknowledged or after 10 seconds, whichever comes first, using a snapshot class with `deletionPolicy: Retain`. The snapshot does not depend on the parent, so a compromised parent cannot withhold it. The controller gains `create` and `get` on `volumesnapshots` in the mirror's namespace, and no `delete`.
- **Extraction.** A volume holds several stores, including other tenants', so the snapshot is an interim artifact. The FE-5 procedure mounts a restored copy in a job, archives only the held store with numeric owners, modes, and ACLs, hashes each pack, and registers the archive as a capture. The snapshot is then deleted through the cluster admin's rights. A deployment without CSI snapshots skips this step, and the archive runs against the live store while the flag holds, which a compromised parent can affect. `gitMirror.forensics.volumeSnapshotClass` selects the behaviour.
- **Continued service.** Every hold moves service to a fresh generation under the same store identity, using the hook's on-disk generation, and the mirror fills it from the upstream on the next request. A frozen store that kept serving would stop refreshing, so the egress design's read-after-push wait could not complete and tenants would see stale refs. Serving a sealed generation would also keep a poisoned store in use. A hold therefore costs one refetch, and tenants see a slower first fetch and no failure, including on a public store. Read-after-push targets the current generation.
- **Tenant view.** Held bytes are excluded from the per-tenant mirror storage quota (P7), and neither the quota read nor the prefetch API exposes the flag (FP3).
- **Tenant deletion.** The §12.8 mirror purge skips a store whose index or `security_holds` row says it is held, and it fails closed when it cannot read them. A tenant store held under a deferring basis keeps its generation through teardown until release, and one under any other basis is purged with a `forensics.evidence_lost` event. A public store holds no tenant's data, so purge does not reach it. The tenant-readable receipt follows the rule in FE-3, and the platform-tenant case record lists the store.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Mirror unreachable, or the replica lost | The row stays `pending`, the snapshot proceeds, and the reconciler retries the flag with an alert. Purge reads the row, so it cannot run in the gap. |
| Snapshot fails or no class is configured | The hold stands, `snapshot: unavailable` is reported, and the case shows it. |
| Compromised parent ignores or forges the flag | The snapshot and the fetch records (`git.operation`, `enforced` from the mirror) are the evidence. The limit is stated under "Residual risks". |
| Volume full while held | The mirror's capacity alert fires, a held generation is never deleted to make room, and fresh generations fail over to the `direct` route when the volume cannot hold them. |

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
| `artifacts`, `checkpoints`, and `transcripts` | Copied to `security-hold/{tenant}/{case}/...` in the region's escrow bucket, using the existing `storage.regions.<region>.legalHoldEscrow` entry and a new `securityHoldKekId`. FE-3 reuses `pkg/legalholdescrow`'s read and re-encrypt path and keeps its own records (below). The originals follow normal rotation and deletion. | Until release |
| `audit` | A contiguous segment of the tenant's chain for the time range, as canonical tuples with `prev_hash`, `sequence_number`, and `event_schema_version`, plus the tuple before the range. Filtering would break the per-tenant chain, and §12.8 step 13 states that the table has no `user_id` or `session_id` column, so every scope copies every row of the tenant in range, and a reader filters afterwards. Anchor events for held evidence fall in that segment, and a release cannot leave them behind. | Until release |
| `evidence` | `lenny-evidence` sets the held marker on every manifest row in range, and on new rows while `to` is absent, and reports the rows to the hold. A release recomputes the marker from the remaining holds, since several holds can cover one row. | Until release, after which normal stream retention applies |

**Separate records.** `POST /v1/admin/legal-hold` with `hold: false` calls `ReleaseForSession`, which lists `Records.ActiveForSession` and deletes each object, whether or not a legal hold was ever set (F-12.8.64), and the handler returns `escrowReleased` when the count is above zero. Security copies therefore never enter those records. They live in `security_hold_items` under the hold, in their own prefix and under their own KEK, and `Releaser` has no path to them, so a tenant admin can neither delete one nor learn of one through that count.

**Open-ended holds and retention races.** The handler marks manifest rows and starts the copy before it returns, and the sweeper and compactor read the marker in the transaction that deletes or merges. While a hold is active, a worker continues copying:

- **Audit.** At `forensics.holds.auditCopyInterval` (5 minutes), the worker extends the segment for an open range and advances `auditCursor`. The §16.4 partition GC gains a second guard: it holds a partition containing any tenant row in a hold's range above that cursor, and it emits `AuditPartitionDropBlocked` with `reason: security_hold`. `POST /v1/admin/audit-partitions/{partition}/drop` refuses that partition while the guard is active. Once the tuples are written the partition can drop, so a hold does not retain other tenants' rows past their retention.
- **Artifacts.** The worker enumerates new artifacts, checkpoints, and transcripts for named sessions and users. Every path that deletes one (the GC, checkpoint rotation, `DeleteByUser`, and `DeleteByTenant`) first calls `EnsureCopied` against the hold index, which closes the interval between the worker's runs. A copy failure refuses the deletion, and the deleter retries.
- **Teardown.** Phase 3.5 flushes the audit segment to the tail and runs `EnsureCopied` for every in-scope item before Phase 4, because the audit eraser has no escrow skip and no later rows exist to copy afterwards.

**The §12.8 ledger.** Security holds stay outside the legal-hold ledger. Step 0(c) and Phase 3.5(c) block on `audit_range` and `workspace_snapshot` entries and return 409s that list holds, so placing security holds there would both block erasure and reveal them. Nothing sets those two types today, so §12.8 and §15.1 remove them, the `ERASURE_BLOCKED_BY_LEGAL_HOLD` and `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` text narrows to sessions and artifacts, and the tripwire test on the setter stays as it is. The tenant deletion controller reads `security_holds` directly, in the separate step under "Interaction with erasure and teardown". This resolves the gap the outline named by removing the unused ledger types.

**Tenant visibility.** `GET /v1/admin/legal-holds` excludes these holds for every caller, and `lenny_tenant_legal_hold_active_count` does not count them. Tenant reads of a stream apply the stream's normal retention window, so a held row past that window reads as expired. Held bytes are charged to the platform and excluded from `storageQuotaBytes`. Tenant reads of a quarantined pod's later records are covered under "Records after a quarantine".

### Interaction with erasure and teardown

**`DeleteByUser`.** Step 0 does not block on a security hold, because a `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` lists holds to a tenant-admin caller. The job runs and deletes the originals in the tenant's stores. A hold whose `basis` is in `forensics.holds.deferErasureBases` keeps its escrow copies and held evidence, skips destroying the per-session key, and adds the job to its pending erasures. At release, the worker deletes the copies, destroys the keys unless another hold covers them, and writes a `gdpr.erasure_deferred_completed` platform-tenant event. A hold with any other `basis` does not defer: the job deletes its in-scope copies and destroys the keys, and a `forensics.evidence_lost` event tells the investigator.

**Basis.** `basis` is `legal_obligation`, `legal_claims`, `security_incident_response`, or `customer_instruction`, and `basisRef` names the approval, which the design does not supply. Article 17(3)(b) and (e) name legal obligations and legal claims. Recital 49 describes network and information security as a legitimate interest and is not a 17(3) exception, so `security_incident_response` is outside the default `deferErasureBases`. §11.8 states that Lenny operates as a data processor, so retaining against a controller's instruction needs the operator's own ground, and which ground fits an investigation is for counsel (decision 24). A hold cannot be created without both fields.

**Receipt.** The `legal_basis` enum in §12.8 belongs to the `RedactionReceipt` for dead-lettered rows, so this design leaves it alone. The tenant-readable `gdpr.erasure_completed` event and job status have the normal form. Whether the receipt states that something was retained is decision 7, and the default is the neutral form, with the retained items recorded in the platform-tenant case. A dead-lettered row in a held audit segment keeps its unredacted copy under the same deferral.

**Tenant deletion.** Phase 3.5 enumerates security holds and does not block on them, since the originals are already segregated. `deferErasureBases` applies here as it does under `DeleteByUser`, because a basis that is no ground to retain against an erasure request is no stronger against a deletion: a hold outside the list loses its copies, its keys, and any held store, with a `forensics.evidence_lost` event, and offboarding evidence on that basis then rests on the retention class. For a hold in the list, it waits for any hold still `materializing`, and it escrows held evidence: `EvidenceStore` moves marked batches to the escrow prefix, unwraps per-session keys under the tenant KEK, and rewraps them under the escrow KEK before Phase 4a destroys the tenant key. Batch bytes do not change, so anchored digests still verify. The audit segment carries the anchors the Phase 4 audit eraser deletes. The Phase 6 receipt keeps its normal fields, and the case records what was retained. If materialization cannot finish before the §12.8 deadline, the phase blocks fail closed, and the existing `TenantDeletionOverdue` alert fires on the operator side with no tenant-visible reason.

**Restore.** The post-restore reconciler's ledger gate (§25.11 `confirm-legal-hold-ledger`) gains `security_holds`, so a restored database does not replay an erasure that a hold deferred.

### Security retention class

A stream can carry `retentionClass: security` at the deployment level. For those streams, tenant teardown moves batches inside the stream's retention window into the escrow prefix, where they stay for `forensics.retention.afterTeardownDays`, regardless of any hold. The class exists because a tenant that offboards after a compromise otherwise removes the evidence (R4), and the egress design states that tenant deletion removes these streams today.

The recommended members are `credential.mint`, `egress.decision`, `egress.tunnel`, and `git.operation`, because they carry session identifiers and no content. `agent.activity.*` stays out unless a hold covers it, because of its sensitivity. The chart default is 0 days, and a SaaS operator sets a value that its customer agreement states (decision 8). The design makes no claim that this retention meets an Article 17(3) exception. Recital 49 is one argument for counsel, limited to what is strictly necessary and proportionate. §12.9 gains a row for retained evidence.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Region has no escrow entry | `SECURITY_HOLD_REGION_UNRESOLVABLE`, with the same fail-closed rule as `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`, and `DataResidencyViolationAttempt`. |
| A source object is missing | The hold continues, and the item is listed as `missing` with a `forensics.evidence_lost` event. This differs from the override path, which treats a missing source as a hard error, because a hold has to preserve whatever remains. |
| `lenny-evidence` is unavailable | The hold stays `materializing`, retries, and alerts. The sweeper's marker read fails closed, so nothing is deleted under a read error. |
| Compromised `lenny_erasure` role or a database superuser | The escrow bucket and the SIEM copy are outside that role. §11.7 states the superuser limit, which still applies. |

## FE-4: Reconciler

### Independent sources

A compromised decision service writes `egress.decision` and receives Envoy's access log stream, and it supplies the tags Envoy logs. A reconciler reading only those records would compare one component with itself. Okta's root cause report shows the general risk: scoping missed activity that produced "an entirely different log event", so this design compares sources rather than trusting one. The sources below are outside its control, and the reconciler uses a tag from a suspect component only to look up a record, never to attribute one (FP6).

| Source | What it measures | Independent of | Status |
| :-- | :-- | :-- | :-- |
| Envoy-measured fields, sent by a second access logger directly to `lenny-evidence` | Downstream peer address, CONNECT authority, upstream address and source port, times, bytes, and the Envoy replica | The decision service | `egress.accesslog`, from EG-10 |
| `credential.mint` from the Token Service | Each mint, with its own `pkg/binding` read | The decision service | From EG-2 |
| `git.operation` from the mirror | Fetches through the mirror, with its own binding read | The decision service | From EG-5 |
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
| R-TUNNEL-AFTER-END | Tunnel end with the binding's end from `pod.address` and the session row | A tunnel outlived its binding past the grace. This is the visible sign of a compromised `netagent`. |
| R-QUARANTINE-BREACH | Any allow, mint, or mirror read after a quarantine's set time plus the 30-second bound | An enforcement point did not apply the state. Critical. |
| R-MINT-NO-SCOPE | `credential.mint` with the decision service's scope decisions | One gate lacks a record of the other. |
| R-MINT-WRONG-TENANT | `credential.mint` tenant with the binding's owning tenant | A tenant's credential reached another tenant's pod (P4). Critical. |
| R-GIT-CLAIM | `netagent`'s `git.operation` with the mirror's record, the access log, and any provider log | A self-reported operation has no enforced counterpart, or the reverse. Git on an uncovered host is an opaque tunnel and writes no `git.operation`, so a missing record there does not mean no push, and `egress.tunnel` is the only trace. |
| R-LOCAL-CLAIM | `egress.local` tunnel closes with the access log's end times | `netagent` reported a close that Envoy never saw. |
| R-SEQUENCE | Sequence gaps, incarnations without a closing record, compaction anchors, and §11.7 chain state | Loss or tampering, using the detection the egress design specifies. A gap alone is a low-severity finding. |
| R-EDGE | The access log's byte counts with NAT or flow logs, per replica and time bucket | Traffic left the cluster outside the proxy, or the proxy under-reported. |
| R-ACTIVITY | `agent.activity.tool` commands with `agent.activity.exec` | A reported command with no process, or a process with no tool call in its ancestry. Informational, because scripts legitimately spawn children. |

A lossy input produces low-severity findings on its own, because `egress.tunnel` can lack close records during an Envoy crash. A rule raises severity when two independent sources agree against a third. At level 1, Envoy is absent, so `egress.accesslog` is empty and the rules that read it do not run. `egress.decision` is `self_reported` there, the deployer's proxy log is an imported source, and `coverage` on a finding states this. An `internet` pool's raw sockets also pass no proxy, so only R-EDGE and `connect` records observe them.

### Component, API, and failure behaviour

`lenny-evidence-reconcile` is a Deployment in `lenny-system` with its own identity: read-only rights on the evidence bucket, a read-only manifest role, and any T4 KMS decrypt rights per region, with a producer SAN allowed to write one stream. It carries required anti-affinity against the decision service, for the reason the egress design gives for the mirror. It runs each tenant-hour after compaction, on a staggered schedule, over `egress.*`, `credential.mint`, and `git.operation`, and it scans objects, so its cost grows with evidence size. `POST /v1/admin/evidence/reconcile` runs a case's range on demand, including activity streams and imported sources.

Findings go to the new stream `evidence.reconcile`, with provenance `observed`, under the platform prefix when a finding spans tenants. `GET /v1/admin/evidence/reconcile/findings` reads them and requires `forensics.query`. Critical findings raise `EvidenceDisagreement`, and `ReconcilerStale` fires when a tenant-hour has gone unprocessed past a bound. Reconciler failure affects no traffic. A compromised reconciler can suppress findings, and the deployer's SIEM copy of the underlying streams (SO-3) is the check on it. A compromised `lenny-evidence` can drop any stream before it is anchored, which the imported sources and the SIEM copy reveal when they are configured.

## FE-5: Capture runbook and abuse workflow

### Capture commands and storage

The design adds no standing privileged Lenny component for capture (decision 13). The chart ships an unbound ClusterRole, `lenny-forensics-capture`, holding `nodes/checkpoint`, `get` on `pods` in agent namespaces, and `create` on Jobs in a `lenny-forensics` namespace, for a deployer's privileged access workflow to bind for the duration of a case. The Kubernetes documentation warns that `nodes/proxy` authorizes executing commands in any container on the node, and it maps `/checkpoint/*` to `nodes/checkpoint`, so the role never holds `nodes/proxy`. The capture Job runs privileged on the target node, under `lenny-forensics` admission rules separate from those of agent namespaces, using a digest-pinned image Lenny publishes.

| Command | API | Behaviour |
| :-- | :-- | :-- |
| `lenny-ctl admin forensics case open\|close\|get` | `POST /v1/admin/cases`, and related | Case record |
| `lenny-ctl admin forensics capture register` | `POST /v1/admin/cases/{id}/captures` | Records the pod, kind, collector, tool version, digest, size, and time, and returns a gateway-minted presigned `PUT` with a single key, a single method, an exact `Content-Length`, and a short expiry, on the rule NET-071 sets. |
| `lenny-ctl admin forensics capture get` | `GET /v1/admin/cases/{id}/captures/{captureId}` | Requires `forensics.capture.read`, returns a presigned `GET`, and writes a custody event. |
| `lenny-ctl admin forensics attribute` | The egress design's cross-tenant query | Takes address, port, target, and time, and returns candidates (below). |
| `lenny-ctl admin forensics export` | `POST /v1/admin/cases/{id}/export` | Writes a bundle manifest of captures, held items, digests, anchors, and custody events, and `lenny-ctl admin forensics verify` recomputes them. |

### Capture order

The runbook `docs/runbooks/forensic-capture.md` follows the §25.7 format, and the order follows RFC 3227: memory and process state first, then temporary file systems, then disk, then remote logs. RFC 3227 also warns against shutting a system down before collection, because evidence is lost. Quarantine comes first, in tension with that order, since quarantining changes network state. The runbook takes quarantine first because stopping an active exfiltration outweighs the changed network state, and it records the pod's network state before the label where the operator has time.

1. Open a case. Quarantine the pod, and read `preserve` and `freeze` from its status. If `lenny-ctl` is unavailable, use the break-glass label, which reaches the adapter with no RPC, so the runtime may lose its socket after 120 seconds.
2. Set a security hold covering the tenant and range as soon as a basis is approved. Quarantine does not wait for it, and normal retention applies to anything outside the pod until it lands.
3. Freeze from the node where the in-pod freeze is absent or failed, which comes first on an embedded pod because of the workspace eviction, then capture memory and process state (table below). Each archive contains every memory page of the checkpointed processes, including keys (Kubernetes warning), so the Job encrypts it to the case's public key before it leaves the node and removes the node copy.
4. Copy tmpfs volumes (`/sessions`, `/tmp`, and `/run/lenny` for the credential file), then `/workspace` and `/artifacts`, then container logs. The Job hashes each file.
5. Register each capture.
6. Reset open tunnels if R-TUNNEL-AFTER-END fires or `netagent` is suspect. Envoy has no per-tunnel kill, so the procedure runs the chart's graceful drain against the replicas named in `egress.accesslog`, which interrupts other tenants' tunnels on those replicas until they reconnect. On a shared replica set, an investigator weighs that against the pool's remaining tunnel cap.
7. Rotate credentials. `credential.mint` lists every token and secret version the pod received. Revoke direct-mode LLM credentials through `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke`. For a `userCredential` binding, follow the limits under "Delivered user tokens": remove the user's link for an `oauth2Link` token or child, which revokes its grant when its token trust record still matches, and for a `registered` value act on the exposure rows through the tenant's backend. Changing a record because it was compromised is not enough, because replaying the record restores its tokens, so the case revokes at the provider or deletes the rows.
8. If a container escape is suspected, cordon the node and keep the pods running, so evidence is preserved before workloads move, as Wiz's guidance states. Drain only after collection.
9. Release the quarantine.

Memory capture by isolation profile, each marked as untested here:

| Profile | Method | What the sources establish | Validation owed |
| :-- | :-- | :-- | :-- |
| `standard` (runc) | Kubelet `POST /checkpoint/{namespace}/{pod}/{container}`, per container. | Beta and on by default since Kubernetes 1.30. Returns 500 when the CRI runtime does not implement it. KEP-2008 states the container continues without knowing. CRI-O implements it, and containerd's CRI plugin has a handler that requires CRIU on the node. | Run on the deployment's runtime version. |
| `sandboxed` (gVisor, the default) | `runsc checkpoint --leave-running --image-path=<path> <id>` from a Job on the node. | The gVisor guide documents raw `runsc` and Docker usage and says containers stop after a checkpoint unless `--leave-running` is set. It does not cover Kubernetes. | Whether the CRI path works, how to locate the sandbox root on containerd, and which containers in the pod the sandbox checkpoint covers. |
| `microvm` (Kata) | Hypervisor-level guest memory dump. | No source found documents a procedure. An old copy of Kata's limitations page states that the runtime provides no checkpoint and restore commands. | The whole method, per hypervisor. |

An attempt that fails falls back to step 4. `kubectl debug` is a poor substitute for memory capture, because `lenny-ephemeral-container-cred-guard` blocks the identities that could reach credentials, and adding a container changes the pod.

### Abuse workflow

`docs/runbooks/abuse-report.md`:

1. Record the report in a case: source address, port when given, destination, time, and the reporter's clock source. RFC 7422 states that identifying a subscriber behind a shared address depends on source port and accurate timekeeping, and that a report often lacks a port.
2. On a shared cloud NAT, map the public address and port to the `lenny-egress` replica with the NAT log. Without that log, the query returns every tunnel from the egress address in the window, ranked by target match. The operations guide requires the deployer to retain NAT logs for at least the longest evidence retention.
3. Run `attribute` with a window of plus or minus 60 seconds. It reads `egress.tunnel`, joins `egress.decision`, and checks attribution against `egress.accesslog` and `pod.address`, so it reports a mismatch beside each candidate. It also searches `connect` records in `agent.activity.exec` by destination. An empty result is never a clearance: the response always carries `coverage`, listing from `pod.address` each pod in the window whose traffic Envoy does not see (`internet` pools, and every pod at level 1) and whether it had `connect` coverage. On a NAT log or node address, the list narrows to the pods on that node. At level 1 the deployer's proxy log is the record of the tunnel, and the runbook reads it.
4. For each candidate and each uncovered pod the list narrows to, read the session's owner, then the `connect` records in `agent.activity.exec` in the same pod around the tunnel's start, when coverage exists. That join is a time-based correlation and is labelled so.
5. Classify: tenant-directed abuse, a prompt-injected agent, or a compromised credential or component. Each class names its next action. An abusive tenant gets quarantine before suspension, because suspension drains sessions and a pod with no label is released.
6. Reply to the reporter with the action taken and no tenant identity. Contacting the tenant is a case action, after decision 6's rule.

## FE-6: Agent activity streams

### What can observe an agent

Lenny's default isolation is gVisor, §5.3 restricts `runc` to development and testing behind `allowStandardIsolation`, and §5.3 lists Kata for higher-risk multi-tenant workloads. A host eBPF sensor, which the outline named as its example, therefore sees the Sentry and the gofer on the default profile rather than the agent's processes.

| Source | What it sees | What it cannot see | Sidecar | Embedded | runc | gVisor | Kata | Provenance | Privilege |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Gateway: platform and connector MCP calls (`CH-MCP-PLATFORM`, `CH-MCP-CONNECTOR`), `PreConnectorRequest` | Every call that crosses `LNK-GWCONTROL`, with arguments | Anything local to the pod | Yes | Yes | Yes | Yes | Yes | `enforced` | None |
| Adapter: `tool_call` frames for adapter-local tools (`read_file`, `write_file`, `list_dir`, and `delete_file`) | Those tools with paths, for runtimes that use them | A runtime's own shell or file tool, because §28.5.3 sends only adapter-local tools on that path | Yes, and the adapter runs at its own UID | Yes, and the adapter is the agent, so a compromised agent writes the record | Yes | Yes | Yes | `self_reported` | None |
| Session transcript (§7.1) | Tool calls as the runtime recorded them | Everything the runtime omits or rewrites | Runtime dependent | Runtime dependent | Yes | Yes | Yes | `self_reported` | None. Retained 7 days by default. |
| `PreLLMRequest` interceptor (proxy mode, §4.8) | Message history, including earlier tool-use blocks in provider format | Direct mode, the response stream (`PostLLMResponse` fires once on metadata), and anything the runtime omits from history | Yes | Yes | Yes | Yes | Yes | `self_reported` in substance | None. Provider-specific parsing. |
| `GET /v1/sessions/{id}/setup-output` | Setup command output | Anything after setup | Yes | Yes | Yes | Yes | Yes | `self_reported` | None |
| `egress.*`, `git.operation`, and `credential.mint` | Network, git, and credentials | Process and file activity | Yes | Yes | Yes | Yes | Yes | As stated in the egress design | None |
| Runtime SDK producer (new) | Tool name, intent, command text as the agent stated it, the model turn and `toolCallId`, and files the tool opened | Children of a command, anything a compromised or uninstrumented runtime omits, and a runtime with no SDK | Yes | Yes | Yes | Yes | Yes | `self_reported` | None |
| Supervisor tap (seccomp user notification or ptrace from the namespace init) | `execve` and `openat` in the runtime container | Tampering by a process that attacks the supervisor, which §4.7.11 item 8 places on the untrusted side | Yes | No supervisor | Yes | Unverified | Yes | `self_reported` | None, but a trap on each traced syscall |
| Host eBPF sensor (Tetragon, Falco) | Under runc, every process with arguments and pod identity; file access through kprobes with in-kernel path filters | Under gVisor, syscalls the Sentry handles, so the host sees the Sentry and gofer. Under Kata, the guest kernel. Files reached through a hard link or another bind mount, per Tetragon's documentation | Yes | Yes | Yes | No | No | `observed` | Privileged DaemonSet |
| gVisor runtime monitoring (`seccheck` remote sink) | Trace points for all syscalls and for events such as container start, with context fields such as PID, UID, and container ID | Other profiles. Anything after a sink disconnect, per third-party trials. A compromised Sentry. | Yes | Yes | No | Yes | No | `observed` | None in the sandbox. A node process owning a Unix socket, and node configuration of `runsc`. |
| In-guest sensor under Kata (eBPF in the VM kernel, as Ant Group's AntCWPP does) | Guest processes and files | A compromised guest kernel | Yes | Yes | No | No | Yes | `self_reported` under the envelope's rule, because it runs inside the pod's VM | Guest kernel build and a node agent |

Evidence for the host-sensor limit comes from several directions. Google's documentation states that Container Threat Detection is not compatible with GKE Sandbox. AWS documents that GuardDuty Runtime Monitoring does not cover EKS on Fargate. The Kubescape proposal for gVisor states that its node agent sees host kernel activity while many application system calls are handled inside the Sentry. The Sysdig press release and gVisor's tutorial show Falco moving to gVisor's own trace points for this reason. No source states the Tetragon limit in those words, so this design records it as inference from the architecture, and the first spike tests it.

Each product surveyed separates the two concerns. Claude Code's documented telemetry emits `claude_code.tool_result`, withholds command text unless `OTEL_LOG_TOOL_DETAILS=1` is set, and offers `PreToolUse` and `PostToolUse` hooks that receive `tool_input.command`. Those are tool-level records, and the page does not address child processes. Gemini's agent hooks are similar. These are the semantic layer, which says why a command ran, and none of them claims to see what the command then did.

### Recommendation

Use layered producers, joined by `podUid`, `sessionId`, `traceId`, and time.

1. **FE-6a, tool activity, on every profile and pod model.** The gateway records platform and connector calls as `enforced`. The adapter records adapter-local tools. The runtime SDK gains an `activity` report with a tool name, `toolCallId`, input, and outcome. These are `self_reported`, and they give an investigator the agent's account of what it did, including which model turn asked for it.
2. **FE-6b, process and file activity, from a `lenny-activity-collector` DaemonSet reading gVisor's remote sink.** The default profile gets `observed` evidence with no eBPF and no privileged container (FP9), because the Sentry dials out to a Unix socket on the node. The egress design's objection to a privileged component on agent nodes does not apply to this collector. Its objection to per-node capacity does, and that cost is stated under "Volume and cost".

The other profiles differ. `standard` pools get FE-6a, plus a published producer contract, so a deployer running Tetragon or Falco can forward to `lenny-evidence` under its own identity, and a reference forwarder for Tetragon is tested on Kind (decision 3). `microvm` pools get FE-6a and the network evidence in the first version, and the API reports `coverage: tool_only` for them, so investigators see the gap (decision 2). The supervisor tap in the table above is the first follow-up for `microvm`, because it needs no guest kernel work, and its records are `self_reported`. An in-guest sensor is later work: a guest kernel with BPF and BTF per Kata release and hypervisor, a sensor in every pod with BPF rights inside the VM, and an admission exception, as Ant Group's AntCWPP runs. Under the envelope's rule it would still be `self_reported`. A file-only watch on a pod's workspace volume from the node was considered for `standard` and `microvm` and is not used: it records that a file changed, with no process and no reads, and it needs read access to every pod's volume directory on the node, including mounted secrets.

A new pool field, `activityMonitoring: true`, selects coverage, and it is valid only with `isolationProfile: sandboxed`. A profile maps to one RuntimeClass through `runtimeClasses.profiles.<profile>.name` (§17.5, `RuntimeClassNameOverrides`), so the profile cannot distinguish the two. The controller renders the class named in `runtimeClasses.monitored.name`, by default `gvisor-monitored`, whose runtime handler points at a `runsc` configuration naming the sink. An unmonitored gVisor pool on the same node has no session and no cost. Admission policies keyed on RuntimeClass (§17.2) gain the name. Configuring the handler is node provisioning, which the cluster admin owns, as with RuntimeClass installation today (§17). GKE Sandbox preconfigures gVisor, and the Falco Helm chart installs there through a values file, so the managed path needs its own check.

### Streams

| Stream | Producer | Provenance | Payload | Default retention |
| :-- | :-- | :-- | :-- | :-- |
| `agent.activity.tool` | Gateway, including reports it forwards for the adapter and the SDK | `enforced` for calls the gateway carried, `self_reported` otherwise | `toolCallId`, tool, connector, phase, outcome, bounded redacted input, duration, and `reportedBy` | 90 days |
| `agent.activity.exec` | Collector | `observed` | `kind` (`exec`, `exit`, or `connect`), PID, parent PID, process start time, binary path, `argv[0]`, argument count, UID, container name, working directory class, exit status, and the destination of a `connect` | 30 days |
| `agent.activity.exec.args` | Collector | `observed` | The process key and its redacted, bounded arguments | 14 days |
| `agent.activity.file` | Collector | `observed` | Per process and directory prefix: operation and count per window. Exact records for the watchlist and for writes outside the workspace | 30 days |

Separate streams carry different permissions, retention, and keys, which answers the outline's question about differing audiences: a customer's security team can hold `exec` while only forensics holds `args`. Attribution comes from ingest. The collector supplies the pod and container and the time, and `lenny-evidence` sets tenant and session from the binding at that time. On an embedded pod the adapter's own work appears in the `adapter` container at the agent UID, and the record's binding state (warm-up, materializing, or running) is the distinction available. Warm-pod records have no tenant and use the platform prefix, as the envelope states. Attribution comes from the binding, as the envelope requires. A pod with one bound session gets that session. A concurrent-slot pod's records carry `candidateSessions` from the binding at that time, with no `sessionId`, and a `workdirHint` field that the agent controls and nothing else reads, so a process cannot choose its key by changing directory.

**Joining a command to a tunnel.** Prompt-injection exfiltration is the case an investigator most needs answered: which command opened this connection. gVisor's `Connect` trace point has `fd`, `fd_path`, and one `address`, with no local port, so `agent.activity.exec` cannot supply the port that `egress.local` now records. On gVisor the join is a time-based correlation of a process's `connect` to the local endpoint with `netagent`'s tunnel open in the same pod, and the query returns it with `join: time_correlated` and every candidate process. A busy pod can yield several candidates, and a `connect` may reuse a keepalive connection. The spike checks whether a trace point or an upstream gVisor change can supply the port. A host sensor under `standard` may see the socket's local port, which would make `join: exact` there, and that is also unverified. A `connect` to any address other than the local endpoints is recorded with its destination. On a `proxied` or `restricted` pool the NetworkPolicy drops it, so the attempt is a signal. On an `internet` pool it succeeds and passes no proxy, so this record is the one Lenny source for that connection, which `attribute` searches by destination.

**Trace point fields.** gVisor's `Execve` carries `pathname`, `argv`, and `envv`, and `envv` stays off. `Open` carries a raw `pathname` that the source describes as not canonicalized, and an `fd_path` for the `openat` directory. The collector resolves it against the process's working directory before the watchlist check. It cannot resolve symlinks, so the watchlist is weaker than a canonical path filter, and the residual is stated below.

**Records after a quarantine.** Tenant reads must not show `quarantined` denials or activity after a session's failure (FP3). Records from a quarantined pod, from the moment the state applies, go under the platform prefix with the tenant in the payload, as the egress design states, so they appear in no tenant read or export, and the tenant's sequence numbers show no gap. They stay until the quarantine's release plus `forensics.quarantine.recordRetentionDays` (90 by default), or longer under a hold, and tenant deletion removes them when no deferring hold covers them. Activity payloads keep their session or pod-incarnation key, and that key follows the rules under "Keys", so an erasure with no deferring hold makes them unreadable.

### Producer contract and channels

- **SDK.** The runtime writes an `activity` frame on `CH-MSGSOCK`, or calls the SDK's `reportActivity`. The adapter validates, sizes, and rate-limits it (§4.7.11 item 3), and sends it on a new channel proposed as `CH-ACTIVITYREPORT`. The gateway overwrites session and tenant from the binding, rejects a report for a session the pod does not hold, forces `self_reported`, and forwards to `lenny-evidence`, as the decision service does for `netagent`. Reporting is optional at every integration level, and an absent report shows as `coverage` rather than as an error.
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
| Detail setting | `metadata` writes no `args` stream, `redacted` is the default, and `full` writes arguments with the structural layer off, and needs a platform gate and a tenant opt-in. |

Redaction misses secrets with no recognizable form, so §12.9 classifies all four streams as T3 by default and T4 when `workspaceTier` is T4. Encryption and restricted reads are the control, and redaction reduces exposure. Tool input from the SDK passes the same rules, in the adapter, because a compromised runtime controls that input.

### Keys

Each session has a data key, generated by `lenny-evidence` at the first record, wrapped by the tenant's KMS key on T4 and by a regional evidence key otherwise, and stored in `evidence_session_keys` (tenant, session, user, wrapped key, state, and timestamps). `payloadKeyRef` names it. `lenny-evidence` encrypts on ingest, so digests are computed over ciphertext and still verify after destruction. A record with no single session (service mode, or any concurrent-slot record) uses a key per tenant and pod incarnation, selected by the binding.

| Event | Effect |
| :-- | :-- |
| Read | The gateway's handler asks `lenny-evidence` to decrypt, after its permission check. A cross-tenant platform read writes `forensics.evidence_read`. |
| `DeleteByUser` | Tenant reads of that user's sessions stop at once. The job schedules destruction of their keys after `forensics.activity.keyDestructionDelay` (0 by default), so a hold set in that window can still stop it, and it then records a tombstone. A key under a hold with a deferring `basis` is queued in pending erasures. A pod-incarnation key is destroyed when every user whose session ran on that pod has been erased, or at retention expiry, because destroying it on one user's request would let a tenant erase other sessions' evidence. The erased user's records on that pod stay encrypted until then, which is stated under "Residual risks". |
| Retention expiry | The sweeper deletes batches, and it destroys a session's key once its last batch is gone. |
| Tenant teardown | Keys are deleted with the tenant, except those under a hold or the retention class, which are rewrapped under the escrow KEK. |

A compacted object holds several sessions' ciphertext, so a hold on one session's manifests keeps other sessions' ciphertext in the same tenant-hour. Their keys follow normal retention, so those records become unreadable on schedule.

### Who may read

The streams are customer data about the customer's own agents. A tenant admin can already read session content (§10.2 `Read other users' sessions`), so a tenant read of `tool` and `exec` grants nothing new. An operator read is the new exposure. The defaults are as follows, and `security-oversight-design.md` decision 19 settles that no security role holds one by default. A `security-admin` grants a stream to `security-auditor` through `auditorStreamGrants`, under the delay:

| Permission | Default holders | Operator access |
| :-- | :-- | :-- |
| `evidence.read.agent.activity.tool` | `tenant-admin` | `forensics.query` with an open case naming the tenant |
| `evidence.read.agent.activity.exec` | `tenant-admin` | Same |
| `evidence.read.agent.activity.file` | `tenant-admin` | Same |
| `evidence.read.agent.activity.exec.args` | None until granted by a tenant admin | Same, and each read writes a custody event |

`tenant-viewer` holds none of these by default, because an activity stream can expose paths and commands of other users' sessions in a form the viewer role's session read does not present today. A tenant admin who is the subject of an investigation can read these streams, which is why the operator's copy is the one under hold.

**Activity floor.** A tenant admin could otherwise turn every activity stream off or erase a user before an investigation starts, and a later hold cannot recover a record that was never written or a key already destroyed. `forensics.activity.floor` sets a deployment minimum for each stream (which streams must stay on), detail, and retention, `agentActivity.floor` raises it per tenant under the platform admin's control, and the tenant's settings can only go above the floor. A floor that keeps `exec` or `file` on means sessions need a monitored pool, and session creation on another pool is refused. Lowering a tenant's own setting above the floor is an audited write (decision 23).

### Volume and cost

Process and file events exceed egress decisions by orders of magnitude. The figures below are planning assumptions for a 30-minute coding session, to be replaced by the spike's measurements. Daily figures use the §16.4 Tier 3 estimate of 10,000 sessions per day, and they are uncompressed.

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
- **Isolation between tenants (P7).** The collector bounds each pod, and ingest bounds each tenant. Past a bound, loss appears as a sequence gap and a gap record, as the egress design specifies, and a flood is itself recorded by the count.
- **Effect on the agent.** gVisor's documentation warns that a high number of remote sink retries can significantly delay application execution, so the configuration sets low retries and accepts a drop. An attacker can cause drops by flooding, which is visible as gaps and a rate-limit record. Tracing `openat` adds work on each open in the Sentry, by an amount no gVisor source states, so the `file` stream can be turned off per pool, and the spike measures both. For scale, Falco's project documentation gives a rule of thumb for its kernel path: below about 1,500 kernel events per second per CPU keeping up is rarely a problem, and above about 3,000 it becomes harder, with event rates depending on the workload.
- **Manifest rows.** The compaction bound of one row per tenant-hour per stream gives 120,000 rows per day per stream at 5,000 tenants. The egress design's bound is 720,000 for its six streams. The four activity streams and `pod.address` raise that to at most 1,320,000.

**Per-tenant cost.** A tenant's stored bytes are `sessions per day × Σ (MB per session × retention days)`. At the assumed sizes, that is 80 MB per daily session, so a tenant with 100 sessions per day holds about 8 GB. `lenny_evidence_bytes_total{tenant_id, stream}` and a daily `evidence.usage` billing event in §11.2.1 let an operator charge for it, and the tenant's activity bytes count against a separate `evidenceQuotaBytes`, so a full quota produces gap records and never refuses a session. The operator bears the collector's node cost in the pool's resource accounting, because `applyResources` copies the pool's class onto pod containers and the collector is outside the pod.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Collector absent when a sandbox starts | `ignore_setup_error: false` on the monitored class fails the sandbox, so the pod fails its warm fill, and the alert names the node. |
| Collector restarts with sandboxes running | A third-party plugin's README states that `runsc` connects to the sink once at sandbox start, does not reconnect, and needs a sandbox restart after the receiver restarts. A Kubescape trial states that its restart run does not establish recovery of the existing connection. Running pods therefore go dark until they retire, unless the spike finds a reattach path. The collector records `coverage: lost` for each, and `ActivityCoverageLost` fires. The DaemonSet uses `OnDelete` updates during node rotation. Whether `runsc trace create` can reattach from a Job is a validation item. |
| Coverage lost on a bound pod | `forensics.activity.onCoverageLoss`: `continue` (default) records the gap, `retire` refuses new sessions and retires the pod after its session, and `quarantine` applies on regulated tenants only by explicit choice. |
| Sentry drops a trace point | The sink's sender drop count, where reported, becomes a gap record. A third-party trial notes that zero reported drops does not prove zero loss, so absence of a record is never proof. |
| `lenny-evidence` unavailable | The collector buffers per tenant, then records a gap. Activity never blocks the agent, including on a regulated tenant, because the write-ahead rule for egress decisions exists to make a record precede a connection and the volume here would make it an availability risk. |
| Runtime does not report | `agent.activity.tool` holds only gateway and adapter records, and `GET` reports `coverage`. |
| Node loss | Unflushed records are lost, which the incarnation rule detects. |

**Coverage API.** `GET /v1/admin/sessions/{id}/activity-coverage` and a tenant equivalent return, per pod incarnation, which producers ran, from when to when, and any gap (R8).

### Read API and export

`GET /v1/agent-activity` takes `sessionId`, `stream`, `kind`, a binary or path prefix, and a time range, and it needs the stream's permission. `GET /v1/admin/evidence/{stream}` is the platform form. Export uses the egress design's cursor path, with OCSF translation at delivery: Process Activity (class 1007) for `exec` and File System Activity (class 1001) for `file`, both read from the OCSF v1.1.0 schema, and `exec.args` in `unmapped.lenny.*` only when the subscription holds its permission. A SIEM copy is outside key destruction, so the export subscription states this and §12.8 lists it as a sink to notify.

### Delivered user tokens

`user-credential-delivery-design.md` can place a person's token in a pod. `delivery: inject` keeps it in `netagent` memory, and `delivery: file` writes it to `/run/netagent/credentials/<name>/value`, readable by any process at the agent UID.

- **Quarantine.** `quarantined` in `pkg/binding` stops release on I8 from each instance's own read, so a held pod receives no new token and no refreshed one. It does not depend on `netagent`.
- **What a held pod keeps.** Two cases differ. An honest `netagent` removes the file on the binding change, so the held pod has no file, and any copy the agent made remains. A compromised `netagent`, or one frozen or stopped before the change, leaves the file in an emptyDir that `keepalive` keeps with the pod. In that case, and for any copy, the token stays valid at the provider, so **a quarantined pod can hold a live credential for a person**. The capture commands in FE-5 do not copy `/run/netagent` by default, and a capture that includes it is T4 and needs its own case entry. The identifier in `binding.json` and the hash in `credential.mint` serve most investigations with no copy.
- **Rotation limits.**
  - `oauth2Link`: remove the user's link, which revokes its grant at the provider's endpoint when one exists. Revoking one child needs the token value, and no route takes a hash, so the case works by link. That can break the user's other sessions.
  - `registered`: Lenny holds no way to revoke. The case states this, and the tenant's backend acts, from the durable exposure rows, which name each generation and the writer's `tokenId`, or on the investigator's notice.
  - Copies: nothing Lenny does reaches a copy the agent made. A token that reached a provider through an opaque tunnel shows in that provider's logs and not in Lenny's.
- **Where the token can be in evidence.** Under `file`, an agent can print it or store it, so it can reach a transcript, an LLM request, a workspace, a checkpoint, an export, or an `agent.activity` record. Sessions holding such a binding are marked `agentHeldCredential`. For these sessions, investigators treat captures and transcripts as T4 regardless of `workspaceTier`, and a report to a tenant names the session rather than quoting. Under `inject` the agent cannot read the token, except through a reflection the egress design's scrub misses.
- **Redaction.** FE-6's structural and entropy layers apply, and they miss a token with no recognizable form. Known values do not reach FE-6's collector or adapter, because the collector is a node-level process shared across tenants, and `leakScan` works in the gateway's interceptors instead. An investigator holding a leaked value tests it pod by pod through `forensics.query`, as FE-6 states.
- **Attribution.** `credential.mint` carries `tokenSource` and an opaque `credentialRef` or `linkRef`, with no user identifier, a token hash for an OAuth2 token or child, and `generation` with a per-tenant keyed hash for a `registered` value, which keeps its §12.8 treatment. That join lives in rows erasure deletes, so an investigation that needs the person after erasure works from a security hold, under FE-3's rules.
- **Reconciler.** FE-4 can add R-USER-CREDENTIAL-POD, comparing a `userCredential` mint with the pod's model, since a release to a pod that can serve another session is a defect.

## Configuration by scope

| Setting | Description | Applies to | Managed by | Notes |
| :-- | :-- | :-- | :-- | :-- |
| `forensics.enabled` | Turns on the case, quarantine, and hold APIs and the `platform-forensics` role. | Deployment | Platform admin (Helm) | |
| `forensics.breakGlassGroups` | Lists the Kubernetes groups that may stamp the quarantine label directly when the API is unavailable. | Deployment | Platform admin (Helm) | Empty disables the path. |
| `forensics.quarantine.alertAfter` | Sets the age at which `QuarantineAged` fires. | Deployment | Platform admin (Helm) | 24 hours by default. It never releases. |
| `forensics.quarantine.maxPods` | Sets the count of quarantined pods at which an alert fires. | Deployment | Platform admin (Helm) | It alerts and does not refuse. |
| `forensics.quarantine.preserveTimeout` | Bounds how long the handler waits for the adapter's acknowledgement before stamping the label. | Deployment | Platform admin (Helm) | 5 seconds by default. |
| `forensics.holds.deferErasureBases` | Lists the hold bases under which user erasure and tenant deletion defer, for copies, keys, and held stores. | Deployment | Platform admin (Helm), on counsel's advice | `legal_obligation` and `legal_claims` by default. Decision 24. |
| `forensics.holds.auditCopyInterval` | Sets how often an open-ended hold copies new audit rows. | Deployment | Platform admin (Helm) | 5 minutes by default. |
| `forensics.quarantine.recordRetentionDays` | Sets how long a quarantined pod's later records remain after release. | Deployment | Platform admin (Helm) | 90 days by default. |
| `forensics.twoPersonRelease` | Requires a release to come from a different principal than the one who set it. | Deployment | Platform admin (Helm) | Decision 12. |
| `gitMirror.forensics.volumeSnapshotClass` | Names the CSI snapshot class for store holds. | Deployment | Platform admin (Helm) | Empty means no snapshot, and the API reports it. |
| `gitMirror.heldUidBudget` | Caps the fetch UIDs that held stores may keep out of the pool. | Deployment | Platform admin (Helm) | |
| `storage.regions.<region>.legalHoldEscrow` | Names the escrow endpoint, bucket, KMS key, and KEK, reused for security holds. | Region | Platform admin (Helm) | Existing setting (§12.8). |
| Stream `retentionClass: security` and `forensics.retention.afterTeardownDays` | Selects the streams retained after tenant teardown, and for how long. | Deployment | Platform admin (Helm) | 0 days by default. Decision 8. |
| `lenny-evidence-reconcile` schedule and imported sources | Sets the reconciler's cadence and the object store prefixes holding NAT, flow, provider, and Kubernetes audit logs. | Deployment | Platform admin (Helm) | Imports are optional. |
| `forensics.activity.tool.enabled`, `forensics.activity.exec.enabled`, and `forensics.activity.file.enabled`, with per-stream retention | Turn on each kind of activity collection separately: tool and connector calls (`agent.activity.tool`), processes and commands (`agent.activity.exec` and `.args`), and file access (`agent.activity.file`), and set the ceiling on detail and retention. | Deployment | Platform admin (Helm) | Each off by default. Decision 4. |
| `forensics.activity.floor` | Sets which streams must stay on, and the minimum detail and retention, that a tenant's settings cannot go below. | Deployment | Platform admin (Helm) | `none` by default. A SaaS operator raises it. Decision 23. |
| `forensics.activity.keyDestructionDelay` | Delays destruction of an erased user's keys, so a hold set in that window can stop it. | Deployment | Platform admin (Helm), on counsel's advice | 0 by default. Decision 23. |
| `agentActivity.floor` | Raises the floor for one tenant, per stream. | Tenant | Platform admin (admin API) | The tenant cannot lower it. |
| `forensics.activity.fullDetailGate` | Grants a tenant the right to choose `full` detail. | Deployment and tenant | Platform admin (admin API) | Same pattern as the egress gates. |
| `forensics.activity.redactPatterns` and `execRatePerPod` | Extends the redaction patterns and sets the per-pod rate limit. | Deployment | Platform admin (Helm) | Patterns can be added and shipped ones cannot be removed. |
| `lenny-activity-collector` image, resources, and producer allowlist | Installs the collector, and lists deployer sensors allowed to write activity streams. | Deployment | Platform admin (Helm) | |
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
| Evidence read permissions | Grants named read permissions on each activity stream. | Tenant | Tenant admin, subject to `security-oversight-design.md` | Defaults under "Who may read". |
| Activity reporting by the runtime | Reports tool calls through the SDK. | Runtime | Runtime author | Optional at every integration level. |

No setting exists at session scope, because a prompt-injected or malicious client could use one to reduce evidence.

## Scenarios

### An abuse report from an IP and a timestamp

A hosting provider reports that 203.0.113.7 scanned its host 198.51.100.20 on port 443 at 2026-11-20T14:03:11Z. It gives no source port.

1. Dave, on the operator's forensics team, opens a case. The address is the shared NAT for the default `lenny-egress` Deployment.
2. `lenny-ctl admin forensics attribute --address 203.0.113.7 --target 198.51.100.20:443 --at 2026-11-20T14:03:11Z --window 60s` reads `egress.tunnel` by time and target, and returns three tunnels. All three carry the same session in `acme`, owned by `alice@acme.com`. The attribution check against `egress.accesslog` and `pod.address` reports no mismatch.
3. `agent.activity.exec`, read under the case, shows a `python` process in that session's runtime container with `connect` records to the local endpoint within a second of each tunnel's start, returned as `join: time_correlated`, and no other process connected in those windows. `agent.activity.tool` shows that the command ran after a fetch of a web page whose text instructed it. This suggests prompt injection rather than tenant-directed abuse, and it is a correlation rather than a proof.
4. The session is still running, so Dave quarantines the pod with `sessionDisposition: fail`, sets a security hold on the session with `evidence`, `audit`, `artifacts`, and `transcripts` and an approved `basis`, and captures memory with `runsc checkpoint --leave-running`.
5. Without activity coverage (a Kata pool, for example), step 3 has only `agent.activity.tool` and the network streams, and `activity-coverage` says so.
6. Dave answers the reporter, and contacts `acme` through the operator's security contact, recording the disclosure on the case. The tenant admin can then read its own streams.

The source port would have narrowed step 2 to one tunnel. On a large shared address with no port and no NAT log, the query returns every tunnel to that target in the window, which is the limit RFC 7422 describes.

### A tenant reports a leaked secret

Bob, a tenant admin at `globex`, reports that the API key from the credential binding `globex-crm` appeared in a public paste. The binding uses `inject` delivery, so the agent should never have held the value.

1. Bob reads `credential.mint` for the binding through the tenant read. It lists each pod and session that received secret version 7, and all of them are `globex` pods.
2. Bob rotates the secret, which the 15-minute cache lifetime bounds in pods, and he checks the host's logs, using the token hash where the provider exposes it. For a stored secret the provider's own records are the sole evidence of use, which is the limit CircleCI described: "there is no way for us to know if your secrets were used".
3. Bob's own reads show `egress.local` scrub hits, and `egress.decision` records for an unexpected host, in one session. He files a report.
4. Carol, on the operator's team, opens a case naming `globex`. She runs R-MINT-WRONG-TENANT over the range to test for a cross-tenant breach, and it finds no record of the binding minted to another tenant's pod.
5. Bob supplies the key's value through the case's encrypted channel. A fingerprint test over `agent.activity.exec.args` and `agent.activity.tool` in the range finds a match in one session, in a `curl` command line, as the session's tool report also shows. So the value reached the agent's context through a path outside `inject`, which is a different finding from a platform leak.
6. Carol sets a security hold for the session and the user, with `basis: customer_instruction` and the reference Bob's written request gives, and reports the result to Bob. Where the fingerprint search finds nothing, the investigation has no conclusion on that point, because redaction patterns, coverage gaps, and pods with `tool_only` coverage leave room for a miss.

### A compromised `netagent`

`ActivityCoverageLost` is silent, but R-TUNNEL-AFTER-END fires in a tenant-hour for a pod in `acme`: a tunnel remains open 40 minutes after its session ended, and `netagent`'s `egress.local` reported the close. R-LOCAL-CLAIM confirms it.

1. Dave treats `netagent`'s records as unreliable, because `self_reported` records are evidence only while the pod is uncompromised. The access log, `credential.mint`, and `pod.address` remain, because ingest set their producer from verified identity.
2. He quarantines the pod. The Token Service and the mirror refuse at once, each from its own `pkg/binding` read, and the decision service refuses new CONNECTs. A compromised `netagent` does not close its tunnels, so the tunnel survives. The pool's cap is 2 hours, so he reads `egress.accesslog` for the replica, and he runs the graceful drain on that replica after the checkpoint, accepting that other tenants' tunnels on it reconnect.
3. He checkpoints the `netagent` container. A native sidecar restarts when it exits, which would lose its memory, so this comes first. The archive holds the upstream tokens in that memory (P2), and it is encrypted to the case key.
4. `credential.mint` lists every token minted for the pod with its hash or `jti`, and Dave revokes or waits out those tokens, and rotates any stored secret version listed.
5. A compromised `netagent` could not write another tenant's records, because the decision service overwrites identity, and it could request credentials only within the pod's binding. R-MINT-NO-SCOPE and R-ATTRIBUTION run over the case range to confirm this.
6. A compromised `netagent` that sits on a quarantined pod could still attempt raw connections, so `agent.activity.exec` `connect` records and the NetworkPolicy's drops are checked for the attempt. Because `netagent` runs in the pod's Sentry, its own `exec` and `connect` trace points are `observed` too.

### A tenant erasure request during an open investigation

Carol, a tenant admin at `acme`, submits `POST /v1/admin/users/alice/erase` while Dave's case has a security hold covering Alice's sessions.

1. Step 0 finds no §12.8 legal hold, so the job starts. The security hold sits outside that ledger, and the response is the normal one.
2. The job drains Alice's sessions and deletes the originals in the tenant's stores. A quarantined pod remains, and the release step skips it.
3. The hold's `basis` is `legal_claims`, approved by the operator's counsel, so its escrow copies and held evidence manifests stay, its per-session keys are skipped, and the job joins the pending erasures. Keys for her other sessions are destroyed, after any configured delay, so those records become unreadable while their digests still verify. Had the basis been outside `deferErasureBases`, the copies and keys would go too, and Dave would see `forensics.evidence_lost`.
4. The tenant-readable events have the normal form, under decision 7's default, and the case records the retention.
5. When Dave releases the hold, the worker deletes the copies and destroys the keys, unless another hold covers them, writes `gdpr.erasure_deferred_completed`, and terminates the quarantined pod, removing its volumes.

If `acme` asks for tenant deletion instead, Phase 3.5 waits for the hold to finish materializing, escrows held evidence and rewraps its keys, and continues, so Phase 4a can destroy the tenant key within the T4 deadline. A quarantined pod keeps tenant data on its node's disk past that deadline, which the case states, and which the operator's agreement has to allow.

## Review of revision 7

Round 4 of the reviews raised m7: git on an uncovered host produces no `git.operation`. It holds, and R-GIT-CLAIM states it. None was rejected.

## Review of revision 6

Round 3 of the reviews raised the revert case on `user-credential-delivery-design.md`: a record replayed after an incident restores old tokens. It holds, and step 7 now states it. None was rejected.

## Review of revision 5

Round 2 of the reviews raised N29 on this document: step 7 said to revoke a child through its link provider, against this design's own limit. It holds, and step 7 now removes the link for `oauth2Link` and acts on exposure rows for `registered`. None was rejected.

## Review of revision 4

Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Known values reaching FE-6's redaction | Holds. FE-6 redacts in the collector and adapter, and that design gave them no such value. | They do not reach it. `leakScan` stays in the gateway. |
| "Revoking child tokens by hash" | Holds. Revocation needs the token value. | The case removes the link. |
| A held pod's file, stated two ways | Holds | Two cases, stated. |
| Exposure events are best effort | Holds | Durable rows, in that design. |

None was rejected.

## Residual risks

- **Lost before quarantine.** Memory and tmpfs state from before the first alert are gone unless the pod is still running, and the default pod lifetime is one session (§6.1). Many investigations start from records alone. A pool can extend `retentionClass` evidence, and nothing extends a pod's life in advance.
- **A person's token in a held pod.** A quarantined pod under `delivery: file`, whose `netagent` did not remove the file, holds a token valid at its provider until someone revokes it there, and Lenny cannot revoke a `registered` one. A copy an agent made is out of reach.
- **Tokens in evidence.** Redaction misses a token with no recognizable form, so stored evidence can hold one for its retention, under its T3 or T4 class.
- **Eviction and node loss.** The kubelet's eviction ignores PodDisruptionBudgets and grace periods, and a hard threshold terminates immediately, so a quarantined pod can be lost. An unfrozen agent can trigger this itself by filling the workspace past its `sizeLimit`, so on embedded pods the volumes last only until the agent chooses otherwise or the node-side freeze lands. A quarantined pod needs capturing at once, and the loss is recorded.
- **A compromised runtime destroys its own state.** Nothing stops a process from exiting or from overwriting its workspace before the quarantine lands, or on an embedded pod before a node-side freeze. The freeze is best effort in the pod. A compromised `netagent` restarts on exit, which loses its memory. `keepalive` protects volumes against container exit, and it does not protect them against eviction or a compromised guest or Sentry.
- **Open tunnels.** Quarantine does not end an open tunnel through a compromised `netagent` until the pool's cap or a replica drain, because Envoy has no per-tunnel kill. A drain interrupts other tenants. Exfiltration can continue in that window, and `egress.accesslog` records it.
- **Kernel or Sentry compromise.** An attacker who controls the Sentry can stop trace points and forge them, and an escape reaches the collector and any forensic Job on that node. A Kata pool has no `observed` activity evidence in the first version.
- **Coverage gaps.** Drops, a disconnected sink, redaction misses, and a runtime that reports nothing all leave holes. `coverage` and gap records state what was missed, as far as they can tell, and absence of a record is never proof.
- **Agent evasion by interpretation.** An agent can read a file through a long-running interpreter with no new `exec`, and a `file` aggregate shows a count per directory rather than each file. A symlink or a raw relative path can evade the watchlist, because `Open` carries a raw `pathname`, and a bind mount or hard link can evade a path filter on a host sensor.
- **Process-to-tunnel attribution.** On gVisor the join is a time-based correlation, so a busy pod or a reused connection leaves the answer ambiguous.
- **Erasure on a shared pod.** An erased user's records on a concurrent-slot pod stay encrypted until every user on that pod is erased or retention ends. Counsel should confirm that retention is acceptable on those pools.
- **Evidence before a hold.** Under a floor of `none` and a key delay of 0, a tenant can stop collection or erase a user before any hold exists, and that evidence is gone. A deployment choosing those defaults accepts this loss.
- **Privacy of the evidence itself.** Activity streams record what developers and agents did, including paths and commands that identify people, and redaction is imperfect. A held key survives an erasure request. Captures contain all process memory. Counsel should review the operator's basis, and the customer agreement should state collection, retention, and operator access.
- **Traffic outside Envoy.** An `internet` pool's raw sockets and every connection at level 1 have no Envoy record. Without activity coverage or an imported proxy or flow log, `attribute` can only list the pods it cannot speak for.
- **Tipping off.** FP3 hides holds from tenant-readable surfaces. An embedded adapter is the agent, so it sees `CH-PRESERVE` up to 5 seconds before the network cut. A pattern of sessions failing with a generic reason, and a slower first fetch after a store hold, remain observable, and a tenant could infer an investigation from them.
- **Platform trust.** A database superuser, a cluster admin, a compromised `lenny-evidence`, and a platform admin who grants itself a role are all inside the boundary. The audit chain, pgaudit, and the SIEM copy detect some of this after the fact, as §11.7 states. A snapshot volume holds other tenants' stores until extraction.
- **Compromised mirror parent.** It can forge `enforced` `git.operation` records (the limit the egress design states), and it can ignore the held flag. The snapshot is the check, taken up to 10 seconds later.
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

- §4.4.3 and §4.4.4: the stuck-checkpoint exit that replaces the liveness probe, and the statement that the size-limit eviction reaches a quarantined pod.
- §4.7.10 and §28.5.3: the `keepalive` container, the `freeze` request on `CH-SUPERVISE`, and the preservation state.
- §4.6.1, §4.6.3, and §6.2: the `quarantined` binding state and phase, the projection and drain edges, orphan GC exclusion, the label check in `lenny-sandboxclaim-guard`, the forensics reconciler's PDB and VolumeSnapshot grants, and RBAC.
- §4.7, §28.3, and §28.5: `CH-PRESERVE`, `CH-ACTIVITYREPORT`, and the `activity` frame on `CH-MSGSOCK`, with register rows. These identifiers are proposals under the naming law.
- §4.7.11: the adapter's validation of `activity` frames.
- §5.2, §5.3, §17.2, and §17.5: the `activityMonitoring` pool field, the monitored RuntimeClass name, and the collector and `lenny-forensics` namespace rules, with admission inventory and preflight checks.
- §7.3 and §10.1: `orphan_pod_terminated` as the surfaced reason, and the `resume` disposition.
- §10.1: the preservation state beside hold state, and `CH-PRESERVE` accepted during hold.
- §10.2: the `platform-forensics` role and permission rows.
- §11.2.1: the `evidence.usage` billing event and `evidenceQuotaBytes`.
- §11.7: the `forensics.*` events under CMP-058, the anchor retention rule, and a correction of the "6004 File System Activity" class name (OCSF v1.1.0 lists File System Activity as class 1001, and Datastore Activity as 6005), filed with the paragraph's other wrong class names as F-11.7.27.
- §11.8: quarantine, security holds, the reconciler, and the capture runbook as first-responder primitives.
- §12.2 and §12.6: `pod_quarantines`, `security_holds`, cases, `evidence_session_keys`, and `EvidenceStore`'s escrow operation.
- §12.8: removal of `audit_range` and `workspace_snapshot` from the ledger in step 0(c) and Phase 3.5(c), a separate security-hold step in teardown, `EnsureCopied` on every deletion path, hold basis and deferred erasure, key destruction, mirror purge, the retention class, and the post-restore gate.
- §12.9: rows for activity streams, captures, and retained evidence.
- §13.1 and §13.2: the `keepalive` container identity, selector exclusions on every agent policy, the collector and reconciler rows, and the deletion rule in `lenny-label-immutability`.
- §15.1: the new admin endpoints, error codes (`QUARANTINE_NO_POD` and `SECURITY_HOLD_REGION_UNRESOLVABLE`), the narrowed legal-hold error text,, and the correction of the `POST /v1/admin/legal-hold` body, which the specification gives as `resourceType` and `resourceId` while the code takes `tenantId`, `sessionId`, and `artifactId`.
- §15.4 and §15.7: the `activity` report in the adapter specification and the SDKs.
- §16.1, §16.4, §16.5, and §16.7: new metrics, the partition GC guard, the alerts (`QuarantineAged`, `HoldReviewDue`, `EvidenceDisagreement`, `ReconcilerStale`, and `ActivityCoverageLost`), and the event catalog.
- §17.7 and §25.7: `forensic-capture` and `abuse-report` runbooks.
- §17.8: collector and reconciler sizing, and the updated manifest bound.
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
| FE-1 | FE-0, EG-2 | The case record, and the `quarantined` binding state |
| FE-2 | FE-0, FE-3, EG-5 | The case record, the `security_holds` row that purge reads, and the held flag |
| FE-3 | FE-0, EG-10 | The case record, the held marker, and `EvidenceStore` |
| FE-4 | EG-2, EG-3, EG-5, EG-10, FE-1 | The streams it reconciles, and the quarantine rule |
| FE-5 | FE-0, FE-1, FE-2, FE-3, FE-4, EG-8 | The procedures it documents, the `pod.address` and `egress.accesslog` checks in `attribute`, and the cross-tenant query |
| FE-6a | EG-9, EG-10, FE-3 for its hold behaviour | The named permissions, the envelope, ingest, the key field, and the marker. It needs FE-3 only for the hold rule, and a release before FE-3 ships without that rule. |
| FE-6b | FE-6a, EG-11 | The streams and keys, and the PKI |
| FE-4's `R-ACTIVITY` rule | FE-6b | It lands with FE-6b, so FE-4 does not wait. |

The egress design's table lists FE-1 to FE-6 with their EG dependencies, and these add to it without conflict. No shared queue file exists, and proposals from different designs interleave as long as these hold.

| Other design | Relationship |
| :-- | :-- |
| `security-oversight-design.md` SO-1 | SO-1's `security-auditor` holds an activity read permission only through `auditorStreamGrants`. Neither waits for the other, because `tenant-admin` holds them throughout. |
| SO-3 | A per-tenant SIEM receives activity streams through its export. FE-6a states what crosses it, and SO-3 states the destination. |
| SO-4 | SO-4 must exclude the `forensics.*` prefix from what it mirrors to a tenant, unless a case discloses it. If SO-4 lands first, FE-0 adds the exclusion. |
| SO-7 | FE-5 adds no right to `lenny-ops`, so it does not conflict with SO-7's narrowing. |
| `tenant-scoping-identity-design.md` TS-1 | Holds and streams use the tenant, and an environment filter can be added to reads later. No dependency. |
| `user-credential-delivery-design.md` | Its `quarantined` handling needs nothing here. FE-6 takes nothing from `leakScan`. |
| `user-git-identity-design.md` | `link_ref` and child tokens follow "Delivered user tokens". No dependency in either direction. |

## Changes requested of egress-and-git-design.md

Revisions 38 to 40 of that document applied every request this design made: `egress.accesslog`, `netagent`'s local client port, `sequenceEnd`, the pod-incarnation key in `payloadKeyRef`, the per-node producer identity with `spec.nodeName`, the marker in `EvidenceStore.DeleteByTenant` with its escrow operation, the persisted fetch UIDs, store generations, the `quarantined` inputs and precedence, a quarantined pod's records under the platform prefix, a refresh against the current generation, and the manifest bound. This design consumes them as fixed inputs, and it requests nothing further. Revisions 44 to 51 of that document add the `userCredential` kind, which reaches this design through `quarantined` with no change.

The local client port in `egress.local` gives no exact join on gVisor, because `Connect` has no local port (see FE-6). This design keeps the field for a sensor that can supply the port.

## Documentation

Each proposal carries the changes for its own surface, and spec changes land first, as the egress design states. Pages describe behaviour with no section numbers, under `doc-content.md`.

| Location | Change | Proposal |
| :-- | :-- | :-- |
| New operator guide page "Security investigations" | Owns cases, quarantine, security holds and how they differ from legal holds, mirror holds, the reconciler's findings, and custody. It states what the tenant can and cannot see, and what the operator must disclose in its agreement. | FE-0 to FE-4 |
| New operator guide page "Agent activity evidence" | Owns the streams, coverage by isolation profile and pod model, the `gvisor-monitored` setup, redaction and its limits, keys, cost and the sizing formula, the producer contract, and how to read `observed` against `self_reported`. | FE-6a, FE-6b |
| Operator guide "Evidence and visibility" (a page in the egress design's chapter) | Links to the two pages above, and gains `egress.accesslog` and `pod.address`. | FE-4 |
| `docs/runbooks/forensic-capture.md`, `abuse-report.md`, and `leaked-secret.md` | New runbooks in the §25.7 format, with the capture table per profile. | FE-5 |
| `docs/runbooks/legal-hold-quota-pressure.md` | States that security holds do not count here. | FE-3 |
| Operator guide `security.md` and `audit-ocsf.md` | `security.md` summarizes incident primitives and links out. `audit-ocsf.md` gains the new classes and corrects the File System Activity class. | FE-0, FE-6a |
| Operator guide `namespace-and-isolation.md` | States which evidence each isolation profile yields, because that now informs the choice of profile, and documents `activityMonitoring`. | FE-6b |
| Operator guide `lenny-ctl.md` and `docs/reference` | The new commands, endpoints, errors, metrics, and alerts. | Each |
| Runtime author guide `platform-tools.md` | States that tool calls through the platform and connectors are recorded. | FE-6a |
| Runtime author guide `runtime-sdk.md` and `integration-levels.md` | The `activity` report and `reportActivity`, what to report for a shell tool, the redaction applied, and that reporting is optional and self-reported. | FE-6a |
| Runtime author guide `runtime-configuration.md` | States that monitored pools record the runtime's processes and files, and that a process-heavy runtime pays the file stream's cost. | FE-6b |
| Runtime author guide `lifecycle.md` | States that a runtime can stop receiving messages and be stopped with SIGSTOP at any time, with no notice. | FE-1 |

## Tests

These describe behaviour to pin. None exists yet. Every test carries its `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`.

### Tier 0: static

- Every rendered NetworkPolicy in an agent namespace other than `default-deny-all` carries the quarantine exclusion, in every chart configuration that renders one, including `internet`, `kube-dns`, `llm-proxy`, and `otlp`. A new policy without it fails.
- The `lenny-forensics-capture` ClusterRole holds no `nodes/proxy` and is bound by nothing the chart renders.
- The collector's pod template passes `pkg/podsecurity` and holds no capability.
- `schemas/` and the OCSF mapping cover each new event type and stream.

### Tier 1: unit

- **Projection.** Each row of the retirement table, with and without the label, computes the stated phase.
- **Binding.** The label alone, the row alone, both, and neither yield the stated state, and quarantine takes precedence over every other state.
- **Holds.** Marker recomputation on release covers overlapping holds, an open-ended range, and a release while another hold still covers a row.
- **Redaction.** Each structural pattern, entropy threshold, truncation boundary, and credential path class, with values from a corpus of known secret forms and a corpus of harmless arguments.
- **Fingerprints.** A value matches its own fingerprint under the pod-incarnation key, and not under another key. A collector with no issued key writes the value as unfingerprinted redaction.
- **Attribution.** A process that changes directory selects no session or key on a concurrent-slot pod, and `workdirHint` feeds nothing else.
- **Floor.** A tenant write below the floor is refused, and an erasure with a delay can be stopped by a hold set inside it.
- **Hold basis.** A hold without both basis fields is refused, and each basis defers erasure or does not, per the configured list.
- **Aggregation.** Windows, counts, watchlist exact records, and collapse above the rate limit, including the empty window and the boundary at the limit.
- **Reconciler rules.** Each rule on matching inputs, on a disagreement, on a lossy input, and on a suspect tag that must not attribute.
- **Coverage.** A timeline with a start, a loss, and no producer returns the stated intervals.

### Tier 2: component

- The WarmPoolController projects `quarantined` from the label, excludes the pod from warm counts, creates the PDB, and releases to `draining` only after the row's release.
- `lenny-label-immutability` refuses a deletion and a label removal from every caller except the controller after release, and it admits a break-glass group's stamp.
- `lenny-sandboxclaim-guard` refuses a claim for a labelled pod, including a race with a stamp.
- The forensics reconciler creates a `VolumeSnapshot`, reports an absent class, and retries an incomplete step after a restart.

### Tier 3: contract

- `CH-PRESERVE`, `CH-ACTIVITYREPORT`, and the `activity` frame against their schemas, including oversized frames and an unknown session.
- The gVisor remote sink's wire format against the collector, across each `runsc` version the chart allows.
- The capture registration's presigned `PUT` refuses another key, another method, and a different length.
- Each new stream's envelope, including a chunk and a pod-incarnation key.
- The producer contract with an allowlisted identity, an unknown identity, and a record naming a pod on another node.

### Tier 4: integration

- **Holds on the compose stack.** A hold's audit segment verifies as a chain after the live rows are deleted, for a tenant scope and for a session scope, each copying every tenant row in range. A held artifact survives rotation and deletion of its original, and the tenant's quota and legal-hold list are unchanged.
- **Attribution coverage.** On an `internet` pool with no tunnel record, `attribute` returns no candidate from Envoy, lists the pod under `coverage`, and finds it through a `connect` record by destination. At level 1 it lists every pod. An empty result always carries `coverage`.
- **Basis across paths.** A `security_incident_response` hold loses its copies, keys, and store on `DeleteByUser` and at Phase 3.5 alike, and a `legal_claims` hold keeps all three on both, with the configured default list.
- **Records after a quarantine.** They stay out of tenant reads, remain for the set period after release, and a key destroyed by erasure makes activity payloads unreadable.
- **Open-ended holds.** Audit rows, artifacts, checkpoints, and transcripts created after the hold are copied, the partition GC waits for rows above the cursor and drops afterwards, every deletion path copies first, and a copy failure refuses the deletion. Phase 3.5 flushes before Phase 4.
- **Ledger.** A security hold produces no `ERASURE_BLOCKED_BY_LEGAL_HOLD` or `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`.
- **Separate records.** `POST /v1/admin/legal-hold` with `hold: false` on a session under a security hold deletes no security copy and returns no `escrowReleased` count, on a live session and on a tombstoned tenant.
- **Erasure.** `DeleteByUser` during a hold returns the normal response, deletes the originals, skips held keys, and completes at release, with the stated receipt fields, and a user with no hold follows the existing path.
- **Teardown.** Phase 3.5 escrows held evidence and rewraps its keys, Phase 4a destroys the tenant key, digests and anchors verify afterwards, and decryption works under the escrow KEK. A region with no escrow entry fails closed.
- **Retention class.** Marked streams survive teardown for the set period and are deleted afterwards, and unmarked ones are removed.
- **Keys.** Key destruction makes a payload unreadable while its digest verifies, and a retention expiry destroys the key after the last batch.
- **Sessions.** `sessionDisposition` `fail` and `resume`, on a single-session, a concurrent-slot, a service-mode, and a warm pod, and a delegation tree. The `fail` session shows `orphan_pod_terminated` and does not retry.
- **Order.** With a test adapter, the row and binding push precede `CH-PRESERVE`, `CH-PRESERVE` precedes the label, and an unacknowledged RPC continues at the timeout.
- **Restore.** A restored database replays no erasure that a hold deferred.

### Tier 5: end to end on Kind

- A quarantined pod keeps its volumes through a force-terminate, a tenant suspension, a recycle limit, an uptime limit, a pool deletion, a tenant deletion's Phase 5, a coordinator hold timeout, and a `kubectl delete`, and each is refused or skipped as stated.
- With `keepalive`, exiting the adapter and the runtime leaves `/workspace` and `/sessions` readable on the node. Without it, the same exits remove them, which pins the kubelet inference. A pod with no quarantine retires as before when its adapter exits.
- In-pod freeze stops every process in the runtime container on a sidecar pod, and `CH-SUPERVISE` keeps working.
- No agent pod renders a liveness probe, an embedded adapter with `checkpointStuck` set exits non-zero and the pod retires, and a node-side freeze of an embedded container does not lead the kubelet to kill it.
- A quarantined pod's workspace filled past its `sizeLimit` is evicted, and the reconciler records `forensics.evidence_lost`, which pins the residual.
- `kubectl drain` waits on the PDB, and the release lets it complete.
- The agent policies apply after the label on Calico or Cilium, because kindnet is not evidence of production enforcement, and new connections to the gateway, object store, and mirror fail.
- A mirror store hold stops re-chown, repack, purge, and fetches on the sealed generation, and a held public store's files stay unwritable by a later fetch. A fresh generation serves fetches and read-after-push at once, and refs stay current.
- The kubelet checkpoint path under `standard`, with CRI-O or containerd with CRIU, produces an archive, and a runtime without it returns the documented error, which the runbook handles.
- The Tetragon reference forwarder writes `agent.activity.exec` for a `standard` pod.

### Tier 6: end to end on cloud

- On gVisor nodes: the collector receives `exec` and `file` records with correct attribution on sidecar and embedded pods, `ignore_setup_error: false` fails a sandbox with no collector, a collector restart produces `coverage: lost`, `activityMonitoring` selects the monitored class, and an unmonitored pool on the same node produces nothing. The join reports `time_correlated` on `Connect` records.
- A host eBPF sensor on the same node records no process inside the gVisor and Kata pods, which pins the coverage table.
- `runsc pause` and `runsc checkpoint --leave-running` through the published image, on an unfrozen and a frozen pod, produce a registered archive. The Kata capture method, once chosen, gets the same test.
- GKE Sandbox as a managed configuration of the monitored class, where it is available.

### Tier 7: load

- `exec` at the rate limit on many pods, with the collector and ingest at the assumed Tier 3 average and at a peak multiple, and the measured effect on a build workload against an unmonitored pool, with and without `openat` traced.
- A flood from one tenant's pod produces gaps for that tenant alone.
- Concurrent holds, retention sweeps, and compaction lose no marked row.
- The reconciler completes a tenant-hour within its schedule at the sized evidence volume.

### Tier 8: chaos

- Quarantine during a Postgres outage (break glass), during an API server outage (store re-read), and with a lost binding push (the 30-second bound).
- A node eviction of a quarantined pod records `forensics.evidence_lost`.
- A crash of the gateway between each step of the quarantine order leaves the reconciler to finish it, with no pod retired. An unreachable adapter at `CH-PRESERVE` produces the stated status, and the volumes survive the hold timeout.
- `lenny-evidence` loss during materialization, during activity ingest, and during a sweep leaves nothing deleted.

### Tier 9: security

- A tenant admin's every read (audit query, legal-hold list and clear, quota, retention, error codes, session fields, every stream read and export including sequence continuity, and the erasure and teardown receipts) shows no sign of a hold, quarantine, or case, which pins FP3.
- A process at the agent UID cannot signal `keepalive`. Raw egress on an `internet` pool stops for new connections after the label.
- A compromised `netagent` stand-in leaves its tunnels open under quarantine, and R-TUNNEL-AFTER-END fires. Its forged reports for another session are rejected.
- A compromised decision service stand-in drops records and forges tags, and R-TUNNEL-NO-DECISION and R-ATTRIBUTION fire, and the Token Service refuses a quarantined pod regardless.
- A forged collector record for a pod on another node is rejected.
- An agent UID process cannot read the collector's socket, or reach the capture Job's image, and no secret from a seeded corpus reaches a stored record in plaintext.
- A capture cannot be read without `forensics.capture.read`, and an archive is encrypted when it leaves the node.
- A retired fetch UID under a hold cannot write a held public store.

### Tier 10: conformance

- A runtime that sends `activity` frames, one that sends none, and a misbehaving one that sends oversized or wrong-session frames each conform, and the adapter's behaviour matches the card.
- Quarantine leaves each reference runtime with no message and no `session_end`.

### Tier 11: documentation

- Each new alert resolves to a runbook, and the runbooks pass the §25.7 front-matter checks.
- The documented coverage table matches the tier 6 results, and each documented example command parses.

## Decisions

Each item in the table is open, and nothing in it has been adopted. Decision numbers are referenced above. Decisions 14, 20, and 21 are settled by this revision, subject to the owner's review:

- **Decision 14: network cut on quarantine.** Every agent allow policy excludes the label, including `allow-gateway-ingress`, and the label follows the acknowledged `CH-PRESERVE`. The cost is that a pod whose adapter did not acknowledge can lose `CH-MSGSOCK` at 120 seconds.
- **Decision 20: selecting monitored pods.** The `activityMonitoring` pool field and `runtimeClasses.monitored.name`, because one profile maps to one RuntimeClass. A new isolation profile would touch each profile-keyed rule with no change in isolation. A node-wide setting would charge unmonitored pools and fail every gVisor pod on a collector outage.
- **Decision 21: the `file` stream.** Off per pool until the spike reports the Sentry's cost, so early adopters have `exec` and tool evidence alone.

| # | Decision | Recommendation | Cost of the recommendation | Main alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Producer for agent activity | **Settled**, in the owner's words "go with your recommendations". Two layers: tool activity on every profile, and gVisor runtime monitoring for `observed` process and file evidence. | A new DaemonSet, node configuration, and a dependency on gVisor's trace points, whose compatibility and reconnection behaviour need a spike. | SDK only: no node component, and everything is `self_reported`. A host eBPF sensor sees nothing inside the default profile. |
| 2 | Kata coverage in the first version | **Settled**, in the owner's words "go with your recommendations". Supervisor tracing is the first follow-up. `tool_only`, stated in the API. | The highest-risk profile lacks process evidence, and `microvm` is what §5.3 recommends for that risk. | An in-guest sensor: a custom guest kernel and agent, as Ant Group built, with records that remain `self_reported`. |
| 3 | `standard` coverage | **Settled**, in the owner's words "go with your recommendations". A public producer contract and a tested Tetragon forwarder, with no sensor in the chart. | A deployer installs a privileged DaemonSet itself. | A chart option, which adds the privileged component the egress design avoids, for a profile §5.3 reserves for development. |
| 4 | Default state | **Settled.** Off at the deployment level, with a separate setting for tool and connector activity, for processes and commands, and for file access, in the owner's words "ok (separate config values for tool and connector activity, processes/commands, and file access)". A SaaS operator turns each on, and tenants choose within it. | No evidence exists for a tenant until someone chooses it, and an incident before that has none. | On by default at `tool` level, which is cheap and needs a customer agreement to state it. |
| 5 | Who reads activity | Settled by the owner with SO decision 19: `tool`, `exec`, and `file` to `tenant-admin`, with `security-auditor` only through `auditorStreamGrants`, `args` only on grant, and the operator only under an open case. The `security-auditor` default is withdrawn. | A subject tenant admin can read its own streams, a customer's security team takes an explicit step and a delay, and the case requirement slows the operator. | Operator-only: simpler, and it removes the customer's evidence, which `security-oversight-design.md` exists to provide. |
| 6 | Does the tenant learn of a quarantine or hold | No signal by default, a generic existing failure reason, and disclosure as a recorded case action. | A tenant sees a failure whose reason does not state the cause, and an operator can misuse silence. SO-4 must carve it out. | Immediate notice or a distinct reason: simple and transparent, and it warns a malicious tenant. |
| 7 | Erasure during a hold | Proceed, defer only under an approved basis, and keep the tenant-readable receipt neutral by default, with the customer agreement stating that retention can occur. | A neutral receipt may be inaccurate as a statement to a controller, and that risk is for counsel, since §11.8 calls the operator a processor. | A receipt stating a retention, which hints at an investigation. Blocking erasure, which reveals the hold and delays the tenant's obligation. |
| 8 | Retention class default | 0 days in the chart, and a SaaS operator sets a period in its agreement, 90 days being a starting value. | Without it, offboarding removes evidence. With it, data outlives the tenant, on a basis counsel must assess. | A non-zero chart default, which makes the promise for every deployer. |
| 9 | Who may set a quarantine | `platform-forensics` and `platform-admin`. Tenants ask through the operator. | A customer's team cannot freeze its own pod quickly. | A tenant request permission limited to an exclusive pod, once the audit and cost rules are proven. |
| 10 | Default session disposition | `fail`. | A false positive ends a customer's session. | `resume`, which keeps work going and restores possibly tainted state. |
| 11 | Quarantine lifetime | No automatic release, with an alert at 24 hours. | A forgotten quarantine holds a node from upgrading. Cluster upgrades can stall. | An automatic release at a deadline, which destroys evidence on a timer. |
| 12 | Release control | Two-person release and capture reads on by default for regulated tenants, and off elsewhere. | A small team needs a second person at release. | One person, with the audit event as the control. |
| 13 | Capture automation | No privileged Lenny controller. An unbound role and a published image, used through the deployer's privileged access. | Capture is slower and manual, and needs a practised operator. | An API-driven capture controller holding `nodes/checkpoint`, and privileged Job creation, at the cost of a standing high-value target. |
| 15 | Argument detail default | `redacted`. | Secrets with no recognizable form are stored, encrypted. | `metadata`, which loses the arguments an exfiltration investigation needs. |
| 16 | Who pays | The operator pays for quarantined pods and held bytes. Tenants are metered for activity bytes. | Operators carry investigation costs. | Charging the tenant, which reveals the hold. |
| 17 | Source independent of the decision service | Envoy's second logger to `lenny-evidence`, with the deployer's log pipeline and flow logs as an optional outside source. | A second stream of tunnel records, and a compromised `lenny-evidence` remains a common point. | A log shipper in `lenny-egress` with its own bucket credential, which is stronger against `lenny-evidence` and breaks the rule that no producer holds a store credential. |
| 18 | How a hold protects artifacts | Copy on hold to escrow. | Copy time and storage at set time. Later writes to a session need a named range. | An in-place flag, which is cheaper and changes rotation and quota in ways a tenant can see. |
| 19 | `onCoverageLoss` default | `continue`, with an alert and a gap record. | A dark pod keeps running. | `retire`, which turns a collector rollout into capacity loss. |
| 22 | Post-quarantine freeze | Automatic in-pod SIGSTOP on sidecar pods after the acknowledgement, with `freeze: false` to opt out, and a node-side freeze in the runbook. | It changes process state, and a frozen process may affect checkpoint methods, untested. Embedded pods wait for the node-side step. | No freeze: the agent keeps running and can destroy evidence, though it is observed live. |
| 23 | Activity floor and key delay | `forensics.activity.floor` and `keyDestructionDelay`, with chart defaults of `none` and 0, and a SaaS operator raising them in its agreement. | A floor reduces the tenant's control over collection, and a delay postpones erasure of a key, on a period counsel must accept. With the defaults, a tenant can erase evidence before a hold exists. | No floor, leaving this to the tenant. A non-zero chart default, which makes the promise for every deployer. |
| 24 | Hold basis | A required enumerated `basis` and `basisRef` per hold, with erasure deferred only for `legal_obligation` and `legal_claims` by default. | Each hold needs counsel's input, which slows the first minutes, so an investigator quarantines first and sets the hold once a basis exists. An incident-response basis alone loses material to erasure and to tenant deletion, and a store hold is treated the same way. | Treat security response as deferring by default, which asserts a legal conclusion in code. |
| 25 | `keepalive` container | A regular container in every agent pod, at a minimal request. | One more container per pod, including warm pods, a small density cost, a reserved UID, and an image to pin. | Rely on the sidecar adapter staying alive, which leaves embedded pods and an exited adapter losing the volumes. An ephemeral container at quarantine time, which is unverified and is subject to the cred-guard webhook. |
| 26 | Stuck checkpoint on embedded pods | The adapter exits on `checkpointStuck`, and agent pods carry no liveness probe, which matches the tree. | A deadlocked adapter whose watchdog also hangs goes undetected by the kubelet, until gateway keepalive and the orphan reconciler act. | Keep the probe, so a node-side freeze of an embedded pod kills the container, or route it through `keepalive`, which adds a proxy to a component meant to do nothing. |

## Validation owed

No validation has run. These come first, because the recommendation rests on them, and each is an input to a proposal.

1. **gVisor spike, before FE-6b.** On containerd and on GKE Sandbox: what `Execve` and `Open` deliver in practice, what a build workload costs with each enabled, what the sender drop count reports under flood, whether a sink can reattach to a running sandbox, whether a per-handler configuration selects monitored pods, and whether any trace point can supply a connection's local port.
2. **Host sensor confirmation.** Tetragon and Falco on the same nodes, against gVisor and Kata pods, to pin the coverage table's inference.
3. **Volume.** Measured records per session on representative workloads, replacing the assumed figures, and the compression that a chunked envelope gains.
4. **Capture.** The kubelet path on the deployment's containerd or CRI-O, `runsc checkpoint --leave-running` on a Lenny pod including its native sidecar, and a Kata hypervisor dump for each hypervisor a deployment uses.
5. **Quarantine on CNIs.** What happens to existing flows after the selector change on Calico and Cilium, and how managed node upgrades treat a PDB that refuses eviction indefinitely.
5a. **Volumes and freeze.** That emptyDir data is removed once only native sidecars remain, that `keepalive` prevents it, whether a running ephemeral container would, whether a frozen process can be checkpointed by each method, and how fast an agent reaches the size-limit eviction against the time a node-side freeze takes.
6. **Legal review.** Decisions 6, 7, 8, 23, and 24, including the operator's role against a tenant's instruction, and what a customer agreement must state.

## Sources

Vendor pages describe the vendor's own product. Read on 2026-11-02, and none was tested.

This document has no evidence folder, because no test ran for it. The egress design's stand-in measurement that "Volume and cost" compares against is under `evidence/egress-and-git-design/spike-evidence/`.

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
