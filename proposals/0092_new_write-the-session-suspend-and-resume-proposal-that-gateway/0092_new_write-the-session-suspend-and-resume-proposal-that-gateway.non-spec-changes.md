# Non-spec changes: Session suspend, idle suspension, and resume driver

## Design (implementation-facing)

This proposal lands after proposal 0091. CODE-4 deletes the `ErrHeld` hold in `holdOrFailOnResumeError` that 0091 CODE-2 adds, CODE-5 edits the `Sweep` loop that 0091 CODE-5 rewrites, and CODE-6 reads the turn token that 0091 CODE-1 adds. Each of those edits is located below by function and quoted text, and each assumes the 0091 version of the code.

The components and the work each one does:

- **Watchdog** (`pkg/gateway/runtime/watchdog`). It runs on every replica every tick and already lists every state. It gains three optional hooks beside `RetryAttemptNotifier`, each implemented by the session server: `ResumeDriver` (CODE-4), `SuspendedPodReleaser` (CODE-6), and `IdleSuspender` (CODE-6). It gains one new sweep, `sweepSuspended` (CODE-6), and it measures session age as active time (CODE-1).
- **Session server** (`pkg/gateway/sessionserver`). It implements the hooks. The resume driver, the suspension release, and the idle suspension each act only on a session whose `REG-COORDLEASE` names this replica, or on every session when no lease store is wired (dev mode). A lease read that fails is treated as "not the holder", so the hook does nothing.
- **Binder** (`pkg/gateway/podlifecycle/podsession`). It gains a post-claim callback (CODE-3), which is the only seam through which the driver writes `resuming`.
- **Coordination Sweeper** (`pkg/gateway/coordination/coordination`). Its adoption predicate widens (CODE-5).

**Per-session delivery lock.** CODE-4 adds an in-process lock keyed by session ID on `Server`, separate from the 0091 slot-accounting lock. The message path takes it around its classify-and-send for a target this replica coordinates. `completeResume` takes it before the `resuming → running` commit and releases it after `deliverBuffered` returns. The lock guarantees that no message classified against the newly `running` target is sent before the buffered messages. Never take the 0091 slot-accounting lock while holding it.

## Staged code changes

### CODE-1 · Active-time session age in the store funnel

Targets: `migrations/0182_sessions_accumulated_session_age.up.sql` and `.down.sql` (the next number after 0181); `pkg/gateway/session/sessionstore/sessionstore.go`; `pkg/gateway/session/sessionstore/pgstore/pgstore.go`; `pkg/gateway/session/sessionstore/memstore/memstore.go`; `pkg/gateway/runtime/watchdog/watchdog.go`; `pkg/gateway/sessionserver/resume_held_pod.go`.

1. **Migration.** `ALTER TABLE sessions ADD COLUMN accumulated_session_age_seconds double precision NOT NULL DEFAULT 0 CHECK (accumulated_session_age_seconds >= 0)`. The down migration drops the column.
2. **Field and helpers** in `sessionstore.go`: the field `AccumulatedSessionAgeSeconds float64` on `Session`, and two exported helpers.
   - `AccrueSessionAge(prev session.State, prevUpdatedAt time.Time, row *Session, now time.Time)`: when `prev` is `starting`, `running`, or `input_required`, it adds `max(0, now - prevUpdatedAt)` in seconds to `row.AccumulatedSessionAgeSeconds`.
   - `ActiveAge(row Session, now time.Time) time.Duration`: the accumulated value, plus `now - row.UpdatedAt` when `row.State` is one of those counting states.
3. **The funnel.** In both stores' `Update`, after the mutator returns and before the `UpdatedAt` stamp, call `AccrueSessionAge(prev.State, prev.UpdatedAt, &row, now)` with the same `now` the stamp uses. Add the column to `Create`, to the `UPDATE` statement, to the select list, and to the scan in `pgstore.go`. `Store.Update` is the only writer of the sessions row; grep for any other `UPDATE sessions` statement and route it through the helper. A write that does not change the state still accrues, because the row stays in the counting state across it.
4. **Sweeps.** `sweepMaxAge` and `sweepExpiryWarning` compare `sessionstore.ActiveAge(row, now)` with `effectiveAgeCap` in place of `now.Sub(row.CreatedAt)`. Rewrite the doc comments of `sweepMaxAge`, `sweepExpiryWarning`, and the `resume_pending` and idle-clock comments that describe age as wall-clock time from `CreatedAt`. Cite `// spec: §6.2 (maxSessionAge timer behavior across states)`.
5. **Evaluation on entry to `running`.** The §6.2 paragraph after the pause table evaluates the timer on every transition into `running`. A paused session cannot gain age, so the only over-cap row that can reach `running` is one that left a counting state over the cap before a sweep saw it. Two checks cover it:
   - `sweepResumePending` does not call the CODE-4 driver for a row whose `ActiveAge` exceeds `effectiveAgeCap`. `sweepMaxAge` expires that row on the same or the next tick, because it applies to every non-terminal state.
   - `resumeHeldPod` refuses, with an error the message path already treats as a failed resume, a row whose `ActiveAge` exceeds the effective cap.
   **IMPLEMENTOR'S CHOICE:** how `resumeHeldPod` obtains the effective cap. The constraint is that it returns the value `effectiveAgeCap` returns for the same row, from one shared resolver rather than a second copy of the min-wins logic.

### CODE-2 · Retry accounting

