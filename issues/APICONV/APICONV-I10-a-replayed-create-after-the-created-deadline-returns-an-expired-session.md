---
id: APICONV-I10
status: open
kind: spec-defect
severity: low
requirements: []
---

# A replay of a create or derive after the session's created deadline returns an expired session and upload token

## Specification

§11.5 Idempotency; §7.1 **`uploadToken` format and security properties** (the token expires at session creation plus `maxCreatedStateTimeoutSeconds`).

## Evidence

- A stored CreateSession or derive response replays for 24 hours, with the `uploadToken` re-signed (change 0173), while the session leaves `created` at `maxCreatedStateTimeoutSeconds` (300 s by default) and the token expires then.
- Found by the adversarial review of a per-subject idempotency key with a conflict error, on 2026-11-26.

## Gap

A retry after the deadline receives a session id and an upload token that can no longer be used, with no indication that the operation must be repeated.

## Suggested resolution

State in §11.5 what a replay of a session-creating operation returns after the session's `created` deadline, for example the stored response with the session's current state, or a response that tells the client to create again.
