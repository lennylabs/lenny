# Messaging, Conversational Patterns & Multi-Turn Interactions Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md`
**Perspective:** 23. Messaging, Conversational Patterns & Multi-Turn Interactions
**Category code:** MSG
**Reviewer focus:** Message delivery path completeness and edge cases; `input_required` / session lifecycle integration; agent-team (sibling coordination) pattern; SSE buffer overflow handling; message routing interaction with delegation policies.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1     |
| High     | 4     |
| Medium   | 5     |
| Low      | 3     |
| Info     | 2     |

---

## Critical

### MSG-001 Session Inbox Has No Persistence, Size Bound, or Durability Contract [Critical] — VALIDATED/FIXED
**Section:** 7.2

The delivery routing specification (paths 2 and 3) routes messages to a session "inbox" when the runtime is unavailable or blocked in `await_children`. The spec states "The gateway never drops undelivered messages; they remain in the session inbox until consumed or the session terminates." However, the inbox is never defined as a data structure anywhere in the spec. There is no statement of:

- Whether the inbox is in-memory (per gateway replica) or durable (Postgres/Redis).
- What happens to inbox contents if the coordinating gateway replica crashes between receiving a message and delivering it to the runtime. The session coordination lease will be picked up by a new replica, but an in-memory inbox is gone.
- What the maximum inbox size is. The spec imposes no bound, directly contradicting the "never drops" guarantee: an agent blocked in `await_children` for the full `maxSessionAge` (7,200 s) while receiving a flood of inter-session messages could accumulate unbounded memory on the gateway.
- Whether inbox contents survive a session pod failure and recovery cycle (Section 7.3). The resume flow reconstructs session state from Postgres checkpoints, but no checkpoint spec mentions inbox contents.

The DLQ (for recovering sessions) is separately defined with TTL semantics, but the live-session inbox is given a stronger "never drops" guarantee with zero supporting infrastructure. The inconsistency is a correctness gap: the guarantee cannot be implemented without durability.

**Recommendation:** Define the inbox as a named, bounded, durable structure stored in Redis (sorted set keyed by `session_id`, ordered by arrival timestamp, with a configurable `maxInboxSize` per session). Specify that inbox contents are replicated from Redis on coordinator handoff, and that inbox entries surviving a pod failure are delivered in order on resume alongside the existing checkpoint-based state. Document the `maxInboxSize` cap (suggested default: 500 messages) and the behavior on overflow: drop oldest with a `message_dropped` event to the sender, consistent with the SSE buffer drop-and-reconnect pattern.

**Resolution:** Section 7.2 now defines the session inbox as an in-memory bounded FIFO queue on the coordinating gateway replica with a 5-row contract table: backing store (in-memory only), size bound (`maxInboxSize` default 500), overflow behavior (drop oldest, emit `message_dropped` receipt with `reason: "inbox_overflow"`), durability (none — lost on coordinator crash), crash recovery (new coordinator starts empty; senders must re-send). The "never drops" guarantee was scoped to "while the coordinating replica remains alive." DLQ updated to specify Redis-backed sorted set with `maxDLQSize` cap of 500. `dropped` added as a new `deliveryReceipt.status` value.

---

## High

### MSG-002 Message Delivery Paths Are Missing the `suspended` State [High] — Fixed
**Section:** 7.2

The four delivery paths (Section 7.2) cover: (1) `inReplyTo` resolves a `request_input`, (2) runtime available, (3) runtime blocked in `await_children`, and (4) terminal/recovering state. The session state machine (Section 7.2) defines a `suspended` state (from an `interrupt_request`), but `suspended` does not appear in any of the four delivery paths. It is neither categorised as "available" (the runtime process is paused) nor as "recovering" (the pod is still live and the DLQ TTL logic does not apply). The spec therefore has a routing gap: messages sent to a `suspended` session via `lenny/send_message` have no defined handling path.

Additionally, `suspended → running` can be triggered by `POST /v1/sessions/{id}/messages` with `delivery: immediate`, but this is only documented in Sections 6.2 and 7.2 for the external client API. Whether a pod-to-pod `lenny/send_message` with `delivery: immediate` also transitions the target out of `suspended` is unspecified. If it does not, a parent agent trying to unblock a suspended child via `lenny/send_message` with `delivery: immediate` will get a `queued` receipt with no mechanism to trigger the resume.

