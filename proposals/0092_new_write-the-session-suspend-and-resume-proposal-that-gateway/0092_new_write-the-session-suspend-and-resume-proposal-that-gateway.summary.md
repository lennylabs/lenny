# Summary: Session resume driver, recovering-state coordination lease, active-time session age, and idle suspension

## Summary

**Problem statement.** After proposal 0091 lands, every pod or `Attach` stream failure leaves a session in `resume_pending`, and nothing moves it on to `running`: the restore code exists, but no component triggers it, and a client's `POST /v1/sessions/{id}/resume` served by a replica other than the lease holder fails. A coordinator that dies in a recovering or held-pod suspended state leaves the session without an owner. Two retry budgets exist, and one recovered crash spends two units of the one that is read. Session age runs on wall-clock time where the spec requires active time. A suspended session never releases its pod, the idle flag is misnamed and misdocumented, and `lenny/resume_session` cannot resume a suspended session. The owner decided that an idle session is suspended and its pod released at once, that any message resumes a suspended session, and that a suspended session lives at most 7 days.

**What changes.**
- Session server and watchdog: a resume driver that is the only path out of `resume_pending`, a store-only `POST /resume` that also admits `suspended`, FIFO delivery of buffered messages after a restore, and the deletion of `RESUME_FAILED` (CODE-4, SPEC-1, SPEC-3).
- Binder: a post-claim callback, so `resuming` is written after the claim and before the first RPC to the pod (CODE-3).
- Coordination Sweeper: adoption of every ever-bound non-terminal session, with the fence only when a pod is held (CODE-5, SPEC-2).
- Retry accounting: `retryPolicy.maxRetries` is the only budget, spent once per failure edge (CODE-2, SPEC-1).
- Session store and watchdog: active-time session age in `accumulated_session_age_seconds` (CODE-1, SPEC-4).
- Watchdog and session server: idle suspension, the suspension release, the 7-day suspended-session lifetime, resume on any message, and the renamed and corrected flags (CODE-6, SPEC-5).

**Decisions.**
1. The resume driver is the only code that moves a session out of `resume_pending`. It hooks `sweepResumePending` and runs on the lease holder, or on every replica in dev mode. Every route into recovery is a guarded store write into `resume_pending`, which reuses the §7.2 `awaiting_client_action → resume_pending` edge and the §29.6 step 3 write and needs no inter-replica forward.
2. `resuming` is written after the claim, through the binder's post-claim callback. A pre-claim failure writes nothing, so the `UpdatedAt`-anchored `maxResumeWindowSeconds` window keeps its meaning, §7.2 "pod allocated" and the pre-claim `resume_pending → cancelled` rule stay true, and no state edge or field is added.
3. A post-claim failure goes through `ReportSessionFailure` and `applyFailureFromResuming`, which already implement the §6.2 `resuming` exits and never write `failed`. `holdOrFailOnResumeError` is deleted, which closes F-15.1.41 here.
4. `POST /v1/sessions/{id}/resume` admits `awaiting_client_action` and `suspended`. The podless sources are store writes any replica serves; held-pod `suspended` follows §7.2 delivery path 6 through the coordinator, so the held-pod resume edge keeps one implementation. `RESUME_FAILED` has no producer left and is deleted.
5. A lapsed lease is adopted for every ever-bound non-terminal state except `starting`, with the fence only when a pod is held. The §29.3 terminate, delete, resolution, and events rows need a holder in `awaiting_client_action` and podless `suspended`, and adopting a podless row costs only a generation compare-and-swap and a renew.
6. `retryPolicy.maxRetries` is the only retry budget, spent only on a failure edge into `resume_pending`. `sessionPolicy.maxSessionRetries` has no reader, and the second increment in `bumpRecoveryGeneration` would turn the default budget of 2 into one automatic recovery.
7. Active age uses the single column the spec names, accrued in `Store.Update` from the strictly advancing `UpdatedAt`. The §6.2 evaluation on entry to `running` stays normative.
8. The suspension release reuses the §6.2 graceful release in the order checkpoint, clear binding, release, with the pool's normal session-end disposition and only the slot on a concurrent pod. Clearing the binding first means a crash leaves an orphan claim, never a row naming a released pod.
9. `maxSuspendedPodHoldSeconds` governs interrupt suspensions only. An idle suspension, recorded with the suspension reason `Idle`, has a hold of zero.
10. Idle means a `running` session with no turn in flight and no qualifying event for `maxClientIdleSeconds` (default 900 s). The clock is not evaluated in `input_required`, where a turn is in flight, and is paused in `awaiting_client_action`, which holds no pod.
11. Any message to a `suspended` session resumes it. On a podless session the serving replica writes `resume_pending` and buffers the message, and the driver delivers buffered messages in FIFO order after the restore, without which the triggering message is lost.
12. A suspended session expires `gateway.maxSuspendedSessionSeconds` (default 604800 s) after its entry to `suspended`, reusing the `max_idle_time` expiry reason. The budget-key TTL default rises to 691200 s, must exceed that lifetime, and is re-armed when a root leaves `suspended`, because a TTL set once at tree creation fires during a later suspension of a cycling session.
13. `ReattachNode` routes a `resume_pending` descendant through the driver and skips a `resuming` one, so tree recovery and the driver cannot claim two pods for one session.
14. §7.3 step 3e states the snapshotless rebuild, `--max-idle-time-seconds` is renamed `--max-client-idle-seconds`, and the hold flag's help is corrected.

