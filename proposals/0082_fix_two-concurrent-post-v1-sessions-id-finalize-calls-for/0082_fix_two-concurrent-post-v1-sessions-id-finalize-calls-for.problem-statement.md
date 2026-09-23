# Problem: Concurrent finalize calls both prepare the session's pod

## Statement

Two concurrent POST /v1/sessions/{id}/finalize calls for the same session can both pass the `created` precondition. Both then run the prepare phase against the pod claimed at /create, and the losing call can corrupt or end the winning call's session. §15.1 admits /finalize only from `created` and requires a call in any other state to return `409 INVALID_STATE_TRANSITION`. The shipped behaviour is therefore a code defect against the existing spec.

1. The precondition is checked against a row read before any lock. handleFinalize (pkg/gateway/sessionserver/sessionserver.go) reads the row with s.store.Get and validates EndpointFinalize against that row's state (pkg/api/v1/session/session.go admits only StateCreated). It then calls s.store.Update with a mutation that runs transitionFinalizing. That function sets State to `finalizing` without checking the current state, and the mutation always returns nil. The store's Update contract states that the store does not validate the transition (pkg/gateway/session/sessionstore/sessionstore.go). The Postgres Update holds the row under SELECT ... FOR UPDATE, which serializes the writes, but nothing inside the mutation re-checks the state. Two calls that both read `created` before either commits both write `finalizing` and both proceed. No per-session mutex or other serialization exists in handleFinalize or its route registration.

2. Reachability is limited to calls that the §11.5 idempotency middleware does not collapse. The middleware covers /v1/sessions/{id}/finalize with a Postgres-backed claim, so two calls that carry the same Idempotency-Key execute once across replicas. A call with no key passes straight through to the handler, and two calls with different keys both execute. The window spans the Get, resolveFinalizePlan, and the first Update. The Go SDK retries only after an error or a retryable status and waits at least 200 ms, so its automatic retry normally arrives after the first call has committed `finalizing`. The realistic triggers are keyless duplicate submissions, such as a double submission from a user interface, two orchestration workers acting on one session, a proxy that retries upstream on a connection reset, or client request hedging.

3. Both admitted calls run the prepare phase. For an exclusive pool, prepareAtFinalize (pkg/gateway/sessionserver/finalize.go) calls s.podBinder.Prepare with SandboxName set to the row's PodAssignment. Binder.Prepare (pkg/gateway/podlifecycle/podsession/binder.go) reconnects on every call, so each call dials and negotiates its own adapter connection. Both calls stream PrepareWorkspace into the same session's staging tree, where each staging file is opened with O_CREATE|O_WRONLY|O_TRUNC (pkg/adapter/staging.go), so concurrent streams for one upload reference can truncate or interleave each other. Both calls then run FinalizeWorkspace, RunSetup, and credential assignment on the same pod. For a pool with maxConcurrentSessions above 1 or a service-mode pool, prepareAtFinalize returns before any pod RPC, so these pod-side collisions are limited to exclusive pools. The row-state collisions in point 4 apply to every pool.

4. The losing call writes the row and the pod without checking what the winner has done. Every state write in handleFinalize is unconditional. transitionReady writes `ready`, and failSession (pkg/gateway/sessionserver/start.go) writes `failed` whatever the row holds. A loser whose `finalizing` write lands after the winner's `ready` or `running` commit regresses the row to `finalizing`, which blocks the winner's /start or rewinds a running session. The loser's `finalizing` write also replaces the stored WorkspacePlan when its request carries a body. On any Prepare or pre-Prepare failure the loser drains the shared pod, through the binder's failPhase, through ReclaimClaimed after a reconnect failure, or through reclaimFinalizedPod in the handler, and then marks the row `failed`.

