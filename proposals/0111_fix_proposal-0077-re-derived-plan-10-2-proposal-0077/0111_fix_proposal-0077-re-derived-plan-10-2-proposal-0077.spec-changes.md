# Spec changes — Pre-materialize large git repositories on a warm pod

## Design (as the spec must state it)

The specification states the mechanism in four homes, and every other site cites one of them.

- **§6.4 Repository stores.** (SPEC-2) owns the `gitRepository` shared-asset entry: its fields, the url normalization, the warm fill before ready, the warm-fill failure mode, store immutability, and scrub survival.
- **§4.7.1 `FetchGitPack`** (SPEC-5) owns the RPC contract: authorization, ref resolution, the pack the gateway returns for a ref, for a commit with usable haves, and for a commit without them, and the bound that decides between a delta pack and a shallow pack.
- **§14 Cache-served `gitClone` sources.** (SPEC-3) owns the session half as numbered rules: eligibility (rule 1), tree validation and the materialization bound (rule 2), slot-repository construction (rule 3), the worktree prohibition (rule 4), the capture copy (rule 5), and restore completion (rule 6).
- **§13.4** (SPEC-4) owns the scoped exception that lets the adapter run local git on a gateway-produced pack and on an agent-written slot repository, with pointers in §7.4 and §2.

SPEC-1 adds the entry type to the §5.1 Runtime example and merge table. SPEC-6 adds the `workspace_size_limit` reason to `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. SPEC-7 adds two gateway-emitted metrics.

The gateway performs every git network operation for a cache-served source. The pod runs only local git, under the §13.4 exception. Default `restricted` egress, the base NetworkPolicy, and the §14 gateway-clone sentence remain true and are not edited.

## Edge cases and accepted failure modes

Each case cites the rule that owns it.

- **A warm fill fails.** The pod takes `failed` (§6.4 **Repository stores.**).
- **The Runtime gains or loses a `gitRepository` entry after a pod warmed.** The binder decides eligibility from current configuration (§14 rule 1). A pod without a store for the url takes the shallow pack with no alternates (§14 rule 3), and a pod whose store url the Runtime no longer names receives `PERMISSION_DENIED` on `FetchGitPack` (§4.7.1). The denial fails the materialization, and it fails the resume of a checkpoint whose restore completion has to fetch for that url (§14 rule 6).
- **A store is older than the session's pinned commit.** The session obtains a larger delta, or the shallow pack when the delta exceeds the §4.7.1 bound. The tree is the same either way because git is content-addressed.
- **A tree exceeds the materialization bound.** The workspace materialization fails with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` and `details.reason: workspace_size_limit` (§14 rule 2).
- **A session commit reuses a blob that only the store holds** (a revert, or a cherry-pick by SHA). The capture copy moves the object into the slot (§14 rule 5).
- **A commit lands between the first capture copy and the archive on a runtime that is not paused.** The second copy covers it, and restore's connectivity check fails the resume for any object still missing (§14 rules 5 and 6).
- **A checkpoint restores onto a pod whose store is at a different commit or absent.** Restore completion repoints or removes alternates and fetches `lenny.baseCommit`. When that pack is shallow, a repository whose refs, reflogs, or index reach an ancestor of `lenny.baseCommit`, as an amend or a reset of the pinned commit leaves, fails the resume (§14 rule 6).
- **A restore's pack would exceed `workspaceSizeLimitBytes`.** The resume fails (§14 rule 6).
- **A shallow slot repository.** A slot built from the shallow pack has no history before `resolvedCommitSha`. Git operations that walk earlier history (`git log` past the pin, `merge-base` with older commits) see a shallow repository. This is accepted for the first version.
- **Embedded adapter mode.** `/workspace/shared/` is writable by the single container, so store immutability rests on that container writing nothing after population (§6.4 **Repository stores.**).

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
  - type: gitRepository  # public HTTPS repository held as a read-only bare git object store; see §6.4
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
**Repository stores.** A `sharedAssets` entry of `type: gitRepository` names an HTTPS repository `url`, under the `^https://` rule that applies to `gitClone.url` ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema)), a `gitRef` (a branch or tag name; registration rejects a `gitRef` that matches `^[0-9a-f]{40}$`), and a `destPath`. The entry carries no credential, and the gateway fetches the repository without one. Wherever the platform compares repository URLs, it compares them after normalization: the scheme and host are lowercased, then one trailing `/` and then one trailing `.git` are removed from the path. After the adapter connects to the gateway and before the pod reports ready, the adapter populates `/workspace/shared/{destPath}` with a bare git repository. It builds the repository from the commit and pack that `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)) returns for `gitRef`, and it records that commit as the store's only ref. The adapter runs no git network operation. A fill that fails for any reason, including exhaustion of the node-local ephemeral storage that bounds the volume, or that does not complete within the adapter's fill deadline, takes the pod to `failed` on its warm-fill path ([Section 6.2](#62-pod-state-machine)). On a `preConnect` pool the fill runs in `sdk_connecting`, so the `sdk_connecting` watchdog ([Section 6.1](#61-what-a-pre-warmed-pod-looks-like)) also bounds it, and the deployer sets `sdkConnectTimeoutSeconds` to cover the fill. After population the adapter does not fetch into, repack, prune, or otherwise write a store, so a store holds the history of one commit for the life of the pod. In embedded adapter mode the runtime shares the adapter's container and `/workspace/shared/` is writable by that container, so a store's immutability there rests on the container writing nothing after population, as it does for every other shared asset. Neither the per-slot cleanup nor the whole-pod scrub ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) removes a store. [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.** states how a session uses a store.
```