**Watch out for.**
- This proposal lands after 0091. CODE-4, CODE-5, and CODE-6 edit code that 0091 adds or rewrites, and the SPEC anchors quote text after 0091's edits.
- `resumeOnPod` must stop publishing the binding, writing `PodAssignment`, and fencing. Leaving any of them before the `resuming → running` commit lets a lost commit leave a published binding behind (CODE-4 item 4).
- The snapshotless rebuild on a concurrent pool goes through `BindSlot`, never `Bind`. A hook wired only into `Bind` leaves those sessions in `resume_pending` forever.
- A slot retry calls the post-claim hook again. Without the per-attempt flag, the second call fails the compare-and-swap and aborts a valid restore.
- `TurnInFlight` is in-memory on the coordinating replica. An idle suspension evaluated on any other replica sees no turn and would suspend a session mid-turn, so the lease gate on `SuspendIdle` is a correctness predicate.
- Without the `LastAgentActivityAt` stamp on entry to `running`, a session resumed after a long suspension is idle-suspended on the next tick.
- The admin decoder ignores unknown fields, so a test that expects `maxSessionRetries` to be rejected fails. Assert that the key is not persisted.

## Goals

- A session in `resume_pending` reaches `running` on a replacement pod with no client action, on whichever replica holds its lease.
- A `POST /v1/sessions/{id}/resume` or `lenny/resume_session` call succeeds on any replica, from `awaiting_client_action` and from `suspended`.
- A coordinator's death in any ever-bound non-terminal state other than `starting` leaves the session with a new owner.
- One recovered crash spends one retry.
- Session age counts only active time.
- An idle session releases its pod or slot, a suspended session resumes on any message, and a suspended session expires after its lifetime.

## Non-goals

