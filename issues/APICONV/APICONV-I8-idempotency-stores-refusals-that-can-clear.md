---
id: APICONV-I8
status: planned
change: 0178.S4
kind: build-gap
severity: low
requirements: []
---

# The idempotency middleware stores refusals that can clear and replays them for 24 hours

## Specification

§11.5 Idempotency.

## Evidence

- `maybePersist` stores every response below 500 (`pkg/idempotency/idempotencymw/idempotency.go:511`), including `429 QUOTA_EXCEEDED` (`pkg/gateway/core/session/sessionserver/quota.go:213`) and the user gate's `403` refusals such as `USER_INVALIDATED` and `ERASURE_IN_PROGRESS` (ERASE-I5 covers the erasure case).
- Found by the adversarial review of 0178 open decision O8 on 2026-11-26.

## Gap

A caller whose quota or eligibility recovers keeps receiving the stored refusal for the key's 24-hour window.

## Suggested resolution

Do not store refusals whose condition can clear, as 0178 decision 8 states.
