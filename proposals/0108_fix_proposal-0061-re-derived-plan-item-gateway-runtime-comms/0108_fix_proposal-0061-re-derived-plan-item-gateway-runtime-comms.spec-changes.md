# Spec changes — Built-in OpenAI-dialect adapter conversations claim a pod per turn

## Design (as the spec must state it)

`OpenAICompletionsAdapter` keeps the single-shot model. Its paragraph keeps the bold label **Built-in adapter single-shot compute model.**, so every chat-path citation of that label keeps resolving, and the paragraph keeps the gates, atomicity, failure mapping, and synchronous release it states today. The paragraph drops every Open Responses continuation sentence and gains a successor pointer to the new paragraph.

`OpenResponsesAdapter` gets one normative home, the new §15 bold paragraph **Open Responses conversation sessions.** (SPEC-2). It states the session mapping, the response identity, the warm conditions and the reservation, the cold path, and the recording and failure rule. It reuses by reference the single-shot paragraph's creation gates, the §6.2 timers, §4.7.1 **Session seal.**, `recovery_generation` (§4.2), and §12.8 erasure. It adds no timer, knob, table, frame, or RPC.

§29.3 splits the adapter off-holder row (SPEC-3). Chat Completions and one Open Responses row keep "no forwarding is required", and the other Open Responses row is a forwarding row. The §28.5.4 `CH-HOLDERFORWARD` card's **Messages.** bullet gains a §15 link (SPEC-4).

## Edge cases and accepted failure modes

Each case below is owned by the staged text it cites.

- A continuation of a `suspended`, recovering, `awaiting_client_action`, or terminal session goes cold (SPEC-2 paragraph C). The superseded session is left to its ordinary timers (summary decision 3).
- A crash after the reservation write and before the completion write leaves a not-completed newest response. Warm condition 2 then fails for every later continuation of that session, so every continuation goes cold, and the §6.2 timers reclaim the pod (SPEC-2 paragraph C, "A reserved response is never withdrawn").
- An eviction and restore between the reservation and the completion write advances `recovery_generation`, so the completion write misses its condition and the turn fails with the native `500` (SPEC-2 paragraph D).
- A `POST /v1/sessions/{id}/messages` delivery to a held session appends to the same transcript between turns. The next turn's end sequence bounds a prefix that includes it, so a cold rehydrate of that turn and the warm runtime see the same entries (SPEC-2 paragraphs B and C).
- An erased ancestor session ends the context walk at the gap, and an erased session named by `previous_response_id` itself answers `404` (SPEC-2 paragraph C).
- A session that SPEC-2 paragraph A releases at the end of its turn has every continuation served cold (warm condition 1).
- A forward of a warm turn that is not delivered, or whose outcome is unknown, fails closed with `TARGET_NOT_READY` in the native envelope and is never enqueued in the DLQ (SPEC-3 row 3).
- A response id issued before this change is a bare session id with no `.<n>` ordinal, so `GET /v1/responses/{id}` and `previous_response_id` answer the native `404` for it, and no fallback keeps it resolvable (SPEC-2 paragraph B).

## Staged edits

### SPEC-1 · spec/15_external-api-surface.md § 15 External API Surface

Replace the whole bold paragraph that begins "**Built-in adapter single-shot compute model.** A built-in adapter request that dispatches a Lenny runtime turn (`OpenAICompletionsAdapter`, `OpenResponsesAdapter`)" and ends "An implicit session record is created per request for audit and billing." (the paragraph directly before `### 15.1 REST API`) with the following text. The sentences from "The implicit session runs through the same" through "before the response returns." are unchanged.

```markdown
**Built-in adapter single-shot compute model.** An `OpenAICompletionsAdapter`
request runs an implicit single-shot session that claims a warm pod,
dispatches the turn, and releases the pod within one HTTP call.
The implicit session runs through the same [Section 15.2.1](#1521-restmcp-consistency-contract)
shared session-creation service as `POST /v1/sessions/start`, so the active-user,
quota, concurrency, admission-rate, policy-chain, and environment-admission gates
apply identically. The claim follows the [Section 7.1](07_session-lifecycle.md#71-normal-flow)
step-3 credential-availability pre-check and step-4 warm-pod claim atomicity,
and both roll back with no leaked pod or credential lease. A warm-pod or slot
claim, prepare, or launch failure returns a retryable `503 SESSION_CREATION_FAILED`
with a `Retry-After` header, and a credential-availability pre-check miss returns
`503 CREDENTIAL_POOL_EXHAUSTED` (POLICY) with no pod claimed and no `Retry-After`
header, each mapped into the adapter's native error envelope. On success,
completion, dispatch failure, or request timeout, the pod and its
[Section 4.9](04_system-components.md#49-credential-leasing-service) lease are
released synchronously, recording the completed or failed
[Section 6.2](06_warm-pod-model.md#62-pod-state-machine) terminal disposition,
before the response returns. `OpenResponsesAdapter` requests follow
**Open Responses conversation sessions.**
```

