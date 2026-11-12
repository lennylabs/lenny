# Session lifecycle

Status: design for owner review, revision 5, 2026-11-11. Nothing here is built or staged. It reads against [`message-delivery-design.md`](message-delivery-design.md) revision 14, [`delegation-availability-design.md`](delegation-availability-design.md) revision 8, [`client-tools-design.md`](client-tools-design.md) revision 4, [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 12, and [`security-oversight-design.md`](security-oversight-design.md) revision 47. It is built from the owner's review of 2026-11-11 of how a parent's and a child's lifecycles combine, which followed revision 9 of the message delivery design and the adversarial review of that revision. An adversarial review read revision 1 on 2026-11-11, and revision 2 applies its fixes and the owner's answers of the same day to the questions it raised, as L15 to L22. A re-review read revision 2 the same day, and revision 3 applies its findings and the owner's decisions L23 to L27. A regression review read revision 3 the same day, and revision 4 applies its findings and the owner's decision L28. Revision 5 applies the owner's decision L29 of the same day. Every decision the reviews raised is settled, and "Decisions" records each with the owner's words. It stages no specification edit. The changes go through `change-proposal` after review. Parts of the design carry the label SL-n, requirements carry SLR-n, and decisions carry L-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-11. |
| **[design]** | What this design requires. Nothing implements it. |
| **[unverified]** | A claim the pages and code read do not state. "Validation owed" lists each one. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Revision 9 of the message delivery design made a child session behave as a top-level session whose client is its parent. A child no longer ends when it answers: it ends its turn and waits for its client's next message, as a top-level session does. That change reaches the session state machine in ways revision 9 did not state, and the review of 2026-11-11 found them:

- A session that waits for its client is `running` today, and nothing in its state says that no turn is open. Revision 9 removed `input_required`, the only state that said a session was waiting.
- `suspended` covers an interrupt the runtime acknowledged, an interrupt that timed out, and an idle suspension, told apart by a reason field on the session row ([§6.2](../spec/06_warm-pod-model.md#62-pod-state-machine)) **[source]**. What may wake the session and what a resume restores depend on that field, so the state hides lifecycle meaning.
- Any message resumes a `suspended` session, "whatever its `delivery` value and whichever source sent it" ([§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model), delivery path 6) **[source]**. A child's message would therefore wake a parent its client had suspended.
- An interrupt "does NOT cascade" to children, and the specification leaves it to the runtime to pass one on (§6.2) **[source]**. Under revision 9 a runtime can only do that if its model calls a tool, so a client that suspends a session leaves its children working.
- A child closed by its parent has no defined terminal state or result. `POST /terminate` records `completed` and `DELETE` records `cancelled` ([§15.1](../spec/15_external-api-surface.md#151-rest-api)) **[source]**, and nothing reads the difference inside the gateway.
- When a parent ends, `cascadeOnFailure` decides its children's fate ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)) **[source]**. Its `await_completion` and `detach` values assume a child that ends by itself, and a child that now waits for a client that no longer exists would wait until `cascadeTimeoutSeconds`.
- A parent that waits for its children ends its turn. The rule that keeps a parent from idle suspension counts only an `await_children` call as activity (§6.2) **[source]**, so a parent whose children work would release its pod and pay a restore for each child event.

This design states one session lifecycle that a top-level session and a child share, with the transitions, the suspension of a tree, the close rule, the cascade when a parent ends, and the admission rules over the new states.

## Scope

**Added:**

- The states `idle`, `dormant`, and `blocked` ("SL-1").
- Tree suspension: suspending a session suspends its subtree ("SL-4", L2), and a recovering top-level session can be suspended ("SL-4", L18).
- A suspension that releases the pod at once when the runtime does not acknowledge it, after the gateway refuses and cancels the session's model, platform, and connector calls, and the rule that frames from a paused session open no turn ("SL-3", L15, L24).
- A resume notice that restarts the work a suspension or a pod loss cut, and a report to the session's client that a pod loss cut the turn ("SL-3", L8, L16).
- The close rule, which derives `completed` or `cancelled` from what happened to the session's work ("SL-7", L9, L21).
- Child events for a child in `awaiting_client_action` and for a child the root's client suspends directly ("SL-8", L7).
- Redelivery of `ended` and end-of-turn child events after a parent's restore ("SL-12", L19).

**Changed:**

- `POST /v1/sessions/{id}/interrupt` becomes `POST /v1/sessions/{id}/suspend`, and the MCP tool `interrupt_session` becomes `suspend_session` ("SL-4", L5).
- `POST /v1/sessions/{id}/terminate` becomes `POST /v1/sessions/{id}/close` under the close rule, and the MCP tool `terminate_session` becomes `close_session` ("SL-7", L9).
- Which messages wake a waiting or paused session, and how long a message held for one lives ("SL-5", L3).
- The idle-suspension rule, which becomes the move to `dormant` from `idle`, or from `running` with the open turn cut, and which keeps an idle parent's pod while a descendant works ("SL-6", L11, L17).
- The `interrupt_request` timeout, at which the adapter no longer writes a session state ("SL-3").
- `cascadeOnFailure` becomes `onParentEnd`, with the values `close_all` and `let_finish` ("SL-9", L10).
- The concurrency caps, the resume admission check, and the budget-key re-arm, restated over the new states ("SL-10", L20).
- The MCP Tasks and A2A state mappings, with a `paused` metadata flag for the paused states ("SL-11", L22).

**Removed:**

- The `input_required` sub-state, which revision 9 of the message delivery design removes with `lenny/request_input`, and every specification rule that names it ("SL-11").
- The suspension-reason field of the session row ("SL-1").
- The `detach` cascade value (L6).
- The subtree deadlock detector, its `deadlock_detected` event, `maxDeadlockWaitSeconds`, and `DEADLOCK_TIMEOUT`, because no wait on another session remains for it to detect ("SL-8").

**Out of scope:**

- What a message delivers and the frames that carry it, which [`message-delivery-design.md`](message-delivery-design.md#md-22-parents-and-children) states in "MD-22" and "MD-24".
- A tool with which a parent suspends a child. v1 has none (L12).
- The admin tenant suspension of `POST /v1/admin/tenants/{id}/suspend`, which drains a tenant's sessions and is unrelated to session suspension (§15.1) **[source]**.

## Terms

| Term | Meaning |
| :-- | :-- |
| Client | The party a session answers to. For a top-level session it is the REST, MCP, or provider-surface client. For a child it is the parent session. |
| Root and root's client | The root is the top-level session of a tree. The root's client is the authenticated principal that owns the root, on REST, MCP, or a provider surface. A message's `from` member or a `fromSessionId` never makes a caller the root's client. In v1 the root's client is the only party outside the tree that acts on the tree's sessions. |
| Turn | One unit of work: it opens when a message is written to a runtime with no open turn, or when the runtime writes output on its own, and closes on the runtime's end-of-turn `message` frame ("MD-4" of the message delivery design). |
| Open turn | A turn that has opened and not closed. The gateway tracks it per session. |
| Pod | The agent pod that runs a session's runtime. A session that holds its pod answers its next message at once. One that released it is restored onto a new pod from a checkpoint first. |
| `one_shot` | The §5.1 `interaction` value of a runtime that ends its task after a single end-of-turn frame. |
| Waiting state | `idle` or `dormant`: the session has no open turn and waits for its client's next message. |
| Paused state | `suspended` or `blocked`: the root's client paused the session or an ancestor. |
| Suspension root | The session the root's client suspended. Its descendants are `blocked` under it. |
| Nearest paused ancestor | The closest ancestor of a session that is `suspended` or `blocked`, that carries a recorded suspension (`suspension_requested`), or that ended while `suspended`. A session that becomes live while it has one lands in `blocked` under it ("SL-4", L25). |
| Adoption | A gateway replica's takeover of a session's coordination lease after a coordinator handoff or the loss of the previous coordinating replica ([§10.1](../spec/10_gateway-internals.md#101-horizontal-scaling)). |
| Live state | `running`, `idle`, or `dormant`: a state in which a session works or waits for its client, outside setup, recovery, the paused states, and the terminal states. |
| Turn cut | A turn that ended without an end-of-turn `message` that counts as its end: stopped by a suspension, lost with a pod, ended by the idle clock, or open when a close ended it. An end-of-turn `message` that a runtime writes after a suspension stopped its turn leaves the turn cut ("SL-3"). |
| Resume notice | A system message the gateway writes to a session whose turn a suspension or a pod loss cut, when the session resumes or its restore commits ("SL-3"). |
| Qualifying activity | The traffic that resets a session's `maxClientIdleSeconds` clock and that the adapter's activity window counts ("SL-6"). |
| Close | Ending a session at its client's request or by a cascade, recording `completed` or `cancelled` by the close rule ("SL-7"). |
| Cancel | Ending a session with `cancelled` whatever its state: `DELETE /v1/sessions/{id}` and MCP `cancel_session`. |
| Child event | A message the gateway writes into a parent's inbox on a child's behalf, with `from: { kind: "agent", id: <child id> }` ("MD-22"). |
| Inbox | The gateway's per-session store of accepted messages. One inbox serves every sender of a session: its client, its children, and the system. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Integration levels | Basic (stdin and stdout only), Standard (adds the platform MCP server), and Full (adds `CH-RUNTIMEOPS`) ([§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels)). Below Full, the runtime process receives no signal when its session is interrupted. |
| `interrupt_request` | The message the adapter writes on `CH-RUNTIMEOPS`, the intra-pod channel of a Full-level runtime, to stop the runtime's work, which the runtime answers with `interrupt_acknowledged` ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `interrupt` intent | The delivery intent of a message that stops the open turn at a safe point and delivers the message as the start of the next turn ("MD-1"). It does not suspend the session. |
| `heartbeat_ack` with `busy` | The answer a runtime writes to each heartbeat, whose `busy: true` reports that the runtime works for the session the heartbeat names (§28.5.3). |
| Session seal | The adapter's refusal to write any `message` to a session's runtime, which the gateway requests before it checkpoints a session and releases its pod ([§4.7.1](../spec/04_system-components.md#471-role-and-gateway-rpc-contract) **Session seal.**). |
| DLQ | The per-session dead-letter queue that holds messages for a session in a pre-running, recovering, waiting-without-a-pod, or paused state (§7.2). Its entries take a TTL of `maxResumeWindowSeconds`, 900 seconds by default, today. |
| Backlog resume | The §7.2 rule by which the coordinating replica, or the gateway leader for a session without a pod, resumes a suspended session that holds messages. |
| Resume admission | The [§11.1](../spec/11_policy-and-controls.md#111-admission-and-fairness) check that runs the session-creation limits when a session leaves `suspended`, and refuses the resume when a limit is reached. |
| Delegation lease | The limits a parent grants a child at its start: depth, budget, deadline, tool list, delegation targets, messaging limits, and the cascade policy ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| `messagingRateLimit` | The lease's limit on the messages a session sends, per minute as `maxPerMinute` and over the session's life as `maxPerSession` (§8.3). |
| `perChildMaxAge` | The lease's wall-clock limit on a child's life, which no pause stops (§8.3). |
| `maxSuspendedPodHoldSeconds` | How long a suspended session holds its pod before the gateway checkpoints the workspace and releases it, 900 seconds by default ([§6.2](../spec/06_warm-pod-model.md#62-pod-state-machine), "Graceful pod release during extended suspension"). |
| `maxSuspendedSessionSeconds` | The gateway setting `gateway.maxSuspendedSessionSeconds`, 604,800 seconds by default, after which a suspended session expires (§6.2). |
| `cascadeTimeoutSeconds` | The lease setting, 3600 seconds by default, that bounds how long the children of a terminal parent may keep working ([§8.10](../spec/08_recursive-delegation.md#810-delegation-tree-recovery)). |
| `maxClientIdleSeconds` | The session policy's idle bound, 900 seconds by default (§6.2). |
| LLM proxy and `deliveryMode` | The gateway's proxy for a session's model-provider calls. A runtime with `deliveryMode: proxy` calls its provider through it, and one with `deliveryMode: direct` calls the provider itself with a leased credential ([§4.9](../spec/04_system-components.md#49-credential-leasing-service)). |
| `session_tree_archive` | The Postgres table in which the gateway records each child's terminal outcome, with its `TaskResult` (§7.3, §8.10). |
| `TaskResult` | The record of a child's result that `await_children` returns, with `state` and `output` ([§8.8](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)). |
| `consumed_unrecorded` | The inbox state of a message the runtime consumed after the checkpoint a session resumes from, which the gateway reports and does not resend (O4 of the message delivery design). |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| MD-1, MD-3, MD-4, MD-5, MD-9, MD-18, MD-19, MD-22, MD-23, MD-24, MD-25, and O4 | [`message-delivery-design.md`](message-delivery-design.md#md-22-parents-and-children) | Delivery intents, inbox states with custody and deduplication, turn accounting, the named delivery paths, inter-session messages with their rate limits, files the agent produces, the `request` and `reply` frames, parents and children with the child events and `await_child_sessions`, the tool names, the runtime frames, a parent's watch of a child's stream, and custody that ends at consumption. |
| B1 to B10, B16, B17, and B19 | [`message-delivery-design.md`](message-delivery-design.md#decisions) | The owner's decisions of 2026-11-10 that make a child behave as a top-level session, and those of 2026-11-11 on when `await_child_sessions` returns, on its name, and on what a turn stopped by a suspension consumes. |
| DA-4 | [`delegation-availability-design.md`](delegation-availability-design.md#da-4-tool-list) | The per-session platform tool list. |
| MD-A and MD-E | [`message-delivery-design.md`](message-delivery-design.md#proposal-split-and-order) | The proposals that land the delivery core and the parents and children of the message delivery design. |

## High-level design

Every session, top-level or child, goes through the same states. A session is `running` while it works on a turn and `idle` once the turn ends, waiting for its client. If nothing happens on the session for a while, in a turn or between turns, the gateway saves the workspace and gives the pod back, and the session becomes `dormant`. A turn that was open is cut. The session still waits for its client, and its next message brings it back after a restore.

A client can suspend a session. The session stops its current turn and pauses, and so does every session below it, which becomes `blocked`. A runtime that cannot be told to stop, or does not stop, loses its pod at once. Only the root's client can lift a suspension, by resuming the session or sending it a message. A child's message never wakes a paused session, and messages held for a paused session wait for it. When the tree resumes, each session whose turn the suspension cut receives a notice and carries on with its work. A session restored after a pod loss receives the same notice, and its client learns that the turn was lost.

A session ends in one of these ways: the runtime says it is complete, its client closes it, its client cancels it, a limit expires it, or it fails. A close records `completed` when the session's last turn finished, with that turn's message as its result, and `cancelled` when the close or an earlier suspension or failure cut its work off. When a parent ends, its children are closed at once by default, or allowed to finish the turn they are on.

The rest of this section states each part in the design's terms.

**States (SL-1).** `running` has an open turn. `idle` waits with its pod, and `dormant` waits without it. `suspended` is a session the root's client paused, and `blocked` is a session paused under a suspended ancestor. Setup, recovery, `awaiting_client_action`, and the terminal states keep their meaning. `input_required` and the suspension-reason field leave.

**Transitions (SL-2).** One table lists every transition with its event.

**Suspension and the open turn (SL-3).** A suspension stops the open turn, so a paused session never holds an open turn, and frames a paused session writes change nothing. A runtime that does not acknowledge the stop has its workspace checkpointed and its pod released at once. On resume, and after a restore that a pod loss caused, a session whose turn was cut receives a resume notice that opens a turn.

**Tree suspension (SL-4).** `POST /v1/sessions/{id}/suspend` records the suspension on the session and moves each live descendant to `blocked`. A session that becomes live under a paused ancestor lands in `blocked`. Only the root's client lifts a suspension.

**Messages (SL-5).** Any message wakes a waiting session. A paused session wakes only for the root's client, and holds every other message until the session ends.

**Idle pods (SL-6).** A session with no activity for `maxClientIdleSeconds` becomes `dormant`: an `idle` session unless one of its descendants is `running`, and a `running` session with its turn cut.

**Closing (SL-7).** A session has one close operation, the gateway derives its terminal state, and `DELETE` stays an explicit cancel.

**Parents and children (SL-8).** The parent's state decides when child events reach it, and the child's state decides which child events the parent receives.

**When a parent ends (SL-9).** The parent's inbox closes, then `onParentEnd` closes its children at once (`close_all`) or lets each finish its turn (`let_finish`).

**Admission, caps, and timers (SL-10).** The caps count every non-terminal state except `dormant`, `suspended`, and `blocked`, and the resume admission check runs whenever one of those states moves toward a pod.

**External mappings (SL-11).** `idle` and `dormant` map to MCP Tasks `input_required` and A2A `input-required`, and `suspended` and `blocked` map to `working` with `metadata.paused: true`.

**Recovery (SL-12).** After a parent's restore, the gateway resends each `ended` and end-of-turn child event the parent consumed after its checkpoint.

```
  setup ──start──> running ──end-of-turn message──> idle ──maxClientIdleSeconds──> dormant
                     ▲  │                            │                               ▲  │
                     │  └── maxClientIdleSeconds, turn cut ──────────────────────────┘  │
                     └────── message, or output ─────┘<──── message (after restore) ────┘

  running, idle, dormant ──root's client suspends this session──> suspended
  running, idle, dormant ──root's client suspends an ancestor───> blocked
  suspended ──resume, or message from the root's client──> idle (or restore first)
  blocked   ──the suspension root resumes─────────────────> idle (or restore first)

  running ──complete: true──> completed
  any non-terminal state ──close──> completed (last turn finished) or cancelled (work cut off)
                         ──cancel──> cancelled     ──limit──> expired     ──failure──> failed
```

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
| L15 | What happens to a runtime that does not stop at a suspension. | "1: ok" to the recommendation. A runtime below Full level receives no signal, so the gateway seals the session, checkpoints the workspace, and releases the pod at once, as an idle suspension does today. A Full-level runtime that does not acknowledge `interrupt_request` within its deadline is treated the same way at the deadline. Frames a paused session writes open no turn and change no state. An acknowledged suspension keeps today's pod hold. | SL-3 |
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
| L24 | A suspension that cannot take hold. | "ok". The seal answers "not sealed" while a platform or connector call is in flight, and a failed checkpoint keeps the pod today. When a suspension cannot take hold, the gateway refuses the session's new LLM-proxy, platform, and connector calls and cancels those in flight, then seals the session and releases the pod. It retries a failed checkpoint, and at `maxSuspendedPodHoldSeconds` it forces the release, and the session resumes from its last good checkpoint. A runtime with `deliveryMode: direct` calls its provider without the proxy, and only the pod release stops it. | SL-3 |
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

## Requirements

| ID | Requirement |
| :-- | :-- |
| SLR-1 | A top-level session and a child session have the same states and the same transitions. A child's delegation lease narrows what it may do and changes no transition. |
| SLR-2 | A session is in exactly one state at a time, and the state alone decides what may wake it, whether it counts against the concurrency caps, and whether it holds an open turn. |
| SLR-3 | No paused state holds an open turn, and no frame that a paused session's runtime writes opens a turn or changes the session's state. |
| SLR-4 | When the root's client suspends a session, every descendant in `running`, `idle`, or `dormant` is `blocked` before the suspension call returns. A session that becomes live while it has a paused ancestor, at a setup commit, a restore commit, or a resume that the root's client did not request for that session, lands in `blocked` under its nearest paused ancestor. |
| SLR-5 | No message from a session in the tree, and no system message, moves a session out of `suspended` or `blocked`. |
| SLR-6 | Resuming a suspension root resumes, top-down, every descendant `blocked` under it that resume admission admits, and no descendant in `suspended`. A descendant that admission refuses stays `blocked`, and so does every session `blocked` under it (L25). |
| SLR-7 | A session whose turn a suspension or a pod loss cut receives exactly one resume notice, before any message held for it, unless a message from the root's client to that session lifted the suspension, or the resume or the restore delivers the cut turn's causing message to the runtime again: after the closing frame of a stopped turn left it unconsumed, or after a restore whose redelivery the adapter's deduplication did not drop. |
| SLR-8 | A close records `completed` only when no turn is open, no client tool call of the session is pending, and the session's last turn ended with an end-of-turn `message` that carries no `error`, and otherwise `cancelled`. A cancel always records `cancelled`. |
| SLR-9 | An `idle` session never releases its pod while one of its descendants is `running`. |
| SLR-10 | At a parent's terminal transition, the parent's inbox closes before its children are closed, so no child event opens a turn in a terminal session. |
| SLR-11 | Every `ended` and end-of-turn child event a parent consumed after the checkpoint it resumes from reaches the restored parent again. |
| SLR-12 | The concurrency caps count exactly the non-terminal sessions outside `dormant`, `suspended`, and `blocked`, and every move from one of those states toward a pod passes the resume admission check. |
| SLR-13 | A paused session's platform tool calls that act on another session are refused, so a runtime that did not stop at the suspension starts and steers no session. |
| SLR-14 | A message held for a `dormant`, `suspended`, or `blocked` session expires only when the session ends, and a child event never expires while its parent lives. |
| SLR-15 | A suspension root that ends keeps every descendant `blocked` under it, and only a timeout or a cascade ends them. |
| SLR-16 | A paused session that loses its pod stays in its paused state without a pod, and its restore waits for its resume. |
| SLR-17 | A session in `running` or `idle` with no qualifying activity for `maxClientIdleSeconds` becomes `dormant`, with any open turn cut, or `expired` when it cannot become `dormant`. An `idle` session with a `running` descendant is the exception. |
| SLR-18 | A suspension that the runtime does not acknowledge, or cannot, releases the session's pod without a hold once its checkpoint succeeds, and at the latest at `maxSuspendedPodHoldSeconds` (L24). |
| SLR-19 | When a pod loss cuts a turn and the restore does not deliver the turn's causing message to the runtime again, the session's client receives exactly one report that the turn was lost. |
| SLR-20 | A tree suspension that the gateway recorded on the suspension root reaches every live descendant, after a coordinator crash as well. |
| SLR-21 | A descendant in `awaiting_client_action` does not expire while an ancestor is suspended. |
| SLR-22 | Only the authenticated principal that owns the root acts as the root's client, whatever a message's `from` member names. |
| SLR-23 | Once the gateway finds that a suspension cannot take hold, it admits no new LLM-proxy, platform, or connector call of the session from the pod in use, and it cancels those in flight, until that pod is released. A restored pod carries no refusal (L24). |
| SLR-24 | A turn stopped by a suspension consumes only the message ids its closing frame lists, and every other `delivered` message of the session returns to `pending` (L26). |
| SLR-25 | A close and a cancel apply the session's `onParentEnd` to its children, as every terminal transition does. |
| SLR-26 | Each turn end, each commit to `running` or `idle`, and each adoption of a session in `running` or `idle` is a delivery point for held messages. The first commit to `idle` after a restore, at the restore's commit or, for a restore that landed in a paused state, at the resume, is the redelivery point for `delivered` messages. |
| SLR-27 | A question or a tool-use approval pending when a suspension stops the session's turn is withdrawn before the paused state's write returns. No later answer or decision reaches the session, and no timeout or unattended policy answers a withdrawn approval (L28, L29). |
| SLR-28 | When a `blocked` session ends, every session whose `paused_ancestor_id` names it is re-pointed to the ended session's own `paused_ancestor_id`, so no `blocked` session is left under a terminal session that is not `ended_suspended`. |

## Current state

### Specification

**States [source].** §7.2 lists the session states `created`, `finalizing`, `ready`, `starting`, `running`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`, `completed`, `failed`, `cancelled`, and `expired`, with `input_required` as a sub-state of `running`. It lists `running → completed (agent finishes)` with no signal that causes it.

**Suspension [source].** §6.2 "`suspended` state" lists `running → suspended` on `interrupt_request` with `interrupt_acknowledged`, on the `interrupt_request` deadline, at which "adapter forces suspended", and when `maxClientIdleSeconds` elapses, the idle suspension, with a hold of zero. The session row records why a session is suspended: `InterruptAcknowledged`, `InterruptTimeout`, or `Idle` ("Graceful pod release during extended suspension"). A session suspended by an interrupt holds its pod for `maxSuspendedPodHoldSeconds`, 900 seconds by default, and a session suspended for idleness releases it as it is suspended. `maxSessionAge` and `maxClientIdleSeconds` pause during `suspended`, and `gateway.maxSuspendedSessionSeconds`, 604,800 seconds by default, expires a suspended session ("`suspended → expired` trigger mechanism"). `maxClientIdleSeconds` defaults to 900 seconds. "`interrupt_request` does NOT cascade to children. ... Runtime decides whether to propagate a received interrupt to its children." The specification does not say whether an interrupt ends the open turn. §15.4.3's `Interrupt` row says that below Full level "the runtime process receives no signal". The §28.5.3 `interrupt_request` row and the `CH-RUNTIMEOPS` card's **Degradation.** say that at the deadline the "adapter transitions the session to `suspended` anyway". "Pod failure while `suspended`" restores a suspended session that loses its held pod through §7.3 and lands it in `running`.

**Waking a suspended session [source].** §7.2 delivery path 6: "the message resumes the session, whatever its `delivery` value and whichever source sent it". **Backlog resume.** resumes a session suspended for idleness that holds any message in its DLQ or inbox, and a session suspended for another reason that holds a DLQ message accepted after its suspension.

**Held messages [source].** The §7.2 dead-letter table gives a message held for a recovering session a TTL of `maxResumeWindowSeconds`, 900 seconds by default, and scores each DLQ entry by its acceptance time plus that TTL.

**Idle activity [source].** §6.2 runs the `maxClientIdleSeconds` clock in `running`, with or without an open turn, and resets it on qualifying events: a client message the adapter writes to the runtime, an approval decision, an elicitation answer, or an attach; `agent_output` and `tool_use` events; each `lenny/await_children` invocation and each partial result of its stream ("This ensures that a parent session actively blocked in `await_children` is not idle-suspended."); and evidence of model calls from the LLM proxy or `ReportUsage`. An idle suspension proceeds only when the adapter's activity window also finds the session idle (§4.7.1 **Session seal.**). That window counts the session's `CH-MSGSOCK` reads and writes, each `heartbeat_ack` with `busy: true`, each `llm_request_completed`, and every `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` call while it is in flight. §28.5.3 requires a runtime to report `busy: true` for a session "while it does any work for that session". A session whose runtime is `type: mcp`, or whose pool runs `executionMode: service`, cannot be idle-suspended, and the clock expires it with the `max_idle_time` reason.

**Ending a session [source].** §15.1's precondition table gives `POST /v1/sessions/{id}/interrupt` the precondition `running` ("Only valid while the agent is actively executing"), `POST /v1/sessions/{id}/terminate` every non-terminal state with the result `completed` ("Graceful shutdown"), and `DELETE /v1/sessions/{id}` every non-terminal state with the result `cancelled`, which "always records a `cancelled` terminal state for audit and billing purposes". §15.2 offers `interrupt_session`, `terminate_session`, and `cancel_session`. No billing, retention, or seal rule of the specification reads the difference between `completed` and `cancelled`.

**Cascade [source].** §8.10 "Cascading behavior" applies `cascadeOnFailure` at every terminal transition of the parent, with the values `cancel_all`, the default, `await_completion`, and `detach`, and `cascadeTimeoutSeconds`, 3600 seconds by default. The orphan cleanup job cancels or expires orphaned sessions, and `maxOrphanTasksPerTenant`, 100 by default, makes a `detach` cascade fall back to `cancel_all` when it would exceed the tenant's orphan count.

**Child outcomes [source].** §7.3 records each child's terminal outcome in `session_tree_archive` while its parent is in `awaiting_client_action`, and §8.10's re-await protocol replays settled results from the archive before live results. A resumed parent receives `children_reattached` after `session.resumed` (§7.2). §8.10 injects a `child_failed` event into the parent's stream when a child fails.

**Admission [source].** §11.1 **Resume admission.** runs the creation checks at every `suspended → running` and `suspended → resume_pending` write, and refuses a client message with `429 QUOTA_EXCEEDED` when they fail. §8.3 **Defense-in-depth TTL.** re-arms the TTL of every budget key of a root's tree when the root enters or leaves `suspended` or leaves `awaiting_client_action` for `resume_pending`, and a root that spends longer than `delegation.budgetKeyTTLSeconds`, 8 days by default, outside `suspended` since the last re-arm meets `BUDGET_KEYS_EXPIRED`.

**Deadlock detection [source].** §8.8 "Subtree deadlock detection (heuristic)" marks a subtree `deadlocked` when every running task in it is blocked in `input_required` or in `await_children` waiting only on `input_required` children, and emits `deadlock_detected`, after which `maxDeadlockWaitSeconds` ends with `DEADLOCK_TIMEOUT`. §9.2 "Interaction with `input_required` deadlock detection" relates it to elicitation chains.

**External mappings [source].** §8.8's "Session-level state mapping (supplementary)" maps `suspended` to `working` with `metadata.suspended: true` on MCP Tasks and A2A, `awaiting_client_action` to `input_required` and `input-required`, and the pre-running states to `submitted`. `input_required` appears in §6.2, §6.4, §7.1, §7.2, §7.3, §8, §9.2, §10.1, §10.4, §11.3, §15, §15.1, §15.4, §18, §21, and §29.

### Tree

- `pkg/api/v1/session/session.go` defines the states, `input_required` among them, and `CountsTowardConcurrencyCaps`, which returns `!IsTerminal(s) && s != StateSuspended` **[source]**.
- `countedStatePredicate` in `pkg/gateway/session/sessionstore/pgstore/pgstore.go` is `state NOT IN ('completed', 'failed', 'cancelled', 'expired', 'suspended')`, and the in-memory store calls `CountsTowardConcurrencyCaps` **[source]**.
- `sessionstore.SuspendedReasonInterruptAcknowledged`, `SuspendedReasonInterruptTimeout`, and `SuspendedReasonIdle` are the reason values, written by `interrupt.go` and read by `suspend_release.go` in `pkg/gateway/sessionserver` and by the session store **[source]**. The tenant record has a `SuspendedReason` of its own for the admin tenant suspension, which this design does not change.
- `transitionInterrupt` writes `suspended`, and `transitionTerminate` writes `completed` at once, in `pkg/gateway/sessionserver/session_transitions.go`. The routes are registered in `route_table.go` **[source]**.
- The watchdog's `idleSuspendable` and `sessionidle.Resolver.IdleSuspendable` decide idle suspension **[source]**.
- `cascadeOnFailure` is read in `pkg/api/v1/session/session.go`, `pkg/gateway/sessionserver`, and `pkg/gateway/mcpfabric/mcptools/mcptools.go` **[source]**.

## SL-1 States

| State | Meaning | Open turn | Holds a pod | Counts against the caps |
| :-- | :-- | :-- | :-- | :-- |
| `created`, `finalizing`, `ready`, `starting` | Setup, as today. A child passes through them inside `start_child_session`. | No | As today | Yes |
| `running` | Working on a turn. | Yes | Yes | Yes |
| `idle` | Waiting for its client, with its pod. Replaces "`running` with no open turn". | No | Yes | Yes |
| `dormant` | Waiting for its client, with its pod released. Replaces the idle suspension. | No | No | No |
| `suspended` | The root's client suspended this session. | No | For `maxSuspendedPodHoldSeconds` after an acknowledged suspension or the suspension of an `idle` session, otherwise until its checkpoint succeeds and at most `maxSuspendedPodHoldSeconds`, and then no | No |
| `blocked` | The root's client suspended an ancestor, and this session is paused under it. | No | As for `suspended` | No |
| `resume_pending`, `resuming` | Being restored onto a pod, as today. | No | No, then yes | Yes |
| `awaiting_client_action` | Retries are exhausted, as today. | No | No | Yes |
| `completed`, `cancelled`, `failed`, `expired` | Terminal, as today. | No | No | No |

**Row fields [design].** The session row drops the suspension-reason field and gains:

- `paused_ancestor_id`, set on a `blocked` session to its nearest paused ancestor, which may itself be `blocked` ("SL-4", L25). It replaces revision 2's `suspension_root_id`.
- `suspension_requested`, set on a top-level session in `resume_pending`, `resuming`, or `awaiting_client_action` that the root's client suspends, and cleared when the session becomes `suspended` or its client resumes it (L18).
- `ended_suspended`, set on a session that reaches a terminal state while `suspended`, so the sessions `blocked` under it stay `blocked` ("SL-9").
- `last_turn_end`, one of `none`, `message`, `error`, and `cut`, which records how the session's last turn ended: with an end-of-turn `message`, with one that carries `error`, or cut. The close rule reads it ("SL-7").
- `resume_notice_due`, with the cause `suspension` or `pod_loss`, set when a suspension or a pod loss cuts a turn and cleared when the resume notice is written ("SL-3").

The pod reference the row carries today stays, and decides whether a paused session resumes at once or through a restore (L14).

## SL-2 Transitions

| From | Event | To |
| :-- | :-- | :-- |
| `starting` | Start with a message, or a child's `task` | `running`, or `blocked` under the nearest paused ancestor when the session has one, with the message held |
| `starting` | Start with no message | `idle`, or `blocked` under the nearest paused ancestor when the session has one |
| `running` | End-of-turn `message` | `idle` |
| `running` | End-of-turn `message` with `complete: true`, or the end-of-turn frame of a `one_shot` runtime | `completed` |
| `running` | `maxClientIdleSeconds` with no qualifying activity ("SL-6") | `dormant`, with the turn cut, after the workspace checkpoint and the pod release, or `expired` for a session that cannot become `dormant` |
| `idle` | A message is written, or the runtime writes output on its own | `running` |
| `idle` | `maxClientIdleSeconds` with no qualifying activity and no descendant `running` ("SL-6") | `dormant`, after the workspace checkpoint and the pod release, or `expired` for a session that cannot become `dormant` |
| `dormant` | A message arrives and resume admission admits it | `resume_pending`. The restore lands in `idle`, and the held message is then written, which opens a turn. |
| `running`, `idle`, `dormant` | The root's client suspends this session | `suspended` |
| `running`, `idle`, `dormant` | The root's client suspends an ancestor | `blocked` |
| `blocked` | The root's client suspends this session directly | `suspended`, with `paused_ancestor_id` cleared |
| `resume_pending`, `resuming`, or `awaiting_client_action`, for a top-level session | The root's client suspends this session | The same state, with `suspension_requested` set ("SL-4") |
| `suspended` | `POST /resume`, MCP `resume_session`, or a message from the root's client, admitted by resume admission | `idle` with a pod, or `resume_pending` without one |
| `blocked` | The root's client resumes this session, admitted by resume admission | `idle` with a pod, or `resume_pending` without one |
| `blocked` | The session that `paused_ancestor_id` names enters a live state, and resume admission admits this session | `blocked` under the nearest paused ancestor when the session still has one, and otherwise `idle` with a pod or `resume_pending` without one |
| `running`, `idle` | Retryable failure | `resume_pending` |
| `suspended`, `blocked` | Retryable failure while the pod is held | The same state, with the pod reference cleared, so the session is restored when it resumes ("SL-10") |
| `resume_pending` | The restore starts | `resuming` |
| `resuming` | The restore commits | `suspended` when `suspension_requested` is set, and otherwise `blocked` under the nearest paused ancestor when the session has one. Otherwise `idle`. The commit to `idle` is a delivery point ("MD-3", "MD-5"): a message delivered and not consumed is delivered again and opens a turn, and the resume notice of "SL-3" is written when `resume_notice_due` is still set after the adapter's deduplication. |
| `resuming` | The replayed event log shows that the runtime had already completed the session before the crash | `completed`, as §6.2 "`resuming` failure transitions" states today |
| `resume_pending`, `resuming` | Retries run out | `awaiting_client_action` |
| `awaiting_client_action` | A resume by the session's client or the root's client, or a message from either ("SL-5") | `resume_pending`, and `suspension_requested` is cleared |
| `awaiting_client_action` | `maxAwaitingClientActionSeconds`, whose clock stops while an ancestor is suspended | `expired` |
| Any non-terminal state | A close by the session's client, or a cascade | `completed` or `cancelled` by the close rule ("SL-7") |
| Any non-terminal state | A cancel | `cancelled` |
| Any non-terminal state | A non-retryable failure, or an operator force-terminate | `failed` |
| Any non-terminal state | `maxSessionAge`, a lease's `perChildMaxAge`, a budget, `maxSuspendedSessionSeconds`, or `cascadeTimeoutSeconds` | `expired` |

A restore after a pod loss loses the open turn, so the gateway sets `last_turn_end: cut` and `resume_notice_due` with the cause `pod_loss` when it moves a `running` session to `resume_pending` **[design]** (L16).

## SL-3 Suspension and the open turn

The owner settled L8 on 2026-11-11, and L15 and L16 after the review of revision 1 **[design]**.

**Stopping the turn.** A suspension of a `running` session stops its open turn. The gateway first writes the paused state with `last_turn_end: cut` and `resume_notice_due` with the cause `suspension`, so the paused session holds no open turn from that write on (SLR-3). What happens to the pod then depends on the runtime:

- A Full-level runtime receives `interrupt_request` on `CH-RUNTIMEOPS`. When it answers `interrupt_acknowledged` within the deadline, it has stopped at a safe point, and the session holds its pod for `maxSuspendedPodHoldSeconds`, as an interrupt suspension does today.
- A runtime below Full level receives no signal (§15.4.3), so the suspension cannot take hold, and the gateway does not wait for a signal.
- A Full-level runtime that does not acknowledge within the deadline is in the same position at the deadline.

When a suspension cannot take hold, the session's coordinating replica stops the session's work outside the pod first and then releases the pod (L15, L24):

1. The gateway refuses each new call of the session to the LLM proxy, the platform MCP server, and connectors, and cancels the calls in flight, so no `CH-MCP-PLATFORM` or `CH-MCP-CONNECTOR` call keeps the seal from taking (SLR-23). §4.7.1 **Session seal.** answers "not sealed" while such a call is in flight, whatever the window. The refusal applies to the pod in use and ends when that pod is released, and the pod a later restore claims carries none.
2. It seals the session with an activity window of zero, so the adapter seals it whatever its recent activity.
3. It checkpoints the workspace and releases the pod, in the sequence of today's idle suspension (SLR-18).
4. When the checkpoint fails, the gateway keeps the pod and retries at the evaluation interval of "Graceful pod release during extended suspension". At `maxSuspendedPodHoldSeconds` it releases the pod in any case, and the session resumes from its last good checkpoint.

A runtime with `deliveryMode: direct` calls its model provider without the proxy, so only the pod release stops those calls.

**Cancelled calls.** A call that step 1 cancels before the gateway commits its effect has no effect, and one whose effect the gateway already committed keeps it, and the resume notice names it. For the delegation tools:

- A cancelled `start_child_session` whose child the gateway had already created keeps the child, which is recorded as the session's child and lands `blocked` with its task held, as step 4 of "SL-4" states. The resume notice names it with its session id. A start the gateway had not committed creates nothing.
- A cancelled `await_child_sessions` returns the child events it had moved into its result to `pending` in the session's inbox, and they are delivered after the resume.
- A cancelled `message_child_session` or `close_child_session` follows the general rule: the message or the close took effect if the gateway committed it, and the notice names it.

**Pending questions and approvals (L28, L29).** When a suspension stops a turn, the gateway withdraws every question and every tool-use approval of the session that is still pending (SLR-27):

- A question the session raised through `ask_question` or a `request` with `kind: "question"` is expired as §9.2 "Elicitation Timeout Semantics" item 6 expires the elicitations of a session that leaves its pod, whether or not the pod is released yet. The root's client's view drops it, and a later `respond_to_elicitation` or `dismiss_elicitation` for it is refused with `404 ELICITATION_NOT_FOUND`.
- An approval, from a `request` with `kind: "approval"` or an approval-gated `tool_call` that the gateway holds, is withdrawn in the same way. The gateway emits `tool_use` with the phase `withdrawn`, so the client's view and the MCP approval prompt drop it, and a later `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve` or `.../deny` for it is refused with `404 RESOURCE_NOT_FOUND`. The approval's timeout and the unattended policy of `harnessApprovals` no longer answer it.

While the pod is still held, the gateway ends the runtime's wait: an `ask_question` call returns an error that names the withdrawal, a `request` with `kind: "question"` receives a `reply` with `outcome: "withdrawn"`, a `request` with `kind: "approval"` receives a `reply` with `decision: "withdrawn"`, and a held `tool_call` receives an error result that names the withdrawal, as a denial does. The resume notice names each withdrawn question and approval, so the runtime may ask again or retry the tool.

The `Interrupt` RPC's timeout writes no session state. The adapter returns `INTERRUPT_TIMEOUT`, and the gateway decides what follows: the release above for a suspension, and the `queue` fallback for a message with the `interrupt` intent ("MD-5").

A session that is `idle` at its suspension has no turn to stop and holds its pod for `maxSuspendedPodHoldSeconds`. A `dormant` session has no pod to hold.

**Frames from a paused session.** A runtime can write frames between the write of the paused state and its stop or the pod's release. Such a frame opens no turn and changes no session state (SLR-3):

- `message` and `message_part` frames reach the transcript, and the gateway makes no child event and no result from them. The end-of-turn `message` with which a Full-level runtime closes the stopped turn leaves `last_turn_end: cut`, produces no `end_of_turn` child event, and is never a result.
- `consumed` reports are recorded, because they state which messages the runtime holds. The end-of-turn `message` that closes the stopped turn consumes only the ids it lists, and every other `delivered` message of the session returns to `pending`, so the resume delivers it again (L26, SLR-24). The rule of "MD-4" that an end-of-turn `message` without `consumed` consumes every `delivered` message does not apply to it. When the closing frame leaves the stopped turn's causing message unconsumed, the message's return to `pending` restarts the work at the resume, so the gateway clears `resume_notice_due` and writes no resume notice, as after a restore that delivers the causing message again.
- Every platform tool call that acts on another session, `start_child_session`, `message_child_session`, and `close_child_session` among them, is refused with `409 SESSION_PAUSED` (SLR-13).

A turn that ended with its end-of-turn `message` before the write of the paused state is not cut, and the session receives no resume notice.

**Resume notice.** When a session whose row carries `resume_notice_due` resumes, or its restore commits in a live state, the gateway writes a system message to it before any message held for it: `from.kind: system`, `notice: "resumed"`, and a text by cause. After a suspension the text is "The session was suspended and has now resumed. Continue the work you were doing when it was suspended." After a pod loss it is "The session was restored after its pod was lost, and the turn in progress was cut. Continue the work you were doing." The notice also names each call that "Cancelled calls" says it names and each question and approval that "Pending questions and approvals" withdrew. The message takes the session's default intent and opens a turn, and the gateway clears the flag when it writes the message (SLR-7). The gateway writes no notice when the resume or the restore delivers the cut turn's causing message to the runtime again, which restarts the work: after the closing frame of a stopped turn left it unconsumed, or after a restore whose redelivery the adapter's deduplication did not drop ("After a pod loss"). Held messages follow the notice, so a correction held for the session, from its parent or from the root's client, reaches the runtime after the notice and overrides it. When a message from the root's client to the session itself lifts the suspension, the gateway writes no notice, because that message says what to do next. A session idle at its suspension receives no notice, because it had no work in progress.

**After a pod loss (L16).** A restore loses the open turn, and a runtime that reported its causing message `consumed` would otherwise sit `idle` with nothing to do. When the restore commits in a live state, the session receives the resume notice, and its client receives one report that the turn was lost and that the session was told to continue (SLR-19). A parent receives an `end_of_turn` child event with `error: "turn_lost"` and the text "The child's turn was lost with its pod. The child was restored and told to continue its work." ("SL-8"). A top-level client receives `turn_end` with `error: "turn_lost"` on the session's event stream. The client can wait for the continued turn, send a correction, or close the session. A restore that lands in a paused state reports at the resume. The gateway decides between the notice and a redelivery after the adapter's deduplication of "MD-3": when the adapter writes the cut turn's causing message to the restored runtime again, the redelivery restarts the work as it does today, so the gateway clears `resume_notice_due` and writes neither the notice nor the report. When the adapter drops the redelivered id because the checkpoint's consumed set holds it, and relays `consumed` for it, the notice and the report follow.

**Without the notice.** A resumed tree would sit idle until the root's client sent a message and the root's model steered each child again. The notice restarts each session on its own work.

## SL-4 Tree suspension

The owner settled L2, L5, and L12 on 2026-11-11, and L18 after the review of revision 1 **[design]**.

**Routes.** `POST /v1/sessions/{id}/suspend` replaces `POST /v1/sessions/{id}/interrupt`, and MCP `suspend_session` replaces `interrupt_session`. The precondition is `running`, `idle`, `dormant`, or `blocked`, and for a top-level session also `resume_pending`, `resuming`, or `awaiting_client_action` (L18). A `suspended` session and a child in a recovering state answer `409 INVALID_STATE_TRANSITION` with its current state. The call returns once the suspension is recorded on the session's row and every descendant in a live state is paused (SLR-4).

**Steps.** When the root's client suspends a session:

1. The gateway writes the suspension on the suspension root's row first: `suspended` under "SL-3", or `suspension_requested` for a top-level session in `resume_pending`, `resuming`, or `awaiting_client_action`. That write is the durable record of the tree suspension. Once the session is `suspended`, it opens no turn and sends no `message_child_session`.
2. The gateway moves each descendant in `running`, `idle`, or `dormant` to `blocked`, with `paused_ancestor_id` set to its nearest paused ancestor, top-down, stopping each open turn under "SL-3".
3. A descendant already `suspended` stays `suspended`, because its own suspension came first, and its descendants stay under it.
4. A descendant in a setup state, `resume_pending`, or `resuming` continues. When its setup or its restore commits, it lands in `blocked` under its nearest paused ancestor (SLR-4). A child whose `start_child_session` was in flight at the suspension is such a descendant, and its task stays held until it resumes.
5. A descendant in `awaiting_client_action` keeps waiting, and its `maxAwaitingClientActionSeconds` clock stops while an ancestor is suspended (SLR-21).

**A partial suspension.** A coordinator crash can stop the gateway between step 1 and the last move of step 2. The gateway leader's backlog sweep (§7.2 **Backlog resume.**) also finds every descendant in `running`, `idle`, or `dormant` that has a paused ancestor, and hands each to that session's coordinating replica, which moves it to `blocked` and stops its turn under "SL-3", sending `interrupt_request` and doing the seal and the checkpoint as for a direct suspension. A session without a coordinating replica has no pod, and the leader writes `blocked` itself. The next sweep therefore finishes the suspension (SLR-20).

**Pods and timers.** A session that a Full-level runtime's acknowledgement stopped, and a session that was `idle` at its suspension, holds its pod for `maxSuspendedPodHoldSeconds`, then checkpoints and releases it, as today. Every other paused session releases its pod as soon as its checkpoint succeeds, and the gateway retries a failed checkpoint until `maxSuspendedPodHoldSeconds` forces the release ("SL-3", L24). A paused session's `maxSessionAge` and `maxClientIdleSeconds` clocks stop, and `maxSuspendedSessionSeconds` runs from its entry to the paused state. The budget keys of the tree are re-armed when the root enters and leaves `suspended`, as §8.3 states today, and when it enters and leaves `dormant` ("SL-10").

**Who suspends and who lifts.** In v1 only the root's client suspends a session: it suspends the root, or any descendant directly through REST or MCP. The root's client is the authenticated principal that owns the root, and neither a message's `from` member nor a `fromSessionId` makes a caller the root's client (SLR-22). A parent has no tool to suspend a child (L12). Only the root's client lifts a suspension, by `POST /resume`, `resume_session`, or a message to the suspended session. A resume of a top-level session in `awaiting_client_action` by its client lifts a suspension recorded on it, and when the session's restore commits in a live state, the descendants `blocked` under it resume as in a tree resume ("Resuming a tree"). A parent's `message_child_session` to a child in `suspended` or `blocked` is held until the suspension is lifted.

**A descendant suspended directly.** When the root's client suspends a descendant, the descendant's parent receives a `suspended` child event, because the parent is that child's client and would otherwise see its messages held without a reason. When the root's client resumes it, the parent receives a `resumed` child event ("MD-22").

**Resuming a tree.** Resuming the suspension root resumes the root, and each session that enters a live state then resumes the descendants whose `paused_ancestor_id` names it, top-down, each through resume admission ("SL-10"). A descendant in `suspended` keeps its own suspension, and the sessions `blocked` under it stay `blocked`. A descendant that admission refuses stays `blocked`, and the sessions `blocked` under it stay `blocked` under it until it resumes (L25). Each resumed session receives the resume notice of "SL-3" first, and its held messages follow at the delivery points of "MD-5", the commit to `idle` among them, oldest first.

## SL-5 Messages to a waiting or paused session

The owner settled L3 on 2026-11-11, and L2 makes the hold a safety net under the tree suspension **[design]**.

| State | A message from the root's client | A message from the session's parent | A child event or a system message |
| :-- | :-- | :-- | :-- |
| `idle` | It is written and opens a turn. | It is written and opens a turn. | It is written and opens a turn. |
| `dormant` | It wakes the session through resume admission and a restore, then opens a turn. | It wakes the session in the same way. | It wakes the session in the same way. |
| `suspended` | It resumes the session and the descendants `blocked` under it, then opens a turn. | It is held. | It is held. |
| `blocked` | It resumes this session, then opens a turn. | It is held. | It is held. |
| `awaiting_client_action` | It resumes the session through a restore, then opens a turn. | It resumes the child through a restore, then opens a turn (L7). | It is held. |

A held message stays `pending` in the session's DLQ and is delivered at the next delivery point after the session is live again, which for a restored or resumed session is its commit to `idle` (SLR-5, SLR-26).

**How long a held message lives (SLR-14).** A message held in the DLQ takes a TTL of `maxResumeWindowSeconds`, 900 seconds by default, today, and a pause can last up to `maxSuspendedSessionSeconds`, seven days by default. A message held for a `dormant`, `suspended`, or `blocked` session therefore takes no TTL: it expires with `target_terminated` when the session ends, and the DLQ's `maxDLQSize` cap still applies. A child event never expires while its parent lives, in any state, because the child cannot send it again. Other messages held for a recovering session keep today's TTL.

**Backlog resume** (§7.2) changes to match. It wakes a `dormant` session that holds any message. It resumes a `suspended` or `blocked` session only for a held message from the root's client, and a `blocked` session whose `paused_ancestor_id` names a session in a live state (L25). A suspension root that ended keeps its descendants `blocked` (L4, SLR-15). The same sweep finishes a partial tree suspension ("SL-4").

The delivery paths of "MD-5" read these states: the resume path covers `dormant`, `suspended`, and `blocked`, and `awaiting_client_action` for a message from the session's client or the root's client, under this table, and the direct, steer, interrupt, and queue paths cover `idle` and `running`.

## SL-6 Idle pods and dormancy

The owner settled L11 and L14 on 2026-11-11, and L17 after the review of revision 1 **[design]**.

**The idle clock.** `maxClientIdleSeconds` runs in `running` and `idle`, as §6.2 runs it in `running` today, with or without an open turn. Every qualifying activity resets it, and the adapter's activity window of §4.7.1 **Session seal.** confirms the decision before the pod is released, as it confirms an idle suspension today. When the clock fires:

- An `idle` session with no `running` descendant becomes `dormant` (SLR-9).
- A `running` session has its open turn cut, with `last_turn_end: cut`, and becomes `dormant` at once (L17, SLR-17). It has already been silent for a whole window, and a stop in `idle` would hold its pod for a second one. Its client receives `turn_end` with `error: "idle_timeout"`, and a parent receives an `end_of_turn` child event with the same error, so a parent waiting in `await_child_sessions` learns that the turn ended. No resume notice follows, because the gateway knows of no work in progress, and the client's next message says what to do.

The gateway checkpoints the workspace and releases the pod in the sequence that writes `dormant`, as it does for an idle suspension today.

**Qualifying activity.** The rules of §6.2 and §4.7.1 stay, with the frame names of the message delivery design ("MD-24"):

- Each message the adapter writes to the runtime: a client message, a child event, or a system message.
- Each session-scoped frame the runtime writes: `message`, `message_part`, `activity`, `consumed`, `request`, `tool_call`, and `tool_result`, and each `reply` the adapter writes.
- Each `heartbeat_ack` with `busy: true` for the session.
- Each `llm_request_completed`, each proxied chunk of a model call, and each non-zero usage report, as §6.2 states today.
- Every platform or connector tool call while it is in flight, `await_child_sessions` and `ask_question` among them, and every export in progress ("MD-18").
- A `request` that waits for its `reply`, from the request until the reply arrives or the request expires, so a person who takes longer than the window to approve or to answer does not cost the session its turn (L17).

§6.2's separate rule that counts each `await_children` call and each partial result of its stream as activity leaves, because the in-flight rule covers a parent that waits inside `await_child_sessions`.

**The rule for runtime authors.** A runtime that works through a long task without writing frames reports `busy: true` on each heartbeat, as §28.5.3 already requires, or sends activity updates, or streams message parts. A runtime that does none of these for `maxClientIdleSeconds` has its turn cut.

**Sessions that cannot become dormant.** A session whose runtime is `type: mcp`, which receives no `session_start` to restore from, or whose pool runs `executionMode: service`, which has no checkpoint, cannot become `dormant`. When the clock fires for such a session, it becomes `expired` with the `max_idle_time` reason, as §6.2 expires a session that cannot be idle-suspended today.

**Working descendants.** An `idle` session does not become `dormant` while any of its descendants is `running` (SLR-9). A parent that waits for its children by ending its turn has no tool call in flight, and without this rule it would release its pod while they work and pay a restore for the next child event. When the whole tree is waiting or paused, each session becomes `dormant` on its own clock.

**Timers.** `maxSessionAge` and `maxClientIdleSeconds` pause in `dormant`, and `maxSuspendedSessionSeconds` expires a `dormant` session, as both apply to an idle-suspended session today. The tree's budget keys are re-armed when the root enters and when it leaves `dormant` (L20, L23).

## SL-7 Closing a session

The owner settled L9 on 2026-11-11, and L21 after the review of revision 1 **[design]**.

**What the terminal state is for.** Every close and every cancel stops the session at once, releases the pod, seals the workspace, and applies the session's `onParentEnd` to its children ("SL-9", SLR-25), which closes them at once under the default `close_all`. The gateway does nothing differently afterwards for `completed` and `cancelled`, and the audit record names who closed the session. The state matters to readers outside the gateway:

- An A2A or MCP Tasks client branches on it. A `completed` task carries results the client uses, and a `canceled` task tells it the work was abandoned. Both protocols define a cancel operation whose outcome must be `canceled`.
- A person watching the tree, and `list_child_sessions`, read `completed` as "this session produced what it was asked for" and `cancelled` as "its work was cut off".
- Dashboards and experiment comparisons that count outcomes per runtime revision or variant read it.

A parent never needs the state of a child it closed itself.

**The close rule.** A close records:

- `completed` when no turn is open at the close, no client tool call of the session is pending, and `last_turn_end` is `message`. The result is that end-of-turn message.
- `cancelled` otherwise: a turn is open, client tool calls that the last turn returned are pending (L21), the last turn was cut or ended with `error`, or the session never finished a turn. There is no result.

**Operations.**

| Who | Close | Cancel |
| :-- | :-- | :-- |
| A REST client | `POST /v1/sessions/{id}/close`, which replaces `POST /terminate` | `DELETE /v1/sessions/{id}`, unchanged |
| An MCP client | `close_session`, which replaces `terminate_session` | `cancel_session`, unchanged |
| A parent | `close_child_session`, with no argument | A parent has no cancel tool. |
| A cascade | `onParentEnd` ("SL-9") | A cascade closes by the close rule and never cancels. |

A close of a `running` session cuts its turn at once, as `POST /terminate` does today, and the partial output stays in the transcript and outside the result. A parent that wants a child's turn to finish waits for its end-of-turn child event before it closes the child. A close in a setup state records `cancelled`, where `POST /terminate` records `completed` today.

**One tool.** A model mostly calls a tool with a flag without setting it **[unverified]**, and with two tools it picks between them inconsistently, so a state the model chose would record its guess. The gateway knows whether the work finished.

**Results.** `close_child_session` returns the child's `TaskResult`, and the parent receives no child event for a child it closed itself. `TaskResult.output` of a `completed` close is the last end-of-turn message, and of a completion by `complete: true` the completing message ("MD-22").

## SL-8 Parents, children, and their states

**Child events by the parent's state.**

| Parent state | Its children | Child events |
| :-- | :-- | :-- |
| `running` | They keep running. A child can only be started here, because `start_child_session` is a tool the model calls during a turn. | They are written after the current tool result on a `steer` runtime, and at the turn's end on other runtimes ("MD-22"). |
| `idle` | They keep running. | Each opens a turn. |
| `dormant` | They keep running. "SL-6" makes this rare, because a parent becomes `dormant` only when no descendant runs. | Each wakes the parent through resume admission and a restore, then opens a turn. A refused admission holds the event with `resume_refused` ("MD-22"). |
| `suspended`, `blocked` | They are paused with it. | They are held (SLR-5). |
| `resume_pending`, `resuming` | They keep running. | They wait in the inbox, and "SL-12" resends the `ended` and end-of-turn events the parent consumed after its checkpoint. |
| `awaiting_client_action` | They keep running, and their terminal outcomes are archived in `session_tree_archive`, as today. | They are held until the parent resumes, and none expires while the parent lives (SLR-14). |
| Terminal | `onParentEnd` applies ("SL-9"). | The inbox is closed, and a later child event goes only to the archive. |

**Child events by the child's state.** The event names are those of "MD-22".

| Child state or change | What the parent receives |
| :-- | :-- |
| A mid-turn `message` | A `message` child event. |
| `running` to `idle` | An `end_of_turn` child event. |
| `running` to `dormant` by the idle clock | An `end_of_turn` child event with `error: "idle_timeout"` ("SL-6"). |
| `idle` to `dormant` | No event. A `message_child_session` wakes the child. |
| `running` stopped by a suspension | No `end_of_turn` event, also when the runtime acknowledges with an end-of-turn `message` ("SL-3"). |
| `blocked` | No event, because the parent is paused as well. |
| `suspended` directly by the root's client, and its resume | A `suspended` child event, then a `resumed` child event. |
| `resume_pending`, `resuming` | No event while the child recovers, and the parent's messages wait in the child's inbox. When a pod loss cut the child's turn, the parent receives an `end_of_turn` child event with `error: "turn_lost"` once the restore commits, and the child receives the resume notice (L16). |
| `awaiting_client_action` | An `awaiting_client_action` child event, marked recoverable. The parent closes the child or resumes it with `message_child_session` (L7). |
| Any terminal state the parent did not cause | One `ended` child event with the state and the `TaskResult`. It covers `completed`, `failed`, `cancelled`, and `expired`, and replaces §8.10's `child_failed`. |

**No deadlock to detect.** The subtree deadlock detector of §8.8 looks for sessions blocked in `input_required`, which leaves. A session that waits for its client is `idle` and waits for no other session. `await_child_sessions` returns at once for a covered child that is waiting or paused with nothing new to report, and as soon as a covered child ends for any reason ("MD-22"). An elicitation that travels the §9.2 chain is bounded by its per-hop forwarding timeout. The detector, `deadlock_detected`, `maxDeadlockWaitSeconds`, and `DEADLOCK_TIMEOUT` therefore leave.

## SL-9 When a parent ends

The owner settled L4, L6, and L10 on 2026-11-11 **[design]**.

**Order.** At a parent's terminal transition, whatever its cause:

1. The parent's inbox closes. A child event that arrives later is recorded in `session_tree_archive` and opens no turn (SLR-10).
2. The gateway applies `onParentEnd` to the parent's non-terminal children.
3. Messages still in the inbox are drained to the DLQ and reported as `target_terminated`, as today (§7.2 "Inbox drain on terminal transition").

**`onParentEnd`.** It replaces `cascadeOnFailure` on the delegation lease and keeps its place, its inheritance, and its default.

| Value | Effect on each non-terminal child |
| :-- | :-- |
| `close_all`, the default | The child is closed at once by the close rule, and so is each of its descendants. |
| `let_finish` | A `running` child keeps working, up to `cascadeTimeoutSeconds`, and is closed by the close rule when its turn ends. A child in a waiting state or in `awaiting_client_action` is closed at once, because no client is left to resume it. A child in `resume_pending` or `resuming` is handled by where its restore lands: a restore that writes the resume notice continues the cut turn as a `running` child, and any other landing closes the child at once. A paused child stays paused and ends at its own `maxSuspendedSessionSeconds` or at `cascadeTimeoutSeconds`, whichever comes first (L4). A child still `running` at `cascadeTimeoutSeconds` expires. |

**A `blocked` session that ends.** At the terminal transition of a `blocked` session, the gateway re-points every session whose `paused_ancestor_id` names it to the ended session's own `paused_ancestor_id`, so its `blocked` descendants stay under a paused ancestor that a resume reaches (SLR-28). As a safety net, backlog resume recomputes the nearest paused ancestor of a `blocked` session whose `paused_ancestor_id` names a terminal session that is not `ended_suspended`.

**A suspension root that ends.** A session that reaches a terminal state while `suspended` records `ended_suspended`. Under `let_finish`, the sessions `blocked` under it stay `blocked`, and nothing resumes them: backlog resume and the restore landing treat such a root as suspended (SLR-15).

`detach` leaves (L6). Its orphans had no client and no result reader, and under the waiting states a child that outlives its parent would wait for a client that no longer exists. The orphan cleanup job keeps its role for `let_finish` children and for a cascade a replica failure interrupted, and `maxOrphanTasksPerTenant` applies to `let_finish`, which falls back to `close_all` for a delegation that would exceed it, as it does for `detach` today.

**A child's result.** A child closed by a cascade has a `TaskResult` in `session_tree_archive` under the close rule, readable through `list_child_sessions` and the task tree. No parent receives it, because the parent is terminal.

## SL-10 Admission, caps, and timers

**Caps [design, SLR-12].** `CountsTowardConcurrencyCaps` and `countedStatePredicate` count every non-terminal state except `dormant`, `suspended`, and `blocked`. An `idle` session counts, as a `running` session with no open turn counts today, and a `dormant` session does not, as an idle-suspended session does not today.

**Resume admission [design].** §11.1's check runs at every move from `dormant`, `suspended`, or `blocked` toward a pod: `dormant → resume_pending`, and `suspended` or `blocked` to `idle` or `resume_pending`. A paused session that lost its pod while paused resumes through `resume_pending`, so its restore passes the check (SLR-16). A refused client message answers `429 QUOTA_EXCEEDED`, as today. A refused child event or system message stays `pending` with `resume_refused`. A `blocked` descendant whose tree resume is refused stays `blocked`, with its own subtree `blocked` under it (L25), and backlog resume evaluates it again, as it evaluates a refused backlog resume today.

**Timers.**

| Timer | Runs in | Ends in |
| :-- | :-- | :-- |
| `maxSessionAge` | `running` and `idle`. It pauses in `dormant`, `suspended`, `blocked`, `resume_pending`, `resuming`, and `awaiting_client_action`, as §6.2 pauses it in today's counterparts, and `finalizing`, `ready`, and `starting` keep their dedicated watchdogs. | `expired` |
| `maxClientIdleSeconds` | `running` and `idle` ("SL-6") | `dormant`, with the turn cut from `running`, or `expired` for a session that cannot become `dormant` |
| `maxSuspendedPodHoldSeconds` | `suspended` and `blocked` after an acknowledged suspension or the suspension of an `idle` session, and after a suspension that cannot take hold while its checkpoint keeps failing | Pod release, which for a suspension that cannot take hold is forced, and the session resumes from its last good checkpoint (L24) |
| `maxSuspendedSessionSeconds` | `dormant`, `suspended`, and `blocked` | `expired` |
| `maxResumeWindowSeconds` | `resume_pending` | `awaiting_client_action` |
| `maxAwaitingClientActionSeconds` | `awaiting_client_action`, stopped while an ancestor is suspended | `expired` |
| `cascadeTimeoutSeconds` | Children of a terminal parent under `let_finish` | `expired` |
| `perChildMaxAge` | Every non-terminal state of a child, paused or not | `expired` |

**Budget keys (L20, L23).** §8.3 re-arms the TTL of the tree's budget keys when the root enters or leaves `suspended` or leaves `awaiting_client_action` for `resume_pending`. The gateway also re-arms them when the root enters and when it leaves `dormant`, and §8.3's rule that a root meets `BUDGET_KEYS_EXPIRED` after more than the TTL "outside `suspended`" counts the time outside `suspended` and `dormant`, so a root that alternates between `idle` and `dormant` for longer than `delegation.budgetKeyTTLSeconds` keeps its tree's budget. A `blocked` root cannot exist, because a root has no ancestor.

## SL-11 External mappings

**MCP Tasks and A2A [design].** §8.8's "Session-level state mapping (supplementary)" changes:

| Lenny state | MCP Tasks | A2A |
| :-- | :-- | :-- |
| `running` | `working` | `working` |
| `idle` | `input_required` | `input-required` |
| `dormant` | `input_required` with `metadata.dormant: true` | `input-required` with `metadata.dormant: true` |
| `suspended` | `working` with `metadata.paused: true` | `working` with `metadata.paused: true` |
| `blocked` | `working` with `metadata.paused: true` | `working` with `metadata.paused: true` |

Every turn end therefore reads as input required on both protocols. That is accurate: a session with no open turn waits for its client. Neither protocol has a paused state, so the paused states read as `working` with a flag a client can check, and `metadata.paused` replaces today's `metadata.suspended` (L22). The task-level state `input_required` of §8.8 becomes the mapping of `idle` and `dormant`, and the other rows keep today's mapping. A session in `awaiting_client_action` keeps the mapping `input-required`, and an A2A client, which has no resume call, resumes it with its next message ("SL-5").

**`state_change` [design].** The §15 `state_change` event carries the new states as top-level states and drops `subState`, whose values `input_required` and `suspended` leave with the sub-state model. §10.4's synthesized `status_change` on a coordinator handoff reads the new states.

**Removed text.** Every rule that names `input_required` is rewritten or removed in the sections "Current state" lists, §11.3's `maxRequestInputWait` among them, and "Specification changes" names each.

## SL-12 Recovery of child events

The owner's question L1 confirmed one inbox per session, and the owner settled L19 after the review of revision 1 **[design]**.

Every child event is an ordinary message in the parent's inbox. Under O4 of the message delivery design, when a session is restored from a checkpoint the gateway does not resend the messages its runtime consumed after that checkpoint, because a resend can repeat side effects. It reports each one as `consumed_unrecorded`, and the sender decides whether to send it again. A client can resend. A completed child cannot, and an idle child does not know it is expected to.

The gateway writes child events from durable records, so it acts as their sender:

- For each `ended` child event the parent consumed after its checkpoint, the gateway writes the event again from `session_tree_archive`, which §8.10's re-await protocol already replays (SLR-11).
- For each `end_of_turn` child event the parent consumed after its checkpoint, the gateway writes the event again from the child's transcript, which records the child's end-of-turn `message` (L19). A resent event carries the original event's message id, so the deduplication of "MD-3" drops it for a restored runtime whose own history already holds it.
- A child's mid-turn `message` events take the `consumed_unrecorded` notice of other messages, and their content stays readable in the child's transcript.
- `children_reattached` is still emitted after `session.resumed` when the parent has non-terminal children.

A child whose own pod loss cut its turn reports `turn_lost` to its parent when its restore commits ("SL-3").

With `messaging.durableInbox: false`, pending entries can still be lost when the coordinating replica crashes, as today, and `ended` and end-of-turn child events are recovered from the archive and the child's transcript in that case too.

## Worked example

Alice, of tenant `acme`, runs a `claude-code` session `sess_A` through REST. `claude-code` is a Full-level runtime. Its model starts two children with `start_child_session`: `sess_B` to bisect a failing build and `sess_C` to update the changelog.

1. `sess_C` writes the changelog and ends its turn. It is `idle`, and `sess_A` receives an `end_of_turn` child event. `sess_B` keeps running.
2. `sess_A` ends its own turn while `sess_B` works. It is `idle`, and it keeps its pod past `maxClientIdleSeconds` because `sess_B` is `running`.
3. Alice suspends `sess_A` with `POST /v1/sessions/sess_A/suspend`. `sess_A` had no open turn and becomes `suspended`. `sess_B` becomes `blocked` with `resume_notice_due`, its runtime acknowledges the `interrupt_request` at a safe point, and it keeps its pod for the hold. `claude-code` had reported its task consumed with a `consumed` frame early in the turn, so the closing `message` returns nothing to `pending`, and `sess_B` keeps `resume_notice_due`. `sess_C` becomes `blocked`.
4. `sess_B` had written a mid-turn message shortly before the suspension stopped it. The `message` child event reaches `sess_A`'s inbox and is held, because `sess_A` is `suspended`.
5. Alice sends `sess_A` "Resume, and skip the changelog." The message resumes `sess_A`, then `sess_B` and `sess_C`, each through resume admission. `sess_B` receives the resume notice, because no message of its stopped turn returned to `pending`, and continues bisecting. `sess_A` receives no notice, because Alice's message lifted its suspension, and it receives the held child event and then Alice's message, which opens a turn.
6. `sess_A` closes `sess_C` with `close_child_session`. `sess_C`'s last turn ended with a message, so it ends `completed` with the changelog summary as its result.
7. Alice closes `sess_A` with `POST /v1/sessions/sess_A/close` while `sess_B` still runs. `sess_A`'s inbox closes, and under `close_all` `sess_B` is closed with its turn open, so it ends `cancelled`. `sess_A` had an open turn, so it ends `cancelled` too.

## Limits

- **A suspension below Full level loses the runtime's in-memory state.** A runtime below Full level, or one that does not acknowledge `interrupt_request`, loses its pod at once, and the restore at the resume starts from the checkpoint ("SL-3").
- **A direct-mode runtime keeps calling its provider until the release.** A runtime with `deliveryMode: direct` calls its model provider without the LLM proxy, so a suspension that cannot take hold stops those calls only when the pod is released, which a failing checkpoint can delay up to `maxSuspendedPodHoldSeconds` (L24).
- **A forced release loses work.** When the checkpoint keeps failing until `maxSuspendedPodHoldSeconds`, the session resumes from its last good checkpoint, and the work after it is lost (L24).
- **A silent runtime loses its turn.** A runtime that works for `maxClientIdleSeconds` without writing a frame, reporting `busy`, or calling a tool has its turn cut ("SL-6").
- **A resumed tree repeats work.** A turn the suspension cut cannot continue where it stopped, and the resume notice starts a new turn, which can repeat steps the cut turn had taken.
- **A turn lost with a pod can run twice.** A client that resends its task after a `turn_lost` report, while the session continues from its resume notice, starts the work twice.
- **A tree resume can be partly refused.** Each `blocked` descendant passes resume admission on its own, so a tree at its tenant's cap resumes partly, and the rest resumes as backlog resume admits it.
- **A partial suspension lasts until the next sweep.** After a coordinator crash during a tree suspension, a descendant keeps working until the leader's backlog sweep blocks it ("SL-4").
- **The close rule cannot tell a satisfied close from an abandoned one.** A parent that closes an idle child whose answer it rejected records `completed`, because the child finished its turn.
- **A close in setup records `cancelled`.** A client that ends a session before its first turn sees `cancelled` where `POST /terminate` recorded `completed`.
- **Only the root's client lifts a suspension.** A parent whose child the root's client suspended cannot resume it, and its messages to the child wait.

## Alternatives rejected

- **One `suspended` state with a reason field.** It is today's model. What wakes a session and what a resume restores depend on the field, so every reader of the state also reads the field (L13).
- **The pod as a row property for every state.** The caps, resume admission, and a client reading the state would each combine the state with the property for a waiting session, where today the state alone tells them. A separate no-pod state for every paused state would double the paused states, and nothing reads the pod of a paused session except the path a resume takes (L14).
- **Suspending only the session the client named, with child events held as the only guard.** A client may not know that children run, and means everything when it suspends (L2).
- **Leaving a suspended turn open.** The harnesses cannot pause a turn and continue it, and a pod released during the suspension loses the turn in any case (L8).
- **Holding the pod of a runtime that did not stop.** The runtime would keep calling the model and connectors and raising approvals for up to `maxSuspendedPodHoldSeconds` on every session of the tree, which defeats a suspension meant to stop the spending. Refusing those calls while the pod is held would leave the runtime inside a turn the gateway considers cut (L15).
- **Resuming without a notice.** Each resumed session would wait for a message, so the root's model would have to steer every child again.
- **Only a notice, or only a report, after a pod loss.** A notice alone leaves the client unaware when the runtime ignores it or cannot continue, and a report alone leaves the session idle until its client resends the task (L16).
- **Bounding a silent turn by `maxSessionAge` alone.** A turn whose end-of-turn frame was lost would hold its pod, and every ancestor's, for the session's whole age (L17).
- **Stopping a silent turn in `idle`.** The session would hold its pod for a second window after a whole window of silence (L17).
- **A `cancel` flag on the close tool, or two close tools.** The model would choose the state, and models leave a flag unset or pick between tools inconsistently. The gateway derives the state from facts (L9).
- **Inferring nothing, with every close recording `completed`.** External task protocols, the tree's readers, and outcome dashboards would read abandoned work as finished.
- **Keeping `detach`.** Its children had no client and no reader of their results, and a child that waits after a turn would wait until `cascadeTimeoutSeconds` (L6).
- **A parent's tool to suspend a child.** No use case in the review needed it, and the lifting rule of "SL-4" already accommodates one later (L12).
- **Keeping the deadlock detector.** It detects waits on `input_required`, which leaves, and no remaining state waits on another session.

## Specification changes

| Section (by heading) | Change |
| :-- | :-- |
| [§6.2 Pod State Machine](../spec/06_warm-pod-model.md#62-pod-state-machine), "`suspended` state", "Graceful pod release during extended suspension", "Pod failure while `suspended`", "`resuming` failure transitions", "Idle suspension", the `maxSessionAge` and `maxClientIdleSeconds` per-state tables, and "`suspended → expired` trigger mechanism" | The states `idle`, `dormant`, `suspended`, and `blocked` with their transitions, and the reason field removed. A suspension that stops the open turn, cascades to the subtree, and releases the pod at once when the runtime does not acknowledge it, and the frames of a paused session. The resume notice. A paused session that loses its pod stays paused without one. The move to `dormant` from `idle`, and from `running` with the turn cut, in place of the idle suspension. The qualifying activity with the frame names of the message delivery design and a pending `request`, the in-flight rule in place of the `await_children` activity rule, and the rule that keeps an idle session's pod while a descendant runs. The expiry of a session that cannot become `dormant`, and the timers of "SL-10". "`interrupt_request` does NOT cascade" is replaced by the tree suspension. Step 3 of "Graceful pod release during extended suspension" gains the forced release at `maxSuspendedPodHoldSeconds` for a suspension that cannot take hold, and "`resuming` failure transitions" names `close` in place of `terminate` and lands a restore in the states of "SL-2". |
| [§4.7.1 Role and Gateway RPC Contract](../spec/04_system-components.md#471-role-and-gateway-rpc-contract), **Session seal.**, and §4.7's `Interrupt` RPC | The gateway calls `SealSession` at the move to `dormant` and, with an activity window of zero, at a suspension that the runtime does not acknowledge, in place of "only at the start of an idle suspension attempt", after it has cancelled the session's platform and connector calls in flight. The activity list takes the frame names of the message delivery design. The `Interrupt` RPC's timeout returns `INTERRUPT_TIMEOUT` and writes no session state. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the `interrupt_request` row and the `CH-RUNTIMEOPS` card's **Degradation.** | At the deadline the adapter returns `INTERRUPT_TIMEOUT` and writes no session state, in place of "the adapter transitions the session to `suspended` anyway". The gateway releases the pod for a suspension ("SL-3"). SL-A owns this row together with §4.7's `Interrupt` RPC. The `queue` fallback for an `interrupt` message at the deadline is staged by MD-A of the message delivery design ("MD-5"), whose row points here for the rest. |
| [§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service), the LLM proxy | The proxy refuses each new call of a session whose suspension cannot take hold and cancels its calls in flight, from the pod in use until that pod is released, and a restored pod carries no refusal (L24). A session with `deliveryMode: direct` is stopped only by the pod release. |
| [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels), the `Interrupt` row | A suspension of a runtime below Full level checkpoints the workspace and releases the pod at once. |
| [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), `maxClientIdleSeconds` | The idle bound moves a session to `dormant`, or expires one that cannot become `dormant`, in place of the idle suspension. |
| [§7.2 Interactive Session Model](../spec/07_session-lifecycle.md#72-interactive-session-model), the state list and transitions, delivery path 6, the dead-letter table, which SL-A owns and the message delivery design's row points to, **Backlog resume.**, "Mid-resume terminal transitions", and "Inbox drain on terminal transition" | The states and transitions of "SL-2", `input_required` removed, the message table of "SL-5", and the resume of `awaiting_client_action` by a message from the session's client. A message held for a `dormant`, `suspended`, or `blocked` session takes no TTL, and a child event takes none while its parent lives. Backlog resume by state, with a terminal suspension root that keeps its descendants `blocked` and the sweep that finishes a partial tree suspension. `onParentEnd` and `close` in place of `cascadeOnFailure` and `terminate` in the mid-resume rules and the transition labels, and the inbox closed before the cascade at a terminal transition. |
| [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume) | The restore landing in `idle`, in `blocked` under a paused ancestor, or in `suspended` for a recorded suspension. `last_turn_end: cut`, the resume notice, and the `turn_lost` report for a session restored from `running`. The resume of a session in `awaiting_client_action` by its client's message, and a paused session that loses its pod staying paused. |
| [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) | `onParentEnd` with `close_all` and `let_finish` in place of `cascadeOnFailure`, and the budget-key re-arm when a root enters and leaves `dormant`, beside its entry to and exit from `suspended`. **Defense-in-depth TTL.**'s clause on the time a root spends "outside `suspended`" counts the time outside `suspended` and `dormant` (L23). |
| [§8.5 Delegation Tools](../spec/08_recursive-delegation.md#85-delegation-tools) | `close_child_session` with no argument under the close rule, returning the child's `TaskResult`, `409 SESSION_PAUSED` for a paused caller, and the outcome of each delegation call that a suspension cancels ("SL-3"). Until MD-E of the message delivery design lands, SL-A stages these rules on the specification's tool names, `lenny/cancel_child` and `lenny/delegate_task`. |
| [§8.8 TaskRecord and TaskResult Schema](../spec/08_recursive-delegation.md#88-taskrecord-and-taskresult-schema) | The state mappings of "SL-11" with `metadata.paused`, `TaskResult.state` by the close rule, and "Subtree deadlock detection (heuristic)", `deadlock_detected`, `maxDeadlockWaitSeconds`, and `DEADLOCK_TIMEOUT` removed. |
| [§8.10 Delegation Tree Recovery](../spec/08_recursive-delegation.md#810-delegation-tree-recovery) | "Cascading behavior" with `onParentEnd` and its order of "SL-9", `detach` removed, `maxOrphanTasksPerTenant` applied to `let_finish`, a suspension root that ends keeping its descendants `blocked`, `child_failed` replaced by the `ended` child event, and the resend of `ended` and end-of-turn child events after a restore. |
| [§9.2 Elicitation Chain](../spec/09_mcp-integration.md#92-elicitation-chain) | "Interaction with `input_required` deadlock detection" removed. "Elicitation Timeout Semantics" item 1 states that a session waiting on an elicitation keeps a call in flight or a pending `request`, which counts as activity. Item 6 expires a session's pending elicitations at every suspension that stops its turn, whether or not the pod is released yet, and the `request` that raised one receives a `reply` with `outcome: "withdrawn"` (L28). |
| [§6.4 Pod Filesystem Layout](../spec/06_warm-pod-model.md#64-pod-filesystem-layout), [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow), [§10.1 Horizontal Scaling](../spec/10_gateway-internals.md#101-horizontal-scaling), [§11.3 Timeouts and Cancellation](../spec/11_policy-and-controls.md#113-timeouts-and-cancellation), and [§15.4 Runtime Adapter Specification](../spec/15_external-api-surface.md#154-runtime-adapter-specification) | Each mention of `input_required` names the waiting states, and §11.3's `maxRequestInputWait` paragraph leaves with `lenny/request_input`. |
| [§10.1.4 Coordinator-Loss Detection and Hold State](../spec/10_gateway-internals.md#1014-coordinator-loss-detection-and-hold-state), "Orphan session reconciliation" | The state list names `idle`, and `suspended` and `blocked` with a pod, in place of `input_required`. A paused session whose pod terminated stays paused with its pod reference cleared (SLR-16), in place of the move to `failed` with `orphan_pod_terminated`. |
| [§10.4 Gateway Reliability](../spec/10_gateway-internals.md#104-gateway-reliability) | The synthesized `status_change` reads the new states. |
| [§11.1 Admission and Fairness](../spec/11_policy-and-controls.md#111-admission-and-fairness) | The concurrency caps over the states of "SL-10", and **Resume admission.** at every move from `dormant`, `suspended`, or `blocked` toward a pod, a paused session's restore after a pod loss included, with the held outcome for child events, system messages, and blocked descendants. |
| [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces), **Lifecycle fence and drain.** | The parent's `cascadeOnFailure` walk becomes the `onParentEnd` walk. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), `dev.lenny.session_expired` | The expiry reasons name `maxClientIdleSeconds` for a session that cannot become `dormant`. |
| [§15 External API Surface](../spec/15_external-api-surface.md#15-external-api-surface), "SessionEvent Kind Registry" | `state_change` with the new states and no `subState`, `turn_end` with the errors `turn_lost` and `idle_timeout`, and the `tool_use` phase `withdrawn` for an approval that a suspension withdraws (L29). |
| [§15 External API Surface](../spec/15_external-api-surface.md#15-external-api-surface), **Open Responses conversation sessions.** | Condition 1 of a warm turn, "`S` is `running`", reads that `S` is `idle`, or `dormant`, in which case the gateway restores `S` through resume admission before it serves the turn. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api), the route table, the precondition table, and the error code catalog | `POST /v1/sessions/{id}/suspend` in place of `/interrupt` with its preconditions, `POST /v1/sessions/{id}/close` in place of `/terminate` under the close rule, `DELETE` unchanged, `/resume` from `suspended` and `blocked`, `SESSION_PAUSED` added, and `DEADLOCK_TIMEOUT` removed. The `/interrupt` row of the derive-failure audit table (**Derive-failure audit rows**) names `/suspend` and `/close`. `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve` and `.../deny` answer `404 RESOURCE_NOT_FOUND` for a withdrawn approval (L29). |
| [§15.4 Runtime Adapter Specification](../spec/15_external-api-surface.md#154-runtime-adapter-specification), "`MessageEnvelope` — Unified Message Format" | "A message addressed to a `suspended` session, whatever its `delivery` value" is handled under the message table of "SL-5" and the resume path of "MD-5" for every waiting and paused state. |
| [§15.2 MCP API](../spec/15_external-api-surface.md#152-mcp-api) | `suspend_session` in place of `interrupt_session`, `close_session` in place of `terminate_session`, and `get_session_status` with the new states. The approval-required `tool_use` projection, MCP `elicitation/create`, gains the `withdrawn` phase, carried as the MCP notification that the `approved` and `denied` phases use (L29). |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics) | The delegation deadlock metrics removed. A metric that labels sessions by state gains the new states. |
| [§16.5 Alerting Rules and SLOs](../spec/16_observability.md#165-alerting-rules-and-slos) | `OrphanTasksPerTenantHigh` counts `let_finish` children in place of `detach` orphans. The recovery states that the session availability SLO reads are unchanged. |
| [§17.8.1 Operational Defaults — Quick Reference](../spec/17_deployment-topology.md#1781-operational-defaults--quick-reference) | The `maxClientIdleSeconds` row names the move to `dormant`, or expiry when the session cannot become `dormant`. |
| [§18 Build Sequence](../spec/18_build-sequence.md#18-build-sequence) | The interactive sessions item names the waiting states in place of `input_required`, and the subtree deadlock detector item leaves. |
| [§21 Planned / Post-V1](../spec/21_planned-post-v1.md#21-planned--post-v1), A2A | `input-required` maps to `idle` and `dormant`. |
| [§24.17 Session Operations](../spec/24_lenny-ctl-command-reference.md#2417-session-operations) | `lenny session interrupt` becomes `lenny session suspend`, mapped to `suspend_session`, and a `lenny session close` command maps to `close_session`. |
| [§27.6 Session lifecycle and cleanup](../spec/27_web-playground.md#276-session-lifecycle-and-cleanup) | The playground's idle override bounds the idle clock in `running` and `idle`. |
| [§29.3 Interactive message send](../spec/29_communication-scenarios.md#293-interactive-message-send) and [§29.4 Interrupt, terminate, and delete](../spec/29_communication-scenarios.md#294-interrupt-terminate-and-delete) | The scenario tables take the new states, and §29.4 becomes "Suspend, close, and delete". |
| [§29.6 Restore and resume](../spec/29_communication-scenarios.md#296-restore-and-resume) | "The gateway writes the session to `running`" becomes the landing of "SL-2": `idle`, `blocked`, or `suspended`, with the delivery and the resume notice at the commit to `idle`. |

## Non-specification changes

- `pkg/api/v1/session/session.go`: the states `idle`, `dormant`, and `blocked`, `input_required` removed, `CountsTowardConcurrencyCaps` over the states of "SL-10", and the cascade values.
- `pkg/gateway/session/sessionstore` and `pgstore`: `countedStatePredicate`, the row fields `paused_ancestor_id`, `suspension_requested`, `ended_suspended`, `last_turn_end`, and `resume_notice_due`, the reason values removed, and a migration of stored `suspended` rows by their reason (`Idle` to `dormant`, the others to `suspended`) and of `input_required` rows to `idle`.
- `pkg/gateway/sessionserver`: `session_transitions.go` with the suspend and close transitions, `route_table.go` with the renamed routes, the tree suspension and tree resume, the computation of the nearest paused ancestor at every landing, the release at once for a suspension the runtime does not acknowledge, the frames of a paused session, the resume notice and the `turn_lost` report, the message table of "SL-5" in the delivery paths and the backlog resume code with the sweep that finishes a partial suspension, the TTL of held messages, `suspend_release.go` over the new states, `SESSION_PAUSED` on a paused caller's tool calls, the cascade order of "SL-9", and the `ended`, `awaiting_client_action`, `suspended`, and `resumed` child events.
- The LLM proxy and the platform and connector call paths: the refusal of a session's new calls and the cancellation of its calls in flight when its suspension cannot take hold.
- `pkg/adapter`: the `Interrupt` RPC's timeout without a session write, `SealSession` with an activity window of zero, and the activity list with the frame names of the message delivery design.
- `pkg/gateway/runtime/watchdog` and `pkg/gateway/session/sessionidle`: the idle clock in `running` and `idle`, the cut of a silent turn, the pending `request` as activity, the move to `dormant`, the expiry of a session that cannot become `dormant`, and the rule that a running descendant keeps the pod.
- `pkg/gateway/mcpfabric/mcptools`: `suspend_session`, `close_session`, `close_child_session`, and the removal of the deadlock detector's event.
- The orphan cleanup job: `let_finish` children, and `maxOrphanTasksPerTenant` applied to them.
- `sdks/client` and the `lenny` CLI: the renamed routes and commands and the new states.
- `docs/`: the session lifecycle page and the state diagram.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Every transition of "SL-2" and every refused one. `CountsTowardConcurrencyCaps` for each state. The close rule for each value of `last_turn_end`, an open turn, pending client calls, and a setup state. The message table of "SL-5" for each sender kind in each waiting and paused state and in `awaiting_client_action`. The TTL of a held message for each state, and a child event that outlives `maxResumeWindowSeconds`. Backlog resume by state, with a terminal suspension root. The landing of a setup commit, a restore commit, and a tree resume under a paused ancestor, through a chain of `blocked` ancestors and with a partly refused tree resume. Delivery of held and `delivered` messages at the commit to `idle`. The consumption of a turn stopped by a suspension, with the other `delivered` messages returned to `pending`, and no resume notice when the causing message returns. The re-pointing of a `blocked` subtree when its `blocked` ancestor ends. The withdrawal of a question and of an approval pending at a suspension, with `404 ELICITATION_NOT_FOUND` and `404 RESOURCE_NOT_FOUND` for a later answer or decision, no unattended answer after the approval timeout, and each named in the resume notice. The `onParentEnd` table, with a paused child and an `awaiting_client_action` child under `let_finish`. The idle clock in `running` with each qualifying activity, a pending `request`, and a session that cannot become `dormant`. The migration of stored rows by reason. |
| 3 | The renamed routes and tools against the OpenAPI document and the generated MCP tools, the precondition table with a recovering root, `SESSION_PAUSED`, the `state_change` payload, and the A2A and MCP Tasks mappings with `metadata.paused`, and an Open Responses warm turn on an `idle` session and on a `dormant` one. |
| 4 | On the compose stack: a suspension of a root with a running child and an idle child, a held child event, a resume by message and by route, the resume notice opening a turn in the cut child ahead of a held correction, an idle parent keeping its pod while a child runs, a dormant parent woken by a child event, a silent turn cut to `dormant`, `close_child_session` on an idle and a running child, and a cascade under each value. |
| 5 | On Kind: a tree resume after the hold released every pod, a restore during a tree suspension landing in `blocked`, a parent restored after it consumed a child's `ended` and `end_of_turn` events, which receives both again, and the budget keys of a root that stays `dormant`. |
| 7a | A suspension racing a child's start, a child event racing a suspension, a turn end racing a suspension, and a close racing a turn end, each leaving one outcome. |
| 8 | A suspension that cannot take hold while a connector call and a model call are in flight, which cancels them and releases the pod, a cancelled `start_child_session` whose child was created and one that was not, and a cancelled `await_child_sessions` whose events return to `pending`, and one whose checkpoint keeps failing, released at `maxSuspendedPodHoldSeconds`. Pod loss of a parent with settled and running children, pod loss of a `running` child with its `turn_lost` report and resume notice, pod loss of a `blocked` session that stays paused, and a coordinator crash during a tree suspension that the sweep finishes. |
| 9 | A parent's message, a child event, a system message, and a `send_message` with `fromSessionId` cannot lift a suspension, and a paused runtime's `start_child_session` is refused. |
| 10 | A Full-level runtime ends its turn on `interrupt_request` and resumes from the notice, a Basic runtime's suspension releases its pod at once, and a runtime that reports `busy` through a long tool run keeps its turn. |
| 11 | The lifecycle page's state diagram against the transition table. |

## Validation owed

- Whether each reference harness ends its turn promptly on `interrupt_request` and what it writes when it does, which decides how often "SL-3" falls back to the release at the deadline.
- How each reference harness treats the resume notice, which decides whether its text needs to vary by runtime.
- Whether each reference runtime reports `busy` on heartbeats through a long tool run inside the pod, which decides how often "SL-6" cuts a working turn.
- Whether models leave a boolean tool argument unset more often than they set it, which "SL-7" **One tool.** assumes.
- How many sessions an operator's dashboards count by state today, which decides how the migration of stored `suspended` rows is announced.

## Instructions for reviewers of other designs

- [`message-delivery-design.md`](message-delivery-design.md): revision 12 links this design from "MD-22", carries the child events `ended`, `awaiting_client_action`, `suspended`, and `resumed` and the `end_of_turn` errors `turn_lost` and `idle_timeout`, lists the resume path over `dormant`, `suspended`, `blocked`, and `awaiting_client_action`, and states the TTL of held messages, the frame rule of a paused session in "MD-4", and the exports at a suspension that releases the pod at once in "MD-18". Revision 12 adds the commit to `idle` as a delivery point, the consumption rule of a stopped turn (B19), and pointers to SL-A for the rows the two designs share. No further change.
- [`delegation-availability-design.md`](delegation-availability-design.md): revision 8 retires `detach`, glosses `onParentEnd`, and takes the tool names. No further change.
- [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): a held session waits in `idle` or `dormant`, and a held session that becomes `dormant` wakes for the next request through resume admission and a restore.
- [`client-tools-design.md`](client-tools-design.md): a session with pending client calls is `idle`, and a close while calls are pending records `cancelled` (L21). Its reviewer states how the close rule reads the pending set.
- [`security-oversight-design.md`](security-oversight-design.md): an approval pending when a suspension stops the turn is withdrawn with the `tool_use` phase `withdrawn`, its timeout and the unattended policy no longer answer it, and a later decision answers `404 RESOURCE_NOT_FOUND` (L29). SO-5a's approval path states the withdrawal and its audit record. A pending approval counts as activity for the idle clock.
- [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md): the checkpoint at the move to `dormant` is the idle-suspension checkpoint of today, and a suspension that the runtime does not acknowledge takes the same checkpoint at once.
- [`session-derive-design.md`](session-derive-design.md): a derive reads a source in any waiting or paused state as it reads a `suspended` source today.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): no change.
- Every design that names `POST /interrupt`, `POST /terminate`, `interrupt_session`, `terminate_session`, `cascadeOnFailure`, `detach`, or `input_required`: the cross-design sync takes the names of this design.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| SL-A | The states and their rules: "SL-1" to "SL-7" and "SL-9" to "SL-11", with every specification change above except those SL-B takes. Where those parts state a child event, SL-B lands that statement, and SL-A lands the session's own reports: `turn_end` with `turn_lost` and `idle_timeout`, and the resume notice. SL-A owns the rows it shares with the message delivery design, the §28.5.3 `interrupt_request` row with §4.7's `Interrupt` RPC and the TTL rows of the §7.2 dead-letter table, and that design's rows point to it. Until MD-E lands, its text names the end-of-turn frame by its specification name, `response`, the delegation tools by their specification names, `lenny/delegate_task`, `lenny/cancel_child`, and `lenny/await_children`, and its qualifying activity takes the frame names of MD-E when MD-E lands. | None. It lands with MD-A of the message delivery design, in one proposal or in two applied together, because MD-A's delivery paths name the new states and SL-A's transitions read MD-A's turn accounting. |
| SL-B | Every child event of the lifecycle, wherever this design states it: "SL-8" and "SL-12", the `suspended` and `resumed` child events of "SL-4", the `end_of_turn` child events with `turn_lost` and `idle_timeout` of "SL-3" and "SL-6", the return to `pending` of the child events a cancelled `await_child_sessions` had taken, and the resend of `ended` and end-of-turn child events, with the parts of the §7.3, §8.8, and §8.10 rows that state them. | SL-A, and MD-E of the message delivery design for the child events. |

## Sources

The owner's review of 2026-11-11, the adversarial review of revision 1 of the same day, and the specification and tree as read on that date. No external page was read for this design.

## Review history

An adversarial review read revision 1 on 2026-11-11. It found high findings H1 to H6, medium findings M1 to M14, and low findings 1 to 7, which the notes below call low findings to keep them apart from the decisions L1 to L22, across this design and revisions 10 and 6 of the message delivery and delegation availability designs, and it asked the questions the owner answered as L15 to L22. Revision 2 applies them. A re-review read revision 2 with the message delivery design's revision 11 and the delegation availability design's revision 7 on 2026-11-11, and revision 3 applies its findings with the owner's decisions L23 to L27. A regression review read revision 3 the same day, and revision 4 applies its findings with the owner's decision L28.

### Revision notes

- Revision 1, 2026-11-11: first draft, from the owner's review of the combined lifecycle of a parent and its children, which settled L1 to L14. The review began from the adversarial review of revision 9 of the message delivery design, whose findings H2 (completion of children), M8 (a child event undoing a suspension), and M10 (a parent's completion racing its children's events) this design resolves, and whose finding M4 (the sections that name `input_required`) it covers for the state machine. Choices this revision makes within the owner's decisions: the row fields of "SL-1", the text of the resume notice, the error code `SESSION_PAUSED`, the close rule's treatment of a last turn that ended with `error` and of a close during setup, the routes `POST /v1/sessions/{id}/close` and `close_session`, the application of `maxOrphanTasksPerTenant` to `let_finish`, and the removal of the deadlock detector.
- Revision 2, 2026-11-11: applies the adversarial review of revision 1 and the owner's answers L15 to L22.
  - H3 and L15: a suspension that the runtime does not acknowledge, or cannot, seals, checkpoints, and releases the pod at once, and frames a paused session writes open no turn and change no state ("SL-3", SLR-3, SLR-18). The `Interrupt` RPC's timeout writes no session state, which also answers M7.
  - H4: backlog resume resumes a `blocked` session only when its suspension root has resumed, and a root that ended while `suspended` records `ended_suspended` and keeps its descendants `blocked` ("SL-5", "SL-9", SLR-15).
  - H5 and L16: a pod loss that cuts a turn sets `resume_notice_due`, the restored session receives the resume notice, and its client receives a `turn_lost` report ("SL-3", "SL-8", SLR-19).
  - H2: a message held for a `dormant`, `suspended`, or `blocked` session takes no TTL, and a child event never expires while its parent lives ("SL-5", SLR-14).
  - M1: every landing computes the nearest paused ancestor, setup states join step 4 of "SL-4", step 5's claim that no child can start during a suspension is removed, and SLR-4 exempts a resume the root's client requests for the session.
  - M2: a paused session that loses its pod stays paused without one, and the `return_state` field leaves, because every restore now lands in `idle` unless a suspension applies (SLR-16).
  - M3: the `awaiting_client_action` clock stops while an ancestor is suspended (SLR-21).
  - M4 and L17: the idle clock runs in `running` and `idle`, a silent turn is cut and the session becomes `dormant` with an `idle_timeout` report, a pending `request` counts as activity, the qualifying activity takes the current frame names, a session that cannot become `dormant` expires, and the `maxSessionAge` row of "SL-10" follows §6.2's pause table ("SL-6", SLR-17).
  - M5: a message from the session's client resumes a session in `awaiting_client_action`, top-level or child ("SL-5", "SL-2").
  - M6: the end-of-turn `message` that closes a turn stopped by a suspension leaves the turn cut, produces no `end_of_turn` child event, and is never a result, and a turn that ended before the suspension is not cut ("SL-3").
  - M9 and L18: `blocked` joins the suspend precondition, and a recovering top-level session can be suspended through `suspension_requested` ("SL-4").
  - M10: the resume notice is written before held messages, and no notice is written when the root's client's own message lifts the suspension ("SL-3", SLR-7).
  - M11 and L19: end-of-turn child events are resent from the child's transcript with their original message ids ("SL-12", SLR-11).
  - M12: the suspension is recorded on the suspension root's row first, and the backlog sweep finishes a partial suspension ("SL-4", SLR-20).
  - M13: the specification-change table gains §4.7.1 **Session seal.**, the `Interrupt` RPC, the §28.5.3 `interrupt_request` rows, §15.4.3, §5.2, the §7.2 dead-letter table and "Mid-resume terminal transitions", §9.2 "Elicitation Timeout Semantics", `DEADLOCK_TIMEOUT`, §14, §16.1, §16.5, §17.8.1, §18, §24.17, and §27.6, and the §6.2 row names "Pod failure while `suspended`" and the per-state tables.
  - Low finding 3: "Terms" defines the integration levels, the delegation lease, `messagingRateLimit`, `perChildMaxAge`, the session seal, and `busy`, the label row names every message delivery label this design cites, and table cells are complete sentences.
  - Low finding 4 and L20: the tree's budget keys are re-armed when the root enters `dormant` ("SL-10").
  - Low finding 5: under `let_finish`, a child in `awaiting_client_action` is closed at once, and a recovering child is handled by where its restore lands ("SL-9").
  - Low finding 6: the root's client is the authenticated principal that owns the root ("Terms", "SL-4", SLR-22).
  - L21: a close while client tool calls are pending records `cancelled` ("SL-7", SLR-8).
  - L22: `suspended` and `blocked` map to `working` with `metadata.paused: true` ("SL-11").
  - Choices this revision makes within the owner's decisions: the row fields `suspension_requested`, `ended_suspended`, and `resume_notice_due` with its cause, the texts of the resume notice by cause, the `end_of_turn` errors `turn_lost` and `idle_timeout`, the absence of a resume notice after an idle cut and after a restore that delivers the cut turn's causing message again, the activity window of zero for the seal at a suspension, and the sweep as the recovery of a partial suspension.
- Revision 3, 2026-11-11: applies the re-review of revision 2, whose findings these notes write as RR-H1 and so on, and the owner's decisions L23 to L27.
  - RR-H1: the commit to `idle` and the adoption of an `idle` session are delivery points ("SL-2", "SL-4", "SL-5", SLR-26). The message delivery design names them in "MD-3" and "MD-5".
  - RR-H2 and L24: a suspension that cannot take hold refuses and cancels the session's LLM-proxy, platform, and connector calls before the seal, retries a failed checkpoint, and forces the release at `maxSuspendedPodHoldSeconds` ("SL-3", SLR-18, SLR-23). "Limits" states that a direct-mode runtime stops calling its provider only at the release, and the §4.9 and §6.2 rows follow.
  - RR-H3: a §15 row rewrites condition 1 of an Open Responses warm turn to `idle`, or `dormant` through a restore.
  - RR-M2, RR-M3, and L25: the nearest paused ancestor is `suspended`, `blocked`, carries `suspension_requested`, or ended while `suspended`. `suspension_root_id` becomes `paused_ancestor_id`, a tree resume proceeds top-down from each session that enters a live state, and a refused session keeps its subtree `blocked`. A client's resume of a root in `awaiting_client_action` resumes its `blocked` descendants when the root's restore commits ("SL-4", SLR-6).
  - RR-M4: the gateway decides between the resume notice and a redelivery after the adapter's deduplication ("SL-3", SLR-19).
  - RR-M5 and L26: a turn stopped by a suspension consumes only the ids its closing frame lists (SLR-24).
  - RR-M6 and L23: the budget keys are re-armed when the root enters and leaves `dormant`, and §8.3's clause on time outside `suspended` covers `dormant`.
  - RR-M7: a close and a cancel apply the session's `onParentEnd` ("SL-7", SLR-25).
  - RR-M8: the specification-change table gains §10.1.4 "Orphan session reconciliation", §12.8, §15.4.1, §29.6, §6.2 "`resuming` failure transitions" with the replay row in "SL-2", and the derive-failure audit table of §15.1.
  - RR-M9: SL-A splits into SL-A, which lands with MD-A, and SL-B, which follows MD-E. SL-A owns the §28.5.3 `interrupt_request` row and the §7.2 dead-letter table.
  - Low findings: the `awaiting_client_action` row of "SL-2" admits the root's client, the leader's sweep hands each descendant to its coordinating replica, "Terms" defines a live state, `one_shot`, `maxSuspendedPodHoldSeconds`, `maxSuspendedSessionSeconds`, `cascadeTimeoutSeconds`, `maxClientIdleSeconds`, and the LLM proxy, the labels name MD-A and MD-E, the fragments of "High-level design" and "Alternatives rejected" are full sentences, and "SL-7" **One tool.** carries **[unverified]** with an entry in "Validation owed".
  - L27: the owner accepted revision 2's choices on the `idle_timeout` report and on the notice after a restore.
  - Choices this revision makes within the owner's decisions: the name `paused_ancestor_id`, the hand-off of the sweep to the coordinating replica, and the replay row of "SL-2".
- Revision 4, 2026-11-11: applies the regression review of revision 3, whose findings these notes write as RG-H1 and so on, and the owner's decision L28.
  - RG-H1: when the closing frame of a stopped turn leaves the causing message unconsumed, its return to `pending` restarts the work, so the gateway writes no resume notice ("SL-3", SLR-7). The worked example states that `claude-code` reported its task consumed, so `sess_B` keeps the notice.
  - RG-M1: the L24 refusal applies to the pod in use and ends at its release (SLR-23, the §4.9 row).
  - RG-M2: "SL-3" **Cancelled calls.** states the outcome of each cancelled call, the delegation tools by name, and the resume notice names each committed effect.
  - RG-M3: a `blocked` session that ends re-points its `blocked` descendants to its own paused ancestor, and backlog resume recomputes a nearest paused ancestor that is terminal ("SL-9", SLR-28).
  - L28: a question pending at a suspension that stops the turn is withdrawn under §9.2 item 6, and the resume notice names it ("SL-3" **Pending questions.**, SLR-27, the §9.2 row).
  - Low findings: "SL-10" and "SL-4" state the forced release and the checkpoint retry. "Terms" defines adoption, and SLR-26 names the same delivery and redelivery points as the message delivery design. SL-A and SL-B each own a stated set of items, and SL-A stages the delegation rules on the specification's tool names until MD-E. The §28.5.3 row points to the message delivery design for the `queue` fallback. The `MessageEnvelope` row cites §15.4.
  - Choices this revision makes within the owner's decision: while the pod is held, a withdrawn question ends the runtime's wait with a `reply` with `outcome: "withdrawn"` for a `request`, and with an error that names the withdrawal for an `ask_question` call.
- Revision 5, 2026-11-11: applies the owner's decision L29, which withdraws a tool-use approval pending when a suspension stops the turn as L28 withdraws a question. "SL-3" **Pending questions and approvals.** covers both, SLR-27 follows, and the §15, §15.1, and §15.2 rows add the `tool_use` phase `withdrawn`, the refusal of a later decision, and its MCP projection. §4.8 and §9.2 state no approval timeout or unattended policy: the timeout belongs to SO-5a of the security oversight design and the unattended policy to "MD-19" of the message delivery design, so the reviewer instruction to the security oversight design changes.
  - Choices this revision makes within the owner's decision: the phase `withdrawn` on the `tool_use` event, `404 RESOURCE_NOT_FOUND` for a later decision, a `reply` with `decision: "withdrawn"` for a harness approval, and an error result that names the withdrawal for a held `tool_call`.
