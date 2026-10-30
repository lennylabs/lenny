# Problem: Pre-materialize large git repositories on a warm pod

## Statement

Proposal 0111 re-derives proposal 0077 from the remediation plan §10.2 entry "Proposal 0077, re-derived". The legacy design capture is `proposals/0077_new_pre-materialize-a-workspace-on-a-warm-pod.md`, which labels itself an early draft that was never reviewed or converged. Proposal 0111 supersedes it.

Goal (owner decision 2026-10-28, binding): pre-materialize workspace content on a warm pod, starting with very large git repositories, so that a session does not pay a full clone. Extension to other content is a non-goal of the first version. The proposal names an extension point and does not generalize the template beyond git.

Today every `gitClone` source is cloned cold by the gateway on each session bind, resume after eviction, and checkpoint restore. The gateway clones the pinned `resolvedCommitSha` over its own network path under a 5-minute timeout, buffers the whole repository as an in-memory tar.gz, and runs it through the §13.4 upload archive validators (256 MiB decompressed and 10,000 entries) before streaming the extracted files to the adapter through PrepareWorkspace. The adapter fails closed on any `gitClone` source that reaches it. The per-slot workspace is capped by `workspaceSizeLimitBytes` (default 512Mi). For the very large repositories the owner named, the result today is a failed session rather than a slow one.

Settled direction from the owner, to be specified rather than re-litigated:

- A template names a repository and ref. The proposal decides where the template lives: the sharedAssets precedent is a Runtime field that the controller encodes onto an adapter flag.
- At warm time a pod-wide bare git object store is filled on a volume that the runtime container mounts read-only, the way `/workspace/shared/` (sharedAssets, §6.4) is mounted. Read-only mounting prevents cross-session poisoning.
- Each session's repository is a per-slot repository over git alternates (`git clone --shared` or `--reference`), so refs, index, and new objects stay slot-local. It obtains only the delta from the cached ref to the session's pinned `resolvedCommitSha` and checks it out, falling back to a full materialization when the delta cannot be applied. Git is content-addressed, so a stale cache costs a larger delta and never yields a different tree.
- A slot never runs `git worktree add` against the shared store, because worktree administration, commits, and refs write to the shared repository. A runtime may still create worktrees inside its own slot repository.
- The shared store changes additively only while slots reference it. A fetch adds objects, and gc, prune, and history rewrites wait until no slot depends on the store (for example at re-warm).
- Pre-running setup commands is out of the first version.

First-order open decision, which the run decides before the other points:

- **Where git network I/O and pack parsing run.** No git network operation runs in the pod today. §14 states that the gateway performs the clone using its network path rather than the pod's, so the runtime never sees raw credentials. §13.4 states that pod binaries neither decompress archives nor canonicalize paths on untrusted input, and the adapter enforces this as a fail-closed trust boundary (F-7.4.1). The default `restricted` egress profile lets a pod reach only the gateway and the DNS proxy (§13.2.6), and the base NetworkPolicy opens only the gateway gRPC port and DNS. Both the warm-time fill and the per-session delta therefore need a specified network path and a specified credential path. The candidates are: (a) the gateway fetches from the remote and delivers the store content at warm time and the pack delta at session time, and the pod runs only local git operations (`clone --shared` from the read-only store, applying the delta, and checkout); (b) a gateway-proxied git transport; or (c) the pod fetches directly, which needs a pool egress relaxation, an amendment of the §14 sentence, and a scoped relaxation of the §13.4 boundary for remote-supplied packfiles. Option (a) keeps the existing boundaries intact. The run also states whether a gateway-produced pack applied in the pod counts as untrusted input under §13.4.

Other open design points the run must decide:

