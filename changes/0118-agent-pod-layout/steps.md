# Steps: 0118 Agent pod layout

## Validation owed

- A pod with the full blueprint of "PL-1" and the paths of "PL-3", end to end on kind under runc and gVisor, with SI's broker. 0118.S4 waits on it.
- The per-slot disk watch (O8, D12): agreement between `statfs` on gVisor's pod-share mount and the kubelet's count, and how the 53 MiB gap of Kata `block-plain` (O10) varies with the node filesystem's size; the tracker's running total against the kubelet's figure under a fast writer, hard links, and reflink clones, and its drift between recalibration walks; the walk's cost per pass on runc, gVisor's two mounts, and Kata's two volume modes; its reaction time against a fast writer on each disk-backed volume; and project quotas through the spike below. The step that lands the disk watch waits on it: 0118.S4, or the step O11 gives the watch.
- A hostile-repository stress test suite for the store fill and the adapter's deletion (D17), run as tests on runc, gVisor, and Kata `block-plain`: repositories with setgid and setuid files, symbolic-link and hard-link farms, deep and wide trees, directories of mode `000`, name collisions, and very large files, and a deletion that races a slot's reads of the store. Each case checks that the fill's mode pass leaves no set-ID bit and no write for the other class, that the deletion stays beneath the volume root and removes the whole store, that no slot gains write access, and that a runtime image whose `/etc/group` names the store GID gains nothing. 0118.S4 and 0153.S9, which lands the fill, wait on it, and "Tests" in `change.md` carries it under `security` and `e2e_cloud`.
- Project quotas (D25, "PL-10"). The AWS spike of 2026-11-14 answered the spike D20 asked for on one EC2 node with XFS and ext4 ([its report](evidence/attribution-and-quota-spike/RESULTS.md)). Owed: whether the kubelet's filesystem is mounted with `prjquota` on the node images of EKS (Bottlerocket and AL2023), GKE (Container-Optimized OS and Ubuntu), and AKS (Azure Linux and Ubuntu), and what a deployer changes where it is not; the 2% margin against XFS speculative preallocation on runc and Kata, which the spike measured only on gVisor; reflink clones under a project quota, whose charge to the destination's project the spike did not test, against the kubelet's full-size count; the assignment of project IDs under `CAP_SYS_ADMIN` alone, which the spike ran from a privileged container; the read-write mount that `FS_IOC_FSSETXATTR` needs; the node-name claim of the agent's token that `lenny-quota-agent-node-label` reads, on each supported Kubernetes version; the kubelet keeping Lenny's limit on its own project past the spike's 30 s and across a kubelet restart; the agent's reaction time against a pod with no warm list, whose regular containers start within seconds; and the clearing of limits when pods are deleted while the agent is down. 0118.S6 waits on it, and in particular on a run of the agent under `CAP_SYS_ADMIN` alone with its seccomp profile and Landlock on one XFS node and one ext4 node, recorded under `evidence/`.
- The quota agent's hardening (D28). Owed: the seccomp profile's call list, complete for the agent's start-up, detection, assignment, deletion, Landlock, and API client paths under the kernel of each supported node image, from a run of each path under the profile in log mode on each; the profile's refusal of `mount`, `umount2`, `unshare`, `setns`, `bpf`, `ptrace`, `perf_event_open`, and `keyctl` under `CAP_SYS_ADMIN`, and of `quotactl_fd` with a command that turns quotas off; `seccomp-install`'s write into the kubelet's `seccomp` directory as UID 0 with no capability, and the kubelet's failure of the agent's container when the profile is missing; the Landlock ABI version, and whether Landlock is among the active security modules, on the node images of EKS (Bottlerocket and AL2023), GKE (Container-Optimized OS and Ubuntu), and AKS (Azure Linux and Ubuntu); `FS_IOC_FSSETXATTR` and `quotactl_fd` on a directory descriptor inside a domain that grants only `LANDLOCK_ACCESS_FS_READ_DIR`; a Landlock rule on `/proc/self/mountinfo`, a procfs entry; `syscall.AllThreadsSyscall` restricting every thread of the agent; and the refusal of `bind`, `listen`, and `accept4` under the profile (D29). Owed for the status annotation: the size of the annotation's value against the 1 KiB bound at the largest slot counts, and the CEL expression of `lenny-quota-agent-node-label` that admits the label and the annotation alone, on each supported Kubernetes version. 0118.S6 waits on it.
- `lenny-store-fill`'s 1 GiB memory limit against the largest store and pristine tree a pool's warm list names, and whether `warmCloneBytes`, and a tenant's `maxWarmCloneBytes` with it, counts each pristine tree as well as its store. 0118.S4 waits on it for `storeFill.resources`.
- The disk watch against a directory whose mode denies its owner read (O9): the walk's figure against the kubelet's on runc without user namespaces and on nodes without project quotas. `statfs` on gVisor's pod-share mount, on Kata `block-plain`, and on a project-bound volume counts its bytes **[measured]**. The step that lands the disk watch waits on it.
- `slot-<n>-netcred`'s 8 MiB against a session with many bindings and file deliveries. 0118.S4 waits on it.
- `lenny-store-fill` (D11, D17): its fill through `netagent` attributed as the platform fill, on platform and owned pools; the slots' read access to the stores through the other class; the store GID as the primary group of the adapter and the fill, under `supplementalGroupsPolicy` `Merge` and `Strict`; the webhook's refusal of the store GID on any other container, an ephemeral container included; the adapter's deletion of a withheld store through the group class under runc, gVisor, and Kata `block-plain`, where the fill regroups each store's directory from the volume GID; its UID inside the pod's range under user namespaces; and a failed fill failing the pod with the store named. 0118.S4 and 0153.S9 wait on it.
- The pod-wide spool (D13): pool admission at the defaults and at the limit, a source that waits for staging room and fails with `staging_timeout` at the run's deadline, and the kubelet's count of reflink clones in the spool against `spoolSizeLimitBytes`. 0118.S4 and 0153.S2 wait on it.
- The kubelet's sizing of a memory-backed `emptyDir` to its `sizeLimit` and the resulting `ENOSPC` on the node versions §17.6 supports. 0118.S4 waits on it.
- A bare repository store filled by `lenny-store-fill` serving alternates at `/lenny/git-stores` in a slot (D11). 0153.S9 waits on it.
- The supervisor and `lenny-materialize` running from a `0111` file on runc, gVisor, and Kata, and the non-dumpable state of the supervisor. 0118.S4 waits on it for `lenny-bin-stage`.
- `/dev/shm` per slot with `shmSizeLimitBytes`: POSIX shared memory private to the slot, System V objects and message queues still shared, and the pages charged to the slot's memory. The AWS run of 2026-11-12 still used one shared `/dev/shm` **[measured]**. 0118.S4 waits on it.
- Kata block-plain with the dedicated volume GID under the final layout, with the per-slot `/dev/shm` and the `lost+found` emptiness rule, together with the parallel-stat walk. 0118.S4 waits on it.
- `HOME=/tmp/home` with mode `0700`: npm, pip, Go modules, Cargo, `ssh`, and git under the reference runtimes. 0118.S5 waits on it.
- Harness behaviour under the constant paths: Claude Code with `CLAUDE_CONFIG_DIR=/session/claude` across a derive, and the documented state settings of the other reference harnesses. 0118.S5 waits on it.
- The echo runtime on the Go SDK under Embedded Mode. 0118.S1 waits on it.
- The credential file `/run/lenny/credentials.json`: whether any VCS path still uses `AssignCredentials` or the adapter's credential file after the LLM upstreams design removes direct delivery, as the open row of `0133-llm-upstreams-and-cost-budgets`, "Validation owed", asks. The file, `credentialsPath`, and the SDK constant stay only if one does. 0118.S5 waits on it.
- The nested mount of `slot-<n>-netcred` at `/run/netagent/credentials`, beneath `netagent`'s pod-wide volume mounted read-only at `/run/netagent`: which container creates the mount point in the pod-wide volume before the slot containers start, and whether runc, gVisor, and Kata accept the nesting. 0118.S4 waits on it.
- The ValidatingAdmissionPolicy `lenny-quota-agent-node-label` on each supported Kubernetes version: that the cluster serves the API, and that the policy, which fails closed, refuses every node write of the agent where the node-name claim is absent. 0118.S6 waits on it.
- The effect of a constant working directory on prompt-cache hit rates. No step waits on it; it measures a gain that "PL-7" claims.

