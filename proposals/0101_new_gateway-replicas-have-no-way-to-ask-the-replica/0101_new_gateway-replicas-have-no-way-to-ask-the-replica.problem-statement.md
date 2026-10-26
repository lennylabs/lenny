# Problem: Gateway replicas have no inter-replica request-forwarding channel and no replica address

## Statement

The §28.3 link register declares `LNK-INTERREPLICA` between a forwarding gateway replica and a session's coordinating replica, dialled by the forwarding replica over gRPC. Its endpoint cell names only the internal gRPC `ForwardMessage` RPC and says the specification does not state the address. §28.3 places no channel row on the link, and records the cross-replica message routing as an `ABSENT` claim-register row. §28.5.4 describes `ForwardMessage` as the cross-replica message routing conversation, carries no card, and states no address. §29.3 assigns its message-send rows to `ForwardMessage` on `LNK-INTERREPLICA`. For the interrupt, terminate, delete, held-pod resume, interaction-resolution, and upload rows, §29.3 requires a forward to the coordinator and states explicitly that the specification names no carrier.

In code, no forward exists for any kind. On the default chart topology of two gateway replicas, where sticky routing is only an optimization, an off-holder message to a `running` session is answered HTTP 500 `EXECUTOR_FAILURE` and is neither delivered nor held (F-7.2.38). An off-holder message to an `input_required` session is answered `queued` and buffered in the serving replica's own in-memory inbox, which the coordinator does not read under `durableInbox: false` (F-29.3.2). An off-holder `POST /resume` of a held-pod `suspended` session fails closed with 409 `TARGET_NOT_READY` (F-29.3.1). Off-holder terminate and delete write the terminal state locally, which proposal 0092 accepted as a departure mitigated by a holder-side release (F-10.1.33). A drained session bound on another replica keeps its pod after user erasure deletes its row, until orphan GC drains it (F-12.8.43).

The §29.3 lead paragraph and its `running` and `input_required` rows still state that an unreachable coordinator makes the serving replica buffer the message in its inbox and answer `queued`. Proposal 0092 left those rows unchanged and recorded the fallback as a defect it does not stage. §7.2 delivery path 6 governs only `suspended` sessions, so moving the `running` and `input_required` fallback to DLQ custody is a new decision this proposal makes and justifies. It is not a rewording of an existing path-6 rule.

The proposal must:

- Widen the existing `LNK-INTERREPLICA` link rather than declare a new one. It adds the §28.3 channel row or rows and the §28.5.4 card for a request-forwarding conversation between gateway replicas, named under the channel naming law, and states whether `ForwardMessage` is replaced, renamed, or subsumed.
- Define a dialable replica address. `resolveReplicaID` returns `LENNY_REPLICA_ID` or the hostname with a random 4-byte suffix regenerated on every process start, which is a coordination identity rather than a network address.
- Name `REG-COORDLEASE`, the Redis compare-and-set holder with a 60-second expiry, as the authority for the forward target. `REG-COORDMIRROR` is a Postgres projection whose only writer is the Sweeper, and revert `54292da1b` removed its `coordinator_address` column. If the proposal resolves through the mirror instead, it states the staleness rule. It also states how a coordinator rejects a forward that reached it after the binding moved, through a lease or generation check.
- State the request kinds that need a synchronous holder-side effect: interrupt, mid-session upload, terminate and delete (whose seal runs only on the holder), and message send to a `running` or `input_required` session. For each kind it states the idempotency and the behaviour when the coordinator is unreachable or the binding moved. That includes the message kind's fallback, moved from the serving replica's inbox to a store the coordinator reads.
- Choose for the held-pod resume between a forward on the new channel and a durable resume request that §7.2 **Backlog resume.** picks up, and give reasons.
- Restate the §29.3 interaction-resolution rows as store-and-wake (R10). Those routes already work across replicas through the Postgres interaction store, so interaction resolution leaves the forwarded set.
- Specify the cross-replica release request that R21 carries (owner decision of 2026-10-21) for F-12.8.43. The decision starts from the existing §11.4 full-revoke release propagator on Redis pub/sub (`lenny:session:terminate`, at-most-once, Sweeper as backstop). The proposal either extends that propagator to the drain and erasure release or states why the gRPC channel replaces it. Either way it registers the inter-replica pub/sub fan-out in §28.3, which no register lists today.
- State the gRPC-versus-propagator rationale for each kind, the replica-to-replica authentication and authorization (mTLS identity and the NetworkPolicy self-peer R18 adds), and the security surface of the new port.
- Sequence against draft proposal 0100 (P2a terminal pipeline), which defers F-10.1.33 and the cross-replica half of F-12.8.43 to this proposal and stages a release path of its own.
- Record which plan step (R18, R19, or R21) implements each staged contract, and add claim-register rows for contracts not yet true in code.

