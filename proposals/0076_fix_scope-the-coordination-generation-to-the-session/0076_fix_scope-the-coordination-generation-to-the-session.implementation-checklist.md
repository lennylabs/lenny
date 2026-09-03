## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §10.1 states the counter's baseline of 1, §10.1.2 states the pod-side fenced
      generation per bound session with its initial condition, its per-session gap reset, and step 3's
      acceptance rule, §10.1.8 step 1 applies that rule to the barrier, and §10.1.4 states what the
      pod-level arming event and each terminated session's records carry.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The `spec/28` and `spec/29` mirrors of the record-and-reject rule, the gap
      reset, and the barrier's outcome take the wording SPEC-1 states, and §29.10's co-tenancy
      classification records the per-session generation and the pod-scoped hold as answered.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §4.2's session-record paragraph states that a newly created session row
      carries `coordination_generation = 1` and that the first coordinator handoff mints 2.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · schema** — SCHEMA-1. The fence and barrier RPC, message, and field doc comments and the
      operational-RPC `coordination_generation` field comments take the wording SPEC-2 states for each, and
      `make generate-proto` regenerates the stubs in the same commit.
      Tiers 0, 3. Depends on: S2
- [ ] **S5 · code** — CODE-1 and CODE-3. The fenced generation and the barrier gate move onto the slot
      registry entry, and the hold reports each terminated session's own generation. CODE-1 gives
      `Server.LastFencedGeneration` a session id and CODE-3 deletes its only production caller, so no tree
      between the two deliverables compiles.
      Tiers 0, 1, 2, 4, 7a, 8. Depends on: S1
- [ ] **S6 · code** — CODE-4. The session row's coordination generation is baselined at 1 in migration 0181
      and on both session-store create paths.
      Tiers 0, 1, 2, 3, 4, 7a, 8. Depends on: S1, S3
- [ ] **S7 · code** — CODE-2. `CheckpointBarrier`'s gate reads the per-session generation and accepts a
      barrier for a bound session the pod holds no fenced generation for, which the counter baseline S6
      lands makes reachable.
      Tiers 0, 1, 3, 4, 7a. Depends on: S1, S5, S6
- [ ] **S8 · test** — TEST-1. Two co-tenant sessions handing off, draining, and losing their coordinator
      independently, on proposal 0060's two-replica harness.
      Tiers 1, 4, 7a. Depends on: S5, S6, S7