### SPEC-3 · spec/14_workspace-plan-schema.md § 14 and § 14.1, spec/26_reference-runtime-catalog.md § 26.2

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

1. **Eligibility.** A `gitClone` source is cache-served when its normalized `url` equals the normalized `url` of a `gitRepository` entry in the resolved `sharedAssets` of the Runtime of the pod's pool, `auth` is absent, `submodules` is absent or `false`, and `depth` is absent. The gateway decides eligibility when it materializes the source on a pod, from the Runtime's configuration at that moment. Every other `gitClone` source is materialized by the gateway clone this section describes above. The adapter refuses a `gitClone` source that carries `auth`, `depth`, or `submodules: true`, or whose `resolvedCommitSha` does not match `^[0-9a-f]{40}$`.
2. **Tree validation.** Before the gateway forwards a cache-served source to the adapter, it fetches `resolvedCommitSha` into its mirror of the repository and validates the commit's whole tree, placed under the slot root at the source's `path`. The tree is refused when an entry path fails the [Section 13.4](13_security-model.md#134-upload-security) path-traversal, path-depth, or path-length rule (`path_escapes_root`, `max_path_depth`, `max_path_length`); when an entry is a gitlink or has a mode other than regular file, executable file, directory, or symlink (`non_regular_entry`); or when a symlink fails the [Section 13.4](13_security-model.md#134-upload-security) symlink rule (`symlink`). The tree is also refused when its **materialization bound** exceeds the pool's `workspaceSizeLimitBytes` (`workspace_size_limit`). The materialization bound is the sum, over the tree, of each blob's size rounded up to a multiple of 4 096 bytes, each blob's size again, each tree object's size, and, for each tree entry, 128 bytes plus the byte length of the entry's path. A refusal fails the workspace materialization with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` and the named `details.reason` ([Section 15.1](15_external-api-surface.md#151-rest-api)). The other [Section 13.4](13_security-model.md#134-upload-security) archive validators do not apply, because no archive is unpacked. A cache-served source that passes this check does not exceed `workspaceSizeLimitBytes` at materialization, because the pack the adapter receives is bounded as [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) `FetchGitPack` states.
3. **Slot repository.** The adapter creates the repository's git directory with `git init` in a new directory outside the build tree, and every git operation of this rule runs with that git directory and with the source's `path` as the working tree. When the pod holds a store for the source's normalized `url`, the adapter writes the store's objects directory, as an absolute path, to `objects/info/alternates`. When `resolvedCommitSha` is not already reachable, the adapter calls `FetchGitPack` for `resolvedCommitSha`, naming the store's commit as the only have when it wrote alternates and naming none otherwise. When the response marks the pack shallow, the adapter writes `resolvedCommitSha` to the repository's `shallow` file before it indexes the pack. It indexes the pack with `git index-pack --strict`. When a pack requested with a have cannot be indexed, the adapter removes `objects/info/alternates` and requests the pack again with no have. The adapter records the normalized `url` as `lenny.gitStoreUrl`, `resolvedCommitSha` as `lenny.baseCommit`, and, unless the repository has no alternates or is shallow, the store's commit as `lenny.storeCommit` in the repository's config, and it checks out `resolvedCommitSha` detached. The adapter then moves the git directory to `.git` at the source's `path`, replacing any entry there, inside the build tree that promotion replaces `/workspace/slots/{sessionId}/current` with.
4. **No store worktrees.** No component creates a worktree of a store or uses `git clone --shared` from one. A runtime may create worktrees inside its own slot repository.
5. **Capture copy.** For each repository in the slot whose config carries `lenny.gitStoreUrl`, handled as the [Section 13.4](13_security-model.md#134-upload-security) exception states, the adapter copies into the repository's own object store every object reachable from its refs, reflogs, and index but not from `lenny.baseCommit` that the repository holds only through alternates. It runs the copy twice for every capture: before the pre-checkpoint workspace size probe ([Section 4.4](04_system-components.md#44-event--checkpoint-store) **Hard workspace size limit.**), so the probe counts the copied bytes, and again immediately before the workspace archive is written, which for a Full-level runtime is after quiescence. A failure of either copy fails the capture through the existing checkpoint failure path.
6. **Restore completion.** After the adapter restores a captured workspace tree into a slot and before the session's runtime starts or resumes, it completes each restored repository whose config carries `lenny.gitStoreUrl`, handled as the [Section 13.4](13_security-model.md#134-upload-security) exception states. It overwrites `objects/info/alternates` with the objects directory of the pod's store for that `url`, or deletes the file when the pod has none, and never reads the restored file's contents. When the config carries `lenny.storeCommit` and the pod holds no store for the `url` or holds one at a different commit, the adapter calls `FetchGitPack` for `lenny.baseCommit`, naming the pod's store commit as the only have when the pod holds a store for the `url` and naming none otherwise, and indexes the pack as rule 3 states, then records or removes `lenny.storeCommit` as rule 3 states. It then lists the objects reachable from the repository's refs, reflogs, and index but not from `lenny.baseCommit`. A restore whose pack would bring the slot's restored bytes above `workspaceSizeLimitBytes`, or whose repository references an object it cannot reach, fails the resume.
```

**Edit (d), §26.2 Workspace layout bullets.** In spec/26_reference-runtime-catalog.md §26.2, in the bullets under the paragraph that begins "**Workspace layout.**", make two deletions. In the bullet that begins "- `.git/` is materialized by the gateway when", delete " by the gateway". In the bullet that begins "- `/workspace/shared/` ([§5.1]", delete "; coding-agent runtimes do not use it" and keep the closing period.

### SPEC-4 · spec/13_security-model.md § 13.4 Upload Security, spec/07_session-lifecycle.md § 7.4 Upload Safety, and spec/02_goals-and-non-goals.md § 2 Non-Goals

**Edit (a), §13.4 first bullet.** In the bullet that begins "Gateway validates and authorizes all uploads.", after "pod binaries neither decompress archives nor canonicalize paths on untrusted input." append:

```markdown
 One exception applies. For a `gitRepository` shared asset ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**) and a cache-served `gitClone` source ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**), the adapter runs local git on a pack the gateway produced on `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)) and on the slot repositories that [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rules 5 and 6 complete. The gateway produces each pack from its own mirror, which rejects malformed objects when it fetches them. The agent writes a slot repository, so the adapter treats it as untrusted input. It finds those repositories by walking the slot without following symlinks and considers only a `.git` entry that is a directory. It resolves the repository's git directory and its `objects`, `objects/info`, and `objects/pack` directories beneath the slot with no symlink in any path component, and fails the capture or resume when one is not a directory. It makes every write to the repository through that resolution. Git runs with a git directory and configuration that the adapter creates for each operation, so no configuration, hook, or include that the agent wrote takes effect. The adapter reads the `lenny.*` keys [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rule 3 records only as data, and fails the capture or resume when `lenny.baseCommit` does not match `^[0-9a-f]{40}$`. Git writes working-tree files to a slot only by checking out a `resolvedCommitSha` tree that the gateway validated under the [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) rules, and post-promotion symlink re-validation applies to that tree.
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
 The adapter's local git exception is stated in [Section 13.4](13_security-model.md#134-upload-security). The gateway's tree validation of a cache-served `gitClone` source runs inside the Upload Handler subsystem under the same isolation.
```

