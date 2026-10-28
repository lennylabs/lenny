# Summary: Delegation tree budget lifecycle: the terminal pipeline returns budget once, the first reservation creates the sentinel, extension state is read per request, and erasure deletes the tree row

## Summary

**Problem statement.** The delegation tree's budget lifecycle does not conform to §8.3 and §8.6. A cancelled child never returns its `parallel_children` slot or `tree_memory` share. The child-terminal return credits no tokens, does not decrement `tree_size`, and can address a non-root key set. The Lua scripts recreate expired keys, drop TTLs, and never check the sentinel, so `BUDGET_KEYS_EXPIRED` and its cleanup do not exist. The §8.6 budget source is per-process, so the replica that did not create a session terminates it with no extension attempt. Enforcer, recorder, source, and coordinator state leaks, a deferred `RaiseBudget` can clear deny during a terminate, and batched waiters inherit the driving session's error. User erasure leaves `delegation_tree_budget` rows. The specification is also silent on who runs the return and how often, how a first reservation is recognized, which store records extension grants, where registration and consent state live, when that state is reclaimed, and what happens to a terminal requester or to batched waiters when the driving session ends.

**What changes.**
- §7.1 and §8.3: the budget return becomes a step of the delegated child's terminal pipeline and runs once, on the recorded root, with all four deltas, after a quiescence wait that starts at the terminal transition and that the release does not wait for, and the gateway refuses a usage quiescence timeout that is not below the seal window (SPEC-1, SPEC-2), and `FINAL_USAGE_REPORT` is specified as a per-session report carrying `session_id` (SPEC-10). The post-terminal hook body runs it (CODE-3).
- §8.3 and §16: the first reservation creates the sentinel, returns create no key and keep the TTL, only a reservation detects `BUDGET_KEYS_EXPIRED`, and the cleanup is idempotent per root (SPEC-3, SPEC-8). The Lua scripts, `delegate_task`, and a new cleanup handler implement it (CODE-1, CODE-2, CODE-4).
- §8.6, §9.2, and §19: extension state is read from the `SessionStore` and the `delegation_tree_budget` row on each request, consent state is per replica, and a terminal requester is dismissed with waiters redriven (SPEC-4, SPEC-5, SPEC-7, SPEC-9). A store-backed budget source replaces registration, the elicitor checks the requester, and the coordinator reclaims trees (CODE-6, CODE-7).
- §11.2 conformance: the enforcer forget runs in the hook body, `RaiseBudget` cannot clear deny during a terminate, and a per-replica sweep reclaims per-session state (CODE-5, CODE-8).
- §12.8: user erasure deletes the root's `delegation_tree_budget` row (SPEC-6, CODE-9).

