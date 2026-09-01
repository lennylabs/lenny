## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, and SPEC-3. §10.1 states the generation's scope on the pod side, what
      a hold covers, and the counter's baseline; §4.2 states the same baseline on the session record; and
      §28 and §29 state the same scope.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · schema** — SCHEMA-1. The proto doc comments state the settled scope, and
      `make generate-proto` regenerates the stubs in the same commit.
      Tiers 0, 3. Depends on: S1
- [ ] **S3 · code** — CODE-1 and CODE-3. The fenced generation and the barrier gate move onto the slot
      registry entry, and the hold reports each terminated session's own generation. CODE-1 gives
      `Server.LastFencedGeneration` a session id and CODE-3 deletes its only production caller, so no tree
      between the two deliverables compiles.
      Tiers 0, 1, 2, 4, 7a, 8. Depends on: S1
- [ ] **S4 · code** — CODE-4. The session row's coordination generation is baselined at 1.
      Tiers 0, 1, 2, 3, 4, 7a, 8. Depends on: S1
- [ ] **S5 · code** — CODE-2. `CheckpointBarrier`'s gate reads the per-session generation.
      Tiers 0, 1, 3, 7a. Depends on: S1, S3, S4
- [ ] **S6 · test** — TEST-1. Two co-tenant sessions handing off independently, on proposal 0060's
      two-replica harness.
      Tiers 1, 4, 7a. Depends on: S3, S4, S5
