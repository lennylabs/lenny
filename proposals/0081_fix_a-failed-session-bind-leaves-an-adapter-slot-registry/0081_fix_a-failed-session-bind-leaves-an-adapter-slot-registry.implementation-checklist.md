## Implementation checklist

Every spec step leads. Each implementation step lands the tests that the landing table in the
`## Testing` preamble of the non-spec changes file assigns to it, and its Tiers line is the union
of those rows' tiers and the regression tiers that preamble states. Each step follows the test and
claim-register conventions stated there.

One ordering rule governs the code lane beyond the dependency lines below: the gateway learns
to send the new fields before the adapter begins requiring them. S13 mints and carries the
attempt token and sets `unconditional_teardown` at every non-compensating `Shutdown` caller;
S14 and S16 then refuse a request that carries neither. Landing them in the other order
refuses every bind and every teardown in the window between the two steps.

- [ ] **S1 · spec** — SPEC-5. Lands the §4.7.1 bind attempt token block, the pointer sentence on §15.1's `SETUP_COMMAND_FAILED` row, the pointer sentence on §5.2's `**Client error on exhaustion:**` bullet, and SPEC-5's §15.4 edits. It leads because the other spec steps cite the §4.7.1 block; its citations of §5.2 resolve when S4 lands.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-1. Lands SPEC-1's §4.1, §4.7 and §29.4 edits.
      Tiers 0, 11. Depends on: S1.
- [ ] **S3 · spec** — SPEC-2. Lands SPEC-2's §7.1, §7.2, §7.3, §6.2 and §4.7.9 edits. Its citation of §5.2 resolves when S4 lands.
      Tiers 0, 11. Depends on: S1.
- [ ] **S6 · spec** — SPEC-6. Lands SPEC-6's §16.1 metric catalog rows.
      Tiers 0, 11. Depends on: S1.
- [ ] **S4 · spec** — SPEC-3. Lands SPEC-3's §5.2, §4.7 and §12.6 edits. Its tier 11 runs at S7, which carries the SPEC-3 tier-11 sweep.
      Tiers 0. Depends on: S1.
- [ ] **S5 · spec** — SPEC-4. Lands SPEC-4's §6.2 and §4.6.1 edits. Its tier 11 runs at S7, which lands the reader-facing table that gate reconciles against it.
      Tiers 0. Depends on: S1, S4.
- [ ] **S7 · docs** — DOCS-1 and DOCS-2, with the tests the landing table assigns to S7, the SPEC-3 tier-11 sweep among them.
      Tiers 0, 11. Depends on: S1, S2, S4, S5.
- [ ] **S23 · docs** — DOCS-4. Listed beside S7 because DOCS-4 lands beside DOCS-2, after SPEC-3.
      Tiers 0, 11. Depends on: S4.
- [ ] **S8 · code** — CODE-3 in full, with the tests the landing table assigns to S8.
      Tiers 0, 1. Depends on: S4, S5.
- [ ] **S9 · schema** — SCHEMA-1 in full, except its `ABSENT` claim-register row, which lands with S22, and with the tests the landing table assigns to S9. It precedes every step below that opens a `pkg/adapter` handler file, under the summary's proto-window decision.
      Tiers 0, 1, 3, 11. Depends on: S1, S2, S3, S4, S5.
- [ ] **S10 · code** — CODE-9 in full, except its untokened-entry series, which lands with S16, and its `noteCompensationOutcome` forwarder, which lands with S19, with the tests the landing table assigns to S10.
      Tiers 0, 1, 2, 3, 4, 9, 11. Depends on: S1, S6, S9.
- [ ] **S11 · code** — CODE-7 in full, with the tests the landing table assigns to S11.
      Tiers 0, 1, 3. Depends on: S1, S2, S3, S9.
- [ ] **S12 · code** — CODE-8 in full, with the tests the landing table assigns to S12.
      Tiers 0, 1, 2, 3, 4, 9. Depends on: S1, S11.
- [ ] **S13 · code** — CODE-4 in full, including the §7.4 mid-session pair in `pkg/gateway/sessionserver/upload_to_session.go`, with the tests the landing table assigns to S13.
      Tiers 0, 1, 2, 3, 4, 8, 9. Depends on: S1, S2, S9, S11.
- [ ] **S14 · code** — CODE-6 in full, whose hold-release predicates at this step are the ones CODE-14's **What holds at S14.** paragraph states, with the tests the landing table assigns to S14, the literals, calls and asserted codes the S14 rows of **The admission-rule test migration.** under the files-touched list require among them. The mid-session admission guard that the non-spec Design's **The mid-session conditioning.** paragraph names is confirmed before this step lands.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9, 10. Depends on: S1, S4, S9, S11, S12, S13.
- [ ] **S15 · code** — CODE-14, except the `Shutdown` rows of its derivation table and its removing-site table, which land with S16, and with the tests the landing table assigns to S15.
      Tiers 0, 1, 4, 7a, 9. Depends on: S1, S4, S14.
- [ ] **S16 · code** — CODE-1 and CODE-15 in full, CODE-9's untokened-entry series, and the `Shutdown` rows of CODE-14's derivation table and removing-site table, with the tests the landing table assigns to S16, the literals the rule-10 row of **The admission-rule test migration.** under the files-touched list requires among them.
      Tiers 0, 1, 2, 3, 4, 7a, 9, 10, 11. Depends on: S1, S2, S4, S6, S9, S10, S11, S13, S14, S15.
- [ ] **S18 · code** — CODE-2 in full, with the tests the landing table assigns to S18.
      Tiers 0, 1, 2, 3, 4, 7a, 9, 10. Depends on: S1, S3, S14, S16.
- [ ] **S19 · code** — CODE-13 in full and CODE-9's `noteCompensationOutcome` forwarder, with the tests the landing table assigns to S19. These land in one step so no intermediate commit leaves tier 0 red. S20 reads the disposition this step puts on `Binder.Resume`'s error chain.
      Tiers 0, 1, 2, 3, 4, 8, 9. Depends on: S1, S3, S4, S6, S9, S10, S11, S13, S16.
- [ ] **S20 · code** — CODE-5 in full, with the tests the landing table assigns to S20.
      Tiers 0, 1, 2, 3, 4, 9. Depends on: S1, S3, S4, S19.
- [ ] **S22 · code** — CONF-1 in full, with the `ABSENT` claim-register row SCHEMA-1 stages for it and the tests the landing table assigns to S22.
      Tiers 0, 3, 10. Depends on: S1, S4, S14, S15, S16, S18.
- [ ] **S24 · code** — CODE-10. Every hit CODE-10's grep returns is dispositioned under that sub-block's carrier definition and arm rule and the invariants and non-carrier arm of the non-spec `### Comment-carrier reduction: shared invariants` block. Its tests are the rows the landing table assigns to S24.
      Tiers 0, 11. Depends on: S4.
- [ ] **S26 · code** — CODE-12. Every hit CODE-12's command returns is dispositioned under that sub-block's carrier definition and arm rule and the invariants and non-carrier arm of the non-spec `### Comment-carrier reduction: shared invariants` block. Its tests are the rows the landing table assigns to S26.
      Tiers 0, 11. Depends on: S5.
