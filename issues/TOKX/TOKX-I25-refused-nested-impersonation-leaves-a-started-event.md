---
id: TOKX-I25
status: open
kind: test-gap
severity: medium
requirements: []
---

# The impersonation refusal test finds an admin.impersonation_started row for the impersonated user

## Specification

[§13.3](../../spec/13_security-model.md#133-credential-flow) (Credential Flow), "Platform-admin impersonation.": an impersonation bearer cannot start a nested impersonation, and a refused start records no `admin.impersonation_started` event.

## Evidence

- `tests/security/ops_revocation_enforcement_test.go`: the nested-impersonation case of `TestImpersonationBearerRefusals_spec_13_3` asserts `startedEventsBy(t, target) == 0` after the refused nested `POST /v1/admin/impersonation`.
- `go test -tags security -run TestImpersonationBearerRefusals_spec_13_3 ./tests/security/` fails on `main` at `00ba4358a` with `admin.impersonation_started rows with admin_sub mallory@acme.com = 1, want 0`. It fails the same way on the head of `change/0178.S1-S2-idempotency-scope` (`14673a36f`), where the targeted security run of 0178.S1 found it.
- The other assertions of the case pass: the nested POST answers `403 FORBIDDEN` and returns no bearer, and the impersonation listing is unchanged. The cause is established: change 0168 decision 13 (the owner's answer to FM-31, archived in `changes/archive/2026-11-24-0168-token-fixes/decisions.md`) states that a refused impersonation start keeps its `admin.impersonation_started` event, because the first transaction writes the event before it reads the caller bearer under the share lock. The test predates that decision and was not updated when 0168.S1 landed, because the security tier is outside the merge gate.

## Gap

Either a refused nested impersonation writes an audit row that names a start that never happened, or the test reads rows another case wrote. In the first case the audit trail misreports the impersonation sessions. In the second the test cannot detect that defect.

## Suggested resolution

Update the nested-impersonation case of `TestImpersonationBearerRefusals_spec_13_3` to expect the started event that 0168 decision 13 keeps (one row, with no ended event and no bearer issued), and keep the assertions on the `403 FORBIDDEN`, the missing bearer, and the unchanged listing.
