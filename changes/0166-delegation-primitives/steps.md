# Steps: 0166 Delegation as session primitives

## Validation owed

- The review of SL-12 of 0146 against a parent and a child that recover at the same time ran on 2026-11-23 and was not clean. Decision 15 adopts its fixes as the recovery rules of "DP-4".
- A count of the §8 settings that the reference runtimes of §26 use today, which confirms that the settings "DP-6" moves to the extension have no user in the core. 0166.S3 waits on it.
- A check, for each reference harness of §26, that it shows its model a child's question with the tools to answer or forward it. 0166.S3 waits on it.
- A check, for each reference harness of §26, that it refreshes its MCP tool list when the bridge's `client` server changes its tools mid-session. 0166.S3 waits on it.

## Steps

The owner settled every decision of `decisions.md` on 2026-11-22 and 2026-11-23, decisions 1 to 34. 0166.S2 also waits on the step of [`0170-budgets`](../0170-budgets/change.md) that adds budget objects, which joins its `depends-on` once 0170 is on `main`. 0126.S1 builds the availability levels that decision 6 folds into the root grant inputs, so its availability work is done by 0166.S1, and 0126 is cut back when the owner turns to the other changes.

### 0166.S1 Session grant and parent link
- status: Planned
- aliases: [DP-1, DP-2]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0149.S3, 0149.S10, 0142.S2 (part), 0139.S2 (part), 0138.S1 (part)]

The session grant with its security and resource caveats, the root grant inputs with the tenant delegation settings and the environment's policy and ceilings, the audit events of DPR-11, the platform operations of decision 3 with idempotency keys, child labels, `lineage`, and `session.self`, and `parentSessionId` with the lineage, principal, environment, session labels, bindings, egress, placement, and cascade that follow from it, including the tree terminate, the `approval` and `approvalAboveCreditBudget` fields of a policy rule with the approval of a raise (decision 8), and leaving the parent: `session.reattach` and `session.release` with the `leave` and `leaveMaxDepth` caveats, the rules of a leave, the hand-off, and the REST routes for the root's client (decisions 9, 21, 32, and 33). It depends on 0146.S1 for the states a session with a parent shares, on 0149.S3 for the session's stored environment, on 0149.S10 for a client's `maxDepth: 0`, on the part of 0142.S2 that adds the delegation guardrail and SO-5a's rules, which this step retargets to `session.create`, on the part of 0139.S2 that adds the platform tool server and `internal: true` listing, and on the part of 0138.S1 that adds the REST routes and errors this step extends.

### 0166.S2 Carved budget
- status: Planned
- aliases: [DP-3]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0133.S3]

The carved budget as a reservation from the budget objects of 0170 (decision 12), with `session.set_limits` and the raise of a child's age (decision 13), lease extension for the resource caveats, and the parallelism count by state.

### 0166.S3 Reference extension, events, and tools
- status: Planned
- aliases: [DP-4, DP-5, DP-8]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0166.S2, 0135.S5 (part), 0146.S2 (part), 0122.S1 (part), 0139.S2, 0153.S5 (part), 0138.S1 (part), 0138.S3 (part)]

Child event delivery with the `question` event, the event classes with `childContent` and `wakeOn` (decisions 17, 25, and 28), the recovery rules of decision 15, file parts in messages with the delivered-part check and `materialize` (decision 18), `wake: false` (decisions 19 and 22), the parent as broker with the library's relay (decision 30), the blocking interface for tools a client offers with `clientToolHoldSeconds`, the parent's switch, the fenced default, and `approvals.parentTools` (decisions 23, 24, 29, and 31), the crossing rule of `session.messages` and the watch (decision 34), `session.interrupt`, `session.messages`, `questions.answer`, `questions.forward`, `tools.result`, the paging and cancel rules of `events.wait`, `events.peek` (decision 10), the notice to a derived session (decision 11), the tolerant reading of DPR-12 with `capabilities.childEvents`, and the session tool set and the `ToolSet` in the runtime SDKs and the reference MCP bridge over the operations of 0166.S1, with `--session-tools` and `--session-defaults`. The part that waits on 0135.S5 is the rendering rule and the tool names, which the extension keeps. The part that waits on 0146.S2 is SL-12's resends, which the recovery rules extend. The part that waits on 0122.S1 is the client-tool contract that the blocking call replaces. The part that waits on 0153.S5 is the mid-session overlay that `materialize` uses. The parts that wait on 0138.S1 and 0138.S3 are `wake: false` on REST, the `tool_call` route, and the webhook deliveries of the new session events.

### 0166.S4 Removal of built-in delegation
- status: Planned
- aliases: [DP-6, DP-7]
- lanes: [specification, code, documentation]
- depends-on: [0166.S3]

The removal of the gateway-hosted delegation tools, the virtual child interface, and the settings "DP-6" drops, with successor pointers in §8 and the removals in the chart, the error catalog, and the observability catalogs.
