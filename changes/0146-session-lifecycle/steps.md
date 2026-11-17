# Steps: 0146 Session lifecycle

## Validation owed

- Whether each reference harness ends its turn promptly on `interrupt_request` and what it writes when it does, which decides how often "SL-3" falls back to the release at the deadline.
- How each reference harness treats the resume notice, which decides whether its text needs to vary by runtime.
- Whether each reference runtime reports `busy` on heartbeats through a long tool run inside the pod, which decides how often "SL-6" cuts a working turn.
- Whether models leave a boolean tool argument unset more often than they set it, which "SL-7" **One tool.** assumes.
- How many sessions an operator's dashboards count by state today, which decides how the migration of stored `suspended` rows is announced.

## Steps

### 0146.S1 States and their rules
- status: Planned
- aliases: [SL-A, SL-1, SL-2, SL-3 (part), SL-4 (part), SL-5, SL-6 (part), SL-7, SL-9, SL-10, SL-11]
- lanes: [specification, code, documentation]

The states and their rules: "SL-1" to "SL-7" and "SL-9" to "SL-11", with every specification change above except those 0146.S2 takes. Where those parts state a child event, 0146.S2 lands that statement, and this step lands the session's own reports: `turn_end` with `turn_lost` and `idle_timeout`, and the resume notice. This step owns the rows it shares with the message delivery design, the §28.5.3 `interrupt_request` row with §4.7's `Interrupt` RPC and the TTL rows of the §7.2 dead-letter table, and that design's rows point to it. Until MD-E lands, its text names the end-of-turn frame by its specification name, `response`, the delegation tools by their specification names, `lenny/delegate_task`, `lenny/cancel_child`, and `lenny/await_children`, and its qualifying activity takes the frame names of MD-E when MD-E lands. The egress halt of step 1 of "SL-3" lands with CR-C of [`0124-connector-removal`](../0124-connector-removal/steps.md#steps), which depends on this step.

It has no dependency. It lands with MD-A of the message delivery design, in one proposal or in two applied together, because MD-A's delivery paths name the new states and this step's transitions read MD-A's turn accounting.

### 0146.S2 Child events
- status: Planned
- aliases: [SL-B, SL-8, SL-12, SL-3 (part), SL-4 (part), SL-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0135.S5]

Every child event of the lifecycle, wherever this design states it: "SL-8" and "SL-12", the `suspended` and `resumed` child events of "SL-4", the `end_of_turn` child events with `turn_lost` and `idle_timeout` of "SL-3" and "SL-6", the return to `pending` of the child events a cancelled `await_child_sessions` had taken, and the resend of `ended` and end-of-turn child events, with the parts of the §7.3, §8.8, and §8.10 rows that state them. It needs MD-E of the message delivery design for the child events.
