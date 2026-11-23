# A token exchange without scope issues the subject token's scope

Status: `In progress`, revision 1, 2026-11-19.

## Problem

`POST /v1/oauth/token` called without a `scope` parameter issues a token whose `scope` claim is empty, and an empty claim carries no scope restriction. A caller holding a bearer narrowed to a scope such as `tools:health:read` can therefore exchange it for a token limited only by its roles, which is broader than the subject token it presented.

Evidence from the tree:

- `pkg/tokenexchange/tokenexchange.go`, `Validate` and `isSubset`: the scope check is `isSubset(r.Requested.Scope, r.Subject.Scope)`, and `isSubset` returns true for an empty candidate. `Validate` then issues `Scope: r.Requested.Scope`, which is empty.
- `pkg/tokenservice/tokenservice.go`, `buildExchangeRequest`: the requested scope is `splitScope(req.Scope)`, with no default from the subject token. `mintToken` writes `Scope: strings.Join(issued.Scope, " ")`, which is the empty string when the request omitted `scope`.
- `pkg/common/scopes/scopes.go`, `Set.Present` and `Set.Matches`, and `pkg/gateway/api/admin/scope_enforce.go`, `scopeGate.ServeHTTP`: an empty claim is not present, and a claim that is not present passes every scope gate.

The specification does not settle the case. §13.3 (Credential Flow), **Scope narrowing enforcement.**, states that the Token Service rejects a requested `scope` value the subject token does not carry, and says nothing about a request that names no scope. §25.1 (Design Philosophy and Agent Model), **Matching.**, reads an absent `scope` claim as no scope restriction. The security review of 0162.S1 found the gap on 2026-11-19 and filed it as [TOKX-I2](../../issues/closed/TOKX/TOKX-I2-exchange-without-scope-issues-an-unrestricted-token.md).

## Scope

The change covers the scope that `POST /v1/oauth/token` issues when the request carries no `scope` parameter, for every exchange the endpoint serves: the self-rotation grant, scope narrowing, credential-lease issuance, and `a2a_delegation` child minting with an `actor_token`. It edits the `scope` row of the §13.3 RFC 8693 parameter table, §13.3 **Scope narrowing enforcement.**, and §25.1 **Issuing a scoped token.**

It leaves out:

- The playground mint, `POST /v1/playground/token`, which narrows the subject's scope by intersection with `playground_allowed_scope` rather than through the token exchange. Change 0162 closes the same defect on that path.
- The tokens the gateway mints in process without a subject token: the bootstrap admin token, platform-admin impersonation bearers, and the embedded-mode development token. None of them narrows a subject token, so the default this change states does not apply to them.
- The `audience` parameter. The change does not alter what an exchange without `audience` issues.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token | The `subject_token` of an RFC 8693 exchange: the token whose identity and capabilities the issued token derives from (§13.3). |
| Narrowed subject | A subject token whose `scope` claim is present and non-empty, so the scope gates of §25.1 restrict it. |
| Absent claim | A `scope` claim that is missing or empty. §25.1 (**Matching.**) reads it as no scope restriction. |
| Default scope | The `scope` claim the Token Service issues when the request carries no `scope` parameter. |

## High-level design

When an exchange request names no scope, the Token Service issues the subject token's own scope. The issued token is then never broader than the subject token, because it carries exactly the scopes the subject carried. A subject token with no scope claim carries no restriction under §25.1, so the token issued from it carries none either, and the exchange keeps the subject's unrestricted reach rather than granting a new one. A request that names a scope behaves as today: a subset of the subject's scope is issued as requested, and any value outside it is refused with `invalid_scope`.

## Requirements

None by identifier. The change edits §13.3 (Credential Flow), which is content of the placeholder page TOKX and has no requirement identifiers yet.

## Current state

The specification is silent on the case, and the code issues an empty claim, as "Problem" states.

## Detailed design

### DS-1 Default scope

`tokenexchange.Validate` computes the issued scope once. When the request's scope is empty, the issued scope is the subject's scope; otherwise it is the requested scope, after the subset check. The default sits in the validator rather than in `buildExchangeRequest`, so every caller of `Validate` inherits it, and the checks and the `authorized_tools` intersection of §13.3 rule (e) read the issued scope.

Consequences on the exchanges the endpoint serves:

