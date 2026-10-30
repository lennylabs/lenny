# Spec changes — Pre-materialize large git repositories on a warm pod

## Design (as the spec must state it)

The specification states the mechanism in the homes below, and every other site cites one of them.

- **§6.4 Repository stores.** (SPEC-2) owns the `gitRepository` shared-asset entry: its fields, the url normalization, the warm fill before ready, the warm-fill failure mode, store immutability, and scrub survival.
- **§6.4 Private repository stores.** (SPEC-2 Edit (c)) owns the private entry: its declaration, the owning tenant, the claim rule, the mirror key and credential, and the pod-identity prerequisite.
- **§4.7.1 `FetchGitPack`** (SPEC-5) owns the RPC contract: authorization, ref resolution, the pack the gateway returns for a ref, for a commit with usable haves, and for a commit without them, and the bound that decides between a delta pack and a shallow pack.
- **§14 Cache-served `gitClone` sources.** (SPEC-3) owns the session half as numbered rules: eligibility (rule 1), tree validation and the materialization bound (rule 2), slot-repository construction (rule 3), the worktree prohibition (rule 4), the capture copy (rule 5), restore completion (rule 6), and the restore lineage (rule 7).
- **§13.4** (SPEC-4) owns the scoped exception that lets the adapter run local git on a gateway-produced pack and on an agent-written slot repository, with pointers in §7.4 and §2.
- **§4.9 Credential Pool** (SPEC-8) owns VCS credential binding: the **`hostPatterns`** rules and the **`git-https` credentials.** paragraph. §14 `gitClone.auth`, §15.1, §26.2, and §26.3 cite it. §4.9 **Token Service secret resolution.** (SPEC-8 Edit (h)) owns how the Token Service reads a credential Secret, what a failed read does, and which Secrets it returns to the gateway. §4.9 **RBAC for the Token Service.** rules 1 to 3 (SPEC-8 Edit (l)) own `secretRef` resolution and the Token Service Secret Role, and the §4.9 live-probe, Tier 3, initial setup, and emergency revocation runbook paragraphs, §15.1, and §17.7 cite them. The §13.2.4 Token Service row (SPEC-8 Edit (i)) carries its kube-apiserver egress. §4.3 **High availability and failure handling:** (SPEC-8 Edit (j)) owns the effect of a Token Service outage on VCS credential use, and §4.6.1, §16.5, and §17.7 cite it.

SPEC-1 adds the entry type to the §5.1 Runtime example and merge table. SPEC-6 adds the `workspace_size_limit` reason to `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. SPEC-7 adds two gateway-emitted metrics. SPEC-9 adds the pool owning tenant to the §4.2 scoping table and the §5.2 pool dimensions. SPEC-10 adds the restore lineage key to §12.4 and the `REG-GITLINEAGE` row to §28.3, and adds the VCS credential Secret read to the §28.7 Token Service artifact row. SPEC-11 adds the §17.8.1 rows for the repository-store fill deadline and the gateway mirror limits.

The gateway performs every git network operation for a cache-served source. The pod runs only local git, under the §13.4 exception. Default `restricted` egress, the base NetworkPolicy, and the §14 gateway-clone sentence remain true and are not edited.

## Edge cases and accepted failure modes

Each case cites the rule that owns it.

- **A warm fill fails.** The pod takes `failed` (§6.4 **Repository stores.**).
- **The Runtime gains or loses a `gitRepository` entry after a pod warmed.** The binder decides eligibility from current configuration (§14 rule 1). A pod without a store for the url takes the shallow pack with no alternates (§14 rule 3), and a pod whose store url the Runtime no longer names receives `PERMISSION_DENIED` on `FetchGitPack` (§4.7.1). The denial fails the materialization, and it fails a bind or resume whose restore completion has to fetch for that url (§14 rule 6), unless the session's lineage pool serves it (§14 rule 7).
- **A store is older than the session's pinned commit.** The session obtains a larger delta, or the shallow pack when the delta exceeds the §4.7.1 bound. The tree is the same either way because git is content-addressed.
- **A tree exceeds the materialization bound.** The workspace materialization fails with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` and `details.reason: workspace_size_limit` (§14 rule 2).
- **A session commit reuses a blob that only the store holds** (a revert, or a cherry-pick by SHA). The capture copy moves the object into the slot (§14 rule 5).
- **A commit lands between the first capture copy and the archive on a runtime that is not paused.** The second copy covers it, and restore's connectivity check fails the bind or resume for any object still missing (§14 rules 5 and 6).
- **Agent-written slot content that the §13.4 exception rejects.** The capture, bind, or resume fails (§13.4 exception).
- **A checkpoint restores onto a pod whose store is at a different commit or absent.** Restore completion repoints or removes alternates and fetches `lenny.baseCommit`. When that pack is shallow, a repository whose refs, reflogs, or index reach an ancestor of `lenny.baseCommit`, as an amend or a reset of the pinned commit leaves, fails the bind or resume (§14 rule 6).
- **A derive or replay lands on a Runtime that does not name a restored repository's url.** Restore completion takes the no-store path (§14 rule 6), and `FetchGitPack` serves it through the session's lineage pool (§14 rule 7).
- **A derive from a session that has not checkpointed since it was derived, or from a snapshot whose session the gateway cannot read.** The lineage pool is that intermediate session's pool, or the session has none (§14 rule 7). A restore completion that has to fetch a url the lineage pool's Runtime does not name fails the bind or resume (§14 rule 6).
- **Two sessions restore onto one concurrent-session pod at once.** The pod's record holds both lineage pools, and `FetchGitPack` serves either slot under either pool (§14 rule 7). The §5.2 tenant pin keeps both sessions in one tenant.
- **Redis is unavailable when a derived or replayed session binds.** No lineage pool is recorded, and a restore completion that needs one fails the bind or resume (§12.4).
- **A restore's pack would exceed `workspaceSizeLimitBytes`.** The bind or resume fails (§14 rule 6).
- **A shallow slot repository.** A slot built from the shallow pack has no history before `resolvedCommitSha`. Git operations that walk earlier history (`git log` past the pin, `merge-base` with older commits) see a shallow repository. This is accepted for the first version.
- **Embedded adapter mode.** `/workspace/shared/` is writable by the single container, so store immutability rests on that container writing nothing after population (§6.4 **Repository stores.**).
- **The owning tenant's VCS credential is revoked, removed, or rebound.** Every later operation on the private mirror key is refused (§6.4 **Private repository stores.** rule 4). A fill takes `failed`, a materialization fails, and a restore completion that must fetch fails the bind or resume. Pods that already hold a store are not recycled.
- **A CRD apply gives a Runtime a private entry while a pool lacks `ownerTenantId`.** The mirror path does not check the Runtime against its pools (§6.4 **Private repository stores.** rule 2), and `FetchGitPack` refuses the fill (§4.7.1), so no pod of that pool reaches ready.
- **The gateway does not validate projected service-account tokens.** No private entry is registered or served, and a pool whose CRD-applied Runtime carries one never reaches ready (§6.4 **Private repository stores.** rule 5).
- **The git host revokes the tenant's access while its Lenny credential stays valid.** New sessions fail the authenticated ref pinning at creation (§14). A commit already in the mirror stays servable to the owning tenant's existing sessions.
- **A `github` pool and a `git-https` pool both match a host.** Session creation and `lenny/vcs_token` fail with `GIT_CLONE_AUTH_HOST_AMBIGUOUS` (§4.9 **`hostPatterns`** rule 2).
- **A `gitClone` source names `vcs.github.read` for a host only a `git-https` pool matches.** Session creation fails with `GIT_CLONE_AUTH_UNSUPPORTED_HOST` (§4.9 **`hostPatterns`** rule 3).

