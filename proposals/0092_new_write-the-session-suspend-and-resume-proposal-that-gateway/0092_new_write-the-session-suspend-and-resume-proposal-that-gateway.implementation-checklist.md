## Implementation checklist

Every step lands after proposal 0091 is implemented.

- [ ] **S1 · spec** — SPEC-1. Lands every SPEC-1 edit, SPEC-1a through SPEC-1g2.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands every SPEC-2 edit, SPEC-2a through SPEC-2d.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands every SPEC-3 edit, SPEC-3a through SPEC-3q.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the SPEC-4a edit.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands every SPEC-5 edit, SPEC-5a through SPEC-5z, with the SPEC-5u sweep last.
      Tiers 0, 11. Depends on: S1, S3, S4
- [ ] **S6 · test** — TEST-0. Lands the `spec_28_register_writers_test.go` and `off_holder_matrix_stated_outcome_test.go` updates.
      Tiers 0, 11. Depends on: S2, S5
- [ ] **S7 · docs** — DOCS-1a. Lands the API, error-catalog, and SDK-example pages for `POST /resume`, `resume_session`, and `RESUME_FAILED`.
      Tiers 0, 11. Depends on: S1, S2, S3
- [ ] **S8 · code** — CODE-2. Lands the retry accounting and the `ValidTransitions` edges with their tests.
      Tiers 0, 1, 2, 3. Depends on: S1
- [ ] **S9 · code** — CODE-3. Lands the post-claim callback and the release after the claim on every binder path with their tests.
      Tiers 0, 1. Depends on: S1
- [ ] **S10 · code** — CODE-5. Lands the widened Sweeper adoption, the peer-takeover check against the binding stamp, the `RecordHandoff` compare-and-swap, the pool-resolved readopt binding, and the podless-`suspended` lease exclusion with the mirror release, with their tests.
      Tiers 0, 1, 2, 8, 11. Depends on: S1, S2
- [ ] **S11 · code** — CODE-1. Lands the active-age column, accrual, and sweeps with their tests.
      Tiers 0, 1, 2. Depends on: S4
- [ ] **S12 · code** — CODE-4. Lands the resume driver with its `resuming` incarnation guard, the store-only `POST /resume`, the held-message enqueue and the delivery of held messages, the `sweepResuming` guard, tree-recovery routing, the derive-failure fence comment, the removal of the durable-inbox `EXPIRE`, the credential assignment on the checkpoint resume, the held-message delivery at `POST /start`, the failure-funnel release under the slot-accounting lock, the renewal stop for a released lease, and the `--max-resuming-seconds` help with their tests.
      Tiers 0, 1, 2, 3, 4, 5, 7a. Depends on: S1, S2, S3, S5, S8, S9, S10, S11
- [ ] **S13 · code** — CODE-6. Lands the suspension release, idle suspension, the suspended-session lifetime, suspended-session message routing through the DLQ, the held-message delivery before each path-2 message and after a delegated child's task input, the backlog resume with the backlog check at release, the freshness-gauge correction, the delivery admission write, the DLQ expiry sweep of `resuming` rows, the `deadline_approaching` trigger carriers, the budget-key TTL and re-arm, the flags, and the chart values with their tests.
      Tiers 0, 1, 2, 3, 5, 7a, 11. Depends on: S5, S11, S12
- [ ] **S14 · docs** — DOCS-1b. Lands every other reader page whole: the state machines, the session lifecycle, configuration, concepts, and the runtime-author pages.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5
- [ ] **S15 · test** — TEST-1. Lands the new tests at tier 2 and above and the tier-0 and tier-11 checks.
      Tiers 0, 3, 4, 5, 7a, 8, 9, 11. Depends on: S7, S12, S13, S14
- [ ] **S16 · docs** — RECORDS-1. Lands the finding closures, the new findings, the claim rows, and the remediation-plan tick.
      Tiers 0, 11. Depends on: S15
