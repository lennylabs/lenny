# Proposal: Build the §7.2 path-6 pod-held atomic resume-and-deliver so a `delivery: "immediate"` message to a suspended session returns `delivered`

- **Status:** Approved (2026-07-22). Converged after 4 adversarial review rounds (3 findings fixed; 2 changes dropped by review, 0 refuted). All three §10 open decisions accepted as proposed at sign-off: (1) defer the five cross-section prerequisites (§6.2 pod-release sweep, §4.7 adapter `ready_for_input` + 30s timeout, §10.1 `ForwardMessage`, MCP delivery-field threading, tier-5 real-pod variant) to a single bundled follow-up proposal; (2) gate `resumeHeldPod` on `session.StateSuspended` alone (atomic inside the `store.Update` mutator), with the pod-held/podless distinction added when the §6.2 sweep lands; (3) leave `lenny/send_message` unthreaded for now.
- **Date:** 2026-07-22.
- **Scope:** A code-first build of the §7.2 path-6 pod-held branch (`spec/07_session-lifecycle.md:326-330`), which the implementation does not perform today. The pure router `Classify` (`pkg/gateway/session/messagerouting/messagerouting.go:137`) gains one `ActionResumeAndDeliver` action and returns it for a `suspended` target that carries `delivery: "immediate"`; the REST handler (`pkg/gateway/sessionserver/messages.go`) runs a coordinator-side atomic resume-and-deliver on that action through a new in-place `resumeHeldPod` primitive (`pkg/gateway/sessionserver/start.go`), and fails closed to `queued` on any resume or delivery failure so no message is dropped. The two tests that pin the current spec-violating `queued` fallback are inverted (`pkg/gateway/session/messagerouting/messagerouting_test.go:54`, `tests/tier4_integration/interactive_iteration_test.go:45`), and a tier-2 component test covers the fail-closed paths. This closes T-JRN.1 (High, `TEST-GAPS.md`) by taking option (a) from its recorded human-input decision: build the atomic resume-and-deliver rather than downgrade the interactive-iteration test to a non-immediate message. §7.2 path 6 fully specifies the behavior, so no spec edit is required. The §6.2 pod-release-during-suspension sweep, the §4.7 adapter `ready_for_input` readiness signal and 30-second delivery timeout, the §10.1 `ForwardMessage` cross-replica forwarding, the MCP `send_message` delivery-field threading, and the tier-5 real-pod variant are each recorded as distinct follow-on work and deferred.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

§7.2 path 6 (`spec/07_session-lifecycle.md:326-330`) defines the routing for a message whose target session is `suspended`. When the message carries `delivery: "immediate"` and the session still holds its pod, line 327 requires:

> **Pod still held:** The gateway atomically resumes the session (`suspended → running`) and delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`. The delivery receipt is `delivered` on successful resume-and-deliver.

The pure router does not perform this. `Classify(state session.State, inputRequired, immediate bool, src Source)` (`messagerouting.go:137`) returns `Decision{Path: 6, Action: ActionBufferInbox, Status: DeliveryStatusQueued}` for a `suspended` target unconditionally (`messagerouting.go:152-153`). It never inspects `immediate`; its own doc comment records that `immediate` "does not change the destination for the conditions a synchronous executor exposes" (`messagerouting.go:133-136`). No `ActionResumeAndDeliver` value exists in the `Action` enum (`messagerouting.go:42-71`), and the `ActionBufferInbox` doc names "the pod-held suspended case of path 6" as one of its cases (`messagerouting.go:51`).

Both delivery surfaces route the suspended case to the inbox as a result. The REST handler computes `immediate` from the request and calls `Classify` (`messages.go:528-535`), then buffers on the `ActionBufferInbox` case (`messages.go:621-642`), stamping `queued` at `messages.go:637`. The MCP `lenny/send_message` handler hard-codes `immediate=false` in its `Classify` call (`mcptools_register.go:356`).

The current gap behavior is pinned by two tests. The unit case `path6_suspended_immediate_still_buffers` asserts `ActionBufferInbox`/`queued` for a `suspended`, `immediate` message (`messagerouting_test.go:54-57`). The tier-4 test `TestInteractiveIterationInterruptThenQueuedMessage` sends a fourth prompt with `delivery: "immediate"` to a suspended, pod-held session and asserts `deliveryReceipt.status == "queued"` (`interactive_iteration_test.go:177-178`) and a still-`suspended` session (`interactive_iteration_test.go:188-190`); its header comment records that the atomic resume-and-deliver is "still unimplemented" (`interactive_iteration_test.go:35-38`).

This blocks T-JRN.1 (High, OPEN, `TEST-GAPS.md:11898`), the end-to-end interactive developer journey. Its "Needs human input" note (`TEST-GAPS.md:11904`) records that a first attempt at the tier-4 interactive-iteration test was discarded because it pinned the spec-violating `queued` fallback, and asks a human to choose between implementing the atomic resume-and-deliver (option a) or rewriting the test to a non-immediate message (option b). This proposal takes option (a).

This is a code-to-spec build gap. §7.2 path 6 fully specifies the pod-held, pod-released, and coordinator-forwarding behavior, so no spec edit is needed. The state-machine edge `{Suspended, Running}` is already legal (`pkg/session/state/state.go:94`). The interrupt path stamps `SuspendedAt` in the same transaction that writes `suspended` and leaves `PodAssignment` unchanged (`interrupt.go:87-91`; `transitionInterrupt` at `sessionserver.go:3206` writes only the state). In `--dev-mode` the gateway builds no pod binder (`cmd/lenny-gateway/stores.go:1926` gates the only `podBinder` construction on `--agent-namespace`, and every `PodAssignment` write is nested under `if s.podBinder != nil`, for example `start.go:747` and `:768`), so a dev-mode session's `PodAssignment` is never set and stays the empty string; the session still holds its in-process echo executor. Because no §6.2 sweep releases a suspended pod today, every `suspended` row is pod-held in the currently reachable state set, so the router's resume-and-deliver and the `resumeHeldPod` transition are gated on `session.StateSuspended` alone and require no `PodAssignment` binding. A `suspended` row is pod-held only because the release sweep is unbuilt, rather than by spec definition: §6.2 keeps a released session in `suspended` (podless) with no state change (`spec/06_warm-pod-model.md:219,280`). When the deferred §6.2 sweep lands, the guard must additionally distinguish a pod-held row from a podless-suspended row and route the podless case to `resume_pending`/`queued` per §7.2 line 328 (`spec/07_session-lifecycle.md:328`). The in-process echo executor's `Send` is synchronous and always returns a ready response with a nil error (`executor.go:123`; `echo.go` `Send`), so on the single coordinating replica the `delivered` outcome is reachable in the tier-4 `--dev-mode` harness once the router routes a resume-and-deliver action, without the deferred §4.7 `ready_for_input` signal, the §6.2 pod-release sweep, or the §10.1 `ForwardMessage` machinery.

## 2. Decisions

- **Build only the reachable coordinator core; defer the three cross-section prerequisites.** The tier-4 single-replica-coordinator `delivered` outcome requires none of the following. (1) The §6.2 pod-release-during-suspension sweep: no sweep consumes `maxSuspendedPodHoldSeconds` today, so a suspended session always holds its pod and the pod-held branch is the only reachable case; the pod-released `resume_pending` branch is unreachable. (2) The §4.7 adapter `ready_for_input` signal plus the 30-second delivery timeout: the in-process echo executor is always ready by construction. (3) The §10.1 `ForwardMessage` cross-replica forwarding: a single replica is always the coordinator. Each is entirely unbuilt today, and each stays spec-compliant when deferred because the spec sanctions the `queued` fallback for the pod-released and unreachable-coordinator cases (`spec/07_session-lifecycle.md:328,330`). All three are deferred as distinct follow-on features (§6, §8, §10).
- **Extend the pure `Classify` rather than add a parallel classifier, and branch on the `immediate` flag the router already receives.** Add one `ActionResumeAndDeliver` value to the existing `Action` enum and change the `suspended` branch to return `ActionResumeAndDeliver`/`delivered` when `immediate` is set and `ActionBufferInbox`/`queued` otherwise. `Classify` already takes `immediate` (`messagerouting.go:137`) and the REST handler already computes and passes it (`messages.go:528-535`); the suspended branch never inspected it. The signature is unchanged, so the MCP call site (`mcptools_register.go:356`) needs no edit. This keeps a single canonical router deciding every §7.2 path for both REST and MCP with the minimum new surface.
- **Do not thread a pod-held predicate through the router.** An earlier draft added a `podHeld bool` parameter to `Classify` sourced from a wall-clock deadline. It is removed. No sweep releases a suspended pod today, so `podHeld` is always true for the only reachable case and the router gains no reachable decision from it. A deadline-derived predicate (`clock().Sub(SuspendedAt) < maxSuspendedPodHoldSeconds`) additionally diverges from the spec for a reachable case: a session suspended longer than the window still holds its pod because nothing releases it, so §7.2 path 6 requires `delivered`, yet a deadline predicate would return `queued`. The handler-side guard in `resumeHeldPod` (below) covers the "pod not actually held" edge without importing that divergence into the canonical router. When the §6.2 pod-release sweep lands, the pod-released distinction is added to the router then with its correct `suspended → resume_pending` action, rather than pre-threaded now with the wrong (inbox/queued) target.
- **Run the atomic resume-and-deliver on the coordinator and fail closed to `queued`, never dropping.** On `ActionResumeAndDeliver` the handler transitions `suspended → running` via an in-place resume primitive that reuses the bound `PodAssignment` (no fresh-pod claim), then delivers via `executor.Send`, then stamps `delivered`. On a resume failure the row stays `suspended` and the message buffers to the inbox (`queued`). On a post-resume delivery failure the message buffers to the inbox (`queued`) while the session is `running` (a running session's inbox drains on the next `ready_for_input`). Both fallbacks preserve the message, matching the spec's own `queued` fallback for the unreachable-coordinator case (`spec/07_session-lifecycle.md:330`).
- **Add a narrow coordinator-side `resumeHeldPod` primitive rather than generalize `resumeOnPod`.** The existing `resumeOnPod` (`start.go:3519`) always claims a fresh warm pod (the podless resume), and `EndpointResume` excludes `suspended`. A new `resumeHeldPod` writes `suspended → running` through the existing `transitionResume` (`sessionserver.go:3215`), reusing any already-bound pod without a fresh-pod claim, guards inside the `store.Update` mutator that the row is `session.StateSuspended` so the check and the transition are atomic under the row lock, and emits the same status-change and lifecycle-audit signals `handleResume` emits (`start.go:3194-3208`). The guard does not require a non-empty `PodAssignment`: no §6.2 sweep releases a suspended pod today, so every `suspended` row is pod-held in the currently reachable state set, and in `--dev-mode` the gateway builds no pod binder so `PodAssignment` is empty while the session still holds its in-process executor. §6.2 keeps a released session in `suspended` (podless) rather than moving it out of the state (`spec/06_warm-pod-model.md:219,280`), so when the deferred §6.2 sweep lands the guard must additionally distinguish a pod-held row from a podless-suspended row and route the podless case to `resume_pending`/`queued` per §7.2 line 328. For the in-process echo executor this collapses to the state write plus `executor.Send`. The real-pod in-place adapter-resume signal against a still-held adapter is the deferred §4.7 work.
- **Leave the MCP `lenny/send_message` path unthreaded and defer it with the other MCP work.** The `send_message` tool schema exposes no `delivery` field, so an inter-session immediate message is not expressible via the tool today. A message without `delivery: "immediate"` to a suspended session correctly stays buffered per spec (`spec/07_session-lifecycle.md:330`), so the MCP path is spec-consistent for the messages it can express. Because the router branches on `immediate` regardless of source, path 6 flows through uniformly once the tool gains a delivery field.
- **Pin the delivered outcome at tier 1 (router), tier 2 (`resumeHeldPod` transition and the handler fail-closed paths), and tier 4 (end-to-end against the echo executor), inverting the two tests that pin the current gap.** The tier-5 real-pod variant is deferred with §4.7 because a real held-pod adapter cannot confirm in-place delivery without the `ready_for_input`/resume signal.

## 3. The path-6 pod-held flow after the change

The interrupt path is unchanged: a `POST /interrupt` writes `suspended`, stamps `SuspendedAt`, and leaves `PodAssignment` unchanged (`interrupt.go:87-91`; in `--dev-mode` it stays the empty string because no pod binder ever bound it). A subsequent `delivery: "immediate"` message to that session then flows as follows.

- **The router selects resume-and-deliver.** `deliverMessageBatch` computes `immediate` from the request (`messages.go:528-534`) and calls `Classify` (`messages.go:535`). For a `suspended` target with `immediate` set, `Classify` returns `Decision{Path: 6, Action: ActionResumeAndDeliver, Status: DeliveryStatusDelivered}`. A `suspended` target without `immediate` still returns `ActionBufferInbox`/`queued`.
- **The handler runs the coordinator-side resume-and-deliver.** On `ActionResumeAndDeliver` the handler calls `resumeHeldPod(ctx, tenantID, row.ID)`, which in a single `store.Update` guards that the row is `session.StateSuspended` and transitions it `suspended → running` reusing any already-bound pod, and emits the resume status-change and the `session.resumed` lifecycle audit. On success the handler calls `executor.Send(ctx, row.ID, msgs)`; on a nil error it records the response (the same PostAgentOutput chain, transcript write, event publish, and TTFT path the `ActionDeliver` case runs at `messages.go:559-609`) and stamps `outcome.status = delivered`.
- **A resume failure fails closed to `queued`.** If `resumeHeldPod` returns an error (a store fault, or the guard rejecting a non-`suspended` row), the handler does not transition the session and buffers the message to the inbox through the same `bufferIncomingMessages` path the `ActionBufferInbox` case uses (`messages.go:622`), yielding a `queued` receipt. The row is left `suspended`.
- **A post-resume delivery failure fails closed to `queued`.** If `resumeHeldPod` succeeds but `executor.Send` returns an error, the handler buffers the message to the inbox (`queued`) while the session remains `running`. The message is preserved for FIFO redelivery on the next `ready_for_input` rather than surfacing a 500. The delivery failure is recorded via the existing `tracing.RecordError` path.

For the in-process echo executor, `resumeHeldPod` returns after the state write (there is no pod adapter to signal) and `executor.Send` always returns a ready response with a nil error, so the reachable outcome is `delivered` with the session `running`.

## 4. Edge cases and accepted failure modes

Every row states the observable outcome, whether this proposal builds or defers it, the exact spec text that governs it, and the docs page that documents it for a reader. `spec/07_session-lifecycle.md` is the source of truth; the reader-facing page is `docs/api/rest.md`.

| Scenario | Observable outcome | Built or deferred | Governing spec text | Docs page |
|:--|:--|:--|:--|:--|
| `suspended`, pod held, `delivery: "immediate"` (REST) | `suspended → running`, message delivered to the runtime, receipt `delivered` | Built (C1, C2, C3) | `:327` "The gateway atomically resumes the session (`suspended → running`) and delivers the message ... The delivery receipt is `delivered` on successful resume-and-deliver." | `docs/api/rest.md:128` "To wake a `suspended` session, send a message (optionally with `delivery: "immediate"`)." |
| `suspended`, pod released (podless), `delivery: "immediate"` | receipt `queued`, message buffered; the spec's `suspended → resume_pending` transition is not performed | Deferred (§6.2 sweep); unreachable today because no sweep releases a suspended pod, so no `suspended` session is podless. When the sweep lands, a released session stays `suspended` (podless) and this immediate-message path performs the `suspended → resume_pending` transition | `:328` "The gateway transitions the session to `resume_pending` ... The delivery receipt is `queued`." | `docs/api/rest.md:130` |
| `suspended`, no `delivery: "immediate"` | receipt `queued`, message buffered | Unchanged | `:330` "Messages without `delivery: "immediate"` remain buffered until an explicit `resume_session` or a subsequent `delivery: "immediate"` message triggers the resume." | `docs/api/rest.md:128` |
| resume succeeds, `executor.Send` then fails | receipt `queued`, message buffered, session left `running` | Built fallback (C2); dead code under the always-ready echo executor. The spec's path-6 `queued` fallback does not name a `running`-plus-`queued` outcome, so this is an accepted local divergence chosen to preserve the message rather than drop it or return a 500 | `:330` "the forwarding replica falls back to inbox buffering with a `queued` delivery receipt status — the message is not silently dropped." | `docs/api/rest.md:216` |
| `resumeHeldPod` fails (store fault or guard rejects a non-`suspended` row) | receipt `queued`, message buffered, session left `suspended` | Built fallback (C2, C3) | `:330` "the message is not silently dropped." | `docs/api/rest.md:216` |
| `delivery: "immediate"` lands on a non-coordinator replica | replica forwards to the coordinator; on an unreachable coordinator, `queued` fallback | Deferred (§10.1 `ForwardMessage`); unreachable today because a single replica is always the coordinator | `:330` "When a `delivery: immediate` message lands on a non-coordinator replica, that replica forwards the message to the session's coordinator ... If the coordinator is unreachable ... the forwarding replica falls back to inbox buffering with a `queued` delivery receipt status." | `docs/api/rest.md:216` |
| real held-pod delivery gated on `ready_for_input` and the 30-second timeout | resume-and-deliver waits for the adapter readiness signal; a timeout falls through to `queued` | Deferred (§4.7); the echo executor is always ready, so the readiness gate is a no-op at tier 4 | `:327` "delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`." | `docs/api/rest.md:216` |
| `lenny/send_message` immediate message to a `suspended` sibling/child | message buffered, `queued` (the tool cannot express `delivery: "immediate"`) | Deferred (MCP delivery-field threading); the router already branches on `immediate` regardless of source | `:330` "This applies uniformly to all message sources: external client (`POST /v1/sessions/{id}/messages`) and inter-session via `lenny/send_message`." | `docs/api/mcp.md:471` |

