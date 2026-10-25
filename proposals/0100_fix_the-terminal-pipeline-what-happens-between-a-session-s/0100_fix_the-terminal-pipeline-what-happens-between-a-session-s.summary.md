# Summary: Terminal pipeline completeness, part A: one bounded platform-owned context for terminal work, a shutdown wait on the terminal claim record, and seal-correctness fixes

## Summary

**Problem statement.** After a terminal write commits, the D7 post-terminal hook runs the seal and export, the inbox drain, the terminal events and signals, and the pod or slot release on whatever context the writer passes. That context is unbounded for the REST and MCP writers, tied to the request for admin force-terminate and `failFinalizing`, and 30 s for the budget terminator, which is shorter than the 300 s seal window. Shutdown waits 5 s and then cancels the server, so every rolling deploy cuts open hooks. Inside the hook the seal can report success for a workspace it did not export, let a turn run during the export, keep exporting a deleted session, release with the clean disposition after a failed relabel, and retry a permanent condition for the whole window. Post-resume cleanup can delete a live seal intent, retention GC can collect before the terminal retention roll, a write whose commit acknowledgement is lost skips its post-commit tail, the derive-failure fence runs outside its transaction, and terminate rejects the internal `resuming` state. §7.2 and §15.1 also disagree on the state terminate yields from `resume_pending`. The spec states no rule for the context or bound of terminal work, for a shutdown wait, or for a write whose commit outcome is unknown, and P2b and P3 TT-44 need such a rule to cite.

**What changes.**

- §7.1 gains **Terminal pipeline context:** with the terminal pipeline budget and **Post-terminal window:** with the release marker, and the **Seal-and-export invariant:**, **Artifact retention:**, and derive rule 1 gain the seal-outcome, retention, and derive rules (SPEC-1, SPEC-2, SPEC-3).
- §10.1.7 and §29.7 state the SIGTERM wait and its departure, §12.3 states the lost-acknowledgement row, §5.2 re-bases its slot invariant, rehydration seed, and stale-member release, §4.6.1 its orphan predicates and pool-drain finalizer rule, and §10.1.7 the deadline of a checkpoint upload's intent row, and §7.2 and §6.2 align terminate to §15.1 (SPEC-4 to SPEC-7).
- `pkg/gateway/sessionserver` bounds the hook once at its entries and waits on `terminalClaimRecord` at shutdown, and `cmd/lenny-gateway` reorders the shutdown sequence (CODE-1, CODE-2).
- `pgstore.Store.Update` confirms a write by the identifier it stored, after a commit error (CODE-3).
- The seal step, the pod executor, and the Checkpointer fix the seal outcome, gate delivery during the seal, give each checkpoint intent row a deadline, and release a binding whose row is gone (CODE-4, CODE-5).
- CODE-7 adds the release marker and the post-terminal window predicate; the §5.2 gates, retention GC, resume cleanup, derive-failure insert, terminate precondition, and the §12.8 store deletions get fixes, TEST-1 pins every change, and REC-1 records the resolutions in `BUILD-GAPS.md` (CODE-6, CODE-7, TEST-1, REC-1).

**Decisions.**

