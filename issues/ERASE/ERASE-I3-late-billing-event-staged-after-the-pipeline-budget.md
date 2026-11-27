---
id: ERASE-I3
status: planned
kind: build-gap
severity: high
requirements: []
change: 0176.S2
---

# A `session.completed` billing event appended after the terminal pipeline's budget is staged and inserted after the erasure has passed

## Specification

§12.8 step 5 purges the billing buffer before pseudonymization because a staged event "flushed after … pseudonymized would re-insert the raw user_id". §7.1 "Terminal pipeline context:" bounds the pipeline by its budget.

## Evidence

- Found by the review of step 0176.S1 on branch `change/0176.S1-pipeline-mark` (2026-11-25), recorded there as open decision O2 of change 0176.
- When the pipeline's context has ended, the billing append of `afterTerminalRelease` (`pkg/gateway/core/session/sessionserver/usage.go`) fails on the primary store and falls through to the failover pipeline's in-memory buffer, whose flusher later inserts the row with the raw `user_id`. Every budget overrun takes this path; no primary outage is needed.
- The related legacy finding F-12.8.53 (`BUILD-GAPS.md`) reports that the per-replica billing buffer is erased only on the replica that runs the eraser.

## Gap

Change 0176 makes the erasure wait until the pipeline writes its mark or its budget elapses. An append that runs after the budget is staged instead and reaches `billing_events` after the erasure has moved on, so the raw `user_id` can survive a completed erasure.

## Suggested resolution

Under the owner's answer to 0176 O2: skip the append on an ended pipeline context and count it, with an exception in §11.2.1 (recommended); or have `failover.Pipeline.Append` refuse to stage when the caller's context has ended; or keep the behaviour and record the limit.

The owner settled O2 on 2026-11-26 with option (e), decision 6 of change 0176: the terminal pipeline writes `session.completed` with an empty `user_id`, so an event staged after the budget carries no user identifier and the erasure has nothing to rewrite in it. Step 0176.S2 builds it. Events staged on another replica outside the purge remain ERASE-I4.
