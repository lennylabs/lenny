# Slot attribution and project quotas (AWS, 2026-11-13)

This report covers two tests run on AWS in `us-west-2` on 2026-11-14 between 07:10 and 07:48 UTC (2026-11-13 Pacific time). Test 1 checks whether `netagent` can attribute a loopback connection to a slot UID through `NETLINK_SOCK_DIAG` and `/proc/net/tcp{,6}`. Test 2 checks project quotas on the kubelet's emptyDir filesystem: how they are detected, assigned, read from inside a pod, and enforced. No file under `spec/`, `designs/`, `docs/`, or the code was edited.

Every claim carries the tag [measured] or [inference].

## Summary

- **Attribution readers by runtime.** [measured]

  | Runtime | `sock_diag` (netlink) | `/proc/net/tcp` | `/proc/net/tcp6` |
  | :-- | :-- | :-- | :-- |
  | runc, without and with `hostUsers: false` | answers | answers | answers |
  | gVisor netstack | refused: `socket()` returns `EPROTONOSUPPORT` | answers | answers |
  | gVisor hostinet | refused: `EPROTONOSUPPORT` | answers | file absent (`ENOENT`) |
  | Kata virtio-fs and Kata block-plain | refused: every request returns `ENOENT` | answers | answers |

  The Kata 4.3.1 guest kernel (6.18.56) is built with `# CONFIG_INET_DIAG is not set`. [measured]
- **UIDs reported.** Every reader that answered gave the correct, distinct UID for every connection: adapter 10000, slots 20000, 20001, and 20002. [measured]
  - This held at 1,002 concurrent connections from three slots, with 0 mismatches and 0 errors.
  - Under `hostUsers: false`, both readers report the UIDs as they appear inside the pod. Root on the host sees the mapped UIDs (1939885600 + n).
- **Latency.**
  - With 1,002 connections open, the targeted netlink lookup takes 7 µs at p50 and 25-30 µs at p99. One `/proc/net/tcp` scan takes 2.1-6.5 ms at p50 (about 2,000 rows). [measured]
  - Under a burst of 1,002 accepts with one reader enabled, netlink stays at 4.5-9.3 µs p50. `/proc/net/tcp` reaches 84-204 ms p50 and about 500 ms p99 on runc, and 27-78 ms p50 on gVisor. [measured]
- **Spoofing.** A slot cannot make its connection carry another UID alone. [measured]
  - A socket keeps its creator's UID. A socket passed from slot 0 to slot 1 over an abstract Unix socket is attributed to slot 0, whether it was passed before or after `connect`. This needs slot 0's cooperation.
  - A forked child keeps the slot's UID.
  - A nested user namespace (`unshare -U`) changes nothing. runc's RuntimeDefault seccomp refuses it with `EPERM`. gVisor and Kata allow it, and the readers still report the slot's UID.
- **Closed connections.** A client that closes before the lookup produces a wrong answer. [measured]
  - After a FIN, its socket is reported in state 5 (`FIN_WAIT2`) with **UID 0**, by both readers on runc and by `/proc/net/tcp` on Kata.
  - After an RST, the socket is not found.
  - `netagent` must accept only state 1 (`ESTABLISHED`) and a UID it knows. [inference]
