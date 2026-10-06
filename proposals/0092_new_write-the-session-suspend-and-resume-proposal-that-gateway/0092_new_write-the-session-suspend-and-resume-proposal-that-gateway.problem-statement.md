# Problem: Session resume driver, recovering-state lease, active-time session age, and idle suspension

## Statement

The gateway has no automatic path from `resume_pending` to `running`. §7.3 "Resume flow after pod failure" (steps 3a through 3g) requires the gateway to allocate a replacement warm pod, restore the checkpoint, and resume. In the tree, the only writer of `resuming` is `handleResume`, which serves `POST /v1/sessions/{id}/resume` and admits only `awaiting_client_action`. The watchdog's `sweepResumePending` moves an expired `resume_pending` row to `awaiting_client_action` after `maxResumeWindowSeconds` (default 900 s), and `sweepResuming` moves a timed-out `resuming` row back to `resume_pending`. Neither sweep starts a resume. A failed session therefore reaches `running` only after a stall of about 15 minutes and a manual client `POST /resume`. `handleResume` also writes `resuming` directly from `awaiting_client_action` before it claims a pod and never writes `resume_pending`, which contradicts the chain its own comment names.

The restore mechanics already exist. `resumeOnPod` claims a fresh pod, restores the checkpoint through `podBinder.Resume`, takes the coordination lease, publishes the binding, bumps `recovery_generation`, and fences the pod. When no checkpoint exists, it rebuilds from the stored workspace plan through `startOnPod`. The post-resume tail in `handleResume` emits `session.resumed`, clears the inbox, and runs delegation-tree recovery. The missing piece is the trigger: a driver that invokes this existing path from `resume_pending`, and later from a podless `suspended` session. The driver reuses `resumeOnPod` and the `handleResume` tail rather than building a parallel restore path.

Proposal 0091 is Approved and is assumed implemented. Under it, the session's Attach stream lives as long as its binding, a stream failure is reported as `runtime_crash` through the §7.3 classifier (`resume_pending` while retries remain, then `awaiting_client_action`), the failure edge releases only the pod binding with the failed disposition, the coordinating replica keeps and renews `REG-COORDLEASE` in `resume_pending` and `awaiting_client_action`, and a `POST /resume` that meets a held lease answers the retryable `RESUME_FAILED`. Every pod or stream failure then lands in `resume_pending` with no driver, and a client retry that reaches a replica other than the lease holder keeps receiving retryable failures because the §29.3 forward of `POST /resume` is not built.

The `maxSessionAge` clock does not match the spec. §6.2 "`maxSessionAge` timer behavior across states" pauses the timer in `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action`, and persists `accumulated_session_age_seconds`. The tree has no such column or code. `sweepMaxAge` expires every non-terminal session once `now - CreatedAt` exceeds the cap (7200 s), suspended and recovering sessions included. Any lifetime for a suspended session longer than two hours from creation is unreachable until the pause accounting exists.

The proposal covers the following, in deliverable order. The owner decisions recorded in the remediation plan (gateway-runtime-comms-remediation.md §10.2, the suspend-and-resume entry) fix items 5 through 8.

