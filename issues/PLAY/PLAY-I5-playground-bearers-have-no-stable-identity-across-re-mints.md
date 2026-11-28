---
id: PLAY-I5
status: planned
change: 0178.S3
kind: spec-defect
severity: low
requirements: []
---

# Playground bearers in apiKey and dev modes have no stable identity across re-mints

## Specification

§27 Web playground, the bearer mint.

## Evidence

- The `apiKey` and `dev` modes mint with no playground session record (`completeMint(..., nil)` in `pkg/gateway/playground/token.go:236,370`), so nothing ties one re-mint to the next.
- In `oidc` mode the record's identifier is the HttpOnly cookie value (`parseSessionCookie`, `pkg/gateway/playground/auth.go:496-501`), which no bearer claim may carry.
- Found by the adversarial review of 0178 open decision O8 on 2026-11-26.

## Gap

Any mechanism that needs a stable identity for a playground bearer across its re-mints, such as the rotation chain of 0178 decision 8, has no value to use in these modes.

## Suggested resolution

Define the stable identity per mode, as 0178 decision 8 does for the rotation chain, and state it in §27.
