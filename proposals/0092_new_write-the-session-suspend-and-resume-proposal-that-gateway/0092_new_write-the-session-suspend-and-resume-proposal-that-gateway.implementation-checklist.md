## Implementation checklist

Every step lands after proposal 0091 is implemented.

- [ ] **S1 · spec** — SPEC-1. Lands every SPEC-1 edit.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands every SPEC-2 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands every SPEC-3 edit.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands every SPEC-4 edit.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands every SPEC-5 edit, with the SPEC-5u sweep last.
      Tiers 0, 11. Depends on: S1, S2, S3, S4
- [ ] **S6 · spec** — SPEC-6. Lands every SPEC-6 edit.
      Tiers 0, 11. Depends on: S3, S5
- [ ] **S7 · test** — TEST-0. Lands the `spec_28_register_writers_test.go` and `off_holder_matrix_stated_outcome_test.go` updates. Its tier-11 run passes every test except `TestFrameIdentifierAgreesAcrossSchemaSpecAndReference`, which is red from S5 until S8.
      Tiers 0, 11. Depends on: S2, S5
- [ ] **S8 · schema** — The non-spec **Heartbeat frames.** edits. Lands the `heartbeat` and `heartbeat_ack` schema, adapter-contract reference, and tier-3 address-test changes in one commit.
      Tiers 0, 3, 11. Depends on: S5
- [ ] **S9 · docs** — DOCS-1a. Lands the API, error-catalog, and SDK-example pages for `POST /resume`, `resume_session`, and `RESUME_FAILED`, and the `QUOTA_EXCEEDED` refusal of a resume at a concurrent-session limit.
      Tiers 0, 11. Depends on: S1, S2, S3, S5, S6
- [ ] **S10 · code** — CODE-2. Lands the retry accounting and the `ValidTransitions` edges with their tests.
      Tiers 0, 1, 2, 3. Depends on: S1
- [ ] **S11 · code** — CODE-3. Lands the post-claim callback and the release after the claim on every binder path with their tests.
      Tiers 0, 1. Depends on: S1
- [ ] **S12 · code** — CODE-5. Lands the widened Sweeper adoption, the peer-takeover check against the binding stamp, the `RecordHandoff` compare-and-swap, the pool-resolved readopt binding, the podless-`suspended` lease exclusion with the mirror release, and the takeover delivery hook, unset until S14, with their tests.
      Tiers 0, 1, 2, 8, 11. Depends on: S1, S2
- [ ] **S13 · code** — CODE-1. Lands the active-age column, accrual, and sweeps with their tests.
      Tiers 0, 1, 2. Depends on: S4
- [ ] **S14 · code** — CODE-4. Lands the resume driver with its `resuming` incarnation guard, the store-only `POST /resume`, the held-message enqueue and the delivery of held messages, the `completeResume` post-commit tail table, the delivery of held messages after a takeover with its Sweeper wiring, the `sweepResuming` guard, tree-recovery routing, the derive-failure fence comment, the removal of the durable-inbox `EXPIRE`, the credential assignment on the checkpoint resume with the non-retryable `credential_rejected` failure reason, the held-message delivery at `POST /start`, the failure-funnel release under the slot-accounting lock, the renewal stop for a released lease, the generation-scoped pod-release lease return, the primary-read deadline with its `--session-primary-read-timeout` flag, the `--max-resuming-seconds` help, and the single recorder of `lenny_session_resume_attempts_total` and its comment rewrites, with their tests.
      Tiers 0, 1, 2, 3, 4, 5, 7a. Depends on: S1, S2, S3, S5, S10, S11, S12, S13
- [ ] **S15 · code** — CODE-6. Lands the suspension release, idle suspension, the suspended-session lifetime, suspended-session message routing through the DLQ, the held-message delivery before each path-2 message and after a delegated child's task input, the backlog resume with the backlog check at release, the freshness-gauge correction, the adapter's activity clock with its count of platform and connector calls in flight and its session seal, the per-chunk idle-clock stamp of a proxy-mode LLM stream, the per-entry heartbeats from `session_start`, the idle expiry of a session that cannot be idle-suspended, the `busy` field and busy hold in the runtime SDKs and the compliance check, the terminate re-validation on the locked row, the DLQ expiry sweep of `resuming` rows, the `deadline_approaching` trigger carriers, the budget-key TTL and re-arm, the gateway leader's backlog re-check and its index migration, the user-erasure step for the session DLQ and durable inbox, the expiry of a session's pending elicitations when it leaves its pod, the flags, and the chart values with their tests.
      Tiers 0, 1, 2, 3, 4, 5, 7a, 10, 11. Depends on: S2, S5, S8, S13, S14
- [ ] **S16 · code** — CODE-7. Lands the concurrent-session count predicate and its partial index, the shared cap check, the resume admission on every write out of `suspended`, and the §16.5 session-availability population, with their tests.
      Tiers 0, 1, 2, 3, 4, 11. Depends on: S3, S5, S6, S14, S15
- [ ] **S17 · docs** — DOCS-1b. Lands every other reader page whole: the state machines, the session lifecycle, configuration, concepts, multi-tenancy, the observability SLO table, and the runtime-author pages.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S8
- [ ] **S18 · test** — TEST-1. Lands the new tests at tier 2 and above and the tier-0 and tier-11 checks.
      Tiers 0, 3, 4, 5, 7a, 8, 9, 10, 11. Depends on: S8, S9, S14, S15, S17
- [ ] **S19 · docs** — RECORDS-1. Lands the finding closures, the new findings, the claim rows, and the remediation-plan tick.
      Tiers 0, 11. Depends on: S16, S18
