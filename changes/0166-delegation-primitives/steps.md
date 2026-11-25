# Steps: 0166 Delegation as session primitives

## Validation owed

- The review of SL-12 of 0146 against a parent and a child that recover at the same time ran on 2026-11-23 and was not clean. Decision 15 adopts its fixes as the recovery rules that SL-12 takes on ("DP-7").
- A count of the §8 settings that the reference runtimes of §26 use today, which confirms that the settings "DP-6" moves to the extension have no user in the core. 0166.S3 waits on it.
- A check, for each reference harness of §26, that it shows its model a child's question with the tools to answer or forward it. 0166.S3 waits on it.
- A check, for each reference harness of §26, that it refreshes its MCP tool list when the bridge's `client` server changes its tools mid-session. 0122's step that builds the blocking interface waits on it, and 0166.S3's parent tools with it.
- The open decisions that applying "DP-7" raised in other changes on 2026-11-23 are all settled: O22, O23 (i), and O23 (ii) of 0122, O18 and O19 of 0126, O26 of 0135, D13 and D14 of 0139, and decisions 43 and 44 of 0149. O33 of this change, who may set `maxDepth: 0`, is settled as decisions 50 and 51.

## Steps

The owner settled every decision of `decisions.md` on 2026-11-22, 2026-11-23, and 2026-11-24, decisions 1 to 59. Under decision 35 the rules that "DP-7" lists belong to other changes, and they become steps of those changes when the owner turns to them. Each step below depends on those steps where it needs them. 0126 is not cancelled: 0126.S1 is cut back to deciding whether a session holds a grant (decision 39), and 0166.S1 depends on it. 0166.S2 also depends on 0170.S1 of [`0170-budgets`](../0170-budgets/change.md), which adds budget objects.

### 0166.S1 Session grant and parent link
- status: Planned
- aliases: [DP-1, DP-2]
- lanes: [specification, code, documentation]
- depends-on: [0126.S1, 0141.S1, 0146.S1, 0149.S3, 0149.S10, 0142.S2 (part), 0142.S6 (part), 0139.S2 (part), 0138.S1 (part)]

The session grant with its security and resource caveats and the content caveat that every session carries (decision 45), the root grant inputs and the resolution of every caveat with a runtime's defaults and ceilings (decision 40), the audit events of DPR-11, the platform operations of decision 3 with idempotency keys, tags, `lineage`, and `session.self`, and `parentSessionId` with the lineage, principal, environment, root `labels` (decision 44), bindings, egress, placement, and `onParentEnd` as a caveat that follow from it, including the tree terminate, and leaving the parent: `session.transfer` and `session.release` with the `leave`, `leaveMaxDepth`, and `maxLeaves` caveats and the rules of a leave (decisions 32, 33, 46, and 54). It depends on 0126.S1 for whether a session holds a grant, on 0141.S1 for the post-chain point where the checks run, on 0146.S1 for the states a session with a parent shares, on 0149.S3 for the session's stored environment, on 0149.S10 for a client's `maxDepth: 0`, on the part of 0142.S2 that adds the security record's delegation fields, on the part of 0142.S6 that adds SO-5a's rules, on the part of 0139.S2 that adds the platform tool server and its listing attribute, and on the part of 0138.S1 that adds the REST surface this step's leave routes extend. The end of a root that releases a child with `handoff: true` lands in 0146.S4 "Hand-off and leaving a parent", which depends on this step.

### 0166.S2 Budget caps
- status: Planned
- aliases: [DP-3]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0170.S1]
- closes: [F-8.3.9]

The count of a tree at its root's session budget and a cap on every key of every child, which sets nothing aside (decision 57, 0170 decision 70), with `session.set_limits` and its raise of a child's age (decision 13), the default cap from `defaultDelegationFraction`, what `session.transfer` and `session.release` do to a tree's counters, the root client's `PATCH` of a descendant's caps (0170 decisions 52 and 70), `gateway.delegation.sessionBudget` with its levels, its strictest value in effect, the limits it requires, and `SESSION_BUDGET_REQUIRED` (decisions 52 and 53), lease extension for the resource caveats, and the parallelism count with its difference from SL-10. It removes the tree budget of §8.3 and `maxTokenBudget`, and with them the TTL of the tree's keys: `delegation.budgetKeyTTLSeconds`, `BUDGET_KEYS_EXPIRED`, `GWSEAM-RearmBudget`, and `GWSEAM-HandleBudgetKeysExpired` (0170 decision 43, 0146 L36), which no other step removes. It closes F-8.3.9: a child's charges count at the root as they settle, so nothing returns when a child ends and no wait for its reservations remains. 0166.S1 lands `session.transfer` and `session.release` with no budget to move, and this step adds what each does to a tree's counters.

### 0166.S3 Reference extension and parent controls
- status: Planned
- aliases: [DP-4, DP-5, DP-8]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0166.S2, 0135.S5 (part), 0146.S2 (part), 0122.S1 (part), 0139.S2, 0153.S5 (part)]

`wakeOn` (decisions 17 and 25), `events.peek` (decision 10), `questions: forward`, `session.messages` as the parent's binding of the client's read, `session.interrupt`, `tools.result` with the parent's switches and the fenced default (decisions 23 and 31), the tolerant reading of DPR-12 with `capabilities.childEvents`, and the session tool set in the runtime SDKs and the reference MCP bridge over the operations of 0166.S1: the `sessions` library with its relay (decision 30) and `ToolSet`, the reference tools, `--session-tools`, and `--session-defaults`. The part that waits on 0135.S5 is the child events and the rendering rule, the part that waits on 0146.S2 is SL-12's resends, the part that waits on 0122.S1 is the client-tool interface parent tools use, and the part that waits on 0153.S5 is the mid-session overlay that `materialize` uses.

### 0166.S4 Removal of built-in delegation
- status: Planned
- aliases: [DP-6, DP-7]
- lanes: [specification, code, documentation]
- depends-on: [0166.S3]

The removal of the gateway-hosted delegation tools, the virtual child interface, and the settings "DP-6" drops, with successor pointers in §8 and the removals in the chart, the error catalog, and the observability catalogs.
