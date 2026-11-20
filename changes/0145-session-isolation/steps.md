# Steps: 0145 Session isolation

## Validation record

[`0153-workspace-materialization`](../0153-workspace-materialization/change.md) records the materialization spike, and [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md) records the AWS re-validation of the checkpoint through the broker and the full tables of the parallel stat walk. This record holds the results this design relies on.

**Local spike, 2026-11-11.** A kind v0.31.0 cluster with Kubernetes v1.35.0, containerd 2.2.0, runc 1.2.9, and kernel 6.12.76-linuxkit on Docker Desktop for arm64 with cgroup v2. The kind configuration enabled `PodLevelResources`, `UserNamespacesSupport`, `ContainerRestartRules`, `ImageVolume`, `InPlacePodLevelResourcesVerticalScaling`, and `RestartAllContainersOnContainerExits`; the alpha gates also needed `NodeDeclaredFeatures`, without which the kubelet panicked. The test namespace enforced `pod-security.kubernetes.io/enforce=restricted`, and a negative control that added `NET_RAW` was rejected. No pod with this design's full layout was run: the spike tested each mechanism in pods built for that test.

| Experiment | Result |
| :-- | :-- |
| Identity | An adapter at UID 10000 on `@lenny` saw slots at 10001, 10002, and 10003 by `SO_PEERCRED`, each with its own UID and GID. Slot 1 with slot 2's environment values was still seen as 10001. `setpriv --reuid=10002` failed with `EPERM`. Slot 1 had `CapEff` and `CapBnd` 0, `NoNewPrivs` 1, and `Seccomp` 2. The peer process ID was 0. |
| Visibility | Slot 1 saw only its own processes, and `kill -0` over PIDs 1 to 300 reached only its own namespace. A `0600` `/dev/shm` file of slot 2 was unreadable. Writing slot 2's `0666` file was refused by `fs.protected_regular=2`. Slot 1 reached slot 2's `127.0.0.1:8002`, read its `0666` `/dev/shm` file, listed its System V segments, listed its abstract names in `/proc/net/unix`, connected to `@slot2-mux` (seen as UID 10001), and could bind unclaimed abstract names. |
| Ownership, `fsGroup` | With `fsGroup=20000`, slot 1 read slot 2's umask-022 file and appended to its umask-007 file. |
| Ownership, groups | No container-level `supplementalGroups` exists. Pod-level groups `[10001,10002]` reached every container, and slot 1 read slot 2's `0660` object. Under `Merge`, the adapter image's `/etc/group` gave it 10001 to 10003, and an untrusted slot image gave itself 10000 and 10002. `Strict` removed image-declared groups. |
| Ownership, setgid and ACLs | A setgid `2770` directory with umask 007 let the adapter read and write through the group, and `mkstemp` files locked it out. Per-pair volumes with default ACLs for both UIDs let each side read and write the other's files and overrode umask 077. `chmod 600` and `mkstemp` set the mask to `---`, and a slot's `setfacl -b` and `chmod 700` locked the adapter out. |
| Shared repository | A generated repository of 200 commits, a 301 MiB pack, and a 2,001-file working tree of 9.9 MB was copied by an init container into a shared `emptyDir` mounted read-only. "SI-8" gives the timings. Overlay mounts failed with "must be superuser", `unshare -Ur` failed with `EPERM`, and `/dev/fuse` was absent. An image volume was admitted under Pod Security Standards Restricted. |
| CPU and memory | "SI-9" gives the results. |
| Cleanup | Escaped processes (a `setsid` double fork, a `nohup` loop that ignores SIGTERM, a respawner, and 30 children) survived a process-group kill. Writing `cgroup.kill` failed because cgroup2 is mounted read-only. `kill(-1, SIGKILL)` as the slot UID from a Go helper killed them all in one pass and left PID 1, its own child, and slot 2. Busybox `kill -9 -1` killed the calling shell. Restarts under container restart rules took 1.5 s, 14.9 s, and 29.0 s, and files and IPC objects survived them. |
| gVisor | runsc `release-20261109.0` for arm64 with the systrap platform. Identity held, the peer process ID was 0, and spoofing failed. Process isolation, open localhost TCP and abstract sockets, and a private `0600` file matched runc. `fs.protected_regular` was not enforced, and `ipcs` showed no segments even in the creating container. Landlock returned `ENOSYS`. |
| User namespaces | kind's `/kind` directory (`drw-r--r--`) broke its container hook inside a user namespace until it was set to `755`. Through kind's kubelet, in-pod UIDs were 10000 to 10003, `uid_map` was `0 3145662464 65536`, `SO_PEERCRED` reported in-pod UIDs, spoofing failed, Pod Security Standards Restricted admission passed, and `unshare -Ur` returned `EPERM`. |
| Landlock | securityfs was not mounted. The probe returned ABI 6 in a runc pod under Pod Security Standards Restricted and with `hostUsers: false`. A ruleset allowed the workspace, denied `/dev/shm` writes, `/var` reads, and cgroup reads, and refused `connect(@lenny)` with `EPERM`. TCP was not restricted. |

**AWS spike, 2026-11-11.** us-west-2, arm64 instances, EKS AMIs dated 2026-11-07, estimated spend USD 0.04, with every resource deleted and the deletion verified. No kubelet ran on these instances: the checks read the node images and ran runc and Kata directly.

