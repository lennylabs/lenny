# Non-spec changes — Terminal pipeline completeness: seal and writer context

## Design (implementation-facing)

CODE-1 detaches and bounds the hook context once, at the two entries every terminal writer reaches (`AfterTerminalWrite` and `recordSessionCompleted`), and detaches the terminal write itself only in `failFinalizing` and admin `ForceTerminate`. CODE-2 makes gateway shutdown wait on the existing `terminalClaimRecord` before `sessionSrv.Close()`. CODE-3 resolves a lost commit acknowledgement once, in `pgstore.Store.Update`, by reading back from the primary a write identifier the update stored. CODE-4 fixes the seal outcome, adds the closing gate that keeps message delivery off a sealing binding, and gives each checkpoint intent row the deadline its attempt runs under. CODE-5 makes the Checkpointer release a binding whose row is gone on the primary. CODE-6 moves the §5.2 startup gate and the chart gate to the terminal pipeline budget. CODE-7 holds the retention GC guard, generation-scoped resume cleanup, the fenced derive-failure insert, the terminate precondition, and the orphan-GC terminal-age lookup. TEST-1 pins each change. REC-1 records the resolutions in `BUILD-GAPS.md`.

The normative rules live in the staged spec text: the context, the budget, and the post-terminal window in SPEC-1; the seal outcomes in SPEC-2; the retention start in SPEC-3; the shutdown wait in SPEC-4; the commit-status rule in SPEC-5; the slot invariant, the orphan predicate, and the intent-row deadlines in SPEC-6; the terminate edges in SPEC-7. Each CODE deliverable cites its rule by spec heading in a `// spec:` comment.

## Staged code changes

### CODE-1 · One gateway-owned, bounded context at the hook entry, and detached gateway-decided writes

Targets: `pkg/gateway/sessionserver/terminal_hook.go`, `pkg/gateway/sessionserver/usage.go`, `pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/session_terminal.go`, `cmd/lenny-gateway/session_admin.go`, `pkg/gateway/externalapi/admin/sessions.go`, `cmd/lenny-gateway/session_budget.go`.

1. In `sessionserver.go`, add

   ```go
   // terminalPipelineBudget is the §7.1 terminal pipeline budget: the seal
   // window plus the release allowance (--stream-failure-report-timeout).
   // spec: §7.1 (Terminal pipeline context)
   func (s *Server) terminalPipelineBudget() time.Duration {
   	return s.sealMaxDuration + s.streamFailureReportTimeout
   }

   // TerminalPipelineBudget exports terminalPipelineBudget for the shutdown
   // wait.
   func (s *Server) TerminalPipelineBudget() time.Duration { return s.terminalPipelineBudget() }

   // terminalContext detaches ctx from its caller's cancellation and bounds
   // it by what remains of the terminal pipeline budget counted from
   // terminatedAt, the instant the §5.2 stale slot release ages the session
   // from. A zero terminatedAt, which that release never ages, gets the
   // whole budget. spec: §7.1 (Terminal pipeline context)
   func (s *Server) terminalContext(ctx context.Context, terminatedAt time.Time) (context.Context, context.CancelFunc) {
   	remaining := s.terminalPipelineBudget()
   	if !terminatedAt.IsZero() {
   		remaining = min(remaining, remaining-s.clock().Sub(terminatedAt))
   	}
   	return context.WithTimeout(context.WithoutCancel(ctx), remaining)
   }
   ```

   `context.WithoutCancel` keeps the caller's values (trace span, tenant), so the seal span still stitches under the writer's trace. Measure the elapsed time on `s.clock` rather than with `time.Now` or an absolute `WithDeadline`, so a hook test's fake server clock does not hand the pipeline an already-expired context.

2. In `terminal_hook.go`, rename the current body of `AfterTerminalWrite` to an unexported `afterTerminalWrite` with the same signature. The exported `AfterTerminalWrite` derives `terminalContext(ctx, sess.TerminatedAt)`, defers its cancel, and calls `afterTerminalWrite`.
3. In `usage.go`, `recordSessionCompleted` derives `terminalContext(ctx, sess.TerminatedAt)` once, defers its cancel, and calls `afterTerminalWrite` and then `afterTerminalRelease` on that context. It calls the unexported body, so the budget is applied once and `afterTerminalRelease` (the session-log close) shares it.
4. In `session_terminal.go`, `failFinalizing` sets `ctx = context.WithoutCancel(ctx)` as its first statement. This covers its three `finalize.go` callers without editing them.
5. In `cmd/lenny-gateway/session_admin.go`, `sessionAdminAdapter.ForceTerminate` sets `ctx = context.WithoutCancel(ctx)` before its store Update. In `pkg/gateway/externalapi/admin/sessions.go`, `handleForceTerminateSession` emits `eventSessionForceTerminated` on `context.WithoutCancel(req.Context())`. The handler still calls `ForceTerminate` synchronously and responds after it returns.
6. In `cmd/lenny-gateway/session_budget.go`, `TerminateSession` opens the claim before it starts the goroutine:

   ```go
   end := func() {}
   if t.beginTerminal != nil {
   	end = t.beginTerminal(sessionID)
   }
   go func() {
   	defer end()
   	ctx, cancel := context.WithTimeout(context.Background(), budgetTerminateTimeout)
   	defer cancel()
   	t.terminate(ctx, sessionID, reason)
   }()
   ```

   `terminate` no longer opens the claim. The `budgetTerminateTimeout` doc comment states that the deadline bounds the lookup and the terminal write, and that `AfterTerminalWrite` re-bounds the hook.