- **Private repositories and tenancy.** A warm pod holds no credential lease, and leases are assigned per session at slot assignment. Warm pods carry no `lenny.dev/tenant-id` until first assignment. A pool-level cache of a private repository would place one tenant's objects on a pod that is still unpinned. The run decides whether the first version limits warm caching to public repositories (no `gitClone.auth`) or to pools tied to one tenant, and which credential the warm fill uses if private repositories are in scope.
- **The archive path and size limits.** Whether a warm-cached `gitClone` bypasses the gateway archive path and its §13.4 validators, and how a large working tree fits `workspaceSizeLimitBytes`.
- **Checkpoint, restore, and eviction.** A slot repository that borrows objects through alternates depends on objects outside the slot workspace. A checkpoint restored onto a pod whose cache is missing or different leaves the repository with unreachable objects unless the design dissociates it (repack) or re-materializes it. The checkpoint size of a large working tree is part of this point.
- **Recycle scrub.** Whether the cache survives the §5.2 recycle scrub as pod state. The whole-pod scrub removes `/workspace/slots/*` and names no other volume. On the default non-recycling pool a pod serves one session, so the cache only moves clone cost to warm time.
- **Cache refresh cadence.**
- **Working-tree checkout cost** for very large trees: sparse checkout or a reused tree.
- **Disk sizing** and the failure mode when the cache volume is full. The sharedAssets precedent is an emptyDir bounded by node-local ephemeral storage.
- **Interaction with proposal 0086.** Proposal 0086 is Approved and not yet implemented. It binds and pins the plan at creation or finalize, composes runtime `workspaceDefaults` into the bound plan, and states that setup runs per session after materialization, worded to admit a later warm-time mode. The current spec still pins `resolvedCommitSha` "at session creation" (§14, §15.1). Proposal 0111 is drafted against 0086's post-landing wording ("when the plan binds").
- **The §14 per-slot scope note.** §14 says that the pool model relies on plan uniformity "to pre-warm pods with a single workspace template". No pool-level workspace template exists in the spec or the code. The run reconciles that sentence with the repository template.
- **Observability:** cache hit, delta size, and fallback metrics.

The plan asks for a spike measuring delta-plus-checkout against a cold clone on a large repository. Stage it as a non-spec test-infra deliverable with its pass criterion, rather than running it during drafting.

Optional cut: if the owner accepts public repositories only for the first version, private-repository fill, credentials, and tenancy become a follow-up proposal. Otherwise the proposal stays whole.

## Evidence

- (verified) Plan entry: `gateway-runtime-comms-remediation.md:3421-3437`, "Proposal 0077, re-derived" (Draft; owner decision 2026-10-28), carrying the settled direction and the credential, scrub, refresh, checkout, and disk-sizing open points. The plan says the spike "opens the run"; this statement stages it as a non-spec deliverable instead.
- (verified) Legacy capture: `proposals/0077_new_pre-materialize-a-workspace-on-a-warm-pod.md:3-8`, status "EARLY DRAFT ... Not reviewed, not converged".
- (verified) Gateway clone path: `spec/14_workspace-plan-schema.md:91` ("The gateway performs the clone using its network path, not the pod's, so the runtime never sees raw credentials"); `pkg/gateway/provisioning/gitref/clone.go:17-18` (`DefaultCloneTimeout = 5 * time.Minute`), `:38-50`; `pkg/gateway/provisioning/gitref/archive.go:16-39` (whole clone archived into memory); `pkg/gateway/podlifecycle/podsession/archiveextract.go:127-155` (`CloneArchive` then `archive.Extract` with the upload validators); `pkg/gateway/podlifecycle/podsession/workspacestage.go:16-45`.
- (verified) Adapter trust boundary: `pkg/adapter/workspace/materialize.go:499-502` ("gitClone sources are extracted by the gateway; the adapter must not clone or decompress"); `spec/13_security-model.md:749` (pod binaries neither decompress archives nor canonicalize paths on untrusted input).
- (verified) Default egress: `spec/13_security-model.md:495` (`restricted` (default): Gateway + DNS proxy only); `spec/13_security-model.md:89-123` (base pod egress policy); `spec/23_competitive-landscape.md:76` ("Gateway-mediated file delivery ensures pods never fetch external data directly").
- (verified) Archive limits: `pkg/upload/upload.go:30` (256 MiB), `:37` (10,000 entries); `pkg/upload/archive/archive.go:246-265`.
- (verified) Workspace size cap: `spec/04_system-components.md:273` (`workspaceSizeLimitBytes`, default 512Mi, emptyDir sizeLimit, checkpoint skipped over the limit).
- (verified) sharedAssets precedent: `spec/06_warm-pod-model.md:406`; `spec/05_runtime-registry-and-pool-model.md:107-110,226`; `pkg/controller/sandbox/podspec/sidecar.go:47` (read-only mount); `pkg/adapter/warmlayout.go:130-158` (adapter materializes only the inline set; `type: artifact` exists in the spec only).
- (verified) No warm credential and tenant pinning: `spec/06_warm-pod-model.md:15,26`; `spec/05_runtime-registry-and-pool-model.md:456-457` (`lenny.dev/tenant-id` set at first assignment).
- (verified) `resolvedCommitSha` pin timing today: `spec/14_workspace-plan-schema.md:102,104`; `spec/15_external-api-surface.md:624`.
- (verified) Proposal 0086 status Approved, implemented-date blank (`proposal-status.mjs`); its summary and spec changes state per-session setup timing worded to admit a warm-time mode.
- (verified) Per-slot scope note: `spec/14_workspace-plan-schema.md:5`.
- (corrected) The original pointer "pkg/adapter (gitClone materialization)" is wrong: no gitClone materialization exists in `pkg/adapter`. The clone lives in `pkg/gateway/provisioning/gitref` and `pkg/gateway/podlifecycle/podsession`.

