## Implementation checklist

Re-baselined 2026-10-22. The spec steps land after proposal 0090's (landed) and before proposal 0084's. CODE-4 serializes with R17 under rule S-4.

- [ ] **S1 · spec** — SPEC-1. Lands `command` and `env` in §5.1 and the `command` line of the §17.4 sidecar Runtime example.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §4.7.10 **Runtime supervisor.** paragraph, the lifetime scoping, the trade-off row, and the §4.7.1 `Shutdown` row.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3, SPEC-4, and SPEC-11. Lands §4.7.9 steps 4 and 7, §4.7.11 item 8, and the launch-ordering wording.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-5. Lands `recycle.runtimeProcess` with the `keep` pod and `restart` pod terms, the acknowledgment rule, the supervised `cleanupCommands`, and the scrub edits in §5.2, and SPEC-5's edits in §5.1, §4.6.1, §4.6.3, §6.2, §13.2, §16.5, §17.2, and §29.2 that the summary's deliverable index lists.
      Tiers 0, 11. Depends on: S2
- [ ] **S5 · spec** — SPEC-6 and SPEC-9. Lands §13.1 **Runtime supervisor volume.**, the §6.1 SDK-warm text, the §5.1 `capabilities.preConnect` sentence, the §6.1 `preConnect` compatibility row (SPEC-9(c)), and §6.2 **preConnect re-warm on scrub_warning.** (SPEC-9(d)).
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-7 and SPEC-8. Lands `CH-SUPERVISE` in §28 and the `shutdown` and exit-code corrections. Placed before the code that lands its `WIRED` claim rows.
      Tiers 0, 11. Depends on: S2, S3, S4
- [ ] **S7 · code** — CODE-1 with its tier-1 and tier-2 tests, and FIXTURE-1's `command` and any needed `env` on every existing sidecar fixture and bootstrap runtime.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S8 · code** — CODE-2 with its C unit, fuzz, and binary-check tests.
      Tiers 0, 1. Depends on: S2, S3, S6
- [ ] **S9 · code** — CODE-3 with its tier-1, tier-3, and tier-7a tests and its claim rows.
      Tiers 0, 1, 3, 7a. Depends on: S4, S6, S8
- [ ] **S10 · code** — CODE-5 with its tier-1, tier-2, and tier-4 tests.
      Tiers 0, 1, 2, 4. Depends on: S4, S7
- [ ] **S11 · code** — CODE-4 with its tier-1 and tier-2 tests. Serializes with R17 under S-4.
      Tiers 0, 1, 2. Depends on: S5, S7, S8, S9, S10
- [ ] **S12 · code** — CODE-6.
      Tiers 0, 1. Depends on: S8
- [ ] **S13 · test** — FIXTURE-1's probe runtime and pools, and the tier-5, tier-6, tier-8, tier-9, and tier-10 tests.
      Tiers 0, 5, 6, 8, 9, 10. Depends on: S9, S10, S11
- [ ] **S14 · docs** — DOC-1 with the tier-11 checks.
      Tiers 0, 11. Depends on: S1 to S6, S13
