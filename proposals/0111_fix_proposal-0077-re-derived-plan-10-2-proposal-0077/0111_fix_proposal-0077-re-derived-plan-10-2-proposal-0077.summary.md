# Summary: Warm git object stores on sharedAssets, served from a gateway mirror over one GatewayControl RPC

## Summary

**Problem statement.** The gateway clones every `gitClone` source cold on each bind, archives the whole clone in memory, and runs it through the §13.4 upload archive validators (256 MiB decompressed, 10 000 entries) before the adapter receives extracted files. A very large repository therefore fails the session with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` or on the 5-minute clone timeout, and a repository within the limits pays a full clone on every bind. The owner's binding direction (plan entry "Proposal 0077, re-derived", 2026-10-28) is a pod-wide read-only bare object store filled at warm time, and a per-slot repository over git alternates that obtains only the delta to the pinned `resolvedCommitSha`, with a fallback, no worktrees against the store, additive-only store mutation, and no setup pre-running.

**What changes.**

- The Runtime `sharedAssets` list gains a `gitRepository` entry type, and §6.4 gains the **Repository stores.** paragraph for the warm fill, its failure mode, and store immutability (SPEC-1, SPEC-2, CODE-2, CODE-4).
- GatewayControl gains one adapter-initiated server-streaming RPC, `FetchGitPack`, served from a per-replica gateway mirror (SPEC-5, CODE-3).
- §14 gains **Cache-served `gitClone` sources.**: eligibility, tree validation with a materialization bound, slot-repository construction, the capture copy, and restore completion (SPEC-3, CODE-5, CODE-6).
- §13.4 gains a scoped exception for the adapter's local git on gateway-produced packs and agent-written slot repositories, with §7.4 and §2 pointers; §15.1 gains the `workspace_size_limit` reason; §16.1 gains two gateway metrics (SPEC-4, SPEC-6, SPEC-7).
- The images of the binaries that run git ship a git binary (CODE-1).
- A spike gates the code steps, and the tests and defect filings close the proposal (INFRA-1, TEST-1, REC-1).

**Decisions.**

1. The gateway performs every git network operation. The pod runs only local git. Default `restricted` egress, the base NetworkPolicy, the §14 gateway-clone sentence, and §23's gateway-mediated delivery sentence stay true and are not edited.
2. The store is a new `type: gitRepository` value on `sharedAssets`, with `url`, `gitRef`, and `destPath`. sharedAssets already supplies the warm-time read-only volume, the destPath merge rule, and the controller-to-adapter flag. The key is `gitRef` because `ref` already means a `lenny-blob://` URI on artifact entries.
3. The store is immutable after population. This settles additive-only mutation, gc, prune, and refresh by construction; a fresher store arrives with a replacement pod.
4. Public repositories only. A `gitRepository` entry carries no credential, and an `auth`-bearing `gitClone` source stays on the cold path, so an unpinned warm pod never holds tenant-private objects. This takes the owner's optional cut; see **Open decisions for human to make**.
5. A per-replica gateway bare mirror, fetched with `fsckObjects`, single-flight per url, on an `emptyDir` bounded by an in-process byte budget, writing no durable state. A stateless relay of a remote pack cannot validate the tree of `resolvedCommitSha` (it lacks the objects the haves exclude), so it would drop today's symlink-target validation; the mirror also turns N warm fills into one remote fetch per replica.
6. The binder validates the whole tree of `resolvedCommitSha` in the mirror at materialization, because the commit is pinned for the session and restore performs no checkout. `FetchGitPack` therefore serves objects only.
7. Packs are non-thin and produced by `pack-objects` from the mirror. The pod indexes them with `git index-pack --strict` under the §13.4 exception (SPEC-4).
8. The no-have fallback is a shallow pack of `resolvedCommitSha`'s tree, and a delta pack larger than that tree's blob and tree bytes is replaced by the shallow pack. Together with the materialization bound this keeps a cache-served source inside `workspaceSizeLimitBytes` at materialization; a full-history fallback would let a large repository pass the tree check and then evict the pod. A shallow slot repository has no history before the pin, which is accepted.
9. The slot repository is `git init` plus an absolute alternates entry, the indexed pack, and a detached checkout. `git clone --shared` is not used, because it copies store refs into the slot, which then reference history a replacement pod may lack.
10. The adapter copies store-only objects into the slot before every capture, twice (before the size probe and immediately before the archive), and restore runs a connectivity check bounded by the session delta. Without the copy, a session commit that reuses a store-only blob restores as a corrupt repository; the second copy and the check close the window a runtime that is not paused leaves.
11. Restore completion runs in the adapter after the workspace is restored and calls `FetchGitPack` keyed by the slot-local config §14 rule 3 records. Resume gains no field and no PrepareWorkspace step, and the gateway does no lineage walk.
12. The binder decides eligibility from current Runtime configuration. The adapter admits any well-formed eligible source and uses its store only to choose alternates and haves, so configuration skew takes the shallow fallback through the same RPC and no handshake field is needed.
13. A warm-fill failure, including volume exhaustion and a fill deadline, takes the existing §6.2 warm-fill edge to `failed`; on a `preConnect` pool the existing `sdk_connecting` watchdog also bounds the fill. The store has no size field and no fill-timeout Runtime field.
14. `FetchGitPack` follows the `ReportPodScrub` precedent: an mTLS-verified certificate and a valid SA token, the pod taken from `podId`, and `PERMISSION_DENIED` for a url outside the pod's Runtime. A spoofed `podId` exposes only public content and which repository a Runtime names, which is accepted; the binding gap is filed as a defect.
15. No §28 link, channel, or register row. Adapter-initiated GatewayControl RPCs carry no channel register entry; `FetchGitPack` is a §4.7.1 Adapter → Gateway row.
16. Two gateway-emitted metrics, because §16.1 places adapter-emitted metrics outside the §16.9 default scrape target set. `kind` is classified by what the handler observes, restore traffic is recorded with materialization traffic, and no hit count is derived from the metrics. An exact materialization hit count would need a request purpose field, which this proposal does not add.
17. The size and path checks surface through `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with the existing sub-codes plus `workspace_size_limit`, the spelling §4.4 already uses for the same limit.
18. Tree validation (§14 rule 2) runs inside the Upload Handler subsystem gate. Pack production runs under its own per-replica concurrency limit with no breaker, so a fill or restore never takes an upload slot or trips the upload breaker.
19. 0111 sequences after proposal 0086 and stages no edit to the §14 per-slot shared-template scope note or the pin-timing sentences.

**Watch out for.**

- GatewayControl's authentication chain is registered only as unary interceptors. `FetchGitPack` is the service's first server-streaming RPC and bypasses both checks unless CODE-3 registers stream interceptors.
- CRD `sharedAssets` are not mirrored into `runtimestore` today. Without CODE-2's `applyCRDFields` change, every `FetchGitPack` call is refused and every warm fill fails.
- `index-pack --strict` checks commit parents. On a shallow pack the `shallow` file must be written before indexing.
- `ValidateTree` results depend on the pool's limit. Memoizing the pass/fail verdict lets one pool's larger limit skip another pool's check.
- `/workspace/shared/` is read-write in embedded adapter mode, so a test asserting `EROFS` belongs to the sidecar model only.
- The default `workspaceSizeLimitBytes` (512Mi) refuses most very large trees with `workspace_size_limit`. A deployer raises the pool limit for those repositories, and the spike sets it explicitly.

## Goals

- A session on a pool whose Runtime names a public repository as a `gitRepository` shared asset obtains that repository from a warm store plus a delta, with no gateway clone and no archive.
- A cache-served source never reaches the §13.4 archive ceilings, and a tree too large for the pool fails deterministically at materialization.
- A checkpoint of an alternates-backed repository either restores intact or fails the resume, and never restores with a missing object.
- The pod gains no network route, no credential, and no ability to write a store it shares with other slots.
- Operators can observe pack sizes by kind and the number of cache-served sources.

## Non-goals

- Private-repository warm caching, warm-time credentials, and tenant-pinned pools. These are deferred to a follow-up proposal under the public-only cut.
- A §21 post-v1 bullet recording the private-repository deferral. The validated problem does not require it, and the follow-up proposal owns it.
- Sparse checkout or a reused working tree. A large tree fits only when the deployer raises `workspaceSizeLimitBytes`, and §14 rule 2 fails it deterministically otherwise.
- In-place store refresh, fetch into the store, gc, or prune.
- Pre-running setup commands, and proposal 0086's warm-time setup hook.
- Content other than git. The `type` discriminator on `sharedAssets` is the extension point.
- Option (c), pods fetching directly from the git host. It needs a per-pool egress relaxation, amendments to §14, §13.4, and §23, and it reverses F-7.4.1.
- Option (b), a gateway-proxied git smart-HTTP transport. It adds more surface than one want/haves RPC, gives the pod an arbitrary git endpoint, and the pod would parse remote-negotiated packs the gateway never validated.
- A stateless relay of a remote thin pack with in-pod `index-pack --strict`. It drops the symlink-target validation today's `gitClone` path performs.
- Incremental validation of only the trees in a delta pack. Subtrees reachable from store history were never validated, and symlink safety depends on a subtree's position.
- Validation inside every `FetchGitPack` call with a session id. Validation at materialization keeps a session id off the RPC.
- A gateway-produced pack plus index so the pod never runs `index-pack`. It ships extra bytes and adds an index-transfer surface.
- Git bundles and a new `gitBundle` WorkspaceSource type. The cache-served `gitClone` passes through unchanged.
- Delivering the session pack over PrepareWorkspace `upload_ref`. `FetchGitPack` already carries the warm fill, so one transport serves materialization and restore.
- `ResumeRequest.git_bundles` or a PrepareWorkspace step before Resume. Restore completion keyed by slot-local config needs no Resume change.
- Recording each pod's store commit in `agent_pod_state` or on `NegotiateVersionResponse`. The adapter supplies its haves on the RPC.
- The cache record as a comment line in `objects/info/alternates`. Slot repository git config is plainer and survives the checkpoint the same way.
- `git clone --shared` from the store.
- Dissociating or repacking the slot repository before checkpoint. It puts the whole repository into every checkpoint and breaks `workspaceSizeLimitBytes`.
- A full connectivity check (`rev-list --objects` over all history) on restore in place of the capture copy. It costs minutes on large histories.
- Accepting the corrupt-restore case for store-history objects.
- A new Runtime field (`gitCache`, `workspaceTemplate`, `repoCache`) with its own volume, `sizeLimit`, and `fillTimeoutSeconds`.
- A fill-timeout Runtime field and a store `sizeLimit` field.
- Leaving the pod READY without a store when the fill fails. That hides misconfiguration behind slow sessions.
- Gateway-pushed warm fill over `LNK-POD-GRPC`. The gateway never dials an unclaimed pod.
- Warm fill through `CH-OBJSTORE` presigned bundles. It needs minting at warm time, platform-owned blob GC and quota, and object-store egress on pools that do not checkpoint.
- Inline `sharedAssets` content for the store. The CRD caps `Content` at 32 KiB.
- Two RPCs for warm fill and session delta.
- Registering `FetchGitPack` as a §28 channel with a contract card and a failure-matrix row.
- A generic `FetchSharedAsset` transport keyed by entry index for `type: artifact`. Artifact delivery is out of scope.
- Making `auth`-bearing `gitClone` sources cache-served. `auth` signals private intent.
- Honoring `depth` or `submodules` on cache-served sources. Such sources keep the existing gateway path.
- Moving every `gitClone` onto the pack path and retiring `CloneArchive`. That exceeds the warm-cache scope and remains a candidate follow-up.
- Scoping the §13.4 archive ceilings away from `gitClone` and streaming the clone in place of the warm cache. The owner's direction is binding; cold-path limits for ineligible sources are a possible separate proposal.
- Raising the §13.4 archive ceilings globally. That weakens protection against client upload bombs.
- Adapter-emitted metrics.
- A new error code for size or path violations.
- Editing the §14 per-slot shared-template scope note or the `resolvedCommitSha` pin-timing sentences in §14 and §15.1.

## Open decisions for human to make

1. **Public repositories only.** Does the owner accept that the first version warm-caches public repositories only? A `gitRepository` entry carries no credential, and an `auth`-bearing `gitClone` source stays on the cold gateway-clone path, so an unclaimed warm pod never holds tenant-private objects. Ground: the drafting run took the owner's optional cut (Decision 4). If the owner rejects the cut, private-repository fill, a warm-time credential, and tenant-pinned pools must be added to this proposal.
4. **Warm-fill deadline override.** Should the adapter's warm-fill deadline `--git-store-fill-timeout` (default 15 minutes) be rendered into the adapter's arguments from a controller-wide chart value, or is a fixed default accepted? As staged (CODE-4 step 2.1), the flag is called operator-tunable, but the controller does not render it, so every pod runs the default and no operator can change it. The controller writes the adapter's arguments, as it does for `--workspace-size-limit-bytes` (pkg/controller/sandbox/podspec/objectstore_ca.go:42). Recommendation: render it from a controller-wide chart value. Ground: the project rule requires a default the spec does not fix to be overridable and documented as operator-tunable (.claude/rules/code-best-practices.md:71). On a pool without `preConnect`, this deadline is the only bound on a stalled fill, and fill time grows with repository size, which the deployer controls. A controller-wide value does not conflict with Decision 13, which rules out a Runtime or pool field only. Alternative, accept the fixed default and drop "operator-tunable" from CODE-4: the tree has precedent, because the adapter defines timing flags with defaults the controller never renders, such as `--session-start-ack-timeout` and `--heartbeat-ack-timeout-seconds` (cmd/lenny-adapter/main.go:233-241). It lost because those bounds do not scale with deployer-controlled input, and some of the precedent flags carry spec-fixed values that the rule does not reach. Alternative, render it from a Runtime or pool field: it lost because Decision 13 and the non-goal "A fill-timeout Runtime field" exclude it. Cost of deciding otherwise: with the fixed default, an operator with a large repository on a non-`preConnect` pool cannot raise the only bound on a stalled fill. Rendering adds a chart value, a controller flag, and an adapter argument to CODE-2 and CODE-4, and possibly a §17.8.1 row. Either answer changes the CODE-4 step 2.1 wording, which is false as it stands. Confidence: moderate.
8. **Restore on a different target Runtime.** Does the owner accept that deriving or replaying a cache-served session onto a Runtime that does not name the source repository url as a `gitRepository` entry fails the new session with `PERMISSION_DENIED`, with the remedy that the target Runtime names the same repository? A derive (`POST /v1/sessions/{id}/derive`) or a replay (`POST /v1/sessions/{id}/replay`, which requires `targetRuntime`) starts the new session from the source workspace on another Runtime, and §15.1 requires only that `targetRuntime` has the same `executionMode` (spec/15_external-api-surface.md:772). Restore completion (§14 rule 6) then calls `FetchGitPack` for the url the slot repository records, and SPEC-5 refuses a url the calling pod's Runtime does not name, so the restore fails. This holds once approved proposal 0109 lands, because its bind-time restore delivers the captured tree that rule 6 completes (see **Impacts on other proposals**, row 0109). Recommendation: accept, and state it as a clause under spec-changes.md **Edge cases and accepted failure modes** citing §14 rule 6 and §4.7.1, with a note on the replay docs page. Ground: the staged authorization model already produces this outcome, and the proposal accepts the same `PERMISSION_DENIED` failure for a Runtime that loses its entry (spec-changes.md:21). The case differs in cost: §15.1 names replay against a different runtime version as the primary mechanism for regression testing runtime upgrades (spec/15_external-api-surface.md:754), so the failure falls on a client workflow rather than on an operator's configuration edit. Alternative, authorize the restore fetch by the source session's lineage: it lost because it falsifies Decision 11 ("the gateway does no lineage walk"). Alternative, authorize a url the target Runtime does not name: it lost because it falsifies Decision 14, which authorizes against the pod's Runtime from `podId`. Alternative, make the capture copy every object so a restore needs no fetch: it lost because every checkpoint would then carry the base objects of the repository rather than the store-only objects Decision 10 copies, which counts the repository against `workspaceSizeLimitBytes` at every capture. Alternative, stage nothing: it lost because the failure on a primary workflow would be recorded nowhere. Cost of deciding otherwise: any design change reopens `FetchGitPack` authorization or restore keying, and with them Decision 10, 11, or 14; accepting it costs a clause and a docs note and leaves cache-served sessions replayable only onto Runtimes that name the same repository. Confidence: moderate.

## Defects in the shipped tree that this proposal does not stage

- **GatewayControl does not bind the pod to the client certificate.** `RequireVerifiedPeerInterceptor` checks only that the chain verified, and `ReportPodScrub` takes the pod from the request's `pod_id`. `FetchGitPack` inherits the gap. Binding GatewayControl identity to the peer's SPIFFE identity is a change to every pod-scoped RPC and stays out of scope.
- **The CRD `SharedAsset` spells the destination `path` where §5.1 uses `destPath`.** The spelling drift is new. The missing `type: artifact` delivery is already recorded by the F-6.4.3 resolution as a gateway-side follow-on, so REC-1 cites that record and files only the spelling drift. Both are outside the git-store scope.
- **Admin-API `sharedAssets` do not reach pods.** The sandbox controller encodes the pod flag from the CRD only. CODE-2 mirrors CRD entries into `runtimestore`, which closes the opposite direction only.
- **The spec contradicts itself on coding-agent egress.** §26.2 **Egress profile.** says `restricted` allows HTTPS to an allowlist that includes the operator's Git hosts and names an `egressProfile: unrestricted` value, while the §13.2.6 `egressProfile` enum limits `restricted` to the gateway and the DNS proxy and has no `unrestricted` value. The in-pod git credential helper (`cmd/git-credential-lenny`, gateway tool `lenny/vcs_token`, F-26.2.5) therefore has no route to a Git host under the default profile. Resolving the egress model is outside this proposal, which adds no pod egress.
- **The cold `gitClone` path admits symlinks whatever the Runtime's `allowSymlinks`.** `extractGitCloneSource` passes `AllowSymlinks: true`, while §13.4 and §7.4 reject symlinks unless the Runtime opts in. A cache-served source follows §13.4 (§14 rule 2).
- **§6.4 claims a kernel-level `EROFS` boundary for `/workspace/shared/` on every pod.** In embedded adapter mode the single container mounts it read-write. SPEC-2 states the embedded case for repository stores only; the general `/workspace/shared/` sentence is left for a separate change.
- **A derived Runtime never falls back to its base Runtime's pool.** `ResolvePool` (`pkg/gateway/podlifecycle/podsession/resolve.go`) matches a pool only when its template's `runtimeRef` equals the session's Runtime, so the §5.1 **Derived Runtime Instantiation** fallback to the base Runtime's pool is not implemented.
- **The §4.4 512Mi default for `workspaceSizeLimitBytes` is not implemented.** `resolve.go` leaves an unset template value at 0, the sandbox controller renders no `--workspace-size-limit-bytes`, and `checkpoint.WorkspaceSizePreCheck` treats 0 as disabled for both the pre-checkpoint probe and the §7.3 Resume pre-extraction check. The proposal applies the default only to its own checks (CODE-5 step 1).

## Impacts on other proposals

| Proposal | Status | Effect of this change | Required action |
|:--|:--|:--|:--|
| 0086 | Approved | 0111 applies after 0086. SPEC-3 Edit (b) edits the §14 `gitClone.ref` bullet and the §14.1 **Gateway reconciliation (live consumer):** bullet that 0086 SPEC-1 Edits (c) and (d) and SPEC-2 Edit (c) edit, on different text, and 0111 reads only "the pinned `resolvedCommitSha`", so it composes with either pin wording. 0086 SPEC-1 Edit (e) deletes the §14 per-slot scope note, and 0111 stages nothing there. | None for 0086. 0111's implementor applies after 0086 lands. |
| 0085 | Approved | Both edit §6.2. 0085 leaves the **Client visibility:** bullet unchanged, and SPEC-6 Edit (b) replaces a quoted substring of it, so the edits compose. | None. |
| 0109 | Approved (2026-10-29) | Both edit the §7.4 bullet "Archive extraction is especially strict."; 0109 SPEC-1 Edit (b) rewrites its opening words and SPEC-4 Edit (c) anchors on later text, so they compose. 0109's bind-time `FinalizeWorkspace` restore seeds a captured tree that can carry slot repositories with `lenny.gitStoreUrl`, and §14 rule 6 runs on that tree, so restore completion can fail the first bind of a derived or replayed session. 0109 SPEC-1 Edit (a), the **Derive restore semantics:** paragraph, lists the causes that fail the `FinalizeWorkspace` stage ("When the gateway cannot resolve the row, ... or the adapter cannot fetch, decode, or extract a chunk"), and that list does not name a restore-completion failure. Rule 6 states its failure as a failed resume and does not name a derive's first bind. A replay onto a target Runtime that does not name the url fails with `PERMISSION_DENIED` when the restored repository carries `lenny.storeCommit` (see open decision 8). 0109's chunk restore, its `restore_chunks` presign, SPEC-1 Edits (b) to (d), and SPEC-2 to SPEC-7 are unaffected. Both also edit the **Gateway-mediated file delivery** paragraph of `docs/getting-started/concepts.md`: 0111 DOCS-1 rewrites the "no git-based workspace population" sentence, and 0109 DOC-1 replaces "and on resume `GET`s them from" in the next sentence, so the edits compose in either order. 0109's tier-11 mirror assertion on that substring is unaffected. | Proceeding re-spends 0109's convergence, so the human decides whether to accept these effects or change rule 6 (open decision 8). If they are accepted, whichever proposal lands second calls 0111's restore completion after 0109's restore seed and before the plan's sources apply (CODE-6 step 3), adds a restore-completion failure to the causes in 0109's **Derive restore semantics:** paragraph, and states a rule 6 failure at that seed as a failed bind. For the docs paragraph, whichever lands second keeps the "Checkpoint chunk objects are the one exception" sentence consistent with the repository-store fill that DOCS-1 describes; the docs overlap adds no decision. |
| 0077 | Early draft (single file, never reviewed) | 0111 re-derives and supersedes it. | None. 0077 stages nothing; it may be withdrawn. |

## Deliverable index

- SPEC-1 — spec/05_runtime-registry-and-pool-model.md — §5.1 Standalone Runtime example comment and `gitRepository` entry, and the merge-table per-url uniqueness rule.
- SPEC-2 — spec/06_warm-pod-model.md — §6.4 population-sentence enumeration and the new **Repository stores.** paragraph.
- SPEC-3 — spec/14_workspace-plan-schema.md, spec/26_reference-runtime-catalog.md — §14 `gitClone` row cells, the §14 and §14.1 replay wording, the **Cache-served `gitClone` sources.** paragraph with rules 1 to 6, and the §26.2 workspace-layout bullet deletions.
- SPEC-4 — spec/13_security-model.md, spec/07_session-lifecycle.md, spec/02_goals-and-non-goals.md — §13.4 local-git exception and archive-validators pointer, the §7.4 pointer, and the §2 Non-Goals pointer.
- SPEC-5 — spec/04_system-components.md — §4.7.1 Adapter → Gateway `FetchGitPack` row.
- SPEC-6 — spec/15_external-api-surface.md, spec/06_warm-pod-model.md — §15.1 `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` row with `workspace_size_limit`, and the §6.2 **Client visibility:** pointer.
- SPEC-7 — spec/16_observability.md — §16.1 git pack bytes and cache-served source metric rows.
- CODE-1 — Dockerfile.git, Dockerfile, .github/workflows/release.yml, scripts/cloud/aws/build-images.sh, image-build targets, BUILD-GAPS.md — the images of the binaries that run git carry it, and the missing-git defect is filed.
- CODE-2 — pkg/apis, CRD manifests, pkg/gateway/runtime/runtimestore, pkg/gateway/externalapi/admin, pkg/controller, pkg/adapter/sharedassets — `gitRepository` from the CRD to the adapter flag and the registry, with registration validation.
- CODE-3 — schemas/lenny-adapter.proto, pkg/gateway/provisioning/gitref, the leasecontrol handler, cmd/lenny-gateway, pkg/observability/metrics, charts/lenny, pkg/embedded/manifests — gateway mirror, tree validation, pack producer, `FetchGitPack` handler, stream interceptors, metrics, and the chart.
- CODE-4 — cmd/lenny-adapter, pkg/adapter/gitstore.go, pkg/adapter/server.go, pkg/adapter/gatewaylink.go — adapter warm fill before READY.
- CODE-5 — pkg/gateway/podlifecycle/podsession, pkg/upload, pkg/gateway/sessionserver, pkg/adapter/workspace, schemas/workspaceplan-v1.json — binder routing and slot-repository construction.
- CODE-6 — pkg/adapter/checkpoint.go, pkg/adapter/resume.go, pkg/adapter/gitstore.go, pkg/adapter/scrub — capture copy and restore completion.
- INFRA-1 — tests/tier7a_load_local/gitcachespike, BUILD-PROGRESS.md, deviations file — gating spike with a pass criterion and recorded results.
- TEST-1 — tests across tiers 1, 2, 3, 4, 5, 7a, and 9 — case list for every staged rule.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — filing of the defects this proposal does not stage.
