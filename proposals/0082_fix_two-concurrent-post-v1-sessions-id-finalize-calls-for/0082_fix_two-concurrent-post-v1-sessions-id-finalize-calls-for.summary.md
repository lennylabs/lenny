# Summary: Concurrent finalize calls both prepare the session's pod

## Summary

**Problem statement.** handleFinalize checks the §15.1 `created` precondition against a row it read before taking any lock, and then writes every state change unconditionally. Two keyless or different-key `/finalize` calls that both read `created` both commit `finalizing` and both run the prepare phase against the one pod claimed at `/create`. The loser duplicates staging, setup, and credential work, can demote an SDK-warm runtime, can drain the shared pod, and overwrites the winner's state and WorkspacePlan (the entry race). Separately, a DELETE, a terminate, or the `maxFinalizingTimeoutSeconds` watchdog can make the row terminal while the prepare phase runs, and the still-running handler then writes `ready` or `failed` over the terminal state (the exit race). Both halves are one defect: the finalize state writes do not check the locked row. The full evidence is in the problem statement file.

**What changes.**

- `spec/15_external-api-surface.md` §15.1: one sentence appended to the finalize row's Notes cell, stating the response to a finalize call that a terminal writer overtook (SPEC-1).
- `pkg/gateway/sessionserver/sessionserver.go` handleFinalize: the created → finalizing Update re-checks the precondition against the locked row (CODE-2).
- `pkg/gateway/sessionserver/start.go`, `finalize.go`, and `sessionserver.go`: the ready write and every finalize failure write admit only `finalizing`, and a lost exit write revokes only the session's lease (CODE-1, CODE-3).
- `docs/api/rest.md` and `docs/api/mcp.md`: the finalize reference states the overtaken-call outcome (DOCS-1).
- Tests at tiers 1, 2, 4, and 7a (TEST-1 through TEST-4).

**Decisions.**

- The entry guard calls the existing session.Validate inside the Update mutation. No generic transition-guard helper or new file is added.
- The exit guards are in scope and mandatory.
- A lost exit write never calls reclaimFinalizedPod. It revokes only the session-keyed lease through `Credentials.ReleaseSession`.
- Every refused or superseded finalize write returns the existing *session.PreconditionError, rendered as 409 by writePreconditionError. No new sentinel or error code is added.
- A superseded prepare failure answers as SPEC-1 states.
- failSession stays unconditional for its non-finalize callers. The finalize handler uses a new failFinalizing.
- The upload-token consume moves ahead of the ready write, so `finalizing` is the only from-state of any finalize failure write.
- The spec change is the single SPEC-1 sentence in the §15.1 finalize row. The §15.1 preamble, §6.2, §7.1, §7.2, and §29 are not edited.
- Citation repair covers only the code this change edits.
- The finalize request context stays request-scoped.

**Watch out for.**

- `podclaim.DeleteClaim` deletes `claim-<podName>` with no owner check. A reclaim issued after a terminal writer has released the pod can delete a successor session's claim on a recycled pod. This is why a lost exit write revokes the lease and does not reclaim.
- The pre-lock Get and Validate stay, because resolveFinalizePlan needs the row. They are an early rejection only; the in-mutation check is authoritative. A test that issues the second call after the first commits passes on the unfixed code, so every race test must force both pre-lock reads ahead of either entry Update.
- The existing Update closure names its parameter `r`, which shadows the *http.Request. The rewritten closures name it `row`.
- At tier 1, Prepare cannot succeed without an adapter, so `prep` is always nil. Lease-revoke and no-pod-RPC assertions belong at tier 4 (TEST-3), and a tier-1 assertion of a ReleaseSession call cannot pass.
- `Server.podBinder` is the concrete `*podsession.Binder`. Tests build a real Binder over a fake or envtest client rather than a fake binder.
- `tests/flake-budget.yaml` is the quarantine registry. The TEST-4 stress budget is declared in the test file's doc comment.
- Proposal 0081 edits `start.go` and the Binder.Prepare reclaim closure that CODE-3's prepare-failure path relies on. A second-to-land rebase is textual; see the 0081 row under Impacts on other proposals.

## Goals

- Exactly one `/finalize` call per session commits `finalizing`, and every other call is refused under the row lock before any pod RPC, reclaim, failure write, or WorkspacePlan write.
- A finalize call never writes `ready` or `failed` over a state another writer committed, and never emits a second terminal lifecycle.
- A lease that AssignCredentials issued after a terminal revoke is released.
- A finalize call that a terminal writer overtook returns the response SPEC-1 states.
- The finalize code touched by this change cites §7.1, §15.1, and §6.2 rather than the refuted "§4.3 preparation barrier".

## Non-goals

