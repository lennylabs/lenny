---
id: DTREE-I5
status: open
kind: spec-defect
severity: medium
requirements: []
---

# A leave that commits between a parent's end and the cascade takes the session out of a force-terminate

## Specification

Change 0146 (session lifecycle), revision 24: SLR-31, "A force-terminate of the parent, and a tree terminate, close every descendant at once whatever `onParentEnd` holds"; "Sessions that left their parent" in SL-9, "A transferred session is no longer its former parent's child, so its former parent's end ... does not reach it". Change 0166 DPR-16: a session leaves "only while the grant that governs the call is present", and "the grant that governs a call of the root's client through the REST route is the root's".

## Evidence

- `tests/formal/sessionlifecycle/`, configuration `chain3_end_leave.cfg`: `ForceReachesDescendants` fails (counterexample R6 in `changes/0175-session-lifecycle-model/change.md`). Trace on the chain r, a, b: an operator force-terminates a; before the cascade closes b, the root's client releases b, which becomes a root; b keeps running. A transfer to the live root has the same effect.
- `cascadeToChildren` and `cancelNodeGuarded` in `pkg/gateway/core/session/sessionserver/usage.go` close each child in a write of its own after the parent's terminal write, so the window exists in the code's design.
- Found by step 0175.S1 on 2026-11-27.

## Gap

The text does not say whether a leave may commit after the session's parent has ended. When it may, a force-terminate or a tree terminate does not stop every descendant the session had when it ended.

## Suggested resolution

Open decision O10 of change 0175: refuse a leave from a parent that ended by a force-terminate or a tree terminate (recommended), refuse a leave from any ended parent, or state the race.
