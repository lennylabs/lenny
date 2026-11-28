# Session lifecycle model

`SessionLifecycle.tla` is a TLA+ model of the session state machine of change 0146 (`changes/0146-session-lifecycle/`), as its revision 24 states it, with the rules that decision 1 of change 0175 moved into it from change 0166 (`changes/0166-delegation-primitives/`). Change 0175 (`changes/0175-session-lifecycle-model/`) builds the model, and its `change.md` records the results and the counterexamples.

The model is a design check. No tier of `lenny-test` runs it, and no Go test reads it. A change that edits a rule the model describes updates the model and reruns it, as decision 2 of change 0175 states.

## Sources the model was read against

| Source | Branch | Commit |
| :-- | :-- | :-- |
| Change 0146, revision 24 ("SL-1" to "SL-12", SLR-1 to SLR-32) | `change/0175.S1-session-lifecycle-model` | the commit that adds this model |
| Change 0166, revision 19 ("DP-1", "DP-2" "Leaving the parent", "DP-3", "DP-4") | `change/0175.S1-session-lifecycle-model` | the same commit |
| §6.2, §7.2, §8.10, §11.1, §15.1, and §24.11 of the specification, and the session server, watchdog, and orphan cleanup code | `main` | `cf4cb5480` |

None of the states `idle`, `dormant`, or `blocked`, the tree suspension, the close rule, `onParentEnd`, the tree terminate, or the leaves is built on `main`; steps 0146.S1, 0146.S2, 0146.S4, 0166.S1, and 0166.S2 build them. A change that lands a different version of these rules compares it with the revisions above and edits the model where they differ.

## Plain TLA+

The module is plain TLA+ rather than PlusCal. Most rules of the state machine are guards over the tree: the nearest paused ancestor of a session, the running descendants of an idle session, the counts of the concurrency caps and of a parent's parallelism, and the descendants a suspension blocks in one call. Each is an operator over the whole session table, which plain TLA+ states directly and PlusCal does not make shorter. One action is one guarded row write of the gateway, or one call that the text says returns after its row writes, so there are no intermediate labels for PlusCal to describe. Without a translation there is no committed translation to keep current, so `run.sh` has no translator step.

## What the model covers

### Sessions and the tree

Each configuration names a planned tree: `chain2` (r creates a), `chain3` (r creates a, a creates b), `fan3` (r creates a and b), or `chain4` (r, a, b, c in a chain). The root's client has created r, which waits in `idle`; every other session is created by the runtime of its planned parent with `session.create` while that parent is `running`. The parent link of a session is the link the cascade, the tree suspension, and SLR-9 follow; a transfer re-links a session to its root, and a release makes it a root.

A session row holds the fields of "Row fields" in SL-1 that a transition reads: the state, `suspension_cause`, `paused_ancestor_id`, `suspension_requested`, `ended_suspended`, `last_turn_end`, `resume_notice_due`, and whether the session holds a pod, and the session's `onParentEnd`, whether its parent is its client, whether `perChildMaxAge` applies, whether it ended under a force-terminate or a tree terminate, and `successorSessionId`. Its inbox is a set of message kinds: `wake` for a message that opens a turn or wakes the session, and `nowake` for a child event outside the parent's `wakeOn`. Two flags record history that the properties read: `direct`, set when the root's client lifted this session's own pause, and `ld`, set on a dormant session that an external-limit suspension left dormant (L42).

The setup states `created` to `starting` are one state, `starting`. A session that its parent has not created is `absent`.

### Actions, the text, and the code

The code column names what `main` has today. Every state, transition, and rule of the right-hand columns that the code column calls "not built" is a rule of changes 0146 and 0166 that their steps build.

