# Steps: 0153 Workspace materialization

## Validation record

**The materialization spike, 2026-11-12.** A kind v1.35.0 cluster on Docker Desktop for arm64, kernel 6.12.76-linuxkit, with runc and with gVisor `release-20261109.0` on the systrap platform, in a namespace enforcing Pod Security Standards Restricted, with read-only root filesystems. The slot ran as UID 20000 and the adapter as UID 10000. The spike ran on the layout before SI D10, with the worker's working directory at the volume root `/workspace/slots` and the helper staged in `/stage`, and its results carry over to the owner's final layout, where the volume root is `/workspace` and the helper is in `/lenny/bin` **[inference]**. The cluster has been deleted. Its code and logs were under the session scratchpad's `spike-mat/`: `sup/` held `fdin.c` and the patched supervisor, `sm/` held the helper (`mat.go`) and the adapter (`adapter.go`, `proto.go`), `k8s/gen.sh` rendered the pods, and `logs/` held the runs `mr` and `mg` (runc and gVisor, 512Mi slot), `eq-mr` and `eq-mg` (equal 1Gi limits), and `mr-mem`, `mr-mem1g`, and `mr-mem1g-b` (memory runs).

The code and logs of every run below lived in a session scratchpad that did not persist, and this change has no `evidence/` folder. The tables below are the only record of the results, so a **[measured]** claim can be re-checked only by repeating its run.

**Protocol, as the materialization spike built it.** The spike's prototype passed content descriptors on `setup` itself, at descriptor 6 and above, and ran a synchronous worker. The asynchronous-worker spike below replaced both.

| Check | Result, runc and gVisor |
| :-- | :-- |
| `ping` with one stray descriptor | `pong` with `malformed_request` |
| `setup` naming 3 descriptors and carrying 1 | `setupdone` with `malformed_request` |
| `setup` naming 254 descriptors | `setupdone` with `malformed_request` |
| `ping` after those | `pong` |
| Helper writes to a content descriptor | `EBADF` |
| Helper reopens a content descriptor writable through `/proc/self/fd` | `EACCES` |
| Helper `fchmod`, `ftruncate`, `openat` relative to a content descriptor | `EPERM`, `EINVAL`, `ENOTDIR` |
| Content descriptor's device and inode against the adapter's `fstat` | Equal |
| Supervisor C added over the broker prototype | 179 lines, 156 neither blank nor comment, 4,052 to 4,231; `.text` 21,264 to 22,240 bytes; freestanding with `-Werror` |

**Reach of a naive extractor.**

| Target | As the slot UID | As the adapter UID |
| :-- | :-- | :-- |
| `/tmp` | Written (the slot's own) | Written (the adapter's own) |
| Warm stores | `EROFS` | `EROFS` |
| Stage volume, `/run/lenny` | `EROFS` | `EROFS` on the read-only root, because the adapter does not mount them |
| Spool volume | `EROFS` on the read-only root, because the slot does not mount it | Written |
| Another slot's workspace volume | `EROFS` on the read-only root, because the slot does not mount it | Written |
| `/etc` | `EACCES` | `EACCES` |

**Forged results.** An oversized result (65,537 bytes), an unknown member, two concatenated objects, an unknown code with a negative index, an out-of-range warning index, and a stall ending at the deadline with `cleanup_timeout` each failed the session. A well-formed lie that claimed 10 bytes passed validation after the helper wrote 64 MiB, which the adapter measured at 67,108,903 bytes. A sibling process, the spike's runtime process running beside the helper, failed to forge the result in 160 of 160 attempts on each runtime, across four variants of 40 attempts with the helper readable or exec-only and dumpable or not.

**Hostile archives, with identical verdicts under runc and gVisor.**

| Archive | Outcome |
| :-- | :-- |
| Zip-slip in `tar.gz` and zip; absolute paths in `tar` and zip | `path_escapes_root` |
| Symbolic link to `/tmp`, links refused | `symlink` |
| Symbolic link to `/tmp` or `../../..`, `allowSymlinks` | `symlink_target_escapes_root` |
| In-tree link, then a write through it, `tar` and zip | `path_crosses_symlink` |
| Hard link outside and inside the tree, character device, FIFO | `non_regular_entry` |
| Modes `04755`, `02755`, `01777` | Written `0755`, `0755`, `0777`, owned by UID 20000 |
| 10,001 entries; depth 41 | `max_entry_count`; `max_path_depth` |
| 2 GiB zeros in `tar.gz`, §7.4 values | `max_entry_size` at once; with a 256 MiB total and no ratio, `max_decompressed_size` at 268,435,457 bytes |
| 2 GiB zeros, 4 GiB ceilings, ratio 100 | `max_decompression_ratio` at 260,519,401 bytes (`tar.gz`) and 260,523,801 bytes (zip) |
| 2 GiB zeros in zip, 64 MiB entry, no ratio | `max_entry_size` at 67,108,865 bytes |
| Zip with a size header of 10 bytes over 512 MiB | `format_error` |
| `stripComponents: 1`, `tar.gz` with a collision; zip | One skip warning per short entry and one collision warning |

