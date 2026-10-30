# Non-spec changes — Pre-materialize large git repositories on a warm pod

## Design (implementation-facing)

The normative rules live in the staged spec text: §6.4 **Repository stores.** (SPEC-2), §4.7.1 `FetchGitPack` (SPEC-5), and §14 **Cache-served `gitClone` sources.** rules 1 to 6 (SPEC-3). The deliverables below cite those homes and add only what the implementor needs beyond them.

Component split:

- **Gateway** (CODE-3, CODE-5): a per-replica bare mirror per normalized url, tree validation at materialization, eligibility routing in the binder, and the `FetchGitPack` handler.
- **Controller and registry** (CODE-2): the `gitRepository` entry travels from the Runtime CRD to the adapter flag and into `runtimestore`, so the pod, the binder, and the `FetchGitPack` authorizer see the same url set.
- **Adapter** (CODE-4, CODE-5, CODE-6): the warm fill before READY, slot-repository construction, the capture copy, and restore completion.
- **Images** (CODE-1): the gateway and adapter images carry a git binary.

One url-normalization function, `gitref.NormalizeURL` in `pkg/gateway/provisioning/gitref`, implements the §6.4 rule. The admin validation, the binder, the `FetchGitPack` authorizer, and the adapter all call it. The adapter imports it from that package, or the function moves to a leaf package both trees import; it is never reimplemented.

Every adapter git invocation runs with `-c protocol.allow=never` and an environment with `GIT_TERMINAL_PROMPT=0` and no `HOME` config. The adapter runs only the git subcommands named in this file.

## Staged code changes

### CODE-1 · Platform images carry git

Targets: a new `Dockerfile.git`; `.github/workflows/release.yml`; `scripts/cloud/aws/build-images.sh`; any Makefile or `tests/testinfra/kind` image-build target that builds `lenny-gateway` or `lenny-adapter`; the root `Dockerfile` header comment; `BUILD-GAPS.md`.

1. `Dockerfile.git` builds the binary the same way the root `Dockerfile` does. Its final stage runs on a pinned, digest-referenced nonroot base that ships `git`, `git-remote-https`, and CA certificates, with the default user at UID 65532, `WORKDIR /`, the `docs/runbooks` copy, and the same `ENTRYPOINT`.
2. In the `release.yml` image matrix, `lenny-gateway` and `lenny-adapter` take `file: ./Dockerfile.git`, following the `lenny-supervisor` `file:` precedent. In `build-images.sh`, the `IMAGES` entries for the same two binaries name `Dockerfile.git` in their third field. Every other binary stays on the root `Dockerfile` and `gcr.io/distroless/static:nonroot`.
3. Update the root `Dockerfile` header comment so it no longer states that every platform binary uses distroless.
4. `charts/lenny/values.yaml` is unchanged, because the image repository names do not change.
5. In `BUILD-GAPS.md`, file the pre-existing High defect: the shipped gateway image has no git binary, so the ls-remote resolver wired in `cmd/lenny-gateway/sessionsrv.go` and `gitref.CloneArchive`, called from `extractGitCloneSource`, fail for every `gitClone` source. Record it as owned by 0111 and closed by CODE-1.

### CODE-2 · `gitRepository` through the CRD, `runtimestore`, the controller flag, and the adapter decoder

Targets: `pkg/apis/lenny/v1alpha1/runtime_types.go` (`SharedAsset`); `pkg/embedded/crds/lenny.dev_runtimes.yaml` and `charts/lenny/crds/lenny.dev_runtimes.yaml` (regenerated); `pkg/gateway/runtime/runtimestore/runtimestore.go`; `pkg/gateway/externalapi/admin/runtimes.go`; `pkg/controller/runtime/controller.go` (`applyCRDFields`); `pkg/controller/sandbox/controller.go` (`encodeSharedAssets`) and `pkg/controller/sandbox/podspec/shared_assets.go`; `pkg/adapter/sharedassets/sharedassets.go`; `cmd/runtimes/preconnect-echo` and `cmd/runtimes/echo-embedded` flag decoders; `pkg/gateway/externalapi/openapi/openapi.json` and `pkg/ops/mcp/generated_tools.go` (regenerated).

