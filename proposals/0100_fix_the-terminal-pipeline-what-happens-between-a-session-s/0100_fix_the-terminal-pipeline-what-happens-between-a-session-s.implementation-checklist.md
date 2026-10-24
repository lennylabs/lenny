# Implementation checklist — 0100_fix_the-terminal-pipeline-what-happens-between-a-session-s

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §7.1 **Terminal pipeline context:** paragraph.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the two **Seal-and-export invariant:** edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §7.1 **Artifact retention:** and derive rule 1 edits and the §12.5 retention item.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the §10.1.7 paragraph and the §29.7 step edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. Lands the §12.3 durability-table row.
      Tiers 0, 11. Depends on: —
- [ ] **S6 · spec** — SPEC-6. Lands the §5.2 claimOrphanTimeout sentence edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-7. Lands the §7.2 enumeration and paragraph edits and the §6.2 bullet sentence.
      Tiers 0, 11. Depends on: —
- [ ] **S8 · code** — CODE-1. Lands the bounded hook context, the detached gateway-decided writes, and the budget terminator claim.
      Tiers 0, 1, 7a. Depends on: S1
- [ ] **S9 · code** — CODE-2. Lands `WaitTerminalWork` and the runserver shutdown helper.
      Tiers 0, 1, 7a. Depends on: S4, S8
- [ ] **S10 · code** — CODE-3. Lands the commit-status check in `pgstore.Store.Update`.
      Tiers 0, 1, 2, 8. Depends on: S5
- [ ] **S11 · code** — CODE-4. Lands the seal outcome fixes and the closing gate.
      Tiers 0, 1, 7a. Depends on: S1, S2
- [ ] **S12 · code** — CODE-5. Lands the primary-read row check and the Checkpointer binding release.
      Tiers 0, 1, 7a. Depends on: S2, S11
- [ ] **S13 · code** — CODE-6. Lands the startup gate, flag help, and chart gate edits.
      Tiers 0, 1. Depends on: S1, S6
- [ ] **S14 · code** — CODE-7. Lands the retention settle, resume-cleanup scope, fenced derive-failure insert, and terminate precondition.
      Tiers 0, 1, 2, 7a. Depends on: S3, S7, S8
- [ ] **S15 · test** — TEST-1. Lands the tests listed under TEST-1.
      Tiers 0, 1, 2, 7a, 8. Depends on: S8, S9, S10, S11, S12, S13, S14
- [ ] **S16 · docs** — REC-1. Lands the `BUILD-GAPS.md` resolutions, departures, and notes.
      Tiers 0, 11. Depends on: S15
