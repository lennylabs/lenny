# Problem: Built-in OpenAI-dialect adapter conversations claim a pod per turn

## Statement

Proposal 0061 re-derived (plan item: gateway-runtime-comms-remediation.md §10.2 'Queue after P4b', 'Proposal 0061, re-derived'). The design capture is proposals/0061_draft_single-shot-adapter-pod-hold-idle-ttl.md, a legacy single-file draft that was never converged and stages nothing. This proposal (number 0108) supersedes it.

The built-in OpenAI-dialect adapters, OpenResponsesAdapter at `/v1/responses` and OpenAICompletionsAdapter at `/v1/chat/completions`, run under the §15 **Built-in adapter single-shot compute model**. Every HTTP request claims a warm pod through the shared §15.2.1 create-and-start service, dispatches one turn, marks the implicit session completed, and releases the pod and its §4.9 lease before the response returns. §15 also states that an OpenResponsesAdapter continuation "re-claims a fresh pod per request and does not retain a pod bind across calls", and the §29.3 off-holder table row for both routes says no forwarding is required because each call is single-shot. The code implements this contract faithfully. The change is therefore an owner-decided change to the §15 and §29.3 normative contract. It does not bring code up to an existing contract.

A multi-turn Open Responses conversation pays a pod claim and a credential lease on every message. On a non-recycling pool it also pays runtime launch, workspace setup, and teardown per message. On a recycling pool with `runtimeProcess: keep` it pays a scrub and a re-claim instead. In both cases the runtime memory is lost between turns, so every continuation re-runs the proposal-0059 `previous_response_id` chain-walk rehydration, which reads one session row and one transcript bucket per ancestor. Proposal 0059 was renumbered from 0056 on integration; the current proposals/0056 is the unrelated termination-grace-floor fix. Chat Completions is stateless: the client resends the whole history, the handler keeps no conversation state, and there is no server-side conversation identity, so its only per-turn cost is the claim.

Owner decision (2026-10-28, binding, part of v1): a conversation on these adapters is a session. It keeps its pod across turns and follows the same timeouts and TTLs as every other session: the §6.2 `maxClientIdleSeconds` clock, `maxSessionAge`, and the per-state deadlines that proposal 0105 anchors on `state_entered_at`. The same watchdog sweeps reap it, with no separate pod-hold TTL and no new idle-TTL knob. A continuation that reaches another gateway replica forwards to the holder over CH-HOLDERFORWARD (proposal 0101, implemented) rather than taking the cold path.

Under the existing §6.2 model, `maxClientIdleSeconds` does not end a session. It idle-suspends a `running` session (`running → suspended`, hold of zero, pod released), and the suspended session lives until `gateway.maxSuspendedSessionSeconds` (default 7 days) and resumes on any message through §7.2 delivery path 6. Only a session that cannot be idle-suspended (`executionMode: service`, or a `type: mcp` runtime) expires at the idle bound. The proposal-0059 cold rehydration path is therefore the correctness floor for a terminal session and for a continuation the held session cannot serve, rather than for every session past its idle bound.

Decide whole at drafting time:

1. The conversation identity. For Open Responses, use the `previous_response_id` lineage. For Chat Completions, keep the endpoint single-shot in v1 with the reason stated (stateless dialect, no identity on the wire, no rehydration cost to save), unless the drafter defines an identity; an identity design is a separate follow-up proposal rather than part of this one.
2. How an adapter conversation maps onto the session lifecycle: which states it passes through, when it is created, how a turn is dispatched on the held pod, and what ends it (`maxSessionAge`, an explicit end if the dialect has one, terminate). This item settles whether a continuation of an idle-suspended adapter session resumes it through the existing suspend and resume path or takes the 0059 cold path.
3. Response identity. Today each response id is its own session id: `BindSingleShot` mints a session per request, the response is built from that session id, `ContinuationParentID` points from response row to response row, and each response's transcript bucket holds only its own turn. `GET /v1/responses/{id}`, the chain-walk storage, and §12.8 erasure coverage rely on that identity, and §15 says "An implicit session record is created per request for audit and billing". A session that spans turns needs response ids decoupled from the session id, with a per-response record that keeps those consumers working.
4. Lineage forks and concurrent turns. `previous_response_id` may name any earlier response, so the lineage is a tree. The held pod's runtime memory is linear, so the warm path is valid only when `previous_response_id` is the newest response the held session produced. Decide whether any other continuation, and a concurrent turn on the same conversation, is serialized, refused, or sent to the cold path.
5. Warm and cold equivalence: transcript recording continues on the warm path, so a later cold continuation rehydrates the same state.
6. Interaction with warm-pool capacity, credential leases, and quotas (a held conversation occupies a pod like any session), and with §4.6 eviction.
7. Isolation: reuse is only within one conversation of one tenant.
8. Coordination with the pinned-idle inventory proposal, which is not yet written, and with recycling pools (`maxSessionsPerPod`), without staging their changes. Until that proposal exists, coordinate against the §5.2 interim pinned-idle rule from proposal 0079.

