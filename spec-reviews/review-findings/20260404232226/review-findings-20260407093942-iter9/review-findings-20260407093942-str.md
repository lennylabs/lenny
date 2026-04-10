# Review Findings — Iteration 9, Perspective 9: Storage Architecture & Data Management

**Document reviewed:** `docs/technical-design.md`
**Iteration:** 9
**Perspective:** Storage Architecture & Data Management (STR)
**Category prefix:** STR (starting at STR-032 per instructions)
**Sections reviewed:** §4.4, §4.5, §9.4, §11.2.1, §12.1–12.9, §17.3
**Date:** 2026-04-07

---

## Summary

| ID      | Severity | Finding                                                                                       | Section  |
|---------|----------|-----------------------------------------------------------------------------------------------|----------|
| STR-032 | Medium   | Billing Redis stream key-level TTL expires from stream creation, not from last write — events written late in an outage window have far less than `billingStreamTTLSeconds` of durability | §11.2.1, §12.4 |
| STR-033 | Low      | `XGROUP CREATE ... MKSTREAM` race condition when multiple gateway replicas start simultaneously is unaddressed — concurrent creators receive `BUSYGROUP` errors with no handling guidance | §11.2.1 |

---

## STR-032 Billing Redis Stream TTL Expires from Stream Creation, Not from Last Write [Medium]

**Section:** §11.2.1, §12.4

**Problem:**

The spec sets a key-level TTL of `billingStreamTTLSeconds` (default: 3600s) on the billing Redis stream key (`t:{tenant_id}:billing:stream`). In Redis, `EXPIRE`/`EXPIREAT` sets a fixed expiry from the moment the TTL is applied — subsequent `XADD` operations do not refresh or extend the TTL on the key. This means:

- If the stream is created at T=0 with TTL=3600s, the entire stream key expires at T=3600s.
- An event written to the stream at T=3500s will expire only 100 seconds later — not 3600 seconds later as the spec implies.
- Under a prolonged Postgres outage (e.g., 58 minutes), events written in the last two minutes of the outage window have as little as 120 seconds of Redis durability, not the full 3600s.

The spec's durability claim — "events not flushed to Postgres within this window are permanently lost (this scenario requires Redis uptime > 1 hour while Postgres is simultaneously down)" — is misleading. Under the current design, data loss can occur for events written near the end of the TTL window even when both Redis and Postgres are restored well within the 1-hour mark.

**Impact:**

Billing events written late in a Postgres outage have much shorter durability than the spec guarantees. The `BillingStreamBackpressure` alert at 80% of `billingRedisStreamMaxLen` does not help here — this is a time-based expiry, not a count-based one. In a Tier 3 scenario with 50,000 entries × (500ms flush interval), billing events arrive faster than they can be flushed during a Postgres outage, compounding the risk.

**Fix:**

Choose one of:

1. **Use a sliding TTL (`PERSIST` + `EXPIRE` refresh on `XADD`):** After each `XADD`, reset the stream TTL to `billingStreamTTLSeconds` using `EXPIRE key billingStreamTTLSeconds`. This keeps the stream alive for `billingStreamTTLSeconds` after the *last* write, matching the intended durability semantics. Note: this adds one extra Redis round-trip per event write; use a Lua script or pipeline to batch the `XADD` + `EXPIRE` atomically.

2. **Remove the key-level TTL and rely on MAXLEN only:** Manage stream lifetime purely via the `MAXLEN` cap (50,000 entries) rather than a time-based TTL. When Postgres recovers, the flusher drains the stream and `XDEL`s each entry after acknowledgement. A separate cleanup routine can delete the key entirely when the stream is empty and no pending entries remain. This eliminates the time-based expiry risk entirely.

3. **Explicitly document the semantics as "TTL from stream creation" and reduce the claimed durability window:** If option 1 or 2 is not adopted, the spec must accurately state that events have `billingStreamTTLSeconds - elapsed_since_stream_creation` durability, and the `BillingStreamBackpressure` alert must additionally fire when the stream age approaches the TTL cap.

---

## STR-033 `XGROUP CREATE ... MKSTREAM` Race Condition on Concurrent Gateway Startup Is Unaddressed [Low]

**Section:** §11.2.1

**Problem:**

The spec states that each gateway replica initializes the billing consumer group via `XGROUP CREATE t:{tenant_id}:billing:stream billing-flusher $ MKSTREAM`. In Redis, `XGROUP CREATE` returns a `BUSYGROUP Consumer Group name already exists` error if the group already exists — regardless of whether `MKSTREAM` is specified. When multiple gateway replicas start simultaneously (e.g., during a rolling deployment or HPA scale-up event), the following race occurs:

- All replicas concurrently issue `XGROUP CREATE billing-flusher ... MKSTREAM`.
- The first replica to execute the command succeeds.
- All subsequent replicas receive `BUSYGROUP` errors.

The spec does not specify how replicas should handle this error. Without explicit guidance, implementations may:
- Treat `BUSYGROUP` as a fatal startup error and crash-loop.
- Ignore it silently and proceed (correct behavior, but not specified).
- Log it as an error and continue degraded.

The correct behavior — treat `BUSYGROUP` as a benign "group already exists" condition and proceed — is a standard Redis pattern but must be specified explicitly in the design doc since it affects billing stream initialization reliability.

**Impact:**

Low severity: any competent Redis implementation would handle this idiomatically. However, since the spec explicitly describes the `XGROUP CREATE` call and the consumer group initialization sequence, the absence of error handling guidance is a specification gap that could cause gateway startup failures in environments where strict error handling is enforced (e.g., crash on any Redis error).

**Fix:**

Add one sentence to the billing stream initialization description: "If `XGROUP CREATE` returns `BUSYGROUP`, the replica treats it as a no-op — the group already exists and the replica proceeds to join it as a distinct consumer using its pod ID as the consumer name. This is the expected concurrent-startup behavior and must not be treated as an error."