**Recommendation:** Add a fifth delivery path (or clearly expand path 2) to address `suspended`: "Target session is `suspended` → buffer in inbox; if `delivery: immediate` is set, atomically resume the session (same as `POST /v1/sessions/{id}/messages delivery:immediate`) before delivering." Specify explicitly whether pod-to-pod `lenny/send_message` with `delivery: immediate` can resume a suspended session, and document this in the messaging scope and delivery path tables.

**Resolution:** Section 7.2 delivery routing now has five paths instead of four. New path 4 covers `suspended` sessions: messages are buffered in the session inbox by default; if `delivery: "immediate"` is set, the gateway atomically resumes the session and delivers the message. This behavior applies uniformly to all message sources (external client and `lenny/send_message`), explicitly resolving the pod-to-pod resume question. The previous path 4 (terminal/recovering) is now path 5.

### MSG-003 `input_required` State Missing from Session Lifecycle State Machine in Section 7.2 [High] — Fixed
**Section:** 7.2, 8.9

The canonical task state machine in Section 8.9 includes `input_required` as a reachable state (via `lenny/request_input`). The session lifecycle state machine in Section 7.2 does not. The two state machines co-exist for the same logical entity (a running session = a running task), and `input_required` is effectively a sub-state of `running` from the session manager's perspective. However:

- The `status_change(state)` gateway→client streaming event (Section 7.2) is the mechanism by which external clients observe session state. It is unclear whether `input_required` is ever surfaced as a `status_change` event to the external client, or whether the client only sees `running` while the agent is blocked.
- The dead-letter handling table (Section 7.2) does not list `input_required` as a target session state. If a sibling sends a message to a session in `input_required`, it is not covered by path 1 (no `inReplyTo`) nor path 2 (runtime is not "available" — it is blocked in the MCP tool call), nor path 4 (not terminal/recovering). It falls through to inbox buffering, but this is not stated.
- `lenny/await_children` explicitly unblocks on `input_required` (Section 8.9), meaning the parent observes the child entering this state. But a sibling (at `siblings` scope) receiving the task tree via `lenny/get_task_tree` and seeing a peer in `input_required` has no specified behavior for what happens when it sends a message to that peer.

**Recommendation:** Add `input_required` to the Section 7.2 session state machine as a sub-state of `running`. Specify that `status_change(state: "input_required")` is emitted to the external client when the session enters this sub-state, so external observers and sibling agents can reason about it. Add `input_required` as a named case to the delivery path table: messages without a matching `inReplyTo` are buffered in inbox; messages with a matching `inReplyTo` resolve the blocked tool call (path 1). Clarify that `delivered` receipt status is returned in the `inReplyTo` case.

**Resolution:** Section 7.2 session state machine now includes `input_required` as a sub-state of `running`, with transitions mirroring the canonical task state machine (Section 8.8): `running → input_required` (via `lenny/request_input`), `input_required → running` (input provided or request resolved), `input_required → cancelled`, and `input_required → expired`. The `status_change` event description explicitly lists `input_required` alongside `suspended`. A new delivery path 4 covers `input_required` sessions: messages without a matching `inReplyTo` are buffered in the session inbox with a `queued` receipt; messages with a matching `inReplyTo` continue to be handled by path 1 with a `delivered` receipt. The previous paths 4 and 5 are renumbered to 5 and 6 respectively, with internal path references updated.

### MSG-004 Sibling Coordination Protocol Is Underspecified for Concurrent `input_required` [High] — Fixed
**Section:** 7.2, 8.5, 8.9

The spec introduces `lenny/get_task_tree` + `lenny/send_message` under `siblings` scope as the mechanism for sibling coordination (Section 8.5, Section 7.2). This is architecturally sound, but the protocol for concurrent coordination among siblings is not specified at all. Key gaps:

1. **Ordering guarantees:** When two siblings both call `lenny/send_message` to the same third sibling concurrently, what is the delivery order? The inbox is FIFO but the spec does not define the "arrival timestamp" to use — is it the time the gateway receives the call, the time it acquires a Redis lock, or something else? Under multi-replica gateway operation, two messages arriving at different replicas simultaneously have a race-condition ordering.

2. **Shared state coordination:** Sibling coordination typically involves one sibling broadcasting a result that others should act on. The spec provides `lenny/send_message` (point-to-point) but no broadcast primitive. Siblings that want to notify all peers must enumerate them from the task tree and send N individual messages. If new siblings are spawned between the enumeration and the send loop, they are missed. No guidance is given.