1. **runtimestore.** Add `SharedAssetGitRepository SharedAssetType = "gitRepository"` to `AllSharedAssetTypes`, and add `URL string` (json `url`) and `GitRef string` (json `gitRef`) to `SharedAsset`. The existing `Ref` field keeps its artifact meaning and is not reused for a git ref, because SPEC-1 names the key `gitRef`.
2. **Validation.** Extend `validateSharedAssets` for `gitRepository` entries: `url` and `gitRef` required, `gitRef` rejected when `workspaceplan.IsCommitSHA` holds, `url` matching `^https://`, `ref`, `path`, `content`, and `mode` forbidden, `destPath` required. Check normalized-url uniqueness with `gitref.NormalizeURL` on the effective merged list after `mergeSharedAssets`, on every registration and update of a base or derived Runtime (SPEC-1 Edit (c)). The check runs in Go and not in CEL, because CEL cannot apply the normalization.
3. **Mirroring.** `applyCRDFields` copies CRD `sharedAssets` into `runtimestore`. A CRD `gitRepository` entry maps to `Type`, `URL`, `GitRef`, and `DestPath` (from the CRD `Path`). This gives the binder and the `FetchGitPack` authorizer the url set the pod flag carries.
4. **CRD.** Add `Type` (enum `inline` | `gitRepository`; empty means `inline`), `URL`, and `GitRef`. Keep only per-entry CEL rules: `content` and `mode` only on `inline` entries; `url` and `gitRef` required on `gitRepository` entries; `url` matching `^https://`; `gitRef` not matching `^[0-9a-f]{40}$`.
5. **Adapter decoder.** Add `Type`, `URL`, and `GitRef` to `sharedassets.FileSpec`, and extend `encodeSharedAssets` to carry them. `Materialize` and the `cmd/runtimes` decoders skip `gitRepository` entries.

### CODE-3 · Gateway mirror, tree validation, and the `FetchGitPack` handler

Targets: `schemas/lenny-adapter.proto` and regenerated `pkg/proto/adapter/v1`; `pkg/gateway/provisioning/gitref/mirror.go` (new); a `FetchGitPack` handler beside `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`; `cmd/lenny-gateway/gwcontrol_server.go`; `charts/lenny/templates` (gateway deployment) and `charts/lenny/values.yaml`; `pkg/observability/metrics/catalog.go` and `catalog_test.go` (`spec161Metrics`).

1. **Proto.** In `service GatewayControl`, after `ReportPodScrub`:

   ```proto
   // FetchGitPack returns a git pack for a gitRepository shared asset.
   // The first response is header; every later response is pack_chunk.
   // The adapter rejects any other order.
   // spec: §4.7.1 Role and Gateway RPC Contract (FetchGitPack)
   rpc FetchGitPack(FetchGitPackRequest) returns (stream FetchGitPackResponse) {}

   message FetchGitPackRequest {
     string pod_id = 1;
     string url = 2;
     string want = 3;            // a ref, or a 40-hex commit SHA
     repeated string haves = 4;  // commit SHAs; empty when want is a ref
   }

   message FetchGitPackHeader {
     string resolved_commit = 1;
     bool shallow = 2;
   }

   message FetchGitPackResponse {
     oneof payload {
       FetchGitPackHeader header = 1;
       bytes pack_chunk = 2;
     }
   }
   ```

2. **Authentication.** In `gwcontrol_server.go`, register a stream interceptor chain (`grpc.ChainStreamInterceptor`) carrying stream forms of `leasecontrol.RequireVerifiedPeerInterceptor` and `leasecontrol.RequireSATokenInterceptor`, built from the same arguments as the unary chain. Without it the server-streaming RPC bypasses both checks.
3. **Mirror** (`gitref.Mirror`). One bare repository per normalized url under the mirror root, created with `transfer.fsckObjects=true` and `fetch.fsckObjects=true`. Each operation on a url holds that url's single-flight lock.
   - `Ensure(ctx, url, sha)` fetches `sha` by SHA when absent, reusing the `git fetch -- <url> <sha>` form `gitref.Clone` uses, with no credential.
   - `ResolveRef(ctx, url, ref)` reuses `LsRemoteResolver.Resolve` against the remote on every call, then `Ensure`s the commit.
   - Capacity: when a fetch fails for lack of space, the replica deletes that url's mirror directory under the lock and refetches once. When the refetch also fails, the operation fails. The mirror writes no durable state and adds no reader-drain protocol.