## 5. Proposed changes

### C1. Router: add `ActionResumeAndDeliver` and branch the suspended case on `immediate`

**Target:** `pkg/gateway/session/messagerouting/messagerouting.go`: the `Action` enum (`:42-71`), the `Classify` doc comment (`:104-136`), and the `suspended` branch (`:152-153`).

**Rationale:** The router is the single canonical concern that selects the §7.2 delivery path for both REST and MCP. It must gain a resume-and-deliver outcome so the suspended case can return `delivered` for an immediate message instead of unconditionally buffering. `Classify` already receives `immediate`, so the suspended branch reads the flag it already has; the signature is unchanged and no caller is touched.

**Anchor (enum).** Add a new value to the `Action` enum after `ActionRejectNotReady` (`:70`), and correct the `ActionBufferInbox` doc (`:48-52`) so it no longer claims the pod-held suspended case.

**Change (staged description).** Add:

```go
// ActionResumeAndDeliver drives the §7.2 path-6 pod-held resume-and-deliver:
// a `delivery: "immediate"` message to a `suspended` session whose pod is
// still held atomically resumes the session (`suspended → running`) and
// delivers the message to the runtime, returning `delivered`. The caller
// (the coordinating replica) performs the resume and the delivery and fails
// closed to inbox buffering (`queued`) when it cannot, per line 330.
// spec: §7.2 path 6 (lines 326-330).
ActionResumeAndDeliver
```

Amend the `ActionBufferInbox` doc to drop "and the pod-held suspended case of path 6" and, in its place, name only the non-immediate suspended case (a suspended target without `delivery: "immediate"` still buffers).

**Anchor (branch).** Replace the `suspended` case (`:152-153`):

```go
case state == session.StateSuspended:
	// §7.2 path 6. A `delivery: "immediate"` message atomically resumes a
	// pod-held suspended session and delivers it (ActionResumeAndDeliver,
	// `delivered`); the caller fails closed to inbox buffering when the pod
	// is not held or the resume/delivery fails, per line 330. A suspended
	// target without `immediate` remains buffered (line 330).
	if immediate {
		return Decision{Path: 6, Action: ActionResumeAndDeliver, Status: session.DeliveryStatusDelivered}
	}
	return Decision{Path: 6, Action: ActionBufferInbox, Status: session.DeliveryStatusQueued}
```

**Anchor (doc comment).** Update the `Classify` doc so the suspended bullet (`:121-126`) states that an immediate message selects `ActionResumeAndDeliver` and the caller resumes-and-delivers or fails closed to `queued`, and so the closing paragraph (`:133-136`) no longer asserts that `immediate` never changes the destination (it now selects the resume-and-deliver action for a suspended target). Carry `// spec: §7.2 path 6 (lines 326-330)`.

### C2. Handler: run the atomic resume-and-deliver on `ActionResumeAndDeliver`, failing closed to `queued`

