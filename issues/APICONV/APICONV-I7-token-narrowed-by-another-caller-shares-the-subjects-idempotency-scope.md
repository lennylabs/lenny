---
id: APICONV-I7
status: planned
change: 0178.S3
kind: spec-defect
severity: low
requirements: []
---

# A token that another caller narrows for a user shares that user's idempotency scope

## Specification

§11.5 Idempotency, **Mechanism.**; §13.3 token exchange, narrowing by a caller other than the subject.

## Evidence

- `checkCallerIdentity` (`pkg/tokenexchange/tokenexchange.go:498-511`) lets a caller present another user's `subject_token` and receive a strictly narrower `user_bearer` for that user. `issuedAct` copies the subject's `act`, which is empty, and `derivedLineage` leaves `act_sub` null (`pkg/tokenservice/tokenservice.go:1024`).
- The token therefore carries nothing the idempotency scope separates, and its holder, or anyone it passes the token to, shares the user's stored responses.
- Found by the review of 0178 open decision O4 on 2026-11-26.

## Gap

Narrowing a token for a later holder does not narrow what that holder can replay from the user's stored responses.

## Suggested resolution

Scope idempotency records by credential lineage, which a narrowing starts afresh, as the redesign of the scope under review proposes, or record the narrowing caller in the token.
