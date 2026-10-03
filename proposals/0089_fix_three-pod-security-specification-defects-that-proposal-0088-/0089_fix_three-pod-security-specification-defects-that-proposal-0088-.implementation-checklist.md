## Implementation checklist

- [ ] **S1 · spec** — SPEC-A. Lands both §13.1 membership-paragraph edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-C. Lands the §13.1 residual sentences.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-B1. Lands the §17.2 paragraph and the item 3 and item 13 edits.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-B2. Lands the §13.1 carrier edits and the §15.1 row deletion.
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · code** — CODE-B. Lands the errorclassify deletion and the comment edits.
      Tiers 0, 1, 3. Depends on: S3, S4
- [ ] **S6 · docs** — DOCS-B. Lands the docs error-catalog row deletion.
      Tiers 0, 11. Depends on: S4