**Target:** `pkg/gateway/sessionserver/messages.go`: the `deliverMessageBatch` switch (a new `case messagerouting.ActionResumeAndDeliver:` alongside `ActionDeliver` at `:537` and `ActionBufferInbox` at `:621`) and the `deliverMessageBatch` doc comment listing elided paths (`:507-520`).

**Rationale:** The REST handler owns the delivery flow and is where the session row (`PodAssignment`, `SuspendedAt`), the executor, and the inbox buffer are in scope. It must handle the new action by performing the coordinator-side resume-and-deliver and failing closed to `queued` so no message is dropped. The doc comment currently lists "the path-6 pod-held resume-and-deliver" among behaviors gated on the pod-adapter readiness model (`:515-516`) and must be corrected.

**Anchor.** Add a case to the switch after the `ActionDeliver` case (`:537-619`) and before `ActionBufferInbox` (`:621`).

**Change (staged description).**

```go
case messagerouting.ActionResumeAndDeliver:
	// §7.2 path 6 pod-held branch (lines 326-327): atomically resume the
	// suspended session and deliver. resumeHeldPod transitions
	// suspended → running reusing any already-bound pod, guarding a
	// still-suspended row inside its store.Update mutator so the check and
	// the write are atomic (every `suspended` row is pod-held while the
	// §6.2 release sweep is unbuilt). Fail closed to inbox buffering
	// (`queued`) on a resume or delivery failure so the message is never
	// dropped (line 330).
	if err := s.resumeHeldPod(r.Context(), tenantID, row.ID); err != nil {
		// Resume did not happen: leave the row suspended and buffer the
		// message. bufferIncomingMessages yields the `queued` receipt the
		// ActionBufferInbox case produces. The §16.3 taxonomy defines only
		// TRANSIENT, PERMANENT, POLICY, and UPSTREAM; a resume fault (a
		// store.Update write failure or a lost suspended-guard race) is
		// recoverable and never touched the pod, so TRANSIENT is its bucket,
		// matching the create-path store-write convention.
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
		if berr != nil {
			outcome.status = session.DeliveryStatusError
			outcome.reason = session.DeliveryReasonInboxUnavailable
			break
		}
		outcome.status = session.DeliveryStatusQueued
		outcome.queueDepth = depth
		if dropped {
			outcome.status = session.DeliveryStatusDropped
			outcome.reason = session.DeliveryReasonInboxOverflow
		}
		break
	}
	// The session is running. Deliver to the runtime; on a delivery failure
	// buffer to the inbox (`queued`) while leaving the session running (its
	// inbox drains on the next ready_for_input) rather than returning a 500.
	o, err := s.executor.Send(r.Context(), row.ID, msgs)
	if err != nil {
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryUpstream))
		dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
		if berr != nil {
			outcome.status = session.DeliveryStatusError
			outcome.reason = session.DeliveryReasonInboxUnavailable
			break
		}
		outcome.status = session.DeliveryStatusQueued
		outcome.queueDepth = depth
		if dropped {
			outcome.status = session.DeliveryStatusDropped
			outcome.reason = session.DeliveryReasonInboxOverflow
		}
		break
	}
	// Record and publish the response exactly as the ActionDeliver case does
	// (PostAgentOutput chain, transcript append, session-event publish, TTFT).
	// Extract that response-recording block into a shared helper reused by
	// both the ActionDeliver and ActionResumeAndDeliver cases so the two do
	// not duplicate it. On success stamp `delivered`.
	outcome.out = /* recorded parts */ nil
	outcome.status = session.DeliveryStatusDelivered
```

The response-recording body currently inlined in the `ActionDeliver` case (`messages.go:550-618`: `respAnnotations`, the `runPostAgentOutput` chain, the transcript append, the `message_delivered`/`response`/`response_degraded` publishes, and `recordTTFTOnce`) is extracted into one unexported helper (for example `recordDeliveredResponse`) and called from both cases, so the two delivery paths share a single canonical response-recording implementation rather than a copied block, per the reuse rule in `code-best-practices.md`.

Update the `deliverMessageBatch` doc comment (`:507-520`) to remove "the path-6 pod-held resume-and-deliver" from the list of behaviors a coordinating replica cannot drive, and to state that the pod-held immediate case now resumes-and-delivers on the coordinator and fails closed to `queued`. Carry `// spec: §7.2 path 6 (lines 326-330)`.

### C3. Add the coordinator-side in-place `resumeHeldPod` primitive

**Target:** `pkg/gateway/sessionserver/start.go`: a new method near `resumeOnPod` (`:3519`), reusing `transitionResume` (`sessionserver.go:3215`) and the legal `{Suspended, Running}` edge (`state.go:94`).

**Rationale:** No in-place resume-on-held-pod primitive exists. `resumeOnPod` (`:3519`) always claims a fresh warm pod (the podless resume), and `EndpointResume` excludes `suspended`. Path 6 needs a transition that reuses the already-bound `PodAssignment`. Isolating it as one small method keeps the atomic-resume concern in the sessionserver engine where the store and clock live, and leaves the fresh-claim resume path untouched.

**Change (staged description).** Add:

```go
// resumeHeldPod performs the §7.2 path-6 pod-held resume: it transitions a
// suspended session whose pod is still held from `suspended` to `running`,
// reusing any bound pod (no fresh warm-pod claim). It is the
// coordinator-side half of the atomic resume-and-deliver; the caller
// (deliverMessageBatch) delivers the message via executor.Send after this
// returns.
//
// It fails closed atomically: the state guard runs inside the store.Update
// mutator, which returns a typed guard error when the row is not
// session.StateSuspended before calling transitionResume. Under the pgstore
// SELECT ... FOR UPDATE row lock (pgstore.go:456-469) and the memstore mutex
// (memstore.go:120-122), the check and the transition commit as one critical
// section, so a concurrent terminal transition on the suspended row
// ({Suspended, Cancelled} via DELETE, {Suspended, Expired} via the watchdog,
// {Suspended, Completed}/{Suspended, Failed} via parent cascade, all legal at
// state.go:96-99) cannot slip between the guard and the write and resurrect a
// terminal session to running. Neither store validates transition legality, so
// the guard inside the mutator is the only thing preventing an illegal
// Terminal → Running write. A misroute (a non-suspended row) is a
// caller-visible error the handler maps to a `queued` fallback rather than a
// silent fresh claim. It does not require a non-empty PodAssignment: no §6.2
// sweep releases a suspended pod today, so every `suspended` row is pod-held in
// the currently reachable state set, and in --dev-mode the gateway builds no
// pod binder so PodAssignment is empty while the session still holds its
// in-process executor. §6.2 keeps a released session in `suspended` (podless)
// with no state change (spec/06_warm-pod-model.md:219,280); when the deferred
// §6.2 sweep lands the guard must additionally distinguish a pod-held row from a
// podless-suspended row and route the podless case to `resume_pending`/`queued`
// per §7.2 line 328. On the state write it reuses transitionResume
// (sessionserver.go:3215, which sets StateRunning; the {Suspended, Running} edge
// is legal at state.go:94) and emits the same resume status-change
// (emitStatusChange) and session.resumed lifecycle audit that handleResume
// emits (start.go:3194-3208).
//
// For the in-process/echo executor there is no pod adapter to signal, so the
// method returns after the state write; the runtime delivery is the caller's
// executor.Send. The real-pod in-place adapter-resume signal against a
// still-held pod (waiting for the §4.7 ready_for_input signal) is deferred.
// spec: §7.2 path 6 pod-held branch (lines 326-327).
func (s *Server) resumeHeldPod(ctx context.Context, tenantID, id string) error
```