3. **Token budget implications:** The `messagingRateLimit` applies per-session. In a wide fan-out tree (`maxParallelChildren: 10`, `siblings` scope), a coordination storm where every sibling messages every other sibling generates O(N²) messages against each sibling's rate limit. The spec does not warn about this or recommend a design pattern to avoid it.

4. **Parent-to-child messaging does not enforce `messagingScope`** (parent→child is always permitted via `lenny/send_message` with `direct` scope since the parent created the child). But there is no equivalent `lenny/send_to_parent` tool. A child that wants to reply to a sibling's message must use `lenny/send_message` (which requires `siblings` scope). The asymmetry is not documented.

**Recommendation:** Add a "Sibling Coordination Patterns" subsection to Section 7.2 or 8.5 that: (a) defines message ordering semantics under concurrent delivery (recommend Redis `ZADD` with gateway-replica wall-clock + replica-ID tiebreaker); (b) documents the absence of a broadcast primitive and the N-message enumeration approach with its race; (c) warns about O(N²) messaging storms and recommends maximum fan-out limits for `siblings`-scoped deployments; (d) documents the `send_to_parent` gap and whether it is planned or out of scope.

**Resolution:** Section 7.2 now includes a "Sibling coordination patterns" subsection covering all four gaps: (1) message ordering is defined as coordinator-local FIFO — the coordinating gateway replica timestamps messages on receipt and only one replica coordinates a given session, eliminating multi-replica races; agents requiring causal ordering should use application-level sequence numbers; (2) the absence of a broadcast primitive is documented as intentional, with the enumeration-via-task-tree approach and its snapshot race noted, and a coordinator pattern recommended for reliable broadcast; (3) O(N²) storm risk is explicitly warned about with guidance to reduce per-session rate limits proportionally and prefer hub-and-spoke patterns for high-fan-out trees; (4) the `send_to_parent` asymmetry is documented as intentional — parent-to-child is always permitted via `lenny/send_message` with `direct` scope (the parent created the child and holds the lease), while child-to-parent uses `lenny/send_message` (requires `messagingScope: direct` or wider) or `lenny/request_input` for synchronous hand-offs, preserving the top-down control model.

### MSG-005 Delegation Policy Scope Does Not Govern `lenny/send_message` Across Delegation Tree Boundaries [High] — Fixed
**Section:** 7.2, 8.3, 13.5

The `messagingScope` setting controls which sessions can be targeted by `lenny/send_message` (`direct` or `siblings`). Section 13.5 cites `messagingScope` as a security control. However, the scope is evaluated against the task tree structure — specifically, parent/child/sibling relationships — but the `DelegationPolicy` controls which *runtimes* a session is allowed to delegate to. These two controls are orthogonal and their interaction is not specified:

1. A session with `siblings` scope can message any sibling, regardless of whether the parent's `DelegationPolicy` would have allowed that sibling's runtime to be delegated to. The runtime that receives the message was delegated by the same parent (so it is trusted at creation time), but the *content* of cross-sibling messages is not governed by `contentPolicy.interceptorRef` — only `delegate_task` calls are intercepted. A compromised sibling can send adversarial prompts to its peers.

2. The `messagingRateLimit` in the delegation lease applies per-session, but it is set at delegation time. The leaf lease cannot be stricter than the parent's lease granted it. This means a sibling session's messaging rate limit is determined by the parent's choice at delegation time, not by the receiving sibling's runtime configuration. If the parent grants all children the same messaging rate limit and one child is compromised, it can exhaust its own rate limit but also flood siblings until each of their rate limits is hit independently — the total messaging rate to a single victim sibling is N × rate_limit for a tree of N compromised siblings.

3. The spec does not specify whether `contentPolicy.interceptorRef` (Section 8.3) applies to `lenny/send_message` payloads or only to `delegate_task` payloads. Section 13.5 lists `messagingRateLimit` and `messagingScope` as content security controls, but not `interceptorRef`. The gap means inter-session messages bypass the content scanning hook entirely.

**Recommendation:** (a) Specify that `contentPolicy.interceptorRef` (when set) is also invoked at the `PreMessage` hook phase for `lenny/send_message` payloads, not only at the `PreDelegation` phase. (b) Add a tree-wide aggregate messaging rate limit (e.g., `messagingRateLimit.maxTreePerMinute`) alongside the per-session limit, so a compromised subtree cannot flood a single target by parallelising senders. (c) Document the security model explicitly: sibling content is not intercepted by default; deployers who enable `siblings` scope without `interceptorRef` accept the risk.

