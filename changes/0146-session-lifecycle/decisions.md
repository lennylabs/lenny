# Decisions: 0146 Session lifecycle

## Open decisions

The M3 readiness review of 2026-11-19 raised SL-Q3 to SL-Q14, which continue the numbering of SL-Q1 and SL-Q2; its second pass, which closed the requests of other changes, raised SL-Q13 and SL-Q14. Each moves to "Decisions" with the owner's answer as the next L entry.

**SL-Q3. Whether a paused session that keeps its pod under `checkpointPolicy: sealOnly` or `none` counts against the concurrency caps. Open.**

[IC-8](../0131-incremental-checkpoint/change.md#ic-8-checkpoint-policy-interval-and-change-tracker) of `0131-incremental-checkpoint` keeps a suspended session's pod under `sealOnly` and `none` and does not take the release after `maxSuspendedPodHoldSeconds`, which revision 13 states in "SL-1", "SL-4", and "SL-10". "SL-10" and SLR-12 exclude `suspended` and `blocked` from the caps. Read together, a user can suspend sessions and start more, and each suspended session holds a pod or a slot for up to `maxSuspendedSessionSeconds`, 7 days by default, without counting against the user, runtime, or tenant cap. On a tenant-derived runtime the pods come from the tenant's owned pool, so the user crowds out other users of the same tenant; on a shared pool the policy is set by the operator or a platform admin, and the pool's size bounds the total.

- (a) A paused session counts against the caps while its row carries a pod reference: SLR-12 counts the non-terminal sessions outside `dormant`, and outside `suspended` and `blocked` without a pod, and `countedStatePredicate` reads the pod reference for the paused states. This amends SLR-2, under which the state alone decides whether a session counts, and revisits the reasoning of L14 and of "The pod as a row property for every state" in "Alternatives rejected".
- (b) Under `sealOnly` and `none`, a paused session that keeps its pod ends as `expired` after its seal at `maxSuspendedPodHoldSeconds`, as L30 ends an idle one at the idle timeout. SLR-2 holds, and such a suspension lasts 900 seconds by default.
- (c) `POST /v1/sessions/{id}/suspend` refuses a session under `sealOnly` or `none`.
- (d) The pod stays, and a per-tenant cap bounds the paused sessions that hold a pod.

**Recommendation: (a).** It keeps the pod hold that IC-8 promises the runtime author, closes the path with the caps that already exist, and is the recommendation that `0131-incremental-checkpoint` records as D29. It needs the owner to amend SLR-2 and revisit L14, and (b) is the option that keeps both. Whichever option is taken, IC-8's table and the timer table of "SL-10" state the same outcome. It blocks 0146.S1 (SL-1, SL-10) and 0131.S8. The other change is [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md) (D29). (raised by the M3 readiness review, 2026-11-19; sources: 0146-F1, 0146-F3, checkpoint-workspace-C1)

**SL-Q4. What 0146.S1 does to the in-gateway client MCP tools and the other surfaces that 0138.S6 removes. Open.**

0138.S1 depends on 0146.S1, and 0138.S6 removes the client MCP tools, the translators, and the A2A surface last. When 0146.S1 lands, `lenny/interrupt_session`, `lenny/terminate_session`, and `lenny/resume_session` are still registered in `pkg/gateway/platformtools/mcptools/client_tools.go`, and "Non-specification changes" renames them to `suspend_session` and `close_session`, while the banner says those tools move to the MCP adapter. The program allows no transition mode for behavior a member change removes.

- (a) 0146.S1 deletes or stubs the client tools that call the routes it renames, builds no `suspend_session` or `close_session` in Lenny, and drops the generated MCP tools from its `contract` test; 0138.S6 removes what remains.
- (b) 0146.S1 renames the tools as "Non-specification changes" states, and 0138.S6 deletes them later.

**Recommendation: (a).** No code is written for a surface the target state removes, which matches the recommendations of O17 of `0135-message-delivery` and O20 of `0138-rest-only-client-api` on the same question. It blocks 0146.S1's code list and its `contract` test. The other changes are [`0138-rest-only-client-api`](../0138-rest-only-client-api/decisions.md) (O20) and [`0135-message-delivery`](../0135-message-delivery/decisions.md) (O17). (raised by the M3 readiness review, 2026-11-19; sources: 0146-F11, client-api-sessions-C12)

**SL-Q5. How the rollout drain of `0140-runtime-revisions` treats sessions that wait or pause. Open.**

"SL-6" pauses `maxSessionAge` in `dormant`, so a session that alternates between `idle` and `dormant` can outlive `maxSessionAge` in wall-clock time by up to `maxSuspendedSessionSeconds` per pause. `0140-runtime-revisions` defaults `rollout.drainTimeoutSeconds` to the runtime's `maxSessionAge`, states that "No session outlives `maxSessionAge`", reports only suspended sessions before `Contracting`, and ends "the remaining ones" at the drain timeout without saying in which state an `idle` or `dormant` session ends. This change's instruction to 0140 said "no change".

- (a) 0140's `Draining`, the D7 move, and the options report name `idle`, `dormant`, `suspended`, and `blocked`, `drainTimeoutSeconds` stops relying on `maxSessionAge` as a wall-clock bound, and 0140 states whether the drain ends a waiting session, and in which state, or moves it to the new revision at its next resume.
- (b) `maxSessionAge` keeps running in `dormant`, which changes "SL-6", "SL-10", and the instruction that the timers apply to `dormant` as they apply to an idle-suspended session today.

**Recommendation: (a).** `dormant` replaces the idle suspension, whose `maxSessionAge` pauses today, and the drain is the mechanism that reads the bound. It blocks the instruction to 0140 in "Instructions for reviewers of other designs" and 0140's rollout step. The other change is [`0140-runtime-revisions`](../0140-runtime-revisions/decisions.md). (raised by the M3 readiness review, 2026-11-19; sources: 0146-F13)

**SL-Q6. The `turn_end.status` of a cut turn, and what the turn interrupt writes to `last_turn_end`. Open.**

"MD-20" of `0135-message-delivery` gives `turn_end.status` the values `completed`, `failed`, `interrupted`, and `runtime_exit`. R3 of `0138-rest-only-client-api` reports `cut` with its cause. This change records `last_turn_end: cut` for a suspension, an idle cut, and a pod loss, reports `turn_end` with `error: "idle_timeout"` or `error: "turn_lost"`, and records `completed` at a close only when `last_turn_end` is `message`. R19, the turn interrupt, ends the turn `interrupted` and leaves the session `idle`, and nothing states what it writes to `last_turn_end`, so a close after an interrupt can record `completed` with the interrupted output.

- (a) `turn_end.status` gains `cut`, with `error` naming the cause, R3 uses the same set, and R19 writes `last_turn_end: cut`, so a close after an interrupt records `cancelled` and R16 refuses a continuation until a later turn completes.
- (b) As (a), except that R19 writes `last_turn_end: message`, so a close records `completed` and R16 accepts the interrupted turn.

**Recommendation: (a).** One vocabulary across the three changes lets a client read a cut from the event alone, and an interrupted turn did not finish its work, which the close rule reads. It matches O12 (a) of `0135-message-delivery` and O18 (a) of `0138-rest-only-client-api`. It blocks 0146.S1 ("SL-1" **Row fields**, "SL-3", and "SL-7"), 0135.S1, 0135.S4, and 0138.S1. The other changes are [`0135-message-delivery`](../0135-message-delivery/decisions.md) (O12) and [`0138-rest-only-client-api`](../0138-rest-only-client-api/decisions.md) (O18). (raised by the M3 readiness review, 2026-11-19; sources: client-api-sessions-C6)

**SL-Q7. Whether the pod hold of a suspended session extends past `maxSuspendedPodHoldSeconds` for exports already running. Open.**

"MD-18" **Suspension** of `0135-message-delivery` releases the pod only after the exports already started end, at most `exportTimeoutSeconds` (900 s) after the hold ends. The state table of "SL-1" and "SL-4" **Pods and timers.** release the pod when the hold ends. Both changes edit §6.2.

- (a) "SL-1" and "SL-4" state the extension for exports already started, bounded by `exportTimeoutSeconds`.
- (b) At the end of the hold the gateway fails running exports with `suspend_hold_expired`, as "MD-18" already does for a suspension that releases the pod at once, and the runtime emits the files again after the resume.

**Recommendation: (a).** "MD-18" is the more specific text, and failing an export at the end of the hold discards an upload that is nearly done. It matches O11 (a) of `0135-message-delivery`. It blocks 0146.S1 (SL-1, SL-4, SL-10) and 0135.S4. The other change is [`0135-message-delivery`](../0135-message-delivery/decisions.md) (O11). (raised by the M3 readiness review, 2026-11-19; sources: client-api-sessions-C8)

**SL-Q8. How a session under `checkpointPolicy: sealOnly` or `none` resumes after it loses its pod. Open.**

IC-8 of `0131-incremental-checkpoint` states that a pod failure or an eviction resumes such a session with `resumeMode: conversation_only` and `workspaceLost: true` from the §4.4.5 minimal-state record. That record is written only when an eviction checkpoint cannot be persisted, which these policies never attempt, so §7.3 step 3e, "SL-6" of this change, WM-13 of `0153-workspace-materialization`, and D19 of `0140-runtime-revisions` send such a session through the snapshotless rebuild, which reruns setup commands and carries no conversation state.

- (a) Under `sealOnly` and `none`, the preStop path and the pod-failure path write the minimal-state record without a checkpoint, so IC-8's statement holds, and "SL-6" names the case.
- (b) IC-8 adopts the snapshotless rebuild for these policies and drops `conversation_only` from its table.

**Recommendation: (a).** It keeps the conversation that a runtime author who chose `sealOnly` expects to survive, and it matches D30 (a) of `0131-incremental-checkpoint`. It blocks 0146.S1 (SL-6) and 0131.S8. The other changes are [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md) (D30) and `0153-workspace-materialization` (O18). `0140-runtime-revisions` records no counterpart, because its "RV-6" sends a session with neither a checkpoint nor a minimal-state record through the snapshotless rebuild, which holds under either option. (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C4)

**SL-Q9. The outcome of a paused session on a pod quarantined with `sessionDisposition: fail`. Open.**

FE-1 of `0128-forensics-evidence-preservation` fails a quarantined pod's sessions with the existing reason `orphan_pod_terminated`, so that the tenant receives no signal that a quarantine happened (its decision 6). After this change, a paused session whose pod is lost stays paused with its pod reference cleared (SLR-16, the §10.1.4 row) and never reaches that terminal state, so a quarantine on a pod that holds a `suspended` or `blocked` session produces an outcome that only a quarantine produces.

- (a) Under `fail`, a paused session on the quarantined pod takes the lost-pod outcome of SLR-16: it stays paused with its pod reference cleared, and its resume restores it elsewhere.
- (b) A paused session fails with `orphan_pod_terminated`, and 0128 records the signal as an exception under its decision 6.

**Recommendation: (a).** It reuses an existing outcome and keeps decision 6 of 0128 intact. It blocks 0146.S1 (the §10.1.4 row) and 0128's quarantine step. The other change is [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/decisions.md) (O9). (raised by the M3 readiness review, 2026-11-19; sources: checkpoint-workspace-C6)

**SL-Q10. Whether 0146.S1 is split. Open.**

0146.S1 carries "SL-1" to "SL-7" and "SL-9" to "SL-11": about 25 specification rows, a store migration, the routes, the watchdog, the cascade, and the adapter. In the tree, 23 non-test Go files under `pkg/` and `cmd/` read `StateRunning`, 19 read `StateSuspended`, and 17 read `StateInputRequired`. Revision 13 moves only the helper-run cancellation into 0146.S3. Steps of other changes depend on 0146.S1 as a whole: 0122.S1, 0124.S3, 0124.S4 (part), 0135.S5, 0138.S1, and 0139.S1.

- (a) Split 0146.S1 into states, the store migration, and the caps ("SL-1", "SL-10"), which shares the branch of 0135.S1; suspension and tree suspension ("SL-3", "SL-4", "SL-5"); idle and dormancy ("SL-6"); and close and cascade ("SL-7", "SL-9"), each listing the packages it touches, and re-point each edge that other changes hold on 0146.S1 to the step it needs.
- (b) Keep 0146.S1 as one step on one branch with 0135.S1.

**Recommendation: (a).** Smaller branches are reviewable, and the edges of other changes then wait only for the part they read. Splitting needs edits to the steps of 0122, 0124, 0135, 0138, and 0139. It blocks 0146.S1. (raised by the M3 readiness review, 2026-11-19; sources: 0146-F15)

**SL-Q11. What DLQ overflow does to child events and to messages held without a TTL. Open.**

"SL-5" keeps the DLQ's `maxDLQSize` cap for messages held for a `dormant`, `suspended`, or `blocked` session, and states that a child event never expires while its parent lives (SLR-14). §7.2's dead-letter table drops the oldest entry on overflow and orders entries by acceptance time plus the TTL. A parent in `awaiting_client_action` or paused, whose children keep running, can therefore lose its oldest child events, an `ended` event among them, and a held message that takes no TTL has no defined order.

- (a) The DLQ evicts held child events last: it evicts other held messages first, never evicts an `ended`, `end_of_turn`, or `awaiting_client_action` child event, and orders a message held without a TTL by its acceptance time.
- (b) At the cap the DLQ refuses new messages from senders other than the gateway's child events, with a refusal the sender sees, and evicts nothing that SLR-14 protects.
- (c) Keep the drop and weaken SLR-14 to "while the DLQ has room".

**Recommendation: (a).** It keeps SLR-14 and the exactly-once delivery of each child event, and it follows the protection that O13 of `0135-message-delivery` recommends for the inbox. It blocks 0146.S1 (the dead-letter rows of §7.2). The related decision is [`0135-message-delivery`](../0135-message-delivery/decisions.md) O13, which covers the inbox cap. (raised by the M3 readiness review, 2026-11-19; sources: 0146-F17)

**SL-Q12. Which gates resume admission runs, and how a tree resume is admitted. Open.**

§11.1 **Resume admission.** runs "the check that session creation runs", described there as the concurrency caps over the tenant, the user, and the runtime. Session creation in the tree runs further gates (`createAdmissionGates` in `pkg/gateway/core/session/sessionserver/create.go`: the store-availability check, the lifecycle fence of §12.8, the admission-rate limit, the policy chain, and environment admission, with the pool-drain gate that `0161` added to create-and-start). This change makes resume admission frequent, at every wake from `dormant` and at every descendant of a tree resume, and §11.1 **Concurrent admissions.** lets concurrent admissions overshoot a cap by the number in flight.

- (a) Resume admission runs the caps alone, as §11.1 describes it today.
- (b) Resume admission runs every creation gate.
- (c) Resume admission runs the caps, the store-availability check, and the lifecycle fence, and not the admission-rate limit, the policy chain, environment admission, or the pool-drain gate.

Separately, a tree resume either admits its descendants one at a time, so it cannot overshoot a cap, or admits them concurrently under §11.1 **Concurrent admissions.**

**Recommendation: (c), with a tree resume that admits its descendants one at a time.** A resume needs the stores and must respect a tenant's deletion fence, while a wake from `dormant` is not a new session and would otherwise spend the creation rate budget. It blocks 0146.S1 (SL-10, the §11.1 row). (raised by the M3 readiness review, 2026-11-19; sources: 0146-F18)

**SL-Q13. What the slot's runtime does while the disk watch's trip takes its best-effort checkpoint, and how that checkpoint starts and queues. Open.**

"SL-2" **Disk watch [design].** ends a session `failed` with `workspace_size_limit` after a best-effort checkpoint under `full`, and defers the checkpoint's start and deadline to IC-1 and IC-8 of `0131-incremental-checkpoint`, which do not yet state them or what the runtime does while the checkpoint runs. Every checkpoint of 0131 starts from the gateway's `CheckpointStart` and waits in the pod-wide queue under the attempt deadline, 300 seconds by default. On a pod with several slots, and on a volume that no project quota bounds, a session can write past the threshold while its checkpoint waits, and the kubelet then evicts the pod and ends every slot's session, which the trip exists to prevent.

- (a) The adapter ends the slot's runtime processes with the release's first End, the gateway then starts the best-effort checkpoint of the quiesced volumes as a `disk_watch` trigger that skips the queue and runs under a short time cap, and the session ends `failed`. "SL-2" states the order by reference to IC-1 and IC-8.
- (b) As (a), with the slot frozen in place of ending its runtime processes.
- (c) The checkpoint keeps its order and queues like any other trigger with a short deadline, and is abandoned when use passes a second threshold.
- (d) Accept the eviction risk and state it in "Limits" of `0118-agent-pod-layout`.

**Recommendation: (a).** A runtime that has ended cannot write past the threshold, and an End is already the release's first step. It blocks the `failed` transition of "SL-2" in 0146.S1, 0118.S4, and 0131.S1 and 0131.S8. The other changes are [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md) (O12) and [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md) (D36). (raised by the M3 readiness review, 2026-11-19; requested by change 0118; sources: 0118-F5, checkpoint-workspace-C5)

**SL-Q14. What "Limits" and the lifecycle page state about the egress halt on a pool with `egressProfile: internet`. Open.**

Step 1 of "SL-3" halts the slot's egress through `netagent` when a suspension cannot take hold, and "Limits" names one exception, the pool without slot attribution. `0127-egress-and-git` keeps raw sockets on a pool with `egressProfile: internet`, so a slot's traffic there does not pass `netagent`, and the halt stops proxied connections alone while `HaltSessionEgress` answers `halted`. A runtime on such a pool that does not stop at a suspension keeps calling a model provider or an operator service on a direct socket until the pod is released, or under `sealOnly` or `none` until the session resumes or ends.

- (a) "Limits", "SL-3", and the session lifecycle page name the pool with `egressProfile: internet` beside the pool without slot attribution, and the halt's outcome on such a pool reports that it covered proxied connections alone, as CR-4 of `0124-connector-removal` states it.
- (b) The halt is enforced outside `netagent` on such pools, which needs a per-slot network control that no member change designs, and "Limits" stays as written.

**Recommendation: (a)**, as `0124-connector-removal` recommends, because no member change designs a per-slot control outside `netagent`. It blocks the "Limits" entry and the documentation row of 0146.S1, and 0124.S3. The other change is [`0124-connector-removal`](../0124-connector-removal/decisions.md) (O19), which also names `0127-egress-and-git`. (raised by the M3 readiness review, 2026-11-19; requested by change 0124; sources: 0124-F2)

## Decisions

Settled by the owner on 2026-11-11, in a review of the combined lifecycle of a parent and its children after revision 9 of the message delivery design. The rows quote the owner.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L1 | Whether child events share the parent's inbox. | The owner asked: "child events/messages use the parent's inbox in the gateway and the inbox is shared across all children as well as the client, right?" They do. One inbox per session serves every sender, and the loss on a restore that the review found comes from O4, which leaves a resend to the sender. The gateway therefore acts as the sender of terminal child events ("SL-12"). | SL-12 |
| L2 | What a client's suspension covers. | "shouldn't the solution be to suspend the entire tree, not just the parent? Your solution could/should be built on top as a safety net, but I think that if the client wants to suspend a session they mean everything, not just the parent (the client may or may not know children are running)". A suspension covers the session's subtree, and holding child events while a session is paused is the safety net. | SL-4, SL-5 |
| L3 | Whether a client message resumes a suspended tree. | "Ok to client messages resuming". A message from the root's client resumes the suspended session and its tree, as `POST /resume` does. | SL-5 |
| L4 | What happens to a tree whose suspended root ends. | "(a)". The descendants stay `blocked`, and each ends at its own `maxSuspendedSessionSeconds` or at `cascadeTimeoutSeconds`, whichever comes first, unless the policy is `close_all`. | SL-9 |
| L5 | The name of the route that suspends. | "ok on interrupt->suspend". `POST /v1/sessions/{id}/interrupt` becomes `POST /v1/sessions/{id}/suspend`, with the MCP tool, and the delivery intent keeps the word `interrupt`. | SL-4 |
| L6 | Whether `detach` stays. | "I don't think `detach` should be an option", and "retire `detach`". | SL-9 |
| L7 | A child in `awaiting_client_action`. | "ok". The parent receives a child event marked recoverable, and its `message_child_session` resumes the child as a client's resume does. | SL-8 |
| L8 | Whether a suspension ends the open turn. | "ok" to the recommendation. A suspension stops the open turn, and on resume a session whose turn was cut receives a notice that opens a turn, so the work continues. | SL-3 |
| L9 | What a close records. | The owner first answered "option (a). Children can either be stopped explicitly or they are closed when the parent session is closed", and then asked: "My understanding is that `POST /terminate`, `DELETE`, close_child(cancel: false), close_child(cancel: true) all have the same ultimate effect ... what's the benefit of tagging the session one way or another? ... should we have one `close_child` with an argument or two separate tools?" After the review showed that only readers outside the gateway read the difference, the owner chose "(a)": the gateway derives the state from what happened to the work, `close_child_session` takes no argument, a close by the same rule replaces `POST /terminate`, and `DELETE` stays an explicit cancel. | SL-7 |
| L10 | The cascade's names. | "ok". `cascadeOnFailure` becomes `onParentEnd`, with `close_all` and `let_finish`. | SL-9 |
| L11 | An idle parent's pod while its children work. | "ok". An `idle` session keeps its pod while any descendant is `running`. | SL-6 |
| L12 | A parent suspending a child. | "ok" to no tool in v1. | SL-4 |
| L13 | Separate paused states. | "I think we should have different `suspended` states, given that how a session entered `suspended` matters later on. I'd like a clean state machine." The names followed: "i like suspended and blocked." | SL-1 |
| L14 | Whether holding a pod is a state. | "ok" to option (c), after the owner asked for more context. `idle` splits into `idle`, which holds its pod, and `dormant`, which released it. The paused states keep a row property for the pod, because nothing reads it for a paused session except the path a resume takes. | SL-1, SL-10 |

Settled by the owner on 2026-11-11 after the adversarial review of revision 1. The rows quote the owner's answers to the questions the review raised, and revision 2 applies them. The owner accepted the review's other fixes, which "Review history" lists.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L15 | What happens to a runtime that does not stop at a suspension. | "1: ok" to the recommendation. A runtime that does not declare `interrupt` receives no signal, so the gateway seals the session, checkpoints the workspace, and releases the pod at once, as an idle suspension does today. A runtime that declares `interrupt` and does not acknowledge `interrupt_request` within its deadline is treated the same way at the deadline. Revision 7 restates the owner's answer, which named integration levels, in the capability terms of RP D3. Frames a paused session writes open no turn and change no state. An acknowledged suspension keeps today's pod hold. | SL-3 |
| L16 | A session whose turn a pod loss cut. | The owner asked to "elaborate on why both", and then answered "2. ok" to option (c). When the restore commits, the session receives the resume notice, which opens a turn, and its client receives a report that the turn was lost and that the session was told to continue: an `end_of_turn` child event with `error: "turn_lost"` for a parent, and `turn_end` with the same error for a top-level client. | SL-3, SL-8 |
| L17 | A turn that goes silent. | "i think the proper solution would be to move sessions to idle when there has been no activity at all on the wire (no message either direction or progress update) after a configurable period of time. I thought that was already in place but I may be mossing something. Then the rule for runtimes should be "if you don't want to be put on idle state in the middle of a long-running task, make sure to send activity updates or streaming messages on a regular basis"". §6.2 already runs the idle clock in `running`, and revision 1 had moved it to `idle` alone. The owner then answered "3. ok on both counts" to the two details: when the clock fires on an open turn, the turn is cut and the session becomes `dormant` at once, because it has already been silent for a whole window, and a pending approval or question counts as activity until it is answered or expires. | SL-6 |
| L18 | Suspending a recovering session. | "4: ok". A top-level session in `resume_pending`, `resuming`, or `awaiting_client_action` can be suspended. The suspension is recorded on its row, its descendants are `blocked`, and the session becomes `suspended` when its restore commits. | SL-4 |
| L19 | An end-of-turn child event lost on a parent's restore. | "5: ok" to option (a). The gateway resends each end-of-turn child event the parent consumed after its checkpoint, from the child's transcript, as it resends `ended` events from `session_tree_archive`. | SL-12 |
| L20 | The budget keys of a long-lived dormant root. | "6: ok". The gateway re-arms the TTL of the tree's budget keys when the root enters `dormant`, as it does when the root enters or leaves `suspended`. | SL-10 |
| L21 | Closing a session whose client tool calls are pending. | "7: ok". The turn that returned the calls is unfinished, so the close records `cancelled`. | SL-7 |
| L22 | How the paused states appear on A2A and MCP Tasks. | "8: ok". Neither protocol has a paused state, so `suspended` and `blocked` map to `working` with `metadata.paused: true`. | SL-11 |

Settled by the owner on 2026-11-11 after a re-review of revision 2. The owner answered "ok" to each recommendation below, and accepted the re-review's other fixes, which "Review history" lists.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L23 | When the tree's budget keys are re-armed for a `dormant` root. | "ok". The gateway re-arms them when the root enters and when it leaves `dormant`, as §8.3 does for `suspended` today, and §8.3's clause on the time a root spends outside `suspended` covers `dormant` as well. It extends L20. | SL-10 |
| L24 | A suspension that cannot take hold. | "ok". The seal answers "not sealed" while a platform or connector call is in flight, and a failed checkpoint keeps the pod today. When a suspension cannot take hold, the gateway refuses the session's new LLM-proxy, platform, and connector calls and cancels those in flight, then seals the session and releases the pod. It retries a failed checkpoint, and at `maxSuspendedPodHoldSeconds` it forces the release, and the session resumes from its last good checkpoint. A runtime's calls to a provider outside the proxy stop only at the pod release: these were direct-mode calls when the owner settled L24, and they are the calls of [LU-10](../0133-llm-upstreams-and-cost-budgets/change.md#lu-7-calls-outside-the-gateway) since `0133-llm-upstreams-and-cost-budgets` removed direct mode. Superseded in part on 2026-11-15 by [`0124-connector-removal`](../0124-connector-removal/change.md): connector calls leave with connectors, and step 1 also halts the slot's egress (its CR-4, which the owner accepted as O9 with "ok"), so calls outside the proxy stop at the suspension except on a pool without slot attribution. | SL-3 |
| L25 | Where a session lands under a tree resume that admission refuses in part. | "ok". A session lands `blocked` under its nearest paused ancestor, whether that ancestor is `suspended` or `blocked`. A tree resume that admission refuses for one session leaves that session's subtree `blocked` under it, and the subtree resumes when the session does. | SL-4 |
| L26 | What a turn stopped by a suspension consumes. | "ok". The end-of-turn `message` that closes the stopped turn consumes only the message ids it lists, and every other `delivered` message of the session returns to `pending`, so the resume delivers it again. The message delivery design records the same rule as B19. | SL-3 |
| L27 | The choices of revision 2. | "ok". The owner accepted the `idle_timeout` report without a resume notice after an idle cut, and the absence of the notice and the report after a restore that delivers the cut turn's causing message again. | SL-3, SL-6 |

Settled by the owner on 2026-11-11 after a regression review of revision 3. The owner answered "b, go ahead", and accepted the review's fixes, which "Review history" lists.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L28 | A question pending when a suspension stops the turn. | "b, go ahead". A question the session raised through `ask_question` or a `request` with `kind: "question"`, and that is pending when a suspension stops its turn, is withdrawn. The gateway expires it as §9.2 "Elicitation Timeout Semantics" item 6 expires a session's elicitations when the session leaves its pod, extended to every suspension that stops a turn, whether or not the pod is released yet. The client's view drops it, a later answer is refused as not found, and the resume notice names each withdrawn question so the runtime may ask again. The rejected option kept the question open and delivered a later answer as a held message, which would reverse item 6, need `maxElicitationWait` to pause, and add a second answer path. | SL-3 |

Settled by the owner on 2026-11-11 after revision 4 showed that a pending approval stayed open while a pending question was withdrawn.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L29 | A tool-use approval pending when a suspension stops the turn. | "Withdraw approvals the same way". A pending approval, from a `request` with `kind: "approval"` or an approval-gated `tool_call` that the gateway holds, is withdrawn as L28 withdraws a question. It leaves the client's view, a later decision is refused as not found, its timeout and the unattended policy's answer no longer fire, and the resume notice names it so the runtime can ask again if it retries the tool. The rejected option kept the approval open with a paused timeout and held the decision until the resume, the machinery L28 rejected for questions. | SL-3 |

Settled by the owner on 2026-11-13, in the cross-design review of that day.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L30 | What the idle clock does to a session whose checkpoint policy takes no checkpoint. | "(a)". A session whose effective `checkpointPolicy` is `sealOnly` or `none` stays `idle` on its pod, and the idle timeout ends the session instead of making it `dormant`. | SL-6, SL-3 |
| L31 | The `type: mcp` and service-mode exceptions to dormancy. | Moot under the owner's removal of non-agent runtimes ([`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md), D1). Every session can become `dormant` subject only to its checkpoint policy. | SL-6 |

Settled by the owner on 2026-11-13, in the answers to the consolidated list of open decisions of that day. The read-only checks of 2026-11-13 raised SL-Q1 and SL-Q2, and L32 and L33 settle them.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L32 | How an `idle` or `dormant` session appears to an MCP Tasks client (SL-Q1). | "ok", after the owner asked whether one task per call is how MCP Tasks are used. One MCP task per message a client sends, which completes at the end of the turn that consumes the message. `idle`, `dormant`, and `awaiting_client_action` no longer map to `input_required`, which is left to elicitations. The answer also settles MP O7 of [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md#mp-4-tasks), which owns the mapping; under the owner's choice of the same day to move every client protocol adapter out of Lenny, the mapping moves with the MCP adapter, and `0138-rest-only-client-api` governs that move. | SL-11 |
| L33 | Where a resume or a restore lands when the runtime handshake refuses it with a reason that is not retryable (SL-Q2). | "ok" to the recommendation: `failed`. The same answer settles O13 of [`0139-runtime-protocol`](../0139-runtime-protocol/decisions.md#open-decisions), whose RP-1, RP-2, and §7.3 row now send such a session to `failed`. | SL-2 |
| L34 | The end of a session whose slot volume passes the disk watch's threshold. | "ok": confirmed as written. A best-effort checkpoint when the session's checkpoint policy and the slot's capture budget allow it, then `failed` with the reason `workspace_size_limit` and the code `WORKSPACE_SIZE_LIMIT_EXCEEDED`, and the release of that slot alone. | SL-2 |

The rows below keep the questions as they were raised, as the record of SL-Q1 and SL-Q2.

| ID | Question | State and recommendation as raised | Where |
| :-- | :-- | :-- | :-- |
| SL-Q1 | How an `idle` or `dormant` session appears to an MCP Tasks client. | Settled as L32. When raised: "SL-11" and MP-4 of [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md#mp-4-tasks) give a session one task whose ID is the session's ID, and an `idle` or `dormant` session reads as `input_required`. Under the Tasks extension `input_required` carries `inputRequests` that the client answers with `tasks/update`, and an idle session has none, as finding 6 of the REST-only spike of 2026-11-13 states. Recommendation: one task per message a client sends, which completes at the end of the turn that consumes the message, so that a waiting session has no task in `input_required` and that status is left to elicitations. MP-4 owns the projection and decides with the owner. | SL-11 |
| SL-Q2 | Where a resume or a restore lands when the runtime handshake refuses it with a reason that is not retryable. | Settled as L33. When raised: "SL-2" sends every non-retryable failure to `failed`, and RP-1 of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-1-handshake) and its §7.3 row send such a refusal to `awaiting_client_action`, where the client can wait for a revision with a fixed image. Recommendation: `failed`, because this design owns the states, a refusal that is not retryable recurs on every pod of the pod set, and `awaiting_client_action` would hold the session and its caps until `maxAwaitingClientActionSeconds`. Until the owner answers, this design keeps `failed`, and RP-1 keeps its text. | SL-2 |

**Gateway seam operations (0117, decision 8).** Settled by the owner on 2026-11-17 in [0117](../0117-gateway-package-layout/decisions.md), which raised the conflict between its seam interface page and this change: a member change that removes, reshapes, or adds a mechanism behind a gateway seam operation edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` in the step that changes the mechanism. In this change that is 0146.S1 and 0146.S2, whose entries in `steps.md` name the operations.

Settled by the owner on 2026-11-23 in [`0170-budgets`](../0170-budgets/decisions.md#decisions), from the review of 0166 and 0170 with the branch's edits to the other v1 changes.

| ID | Question | Decision | Where |
| :-- | :-- | :-- | :-- |
| L35 | What a budget's exhaustion does to a running session and its descendants. | The owner answered "budget wins", then "maybe hitting the session budget should either put every session in the tree to sleep or none? Putting just some is not a very good behavior IMO", then asked whether an existing state or a delay could avoid releasing pods that a raise would wake again, and answered "ok" to reusing `suspended` and `blocked` (decision 42 of 0170). A budget suspends the session with `suspension_cause: budget` and blocks its subtree, holds the pods for `budgets.suspendPodHoldSeconds`, resumes the subtree when the limit gains room, and checkpoints and releases the pods after the hold. | SL-1, SL-2, SL-4, SL-6, SL-10 |
| L36 | Budget keys once carves replace the tree budget. | "R9: ok" (decision 43 of 0170). Budget keys carry no TTL, and the re-arming of L20 and L23, `delegation.budgetKeyTTLSeconds`, and `BUDGET_KEYS_EXPIRED` retire. | SL-6, SL-10 |
| L37 | What a budget does to a paused session that holds a pod, and to a client's suspension of a budget-suspended session. | "S4: ok" on 2026-11-24, from the second review of the 0166 branch (decision 51 of 0170). A compute tick refused on a session paused for any cause that still holds its pod starts the budget hold, and a client's suspension of a budget-suspended session sets `suspension_cause: client`, after which only the client resumes it, without restarting or extending the hold. 0170 owns the trigger, which takes effect at the first refused charge of any meter the limit counts. | SL-2, SL-4, SL-10 |
| L38 | Under decision 22 of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/decisions.md#decisions), settled by the owner on 2026-11-24, Lenny removes its LLM proxy, and a runtime's model calls reach an operator's LLM gateway through egress. SLR-23 keeps the refusal of platform calls and the egress halt, which now stops a session's model calls, and 0146.S1's refusal in the proxy lasts until 0133.S2 removes the proxy. SL-6 counts `llm_request_completed` and no longer counts proxied chunks or usage reports, because Lenny sees no model call, and the owner accepted that model calls give no idle evidence ("I'm ok with 2, 3, 4, 5, and 6 above"). |
