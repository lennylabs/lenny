# Incremental checkpoints

Status: design for owner review, revision 12, 2026-11-10. An independent reader review read revision 4. On 2026-11-10 an adversarial review of the output-file export read revision 10 as first written, and the same revision, extended that day, applied its findings. A re-review read the extended revision 10 the same day, and revision 11 applies the fixes the owner approved with decisions A18 to A21 of `message-delivery-design.md`, with A19 as the owner revised it that day after a short review of that design's revision 6. The owner has settled decisions D7, D8, D14, and D15, and has ruled out a mechanism that forces a runtime to pause (see "Owner's direction"). It reads against [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 10 and [`message-delivery-design.md`](message-delivery-design.md) revision 8. Proposals carry the label IC-n. "Review history" at the end records how the design reached this form.

**Evidence sources.** The measurements this design cites come from a laptop and from AWS. "Measurements" holds runs on a laptop on 2026-11-05 against a copy of today's archive code. "Benchmarks" holds runs on AWS on 2026-11-05 and 2026-11-06, made with the *harness*, a standalone program that implements both today's checkpoint method and this design's capture, storage, and restore paths and runs no Lenny component. Its "Kubernetes checks" ran the same builds on a single-node cluster. A pause written as *walk / tracker* gives the figure with the stat walk and with the change tracker as the change source. "Benchmark terms" under "Terms" defines the scenarios and configurations the tables name.

## Problem

