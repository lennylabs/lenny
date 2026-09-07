## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The derivation rule replaces 0073's §4.1 table, and the three sentences that
      ground the declared classification are rewritten or removed with it.
      Tiers 0, 11. Depends on: proposals 0073 and 0076 applied
- [ ] **S2 · test** — TEST-1. The tier-0 table-reconciliation gate 0073 adds is replaced by the rule gate.
      Tiers 0. Depends on: S1
- [ ] **S3 · test** — TEST-2. The tier-3 session-address suite covers `CoordinatorFenceRequest` and its
      false coverage clause is removed.
      Tiers 3. Depends on: S1
