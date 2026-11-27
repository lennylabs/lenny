---
id: UPLOAD-I7
status: open
kind: test-gap
severity: medium
requirements: []
---

# The security test of finalize upload tokens creates its sessions with no principal and fails before it reaches finalize

This issue was filed as UPLOAD-I2 on the branch of steps 0178.S1 and 0178.S2, and took the number UPLOAD-I7 when the branch merged `main`, where change 0177 had filed UPLOAD-I2 to UPLOAD-I6.

## Specification

[§10.2](../../spec/10_gateway-internals.md#102-authentication) (Authentication), **Session ownership.** rules 1 and 3: a create binds the session's owner from the principal's subject, and a create with no subject is refused. [§7.4](../../spec/07_session-lifecycle.md#74-upload-safety) (Upload Safety) is the behavior the test means to probe.

## Evidence

- `tests/security/finalize_upload_token_test.go`: `newFinalizeTokenProbe` mounts `apifront.Handler(srv)` with no principal, and `create` sends `POST /v1/sessions` with only `X-Lenny-Tenant-ID`.
- `go test -tags security -run TestFinalizeRefusesForeignAndReplayedUploadTokens_spec_7_4 ./tests/security/` fails on `main` at `00ba4358a` with `create: status 401 body {"error":{"code":"UNAUTHORIZED",…"reason":"principal_subject_required"}}`. It fails the same way on the head of `change/0178.S1-S2-idempotency-scope` (`14673a36f`), where the targeted security run of 0178.S1 found it.

## Gap

The security tier never reaches the finalize checks, so a regression in the upload-token verification at finalize would pass unnoticed.

## Suggested resolution

Mount the handler behind `ownerprincipal.Wrap` (`tests/testinfra/ownerprincipal`), as the contract suites do, and rerun the test.
