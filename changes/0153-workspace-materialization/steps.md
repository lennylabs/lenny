# Steps: 0153 Workspace materialization

## Validation record

**The materialization spike, 2026-11-12.** A kind v1.35.0 cluster on Docker Desktop for arm64, kernel 6.12.76-linuxkit, with runc and with gVisor `release-20261109.0` on the systrap platform, in a namespace enforcing Pod Security Standards Restricted, with read-only root filesystems. The slot ran as UID 20000 and the adapter as UID 10000. The spike ran on the layout before SI D10, with the worker's working directory at the volume root `/workspace/slots` and the helper staged in `/stage`, and its results carry over to the owner's final layout, where the volume root is `/workspace` and the helper is in `/lenny/bin` **[inference]**. The cluster has been deleted. Its code and logs were under the session scratchpad's `spike-mat/`: `sup/` held `fdin.c` and the patched supervisor, `sm/` held the helper (`mat.go`) and the adapter (`adapter.go`, `proto.go`), `k8s/gen.sh` rendered the pods, and `logs/` held the runs `mr` and `mg` (runc and gVisor, 512Mi slot), `eq-mr` and `eq-mg` (equal 1Gi limits), and `mr-mem`, `mr-mem1g`, and `mr-mem1g-b` (memory runs).

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

- Slot memory and the memory-cgroup behaviour of materialization on managed node filesystems on EKS, GKE, and AKS, with `runtimes.minSlotMemoryBytes` and pacing, at 512Mi, 1Gi, and 2Gi, with 1 GiB and 4 GiB plans run back to back, and the cause of both kills in the memory runs.
- x86_64; every run was arm64.
- The pinned git built static and staged `0111`, and every O11 step in the helper: init, alternates, the fetch through `netagent`, the tree-size check, checkout, the move, the index refresh with and without the 1 s wait, the capture copy, and restore completion.
- The fetch grant in `netagent`. SI O9's attribution under runc, gVisor, and Kata is measured: the AWS spike of 2026-11-14 found that runc answers `sock_diag`, and gVisor and Kata answer only `/proc/net/tcp`, with the right slot UID on every answering reader ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution); [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants), revision 83). The grant itself, with the production `netagent`, is still owed.
- The overlay directory under the owner's final layout, with the working directory at the volume root, the no-replace fallback, and a checkpoint walk running during an overlay.
- The gap that a directory-descriptor cache would close, on gVisor and on managed nodes (D13).
- The content socket: one source at a time with done pipes under runc, gVisor, and Kata, the cost per source for plans of many small uploads, truncation of a staged file the helper still holds, and the descriptor checks of "WM-4" on a descriptor received over a socket.
- `FICLONE` from the spool into the workspace on the node filesystems of EKS, GKE, and AKS, and the fallbacks under gVisor and Kata.
- The warm-up checkout: `prestage` times for Chromium and Linux by checkout and by reflink rebuild; whether the index refresh after `git read-tree` re-reads every file, and whether a copied index under `core.checkStat=minimal` avoids it; the cost of deleting an unused Chromium tree in the background and its effect on the session's own disk use and broker latency; and the incremental checkout at bind when the commit moved.
- Kata, through a kubelet.
- A distroless runtime image, which has no shell and no git of its own.
- `pidfd_getfd` and `ptrace` from a sibling against the helper and its git children, under the RuntimeDefault seccomp profile and gVisor.
- `RLIMIT_FSIZE` and `SIGXFSZ` under gVisor through a kubelet, and the empty environment with the pinned git.
- `lenny-store-fill`'s feasibility, which the owner made a condition of D20: the stores readable by every slot's UID; the adapter's deletion of a store whose files the fill's UID owns, on runc, gVisor, Kata `block-plain` with the dedicated `fsGroup`, and Kata virtio-fs; `netagent`'s attribution of the fill's UID as the platform fill; and the fill's UID mapped within the pod's range under user namespaces. The hostile-repository stress test suite of D23 runs as tests on runc, gVisor, and Kata: setgid and setuid files, symbolic-link and hard-link farms, deep and wide trees, directories of mode `000`, name collisions, very large files, and a deletion racing a slot's reads, as PL's "Validation owed" lists it.
- The shared staging space under concurrent imports on a pod with four slots: the waits, the order, and the spool's kubelet count against `spoolSizeLimitBytes` on gVisor's default mount.
- The helper's writeback pacing and its effect on the 512Mi throttling.
- The earliest runsc release that supports the supervisor's `close_range`, `openat2`, and `STATX_MNT_ID`; `release-20261109.0` is verified.

## Steps

### 0153.S1 Asynchronous worker and setup
- status: Planned
- aliases: [WM-A, WM-3, WM-11]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0145.S3]

The supervisor's asynchronous worker and `setup` with descriptors, log pipes, cancellation by close, the process-group kill, `env`, `cwd`, `RLIMIT_FSIZE`, the refusal rule, and the adapter's per-slot run queue.

