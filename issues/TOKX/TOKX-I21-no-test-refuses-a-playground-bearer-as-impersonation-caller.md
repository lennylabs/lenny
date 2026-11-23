---
id: TOKX-I21
status: open
kind: test-gap
severity: low
requirements: []
---

# No test refuses a playground bearer as the caller of an impersonation mint

## Specification

§13.3 (Credential Flow) **Token rule properties.** item 6: no operation issues a token whose caller token is a playground bearer. §10.2 (Authentication) "Playground mint invariants" invariant 2 pins `playground_allowed_scope`, and invariant 5 copies the subject's roles onto the playground bearer.

## Evidence

- `pkg/gateway/api/admin/impersonation.go`, `requireImpersonationAdmin`: the handler checks only the `platform-admin` role, which a playground bearer minted from a platform-admin token carries.
- `pkg/api/openapi/openapi.json`: the impersonation routes declare `x-lenny-scope: tools:impersonation:write`, which `playground_allowed_scope` does not grant, so the gateway's scope gate refuses a playground bearer on them. That declaration is the only barrier.
- `tests/component/tokenproperties/world_test.go`, `impersonate`: the property tests call the impersonation issuer directly and leave playground callers out, so item 6 for an impersonation caller rests on the route's scope declaration, which no test drives.
- Found by the general and security reviews of 0165.S2 on 2026-11-23.

## Gap

A change that drops or widens the route's `x-lenny-scope` would let a playground bearer start an impersonation session, and no test would fail.

## Suggested resolution

Add a test that sends `POST /v1/admin/impersonation` with a playground bearer minted from a platform-admin token through the gateway's auth chain and expects a refusal, or have `requireImpersonationAdmin` refuse a principal whose `origin` is `playground`, and state the refusal in §13.3 rule 1.