Targets: `pkg/gateway/sessionserver/session_generation.go`; `pkg/gateway/runtime/runtimestore/runtimestore.go`; `pkg/gateway/externalapi/admin/runtimes.go`; `pkg/gateway/externalapi/openapi/openapi.json`; the generated `pkg/ops/mcp/generated_tools.go`.

1. In `bumpRecoveryGeneration`, delete `row.RetryCount++` and the call `s.recordSessionRetry(ctx, updated)`. Rewrite its doc comment so it describes only the `recovery_generation` bump and the `PodAssignment` write, with `// spec: §4.2 (Session Manager)` for `recovery_generation`. The retry metric and audit fire only on the spend edges: `transitionToResumePending` in `failure.go`, and `sweepResuming` through `notifyRetryAttempt` in the watchdog. SPEC-1b **Retry accounting.** is the rule.
2. Delete `SessionPolicy.MaxSessionRetries` and its copy in `runtimestore.go`, and its validation in `admin/runtimes.go`.
3. Delete `sessionPolicy.maxSessionRetries` from `openapi.json`, then regenerate `generated_tools.go` with the existing generator so the drift test stays green. No file under `schemas/` names the field.
4. The admin decoder does not reject unknown fields. A runtime create or update that still carries `maxSessionRetries` is accepted and the key is not persisted. Do not add strict decoding.

### CODE-3 · Binder post-claim callback

Targets: `pkg/gateway/podlifecycle/podsession/resume.go`, `bindrequest.go`, `binder.go`, and the slot-bind request and `Binder.BindSlot`; `pkg/gateway/sessionserver/pod_launch.go`, `slot_bind.go`, `resume_rebind.go`, and `checkpointResumeRequest`.

1. Add `OnClaimed func(context.Context) error` to `podsession.ResumeRequest`, `BindRequest`, and `SlotBindRequest`. A nil hook is a no-op, which covers create and start.
2. Invoke the hook after the claim, and after the slot reservation where there is one, and before the first entry-touching RPC on each path:
   - `Binder.Resume`: after `reserveResumeSlot`, before `cl.Resume`.
   - `Binder.Bind`: after the claim, before `Prepare`.
   - `Binder.BindSlot`: after `connectSlot`, before `materializeSlot`.
3. Release on a hook error, per path. No path issues a compensation RPC, because no entry-touching RPC has run.
   - `Bind`: `ReclaimClaimed(SandboxName, SessionID)`. No lease has been assigned yet.
   - `Resume`: `cl.Close`, `releaseResumeSlot(leaked=false)`, and `podclaim.DeleteClaim` on the claimed sandbox. Do not call `failResume`. `ReclaimClaimed` does not fit here, because its session-wide credential revoke has the wrong scope on a resume.
   - `BindSlot`: `cl.Close` and `ReleaseSlotReservation(leaked=false)`.
4. Return the hook error wrapped in the new sentinel `podsession.ErrOnClaimed`. `applySlotRetryPolicy` and `runWithQueue` treat an error that `errors.Is(err, podsession.ErrOnClaimed)` as non-retryable and stop.
5. Thread the hook from `resumeOnPod` (new parameter `onClaimed`) into `checkpointResumeRequest`, and through `startOnPod` into both `bindReq` and `slotReq`, so a snapshotless rebuild on a pool with `maxConcurrentSessions > 1` reaches it through `bindConcurrentSlot`.
6. The coordination-lease acquire is not part of the hook. CODE-4 acquires the lease once, before `resumeOnPod`.

### CODE-4 · Resume driver, store-only `POST /resume`, buffered delivery, tree-recovery routing

Targets: `pkg/gateway/runtime/watchdog/watchdog.go`; new `pkg/gateway/sessionserver/resume_driver.go`; `pkg/gateway/sessionserver/resume.go`, `resume_rebind.go`, `failure.go`, `podclaimerror.go`, `treerecovery.go`, `messages_delivery.go`, and `sessionserver.go`; `pkg/gateway/externalapi/errorclassify/errorclassify.go`; `pkg/api/v1/session/session.go`; `pkg/gateway/session/sessioninbox/coordinator.go`; `cmd/lenny-gateway/flags.go` and the wiring in `cmd/lenny-gateway`; `sdks/client/go/lenny/client.go`.