The helper reported a resident memory between 3,684 and 6,404 KB across these runs.

**Performance, equal 1Gi limits, seconds as helper / broker `mkfile` from the adapter / adapter writing directly to the spool.**

| Archive | runc | gVisor run 1 | gVisor run 2 |
| :-- | :-- | :-- | :-- |
| 100,000 files, `tar.gz` | 1.53 / 2.92 / 1.51 | 5.11 / 3.88 / 3.89 | 4.95 / 4.05 / 4.04 |
| 100,000 files, zip | 2.04 / 1.60 / 2.02 | 5.97 / 5.19 / 5.44 | 6.16 / 5.19 / 5.25 |
| 4 × 256 MiB, `tar.gz` | 1.67 / 1.16 / 1.06 | 0.98 / 0.81 / 0.87 | 0.91 / 1.18 / 1.11 |
| 4 × 256 MiB, zip | 1.33 / 1.12 / 1.08 | 2.42 / 1.31 / 1.77 | 1.41 / 1.19 / 1.51 |

Helper resident memory: 20 to 33 MB for 100,000 files in `tar.gz`, 61 to 75 MB in zip, and 4 to 19 MB for the large files.

**Memory runs.** A 512Mi slot with an unlimited adapter under runc took 9.8 to 20.2 s for 1 GiB of large files from `tar.gz` and 37.7 to 42.5 s from zip, against 0.7 to 1.3 s under gVisor. In `eq-mr`'s second back-to-back run, the slot's supervisor connection closed after 69,200 of 100,000 files of the broker-path baseline, and every later step found the slot gone, with 2.2 GB on the volume; the study attributed it to an out-of-memory kill of the 1Gi slot. In `mr-mem1g`, whose recorded manifest limits both the adapter and the slot to 1Gi, the helper's runs on 100,000 files completed (1.69 s for `tar.gz` and 2.01 s for zip), the broker-path baseline took 9.51 s for `tar.gz`, the controlling command in the adapter container was killed with exit status 137 during the zip baseline, and later commands found no adapter container. `mr-mem` and `mr-mem1g-b` recorded only that the adapter container was gone.

**The asynchronous-worker spike, 2026-11-12 to 2026-11-13.** A kind v1.35.0 cluster on the same arm64 host, with runc and with gVisor `release-20261109.0`, on the merged broker and materialization supervisor. Its final tree was the session scratchpad's `spike-async/sup`, the cumulative diff `async.diff`, and the review rounds `fixes.diff`, `fixes2.diff`, and `fixes3.diff`; its final logs were `spike-async/logs/r5/` and the `final-*` runs. Three independent reviews read the diff, and the last found nothing at Medium or above. Results, identical under runc and gVisor except where stated **[measured]**:

| Check | Result |
| :-- | :-- |
| Size | Net +376 lines of C across all rounds; the supervisor is 4,915 lines; freestanding with `-Werror` |
| A second `setup` during a run | `setupdone` with `worker_busy` in 0.1 ms; its log pipes at end-of-file at once and its cancel pipe broken; `cleanup`, `sweep`, and `launch` refused the same way under their own reply types |
| `fs` and `ping` during a run | Served in 0.1 ms; the run completed afterwards |
| Three walks of 100,100 entries during a 60 s `setup` | 224 to 245 ms each in 502 requests, against 227 ms with no run; 591 pings answered, 99th percentile 0.2 ms |
| Cancel by close | `setupdone` with `signal` 9 and `setup_cancelled` 1.1 to 1.6 ms after the close |
| A byte on the cancel pipe | No reply 1.5 s later; the run went on until the close |
| Deadline of 2 s | `setup_timeout` with `signal` 9 after 2,008 ms; no process of the program's group remained |
| A program that left a child in its group, a `setsid` child, and a double-forked grandchild | After `setupdone`, the same-group child was gone and the two others ran until the next End, which removed them |
| `end` during a run | The run's `setupdone` with `setup_cancelled`, then `ended` |
| Release order end, sweep, cleanup, end, launch | Each answered in order |
| `cwd` of `..`, a link, `/etc`, and a missing directory | `setup_failed` with `reason` `cwd` and errno 18, 40, 18, and 2, in 0.1 ms |
| `execve` of a missing program | `setup_failed` with `reason` `exec` and errno 2 |
| `fsizeBytes` exceeded | `signal` 25 (`SIGXFSZ`) with `setup_failed` |
| Empty environment and a `cwd` below the root | The program saw no variable and its `cwd` |
| 200,000 `fs` requests with a `setup` among them | Every reply in order with the right descriptor count, and one `setupdone` between them |
| A slot holding every in-flight descriptor its UID allows | Its own `fs` requests got errno 109 and no descriptor; `ping` and a later `setup` were served |

