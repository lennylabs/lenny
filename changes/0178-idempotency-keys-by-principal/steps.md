# Steps: 0178 Idempotency keys scoped by principal

## Validation owed

None.

## Steps

### 0178.S1 Records scoped by principal
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [APICONV-I1]

No open decision touches the step. §11.5 **Mechanism.**, as IP-4 in `change.md` states (decisions 1 and 3). Then the subject and the session id in `idempotency.Key` and the store interface (IP-1), the resolver for the REST middleware and the MCP hook (IP-2), the migration of `idempotency_keys` (IP-3), and the unit, component, contract, load_local, and security tests. Then the idempotency sections of `docs/api/rest.md` and `docs/api/mcp.md`. The step closes [APICONV-I1](../../issues/APICONV/APICONV-I1-idempotency-key-scoped-per-tenant-only.md), which 0138.S1 closed before this change took it over.

### 0178.S2 User erasure deletes the user's idempotency records
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0178.S1]
- closes: [ERASE-I2]

§12.8 **Storage backends in erasure scope:** gains idempotency records, deleted by `(tenant_id, subject)`, and the **`DeleteByUser` dependency-ordered deletion sequence** and tenant deletion Phase 4 gain their steps, as IP-5 in `change.md` states (decision 2). Then `DeleteByUser` on the idempotency stores, the new store name in `pkg/storage/erasure/order.go`, the user eraser and the tenant eraser in the erasure wiring of `cmd/lenny-gateway`, and the tests: unit tests of the stores and the erasure orders, an integration test with a real erasure, and the erasure completeness check `TestErasureDeletesTheUsersRowsInEveryWiredStore_spec_12_8` extended to the new store. Then the GDPR erasure section of `docs/operator-guide/security.md`. The step depends on 0178.S1, because a record names no user until 0178.S1 adds the subject. It closes [ERASE-I2](../../issues/ERASE/ERASE-I2-user-erasure-leaves-idempotency-records.md).