- Leaving the exit writes out of scope. Rejected because the overwrite already breaks §6.2's FINALIZE_TIMEOUT rule, the §15.1 terminate and DELETE rows, and §7.2 without a concurrent client, and the fix uses the same mechanism in the same function.
- Calling reclaimFinalizedPod when the ready write or a failure write loses to a terminal writer. Rejected because DeleteClaim deletes `claim-<podName>` with no owner check after the terminal path has already released the pod, so a late delete can remove a successor session's claim on a recycled pod. Only the session-keyed lease revoke is safe.
- A global IsTerminal guard inside failSession for every caller. Rejected because the `/start` and tree-recovery callers belong to the `/start` serialization work 0081 assigns to a holder claim. The finalize-scoped failFinalizing keeps the change scoped.
- A failSession variant with an arbitrary guard, or an admit function accepting both `finalizing` and `ready`. Superseded by moving the consume before the ready write, which leaves `finalizing` as the only from-state.
- A new sentinel such as errFinalizeSuperseded or errFinalizeNotCreated. Rejected because *session.PreconditionError already carries the locked state and renders through writePreconditionError. A sentinel would need its own mapping and would lose `details.currentState`.
- Returning the prepare-phase error (SESSION_CREATION_FAILED, SETUP_COMMAND_FAILED) when the failure write loses. Rejected because it tells the client the session failed when it is `cancelled` or `completed`.
- A generic transitionguard.go helper presented as the reuse point for other handlers. Rejected because it would restate the §15.1 precondition table beside session.Validate and would drop the capability-gated states when reused on a gated endpoint.
- A new §7.1 paragraph, or any §6.2, §7.2, or §29 restatement. Rejected because the §15.1 finalize row is the endpoint's single contract home.
- Restating in SPEC-1 the atomic admission, the overlapping-call refusal, the finalizing-only closing write, or the lease revoke. Rejected because the §15.1 preamble, the terminate and DELETE rows, §6.2, and §7.1 step 23 already state them.
- No spec change at all. Rejected because the response to an admitted call that a terminal writer overtook is a client-observable outcome that no spec text states.
- A general atomic-admission rule in the §15.1 preamble for every state-mutating endpoint. Rejected because it is false for `/start` and would place handleTransition and handleDelete out of conformance on the day it lands.
- Running the post-admission prepare phase on a detached context bounded by `maxFinalizingTimeoutSeconds`. Excluded because it adds new spec behavior, a server option, and cross-package constant movement for a fault that predates this race. It is recorded under Defects in the shipped tree that this proposal does not stage.
- A tier-8 owner-crash chaos test and a tier-3 envelope contract test. Excluded because no crash-recovery path or wire envelope changes.
- Guarding the SetupOutput and WorkspaceRoot persists in applyFinalizePrepareResult. Excluded because they write no state, and a setup trail on a terminal row is harmless audit data.
- Removing the pre-lock Get and Validate. Rejected because resolveFinalizePlan needs the row, and the early check answers 404 or 409 before the body is read and blobs are resolved.
- A per-session in-process mutex or singleflight in handleFinalize. Rejected because it does not span gateway replicas, and the row lock already exists.
- A Redis SETNX lock modeled on the derive lock. Rejected because it adds a second serialization surface next to the row lock.
- Acquiring the §10.1 coordination lease at `/finalize`, or extending proposal 0060's co-location to finalize. Rejected because §29 states that no holder exists in `created`, `finalizing`, or `ready`, and a compare-and-swap on a declared edge suffices.
- Bumping or checking `coordination_generation`, or adding a `finalize_attempt` column. Rejected because `created` is left exactly once, so no ownership passes to a second call.
- Making Idempotency-Key mandatory on `/finalize`, or generating one in the SDKs. Rejected because it changes the §11.5 optional-key contract, does not stop callers with different keys, and does nothing for the exit race.
- Using the single-use upload token as the admission fence. Rejected because handleFinalize does not validate the token, the consume tracker can be nil, and the token is not transactional with the row state.
- Validating transitions inside `sessionstore.Update`. Rejected because it changes the documented store contract for every caller and would break the watchdog, terminate, and DELETE.
- A new conditional-update store method. Rejected because the mutate-error idiom already provides the compare-and-swap on both stores.
- A new error code such as FINALIZE_IN_PROGRESS. Rejected because the §15.1 preamble already mandates 409 INVALID_STATE_TRANSITION.
- Join semantics, where a loser waits for and returns the winner's result. Rejected because it needs a cross-replica wait channel and contradicts §15.1's 409 rule.
- Handler-side recovery after an ambiguous entry commit. Rejected because the handler cannot tell its own commit from a concurrent winner's, so it could kill a live winner. It returns 500, and the watchdog resolves an orphaned `finalizing` row.
- A metric counting refused concurrent finalizes. Excluded because no spec text calls for it.
- Staging any edit to proposal 0081's files.

## Open decisions for human to make

- **Should the 409-versus-410 precedence for a finalize call made after the upload token was consumed be recorded as a follow-up finding?** §7.1 states that the gateway validates the upload token on every finalize call and that a consumed token returns `410 UPLOAD_TOKEN_CONSUMED`. handleFinalize validates no token today, so a finalize call that arrives after another call has moved the session to `ready` receives `409 INVALID_STATE_TRANSITION` from the §15.1 preamble. No spec text states which of the two responses wins. The gap predates this proposal, and this proposal does not change it. The choice is whether to add it to "Defects in the shipped tree that this proposal does not stage" as a follow-up finding, or to leave it unrecorded. The spec loop derived no recommendation; the open-decisions-and-impact-review phase supplies one. Source: review-log entry `[spec.1.review-docs-alignment.1]`.

