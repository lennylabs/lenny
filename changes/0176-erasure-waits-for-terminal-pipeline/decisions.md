# Decisions: 0176 Erasure waits for the terminal pipeline

## Open decisions

### O1 The erasure wait also keeps the post-terminal window

Raised during the build of 0176.S1, 2026-11-25. EW-2 replaces the release gate (`admin.ReleasePending`, which reads `InPostTerminalWindow`) with `TerminalPipelinePending` alone. The two predicates differ when the replica that runs a session's terminal pipeline does not hold the session's binding. The chaos test `TestLostBindReleaseFanOutHoldsErasureUntilReleaseOrWindowCap_spec_28_5_7` (`tests/chaos/holder_forward_chaos_test.go`) builds that case: the erasure drain runs the pipeline on replica a, the `CH-BINDRELEASE` publish to replica b is lost, and b keeps the binding with no release recorded. The pipeline on a ends and records its pipeline mark, so `TerminalPipelinePending` reports false while the session is still in its post-terminal window on b. §7.1 **Post-terminal window:** item 3 states that a component which treats a terminal session as finished does not act on the pod, the binding, or the intent row of a checkpoint in progress while the window lasts, and `DeleteByUser` deletes checkpoint intent rows and objects.

- (a) The wait holds while a session is pipeline-pending or in its post-terminal window. Both arms end within one terminal pipeline budget, so the longest wait is unchanged, and the lost fan-out case keeps the guarantee the chaos test pins. The pipeline-pending predicate itself reads no pod field, as EW-2 states.
- (b) The wait holds only while a session is pipeline-pending, as EW-2 states. The chaos test changes to expect the erasure to proceed once the pipeline on replica a has recorded its mark, while replica b still holds the binding.

Recommendation: (a). The branch implements (a) in `admin.TerminalPipelinesPending` and in §12.8 **Lifecycle fence and drain.** pending the owner's answer; (b) is a one-line change to that function, the §12.8 sentence, and the chaos test.

## Decisions

1. **The erasure job waits for the whole terminal pipeline (ER-1).** The gateway marks the session row when the last step of the session's terminal pipeline finishes, after `afterTerminalRelease` in `recordSessionCompleted`, and every terminal session, with or without a pod, is pending until the mark is set or the terminal pipeline budget has elapsed. User erasure (`store_deleting`) and tenant deletion, which uses the same predicate, both wait on it. Owner's answer to ER-1 on the M3 readiness decision page, 2026-11-25, option (a). The options not chosen were (b), moving the billing, quota-checkpoint, and session-log steps before the pod release and extending the wait to sessions without a pod, and (c), option (a) together with re-pseudonymizing when the verification finds a row and refusing billing writes for a user whose processing is restricted.
