# Steps: 0178 Idempotency keys scoped by principal

## Validation owed

None.

## Steps

### 0178.S1 Records scoped by principal
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: []

Waits on open decision O1. §11.5 **Mechanism.**, as IP-4 in `change.md` states (decision 1). Then the subject in `idempotency.Key` and the store interface (IP-1), the resolver for the REST middleware and the MCP hook (IP-2), the migration of `idempotency_keys` (IP-3), and the unit, component, contract, load_local, and security tests. Then the idempotency sections of `docs/api/rest.md` and `docs/api/mcp.md`. No issue tracks the gap, because decision 1 lands it as this change.
