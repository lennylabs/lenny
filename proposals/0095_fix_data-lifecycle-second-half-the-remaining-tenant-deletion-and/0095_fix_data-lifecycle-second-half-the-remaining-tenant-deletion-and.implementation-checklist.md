## Implementation checklist

Every step lands the tests the non-spec Testing section places under its deliverables, and classifies each failure those tests expose as Testing **Failures the tests find.** states.

- [ ] **S1 · spec** — SPEC-1. Lands both receipt edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. Lands the §10.2, §27.3.1, and §4.2 edits.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-2. Lands the tenant deletion lifecycle edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. Lands the §11.4 edits.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands the scope-table, step 18, and Phase 4 edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-6. Lands the override sub-step edits.
      Tiers 0, 11. Depends on: S5
- [ ] **S7 · migration** — MIG-1. Lands migration 0188 and its tier-2 test.
      Tiers 0, 2. Depends on: S1
- [ ] **S8 · code** — CODE-2. Lands the sticky tenant delete and the stream purge.
      Tiers 0, 1, 2. Depends on: S5
- [ ] **S9 · code** — CODE-5. Lands the Phase 3.5 escrow changes.
      Tiers 0, 1, 2. Depends on: S5, S6
- [ ] **S10 · code** — CODE-1, CODE-3, CODE-4. Lands the rank table and completeness check with the wiring of both paths, and closes F-12.8.37.
      Tiers 0, 1, 2, 4, 9, 11. Depends on: S5, S7, S8, S9
- [ ] **S11 · code** — CODE-6. Lands CODE-6 and its tests.
      Tiers 0, 1, 2, 9. Depends on: S2, S3
- [ ] **S12 · code** — CODE-7. Lands the lease step without deny-listing and the retargeted lease-step tests.
      Tiers 0, 1, 4, 8, 9. Depends on: S4
- [ ] **S13 · code** — CODE-8. Lands the Phase 3 revoker, its wiring, and the in-phase pause.
      Tiers 0, 1, 4, 8. Depends on: S3, S4, S5, S10, S11, S12
- [ ] **S14 · code** — CODE-9. Lands full_revoke continuation.
      Tiers 0, 1, 3, 4. Depends on: S4, S12
- [ ] **S15 · docs** — DOCS-1. Lands the operator-guide and `lenny-ctl` reference edits.
      Tiers 0, 11. Depends on: S2, S3, S5, S6, S10, S11, S13
- [ ] **S16 · test** — TEST-1. Lands the harness extension and the cross-cutting scenarios.
      Tiers 0, 4, 7a, 8, 9, 11. Depends on: S10, S11, S13, S14
- [ ] **S17 · docs** — REC-1. Lands the finding and test-gap records, departures, new findings, claim-register note, records gate, and the P5b bullet.
      Tiers 0, 11. Depends on: S16