| | EKS AL2023, 1.37 | EKS AL2023, 1.32 | Bottlerocket, aws-k8s-1.37 (1.69.0) |
| :-- | :-- | :-- | :-- |
| Kernel | 6.18.56 | 6.1.189 | 6.18.56 |
| Landlock in the LSM list, and ABI | Yes, ABI 7 | Yes, ABI 2 | Yes, ABI 7 |
| `user.max_user_namespaces` | 7312 | 7034 | 0 |
| Unprivileged user namespace | Works | Works | Fails with `ENOSPC` |
| SELinux | Permissive | Permissive | Enforcing |
| containerd and runc | 2.3.7 and 1.4.3 | 2.3.7 and 1.4.3 | 2.3.7 and 1.3.6 |
| runc idmap support | Yes | Yes | Yes |
| cgroup | v2 | v2 | v2 |
| Idmapped mount on XFS | Works | Works | Works after the limit is raised |
| Idmapped mount on tmpfs | Works | Fails with `EINVAL` | Works after the limit is raised |

- **User namespaces with runc on AL2023 1.37.** Container A created a user namespace mapping 0 to host 200000 and a network namespace and listened as UID 1001. Container B joined both as UID 1002. Each read the other's UID by `SO_PEERCRED`, and A appeared as UID 201001 on the host. Container C, in its own user namespace with A's network namespace, and A each saw the other as UID 65534.
- **Kata on c6g.metal**, with Kata 4.3.1, QEMU, virtiofsd, crictl 1.37, and CNI plugins 1.10.0, run through crictl with no kubelet. The pod sandbox started in 3 seconds with guest kernel 6.18.56 and Landlock ABI 7. Containers at UIDs 1001 and 1002 read each other's UID by `SO_PEERCRED`. The peer process IDs were 6 and 2, which suggests a shared process view; crictl's container configuration defaults to the pod's process namespace where the kubelet requests a per-container one, so the shared view is likely an artifact of the test **[inference]**.

**Spikes of 2026-11-12.** Spikes 1 to 3 ran locally on a kind cluster or under Docker with runc 1.2.9 and gVisor runsc `release-20261109.0` with the systrap platform, kernel 6.12 for arm64 on linuxkit, and an XFS loop file system with `reflink=1`. They used revision 3's layout, under its paths.

### Spike 1: compatibility with the incremental checkpoint design

Every mechanism of the incremental checkpoint design worked through the per-slot layout under runc and gVisor: the identity comparison, the rule for files written during capture, `FICLONE` into the spool, export clones, the change tracker on runc, confined reads, restore with revision 2's mode rule, and cleanup **[measured]**. A change set was captured in 8.7 ms with 1.6 MiB of new disk on XFS through `FICLONE`, against about 60 ms and 101.6 MiB through the copy fallback **[measured]**. Every planted symbolic link was refused, where a naive open would have read the other slot's secret and the adapter's environment **[measured]**. The verdict was "compatible with the changes listed". Its findings:

- Under gVisor, the IC-8 pod-share annotation on a volume a slot mounts through subPath gives the adapter and the slot disjoint views, so the adapter would checkpoint none of the slot's files and report no error. D8 is the fix.
- The kubelet created subPath directories as `root` with mode `0777`, so the slot could rename its session directory. D8 makes this moot.
- Slot roots should be opened with `RESOLVE_BENEATH | RESOLVE_NO_XDEV | RESOLVE_NO_SYMLINKS`, everything below them with `RESOLVE_BENEATH | RESOLVE_NO_XDEV`, and final components with `O_NOFOLLOW`.
- Warm stores must be at the same absolute path wherever git resolves alternates.
- Restore as the adapter with widened modes left the slot with `EPERM` on `chmod` and on `utimensat` with explicit times. D9 retires that rule.
- The Sweep must not depend on changing the mode of a volume root, and must clean `/tmp`; the adapter must check that the tree is empty before reusing a slot, and fail the slot otherwise.
- `mkstemp` files are `0600` and unreadable to the adapter under any non-privileged scheme. D9 addresses this.
- An `EACCES` from `inotify_add_watch` should count as one unreadable path rather than degrade the whole tracker. D9's supervisor-held tracker supersedes this.
- The spool cap must be accounted per file by logical size or allocated blocks, never by `statfs`, because the kubelet counts reflink clones at full size against `emptyDir.sizeLimit`.
- Index identity is per mount view: under gVisor the device differs between containers, which costs a full re-read and causes no error.
- After `git clone --shared` from a warm store, the index must be refreshed more than 1 s after checkout, or 2,342 duplicate loose objects, about 28 MB, are written into the slot.

### Spike 2: the layout without subPath

The setup was kind v1.35.0 with the worker's `/var/lib/kubelet/pods` on an 8 GiB XFS loop file system with `reflink=1`, a namespace enforcing Pod Security Standards Restricted, no `fsGroup`, and `supplementalGroupsPolicy: Strict`. Slots mounted their whole volume, the adapter mounted both slot volumes and an adapter-only spool volume, and the warm stores were read-only at one path everywhere, filled by an init container. The pods were `nr` (runc), `ng` (gVisor with no annotation), `nh` (gVisor with the IC-8 pod-share annotations on both slot volumes and the spool), and `oh` (the subPath layout with pod-share, as a negative control).