4. **ValidateTree** (`gitref.Mirror.ValidateTree(ctx, url, sha, pathPrefix, allow, limit)`, where `allow` is the source's `upload.RuntimeAllow`). Walks the whole tree with `git ls-tree -r -t -l --full-tree` and applies §14 rule 2. `allow` carries the Runtime's `allowSymlinks`; do not copy `extractGitCloneSource`'s `AllowSymlinks: true`. Symlink targets go through `upload.ValidateSymlinkTarget` with the link path joined under `allow.WorkspaceRoot` and `pathPrefix`. A violation returns `*upload.ValidationError` with the matching `upload.Reason`.
   - Memoize only the limit-independent results, keyed by `(url, sha, pathPrefix, allow.AllowSymlinks, allow.WorkspaceRoot)`: the path and symlink verdict, the materialization bound, and the summed blob and tree-object bytes. Compare the bound against `limit` on every call. Drop a url's memo entries when its mirror is rebuilt.
5. **Pack** (`gitref.Mirror.Pack(ctx, url, want, haves, w)`). Implements the §4.7.1 pack rule. Unknown haves get one `Ensure` each and are dropped when still absent. The delta's object bytes come from `git rev-list --objects <want> --not <haves>` piped to `git cat-file --batch-check='%(objectsize)'`, compared with the summed blob and tree-object bytes from the memo. The pack is produced with `git pack-objects --stdout` (never `--thin`) and streamed to `w`. A shallow pack is produced by listing the commit object and `git ls-tree -r -t` of its tree into `pack-objects`.
6. **Handler.** Resolves the pod's Runtime from `pod_id` through the pod registry, normalizes `url`, and checks it against the Runtime's `gitRepository` urls in `runtimestore`.

   | Condition | Status |
   |:--|:--|
   | pod unknown, or url not on its Runtime | `PERMISSION_DENIED` |
   | `want` neither a ref nor a 40-hex SHA, or a ref `want` with `haves` | `INVALID_ARGUMENT` |
   | ref or commit absent on the remote | `NOT_FOUND` |
   | remote unreachable or fetch failed | `UNAVAILABLE` |
   | mirror out of space after one rebuild | `RESOURCE_EXHAUSTED` |

   The handler sends the header, then pack chunks of at most 1 MiB, and never holds the whole pack in memory.
7. **Gates.** The binder runs `ValidateTree` through `UploadGate.Do` (the §4.1 Upload Handler subsystem), as SPEC-4 Edit (c) states, and not through `gatedExtract`, which records the extraction-abort metric SPEC-6 excludes. The handler runs each `Pack` under its own `subsystem.Limiter`, with no breaker, sized by `gateway.gitMirror.maxConcurrentPacks` (step 9). A call waits for a slot within its deadline.
8. **Metrics.** Register `lenny_workspace_git_pack_bytes` (histogram, `pool`, `kind`) and `lenny_workspace_git_cache_sources_total` (counter, `pool`) per SPEC-7, and add both to `catalog.go` and `spec161Metrics`. The handler observes `kind` from the request and the served pack: `warm` for a ref `want`, `delta` for a pack that excludes at least one have, `full` for a shallow pack.
9. **Chart.** Add a gateway `emptyDir` named `git-mirror`, mounted at `/var/lib/lenny/git-mirror`, with `sizeLimit` from `gateway.gitMirror.sizeLimit` (default `20Gi`, documented as operator-tunable) and a `gateway.gitMirror.maxConcurrentPacks` value (default `8`, operator-tunable), and pass the path and the pack limit to the gateway through its existing configuration surface. The gateway runs with `readOnlyRootFilesystem: true`, so the mirror cannot live elsewhere.

### CODE-4 · Adapter warm fill before READY

Targets: `cmd/lenny-adapter/main.go`; `pkg/adapter/gitstore.go` (new); the adapter's GatewayControl client wiring in `pkg/adapter/gatewaylink.go`.

Add a pre-READY step in `main.go` after `ConnectGateway` and before `RunPreReady`, separate from `EnsureWarmWorkspaceLayout`. It implements §6.4 **Repository stores.**

1. When the decoded shared-asset set has any `gitRepository` entry and `ConnectGateway` produced no GatewayControl client (empty `--gateway-grpc-addr`), the step returns a fatal startup error.
2. For each `gitRepository` entry, in order:
   1. Call `FetchGitPack(pod_id = POD_NAME from the downward API, url, want = gitRef, no haves)` under a deadline from the adapter flag `--git-store-fill-timeout` (default `15m`, operator-tunable; the controller does not render it, so pods use the default), with gRPC keepalive on the connection. The deadline is not a Runtime or pool field. Non-preConnect pools have no warming watchdog, so this deadline is the only bound on a stalled fill.
   2. Run `git init --bare` in `/workspace/shared/.tmp-<hex sha256 of the normalized url>`.
   3. Pipe the pack chunks to `git index-pack --strict --stdin`.
   4. Run `git cat-file -e <resolved_commit>^{commit}`. A pack that lacks the announced commit is a fill failure.
   5. Run `git update-ref refs/lenny/store <resolved_commit>`.
   6. Set directories to 0755 and files to 0444 (matching `warmSharedMode`), so the runtime UID can read the store through alternates whatever the umask.
   7. Create `destPath`'s parent directories, then rename the temporary directory to `destPath`.
3. Keep an in-memory map from normalized url to `(destPath, resolved_commit)`. CODE-5 and CODE-6 read it.
4. Any error, deadline expiry, or volume exhaustion is a fatal startup error (§6.4 **Repository stores.**).

### CODE-5 · Binder routes cache-served sources; the adapter builds the slot repository

Targets: `pkg/gateway/podlifecycle/podsession/archiveextract.go` (`rewriteExtractedSources`, `extractGitCloneSource`); `pkg/gateway/podlifecycle/podsession/workspacestage.go` (`stageWorkspace`); `pkg/gateway/podlifecycle/podsession/bindprepare.go` and `slotbinder.go` (both `stageWorkspace` call sites); `pkg/gateway/podlifecycle/podsession/resolve.go`; `pkg/upload/upload.go`; `pkg/gateway/sessionserver/podclaimerror.go` (`writeUploadArchiveLimitExceeded`); `pkg/adapter/workspace/materialize.go` (`MaterializeWithPolicy`, the `gitClone` case); `schemas/workspaceplan-v1.json` (the `gitClone` `depth` description: delete "Omit for a full clone.", leaving "Shallow-clone depth.").

Binder:

1. Extend the `stageWorkspace` inputs with the normalized `gitRepository` url set of the matched pool's `SandboxTemplate` `runtimeRef` (§14 rule 1) and the pool's `workspaceSizeLimitBytes`, carried on the bind request from `resolve.go`'s match at both call sites. Extend `upload.RuntimeAllow` or add a sibling struct; either way both call sites pass both values.
2. In `rewriteExtractedSources`, a source eligible under §14 rule 1 skips `extractGitCloneSource`. The binder calls `Mirror.Ensure` and `Mirror.ValidateTree` with the source's `path`, the Runtime's `upload.RuntimeAllow`, and the limit, increments `lenny_workspace_git_cache_sources_total`, and forwards the `gitClone` source unchanged with its `resolvedCommitSha`. A mirror failure fails the workspace materialization on the path a failed gateway clone takes today.
3. Add `upload.ReasonWorkspaceSizeLimit Reason = "workspace_size_limit"`, so the existing `*upload.ValidationError` mapping in `podclaimerror.go` surfaces it. Change the `writeUploadArchiveLimitExceeded` message for that reason so it does not claim an upload violated a §13.4 archive ceiling. `workspace_size_limit` does not increment `lenny_upload_extraction_aborted_total`.

Adapter:

4. **Guard.** The `gitClone` case in `MaterializeWithPolicy` admits a source per §14 rule 1's adapter clause and keeps the existing fail-closed error for every other `gitClone`. Whether the pod holds a store for the url decides only whether alternates and a have are used. It never decides admission.
5. **Injection.** Pass a git-pack fetcher interface, backed by the GatewayControl client and CODE-4's url map, into `MaterializeWithPolicy` through `ArchivePolicy` or a new options struct. A nil fetcher with an admitted `gitClone` source fails closed.
6. **Construction.** Per §14 rule 3, create the git directory in a fresh mode-0700 directory under `/workspace/staging/`, as CODE-6 bullet (2) does, with `GIT_DIR=<dir> git init` followed by `git config core.bare false`. Do not pass a work tree to `git init`, because it records an absolute `core.worktree` that points into the build directory after the move. Run every later git command with `GIT_DIR=<dir>` and `GIT_WORK_TREE` set to the source path in the promotion build directory, so the existing post-promotion symlink re-validation covers the checked-out tree. After the checkout, remove any entry at `<path>/.git` and rename the directory there. The alternates entry is an absolute path to `<store>/objects`. Write the `shallow` file before `index-pack` when the header marks the pack shallow: `index-pack --strict` checks commit parents and fails on a shallow commit whose parent is not yet declared shallow. Check out with `git checkout --force --detach <resolvedCommitSha>`. Without `--force`, git refuses to overwrite an untracked same-path file that an earlier source or a restored tree left (§14 **Path collision rule**).

### CODE-6 · Capture copy and restore completion

Targets: `pkg/adapter/checkpoint.go`; `pkg/adapter/resume.go`; `pkg/adapter/gitstore.go`; `pkg/adapter/scrub` (`resolve_linux.go`).

Steps 1 and 2 reach each slot repository only through one helper in `gitstore.go`, which implements the §13.4 exception for slot repositories:

- (1) It opens the git directory, then `objects`, `objects/info`, and `objects/pack`, with `openat2` and `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` from a descriptor on the slot root, reusing the `pkg/adapter/scrub` resolver (exported, or moved to a leaf package the scrub also calls). A failed open fails the capture or resume.
- (2) Per operation it creates a fresh mode-0700 git directory under `/workspace/staging/` and removes it afterwards. Through the opened descriptors it copies the slot repository's `HEAD`, `packed-refs`, `refs/`, `logs/`, `index`, `shallow`, and `worktrees/` into it, copying only regular files and directories; any other entry, or a `reftable/` directory, fails the operation. Git runs with `GIT_DIR` set to that directory, `GIT_OBJECT_DIRECTORY` set to the `/proc/self/fd` path of the opened objects descriptor passed to git as an inherited file, `GIT_CONFIG_NOSYSTEM=1`, and `GIT_CONFIG_GLOBAL=/dev/null`, so the slot's `config` and hooks are never read by git.
- (3) It reads the `lenny.*` keys §14 rule 3 records from a copy of the repository's `config` with `git config --file <copy> --no-includes`, and passes `lenny.baseCommit` to git after `--end-of-options`.
- (4) Git writes packs and indexes into the scratch directory, and the helper moves each `.pack`, `.idx`, and `.rev` into `objects/pack` with `renameat` against the opened `objects/pack` descriptor. It writes and deletes `objects/info/alternates` with `openat` and `unlinkat` against the opened `objects/info` descriptor, and writes `shallow` into the scratch git directory before indexing and into the slot with `openat` against the opened git-directory descriptor. Restore completion's rewrite or removal of `lenny.storeCommit` (§14 rule 6) edits a copy of `config` with `git config --file <copy>` and moves it over `config` with `renameat` against the opened git-directory descriptor.

1. **Capture copy** (§14 rule 5). A helper `copyStoreOnlyObjects(ctx, repo)` lists `git rev-list --objects --all --reflog --indexed-objects --not <lenny.baseCommit>`, keeps the objects not present in the repository's own object directory, and writes them with `git pack-objects` into `objects/pack`. Call it for every repository step 2.1's walk finds, at two points: before `probeWorkspaceBytes`, and immediately before `streamChunks`. For a Full-level runtime the second point is after `RequestCheckpoint` returns. A failure at either point fails the capture through the existing failure path.
2. **Restore completion** (§14 rule 6). After `restoreChunks` and before session connectors and `Runtime.Start`:
   1. Walk the restored tree with `filepath.WalkDir`, never following symlinks, for `.git` entries that are directories and whose config carries `lenny.gitStoreUrl`.
   2. Overwrite `objects/info/alternates` from CODE-4's url map, or delete it, without reading it.
   3. Skip the fetch unless §14 rule 6 calls for one, comparing `lenny.storeCommit` with the store commit in CODE-4's url map.
   4. Otherwise call `FetchGitPack`. Before indexing, refuse when the restored bytes plus the bytes received exceed `workspaceSizeLimitBytes`, returning `FailedPrecondition` as the existing pre-extraction size check in `resume.go` does. Index as CODE-5 step 6 states.
   5. Run the connectivity check `git rev-list --objects --all --reflog --indexed-objects --not <baseCommit>` and fail the Resume with `FailedPrecondition` on any missing object.
3. When proposal 0109 has landed, also call restore completion after its `FinalizeWorkspace` restore seed and before the plan's sources apply (see the summary's **Impacts on other proposals**).

