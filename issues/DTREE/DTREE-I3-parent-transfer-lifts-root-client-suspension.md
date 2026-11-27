---
id: DTREE-I3
status: open
kind: spec-defect
severity: medium
requirements: []
---

# A parent's transfer of a blocked child lifts a pause that only the root's client may lift

## Specification

Change 0146 (session lifecycle), revision 24:

- "Sessions that left their parent" in SL-4: "A transferred session is re-pointed to its nearest paused ancestor on the path from the root, and when that path holds none it resumes through resume admission".
- "Who suspends and who lifts" in SL-4: "Only the root's client lifts a suspension".
- SLR-5: "No message from a session in the tree, and no system message, moves a session out of `suspended` or `blocked`."

## Evidence

- `tests/formal/sessionlifecycle/`, configuration `chain4_leave.cfg`: `OnlyRootClientLifts` fails (counterexample R3 in `changes/0175-session-lifecycle-model/change.md`). Trace on the chain r, a, b, c: the root's client suspends a; it resumes b directly; b's runtime creates c, which lands `blocked` under a; b transfers c, whose new path is r alone, so c resumes while a is still suspended.
- Found by step 0175.S1 on 2026-11-27.

## Gap

A parent session, which has no operation to suspend or resume a child (L12), resumes a child paused under a suspension of the root's client by transferring it.

## Suggested resolution

Open decision O8 of change 0175: a transferred `blocked` session with no paused ancestor on its new path becomes `suspended` for its client, as a released one does (recommended), or a parent's leave of a `blocked` child is refused.
