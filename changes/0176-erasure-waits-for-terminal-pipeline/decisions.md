# Decisions: 0176 Erasure waits for the terminal pipeline

## Open decisions

### O5. The periodic `token_usage.checkpoint` event can carry the raw user id after an erasure (ERASE-I7)

The security review of 0176.S2 found that the billing checkpointer lists active sessions from a replica and appends `token_usage.checkpoint` with each session's `user_id`, with nothing tying it to the creation fence, the drain, or the terminal pipeline wait of §12.8. A pass that listed the user's session before the drain appends the raw id after `store_deleting`: before the verification it fails the erasure job, and after it the raw id stays in `billing_events` beside a verified receipt.

- **(a) No user id on the checkpoint event.** `token_usage.checkpoint` is written with an empty `user_id`, as decision 6 does for `session.completed`, and consumers join on `session_id` with `session.created`.
- **(b) Fence the lister.** The lister reads the primary and skips sessions whose user is `processing_restricted`. This narrows the window and leaves the staged failover append open.
- **(c) Refuse billing inserts for a restricted user.** Option (c) of decision 1, which the owner declined there.

Recommendation: (a). It closes every timing of the write, including a staged append flushed later, and uses the join decision 6 already set.

### O6. The periodic quota checkpoint can recreate an erased user's quota row (ERASE-I8)

The periodic quota checkpoint lists active subjects from a replica and writes each subject's row keyed by the raw `UserID` at the end of the pass. A pass that read the counter before the erasure deleted it, and whose write commits after the delete, leaves a `ScopeUser` row that nothing deletes; the erasure verification checks billing events only. The final checkpoint inside the terminal pipeline is covered by the pipeline wait of 0176.S1; the periodic pass is not.

- **(a) Fence the write.** The periodic write skips a subject whose user row is `processing_restricted`, read in the write transaction on the primary, the fence pattern of §12.8.
- **(b) Delete again before verifying.** The erasure job deletes the user's quota rows again just before verification and adds them to the verification.

Recommendation: (a). It applies the fence §12.8 already uses for other writers, and it keeps the row from being written rather than cleaning it up afterwards.

### O7. The pipeline wait margin has no upper bound (ERASE-I9)

The gateway refuses a negative margin and one whose sum with the budget overflows, and accepts any other value. A session whose writer runs no terminal pipeline, or whose pipeline a restart cut, holds each erasure for the whole budget plus the margin, so a margin entered in the wrong unit, such as `720h`, holds such erasures past the 72-hour erasure deadline with no startup error.

- **(a) A ceiling.** The gateway refuses to start with a margin above the terminal pipeline budget.
- **(b) A warning.** The gateway logs a warning at startup when the margin exceeds a threshold.
- **(c) Unbounded.** Keep the value unbounded and rely on the `ErasureJobOverdue` alert.

Recommendation: (a). The margin exists to cover clock skew and a commit just after the deadline, which a value above the budget does not describe, and refusing at startup is how the gateway already treats a negative margin.

## Decisions

1. **The erasure job waits for the whole terminal pipeline (ER-1).** The gateway marks the session row when the last step of the session's terminal pipeline finishes, after `afterTerminalRelease` in `recordSessionCompleted`, and every terminal session, with or without a pod, is pending until the mark is set or the terminal pipeline budget has elapsed. User erasure (`store_deleting`) and tenant deletion, which uses the same predicate, both wait on it. Owner's answer to ER-1 on the M3 readiness decision page, 2026-11-25, option (a). The options not chosen were (b), moving the billing, quota-checkpoint, and session-log steps before the pod release and extending the wait to sessions without a pod, and (c), option (a) together with re-pseudonymizing when the verification finds a row and refusing billing writes for a user whose processing is restricted.
2. **The erasure wait also keeps the post-terminal window (O1).** User erasure and tenant deletion wait while a drained session is pipeline-pending or in its post-terminal window, so the lost `CH-BINDRELEASE` case keeps the guarantee that `TestLostBindReleaseFanOutHoldsErasureUntilReleaseOrWindowCap_spec_28_5_7` pins; both arms end within one terminal pipeline budget. 0176.S1 built it. The option not chosen was (b), the pipeline mark alone. Owner's answer to EW-1 on the M3 readiness decision page, 2026-11-26, option (a).
3. **A margin on the budget arm (O3).** An operator-tunable margin, default 30 seconds, is added to the budget that the erasure gate and the tenant-deletion terminator read, and to nothing else, to cover clock skew between replicas and a commit that lands just after the deadline. A budget raised during a rollout stays the operator's responsibility, which the configuration page states. 0176.S2 builds it. The options not chosen were (b), a per-row pipeline deadline plus a margin, and (c), no margin with the clock assumption stated in §7.1. Owner's answer to EW-3 on the M3 readiness decision page, 2026-11-26, option (a).
4. **Writers that run the post-terminal hook alone (O4).** §7.1 "Terminal pipeline context:" names which terminal writers run the session-log close, the final quota checkpoint, and the `session.completed` billing event, and which run the terminal write and the pipeline mark alone; whether every writer should run the full pipeline is issue SEND-I1. 0176.S2 edits §7.1. The option not chosen was (b), routing every writer through the full pipeline in this change. Owner's answer to EW-4 on the M3 readiness decision page, 2026-11-26, option (a).
5. **Coverage waiver for 0176.S1.** The floor of 80% is waived for the 20 changed lines of `pkg/session/sessionstore/pgstore/pgstore.go`, which the unit tier cannot reach without Postgres and which the step's component, integration, and security tests exercise, and for the 3 changed lines of `cmd/lenny-gateway/sessionsrv.go` that pass two flag values through `terminalPipelineFlags`. The measured coverage was 70.9% (56 of 79 lines) on gate runs `cc7d4399f9e28c53f221834b` and `d0b76fb7f7a807c81cf6267a`. Issue GATE-I14 records that the coverage check measures the unit tier alone. Owner's answer to EW-W on the M3 readiness decision page, 2026-11-26, option (a).
6. **The late `session.completed` billing event carries no user (O2).** Option (e), proposed by the owner. The terminal pipeline writes the `session.completed` billing event with `user_id` empty, and the event keeps the tenant, the session id, the environment, the experiment and variant, and the labels. A consumer that needs the user joins on `session_id` with the session's `session.created` event, whose `user_id` user erasure pseudonymizes. A late append then carries nothing the erasure rewrites, because the pseudonymize path updates `billing_events.user_id` alone, so the event is neither dropped nor left with a raw identifier, and §11.2.1 needs no exception to "never dropped". §11.2.1's `user_id` row states that `session.completed` leaves it empty, and the billing documentation states the join. Owner's proposal in chat on 2026-11-26 ("if it doesn't carry much information and it's not used, could we not store the user id with it at all? Just the session id") and answer "EW-2b: ok". The options not chosen were (a), writing the event first and skipping it when the pipeline starts past its budget, (b), skipping it whenever the budget has run out, (c), an erasure check on every billing insert, and (d), keeping the behavior.
