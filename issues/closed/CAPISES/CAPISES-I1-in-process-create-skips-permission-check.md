---
id: CAPISES-I1
status: closed
kind: build-gap
severity: high
requirements: []
change: 0161.S1
closed: fixed
closed-by: 0161.S1
---

# Session creation through MCP and the single-shot path skips the create-permission check

## Specification

§15.1 and the gateway's authorization model (§10.2): a caller whose role lacks the session-create permission, such as a `tenant-viewer`, is refused session creation. The REST create routes apply this check; §15.2 requires the MCP `lenny/create_session` tool to be semantically equivalent to `POST /v1/sessions`, refusals included.

## Evidence

- The security review of 0117.S8 on 2026-11-19 (branch `change/0117.S8-api-front`, raised as 0117 decision 71): `CreateSessionService` and `CreateAndStartService` in `pkg/gateway/core/session/sessionserver` apply the tenant check only. A `tenant-viewer` that the REST route refuses with 403 can create a session through MCP `lenny/create_session` or the single-shot path.
- `main` behaves the same; 0117.S8 kept the behavior because 0117's steps change no behavior.

## Gap

The create permission is enforced on one surface and not on the others, so a read-only role can start sessions, which consumes pool capacity, credentials, and budget in its tenant.

## Suggested resolution

Apply the same create-permission check (through `GWSEAM-AuthorizePermission` once 0117.S8 lands) in the in-process create services, so every surface refuses alike; add tests on the MCP and single-shot paths for a `tenant-viewer`, and a contract test that REST and MCP create refuse the same roles.
