# Token rule fixes from the formal model

Status: `In progress`, revision 1, 2026-11-22.

## Problem

Change 0165 (a formal model of the token rules, `changes/0165-token-rules-model/` on its own branch) checked the token rules of §13.3 (Credential Flow) as change 0164 (token exchange narrowing and identity rules) builds them, with an Alloy 6 model of the exchange, the child mint, the recursive-revocation request, the platform-admin impersonation mint, the playground mint, and the full revoke of a user. The model returned counterexamples CX-1 to CX-13, and the reading of the code found the gaps G1 to G7 between the code and the text. 0164 decision 10 settled CX-2 by restoring the rotation link, 0164.S1 closes CX-1, and 0164 decision 8 settled CX-12. This change carries the owner's answers to the others:

- CX-3 and G2: a playground bearer has no `issued_tokens` row and no parent, so revoking the subject it was minted from leaves it live, and the recursive-revocation request refuses it as a root although §13.3 says the request accepts it.
- CX-4: an impersonation bearer's `exp` is not bounded by the caller's bearer, and revoking the caller's bearer leaves the impersonation live (`pkg/credential/impersonation/service.go` sets `ExpiresAt: now.Add(req.Duration)` with no cap).
- CX-5 and G3: a token with no `issued_tokens` row cannot be named as a revocation root, so the tokens derived from it cannot be revoked through it.
- CX-6: a child is bounded by its subject's scope only, so a child minted from an unrestricted subject with a narrow actor is broader than the token that delegates it.
- CX-7: a request that lists every current action of a domain counts as strictly narrower than the domain wildcard while it admits the same tools today; the text does not say why.
- CX-8: a child's depth is its actor's plus one, while rule (b) says an exchange rejects a decrement without saying against which token.
- CX-9 and [TOKX-I14](../../issues/closed/TOKX/TOKX-I14-rotated-delegation-child-drops-its-act-claim.md): an exchange without an `actor_token` issues no `act` claim, so narrowing or rotating a delegation child drops the name of its delegator.
- CX-10 and G4: the Token Service does not read the caller token's revocation on a mint.
- CX-11: a full revoke of a user revokes the user's tokens by `sub` and not the tokens others derived from them.
- G1: a request without `audience` issues a token with no `aud` claim, although the parameter table names `lenny-gateway` as the default, so a self-exchange without `audience` is not detected as a rotation.
- G5, G6, and G7 are filed as issues and not fixed here.

## Scope

The change covers the rules above in §13.3, the playground mint invariants of §10.2, step 5 of §11.4, the `issued_tokens` columns of §12.2, and the `token.revoked` event of §16.7, and the code that carries them in `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/storage/issuedtokenstore`, `pkg/credential/impersonation`, `pkg/gateway/playground`, `pkg/gateway/api/admin`, and `pkg/auth/authmw`, with a migration.

It leaves out:

- G5, G6, and G7, which the owner asked to be filed as issues.
- The property tests of 0165.S2 and a TLA+ model of the concurrent orderings, which come later.
- [TOKX-I10](../../issues/TOKX/TOKX-I10-revocation-cascade-has-no-depth-or-time-bound.md), [TOKX-I13](../../issues/TOKX/TOKX-I13-derivation-committed-during-a-cascade-escapes-it.md), and [AUTHN-I1](../../issues/AUTHN/AUTHN-I1-no-way-to-lift-a-user-invalidation.md), which no fix here closes.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token, actor token, caller token | The `subject_token` and `actor_token` of an RFC 8693 exchange, and the `Authorization: Bearer` token that authenticates the request (§13.3 **Client authentication on `/v1/oauth/token`.**). |
| Lineage | The tree the `parent_jti` column of `issued_tokens` (§12.2) forms within a tenant. A lineage descendant of a token is a token whose chain of `parent_jti` values reaches it. |
| Impersonator link | The `impersonator_tenant_id` and `impersonator_jti` columns this change adds to an impersonation row, naming the platform-admin bearer that started it. |
| Counterexample (CX-n) | A sequence of operations the 0165 model found that ends in a state where a property is false, listed in 0165's `change.md` under "Counterexamples". |
| Gap (Gn) | A difference between the code and the text that 0165's reading of the code found, listed in 0165's `change.md` under "Current state". |

## High-level design

Each fix makes the rule the model checks hold for the scenario that broke it. A playground bearer gets an `issued_tokens` row whose parent is the pasted subject, so the recursive revocation reaches it and the revocation request finds it. An impersonation bearer expires no later than the admin bearer that started it, and its row names that bearer in a new pair of columns, which the Token Service follows across tenants after a recursive revocation commits. A recursive-revocation request whose root has no row records the root as revoked and cascades. A child mint also requires the actor's scope to grant the child's scope. An exchange without an actor copies the subject's `act` claim. Every mint re-reads the caller token's row and refuses a revoked caller. A full revoke follows the lineage of every token of the user. A request without `audience` requests `lenny-gateway`. Two rules that the code already keeps are stated: a domain wildcard is broader than a list of its current actions, and a child's depth is compared with its actor.