- **Project quotas** (XFS mounted `prjquota` under the kubelet's root, and ext4 with `-O quota,project` mounted `prjquota`) work as a per-slot disk bound on runc, gVisor's default mount, and Kata virtio-fs. [measured]
  - A privileged agent sets the project ID with PROJINHERIT and the hard limit. A container with only `CAP_SYS_ADMIN` under RuntimeDefault seccomp can also set the limit.
  - `statfs` called by the unprivileged slot then reports the project's limit as the size and its usage as used. This holds through gVisor's gofer and through Kata's virtio-fs.
  - Bytes under a mode-000 directory count.
  - At the limit, the writer gets an error at `write()`: `ENOSPC` on XFS and `EDQUOT` on ext4. The pod is not evicted.
- **Project-ID escape.** Without user namespaces, a slot on runc can take its own files out of the project: it sets its directory's project ID or clears PROJINHERIT with `FS_IOC_FSSETXATTR`, and its bytes stop counting. This happened on XFS and on ext4. [measured]
  - With `hostUsers: false`, every such change fails with `EINVAL`.
  - Under gVisor and Kata virtio-fs, the ioctl is not supported (`ENOTTY`).
- **Kubelet feature gate.** With `LocalStorageCapacityIsolationFSQuotaMonitoring` on (k3s 1.37), the kubelet assigns project IDs only to user-namespace pods. [measured]
  - It assigns IDs from 1048577 upward with PROJINHERIT, records them in `/etc/projects` and `/etc/projid`, and writes an effectively unlimited limit (2^53 KiB).
  - Lenny can set a hard limit on the kubelet's own project ID, and the kubelet left that limit alone for at least 30 s.
  - Reassigning the directory to a Lenny project blinds the kubelet: its stats reported 0 bytes for a volume holding 120 MiB.
- **Two runtime modes do not fit.** [measured]
  - gVisor's pod-share annotation turns the volume into a sentry tmpfs backed by one `.gvisor.filestore.*` file. The quota applies only once the agent moves that file into the project, and then writes stop with `ENOSPC` intact.
  - Kata block-plain backs each volume with a sparse `disk.img` sized to the whole node filesystem (64 GB), not to `sizeLimit`. A quota on that image killed the pod when the guest reached it.
  - Inside a block-plain guest, `statfs` tracks the volume's usage exactly, but reports the node filesystem's size.
- **Spend: $0.27 of the $15 budget.** Every resource was deleted, and each deletion was verified by resource ID.

## Setup

- One `m8i.2xlarge` with `NestedVirtualization=enabled` (`i-009dc1f94dc2f2721`), running Amazon Linux 2023 with kernel 6.12.115-140.210.amzn2023.x86_64. [measured]
  - It used shutdown behaviour `terminate` and a scheduled `shutdown -h +330` as a backstop.
  - It ran from 07:10:58 to 07:48 UTC.
- The root volume is a 40 GB gp3. The data volume is a 60 GB gp3 formatted XFS (`reflink=1`) and mounted `noatime,prjquota`. It holds `/var/lib/kubelet`, `/var/lib/containerd`, and `/var/lib/rancher`. [measured]
- Software is the previous run's set (`scratchpad/aws-si-kata-pstat/aws/node-setup.sh`) with no Chromium or LLVM clones. [measured]
  - containerd 2.4.2, runc 1.5.3, gVisor `release-20261109.0` (systrap, directfs), Kata 4.3.1 runtime-rs on QEMU, and k3s v1.37.2+k3s1 on the external containerd.
  - Kata's guest kernel is 6.18.56.
  - Kata runs two handlers: `kata` (virtio-fs) and `kata-block` (`emptydir_mode = "block-plain"`).
  - A new handler, `runsc-host`, runs gVisor with `network = "host"`.
- Every resource was tagged `project=lenny-spike` and `run=attrib-quota-2026-11-13`: the instance, its volumes and ENI, the security group (no ingress), the S3 bucket, the IAM role, and the instance profile. [measured]
- The harness is a static Go binary, `aq`, in an Alpine 3.22 image with `xfsprogs`, `e2fsprogs`, `iproute2`, and `util-linux`. [measured]
  - Source: `scratchpad/aws-attrib-quota/aq/`.
  - Node scripts: `scratchpad/aws-attrib-quota/aws/t1.sh`, `t1b.sh`, `t2.sh`, `t3.sh`, and `t4.sh`.
  - Raw results: `scratchpad/aws-attrib-quota/results/results/`.

## Test 1: per-slot attribution

### Pod and method

The pod has these containers, each running as its own UID and GID: [measured]
- `netagent`, UID 10001, runs the listener.
- `adapter`, UID 10000.
- `slot0`, `slot1`, and `slot2`, UIDs 20000, 20001, and 20002.

Every container sets `runAsNonRoot`, `allowPrivilegeEscalation: false`, drops all capabilities, and uses `RuntimeDefault` seccomp. The runc case ran once with `hostUsers: true` and once with `hostUsers: false`.

The listener in `netagent` listens on `127.0.0.1:9000` and `[::1]:9000`. On each accept it resolves the connecting socket's owner twice:
- **netlink:** one `SOCK_DIAG_BY_FAMILY` request without `NLM_F_DUMP`, keyed on the connection's 4-tuple with `INET_DIAG_NOCOOKIE`. The UID is read from `inet_diag_msg.idiag_uid`.
- **proc:** a read of `/proc/net/tcp` or `/proc/net/tcp6`, matching the client socket's row (local address = the accepted connection's peer).

The client then sends its label and real UID, which the listener compares with each answer.