**Decisions.**
- D1. The design fixes everything at the post-terminal hook, adds no idempotency marker, reads extension state per request, caps grants on the tree row, keeps consent state replica-local, threads the tenant from callers, and handles driver end in the elicitor. A lease elicitation carries no `originPod`, so `ExpireRaisedBy` never ends it, and only an elicitor-side check notices the driver ending; only this design also closes the F-8.6.25 deny-clear race.
- D2. Return ownership reuses the single-winner terminal write plus one `AfterTerminalWrite` run per applied write. Every writer, and the funnel through `recordSessionCompleted`, reaches the same body, so no guard key, column, or ledger is added, and §7.1 already says a cut pipeline is not resumed.
- D3. Decided by the owner on 2026-10-28 (open decision C), superseding the earlier owner-delegated settlement that placed the return before the release. The return is a step of the delegated child's §7.1 terminal pipeline, runs once on the recorded root, and follows §8.3 **Usage quiescence before budget return** (SPEC-2). Its quiescence wait starts at the session's terminal transition, and the release does not wait for it (SPEC-2 Edit (b)). The adapter sends the report during the `Shutdown` that the release issues, so a wait the release does not hold makes step 1 reachable, and a `Shutdown` that hangs cannot delay the return past the timeout. Until remediation step R12 adds the gateway's `CH-ADAPTEREVENTS` consumer, the wait ends at the timeout (see the defect **The gateway has no `CH-ADAPTEREVENTS` consumer**). The quiescence timeout is held below the seal window (decision B), so the wait ends inside the terminal pipeline budget and §7.1 **Terminal pipeline context:** needs no exception. A return that process exit cuts is not reclaimed or retried (Non-goals). Rejected: keeping the return before the release with the timeout as the only bound, because the child's final tokens go uncredited; returning after the release completes, because a hung `Shutdown` could consume the terminal pipeline budget before the return runs; a `context.WithoutCancel` detachment, because it takes the return outside the pipeline budget that §7.1 bounds; a fixed 5 s `treeBudgetReturnTimeout` constant, because it is a non-spec default with no override and duplicates the configurable quiescence timeout; a §7.1 exception stating that the budget does not bound the return, because it contradicts the next §7.1 sentence; and ending the wait when the child's `CH-ATTACH` stream closes, because §8.3 step 1 names the stream that carries `FINAL_USAGE_REPORT` and the gateway closes `CH-ATTACH` itself in the seal step.
- D4. Token credit follows the literal §8.3 step 3 formula. On the code's tree-wide additive counter it under-credits once per ancestor level, and rewriting the token-axis model is outside the validated problem.
- D5. The first reservation is recognized only on the absent-sentinel path: `ListByRoot` showing no delegated row is the witness, because the reserve precedes the child-row insert and only erasure deletes session rows. The `ListByRoot` call is paid only when the sentinel is absent.
- D6. Rebuilding the sentinel after Redis data loss belongs to §11.2 and §12.4 reconciliation, so the reserve path never reconstructs a tree, and a return that meets the absent sentinel is not a detection (D7). The shipped reconciler does not meet that contract; see **Redis recovery misses trees** under the defects below.
- D7. The `BUDGET_KEYS_EXPIRED` cleanup adds no state. The single-winner root write decides who emits the event, and only a reservation detects (SPEC-3 Edit (b)), so §8.3 cleanup step 2 needs no skip marker and the cleanup's own returns start no further cleanup.
- D8. The `delegation_tree_budget` row's cumulative `ext_*` columns are the single record of grants, and the Redis counters record consumption only. The Redis `token_grant` mirror is deleted because `Restore` never rebuilds it, at the cost of one primary-key read per `delegate_task` with a finite base cap.
- D9. Registration is deleted. One store-backed `BudgetSource` reads the requesting, root, and parent rows per request, with denial and grants from a `DenialStore` that has a Postgres and an in-memory backing.
- D10. The tenant is threaded from callers that already hold it. `SessionStore.GetByID` is used only on the platform-admin clear route, which its contract permits.
- D11. Extension grants are one pool on the `delegation_tree_budget` row: the `Grant` transaction caps the root's lease plus the cumulative grant at the effective max, and the reservation ceiling reads it, because the row is the only cross-replica record of grants, the §8.3 token counter is tree-wide, and per-replica amounts multiply the ceiling by the replica count. The open elicitation, its batch, and the success cool-off stay in the serving replica's memory, and losing them re-elicits but never grants without approval, because denial and cool-off are re-checked in the grant transaction.
- D12. Reclamation is the hook-body forget, a per-replica sweep over terminal or absent rows, and coordinator deletion once a tree is idle. The sweep covers other replicas and late requests without a generation key.
- D13. A terminal requester gets `ErrSessionTerminal` from the source and a dismissed elicitation from the elicitor. Waiters behind an ended driver re-enter consent, and the enforcer's `terminating` flag keeps deny set.
- D14. User erasure calls a new `pgstore.DeleteByRoot` after `PurgeRoot`. Tenant deletion already deletes the rows.
- D15. 0107 applies after 0105 and 0106 are implemented. Non-spec changes **Sequencing** is the one home for the ordering.

**Watch out for.**
- Do not route the `BUDGET_KEYS_EXPIRED` root write through `transitionToFailed`; see CODE-2.
- A return that meets an absent sentinel is not a detection, under a terminal root after cleanup or a live root before Redis recovery restores the tree. Treating it as one fires the `DelegationBudgetKeysExpired` alert on healthy trees; see CODE-3.
- `ParentSessionID` and `RootSessionID` do not classify a delegated child; see CODE-6 item 3.
- `subtreeSessionIDs` lists every session in the tenant. The return must not reuse it; see CODE-3.
- CODE-1 and CODE-4 land in one commit. The sentinel check without the first-reservation create fails every new tree.
- The sweep reclaims memory only and does not refuse late requests on a terminal session.
- The release does not wait for the return step, whose wait runs only for a row that CODE-3 item 2 admits. Calling the release after the wait would let a hung `Shutdown` hold the return, and a wait started ahead of those checks would hold every session's hook open for the timeout; see CODE-3 item 1.

