# Token exchange narrowing and identity rules

Status: `In progress`, revision 4, 2026-11-22.

## Problem

The reviews of 0163.S1 on 2026-11-19 filed five defects in the token exchange of `POST /v1/oauth/token` and in the tokens that relate to it. Each predates 0163.S1.

- [TOKX-I3](../../issues/closed/TOKX/TOKX-I3-exchange-cannot-narrow-a-subject-without-scope.md) (Medium): the subset check of §13.3 rule (a) compares literal strings, so an exchange cannot narrow a `subject_token` that carries no `scope` claim, nor narrow `tools:*` or `tools:<domain>:*`. No token an operator obtains carries a `scope` claim, so the §25.1 narrowing flow produces no scope-restricted token. Rule (e) also drops a child's `authorized_tools` when the issued scope is absent.
- [TOKX-I4](../../issues/closed/TOKX/TOKX-I4-exchange-drops-the-playground-origin-claim.md) (Low): a playground bearer presented as `subject_token` or `actor_token` is exchanged for a token without the `origin` claim, which escapes the playground logout revocation and the playground session caps.
- [TOKX-I5](../../issues/closed/TOKX/TOKX-I5-exchange-does-not-check-the-caller-identity.md) (Low): the exchange compares the caller with the subject on `tenant_id` only, although §13.3 **Client authentication on `/v1/oauth/token`.** requires a caller of another identity to narrow scope. A token derived without an `actor_token` records no parent, so the recursive revocation of its subject does not reach it.
- [TOKX-I6](../../issues/closed/TOKX/TOKX-I6-impersonation-bearer-ignores-the-caller-scope.md) (Low): a platform-admin impersonation bearer carries no `scope` claim whatever the caller's scope.
- [TOKX-I7](../../issues/closed/TOKX/TOKX-I7-token-exchanged-audit-payload-scope-fields.md) (Low): the `token.exchanged` row carries one `scope` field where §16.7 names `requested_scope` and `granted_scope`.
- [TOKX-I9](../../issues/closed/TOKX/TOKX-I9-revocation-request-accepts-any-caller-and-a-revoked-root.md) (Medium), filed by the review of this step: the recursive-revocation request accepts any caller in the tenant and a revoked caller, so a holder of a leaked token that its owner rotated away from could revoke the owner's current token through the rotation edge. The same review found that a child mint lets a caller holding another user's token mint a child at that user's full scope (decisions 7 and 8).

## Scope

The change covers the scope check, the origin check, and the caller-identity check of every exchange `POST /v1/oauth/token` serves, the lineage the Token Service records for the token an exchange issues, who may send the recursive-revocation request ([TOKX-I9](../../issues/closed/TOKX/TOKX-I9-revocation-request-accepts-any-caller-and-a-revoked-root.md)), the scope claim of the impersonation bearer, and the scope fields of the `token.exchanged` row.

It leaves out:

- The playground mint, `POST /v1/playground/token`, which narrows by intersection and which change 0162 settled.
- The origin check of the recursive-revocation request (`requested_token_type=...:access_token:revoked`). Its subject may carry the `origin: "playground"` claim, because revoking a playground bearer narrows nothing.
- The other `token.exchanged` payload fields whose names differ from §16.7 (`subject_sub`, `jti`, and the absent `subject_token_jti` and `actor_sub`). The review of this step files them as an issue.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token | The `subject_token` of an RFC 8693 exchange (§13.3). |
| Canonical scope value | A value that §25.1 **Scope value syntax.** admits: `tools:<domain>:<action>` with a domain from the closed taxonomy, or `tools:*`. |
| Grant | A scope set grants a value when §25.1 **Matching.** permits it: an absent claim and `tools:*` grant every value, `tools:<domain>:*` grants every action of its domain, and any other value grants itself. |
| Strictly narrower | A requested scope is strictly narrower than a subject's scope when the subject grants every requested value and the requested set does not grant every value of the subject's scope. An absent subject claim counts as `tools:*`, and `tools:*` counts as the `tools:<domain>:*` value of every domain in the closed taxonomy. |
| Different caller | A caller whose `sub` differs from the subject token's, or whose `caller_type` differs from the subject token's when both tokens carry one. |
| Derived token | A token an exchange issues from a subject token. Its parent is the token the recursive revocation walks from. |

