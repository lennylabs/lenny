## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §14 `uploadRef` field note, the canonical-example trim, and the `gitClone.ref` wording edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §14.1 **Plan binding.** paragraph and the layer-1 and replay wording edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §15.1 and §15.2 row edits.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the §7.1 flow-step and §7.4 edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. Lands the §29.2 step edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-6. Lands the §24, §24.17, and §26.2 edits.
      Tiers 0, 11. Depends on: S2, S3
- [ ] **S7 · code** — CODE-1. Lands the shared-resolver `uploadRef` scope check and its comment retargeting.
      Tiers 0, 1, 2, 4. Depends on: S1, S2
- [ ] **S8 · code** — CODE-2. Lands the `lenny/finalize_workspace` input schema.
      Tiers 0, 1, 3. Depends on: S2, S3
- [ ] **S9 · code** — CODE-3. Lands the OpenAPI finalize body and responses and the `uploadRef` descriptions.
      Tiers 0, 3. Depends on: S1, S2, S3
- [ ] **S10 · code** — CODE-4. Lands the SDK and gateway comment corrections.
      Tiers 0, 1. Depends on: S1, S2, S4
- [ ] **S11 · test** — TEST-1. Lands the tests across tiers 1, 2, 3, and 9.
      Tiers 0, 1, 2, 3, 4, 9. Depends on: S7, S8, S9
- [ ] **S12 · docs** — DOCS-1. Lands the reader-facing upload and finalize documentation.
      Tiers 0, 11. Depends on: S1, S2, S3, S6, S9
