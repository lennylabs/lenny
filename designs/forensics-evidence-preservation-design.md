# Forensics and evidence preservation

Status: design for owner review, revision 1, 2026-11-02. Nothing in this document is built, staged, or validated. No spike, test, or benchmark has run for it, and every number under "Volume and cost" is a planning assumption. Claims about Lenny were checked against the specification and the tree on 2026-11-02. Claims about external products come from the pages listed under "Sources" and were read rather than tested. Proposals carry the label FE-n.

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
- FP7. A secret that appears in a command line never reaches storage in plaintext, and `DeleteByUser` can make stored activity unreadable by destroying a key, unless a security hold covers that key.
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

**Case.** A case is a row in the regional platform Postgres with an identifier, a title, the tenants it names, the opening principal, a justification, a state (`open` or `closed`), and an optional public key for capture encryption. A case spanning regions has one row per region under one identifier, because §11.7 **Platform-tenant audit event residency** already routes facts about a tenant to that tenant's region. Every operation below requires a `caseId`, so an investigator can enumerate what one investigation froze, and a closed case refuses new actions.

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
| `POST /v1/admin/quarantines` | Body: `caseId`, one of `podName` with `namespace` or `sessionId`, optional `includeDelegationTree`, `sessionDisposition` (`fail` by default, or `resume`), and `reason`. Returns `202` with the quarantine identifier, `affectedSessions`, and a per-step status. `dryRun` is refused, as on other action endpoints (§15.1). |
| `GET /v1/admin/quarantines` and `GET /v1/admin/quarantines/{id}` | Filters: `caseId`, `tenantId`, and `state`. Returns each step's status and the age of the quarantine. |
| `POST /v1/admin/quarantines/{id}/release` | Body: `justification` and `acknowledgeEvidenceLoss`, required when no capture is registered. Moves the pod to `draining`. |

`lenny-ctl admin forensics quarantine set|get|list|release` maps to these, in a new §24 subsection beside §24.11. A request naming a session whose pod is gone returns `409 QUARANTINE_NO_POD`, and the response points to a security hold. Each pod has at most one active quarantine, and a repeat request adds the new case to it.

### Data model

| Carrier | Fields | Authority |
| :-- | :-- | :-- |
| `pod_quarantines` row (session store, platform-scoped, because a lookup by pod UID has no tenant context) | Identifier, pod UID, name, namespace, node, pool, tenant, sessions, case, state (`applying`, `active`, `released`), step status, set and release principals and times | The record of the quarantine |
| Pod label `lenny.dev/quarantine=<id>` | Quarantine identifier | The enforcement signal, read from informers with no database call |
| `SandboxClaim.status.phase = quarantined` | New gateway-owned binding state (§4.6.3) | Excludes the claim from orphan GC and hold expiry |
| `Sandbox.status.phase = quarantined` | New phase, written by the WarmPoolController as a projection of the label (§6.2) | Excludes the pod from inventory and from every drain edge |
| Per-pod `PodDisruptionBudget` | `minAvailable: 1`, selecting the quarantine label | Refuses API-initiated eviction |

The label and the row are both read, so either outage leaves one path. `pkg/binding` treats a pod as quarantined when the informer shows the label or when its bounded re-read of the store finds an active row. The PDB uses an integer `minAvailable` because §4.6.1's warm-pod PDB rule states that a `maxUnavailable` budget cannot compute `expectedPods` on a `Sandbox`-owned pod.

### Enforcement points

| Control | Enforced by | Mechanism |
| :-- | :-- | :-- |
| New egress, mints, and mirror reads | Decision service, Token Service, and mirror | The `quarantined` state in `pkg/binding`, each instance reading independently (EG-2). A compromised decision service cannot release the Token Service gate. |
| Raw network paths | CNI | The chart's agent allow policies (`allow-gateway-ingress`, `allow-pod-egress-base`, `allow-pod-egress-objectstore`, and the `proxied` and `gitAccess` policies) gain `lenny.dev/quarantine DoesNotExist` in their pod selector, so the namespace's `default-deny-all` applies. Network policies are additive, so a deny policy cannot express this. |
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
| Adapter or runtime container exit, under **Liveness.** | Retire | The pod remains. `restartPolicy` is `Never` (`podspec/base_pod.go`), so the container stays exited, and disk and tmpfs volumes remain while the pod object exists. |
| Force-terminate, tenant suspension drain, user invalidation, and erasure drain | Session ends and the pod is released | Session ends, and the release step skips a labelled pod. |
| Tenant deletion Phase 5, and pool deletion | CRDs removed | The labelled Sandbox is skipped, and the deletion reports it to the forensics reconciler. |
| Node drain | preStop checkpoint, then eviction | The PDB refuses eviction, so the drain waits. |
| Node-pressure eviction | Kubelet evicts | No Lenny control applies, because the kubelet honours neither PodDisruptionBudgets nor the grace period. See "Residual risks". |
| Coordinator hold timeout (§10.1) | After `coordinatorHoldTimeoutSeconds` (120 s), the adapter ends sessions and closes the runtime's `CH-MSGSOCK` | The gateway sends a new RPC, proposed as `CH-PRESERVE`, which puts the adapter into a state that never ends the runtime and sends it nothing. Delivery is best effort, because an embedded adapter is the agent. |

### Sessions on the pod

`sessionDisposition: fail` writes `failed` with `FailureClassRuntime` and the reason `POD_WITHDRAWN`, as force-terminate does for `FORCE_TERMINATED`. §7.3 treats a reason on neither retry list as non-retryable, so no resume follows. Leases return through the §11.4 lease step, which stops proxy-mode LLM access at once. A direct-mode credential stays usable until it is revoked, which FE-5 covers.

`sessionDisposition: resume` uses the `pod_evicted` path, so the client sees a recovery on a replacement pod and no failure. The default is `fail`, because a resume restores the session from a checkpoint the compromised pod may have written, and it continues any injected instruction in the transcript. An investigator chooses `resume` when the suspect is the pod rather than the session.

The API returns every session on the pod. A concurrent-slot pod can hold several sessions of one tenant, because §5.2 forbids cross-tenant slot sharing, and all of them end. A service-mode pod has no session, so the pin stops routing, and the router sends the tenant to another pod. A warm pod with no claim is quarantined through the label alone. `includeDelegationTree` resolves the pods of the session's tree, and each pod gets its own quarantine under the same case.

### Order and failure behaviour

1. The handler commits the row and the custody event in one step. A failure returns `503`, and nothing has changed.
2. It stamps the label, sets the claim phase, pushes the binding change to the decision replicas, and sends the preservation RPC.
3. It ends the sessions.
4. The forensics reconciler creates the PDB and retries any incomplete step, and it reports per-step status on the row.

| Failure | Behaviour |
| :-- | :-- |
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
| `POST /v1/admin/mirror/stores/{id}/hold` | Body: `caseId`, `note`, and `stopServing` (default `false`). Returns the snapshot reference and the flag state. |
| `POST /v1/admin/mirror/stores/{id}/release` | Body: `justification`. Clears the flag and allows repack and purge. |

### Mechanism

