# Decisions: 0178 Idempotency keys scoped by principal

## Open decisions

- **O1. The scope of a bearer bound to a session.** Some principals carry a `SessionID`: a session-capability bearer, and the bearer of a delegated child. Two sessions of one user then have the same subject, so under decision 1 as written, a `lenny/delegate_task` call from one session with `idempotencyKey: "child-1"` and the same arguments as an earlier call from the user's other session receives the other session's stored child handle. An agent that names its keys from a fixed template makes that collision likely.
  - (a) Add the principal's `SessionID`, when it is set, to the record scope, so the scope is the tenant, the subject, and the session for a bearer bound to a session, and the tenant and the subject otherwise.
  - (b) Keep the scope at the tenant and the subject for every principal, as decision 1 states it.
  - Recommendation: (a). A retry comes from the same session that sent the first request, so (a) keeps every retry collapsing, and it stops one session's stored result from reaching another session of the same user. It adds a `session_id` column with an empty-string default to the primary key in the migration of IP-3, because a primary-key column cannot be NULL.

## Decisions

1. **Idempotency keys are scoped by principal (IK-3).** Idempotency records are scoped by the principal, the tenant together with the user (subject), through a change that edits §11.5 and the store. Owner's answer to IK-3 on the M3 readiness decision page, 2026-11-25, option (a). The option not chosen was (b), keeping the per-tenant scope and documenting it. The question arose from the review of change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), whose replay rebuilds an upload token for any caller that holds the key.
2. **User erasure deletes the user's idempotency records (ERASE-I2).** Deleting a user's idempotency records during user erasure, which revision 1 left out of scope, is added to this change as its next step, 0178.S2. Owner's direction in chat, 2026-11-25, with the words "Add the fix for the deletion of idempotency records to the list of next steps." The deletion finds the records by the subject that step 0178.S1 adds.
