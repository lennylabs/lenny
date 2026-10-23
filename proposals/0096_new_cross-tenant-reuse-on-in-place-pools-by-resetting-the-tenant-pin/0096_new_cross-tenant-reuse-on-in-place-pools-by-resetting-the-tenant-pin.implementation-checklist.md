## Implementation checklist

The review run fills in each step's deliverable, tiers, and dependencies. The skeleton below records the order the problem statement implies.

- [ ] **S1 · spec** — The §5.2 tenant-pinning edits: the new permitted label edge, the reset after a proven restart, and the `allowCrossTenantReuse` paragraph.
      Tiers 0, 11. Depends on: proposal 0087 part 1 implemented.
- [ ] **S2 · spec** — The §5.2 recycle step 7 fresh-guest sentence, the `scrubProfile` paragraph, and the `onScrubFailure: fail` requirement on cross-tenant pools.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — The §4.6.3 RBAC sentence and the §18 package-list restatement of the label transitions.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · code** — The `lenny-tenant-label-immutability` webhook edge, with tier-1, tier-2, and tier-9 tests.
      Tiers 0, 1, 2, 9. Depends on: S1, S3
- [ ] **S5 · code** — The gateway pin reset keyed on the attestation and a clean step-6 verification, the acquisition predicates, and the pool admission rules.
      Tiers 0, 1, 2, 4, 7a, 9. Depends on: S1, S2, S4
- [ ] **S6 · test** — The cross-tenant cluster tests on a Kata RuntimeClass, with a skip-register entry where no KVM-capable node exists.
      Tiers 6, 9. Depends on: S5
- [ ] **S7 · docs** — Operator and security documentation of cross-tenant reuse and its residuals.
      Tiers 0, 11. Depends on: S1, S2, S3
