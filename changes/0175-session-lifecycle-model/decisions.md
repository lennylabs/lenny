# Decisions: 0175 A formal model of the session lifecycle

## Open decisions

### O4. Does `perChildMaxAge` expire a child in `awaiting_client_action` while an ancestor is suspended? (TIMEOUT-I1)

Moving the child's age into 0146 (decision 1) put two rules side by side that disagree. SLR-21 states that "a descendant in `awaiting_client_action` does not expire while an ancestor is suspended". "A child's age" in SL-10, from decision 13 of 0166, states that `perChildMaxAge` counts "in every non-terminal state of the child", and the term in 0146 says no pause stops it. The model's configuration `chain2_childage`, which reads the age rule, breaks `ACANoExpiryWhileAncestorPaused` (counterexample R5 in `change.md`); `chain2_client` and `chain3_client`, which read SLR-21, hold it.

- **(a) SLR-21 narrows to `maxAwaitingClientActionSeconds`.** SLR-21 reads "a descendant in `awaiting_client_action` does not reach `maxAwaitingClientActionSeconds` while an ancestor is suspended", and `perChildMaxAge` still expires the child, as decision 13 of 0166 makes it a deadline no pause stops.
- **(b) The age stops too.** `perChildMaxAge` pauses for a child in `awaiting_client_action` while an ancestor is suspended, and SL-10 "A child's age" names the exception.

Recommendation: (a). Decision 13 of 0166 made the age a wall-clock deadline that no pause stops, and SL-10's timer table already counts it in the paused states; SLR-21 was written for the `maxAwaitingClientActionSeconds` clock, which the same table stops while an ancestor is suspended. The fix is a wording edit of SLR-21 in 0146.

### O5. The tree terminate: the root's terminal state, and a tree whose root already ended (DTREE-I1)

SL-9 and 0166 "DP-2" state that `POST /v1/admin/sessions/{rootId}/terminate-tree` "writes the same audit event as a force-terminate" and closes each descendant at once. Neither states the terminal state it records on the root, and neither states what it does when the root has already ended while descendants still run under `let_finish` or stay `blocked` under an `ended_suspended` root, which is the case an administrator who wants a tree stopped meets. The model ends the root `failed`, as a force-terminate does, and applies the route to an ended root by closing its remaining descendants; both are defaults of the model, not rules of the text.

- **(a) As a force-terminate.** The root ends `failed` with the reason `TREE_TERMINATED`, and on a root that has already ended the route closes each non-terminal descendant still under it at once, answering `200` with the count closed.
- **(b) By the close rule.** The root ends `completed` or `cancelled` by the close rule, as a cascade closes a child, and an ended root behaves as in (a).
- **(c) Root only while live.** As (a), and the route answers `409 INVALID_STATE_TRANSITION` on a root that has already ended.

Recommendation: (a). The route is an administrator's stop of a tree, so its record should read as the force-terminate whose audit event it shares, and an administrator who finds a tree still running under an ended root needs the route most.

### O6. A resume the root's client asks for is undone when the session has no pod (SUSP-I1)

`DirectResumeHonoured` fails in every configuration with a suspension (counterexample R1 in `change.md`). The root's client resumes a `blocked` session, or a `suspended` one under a still-paused ancestor, through `POST /resume` or a message. With a pod the session becomes `idle` and stays live, which SLR-4 allows ("a resume that the root's client did not request for that session" is the case that lands in `blocked`). Without a pod the SL-2 row moves it to `resume_pending`, and when its restore commits, the `resuming` row lands it in `blocked` under its nearest paused ancestor, because SLR-4 lists "a restore commit" without the exception. The same happens to a session the root's client resumed with a pod if that pod is lost before the ancestor resumes. The root's client sees its resume accepted and the session paused again.

- **(a) Record the requested resume.** The row gains `resume_requested`, set when the root's client lifts the session's own pause and cleared when something pauses the session again; a restore commit lands such a session live, and SLR-4 and the `resuming` row of SL-2 name the exception.
- **(b) Refuse the resume under a paused ancestor.** `POST /resume` and a message from the root's client to a session with a paused ancestor answer `409 ANCESTOR_PAUSED` naming the suspension root, so only the suspension root's resume reaches a descendant. This removes the direct resume of a descendant that SL-2 and SLR-4 now allow.
- **(c) State the limit.** SL-2 and the client guide state that a direct resume of a session without a pod lasts only until its restore commits.

