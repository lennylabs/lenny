# Implementation checklist — 0104_fix_two-gaps-leave-lenny-s-session-scoped-and-delegation

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §10.2 and §10.3 edits, after 0102's spec steps (summary **Impacts on other proposals**, row 0102).
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. Lands the §12.8 *Creation fence* edits.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-2. Lands the §8.1 and §8.2 edits.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the §15 edits.
      Tiers 0, 11. Depends on: S2, S3
- [ ] **S5 · spec** — SPEC-5. Lands the restatement edits across the remaining spec files.
      Tiers 0, 11. Depends on: S1, S3
- [ ] **S6 · code** — CODE-2. Lands the fence rule in sessionstore, pgstore, and memstore, and the cascade's primary read in sessionserver `usage.go`.
      Tiers 0, 1, 2, 7. Depends on: S2, S3, S4
- [ ] **S7 · code** — CODE-1. Lands the deletion and the registry edits, in the same proposal merge as S6.
      Tiers 0, 1, 3. Depends on: S3, S4, S6
- [ ] **S8 · code** — CODE-3. Lands the caller-check function and its call sites, after 0102 CODE-2 item 7.
      Tiers 0, 1, 3, 4, 9. Depends on: S1, S5
- [ ] **S9 · code** — CODE-4. Lands the alert description and the regenerated rule files.
      Tiers 0, 1, 11. Depends on: S5
- [ ] **S10 · test** — TEST-1. Lands the tests in the Testing section.
      Tiers 1, 2, 3, 7, 8, 9. Depends on: S6, S7, S8
- [ ] **S11 · docs** — DOCS-1. Lands the docs edits.
      Tiers 11. Depends on: S1, S2, S3, S4, S5, S9
- [ ] **S12 · docs** — REC-1. Lands the record closures, the new BUILD-GAPS finding and queue entry, and the regenerated claim-register row.
      Tiers 0, 11. Depends on: S6, S7, S8, S9, S10, S11
