# Implementation checklist — 0114_fix_upload-tokens-and-the-playground-oidc-state-depend-on

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §7.1 and §7.4 `uploadToken` text and the §11.1 upload cap scope.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §15.1 `uploadToken` edits and the §18 bullets.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §27.3.1, §15.1 playground row, §12.4, and §18.28 edits.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · code** — CODE-1, CODE-2. Lands row verification in the package and at both session server call sites.
      Tiers 0, 1, 3, 4. Depends on: S1, S2
- [ ] **S5 · code** — CODE-3. Lands the key material deletion, the gateway wiring removal, and the constructor sweep.
      Tiers 0, 1, 4. Depends on: S1, S2, S4
- [ ] **S6 · code** — CODE-4. Lands the connector signer removal.
      Tiers 0, 1, 4. Depends on: S3
- [ ] **S7 · code** — CODE-5. Lands the playground state move and the shared store helper.
      Tiers 0, 1, 4. Depends on: S3, S6
- [ ] **S8 · docs** — DOCS-1. Lands the client documentation.
      Tiers 0, 11. Depends on: S1, S2, S4
- [ ] **S9 · test** — TEST-1. Lands the tests that no code step carries and records the red run.
      Tiers 0, 1, 3, 4, 5, 9, 11. Depends on: S1, S2, S3, S5, S7, S8
- [ ] **S10 · docs** — RECORDS-1. Lands the `BUILD-GAPS.md` and plan records.
      Tiers 0, 11. Depends on: S9
