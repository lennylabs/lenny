# Implementation checklist — 0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the staged §14 edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §14.1 **Plan binding.** paragraph and every other SPEC-2 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-8. Lands the §14.1 **Runtime workspace defaults.** paragraph and every other SPEC-8 edit.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-3. Lands the staged §15.1 and §15.2 row edits.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S5 · spec** — SPEC-4. Lands the staged §7.1 and §7.4 edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-5. Lands the staged §29.2 step edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S7 · spec** — SPEC-6. Lands every staged §24 and §26 edit.
      Tiers 0, 11. Depends on: S2, S4
- [ ] **S8 · spec** — SPEC-7. Lands the §7.5 setup-timing home and every SPEC-7 edit that cites it.
      Tiers 0, 11. Depends on: —
- [ ] **S9 · code** — CODE-1. Lands the shared-resolver `uploadRef` scope check and its comment retargeting.
      Tiers 0, 1, 2, 4. Depends on: S1, S2
- [ ] **S10 · code** — CODE-2. Lands the `lenny/finalize_workspace` input schema.
      Tiers 0, 1, 3. Depends on: S2, S4
- [ ] **S11 · code** — CODE-3. Lands the OpenAPI and published-schema edits.
      Tiers 0, 3. Depends on: S1, S2, S4
- [ ] **S12 · code** — CODE-4. Lands the SDK and gateway comment corrections.
      Tiers 0, 1. Depends on: S1, S2, S5
- [ ] **S13 · code** — CODE-5. Lands the proto and setup comment corrections and the regenerated bindings.
      Tiers 0, 1. Depends on: S8
- [ ] **S14 · code** — CODE-6. Lands the runtime-defaults composer, its callers, and the registration check.
      Tiers 0, 1, 3, 4. Depends on: S3, S9
- [ ] **S15 · test** — TEST-1. Lands the TEST-1 tests.
      Tiers 0, 1, 2, 3, 4, 9. Depends on: S8, S9, S10, S11
- [ ] **S16 · docs** — DOCS-1. Lands every DOCS-1 page change.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S7, S8, S11
- [ ] **S17 · test** — TEST-2. Lands the TEST-2 tests.
      Tiers 0, 1, 3, 4. Depends on: S14