The verdict was that the layout loses no file data on runc or on gVisor, with or without the pod-share annotation: every byte of every file the adapter could read came back, and the adapter saw every slot file operation at once **[measured]**. The losses that remained came from the adapter's file access under revision 2: unreadable `0600` files and `0700` directory contents (including a `0644` file replaced by an atomic save through Python's `tempfile`, which leaves it `0600`), hard-link identity (a link count of 2 became 1), the owner UID, and the widened modes **[measured]**. D9 addresses each.

| Check | runc | gVisor | gVisor pod-share |
| :-- | :-- | :-- | :-- |
| `FICLONE` from a slot volume to the spool volume (two bind mounts of one XFS) | Works in 36 to 98 µs for 200 MiB | `EOPNOTSUPP` | `EOPNOTSUPP` |
| `FICLONERANGE` | Works in 37 µs | `EOPNOTSUPP` | `EOPNOTSUPP` |
| `copy_file_range` | Works in 34 µs with extents shared | A real copy, 41 ms per 200 MiB | A real copy, 48 ms per 200 MiB |
| Read and write fallback | 127 ms, 200 MiB of new disk | 105 ms | 111 ms |
| Sharing after the clone | `FIEMAP` shows 209,715,200 of 209,715,200 bytes shared, with no change in `df` | `FIEMAP` returns `ENOTTY`, and `df` grows 200 MiB per copy | `ENOTTY`, and the volume's `statfs` grows 200 MiB per copy |
| Copy-on-write after the clone | An 8 MiB overwrite of the original leaves the copy's hash unchanged, with 8 MiB of new disk | The copy is unchanged | The copy is unchanged |
| Create, append, rename-over, unlink, `mkdir`, `rmdir`, hard link, and symbolic link | All visible to the adapter | All visible | All visible |
| Round trip of a 22-entry tree | 17 captured, with content and modification time identical | Same | Same |
| Start-up canary written by the supervisor | Seen by the adapter | Seen | Seen; not seen in the subPath layout `oh` |
| Change tracker over 200 files with renames and the other operations | 207 dirty paths | 0 events | 207 |
| A write through a hard link in another directory | Only the written name reported | Not applicable | Only the written name reported |
| A spool limited to 300Mi with three `FICLONE` copies of 200 MiB | The kubelet counted 600 MiB and evicted the pod ("Usage of EmptyDir volume "spool" exceeds the limit "300Mi""), with almost no new disk | Not run | Not run |
| Volume root | `root:root` with mode `0777` | `0777` | `1777` (sticky) |
| A slot renames or removes its own volume root | `EACCES`, and `EBUSY` on the mount point | Same | Same |
| A slot renames or deletes an adapter-owned top-level entry | Allowed | Allowed | `EPERM` |
| The adapter deletes a slot-owned top-level entry | Allowed | Allowed | `EPERM` |
| Release: the slot's removal, then the adapter's | The volume is empty | Empty | The slot got `EPERM` on the adapter's entries, the adapter's removal took them, and the volume is empty |
| A session directory replaced by a relative symbolic link | `RESOLVE_NO_SYMLINKS` refused it with `ELOOP`; `RESOLVE_BENEATH` alone followed it into the slot's own files | Same | Same |
| A session directory replaced by an absolute symbolic link to the other slot's volume | `EXDEV` | `EXDEV` | `EXDEV` |
| Warm stores read-only at the same path | `EROFS` on both sides; `git fsck` on the slot's `--shared` clone returns 0 | Same | Same |

All rows are **[measured]**. Under gVisor without the annotation the device differed between views (52 and 111). With pod-share both containers shared one sentry mount (device 51), and `statfs` was per volume. On runc `statfs` reports the whole node file system **[measured]**.

### Spike 3: the file-descriptor broker

The setup was kind v1.35.0, containerd 2.2.0, runc 1.2.9, runsc `release-20261109.0` with systrap, kernel 6.12.76-linuxkit for arm64, XFS loop volumes with `reflink=1` for runc, and `emptyDir` volumes with no reflink for gVisor. The broker was prototyped in a copy of `supervisor/` and built freestanding with `-Werror`, adding about 470 lines (13% of 3,585) and growing `.text` from 17,864 to 21,264 bytes **[measured]**. The candidates compared:

| | Supervisor broker (C) | Per-slot broker container (Go) | Capabilities | Copy-out by the supervisor | Rewriting modes |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `0600`, `0400`, `mkstemp`, and atomic-save files; `0700` and `0500` directories | Read on runc and gVisor | Read | Read, and also `0000` and `0300` | Read | Partly |
| `0000` files and `0300` directories | Metadata only | Metadata only | Full | Metadata only | Not run |
| Restore of owner, mode, and nanosecond times | Exact (0 differences in 23 entries) | Exact | Exact only with `CHOWN`, `FOWNER`, and `DAC_OVERRIDE` | Not run | Not run |
| `FICLONE` from a slot file to the adapter's spool | Works on runc (12 of 12; a 64 MiB clone used no spool bytes) | Works | Works | In C | Not run |
| Change tracker on gVisor's default mount | Works (211 dirty paths) | Fails (1) | Fails, as today | Not run | Not run |
| Pod Security Standard | Restricted | Restricted | Restricted rejects it; Baseline only with `hostUsers: false`, and `DAC_READ_SEARCH` needs Privileged | Restricted | Not run |
| gVisor | Yes | Yes | No (it rejects `hostUsers: false`) | Yes | Not run |
| Added C | About 470 lines | None | None | Over 600 estimated, plus the index logic | Not run |
| Per-slot cost | None | About 10 MB resident on runc; about 25 MB in the sandbox and 38 MB on the node on gVisor | None | None | Not run |
| One code path | Yes | Yes | Yes | No | No |