## High-level design

The exchange reads scope values the way the scope gates read them. A requested value is accepted when it is canonical and the subject's scope grants it, so an operator can narrow a token that carries no `scope` claim, or a `tools:*` or `tools:<domain>:*` token, to any narrower set. A child mint keeps the `authorized_tools` entries its issued scope grants, which keeps the whole allowlist when the issued scope is absent.

Three refusals close the paths by which an exchange outlives or outreaches its subject. A playground bearer is refused as `subject_token` or `actor_token` with `invalid_grant`. A different caller must request a scope strictly narrower than the subject's, so it cannot take a copy of another user's token at equal reach. A child mint with an `actor_token` requires the actor token to belong to the caller, and a subject that belongs to another user than the actor must be narrowed strictly.

Every derived token records a parent: the actor token for a child mint, as today, and the subject token for every other exchange, a rotation included (decision 10). The recursive revocation of a token therefore reaches every token derived from it by narrowing, lease issuance, an exchange from another caller, or a rotation, so a holder who rotates a leaked token loses the successor when the owner revokes the leaked token. The same link lets a holder of two of a user's leaked tokens, one live and one the user rotated away from, revoke the user's current token; §13.3 states that trade-off, and the operator guide answers it with a full revoke of the user.

The recursive-revocation request is accepted only from the token's own user or a tenant administrator, and is refused when the caller's own token is revoked. Any token that carries the user's identity may send it (decision 11), and it revokes only its subject token and that token's lineage descendants. A root that is already revoked still cascades, so an owner finishes a cleanup with another live token.

The impersonation bearer carries the impersonating caller's `scope` claim, and the `token.exchanged` row records both the requested scope and the granted scope.

## Requirements

None by identifier. §13.3 (Credential Flow), §16.7 (Section 25 Audit Events), and §25.1 (Design Philosophy and Agent Model) are content of the placeholder pages TOKX, AUDIT, and OPS and carry no requirement identifiers yet.

## Current state

- `pkg/tokenexchange/tokenexchange.go`, `resolveIssuedScope`, `isSubset`, and `intersect`: literal membership for the subset check and the rule (e) intersection; `Validate` compares caller and subject on `tenant_id` only.
- `pkg/tokenservice/tokenservice.go`, `authenticateCaller`: only the caller takes the origin check. `delegationLineage` records `parent_jti` for an `actor_token` mint only. `exchangeAuditPayload` has one `scope` field.
- `pkg/credential/impersonation/service.go`, `sign` and `impersonationRow`: no `scope` on the bearer or the row.
- `docs/operator-guide/agent-operability.md`, "Issuing a scoped token": the example narrows an unscoped admin token, which the Token Service refuses today.

## Detailed design

### TX-1 Narrowing by the scope grammar

`resolveIssuedScope` keeps the 0163 default for a request without scope. Otherwise it parses the subject's scope with `scopes.Parse` and accepts the request when every requested value parses with `scopes.ParseScope` and the parsed subject set `Matches` it; otherwise it refuses with `invalid_scope` and reason `scope_not_subset`. A subject scope that does not parse refuses every explicit request. A non-canonical requested value is refused even when the subject carries it literally, because the scope gates refuse a claim that does not parse.

For a child mint, `Validate` keeps the allowlist entries the issued scope `Matches`. An absent issued scope matches every entry, so the allowlist survives whole, and an issued scope that does not parse keeps no entry.

### TX-2 Playground-origin source tokens

`tokenexchange.Token` carries the `origin` claim. `Validate` refuses a subject or actor whose origin is `playground` with `invalid_grant` and reason `subject_token_playground` or `actor_token_playground`. The refusal goes through `validateExchange`, so it writes the rejected `token.exchanged` row. The revocation request does not call `Validate` and is unaffected.

### TX-3 Different caller