1. **Watchdog hook.** Add `ResumeDriver` beside `RetryAttemptNotifier`, with one method `DriveResume(ctx context.Context, row sessionstore.Session)`. In `sweepResumePending`, for a row still inside its window, call it when the wired `TerminalHook` implements `ResumeDriver`, subject to the CODE-1 item 5 age check. The expiry branch is unchanged.
2. **`DriveResume`** returns at once, writing nothing, when any of these holds: a lease store is wired and `leaseStore.Get` does not name this replica, or it returns an error; the session ID is in the per-replica in-flight set; or the session's next-attempt time is in the future. Otherwise it adds the session to the in-flight set and starts `attemptResume` on a goroutine whose context derives from the server-lifetime context, which `Server` cancels on shutdown. The goroutine removes the in-flight entry on exit. Cite `// spec: §7.3 (Resume driver)`.
3. **`attemptResume`**, in this order:
   1. Re-read the row and return unless it is `resume_pending`.
   2. Arm the attempt deadline with `time.AfterFunc` for the time left in the window (`UpdatedAt + maxResumeWindowSeconds - now`, resolved as `sweepResumePending` resolves it), cancelling the attempt context when it fires.
   3. Call `acquireCoordinationLease` once. `ErrHeld` or any other error ends the attempt with no write. The acquires inside `registerBinding` and in the checkpoint branch of `resumeOnPod` stay as idempotent self-renews.
   4. Call `resumeOnPod(ctx, row, onClaimed)`. The `onClaimed` hook is a store `Update` that sets `State = resuming` only when `r.State == resume_pending` and `r.CoordinationGeneration` equals the value item 3.1 read, and otherwise returns `errResumeSuperseded`. A per-attempt flag makes a second invocation in the same attempt a no-op that returns nil, so a slot retry after a post-hook failure does not misfire. When the write succeeds, the hook resets the deadline timer to `MaxResumingSeconds`, which matches the `sweepResuming` anchor.
   5. When the hook never ran, the failure is a pre-claim failure. Record the next-attempt time as `now + backoff + jitter`, where the jitter is uniform in `[0, backoff/2)`, and write nothing.
   6. When the hook returned `errResumeSuperseded`, stop. The binder has already released the claim.
   7. When the hook ran and a later step failed, report through `ReportSessionFailure`, which routes a `resuming` row to `applyFailureFromResuming`. The report carries the generation item 3.1 read in a new optional `FailureReport.ExpectedCoordinationGeneration`; when it is set, the store `Update` in `transitionToResumePending` and `transitionToAwaitingClientAction` also requires `r.CoordinationGeneration` to equal it and otherwise takes the existing `errReportConflict` no-op, so a superseded attempt writes nothing, spends no retry, and bumps no generation. Reports from other reporters leave it unset. A setup-command failure that arrives as `FailedPrecondition` is reported as `setup_command_failed`, and every other post-claim error as `runtime_crash`. Increment `lenny_session_resume_attempts_total{outcome="failure"}`.
   8. On success, call `completeResume`.
4. **`resumeOnPod` publishes nothing.** Both branches return the `BindResult` and the adapter-reported mode without calling `registerBinding`, `podRegistry.Put`, `bumpRecoveryGeneration`, or `fenceResumedPod`. `completeResume` performs those steps after the commit. This keeps a lost commit from leaving a published binding or a `PodAssignment` behind.
5. **`completeResume`**, in this order:
   1. Take the per-session delivery lock.
   2. One store `Update` guarded on `State == resuming` and the generation item 3.1 read: write `running` through `transitionResume`, increment `RecoveryGeneration`, set `PodAssignment` to the result's sandbox, and stamp `LastAgentActivityAt = now` (CODE-6 item 8). Both branches bump `recovery_generation`; the snapshotless branch did not before.
   3. When the guard fails, release the pod through the binder's post-RPC failure release, which runs the §7.1 pod-side reclaim and deletes the claim, then publish nothing, write nothing, release the lock, and stop. **IMPLEMENTOR'S CHOICE:** whether the binder exports `failResume` or wraps it as a new `Binder` method. The constraint is that the release runs the pod-side reclaim on the connection the attempt holds before the claim is deleted.
   4. Publish the binding: `registerBinding` for the rebuild branch, `podRegistry.Put` for the checkpoint branch. Then call `fenceResumedPod`.
   5. Run the tail that `handleResume` runs today after its commit, moved here unchanged: the success counter, the `session.resumed` audit row, `emitStatusChange`, `clearInboxOnResume`, the partial-manifest cleanup, `classifyResumeWithAdapter`, `emitResumedEvent`, `emitChildrenReattached`, and `recoverDelegationTree`.
   6. Call `deliverBuffered`, then release the lock.
6. **`deliverBuffered`.** `sessioninbox` gains `DrainForResume(ctx, tenantID, sessionID)`, which returns the session-inbox entries first, read through the coordinator's inbox in either `messaging.durableInbox` mode, and then the DLQ entries in score order. Session-inbox entries were buffered before the session left the active states, and DLQ entries arrived during recovery, so this order is FIFO. Each drained message goes through the existing §7.2 delivery classifier: delivered when the runtime is idle, otherwise buffered in the inbox. A drained message is never sent as a raw executor turn while a turn is in flight. Cite `// spec: §7.2 (recovering-state DLQ FIFO delivery)`.
7. **Message path lock.** The message path takes the per-session delivery lock around its classify-and-send when this replica holds the session's lease.
8. **Store-only `POST /resume`.** `EndpointResume` admits `StateAwaitingClientAction` and `StateSuspended`. `handleResume` becomes:
   - from `awaiting_client_action`, or from `suspended` with an empty `PodAssignment`: call `enterResumePending`, then answer `200` with the updated row;
   - from `suspended` with a non-empty `PodAssignment`: call `resumeHeldPod` (CODE-6 item 6) and answer `200` with the row in `running`.
   Delete the pre-claim `resuming` write, the `resumeOnPod` call, `holdOrFailOnResumeError` (including the 0091 `ErrHeld` hold and the `failSession` exit), and the `RESUME_FAILED` write. `incSessionResumeAttempt` moves to the driver.
9. **`enterResumePending`** in `failure.go`: a store `Update` guarded on the expected source state (and, for `suspended`, on an empty `PodAssignment`) that writes `resume_pending` without touching `RetryCount`, followed by `migrateInboxOnResumePending` and `emitStatusChange`. The guard failing returns the precondition error `handleResume` already writes. `POST /resume` and the CODE-6 podless message route both call it.
10. **`RESUME_FAILED` removal.** Delete the code from `podclaimerror.go` and `errorclassify.go`. `openapi.json` does not name it.
11. **`ReattachNode`** in `treerecovery.go`: a node in `resume_pending` goes through `DriveResume`, which applies the in-flight set and the lease gate, and a node in `resuming` is skipped. Other states keep their current handling. The unguarded `running` write for a `resume_pending` node is deleted.
12. **Backoff flag.** `--resume-driver-backoff-seconds` / `LENNY_RESUME_DRIVER_BACKOFF_SECONDS`, default 10, operator-tunable, documented in the flag help.
13. **SDK.** Rewrite the `Resume` doc comment in `sdks/client/go/lenny/client.go`: the call returns the session in its new state, and the restore's outcome arrives on the event stream.
14. **Lease gate dependency.** `DriveResume`'s lease gate relies on 0091 replacing the Sweeper's dead-connection lease release with keep-and-renew. Where the lease lapses anyway, CODE-5 adopts the `resume_pending` row, which adds up to one Sweeper period of delay.