The supervisor broker, measured on runc, gVisor's default mount, and gVisor's pod-share mount:

- **Reads.** `getdents` on passed descriptors of `0700` and `0500` directories worked, and so did reads of `0600`, `0400`, and `mkstemp` files. The adapter's own mount returned `EACCES` for each.
- **Identity.** `fstat` on a passed descriptor matched `lstat` on device, inode, size, mode, and change and modification times to the nanosecond, and the recheck of a file written during capture worked. The descriptor's mount ID was the slot's mount (374), where the adapter's own mount has 322, and under gVisor's default mount the device differed as well (`0:96` against `0:55`).
- **Change tracker.** With the supervisor's `inotify` descriptor and broker-added watches, runc gave 212 dirty paths with the barrier seen in 0.3 to 0.7 ms, and gVisor's default mount gave 211. The Go broker container got 1 there.
- **Restore.** Owner, mode (`0000`, `0400`, `0500`, and `0300` included), nanosecond modification time, hard links, sparse extents, FIFOs, and symbolic links came back exactly, and the slot could change the mode and times of the results. Today's restore path got 22 of 23 entries wrong.
- **Extended attributes.** `fgetxattr` and `fsetxattr` on passed descriptors of `0600` files returned `EACCES`.

Performance over 10,000 files of 4 KiB, in µs per file, broker against direct:

| | runc | gVisor default | gVisor pod-share |
| :-- | :-- | :-- | :-- |
| Walk, open, and read, batch of 253 | 4.9 against 3.8 | 25.4 against 23.2 | 16.1 against 15.0 |
| Same, batch of 1 | 39.0 | 80.1 | 73.2 |
| Stat-only walk through `open` | 2.1 against 0.5 | 15.5 against 5.1 | 10.3 against 2.2 |
| Restore | 9.2 against 5.2 | 43.7 against 23.4 | 24.6 against 11.4 |

The capability option's measurements are under "Alternatives rejected". Kata was reasoned and not run in this spike, and the AWS runs below measured it.

All results in this subsection are **[measured]** unless tagged otherwise.

### Asynchronous supervisor worker, 2026-11-12

The prototype is the supervisor that merges the broker and the materialization changes, with the asynchronous worker added. Its net change over that merged base is 376 lines of code, not counting blank and comment lines, and it has 4,915 lines in all **[measured]**. An independent reviewer read it three times. The final review found nothing at Medium or above, and its Low items are fixed. The spike ran on kind v1.35.0 with runc and with gVisor `release-20261109.0` on systrap, kernel 6.12 for arm64. Each result below held on both runtimes unless the row names one, and all are **[measured]**.

| Check | Result |
| :-- | :-- |
| Broker during a run | A walk of 100,100 entries in 502 requests took about 200 ms while a setup ran, against 216 ms idle (runc). |
| Throughput | 1,000 setups with no failure: 0.2 ms at the median and 0.4 ms at the 99th percentile on runc, and 0.7 and 2.5 ms on gVisor. |
| Busy | A second `setup` and a `cleanup` during a run got `worker_busy` in 0.4 ms, and the refused setup's descriptors were closed at once. `sweep` and `launch` during a run got `worker_busy`. |
| Cancel | `setup_cancelled` 1.1 ms after the adapter closed the cancel pipe. A byte written to the pipe cancelled nothing. |
| Deadline | `setup_timeout` at 2,012 ms for a 2,000 ms deadline, with no process of the program's group left. |
| Process groups | A setup command's background job died when the command exited. Processes that called `setsid` survived until End, which removed them. |
| End during a run | The run's `setup_cancelled` reply preceded `ended`. |
| Ordering | 200,000 `fs` replies came back in order with one `setupdone` among them, up to 64 in flight, with no wrong descriptor count. |
| Launch permission | A fresh slot ran a setup and then a launch. After a `cleanup`, `launch` got `launch_not_permitted` until an `end`. The release order `end`, `sweep`, `cleanup`, `end`, `launch` succeeded. |
| Malformed requests | A relative `argv[0]` with a slash, an `nfds` that differed from the descriptors attached, and an unknown member got `malformed_request`, with their descriptors closed. |
| Working directory | `..` and `/etc` failed with `reason` `cwd` and errno 18 (`EXDEV`), a link with errno 40 (`ELOOP`), and a missing directory with errno 2. |
| Modes | `mkdir` with `4755` or `2755` got errno 22 (`EINVAL`), and `1777` succeeded. |
| `RLIMIT_FSIZE` | A program that wrote past `fsizeBytes` ended by signal 25 with `setup_failed`. |
| Privileges | The setup program ran with `no_new_privs` and a core limit of 0. An exec-only program stayed non-dumpable, so `pidfd_getfd` from a same-UID process returned `EPERM` and reading its descriptors `EACCES`. A readable program was dumpable. |
| Send stall | With the adapter's reader stopped, the supervisor closed the connection 10.04 s later. `SO_SNDTIMEO` of 1 s gave up after 1.02 s on runc and did not fire within 5 s on gVisor. |
| Stuck cleanup | A cleanup that ignored its 1 s deadline ended with `cleanup_failed` at 6.0 s, and a ping during it answered in 0.3 ms. |
| Tree | A setup program wrote a tree of 100,000 files in 572 to 607 ms. |
| Descriptors in flight | On runc, a pod on the same node at the slot UID that held 1,048,685 descriptors in flight against an `RLIMIT_NOFILE` of 1,048,576 made the victim's reply carry errno 109 (`ETOOMANYREFS`) with no descriptor for about 20 s, and the victim recovered when the attacker released them. Inside the slot, the same attack left pings answered in 0.1 ms, and a `mkfile` retried after the refusal succeeded with the refused entry gone. On gVisor the attacker held 53,886,976 descriptors in flight with no limit, and the victim was unaffected. |
| PID 1 signals | On gVisor, SIGSTOP from a slot process held the supervisor: a ping answered after 2.7 s, when the test resumed it. SIGKILL killed it, and the connection closed. On runc a ping answered in 0.1 ms after the same SIGSTOP. |