Perform the state guard and the transition in a single `s.store.Update` call so they commit atomically under the row lock: `s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error { if row.State != session.StateSuspended { return errResumeHeldPodNotSuspended }; transitionResume(row); return nil })`. Returning the typed guard error from inside the mutator leaves the row unchanged and surfaces the error to the caller, which maps it to the `queued` fallback. Do not perform a separate `s.store.Get` guard before the `Update`: a check outside the mutator is not atomic with the write, so a concurrent terminal transition (a legal `{Suspended, Cancelled}`, `{Suspended, Expired}`, `{Suspended, Completed}`, or `{Suspended, Failed}` edge) landing between the read and the write would let `transitionResume` resurrect a terminal session to `running`, and neither store validates transition legality. Do not gate on `row.PodAssignment`, because every `suspended` row is pod-held in the currently reachable state set and the dev-mode target carries an empty `PodAssignment`. After a successful write, emit `s.emitStatusChange(updated.TenantID, updated.ID, updated.State)` and, when `s.lifecycleAudit != nil`, the `auditSessionResumed` lifecycle event, mirroring `handleResume` (`start.go:3194-3208`). Take `context.Context` first and propagate it. Run `gofumpt` and `goimports`.

### T1. tier-1: invert the messagerouting unit case and add the non-immediate branch

**Target:** `pkg/gateway/session/messagerouting/messagerouting_test.go`: the `path6_suspended_immediate_still_buffers` case (`:53-57`) and the surrounding table.

**Rationale:** The case at `:53-57` pins the current gap (suspended plus immediate still buffers). C1 changes the router contract, so this case must be inverted, and the non-immediate suspended case must stay covered at the tier the router change reaches.

**Change (staged description).** Invert `path6_suspended_immediate_still_buffers` to `path6_suspended_immediate_resumes`: `state: session.StateSuspended`, `immediate: true`, `src: SourceInterSession`, `wantPath: 6`, `wantAction: ActionResumeAndDeliver`, `wantStatus: session.DeliveryStatusDelivered`. Keep `path6_suspended_buffers_inbox` (`:48-52`, non-immediate) asserting `ActionBufferInbox`/`queued`. Add a case asserting an external-source suspended immediate message also selects `ActionResumeAndDeliver`/`delivered`, so both sources are covered. No table field is added. Carry `// spec: §7.2 path 6 (lines 326-330)`.

### T2. tier-2: `resumeHeldPod` transition and the handler fail-closed paths

**Target:** `pkg/gateway/sessionserver`: a component test alongside the existing `messages.go` / resume harness, exercising `resumeHeldPod` and `deliverMessageBatch` against a real session store with an injected executor.

**Rationale:** C2 and C3 add the coordinator-side resume-and-deliver and its two fail-closed fallbacks. These are injectable at the component tier (the store is real, `Options.Executor` is an injected interface), and they cover the non-happy paths the tier-4 real binary cannot reach (the echo executor never errors, and the router never routes a non-`suspended` row into resume-and-deliver). Per `test-coverage.md`, a change that reads and writes session state through the store reaches tier 2.

**Change (staged description).** Assert, across `StateSuspended` rows with and without a bound `PodAssignment`:

- **(a) happy resume-and-deliver:** `resumeHeldPod` on a `suspended` row with a non-empty `PodAssignment` (the real-pod shape) transitions the row `suspended → running` via `store.Update` and emits the status-change; a subsequent `deliverMessageBatch` on `ActionResumeAndDeliver` with a ready executor returns `delivered` and records the response. The non-happy path this guards is a suspended row left unresumed or a message dropped.
- **(b) dev-mode shape resume-and-deliver:** `resumeHeldPod` on a `suspended` row whose `PodAssignment == ""` (the `--dev-mode` / nil-pod-binder shape the tier-4 target runs) still transitions `suspended → running` and the handler delivers, returning `delivered`. This pins that the guard does not require a bound pod, so the tier-4 dev-mode outcome is reachable. The non-happy path is a pod-unbound suspended row wrongly rejected to `queued`. `// spec: §7.2 path 6 (lines 326-327)`.
- **(c) post-resume delivery fail-closed (spec-named failure):** with a fake `Options.Executor` whose `Send` returns an error, a successful `resumeHeldPod` followed by the failed `Send` buffers the message to the inbox (`queued`) and leaves the session `running`. The non-happy path is a dropped message or a 500 on a delivery failure after a committed resume.
- **(d) resume-guard fail-closed on a misroute and on a concurrent terminal transition (spec-named failure, race):** `resumeHeldPod` on a non-`suspended` row (a misroute) returns the typed guard error from inside the `store.Update` mutator and performs no transition and no fresh claim; the handler buffers the message and the receipt is `queued`, the row unchanged. A companion race assertion drives a row that is `suspended` when the handler routes but terminal (for example `cancelled` via a concurrent `DELETE`) by the time the mutator runs, and asserts the mutator guard rejects the write so the row stays terminal rather than being resurrected to `running`. Because the guard and the transition share the store's row lock, the check and the write are atomic and no illegal Terminal → Running state is persisted. This is the fail-closed path the handler's resume-failure fallback (C2) drives. `// spec: §7.2 path 6 (line 330 queued fallback)`.

Carry `// spec: §7.2 path 6 (lines 326-330)` and a `// diagnosis:` comment stating that a failure means the coordinator did not atomically resume-and-deliver a pod-held immediate message, or did not fail closed to `queued` while preserving the message.

### T3. tier-4: invert the interactive-iteration test to assert `delivered` + `running`

**Target:** `tests/tier4_integration/interactive_iteration_test.go`: the fourth-prompt assertions (`:172-190`) and the header comment (`:26-44`).

**Rationale:** This live test pins the current gap: it sends a `delivery: "immediate"` fourth prompt to a suspended, pod-held session and asserts `queued` (`:177-178`) and still-`suspended` (`:188-190`), with a header noting the atomic resume-and-deliver is out of scope (`:35-38`). It is the T-JRN.1 tier-4 test and must be inverted to assert the spec-required `delivered` + `running`.

**Change (staged description).** Narrow the edit to the single happy-path inversion plus the header rewrite. Replace the fourth-prompt block (`:164-190`):

- The receipt status assertion changes from `queued` to `delivered` (`:177-178`).
- The "buffered message must not produce executor output" assertion (`:180-182`) changes to require a non-empty echo output for the fourth prompt (the message was delivered), with the echo containing the fourth prompt text.
- The post-message session-state assertion changes from `suspended` (`:188-190`) to `running` (the atomic resume transitioned it).
- Assert a non-empty `deliveredAt` on the receipt.
- The transcript assertions (`:192-226`) change from six entries (three exchanges) to eight entries (four exchanges), and the "transcript must not record the buffered fourth prompt" guard (`:221-226`) is removed, since the fourth prompt is now delivered and transcribed. Extend the ordered-exchange loop to cover all four prompts.