**Resolution:** All three sub-issues addressed. (a) Already fixed by SEC-105: the `PreMessageDelivery` interceptor phase (Section 4.8) applies `contentPolicy.interceptorRef` to `lenny/send_message` payloads, documented in Section 7.2 and Section 13.5. (b) A new `maxInboundPerMinute` field (default: 60) added to `messagingRateLimit` in the delegation lease (Section 8.3). This is enforced on the receiving session as a tree-wide aggregate cap — regardless of how many senders contribute, the target accepts at most `maxInboundPerMinute` messages per sliding window. Messages exceeding the limit receive a `RATE_LIMITED` delivery receipt. The O(N²) storm risk guidance in Section 7.2 sibling coordination patterns now references this enforced limit. Section 13.5 mitigation list updated to describe both outbound and inbound rate limits. (c) Already documented: Section 13.5 states the residual risk without `contentPolicy.interceptorRef`, and Section 22.3 explicitly notes that deployers without interceptors accept content-level risk.

---

## Medium

### MSG-006 `ready_for_input` Signal Has No Defined gRPC RPC or Protocol Contract [Medium]
**Section:** 7.2, 15.4, 15.4.3

Delivery path 2 states: "A runtime is considered *available* when it is actively reading from stdin — that is, its adapter reports `ready_for_input` (between tool calls, after emitting output, or during any explicit input-wait)." This signal is critical to delivery path correctness: without it the gateway cannot distinguish a runtime that is genuinely reading stdin (safe to write) from one that is executing a tool call and not reading (unsafe to write — the message would be interleaved with a tool result).

However, `ready_for_input` does not appear anywhere in the adapter protocol specification (Section 15.4), the gRPC lifecycle RPC table (Section 4.7), the outbound message type table (Section 15.4.1), or the integration tier matrix (Section 15.4.3). There is no `{type: "ready_for_input"}` outbound message type defined in the binary protocol, and no corresponding RPC in the internal control API (Section 15.3). The protocol trace (Section 15.4.1) does not show this signal. Minimum-tier runtimes cannot produce it because they have no MCP or lifecycle channel.

**Recommendation:** Define `ready_for_input` as a concrete protocol artifact. Options: (a) an outbound binary protocol message `{type: "ready_for_input"}` after each `response` or `tool_result`, required for Standard/Full tiers; or (b) an implicit heuristic — adapter infers ready-for-input by tracking outstanding `tool_call` IDs (ready when outstanding count == 0 and no `response` parts are streaming). Document explicitly which approach is used, specify the behavior for Minimum-tier adapters (which cannot signal readiness), and add the signal to the tier matrix (Section 15.4.3).

### MSG-007 SSE Buffer Overflow Drops Connection Without Notifying Sender [Medium]
**Section:** 7.2

The SSE buffer policy states: "If a slow client falls behind and the buffer fills, the gateway drops the connection and the client must reconnect with its last-seen cursor." This is the correct backpressure response for external clients (they reconnect and replay). However, for inter-session messages delivered via `lenny/send_message`, the delivery receipt says `delivered` as soon as the message is written to the session inbox or stdin. If the *target session's gateway→client SSE buffer* subsequently overflows and the client disconnects, the events that were "delivered" to the agent (and may have produced output) are lost from the client's event stream. The client reconnects and replays from the EventStore, but the spec does not specify whether the `agent_output` events produced in response to inter-session messages are persisted to the EventStore.

Additionally, the buffer overflow drop is a silent disconnection from the agent's perspective — the agent does not know the client dropped. For a human-interactive session where the agent has been injected with a follow-up message (path 2) and produces a response that exceeds the SSE buffer, the client misses the response without notification beyond a dropped connection. The `checkpoint_boundary` mechanism handles events older than the checkpoint window, but buffer overflow is a different failure mode for current events.

**Recommendation:** (a) Specify that all `agent_output` events, including those triggered by inter-session message injection, are persisted to the EventStore before being written to the SSE buffer. This is implied by the reconnect-with-cursor design but not stated. (b) Add a metric `lenny_sse_buffer_overflow_total` (labeled by session and cause) to the observability section. (c) Consider emitting a `buffer_overflow` synthetic event into the EventStore at the drop point, so clients that reconnect can distinguish "I reconnected after a buffer overflow" from "I reconnected after a network glitch."

