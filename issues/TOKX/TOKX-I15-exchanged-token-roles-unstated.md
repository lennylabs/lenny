---
id: TOKX-I15
status: open
kind: spec-defect
severity: low
requirements: []
---

# The specification does not state the `roles` claim of a token an exchange issues

## Specification

§13.3 (Credential Flow), "Lenny JWT claim structure": the claim table lists `tenant_id`, `session_id`, `caller_type`, `delegation_depth`, `act`, `authorized_tools`, and `typ` with their source on exchange, and does not list `roles`. §10.2 (Authentication) "Playground mint invariants" invariant 5 bounds a playground bearer's roles by its subject's, and §13.3 "Platform-admin impersonation." rule 1 has the impersonation bearer carry the target user's roles.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `mintToken`: the issued claims carry no `Roles`, so every token `/v1/oauth/token` issues has no `roles` claim, whatever the subject carried.
- `pkg/gateway/playground/token.go`, `completeMint`: the playground bearer copies the subject's `Roles`.
- `pkg/gateway/api/admin/impersonation.go` and `pkg/credential/impersonation/service.go`, `sign`: the impersonation bearer carries the target user's roles.
- `pkg/tokenservice/tokenservice.go`, `authorizeRevocation`: the recursive-revocation request reads the caller token's `roles` claim when no platform-managed assignment exists, so a token issued by an exchange never authorizes an administrator's revocation through its claim.
- Gap G5 of change 0165 (a formal model of the token rules), on 2026-11-20. The model lets an exchange issue any subset of the subject's roles, which covers the code and every reading the text admits.

## Gap

A reader of §13.3 cannot tell whether an exchanged token keeps, narrows, or drops its subject's roles, and the three mints that issue tokens treat `roles` three ways.

## Suggested resolution

Add a `roles` row to the claim table of §13.3 that states the source on exchange (dropped, as the code does, or copied and bounded by the subject's roles), and align `mintToken` with it. Decided by the owner (change 0167 decision 11 files it without a fix).