## Staged schema, chart, and migration changes

The proto change and the gateway mirror volume are part of CODE-3, and the CRD change is part of CODE-2. No migration is needed: the design writes no durable state.

## Staged docs changes

### REC-1 · File the defects this proposal does not stage

Targets: `BUILD-GAPS.md`; `gateway-runtime-comms-remediation.md` §10.2.

File each entry of the summary's **Defects in the shipped tree that this proposal does not stage** as a finding in both records. CODE-1 files the missing git binary.

## Testing

### INFRA-1 · Spike as a gating deliverable

Target: `tests/tier7a_load_local/gitcachespike` (new), behind its own build tag and outside the default tier runs.

The spike runs before CODE-3 to CODE-6 are written. It drives `git` directly and needs no platform code.

1. Use a public repository of at least 2 GiB packed size and at least 100 000 files, with two commits about 7 days apart. Set the test pool's `workspaceSizeLimitBytes` explicitly above the materialization bound of the newer commit (§14 rule 2), and record both values.
2. Measure and report each phase separately:
   - (a) today's cold gateway path: clone time, or the `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` sub-code;
   - (b) the mirror delta fetch plus `pack-objects` for `want ^have`;
   - (c) the pod-side steps: `init` with alternates, then `index-pack --strict` of the delta pack;
   - (d) the detached checkout alone;
   - (e) a cold `clone --no-checkout` followed by a checkout of the same commit, with download and checkout timed separately;
   - (f) the store's on-disk size and the slot object directory's size.