## Defects in the shipped tree that this proposal does not stage

- **The prepare phase runs on the request context.** When a client disconnects mid-Prepare, the context is cancelled, the finalize failure write runs on a cancelled context, and the row stays in `finalizing` until the watchdog fires (600 s by default). After this proposal, a proxy's reset-and-retry receives 409 rather than re-running finalize. Detaching the post-admission work needs new §6.2 text and a server option, and the fault predates the race, so it stays out and is recorded as a follow-up finding.
- **The §15.1 terminate row's "aborts the in-progress setup" is not implemented.** Terminate from `finalizing` does not cancel a running Prepare on another replica. Implementing it needs a cross-replica cancel signal. This proposal only stops finalize from overwriting the terminal state.
- **DemoteSDK is unfenced.** The adapter's DemoteSDK releases whichever registry entry exists. Fencing it with a session identifier or bind-attempt token is an adapter protocol change that belongs with 0081's forward-RPC fencing gap. The entry guard makes it unreachable from concurrent `/finalize`.
- **Concurrent `/start` is not serialized.** 0081 records that it needs a bounded holder-and-expiry claim, which the created → finalizing guard does not provide.
- **handleTransition and handleDelete read, validate, and write unconditionally.** Their failure mode is a lost or overwritten state transition. A follow-up finding converts them, building any shared guard on session.Validate so it stays capability-aware.
- **Stale "§4.3" citations remain elsewhere.** prepareAtFinalize and applyFinalizePrepareResult in `finalize.go`, and several sites in `start.go`, still cite §4.3. They are left for a citation-sweep finding.

## Impacts on other proposals

| Proposal | Status | What 0082 does to it | What it must do |
|:--|:--|:--|:--|
| 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry | Draft | No 0081 deliverable loses its subject. 0081 already names 0082 as the remedy for concurrent `/finalize` in its known-defects bullet (summary.md:1170-1182), so that bullet needs no change. CODE-8's Binder.Launch arm is unaffected. The Binder.Prepare arm of CODE-8's reclaim short-circuit loses the only trigger 0081 names for it: two concurrent `/finalize` calls producing two Prepare attempts (summary.md:149-153, non-spec-changes.md:1197-1200 and :2032-2034). After 0082, exactly one `/finalize` commits `finalizing` per session, and a refused call returns before prepareAtFinalize. A terminate, a DELETE, or the watchdog does not mint a bind attempt and so cannot produce SLOT_BIND_ATTEMPT_SUPERSEDED on the Prepare path. | 0081 must either name another reachable trigger for the Prepare arm or state it as a guard with no known path once 0082 lands, and it must drop concurrent `/finalize` as the justification. There is no landing-order dependency. Both proposals edit spec/15 §15.1 in different tables: 0082 edits the precondition table's finalize row, and 0081 edits the error catalog's SETUP_COMMAND_FAILED row. 0081's §29.4 note cites that precondition table, so the note must stay true after 0082's added passage. Code overlap spans `start.go` (0082 extracts failSession's tail, and 0081 edits isTransientPodClaimError and applySlotRetryPolicy) and `sessionserver.go` (0082 edits handleFinalize, and 0081's CODE-3 edits only the slotLeakGauge comment). 0082's prepare-failure path relies on whichever Binder.Prepare reclaim closure is current, including 0081's refusal-aware one. A second-to-land rebase is textual. |

## Deliverable index

- **SPEC-1** (`spec/15_external-api-surface.md`): §15.1 finalize row states the response to a finalize call that a terminal writer overtook.
- **CODE-1** (`pkg/gateway/sessionserver/start.go`): finalizingPrecondition, the exit-write guard admitting only `finalizing`.
- **CODE-2** (`pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/session/sessionstore/sessionstore.go`): entry compare-and-swap on created → finalizing.
- **CODE-3** (`pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/start.go`, `pkg/gateway/sessionserver/finalize.go`): guarded ready and failure writes, failFinalizing, afterFailed, and revokeFinalizeLease.
- **DOCS-1** (`docs/api/rest.md`, `docs/api/mcp.md`): finalize reference states the overtaken-call outcome.
- **TEST-1** (`pkg/gateway/sessionserver/finalize_race_internal_test.go`): tier 1 deterministic entry and exit interleavings.
- **TEST-2** (`tests/tier2_component/stores/sessionstore_test.go`): tier 2 guarded mutation serializes on the Postgres row lock.
- **TEST-3** (`tests/tier4_integration/finalize_admission_race_test.go`, and `tests/tier4_integration/eager_claim_lifecycle_test.go` when the dialer is extended in place): tier 4 real-binder entry and exit races.
- **TEST-4** (`tests/tier7a_load_local/finalize_admission_race_test.go`): tier 7a concurrent finalizes and DELETE under `-race`.