5. On an SDK-warm pod the loser's DemoteSDK can tear down the winner's pre-connected runtime. Binder.Prepare sends DemoteSDK before staging when PreConnect is set and a plan path matches sdkWarmBlockingPaths. The adapter's DemoteSDK handler (pkg/adapter/sdkwarm.go) ignores its request, checks only that the runtime's static type is SDK-warm, closes whatever session the runtime has bound (pkg/adapter/embedded_sdkwarm.go), and releases whichever entry anyRegisteredSession returns. A second DemoteSDK is therefore not a no-op. The demotion does not require the two calls to carry different plans: two no-body calls against a stored plan that matches a blocking path both demote. The most reachable interleaving is a loser DemoteSDK that lands while the winner is still in Prepare, or between the winner's `ready` write and its /start. The winner's PrepareResult then reports that no demotion happened, and the winner's Launch at /start sends ConfigureWorkspace to an SDK that is no longer connected. Ending a session that has already launched is the least reachable form, because the launch runs in the client's separate /start call.

6. Proposal 0081 (status Draft) does not stage a fix. It records this defect among the defects in the shipped tree and assigns the compare-and-swap on the transition into `finalizing` to a separate proposal. 0081 does interact with this problem: its staged design mints a per-attempt token in Binder.Prepare and adds a reclaim short-circuit whose stated purpose is that the loser's refusal in two concurrent finalize calls does not drain the winner's pod. Once 0081 lands, the loser-drains-winner's-pod collision on the Prepare-refusal path is partly addressed. The row-state overwrites, the duplicate staging and setup, and the unfenced DemoteSDK remain.

The expected remedy, which the proposal must validate rather than assume, is a compare-and-swap on created → finalizing inside the existing Update mutation. The mutation re-runs session.Validate for EndpointFinalize against the locked row and returns the resulting precondition error, and the handler maps that error through writePreconditionError to `409 INVALID_STATE_TRANSITION`. Both pgstore and memstore return a mutate error before writing, and sessionserver already uses this idiom (resumeHeldPod in start.go, errReportConflict in failure.go). The loser is refused before any pod RPC, which closes every collision in points 3, 4, and 5. The remedy needs no new column, lock, migration, or error code. It needs no new spec mechanism. At most the proposal adds a clarifying sentence in §15.1 or §7.1 that the created → finalizing transition is atomic and a concurrent loser receives the existing precondition error.

The proposal decides explicitly whether the exit writes are in scope. The finalizing → ready write and failSession are unconditional, and DELETE admits every non-terminal state including `finalizing`. A DELETE that commits `cancelled` while the prepare phase runs is later overwritten by `ready` or `failed`. Making those writes conditional on the row still being `finalizing` uses the same mechanism in the same function.

Out of scope, each as its own finding:

- Fencing DemoteSDK with a session identifier or a bind-attempt token. This changes the adapter protocol and the spec, addresses the separate fault that DemoteSDK releases whichever registry entry exists, and is unreachable from a concurrent /finalize once the entry compare-and-swap lands. 0081 leaves its forward-RPC fencing gap open, and the fencing belongs with that gap.
- Serializing concurrent POST /v1/sessions/{id}/start. 0081 records that /start cannot use a state compare-and-swap, because the state machine has no starting → ready edge and §7.2 does not expose pre-attached failures as state transitions, and that /start instead needs a bounded holder-and-expiry claim on the row. The created → finalizing compare-and-swap uses an existing declared edge and does not close /start.
- The generic read, validate, and unconditional-write pattern in handleTransition (which serves terminate) and handleDelete. Those handlers make no pod RPC after their write, so their failure mode is a lost or overwritten state transition. A later proposal can reuse the mutation-level precondition helper this proposal introduces.
- Changing 0081's staged design, and editing any implemented proposal.

## Evidence

