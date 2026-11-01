# Implementation checklist — 0115_fix_f-12-9-24-12-9-requires-envelope-encryption

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §12.9 cell, paragraph, and bullet.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §4.9 paragraph, callout edits, and preflight sentence.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §17.6 row text.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · code** — CODE-1. Lands the warning constant and its unit test.
      Tiers 0, 1. Depends on: S1, S2, S3
- [ ] **S5 · docs** — DOCS-2. Lands the runbook edits and new section.
      Tiers 11. Depends on: S1, S2
- [ ] **S6 · code** — CHART-1. Lands the chart comments.
      Tiers 0, 11. Depends on: S1, S2, S5
- [ ] **S7 · docs** — DOCS-1. Lands the operator guide edits.
      Tiers 11. Depends on: S1, S2, S5
- [ ] **S8 · test** — TEST-1. Lands the documentation tests.
      Tiers 0, 11. Depends on: S1, S2, S5, S7