## Goals

- A delegated child's terminal transition returns its `parallel_children` slot, `tree_memory` share, `tree_size` unit, and unused tokens once, on the recorded root, whichever writer committed it (F-8.3.2, F-8.3.4, F-8.3.5).
- The Lua scripts check the sentinel, create the tree-wide keys on the tree's first reservation, never recreate a key, and never drop a TTL, and `BUDGET_KEYS_EXPIRED` runs the §8.3 cleanup once per root (F-8.3.3).
- Any replica serves an extension request for any session, a terminal session receives no grant and no elicitation, and batched waiters survive the driving session ending (F-8.6.24, F-8.6.25, F-8.6.26).
- Per-tree and per-session extension, consent, enforcer, and recorder state is reclaimed on every replica (F-8.6.27, F-11.2.31).
- User erasure deletes the user's `delegation_tree_budget` rows (F-12.8.57).

## Non-goals

- A per-child idempotency marker for returns: a Redis `{root}:dlg:returned` set, a SETNX key, a session or archive column, or the archive row. The single-winner terminal write already gives exactly-once (D2), a marker adds a key with its own TTL and erasure obligation, and the archive read is read-then-write.
- Adding the return to each writer's signal tail (`archiveCancelled`, `cancel_session`'s nil tail, `settledChildSignals`). The next writer would miss it.
- A Postgres compare-and-set return owner. The hook already has one owner, and a CAS marker cannot commit atomically with the Redis effect.
- Adding spec text only for §12.8. The spec gives no witness for the first invocation, says nothing about where elicitation state lives across replicas, and does not reconcile the §8.6 in-flight bullet with §8.3's Redis counters.
- Enumerating terminal writers only in code with no spec clause. A reviewer cannot check exactly-once against text that does not exist.
- Changing §8.3 step 3 to "own consumption" (D4). It is filed as a follow-up.
- Rewording §8.3 step 4's `DECRBY`/`INCRBY` direction to match the code's upward counter. The observable behavior is equivalent.
- Creating the sentinel at root-session creation. Session creation would depend on Redis, and a failed write would later fail a healthy tree.
- Creating the sentinel whenever the parent is the root, or when `children_total` is absent. Neither can be told apart from TTL expiry, and both silently restart counters.
- Using the `delegation_tree_budget` row's existence as the first-reservation witness. `Deny` and `Grant` upsert the row before any delegation.
- Calling `ListByRoot` on every delegation from the root (D5).
- Young-tree reconstruction on a missing sentinel (D6).
- Narrowing the sentinel check to returns only. That weakens a fail-closed MUST.
- Keeping the Redis `token_grant` mirror and rebuilding it in `Restore` and §11.2 step (e) (D8).
- Making Postgres authoritative for consumption counters, or Redis authoritative for grants. The first contradicts §8.3 and §12.4, and the second breaks the in-transaction denial re-check.
- Durable per-session extension amounts: `subtree_id` rows, an `extension` object on the `delegation_lease` JSONB, or folding grants into `MaxTokenBudget` (D11).
- Lazy hydration of `MemoryBudgetSource` on a miss. It keeps two registration paths and maps to reclaim.
- Treating a registration miss as unknown or Pending. The session still gets no extension attempt.
- A new registration table or Redis hash. The `SessionStore` rows already carry every field.
- Resolving the tenant with `SessionStore.GetByID` outside the platform-admin path (D10).
- Cross-replica elicitation serialization through a Redis lock, a Postgres claim, durable success cool-off columns, or forwarding over `LNK-INTERREPLICA`. Each is new protocol for a coalescing property, and denial and cool-off are already durable.
- Finding a pending elicitation for a tree through the interaction store. The store is keyed by session and has no tree index.
- Relying on `expireRaisedElicitations` to end the driver's prompt (D1).
- A recorder forget on a terminal read in `RecordUsage`, or keying the enforcer on a session generation (D12).
- Subtree-scoped denial. It is outside the validated problem.
- The §8.10 tree-settle purge of keys and rows, and growth of `delegation_tree_budget` rows beyond erasure.
- Emitting `delegation.budget_return_usage_lag`, which is listed under the defects below.
- Running the return before the release with the timeout as its only bound, running it after the release completes, detaching it from the terminal pipeline context, giving it its own fixed timeout, or adding a §7.1 exception that leaves it outside the terminal pipeline budget (D3).
- Retrying, re-driving, or reclaiming a return that process exit or the terminal pipeline budget cut. Without the marker rejected above, no reclaimer can tell a cut return from one that ran, or from a reservation whose child row is not yet inserted (D5), and the loss fails closed (non-spec Edge cases).
- Restoring per-parent `parallel_children` and `children_total` during reconstruction.
- Seeding the per-session enforcer from durable `sessionusage`. The crash window it would close is listed under the defects below.
- F-8.2.32 (`perChildMaxAge` default). It is listed under the defects below.
- A §7.1 pipeline clause for the enforcer discard. The forget move is conformance.
- Deleting `MemoryBudgetSource`'s grant-math tests wholesale. They migrate to store-backed source fixtures.