## Staged edits

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.1 Runtime

**Edit (a), Standalone Runtime YAML example.** In the code block under the Standalone Runtime example, replace the comment on the `sharedAssets:` line:

```yaml
sharedAssets:  # files populated into /workspace/shared/ (read-only) by the adapter at warm time, before READY; mounted and populated on every pod
```

with

```yaml
sharedAssets:  # files and repository stores populated into /workspace/shared/ (read-only) by the adapter at warm time, before READY; mounted and populated on every pod
```

**Edit (b), same code block.** Insert after the `type: inline` entry (after the line `    content: '{"version": 1}'`) and before `labels:`:

```yaml
  - type: gitRepository  # HTTPS repository held as a read-only bare git object store; see §6.4
    url: "https://github.com/acme/monorepo.git"
    gitRef: main
    destPath: git/monorepo/
```

**Edit (c), Normative Merge Algorithm table, `sharedAssets` row.** In the Notes cell, after "The directory is mounted and populated on every pod.", append:

```markdown
 A `gitRepository` entry populates a read-only bare git object store at `destPath` ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**). After the merge, a Runtime's resolved `sharedAssets` list carries at most one `gitRepository` entry per `url`, compared after the normalization that paragraph defines, and registration rejects a Runtime whose resolved list carries two.
```

The row stays one physical line.

### SPEC-2 · spec/06_warm-pod-model.md § 6.4 Pod Filesystem Layout

**Edit (a), paragraph `**/workspace/shared/ population and enforcement.**`.** Replace

```markdown
a list of artifact references or inline file specs.
```

with

```markdown
a list of artifact references, inline file specs, and repository stores (**Repository stores.** below).
```

**Edit (b), new paragraph.** Insert directly after the paragraph that begins "**`/workspace/shared/` population and enforcement.**":

```markdown
**Repository stores.** A `sharedAssets` entry of `type: gitRepository` names an HTTPS repository `url`, under the `^https://` rule that applies to `gitClone.url` ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema)), a `gitRef` (a branch or tag name; registration rejects a `gitRef` that matches `^[0-9a-f]{40}$`), and a `destPath`. **Private repository stores.** below governs an entry that carries a credential. Wherever the platform compares repository URLs, it compares them after normalization: the scheme and host are lowercased, then one trailing `/` and then one trailing `.git` are removed from the path. After the adapter connects to the gateway and before the pod reports ready, the adapter populates `/workspace/shared/{destPath}` with a bare git repository. It builds the repository from the commit and pack that `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)) returns for `gitRef`, and it records that commit as the store's only ref. The adapter runs no git network operation. A fill that fails for any reason, including exhaustion of the node-local ephemeral storage that bounds the volume, or that does not complete within the adapter's fill deadline, takes the pod to `failed` on its warm-fill path ([Section 6.2](#62-pod-state-machine)). On a `preConnect` pool the fill runs in `sdk_connecting`, so the `sdk_connecting` watchdog ([Section 6.1](#61-what-a-pre-warmed-pod-looks-like)) also bounds it, and the deployer sets `sdkConnectTimeoutSeconds` to cover the fill. After population the adapter does not fetch into, repack, prune, or otherwise write a store, so a store holds the history of one commit for the life of the pod. In embedded adapter mode the runtime shares the adapter's container and `/workspace/shared/` is writable by that container, so a store's immutability there rests on the container writing nothing after population, as it does for every other shared asset. Neither the per-slot cleanup nor the whole-pod scrub ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) removes a store. [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** states how a session uses a store.
```

**Edit (c), new paragraph.** Insert directly after the **Repository stores.** paragraph:

```markdown
**Private repository stores.** A `gitRepository` entry that carries `auth` is private, and the following rules govern it and the pools whose Runtime carries one. An entry without `auth` is public.

