# Implementation checklist — 0098_fix_the-gateway-exits-at-startup-whenever-the-playground-is

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §27.8 label rename.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · code** — CODE-1. Lands the label literal, its tier-1 test, and the spec-map entry.
      Tiers 0, 1. Depends on: S1
- [ ] **S3 · test** — TEST-1. Lands the tier-4 playground-bearer case.
      Tiers 0, 4. Depends on: S2
- [ ] **S4 · docs** — REC-1. Lands the F-27.8.1 closure and its record edits in one commit.
      Tiers 0, 11. Depends on: S3
