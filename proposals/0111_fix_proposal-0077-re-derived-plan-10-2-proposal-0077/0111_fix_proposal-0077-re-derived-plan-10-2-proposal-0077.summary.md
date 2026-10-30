# Summary: Warm git object stores on sharedAssets, served from a gateway mirror over one GatewayControl RPC

## Summary

**Problem statement.** The gateway clones every `gitClone` source cold on each bind, archives the whole clone in memory, and runs it through the §13.4 upload archive validators (256 MiB decompressed, 10 000 entries) before the adapter receives extracted files. A very large repository therefore fails the session with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` or on the 5-minute clone timeout, and a repository within the limits pays a full clone on every bind. The owner's binding direction (plan entry "Proposal 0077, re-derived", 2026-10-28) is a pod-wide read-only bare object store filled at warm time, and a per-slot repository over git alternates that obtains only the delta to the pinned `resolvedCommitSha`, with a fallback, no worktrees against the store, additive-only store mutation, and no setup pre-running.

**What changes.**

- The Runtime `sharedAssets` list gains a `gitRepository` entry type, and §6.4 gains the **Repository stores.** paragraph for the warm fill, its failure mode, and store immutability (SPEC-1, SPEC-2, CODE-2, CODE-4).
- A `gitRepository` entry with `auth` caches a private repository for the tenant that owns the pool. §6.4 gains **Private repository stores.**, and pool records gain `ownerTenantId` (SPEC-2, SPEC-9, CODE-2, CODE-3, CODE-5, CODE-8).
- GatewayControl gains one adapter-initiated server-streaming RPC, `FetchGitPack`, served from a per-replica gateway mirror (SPEC-5, CODE-3).
- §14 gains **Cache-served `gitClone` sources.**: eligibility, tree validation with a materialization bound, slot-repository construction, the capture copy, and restore completion (SPEC-3, CODE-5, CODE-6).
- §13.4 gains a scoped exception for the adapter's local git on gateway-produced packs and agent-written slot repositories, with §7.4 and §2 pointers; §15.1 gains the `workspace_size_limit` reason; §16.1 gains two gateway metrics (SPEC-4, SPEC-6, SPEC-7).
- The images of the binaries that run git ship a git binary (CODE-1).
- A spike gates the code steps, and the tests and defect filings close the proposal (INFRA-1, TEST-1, REC-1).
- §4.9 gains a built-in `git-https` credential provider for any HTTPS Git host and provider-independent host-to-pool binding, and §14, §15.1, §26.2, and §26.3 cite §4.9 for the built-in providers (SPEC-8, CODE-7).

**Decisions.**

1. The gateway performs every git network operation. The pod runs only local git. Default `restricted` egress, the base NetworkPolicy, the §14 gateway-clone sentence, and §23's gateway-mediated delivery sentence stay true and are not edited.
2. The store is a new `type: gitRepository` value on `sharedAssets`, with `url`, `gitRef`, and `destPath`. sharedAssets already supplies the warm-time read-only volume, the destPath merge rule, and the controller-to-adapter flag. The key is `gitRef` because `ref` already means a `lenny-blob://` URI on artifact entries.
3. The store is immutable after population. This settles additive-only mutation, gc, prune, and refresh by construction; a fresher store arrives with a replacement pod.
4. Private repositories are cached for the one tenant that owns the pool, under §6.4 **Private repository stores.** (SPEC-2 Edit (c)). Ownership is enforced at pool resolution. The §5.2 tenant pin is not stamped at warm time, because §5.2 counts pinned idle pods apart from `minWarm`, and a pool whose pods were pinned at warm time would never report warm. The token comes from the `vcscred.Resolver` that the cold `gitClone` path calls. That resolver reads the credential's Secret through the Token Service on each call and creates no §4.9 lease record, so rule 4's resolution on every operation is what stops service after a revocation. Warm pods are not recycled on revocation. They hold no token, only the owning tenant reaches them, and session creation already runs the authenticated `ls-remote` for an `auth`-bearing source.
5. A per-replica gateway bare mirror per mirror key (§6.4 **Private repository stores.** rule 4), fetched with `fsckObjects`, writing no durable state. A stateless relay of a remote pack cannot validate the tree of `resolvedCommitSha` (it lacks the objects the haves exclude), so it would drop today's symlink-target validation; the mirror also turns N warm fills into one remote fetch per replica. The mirror's `emptyDir` has no `sizeLimit`, because the kubelet evicts a pod that exceeds it rather than failing the write, so the in-process budget (CODE-3 step 7 rule M2) bounds the volume. The container requests that budget as ephemeral storage, so the scheduler reserves it.
6. The binder validates the whole tree of `resolvedCommitSha` in the mirror at materialization, because the commit is pinned for the session and restore performs no checkout. `FetchGitPack` therefore serves objects only.
7. Packs are non-thin and produced by `pack-objects` from the mirror. The pod indexes them with `git index-pack --strict` under the §13.4 exception (SPEC-4).
8. The no-have fallback is a shallow pack of `resolvedCommitSha`'s tree, and a delta pack larger than that tree's blob and tree bytes is replaced by the shallow pack. Together with the materialization bound this keeps a cache-served source inside `workspaceSizeLimitBytes` at materialization; a full-history fallback would let a large repository pass the tree check and then evict the pod. A shallow slot repository has no history before the pin, which is accepted.
9. The slot repository is `git init` plus an absolute alternates entry, the indexed pack, and a detached checkout. `git clone --shared` is not used, because it copies store refs into the slot, which then reference history a replacement pod may lack.
10. The adapter copies store-only objects into the slot before every capture, twice (before the size probe and immediately before the archive), and restore runs a connectivity check bounded by the session delta. Without the copy, a session commit that reuses a store-only blob restores as a corrupt repository; the second copy and the check close the window a runtime that is not paused leaves.
11. Restore completion runs in the adapter after the workspace is restored and calls `FetchGitPack` keyed by the slot-local config §14 rule 3 records. Resume gains no field and no PrepareWorkspace step. For a derived or replayed session, the gateway authorizes the fetch against the pool of the session `parent_workspace_ref` names (SPEC-5), because a restored repository carries `lenny.storeCommit` only when the capturing pod held a store for its url. A derived session's plan carries no source to clone, and a clone archive would overwrite the restored repository.
12. The binder decides eligibility from current Runtime configuration. The adapter admits any well-formed eligible source and uses its store only to choose alternates and haves, so configuration skew takes the shallow fallback through the same RPC and no handshake field is needed.
13. A warm-fill failure, including volume exhaustion and a fill deadline, takes the existing §6.2 warm-fill edge to `failed`; on a `preConnect` pool the existing `sdk_connecting` watchdog also bounds the fill. The store has no size field and no fill-timeout Runtime field.
14. `FetchGitPack` requires the checks `ReportPodScrub` requires, and also requires `podId` to name the pod its SA token was issued for (§4.7.1), because a private store's objects would otherwise reach any holder of a pod certificate.
15. No §28 link, channel, or register row. Adapter-initiated GatewayControl RPCs carry no channel register entry; `FetchGitPack` is a §4.7.1 Adapter → Gateway row.
16. Two gateway-emitted metrics, because §16.1 places adapter-emitted metrics outside the §16.9 default scrape target set. `kind` is classified by what the handler observes, restore traffic is recorded with materialization traffic, and no hit count is derived from the metrics. An exact materialization hit count would need a request purpose field, which this proposal does not add.
17. The size and path checks surface through `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with the existing sub-codes plus `workspace_size_limit`, the spelling §4.4 already uses for the same limit.
18. Mirror work runs under its own per-replica fetch and pack limiters (CODE-3 step 7 rules M3 and M4) rather than the Upload Handler subsystem, so an upload outage does not stop cache-served materialization. The fetch limiter bounds the gateway's memory and the mirror's disk independently of how many keys a replica fetches at once, a number that grows with each tenant's private repositories and that the chart cannot know.
19. 0111 sequences after proposal 0086 and stages no edit to the §14 per-slot shared-template scope note or the pin-timing sentences.
20. A `git-https` credential is one Secret in the `kubernetes.io/basic-auth` layout (`username` and `password`). A deploy token's username and token rotate together, the §4.9 secret-per-credential topology and per-credential revocation apply unchanged, and the pool needs no new field, column, admin-API property, or chart value. The `leaseScope` pattern already admits the provider name. Lenny mints nothing from a host's API, so the lease TTL is synthetic.
21. A host binds to one VCS pool across providers, as §4.9 and the §15.1 `GIT_CLONE_AUTH_HOST_AMBIGUOUS` row already state, and a `leaseScope` provider is checked against the bound pool. The in-pod credential helper knows only the host, so this lets `lenny/vcs_token` resolve a `git-https` pool without a provider argument, and session creation and the helper always bind the same pool. The per-provider filter in `ResolveVCSPool` and §14's per-provider sentence contradicted §4.9; SPEC-8 and CODE-7 align them.

**Watch out for.**

- GatewayControl's authentication chain is registered only as unary interceptors. `FetchGitPack` is the service's first server-streaming RPC and bypasses both checks unless CODE-3 registers stream interceptors.
- CRD `sharedAssets` are not mirrored into `runtimestore` today. Without CODE-2's `applyCRDFields` change, every `FetchGitPack` call is refused and every warm fill fails.
- `index-pack --strict` checks commit parents. On a shallow pack the `shallow` file must be written before indexing.
- `ValidateTree` results depend on the pool's limit. Memoizing the pass/fail verdict lets one pool's larger limit skip another pool's check.
- `/workspace/shared/` is read-write in embedded adapter mode, so a test asserting `EROFS` belongs to the sidecar model only.
- The default `workspaceSizeLimitBytes` (512Mi) refuses most very large trees with `workspace_size_limit`. A deployer raises the pool limit for those repositories, and the spike sets it explicitly.
- CODE-3 step 6 reads the pod name from the TokenReview's `user.extra["authentication.kubernetes.io/pod-name"]`. Whether every supported Kubernetes version fills it for projected tokens is unverified; TEST-1 item 21 runs it on Kind.

## Goals

- A session on a pool whose Runtime names a repository as a `gitRepository` shared asset obtains that repository from a warm store plus a delta, with no gateway clone and no archive.
- A private repository's objects reach only pods that the owning tenant's sessions claim.
- A cache-served source never reaches the §13.4 archive ceilings, and a tree too large for the pool fails deterministically at materialization.
- A checkpoint of an alternates-backed repository either restores intact or fails the resume, and never restores with a missing object.
- The pod gains no network route, no credential, and no ability to write a store it shares with other slots.
- Operators can observe pack sizes by kind and the number of cache-served sources.
- A private repository on any HTTPS Git host is cloned, pinned, and served to the in-pod credential helper through a `git-https` pool, with no custom provider code.

## Non-goals

- Stamping the §5.2 tenant pin on a tenant-owned pool's pods at warm time, and recycling warm pods when a VCS credential is revoked (see decision 4).
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
- Honoring `depth` or `submodules` on cache-served sources. Such sources keep the existing gateway path.
- Moving every `gitClone` onto the pack path and retiring `CloneArchive`. That exceeds the warm-cache scope and remains a candidate follow-up.
- Scoping the §13.4 archive ceilings away from `gitClone` and streaming the clone in place of the warm cache. The owner's direction is binding; cold-path limits for ineligible sources are a possible separate proposal.
- Raising the §13.4 archive ceilings globally. That weakens protection against client upload bombs.
- Adapter-emitted metrics.
- A new error code for size or path violations.
- Minting short-lived tokens from a Git host's API for `git-https`. The pool holds a deployer-managed token.
- A chart seed for a default `git-https` pool. `bootstrap.credentialPools` accepts the entry.
- Editing the §14 per-slot shared-template scope note or the `resolvedCommitSha` pin-timing sentences in §14 and §15.1.

## Open decisions for human to make

None open. The decisions taken on 2026-10-30 are below; the redesign run stages them.

- **Private repositories are in the first version (owner decision).** A warm pool becomes tenant-owned only when its Runtime caches a private repository; every other pool stays platform-global. The gateway fills the store by leasing a read token from the owning tenant's existing VCS credential pool (§4.9) through the same credential-lease mechanism `gitClone` uses today, so no new credential is stored and no token reaches the pod. The gateway keeps one mirror per (tenant, url) for a private repository and one shared mirror per url for a public repository. Only the owning tenant's sessions claim a tenant-owned pod.
- **Any HTTPS git host (owner decision).** A built-in generic `git-https` credential provider (a username plus a token or password from a `secretRef`) joins `github` in §4.9, so private repositories on GitLab, Bitbucket, Gitea, or a self-hosted server work on the cold path (clone, `ls-remote` pinning, the in-pod credential helper) and on the warm path, with no custom provider code.
- **SSH stays deferred (owner decision),** as §14 and §21.9 state.
- **`/start` client visibility (orchestrator, under delegation):** the §6.2 `/start` clause is widened in SPEC-6 Edit (b), so a cache-served refusal surfaces as its non-retryable §15.1 code.
- **Warm-fill deadline (orchestrator):** `--git-store-fill-timeout` is rendered into the adapter arguments from a controller-wide chart value, so it is operator-tunable as CODE-4 states.
- **Capture failure from agent-written slot content (orchestrator):** the edge-case list gains that row.
- **Derive or replay onto a Runtime that does not name the repository (orchestrator):** restore completion obtains `lenny.baseCommit` from the gateway instead of failing with `PERMISSION_DENIED` (SPEC-5; decision 11), following the rule that a cache problem falls back to a correct cold clone.

## Defects in the shipped tree that this proposal does not stage

- **GatewayControl does not bind the pod to the client certificate.** `RequireVerifiedPeerInterceptor` checks only that the chain verified, and `ReportPodScrub` takes the pod from the request's `pod_id`. `FetchGitPack` closes the gap for itself (§4.7.1), and every other GatewayControl RPC keeps it. Binding GatewayControl identity to the peer's SPIFFE identity is a change to every pod-scoped RPC and stays out of scope.
- **The CRD `SharedAsset` spells the destination `path` where §5.1 uses `destPath`.** The spelling drift is new. The missing `type: artifact` delivery is already recorded by the F-6.4.3 resolution as a gateway-side follow-on, so REC-1 cites that record and files only the spelling drift. Both are outside the git-store scope.
- **Admin-API `sharedAssets` do not reach pods.** The sandbox controller encodes the pod flag from the CRD only. CODE-2 mirrors CRD entries into `runtimestore`, which closes the opposite direction only.
- **The spec contradicts itself on coding-agent egress.** §26.2 **Egress profile.** says `restricted` allows HTTPS to an allowlist that includes the operator's Git hosts and names an `egressProfile: unrestricted` value, while the §13.2.6 `egressProfile` enum limits `restricted` to the gateway and the DNS proxy and has no `unrestricted` value. The in-pod git credential helper (`cmd/git-credential-lenny`, gateway tool `lenny/vcs_token`, F-26.2.5) therefore has no route to a Git host under the default profile. Resolving the egress model is outside this proposal, which adds no pod egress.
- **The cold `gitClone` path admits symlinks whatever the Runtime's `allowSymlinks`.** `extractGitCloneSource` passes `AllowSymlinks: true`, while §13.4 and §7.4 reject symlinks unless the Runtime opts in. A cache-served source follows §13.4 (§14 rule 2).
- **§6.4 claims a kernel-level `EROFS` boundary for `/workspace/shared/` on every pod.** In embedded adapter mode the single container mounts it read-write. SPEC-2 states the embedded case for repository stores only; the general `/workspace/shared/` sentence is left for a separate change.
- **A derived Runtime never falls back to its base Runtime's pool.** `ResolvePool` (`pkg/gateway/podlifecycle/podsession/resolve.go`) matches a pool only when its template's `runtimeRef` equals the session's Runtime, so the §5.1 **Derived Runtime Instantiation** fallback to the base Runtime's pool is not implemented.
- **The §4.4 512Mi default for `workspaceSizeLimitBytes` is not implemented.** `resolve.go` leaves an unset template value at 0, the sandbox controller renders no `--workspace-size-limit-bytes`, and `checkpoint.WorkspaceSizePreCheck` treats 0 as disabled for both the pre-checkpoint probe and the §7.3 Resume pre-extraction check. The proposal applies the default only to its own checks (CODE-5 step 1).
- **The `github` provider does not mint GitHub App installation tokens.** §4.9 states App credentials yielding a short-lived installation token with a one-hour ceiling, while `vcscred.StoreResolver` reads a deployer-stored token from the Secret's `token` key and pairs it with `x-access-token` (`pkg/gateway/provisioning/vcscred/vcscred.go`). CODE-7 keeps that behavior for `github`.
- **`hostPatterns` is not required at pool registration.** §4.9 makes it required on VCS pools, and the admin handlers accept a VCS pool without it (`pkg/gateway/externalapi/admin/credential_pools.go`). Such a pool binds no host, so resolution fails closed with `GIT_CLONE_AUTH_UNSUPPORTED_HOST`.
- **The cold `gitClone` clone has no concurrency or disk bound.** `extractGitCloneSource` calls `gitref.CloneArchive` before it enters `UploadGate`, and `CloneArchive` holds the whole archive in memory (`pkg/gateway/provisioning/gitref/archive.go`). CODE-3 step 9 gives the clone a writable directory outside the mirror budget and does not bound its use.
- **The Token Service cannot read credential Secrets on a chart install.** §4.9 requires the `lenny-token-service-secrets` Role, and the chart does not render it: the admin handler only emits a `kubectl patch role` for it (`pkg/gateway/externalapi/admin/credential_pools.go`). §13.2.4 and `system-network-policies.yaml` also give the Token Service no kube-apiserver egress. The §4.9 live probe and the CODE-7 `ReadCredentialSecret` path both depend on these.

## Impacts on other proposals

| Proposal | Status | Effect of this change | Required action |
|:--|:--|:--|:--|
| 0086 | Approved | 0111 applies after 0086. SPEC-3 Edit (b) edits the §14 `gitClone.ref` bullet and the §14.1 **Gateway reconciliation (live consumer):** bullet that 0086 SPEC-1 Edits (c) and (d) and SPEC-2 Edit (c) edit, on different text, and 0111 reads only "the pinned `resolvedCommitSha`", so it composes with either pin wording. SPEC-8 Edit (b) anchors its replacements on text after the sentence opening that 0086 SPEC-2 Edit (d) rewrites in the §4.9 **`hostPatterns`** paragraph, so it composes with that edit and keeps its **Plan binding.** timing clause. 0086 SPEC-1 Edit (e) deletes the §14 per-slot scope note, and 0111 stages nothing there. | None for 0086. 0111's implementor applies after 0086 lands. |
| 0085 | Approved | Both edit §6.2. 0085 leaves the **Client visibility:** bullet unchanged, and SPEC-6 Edit (b) replaces a quoted substring of it, so the edits compose. | None. |
| 0109 | Approved (2026-10-29) | Both edit the §7.4 bullet "Archive extraction is especially strict."; 0109 SPEC-1 Edit (b) rewrites its opening words and SPEC-4 Edit (c) anchors on later text, so they compose. 0109's bind-time `FinalizeWorkspace` restore seeds a captured tree that can carry slot repositories with `lenny.gitStoreUrl`, and §14 rule 6 runs on that tree, so restore completion can fail the first bind of a derived or replayed session. 0109 SPEC-1 Edit (a), the **Derive restore semantics:** paragraph, lists the causes that fail the `FinalizeWorkspace` stage ("When the gateway cannot resolve the row, ... or the adapter cannot fetch, decode, or extract a chunk"), and that list does not name a restore-completion failure. Rule 6 states its failure as a failed resume and does not name a derive's first bind. 0109's chunk restore, its `restore_chunks` presign, SPEC-1 Edits (b) to (d), and SPEC-2 to SPEC-7 are unaffected. Both also edit the **Gateway-mediated file delivery** paragraph of `docs/getting-started/concepts.md`: 0111 DOCS-1 rewrites the "no git-based workspace population" sentence, and 0109 DOC-1 replaces "and on resume `GET`s them from" in the next sentence, so the edits compose in either order. 0109's tier-11 mirror assertion on that substring is unaffected. | Whichever proposal lands second calls 0111's restore completion after 0109's restore seed and before the plan's sources apply (CODE-6 step 3), adds a restore-completion failure to the causes in 0109's **Derive restore semantics:** paragraph, and states a rule 6 failure at that seed as a failed bind. For the docs paragraph, whichever lands second keeps the "Checkpoint chunk objects are the one exception" sentence consistent with the repository-store fill that DOCS-1 describes; the docs overlap adds no decision. |
| 0077 | Early draft (single file, never reviewed) | 0111 re-derives and supersedes it. | None. 0077 stages nothing; it may be withdrawn. |

## Deliverable index

- SPEC-1 — spec/05_runtime-registry-and-pool-model.md — §5.1 Standalone Runtime example comment and `gitRepository` entry, and the merge-table per-url uniqueness rule.
- SPEC-2 — spec/06_warm-pod-model.md — §6.4 population-sentence enumeration and the new **Repository stores.** and **Private repository stores.** paragraphs.
- SPEC-3 — spec/14_workspace-plan-schema.md, spec/26_reference-runtime-catalog.md — §14 `gitClone` row cells, the §14 and §14.1 replay wording, the **Cache-served `gitClone` sources.** paragraph with rules 1 to 6, and the §26.2 workspace-layout bullet deletions.
- SPEC-4 — spec/13_security-model.md, spec/07_session-lifecycle.md, spec/02_goals-and-non-goals.md — §13.4 local-git exception and archive-validators pointer, the §7.4 pointer, and the §2 Non-Goals pointer.
- SPEC-5 — spec/04_system-components.md — §4.7.1 Adapter → Gateway `FetchGitPack` row.
- SPEC-6 — spec/15_external-api-surface.md, spec/06_warm-pod-model.md — §15.1 `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` row with `workspace_size_limit`, and the §6.2 **Client visibility:** pointer.
- SPEC-7 — spec/16_observability.md — §16.1 git pack bytes and cache-served source metric rows.
- SPEC-8 — spec/04_system-components.md, spec/14_workspace-plan-schema.md, spec/15_external-api-surface.md, spec/26_reference-runtime-catalog.md — §4.9 `git-https` provider rows, the **`hostPatterns`** rules, the **`git-https` credentials.** paragraph, and the §14, §15.1, §26.2, and §26.3 pointers.
- SPEC-9 — spec/04_system-components.md, spec/05_runtime-registry-and-pool-model.md — §4.2 `Runtimes, pools` table row and §5.2 pool-dimensions bullet.
- CODE-1 — Dockerfile.git, Dockerfile, .github/workflows/release.yml, scripts/cloud/aws/build-images.sh, image-build targets, BUILD-GAPS.md — the images of the binaries that run git carry it, and the missing-git defect is filed.
- CODE-2 — pkg/apis, CRD manifests, pkg/gateway/runtime/runtimestore, pkg/gateway/externalapi/admin, pkg/controller, pkg/adapter/sharedassets — `gitRepository` from the CRD to the adapter flag and the registry, with registration validation.
- CODE-3 — schemas/lenny-adapter.proto, pkg/gateway/provisioning/gitref, the leasecontrol handler and SA token verifier, cmd/lenny-gateway, pkg/observability/metrics, charts/lenny, pkg/embedded/manifests — gateway mirror, tree validation, pack producer, `FetchGitPack` handler, stream interceptors, metrics, and the chart.
- CODE-4 — cmd/lenny-adapter, pkg/adapter/gitstore.go, pkg/adapter/server.go, pkg/adapter/gatewaylink.go — adapter warm fill before READY.
- CODE-5 — pkg/gateway/podlifecycle/podsession, pkg/upload, pkg/gateway/sessionserver, pkg/adapter/workspace, schemas/workspaceplan-v1.json — binder routing and slot-repository construction.
- CODE-6 — pkg/adapter/checkpoint.go, pkg/adapter/resume.go, pkg/adapter/gitstore.go, pkg/adapter/scrub — capture copy and restore completion.
- CODE-7 — pkg/credential, pkg/gateway/credentials/credentialpoolstore, pkg/gateway/provisioning/vcscred, pkg/gateway/mcpfabric/mcptools, charts/lenny, schemas/lenny-tokenservice.proto, pkg/tokenservice, cmd/lenny-gateway — the `git-https` provider, host binding, `lenny/vcs_token`, and the Token Service credential Secret read.
- CODE-8 — migrations, pkg/gateway/runtime/poolstore, pkg/gateway/externalapi/admin, pkg/gateway/externalapi/openapi, pkg/gateway/podlifecycle/podsession/resolve.go and its callers — the pool owning tenant, its admission, and the resolution rule.
- INFRA-1 — tests/tier7a_load_local/gitcachespike, BUILD-PROGRESS.md, deviations file — gating spike with a pass criterion and recorded results.
- TEST-1 — tests across tiers 1, 2, 3, 4, 5, 7a, and 9 — case list for every staged rule.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — filing of the defects this proposal does not stage.