## Who observes it

Deployers and clients whose sessions use very large git repositories observe it on every session start, resume after eviction, and checkpoint restore, because nothing is cached across sessions or pods. Sessions with small repositories are unaffected: they fit the existing limits, and workspace materialization is excluded from the pod-warm startup SLO (`spec/06_warm-pod-model.md:353`). This is an owner-requested capability with a binding direction (plan entry, 2026-10-28) rather than a defect in shipped behavior.

## What breaks if nothing changes

A session whose repository exceeds 10,000 files or 256 MiB decompressed fails with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. A repository that cannot clone within 5 minutes fails on the clone timeout. A working tree larger than `workspaceSizeLimitBytes` causes pod eviction or skipped checkpoints. Repositories within the limits pay a full cold clone, gateway memory for the whole archive, and a full stream to the pod on every bind, resume, and restore. A warm cache alone does not fix the failure cases unless the design also decides the archive path and size-limit point above.

## Findings this unblocks

- Plan §10.2 entry "Proposal 0077, re-derived". No BUILD-GAPS or TEST-GAPS finding id is named by the input.

## Prior art considered

- No landed or approved proposal, spec mechanism, or code path pre-materializes workspace or git content on a warm pod. A search for alternates, `clone --shared`, `--reference`, bare object store, and git cache across `proposals/`, `spec/`, `docs/`, `pkg/`, `BUILD-GAPS.md`, and `TEST-GAPS.md` matched only 0077, 0111, the plan, and unrelated code.
- Draft 0077 is the only earlier capture and stages nothing. 0111 supersedes it.
- sharedAssets (§6.4) is the precedent for the read-only warm-time volume. The code carries only inline file specs passed base64 on `--shared-assets`. Warm-time artifact delivery is specified but unimplemented, so it is missing code this proposal builds or depends on.
- Proposal 0086 (Approved, not implemented) leaves a hook for a warm-time setup mode and changes the pin timing to "when the plan binds". Setup pre-running is out of 0111's first version, so the setup hook is not needed yet.
- The §14 per-slot scope note asserts pre-warming "with a single workspace template", which nothing implements.
- Deployer-side alternatives exist without a platform change: a runtime image with the repository baked in, and parking pre-started sessions in `suspended` (bounded by `maxSuspendedPodHoldSeconds`). The owner chose to build the capability anyway.
- No open BUILD-GAPS or TEST-GAPS finding covers pre-materialization. F-5.1.23 (owned by 0086) only asks that setup timing stay a default. §21 defers SSH `gitClone` and does not mention a warm cache.

## Validated premises