### CODE-5 · Sweeper adoption of the SPEC-2a states

Target: `pkg/gateway/coordination/coordination/coordination.go`, in the 0091 version of `Sweep`. Locate edits by function and quoted text.

1. **Predicate.** Replace `isRunningPod` with two predicates.
   - `adoptedState(row)`: the row's state is one that SPEC-2a lists as adopted.
   - `podHeld(row)`: the state is `running`, `input_required`, or `suspended`, and `PodAssignment != ""`.
   Set `adoptable := leaseUnheld && adoptedState(row) && !inAdoptionBackoff`. The never-bound exclusion of implemented proposal 0060 holds, because every state `adoptedState` admits is reachable only after a bind. A `resume_pending` or `awaiting_client_action` row is never re-adopted onto a pod, whatever its `PodAssignment` says, because that pod failed.
2. **Takeover branch.** After a non-zero `RecordHandoff`:
   - when `podHeld(row)`, run `readoptAndFence` and `publish` as today;
   - otherwise skip both, and still call `clearAdoptionBackoff`, `upsertMirror` with the `RecordHandoff` generation, and `held++`.
   On both branches, record the `RecordHandoff` result, never `row.CoordinationGeneration`, in 0091's per-session generation map. When the resume driver later publishes a replacement pod's binding on the adopting replica, the binding's `BindResult.CoordinationGeneration` comes from the post-handoff row through 0091's publish-site rule. A zero `RecordHandoff` keeps the existing release-and-continue path.
3. **Comments.** Rewrite the doc comment of the replaced predicate, the `Sweep` doc comment, and the comment above `eligible`. Each names the adopted states and states that a row with a held pod is re-fenced. Cite `// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §10.1.2 (Coordinator Handoff Protocol)`.

### CODE-6 · Suspension release, idle suspension, suspended lifetime, message routing, and flags

Targets: `pkg/gateway/runtime/watchdog/watchdog.go`; new `pkg/gateway/sessionserver/suspend_release.go`; `pkg/gateway/session/executor` (the pod executor that 0091 CODE-1 gives a turn token); `pkg/gateway/sessionserver/resume_held_pod.go`, `messages_delivery.go`, `interrupt.go`, and `sessionserver.go`; `pkg/gateway/session/messagerouting/messagerouting.go`; `pkg/gateway/session/sessionidle/sessionidle.go`; `cmd/lenny-gateway/flags.go`, `controlserver.go`, and the checkpoint wiring; `charts/lenny/values.yaml`, `values.schema.json`, and `templates/gateway-deployment.yaml`.

1. **Suspension reason.** Add the exported constant `SuspendedReasonIdle = "Idle"` beside the values `suspendReason` returns in `interrupt.go`, and export the two interrupt values as constants. `sweepSuspended` and `SuspendIdle` compare against these constants.
2. **`sweepSuspended`**, called from `Tick`, lists `suspended` rows and, for each one:
   1. When `now - SuspendedAt >= MaxSuspendedSessionSeconds`, write `expired` through a guarded `Update` (`State == suspended && SuspendedAt` unchanged) with `FailureReason = expired:idle` (`session.FailureExpiredIdle`), then call `notifySessionExpiry` with `ExpiryReasonMaxIdleTime` and `recordCompleted`, as `sweepMaxAge` does.
   2. Otherwise, when `PodAssignment != ""` and `now - SuspendedAt >= hold`, call `SuspendedPodReleaser.ReleaseSuspendedPod(ctx, row)` when the hook is wired. The hold is zero when `SuspendedReason == SuspendedReasonIdle`, and the deploy-wide `MaxSuspendedPodHoldSeconds` otherwise.
3. **Hold source.** No tenant-configuration field for `maxSuspendedPodHoldSeconds` exists, so only the deploy-wide value applies. Correct the `Config.MaxSuspendedPodHoldSeconds` comment that claims a per-tenant cap. RECORDS-1 files the tenant cap.
4. **`ReleaseSuspendedPod`** in `suspend_release.go`, under the Design lease gate:
   1. Skip a session whose last checkpoint failure is less than 60 s old (a per-replica retry map).
   2. Checkpoint through the `SuspensionCheckpointer` consumer interface (`CheckpointWithTrigger(ctx, tenantID, sessionID, checkpoint.TriggerPeriodic)`), wired in `cmd/lenny-gateway` to the existing checkpoint service. On failure, emit `session.suspension_checkpoint_failed`, increment `lenny_session_suspension_checkpoint_failed_total`, record the retry time, and stop.
   3. Clear `PodAssignment` through a guarded `Update` (`State == suspended && PodAssignment == row.PodAssignment`). When the guard fails, stop.
   4. Call `releaseExecutor(ctx, s.executor, row.ID, executor.DispositionCompleted)`. On a pod with `maxConcurrentSessions > 1` the executor's existing `SlotID` dispatch releases only the slot.
   5. Emit `session.pod_released_during_suspension` and increment `lenny_session_pod_released_during_suspension_total`.
   A crash between steps 3 and 4 leaves an orphan claim for the claim GC and a row that names no pod. It never leaves a row that names a released pod.