## Open decisions for human to make

None. Decision C, the order of the budget return against the pod or slot release, was decided by the owner on 2026-10-28 and is recorded as D3.


## Defects in the shipped tree that this proposal does not stage

- **F-8.2.32.** A child that omits `perChildMaxAge` is unbounded by it. It concerns a `DelegationPolicy` default rather than budget state, and its fix depends on 0105 being implemented, so it is filed as its own proposal.
- **Token credit under-credits per ancestor level.** The literal §8.3 step 3 formula subtracts descendant consumption once per ancestor on the tree-wide additive counter (D4). Fixing it means rewriting the token-axis model, which is outside the validated problem.
- **`delegation.budget_return_usage_lag` is never emitted.** §8.3 **Usage quiescence before budget return** step 2 names the event and `lenny_delegation_budget_return_usage_lag_total`, and the gateway implements neither. CODE-3 item 1 ends the wait on the step 2 timeout without emitting either, so a return that took the timeout path is not observable. The counter is registered only in `pkg/observability/metrics/catalog.go:179`, and no code emits the event or increments the counter. This proposal does not stage the emission because the gap affects only observability and SPEC-2 leaves the emission clause unchanged. Until the gateway has a `CH-ADAPTEREVENTS` consumer, every return takes the timeout path, so emitting now would warn on every delegated child's return. The gap is filed as 8.3-M10 in `BUILD-GAPS.md`.
- **A late request on a forgotten terminal session.** After the forget, a late proxied request re-creates an enforcer entry with deny unset, and `Allow` returns true for it. Admission of requests on a terminal session belongs to the proxy's admission check rather than to budget state.
- **A budget termination lost to a crash is not retried.** §11.2 enforces the per-session budget against Redis counters and restores them after a crash (**Crash Recovery for Quota Counters:**), and the shipped `sessionbudget.Enforcer` holds consumption in process memory. A budget termination that a crash cuts before its terminal write leaves the session running, and the next replica's enforcer counts from zero, so the session can spend up to another full budget. This is the residue proposal 0100 handed to F-11.2.31. Moving per-session enforcement onto the shared counters is §11.2 work outside the tree budget lifecycle, so it is filed as its own finding.
- **Subtree-scoped denial.** §8.6 denies the requesting subtree, and the code denies the whole tree.
- **Per-parent counters are not restored.** §11.2 reconstruction restores the tree-wide keys and not `parallel_children` or `children_total`.
- **The §11.4 full revoke cancel writes run no terminal pipeline.** `AfterRevokeWrite` drains the inbox and DLQ and runs no budget return, so a child the revoke cancels leaves its reservation counted, against §7.1 **Terminal pipeline context:**. `AfterRevokeWrite` is not a terminal pipeline: it runs on the admin request's context, once per cancelled session in turn. Adding the return to it alone is the per-writer copy that Non-goals rejects, and it would run the usage quiescence wait once per cancelled delegated child inside the admin request. The fix is to run the post-terminal hook for these writes, which is §11.4 work.
- **The §8.10 tree-settle purge is absent.** Tree-wide keys and `delegation_tree_budget` rows are not cleaned up when the last session in the tree settles, so keys live until their TTL and rows until erasure.
- **Redis recovery misses trees.** §11.2 **Crash Recovery for Delegation Budget Counters:** rebuilds every active tree before `delegate_task` resumes for it, and §12.4 rejects `delegate_task` during the reconciliation window. The shipped `delegationbudget.Reconciler` rebuilds only trees that have a `delegation_tree_budget` checkpoint row, runs only on a down-to-up edge its probe observes, and gates no `delegate_task`. After CODE-1 and CODE-4, a tree it misses fails at its next reservation with `BUDGET_KEYS_EXPIRED`, where today its counters restart at zero. The fix needs a restart detector and a reconciliation gate, which the other §12.4 recovery reconcilers also lack, so it is filed as its own proposal.
- **The delegation-budget-recovery runbook names surfaces that do not exist.** `docs/runbooks/delegation-budget-recovery.md` sends the operator to `/v1/delegations/{tree}/close`, to `delegation.budgets.*` Helm values, to `GET /v1/admin/delegation-trees`, and to a Redis key `delegation:budget:<tree-id>`, and its overview says expired keys make mints fail with `BUDGET_EXHAUSTED`. No gateway route, chart value, or key of those names exists; the tree's keys are `{root}:dlg:*`. These lines are wrong independently of this proposal's changes, and correcting them means choosing the operator procedure for an expired or exhausted tree, so DOC-1 does not take them and they are filed as their own documentation defect.
- **`DELEGATION_BUDGET_UNAVAILABLE` has no docs home.** SPEC-3 Edit (b) adds a Postgres-read cause for `DELEGATION_BUDGET_UNAVAILABLE`, and no page under `docs/` names the code. The `lenny/delegate_task` error table at `docs/runtime-author-guide/platform-tools.md:66-77` omits it, although clients receive it through `schemas/lenny-adapter.proto:615` and `pkg/gateway/externalapi/errorclassify/errorclassify.go:444`. The existing §12.4 Redis-outage cause is equally undocumented, so the gap predates this proposal, and no staged change depends on a docs row for the code. DOC-1 therefore does not take it.
- **Pool admission has no seal window to compare.** SPEC-2 has pool admission refuse a pool whose `maxWorkspaceSealDurationSeconds` is not greater than the usage quiescence timeout, and the tree carries that window only as the gateway flag `--workspace-seal-max-duration-seconds`, so the startup check is the whole check. Widen F-5.2.57, which files the same gap for the §5.2 `claimOrphanTimeout` bound, to cover SPEC-2.
- **The chart renders no usage quiescence timeout.** §11.3 names `delegation.usageQuiescenceTimeoutSeconds` as a Helm value, and `charts/lenny` has no such value and renders neither `--delegation-usage-quiescence-timeout-seconds` nor `LENNY_DELEGATION_USAGE_QUIESCENCE_TIMEOUT_SECONDS`, so a chart deployment runs the 5 s default and its seal window cannot go below 6 s under CODE-3's startup check. File it under §11.3; the chart's `delegation:` block and its `with .Values.delegation` env render are where the value goes.
- **The gateway has no `CH-ADAPTEREVENTS` consumer.** §8.3 **Usage quiescence before budget return** step 1 ends the wait on the child's `FINAL_USAGE_REPORT` or on that stream's close, and no gateway code opens the stream, so CODE-3's wait always runs to the step 2 timeout. The consumer belongs to remediation step R12, because the adapter enters its pod-wide §10.1.4 hold on any end of the stream while it holds a started session (`onCoordinatorChannelClosed` in `pkg/adapter/holdstate.go`). A gateway client that lands before R12's corrected hold entry condition would therefore hold, and at the hold timeout terminate, every session on a pod whose stream its replica closes or loses, including sessions another replica coordinates.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0106 | Approved (2026-10-27) | CODE-6 deletes `registerLeaseTree` and `LeaseTreeRegistrar`. That removes the `registerLeaseTree` calls 0106 CODE-A adds after the derive insert (derive item 4) and the replay insert (replay item 3), and the recording-registrar assertion in 0106 TEST item 1 (`derive_test.go`, `replay_test.go`) loses its subject, as does 0106 D3. 0106 CODE-A's removal of the derive and replay parent writes stands, and 0106's other deliverables are unaffected. CODE-6's delegated-child test (see CODE-6 item 3) does not depend on 0106's parent-write removal. | Nothing. 0106 lands first (D15), and 0107 CODE-6 deletes the calls and, under its item 7, the registration assertion. Proceeding over approved 0106 is the human's call, on whether 0107 D9's store-backed source replaces the registration 0106 D3 chose. |
| 0105 | Approved (2026-10-27) | 0107's CODE-3 item 6 writer table and its quiescence wait inside the post-terminal hook depend on 0105 CODE-4 item 2, which runs the watchdog, orphan-reconciler, and orphan-cleanup terminal hooks on a detached goroutine; the rest is file-level overlap (0105 CODE-6 edits `usage.go` and `session_terminal.go`, which CODE-3 also edits; 0105 CODE-7 edits `delegation/service.go` and `mcptools_register.go`, which CODE-4 also edits). No statement of 0105 changes. | Nothing. 0105 lands first (D15). |
| 0104 | Approved | File-level overlap only: CODE-6 edits `cmd/lenny-gateway/gwcontrol_server.go` and the `leasecontrol` package, which 0104 CODE-1 also edits for service-account tokens. No statement of 0104 changes. | Nothing. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md — §7.1 terminal pipeline lists the delegated child's budget return.
- SPEC-2 — spec/08_recursive-delegation.md — §8.3 item 3 and **Usage quiescence before budget return** steps 1 and 2: the return runs once, from the terminal pipeline, on the recorded root, after a wait that starts at the terminal transition and that the release does not wait for, and the bound on the usage quiescence timeout.
- SPEC-3 — spec/08_recursive-delegation.md — §8.3 sentinel creation, reservation-only detection, TTL preservation, and returns that modify no key.
- SPEC-4 — spec/08_recursive-delegation.md — §8.6 grant scope, per-request extension state, grant authority, replica-local consent state, denial persistence before `REJECTED`, reclamation, and terminal sessions.
- SPEC-5 — spec/08_recursive-delegation.md — §8.6 elicitation flow step 2: batched requests outlive the driving session.
- SPEC-6 — spec/12_storage-architecture.md — §12.8 erasure deletes the root's `delegation_tree_budget` row.
- SPEC-7 — spec/09_mcp-integration.md — §9.2 item 6: a lease-extension elicitation is dismissed when its requester is terminal.
- SPEC-8 — spec/16_observability.md — §16.1 and §16.5 count a `BUDGET_KEYS_EXPIRED` detection.
- SPEC-9 — spec/19_resolved-decisions.md — §19 row 13 serializes elicitations per tree on each replica.
- SPEC-10 — spec/04_system-components.md — §4.7.3 `FINAL_USAGE_REPORT` is per session with `session_id`, and §28.5.2 and §29.4 drop its stream-close timing.
- CODE-1 — pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go — sentinel check, first-reservation create, and a non-recreating, TTL-preserving return.
- CODE-2 — pkg/gateway/sessionserver/budget_keys_expired.go — `BUDGET_KEYS_EXPIRED` tree cleanup, idempotent per root.
- CODE-3 — pkg/gateway/sessionserver/terminal_hook.go — tree-budget return in `afterTerminalWrite` with full deltas and the recorded root.
- CODE-4 — pkg/gateway/mcpfabric/delegation/service.go — first-reservation retry, row-raised token cap, reserve-side expiry, and the `token_grant` mirror deletion.
- CODE-5 — pkg/gateway/session/sessionbudget/sessionbudget.go — forget in the hook body, and `RaiseBudget` cannot clear deny during a terminate.
- CODE-6 — pkg/gateway/mcpfabric/delegationtree/leasecontrol/storebudget.go — store-backed §8.6 budget source; registration and `TenantResolver` deleted.
- CODE-7 — cmd/lenny-gateway/lease_elicit.go — terminal requester, driver-end redrive, denial write, and coordinator reclamation.
- CODE-8 — cmd/lenny-gateway/budget_state_sweep.go — per-session state sweep on every replica.
- CODE-9 — pkg/gateway/mcpfabric/delegationbudget/pgstore/pgstore.go — user erasure deletes `delegation_tree_budget` rows.
- DOC-1 — pkg/alerting/rules/rules.go — `BUDGET_KEYS_EXPIRED` detection and budget key TTL wording in the alert catalog, metrics reference, runbook, and configuration reference, and the `FINAL_USAGE_REPORT` row in the adapter contract reference.