- **Flag.** The gateway sets the held flag in the store index (EG-5) on the mirror's parent, authenticated by the gateway's identity. The flag stops the sweep's re-chown, repacks, and purge, as the hook states. It also stops new fetches for that store once the in-flight fetch has ended and its sweep has run. The re-chown is skipped so that on-disk owners keep saying which fetch UID wrote each file.
- **Fetch UIDs.** The egress design returns a fetch UID to the pool only after the sweep and the re-chown. A skipped re-chown leaves files owned by that UID, and a public store's directories are mode `2775`, so a later fetch reusing that UID could traverse and write them. The UIDs that own files in a held store therefore stay out of the pool until release, and the index persists that set so a restart honours it (see "Changes requested"). The range bounds concurrent fetches, so `gitMirror.heldUidBudget` caps how many UIDs held stores may retire, and a hold past the cap re-chowns after recording an ownership manifest.
- **Snapshot.** The forensics reconciler creates a CSI `VolumeSnapshot` of the replica's volume, after the flag is acknowledged or after 10 seconds, whichever comes first, using a snapshot class with `deletionPolicy: Retain`. The snapshot does not depend on the parent, so a compromised parent cannot withhold it. The controller gains `create` and `get` on `volumesnapshots` in the mirror's namespace, and no `delete`.
- **Extraction.** A volume holds several stores, including other tenants', so the snapshot is an interim artifact. The FE-5 procedure mounts a restored copy in a job, archives only the held store with numeric owners, modes, and ACLs, hashes each pack, and registers the archive as a capture. The snapshot is then deleted through the cluster admin's rights. A deployment without CSI snapshots skips this step, and the archive runs against the live store while the flag holds, which a compromised parent can affect. `gitMirror.forensics.volumeSnapshotClass` selects the behaviour.
- **Continued service.** With `stopServing: false`, the frozen store keeps answering `upload-pack`, because reads do not change it. A poisoned store needs `stopServing: true`, which retires that generation and has the mirror start a fresh generation from the upstream on the next request. A held public store therefore costs one refetch, and every tenant sees a slower first fetch and no failure. The on-disk layout has to allow two generations of one store identity (see "Changes requested").
- **Tenant view.** Held bytes are excluded from the per-tenant mirror storage quota (P7), and neither the quota read nor the prefetch API exposes the flag (FP3).
- **Tenant deletion.** The §12.8 mirror purge skips a store whose index or `security_holds` row says it is held, and it fails closed when it cannot read them. A store held at teardown keeps its files until release, and the erasure receipt records it under the rule in FE-3.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Mirror unreachable, or the replica lost | The row stays `pending`, the snapshot proceeds, and the reconciler retries the flag with an alert. Purge reads the row, so it cannot run in the gap. |
| Snapshot fails or no class is configured | The hold stands, `snapshot: unavailable` is reported, and the case shows it. |
| Compromised parent ignores or forges the flag | The snapshot and the fetch records (`git.operation`, `enforced` from the mirror) are the evidence. The limit is stated under "Residual risks". |
| Volume full while held | `gitMirror` capacity alerts fire, and a held store is never deleted to make room. |

## FE-3: Platform-only holds and a security retention class

### Security holds

A security hold is a separate object from the §12.8 `legal_hold` flag. The tenant-visible legal hold keeps its current behaviour for a tenant's own e-discovery.

| Method and path | Behaviour |
| :-- | :-- |
| `POST /v1/admin/security-holds` | Body: `caseId`, `tenantId`, `scope`, `include`, `note`, and `reviewAt`. Returns `202` with the identifier and `materializing`. |
| `GET /v1/admin/security-holds` | Filters: `caseId`, `tenantId`, and `state`. Requires `forensics.hold.write` or `forensics.query`. Returns item counts and materialization progress. |
| `POST /v1/admin/security-holds/{id}/release` | Body: `justification`. Runs deferred erasures (below). |

`scope.kind` is `session` (with `sessionIds`), `user` (with `userId`), `pod` (with `podUids`), or `tenant` (the tenant). Each takes `from` and an optional `to`. An absent `to` keeps holding new records until release. `include` lists `audit`, `evidence` (with an optional list of streams), `artifacts`, `checkpoints`, and `transcripts`. A `tenant` scope accepts `audit` and `evidence` only, because copying every artifact of a tenant is unbounded, so artifacts need named sessions or a user.

| `security_holds` field | Meaning |
| :-- | :-- |
| Identifier, case, tenant, region | Regional platform Postgres, following CMP-058 |
| Scope, include, and range | As above |
| State | `materializing`, `active`, `failed`, or `released` |
| Items | Per-class counts, escrow object keys, and digests |
| Set and release principals, note, and `reviewAt` | `HoldReviewDue` alerts at `reviewAt`, and nothing expires automatically |
| Pending erasures | Erasure jobs and users deferred by this hold |

### Copy on hold

The tenant's data is copied or marked so that nothing in the tenant's own stores changes. The alternative is an in-place flag like `legal_hold`, which alters checkpoint rotation, counts against `storageQuotaBytes`, and raises `CheckpointStorageHigh`, each of which a tenant can observe (decision 18).

| Class | Mechanism | Retention |
| :-- | :-- | :-- |
| `artifacts`, `checkpoints`, and `transcripts` | Copied at hold time to `legal-hold-escrow/{tenant}/security/{case}/...`, using the existing region-scoped `storage.regions.<region>.legalHoldEscrow` entry, the `legal_hold_escrow_kek`, and the `pkg/legalholdescrow` migrator and its `SourceReader`. The originals follow normal rotation and deletion. | Until release |
| `audit` | The rows in range are written as canonical tuples with their `prev_hash`, `sequence_number`, and `event_schema_version`, plus the tuple before the range, so the segment verifies against §11.7 item 3 with no live table. Anchor events for held evidence are always included. This gives `audit_range` a body in the migrator. | Until release |
| `evidence` | `lenny-evidence` sets the held marker on every manifest row in range, and on new rows while `to` is absent, and reports the rows to the hold. A release recomputes the marker from the remaining holds, since several holds can cover one row. | Until release, after which normal stream retention applies |

**Retention races.** The handler marks manifest rows and starts the copy before it returns, and the sweeper and compactor read the marker in the transaction that deletes or merges. The §16.4 partition GC gains a second guard: it holds an audit partition while a hold in `materializing` state overlaps it, and it emits `AuditPartitionDropBlocked` with `reason: security_hold`. `POST /v1/admin/audit-partitions/{partition}/drop` refuses that partition while the guard is active. Once the tuples are written the partition can drop, so a hold does not retain other tenants' rows past their retention.

**The §12.8 ledger.** `audit_range` in step 0 and Phase 3.5 now means an audit copy under a security hold, and `workspace_snapshot` remains as the existing checkpoint class under one. The tripwire test on the setter stays, because the tenant-visible setter keeps its two scopes. The spec text changes to name the new source, and `tenantHoldEnumerator` reads it.

**Tenant visibility.** `GET /v1/admin/legal-holds` excludes these holds for every caller, and `lenny_tenant_legal_hold_active_count` does not count them. Tenant reads of a stream apply the stream's normal retention window, so a held row past that window reads as expired. Held bytes are charged to the platform and excluded from `storageQuotaBytes`.

### Interaction with erasure and teardown

**`DeleteByUser`.** Step 0 does not block on a security hold, because a `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` lists holds to a tenant-admin caller. The job runs and deletes the originals in the tenant's stores. It leaves escrow copies and held evidence alone, and for a session under a hold it skips destroying the per-session key. It adds itself to the hold's pending erasures. At release, the worker deletes the copies, destroys the keys unless another hold covers them, and writes a `gdpr.erasure_deferred_completed` event. The receipt's `legal_basis` uses the existing value `gdpr_art17_with_art17_3_exception`. Whether the tenant-visible receipt states that something was retained is decision 7, and it has a legal dimension: §11.8 states that Lenny operates as a data processor, and Article 17(3) sets out the exceptions, so the operator needs its own basis for retaining against a controller's instruction.

