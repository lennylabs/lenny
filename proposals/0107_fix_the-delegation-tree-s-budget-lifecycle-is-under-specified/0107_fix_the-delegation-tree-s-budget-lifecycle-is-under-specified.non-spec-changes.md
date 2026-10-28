# Non-spec changes — Delegation tree budget lifecycle is under-specified

## Design (implementation-facing)

- The post-terminal hook body `afterTerminalWrite` is the one owner of the tree-budget return and the per-session forget (CODE-3, CODE-5). The signal tails stop carrying budget work.
- The Lua scripts enforce the sentinel, and the gateway decides first-reservation versus expiry (CODE-1, CODE-4). `HandleBudgetKeysExpired` is the one cleanup entry point, called only from the reserve side (CODE-2).
- The delegation tree's grants live only in the `delegation_tree_budget` row's `ext_*` columns. The reserve reads them for the token cap, and the Redis `token_grant` mirror is gone (CODE-4).
- One store-backed `BudgetSource` serves every replica, and consent state stays in the serving replica's memory (CODE-7). A per-replica sweep reclaims what the writer's hook cannot reach (CODE-8).

**Sequencing.** 0107 applies after 0106 is implemented. CODE-6 deletes the `registerLeaseTree` calls 0106 CODE-A adds to derive and replay, and CODE-6's delegated-child test assumes derive and replay no longer write a delegation parent.

## Staged code changes

### CODE-1 · treebudget scripts: sentinel check, first-reservation create, non-recreating TTL-preserving return

Targets: `pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go` (`reserveScript`, `returnScript`, `Reservation`, `Reserve`, `Return`), `pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget_test.go`.

1. Add `CreateTree bool` to `Reservation`, and add the exported sentinel error `ErrBudgetKeysExpired`.
2. `reserveScript` takes the create flag as a new trailing ARGV after the TTL. Before any read of the counters, it runs `EXISTS` on the tokens key (the sentinel, `{root}:dlg:tokens`).
   - Sentinel present: the script proceeds as today.
   - Sentinel absent and the flag unset: the script returns a distinct status and writes no key.
   - Sentinel absent and the flag set: the script runs `SET k 0 NX EX ttl` for each tree-wide key (`tree_size`, `tree_memory`, and `tokens`), then proceeds. The sentinel is created even when `TokenDelta` is zero.
   - The existing arm-on-create rule stays: a key the script writes gets the TTL when it has none.
3. `returnScript` runs `EXISTS` on the sentinel first. When the sentinel is absent, it returns the same distinct status and writes no key. Otherwise, for each non-zero delta, it skips a key that does not exist, runs `DECRBY` on a key that does, and clamps a negative result with `SET k 0 KEEPTTL`.
4. `Reserve` and `Return` map the distinct status to `ErrBudgetKeysExpired`. `ErrBudgetUnavailable` keeps its meaning (Redis could not be consulted).

CODE-4 owns the deletion of the `token_grant` mirror, so that it lands atomically with the `TreeBudgetGranter` deletion and the row-raised token cap.

### CODE-2 · `BUDGET_KEYS_EXPIRED` tree cleanup, idempotent per root

Targets: `pkg/gateway/sessionserver/budget_keys_expired.go` (new), `pkg/gateway/sessionserver/budget_keys_expired_test.go` (new), `pkg/gateway/sessionserver/usage.go` (`cascadeToChildren`).

`func (s *Server) HandleBudgetKeysExpired(ctx context.Context, tenantID, rootID string)`. Annotate it `// spec: §8.3, §7.1, §8.10`.

