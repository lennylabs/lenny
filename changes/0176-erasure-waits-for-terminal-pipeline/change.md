# Erasure waits for the terminal pipeline

Status: `In progress`, revision 7, 2026-11-27.

## Problem

The user erasure job pseudonymizes the user's billing events and then verifies that neither the erasure salt nor the original user id survives ([§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces)). The verification fails intermittently in the merge gate, and in a run where it passes it can pass with a raw `user_id` still in `billing_events`. Issue [ERASE-I1](../../issues/closed/ERASE/ERASE-I1-billing-verification-fails-intermittently.md) records the evidence: gate runs `5fe88e7f64fb9c324fbd3653` and `f26af57d68d9f604063ec524` failed with `billing erasure verification failed: the erasure salt or the original user id survived pseudonymization`, and the surviving row is the `session.completed` billing event of the session the job drained, carrying the raw `user_id`.

The cause is an ordering gap that the specification states and the code implements:

- The erasure job drains the user's sessions (`admin.DrainSessions`, then `ForceTerminate`, then `OnSessionTerminal`). `OnSessionTerminal` runs the terminal pipeline in a detached goroutine (`recordSessionCompleted` in `pkg/gateway/core/session/sessionserver/usage.go`): the terminal steps, the pod release, and then `afterTerminalRelease`, which closes the session log, writes the final quota checkpoint, and appends the `session.completed` billing event with the raw `user_id`.
- The job waits only on the release gate (`admin.ReleasePending`, which applies `sessionstore.InPostTerminalWindow`). A session is pending only while its terminal row names a pod and records no release. A session with no pod is never pending, and for a session with a pod the window closes before `afterTerminalRelease` runs.
- §12.8 **Lifecycle fence and drain.** defers store deletion only while a session is in the §7.1 post-terminal window, and §7.1 **Post-terminal window:** ends that window when the release is recorded, while **Terminal pipeline context:** lists steps after the release. The specification therefore leaves the terminal billing event outside the wait.

When the late row lands between the pseudonymizing step and the verification, the job fails and the user stays restricted until a retry. When it lands after the verification, the job completes and writes a verified receipt while the raw `user_id` stays in `billing_events`, which §12.8 forbids. Tenant deletion uses the same predicate before Phase 3, so its later phases can also run while a drained session's pipeline still writes.

The owner settled the remedy as decision 1: the session row records the end of its terminal pipeline, and erasure and tenant deletion wait for that record or for the pipeline's budget.

## Scope

The change covers:

- A mark on the session row that records the end of the session's terminal pipeline, written by the replica that ran the pipeline.
- The predicate that user erasure (`store_deleting`) and tenant deletion (Phase 2) wait on, which becomes "a terminal session whose pipeline has not recorded its end and whose terminal pipeline budget has not elapsed", for sessions with and without a pod.
- §7.1 **Terminal pipeline context:** and **Post-terminal window:**, and §12.8 **Lifecycle fence and drain.**

It leaves out:

- The §7.1 post-terminal window itself, which keeps governing when a session holds its pod and slot (`HoldsPod`, the slot queries, and the warm-pool controller's `claimOrphanTimeout`). Widening that window would hold pods and slots for the rest of the pipeline, which the pod accounting does not need.
- Re-pseudonymizing when verification finds a row, and refusing a billing append for a restricted user (option (c) of decision 1).
- Moving the billing, quota-checkpoint, and session-log steps before the pod release (option (b) of decision 1).
- A billing event that the pipeline hands to the failover tiers of §11.2.1 (the Redis stream or the in-memory buffer) because the primary write failed. Such an event reaches `billing_events` when a flusher drains it, which can be after the mark. Decision 6 makes the pipeline's only billing event, `session.completed`, carry no user id, so such an event holds nothing user erasure rewrites (EW-5). The per-replica buffer is the subject of finding F-12.8.53 in `BUILD-GAPS.md` and issue ERASE-I4, and §12.4 states that user erasure purges the stream.

## Terms

| Term | Meaning |
| :-- | :-- |
| Terminal pipeline | The steps the gateway runs after a terminal write commits, under one context it owns ([§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**): seal and export, inbox drain, terminal events and signals, pod or slot release, delegation budget return, session-log close, final quota checkpoint, and the `session.completed` billing event. In the code it is `recordSessionCompleted`. |
| Terminal pipeline budget | The bound on the whole pipeline, counted from the terminal transition: the largest `maxWorkspaceSealDurationSeconds` of any pool plus the release allowance (§7.1). The gateway computes it as `terminalPipelineBudgetInEffect` and passes it to the release gate as `postTerminalCap`. |
| Post-terminal window | The interval in which a terminal session whose row names a pod still holds the pod: from the terminal transition until the release is recorded, and for no longer than the terminal pipeline budget (§7.1 **Post-terminal window:**). |
| Pipeline mark | The new `terminal_pipeline_done_at` column of the session row: the instant the replica that ran the session's terminal pipeline finished its last step. |
| Pipeline-pending session | A terminal session whose pipeline mark is not set and whose terminal transition is less than one terminal pipeline budget ago. |

## High-level design

When the gateway finishes a session's terminal pipeline, it writes the time on the session row. User erasure and tenant deletion, after their drain, list the scope's sessions from the Postgres primary as they do today, and wait while any terminal session in the scope has no such time recorded and ended less than one pipeline budget ago. Every write of the pipeline happens under a context whose deadline is the end of that budget, so once the budget has elapsed no step of the pipeline can still write, whether or not the replica wrote the mark. A session with no pod waits on the same rule, so the billing event of a session that never had a pod is also covered.

The pod accounting does not change. A session still releases its pod at the same point, and the slot queries still read the post-terminal window.

## Requirements

None by identifier. §7.1 is content of the placeholder page SESS, and §12.8 of the placeholder page ERASE.

## Current state

The state on `main` at `6bc1c2859`:

- `pkg/gateway/core/session/sessionserver/usage.go`: `OnSessionTerminal` starts `recordSessionCompleted` in a goroutine under `BeginTerminal`. `recordSessionCompleted` takes the bounded context from `terminalContext`, runs `afterTerminalWrite` (which includes the release), then `afterTerminalRelease`. `afterTerminalRelease` closes the session log, forgets the input-wait counter, writes the quota checkpoint, and appends the `session.completed` billing event, and it returns early when no billing store is wired.
- `pkg/gateway/core/session/sessionserver/sessionserver.go`, `terminalContext`: a context detached from the caller with a deadline at the terminal transition plus the terminal pipeline budget.
- `pkg/session/sessionstore/terminal_mark.go`: `InPostTerminalWindow` (terminal, names a pod, no release marker, `TerminatedAt` set, inside the cap), `HoldsPod`, and `MarkPodReleased`.
- `pkg/gateway/api/admin/drain.go`: `ReleasePending` lists the scope from the primary and returns the identifiers for which `InPostTerminalWindow` holds. `ErrReleaseCapUnset` fails closed on a non-positive cap.
- `pkg/storage/erasurejob/runner_phases.go`, `awaitRelease`, and `pkg/storage/erasurejob/runner.go`, `ErrReleasePending`: the job stays in `store_deleting` with no failure recorded while the gate lists a session. `runner_sweep.go` treats the error as a wait.
- `cmd/lenny-gateway/adminrouter.go` wires the gate for user erasure, and `cmd/lenny-gateway/tenantdeletion.go`, `tenantSessionTerminator`, calls `ReleasePending` for tenant deletion. Both pass `postTerminalCap` from `cmd/lenny-gateway/stores.go`.
- `pkg/billing/billingstore/failover/failover.go`, `Pipeline.Append`: the primary path is a synchronous Postgres insert, so the billing row has committed when `Append` returns on that path.
- `migrations/`: the highest migration is `0203_issued_tokens_depth`. The `sessions` table has `pod_released_at` and `terminated_at`, and no column for the end of the pipeline.

## Detailed design

### EW-1 The pipeline mark

The `sessions` table gains `terminal_pipeline_done_at TIMESTAMPTZ` (NULL until written), in a new migration. `sessionstore.Session` gains `TerminalPipelineDoneAt`, which the Postgres store reads and writes, and the in-memory store keeps. `sessionstore.MarkTerminalPipelineDone(now)` returns the `Update` mutator that sets the column on a terminal row that has no mark, and returns `ErrSuperseded` on a non-terminal row or a row that already has one, as `MarkPodReleased` does.

`recordSessionCompleted` writes the mark in one `Update` after `afterTerminalRelease` returns, whatever the outcome of its steps, and on every path, including the one with no billing store. It writes with the pipeline's own context. When that context has expired, the write fails and the budget already ends the wait, so the failure is logged and changes nothing else. A pipeline that the process exit cuts writes no mark, and the budget ends the wait for its session as well.

### EW-2 The pending predicate

`sessionstore.TerminalPipelinePending(s, now, budget)` reports true when `s` is terminal, `TerminatedAt` is set, `TerminalPipelineDoneAt` is not set, and `now` is before `TerminatedAt` plus `budget`. A non-positive budget reports false, and a terminal row with no `TerminatedAt` reports false, for the reasons `InPostTerminalWindow` states for the same cases. The predicate does not read `PodAssignment` or `PodReleasedAt`.

`admin.ReleasePending` is replaced by `admin.TerminalPipelinesPending`, which lists the scope from the primary with the filter `DrainSessions` uses and returns the identifiers for which `TerminalPipelinePending` holds. It keeps `ErrReleaseCapUnset`, renamed for the budget, so an unwired budget still fails closed. The erasure job's `awaitRelease` and `ErrReleasePending`, and the tenant-deletion terminator, take the new names, and their messages name the terminal pipeline in place of the post-terminal window. The job still waits in `store_deleting` and records no failure.

Every write of the pipeline runs under `terminalContext`, whose deadline is the terminal transition plus the budget, so the predicate's budget arm is a bound on the pipeline's writes. The budget the gate passes is the one `terminalContext` applies (`postTerminalCap`), and a test pins the two to one value.

### EW-3 Terminal writers that run no pipeline

A path that writes a terminal row without calling `OnSessionTerminal`, such as a `Create` of a terminal row, leaves the row pending until the budget elapses. That delays an erasure or a tenant deletion by at most one budget and never shortens the wait. The build lists the terminal writers and records in the step's digest which of them run the pipeline.

### EW-4 The specification

§7.1 **Terminal pipeline context:** gains the session-log close, the final quota checkpoint, and the `session.completed` billing event in its list of steps, and states that the replica that runs the pipeline records on the session row when its last step ends, and that no step writes after the budget. A new paragraph after **Post-terminal window:** states the pipeline-pending rule and that the post-terminal window still governs only the pod, the slot, the binding, and the checkpoint intent row. §12.8 **Lifecycle fence and drain.** replaces "while a session of the scope is in its post-terminal window" with the pipeline-pending rule, for sessions with and without a pod.

### EW-5 The margin, the terminal writers, and `session.completed` (0176.S2)

- **Margin (decision 3).** `admin.TerminalPipelinesPending` takes a `margin` beside the budget and reads both of its arms under the budget plus the margin. The gateway flag `--terminal-pipeline-wait-margin` (environment variable `LENNY_TERMINAL_PIPELINE_WAIT_MARGIN`, default 30 s, `admin.DefaultPipelineWaitMargin`) sets it, and the erasure pipeline gate and the tenant-deletion terminator pass it. The session store's slot queries, the retention collector, and the pipeline's own context keep the budget alone. A negative margin, or one whose sum with the budget overflows, fails the gate closed with `admin.ErrPipelineWaitMarginInvalid`, and the gateway refuses to start with it. §12.8 **Lifecycle fence and drain.** states the margin and the deployer's responsibility for a budget raised during a rollout, and §7.1 **Pipeline-pending session:** points to it.
- **Terminal writers (decision 4).** A new §7.1 paragraph, **Terminal writers and the pipeline steps:**, lists the writers that run the whole pipeline, the writers that run the post-terminal hook and the pipeline mark with no session-log close, final quota checkpoint, or `session.completed` event, and the writers that run no pipeline, as the 0176.S1 digest found them. Whether every writer should run the whole pipeline is issue SEND-I1. §11.2.1's `session.completed` row names the writers of the first group.
- **`session.completed` (decision 6).** The terminal pipeline (`sessionCompletedEvent` in `pkg/gateway/core/session/sessionserver/usage.go`) and the watchdog's fallback without a terminal hook write `session.completed` with an empty `user_id`. §11.2.1's `user_id` row states it and the join on `session_id` with `session.created`, and §7.1 **Terminal pipeline context:** states that an event written late carries nothing user erasure pseudonymizes.

## Worked example

`alice` is erased while her session `s1` runs on pod `p1`. The job drains `s1`: the forced terminal write commits at 12:00:00, and replica `gw-2` starts the pipeline. In this deployment the terminal pipeline budget is 330 seconds. `gw-2` releases `p1` at 12:00:02, so the post-terminal window closes and the pod returns to its pool. The job's next pass at 12:00:03 lists `s1`, finds it terminal with no pipeline mark and less than 330 seconds old, and stays in `store_deleting`. `gw-2` appends the `session.completed` event, which carries no user id, at 12:00:04 and writes the mark at 12:00:04. The job's next pass finds no pending session, deletes the stores, pseudonymizes the billing events, which rewrites the `session.created` row, and the verification passes. Had `gw-2` crashed at 12:00:03, the job would wait until 12:06:00, the budget plus the default 30-second margin, after which no step of the pipeline can write.

## Limits, and alternatives rejected

- An erasure or a tenant deletion waits up to one terminal pipeline budget plus the pipeline wait margin when a pipeline writes no mark, by default the largest seal duration plus 60 seconds.
- A billing event routed to the failover tiers of §11.2.1 is outside the mark, as "Scope" states.
- Option (b), moving the billing, quota-checkpoint, and session-log steps before the pod release and extending the wait to sessions without a pod, was not taken (decision 1). It reorders the pipeline against the seal and release ordering of §7.1 and leaves any future step after the release outside the wait.
- Option (c), option (a) together with re-pseudonymizing when the verification finds a row and refusing billing writes for a restricted user, was not taken (decision 1).

## Specification and non-specification changes

- `spec/07_session-lifecycle.md`: §7.1, **Terminal pipeline context:** and **Post-terminal window:**, and a new paragraph on the pipeline mark.
- `spec/12_storage-architecture.md`: §12.8, **Lifecycle fence and drain.**
- `migrations/`: a new migration adding `sessions.terminal_pipeline_done_at`.
- `pkg/session/sessionstore`, `pkg/session/sessionstore/pgstore`, and `pkg/session/sessionrow`: the field, the column, the mutator, and the predicate.
- `pkg/gateway/core/session/sessionserver`: the mark write at the end of `recordSessionCompleted`.
- `pkg/gateway/api/admin`: `TerminalPipelinesPending` in place of `ReleasePending`.
- `pkg/storage/erasurejob`: the gate's names and messages.
- `cmd/lenny-gateway`: the wiring in `adminrouter.go` and `tenantdeletion.go`.
- 0176.S2: `spec/07_session-lifecycle.md` §7.1, **Terminal pipeline context:**, the new **Terminal writers and the pipeline steps:**, and **Pipeline-pending session:**; `spec/11_policy-and-controls.md` §11.2.1, the `session.completed` event row and the `user_id` field row; `spec/12_storage-architecture.md` §12.8 **Lifecycle fence and drain.**; `pkg/gateway/api/admin` (the margin, `DefaultPipelineWaitMargin`, `PipelineWaitMarginValid`, `ErrPipelineWaitMarginInvalid`); `cmd/lenny-gateway` (the flag, its startup check, and the wiring); `pkg/gateway/core/session/sessionserver/usage.go` and `pkg/gateway/core/session/watchdog/watchdog.go` (`session.completed` without a user id).

## Documentation

- `docs/operator-guide/security.md` (operator): the "Erasure Process" list states that after the session drain the job waits until each drained session's terminal steps, the final billing event included, have finished, or until the terminal pipeline budget has elapsed, before it deletes any data.
- `docs/operator-guide/configuration.md` (operator), 0176.S2: a new "Erasure and tenant-deletion wait" section with the flag `--terminal-pipeline-wait-margin`, its environment variable, its default of 30 s, the startup refusal of a negative or overflowing value, what the margin covers and what it leaves alone, the deployer's responsibility for a budget raised during a rollout and the order of rollouts that keeps the wait, and that the chart renders neither the margin nor the release allowance (ERASE-I6, SEND-I2); and a new "Event Fields" section under "Metering and Billing" stating that `session.completed` carries no user id, the join on `session_id` with `session.created`, and the ways a session ends with no `session.completed` event.
- `docs/operator-guide/security.md` (operator), 0176.S2: the "Erasure Process" wait includes the margin, and the billing pseudonymization item states that `session.completed` carries no user id.
- `docs/operator-guide/multi-tenancy.md` and `docs/runbooks/tenant-deletion-overdue.md` (operator), 0176.S2: the Phase 2 wait includes the margin.
- No reader-facing behavior changes: SESS.

## Tests

- Unit, `pkg/session/sessionstore`: `TerminalPipelinePending` with and without a pod, with and without a release recorded, with the mark set, at the budget boundary, with a zero `TerminatedAt`, and with a non-positive budget; `MarkTerminalPipelineDone` on a terminal row, on a row with a mark, and on a non-terminal row. Cites §7.1 (Normal Flow).
- Unit, `pkg/gateway/core/session/sessionserver`: the mark is written after the `session.completed` append and the quota checkpoint, on the path with no billing store, and not when the pipeline's context has expired. Cites §7.1 (Normal Flow).
- Unit, `pkg/gateway/api/admin` and `cmd/lenny-gateway`: the lister fails closed on a non-positive budget, and the gate's budget equals the one `terminalContext` applies. Cites §12.8 (Compliance Interfaces).
- Component, `tests/component/stores`: the Postgres store reads and writes the mark, and the primary listing returns it. Cites §7.1 (Normal Flow).
- Load_local: a drain and the erasure runner with injected delays before the billing append and before the verification, under `-race`, leave no raw `user_id` in billing events after `completed`. Cites §12.8 (Compliance Interfaces).
- Integration: after an erasure job completes, plus one terminal pipeline budget, no billing event carries the raw `user_id`; the same for a session without a pod; tenant deletion does not leave Phase 2 while a drained session's pipeline has not ended. The existing integration tests stay as written, because a wait for `session.completed` in a test would hide the defect. Cites §12.8 (Compliance Interfaces).
- Security, `tests/security`: erasure completeness, with a delayed terminal pipeline, finds no raw `user_id` in any store the job verifies. Cites §12.8 (Compliance Interfaces).
- 0176.S2, unit: the gate's margin at the boundary of each arm, with a zero, a negative, and an overflowing margin (`pkg/gateway/api/admin`); the flag's default, the startup check, and that the margin holds the erasure gate and the tenant-deletion terminator while the retention collector, which reads the budget alone, does not wait (`cmd/lenny-gateway`); a `session.completed` event appended after the user's billing events were pseudonymized carries no user id and keeps the other fields (`pkg/gateway/core/session/sessionserver`); the watchdog fallback writes `session.completed` without a user id (`pkg/gateway/core/session/watchdog`). Integration and security: the drained sessions' `session.completed` rows carry an empty `user_id`. Cite §12.8 (Lifecycle fence and drain) and §11.2.1 (Billing Event Stream).
