---
id: TOKX-I22
status: closed
kind: build-gap
severity: low
requirements: []
closed: fixed
closed-by: 0168.S1
---

# A recursive-revocation request refuses a caller that its own impersonation step revoked

## Specification

§13.3 (Credential Flow) **Token rule properties.** item 4: the Token Service accepts a recursive-revocation request when its caller token is live, is not a playground bearer, carries the `subject_token`'s `tenant_id`, and either carries the `subject_token`'s `sub` or holds the `tenant-admin` or `platform-admin` role. Item 3: an accepted request revokes its `subject_token`, every lineage descendant of the `subject_token` in its tenant, and every active impersonation row whose impersonator link names one of those tokens. Neither item has an exception for a caller that the request's own impersonation step revokes.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `revokeImpersonationsFrom`: the request reads the subtree with `LineageSubtree`, which passes because the caller is live, then revokes every impersonation row whose `impersonator_jti` is in the subtree with `RevokeImpersonationsByImpersonator`, in its own transaction.
- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `RevokeLineage`: the cascade's transaction reads the caller's row under a share lock and refuses a revoked caller with `401 invalid_client`, including a caller that the impersonation step of the same request revoked.
- The property tests of change 0165 (step 0165.S2) found the sequence as finding F1: bob holds a token B in acme with the `platform-admin` role and starts an impersonation session in his own tenant whose bearer I carries bob's `sub`. A recursive-revocation request with I as caller and B as `subject_token` ends I and the other sessions B's lineage started, then answers `401 invalid_client`, and B stays live. The generated test first reached it at `-rapid.seed=20389901`. The skipped subtest "an impersonation bearer as the caller of its root's revocation revokes the root" of `TestTokenRuleScenariosOfTheModelKeepTheProperties` in `tests/component/tokenproperties` holds the sequence, and the generator of `TestTokenRulesKeepTheirPropertiesOverGeneratedSequences` does not draw it until the fix lands.
- The sequence needs an impersonation inside the admin's own tenant, because the tenant check refuses a caller of another tenant first. The owner of B can retry with B or another live token, which succeeds.

## Gap

A recursive-revocation request whose caller is an impersonation bearer that the `subject_token`'s lineage started ends the impersonation sessions and is then refused, so it revokes no token of the lineage and its reply does not say that the sessions ended.

## Suggested resolution

The owner settled the rule on 2026-11-23 as change 0165 decision 15 (answer to FM-14 on the M3 readiness decision page): the cascade accepts a caller that the same request's impersonation step revoked. Option (a) of that decision gives the mechanism: take the `jti` values from the `RETURNING` set of the impersonation update, pass them to `RevokeLineage`, and refuse every other revoked caller as now. A reading by time or by revocation reason would accept a caller that another request revoked and is excluded. Change 0168 carries the fix, together with [TOKX-I20](TOKX-I20-caller-revoked-between-the-subtree-read-and-the-impersonation-step.md), and removes the generator's exclusion and the subtest's skip.

## Resolution

Change 0168 (decision 1) closes it in step 0168.S1. The impersonation step returns the `jti` values it revoked in the request's tenant, and the cascade accepts a revoked caller among them (`LineageRevocation.StepRevokedCallers`) and refuses every other revoked caller. The property tests drop the generator's exclusion of the sequence and the skip of the subtest "an impersonation bearer as the caller of its root's revocation revokes the root".