1. Increment `lenny_delegation_budget_keys_expired_total{tenant_id, pool}` on every call. The pool is the root row's pool.
2. Fail the root through a new hook-path writer, `failBudgetKeysExpired`, built on `writeTerminal`. It opens `BeginTerminal(rootID)`. Inside the locked mutator, a new `budgetKeysExpiredGuard` runs first. It admits only the states §7.2 gives a `BUDGET_KEYS_EXPIRED` transition to `failed` and returns `sessionstore.ErrSuperseded` for every other state (SPEC-3 Edit (d)), so do not reuse `nonTerminalGuard`, which admits `resume_pending`, `resuming`, and `awaiting_client_action`. Then the mutator sets `State=failed`, `FailureClass=session.FailureClassBudgetKeysExpired`, and `FailureReason="BUDGET_KEYS_EXPIRED"`, and records `MarkTerminated`. On an applied write it calls `afterFailed(ctx, from, updated)`. Do not route this write through `transitionToFailed` or the failure funnel: `reportGuard` refuses a suspended root that holds no pod, and `failureClassForReason` maps the reason to `runtime_failure`.
3. Only the writer whose root write applied does the following:
   - (a) It emits `delegation.budget_keys_expired` with `root_session_id` and `tenant_id`.
   - (b) It cancels every non-terminal `ListByRoot(tenantID, rootID)` row except the root's own, which `ListByRoot` also returns (§8.3 cleanup item 1), through `cancelNodeGuarded(ctx, tenantID, node)`. That helper is extracted from `cascadeToChildren`'s per-node block (`BeginTerminal`, `writeTerminal(cancelled, nonTerminalGuard)`, and `AfterTerminalWrite` on a detached goroutine), and `cascadeToChildren` calls it too, so there is no parallel cancel loop.
4. When the guard refuses the root write, the handler still runs step 3(b) and emits no event. The guarded writes make this idempotent.

A failed root write does not cascade by itself, because `settledChildSignals` has no `cascadeToChildren`, so the explicit sweep in step 3(b) is required.

### CODE-3 · Tree-budget return in `afterTerminalWrite` with full deltas and the recorded root

Targets: `pkg/gateway/sessionserver/terminal_hook.go` (`afterTerminalWrite`), `pkg/gateway/sessionserver/usage.go` (`archiveSettledChild`, `returnTreeBudget`), `pkg/gateway/sessionserver/session_subresources.go` (new `subtreeTokens` helper), `pkg/gateway/sessionserver/delegation_seams.go` (`TreeBudgetReturner`).

1. `afterTerminalWrite` calls `s.returnTreeBudget(ctx, sess)` after `releaseAfterTerminal` and before `expireRaisedElicitations`. Annotate it `// spec: §7.1 (Terminal pipeline context), §8.3`.
2. `returnTreeBudget` runs only when `sess.ParentSessionID != ""`. When such a row has `sess.RootSessionID == sess.ID`, it logs at warn and skips, because an empty root column reads back as the row's own ID. Otherwise it calls `Return` with:
   - `RootSessionID = sess.RootSessionID` and `ParentSessionID = sess.ParentSessionID`;
   - `TreeSizeDelta = 1`, `TreeMemoryDelta = treebudget.PerNodeMemoryBytes`, and `ParallelChildrenDelta = 1`;
   - `TokenDelta = max(0, slice − subtreeTokens(ctx, sess))`, where the slice is the child's `DelegationLease.MaxTokenBudget` (the `TokenDelta` its reservation carried). A zero slice gives a zero `TokenDelta`.
3. `subtreeTokens` is built from `ListByRoot(tenant, sess.RootSessionID)` and `treeArchive.Replay(tenant, sess.RootSessionID)`. It walks breadth-first from `sess.ID` and sums `billing.SessionTotals`. Do not reuse `subtreeSessionIDs`, which calls `store.List(ListFilter{})` across the whole tenant.
4. A `Return` error, including `ErrBudgetKeysExpired`, is logged and the step continues, so the reservation leaks conservatively. `ErrBudgetKeysExpired` from `Return` calls no `HandleBudgetKeysExpired` and increments no metric (SPEC-3 Edit (c)).
5. `archiveSettledChild` stops calling the return and drops the `alreadyArchived` guard on it, and it archives under `sess.RootSessionID`. Keep the `existingVer` read. Replace `treeRoot` only in `archiveSettledChild` and the return. `children_reattached.go`, `resume_driver.go`, `detachExceedsOrphanCap`, and `subtreeSessionIDs` keep calling `treeRoot`.

### CODE-4 · `delegate_task`: first-reservation retry, row-raised token cap, reserve-side expiry, and `token_grant` deletion

Targets: `pkg/gateway/mcpfabric/delegation/service.go` (`reserveTreeBudget`, a new `BudgetKeysExpiredHandler` seam, error mapping), `pkg/gateway/mcpfabric/delegationtree/leasecontrol/leasecontrol.go` (delete `TreeBudgetGranter` and its call in the grant path), `pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go` (delete `tokenGrantKey`, `GrantTokenBudget`, the `KEYS[7]` read and `cap + token_grant` in `reserveScript`, and `token_grant` in `RearmTree`), `pkg/gateway/mcpfabric/delegationtree/treebudget/rearm_test.go`, `cmd/lenny-gateway/sessiondeps.go` (wire the handler and the grant reader, and delete the granter wiring).