When the exchange carries no `actor_token` and the caller is a different caller, `Validate` requires the requested scope to be strictly narrower than the subject's and refuses otherwise with `invalid_scope` and reason `scope_not_narrowed`. A request without scope is never strictly narrower, so a different caller cannot take the 0163 default. `strictlyNarrower` compares a subject's `tools:*` as the domain wildcard of every domain, so a request that lists every domain wildcard is not narrower than an unscoped or `tools:*` subject. A `caller_type` that one of the two tokens does not carry is not compared, because an IdP caller token carries none; the `sub` comparison still applies to it.

### TX-4 Lineage of every derived token

`derivedLineage`, which replaces `delegationLineage`, returns the actor's `sub` and `jti` for an `actor_token` mint, as before, and an empty `act_sub` with the subject's `jti` as `parent_jti` for every other exchange, a self-rotation included (decision 10). `RevokeCascade` walks `parent_jti`, so the revocation reaches every derived token. A row with `parent_jti` set is never an impersonation row under §13.3 **Platform-admin impersonation.** rule 2. A rotation names its predecessor, so revoking a rotated-out token reaches its successor, a derived token cannot leave its ancestor's lineage by rotating, and a token derived from the predecessor before the rotation still names the predecessor. A rotated child records the child as parent and no `act_sub`, because the rotated token carries no `act` claim. A self-rotation carries no `actor_token`, presents the caller token as the subject, and issues the subject's scope and audience, as §13.3 states. The rotation itself revokes only the predecessor. Revision 3's `inheritLineage`, which copied the predecessor's `parent_jti` and `act_sub` under decision 7, is removed. Migration 0199 adds the partial index `(tenant_id, parent_jti) WHERE parent_jti IS NOT NULL`, so each step of the cascade walk reads an index rather than the tenant's rows.

### TX-5 Impersonation bearer scope

`IssueRequest` carries the caller's raw `scope` claim, which the admin handler reads from the principal. `sign` stamps it on the bearer, and `impersonationRow` records it in the row's `scope` column. An absent caller claim leaves the bearer without one.

### TX-6 Audit payload scope fields

`exchangeAuditPayload` replaces `scope` with `requested_scope`, the request's `scope` parameter as split, and `granted_scope`, the issued scope. A rejected row carries `requested_scope` and no `granted_scope`.

### TX-7 Child-mint identity

`checkCallerIdentity` hands an exchange with an `actor_token` to `checkChildMintIdentity` (decision 8). It refuses an actor whose `sub` differs from the caller's with `invalid_grant` and reason `actor_token_not_caller`, and, when the subject's `sub` differs from the actor's, requires `strictlyNarrower` as TX-3 does, refusing otherwise with `invalid_scope` and reason `scope_not_narrowed`. A child whose subject, actor, and caller share a `sub` is unaffected.

### TX-8 Revocation request authorization

`handleRevocationRequest` checks the tenant as before, then `authorizeRevocation`: the caller's `sub` equals the subject's, or the caller holds `tenant-admin` or `platform-admin`; any other caller receives `403 unauthorized_client` (decision 9). The caller's roles are its platform-managed assignment from the users table (`userstore.PlatformRoles`, wired in `cmd/lenny-token-service` as on the gateway) when one exists, and its token's `roles` claim otherwise; a failed lookup refuses the request with `503 token_store_unavailable` or `500 token_exchange_failed`. The store method `RevokeCascadeByCaller` reads the caller token's `issued_tokens` row `FOR SHARE` in the revoking transaction and returns `ErrCallerRevoked`, which the handler maps to `401 invalid_client`, when that row is revoked. A caller with no row passes. `RevokeCascade` delegates to it with no caller. An already-revoked root still cascades. Two requests that revoke each other's caller token deadlock on the share locks at most once, and `RevokeCascadeByCaller` retries a transaction Postgres aborts as a deadlock victim, so the second request is refused.

## Worked example

