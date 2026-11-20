# Steps: 0163 A token exchange without scope issues the subject token's scope

## Validation owed

None.

## Steps

### 0163.S1 Token exchange default scope
- status: Planned
- lanes: [specification, code, documentation]
- closes: [TOKX-I2]

The `scope` row of the §13.3 RFC 8693 parameter table and **Scope narrowing enforcement.** state that an exchange without `scope` issues the subject token's scope, and that a subject with an absent claim yields an issued token with an absent claim (decision 1). `Validate` in `pkg/tokenexchange/tokenexchange.go` issues the subject's scope when the request names none, with unit tests in `pkg/tokenexchange` and `pkg/tokenservice`, contract tests in `tests/contract/oauth_token`, and a security-tier test in `tests/security`. The client authentication page, the agent operability page, and the admin API page state the default.