- verified: pkg/gateway/sessionserver/sessionserver.go, handleFinalize (Get, then session.Validate for EndpointFinalize, then Update with transitionFinalizing whose mutation returns nil), transitionFinalizing and transitionReady (unconditional state assignment), and the unconditional finalizing → ready Update.
- verified: pkg/gateway/sessionserver/sessionserver.go, writePreconditionError (maps *session.PreconditionError through errors.As).
- verified: pkg/gateway/sessionserver/start.go, failSession (sets StateFailed unconditionally), podBinder.Launch in the /start path, and resumeHeldPod with errResumeHeldPodNotSuspended (state check inside an Update mutation).
- verified: pkg/gateway/sessionserver/finalize.go, prepareAtFinalize (early nil returns for service mode and maxConcurrentSessions above 1; podBinder.Prepare with SandboxName from PodAssignment) and reclaimFinalizedPod.
- verified: pkg/gateway/session/sessionstore/sessionstore.go, the Update contract ("The store does NOT validate the transition").
- verified: pkg/gateway/session/sessionstore/pgstore/pgstore.go, Update under SELECT ... FOR UPDATE, returning the mutate error before the write; pkg/gateway/session/sessionstore/memstore/memstore.go, Update under the store mutex.
- verified: pkg/api/v1/session/session.go, EndpointFinalize admits only StateCreated; EndpointDelete admits every non-terminal state.
- verified: pkg/gateway/podlifecycle/podsession/binder.go, Binder.Prepare (reconnect per call, ReclaimClaimed on reconnect failure, DemoteSDK sent before staging when a blocking path matches, failPhase on later failures).
- verified: pkg/adapter/sdkwarm.go, Server.DemoteSDK (ignores the request, type-only check, releases anyRegisteredSession) and anyRegisteredSession; pkg/adapter/embedded_sdkwarm.go, SDKWarmInProcessRuntime.DemoteSDK (closes the bound session).
- verified: pkg/adapter/staging.go, staging files opened with O_CREATE|O_WRONLY|O_TRUNC in the session's staging tree.
- verified: cmd/lenny-gateway/httpsurface.go, /v1/sessions/{id}/finalize on the idempotency allow-list with a Postgres-backed store; pkg/gateway/middleware/idempotency/idempotency.go, a request with no key passes through to the inner handler.
- verified: spec/15_external-api-surface.md §15.1, the precondition table preamble (invalid state returns 409 INVALID_STATE_TRANSITION) and the finalize row (`created`; `finalizing` → `ready`).
- verified: spec/07_session-lifecycle.md §7.1, steps 11 through 14 (FinalizeWorkspace through StartSession).
- verified: spec/04_system-components.md §4.7, the ConfigureWorkspace and DemoteSDK rows; spec/06_warm-pod-model.md §6.1, demotion.
- verified: spec/11_policy-and-controls.md §11.5, FinalizeWorkspace on the idempotency-key list with an optional key.
- verified: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry, status Draft; its summary records the concurrent /finalize defect and assigns the compare-and-swap to a separate proposal, records the Prepare token and reclaim short-circuit, and records the /start serialization rows. The concurrent /finalize defect row is present only in the uncommitted working tree of 0081.
- refuted: spec/04_system-components.md §4.3 "preparation barrier". §4.3 is Token Service, and the phrase "preparation barrier" does not appear under spec/. The code comments in handleFinalize cite "§4.3 preparation barrier" and "§4.3 (proposal)", which is stale code-side citation drift. The spec basis for the finalize barrier is §7.1 steps 11 through 13 and the §15.1 finalize row.

## Who observes it

The problem is observed by a client that submits /finalize for one session more than once without an Idempotency-Key, or with different keys, while the first call is still between its row read and its first write. The realistic sources are a double submission from a user interface, two orchestration workers acting on one session, a proxy that retries upstream on a connection reset, and client request hedging. Only a caller that passes the manage() permission check for the session can reach the handler, so the harm falls on the session's own owner or tenant. Neither the spec nor the SDKs tell clients to serialize /finalize, and the SDKs do not generate an idempotency key, so a client has no documented way to know it must avoid concurrent calls. The Go SDK's automatic retry does not normally reach the window, because it waits at least 200 ms and retries only after an error or a retryable status.

## What breaks if nothing changes

