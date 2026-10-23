## Implementation checklist

Re-baselined 2026-10-22. The spec steps land after proposal 0090's (landed) and before proposal 0084's. Until proposal 0084 lands, pool admission refuses `recycle.runtimeProcess: restart` through a code-only gate in S17 (D-RESTARTGATE), and the cases above tier 1 that need an admitted `restart` pool are skipped through `tests/registers/skip-reasons.yaml`; proposal 0084 removes the gate and the skip entries. CODE-4 serializes with R17 under rule S-4.

Precondition: the precursor direct fix on branch `fix/warmpool-reclaim-terminated-adapter` (summary D-PREREQS) has landed before S1 starts. No step of this checklist changes `observePod`, `Decide`, or `DecideSDKWarm`.

- [ ] **S1 · spec** — SPEC-1. Lands the `command` and `env` Runtime fields and their sites.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §4.7.10 **Runtime supervisor.** paragraph and its citing sites.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §4.7.9 startup-sequence edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. Lands §4.7.11 item 8.
      Tiers 0, 11. Depends on: S2
- [ ] **S5 · spec** — SPEC-11. Lands the launch-ordering wording.
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-5. Lands `recycle.runtimeProcess` and its sites.
      Tiers 0, 11. Depends on: S2
- [ ] **S7 · spec** — SPEC-6. Lands §13.1 **Runtime supervisor volume.**
      Tiers 0, 11. Depends on: S2
- [ ] **S8 · spec** — SPEC-9. Lands the SDK-warm and pre-warming sites.
      Tiers 0, 11. Depends on: S2
- [ ] **S9 · spec** — SPEC-7. Lands `CH-SUPERVISE` and its citing sites.
      Tiers 0, 11. Depends on: S2, S3, S4, S5, S6
- [ ] **S10 · spec** — SPEC-8. Lands the `shutdown`, signal, and exit-code statements.
      Tiers 0, 11. Depends on: S9
- [ ] **S11 · spec** — SPEC-12. Lands the per-process connection wording and the `CH-SUPERVISE` launch step.
      Tiers 0, 11. Depends on: S9
- [ ] **S12 · spec** — SPEC-13. Lands the §24.2 `lenny-ctl preflight supervisor` row.
      Tiers 0, 11. Depends on: S4
- [ ] **S13 · spec** — SPEC-14. Lands the §18 build-sequence entries.
      Tiers 0, 11. Depends on: S6, S7, S9, S12
- [ ] **S6b · test** — TEST-2. Lands the updated tier-11 pin.
      Tiers 0, 11. Depends on: S6
- [ ] **S14 · code** — CODE-1 with its tier-1, tier-2, and tier-3 tests.
      Tiers 0, 1, 2, 3. Depends on: S1, S8
- [ ] **S15 · code** — CODE-2 with its C unit, fuzz, and binary-check tests.
      Tiers 0, 1. Depends on: S2, S3, S4, S5, S6, S9, S10
- [ ] **S16 · code** — CODE-3 with its tier-1, tier-3, and tier-7a tests and its claim rows.
      Tiers 0, 1, 3, 7a. Depends on: S2, S3, S4, S5, S6, S8, S9, S10, S11, S15
- [ ] **S17 · code** — CODE-5 with its tier-1, tier-2, tier-3, and tier-4 tests, including the interim refusal of `restart` until proposal 0084 lands, its comment block, its tier-1 test, and the skip-register entries for the tier-4 `restart` cases.
      Tiers 0, 1, 2, 3, 4. Depends on: S6, S14
- [ ] **S18 · code** — CODE-4 with its tier-1 and tier-2 tests, and the rendered-argv start of TEST-1's tier-3 supervisor test. Serializes with R17 under S-4.
      Tiers 0, 1, 2, 3. Depends on: S1, S2, S6, S7, S8, S14, S15, S16, S17
- [ ] **S19 · code** — CODE-6.
      Tiers 0, 1. Depends on: S4, S12, S15, S18
- [ ] **S20 · test** — FIXTURE-1, and TEST-1's tier-5, tier-6, tier-8, tier-9, and tier-10 tests, with the skip-register entries for the cases that need an admitted `restart` pool.
      Tiers 0, 5, 6, 8, 9, 10. Depends on: S16, S17, S18, S19
- [ ] **S21 · docs** — DOC-1 with the tier-11 checks.
      Tiers 0, 11. Depends on: S1 to S13, S20