### AWS run: Kata `block-plain` with `fsGroup` and the parallel stat walk, 2026-11-12

us-west-2, two `m8i.2xlarge` x86_64 nodes with nested virtualization, Amazon Linux 2023 with kernel 6.12.115, a 200 GB gp3 data volume of XFS with `reflink=1` at 3,000 IOPS, containerd 2.4.2, runc 1.5.3, gVisor `release-20261109.0`, Kata 4.3.1 with QEMU, and single-node k3s v1.37.2. Spend USD 1.77, with every resource deleted and the deletion verified by ID. The pods used revision 3's layout with the dedicated volume GID 29000 on Kata `block-plain`. All results are **[measured]** unless tagged.

- **Volume roots.** Under Kata `block-plain` with `fsGroup`, disk roots came up `root:29000 2775`, `lost+found` `2770`, and memory-backed roots `3777`. Every process in every container carried GID 29000. Without `fsGroup` an earlier run saw the disk roots as `root:root 0755`.
- **Separation.** Each slot mounted only its own volumes, and the walk from each slot found no entry of the other slot outside the shared `/dev/shm`. Through GID 29000 a slot reached another slot's object only after the owner assigned it to the GID with `chown` or `IPC_SET`. Each slot read the `0640` and `0660` files of the pod-wide read-only volumes. The adapter read and wrote group-accessible slot files through its own mount.
- **Broker.** The adapter reached every file of both slots through the broker with 0 missed changes and 0 identity differences against the slot's view, over 600 entries per slot that included modes `0000`, `0300`, `0400`, and `0500`, a FIFO, links, and a sparse file.
- **Release.** With two Sweep patches, coverage of the slot's workspace volume and `0700` on owned directories before descent, every ordinary and hostile release left every volume empty, and the next session started clean. A hostile slot removed `lost+found`, replaced it with its own directory holding a file, wrote a `0500` directory into the real `lost+found`, and left `0000` directories; the Sweep removed every slot-owned entry. Without the patches, the release failed with `sweep_failed` on a `0500` directory under runc. An LLVM session of 208,088 entries failed with `sweep_bound` and left 8,096 entries after 23.9 s.
- **Restore.** Broker restores at the gp3 baseline with one VM on the node took 76.4 s for LLVM, against 79.4 s through the adapter's own mount, and 77.5 s for Chromium, with 0 bad chunks. The only fidelity loss was the setgid bit on 4 directories. The host kept at least 19 GiB available.
- **Parallel stat walk.** At 16 threads it cost 1.02 to 1.24 times the direct walk on Chromium and LLVM, with 0 missed changes and 0 identity differences on every runtime. Chromium paused 1.29 s on runc, 1.51 s on Kata `block-plain`, 2.55 s on gVisor pod-share, 6.33 s on gVisor's default mount, and 9.44 s on Kata `virtio-fs`. Its fallback fired only on `EACCES`, for `0700` and `0750` directories, and on Kata `block-plain` only for `0700`, because the volume GID made `0750` searchable. Fallback batches from 16 threads produced worst requests of 146 to 370 ms on Kata `virtio-fs`.
- **Tracker queues.** The `inotify` queue overflowed at 16,384 events under runc and both Kata handlers, and never overflowed under gVisor, which drained 600,000 events after a 20 s stall.


### AWS spike: slot attribution and project quotas, 2026-11-14

us-west-2, one `m8i.2xlarge` x86_64 node with nested virtualization, Amazon Linux 2023 with kernel 6.12.115, a 60 GB gp3 data volume of XFS with `reflink=1` mounted `prjquota` under `/var/lib/kubelet`, containerd 2.4.2, runc 1.5.3, gVisor `release-20261109.0` with a second handler under host networking, Kata 4.3.1 with QEMU and guest kernel 6.18.56 under both volume modes, and single-node k3s v1.37.2. Spend USD 0.27, with every resource deleted and the deletion verified by ID. The report, the harness, the node scripts, and the raw results are in [`evidence/agent-pod-layout-design/attribution-and-quota-spike/`](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md). All results are **[measured]** unless tagged.

