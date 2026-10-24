# Summary: Terminal pipeline completeness, part A: one bounded platform-owned context for terminal work, a shutdown wait on the terminal claim record, and seal-correctness fixes

## Summary

**Problem statement.** After a terminal write commits, the D7 post-terminal hook runs the seal and export, the inbox drain, the terminal events and signals, and the pod or slot release on whatever context the writer passes. That context is unbounded for the REST and MCP writers, tied to the request for admin force-terminate and `failFinalizing`, and 30 s for the budget terminator, which is shorter than the 300 s seal window. Shutdown waits 5 s and then cancels the server, so every rolling deploy cuts open hooks. Inside the hook the seal can report success for a workspace it did not export, let a turn run during the export, keep exporting a deleted session, release with the clean disposition after a failed relabel, and retry a permanent condition for the whole window. Post-resume cleanup can delete a live seal intent, retention GC can collect before the terminal retention roll, a write whose commit acknowledgement is lost skips its post-commit tail, the derive-failure fence runs outside its transaction, and terminate rejects the internal `resuming` state. §7.2 and §15.1 also disagree on the state terminate yields from `resume_pending`. The spec states no rule for the context or bound of terminal work, for a shutdown wait, or for a write whose commit outcome is unknown, and P2b and P3 TT-44 need such a rule to cite.

**What changes.**

- §7.1 gains **Terminal pipeline context:** with the terminal pipeline budget, and the **Seal-and-export invariant:**, **Artifact retention:**, and derive rule 1 gain the seal-outcome, retention, and derive rules (SPEC-1, SPEC-2, SPEC-3).
- §10.1.7 and §29.7 state the SIGTERM wait and its departure, §12.3 states the lost-acknowledgement row, §5.2 re-bases its slot invariant and §4.6.1 its live-binding orphan predicate, and §7.2 and §6.2 align terminate to §15.1 (SPEC-4 to SPEC-7).
- `pkg/gateway/sessionserver` bounds the hook once at its entries and waits on `terminalClaimRecord` at shutdown, and `cmd/lenny-gateway` reorders the shutdown sequence (CODE-1, CODE-2).
- `pgstore.Store.Update` confirms a write by the identifier it stored, after a commit error (CODE-3).
- The seal step, the pod executor, and the Checkpointer fix the seal outcome, gate delivery during the seal, and release a binding whose row is gone (CODE-4, CODE-5).
- The §5.2 gates, the orphan-GC session lookup, retention GC, resume cleanup, derive-failure insert, and terminate precondition get fixes, TEST-1 pins every change, and REC-1 records the resolutions in `BUILD-GAPS.md` (CODE-6, CODE-7, TEST-1, REC-1).

**Decisions.**