## Requirements

None by identifier. §13.3 is content of the placeholder page TOKX, and §10.2, §11.4, §12.2, and §16.7 carry no requirement identifiers for these rules yet.

## Current state

The statements under "Problem" are the state on `main` at `dce47dd79`, which includes 0164.S1.

## Detailed design

### TF-1 Playground bearer lineage (decision 1)

§10.2 **Playground mint invariants.** gains invariant 6: every playground mint records the bearer in `issued_tokens` after it signs and before it returns, with `parent_jti` set to the pasted subject's `jti` in the `apiKey` mode and null in the `oidc` and `dev` modes. The record runs in one transaction that takes the per-tenant audit advisory lock and repeats the step (1a) re-read of the subject, through a new `RecordWithSources` on `issuedtokenstore.Store`. A Postgres outage refuses the mint with `503 SERVICE_UNAVAILABLE`, and any other failure with `500 INTERNAL_ERROR`. The recursive revocation then reaches the bearer, and the revocation request finds a row for a playground subject, which matches the sentence of §13.3 that says the request accepts one.

### TF-2 Impersonation bearer bound and link (decision 2)

Rule 1 of **Platform-admin impersonation.** caps the bearer's `exp` at the caller bearer's `exp`; `authmw.Principal` carries the caller's `exp` for it. The row records the caller bearer's `tenant_id` and `jti` in two new columns, `impersonator_tenant_id` and `impersonator_jti` (migration 0200, with a partial index). The row predicate of rule 2 stays `act_sub IS NOT NULL AND parent_jti IS NULL`, so the new columns change no impersonation query. After a recursive-revocation request commits, the Token Service runs `RevokeImpersonationsByImpersonator` under `__all__`: it revokes the active impersonation rows whose `impersonator_tenant_id` is the request's tenant and whose `impersonator_jti` is in the cascade's subtree, revoked nodes included, writes a `token.revoked` row for each in the row's tenant, propagates each, and emits one `cross_tenant_read` event. Rule 7 states that this read needs no `platform-admin` caller because it reaches only rows derived from the request's own tenant.

### TF-3 Revocation root without a row (decision 3)

`RevokeCascadeByCaller` takes the root's row. When the root has no row, the same transaction inserts it and the cascade stamps it revoked with `explicit_revoke`, then walks its descendants. The Token Service builds the row from the verified `subject_token`, with the SHA-256 of its bytes.

### TF-4 Child bounded by its actor (decision 4)

`tokenexchange.Validate` refuses a child whose issued scope the actor's scope does not grant, with `400 invalid_scope` and reason `scope_exceeds_actor`. An issued scope with no value is granted only by an actor with an absent scope or `tools:*`.

### TF-5 Domain wildcard (decision 5)

§13.3 states that `tools:<domain>:*` grants more than a list of its domain's current actions, because it also grants actions added later, so the listed form is strictly narrower. No code changes.

### TF-6 Depth of a child (decision 6)

The parameter table, the claim table, and rule (b) state that a child's depth is the actor's plus one and that rule (b) compares a child with its actor and every other exchange with its subject. No code changes.

### TF-7 `act` on exchanges without an actor (decision 7)

The claim table's `act` row states that an exchange without an `actor_token` copies the subject's `act`. `tokenexchange.Token` carries `Act`, `toExchangeToken` maps the claim, and `Validate` copies it. The row's `act_sub` stays null, because a row with `act_sub` set names a child or an impersonation, and the lineage already holds through `parent_jti`. This closes [TOKX-I14](../../issues/closed/TOKX/TOKX-I14-rotated-delegation-child-drops-its-act-claim.md).

### TF-8 Caller revocation on a mint (decision 8)

Step (1a) of **Write-before-issue ordering.** also reads the caller token's row and refuses a revoked caller with `401 invalid_client` and reason `caller_token_revoked`. The caller is listed after the subject and the actor, and a caller that is also the subject or the actor is checked in that role; the impersonation predicate is not applied to the caller. `issuedtokenstore` gains the source role `caller_token`.

### TF-9 Full revoke follows the lineage (decision 9)

`RevokeBySubject` seeds a recursive walk with every row of the user in the tenant, revoked or not, and revokes every not-yet-revoked row in the subtree, returning all the `jti` values it revoked for the revocation cache. §11.4 step 5 and §13.3 **Rotation lineage and leaked tokens.** say so.

### TF-10 Default audience (decision 10)

`buildExchangeRequest` requests `lenny-gateway` when the request names no `audience`, so a self-exchange without `audience` of a subject whose audience is `lenny-gateway` is a rotation. Rule (c) states the default, and that a subject without an `aud` claim grants no audience, which the subset check already enforces.