### SPEC-2 · spec/15_external-api-surface.md § 15 External API Surface

Insert the following four paragraphs directly after the paragraph SPEC-1 writes (the **Built-in adapter single-shot compute model.** paragraph) and before `### 15.1 REST API`.

```markdown
**Open Responses conversation sessions.** An `OpenResponsesAdapter` request
that carries no `previous_response_id`, and every continuation that is served
cold as the continuation rules below state, creates a session through the gates,
atomicity, and failures of **Built-in adapter single-shot compute model.** After
a turn that succeeds, the session stays `running` and keeps its pod and its
[Section 4.9](04_system-components.md#49-credential-leasing-service) lease only
when its `conversationContinuity` is `"platform"`
([Section 7.1](07_session-lifecycle.md#71-normal-flow)) and its runtime, with
the session tenant's capabilities applied
([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)), declares
`capabilities.interaction: multi_turn` and `capabilities.injection.supported: true`.
The gateway releases every other session with the `completed` disposition before
the response returns, as **Built-in adapter single-shot compute model.** states
for a Chat Completions session. A held session is an ordinary session. The
[Section 6.2](06_warm-pod-model.md#62-pod-state-machine) `maxClientIdleSeconds`
clock and **Idle suspension.**, `maxSessionAge`, the per-state deadlines, and
`gateway.maxSuspendedSessionSeconds` govern it, it counts toward the
[Section 11.1](11_policy-and-controls.md#111-admission-and-fairness) limits as
any session does, and the adapter adds no timer of its own.

Each turn produces one response. A response id is the session id, a `.`, and
the turn's 1-based ordinal within the session, so the first response of session
`S` is `S.1`. Clients treat response ids as opaque. The session row records, for
each response, its creation time, its end sequence (the highest transcript
sequence number assigned to its turn's entries), the session's
`recovery_generation` ([Section 4.2](04_system-components.md#42-session-manager))
captured for its turn, and whether the turn completed. The session row also
records the runtime its creating request asked for, and the response id its
first turn continued from, which is empty for the first session of a
conversation. `GET /v1/responses/{id}` returns a completed
response of a session in the caller's tenant and echoes its
`previous_response_id`: the response with the preceding ordinal on the same
session, or, for ordinal 1, the response the session's first turn continued
from. An id that names no completed response of a session in the caller's
tenant returns a native `404`. The response records live on the session row and
each turn's entries live in the session's own transcript, so the
[Section 12.8](12_storage-architecture.md#128-compliance-interfaces) erasure of
the session covers them.

On a continuation the gateway resolves `previous_response_id` before it claims
a pod or creates a session. An id that `GET /v1/responses/{id}` would answer
with `404` is rejected fail-closed as a native `404`, with no session created,
no rehydration, and no dispatch. For a resolved response `R` with ordinal `n` on
session `S`, the gateway serves the turn on `S`'s held pod, a warm turn, only
when all of the following hold on `S`'s row:
1. `S` is `running`.
2. `R` is `S`'s newest response and has completed.
3. `S`'s `recovery_generation` equals the value recorded for `R`.
4. This request asks for the user, runtime, and environment that the request
   which created `S` asked for.

Before it dispatches a warm turn, the gateway reserves ordinal `n+1` on `S` with
one write that takes effect only while conditions 1 through 4 still hold, and
that records the response as not completed with `S`'s current
`recovery_generation`. A reserved response is never withdrawn. Every other
continuation is served cold: a continuation that fails a condition, that loses
the reservation write, or whose warm dispatch fails before any input reaches
the runtime. The cold path covers a continuation of an earlier
response, a concurrent continuation of the same response, and a continuation of
a session in any state other than `running`, including `suspended`,
`resume_pending`, `resuming`, `awaiting_client_action`, and every terminal
state. A cold turn creates a new session as the first paragraph above states,
records `R`'s id as the response that session's first turn continued from,
applies `R`'s conversation context ahead of the new input, and dispatches it.
`R`'s conversation context is the transcript of `R`'s session up to and including
`R`'s end sequence, preceded by the conversation context of the response that
session's first turn continued from, back to the first session of the
conversation. The walk stops at a session it has already visited, and at an
ancestor session it cannot read, such as an erased one. A continuation is never
serialized, queued, or delivered through
[Section 7.2](07_session-lifecycle.md#72-interactive-session-model) delivery
path 6. [Section 29.3](29_communication-scenarios.md#293-interactive-message-send)
states how a warm turn that reaches a replica other than `S`'s coordinating
replica is served.

Each turn appends its input and its output to its own session's transcript on
both paths. The gateway completes the response, recording its end sequence, in
one write that takes effect only while the session's `recovery_generation`
still equals the value captured for the turn: at the reservation for a warm
turn, and at session creation for a turn that creates its session. The gateway
returns a response only after that write. When the transcript append, the
completion write, or a dispatch fails, and the continuation rules above do not
answer that failure with a cold turn, the gateway answers a native `500`,
leaves the response not completed, and ends the session with the `failed`
disposition, releasing its pod and lease before the response returns. A warm
turn writes no idle-clock
stamp of its own: the adapter's write of the turn is
activity under [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)
**Session seal.**, which **Idle suspension.** reads.
```

