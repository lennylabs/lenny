---
id: TOKX-I19
status: open
kind: build-gap
severity: low
requirements: []
---

# A full revoke misses the delegations a user made with a token that has no `issued_tokens` row

## Specification

§11.4 (User Invalidation) step 5: the user's issued tokens, and every lineage descendant of a token that carries the user's `sub` (each token whose `parent_jti` chain in the tenant reaches it, whatever its own `sub`), are revoked. §13.3 (Credential Flow) **Token rule properties.** item 10 states the same outcome. §13.3 **Recursive-revocation request authorization.** names a Lenny token minted outside `/v1/oauth/token` that the platform did not record as a token without a row.

## Evidence

- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `RevokeBySubject`: the recursive walk is seeded with `WHERE tenant_id = $1 AND sub = $2`, the user's rows only, so a token of the user that has no row seeds nothing, and a child whose `parent_jti` names it is not reached.
- `pkg/tokenservice/tokenservice.go`, `derivedLineage`: a child minted with a token as `actor_token` records the actor's `jti` as `parent_jti` and the actor's `sub` as `act_sub`, whether or not the actor has a row.
- The property tests of change 0165 (step 0165.S2) reproduce it in `tests/component/tokenproperties`: alice holds a token A without a row; bob holds A and a token S of his own and mints a child C with S as `subject_token` and A as `actor_token` and caller; a full revoke of alice leaves C live. The fixed sequence is the skipped subtest "a full revoke reaches the delegations made with a token without a row" of `TestTokenRuleScenariosOfTheModelKeepTheProperties`.
- Every Lenny mint on `main` records its token when the gateway has an issued-token store, so the sequence needs a Lenny-signed token minted without a record; the `subject_token` and `actor_token` of a mint are verified with the Lenny key set only, so an upstream ID token cannot be the actor.

## Gap

A full revoke of a user leaves live a child that another user minted with an unrecorded token of the user as actor, although its `act` claim names the user and its lineage reaches a token of the user.

## Suggested resolution

The owner settled it on 2026-11-23 as change 0165 decision 16 (answer to FM-15 on the M3 readiness decision page): seed the walk of `RevokeBySubject` also with the child rows whose `act_sub` is the user and whose `parent_jti` is set, which are the delegations the user made with any token. Change 0168 carries the fix.
