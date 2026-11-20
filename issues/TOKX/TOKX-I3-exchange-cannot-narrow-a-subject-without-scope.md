---
id: TOKX-I3
status: open
kind: spec-defect
severity: medium
requirements: []
---

# A token exchange cannot narrow a subject token that carries no `scope` claim

## Specification

§13.3 (Credential Flow), **Scope narrowing enforcement.**, rule (a): the Token Service rejects any requested `scope` value not present in `subject_token.scope`. Rule (e) intersects a child's `authorized_tools` with the issued token's `scope`.

§25.1 (Design Philosophy and Agent Model), **Matching.**: an absent `scope` claim carries no scope restriction. **Scope value syntax.**: `tools:*` is equivalent to omitting the claim. **Issuing a scoped token.**: operators issue narrowed tokens by exchanging the agent's existing token with a narrower `scope`.

## Evidence

- `pkg/tokenexchange/tokenexchange.go`, `resolveIssuedScope` and `isSubset`: the subset check builds its set from the subject's values, so every requested value misses when the subject carries no claim, and the exchange returns `invalid_scope` with reason `scope_not_subset`. A request for `tools:*` from such a subject is refused the same way, and a request for `tools:pool:read` from a subject holding `tools:pool:*` is refused because the check compares literal strings.
- The tokens Lenny mints without an exchange carry no `scope` claim: `sign` in `pkg/gateway/api/admintoken/admintoken.go`, `sign` in `pkg/credential/impersonation/service.go`, and `Issue` in `pkg/embedded/devauth/devauth.go`. The Token Service verifies `subject_token` against the Lenny key set alone (`cmd/lenny-token-service`), so an identity provider token cannot be a subject.
- `docs/operator-guide/agent-operability.md`, "Issuing a scoped token": the example exchanges `${ADMIN_TOKEN}` for `tools:health:* tools:diagnostics:* tools:escalation:create`, which the Token Service refuses with `invalid_scope`.
- `pkg/tokenexchange/tokenexchange.go`, `Validate`: a child-minting exchange intersects the subject's `authorized_tools` with the issued scope, and `intersect` returns nil for an empty scope, so a child of a subject without a `scope` claim loses its allowlist.
- The playground mint reads an absent claim as unrestricted and intersects it with `playground_allowed_scope` (§10.2, **Playground mint invariants**, invariant 2, as change 0162 states it), so the two narrowing paths treat an unscoped subject differently.
- The general and security reviews of 0163.S1 on 2026-11-19 found the gap. It predates that step, which issues the subject's scope when the request names none and leaves explicit narrowing unchanged.

## Gap

No token an operator obtains carries a `scope` claim, and the exchange refuses to narrow a token without one, so the §25.1 narrowing flow cannot produce a scope-restricted token. Agents hold tokens limited only by their roles. The refusal fails closed and broadens nothing.

## Suggested resolution

Choose between: (a) read an absent subject claim as unrestricted in the subset check, accepting a requested set when every value parses as a canonical scope and the subject's parsed set matches it (`scopes.Parse(subject).Matches`), which also honours `tools:*` and `tools:<domain>:*` subjects, and keep the allowlist of rule (e) when the issued scope is absent; or (b) state in §13.3 and §25.1 that an unscoped subject cannot be narrowed through the exchange, name the path that issues the first scoped token, and correct the operator guide example. The choice needs the owner.
