# Decisions: 0153 Workspace materialization

## Decisions

The owner settled D1 to D9 on 2026-11-12 by accepting the materialization study "with its recommendations", and settled SI O11 the same day. The owner's later answers of the same day reversed D5, superseded D8, and settled open decisions O1 to O8; D10 to D16 record them. The owner's decisions of 2026-11-13 settled O9 and the remaining questions of revision 3, which D19 to D22 record, and superseded the per-slot allowance of D12. The owner's answers of 2026-11-13 to the consolidated list of open decisions add D23 and D24. D25 records the owner's model of 2026-11-14 for warm stores, D26 the owner's answers of the same day to the confirmation list of question 17, and D27 the owner's later confirmation of the same day of the store GID as a primary group.

| # | Question | Owner's answer | Applied in |
| :-- | :-- | :-- | :-- |
| D1 (study A) | Does gateway-side archive extraction move into the helper? | Yes. Gateway archive extraction moves into the helper. | WM-9 |
| D2 (study B) | How does the helper report back? | Results come back on a pipe, and the setup log goes on a pipe too. | WM-3, WM-5 |
| D3 (study C) | Which git does materialization use? | A pinned static git staged read-only in `/lenny/bin`. | WM-10 |
| D4 (study D) | Where does checkpoint restore run? | Restore stays in the adapter, through the broker. | WM-7, WM-13 |
| D5 (study E) | Is the worker synchronous? | Revision 1: "The worker stays synchronous, with a cancel pipe and adapter-side serialization." **Reversed** on 2026-11-12 by D10. | WM-11 |
| D6 (study F) | How does `netagent` tell a materialization fetch from the agent's? | The adapter registers a fetch grant with `netagent`, scoped to the slot, the URL, and a time window. The egress design owns it, together with SI O9. | WM-10 |
| D7 (study G) | How is the helper protected from processes beside it? | The helper is staged `0111` and also calls `prctl(PR_SET_DUMPABLE, 0)`. | WM-2 |
| D8 (study H) | How does a plan with many sources fit the descriptor limit? | Revision 1: bundle files. **Superseded** by D12 (O4): content sources reach the helper one at a time over the content socket, so a run passes six descriptors whatever the plan holds, and no bundle is needed. | WM-4 |
| D9 (SI O11) | Where does git on a slot repository run? | Every git step on a slot repository runs as the slot UID: init, alternates, the fetch through `netagent`, the tree-size check, checkout, the index refresh more than 1 s after checkout, the capture copy before a checkpoint's first pass, and restore completion. These become helper subcommands. The adapter runs no git on slot repositories. The disk watch is the backstop for the tree-size check. | WM-10, WM-13 |
| D10 | The worker's concurrency. | "Asynchronous supervisor worker: approved. This reverses WM decision E, because AWS showed a checkout blocking the broker for 19 s and a repack exceeding the 120 s deadline." The owner required a security review of the supervisor change. Three independent reviews read the spike's diff, and the last found nothing at Medium or above; its Low items are fixed. | WM-3, WM-11 |
| D11 (O1) | The memory floor. | "The floor is a platform-wide setting (a Helm value such as `runtimes.minSlotMemoryBytes`, default 1Gi until validation replaces it). Pool and resource-class validation refuses anything below it, a platform admin may lower it with a logged warning, and a pool owner may not go below it. The 8 MiB writeback-pacing interval stays an internal constant." | WM-14 |
| D12 (O4) | Where content waits before the helper reads it. | "A files-only mechanism, one source at a time, in a single helper run. At the start of the run the adapter gives the helper one end of a socketpair. For each content source, in plan order, the adapter stages the file in the spool, verifies it (chunk hashes for imports), and sends its fd over the socket. The helper applies it and the adapter deletes the staging file. The adapter never reads what the helper writes on that socket (shutdown SHUT_RD). The collision map stays in the helper's memory for the whole run. Staging needs room for the largest single source, not the whole plan; the per-slot staging allowance is a setting sized to the largest single item the platform accepts, with a pod-wide shared allowance as a possible later refinement. There is no streaming path." The per-slot allowance is **superseded** on 2026-11-13 by D19, which shares the staging space across the pod. | WM-4, WM-12 |
| D13 (O2, O3, O5) | The directory-descriptor cache; `RLIMIT_FSIZE`; cancellation. | O2 (a): "no directory-fd cache in the first release", with its measurement owed. O3 (a): "RLIMIT_FSIZE applies to helper runs only, meaning executions of lenny-materialize". O5: cancellation by close of the cancel pipe only, owned by the supervisor for every `setup`; the helper receives no cancel descriptor, and the adapter runs `discard` after a cancelled helper run. | WM-3, WM-6, WM-11 |
| D14 (O6, O7, O8) | Channel identifiers; the warning cap; the index refresh's timing. | Each accepted as recommended: `CH-MATERIALIZE`, `CH-SETUPLOG`, and `CH-MATRESULT`; at most 256 warnings per run; the refresh at the end of the same helper run, "after waiting out whatever remains of 1 s since the last checkout". | WM-5, WM-10, "Specification changes" |
| D15 | Reflink clones. | "The helper clones staged files into /workspace with FICLONE where the filesystem supports reflinks (runc on XFS or btrfs; both volumes are on one filesystem, and Linux 5.18+ allows cross-mount clones, as spike 2 measured). It falls back to copy_file_range and then to read/write. This makes staging nearly free for plain uploads. Archives are still decompressed." | WM-16 |
| D16 | Warm-up checkout and the reflink rebuild. | "Warm-up checkout: approved." Known clones are checked out at warm-up and after every slot release into `/workspace/.lenny-prestage/<entry>`, adopted by rename at bind with an incremental checkout if the commit moved, unused trees renamed aside and deleted in the background, checkpoints skipping the reserved names, a staged tree of a withheld store removed before the session starts, and the empty-workspace rule allowing reserved staging directories. "Reflink rebuild: approved, if it stays simple": one pristine tree per store in the pod-wide store volume, from which the helper creates slot-owned files and clones their content, falling back to a normal checkout on gVisor and Kata. | WM-15 |
| D17 | Pre-launched runtimes. | "Pre-launched runtimes (SDK-warm / `preConnect`) are removed entirely. Every runtime launches at bind, after materialization and setup." PL-8 owns the removal. | WM-8, WM-11, WM-15 |
| D18 | Running a one-slot pod's slot at the adapter's UID. | Not taken. The coordinator advised against it, and the owner chose the reflink clone instead. "Alternatives rejected" records the reasons. | "Alternatives rejected" |
| D19 (O9) | How much spool space upload staging may use. | Settled 2026-11-13, after the owner noted that no reliable "largest upload" is known ahead of time and preferred "a configurable, generous spool size": "pod-wide `spoolSizeLimitBytes` (default 16 GiB), adapter uses 1/spoolHeadroomFactor, reserves per-slot `checkpointCaptureBudgetBytes` (pool setting, configurable), rest is shared staging; wait then `staging_limit`; gateway refuses oversize uploads at upload time. Admission must refuse a spool whose usable share does not cover slots x capture budget." | WM-4, WM-6, WM-12 |
| D20 (EG decision 61) | Who fills the warm stores and their pristine trees. | Settled 2026-11-13: "if the separate container works from a permissions standpoint, then yes". The `lenny-store-fill` init container fills the stores and writes the pristine trees under its own UID, with no credential and only the store volume writable, after the `netagent` native sidecar starts and before the adapter starts. The stores are readable by every slot, `netagent` attributes the fill's UID as the platform fill and applies the fill's authorization, the adapter keeps read-write access to the store volume only to delete withheld stores and never parses repository data, the dedicated `fsGroup` GID covers the volume on Kata `block-plain`, and under user namespaces the fill's UID maps within the pod's range. The owner asked for validation. "WM-9" needs no store fill exception. | WM-1, WM-9, WM-15 |
| D21 | The content handoff of revision 2. | Accepted 2026-11-13: a done pipe per content source, the channel name `CH-MATCONTENT`, and `PrepareWorkspace` as a pull of one source per request. | WM-4, O6 |
| D22 | What a pool with no default clones stages at warm-up. | Changed by D25 on 2026-11-14, which replaces the `path` with `prestage: true` and adoption by repository. Settled 2026-11-13 as (a): "The operator's `gitRepository` warm entries gain an optional target `path` and are staged like default clones." A `gitRepository` entry of a runtime's `sharedAssets` with a `path` is checked out at warm-up into `.lenny-prestage`, and a session adopts the tree when its plan clones that repository at that `path` ([`0140-runtime-revisions`](../0140-runtime-revisions/decisions.md#decisions), D26). | WM-15 |
| D23 | The store volume's group (2026-11-13). | "ok but add an owed item to stress test this thoroughly (with actual tests)". `lenny-store-fill` writes the stores and the pristine trees with a dedicated store GID, in place of the adapter's former GID, as the file group and a umask of `0002`; the adapter deletes withheld stores through it with no-follow deletion; no slot container holds it. Every agent-pod container runs with `allowPrivilegeEscalation: false`, so setuid and setgid bits have no effect, and the fill's mode pass also clears them. [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md#decisions) owns the mechanism as its D17, with the hostile-repository stress test suite. PL's revision 8 makes the store GID the primary group of both the adapter and `lenny-store-fill`, because Kubernetes has no container-level supplementary groups, and the owner confirmed that refinement on 2026-11-14 (D27). | WM-9, WM-15, "Validation owed" in `steps.md` |
| D24 | Defaults and names (2026-11-13). | "ok" to each as written: `spoolSizeLimitBytes` stays 16 GiB by default ("keep 16 GiB"), and the 512 MiB per slot is the pool's `checkpointCaptureBudgetBytes`, configurable per pool, which confirms D19; the operator's `gitRepository` warm entries with a target `path`, which confirms D22; the reason `staging_timeout`; and `minStagingBytes`, the 1 GiB staging floor that pool admission enforces. | WM-4, WM-15 |
| D25 | What a warm-up checkout stages and where a session adopts it (2026-11-14). | Settled 2026-11-14 as the owner's model for question 17: "sharedAssets should define any git stores to warm up. workspaceDefaults shouldn't also do that through `warm: true`" and "in sharedAssets, we should indicate whether we want a repo to be pre-staged". The owner then asked "does prestage need a path? Can't the path be determined at the time of copying (based on workspaceDefaults and the session plan)?" and settled `prestage: true` with no path. The pod stages every `gitRepository` entry of `sharedAssets` with `prestage: true`, an operator's or a tenant's, and at bind the first `gitClone` source of the composed workspace, from the defaults or the plan, whose URL the store serves and that a store may serve adopts the tree by rename to its own `path`; a ref difference is checked out in place, the collision rule applies to an existing target, a second source of the same URL is built from the store, and unused trees are discarded. This replaces D22's `path` and the same-path adoption rule ([`0147-tenant-derived-runtimes` decision 42](../0147-tenant-derived-runtimes/decisions.md#decisions); [`0140-runtime-revisions`](../0140-runtime-revisions/decisions.md#decisions), D29). | WM-15 |
| D26 | The skip of a warm-up checkout, the write `Warning`, and the checkout's timeout (2026-11-14). | The owner confirmed revision 9's skip of an entry whose tree-size check stops or whose run reaches the timeout, with `lenny_prestage_skipped_total`, the `PrestageSkipped` event, and the write `Warning` when staged trees exceed half of `workspaceSizeLimitBytes`. Asked where the 15-minute timeout comes from and whether a runtime can override it, the owner wrote: "the tenant can choose repos, so if a repo can add warm-up time, I think the tenant should have a way to change the setting per runtime". A runtime and a pool may set `prestageTimeoutSeconds`, a tenant-derived runtime within its grant entry's `maxPrestageTimeoutSeconds` ([`0147-tenant-derived-runtimes` decision 43](../0147-tenant-derived-runtimes/decisions.md#decisions)), and a pod's timeout is the runtime revision's value, else the pool's, else `adapter.prestageTimeoutSeconds`. The 900-second chart default is a judgment that no measurement has set, and the `prestage` times that "Validation owed" in `steps.md` lists for Chromium and Linux test it. | WM-11, WM-15 |
| D27 | The store GID as a primary group (2026-11-14). | "Ok on the rest": the owner confirmed the store GID as the primary group (`runAsGroup`) of both the adapter and `lenny-store-fill`, with the pod under `supplementalGroupsPolicy: Strict`, which PL's revision 8 made after the owner's D23 answer. [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md#decisions) owns the mechanism as its D17. | WM-9, D23 |

**Gateway seam operations (0117, decision 8).** Settled by the owner on 2026-11-17 in [0117](../0117-gateway-package-layout/decisions.md), which raised the conflict between its seam interface page and this change: a member change that removes, reshapes, or adds a mechanism behind a gateway seam operation edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` in the step that changes the mechanism. In this change that is 0153.S3, whose entries in `steps.md` name the operations.

### Settled open decisions O1 to O9

The owner settled O1 to O8 on 2026-11-12 and O9 on 2026-11-13; the table above records them as D11 to D14 and D19, and the options stay here as the record of the choice. The M3 readiness review of 2026-11-19 moved them here from "Open decisions".

**O1. Slot memory sizing for materialization. Settled 2026-11-12: (a), as a platform-wide setting (D11).**

- (a) A floor on each slot container's memory limit, which the validation of pools and resource classes enforces, set provisionally at 1Gi and replaced by the value the validation on managed nodes supports, together with the helper's writeback pacing.
- (b) The helper's writeback pacing alone, with no floor.
- (c) No rule: deployers size slots and accept dead slots.

The owner asked whether the floor should be configurable, and settled that it is the Helm value `runtimes.minSlotMemoryBytes`, which a platform admin may lower with a logged warning and below which a pool owner may not go ("WM-14"). The spike's 512Mi slot took up to 42 s for 1 GiB, and a 1Gi slot was still lost in a back-to-back run **[measured]**, so the floor's final value waits for the validation of "Validation owed" in `steps.md`.

**O2. A directory-descriptor cache in the helper. Settled 2026-11-12: (a).**

- (a) Resolve every entry from the workspace root, as the spike did.
- (b) Keep a bounded cache of directory descriptors for recently used parents and resolve beneath them with the same `openat2` flags.

No cache in the first release. Its measurement is owed: the gap on gVisor, about 1 s on 100,000 files **[measured]**, measured on managed nodes, decides whether a later release adds (b). Under (b) a process of the session could rename a cached directory during the run, and the helper would then write into the moved directory, which costs only that session but makes the outcome harder to reason about.

**O3. `RLIMIT_FSIZE`. Settled 2026-11-12: (a), on helper runs only.**

- (a) On helper runs, set to the remaining workspace bytes, so the kernel refuses any single file larger than the session could hold, whatever the helper's own checks do.
- (b) Not set.

It applies to every execution of `lenny-materialize`, whatever its subcommand, and to no deployer setup command, because a legitimate build can write a single file larger than any value the adapter could choose and would die of `SIGXFSZ`. The spike measured `SIGXFSZ` at the limit **[measured]**.

**O4. Where content waits before the helper reads it. Settled 2026-11-12: one source at a time over a socket pair (D12).**

Revision 1 offered (a) staging every source for the whole run, (b) staging uploads and streaming imports through a pipe, and (c) counting staging against the spool cap. The owner replaced all three with the files-only mechanism of "WM-4": one source at a time, handed over a socket pair in a single helper run, with staging sized to the largest single source and no streaming path.

**O5. Cancelling a setup command. Settled 2026-11-12: cancellation by close only, owned by the supervisor (D13).**

Revision 1 offered (a) a worker that watches the cancel pipe for every `setup`, and (b) a cancel pipe that only the helper watches. The asynchronous worker made the supervisor the program's parent, so the supervisor watches the cancel pipe for every `setup` and kills the program's process group when the adapter closes it. No program receives the pipe, and the adapter runs `discard` after a cancelled helper run ("WM-11").

**O6. Channel identifiers. Settled 2026-11-12: (a).**

- (a) Name the gateway-to-pod conversation that carries `PrepareWorkspace` and `FinalizeWorkspace` `CH-MATERIALIZE` on `LNK-POD-GRPC`, and the two intra-pod pipes `CH-SETUPLOG` and `CH-MATRESULT`, each with a register row and a contract card.
- (b) Name the gRPC conversation only, and describe the pipes inside the `CH-SUPERVISE` card.

No channel carries materialization today, which is one of the contradictions under "Current state", and the pipes carry slot-written bytes to the adapter, which a register row with authority and degradation columns states better than a sentence inside a card whose rule is that it carries no slot-written byte. The content socket of D12 is a fourth conversation, from the adapter to the helper, which revision 2 named `CH-MATCONTENT` under the same rule, with the done pipe in its card, and which the owner accepted on 2026-11-13 (D21). The naming lint has the last word on all four.

**O7. The cap on slot-reported warnings. Settled 2026-11-12: (a).**

- (a) At most 256 warnings per helper run, with `warningsDropped` carrying the count of the rest and the warning event stating it.
- (b) No cap, as §7.4's one warning per skipped entry reads today.

An archive of 10,000 entries with a large `stripComponents` would otherwise produce 10,000 events from slot-written data, all reaching the client. §7.4 changes to state the cap.

**O8. When the index refresh runs. Settled 2026-11-12: (a).**

- (a) At the end of the `materialize` or `restore-complete` run, after waiting out whatever remains of 1 s since the last checkout.
- (b) In a separate helper run immediately before the first setup command.

Git sources usually come before the client's files and archives, so the wait is usually covered by later sources and costs nothing, and at worst it adds 1 s. `prestage` follows the same rule.

**O9. How much spool space upload staging may use. Settled 2026-11-13 as the owner revised it (D19).**

Revision 2 named a per-slot `stagingSizeLimitBytes` and asked for its default, with these options:

- (a) The pool's `workspaceSizeLimitBytes`, 10 GiB by default, which bounds every source the helper can write.
- (b) The largest single upload or import the pool accepts: the larger of the gateway's per-upload cap and the 5 GiB default of MD-18's output files.
- (c) A pod-wide allowance that the slots share, so the spool reserves one largest source per pod rather than one per slot.

The owner noted that no reliable "largest upload" is known ahead of time and preferred a configurable, generous spool size. The settled form replaces the per-slot setting: one pod-wide `spoolSizeLimitBytes`, 16 GiB by default, of which the adapter uses `1/spoolHeadroomFactor`, reserves each slot's `checkpointCaptureBudgetBytes`, and shares the rest as staging space; a source that does not fit waits and then fails with `staging_limit`; the gateway refuses an upload larger than the pool's staging space at upload time; and pool admission refuses a pool whose usable share does not cover every slot's capture budget. "WM-4" states the result. This revision sets the staging floor that admission checks beyond the capture budgets at `minStagingBytes`, a constant of 1 GiB, which the owner confirmed on 2026-11-13 (D24).

## Open decisions

The M3 readiness review of 2026-11-19 raised O10 to O18, and its second pass raised O19 to O24 at the request of other changes. O1 to O9 are settled and sit under "Decisions", in "Settled open decisions O1 to O9".

**O10. Whether a process that escapes a `prestage` run can reach the next session. Open.**

"WM-3" states that "A process that left the group with `setsid` survives until the next End". "WM-15" runs `prestage` at warm-up before READY and at the end of each slot release after the last End, and the bind sends no End before `launch` (SI-3 of [`0145-session-isolation`](../0145-session-isolation/change.md#si-3-runtime-processes-slot-release-and-setup-commands)). `prestage` runs the pinned git as the slot UID over warm-list repositories, whose git objects and trees "WM-9" classes as untrusted content, and "WM-9" confines `lenny-store-fill` for the same parsing because it ends before READY. A process that a git defect starts during `prestage` and that calls `setsid` therefore runs beside the next session bound to the slot, and it can alter a staged tree before the contribution to the platform chunk sets reads it. WMS-1, SIR-38 of `0145-session-isolation`, and "Adoption." in IC-4 of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#platform-chunk-sets) each assume that no such process exists.

- (a) After every `prestage` run, completed or cancelled with its `discard`, the adapter sends `end` and waits for `ended` before the slot accepts a bind and before the contribution reads the staged trees, and the supervisor refuses `launch` with `launch_not_permitted` after a `prestage` run until an attested End. At warm-up the End precedes READY.
- (b) End becomes the first request of every bind, and the contribution reads the staged trees only after an End.
- (c) Accept the residual and document it, which contradicts WMS-1 and SIR-13 of `0145-session-isolation`.

**Recommendation: (a).** It reuses the attested End and the launch permission that `cleanup` runs already have, and the supervisor enforces it. An End removes processes and not what a compromised run wrote, so under (a) or (b) the edit also states whether adoption at bind rewrites or checks the adopted repository's `.git` directory (configuration, hooks directory, and index) or accepts that residue explicitly. A `security` test with a `setsid` child left by `prestage` follows under (a) or (b). It blocks 0153.S8. The other changes are `0145-session-isolation`, whose O18 records the same question for SI-3's release order and the launch permission, and `0131-incremental-checkpoint`, whose trust statements for never-launched pods rely on it (its D24). (raised by the M3 readiness review, 2026-11-19; sources: 0153-F1, pod-structure-C2)

**O11. From which run in a slot the adapter treats broker replies as untrusted. Open.**

The "Broker root" term and **Trust point.** in "WM-3" distrust the broker's replies from the first run of the supervisor's worker for the first session bound to the slot. The first `prestage` of a slot runs before that, and the contribution of the staged trees to the platform chunk sets reads them through the broker after it ("WM-15"). The git objects that `prestage` parses are untrusted content under "WM-9", and on a tenant-derived runtime's owned pool they come from repositories the tenant chose.

- (a) Move the trust point to the first worker run in the slot, `prestage` included. The adapter records the pin before that run, and the contribution's reads and the restore at bind take the descriptor checks of SIR-21 and IC S6 under distrust, which already exist.
- (b) Keep the trust point as written, and `0131-incremental-checkpoint` states the residual and accepts it.

**Recommendation: (a).** The checks exist already, and (a) removes a trust assumption that `prestage` does not meet. It blocks 0153.S8 and the wording of "WM-3" and the "Broker root" term. The other changes are `0145-session-isolation`, whose O24 records the same question for SIR-22, and `0131-incremental-checkpoint`, whose D24 settles it together with the seeding of the file index. (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C11, 0153-F1)

**O12. Which RPC carries the adapter's request for one content source. Open.**

D21 accepts `PrepareWorkspace` as a pull of one source per request, and "WM-4" step 1 has the adapter ask the gateway for each content source while a helper run is in progress. In `schemas/lenny-adapter.proto` the adapter is the gRPC server: `PrepareWorkspace` is a client stream from the gateway and `FinalizeWorkspace` is unary, so neither carries a request from the adapter. The tree already has an adapter-dialed service, `GatewayControl`, and a request-and-answer pattern inside a gateway-opened stream, `Checkpoint`.

- (a) `FinalizeWorkspace` becomes a bidirectional stream: the adapter sends "source i" and the gateway answers with that source's bytes, so one call carries one deadline and the plan order, and the gateway serves only the sources of the session the call names.
- (b) A per-source fetch RPC on `GatewayControl`, which the adapter already dials and the gateway already authenticates per pod. The gateway checks that the requested source belongs to the session bound to the requesting slot.
- (c) The gateway pushes each content source in plan order on its own `PrepareWorkspace` call, and the adapter applies back-pressure until staging space and the helper are ready, which keeps the RPC directions and runs two calls concurrently.

**Recommendation: (a).** It keeps one deadline per run and scopes each source to the call's session by construction, and the mid-session `FinalizeWorkspace` serves overlays the same way. Whichever option is chosen, the edit states the request's deadline and its behaviour when the gateway replica changes during a run, and updates the §4.7.1 and §28.5.1 rows, the "Non-specification changes" entry for `schemas/lenny-adapter.proto`, and, under (b), O6's description of `CH-MATERIALIZE` as a gateway-to-pod conversation. It blocks 0153.S2. (raised by the M3 readiness review, 2026-11-19; sources: 0153-F2, downgraded to Medium by the skeptic's verdict)

**O13. Whether a slot's share of the shared staging space is capped, and when a content source is staged. Open.**

D19 shares one staging space across the slots of a pod with no per-slot share. During a mid-session overlay the session's own processes run beside the helper ("WM-5"), and a process of the slot UID can send `SIGSTOP` to the helper, which non-dumpability does not prevent. The agent can start a child-file import itself (`import_child_session_file`), stage a file up to the whole staging space, stop the helper, and hold the space until the `ImportFile` stream's deadline, so the other slots' sources wait and fail with `staging_timeout`. The slots of one pod belong to different users of one tenant. The adapter also stages the next content source as soon as the previous one's done pipe hangs up, so a staged file holds its space through the git sources before it.

- (a) Cap the staging space that overlays and imports of one slot may hold, for example at the staging space divided by `maxConcurrentSessionsPerPod`, with pre-start runs keeping the whole space, and stage content source i only once the helper reaches it, through a per-source ready pipe the helper closes, which keeps the rule that the adapter reads no byte the helper writes.
- (b) Keep one shared space, and cancel an overlay whose held staging blocks another slot's pre-start source past a bound.
- (c) Keep D19 as written and state the residual: a session can delay other slots' sources until `staging_timeout`.

**Recommendation: (a).** It bounds what one session can hold without reserving the largest source per slot, which D19 rejected. It changes D19, so it needs the owner. A `load_kind` or `security` test of a stopped helper holding staging follows. It blocks 0153.S2 and 0153.S5. [`0135-message-delivery`](../0135-message-delivery/change.md#md-26-files-from-a-child) MD-26 is the agent-triggered import path, and its `staging_timeout` and `IMPORT_LIMIT_EXCEEDED` text follows the answer. (raised by the M3 readiness review, 2026-11-19; sources: 0153-F3)

**O14. Which step owns the `CH-SUPERVISE` worker and its refusal rule, and what a `freeze` does during a `cleanup` run. Open.**

0153.S1 builds "The supervisor's asynchronous worker and `setup` with descriptors, log pipes, cancellation by close, the process-group kill, ... the refusal rule", and [0145.S3](../0145-session-isolation/steps.md#steps), on which it depends, builds "The asynchronous worker and the whole `CH-SUPERVISE` protocol". WMR-12 answers every request other than `fs`, `ping`, and `end` with `worker_busy` during any run, and WMR-14 cancels a run only by closing the cancel pipe. SIR-23 of `0145-session-isolation` serves a `freeze` during a `cleanup` run after killing the cleanup worker's process group, because a `cleanup` run has no cancel pipe. Under WMR-12 a `freeze` during a `cleanup` run waits for the cleanup deadline, which the evidence capture of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md#freezing-the-agent) does not expect. "WM-3" also restates the parts of the protocol that `0145-session-isolation` owns, so the two statements can drift.

- (a) 0145.S3 is the only owner of the worker, the refusal rule, and the `freeze` exception, and SIR-23 stands. WMR-12 and WMR-14 point to SIR-23, scoped to `setup` runs, "WM-3" keeps only the `setup` members, the helper's descriptor layout, and the helper-specific checks and points to SI-3 and SI-5 for the rest, and 0153.S1 keeps the `setup` members, the helper's descriptor layout, and the adapter's per-slot run queue.
- (b) 0153.S1 owns the worker under WMR-12, `cleanup` runs get a cancel pipe, and SIR-23's exception leaves.

**Recommendation: (a).** `0145-session-isolation` owns the `CH-SUPERVISE` protocol, and (a) needs no new descriptor on `cleanup`. It blocks 0153.S1. The other changes are `0145-session-isolation`, whose O20 records the same question, and `0128-forensics-evidence-preservation`, whose FE-1 relies on the answer. (raised by the M3 readiness review, 2026-11-19; sources: 0153-F4, 0153-F5, 0153-F19, pod-structure-C6, checkpoint-workspace-C3, pod-structure-C7)

**O15. Which process emits `lenny_upload_extraction_aborted_total` and the new materialization metrics. Open.**

The gateway counts `lenny_upload_extraction_aborted_total` today through `GWSEAM-RecordExtractionAbort` (`pkg/gateway/uploadhandler/handler.go`), which 0153.S3 removes as extraction leaves the gateway. The §16.1 row keeps the metric "counts slot-reported reasons" without naming its emitter, and names no emitter for `lenny_materialize_duration_seconds`, `lenny_materialize_result_refused_total`, `lenny_setup_cancelled_total`, `lenny_prestage_adopted_total`, `lenny_prestage_skipped_total`, or `lenny_materialize_reflink_total`. An adapter-emitted metric depends on the adapter metrics endpoint, which N4 of the naming law in §28 defers to a remediation step.

- (a) The gateway counts the extraction-abort reasons from the error `FinalizeWorkspace` maps, through a reshaped `GWSEAM-RecordExtractionAbort`, and the metrics that only the adapter observes, the `lenny_prestage_*` series and the reflink method among them, wait for the adapter metrics endpoint, with 0153.S8 depending on the step that adds it.
- (b) The gateway counts every metric it can derive from the adapter's replies, the extraction-abort reasons, the run outcome, and the duration it measures, and only the metrics no gateway call carries wait for the adapter metrics endpoint.
- (c) The adapter emits all of them, and 0153.S3 and 0153.S8 depend on the step that adds the adapter metrics endpoint.

**Recommendation: (a).** It keeps the existing metric where it is emitted today and adds no adapter dependency to 0153.S3. It blocks 0153.S3 (the seam operation and the §16.1 row) and 0153.S8. (raised by the M3 readiness review, 2026-11-19; sources: 0153-F8)

**O16. Where a mid-session upload's bytes wait, and whether a mid-session upload accepts archives. Open.**

"WM-4" assumes that "Upload bytes stay with the gateway as the Upload Handler stored them", and "WM-12" runs `overlay` with a mid-session upload's sources, "archives included". In the tree a mid-session upload arrives as base64 JSON under `uploadToSessionMaxTotalBytes`, is never stored as a blob, and reaches the session core through `GWSEAM-WriteWorkspace` as `[]WorkspaceFile` contents (`pkg/gateway/uploadhandler/upload_to_session.go`, `pkg/gateway/seam/core.go`). Neither this change nor [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) states whether the client API accepts archives on `upload_to_session`.

- (a) The gateway stores a mid-session upload's files as blobs, as it stores pre-start uploads, and serves them one source at a time as "WM-4" states; the client API accepts archives on a mid-session upload under the §7.4 ceilings.
- (b) The gateway holds the decoded files in memory for the length of the mid-session `FinalizeWorkspace` call and serves them one source at a time; a mid-session upload stays files-only, and "archives included" leaves "WM-12".
- (c) As (a), with a mid-session upload kept files-only.

**Recommendation: (a).** It gives pre-start and mid-session uploads one storage path, keeps gateway memory free of upload bytes as WMS-2 states, and matches "WM-12" as written. The client API part needs the agreement of `0138-rest-only-client-api`. Under any option 0153.S5 reshapes `GWSEAM-WriteWorkspace`. It blocks 0153.S5. (raised by the M3 readiness review, 2026-11-19; sources: 0153-F8)

**O17. Which step owns the `capture-copy` and `restore-complete` wiring, given that 0131.S1 and 0153.S4 each need the other. Open.**

IC-1 of `0131-incremental-checkpoint` runs the helper's `capture-copy` before every pre-copy pass, and IC-5 runs `restore-complete` after a restore. 0153.S4 builds both subcommands and depends on [0131.S1](../0131-incremental-checkpoint/steps.md#steps), so an edge from 0131.S1 or 0131.S5 to 0153.S4 would close a cycle, and the `integration` test that runs `capture-copy` before pre-copy has no step that can pass it.

- (a) A new or reshaped step of this change, ahead of 0131.S1, builds `capture-copy` and `restore-complete`, and 0131.S1 and 0131.S5 depend on it; 0153.S4 keeps `gitClone`, the tree-size check, the index refresh, and the fetch-grant client.
- (b) 0153.S4 lands the adapter's calls to both subcommands itself, owns the `integration` test, and drops 0131.S1 from its depends-on, with 0131.S1 leaving the hook.
- (c) 0131.S1 and 0131.S5 carry `0153.S4 (part)` edges scoped to the calls, which leaves a cycle through a part edge.

**Recommendation: (a).** It orders the helper before both of its callers without a part-edge cycle. It blocks 0153.S4, 0131.S1, and 0131.S5. The other change is `0131-incremental-checkpoint`, whose D27 records the same question. (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C2)

**O18. How a session under `sealOnly` or `none` resumes after it loses its pod. Open.**

IC-8 of `0131-incremental-checkpoint` resumes such a session with `resumeMode: conversation_only` and `workspaceLost: true` from the §4.4.5 minimal state record. That record is written only when an eviction checkpoint fails, which these policies never take, so §7.3 step 3e, SL-6 of `0146-session-lifecycle`, D19 of `0140-runtime-revisions`, and the snapshotless rebuild row of "WM-13" send the session through the snapshotless rebuild, which reruns setup commands and carries no conversation state.

- (a) Under `sealOnly` and `none` the preStop path and the pod-failure path write the minimal state record without a checkpoint, so IC-8's statement holds, and "WM-13" gains a row for a resume from the minimal state record with the workspace handling that IC-8 states.
- (b) IC-8 adopts the snapshotless rebuild for these policies, and "WM-13" stays as written.

**Recommendation: (a)**, which the consistency review recommends, because it keeps the conversation that a runtime author who chose `sealOnly` expects to survive. It blocks the "WM-13" table of 0153.S6. The other changes are `0131-incremental-checkpoint`, whose D30 records the same question, `0146-session-lifecycle`, and `0140-runtime-revisions`. (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C4)

**O19. Which step renders the `lenny-store-fill` init container, and how 0118.S4, on which 0153.S2 and 0153.S9 depend, is ordered against 0145.S1. Open.**

0153.S9 lands the `lenny-store-fill` program and states that [0118.S4](../0118-agent-pod-layout/steps.md#steps) renders the init container and its volumes; 0153.S2 depends on 0118.S4 for `lenny-spool`. O11 of [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md) records that 0118.S4's per-slot volumes and the slot containers of [0145.S1](../0145-session-isolation/steps.md#steps) cannot land apart without an insecure or broken intermediate state, and that the disk watch waits on 0145.S2 and 0131.S8. Its options (a) and (b) move the rendering of `lenny-store-fill` onto 0153.S9's branch with its program.

- (a) As option (a) of 0118's O11: 0118.S4 and 0145.S1 share one branch, the disk watch becomes a step of its own in 0118, and 0153.S9 renders the `lenny-store-fill` init container on its own branch with the program, keeping its edge to 0118.S4 for the volumes.
- (b) As option (b) of 0118's O11, with `lenny-store-fill` rendered by 0153.S9 as in (a).
- (c) 0118.S4 keeps rendering the init container, as 0153.S9 states today, and only the order of 0118.S4 against 0145.S1 changes.

**Recommendation: (a)**, as 0118 recommends. A program and its rendering on one branch let the step's `security` test of the rendered pod run against the program it renders. It blocks 0153.S2 and 0153.S9. The matching decision is O11 of `0118-agent-pod-layout`, which also names `0145-session-isolation` and `0131-incremental-checkpoint`. (raised by the M3 readiness review, 2026-11-19; requested by change 0118; sources: 0118-F1, 0118-F3, pod-structure-C1, pod-structure-C7)

**O20. The usable size of a slot volume against which this change's size checks compare, and what a disk-watch trip does while no session is bound. Open.**

"WM-15" charges the staged trees against the slot's full `workspaceSizeLimitBytes`, and the helper's running bound in "WM-10" and the §4.4.4 row compare against the same limit. The disk watch of [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) trips at `adapter.diskWatchThresholdPercent` of the volume's `sizeLimit`, 90% by default, and a project quota stops writes at `sizeLimit` less 2%. A staged tree between the threshold and the limit passes "WM-15"'s check and trips the watch on a slot with no session, and a plan close to the limit passes the check and fails with `ENOSPC` in the helper.

- (a) The usable size of a slot volume is `adapter.diskWatchThresholdPercent` of its `sizeLimit`. Every size check of this change, the tree-size check of `prestage` and of `gitClone` and the helper's running bound, compares against it, and a trip with no session bound discards the staged trees and keeps the slot.
- (b) The checks stay at `workspaceSizeLimitBytes`, and a trip with no session bound fails the slot.

**Recommendation: (a)**, as 0118 recommends. A check that admits what the watch then fails gives the deployer a limit that does not hold, and a staged tree is the platform's own and can be discarded. It blocks 0153.S8. The matching decision is O13 of [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md), which also names `0131-incremental-checkpoint`. (raised by the M3 readiness review, 2026-11-19; requested by change 0118; sources: 0118-F6)

**O21. What a pod with a non-empty warm list does on a pool that runs no `netagent`. Open.**

"The store fill" in "WM-9" and WMR-28 send every fetch of `lenny-store-fill` through `netagent`, which attributes the fill's UID. [`0127-egress-and-git`](../0127-egress-and-git/change.md) has pools with `egressProfile: internet` that run no `netagent`, and a public `gitRepository` entry of a Runtime's `sharedAssets` goes onto the warm list of every pool of that runtime ([`0147-tenant-derived-runtimes`, "Warm stores"](../0147-tenant-derived-runtimes/change.md#warm-stores)). Neither this change nor the pod layout states what such a pod does.

- (a) Pool admission refuses a pool whose warm list is not empty when the pool runs no `netagent`.
- (b) The controller renders `netagent` on every pod whose warm list is not empty.
- (c) The pod renders no `lenny-store-fill` and skips the warm list, so no store is filled and `prestage` stages nothing.

**Recommendation: (a)**, as 0118 recommends. It states the combination as a configuration error at write time, keeps the egress design's choice of which pools run `netagent`, and leaves no pod that silently lacks its stores. It blocks 0153.S9. The matching decision is O14 of [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md), which also names `0127-egress-and-git` and `0147-tenant-derived-runtimes`. (raised by the M3 readiness review, 2026-11-19; requested by change 0118; sources: 0118-F7)

**O22. The commit key a slot repository records. Open.**

"WM-10" step 5 and the `prestage` steps of "WM-15" write the config keys `lenny.gitStoreUrl` and `lenny.baseCommit`, and `restore-complete` "fetches `lenny.baseCommit` when the pod's store differs". [`0127-egress-and-git`](../0127-egress-and-git/decisions.md) keeps draft proposal 0111's `lenny.storeCommit` beside `lenny.gitStoreUrl`. Telling whether the restoring pod's store is the one the repository borrowed from needs the capturing pod's store commit, which the URL alone does not identify.

- (a) One key, `lenny.baseCommit`, as "WM-10" states, and 0127 drops `lenny.storeCommit`.
- (b) Both keys: `lenny.baseCommit` for the commit the session checked out, and `lenny.storeCommit` for the store's commit at materialization, which `restore-complete` compares with the pod's store; "WM-10" and "WM-15" record both and `restore-complete` reads both.

**Recommendation: (b)**, as 0127 recommends, because restore completion has to tell whether the restoring pod's store is the one the repository borrowed from. It blocks 0153.S4, 0153.S6, and 0153.S8. The matching decision is decision 70 of `0127-egress-and-git`; its decision 69 records where the rules of the 0111 rewrite that no member change states are written, which the store parts of 0153.S4, 0153.S8, and 0153.S9 also wait on. (raised by the M3 readiness review, 2026-11-19; requested by change 0127; sources: 0127-p1-F1)

**O23. How the child-file import is ordered against 0153.S5. Open.**

"WM-12" writes a child-file import through `overlay`, which 0153.S5 builds, and 0153.S5 depends on `0135.S5 (part)` for the `ImportFile` stream of "MD-26" in [`0135-message-delivery`](../0135-message-delivery/change.md#md-26-files-from-a-child). 0135.S5 neither depends on 0153.S5 nor marks its import as a part, so either 0135.S5 ships an import that cannot write or the two steps wait on each other.

- (a) A new step of 0135 carries "MD-26" and depends on 0135.S5 and 0153.S5, and 0153.S5 keeps its `0135.S5 (part)` edge, naming the `ImportFile` stream.
- (b) 0135.S5 takes `0153.S5 (part)` for the import's write, and 0153.S5 keeps `0135.S5 (part)` for the stream.

**Recommendation: (a)**, as 0135 recommends. A separate import step makes the wait an edge that the board reads. It blocks 0153.S5. The matching decision is O18 of [`0135-message-delivery`](../0135-message-delivery/decisions.md). (raised by the M3 readiness review, 2026-11-19; requested by change 0135; sources: 0135-p2-F11)

**O24. Which step closes F-8.7.17. Open.**

F-8.7.17 (High in `BUILD-GAPS.md`) is the race in `ExportPaths`, which checks a resolved path and then reads by path, so a parent agent that wins the race exports a file such as the adapter's token. This change keeps `ExportPaths` "through the broker", and the file-descriptor broker of [`0145-session-isolation`](../0145-session-isolation/change.md#si-5-the-supervisor-file-descriptor-broker) (0145.S2) is the fix the finding proposes. No step of any change lists the finding in `closes`.

- (a) 0145.S2 lists F-8.7.17 in `closes`, because its broker is the mechanism that closes the race, and this change adds no `closes` entry.
- (b) A step of this change that moves `ExportPaths` onto the broker lists F-8.7.17 in `closes`.

**Recommendation: (a)**, as 0135 recommends. The broker closes the race, and this change only reads through it. It blocks no step of this change under (a); under (b) it adds a `closes` entry to the step that moves `ExportPaths`. The matching decision is O19 of [`0135-message-delivery`](../0135-message-delivery/decisions.md), which also names `0145-session-isolation`. (raised by the M3 readiness review, 2026-11-19; requested by change 0135; sources: 0135-p1-F9, 0135-p2-F12)
