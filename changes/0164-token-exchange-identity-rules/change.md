# Token exchange narrowing and identity rules

Status: `Decided`, revision 1, 2026-11-20.

## Problem

The reviews of 0163.S1 on 2026-11-19 filed five defects in the token exchange of `POST /v1/oauth/token` and in the tokens that relate to it. Each predates 0163.S1.

- [TOKX-I3](../../issues/TOKX/TOKX-I3-exchange-cannot-narrow-a-subject-without-scope.md) (Medium): the subset check of §13.3 rule (a) compares literal strings, so an exchange cannot narrow a `subject_token` that carries no `scope` claim, nor narrow `tools:*` or `tools:<domain>:*`. No token an operator obtains carries a `scope` claim, so the §25.1 narrowing flow produces no scope-restricted token. Rule (e) also drops a child's `authorized_tools` when the issued scope is absent.
- [TOKX-I4](../../issues/TOKX/TOKX-I4-exchange-drops-the-playground-origin-claim.md) (Low): a playground bearer presented as `subject_token` or `actor_token` is exchanged for a token without the `origin` claim, which escapes the playground logout revocation and the playground session caps.
- [TOKX-I5](../../issues/TOKX/TOKX-I5-exchange-does-not-check-the-caller-identity.md) (Low): the exchange compares the caller with the subject on `tenant_id` only, although §13.3 **Client authentication on `/v1/oauth/token`.** requires a caller of another identity to narrow scope. A token derived without an `actor_token` records no parent, so the recursive revocation of its subject does not reach it.
- [TOKX-I6](../../issues/TOKX/TOKX-I6-impersonation-bearer-ignores-the-caller-scope.md) (Low): a platform-admin impersonation bearer carries no `scope` claim whatever the caller's scope.
- [TOKX-I7](../../issues/TOKX/TOKX-I7-token-exchanged-audit-payload-scope-fields.md) (Low): the `token.exchanged` row carries one `scope` field where §16.7 names `requested_scope` and `granted_scope`.

## Scope

The change covers the scope check, the origin check, and the caller-identity check of every exchange `POST /v1/oauth/token` serves, the lineage the Token Service records for the token an exchange issues, the scope claim of the impersonation bearer, and the scope fields of the `token.exchanged` row.

It leaves out:

- The playground mint, `POST /v1/playground/token`, which narrows by intersection and which change 0162 settled.
- The recursive-revocation request itself (`requested_token_type=...:access_token:revoked`). Its subject may carry the `origin: "playground"` claim, because revoking a playground bearer narrows nothing.
- The other `token.exchanged` payload fields whose names differ from §16.7 (`subject_sub`, `jti`, and the absent `subject_token_jti` and `actor_sub`). The review of this step files them as an issue.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token | The `subject_token` of an RFC 8693 exchange (§13.3). |
| Canonical scope value | A value that §25.1 **Scope value syntax.** admits: `tools:<domain>:<action>` with a domain from the closed taxonomy, or `tools:*`. |
| Grant | A scope set grants a value when §25.1 **Matching.** permits it: an absent claim and `tools:*` grant every value, `tools:<domain>:*` grants every action of its domain, and any other value grants itself. |
| Strictly narrower | A requested scope is strictly narrower than a subject's scope when the subject grants every requested value and the requested set does not grant every value of the subject's scope. An absent subject claim counts as `tools:*`. |
| Different caller | A caller whose `sub` differs from the subject token's, or whose `caller_type` differs from the subject token's when both tokens carry one. |
| Derived token | A token an exchange issues from a subject token. Its parent is the token the recursive revocation walks from. |

## High-level design

The exchange reads scope values the way the scope gates read them. A requested value is accepted when it is canonical and the subject's scope grants it, so an operator can narrow a token that carries no `scope` claim, or a `tools:*` or `tools:<domain>:*` token, to any narrower set. A child mint keeps the `authorized_tools` entries its issued scope grants, which keeps the whole allowlist when the issued scope is absent.

Three refusals close the paths by which an exchange outlives or outreaches its subject. A playground bearer is refused as `subject_token` or `actor_token` with `invalid_grant`. A different caller must request a scope strictly narrower than the subject's, so it cannot take a copy of another user's token at equal reach. A child mint with an `actor_token` keeps its own rules.

Every derived token records a parent: the actor token for a child mint, as today, and the subject token for every other exchange. The recursive revocation of a token therefore reaches every token derived from it by rotation, narrowing, lease issuance, or an exchange from another caller.

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

When the exchange carries no `actor_token` and the caller is a different caller, `Validate` requires the requested scope to be strictly narrower than the subject's and refuses otherwise with `invalid_scope` and reason `scope_not_narrowed`. A request without scope is never strictly narrower, so a different caller cannot take the 0163 default. A `caller_type` that one of the two tokens does not carry is not compared, because an IdP caller token carries none; the `sub` comparison still applies to it.

### TX-4 Lineage of every derived token