The existing `context.WithoutCancel` calls in `session_transitions.go`, `mcptools_register.go`, and the cascade in `usage.go` are redundant after this change and may stay. The REST, MCP, and watchdog terminal Updates keep their caller context.

### CODE-2 · Shutdown waits on the existing terminal claim record

Targets: `pkg/gateway/sessionserver/sessionserver.go`, `cmd/lenny-gateway/runserver.go`. `replica_records.go` is unchanged.

1. Add next to `Close`:

   ```go
   // WaitTerminalWork blocks until s.terminalClaims is empty, or ctx ends.
   // spec: §10.1.7 (Terminal pipelines at process exit.)
   func (s *Server) WaitTerminalWork(ctx context.Context) error
   ```

   It polls `s.terminalClaims.size()` every 100 ms, returns nil when the size is zero, and returns `ctx.Err()` when ctx ends.
2. Factor the shutdown sequence after `<-stopCh` in `runserver.go` into a helper that takes its collaborators as interfaces, so a test drives it with fakes. The helper runs, in this order:
   - (a) `httpSrv.Shutdown` on a `--shutdown-timeout` context.
   - (b) `sessionSrv.WaitTerminalWork` on `context.WithTimeout(context.Background(), sessionSrv.TerminalPipelineBudget())`. When the bound ends the wait, log the number of entries still open.
   - (c) `sessionSrv.Close()`.
   - (d) On a fresh `context.WithTimeout(context.Background(), *shutdownTimeout)`: `traceShutdown`, `llmProxySrv.Shutdown`, the GatewayControl `GracefulStop`, the pool and DDL-pool closes, the SQLite final snapshot, and the Redis closes, in their current relative order.

The wait must precede `Close()`, because `releaseAfterTerminal` takes the slot-account lock on `s.lifetime()` and skips the release once that context is cancelled. `traceShutdown` moves after `Close()` so that spans the terminal pipelines emit during the wait are exported. Background loops (watchdog, budget terminator, orphan reconcilers) can open claims during the wait; the budget bound covers them.

### CODE-3 · pgstore.Update classifies a write whose commit acknowledgement is lost

Target: `pkg/gateway/session/sessionstore/pgstore/pgstore.go` (`Store.Update`).

1. Generate `writeID := uuid.NewString()` per call and add `last_write_id = $57::uuid` to `updateSQL`, bound to `writeID`. Set `wroteOK = true` after the UPDATE `tx.Exec` succeeds. A `mutate` error or an Exec error returns unchanged with no check, because a failed Exec rolls the transaction back.
2. When `InTx` returns an error and `wroteOK` is set, the remaining error is the commit. Under `pgtenant.InTx` on `s.pool` with `context.WithTimeout(context.WithoutCancel(ctx), sessionstore.DefaultPrimaryReadTimeout)`, run `SELECT last_write_id = $3::uuid FROM sessions WHERE id = $1::uuid AND tenant_id = $2`.
   - Result true: return `(out, nil)`.
   - Result false or NULL, no row, a query error, or the timeout: return the original `normalizeMiss(err)`.
3. Add `migrations/NNNN_sessions_last_write_id.up.sql` (`ALTER TABLE sessions ADD COLUMN last_write_id uuid`) and its `.down.sql`, numbered with the next free migration number. The column stays out of `selectList` and `sessionstore.Session`.
4. Cite `// spec: §12.3 (Write durability categories during failover)`.

Do not use `updated_at` as the discriminator. `pgtenant.MonotonicNext` returns `prev + 1µs` whenever the local clock is not after `prev`, so a competing writer on a lagging clock stamps the same value, and a matching `updated_at` does not prove this write applied. Do not check `pg_xact_status` on the transaction ID (D15). memstore cannot produce an ambiguous commit and is unchanged.

### CODE-4 · Seal outcome fixes, the closing gate, and the intent-row deadline

Targets: `pkg/gateway/checkpoint/checkpointer/checkpointer.go`, `pkg/gateway/checkpoint/checkpointer/uploaddriver.go`, `pkg/gateway/sessionserver/seal.go`, `pkg/gateway/sessionserver/terminal_hook.go`, `pkg/gateway/podlifecycle/podsession/bindrequest.go`, `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/executor.go`.