The change rewrites the §15 single-shot paragraph and the §29.3 off-holder row for `POST /v1/chat/completions` and `POST /v1/responses`.

## Evidence

- verified: spec/15_external-api-surface.md §15 **Built-in adapter single-shot compute model** (claim, dispatch, and release within one HTTP call; continuation "re-claims a fresh pod per request"; "An implicit session record is created per request for audit and billing"). The adapter table marks both adapters V1.
- verified: spec/29_communication-scenarios.md §29.3, the off-holder row for `POST /v1/chat/completions` and `POST /v1/responses` ("No forwarding is required. Each call runs an implicit single-shot session").
- verified: spec/06_warm-pod-model.md §6.2 transition `running → suspended (maxClientIdleSeconds elapsed — idle suspension; hold of zero)`, the idle-clock paragraph that expires only a session that cannot be idle-suspended, **Idle suspension.**, and **`suspended → expired` trigger mechanism:** (`gateway.maxSuspendedSessionSeconds`, default 604800s).
- verified: spec/05_runtime-registry-and-pool-model.md §5.2 recycle configuration and `runtimeProcess: keep`, and the tenant-pinning and interim pinned-idle text.
- verified: spec/28_communication-channels.md registers CH-HOLDERFORWARD; §29.3 **Forwarding.** governs off-holder forwarding; §6.2 defines `state_entered_at` as the per-state deadline anchor.
- verified: pkg/gateway/environment/translator/open_responses.go `handleCreate` calls `BindSingleShot`, defers `releaseSingleShot`, rehydrates through `h.cont.rehydrate`, dispatches one `exec.Send`, writes `StateCompleted` and `ContinuationParentID`, and builds the response from `sessionID`.
- verified: pkg/gateway/environment/translator/continuity.go stores chain-walk single-turn buckets and walks the `ContinuationParentID` chain with one `sessions.Get` and one `transcripts.Get` per ancestor. `ContinuationParentID` is a Go field and does not appear in spec/.
- verified: pkg/gateway/environment/translator/openai_chat.go marks the session completed with the comment that the translator "does not maintain conversation state across calls".
- verified: cmd/lenny-gateway/credentialsurface.go wires the production `sessionSingleShotBinder`, whose `BindSingleShot` calls `CreateAndStartService`.
- verified: pkg/gateway/sessionserver/holder_forward.go (CH-HOLDERFORWARD, proposal 0101) and pkg/gateway/runtime/watchdog (per-state sweeps, proposal 0105).
- verified: git history `9d9801ae9 integrate C-50 (proposal 0059, renumbered from 0056)` and `7c4605159 integrate C-13 (proposal 0057, renumbered from 0055)`; proposals/0056 is the termination-grace-floor fix.

## Who observes it

Every client of the two V1 OpenAI-compatible entry points on a deployed gateway, because production wires both adapters to the sessionserver-backed binder. The no-op binder that claims no pod serves only the in-memory mode and unit tests. Open Responses clients carry most of the cost: each continuation pays a pod claim plus a rehydration walk over the whole lineage. Chat Completions clients pay only the per-request claim.

## What breaks if nothing changes

No correctness failure results. The cold path rejects an unknown or cross-tenant `previous_response_id` with a native 404 and recovers past an erased ancestor. The cost is latency, pool churn, and capacity exposure:

- Every message pays a warm-pool draw and a §4.9 lease, and on a non-recycling pool a runtime launch and teardown.
- Every message is a new chance to fail with a retryable `503 SESSION_CREATION_FAILED` or `503 CREDENTIAL_POOL_EXHAUSTED`, where a held session would meet that check once.
- Open Responses turn N performs O(N) store reads and replays O(N) prior turns into the runtime, so a conversation costs O(N^2) reads.

The owner decided on 2026-10-28 that the held-session behavior is part of v1, which is the basis for the change.

## Findings this unblocks

No filed BUILD-GAPS or TEST-GAPS finding records the per-turn pod claim on the adapters, so this proposal closes no filed finding.

## Prior art considered

