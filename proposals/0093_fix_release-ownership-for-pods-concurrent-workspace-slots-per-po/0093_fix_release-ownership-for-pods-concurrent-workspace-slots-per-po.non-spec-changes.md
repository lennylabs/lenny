# Non-spec changes: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write

## Design (implementation-facing)

The slot release becomes idempotent per session at the one primitive every slot reservation and release passes through (CODE-1, implementing SPEC-1a). Because a duplicate release is then a no-op, the design adds no arbitration between releasers. It routes every pre-running release through one slot-aware `Binder.ReclaimClaimed` (CODE-2), deletes the side releasers that delete a shared claim by name (CODE-3, CODE-4), orders the post-terminal hook against a superseded binding and against the failure funnel (CODE-5, CODE-6), and routes full_revoke through the existing releasers (CODE-7). Releases that no replica can perform are recorded as departures with a bound (**Edge cases and accepted failure modes** below).

The change adds no RPC, proto field, column, table, session state, background loop, or error code. It changes the value type stored under `lenny:pod:{pod_id}:active_slots` and the signatures of `slotcounter.Counter.Reserve`, `slotcounter.Counter.Release`, `slotcounter.SlotSource.GetActiveSlotsByPod`, `sessionstore.Store.GetActiveSlotsByPod`, and `podclaim.SlotClaimer.ReleaseSlot`. Every caller is updated in the same step.

**Resource-to-owner table after this proposal.** Each cell names the one component that releases the resource on that path. A second release from any other path is a no-op, apart from the Redis-restart departures in the spec-changes **Edge cases and accepted failure modes**: a keyed slot release that finds no member (CODE-1), a claim delete that finds no claim, or a lease release for a session that holds no lease.