5. **Idle suspension.** Set `idleClockRunningStates` to `{running}`. `sweepIdle` calls `IdleSuspender.SuspendIdle(ctx, row)` for an over-idle row in place of writing `expired`. `SuspendIdle`, under the Design lease gate, returns without a write when `executor.TurnInFlight(row.ID)` reports a turn in flight. Otherwise it writes `suspended` through a guarded `Update` (`State == running && LastAgentActivityAt` unchanged), setting `SuspendedAt = now` and `SuspendedReason = SuspendedReasonIdle`, and calls `emitStatusChange`. It releases nothing: the next `sweepSuspended` tick releases the pod, because the hold is zero. `TurnInFlight(sessionID string) bool` is a new executor accessor over 0091's turn token.
6. **`resumeHeldPod`.** Its guard additionally requires `PodAssignment != ""` when a pod binder is wired, and it stamps `LastAgentActivityAt = now` with the `running` write. Rewrite its doc comment, which marks the podless route as deferred.
7. **Message routing.** In `messagerouting.Classify`, a `suspended` target returns `ActionResumeAndDeliver` whatever `immediate` is. In `messages_delivery.go`, the `ActionResumeAndDeliver` case reads the row's `PodAssignment`. When it is empty and a binder is wired, it calls `enterResumePending` (CODE-4 item 9), buffers the message with `bufferTargetDLQ`, and answers `queued`. When the pod is held, the existing resume-and-deliver path runs. Rewrite the comments that cite line numbers of §7.2.
8. **Idle clock pause.** The clock is paused outside `running` by stamping `LastAgentActivityAt = now` on every transition into `running` from a state where the clock is paused: the CODE-4 commit and `resumeHeldPod`. Without the stamp, a session resumed after a long suspension would be idle-suspended on the next tick.
9. **Defaults.** `watchdog.DefaultMaxIdleSeconds = 900`. In `sessionidle.EffectiveMaxIdleSeconds`, delete the fallback to the age cap so the resolver returns 0 when no policy is set and `DefaultMaxIdleSeconds` is the only default. `Config.MaxSuspendedSessionSeconds` with `DefaultMaxSuspendedSessionSeconds = 604800`.
10. **Flags.** Rename `--max-idle-time-seconds` / `LENNY_MAX_IDLE_TIME_SECONDS` to `--max-client-idle-seconds` / `LENNY_MAX_CLIENT_IDLE_SECONDS` with no alias. Its help states the §6.2 idle suspension, the 900 s default, and that the per-pool `sessionPolicy.maxClientIdleSeconds` and the §27.6 playground override tighten it. Correct the `--max-suspended-pod-hold-seconds` help: the hold applies to interrupt suspensions, and when it ends the gateway checkpoints and releases the pod while the session stays `suspended`. Add `--max-suspended-session-seconds` / `LENNY_MAX_SUSPENDED_SESSION_SECONDS`, default 604800. Update `controlserver.go` for the renamed field.
11. **Chart.** Add `gateway.maxClientIdleSeconds` (900), `gateway.maxSuspendedPodHoldSeconds` (900), and `gateway.maxSuspendedSessionSeconds` (604800) to `values.yaml` and `values.schema.json` (integers, minimum 1), and render them as the matching flags in `templates/gateway-deployment.yaml`. The chart carries no `delegation.budgetKeyTTLSeconds` value and no Go code sets a budget-key TTL, so the SPEC-5 TTL default has no carrier in this proposal; RECORDS-1 files it.

## Staged schema, chart, and migration changes

The migration is part of CODE-1, the `openapi.json` edit and the regenerated MCP tools are part of CODE-2, and the chart values are part of CODE-6. Each lands in the step of the code that reads it.

## Staged docs changes

### DOCS-1a · Reader documentation for the driver, the lease, and the retry budget

Lands with SPEC-1, SPEC-2, and SPEC-3. Targets:

- `docs/client-guide/session-lifecycle.md`: the resume and recovery sections and the `retryPolicy` examples.
- `docs/api/rest.md`: the `POST /v1/sessions/{id}/resume` entry.
- `docs/api/mcp.md`: the `resume_session` entry.
- `docs/reference/error-catalog.md`: delete the `RESUME_FAILED` row; remove `POST /v1/sessions/{id}/resume` and `RESUME_FAILED` from the `SETUP_COMMAND_FAILED` row.
- `docs/reference/state-machines.md`: the `suspended` and `awaiting_client_action` resume edges.
- `docs/reference/configuration.md`: delete the `sessionPolicy.maxSessionRetries` row.
- `docs/operator-guide/configuration.md` and `docs/reference/execution-modes.md`: delete the `maxSessionRetries` line from the `sessionPolicy` examples.
- `docs/runtime-author-guide/runtime-configuration.md` and `docs/runtime-author-guide/publishing.md`: delete `maxSessionRetries` where they list `sessionPolicy` members.

Content: State the retry accounting that SPEC-1b **Retry accounting.** gives. On the client guide and the API pages only, describe automatic recovery first and present `POST /v1/sessions/{id}/resume` as the client override, which returns the session in its new state while the restore's outcome arrives on the event stream.

### DOCS-1b · Reader documentation for active age and idle suspension

Lands with SPEC-4 and SPEC-5. Targets:

