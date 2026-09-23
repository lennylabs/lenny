## Implementation checklist

Every spec step leads. Each implementation step carries the tests for the tiers its line
names, and follows the test and claim-register conventions stated in the `## Testing` preamble
of the non-spec changes file.

One ordering rule governs the code lane beyond the dependency lines below: the gateway learns
to send the new fields before the adapter begins requiring them. S13 mints and carries the
attempt token and sets `unconditional_teardown` at every non-compensating `Shutdown` caller;
S14 and S16 then refuse a request that carries neither. Landing them in the other order
refuses every bind and every teardown in the window between the two steps.

- [ ] **S1 · spec** — SPEC-5. Lands the §4.7.1 bind attempt token block after the RPC tables, the two §15.4 blocks after the SDK-warm demotion contract, the §15.1 `SETUP_COMMAND_FAILED` row replacements, and the §6.2 pre-attached `**Client visibility:**` pointer. §15.1 takes no new catalog row for either adapter error code. SPEC-5 leads because the §4.7 `Shutdown` row, §5.2's reclaim-hold paragraph and §7.1's obligation each point at the §4.7.1 block. Three staged citations forward-reference S4: the §4.7.1 block's own pointers at §5.2's reclaim hold and disposition table, §7.1's citation of that table, and §15.4's reclaim-hold block. Each resolves when S4 lands, and the applied spec is correct once every spec step has landed.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-1. Lands the §4.1 `ShutdownRequest` sentence replacement, the §4.7 Gateway → Adapter `Shutdown` and `DemoteSDK` row replacements, and the §29.4 session-end step 13 sentence.
      Tiers 0, 11. Depends on: S1.
- [ ] **S3 · spec** — SPEC-2. Lands the §7.1 reclaim-obligation paragraph after the atomicity paragraph, the §7.2 mid-resume snapshot-close edits, the §7.3 resume-flow sentence, the §6.2 `resuming` mid-resume cancel clause, and the §4.7.9 step 5 sentence. Its citation of §5.2's disposition table resolves when S4 lands. §5.2's slot-retry `**Max retries:**` bullet is left as it stands.
      Tiers 0, 11. Depends on: S1.
- [ ] **S4 · spec** — SPEC-3. Lands the §5.2 `**Scrub model.**` edits with the disposition table and the `**Slot-identifier reclaim hold.**` paragraph, the §5.2 `**Slot cleanup:**` and `**Whole-pod replacement trigger:**` bullet edits, the §4.7 Adapter → Gateway `ReportSessionScrub` row, and the §12.6 `sessions_served` prose and DDL comment. Tier 11 is deferred to S7, which carries the SPEC-3 tier-11 sweep.
      Tiers 0. Depends on: S1.
- [ ] **S5 · spec** — SPEC-4. Lands the §6.2 per-slot sub-state fence edits and the pre-`running` cleanup paragraph after the fence, the §4.6.1 claim-deletion bullets with their input enumeration and the deleted closing sentence, and the §6.2 `claimed ──→ draining` fence trigger and projection-prose pointers. Tier 11 is deferred to S7 and tier 1 to S8, because the reader-facing table and the transcribed edge list are the two artifacts those gates reconcile against this block.
      Tiers 0. Depends on: S1, S4.
- [ ] **S6 · spec** — SPEC-6. Lands the §16.1 metric catalog rows for the counters S10 emits and for the shipped `lenny_adapter_leaked_slots` gauge.
      Tiers 0, 11. Depends on: S1.
- [ ] **S7 · docs** — DOCS-1, DOCS-2, DOCS-3, with the tier-11 cases the non-spec `### Documentation reconciliation tests, tier 11` block states for SPEC-3, SPEC-4, DOCS-1, DOCS-2 and DOCS-3.
      Tiers 0, 11. Depends on: S1, S2, S4, S5.
- [ ] **S8 · code** — CODE-3. `ValidTransitions()` and its doc comment in `pkg/sandbox/slotstate` gain the new edge, the retired glosses there are re-keyed onto §6.2 and §5.2 pointers, the retired cleanup-timeout trigger is deleted from the doc comments that carry it in `pkg/sandbox/slotstate`, `pkg/gateway/runtime/slothealth`, `pkg/gateway/sessionserver` and `pkg/gateway/metrics/gatewaymetrics`, each of which keeps its existing §6.2 citation, and `TestValidTransitions_spec_6_2` moves to the seven-edge set with a positive and a negative `IsValid` assertion.
      Tiers 0, 1. Depends on: S4, S5.
- [ ] **S9 · schema** — SCHEMA-1 in full, except its `ABSENT` claim-register row, which lands with S22. It precedes every step below that opens a `pkg/adapter` handler file, under the summary's proto-window decision.
      Tiers 0, 1, 3, 11. Depends on: S1, S2, S3, S4, S5.