3. Pass when all hold:
   - (c) plus (d) succeeds for a tree over 10 000 files and 256 MiB;
   - (c) is at most 25% of the download phase of (e);
   - the slot object directory is under 5% of the store's size;
   - (d) is at most 60 seconds, or the result is recorded as a checkout-cost finding.
4. When the first or third condition fails, or (d) exceeds its bound, implementation stops and the proposal returns to change-proposal to reconsider sparse checkout, a reused tree, or a store size bound.
5. Record the results in this proposal's deviations file and in `BUILD-PROGRESS.md` before any CODE step after CODE-2 starts.

### TEST-1 · Tests across the reached tiers

Every test carries a `// spec:` annotation naming the section it pins, and tier-2-and-up tests carry a `// diagnosis:` comment. Table-driven cases are one row per rule case.

Tier 1 (unit):

1. `gitref.NormalizeURL`: case, trailing `/`, trailing `.git`, both together, and a url that differs only in path case (not equal). `// spec: 6.4 (Pod Filesystem Layout)`
2. Eligibility (§14 rule 1): `auth` present, `depth` set, `submodules: true`, url absent from the set, and url present after normalization. `// spec: 14 (Workspace Plan Schema)`
3. `ValidateTree` (§14 rule 2), one row per refusal: `..` component, absolute path, a 33-component path, a 4 097-byte path, a gitlink, a symlink under a Runtime without `allowSymlinks`, a symlink escaping the slot root, a symlink into `/proc`, and a tree whose bound exceeds the limit by one byte next to one exactly at the limit. The escape and `/proc` rows run with `allowSymlinks: true`. Each row asserts the `upload.Reason`. A memo row validates one tree against a 4Gi limit, then a 512Mi limit, and asserts the second refuses with `workspace_size_limit`. A second memo row validates one symlink-bearing tree with `allowSymlinks` true, then false, and asserts the second refuses with `symlink`. `// spec: 14 (Workspace Plan Schema), 13.4 (Upload Security)`
4. `podclaimerror` maps each `ValidateTree` reason to `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with the matching `details.reason`, and `workspace_size_limit` leaves `lenny_upload_extraction_aborted_total` unchanged. `// spec: 15.1 (REST API)`
5. Pack selection (§4.7.1): an unknown have triggers exactly one fetch-by-SHA; a have that resolves yields `delta`; a have still unknown yields `full` with `shallow` true; a delta whose object bytes exceed the tree's blob and tree bytes yields `full`. The discriminating assertion is the fetch count of one. `// spec: 4.7.1 (Role and Gateway RPC Contract)`
6. Adapter guard: a non-40-hex `resolvedCommitSha`, `auth` present, and `depth` set are refused; a url absent from the warm-fill map is admitted and takes the shallow pack with no alternates file. `// spec: 14 (Workspace Plan Schema)`
7. Warm fill: a missing gateway address with a `gitRepository` entry is fatal; deadline expiry is fatal; a pack missing the announced commit is fatal; store modes are 0755 and 0444 after fill; a nested `destPath` is created. `// spec: 6.4 (Pod Filesystem Layout)`
8. Every adapter git invocation carries `protocol.allow=never` (assert on the command builder). The CODE-6 slot-repository helper, under both the capture copy and restore completion: a marker script named by `core.fsmonitor`, `core.hooksPath`, and `include.path` in the slot `config` leaves no marker; a `lenny.baseCommit` of `--all` fails; `objects/pack` or `objects/info` symlinked to the store fails and leaves the store's file list unchanged; a `.git` file pointing at the store is skipped; a `reftable/` directory fails. Slot construction (§14 rule 3) over a build tree whose source `path`, or the in-tree directory that a symlink at `path` or at a parent component of `path` names, already holds a `.git` with those keys and a `post-checkout` hook leaves no marker and yields a repository whose config has no `core.worktree`, and a same-path file an earlier source wrote holds the pinned content. A race that swaps `objects/pack` between a directory and a symlink to the store, run 1 000 times during the capture copy, never changes the store's file list; this is the discriminating assertion. `// spec: 13.4 (Upload Security), 14 (Workspace Plan Schema)`
9. One row in the existing `pkg/adapter` scrub table asserts `/workspace/shared/{destPath}` survives the whole-pod scrub. `// spec: 6.4 (Pod Filesystem Layout), 5.2 (Pool Configuration and Execution Modes)`
10. Capture copy (§14 rule 5): a session commit that reverts to a store-only blob is copied; a copy failure fails the checkpoint through the existing failure path; the probe counts the copied bytes. `// spec: 14 (Workspace Plan Schema), 4.4 (Event / Checkpoint Store)`
11. Restore completion (§14 rule 6): store at the same commit (no `FetchGitPack` call), a delta-built slot whose own pack holds `baseCommit`'s commit object, restored onto a store at a different commit (fetches; this is the discriminating case), no store (shallow), a slot that holds `baseCommit` through the shallow fallback (no call), a second restore onto the first store after a delta restore onto another (fetches, because rule 6 rewrote `lenny.storeCommit`), a restored alternates file pointing outside the store (overwritten, contents never read), a symlinked directory in the walk (not followed), an over-limit pack (resume fails), and a missing object after completion (resume fails). `// spec: 14 (Workspace Plan Schema)`