1. Resume driver. Build the automatic `resume_pending` to `resuming` to `running` path on top of `resumeOnPod`. Fix the order in which `handleResume` writes `resuming` relative to the claim, and decide whether the client path passes through `resume_pending`. Settle the boundary with BUILD-GAPS F-15.1.41 (a non-transient resume claim error demotes the row to `failed`), which the recycling follow-up owns but which changes the same `handleResume` failure exit and `isTransientPodClaimError`.
2. Retry budget. Reconcile `sessionPolicy.maxSessionRetries` (§6.2, §5.2, default 1) with `retryPolicy.maxRetries` (§7.2, §7.3). The watchdog spends `retryPolicy.maxRetries` through `effectiveMaxRetries`. The driver spends whichever budget the proposal settles on.
3. No-checkpoint restore rule. Write into §7.3 the snapshotless rebuild that `resumeOnPod` already performs.
4. Coordination lease in the recovering states. State when and by whom `REG-COORDLEASE` is acquired, adopted, or released in `resume_pending`, `awaiting_client_action`, and `suspended`. The §29.3 off-holder matrix already asserts a coordinating replica in these states; what the spec lacks is adoption after coordinator death and the release point. The coordination Sweeper adopts a lapsed lease only for a `running` or `input_required` row with a pod assignment (`isRunningPod`). Takeover of held-pod `suspended` sessions belongs here and must preserve the never-bound-row exclusion that implemented proposal 0060 built into `isRunningPod`. Decide the lease lifecycle so that a resume either needs no inter-replica forward or keeps 0091's retryable `RESUME_FAILED`.
5. Active-time `maxSessionAge`. Owner decision: `maxSessionAgeSeconds` stays at 7200 s and counts only active time. This requires implementing the §6.2 pause table and `accumulated_session_age_seconds`, which the tree lacks.
6. Podless suspension. Enforce the existing §6.2 "Graceful pod release during extended suspension" (checkpoint, then release after `maxSuspendedPodHoldSeconds`, default 900 s), which closes F-11.3.35. Route a message to a podless `suspended` session through `resume_pending` and the driver, at the integration point `resumeHeldPod` already marks as deferred.
7. Idle suspension. Owner decisions: a session is idle when no turn is in flight and no traffic passes in either direction between client and runtime; when the idle window ends (default 15 minutes, operator-tunable), the session is suspended with an immediate checkpoint and its pod or slot is released at once; any new message to a suspended session triggers a resume, beyond `delivery: immediate`; a suspended session expires after an operator-tunable lifetime (default 7 days). These amend the existing §6.2 release and §7.2 inbox path 6 rather than adding a parallel suspension mechanism. The proposal states which suspensions the `maxSuspendedPodHoldSeconds` hold still governs once idle-triggered suspension releases at once, and what the idle clock means in `awaiting_client_action`, where §6.2 currently runs it as that state's reclaimer and no pod exists to release. Rename the idle flag to match `maxClientIdleSeconds` and correct its help, which closes F-11.3.36.
8. Content-free resume of a suspended session. §15.1 excludes `suspended` from `POST /resume` ("use message delivery or `resume_session`"), and `session_test.go` asserts the exclusion. The MCP tool `lenny/resume_session`, which §15.2 describes as resuming a suspended session, is wired to `POST /resume` and therefore cannot resume a suspended session. The proposal fixes the tool and decides whether to reverse the §15.1 exclusion for REST.

The proposal also settles two spec gaps the plan leaves open: the disposition (recycle or retire) of a pod a suspension releases, and what releasing "the pod" means for one session on a concurrent-mode pod. The plan does not decide the second gap.

Out of scope: a general §29.3 inter-replica forward carrier for non-message requests (interrupt, terminate, delete, resume, interaction resolution, upload, and events), takeover of `starting` sessions after coordinator death (coordination Sweeper work), and recycling of an `expired` session's pod on a recycling pool (the recycling follow-up proposal). The proposal builds on proposal 0090's runtime contract: Resume writes `session_start` on the new pod, and a released session receives `session_end`. The change stays as small as the decisions allow.

## Evidence

