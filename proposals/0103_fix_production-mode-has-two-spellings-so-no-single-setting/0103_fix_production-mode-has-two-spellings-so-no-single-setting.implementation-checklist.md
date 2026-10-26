# Implementation checklist — 0103_fix_production-mode-has-two-spellings-so-no-single-setting

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §17.9.1 edits (a) and (b).
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §11.7, §12.3, §16.4, §16.5, and §17.6 edits (a) to (j).
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · code** — CODE-1. Lands `pkg/deployenv` and its tests.
      Tiers 0, 1. Depends on: S1
- [ ] **S4 · code** — CODE-2. Lands the providerflags changes and their tests.
      Tiers 0, 1. Depends on: S1, S3
- [ ] **S5 · code** — CODE-3. Lands the gateway decision functions, call sites, and tests.
      Tiers 0, 1. Depends on: S1, S2, S4
- [ ] **S6 · code** — CODE-4 and CHART-1. Lands both in one commit with `make generate`, per CODE-4's closing note.
      Tiers 0, 1, 5, 9. Depends on: S1, S4
- [ ] **S7 · code** — CODE-5. Lands the preflight and ctlcli changes and their tests.
      Tiers 0, 1. Depends on: S1, S2, S3
- [ ] **S8 · code** — CODE-6. Lands the alert descriptions, metric text, and regenerated rule files.
      Tiers 0, 1, 11. Depends on: S1, S2
- [ ] **S9 · docs** — DOCS-1. Lands the install-wizard paragraph.
      Tiers 11. Depends on: S1, S6
- [ ] **S10 · test** — TEST-1. Lands the Tier 0 carrier guard.
      Tiers 0. Depends on: S1, S5, S6