Tier 2 (component):

12. CRD CEL: `content` or `mode` on `gitRepository` rejected; `url` or `gitRef` missing rejected; a non-https url rejected; a 40-hex `gitRef` rejected by CEL and by admin validation. Admin validation rejects duplicate normalized urls across a base and a derived Runtime after merge. `applyCRDFields` mirrors a `gitRepository` entry into `runtimestore`. `// spec: 5.1 (Runtime), 6.4 (Pod Filesystem Layout)`

Tier 3 (contract):

13. `FetchGitPack`: the header arrives first and the adapter rejects a stream that starts with a chunk; `PERMISSION_DENIED` for a url absent from the pod's Runtime; `INVALID_ARGUMENT` for a ref `want` with haves; a call without a verified client certificate or a valid SA token is refused on the stream interceptors; with the Upload Handler breaker open the call still serves, and failing calls leave that breaker closed. `// spec: 4.7.1 (Role and Gateway RPC Contract), 10.3 (mTLS PKI), 4.1 (Edge Gateway Replicas)`
14. Metrics: `kind` is `warm`, `delta`, and `full` for the three request types, a restore-completion request is recorded, and the counter increments once per routed source. `// spec: 16.1 (Metrics)`

Tier 4 (`tests/tier4_integration`, local HTTPS git fixture):

