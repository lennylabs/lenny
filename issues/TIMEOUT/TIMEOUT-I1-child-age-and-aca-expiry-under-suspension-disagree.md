---
id: TIMEOUT-I1
status: open
kind: spec-defect
severity: low
requirements: []
---

# perChildMaxAge and SLR-21 disagree on a child in awaiting_client_action under a suspended ancestor

## Specification

Change 0146 (session lifecycle), revision 24, which the specification takes on in step 0146.S1:

- SLR-21: "A descendant in `awaiting_client_action` does not expire while an ancestor is suspended."
- "A child's age" in SL-10, moved from decision 13 of change 0166 by decision 1 of change 0175: `perChildMaxAge` is a deadline "counted in every non-terminal state of the child", and the term in 0146 says no pause stops it.

## Evidence

- `tests/formal/sessionlifecycle/`, configuration `chain2_childage.cfg`: `ACANoExpiryWhileAncestorPaused` fails when the age rule applies to a child in `awaiting_client_action` under a suspended root (counterexample R5 in `changes/0175-session-lifecycle-model/change.md`).
- Found on 2026-11-27 when step 0175.S1 moved the age rule from 0166 into 0146.

## Gap

The two rules give different outcomes for the same child: the age expires it, and SLR-21 says it does not expire. A build of 0146.S1 and 0166.S2 would implement one of them and contradict the other.

## Suggested resolution

Open decision O4 of change 0175: narrow SLR-21 to the `maxAwaitingClientActionSeconds` clock (recommended), or pause the age for that state.
