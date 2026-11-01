# Implementation checklist — 0113_fix_reader-access-and-the-10-2-permission-matrix-are

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §10.2 **Session ownership.** and **Role assignment:** edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §15 Open Responses edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §9.4 recall sentence.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the §15.1 route row.
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · spec** — SPEC-5. Lands the §27.3.1 failure-table row.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · spec** — SPEC-6. Lands the §10.1.3 item 5 sentence.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-7. Lands the §12.3 sentence.
      Tiers 0, 11. Depends on: S1
- [ ] **S8 · code** — CODE-1, CODE-2, CODE-4. Lands the matrix predicate, the dev role, and the `lookupCallerSession` step in one commit, as their landing constraint requires.
      Tiers 0, 1, 3, 9. Depends on: S1
- [ ] **S9 · code** — CODE-3. Lands the create services' mux dispatch.
      Tiers 0, 1, 3, 9. Depends on: S1, S8
- [ ] **S10 · code** — CODE-5. Lands the Open Responses checks.
      Tiers 0, 1, 3, 7a, 9. Depends on: S1, S2, S8
- [ ] **S11 · code** — CODE-6. Lands owner-only memory recall.
      Tiers 0, 1, 3, 9. Depends on: S1, S3, S4, S8
- [ ] **S12 · code** — CODE-7. Lands role refresh and the one read evaluation.
      Tiers 0, 1, 3. Depends on: S1, S8
- [ ] **S13 · code** — CODE-8. Lands the ticker case.
      Tiers 0, 1, 3, 7a. Depends on: S1, S5, S6, S7, S12
- [ ] **S14 · test** — TEST-1. Lands the cross-tier tests.
      Tiers 3, 4, 7a, 8, 9. Depends on: S8, S9, S10, S11, S12, S13
- [ ] **S15 · docs** — DOCS-1. Lands the documentation pages.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5
- [ ] **S16 · docs** — RECORDS-1. Lands the finding closures and the plan update.
      Tiers 0. Depends on: S14, S15
