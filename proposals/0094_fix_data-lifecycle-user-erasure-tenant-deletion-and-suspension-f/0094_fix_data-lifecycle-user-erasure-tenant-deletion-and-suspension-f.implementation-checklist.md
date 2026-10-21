## Implementation checklist

Every step lands the tests the non-spec Testing section places under its deliverables, and classifies each failure those tests expose as Testing **Failures the tests find.** states.

- [x] **S1 · spec** — SPEC-1. Lands every SPEC-1 edit.
      Tiers 0, 11. Depends on: —
- [x] **S2 · spec** — SPEC-2. Lands every SPEC-2 edit.
      Tiers 0, 11. Depends on: S1
- [x] **S3 · spec** — SPEC-3. Lands both phase-table row edits.
      Tiers 0, 11. Depends on: S1
- [x] **S4 · spec** — SPEC-4. Lands every SPEC-4 edit.
      Tiers 0, 11. Depends on: S1, S2
- [x] **S5 · migration** — MIG-1. Lands migration 0186 and its tier-2 test.
      Tiers 0, 2. Depends on: S2
- [x] **S6 · migration** — MIG-2. Lands migration 0187 and its tier-2 test.
      Tiers 0, 2. Depends on: S1, S5
- [x] **S7 · code** — CODE-3. Lands the primary listing field, the tenant-scoped `ForceTerminate`, and `DrainSessions`.
      Tiers 0, 1, 2. Depends on: S1
- [x] **S8 · code** — CODE-1. Lands the creation predicate, both store fences, the session reservation, and `CreateWithTranscript` with its transcript copiers.
      Tiers 0, 1, 2, 7a. Depends on: S1, S2, S3, S4, S6, S7
- [x] **S9 · code** — CODE-2. Lands the creation gates, the interaction-resolution caller check, the source-owner check, the reservations and undo, the replay's in-transaction transcript copy, the refusal mapping, and the classifier entries.
      Tiers 0, 1, 3, 4, 7a. Depends on: S1, S2, S4, S8
- [x] **S10 · code** — CODE-4. Lands the suspend handler.
      Tiers 0, 1, 3, 7a. Depends on: S1, S4, S7
- [x] **S11 · code** — CODE-5. Lands the Phase 2 terminator wiring and the in-phase retry.
      Tiers 0, 1. Depends on: S1, S3, S7
- [x] **S12 · code** — CODE-6. Lands the Postgres erasure job store, the `Memory` store's Source Mode snapshot and restore rule, the one-transaction SQLite flush, the users restriction helper, the clear handler, the sampler change, and the `ErasureJobOverdue` expression.
      Tiers 0, 1, 2, 4, 11. Depends on: S2, S4, S5, S8
- [x] **S13 · code** — CODE-7. Lands the runner's phase dispatch and completion order, the handler changes, the leader-gated erasure sweep and sampler, the `cross_tenant_read` events, the salt lock, and the `ERASURE_JOB_NOT_FAILED` classifier entry.
      Tiers 0, 1, 2, 3, 4, 7a, 8. Depends on: S1, S2, S4, S7, S8, S11, S12
- [x] **S14 · code** — CODE-8. Lands the route removal and its test updates.
      Tiers 0, 1, 3, 4. Depends on: —
- [x] **S15 · docs** — DOCS-1. Lands every reader page edit.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S10, S11, S13
- [x] **S16 · test** — TEST-1. Lands the cross-cutting lifecycle scenarios.
      Tiers 0, 4, 5, 7a, 8, 9. Depends on: S9, S10, S11, S13, S14
- [x] **S17 · docs** — REC-1. Lands the finding closures, the new findings, and the P5 bullet update.
      Tiers 0, 11. Depends on: S16