1. **Closing mark.** `BindResult` gains an atomic `closing` flag with `MarkClosing()` and `Closing() bool`. Once set it is never cleared; a new binding is a new `BindResult`.
2. **streamFor refuses a closing binding.** Under `e.mu`, on both the cache hit and the cache miss, `streamFor` returns `fmt.Errorf("podexec: session %s: %w", sessionID, ErrSessionSealed)` when the registry's `BindResult` for the session reports `Closing()`. On a cache hit the check reads the registry entry for the session, so an evicted-then-reopened path cannot bypass it.
3. **Send converts the refusal.** `PodExecutor.Send` returns `&SealedError{Delivered: 0}` when `streamFor` returns `ErrSessionSealed`, before any message is written. Every `Send` caller then takes its existing sealed-reroute branch: REST `deliverNow` through `errors.As` on `*executor.SealedError`, MCP through `errors.Is`, and held delivery through its `%w` wrap. A bare sentinel would reach REST `deliverNow`'s fallthrough and surface as `EXECUTOR_FAILURE`.
4. **Doc comments.** Widen the `ErrSessionSealed` and `SealedError` doc comments to cover the terminal-seal closing mark (`// spec: §7.1 (Terminal pipeline context)`) as well as the §4.7.1 idle-suspension refusal.
5. **Seal start.** In `sealWorkspace`, before the retry loop, read `s.podRegistry.Get(sess.ID)`. With no binding (or a nil registry), skip the `Seal` call and return nil, which is the outcome today's no-op seal records. With a binding, call `MarkClosing()` on it, then `EvictStream(sess.ID)` through the existing `streamEvicter` seam when `s.executor` satisfies it, then enter the loop. In `driveCheckpoint`, after `lockFlight` returns, an attempt whose source is not `sealed` returns `ErrNoBinding` without opening a stream when the binding reports `Closing()`; `snapshot` passes its source in, because `Seal` stamps `TriggerPeriodic` and the trigger cannot tell a seal apart. The periodic sweep, the barrier, and the preStop loop all reach it through `CheckpointWithTrigger`.
6. **Binding lost during the loop.** After a `Seal` error, when `s.podRegistry.Get(sess.ID)` reports no binding, record `sealOutcomePermanent` and return the error. `Checkpointer.Seal` no longer maps `ErrNoBinding` to nil; delete that branch, and keep no second mapping path.
7. **Absent row.** `Checkpointer.snapshot`'s row read (see CODE-5 item 2) returns `sessionstore.ErrNotFound` for a row absent on the primary. `isPermanentSealError` adds `errors.Is(err, sessionstore.ErrNotFound)`. Adapter gRPC `NotFound` stays transient.
8. **Release disposition after a failed seal.** In `afterTerminalWrite`, record `sealFailed := true` when `sealWorkspace` returns an error. `releaseAfterTerminal` takes the disposition as a parameter, and the hook passes `executor.DispositionFailed` when `sealFailed` is set and `dispositionForState(sess.State)` otherwise. The relabel's returned row no longer decides the disposition, so a relabel store error cannot produce the clean disposition.
9. **Intent-row deadline.** In `driveCheckpoint`, when `c.Deadline` is zero and the parent context carries no deadline, derive the attempt's context with `context.WithTimeout` of the §4.4 checkpoint timeout (60 s) in place of `context.WithCancel`, so every attempt's context carries a deadline. In `uploaddriver.go`, where the intent row is built, set `CheckpointTimeoutAt` to that deadline. For a seal the context carries the `sealWorkspace` window (`context.WithTimeout(ctx, s.sealMaxDuration)` in `seal.go`) inside the CODE-1 budget context, and for a periodic attempt on the `Run` context it carries the 60 s timeout. Production leaves `Checkpointer.Deadline` zero (`cmd/lenny-gateway/pod_lifecycle.go`), so without this item every row's `CheckpointTimeoutAt` equals its `CheckpointStartedAt` (D21).

Cite `// spec: §7.1 (Seal-and-export invariant)` on items 5 to 8, `// spec: §10.1.7 (preStop Hook Drain), §7.1 (Terminal pipeline context), §4.4.3 (Checkpoint timeout and recovery)` on item 9 and on the item 5 checkpoint skip, and `// spec: §7.1 (Terminal pipeline context)` on items 1 to 3. The seal export uses the separate `Adapter.Checkpoint` RPC, so evicting the Attach stream does not disturb it.

### CODE-5 · Checkpointer releases a binding whose row is gone on the primary

Targets: `pkg/gateway/checkpoint/checkpointer/checkpointer.go`, `pkg/gateway/sessionserver/terminal_binding.go` (reused unchanged), `cmd/lenny-gateway/pod_lifecycle.go`.

