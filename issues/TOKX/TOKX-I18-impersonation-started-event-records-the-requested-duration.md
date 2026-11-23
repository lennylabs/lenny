---
id: TOKX-I18
status: open
kind: spec-defect
severity: low
requirements: []
---

# The `admin.impersonation_started` event records the requested duration, not the bearer's expiry

## Specification

§13.3 (Credential Flow) "Platform-admin impersonation." rule 1 (b): the bearer's `exp` is the earlier of the started event's time plus `durationSeconds` and the `exp` of the caller's bearer. §16.7 (Section 25 audit events) `admin.impersonation_started` carries `impersonation_duration_seconds`.

## Evidence

- `pkg/credential/impersonation/service.go`, `startedPayload`: the event records `impersonation_duration_seconds` from the request, and `Issue` caps the ticket's `ExpiresAt` with `bearerExpiry` after the payload's inputs are fixed, so a session capped by the caller's bearer is recorded with the longer requested duration.
- The capped expiry is visible only in the bearer's `issued_tokens` row and in the API response's `expiresAt`.
- Found by the general review of 0167.S1 on 2026-11-22 (finding 12).

## Gap

An auditor reading the started event cannot tell when the session's bearer expires once change 0167 caps it at the caller's bearer.

## Suggested resolution

Add the effective expiry to the started event's payload in §16.7 (for example `expires_at`), or record the effective duration in `impersonation_duration_seconds`. Either changes the §16.7 payload, so the owner decides.
