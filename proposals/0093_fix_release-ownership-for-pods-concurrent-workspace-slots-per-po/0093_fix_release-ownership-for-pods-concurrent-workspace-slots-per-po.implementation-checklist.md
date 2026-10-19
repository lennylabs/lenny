## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the staged edits SPEC-1a to SPEC-1l.
      Tiers 0. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the staged edits SPEC-2a to SPEC-2u.
      Tiers 0. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the staged edits SPEC-3a and SPEC-3b.
      Tiers 0. Depends on: —
- [ ] **S4 · docs** — DOCS-1. Lands the reader-page edits and the tier-11 gate retargets the spec steps and those edits need.
      Tiers 0, 11. Depends on: S1, S2, S3
- [ ] **S5 · code** — CODE-8. Lands the comment edits and the `make generate` and `make generate-proto` regenerations.
      Tiers 0, 1. Depends on: S2
- [ ] **S6 · code** — CODE-1. Lands the slot set primitive and its callers, the hash-tagged per-pod keys, and the slot-holder predicate, with the tests **Testing** assigns to CODE-1.
      Tiers 0, 1, 2, 4, 7a, 8. Depends on: S1, S2
- [ ] **S7 · code** — CODE-2. Lands the slot-aware `ReclaimClaimed`, with the tests **Testing** assigns to CODE-2.
      Tiers 0, 1, 2. Depends on: S1, S2, S6
- [ ] **S8 · code** — CODE-3. Lands the finalize exits, with the tests **Testing** assigns to CODE-3.
      Tiers 0, 1. Depends on: S2, S7
- [ ] **S9 · code** — CODE-4. Lands the created sweeper deletion, with the tests **Testing** assigns to CODE-4.
      Tiers 0, 1. Depends on: S2, S7
- [ ] **S10 · code** — CODE-5. Lands the hook's stale-binding step, with the tests **Testing** assigns to CODE-5.
      Tiers 0, 1. Depends on: —
- [ ] **S11 · code** — CODE-6. Lands the slot-accounting lock on the hook's release step, with the tests **Testing** assigns to CODE-6.
      Tiers 0, 1, 7a. Depends on: S6, S10
- [ ] **S12 · code** — CODE-7. Lands the full_revoke release routing, with the tests **Testing** assigns to CODE-7.
      Tiers 0, 1, 2, 3, 9. Depends on: S3, S7, S11
- [ ] **S13 · code** — CODE-9. Lands the stale-slot reaper, its flags and chart values, the shared claim-orphan timeout with its default and its checks, the launch bound, the per-row terminal stamp, and the adapter's `absent` answer after an unclean or in-flight `Shutdown` teardown, with the tests **Testing** assigns to CODE-9.
      Tiers 0, 1, 2, 3, 4, 5, 10, 11. Depends on: S1, S6
- [ ] **S14 · test** — TEST-1. Lands the harness extension and the cross-tier tests.
      Tiers 0, 1, 2, 4, 5, 7a, 8, 9, 11. Depends on: S1, S2, S3, S4, S5, S6, S7, S8, S9, S10, S11, S12, S13
- [ ] **S15 · docs** — REC-1. Lands the BUILD-GAPS closures, annotations, and filings.
      Tiers 0, 11. Depends on: S14
