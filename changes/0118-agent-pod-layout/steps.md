# Steps: 0118 Agent pod layout

## Validation owed

- A pod with the full blueprint of "PL-1" and the paths of "PL-3", end to end on kind under runc and gVisor, with SI's broker.
- The per-slot disk watch (O8, D12): agreement between `statfs` on gVisor's pod-share mount and the kubelet's count, and how the 53 MiB gap of Kata `block-plain` (O10) varies with the node filesystem's size; the tracker's running total against the kubelet's figure under a fast writer, hard links, and reflink clones, and its drift between recalibration walks; the walk's cost per pass on runc, gVisor's two mounts, and Kata's two volume modes; its reaction time against a fast writer on each disk-backed volume; and project quotas through the spike below.
- A hostile-repository stress test suite for the store fill and the adapter's deletion (D17), run as tests on runc, gVisor, and Kata `block-plain`: repositories with setgid and setuid files, symbolic-link and hard-link farms, deep and wide trees, directories of mode `000`, name collisions, and very large files, and a deletion that races a slot's reads of the store. Each case checks that the fill's mode pass leaves no set-ID bit and no write for the other class, that the deletion stays beneath the volume root and removes the whole store, that no slot gains write access, and that a runtime image whose `/etc/group` names the store GID gains nothing.
- Project quotas (D25, "PL-10"). The AWS spike of 2026-11-14 answered the spike D20 asked for on one EC2 node with XFS and ext4 ([its report](evidence/attribution-and-quota-spike/RESULTS.md)). Owed: whether the kubelet's filesystem is mounted with `prjquota` on the node images of EKS (Bottlerocket and AL2023), GKE (Container-Optimized OS and Ubuntu), and AKS (Azure Linux and Ubuntu), and what a deployer changes where it is not; the 2% margin against XFS speculative preallocation on runc and Kata, which the spike measured only on gVisor; reflink clones under a project quota, whose charge to the destination's project the spike did not test, against the kubelet's full-size count; the assignment of project IDs under `CAP_SYS_ADMIN` alone, which the spike ran from a privileged container; the read-write mount that `FS_IOC_FSSETXATTR` needs; the node-name claim of the agent's token that `lenny-quota-agent-node-label` reads, on each supported Kubernetes version; the kubelet keeping Lenny's limit on its own project past the spike's 30 s and across a kubelet restart; the agent's reaction time against a pod with no warm list, whose regular containers start within seconds; and the clearing of limits when pods are deleted while the agent is down.
- The quota agent's hardening (D28). Owed: the seccomp profile's call list, complete for the agent's start-up, detection, assignment, deletion, Landlock, and API client paths under the kernel of each supported node image, from a run of each path under the profile in log mode on each; the profile's refusal of `mount`, `umount2`, `unshare`, `setns`, `bpf`, `ptrace`, `perf_event_open`, and `keyctl` under `CAP_SYS_ADMIN`, and of `quotactl_fd` with a command that turns quotas off; `seccomp-install`'s write into the kubelet's `seccomp` directory as UID 0 with no capability, and the kubelet's failure of the agent's container when the profile is missing; the Landlock ABI version, and whether Landlock is among the active security modules, on the node images of EKS (Bottlerocket and AL2023), GKE (Container-Optimized OS and Ubuntu), and AKS (Azure Linux and Ubuntu); `FS_IOC_FSSETXATTR` and `quotactl_fd` on a directory descriptor inside a domain that grants only `LANDLOCK_ACCESS_FS_READ_DIR`; a Landlock rule on `/proc/self/mountinfo`, a procfs entry; `syscall.AllThreadsSyscall` restricting every thread of the agent; and the refusal of `bind`, `listen`, and `accept4` under the profile (D29). Owed for the status annotation: the size of the annotation's value against the 1 KiB bound at the largest slot counts, and the CEL expression of `lenny-quota-agent-node-label` that admits the label and the annotation alone, on each supported Kubernetes version.
- `lenny-store-fill`'s 1 GiB memory limit against the largest store and pristine tree a pool's warm list names, and whether `warmCloneBytes`, and a tenant's `maxWarmCloneBytes` with it, counts each pristine tree as well as its store.
- The disk watch against a directory whose mode denies its owner read (O9): the walk's figure against the kubelet's on runc without user namespaces and on nodes without project quotas. `statfs` on gVisor's pod-share mount, on Kata `block-plain`, and on a project-bound volume counts its bytes **[measured]**.
- `slot-<n>-netcred`'s 8 MiB against a session with many bindings and file deliveries.
- `lenny-store-fill` (D11, D17): its fill through `netagent` attributed as the platform fill, on platform and owned pools; the slots' read access to the stores through the other class; the store GID as the primary group of the adapter and the fill, under `supplementalGroupsPolicy` `Merge` and `Strict`; the webhook's refusal of the store GID on any other container, an ephemeral container included; the adapter's deletion of a withheld store through the group class under runc, gVisor, and Kata `block-plain`, where the fill regroups each store's directory from the volume GID; its UID inside the pod's range under user namespaces; and a failed fill failing the pod with the store named.
- The pod-wide spool (D13): pool admission at the defaults and at the limit, a source that waits for staging room and fails with `staging_timeout` at the run's deadline, and the kubelet's count of reflink clones in the spool against `spoolSizeLimitBytes`.
- The kubelet's sizing of a memory-backed `emptyDir` to its `sizeLimit` and the resulting `ENOSPC` on the node versions §17.6 supports.
- A bare repository store filled by `lenny-store-fill` serving alternates at `/lenny/git-stores` in a slot (D11).
- The supervisor and `lenny-materialize` running from a `0111` file on runc, gVisor, and Kata, and the non-dumpable state of the supervisor.
- `/dev/shm` per slot with `shmSizeLimitBytes`: POSIX shared memory private to the slot, System V objects and message queues still shared, and the pages charged to the slot's memory. The AWS run of 2026-11-12 still used one shared `/dev/shm` **[measured]**.
- Kata block-plain with the dedicated volume GID under the final layout, with the per-slot `/dev/shm` and the `lost+found` emptiness rule, together with the parallel-stat walk.
- `HOME=/tmp/home` with mode `0700`: npm, pip, Go modules, Cargo, `ssh`, and git under the reference runtimes.
- Harness behaviour under the constant paths: Claude Code with `CLAUDE_CONFIG_DIR=/session/claude` across a derive, and the documented state settings of the other reference harnesses.
- The echo runtime on the Go SDK under Embedded Mode.
- The effect of a constant working directory on prompt-cache hit rates.

