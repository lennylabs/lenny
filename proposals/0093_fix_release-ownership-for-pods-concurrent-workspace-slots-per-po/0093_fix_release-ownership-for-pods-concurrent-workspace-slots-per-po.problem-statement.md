# Problem: Release ownership for pods, per-pod claims, concurrent-workspace slots, and bound credential leases at and after a terminal write

## Statement

This is proposal P1 of `gateway-runtime-comms-remediation.md` §10.2 ("P1. Release ownership for pods, slots, and credential leases"). It runs after proposal 0092, which landed the D6 terminal binding release (`ReleaseTerminalBinding`), `ReleaseUncommitted`, the generation-scoped lease return (`releasePodCredentials`), the post-terminal hook, and the resume driver. The tree was validated at d05c25630 (2026-10-16).

**Problem.** Several gateway paths release a pod, a per-pod SandboxClaim, a concurrent-workspace slot, or a bound or finalize credential lease at or after a session's terminal write, and no single component owns each release. The defects fall into two classes that need different remedies.

- **Releases that hit the wrong target.** The pre-running reclaim (`terminalReclaimPreRunning` through `ReclaimClaimed`), the finalize-failure reclaim, the created sweeper, the failure funnel's non-terminal-edge release, and full_revoke's cancel delete the per-pod claim by pod name (`claim-<podName>`) with no owner precondition and no pool-kind branch. On a concurrent-workspace pool the per-pod claim is shared by every slot on the pod, so one session's pre-running terminal write deletes the claim of every live sibling. The WarmPoolController projects "no claim on a claimed pod" to `draining` and deletes the pod. A superseded coordinator, the `cancel_child` handler, and full_revoke's `terminateLocal` release a local binding or send `Shutdown` with no coordination-generation check. `ShutdownRequest.coordination_generation` already exists and §10.1.1 already requires pods to validate the generation on every gateway-to-pod RPC, but the gateway never sets the field on `Shutdown` and the adapter never reads it there.
- **Releases that never happen.** A pod release on a replica that does not hold the in-memory binding is a no-op (`PodExecutor.Release`). No slot-aware releaser exists for a pre-running slot reservation, so a concurrent-pool finalize whose ready write fails, a pre-running terminal write, and a full_revoke cancel of a pre-running session all leave `active_slots` incremented. A process exit between the terminal commit and the in-process release leaks the slot and the lease. A failed slot release after the unbind is never retried. `Binder.Resume` leaks the exclusive claim `claimPod` created when `reserveResumeSlot` fails.

The spec is silent or contradictory on who owns each release. §4.6.3 and REG-CLAIM list gateway claim deletes only at hold expiry and when an acquisition ends the hold, while §29.4 step 17 has the gateway delete the claim at session end. §7.3 states the generation-scoped lease return but no rule that only the binding replica's `Binder.Release` and `Binder.ReleaseSlot` return leases; that exclusivity clause and the list of callers exist only in 0092's non-spec D6 text. No spec section describes the created sweeper.

**Core members (BUILD-GAPS), kept in this proposal:**