**Tenant deletion.** Phase 3.5 enumerates security holds and does not block on them, since the originals are already segregated. It waits for any hold still `materializing`, and it escrows held evidence: `EvidenceStore` moves marked batches to the escrow prefix, unwraps per-session keys under the tenant KEK, and rewraps them under the escrow KEK before Phase 4a destroys the tenant key. Batch bytes do not change, so anchored digests still verify. The audit copy carries the anchors the Phase 4 audit eraser deletes. If materialization cannot finish before the §12.8 deadline, the phase blocks fail closed, and the existing `TenantDeletionOverdue` alert fires on the operator side with no tenant-visible reason.

**Restore.** The post-restore reconciler's ledger gate (§25.11 `confirm-legal-hold-ledger`) gains `security_holds`, so a restored database does not replay an erasure that a hold deferred.

### Security retention class

A stream can carry `retentionClass: security` at the deployment level. For those streams, tenant teardown moves batches inside the stream's retention window into the escrow prefix, where they stay for `forensics.retention.afterTeardownDays`, regardless of any hold. The class exists because a tenant that offboards after a compromise otherwise removes the evidence (R4), and the egress design states that tenant deletion removes these streams today.

The recommended members are `credential.mint`, `egress.decision`, `egress.tunnel`, and `git.operation`, because they carry session identifiers and no content. `agent.activity.*` stays out unless a hold covers it, because of its sensitivity. The chart default is 0 days, and a SaaS operator sets a value that its customer agreement states (decision 8). Recital 49 names network and information security as a legitimate interest for processing that is strictly necessary and proportionate, which is the argument the operator's counsel would assess. §12.9 gains a row for retained evidence.

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
| Envoy-measured fields, sent by a second access logger directly to `lenny-evidence` | Downstream peer address, CONNECT authority, upstream address and source port, times, bytes, and the Envoy replica | The decision service | New stream `egress.accesslog` (see "Changes requested") |
| `credential.mint` from the Token Service | Each mint, with its own `pkg/binding` read | The decision service | From EG-2 |
| `git.operation` from the mirror | Fetches through the mirror, with its own binding read | The decision service | From EG-5 |
| `pod.address`, from the controller's watch | Pod UID, addresses, node, and tenant label over time, which resolves a peer address at a time to a pod | The decision service and `netagent` | New stream, provenance `observed` |
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
| R-GIT-CLAIM | `netagent`'s `git.operation` with the mirror's record, the access log, and any provider log | A self-reported operation has no enforced counterpart, or the reverse. |
| R-LOCAL-CLAIM | `egress.local` tunnel closes with the access log's end times | `netagent` reported a close that Envoy never saw. |
| R-SEQUENCE | Sequence gaps, incarnations without a closing record, compaction anchors, and §11.7 chain state | Loss or tampering, using the detection the egress design specifies. A gap alone is a low-severity finding. |
| R-EDGE | The access log's byte counts with NAT or flow logs, per replica and time bucket | Traffic left the cluster outside the proxy, or the proxy under-reported. |
| R-ACTIVITY | `agent.activity.tool` commands with `agent.activity.exec` | A reported command with no process, or a process with no tool call in its ancestry. Informational, because scripts legitimately spawn children. |

A lossy input produces low-severity findings on its own, because `egress.tunnel` can lack close records during an Envoy crash. A rule raises severity when two independent sources agree against a third.

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

1. Open a case, then set a security hold covering the tenant and range.
2. Quarantine the pod. If `lenny-ctl` is unavailable, use the break-glass label.
3. Capture memory and process state (table below). Each archive contains every memory page of the checkpointed processes, including keys (Kubernetes warning), so the Job encrypts it to the case's public key before it leaves the node and removes the node copy.
4. Copy tmpfs volumes (`/sessions`, `/tmp`, and `/run/lenny` for the credential file), then `/workspace` and `/artifacts`, then container logs. The Job hashes each file.
5. Register each capture.
6. Reset open tunnels if R-TUNNEL-AFTER-END fires or `netagent` is suspect. Envoy has no per-tunnel kill, so the procedure runs the chart's graceful drain against the replicas named in `egress.accesslog`, which interrupts other tenants' tunnels on those replicas until they reconnect. On a shared replica set, an investigator weighs that against the pool's remaining tunnel cap.
7. Rotate credentials. `credential.mint` lists every token and secret version the pod received. Revoke direct-mode LLM credentials through `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke`.
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
3. Run `attribute` with a window of plus or minus 60 seconds. It reads `egress.tunnel`, joins `egress.decision`, and checks attribution against `egress.accesslog` and `pod.address`, so it reports a mismatch beside each candidate.
4. For each candidate, read the session's owner, then `agent.activity.exec` joined on the decision identifier and local port, when coverage exists.
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

The other profiles differ. `standard` pools get FE-6a, plus a published producer contract, so a deployer running Tetragon or Falco can forward to `lenny-evidence` under its own identity, and a reference forwarder for Tetragon is tested on Kind (decision 3). `microvm` pools get FE-6a and the network evidence in the first version, and the API reports `coverage: tool_only` for them, so investigators see the gap (decision 2). An in-guest sensor is later work. Under the envelope's rule it would still be `self_reported`.

A pool's RuntimeClass selects coverage. The chart documents a `gvisor-monitored` RuntimeClass whose runtime handler points at a `runsc` configuration naming the sink, so a pool opts in by RuntimeClass, and an unmonitored gVisor pool on the same node has no session and no cost. Configuring the handler is node provisioning, which the cluster admin owns, as with RuntimeClass installation today (§17). GKE Sandbox preconfigures gVisor, and the Falco Helm chart installs there through a values file, so the managed path needs its own check.

### Streams

| Stream | Producer | Provenance | Payload | Default retention |
| :-- | :-- | :-- | :-- | :-- |
| `agent.activity.tool` | Gateway, including reports it forwards for the adapter and the SDK | `enforced` for calls the gateway carried, `self_reported` otherwise | `toolCallId`, tool, connector, phase, outcome, bounded redacted input, duration, and `reportedBy` | 90 days |
| `agent.activity.exec` | Collector | `observed` | `kind` (`exec`, `exit`, or `connect`), PID, parent PID, process start time, binary path, `argv[0]`, argument count, UID, container name, working directory class, exit status, loopback port for a `connect`, and non-proxy `connect` targets | 30 days |
| `agent.activity.exec.args` | Collector | `observed` | The process key and its redacted, bounded arguments | 14 days |
| `agent.activity.file` | Collector | `observed` | Per process and directory prefix: operation and count per window. Exact records for the watchlist and for writes outside the workspace | 30 days |

Separate streams carry different permissions, retention, and keys, which answers the outline's question about differing audiences: a customer's security team can hold `exec` while only forensics holds `args`. Attribution comes from ingest. The collector supplies the pod and container and the time, and `lenny-evidence` sets tenant and session from the binding at that time. On an embedded pod the adapter's own work appears in the `adapter` container at the agent UID, and the record's binding state (warm-up, materializing, or running) is the distinction available. Warm-pod records have no tenant and use the platform prefix, as the envelope states. Records on a concurrent-slot pod take the session from a working directory under `/workspace/slots/{sessionId}/`, and otherwise carry only the tenant.