1. `Checkpointer` gains a field `ReleaseGone func(tenantID, sessionID string)`. In `cmd/lenny-gateway/pod_lifecycle.go`, wire it through a late-bound seam in the style of `terminalReleaserSeam` (`cmd/lenny-gateway/redis_quota.go`) to `sessionSrv.ReleaseTerminalBinding(sessionstore.Session{ID: sessionID, TenantID: tenantID, State: session.StateFailed})`. `StateFailed` gives a row deleted outside the hook the same failed disposition CODE-4 item 8 gives a row deleted during the seal.
2. Fold the row read into `sessionCheckpointContext`, so a checkpoint makes one row read. The read uses `sessionstore.ReadPrimary(ctx, c.Sessions, timeout, tenantID, sessionID)`, never `c.Sessions.Get`, because pgstore `Get` reads the replica and replica lag would make a just-bound session read as absent. On `ErrNotFound` it returns that error and `snapshot` drives no export. Any other read error keeps today's degrade-and-continue behavior and releases nothing.
3. Call `ReleaseGone` in one place, `CheckpointWithTrigger`, when the read returned `ErrNotFound`. That covers the periodic `Sweep` and the barrier's eviction call. `Seal` does not call it, because the hook's own release runs.

`ReleaseTerminalBinding` already dedups through `startRelease`, defers to an open terminal claim, takes the slot-account lock, and returns credential leases.

### CODE-6 · §5.2 startup gate and chart gate use the terminal pipeline budget

Targets: `cmd/lenny-gateway/startupgates.go`, `cmd/lenny-gateway/flags.go`, `cmd/lenny-controller/flags.go`, `charts/lenny/templates/gateway-deployment.yaml`, `cmd/lenny-gateway/slot_reaper_test.go`.

1. `slotReaperSettings` gains `streamFailureReportTimeout time.Duration`, wired from `*f.streamFailureReportTimeout` where the settings are built. In `validateSlotReaperFlags`, the seal term becomes the seal window in effect plus the stream-failure report timeout in effect; a non-positive timeout falls back to `sessionserver.DefaultStreamFailureReportTimeout`. The doc comment and the error message name `--stream-failure-report-timeout` and cite `// spec: §5.2 (Slot assignment atomicity)`.
2. Update the help text of `--slot-reaper-terminal-age` and `--max-resuming-seconds` in `cmd/lenny-gateway/flags.go` and of `--claim-orphan-timeout` in `cmd/lenny-controller/flags.go` to name the terminal pipeline budget in place of the seal window.
3. In the chart's §5.2 `fail` gate, the seal term becomes `$sealWindow` plus 30 (the default release allowance), because the chart does not render `--stream-failure-report-timeout`. Update the comment and the `fail` message to name the allowance. The gateway startup gate stays the complete check.
4. `defaultReaperSettings` and the gate table in `slot_reaper_test.go` gain the allowance (TEST-1 item 7).

With the defaults the floor stays at 601 s, so no default install changes. No Go pool-admission comparison exists to edit (REC-1 records that gap).

### CODE-7 · Retention GC guard, resume-cleanup scope, fenced derive-failure insert, terminate precondition, orphan-GC terminal-age lookup

Targets: `pkg/gateway/storage/retentiongc/retentiongc.go` and `cmd/lenny-gateway/leader_sweeps.go`; `pkg/gateway/checkpoint/checkpointer/partialcleanup.go`, `pkg/gateway/sessionserver/checkpoint_seams.go`, `pkg/gateway/sessionserver/resume_driver.go`; `pkg/gateway/session/sessionstore/sessionstore.go`, `pgstore/pgstore.go`, `memstore/memstore.go`; `pkg/gateway/sessionserver/derive.go`; `pkg/api/v1/session/session.go`; `pkg/gateway/sessionserver/session_transitions.go`; `pkg/gateway/sessionserver/lifecycle.go`; `pkg/controller/warmpool/gc.go`, `cmd/lenny-controller/main.go`, `migrations/NNNN_sessions_terminal_by_pod_index.up.sql` and `.down.sql` (new).