## Steps

### 0118.S1 Removal of the embedded deployment model
- status: Planned
- aliases: [PL-A, PL-2]
- lanes: [specification, code, documentation]

Removal of the embedded deployment model: the field, the CRD rules, the pod builder's branch, the in-process runtime and its pre-launched variant, the RuntimeReconciler validation, every specification branch, the echo runtime and the Embedded Mode seed on the Go SDK, and `command` required on every runtime. IC-9 ([0131.S9](../0131-incremental-checkpoint/steps.md#steps)) lands in the same release or earlier, because IC-9 removes the `SIGSTOP` path and the `embedded` level.

This step goes first because it removes a branch from every file that 0118.S4 (PL-B) and SI-A rewrite.

### 0118.S2 Removal of pre-launched runtimes
- status: Planned
- aliases: [PL-D, PL-8]
- lanes: [specification, code, documentation]
- depends-on: [0118.S1]

Removal of pre-launched runtimes from the pod and the pool ("PL-8"), with RP D6's removal of the capability. RP's proposal for D6 lands in the same release.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes the pod recycle operations (`GWSEAM-IncrementSessionsServed`, `GWSEAM-IncrementScrubFailureCount`, `GWSEAM-RecycleCounters`, `GWSEAM-RecordLeak`, `GWSEAM-RetireOnSessionCount`, `GWSEAM-InspectForRecycle`, `GWSEAM-RecyclePod`, `GWSEAM-RetirePod`, `GWSEAM-RecordScrubFailure`, and `GWSEAM-RecordPodRetirement`), whose recycle policy no longer carries `preConnect` (PL-8).

### 0118.S3 Renames and acknowledgeSharedPod
- status: Planned
- aliases: [PL-E, PL-9]
- lanes: [specification, code, documentation]
- depends-on: [0119.S2]

The renames and `acknowledgeSharedPod` of "PL-9", across the specification, the schemas, the code, the charts, and the documentation. It waits for AR-B ([0119.S2](../0119-agent-runtimes-only/steps.md#steps)) so that it renames no service-mode field, and it can land before this change's other steps, because it changes names only. It touches the same files as most other proposals, so landing it first keeps their diffs on the new names.

### 0118.S4 Layout and volumes
- status: Planned
- aliases: [PL-B, PL-3, PL-4, PL-5]
- lanes: [specification, code, documentation]
- depends-on: [0118.S1]

The layout and volumes: the paths of "PL-3", the reserved names, the volumes of "PL-5", `lenny-bin-stage`, the reserved root, the `/adapter` view, the removal of `/artifacts` and `slotlayout`, the canary order, the disk watch with its three methods, the Kata block-plain exception, the eviction and ephemeral-storage rules, `lenny-store-fill`, the pod-wide spool, `slot-<n>-netcred`, and O3, O6, O7, and O8. SI-A ([0145.S1](../0145-session-isolation/steps.md#steps)) lands in the same release, because SI-A renders the per-slot volumes this design names. WM's spool change and the egress design's decision 61 land in the same release.

### 0118.S5 Runtime-facing environment and contract
- status: Planned
- aliases: [PL-C, PL-6]
- lanes: [specification, code, documentation]
- depends-on: [0118.S4]

The runtime-facing environment and the contract: O1, O2, and O4, the SDKs, the reference runtimes' `env`, §26, §24.18, and the runtime author guide.

### 0118.S6 Project quotas
- status: Planned
- aliases: [PL-F, PL-10]
- lanes: [specification, code, documentation]
- depends-on: [0118.S4, 0136.S3]

Project quotas ("PL-10", D25): `lenny-quota-agent` with its image, DaemonSet, rights, seccomp profile and its installer, Landlock restriction (D28), admission policy, and chart values, the adapter's quota-bound method, the status annotation with the metrics and the alert that `lenny-ops` derives from it (D29), posture's `diskQuota`, and the node requirements in the documentation. It needs 0118.S4 for the disk watch, and placement's PM-C for the named node agents and `lenny-node-agents`.