| Action | Actor | Text | Code on `main` |
| :-- | :-- | :-- | :-- |
| `Create` | runtime of the parent | 0166 "DP-1" `session.create`, with `onParentEnd` equal to or stricter than the parent's (SL-9, SLR-32), the parallelism count, and creation admission (SL-10) | `lenny/delegate_task` in `pkg/gateway/platformtools/mcptools`; no `onParentEnd` ordering and no count of `blocked` |
| `StartCommit` | gateway | SL-2 `starting` rows, SLR-4 | The `starting → running` write in `pkg/gateway/core/session/sessionserver`; the landing in `blocked` is not built |
| `EndTurn`, `Complete`, `Output`, `Deliver` | runtime, gateway | SL-2 `running` and `idle` rows, the delivery points of SLR-26, the resume notice of SL-3 | `running → completed` in the session server; `idle` and the resume notice are not built |
| `IdleClock`, `WakeDormant` | gateway | SL-6, SLR-9, SLR-17, the `dormant` row of SL-5 | The idle suspension of `sweepIdle` and `idleSuspendable` in `pkg/gateway/core/session/watchdog/watchdog.go`, which writes `suspended`; `dormant` is not built |
| `PodLoss`, `PodRelease`, `RestoreStart`, `RestoreCommit`, `RetriesOut` | gateway | SL-2 retry and restore rows, SL-3 "After a pod loss", SLR-16, SLR-18 | `ReleaseSuspendedPod` in `suspend_release.go`, and the resume driver in `resume_driver.go`; the landing in `suspended` or `blocked` at the commit is not built |
| `RootSuspend` | root's client | SL-4 "Routes" and "Steps", SL-3 "Stopping the turn" | `handleInterrupt` in `interrupt.go` and `transitionInterrupt` in `session_transitions.go`, which suspend one session and no subtree |
| `RootResume`, `RootMsg` | root's client | SL-2 resume rows, SL-5 first column, L45 | `handleResume` in `resume.go`, delivery path 6 of §7.2 (any message resumes a suspended session) |
| `RootClose`, `RootCancel` | root's client | SL-7 close rule (SLR-8), `DELETE` | `transitionTerminate`, which writes `completed`, and `handleDelete`, in `session_transitions.go` |
| `ParentSend`, `ParentClose` | runtime of the parent | 0166 "DP-1" `session.send` and `session.close`, SL-5 second column, SLR-13 | The gateway-hosted delegation tools in `mcptools`; `session.send` and `session.close` are not built |
| `ParentTransfer`, `RootTransfer`, `ParentRelease`, `RootRelease`, `Handoff` | parent or root's client | 0166 "Leaving the parent" in "DP-2", DPR-16; SL-4 "Sessions that left their parent", SL-7 "Hand-off", SL-9 | Not built |
| `Repoint`, `AncestorLive` | gateway | SL-2 `blocked` rows, SL-4 "Resuming a tree", SL-9 "A blocked session that ends", SLR-6 | Not built |
| `Sweep` | gateway | SL-4 "A partial suspension", SLR-20 | `ResumeBacklog` and `BacklogRechecker` in `suspended_backlog.go` resume suspended sessions; the sweep that blocks descendants is not built |
| `ExtLimit` | gateway | SL-4 "External limit suspension", L42 | Not built |
| `Expire` | gateway | SL-10 "Timers" and "A child's age", SLR-21 | The watchdog sweeps in `watchdog.go`, among them `expireIdle`, `sweepResumePending`, and the `suspended` expiry |
| `Fail`, `ForceTerminate` | gateway, operator | SL-2, SL-9 "Force-terminate and tree terminate", SLR-31 | `ForceTerminator.ForceTerminate` in `force_terminate.go`, which writes `failed` with `FORCE_TERMINATED` and runs the terminal pipeline |
| `TreeTerminate` | tenant administrator | SL-9, SLR-31, the route of 0166 "DP-2" | Not built |
| `Cascade`, `CascadeTimeout` | gateway | SL-9 "Order" and "`onParentEnd`", SLR-10, SLR-25 | `cascadeToChildren` and `cancelNodeGuarded` in `usage.go`, which apply `cascadeOnFailure` level by level, cancelling each child, from a listing taken after each terminal write; the orphan cleanup in `pkg/gateway/platformtools/orphancleanup` |

### Abstraction choices