- D1. The context is detached and bounded once, at `AfterTerminalWrite` and `recordSessionCompleted`, which every hook writer reaches directly or through `OnSessionTerminal`. A per-writer rule repeats at more than ten sites and the next writer misses it.
- D2. Terminal pipeline budget = the largest `maxWorkspaceSealDurationSeconds` of any pool + the existing `--stream-failure-report-timeout` (the release allowance, default 30 s), the bound `ReportAttachStreamFailure` already uses for the same post-seal work, as one deployment-wide number, because the window cap, the store-side bounds, and the shutdown wait span pools. No new flag.
- D3. The budget is not derived from the server-lifetime context. Cancelling on `Close` would cut every open seal at shutdown and record false `timeout` outcomes and `WorkspaceSealStuck` alerts. Shutdown waits instead, and the kubelet SIGKILL is the outer bound.
- D4. The REST, MCP, and watchdog terminal Updates keep their caller context; an Update that never commits applies nothing, and CODE-3 resolves a lost acknowledgement. Only `failFinalizing` (a non-commit strands the row in `finalizing`) and admin `ForceTerminate` with its audit emit detach their write.
- D5. The budget terminator opens its claim synchronously in `TerminateSession`, so a SIGTERM racing the goroutine still sees it.
- D6. Shutdown polls the existing `terminalClaimRecord.size()`, which already counts writer claims across the hook and marks D6 releases. No new tracking set.
- D7. F-7.1.23 is fixed with a closing mark on `BindResult` that `streamFor` checks, plus an eviction of the cached Attach stream at seal start. A turn during the export changes the sealed workspace, which is the output loss the problem names. A delivery-lock barrier deadlocks, because `endConn` runs the stream-failure handler before `close(c.done)` while a Send holding the delivery lock waits on `c.done`.
- D8. The closing refusal surfaces from `Send` as `&SealedError{Delivered: 0}`, so every caller's existing sealed-reroute branch handles it with no caller change.
- D9. A binding that disappears during the seal and a row absent on the primary end the window with `permanent`. Adapter gRPC `NotFound` stays transient because its meaning at the adapter is ambiguous.
- D10. F-7.1.24: the Checkpointer confirms absence on the primary with `ReadPrimary` and releases through the existing D6 `ReleaseTerminalBinding` with a synthesized `failed` row. Without the release, the binding never leaves the registry, and the replica read would release live, just-bound sessions under lag.
- D11. The §5.2 slot invariant is re-based on the terminal pipeline budget, and the §4.6.1 orphan predicates count a session that holds its pod (D22). Without the second change the controller drains the pod mid-seal, because a long session's `bound` claim passes `claimOrphanTimeout` from its binding transition before the session ends. With the first, the stale-slot reaper, whose terminal age is `claimOrphanTimeout`, never reclaims a slot the terminal pipeline still uses.
- D12. `POST /terminate` yields `completed` from `resume_pending` and `resuming`, by aligning §7.2 and §6.2 to §15.1; CODE-7 item 4 makes the code change. §15.1 is the endpoint table and contrasts terminate with DELETE for audit and billing. The owner's delegate confirmed it on 2026-10-24 under the owner's overnight delegation: §15.1 is the client-facing contract, and a terminate during an automatic restore ends the session as the caller asked.
- D13. F-15.1.42 is resolved by merging derive rule 1's `completed`, `failed`, and `cancelled`/`expired` bullets into one sealed-else-checkpoint rule. The code already conforms and reports the source. The hook seals every terminal session, and derive copies whichever snapshot the row records, so a `failed` source whose seal succeeded already derives from the sealed workspace.
- D14. F-7.1.27 is closed by a spec clause with no code change: for a slot session "terminates the pod anyway" means the §5.2 slot cleanup and release, which `Binder.ReleaseSlot` already performs.
- D15. F-7.2.29 is resolved in `pgstore.Store.Update`, which stores a fresh `last_write_id` in every update and, after a commit error, reads it back from the primary. A transaction-ID status check (`pg_xact_status`) is not used, because a primary promoted by a failover, or restarted after a crash, can reassign the ID of a transaction whose WAL it never made durable. `updated_at` does not identify a write, because `MonotonicNext` stamps the same `prev + 1µs` on replicas whose clocks lag `prev`.
- D16. F-7.3.32 is resolved by scoping post-resume cleanup by coordination generation. A crashed attempt's intent row also carries `in_progress`, so an `in_progress` skip would leave exactly the residue cleanup exists to reclaim.
- D17. F-15.1.44 is resolved by a new `CreateIfGeneration` store method that reads the source generation `FOR SHARE` inside the INSERT transaction, following `fenceInsert`.
- D18. F-7.1.28 is resolved in the retention GC, which collects nothing until the session's retention window has elapsed from `TerminatedAt`, which the terminal write commits. A cut pipeline, a failed roll, or a misclassified terminal write therefore cannot shorten retention, and the terminal roll stays a best-effort step. Rolling the deadline inside every terminal write needs the window on every Create path, and the store layer has no tier lookup.
- D19. Recorded departures: (a) terminal work cut by SIGKILL or a crash (SPEC-4); (b) a committed terminal write that the SPEC-5 check classifies as not applied. (c) A budget termination lost to a crash before its write is process-local enforcer state owned by F-11.2.31 (P4).
- D20. F-12.8.43's remote binding is released by CODE-5 within one checkpoint interval on the binding replica, and `ReleaseTerminalBinding` returns its credential leases. The entry stays OPEN for the cross-replica release under the inter-replica carrier proposal.
- D21. The post-terminal window is stated once, in SPEC-1 (a), and each component that treats a terminal session as finished was checked against it (review log `[mechanism.post-terminal-window]`). Three components needed edits beyond SPEC-6 (a) and (b). First, the §12.5 backstop selects an `in_progress` row of a terminal session once `checkpoint_timeout_at` passes, and the tree stamps a seal row's `checkpoint_timeout_at` at its start, because production leaves `Checkpointer.Deadline` zero. The backstop therefore deletes the chunks a live seal has already uploaded, and the seal can still record a sealed snapshot over them, because `ConfirmChunk` and `Finalise` return nil on a soft-deleted row. A periodic checkpoint that the sweep starts after the terminal commit runs on a context with no deadline, so its row is exposed in the same way, and the checkpoint it records is the fallback derive rule 1 uses after a failed seal. SPEC-6 (c) and CODE-4 item 9 give every row the deadline its attempt runs under, and CODE-4 item 5 stops any non-seal attempt from starting on a sealing binding. Second, retention GC under a tier window shorter than the budget collected a seal's chunks mid-attempt; CODE-7 item 1 holds it to the post-terminal window (D22). Third, the §12.8 store deletions started right after the drain, so a seal still running uploaded after the erased session's artifacts were deleted. SPEC-1 (e) holds them to the SPEC-1 (a) window, and CODE-7 item 6 gates them on it. A recorded departure has no bounding fallback, because retention GC works from session rows and the erasure deletes the row.
- D22. The post-terminal window ends when the release is recorded in `sessions.pod_released_at`, which the replica that released the session's binding, or reclaimed its pre-running claim, writes after that release. `TerminatedAt` plus the budget caps the window for a release that never records (owner's delegate, 2026-10-25). The release is what frees the resources, and a timer cannot see a release that finished early or a pipeline still running. The marker is a new column because the §8.8 usage rollup, the §8.9 node projection, and the admin session view read `pod_assignment` on terminal rows. One predicate (CODE-7 item 5) serves the §5.2 seed and stale-member release, the §12.4 gate, the §4.6.1 predicates, retention GC, and the §12.8 gate (CODE-7 item 6). The controller caps the window at `claimOrphanTimeout` (open decision 1). The stale-slot reaper keeps its `claimOrphanTimeout` age, because that age also bounds the launch and restore windows of §5.2, which no release marker observes.

