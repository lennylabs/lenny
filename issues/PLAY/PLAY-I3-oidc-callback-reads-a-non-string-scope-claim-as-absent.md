---
id: PLAY-I3
status: open
kind: spec-defect
severity: low
requirements: []
---

# The playground OIDC callback reads a non-string `scope` claim as absent

## Specification

§27.3.1 (OIDC cookie-to-MCP-bearer exchange), the `GET /playground/auth/callback` bullet: the gateway validates the returned ID token and "extracts standard Lenny claims (`user_id`, `tenant_id`, `caller_type`, `scope`".

§10.2 (Authentication), **Playground mint invariants**, invariant 2: a subject whose `scope` claim is absent, empty, or whitespace-only mints `playground_allowed_scope`, and a subject `scope` claim that carries a value the §25.1 canonical form refuses, such as `openid profile`, refuses the mint with `401 UNAUTHORIZED` and `details.reason: "subject_scope_invalid"`.

Neither section states how the callback reads a `scope` claim whose JSON value is not a string.

## Evidence

- `pkg/gateway/playground/oidc.go`, `extractSubject`: the callback fills `OIDCSubject.Scope` with `stringClaim(claims, "scope")`, and the session record stores that value as the subject's scope.
- `pkg/gateway/playground/oidc.go`, `stringClaim`: a claim whose value is not a string yields the empty string, so an ID token carrying `"scope": ["openid", "profile"]` is stored with an empty scope.
- `pkg/gateway/playground/token.go`, `intersectScope`: the mint reads the empty scope as absent and issues `playground_allowed_scope`, while the string `"openid profile"` is refused with 401. The ceiling bounds the result, and the subject held no scope restriction under either reading, so no privilege is gained.
- Change 0149, "TS-2", filters an IdP token's `scope` to values with the `tools:` prefix at the verifier, reads a token with no such value as having an absent scope, and keeps a malformed `tools:` value as `TOKEN_INVALID`. Under that reading `openid profile` no longer refuses a token, and the playground callback, which reads the ID token outside the verifier, keeps a rule of its own.
- Raised by the general review of 0162.S1 (finding 4). The owner chose option (a) of decision 2 of change 0162, question SM-2 of the M3 readiness review, on 2026-11-19: file this issue under PLAY and settle it with the scope-claim handling of 0149 (TS-2).

## Gap

The callback reads the same scope two ways depending on its JSON type: an array is stored as absent and mints `playground_allowed_scope`, and the equivalent space-separated string refuses the mint. The rule differs from the reading 0149 gives every verifier.

## Suggested resolution

When 0149.S2 (TS-2) lands, have the playground OIDC callback read the ID token's `scope` claim through the same filter as the verifier, including its handling of a non-string value, and state in §27.3.1 that the callback applies that reading. Update the §10.2 failure-mode example and the contract test case that use `openid profile`, as the review history of 0162 records.
