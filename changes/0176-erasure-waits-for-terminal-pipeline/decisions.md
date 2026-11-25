# Decisions: 0176 Erasure waits for the terminal pipeline

## Open decisions

### O1 The erasure wait also keeps the post-terminal window

Raised during the build of 0176.S1, 2026-11-25. EW-2 replaces the release gate (`admin.ReleasePending`, which reads `InPostTerminalWindow`) with `TerminalPipelinePending` alone. The two predicates differ when the replica that runs a session's terminal pipeline does not hold the session's binding. The chaos test `TestLostBindReleaseFanOutHoldsErasureUntilReleaseOrWindowCap_spec_28_5_7` (`tests/chaos/holder_forward_chaos_test.go`) builds that case: the erasure drain runs the pipeline on replica a, the `CH-BINDRELEASE` publish to replica b is lost, and b keeps the binding with no release recorded. The pipeline on a ends and records its pipeline mark, so `TerminalPipelinePending` reports false while the session is still in its post-terminal window on b. §7.1 **Post-terminal window:** item 3 states that a component which treats a terminal session as finished does not act on the pod, the binding, or the intent row of a checkpoint in progress while the window lasts, and `DeleteByUser` deletes checkpoint intent rows and objects.

- (a) The wait holds while a session is pipeline-pending or in its post-terminal window. Both arms end within one terminal pipeline budget, so the longest wait is unchanged, and the lost fan-out case keeps the guarantee the chaos test pins. The pipeline-pending predicate itself reads no pod field, as EW-2 states.
- (b) The wait holds only while a session is pipeline-pending, as EW-2 states. The chaos test changes to expect the erasure to proceed once the pipeline on replica a has recorded its mark, while replica b still holds the binding.

Recommendation: (a). The branch implements (a) in `admin.TerminalPipelinesPending` and in §12.8 **Lifecycle fence and drain.** pending the owner's answer. Option (b) changes that function's predicate, the §12.8 sentence, the chaos test, the `marked-binding-held` row of `TestTerminalPipelinesPendingListsTheSessionsErasureWaitsOn` (`pkg/gateway/api/admin/drain_test.go`), the pod-release clause of `docs/operator-guide/security.md`, `docs/operator-guide/multi-tenancy.md`, and `docs/runbooks/tenant-deletion-overdue.md`, and the doc comments of `admin.TerminalPipelinesPending`, `erasurePipelineGate` (`cmd/lenny-gateway/adminrouter.go`), `tenantSessionTerminator` (`cmd/lenny-gateway/tenantdeletion.go`), and `WithPipelineGate` (`pkg/storage/erasurejob/runner.go`).

### O2 A billing append after the budget reaches the failover tiers

Raised by the review of 0176.S1, 2026-11-25. A terminal pipeline that runs past its budget, such as a seal that runs to its maximum followed by a slow release, still calls `billing.Append` for the `session.completed` event on its expired context (`afterTerminalRelease`, `pkg/gateway/core/session/sessionserver/usage.go`). The primary insert fails on the expired context, the Redis stream publish fails on the same context, and `failover.Pipeline.Append` (`pkg/billing/billingstore/failover/failover.go`) then stages the event in the replica's in-memory buffer, which takes no context. A flusher inserts it into `billing_events` with the raw `user_id` later. The erasure gate's budget arm has already released the job by then, and `PurgeStagedByUser` purges only the erasure replica's buffer (F-12.8.53). The branch qualifies the §7.1 statement and the `TerminalPipelinePending` comment to name this exception; it does not change the behavior. Change.md "Scope" excludes failover-tier events, but this case needs no primary outage: every budget overrun produces it.

- (a) Skip the `session.completed` append when the pipeline's context has ended, and count the drop on a metric. §11.2.1 states that a billing event is never dropped, so this needs a §11.2.1 exception.
- (b) Have `failover.Pipeline.Append` return the caller's context error without staging when the caller's context has ended, so only a primary failure on a live context reaches the failover tiers. Every caller of `Append` on an ended context then loses the event, which also needs a §11.2.1 exception.
- (c) Keep the behavior, as the qualified §7.1 text states, and leave the exposure to F-12.8.53.

Recommendation: (a), scoped to the terminal pipeline, because it closes the case where the erasure has already moved on, and the event it drops belongs to a session whose terminal accounting has already run past its budget. A follow-up change carries the §11.2.1 exception.

### O3 A margin on the budget arm for clock and configuration skew

Raised by the review of 0176.S1, 2026-11-25. The erasure gate compares its own clock and its own flag-derived budget (`postTerminalCap`) with a `TerminatedAt` that another replica stamped, while that replica's pipeline deadline comes from its own clock and its own flags (`terminalContext`). Clock skew between the two replicas, a rollout that raises `--workspace-seal-max-duration-seconds` or `--stream-failure-report-timeout` on some replicas first, or an insert that commits just after the client's deadline lets the budget arm end the wait while an unmarked pipeline can still write. `InPostTerminalWindow` has the same exposure today.

- (a) Add an operator-tunable margin, applied by the erasure gate and the tenant-deletion terminator only, to the budget they read.
- (b) Record each session's pipeline deadline on the row at the terminal write, and have the gate read the per-row deadline plus a margin.
- (c) State the clock and configuration assumption in §7.1 and add no margin.

Recommendation: (a), with a default of 30 seconds, because it covers the skew and the in-flight commit without a schema change. A budget raised during a rollout stays the operator's responsibility, which the configuration page states.

### O4 Terminal writers that run the post-terminal hook alone append no billing event

Raised by the review of 0176.S1, 2026-11-25. The §7.1 edit lists the final quota checkpoint and the `session.completed` billing event among the steps of the terminal pipeline. The writers that call `AfterTerminalWrite` directly run no quota checkpoint, no billing event, and no session-log close: `afterFailed` (failSession, failFinalizing, failStarting), `cancelNodeGuarded` (cascade and the budget-keys-expired cleanup), the `mcptools` cancel tools, and the Open Responses `endHeldSession`. The session-log gap predates the step. The branch writes the pipeline mark at the end of `AfterTerminalWrite`, so the erasure wait ends when those writers' pipelines end.

- (a) State in §7.1 which writers run the session-log close, the quota checkpoint, and the billing event, and file an issue under SESS for the writers that should run them.
- (b) Route those writers through `recordSessionCompleted`, so every terminal write emits the billing event, in a change of its own.

Recommendation: (a) now, with (b) decided in the change the issue opens.

## Decisions

1. **The erasure job waits for the whole terminal pipeline (ER-1).** The gateway marks the session row when the last step of the session's terminal pipeline finishes, after `afterTerminalRelease` in `recordSessionCompleted`, and every terminal session, with or without a pod, is pending until the mark is set or the terminal pipeline budget has elapsed. User erasure (`store_deleting`) and tenant deletion, which uses the same predicate, both wait on it. Owner's answer to ER-1 on the M3 readiness decision page, 2026-11-25, option (a). The options not chosen were (b), moving the billing, quota-checkpoint, and session-log steps before the pod release and extending the wait to sessions without a pod, and (c), option (a) together with re-pseudonymizing when the verification finds a row and refusing billing writes for a user whose processing is restricted.
