# Decisions: 0178 Idempotency keys scoped by principal

## Open decisions

None.

## Decisions

1. **Idempotency keys are scoped by principal (IK-3).** Idempotency records are scoped by the principal, the tenant together with the user (subject), through a change that edits §11.5 and the store. Owner's answer to IK-3 on the M3 readiness decision page, 2026-11-25, option (a). The option not chosen was (b), keeping the per-tenant scope and documenting it. The question arose from the review of change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), whose replay rebuilds an upload token for any caller that holds the key.
2. **User erasure deletes the user's idempotency records (ERASE-I2).** Deleting a user's idempotency records during user erasure, which revision 1 left out of scope, is added to this change as its next step, 0178.S2. Owner's direction in chat, 2026-11-25, with the words "Add the fix for the deletion of idempotency records to the list of next steps." The deletion finds the records by the subject that step 0178.S1 adds.
3. **The scope of a bearer bound to a session (O1).** When the principal carries a `SessionID`, as a session-capability bearer and the bearer of a delegated child do, the record scope includes it, so the scope is the tenant, the subject, and the session for such a bearer, and the tenant and the subject otherwise. The session id is an extra column of the primary key of `idempotency_keys`, `session_id`, which holds the empty string for an ordinary user token, because a primary-key column cannot be NULL. The option not chosen was (b), keeping the scope at the tenant and the subject for every principal, as decision 1 states it. Owner's answer to IP-1 (open decision O1) on the M3 readiness decision page, 2026-11-26, option (a).
