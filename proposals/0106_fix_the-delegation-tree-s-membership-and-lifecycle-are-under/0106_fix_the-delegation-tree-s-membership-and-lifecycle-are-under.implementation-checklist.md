# Implementation checklist — 0106_fix_the-delegation-tree-s-membership-and-lifecycle-are-under

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands Edits (a), (b), and (c) in §8.10.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §7.2 state-machine line.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. Lands Edit (a) in §8.10 and Edit (b) in §7.2.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · code** — CODE-A, with TEST-1 item 1's tier-1 cases.
      Tiers 0, 1. Depends on: —
- [ ] **S5 · code** — CODE-B. Lands items 1 to 6, with TEST-1 items 2 and 3.
      Tiers 0, 1. Depends on: S1, S2
- [ ] **S6 · code** — CODE-C items 1 to 6, with TEST-1 item 4's tier-1 cases.
      Tiers 0, 1. Depends on: S3
- [ ] **S7 · code** — CODE-D. Lands `effectiveCascade` and its call sites, with TEST-1 item 6.
      Tiers 0, 1. Depends on: S3, S6
- [ ] **S8 · code** — CODE-E, with TEST-1 item 7.
      Tiers 0, 1. Depends on: —
- [ ] **S9 · code** — CODE-F. Lands the detached rollback context, with TEST-1 item 8.
      Tiers 0, 1. Depends on: —
- [ ] **S10 · test** — TEST-1. Lands the tier-2, tier-4, and tier-7a tests, item 5, and item 9.
      Tiers 0, 1, 2, 4, 7a, 8, 11. Depends on: S4, S5, S6, S7, S8, S9
- [ ] **S11 · docs** — REC-1. Lands the BUILD-GAPS records and the P4a plan entry.
      Tiers 0, 11. Depends on: S10