**Watch out for.**

- `WaitTerminalWork` must run before `sessionSrv.Close()`. `releaseAfterTerminal` takes the slot lock on `s.lifetime()` and skips the release once it is cancelled (CODE-2).
- `traceShutdown` currently runs first on the 5 s context; it moves after `Close()` on the fresh context, or the spans emitted during the wait are dropped (CODE-2).
- pgstore `Get` reads the replica. The absent-row check uses `ReadPrimary`; using `Get` releases live sessions under replica lag (CODE-5).
- REST `deliverNow` routes on `errors.As(*executor.SealedError)`. Returning the bare `ErrSessionSealed` from `Send` produces `EXECUTOR_FAILURE` on REST (CODE-4).
- Do not hold the delivery lock across the seal to stop a turn; it deadlocks against `endConn` (D7).
- Do not change `BeginTerminal`'s signature to return a context. It is duplicated across seam interfaces in orphancleanup, mcptools, watchdog, orphansession, admin/users, and cmd.
- The existing retentiongc tests seed terminal rows without `TerminatedAt`; after CODE-7 those rows are ineligible and the tests must seed it.
- Background loops open claims during the shutdown wait. A test must treat a claim opened mid-wait as extending the wait up to the bound rather than as a hang.
- The chart does not render `--stream-failure-report-timeout`, so the chart gate uses the 30 s default and the gateway startup gate stays the complete check. No Go pool-admission comparison exists to edit (CODE-6).
- `TestSlotOccupancyIsAKeyedSetAtEverySite_spec_5_2` requires the §5.2 sentence to keep naming `maxWorkspaceSealDurationSeconds`; SPEC-6 keeps it.
- TEST-1 items 10, and 12's terminate cases for states other than `resuming`, pass on the pre-fix tree by design. Do not distort them to make them fail.
- The `failed` disposition after a seal failure comes from the hook's own `sealFailed` flag; the relabel's returned row must not decide it (CODE-4 item 8).
- A session store built without `WithPostTerminalCap` counts no terminal row as holding its pod, which is the tree's behavior. TEST-1 item 14 pins both production store constructions (CODE-7 item 5), and TEST-1 item 16 (e) pins the cap handed to retention GC and the §12.8 gate (CODE-7 items 1, 5 (e), and 6).
- An erasure runner built without `WithReleaseGate` fails every job at `store_delete`, so every runner fixture wires one (CODE-7 item 6).

