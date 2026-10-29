# Implementation checklist — 0109_fix_a-derived-or-replayed-session-starts-without-its-source

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands SPEC-1(a) through (d) in §7.1, §7.4, and §15.1.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands SPEC-2(a) and (b) in §4.7.1.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands SPEC-3(a) in §5.1 and the SPEC-3(b) deletion in §14.1.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the §19 Decision 12 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. Lands SPEC-5(a) through (e) in §13 and §4.4.3.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · spec** — SPEC-6. Lands SPEC-6(a) through (d) in §28.5.5 and §28.8.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-7. Lands the §6.1 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S8 · code** — CODE-1. Lands the proto field, regenerated bindings, and the adapter client carrier.
      Tiers 0, 1, 3. Depends on: S1, S2
- [ ] **S9 · code** — CODE-2. Lands the adapter seed, pairing refusal, and error mapping.
      Tiers 0, 1, 3, 10. Depends on: S1, S2, S3, S8
- [ ] **S10 · code** — CODE-3. Lands the Binder restore call sites and the demotion clause.
      Tiers 0, 1. Depends on: S1, S7, S8, S9
- [ ] **S11 · code** — CODE-4. Lands the composer restore closure.
      Tiers 0, 1, 4. Depends on: S1, S5, S6, S10
- [ ] **S12 · code** — CODE-5. Lands the chunked-only derive and replay copy.
      Tiers 0, 1, 4, 7a, 8, 9. Depends on: S1
- [ ] **S13 · test** — TEST-1. Lands the tests under the non-spec Testing section.
      Tiers 0, 1, 3, 4, 10. Depends on: S9, S10, S11, S12
- [ ] **S14 · docs** — DOC-1. Lands the docs pages, chart comment, and tier-11 mirror check.
      Tiers 0, 11. Depends on: S1, S4, S5, S13
- [ ] **S15 · docs** — REC-1. Lands the BUILD-GAPS and remediation-plan records.
      Tiers 0. Depends on: S13, S14