## Evidence

- (verified) spec/28_communication-channels.md §28.3 link register, `LNK-INTERREPLICA` row: endpoint "The internal gRPC `ForwardMessage` RPC, whose address the specification does not state". The paragraph after it says the link carries no channel row and the routing is an `ABSENT` claim-register row.
- (verified) spec/28_communication-channels.md §28.3 register-entry register: `REG-COORDLEASE` (Redis, compare-and-set, 60-second expiry, gateway replicas write) and `REG-COORDMIRROR` (Postgres `coordination_lease`, writer "Gateway sweeper", "a projection rather than an exclusion primitive").
- (verified) spec/28_communication-channels.md §28.5.4 Inter-replica: no card, no address, `ForwardMessage` named as the cross-replica message routing conversation, coordinator identified "through the coordination lease".
- (verified) spec/29_communication-scenarios.md §29.3, paragraph "Two requirements govern the rows whose required outcome is a forward to the coordinator": message-send rows ride `LNK-INTERREPLICA`, and the interrupt, terminate, delete, resume, interaction-resolution, and upload rows have no named carrier. The same paragraph and the `running` and `input_required` rows of the off-holder matrix state the inbox fallback.
- (verified) spec/07_session-lifecycle.md §7.2 delivery path 6 ("Target session is `suspended`"), **Held messages.** and **Backlog resume.**: path 6 covers only `suspended`, and a replica does not forward a message for a `suspended` session.
- (verified) spec/11_policy-and-controls.md §11.4 full-revoke note: "the gateway publishes the step-2 release request and the token revocations on Redis pub/sub channels".
- (verified) cmd/lenny-gateway/coordination_identity.go `resolveReplicaID`: `LENNY_REPLICA_ID` override, or hostname plus a random 4-byte hex suffix.
- (verified) pkg/gateway/podlifecycle/podterminate/propagator/propagator.go: `Channel = "lenny:session:terminate"`, the cross-replica §11.4 step-2 binding-release payload.
- (verified) pkg/gateway/sessionserver/toolapproval.go: the Postgres shared interaction store poll is the cross-replica wake for an approval posted to another replica. tests/tier7a_load_local/toolapproval_xreplica/xreplica_wake_test.go exists.
- (verified) pkg/gateway/sessionserver/messages.go: comment listing the §7.2 inter-replica `ForwardMessage` gRPC and cross-replica coordinator routing as elided.
- (verified) pkg/gateway/sessionserver/resume.go: off-holder held-pod resume answers 409 `TARGET_NOT_READY`.
- (verified) charts/lenny/values.yaml `gateway.replicas: 2`.
- (verified) BUILD-GAPS.md: F-7.2.38, F-29.3.1, F-29.3.2, F-10.1.33, F-10.1.35, and F-12.8.43, all OPEN.
- (verified) gateway-runtime-comms-remediation.md: §10.2 entry "Inter-replica carrier proposal (not yet written)"; R10 re-derivation (interaction rows restated as store-and-wake, "which is how the four routes already work across replicas"); R13 re-derivation (revert `54292da1b` removed `coordinator_address` and `InterReplicaAddress`, routing uses the `REG-COORDLEASE` holder, the replica address moves to this proposal); R21 re-derivation (proposal 0093 rejected the durable release record, and the release request rides R18 or the Redis propagator as this proposal specifies).
- (verified) proposals/0092_new_write-the-session-suspend-and-resume-proposal-that-gateway/ summary: the inbox fallback for `running` and `input_required` is listed among the defects it does not stage.
- (verified) Branch `draft/0100-terminal-pipeline-p2a` exists and is not merged.

## Who observes it

- Clients of the default install. The chart runs two gateway replicas and §10.1 treats sticky routing as an optimization, so the load balancer routinely sends a session-scoped call to a replica that does not coordinate the session. No failure is needed to reach the defect.
- A client sending a message to a `running` session through a non-coordinating replica receives HTTP 500 `EXECUTOR_FAILURE` (F-7.2.38).
- A client answering an `input_required` session through a non-coordinating replica receives `queued`, and the message never reaches the runtime under `durableInbox: false` (F-29.3.2).
- A client resuming a held-pod `suspended` session through a non-coordinating replica receives 409 `TARGET_NOT_READY`. A retry succeeds only when the load balancer reaches the coordinator (F-29.3.1).
- Operators running user erasure after a drain observe pods counted in `lenny_orphaned_claims_total` until orphan GC drains them (F-12.8.43).

## What breaks if nothing changes