- **Atomic steps.** A suspension call writes the suspension root's row and every descendant's row in one step, because SL-4 "Routes" states that the call returns once they are written and "Across replicas" serializes each row with a compare-and-set. A coordinator crash between step 1 and step 2 of "Steps" is a suspension that writes the root's row alone (`MaxCrash`), which the backlog sweep finishes. The forwarded turn stops, the egress halt, the seal, and the checkpoint of SL-3 are not steps of their own: their outcome is the pod the paused session holds, and `PodRelease` releases it at any later point.
- **The cascade.** Each child of a terminal parent is closed in a step of its own, as `cascadeToChildren` closes one child per write, so a child can act between its parent's end and its own close.
- **Admission.** Resume admission and creation admission are one check against `Cap`, the tenant's concurrency cap over the counted states of SL-10. The budget admission of 0170 is left out. A message from the root's client that admission refuses is answered with `429` and changes nothing; a parent's message is held.
- **Time.** No clock is modelled. Each timer of SL-10 is an action that may fire while its rule allows it. `maxResumeWindowSeconds` is `RetriesOut`. A raise of `perChildMaxAge` by `session.set_limits` moves a deadline and changes no state, so the model covers it through `Expire`, which may fire at any instant of a child's life.
- **Messages.** Turn content is not modelled. A message that opens a turn is the inbox kind `wake`, and a child event outside `wakeOn` is `nowake`. The only `nowake` events are expiries when `WakeOnExpired` is `FALSE`.
- **The root's client.** One principal owns every root, as a released session remains a root of the same principal, so one actor acts as the root's client of every tree.
- **Turn ends.** An end-of-turn message that carries `error` is not modelled: every rule the model reads treats `last_turn_end: error` as it treats `cut`, so `cut` stands for both.
- **Defaults where the text is silent.** A `blocked` session that a leave leaves with no paused ancestor, and that resume admission then refuses, stays `blocked` as it was; the text does not say what happens to it. The tree terminate ends its root `failed` and also applies to a root that has already ended (open decision O5 of change 0175).

### Left out

- **Budget suspension** (L35 to L41, L44). 0170 owns its trigger and its room, and the model includes the two causes `client` and `external_limit`.
- **The egress halt, the seal, checkpoints, and helper runs** of SL-3 and SL-6, other than through the pod a session holds.
- **Questions, approvals, client tools, and exports.** The close rule's condition on an open call to a client's tool is subsumed by the open turn, as SL-7 states it is under the blocking interface.
- **`sealOnly` and `none` checkpoint policies**, under which a session never becomes `dormant` (SL-6).
- **The disk watch's end** (SL-2, L34), which is a `failed` end like `Fail`.
- **Several replicas.** Each step is one row write that its compare-and-set serializes, so the model has one gateway.
- **A transfer of a root's own child**, which changes only the session's client, since the parent link the model follows already names the root.

## Properties

TLC checks the invariants and the action properties of the safety configurations on every reachable state and step, and the liveness properties of the liveness configurations under `FairSpec`, which assumes weak fairness of `CascadeTimeout`, `Repoint`, and `Sweep`, and strong fairness of `Cascade`, whose `let_finish` case a child moves in and out of, and of `AncestorLive`, whose admission other sessions take and free.

