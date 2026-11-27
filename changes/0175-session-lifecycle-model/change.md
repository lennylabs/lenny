# A formal model of the session lifecycle

Status: `In progress`, revision 4, 2026-11-27.

## Problem

Change 0146 (session lifecycle, [`changes/0146-session-lifecycle/`](../0146-session-lifecycle/change.md)) defines the session state machine that the v1 program builds in M3: the states of "SL-1", the transitions of "SL-2", suspension and the open turn ("SL-3"), tree suspension ("SL-4"), messages to a waiting or paused session ("SL-5"), idle pods and dormancy ("SL-6"), closing ("SL-7"), parents and children ("SL-8" and "SL-9"), admission, caps, and timers ("SL-10"), and the recovery of child events ("SL-12"). Change 0166 (delegation as session primitives, on branch `change/0166-delegation-primitives`) adds the delegation operations that move sessions through those states. The state machine spans several gateway replicas, the adapter, the controllers, and a tree of sessions, so an ordering of events that no section states on its own can leave a session in a state no rule leaves, or a tree in which a parent and a child disagree.

The owner asked on 2026-11-25 for a comprehensive review of the state machine with a formal validation, as prep work for M3, before the steps of 0146 are built. Changes 0165 (an Alloy model of the token rules) and 0169 (a TLA+ model of their concurrent orderings) are the precedent: each counterexample their models found was confirmed against the text and the code and became an open decision for the owner.

## Scope

The change reviews the state machine of 0146 as 0166 extends it, and builds a formal model of it (TLA+ with TLC, as 0169 does, unless decision O1 settles another tool) that checks the properties the review writes down: the safety properties, such as a session holding one state, every transition taken only from the states its rule names, and a terminal state never left, and the liveness properties, such as every suspended tree resuming or closing and every child event reaching its parent.

It leaves out:

- Fixing the state machine. Each counterexample is confirmed against the text, written as a scenario, and reported to the owner as an open decision. The fix lands in 0146, in 0166, or in another change.
- Timing bounds. The model states orderings; a timer is modelled as an event that may fire, and the values of the timers are outside what it checks.

## Ordering

The change starts after branch `change/0166-delegation-primitives` merges into `main`, so the model reads the delegation operations as they land. The owner set this order on 2026-11-25. Change 0166 merged into `main` as records with merge `6bc1c2859`, so the precondition holds, and step 0175.S1 started on 2026-11-27. Under decision 1 the step first moved the state-machine rules of 0166 into 0146 (0146 revision 24, 0166 revision 19), and the model reads 0146 as revised.

## Terms

| Term | Meaning |
| :-- | :-- |
| Model | The TLA+ specification of the session state machine this change adds under `tests/formal/`. |
| Counterexample | A trace of the model that breaks one of the properties. |
| Configuration | A `.cfg` file of the model that fixes the planned tree, the suspension causes in play, the cap, and the bounds on suspensions, pod losses, leaves, kills, and crashes. |
| Root's client, nearest paused ancestor, live state, paused state | As 0146 "Terms" defines them. |

## High-level design

The model describes a tree of sessions and the parties that act on it: the root's client, through the REST routes; each parent's runtime, through the platform operations of 0166; the gateway, which commits setups and restores, runs the idle clock and the timers, resumes trees, sweeps partial suspensions, and applies `onParentEnd`; an operator, who force-terminates a session; and a tenant administrator, who terminates a tree. Each step is one guarded row write of the gateway, or one call that 0146 says returns once its rows are written. Time is not modelled: every timer is a step that may fire while its rule allows it, so the model checks every ordering of the events and none of the durations.

TLC explores every ordering of these steps within each configuration's bounds, over trees of two to four sessions (chains and a fan), and either finds no behavior that breaks a property or returns a shortest one that does. The properties are those of "Detailed design" below, each written first in plain words and then as an invariant, an action property, or a liveness property.

Each counterexample is checked against 0146, 0166, and the specification and code on `main`. A modelling error is fixed in the model. A confirmed gap becomes an open decision of this change and an issue, and the model keeps the rule as written until the change that carries the fix edits it.

The model, its configurations, its runner, and its README live under `tests/formal/sessionlifecycle/`, laid out as `tests/formal/uploadtokens/` is, so that a later change to a rule the model describes updates the model and reruns it (decision 2).

## Requirements

None. The change adds a model and tests of the specification and changes no requirement.

## Current state

The model was read on 2026-11-27 against:

- **Change 0146 at revision 24** and **change 0166 at revision 19**, on this step's branch. 0146 states the states of SL-1, the transitions of SL-2, the suspension of the open turn (SL-3), the tree suspension (SL-4), the messages to a waiting or paused session (SL-5), dormancy (SL-6), the close rule and the hand-off (SL-7), the child events (SL-8), the cascade with the ordering of `onParentEnd`, the force-terminate, and the tree terminate (SL-9), admission, the parallelism count, and the timers with a child's age (SL-10), and recovery with no ordering (SL-12). 0166 states the platform operations, the leaves by `session.transfer` and `session.release` ("Leaving the parent" in DP-2, DPR-16), and the tree terminate route.
- **The specification on `main` at `cf4cb5480`.** §7.2 lists the states `created` to `starting`, `running` with the sub-state `input_required`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`, and the terminal states; it has no `idle`, `dormant`, or `blocked`. §6.2 suspends one session on `interrupt_request` and does not cascade to children. §8.10 applies `cascadeOnFailure` (`cancel_all`, `await_completion`, `detach`) at a parent's terminal transition. §24.11 force-terminates one session to `failed` with `FORCE_TERMINATED`. §15.1 has no tree terminate route.
- **The code on `main` at `cf4cb5480`.** `pkg/api/v1/session/session.go` defines the states of §7.2. `pkg/gateway/core/session/sessionserver` holds the suspension (`handleInterrupt` in `interrupt.go`, `transitionInterrupt` in `session_transitions.go`), the resume (`handleResume` in `resume.go`), the terminate that writes `completed` (`transitionTerminate`), the pod release of a suspended session (`suspend_release.go`), the backlog resume (`suspended_backlog.go`), the force-terminate (`ForceTerminator` in `force_terminate.go`), and the cascade (`cascadeToChildren` and `cancelNodeGuarded` in `usage.go`), which cancels each child in a write of its own from a listing taken after the parent's terminal write. The watchdog in `pkg/gateway/core/session/watchdog/watchdog.go` runs the idle suspension and the timers. Delegation is the gateway-hosted tool `lenny/delegate_task` in `pkg/gateway/platformtools/mcptools`.

None of the states `idle`, `dormant`, or `blocked`, the tree suspension, the close rule, `onParentEnd`, the tree terminate, or the leaves is built: steps 0146.S1, 0146.S2, 0146.S4, 0166.S1, and 0166.S2 build them. The model therefore checks the design, and `tests/formal/sessionlifecycle/README.md` names, for each action, the text it follows and the code that carries it today or "not built".

## Detailed design

`tests/formal/sessionlifecycle/README.md` maps each action of the model to the text and the code, and lists the abstraction choices and what the model leaves out. This section states the properties and the choices behind them.

### LC-1 Properties in plain words

The review of 0146 as revised wrote these properties before the model was built. Each names the rule it comes from.

Safety, which holds in every reachable state or step:

- **P1.** The parent links form a forest: no session is its own ancestor.
- **P2.** No session leaves a terminal state (SL-1).
- **P3.** Every change of state is a transition that the SL-2 table lists.
- **P4.** A `blocked` session's `paused_ancestor_id` names one of its ancestors ("Row fields" in SL-1). **P4b.** No `blocked` session is left under a terminal session that is not `ended_suspended` (SLR-28 as written).
- **P5.** No session is live while it has a paused ancestor, except a session whose own pause the root's client lifted and a `dormant` session that an external-limit suspension left `dormant` (SLR-4, L42).
- **P6.** A resume or a message by which the root's client lifted a session's own pause is not undone: the session does not return to `blocked` until something pauses it again (SL-2, SLR-4).
- **P7.** Only the root's client lifts a pause: a session leaves `suspended` or `blocked` for a non-terminal state only by a step of the root's client, or by the gateway's tree resume after its paused ancestor entered a live state (SL-4 "Who suspends and who lifts", SLR-5).
- **P8.** A suspension of a `running` session cuts its turn and records the resume notice, so no paused session holds an open turn (SL-3, SLR-3, SLR-7).
- **P9.** An `idle` session does not become `dormant` while a descendant is `running` (SLR-9).
- **P10.** The sessions in the counted states never exceed the tenant's cap (SLR-12).
- **P11.** A parent's children in the counted states and in `blocked` never exceed `maxParallelChildren` (SL-10 "A parent's parallelism").
- **P12.** A close records `completed` only for a session with no open turn whose last turn ended with a message, and `cancelled` otherwise (SLR-8).
- **P13.** `onParentEnd` only narrows down the tree: a child of a `close_all` session is `close_all` (SLR-32).
- **P14.** A session in `awaiting_client_action` does not expire while an ancestor is suspended (SLR-21).
- **P15.** A terminal session's inbox is closed, so no child event opens a turn in it (SLR-10).
- **P16.** A handed-off root ended `completed`, and its successor is a root (SLR-30).
- **P17.** A parent acts on a child only while the parent runs, so a paused session acts on no other session (SLR-13).

Liveness, under the fairness the README states:

- **L1.** Every child of a terminal parent ends: at once under `close_all`, and under `let_finish` when its turn ends or at `cascadeTimeoutSeconds` (SL-9, SLR-31).
- **L2.** Every descendant of a force-terminated or tree-terminated session ends, whatever `onParentEnd` holds (SLR-31).
- **L3.** A session `blocked` under an ancestor that entered a live state leaves `blocked`, or moves under another paused ancestor (SL-4 "Resuming a tree", SLR-6).
- **L4.** A live session left under a paused ancestor by a partial suspension is paused by the backlog sweep (SLR-20).

### LC-2 What the model abstracts

A suspension call writes the suspension root's row and each descendant's row in one step, because SL-4 says the call returns once those rows are written; a coordinator crash is a call that writes the root's row alone, which the sweep finishes. The cascade closes one child per step, as `cascadeToChildren` does, so a child can act between its parent's end and its own close. Resume admission and creation admission are one check against the tenant's cap. Timers are steps that may fire, so a raise of `perChildMaxAge` by `session.set_limits` changes no state of the model. Budget suspension, the egress halt, checkpoints, questions, approvals, client tools, and the `sealOnly` and `none` checkpoint policies are left out; the README lists why.

### LC-3 Configurations

The configurations split the concerns, because one configuration with every party, two suspensions, a leave, a kill, and a crash grows past what TLC explores in ten minutes with two workers. "Model results" lists each with its bounds.

## Specification and non-specification changes

None to the specification. The model and its runner are non-specification files under `tests/formal/`. Under decision 1 the step edits the records of changes 0146 (revision 24) and 0166 (revision 19), which "Ordering" names. A confirmed counterexample becomes an open decision of this change, and its fix lands in 0146, 0166, or another change.

## Documentation

The `formal/` entry of `tests/README.md` names the new model.

## Tests

The model's checks are the tests. Their results are recorded in this file per configuration, as 0169 records its results.

## Model results

Step 0175.S1 ran every configuration of `tests/formal/sessionlifecycle/` on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run, in the `eclipse-temurin:21-jdk` container the runner starts, against the sources "Current state" names. Every run finished. TLC stops at the first violation, so each safety configuration was repeated without each violated check until a run explored the whole state space. `chain4_leave` lists only the checks its counterexamples break, because its state space grows past what TLC explores in ten minutes; `chain3_end_leave` was run once per liveness property and once with its invariants alone.

### Results by property

| Property | Result |
| :-- | :-- |
| P1 to P3, P5, P8 to P10, P12, P13, P15 to P17 | Hold in every configuration that lists them. |
| P4 A `blocked` session points to an ancestor | Fails wherever a leave is allowed (R2). |
| P4b SLR-28 as written | Fails in every configuration with a suspension (G1); the safety net of SL-9 covers it, and L3 holds. |
| P6 A direct resume is not undone | Fails in every configuration with a suspension (R1). |
| P7 Only the root's client lifts a pause | Fails in `chain4_leave` (R3); holds in the other configurations, which have no session between the root and a parent that can transfer. |
| P11 Parallelism | Fails in `fan3_admission` (R4), the one configuration in which a parent has two children. |
| P14 SLR-21 | Holds under its own reading, and fails under the age rule that decision 1 moved into 0146 (R5). |
| L1, L2 The cascade reaches every child | Hold without leaves; fail when a leave races the cascade (R6). |
| L3, L4 The tree resume and the sweep | Hold. |

### Runs

The distinct states and time are those of the last run of each configuration, which explored the whole state space.

| Configuration | Bounds | Distinct states | Time | Violated |
| :-- | :-- | --: | --: | :-- |
| `chain2_client` | r, a; two suspensions, one pod loss; cap 2; both root values; expiry | 308,647 | 24 s | P4b (G1), P6 (R1) |
| `chain3_client` | r, a, b; one suspension, one pod loss; cap 3; root `close_all`; expiry | 5,697,401 | 13 min 33 s | P4b (G1), P6 (R1) |
| `fan3_admission` | r with a and b; one suspension, one pod loss; cap 2, parallelism 1; root `close_all`; expiry | 3,532,120 | 7 min 30 s | P11 (R4), P4b (G1), P6 (R1) |
| `chain2_extlimit` | r, a; client and external-limit suspensions, two in all; one pod loss; cap 2; both root values; expiry | 512,719 | 39 s | P4b (G1), P6 (R1) |
| `chain3_extlimit` | r, a, b; one client or external-limit suspension; cap 3; root `close_all`; expiry | 1,664,783 | 3 min 51 s | P4b (G1), P6 (R1) |
| `chain3_leave` | r, a, b; one suspension, one leave or hand-off; cap 3; root `close_all` | 5,667,779 | 11 min 46 s | P4b (G1), P6 (R1), P4 (R2) |
| `chain4_leave` | r, a, b, c; one suspension, one leave; cap 3; root `close_all` | not explored to the end | | P4 (R2), P7 (R3) |
| `chain2_childage` | r, a; one suspension, one pod loss; the age rule; expiry outside `wakeOn` | 121,458 | 5 s | P14 (R5, expected) |
| `chain2_end` | r, a; one suspension, one kill; `FairSpec` | 41,355 | 15 s | None |
| `chain3_end` | r, a, b; one kill; `FairSpec` | 49,344 | 1 min 9 s | None |
| `chain3_end_leave` | r, a, b; one kill, one leave; `FairSpec` | 205,597 (invariants) | 12 s | L1, L2 (R6) |
| `chain2_resume` | r, a; two suspensions, one cut by a crash; `FairSpec` | 65,676 | 20 s | None |

`chain3_client` and `chain3_leave` run longer than the ten minutes the step aimed for; the two-suspension and `let_finish` cases moved to the chain of two for that reason, and a configuration with two suspensions on the chain of three reached 8,186,426 distinct states in 16 min 40 s during the step.

### Counterexamples

Each trace below was checked against 0146 at revision 24, 0166 at revision 19, and the specification and code on `main` at `cf4cb5480`. None of the mechanisms is built on `main`, so each is a gap in the design text, which the steps of 0146 and 0166 would build as written.

- **R1. A resume the root's client asks for is undone when the session has no pod (P6, confirmed gap).** In `chain2_client`, child a's turn is cut by the idle clock and a becomes `dormant`, with no pod; the root's client suspends r, and a becomes `blocked` under r. A `blocked` session that held its pod reaches the same state when the pod is released at the end of the hold or by a suspension that cannot take hold. The root's client resumes a with `POST /resume`, which SL-2 moves to `resume_pending` because a has no pod. The restore commits, and the `resuming` row of SL-2 lands a in `blocked` under r again, because SLR-4 lists "a restore commit" without the exception it gives "a resume that the root's client did not request for that session". With a pod, the same resume moves a to `idle`, and it stays live. Open decision O6 and [SUSP-I1](../../issues/SUSP/SUSP-I1-direct-resume-without-pod-lands-blocked.md).
- **R2. A leave keeps descendants paused under a session outside their tree (P4, confirmed gap).** In `chain3_leave`, child a of root r creates b, and the root's client suspends r, so a is `blocked` under r while b is still in setup. The root's client resumes a directly, which SL-2 allows, so a is `idle`. b's setup commits and lands `blocked` under r (SLR-4, step 4 of SL-4 "Steps"), because a is not paused. The root's client releases a with `POST /release`. SL-4 "Sessions that left their parent" recomputes the nearest paused ancestor of the leaving session alone, so b keeps `paused_ancestor_id` naming r, which is in another tree. b stays `blocked` while a runs, until r resumes and the tree resume reaches across trees, or until `maxSuspendedSessionSeconds`. `chain4_leave` reaches the same trace, and an exploration run reached it with a in `resume_pending` after a pod loss in place of the direct resume. Open decision O7 and [DTREE-I2](../../issues/DTREE/DTREE-I2-leave-keeps-descendants-paused-under-former-ancestor.md).
- **R3. A parent's transfer lifts a pause that only the root's client may lift (P7, confirmed gap).** In `chain4_leave`, the root's client suspends a, so b is `blocked` under a; it resumes b directly, which SL-2 allows; b's runtime creates c, which lands `blocked` under a (SLR-4); b's runtime transfers c with `session.transfer`. c's new path is r alone, which holds no paused ancestor, so SL-4 "Sessions that left their parent" resumes c through resume admission while a is still suspended. SL-4 "Who suspends and who lifts" and SLR-5 reserve the lift to the root's client. Open decision O8 and [DTREE-I3](../../issues/DTREE/DTREE-I3-parent-transfer-lifts-root-client-suspension.md).
- **R4. A parent's parallelism exceeds `maxParallelChildren` without a creation (P11, confirmed gap).** In `fan3_admission` with `maxParallelChildren: 1`, child a of r becomes `dormant`, which the count leaves out; r creates b, which the count admits; the root's client suspends r, and a, `dormant`, becomes `blocked`, which the count includes. The text checks the count at `session.create` and does not say whether the bound holds afterwards; a wake or a resume of a `dormant` or `suspended` child exceeds it in the same way. Open decision O9 and [ADMIT-I1](../../issues/ADMIT/ADMIT-I1-parallelism-bound-exceeded-by-wake-or-block.md).
- **R5. `perChildMaxAge` and SLR-21 disagree (P14, confirmed contradiction found by decision 1).** In `chain2_childage`, which reads the age rule as decision 13 of 0166 states it, child a loses its pod while running and its restore runs out of retries, so a is in `awaiting_client_action`; the root's client suspends r; a's `perChildMaxAge` passes and a expires, while SLR-21 states that a descendant in `awaiting_client_action` does not expire while an ancestor is suspended. The `ended` event of the expiry reaches r as `nowake`, because that configuration's `wakeOn` leaves out the kind `expired`. Under the reading of SLR-21 (`AgeStopsInACA = TRUE`), P14 holds in every configuration. Open decision O4 and [TIMEOUT-I1](../../issues/TIMEOUT/TIMEOUT-I1-child-age-and-aca-expiry-under-suspension-disagree.md).
- **R6. A leave that commits between a parent's end and the cascade escapes the cascade (L1, L2, confirmed gap).** In `chain3_end_leave`, an operator force-terminates a; before the cascade closes a's child b, the root's client releases b with `POST /release`, which 0166 DPR-16 allows while the root's grant is present; b becomes a root, and no cascade reaches it. A transfer, which re-links b to the live root, has the same effect, which the exploration runs of the step reached first. The run of L1 reaches the same with a non-retryable failure of a under `let_finish`. The cascade closes each child in a write of its own after the parent's terminal write, as `cascadeToChildren` does on `main`, so the window is in the design. Without leaves (`chain2_end`, `chain3_end`), L1 and L2 hold. Open decision O10 and [DTREE-I5](../../issues/DTREE/DTREE-I5-leave-racing-cascade-escapes-force-terminate.md).

### Differences between the text and itself

- **G1. SLR-28 claims more than its re-pointing gives (P4b).** In every configuration with a suspension, the root's client suspends r while a runs, so a is `blocked` under r; it lifts r with a message, so r is `idle`; before the tree resume reaches a, it cancels r. r ended from `idle`, so SLR-28's re-pointing, which applies when a `blocked` session ends, does not run, and a is `blocked` under a terminal session that is not `ended_suspended`, which SLR-28's "so no `blocked` session is left under ..." excludes. The safety net of SL-9 "A blocked session that ends", under which backlog resume recomputes such a session's nearest paused ancestor, resumes a, and L3 holds with it. The fix is a wording edit of SLR-28, which [DTREE-I4](../../issues/DTREE/DTREE-I4-slr28-overstates-repointing-of-blocked-sessions.md) records. It raises no decision.

### Gaps found by the review

- **The tree terminate's root state.** Neither 0146 nor 0166 states the terminal state the tree terminate records on the root, or what it does when the root has already ended while descendants still run. The model ends the root `failed`, as a force-terminate does, and applies the route to an ended root; both are model defaults. Open decision O5 and [DTREE-I1](../../issues/DTREE/DTREE-I1-tree-terminate-root-state-and-ended-root-unstated.md).

### Modelling errors fixed during the step

- The first version cleared a session's record of a direct resume when it became `dormant`, so a session the root's client resumed and that then went `dormant` read as live under a paused ancestor without an exemption. The record now clears when the session is paused again or woken by another sender.
- The first version let `cascadeTimeoutSeconds` expire a child in `awaiting_client_action`, which `let_finish` closes at once, and stated SLR-21 over an ended ancestor. The cascade timeout no longer applies to that state, and P14 reads a suspended ancestor that has not ended.
- The first version required the tree resume to be the step that lifts a `blocked` session whose paused ancestor had already resumed; a leave that resumes such a session is the step the tree resume would take, and P7 now accepts any step once the ancestor's pause is lifted.
- The first liveness runs let the root's client keep a `let_finish` child of an ended parent out of the states the cascade closes, by sending it a message at each turn end; the cascade timeout now runs in every state of such a child, as SL-9 states, and the cascade is strongly fair.