- F-4.6.26 (pre-running reclaim by name; on a concurrent pool it deletes the shared claim and releases no slot).
- F-4.6.27, with new defect N1 folded in. N1: on a concurrent-workspace pool, a finalize whose ready write fails releases no slot reservation (`finalize.go` `prepareAtFinalize` returns nil, `finalizeReadyWriteFailed` reclaims only when prepared, and no terminal path calls `ReleaseSlotReservation`). N1 predates 0092; it is an instance of the missing slot-aware pre-running releaser.
- F-4.6.28 (the created sweeper hard-deletes and reclaims by name with no state predicate).
- F-4.6.29 (a process exit between the terminal commit and the in-process release), for the pod, claim, and slot halves.
- F-4.9.35 (`revokeFinalizeLease` ignores REG-COORDLEASE).
- F-5.2.49 (a failed slot release after the unbind is never retried).
- F-7.3.33 (the failure funnel's non-terminal-edge release races the hook's seal and release).
- F-7.3.34, with new defect N2 folded in. N2: `Binder.Resume` returns without releasing the exclusive claim `claimPod` created when `reserveResumeSlot` fails. Correction to F-7.3.34(a): on the resume path `ReserveSlotOnPod` calls `reserveSlot` with `freshPod=false`, so only the `Counter.Release` rollbacks run there on the attempt context; the claim-delete rollbacks belong to `ClaimSlot`'s fresh-acquisition path.
- F-10.1.21 (a pod release on a non-binding replica is a no-op).
- F-10.1.28 (a superseded coordinator or `cancel_child` releases without a generation check).
- F-11.4.11 (full_revoke's `terminateLocal` sends an unfenced `Shutdown`).
- F-11.4.15, merged with its duplicate F-11.4.16 (a pre-running session cancelled by full_revoke keeps its pod, slot reservation, and finalize lease).
- The slot-return half of F-11.4.10: `RevokeUserLeases` removes lease records and returns no pool slot.

**Spec gaps the core stages.** A releaser for a terminal row that no replica binds (S1); an owner-checked, slot-aware pre-running release (S2, S7); a generation fence on release, seal, and `Shutdown` from a superseded replica (S3), staged as conformance to the existing §10.1.1 rule and proto field for the pod-side half and as a new rule only for Kubernetes claim writes; a durable record or reconciler for a missed release (S4, S6); a releaser for a dead slot on a pod with live siblings (S8); a cancelled-context rule for the slot reservation rollback (S10); an owner for leases with no binding releaser and for the finalize lease (S14, S15); terminal-write treatment of the coordination lease and mirror row (S16), split with R16; when a pre-running start acquires REG-COORDLEASE (S17), citing 0060 D1; contradiction C1 (the claim writer set in §4.6.3 and REG-CLAIM against §29.4 step 17 and §6.2); the lease-before-`Shutdown` half of C2, and only if this proposal restages `Binder.Release` (F-7.1.30 owns the rest of the session-end order); and the exclusivity clause of 0092's D6 single-releaser rule, staged as an amendment to §7.3 **Credential leases on the replacement pod.** and **Exits from `resuming`.** and §10.1.5 **Session with no bound pod.**, which already hold the generation-scoped return.

**Preconditions this proposal depends on and does not fix.**

- **Token Service pool counts are per replica in production (N4, refiled).** The chart default runs the Token Service at 2 replicas (`tokenService.enabled: true`, `replicas: 2`). Each replica builds its own in-memory `credleasestore.New()` and `credassign.Service` with a process-local `active` map. The gateway dials one `grpc.ClientConn` through a ClusterIP Service with no load-balancing configuration. A `RevokeCredentials` call that reaches a replica other than the minter gets `NotFound`, the gateway client treats `NotFound` as a clean release and removes the shared Postgres lease row, and the minter's count is never decremented. A Token Service restart zeroes every count. This contradicts §4.3 ("fully stateless") and §4.9 (concurrent session counts persisted in Postgres in `CredentialPoolStore`). No release-ownership rule can make a lease release decrement the right count while this holds, so the core's lease invariant (no leaked pool count) and any departure that names lease expiry as its bound depend on a separate fix that lands first or alongside.
- **The claim-orphan GC is not wired.** The chart renders `--agent-namespaces` only for the preflight job, so `lenny-controller` registers neither the `ClaimGarbageCollector` nor the `MirrorReconciler`. Branch `fix/controller-agent-namespaces` carries no commit beyond d05c25630. Even once wired, the GC skips any pod with a live session and never touches REG-SLOTCOUNT or a credential lease. It is a bounded fallback only for exclusive-pod claims on pods with no live session. A departure for slots, sibling-occupied pods, or leases needs another bound.

**Members moved out of this proposal, pending owner confirmation.** The validation found these outside the release-ownership boundary under the owner's own drafting rule (one subsystem boundary). Each is recorded with its proposed destination.

- **Credential-leasing accounting (a separate proposal that lands first):** N4 as refiled above, F-4.9.37 (the expiry sweep deletes the row and returns no slot, which is partly a code defect against §7.3, which names expiry as a return path), F-4.9.34 (a failed revoke is dropped with no retry, which diverges the row from the count), and spec gaps S11, S12, and S12b. Deriving `active leases < maxConcurrentSessions` (§4.9) from the shared lease store would close all four together.
- **F-4.9.32 (direct fix):** `Renew` checks only the lease record and pool binding. §4.9 **Renewal loop** step 1 already requires the session-liveness check, so this is a code-to-spec fix with no spec gap.
- **F-4.9.33:** `FallbackTerminator` is never assigned and `RegisterChain` has no production caller. The terminal-writer half goes to P2; chain seeding and persisting `rotationCount` (§4.9 Fallback Flow) go to a BUILD-GAPS fix.
- **F-10.1.30 and F-10.1.32 (coordination fencing, with F-10.1.22 or R16):** an ambiguous `RecordHandoff` error (S5, an extension of the §10.1.2 step 1 zero-row re-read rule) and `reportGuard` comparing state only. A wrong release is their downstream effect.
- **F-5.2.50 (S9):** failed-slot accounting for the §5.2 whole-pod replacement trigger, to the recycling or concurrent-pool health follow-up.
- **The deny-list and session-set halves of F-11.4.10, and C3:** `RevokeUserLeases` deny-lists `lease.CredentialKey()`, which for a pool lease is `{PoolID, CredentialID}` with no user, so one user's full_revoke revokes a shared pool credential for every user. §11.4 step 6 says the user's leases are "revoked (returned to pool)", so this is a code defect against explicit spec text. The session-set question is P5's ("P5 decides when and for which sessions a release runs, and P1 decides how"). Both go to P5 or a §11.4 security proposal; this proposal keeps only "full_revoke's sessions release through the single releaser".

**Settled context.**

- F-4.6.30 (claims stay bound after their sessions are gone) has no single traced cause. The GC that would reclaim them is unregistered in a chart install. One claim comes from `TestSessionContinuesAfterCoordinatingGatewayReplicaLoss`, which is the crash case (F-4.6.29, S1) more than F-10.1.21's live non-binding release. The other leaked claims are unattributed.
- Out of scope; do not design remedies for these: the terminal pipeline (P2), timers and sweeps (P3; the created sweeper's removal borders P3 and needs an explicit boundary), the delegation tree and budget (P4), data lifecycle (P5), R12, R13, R16, R21, R22, the recycling follow-up, the pre-attached seam disposition (draft 0085), and the terminal Sandbox objects nothing deletes.
- P1's full_revoke edits land before P5's, serialized on `cmd/lenny-gateway/user_revocation.go`.

**Drafting rules (owner, plan §10.2, from `scratchpad/terminal-transition/lessons.md`).**

- Open with the member defect list, re-confirmed against the tree, and drop a defect that is gone, with the reason.
- Keep the scope to one subsystem boundary. A defect found outside it goes to BUILD-GAPS or its owning group.
- Write a resource-to-owner table first.
- Add no new per-replica state unless the spec requires it. Prefer state on the session row or in a store with a TTL. An in-memory set must be safe to lose, with a named durable backstop.
- Record a residual gap as a departure with a bounding fallback and a claim-register row, rather than adding a rule.
- State tests as intents with tiers, and make them expansive: table-driven over every writer, path, and state; randomized interleavings under -race (tier 7a); fault injection at each step, including store errors, context cancel, a crash between effects, and replica takeover (tier 8); invariant checkers after every scenario (no leaked pod, slot, claim, lease, or pool count; exactly-once effects); and multi-replica tier-4 and tier-5 scenarios.
- Stage the owner's triage rule for failures the tests find. Fix in this proposal what is relevant to it, or what is straightforward and not owned elsewhere. File everything else, with a deviation entry and a `blocked:` skip.

## Evidence

All entries below were verified against the tree at d05c25630. BUILD-GAPS line evidence for most members was pinned at 319b80091, before 0092's commit 3f943f56f, so its line numbers have drifted; the citations here are current.

- (verified) Plan anchor: `gateway-runtime-comms-remediation.md:2741` (P1 entry, "not yet written") and the P5 entry ("P5 decides when ... P1 decides how").
- (verified) Every member ID exists in `BUILD-GAPS.md` and is OPEN, and each names P1 as its owner. F-4.9.37, F-5.2.50, and F-11.4.16 are absent from the plan's P1 member line; each was filed by 0092 with "Owned by proposal P1".
- (verified) Pre-running reclaim by name: `pkg/gateway/sessionserver/usage.go:462-468` (`isPreRunningClaimState`), `:510-534` (`terminalReclaimPreRunning` skips only on a local binding); `pkg/gateway/podlifecycle/podsession/reclaim.go:79-88` (`ReclaimClaimed` revokes by session ID, then deletes by name); `pkg/gateway/podlifecycle/podclaim/claimer.go:433-444` (`DeleteClaim` with no precondition), `:458-462` (`ClaimName` is per pod).
- (verified) Concurrent pools claim at create and register no binding: `pkg/gateway/sessionserver/pod_claim.go:144-166`; `pkg/gateway/podlifecycle/podsession/slotbinder.go:170-205` (`ClaimSlot` closes the connection and returns).
- (verified) Claim deletion drains the pod: `pkg/controller/warmpool/occupancy.go:76-79`, `:130-141` (no claim on a claimed pod projects to `draining`); `pkg/controller/sandbox/lifecycle/lifecycle.go:99-103` (`draining` yields `ActionDeletePod`).
- (verified) `/start` registers its binding while the row is `ready` and `commitStart` re-validates the locked row: `pkg/gateway/sessionserver/start.go:141-160`.
- (verified) N1: `pkg/gateway/sessionserver/finalize.go:242` (concurrent pool returns nil), `:459-484` (`finalizeReadyWriteFailed`); non-test callers of `ReleaseSlotReservation` are only `slotbinder.go:187,237,696`, `resume.go:444`, `slot_bind.go:407`, and `bind_rollback.go:102`. Pre-0092 origin: `git show 3f943f56f^:pkg/gateway/sessionserver/finalize.go` lines 452-478 reclaim only when prepared, and `afterFailed` in `session_terminal.go` at that revision releases nothing.
- (verified) N2: `pkg/gateway/podlifecycle/podsession/resume.go:183-198`; `claimconnect.go:44-74` (`claimPod` creates an exclusive claim).
- (verified) F-7.3.34(a) correction: `pkg/gateway/podlifecycle/podclaim/slotclaimer.go:642` (`reserveSlot(ctx, sb, claim, req, false)`).
- (verified) F-10.1.21: `pkg/gateway/session/executor/pod.go:269-274` (`Release` returns nil when `unbind` misses).
- (verified) Shutdown fence: `schemas/lenny-adapter.proto` `ShutdownRequest.coordination_generation = 6` with the validation comment; `SealSessionRequest.coordination_generation = 2`; `pkg/gateway/runtime/adapterclient/client.go:981-988` (builder omits the generation, sets `UnconditionalTeardown: true`); `pkg/adapter/session.go:288` (the handler branches on `unconditional_teardown` and `bind_attempt` only); the adapter reads the generation only in `coordination.go` and `sessionseal.go`; `spec/10_gateway-internals.md:30` (**Generation counters:**).
- (verified) full_revoke: `cmd/lenny-gateway/user_revocation.go:112-141` (`terminateLocal` walks `registry.Snapshot()` and sends an unconditional `Shutdown`), `:184-190` (`RevokeUserLeases` deny-lists `CredentialKey()` and removes the record); `pkg/credential/lease.go:296-301` (a pool `CredentialKey` is `{PoolID, CredentialID}`); `spec/11_policy-and-controls.md:270-276` (§11.4 **User Invalidation**, steps 2, 4, and 6).
- (verified) N4 in production: `cmd/lenny-token-service/surfaces.go:66-70` (in-memory lease store and `credassign.New`; the comment calls it "appropriate for the development path"); `charts/lenny/values.yaml:401-402`; `charts/lenny/templates/gateway-deployment.yaml:478-482`; `charts/lenny/templates/token-service-deployment.yaml:171` (a ClusterIP Service); `cmd/lenny-gateway/executor_credentials.go:83` (gateway lease store is Postgres), `:129-149`, `:229` (`grpc.NewClient` with no service config); `pkg/tokenservice/grpc.go:207-209` (`NotFound` when the lease is not in this replica's store); `pkg/gateway/credentials/credassign/client.go:363-377` (`Release` removes the record whatever the revoke outcome), `:380-407` (`NotFound` is a clean outcome); `spec/04_system-components.md:220`, `:1669`.
- (verified) GC wiring: `charts/lenny/templates/preflight-job.yaml:283` is the only chart site rendering `--agent-namespaces`; `cmd/lenny-controller/controllers.go:180-203`; `cmd/lenny-controller/flags.go:152`; `pkg/controller/warmpool/gc.go:222-249` (skips any pod with an active session; no counter or lease handling); `fix/controller-agent-namespaces` points at d05c25630.
- (verified) Slot-count durable source: `pkg/gateway/session/sessionstore/pgstore/pgstore.go:827-851` (`GetActiveSlotsByPod` counts non-terminal rows by `pod_assignment`); `pkg/gateway/storage/slotcounter/slotcounter.go:272,326,397` (reserve, rehydrate, seed), `:465-470` (the `Release` comment relies on rehydration), `:482-489` (`Reset`, no non-test caller); `spec/05_runtime-registry-and-pool-model.md:593`; `spec/10_gateway-internals.md:62`.
- (verified) Credential renewal and sweep: `cmd/lenny-gateway/cred_renewal.go:177-187`; `spec/04_system-components.md:1564` (**Renewal loop** step 1); `cmd/lenny-gateway/credential_lease_sweep.go:81-93` (`DeleteExpired` only).
- (verified) Created sweeper: `pkg/gateway/session/createdsweeper/createdsweeper.go` (filters `created` on the listed snapshot, reclaims, then `store.Delete`); `pkg/gateway/session/sessionstore/pgstore/pgstore.go:772-785` (bare DELETE by id); `pkg/gateway/runtime/watchdog/watchdog.go:55` (`CREATED_TIMEOUT`); no spec section names the sweeper.
- (verified) Coordination and failure funnel: `pkg/gateway/coordination/coordination/coordination.go:610-628` (`RecordHandoff` returns `(0,false)` on any store error); `pkg/gateway/sessionserver/failure.go:289-302` (`reportGuard` compares the generation only in `resuming`).
- (verified) F-4.9.33: `pkg/gateway/llmproxy/llmproxy/fallback.go:132-133`, `handler.go:264-267`; no non-test assignment of `FallbackTerminator` or call to `RegisterChain` under `pkg/` or `cmd/`.
- (verified) Spec contradictions: `spec/04_system-components.md:619` (§4.6.3 SandboxClaim row); `spec/28_communication-channels.md:141` (REG-CLAIM); `spec/29_communication-scenarios.md:769-780` (§29.4 step 17); `spec/07_session-lifecycle.md:443,447` (§7.3 **Exits from `resuming`.** and **Credential leases on the replacement pod.**).
- (verified) Gateway replica count: `charts/lenny/values.yaml:2108-2109` (`gateway.replicas: 2`).
- (unverified) The dossier `/home/ec2-user/lenny/scratchpad/p1-p5/p1-dossier.md` and the live-cluster counts it reports (48 leaked claims, 47 with no sessions row, about 16,000 terminal Sandbox objects). The tree cannot confirm them.

## Who observes it

- **Operators who enable concurrent-workspace pools** (`maxConcurrentSessions > 1`, which requires `acknowledgeProcessLevelIsolation`; the default is 1). A client delete or terminate of a session that has not started, or the watchdog's `CREATED_TIMEOUT` or `READY_TIMEOUT` on an abandoned upload, deletes the shared per-pod claim and the WarmPoolController deletes the pod. Every live sibling session on that pod ends. No race and no second replica is needed. The same operators see `active_slots` leak on finalize failures, pre-running terminal writes, and full_revoke cancels of pre-running sessions, which ends in `WARM_POOL_EXHAUSTED` for later sessions.
- **Every default install.** The chart runs 2 gateway replicas, so the cross-replica cases (the exclusive-pod case of F-4.6.26, F-10.1.28, F-4.9.35, F-11.4.11) are reachable without a contrived topology. Exclusive claims that leak keep their pods out of the warm pool with no backstop while the GC is unregistered.
- **Users who share a pooled credential with a revoked user.** One user's full_revoke deny-lists the shared pool credential, and the propagator fans the revocation out to every replica. Every other user's session on that credential receives `CREDENTIAL_REVOKED` until no unexpired lease of the credential remains. This consequence belongs to the moved-out deny-list half of F-11.4.10.
- **Every default install using credential pools.** With 2 Token Service replicas, releases that reach the wrong replica leave counts incremented, and a Token Service restart zeroes them. Both drift the effective `maxConcurrentSessions` cap. This belongs to the credential-accounting precondition.

## What breaks if nothing changes

- On concurrent-workspace pools, ordinary pre-running terminations kill live sibling sessions, and slot counters climb until the pool refuses new sessions.
- Under the default two-replica gateway, a terminal write on one replica can reclaim a claim while `/start` on another replica holds a live launch, and a superseded coordinator's `Shutdown` can tear down a session another replica now serves. The adapter accepts the `Shutdown` because the gateway sends no generation and the adapter checks none.
- Exclusive claims leaked by a non-binding release or a crash keep pods `claimed` indefinitely, because the claim-orphan GC is not registered. Tier-5 `TestPodLifecycle` and tier-8 `TestOrphanGCReclaimsReservedClaimAfterHolderCrash` keep failing (F-4.6.30).
- Credential pool counts drift in both directions in production, so pools report `CREDENTIAL_POOL_EXHAUSTED` while capacity is free, or admit sessions above `maxConcurrentSessions` after a Token Service restart.
- The spec keeps two disagreeing claim writer sets, and the single-releaser lease rule that 0092's code relies on stays in non-spec text, so later changes have no normative rule to check against.

## Findings this unblocks

- Core: F-4.6.26, F-4.6.27, F-4.6.28, F-4.6.29, F-4.9.35, F-5.2.49, F-7.3.33, F-7.3.34, F-10.1.21, F-10.1.28, F-11.4.11, F-11.4.15, F-11.4.16 (as a duplicate), and the slot-return half of F-11.4.10.
- Partly: F-4.6.30, once the GC chart fix also lands.
- Test gaps the core's test intents can close or cite: T-6.1.5, T-6.2.3, T-10.1.9, T-11.4.1, T-11.4.2, T-11.4.8, and T-24.11.1 in `TEST-GAPS.md`.

## Prior art considered

- **No proposal stages this change.** A grep of `proposals/` for the member IDs returns only 0092's files, and 0092 records each as a gap it hands to P1 (and to P5 for full_revoke) in `0092_*.non-spec-changes.md` and `0092_*.summary.md`.
- **§7.3 and §10.1.5 already hold part of the lease rule.** §7.3 **Credential leases on the replacement pod.** states that a pod release returns the binding's leases plus those at or below its generation, that a stopped attempt returns only its own leases, and that an unconfirmed attempt's leases return through a later pod release or expiry. §7.3 **Exits from `resuming`.** names the attempt as the only gateway component that releases the replacement pod. The proposal amends this text with D6's exclusivity clause and caller set instead of restating the rule.
- **Slot-count rehydration already exists.** §5.2 **Post-recovery rehydration atomicity** and §10.1.4 reset REG-SLOTCOUNT and reseed it from `GetActiveSlotsByPod`, which counts non-terminal session rows. `slotcounter` implements it behind a per-pod `rehydrated` flag, and `Counter.Reset` has no production caller. Clearing the flag when a slot release cannot be confirmed, or running the rebuild periodically with a guard for reservations whose session row is not yet persisted (the precedent is the §11.2 reservation-aware rebuild), bounds the slot leaks in F-4.6.29, F-5.2.49, N1, F-11.4.15, and S8 from session-row state. This is the preferred backstop over a new pending-release record for slots.
- **The watchdog already covers the created sweeper's purpose.** The pre-running watchdog writes `created → failed` with `CREATED_TIMEOUT` through the guarded write and the hook, using the same `maxCreatedStateTimeoutSeconds`. Removing the sweeper's reclaim and hard delete applies existing spec and needs no new section. F-4.6.28's own suggested resolution says the same.
- **The pod-side generation fence already exists in spec and proto.** §10.1.1 **Generation counters:** and `ShutdownRequest.coordination_generation` require the fence; the gaps are in the gateway builder and the adapter handler. This half of S3 is conformance work in the F-10.1.22 family. §10.1.5 already defines replica behavior after a generation-stale rejection.
- **§10.1.2 step 1** already requires a re-read after a zero-row compare-and-swap; F-10.1.30 extends it to an error of unknown outcome.
- **R16** owns generation validation on the operational RPCs, the UNWIRED fence fields on `RevokeCredentials` and `ExtendCredentialLease`, and the ABSENT claim-register row for lease or mirror release on gateway drain. S3 and S16 need an explicit split with R16.
- **Proposal 0060** ratified D1, "acquire the lease at bind on the binding replica", with no spec edit. S17 cites it.
- **Draft proposal 0085** (status Draft, not converged) covers the pre-attached seam disposition at `/finalize`, the `/start` slot reservation, and delegated-child materialization. It stages no owner-checked or slot-aware reclaim, so there is no duplication, but this proposal's pre-running reclaim must stay consistent with 0085's seam decision.
- **Branch `fix/controller-agent-namespaces`** is the GC chart fix. It has no commits beyond d05c25630. 0079's final verify traced the never-passing tier-8 orphan-GC tests to a controller started without `--agent-namespaces`.

## Validated premises

Each lens ran independently. The verdicts below record what each found and how the consolidation resolved disagreements.

**Premise lens (verdict: revise).**

- Refuted, and the premise the credential half rests on: "N4 affects dev mode only." The production chart runs 2 Token Service replicas, each with in-memory leases and counts, and a release reaching the wrong replica gets `NotFound`, which the gateway treats as a clean release. Verified; N4 is refiled as a production precondition.
- Refuted in part: "the GC fix lands ahead, so a departure may rely on the GC as a bounded fallback." The fix is not in the tree, and the GC bounds only exclusive-pod claims on pods with no live session. Verified.
- Confirmed: the F-4.6.26 mechanism, including the shared-claim delete on concurrent pools.
- Confirmed with a correction: the credential-release premises hold, and §7.3 already names expiry as a return path, so F-4.9.37 is partly a code defect against §7.3.
- Confirmed: full_revoke's security removal is owner-unaware, and a pre-running session leaks under it.
- Confirmed: N2, and `claimPod` creates an exclusive claim.
- Correction confirmed: F-7.3.34(a) involves only `Counter.Release` rollbacks on the resume path.
- Confirmed with a corrected origin: N1's slot leak predates 0092. The premise lens said the pre-0092 hook ran `ReclaimClaimed`; the consolidation found that `afterFailed` at `3f943f56f^` released nothing. Both readings agree the slot reservation leaked before 0092.
- Confirmed: spec gaps C1 and S17.
- Refuted in part: "0092's single-releaser rule exists only in non-spec text." §7.3 holds the generation-scoped return; only the exclusivity clause and caller set are D6-only.
- Confirmed: F-4.6.28, F-10.1.21, and F-10.1.30.
- Confirmed: F-4.9.33 sits outside release ownership.

**Evidence lens (verdict: revise).**

- Confirmed: the plan anchor and the member list. The added members F-4.9.37, F-5.2.50, and F-11.4.16 are sourced; F-11.4.16 duplicates F-11.4.15.
- Confirmed: BUILD-GAPS line evidence drifted after 0092; the defect content still matches.
- Refuted origin, confirmed substance: N1 is real and predates 0092. The original statement's "introduced by 0092's `podReclaimed`" is withdrawn.
- Confirmed: N2. The lens's caveat stands: a concurrent start can fill the pod between the claim CREATE and the reservation, so a by-name delete of that claim is not obviously safe.
- Confirmed: the GC is unregistered in a chart install.
- Refuted as stated: "The leaks behind F-4.6.30 come from F-10.1.21." The tree supports one test, and that case is the crash case of F-4.6.29 and S1. The remaining leaked claims are unattributed.
- Superseded: the lens's confirmation of N4 as dev-only. It checked the in-process path only; the premise and alternatives lenses showed the Token Service path holds the same per-replica state in production, and the consolidation verified that.
- Confirmed: C1, C2 (F-7.1.30), and C3. The §11.4 heading is "User Invalidation".
- Superseded in part: "the single-releaser rule exists only in D6." The prior-art and premise lenses showed §7.3 holds part of it.
- Confirmed: every 0092 deliverable the statement names exists, and each cited code path exists.

**Prior-art lens (verdict: revise).**

- Confirmed: no proposal stages this change, and nobody else owns it.
- Confirmed: §7.3 and §10.1.5 already hold part of the lease rule.
- Confirmed, and a premise the slot design rests on: the slot-count rehydration from `GetActiveSlotsByPod` exists and is the natural backstop for slot leaks.
- Confirmed: the watchdog's `CREATED_TIMEOUT` path answers the created sweeper, and sweep ownership borders P3.
- Refuted in part: "`ShutdownRequest` and the seal RPCs carry no `coordination_generation` field." Both carry it (`ShutdownRequest` field 6, `SealSessionRequest` field 2). The R16 split the lens asks for still holds.
- Confirmed: F-10.1.30 extends §10.1.2 step 1.
- Confirmed: 0085 overlaps on the pre-running seams without duplicating.
- Confirmed: 0060 D1 is prior art for S17.
- Confirmed: F-4.9.33 is a wiring gap closer to P2.
- Confirmed: the GC chart fix has not landed.
- Confirmed: the related open TEST-GAPS findings.

**Scope lens (verdict: revise).**

- Confirmed: one coherent problem sits at the center (several releasers, by-name release), and the core members listed under the statement belong together.
- Confirmed, and a premise the scope rests on: credential-leasing accounting (F-4.9.32, F-4.9.34, F-4.9.37, N4, S11, S12, S12b) is a separate problem that lands first, because the core's lease-expiry fallback depends on it.
- Confirmed: F-4.9.33 leaves P1.
- Confirmed: F-5.2.50 leaves P1.
- Confirmed: F-10.1.30 and F-10.1.32 leave P1; F-10.1.28 stays.
- Confirmed: F-11.4.10 splits; the slot-return half stays, and the deny-list granularity, session-set halves, and C3 go to P5 or a §11.4 security proposal.
- Confirmed: the statement contradicted itself on C2; it now takes only the conditional lease-before-`Shutdown` half.
- Confirmed: the original scope broke the one-boundary drafting rule and risked repeating 0092's unbounded growth (15,844 lines across 0092's files).

**Impact lens (verdict: revise).**

- Confirmed, and the most severe consequence: on a concurrent-workspace pool, an ordinary pre-running termination kills every live sibling session on the pod, with no race. The consolidation verified each link of the chain.
- Confirmed: concurrent-workspace pools are opt-in and documented.
- Confirmed: the default chart runs 2 gateway replicas.
- Confirmed: full_revoke's pool-credential deny-list affects other users, as a code defect against §11.4 step 6.
- Confirmed: leaks are unbounded today because the GC is unregistered, and slot and lease leaks stay unbounded after the chart fix.
- Confirmed with a correction: credential counts reset on a Token Service restart. F-4.9.32 is a code-to-spec defect.
- Refuted: "N4 affects only dev and in-process mode, with low impact." The lens relied on the original statement; N4 affects production.

**Alternatives lens (verdict: revise).**

- Confirmed, and a premise the credential half rests on: N4 is a production leak and violates §4.3.
- Confirmed as a design direction: deriving pool counts from the durable lease rows resolves F-4.9.37, N4, F-4.9.34's divergence, and the restart reset together. Recorded for the credential-accounting proposal.
- Confirmed as a design direction: the defects split into wrong-target releases (inline fences) and missed releases (reconciliation from durable state). The slot half is adopted as the preferred backstop; whether reconciliation retires S4 and S6 for leaks is a drafting decision.
- Confirmed, and a premise the fence design rests on: S3 overstates the pod-side gap; the spec rule and proto field exist, and only the Kubernetes claim writes lack a fence in spec.
- Confirmed: F-4.9.32, F-4.9.33, F-5.2.50, F-10.1.30, and F-10.1.32 leave the release-ownership boundary.
- Confirmed: removing the created sweeper's reclaim and hard delete is the smallest fix for F-4.6.28.
- Confirmed with a qualification: the exclusive-pod harm of F-4.6.26 is smaller than first stated, because `commitStart` re-validates the locked row and refuses after a terminal write. A UID or resourceVersion precondition (as `DeleteOnHoldExpiry` uses) and a pool-kind branch address the remaining harms. Whether S17 is still needed is a drafting decision.
- Confirmed: the GC fix has not landed and precedes any departure that names the GC.
