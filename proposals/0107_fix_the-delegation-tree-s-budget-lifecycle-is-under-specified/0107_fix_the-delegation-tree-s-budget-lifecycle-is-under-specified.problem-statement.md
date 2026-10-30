# Problem: Delegation tree budget lifecycle diverges from §8.3 and §8.6, and the specification leaves return idempotency and cross-replica extension state open

## Statement

The delegation tree's budget lifecycle is mis-implemented against a specification that already answers most of its questions, and the specification is silent on a narrower set of points (plan item P4b; BUILD-GAPS F-8.3.2 to F-8.3.5, F-8.6.24 to F-8.6.27, F-11.2.31, and F-12.8.57, all OPEN).

The following are code defects against existing specification text, and their fix is conformance with expansive tests:

- The MCP `cancel_child` and `cancel_session` paths never run the tree-budget return. The `cancel_child` signal tail only archives the node, and a later settle skips the return because the node is already archived (F-8.3.2, High). §8.3 step 3 requires a return on every terminal state, cancelled included.
- `returnScript` clamps with a plain `SET`, which drops the key's TTL, and its `DECRBY` recreates an absent key with no TTL. Neither the reserve script nor the return script performs the `EXISTS {root_session_id}:dlg:tokens` sentinel check, so `BUDGET_KEYS_EXPIRED` is unimplemented (F-8.3.3). §8.3 "Defense-in-depth TTL" and "`BUDGET_KEYS_EXPIRED` detection and tree cleanup" require both.
- The child-terminal return credits no unused tokens and does not decrement `tree_size` (F-8.3.4). §8.3 step 4 requires `budget_return.lua` to do both. The `tree_size` omission is outside the original finding and belongs to the same fix.
- `treeRoot` returns the deepest reachable node rather than the root when a parent read fails, and `archiveSettledChild` ignores the row's `RootSessionID` (F-8.3.5).
- The §8.6 budget source is the per-process `MemoryBudgetSource`. The production chart runs two gateway replicas, so a tree-registration miss on the replica that did not create the root fails terminal and terminates the session with no extension attempt (F-8.6.24, High). §8.6 "Durability" already requires the extension-denied flag and `cool_off_expiry` to be read from the `delegation_tree_budget` Postgres row on every request, and §10.1.1 states that gateway replicas are stateless proxies over externalized state.
- The per-session budget enforcer and recorder are forgotten only through `recordSessionCompleted`, and the `AfterTerminalWrite` hook path that `cancel_child`, the budget terminator, and other writers use does not reach them (F-11.2.31).
- User erasure leaves `delegation_tree_budget` rows (F-12.8.57). The plan routes it to P4, and the 0106 review assigned it to P4b as budget-key work.

The specification is silent or inconsistent on these points, which a proposal must settle:

1. Budget returns have no idempotency or exactly-once rule, and the specification does not name which terminal writers run the return. Once F-8.3.2 adds a return on the cancel paths, several writers can reach one child (cancel paths, the child-terminal funnel, and orphan completion), and a double credit becomes possible without a single owner or a guard.
2. The sentinel and TTL rule does not cover a tree that reserves no token slice. The reserve script never creates `{root_session_id}:dlg:tokens` for such a tree, so adding the mandated sentinel check without a creation rule would fail every such tree.
3. §8.6 "In-flight requests" increments the tree budget counters inside a Postgres row-locked transaction, while §8.3 holds the same counters in Redis. The specification does not say which store is authoritative for a counter that an extension grant raises.
4. The specification does not name a store for the session-to-tree and tenant registration that the §8.6 extension path resolves, or for per-tree episode and consent-batching state across replicas.
5. The specification does not state when per-tree and per-child extension and consent state is reclaimed (F-8.6.27). Most of this follows once the state is externalized, and §8.10 cleanup after the last session settles covers the shared keys.
6. The §8.6 approval modes do not state that a grant or an elicitation dispatch to an already-terminal session is dropped (F-8.6.25), or that a batched waiter's outcome is decoupled from the driving session ending (F-8.6.26).

F-8.2.32 (a child that omits `perChildMaxAge` is unbounded by it) is a confirmed defect and a spec gap, but it is removed from this problem. It concerns a `DelegationPolicy` default source rather than budget state, it sits outside the plan's P4b seam, and its fix depends on proposal 0105, whose status is Approved rather than Implemented. It is filed as its own proposal after 0105 is implemented.

## Evidence

