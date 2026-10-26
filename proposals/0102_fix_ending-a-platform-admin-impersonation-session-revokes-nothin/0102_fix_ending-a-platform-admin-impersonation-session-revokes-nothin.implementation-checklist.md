# Implementation checklist — 0102_fix_ending-a-platform-admin-impersonation-session-revokes-nothin

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §13.3 edits (a) to (c).
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §10.2 edits (a) to (d).
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §12.2 and §12.6 edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the §16.7 edits (a) and (b).
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. Lands the §11.4 edits (a) and (b).
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · spec** — SPEC-6. Lands the §15.1 edits (a) and (b).
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-7. Lands the §11.7, §15.1, §25.11, and §16.5 edits (a) to (d).
      Tiers 0, 11. Depends on: S1
- [ ] **S8 · code** — CODE-1. Lands CODE-1.
      Tiers 0, 1, 2. Depends on: S3
- [ ] **S9 · code** — CODE-2. Lands CODE-2.
      Tiers 0, 1, 2, 3. Depends on: S1, S2, S3, S5, S6, S8
- [ ] **S10 · code** — CODE-3 and CODE-4. Lands CODE-3 and CODE-4.
      Tiers 0, 1, 2, 3, 4, 5, 8, 9. Depends on: S1, S4, S6, S7, S9
- [ ] **S11 · code** — CODE-5. Lands CODE-5.
      Tiers 0, 1, 4, 9. Depends on: S1, S5, S9
- [ ] **S12 · test** — TEST-1. Lands TEST-1.
      Tiers 0, 1, 2, 3. Depends on: S9, S10, S11
- [ ] **S13 · test** — TEST-2. Lands TEST-2.
      Tiers 0, 4, 9. Depends on: S9, S10, S11
- [ ] **S14 · docs** — DOCS-1. Lands DOCS-1.
      Tiers 0, 11. Depends on: S1, S2, S6, S7, S12, S13
