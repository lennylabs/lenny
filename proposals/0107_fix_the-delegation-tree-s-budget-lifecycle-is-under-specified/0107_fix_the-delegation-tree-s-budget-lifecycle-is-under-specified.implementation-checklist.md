# Implementation checklist — 0107_fix_the-delegation-tree-s-budget-lifecycle-is-under-specified

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §7.1 terminal pipeline lists the budget return.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §8.3 step 3 return ownership.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §8.3 TTL, sentinel creation, and cleanup edits (a) to (d).
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-8. §16.1 metric row and §16.5 alert row edits (a) and (b).
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · spec** — SPEC-4. §8.6 edits (a) to (d) and the **Extension state:** paragraph.
      Tiers 0, 11. Depends on: —
- [ ] **S6 · spec** — SPEC-5. §8.6 elicitation flow item 2.
      Tiers 0, 11. Depends on: S5
- [ ] **S7 · spec** — SPEC-7. §9.2 item 6.
      Tiers 0, 11. Depends on: S6
- [ ] **S8 · spec** — SPEC-9. §19 row 13.
      Tiers 0, 11. Depends on: S5
- [ ] **S9 · spec** — SPEC-6. §12.8 step 16 and tenant Phase 4.
      Tiers 0, 11. Depends on: —
- [ ] **S10 · code** — CODE-2. The `BUDGET_KEYS_EXPIRED` cleanup handler and the extracted cancel helper.
      Tiers 0, 1, 7a. Depends on: S3, S4
- [ ] **S11 · code** — CODE-6. The store-backed budget source, with registration deleted.
      Tiers 0, 1, 2, 3, 4, 7a, 9. Depends on: S5
- [ ] **S12 · code** — CODE-1, CODE-4. The script changes and the `delegate_task` side, bundled because the sentinel check without the first-reservation create fails every new tree, and the mirror deletion breaks the build without the row-raised cap.
      Tiers 0, 1, 2, 4, 7a. Depends on: S3, S5, S10, S11
- [ ] **S13 · code** — CODE-3. The return in `afterTerminalWrite`.
      Tiers 0, 1, 4, 7a. Depends on: S1, S2, S3, S10, S12
- [ ] **S14 · code** — CODE-5. The forget move and the `terminating` flag.
      Tiers 0, 1, 7a. Depends on: S5, S11, S13
- [ ] **S15 · code** — CODE-7.
      Tiers 0, 1, 7a. Depends on: S5, S6, S7, S8, S11
- [ ] **S16 · code** — CODE-8. The per-replica sweep and `ListAbsentSessions`.
      Tiers 0, 1, 4, 7a. Depends on: S5, S11, S14
- [ ] **S17 · code** — CODE-9. `DeleteByRoot` and the eraser call.
      Tiers 0, 1, 4, 9. Depends on: S9
- [ ] **S18 · docs** — DOC-1.
      Tiers 0, 1, 2, 11. Depends on: S4