## Steps

### 0118.S1 Removal of the embedded deployment model
- status: Planned
- aliases: [PL-A, PL-2]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1, 0119.S2, 0131.S9]
- closes: [F-4.7.23, F-5.2.40, F-7.2.28]

Removal of the embedded deployment model: the field, the CRD rules, the pod builder's branch, the in-process runtime and its pre-launched variant, the RuntimeReconciler validation, every specification branch, the echo runtime and the Embedded Mode seed on the Go SDK, and `command` required on every runtime. It depends on IC-9 ([0131.S9](../0131-incremental-checkpoint/steps.md#steps)), which removes the `SIGSTOP` path, the `embedded` checkpoint level, and `pkg/adapter/embeddedcheckpoint`, so this step does not remove them again. It depends on AR-A ([0119.S1](../0119-agent-runtimes-only/steps.md#steps)), because today's schema forbids `command` on a `type: mcp` runtime, so `command` cannot be required on every runtime until AR-A removes that type. It depends on AR-B ([0119.S2](../0119-agent-runtimes-only/steps.md#steps)), because "PL-1" makes every runtime container a slot container on the premise that AR removed the service-mode pods, and AR-B deletes the service-mode code that this step's pod builder would otherwise rewrite. The findings it closes describe the embedded reference runtimes, which this step deletes.

This step goes first because it removes a branch from every file that 0118.S4 (PL-B) and SI-A rewrite.

### 0118.S2 Removal of pre-launched runtimes
- status: Planned
- aliases: [PL-D, PL-8]
- lanes: [specification, code, documentation]
- depends-on: [0118.S1]
- closes: [F-4.7.33, F-5.1.35, F-6.1.29, T-4.7.10, T-5.1.5, T-6.1.3, T-6.1.6, T-6.1.7, T-6.1.8, T-6.1.11, T-6.2.1, T-15.4.0.1, T-DOC-RA.9, T-6.2.6 (part)]

Removal of pre-launched runtimes from the pod and the pool ("PL-8"), with the removal of the capability that RP D6 decided. This step removes `capabilities.preConnect` and `sdkWarmBlockingPaths` from the Runtime and lands the refusal of a write that names either ("PL-9", **Refusal of removed and renamed fields.**), and [0139.S1](../0139-runtime-protocol/steps.md#steps) lands RP's capability catalog without `preConnect`. It removes `pkg/gateway/runtime/sdkwarm`, the gateway's SDK-warm demotion decision, and changes that package's row in `tests/registers/gateway-layout.yaml` to `removed-by: '0118'`. It closes the `SDK_DEMOTION_NOT_SUPPORTED` part of T-6.2.6, the code it removes; the other timeout codes of T-6.2.6 stay open. F-5.1.35 and F-6.1.29 close with this removal, as 0140.S1 states.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-InspectForRecycle`, whose `PodRecyclePolicy` loses `PreConnect`, and `GWSEAM-RecyclePod`, which loses its `preConnect` argument (PL-8). The other pod recycle operations carry no pre-launch value in `pkg/gateway/seam`, and [0145.S6](../0145-session-isolation/steps.md#steps) reshapes them for the dead slot and the pod-wide scrub.

### 0118.S3 Renames and acknowledgeSharedPod
- status: Planned
- aliases: [PL-E, PL-9]
- lanes: [specification, code, documentation]
- depends-on: [0119.S2]

The renames and `acknowledgeSharedPod` of "PL-9", across the specification, the schemas, the code, the charts, and the documentation. It renames the pool's `sessionPolicy.maxConcurrentSessions` and `acknowledgeProcessLevelIsolation` and nothing else: a credential pool's `maxConcurrentSessions` (§4.9) and a tenant's `max_concurrent_sessions` quota keep their names. It waits for AR-B ([0119.S2](../0119-agent-runtimes-only/steps.md#steps)) so that it renames no service-mode field, and it can land before this change's other steps. Until slot containers land, the refusal of a shared pool and `sessionIsolationLevel.residualStateWarning` keep today's list of what concurrent slots share, which names the process namespace, `/tmp`, the memory cgroup, and the network stack, and the acknowledgment keeps its use for `recycle.runtimeProcess: keep`. The list of "PL-9" replaces today's list in the step that lands slot containers, [0145.S1](../0145-session-isolation/steps.md#steps) or the branch it shares with 0118.S4 if open decision O11 settles so, and the `keep` use leaves with `keep` in [0145.S3](../0145-session-isolation/steps.md#steps). It touches the same files as most other changes, so landing it first keeps their diffs on the new names.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes: `GWSEAM-InspectForRecycle`, whose `PodRecyclePolicy.MaxConcurrentSessions` becomes `MaxConcurrentSessionsPerPod`.

### 0118.S4 Layout and volumes
- status: Planned
- aliases: [PL-B, PL-1, PL-3, PL-4, PL-5]
- lanes: [specification, code, documentation]
- depends-on: [0118.S1]

The pod blueprint of "PL-1" and the layout and volumes: the paths of "PL-3", the reserved names, the volumes of "PL-5" with their size limits, `lenny-bin-stage`, the reserved root, the `/adapter` view, the removal of `/artifacts` and `slotlayout`, the removal of the `lenny-cred-readers` group and of the pod-level `fsGroup` for credential delivery (the §13.1 row, PLR-10), the canary order, the disk watch with its three methods, the Kata block-plain volume GID and its `fsGroup` exception, the eviction and ephemeral-storage rules, the rendering of the `lenny-store-fill` init container and of `lenny-spool`, `slot-<n>-netcred`, and O3, O6, O7, and O8.

This step renders the per-slot volumes that "PL-5" lists. SI-A ([0145.S1](../0145-session-isolation/steps.md#steps)) lands the slot containers that mount them. This step's pod rendering and 0145.S1 are built on one shared branch (O11, ordering part, D30). How the disk watch waits for SI's broker ([0145.S2](../0145-session-isolation/steps.md#steps)) and, for its method 2, for the change tracker ([0131.S8](../0131-incremental-checkpoint/steps.md#steps)), and which step renders `lenny-store-fill`, remain open in O11 in `decisions.md`.

Other steps own parts of the mechanisms this step renders. [0153.S9](../0153-workspace-materialization/steps.md#steps) lands the `lenny-store-fill` program, the store fill and the pristine trees moved out of the adapter, and the adapter's delete-only use of the store volume. [0127.S5](../0127-egress-and-git/steps.md#steps) lands `netagent`'s attribution of the fill's UID (egress decision 61). [0153.S2](../0153-workspace-materialization/steps.md#steps) lands the spool's staging accounting, its wait, and its admission floor under `spoolSizeLimitBytes`. [0145.S5](../0145-session-isolation/steps.md#steps) lands the adapter's `uid_map` check of step 2 of the start-up order, and [0145.S3](../0145-session-isolation/steps.md#steps) lands the launch at bind. This step owns the Kata volume GID and the disk watch.

PLR-1 holds in full once `keepalive` ([0128.S2](../0128-forensics-evidence-preservation/steps.md#steps)) and `netagent` ([0127.S2](../0127-egress-and-git/steps.md#steps)) have landed.

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
- depends-on: [0118.S4, 0136.S7, 0136.S8]

Project quotas ("PL-10", D25): `lenny-quota-agent` with its image, DaemonSet, rights, seccomp profile and its installer, Landlock restriction (D28), admission policy, and chart values, the adapter's quota-bound method, the status annotation with the metrics and the alert that `lenny-ops` derives from it (D29), posture's `diskQuota`, and the node requirements in the documentation. It needs 0118.S4 for the disk watch, [0136.S7](../0136-placement/steps.md#steps) for the named node agents and the narrowing of `lenny-node-agents`, and [0136.S8](../0136-placement/steps.md#steps) for the continuous-check pass in `lenny-ops` to which it adds `diskQuota`, the `lenny_quota_agent_*` series, and `QuotaAgentUnavailable`. The quota agent's admission policy, its metrics, its alert, and `diskQuota` stay in this step (pod-structure-C7). The 1 KiB bound on `lenny.dev/quota-agent-status` is open decision O17 in `decisions.md`.