- D1. The context is detached and bounded once, at `AfterTerminalWrite` and `recordSessionCompleted`, which every hook writer reaches directly or through `OnSessionTerminal`. A per-writer rule repeats at more than ten sites and the next writer misses it.
- D2. Terminal pipeline budget = `maxWorkspaceSealDurationSeconds` + the existing `--stream-failure-report-timeout` (the release allowance, default 30 s), the bound `ReportAttachStreamFailure` already uses for the same post-seal work. No new flag.
- D3. The budget is not derived from the server-lifetime context. Cancelling on `Close` would cut every open seal at shutdown and record false `timeout` outcomes and `WorkspaceSealStuck` alerts. Shutdown waits instead, and the kubelet SIGKILL is the outer bound.
- D4. The REST, MCP, and watchdog terminal Updates keep their caller context; an Update that never commits applies nothing, and CODE-3 resolves a lost acknowledgement. Only `failFinalizing` (a non-commit strands the row in `finalizing`) and admin `ForceTerminate` with its audit emit detach their write.
- D5. The budget terminator opens its claim synchronously in `TerminateSession`, so a SIGTERM racing the goroutine still sees it.
- D6. Shutdown polls the existing `terminalClaimRecord.size()`, which already counts writer claims across the hook and marks D6 releases. No new tracking set.
- D7. F-7.1.23 is fixed with a closing mark on `BindResult` that `streamFor` checks, plus an eviction of the cached Attach stream at seal start. A turn during the export changes the sealed workspace, which is the output loss the problem names. A delivery-lock barrier deadlocks, because `endConn` runs the stream-failure handler before `close(c.done)` while a Send holding the delivery lock waits on `c.done`.
- D8. The closing refusal surfaces from `Send` as `&SealedError{Delivered: 0}`, so every caller's existing sealed-reroute branch handles it with no caller change.
- D9. A binding that disappears during the seal and a row absent on the primary end the window with `permanent`. Adapter gRPC `NotFound` stays transient because its meaning at the adapter is ambiguous.
- D10. F-7.1.24: the Checkpointer confirms absence on the primary with `ReadPrimary` and releases through the existing D6 `ReleaseTerminalBinding` with a synthesized `failed` row. Without the release, the binding never leaves the registry, and the replica read would release live, just-bound sessions under lag.
- D11. The §5.2 slot invariant is re-based on the terminal pipeline budget, and the §4.6.1 live-binding orphan predicate ages a terminal session from its terminal transition. Without the second change the controller drains the pod mid-seal, because a long session's `bound` claim passes `claimOrphanTimeout` from its binding transition before the session ends. Neither the stale-slot reaper nor the orphan GC can then reclaim a slot or pod the terminal pipeline is still using.
- D12. `POST /terminate` yields `completed` from `resume_pending` and `resuming`, by aligning §7.2 and §6.2 to §15.1; the only code change is admitting `StateResuming`. §15.1 is the endpoint table and contrasts terminate with DELETE for audit and billing. The drafting stage flagged this for owner confirmation; the review loop records it under **Open decisions for human to make**.
- D13. F-15.1.42 is resolved by merging derive rule 1's `completed` and `cancelled`/`expired` bullets into one sealed-else-checkpoint rule. The code already conforms and reports the source.
- D14. F-7.1.27 is closed by a spec clause with no code change: for a slot session "terminates the pod anyway" means the §5.2 slot cleanup and release, which `Binder.ReleaseSlot` already performs.
- D15. F-7.2.29 is resolved in `pgstore.Store.Update`, which stores a fresh `last_write_id` in every update and, after a commit error, reads it back from the primary. A transaction-ID status check (`pg_xact_status`) is not used, because a primary promoted by a failover, or restarted after a crash, can reassign the ID of a transaction whose WAL it never made durable. `updated_at` does not identify a write, because `MonotonicNext` stamps the same `prev + 1µs` on replicas whose clocks lag `prev`.
- D16. F-7.3.32 is resolved by scoping post-resume cleanup by coordination generation. A crashed attempt's intent row also carries `in_progress`, so an `in_progress` skip would leave exactly the residue cleanup exists to reclaim.
- D17. F-15.1.44 is resolved by a new `CreateIfGeneration` store method that reads the source generation `FOR SHARE` inside the INSERT transaction, following `fenceInsert`.
- D18. F-7.1.28 is resolved in the retention GC, which collects nothing until the session's retention window has elapsed from `TerminatedAt`, which the terminal write commits. A cut pipeline, a failed roll, or a misclassified terminal write therefore cannot shorten retention, and the terminal roll stays a best-effort step. Rolling the deadline inside every terminal write needs the window on every Create path, and the store layer has no tier lookup.
- D19. Recorded departures: (a) terminal work cut by SIGKILL or a crash (SPEC-4); (b) a committed terminal write that the SPEC-5 check classifies as not applied. (c) A budget termination lost to a crash before its write is process-local enforcer state owned by F-11.2.31 (P4).
- D20. F-12.8.43's remote binding is released by CODE-5 within one checkpoint interval on the binding replica, and `ReleaseTerminalBinding` returns its credential leases. The entry stays OPEN for the cross-replica release under the inter-replica carrier proposal.

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
- TEST-1 items 10, and 12 for states other than `resuming`, pass on the pre-fix tree by design. Do not distort them to make them fail.
- The `failed` disposition after a seal failure comes from the hook's own `sealFailed` flag; the relabel's returned row must not decide it (CODE-4 item 8).

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
- A no-spec-change proposal that closes every member as a direct fix. The writer-context rule, the shutdown wait, and the unknown-commit resolution are spec-silent.
- One terminal-signal set for every writer (F-7.2.35, P2b).
- Off-holder terminal writes (F-10.1.33, TT-29) and the cross-replica release of F-12.8.43, deferred to the inter-replica carrier proposal.
- The terminal-writer half of F-4.9.33, pending the owner's P2a or P2b placement decision.

## Open decisions for human to make

None at drafting time. The review loops write this section.

## Defects in the shipped tree that this proposal does not stage

