## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §4.7.10 lifetime contract and its §4.7.9, §4.7.1, and §15.4.2 alignments.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the manifest-row deletions and the repointed readers.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. Lands the `CH-MSGSOCK` frame definitions and their §15.4 and §15.4.3 edits.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the `CH-RUNTIMEOPS` card edits and the §15.7 **Graceful shutdown.** edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. Lands the §15.7 SDK contract edits.
      Tiers 0, 11. Depends on: S3
- [ ] **S6 · spec** — SPEC-6. Lands the §15.4.6 category rows.
      Tiers 0, 11. Depends on: S3, S4
- [ ] **S7 · spec** — SPEC-7. Lands the §29 trace edits.
      Tiers 0, 11. Depends on: S1, S3
- [ ] **S8 · spec** — SPEC-8. Lands the Part B handshake text.
      Tiers 0, 11. Depends on: S3, S4
- [ ] **S9 · docs** — DOCS-1. Lands the runtime-author documentation and its tier-11 test retargets.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S8
- [ ] **S10 · schema** — SCHEMA-1. Lands both schemas, the fixtures, and the gate lists.
      Tiers 0, 3, 11. Depends on: S3, S4, S9
- [ ] **S11 · code** — CODE-3. Lands the adapter `CH-RUNTIMEOPS` sender changes and their peer and test updates.
      Tiers 0, 1, 3, 8, 9, 10. Depends on: S4, S10
- [ ] **S12 · code** — CODE-1. Lands the adapter session-frame helpers at every start and end site.
      Tiers 0, 1, 3, 4. Depends on: S1, S3, S10
- [ ] **S13 · code** — CODE-2. Lands the developer-loop `session_start`.
      Tiers 0, 1, 3. Depends on: S3, S10
- [ ] **S14 · code** — CODE-4. Lands the multi-session Go SDK.
      Tiers 0, 1, 3, 7a, 10. Depends on: S5, S11, S12
- [ ] **S15 · code** — CODE-5. Lands the multi-session Python and TypeScript SDKs.
      Tiers 0, 1, 3, 10. Depends on: S5, S11, S12
- [ ] **S16 · code** — CODE-6. Lands the harness, reference-runtime, and `terminate`-handler changes.
      Tiers 0, 1, 3, 10. Depends on: S6, S14, S15
- [ ] **S17 · code** — CODE-8. Lands the removal of the per-session manifest fields.
      Tiers 0, 1, 2, 7a, 8, 9, 10. Depends on: S2, S14, S15, S16
- [ ] **S18 · test** — TEST-1. Lands the new cross-component tests.
      Tiers 0, 1, 3, 4, 7a, 10. Depends on: S12, S13, S14, S15, S16, S17
- [ ] **S19 · code** — CODE-7. Lands the Part B handshake in the adapter and every in-tree client; it also waits on proposal 0084's nonce-pinning step.
      Tiers 0, 1, 3, 4, 9, 10. Depends on: S8, S14, S15, S16, S18
- [ ] **S20 · docs** — RECORDS-1. Lands the `BUILD-GAPS.md` notes and new findings.
      Tiers 0, 11. Depends on: S17, S19