The phases:
- **basic:** one IPv4 and one IPv6 connection from each slot and from the adapter.
- **many:** 334 connections from each of the three slots at once, 64 in flight per slot, held for 45 s. With all of them held, a re-resolve pass looks up every held connection sequentially through each reader (the "quiet" latency), and dumps all TCP sockets once.
- **many6:** 1,000 IPv6 connections from slot 1, with the same re-resolve pass.
- **pass, fork, and nested user namespace:** described under "Spoofing".
- **close and rst:** the listener delays its lookup by 100 ms while the client closes with FIN, or with RST (`SO_LINGER` 0).
- **burst** (`t1b.sh`): the 3 × 334 burst again with only one reader enabled at accept time, so the two readers' at-accept latencies do not queue behind each other.

### Readers and UIDs

| Runtime | netlink | `/proc/net/tcp` | `/proc/net/tcp6` | UIDs reported in `netagent` | Host view |
| :-- | :-- | :-- | :-- | :-- | :-- |
| runc | answers | answers | answers | 10000, 20000-20002 | the same UIDs (`nsenter` + `ss` as root) |
| runc, `hostUsers: false` | answers | answers | answers | 10000, 20000-20002 (in-pod UIDs) | 1939885600-1939885602; `uid_map` is `0 1939865600 65536` |
| gVisor (netstack) | `EPROTONOSUPPORT` at `socket(AF_NETLINK, …, NETLINK_SOCK_DIAG)` | answers | answers | 10000, 20000-20002 | sockets are not in a host namespace |
| gVisor hostinet | `EPROTONOSUPPORT` | answers | `ENOENT`: the file does not exist | 10000, 20000-20002 (IPv4 only) | not checked |
| Kata virtio-fs | `ENOENT` for targeted requests and dumps | answers | answers | 10000, 20000-20002 | inside the guest |
| Kata block-plain | `ENOENT` | answers | answers | 10000, 20000-20002 | inside the guest |

All rows are [measured].
- The Kata guest kernel config `/opt/kata/share/kata-containers/config-6.18.56-208` contains `# CONFIG_INET_DIAG is not set`, and the same holds in the debug, dragonball, and nvidia variants. The kernel has no inet_diag handler, which is why every request returns `ENOENT`. [measured]
- Under gVisor and Kata, `ss` inside `netagent` still printed the per-slot UIDs: it falls back to `/proc/net/tcp` when netlink fails. [measured]
- On every runtime and reader that answered, all 1,002 IPv4 and all 1,000 IPv6 connections of the "many" phases resolved to their slot's UID, with 0 mismatches and 0 lookup errors. The exception is IPv6 under gVisor hostinet, where no reader exists. [measured]
- Both readers report UIDs in the reader's user namespace. On runc with `hostUsers: false` this is the in-pod UID, which matches the configuration the controller renders. [measured]

### Latency

**Quiet re-resolve** of every held connection, sequential and in µs:

| Runtime | Held | netlink p50 / p99 / max | proc p50 / p99 / max | Rows in `/proc/net/tcp` |
| :-- | --: | :-- | :-- | --: |
| runc, IPv4 | 1,002 | 7.1 / 24.5 / 35 | 2,925 / 3,719 / 148,404 | 2,015 |
| runc, IPv6 | 1,000 | 7.7 / 29.8 / 74 | 3,349 / 4,378 / 5,222 | 2,001 (tcp6) |
| runc `hostUsers: false`, IPv4 | 1,002 | 7.1 / 27.8 / 96 | 2,949 / 3,792 / 4,485 | 2,015 |
| gVisor, IPv4 | 1,002 | not available | 4,470 / 6,694 / 8,086 | about 2,000 |
| gVisor hostinet, IPv4 | 1,002 | not available | 6,529 / 8,846 / 18,047 | about 2,000 |
| Kata virtio-fs, IPv4 | 1,002 | not available | 2,287 / 4,444 / 6,083 | about 2,000 |
| Kata block-plain, IPv4 | 1,002 | not available | 2,123 / 4,248 / 4,864 | about 2,000 |

All values are [measured].
- With a handful of sockets ("basic"), one netlink lookup took 13-19 µs on runc after the first lookup. [measured]
  - The first lookup in a process took 7.7 ms, which includes opening the netlink socket.
  - One `/proc/net/tcp` read took 1.2-1.9 ms on runc, 0.1-0.3 ms on gVisor, and 0.2-0.8 ms on Kata.
- A full netlink dump of all IPv4 TCP sockets (2,015 rows) took 1.3 ms on runc. [measured]
- The cost of `/proc/net/tcp` grows with the number of sockets in the network namespace, because each lookup reads and parses the whole table. [inference, consistent with the basic and many figures above]

