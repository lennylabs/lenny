## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the REG-SLOTCOUNT set and its keyed release, all of SPEC-1.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the SandboxClaim writer edge and its citing sites, all of SPEC-2.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §11.4 full_revoke release steps and **Note** edits, all of SPEC-3.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · code** — CODE-1. Lands the slot set primitive and its callers, with the tests **Testing** assigns to CODE-1.
      Tiers 0, 1, 2, 4, 7a. Depends on: S1, S2
- [ ] **S5 · code** — CODE-2. Lands the slot-aware `ReclaimClaimed`, with the tests **Testing** assigns to CODE-2.
      Tiers 0, 1, 2. Depends on: S1, S2, S4
- [ ] **S6 · code** — CODE-3. Lands the finalize exits, with the tests **Testing** assigns to CODE-3.
      Tiers 0, 1. Depends on: S2, S5
- [ ] **S7 · code** — CODE-4. Lands the created sweeper deletion, with the tests **Testing** assigns to CODE-4.
      Tiers 0, 1. Depends on: S2, S5
- [ ] **S8 · code** — CODE-5. Lands the hook's stale-binding step, with the tests **Testing** assigns to CODE-5.
      Tiers 0, 1. Depends on: —
- [ ] **S9 · code** — CODE-6. Lands the slot-accounting lock on the hook's release step, with the tests **Testing** assigns to CODE-6.
      Tiers 0, 1, 7a. Depends on: S8
- [ ] **S10 · code** — CODE-7. Lands the full_revoke release routing, with the tests **Testing** assigns to CODE-7.
      Tiers 0, 1, 2, 3, 9. Depends on: S3, S5, S9
- [ ] **S11 · test** — TEST-1. Lands the harness extension and the cross-tier tests.
      Tiers 0, 2, 4, 5, 7a, 8, 9, 11. Depends on: S1, S2, S3, S4, S5, S6, S7, S8, S9, S10
- [ ] **S12 · docs** — REC-1. Lands the BUILD-GAPS closures, annotations, and filings.
      Tiers 0, 11. Depends on: S11
