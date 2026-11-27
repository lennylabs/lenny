---
id: ADMIT-I1
status: open
kind: spec-defect
severity: low
requirements: []
---

# maxParallelChildren can be exceeded without a creation, and the text does not say whether it may

## Specification

Change 0146 (session lifecycle), revision 24, "A parent's parallelism" in SL-10: "A child counts toward its parent's `maxParallelChildren` ... in every state the caps count and in `blocked`". The caps count every non-terminal state except `dormant`, `suspended`, and `blocked`. **Resume admission** in SL-10 runs the tenant's caps, and which other gates a resume runs is open decision SL-Q12 of 0146.

## Evidence

- `tests/formal/sessionlifecycle/`, configuration `fan3_admission.cfg`: `ParallelismHolds` fails (counterexample R4 in `changes/0175-session-lifecycle-model/change.md`). Trace with `maxParallelChildren: 1`: child a becomes `dormant`; the root creates child b, which the count admits; the root's client suspends the root, and a becomes `blocked`, so two children count.
- A `dormant` or `suspended` child that wakes or resumes takes the count above the bound in the same way.
- Found by step 0175.S1 on 2026-11-27.

## Gap

The text states the count and its refusal at `session.create`, and is silent on whether the bound holds at every instant. A build of 0166.S2 could count at creation alone or try to hold the bound on every wake.

## Suggested resolution

Open decision O9 of change 0175: state the bound as a check at creation (recommended), add the parent's parallelism to resume admission, or count every non-terminal child.
