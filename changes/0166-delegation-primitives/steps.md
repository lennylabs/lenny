# Steps: 0166 Delegation as session primitives

## Validation owed

- The review of SL-12 of 0146 against a parent and a child that recover at the same time ran on 2026-11-23 and was not clean. Decision 15 adopts its fixes as the recovery rules of "DP-4".
- A count of the §8 settings that the reference runtimes of §26 use today, which confirms that the settings "DP-6" moves to the extension have no user in the core. 0166.S3 waits on it.
- A check, for each reference harness of §26, that it shows its model a child's question with the tools to answer or forward it. 0166.S3 waits on it.

## Steps

The owner chose O1 option (a) on 2026-11-22 and settled O2 to O6, O8 to O11, and O14 to O16 on 2026-11-23. O7 settled on 2026-11-23 as decision 15. Every open decision is settled. 0166.S2 also waits on the step of [`0170-budgets`](../0170-budgets/change.md) that adds budget objects, which joins its `depends-on` once 0170 is on `main`.

### 0166.S1 Session grant and parent link
- status: Planned
- aliases: [DP-1, DP-2]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0149.S3, 0142.S2 (part)]

The session grant with its security and resource caveats, the root grant inputs with the tenant delegation settings and the environment's policy and ceilings, the audit events of DPR-11, the platform operations of O3 with idempotency keys, child labels, and `lineage`, and `parentSessionId` with the lineage, principal, environment, session labels, bindings, egress, placement, and cascade that follow from it, including the tree terminate, the `approval` and `approvalAboveCreditBudget` fields of a policy rule (decision 8), and `session.release` with its notice and hand-off (decisions 9 and 21), and `session.connect` and `session.disconnect` with the `peerMessaging` caveat (decision 20). It depends on 0146.S1 for the states a session with a parent shares, on 0149.S3 for the session's stored environment, and on the part of 0142.S2 that adds the delegation guardrail and SO-5a's rules, which this step retargets to `session.create`.

### 0166.S2 Carved budget
- status: Planned
- aliases: [DP-3]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0133.S3]

The carved budget as a reservation from the budget objects of 0170 (decision 12), with `session.set_limits` with the raise of a child's age, the `wake` setting of decisions 13 and 17, lease extension for the resource caveats, and the parallelism count by state.

### 0166.S3 Reference extension and questions
- status: Planned
- aliases: [DP-4, DP-5]
- lanes: [specification, code, documentation]
- depends-on: [0166.S1, 0166.S2, 0135.S5 (part)]

Child event delivery with the `question` event, the event classes and `wake` of decision 17, file parts in messages with `materialize` (decision 18), `wake: false` (decisions 19 and 22), peer messaging (decision 20), the blocking interface for tools a client offers with the parent's switch (decision 23), the recovery rules of decision 15, `session.interrupt`, `session.messages`, `questions.answer`, `questions.forward`, the paging and cancel rules of `events.wait`, `events.peek` (decision 10), the notice to a derived session (decision 11), the tolerant reading of DPR-12, and the child-session tool set in the runtime SDKs and the reference MCP bridge over the operations of 0166.S1, with `--session-tools` and `--session-defaults`. The part that waits on 0135.S5 is the rendering rule and the tool names, which the extension keeps.

### 0166.S4 Removal of built-in delegation
- status: Planned
- aliases: [DP-6, DP-7]
- lanes: [specification, code, documentation]
- depends-on: [0166.S3]

The removal of the gateway-hosted delegation tools, the virtual child interface, and the settings "DP-6" drops, with successor pointers in §8 and the removals in the chart, the error catalog, and the observability catalogs.