- §15 and §29.3 pin the opposite behavior (claim and release per request, no forwarding). No landed proposal or spec mechanism keeps a built-in adapter pod across turns, and `SingleShotBinder` has no hold path.
- proposals/0061_draft_single-shot-adapter-pod-hold-idle-ttl.md is the only prior capture. It is an unconverged Draft that stages nothing. Its design (an optional sticky pod with its own idle TTL) is the design the 2026-10-28 owner decision replaces. It mixes the labels 0056 and 0059 for the rehydration proposal, which is the source of the misattribution in the earlier version of this statement.
- Proposal 0057 built the single-shot binding model. Proposal 0059 built `previous_response_id` continuation with chain-walk rehydration.
- Proposal 0101 (CH-HOLDERFORWARD) and proposal 0105 (`state_entered_at` per-state deadlines and watchdog sweeps) are implemented and are the mechanisms the owner decision reuses.
- The §6.2 idle-suspension and §7.2 delivery path 6 resume machinery (proposal 0092, implemented) already answers part of what happens to an idle adapter session.
- The pinned-idle inventory proposal is not yet written. §5.2 carries only the proposal-0079 interim rule.

## Validated premises

**Premise lens (verdict: revise).**
- Holds: both adapters exist at the stated routes and are V1 in the §15 adapter table.
- Holds: each request claims a warm pod, dispatches one turn, and releases the pod within the call, in spec and in code. The production binder runs the full create-and-start chain.
- Holds with correction: Open Responses rehydrates per message through the `ContinuationParentID` chain walk. The attribution to proposal 0056 is refuted. The mechanism is proposal 0059, built on proposal 0057.
- Refuted as stated: "every message pays warm-pool draw, credential lease, workspace materialization, runtime launch, and teardown". On a recycling pool with `runtimeProcess: keep`, release runs a scrub and the pod serves the next session, so the runtime is not relaunched. Runtime memory is still lost, so the problem keeps its direction. The cost statement depends on the pool mode.
- Holds: Chat Completions has no conversation identity today.
- Holds: proposals 0101 and 0105 are implemented, and the §29.3 adapter row has to change.
- Holds: the 0061 legacy draft exists.

**Evidence lens (verdict: revise).**
- Refuted: "proposal-0056 transcript rehydration". Proposal 0056 is the termination-grace floor; the rehydration is proposal 0059 (renumbered from 0056, confirmed in git history).
- Verified: the §15 single-shot model, the code locations, 0101 and 0105 status, CH-HOLDERFORWARD in §28, the §29.3 row, and the plan item and draft.
- Refuted as stated: treating `maxClientIdleSeconds` as a plain idle timeout that ends the conversation. It is an idle-suspension trigger and expires only a session that cannot be idle-suspended. Decision (2) is reframed accordingly.

**Prior-art lens (verdict: revise).**
- Confirmed: no existing proposal or spec mechanism covers the change; 0061 is unconverged and duplicates no work; no filed finding covers it.
- Refuted: the 0056 attribution, as above.
- Refuted (load-bearing for item 2): "the cold path is the floor once the session has ended" assumed idle ends the session. A suspended session resumes on any message until `maxSuspendedSessionSeconds`. Item (2) now has to decide between resume and the cold path for an idle-suspended adapter session.
- Confirmed: the pinned-idle proposal is unwritten; coordinate against the §5.2 interim rule from proposal 0079.

**Scope lens (verdict: stands).**
- Confirmed: one problem, kept as one proposal; the decision items are facets of one design touching one §15 paragraph, one §29.3 row, and one handler path.
- Confirmed: Chat Completions is the one place the problem could split. Settle it first; an identity design becomes its own follow-up.
- Added: response-id decoupling and lineage forks belong inside this proposal (now items 3 and 4).
- Confirmed: the pinned-idle item is a non-overlap check and stays "stage none of their changes".

**Impact lens (verdict: revise).**
- Confirmed: the behavior runs in production through cmd/lenny-gateway/credentialsurface.go.
- Refuted framing (load-bearing): the change is not a code-versus-spec defect. Code and spec agree; the owner decision changes the §15 and §29.3 contract.
- Confirmed: every V1 client sees per-message claim cost and per-message exposure to 503 claim failures.
- Added: Open Responses rehydration is O(N) per turn and O(N^2) per conversation.
- Confirmed: Chat Completions has no rehydration cost; the larger impact is on Open Responses.
- Confirmed: no correctness failure; the case for change rests on the owner decision.

**Alternatives lens (verdict: revise).**
- Confirmed: "no change" satisfies the spec and is ruled out by owner authority rather than on technical grounds.
- Confirmed: the smallest defensible change keeps `/v1/chat/completions` single-shot in v1 (now the default in item 1).
- Confirmed (load-bearing): response id equals session id today, and a multi-turn session breaks that identity for GET, chain-walk storage, and §12.8 erasure (now item 3).
- Confirmed: the lineage is a tree, so the warm path applies only at the head the held pod produced (now item 4).
- Confirmed (load-bearing): idle suspends rather than ends the session, leaving two cold paths to choose between (now in item 2).
- Confirmed: the 0056 label is wrong; 0059 was renumbered from 0056 and 0057 from 0055.