alice holds a platform-admin token with no `scope` claim. She exchanges it for `tools:health:* tools:diagnostics:read`: both values are canonical and the absent claim grants them, so the Token Service issues the narrowed token and records alice's token as its parent. bob, in the same tenant, presents alice's narrowed token as `subject_token` without `scope`: he is a different caller and the request is not strictly narrower, so the exchange returns `400 invalid_scope`. With `scope=tools:health:read` it is accepted, and the token he receives records alice's narrowed token as its parent. When alice revokes her original token with the revocation request, the cascade revokes her narrowed token and bob's. bob cannot send that request himself: he is neither alice nor a tenant administrator, so the Token Service returns `403 unauthorized_client`. Had her original token leaked to carol and carol rotated it first, alice's revocation of the leaked token would also revoke carol's successor, which records the leaked token as its parent. Had alice rotated away from the leaked token herself, carol, holding only the leaked token, could not revoke alice's replacement, because the leaked token is revoked and the request is refused with `401 invalid_client`. Had a second token of alice's that is still valid leaked to carol as well, carol could revoke alice's replacement through it, and alice would sign in again; an administrator answers such a leak with a full revoke of alice.

## Limits, and alternatives rejected

- The options not chosen are listed with each decision in `decisions.md`.
- A rotation records its predecessor (decision 10), so a holder of two of a user's leaked tokens, one live and one the user rotated away from, can revoke the user's current token. The user signs in again, and an administrator fully revokes the user when more than one token may have leaked. No rotation lineage avoids both this and a leak holder keeping a rotated token (change 0165, scenario CX-2).
- Any token of the user's may send the revocation request (decision 11), including a narrowly scoped one an agent holds; the request reaches only the token it presents and that token's descendants.
- A self-rotation adds one level to a chain, as other exchanges do ([TOKX-I10](../../issues/TOKX/TOKX-I10-revocation-cascade-has-no-depth-or-time-bound.md)).
- A `platform-admin` revokes through the revocation request only within its own tenant, because the tenant check runs first.
- A refused revocation request writes no audit row ([TOKX-I12](../../issues/TOKX/TOKX-I12-refused-revocation-request-writes-no-audit-row.md)).
- Recording a parent for every exchange lets a caller build a long chain of derived tokens. The index of TX-4 keys each step of the cascade, and the change sets no bound on the depth of a chain or on the duration of a cascade ([TOKX-I10](../../issues/TOKX/TOKX-I10-revocation-cascade-has-no-depth-or-time-bound.md)).

## Specification and non-specification changes

- `spec/13_security-model.md`: §13.3 the `scope` row of the RFC 8693 parameter table, **Scope narrowing enforcement.**, **Platform-admin impersonation.** rule 1, the recursive-revocation paragraph, **Lineage of derived tokens.**, the new **Rotation lineage and leaked tokens.** and **Recursive-revocation request authorization.**, and **Client authentication on `/v1/oauth/token`.**
- `spec/12_storage-architecture.md`: §12.2 the `parent_jti` column and the indexes of `TokenIssuanceStore`.
- `spec/25_agent-operability.md`: §25.1 **Issuing a scoped token.**
- `spec/16_observability.md`: §16.7 `token.revoked` names the tokens derived from the revoked token as the reach of a cascade. The `token.exchanged` row adopts the §16.7 scope field names, with no edit there.
- `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/storage/issuedtokenstore` (`RevokeCascadeByCaller`), `cmd/lenny-token-service` (the platform role resolver), `pkg/credential/impersonation`, and `pkg/gateway/api/admin` (`impersonation.go`).
- `migrations/0199_issued_tokens_parent_index`: the lineage index of TX-4.
- Tests that sent non-canonical scope values (`sessions:read`) send canonical ones.
- `tests/registers/spec-section-citations.yaml`: the counts of the packages whose code and tests the step changes.

## Documentation

- `docs/operator-guide/agent-operability.md` (operator): "Issuing a scoped token" states the grammar, the different-caller rule, and a working example.
- `docs/client-guide/authentication.md` (client developer): the token exchange section states the grammar, the playground refusal, the different-caller rule, the child-mint rule, the lineage of a derived token and of a rotation, and who can revoke a token.
- `docs/api/admin.md` (operator): the token exchange paragraph states the same rules, including the child-mint rule and the revocation request's callers, and the impersonation paragraph states the bearer's scope.
- `docs/operator-guide/security.md` (operator): the bearer-token paragraph states the different-caller and child-mint rules, the playground refusal, the revocation of derived and rotated tokens, the two-leaked-tokens trade-off with the instruction to fully revoke a user when more than one of the user's tokens may have leaked, who can revoke a token, and the impersonation bearer's scope.
- `docs/operator-guide/agent-operability.md` (operator): "Issuing a scoped token" also states who can revoke a token.
- `docs/operator-guide/web-playground.md` (operator): the revocation paragraph states that a playground bearer cannot be exchanged for a token that outlives logout.