- On an exclusive pool, concurrent PrepareWorkspace streams truncate or interleave the session's staged uploads, and FinalizeWorkspace and setup commands run twice on one pod, which can leave a corrupted workspace.
- Any failure in the losing call drains the pod the winning call is using and marks the row `failed`, which kills a session the winner prepared successfully.
- The loser's unconditional writes overwrite the winner's state: a late `finalizing` write blocks the winner's /start or regresses a running row, a late `ready` or `failed` write overwrites a later state, and a loser request body replaces the stored WorkspacePlan.
- On an SDK-warm pod, the loser's DemoteSDK tears down the pre-connected SDK and releases the winner's registry entry, so the winner's bind fails or its Launch configures a disconnected SDK.
- No pod leaks, because every failure path reclaims the pod. The cost is lost work, a corrupted workspace, and terminal states the client cannot explain.

## Findings this unblocks

**BUILD-GAPS finding ids: none.** No BUILD-GAPS.md finding references this problem.

## Prior art considered

- Proposal 0081 (Draft) records this defect as unstaged and assigns the compare-and-swap remedy to a separate proposal. Its per-attempt Prepare token and reclaim short-circuit partly address the loser-drains-winner's-pod collision once it lands. 0082's impacts row on 0081 must account for that interaction.
- No other landed or draft proposal addresses the concurrent /finalize race. A search of proposals/ returned only 0081 and unrelated matches in 0018 and 0038.
- The §11.5 idempotency middleware covers /finalize but deduplicates only same-key requests, and the key is optional.
- The §10.1 per-session coordination lease is acquired in registerBinding at /start and is never acquired in finalize.go. Proposal 0060 co-locates the lease with the pod binding at bind and start and does not cover finalize. The §10.1 coordination_generation compare-and-swap covers replica takeover and does not guard the created → finalizing edge.
- sessionserver already implements a state check inside an Update mutation that returns a sentinel error: resumeHeldPod with errResumeHeldPodNotSuspended in start.go, and errReportConflict with legalReportTransition in failure.go. The remedy reuses that idiom.
- BUILD-GAPS.md and TEST-GAPS.md carry no open finding for this race. The closest finding, F-7.4.16, is closed and concerned an upload racing finalize. The tier-8 concurrency tests do not exercise /finalize.
- handleStart has the same unguarded read-then-write sequence, but it is partly fenced by the coordination-lease acquire in registerBinding, and 0081 records that it needs a different mechanism.

## Validated premises

Six lenses validated this problem independently.

### Premise lens — revise

- Confirmed (load-bearing): the precondition is checked against a row read before any lock, the Update mutation writes `finalizing` without checking the current state, and the store validates no transition. The spec admits /finalize only from `created`, so the fix is code conformance.
- Confirmed: the §11.5 middleware collapses same-key duplicates, so the race is reachable only by keyless or different-key calls. This narrows who observes the problem without refuting it.
- Confirmed: both calls run Binder.Prepare on their own adapter connection for an exclusive pool. For maxConcurrentSessions above 1 or service mode, no pod RPC runs at finalize.
- Confirmed: DemoteSDK is gated only on the runtime's static type, closes whatever session is bound, and releases the registry's single entry. The harm does not require the calls to carry different plans.
- Refuted (load-bearing, timing): the original claim that the loser's DemoteSDK ends a launched session after "ordinary latency", with setup commands widening the window. Launch runs in the client's separate /start, which requires `ready`, so the loser would have to lag the winner's whole Prepare, its ready write, and the client's /start round trip. Longer winner setup commands delay the winner's launch and narrow that window. The likelier effect is a corrupted or failed winner bind while the winner is in Prepare or before its /start.
- Confirmed with addition: the loser's finalizing, ready, and failed writes are unconditional, and reclaimFinalizedPod drains the shared pod. The addition is that a late finalizing write regresses a winner at `ready` or `running`.
- Revised: "0081 does not change it" is partly inaccurate. 0081's Prepare token and reclaim short-circuit address the loser-drains-winner's-pod collision on the Prepare-refusal path once 0081 lands.

### Evidence lens — revise

