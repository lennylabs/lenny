# Steps: 0164 Token exchange narrowing and identity rules

## Validation owed

None.

## Steps

### 0164.S1 Token exchange narrowing and identity rules
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0163.S1]
- closes: [TOKX-I3, TOKX-I4, TOKX-I5, TOKX-I6, TOKX-I7]

§13.3 states the narrowing grammar of rule (a) and the rule (e) allowlist for an absent issued scope (decision 1), the refusal of a playground-origin `subject_token` or `actor_token` (decision 2), the different-caller rule and the parent every derived token records (decision 3), and the impersonation bearer's scope (decision 4). §12.2 states the `parent_jti` column for every derived token, and §25.1 **Issuing a scoped token.** points to the grammar. `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/credential/impersonation`, and the impersonation handler of `pkg/gateway/api/admin` carry the rules, and the `token.exchanged` row carries `requested_scope` and `granted_scope` (decision 5), with unit, contract, component, and security tests. The step depends on 0163.S1, which changes the same code. The agent operability page, the client authentication page, and the admin API page state the rules. One step carries the work, because the lineage is one function and the rest share the validator and its tests.