1. **Retention GC guard.** `retentiongc.Options` gains `RetentionFor func(context.Context, sessionstore.Session) time.Duration`. `eligible` becomes a `Collector` method taking `ctx` and, after the existing checks, additionally requires `!s.TerminatedAt.IsZero() && !s.TerminatedAt.Add(c.retentionFor(ctx, s)).After(now)`, resolving the window only for a row already past `RetentionExpiresAt`. A terminal row with a zero `TerminatedAt` stays ineligible, matching `ListStaleTerminalSessions`, and a nil `RetentionFor` makes every row ineligible. Both `eligible` call sites (the listing pass and the pre-delete re-read) take the predicate. In `lifecycle.go`, add the exported `(*Server).RetentionWindow(ctx context.Context, sess sessionstore.Session) time.Duration`, which returns the larger of `s.retentionForTier(ctx, sess.TenantID, sess.Environment)` and `s.terminalPipelineBudget()`, citing `// spec: §7.1 (Terminal pipeline context)` on the comparison; `leader_sweeps.go` passes `w.sessionSrv.RetentionWindow`. Cite `// spec: §7.1 (Artifact retention)`. Change the `rollRetentionOnTerminal` comment to cite §7.1 **Artifact retention:**.
2. **Resume cleanup by generation.** Widen the seam to `CleanupAfterResume(ctx, tenantID, sessionID string, resumedGen int64)` in `checkpoint_seams.go` and its fakes. `resume_driver.go` passes `r.CoordinationGeneration` from the committed resume row. In `PartialCleaner.CleanupAfterResume`, return nil when `record.CoordinationGeneration >= resumedGen`, because the live incarnation wrote that row (a periodic checkpoint or a terminal seal intent). Otherwise clean the row whatever its `manifest_reason`, `in_progress` included, because it is a lower-generation abandoned residue. Cite `// spec: §10.1.7 (Cleanup.)`.
3. **Fenced derive-failure insert.** Add `CreateIfGeneration(ctx context.Context, sess Session, sourceTenantID, sourceID string, wantGen int64) error` to the `sessionstore.Store` interface. pgstore reads the source row's `coordination_generation` `FOR SHARE` inside the INSERT transaction, following the `fenceInsert` pattern in `Store.Create`, and returns `sessionstore.ErrGenerationAdvanced` (new sentinel) without inserting when the generation differs from `wantGen`. memstore performs the same check under its lock. `persistDeriveFailure` replaces its separate `readPrimary` fence and `Create` with one `CreateIfGeneration` call on `context.WithoutCancel(ctx)`, and treats `ErrGenerationAdvanced` as "no row written". Cite `// spec: §7.1 (Derive session semantics)`.
4. **Terminate precondition.** Add `StateResuming` to `EndpointTerminate`'s base states in `pkg/api/v1/session/session.go`. `transitionTerminate` already writes `completed`. Update the `handleTransition` doc comment in `session_transitions.go` to state that terminate from `resume_pending` and `resuming` yields `completed`, citing `// spec: §7.2 (Interactive Session Model)`.
5. **Orphan-GC terminal-age lookup.** Add `PodHasLiveSession(ctx context.Context, podID string, terminatedAfter time.Time) (bool, error)` to `pgstore.Store` and `memstore.Store`. It reports whether a row whose `pod_assignment` names `podID` is non-terminal, or is terminal with a `terminated_at` later than `terminatedAfter`. A terminal row with no `terminated_at` does not count. pgstore runs it cross-tenant (`InAllTenants`), served by a new partial index on `sessions (pod_assignment, terminated_at) WHERE pod_assignment <> '' AND terminated_at IS NOT NULL`. Leave `GetActiveSlotsByPod` unchanged, because §5.2 rehydration seeds from it. `warmpool.SessionLookup.PodHasActiveSession` gains `terminatedAfter time.Time`. `ClaimGarbageCollector.evaluate` passes `now - orphanTimeout` for a `bound` or `recycling` claim and the zero time otherwise, so only predicate 1 changes. `sessionActiveLookup` in `cmd/lenny-controller/main.go` holds a one-method interface that `*pgstore.Store` satisfies and calls `PodHasLiveSession`. Update the `SessionLookup` doc comment and the lookup fakes in `gc_test.go`, `gc_internal_test.go`, `gc_reclaim_internal_test.go`, and `tests/tier8_chaos/session_suspend_resume_chaos_test.go`. Cite `// spec: §4.6.1 (Orphaned SandboxClaim detection)`.

## Staged schema, chart, and migration changes

The CODE-3 migration adding `sessions.last_write_id`, the CODE-7 item 5 index migration, and the chart gate edit inside CODE-6.

## Staged docs changes

### DOC-1 · Orphan-claim drain condition on the troubleshooting page

Target: `docs/operator-guide/troubleshooting.md`. In the `SandboxClaimOrphanRateHigh` row, replace "and no active session uses the pod" with "and no session that is active, or that ended less than the claim orphan timeout ago, uses the pod". The row stays one physical table line.

### REC-1 · BUILD-GAPS resolutions and recorded departures

Target: `BUILD-GAPS.md` only. The proposal's `.deviations.md` is written by `implement-proposal` alone.