### MSG-008 Dead-Letter Queue Has No Maximum Size or Bounded Memory Contract [Medium]
**Section:** 7.2

The DLQ for recovering sessions (`resume_pending`, `awaiting_client_action`) has a configurable TTL but no defined maximum size. The spec notes the TTL defaults to `maxResumeWindowSeconds` (900 s). During a 900-second recovery window, a session with `siblings` scope in a large fan-out tree could receive an unbounded number of messages from peers (subject only to each sender's per-session rate limit of 30/min × N senders). For a 50-sibling tree each at 30 msg/min, a recovering session could accumulate 1,350 messages in its DLQ before the TTL expires. The spec gives no indication of where the DLQ is stored (Redis? Postgres? gateway memory?) or what happens when it grows large.

**Recommendation:** Define the DLQ backing store (recommend Redis sorted set with `session_id:dlq` key, scored by expiry timestamp) and specify a `maxDLQSize` cap (suggested default: 500 messages). On overflow, drop the oldest DLQ entries and emit a `message_dropped` event to each affected sender via the `delivery_receipt` mechanism. Document the DLQ size cap alongside the SSE buffer cap in the per-session memory footprint analysis (Section 8.2 delegation tree memory management).

### MSG-009 `delivery: "immediate"` Field Value in Protocol Trace Conflicts with Enum in `MessageEnvelope` [Medium]
**Section:** 15.4.1

The `MessageEnvelope` specification (Section 15.4.1) documents `delivery` as an optional mechanical hint where `"immediate"` means deliver at next stdin read. The protocol reference trace (Section 15.4.1) shows an example message on stdin:

```json
{"type": "message", "id": "msg_001", "input": [...], "from": "client", "threadId": "t_01", "delivery": "at-least-once"}
```

`"at-least-once"` is not defined as a valid `delivery` value anywhere in the `MessageEnvelope` spec. The only defined value is `"immediate"` with "absent means queue for next natural pause." `"at-least-once"` reads as a delivery *guarantee* qualifier (a semantics dimension), not a timing hint. If it is an intentional valid value, its semantics must be defined. If it is a copy-paste error in the example, it will confuse runtime authors reading the protocol reference.

**Recommendation:** Remove `"at-least-once"` from the example trace and replace with `"immediate"` or omit the field. If `"at-least-once"` is intended as a future delivery guarantee annotation (complementary to `"immediate"` timing), define it explicitly in the `MessageEnvelope` spec with semantics and interaction rules.

### MSG-010 Sibling `lenny/send_message` Routing Is Not Subject to Delegation Policy Approval Modes [Medium]
**Section:** 7.2, 8.3, 8.4

Delegation (`lenny/delegate_task`) has three approval modes: `policy` (auto-approve), `approval` (pause parent for client approval), and `deny`. The `approval` mode exists specifically so a human can review what agent tasks are being spawned. However, inter-session messaging via `lenny/send_message` carries no equivalent approval mechanism. A session in an `approval`-mode delegation tree can send content to its children, siblings, or parent without any human review, despite the parent operator having opted into human-in-the-loop oversight for task *creation*. The security model of Section 13.5 does not address this gap: it lists messaging rate limits and scope as controls, but not approval-mode consistency.

**Recommendation:** Add a note to Section 8.4 clarifying that approval modes govern task delegation (creation of new child sessions), not inter-session message delivery. If deployers need messaging approval, they should combine approval mode with `contentPolicy.interceptorRef` and the `PreMessage` hook (see MSG-005). Document this as an explicit design decision so deployers with strict oversight requirements know the boundary.

---

## Low

### MSG-011 `threadId` Is Reserved but Has No v1 Semantics or Validation [Low]
**Section:** 15.4.1

The `MessageEnvelope` spec says `threadId` is "optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1." The spec also states that `MessageEnvelope` is "future-proof" because it accommodates "threaded messages" without schema changes. However, `threadId` is injected by the adapter from execution context (the spec says "adapter-injected fields — runtime never supplies these"), yet the adapter protocol never specifies what value the adapter should inject for the single implicit v1 thread. Different adapters could inject `null`, `""`, `session_id`, or `"default"`, causing inconsistency in the EventStore for cursors and replay. Additionally, if a client sends a message with an unexpected `threadId`, there is no specified validation or rejection behavior.

**Recommendation:** Specify the canonical v1 `threadId` value (recommend `"default"` or the session ID) and require all adapters to inject this value. Specify that messages with any other `threadId` value in v1 are rejected with `VALIDATION_ERROR` until multi-thread support is added.

### MSG-012 `lenny/request_input` Timeout Behavior on Runtime Termination Is Unspecified [Low]
**Section:** 7.2, 8.9

`lenny/request_input` "blocks until answer arrives." The spec documents that `input_required → expired` when the deadline is reached, and `input_required → cancelled` when the parent cancels. But what happens to the outstanding `lenny/request_input` tool call if the *calling runtime's pod* crashes mid-block? The tool call is in-flight from the runtime's perspective — it is waiting on a response that will never come because the pod is gone. On pod recovery (Section 7.3), the spec replays workspace checkpoint state but does not mention that in-flight `lenny/request_input` calls are re-issued or notified. The parent (waiting on `await_children`) would see the child's `input_required` event but the `requestId` from before the crash is no longer valid on the recovered pod.

**Recommendation:** Specify that on session recovery (pod replacement), all outstanding `lenny/request_input` calls are considered cancelled. The recovering session receives a synthetic `lenny/request_input` cancellation event at the start of the resumed session, so the agent can decide whether to re-issue the request. The parent's `await_children` stream should receive a `child_recovering` event with the stale `requestId` marked invalid, so it does not attempt to respond to the stale request.

### MSG-013 No Guidance on `MessageEnvelope.from.id` Format for System-Generated Messages [Low]
**Section:** 15.4.1

The `MessageEnvelope.from` field has `kind: "client | agent | system | external"` and `id: "..."`. The spec says `from.kind` and `from.id` are adapter-injected. For `kind: "system"` (e.g., lifecycle notifications, inbox delivery triggers), what value should `from.id` carry? The spec gives no examples. For `kind: "agent"` (a message from `lenny/send_message`), does `from.id` carry the sending session's `session_id`, its `task_id`, or its `user_id`? This matters for runtimes that want to route messages differently based on origin (e.g., a parent's message vs. a sibling's message).

**Recommendation:** Add a table to Section 15.4.1 specifying canonical `from.id` values for each `kind`: `client` → `user_id`; `agent` → `session_id` of the sending session; `system` → a well-known platform identifier (e.g., `"lenny-platform"`); `external` → the registered connector ID or external agent ID.

---

## Info

### MSG-014 Multi-Turn Conversation State Is Not Explicitly Modelled in TaskRecord [Info]
**Section:** 8.9

The `TaskRecord.messages` array is described as "forward-compatible with multi-turn dialog," and the spec explicitly reserves `threadId` for future multi-thread sessions (Section 15.4.1). However, the `TaskRecord` schema (Section 8.9) shows a flat `messages` array with `role: "caller" | "agent"` and no `threadId` or `inReplyTo` tracking. For a multi-turn session where the caller sends three follow-up messages and the agent responds to each, the messages array correctly records the turns but loses the reply structure. This is noted here as informational since multi-thread is deferred, but the `inReplyTo` field from `MessageEnvelope` is not carried into the `TaskRecord` schema at all — adding it later would be a schema migration.

**Recommendation:** Add `messageId` and `inReplyTo` optional fields to the `TaskRecord.messages` entries now, populated from `MessageEnvelope.id` and `MessageEnvelope.inReplyTo`. This preserves reply structure at storage time even if multi-thread sessions are not yet supported. Setting `schemaVersion: 1` on existing records and `schemaVersion: 2` on records with these fields would cleanly separate the two.

### MSG-015 `lenny/send_message` Cross-Tenant Isolation Enforcement Is Implicit [Info]
**Section:** 7.2, 4.2

The `messagingScope` controls intra-tree message targeting, but the spec does not explicitly state that `lenny/send_message` cannot cross tenant boundaries. The gateway's multi-tenant isolation (Section 4.2) is enforced at the Postgres RLS level for data access, and `tenant_id` is on all session records. A session cannot receive a `task_id` for a session in another tenant through normal operation (the task tree is tenant-scoped), but a crafted `task_id` that belongs to another tenant could theoretically reach the gateway's message routing logic before the tenant check occurs. The spec does not state whether the gateway validates `tenant_id` on the target session before routing a `lenny/send_message` call.

**Recommendation:** Add an explicit statement to Section 7.2: "The gateway validates that the target session's `tenant_id` matches the calling session's `tenant_id` before routing. Cross-tenant messaging returns `TARGET_NOT_FOUND` (identical response to a missing target, to prevent tenant enumeration)."
