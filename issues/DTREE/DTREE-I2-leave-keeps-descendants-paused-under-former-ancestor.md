---
id: DTREE-I2
status: open
kind: spec-defect
severity: medium
requirements: []
---

# A leave keeps descendants blocked under a session that is no longer their ancestor

## Specification

Change 0146 (session lifecycle), revision 24, "Sessions that left their parent" in SL-4: "A leave of a `blocked` session recomputes its nearest paused ancestor in its new position." "Row fields" in SL-1: `paused_ancestor_id` is "set on a `blocked` session to its nearest paused ancestor". Change 0166 "Leaving the parent" in DP-2: a released session's "own children ... move with it", and a transferred session is re-linked to its root.

## Evidence

- `tests/formal/sessionlifecycle/`, configurations `chain3_leave.cfg` and `chain4_leave.cfg`: `BlockedUnderPausedAncestor` fails (counterexample R2 in `changes/0175-session-lifecycle-model/change.md`). Trace: the root's client suspends root r while a's child b is in setup, so a is `blocked`; it resumes a directly; b's setup commits and lands `blocked` under r; the root's client releases a; b stays `blocked` with `paused_ancestor_id` naming r, which is in another tree.
- An exploration run reached the same with a in `resume_pending` after a pod loss in place of the direct resume. A transfer past a paused session between the root and the former parent leaves the same kind of pointer.
- Found by step 0175.S1 on 2026-11-27.

## Gap

Only the leaving session is re-pointed. A descendant whose `paused_ancestor_id` names a session above the leaving one stays `blocked` while every session above it is live, until the former ancestor resumes, when the tree resume reaches into another tree, or until `maxSuspendedSessionSeconds` expires it.

## Suggested resolution

Open decision O7 of change 0175: recompute the nearest paused ancestor of every such descendant in the leave's write (recommended), or refuse the leave.