**The AWS checkpoint re-validation, 2026-11-12, on the O11 git steps (runc, LLVM).** A no-op through the synchronous worker took 12 ms. A detached checkout over warm-clone alternates took 19.2 s, and a broker request sent 0.1 s after it waited 19.1 s. The capture copy, `git repack -a -d -l`, did not finish within the adapter's 120 s broker deadline, the adapter declared the slot dead, and the pod's checkpoints failed afterwards; the same happened on Linux. The index refresh steps returned `cleanup_failed`, so the loose-object count with and without the 1 s delay was not obtained **[measured]**. These results led to D10.

**Earlier evidence this design uses.** Spike 1 of 2026-11-12 measured the index refresh's effect, and spikes 2 and 3 measured the layout, `FICLONE` between two volumes of one filesystem, `copy_file_range` under gVisor, and the broker, as [`0145-session-isolation`, "Validation record"](../0145-session-isolation/steps.md#validation-record) records.

## Validation owed

- Slot memory and the memory-cgroup behaviour of materialization on managed node filesystems on EKS, GKE, and AKS, with `runtimes.minSlotMemoryBytes` and pacing, at 512Mi, 1Gi, and 2Gi, with 1 GiB and 4 GiB plans run back to back, and the cause of both kills in the memory runs. Waits on it: 0153.S7.
- x86_64; every run was arm64. Waits on it: 0153.S2 and 0153.S4.
- The pinned git built static and staged `0111`, and every O11 step in the helper: init, alternates, the fetch through `netagent`, the tree-size check, checkout, the move, the index refresh with and without the 1 s wait, the capture copy, and restore completion. Waits on it: 0153.S4.
- The fetch grant in `netagent`. SI O9's attribution under runc, gVisor, and Kata is measured: the AWS spike of 2026-11-14 found that runc answers `sock_diag`, and gVisor and Kata answer only `/proc/net/tcp`, with the right slot UID on every answering reader ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution); [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants), revision 83). The grant itself, with the production `netagent`, is still owed. Waits on it: 0153.S4.
- The overlay directory under the owner's final layout, with the working directory at the volume root, the no-replace fallback, and a checkpoint walk running during an overlay. Waits on it: 0153.S5.
- The gap that a directory-descriptor cache would close, on gVisor and on managed nodes (D13). No step waits on it; it decides whether a later release adds the cache.
- The content socket: one source at a time with done pipes under runc, gVisor, and Kata, the cost per source for plans of many small uploads, truncation of a staged file the helper still holds, and the descriptor checks of "WM-4" on a descriptor received over a socket. Waits on it: 0153.S2.
- `FICLONE` from the spool into the workspace on the node filesystems of EKS, GKE, and AKS, and the fallbacks under gVisor and Kata. Waits on it: 0153.S2 for staged uploads and 0153.S8 for the reflink rebuild.
- The warm-up checkout: `prestage` times for Chromium and Linux by checkout and by reflink rebuild; whether the index refresh after `git read-tree` re-reads every file, and whether a copied index under `core.checkStat=minimal` avoids it; the cost of deleting an unused Chromium tree in the background and its effect on the session's own disk use and broker latency; and the incremental checkout at bind when the commit moved. Waits on it: 0153.S8.
- Kata, through a kubelet. Waits on it: 0153.S2.
- A distroless runtime image, which has no shell and no git of its own. Waits on it: 0153.S4.
- `pidfd_getfd` and `ptrace` from a sibling against the helper and its git children, under the RuntimeDefault seccomp profile and gVisor. Waits on it: 0153.S2.
- `RLIMIT_FSIZE` and `SIGXFSZ` under gVisor through a kubelet, and the empty environment with the pinned git. Waits on it: 0153.S1 for `RLIMIT_FSIZE` and 0153.S4 for the pinned git.
- `lenny-store-fill`'s feasibility, which the owner made a condition of D20: the stores readable by every slot's UID; the adapter's deletion of a store whose files the fill's UID owns, on runc, gVisor, Kata `block-plain` with the dedicated `fsGroup`, and Kata virtio-fs; `netagent`'s attribution of the fill's UID as the platform fill; and the fill's UID mapped within the pod's range under user namespaces. The hostile-repository stress test suite of D23 runs as tests on runc, gVisor, and Kata: setgid and setuid files, symbolic-link and hard-link farms, deep and wide trees, directories of mode `000`, name collisions, very large files, and a deletion racing a slot's reads, as PL's "Validation owed" lists it. Waits on it: 0153.S9, which the owner's condition on D20 gates.
- The shared staging space under concurrent imports on a pod with four slots: the waits, the order, and the spool's kubelet count against `spoolSizeLimitBytes` on gVisor's default mount. Waits on it: 0153.S2 and 0153.S5.
- The helper's writeback pacing and its effect on the 512Mi throttling. Waits on it: 0153.S7.
- The earliest runsc release that supports the supervisor's `close_range`, `openat2`, and `STATX_MNT_ID`; `release-20261109.0` is verified. Waits on it: 0153.S1.

