## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §10.1 states the generation's scope on the pod side and what a hold covers.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · schema** — SCHEMA-1. The proto doc comments state the settled scope.
      Tiers 0, 3. Depends on: S1
- [ ] **S3 · code** — CODE-1. The fenced generation moves onto the slot registry entry.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S4 · code** — CODE-2. `CheckpointBarrier`'s gate reads the per-session generation.
      Tiers 0, 1, 3. Depends on: S3
- [ ] **S5 · code** — CODE-3. Hold state takes the scope §7's decision settles.
      Tiers 0, 1, 8. Depends on: S1, S3
- [ ] **S6 · test** — TEST-1. Two co-tenant sessions handing off independently, on proposal 0060's
      two-replica harness.
      Tiers 7a, 8. Depends on: S3, S4, S5