1. **Expiry decision.** `reserveTreeBudget` calls `Reserve` with `CreateTree: false`. On `ErrBudgetKeysExpired`, it runs `ListByRoot(tenant, root)`.
   - If no row has `ParentSessionID != ""`, retry once with `CreateTree: true`.
   - Otherwise retry `Reserve` with `CreateTree: false` once. A concurrent first delegation may have created the sentinel after the first attempt, and it commits its child row only after that. Only a second `ErrBudgetKeysExpired` calls `HandleBudgetKeysExpired(ctx, tenant, root)` through the seam and fails the call with `BUDGET_KEYS_EXPIRED`.
   - A `ListByRoot` error returns `ErrBudgetUnavailable` (SPEC-3 Edit (b)).
2. **Token cap.** `reserveTreeBudget` reads `DenialStore.Grants` (CODE-6) as `Grants(tenant, root).Tokens` only when `parentSlice.MaxTokenBudget > 0`, and sets `TokenCap = parentSlice.MaxTokenBudget + ext_tokens`. A `Grants` read error returns `ErrBudgetUnavailable` (SPEC-3 Edit (b)).
3. **Mirror deletion.** Delete the Redis `token_grant` mirror and `TreeBudgetGranter` in this same change. Removing one without the other either breaks the build or stops a granted extension from raising the reserve ceiling.

**IMPLEMENTOR'S CHOICE:** how `delegation.Service` receives the grant reader. The constraint is that it is a consumer-side interface in the delegation package, introduces no import cycle, and reads the same `ext_tokens` value that `DenialStore.Grants` returns.

### CODE-5 · Enforcer: forget in the hook body, and `RaiseBudget` cannot clear deny during a terminate

Targets: `pkg/gateway/sessionserver/terminal_hook.go`, `pkg/gateway/sessionserver/usage.go` (`afterTerminalRelease`), `pkg/gateway/session/sessionbudget/sessionbudget.go` (`counter.terminating`, `TerminateSession`, `RaiseBudget`).

1. Move `s.budgetForget(sess.ID)` from `afterTerminalRelease` into `afterTerminalWrite`, directly after the CODE-3 return step. `budgetForget` is the composed forget of the enforcer, the recorder, and the anomaly detector.
2. The per-session counter gains `terminating bool`, which `TerminateSession` and `Record`'s terminal branch set. `RaiseBudget` leaves `deny` set when `terminating` is true. Annotate it `// spec: §8.6, §11.2`.

### CODE-6 · Store-backed §8.6 budget source; delete registration and `TenantResolver`

