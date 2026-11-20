# Steps: 0146 Session lifecycle

## Validation owed

- Whether each reference harness ends its turn promptly on `interrupt_request` and what it writes when it does, which decides how often "SL-3" falls back to the release at the deadline.
- How each reference harness treats the resume notice, which decides whether its text needs to vary by runtime.
- Whether each reference runtime reports `busy` on heartbeats through a long tool run inside the pod, which decides how often "SL-6" cuts a working turn.
- Whether models leave a boolean tool argument unset more often than they set it, which "SL-7" **One tool.** assumes.
- Which of Lenny's own dashboards, alerts, and recording rules under `docs/` and `charts/` count sessions by state, which the migration of stored `suspended` rows and the new states must update in 0146.S1.

## Steps

### 0146.S1 States and their rules
- status: Planned
- aliases: [SL-A, SL-1, SL-2, SL-3 (part), SL-4 (part), SL-5, SL-6 (part), SL-7, SL-9, SL-10, SL-11]
- lanes: [specification, code, documentation]
- depends-on: [0119.S1, 0119.S2, 0131.S8 (part), 0118.S4 (part), 0145.S3 (part), 0135.S4 (part)]
- closes: [F-8.8.17, F-8.8.23, F-15.1.61, F-8.10.28, F-7.2.35 (part), T-6.2.8, T-7.2.8, T-7.3.7, T-8.2.12]

The states and their rules: "SL-1" to "SL-7" and "SL-9" to "SL-11", with every specification change above except those 0146.S2 takes. Where those parts state a child event, 0146.S2 lands that statement, and this step lands the session's own reports: `turn_end` with `turn_lost` and `idle_timeout`, and the resume notice. This step owns the rows it shares with the message delivery design, the §28.5.3 `interrupt_request` row with §4.7's `Interrupt` RPC and the TTL rows of the §7.2 dead-letter table, and that design's rows point to it. Until MD-E lands, its text names the end-of-turn frame by its specification name, `response`, the delegation tools by their specification names, `lenny/delegate_task`, `lenny/cancel_child`, and `lenny/await_children`, and the qualifying activity of §6.2 and §4.7.1 keeps those names; 0135.S5 (MD-E) renames the frames and tools in those lists when it lands. Until 0139.S1 (RP-A) converts them, it reads the `interrupt` declaration as `integrationLevel: full` and sends `interrupt_request` on the channel the tree uses, as 0135.S1 does. The egress halt of step 1 of "SL-3" lands with CR-C of [`0124-connector-removal`](../0124-connector-removal/steps.md#steps), which depends on this step. The cancellation of a helper run or a setup command at a pod release, of "SL-3" **A running helper run.** and "SL-6", lands in 0146.S3, because the mid-session overlay it cancels lands in 0153.S5, which depends on this step through 0135.S5.

It lands with MD-A of the message delivery design: 0146.S1 and 0135.S1 share one branch, because MD-A's delivery paths name the new states and this step's transitions read MD-A's turn accounting. The following parts wait on steps of other changes, which the `(part)` edges record: the rules for a session under `checkpointPolicy: sealOnly` or `none` ("SL-3", "SL-6", and the pod hold of "SL-1", "SL-4", and "SL-10") on 0131.S8 (IC-8); the end of a session at the disk watch's threshold ("SL-2") on 0118.S4 (PL-5); the release of one slot container at a suspension and at the disk watch's end on 0145.S3 (SI-3); and the pending `request` as activity and the `reply` with `outcome: "withdrawn"` or `decision: "withdrawn"` ("SL-3" and "SL-6") on 0135.S4 (MD-19). The rest of the step can land first. The whole step waits on 0119.S1 and 0119.S2, which remove `type: mcp` runtimes and service mode, because "SL-6" lets every session become `dormant` on the ground that every runtime is an agent runtime (L31).