- A general §29.3 inter-replica forward carrier for non-message requests. The store-only resume route does not need it.
- Takeover of `starting` sessions after coordinator death, left to coordination work, and recycling an `expired` session's pod, left to the recycling follow-up.
- A Sweeper `Driver.Drive` seam hosting the resume, the hold release, and the idle suspension. The watchdog already lists each state, holds the hold and idle configuration, and runs every 5 s against the Sweeper's 15 s.
- Not renewing the lease in `awaiting_client_action` and podless `suspended`. It would need §29.3 terminate and delete edits and a window of retryable `RESUME_FAILED`.
- Releasing the lease with the binding. It reverses 0091's approved invariant and removes the claim mutex.
- Acquiring the lease at attempt time as the only adoption. It skips the `RecordHandoff` generation compare-and-swap that §10.1.2 requires on every acquisition.
- A synchronous `POST /resume` that claims a pod. It leaves two restore entry points and needs a forward or the off-holder `RESUME_FAILED`.
- A `202` response with idempotent replay in `resume_pending` and `resuming`. `200` with the row matches the existing endpoint, and a client that lost the response can read the state.
- Writing `resuming` at attempt start with a revert on a transient error, and anchoring the window on `ResumeEligibleUntil`, which is a §4.2 create-time field.
- A pool-exhaustion wait inside `resuming`. It moves the wait out from under `maxResumeWindowSeconds`.
- Splitting `Binder.Resume` into separate claim and restore calls. The callback gives the same ordering.
- A `resume_pending_since` column or any other new window anchor.
- An immediate `resume_pending → awaiting_client_action` on a non-retryable pre-claim error. A pre-claim error writes nothing, and the window ends it.
- Extra driver triggers from the failure funnel, from `POST /resume`, or from the podless message path, and an inline resume attempt in the message path. The 5 s tick covers every entry, and an inline attempt blocks the request.
- A generation-guarded commit with publish-after-commit plus a lock around classify and send as a separate design. CODE-4 takes the publish-after-commit order and one per-session delivery lock.
- Keeping `sessionPolicy.maxSessionRetries` as a per-pool cap or reconciling it through `min()`, and deleting `--retry-max-retries`, which is the deployer cap and default for `retryPolicy.maxRetries`.
- A second age column. Incremental accrual on the strictly advancing `UpdatedAt` is exact.
- Deleting `maxSuspendedPodHoldSeconds` and running the idle clock in pod-held `suspended`. The owner asked to enforce the existing release, and interrupt pause-and-decide keeps its held pod.
- Running the idle clock in `input_required` or `awaiting_client_action`, and idle detection from `LastAgentActivityAt` alone, which would suspend a silent tool call mid-turn.
- Releasing every suspension at once, including interrupt suspensions.
- New vocabulary: a new expiry or failure reason for the suspended lifetime, a new checkpoint trigger label (`pre_scale_down` is bound by §4.4 to the PoolScalingController release), a new release disposition, and a new suspension-reason column. `periodic`, `max_idle_time`, `DispositionCompleted`, and the existing `SuspendedReason` field are reused, with one new value `Idle`.
- A tenant-level override for `maxSuspendedSessionSeconds`.
- A new `resumeMode` value for a rebuild, or widening `conversation_only`. The misreported `full` mode is recorded as a defect.
- A new MCP-only route or REST endpoint for content-free resume.
- Resetting `retryCount` after a successful resume.

## Open decisions for human to make

No entry is recorded yet; the review loops write this section. The drafting pass left one question for them to adjudicate: whether to ship this as one proposal or to split it into option A (the driver, lease, and retry work: SPEC-1 to SPEC-3, CODE-2 to CODE-5, DOCS-1a) and option B (active age and idle suspension: SPEC-4, SPEC-5, CODE-1, CODE-6, DOCS-1b). The owner made the decisions behind option B from tree descriptions that the validated problem refuted (that age already counts active time, and that podless suspension already exists). Option B's lifetime depends on CODE-1, and its podless route depends on CODE-4.

## Defects in the shipped tree that this proposal does not stage

- **A snapshotless rebuild reports `resumeMode: full`.** `classifyResume` returns `full` for a resume with no checkpoint, which §7.2 defines as a full restore from a checkpoint. A new mode is out of scope; RECORDS-1 files it.
- **Off-holder resume of a held-pod `suspended` session.** A message or `POST /resume` served by a replica other than the coordinator runs `resumeHeldPod` locally with no §7.2 path 6 forward, and every message now takes that route. The forward carrier is out of scope; RECORDS-1 files it.
- **`nodeNeedsRecovery` reads the local pod registry.** The root's lease holder can judge a descendant bound on a peer replica orphaned. It predates this proposal; RECORDS-1 files it.
- **Delegation budget keys carry no TTL.** No code passes `delegation.budgetKeyTTLSeconds`, and no chart value carries it, so the SPEC-5 TTL default and re-arm have no carrier. Building the budget-key TTL is out of scope; RECORDS-1 files it.
- **No tenant cap for `maxSuspendedPodHoldSeconds`.** No tenant-configuration field carries it, so only the deploy-wide value applies. RECORDS-1 files it.
- **No takeover of `starting` sessions.** The finding 0091 filed is narrowed to `starting` and stays open.
- **An `expired` session's pod is not recycled on a recycling pool.** It belongs to the recycling follow-up, which RECORDS-1 item 9 hands it to.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0091 | Approved, not implemented | Lands after it. Deletes the `ErrHeld` hold in `holdOrFailOnResumeError` that its CODE-2 item 8 adds and the retryable `RESUME_FAILED` answer it keeps. Edits the `Sweep` loop its CODE-5 rewrites, reads the turn token its CODE-1 adds, and quotes the §6.2 and §7.3 text its SPEC-1 writes. Closes or narrows the findings its RECORDS-1 items 4, 10, 11, 16, and 17 file. | Nothing. It lands first, and its record stands. |
| 0060 | Implemented | Widens the Sweeper adoption predicate it built and keeps its never-bound exclusion. | Nothing. The record stands. |
| 0058 | Implemented | Its §7.2 path 6 pod-held resume-and-deliver now runs for any message to a held-pod `suspended` session, beyond `delivery: immediate`, and gains a podless branch. | Nothing. The record stands. |
| 0081 | Implemented | Removes `RESUME_FAILED` from the slot-bind refusal envelope it specified in §4.7.1. | Nothing. The record stands. |
| 0085 | Implemented | Removes `RESUME_FAILED` from the `SETUP_COMMAND_FAILED` fallback list it edited in §15.1. | Nothing. The record stands. |
| 0090 | Implemented | The resume driver relies on its runtime contract: `Resume` writes `session_start` on the new pod, and a released session receives `session_end`. | Nothing. |