| Property | Formula | What it states |
| :-- | :-- | :-- |
| P1 | `NoCycle` | The parent links form a forest. |
| P2 | `TerminalStays` | No session leaves a terminal state. |
| P3 | `LegalMoves` | Every change of state is a transition of the SL-2 table. |
| P4 | `BlockedUnderPausedAncestor` | A blocked session's `paused_ancestor_id` names one of its ancestors. |
| P4b | `NoBlockedUnderEndedSession` | No blocked session is left under a terminal session that is not `ended_suspended` (SLR-28 as written). |
| P5 | `LiveUnderPausedExempt` | No session is `running`, `idle`, or `dormant` while it has a paused ancestor, except a session whose own pause the root's client lifted and a dormant session an external limit left dormant (SLR-4). |
| P6 | `DirectResumeHonoured` | A resume or message by which the root's client lifted a session's own pause is not undone: the session does not return to `blocked` until something pauses it again. |
| P7 | `OnlyRootClientLifts` | A session leaves `suspended` or `blocked` for a non-terminal state only by a step of the root's client, or by the tree resume after its paused ancestor entered a live state (SL-4 "Who suspends and who lifts", SLR-5). |
| P8 | `SuspensionCutsTurn` | A suspension cuts the open turn and records the resume notice (SL-3, SLR-3). |
| P9 | `IdleKeepsPodForRunningDescendant` | An idle session does not become dormant while a descendant runs (SLR-9). |
| P10 | `CapsHold` | The sessions in the counted states never exceed the cap (SLR-12). |
| P11 | `ParallelismHolds` | A parent's children in the counted states and in `blocked` never exceed `maxParallelChildren` (SL-10 "A parent's parallelism"). |
| P12 | `CloseRule` | A close records `completed` only for a session with no open turn whose last turn ended with a message (SLR-8). |
| P13 | `OnParentEndOrder` | A child of a `close_all` session is `close_all` (SLR-32). |
| P14 | `ACANoExpiryWhileAncestorPaused` | A session in `awaiting_client_action` does not expire while an ancestor is suspended (SLR-21). |
| P15 | `InboxClosed` | A terminal session's inbox is closed (SLR-10). |
| P16 | `HandoffSuccessor` | A handed-off root ended `completed` and its successor is a root (SLR-30). |
| P17 | `ParentActsOnlyWhenRunning` | A parent's operation on a child happens only while the parent runs, so a paused session acts on no other session (SLR-13). |
| L1 | `ParentEndReachesChildren` | Every child of a terminal parent ends (SL-9, SLR-31). |
| L2 | `ForceReachesDescendants` | Every child of a force-terminated or tree-terminated session ends (SLR-31). |
| L3 | `TreeResumeReachesBlocked` | A session blocked under an ancestor that entered a live state, or ended without `ended_suspended`, leaves `blocked` or moves under another paused ancestor (SLR-6). |
| L4 | `PartialSuspensionCompletes` | A live session under a paused ancestor that is not exempt is paused by the sweep (SLR-20). |

`TerminalStays`, `LegalMoves`, `SuspensionCutsTurn`, `IdleKeepsPodForRunningDescendant`, `CloseRule`, `OnParentEndOrder`, `InboxClosed`, and `ParentActsOnlyWhenRunning` hold by the construction of the guards they restate; they pin each guard to the rule it carries.

## Configurations

Every configuration sets the planned tree (`Shape`), the suspension causes in play (`Causes`), the tenant's cap (`Cap`), `maxParallelChildren` (`MaxPar`), the bounds on suspension calls and external-limit signals (`MaxSuspend`), pod losses (`MaxPodLoss`), leaves (`MaxLeave`), force-terminates, tree terminates, and non-retryable failures together (`MaxKill`), and suspensions a coordinator crash cuts (`MaxCrash`), whether a root may hand off (`AllowHandoff`), whether the parents' `wakeOn` holds the kind `expired` (`WakeOnExpired`), the reading of SLR-21 (`AgeStopsInACA`), the root's `onParentEnd` values (`RootOpes`), whether the actor of a step is recorded (`TrackActor`, which `OnlyRootClientLifts` needs), and whether the expiry timers fire (`WithExpiry`).

