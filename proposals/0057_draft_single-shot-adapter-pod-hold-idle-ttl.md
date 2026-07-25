# Proposal: Hold the single-shot adapter pod for an idle TTL instead of releasing it per request

- **Status:** Draft (design capture, not converged). This document records the problem and design space for a follow-up to proposals 0055 (single-shot pod-binding) and 0056 (Open Responses `previous_response_id` continuation). It has NOT been run through the `change-proposal` convergence loop and stages no changes for sign-off. It exists so the gap is not lost; converge it after 0056 lands.
- **Date:** 2026-07-25.
- **Scope:** The built-in OpenAI-dialect adapters (`OpenAICompletionsAdapter` at `/v1/chat/completions`, `OpenResponsesAdapter` at `/v1/responses`) claim and release a warm pod on every HTTP request under the single-shot compute model. For a multi-turn conversation this re-pays the full pod-claim cost per message and, for Open Responses, the 0056 transcript rehydration per message. A held pod with an idle TTL would avoid both on the warm path. This draft records the problem, the design space, and the open questions.

## 1. Problem

Proposal 0055 built the §15 single-shot compute model (`spec/15_external-api-surface.md:585-606`): a built-in adapter request that dispatches a runtime turn claims a warm pod, dispatches the turn, and releases the pod within one HTTP call. Every adapter request therefore draws a fresh warm pod, assigns a credential lease, materializes the workspace, launches the runtime, and tears all of it down again.

A multi-turn conversation over these adapters repeats that whole cycle per message:

- **OpenAI Chat Completions** clients resend the full message history each call (the API is stateless), so each `/v1/chat/completions` call is a fresh single-shot session by design.
- **Open Responses** clients send only the new turn plus a `previous_response_id`, and proposal 0056 reconstructs the prior conversation by replaying the stored transcript onto the freshly-claimed pod. The rehydration exists *because* the previous turn's pod was released. Holding that pod would make both the re-claim and the replay unnecessary on a continuation.

The pod-claim path is the expensive part of a turn (warm-pool draw, §4.9 credential-lease assignment, §6.3 workspace materialization, runtime launch, and the matching teardown). Paying it per message, when a conversation is a sequence of messages to the same logical agent, is wasteful.

The obstacle is that an OpenAI-style adapter exposes a stateless request/response surface with **no explicit "session done" signal**. Unlike the REST session lifecycle (`POST /v1/sessions` … `POST /v1/sessions/{id}/terminate`), the client never tells the gateway a conversation has ended, so the gateway cannot know when it is safe to release a held pod. A time-based release (an idle TTL) is the natural answer: hold the pod for a bounded idle window after a response, reuse it if the conversation continues within the window, and release it when the window expires.

## 2. Design overview

Give the single-shot adapter path an optional **sticky pod with an idle TTL**, sitting between the fully-single-shot model (0055) and the fully-held REST session lifecycle. The held pod is an implicit session whose end is reaped by an idle timer rather than declared by the client.

- **Warm path (continuation within the TTL).** A continuation whose conversation still holds a live pod dispatches the new turn straight onto that pod. No pod re-claim, no credential re-assignment, and — because the runtime already carries the conversation in its own memory — no 0056 rehydration.
- **Cold path (first turn, or TTL expired, or a different gateway replica).** Falls back to the 0055 claim-and-dispatch plus, for Open Responses, the 0056 transcript rehydration. 0056 stays the correctness floor; the held pod is a performance fast path layered on top of it.
- **Release.** Each response resets the idle timer. On expiry a reaper releases the pod through the existing release path, recording the §6.2 terminal disposition. A held pod is also releasable under warm-pool pressure (see the eviction interaction below).

## 3. Design space and open questions

These are the decisions a converged proposal must resolve. They are recorded here, not decided.

- **Conversation key for pod affinity.** Open Responses has a natural conversation identity (the `previous_response_id` lineage / `ContinuationParentID` chain from 0056), so a held pod can be keyed by the conversation root. Chat Completions has no server-side conversation identity — the client resends full history and sends no continuation id — so held-pod reuse may be Open-Responses-only, or need a different affinity key (for example a client-supplied idempotency or conversation header). Decide whether this optimization covers both adapters or only Open Responses.
- **TTL value and tunability.** An operator-tunable idle timeout, defaulted to a documented value, reset on each response. Trades the cost of occupying a warm pod and a credential lease for the idle window against the cost of re-claiming. Decide the default and the unit.
- **Resource accounting and caps.** A held pod occupies warm-pool capacity (or a concurrent slot) and a credential lease for the whole idle window. Decide the per-tenant cap on held pods, the global cap, and the reap-under-pressure policy so held pods do not starve fresh claims.
- **Interaction with §4.6 warm-pool eviction (cluster C-22).** A held single-shot pod is a new class of occupant the eviction and checkpoint machinery must account for. Coordinate with C-22 (agent-pod eviction-checkpoint trigger); a held pod evicted mid-idle must release cleanly.
- **Concurrency on one conversation.** Two requests for the same conversation cannot both use the single held pod (a pod runs one turn at a time). Decide whether the second serializes behind the first or falls back to the cold path (claim-fresh plus rehydrate).
- **Gateway-replica affinity and failover.** The held pod's binding lives in one gateway replica's registry (and the persisted assignment). A continuation routed to a different replica will not find the affinity and takes the cold path. A replica crash must not orphan the held pod: it needs the same reclamation as any binding (the §4.6.1 orphan-GC backstop). Decide whether affinity is best-effort per replica or coordinated.
- **Correctness equivalence.** The warm path and the cold path must produce the same conversation state. The runtime's in-pod memory on the warm path must match what 0056 rehydration would have replayed on the cold path, so a conversation that switches paths mid-stream (a TTL expiry between two turns) does not observe a discontinuity. The transcript recording from 0056 must continue on the warm path so a later cold turn can still rehydrate.
- **Isolation and reuse safety.** A held pod is reused across turns of the same conversation and the same tenant only. Confirm the §13 isolation model permits reuse without a scrub between turns of one conversation (as an interactive REST session already reuses its pod across messages), and that a held pod is never reused across tenants or conversations.

## 4. Non-goals

- **Replacing the 0056 rehydration.** It remains the correctness fallback for the cold path (first turn, TTL expiry, replica failover) and is not removed.
- **An explicit client-managed session lifecycle on the adapter surface.** The OpenAI-dialect wire contract is unchanged; the hold is inferred and time-bounded, not client-declared.
- **Changing the REST session lifecycle.** That path already holds its pod for the session and is untouched.

## 5. Relationship to existing work

- Builds on proposal 0055 (single-shot compute model, `spec/15:585-606`) and proposal 0056 (Open Responses continuation and transcript rehydration).
- Amends the §15 single-shot compute-model paragraph to describe the idle-TTL hold and its warm/cold paths.
- Interacts with the §6 warm-pod model, the §6.2 pod state machine, the §4.9 credential-leasing service (a held pod holds its lease for the idle window), and §4.6 eviction (cluster C-22).