Rewrite the header comment (`:26-44`) to state that the fourth `delivery: "immediate"` prompt to the suspended, pod-held session atomically resumes the session and delivers, per §7.2 path 6 line 327, and that the session returns to `running`. Remove the sentence that records the resume-and-deliver as unimplemented and out of scope.

The fail-closed fallback paths (resume-guard rejection on a misroute, post-resume delivery failure) are not injectable against the real binary: the dev-mode echo executor is always ready and errors on no message, a suspended dev-mode session always holds its pod so the router never routes a non-`suspended` row into resume-and-deliver, and the binary exposes no fault-injection flag. Those paths are covered at tier 2 by T2 (cases c and d) where the executor and the non-`suspended` misroute row are injectable. Do not add tier-4 sub-cases for them, and do not advance a clock (the design carries no hold-deadline). Preserve the existing `// spec:` and `// diagnosis:` annotation form, updated to the resume-and-deliver behavior.

### DOC1. `TEST-GAPS.md`: record T-JRN.1's tier-4 closure and the deferrals

**Target:** `TEST-GAPS.md`: T-JRN.1 (`:11898-11904`).

**Rationale:** T-JRN.1's tier-4 multi-prompt-with-interrupt-resume loop becomes closable once path 6 delivers. The cross-section prerequisites and the tier-5 real-pod variant are distinct follow-on features that must be recorded so they are not lost or double-claimed, especially the §6.2 pod-release sweep, which is warm-pod-adjacent and must not collide with proposal-B's C-22 (§4.4/§4.6 eviction).

**Change (staged description).** On application, update T-JRN.1's "Needs human input" note to record that option (a) was taken: the §7.2 path-6 pod-held atomic resume-and-deliver is built (router `ActionResumeAndDeliver`, handler resume-and-deliver, `resumeHeldPod`), and the tier-4 interactive-iteration loop now pins the spec-required `delivered` + `running`. Leave T-JRN.1 OPEN for its tier-5 real-pod half (the `interactive_session_test.go` variant against a real `echo-runtime-sidecar` pod), which is blocked on the deferred §4.7 held-pod adapter-resume signal, and record that dependency. Add OPEN follow-on findings for: the §6.2 pod-release-during-suspension sweep (consume `maxSuspendedPodHoldSeconds`, checkpoint the workspace, release the held pod, clear the pod binding, and keep the session in `suspended` (podless) with no state change per §6.2; the `suspended → resume_pending` transition then fires when a `delivery: "immediate"` message later reaches the now-podless session, which requires teaching `resumeHeldPod` and the router to distinguish a pod-held row from a podless-suspended row, flagged against C-22 to avoid a double-claim); the §4.7 adapter `ready_for_input` signal plus the 30-second delivery timeout and the real-pod in-place adapter-resume signal; the §10.1 `ForwardMessage` cross-replica coordinator forwarding; and the MCP `send_message` delivery-field threading. Applied at implementation time, consistent with how findings are closed.

## 6. Non-goals

- **No spec edit.** §7.2 path 6 (`spec/07_session-lifecycle.md:326-330`) fully specifies the pod-held, pod-released, and coordinator-forwarding behavior; this is a code-to-spec build gap.
- **No `podHeld` parameter on `Classify` and no hold-deadline predicate.** An earlier draft threaded a `podHeld bool` into the router, computed from `PodAssignment != "" && !SuspendedAt.IsZero() && clock().Sub(SuspendedAt) < maxSuspendedPodHoldSeconds`. It is dropped. `Classify` already receives `immediate`, no sweep releases a suspended pod so the deadline term is spec-divergent for a reachable past-window still-held session, and the handler-side `resumeHeldPod` guard covers the pod-not-held edge. The signature stays `Classify(state, inputRequired, immediate bool, src Source)`.
- **No `maxSuspendedPodHoldSeconds` plumbing into the session server.** With the deadline predicate dropped, there is no session-server consumer for the value. It stays where the deferred §6.2 sweep will read it (the watchdog config), and no new duration field is added to `Server`, `Options`, or the session row.
- **No §6.2 pod-release-during-suspension sweep.** The watchdog has no `sweepSuspended` today (a suspended session always holds its pod), so the pod-held branch is the only reachable case; building the release sweep (which releases the pod and keeps the session in `suspended` (podless) per §6.2), the `suspended → resume_pending` transition on a later `delivery: "immediate"` message to the podless session, and the release counter is deferred. It is warm-pod-adjacent and must be flagged against proposal-B's C-22 (§4.4/§4.6 eviction) to avoid a double-claim.
- **No §4.7 adapter `ready_for_input` signal, no 30-second delivery timeout, and no real-pod in-place adapter-resume against a still-held pod.** The in-process echo executor is always ready, so the tier-4 `delivered` outcome needs none of this; the real-pod readiness-gated delivery is deferred.
- **No §10.1 `ForwardMessage` cross-replica coordinator forwarding.** A single replica is always the coordinator; a non-coordinator replica's `queued` fallback is left until `ForwardMessage` lands.
- **No MCP `send_message` delivery-field threading.** The tool schema exposes no `delivery` field, so adding one is new surface, deferred with the other MCP-path work. The MCP `Classify` call (`mcptools_register.go:356`) is unchanged; it passes `immediate=false` and its suspended messages remain buffered, which is spec-consistent.
- **No tier-5 real-pod `delivered` assertion in this proposal.** It depends on the deferred §4.7 held-pod adapter-resume signal. T-JRN.1 stays OPEN for that half.
- **No change to the interrupt/suspend path (`interrupt.go`) or to `resumeOnPod`'s fresh-pod claim (`start.go:3519`),** which continues to serve the podless `awaiting_client_action` resume.
- **No new wire field, RPC, or endpoint.** `ActionResumeAndDeliver` is an internal router enum value; the `deliveryReceipt` schema and the `/messages` request are unchanged.

## 7. Testing