- **Attribution readers.** A listener in a `netagent` container resolved each loopback connection's owner through `sock_diag` and through `/proc/net/tcp` and `/proc/net/tcp6`. runc, with and without user namespaces, answered both. gVisor refused `sock_diag` with `EPROTONOSUPPORT` and answered both files, except under host networking, where `/proc/net/tcp6` does not exist. Kata refused every `sock_diag` request with `ENOENT`, because its guest kernel is built without `CONFIG_INET_DIAG`, and answered both files.
- **UIDs.** Every reader that answered gave each connection its own slot's UID, adapter 10000 and slots 20000 to 20002, with no mismatch over 1,002 concurrent IPv4 connections and 1,000 IPv6 connections. Under user namespaces both readers reported the UIDs inside the pod.
- **Latency.** With 1,002 connections open, one targeted `sock_diag` lookup took 7 µs at the median, and one read of `/proc/net/tcp` 2.1 to 6.5 ms. Under a burst of 1,002 accepts with one read each, `/proc/net/tcp` put the median accept 84 to 204 ms late on runc, 27 to 78 ms on gVisor, and 2 to 3 ms on Kata, with 99th percentiles up to about 0.6 s.
- **Spoofing.** A socket kept its creator's UID when one slot passed it to another over an abstract Unix socket, before or after `connect`, and a forked child kept the slot's UID. `unshare -U` failed with `EPERM` under runc's RuntimeDefault seccomp profile, and succeeded under gVisor and Kata, where both readers still reported the slot's UID.
- **Closed connections.** A client that closed with FIN before the lookup left a socket in `FIN_WAIT2` that both readers on runc and `/proc/net/tcp` on Kata reported with UID 0. After an RST the socket was not found.
- **Project quotas.** A node agent set a project ID with the inherit flag and a hard limit on each `emptyDir` directory, and `statfs` from the unprivileged slot then reported the limit and the usage on runc with and without user namespaces, gVisor's default mount, Kata virtio-fs, and ext4, bytes under a mode-`000` directory included. A write at the limit failed at `write()` with `ENOSPC` on XFS and `EDQUOT` on ext4, the files kept their bytes, and the pod was not evicted. A container with only `CAP_SYS_ADMIN` under RuntimeDefault read and set project limits.
- **Escape.** Without user namespaces, a runc slot changed its own directory's project ID and cleared its inherit flag with `FS_IOC_FSSETXATTR`, and its later bytes did not count. Under user namespaces the calls failed with `EINVAL`, and under gVisor and Kata virtio-fs with `ENOTTY`.
- **The kubelet's projects.** With `LocalStorageCapacityIsolationFSQuotaMonitoring` on, the kubelet assigned project IDs from 1048577 upward to user-namespace pods only, with an effectively unlimited limit. A Lenny limit on the kubelet's project held for the 30 s watched, and moving a volume to another project made the kubelet report 0 bytes for it.
- **gVisor pod-share and Kata block-plain.** Under the pod-share annotation the volume is one `.gvisor.filestore.*` file, and the sentry's `statfs` reported an 8 EiB size and the usage, bytes under a mode-`000` directory included. Under Kata block-plain each volume's `disk.img` is sized to the node filesystem, the guest's `statfs` tracked the volume's usage exactly and reported the node filesystem's size, the kubelet's count of the image ran about 53 MiB above the guest's figure, and a quota on `disk.img` stopped the VM at the limit.

## Validation owed

- A pod with PL's full layout, the broker, the asynchronous worker, and a real IC adapter, end to end under runc, gVisor, and Kata.
- User namespaces through a kubelet on EKS, GKE, and AKS with every volume type the pod mounts; the cross-pod in-flight attack repeated under user namespaces; and the broker, idmapped `emptyDir` volumes, and the per-slot UIDs under them. The `uid_map` check on a cluster that drops `hostUsers`.
- Kata with user namespaces, whose support is unverified.
- Kata under Cloud Hypervisor and under Dragonball, the default VMM of Kata 4.0's runtime-rs, with the checks this design's Kata runs made under QEMU: `/proc/net/tcp` slot attribution, project quotas on virtio-fs, `statfs` on `block-plain`, `SO_PEERCRED` across the slot boundary, and the release's emptiness check (D35).
- AWS Nitro Sandboxes on EKS (Kubernetes 1.36 or later) and Edera as backends of the `microvm` profile, with the same checks, startup time, and per-pod memory overhead, before the documentation names either as supported (D35).
- A release at the scaled Sweep bound with LLVM- and Chromium-sized sessions on each runtime, within the Sweep's time backstop.
- The fallback cap of 2 outstanding requests on Kata `virtio-fs` with a `0700` flat directory of 1,000,000 entries, against the worst request of other broker users.
- The disk watch as PL's O8 settles it, which PL's "Validation owed" lists, and the `tmp` root's refusal of every operation other than `open` of a directory and `statat`.
- The bytes the disk watch's walk misses in a directory whose mode denies its owner read, against the kubelet's count, on runc without user namespaces and on nodes without project quotas. `statfs` on gVisor's pod-share mount, on Kata `block-plain`, and on a project-bound volume counts them **[measured]**. What project quotas still owe, on managed node images and with reflink clones, is in PL's "Validation owed" (D32).
- The second Sweep's cost on a released slot, and a cleanup command's file in `/tmp` and `/dev/shm` gone after it.
- `lenny-store-fill` under user namespaces and on Kata `block-plain`: its UID inside the pod's range, its access to the store volume's root, and the adapter's deletion of a withheld store it filled. The hostile-repository stress test suite of D28, run as tests on runc, gVisor, and Kata, which PL's "Validation owed" lists.
- `fsGroup` together with `supplementalGroupsPolicy: Strict` and `hostUsers: false`; two slots under `fsGroup` checkpointing or restoring at once; and a Kata VM's memory with two slots of LLVM or Chromium size.
- The asynchronous worker on Kata, on x86_64, and the earliest gVisor release that serves the protocol.
- Managed node file systems: the reflink, `FICLONE` across mounts, and the broker's behaviour on the file systems that back `/var/lib/kubelet` on EKS, GKE, and AKS.
- Real harnesses: their resident memory per slot and the file patterns they create.
- Bottlerocket with SELinux enforcing, for slot containers and the broker.
- Landlock's abstract-socket scoping on managed images, beyond the ABI number.
- O4 (a) and O5 (a) together, and the adapter's `cpu.max` detection.
- The kubelet's `singleProcessOOMKill` and its effect on a slot container's supervisor.
- `netagent`'s slot attribution in the production component (O9, D29): the readers that the AWS spike of 2026-11-14 measured with a test listener ("Validation record"), now with the state and UID rule, the batched read of `/proc/net/tcp`, and the start-up check under a burst on gVisor and Kata; the managed runtimes of EKS, GKE Sandbox, and AKS; and the per-slot scopes of egress decision 60 and the platform marker of D30, which the egress design carries.

