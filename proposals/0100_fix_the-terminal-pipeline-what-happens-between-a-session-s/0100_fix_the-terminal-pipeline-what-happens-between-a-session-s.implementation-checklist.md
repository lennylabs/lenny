# Implementation checklist — 0100_fix_the-terminal-pipeline-what-happens-between-a-session-s

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. Lands parts (a) to (e): the §7.1 paragraphs, the §7.1 normal-flow and §29.4 reorder, the §28.5.1 `CH-ATTACH` **Timing.** clause, and the §12.8 **Lifecycle fence and drain.** clause.
      Tiers 0, 11. Depends on: —
- [x] **S2 · spec** — SPEC-2. Lands parts (a) to (c) in the §7.1 **Seal-and-export invariant:** and parts (d) to (f) in §16.1, §16.5, and §29.4.
      Tiers 0, 11. Depends on: S1
- [x] **S3 · spec** — SPEC-3. Lands the §7.1 **Artifact retention:** and derive rule 1 edits and the §12.5 retention item.
      Tiers 0, 11. Depends on: —
- [x] **S4 · spec** — SPEC-4. Lands the §10.1.7 paragraph, the §29.7 step edits, and the §7.1 **Seal-and-export invariant:** first-sentence pointer.
      Tiers 0, 11. Depends on: S1
- [x] **S5 · spec** — SPEC-5. Lands the §12.3 durability-table row.
      Tiers 0, 11. Depends on: S4
- [x] **S6 · spec** — SPEC-6. Lands parts (a) to (d): §5.2, §4.6.1, the §10.1.7 intent-row items, §4.4.2, §4.4.3, and the §28.5.1 and §28.8 `CH-CHECKPOINT` sites.
      Tiers 0, 11. Depends on: S1
- [x] **S7 · spec** — SPEC-7. Lands the §7.2 enumeration and paragraph edits and the §6.2 bullet sentence.
      Tiers 0, 11. Depends on: —
- [ ] **S8 · code** — CODE-1. Lands the bounded hook context, the detached gateway-decided writes, and the budget terminator claim.
      Tiers 0, 1, 7a. Depends on: S1
- [ ] **S9 · code** — CODE-2. Lands `WaitTerminalWork` and the runserver shutdown helper.
      Tiers 0, 1, 7a. Depends on: S4, S8
- [ ] **S10 · code** — CODE-3. Lands the write-identifier check in `pgstore.Store.Update` and its migration.
      Tiers 0, 1, 2, 8. Depends on: S5
- [ ] **S11 · code** — CODE-4. Lands every item.
      Tiers 0, 1, 2, 7a. Depends on: S1, S2, S6, S8
- [ ] **S12 · code** — CODE-5. Lands the primary-read row check and the Checkpointer binding release.
      Tiers 0, 1, 7a. Depends on: S2, S11
- [ ] **S13 · code** — CODE-6. Lands the startup gate, flag help, and chart gate edits.
      Tiers 0, 1. Depends on: S1, S6, S8
- [ ] **S14 · code** — CODE-7. Lands every item.
      Tiers 0, 1, 2, 7a, 8, 11. Depends on: S1, S3, S6, S7, S8, S10
- [ ] **S15 · test** — TEST-1. Lands the tests listed under TEST-1.
      Tiers 0, 1, 2, 4, 7a, 8. Depends on: S8, S9, S10, S11, S12, S13, S14
- [ ] **S16 · docs** — DOC-4. Lands the `docs/api/internal.md` Attach stream-end sentence.
      Tiers 0, 11. Depends on: S1, S11
- [ ] **S17 · docs** — DOC-1, DOC-2, DOC-3, and DOC-5. Lands every item, including the `rules.go` Description edit and the `make generate` outputs.
      Tiers 0, 1, 2, 11. Depends on: S1, S2, S3, S4, S6, S11, S12, S13, S14
- [ ] **S18 · docs** — REC-1. Lands the `BUILD-GAPS.md` resolutions, departures, and notes, the remediation-plan and claim-register owner moves, and the regenerated `tests/claim-map.json`.
      Tiers 0, 11. Depends on: S8, S9, S10, S11, S12, S13, S14, S15