- Messages to `running` sessions fail with a server error, and messages to `input_required` sessions are silently lost. These are the most severe effects.
- Held-pod resume stays fail-closed off-holder, which is retryable and loses nothing.
- Off-holder terminate and delete keep writing the terminal state locally against the §29.3 terminate row. The holder-side Sweeper release cleans up the pod and binding, so the effect is a contract departure that loses the holder-side seal rather than an orphaned pod.
- After user erasure, a drained session's pod on another replica stays bound until `claimOrphanTimeout` plus one GC sweep interval, and indefinitely while another active session references it. Whether its credential leases are returned is an open question in F-12.8.43.
- R18, R19, and R21 stay blocked because they implement the contract this proposal stages.

## Findings this unblocks

- F-29.3.1 (off-holder held-pod resume).
- F-10.1.33 (off-holder terminate and delete).
- F-7.2.38 (off-holder message to a `running` session).
- F-29.3.2 (off-holder message to an `input_required` session buffered in the serving replica's inbox).
- F-12.8.43 (cross-replica release after user erasure), through the release request.

F-10.1.35 (stale generation on a barrier after a resume commit) is closed by R13's mirror refresh and needs no carrier. It is listed here only because it shows that `REG-COORDMIRROR` lags the lease.

## Prior art considered

- No proposal under `proposals/`, landed or draft, stages a general inter-replica request-forwarding service. Proposal 0052 deferred the forward-to-holder transport. Proposal 0092 put the general §29.3 forward carrier out of scope and recorded the off-holder resume, the off-holder terminate and delete, and the inbox fallback as unstaged defects.
- The specification already has `LNK-INTERREPLICA` in the §28.3 link register and the `ForwardMessage` conversation in §28.5.4 and §29.3. The proposal extends that link.
- The §11.4 full-revoke cross-replica binding release is specified and implemented as a Redis pub/sub propagator (`pkg/gateway/podlifecycle/podterminate/propagator`, channel `lenny:session:terminate`, wired in `cmd/lenny-gateway/user_revocation.go`). It is direct prior art for the R21 release request, and no §28 register lists it.
- Other Redis pub/sub propagators exist in `pkg/gateway` (credential revocation, deny list, credential renewal, and the EventBus) and serve as comparison points for the transport choice.
- Interaction resolution already crosses replicas through the Postgres shared interaction store with a poll on the coordinating replica, pinned by a tier-7a cross-replica test.
- Draft proposal 0100 (P2a terminal pipeline, unmerged) defers F-10.1.33 and the cross-replica half of F-12.8.43 to this proposal. Its staged release path and its `sessions.pod_released_at` marker already shorten F-12.8.43's bound.
- Proposal 0093 rejected a durable release record, which leaves the propagator or the new channel as the release carrier.

## Validated premises

### Premise lens (verdict: revise)

- CONFIRMED, load-bearing: §29.3 requires a forward to the coordinator on the interrupt, terminate, delete, held-pod resume, interaction-resolution, and upload rows and names no carrier for them. §28.3 places no channel on the boundary, and §28.5.4 has no card. Verified in the §29.3 "Two requirements" paragraph and in §28.3 and §28.5.4.
- PARTLY REFUTED: the original statement put message delivery among the rows with no named conversation. §29.3 and §28.5.4 name `ForwardMessage` on `LNK-INTERREPLICA` for message send, and the link register row exists. The proposal widens an existing link and settles what happens to `ForwardMessage`.
- CONFIRMED: §28.3 and §28.5.4 state no address, and `resolveReplicaID` is a per-process random-suffixed identity.
- REVISE, load-bearing: the original statement resolved the coordinator address from `REG-COORDMIRROR`. The mirror is a Sweeper-written projection that lags (F-10.1.35), and `REG-COORDLEASE` is the authoritative holder. The proposal names the authority and a stale-forward rejection rule.
- REFUTED as stated, load-bearing: the original statement said §7.2 path 6 had replaced the §29.3 inbox fallback and that proposal 0092 replaced it with DLQ custody. Path 6 covers only `suspended`. Proposal 0092 left the `running` and `input_required` rows unchanged and recorded the fallback as an unstaged defect (F-29.3.2). The fallback is still in §29.3, and moving it is a new decision.
- CONFIRMED: off-holder held-pod resume fails closed (`resume.go`, F-29.3.1).
- CONFIRMED: off-holder terminate and delete write locally, accepted by 0092 and 0093 as a departure (F-10.1.33).
- CONFIRMED: F-12.8.43 is open with an `ABSENT` row deferred to R21.
- MINOR MISATTRIBUTION: the original statement tied F-10.1.35 to bind-time coordinator resolution. It concerns mirror staleness. F-7.2.38 and F-29.3.2 were omitted and are now listed.
- CONFIRMED: Redis pub/sub propagators exist in `pkg/gateway`, so the gRPC-versus-propagator comparison has concrete alternatives.

### Evidence lens (verdict: revise)

- VERIFIED: `resolveReplicaID` returns `LENNY_REPLICA_ID` or hostname plus random suffix, which cannot be dialled.
- DRIFTED, load-bearing: the claim that §29.3 names no conversation for message delivery is false. `LNK-INTERREPLICA` (C19) and `ForwardMessage` exist. The address half is verified. The plan's §10.2 entry repeats the over-broad wording.
- FALSE: the claim that §7.2 path 6 replaced the inbox fallback contradicts the spec. The DLQ move remains a deliverable.
- DRIFTED: address resolution belongs to the `REG-COORDLEASE` holder. Revert `54292da1b` removed `coordinator_address`, and §28.5.4 says the coordinator is identified through the coordination lease.
- VERIFIED: F-29.3.1, F-10.1.33, and F-12.8.43 are open as described. The plan's R13, R18, R19, R21, and §10.2 entries match the deliverables. Redis propagators exist.

### Prior-art lens (verdict: revise)

- CONFIRMED: no proposal or branch stages a general inter-replica forwarding service. The gap is open.
- CONFIRMED, load-bearing: the specification already declares `LNK-INTERREPLICA` and `ForwardMessage`. The proposal extends the existing link.
- CONFIRMED, load-bearing: a cross-replica binding release exists in §11.4 and in code on `lenny:session:terminate`, and no §28 register lists it. The release decision starts from it, and the proposal registers it.
- CONFIRMED: draft proposal 0100 overlaps the release work and defers F-10.1.33 and part of F-12.8.43 here. The proposal sequences against it.
- CONFIRMED: the cited BUILD-GAPS findings are open, and no forward exists in code or in `schemas/`.

### Scope lens (verdict: revise)

- CONFIRMED: the channel, card, address, authentication, port security, transport choice, and per-kind behaviour form one contract and stay together.
- CONFIRMED, load-bearing: the original statement contradicted itself by listing interaction resolution as a carried kind while restating it as store-and-wake. Verified against `toolapproval.go` and the plan's R10 re-derivation. Interaction resolution leaves the forwarded set, and the restatement stays.
- PARTLY REFUTED, load-bearing: the lens proposed cutting the cross-replica release (R21, F-12.8.43) out as a separate problem whose fix may be a durable release record. Proposal 0093 rejected the durable release record, and the plan's R21 re-derivation records the owner decision of 2026-10-21 that this proposal specifies the release request. Release stays in scope, framed as extending or replacing the existing §11.4 propagator. The lens's point that release has premises of its own (erasure ordering and credential-lease return) is kept as a constraint, and R21's concurrent-slot eviction leg stays out of scope.
- CONFIRMED: the inbox-to-DLQ move is kept as the message kind's unreachable-coordinator fallback rather than as an independent deliverable.
- CONFIRMED: F-10.1.35 is closed by R13 and needs no carrier. It is kept only as evidence of mirror lag.
- CONFIRMED: address resolution is framed against the `REG-COORDLEASE` holder.

### Impact lens (verdict: revise)

- CONFIRMED, load-bearing: the defect is reachable on the default install (two replicas, sticky routing only an optimization).
- CONFIRMED, load-bearing: the most severe effects are F-7.2.38 (500 `EXECUTOR_FAILURE`) and F-29.3.2 (silent loss of `input_required` messages). Both are added to the unblocked findings.
- CONFIRMED: F-29.3.1 has moderate impact because it is retryable and loses nothing.
- CONFIRMED: F-10.1.33 is a contract departure mitigated by holder-side release rather than an orphaned pod.
- CONFIRMED: F-12.8.43 is bounded by `claimOrphanTimeout` plus one sweep, except while another active session references the pod.
- CONFIRMED: `ForwardMessage` already exists in §28.5.4 with an `ABSENT` claim, so the gap covers the non-message kinds, the channel row, and the address.
- CONFIRMED: the §29.3 inbox fallback still in the spec has a runtime effect through F-29.3.2.

### Alternatives lens (verdict: revise)

- CONFIRMED: a no-change reading does not hold. Code fails closed or writes locally.
- CONFIRMED: message send already has a named conversation. What it lacks is the channel row, the address, and the implementation.
- CONFIRMED, load-bearing: interaction resolution needs no carrier and is handled by restating §29.3 as store-and-wake.
- CONFIRMED, load-bearing: the §11.4 Redis pub/sub release propagator is a candidate carrier for the R21 release. The proposal reuses it or states why it is inadequate, and registers it in §28.3.
- OPEN: the held-pod resume could be a durable resume request picked up by §7.2 **Backlog resume.** instead of a forward. The proposal chooses and gives reasons.
- CONFIRMED: the kinds that need a synchronous holder-side carrier are interrupt, upload, terminate and delete, and message send to `running` or `input_required`. That set justifies the request-forwarding channel.
