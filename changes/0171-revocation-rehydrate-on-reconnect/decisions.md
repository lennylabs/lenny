# Decisions: 0171 Revocation cache rehydration on event-bus reconnect

## Open decisions

None.

## Decisions

1. **Each replica rehydrates its revocation cache when its event-bus subscription reconnects, and §13.3 states the bound (FM-30).** Each gateway replica rehydrates its revocation cache immediately when its event-bus subscription reconnects, or when the bus reports a dropped message. §13.3 states the bound: a lost `token.revoked` event is reconciled by the next rehydration, within 30 seconds, and a replica refuses validation after 90 seconds without a successful rehydration. The per-request Postgres read is removed from the text, and no validation reads the database. Owner's answer to FM-30 on the M3 readiness decision page and in chat, 2026-11-23, which settles open decision 14 of change 0169 (a concurrency model of the token rules, [decision 14](../0169-token-concurrency-model/decisions.md)) with an option of its own and lands it as this change rather than as an issue. The options not chosen were a per-token Postgres read behind a short TTL cache (default 5 seconds), a specification-only edit describing the current code (0169's option (b)), and an issue under TOKX for the missing read (0169's option (a)).
