# Decisions: 0171 Revocation cache rehydration on event-bus reconnect

## Open decisions

- **RH-1. Rehydration on the first subscription confirmation.** RR-1 calls the reconnect callback on every confirmation after the first, so a replica whose first subscription is confirmed after its startup rehydration began, such as one that starts while Redis is unreachable, applies a revocation published in between only at its next periodic rehydration, at most 30 seconds later. §13.3 as built on the step's branch states that case under the periodic bound. The review of 0171.S1 (general finding 3, security finding 4) raised it.
  - (a) Call the callback on every confirmation, the first included, so the startup case is applied at once. It costs one extra rehydration at startup, which the trigger coalesces, and RR-1 and §13.3 are revised to match.
  - (b) Keep RR-1 as written, with the startup case under the 30-second periodic bound.
  - Recommendation: (a). The extra read is one per replica start, and it closes the one case in which a reachable Redis still leaves a missed event to the periodic read.
- **RH-2. Spacing between reconnect-triggered rehydrations.** A Redis connection that flaps (an idle-connection reset by a proxy, `client-output-buffer-limit pubsub`, `CLIENT KILL`, or a Sentinel flap) makes each replica start a full rehydration as soon as the previous one ends, so Postgres serves one continuous read per replica rather than one per 30 seconds. The trigger coalesces requests, so each replica runs at most one read at a time. The review of 0171.S1 (security finding 3) raised it. Any spacing is a default the specification does not fix, so it needs a flag.
  - (a) No spacing, as built.
  - (b) A minimum spacing between reconnect-triggered rehydrations, default 5 seconds, overridable by a gateway flag, with a counter of reconnect-triggered rehydrations added to the §16.1 metrics catalog.
  - (c) The spacing of (b) without the metric.
  - Recommendation: (c). It bounds the load at one read per replica per 5 seconds and delays a reconnect's rehydration by at most 5 seconds, inside the 30-second bound, without a specification edit for a new metric.

## Decisions

1. **Each replica rehydrates its revocation cache when its event-bus subscription reconnects, and §13.3 states the bound (FM-30).** Each gateway replica rehydrates its revocation cache immediately when its event-bus subscription reconnects, or when the bus reports a dropped message. §13.3 states the bound: a lost `token.revoked` event is reconciled by the next rehydration, within 30 seconds, and a replica refuses validation after 90 seconds without a successful rehydration. The per-request Postgres read is removed from the text, and no validation reads the database. Owner's answer to FM-30 on the M3 readiness decision page and in chat, 2026-11-23, which settles open decision 14 of change 0169 (a concurrency model of the token rules, [decision 14](../archive/2026-11-23-0169-token-concurrency-model/decisions.md)) with an option of its own and lands it as this change rather than as an issue. The options not chosen were a per-token Postgres read behind a short TTL cache (default 5 seconds), a specification-only edit describing the current code (0169's option (b)), and an issue under TOKX for the missing read (0169's option (a)).