- `docs/client-guide/session-lifecycle.md`: the suspension, idle, and expiry passages.
- `docs/reference/state-machines.md`: the `suspended` row and the idle and expiry edges.
- `docs/reference/configuration.md`: the `gateway.maxSuspendedPodHoldSeconds`, `sessionPolicy.maxClientIdleSeconds`, `session.maxClientIdleSeconds`, and `playground.maxIdleTimeSeconds` rows, a new `gateway.maxSuspendedSessionSeconds` row, and a new `delegation.budgetKeyTTLSeconds` row in the delegation table.
- `docs/operator-guide/configuration.md` and `docs/reference/execution-modes.md`: the `maxClientIdleSeconds` example lines.
- `docs/runtime-author-guide/runtime-configuration.md`, `docs/runtime-author-guide/lifecycle.md`, and `docs/getting-started/concepts.md`: the hold, release, and suspended-state passages.
- `docs/client-guide/webhooks.md` and `docs/reference/cloudevents-catalog.md`: the `dev.lenny.session_expired` rows.
- `docs/reference/metrics.md`: the `lenny_session_pod_released_during_suspension_total` and `lenny_session_expiry_total` rows.
- `docs/operator-guide/web-playground.md`: the idle-timeout passages.
- `docs/runbooks/delegation-budget-recovery.md`: the TTL's relation to the suspended-session lifetime.

Content: idle suspension (no turn in flight, default 900 s, the pod or slot released at once), resume on any message with no `delivery: "immediate"` condition, the suspended-session lifetime with its default and the `gateway.maxSuspendedSessionSeconds` setting, active-time session age, the renamed gateway flag, and the budget-key TTL default with its constraint that it exceed the suspended-session lifetime. The `session_expired` and `max_idle_time` descriptions become the suspended-session lifetime expiry.

Both parts: reference tables keep their structure and change only their rows. Cite no section numbers, state no counts, and re-read each table against the prose on its page.

### RECORDS-1 · Findings, claim rows, and the remediation-plan tick

Targets: `BUILD-GAPS.md`; `scripts/seed-claim-register.py` and the regenerated `tests/claim-map.json`; `gateway-runtime-comms-remediation.md`. This deliverable lands after 0091's RECORDS-1, and every closure cites the F-id 0091's implementor assigned under its RECORDS-1 item 18.

1. **Close** F-11.3.35 and F-11.3.36, citing CODE-6 and its tests.
2. **Close** F-15.1.41. CODE-4 deletes `holdOrFailOnResumeError`, and SPEC-3 deletes `RESUME_FAILED`, so no resume exit writes `failed`.
3. **Close** the finding 0091 RECORDS-1 item 4 filed. The WIRED row in item 8 discharges its claim-row obligation, and no R step is created.
4. **Close** the finding 0091 RECORDS-1 item 10 filed. SPEC-2 and CODE-5 close the lease half. SPEC-3 closes the §29.3 `POST /resume` forward half by replacing that off-holder row with store-write rows.
5. **Close** the findings 0091 RECORDS-1 items 11 and 17 filed.
6. **Rewrite in place** the finding 0091 RECORDS-1 item 16 filed so that it covers only `starting`, with a resolution note that CODE-5 closed the held-pod `suspended` half, and leave it OPEN.
7. **File** these findings, each with the next free `F-<section>.<n>` identifier in the section that owns it:
   - (a) A snapshotless rebuild reports `resumeMode: full` (`classifyResume`), which §7.2 defines as a full restore from a checkpoint. Filed under §7.2.
   - (b) A message or a `POST /v1/sessions/{id}/resume` call to a held-pod `suspended` session served off-holder runs `resumeHeldPod` locally with no §29.3 forward, and the exposure now covers every message rather than `delivery: immediate` alone. Filed under §29.3.
   - (c) `nodeNeedsRecovery` judges orphanhood from the local pod registry, so the root's lease holder can treat a descendant bound on a peer replica as orphaned. Filed under §8.10.
   - (d) Delegation budget keys carry no TTL: no code passes `delegation.budgetKeyTTLSeconds`, no chart value carries it, and the SPEC-5o re-arm has no implementation. Filed under §8.3.
   - (e) No tenant-configuration field carries `maxSuspendedPodHoldSeconds`, so the §6.2 `min(deployment_value, tenant_value)` rule applies only the deploy-wide value. Filed under §6.2.
8. **Claim rows.** Add `WIRED` rows to `scripts/seed-claim-register.py` for the §7.3 resume driver (including the §28.5.1 `CH-ATTACH` replacement-pod re-attach sentence), the §10.1.1 adoption rule, the suspension release, the idle suspension, and active session age. Regenerate `tests/claim-map.json` with the script; a hand edit fails the tier-0 reproducibility gate. **IMPLEMENTOR'S CHOICE:** each row's `note` wording. The constraint is one clause stating what the surface does, as every `EXPLICIT` entry carries.
9. **Remediation plan.** Tick "Session suspend-and-resume proposal (not yet written), after proposal 0091 and before proposal 0087" in `gateway-runtime-comms-remediation.md` §10.2, recording the proposal number and implementation date. In the text of the recycling follow-up item, add the gap that an `expired` session's pod is recycled on a recycling pool, which this proposal does not take.

## Testing

Every test carries a `// spec:` annotation naming the section and heading it exercises, and every tier-2-and-higher test carries a `// diagnosis:` comment. Tier-1 tests land in the step of the code they test. TEST-1 holds the tests at tier 2 and above, and the cross-cutting tier-0 and tier-11 checks.

### Tests that land with CODE-1