- Verified: handleFinalize, transitionFinalizing, the StateCreated-only precondition, and the absence of serialization in the handler and route.
- Verified: the store contract, pgstore Update under SELECT ... FOR UPDATE, and memstore Update under its mutex, both returning the mutate error. A compare-and-swap inside the mutation is mechanically available.
- Verified: prepareAtFinalize and Binder.Prepare, including the DemoteSDK send before staging.
- Verified: the DemoteSDK handler ignores its request and checks no session or token.
- Drifted: the timing argument, as refuted under the premise lens. The nearer harms are a loser demotion during the winner's Prepare and a loser failure that drains the shared pod.
- Verified with addition: transitionReady and failSession are unconditional, and the loser's finalizing write also replaces WorkspacePlan when its request has a body.
- Verified with caveat: 0081's concurrent /finalize defect row exists only in the uncommitted working tree. The /start remedy in 0081 is a holder-and-expiry claim and shares no mechanism with the finalize compare-and-swap.
- Refuted: the citation "spec/04_system-components.md §4.3 preparation barrier". §4.3 is Token Service and the phrase does not appear in the spec.

### Prior-art lens — stands

- Confirmed: only 0081 addresses the race, as an unstaged defect. The shipped code still has it, and transitionFinalizing is the only writer of StateFinalizing in pkg/.
- Confirmed: the store row lock is a working base for a compare-and-swap but does nothing unless the mutation checks the state.
- Confirmed: sessionserver already uses the state-check-inside-mutation idiom in resumeHeldPod and the failure-report path.
- Confirmed: neither §11.5 idempotency nor the §10.1 coordination lease serializes /finalize, and proposal 0060 does not extend the lease to finalize.
- Confirmed: the spec defines no concurrency rule for /finalize beyond the precondition table, so the fix needs at most a clarifying sentence.
- Confirmed: no open BUILD-GAPS or TEST-GAPS finding covers the race.
- Confirmed: /start has the same pattern but is partly fenced and needs different work.

### Scope lens — revise

- Confirmed (load-bearing): the defect is one check-then-act gap at the entry edge, and points 3, 4, and 5 are its consequences. A compare-and-swap at the entry edge closes all of them.
- Confirmed: DemoteSDK fencing is a separate fault on a separate contract surface and is declared out of scope.
- Added: the exit writes (finalizing → ready and failSession) are unconditional, so a DELETE that commits `cancelled` during prepare is overwritten. The entry compare-and-swap does not close this. The proposal must decide it explicitly.
- Confirmed: the generic pattern in handleTransition and handleDelete is a separate, wider problem and stays out.
- Confirmed: /start stays out, because the same change does not close it.

### Impact lens — revise

- Confirmed: the race is reachable in the shipped tree through the sole REST entry point.
- Confirmed: the window is narrow, the Go SDK's automatic retry does not normally reach it, and the realistic triggers are keyless duplicate submissions.
- Confirmed: the SDK-warm post-launch scenario is the least reachable consequence, although the handler behaviour it relies on is correct as described.
- Confirmed (load-bearing): the consequences that reach exclusive pools of every warm type, and the row-state overwrites that reach every pool, are more likely and more severe, including staged-upload corruption through O_TRUNC, duplicate setup, and pod drain by a failing loser.
- Confirmed: the damage stays within one session and leaks no pod, and no spec or SDK guidance tells clients to serialize /finalize.

### Alternatives lens — stands

- Confirmed: no reading makes this a non-problem.
- Confirmed (load-bearing): §15.1 already requires 409 INVALID_STATE_TRANSITION for a call in an invalid state, so the smallest defensible fix is code-only conformance.
- Confirmed (load-bearing): the compare-and-swap uses existing primitives (the locked Update, session.Validate, and writePreconditionError) and needs no new column, lock, migration, or error code.
- Confirmed: the compare-and-swap removes the loser's later writes and reclaims, because a refused loser returns before any of them.
- Confirmed: DemoteSDK fencing is not owed here and belongs with 0081's forward-RPC fencing gap.
- Confirmed: the /finalize fix does not close /start, which needs a bounded claim.
- Confirmed: the broader read-validate-write pattern is a follow-up finding, and this proposal names it without fixing it.