## Goals

- Every terminal pipeline runs on a gateway-owned context bounded by one stated budget, whatever the writer.
- Gateway shutdown waits for open terminal pipelines within the budget and the grace period, and the residual is stated as a bounded departure.
- The seal never reports success for a workspace it did not export, and no turn runs during the export.
- A write whose commit acknowledgement is lost runs its post-commit steps when the transaction committed.
- Retention GC, post-resume cleanup, the derive-failure fence, and the terminate precondition match the spec.
- §7.2, §6.2, and §15.1 state one terminate outcome for the recovery states.
- P2b and P3 TT-44 have a spec rule to cite.

## Non-goals

- A delivery-lock barrier in the hook for F-7.1.23. It deadlocks (D7), and `completeResume` holds the delivery lock across its tail.
- Recording F-7.1.23 as a departure. The closing gate is one in-memory flag on existing surfaces (D7).
- Reusing `SealSession` (idle window 0) or a new adapter RPC as the terminal seal barrier. §4.7.1 makes the seal idle-conditional, and a new RPC adds a wire contract to every terminal path.
- Reusing the `MarkSealed`/`GrantSealed` epochs for the closing gate. They carry adapter-grant semantics for idle suspension, and overloading them risks double delivery on an unseal of unknown outcome.
- Making Sweeper dead-channel eviction and translator `releaseSingleShot` defer to an open seal. A dead or taken-over channel cannot be sealed from this replica, so reporting the lost binding as permanent resolves the false success at one site; translator release belongs to P2b.
- Changing `BeginTerminal` to return the terminal context (D1).
- A per-writer context rule or `context.WithoutCancel` at every writer site (D1).
- Deriving the hook context from the server-lifetime context (D3).
- Detaching the REST, MCP, and watchdog terminal Update (D4).
- A new tracking set or WaitGroup for open hooks and D6 releases (D6).
- A new `--terminal-hook-budget` flag or a fixed spec constant for the release allowance (D2).
- Raising `--shutdown-timeout` or the gateway `terminationGracePeriodSeconds` to cover a 300 s seal. It changes the §10.1.7 and §17.8 grace budgets for every install; the residual is a departure (D19).
- Implementing the shutdown wait as a preStop stage-3 step. Stage-3 polling is not implemented in `prestop.go`, preStop does not run in source mode, and a terminal write can start after preStop returns.
- Aborting open seals 10 s before the grace deadline with a non-timeout outcome. It adds an abort path that marks seals `failed` that would have succeeded.
- Bounding the shutdown wait by `LENNY_TERMINATION_GRACE_SECONDS`. The budget keeps the bound one number, and the kubelet enforces the grace period.
- A durable outbox, a peer re-seal of a terminal session, or a `seal_state` column. Each adds a table, state, or adoption path and depends on the inter-replica carrier.
- Re-reading after an Update error at each writer site. The single store site also covers recovering-edge writers (D15).
- `pg_xact_status` on the transaction ID, or `updated_at` as the token (D15).
- A 503 `DERIVE_SNAPSHOT_UNAVAILABLE` or 409 refusal during a window after termination. It refuses collapse-path `completed` sessions that never seal (D13).
- A new derive error code, a derive that waits for the seal, or holding the derive lock across the seal (D13).
- Rolling `RetentionExpiresAt` inside every terminal write (D18).
- Counting a slot seal failure toward the §5.2 whole-pod replacement trigger, or terminating the whole concurrent-workspace pod. It kills sibling sessions against §5.2 failure isolation (D14).
- Treating adapter gRPC `NotFound` as a permanent seal error (D9).
- Skipping the relabel and audit for an absent row. The relabel fails harmlessly with `ErrNotFound`, and the release uses the failed disposition.
- Terminate from `resume_pending`/`resuming` writing `cancelled` with a §15.1 amendment (D12).
- Implementing the `resume_pending → completed` "already finished" budget and lease determination in REST terminate. No machinery performs it, and SPEC-7 removes the example that tied it to terminate.
- Scoping resume cleanup by the resolved `checkpoint_id`. The generation separates live work from residue (D16).
- Seeding the budget enforcer from §8.8 durable totals, a durable budget-terminate retry, or a §11.2 departure paragraph (D19 (c)).
- An `onApplied` audit callback on `ForceTerminate` that emits before the hook. The detached emit after the synchronous call loses the row only to a SIGKILL during the hook, which the shutdown wait bounds.
- Returning from admin force-terminate before the hook finishes. The §12.8 lifecycle drain and erasure paths rely on a synchronous release.
- Narrowing SPEC-1 (e) to a recorded departure for the §12.8 store deletions (D21).
- A no-spec-change proposal that closes every member as a direct fix. The writer-context rule, the shutdown wait, and the unknown-commit resolution are spec-silent.
- One terminal-signal set for every writer (F-7.2.35, P2b).
- Off-holder terminal writes (F-10.1.33, TT-29) and the cross-replica release of F-12.8.43, deferred to the inter-replica carrier proposal.
- The terminal-writer half of F-4.9.33, pending the owner's P2a or P2b placement decision.