- §5.2 says "pool admission refuses a pool" whose configuration does not meet the `claimOrphanTimeout` bound, but no Go pool-admission check enforces it; only the chart `fail` gate does. Building an admission check is a separate surface from the bound this proposal re-bases, so REC-1 files it as a finding.
- F-11.2.31: a budget termination lost to a crash before its write is not retried, because the enforcer state is process-local. It belongs to P4.
- F-7.2.35: writers do not share one terminal-signal set. It belongs to P2b.
- F-10.1.33: a terminal write made off the coordinating replica. It depends on the inter-replica carrier proposal.
- F-4.9.33, terminal-writer half: out of scope until the owner places it in P2a or P2b.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0092 | Implemented | Bounds the context of the D7 hook 0092 built and closes the seal writer-context question it handed to P2. | Nothing; an implemented proposal is not edited. |
| 0093 | Implemented | Leaves its F-10.1.33 accepted departure standing and moves the entry's owner to the inter-replica carrier proposal. | Nothing. |
| 0094 | Implemented | Closes the `session.force_terminated` half of F-11.7.22 that 0094 left to P2. | Nothing. |
| 0095 | Implemented | Narrows the bound of its F-12.8.43 departure to one checkpoint interval on the binding replica. | Nothing. |
| 0097 | Approved | Both edit help text in `cmd/lenny-gateway/flags.go`; the edits touch different flags. | Whichever lands second rebases the textual conflict. |
| P2b (plan step, no proposal yet) | Not drafted | Supplies the writer-context rule P2b depends on. | Cite §7.1 **Terminal pipeline context:**. |
| P3 TT-44 (plan step, no proposal yet) | Not drafted | Supplies the bounded hook context TT-44 needs. | Cite §7.1 **Terminal pipeline context:**. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md, spec/29_communication-scenarios.md, spec/28_communication-channels.md — §7.1 **Terminal pipeline context:** paragraph; terminal write before the seal in the §7.1 normal flow and §29.4; the §28.5.1 `CH-ATTACH` **Timing.** stream end.
- SPEC-2 — spec/07_session-lifecycle.md, spec/16_observability.md, spec/29_communication-scenarios.md — **Seal-and-export invariant:** seal outcomes and their §16.1, §16.5, and §29.4 restatements.
- SPEC-3 — spec/07_session-lifecycle.md, spec/12_storage-architecture.md — retention TTL start, derive rule 1 terminal-state bullet, §12.5 retention item.
- SPEC-4 — spec/10_gateway-internals.md, spec/29_communication-scenarios.md — **Terminal pipelines at process exit.** and the §29.7 trace.
- SPEC-5 — spec/12_storage-architecture.md — §12.3 durability-table row for a lost commit acknowledgement.
- SPEC-6 — spec/05_runtime-registry-and-pool-model.md, spec/04_system-components.md — §5.2 slot invariant names the terminal pipeline budget; §4.6.1 live-binding orphan predicate.
- SPEC-7 — spec/07_session-lifecycle.md, spec/06_warm-pod-model.md — terminate yields `completed` from the recovery states.
- CODE-1 — pkg/gateway/sessionserver, cmd/lenny-gateway, pkg/gateway/externalapi/admin — bounded hook context and detached gateway-decided writes.
- CODE-2 — pkg/gateway/sessionserver/sessionserver.go, cmd/lenny-gateway/runserver.go — shutdown wait on the terminal claim record.
- CODE-3 — pkg/gateway/session/sessionstore/pgstore/pgstore.go, migrations/ — write-identifier check after a commit error.
- CODE-4 — pkg/gateway/sessionserver, pkg/gateway/checkpoint/checkpointer, pkg/gateway/session/executor, pkg/gateway/podlifecycle/podsession — seal outcome fixes and the closing gate.
- CODE-5 — pkg/gateway/checkpoint/checkpointer, cmd/lenny-gateway/pod_lifecycle.go — release of a binding whose row is gone.
- CODE-6 — cmd/lenny-gateway, cmd/lenny-controller, charts/lenny — §5.2 gates on the terminal pipeline budget.
- CODE-7 — pkg/gateway/storage/retentiongc, pkg/gateway/checkpoint/checkpointer, pkg/gateway/session/sessionstore, pkg/gateway/sessionserver, pkg/api/v1/session, pkg/controller/warmpool, cmd/lenny-controller, migrations/ — retention GC guard, resume-cleanup scope, fenced derive-failure insert, terminate precondition, orphan-GC terminal-age lookup.
- TEST-1 — test files named in TEST-1 — tests across the reached tiers.
- DOC-1 — docs/operator-guide/troubleshooting.md — orphan-claim drain condition.
- REC-1 — BUILD-GAPS.md — resolutions, departures, and deferral notes.