1. **Declaration.** `auth` takes the form of the `gitClone.auth` object ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema)), and registration rejects a `leaseScope` whose access segment is not `read`.
2. **Owning tenant.** A pool whose Runtime's resolved `sharedAssets` carry a private entry is tenant-owned, and its `ownerTenantId` names an existing tenant. Registration rejects a pool write that omits `ownerTenantId` on such a pool, sets it on any other pool, or changes it after the pool is created. It rejects a `service`-mode pool whose Runtime carries a private entry. It rejects a Runtime write through the admin API or the bootstrap seed ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) after which a pool that resolves against that Runtime, or against a Runtime derived from it, breaks this rule. A Runtime write by a `tenant-admin` ([Section 10.2](10_gateway-internals.md#102-authentication)) that adds a private entry, or changes a private entry's `url` or `auth`, is rejected unless every pool that resolves against that Runtime, or against a Runtime derived from it, is owned by the caller's tenant. A Runtime applied as a CRD is mirrored without that check, and a pool it leaves without `ownerTenantId` is served no private entry ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) `FetchGitPack`). A tenant-owned pool is a pool under its owning tenant for the tenant deletion rule of [Section 15.1](15_external-api-surface.md#151-rest-api).
3. **Claim.** Pool resolution for a session treats a pool whose `ownerTenantId` is set as absent unless the session's tenant equals it. This holds for a pool the session names and on the resume and re-adoption paths.
4. **Mirror key and credential.** The gateway keeps one mirror per mirror key. The key is the normalized `url` for a public entry, and the pair of `ownerTenantId` and normalized `url` for a private entry. Before every operation that reads or fetches objects under a private key, including each `FetchGitPack` call ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)) and each [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 2 check, the gateway resolves the entry's `leaseScope` for the owning tenant by the [Section 4.9](04_system-components.md#49-credential-leasing-service) **`hostPatterns`** rules, as it does for an `auth`-bearing `gitClone` source, and refuses the operation when the resolution fails. The resolved credential is used only on the gateway's network path. It is not written to the mirror's configuration and does not reach the pod. Each gateway replica deletes its mirror of a private key once the key's owning tenant is not registered ([Section 10.2](10_gateway-internals.md#102-authentication)), within the [Section 12.9](12_storage-architecture.md#129-data-classification) erasure-on-deletion timeline for workspace files.
5. **Pod identity.** `FetchGitPack` serves a private entry only to a pod whose projected service-account token the gateway has validated as [Section 10.3](10_gateway-internals.md#103-mtls-pki) **Projected SA token:** requires. A gateway that does not validate that token, because `global.saTokenAudience` is unset or the gateway cannot verify the token's signature, refuses every `FetchGitPack` call for a private entry. On such a gateway, registration through the admin API or the bootstrap seed rejects a Runtime write whose resolved `sharedAssets` carry a private entry, with an error that names `global.saTokenAudience`.
```

### SPEC-3 · spec/14_workspace-plan-schema.md § 14 and § 14.1, spec/26_reference-runtime-catalog.md § 26.2, spec/07_session-lifecycle.md § 7.1, spec/04_system-components.md § 4.7.1

**Edit (a), sources table, `gitClone` row, optional-fields and Purpose cells.** In the optional-fields cell, replace `default full clone` with `` default full clone except as **Cache-served `gitClone` sources.** rule 3 states ``. In the Purpose cell, after "The gateway performs the clone using its network path, not the pod's, so the runtime never sees raw credentials." append:

```markdown
 A cache-served source (see **Cache-served `gitClone` sources.**) reaches the pod as a gateway-produced git pack rather than as extracted files.
```

Keep the existing sentence unchanged. The row stays one physical line.

**Edit (b), replay wording.** In the sentence that begins "All subsequent materializations for the same session (retries within `retryPolicy.maxResumeWindowSeconds`, resumes after pod eviction, checkpoint restores)", replace

```markdown
clone `resolvedCommitSha` rather than re-resolving `ref`
```

with

```markdown
materialize `resolvedCommitSha` rather than re-resolving `ref` (a cache-served source is materialized and completed as **Cache-served `gitClone` sources.** states)
```

In §14.1, in the **Consumer obligations by consumer type.** bullet that begins "- **Gateway reconciliation (live consumer):**", replace

```markdown
the gateway clones `sources[<n>].resolvedCommitSha`
```

with

```markdown
the gateway materializes `sources[<n>].resolvedCommitSha`
```

Both replacements compose with proposal 0086's SPEC-1 Edits (c) and (d) and SPEC-2 Edit (c), which replace different text in the same paragraphs.

**Edit (c), new paragraph.** Insert directly after the paragraph that begins "**`gitClone.auth` object.**" and before "**Field notes:**":

```markdown
**Cache-served `gitClone` sources.** A Runtime's `gitRepository` shared assets ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**) let a session obtain a large repository from a store on the pod rather than from a gateway clone. The following rules govern a cache-served source.

1. **Eligibility.** A `gitClone` source is cache-served when its normalized `url` equals the normalized `url` of a `gitRepository` entry in the resolved `sharedAssets` of the Runtime of the pod's pool, `submodules` is absent or `false`, and `depth` is absent. The gateway decides eligibility when it materializes the source on a pod, from the Runtime's configuration at that moment. Every other `gitClone` source is materialized by the gateway clone this section describes above. The adapter refuses a `gitClone` source that carries `depth` or `submodules: true`, or whose `resolvedCommitSha` does not match `^[0-9a-f]{40}$`.
2. **Tree validation.** Before the gateway forwards a cache-served source to the adapter, it fetches `resolvedCommitSha` into the entry's mirror ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** rule 4) and validates the commit's whole tree, placed under the slot root at the source's `path`. The tree is refused when an entry path fails the [Section 13.4](13_security-model.md#134-upload-security) path-traversal, path-depth, or path-length rule (`path_escapes_root`, `max_path_depth`, `max_path_length`); when an entry is a gitlink or has a mode other than regular file, executable file, directory, or symlink (`non_regular_entry`); or when a symlink fails the [Section 13.4](13_security-model.md#134-upload-security) symlink rule (`symlink`). The tree is also refused when its **materialization bound** exceeds the pool's `workspaceSizeLimitBytes` (`workspace_size_limit`). The materialization bound is the sum, over the tree, of each blob's size rounded up to a multiple of 4 096 bytes, each blob's size again, each tree object's size, and, for each tree entry, 128 bytes plus the byte length of the entry's path. A refusal fails the workspace materialization with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` and the named `details.reason` ([Section 15.1](15_external-api-surface.md#151-rest-api)). The other [Section 13.4](13_security-model.md#134-upload-security) archive validators do not apply, because no archive is unpacked. A cache-served source that passes this check does not exceed `workspaceSizeLimitBytes` at materialization, because the pack the adapter receives is bounded as [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) `FetchGitPack` states.
3. **Slot repository.** The adapter creates the repository's git directory with `git init` in a new directory outside the build tree, and every git operation of this rule runs with that git directory and with the source's `path` as the working tree. When the pod holds a store for the source's normalized `url`, the adapter writes the store's objects directory, as an absolute path, to `objects/info/alternates`. When `resolvedCommitSha` is not already reachable, the adapter calls `FetchGitPack` for `resolvedCommitSha`, naming the store's commit as the only have when it wrote alternates and naming none otherwise. When the response marks the pack shallow, the adapter writes `resolvedCommitSha` to the repository's `shallow` file before it indexes the pack. It indexes the pack with `git index-pack --strict`. When a pack requested with a have cannot be indexed, the adapter removes `objects/info/alternates` and requests the pack again with no have. The adapter records the normalized `url` as `lenny.gitStoreUrl`, `resolvedCommitSha` as `lenny.baseCommit`, and, unless the repository has no alternates or is shallow, the store's commit as `lenny.storeCommit` in the repository's config, and it checks out `resolvedCommitSha` detached. The adapter then moves the git directory to `.git` at the source's `path`, replacing any entry there, inside the build tree that promotion replaces `/workspace/slots/{sessionId}/current` with.
4. **No store worktrees.** No component creates a worktree of a store or uses `git clone --shared` from one. A runtime may create worktrees inside its own slot repository.
5. **Capture copy.** For each repository in the slot whose config carries `lenny.gitStoreUrl`, handled as the [Section 13.4](13_security-model.md#134-upload-security) exception states, the adapter copies into the repository's own object store every object reachable from its refs, reflogs, and index but not from `lenny.baseCommit` that the repository holds only through alternates. It runs the copy twice for every capture: before the pre-checkpoint workspace size probe ([Section 4.4](04_system-components.md#44-event--checkpoint-store) **Hard workspace size limit.**), so the probe counts the copied bytes, and again immediately before the workspace archive is written, which for a Full-level runtime is after quiescence. A failure of either copy fails the capture through the existing checkpoint failure path. A captured repository whose config carries `lenny.storeCommit` therefore lacks the objects reachable from `lenny.baseCommit` that only the capturing pod's store held, and only rule 6 completes it, so the same repository in a snapshot downloaded through `GET /v1/sessions/{id}/workspace` ([Section 15.1](15_external-api-surface.md#151-rest-api)) or supplied as an `uploadArchive` source is incomplete.
6. **Restore completion.** After the adapter restores a captured workspace tree, into a slot on a resume or into the build tree at a first bind ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**), and before the session's runtime starts or resumes, it completes each restored repository whose config carries `lenny.gitStoreUrl`, handled as the [Section 13.4](13_security-model.md#134-upload-security) exception states. It overwrites `objects/info/alternates` with the objects directory of the pod's store for that `url`, or deletes the file when the pod has none, and never reads the restored file's contents. When the config carries `lenny.storeCommit` and the pod holds no store for the `url` or holds one at a different commit, the adapter calls `FetchGitPack` for `lenny.baseCommit`, naming the pod's store commit as the only have when the pod holds a store for the `url` and naming none otherwise, and indexes the pack as rule 3 states, then records or removes `lenny.storeCommit` as rule 3 states. It then lists the objects reachable from the repository's refs, reflogs, and index but not from `lenny.baseCommit`. A restore whose pack would bring the slot's restored bytes above `workspaceSizeLimitBytes`, or whose repository references an object it cannot reach, fails the resume or the bind.
7. **Restore lineage.** A session whose record carries a `parent_workspace_ref` ([Section 4.5](04_system-components.md#45-artifact-store) **Workspace lineage:**) has a lineage pool, read each time the gateway binds the session: the pool of the session that `parent_workspace_ref` names. The session has no lineage pool when `parent_workspace_ref` names no session the gateway can read, when that session's pool no longer exists, or when that pool's `ownerTenantId` is set and differs from the session's tenant. Before the gateway sends a pod the call that restores the session's captured workspace, it records the lineage pool against that pod in `lenny:pod:{pod_id}:git_lineage` ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)), with an expiry at that call's bound (for a resume, the [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) **`resuming` timeout (300s):**). Expiry is the only removal. [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) `FetchGitPack` reads the record.
```

**Edit (d), §26.2 Workspace layout bullets.** In spec/26_reference-runtime-catalog.md §26.2, in the bullets under the paragraph that begins "**Workspace layout.**", make two deletions. In the bullet that begins "- `.git/` is materialized by the gateway when", delete " by the gateway". In the bullet that begins "- `/workspace/shared/` ([§5.1]", delete "; coding-agent runtimes do not use it" and keep the closing period.

**Edit (e), §7.1 paragraph `**Derive restore semantics:**`.** The paragraph is proposal 0109's, landed on main. Make two replacements. Replace "extracts only the bundle's workspace tree into the build tree before it materializes the bound plan's `sources` array" with "extracts only the bundle's workspace tree into the build tree, and completes it as [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 6 states, before it materializes the bound plan's `sources` array". Replace "or the adapter cannot fetch, decode, or extract a chunk," with "the adapter cannot fetch, decode, or extract a chunk, or that completion fails,".

**Edit (f), §4.7.1 stage table, `FinalizeWorkspace` row.** In the row whose first cell is `` `FinalizeWorkspace` `` and that names `restore_chunks`, replace "extract the starting snapshot's workspace tree into the build tree first" with "restore the starting snapshot's workspace tree into the build tree first". The row stays one physical line.

### SPEC-4 · spec/13_security-model.md § 13.4 Upload Security, spec/07_session-lifecycle.md § 7.4 Upload Safety, and spec/02_goals-and-non-goals.md § 2 Non-Goals

**Edit (a), §13.4 first bullet.** In the bullet that begins "Gateway validates and authorizes all uploads.", after "pod binaries neither decompress archives nor canonicalize paths on untrusted input." append:

```markdown
 One exception applies. For a `gitRepository` shared asset ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**) and a cache-served `gitClone` source ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**), the adapter runs local git on a pack the gateway produced on `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)) and on the slot repositories that [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rules 5 and 6 complete. The gateway produces each pack from its own mirror, which rejects malformed objects when it fetches them. The agent writes a slot repository, so the adapter treats it as untrusted input. It finds those repositories by walking the workspace tree that rule 5 or 6 operates on, without following symlinks and considers only a `.git` entry that is a directory. It resolves the repository's git directory and its `objects`, `objects/info`, and `objects/pack` directories beneath that tree with no symlink in any path component, and fails the capture, resume, or bind when one is not a directory. It makes every write to the repository through that resolution. Git runs with a git directory and configuration that the adapter creates for each operation, so no configuration, hook, or include that the agent wrote takes effect. The adapter reads the `lenny.*` keys [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rule 3 records only as data, and fails the capture, resume, or bind when `lenny.baseCommit` does not match `^[0-9a-f]{40}$`. Git writes working-tree files to a slot only by checking out a `resolvedCommitSha` tree that the gateway validated under the [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rules, and post-promotion symlink re-validation applies to that tree.
```

**Edit (b), §13.4 archive-validators bullet.** In the bullet that begins "**Archive validators (normative, non-tunable platform ceilings).**", replace

```markdown
Full rationale and streaming-enforcement mechanics live in [§7.4](07_session-lifecycle.md#74-upload-safety):
```

with

```markdown
Full rationale and streaming-enforcement mechanics live in [§7.4](07_session-lifecycle.md#74-upload-safety). A cache-served `gitClone` source does not pass through the upload pipeline. Its checks are stated in [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** The limits are:
```

The nested list that follows is unchanged.

**Edit (c), §7.4 bullet "Archive extraction is especially strict."** After "it is never delegated to agent pods" and its clause ending "so that a hostile archive cannot starve session attachment or delegation.", insert:

```markdown
 The adapter's local git exception is stated in [Section 13.4](13_security-model.md#134-upload-security).
```

Proposal 0109's Edit (b) rewrites the opening words of this bullet. This insertion anchors on later text and composes with it.

**Edit (d), §2 Non-Goals.** In spec/02_goals-and-non-goals.md under "### Non-Goals", in the bullet "Git-based or object-store-based workspace population by pods", append:

```markdown
 (the adapter's local git on a gateway-produced pack is the [Section 13.4](13_security-model.md#134-upload-security) exception)
```

### SPEC-5 · spec/04_system-components.md § 4.7.1 Role and Gateway RPC Contract

In the table under "*Adapter → Gateway RPCs:*", insert a row directly after the `ReportPodScrub` row. The row is one physical line:

```markdown
| `FetchGitPack` | Return a git pack for a `gitRepository` shared asset ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**). The request carries `podId`, `url`, `want` (a ref or a 40-hex commit SHA), and `haves` (commit SHAs), and no session identifier, so it is pod-scoped ([Section 4.1](#41-edge-gateway-replicas) **Request Message Scope**) for a warm fill and a session fetch alike. The gateway applies the same mTLS client-certificate and projected service-account token checks to this server-streaming RPC that it applies to every other GatewayControl call ([Section 10.3](10_gateway-internals.md#103-mtls-pki)). It rejects, with `PERMISSION_DENIED`, a `podId` other than the pod the projected service-account token was issued for. It resolves the calling pod's pool and that pool's Runtime from `podId`. When `want` is a commit SHA and that Runtime has no `gitRepository` entry whose `url` equals the request's `url` after normalization, the gateway uses in that pool's place a pool that an unexpired entry of the pod's restore lineage record ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 7) names and whose Runtime has such an entry. It rejects, with `PERMISSION_DENIED`, a `url` that is not, after normalization, a `gitRepository` entry on the Runtime of the pool in use, a private entry on a pool whose `ownerTenantId` is empty, and a call that [Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** rule 4 or rule 5 refuses for the pool in use. When `want` is a ref, the request carries no `haves`; the gateway resolves the ref against the remote over its own network path on every call and returns a self-contained pack of every object reachable from the resolved commit. When `want` is a commit SHA, the gateway reads it from the entry's mirror ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** rule 4) and fetches it from the remote when the mirror lacks it. A have that the mirror lacks after one fetch of it by SHA is ignored. When at least one have remains, the gateway computes the self-contained (non-thin) pack of the objects reachable from `want` and not from the remaining haves; when that pack's objects sum to more bytes than the blobs and tree objects of `want`'s tree, or when no have remains, the gateway instead returns a shallow pack holding `want`'s commit object and every object reachable from its tree. The response streams a header naming the resolved commit and stating whether the pack is shallow, followed by the pack, and the gateway streams the pack without buffering it whole. |
```

### SPEC-6 · spec/15_external-api-surface.md § 15.1 REST API, error table, and spec/06_warm-pod-model.md § 6.2 Pod State Machine

**Edit (a), §15.1 error table.** Locate the row by its first cell, `` `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` ``. Keep the first three cells. Replace the fourth cell with the text below. The row stays one physical line.

```markdown
Workspace materialization aborted because the content violates a platform-normative validator defined in [Section 13.4](13_security-model.md#134-upload-security) / [Section 7.4](07_session-lifecycle.md#74-upload-safety), or because [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 2 refuses a cache-served `gitClone` source's tree. `details.reason` carries the specific sub-code: `max_decompressed_size`, `max_decompression_ratio`, `max_entry_count`, `max_entry_size`, `max_path_depth`, `max_path_length`, `path_escapes_root`, `non_regular_entry`, `symlink`, or `workspace_size_limit`. `workspace_size_limit` is the gateway's check of a cache-served `gitClone` tree against `workspaceSizeLimitBytes`, the same limit the [Section 4.4](04_system-components.md#44-event--checkpoint-store) pre-checkpoint workspace size probe reports under that reason; it is not an archive-extraction abort and is not counted in `lenny_upload_extraction_aborted_total`. Not retryable. The same input fails the same way on retry. The client must supply a conformant archive or, for a `gitClone` source, a commit that conforms to the rules or a pool whose `workspaceSizeLimitBytes` fits the tree. Applies to client uploads, to `gitClone` sources ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema)), and to delegation file exports ([Section 8.7](08_recursive-delegation.md#87-file-export-model)).
```

**Edit (b), §6.2 bullet `**Client visibility:**`.** Replace

```markdown
unless it is an archive validator violation, which surfaces as `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` ([§13.4](13_security-model.md#134-upload-security))
```

with

```markdown
unless it is a violation the `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` row of [§15.1](15_external-api-surface.md#151-rest-api) names, which surfaces as that code
```

In the `/start` sentence of the same bullet, replace

```markdown
while any other setup-window failure stays the retryable `STARTING_FAILED` fallback
```

with

```markdown
while a violation the `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` row of [§15.1](15_external-api-surface.md#151-rest-api) names surfaces as that code, and any other setup-window failure stays the retryable `STARTING_FAILED` fallback
```

Proposal 0085 edits other §6.2 bullets and leaves this one unchanged.

### SPEC-7 · spec/16_observability.md § 16.1 Metrics

Insert two rows directly after the row that begins "| Upload extraction aborted (`lenny_upload_extraction_aborted_total`". Each row is one physical line.

```markdown
| Git pack bytes (`lenny_workspace_git_pack_bytes`, histogram labeled by `pool`, `kind`: `warm` \| `delta` \| `full` — bytes of each pack the gateway streams on `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)). `warm` records a request whose `want` is a ref (a repository-store fill). `delta` records a pack that excludes the objects of at least one have. `full` records a shallow pack. Requests from materialization and from restore completion ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**) are both recorded.) | Histogram       |
| Cache-served Git sources (`lenny_workspace_git_cache_sources_total`, counter labeled by `pool` — each `gitClone` source the gateway routes to a repository store ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**).) | Counter         |
```

### SPEC-8 · spec/04_system-components.md § 4.3, § 4.6.1, § 4.7.11, and § 4.9, spec/13_security-model.md § 13.2.4, spec/14_workspace-plan-schema.md § 14, spec/15_external-api-surface.md § 15.1, spec/16_observability.md § 16.1, § 16.4, and § 16.5, spec/17_deployment-topology.md § 17.7, spec/24_lenny-ctl-command-reference.md § 24.5, spec/26_reference-runtime-catalog.md § 26.2 and § 26.3

**Edit (a), §4.9 Credential Provider table.** Insert after the row that begins "| `github`           | GitHub App credentials |":

```markdown
| `git-https`        | Username and token or password (Kubernetes Secret) | HTTP Basic username and password for an HTTPS Git host |
```

**Edit (b), §4.9 Credential Pool, `hostPatterns` paragraph.** Make two replacements in the paragraph that begins "**`hostPatterns` — VCS-pool-only field.**". Leave the rest of the paragraph unchanged, including the sentence opening that 0086 SPEC-2 Edit (d) rewrites. Replace

```markdown
(v1 built-in: `github`; custom providers may implement others such as GitLab, Bitbucket, Gitea, self-hosted)
```

with

```markdown
(v1 built-ins: `github` and `git-https`; custom providers may implement others)
```

Replace the text from ", compares it against `hostPatterns` across all VCS pools in the tenant," (including its leading comma) through "multiple matches yield `422 GIT_CLONE_AUTH_HOST_AMBIGUOUS`." with the block below. The paragraph's remaining sentence, "Non-VCS pools (LLM providers) ignore this field.", then stands after the list as its own paragraph, without its leading space.

```markdown
 and binds the host to a pool by the rules below, which govern every VCS credential resolution for a host:

1. The candidates are the tenant's active VCS pools whose `hostPatterns` match the host, whatever their provider.
2. Zero candidates yields `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, and more than one yields `GIT_CLONE_AUTH_HOST_AMBIGUOUS` ([§15.1](15_external-api-surface.md#151-rest-api)).
3. When the request carries an `auth.leaseScope` whose `<provider>` differs from the single candidate's provider, the gateway rejects it with `GIT_CLONE_AUTH_UNSUPPORTED_HOST`.
4. A request that carries no `leaseScope`, such as the in-pod credential helper's ([§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes)), uses the single candidate.
```

**Edit (b2), §4.9 Credential Pool, new paragraph.** Insert directly after the paragraph "Non-VCS pools (LLM providers) ignore this field." that Edit (b) leaves:

```markdown
**`git-https` credentials.** A `git-https` pool serves any HTTPS Git host its `hostPatterns` name, such as a GitLab, Bitbucket, Gitea, or self-hosted server. Each credential's Secret holds a `username` key and a `password` key, the `kubernetes.io/basic-auth` layout. `password` holds the host's token or password, such as a GitLab deploy token, a Bitbucket app password, or a Gitea access token. A credential whose `secretRef` names a data key, or whose Secret lacks a non-empty `username` or `password`, is unusable. For each git operation it performs or serves, the gateway obtains the pair from the Token Service ([Section 4.3](#43-token-service)), which resolves the Secret as **Token Service secret resolution.** states, and sends the pair as HTTP Basic authentication to the bound host. The `vcs.git-https.read` and `vcs.git-https.write` scopes resolve to the same credential, and the host's grants on the token decide whether a push succeeds.
```

**Edit (c), §4.9 `leaseTTLSeconds` table.** Insert after the row that begins "| `github` | **3600** (1 hour) | 3600 (GitHub installation token maximum) |":

```markdown
| `git-https` | **3600** (1 hour) | None (the token is deployer-managed; TTL is synthetic) | The token carries no upstream expiry. |
```

**Edit (d), §4.9 direct-mode `materializedConfig` table.** Insert directly before the row that begins "| `azure_openai` | `apiKey` | string | yes (API-key pools) |":

```markdown
| `git-https` | `username` | string | yes | HTTP Basic username for the Git host. Plaintext. |
| | `password` | string | yes | Token or password for the Git host. Plaintext. |
```

**Edit (e), §14 `gitClone.auth` paragraph.** Make three replacements in the paragraph that begins "**`gitClone.auth` object.**". Replace

```markdown
v1 ships `github` as the only built-in provider (scopes `vcs.github.read` / `vcs.github.write`), and deployers may register additional providers (e.g., GitLab, Bitbucket, Gitea, self-hosted) via the custom `CredentialProvider` interface in [§4.9](04_system-components.md#49-credential-leasing-service)
```

with

```markdown
v1 ships the built-in providers `github` (scopes `vcs.github.read` / `vcs.github.write`) and `git-https` (scopes `vcs.git-https.read` / `vcs.git-https.write`), which serves any HTTPS Git host ([§4.9](04_system-components.md#49-credential-leasing-service) **`git-https` credentials.**), and deployers may register further providers via the custom `CredentialProvider` interface in [§4.9](04_system-components.md#49-credential-leasing-service)
```

Replace the text from "The gateway resolves `<provider>` by matching the `url` host" through "are rejected with `422 GIT_CLONE_AUTH_HOST_AMBIGUOUS`." with

```markdown
The gateway binds the `url` host (extracted from the parsed HTTPS URL's authority; see `gitClone.url` restrictions above) to one of the tenant's VCS credential pools by the [§4.9](04_system-components.md#49-credential-leasing-service) **`hostPatterns`** rules, at session creation and for every git operation it performs for the source.
```

Replace "The gateway then performs the clone with a short-lived HTTPS bearer/basic token scoped to the repository," with "The gateway then performs the clone with the HTTP Basic credential of the bound pool,". Leave the rest of the paragraph, the `gitClone.url` restrictions paragraph, and §21.9 unchanged.

**Edit (f), §15.1 error table, `GIT_CLONE_AUTH_UNSUPPORTED_HOST` row.** Replace "but the URL's host does not match any VCS credential pool's `hostPatterns` in the tenant." with "but the URL's host matches no VCS credential pool's `hostPatterns` in the tenant, or matches a pool whose provider differs from the `auth.leaseScope` provider." Replace "The caller must register a VCS credential pool whose `hostPatterns` covers the host, or remove `auth` for public repos." with "The caller must register a VCS credential pool whose `hostPatterns` covers the host, name that pool's provider in `auth.leaseScope`, or remove `auth` for public repos." The row stays one physical line.

**Edit (g), §26.2 and §26.3.** In the §26.2 bullet that begins "- `vcs.<provider>.read` / `vcs.<provider>.write` — optional;", replace "V1 ships `github` as the only built-in VCS provider (`vcs.github.read` / `vcs.github.write`); deployers may register additional providers (GitLab, Bitbucket, Gitea, self-hosted) via the custom `CredentialProvider` interface in [§4.9](04_system-components.md#49-credential-leasing-service)." with "The built-in VCS providers are listed in [§4.9](04_system-components.md#49-credential-leasing-service) **Credential Provider**." In the §26.3 scope table row for `vcs.<provider>.read` / `vcs.<provider>.write`, replace "V1 ships `github` as the built-in provider." with "The built-in VCS providers are listed in [§4.9](04_system-components.md#49-credential-leasing-service)." and replace "short-lived token scoped to the single repo." with "the credential of the pool the host binds to." The row stays one physical line.

**Edit (h), §4.9 Credential Lifecycle and Secret Management and the §16.1 secret-reloads row.** Make the replacements below. In the paragraph that begins "**Token Service secret resolution.**", replace

```markdown
The Token Service uses a Kubernetes **informer** (watch) on Secrets in the `lenny-system` namespace.
```

with

```markdown
The Token Service reads a Secret that a credential pool references with a `get` on the Secret that **RBAC for the Token Service.** rule 1 resolves, the only access the `lenny-token-service-secrets` Role grants, at each call that needs the Secret's value. It does not list, watch, or cache Secrets. A read that fails for any reason other than an absent Secret, key, or value or a denied `get` fails that call with a retryable error, which the gateway counts as Token Service unavailability ([Section 4.3](#43-token-service) **High availability and failure handling:**), and the Token Service serves no earlier value in its place. Its `ReadCredentialSecret` call returns a Secret's value to the gateway only for an unrevoked credential of an active VCS pool.
```

and in the same paragraph replace "the Token Service detects the change within its informer resync interval (default: 30 seconds, configurable via `tokenService.secretResyncInterval` Helm value)" with "the Token Service's next read of the Secret observes the change", and replace "The metric `lenny_token_service_secret_reloads_total` (counter, labeled by secret name and outcome: `success`, `not_found`, `parse_error`) tracks secret resolution events." with "The metric `lenny_token_service_secret_reloads_total` ([Section 16.1](16_observability.md#161-metrics)) counts each read by outcome." In the §16.1 row for `lenny_token_service_secret_reloads_total`, replace "outcome: `success`, `not_found`, `parse_error`" with "outcome: `success`, `not_found`, `denied`, `unavailable`, `parse_error`"; the row stays one physical line. In **Credential rotation workflow:** step 2, replace "The Token Service detects the Secret change via its informer within 30 seconds." with "The Token Service's next read of the Secret returns the new material." In step 4, replace "Existing active leases continue using the previously materialized credentials until they expire naturally (governed by lease TTL)." with "An existing active lease that holds a copy of the previous material keeps using it until the lease expires naturally (governed by lease TTL)." In the **GitOps pattern.** "**Ordering:**" bullet, replace "re-resolved on the next informer event" with "re-resolved at the next read".

**Edit (i), §13.2.4 component table, Token Service row.** In the row whose first cell is "**Token Service** (`lenny.dev/component: token-service`)", replace "`kube-system` CoreDNS (UDP/TCP 53)." in the Egress Allowed cell with "kube-apiserver (TCP 443, CIDR `{{ .Values.kubeApiServerCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53)." The row stays one physical line.

**Edit (j), §4.3, §4.6.1, §16.5, and §17.7 Token Service outage.** Make the changes below.

In §4.3 **High availability and failure handling:**, in the bullet "Sessions that already hold credential leases **continue operating** ...", replace "until renewal." with "until renewal; their VCS credential operations fail as the VCS bullet below states." In the bullet "Sessions that do not need LLM credentials (e.g., file-processing-only runtimes) are **unaffected**.", replace "do not need LLM credentials" with "need neither LLM nor VCS credentials". In the bullet "New sessions that require LLM or OAuth credentials **cannot start** ...", replace "require LLM or OAuth credentials" with "require LLM, OAuth, or VCS credentials". Insert directly after the bullet "Sessions that need neither LLM nor VCS credentials ... are **unaffected**.":

```markdown
- The gateway reads a VCS credential through the Token Service for each git operation that uses it and keeps no copy between operations ([Section 4.9](#49-credential-leasing-service) **`git-https` credentials.** and **Token Service secret resolution.**), so VCS credentials have no grace period. Git operations through the in-pod credential helper ([Section 26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes)) fail in running sessions, and every operation on a private repository store's mirror is refused ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** rule 4).
```

In the §4.3 paragraph that begins "Additionally, the gateway **caches active credential leases in memory**.", replace "does not affect already-leased credentials until the lease expires" with "does not affect already-leased credentials other than VCS credentials until the lease expires".

In §4.6.1 **etcd unavailability (degraded mode):**, in the bullet that begins "**Existing sessions continue unaffected.**", replace "**Existing sessions continue unaffected.**" with "**Existing sessions continue.**" and replace "they do not depend on etcd for ongoing operation." with "they do not depend on etcd for ongoing operation, except that the Token Service cannot read a credential Secret while the API server cannot read etcd ([Section 4.9](#49-credential-leasing-service) **Token Service secret resolution.**), so the Token Service outage effects of [Section 4.3](#43-token-service) **High availability and failure handling:** apply to them."

In the §16.5 `TokenServiceUnavailable` row, replace "(existing sessions unaffected until lease expiry)" with "(the effect on running sessions is in [§4.3](04_system-components.md#43-token-service) **High availability and failure handling:**)". The row stays one physical line.

In the §17.7 **Token Service outage** bullet, in *Trigger*, replace "existing sessions are unaffected until credential lease expiry" with "[Section 4.3](04_system-components.md#43-token-service) **High availability and failure handling:** states the effect on running sessions". In *Remediation*, replace "(1) **Existing sessions continue** — sessions holding active leases are unaffected until lease expiry (grace period = lease TTL, typically minutes to hours depending on `credentialLeaseTTL`)" with "(1) **Leased sessions continue** — active credential leases keep working until lease expiry, while VCS credential operations fail at once ([Section 4.3](04_system-components.md#43-token-service) **High availability and failure handling:**)". In *Diagnosis*, replace "(5) Verify Secret access: `kubectl auth can-i get secret -n lenny-system --as=system:serviceaccount:lenny-system:lenny-token-service` — RBAC denial prevents credential decryption." with "(5) Verify Secret access for a Secret a credential pool references: `kubectl auth can-i get secret/<name> -n lenny-system --as=system:serviceaccount:lenny-system:lenny-token-service`. The Role grants only named Secrets ([Section 4.9](04_system-components.md#49-credential-leasing-service) **RBAC for the Token Service.** rule 2), so a check that names no Secret answers `no` on a correct install." In *Remediation*, replace "(4) **RBAC fix:** If Secret access is denied, re-apply the Token Service RBAC: `kubectl apply -f charts/lenny/templates/token-service-rbac.yaml`." with "(4) **RBAC fix:** If Secret access is denied, grant the Secret as [Section 4.9](04_system-components.md#49-credential-leasing-service) **RBAC for the Token Service.** rule 3 states." Leave the rest of the item unchanged.

**Edit (k), §4.7.11 item 6 and §16.4 credential-sensitive RPCs.** In §4.7.11 item 6, replace "`AssignCredentials` and `RotateCredentials` RPCs carry credential material in their payloads." with "The `AssignCredentials` and `RotateCredentials` RPCs and the Token Service `ReadCredentialSecret` RPC ([Section 4.9](#49-credential-leasing-service) **Token Service secret resolution.**) carry credential material in their payloads.", and replace "never the `materializedConfig` contents." with "never the `materializedConfig` contents or a `ReadCredentialSecret` value.". In the §16.4 bullet "**Credential-sensitive RPCs**", replace "(`AssignCredentials`, `RotateCredentials`)" with "(`AssignCredentials`, `RotateCredentials`, and the Token Service `ReadCredentialSecret`)".

**Edit (l), §4.9 **RBAC for the Token Service.** Role example and rules.** In the Role example, replace the line `    resourceNames: []  # populated by Helm template from credentialPool secretRef list` with `    resourceNames: ["lenny-token-service-secrets-placeholder"]  # the chart appends the names rule 2 below lists`. Replace the paragraph that begins "The `resourceNames` list is populated at install time" (through "emits the required RBAC patch command.") with:

```markdown
The Role and every `secretRef` follow these rules:

1. **`secretRef` resolution.** A credential with no `secretRef` names no Secret. A one-segment `secretRef` names the Secret of that name in the release namespace. A two- or three-segment `secretRef` names the Secret whose namespace is its first segment and whose name is its second, and a third segment names a data key that only a provider's rule under **Credential Pool** reads. Any other `secretRef`, including one with an empty segment, is invalid and names no Secret: the admin credential-pool endpoints reject it with `400 VALIDATION_ERROR`, the chart render fails on it, and the Token Service reads nothing for it. The chart render, the admin API, the admin-time live-probe, and the Token Service resolve a `secretRef` by this rule.
2. **Role contents.** The chart renders the Role in the release namespace. Its `resourceNames` list a placeholder first, because an empty list grants every Secret in the namespace, followed by the name of each Secret that a `secretRef` in `bootstrap.credentialPools`, in an enabled `bootstrap.defaultGithubVcsPool`, or in `tokenService.credentialSecretRefs` names. A `secretRef` that names another namespace fails the render.
3. **Single writer.** The chart render is the only writer of the Role's `resourceNames`, and no Lenny component patches the live Role. A credential whose Secret the Role does not name is granted by adding its `secretRef` to `tokenService.credentialSecretRefs` and running `helm upgrade`. Because the values hold every granted name, a later `helm upgrade` keeps the grant.
```

**Edit (m), citers of the §4.9 Role rules.** (1) In §4.9 **Admin-time RBAC live-probe (required).**, replace "the error body names the missing `resourceName`(s) (for pool creation, the full list of failing Secrets is returned so the operator can patch RBAC in a single round-trip), and the response `details` field includes the exact `kubectl` / Helm RBAC patch command that `lenny-ctl admin credential-pools add-credential` would emit so the operator can apply the grant and retry" with "the error body names the failing `secretRef` values (for pool creation, every failing one, so a single `helm upgrade` grants them all), and the response `details` field gives a remediation for each: for a `DENIED` Secret, the grant of **RBAC for the Token Service.** rule 3, and for a `NOT_FOUND` Secret, the creation of that Secret in the release namespace". (2) In §4.9 **Large pool Secret management (Tier 3).**, replace the paragraph body from "At Tier 3 with hundreds of credentials" through "This preserves the Secret-per-credential topology while eliminating manual Helm updates." with "At Tier 3 with hundreds of credentials, operators should use an external secrets manager (AWS Secrets Manager, GCP Secret Manager, HashiCorp Vault) with the External Secrets Operator (`external-secrets.io`) to synchronize Secret material into Kubernetes Secrets. The `secretRef` in `CredentialPool` always points to a Kubernetes Secret, and the External Secrets Operator handles the synchronization from the external source. The External Secrets Operator does not change the Token Service Secret Role, so each new Secret name enters the Role by **RBAC for the Token Service.** rule 3." The bold label stays. (3) In the §15.1 `CREDENTIAL_SECRET_RBAC_MISSING` row, replace "`details.resourceNames` is an array naming the missing Secret(s) and `details.rbacPatch` contains the required RBAC patch command (equivalent to `lenny-ctl admin credential-pools add-credential`'s emitted patch) covering the full set. Apply the grant and retry." with "`details.resourceNames` is an array of the failing `secretRef` values. `details.remediation` is an object with one member per `details.resourceNames` entry, keyed by that entry, whose value is a string giving the remediation that [Section 4.9](04_system-components.md#49-credential-leasing-service) **Admin-time RBAC live-probe (required).** names for that ref's verdict. Apply each and retry." The row stays one physical line. (4) In the §24.5 row for `lenny-ctl admin credential-pools add-credential`, replace "Add credential to pool (also emits required RBAC patch command)" with "Add credential to pool". (5) In §17.7 **Credential pool exhaustion** *Remediation*, replace "(2) Add credentials: `POST /v1/admin/credential-pools/<name>/credentials` with a new `secretRef`." with "(2) Add credentials: grant each new Secret as [Section 4.9](04_system-components.md#49-credential-leasing-service) **RBAC for the Token Service.** rule 3 states, then `POST /v1/admin/credential-pools/<name>/credentials` with its `secretRef`." (6) In §4.9 **Initial setup workflow:**, replace "2. **Register the credential pool** via the admin API (`POST /v1/admin/credential-pools`) or via the bootstrap seed" with "2. **Register the credential pool** via the admin API (`POST /v1/admin/credential-pools`), after granting each of its Secrets as **RBAC for the Token Service.** rule 3 states, or via the bootstrap seed". (7) In §4.9 **Emergency revocation runbook:**, replace "(5) add a replacement credential to the pool via `PUT /v1/admin/credential-pools/{name}`;" with "(5) add a replacement credential to the pool via `PUT /v1/admin/credential-pools/{name}`, after granting its Secret as **RBAC for the Token Service.** rule 3 states when the Secret is new;".

### SPEC-9 · spec/04_system-components.md § 4.2 and spec/05_runtime-registry-and-pool-model.md § 5.2

**Edit (a), §4.2 multi-tenancy table.** In the row whose first cell is "Runtimes, pools", append to the last cell: "A pool record's `ownerTenantId` is not a `tenant_id` column and is not an RLS key; [Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** states its rules." The row stays one physical line.

**Edit (b), §5.2 pool dimensions.** Under "**Pool dimensions:**", insert after "- Runtime name": "- Owning tenant (`ownerTenantId`; see [Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.**)".

### SPEC-10 · spec/12_storage-architecture.md § 12.4, spec/28_communication-channels.md § 28.3, § 28.5.7, and § 28.7

**Edit (a), §12.4 key table.** Insert directly after the row whose first cell is `` `lenny:pod:{pod_id}:rehydrating` ``, as one physical line:

| `lenny:pod:{pod_id}:git_lineage` | Restore lineage record (`REG-GITLINEAGE`) | **Pod-scoped.** A sorted set of pool names, each scored by its expiry time ([§14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 7). A write drops expired members, and the key expires with its latest member. |

**Edit (b), §12.4 tenant-prefix exceptions.** Replace "the `lenny:pod:{pod_id}:*` slot-counter keys are pod-scoped" with "the `lenny:pod:{pod_id}:*` slot-counter and restore-lineage keys are pod-scoped".

**Edit (c), §12.4 failure table.** Insert directly after the row whose first cell is "Session slot set (`lenny:pod:{pod_id}:active_slots`)", as one physical line:

| Restore lineage record (`lenny:pod:{pod_id}:git_lineage`) | **Fail closed, reconstructed on the next attempt** — a bind that cannot write the record proceeds without it, and a `FetchGitPack` call that needs the record and cannot read it fails, so a restore completion that needs a lineage pool fails its bind or resume ([§14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 7). The next attempt resolves the lineage pool from Postgres and writes it again. |

**Edit (d), §28.3 Register-entry register.** Insert directly after the `REG-SLOTCOUNT` row:

| `REG-GITLINEAGE` | Redis | `lenny:pod:<pod>:git_lineage` | Gateway replicas | Gateway replicas | Per pod, the lineage pools of the sessions whose workspace the gateway is restoring onto it, each until its restoring call's bound ([§14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** rule 7) | None |

**Edit (e), §28.5.7 Gateway-to-store.** In the paragraph that begins "This boundary carries the channels a gateway replica opens to a datastore", delete the clause ", which are `REG-COORDLEASE`, `REG-COORDMIRROR`, `REG-SLOTCOUNT`, `REG-PODSTATE`, and `REG-CLAIM`," (it spans a line break), so the sentence reads "The entries of the §28.3 register-entry register are shared state mediating two participants with no live connection rather than channels per §28.2, so they carry no card here." Reflow the paragraph's lines.

**Edit (f), §28.7 Wire-contract artifact register.** In the row whose first cell is `` `schemas/lenny-tokenservice.proto` ``, replace "and for the admin-time probe" with "for the read of a VCS credential's Secret ([§4.9](04_system-components.md#49-credential-leasing-service) **Token Service secret resolution.**), and for the admin-time probe". The row stays one physical line.

### SPEC-11 · spec/17_deployment-topology.md § 17.8.1 Operational Defaults — Quick Reference

In the table under "All tunable defaults collected in one place for operator convenience.", insert two rows directly after the row that begins "| Runtime supervisor timing: ping interval". Each row is one physical line.

```markdown
| Repository-store fill deadline (`controller.tuning.gitStoreFillTimeout`) | The controller's default; the specification states no value | [§6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.** |
| Gateway repository mirror limits per replica: disk budget (`gateway.gitMirror.maxBytes`), concurrent remote fetches (`gateway.gitMirror.maxConcurrentFetches`), and concurrent pack productions and tree walks (`gateway.gitMirror.maxConcurrentPacks`) | The chart's default; the specification states no value | [§6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Private repository stores.** rule 4 |
```

## Spec files touched

- `spec/02_goals-and-non-goals.md` (SPEC-4)
- `spec/04_system-components.md` (SPEC-3, SPEC-5, SPEC-8, SPEC-9)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1, SPEC-9)
- `spec/06_warm-pod-model.md` (SPEC-2, SPEC-6)
- `spec/07_session-lifecycle.md` (SPEC-3, SPEC-4)
- `spec/12_storage-architecture.md` (SPEC-10)
- `spec/13_security-model.md` (SPEC-4, SPEC-8)
- `spec/14_workspace-plan-schema.md` (SPEC-3, SPEC-8)
- `spec/15_external-api-surface.md` (SPEC-6, SPEC-8)
- `spec/16_observability.md` (SPEC-7, SPEC-8)
- `spec/17_deployment-topology.md` (SPEC-8, SPEC-11)
- `spec/24_lenny-ctl-command-reference.md` (SPEC-8)
- `spec/26_reference-runtime-catalog.md` (SPEC-3, SPEC-8)
- `spec/28_communication-channels.md` (SPEC-10)