## Open decisions for human to make

None. D12 was confirmed by the owner's delegate on 2026-10-24. On 2026-10-25 the owner's delegate settled the three remaining decisions as the staged text applies them: the warm-pool controller caps the post-terminal window at `claimOrphanTimeout`, which §5.2 already keeps above the terminal pipeline budget, so no controller copy of the budget can be set below it; the stale-slot reaper keeps its `claimOrphanTimeout` terminal age, because that age also bounds the §5.2 launch and restore windows that no release marker observes; and the §12.3 Tier 3 write estimate stays unchanged, because the release-marker write adds about 30 writes per second at the sustained rate, inside the estimate's rounding, and bursts fall under the table's existing burst note.

## Defects in the shipped tree that this proposal does not stage

- §5.2 says "pool admission refuses a pool" whose configuration does not meet the `claimOrphanTimeout` bound, but no Go pool-admission check enforces it; only the chart `fail` gate does. Building an admission check is a separate surface from the bound this proposal re-bases, so REC-1 files it as a finding.
- F-11.2.31: a budget termination lost to a crash before its write is not retried, because the enforcer state is process-local. It belongs to P4.
- F-7.2.35: writers do not share one terminal-signal set. It belongs to P2b.
- F-10.1.33: a terminal write made off the coordinating replica. It depends on the inter-replica carrier proposal.
- F-12.8.43 (BUILD-GAPS.md:24696): user erasure can delete the row of a drained session bound on another replica before that replica releases the session's pod. `admin.DrainSessions` (`pkg/gateway/externalapi/admin/drain.go:82`) writes the terminal row on the draining replica, and the binding replica releases only through `ReleaseTerminalBinding` (`pkg/gateway/sessionserver/terminal_binding.go:44`) after it lists that row. Releasing the binding before the row is deleted needs the inter-replica carrier proposal. This proposal narrows the residual through CODE-5 and leaves the entry OPEN (see D20).
- F-4.9.33, terminal-writer half (BUILD-GAPS.md:3689): an exhausted credential fallback chain writes no terminal row. The handler terminates the session only through its `FallbackTerminator` field (`pkg/gateway/llmproxy/llmproxy/handler.go:267`, called at `pkg/gateway/llmproxy/llmproxy/fallback.go:132-133`), and no non-test code assigns that field. The remediation plan moves this half to P2 only pending owner confirmation (`gateway-runtime-comms-remediation.md:2814`), and placing it in P2a or P2b is the owner's call, so this proposal stages nothing for it. No staged change depends on a fallback terminator existing.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0092 | Implemented | Bounds the context of the D7 hook 0092 built and closes the seal writer-context question it handed to P2. | Nothing; an implemented proposal is not edited. |
| 0093 | Implemented | Leaves its F-10.1.33 accepted departure standing and moves the entry's owner to the inter-replica carrier proposal. | Nothing. |
| 0094 | Implemented | Closes the `session.force_terminated` half of F-11.7.22 that 0094 left to P2. | Nothing. |
| 0095 | Implemented | Narrows the bound of its F-12.8.43 departure to one checkpoint interval on the binding replica. | Nothing. |
| 0097 | Approved (2026-10-24) | Both edit `cmd/lenny-gateway/flags.go`, `charts/lenny/templates/gateway-deployment.yaml`, `charts/lenny/values.yaml`, and `pkg/alerting/rules/rules.go`, and both run `make generate`, which rewrites `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml`. CODE-6 here edits the help text of `--slot-reaper-terminal-age` and `--max-resuming-seconds`, the chart's §5.2 seal-window `fail` gate, and the `controller.claimOrphanTimeoutSeconds` comment in `values.yaml`. DOC-5 here edits the `WorkspaceSealStuck` Description in `rules.go`. 0097 edits other flags, other chart lines, other `values.yaml` keys, and other alert rules (its CODE-6, CODE-7, CODE-8, CODE-10, CODE-11, CODE-13, CHART-1, CHART-2, CHART-4, and CHART-5). No 0097 deliverable loses its subject, and every 0097 deliverable survives. | Whichever lands second rebases the textual conflict and reruns `make generate`. 0097 needs no re-review. |
| 0099 | Approved (2026-10-24) | Both edit comments in `charts/lenny/values.yaml`. CHART-1 there edits the `redis.cluster.addrs` and `capacityPlanning.singleTenantRedisTopology` comments, and CODE-6 here edits the `controller.claimOrphanTimeoutSeconds` comment. No 0099 deliverable loses its subject. | Whichever lands second rebases any textual conflict. 0099 needs no re-review. |
| P2b (plan step, no proposal yet) | Not drafted | Supplies the writer-context rule P2b depends on. | Cite §7.1 **Terminal pipeline context:**. |
| P3 TT-44 (plan step, no proposal yet) | Not drafted | Supplies the bounded hook context TT-44 needs. | Cite §7.1 **Terminal pipeline context:**. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md, spec/29_communication-scenarios.md, spec/28_communication-channels.md, spec/12_storage-architecture.md — §7.1 **Terminal pipeline context:**, **Post-terminal window:**, **Gateway-owned terminal writes:**, and **Delivery and checkpoints during the seal:** paragraphs; terminal write before the seal in the §7.1 normal flow and §29.4; the §28.5.1 `CH-ATTACH` **Timing.** stream end; the §12.8 erasure and tenant-deletion wait.
- SPEC-2 — spec/07_session-lifecycle.md, spec/16_observability.md, spec/29_communication-scenarios.md — **Seal-and-export invariant:** seal outcomes and their §16.1, §16.5, and §29.4 restatements.
- SPEC-3 — spec/07_session-lifecycle.md, spec/12_storage-architecture.md — retention TTL start, derive rule 1 terminal-state bullet, §12.5 retention item.
- SPEC-4 — spec/10_gateway-internals.md, spec/29_communication-scenarios.md, spec/07_session-lifecycle.md — **Terminal pipelines at process exit.**, the §29.7 trace, and the **Seal-and-export invariant:** first-sentence pointer.
- SPEC-5 — spec/12_storage-architecture.md — §12.3 durability-table row for a lost commit acknowledgement.
- SPEC-6 — spec/05_runtime-registry-and-pool-model.md, spec/04_system-components.md, spec/10_gateway-internals.md, spec/28_communication-channels.md — §5.2 slot invariant, rehydration seed, and stale-member release; §4.6.1 orphan predicates and pool-drain finalizer rule; §10.1.7 intent-row deadlines; §4.4 and §28 deadline-fire retention.
- SPEC-7 — spec/07_session-lifecycle.md, spec/06_warm-pod-model.md — terminate yields `completed` from the recovery states.
- CODE-1 — pkg/gateway/sessionserver, cmd/lenny-gateway, pkg/gateway/externalapi/admin — bounded hook context and detached gateway-decided writes.
- CODE-2 — pkg/gateway/sessionserver/sessionserver.go, cmd/lenny-gateway/runserver.go — shutdown wait on the terminal claim record.
- CODE-3 — pkg/gateway/session/sessionstore/pgstore/pgstore.go, migrations/ — write-identifier check after a commit error.
- CODE-4 — pkg/gateway/sessionserver, pkg/gateway/checkpoint/checkpointer, pkg/gateway/session/executor, pkg/gateway/podlifecycle/podsession — seal outcome fixes, the closing gate, and the intent-row deadline.
- CODE-5 — pkg/gateway/checkpoint/checkpointer, cmd/lenny-gateway/pod_lifecycle.go — release of a binding whose row is gone.
- CODE-6 — cmd/lenny-gateway, cmd/lenny-controller, charts/lenny — §5.2 gates on the terminal pipeline budget.
- CODE-7 — pkg/gateway/storage/retentiongc, pkg/gateway/checkpoint/checkpointer, pkg/gateway/session/sessionstore, pkg/gateway/sessionserver, pkg/gateway/environment/translator, pkg/gateway/storage/slotcounter, pkg/gateway/storage/erasurejob, pkg/gateway/externalapi/admin, pkg/api/v1/session, pkg/controller/warmpool, pkg/controller/tenantdeletion, cmd/lenny-gateway, cmd/lenny-controller, migrations/ — retention GC guard, resume-cleanup scope, fenced derive-failure insert, terminate precondition, release marker and post-terminal window predicate, lifecycle store-deletion gate.
- TEST-1 — test files named in TEST-1 — tests across the reached tiers.
- DOC-1 — docs/operator-guide/troubleshooting.md — orphan-claim drain condition.
- DOC-2 — docs/operator-guide/multi-tenancy.md, docs/runbooks/tenant-deletion-overdue.md, tests/tier11_docs/data_lifecycle_fence_drain_doc_reconciliation_test.go — tenant-deletion Phase 2 completion condition.
- DOC-3 — docs/operator-guide/configuration.md, docs/operator-guide/disaster-recovery.md, tests/tier11_docs/stale_slot_reaper_config_doc_test.go — stale slot reaper floor and the pods the reaper and the slot-set rebuild cover.
- DOC-4 — docs/api/internal.md — Attach stream end after a session ends.
- DOC-5 — docs/reference/metrics.md, docs/runbooks/workspace-seal-stuck.md, docs/getting-started/concepts.md, docs/reference/state-machines.md, docs/tutorials/session-derive-replay.md, pkg/alerting/rules/rules.go and its generated files, tests/tier11_docs/checkpoint_presigned_capability_doc_reconciliation_test.go — seal outcomes and the terminal state during the seal.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md, scripts/seed-claim-register.py, tests/claim-map.json, tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go — resolutions, departures, and deferral notes.
