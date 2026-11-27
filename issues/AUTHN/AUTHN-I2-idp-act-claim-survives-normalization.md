---
id: AUTHN-I2
status: open
kind: build-gap
severity: low
requirements: []
---

# An upstream IdP token keeps its act claim through normalization to user_bearer

## Specification

§10.2 **JWT claims**: a `user_bearer` carries no `session_id` or `act` claim.

## Evidence

- `normalizeUserBearer` (`pkg/auth/jwt/oidc.go:265-270`) clears `typ`, `session_id`, the delegation depth, and `origin`, and leaves `act`.
- `authmw` copies it into `Principal.Act` (`pkg/auth/authmw/auth.go:603`).
- Found by the review of 0178 open decision O4 on 2026-11-26.

## Gap

An IdP-issued token can present an `act` claim that the platform treats as a Lenny delegation actor, which §10.2 says a `user_bearer` never carries. Today it only separates idempotency records; a rule that reads `act` on a `user_bearer` would trust a claim no Lenny mint wrote.

## Suggested resolution

Clear `act` in `normalizeUserBearer`, with a unit test.