15. A session with a cache-served source on a pod with a store, an older store, and no store materializes the same tree in all three cases.
16. A truncated `FetchGitPack` stream on warm fill, on the session delta or shallow fetch, and on restore completion each fails on its spec-named path; a mirror volume at `sizeLimit` during `Ensure` rebuilds once and then fails with `RESOURCE_EXHAUSTED`.

Tier 5 (`tests/tier5_e2e_kind`):

17. On a sidecar-model pool, a runtime write under a populated store returns `EROFS`; the existing podspec read-only-mount test is extended to assert the mount with a `gitRepository` asset present.
18. A pod whose warm fill fails takes `failed`, on a pod-warm pool and on a `preConnect` pool.
19. A checkpoint captured on one pod restores on a replacement pod whose store is at a different commit, and the session's repository passes `git fsck --connectivity-only`.

Tier 7a:

20. Concurrent `Ensure` and `Pack` calls for one url run single-flight; 50 concurrent warm fills against one replica cause one remote fetch; with the pack limit at 2, a third concurrent `Pack` waits until one returns. Run under `-race`.

Tier 9:

21. A pod on a Runtime without a `gitRepository` entry for a url receives `PERMISSION_DENIED`; the pod's egress under `restricted` reaches no git host during warm fill or session use.

## Edge cases and accepted failure modes