Recommendation: (a). It keeps the direct resume that SL-2 allows and makes its outcome the same with and without a pod, at the cost of one row field that the restore commit reads.

### O7. A leave leaves descendants paused under a session outside their tree (DTREE-I2)

`BlockedUnderPausedAncestor` fails in `chain3_leave` and `chain4_leave` (counterexample R2 in `change.md`). SL-4 "Sessions that left their parent" recomputes the nearest paused ancestor of the session that leaves, and of no other session. A descendant of the leaving session that is `blocked` with a `paused_ancestor_id` above the leaving session keeps that pointer: after a release it names a session in another tree, and after a transfer past a paused session it names a session that is no longer an ancestor. Such a descendant exists whenever the leaving session was not paused itself when the suspension reached the descendant, for example while it was `resume_pending` or `resuming`, or after the root's client resumed it directly. The descendant then stays `blocked` while every session above it is live, until the former ancestor resumes, when the tree resume reaches across trees to resume it, or until `maxSuspendedSessionSeconds` expires it.

- **(a) Recompute the subtree.** A leave recomputes the nearest paused ancestor of every `blocked` session in the leaving session's subtree whose `paused_ancestor_id` names a session outside the subtree, in the same write, and resumes through resume admission each one left with none.
- **(b) Refuse the leave.** `session.transfer` and `session.release` refuse a session that has a `blocked` descendant whose `paused_ancestor_id` names a session outside its subtree, with `409 SESSION_PAUSED`.

Recommendation: (a). The rule of SL-4 already recomputes the leaving session; extending it to the subtree keeps every `paused_ancestor_id` an ancestor, which the tree resume and backlog resume rely on, and a leave stays possible whatever the subtree's state.

### O8. A parent's transfer lifts a pause that only the root's client may lift (DTREE-I3)

`OnlyRootClientLifts` fails in `chain4_leave` (counterexample R3 in `change.md`). The root's client suspends a, so a's child b and b's children are `blocked` under a. The root's client resumes b directly, which SL-2 allows, and b's runtime creates c, which lands `blocked` under a (SLR-4). b then transfers c with `session.transfer`. SL-4 "Sessions that left their parent" re-points a transferred `blocked` session to its nearest paused ancestor on the path from the root, "and when that path holds none it resumes through resume admission": c's new path is the root alone, so c resumes while a is still suspended. SL-4 "Who suspends and who lifts" states that only the root's client lifts a suspension, and SLR-5 that no session in the tree moves a session out of `blocked`.

- **(a) A parent may not leave a `blocked` child.** `session.transfer` and `session.release` by a parent refuse a child in `blocked` with `409 SESSION_PAUSED`; the root's client's REST routes still move it, and a transfer by the root's client that leaves no paused ancestor resumes the session as SL-4 states.
- **(b) The transferred session stays paused.** A transferred `blocked` session whose new path holds no paused ancestor becomes `suspended` with `suspension_cause: client`, as a released one does, so only the root's client resumes it.
- **(c) State the exception.** SL-4 and SLR-5 state that a parent's transfer of a `blocked` child whose new path holds no paused ancestor resumes it.

Recommendation: (b). It keeps the leave available to a parent, keeps the rule that only the root's client lifts a pause, and treats a transfer and a release of a `blocked` session alike.

### O9. Is `maxParallelChildren` a bound at every instant, or a check at creation? (ADMIT-I1)

`ParallelismHolds` fails in `fan3_admission` (counterexample R4 in `change.md`). SL-10 "A parent's parallelism" counts a child in every state the caps count and in `blocked`, and not in `dormant` or `suspended`. A parent with `maxParallelChildren: 1` whose child a is `dormant` creates child b, which the count admits. The root's client then suspends the parent, and a becomes `blocked`, which counts, so the parent has two counted children. The same happens when a `dormant` or `suspended` child wakes or resumes, because resume admission runs the tenant's caps and not the parent's parallelism (SL-10 **Resume admission**, open decision SL-Q12 of 0146). The text states the count and the refusal at `session.create`, and does not say whether the bound holds afterwards.

