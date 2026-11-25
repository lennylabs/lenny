# Decisions: 0176 Erasure waits for the terminal pipeline

## Open decisions

None.

## Decisions

1. **The erasure job waits for the whole terminal pipeline (ER-1).** The gateway marks the session row when the last step of the session's terminal pipeline finishes, after `afterTerminalRelease` in `recordSessionCompleted`, and every terminal session, with or without a pod, is pending until the mark is set or the terminal pipeline budget has elapsed. User erasure (`store_deleting`) and tenant deletion, which uses the same predicate, both wait on it. Owner's answer to ER-1 on the M3 readiness decision page, 2026-11-25, option (a). The options not chosen were (b), moving the billing, quota-checkpoint, and session-log steps before the pod release and extending the wait to sessions without a pod, and (c), option (a) together with re-pseudonymizing when the verification finds a row and refusing billing writes for a user whose processing is restricted.