- **Spoofed `pod_id`.** `FetchGitPack` follows the `ReportPodScrub` precedent and does not bind `pod_id` to the client certificate. A caller holding any valid pod certificate can read public repositories another Runtime names. This is accepted; the defect is filed under REC-1.
- **Mirror loss on a gateway restart.** The mirror is an `emptyDir` cache. The next request refetches.
- **A gateway replica without the source's commit.** `Ensure` fetches it by SHA. A remote that deleted the commit returns `NOT_FOUND`, and the materialization or restore fails.
- **Store staleness on recycling pools.** A store is never refreshed. A fresher store arrives with a replacement pod, bounded by the existing recycle limits.

## Files touched on application (non-spec)

- `Dockerfile`, `Dockerfile.git` (new), `.github/workflows/release.yml`, `scripts/cloud/aws/build-images.sh`, image-build targets in `Makefile` and `tests/testinfra/kind`
- `schemas/lenny-adapter.proto`, `pkg/proto/adapter/v1` (regenerated), `schemas/workspaceplan-v1.json`
- `pkg/apis/lenny/v1alpha1/runtime_types.go`, `pkg/embedded/crds/lenny.dev_runtimes.yaml`, `charts/lenny/crds/lenny.dev_runtimes.yaml`
- `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/externalapi/admin/runtimes.go`, `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/ops/mcp/generated_tools.go`
- `pkg/controller/runtime/controller.go`, `pkg/controller/sandbox/controller.go`, `pkg/controller/sandbox/podspec/shared_assets.go`
- `pkg/gateway/provisioning/gitref/mirror.go` (new), the `FetchGitPack` handler in `pkg/gateway/mcpfabric/delegationtree/leasecontrol`, `cmd/lenny-gateway/gwcontrol_server.go`
- `pkg/gateway/podlifecycle/podsession/archiveextract.go`, `workspacestage.go`, `bindprepare.go`, `slotbinder.go`, `resolve.go`
- `pkg/upload/upload.go`, `pkg/gateway/sessionserver/podclaimerror.go`
- `pkg/observability/metrics/catalog.go`, `catalog_test.go`
- `charts/lenny/templates` (gateway deployment), `charts/lenny/values.yaml`
- `cmd/lenny-adapter/main.go`, `pkg/adapter/gitstore.go` (new), `pkg/adapter/gatewaylink.go`, `pkg/adapter/sharedassets/sharedassets.go`, `pkg/adapter/workspace/materialize.go`, `pkg/adapter/checkpoint.go`, `pkg/adapter/resume.go`, `pkg/adapter/scrub`
- `cmd/runtimes/preconnect-echo`, `cmd/runtimes/echo-embedded`
- `tests/tier7a_load_local/gitcachespike` (new), tests under the tiers TEST-1 names
- `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`, `BUILD-PROGRESS.md`
