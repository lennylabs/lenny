## Implementation checklist

Every step lands after proposal 0091 is implemented. Every step fixes or files each test failure it exposes, as Testing **Failures the tests find.** in the non-spec changes states.

- [ ] **S1 · spec** — SPEC-1. Lands every SPEC-1 edit.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands every SPEC-2 edit. Its tier-11 run is red for `TestOffHolderMatrixRowsStateTheirOutcome` and `TestOffHolderMatrixPreambleMatchesForwardingRows` from S2 until S7.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands every SPEC-3 edit.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands every SPEC-4 edit.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands every SPEC-5 edit, with the SPEC-5u sweep last. Its tier-11 run is red for the `inter-replica link` subtest of `TestSection28RegisterWritersMatchTheSpec_spec_28_3` from S5 until S7, and for `TestFrameIdentifierAgreesAcrossSchemaSpecAndReference` from S5 until S8.
      Tiers 0, 11. Depends on: S1, S2, S3, S4
- [ ] **S6 · spec** — SPEC-6. Lands every SPEC-6 edit.
      Tiers 0, 11. Depends on: S3, S5
- [ ] **S7 · test** — TEST-0. Lands the tier-11 spec-text test updates that intents T0.1 and T0.2 state. Its tier-11 run passes every test except `TestFrameIdentifierAgreesAcrossSchemaSpecAndReference`, which is red from S5 until S8.
      Tiers 0, 11. Depends on: S2, S5
- [ ] **S8 · schema** — The non-spec **Heartbeat frames.** edits. Lands the `heartbeat` and `heartbeat_ack` schema, adapter-contract reference, and tier-3 address-test changes in one commit.
      Tiers 0, 3, 11. Depends on: S5
- [ ] **S9 · docs** — DOCS-1a. Lands the API, error-catalog, and SDK-example pages for `POST /resume`, `resume_session`, and `RESUME_FAILED`, and the `QUOTA_EXCEEDED` refusal of a resume at a concurrent-session limit.
      Tiers 0, 11. Depends on: S1, S2, S3, S5, S6
- [ ] **S10 · code** — CODE-2. Lands the retry accounting and the `ValidTransitions` edges with their tests.
      Tiers 0, 1, 2, 3. Depends on: S1
- [ ] **S11 · code** — CODE-3. Lands the post-claim callback and the release after the claim on every binder path, run on a detached context, with their tests.
      Tiers 0, 1. Depends on: S1
- [ ] **S12 · code** — CODE-5. Lands the widened Sweeper adoption, the peer-takeover check against the binding stamp, the `RecordHandoff` compare-and-swap, the pool-resolved readopt binding, the podless-`suspended` lease exclusion with the mirror release, the takeover delivery hook, and the terminal release hook, both unset until S14, with their tests.
      Tiers 0, 1, 2, 8, 11. Depends on: S1, S2, S5
- [ ] **S13 · code** — CODE-1. Lands the active-age column, accrual, and sweeps with their tests.
      Tiers 0, 1, 2. Depends on: S4, S5
- [ ] **S14 · code** — CODE-4. Lands CODE-4 whole, with its tests, and the harness, checker, cells, operations, and pairs that Testing **Expansive lifecycle tests** **Step placement.** places here.
      Tiers 0, 1, 2, 3, 4, 5, 7a, 8. Depends on: S1, S2, S3, S5, S10, S11, S12, S13
- [ ] **S15 · code** — CODE-6. Lands CODE-6 whole, with its tests, and the cells, operations, and pairs that Testing **Expansive lifecycle tests** **Step placement.** places here.
      Tiers 0, 1, 2, 3, 4, 5, 7a, 8, 10, 11. Depends on: S2, S5, S8, S13, S14
- [ ] **S16 · code** — CODE-7. Lands CODE-7 whole, with its tests, and the cells, operations, and pairs that Testing **Expansive lifecycle tests** **Step placement.** places here.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 11. Depends on: S3, S5, S6, S14, S15
- [ ] **S17 · docs** — DOCS-1b. Lands every other reader page whole: the state machines, the session lifecycle, configuration, concepts, multi-tenancy, the observability SLO table, and the runtime-author pages.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S8
- [ ] **S18 · test** — TEST-1. Lands the new tests at tier 2 and above and the tier-0 and tier-11 checks, except the cells, operations, and pairs that Testing **Expansive lifecycle tests** **Step placement.** places in an earlier step.
      Tiers 0, 2, 3, 4, 5, 7a, 8, 9, 10, 11. Depends on: S8, S9, S14, S15, S17
- [ ] **S19 · docs** — RECORDS-1. Lands the finding closures, the new findings, the claim rows, and the remediation-plan tick. It confirms that every `blocked:` skip this proposal's steps added names an OPEN finding and that each such finding has a matching entry in the deviations file.
      Tiers 0, 11. Depends on: S16, S18