| Configuration | Tree | Causes | Cap, parallelism | Suspensions, pod losses, leaves, kills, crashes | Other | Checks |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `chain2_client` | r, a | client | 2, 1 | 2, 1, 0, 0, 0 | both root values; expiry | P1 to P17 |
| `chain3_client` | r, a, b | client | 3, 1 | 1, 1, 0, 0, 0 | root `close_all`; expiry | P1 to P17 |
| `fan3_admission` | r with a and b | client | 2, 1 | 1, 1, 0, 0, 0 | root `close_all`; expiry | P1 to P17 |
| `chain2_extlimit` | r, a | client, external limit | 2, 1 | 2, 1, 0, 0, 0 | both root values; expiry | P1 to P17 |
| `chain3_extlimit` | r, a, b | client, external limit | 3, 1 | 1, 0, 0, 0, 0 | root `close_all`; expiry | P1 to P17 |
| `chain3_leave` | r, a, b | client | 3, 1 | 1, 0, 1, 0, 0 | hand-off; root `close_all`; no expiry | P1 to P17 |
| `chain4_leave` | r, a, b, c | client | 3, 1 | 1, 0, 1, 0, 0 | root `close_all`; no expiry | P1, P4, P7 (counterexamples only) |
| `chain2_childage` | r, a | client | 2, 1 | 1, 1, 0, 0, 0 | `AgeStopsInACA = FALSE`, `WakeOnExpired = FALSE`; both root values; expiry | P2, P14, P15 |
| `chain2_end` | r, a | client | 3, 1 | 1, 0, 0, 1, 0 | both root values; `FairSpec` | P1, P13, L1, L2 |
| `chain3_end` | r, a, b | client | 3, 1 | 0, 0, 0, 1, 0 | both root values; `FairSpec` | P1, P13, L1, L2 |
| `chain3_end_leave` | r, a, b | client | 3, 1 | 0, 0, 1, 1, 0 | both root values; `FairSpec` | P1, P13, L1, L2 |
| `chain2_resume` | r, a | client | 3, 1 | 2, 0, 0, 0, 1 | root `close_all`; `FairSpec` | P1, P13, L3, L4 |
| `chain3_twosusp` | r, a, b | client | 3, 1 | 2, 1, 0, 0, 0 | root `close_all`; expiry | P1 to P17 except P4b and P6 |
| `chain3_letfinish` | r, a, b | client | 3, 1 | 1, 1, 0, 0, 0 | both root values; expiry | P1 to P17 except P4b and P6 |
| `chain4_safety` | r, a, b, c | client | 3, 1 | 1, 0, 1, 0, 0 | root `close_all`; no expiry | P1 to P17 except P4, P4b, P6, and P7 |

The safety configurations use `VIEW View`, which leaves the actor of the last step out of a state's identity and writes every terminal state as one, because no step reads which terminal state a session ended in. The liveness configurations set `TrackActor = FALSE` and turn off the expiry timers, which keeps their state graphs small enough for TLC's liveness check. A configuration with two suspension calls on the chain of three sessions grows past eight million distinct states and runs for more than fifteen minutes with two workers, and one with both root values and the expiry timers on the chain of three passes nine million, which is why the two-suspension cases and both root values run on the chain of two, and the safety configurations on the chains of three and four run with a root under `close_all`. The configurations `chain3_twosusp`, `chain3_letfinish`, and `chain4_safety` were added on 2026-11-27 to run those cases on the larger chains with 8 workers; they leave out the checks that a counterexample already breaks while its fix has not landed (P4b, P6, and, on the chain of four, P4 and P7), as `.claude/rules/working-practices.md` states. Under a `close_all` root every session is `close_all` (SLR-32), so the `let_finish` cases run in the `chain2_*` configurations and in the liveness configurations on the chain of three.

## Results

Step 0175.S1 ran every configuration on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run. TLC stops at the first violation, so each safety configuration was run again without each violated check until a run explored the whole state space; the states and time are those of that run. `chain4_leave` lists only the checks its counterexamples break and was not explored to the end. `chain3_end_leave` was run once per liveness property and once with its invariants alone.

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `chain2_client` | 308,647 | 24 s | P4b (G1), P6 (R1) |
| `chain3_client` | 5,697,401 | 13 min 33 s | P4b (G1), P6 (R1) |
| `fan3_admission` | 3,532,120 | 7 min 30 s | P11 (R4), P4b (G1), P6 (R1) |
| `chain2_extlimit` | 512,719 | 39 s | P4b (G1), P6 (R1) |
| `chain3_extlimit` | 1,664,783 | 3 min 51 s | P4b (G1), P6 (R1) |
| `chain3_leave` | 5,667,779 | 11 min 46 s | P4b (G1), P6 (R1), P4 (R2) |
| `chain4_leave` | not explored to the end | | P4 (R2), P7 (R3) |
| `chain2_childage` | 121,458 | 5 s | P14 (R5, expected under this reading) |
| `chain2_end` | 41,355 | 15 s | None |
| `chain3_end` | 49,344 | 1 min 9 s | None |
| `chain3_end_leave` | 205,597 (invariants) | 12 s | L1, L2 (R6) |
| `chain2_resume` | 65,676 | 20 s | None |