The change reaches tier 0 (static), tier 1 (the pure `Classify` suspended branch), tier 2 (`resumeHeldPod`'s state transition and the two handler fail-closed fallbacks through a real store with an injected executor), and tier 4 (the end-to-end `delivery: "immediate"` resume-and-deliver against the dev-mode echo executor), per `.claude/rules/test-coverage.md`. Each test below covers a non-happy path and carries a `// spec:` tie.

- **tier-1 router resume-and-deliver (T1, boundary):** `Classify` returns `ActionResumeAndDeliver`/`delivered` for a `suspended` target with `immediate` set (both sources) and `ActionBufferInbox`/`queued` without it. The non-happy path is the inverted gap case that returned `queued` for an immediate suspended message. `// spec: §7.2 path 6 (lines 326-330)`.
- **tier-2 resume-and-deliver happy transition (T2 case a):** `resumeHeldPod` on a `suspended` row with a non-empty `PodAssignment` transitions `suspended → running` reusing the bound pod and emits the status-change; the handler then delivers and stamps `delivered`. The non-happy path is a suspended row left unresumed. `// spec: §7.2 path 6 (lines 326-327)`.
- **tier-2 dev-mode shape resume-and-deliver (T2 case b):** `resumeHeldPod` on a `suspended` row whose `PodAssignment == ""` (the dev-mode / nil-pod-binder shape) still transitions `suspended → running` and the handler delivers, returning `delivered`. The non-happy path is a pod-unbound suspended row wrongly rejected to `queued`, which would make the tier-4 dev-mode outcome unreachable. `// spec: §7.2 path 6 (lines 326-327)`.
- **tier-2 post-resume delivery fail-closed (T2 case c, spec-named failure):** a fake executor whose `Send` errors after a committed resume causes the message to buffer to `queued` with the session left `running`. The non-happy path is a dropped message or a 500 after the resume committed. `// spec: §7.2 path 6 (line 330 not silently dropped)`.
- **tier-2 resume-guard fail-closed (T2 case d, spec-named failure, race):** `resumeHeldPod` on a non-`suspended` row (a misroute) returns the guard error and performs no transition or fresh claim; the handler buffers to `queued` with the row unchanged. A companion race assertion pins that the guard, running inside the `store.Update` mutator, rejects a row that went terminal after routing but before the mutator ran, so no Terminal → Running write is persisted. The non-happy path is a silent drop or a fresh-pod claim on a misrouted row, and the resurrection of a terminal session to `running` when a concurrent terminal transition lands between routing and the mutator. `// spec: §7.2 path 6 (line 330 queued fallback)`.
- **tier-4 end-to-end resume-and-deliver (T3, spec-named failure):** the real dev-mode binary delivers a `delivery: "immediate"` fourth prompt to a suspended, pod-held session, returning `delivered` with a non-empty `deliveredAt`, a fourth echo exchange, and a `running` session with four ordered transcript exchanges. The non-happy path is the divergence T-JRN.1 records: an immediate message to a pod-held suspended session that returns `queued` and leaves the session `suspended`. `// spec: §7.2 path 6 (lines 326-330)`.

## 8. Findings closed on application

This proposal takes option (a) of T-JRN.1 (High, `TEST-GAPS.md:11898`): it builds the §7.2 path-6 pod-held atomic resume-and-deliver (the router `ActionResumeAndDeliver`, the handler resume-and-deliver with the `queued` fail-closed fallbacks, and the `resumeHeldPod` primitive) and inverts the tier-4 interactive-iteration test to pin the spec-required `delivered` + `running`. T-JRN.1's tier-4 multi-prompt-with-interrupt-resume half is closed. Its tier-5 real-pod half stays OPEN, blocked on the deferred §4.7 held-pod adapter-resume signal, and is recorded with that dependency. The three cross-section prerequisites (§6.2 pod-release sweep, §4.7 readiness signal and timeout, §10.1 `ForwardMessage`) and the MCP delivery-field threading are recorded as distinct OPEN follow-on findings so they are not double-claimed, and the §6.2 sweep is flagged against proposal-B's C-22 (§4.4/§4.6 eviction). The change needs no operator hardware beyond the Redis-backed inbox the tier-4 interactive-iteration test already wires.

## 9. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. Three challenge-round revisions are already folded into the staged changes above.

- **The router `podHeld` parameter was removed.** An earlier draft added a `podHeld bool` to `Classify`'s signature, fed by a deadline predicate. `Classify` already receives `immediate`, which is the only signal the reachable pod-held case needs, and no sweep releases a suspended pod, so the parameter carried no reachable decision. The deadline term additionally diverged from §7.2 path 6 for a reachable case (a session suspended past `maxSuspendedPodHoldSeconds` still holds its pod, so `delivered` is required, yet the deadline predicate would return `queued`). C1 now branches the suspended case on `immediate` alone, the signature is unchanged, and the pod-not-held edge is covered by the `resumeHeldPod` guard (C3) that the handler maps to a `queued` fallback (C2).
- **The `maxSuspendedPodHoldSeconds` session-server plumbing and its `Options`/`Server` field were dropped.** With the deadline predicate gone there is no session-server consumer for the value; it stays in the watchdog config where the deferred §6.2 sweep reads it. The prior draft's separate plumbing change and the MCP call-site signature update are both removed, since C1 keeps the `Classify` signature stable.
- **The tier-4 test scope was narrowed to the happy-path inversion.** An earlier draft prescribed two fail-closed sub-cases at tier 4 (a deadline-exceeded path driven by advancing an injectable clock, and a resume-then-deliver failure). Neither is injectable against the real dev-mode binary: the process clock offset is read once at startup and cannot be advanced mid-test, a suspended dev-mode session always holds its pod, and the echo executor never errors. Those paths moved to tier 2 (T2 cases c and d), where the store row and `Options.Executor` are injectable, and the false injectable-clock instruction was struck. The design carries no hold-deadline, so there is no clock to advance.

### Pass 1 (2026-07-22, automated)

- **The `resumeHeldPod` guard no longer requires a non-empty `PodAssignment`, so the tier-4 `delivered` + `running` outcome is reachable in `--dev-mode`.** The prior draft guarded the resume on `session.StateSuspended` with a non-empty `PodAssignment` (C3 `:189-192`, `:206`), and premised the tier-4 delivered outcome on the interrupt path leaving `PodAssignment` bound (§1 `:23`, §3 `:37`). In `--dev-mode` the gateway builds no pod binder: `cmd/lenny-gateway/stores.go:1926` gates the only `podBinder` construction on `--agent-namespace`, and every `PodAssignment` write is nested under `if s.podBinder != nil` (`pkg/gateway/sessionserver/start.go:747`, `:768`), so a dev-mode session's `PodAssignment` is never set. `interrupt.go:87-91` writes only the state, `SuspendedAt`, and `SuspendedReason`, so it cannot create a binding that was never bound. The `PodAssignment != ""` conjunct would therefore reject every suspended dev-mode session and force the `queued` fallback the tier-4 `TestInteractiveIterationInterruptThenQueuedMessage` (`tests/tier4_integration/interactive_iteration_test.go:55` runs `--dev-mode` with no `--agent-namespace`) is being inverted away from, so the T-JRN.1 tier-4 closure would not hold. The guard now gates on `session.StateSuspended` alone, which is correct for today's reachable set because no §6.2 sweep releases a suspended pod, so every `suspended` row is pod-held. When the deferred sweep lands, the guard gains a pod-held/podless distinction (see Pass 2, which corrects an earlier claim that the sweep moves released sessions out of `suspended`). The premises in §1 and §3, the C3 guard doc and prose, the edge-case table rows, the Decision-5 statement, and the Open-decision on pod-not-held handling were all restated to the `StateSuspended`-only predicate.
- **T2 case (b) was inverted from a pod-unbound rejection to a pod-unbound success, and the resume-guard fail-closed coverage moved to case (d).** The prior case (b) pinned a suspended row with `PodAssignment == ""` returning the guard error and falling back to `queued`, which contradicts the corrected guard. Case (b) now pins that a `suspended` row whose `PodAssignment == ""` (the dev-mode shape) resumes-and-delivers to `delivered`, covering the tier-4 dev-mode shape at tier 2. The resume-guard fail-closed path the handler's C2 fallback drives is now covered by case (d) via a non-`suspended` misroute row. The §7 testing bullets and the T3 cross-reference (`cases c and d`) were updated to match.

### Pass 2 (2026-07-22, automated)

- **The false claim that the §6.2 sweep moves a released pod out of `suspended` was corrected everywhere it appeared.** The prior text justified gating `resumeHeldPod` on `session.StateSuspended` alone by asserting that a `suspended` row is pod-held by spec definition, because "§7.2 line 328 moves a pod-released suspension to `resume_pending`, so once the §6.2 sweep lands a released pod is no longer `suspended` and this guard need not change". §6.2 says the opposite: on pod release the "session remains in `suspended` — no state change" (`spec/06_warm-pod-model.md:219`) and "after `maxSuspendedPodHoldSeconds` fires, the pod is released but the session stays in `suspended` (podless) until the client acts" (`spec/06_warm-pod-model.md:280`). The `suspended → resume_pending` transition in §7.2 line 328 is the gateway's response to a `delivery: "immediate"` message arriving at an already-podless suspended session, rather than an effect of the release sweep. A podless-`suspended` row is therefore a reachable state once the deferred sweep lands, and a guard gating on `StateSuspended` alone would resume-and-deliver it to `delivered` instead of routing it to `resume_pending`/`queued`. The justification was restated in §1, Decision 5, the C2 and C3 doc comments, the C3 staged mutator prose, the edge-case table's podless-suspended row, the DOC1 §6.2 follow-on note, the Non-goals §6.2 entry, the Pass 1 record, and the Open-decision on pod-not-held handling: a `suspended` row is pod-held only because the release sweep is unbuilt, gating on `StateSuspended` alone is correct for today's reachable set, and when the sweep lands the guard must additionally distinguish a pod-held row from a podless-suspended row and route the podless case to `resume_pending`/`queued` per §7.2 line 328. The "guard need not change" claim was dropped.
- **The `resumeHeldPod` guard was made atomic with the `suspended → running` write so a concurrently-terminated session cannot be resurrected to `running`.** The prior C3 ran the `StateSuspended` guard in a separate `s.store.Get` and then staged the transition as an unconditional `s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error { transitionResume(row); return nil })`. Neither store validates transition legality: the memstore `Update` applies the mutator under its mutex with only generation and sequence floor clamps (`memstore.go:120-162`), and the pgstore re-reads the row `FOR UPDATE` and then applies the mutator unconditionally (`pgstore.go:456-469`); `transitionResume` unconditionally sets `StateRunning` (`sessionserver.go:3215`). A concurrent terminal transition on the suspended row (`{Suspended, Cancelled}` via `DELETE`, `{Suspended, Expired}` via the watchdog, `{Suspended, Completed}`/`{Suspended, Failed}` via parent cascade, all legal at `state.go:96-99`) landing between the `Get` and the `Update` would let `transitionResume` write an illegal Terminal → Running state, a fail-open regression on the terminal-state invariant that the shipped `ActionBufferInbox` path (which writes no session state) does not have. C3 now performs the `row.State != session.StateSuspended` guard inside the `store.Update` mutator itself, returning a typed guard error before `transitionResume`, so the pgstore `FOR UPDATE` row lock and the memstore mutex make the check-and-transition one critical section. A lost race returns the guard error, which the C2 fallback maps to `queued` with the message preserved and the session left in its terminal state. T2 case (d) and the §7 testing bullet gained a race assertion covering a row that is `suspended` at routing time but terminal by the time the mutator runs, and §3 and Decision 5 were restated to the single-`store.Update` guard.

## 10. Open decisions for review

- **Confirm the deferrals.** Build only the reachable coordinator core (router, handler, `resumeHeldPod`, and the tier-1/2/4 tests) and defer the §6.2 pod-release sweep, the §4.7 adapter `ready_for_input` signal and 30-second timeout, the §10.1 `ForwardMessage` cross-replica forwarding, the MCP delivery-field threading, and the tier-5 real-pod variant to the integrator inbox. Recommended: defer all five.
- **Confirm the pod-not-held handling.** The `resumeHeldPod` guard gates on `session.StateSuspended` alone (checked inside the `store.Update` mutator so it is atomic with the transition) and does not require a non-empty `PodAssignment`, because no §6.2 sweep releases a suspended pod today, so every `suspended` row is pod-held in the currently reachable state set (and the `--dev-mode` target carries an empty `PodAssignment` while still holding its in-process executor). The pod-released `suspended → resume_pending` transition and its `queued` receipt are unreachable today because no sweep releases a suspended pod. Recommended: gate on `StateSuspended` alone now. When the deferred §6.2 sweep lands, it keeps a released session in `suspended` (podless) rather than moving it out of the state, so the guard must then additionally distinguish a pod-held row from a podless-suspended row and route the podless case to `resume_pending`/`queued` per §7.2 line 328.
- **Confirm the MCP scope.** Leave `lenny/send_message` unthreaded (its suspended messages stay buffered, which is spec-consistent for the messages the tool can express) rather than adding a `delivery` field to the tool schema now so inter-session immediate messages also trigger path 6. Recommended: defer MCP threading with the other MCP-path work.

## 11. Files touched on application

- `pkg/gateway/session/messagerouting/messagerouting.go`: C1 (add `ActionResumeAndDeliver` to the `Action` enum at `:42-71`, branch the suspended case at `:152-153` on `immediate`, and correct the `Classify` and `ActionBufferInbox` doc comments).
- `pkg/gateway/sessionserver/messages.go`: C2 (add the `ActionResumeAndDeliver` case to `deliverMessageBatch` between `:537` and `:621`, extract the shared `recordDeliveredResponse` helper from the `ActionDeliver` body, and correct the `deliverMessageBatch` doc comment at `:507-520`).
- `pkg/gateway/sessionserver/start.go`: C3 (add `resumeHeldPod` near `resumeOnPod` at `:3519`, reusing `transitionResume` at `sessionserver.go:3215` and the `emitStatusChange`/lifecycle-audit signals at `:3194-3208`).
- `pkg/gateway/session/messagerouting/messagerouting_test.go`: T1 (invert the suspended-immediate case at `:53-57`, keep the non-immediate case, add the external-source immediate case).
- `pkg/gateway/sessionserver` (component test): T2 (`resumeHeldPod` transition, resume-guard fail-closed, post-resume delivery fail-closed, non-suspended guard).
- `tests/tier4_integration/interactive_iteration_test.go`: T3 (invert the fourth-prompt assertions at `:172-190` to `delivered` + `running` + four transcript exchanges, and rewrite the header comment at `:26-44`).
- `TEST-GAPS.md`: DOC1 (record T-JRN.1's tier-4 closure and the deferred follow-on findings, leaving the tier-5 half OPEN).
