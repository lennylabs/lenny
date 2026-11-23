---
id: TOKX-I13
status: open
kind: build-gap
severity: medium
requirements: []
---

# A token derived while a recursive revocation runs escapes the revocation

## Specification

§13.3 (Credential Flow) **Lineage of derived tokens.**: the recursive-revocation request walks `parent_jti`, so revoking a token also revokes every token derived from it. **Rotation lineage and leaked tokens.**: a holder who rotates a leaked token before the user acts loses the successor when the user revokes the leaked token. §11.4 (User Invalidation) step 5: a full revoke revokes the user's issued tokens. Neither section states how a revocation orders against an issuance that commits while the revocation runs.

## Evidence

- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `revokeCascadeByCallerOnce`: the transaction takes no §11.7 per-tenant lock, and one statement (`WITH RECURSIVE subtree AS (...) UPDATE issued_tokens ... WHERE ... revoked_at IS NULL`) reads the subtree from the snapshot taken when the statement starts. `RevokeBySubject` takes no lock either.
- `RecordWithRotationAudit` and `RecordWithAudit` take the lock and re-read the sources (`lockAndRecheck`) before they insert. A rotation of a token Ln in the subtree that commits after the cascade statement starts inserts its successor with `parent_jti` = Ln and stamps Ln revoked. The cascade's UPDATE then re-checks Ln, finds `revoked_at` set, and skips it, and the successor is outside the statement's snapshot, so it stays live. The request returns `200` with a `revoked_count` that does not show the miss. A narrowing or a child mint from a token in the subtree has the same window.
- A holder who rotates continuously lengthens the chain by one level per rotation ([TOKX-I10](TOKX-I10-revocation-cascade-has-no-depth-or-time-bound.md)), which lengthens the cascade and widens the window. The successor lives no later than the leaked token's `exp`, because an exchange never extends `exp` past its subject's.
- `tests/component/issuedtokencascade/rotation_lockfree_revoke_test.go` covers the opposite ordering, in which the revocation commits first and the rotation is refused.
- Found by the general and security reviews of 0164.S1 (revision 4) on 2026-11-22 (general 3, security 1). The window predates 0164; decision 10 of 0164 restores the rotation link to close the rotate-first escape, which makes the window relevant to that guarantee.

## Gap

A holder of a leaked token in the subtree of a revocation keeps a derived token when its issuance commits while the revocation runs, and a full revoke has the same window for one statement.

## Suggested resolution

Take the §11.7 per-tenant lock at the start of `revokeCascadeByCallerOnce` and `RevokeBySubject`, before the statement that reads the snapshot, so that each issuance either commits first and is visible to the walk or runs after the revocation and fails the step (1a) re-read; state the ordering in §13.3; and add a component test for the ordering. Holding the tenant lock for a cascade blocks the tenant's issuances for the cascade's duration, so the owner decides it together with the bound of TOKX-I10.