| Property | Result |
| :-- | :-- |
| P1, P2, P3, P5, P8, P9, P10, P12, P13, P15, P16, P17 | Hold in every configuration that lists them. |
| P4 | Fails wherever a leave is allowed (R2), and holds in the other configurations. |
| P4b | Fails in every configuration with a suspension (G1): SLR-28 as written. The safety net of SL-9 resumes the session, and L3 holds. |
| P6 | Fails in every configuration with a suspension (R1). |
| P7 | Fails in `chain4_leave` (R3), where a parent transfers a child past a suspended session, and holds in the other configurations. |
| P11 | Fails in `fan3_admission` (R4), the one configuration in which a parent has two children. |
| P14 | Holds under the reading of SLR-21 (`AgeStopsInACA = TRUE`), and fails under the age rule of decision 13 of 0166 (R5). |
| L1, L2 | Hold without leaves, and fail when a leave can race the cascade (R6). |
| L3, L4 | Hold. |

### Larger runs of 2026-11-27

The three larger configurations ran on 2026-11-27 with the same TLC release and jar, on the host's Java (OpenJDK 11.0.26), with 8 workers and a 12 GB heap. A run on the owner's workstation is stopped at one hour, or at 30 minutes when TLC's queue is still growing.

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `chain3_twosusp` | 16,806,904 | 12 min 13 s | None |
| `chain3_letfinish` | 22,749,016 | 15 min 8 s | None |
| `chain4_safety` | not explored to the end: stopped at 30 min with 59,444,646 distinct states, depth 21, and 24,238,740 states on the queue | | None up to the stop |

`chain4_safety` is owed on a larger machine: its queue grew through the 30 minutes, which puts the full run well past one hour on the workstation.

`changes/0175-session-lifecycle-model/change.md` states each counterexample, its trace, and the open decision it raised. The model is checked for counterexamples only; no separate run confirmed that each holding property is reachable rather than vacuous, beyond the traces of the counterexamples, which reach `blocked`, `suspended`, `dormant`, `resume_pending`, `awaiting_client_action`, a release, a transfer, a force-terminate, and an expiry.

## Running it

The runner needs the `tla2tools.jar` of TLC `v1.7.4`, from `https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar`, and Java 11 or later. It checks the jar's SHA-256 against `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88` and stops when it differs. It runs TLC on the host's Java: `TLC_JAVA` names the `java` binary, else `$JAVA_HOME/bin/java`, else `java` on the `PATH`. `TLC_DOCKER=1` runs TLC in an `eclipse-temurin:21-jdk` container with no network instead, with the model directory and the jar mounted read-only.

```sh
tests/formal/sessionlifecycle/run.sh <path-to-tla2tools.jar> <output-dir> <configuration> [property ...]
tests/formal/sessionlifecycle/run.sh <path-to-tla2tools.jar> <output-dir> all
```

With no property named, TLC checks every invariant and property the configuration lists; with properties named, it checks only those. `all` runs every configuration in turn. TLC stops at the first violation, so a run that finds one is repeated with the other checks named. `TLC_WORKERS` sets the worker count (default 8), `TLC_HEAP` the JVM heap (default 12g), `TLC_NODEADLOCK=1` turns off TLC's deadlock check, and with `TLC_DOCKER=1`, `TLC_DETACH=1` starts each container detached. The output directory receives the effective configuration, TLC's log, and a receipt with the TLC release, the jar's SHA-256, the Java version on a host run, the checks, and the start and finish times.

The model belongs to no gate and is run ad hoc. On the owner's workstation, take the shared lock before a run and stop any run at one hour, as `.claude/rules/working-practices.md` states: create `scratchpad/gate.lock` with `mkdir` (retry until it succeeds), and remove it with `rmdir` afterwards.