1. Add a **Resolution:** paragraph to each closed member naming its code site and its `// spec:`-annotated tests (TEST-1 item numbers resolved to test function names), and tick its heading to CLOSED in the form other closed entries use. Members: F-7.1.21 to F-7.1.29, F-7.2.29, F-7.2.30, F-7.3.32, F-10.1.27, F-11.2.30, F-11.7.22 (the `session.force_terminated` half), F-15.1.42, F-15.1.44. Specific wording:
   - F-7.1.27 is closed by the SPEC-2 slot clause with no code change.
   - F-7.1.29 is closed as reframed to a hard delete during the hook.
   - F-15.1.42 is closed by SPEC-3 (b).
   - F-7.2.30 is closed by admitting `StateResuming` and aligning §7.2 to §15.1 (`completed`, SPEC-7). The paragraph states that this differs from the entry's suggested resolution.
   - F-11.7.22's remaining half is closed, citing 0094's annotation for the `tenant.suspended` half.
   - F-7.2.29 is closed by the commit-status check (CODE-3).
2. F-11.2.30 is closed for the 30 s cut and the graceful-restart window. Add a pointer note on the still-OPEN F-11.2.31 recording that it owns the crash window.
3. Add an **Accepted departure (proposal 0100):** paragraph on F-10.1.27 for terminal work cut by SIGKILL or a crash, and on F-7.2.29 for a committed write that the SPEC-5 check classifies as not applied. Each cites its stating spec text (§10.1.7 **Terminal pipelines at process exit.**; the §12.3 durability-table row) and names its pinning test.
4. On F-12.8.43, add an annotation recording that CODE-5 releases the remote binding user erasure leaves behind on the binding replica's next checkpoint tick, which narrows the bound from `claimOrphanTimeout` to one checkpoint interval, and that `ReleaseTerminalBinding` returns the binding's credential leases, which answers the entry's "Question for P2". The entry stays OPEN for the cross-replica release and its owner moves from P2 to the inter-replica carrier proposal.
5. On F-10.1.33, add a deferral note that leaves it OPEN and moves its owner from P2 to the inter-replica carrier proposal.
6. Record that the §5.2 sentence "pool admission refuses a pool, whose configuration does not meet this bound" has no Go enforcement and that only the chart gate exists, as a new OPEN finding (see the summary's **Defects in the shipped tree that this proposal does not stage**).

## Testing

### TEST-1 · Tests across the reached tiers

Every test carries a `// spec:` annotation naming the sections in its item; tests at tier 2 and above carry `// diagnosis:`. Each test that encodes behavior introduced by CODE-1 to CODE-7 fails on the pre-fix tree. Items 10, and 12 for states other than `resuming`, pin behavior the code already has and that SPEC-3 and SPEC-7 now state.

1. **Hook context, every writer** (`pkg/gateway/sessionserver/terminal_context_internal_test.go`, new; extend `TestFunnelAndHookWritersEmitTheTerminalEventsOnce_spec_7_2` in `terminal_writers_internal_test.go`). Drive the table from `terminalWriterInventory` in `terminal_writer_inventory_test.go`, or assert that the table's writer set equals that inventory, so a writer added later fails until covered. For each writer: a fake sealer observes a context whose deadline is the row's `TerminatedAt` plus the budget on the server clock (a row stamped 10 s before the hook entry is the assertion that discriminates against a deadline counted from the entry; a zero `TerminatedAt` gets the whole budget) and which stays live after the writer's context is cancelled mid-seal; the release and the session-log close run on the same bounded context; a seal that never returns is cut at the budget, the release still runs, and the outcome is `timeout`. `failFinalizing` from each of its three branches commits `failed` when the request context is cancelled before the write. `ForceTerminate` commits and the `session.force_terminated` audit row is appended when the admin request context is cancelled during the hook (`pkg/gateway/externalapi/admin/sessions_test.go`). `// spec: 7.1 (Normal Flow), 11.7 (Audit Logging)`. Tiers 1 and 7a.
2. **Seal outcomes** (`pkg/gateway/sessionserver/seal_internal_test.go`). Table: binding removed after the seal begins yields `permanent` on first observation and a failed relabel; after a committed relabel the hook emits one `status_change` and one `session_complete`, each reporting `failed`; never-bound session skips `Seal`; nil registry behaves as never-bound; `sessionstore.ErrNotFound` from the sealer yields `permanent`; adapter gRPC `NotFound` retries; a relabel whose store Update errors still releases with `DispositionFailed` (the assertion that discriminates F-7.1.26). Fault-inject the binding removal at random points of the retry loop. `// spec: 7.1 (Normal Flow)`. Tier 1.
3. **Closing gate** (`pkg/gateway/session/executor/pod_test.go`; REST case in `terminal_writers_internal_test.go`). `streamFor` refuses a closing binding on the cache hit and the cache miss; `Send` returns `*SealedError` with `Delivered == 0` and writes nothing; a `Send` that obtained its conn before `EvictStream` ends with an error and starts no turn after the mark; REST `POST /messages` against a session whose binding is marked closing, with no held entries, takes the terminal-state reroute and never returns `EXECUTOR_FAILURE`. A randomized-interleaving checker under `-race` runs terminal write, seal start, eviction, concurrent Sends, and release in random orders, and asserts that no turn starts after `MarkClosing`, the release runs once, and no goroutine leaks. `// spec: 7.1 (Normal Flow), 28.5.1 (Gateway-to-pod)`. Tiers 1 and 7a.
4. **Deleted row** (`pkg/gateway/checkpoint/checkpointer/checkpointer_test.go`). (a) A deleted row releases exactly once under concurrent periodic and eviction ticks and makes no `driveCheckpoint` call. (b) A row present on the primary but missing from a lagging replica is neither released nor skipped. (c) A read error other than `ErrNotFound` releases nothing. (d) The release defers while a terminal claim is open and fires on a later tick. (e) The synthesized row carries `StateFailed`. `// spec: 7.1 (Normal Flow)`. Tiers 1 and 7a.
5. **Commit acknowledgement lost** (`pkg/gateway/session/sessionstore/pgstore/pgstore_test.go`, against a real Postgres container). Inject a commit error after the UPDATE executes (a pgx wrapper that fails `Commit` after forwarding it, or a connection kill after `COMMIT` is sent). Cases: committed transaction returns the written row and nil; aborted transaction returns the original error; the read-back fails or exceeds `DefaultPrimaryReadTimeout` and returns the original error; an Exec error makes no read-back; a row whose `last_write_id` a competing writer set after this write rolled back, stamping the same `updated_at` (both clocks behind `prev`), is classified not applied (the assertion that discriminates against an `updated_at` comparison). `// spec: 12.3 (Postgres HA Requirements)`. Tiers 2 and 8.
6. **Shutdown wait** (`terminal_writers_internal_test.go` for the claim record; a new `cmd/lenny-gateway` shutdown-helper test for order). `WaitTerminalWork` returns nil at zero, returns `ctx.Err()` at the bound with claims open, and keeps waiting when a claim opens mid-wait until it closes or the bound ends. The helper calls `httpSrv.Shutdown`, then the wait, then `Close()`, then the fresh-context closes, and calls `traceShutdown` after the wait and after `Close()`; a D6 release in progress during the wait completes its slot-lock acquisition. `// spec: 10.1.7 (preStop Hook Drain)`. Tiers 1 and 7a.
7. **Slot reaper gate** (`cmd/lenny-gateway/slot_reaper_test.go`). Table cases include one where the seal window plus the allowance sets the floor, a non-positive timeout that falls back to the default, and the defaults (floor 601 s). Add a chart render case to the existing tier-0 chart render tests that sets `gateway.workspaceSealMaxDurationSeconds` to 580 and asserts the `fail` fires below 611. `// spec: 5.2 (Slot assignment atomicity)`. Tiers 0 and 1.
8. **Budget terminator** (`cmd/lenny-gateway/session_budget_test.go`). The claim is open when `TerminateSession` returns, before the goroutine runs; a hook whose fake seal outlasts `budgetTerminateTimeout` is not cut by it; the claim closes after the hook. `// spec: 11.2 (Budgets and Quotas), 7.1 (Normal Flow)`. Tier 1.
9. **Retention guard** (`pkg/gateway/storage/retentiongc/retentiongc_test.go` and `sweep_tenant_test.go`). Update existing tests in both files to seed `TerminatedAt` and a `RetentionFor`. Cases: zero `TerminatedAt` ineligible; nil `RetentionFor` ineligible; a row past `RetentionExpiresAt` but inside the window from `TerminatedAt` ineligible (the create-time deadline of a session whose roll was lost; the assertion that discriminates F-7.1.28); a row past both eligible; the pre-delete re-read applies the same predicate; legal hold still wins. `(*Server).RetentionWindow` returns the terminal pipeline budget when the tier window is shorter, and the tier window otherwise (`pkg/gateway/sessionserver/lifecycle_test.go`). `// spec: 7.1 (Normal Flow), 12.5 (Artifact Store)`. Tier 1.
10. **Derive in the seal window** (`pkg/gateway/sessionserver/derive_test.go`). A `completed` source whose snapshot is a checkpoint derives from it and reports `workspaceSnapshotSource: "checkpoint"`; after the seal it reports the sealed source. A `failed` source reports the source its row records: `sealed` after a seal that succeeded, and `checkpoint` after a seal-timeout relabel. `// spec: 7.1 (Normal Flow)`. Tier 1.
11. **Resume cleanup scope** (`pkg/gateway/checkpoint/checkpointer/partialcleanup_test.go`). A crash-residue `in_progress` row at a lower generation is cleaned after resume; an `in_progress` row at the resumed generation is left; a row at `resumedGen + 1` from a terminate-from-resuming seal is left. `// spec: 10.1.7 (preStop Hook Drain), 12.5 (Artifact Store)`. Tier 1.
12. **Terminate precondition** (`pkg/api/v1/session/session_test.go`; extend `TestTerminateAndDeleteRevalidateOnTheLockedRow_spec_15_1`). Terminate from each precondition state, `resuming` included, commits `completed`; `resuming` no longer returns 409; the committed row's `coordination_generation` advances from `resuming`. `// spec: 7.2 (Interactive Session Model), 15.1 (REST API)`. Tier 1.
13. **Fenced derive-failure insert** (`derive_test.go` for memstore; `pgstore_test.go` for pgstore). A generation bump between admission and insert writes no row; a concurrent bump racing the insert either blocks on `FOR SHARE` and then sees the new generation or commits first, and never leaves a `failed` row at a stale generation. `// spec: 7.1 (Normal Flow)`. Tiers 1, 2, and 7a.
14. **Orphan GC during the terminal pipeline** (`pkg/controller/warmpool/gc_test.go`, `cmd/lenny-controller/session_lookup_test.go`, `pgstore_test.go`). The table runs over binding state and the pod's rows. A `bound` claim whose transition is older than the timeout, and whose pod's only session terminated 10 s ago, is not drained (the assertion that discriminates). The same claim is drained when that session terminated longer ago than the timeout, or carries no `TerminatedAt`. A `reserved` claim past its hold and an unset-state claim are reclaimed whatever terminal rows name the pod. pgstore runs the query cross-tenant against a real Postgres container. `// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))`. Tiers 1 and 2.
15. **Intent rows and the partial-manifest backstop** (`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go` and `checkpointer_test.go`; a new `pgstore_test.go` in `pkg/gateway/checkpoint/partialmanifeststore/pgstore/` against a real Postgres container). A seal attempt writes its context's deadline as `CheckpointTimeoutAt`. A periodic attempt with a zero `Deadline` on a context with no deadline writes `now` plus 60 s and is cut at it. An eviction attempt waiting on the single-flight lock behind a seal opens no stream once the seal returns, and the row keeps the sealed snapshot; this case discriminates against the tree, where the attempt records a `checkpoint` snapshot over the sealed one. With the session row terminal, `ListReclaimable` omits each `in_progress` row, seal and periodic, before its deadline and returns it after. The omission discriminates against the pre-fix tree, whose rows carry `CheckpointStartedAt`. `// spec: 10.1.7 (preStop Hook Drain), 12.5 (Artifact Store), 4.4.3 (Checkpoint Quiescence, Timeout, and Abort Handling), 7.1 (Normal Flow)`. Tiers 1 and 2.

## Edge cases and accepted failure modes

- A claim opened by a background loop during the shutdown wait extends the wait up to the budget bound (CODE-2).
- A committed write that the CODE-3 read-back classifies as not applied takes the outcome SPEC-5 states.
- A row absent on the replica but present on the primary releases nothing (CODE-5 item 2).
- A periodic checkpoint that starts after the resume carries the resumed generation and is left by cleanup (CODE-7 item 2).
- An old residue row behind a live row is left to the §12.5 backstop (CODE-7 item 2).
- A collapse-path `completed` session that never seals is collectable under the CODE-7 item 1 predicate.

## Files touched on application (non-spec)

- `pkg/gateway/sessionserver/terminal_hook.go`, `usage.go`, `sessionserver.go`, `session_terminal.go`, `seal.go`, `derive.go`, `session_transitions.go`, `checkpoint_seams.go`, `resume_driver.go`, `lifecycle.go`
- `pkg/gateway/checkpoint/checkpointer/checkpointer.go`, `partialcleanup.go`, `uploaddriver.go`
- `pkg/gateway/podlifecycle/podsession/bindrequest.go`
- `pkg/gateway/session/executor/pod.go`, `executor.go`
- `pkg/gateway/session/sessionstore/sessionstore.go`, `pgstore/pgstore.go`, `memstore/memstore.go`
- `pkg/gateway/storage/retentiongc/retentiongc.go`
- `pkg/gateway/externalapi/admin/sessions.go`
- `pkg/api/v1/session/session.go`
- `cmd/lenny-gateway/session_admin.go`, `session_budget.go`, `runserver.go`, `pod_lifecycle.go`, `startupgates.go`, `flags.go`, `leader_sweeps.go`
- `cmd/lenny-controller/flags.go`, `main.go`
- `pkg/controller/warmpool/gc.go`, `gc_internal_test.go`, `gc_reclaim_internal_test.go`
- `tests/tier8_chaos/session_suspend_resume_chaos_test.go`
- `charts/lenny/templates/gateway-deployment.yaml`
- Test files named in TEST-1
- `migrations/NNNN_sessions_last_write_id.up.sql`, `.down.sql` (new)
- `migrations/NNNN_sessions_terminal_by_pod_index.up.sql`, `.down.sql` (new)
- `docs/operator-guide/troubleshooting.md`
- `BUILD-GAPS.md`