- Plan: gateway-runtime-comms-remediation.md §10.2, the 0091 entry with its option (a) scope decision and the suspend-and-resume entry with the owner decisions. (verified)
- Plan text on the concurrent-pod gap: "what releasing "the pod" means for one session on a concurrent pod;" with no answer. (verified)
- Proposal 0091 status is Approved. Its staged SPEC-2a, the CH-ATTACH claim row, DOCS-1 R2, the Invariant paragraph, CODE-2 item 8, and RECORDS-1 items 4, 10, 11, 16, and 17 match the premises above. (verified)
- Proposal 0090 status is Implemented; `session_start`, `session_started`, and `session_end` are in the §28.5.3 CH-MSGSOCK message set. (verified)
- BUILD-GAPS F-11.3.35 and F-11.3.36 are OPEN. F-15.1.41 is OPEN and assigned to the recycling follow-up. (verified)
- `pkg/gateway/sessionserver/resume.go`: `row.State = session.StateResuming` is the sole writer of `resuming` outside the watchdog, before `resumeOnPod`. (verified)
- `pkg/api/v1/session/session.go`: `EndpointResume` admits only `StateAwaitingClientAction`. (verified)
- `pkg/gateway/runtime/watchdog/watchdog.go`: `sweepResumePending` moves `resume_pending` only to `awaiting_client_action`; `sweepResuming` moves `resuming` to `resume_pending`; `sweepMaxAge` measures `now.Sub(row.CreatedAt)` over every non-terminal row; `MaxSuspendedPodHoldSeconds` is defaulted and read by no sweep; `DefaultMaxIdleSeconds` equals `DefaultMaxSessionAgeSeconds` (7200). (verified)
- No `accumulated_session_age` or `AccumulatedSessionAge` identifier exists under `pkg/`, `cmd/`, or `migrations/`. (verified)
- `pkg/gateway/sessionserver/resume_rebind.go`: `resumeOnPod` performs the snapshotless rebuild through `startOnPod` and `registerBinding` and fences the resumed pod. (verified)
- `pkg/gateway/sessionserver/resume_held_pod.go`: the doc comment states that no §6.2 sweep releases a suspended pod today and defers the podless route to `resume_pending` or `queued`. (verified)
- `pkg/gateway/coordination/coordination/coordination.go`: `isRunningPod` admits only `running` or `input_required` rows with a pod assignment. (verified)
- `pkg/gateway/mcpfabric/mcptools/client_tools.go`: `lenny/resume_session` posts to `/resume`. (verified)
- `cmd/lenny-gateway/flags.go`: `--max-suspended-pod-hold-seconds` help says the watchdog transitions the session to `expired`; `--max-idle-time-seconds` help names `maxIdleTimeSeconds`, a 600 s default, and the reason `expired:idle`. (verified)
- Spec §6.2: the `maxSessionAge` pause table and `accumulated_session_age_seconds`; "Graceful pod release during extended suspension"; the `maxClientIdleSeconds` clock table; "`suspended → expired` trigger mechanism"; "Pod crash during an active session" with `maxSessionRetries`. (verified)
- Spec §7.2 inbox path 6, §7.3 retry policy and "Resume flow after pod failure", §15.1 `POST /resume` precondition, §15.2 `resume_session`, §11.3 `maxSuspendedPodHoldSeconds` (900 s), §28.3 `REG-COORDLEASE`. (verified)
- Spec §29.2 step 11 concerns the acquisition of `REG-COORDLEASE` at session creation. §29.3 states the `POST /resume` forward row and, in the terminate row, a coordinating replica in `suspended`, `resume_pending`, and `awaiting_client_action`. (verified; the original statement cited step 11 for the recovering states, which it does not cover)

## Who observes it

- Clients of any session whose pod or Attach stream fails. Once proposal 0091 lands, every such failure leaves the session in `resume_pending` for up to `maxResumeWindowSeconds` (default 900 s) with no recovery attempt, then in `awaiting_client_action` until the client calls `POST /resume`. §7.3's automatic retry never runs.
- Clients whose `POST /resume` reaches a replica other than the lease holder. Under 0091 they receive retryable `RESUME_FAILED` until the coordinator's lease lapses, because no forward and no adoption rule exist.
- Clients of `lenny/resume_session` on a `suspended` session, whose call is refused by the `/resume` precondition.
- Operators, who see a suspended session hold its pod or slot past the 900 s the spec allows (bounded in practice by the 7200 s wall-clock age expiry), and who read flag help that states wrong outcomes, a retired name, and a wrong default.
- Any session that spends time in `suspended`, `resume_pending`, `resuming`, or `awaiting_client_action`, which the wall-clock `sweepMaxAge` expires 7200 s after creation although the spec pauses the clock in those states.

