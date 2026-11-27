---
id: ERASE-I7
status: open
kind: build-gap
severity: medium
requirements: []
---

# A periodic `token_usage.checkpoint` billing event can carry the raw user id after user erasure has pseudonymized the user's events

## Specification

§12.8 user erasure pseudonymizes the user's billing events and verifies that no event keeps the original user id. §11.2.1 lists `user_id` on every billing event; since step 0176.S2 only `session.completed` leaves it empty.

## Evidence

- Found by the security review of step 0176.S2 on 2026-11-27.
- `cmd/lenny-gateway/worker_seams.go`, `billingSessionLister.ListActiveSessions`, lists each tenant's sessions with `sessionstore.ListFilter{}`, which reads a replica rather than the Postgres primary, and keeps every row that is not terminal, with its `UserID`.
- `pkg/billing/billingcheckpoint/billingcheckpoint.go`, `Checkpoint`, emits `billingfanout.TokenUsageCheckpoint(s.TenantID, s.SessionID, s.UserID, ...)` for each listed session with a positive token delta.
- `pkg/billing/billingfanout/billingfanout.go` bounds the append with `emitTimeout` (250 ms); a slower primary write is staged in the failover tiers and flushed later.
- Nothing ties the checkpointer to the creation fence, the drain, or the terminal pipeline wait of §12.8 **Lifecycle fence and drain.**

## Gap

A checkpoint pass that listed the user's session before the drain, or read a lagging replica, appends an event with the raw `user_id` after the erasure job has passed `store_deleting`. Between the pseudonymizing step and the verification it fails the job and leaves the user restricted; after the verification it leaves the raw id in `billing_events` beside a verified receipt. The suggested resolution of BILLEV-I1, a final checkpoint in the terminal pipeline, would add another such write after the pod release.

## Suggested resolution

Open decision O5 of change [0176](../../changes/0176-erasure-waits-for-terminal-pipeline/decisions.md). Options: (a) write `token_usage.checkpoint` with an empty `user_id`, as decision 6 of change 0176 does for `session.completed`, so consumers join on `session_id` (recommended, because it closes every timing of the write and matches the existing join); (b) have the lister read the primary and skip sessions whose user is `processing_restricted`, which narrows the window and leaves the staged-append path open; (c) refuse a billing insert for a restricted user, option (c) of 0176 decision 1, which the owner declined there.