## Steps

### 0153.S1 Asynchronous worker and setup
- status: Planned
- aliases: [WM-A, WM-3, WM-11]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0145.S3, 0147.S6]
- closes: [T-7.5.4]

The supervisor's asynchronous worker and `setup` with descriptors, log pipes, cancellation by close, the process-group kill, `env`, `cwd`, `RLIMIT_FSIZE`, the refusal rule, and the adapter's per-slot run queue, which sends each setup command of `RunSetup` as its own `setup`. Which of this step and [0145.S3](../0145-session-isolation/steps.md#steps) owns the worker and its refusal rule, and what a `freeze` does during a `cleanup` run, is open decision O14 in `decisions.md`, which matches O20 of `0145-session-isolation`.

It closes T-7.5.4: `RunSetup` sends one `setup` per command, and the `contract` tests of `setup` and `setupdone` replace the missing contract test of `RunSetup`.

The step needs SI-A (slot containers, [0145.S1](../0145-session-isolation/steps.md#steps)) and SI-C (D6's `setup`, [0145.S3](../0145-session-isolation/steps.md#steps)). It also needs DR-12's plan and wire contract ([0147.S6](../0147-tenant-derived-runtimes/steps.md#steps)), which adds `setupCommands[].cwd`, the field the `setup` request's `cwd` carries.

### 0153.S2 lenny-materialize for files and uploads
- status: Planned
- aliases: [WM-B, WM-1, WM-2, WM-4, WM-5, WM-6, WM-7, WM-8, WM-16]
- lanes: [specification, code, documentation]
- depends-on: [0153.S1, 0145.S2, 0118.S4, 0131.S7]
- closes: [F-13.4.24 (part), F-8.7.16 (part)]

`lenny-materialize` for `inlineFile`, `mkdir`, and `uploadFile`; per-source staging, the content socket and done pipes, `spoolSizeLimitBytes` with the shared staging space, its wait, its admission floor, and the gateway's refusal at upload, reflink clones of staged files, the plan memfd, and result validation; staging in `/lenny/bin`; pre-start `materialize` with the working-directory check through the broker.

Until 0153.S3 removes the gateway's extraction, the gateway's `rewriteExtractedSources` still sends each archive entry as an `uploadFile` source, with `mkdir` sources and the internal `symlink` source variant. The helper of this step therefore also applies the internal `symlink` source under the link rules of "WM-9", and each extracted entry is one staged content source, which "Limits" names as the inefficient case.

The step needs SI-B (the broker, [0145.S2](../0145-session-isolation/steps.md#steps)) and PL-B ([0118.S4](../0118-agent-pod-layout/steps.md#steps)), which renders `lenny-spool` with `spoolSizeLimitBytes` as its `sizeLimit`; this step lands the staging accounting, its wait, and its admission floor. The staging space and its admission floor use `checkpointCaptureBudgetBytes` and `spoolHeadroomFactor`, which IC-7 ([0131.S7](../0131-incremental-checkpoint/steps.md#steps)) defines. The carrier of the per-source request on `PrepareWorkspace` is open decision O12 in `decisions.md`.

It closes the part of F-13.4.24 that covers pre-start writes without a seed: every write of `inlineFile`, `mkdir`, and `uploadFile` sources resolves under `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` at the slot UID. It closes the materializer half of F-8.7.16, a parent's exports written into the child by the helper at the child's slot UID; [0147.S3](../0147-tenant-derived-runtimes/steps.md#steps) closes the setup-command half.

### 0153.S3 Archive extraction in the helper
- status: Planned
- aliases: [WM-C, WM-9]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2]
- closes: [T-7.4.1, T-7.4.2, T-7.4.3, T-7.4.13]

Archive extraction in the helper and its removal from the gateway, the internal `symlink` source variant included; the §7.4 and §13.4 rewrite. The gateway's extraction stays until 0153.S3 lands, and 0153.S2's helper applies the sources it emits, so nothing breaks between steps.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-RecordExtractionAbort`, as archive extraction leaves the gateway (WM-9), unless open decision O15 keeps the gateway counting `lenny_upload_extraction_aborted_total`, in which case it reshapes the operation to count the reasons that `FinalizeWorkspace` maps.

It closes T-7.4.1 and T-7.4.2 with the helper's tests against the hostile archives of "Validation record", at `unit` and at `component` through the content socket, which replace the gateway extractor those findings name. It closes T-7.4.3 and T-7.4.13 because the mechanisms they leave untested, the post-promotion symlink re-validation and the Upload Handler's extraction isolation, leave the tree with this step.

### 0153.S4 Git in the helper
- status: Planned
- aliases: [WM-D, WM-10]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0127.S3, 0131.S1, 0127.S5 (part), 0153.S9 (part)]

Git in the helper: the pinned git, `gitClone`, the tree-size check, the index refresh, `capture-copy`, and `restore-complete`, and the adapter's fetch-grant client of WMR-24, which registers a grant before a fetching helper run and revokes it at `setupdone`. Which step owns the adapter's calls to `capture-copy` and `restore-complete`, given that [0131.S1](../0131-incremental-checkpoint/steps.md#steps) runs them and this step depends on it, is open decision O17 in `decisions.md`, which matches D27 of `0131-incremental-checkpoint`.

The step needs EG proposal 3 ([0127.S3](../0127-egress-and-git/steps.md#steps)), which is also the egress step that carries the fetch grant, and the IC change for the capture copy, which is IC-1's "Git repositories" ([0131.S1](../0131-incremental-checkpoint/steps.md#steps)). Its store parts, step 2 of WM-10 (alternates), `capture-copy`, and `restore-complete`, need the stores of the rewritten draft 0111 ([0127.S5](../0127-egress-and-git/steps.md#steps)) and their fill (0153.S9), and land with them or after them. 0153.S3 and 0153.S4 can land in either order after 0153.S2.

### 0153.S5 Mid-session overlay
- status: Planned
- aliases: [WM-E, WM-12]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0131.S1, 0135.S5 (part)]
- closes: [F-13.4.24 (part)]

Mid-session `overlay` for uploads and child-file imports, `discard`, and the checkpoint's skip of the reserved names.

The step needs IC-1 ([0131.S1](../0131-incremental-checkpoint/steps.md#steps)), and its child-file import part needs the MD-26 proposal ([0135.S5](../0135-message-delivery/steps.md#steps)); the mid-session upload part does not. How the import is ordered against this step is open decision O23 in `decisions.md`, which matches O18 of `0135-message-delivery`.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operation it changes. It reshapes `GWSEAM-WriteWorkspace`, which carries a mid-session upload's file contents to the session core today, as open decision O16 settles where a mid-session upload's bytes wait.

It closes the part of F-13.4.24 that covers writes into a live tree: an overlay moves each entry into place under `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`, and a write through a link the agent planted fails with `path_crosses_symlink`.

### 0153.S6 Resume, derive, and snapshotless rebuild
- status: Planned
- aliases: [WM-F, WM-13]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0153.S4, 0131.S5, 0144.S1]
- closes: [F-13.4.24 (part)]

Resume, derive, and the snapshotless rebuild through the helper.

It delivers and tests the helper, broker, and slot-UID parts of the derive cases of [`0144-session-derive`](../0144-session-derive/change.md): `from: latest` restores the snapshot through the slot's broker and runs `restore-complete`, `from: start` runs `materialize` from the copied plan, and each creates entries owned by the slot UID. [0144.S1](../0144-session-derive/steps.md#steps) tests the snapshot restore and the plan rebuild on the bind paths the tree has when it lands.

It closes the last part of F-13.4.24: a source applied over a restored seed resolves under `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`, so a chained link the seed carries fails the write with `path_crosses_symlink` rather than redirecting it.

The step needs IC-5 ([0131.S5](../0131-incremental-checkpoint/steps.md#steps)) and SD-1 ([0144.S1](../0144-session-derive/steps.md#steps)).

### 0153.S7 Slot memory floor and writeback pacing
- status: Planned
- aliases: [WM-G, WM-14]
- lanes: [specification, code, documentation]
- depends-on: [0121.S1]

Writeback pacing in the helper, and the slot memory floor of "WM-14" against the chart value `runtimes.minSlotMemoryBytes`. [0121.S1](../0121-billing-resource-attribution/steps.md#steps) adds that chart value and the checks that read it, and this step uses the value rather than adding it.

The step waits on the validation of "Validation owed", and on billing decision 7's mapping, which BR-1 ([0121.S1](../0121-billing-resource-attribution/steps.md#steps)) carries.

### 0153.S8 Warm-up checkout
- status: Planned
- aliases: [WM-H, WM-15]
- lanes: [specification, code, documentation]
- depends-on: [0153.S4, 0153.S9, 0118.S2, 0127.S5, 0140.S1, 0131.S4]

The warm-up checkout: `prestage` of the `gitRepository` entries with `prestage: true`, adoption at bind by the first source of each repository, the background `discard`, and the reflink rebuild; `prestageTimeoutSeconds` on a Runtime and on a pool with its rendering onto the adapter (D26); the skip of an entry with `lenny_prestage_skipped_total` and the `PrestageSkipped` event on the `AgentPod`; the runtime write's `Warning` when the staged trees exceed half of `workspaceSizeLimitBytes`; and the contribution of the staged trees to the platform chunk sets on `CH-SOURCECHUNKS`, which IC-4 ([0131.S4](../0131-incremental-checkpoint/steps.md#steps)) adds, with the record of each staged file's identity that seeds the file index at adoption. An edge from 0131.S4 to this step would close a cycle, so this step depends on 0131.S4. How a process that escapes a `prestage` run is contained is open decision O10 in `decisions.md`, and the trust point of the contribution's reads is O11. The usable size of a slot volume that the staged trees are checked against is O20, which matches O13 of `0118-agent-pod-layout`, and the commit key a staged repository records is O22, which matches decision 70 of `0127-egress-and-git`.

The step needs PL-D (no pre-launch, [0118.S2](../0118-agent-pod-layout/steps.md#steps)), the warm list and `prestage` of the rewritten draft 0111 ([0127.S5](../0127-egress-and-git/steps.md#steps), decision 67 of 0127), and the RV proposal that adds `prestage` (D29 of that design, [0140.S1](../0140-runtime-revisions/steps.md#steps)). It lands after pre-launched runtimes are gone, because a pre-launched process could hold a staged tree open. Tenant entries and trees staged from withheld stores arrive with the staging part of DR-11 ([0147.S8](../0147-tenant-derived-runtimes/steps.md#steps)).

### 0153.S9 lenny-store-fill
- status: Planned
- aliases: [WM-I]
- lanes: [specification, code, documentation]
- depends-on: [0118.S4, 0127.S5]

`lenny-store-fill`: the fill program, the store fill and the pristine trees moved out of the adapter, and the adapter's delete-only use of the store volume. [0118.S4](../0118-agent-pod-layout/steps.md#steps) renders the init container and its volumes, and [0127.S5](../0127-egress-and-git/steps.md#steps) lands `netagent`'s attribution of the fill's UID. Which step renders the init container is open decision O19 in `decisions.md`, which matches O11 of `0118-agent-pod-layout`, and what a pod with a non-empty warm list does on a pool that runs no `netagent` is O21, which matches 0118's O14.

The step needs PL-B ([0118.S4](../0118-agent-pod-layout/steps.md#steps)) and the egress proposal that carries decision 61, which is EG-5 ([0127.S5](../0127-egress-and-git/steps.md#steps)). It waits on the `lenny-store-fill` feasibility validation of "Validation owed", which the owner made a condition of D20.
