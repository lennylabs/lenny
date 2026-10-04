## Implementation checklist

- [x] **S1 · spec** — SPEC-1. Lands the runtime lifetime contract and its alignments.
      Tiers 0, 11. Depends on: —
- [x] **S2 · spec** — SPEC-2. Lands the manifest-row deletions and the repointed readers.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. Lands the `CH-MSGSOCK` session frames and every edit SPEC-3 stages with them, including the §4.7.1 statement of rule 8's application points (Edit 17).
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the `CH-RUNTIMEOPS` session addressing, the ordering of session-scoped frames after the session's `session_started`, and the `terminate` deletion.
      Tiers 0, 11. Depends on: S1, S3
- [ ] **S5 · spec** — SPEC-5. Lands the §15.7 SDK contract.
      Tiers 0, 11. Depends on: S2, S3
- [ ] **S6 · spec** — SPEC-6. Lands the §15.4.6 conformance categories, including the `session_started` precondition and check.
      Tiers 0, 11. Depends on: S3, S4
- [ ] **S7 · spec** — SPEC-7. Lands the §29 scenario traces.
      Tiers 0, 11. Depends on: S1, S3, S4
- [ ] **S8 · spec** — SPEC-8. Lands the Part B connection handshake.
      Tiers 0, 11. Depends on: S2, S3, S4, S7
- [ ] **S9 · docs** — DOCS-1. Lands the runtime-author documentation and its tier-11 test retargets.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S8
- [ ] **S10 · schema** — SCHEMA-1. Lands both schemas, the fixtures, and the gate lists.
      Tiers 0, 3, 11. Depends on: S2, S3, S4, S8, S9
- [ ] **S11 · code** — CODE-3. Lands the adapter `CH-RUNTIMEOPS` sender changes, the `sessionId`-keyed direct-mode token sink, the deletion of `Terminate`, and their peer and test updates.
      Tiers 0, 1, 3, 4, 7a, 8, 9, 10. Depends on: S3, S4, S6, S10
- [ ] **S12 · code** — CODE-1. Lands the adapter open sequence and `session_end` writes on the rows of the SPEC-3 **Session frame writes.** table, the Attach loop's drop of `session_started`, and the fail-closed `DemoteSDK`.
      Tiers 0, 1, 3, 4, 5, 7a. Depends on: S1, S2, S3, S10
- [ ] **S13 · code** — CODE-2. Lands the developer-loop `session_start`.
      Tiers 0, 1, 3, 7a. Depends on: S3, S10
- [ ] **S14 · code** — CODE-4. Lands the multi-session Go SDK, which writes `session_started`, keys each session's context by `sessionId` and `startId`, and defines the `ExperimentContext` and `LLMConfig` types.
      Tiers 0, 1, 3, 7a, 10. Depends on: S1, S2, S3, S4, S5, S11, S12
- [ ] **S15 · code** — CODE-5. Lands the multi-session Python and TypeScript SDKs, which write `session_started` and key each session's context by `sessionId` and `startId`.
      Tiers 0, 1, 3, 10. Depends on: S1, S2, S3, S4, S5, S11, S12
- [ ] **S16 · code** — CODE-6. Lands the harness, reference-runtime, and `terminate`-handler changes, the harness's `session_started` read bounded by the adapter's default acknowledgement timeout (whose constant it declares in `pkg/runtimekit`) plus a fixed margin, the credential re-read assertion, and the `streaming-echo` and `echo-concurrent` acknowledgements.
      Tiers 0, 1, 3, 4, 5, 10. Depends on: S3, S4, S6, S14, S15
- [ ] **S17 · code** — CODE-8. Lands the removal of the per-session manifest fields.
      Tiers 0, 1, 2, 7a, 8, 9, 10. Depends on: S2, S14, S15, S16
- [ ] **S18 · code** — CODE-9. Lands the adapter's `session_started` wait, its flag wired to the `pkg/runtimekit` default constant, and the gate that writes session-scoped `CH-RUNTIMEOPS` frames only after the read, with the fakes that must answer `session_start`.
      Tiers 0, 1, 3, 4, 5, 7a, 8, 9, 10. Depends on: S3, S4, S11, S12, S14, S15, S16
- [ ] **S19 · test** — TEST-1. Lands the new cross-component tests.
      Tiers 0, 1, 3, 4, 7a, 10. Depends on: S12, S13, S14, S15, S16, S17, S18
- [ ] **S20 · code** — CODE-7. Lands the Part B handshake.
      Tiers 0, 1, 3, 4, 5, 7a, 8, 9, 10. Depends on: S8, S14, S15, S16, S19
- [ ] **S21 · docs** — RECORDS-1. Lands the `BUILD-GAPS.md` notes and new findings.
      Tiers 0, 11. Depends on: S17, S20
