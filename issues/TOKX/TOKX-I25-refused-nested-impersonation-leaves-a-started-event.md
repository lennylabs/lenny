---
id: TOKX-I25
status: open
kind: build-gap
severity: medium
requirements: []
---

# The impersonation refusal test finds an admin.impersonation_started row for the impersonated user

## Specification

[§13.3](../../spec/13_security-model.md#133-credential-flow) (Credential Flow), "Platform-admin impersonation.": an impersonation bearer cannot start a nested impersonation, and a refused start records no `admin.impersonation_started` event.

## Evidence

- `tests/security/ops_revocation_enforcement_test.go`: the nested-impersonation case of `TestImpersonationBearerRefusals_spec_13_3` asserts `startedEventsBy(t, target) == 0` after the refused nested `POST /v1/admin/impersonation`.
- `go test -tags security -run TestImpersonationBearerRefusals_spec_13_3 ./tests/security/` fails on `main` at `00ba4358a` with `admin.impersonation_started rows with admin_sub mallory@acme.com = 1, want 0`. It fails the same way on the head of `change/0178.S1-S2-idempotency-scope` (`14673a36f`), where the targeted security run of 0178.S1 found it.
- The other assertions of the case pass: the nested POST answers `403 FORBIDDEN` and returns no bearer, and the impersonation listing is unchanged. The cause is not yet established. Either the refused start writes the event before it refuses, or the row comes from a fixture that shares the audit table with another case of the test.

## Gap

Either a refused nested impersonation writes an audit row that names a start that never happened, or the test reads rows another case wrote. In the first case the audit trail misreports the impersonation sessions. In the second the test cannot detect that defect.

## Suggested resolution

Read the row's payload and the order of the case's writes to tell the two causes apart. Then fix the start path so a refused start writes no `admin.impersonation_started` event, or scope the test's count to the rows the nested request could write.
