---
id: TOKX-I4
status: closed
kind: spec-defect
severity: low
requirements: []
change: 0164.S1
closed: fixed
closed-by: 0164.S1
---

# A token exchange of a playground bearer issues a token without the `origin` claim

## Specification

§13.3 (Credential Flow), **Client authentication on `/v1/oauth/token`.**: a token that carries the `origin: "playground"` claim is not a caller credential, and the Token Service refuses it as the caller token with `401 invalid_client`. The section does not state how a playground bearer presented as `subject_token` or `actor_token` is treated.

§27.3.1 (OIDC cookie-to-MCP-bearer exchange) and §27.6 key the playground's per-request revocation check and its session caps on the `origin` claim.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `authenticateCaller`: only the caller token is checked; the comment states that the `subject_token` and `actor_token` take no origin check.
- `pkg/tokenservice/tokenservice.go`, `mintToken`: the issued claims copy no `Origin`.
- The security review of 0163.S1 on 2026-11-19 found the gap (finding 2). It predates that step.

## Gap

A holder of a playground bearer and of any non-playground caller credential in the same tenant exchanges the bearer as `subject_token`. The issued token keeps the bearer's scope and is bounded by its expiry, but carries no `origin` claim, so it survives the playground logout revocation and creates sessions outside the playground caps until it expires.

## Suggested resolution

State in §13.3 either that the Token Service refuses a playground-origin `subject_token` or `actor_token` with `invalid_grant`, or that the issued token copies the subject's `origin` claim, and change `pkg/tokenservice` to match, with a test. The choice needs the owner.