- (verified) BUILD-GAPS entries exist, are OPEN, and carry the stated severities: F-8.3.2 High (BUILD-GAPS.md:10071), F-8.3.3 Medium (:10078), F-8.3.4 Medium (:10085), F-8.3.5 Low (:10092), F-8.6.24 High (:11123), F-8.6.25 Medium (:11130), F-8.6.26 Medium (:11137), F-8.6.27 Low (:11144), F-11.2.31 Medium (:18445), F-8.2.32 Medium (:9473), and F-12.8.57 Medium (:25131, owner P4).
- (verified) Spec §8.3 "Delegation budget key hash tag convention (R-04)", "Defense-in-depth TTL", "`BUDGET_KEYS_EXPIRED` detection and tree cleanup", step 3 "Return on completion", "Usage quiescence before budget return", and step 4 "Concurrency safety" in spec/08_recursive-delegation.md.
- (verified) Spec §8.6 "Lease Extension Approval Modes", the "Durability", "Query semantics (handoff-safe)", and "In-flight requests (atomic check)" bullets in spec/08_recursive-delegation.md.
- (verified) Spec §8.2 `lease_slice` table and "Defaults are derived from the deployer-configured `DelegationPolicy`" in spec/08_recursive-delegation.md.
- (verified) Spec §12.4 Redis key prefix table row for the `{root_session_id}:dlg:*` keys in spec/12_storage-architecture.md; §11.2 `delegation_tree_budget` checkpoint in spec/11_policy-and-controls.md; §10.1.1 "Gateway replicas are stateless proxies over externalized state" in spec/10_gateway-internals.md.
- (verified) The only occurrence of "idempot" in spec/08_recursive-delegation.md is the §8.10 duplicate-spawn paragraph. No return idempotency rule exists.
- (verified) Code, corrected locations:
  - `pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go`: `reserveScript` skips zero deltas and arms the TTL only on keys it touches; `returnScript` uses `DECRBY` and a plain `SET KEYS[i] 0`; neither checks the sentinel.
  - `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (`cancel_child` signal tail is `archiveCancelled` alone; `cancel_session` passes nil signals) and `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`cancelSubtree`, `cancelNode`, `archiveCancelled`).
  - `pkg/gateway/sessionserver/usage.go`: `archiveSettledChild` with its `alreadyArchived` guard, `returnTreeBudget` (sets only `TreeMemoryDelta` and `ParallelChildrenDelta`), and `treeRoot` (breaks on a failed `Get` and returns `cur.ID`); `pkg/gateway/sessionserver/terminal_hook.go`.
  - `pkg/gateway/mcpfabric/delegationtree/leasecontrol/`: `membudget.go` (in-process maps; its doc comment states it is not handoff-safe and is the seam for a Postgres-backed source), `budgetextension.go` (`ExtendForBudget` returns `OutcomeTerminal` on a `TenantOf` or `TreeBudget` miss; `fanOut` calls `TerminateSession` on errored members), `elicitation.go` (batched waiters copy `batch.err`; `treeFor` never deletes), and `leasecontrol.go` (`TreeBudgetGranter` bridges a §8.6 grant onto `*treebudget.Reserver`).
  - `pkg/gateway/session/sessionbudget/sessionbudget.go` (process-local `sessions` map; `Forget`), `cmd/lenny-gateway/llmproxy_usage.go`, `cmd/lenny-gateway/sessionsrv.go`, `cmd/lenny-gateway/sessiondeps.go` (wires `MemoryBudgetSource` when `--grpc-addr` is set), `cmd/lenny-gateway/session_budget.go`, and `cmd/lenny-gateway/lease_elicit.go`.
  - `pkg/delegation/lease/lease.go` (`ValidateChildSlice` compares `PerChildMaxAge` only when both values are above zero) and `pkg/gateway/mcpfabric/delegation/service.go` (`storeLeaseFromSlice` returns nil for an all-zero slice), relevant only to the excluded F-8.2.32.
- (verified) The original statement's code location `pkg/gateway/mcpfabric/delegation` for the tree budget, lease extension, and budget source was wrong; those live under `pkg/gateway/mcpfabric/delegationtree/`. The budget terminator is in `cmd/lenny-gateway`, not `pkg/gateway/sessionserver`.
- (verified) `charts/lenny/values.yaml` sets two gateway replicas, and `charts/lenny/templates/gateway-deployment.yaml` always passes `--grpc-addr`.

## Who observes it

- An orchestrating parent that races children and cancels the losers (for example `await_children` with mode `any`, then `lenny/cancel_child` on the rest). Each cancel leaks a `parallel_children` slot and a `tree_memory` share until the key TTL (default 8 days) expires, and after `maxParallelChildren` cancels the parent's further `delegate_task` calls are rejected although no child is running.
- A client whose session exhausts its leaf budget in proxy delivery mode on a default two-replica deployment, after a restart, a rolling update, or any routing to the replica that did not create the root. The session is terminated without the §8.6 extension attempt.
- Sibling sessions batched behind one consent elicitation. When the driving session ends, every waiter receives its error and is terminated.
- Operators, through Redis budget keys that never expire after a clamping return, counters recreated with no TTL, per-process extension and enforcer state that grows without reclamation, and `delegation_tree_budget` rows that survive user erasure.

## What breaks if nothing changes