Proposal 0109's Edit (b) rewrites the opening words of this bullet. This insertion anchors on later text and composes with it.

**Edit (d), §2 Non-Goals.** In spec/02_goals-and-non-goals.md under "### Non-Goals", in the bullet "Git-based or object-store-based workspace population by pods", append:

```markdown
 (the adapter's local git on a gateway-produced pack is the [Section 13.4](13_security-model.md#134-upload-security) exception)
```

### SPEC-5 · spec/04_system-components.md § 4.7.1 Role and Gateway RPC Contract

In the table under "*Adapter → Gateway RPCs:*", insert a row directly after the `ReportPodScrub` row. The row is one physical line:

```markdown
| `FetchGitPack` | Return a git pack for a `gitRepository` shared asset ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) **Repository stores.**). The request carries `podId`, `url`, `want` (a ref or a 40-hex commit SHA), and `haves` (commit SHAs). The gateway applies the same mTLS client-certificate and projected service-account token checks to this server-streaming RPC that it applies to every other GatewayControl call ([Section 10.3](10_gateway-internals.md#103-mtls-pki)). It resolves the Runtime of the calling pod's pool from `podId` and rejects, with `PERMISSION_DENIED`, a `url` that is not, after normalization, a `gitRepository` entry on that Runtime. When `want` is a ref, the request carries no `haves`; the gateway resolves the ref against the remote over its own network path on every call and returns a self-contained pack of every object reachable from the resolved commit. When `want` is a commit SHA, the gateway reads it from its mirror of the repository and fetches it from the remote when the mirror lacks it. A have that the mirror lacks after one fetch of it by SHA is ignored. When at least one have remains, the gateway computes the self-contained (non-thin) pack of the objects reachable from `want` and not from the remaining haves; when that pack's objects sum to more bytes than the blobs and tree objects of `want`'s tree, or when no have remains, the gateway instead returns a shallow pack holding `want`'s commit object and every object reachable from its tree. The response streams a header naming the resolved commit and stating whether the pack is shallow, followed by the pack, and the gateway streams the pack without buffering it whole. The request is pod-scoped. |
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

Proposal 0085 edits other §6.2 bullets and leaves this one unchanged.

### SPEC-7 · spec/16_observability.md § 16.1 Metrics

Insert two rows directly after the row that begins "| Upload extraction aborted (`lenny_upload_extraction_aborted_total`". Each row is one physical line.

```markdown
| Git pack bytes (`lenny_workspace_git_pack_bytes`, histogram labeled by `pool`, `kind`: `warm` \| `delta` \| `full` — bytes of each pack the gateway streams on `FetchGitPack` ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)). `warm` records a request whose `want` is a ref (a repository-store fill). `delta` records a pack that excludes the objects of at least one have. `full` records a shallow pack. Requests from materialization and from restore completion ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**) are both recorded.) | Histogram       |
| Cache-served Git sources (`lenny_workspace_git_cache_sources_total`, counter labeled by `pool` — each `gitClone` source the gateway routes to a repository store ([Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) **Cache-served `gitClone` sources.**).) | Counter         |
```

## Spec files touched

- `spec/02_goals-and-non-goals.md` (SPEC-4)
- `spec/04_system-components.md` (SPEC-5)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1)
- `spec/06_warm-pod-model.md` (SPEC-2, SPEC-6)
- `spec/07_session-lifecycle.md` (SPEC-4)
- `spec/13_security-model.md` (SPEC-4)
- `spec/14_workspace-plan-schema.md` (SPEC-3)
- `spec/15_external-api-surface.md` (SPEC-6)
- `spec/16_observability.md` (SPEC-7)
- `spec/26_reference-runtime-catalog.md` (SPEC-3)