## Steps

### 0145.S1 Slot containers
- status: Planned
- aliases: [SI-A, SI-1, SI-2, SI-4, SI-7]
- lanes: [specification, code, documentation]
- depends-on: [0118.S1, 0118.S3, 0119.S1, 0119.S2]
- closes: [T-4.7.5]

Slot containers, per-slot names for runtime and tool connections, two-way checks, the listener drain, the adapter's per-slot not-live state ("SI-4"), the recorded slot count, per-slot credentials and manifests, the per-slot canary check of the start-up order, and the webhook rules. [0118.S4](../0118-agent-pod-layout/steps.md#steps) renders the per-slot volumes that the slot containers mount and the canary order, and how this step is ordered against it is open decision O17 in `decisions.md`. Which step builds the gateway's `CH-ADAPTEREVENTS` consumer (issue CHPODGW-I3) and the gateway half of dead slots is O15, who adds `spec.slotCount` is O19, and whether this step is split is O23. It depends on [0119.S1](../0119-agent-runtimes-only/steps.md#steps) and [0119.S2](../0119-agent-runtimes-only/steps.md#steps), because every pod it renders slot containers for is an agent pod only once `type: mcp` runtimes and service mode are removed.

The step lands the list of what a shared pod's slots still share in the `acknowledgeSharedPod` refusal and in `sessionIsolationLevel.residualStateWarning`, as [PL-9](../0118-agent-pod-layout/change.md#pl-9-pool-settings-and-renames) states it, because that list is true only once slot containers exist. It needs [0118.S3](../0118-agent-pod-layout/steps.md#steps) for the renamed fields.

It closes T-4.7.5 with the `security` test of the per-slot credential file of "SI-7", which replaces the delivery contract that test gap describes.

The gateway packages it changes are `pkg/gateway/core/session/slotcounter` and `pkg/gateway/core/session/podsession/slotbinder.go` for the reservation against the recorded count, and `pkg/gateway/core/session/slothealth` and `pkg/gateway/platformtools/leasecontrol/scrubreport_server.go` for the whole-pod trigger over the recorded count.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes: `GWSEAM-InspectForRecycle`, whose `PodRecyclePolicy` serves the pod's recorded slot count in place of the pool's concurrency for the whole-pod trigger (SIR-16). [0118.S3](../0118-agent-pod-layout/steps.md#steps) renames the same field first.

It writes one manifest per slot with its own `connectionNonce` in place of `mcpNonce` ("SI-7") under manifest version 1, with the matching change to the runtime SDKs and `pkg/runtimekit`, as [RP-6](../0139-runtime-protocol/change.md#rp-6-what-the-adapter-enforces), **The version 2 field set and where each change lands.**, orders it; [0139.S1](../0139-runtime-protocol/steps.md#steps) raises the version to 2 afterwards.

RP's tool connections follow this step. They land in RP-B ([0139.S2](../0139-runtime-protocol/steps.md#steps)).

### 0145.S2 Supervisor file-descriptor broker
- status: Planned
- aliases: [SI-B, SI-5, SI-6]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0118.S4]

The supervisor broker with `statat`, the parallel stat walk's broker rules, and the adapter's checkpoint, restore, export, and tracker through it, with the `metadata_only` record. The `--fs-root` arguments name the paths of PL-3, which [0118.S4](../0118-agent-pod-layout/steps.md#steps) lands. IC's matching changes are [0131.S1](../0131-incremental-checkpoint/steps.md#steps) and [0131.S5](../0131-incremental-checkpoint/steps.md#steps), which capture and restore through this step's broker and depend on it. From which run in a slot the adapter treats broker replies as untrusted (SIR-22) is open decision O24. Whether this step closes F-8.7.17, the race in `ExportPaths` that the broker's descriptors remove, is O27.

### 0145.S3 Asynchronous worker and the CH-SUPERVISE protocol
- status: Planned
- aliases: [SI-C, SI-3]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0118.S4]
- closes: [F-7.5.17]

The asynchronous worker and the whole `CH-SUPERVISE` protocol, the release with the Sweep fixes and the emptiness check, the launch at bind, and the retirement of `keep`. The Sweep covers the writable slot volumes of PL-3, which [0118.S4](../0118-agent-pod-layout/steps.md#steps) lands. This step owns the launch at bind, and [0118.S2](../0118-agent-pod-layout/steps.md#steps) points to it. Which step owns the worker and its refusal rule against [0153.S1](../0153-workspace-materialization/steps.md#steps) is O20, how the release maps onto `Shutdown` is O16, and whether the release ends with an End after `prestage` is O18. Whether [0135.S1](../0135-message-delivery/steps.md#steps) depends on 0145.S1 and this step is O26.

[0131.S1](../0131-incremental-checkpoint/steps.md#steps) and [0131.S5](../0131-incremental-checkpoint/steps.md#steps) depend on this step, because the capture copy and the restore run through its worker. WM's helper runs land after this step: the helper runs of the bind (step 2 of "SI-3") with [0153.S2](../0153-workspace-materialization/steps.md#steps), and the `prestage` run of the release (step 9) with [0153.S8](../0153-workspace-materialization/steps.md#steps), each of which depends on this step through 0153.S1.

It closes F-7.5.17: setup commands run in the slot container as the slot UID through `setup` (D6, SIR-14), so no setup command runs in the adapter's container or reaches the projected service account token or another slot's credential file.

### 0145.S4 Resource mapping and detection
- status: Planned
- aliases: [SI-D, SI-9]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0121.S1]

The detection of "SI-9", the pod-level CPU limit it renders where pod-level resources are enforced, and the fallback where they are not. The container mapping onto the slot containers is the billing design's, which BR-1 ([0121.S1](../0121-billing-resource-attribution/steps.md#steps)) lands in the fallback form, with each slot's CPU limit equal to its request and no pod-level `spec.resources`. This step raises `k8s.io/api` from v0.31.4, which the tree pins and which has no pod-level `spec.resources` field, to a version that carries it, and renders the pod-level CPU limit together with its detection. The `go.mod` change is reviewed under the merge-gate review rule of `.claude/rules/git-workflow.md`. The chart keys of the platform containers' requests and the memory limits of the mapping are O25. How the detection result reaches the controller is part of O15.

### 0145.S5 User namespaces on runc pools
- status: Planned
- aliases: [SI-E, SI-11]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0136.S3]

User namespaces on runc pools by default and the adapter's `uid_map` check, which this step owns and [0136-placement](../0136-placement/change.md) points to. It needs PM's rendering of `hostUsers`, which PM-C lands. The Kata `block-plain` volume GID and its `fsGroup` exception are rendered by [0118.S4](../0118-agent-pod-layout/steps.md#steps).

### 0145.S6 Recycling after the release
- status: Planned
- aliases: [SI-F]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0145.S3, 0136.S1, 0118.S2, 0139.S1 (part)]
- closes: [F-5.2.60, F-5.2.55 (part), F-5.2.45 (part), T-6.2.3 (part)]

Recycling after the release: the one-slot disposition, the **Runtime refused** retirement in place of "Runtime not live", dead slots in place of `leaked` slots with `agent_pod_state.dead_slots` and their rehydration, and the scrub profiles `standard` and `vm-restart` (D36 to D39). O15 decides whether the dead-slot part stays in this step or moves to 0145.S1 with these closes entries, O14 decides the boundary at which a pod with a dead slot scrubs and retires, and O21 decides whether transient failures still count toward the trigger.

It removes `in-place` after PM-A's removal of `allowCrossTenantReuse` ([0136.S1](../0136-placement/steps.md#steps)), because `in-place` existed for pools with `allowCrossTenantReuse`, and the two edits to the scrub profile paragraph land in that order. The **Runtime refused** retirement and its test wait for RP-1's handshake refusal, which [0139.S1](../0139-runtime-protocol/steps.md#steps) lands; the rest of the step can land first. The step builds on `GWSEAM-InspectForRecycle` and `GWSEAM-RecyclePod` as [0118.S2](../0118-agent-pod-layout/steps.md#steps) leaves them, with `PodRecyclePolicy.PreConnect` and the `preConnect` argument removed.

It closes F-5.2.60, because a dead slot retires a pod with one slot under D36 and no `leaked` state remains; the `leaked:` member residue of F-5.2.55, because rehydration restores the recorded dead slots (SIR-42); the persistent half of F-5.2.45, because dead slots live in the slot set every replica reads; and the part of T-6.2.3 that the `e2e_kind` test of a pod with two slots and a dead slot covers.

The gateway packages it changes are `pkg/gateway/core/session/recycle/scrubreporter_seams.go` for the leak drain and the recycle disposition, `pkg/gateway/platformtools/leasecontrol/scrubreport_server.go` for the scrub reports, `pkg/gateway/core/session/slotcounter`, `pkg/gateway/core/session/slotreaper`, and `pkg/gateway/core/session/podsession/slotbinder.go` for the `dead:<n>` members and rehydration, `pkg/gateway/core/session/sessionserver/isolationlevel.go` and `pkg/session/sessionrow` for `best-effort-in-place`, and `pkg/sandbox/podscrub`, `pkg/runtime/poolstore`, `pkg/runtime/runtimestore`, `pkg/controller/poolscaling/poolstoresource.go`, and `pkg/gateway/api/admin` for the scrub profiles.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes the pod recycle operations (`GWSEAM-IncrementSessionsServed`, `GWSEAM-IncrementScrubFailureCount`, `GWSEAM-RecycleCounters`, `GWSEAM-RecordLeak`, `GWSEAM-RetireOnSessionCount`, `GWSEAM-InspectForRecycle`, `GWSEAM-RecyclePod`, `GWSEAM-RetirePod`, `GWSEAM-RecordScrubFailure`, and `GWSEAM-RecordPodRetirement`) for the dead slot and the pod-wide scrub (D37 to D39).