- Parents that cancel children lose delegation capacity for the TTL window, so cancel-heavy orchestration stops delegating.
- Budget-exhausted sessions on multi-replica deployments are killed instead of extended, and a user rejection or cool-off held only in process memory does not survive a handoff, which §8.6 forbids.
- Unused tokens are never credited back to the parent and `tree_size` never decreases, so trees exhaust their token and size budgets early.
- A return after TTL expiry silently recreates partial counters instead of raising `BUDGET_KEYS_EXPIRED`, which is the corruption §8.3 names. A parent read failure credits a non-root key set.
- Process-local maps in `MemoryBudgetSource`, the consent coordinator, and the enforcer grow for the life of the replica.
- An erased user's `delegation_tree_budget` rows remain.

## Findings this unblocks

- F-8.3.2, F-8.3.3, F-8.3.4, F-8.3.5, F-8.6.24, F-8.6.25, F-8.6.26, F-8.6.27, F-11.2.31, and F-12.8.57.
- F-8.10.24 (TT-57, double budget return) is in the plan's P4b seam and still marked OPEN in BUILD-GAPS.md, but the 0106 review records it closed by commit 3f943f56f. It needs no fix here, and its double-return subject bears on the idempotency rule in point 1.

## Prior art considered

- No landed, approved, or draft proposal other than 0107 stages a fix for any member. 0106 (P4a, Approved) does not touch tree budget keys, budget return, or `leasecontrol`. 0104 overlaps only at the file level (`leasecontrol/satoken.go`). 0100 (Implemented) hands the F-11.2.31 enforcer-state residue on to its owner.
- Existing code to build on: the Redis `treebudget` reserver and returner, the Postgres `delegation_tree_budget` checkpoint store (`pkg/gateway/mcpfabric/delegationbudget/pgstore`), the Postgres denial store (`denialpg`), and migrations 0115 and 0130. A fix for F-8.6.24 and F-8.6.27 needs no new storage design.
- Proposal 0105 D10 declined to add a policy-level `perChildMaxAge` default and filed F-8.2.32. That gap has no prior art and is deferred out of this problem.

## Validated premises

- **premise (revise).** P1 to P9 hold as code defects (F-8.3.2, F-8.3.3, F-8.3.4, F-8.3.5, F-8.6.24, F-8.6.26, F-8.6.27, F-11.2.31, and F-8.2.32). P3 also found that `returnTreeBudget` omits the `tree_size` decrement. P10, "the spec does not state when and how budget returns run", is refuted: §8.3 states the trigger, ordering, operations, atomicity, and the sentinel rule, and only idempotency and the set of writers are absent. P11, "the spec does not state where tree budget and extension state live", is refuted: §8.3 and §12.4 place the counters in Redis, and §8.6 places the denial state in Postgres. Only registration, episode and batching state, and reclamation are unspecified. F-8.6.25 is plausible and partly checked: no terminal-state check exists in the non-test `leasecontrol` code.
- **evidence (revise).** Every BUILD-GAPS member exists, is OPEN, and matches its stated severity. Every code defect is verified at shifted lines. The spec citations resolve. The code locations were wrong and are corrected above. F-12.8.57 was missing from the member list. F-8.10.24 is correctly absent because 0106 records it closed.
- **prior-art (revise).** No other proposal stages these members. The claim that the spec does not say where state lives is refuted (loadBearing). The claim that the spec does not say when returns run is refuted except for idempotency and the no-token-slice sentinel (loadBearing). Existing Redis and Postgres stores are prior art for the fix.
- **scope (revise).** Nine members are coupled through shared keys and the terminal check: a §8.6 grant writes the same Redis counter the child-terminal return credits through `TreeBudgetGranter`, and F-8.6.25 and F-11.2.31 meet in `sessionbudget.go`. F-8.2.32 is out of scope (loadBearing), verified: it is outside the P4b seam, and 0105 is Approved rather than Implemented. F-12.8.57 should be placed explicitly; this statement includes it. The scope without F-8.2.32 is comparable to 0106.
- **impact (stands).** F-8.3.2 is reachable through the race-and-cancel pattern and blocks delegation after `maxParallelChildren` cancels for up to 8 days (loadBearing). F-8.6.24 hits the default two-replica chart and terminates sessions without an extension attempt (loadBearing). F-8.3.3 leaks keys and corrupts counters after expiry without direct user visibility. F-8.2.32 is confirmed. Several "unspecified" points are code divergences from existing text.
- **alternatives (revise).** F-8.3.2 to F-8.3.5 conform to existing text and need no spec change (loadBearing). F-8.6.24 and F-11.2.31 are covered in principle by §10.1.1 and §8.6 (loadBearing). The Redis-versus-Postgres authority for counters an extension grant raises is an open inconsistency. Return idempotency is the smallest defensible spec addition. F-8.6.27 largely follows from externalizing state. F-8.6.25 and F-8.6.26 each need at most a sentence in the §8.6 approval modes. F-8.2.32 needs a one-sentence clarification outside the budget lifecycle.
