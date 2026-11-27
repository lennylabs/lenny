---
id: DTREE-I4
status: open
kind: spec-defect
severity: low
requirements: []
---

# SLR-28 claims more than the re-pointing it states gives

## Specification

Change 0146 (session lifecycle), revision 24, SLR-28: "When a `blocked` session ends, every session whose `paused_ancestor_id` names it is re-pointed to the ended session's own `paused_ancestor_id`, so no `blocked` session is left under a terminal session that is not `ended_suspended`." "A blocked session that ends" in SL-9 adds the safety net: "backlog resume recomputes the nearest paused ancestor of a `blocked` session whose `paused_ancestor_id` names a terminal session that is not `ended_suspended`."

## Evidence

- `tests/formal/sessionlifecycle/`: the invariant `NoBlockedUnderEndedSession`, which states the second half of SLR-28, fails (counterexample G1 in `changes/0175-session-lifecycle-model/change.md`). Trace: the root's client suspends root r, and child a becomes `blocked` under r; the root's client resumes r by a message, so r is `idle`; before the tree resume reaches a, the root's client cancels r. r ended from `idle`, not from `blocked`, so nothing re-points a, and a is `blocked` under a terminal r that is not `ended_suspended`.
- The safety net of SL-9 resumes or re-points a, and the model's liveness property `TreeResumeReachesBlocked` holds with it.
- Found by step 0175.S1 on 2026-11-27.

## Gap

The "so no `blocked` session is left under ..." clause of SLR-28 does not follow from the re-pointing it states, because a session's `paused_ancestor_id` can name an ancestor that became live and then ended before the tree resume reached the session. A test written from SLR-28 alone would fail on this ordering.

## Suggested resolution

Reword SLR-28 to state the re-pointing alone, and state in SLR-6 or SL-9 that backlog resume treats a `paused_ancestor_id` naming a terminal session that is not `ended_suspended` as lifted. This is a wording fix in 0146 and raises no decision.