## What breaks if nothing changes

- Crash recovery stays manual and slow: every failure costs a stall of about 15 minutes and a client action.
- The 0091 lease behavior strands client retries on non-holder replicas.
- The owner's idle-suspension design cannot work: an idle-suspended session would be expired by `sweepMaxAge` 7200 s after creation, so the 7-day suspended lifetime is unreachable.
- Suspended sessions keep their pods past the spec's hold window and waste warm-pool capacity.
- Operator documentation in the flag help stays wrong.

The idle-to-expiry behavior itself conforms to the current spec (`maxClientIdleSeconds` defaults to 7200 s and fires `expired`). Items 6 through 8 in the statement are therefore a product change by owner decision. They do not fix a defect users hit today, except for F-11.3.35, F-11.3.36, and the `resume_session` wiring.

## Findings this unblocks

- BUILD-GAPS F-11.3.35 and F-11.3.36.
- Proposal 0091 RECORDS-1 items 4 (no driver from `resume_pending`), 10 (recovering-state lease lifecycle and the §29.3 resume forward), 11 (the two retry budgets), 16 (held-pod `suspended` half only), and 17 (`handleResume` writes `resuming` before the claim).
- BUILD-GAPS F-15.1.41, which this proposal takes from the recycling follow-up.

## Prior art considered

- No proposal under `proposals/` stages this change. Draft 0061 concerns the single-shot OpenAI-dialect adapter pod hold and does not touch session suspension.
- The restore primitives exist in the tree: `resumeOnPod`, `podBinder.Resume`, checkpoint selection in `resume_checkpoint.go`, the snapshotless rebuild, lease acquisition through `registerBinding`, and the resumed-pod fence. The watchdog's `resuming` timeout and retry sweep also exist.
- The spec already defines podless suspension: the §6.2 graceful release after `maxSuspendedPodHoldSeconds`, the podless resume through `resume_pending`, and §7.2 path 6 immediate-delivery resume with its coordinator forward. Idle suspension changes when that release fires.
- `resumeHeldPod` implements the held-pod `suspended` to `running` edge and records the podless route as deferred to this work.
- Implemented proposal 0060 limited Sweeper adoption to running rows with a pod assignment to keep peers from adopting never-bound rows. Takeover of held-pod `suspended` rows must keep that exclusion.
- The §15.1 exclusion of `suspended` from `POST /resume` is deliberate and covered by `session_test.go`.
- Proposal 0091 lists automatic re-dispatch from `resume_pending` as out of scope and files the RECORDS-1 items above for this proposal.

## Validated premises

Premise lens (verdict: revise)

- Confirmed: nothing in the tree moves a session from `resume_pending` to `resuming`; the only writer of `resuming` is `handleResume`, admitted only from `awaiting_client_action`.
- Refuted: "maxSessionAgeSeconds is unchanged and counts only active time" describes the spec rather than the tree. `sweepMaxAge` uses `CreatedAt` over every non-terminal row, and no accumulated-age column exists. Leaving `maxSessionAge` unchanged is not enough; the pause accounting must be built.
- Refuted for the tree: "a podless suspended root session can stay suspended forever" holds only in the spec (§6.2). In the tree no podless suspended row exists, because nothing releases a suspended pod, and `sweepMaxAge` expires suspended rows 7200 s after creation.
- Corrected: §29.2 step 11 concerns creation-time acquisition. The recovering-state gap is adoption after coordinator death and release, filed by 0091 RECORDS-1 item 10.
- Confirmed: §29.3 requires the `POST /resume` forward, and `handleResume` reads no lease holder.
- Confirmed: the Sweeper adopts only running or `input_required` rows with a pod assignment.
- Confirmed: `handleResume` writes `resuming` before the claim and never writes `resume_pending`.
- Confirmed with an addition: the idle premises hold for the tree. The statement omitted that the §6.2 idle clock runs in `awaiting_client_action`, where no pod exists to release.
- Confirmed: F-11.3.35 is unenforced and its flag help contradicts §6.2.
- Confirmed and extended: `lenny/resume_session` is wired to `POST /resume`, which refuses `suspended`, so the content-free resume of a suspended session is broken in code as well as missing from REST.
- Confirmed: 0091 is Approved; both retry budgets exist; the 0090 contract is in §28.

