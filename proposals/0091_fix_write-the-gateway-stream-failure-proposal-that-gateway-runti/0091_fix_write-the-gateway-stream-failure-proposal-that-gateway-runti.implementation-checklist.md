## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the mid-session failure-outcome reconciliation, all of SPEC-1.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2, SPEC-3. Lands the `CH-ATTACH` stream rules and their pointers (all of SPEC-2) with the §28.8 rows (SPEC-3), in one edit as SPEC-3 requires.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-4. Lands the §5.2 **Failure isolation:** append, the retire-on-failure scoping in §4.6.3, §5.2, §6.1, and §6.2, and the §6.2 per-slot line.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · docs** — DOCS-1. Lands the reader-doc edits that follow SPEC-1, SPEC-2, and SPEC-4.
      Tiers 0, 11. Depends on: S1, S2, S3
- [ ] **S5 · code** — CODE-1. Lands the session-scoped stream, the reader, the turn token, and cancel-based eviction.
      Tiers 0, 1. Depends on: S2
- [ ] **S6 · test** — TEST-1. Lands the step-1 tests.
      Tiers 0, 1, 2, 4, 5, 7a. Depends on: S5
- [ ] **S7 · code** — CODE-2. Lands the release funnel, the slot accounting, and the release before a resume rebinds, with their tests.
      Tiers 0, 1, 2, 7a. Depends on: S1, S2, S3, S6
- [ ] **S8 · code** — CODE-3. Lands the deliverable with its tests.
      Tiers 0, 1. Depends on: S1, S2, S7
- [ ] **S9 · test** — TEST-2. Lands the step-2 tests.
      Tiers 0, 4, 5, 7a, 8, 11. Depends on: S4, S8
- [ ] **S10 · code** — CODE-5 and its tests.
      Tiers 0, 1, 8. Depends on: S2, S5
- [ ] **S11 · docs** — RECORDS-1. Lands the claim rows, the finding closures and new findings, and the remediation-plan tick.
      Tiers 0, 11. Depends on: S6, S9, S10
