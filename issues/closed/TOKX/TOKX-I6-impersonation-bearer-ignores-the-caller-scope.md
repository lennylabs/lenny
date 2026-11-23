---
id: TOKX-I6
status: closed
kind: spec-defect
severity: low
requirements: []
change: 0164.S1
closed: fixed
closed-by: 0164.S1
---

# A platform-admin impersonation bearer carries no scope claim whatever the caller's scope

## Specification

§13.3 (Credential Flow), **Platform-admin impersonation.**, rule 1: the gateway signs a `user_bearer` for the target user. The rule does not state the bearer's `scope` claim, nor how the impersonating caller's scope bounds it.

§25.1 (Design Philosophy and Agent Model), **Matching.**: an absent `scope` claim carries no scope restriction.

## Evidence

- `pkg/credential/impersonation/service.go`, `sign`: the minted claims set no `Scope`.
- `pkg/api/openapi/openapi.json`: `POST /v1/admin/impersonation` declares `x-lenny-scope: tools:impersonation:write`, so a caller narrowed to that scope passes the scope gate.
- The security review of 0163.S1 on 2026-11-19 found the gap (finding 4). It predates that step.

## Gap

A platform-admin token narrowed to `tools:impersonation:write` obtains a bearer for the target user that carries no scope restriction. No path issues a narrowed platform-admin token today ([TOKX-I3](TOKX-I3-exchange-cannot-narrow-a-subject-without-scope.md)), so the case is not reachable yet; it becomes reachable once a narrowed admin token can be issued.

## Suggested resolution

State in §13.3 that the impersonation bearer carries the caller's `scope` claim when the caller's claim is present, or that a caller whose claim is present and is not `tools:*` is refused, and change `pkg/credential/impersonation` to match. The choice needs the owner.
