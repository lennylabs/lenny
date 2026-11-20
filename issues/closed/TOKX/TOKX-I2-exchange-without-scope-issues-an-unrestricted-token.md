---
id: TOKX-I2
status: closed
kind: spec-defect
severity: high
requirements: []
change: 0163.S1
closed: fixed
closed-by: 0163.S1
---

# A token exchange that omits `scope` issues a token with no scope restriction

## Specification

§13.3 (Credential Flow), **Scope narrowing enforcement.**: "RFC 8693 specifies that the issued token's capabilities MUST be a subset of the `subject_token`'s. Lenny's Token Service enforces this by: (a) rejecting any `scope` value not present in `subject_token.scope` (returns `invalid_scope`)". The section does not state the issued scope when the request carries no `scope` parameter.

§25.1 (Design Philosophy and Agent Model), **Matching.**: an absent `scope` claim carries no scope restriction.

## Evidence

- `pkg/tokenexchange/tokenexchange.go`, `isSubset`: an empty candidate is a subset of every universe (`if len(candidate) == 0 { return true }`). `Validate` checks `isSubset(r.Requested.Scope, r.Subject.Scope)` and issues `Scope: r.Requested.Scope`.
- `pkg/tokenservice/tokenservice.go`, `buildExchangeRequest`: the requested scope is `splitScope(req.Scope)`, with no default from the subject token. `mintToken` writes `Scope: strings.Join(issued.Scope, " ")`, which is empty when the request omitted `scope`.
- `pkg/common/scopes/scopes.go`, `Set.Present` and `Set.Matches`, and `pkg/gateway/api/admin/scope_enforce.go`, `scopeGate.ServeHTTP`: an empty claim is not present, and a claim that is not present passes every scope gate.
- The security review of 0162.S1 on 2026-11-19 found the gap (finding 1). It predates that step. Change 0162 closes the same defect on the playground mint, `POST /v1/playground/token`, and leaves `POST /v1/oauth/token` unchanged.

## Gap

The holder of a bearer narrowed to a scope such as `tools:health:read` presents it as the caller token and the `subject_token` of `POST /v1/oauth/token`, with `audience=lenny-gateway` and no `scope` parameter. The Token Service issues a token with an empty `scope` claim, which every scope gate reads as unrestricted, so the new token is limited only by the subject's roles.

## Suggested resolution

State in §13.3 what an exchange without `scope` issues: either the subject's scope (RFC 8693 leaves the default to the authorization server), or a refusal with `invalid_scope` when the subject's scope is present and the request's is empty. Change `Validate` or `buildExchangeRequest` to match, with a test that an exchange from a narrowed subject without `scope` never issues an empty claim. The choice needs the owner.
