---
id: CAPIMSG-I1
status: open
kind: build-gap
severity: low
requirements: []
---

# A warm Open Responses continuation delivers a turn without the `manage_own_sessions` check

## Specification

§10.2 (Authorization and RBAC), **Session ownership.** rule 7: "The gateway applies these rules after the permission matrix admits the operation, checking a read against \"Read own session history\" and a session create or an act (rule 5) against \"Create / cancel own sessions\", and a caller the matrix refuses receives its refusal." Delivering a turn to a session is an act on it, which `POST /v1/sessions/{id}/messages` checks against that row.

## Evidence

- `pkg/gateway/environment/translator/open_responses.go`: a continuation that meets the warm conditions (`warmEligible`) runs on the held session through `serveWarm` and delivers the turn without a check of `manage_own_sessions`; a cold continuation goes through the create-and-start service, which 0161.S1 makes check it.
- The security review of 0161.S1 on 2026-11-19 found the gap (finding F3). It predates that step.

## Gap

A principal whose roles lose "Create / cancel own sessions" after a conversation starts, such as a user later given only a viewer role, can keep delivering turns to the held session while its continuation stays warm.

## Suggested resolution

Run the permission check of the "Create / cancel own sessions" row on the warm path before the turn is delivered, returning the native `403` as the cold path does, with a test.