**At-accept latency under a burst** of 3 × 334 simultaneous connections, with one reader enabled (`t1b.sh`), in µs:

| Runtime | netlink only, p50 / p99 per slot | proc only, p50 per slot | proc only, p99 per slot |
| :-- | :-- | :-- | :-- |
| runc | 5.1-5.8 / 523-765 | 119,620-162,376 | 495,045-500,750 |
| runc `hostUsers: false` | 4.5-9.3 / 22-77,565 | 84,274-203,680 | 486,068-526,230 |
| gVisor | not available | 38,391-78,169 | 166,514-209,984 |
| gVisor hostinet | not available | 26,841-47,723 | 93,124-179,903 |
| Kata virtio-fs | not available | 2,600-2,654 | 3,769-595,011 |
| Kata block-plain | not available | 2,256-2,860 | 4,073-575,262 |

All values are [measured].
- During the burst, each accepted connection waits for its own scan, and the scans compete for the CPU. On runc, 1,002 scans of about 2,900 rows cost several CPU-seconds, which puts the median accept 0.1-0.2 s behind. [inference]
- Under the burst, netlink kept its median in single-digit microseconds. Its tail (one slot's p99 of 78 ms) reflects goroutine scheduling in the listener. [inference]
- In the first combined run, which did both lookups per accept, the netlink figure was inflated to a p50 of 72-255 ms because it queued behind the scans. The table above replaces those numbers. [measured]

### Spoofing and races

| Probe | runc and runc userns | gVisor and gVisor hostinet | Kata (both) |
| :-- | :-- | :-- | :-- |
| Slot 0 creates an unconnected TCP socket and passes it to slot 1 over an abstract Unix socket. Slot 1 connects and sends its claim. | attributed to 20000 (slot 0) | 20000 | 20000 |
| As above, but slot 0 connects before passing. | 20000 | 20000 | 20000 |
| Forked child (`sh -c 'aq conn & wait'`) | 20000 | 20000 | 20000 |
| `unshare -U -r` (inner UID 0) | refused, `EPERM` | allowed; reported as 20000 | allowed; reported as 20000 |
| `unshare -U --map-user=20001` (inner UID 20001) | refused, `EPERM` | allowed; reported as 20000 | allowed; reported as 20000 |
| Client closes with FIN; lookup after 100 ms | state 5, **UID 0**, from both readers | `/proc/net/tcp`: not found | `/proc/net/tcp`: state 5, **UID 0** |
| Client closes with RST; lookup after 100 ms | netlink `ENOENT`; proc not found | not found | not found |

All rows are [measured].
- The owner UID of a socket is fixed when the socket is created, so a passed socket keeps its creator's UID wherever it is used. [measured]
  - A slot can carry another slot's attribution only if that slot hands it a socket. SI-10's shared abstract socket namespace makes the handoff possible.
  - The receiving slot then acts under the creator's scope. That gives it nothing the creator could not already do by relaying for it. [inference]
- The `EPERM` from `unshare` on runc matches the RuntimeDefault seccomp profile, which refuses `CLONE_NEWUSER` without `CAP_SYS_ADMIN`. [inference about the mechanism; the refusal is measured]
- A slot on gVisor or Kata can create user namespaces. Attribution does not depend on it, but it matters to any other isolation argument that assumes it cannot. [measured]
- The FIN case gives a false answer rather than a missing one: an orphaned socket in `FIN_WAIT2` reports UID 0. [measured]
  - `netagent` should accept an answer only when the state is 1 (`ESTABLISHED`) and the UID is one it was configured with, and refuse every other answer. UID 0, and the overflow UID 65534 under user namespaces, then refuse rather than attribute. [inference]
  - A connection whose client has already closed carries no traffic, so the refusal costs nothing. [inference]

### What this means for `netagent`

- Under the design's reader order, netlink first and `/proc/net/tcp{,6}` second, every measured runtime answers. runc uses netlink. gVisor (both network modes) and both Kata modes use `/proc/net/tcp`. [measured] No measured pool would become a fallback (union-scope) pool. [inference]
- gVisor hostinet has no `/proc/net/tcp6`, so IPv6 loopback connections cannot be attributed there. `netagent` should listen on IPv4 loopback only, or refuse unattributable IPv6 connections. [measured; recommendation is inference]
- On gVisor and Kata, the second reader costs milliseconds per lookup, and a burst of hundreds of connections pushes accepts to tenths of a second. [measured] Two options [inference]:
  - Cache: read the table once per accept batch and resolve all pending accepts from that one read.
  - Kata only: ship a guest kernel with `CONFIG_INET_DIAG=y` and `CONFIG_INET_TCP_DIAG=y`, so the netlink reader works there too.
- The start-up check that records the attribution result in the pool status should test each reader with a connection whose expected UID is known. A runtime that answers "not found" or returns UID 0 for an established connection then fails the check. [inference]

## Test 2: project quotas

### Detection

| Probe | Unprivileged slot (runc) | Unprivileged slot (runc, `hostUsers: false`) | gVisor default mount | gVisor pod-share | Kata virtio-fs | Kata block-plain | Privileged agent or host root |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Superblock options in `/proc/self/mountinfo` | `xfs … prjquota` | `xfs … prjquota` (mount options add `idmapped`) | `9p`, no options | `tmpfs` | `virtiofs`, no options | guest `ext4`, `rw,discard` | `xfs … prjquota` |
| `FS_IOC_FSGETXATTR` (project ID, PROJINHERIT) | answers | answers | `ENOTTY` | `ENOTTY` | `ENOTTY` | answers (guest ext4, projid 0) | answers |
| `quotactl_fd(Q_XGETQSTATV)` (accounting and enforcement flags) | `EPERM` | `EPERM` | `ENOSYS` | `ENOSYS` | `ENOSYS` | `ENOSYS` | answers: `prj_acct` and `prj_enfd` true |
| `quotactl_fd(Q_XGETQUOTA)` for the directory's project | `EPERM` | `EPERM` | `ENOSYS` | `ENOSYS` | `ENOSYS` | `ENOSYS` | answers |
| `statfs` on the volume root after assignment | project limit and usage | project limit and usage | project limit and usage | `tmpfs`, 8 EiB size, sentry usage | project limit and usage | guest device: size of the node filesystem, usage of the volume | project limit and usage |

All cells are [measured].
- `xfs_quota -x -c 'state -p' /data` on the host reports project accounting and enforcement ON. [measured]
- As an unprivileged UID on the host with no seccomp filter (`setpriv --reuid=20000`), `Q_XGETQSTATV` answered and `Q_XGETQUOTA` returned `EPERM`. [measured]
  - Inside the pod, therefore, RuntimeDefault seccomp refuses the state query, and the kernel refuses the per-project query. [inference about the source of the in-pod `EPERM`]
- A container with only `CAP_SYS_ADMIN` under RuntimeDefault seccomp could read and set quotas through `quotactl_fd`. [measured]
- `statfs` reports the project only on a directory that carries PROJINHERIT. On a regular file inside the project it reports the whole filesystem (61,376 MiB). The volume root and every directory created under it inherit the flag. [measured]

### Assignment

- The privileged agent assigned each emptyDir's host directory a project ID with PROJINHERIT through `FS_IOC_FSSETXATTR`, and a 256 MiB hard limit through `quotactl_fd(Q_XSETQLIM)`. [measured]
  - The agent is a DaemonSet-style pod with `privileged: true` and `hostPath` `/var/lib/kubelet` mounted with `HostToContainer` propagation.
  - It used the host path `/var/lib/kubelet/pods/<uid>/volumes/kubernetes.io~empty-dir/<name>`.
  - It needs no block device path and no `/etc/projects` entry.
- `xfs_quota` on the host and `Q_XGETQUOTA` agreed with the bytes written. [measured]

### `statfs` from inside the pod, and the mode-000 directory

After assignment, the unprivileged slot's `statfs("/workspace")` reported a size of 256.0 MiB. Used bytes after each step:

| Runtime | After 40 MiB | After 50 MiB more under a directory then `chmod 000` | Host project usage at that point |
| :-- | :-- | :-- | :-- |
| runc | 40.0 MiB | 90.0 MiB | 90.0 MiB (`du` 90.0 MiB) |
| runc `hostUsers: false` | 40.0 MiB | 90.0 MiB | 90.0 MiB |
| gVisor default mount | 63.9 MiB | 127.9 MiB | 127.9 MiB (`du` 90.0 MiB) |
| Kata virtio-fs | 40.0 MiB | 90.0 MiB | 90.0 MiB |
| ext4 (runc, `hostPath`) | 40.0 MiB | 90.0 MiB | 90.0 MiB |

All values are [measured].
- The bytes under the mode-000 directory count on every runtime, and its inode carries the project ID (`xfs_io lsproj`). [measured]
- `statfs` also reports the project to the adapter container through its own mount of the same volume. [measured]
- Under gVisor's directfs mount, project usage ran ahead of the bytes written while the files were still being extended: 63.9 MiB for 40 MiB. It matched `du` again once the project filled (262,144 KiB). [measured] This looks like XFS speculative preallocation charged to the project. [inference] A watch at 90% of the limit on gVisor would therefore trip early, never late. [inference]

### At the limit

| Runtime | Error to the writer | Where | Bytes in the files | Pod evicted |
| :-- | :-- | :-- | :-- | :-- |
| runc | `ENOSPC` (28) | `write()` | the written bytes, intact | no (40 s) |
| runc `hostUsers: false` | `ENOSPC` | `write()` | intact | no (40 s) |
| gVisor default mount, without and with `fsync` after each chunk | `ENOSPC` | `write()` | intact; last 1 MiB verified | no (30 s) |
| gVisor pod-share, with the filestore moved into the project | `ENOSPC` | `write()` | intact; readable | no (20 s) |
| Kata virtio-fs, without and with `fsync` | `ENOSPC` | `write()` | intact | no (30 s) |
| ext4 (runc, `hostPath`) | `EDQUOT` (122) | `write()` | intact | not applicable |
| Kata block-plain, with `disk.img` moved into a 128 MiB project | the slot's `exec` failed with `ttrpc: closed` | VM | the pod failed (phase `Failed`) 6 s later | the pod died |

All rows are [measured].
- No error was deferred to `fsync` or `close` on any runtime, including gVisor, whose writes reach the host file in `write()`. [measured]
- The project limit and the kubelet's `sizeLimit` are independent. With the project limit raised to 2 GiB above a 1 GiB `sizeLimit`, a 1,100 MiB write went through and the kubelet evicted the pod. [measured]
  - The event read "Usage of EmptyDir volume "ws" exceeds the limit "1Gi"".
  - Eviction came 31 s after the write on runc and 70 s after it on runc with `hostUsers: false`.
- With a project limit below `sizeLimit`, writes stop at the project limit and the kubelet never evicts. [measured]
- The block-plain failure fits QEMU stopping the VM when its image write returns `ENOSPC`. [inference]

### Escape by the slot

The slot created a directory inside the project, then tried these with `FS_IOC_FSSETXATTR`:
1. Set the directory's project ID to 0.
2. Clear the directory's PROJINHERIT flag.
3. Set the directory's project ID to 777.
4. Set the project ID of a file inside it to 0.

Then it wrote 32 MiB (16 MiB on gVisor and Kata) into the file.

| Runtime | Result | Bytes counted to the project |
| :-- | :-- | :-- |
| runc (XFS) | every call succeeded; the directory ended at project 777 and the file at project 0 | none of the 32 MiB (project stayed at 92,164 KiB while `du` grew to 127.9 MB) |
| ext4 (runc) | every call succeeded; directory at 777, file at 0 | none (`bcount` stayed 94,384,128) |
| runc `hostUsers: false` | every call returned `EINVAL`; IDs unchanged | all 32 MiB |
| gVisor default mount | `ENOTTY` (ioctl not supported) | all |
| Kata virtio-fs | `ENOTTY` | all |

All rows are [measured].
- The kernel lets the owner of an inode change its project ID from the initial user namespace, and refuses the change from any other user namespace. This matches the `EINVAL` under `hostUsers: false`. [inference about the mechanism; the results are measured]
- On runc without user namespaces, a slot can therefore keep its bytes out of the project. The kubelet's walk still counts them, so the escape trades the slot's own clean failure for a pod-wide eviction. [inference] SI-11 already renders `hostUsers: false` on runc pools, and an operator who disables it loses this bound. [inference]

### Kubelet feature gate `LocalStorageCapacityIsolationFSQuotaMonitoring`

k3s restarted with `--kubelet-arg=feature-gates=LocalStorageCapacityIsolationFSQuotaMonitoring=true`.

| Pod | Project the kubelet assigned | Limit the kubelet wrote | Kubelet stats for the volume |
| :-- | :-- | :-- | :-- |
| `hostUsers: true` | none (project 0, no PROJINHERIT) | none | not read by the probe |
| `hostUsers: false` | 1048577 (`ws`) and 1048578 (`other`), PROJINHERIT set, entries in `/etc/projects` and `/etc/projid` | soft and hard 9,007,199,254,740,992 KiB (2^53, no effective limit) | `usedBytes` 104,857,600 after a 100 MiB write |

All rows are [measured].
- With the kubelet's own limit in place, `statfs` in the pod reports the whole filesystem, because that limit exceeds it. [measured]
- **Lenny's limit on the kubelet's project.** The agent set 256 MiB on project 1048577 with `-limit-only`. [measured]
  - The slot's write stopped with `ENOSPC` at 256 MiB.
  - `statfs` in the pod reported 256.0 MiB size and 256.0 MiB used.
  - 30 s later the kubelet had not reset the limit.
- **Lenny's own project over the kubelet's.** The agent moved `other` from 1048578 to project 53201. [measured]
  - After 120 MiB was written, the kubelet's stats reported `usedBytes: 0` for the volume.
  - The kubelet reads its own project ID, so it no longer sees the volume's growth. [inference]
  - Its sizeLimit eviction depends on that figure, so it would no longer fire. [inference]
- **Eviction with the gate on.** The pod was evicted 4 s after a 700 MiB write to a 512 MiB `sizeLimit` volume. [measured]
- The kubelet removed its project's quota when the pod was deleted. Two lines from the deleted pod were still in `/etc/projects` when the next pod started, and that pod received project 1048580. [measured]

### gVisor pod-share and Kata block-plain

- **gVisor pod-share** (the `dev.gvisor.spec.mount.ws.share: pod` and `type: bind` annotations, as in IC-8). [measured]
  - The host emptyDir directory holds one file, `.gvisor.filestore.<sandbox id>`, owned by root. It existed before the agent could assign the project, so it kept project 0.
  - Inside the pod, the volume is a sentry `tmpfs` with an 8 EiB size. `statfs` reports the sentry's usage (40.0 MiB after 40 MiB, then 90.0 MiB).
  - With only the directory assigned, a 400 MiB write through a 256 MiB project succeeded.
  - With the agent setting the filestore file's own project ID as well, writes stopped with `ENOSPC` at 256 MiB. A 200 MiB file and 56 MiB of a second file were intact.
  - The sentry's `statfs` keeps the 8 EiB size, so the slot cannot read the limit there. [measured]
- **Kata block-plain.** [measured]
  - The host directory holds `disk.img`, a sparse file whose apparent size is 64,357,400,576 bytes, the size of the node's `/data` filesystem. The guest sees an ext4 device of 61,271 MiB.
  - The guest's `statfs` tracks the volume's usage exactly: 64.0 MiB after 64 MiB, 114.0 MiB after 50 MiB more under a mode-000 directory, and 50.0 MiB after deleting the 64 MiB file. The mount uses `discard`.
  - The image's allocated size ran about 53 MiB above the guest's figure (117 MiB, 167 MiB, then 103 MiB).
  - The kubelet counts the image's allocated blocks (103.5 MiB) and evicted the pod 56 s after a 1,200 MiB write to a 1 GiB `sizeLimit` volume.
  - The device's size does not bound the volume's writes to `sizeLimit`, which differs from the inference in PL-5's O9 option (a) and in the open decision's mitigation list. The bound is the node filesystem.
  - As noted above, a host quota on the image kills the VM.

## Proposal: detect and use project quotas automatically

The steps below are [inference] built on the measurements above.

1. **Node agent detection.** The privileged node agent (or `lenny-ops`'s DaemonSet) runs on every node. For the kubelet's root directory it reads:
   - the superblock options from `/proc/self/mountinfo`, checking for `xfs` with `prjquota` (or `pquota`), or `ext4` with `prjquota`;
   - the quota state through `quotactl_fd(Q_XGETQSTATV)`, requiring project accounting and enforcement both on.

   It labels the node, for example `lenny.dev/project-quota=enforcing`. The agent needs `CAP_SYS_ADMIN` and a `hostPath` of `/var/lib/kubelet` with `HostToContainer` propagation. `privileged` is not required (measured with `CAP_SYS_ADMIN` alone).
2. **Kubelet gate check.** The agent reads the kubelet's feature gate from `/configz` or the node's config. Projects of the kubelet's (ID ≥ 1048577 with an `/etc/projects` entry) appear only on user-namespace pods when the gate is on.
3. **Per-volume assignment, when a pod lands on a quota-capable node.** For each disk-backed slot volume (`slot-<n>-workspace`, `slot-<n>-session`, and `slot-<n>-tmp`), the agent does one of two things:
   - If the kubelet already assigned a project to the directory, it keeps that ID and sets the hard limit on it.
   - If not, it assigns a Lenny project ID from a range below 1048576 with PROJINHERIT, and sets the hard limit.

   It never reassigns a kubelet project, because that blinds the kubelet's accounting. The limit is the volume's `sizeLimit` less a margin (for example 1-2%), so the slot meets `ENOSPC` before the kubelet's walk reaches `sizeLimit`.
4. **Adapter reads the result unprivileged.** The adapter does not need the agent to report back. It calls `fstatfs` on the slot's broker-passed root descriptor:
   - When the reported size is below the node filesystem's size and the root's `FS_IOC_FSGETXATTR` shows PROJINHERIT with a nonzero project, the volume is quota-bound. The watch uses `statfs` as PL-5's method 1, with no walk.
   - Where `FS_IOC_FSGETXATTR` is unsupported (gVisor default and Kata virtio-fs return `ENOTTY`), a `statfs` size equal to the expected limit is the signal.
5. **What the slot meets.** With the quota below `sizeLimit`, the slot gets `ENOSPC` (`EDQUOT` on ext4) at `write()`, and the pod is never evicted for that volume. The adapter's 90% watch still fails the session for `workspace_size_limit` as PL-5 states, now as a clean per-slot failure.
6. **Runtimes that take part:**
   - runc with `hostUsers: false`;
   - gVisor's default mount;
   - Kata virtio-fs.

   On runc without user namespaces, the slot can escape the project, so the quota is not a bound there. The pool should either require user namespaces for the quota mode, or keep the walk as the authority and use the quota only as an early signal.
7. **Runtimes that stay on the current methods:**
   - **gVisor pod-share:** the agent can move the `.gvisor.filestore.*` file into the project, which enforces the limit, but the slot cannot read it through `statfs`. The adapter keeps reading the sentry's `statfs` usage (method 1 as PL-5 states), and the project becomes a backstop rather than the watch's source.
   - **Kata block-plain:** never put a quota on `disk.img`, because the VM dies at the limit. The guest's `statfs` gives exact usage. PL-5's pending block-plain measurement is now answered: method 1 applies for usage, the kubelet's figure runs about 53 MiB above it, and `sizeLimit` remains the only bound.
8. **Fallback.** On a node without `prjquota`, or where the agent cannot assign a project, the pod uses PL-5's methods 1-3 unchanged. The node label and the adapter's `statfs` check agree on which path is in force, so the adapter needs no configuration.

## Validation still open

- EKS (Bottlerocket and AL2023), GKE (COS), and AKS (Azure Linux and Ubuntu) node images were not checked for `prjquota` on the kubelet's filesystem. Their default mount options decide whether step 1 ever finds a capable node. [inference]
- The 1-2% margin between the quota and `sizeLimit` was not measured against XFS preallocation on runc and Kata. Only gVisor showed preallocation running ahead of `du` (up to about 38 MiB). [measured for gVisor]
- Reflink clones under a project quota were not tested. [inference: XFS charges remapped extents to the destination's project]

## Spend and cleanup

- **Spend:** one `m8i.2xlarge` for 0.619 h ($0.27, including its 100 GB of gp3 and its public IPv4 address) and S3 transfer under 100 MB. The total is **$0.27**. [measured from `aws/ledger.py`]
- **Created and deleted** (verified at 2026-11-14T07:48:01Z):

  | Resource | ID | Verification |
  | :-- | :-- | :-- |
  | Instance | `i-009dc1f94dc2f2721` | `terminated` |
  | Volume (root) | `vol-063764d1ec618b69a` | `InvalidVolume.NotFound` |
  | Volume (data) | `vol-062265c6d24bbe0e2` | `InvalidVolume.NotFound` |
  | Network interface | `eni-03c611cfb7b562fa5` | `InvalidNetworkInterfaceID.NotFound` |
  | Security group | `sg-00a28c4e699591e5f` | `InvalidGroup.NotFound` |
  | S3 bucket | `lenny-spike-aq-780138804904-1794640217` | `head-bucket` exit 254 |
  | IAM role | `aq-node-role` | `get-role` exit 254 |
  | IAM instance profile | `aq-node-profile` | `get-instance-profile` exit 254 |

  The instance tag query for `project=lenny-spike` returned no instance that is not terminated. [measured]
- **Cleanup fault.** The adapted cleanup script still named the previous run's inline policy (`sikp-bucket`), so the role was deleted by hand after `aq-bucket` was removed. Both deletions are in `logs/cleanup.txt`. [measured]
- **Untouched.** Instance `i-080c5e8e1e25aec21` was `running` before and after and was never targeted. No EKS cluster or the kind cluster `lenny-e2e` was touched. [measured]
- **Local.** The Docker image `aqspike:1` was removed by name. [measured]