| Path | Exclusive claim | Concurrent slot | Credential leases | Local binding |
|:--|:--|:--|:--|:--|
| Terminal write that applied from `created`, `finalizing`, or `ready` with no local binding (DELETE, terminate, `cancel_child`, the watchdog's pre-running timeouts, `failFinalizing`) | The post-terminal hook's `terminalReclaimPreRunning`, through `ReclaimClaimed` by name (CODE-2) | The same call, through the keyed slot release (CODE-1, CODE-2) | The same call, through `Credentials.ReleaseSession` | None exists |
| Terminal write that applied on the replica holding a current binding | The hook's executor release (`Binder.Release`), ordered after the failure funnel (CODE-6) | The hook's executor release (`Binder.ReleaseSlot`), ordered after the failure funnel (CODE-6) | The binding release | The same release |
| Terminal write by a replica whose local binding is stale | The adopter's D6 release | The adopter's D6 release | The adopter's D6 release | Evicted by the hook's stale-binding step without a release, or released failed for a resume-family `fromState` (CODE-5) |
| full_revoke | The pre-running reclaim or the D6 release body, after the deny-list step (CODE-7) | As for the exclusive claim | `RevokeUserLeases`, before the first cancel write (CODE-7) | `ReleaseRevokedBinding` (CODE-7) |
| `/start` that fails after its slot reservation | Not applicable | `BindReservedSlot`'s keyed release; a later pre-running reclaim of the same session removes nothing | Not applicable | None remains |
| Process exit between the terminal commit and the release | Departure: claim-orphan GC | Departure: stays counted | Departure: lease expiry | Lost with the process |

## Staged code changes

### CODE-1 · Slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry

Implements SPEC-1a and SPEC-1b. The rules the scripts implement are stated in SPEC-1a; the code comments cite `// spec: §5.2 (Slot assignment atomicity)`.

Targets:

- `pkg/gateway/storage/slotcounter/slotcounter.go`: `reserveScript`, `seedScript`, `releaseScript`, `Counter.Reserve`, `Counter.rehydrate`, `Counter.seed`, `Counter.Release`, `SlotSource`, and the package doc comment.
- `pkg/gateway/session/sessionstore/sessionstore.go` `Store.GetActiveSlotsByPod`; `pkg/gateway/session/sessionstore/pgstore/pgstore.go` and `pkg/gateway/session/sessionstore/memstore/memstore.go` `GetActiveSlotsByPod`.
- `cmd/lenny-controller/main.go` `sessionActiveLookup.PodHasActiveSession`.
- `pkg/gateway/podlifecycle/podclaim/slotclaimer.go` `reserveSlot` and `ReleaseSlot`.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` `ReleaseSlotReservation`, `Binder.ReleaseSlot`, and the `SlotClaimer` constructions in that file.
- Doc comments that describe the clamp-at-zero counter: `slotcounter.go` (`Release`, `SlotSource`, the package comment), `slotclaimer.go` (`ReleaseSlot`), `sessionstore.go` (`GetActiveSlotsByPod`), and the `active_slots` comments in `slotbinder.go` and `pkg/gateway/metrics/gatewaymetrics/`.
- The test-infrastructure stale-key cleanup in item 7.

Changes:

1. **Scripts.** `KEYS[1]` is the active-slots key and `KEYS[2]` the rehydrated flag.
   - `reserveScript`, with `ARGV[1]` the session identifier and `ARGV[2]` `maxConcurrent`:
     ```
     if redis.call('EXISTS', KEYS[2]) == 0 then return -2 end
     if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then return redis.call('SCARD', KEYS[1]) end
     local n = redis.call('SCARD', KEYS[1])
     if n >= tonumber(ARGV[2]) then return -1 end
     redis.call('SADD', KEYS[1], ARGV[1])
     return n + 1
     ```
   - `seedScript`, with `ARGV` the session identifiers (possibly none):
     ```
     redis.call('DEL', KEYS[1])
     for i = 1, #ARGV do redis.call('SADD', KEYS[1], ARGV[i]) end
     redis.call('SET', KEYS[2], '1')
     return 1
     ```
   - `releaseScript`, with `ARGV[1]` the session identifier and `ARGV[2]` `'1'` for a leaked release and `'0'` otherwise:
     ```
     if redis.call('EXISTS', KEYS[2]) == 0 then return {-2, 0} end
     local r = redis.call('SREM', KEYS[1], ARGV[1])
     if ARGV[2] == '1' then redis.call('SADD', KEYS[1], 'leaked:' .. ARGV[1]) end
     return {r, redis.call('SCARD', KEYS[1])}
     ```
     The leaked arm adds its member whether or not `SREM` removed one. That is the arm that keeps a leak counted after a clean release of the same session.
2. **Signatures.**
   - `func (c *Counter) Reserve(ctx context.Context, podID, sessionID string, maxConcurrent int32) (count int32, rehydrated bool, err error)`.
   - `func (c *Counter) Release(ctx context.Context, podID, sessionID string, leaked bool) (removed bool, remaining int32, rehydrated bool, err error)`. On the `-2` result, `Release` calls `Counter.rehydrate` with `sessionID` as an extra seed member and retries the script, under `Reserve`'s attempt bound and `ErrRehydrationStalled`. `Counter.rehydrate` and `Counter.seed` take that optional extra member, `Reserve` passes none, and the source-nil path seeds only the extra member.
   - Both return an error and run no script when `sessionID` is empty. A keyed release with no key would remove nothing and report a false zero to a caller that ignores `removed`.
   - `SlotSource.GetActiveSlotsByPod(ctx, podID) ([]string, error)` returns the identifiers of the non-terminal sessions whose `pod_assignment` names the pod. `pgstore` selects `id` with the existing predicate and index; `memstore` returns the matching IDs. `Counter.seed` passes them to `seedScript`. `sessionActiveLookup.PodHasActiveSession` tests `len(ids) > 0`.
   - `Counter.Reset` is unchanged, because `DEL` is type-agnostic. The §12.4 Postgres fallback (`fallback.go`) is unchanged; a release during a Redis outage fails and takes item 5.
3. **`SlotClaimer.ReleaseSlot`** becomes `func (c *SlotClaimer) ReleaseSlot(ctx context.Context, sandboxName, sessionID string, recycle, leaked bool) (recycled bool, err error)`. It calls `Counter.Release(ctx, sandboxName, sessionID, leaked)` before any disposition, and records a reported `rehydrated` through `recordRehydration`. **IMPLEMENTOR'S CHOICE:** whether the `pool` label comes from the caller or from the Sandbox's `warmpool.LabelPool` label — the value equals the pool label `reserveSlot` records for the same pod. The early return that skips the counter on a leaked release is deleted, because the script now records the leak. After the script it returns `(false, nil)` when `leaked` is true, when `removed` is false, or when `remaining > 0`. Only a clean release that removed the member and left the set empty reaches the existing recycle patch or `DeleteClaim`. A counter failure is returned wrapped with a new sentinel, `var ErrSlotCounterRelease = errors.New("podclaim: slot counter release failed")`, as `fmt.Errorf("%w: sandbox %s session %s: %w", ErrSlotCounterRelease, sandboxName, sessionID, err)`. A disposition failure is returned without the sentinel. `ReleaseSlot` stays a single attempt.
4. **`reserveSlot` rollbacks (F-7.3.34(a)).** The rollback after a failed `CreateClaim` is `c.Counter.Release(rctx, sb.Name, req.SessionID, false)`. The rollbacks after `writeBoundStatus`, `StampPodTenant`, or `StampMaxPodUptime` fails, on a fresh or an existing claim, are each `c.ReleaseSlot(rctx, sb.Name, req.SessionID, false, false)`, which replaces both the counter release and the fresh-claim `c.Client.Delete`, for the reason Decision 11 gives (SPEC-2c). `rctx` is `context.WithTimeout(context.WithoutCancel(ctx), c.RollbackTimeout)`. `RollbackTimeout` is a new `time.Duration` field on `SlotClaimer`. Every `SlotClaimer` construction in `podsession` sets it to `slotCleanupFloor`, the §5.2 per-slot cleanup floor; a zero value uses 5 seconds. `Counter.Reserve` passes `req.SessionID`.
5. **Bounded detached retry (F-5.2.49).** One helper in `podsession`, `releaseSlotRetrying(ctx context.Context, claimer *podclaim.SlotClaimer, sandboxName, sessionID string, recycle, leaked bool) (bool, error)`, runs `claimer.ReleaseSlot` on `detachedReleaseContext(ctx)` and repeats it while the error satisfies `errors.Is(err, podclaim.ErrSlotCounterRelease)` and that context has not ended. `ReleaseSlotReservation` (passing `slotID`, which equals the session identifier) and `Binder.ReleaseSlot` (passing `result.SessionID`) both call it. `Binder.ReleaseSlot` keeps the caller's context for the adapter `Shutdown` and uses the helper only for the claimer call. A disposition error is returned at once, because the member is already removed and a repeat would find no member and skip the disposition.
   **IMPLEMENTOR'S CHOICE:** the pause between attempts. The total stays within `detachedReleaseContext`'s bound, at least two attempts run when the first fails fast, and the pause is injectable so a tier-1 test runs without wall-clock sleeps.
6. **Callers.** `tests/tier7a_load_local/scenarios/slot_leaked_counted_race/scenario.go` and every other direct caller of the changed signatures pass the session identifier.
7. **Stale string keys on standing test clusters.** Add a step to the test-infrastructure bring-up that deletes every `lenny:pod:*:active_slots` key whose Redis `TYPE` is `string`, logs each deletion, and never fails the run. **IMPLEMENTOR'S CHOICE:** where the step lives (`cmd/lenny-test` infra bring-up or a helper under `tests/testinfra`). The step runs before any tier-2-or-higher test against a standing Redis, and no gateway or controller code carries it.

### CODE-2 · Slot-aware pre-running reclaim in `Binder.ReclaimClaimed`

Implements SPEC-2c and SPEC-2e. Depends on CODE-1, without which the slot branch double-decrements on the paths the summary's **Watch out for.** names.

Targets: `pkg/gateway/podlifecycle/podsession/reclaim.go` `ReclaimClaimed`; `pkg/gateway/sessionserver/usage.go` `terminalReclaimPreRunning`; `pkg/gateway/sessionserver/bind_rollback.go` `rollbackClaim`; the `ReclaimClaimed` calls in `pkg/gateway/podlifecycle/podsession/bindprepare.go` and `bindlaunch.go`; `pkg/gateway/sessionserver/isolationlevel.go` `scrubPolicyForPool`.

Changes:

1. Add `const ScrubPolicyPerSlot = "best-effort-per-slot"` in `sessionserver` and use it in `scrubPolicyForPool`.
2. `func (b *Binder) ReclaimClaimed(ctx context.Context, sandboxName, sessionID string, perSlot bool) error`. It releases the session's credentials first, as today. When `perSlot` is true it calls `b.ReleaseSlotReservation(ctx, sandboxName, sessionID, false)` and deletes no claim; the occupancy-zero disposition inside `SlotClaimer.ReleaseSlot` deletes the claim when the release empties the set, because `ReleaseSlotReservation` passes `recycle=false`. When `perSlot` is false it deletes the claim by name on a detached context, as today. Rewrite the doc comment: drop the created-expiry sweeper (CODE-4 deletes it), and cite `// spec: §5.2 (Slot assignment atomicity; Recycle lifecycle), §6.2, §4.6.3`.
3. `terminalReclaimPreRunning` passes `sess.ScrubPolicy == ScrubPolicyPerSlot`. The persisted `ScrubPolicy` is the discriminator; the reclaim does not resolve the pool.
4. `rollbackClaim` becomes one call, `s.podBinder.ReclaimClaimed(ctx, claim.SandboxName, sessionID, claim.SlotID != "")`. This collapse is a reuse refactor; its behavior on both branches is unchanged apart from a lease release that is a no-op at create.
5. `bindprepare.go` and `bindlaunch.go` pass `false`, because `Prepare` and `Launch` run only on exclusive pools.
6. Update the comment on `terminalReclaimPreRunning` that names the created-expiry sweeper.

### CODE-3 · Finalize: the pod and slot release runs from the applied failure write's hook

Targets: `pkg/gateway/sessionserver/finalize.go` (`prepareAtFinalize`, `finalizePlanParseFailed`, `finalizePrepareFailed`, `finalizeReadyWriteFailed`, `reclaimFinalizedPod`); `pkg/gateway/sessionserver/session_terminal.go` `failFinalizing`; their callers in `pkg/gateway/sessionserver/finalize_handler.go`.

Changes:

1. Remove the `podReclaimed` parameter from `failFinalizing`. The hook then always runs with the locked row's `fromState` (`finalizing`), so after CODE-2 the hook's reclaim releases a concurrent slot (N1). Rewrite the doc comment accordingly.
2. Delete the three pre-Prepare `reclaimFinalizedPod` calls in `prepareAtFinalize`: after `ResolvePool` fails, after `resolveCredentialPools` fails, and after `checkCredentialDeliveryIsolation` refuses. The applied failure write's hook covers them on both pool kinds. Rewrite the comments that say the outer handler does not reclaim on a pre-Prepare error.
3. Delete the store-error reclaim in `finalizeReadyWriteFailed`, because the ready write may have committed. Remove its now-unused `prepared` parameter and rewrite its doc comment bullets: a store error on the failure write releases nothing at the handler, and the finalizing watchdog's terminal write and hook release the pod if the row is still `finalizing`.
4. Keep the store-error reclaim in `finalizePlanParseFailed`, routed through CODE-2's slot-aware call. `reclaimFinalizedPod` takes the row's per-slot flag (`func (s *Server) reclaimFinalizedPod(ctx context.Context, sandboxName, sessionID string, perSlot bool)`), and `finalizePlanParseFailed` receives the row's `ScrubPolicy` from its caller beside `podAssignment`. No ready write runs on that exit, and a later release by the finalizing watchdog's hook is a no-op after CODE-1. `reclaimFinalizedPod`'s doc comment names this as its only caller.
5. Keep `revokeFinalizeLease` and all four of its call sites unchanged, including the unconditional revoke in `finalizePrepareFailed`. That revoke is the only lease return when `failFinalizing` commits and still reports a store error.
6. Code comments on the changed exits cite `// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states), §6.1 (Per-session credential lease lifecycle)`.

### CODE-4 · Delete the created sweeper

Targets:

- `pkg/gateway/session/createdsweeper/` (the package and its tests).
- `cmd/lenny-gateway/workers.go` (the sweeper import and its construction and start).
- `cmd/lenny-gateway/worker_seams.go` `createdSweeperReclaim`.
- `cmd/lenny-gateway/flags.go`: the `--max-created-state-timeout-seconds` help text drops "and the createdsweeper's abandoned-row timeout" and "the three deadlines" becomes "the two deadlines".
- The comments that name the sweeper in `cmd/lenny-gateway/controlserver.go`, `pkg/gateway/sessionserver/create.go`, `pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/usage.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`, and `pkg/gateway/sessionserver/terminal_reclaim_internal_test.go`.
- `pkg/gateway/ARCHITECTURE.md`: the `createdsweeper` package inventory entry.
- `pkg/gateway/podlifecycle/podsession/binder_phases_test.go` `TestCreatedSweepReleasesClaimedPod`: retarget to the watchdog's CREATED_TIMEOUT write driving the hook's reclaim.
- `tests/registers/change-graph-coverage.yaml`: remove the sweeper entry.

Changes: delete the package and every wiring site above. The `MetricsSink` interface leaves with the package; production wires no metrics sink for it. Keep `sessionstore.Store.Delete` and its `pgstore` and `memstore` implementations, because `OpenResponsesHandler.handleDelete` still calls it. An abandoned `created` row now ends as the watchdog's guarded `created → failed` write with `CREATED_TIMEOUT` and the post-terminal hook, on the same `maxCreatedStateTimeoutSeconds`. §15.1 names `expired` for this expiry and §7.2 lists no timeout edge out of `created`; that existing mismatch is filed for P3 by REC-1 and is not changed here.

### CODE-5 · The post-terminal hook settles a stale local binding before the seal and the release

Targets: `pkg/gateway/sessionserver/terminal_hook.go` `AfterTerminalWrite`; `pkg/gateway/sessionserver/terminal_binding.go` `releaseTerminalBinding`; `pkg/gateway/sessionserver/stream_failure.go` `reportSupersededByPeer` and `evictStaleBinding` (reused); `pkg/gateway/sessionserver/resume_rebind.go` `releaseEarlierBinding` (reused).

Changes:

1. Extract `func bindingStale(bindGen, rowGen int64) bool { return bindGen == 0 || rowGen > bindGen }` and use it in `releaseTerminalBinding`, in `reportSupersededByPeer`, and in the new step below, so one predicate replaces three copies.
2. Add `func (s *Server) settleStaleBinding(fromState session.State, sess sessionstore.Session)`, called in `AfterTerminalWrite` right after `CancelResumeAttempt` and before the seal, so a same-replica resume attempt has stopped before the step reads the registry. It returns at once when `s.podRegistry` is nil, when the registry holds no binding for `sess.ID`, or when `bindingStale(bind.CoordinationGeneration, sess.CoordinationGeneration)` is false. Otherwise:
   - when `fromState` is `running`, `input_required`, or `suspended` (the states in which a peer re-adopts the same pod, as `coordination.PodHeld` defines them), it calls `s.evictStaleBinding(sess.ID, bind)` and makes no binder call;
   - when `fromState` is `resume_pending`, `resuming`, or `awaiting_client_action`, the stale binding is the session's earlier pod, which no adopter holds. It calls `s.releaseEarlierBinding(s.lifetime(), sess)`, which releases with `executor.DispositionFailed` under the slot-accounting lock;
   - for every other `fromState` it does nothing.
   In both acting branches the hook's later seal finds no binding and returns nil, and its later release misses. The step cites `// spec: §10.1.1 (Generation counters:), §10.1.5 (Stale Replica Behavior), §7.3 (Resume driver)`.
3. Rewrite `AfterTerminalWrite`'s step list in its doc comment to add the step between step 1 and step 2.

### CODE-6 · The post-terminal hook's release waits for the failure funnel

Target: `pkg/gateway/sessionserver/terminal_hook.go` `AfterTerminalWrite`, step 5 (the `terminalReclaimPreRunning` call and the executor release). `pkg/gateway/sessionserver/failure.go` is not changed.

Change: wrap step 5 in `unlock, err := s.slotAccountLocks.Lock(s.lifetime(), sess.ID)` and `defer unlock()` scoped to that step. The lock waits on the lifetime context, so a cancelled writer request cannot skip the release. When the lock wait returns an error (server shutdown), the release step is skipped and logged. The seal stays outside the lock, because a seal can run for up to `maxWorkspaceSealDurationSeconds` and would block the funnel. The funnel takes the same lock before its edge write and holds it through `accountFailedSlot` and its failed-disposition release, so in the racing order the hook's release finds no binding. `transitionToFailed` unlocks before it calls the hook, and no other holder of the lock calls the hook, so the change adds no lock cycle. The step cites `// spec: §5.2 (whole-pod replacement trigger), §7.3 (Resume flow after pod failure), §7.2 (Interactive Session Model)`.

### CODE-7 · full_revoke releases through the pre-running reclaim and the D6 release body; leases return at the deny-list step

Implements SPEC-3.

Targets: `pkg/gateway/externalapi/admin/users.go` (`RevokeHook`, `terminateUserSessions`, the handler that calls it); `pkg/gateway/externalapi/admin/users_invalidate.go` `runFullRevokeFanOut` and its comments; `pkg/gateway/sessionserver/terminal_hook.go` (new `ReclaimRevokedPreRunning`); `pkg/gateway/sessionserver/terminal_binding.go` (new `ReleaseRevokedBinding`, and `releaseTerminalBinding` gains a context parameter and a result); `cmd/lenny-gateway/user_revocation.go` (`podTerminateFanOut`, `terminateLocal`, `userRevokeReason`, `userTerminateDeadline`, `userLeaseStore`, `RevokeUserLeases`); `cmd/lenny-gateway/adminrouter.go` and `cmd/lenny-gateway/billing_security_workers.go` (wiring and comments); `pkg/gateway/podlifecycle/podterminate/propagator/propagator.go` (`Request.Reason` and the package comment).

Changes:

1. **Order.** `terminateUserSessions` calls `userLeases.RevokeUserLeases` with the IDs of the non-terminal rows its `List` returns, before its first cancel write, and `leasesRevoked` in the response is the count that call returns. The call must precede the first cancel write, because once a row is terminal the coordination Sweeper and the resume tail can release it, a release returns its leases and removes their records, and a lease the enumeration no longer finds receives no deny-list entry. After the cancel writes, `runFullRevokeFanOut` runs, in this order:
   1. For each cancelled session whose pre-revoke state is `created`, `finalizing`, or `ready`, `RevokeHook.ReclaimRevokedPreRunning`.
   2. `userPods.TerminateUserSessions`, which releases the bindings this replica holds and publishes the request to peers.
   3. The token, elicitation, and playground steps, unchanged.
2. **`terminateUserSessions`** returns `[]RevokedSession` and the deny-listed lease count (item 1), where `type RevokedSession struct { Row sessionstore.Session; From session.State }` holds the updated row and the pre-revoke state read on the locked row. Its doc comment drops "sends Shutdown". `AfterRevokeWrite` is unchanged and keeps only `CancelResumeAttempt` and `drainMessagingOnTerminal`.
3. **`RevokeHook`** gains `ReclaimRevokedPreRunning(ctx context.Context, from session.State, row sessionstore.Session)`. The session server implements it as `s.terminalReclaimPreRunning(ctx, from, row)`, which already skips a session bound on this replica.
4. **`ReleaseRevokedBinding`.** Add `func (s *Server) ReleaseRevokedBinding(ctx context.Context, tenantID, sessionID string) (released bool, err error)`. It reads the row with `sessionstore.ReadPrimary`, returns `(false, nil)` unless the row is terminal, returns `(false, nil)` when `s.terminalClaims.startRelease(sessionID)` refuses, and otherwise runs `releaseTerminalBinding(ctx, row)` with `endRelease` deferred. A store read error returns `(false, err)`. `releaseTerminalBinding` takes the context and returns whether it released a binding. `ReleaseTerminalBinding` passes `s.lifetime()` and ignores the result. The stale-binding eviction inside `releaseTerminalBinding` applies unchanged.
5. **`podTerminateFanOut.terminateLocal`** calls `ReleaseRevokedBinding(callCtx, req.TenantID, bind.SessionID)` for each matching registry entry in place of `bind.Adapter.Shutdown`, where `callCtx` is `context.WithTimeout(ctx, userTerminateRPCTimeout)` as today. The lock wait, the adapter `Shutdown`, and the release run on that context; the binder's claim, slot, and lease effects keep their own detached contexts. A released binding counts toward `PodsTerminated`; an error adds the session to `FailedSessions`; `(false, nil)` counts neither. The `bind.Adapter == nil` skip is removed, because `ReleaseRevokedBinding` decides from the row and the registry entry. `podTerminateFanOut` takes the releaser as an interface field, `revokedBindingReleaser`, wired in `adminrouter.go`.
6. Delete `userRevokeReason` and `userTerminateDeadline`, and remove `Request.Reason` from `podterminateprop.Request`. The adapter does not read `ShutdownRequest.reason`. The release path's `Shutdown` carries the release path's own deadline.
7. **`RevokeUserLeases`** adds each lease's credential to the deny list and then calls `Release(lease.LeaseID)` on the credential assigner, which removes the lease record and returns its pool slot. `userLeaseStore` drops `Remove`. `userLeaseRevoker` takes the assigner through a consumer-side interface with `Release(leaseID string) error`, wired from `w.credAssign` in `adminrouter.go`. A `Release` error is logged with the lease and session identifiers, and the lease still counts.
8. Update the `§11.4 step` citations in the touched files to the SPEC-3 step numbers: the deny-list write and the lease return are step 1, the release is step 2, and the pre-running reclaim is step 4.

## Staged schema, chart, and migration changes

None. The controller chart fix that registers the claim-orphan GC is a precondition owned outside this proposal (REC-1 item 3).

## Staged docs changes

`docs/reference/state-machines.md` describes leaked slots as counted in the pod's Redis slot-counter occupancy, which stays true under SPEC-1.

### REC-1 · BUILD-GAPS records

Targets: `BUILD-GAPS.md` and `gateway-runtime-comms-remediation.md`. No `tests/claim-map.json` row is added: that register holds §28 statements and remediation-plan R-steps, and the departures below belong to §4.6.1, §4.9, and §5.2. The departures themselves are recorded in this file's and the spec-changes file's **Edge cases and accepted failure modes** sections; the deviations file stays implementor-owned.

1. **Close**, each with the deliverable that resolves it: F-4.6.26 (CODE-2); F-4.6.27 with N1 (CODE-2, CODE-3); F-4.6.28 (CODE-4); F-5.2.49 (CODE-1); F-7.3.33 (CODE-6, reason: the hook's release is ordered after the funnel by `slotAccountLocks`); F-7.3.34 (CODE-1 for part (a); part (b) closes because the rebuild-branch drain retires the exclusive pod, so no adapter entry survives for a reclaim to remove); F-10.1.28 (CODE-5); F-11.4.11 (CODE-7); F-11.4.15 (CODE-2, CODE-7); F-11.4.16 as a duplicate of F-11.4.15.
2. **Close F-4.9.35** as resolved by 0092's D7, with this invariant: `finalizing` exits only to the finalize handler's own ready write or to a terminal state, so every finalize-exit lease revoke runs on a terminal row or before the ready write. The tier-1 test in **Testing** pins the invariant.
3. **Annotate and keep OPEN** F-4.6.29 and the F-10.1.21 residue (the dead-connection eviction, coordinator loss, a failed readopt, and `ReleaseSuspendedPod` stopping between clearing `pod_assignment` and the release) as accepted departures with the bounds in **Edge cases and accepted failure modes**. They stay open until the controller `--agent-namespaces` and `--postgres-dsn` chart fix lands. File that chart fix as its own finding, or fold it explicitly into F-4.6.30, so the precondition has an owner.
4. **Annotate F-11.4.10**: the slot-return half is closed by CODE-7; the finding stays OPEN for the deny-list granularity and session-set halves, which P5 owns.
5. **File** N2 (`Binder.Resume` leaves the claim `claimPod` created when `reserveResumeSlot` fails) as an accepted departure bounded by the claim-orphan GC, open until the chart fix lands.
6. **File** the defects this proposal confirms and does not stage, as the summary's **Defects in the shipped tree that this proposal does not stage** lists them, each with its owner.
7. **Remediation plan.** Tick the P1 entry in `gateway-runtime-comms-remediation.md` §10.2, naming proposal 0093, and record each member the problem statement moved out with its destination.
8. **Triage rule for failures the tests find.** Fix in this proposal a failure that is relevant to release ownership, or that is straightforward and owned elsewhere by nobody. File every other failure in `BUILD-GAPS.md`, record it in the deviations file, and skip the affected case with a `blocked:` reason in `tests/registers/skip-reasons.yaml`.

## Testing

Every test carries `// spec:`, and every tier-2-or-higher test carries `// diagnosis:`. Tests that a code deliverable breaks or needs land in that deliverable's step; TEST-1 holds the cross-tier tests and lands after the code.

### Tests that land with CODE-1

- **Tier 1, `slotcounter` (miniredis).** A table over {reserve, clean release, leaked release} sequences for one and two sessions, asserting the set members and the returned `removed` and `remaining` against SPEC-1a. Required rows: a duplicate clean release removes nothing; a clean release, then a leaked release of the same session, leaves one `leaked:` member counted; a second leaked release changes nothing; a repeated reservation of a present member succeeds without adding, including at the cap; a release with the `rehydrated` flag absent seeds the source's identifiers plus its own member, removes its member, and reports `removed`, `rehydrated`, and the remaining cardinality; a reservation at the cap returns exhausted; an empty session identifier is an error with no script run. `TestReleaseDecrementsAndClampsAtZero` becomes the keyed no-op test; `TestResetClearsCounter` and the rehydration tests seed from identifiers. A concurrent reservation race under `-race` never exceeds the cap.
- **Tier 1, `podclaim`.** `ReleaseSlot` disposes only when `removed` is true, `remaining` is zero, and `leaked` is false; a release that removed nothing on an empty set leaves the claim (a duplicate release); a counter failure carries `ErrSlotCounterRelease`. `reserveSlot`'s rollbacks run after the attempt context is cancelled and remove the member; the claim is deleted only when the set is then empty, and a sibling that reserved onto the fresh claim before the `writeBoundStatus` failure keeps it (F-7.3.34(a), SPEC-2c).
- **Tier 1, `podsession`.** `releaseSlotRetrying` retries a counter failure until success, stops at the bound, and does not retry a disposition failure; the discriminating assertion is that a disposition error after a removal is not followed by a second `ReleaseSlot` call.
- **Tier 4, `pgstore`.** `GetActiveSlotsByPod` returns exactly the non-terminal session identifiers naming the pod, across tenants, and none for an empty pod name.
- **Retargets.** `tests/tier4_integration/slot_counter_redis_outage_fallback_test.go` keeps its assertions under the set type. Every test that reads or writes `lenny:pod:*:active_slots` as a string moves to set reads (`SCARD`, `SMEMBERS`) or fills the set to force exhaustion: at least `tests/tier2_component/slotrelease/revoke_double_teardown_test.go`, `tests/tier4_integration/recycle_scrub_path_test.go`, `tests/tier7a_load_local/held_attach_stream_race_test.go`, `pkg/gateway/session/executor/podstream_internal_test.go`, and the `podsession`, `podclaim`, and `sessionserver` tests that `grep -rln active_slots --include=*_test.go pkg tests` lists. `tests/tier7a_load_local/scenarios/slot_leaked_counted_race` passes the session identifier.

### Tests that land with CODE-2

- **Tier 1, pre-running writer table (Part A).** Rows: DELETE, terminate, `cancel_child`, the watchdog's `created`, `finalizing`, and `ready` timeouts, `failFinalizing`, and full_revoke. Columns: {exclusive pool, concurrent pool with the session alone, concurrent pool with a live sibling}. On the exclusive pool assert one by-name claim delete and one `ReleaseSession`. On a concurrent pool assert exactly one member removed and one `ReleaseSession`; with a sibling, assert the claim is intact and no drain is requested; alone, assert the claim is deleted (SPEC-2e).
- **Tier 1, double-release paths.** A failed `/start` that releases through `BindReservedSlot`, followed by DELETE, removes the member once. A hook reclaim followed by a peer's D6 release removes the member once. A hook's clean release followed by D6's leaked release leaves `leaked:<id>` present and the claim intact while a sibling lives.
- **Tier 1, `rollbackClaim`.** Both branches behave as before the collapse.

### Tests that land with CODE-3

- **Tier 1, finalize exits (Part B).** Rows: the `failFinalizing` exits (plan parse, prepare failure, ready-write failure). Columns: {applied, lost to a terminal state, lost to a non-terminal state, store error}. Assert one release per resource after the write applies, and no release at the handler for a store error on the ready-write exit; after one finalizing-watchdog pass, assert exactly one release. Add the case where `failFinalizing` commits but returns a store error inside `finalizePrepareFailed`: the lease is still revoked.
- **Tier 1, invariant for F-4.9.35.** A test that parses the `sessionserver` package and asserts that `transitionReady` has a single caller, and that every `revokeFinalizeLease` call site is reached only on a terminal row or before the ready write.

### Tests that land with CODE-4

- **Tier 1.** `TestCreatedSweepReleasesClaimedPod` retargets to the watchdog's CREATED_TIMEOUT write: the hook reclaims the claim (exclusive) or the member (concurrent), and the row stays as a `failed` row.
- **Tier 0.** The build, `go vet`, and the change-graph register pass with the package removed.

### Tests that land with CODE-5

- **Tier 1.** A table over the local binding {stamped at the row's generation, below it, 0, absent} and `fromState` {`running`, `input_required`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`}. Assert no Checkpoint RPC and no binder call through an evicted binding, and a failed-disposition release of the earlier pod on the resume-family rows. Every call site of `bindingStale` agrees on every input.

### Tests that land with CODE-6

- **Tier 1.** A table over the funnel edge {`resume_pending`, `awaiting_client_action`}, the terminal writer opening its claim before or after the funnel's check, and the pool kind {exclusive recycling, concurrent}. Assert that the release uses the failed disposition, that the drain is stamped before the slot release, and that the hook's release is a miss. A terminal write that loses after the funnel commits (for example `expireIdle`) still leaves the binding released by the funnel.

### Tests that land with CODE-7

- **Tier 1, `admin`.** `terminateUserSessions` calls `RevokeUserLeases` with every listed non-terminal session before its first cancel write (an ordered recorder over the session-store and lease-revoker fakes). The deny list receives each revoked session's credential key whether the session was pre-running, bound locally, or bound on a peer.
- **Tier 1, `cmd/lenny-gateway`.** `TestUserLeaseRevokerRevokesAndDenies` asserts, for each lease, a deny-list write and then a `Release` of its lease ID, and that a `Release` error still counts the lease. The `TestPodTerminateFanOut*` tests assert `ReleaseRevokedBinding` calls in place of a raw `Shutdown`, and no `USER_REVOKED` anywhere. `ReleaseRevokedBinding` returns `(false, nil)` for a non-terminal row and when `startRelease` refuses.
- **Tier 1, `propagator`.** `propagator_test.go` drops `Reason`; `users_invalidate_fanout_test.go` drops its `USER_REVOKED` expectations.
- **Tier 2, retarget.** `tests/tier2_component/slotrelease/revoke_double_teardown_test.go` asserts that full_revoke releases through D6's body and removes the member exactly once, with no `USER_REVOKED` `Shutdown`.
- **Tier 3.** The pub/sub `Request` round-trips through JSON without a `reason` field, and a peer applying it releases through `TerminateLocal`.

### TEST-1 · Cross-tier tests

- **Harness extension.** `tests/testinfra/lifecycleharness` (`harness.go`, `cluster.go`, `pools.go`, `checker.go`) gains a concurrent pool, a `SlotClaimer`, and a slot set on miniredis or the compose Redis. The checker asserts, after every scenario, that each concurrent pod's set holds exactly the non-terminal sessions naming the pod plus its `leaked:` members, that no pod with an empty set and no non-terminal row keeps a claim, and that each member was removed at most once. A scenario that injects a crash between commit and release opts into the existing `RelaxOrphanClaims` relaxation and a matching slot relaxation.
- **Tier 2 (envtest with Redis).** `SlotClaimer.ReleaseSlot` against a real kube-apiserver: the claim is deleted only by the removing release that empties the set; a recycling pool patches `recycling`; a pre-running release on a recycling pool deletes the claim. `// spec: §5.2 (Slot assignment atomicity; Recycle lifecycle), §4.6.3`.
- **Tier 4 (compose, multi-replica).**
  - On a concurrent pool, a DELETE of a session that never started, with a live sibling, leaves the sibling running, the claim bound, and one member.
  - An abandoned `created` row ends `failed` with CREATED_TIMEOUT and its claim or member released.
  - A superseded coordinator's terminal hook evicts without releasing, and the adopter's Sweeper releases exactly once.
  - A DELETE lands between the `resuming` write and `releaseEarlierBinding`: the earlier pod is released and does not leak to the GC.
  - full_revoke across two replicas: every cancelled session's pod, member, and lease is released once, and the response count matches the deny-listed leases.
- **Tier 5 (Kind), `tests/tier5_e2e_kind`.** On a concurrent pool, terminate a not-started session while a sibling runs: the pod stays `claimed`, does not drain, and the sibling completes. `// spec: §6.2, §4.6.1, §5.2`.
- **Tier 7a (`-race`).** Randomized interleavings of the hook's reclaim, a peer's D6 release, and a `BindReservedSlot` failure release on one slot, with clean and leaked outcomes, checked by the harness invariant; randomized interleavings of the failure funnel with the hook's release step; concurrent reservations and releases on one pod never exceed the cap.
- **Tier 8 (chaos).**
  - Crash between the terminal commit and the hook: assert the departure through `RelaxOrphanClaims` (the slot stays counted, and the claim is left for the GC), with no skip.
  - A case asserting that the chart-installed `ClaimGarbageCollector` reclaims that claim extends `tests/tier8_chaos/orphan_gc_recovery_test.go` and carries a `blocked:` skip until the chart fix lands.
  - A Redis restart between a reservation and the pod's last release: that release rehydrates the set, removes its member, and runs the occupancy-zero disposition once, without the GC.
  - Injected counter failures on release: the retry succeeds within the bound, or the slot stays counted.
  - A store error on `failFinalizing`'s write: the finalizing watchdog releases once.
  - full_revoke on a replica that does not bind the session, with the deny-list publish dropped: before the session's release runs, the binding replica's LLM proxy rejects the revoked lease token, and the session's release returns no lease. `// spec: §11.4 (User Invalidation)`.
- **Tier 9 (security).** full_revoke deny-lists and returns every lease of every cancelled session before its first cancel write, and `leasesRevoked` equals the deny-listed count. `// spec: §11.4 (User Invalidation)`.
- **Tier 11 (docs).** Extend `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go` and `tests/tier11_docs/redis_key_prefix_registry_test.go`: §5.2, §12.4 (both rows), §10.1.4, and `REG-SLOTCOUNT` describe a set, and none describes an `INCR` or a reset to 0. Add assertions that the SandboxClaim writer sites SPEC-2 edits name the occupancy-end delete and that §11.4 no longer names `USER_REVOKED`.

## Edge cases and accepted failure modes

The SPEC-owned cases are in the spec-changes file. These are the departures and code-level cases.

- **Releases no replica performs (departures).** Each is bounded as follows. An exclusive claim is reclaimed by the claim-orphan GC once the GC is registered. A concurrent slot stays counted, which fails closed, until the GC drains a pod that no non-terminal row names or until a Redis reseed; on a pod with continuous sibling traffic that can be the pod's lifetime. A lease row is removed by the expiry sweep, which also expires its deny entry, but the per-replica credential-assignment count is returned only by a process restart until the credential-accounting fix (N4) lands. The cases are:
  - a process exit between the terminal commit and the release (F-4.6.29);
  - the F-10.1.21 residue: the dead-connection eviction, coordinator loss, a failed readopt, and `ReleaseSuspendedPod` stopping between clearing `pod_assignment` and the release;
  - a `failFinalizing` write that commits and reports a store error on any exit other than the plan-parse exit (no hook runs, and the watchdog no longer lists the row);
  - a slot release that still fails when the bounded retry ends (CODE-1 item 5);
  - the claim `claimPod` created when `reserveResumeSlot` fails (N2), which a sibling may have reserved onto, so no by-name delete is safe;
  - a lease release that is lost.
- **A disposition failure after the member is removed.** The recycle patch or the claim delete fails after the release removed the last member. The release is not repeated (CODE-1 item 5). The claim is left for the claim-orphan GC, or for the recycle missing-report timeout once the claim is `recycling`. This is unchanged from today.
- **A superseded coordinator's terminal write loses the seal.** After CODE-5, a terminal write by a replica whose binding is stale does not seal through that binding, and the adopter's D6 release runs no seal, so §7.1 **Seal-and-export invariant:** is not met for that case. This is the same loss an off-holder terminal write already has under the F-10.1.33 departure (owner P2), which this case joins.
- **A reservation made during a Redis outage.** The §12.4 Postgres fallback admits without adding a member. After a Redis restart, the rehydration or the session's own release seeds its member (SPEC-1a). When Redis kept its data through the outage, the session's release finds no member and changes nothing.
- **F-7.3.34(b).** A rebuild-branch bind failure is on an exclusive pod, and the drain retires the pod, so the code is not changed.
- **An exclusive recycling pod rebound before a writer's by-name delete.** A peer's D6 recycles a `/start` binding of a pre-running session and a successor rebinds the pod before the writer's hook deletes the claim by name. The case predates this proposal, is narrow, and recovers through the successor's own failure path. Closing it needs session identity on the claim, which this proposal does not add.

## Files touched on application (non-spec)

- `pkg/gateway/storage/slotcounter/slotcounter.go` and its tests (CODE-1)
- `pkg/gateway/session/sessionstore/sessionstore.go`, `pgstore/pgstore.go`, `memstore/memstore.go` (CODE-1)
- `cmd/lenny-controller/main.go` (CODE-1)
- `pkg/gateway/podlifecycle/podclaim/slotclaimer.go` (CODE-1)
- `pkg/gateway/podlifecycle/podsession/slotbinder.go`, `reclaim.go`, `bindprepare.go`, `bindlaunch.go` (CODE-1, CODE-2, CODE-4)
- `pkg/gateway/metrics/gatewaymetrics/` comments (CODE-1)
- `pkg/gateway/sessionserver/usage.go`, `bind_rollback.go`, `isolationlevel.go` (CODE-2, CODE-4)
- `pkg/gateway/sessionserver/finalize.go`, `finalize_handler.go`, `session_terminal.go` (CODE-3)
- `pkg/gateway/session/createdsweeper/` (deleted), `cmd/lenny-gateway/workers.go`, `worker_seams.go`, `flags.go`, `controlserver.go`, `pkg/gateway/sessionserver/create.go`, `sessionserver.go`, `pkg/gateway/ARCHITECTURE.md`, `tests/registers/change-graph-coverage.yaml` (CODE-4)
- `pkg/gateway/sessionserver/terminal_hook.go`, `terminal_binding.go`, `stream_failure.go` (CODE-5, CODE-6, CODE-7)
- `pkg/gateway/externalapi/admin/users.go`, `users_invalidate.go`, `cmd/lenny-gateway/user_revocation.go`, `adminrouter.go`, `billing_security_workers.go`, `pkg/gateway/podlifecycle/podterminate/propagator/propagator.go` (CODE-7)
- The test-infrastructure stale-key cleanup (CODE-1 item 7)
- `tests/testinfra/lifecycleharness/` and the tier 2, 4, 5, 7a, 8, 9, and 11 test files named under **Testing** (TEST-1)
- `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` (REC-1)
