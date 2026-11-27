---
id: DTREE-I1
status: open
kind: spec-defect
severity: medium
requirements: []
---

# The tree terminate states neither the root's terminal state nor its effect on a tree whose root already ended

## Specification

Change 0146 (session lifecycle), revision 24, "Force-terminate and tree terminate" in SL-9: a `tenant-admin` terminates a tree of its own tenant with `POST /v1/admin/sessions/{rootId}/terminate-tree`, "which writes the same audit event as a force-terminate, closes each descendant of the root at once by the close rule whatever `onParentEnd` holds, and also reaches each session transferred in the tree" (SLR-31). Change 0166 "Ending a tree" in DP-2 keeps the route.

## Evidence

- Neither 0146 nor 0166 states the terminal state the route records on the root. A force-terminate records `failed` with `FORCE_TERMINATED` (SL-2; §24.11; `ForceTerminator.ForceTerminate` in `pkg/gateway/core/session/sessionserver/force_terminate.go`).
- Neither states what the route does when the root has already ended while descendants run under `let_finish` or stay `blocked` under an `ended_suspended` root (SL-9).
- The model under `tests/formal/sessionlifecycle/` ends the root `failed` and applies the route to an ended root; both are defaults of the model (`TreeTerminate` in `SessionLifecycle.tla`).
- Found by the review of step 0175.S1 on 2026-11-27.

## Gap

Step 0166.S1, which builds the route, has no rule for the root's state or for an ended root.

## Suggested resolution

Open decision O5 of change 0175: the root ends `failed` as a force-terminate, and on an ended root the route closes the remaining descendants (recommended).