- **premise lens (revise).**
  - Holds: the gateway clones over its own path today (§14; `gitref.Clone`; the binder rewrites gitClone into file sources; the adapter rejects gitClone).
  - Refuted, load-bearing: the statement treated the credential path as the only obstacle to running git in the pod. Default `restricted` egress and the base NetworkPolicy give a pod no route to any git host, so neither the warm fill nor a session delta fetch from the remote works on a default pool. Now the first-order open decision.
  - Refuted, load-bearing: the statement assumed the pod may run git against remote content. §13.4 and the adapter's F-7.4.1 guard forbid pod binaries from decompressing untrusted input, and a git fetch unpacks remote-supplied zlib packfiles. Either pack retrieval moves to the gateway or §13.4 is relaxed for this path.
  - Holds: sharedAssets is a read-only emptyDir filled by the adapter at warm time; its size is bounded by node-local ephemeral storage.
  - Holds: a warm pod holds no credential lease.
  - Holds: `resolvedCommitSha` is pinned at session creation and replay clones the pinned SHA.
  - Holds: per-slot repositories fit the slot model; the scrub names no other volume; the default non-recycling pool serves one session per pod.
  - Holds: 0086 is Approved with the described contract; the plan entry and legacy 0077 exist as described.
- **evidence lens (revise).**
  - Verified: plan entry, legacy capture, sharedAssets precedent, no warm credential lease, gateway clone path, and §5.2 scrub pointer.
  - Caveat: 0086 is Approved but not implemented, so "pin at bind" is the pending contract and the current spec still says "at session creation".
  - Corrected: the code pointer "pkg/adapter (gitClone materialization)" drifted; the clone lives in the gateway. The design moves git network access into the pod for the first time, so the per-session fetch's credential and network path and the gateway-clone boundary are decisions the run must make.
- **prior-art lens (stands).** Nothing pre-materializes git content on a warm pod; 0077 is an unconverged draft. sharedAssets is a precedent for the volume and mount only, with inline files in code. The §14 gateway-clone sentence constrains the design. 0086 leaves a hook and changes the pin wording. The §14 per-slot scope note is aspirational and needs reconciling. No open gap finding covers the capability.
- **scope lens (revise).** One proposal: the settled pieces depend on each other, and the spike is correctly a non-spec deliverable. Load-bearing: the statement omitted the in-pod git network path, which applies to public repositories too and to both the warm fill and the session delta. Load-bearing: default egress blocks any git host, so the network and credential path is the central decision. Private-repository tenancy is its own open point and the seam for an optional public-only first version. "Extensible to other content" is bounded as a non-goal with an extension point. Where the template lives is a detail within the proposal.
- **impact lens (revise).** The gap is real: every bind, resume, and restore clones cold with no cache. Load-bearing: for very large repositories the consequence is a failed session (archive validators of 256 MiB and 10,000 entries, a 5-minute clone timeout, the 512Mi workspace cap, skipped checkpoints), so the design must decide whether a cached clone bypasses the archive path. Load-bearing: pod egress for the warm fill, the delta, and the fallback is unaddressed. Checkpoint and restore of an alternates-backed repository on a pod lacking the cache is an unnamed consequence. Small-repository users are unaffected; the impact falls on owner-prioritized large-repository deployers.
- **alternatives lens (revise).** Deployer-side alternatives (baked image, suspended sessions) exist but the owner chose to build the capability. Load-bearing: in-pod fetches reverse §14, F-7.4.1, and default egress, including at session time. Load-bearing: the smallest change that keeps the owner's direction keeps network I/O and credentials on the gateway, which delivers the store at warm time and the pack delta at session time while the pod runs only local git operations. Load-bearing: warm pods are tenant-unpinned, so private repositories are a tenancy question first; the first version can limit caching to public repositories or single-tenant pools. Warm-time sharedAssets artifact delivery is unimplemented. The 0086 interaction is contained and reopens nothing in 0086.
- **Consolidated verdict.** Viable as a new capability. No load-bearing premise of the goal was refuted; the refuted premises are the implicit ones that the pod may reach a git host and parse remote packs, and they now appear as the first-order open decision.