- A self-rotation without `scope` issues the subject's scope, so `isSelfRotation` in `pkg/tokenservice/tokenservice.go` now recognises it as a rotation for a narrowed subject and revokes the superseded token, as it already did for a subject with an absent claim. Its `token.exchanged` row changes `exchange_type` from `scope_narrow` to `admin_rotation`, which `classifyExchangeType` returns for a rotation.
- A child-minting exchange without `scope` intersects the subject's `authorized_tools` with the subject's scope rather than with an empty scope.
- The accepted `token.exchanged` audit row and the RFC 8693 response `scope` field carry the issued scope. A rejected row still carries the requested scope.
- The default applies when the `scope` parameter is absent or empty, in a form body and in a JSON body alike, because `splitScope` reads both as no value.

### DS-2 Specification text

- §13.3, the `scope` row of the RFC 8693 parameter table: a request without `scope` receives the subject token's scope.
- §13.3, **Scope narrowing enforcement.**: the enforcement list states the default, and states that a subject token with an absent claim yields an issued token with an absent claim, which §25.1 (**Matching.**) reads as no restriction. Rule (e) intersects `authorized_tools` with the issued token's scope, which names the scope the default produces when the request carries none.
- §25.1, **Issuing a scoped token.**: the paragraph states the default and points to §13.3.

## Limits, and alternatives rejected

The change does not narrow a subject token with an absent claim. Such a token is already unrestricted by scope, and the exchange cannot grant more than its roles allow. An explicit narrowing of such a subject is refused with `invalid_scope` today, because the subset check compares literal values; [TOKX-I3](../../issues/closed/TOKX/TOKX-I3-exchange-cannot-narrow-a-subject-without-scope.md) records that gap for the owner, and the change leaves it as it is.

The alternative not chosen was option (b) of question SM-1, refusing an exchange without `scope` with `invalid_scope` when the subject's scope is present. It breaks the self-rotation grant for narrowed tokens, whose callers would have to repeat the scope, and RFC 8693 leaves the default to the authorization server.

## Specification and non-specification changes

- `spec/13_security-model.md`: §13.3 Credential Flow, the `scope` row of the RFC 8693 parameter table and **Scope narrowing enforcement.**
- `spec/25_agent-operability.md`: §25.1, **Issuing a scoped token.**
- `pkg/tokenexchange`: `tokenexchange.go`.
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose tests the step adds.
- `issues/closed/TOKX/TOKX-I2-exchange-without-scope-issues-an-unrestricted-token.md`: filed on this branch with the content 0162 gave it, and closed by 0163.S1.

## Documentation

- `docs/client-guide/authentication.md` (client developer): the token exchange section states that a request whose `scope` is absent or empty receives the subject token's scope, shows the response `scope` field, and states that a self-exchange with the same audience and scope is a rotation that revokes the presented token.
- `docs/operator-guide/agent-operability.md` (operator): "Issuing a scoped token" states the same default.
- `docs/api/admin.md` (operator): the token exchange paragraph states the same default.

## Tests

Unit tier, in `pkg/tokenexchange`: `Validate` issues exactly the subject's scope for a request without scope from a narrowed subject, issues no scope for a request without scope from a subject with an absent claim, issues the requested subset unchanged, and refuses a value outside a narrowed subject's scope with `invalid_scope`; a child-minting exchange without scope intersects `authorized_tools` with the subject's scope. Cites §13.3 (Credential Flow). A further test checks that the issued scope does not alias the subject's slice and carries no annotation.

Unit tier, in `pkg/tokenservice`: `POST /v1/oauth/token` without `scope` from a narrowed subject signs a token whose `scope` claim equals the subject's and returns it in the response `scope` field; from a subject with an absent claim it signs a token with no `scope` claim. A self-rotation without `scope` from a narrowed subject revokes the superseded token. Cites §13.3 (Credential Flow).

Contract tier, in `tests/contract/oauth_token`: over HTTP, an exchange with an empty `scope` in a JSON body and one with no `scope` key in a form body, from a narrowed subject, return 200 with the subject's scope in the response and the issued claim; an exchange without `scope` from a subject with an absent claim issues no claim; an exchange with a subset returns that subset; and an exchange with a value outside the subject's scope returns 400 `invalid_scope`. Cites §13.3 (Credential Flow).

Security tier, in `tests/security`: with the Token Service built from the KMS stub as `cmd/lenny-token-service` builds it, a bearer narrowed to `tools:health:read`, presented as the caller and the subject without `scope`, is exchanged for a token whose `scope` claim is `tools:health:read`. Cites §13.3 (Credential Flow) and §25.1.