- Tier 1, both stores: a row that goes `running → suspended → resume_pending → resuming → running` accrues only the `running` intervals, measured with an injected clock. A write that keeps the state at `running` accrues the interval since the previous write. `// spec: §6.2 (maxSessionAge timer behavior across states)`.
- Tier 1, watchdog: a session created 3 hours ago with 1 hour of active time is not expired at a 2-hour cap; a session with 2 hours and 1 second of active time is expired, from `running` and from `suspended`. `sweepExpiryWarning` fires against active age. The discriminating assertion is the 3-hour-old session surviving.
- Tier 1: `sweepResumePending` does not call the driver for an over-cap row, and `resumeHeldPod` refuses one.
- Tier 4: migration 0182 applies and rolls back, the `CHECK` rejects a negative value, and pgstore round-trips the column.

### Tests that land with CODE-2

- Tier 1: one retryable failure followed by a successful resume leaves `RetryCount == 1` and `RecoveryGeneration` incremented by 1. With `maxRetries = 2`, a second failure still enters `resume_pending`. `lenny_session_retry_total` and `session.retry_attempted` fire once per spend edge and never on resume success. `// spec: §7.3 (Retry and Resume)`.
- Tier 1: update `runtimestore_test.go`, `admin/runtimes_test.go`, `admin/pools_test.go`, `poolstore_test.go`, and the `openapi_test.go` property list so none sets or expects `maxSessionRetries`.

### Tests that land with CODE-3

- Tier 1, one case per path (`Resume` exclusive, `Resume` slot, `Bind`, `BindSlot`): the hook runs after the claim and before the first RPC; a hook error deletes the claim or releases the slot reservation and issues no compensation RPC; the error matches `podsession.ErrOnClaimed`.
- Tier 1: `applySlotRetryPolicy` does not retry an `ErrOnClaimed` error. A post-hook failure followed by a slot retry invokes the driver's hook again without returning `errResumeSuperseded`.

### Tests that land with CODE-4

- Tier 1, watchdog: `ResumeDriver` is called only for rows inside the window, and the expiry branch is unchanged.
- Tier 1, driver: a non-holder does nothing; a lease read error does nothing; a session already in flight is not started twice; a pre-claim failure writes nothing, sets the next-attempt time, and the window still elapses to `awaiting_client_action`; a `resume_pending → cancelled` write before the hook makes the hook return `errResumeSuperseded` and the claim is released; a `RecordHandoff` bump after the item 3.1 read makes the hook return `errResumeSuperseded` and release the claim, and a bump between the hook and the commit makes the commit release the pod and publish nothing; a bump before a post-claim failure makes the report write nothing; a post-claim failure takes the `applyFailureFromResuming` exits, with a setup `FailedPrecondition` landing in `awaiting_client_action` as `setup_command_failed`; a lost commit publishes no binding, writes no `PodAssignment`, and releases the pod with the pod-side reclaim; the deadline before the claim is the remaining window, and after the hook it is `MaxResumingSeconds`.
- Tier 1: buffered delivery returns session-inbox entries, in-memory and durable, before DLQ entries and sends no drained message while a turn is in flight. `// spec: §7.2 (Interactive Session Model)`.
- Tier 1, `pkg/api/v1/session/session_test.go`: `EndpointResume` admits `suspended` and `awaiting_client_action` and rejects the others. Update the cases that assert the `suspended` exclusion.
- Tier 1: `handleResume` from `awaiting_client_action` and from podless `suspended` writes `resume_pending`, returns `200`, and claims no pod; from held-pod `suspended` it returns `200` with `running`.
- Tier 1: `ReattachNode` routes a `resume_pending` node through `DriveResume` and skips a `resuming` node.
- Tier 1: update `slotretry_test.go`, `resume_setup_demotion_internal_test.go`, `podclaimerror_internal_test.go`, and `errorclassify_test.go`, which pin `RESUME_FAILED` and the demotion.

### Tests that land with CODE-5

- Tier 1: the predicate adopts each state SPEC-2a lists with a lapsed lease, and no other non-terminal state. A podless row (`resume_pending`, `awaiting_client_action`, or `suspended` with no `PodAssignment`) gets exactly one generation bump and no readopt; a held-pod `suspended` row is re-fenced. `// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination)`.
- Tier 1: the dead-connection branch releases the lease of a bound session already in `resume_pending`, and a later sweep adopts the row with exactly one generation bump and no readopt.
- Tier 1: the per-session generation map records the `RecordHandoff` value on the podless takeover edge.

### Tests that land with CODE-6

- Tier 1, watchdog: `sweepSuspended` expires a row at `MaxSuspendedSessionSeconds` and not one second before; releases an `Idle` row on the first tick; releases an interrupt row only after the hold; never releases a podless row. `sweepIdle` calls the hook only for `running`.
- Tier 1, session server: `SuspendIdle` writes nothing when a turn is in flight, when the replica is not the holder, or when `LastAgentActivityAt` moved; `ReleaseSuspendedPod` keeps the pod on a checkpoint failure and retries after 60 s, and clears `PodAssignment` before the release.
- Tier 1: `resumeHeldPod` refuses a `suspended` row with an empty `PodAssignment` when a binder is wired; `messagerouting.Classify` returns `ActionResumeAndDeliver` for a `suspended` target without `immediate`; the podless message branch writes `resume_pending`, buffers to the DLQ, and answers `queued`.
- Tier 1: `EffectiveMaxIdleSeconds` returns 0 with no policy set; the renamed flag parses and the old one is rejected.
- Helm unittest in `charts/lenny/tests/gateway-deployment_test.yaml`: the three flags render from values, and `values.schema.json` rejects a negative value.

