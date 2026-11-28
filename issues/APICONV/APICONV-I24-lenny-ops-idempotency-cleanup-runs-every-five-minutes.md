---
id: APICONV-I24
status: open
kind: build-gap
severity: low
requirements: []
---

# The lenny-ops idempotency cleanup runs every five minutes where the specification says daily at 02:30 UTC

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency › Retention**: a scheduled `DELETE` runs daily off-peak, at 02:30 UTC by default, and removes expired rows; lazy cleanup on acquire handles bursts between scheduled runs.

## Evidence

- `IdempotencyCleanupInterval` in `pkg/ops/opsservice/reconcile.go` is 5 minutes, and the `idempotency-cleanup` reconciler runs `PruneExpired` (`cmd/lenny-ops/servicebody.go`) at that interval. No setting selects a time of day.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F14), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

The code and the specification state different schedules. Neither schedule changes which records replay, because `Claim` ignores expired rows, but an operator reading §25.4 expects one large daily `DELETE` and sees a small one every five minutes.

## Suggested resolution

State in §25.4 that the cleanup runs every five minutes, the schedule the code has, or change the reconciler to the daily schedule with its setting. Either way, name the interval or the time as an operator setting with its default.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