The step needs SI-A (slot containers, [0145.S1](../0145-session-isolation/steps.md#steps)) and SI-C (D6's `setup`, [0145.S3](../0145-session-isolation/steps.md#steps)).

### 0153.S2 lenny-materialize for files and uploads
- status: Planned
- aliases: [WM-B, WM-1, WM-2, WM-4, WM-5, WM-6, WM-7, WM-8, WM-16]
- lanes: [specification, code, documentation]
- depends-on: [0153.S1, 0145.S2, 0118.S4]

`lenny-materialize` for `inlineFile`, `mkdir`, and `uploadFile`; per-source staging, the content socket and done pipes, `spoolSizeLimitBytes` with the shared staging space, its wait, its admission floor, and the gateway's refusal at upload, reflink clones of staged files, the plan memfd, and result validation; staging in `/lenny/bin`; pre-start `materialize` with the working-directory check through the broker.

The step needs SI-B (the broker, [0145.S2](../0145-session-isolation/steps.md#steps)) and PL-B ([0118.S4](../0118-agent-pod-layout/steps.md#steps)).

### 0153.S3 Archive extraction in the helper
- status: Planned
- aliases: [WM-C, WM-9]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2]

Archive extraction in the helper and its removal from the gateway; the §7.4 and §13.4 rewrite. The gateway's extraction stays until 0153.S3 lands, so nothing breaks between steps.

### 0153.S4 Git in the helper
- status: Planned
- aliases: [WM-D, WM-10]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0127.S3, 0147.S8, 0131.S1]

Git in the helper: the pinned git, `gitClone`, the tree-size check, the index refresh, `capture-copy`, and `restore-complete`.

The step needs EG proposal 3 ([0127.S3](../0127-egress-and-git/steps.md#steps)), which is also the egress step that carries the fetch grant, DR-11 ([0147.S8](../0147-tenant-derived-runtimes/steps.md#steps)), and the IC change for the capture copy, which is IC-1's "Git repositories" ([0131.S1](../0131-incremental-checkpoint/steps.md#steps)). 0153.S3 and 0153.S4 can land in either order after 0153.S2.

### 0153.S5 Mid-session overlay
- status: Planned
- aliases: [WM-E, WM-12]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0131.S1, 0135.S5]

Mid-session `overlay` for uploads and child-file imports, `discard`, and the checkpoint's skip of the reserved names.

The step needs IC-1 ([0131.S1](../0131-incremental-checkpoint/steps.md#steps)) and the MD-26 proposal ([0135.S5](../0135-message-delivery/steps.md#steps)).

### 0153.S6 Resume, derive, and snapshotless rebuild
- status: Planned
- aliases: [WM-F, WM-13]
- lanes: [specification, code, documentation]
- depends-on: [0153.S2, 0153.S4, 0131.S5, 0144.S1]

Resume, derive, and the snapshotless rebuild through the helper.

The step needs IC-5 ([0131.S5](../0131-incremental-checkpoint/steps.md#steps)) and SD-1 ([0144.S1](../0144-session-derive/steps.md#steps)).

### 0153.S7 Slot memory floor and writeback pacing
- status: Planned
- aliases: [WM-G, WM-14]
- lanes: [specification, code, documentation]
- depends-on: [0121.S1]

`runtimes.minSlotMemoryBytes` and writeback pacing.

The step waits on the validation of "Validation owed", and on billing decision 7's mapping, which BR-1 ([0121.S1](../0121-billing-resource-attribution/steps.md#steps)) carries.

### 0153.S8 Warm-up checkout
- status: Planned
- aliases: [WM-H, WM-15]
- lanes: [specification, code, documentation]
- depends-on: [0153.S4, 0153.S9, 0118.S2, 0147.S8, 0140.S1]

The warm-up checkout: `prestage` of the `gitRepository` entries with `prestage: true`, adoption at bind by the first source of each repository, the background `discard`, and the reflink rebuild.

The step needs PL-D (no pre-launch, [0118.S2](../0118-agent-pod-layout/steps.md#steps)), DR-11 ([0147.S8](../0147-tenant-derived-runtimes/steps.md#steps)), and the RV proposal that adds `prestage` (D29 of that design, [0140.S1](../0140-runtime-revisions/steps.md#steps)). It lands after pre-launched runtimes are gone, because a pre-launched process could hold a staged tree open.

### 0153.S9 lenny-store-fill
- status: Planned
- aliases: [WM-I]
- lanes: [specification, code, documentation]
- depends-on: [0118.S4, 0127.S5]

`lenny-store-fill`: the init container, the store fill and the pristine trees moved out of the adapter, the adapter's delete-only use of the store volume, and `netagent`'s attribution of the fill's UID.

The step needs PL-B ([0118.S4](../0118-agent-pod-layout/steps.md#steps)) and the egress proposal that carries decision 61, which is EG-5 ([0127.S5](../0127-egress-and-git/steps.md#steps)).