**Joining a command to a tunnel.** Prompt-injection exfiltration is the case an investigator most needs answered: which command opened this connection. `agent.activity.exec` records a process's `connect` to the local endpoint with its local port, and `netagent` knows the same port for the tunnel it opens with a decision identifier. Recording that port in the decision path joins process to tunnel without trusting either record alone (see "Changes requested"). A `connect` to any address other than the local endpoints is also recorded, because the NetworkPolicy drops it and the attempt itself is a signal.

### Producer contract and channels

- **SDK.** The runtime writes an `activity` frame on `CH-MSGSOCK`, or calls the SDK's `reportActivity`. The adapter validates, sizes, and rate-limits it (§4.7.11 item 3), and sends it on a new channel proposed as `CH-ACTIVITYREPORT`. The gateway overwrites session and tenant from the binding, rejects a report for a session the pod does not hold, forces `self_reported`, and forwards to `lenny-evidence`, as the decision service does for `netagent`. Reporting is optional at every integration level, and an absent report shows as `coverage` rather than as an error.
- **Collector.** One unprivileged process per monitored node, running as non-root, with a hostPath directory for its socket, an informer limited to its own node's pods, and a `lenny-evidence` client. gVisor allows one trace session, named `Default`, with several sinks, so a deployer already running Falco on these nodes merges the two configurations. It filters the fields that gVisor always sends (a third-party trial reports that `container/start` carries arguments and the working directory regardless of optional fields) before anything leaves memory. It authenticates with a projected token carrying pod and node claims, and ingest accepts a record only for a pod on that node (see "Changes requested"), so a compromised collector can forge records for pods on its node and no others.
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
| Fingerprints | HMAC-SHA-256 under a key derived from the session's data key. An investigator holding a leaked value can test whether it appeared in a session, through `forensics.query`, without the record storing it. |
| Detail setting | `metadata` writes no `args` stream, `redacted` is the default, and `full` writes arguments with the structural layer off, and needs a platform gate and a tenant opt-in. |

Redaction misses secrets with no recognizable form, so §12.9 classifies all four streams as T3 by default and T4 when `workspaceTier` is T4. Encryption and restricted reads are the control, and redaction reduces exposure. Tool input from the SDK passes the same rules, in the adapter, because a compromised runtime controls that input.

### Keys

Each session has a data key, generated by `lenny-evidence` at the first record, wrapped by the tenant's KMS key on T4 and by a regional evidence key otherwise, and stored in `evidence_session_keys` (tenant, session, user, wrapped key, state, and timestamps). `payloadKeyRef` names it. `lenny-evidence` encrypts on ingest, so digests are computed over ciphertext and still verify after destruction. A record with no session (service mode, or a slot the working directory rule cannot attribute) uses a key per tenant and pod incarnation.

| Event | Effect |
| :-- | :-- |
| Read | The gateway's handler asks `lenny-evidence` to decrypt, after its permission check. A cross-tenant platform read writes `forensics.evidence_read`. |
| `DeleteByUser` | The job destroys the keys of that user's sessions, and `lenny-evidence` records a tombstone. A key under a security hold is skipped and queued in pending erasures. A pod-incarnation key is destroyed when any user whose session ran on that pod is erased, because it cannot be scoped further, unless a hold covers it. |
| Retention expiry | The sweeper deletes batches, and it destroys a session's key once its last batch is gone. |
| Tenant teardown | Keys are deleted with the tenant, except those under a hold or the retention class, which are rewrapped under the escrow KEK. |

A compacted object holds several sessions' ciphertext, so a hold on one session's manifests keeps other sessions' ciphertext in the same tenant-hour. Their keys follow normal retention, so those records become unreadable on schedule.

### Who may read