### TEST-1 · Tests at tier 2 and above, and the tier-0 and tier-11 checks

- **Tier 0.** Regenerate `openapi.json` and `generated_tools.go`; the codegen drift check passes.
- **Tier 2.** `tests/tier2_component/stores/runtimestore_test.go` no longer sets or reads `MaxSessionRetries`, and the runtime store round-trips a `SessionPolicy` without it.
- **Tier 3**, `tests/tier3_contract`: `POST /v1/sessions/{id}/resume` returns `200` with `resume_pending` from `awaiting_client_action` and from podless `suspended`, and `running` from held-pod `suspended`; `RESUME_FAILED` is absent from the error catalog and from `openapi.json`; `openapi.json` and the generated MCP schema no longer list `sessionPolicy.maxSessionRetries`, and a runtime create that carries the key does not persist it; `lenny/resume_session` and `POST /resume` produce the same transition from each admitted state.
- **Tier 4**, `tests/tier4_integration`: a pod crash reaches `running` with no client call; `POST /resume` served by a non-holder replica leads to `running`; with two replicas, killing the holder in `resume_pending` lets the peer adopt and drive the restore after the lease lapses; a message to a podless `suspended` session is delivered after the restore, before a later message.
- **Tier 5**, `tests/tier5_e2e_kind`: an idle session is suspended, its pod is released with the recycle disposition on a recycling pool, and a later message resumes it on a fresh pod; on a concurrent pool the release frees one slot and the other session keeps running; a held-pod `suspended` session's coordinator is killed and the adopter fences the pod.
- **Tier 7a**, `tests/tier7a_load_local`: two drivers on one `resume_pending` row claim exactly one pod; `ReattachNode` racing the watchdog driver on one descendant claims exactly one pod; idle suspension racing a message either suspends then resumes or does not suspend, and never loses the message; `POST /resume` racing `DELETE` ends `cancelled` with no pod left claimed; a message arriving during `deliverBuffered` is delivered after the buffered ones.
- **Tier 8**, `tests/tier8_chaos`: the coordinator is killed in `resume_pending` and in held-pod `suspended`, and the session recovers on the peer; a crash between the suspension's binding clear and the release leaves an orphan claim and an intact checkpoint, and the session resumes from it.
- **Tier 11**, `tests/tier11_docs`: the docs, the flag help, and the claim map are consistent with the staged spec, and no reader page names `RESUME_FAILED` or `maxSessionRetries`.

## Edge cases and accepted failure modes

- **Hung pre-claim wait.** A claim that waits on an exhausted pool is bounded by the remaining window, after which the attempt context is cancelled and `sweepResumePending` moves the row to `awaiting_client_action`.
- **Driver goroutine at shutdown.** The server-lifetime context cancels an in-flight attempt. A cancelled attempt before the hook writes nothing. One after the hook leaves a `resuming` row for `sweepResuming` and the peer's adoption.
- **Message on a non-coordinator replica during a drain.** The per-session delivery lock is in-process, so it orders only messages served by the coordinating replica. A message served elsewhere follows the existing off-holder behavior, which RECORDS-1 finding (b) records for held-pod `suspended` and which the §29.3 `running` forward governs once built.
- **Idle-suspended session with a delegation tree.** Children keep running. The parent's suspension releases only its own pod or slot.

## Files touched on application (non-spec)

- `migrations/0182_sessions_accumulated_session_age.up.sql`, `migrations/0182_sessions_accumulated_session_age.down.sql`
- `pkg/gateway/session/sessionstore/sessionstore.go`, `pgstore/pgstore.go`, `memstore/memstore.go`, and their tests
- `pkg/gateway/runtime/watchdog/watchdog.go` and its tests
- `pkg/gateway/sessionserver/session_generation.go`, `resume.go`, `resume_rebind.go`, `resume_driver.go` (new), `resume_held_pod.go`, `failure.go`, `podclaimerror.go`, `treerecovery.go`, `messages_delivery.go`, `pod_launch.go`, `slot_bind.go`, `interrupt.go`, `suspend_release.go` (new), `sessionserver.go`, and their tests
- `pkg/gateway/podlifecycle/podsession/resume.go`, `bindrequest.go`, `binder.go`, the slot-bind request and `BindSlot`, and their tests
- `pkg/gateway/coordination/coordination/coordination.go` and its tests
- `pkg/gateway/session/executor` (the pod executor's `TurnInFlight`), `pkg/gateway/session/messagerouting/messagerouting.go`, `pkg/gateway/session/sessionidle/sessionidle.go`, `pkg/gateway/session/sessioninbox/coordinator.go`, and their tests
- `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/externalapi/admin/runtimes.go`, `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/ops/mcp/generated_tools.go`, and their tests
- `pkg/api/v1/session/session.go`, `session_test.go`
- `cmd/lenny-gateway/flags.go`, `controlserver.go`, and the wiring files
- `sdks/client/go/lenny/client.go`
- `charts/lenny/values.yaml`, `values.schema.json`, `templates/gateway-deployment.yaml`, `tests/gateway-deployment_test.yaml`
- `tests/tier2_component/stores/runtimestore_test.go`, `tests/tier3_contract/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier11_docs/`
- The DOCS-1a and DOCS-1b pages
- `BUILD-GAPS.md`, `scripts/seed-claim-register.py`, `tests/claim-map.json`, `gateway-runtime-comms-remediation.md`