## Deliverable index

- SPEC-1 — `spec/07_session-lifecycle.md`, `spec/06_warm-pod-model.md`, `spec/05_runtime-registry-and-pool-model.md` — state the resume driver, the snapshotless rebuild, and the retry accounting in §7.3, and delete `maxSessionRetries`.
- SPEC-2 — `spec/10_gateway-internals.md` — state lease renewal and adoption after the first bind in §10.1.1, and the no-pod skip in §10.1.2 step 2.
- SPEC-3 — `spec/15_external-api-surface.md`, `spec/07_session-lifecycle.md`, `spec/04_system-components.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/29_communication-scenarios.md` — make `POST /resume` a store write that admits `suspended`, delete `RESUME_FAILED`, and update the §29.3 and §29.6 rows and steps.
- SPEC-4 — `spec/05_runtime-registry-and-pool-model.md` — correct the `maxSessionAgeSeconds` comment to active time.
- SPEC-5 — `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`, `spec/15_external-api-surface.md`, `spec/29_communication-scenarios.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/11_policy-and-controls.md`, `spec/08_recursive-delegation.md`, `spec/09_mcp-integration.md`, `spec/16_observability.md`, `spec/14_workspace-plan-schema.md`, `spec/17_deployment-topology.md`, `spec/27_web-playground.md` — state idle suspension, the suspension release, the suspended-session lifetime, resume on any message, and the budget-key TTL rule.
- CODE-1 — `migrations/0182_sessions_accumulated_session_age.*.sql`, `pkg/gateway/session/sessionstore/`, `pkg/gateway/runtime/watchdog/watchdog.go`, `pkg/gateway/sessionserver/resume_held_pod.go` — accrue and enforce active session age.
- CODE-2 — `pkg/gateway/sessionserver/session_generation.go`, `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/externalapi/admin/runtimes.go`, `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/ops/mcp/generated_tools.go` — one retry budget.
- CODE-3 — `pkg/gateway/podlifecycle/podsession/`, `pkg/gateway/sessionserver/pod_launch.go`, `slot_bind.go`, `resume_rebind.go` — the post-claim callback on every binder path.
- CODE-4 — `pkg/gateway/sessionserver/resume_driver.go` and the session-server, watchdog, session API, inbox, error-classification, flag, and SDK files the deliverable names — the resume driver, the store-only `POST /resume`, buffered delivery, and tree-recovery routing.
- CODE-5 — `pkg/gateway/coordination/coordination/coordination.go` — adoption of every ever-bound non-terminal session.
- CODE-6 — `pkg/gateway/runtime/watchdog/watchdog.go`, `pkg/gateway/sessionserver/suspend_release.go` and the session-server, executor, routing, idle-resolver, flag, and chart files the deliverable names — the suspension release, idle suspension, the suspended-session lifetime, podless message routing, and the flags.
- DOCS-1a — the reader pages the deliverable names under `docs/` — reader documentation for the driver, the lease, and the retry budget.
- DOCS-1b — the reader pages the deliverable names under `docs/` — reader documentation for active age and idle suspension.
- TEST-1 — `tests/tier2_component/`, `tests/tier3_contract/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier11_docs/` — tests at tier 2 and above, and the tier-0 and tier-11 checks.
- RECORDS-1 — `BUILD-GAPS.md`, `scripts/seed-claim-register.py`, `tests/claim-map.json`, `gateway-runtime-comms-remediation.md` — finding closures and new findings, claim rows, and the remediation-plan tick.
