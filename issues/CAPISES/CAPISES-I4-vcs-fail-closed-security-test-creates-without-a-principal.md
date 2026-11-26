---
id: CAPISES-I4
status: open
kind: test-gap
severity: medium
requirements: []
---

# The security test of the fail-closed VCS credential read creates its session with no principal and fails before the credential read

## Specification

[§10.2](../../spec/10_gateway-internals.md#102-authentication) (Authentication), **Session ownership.** rules 1 and 3: a create binds the session's owner from the principal's subject, and a create with no subject is refused. [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) holds the gitClone credential behavior the test means to probe.

## Evidence

- `tests/security/vcs_credential_read_failclosed_test.go`: the test mounts `apifront.Handler(srv)` with no principal, and `vcsFailClosedCreate` sends the create without one.
- `go test -tags security -run TestVCSCredentialReadFailsClosedWithNoEarlierValue ./tests/security/` fails on `main` at `00ba4358a` with `first create status = 401, want 201; body {"error":{"code":"UNAUTHORIZED",…"reason":"principal_subject_required"}}`. It fails the same way on the head of `change/0178.S1-S2-idempotency-scope` (`14673a36f`), where the targeted security run of 0178.S1 found it.

## Gap

The security tier never reaches the fail-closed credential read, so a regression that serves a stale or empty VCS token would pass unnoticed.

## Suggested resolution

Mount the handler behind `ownerprincipal.Wrap` (`tests/testinfra/ownerprincipal`), or attach a principal in `vcsFailClosedCreate`, and rerun the test.