Targets:
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/membudget.go`, replaced by `storebudget.go`.
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/memdenial.go` (new), the in-memory `DenialStore` extracted from today's inlined path, with `Grant` and `Grants`.
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg/denialpg.go` (`Grant`, `Grants`) and `tests/tier2_component/leasedenial/leasedenialstore_test.go`.
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/leasecontrol.go` (`TenantResolver`, `ErrSessionTerminal`, `DenialStore.Grant` and `Grants`, and `ExtendLease`), `budgetextension.go` (`ExtendForBudget` gains `tenantID`), and `dispatch_types.go` (`ExtendRequest.TenantID`).
- `pkg/gateway/sessionserver/delegation_seams.go`, `create.go`, `create_and_start.go`, `delegated_child_materialize.go`, `derive.go`, and `replay.go`: delete `registerLeaseTree`, `LeaseTreeRegistrar`, and every call.
- `pkg/gateway/mcpfabric/delegation/service.go`: delete `LeaseChildRegistrar` and its `AddSession` and `SetParentLease` calls.
- `pkg/gateway/externalapi/admin/lease_extension.go` (`handleClearExtensionDenial`).
- `cmd/lenny-gateway/sessiondeps.go`, `gwcontrol_server.go`, `controlserver.go` (`leaseExtendSeam` passes the tenant it receives), and `adminrouter.go` (delete `WithTenantResolver`).
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/*_test.go`, `tests/tier7a_load_local/scenarios/extension_episode_fanout`, and `tests/tier9_security/budget_extension_failclosed_test.go` (the `grantRecordingSource` fake).

1. **`StoreBudgetSource`** implements `BudgetSource`. Its dependencies are a `SessionStore` reader (`Get(tenantID, id)`), a `DenialStore`, the per-tree extension configuration from the deployment flags that feed `MemoryBudgetSource` today, and a clock. Construct one source, backed by `denialpg` when Postgres is configured and by `memdenial` otherwise.
2. **`TreeBudget(ctx, tenantID, sessionID)`** reads the requesting row. `ErrNotFound` returns `ErrSessionNotFound`, and a terminal row returns `ErrSessionTerminal`. The root is `row.RootSessionID`, and an empty value collapses to `row.ID`. Base limits come from the root row's `DelegationLease`, through the mapping `registerLeaseTree` computes today, moved into `leasecontrol`. Denial and cool-off come from `DenialStore.Denied`. `Current` is the base plus `DenialStore.Grants(tenant, root)`.
3. **Delegated-child test.** A session is a delegated child, and the source reads the parent row for the `parentLeaseCeiling` ceiling, only when `row.RootSessionID != "" && row.RootSessionID != row.ID`. Do not key this on `ParentSessionID`. A derived session is extendable as its own root with no parent ceiling.
4. **`ApplyGrant`** re-reads the row and returns `ErrSessionTerminal` for a terminal row. It then runs `DenialStore.Grant`, which in the transaction that re-checks the denial and under the same row lock clamps each dimension of the grant so that the row's cumulative `ext_*` value does not exceed the effective max less the root's base limit, adds the clamped amount, and returns it as the applied grant. `memdenial` clamps under its mutex. `ExtendLease` takes its per-dimension outcomes and its response amounts from the applied grant, so the `RaiseBudget` delta in `budgetextension.go` is the applied grant and a grant clamped to zero answers `CEILING_REACHED`. `DenialStore` gains `Grants(ctx context.Context, tenantID, rootSessionID string) (Dimensions, error)`: `denialpg` implements it as a primary-key `SELECT` of the `ext_*` columns and returns zero for a missing row, and `memdenial` returns its recorded cumulative grants.

   **IMPLEMENTOR'S CHOICE:** the `Grant` and `ApplyGrant` signatures that carry the clamp limit and return the applied grant. The constraint is that the clamp runs under the store's row lock or mutex, in the transaction that re-checks the denial.
5. **Tenant threading.** `ExtendForBudget(reqCtx, waitCtx context.Context, tenantID, sessionID string)` and `ExtendRequest.TenantID` carry the tenant, and the dispatch rejects an empty `TenantID`.
6. **Admin clear route.** `handleClearExtensionDenial` resolves the root row before the clear.
   - A tenant-admin caller uses `SessionStore.Get(principal.TenantID, rootSessionId)`. `ErrNotFound`, including a cross-tenant miss, answers 404.
   - A platform-admin caller uses `SessionStore.GetByID(rootSessionId)`, which the §24.11 contract permits on a platform-admin-gated path.
   - The route then calls `ClearSubtreeDenial(ctx, row.TenantID, rootSessionId, sessionId)`, and `found` is true exactly when the root row exists.
7. **Deletions.** Delete `MemoryBudgetSource`, `RegisterTree`, `AddSession`, `SetParentLease`, `LeaseTreeRegistrar`, `LeaseChildRegistrar`, `TenantResolver`, and `WithTenantResolver`. Migrate `MemoryBudgetSource`'s grant-math tests to `StoreBudgetSource` fixtures.

### CODE-7 · Elicitation: terminal requester, driver-end redrive, and coordinator reclamation

Targets: `cmd/lenny-gateway/lease_elicit.go` (`Elicit`), `pkg/gateway/mcpfabric/delegationtree/leasecontrol/elicitation.go` (`requestConsent`, `await`, `treeFor`), `pkg/gateway/mcpfabric/delegationtree/leasecontrol/elicitation_test.go`, `cmd/lenny-gateway/lease_elicit_test.go`.

1. **Requester check.** `leaseElicitor.Elicit` reads the requesting row with `SessionStore.Get(tenantID, sessionID)` before `Put`, and again on each poll tick.
   - Before `Put`, a terminal row or `ErrNotFound` returns the new `leasecontrol.ErrRequesterEnded` with no interaction written, and a read error returns that error.
   - On a tick, a terminal row or `ErrNotFound` sets the interaction to `PhaseDismissed` with `Reason = "LEASE_EXTENSION_REQUESTER_TERMINAL"` (SPEC-5) and returns `ErrRequesterEnded`. A read error on a tick is logged and polling continues.
2. **Redrive.** When the driver's `Elicit` returns `ErrRequesterEnded`, `requestConsent` closes the batch with a redrive flag instead of storing the error for waiters. `await` sends each waiter whose batch carries the flag back into `requestConsent`, so the first to re-enter becomes the driver through the existing `tc.pending` logic. A timeout or a user dismissal still propagates to every waiter as today.
3. **Coordinator reclamation.** `elicitCoordinator` deletes `trees[root]` once the tree has no pending batch and its success cool-off has elapsed. A `time.AfterFunc` scheduled at the cool-off end performs the delete, and it re-checks both conditions under the coordinator lock.

`fanOut` needs no change: `dispatchOne` reports a grant only from the post-`ApplyGrant` response, and CODE-6 makes `ApplyGrant` refuse a terminal session.

### CODE-8 · Per-session state sweep on every replica

Targets: `cmd/lenny-gateway/budget_state_sweep.go` (new), `cmd/lenny-gateway/sessionsrv.go` (the composed `BudgetForget` closure and the sweep wiring), `pkg/gateway/session/sessionbudget/sessionbudget.go` (`TrackedSessions`), `cmd/lenny-gateway/llmproxy_usage.go` (recorder `TrackedSessions`), `pkg/gateway/session/tokenanomaly/tokenanomaly.go` (`TrackedSessions`), and `pkg/gateway/session/sessionstore` (`ListAbsentSessions` on the interface, `memstore`, and `pgstore`).

1. Add `ListAbsentSessions(ctx context.Context, ids []string) ([]string, error)` to `SessionStore`. It returns each id that has no row, across tenants, in the same way as `ListStaleTerminalSessions`.
2. On every replica, the sweep runs every `--budget-state-sweep-interval` (a new flag, default `slotreaper.DefaultInterval`, 60 s) and does the following:
   1. It computes the union of the session ids tracked by the enforcer, the recorder's `grantedDelta` map, and the anomaly detector.
   2. It resolves the ids in batches of `slotreaper.DefaultBatchSize` through `ListStaleTerminalSessions(batch, now)` and `ListAbsentSessions(batch)`.
   3. It calls the composed `BudgetForget` for each returned id. On a store error, it keeps every entry of that batch and logs at warn.

The sweep reclaims memory only. It does not refuse a late request on a terminal session.

### CODE-9 · User erasure deletes `delegation_tree_budget` rows

Targets: `pkg/gateway/mcpfabric/delegationbudget/pgstore/pgstore.go` (`DeleteByRoot`), `cmd/lenny-gateway/adminrouter.go` (the `delegation_budget` `SessionEraser`).

`DeleteByRoot(ctx context.Context, tenantID, rootSessionID string) (int, error)` runs `DELETE FROM delegation_tree_budget WHERE tenant_id=$1 AND root_session_id=$2` under `pgtenant.InTx`. The eraser closure passes the tenant it receives, calls `PurgeRoot` and then `DeleteByRoot`, and fails the step on either error.

## Staged schema, chart, and migration changes

None. CODE-9 uses the existing `delegation_tree_budget` table, and CODE-8's flag needs no chart value.

## Staged docs changes

None staged yet. SPEC-3 and SPEC-8 make reader-facing pages that describe `BUDGET_KEYS_EXPIRED` detection and the budget key TTL stale, and the review log carries the owed docs change as an `OPEN` entry for the next non-spec loop.

## Testing

Every test carries its `// spec:` annotation, and every tier-2-and-up test carries a `// diagnosis:` comment.

1. **Script sentinel and TTL rules (CODE-1).** Tier 1 on miniredis and tier 2 on the real-Redis container (`tests/testinfra/containers/redis.go`). The cases are:
   - an absent sentinel without the flag returns `ErrBudgetKeysExpired` with no key written, for both scripts;
   - the flag creates `tree_size`, `tree_memory`, and `tokens` with the TTL for a zero `TokenDelta`;
   - a return skips a missing per-parent key;
   - a clamping return keeps the TTL (the discriminating assertion is `TTL > 0` after a clamp);
   - an over-return clamps at zero.

   Tier 7a: a randomized interleaving of reserves, returns, and sentinel deletions, run without TTL advance, asserts that no script creates or increments any counter while the sentinel is absent. `// spec: §8.3 (Defense-in-depth TTL), §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)`.
2. **Cleanup idempotency (CODE-2).** Tier 1 and tier 7a. The cases are:
   - randomized concurrent reservation detections, with the sweep's own cancels running their returns, end with exactly one applied root write carrying `failure_class=budget_keys_expired`, one event, and every member terminal;
   - the root in each of `running`, `input_required`, and `suspended` (with a held pod and with a released pod) is failed with that class, and a root in each of `resume_pending`, `resuming`, and `awaiting_client_action` keeps its state while every descendant is cancelled and no event is emitted;
   - a detach-policy child and its subtree are cancelled;
   - a terminal root with a live orphan increments the metric, cancels the orphan, and emits no event;
   - `cascadeToChildren`'s existing tests pass on the extracted helper.

   `// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §8.10`.
3. **Return once per terminal transition (CODE-3, CODE-5).** Tier 1 writer table. Each of these writers asserts exactly one `Return` with the four deltas on the recorded root, and exactly one forget:
   - the funnel through `recordSessionCompleted`;
   - `cancel_child` and its cascade;
   - `cancel_session` with nil signals;
   - the orphan reconciler and orphan cleanup through `OnSessionTerminal`;
   - the watchdog;
   - the budget terminator;
   - the admin adapters;
   - `failBudgetKeysExpired`.

   Further cases:
   - a seal relabel runs no second return;
   - a row with a parent and `RootSessionID == ID` logs and skips;
   - the token credit is `max(0, slice − subtree consumption)`, with a zero slice giving zero;
   - the return issues no tenant-wide `List`;
   - a return that meets `ErrBudgetKeysExpired` under a terminal root, and one under a live root before Redis recovery restores the tree, call no handler, leave the metric unchanged, and fail no session.

   Tier 7a: concurrent terminal writers on one child produce one `Return`. Tier 4: on the compose stack, a parent that races children and cancels the losers can delegate `maxParallelChildren` more times. `// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §11.2`.
4. **First reservation and token cap (CODE-4).** Tier 1. The cases are:
   - a deterministic interleaving where B's first reserve sees the sentinel absent, A then creates the sentinel and commits its child, and B's `ListByRoot` then sees A's child: B is admitted and the root does not fail;
   - two expired results with a delegated row call the handler and fail the call with `BUDGET_KEYS_EXPIRED`;
   - a `Grants` read failure and a `ListByRoot` failure reject with `DELEGATION_BUDGET_UNAVAILABLE` and leave no counter mutated;
   - a zero base cap makes no `Grants` read;
   - a granted extension raises `TokenCap` by `ext_tokens`.

   Tier 4: `denialpg.Grants` returns zero for a missing row and the cumulative `ext_*` values after two grants. `// spec: §8.3, §8.6 (Extension state)`.
5. **Deny cannot clear during a terminate (CODE-5).** Tier 7a: a randomized interleaving of `TerminateSession` and a fan-out `RaiseBudget` never leaves `Allow` true. `// spec: §8.6, §11.2`.
6. **Any-replica extension (CODE-6).** Tier 4: two gateway `Server` instances over one Postgres. A session created on A is extended on B. A denial written on A is observed on B's first request. Concurrent grants on A and B for one tree never raise `ext_tokens` past the effective max less the base, and the later grant receives the remaining headroom. A terminal session gets `ErrSessionTerminal` from `TreeBudget` and `ApplyGrant`. A derived session's ceiling ignores its derive source's lease.

   Tier 3 and tier 9 on the admin clear route:
   - a platform-admin clears a tree created on another replica;
   - a tenant-admin of `globex` gets 404 for an `acme` tree;
   - an unknown root gets 404.

   Tier 7a: the `extension_episode_fanout` scenario runs against the store-backed source. `// spec: §8.6 (Extension state), §8.6 (Durability), §10.1.1, §15.1`.

   Tier 2: the leasedenial store clamps a grant at the remaining headroom and returns the applied grant. Tier 1: concurrent `memdenial` `Grant` calls never exceed the headroom. `// spec: §8.6 (In-flight requests)`.
7. **Driver end and reclamation (CODE-7).** Tier 1. The cases are:
   - a driver whose session ends mid-poll dismisses the interaction with `LEASE_EXTENSION_REQUESTER_TERMINAL`, and its live waiters redrive into a new elicitation rather than being terminated;
   - a waiter whose own session ended before consent arrived gets neither a `RaiseBudget` call nor a grant;
   - a terminal requester before `Put` writes no interaction;
   - a timeout still propagates to every waiter;
   - `trees` is empty after the cool-off elapses with nothing pending.

   Tier 7a: randomized driver-end, waiter-arrival, and approval interleavings never terminate a live waiter because of the driver's end. `// spec: §8.6 (Elicitation mode flow), §9.2`.
8. **Per-replica sweep (CODE-8).** Tier 1 with a fake clock and store. The cases are:
   - a session that ends on replica A is forgotten on replica B within one tick;
   - a live session is retained;
   - an erased session (row deleted) is forgotten;
   - an entry that a late request re-creates is swept on the next tick;
   - more than 500 tracked ids are resolved in batches;
   - a store error retains every entry.

   Tier 7a: a randomized interleaving of `Record`, terminal writes, and sweep ticks never forgets a non-terminal session. Tier 4: `pgstore.ListAbsentSessions` returns only missing ids across tenants. `// spec: §11.2, §10.1.1`.
9. **User erasure (CODE-9).** Tier 4 and tier 9: erasing `alice` removes her roots' rows and keys and leaves `bob`'s untouched, and an eraser call under the wrong tenant deletes nothing. `// spec: §12.8 (Compliance Interfaces)`.

## Edge cases and accepted failure modes

- A gateway process that exits between the terminal write and the return step leaves the child's reservation, its parent's `parallelChildren` slot included, counted until the tree's budget keys are purged or expire, because §7.1 **Terminal pipeline context:** does not resume a terminal pipeline that process exit cuts (SPEC-1).
- When a sweep tick and a late request race, the late request re-creates an entry that the next tick sweeps (CODE-8).
- A `Grants` read failure rejects a delegation that would have fit, which is retryable (CODE-4).
- A replica that loses its in-memory consent state presents a new elicitation for a tree another replica already batched (SPEC-4 Edit (c)).

## Files touched on application (non-spec)

- `pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go`, `treebudget_test.go`, `rearm_test.go` (CODE-1, CODE-4)
- `pkg/gateway/sessionserver/budget_keys_expired.go`, `budget_keys_expired_test.go` (CODE-2)
- `pkg/gateway/sessionserver/terminal_hook.go`, `usage.go`, `session_subresources.go`, `delegation_seams.go` (CODE-2, CODE-3, CODE-5, CODE-6)
- `pkg/gateway/mcpfabric/delegation/service.go` (CODE-4, CODE-6)
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/leasecontrol.go`, `denialpg/denialpg.go`, `memdenial.go`, `storebudget.go`, `membudget.go` (deleted), `budgetextension.go`, `dispatch_types.go`, `elicitation.go`, and tests (CODE-4, CODE-6, CODE-7)
- `pkg/gateway/session/sessionbudget/sessionbudget.go` (CODE-5, CODE-8)
- `pkg/gateway/sessionserver/create.go`, `create_and_start.go`, `delegated_child_materialize.go`, `derive.go`, `replay.go`, and `pkg/gateway/externalapi/admin/lease_extension.go` (CODE-6)
- `pkg/gateway/session/tokenanomaly/tokenanomaly.go`, `pkg/gateway/session/sessionstore` (interface, `memstore`, `pgstore`) (CODE-8)
- `pkg/gateway/mcpfabric/delegationbudget/pgstore/pgstore.go` (CODE-9)
- `cmd/lenny-gateway/sessiondeps.go`, `gwcontrol_server.go`, `controlserver.go`, `adminrouter.go`, `lease_elicit.go`, `lease_elicit_test.go`, `sessionsrv.go`, `llmproxy_usage.go`, `budget_state_sweep.go` (CODE-4, CODE-6, CODE-7, CODE-8, CODE-9)
- `tests/tier7a_load_local/scenarios/extension_episode_fanout`, `tests/tier2_component/leasedenial/leasedenialstore_test.go`, `tests/tier9_security/budget_extension_failclosed_test.go` (CODE-6)
