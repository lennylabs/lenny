## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the mid-session failure-outcome reconciliation, all of SPEC-1.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2, SPEC-3. Lands the `CH-ATTACH` stream rules and their pointers, all of SPEC-2, with the §28.8 rows of SPEC-3 in the same edit, as SPEC-3 requires.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-4. Lands the concurrent-pool failure routing and the retire-on-failure scoping, all of SPEC-4.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · docs** — DOCS-1. Lands the reader-doc edits that follow SPEC-1, SPEC-2, and SPEC-4.
      Tiers 0, 11. Depends on: S1, S2, S3
- [ ] **S5 · code** — CODE-1. Lands the session-scoped stream, the reader, the turn token, cancel-based eviction, and eviction on end.
      Tiers 0, 1. Depends on: S2
- [ ] **S6 · test** — TEST-1. Lands the step-1 tests.
      Tiers 0, 1, 2, 4, 5, 7a. Depends on: S5
- [ ] **S7 · code** — CODE-2. Lands the release funnel, the slot accounting, the per-session slot-accounting lock, the release before a resume rebinds, and the hold of a resume whose bind meets another replica's lease, with their tests.
      Tiers 0, 1, 2, 7a. Depends on: S1, S2, S3, S6
- [ ] **S8 · code** — CODE-3. Lands the injected stream-failure handler, the `BindResult.CoordinationGeneration` stamp at every binding publish site, the coordination check that suppresses a stale replica's report and evicts its binding without a drain, the `runtime_crash` report on a bounded detached context, the report-timeout flag, and the path-6 delivery fallback, with their tests.
      Tiers 0, 1, 7a. Depends on: S1, S2, S7
- [ ] **S9 · test** — TEST-2. Lands the step-2 tests.
      Tiers 0, 4, 5, 7a, 8, 11. Depends on: S4, S8
- [ ] **S10 · code** — CODE-5. Lands the eviction of a bound session's binding and stream after a peer takes the session over, with its tests.
      Tiers 0, 1, 8. Depends on: S2, S5
- [ ] **S11 · docs** — RECORDS-1. Lands the claim rows, the finding closures and new findings, and the remediation-plan tick.
      Tiers 0, 11. Depends on: S6, S9, S10
