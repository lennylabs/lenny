## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Appends the overtaken-call sentence to the §15.1 finalize row.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · code** — CODE-2. Lands the entry compare-and-swap in handleFinalize and the Store.Update doc sentence.
      Tiers 0, 1. Depends on: S1
- [ ] **S3 · code** — CODE-1, CODE-3. Lands the exit guards in handleFinalize with their helpers in start.go and finalize.go; bundled because CODE-3 is CODE-1's only caller.
      Tiers 0, 1. Depends on: S1, S2
- [ ] **S4 · test** — TEST-1. Lands the tier-1 interleaving tests.
      Tiers 0, 1. Depends on: S2, S3
- [ ] **S5 · test** — TEST-2. Lands the tier-2 row-lock serialization subtest.
      Tiers 0, 2. Depends on: S2
- [ ] **S6 · test** — TEST-3. Lands the tier-4 real-binder race tests.
      Tiers 0, 4. Depends on: S2, S3
- [ ] **S7 · test** — TEST-4. Lands the tier-7a concurrent finalize and DELETE tests.
      Tiers 0, 7a. Depends on: S2, S3
- [ ] **S8 · docs** — DOCS-1. Lands the REST and MCP reference edits.
      Tiers 0, 11. Depends on: S1