Evidence lens (verdict: stands)

- Verified: the plan entries, F-11.3.35, F-11.3.36, the resume-driver gap, the `handleResume` ordering, idle-to-expiry today, the spec statement that a podless suspended root session may remain indefinitely, the §6.2 graceful release, the two retry budgets, the §29.3 resume forward, the 0091 premises, the 0090 contract, and the §28.3 `REG-COORDLEASE` row.
- Drifted: the original scope item presented "only the slot" as the answer for a concurrent pod; the plan leaves it open.
- Drifted: the §29.2 step 11 citation, as the premise lens found.

Prior-art lens (verdict: revise)

- Confirmed: no other proposal stages this change.
- Refuted in part: "nothing claims a replacement pod and restores the checkpoint" overstates the gap. The restore path exists in `resumeOnPod`; only the trigger is missing.
- Confirmed: the code already implements the no-checkpoint rebuild, which the spec does not state.
- Confirmed: the spec already defines podless suspension; idle suspension amends it.
- Confirmed: `resumeHeldPod` is the integration point for the podless route.
- Confirmed: the REST exclusion of `suspended` is deliberate and test-asserted.
- Confirmed: the Sweeper exclusion comes from implemented proposal 0060.
- Confirmed: the closed findings match 0091's deferrals; the retry budgets are unreconciled.

Scope lens (verdict: revise)

- Confirmed: the driver, its retry budget, the no-checkpoint rule, and the `handleResume` order form one unit.
- Accepted: a general §29.3 forward carrier for non-message requests is out of scope; the lease lifecycle is decided so that resume needs no new carrier or keeps the retryable error.
- Accepted: takeover of `starting` sessions moves to coordination work; only the held-pod `suspended` half stays.
- Accepted: recycling of an `expired` session's pod moves to the recycling follow-up.
- Accepted: the idle-suspension work depends on the driver and is ordered after it; a split into two proposals is left open.
- Accepted: 0091 RECORDS-1 item 4 is added to the closed findings, and the F-15.1.41 boundary is named.

Impact lens (verdict: revise)

- Confirmed: the resume-driver gap is user-visible on every failure once 0091 lands.
- Confirmed: `handleResume` contradicts the documented chain.
- Refuted: the podless-forever premise and the "unchanged" `maxSessionAge` premise, as the premise lens found; decision (6) is new code.
- Refined: the F-11.3.35 hold is bounded in practice by the 7200 s wall-clock age cap.
- Refined: F-11.3.36 is operator-facing misinformation; idle-to-expiry rarely binds before the age cap, so the idle decisions are a product redesign.
- Confirmed: the 0091 lease behavior strands client retries without a forward or adoption rule.

Alternatives lens (verdict: revise)

- Refuted in part: after 0091 the session does not stall with nothing to move it on. The watchdog moves it to `awaiting_client_action` after 900 s, and `POST /resume` recovers it. The missing piece is the automatic §7.3 step 3b.
- Confirmed: the driver is an implementation gap against existing spec text and reuses `resumeOnPod`.
- Recorded: items 6 through 8 are owner product decisions that replace spec-conformant behavior; a split into a driver-and-lease proposal and an idle-suspension follow-on is a smaller alternative, left to the owner.
- Recorded: F-11.3.36's flag text and F-11.3.35's enforcement do not depend on the idle redesign; the proposal must state which suspensions the hold governs after idle suspension releases at once.
- Confirmed: the lease acquisition and forward rules plus the driver are the part that 0091 depends on.