- [ ] **S10 · code** — CODE-9, including `tests/tier11_docs/slot_compensation_metric_reference_test.go` with its `slotAddressCaseFiles` row and its `tests/spec-map.json` §16.1 credit. The assertions that each counter fires land with S16 and S19.
      Tiers 0, 1, 11. Depends on: S1, S6, S9.
- [ ] **S11 · code** — CODE-7. `pkg/gateway/runtime/adapterclient` gains a translation from the gRPC status detail to two sentinel errors matched with `errors.Is`, following the `RunSetup` detail reader.
      Tiers 0, 1, 3. Depends on: S1, S9.
- [ ] **S12 · code** — CODE-8 in full.
      Tiers 0, 1, 2. Depends on: S1, S11.
- [ ] **S13 · code** — CODE-4 in full, including the §7.4 mid-session pair in `pkg/gateway/sessionserver/upload_to_session.go`.
      Tiers 0, 1, 2, 3, 4. Depends on: S1, S2, S9, S11.
- [ ] **S14 · code** — CODE-6 in full, including the bind-field literal sweep that the files-touched list defines by grep. The mid-session admission guard that the non-spec Design's **The mid-session conditioning.** paragraph names is confirmed before this step lands.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9, 10. Depends on: S1, S4, S9, S11, S12, S13.
- [ ] **S15 · code** — CODE-14, except its derivation table's `Shutdown` row, the `Shutdown` row of its tier-1 expired-acquisition case, and the tier-7a cases that drive a removing `Shutdown`, which land with S16.
      Tiers 0, 1, 7a. Depends on: S1, S4, S14.
- [ ] **S16 · code** — CODE-1 and CODE-15 in full, including CODE-1's `ShutdownRequest` literal sweep, the tier-1 `exited_cleanly` cases that drive `Server.removeSlotTreeFn`, and the registered-but-unbound class of the tier-9 matching-reclaim arm under Testing **Tier 9, the credential fence.**; CODE-14's `Shutdown` row, the `Shutdown` row of its tier-1 expired-acquisition case and its tier-7a cases that drive a removing `Shutdown`; and the Testing subsection **Tier-1 adoption-ordering tests, every case walked**.
      Tiers 0, 1, 2, 3, 4, 7a, 9, 10. Depends on: S1, S2, S4, S9, S10, S11, S13, S14, S15.
- [ ] **S18 · code** — CODE-2 in full, including its re-fixturing of the shipped test callers of `noteRuntimeStarted`.
      Tiers 0, 1, 7a. Depends on: S1, S3, S14, S16.
- [ ] **S19 · code** — CODE-13, all of it: the compensating `Shutdown`, the leaked disposition, `ReleaseSlotReservation`'s `leaked` parameter at every call site in CODE-13's table, and the attempt-scoped lease release together with the `CredentialAssigner` widening, every `releaseAttemptCredentials` call site and the fake sweep the files-touched list defines by grep. These land in one step so no intermediate commit leaves tier 0 red. S20 reads the disposition this step puts on `Binder.Resume`'s error chain.
      Tiers 0, 1, 2, 4, 8, 9. Depends on: S1, S3, S4, S9, S10, S11, S13, S16.
- [ ] **S20 · code** — CODE-5 in full.
      Tiers 0, 1, 2, 4. Depends on: S1, S3, S4, S19.
- [ ] **S22 · code** — CONF-1 in full, with the `ABSENT` claim-register row SCHEMA-1 stages for it.
      Tiers 0, 3, 10. Depends on: S1, S4, S14, S15, S16, S18.
- [ ] **S23 · docs** — DOCS-4. It carries the step id S23 rather than a position beside S7 because renumbering the earlier steps would invalidate every dependency line.
      Tiers 0, 11. Depends on: S4.
- [ ] **S24 · code** — CODE-10. Every hit CODE-10's grep returns is dispositioned under that sub-block's carrier definition and arm rule and the invariants and non-carrier arm of the non-spec `### Comment-carrier reduction: shared invariants` block.
      Tiers 0, 11. Depends on: S4.
- [ ] **S25 · code** — CODE-11. The four sites CODE-11 names take the reduction its arm rule states, under its IMPLEMENTOR'S CHOICE constraint and the invariants of the non-spec `### Comment-carrier reduction: shared invariants` block.
      Tiers 0. Depends on: S1.
- [ ] **S26 · code** — CODE-12. Every hit CODE-12's command returns is dispositioned under that sub-block's carrier definition and arm rule and the invariants and non-carrier arm of the non-spec `### Comment-carrier reduction: shared invariants` block.
      Tiers 0, 11. Depends on: S5.
