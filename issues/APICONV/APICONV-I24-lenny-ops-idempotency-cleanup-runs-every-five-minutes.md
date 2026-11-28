---
id: APICONV-I24
status: planned
kind: build-gap
severity: low
requirements: []
change: 0183.S5
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

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S5 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