## Tests

Unit tier, in `pkg/tokenexchange`: narrowing an unscoped subject; `tools:*` and `tools:<domain>:*` subjects; a non-canonical requested value refused; a playground subject and a playground actor refused with `invalid_grant`; a different caller with an equal scope, with no scope, and with a narrower scope; a different `caller_type`; a child mint by a different caller unaffected; the rule (e) allowlist kept whole for an absent issued scope and filtered by wildcards. Cites §13.3 (Credential Flow).

Unit tier, in `pkg/tokenservice`: the derived-token lineage for a narrowing, a different-caller exchange, and a child mint; the audit row's `requested_scope` and `granted_scope` for an accepted exchange without scope and for a rejected one; a playground subject refused with a rejected audit row. Cites §13.3 and §16.7.

Unit tier, in `pkg/credential/impersonation` and `pkg/gateway/api/admin`: the bearer and the row carry the caller's scope, and no scope when the caller carries none, from the service and through the admin handler. Unit tier, in `migrations`: migration 0199 creates and drops the lineage index. Cites §13.3 and §12.2.

Contract tier, in `tests/contract/oauth_token`: over HTTP, a narrowing of an unscoped subject, a playground subject and actor refused with `400 invalid_grant`, and a different caller refused with `400 invalid_scope` for an equal scope and accepted for a narrower one. Cites §13.3.

Component tier, in `tests/component/issuedtokencascade`: on Postgres, the recursive revocation of a subject revokes a token narrowed from it and a token another caller derived from it. In `tests/component/migrations`: the migrated schema carries the lineage index. Cites §13.3 and §12.2.

Security tier, in `tests/security`: with the Token Service built as `cmd/lenny-token-service` builds it, a different caller cannot copy another user's token at equal scope, and a playground bearer cannot be exchanged. Cites §13.3.

Decisions 7 to 9 add the following tests. Unit tier, in `pkg/tokenexchange` and `pkg/tokenservice`: a child mint of another user's token refused at equal or no scope and accepted narrower; an actor token not the caller's refused; a same-user child mint unaffected; a rotation records no parent; a revocation request accepted from the owner, a tenant-admin, and a platform-admin, refused from another user and a tenant-viewer, and refused for a revoked caller. Contract tier, in `tests/contract/oauth_token`: the child-mint rules and the refusal of another user's revocation request over HTTP. Component tier, in `tests/component/issuedtokencascade`: on Postgres, a rotation's token of a root records no parent and survives the revocation of the token it replaced, whose holder revokes nothing; a rotated narrowing, a rotated other-caller exchange, and a rotated child stay under their ancestors; the cascade from an already-revoked root revokes the tokens derived from it; another user, an administrator, a revoked caller, and mutual revocations. Unit tier also covers the platform-managed role assignment and a failed lookup. Security tier, in `tests/security`: a child mint cannot copy another user's token, an actor token must be the caller's, and another user cannot revoke a user's token. Cites §13.3.

Decision 10 changes the following tests. Unit tier, in `pkg/tokenservice`: a self-rotation records the replaced token as its parent. Component tier, in `tests/component/issuedtokencascade`: on Postgres, a rotation records the token it replaces, whose holder alone revokes nothing, and revoking the replaced token revokes its successor and the tokens derived from it; a leak holder's rotated token is revoked with the leaked token; a rotated narrowing, a rotated other-caller exchange, and a rotated child stay under their ancestors through the replaced token; and a holder of two of a user's leaked tokens revokes the user's rotated token and nothing outside its lineage, which documents the trade-off §13.3 accepts. Cites §13.3.