### SPEC-3 · spec/29_communication-scenarios.md § 29.3 Interactive message send

**Edit A (lead-in clause).** In the paragraph that follows **Forwarding.** rule 8 (it begins "Where a row below says that the serving replica fails closed on an unreachable coordinator"), replace the clause

"the built-in adapter row addresses no pre-existing session and so has no holder to forward to"

with

```markdown
the `POST /v1/chat/completions` row and the `POST /v1/responses` row that requires no forwarding are served by the serving replica itself
```

**Edit B (rows).** Replace the off-holder table row that begins "| `POST /v1/chat/completions` and `POST /v1/responses` | No pre-existing session is addressed |" with the three rows below, in this order. Each row is one physical line, because a table row cannot wrap.

```markdown
| `POST /v1/chat/completions` | No pre-existing session is addressed | No forwarding is required. Each call runs an implicit single-shot session that the serving replica creates, claims a warm pod for, dispatches, and releases within the call ([§15](15_external-api-surface.md#15-external-api-surface) **Built-in adapter single-shot compute model.**), so the serving replica is the coordinating replica by construction and no off-holder condition arises. A creation or claim failure fails closed as `SESSION_CREATION_FAILED` or `CREDENTIAL_POOL_EXHAUSTED`, HTTP 503 ([§15.1](15_external-api-surface.md#151-rest-api)), rendered into the adapter's native error envelope (§15); no inbox buffering and no `queued` receipt applies | §29 |
| Every `POST /v1/responses` that no forwarding row names | No pre-existing session is served | No forwarding is required. The serving replica serves the request itself as [§15](15_external-api-surface.md#15-external-api-surface) **Open Responses conversation sessions.** states, and a session it creates for the request has the serving replica as its coordinating replica by construction, so no off-holder condition arises. A creation or claim failure fails closed as `SESSION_CREATION_FAILED` or `CREDENTIAL_POOL_EXHAUSTED`, HTTP 503 ([§15.1](15_external-api-surface.md#151-rest-api)), rendered into the adapter's native error envelope (§15); no inbox buffering and no `queued` receipt applies | §29 |
| `POST /v1/responses` whose `previous_response_id` names a response for which warm conditions 1 through 4 of [§15](15_external-api-surface.md#15-external-api-surface) **Open Responses conversation sessions.** hold on the serving replica's read of the session row | `running` | Forward to the coordinator, which evaluates the continuation and serves the turn as §15 **Open Responses conversation sessions.** states. When the forward is not delivered or its outcome is unknown, the serving replica fails closed with `TARGET_NOT_READY`, HTTP 409, carrying `currentState`, rendered into the adapter's native error envelope. The request is not a message under **Forwarding.** rule 6, so it is never enqueued in the DLQ, and rules 6 and 7 otherwise apply as written. Rule 4 does not exempt a session the coordinator creates for the turn from the gates of §15 **Built-in adapter single-shot compute model.** | §29 |
```

Nothing else changes in §29.3 or §28.8.

### SPEC-4 · spec/28_communication-channels.md § 28.5.4 Inter-replica

In the `CH-HOLDERFORWARD` card, in the **Messages.** bullet, replace

"([§15.1](15_external-api-surface.md#151-rest-api), [§15.2](15_external-api-surface.md#152-mcp-api))."

with

```markdown
([§15](15_external-api-surface.md#15-external-api-surface), [§15.1](15_external-api-surface.md#151-rest-api), [§15.2](15_external-api-surface.md#152-mcp-api)).
```

## Spec files touched

- spec/15_external-api-surface.md (SPEC-1, SPEC-2)
- spec/29_communication-scenarios.md (SPEC-3)
- spec/28_communication-channels.md (SPEC-4)