`delegationLineage` returns the actor's `sub` and `jti` for an `actor_token` mint, as today, and an empty `act_sub` with the subject's `jti` as `parent_jti` for every other exchange. `RevokeCascade` walks `parent_jti`, so the revocation reaches every derived token. A row with `parent_jti` set is never an impersonation row under §13.3 **Platform-admin impersonation.** rule 2. A rotation records its predecessor as parent, so revoking a rotated-out token also revokes its successors; the rotation itself still revokes only the predecessor.

### TX-5 Impersonation bearer scope

`IssueRequest` carries the caller's raw `scope` claim, which the admin handler reads from the principal. `sign` stamps it on the bearer, and `impersonationRow` records it in the row's `scope` column. An absent caller claim leaves the bearer without one.

### TX-6 Audit payload scope fields

`exchangeAuditPayload` replaces `scope` with `requested_scope`, the request's `scope` parameter as split, and `granted_scope`, the issued scope. A rejected row carries `requested_scope` and no `granted_scope`.

## Worked example

alice holds a platform-admin token with no `scope` claim. She exchanges it for `tools:health:* tools:diagnostics:read`: both values are canonical and the absent claim grants them, so the Token Service issues the narrowed token and records alice's token as its parent. bob, in the same tenant, presents alice's narrowed token as `subject_token` without `scope`: he is a different caller and the request is not strictly narrower, so the exchange returns `400 invalid_scope`. With `scope=tools:health:read` it is accepted, and the token he receives records alice's narrowed token as its parent. When alice revokes her original token with the revocation request, the cascade revokes her narrowed token and bob's.

## Limits, and alternatives rejected

- The options not chosen are listed with each decision in `decisions.md`.
- A lineage that records the subject's parent for rotations makes the revocation of a rotated-out token reach its successors. The owner's answer to SM-7 records a parent for every exchange, and the change applies it to rotations too.
- `issued_tokens.parent_jti` carries no index. Recording a parent for every exchange makes the cascade walk more rows; the change adds no index.

## Specification and non-specification changes

- `spec/13_security-model.md`: §13.3 the `scope` row of the RFC 8693 parameter table, **Scope narrowing enforcement.**, **Platform-admin impersonation.** rule 1, the recursive-revocation paragraph, and **Client authentication on `/v1/oauth/token`.**
- `spec/12_storage-architecture.md`: §12.2 the `parent_jti` column of `TokenIssuanceStore`.
- `spec/25_agent-operability.md`: §25.1 **Issuing a scoped token.**
- `spec/16_observability.md`: no edit; the code adopts the §16.7 names.
- `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/credential/impersonation`, and `pkg/gateway/api/admin` (`impersonation.go`).
- Tests that sent non-canonical scope values (`sessions:read`) send canonical ones.
- `tests/registers/spec-section-citations.yaml`: the counts of the packages whose code and tests the step changes.

## Documentation

- `docs/operator-guide/agent-operability.md` (operator): "Issuing a scoped token" states the grammar, the different-caller rule, and a working example.
- `docs/client-guide/authentication.md` (client developer): the token exchange section states the grammar, the playground refusal, the different-caller rule, and the lineage of a derived token.
- `docs/api/admin.md` (operator): the token exchange paragraph states the same rules, and the impersonation paragraph states the bearer's scope.
- `docs/operator-guide/security.md` (operator), when it restates the narrowing rules.

## Tests

Unit tier, in `pkg/tokenexchange`: narrowing an unscoped subject; `tools:*` and `tools:<domain>:*` subjects; a non-canonical requested value refused; a playground subject and a playground actor refused with `invalid_grant`; a different caller with an equal scope, with no scope, and with a narrower scope; a different `caller_type`; a child mint by a different caller unaffected; the rule (e) allowlist kept whole for an absent issued scope and filtered by wildcards. Cites §13.3 (Credential Flow).

Unit tier, in `pkg/tokenservice`: the derived-token lineage for a narrowing, a different-caller exchange, and a child mint; the audit row's `requested_scope` and `granted_scope` for an accepted exchange without scope and for a rejected one; a playground subject refused with a rejected audit row. Cites §13.3 and §16.7.

Unit tier, in `pkg/credential/impersonation`: the bearer and the row carry the caller's scope, and no scope when the caller carries none. Cites §13.3.

Contract tier, in `tests/contract/oauth_token`: over HTTP, a narrowing of an unscoped subject, a playground subject and actor refused with `400 invalid_grant`, and a different caller refused with `400 invalid_scope` for an equal scope and accepted for a narrower one. Cites §13.3.

Component tier, in `tests/component/issuedtokencascade`: on Postgres, the recursive revocation of a subject revokes a token narrowed from it and a token another caller derived from it. Cites §13.3 and §12.2.

Security tier, in `tests/security`: with the Token Service built as `cmd/lenny-token-service` builds it, a different caller cannot copy another user's token at equal scope, and a playground bearer cannot be exchanged. Cites §13.3.