It closes F-8.8.17 and F-8.8.23 by removing the deadlock detector ("SL-8" **No deadlock to detect.**), F-15.1.61 through the precondition rows of the §15.1 row, which drop `input_required`, F-8.10.28 and the cascade part of F-7.2.35 through the order of "SL-9" at every terminal transition (SLR-25), with the `integration` test that every terminal writer applies `onParentEnd`, T-6.2.8 by removing `input_required`, T-7.2.8 through the `chaos` test of an unacknowledged `interrupt_request`, and T-7.3.7 and T-8.2.12 through the `integration` tests of the cascade. CHGWPOD-I10, the generation check on the `Interrupt` RPC, stays open: this step changes what the RPC's timeout writes and not the generation fence.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes the held-delivery operations (`GWSEAM-LockDelivery`, `GWSEAM-HoldMessage`, `GWSEAM-HoldUndeliveredForward`, `GWSEAM-DeliverHeld`, `GWSEAM-ResumeSuspended`, and `GWSEAM-AdmitResume`), `GWSEAM-EnqueueInbox`, and `GWSEAM-RouteMessage`, which chooses among those paths (SL-5 and SL-10), the delegation budget operations (`GWSEAM-ReturnBudget`, `GWSEAM-RearmBudget`, `GWSEAM-ObserveHighWatermark`, `GWSEAM-NodeUsage`, `GWSEAM-TreeUsage`, and `GWSEAM-SubtreeTokens`, SL-10), `GWSEAM-Orphaned` (SL-9), and `GWSEAM-AuthorizeSession` and `GWSEAM-SessionAccessible`, which applies the same ownership rule to a session record the platform tools seam read, with the predicate for the root's client, which refuses a principal that carries a session claim (SLR-22). It also reshapes `GWSEAM-StampActivity` for the qualifying activity and the idle clock in `running` and `idle` (SL-6); the tool approval operations `GWSEAM-RegisterApproval`, `GWSEAM-CancelApproval`, `GWSEAM-ResolveApproval`, and `GWSEAM-AwaitApproval` for the `withdrawn` outcome (SL-3, L29); the terminal operations `GWSEAM-OnSessionTerminal`, `GWSEAM-AfterTerminalWrite`, and `GWSEAM-ExpireSession` for the inbox close before the cascade and the expiry of a session under `sealOnly` or `none` (SL-6, SL-9); the tree recovery operations `GWSEAM-RecoverTree`, `GWSEAM-ReattachNode`, `GWSEAM-StartNode`, and `GWSEAM-FailNode` for a paused session that stays paused after a pod loss (SLR-16); and `GWSEAM-HandleBudgetKeysExpired` for the time that counts toward `BUDGET_KEYS_EXPIRED` (SL-10). It adds the refusal and cancellation of a session's calls through the metering proxy (SLR-23), a crossing that has no operation yet.

### 0146.S2 Child events
- status: Planned
- aliases: [SL-B, SL-8, SL-12, SL-3 (part), SL-4 (part), SL-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0135.S5]

Every child event of the lifecycle, wherever this design states it: "SL-8" and "SL-12", the `suspended` and `resumed` child events of "SL-4", the `end_of_turn` child events with `turn_lost` and `idle_timeout` of "SL-3" and "SL-6", the return to `pending` of the child events a cancelled `await_child_sessions` had taken, and the resend of `ended` and end-of-turn child events, with the parts of the §7.3, §8.8, and §8.10 rows that state them. It needs MD-E of the message delivery design for the child events.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. 0135.S5 adds the operation for child events into the parent's inbox (MD-22), and this step uses it for the resend of SL-12 and for the `turn_lost` and `idle_timeout` child events, and adds no second operation for that crossing. It reshapes `GWSEAM-RecoverTree` and `GWSEAM-ReattachNode` for the resend after a parent's restore (SL-12).

### 0146.S3 Helper runs at a pod release
- status: Planned
- aliases: [SL-3 (part), SL-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0153.S1, 0153.S5]

The cancellation of a helper run or a setup command still running in the session's slot when a suspension or the move to `dormant` releases the pod: "SL-3" **A running helper run.** and the matching sentence of "SL-6", with the part of the §6.2 and §7.3 row that names where the lifecycle states the rule. It needs the cancel pipe and `setupdone` with `setup_cancelled` of 0153.S1 (WM-11) and the mid-session overlay of 0153.S5 (WM-12). It is a step of its own because 0153.S5 depends on 0135.S5, which depends on 0146.S1, so the rule cannot land in 0146.S1. Its tests are the `e2e_kind` cases of "Tests" that cancel a helper run and a setup command at a release.