## Worked example

alice in acme holds a root token R with no `scope` claim and audience `lenny-gateway`. She pastes R into the playground in the `apiKey` mode and receives a playground bearer P, whose row records R as its parent. She narrows R to N (`tools:me:read`) with R as caller; the request names no `audience`, so N carries `lenny-gateway`. She then tries to mint a child with R as subject and N as actor and caller, without `scope`: the child's scope would be unrestricted, which N's scope does not grant, so the request is refused with `scope_exceeds_actor`. She rotates R into R', which revokes R; a later mint with R as caller is refused with `caller_token_revoked`. A tenant administrator then sends a recursive-revocation request with R as root: the cascade revokes P, N, and R'. carol, a `platform-admin` whose bearer C started an impersonation of alice, revokes C with a revocation request in her own tenant, and the Token Service then revokes the impersonation row whose `impersonator_jti` is C.

## Limits, and alternatives rejected

- **Impersonation link through `parent_jti`.** Recording the admin bearer in `parent_jti` would make the impersonation row match neither the row predicate of rule 2 nor the tenant of its parent: the predicate identifies an impersonation row by a null `parent_jti`, a child row already has `act_sub` and `parent_jti` set, and the in-tenant walk could not reach a row in another tenant. A new predicate would need a third column to tell a child row from an impersonation row, and the walk would still need a cross-tenant step. A separate column pair keeps the predicate and the lineage index unchanged and makes the cross-tenant step explicit.
- **Impersonation link in the same transaction as the cascade.** The cascade runs under the request's tenant, and the impersonation rows are in other tenants, which the `__all__` context reaches only in its own transaction. The second transaction runs after the first commits; a failure of it returns `500 token_exchange_failed`, and a retry, whose root is then revoked, finishes it.
- **Cross-tenant read for every revocation request.** The impersonation step runs on every recursive-revocation request served by a durable store, because the Token Service cannot tell from the cascade's tenant which tokens started an impersonation. Each request therefore writes one `cross_tenant_read` row; revocation requests are rare enough that the extra row is acceptable.
- **Full revoke seeded by every row of the user.** Seeding only with the rows the full revoke newly revokes would miss the descendants of a token the user had already revoked, such as one rotated away from. Seeding with every row of the user reaches them.
- **Bearer possession.** The fixes bind tokens, not holders, as 0165 "Limits" states.

## Specification and non-specification changes

- `spec/13_security-model.md`, §13.3: the parameter and claim tables, rules (b) and (c), the child-mint scope bound, the domain-wildcard statement, step (1a), **Lineage of derived tokens.**, **Rotation lineage and leaked tokens.**, **Recursive-revocation request authorization.**, and **Platform-admin impersonation.** rules 1, 2, 4, and 7.
- `spec/10_gateway-internals.md`, §10.2: playground mint invariant 6 and the failure-mode row.
- `spec/11_policy-and-controls.md`, §11.4 step 5.
- `spec/12_storage-architecture.md`, §12.2: the `impersonator_tenant_id` and `impersonator_jti` columns and their index.
- `spec/16_observability.md`, §16.7 `token.revoked`.
- `migrations/0200_issued_tokens_impersonator.up.sql` and its down migration.
- `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/storage/issuedtokenstore`, `pkg/credential/impersonation`, `pkg/gateway/playground`, `pkg/gateway/api/admin`, `pkg/auth/authmw`, and `cmd/lenny-token-service`.

## Documentation

- `docs/client-guide/authentication.md` (client): the default audience, the child-mint bound by the actor, the copied `act` claim, the depth of a child, the revoked-caller refusal, the domain-wildcard statement, and a revocation root without a row.
- `docs/api/admin.md` (operator): the impersonation bearer's expiry and its end when the caller's bearer is revoked, and the full revoke's lineage reach.
- `docs/operator-guide/security.md` (operator): what a recursive revocation and a full revoke now reach.
- `docs/operator-guide/web-playground.md` (operator): revoking the pasted token revokes the playground bearers minted from it.

## Tests

- Unit: `pkg/tokenexchange` (child bound by the actor, the copied `act`, depth of a child), `pkg/tokenservice` (default audience and rotation, revoked caller, revocation root without a row, impersonation step after a cascade), `pkg/credential/impersonation` (expiry cap, impersonator link), `pkg/gateway/playground` (lineage record and its refusals), `pkg/gateway/api/admin` (caller expiry passed through), `pkg/storage/issuedtokenstore` (caller source role), and `migrations` (migration 0200).
- Component: `tests/component/issuedtokencascade` (root without a row, playground row cascade, impersonator link across tenants, full revoke through the lineage, caller revoked in step (1a)).
- Contract: `tests/contract/oauth_token` where the wire responses change (the `scope_exceeds_actor` and `caller_token_revoked` refusals).
- Security: `tests/security` for the revoked caller and the child bound by its actor.