The streams are customer data about the customer's own agents. A tenant admin can already read session content (§10.2 `Read other users' sessions`), so a tenant read of `tool` and `exec` grants nothing new. An operator read is the new exposure. Proposed defaults, for `security-oversight-design.md` to decide:

| Permission | Default holders | Operator access |
| :-- | :-- | :-- |
| `evidence.read.agent.activity.tool` | `tenant-admin`, `security-auditor` | `forensics.query` with an open case naming the tenant |
| `evidence.read.agent.activity.exec` | `tenant-admin`, `security-auditor` | Same |
| `evidence.read.agent.activity.file` | `tenant-admin`, `security-auditor` | Same |
| `evidence.read.agent.activity.exec.args` | None until granted by a tenant admin | Same, and each read writes a custody event |

`tenant-viewer` holds none of these by default, because an activity stream can expose paths and commands of other users' sessions in a form the viewer role's session read does not present today. A tenant admin who is the subject of an investigation can read these streams, which is why the operator's copy is the one under hold.

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
- **Manifest rows.** The compaction bound of one row per tenant-hour per stream gives 120,000 rows per day per stream at 5,000 tenants, so these streams raise the egress design's 600,000 to at most 1,080,000.

**Per-tenant cost.** A tenant's stored bytes are `sessions per day × Σ (MB per session × retention days)`. At the assumed sizes, that is 80 MB per daily session, so a tenant with 100 sessions per day holds about 8 GB. `lenny_evidence_bytes_total{tenant_id, stream}` and a daily `evidence.usage` billing event in §11.2.1 let an operator charge for it, and the tenant's activity bytes count against a separate `evidenceQuotaBytes`, so a full quota produces gap records and never refuses a session. The operator bears the collector's node cost in the pool's resource accounting, because `applyResources` copies the pool's class onto pod containers and the collector is outside the pod.

### Failure behaviour

| Failure | Behaviour |
| :-- | :-- |
| Collector absent when a sandbox starts | `ignore_setup_error: false` on `gvisor-monitored` fails the sandbox, so the pod fails its warm fill, and the alert names the node. |
| Collector restarts with sandboxes running | A third-party plugin's README states that `runsc` connects to the sink once at sandbox start, does not reconnect, and needs a sandbox restart after the receiver restarts. A Kubescape trial states that its restart run does not establish recovery of the existing connection. Running pods therefore go dark until they retire, unless the spike finds a reattach path. The collector records `coverage: lost` for each, and `ActivityCoverageLost` fires. The DaemonSet uses `OnDelete` updates during node rotation. Whether `runsc trace create` can reattach from a Job is a validation item. |
| Coverage lost on a bound pod | `forensics.activity.onCoverageLoss`: `continue` (default) records the gap, `retire` refuses new sessions and retires the pod after its session, and `quarantine` applies on regulated tenants only by explicit choice. |
| Sentry drops a trace point | The sink's sender drop count, where reported, becomes a gap record. A third-party trial notes that zero reported drops does not prove zero loss, so absence of a record is never proof. |
| `lenny-evidence` unavailable | The collector buffers per tenant, then records a gap. Activity never blocks the agent, including on a regulated tenant, because the write-ahead rule for egress decisions exists to make a record precede a connection and the volume here would make it an availability risk. |
| Runtime does not report | `agent.activity.tool` holds only gateway and adapter records, and `GET` reports `coverage`. |
| Node loss | Unflushed records are lost, which the incarnation rule detects. |

**Coverage API.** `GET /v1/admin/sessions/{id}/activity-coverage` and a tenant equivalent return, per pod incarnation, which producers ran, from when to when, and any gap (R8).

### Read API and export

`GET /v1/agent-activity` takes `sessionId`, `stream`, `kind`, a binary or path prefix, and a time range, and it needs the stream's permission. `GET /v1/admin/evidence/{stream}` is the platform form. Export uses the egress design's cursor path, with OCSF translation at delivery: Process Activity (class 1007) for `exec` and File System Activity (class 1001) for `file`, both read from the OCSF v1.1.0 schema, and `exec.args` in `unmapped.lenny.*` only when the subscription holds its permission. A SIEM copy is outside key destruction, so the export subscription states this and §12.8 lists it as a sink to notify.

## Configuration by scope

| Setting | Description | Applies to | Managed by | Notes |
| :-- | :-- | :-- | :-- | :-- |
| `forensics.enabled` | Turns on the case, quarantine, and hold APIs and the `platform-forensics` role. | Deployment | Platform admin (Helm) | |
| `forensics.breakGlassGroups` | Lists the Kubernetes groups that may stamp the quarantine label directly when the API is unavailable. | Deployment | Platform admin (Helm) | Empty disables the path. |
| `forensics.quarantine.alertAfter` | Sets the age at which `QuarantineAged` fires. | Deployment | Platform admin (Helm) | 24 hours by default. It never releases. |
| `forensics.quarantine.maxPods` | Sets the count of quarantined pods at which an alert fires. | Deployment | Platform admin (Helm) | It alerts and does not refuse. |
| `forensics.twoPersonRelease` | Requires a release to come from a different principal than the one who set it. | Deployment | Platform admin (Helm) | Decision 12. |
| `gitMirror.forensics.volumeSnapshotClass` | Names the CSI snapshot class for store holds. | Deployment | Platform admin (Helm) | Empty means no snapshot, and the API reports it. |
| `gitMirror.heldUidBudget` | Caps the fetch UIDs that held stores may keep out of the pool. | Deployment | Platform admin (Helm) | |
| `storage.regions.<region>.legalHoldEscrow` | Names the escrow endpoint, bucket, KMS key, and KEK, reused for security holds. | Region | Platform admin (Helm) | Existing setting (§12.8). |
| Stream `retentionClass: security` and `forensics.retention.afterTeardownDays` | Selects the streams retained after tenant teardown, and for how long. | Deployment | Platform admin (Helm) | 0 days by default. Decision 8. |
| `lenny-evidence-reconcile` schedule and imported sources | Sets the reconciler's cadence and the object store prefixes holding NAT, flow, provider, and Kubernetes audit logs. | Deployment | Platform admin (Helm) | Imports are optional. |
| `forensics.activity.enabled` and per-stream retention | Turns on agent activity collection and sets the ceiling on detail and retention. | Deployment | Platform admin (Helm) | Off by default. Decision 4. |
| `forensics.activity.fullDetailGate` | Grants a tenant the right to choose `full` detail. | Deployment and tenant | Platform admin (admin API) | Same pattern as the egress gates. |
| `forensics.activity.redactPatterns` and `execRatePerPod` | Extends the redaction patterns and sets the per-pod rate limit. | Deployment | Platform admin (Helm) | Patterns can be added and shipped ones cannot be removed. |
| `lenny-activity-collector` image, resources, and producer allowlist | Installs the collector, and lists deployer sensors allowed to write activity streams. | Deployment | Platform admin (Helm) | |
| `gvisor-monitored` runtime handler and sink configuration | Points `runsc` at the collector's socket, on nodes that run monitored pods. | Node pool | Cluster admin | Node provisioning, with a preflight check. |
| `isolationProfile` and RuntimeClass selection | Chooses monitored or unmonitored gVisor for a pool. | Pool | Platform admin | Existing field. |
| `forensics.activity.fileStream` | Turns on the `file` stream for a pool. | Pool | Platform admin | Off for pools whose workloads make the Sentry cost unacceptable. |
| `forensics.activity.onCoverageLoss` | Chooses what happens to a bound pod that loses coverage. | Pool | Platform admin | Decision 19. |
| `agentActivity.level` | Chooses `none`, `tool`, or `process` within the ceiling and the pool's coverage. | Tenant | Tenant admin | A session cannot lower it. |
| `agentActivity.detail` | Chooses `metadata`, `redacted`, or `full`. | Tenant | Tenant admin | `full` needs the gate. |
| `agentActivity.retentionDays` per stream | Shortens or lengthens retention within the deployment's limits. | Tenant | Tenant admin | Subject to any regulatory floor. |
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
3. `agent.activity.exec`, read under the case, shows a `python` process in that session's runtime container with `connect` records matching the local ports of those tunnels. `agent.activity.tool` shows that the command ran after a fetch of a web page whose text instructed it. This suggests prompt injection rather than tenant-directed abuse.
4. The session is still running, so Dave quarantines the pod with `sessionDisposition: fail`, sets a security hold on the session with `evidence`, `audit`, `artifacts`, and `transcripts`, and captures memory with `runsc checkpoint --leave-running`.
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
6. Carol sets a security hold for the session and the user, and reports the result to Bob. Where the fingerprint search finds nothing, the investigation has no conclusion on that point, because redaction patterns, coverage gaps, and pods with `tool_only` coverage leave room for a miss.

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

1. Step 0 finds no §12.8 legal hold, so the job starts. The security hold does not block it, and the response is the normal one.
2. The job drains Alice's sessions and deletes the originals in the tenant's stores. A quarantined pod remains, and the release step skips it.
3. The escrow copies stay, and held evidence manifests stay. The per-session keys for Alice's sessions under the hold are skipped, and the job is added to the hold's pending erasures. Keys for her other sessions are destroyed, so those records become unreadable while their digests still verify.
4. The receipt records `legal_basis: gdpr_art17_with_art17_3_exception` and the fact of a retention, in the form decision 7 selects.
5. When Dave releases the hold, the worker deletes the copies and destroys the keys, unless another hold covers them, writes `gdpr.erasure_deferred_completed`, and terminates the quarantined pod, removing its volumes.

If `acme` asks for tenant deletion instead, Phase 3.5 waits for the hold to finish materializing, escrows held evidence and rewraps its keys, and continues, so Phase 4a can destroy the tenant key within the T4 deadline. A quarantined pod keeps tenant data on its node's disk past that deadline, which the receipt and the case state.

## Residual risks

- **Lost before quarantine.** Memory and tmpfs state from before the first alert are gone unless the pod is still running, and the default pod lifetime is one session (§6.1). Many investigations start from records alone. A pool can extend `retentionClass` evidence, and nothing extends a pod's life in advance.
- **Node-pressure eviction and node loss.** The kubelet's eviction ignores PodDisruptionBudgets and grace periods, and a hard threshold terminates immediately, so a quarantined pod can be lost. Quarantined pods should be captured promptly, and the loss is recorded.
- **A compromised runtime destroys its own state.** Nothing stops a process from exiting or from overwriting its workspace before the quarantine lands. A compromised `netagent` restarts on exit, which loses its memory.
- **Open tunnels.** Quarantine does not end an open tunnel through a compromised `netagent` until the pool's cap or a replica drain, because Envoy has no per-tunnel kill. A drain interrupts other tenants. Exfiltration can continue in that window, and `egress.accesslog` records it.
- **Kernel or Sentry compromise.** An attacker who controls the Sentry can stop trace points and forge them, and an escape reaches the collector and any forensic Job on that node. A Kata pool has no `observed` activity evidence in the first version.
- **Coverage gaps.** Drops, a disconnected sink, redaction misses, and a runtime that reports nothing all leave holes. `coverage` and gap records state what was missed, as far as they can tell, and absence of a record is never proof.
- **Agent evasion by interpretation.** An agent can read a file through a long-running interpreter with no new `exec`, and a `file` aggregate shows a count per directory rather than each file. A file reached through a bind mount or hard link can evade a path filter on a host sensor. The watchlist reduces this for the paths that matter most.
- **Privacy of the evidence itself.** Activity streams record what developers and agents did, including paths and commands that identify people, and redaction is imperfect. A held key survives an erasure request. Captures contain all process memory. Counsel should review the operator's basis, and the customer agreement should state collection, retention, and operator access.
- **Tipping off.** FP3 hides holds from tenant-readable surfaces. A session ending as `POD_WITHDRAWN` and a pod's absence remain observable, and a tenant could infer an investigation from a pattern of them.
- **Platform trust.** A database superuser, a cluster admin, a compromised `lenny-evidence`, and a platform admin who grants itself a role are all inside the boundary. The audit chain, pgaudit, and the SIEM copy detect some of this after the fact, as §11.7 states. A snapshot volume holds other tenants' stores until extraction.
- **Compromised mirror parent.** It can forge `enforced` `git.operation` records (the limit the egress design states), and it can ignore the held flag. The snapshot is the check, taken up to 10 seconds later.
- **Capture methods untested.** The gVisor and Kata memory captures are unproven on Lenny's node configuration, and an operator should test them before needing them.

## Specification sections affected

- §4.6.1, §4.6.3, and §6.2: the `quarantined` binding state and phase, the projection and drain edges, orphan GC exclusion, the label check in `lenny-sandboxclaim-guard`, the forensics reconciler's PDB and VolumeSnapshot grants, and RBAC.
- §4.7, §28.3, and §28.5: `CH-PRESERVE`, `CH-ACTIVITYREPORT`, and the `activity` frame on `CH-MSGSOCK`, with register rows. These identifiers are proposals under the naming law.
- §4.7.11: the adapter's validation of `activity` frames.
- §5.3 and §17.2: the `gvisor-monitored` RuntimeClass, and the collector and `lenny-forensics` namespace rules, with admission inventory and preflight checks.
- §7.3: `POD_WITHDRAWN` and the `resume` disposition.
- §10.1: the preservation state beside hold state.
- §10.2: the `platform-forensics` role and permission rows.
- §11.2.1: the `evidence.usage` billing event and `evidenceQuotaBytes`.
- §11.7: the `forensics.*` events under CMP-058, the anchor retention rule, and a correction of the "6004 File System Activity" class name (OCSF v1.1.0 lists File System Activity as class 1001, and Datastore Activity as 6005).
- §11.8: quarantine, security holds, the reconciler, and the capture runbook as first-responder primitives.
- §12.2 and §12.6: `pod_quarantines`, `security_holds`, cases, `evidence_session_keys`, and `EvidenceStore`'s escrow operation.
- §12.8: the meaning of `audit_range` and `workspace_snapshot`, security holds in step 0 and Phase 3.5, deferred erasure, key destruction, mirror purge, the retention class, and the post-restore gate.
- §12.9: rows for activity streams, captures, and retained evidence.
- §13.1 and §13.2: selector exclusions on the agent policies, the collector and reconciler rows, and the deletion rule in `lenny-label-immutability`.
- §15.1: the new admin endpoints, error codes (`QUARANTINE_NO_POD` and `SECURITY_HOLD_REGION_UNRESOLVABLE`), and the correction of the `POST /v1/admin/legal-hold` body, which the specification gives as `resourceType` and `resourceId` while the code takes `tenantId`, `sessionId`, and `artifactId`.
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
| FE-1 | Quarantine | The API, the row and label, the binding state's writer, the phase, every retirement path, the preservation RPC, the policy selectors, the webhook rules, the PDB, and release. |
| FE-2 | Mirror store hold | The store APIs, the flag's setter, the UID rule, stop-serving with a fresh generation, the snapshot, and purge. |
| FE-3 | Security holds and the retention class | The hold API, copy on hold, the audit tuples, the marker's setter, the GC guard, step 0 and Phase 3.5, deferred erasure, escrow of evidence and keys, and the retention class. |
| FE-4 | Reconciler | `pod.address`, `lenny-evidence-reconcile`, the rules, `evidence.reconcile`, imports, and the API. |
| FE-5 | Capture runbook and abuse workflow | The unbound role, the published capture image, capture registration, `attribute`, export and verify, and the runbooks. |
| FE-6a | Tool activity | `agent.activity.tool`, the key store, the SDK and adapter report, the coverage API, the read API, and redaction. |
| FE-6b | Process and file activity | The collector, `gvisor-monitored`, `agent.activity.exec`, `.exec.args`, and `.file`, the producer contract, and the Tetragon reference forwarder. It starts with the spike under "Validation owed". |

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| FE-0 | None | It adds records and a role. |
| FE-1 | FE-0, EG-2 | The case record, and the `quarantined` binding state |
| FE-2 | FE-0, EG-5 | The case record, and the held flag |
| FE-3 | FE-0, EG-10 | The case record, the held marker, and `EvidenceStore` |
| FE-4 | EG-2, EG-3, EG-5, EG-10, FE-1 | The streams it reconciles, and the quarantine rule |
| FE-5 | FE-0, FE-1, FE-2, FE-3, EG-8 | The procedures it documents, and the cross-tenant query |
| FE-6a | EG-9, EG-10, FE-3 for its hold behaviour | The named permissions, the envelope, ingest, the key field, and the marker. It needs FE-3 only for the hold rule, and a release before FE-3 ships without that rule. |
| FE-6b | FE-6a, EG-11 | The streams and keys, and the PKI |
| FE-4's `R-ACTIVITY` rule | FE-6b | It lands with FE-6b, so FE-4 does not wait. |

The egress design's table lists FE-1 to FE-6 with their EG dependencies, and these add to it without conflict. No shared queue file exists, and proposals from different designs interleave as long as these hold.

| Other design | Relationship |
| :-- | :-- |
| `security-oversight-design.md` SO-1 | SO-1's `security-auditor` holds the activity read permissions if decision 5 is adopted. Neither waits for the other, because `tenant-admin` holds them until SO-1 lands. |
| SO-3 | A per-tenant SIEM receives activity streams through its export. FE-6a states what crosses it, and SO-3 states the destination. |
| SO-4 | SO-4 must exclude the `forensics.*` prefix from what it mirrors to a tenant, unless a case discloses it. If SO-4 lands first, FE-0 adds the exclusion. |
| SO-7 | FE-5 adds no right to `lenny-ops`, so it does not conflict with SO-7's narrowing. |
| `tenant-scoping-identity-design.md` TS-1 | Holds and streams use the tenant, and an Environment filter can be added to reads later. No dependency. |

## Changes requested of egress-and-git-design.md

Revision 37 of `egress-and-git-design.md` applied the items below that had to land before its proposals, so they are history here rather than open requests.

These are requests. This design does not edit that document, and it treats its hooks as fixed. Each item would be rework if added after EG-2, EG-5, or EG-10 ship.

| Change | In | Why it would be rework later |
| :-- | :-- | :-- |
| A second Envoy access logger, sent directly to `lenny-evidence` under Envoy's own identity, as stream `egress.accesslog`. It carries only fields Envoy measures: downstream peer address, CONNECT authority, upstream address and source port, times, bytes, the replica, and the decision identifier as a lookup key. | EG-2 and EG-10 | FE-4 needs a source the decision service does not control, and today the access log stream terminates at the decision service. It changes the chart's bootstrap, the SAN allowlist, and the pinned log formats. Records not captured cannot be recovered. |
| `netagent` records the local client port for each tunnel, in the decision path or in an `egress.local` tunnel-open record at every level, with the decision identifier. | EG-2 | It joins a process to a tunnel (FE-6). It changes the I2 CONNECT contract or `egress.local`'s volume. |
| An envelope option for a record to carry a chunk of one session's payloads, with `sequence` and an optional `sequenceEnd`. | EG-10 | A payload encrypted per record does not compress in a batch, and the activity streams are the first to use `payloadKeyRef`. A format change later would split stored records into two forms. The alternative, keeping per-record encryption, costs the storage shown under "Volume and cost" with no compression. |
| `payloadKeyRef` can name a key for a tenant and pod incarnation as well as a session. | EG-10 | Service-mode and unattributed slot records have no session. The envelope's wording says "per-session". |
| A per-node producer identity at `lenny-evidence`: a projected token with pod and node claims, plus a rule that a node-bound producer's records must name a pod on its node. `lenny-evidence` needs a `pkg/binding` view that includes `spec.nodeName`, and the informer's projection gains that field. | EG-2 and EG-10 | Ingest authenticates by client SAN today, and a certificate per DaemonSet pod is a different PKI cost. The projection's fields are stated in the design. |
| `EvidenceStore.DeleteByTenant` honours the held marker and has an operation that escrows marked batches and rewraps their keys. | EG-10 | The design states that "tenant deletion removes them" and that retention and the compactor honour the marker. The interface is new in EG-10. |
| The mirror's store index persists the fetch UIDs that own files in a held store, keeps them out of the pool across a restart, and bounds their number. | EG-5 | The UID reuse rule and the restart behaviour are specified around a re-chown that the hold skips. On a public store (mode `2775`), a reused UID could write a held store's files. |
| The on-disk store path includes a generation, so a held generation and its successor coexist under one store identity. | EG-5 | It changes the on-disk layout, which the egress scope rule names as costly. |
| The `quarantined` state names its inputs: the `lenny.dev/quarantine` label and an active `pod_quarantines` row, either of which applies. It takes precedence over every other binding state, including provisional and warm-up, and level 1's Helm-rendered tenant policies carry the same selector exclusion. | EG-2 and EG-7 | The hook names the state and not its source. Level 1 has no decision in the path for some flows, so the selector is its enforcement. |

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
| Operator guide `namespace-and-isolation.md` | States which evidence each isolation profile yields, because that now informs the choice of profile. | FE-6b |
| Operator guide `lenny-ctl.md` and `docs/reference` | The new commands, endpoints, errors, metrics, and alerts. | Each |
| Runtime author guide `platform-tools.md` | States that tool calls through the platform and connectors are recorded. | FE-6a |
| Runtime author guide `runtime-sdk.md` and `integration-levels.md` | The `activity` report and `reportActivity`, what to report for a shell tool, the redaction applied, and that reporting is optional and self-reported. | FE-6a |
| Runtime author guide `runtime-configuration.md` | States that monitored pools record the runtime's processes and files, and that a process-heavy runtime pays the file stream's cost. | FE-6b |
| Runtime author guide `lifecycle.md` | States that a pod can end with `POD_WITHDRAWN`, and that the runtime receives nothing further in that case. | FE-1 |
| Client guide | Adds `POD_WITHDRAWN`, with no explanation of cause. | FE-1 |

## Tests

These describe behaviour to pin. None exists yet. Every test carries its `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`.

### Tier 0: static

- The chart renders the quarantine exclusion on every agent allow policy, and a schema check fails a new policy that lacks it.
- The `lenny-forensics-capture` ClusterRole holds no `nodes/proxy` and is bound by nothing the chart renders.
- The collector's pod template passes `pkg/podsecurity` and holds no capability.
- `schemas/` and the OCSF mapping cover each new event type and stream.

### Tier 1: unit

- **Projection.** Each row of the retirement table, with and without the label, computes the stated phase.
- **Binding.** The label alone, the row alone, both, and neither yield the stated state, and quarantine takes precedence over every other state.
- **Holds.** Marker recomputation on release covers overlapping holds, an open-ended range, and a release while another hold still covers a row.
- **Redaction.** Each structural pattern, entropy threshold, truncation boundary, and credential path class, with values from a corpus of known secret forms and a corpus of harmless arguments.
- **Fingerprints.** A value matches its own fingerprint under the session key, and not under another key.
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

- **Holds on the compose stack.** A hold's audit tuples verify as a chain segment after the live rows are deleted. A held artifact survives rotation and deletion of its original, and the tenant's quota and legal-hold list are unchanged. The partition GC waits during materialization and drops afterwards.
- **Erasure.** `DeleteByUser` during a hold returns the normal response, deletes the originals, skips held keys, and completes at release, with the stated receipt fields, and a user with no hold follows the existing path.
- **Teardown.** Phase 3.5 escrows held evidence and rewraps its keys, Phase 4a destroys the tenant key, digests and anchors verify afterwards, and decryption works under the escrow KEK. A region with no escrow entry fails closed.
- **Retention class.** Marked streams survive teardown for the set period and are deleted afterwards, and unmarked ones are removed.
- **Keys.** Key destruction makes a payload unreadable while its digest verifies, and a retention expiry destroys the key after the last batch.
- **Sessions.** `sessionDisposition` `fail` and `resume`, on a single-session, a concurrent-slot, a service-mode, and a warm pod, and a delegation tree.
- **Restore.** A restored database replays no erasure that a hold deferred.

### Tier 5: end to end on Kind

- A quarantined pod keeps its volumes through a force-terminate, a tenant suspension, a recycle limit, an uptime limit, a pool deletion, a tenant deletion's Phase 5, a coordinator hold timeout, and a `kubectl delete`, and each is refused or skipped as stated.
- `kubectl drain` waits on the PDB, and the release lets it complete.
- The agent policies apply after the label on Calico or Cilium, because kindnet is not evidence of production enforcement, and new connections to the gateway, object store, and mirror fail.
- A mirror store hold stops re-chown, repack, purge, and fetches, a held public store's files stay unwritable by a later fetch, and `stopServing` serves a fresh generation.
- The kubelet checkpoint path under `standard`, with CRI-O or containerd with CRIU, produces an archive, and a runtime without it returns the documented error, which the runbook handles.
- The Tetragon reference forwarder writes `agent.activity.exec` for a `standard` pod.

### Tier 6: end to end on cloud

- On gVisor nodes: the collector receives `exec` and `file` records with correct attribution on sidecar and embedded pods, `ignore_setup_error: false` fails a sandbox with no collector, a collector restart produces `coverage: lost`, and an unmonitored RuntimeClass on the same node produces nothing.
- A host eBPF sensor on the same node records no process inside the gVisor and Kata pods, which pins the coverage table.
- `runsc checkpoint --leave-running` through the published image leaves the agent running and produces a registered archive. The Kata capture method, once chosen, gets the same test.
- GKE Sandbox as a managed configuration of `gvisor-monitored`, where it is available.

### Tier 7: load

- `exec` at the rate limit on many pods, with the collector and ingest at the assumed Tier 3 average and at a peak multiple, and the measured effect on a build workload against an unmonitored pool, with and without `openat` traced.
- A flood from one tenant's pod produces gaps for that tenant alone.
- Concurrent holds, retention sweeps, and compaction lose no marked row.
- The reconciler completes a tenant-hour within its schedule at the sized evidence volume.

### Tier 8: chaos

- Quarantine during a Postgres outage (break glass), during an API server outage (store re-read), and with a lost binding push (the 30-second bound).
- A node eviction of a quarantined pod records `forensics.evidence_lost`.
- A crash of the gateway between each step of the quarantine order leaves the reconciler to finish it, with no pod retired.
- `lenny-evidence` loss during materialization, during activity ingest, and during a sweep leaves nothing deleted.

### Tier 9: security

- A tenant admin's every read (audit query, legal-hold list, quota, retention, error codes, and session fields other than `POD_WITHDRAWN`) shows no sign of a hold, quarantine, or case, which pins FP3.
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

Each item is open, and nothing below has been adopted. Decision numbers are referenced above.

| # | Decision | Recommendation | Cost of the recommendation | Main alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Producer for agent activity | Two layers: tool activity on every profile, and gVisor runtime monitoring for `observed` process and file evidence. | A new DaemonSet, node configuration, and a dependency on gVisor's trace points, whose compatibility and reconnection behaviour need a spike. | SDK only: no node component, and everything is `self_reported`. A host eBPF sensor sees nothing inside the default profile. |
| 2 | Kata coverage in the first version | `tool_only`, stated in the API. | The highest-risk profile lacks process evidence, and `microvm` is what §5.3 recommends for that risk. | An in-guest sensor: a custom guest kernel and agent, as Ant Group built, with records that remain `self_reported`. |
| 3 | `standard` coverage | A public producer contract and a tested Tetragon forwarder, with no sensor in the chart. | A deployer installs a privileged DaemonSet itself. | A chart option, which adds the privileged component the egress design avoids, for a profile §5.3 reserves for development. |
| 4 | Default state | Off at the deployment level. A SaaS operator turns it on, and tenants choose within it. | No evidence exists for a tenant until someone chooses it, and an incident before that has none. | On by default at `tool` level, which is cheap and needs a customer agreement to state it. |
| 5 | Who reads activity | `tool`, `exec`, and `file` to `tenant-admin` and `security-auditor`, `args` only on grant, and the operator only under an open case. Owned by `security-oversight-design.md`. | A subject tenant admin can read its own streams, and the case requirement slows the operator. | Operator-only: simpler, and it removes the customer's evidence, which `security-oversight-design.md` exists to provide. |
| 6 | Does the tenant learn of a quarantine or hold | No signal by default, and disclosure as a recorded case action. | A tenant may see a failed session with no explanation for a period, and an operator can misuse silence. SO-4 must carve it out. | Immediate notice: simple and transparent, and it warns a malicious tenant. |
| 7 | Erasure during a hold | Proceed, defer the held part, and state in the receipt that some records are retained under an exception, without naming the case. | The receipt can hint at an investigation, and the operator needs its own legal basis, since §11.8 calls it a processor. Counsel must review. | Silent retention, which risks a false receipt. Blocking erasure, which reveals the hold and delays the tenant's obligation. |
| 8 | Retention class default | 0 days in the chart, and a SaaS operator sets a period in its agreement, 90 days being a starting value. | Without it, offboarding removes evidence. With it, data outlives the tenant, on a basis counsel must assess. | A non-zero chart default, which makes the promise for every deployer. |
| 9 | Who may set a quarantine | `platform-forensics` and `platform-admin`. Tenants ask through the operator. | A customer's team cannot freeze its own pod quickly. | A tenant request permission limited to an exclusive pod, once the audit and cost rules are proven. |
| 10 | Default session disposition | `fail`. | A false positive ends a customer's session. | `resume`, which keeps work going and restores possibly tainted state. |
| 11 | Quarantine lifetime | No automatic release, with an alert at 24 hours. | A forgotten quarantine holds a node from upgrading. Cluster upgrades can stall. | An automatic release at a deadline, which destroys evidence on a timer. |
| 12 | Release control | Two-person release and capture reads on by default for regulated tenants, and off elsewhere. | A small team needs a second person at release. | One person, with the audit event as the control. |
| 13 | Capture automation | No privileged Lenny controller. An unbound role and a published image, used through the deployer's privileged access. | Capture is slower and manual, and needs a practised operator. | An API-driven capture controller holding `nodes/checkpoint`, and privileged Job creation, at the cost of a standing high-value target. |
| 14 | Network cut on quarantine | Add the selector exclusion to every agent policy. | The chart's policies change, new connections to the gateway stop, and the gateway's RPC must arrive first. | Rely on the binding state alone, which leaves raw paths to the gateway and object store. |
| 15 | Argument detail default | `redacted`. | Secrets with no recognizable form are stored, encrypted. | `metadata`, which loses the arguments an exfiltration investigation needs. |
| 16 | Who pays | The operator pays for quarantined pods and held bytes. Tenants are metered for activity bytes. | Operators carry investigation costs. | Charging the tenant, which reveals the hold. |
| 17 | Source independent of the decision service | Envoy's second logger to `lenny-evidence`, with the deployer's log pipeline and flow logs as an optional outside source. | A second stream of tunnel records, and a compromised `lenny-evidence` remains a common point. | A log shipper in `lenny-egress` with its own bucket credential, which is stronger against `lenny-evidence` and breaks the rule that no producer holds a store credential. |
| 18 | How a hold protects artifacts | Copy on hold to escrow. | Copy time and storage at set time. Later writes to a session need a named range. | An in-place flag, which is cheaper and changes rotation and quota in ways a tenant can see. |
| 19 | `onCoverageLoss` default | `continue`, with an alert and a gap record. | A dark pod keeps running. | `retire`, which turns a collector rollout into capacity loss. |
| 20 | Selecting monitored pods | A separate `gvisor-monitored` RuntimeClass. | One more RuntimeClass, and node configuration. | A node-wide setting, in which an unmonitored pool pays for monitoring, and a collector outage fails every gVisor pod. |
| 21 | Default for the `file` stream | Off per pool until the spike reports the Sentry's cost. | Early adopters have `exec` and tool evidence alone. | On with `exec`, which risks build-time regressions unmeasured. |

## Validation owed

No validation has run. These come first, because the recommendation rests on them, and each is an input to a proposal.

1. **gVisor spike, before FE-6b.** On containerd and on GKE Sandbox: which trace points carry `execve` arguments and `openat` paths, what a build workload costs with each enabled, what the sender drop count reports under flood, whether a sink can reattach to a running sandbox, and whether a per-handler configuration selects monitored pods.
2. **Host sensor confirmation.** Tetragon and Falco on the same nodes, against gVisor and Kata pods, to pin the coverage table's inference.
3. **Volume.** Measured records per session on representative workloads, replacing the assumed figures, and the compression that a chunked envelope gains.
4. **Capture.** The kubelet path on the deployment's containerd or CRI-O, `runsc checkpoint --leave-running` on a Lenny pod including its native sidecar, and a Kata hypervisor dump for each hypervisor a deployment uses.
5. **Quarantine on CNIs.** What happens to existing flows after the selector change on Calico and Cilium, and how managed node upgrades treat a PDB that refuses eviction indefinitely.
6. **Legal review.** Decisions 6, 7, and 8, including the operator's role against a tenant's instruction, and what a customer agreement must state.

## Sources

Vendor pages describe the vendor's own product. Read on 2026-11-02, and none was tested.

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
- [containerd's CRI checkpoint handler](https://github.com/containerd/containerd/blob/v2.0.0-rc.1/internal/cri/server/container_checkpoint_linux.go) (source code at v2.0.0-rc.1).

**gVisor and Kata (project documentation unless stated)**

- [gVisor runtime monitoring](https://raw.githubusercontent.com/google/gvisor/master/g3doc/user_guide/runtime_monitoring.md), [seccheck](https://raw.githubusercontent.com/google/gvisor/master/pkg/sentry/seccheck/README.md) on sessions, optional fields, `ignore_setup_error`, and retries, and the [remote sink example](https://raw.githubusercontent.com/google/gvisor/master/examples/seccheck/README.md).
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