[§4.4 Event / Checkpoint Store](../spec/04_system-components.md#44-event--checkpoint-store) archives a session's whole workspace into one gzip-compressed tar stream on every checkpoint. A Full-level runtime, which pauses itself on request so that the checkpoint is consistent, stays quiesced until the last byte of that stream is uploaded. Both the cost of a checkpoint and the length of the pause grow with the size of the workspace.

- **The pause grows with the workspace.** The adapter, Lenny's container in the agent pod that runs the checkpoint, sends `checkpoint_request` (see "Quiesce handshake" under "Terms"), archives and uploads every byte, and sends `checkpoint_complete` after the stream ends (`Checkpoint` in `pkg/adapter/checkpoint.go`). [§4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling](../spec/04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling) states the relationship as `expected_quiescence_seconds ≈ workspace_bytes / (100 × 1024 × 1024)`.
- **A large workspace never completes a checkpoint.** A periodic attempt runs under the 60-second `CheckpointTimeout` (`attemptContext` in `pkg/gateway/checkpoint/checkpointer/checkpointer.go`). A workspace that cannot be archived and uploaded in 60 seconds fails every attempt. Each failed attempt still pauses a Full-level runtime for up to 60 seconds, and the attempt repeats every `periodicCheckpointIntervalSeconds`, the gateway-wide checkpoint interval.
- **Every attempt starts over.** A tar stream has no reuse between attempts. A session holding a 5 GiB clone uploads 5 GiB on each checkpoint, even when one file changed. The tar chunks a timed-out attempt uploaded are a prefix of a tar stream that the next attempt cannot use.
- **The size limit replaces one failure with another.** [§4.4.4 Checkpoint SLOs and Scheduling](../spec/04_system-components.md#444-checkpoint-slos-and-scheduling) caps the workspace at the pool's `workspaceSizeLimitBytes`, 512 MiB by default, to bound the pause. Under that cap, a session that clones a large monorepo would be evicted by the size limit of the workspace's `emptyDir` volume (`emptyDir.sizeLimit`), or would run without checkpoints because the adapter's size probe before each attempt rejects every attempt. The tree implements neither limit by default (see "Defects found while verifying"), so such a session meets the 60-second `CheckpointTimeout` of the preceding bullet.
- **Best-effort checkpoints fail when files change.** The spec describes a checkpoint of a Basic-level or Standard-level runtime, which does not pause, as a snapshot with minor inconsistencies. The archive code fails the whole attempt when a file grows during the archive or disappears between the directory walk and the read (see "Measurements").
- **Interrupts wait for uploads.** The Checkpoint handler holds the pod-wide operation lock, which serializes a checkpoint with an interrupt, from before the size probe until the upload ends (`pkg/adapter/checkpoint.go`, `s.ops.Begin`), so an interrupt waits for the whole upload on every integration level.

## Scope

This design covers how the adapter captures a workspace, the stored format, the upload and restore protocol, derive, retention, garbage collection, and the runtime's quiesce contract, for checkpoints and for the seal at session end. It also covers the Runtime and pool settings that turn checkpointing off and set the checkpoint interval, and the removal of the embedded adapter's `SIGSTOP` checkpoint path (IC-9). It applies to every isolation profile and every integration level.

The event store, the transcript, and the eviction minimal-state record in [§4.4.5 Checkpoint Storage Failure and Eviction Fallback](../spec/04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback) are unchanged. Node-level filesystem snapshots are evaluated and deferred (see "Node-level snapshots").

## Terms

| Term | Meaning |
| :-- | :-- |
| Current method | Today's checkpoint path. `ArchiveTree` archives the whole workspace into one gzip-compressed tar stream, and the adapter uploads the stream in fixed-size tar chunks while a Full-level runtime stays quiesced. See "Current state", and "Tar chunk and partial manifest" below. |
| Snapshot | One stored capture of a session's workspace under this design: a manifest and the packs it references, recorded by a snapshot row in Postgres. A checkpoint or a seal produces one. |
| Seal | The snapshot taken at session end, which becomes the session's `final_workspace_ref`. See IC-5, "Seal". |
| Pre-copy pass | The first pass of a checkpoint. It reads every file that has no trusted entry in the file index and uploads their new chunks while the runtime keeps running. See IC-1, "Pre-copy". |
| Capture pass | The second pass. It finds the files changed since pre-copy and reads or clones them into the spool. A Full-level runtime is quiesced during this pass and no other. See IC-1, "Capture". |
| Upload pass | The third pass. After the runtime resumes, it packs the spooled bytes, uploads them, and asks the gateway to finalize the snapshot. See IC-1, "Upload". |
| Pause | The time a Full-level runtime stays quiesced for one checkpoint: the handshake, the change source, and reading or cloning the changed files. |
| Pause budget | The pool's `checkpointPauseBudgetSeconds`, the time the capture pass may take. The adapter compares it with a capture time it estimates before it sends `checkpoint_request`, and it ends a capture that runs past it. See IC-1, "Pause budget and convergence". |
| Pre-copy round and convergence | A pre-copy round is one pre-copy pass followed by a new estimate. Convergence is running further rounds, up to the pool's `checkpointPreCopyRounds`, until the estimate fits the pause budget. Live migration of a virtual machine works the same way: it copies memory while the machine runs and pauses the machine only to copy the last changed pages. |
| Changed set | The files the capture pass reads: files whose identity differs from the file index, dirty files, and racy entries. |
| File identity | What `lstat` reports for a file and this design compares: device, inode, size, `mtime` and `ctime` in nanoseconds, and mode. |
| File index | The adapter's record of each file's identity and chunk list from the session's previous snapshot. A file whose identity matches a trusted entry is not read again. |
| Trusted entry | A file index entry whose identity matches the file's current identity and that is not a racy entry. Pre-copy keeps a trusted entry's chunk list without reading the file. See IC-1, "Change detection". |
| Dirty file | A file whose identity changed while pre-copy read it, which the capture pass reads again, or a path the change tracker reported. |
| Racy entry | An index entry whose file `mtime` falls within `racyWindow` before the read that produced the entry started. The capture pass reads it again, following git's rule for racily clean index entries. See IC-1, "Change detection". |
| Change source | How the capture pass learns which files changed: the stat walk or the change tracker. |
| Stat walk | A parallel walk of the workspace that calls `lstat` on every entry and reads no content. Its cost grows with the number of files. "The walk" means this walk. |
| Change tracker | An inotify watcher in the adapter that records which paths changed since the last capture, so that the capture pass does not walk. "The tracker" means this watcher. See IC-1, "Change tracker". |
| Barrier file and swap | At the capture, the adapter writes a barrier file in the slot, and the tracker reads events until it sees the barrier file's event, returns its dirty set, and starts an empty one. That exchange is the swap, and the time the adapter waits for it is the barrier wait. The barrier file is unrelated to the gateway's CheckpointBarrier protocol. |
| Degraded tracker | A tracker that failed to add a watch or missed a change that the pre-copy cross-check found. Every capture walks until the adapter restarts it. |
| Spool | `/workspace/staging/checkpoint/`, where the capture pass places cloned files and new chunks until the upload pass packs them. |
| Spool cap | The pool's `checkpointCaptureBudgetBytes`, 512 MiB by default, which caps the disk use of the spool and of the output-file export clones under `/workspace/staging/export/` (IC-7). Export clones, those of parts waiting for the export cap included, may use at most `exportSpoolFraction` of it, a constant of 0.5, which is 256 MiB at the default. `CheckpointStart` carries it as `capture_budget_bytes`. |
| Size probe and reflink probe | The size probe is the adapter's measurement of the workspace before each attempt, which it reports in `CheckpointProbe` and which refuses an attempt over `workspaceSizeLimitBytes`. The reflink probe is the adapter's check at start-up of whether the spool accepts `FICLONE`. |
| Reflink clone | A copy that shares the source file's disk blocks, made with the `FICLONE` ioctl, as `cp --reflink` does. It needs no privilege on XFS with `reflink=1` and on Btrfs, and its time does not grow with the file's size. |
| Chunk and chunk ID | A piece of a file cut by content-defined chunking (FastCDC), named by the SHA-256 of its uncompressed bytes. See IC-2. |
| Pack and pack ID | An object in the object store that holds many compressed chunks and a footer listing them, named by the SHA-256 of its bytes. The footer carries the pack nonce, a random 128-bit value the adapter writes into every pack it builds, so no two packs an adapter builds share a pack ID. The catalog identifies a pack by its object key, `(tenant_id, owner_session_id, pack_id)`, because a pod chooses the bytes it declares, so two sessions that declare identical bytes hold two packs. See IC-2. |
| Manifest | The sorted list of a snapshot's entries, one per path, with each file's chunk IDs. It is itself chunked and stored in packs. |
| Dedup set | The chunks the adapter treats as already stored, which it references instead of uploading. It is built from the same sources as the allowed set, and a chunk enters it only after the gateway confirms the chunk's pack with `PackCommitted`. Checkpoints and exports of a session share it. |
| Allowed set and reference closure | The allowed set is the packs a session may reference in a new snapshot. The reference closure check is the gateway's refusal of a snapshot that references a pack outside that set. See IC-4. |
| Pack lease | A pack whose upload the gateway confirmed and that no snapshot or output file references yet is in state `unreferenced`, and it is kept until its lease expires, so that a later attempt or export can reuse its chunks. The lease length is the gateway setting `packLeaseSeconds`, 1800 seconds by default, which pool admission requires to be at least twice the larger of `checkpointAttemptTimeoutSeconds` and `exportTimeoutSeconds`, and each reference to the pack extends it. See IC-4, "Catalog". |
| "Already stored" answer | The gateway's answer `PackStored` to a `PackReady` whose pack is already in the session's allowed set. It covers the adapter's resend of a pack it built, after a `PackCommitted` the stream lost. The adapter skips that upload and references the pack. See IC-3. |
| Export queue | The file parts of a session that wait because `files.maxConcurrentExports` exports already run, at most `files.maxQueuedExports`, 8 by default ([MD-18](message-delivery-design.md#md-18-files)). |
| Export record | The `output_file_exports` row the gateway writes for each file part, keyed by session and part, which holds the export's state, its clocks, its quota reservation, and the coordination generation that drives it. Every update of the row carries that generation as a guard. See IC-4, "Catalog". |
| Unopened part | A file part that the adapter relays with `unopened: true` because it already holds `files.maxQueuedExports` plus `files.maxConcurrentExports` open export descriptors for the session. The gateway refuses exactly such parts with `too_many_exports` ([MD-18](message-delivery-design.md#md-18-files)). See IC-3. |
| `ExportAbort` | The message with which the gateway ends a failing export on the `ExportOutputFile` stream, with a reason, in place of `CheckpointAbort`. See IC-3. |
| Seal window | A session's effective `maxWorkspaceSealDurationSeconds`: its runtime revision's value when set, and otherwise its pool's, 300 seconds by default and at most the chart value `gateway.workspaceSealMaxDurationCeilingSeconds`, 600 seconds by default. The seal checkpoint and the session's exports still running share it ([MD-18](message-delivery-design.md#md-18-files), A19). See IC-5, "Seal". |
| Best-effort capture | A capture that runs while the runtime keeps running. It tolerates files changing and tags the snapshot `consistency: best-effort`. A quiesced capture tags it `consistency: consistent`. See IC-1, "Best-effort capture". |
| Torn file | A file that kept changing through every re-read of a best-effort capture. It is stored as last read and flagged `torn` in the manifest. |
| Compaction | Writing the live chunks of a sparsely used pack into new packs, so that the old pack loses its last reference. See IC-6. |
| Checkpoint policy | `checkpointPolicy` on a Runtime or a pool: `full`, `sealOnly`, or `none`. The effective policy is the stricter of the two values. See IC-8. |
| Effective interval | The checkpoint interval a session runs on, resolved from the pool, the Runtime, and the gateway default and clamped to the deployer's bounds. See IC-8, "Interval". |

The levers in "Levers" are grouped by who sets them. The deployer installs the chart and owns the nodes. The operator defines pools and grants them to tenants. The runtime author writes the Runtime definition and the runtime. The tenant chooses among the runtimes and pools it has access to and can derive its own runtimes.

### Terms from other designs and the platform

This design builds on mechanisms the specification, the platform, and other designs define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Checkpoint | A save of a session's workspace to the object store, from which the session resumes on another pod after a pod failure, an eviction, or a drain ([§4.4 Event / Checkpoint Store](../spec/04_system-components.md#44-event--checkpoint-store)). The gateway starts checkpoints on a periodic schedule and before a scale-down, a drain, a CheckpointBarrier, and an eviction. |
| Tar chunk and partial manifest | Under the current method, a tar chunk is one fixed-size slice of the tar stream, uploaded as its own object. A partial manifest is the Postgres row of an attempt that did not finish. It records the session, the attempt's `checkpoint_id`, the object key prefix and size of its tar chunks, and the number of chunks and bytes uploaded, so that a resume can reassemble the tar prefix ([§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain), **Partial manifest on checkpoint timeout**). |
| Workspace and slot | A slot is a session's directory tree in the pod, `/workspace/slots/{sessionId}/`, and the session's workspace is its `current/` directory. A pod that serves several sessions holds one slot for each. `/sessions/{sessionId}` is a separate per-session tmpfs that a checkpoint also captures. |
| Gateway | Lenny's API gateway. Its checkpointer schedules checkpoint attempts (`SweepDue` runs the attempts that are due), and the gateway signs every object-store grant and writes the checkpoint records in Postgres. |
| Runtime and Runtime definition | The runtime is the agent program a pod runs. The Runtime definition ([§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime)) is the platform object that describes it. A derived runtime starts from a base Runtime. |
| Pool and warm pod | A pool is a deployment target of warm pods for one runtime and one operational profile ([§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A warm pod is a pod the pool starts before any session needs it ([§6 Warm Pod Model](../spec/06_warm-pod-model.md#6-warm-pod-model)). The pool's configuration holds most of the settings this design adds. |
| Resource class | A pool dimension, `small`, `medium`, or `large`, that sets the resource requests of the pool's pods, including the `ephemeral-storage` request (§5.2). |
| Pod controller | The WarmPoolController, which creates a pool's warm pods and renders their pod specs ([§4.6.1 Warm Pool Controller (Pod Lifecycle)](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle)). The rendering code is in `pkg/controller/sandbox/podspec`. |
| Claim and bind | A claim reserves a warm pod for a session, and bind attaches the session to that pod and prepares its workspace. A derived session restores its starting snapshot at its first bind. |
| Adapter | `lenny-adapter`, the container in each agent pod that bridges the gateway and the runtime ([§4.7 Runtime Adapter](../spec/04_system-components.md#47-runtime-adapter)). It runs the checkpoint inside the pod. The runtime runs in the agent container beside it. |
| Integration level | How much of Lenny's protocol a runtime implements ([§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels)): Basic, Standard, or Full. A Full-level runtime opens `CH-RUNTIMEOPS` and can pause itself for a checkpoint. |
| `CH-RUNTIMEOPS` and `lifecycle_support` | `CH-RUNTIMEOPS` is the intra-pod channel on which the adapter and a Full-level runtime exchange operational messages ([§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod)). `lifecycle_support` is the list of operations the runtime declares on it, and `"checkpoint"` in that list means the runtime takes part in the quiesce handshake. |
| Quiesce handshake | The adapter sends `checkpoint_request` with `deadlineMs`, the runtime stops writing and answers `checkpoint_ready`, and the adapter sends `checkpoint_complete` when the runtime may continue. A runtime resumes on its own 60 seconds after `checkpoint_ready`. |
| Interrupt | A request to stop a session's current work. The adapter sends `interrupt_request` with `deadlineMs` on `CH-RUNTIMEOPS`, and the runtime answers `interrupt_acknowledged` once it reaches a safe stop point. If no acknowledgement arrives within `deadlineMs`, the adapter moves the session to `suspended` anyway and reports `INTERRUPT_TIMEOUT` to the gateway ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). The adapter's operation lock keeps an interrupt and a checkpoint from running at the same time. |
| `Checkpoint` stream | The gRPC stream in `schemas/lenny-adapter.proto` on which the gateway drives a checkpoint in the adapter and grants each upload. [§28](../spec/28_communication-channels.md#28-communication-channels) registers it as the channel `CH-CHECKPOINT`. |
| Operation lock | The adapter's pod-wide lock (`s.ops.Begin` in `pkg/adapter/oplock.go`) that serializes a checkpoint with an interrupt and other pod operations. |
| Object-store grant | The pod holds no object-store credential. For each object the pod may write or read, the gateway signs a presigned URL that names one method and one key and expires after `checkpointCapabilityTTLSeconds`, 30 seconds by default ([§13.2.3 Checkpoint Capability Model](../spec/13_security-model.md#1323-checkpoint-capability-model)). This design calls such a URL a grant, and §13.2.3 calls it a capability. The gateway's `checkpointGrantWindow`, 4 by default, caps the upload grants outstanding at once ([§10.1](../spec/10_gateway-internals.md#101-horizontal-scaling)), and a pool can override it with `SandboxTemplate.checkpointGrantWindow`, which this design caps at 8. Under this design it counts a session's grants across its `Checkpoint` and `ExportOutputFile` streams, on the session's coordinating replica (IC-3). |
| Output file | A file the agent produces for the user, which a `file` output part names by its workspace path. The adapter copies it to the artifact store on the `ExportOutputFile` stream with this design's chunks, packs, and grants, and the gateway offers it as a download ([`message-delivery-design.md`, "MD-18 Files"](message-delivery-design.md#md-18-files)). An `output_files` row records it. |
| Export and `exportTimeoutSeconds` | An export is the copy of one output file over the `ExportOutputFile` stream. `exportTimeoutSeconds` is the pool setting, 900 seconds by default, after which the gateway fails an export that has not reached `ExportReady`. |
| `CH-OUTPUTFILE` | The channel that carries the `ExportOutputFile` stream on the gateway's gRPC link to the adapter, beside `CH-CHECKPOINT`, which [`message-delivery-design.md`, "MD-18 Files"](message-delivery-design.md#md-18-files) names under the specification's naming law. |
| Coordinating replica | The gateway replica that holds a session's coordination lease and drives its streams to the pod. A coordinator handoff moves the lease to another replica ([§10.1](../spec/10_gateway-internals.md#101-horizontal-scaling)). |
| Coordinator fence | Step 2 of the coordinator handoff protocol of [§10.1.2](../spec/10_gateway-internals.md#1012-coordinator-handoff-protocol): the new coordinator sends `CoordinatorFence` with its generation, and the pod accepts the previous coordinator's RPCs until it acknowledges the fence and refuses them afterwards. |
| Quota reservation and `Rehydrate` | Under [§11.2 Budgets and Quotas](../spec/11_policy-and-controls.md#112-budgets-and-quotas), a reservation is an atomic increment of the tenant's storage counter in Redis ahead of a write, released once by a guarded update of `reservation_released_at` (**Reservation release is exactly-once and guarded.**). `Rehydrate` rebuilds the counter from Postgres and adds every reservation not yet released (**The relative decrement is rebuild-safe.**). |
| Tombstone retention | A soft-deleted catalog row stays in Postgres for `gc.tombstoneRetentionSeconds`, 24 hours by default, before the hard-prune sweep of [§12.5](../spec/12_storage-architecture.md#125-artifact-store) removes it. |
| Isolation profile | A pool's `isolationProfile`, which selects a Kubernetes RuntimeClass ([§5.3 Isolation Profiles](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles)): `standard` runs on runc, `sandboxed` on gVisor, and `microvm` on Kata Containers. `sandboxed` is the default. |
| runc | The default container runtime, which runs containers on the node's kernel and isolates them with namespaces and cgroups. |
| gVisor | A container runtime (`runsc`) whose user-space kernel, the sentry, handles the sandbox's system calls. By default each container reaches a volume through its own gofer mount, which appears as `9p` in the pod. Systrap and KVM are the platforms gVisor can use to intercept system calls, and directfs lets the sentry open host files directly. |
| Kata Containers | A container runtime that runs each pod in a lightweight virtual machine with its own guest kernel. By default the pod reaches its volumes through the shared file system virtio-fs, and `emptydir_mode = "block-plain"` in Kata's `configuration.toml` places each `emptyDir` on a block device instead. |
| `emptyDir` and `sizeLimit` | A Kubernetes volume backed by a directory on the node for the pod's lifetime. The kubelet evicts the pod when the volume's usage exceeds its `sizeLimit`. |
| inotify | The Linux interface that reports file system events, such as a create or a write, on watched directories. Each watch counts against per-user kernel limits, and a full event queue reports `IN_Q_OVERFLOW`. |
| Resume | Restoring a session onto a new pod from its latest checkpoint ([§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume)). |
| Derive and plan | A derive starts a new session from an existing one. A derive request with `from: latest` starts from the source's snapshot, and one with `from: start` rebuilds the workspace from the source's plan ([`session-derive-design.md`, "What each mode starts from"](session-derive-design.md#what-each-mode-starts-from)). The plan is the session's `WorkspacePlan`, the declarative description of how its workspace is prepared, such as the files to upload and the git repositories to clone ([§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema)). A `gitClone` source in a plan clones a git repository over HTTPS at a branch, tag, or commit. |
| Minimal-state record | The Postgres record from which a session resumes its conversation without its workspace when no checkpoint can be used, with `resumeMode: conversation_only` and `workspaceLost: true` ([§4.4.5](../spec/04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback)). |
| preStop drain and tiered drain cap | Before a pod ends, its preStop hook drains it ([§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain)). An agent pod's hook takes an eviction checkpoint of its sessions, and a gateway pod's hook waits for the checkpoints in progress of the sessions it coordinates. Each wait is capped by a tier table keyed on the session's last measured workspace size: 30 seconds up to 100 MB, 60 seconds up to 300 MB, and 90 seconds up to the 512 MB limit. |
| CheckpointBarrier | During a rolling update of the gateway, the CheckpointBarrier protocol holds tool-call dispatch while sessions checkpoint, and the gateway waits up to `checkpointBarrierAckTimeoutSeconds` for each pod's `CheckpointBarrierAck` ([§10.1.8](../spec/10_gateway-internals.md#1018-rolling-updates-and-the-checkpointbarrier-protocol)). |
| Pool-config admission webhook and its floors | `lenny-pool-config-validator`, the admission webhook that checks every pool configuration write ([§10.1.7](../spec/10_gateway-internals.md#1017-prestop-hook-drain)). Its agent-pod grace floor rejects a pool whose `terminationGracePeriodSeconds` is below `maxConcurrentSessions × max_tiered_checkpoint_cap + 30`, where `max_tiered_checkpoint_cap` is the largest drain cap the pool's `workspaceSizeLimitBytes` allows and 30 seconds is the stream-drain budget. Its BarrierAck floor rejects a pool whose `checkpointBarrierAckTimeoutSeconds` is below `max_tiered_checkpoint_cap`. |
| Legal hold and erasure | A legal hold keeps a session's stored data from deletion and rotation. `DeleteByUser` erases a user's data ([§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces)). |
| Security hold, escrow, and `EnsureCopied` | A security hold is the platform-only hold of [`forensics-evidence-preservation-design.md`, "FE-3"](forensics-evidence-preservation-design.md#fe-3-platform-only-holds-and-a-security-retention-class), which the tenant under investigation can neither see nor release. It copies held checkpoints to escrow, the per-region bucket that §12.8 uses for legal-hold copies. `EnsureCopied` is FE-3's call that every path deleting held material makes first, so that the hold's copy exists before the original goes. |
| Sidecar and embedded models | In the sidecar model, the default, the adapter runs in its own container. In the embedded model, a first-party binary embeds the adapter, so the adapter and the agent are one process in one container ([§4.7.10 Deployment Model](../spec/04_system-components.md#4710-deployment-model)). |
| Embedded `SIGSTOP` checkpoint path | The path of [§4.4.3](../spec/04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling) **Embedded adapter mode only:** under which an embedded adapter stops the agent with `SIGSTOP` for a checkpoint and resumes it with `SIGCONT`. Its watchdog sets the `checkpointStuck` flag, and **Embedded adapter liveness integration:** fails the `/healthz` liveness endpoint while the flag is set. IC-9 removes all of it. |
| Revision, versioned field, and live field | A revision is an immutable, numbered copy of a Runtime's versioned fields. A change to a versioned field creates a revision, and a session reads those fields from the revision chosen for it. A change to a live field applies to the next session on every revision and creates no revision ([`runtime-revisions-design.md`, "RV-1 Revisions"](runtime-revisions-design.md#rv-1-revisions)). |
| Storage reservation | The `Reserve` charge that [`session-derive-design.md`](session-derive-design.md#what-it-refuses) takes on the tenant's storage counter ahead of a derive's first copy, which `undoDerivedWrites` releases when a later step fails. |
| Session envelope | The session object the API returns to a client. |
| `.lennyignore` | A file in a workspace that lists paths to leave out, in the format of `.gitignore`. The CLI honors it when it uploads a workspace. |
| `final_workspace_ref` | The reference to a session's final workspace snapshot, recorded at session end. |
| F-n.n.n | A finding filed in `BUILD-GAPS.md` at the repository root. |

### Benchmark terms

"Benchmarks" names its scenarios and configurations with these terms. Its "Setup" states them in full.

| Term | Meaning |
| :-- | :-- |
| Steady | A checkpoint after 50 files changed. |
| After checkout | A checkpoint after `git checkout HEAD~2000`, on the repositories with full history. |
| Package install | A checkpoint that starts 3 seconds after a writer begins creating 150,000 files of 4 KiB at 10,000 files per second. |
| First checkpoint | A session's first checkpoint, whose pre-copy reads and uploads the whole workspace. |
| Configuration | One row of a benchmark table: an isolation profile and the variant of it measured. "gVisor" is the systrap platform with directfs. "gVisor exclusive" adds `--file-access-mounts=exclusive`, and "gVisor KVM" adds `--platform=kvm`. "Pod-share mount hint" is the gVisor annotations that make the workspace one mount shared by the pod's containers (IC-1, "Change tracker"). "Kata" is the default virtio-fs cache mode, `auto`, and "Kata metadata" and "Kata always" set `virtio_fs_cache` to those values. "Kata block" places the workspace on a block device, as `emptydir_mode = "block-plain"` does. |
| Metal, 2 CPUs | "Metal" marks a run on the bare-metal `m7i.metal-24xl`. The other rows of the pause and restore tables ran on `m8i.2xlarge` with nested virtualization enabled, so Kata and gVisor KVM ran a virtual machine inside the EC2 virtual machine. "2 CPUs" marks a pod limited to 2 CPUs and 4 GiB instead of 4 CPUs and 8 GiB. |
| gp3 baseline | An EC2 gp3 volume at its included performance of 3,000 IOPS (input/output operations per second) and 125 MiB/s. |
| Verification walk | A walk after each capture, while the workload was still stopped, that counted the changes the capture missed. |

### Labels from other designs

This document cites the following labels from other designs.

| Label | Design | What it names |
| :-- | :-- | :-- |
| EG-2 and EG-3 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | The egress design's proposals for the pod network agent and proxied egress (EG-2), which also renders the workspace volume's `sizeLimit`, and for the pod git path, which runs git inside the agent pod (EG-3). |
| FE-1 and FE-3 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) | [FE-1](forensics-evidence-preservation-design.md#fe-1-quarantine) quarantine of an agent pod, and [FE-3](forensics-evidence-preservation-design.md#fe-3-platform-only-holds-and-a-security-retention-class) platform-only security holds. |
| MD-3, MD-18, and A12 to A21 | [`message-delivery-design.md`](message-delivery-design.md) | [MD-3](message-delivery-design.md#md-3-inbox-states-and-custody) inbox states and the consumed message-id set a checkpoint records, [MD-18](message-delivery-design.md#md-18-files) how a file the agent produces for the user is frozen, copied to the artifact store, offered as a download, and kept for the session's retention, [A12](message-delivery-design.md#decisions) the owner's decision of 2026-11-10 that such a file is copied with this design's chunks, packs, and grants and shares the session's grant window with its checkpoints, A13 to A17, the owner's decisions of the same day on the per-pool grant window and its maximum of 8, the restated S5 bound, the export lifecycle, holds over output files, and the pause of export work during a capture pass, and A18 to A21, the owner's decisions of the same day on the pack nonce, the seal window that the seal checkpoint and the exports share, the whole-file SHA-256 as published metadata, and the files a reflink pool does not clone. |
| RV-1 and RV-2 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | [RV-1](runtime-revisions-design.md#rv-1-revisions) the revision record and its field classes, and [RV-2](runtime-revisions-design.md#rv-2-selection) the choice of a revision for each session. |
## High-level design

When a session's pod fails or is drained, Lenny resumes the session on another pod from its last checkpoint, a saved copy of the session's files in object storage. Today each checkpoint packs the whole workspace into one archive and uploads it, and an agent that cooperates with checkpoints stays paused until the upload ends. A workspace holding a large repository therefore pauses the agent for minutes, fails the 60-second checkpoint timeout every time, and uploads every byte again on each attempt.

This design uploads only what changed, and it pauses the agent only while it collects the last few changes. While the agent runs, Lenny copies the files that changed since the last checkpoint. It then pauses the agent, takes the files that changed during that copy, and resumes the agent before uploading them. Files are stored as pieces named by a hash of their content, so content already stored is not uploaded again and a failed attempt's uploads are reused. For a checkout of the Chromium repository, 516,000 files in 5.9 GiB, the measured pause fell from 225 to 625 seconds to between 1.1 and 8.6 seconds when Lenny finds changes by scanning the files, and to between 0.11 and 0.16 seconds when a watcher records changes as they happen. The limits are that, without the watcher, the pause grows with the number of files in the workspace, that the watcher sees the agent's writes under gVisor and Kata only with particular node configuration, and that a restore still downloads the whole workspace.

The rest of this section states each part in the design's terms, which "Terms" above defines.

```
            pre-copy pass               capture pass          upload pass
runtime:    running ------------------- | quiesced | ------------ running ------->
adapter:    read files whose identity     take the changed      pack spooled chunks,
            changed, upload new packs     set (tracker or       upload packs and
                                          walk), read or        manifest
                                          clone into spool
gateway:    grants a PUT per pack ............................. checks reference closure,
                                                                writes the snapshot row
```

**Capture in passes (IC-1).** A checkpoint runs a pre-copy pass, a capture pass, and an upload pass. Pre-copy reads every file that has no trusted entry in the file index and uploads its new chunks while the runtime runs. A Full-level runtime is quiesced only during the capture pass, in which the adapter takes the changed set from the change tracker, or from a stat walk when no healthy tracker runs, and reads or clones those files into the spool. The upload pass packs and uploads the spool after the runtime resumes. Before it asks the runtime to pause, the adapter estimates the capture time. When the estimate exceeds the pause budget, the adapter runs further pre-copy rounds and then captures on the best-effort path. A runtime that does not quiesce is always captured best-effort, and a file that changes during that capture is flagged `torn` without failing the attempt. Git lock files and temporary git packfiles are left out, and git metadata is captured before git objects.

**Storage format (IC-2).** Files are cut into content-defined chunks named by their SHA-256, compressed with zstd, and stored in packs of about 16 MiB under the writing session's prefix. Each pack carries a random nonce, so no two packs share an ID. A manifest lists every path with its chunk IDs, and Postgres holds the snapshot root.

**Checkpoint stream (IC-3).** The gateway still drives each checkpoint over the `Checkpoint` stream and grants each upload, now one pack at a time. `checkpoint_complete` now means the snapshot is captured, and the gateway runs due attempts concurrently. An `ExportOutputFile` stream beside it copies an output file of [MD-18](message-delivery-design.md#md-18-files) on the same packs and grants.

**Catalog, reference closure, and quota (IC-4).** Postgres records packs, snapshots, output files, and which snapshot or output file references which pack. The gateway accepts a snapshot only when every pack it references is in the session's allowed set, so a pod cannot read another session's content by naming a hash. The tenant is charged once for each pack.

**Restore, derive, download, and seal (IC-5).** A restore fetches the snapshot's packs, verifies every chunk, and builds the file index from the files it wrote. A derive references the source's packs and copies no object. The workspace download route still returns a gzip-compressed tar, which the gateway renders. The seal runs the same passes at session end, and the session's exports still running finish in the time the seal leaves in the seal window.

**Retention and garbage collection (IC-6).** The latest 2 snapshots per session are retained, and output files follow the session's retention. A pack is deleted when no live snapshot or output file references it and its lease has expired, sparse packs are compacted, and erasure and legal hold apply to snapshots and output files.

**Size limits and drain budgets (IC-7).** `workspaceSizeLimitBytes` bounds disk use and storage instead of the pause, an attempt gets its own timeout, and the tiered drain caps become one cap.

**Checkpoint policy, interval, and change tracker (IC-8).** A Runtime and a pool can each set `checkpointPolicy` to `full`, `sealOnly`, or `none`, and the stricter value applies. Each can set its own checkpoint interval, with the pool's value winning, within the deployer's bounds. The pool's `checkpointChangeTracker` can turn the tracker off. Under its default, `auto`, the adapter runs the tracker only on workspace file systems where the agent container's writes reach the adapter's watches.

**Removal of the embedded `SIGSTOP` path (IC-9).** The specification lets an embedded adapter stop the agent with `SIGSTOP` for a checkpoint. IC-9 removes that path, its `checkpointStuck` flag, and the liveness rule built on the flag, so an embedded binary is checkpointed by the same cooperative or best-effort rule as every other runtime.

**Levers.** "Levers" lists the settings with which deployers, operators, runtime authors, and tenants lower the pause or how often it occurs, with the measured effect and the cost of each.

## Owner's direction

On 2026-11-05 the owner wrote: "we definitely need to improve checkpointing with a re-design." The owner supplied pointers from another conversation, which "Starting points" evaluates.

On the same day the owner accepted the recommended `checkpointPolicy` values (IC-8, [decision D8](#decisions)), in the words "your recommendation makes sense", and added: "I think a runtime/pool should be able to override the default checkpoint interval if possible."

The owner then asked for measurements on AWS, run independently of the project's test infrastructure, with repositories fetched from the internet, and scoped to "how long it takes to checkpoint and transfer". For the follow-up runs the owner wrote: "Depending on the results, and if needed to find a good solution, try different combinations. If 4 helps, then it should be documented for deployers. Make sure to measure performance impacts of 5." Item 4 is the gVisor KVM platform, and item 5 is the inotify change tracker. The owner added: "Once we're done with the tests and decisions, make sure the design doc includes changes/updates to Lenny's docs, with maybe a new section for checkpointing, with actual benchmarks and all the levers operators and tenants can use to reduce the impact when needed (including things like disabling checkpointing or changing the frequency, running on runc in a dedicated pool (tenant pool), etc.)"

Asked whether the default pause budget should be 5 s or 10 s, the owner chose "10 s default" ([decision D7](#decisions)). The same day the owner asked for the Kubernetes checks in "Benchmarks": "Test --file-access-mounts=exclusive with ConfigMaps etc. Test emptydir_mode = "block-plain"'s interaction with the kubelet's emptyDir size limit."

Asked whether the platform has a way to stop a runtime that does not quiesce, such as the cgroup freezer, which suspends every process in a container's control group, the owner answered: "no, I don't want the forcing mechanism, thanks". Quiescence therefore stays cooperative. A Full-level runtime pauses itself on `checkpoint_request`, and every other runtime is captured on the best-effort path while it runs.

On 2026-11-08 the owner accepted, in the word "ok", the recommended placement of the settings this design names with defaults ([decision D14](#decisions)). The pack format and capture settings are constants that the specification states, `checkpointConcurrency` is a gateway setting from the chart, and `restoreConcurrency` is an adapter setting that the controller passes from the chart to every agent pod.

On 2026-11-09 the owner settled that the embedded `SIGSTOP` checkpoint path of §4.4.3 is removed, and that this design states the removal ([decision D15](#decisions), IC-9). [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) had proposed to amend that path for quarantined pods, and it drops the amendment.

## Requirements

- R1. The time a runtime spends quiesced depends on the number of files in the workspace and on the bytes changed since the last pre-copy pass. It does not depend on the total bytes in the workspace. The target is a P95 pause under 1 second for a workspace of 500,000 files with up to 256 MiB changed since pre-copy, on runc with node-local SSD.
- R2. A checkpoint uploads only content that the session's retained snapshots do not already hold.
- R3. Bytes that a failed or timed-out attempt uploaded are reused by the next attempt of the same session.
- R4. Node disk and the tenant's storage quota bound workspace size. The checkpoint pause no longer sets that bound.
- R5. The pod keeps the capability model of [§13.2.3 Checkpoint Capability Model](../spec/13_security-model.md#1323-checkpoint-capability-model). It holds no object-store credential and no `LIST`, `DELETE`, or multipart capability, and the gateway chooses every key it signs.
- R6. A pod can reference only content that its own session wrote or that its session started from. Naming a hash grants no read.
- R7. The design works on runc, gVisor, and Kata without node configuration, added privileges, or a particular node filesystem. A filesystem feature speeds up capture where the adapter detects it, and node configuration that a deployer applies lowers the pause further (see "Levers").
- R11. A snapshot tagged `consistency: consistent` contains every change made before the runtime acknowledged the quiesce. A change source that can lose a change without reporting the loss is not used for a consistent capture.
- R8. A file that changes during a best-effort capture costs that file's consistency. The attempt still completes.
- R9. A restore verifies every byte it writes against the content hash recorded for it.
- R10. Client contracts are unchanged: `GET /v1/sessions/{id}/workspace` still returns a gzip-compressed tar, and a derive still starts from the source's snapshot.

## Security properties

- S1. The gateway signs a `PUT` only for a pack key under the writing session's own prefix, and never for a pack key `(tenant_id, owner_session_id, pack_id)` its catalog already holds, in any state, a soft-deleted row included, so a pod cannot overwrite a pack a snapshot or an output file references or one whose deletion is still pending. The pack nonce of IC-2 makes every pack an adapter builds distinct, so an honest adapter never declares a key the catalog holds except to resend a pack it built. A `PackReady` resolves the pack ID against the session's own key first. A pack under that key that is in the session's allowed set is answered "already stored" and receives no grant, and a key held in any other state is refused (IC-3). Only when the session holds no such key does the gateway consult the allowed set's packs that another session owns, which are the packs of a derived session's starting snapshot, and only for the "already stored" answer. The check therefore reads the session's own keys and its allowed set, which the session may already read, and it reveals nothing about any other session's packs.
- S2. The gateway accepts a snapshot only when every pack it references is in the session's allowed set (see "Reference closure"). The allowed set comes from Postgres, so a pod that names a foreign pack ID fails the check.
- S3. The gateway signs a `GET` only for packs that the snapshot being restored references.
- S4. A restore recomputes the SHA-256 of every chunk and fails the file whose chunk does not match, so a corrupt or substituted pack cannot silently change restored content.
- S5. A compromised pod can write up to the pool's effective `checkpointGrantWindow` times `packMaxBytes` of chosen bytes into its own session's prefix within the grant TTL. The effective window is the number of upload grants the gateway keeps outstanding at once for one session, counted across the session's `Checkpoint` and `ExportOutputFile` streams together on the session's coordinating replica: the pool's `SandboxTemplate.checkpointGrantWindow` when set, at most 8, and otherwise the gateway's default of 4 ([§10.1](../spec/10_gateway-internals.md#101-horizontal-scaling)). `packMaxBytes` is the largest pack IC-2 allows, a constant of 32 MiB. The bound is therefore 128 MiB by default and at most 256 MiB, per session across both streams, and an output-file export does not add a window of its own ([A12 and A13](message-delivery-design.md#decisions)). Today's bound is `checkpointGrantWindow × partialChunkSizeBytes`, 4 × 16 MiB, so the pack format doubles it by default ([A14](message-delivery-design.md#decisions)). A new coordinator after a handoff cannot see the grants the previous one signed, and the pod accepts the previous coordinator's RPCs until it acknowledges the new coordinator's fence ([§10.1.2](../spec/10_gateway-internals.md#1012-coordinator-handoff-protocol), step 2). The new coordinator therefore counts the session's whole window as outstanding until `checkpointCapabilityTTLSeconds` (30 s) has passed since the pod acknowledged its fence, and the adapter cancels the session's open `Checkpoint` and `ExportOutputFile` streams of the previous generation when it acknowledges the fence, so the bound holds across the handoff. The gateway refuses at startup a `--checkpoint-grant-window` above 8, so the gateway's default window cannot exceed the bound either. The bound holds because the gateway refuses a grant for a pack whose declared length exceeds `packMaxBytes`, so the size a grant can carry is a gateway constant rather than a pod's report. The bound limits a burst of chosen bytes in the session's own prefix, which the tenant's quota pays for and garbage collection reclaims once no snapshot or output file references the pack.

R5 and S1 to S4 apply to the packs that the `ExportOutputFile` stream writes for output files (IC-3). Those packs use the same key prefix, `/{tenant_id}/checkpoints/{session_id}/packs/{pack_id}`, and the same `checkpoint_packs` rows as a checkpoint's packs. S1's refusal covers their grants, S2's check runs on each output file's pack references, and S4's verification runs on each download of an output file (IC-5).

## Current state

- `ArchiveTree` (`pkg/adapter/workspace/tree.go`) walks the workspace and the `/sessions` tree, writes a tar, and compresses it with `compress/gzip`. The handler slices the stream into fixed-size tar chunks and uploads them one at a time against per-chunk presigned `PUT` grants (`streamChunks` and `uploadChunk` in `pkg/adapter/checkpoint.go`).
- `ExtractTree` restores the stream and stops at a fixed 2 GiB (`maxExtractBytes` in `pkg/adapter/workspace/extract.go`).
- Derive copies every tar chunk object of the source checkpoint into the derived session's prefix ([§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow) **Derive copy semantics:**), and [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store) **Workspace deduplication.** states that a future content-addressed format must add reference counting to garbage collection and erasure.
- Retention keeps the latest 2 checkpoints per session (`pkg/gateway/checkpoint/checkpointretention`), with legal-hold exemptions.
- An attempt whose deadline fires leaves a partial manifest, a Postgres row recording the tar chunks it uploaded, which the resume path reassembles into a tar prefix ([§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain) **Reassembly on resume.**).
- The embedded `SIGSTOP` checkpoint path in `pkg/adapter/embeddedcheckpoint`, which stops the runtime process with `SIGSTOP` for a checkpoint and resumes it with `SIGCONT`, has no caller outside its own package and tests. §4.4.3 specifies it, and `pkg/checkpoint` maps its `embedded` checkpoint level to `consistent` on the strength of it. IC-9 removes the path.

## Starting points

The owner's pointers propose a copy-on-write snapshot, followed by an incremental, deduplicated archive of that snapshot. The table records what each pointer contributes here.

| Pointer | Fit for Lenny | Outcome |
| :-- | :-- | :-- |
| An atomic filesystem snapshot on ZFS, Btrfs, or thin volumes of LVM (the Linux Logical Volume Manager), taken before archiving | Requires a chosen node filesystem and privileged operations. Agent pods drop all Linux capabilities ([§13.1 Pod Security](../spec/13_security-model.md#131-pod-security)), and the workspace is an `emptyDir` directory rather than a dataset or subvolume. | Deferred as a node-level provider (see "Node-level snapshots"). |
| A reflink copy with `cp --reflink=always` | `FICLONE` needs no privilege on XFS with `reflink=1` and on Btrfs. It cloned a 2 GiB file in 1.1 ms, and it was slower than a plain copy for 4 KiB files. On AWS it worked under runc on XFS and returned `EOPNOTSUPP` under gVisor and Kata (see "Benchmarks"). | Adopted for large changed files during the capture pass, when the reflink probe at adapter start finds it available. In practice that is runc only. |
| restic, BorgBackup, or Kopia for incremental, deduplicated archives | Each tool needs direct repository access with listing and deletion, plus repository locks and pruning. That conflicts with R5. Borg also needs Python and an SSH or local repository. | The techniques are adopted: content-defined chunking, deduplication, packs, and comparison against the parent snapshot's file metadata. The tools are not adopted. |
| Plain tar is the wrong tool at this frequency | Agreed. | Replaced by packs and a manifest. |
| An atomic snapshot keeps `.git` consistent, and `git bundle` or `git clone --mirror` otherwise | The quiesced capture pass is point-in-time for Full-level runtimes. A bundle needs git in the adapter image and re-reads history on each run. | Ordered capture of git metadata on the best-effort path (see "Git repositories"). Bundles are not adopted. |
| Timer and event triggers with a debounce | The gateway's periodic schedule and its existing triggers stay. | A background pre-copy between checkpoints is a later optimization. |
| restic or Kopia on the live folder works, but the result is not point-in-time | This is the pre-copy pass. | The capture pass closes the gap for Full-level runtimes. |

## IC-1: Capture in passes

A checkpoint runs a pre-copy pass, a capture pass, and an upload pass. The runtime is quiesced only during the capture pass.

### Pre-copy

The adapter walks `/workspace/slots/{sessionId}/current/` while the runtime runs. For each regular file it reads the file's identity with `lstat`: device, inode, size, `mtime` in nanoseconds, `ctime` in nanoseconds, and mode. It compares that identity with the adapter's file index from the session's previous snapshot. When the change tracker is healthy, the pre-copy walk also cross-checks it: a file whose identity changed and that the tracker did not report marks the tracker degraded (see "Change tracker").

- A file whose identity matches a trusted index entry keeps the entry's chunk list without a read.
- Every other file is read, split into chunks, and hashed. A chunk whose ID the dedup set holds is referenced. A new chunk is compressed and appended to the open pack, and a full pack is uploaded.
- The adapter records each file's identity from before the read and checks it with `fstat` after the read. A file whose identity changed during the read is marked dirty so that the capture pass reads it again.

Pre-copy reads files with a bounded worker pool and keeps several pack uploads in flight. It holds no operation lock, so interrupts proceed. Packs it confirms stay usable when the attempt later fails (R3).

### Capture

On a Full-level runtime the capture pass runs under the cooperative handshake on `CH-RUNTIMEOPS`.

1. The adapter takes the pod-wide operation lock and sends `checkpoint_request` with `deadlineMs` set to the pause budget.
2. After `checkpoint_ready`, the adapter builds the changed set: files whose identity differs from the index, dirty files, and racy entries (see "Change detection"). With a healthy change tracker, the adapter writes a barrier file, waits until the tracker has read the barrier's event, and takes the tracker's dirty paths, then reads the identity of those paths only. Without a healthy tracker, the adapter walks the workspace again with a parallel stat-only walk, which also records directories, symlinks, and modes.
3. The adapter decided before step 1 whether the capture fits the pause budget, from an estimate built during pre-copy (see "Pause budget and convergence"). If the changed set found in step 2 exceeds that estimate by more than the budget allows, the adapter ends the capture without reading.
4. The adapter captures each changed file. A file of at least `reflinkMinBytes`, a constant of 1 MiB, is cloned into the spool with `FICLONE` when the spool passed the reflink probe. Every other changed file is read, chunked, and hashed, and its new chunks are written to the spool uncompressed. The adapter copies `/sessions/{sessionId}`, which is a tmpfs capped at 256 MiB, in full.
5. The adapter sends `checkpoint_complete` with status `ok` and releases the lock. The snapshot's file list is complete at this point, and only the spooled bytes remain to upload.

The pause is the handshake, plus the tracker swap or the stat walk, plus the time to read or clone the changed files from local disk. Recently written files are usually in the page cache. On the measured nodes the stat walk costs about 2 µs per file under runc and on a Kata block volume, 3 to 5 µs on Kata's shared file system with the metadata cache, and 11 to 27 µs under gVisor and under Kata's default cache mode. Reading a changed 4 KiB file costs 5 to 13 µs under runc and a Kata block volume and 46 to 129 µs under gVisor and Kata's shared file system (see "Benchmarks").

The spool is `/workspace/staging/checkpoint/`, on the same volume as the slots, which `FICLONE` requires. The adapter removes spooled data once its pack is confirmed.

### Upload

After the runtime resumes, the adapter chunks any cloned files, compresses the spooled chunks into packs, uploads them, uploads the manifest, and asks the gateway to finalize the snapshot. A failure in this pass fails the checkpoint without affecting the runtime, and the packs already confirmed stay usable by the next attempt.

### Pause budget and convergence

The capture pass is bounded by the pool's `checkpointPauseBudgetSeconds` (default 10), which is a time. A byte budget alone is not enough: on Kata's shared file system with a package install running, a changed set of 40 MiB in 10,000 files took 1.8 s to read, while a single changed 400 MiB file took 0.03 s to clone under runc. The adapter therefore estimates the capture time before it sends `checkpoint_request`:

```
estimate = change-source cost + changed files × per-file read cost + changed bytes ÷ read rate
```

- The change-source cost is the duration of the last stat walk, or the tracker's swap time plus its event backlog multiplied by its measured cost per event. The backlog is the byte count pending on the inotify descriptor, which `FIONREAD` reports, divided by the event size.
- The per-file cost and the read rate are measured on the pod during the preceding pre-copy pass, so they reflect the isolation profile and the disk.
- The changed set is the set the pre-copy pass marked dirty, plus files the tracker reported after the pass.
- Bytes that the capture will clone with `FICLONE` count at the measured clone cost instead of the read rate.

The pool's `checkpointCaptureBudgetBytes` (default 512 MiB) stays as the spool cap. Pool admission rejects a `checkpointPauseBudgetSeconds` of 60 or more, because a Full-level runtime resumes on its own 60 seconds after `checkpoint_ready` and the capture would then no longer be point-in-time. When the estimate is over the pause budget, the adapter skips the handshake for this round, runs another pre-copy round, and estimates again, as live migration of a virtual machine does, up to the pool's `checkpointPreCopyRounds` (default 3) rounds. When the changed set found during the capture exceeds the estimate by more than the remaining budget, the adapter ends the capture with `checkpoint_complete` status `failed` and reason `precopy_required` and takes another round. Another round reduces only the changed set. When the change-source cost alone, which is the stat walk without a tracker, exceeds the budget, further rounds cannot bring the estimate under it, so the adapter skips them and captures on the best-effort path at once. A workspace of about 600,000 files under gVisor without a tracker is that case, because its walk alone takes more than 10 s whatever changed. Chromium's 516,000 files walked in 8.6 s, which fits the 10 s default and leaves 1.4 s for changed files.

The benchmark harness decided with a byte estimate, which is the defect this formula corrects. Its timings bear out the terms of the formula. An unpaused stat walk predicted the paused walk that followed it with a median ratio of 1.13 and a worst case of 2.66 while files were being created. Under the tracker, the barrier wait took 0.2 to 0.4 s on runc and 3.5 to 6.1 s on gVisor during sustained file creation, because the tracker had a backlog of unread events at the pause (see "Benchmarks"). Reading the tracker's dirty set without a swap does not see that backlog, so the estimate takes it from the tracker's queue depth.

If the last round is still over budget, the adapter captures without quiescing, as on the best-effort path, and tags the snapshot `consistency: best-effort`. The session gets a usable checkpoint, and `lenny_checkpoint_consistency_downgrade_total` counts the event. When the pause budget expires during a capture, the adapter ends the capture, the runtime resumes, and the attempt continues on the best-effort path.

### Best-effort capture

On a Basic or Standard runtime the adapter runs the same passes without the handshake and without the operation lock. The capture pass tolerates change instead of failing:

- A file whose identity changes during its read is read again, up to `bestEffortRereads` times, a constant of 2. A file still changing after that is stored as last read, and the manifest flags it `torn`.
- A file or directory that disappears between the walk and the read is left out of the snapshot.
- The attempt never fails because a file changed.
- New chunks stream into the packer as they are read. No pause holds the workspace still on this path, so nothing has to be held in memory or in the spool. The first harness version buffered the chunks of a best-effort capture in memory and was killed when a 4 GiB database file changed.

The snapshot is tagged `consistency: best-effort` and records the count of torn files.

On the measured nodes, under the harness's 512 MiB byte budget, a 4 GiB file under constant writes was over budget in every round on gVisor and Kata, where `FICLONE` is unavailable, and the attempt ended on this path with no pause and the file flagged `torn`. Under runc the same file was cloned during the pause in 2.0 to 2.5 s, and the snapshot stayed consistent.

### Git repositories

The adapter treats a directory that contains a `.git` directory, or a `.git` file that names a git directory, as a repository.

- **Lock files.** Files named `*.lock` under a git directory are left out of every snapshot. A restored lock file blocks the next git command that needs the lock, and git treats a missing lock file as unlocked.
- **Temporary packfiles.** Files named `tmp_pack_*`, `tmp_idx_*`, and `tmp_rev_*` under `objects/pack/` are left out of every snapshot. Git writes them during a fetch or a repack and renames them into place when they are complete, so a captured copy is always incomplete. On LLVM a running `git repack -a -d` held a 374 to 411 MiB temporary git packfile. With the exclusion the pause was 0.03 to 0.07 s on every runtime. Without it, the capture on most gVisor and Kata variants stayed over budget and fell to best effort.
- **Order on the best-effort path.** The adapter captures `HEAD`, `refs/`, `packed-refs`, `index`, and the remaining metadata before `objects/`. Git writes objects before it updates the refs that point to them, so a ref captured earlier names objects that already existed when the adapter read `objects/`. Git removes objects only through gc, prune, or repack. When a git packfile listed earlier disappears during the read, the adapter lists `objects/pack/` once more.
- **Repacks.** `git gc` and `git repack` write new packfiles whose bytes differ from the old ones, so a repack causes the history to upload again. [Decision D6](#decisions) proposes disabling automatic maintenance in workspace repositories. The setting belongs in the git configuration that [`egress-and-git-design.md`, "Git configuration"](egress-and-git-design.md#git-configuration) injects, which does not carry it today, so D6 is a request to that design (see "Changes requested of other designs"). Until that design adopts it, automatic maintenance stays on and a repack uploads the history again.
- **Alternates.** Objects reached through `objects/info/alternates` outside the slot are not captured. The warm repository stores of [`tenant-derived-runtimes-design.md`, "Warm clones"](tenant-derived-runtimes-design.md#warm-clones) are such objects. A pod fills them outside the slot for the entries on its pool's warm list, which are an operator's entries, a tenant's warm default clones, and a tenant's cache-only entries, and a store backs any `gitClone` source in the plan whose URL matches it, a client's source included, unless the source sets `depth` or `submodules: true`. Restore relies on that design's restore completion, the step that reconnects a restored repository to the pod's store, to reconnect them.

### Change detection

An index entry is trusted when its identity matches the file's current identity and the entry is not racy. An entry is racy when the file's `mtime` falls within `racyWindow`, a constant of 2 s, before the instant the read that produced the entry started. This is git's rule for racily clean index entries. A write within the timestamp granularity of an earlier read leaves `mtime` unchanged, so a racy entry is re-read during the capture pass. `ctime` detects mode changes and renames onto the path, and the inode detects a replacement by rename.

### Change tracker

The stat walk is the largest part of the pause on gVisor and Kata, and its cost grows with the number of files in the workspace. The change tracker removes the walk from the pause by recording which paths changed since the last capture.

- **Mechanism.** The adapter runs one tracker per session slot. The tracker adds an inotify watch on every directory under the slot with the mask `IN_MODIFY | IN_ATTRIB | IN_CLOSE_WRITE | IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO | IN_DELETE_SELF | IN_MOVE_SELF`, and it keeps a set of dirty file paths and dirty directory paths. A directory created later gets a watch when its `IN_CREATE` event arrives, and the tracker then lists it once, so files created before the watch existed still enter the dirty set.
- **Swap.** At the capture, the adapter writes a barrier file in the slot root and asks the tracker to swap. The tracker reads events until it sees the barrier file's event, returns the dirty set, and starts an empty one. Every event queued before the barrier file is therefore in the returned set.
- **Fallback.** The capture walks in place of the swap when the tracker reports an `IN_Q_OVERFLOW` since the last swap, when an `inotify_add_watch` call has failed, when the tracker started after the previous snapshot, or when the pre-copy cross-check found a change the tracker did not report. A failed watch and a failed cross-check mark the tracker degraded until the adapter restarts it, because a directory without a watch stays unwatched. An overflow clears at the next walk. `lenny_checkpoint_tracker_fallback_total` counts each fallback by reason.
- **Visibility across containers.** The tracker runs in the adapter container, and the runtime writes from the agent container. The adapter sees those writes only when both containers reach the workspace through one mount inside the kernel or sentry that delivers the events. In a single-node Kubernetes cluster on 2026-11-06, 200 files created, 1 file appended to, and 1 file renamed from a second container of the pod produced 202 dirty paths under runc, under gVisor with the pod-share mount hint described below, and on a Kata block volume. They produced none under gVisor's default mount and none on Kata's shared file system. The adapter therefore decides from the workspace mount's file system type, which it reads from `/proc/self/mountinfo`, whether a tracker can run (see "Change tracker setting").
- **Limits.** The tracker needs one watch per directory. The measured workspaces needed 46,969 watches for Chromium and 17,153 for LLVM. A slot whose directory count exceeds `checkpointTrackerMaxWatches` (default 262,144) runs without a tracker.
- **runc.** Under runc the watches count against the node kernel's per-user limits, `fs.inotify.max_user_watches` and `fs.inotify.max_user_instances`, and every process on the node that runs as the same host user ID (UID) shares them. Every adapter on a node runs as the same UID, so all trackers on the node share one budget. The kernel sizes `max_user_watches` from memory, and the measured 32 GiB nodes allowed 255,070 watches, which five Chromium-sized workspaces exhaust. `max_user_instances` defaults to 128, and each tracker uses one instance. A tracker whose watch fails falls back to the walk, so exhaustion costs pause length rather than correctness. Deployers raise both limits with a sysctl on runc nodes (see "Levers"). Whether Kubernetes user namespaces (`hostUsers: false`) give each pod its own budget was not tested. Under gVisor and Kata the limits belong to the sandbox or the guest kernel, so pods on a node do not share them.
- **gVisor.** Under gVisor's default mount, each container of the pod reaches the `emptyDir` through its own gofer mount, and the sentry delivers an inotify event only to watches on the mount the change was made through. A tracker in the adapter container therefore sees none of the agent container's writes. The pod annotations `dev.gvisor.spec.mount.<volume>.share: pod`, `.type: bind`, `.options: rw`, and `.source: <volume>` make the volume one mount owned by the sentry and shared by the pod's containers. The sentry then keeps the volume's content in a single file, `.gvisor.filestore.<id>`, in the `emptyDir` directory on the node, the kubelet's size accounting counts that file, and events cross containers. The containerd runtime handler for gVisor has to pass `dev.gvisor.*` pod annotations through to runsc, which is a node setting. On that mount the stat walk was 3.2 times faster than on the default mount, and Chromium restored in 56 s against 177 s, because the sentry writes one file on the node in place of 516,000 (see "Benchmarks"). The content is page cache on the node, as on the default mount: a pod with a 1 GiB memory limit wrote 3 GiB into the volume without an out-of-memory (OOM) kill.
- **gVisor descriptors.** On the default mount, the sentry holds one host file descriptor for each watched directory, in addition to its cached directory entries, and the sandbox inherits containerd's `LimitNOFILE`, which is 65,535 on a default install. In single-container runs with Chromium's watches in place, the first walk exhausted that limit. File opens inside the sandbox then failed with `EMFILE`, for every process in it, and inotify events stopped arriving without an overflow event or an error. On the pod-share mount the sentry held at most 777 host descriptors during the same benchmark, against 113,221 on the default mount, so the hint removes the hazard and no descriptor limit has to change. The tracker never runs on the default mount, because the adapter sees `9p` there.
- **Kata.** The guest kernel sizes `fs.inotify.max_user_watches` from guest memory, and a 2 GiB guest allows 15,514 watches. Chromium's watches failed with `ENOSPC`, and the tracker marked itself degraded and fell back to walks as specified. The Kata `kernel_params` setting `sysctl.fs.inotify.max_user_watches=1048576` removes the limit. On Kata's default shared file system, virtio-fs, a change made from one container produces no event in another container's watches. With `emptydir_mode = "block-plain"`, the volume is an `ext4` file system inside the guest, mounted by both containers, and events cross containers. On Kata the tracker therefore requires `block-plain`.
- **Cost.** Tracker setup took 0.2 to 1.8 s under runc, on a Kata block volume, and on Kata's shared file system with the metadata cache, 2.7 to 8.4 s on Kata's default cache mode, and 2.4 to 14.5 s under gVisor. The setup used up to 480 MiB of memory, and the tracker settled at 35 to 130 MiB. Events cost 20 to 45 µs of CPU each under runc and Kata and 30 to 60 µs under gVisor. The effect on the runtime's own file operations is in "Benchmarks".

The tracker is an optimization of the change source. Correctness rests on the fallback rules and on the racy rule, which apply to tracked paths in the same way as to walked ones.

## IC-2: Storage format

### Chunks

A file of at least 1 MiB is split with content-defined chunking (FastCDC with a gear hash), with a minimum of 256 KiB, an average of 1 MiB, and a maximum of 4 MiB. A smaller file is one chunk. A chunk's ID is the SHA-256 of its uncompressed bytes. Content-defined boundaries keep an insertion near the start of a large file from changing every later chunk.

### Packs

A pack is a sequence of compressed chunk frames followed by a footer that lists each chunk's ID, offset, compressed length, uncompressed length, and compression method. The target size is 16 MiB, which matches today's `DefaultChunkSizeBytes`. `packMaxBytes` is a constant of 32 MiB that the specification states, and no setting changes it. The gateway refuses a grant for a pack larger than `packMaxBytes` (see IC-3), because the bound in S5 depends on it. The footer also carries the pack nonce, 128 random bits that the adapter draws for every pack it builds, for checkpoints and exports alike. A pack's ID is the SHA-256 of its bytes, nonce included, so no two packs the adapter builds share a pack ID, and the gateway can refuse every key its catalog holds (S1) without blocking an upload. Deduplication works on chunks, which the nonce does not change, so a chunk already stored is referenced in the pack that holds it and never packed again to be reused ([A18](message-delivery-design.md#decisions)). Packing small chunks keeps the object count and the per-request cost down for workspaces with many small files.

### Manifests

A snapshot's manifest is a sorted stream of entries, one per path. Each entry records the type, mode, size, `mtime`, symlink target, chunk ID list, and flags such as `torn`. The format is versioned. The manifest stream is itself chunked with content-defined chunking and stored in packs, so an unchanged region of a large manifest deduplicates across snapshots.

The snapshot root lives in Postgres: the format version, the manifest's chunk list, the referenced pack IDs, the consistency tag, the parent snapshot, and counts of files, logical bytes, new bytes, and torn files.

### Object keys

A pack is stored at `/{tenant_id}/checkpoints/{session_id}/packs/{pack_id}`, where `session_id` is the session whose pod wrote the pack. The per-attempt `{checkpoint_id}` prefix is removed, because packs outlive the attempt that wrote them.

### Compression and encryption

Chunks are compressed with zstd from `github.com/klauspost/compress`, which `go.mod` already carries as an indirect dependency through `minio-go`. A chunk whose compressed size is at least 97% of its input is stored uncompressed, which skips packfiles and media. Encryption is unchanged: each `PUT` replays the signed server-side encryption headers that [§13.2.3 Checkpoint Capability Model](../spec/13_security-model.md#1323-checkpoint-capability-model) defines per backend.

## IC-3: Checkpoint stream

The `Checkpoint` stream in `schemas/lenny-adapter.proto` keeps its gateway-driven grant and confirm structure, and its messages change from tar chunks to packs.

- `CheckpointStart` adds `parent_snapshot_id`, `pack_target_bytes`, `pause_budget_ms`, `capture_budget_bytes`, and `precopy_rounds`, and the session's output-file chunk lists and leased packs, each leased pack with the chunk IDs its footer lists, so the dedup set of a pod that did not write them includes them. It drops `chunk_encoding`, which today names the tar chunks' encoding, `tar` or `tar.gz`.
- `CheckpointProbe`, the adapter's first frame, carries the size probe's result. It reports `workspace_bytes`, `file_count`, and `estimated_new_bytes`, which is the size of the files whose identity changed. The gateway reserves quota against the estimate.
- `PackReady` declares `pack_id`, `length`, and `content_md5`. The gateway answers `CheckpointAbort` without signing anything when the declared `length` is zero or larger than `packMaxBytes`, so the size a grant can carry is a gateway constant and the bound in S5 holds. It then resolves the pack ID against the session's own object key, `(tenant_id, owner_session_id, pack_id)`. When the catalog holds that key and the pack is in the session's allowed set, it answers `PackStored`, the "already stored" answer: the adapter skips that upload and references the pack, and the answer extends the lease of an `unreferenced` pack. That case is the adapter's resend of a pack it built, with the same nonce, after a `PackCommitted` the stream lost. When the catalog holds that key in any other state, a soft-deleted row included, the gateway refuses it, as S1 states, with `CheckpointAbort`. When the catalog holds no row for that key, the gateway consults the packs in the session's allowed set that another session owns, the packs of a derived session's starting snapshot, and answers `PackStored` for a match, for the "already stored" answer only. Any other pack is answered `PackGrant` with a single-key `PUT` grant that signs `Content-Length`, `Content-MD5`, and the encryption headers. The answers reveal nothing outside the session's own keys and its allowed set. Before it signs, the gateway applies §11.2's signing-time headroom check to the attempt or export the grant serves, and a refusal there is `CheckpointAbort` too. The adapter never rebuilds a pack to reuse it: a retry references the chunks of confirmed packs under lease through its dedup set, and chunks it uploads again go into new packs with new nonces, new pack IDs, and new grants.
- `PackCommitted` confirms an upload. The gateway checks the object's length with `StatObject` and inserts a pack row in state `unreferenced`, with a lease. The adapter adds the pack's chunks to its dedup set only at this confirmation, so a checkpoint and an export of the same session never reference a chunk whose pack upload is still in flight.
- `CaptureDone` reports `quiesced_ms`, `consistency`, `changed_files`, `torn_files`, and `rounds`.
- `SnapshotReady` carries the manifest chunk list, the referenced pack IDs, and the adapter's consumed message-id set in report order ([`message-delivery-design.md`, "MD-3"](message-delivery-design.md#md-3-inbox-states-and-custody)), sent in batches for large snapshots. The gateway runs the reference closure check and writes the snapshot row and its pack references in one transaction. A closure failure answers `ReferenceRefused` with the offending pack IDs, and the adapter evicts their chunks from its dedup set, uploads the chunks again, and sends `SnapshotReady` again.
- `CheckpointSummary` and `CheckpointFailed` keep their meaning. Today `CheckpointSummary` closes a successful stream with the totals the gateway checks before it finalizes, and `CheckpointFailed` ends the stream when the object store rejects a `PUT`, carrying the store's HTTP status and error code.

On `CH-RUNTIMEOPS`, `checkpoint_complete` changes meaning from "the snapshot is uploaded" to "the snapshot is captured". The runtime's 60-second autonomous resume stays as a safety net, and `deadlineMs` carries the pause budget. Today every periodic attempt sends `deadline_ms = 0`, because nothing sets the checkpointer's `Deadline` (see "Defects found while verifying").

The gateway's `SweepDue` runs due attempts concurrently, up to `checkpointConcurrency` per gateway replica, 16 by default. `checkpointConcurrency` is a gateway setting: the chart value `gateway.checkpointConcurrency` renders as the gateway's environment variable `LENNY_CHECKPOINT_CONCURRENCY`, which the gateway flag `--checkpoint-concurrency` reads, as `gateway.checkpointGrantWindow` renders as `LENNY_CHECKPOINT_GRANT_WINDOW`. Today it runs them one at a time, so one slow session delays every other session on the replica. The gateway carries grants and catalog writes, and the pack bytes travel between the pod and object storage.

### `ExportOutputFile` stream

The gateway opens an `ExportOutputFile` stream to the adapter for each output file, a `file` output part that names a workspace path, as [`message-delivery-design.md`, "MD-18 Files"](message-delivery-design.md#md-18-files) states. The stream sits beside `Checkpoint` in `schemas/lenny-adapter.proto` on its own channel, `CH-OUTPUTFILE`, which the pod fences by the session's coordination generation as it fences `CH-CHECKPOINT`. It follows the same gateway-driven grant and confirm structure, and reuses `PackReady`, `PackGrant`, `PackStored`, `PackCommitted`, and `ReferenceRefused` with the checks above. It adds these messages:

- `ExportStart`, the gateway's first frame, names the session, the part, the path, the effective limit, and `pack_target_bytes`. The gateway records the instant it sends it as `started_at` in the part's export record (IC-4, "Catalog"), and `exportTimeoutSeconds` counts from it.
- `ExportProbe`, the adapter's answer, carries the file's size and `estimated_new_bytes`, the bytes of the file's chunks that the dedup set does not hold. The gateway refuses a file over the effective limit, and it reserves quota against the estimate (IC-4, "Quota").
- `ExportReady` carries the file's chunk list with each chunk's offset and length in the file and its pack ID, the referenced pack IDs, and the SHA-256 of the whole file, which the gateway keeps as metadata (IC-5, "Download"). The gateway treats the list as untrusted. It checks that the offsets are contiguous from 0, that each chunk's length is at most the 4 MiB maximum chunk and matches the length the pack's footer lists for that chunk, and that the lengths sum to the size `ExportProbe` reported, which is within the effective limit, so a missing or overlapping chunk is refused. It runs the reference closure check, answers `ReferenceRefused` on a failure as for `SnapshotReady`, and writes the `output_files` row, its `output_file_packs` references, and the export record's state `ready` in one transaction.
- `ExportAbort`, from the gateway, ends a failing export in place of `CheckpointAbort` and carries the reason: `too_large`, `quota_exceeded`, `pack_empty` for a declared length of zero, `pack_too_large` for one over `packMaxBytes`, `pack_key_held` for a key S1 refuses, `export_timeout`, `seal_timeout`, or `pod_evicted`. The adapter removes the clone and closes the stream. A `PostAgentOutput` `REJECT` comes after `ExportReady` has closed the stream, so its reason, `rejected`, reaches the runtime only in `output_failed` ([MD-18](message-delivery-design.md#md-18-files)).

The adapter opens the file beneath the workspace root when it reads the part, whether the export starts at once or waits in the export queue, and freezes it as MD-18 states, with a reflink clone into `/workspace/staging/export/{session_id}/{adapter_id}` where the spool passed the reflink probe and the file fits the remaining export share of the spool (IC-7, "Spool cap"). It holds at most `files.maxQueuedExports` plus `files.maxConcurrentExports` open export descriptors per session, and its count is authoritative: it relays a part beyond the count with `unopened: true`, and the gateway refuses exactly the marked parts with `too_many_exports`. It closes a part's descriptor and removes its clone when the part's export ends, and when it receives `output_failed` for the part, whether or not a stream was ever opened for it. It then chunks and packs the file as IC-2 states, and checks each chunk against the dedup set when it packs the chunk, so a chunk a checkpoint confirmed while the export ran is referenced rather than uploaded. The export takes no quiesce and no pod-wide operation lock. During a checkpoint's capture pass, the adapter pauses export reading, hashing, and compression, so an export does not compete with the capture for disk and CPU while a Full-level runtime is quiesced, and uploads of packs the export already built continue ([A17](message-delivery-design.md#decisions)).

An export ends at `ExportReady`, at a failure, or at `exportTimeoutSeconds` (900 seconds by default) from its `ExportStart`, after which the gateway fails the part, releases the export's unused reservation, and sends `ExportAbort`, and the adapter removes the clone. A part waiting in the export queue fails `exportTimeoutSeconds` after its first frame. At session end the seal checkpoint runs first, the gateway signs no export grant while it runs, and exports still running finish in the time it leaves in the session's seal window before they fail with `seal_timeout` (IC-5, "Seal"). On a preStop drain of the agent pod, the eviction checkpoint runs first, the gateway signs no export grant while it runs, exports continue only with the budget the checkpoint leaves, and an export stream never uses the stream-drain stage of [§10.1.7](../spec/10_gateway-internals.md#1017-prestop-hook-drain) (IC-7, "Exports on a drain"). A new coordinator after a handoff takes over the session's export records, reopens the stream for each part still exporting without reserving again, and the adapter reuses its clone and confirmed packs. A stream that the adapter cancels when it acknowledges the new coordinator's fence is not a failure for the previous coordinator, which stops it without releasing the reservation, changing the export record, or sending `output_failed` (IC-4, "Quota"). MD-18, "Export lifecycle", states these cases and the `output_failed` notice the runtime receives.

The gateway counts a session's outstanding pack grants across its `Checkpoint` and `ExportOutputFile` streams against one window, the pool's effective `checkpointGrantWindow`, on the session's coordinating replica, so the bound in S5 holds per session. After a handoff, the new coordinator counts the whole window as outstanding until the grant TTL has passed since the pod acknowledged its fence, and the adapter cancels the previous generation's open `Checkpoint` and `ExportOutputFile` streams at that acknowledgement (S5). A large export therefore slows the pre-copy and upload passes of a concurrent checkpoint of the same session. It does not lengthen the pause, because the capture pass uploads nothing and the export's own reading waits during it.

## IC-4: Catalog, reference closure, and quota

### Catalog

| Table | Columns |
| :-- | :-- |
| `checkpoint_packs` | `tenant_id`, `owner_session_id`, `pack_id`, which together are the key, `bytes`, `state` (`unreferenced` or `referenced`), `lease_expires_at`, `created_at`, `deleted_at` |
| `checkpoint_snapshots` | `snapshot_id`, `tenant_id`, `session_id`, `parent_snapshot_id`, `trigger`, `consistency`, `manifest_chunks`, `consumed_message_ids` (the adapter's consumed message-id set in report order), counts, `created_at`, `deleted_at` |
| `checkpoint_snapshot_packs` | `snapshot_id`, `owner_session_id`, `pack_id` |
| `output_files` | `output_file_id`, `tenant_id`, `session_id`, `part_id`, `path`, `name`, `mime_type`, `size_bytes`, `sha256` of the whole file, kept as metadata, `chunks` (each chunk's ID, offset and length in the file, pack ID, and offset within the pack), `blob_ref` (the file's `lenny-blob://` URI), `expires_at` (the session's retention), `created_at`, `deleted_at` |
| `output_file_packs` | `output_file_id`, `owner_session_id`, `pack_id` |
| `output_file_exports` | `session_id` and `part_id`, which together are the key, `tenant_id`, `state` (`queued`, `exporting`, `ready`, or `failed`), `first_frame_at`, `started_at`, `reserved_bytes`, `confirmed_bytes`, `reservation_released_at`, `coordination_generation` (the generation of the coordinator that drives the export), `failure_reason`, `notice_acked_at` |

A pack is named by its object key, `(tenant_id, owner_session_id, pack_id)`, because the pack nonce keeps an honest adapter's pack IDs distinct but a pod chooses the bytes it declares, so two sessions can still write packs with identical bytes, and every reference table names the owner with the pack ID. A pack row moves from `unreferenced` to `referenced` when a snapshot or an output file first references it, in the transaction that writes that reference. It never moves back. `state` records that a pack was referenced once, and liveness is the query over the reference tables of IC-6, "Pack reclamation", rather than the state. A `referenced` pack whose last snapshot has rotated away and whose last output file is gone therefore leaves the allowed set at once, because no source of the allowed set names it, its chunks leave the dedup set when `CheckpointStart` or `Resume` next lists the session's packs, and a reference to it that an attempt in flight still makes is refused with `ReferenceRefused`, after which the adapter uploads the chunks again in new packs. Garbage collection deletes it once its lease has expired. An `unreferenced` pack keeps its lease for `packLeaseSeconds`, a gateway setting, 1800 seconds by default, which is twice the longer of the `checkpointAttemptTimeoutSeconds` and `exportTimeoutSeconds` defaults. Pool admission refuses a pool unless `packLeaseSeconds` is at least twice the larger of its `checkpointAttemptTimeoutSeconds` and `exportTimeoutSeconds`, so a retry that runs a whole deadline after a timed-out attempt or export still finds the packs that attempt confirmed. Each `SnapshotReady`, `ExportReady`, and "already stored" answer that references a pack extends its lease to `packLeaseSeconds` from that moment.

The gateway writes an `output_file_exports` row, the export record, when it reads a file part from the session's stream, with `first_frame_at`, and sets `started_at` when it sends `ExportStart` ([MD-18](message-delivery-design.md#md-18-files), "Clocks"). The row carries the export's quota reservation (IC-4, "Quota"), and each `PackCommitted` adds the pack's bytes to `confirmed_bytes` in the transaction that inserts the pack row. A new coordinator reads the session's rows after the pod acknowledges its fence, writes its own generation to each row whose reservation is unreleased, with an update guarded by `coordination_generation < $gen`, which takes the reservation over without a second one, reopens `ExportOutputFile` for each `exporting` row, keeps each `queued` row waiting, and applies `exportTimeoutSeconds` from `started_at` and from `first_frame_at`. A `failed` row keeps the failure notice until `notice_acked_at` is set, and the gateway sends the notice to the runtime again after a resume. The rows live as long as the session's other records.

The checkpoint records the adapter's consumed message-id set in report order in `consumed_message_ids` ([`message-delivery-design.md`, "MD-3"](message-delivery-design.md#md-3-inbox-states-and-custody)). On a resume, the coordinating replica reads the set of the snapshot the session resumes from, and moves every message the gateway recorded as consumed after the newest recorded id in it to `consumed_unrecorded`. Custody of a message ends at consumption, so no consumed message waits for a checkpoint.

### Reference closure

A session's allowed set is the union of these packs:

- packs that the session's own retained snapshots reference;
- packs that the session's live output files reference;
- packs that the snapshot the session started from references, for a derived session; and
- `unreferenced` packs that the session owns with an unexpired lease.

The gateway accepts a snapshot only when its referenced pack IDs are all in the allowed set. The allowed set leaves out the source session's later snapshots, so a session derived from a live source cannot read content the source wrote after the derive. The gateway runs the same check on an output file's referenced pack IDs before it writes the `output_files` row. The reference closure transaction locks the `checkpoint_packs` rows it references with `FOR SHARE`, so garbage collection cannot soft-delete a pack between the check and the write of the reference. A failure answers `ReferenceRefused` with the offending pack IDs.

The adapter builds its dedup set from the same sources, using the manifest it holds locally, the chunk lists of the session's output files, and the leased packs that `CheckpointStart` and `Resume` name, and it uses that one dedup set for checkpoints and exports. A chunk enters the set only after its pack is confirmed, and a `ReferenceRefused` evicts the named packs' chunks, so the set does not keep a pack that expired or was reclaimed. The gateway answers one existence question, the "already stored" answer to `PackReady`. It resolves the pack ID against the session's own keys first and against the allowed set's packs that another session owns only for that answer, so the answer reveals nothing beyond the session's own keys and its allowed set. The adapter never asks whether a chunk hash exists.

The manifest's chunk-to-pack mapping is untrusted input. It can name only bytes inside packs the gateway authorized for the snapshot, and S4 detects a mapping that names the wrong bytes.

### Quota

The tenant is charged once for each pack, at confirmation, and the charge is released when the pack is deleted. A checkpoint therefore charges for its new bytes only. Today every checkpoint charges for the whole workspace. The reservation at the start of an attempt uses `estimated_new_bytes`. An export on the `ExportOutputFile` stream reserves the `estimated_new_bytes` of its `ExportProbe`, or is refused with `quota_exceeded`. Each new pack it writes is charged once at confirmation, and the charge draws down the export's reservation rather than adding to it. The estimate is the pod's report, so the gateway also applies §11.2's signing-time headroom check per export: it refuses to sign a grant whose length would carry the export's granted total past its reservation plus the tenant's remaining headroom, and a pod that reports zero new bytes still cannot exceed the quota. The export record holds the reservation under the rules §11.2 states for a checkpoint's: the atomic Redis increment records `reserved_bytes`, and every party that ends the export, the success and failure arms, a new coordinator, and the [§12.5](../spec/12_storage-architecture.md#125-artifact-store) backstop for a row of a terminal session, releases it with one guarded `UPDATE ... SET reservation_released_at = now() WHERE session_id = $1 AND part_id = $2 AND coordination_generation = $3 AND reservation_released_at IS NULL`, applying the decrement of `reserved_bytes − confirmed_bytes` only when that update changed the row. Every release and every state transition of an export record carries `AND coordination_generation = $gen`, the writing coordinator's own generation, or for the backstop the generation it read, so a coordinator that a handoff replaced changes no record and releases no reservation. A stream that a fence acknowledgement cancels is not a failure for the previous coordinator: it stops without releasing the reservation, changing the record, or sending `output_failed`. `Rehydrate` adds `SUM(reserved_bytes − confirmed_bytes)` over the `output_file_exports` rows whose `reservation_released_at` is null, beside the checkpoint reservations, so a rebuilt counter holds every outstanding export reservation and the later guarded release stays correct. Every terminal outcome settles the reservation exactly once. Writing the `output_files` row settles it, and a failure, an `exportTimeoutSeconds` expiry, a `seal_timeout`, a `PostAgentOutput` `REJECT`, and a pod loss release what the confirmed packs did not draw down. A session runs at most `files.maxConcurrentExports` exports at once, 2 by default, and at most `files.maxQueuedExports` parts, 8 by default, wait for that cap, and a waiting part holds no reservation ([MD-18](message-delivery-design.md#md-18-files)).

## IC-5: Restore, derive, download, and seal

### Restore

The gateway selects the snapshot: the latest finalized snapshot on resume, or the starting snapshot at a derived session's first bind, when the session is first attached to a pod. It signs `GET` grants for that snapshot's packs as the restore requests them, which keeps each grant's 30-second TTL meaningful for large restores. `Resume` also carries the session's output-file chunk lists and its leased packs, each leased pack with the chunk IDs its footer lists, so the restored pod's dedup set covers them (IC-4, "Reference closure").

The adapter fetches the manifest chunks first, then the packs, with `restoreConcurrency` requests in flight, 8 by default. `restoreConcurrency` is an adapter setting: the chart value `controller.adapterRestoreConcurrency` renders as the controller flag `--adapter-restore-concurrency`, and the controller passes it as the adapter flag `--restore-concurrency` to every agent pod it creates. It decompresses each chunk, verifies its SHA-256 (S4), writes the files with the same rules as `ExtractTree`, under which no path may escape the workspace root through `..` or an absolute path, a symlink is restored only when its target resolves inside the root, and setuid and setgid bits are dropped, and applies modes and `mtime`. It builds the file index from the identities of the files it wrote, so the first pre-copy after a restore reads nothing. The restore limit is the pool's `workspaceSizeLimitBytes` in place of the fixed 2 GiB.

Restore time still grows with workspace size. Lazy restore, which fetches a file on first access, is out of scope.

### Derive

A derive writes a snapshot row for the new session that references the source snapshot's manifest and packs, and it copies no object. The new snapshot becomes the derived session's starting snapshot, which places its packs in the derived session's allowed set. A derive copies no output file of the source, and the derived session's allowed set leaves out the packs that only the source's output files reference. [`session-derive-design.md`](session-derive-design.md) keeps its modes: a derive with `from: latest` uses this reference, and one with `from: start` rebuilds from the plan as that design states. [Decision D2](#decisions) records the alternative of copying packs.

A derive with `from: latest` therefore adds no bytes to the tenant's storage counter, because the tenant was charged for each referenced pack when the source confirmed it (IC-4, "Quota"). The storage reservation that session-derive takes ahead of the first copy, and its undo through `undoDerivedWrites`, cover only the uploads a derive with `from: start` copies. Under an effective `checkpointPolicy` of `none` (IC-8), the source has no snapshot, and a derive with `from: latest` is refused with `422 DERIVE_SOURCE_UNAVAILABLE` and `details.reason: checkpointing_disabled`. Both are requests to session-derive (see "Changes requested of other designs").

### Download

`GET /v1/sessions/{id}/workspace` stays a gzip-compressed tar stream. The gateway renders it from the manifest and the packs, using its own object-store credentials, and streams it to the client. The manifest is untrusted input from the pod, so the gateway bounds it before it renders: the sizes of the manifest's entries sum to at most the pool's `workspaceSizeLimitBytes`, and a manifest over that total fails the download.

The gateway renders an output file from its chunk list and packs the same way. It accepts a `Range` header, which the row's chunk offsets in the file and offsets within the pack resolve to byte ranges of the packs that hold the range, and it verifies the SHA-256 of every chunk it serves (S4). S4's per-chunk check is the storage-integrity guarantee for an output file, and the `ExportReady` checks of IC-3 catch missing or overlapping chunks. The whole-file SHA-256 that `ExportReady` carried is metadata in the `output_files` row. The gateway publishes it as `sha256` on the released file part and in a `Repr-Digest` header (RFC 9530) on each response of `GET /v1/blobs/{ref}`, which describes the full file in a `206` response too, so a client can verify its download end to end. The gateway compares it only where it already reads the whole file before release, the `PostAgentOutput` scanner's read, and refuses the part on a mismatch as a `REJECT`. The hash cannot detect a compromised pod, which writes both the chunk list and the hash, and the tier 1 tests and the adapter conformance tests catch an adapter that orders chunks wrongly ([A20](message-delivery-design.md#decisions)). [`message-delivery-design.md`, "MD-18 Files"](message-delivery-design.md#md-18-files) states the route.

### Seal

The seal runs the same passes with trigger `seal`, and the resulting snapshot becomes `final_workspace_ref`. The seal checkpoint runs first, within the session's seal window, which keeps its meaning as the retry window for object-store failures of the seal ([§7.1](../spec/07_session-lifecycle.md#71-normal-flow), **Seal-and-export invariant:**). The gateway signs no export grant while it runs, so the seal has the session's whole grant window. The seal captures the workspace, which holds every exported file, and it shares one dedup set with the exports, so an exported file's chunks upload once, and an export that continues after the seal references the chunks the seal confirmed. Exports of the session still running, or waiting in the export queue, when the seal checkpoint ends finish in the time it leaves in the same window, and under an effective `checkpointPolicy` of `none`, which takes no seal snapshot, they get the whole window. Each part still exporting or waiting when the window ends fails with `seal_timeout`. §7.1's terminal pipeline budget stays the seal window plus the release allowance of 30 s ([A19](message-delivery-design.md#decisions)). A file the runtime changed or deleted after emitting its part is exported from its clone, whose chunks the seal does not hold, and an export pack whose grant is outstanding when the seal starts is not yet confirmed, so the seal may upload its chunks again, at most one grant window of packs. The seal window is the runtime revision's `maxWorkspaceSealDurationSeconds` when set, and otherwise the pool's, at most the chart value `gateway.workspaceSealMaxDurationCeilingSeconds`, as MD-18, "Seal window" states.

## IC-6: Retention, garbage collection, erasure, and legal hold

- **Retention.** The latest 2 snapshots per session are retained, as today, and legal hold suspends rotation. Output files follow the session's retention rather than snapshot rotation, and a legal hold on the session keeps them ([MD-18](message-delivery-design.md#md-18-files)). A pack that any live snapshot or output file references stays, so an output file can keep a pack alive after every snapshot that shared it has rotated.
- **Pack reclamation.** Garbage collection deletes a pack when no live snapshot and no live output file references it and its lease has expired. It computes liveness with a query over `checkpoint_snapshot_packs` and `output_file_packs` instead of a stored counter, so the count cannot drift, and a pack that a rotated snapshot shared with an output file stays. Before it soft-deletes a pack, it locks the pack row with `FOR UPDATE` under `deleted_at IS NULL` and re-checks liveness and the lease in that transaction, so a reference closure transaction that holds the row `FOR SHARE` either commits its reference first or finds the pack deleted and refuses the reference (IC-4, "Reference closure"). The deletion path keeps the rules of [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store): soft delete first, guarded quota release, then the object delete, with the backstop sweep. The guarded quota release returns the tenant's quota only when the soft delete changed the row, so two concurrent deleters cannot release it twice. A soft-deleted pack row keeps its key for `gc.tombstoneRetentionSeconds` while the object delete or the backstop sweep is still pending, so S1 refuses a grant for that key and never revives the row: a revived upload could be deleted after the fact. The pack nonce keeps an honest adapter from ever declaring such a key. The backstop sweep is a periodic garbage collection pass that deletes objects a failed primary cleanup left behind.
- **Compaction.** When the adapter builds its dedup set, it treats chunks in packs that the parent snapshot uses below `packRewriteThreshold`, a constant of 0.5, as absent. Their live chunks go into new packs, and the sparse pack loses its last reference after rotation, or at the end of the session's retention when an output file still references it. Compaction needs no gateway knowledge of chunks.
- **Erasure.** `DeleteByUser` deletes the user's snapshots and output files, and `DeleteByTenant` deletes the tenant's. A pack that another user's derived snapshot references stays until that snapshot goes. This matches today's outcome, where the derive copy survives the source user's erasure.
- **Security holds.** A security hold under [FE-3](forensics-evidence-preservation-design.md#fe-3-platform-only-holds-and-a-security-retention-class) that includes `checkpoints` copies each held snapshot to escrow. Under this design the copy of a snapshot is every pack its manifest references, the manifest's own packs included, written to the hold's escrow prefix, with the snapshot root recorded among the hold's items. The copy references no pack outside that prefix. Pack reclamation and compaction therefore cannot remove escrowed content, and a pack shared with another snapshot of the session or with a derived session is copied whole. A hold whose `include` lists `artifacts` covers the output files of a held session ([A16](message-delivery-design.md#decisions)), and it copies each one as it copies a held snapshot: every pack the file's chunk list references goes to the hold's escrow prefix, and the `output_files` row is recorded among the hold's items. Snapshot rotation, the deletion of an output file at the end of the session's retention, the deletion of an `output_files` row after a `PostAgentOutput` `REJECT`, which includes the refusal of a part whose scanned bytes do not match its whole-file SHA-256, pack reclamation, `DeleteByUser`, and `DeleteByTenant` call `EnsureCopied` before they soft-delete a snapshot row, an `output_files` row, or a pack row, and a copy failure refuses the deletion, which the deleter retries, as FE-3 states for every deletion path.
- **Legal-hold escrow.** The §12.8 legal-hold escrow, which migrates a held tenant's resources to the escrow bucket on a forced tenant deletion, handles a pack-backed item, a snapshot or an output file, the same way: it copies every pack the item references, the manifest's own packs included for a snapshot, and it records the item's row.
- **Partial manifests.** The tar-prefix reassembly in [§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain) **Reassembly on resume.** is removed ([decision D4](#decisions)). A timed-out attempt leaves confirmed packs that the next attempt reuses, and a resume uses the latest finalized snapshot.

## IC-7: Size limits and drain budgets

- **Workspace limit.** The pool's `workspaceSizeLimitBytes` keeps its name and now bounds disk use and storage. [EG-2](egress-and-git-design.md#proposal-split) renders `emptyDir.sizeLimit` on the workspace volume, which the tree omits today, because the egress design relies on the limit. IC-7 changes the value the pod controller renders to the sum of the slot limits plus the spool cap, and whichever of EG-2 and IC-7 lands second keeps the other's rendering and applies its own part. The adapter's size probe and the restore use the same value. [Decision D3](#decisions) sets the default.
- **Spool cap.** The pool's `checkpointCaptureBudgetBytes` caps the spool together with the output-file export clones that the adapter writes under `/workspace/staging/export/{session_id}/{adapter_id}` ([MD-18](message-delivery-design.md#md-18-files)). Export clones may use at most `exportSpoolFraction` of the cap, a constant of 0.5, so a checkpoint's capture pass, which keeps a Full-level runtime quiesced, always has at least half of the spool. The adapter clones a part when it reads it, so the clones of parts waiting in the export queue count against that share as well as those of running exports. On a reflink pool, a file larger than the remaining export share, 256 MiB at the default `checkpointCaptureBudgetBytes` of 512 MiB, is read in place with the identity check of MD-18 and is not cloned ([A21](message-delivery-design.md#decisions)). The adapter removes a clone when its export ends, whatever the outcome.
- **Attempt deadline.** The 60-second `CheckpointTimeout` splits into the pause budget and the pool's `checkpointAttemptTimeoutSeconds` (default 300) for the whole attempt. The first checkpoint of a session holding a large clone can span several attempts, and each attempt continues from the packs the previous ones confirmed.
- **Exports on a drain.** On a preStop drain of the agent pod the eviction checkpoint runs first, within `evictionCheckpointCapSeconds`, and the gateway signs no export grant while it runs, so the checkpoint has the session's whole grant window. Exports continue only with the budget the checkpoint leaves in the drain's checkpoint stage, and an export still running when that stage ends fails with `pod_evicted`, as does each part still waiting in the export queue. An export stream never uses the stream-drain stage of [§10.1.7](../spec/10_gateway-internals.md#1017-prestop-hook-drain), whose 30 seconds the grace floor reserves for the session streams, and a gateway replica's own preStop drain does not wait for its export streams, which the session's next coordinator reopens ([MD-18](message-delivery-design.md#md-18-files)).
- **Drain caps.** The tier table in [§10.1.7](../spec/10_gateway-internals.md#1017-prestop-hook-drain) sizes the preStop wait by total workspace bytes: it caps the wait at 30, 60, or 90 seconds according to the session's last measured workspace size (see "preStop drain and tiered drain cap" under "Terms"). Under this design a drain checkpoint uploads the bytes changed since the last snapshot, so the table becomes one pool setting, `evictionCheckpointCapSeconds` (default 90). The pool-config admission webhook's agent-pod grace floor and BarrierAck floor keep their structure with that single cap in place of `max_tiered_checkpoint_cap`. The grace floor checks that a pod's grace period covers a capped checkpoint for every session slot plus the stream drain, and the BarrierAck floor checks that the gateway waits for a `CheckpointBarrierAck` at least as long as that cap (see "Pool-config admission webhook and its floors" under "Terms").
- **Barrier hold.** The CheckpointBarrier of [§10.1.8 Rolling Updates and the CheckpointBarrier Protocol](../spec/10_gateway-internals.md#1018-rolling-updates-and-the-checkpointbarrier-protocol) holds tool-call dispatch until the capture pass ends instead of until the upload ends.

## IC-8: Checkpoint policy, interval, and change tracker

Today the only checkpoint setting is the gateway-wide interval, `--checkpoint-interval`, which the chart sets from `periodicCheckpointIntervalSeconds`. A value of 0 falls back to the 600-second default (`interval` in `pkg/gateway/checkpoint/checkpointer/checkpointer.go`), so no setting turns checkpointing off. Neither a Runtime nor a pool can set a policy or an interval.

### Policy

The Runtime definition ([§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime)) and the pool configuration ([§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) each take an optional `checkpointPolicy`:

| Value | Checkpoints during the session | Seal at session end | Effect on the rest of the platform |
| :-- | :-- | :-- | :-- |
| `full` (default) | Periodic, pre-scale-down, drain, CheckpointBarrier, and eviction checkpoints run as IC-1 states. | Runs. | Unchanged. |
| `sealOnly` | None. | Runs, and its snapshot becomes `final_workspace_ref`. | A pod failure or an eviction resumes the session with `resumeMode: conversation_only` and `workspaceLost: true`, from the [§4.4.5](../spec/04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback) minimal-state record. An idle or suspended session keeps its pod, and the release of a suspended session's pod after `maxSuspendedPodHoldSeconds` is not taken. Under `full`, a session suspended by an interrupt releases its pod after `maxSuspendedPodHoldSeconds` (900 seconds by default), and an idle suspension checkpoints the workspace and releases the pod at once ([§6.2 Pod State Machine](../spec/06_warm-pod-model.md#62-pod-state-machine)). The session later resumes on another pod from that checkpoint. `GET /v1/sessions/{id}/workspace` and a derive with `from: latest` read the sealed snapshot once the session ends. |
| `none` | None. | Does not run. | Everything under `sealOnly`, and the session never has a workspace snapshot. `GET /v1/sessions/{id}/workspace` returns `404 RESOURCE_NOT_FOUND`. A derive with `from: latest` returns `422 DERIVE_SOURCE_UNAVAILABLE` with `details.reason: checkpointing_disabled`, while a derive with `from: start` still rebuilds from the plan. |

The effective policy is the stricter of the two values, in the order `full`, `sealOnly`, and `none`, where an unset value counts as `full`. The runtime author knows whether the runtime's state lives in workspace files, and the operator owns the pool's storage budget, so either one can turn checkpointing down and neither can turn it back up. A runtime that [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) derives from a base inherits the base's policy and can only make it stricter.

The policy has these further effects:

- **Credential rotation.** A runtime that declares `credentialCapabilities.hotRotation: false`, because it cannot take a rotated credential in place, rotates by checkpoint and restart ([§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service)). Under an effective `checkpointPolicy` of `sealOnly` or `none`, that restart has no checkpoint, so the session restarts with `resumeMode: conversation_only`. Pool admission answers such a combination with a `Warning` naming the consequence.
- **Freshness.** Sessions whose effective policy is `sealOnly` or `none` are left out of `lenny_checkpoint_stale_sessions` and the `CheckpointStale` alert.
- **Visibility.** The session envelope reports the effective `checkpointPolicy`, so a client knows before a failure whether the workspace survives one.
- **Drain.** The preStop path and the [§10.1.8](../spec/10_gateway-internals.md#1018-rolling-updates-and-the-checkpointbarrier-protocol) CheckpointBarrier skip the checkpoint step for these sessions, and the pool-config admission webhook's agent-pod grace floor leaves out the checkpoint cap for a pool whose effective policy is `none`.

The policy is separate from a runtime's ability to quiesce. A runtime whose `lifecycle_support` leaves out `"checkpoint"` still gets checkpoints under `checkpointPolicy: full`, captured on the best-effort path without a `checkpoint_request`. Today the adapter sends `checkpoint_request` to such a runtime anyway, because the Checkpoint handler never calls `Supports("checkpoint")` (see "Defects found while verifying").

### Interval

The Runtime definition and the pool configuration each take an optional `periodicCheckpointIntervalSeconds`. The effective interval for a session is resolved in this order:

1. the pool's value, when set;
2. otherwise the Runtime's value, when set;
3. otherwise the gateway default (`periodicCheckpointIntervalSeconds`, 600 seconds).

The gateway clamps the result to the deployer's bounds, `checkpoint.minIntervalSeconds` (default 60) and `checkpoint.maxIntervalSeconds` (default 3600). The pool's value wins over the Runtime's because the operator pays for the pool's object storage and its gateway load, and the deployer's bounds protect the gateway and the object store from any single pool. [Decision D9](#decisions) records the alternative.

A tenant may also set `periodicCheckpointIntervalSeconds` on a runtime that [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) derives from a base, at or above the base's interval, which is the interval the order above gives a session on the owned pool with no tenant value. A tenant write with a lower value is refused, and a longer interval is accepted, so a tenant cannot raise the object storage and gateway load that the operator or the runtime author chose. The field is live, so the floor reads the base's current value at write and at resolve, and a session's interval is the longer of the tenant's value and the base's. A later base change that raises the base's interval above the tenant's applies at the next resolve, as that design's "DR-6: Base changes" states for live restrict-only fields. The deployer's bounds still clamp the result.

The gateway's schedule then carries a per-session interval. The first-checkpoint jitter applies the gateway's `periodicCheckpointJitterFraction` to the session's own effective interval. The jitter delays a session's first periodic checkpoint by a random share of the interval, up to that fraction (0.2 by default), so that sessions started together do not checkpoint together ([§4.4.4](../spec/04_system-components.md#444-checkpoint-slos-and-scheduling)). The freshness service-level objective (SLO) and `CheckpointStale` measure each session against its own effective interval. An interval has no effect under an effective `checkpointPolicy` of `sealOnly` or `none`.

A short interval is cheap under IC-1, because each checkpoint uploads only the bytes changed since the last one. Under the current implementation, a 60-second interval on a 1 GiB workspace would pause a Full-level runtime for about 27 seconds of every minute, at the archive rates in "Measurements" and a 100 MiB/s upload. The interval override therefore lands together with IC-1 or after it, and never before it.

### Change tracker setting

The pool configuration takes an optional `checkpointChangeTracker` with the values `auto` (default) and `off`. Under `auto` the adapter runs the tracker when the workspace mount's file system type is one on which writes from the agent container reach the adapter container's watches: a node file system under runc, `ext4` on a Kata block volume, and the sentry's `tmpfs` under gVisor's pod-share mount hint. It leaves the tracker off on `9p`, which is gVisor's default mount, on `virtiofs`, which is Kata's shared file system, and on any other `fuse` file system. No value forces the tracker on, because on those file systems it would miss changes without reporting the loss, which R11 forbids. `checkpointChangeTracker: off` keeps every capture on the stat walk.

The pod controller renders the gVisor mount hint annotations for the workspace volume on every pod of a pool with `isolationProfile: sandboxed`. They take effect on nodes whose containerd runtime handler passes `dev.gvisor.*` annotations to runsc, and they are ignored elsewhere, where the adapter sees `9p` and walks. The adapter reports the effective value and the tracker's state in `lenny_checkpoint_tracker_state`.

### Runtime fields and revisions

[`runtime-revisions-design.md`](runtime-revisions-design.md) sorts a Runtime's fields into versioned and live classes ([RV-1](runtime-revisions-design.md#rv-1-revisions)). The Runtime fields this design adds fall into these classes:

- `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are live. A write to either applies to the next session on every revision in the runtime's traffic and creates no revision, and the resolution rules under "Policy" and "Interval" read the current value.
- `checkpointExcludes` ([D5](#decisions)) is versioned, because it names the regenerable directories the binary writes, and gateway-read under decision D14 of [`runtime-revisions-design.md`](runtime-revisions-design.md#decisions), because it reaches the pod only as session data. A write to it creates a revision that shares the pod set of the revision it amends, and a session's checkpoints leave out the list of the revision chosen for it under [RV-2](runtime-revisions-design.md#rv-2-selection).
- **Delivery.** At the post-chain point the gateway composes the session's list, the chosen base revision's list with a tenant's appended entries, and stores it on the session record. It sends the list to the adapter on the call that assigns the session to the pod, `FinalizeWorkspace` or `Resume`. The adapter keeps it with the session's slot, leaves the listed directories out of every capture of that session, the seal included, and adds no tracker watch under them. A resume sends the stored list, including a resume that moves revision under D7 of that design, which keeps the session's stored plan as well (D19), so the list changes only for a new session.
- A tenant's `checkpointExcludes` on a derived runtime merges over the selected base revision: the session's list is the base revision's list with the tenant's entries appended. A tenant's `checkpointPolicy` on a derived runtime can only make the base's current policy stricter, as "Policy" states, and a tenant's `periodicCheckpointIntervalSeconds` can only lengthen the base's current interval, as "Interval" states.
- The pool admission `Warning` under "Policy" reads `credentialCapabilities.hotRotation`, which is versioned, so admission answers with the `Warning` when any revision in the runtime's traffic declares `hotRotation: false`.

## IC-9: Removal of the embedded `SIGSTOP` checkpoint path

[§4.4.3](../spec/04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling) lets an embedded adapter stop the agent with `SIGSTOP` for a checkpoint and resume it with `SIGCONT`, under a 60-second watchdog. The watchdog, or a failed check that the agent left the stopped state, sets `checkpointStuck`, and in embedded mode `/healthz` then fails the liveness probe so that Kubernetes restarts the pod. The owner has ruled out a mechanism that forces a runtime to pause (see "Owner's direction"), and the tree calls the path from nowhere (see "Current state"). IC-9 removes it ([decision D15](#decisions)):

- The **Embedded adapter mode only:** bullet of §4.4.3, the embedded adapter path under **Checkpoint timeout and recovery:**, **SIGCONT confirmation.**, and **Embedded adapter liveness integration:** are removed, and so are the `checkpointStuck` flag and `watchdogTimeoutSeconds`. The sentence of **Checkpoint abort cleanup:** that forbids recovering a checkpoint goroutine's panic while `SIGCONT` is unconfirmed loses its subject and is removed with them.
- An embedded binary is checkpointed by the rule that applies to every runtime. It gets a consistent snapshot when it takes part in the quiesce handshake, and a best-effort snapshot on the path under IC-1, "Best-effort capture", otherwise. The platform never stops its process for a checkpoint.
- `pkg/adapter/embeddedcheckpoint` is deleted. The `embedded` value of `pkg/checkpoint`'s `Level`, which maps to `consistent` only because of the `SIGSTOP` path, is removed, so the consistency of a checkpoint follows from the capture that produced it.
- The statements that rest on the path change with it: the embedded adapter sentence and the `SIGSTOP` clause of **Hard workspace size limit.** in §4.4.4, the "embedded-adapter pools" scope of `CheckpointDurationHigh` in §4.4.4 and §16.5, the `SIGSTOP` rationale of **Interrupt during checkpoint:** in §4.7.2, and the `SIGSTOP`/`SIGCONT` deliverable of §18 (see "Specification sections affected").
- Agent pods render no liveness probe today, and no rule in the specification asks for one once **Embedded adapter liveness integration:** is gone. [FE-1](forensics-evidence-preservation-design.md#fe-1-quarantine) relies on that, because a node-side freeze of an embedded container would otherwise fail a probe on `/healthz` and have the kubelet kill the frozen container.

IC-9 depends on no other IC proposal, and it can land first.

## Measurements

These numbers were measured on a laptop on 2026-11-05, before the cloud runs. They come from a Docker Desktop VM with no CPU limit and warm page caches. "Benchmarks" holds the cloud measurements, which supersede these where the two overlap.

| Measurement | Environment | Result |
| :-- | :-- | :-- |
| Current archive path (a copy of `ArchiveTree`), this repository's `.git` | macOS, Apple M3 Pro, one goroutine, output discarded | 719 MiB in 8.1 s, 89 MiB/s |
| Same, this repository's `pkg/` | Same | 32 MiB in 0.92 s, 35 MiB/s |
| Same, 256 MiB of random bytes | Same | 2.8 s, 93 MiB/s |
| Same, a file appended to during the archive | Same | `archive/tar: write too long`, attempt fails |
| Same, a file created and removed during the archive | Same | `lstat ...: no such file or directory`, attempt fails |
| Same, a file rewritten during the archive | Same | No error in one run |
| Stat-only walk, 420,000 files in 70,000 directories | Linux arm64 container, 12 CPUs, overlayfs over ext4 | Sequential 2.12 s, 4 workers 0.51 s, 16 workers 0.35 s |
| Same | XFS loopback with `reflink=1` | Sequential 0.62 s, 4 workers 0.16 s, 16 workers 0.22 s |
| Stat-only walk, 388,091 files | macOS APFS, sequential | 8.9 s |
| `FICLONE` of a 2 GiB file | XFS loopback with `reflink=1` | 1.1 ms |
| `FICLONE` of 10,000 files of 4 KiB, against a plain copy | Same | 603 ms against 140 ms |
| SHA-256 and MD5 throughput in Go | Apple M3 Pro | 2,615 MiB/s and 718 MiB/s |
| Local copy of 512 MiB with `fsync` | macOS SSD | 1,379 MiB/s |

A parallel stat walk on Linux costs 0.4 to 0.8 µs per file, so the walk alone fits the R1 target at 500,000 files. Today's archive rate of 35 to 93 MiB/s, before any upload, is what makes a whole-workspace pause long.

## Benchmarks

These measurements were taken on AWS between 2026-11-05 and 2026-11-06 with a standalone harness. The harness implements the current method and this design's capture, storage, and restore paths. It runs no Lenny component and uses none of the project's test infrastructure. The scripts, raw results, and analysis are kept outside the repository with the benchmark notes.

### Setup

- **Nodes.** EC2 in `us-west-2` on Amazon Linux 2023, with containerd 2.4.2, runc 1.5.2, gVisor `release-20261102.0` on the systrap platform with directfs, and Kata Containers 4.3.1 on QEMU. Most runs used `m8i.2xlarge` with nested virtualization enabled and a gp3 root volume at its baseline of 3,000 IOPS and 125 MiB/s. Other runs used `i4i.2xlarge` with local NVMe and `m7i.metal-24xl` bare metal with gp3.
- **Pods.** Each run started one container through nerdctl, limited to 4 CPUs and 8 GiB or to 2 CPUs and 4 GiB, with the workspace on a host directory, which is how the kubelet backs an `emptyDir`. Kubernetes and the kubelet were not involved.
- **Workspaces.** The harness cloned each repository from GitHub once, stored it in S3 as a tar, and extracted it into the workspace before each run with the page cache dropped.

  | Workspace | History | Files | Directories | Size |
  | :-- | :-- | --: | --: | --: |
  | Chromium | Depth 1 | 516,434 | 46,969 | 5.9 GiB |
  | LLVM | Full | 189,899 | 17,153 | 6.3 GiB |
  | Linux | Full | 97,281 | Not recorded | 8.1 GiB |

- **Object store.** S3 in the same region, through the AWS SDK. The current method uploads 16 MiB tar chunks one at a time, as `streamChunks` does. This design's path keeps several pack uploads in flight.
- **Quiesce.** The harness stands in for a Full-level runtime by sending `SIGSTOP` to the workload's process group at the start of the capture pass and `SIGCONT` at its end. The pause figures therefore leave out the time a runtime takes to reach `checkpoint_ready`.
- **Scenarios.** "Steady" is a checkpoint after 50 files changed. "After checkout" follows `git checkout HEAD~2000`, on the repositories with full history. "Package install" runs a writer that creates 150,000 files of 4 KiB at 10,000 files per second, and the checkpoint starts 3 seconds after the writer. Other scenarios wrote constantly to a 4 GiB file and ran `git repack -a -d` during the checkpoint.
- **Variants.** "gVisor exclusive" adds `--file-access-mounts=exclusive`, and "gVisor KVM" adds `--platform=kvm`. "Kata metadata" and "Kata always" set `virtio_fs_cache`, whose default is `auto`. "Kata block" places the workspace on an ext4 file system on a block device passed into the guest, which is what Kata's `emptydir_mode = "block-plain"` gives a pod. Kata guests booted with `sysctl.fs.inotify.max_user_watches=1048576` once the guest limit was found, and containerd ran with `LimitNOFILE=1048576` in the final run.
- **Checks.** Every steady, after-checkout, and package-install capture was followed by a verification walk while the workload was still stopped, which counted the changes the capture had missed. Every restore verified each chunk's SHA-256. No run reported here missed a change or restored a wrong byte. An earlier tracker version cleared its overflow flag at each swap, and on Kata, where 31,483 watches had failed, it missed 28 changed files. The persistent degraded state in "Change tracker" is the correction, and the runs after it missed none.

### Current method against this design

The table compares the pause on 4 CPUs on `m8i.2xlarge`. The current method's pause is the time it takes to archive and upload the whole workspace, which is how long a Full-level runtime stays quiesced today. Every value in that column is above the 60-second `CheckpointTimeout`, so in the platform today each of these checkpoints would fail after pausing the runtime for 60 seconds. This design's pause is shown for the stat walk and for the change tracker, for the steady scenario.

| Profile | Workspace | Current method: pause | This design: pause, walk | This design: pause, tracker |
| :-- | :-- | --: | --: | --: |
| runc | Chromium | 225 s | 1.09 s | 0.12 s |
| runc | LLVM | 196 s | 0.30 s | 0.03 s |
| runc | Linux | 322 s | 0.09 s | Not measured |
| gVisor | Chromium | 311 s | 8.59 s | 0.11 s |
| gVisor | LLVM | 284 s | 2.93 s | 0.04 s |
| gVisor | Linux | 459 s | 1.46 s | Not measured |
| Kata | Chromium | 625 s | 6.40 s | 0.16 s |
| Kata | LLVM | 671 s | 2.40 s | 0.06 s |
| Kata | Linux | 1,017 s | 0.60 s | Not measured |

The current method uploaded 2.80 GiB for Chromium, 4.19 GiB for LLVM, and 6.49 GiB for Linux on every checkpoint. This design uploaded 2.97 GiB and 4.31 GiB on the first checkpoint, because zstd at its fastest level compresses less than gzip, and one pack on each steady checkpoint.

The first checkpoint of a session reads and uploads the whole workspace during pre-copy, while the runtime runs. It took 40 to 76 s for Chromium and 18 to 32 s for LLVM under runc, 50 to 113 s under gVisor, and 39 to 83 s under Kata. Its pause was the same as a steady pause, because pre-copy leaves only the files changed during the pass for the capture.

### Pause by configuration

The tables give the pause in seconds as walk / tracker, on `m8i.2xlarge` with nested virtualization unless the row names `metal`. Rows marked 2 CPUs ran with 2 CPUs and 4 GiB, and every other row with 4 CPUs and 8 GiB.

Chromium:

| Configuration | Steady | Package install |
| :-- | --: | --: |
| runc | 1.09 / 0.12 | 1.40 / 0.42 |
| gVisor | 8.59 / 0.11 | 13.76 / 5.87 |
| gVisor exclusive | 5.86 / 0.12 | 10.39 / 5.95 |
| gVisor exclusive, 2 CPUs | 6.49 / 0.11 | 16.87 / 5.63 |
| gVisor, metal | 9.87 / 0.10 | 15.65 / 5.10 |
| gVisor KVM, metal | 13.69 / 0.11 | 19.85 / 3.52 |
| gVisor, pod-share mount hint, Kubernetes | 2.75 / 0.13 | 7.45 / 4.14 |
| Kata | 6.40 / 0.16 | 7.99 / 2.26 |
| Kata metadata | 2.35 / 0.16 | 4.76 / 2.58 |
| Kata metadata, metal | 1.64 / 0.12 | 3.60 / 2.82 |
| Kata block | 1.34 / 0.16 | 1.55 / 0.55 |
| Kata block, 2 CPUs | 1.73 / 0.15 | 2.47 / 1.84 |
| Kata block, metal | 1.05 / 0.13 | 1.43 / 0.39 |

LLVM:

| Configuration | Steady | After checkout | Package install |
| :-- | --: | --: | --: |
| runc | 0.30 / 0.03 | 0.43 / 0.03 | 0.55 / 0.31 |
| gVisor | 2.93 / 0.04 | 2.97 / 0.03 | 5.98 / 9.44 |
| gVisor exclusive | 2.03 / 0.03 | 2.07 / 0.03 | 4.30 / 6.42 |
| gVisor exclusive, 2 CPUs | 2.32 / 0.03 | 2.27 / 0.03 | 10.40 / 10.91 |
| gVisor, metal | 3.19 / 0.03 | 3.17 / 0.03 | 6.26 / 8.74 |
| gVisor KVM, metal | 4.56 / 0.03 | 4.52 / 0.03 | 7.38 / 3.67 |
| Kata | 2.40 / 0.06 | 2.45 / 0.04 | 3.17 / 1.15 |
| Kata metadata | 0.74 / 0.03 | 0.75 / 0.03 | 2.08 / 1.79 |
| Kata metadata, metal | 0.51 / 0.03 | 0.58 / 0.03 | 1.82 / 2.04 |
| Kata block | 0.30 / 0.03 | 0.42 / 0.05 | 0.56 / 0.30 |
| Kata block, 2 CPUs | 0.43 / 0.03 | 0.47 / 0.03 | 0.89 / 0.62 |
| Kata block, metal | 0.31 / 0.03 | 0.46 / 0.03 | 0.54 / 0.25 |

An earlier run on nested `m8i.2xlarge` measured gVisor KVM at 7.55 / 0.04 s for the LLVM steady pause, and Kata always at 2.35 / 0.16 s for Chromium and 0.75 / 0.04 s for LLVM. Kata always matched Kata metadata.

The package-install columns are not a comparison between walk and tracker. The walk run and the tracker run each restarted the writer, and the number of files written by the pause differed between them by up to a factor of 3. What the columns show is the pause for 10,000 to 100,000 freshly written files. In the walk and tracker columns of the same row, the read cost per changed file was the same.

### What the pause consists of

The pause has a change-source part and a read part. These are the per-unit costs measured in the package-install scenario on 4 CPUs.

| Configuration | Stat walk, per file in the workspace | Read, per changed 4 KiB file | Tracker barrier wait during package install |
| :-- | --: | --: | --: |
| runc | 1.6 to 2.1 µs | 5 to 7 µs | 0.2 to 0.3 s |
| gVisor | 15 to 17 µs | 51 to 56 µs | 3.9 to 6.1 s |
| gVisor exclusive | 11 µs | 46 to 49 µs | 3.9 to 4.0 s |
| gVisor KVM, metal | 24 to 27 µs | 47 to 48 µs | 3.5 to 3.7 s |
| Kata | 12 µs | 62 to 69 µs | 0.7 to 1.6 s |
| Kata metadata | 3.9 to 4.6 µs | 116 to 129 µs | 0.5 to 0.9 s |
| Kata metadata, metal | 2.7 to 3.2 µs | 67 to 70 µs | 0.7 to 1.1 s |
| Kata block | 1.6 to 2.6 µs | 6 to 8 µs | 0.2 to 0.4 s |

The tracker removes the stat walk. Under gVisor it replaces the walk with a barrier wait when files are being created at 10,000 per second, because the tracker reads events more slowly than the workload produces them, and the pause waits for the backlog. Under runc and on a Kata block volume both parts stay small.

### Effect of the tracker on the runtime's work

The harness ran each workload six times in alternation, three times without the tracker and three times with it, after a warm-up run. The table gives the medians, in seconds, without / with the tracker.

| Configuration | Create 50,000 files | Delete 50,000 files | `git status`, Chromium | `git checkout HEAD~2000`, LLVM |
| :-- | --: | --: | --: | --: |
| runc | 0.59 / 0.65 (+10%) | 0.32 / 0.37 (+14%) | 0.99 / 0.98 | 2.17 / 2.23 (+3%) |
| gVisor | 3.01 / 5.05 (+68%) | 0.63 / 0.81 (+27%) | 9.41 / 9.49 | 5.68 / 6.21 (+9%) |
| gVisor exclusive | 2.75 / 4.53 (+65%) | 0.61 / 0.75 (+24%) | 7.33 / 7.09 | 4.80 / 5.19 (+8%) |
| gVisor exclusive, 2 CPUs | 2.74 / 6.24 (+128%) | 0.62 / 1.06 (+71%) | 7.52 / 7.41 | 5.22 / 5.56 (+7%) |
| gVisor, metal | 3.14 / 4.52 (+44%) | 0.60 / 0.79 (+32%) | 10.26 / 10.29 | 5.73 / 6.12 (+7%) |
| gVisor KVM, metal | 3.00 / 3.64 (+21%) | 0.91 / 0.99 (+9%) | 13.23 / 13.10 | 7.00 / 7.20 (+3%) |
| Kata | 14.22 / 15.64 (+10%) | 6.77 / 7.25 (+7%) | 19.30 / 19.34 | 11.94 / 12.41 (+4%) |
| Kata metadata | 13.81 / 15.12 (+9%) | 6.75 / 6.85 (+2%) | 6.30 / 6.29 | 8.91 / 9.47 (+6%) |
| Kata block | 0.50 / 0.58 (+16%) | 0.24 / 0.52 (+117%) | 1.15 / 1.25 | 2.22 / 2.85 (+28%) |
| Kata block, 2 CPUs | 0.47 / 0.61 (+30%) | 0.24 / 0.49 (+104%) | 1.20 / 1.20 | 2.22 / 7.35 (+232%) |

Workloads that read files, such as `git status`, are unaffected. Workloads that create or delete many files pay the tracker's event handling, which runs on the pod's CPUs. Under gVisor, creating 50,000 files took 0.6 to 3.5 s longer. On a Kata block volume with 2 CPUs, two of the three checkouts with the tracker took 7.4 s against 2.2 s, which is the largest effect measured.

### Isolation variants

- **gVisor exclusive file access** lowered the stat walk by 30 to 32% and left the read cost unchanged. In Kubernetes it also stopped every kubelet update from reaching the sandbox (see "Kubernetes checks"), so this design rules it out.
- **gVisor's pod-share mount hint** made the stat walk 3.2 times faster than the default mount and restore 3.2 times faster, and it is the only gVisor configuration on which the tracker sees writes from another container (see "Kubernetes checks").
- **gVisor KVM** made the stat walk slower than systrap on both node types. On bare metal it was 39 to 43% slower, and under nested virtualization it was 2.6 times slower. Tracker setup was 37 to 39% slower on metal. File creation with the tracker running was faster than under systrap, at 3.64 s against 4.52 s for 50,000 files. KVM does not reduce the checkpoint pause, so this design does not recommend it for checkpointing.
- **Kata cache modes.** `virtio_fs_cache = "metadata"` made the stat walk 2.7 to 3.2 times faster than the default and doubled the read cost per small file under nested virtualization. `always` measured the same as `metadata`.
- **Kata block volume** gave the lowest pause of any Kata variant, about the same as runc, and a tracker setup cost close to runc's. The workspace is then a file system inside the guest, so the host cannot read it.
- **Bare metal** lowered Kata's walk pause by up to 31% against nested virtualization. It did not help gVisor, whose pause on `m7i.metal-24xl` was 9 to 15% longer than on `m8i.2xlarge`, which has a newer CPU.

### Sizing

- **CPUs.** With 2 CPUs in place of 4, the steady walk pause grew 11 to 43%. The package-install pause under gVisor grew 1.6 to 2.4 times, and the first checkpoint of Chromium under gVisor took 208 s against 139 s.
- **Disks.** Local NVMe on `i4i.2xlarge` did not lower the pause under runc or gVisor against gp3, because the changed files were in the page cache. It made the first checkpoint faster under runc, at 22 s against 31 s for Chromium.

### Restore

A restore downloads the packs, verifies every chunk, and writes each file once. The tables give the seconds until the restored tree is complete, which is when the runtime can start. The `sync` column is the time a following `sync` took to flush the written data to disk, which a restore does not wait for.

Chromium on `m8i.2xlarge` with 4 CPUs, against today's tar restore in the same container:

| Configuration | Container memory limit | Today's tar restore | This design | `sync` after this design |
| :-- | :-- | --: | --: | --: |
| runc, gp3 at 3,000 IOPS and 125 MiB/s | 8 GiB | 75 s | 159 to 168 s | 8 to 13 s |
| runc, gp3 at 3,000 IOPS and 125 MiB/s | 28 GiB | 59 s | 88 to 96 s | 91 to 97 s |
| runc, gp3 at 16,000 IOPS and 1,000 MiB/s | 8 GiB | 63 s | 32 to 35 s | 3 to 6 s |
| runc, gp3 at 16,000 IOPS and 1,000 MiB/s | 28 GiB | 66 s | 18 to 24 s | 18 to 22 s |
| gVisor, gp3 at 3,000 IOPS | 8 GiB | 102 s | 185 s | Not measured |
| gVisor, gp3 at 16,000 IOPS and 1,000 MiB/s | 8 GiB | 104 s | 45 to 49 s | 1 to 2 s |

Each range covers five restores that applied packs in random order and in path order, with 4, 8, and 16 packs in flight. Neither the order nor the concurrency changed the result. The gp3 baseline of 3,000 IOPS is what limits this design's restore of 516,000 small files, and at 16,000 IOPS the restore took half the time of today's tar restore. A larger memory limit let more of the written data wait in the page cache, which shortened the restore and moved the same work into the flush.

Other configurations, gp3 at its baseline, 4 CPUs and 8 GiB:

| Configuration | Chromium | LLVM |
| :-- | --: | --: |
| runc | 172 s | 56 s |
| gVisor exclusive | 176 s | 62 s |
| Kata | 157 s | 72 s |
| Kata metadata | 137 s | 54 s |
| Kata metadata, metal | 40 s | 21 s |
| Kata block | 75 s | 53 s |
| Kata block, 2 CPUs | 83 s | 40 s |
| Kata block, metal | 73 s | 34 s |

Today's tar restore took 71 to 75 s for Chromium and 75 s for LLVM under runc, 102 s and 87 s under gVisor, and 234 s and 147 s under Kata on the same nodes. LLVM's 6.2 GiB in 190,000 files restored faster than tar on every configuration. Kata's shared file system on bare metal restored fastest. Its writes go through the host-side virtio-fs daemon into the node's page cache, outside the pod's memory limit, which matches the effect of the 28 GiB limit under runc.

### Kubernetes checks

These checks ran on 2026-11-06 on a single-node k3s cluster (kubelet 1.37.2) on `m8i.2xlarge`, using the same containerd, runc, gVisor, and Kata builds, with one RuntimeClass per configuration. The Kata block configuration set `emptydir_mode = "block-plain"` in a configuration file Kata ships, because Kata 4.3.1 refuses a `KATA_CONF_FILE` outside its shipped set.

Inotify across containers. A pod ran the tracker in one container and wrote from a second container into the shared `emptyDir`: 200 new files in a new directory, an append to an existing file, and a rename.

| Configuration | Workspace mount seen in the pod | Dirty paths reported |
| :-- | :-- | --: |
| runc | `xfs` from the node | 202 |
| gVisor | `9p` | 0 |
| gVisor exclusive | `9p` | 0 |
| gVisor, pod-share mount hint | `tmpfs` | 202 |
| gVisor exclusive, pod-share mount hint | `tmpfs` | 202 |
| Kata | `virtiofs` | 0 |
| Kata block-plain | `ext4` on `/dev/sda`, with `discard` | 202 |

Kubelet-updated volumes. Each pod mounted a ConfigMap, a Secret, a downward API volume with the pod's labels, and a projected volume with a service account token that expires after 600 s. The test changed the ConfigMap, the Secret, and a label, and read the files every 5 s.

| Configuration | ConfigMap, Secret, and label updates | Token refresh at 80% of its lifetime |
| :-- | :-- | :-- |
| runc | Seen after 11 s | Refreshed |
| gVisor | Seen after 11 s | Refreshed |
| gVisor exclusive | Not seen in 360 s | Not refreshed by 560 s |
| Kata | Seen after 11 s | Refreshed |

With `--file-access-mounts=exclusive` the sandbox kept serving the files it had cached, so a pod would read its expired token until it restarted.

`emptyDir` size limit. Each pod had an `emptyDir` with `sizeLimit: 1Gi`. It wrote 600 MiB, deleted it and wrote 600 MiB again, and then wrote 600 MiB more.

| Configuration | After 600 MiB | After delete and rewrite | After 1,200 MiB live |
| :-- | :-- | :-- | :-- |
| runc | 600 MiB on the node | 600 MiB | Evicted |
| gVisor | 898 MiB on the node | 600 MiB | Evicted |
| gVisor, pod-share mount hint | 600 MiB in `.gvisor.filestore.<id>` | 600 MiB | Evicted |
| Kata | 600 MiB on the node | 600 MiB | Evicted |
| Kata block-plain | 687 MiB in `disk.img` | 687 MiB | Evicted |

Every configuration was evicted with "Usage of EmptyDir volume "ws" exceeds the limit "1Gi"". The kubelet's usage figure trailed the node's disk usage by up to a minute on Kata and Kata block-plain, so eviction there came that much later. Under gVisor's default mount the node's usage read 898 MiB after 600 MiB had been written, the kubelet counted the 898 MiB, and the excess was gone after the rewrite. On Kata block-plain, a deleted file returned its space through `discard`, and the file system's metadata took 87 MiB of the limit.

gVisor's pod-share mount. The Chromium benchmark ran in Kubernetes under gVisor with 4 CPUs and 8 GiB, once on the default mount and once with the mount hints, with the tracker and the workload in one container.

| Measurement | Default mount (`9p`) | Pod-share mount (`tmpfs`) |
| :-- | --: | --: |
| Steady pause, walk / tracker | 8.70 s / 0.12 s | 2.75 s / 0.13 s |
| Package-install pause, walk / tracker | 14.75 s / 7.32 s | 7.45 s / 4.14 s |
| Parallel stat walk of 516,434 files | 8.43 s | 2.21 s |
| Tracker setup, wall / CPU | 9.5 s / 35.7 s | 2.7 s / 9.3 s |
| Create 50,000 files, without / with the tracker | 3.13 s / 5.41 s | 1.87 s / 2.95 s |
| Delete 50,000 files, without / with the tracker | 0.64 s / 0.80 s | 0.26 s / 0.50 s |
| Restore | 177 s | 56 s |
| Most host descriptors held by the sentry | 113,221 | 777 |

The first checkpoint on the pod-share mount in walk mode took 631 s, of which 577 s was spent waiting for uploads to S3 after the pre-copy read had finished in 40 s. The tracker-mode first checkpoint that followed uploaded the same bytes without waiting, so the stall came from the upload path rather than the mount. Its pause was 2.81 s.

### What the benchmarks do not cover

- A Full-level runtime's own time to quiesce, which adds to every pause.
- A multi-node cluster, and the adapter and runtime images Lenny ships. The Kubernetes checks used generic images on one node.
- Clouds other than AWS, and Arm nodes.
- Upload through presigned URLs and through the gateway's grant protocol.

## Levers

The pause, its frequency, and the cost of checkpoints depend on settings held by the deployer, the operator, the runtime author, and the tenant. The deployer installs the chart and owns the nodes. The operator defines pools and grants them to tenants. The runtime author writes the Runtime definition and the runtime. The tenant chooses among the runtimes and pools it has access to and, under `tenant-derived-runtimes-design.md`, derives its own runtimes. The measured effects come from "Benchmarks".

### Whether to checkpoint and how often

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| `checkpointPolicy: sealOnly` or `none` (IC-8) | Operator on the pool, runtime author on the Runtime, tenant on a derived runtime | No checkpoint runs during the session, so the runtime never pauses for one. | The pause and its upload are removed. | A pod failure or eviction resumes the session without its workspace. Under `none` the session also has no final snapshot. |
| `periodicCheckpointIntervalSeconds` (IC-8) | Operator on the pool, runtime author on the Runtime, and tenant on a derived runtime at or above the base's interval, all within the deployer's `checkpoint.minIntervalSeconds` and `checkpoint.maxIntervalSeconds` | Sets how often the pause occurs. | The share of time paused is the pause divided by the interval. For Chromium under gVisor that is 8.6 s in 600 s, or 1.4%, with the stat walk, and 0.11 s in 600 s, or 0.02%, with the tracker. At a 60-second interval the walk pause is 14% of the time. | A longer interval loses more work on a pod failure. |
| Integration level | Runtime author | A runtime that leaves `"checkpoint"` out of `lifecycle_support` is captured on the best-effort path while it runs, with no pause. A Full-level runtime pauses for a consistent snapshot. | A best-effort capture has no pause. | The snapshot is tagged `consistency: best-effort`, and a file written during the capture is flagged `torn`. |

### Where the session runs

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| A runc pool granted to one tenant | Operator: a pool with `isolationProfile: standard`, granted with `lenny-ctl admin pools grant-access`, on nodes tainted for that tenant | Moves the tenant's sessions to the profile with the lowest pause, and makes `FICLONE` available for large changed files on XFS nodes. | Chromium's walk pause is 1.09 s against 8.59 s under gVisor and 6.40 s under Kata, and a 4 GiB file under constant writes is captured consistently in 2.0 to 2.5 s. | runc isolates with cgroups and namespaces only, and [§5.3 Isolation Profiles](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles) restricts `standard` to development and testing. The pool needs `allowStandardIsolation: true`, the deployer's explicit opt-in with a security acknowledgment, and §5.3 states that runc gives no protection against kernel exploits and that gVisor is the minimum for workloads that process untrusted input, which includes LLM-generated code. The lever therefore applies only where the deployer accepts that restriction, and it changes no rule of §5.3. |
| CPU allocation | Operator, through the pool's resource class | The capture runs on the pod's CPUs. | 2 CPUs in place of 4 lengthened the steady walk pause by 11 to 43% and the gVisor package-install pause by 1.6 to 2.4 times. | Node capacity. |
| Node disk | Deployer | Affects the first checkpoint and restore, which read or write the whole workspace. | A gp3 volume at 16,000 IOPS and 1,000 MiB/s restored Chromium in 32 to 35 s under runc and 45 to 49 s under gVisor, against 159 to 168 s and 185 s at the gp3 baseline of 3,000 IOPS and 125 MiB/s. Local NVMe made Chromium's first checkpoint under runc 22 s against 31 s on gp3. Neither changed the pause. | gp3 charges for IOPS above 3,000 and throughput above 125 MiB/s, and NVMe instance types cost more. |

### Node configuration for gVisor

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| Pass `dev.gvisor.*` pod annotations to runsc | Deployer, with `pod_annotations = ['dev.gvisor.*']` on the containerd runtime handler for gVisor | Lets the mount hints that the pod controller renders on every pod of a pool with `isolationProfile: sandboxed` reach runsc, so the workspace becomes one sentry-owned mount shared by the pod's containers. Without it the adapter sees `9p` and walks. | In Kubernetes, Chromium's steady pause fell from 8.70 s to 2.75 s with the walk and to 0.13 s with the tracker. The package-install pause fell from 14.75 s to 7.45 s with the walk and from 7.32 s to 4.14 s with the tracker. Tracker setup fell from 9.5 s to 2.7 s, and Chromium restored in 56 s against 177 s. The sentry held at most 777 host descriptors. | The workspace is stored on the node as one `.gvisor.filestore.<id>` file, so the node cannot read its files. Managed gVisor offerings that do not pass the annotations keep the walk. |
| `--file-access-mounts=exclusive` | Do not set | Lets the sentry cache file metadata for every mount of the sandbox. | The stat walk was 30 to 32% faster. In Kubernetes, ConfigMap, Secret, and downward API updates never reached the sandbox, and the projected service account token was not refreshed, so a pod keeps an expired token. | Ruled out. The pod-share mount hint gives a larger gain on the workspace volume alone. |
| `--platform=kvm` | Deployer | Replaces systrap with KVM for system call interception. | The stat walk was 39 to 43% slower on bare metal and 2.6 times slower under nested virtualization, and tracker setup was 37 to 39% slower. | Not recommended for checkpointing. It requires bare metal or nested virtualization. |

### Node configuration for runc

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| `fs.inotify.max_user_watches` and `fs.inotify.max_user_instances` | Deployer, with a sysctl on runc nodes | Raises the watch and instance budget that every adapter on the node shares, because all of them run as one host UID. | The measured 32 GiB nodes allowed 255,070 watches and 128 instances, so five Chromium-sized workspaces, or 128 trackers, exhaust a node. Past that, trackers fall back to the walk, at 1.09 s for Chromium in place of 0.12 s. | Kernel memory for the watches. The kernel's default gives the watch limit 1% of memory, about 1.3 KiB per watch. |

### Node configuration for Kata

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| `kernel_params = "... sysctl.fs.inotify.max_user_watches=1048576"` | Deployer, in the Kata `configuration.toml` | Raises the guest's watch limit, which a 2 GiB guest sets to 15,514. It matters together with `block-plain`, because the tracker runs only there. | With the higher limit, Chromium's 46,969 watches succeeded. | Guest kernel memory for the watches. |
| `emptydir_mode = "block-plain"` | Deployer, in the Kata `configuration.toml` | Places each `emptyDir` on a block device with an `ext4` file system inside the guest, mounted with `discard`, in place of the shared file system. It is the only Kata mode on which the change tracker sees the agent container's writes. | The lowest Kata pause measured: 1.34 s with the walk and 0.16 s with the tracker for Chromium, and about the same as runc for LLVM. Tracker setup fell from 8.4 s to 1.1 s. Chromium restored in 75 s against 157 s on the shared file system. In Kubernetes the kubelet evicted a pod that exceeded its `emptyDir` `sizeLimit`, and deleted files returned their space to the node. | The host cannot read the workspace. The backing file, `disk.img`, has an apparent size of 100 GiB, so `df` inside the guest reports space the `sizeLimit` does not grant. The `ext4` metadata took 87 MiB of the limit. `block-encrypted` is the variant with encryption at rest. |
| `virtio_fs_cache = "metadata"` | Deployer, in the Kata `configuration.toml` | Lets the guest cache file metadata from the shared file system. | The stat walk was 2.7 to 3.2 times faster than the default, and reading small changed files was about 2 times slower under nested virtualization. `always` measured the same. | The guest sees changes made from the host side of a shared volume later. |
| Bare-metal nodes | Deployer | Removes nested virtualization. | The Kata walk pause was up to 31% lower. | Instance type and price. |

### The workspace and the pause budget

| Lever | Set by | Effect | Measured effect | Cost |
| :-- | :-- | :-- | :-- | :-- |
| `checkpointExcludes` ([D5](#decisions)) | Runtime author on the Runtime, where a change creates a revision that shares its pod set, and tenant on a derived runtime, appended to the selected base revision's list (IC-8, "Runtime fields and revisions") | Leaves regenerable directories such as `node_modules/` and build outputs out of every snapshot. | The stat walk and tracker setup scale with the number of files and directories, at the per-file costs in "Benchmarks". | The excluded files are absent after a restore and must be regenerated. |
| Git maintenance off ([D6](#decisions)) | Platform default in the injected git configuration ([`egress-and-git-design.md`, "Git configuration"](egress-and-git-design.md#git-configuration)), once that design adopts D6, which this design requests of it. Until then the lever does not exist. | Keeps automatic `git gc` from rewriting packfiles. | After a repack, the next checkpoint uploaded the repository's history again: 16 to 54 s for LLVM and Linux against 1 to 10 s for a steady checkpoint. | Repositories grow loose objects until something repacks them. |
| `checkpointPauseBudgetSeconds` (default 10) and `checkpointPreCopyRounds` (default 3) on the pool | Operator on the pool, below 60 s | Bounds the pause. A capture estimated over the budget takes another pre-copy round, and after the last round, or at once when the walk alone exceeds the budget, it runs on the best-effort path. | At 10 s, Chromium's 8.6 s walk under gVisor without a tracker fits, and so does every package-install pause with the tracker on 4 CPUs, the longest of which was 9.4 s. The package-install pauses under gVisor without a tracker, 10.4 to 19.9 s, do not fit. A 4 GiB file under constant writes went to the best-effort path with no pause on gVisor and Kata. | A lower budget produces more best-effort snapshots. A higher one allows longer pauses, and an interrupt waits until the pause ends. |
| `checkpointChangeTracker` | Operator on the pool | Removes the stat walk from the pause. | The steady pause was 0.03 to 0.16 s on every profile. | The tracker's memory and CPU, and 10 to 128% longer file creation and deletion in the workload (see "Benchmarks"). |

## Node-level snapshots

Node-level copy-on-write snapshots give an atomic capture whose cost does not depend on file count. They need a per-pod volume on a snapshot-capable filesystem. One possible route is generic ephemeral volumes from a local CSI (Container Storage Interface) driver that offers `VolumeSnapshot` on ZFS or Btrfs, in place of `emptyDir`. The capture pass would become a snapshot request, and an uploader with access to a clone of the snapshot would perform the upload pass.

This design defers that route for these reasons:

- It requires node pools on a particular filesystem and a privileged CSI driver.
- Warm pools would move from `emptyDir` to per-pod volumes, which adds provisioning time to every warm pod.
- A volume holds every slot of a multi-session pod, so the snapshot covers more than one session.
- The uploader needs a mount of the snapshot that the agent pod does not have today.

The adapter's capture step is an internal interface with operations to fix the changed set at one instant and to read that fixed data, so a node-level provider can replace the `FICLONE` and read paths later.

## Relationship to other designs

- **[`egress-and-git-design.md`](egress-and-git-design.md)** moves git into the pod, under its proposal [EG-3](egress-and-git-design.md#proposal-split) (the pod git path), and injects git configuration ([`egress-and-git-design.md`, "Git configuration"](egress-and-git-design.md#git-configuration)). [Decision D6](#decisions) asks that design to add maintenance settings to that configuration. Its [EG-2](egress-and-git-design.md#proposal-split) renders the workspace volume's `sizeLimit`, whose value IC-7 changes.
- **[`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md)** keeps warm repository stores (["Warm clones"](tenant-derived-runtimes-design.md#warm-clones)) outside the slot and reconnects them at restore. This design does not capture those stores, and it reduces what a session's own clone costs after the first checkpoint. Its "Fields" table states what a tenant may set on a derived runtime, and it does not list this design's Runtime fields yet.
- **[`session-derive-design.md`](session-derive-design.md)** keeps its routes and modes. This design changes how a derive with `from: latest` stores its starting point, which changes what that design's storage reservation covers.
- **[`runtime-revisions-design.md`](runtime-revisions-design.md)** classifies this design's Runtime fields, as IC-8, "Runtime fields and revisions" states.
- **[`message-delivery-design.md`](message-delivery-design.md)** copies a file the agent produces for the user with this design's chunks, packs, grants, catalog, and reclamation ([MD-18](message-delivery-design.md#md-18-files)), under the owner's decisions A12 to A21 of that design. IC-2 gives every pack a nonce, IC-3 adds its `ExportOutputFile` stream with `ExportAbort`, IC-4 its output-file tables and export records, allowed set, and export quota, IC-5 the seal window that the seal checkpoint and the session's exports share and the rendering of the exports' downloads, IC-6 its retention and holds, and IC-7 the export share of the spool. The checkpoint also records that design's consumed message-id set, which IC-4 makes readable on resume.
- **[`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md)** treats a compromised pod's checkpoints as suspect (["Sessions on the pod"](forensics-evidence-preservation-design.md#sessions-on-the-pod)). S2 and S4 limit what such a checkpoint can reference or substitute. They do not make its content trustworthy. Its security holds copy held snapshots to escrow and require `EnsureCopied` on every deletion path, which IC-6, "Security holds" states for packs, snapshots, and output files. Its quarantine relies on IC-9, which removes the only specification rule that asks for a liveness probe on an agent pod. Such a probe would have the kubelet kill an embedded container that a node-side freeze stops.

## Changes requested of other designs

| Design | Request | Why |
| :-- | :-- | :-- |
| [`egress-and-git-design.md`](egress-and-git-design.md) | Add `gc.auto=0` and `maintenance.auto=false` to the git configuration under ["Git configuration"](egress-and-git-design.md#git-configuration) ([D6](#decisions)). | A repack rewrites packfiles, and the next checkpoint uploads the repository's history again (IC-1, "Git repositories"). |
| [`session-derive-design.md`](session-derive-design.md) | Under **Order.** in ["What it refuses"](session-derive-design.md#what-it-refuses), state that a derive with `from: latest` copies no object and reserves nothing, so the storage reservation and `undoDerivedWrites` cover only the uploads a derive with `from: start` copies. Add `checkpointing_disabled` to the table of `DERIVE_SOURCE_UNAVAILABLE` reasons, for a derive with `from: latest` of a source whose effective `checkpointPolicy` is `none`. | IC-5, "Derive", and IC-8, "Policy". |
| [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) | Add rows to "Fields" for `checkpointPolicy`, restrict-only, which a tenant may set only to a value stricter than the base's, for `periodicCheckpointIntervalSeconds`, which a tenant may set only at or above the base's live interval, and for `checkpointExcludes`, appended to the selected base revision's list, which a tenant may set. | IC-8, "Policy", and IC-8, "Runtime fields and revisions". |

## Costs

- **Postgres.** Each attempt writes one row per new pack and one row per referenced pack. A 5 GiB workspace in 16 MiB packs references about 320 packs per snapshot.
- **Adapter CPU.** SHA-256 ran at 1.1 to 1.7 GiB/s on one core of the measured x86 nodes and at 2.6 GiB/s on the arm64 laptop. zstd at its fastest level compressed at 0.85 to 2.0 GiB/s on one core. The tracker adds 20 to 60 µs of CPU per file system event (see "Change tracker").
- **Adapter memory and disk.** The file index holds about 128 bytes per file, so 500,000 files take about 64 MiB. The adapter stores the index as a file under `/workspace/staging/checkpoint/`. The spool needs up to `checkpointCaptureBudgetBytes` of disk on the workspace volume. The tracker used up to 480 MiB during setup and 35 to 130 MiB afterwards for 17,000 to 47,000 watched directories, and the pod's memory limit has to leave room for it.
- **Complexity.** The design adds a storage format, a catalog with reference-based garbage collection, and a format conversion for the download route. It removes the partial-manifest reassembly path and the tiered drain table.
- **Storage.** Retained bytes fall to the unique content of the latest 2 snapshots and of the session's output files, plus sparse-pack overhead bounded by `packRewriteThreshold`. An output file keeps every pack it references alive for the session's retention, so a sparse pack that a rotated snapshot shared with an output file is not compacted away until the output file goes, and its unused chunks are stored for that time.

## Defects found while verifying

These defects exist in the tree today, and none is filed in `BUILD-GAPS.md` yet. Some of them are worth fixing before this design lands.

| Defect | Location | Severity |
| :-- | :-- | :-- |
| The workspace `emptyDir` has no `sizeLimit`, and the adapter's limit defaults to 0, which disables the size probe. [§4.4.4 Checkpoint SLOs and Scheduling](../spec/04_system-components.md#444-checkpoint-slos-and-scheduling) **Hard workspace size limit.** requires both, with a 512 MiB default. [EG-2](egress-and-git-design.md#proposal-split) closes the volume's half, and IC-7 sets the value. | `podVolumes` in `pkg/controller/sandbox/podspec/volumes.go`; `--workspace-size-limit-bytes` in `cmd/lenny-adapter/main.go` | High |
| The `SIGSTOP` checkpoint path has no production caller, while F-4.4.7, a finding filed in `BUILD-GAPS.md` at the repository root, is marked closed. IC-9 removes the path, so the finding's subject goes. | `pkg/adapter/embeddedcheckpoint` | High |
| A best-effort archive fails the attempt when a file grows or disappears during the archive. | `archiveRoot` in `pkg/adapter/workspace/tree.go` | Medium |
| An interrupt waits for the whole checkpoint upload, because the operation lock spans the upload. | `Checkpoint` in `pkg/adapter/checkpoint.go` | Medium |
| `SweepDue` runs due checkpoints one at a time. | `pkg/gateway/checkpoint/checkpointer/schedule.go` | Medium |
| A restore stops at a fixed 2 GiB, whatever `workspaceSizeLimitBytes` allows. | `maxExtractBytes` in `pkg/adapter/workspace/extract.go` | Medium |
| The pool-config webhook admits a `workspaceSizeLimitBytes` above 512 MiB and gives it the 90-second cap. | `MaxTieredCheckpointCapSeconds` in `pkg/admission/pool_config_validator/validator.go` | Low |
| Periodic attempts send `deadline_ms = 0`, which tells the runtime it has no time to quiesce. | `CheckpointStart` in `pkg/gateway/checkpoint/checkpointer/checkpointer.go` | Low |
| §4.4.4 recommends `.lennyignore` excludes for checkpoints, and checkpoints ignore that file. | `pkg/workspacepack` honors it for client uploads only | Low |
| The adapter sends `checkpoint_request` to a runtime whose `lifecycle_support` leaves out `"checkpoint"`. The interrupt, credential-rotation, and deadline paths each check the runtime's declared capability, and the checkpoint path does not. | `Checkpoint` in `pkg/adapter/checkpoint.go` | Medium |
| A `gitClone` source holds the whole archive in gateway memory, and no byte cap was found in `pkg/gateway/provisioning/gitref`. | `CloneArchive` in `pkg/gateway/provisioning/gitref/archive.go` | Medium |
| The operator guide's pool example sets `checkpointPolicy: {enabled: true, intervalSeconds: 600}`, and the configuration reference lists `pool.checkpointing.enabled`, `pool.checkpointing.interval`, `pool.checkpointing.workspaceSizeLimitBytes`, and a Runtime field `capabilities.checkpoint`. No spec section, schema, or code defines any of them, so an operator who sets `enabled: false` expects checkpoints to stop and they do not. | `docs/operator-guide/configuration.md` (Pool Configuration example); `docs/reference/configuration.md` (Checkpointing configuration) | Medium |
| The spec contradicts itself on Basic-level checkpoints. [§4.4.3](../spec/04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling) **Checkpoint quiescence strategy by level:** gives Basic and Standard runtimes a best-effort snapshot without pausing. The [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels) table row **Checkpoint / restore** and the Basic-level paragraph of [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod) state that Basic has no checkpoint support. The runtime author guide follows §15.4.3. [Decision D12](#decisions) resolves it. | `spec/04_system-components.md`, `spec/15_external-api-surface.md`, `spec/28_communication-channels.md`, `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/integration-levels.md` | Medium |
| The troubleshooting guide answers a long checkpoint with "Reduce `workspaceSizeLimitBytes`; use `.lennyignore`". Checkpoints ignore `.lennyignore`, and lowering the size limit evicts sessions whose workspace is larger. | `docs/operator-guide/troubleshooting.md` (Checkpoint Failures) | Low |
| The architecture overview describes a checkpoint as a tar of `/workspace/slots/{sessionId}/current`, and the observability guide states a P95 checkpoint duration objective of 2 s at 100 MB, measured from quiescence through upload. Both follow the current method, and both change under this design. | `docs/getting-started/concepts.md`, `docs/operator-guide/observability.md` | Low |

## Specification sections affected

- [§4.4.2 Checkpoint Atomicity and Partial Manifests](../spec/04_system-components.md#442-checkpoint-atomicity-and-partial-manifests): the atomic unit becomes a snapshot row with its pack references, and partial manifests are removed.
- [§4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling](../spec/04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling): the passes, the pause budget, convergence, best-effort tolerance, and the meaning of `checkpoint_complete`. The section states the capture constants `reflinkMinBytes` (1 MiB), `racyWindow` (2 s), and `bestEffortRereads` (2). IC-9 removes **Embedded adapter mode only:**, the embedded adapter path of **Checkpoint timeout and recovery:**, **SIGCONT confirmation.**, the `SIGCONT` sentence of **Checkpoint abort cleanup:**, and **Embedded adapter liveness integration:**, with `checkpointStuck` and `watchdogTimeoutSeconds`.
- [§4.4.4 Checkpoint SLOs and Scheduling](../spec/04_system-components.md#444-checkpoint-slos-and-scheduling): the SLO measures the pause, the size limit bounds disk and storage, and scheduling and freshness use each session's effective interval. The section states that the gateway runs up to `checkpointConcurrency` due attempts at once per replica. IC-9 removes the embedded adapter sentence, the `SIGSTOP` clause of **Hard workspace size limit.**, and the embedded-adapter scope of `CheckpointDurationHigh`.
- [§4.7.2 Checkpoint and Interrupt Mutual Exclusion](../spec/04_system-components.md#472-checkpoint-and-interrupt-mutual-exclusion): **Interrupt during checkpoint:** loses its `SIGSTOP` rationale (IC-9).
- [§4.4.5 Checkpoint Storage Failure and Eviction Fallback](../spec/04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback): retry budgets apply per pack.
- [§4.5 Artifact Store](../spec/04_system-components.md#45-artifact-store) **Workspace lineage:**: snapshot identity and lineage.
- [§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service): checkpoint-and-restart rotation under `checkpointPolicy: sealOnly` and `none`.
- [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime) and [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): `checkpointPolicy` and `periodicCheckpointIntervalSeconds` on both, the resolution rules, and the pool admission `Warning`. §5.1 also gains `checkpointExcludes`, with a tenant's entries appended to the base revision's list, and the runtime field `maxWorkspaceSealDurationSeconds`, which overrides the pool's seal window within `gateway.workspaceSealMaxDurationCeilingSeconds`, and §5.2's pool admission refuses a pool value above that ceiling ([A19](message-delivery-design.md#decisions)).
- [§4.7 Runtime Adapter](../spec/04_system-components.md#47-runtime-adapter) and [§28](../spec/28_communication-channels.md#28-communication-channels): `FinalizeWorkspace` and `Resume` carry the session's `checkpointExcludes` list, and the adapter leaves the listed directories out of every capture of that session.
- [§4.7 Runtime Adapter](../spec/04_system-components.md#47-runtime-adapter): the `ExportOutputFile` RPC beside `Checkpoint` with `ExportAbort`, the pack nonce in every pack footer, the freeze of a file part when the adapter reads it with the per-session cap on open export descriptors, which the adapter's count holds authoritatively with `unopened: true` on a part beyond it and the descriptor freed at the end of the export or at `output_failed`, the dedup check of each chunk when it is packed, the export share of the spool with the clones of waiting parts counted in it and a file over the remaining share read in place, the cancellation of the previous generation's open `Checkpoint` and `ExportOutputFile` streams when the adapter acknowledges a fence, the pause of export reading, hashing, and compression during a capture pass, the confirm-before-share rule of the dedup set, the eviction after `ReferenceRefused`, and the output-file chunk lists and leased packs on `Resume` and `CheckpointStart`.
- [§28.3 Registers](../spec/28_communication-channels.md#283-registers), [§28.4 Claim register](../spec/28_communication-channels.md#284-claim-register), [§28.5.1 Gateway-to-pod](../spec/28_communication-channels.md#2851-gateway-to-pod), [§28.5.5 Pod-egress](../spec/28_communication-channels.md#2855-pod-egress), and [§28.6 Exclusivity and concurrency model](../spec/28_communication-channels.md#286-exclusivity-and-concurrency-model): the `CH-OUTPUTFILE` register row and contract card with generation fencing, its claim-register row, the `CH-OBJSTORE` row amended for export packs and the per-session window, and the statement that the pod-level operation lock bounds the transfer `CH-OBJSTORE` carries amended, because an export takes no operation lock.
- [§10.1 Horizontal Scaling](../spec/10_gateway-internals.md#101-horizontal-scaling), [§10.1.2 Coordinator Handoff Protocol](../spec/10_gateway-internals.md#1012-coordinator-handoff-protocol), and [§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain): `checkpointGrantWindow` counted per session across both streams on the coordinating replica, the new coordinator's count of the whole window as outstanding until `checkpointCapabilityTTLSeconds` has passed since the pod acknowledged its fence, the adapter's cancellation of the previous generation's open streams at that acknowledgement, the per-pool override's maximum of 8 and the gateway flag's refusal above 8 at startup, the drain order in which the eviction checkpoint runs before exports with no export grant signed while it runs, and the statement that an export stream takes no part in the stream-drain stage.
- [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): `checkpointChangeTracker` and `checkpointTrackerMaxWatches`, and `exportTimeoutSeconds`, counted from `ExportStart`, with the admission refusal of a pool unless `packLeaseSeconds` is at least twice the larger of its `checkpointAttemptTimeoutSeconds` and `exportTimeoutSeconds`.
- [§5.3 Isolation Profiles](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles): the node configuration that lowers the pause on nodes that run pools with `isolationProfile: sandboxed` or `microvm`, stated as deployer guidance: passing `dev.gvisor.*` annotations to runsc and the effect of `--file-access-mounts=exclusive` on kubelet-updated volumes for gVisor, and the Kata guest watch limit and `emptydir_mode`.
- [§6.4 Pod Filesystem Layout](../spec/06_warm-pod-model.md#64-pod-filesystem-layout): the pod controller renders the gVisor pod-share mount hint annotations for the workspace volume on every pod of a pool with `isolationProfile: sandboxed`.
- [§6.2 Pod State Machine](../spec/06_warm-pod-model.md#62-pod-state-machine): idle suspension and the `maxSuspendedPodHoldSeconds` release under `checkpointPolicy: sealOnly` and `none`.
- [§6.4 Pod Filesystem Layout](../spec/06_warm-pod-model.md#64-pod-filesystem-layout): the workspace `sizeLimit` and the spool directory.
- [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow): the seal, **Derive copy semantics:**, and **Derive restore semantics:**. Under **Seal-and-export invariant:** the seal window is the session's effective `maxWorkspaceSealDurationSeconds`, the runtime revision's value over the pool's, the seal checkpoint runs first with no export grant signed while it runs, and the session's exports finish in the time it leaves in the same window, the whole window under `checkpointPolicy: none`, before `seal_timeout`. A session's terminal pipeline budget stays its seal window plus the release allowance, and the session row's `post_terminal_until` records it at the terminal write (A22 of [`message-delivery-design.md`](message-delivery-design.md#decisions)). The seal window resolves the revision's value, then the pool's, then the gateway flag, each at most the ceiling (A23). Under **Terminal pipeline context:** the budget the deployment checks read becomes `gateway.workspaceSealMaxDurationCeilingSeconds` plus the release allowance. The runtime field, its ceiling, the claim orphan timeout's computed default, and the readers of the seal window are stated in [`message-delivery-design.md`, "Specification changes"](message-delivery-design.md#specification-changes).
- [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume): the restore steps, the restore limit, and the adapter's `restoreConcurrency`.
- [§10.1.7 preStop Hook Drain](../spec/10_gateway-internals.md#1017-prestop-hook-drain): the single drain cap, and removal of the chunked tar model and its reassembly.
- [§10.1.8 Rolling Updates and the CheckpointBarrier Protocol](../spec/10_gateway-internals.md#1018-rolling-updates-and-the-checkpointbarrier-protocol): the barrier hold ends after the capture pass.
- [§11.2 Budgets and Quotas](../spec/11_policy-and-controls.md#112-budgets-and-quotas) and [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store): quota per pack, reference-based garbage collection, compaction, and **Workspace deduplication.** §12.5 states the compaction constant `packRewriteThreshold` (0.5). In §11.2, **Hard cap on checkpoint writes.** clamps each declared pack length to `packMaxBytes` in place of `chunk_size_bytes`. §11.2 gains the export reservation, held on the `output_file_exports` row and released by its guarded update of `reservation_released_at` under **Reservation release is exactly-once and guarded.**, with `coordination_generation = $gen` in the guard of every release and state transition, its outstanding term in `Rehydrate` under **The relative decrement is rebuild-safe.**, the draw-down by each confirmed pack, the signing-time headroom check per export, the release of the reservation on every terminal outcome, a new coordinator's takeover without a second reservation, `files.maxConcurrentExports`, and `files.maxQueuedExports`. §12.5 gains `output_files`, `output_file_packs`, and `output_file_exports`, the backstop's release of an export reservation of a terminal session, liveness over both reference tables, a `referenced` pack leaving the allowed set when its last referrer goes, the pack key `(tenant_id, owner_session_id, pack_id)`, the refusal to revive a soft-deleted pack row, `packLeaseSeconds` and its extension, the `FOR SHARE` lock of the reference closure, and the `FOR UPDATE` re-check before a soft delete.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces): erasure and legal hold over shared packs, and the legal-hold escrow of a pack-backed item, a snapshot or an output file, as every pack it references and its row.
- [§13.2.3 Checkpoint Capability Model](../spec/13_security-model.md#1323-checkpoint-capability-model): the pack key layout, signed `Content-MD5`, `GET` scoping by snapshot, and the constant `packMaxBytes` (32 MiB). The pack nonce makes every pack ID an adapter builds distinct. The gateway refuses a grant for a pack larger than `packMaxBytes` and for a pack key its catalog already holds in any state, soft-deleted rows included, resolves a pack ID against the session's own key first, and answers "already stored" for a pack in the session's allowed set, consulting another session's packs in that set only for that answer. The residual exposure becomes the pool's effective `checkpointGrantWindow` times `packMaxBytes`, counted per session across the `Checkpoint` and `ExportOutputFile` streams, 128 MiB by default and at most 256 MiB, and the paragraph names today's bound of 4 × 16 MiB in place of stating that the exposure is unchanged.
- [§15 External API Surface](../spec/15_external-api-surface.md#15-external-api-surface): `GET /v1/sessions/{id}/workspace` keeps its contract and returns 404 under `checkpointPolicy: none`, the derive refusal gains the `checkpointing_disabled` reason, and the session envelope reports the effective `checkpointPolicy`.
- [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels) and [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod): the Basic-level checkpoint statement, as decision D12 resolves it.
- [§16 Observability](../spec/16_observability.md#16-observability): the new metrics including the tracker's, the redefined `CheckpointDurationHigh` alert without its embedded-adapter scope, a pause objective in place of the duration objective, and `CheckpointStale` leaving out sessions under `checkpointPolicy: sealOnly` and `none`.
- [§17.8.1 Operational Defaults — Quick Reference](../spec/17_deployment-topology.md#1781-operational-defaults--quick-reference): a row for `gateway.checkpointConcurrency` (16) beside the `checkpointGrantWindow` row, a row for `controller.adapterRestoreConcurrency` (8), the `checkpointGrantWindow` row restated as per session with a per-pool maximum of 8 and the gateway flag's maximum of 8, and rows for `packLeaseSeconds` (1800) with its admission rule, `exportTimeoutSeconds` (900), `files.maxConcurrentExports` (2), `files.maxQueuedExports` (8), and `gateway.workspaceSealMaxDurationCeilingSeconds` (600).
- [§18 Build Sequence](../spec/18_build-sequence.md#18-build-sequence): the checkpoint benchmark measures the pause and the new-byte rate, and the `SIGSTOP`/`SIGCONT` embedded-adapter deliverable is removed (IC-9).
- [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod): the `checkpoint_request` and `checkpoint_complete` rows for `CH-RUNTIMEOPS`.

## Stored formats and wire contracts this forces

| Surface | Change |
| :-- | :-- |
| `schemas/lenny-adapter.proto` | Pack messages on `Checkpoint`, `CaptureDone`, `SnapshotReady` with the consumed message-id set, `PackStored`, `ReferenceRefused` with the offending pack IDs, restore messages that name packs, and the output-file chunk lists and leased packs on `CheckpointStart` and `Resume`. The `ExportOutputFile` RPC, on the channel `CH-OUTPUTFILE`, with `ExportStart`, `ExportProbe`, `ExportReady`, and `ExportAbort` with its reason, and the pack messages it shares with `Checkpoint`. |
| `CH-RUNTIMEOPS` | `checkpoint_complete` confirms the capture, and `deadlineMs` carries the pause budget. |
| Postgres | `checkpoint_packs`, keyed on `(tenant_id, owner_session_id, pack_id)`, `checkpoint_snapshots` with `consumed_message_ids`, and `checkpoint_snapshot_packs`, which replace the partial-manifest table and the per-checkpoint chunk rows, and `output_files`, `output_file_packs`, and `output_file_exports` for output files. `output_files` keeps the whole-file `sha256` as metadata and has no verification column. |
| Object keys | `/{tenant_id}/checkpoints/{session_id}/packs/{pack_id}`. |
| Pack and manifest formats | Versioned binary formats, with codecs in the adapter and the gateway. The pack footer carries the 128-bit pack nonce. |
| Pool configuration | `checkpointPauseBudgetSeconds`, `checkpointCaptureBudgetBytes`, `checkpointPreCopyRounds`, `checkpointAttemptTimeoutSeconds`, `evictionCheckpointCapSeconds`, `checkpointPolicy`, `periodicCheckpointIntervalSeconds`, `checkpointChangeTracker`, `checkpointTrackerMaxWatches`, and `exportTimeoutSeconds`. The `SandboxTemplate.checkpointGrantWindow` override gains a maximum of 8 in its CRD validation. |
| Runtime definition | `checkpointPolicy`, `periodicCheckpointIntervalSeconds`, and `checkpointExcludes` ([D5](#decisions)). |
| Constants | `packMaxBytes` (32 MiB), `packRewriteThreshold` (0.5), `reflinkMinBytes` (1 MiB), `racyWindow` (2 s), `bestEffortRereads` (2), and `exportSpoolFraction` (0.5). The specification states them, and no setting changes them. The gateway refuses a pack grant whose declared length exceeds `packMaxBytes`. |
| Gateway settings | `LENNY_CHECKPOINT_CONCURRENCY`, read by `--checkpoint-concurrency`, 16 by default. `packLeaseSeconds`, 1800 by default, from the chart value `gateway.packLeaseSeconds`. `--checkpoint-grant-window` and `LENNY_CHECKPOINT_GRANT_WINDOW`, refused at startup above 8. |
| Controller and adapter flags | The controller flag `--adapter-restore-concurrency`, which the controller passes to the adapter of every agent pod it creates as `--restore-concurrency`, 8 by default. |
| Chart values | `checkpoint.minIntervalSeconds`, `checkpoint.maxIntervalSeconds`, `gateway.checkpointConcurrency`, `gateway.packLeaseSeconds`, and `controller.adapterRestoreConcurrency`. |
| Session envelope | The effective `checkpointPolicy`. |
| `FinalizeWorkspace` and `Resume` in `schemas/lenny-adapter.proto`, and the session record | The session's `checkpointExcludes` list, composed at the post-chain point and stored on the session record. |
| Metrics | `lenny_checkpoint_pause_seconds`, `lenny_checkpoint_new_bytes_total`, `lenny_checkpoint_precopy_rounds`, `lenny_checkpoint_torn_files_total`, `lenny_checkpoint_consistency_downgrade_total`, `lenny_checkpoint_tracker_state`, `lenny_checkpoint_tracker_fallback_total`, and `lenny_checkpoint_tracker_watches`. |

The platform has no deployments, so the old format gets no migration or dual-read path.

## Changes to Lenny's documentation

The documentation changes land with the spec changes, after the owner approves the design. Published pages cite no spec section numbers, so the pages below state behavior and settings directly.

### New page: `docs/operator-guide/checkpointing.md`

A new Operator Guide page, titled "Checkpointing", sits after "Observability" in the navigation. It covers these topics:

1. **How a checkpoint runs.** The pre-copy, capture, and upload passes, which pass pauses a Full-level runtime, why the first checkpoint of a session takes longer without pausing longer, and how a restore verifies what it writes.
2. **Consistency by integration level.** A Full-level runtime gets a consistent snapshot and pauses for the capture. Every other runtime gets a best-effort snapshot with no pause, as [decision D12](#decisions) settles. The platform never pauses a runtime by force.
3. **Benchmarks.** The steady and package-install pause tables for Chromium and LLVM, the per-file cost table, the tracker's effect on file creation and deletion, and restore times, from "Benchmarks" in this design. The page states the date, the node types, the component versions, and the harness's quiesce stand-in. It states that a deployer's own numbers depend on CPU, disk, and workspace layout, and it names the metrics that measure them in production.
4. **Operator levers.** `checkpointPolicy`, `periodicCheckpointIntervalSeconds` and the deployer's interval bounds, `checkpointChangeTracker`, `checkpointPauseBudgetSeconds`, `checkpointPreCopyRounds`, `checkpointCaptureBudgetBytes`, `workspaceSizeLimitBytes`, CPU allocation through the resource class, and a pool with `isolationProfile: standard` granted to one tenant on tainted nodes, each with its measured effect and its cost, as in "Levers". The page states that `standard` is restricted to development and testing and needs `allowStandardIsolation: true`, as the isolation profiles section of the specification states.
5. **Node configuration for deployers.** For gVisor, the containerd `pod_annotations = ['dev.gvisor.*']` setting that lets the workspace mount hints reach runsc, with the measured gain and the symptom of its absence (the tracker stays off and the walk pause applies). The statements that `--file-access-mounts=exclusive` stops kubelet updates from reaching the sandbox and must not be set, and that `--platform=kvm` makes checkpoints slower. For Kata, `emptydir_mode = "block-plain"`, which the tracker requires, `kernel_params` for the guest watch limit, and `virtio_fs_cache = "metadata"` for deployers who keep the shared file system. For runc, the `fs.inotify.max_user_watches` and `fs.inotify.max_user_instances` sysctls. Node disk choice and gp3 IOPS for the first checkpoint and for restore.
6. **Runtime author and tenant levers.** Declaring `"checkpoint"` in `lifecycle_support` or leaving it out, `checkpointPolicy` and `periodicCheckpointIntervalSeconds` on the Runtime, `checkpointExcludes` and the revision a change to it creates, a stricter `checkpointPolicy`, a `periodicCheckpointIntervalSeconds` at or above the base's interval, and appended `checkpointExcludes` on a derived runtime, and asking the operator for a dedicated pool.
7. **Monitoring.** `lenny_checkpoint_pause_seconds`, `lenny_checkpoint_consistency_downgrade_total`, `lenny_checkpoint_torn_files_total`, `lenny_checkpoint_new_bytes_total`, the tracker metrics, and what each alert means.

### Updates to existing pages

| Page | Change |
| :-- | :-- |
| `docs/operator-guide/configuration.md` | The pool example replaces the undefined `checkpointPolicy: {enabled, intervalSeconds}` block with `checkpointPolicy: full`, `periodicCheckpointIntervalSeconds: 600`, and `checkpointChangeTracker: auto`, and links to the checkpointing page. |
| `docs/reference/configuration.md` | The "Checkpointing configuration" table drops `pool.checkpointing.enabled`, `pool.checkpointing.interval`, and `pool.checkpointing.workspaceSizeLimitBytes`, and lists every pool and Runtime field in "Stored formats and wire contracts this forces" with its default and bounds, plus the chart values `checkpoint.minIntervalSeconds`, `checkpoint.maxIntervalSeconds`, `gateway.checkpointConcurrency`, and `controller.adapterRestoreConcurrency`. The Runtime row `capabilities.checkpoint` is replaced by a pointer to the `"checkpoint"` entry of `lifecycle_support`. |
| `docs/reference/metrics.md` and `docs/operator-guide/observability.md` | The checkpoint metrics tables add the new metrics. The "Checkpoint duration (100MB)" objective becomes a pause objective, as in §16. |
| `docs/alerting/rules.yaml` and `docs/alerting/openslo.yaml` | `CheckpointDurationHigh` and `CheckpointDurationBurnRate` measure `lenny_checkpoint_pause_seconds`. `CheckpointStale` leaves out sessions whose effective `checkpointPolicy` is `sealOnly` or `none` and measures each session against its own interval. |
| `docs/operator-guide/troubleshooting.md` | "Checkpoint Failures" drops the `.lennyignore` and size-limit advice. It adds best-effort downgrades and what lowers them, the tracker's state by file system type and the node setting each state points to, tracker fallbacks by reason, and a Full-level runtime that is slow to send `checkpoint_ready`. |
| `docs/runbooks/checkpoint-stale.md` | The runbook measures staleness against each session's effective interval, leaves out sessions whose effective `checkpointPolicy` is `sealOnly` or `none`, and explains that a large workspace's first checkpoint can span several attempts. |
| `docs/runtime-author-guide/lifecycle.md` | The "Checkpointing" section states the Basic-level behavior as D12 settles it. The Full-level example carries `deadlineMs` as the pause budget, `checkpoint_complete` arrives once the capture ends, and the section gives the measured pause range and links to the checkpointing page. It states that the platform does not pause a runtime that does not quiesce for a checkpoint, including an embedded binary (IC-9), and the quarantine freeze of [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#freezing-the-agent) stays a separate statement on that page, and it describes `checkpointExcludes` and `checkpointPolicy` on the Runtime. The sentence that a late `checkpoint_ready` sets a `checkpointStuck` health flag is removed, because IC-9 removes the flag. |
| `docs/runtime-author-guide/integration-levels.md` | The "Checkpoint and restore" and "Credential rotation" rows and the Basic-level paragraph match D12, and the handshake example's default deadline is the pause budget. The sentence that flags `checkpointStuck` after a late `checkpoint_ready` is removed (IC-9). |
| `docs/reference/adapter-contract.md` | `checkpoint_complete` means the snapshot is captured, and `deadlineMs` carries the pause budget. |
| `docs/getting-started/concepts.md` and `docs/getting-started/architecture.md` | The checkpoint description changes from a tar of the slot to incremental packs and a manifest, and the exception for checkpoint chunk objects to gateway-mediated file delivery, under which the pod reaches object storage directly with presigned grants, names packs. |
| `docs/api/internal.md` | The `Checkpoint` stream messages follow IC-3. |
| `docs/client-guide/session-lifecycle.md` | The session envelope reports the effective `checkpointPolicy`, and the page states which policies resume with `workspaceLost: true`. |
| `docs/operator-guide/namespace-and-isolation.md` | The isolation profile summary links to the checkpointing page for the checkpoint cost of each profile. |
| `docs/operator-guide/scaling.md` and `docs/reference/configuration.md` | `capacityPlanning.avgWorkspaceSizeMB` estimates the first checkpoint, and checkpoint bandwidth after it follows the rate of changed bytes. |
| `docs/reference/glossary.md` | Entries for pre-copy, capture pass, pause, change tracker, pack, and best-effort snapshot. |

## Tests

### Tier 1: unit

- Chunk boundaries are deterministic, and an insertion changes only nearby chunks.
- Pack and manifest codecs round-trip and reject truncated or reordered input.
- The racy rule re-reads an entry whose `mtime` falls inside the window.
- The reference closure check rejects a foreign pack, a pack from the source's later snapshot, and an expired `unreferenced` pack.
- Convergence: over budget, the adapter takes another round and falls back to best-effort after the last one.
- Policy resolution picks the stricter value, and a derived runtime cannot loosen its base's policy.
- Interval resolution prefers the pool, then the Runtime, then the gateway default, and clamps to the deployer's bounds.
- The pause estimate sums the change-source cost, the per-file cost, and the byte cost, adds the tracker backlog, and counts reflinked bytes at the clone cost.
- The tracker's swap returns every path changed before the barrier file, an overflow forces the next capture to walk, and a failed watch keeps the tracker degraded across swaps.
- The gateway's pack grant check refuses a declared length of zero or above `packMaxBytes` and accepts a length equal to `packMaxBytes`.
- The gateway reads `checkpointConcurrency` from `LENNY_CHECKPOINT_CONCURRENCY`, 16 when unset, and the controller's pod builder renders `--restore-concurrency` on the adapter from `--adapter-restore-concurrency`.
- `checkpointChangeTracker: auto` resolves from the workspace mount's file system type: to on for a node file system under runc, for `ext4` on a Kata block volume, and for `tmpfs` under gVisor's pod-share mount hint, and to off for `9p`, `virtiofs`, and any other `fuse` file system.
- A write to `checkpointPolicy` or `periodicCheckpointIntervalSeconds` creates no revision and reaches the next session on every revision in the traffic. A write to `checkpointExcludes` creates a revision with the same pod hash, and a session's capture leaves out the selected revision's list. A tenant's `checkpointExcludes` on a derived runtime is appended to the selected base revision's list. A resume, including one that moves revision, sends the stored list.
- A tenant's `periodicCheckpointIntervalSeconds` below the base's interval is refused at write, one at or above it is accepted and sets the session's interval, and a later base change that raises the base's interval above the tenant's gives the next session the base's interval.
- `pkg/checkpoint`'s `Level` has no `embedded` value, and no adapter code path sends `SIGSTOP` to the agent for a checkpoint (IC-9).
- Packs: two packs built from the same chunks carry different nonces and pack IDs, and the nonce round-trips through the pack codec.
- Exports: a resend of a pack in the session's allowed set after a lost `PackCommitted` receives `PackStored` and no grant, a file chunked again after a failure gets new pack IDs and new grants, a retry references the chunks of confirmed packs under lease and rebuilds no pack, the gateway resolves the session's own key before the allowed set's packs another session owns and answers those only with `PackStored`, and the same pack ID under another session's key outside the allowed set receives a grant for the writing session's own key. A `referenced` pack whose last snapshot rotated away and that no output file references is outside the allowed set, and a snapshot that still references it receives `ReferenceRefused`. A chunk enters the dedup set only after `PackCommitted`, and `ReferenceRefused` evicts the named packs' chunks. `ExportReady` is refused for non-contiguous offsets, a chunk over 4 MiB, a chunk length that differs from the pack footer, and a total that differs from `ExportProbe` or exceeds the limit. The export reservation is drawn down by each confirmed pack, the signing-time headroom check refuses a grant past the reservation and the headroom when the probe reported zero new bytes, and success, failure, `exportTimeoutSeconds`, `seal_timeout`, `REJECT`, and pod loss each release the rest exactly once through the guarded update of `reservation_released_at`, also when two of them race. `Rehydrate` adds `reserved_bytes − confirmed_bytes` over the unreleased `output_file_exports` rows. A new coordinator takes over an unreleased reservation without reserving again. `exportTimeoutSeconds` counts from `started_at`, and a part waiting in the export queue fails at the same span from `first_frame_at`. At the seal, no export grant is signed while the seal checkpoint runs, an export that continues after it references the chunks the seal confirmed, each part still exporting or waiting at the end of the session's seal window fails with `seal_timeout`, and under `checkpointPolicy: none` the exports get the whole window. An adapter at its descriptor cap relays a further part with `unopened: true`, the gateway refuses exactly the marked parts with `too_many_exports`, and the adapter frees a waiting part's descriptor and clone at its `output_failed`. An update of an export record that carries another generation changes nothing. `ExportAbort` carries `pack_empty`, `pack_too_large`, and `pack_key_held` for the matching `PackReady`. `packLeaseSeconds` is extended by `SnapshotReady`, `ExportReady`, and `PackStored`, and pool admission refuses a `packLeaseSeconds` below twice the larger of the attempt and export timeouts. Export clones stop at `exportSpoolFraction` of the spool cap, the clones of waiting parts count against it, and a file over the remaining share is read in place without a clone. The grant window counts both streams per session, honors the pool override up to 8, refuses a gateway `--checkpoint-grant-window` above 8 at startup, and after a handoff counts the whole window as outstanding until the grant TTL has passed since the fence acknowledgement. An adapter that acknowledges a fence cancels the previous generation's open `Checkpoint` and `ExportOutputFile` streams. An adapter writes contiguous chunk offsets in file order and a whole-file SHA-256 that matches the file it read. A snapshot's `consumed_message_ids` round-trips in report order.

### Tier 2: component

- Pool admission returns the `Warning` for an effective `checkpointPolicy` of `sealOnly` or `none` when any revision in the Runtime's traffic declares `credentialCapabilities.hotRotation: false`, and accepts an interval outside the deployer's bounds only as a clamped value.

### Tier 3: contract

- `FinalizeWorkspace` and `Resume` carry the session's `checkpointExcludes` list, and a capture after either leaves the listed directories out.
- `Checkpoint` message order and every abort arm.
- `checkpoint_complete` is sent once the capture ends, before any pack of the upload pass.
- A `PackReady` whose `length` exceeds `packMaxBytes` receives `CheckpointAbort`, and no `PackGrant` is sent.
- `ExportOutputFile` message order, `ExportAbort` with each reason, `PackStored` and `ReferenceRefused` on both streams, the output-file chunk lists and leased packs on `CheckpointStart` and `Resume`, and the generation stamp on `CH-OUTPUTFILE`, which rejects a stream from a stale coordinator.
- The `SandboxTemplate` CRD refuses a `checkpointGrantWindow` above 8.

### Tier 4: integration

- A checkpoint and restore round trip against MinIO, with byte-for-byte comparison.
- A second checkpoint after a one-file change uploads one pack.
- A timed-out attempt's packs are reused by the next attempt.
- A derive references packs without copies, and garbage collection keeps them while the derived snapshot lives.
- Quota is charged once per pack and released at deletion.
- Under an effective `checkpointPolicy: sealOnly`, no checkpoint runs during the session, the seal runs, and a pod failure resumes with `conversation_only`.
- Under an effective `checkpointPolicy: none`, the seal does not run, `GET /v1/sessions/{id}/workspace` returns 404, and a derive with `from: latest` is refused.
- Two pools with different intervals are checkpointed on their own schedules, and `CheckpointStale` measures each against its own interval.
- A runtime whose `lifecycle_support` leaves out `"checkpoint"` receives no `checkpoint_request` and is captured on the best-effort path.
- A session on a tenant-derived runtime whose base revision excludes `node_modules/` and whose tenant appends `dist/` is captured without either directory, and after a resume onto another pod its next capture still leaves both out.
- With the watch limit set below the workspace's directory count, the tracker reports degraded, every capture walks, and a verification walk finds no missed change.
- A directory created after the tracker started, and the files written into it before its watch existed, are in the next capture.
- A running `git repack` leaves no `tmp_pack_*` file in the snapshot, and the restored repository passes `git fsck`.
- A derive with `from: latest` charges nothing to the tenant's storage counter, and a derive of a source under an effective `checkpointPolicy: none` is refused with `checkpointing_disabled`.
- A security hold on a session copies every pack of each held snapshot to escrow. Rotation, pack reclamation, `DeleteByUser`, and `DeleteByTenant` call `EnsureCopied` first, and a copy failure refuses the deletion. After the originals are deleted, the escrowed snapshot restores byte for byte.
- An export of a file that a checkpoint captured uploads only changed chunks, a second export of an unchanged file uploads no pack, and a `Range` download reads the recorded pack offsets and verifies each chunk. A corrupted pack fails the read of the chunk it holds under S4. A full download and a `206` response carry the file's `Repr-Digest`, and the scanner's whole-file read of a file whose stored bytes differ from the hash refuses the part on the `REJECT` path.
- A security hold with `include: artifacts` copies an output file's packs and its `output_files` row to escrow, and the deletion after a `REJECT` calls `EnsureCopied` first. A forced tenant deletion under a legal hold escrows every pack of a held snapshot and a held output file with their rows.
- A pack that a rotated snapshot shared with an output file stays until the output file's retention ends.
- A workspace download whose manifest's entry sizes sum past `workspaceSizeLimitBytes` fails.

### Tier 5: e2e on Kind

- A 5 GiB workspace checkpoints with a pause under budget and restores on a new pod.
- A drain checkpoint fits `evictionCheckpointCapSeconds`.
- The rendered workspace volume carries `sizeLimit`.
- With `gateway.checkpointConcurrency` and `controller.adapterRestoreConcurrency` set in the chart, the gateway Deployment carries `LENNY_CHECKPOINT_CONCURRENCY` with that value, and the adapter of a new agent pod carries `--restore-concurrency` with that value.
- An embedded binary that does not take part in the quiesce handshake is checkpointed on the best-effort path, its process is never stopped, and its pod renders no liveness probe (IC-9).

### Tier 6: e2e in the cloud

- The pause, the tracker, and restore for a pod with the adapter as a sidecar under runc, gVisor, and Kata, including inotify events from the agent container reaching the adapter container's tracker.
- The tracker on a node for pools with `isolationProfile: sandboxed`, whose containerd handler drops `dev.gvisor.*` annotations, stays off under `checkpointChangeTracker: auto`, and on a node that passes them it runs and reports no fallback.
- A tracker in the adapter container sees the agent container's writes on every configuration where `checkpointChangeTracker: auto` turns it on, and `auto` leaves it off on `9p` and `virtiofs`.
- The reflink probe on each profile's workspace volume.
- Kata with `emptydir_mode = "block-plain"` under the kubelet's `emptyDir` size accounting.

### Tier 7: load

- Concurrent git operations during best-effort captures produce restorable repositories, and the attempt never fails on a changed file.
- An interrupt during the upload pass is acknowledged within the interrupt deadline.
- Concurrent attempts per replica with one slow session.
- A checkpoint and an export of one session never hold more than the pool's effective window of grants together, and the capture pass pauses export reading, hashing, and compression while export pack uploads continue.
- A reference closure racing pack reclamation of the same pack either commits the reference with the pack live or is refused, and never references a soft-deleted pack.
- A writer creating 10,000 files per second during the capture keeps the pause within the budget or ends on the best-effort path, with no missed change in either case.

### Tier 8: chaos

- An adapter crash in each pass, a gateway failover during finalization, and a lease expiry for orphaned packs.
- A coordinator handoff during an export: the adapter cancels the previous generation's streams at the fence acknowledgement, the new coordinator signs no grant for the session until the grant TTL has passed since that acknowledgement, takes over the export's reservation from its `output_file_exports` row, reopens `ExportOutputFile`, and the adapter reuses its clone and confirmed packs. The previous coordinator, still running, treats its cancelled stream as no failure: it releases no reservation, changes no export record, and sends no `output_failed`, and an update it attempts with its own generation afterwards changes no row. A pod loss during an export releases the reservation and leaves the confirmed packs under lease. A Redis loss during an export followed by `Rehydrate` keeps the outstanding export reservation in the counter.
- A drain whose eviction checkpoint runs while an export uploads: no export grant is signed until the checkpoint ends, and an export still running, or a part still waiting in the export queue, at the end of the checkpoint stage fails with `pod_evicted`.

### Tier 9: security

- A pod that names a foreign pack ID is refused.
- A pod that declares a pack larger than `packMaxBytes` receives no grant, and the attempt aborts.
- A `PUT` for an existing pack ID is refused, including one whose row is soft-deleted and awaiting the tombstone sweep.
- A `GET` outside the snapshot's packs is refused.
- A substituted pack is detected at restore.
- An `ExportReady` that names a pack outside the closure, or chunk metadata that disagrees with the pack footers, is refused, and an adapter that reports zero `estimated_new_bytes` cannot write past the tenant's quota.

### Tier 11: documentation

- Runbooks for `CheckpointDurationHigh` and `CheckpointStale` describe the pause metric and the convergence fallback.
- The checkpointing page's settings, defaults, and metric names match the configuration reference and the metrics reference.
- The configuration reference lists `gateway.checkpointConcurrency` and `controller.adapterRestoreConcurrency` with the defaults the chart's `values.yaml` sets.

## Decisions

| ID | Decision | Recommendation | Alternative |
| :-- | :-- | :-- | :-- |
| D1 | Deduplication scope | Per session and its starting snapshot, as "Reference closure" states. | Tenant-wide deduplication across sessions. It saves the first upload of a repository that many sessions clone, but the server would have to answer whether a hash exists, which opens a confirmation attack, in which a session learns whether another session holds a file by asking whether its hash exists, and needs proof of ownership. |
| D2 | Derive storage | A reference to the source's packs, with reference-based garbage collection. | Copy every pack, as derive copies tar chunks today. The cost grows with workspace size. |
| D3 | Default `workspaceSizeLimitBytes` | 10 GiB for the default resource class, with the class's `ephemeral-storage` request rendered to match. | Keep 512 MiB. Large clones then keep failing. |
| D4 | Partial-manifest reassembly | Remove it. | Keep a pack-level partial snapshot. The design reuses confirmed packs instead. |
| D5 | Exclusions | A runtime-declared `checkpointExcludes` list for regenerable directories such as `node_modules/`, empty by default. | No exclusions, or honor a workspace `.lennyignore`, which lets the agent choose what survives a restore. |
| D6 | Git maintenance | Set `gc.auto=0` and `maintenance.auto=false` in the pod's injected git configuration. [`egress-and-git-design.md`](egress-and-git-design.md) owns that configuration, so the recommendation takes effect only as a request that design adopts (see "Changes requested of other designs"). | Leave maintenance on and accept a history re-upload after each repack. |
| D7 | Pause budget defaults | Settled by the owner on 2026-11-06: a 10 s pause budget. The spool cap stays at 512 MiB and the pre-copy rounds at 3, followed by best-effort capture. Admission rejects a budget of 60 s or more. | A 5 s budget, which revision 3's first draft recommended. It halves the longest interrupt delay a checkpoint causes, and it sends Chromium-sized workspaces under gVisor without a tracker to the best-effort path. |
| D8 | `checkpointPolicy` values and resolution | Settled by the owner on 2026-11-05: `full`, `sealOnly`, and `none` on the Runtime and the pool, with the stricter value winning. | None. |
| D9 | Interval resolution between the Runtime and the pool | The pool's value wins, clamped to the deployer's bounds. | The shorter of the two values wins. That protects more of the runtime's work, and it lets a runtime author raise a pool's storage and gateway load without the operator's consent. |
| D10 | Change source for the capture | An inotify change tracker with the stat walk as its fallback, `checkpointChangeTracker: auto` by default, which runs the tracker only on workspace file systems where the agent container's writes reach the adapter's watches: a node file system under runc, gVisor's pod-share mount, and a Kata block volume. Measured steady pause: 0.03 to 0.16 s on every profile. | The stat walk only. The pause then grows with file count, to 8.6 s for Chromium under gVisor. A fanotify tracker with file system marks needs `CAP_SYS_ADMIN`, which agent pods do not hold. |
| D11 | Pause budget | An estimated capture time, decided before `checkpoint_request` from rates measured during pre-copy, with `checkpointCaptureBudgetBytes` kept as the spool cap. | A byte budget, as in revision 2. It misses captures of many small files, which cost 50 to 130 µs each on gVisor and Kata's shared file system. |
| D12 | Basic-level checkpoints | Basic runtimes get the best-effort snapshot that §4.4.3 states, with no pause, and §15.4.3, §28.5.3, and the runtime author guide change to match. The adapter needs nothing from the runtime for a best-effort capture. | Basic runtimes get no checkpoint, as §15.4.3 states. A pod failure then loses the workspace of every Basic session, and credential rotation restarts them without a workspace. |
| D13 | Deployer guidance for gVisor, Kata, and runc | The pod controller renders the gVisor pod-share mount hints on pods of pools with `isolationProfile: sandboxed`, and the guide tells deployers to pass `dev.gvisor.*` annotations to runsc. Recommend `emptydir_mode = "block-plain"` and the guest watch limit on Kata nodes, and the inotify sysctls on runc nodes. State that `--file-access-mounts=exclusive` must not be set and that `--platform=kvm` slows checkpoints. List `virtio_fs_cache = "metadata"` for Kata deployers who keep the shared file system. | Leave node configuration out of Lenny's documentation. Deployers on gVisor and Kata then run with the walk pause or a degraded tracker without knowing why. |
| D14 | Where the checkpoint settings live | Settled by the owner on 2026-11-08, in the word "ok": `packMaxBytes` (32 MiB), `racyWindow` (2 s), `bestEffortRereads` (2), `reflinkMinBytes` (1 MiB), and `packRewriteThreshold` (0.5) are constants that the specification states, and the gateway refuses a pack grant larger than `packMaxBytes`. `checkpointConcurrency` (16) is a gateway setting from the chart value `gateway.checkpointConcurrency` with a §17.8.1 row. `restoreConcurrency` (8) is an adapter setting from the chart value `controller.adapterRestoreConcurrency`, which the controller passes to every agent pod. | Make every setting configurable. A configurable `packMaxBytes` changes the bound in S5 per deployment, and the other constants change the pack format or the capture's correctness rules without a measured need. |
| D15 | The embedded `SIGSTOP` checkpoint path | Settled by the owner on 2026-11-09: remove it, with `checkpointStuck` and the liveness integration built on it, as IC-9 states. An embedded binary is checkpointed cooperatively or on the best-effort path. | Keep the path and its liveness rule. The platform would then pause an embedded agent by force, which the owner ruled out, and a node-side freeze of an embedded container under quarantine would fail the liveness probe, so [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) would have to amend the rule. |

## Not verified

- A Full-level runtime's own quiesce time. The harness used `SIGSTOP` on the workload as a stand-in.
- Restore through the gateway's grant protocol and presigned URLs. The harness restored with direct SDK access.
- Clouds other than AWS, Arm nodes, and Kubernetes node images other than Amazon Linux 2023. The Kubernetes checks ran on a single-node k3s cluster with kubelet 1.37.
- Whether Kubernetes user namespaces give each pod its own inotify budget under runc.
- Which managed gVisor offerings pass `dev.gvisor.*` pod annotations to runsc. Where they do not, pools with `isolationProfile: sandboxed` keep the walk.
- The pod-share mount with the tracker and the workload in separate containers under load. The benchmark ran both in one container, and the cross-container check wrote 202 paths.
- Kata `block-plain` with more than one workspace volume per pod, and its behavior when the node's disk fills, since `disk.img` is sparse.
- Ranged `GET` requests on presigned URLs for each backend, which would let a restore skip unused chunks of a pack. This design fetches whole packs.
- Whether `Content-MD5` binds into the signature on each backend. It does on SigV4 (AWS Signature Version 4) backends that sign it. An Azure SAS (shared access signature) does not bind request headers, so on Azure the write-time check depends on the adapter, and S4 is the control.
- Timestamp granularity on the node kernels Lenny targets. The constant `racyWindow` is set conservatively.
- Which local CSI drivers provide `VolumeSnapshot` on ZFS or Btrfs, for the deferred node-level provider.

The Kubernetes checks settled three items that revision 3's first draft listed here: inotify across containers, `--file-access-mounts=exclusive` against kubelet-updated volumes, and `block-plain` against the `emptyDir` size limit. The cloud runs settled items that revision 2 listed here: `FICLONE` is available under runc only, the stat walk's cost under gVisor and Kata is in "Benchmarks", and zstd's fastest level compresses at 0.85 to 2.0 GiB/s per core. Cold reads of small files ran at 17 to 38 MiB/s on gp3 and at 154 to 351 MiB/s on local NVMe, and the capture read recently changed files from the page cache in every scenario.

## Review history

An independent reader review read revision 4, and revision 4 applied its findings. An adversarial review of the output-file export read revision 10 as first written, together with `message-delivery-design.md` revision 4, on 2026-11-10, and revision 10 was extended the same day with its findings. A re-review read the extended revision 10 together with `message-delivery-design.md` revision 5 the same day, and revision 11 applies its findings. A short review of `message-delivery-design.md` revision 6 the same day led the owner to revise A19, and revision 11 also applies that revision and the short review's fixes. No other review has read any revision of this design. A cross-design sync review on 2026-11-08 raised findings against this document, and revision 7 applies them.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 2 added IC-8, which lets a runtime or pool turn checkpointing off and set its own checkpoint interval.
- Revision 3 added the cloud benchmarks the owner asked for ("Benchmarks"), a change tracker that removes the stat walk from the pause (IC-1, "Change tracker"), a pause budget expressed as estimated capture time, the levers operators, runtime authors, and tenants have to reduce the cost of checkpoints ("Levers"), and the changes this design makes to Lenny's documentation ("Changes to Lenny's documentation").
- Revision 4 followed a pass for a reader new to the project. It added "Terms" with its tables of platform terms, benchmark terms, and labels from other designs, "High-level design", and the evidence sources note. It moved "Owner's direction" after "High-level design", and "Measurements" and "Benchmarks" after the proposals they measure. It named the object that holds each setting the text cites, linked the specification sections and other designs the text cites, and moved the revision history from the status line to these notes. It corrected a Tier 1 test that still resolved `checkpointChangeTracker: auto` by isolation profile, which IC-8 resolves by the workspace mount's file system type. After an independent reader review it added terms for trusted entries, the spool cap, the size and reflink probes, interrupts, warm pods, the pod controller, the resource class, claim and bind, the plan, tar chunks and partial manifests, the tiered drain cap, the CheckpointBarrier, and the pool-config admission webhook's floors. It glossed the `Checkpoint` stream messages, `ExtractTree`'s rules, the guarded quota release and backstop sweep, the suspended-session release, and the first-checkpoint jitter. It also used "tar chunk", "git packfile", "barrier file", "grant", and "pause" consistently. After a validation pass, it reworded the "Problem" bullet on the size limit, which described the specified eviction and size probe as current behaviour although the tree sets neither limit by default, and changed no rule.
- Revision 5 applied [decision D14](#decisions), which the owner settled on 2026-11-08. It made `packMaxBytes`, `racyWindow`, `bestEffortRereads`, `reflinkMinBytes`, and `packRewriteThreshold` constants that the specification states, and it added the rule that the gateway refuses a pack grant whose declared length is zero or exceeds `packMaxBytes`, to IC-2, IC-3, and S5. It made `checkpointConcurrency` a gateway setting from the chart value `gateway.checkpointConcurrency` through `LENNY_CHECKPOINT_CONCURRENCY`, and `restoreConcurrency` an adapter setting that the controller passes from the chart value `controller.adapterRestoreConcurrency` through the controller flag `--adapter-restore-concurrency` to the adapter flag `--restore-concurrency`. It added these to "Stored formats and wire contracts this forces", "Specification sections affected", the configuration reference row, and the tests.
- Revision 6 updated the **Alternates.** bullet under IC-1 to match the warm-list decisions of `tenant-derived-runtimes-design.md` revision 58, and changed no rule. A warm repository store now holds an operator's entries and a tenant's cache-only entries as well as default clones, and it backs any `gitClone` source whose URL matches, a client's source included.
- Revision 7 applied the cross-design sync review of 2026-11-08 and the owner's answers of 2026-11-09. It added IC-9 and decision D15, under which the embedded `SIGSTOP` checkpoint path of §4.4.3, its `checkpointStuck` flag, and its liveness integration are removed, and updated "Current state", "Defects found while verifying", "Specification sections affected", the documentation rows, and the tests to match. It stated under IC-8, "Runtime fields and revisions" that `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are live fields of `runtime-revisions-design.md` revision 3, that `checkpointExcludes` is versioned, and that a tenant's `checkpointExcludes` on a derived runtime merges over the selected base revision, and it made the pool admission `Warning` read every revision in the traffic. It added "Changes requested of other designs": D6 as a request to `egress-and-git-design.md`, a request to `session-derive-design.md` that the storage reservation cover only `from: start` uploads and that `checkpointing_disabled` join its refusal reasons, and a request to `tenant-derived-runtimes-design.md` for rows on the checkpoint fields. It defined a security hold's escrow copy of a snapshot and the `EnsureCopied` call on every snapshot and pack deletion path (IC-6), named EG-2 as the proposal that renders the workspace `sizeLimit` whose value IC-7 sets, and stated §5.3's restriction on `standard` in the runc lever. It corrected the status line and "Review history", which said no review had read the design although revision 4 records an independent reader review, and replaced an explicit count in "Levers".
- Revision 8, dated 2026-11-09, applies the owner's decision N6 of that date, which the owner approved with a guardrail, and changes this rule. A tenant may set `periodicCheckpointIntervalSeconds` on a tenant-derived runtime at or above the base's interval, which is read live, and a lower value is refused. The owner's answer made the runtime `incompatible` when a later base change raises the base's interval above the tenant's. This revision follows the pattern of `tenant-derived-runtimes-design.md` "DR-6: Base changes" for live restrict-only fields instead, under which such a change applies at the next resolve and the session takes the longer interval. "Interval", "Runtime fields and revisions", the interval row of "Levers", item 6 of the checkpointing page, the request to `tenant-derived-runtimes-design.md` under "Changes requested of other designs", and the tier 1 tests state it.
- Revision 9, dated 2026-11-09, applies decision D14 of `runtime-revisions-design.md` revision 7, which the owner settled that day in the words "go with your recommendations", and changes this rule. `checkpointExcludes` is gateway-read, so a change to it creates a revision that shares its pod set. The gateway composes each session's list at the post-chain point, stores it on the session record, and sends it on `FinalizeWorkspace` and `Resume`, and the adapter leaves the listed directories out of every capture of the session and adds no tracker watch under them. A resume sends the stored list, including one that moves revision. "Runtime fields and revisions", the exclusions row of "Levers", "Specification sections affected", "Stored formats and wire contracts this forces", and the tier 1, tier 3, and tier 4 tests state it.
- Revision 10, dated 2026-11-10, applies decision A12 of `message-delivery-design.md` revision 4, which the owner settled that day, and changes these rules. Under A12 an output file is copied with this design's chunks, packs, and grants, because one `PUT` of up to 5 GiB would break S5 and R5. IC-3 adds the `ExportOutputFile` stream beside `Checkpoint`, with `ExportStart`, `ExportProbe`, and `ExportReady` and the pack messages it shares, and the gateway counts a session's outstanding grants across both streams against one `checkpointGrantWindow`, so S5 states the bound per session. "Security properties" states that R5 and S1 to S4 apply to export packs under the same key prefix. IC-4 adds the session's output files to the allowed set and the dedup set, adds the `output_files` and `output_file_packs` tables, and states the export's quota reservation. IC-5 renders an output file's download from its packs with `Range` and per-chunk verification. IC-6's pack reclamation counts `output_file_packs`, output files follow the session's retention and legal hold, erasure deletes them, and a security hold copies their packs to escrow. IC-7's spool cap covers export clones under `/workspace/staging/export/`. "Terms", the label table, the high-level summary of IC-3, and the status line state it. The same day an adversarial review of `message-delivery-design.md` revision 4 and of this revision found defects in the export path, and the owner answered "go with all your recommendations", which approves every fix and that design's decisions A13 to A17. This revision stays revision 10 and applies them, and it reads against `message-delivery-design.md` revision 5 and `runtime-revisions-design.md` revision 8. Under H2, S1, IC-3, and the catalog key a pack on `(tenant_id, owner_session_id, pack_id)`, the reference tables name the owner, and a `PackReady` for a pack in the session's allowed set is answered `PackStored`, which reveals nothing outside the session. Under H4 and M7, a closure failure answers `ReferenceRefused` with the offending pack IDs, which the adapter evicts and uploads again, `CheckpointStart` and `Resume` carry the session's output-file chunk lists and leased packs, and a chunk enters the dedup set only after `PackCommitted`. Under H5, `ExportReady` is checked for contiguous offsets, chunk lengths against the 4 MiB maximum and the pack footers, and the total against the probe and the limit, the `output_files` row records each chunk's pack ID and offset within the pack for `Range`, the first full read verifies the whole-file SHA-256, and IC-5's workspace download bounds its manifest by `workspaceSizeLimitBytes`. Under H6, `exportTimeoutSeconds` (900) ends an export, and `packLeaseSeconds` (1800) is a gateway setting at least the longer of the attempt and export deadlines, extended by each reference. Under M1 and A15, IC-3 and IC-7 state the seal, drain, and handoff rules for exports. Under M2, the stream runs on its own channel, `CH-OUTPUTFILE`, with generation fencing, and "Specification sections affected" amends §28.3, §28.4, §28.5.1, §28.5.5, and §28.6. Under M3, A13, and A14, S5 counts the window on the coordinating replica and across a handoff, keeps the per-pool override with a maximum of 8, so the bound is at most 256 MiB, and names today's bound of 4 × 16 MiB in place of "The residual exposure matches today's", with §10.1, §10.1.7, and §17.8.1 among the affected sections and the CRD maximum among the stored formats. Under M4, a confirmed pack draws down the export's reservation, §11.2's signing-time headroom check applies per export, every terminal outcome releases the reservation, and `files.maxConcurrentExports` (2) caps a session's exports. Under M5 and A16, IC-6 adds the deletion after a `REJECT` or a `checksum_mismatch` to the `EnsureCopied` paths, a hold with `include: artifacts` copies an output file's packs and row, and the §12.8 legal-hold escrow copies a pack-backed item's packs and row. Under M6, the reference closure locks its pack rows `FOR SHARE`, and reclamation re-checks liveness under `FOR UPDATE` with `deleted_at IS NULL`. Under M7 and A17, export clones may use at most `exportSpoolFraction` (0.5) of the spool cap, and a capture pass pauses export reading, hashing, and compression while built packs keep uploading. Under M8, the "Pack lease", "Dedup set", and "Spool cap" terms, the high-level summaries of IC-4 and IC-6, the `checkpoint_packs.state` transition, "Costs" **Storage.**, "Relationship to other designs", "Specification sections affected", "Stored formats and wire contracts this forces", and the tests cover output files, and IC-3 and IC-4 apply `message-delivery-design.md`'s earlier request that the checkpoint record the consumed message-id set in report order and that the catalog make it readable on resume.
- Revision 11, dated 2026-11-10, applies the re-review of the extended revision 10 and of `message-delivery-design.md` revision 5, under that design's decisions A18 to A21, which the owner settled that day in the words "R1, R3, R5: ok" and "R8: ok", and the re-review's other fixes, which the owner approved the same day. It reads against `message-delivery-design.md` revision 6 and `runtime-revisions-design.md` revision 9, and changes these rules.
  - R1 and A18: IC-2 writes a random 128-bit nonce into every pack footer, for checkpoints and exports alike, so no two packs share a pack ID, and the pack ID stays the SHA-256 of the pack's bytes. S1 stays strict and refuses every key the catalog holds in any state, soft-deleted rows included, and IC-6 states why a soft-deleted row is never revived. The "already stored" answer covers a resend of a pack in the allowed set after a lost `PackCommitted`, a retry reuses chunks of confirmed leased packs and rebuilds no pack, and the tier 1 tests assert that a rebuilt file gets new pack IDs and new grants. IC-4, "Catalog", states that a `referenced` pack never returns to `unreferenced` and leaves the allowed set and the dedup set when its last referrer goes.
  - Finding 2: IC-4 adds the `output_file_exports` table, the export record, with the reservation's guarded release, its term in `Rehydrate`, and a new coordinator's takeover, and "Specification sections affected" adds them to §11.2 and §12.5.
  - R3 and A19, as the owner revised A19 after the short review of `message-delivery-design.md` revision 6: IC-3 and IC-5, "Seal", run the seal checkpoint first with no export grant signed while it runs, and the session's exports finish in the time it leaves in the same seal window, the whole window under `checkpointPolicy: none`, before `seal_timeout`. The seal and the exports share one dedup set, and IC-3 checks each chunk against it when it packs the chunk, so an exported file's chunks upload once, and IC-5 names the cases that upload them apart from the seal. The seal window is the runtime revision's `maxWorkspaceSealDurationSeconds` over the pool's, within the chart ceiling `gateway.workspaceSealMaxDurationCeilingSeconds`, and the terminal pipeline budget stays the seal window plus the release allowance. §5.1, §5.2, §7.1, and §17.8.1 in "Specification sections affected" state it. The revision as first written gave exports a separate wait, `files.sealExportWaitSeconds`, under a ceiling of 900 seconds, which this revision removes.
  - Short review: every release and state transition of an export record carries the coordination generation as a guard, and a stream a fence acknowledgement cancels is not a failure for the previous coordinator (IC-3, IC-4 "Catalog" and "Quota", and the tier 8 tests). The adapter's descriptor count is authoritative, a part beyond it is relayed `unopened: true` and refused, and the adapter frees a part's descriptor at the end of its export or at its `output_failed` (IC-3). A part still waiting at the end of a drain's checkpoint stage fails with `pod_evicted` (IC-7). "Relationship to other designs" names IC-5's seal window in place of the seal's export wait.
  - Finding 4: a `failed` export record keeps the failure notice until the runtime acknowledges it.
  - R5 and A20: IC-5 publishes the whole-file SHA-256 as `sha256` and in `Repr-Digest` in place of verifying it on the first full read, the scanner's read compares it, and `sha256_verified_at`, `checksum_mismatch`, and that deletion path leave IC-4, IC-6, and the tests.
  - Finding 6: S5 and IC-3 count the whole window as outstanding after a handoff until the grant TTL has passed since the pod acknowledged the fence, and the adapter cancels the previous generation's streams at that acknowledgement.
  - Finding 7: the gateway refuses `--checkpoint-grant-window` above 8 at startup, in S5, the stored formats, and §17.8.1.
  - R8 and A21: IC-7, "Spool cap", states that a reflink pool reads in place a file larger than the remaining export share, 256 MiB at the default spool cap, and that the clones of waiting parts count against the share.
  - Finding 9: IC-3 freezes a part when the adapter reads it and caps the open export descriptors, `exportTimeoutSeconds` counts from `ExportStart`, a waiting part fails at the same span from its first frame, and `files.maxQueuedExports` joins IC-4, "Quota" and §17.8.1.
  - Finding 10: IC-3 and IC-7 sign no export grant while the eviction checkpoint runs and keep export streams out of the stream-drain stage of §10.1.7.
  - Finding 11: IC-3 adds `ExportAbort` with its reasons.
  - Finding 12: pool admission requires `packLeaseSeconds` to be at least twice the larger of `checkpointAttemptTimeoutSeconds` and `exportTimeoutSeconds`.
  - Finding 14: IC-6's list of `EnsureCopied` paths drops `checksum_mismatch` and matches the list in FE-3 of `forensics-evidence-preservation-design.md`.
  - Finding 16: S1, IC-3, and IC-4, "Reference closure", state that `PackReady` resolves the session's own key first and consults the allowed set's packs another session owns only for the "already stored" answer, in place of the claim that the check reads only the session's own keys.
  - Finding 17: the status line no longer says that revision 10 applies the review of revision 10.
  - "Terms" glosses the pack nonce, the export queue, the export record, the unopened part, `ExportAbort`, the seal window, the coordinator fence, the quota reservation with `Rehydrate`, and tombstone retention, and updates the pack lease, the "already stored" answer, the spool cap, and the label row for `message-delivery-design.md`.

- Revision 12, dated 2026-11-10, follows decisions A22 and A23 of `message-delivery-design.md` revision 7: the §7.1 entry under "Specification sections affected" states that `post_terminal_until` records each session's terminal pipeline budget and that the seal window resolves revision, pool, and gateway flag under the ceiling.