- **(a) A check at creation.** SL-10 states that `maxParallelChildren` limits the children a parent creates while the count is at the bound, and that a child that wakes, resumes, or is blocked from `dormant` or `suspended` may take the count above it.
- **(b) A bound at every instant.** Resume admission of a child also runs its parent's parallelism, holding a woken child's message with `resume_refused` and leaving a refused child `blocked`, and a tree suspension leaves a `dormant` child `dormant`, as an external-limit suspension does.
- **(c) Count `dormant` and `suspended` children.** The count includes every non-terminal state, so the bound holds at every instant and a parent's idle helpers hold places.

Recommendation: (a). The tenant's caps and the tree's session budget already bound what a tree runs at once, so the caveat needs to hold where the parent decides, at creation, and (b) would add a second admission check, and a new way for a wake to be refused, to every child's restore. SL-Q12 of 0146, which asks which gates resume admission runs, is the place to record it.

### O10. A leave that commits between a parent's end and the cascade escapes the cascade (DTREE-I5)

`ForceReachesDescendants` and `ParentEndReachesChildren` fail in `chain3_end_leave` (counterexample R6 in `change.md`). An operator force-terminates a, which ends `failed` and is to close its descendants at once whatever `onParentEnd` holds (SLR-31). The cascade closes each child in a write of its own after the parent's terminal write, as `cascadeToChildren` in `pkg/gateway/core/session/sessionserver/usage.go` does today. Before it reaches a's child b, the root's client releases b with `POST /v1/sessions/{id}/release`, which 0166 DPR-16 allows while the root's grant is present; b becomes a root, so no cascade reaches it and b keeps running. A transfer, which re-links b to the live root, does the same, and so does a leave racing a `close_all` cascade of an ordinary end. SL-9 states that a transferred session is no longer its former parent's child and leaves its former parent's reach, and it does not say whether a leave may commit after the former parent has ended.

- **(a) Refuse a leave from an ended parent.** `session.transfer`, `session.release`, and the root's client's routes refuse a session whose parent link names a terminal session, with `409 INVALID_STATE_TRANSITION`, so `onParentEnd`, a force-terminate, and a tree terminate decide the fate of every child the parent had when it ended.
- **(b) Refuse it only after a force-terminate or a tree terminate.** As (a), only when the ended parent, or an ended ancestor between the session and the root, was force-terminated or tree-terminated, so a `let_finish` child can still be taken over by the root's client.
- **(c) State the race.** SL-9 states that a leave committed before the cascade's write takes the session out of the cascade.

Recommendation: (b). A force-terminate and a tree terminate are an operator's or an administrator's stop, which SLR-31 makes absolute, while under `let_finish` taking over a child that is finishing its turn is the use the root's client's transfer serves. Under `close_all` the window is the cascade's own latency, and (b) can be widened to (a) if the owner prefers one rule.

## Decisions

1. **0146 states the whole state machine before the model is built (O3).** As the first part of 0175.S1, the rules that change 0166 adds to the session state machine move into 0146 as revisions of SL-8 to SL-12: `onParentEnd` as a security caveat a child may only make stricter, the force-terminate that closes a tree's descendants at once, the tree terminate for a `tenant-admin`, `session.set_limits` raising a live child's age, the `ended` event of an expiry following the parent's `wakeOn`, a parent's parallelism counting `blocked` children, and recovery with no ordering across a tree. 0166 keeps its operations and a pointer to each rule, and each conflict the move finds becomes an open decision. Owner's answer to LM-1 (open decision O3) in chat, 2026-11-26, option (a), with the words "LM-1: ok". The option not chosen was (b), the model reading 0146, 0166, and 0135 MD-22 together.
2. **The modelling tool, and the model kept for reruns (O1).** TLA+ with the TLC model checker, reusing the runner and the pinned `tla2tools.jar` of change 0169. The model is stored under `tests/formal/sessionlifecycle/` with its module, one configuration file per scenario, a `run.sh` that runs every configuration, and a `README.md` that states what the model covers, the properties in plain words, how to run it, and its results, as `tests/formal/tokenconcurrency/` does, so that a later change that edits a rule the model describes updates the model and reruns it. The `formal/` entry of `tests/README.md` names the model. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to the recommendation of TLA+, and "Store the TLA+ models so that we can update and re-run them later if we have to make changes". The options not chosen were Alloy, as change 0165 used, and a model-based property test in Go.
3. **Program membership (O2).** The change stays outside the v1 program, as change 0172 does, and "Formal verifications owed before M3" in [the v1 program](../programs/v1.md) tracks it as FV-1. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to that recommendation. The option not chosen was membership in milestone M3.
