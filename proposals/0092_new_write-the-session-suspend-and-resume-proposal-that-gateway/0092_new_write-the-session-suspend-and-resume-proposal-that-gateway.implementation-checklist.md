## Implementation checklist

Every step lands after proposal 0091 is implemented.

- [ ] **S1 · spec** — SPEC-1. Lands the §7.3 resume driver, step 3e, the retry accounting, and the `maxSessionRetries` deletions.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §10.1.1 lease and adoption rule and the §10.1.2 step 2 skip.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the `POST /resume` rows, the `RESUME_FAILED` deletion, and the §29.3 and §29.6 edits.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · spec** — SPEC-4. Lands the §5.2 `maxSessionAgeSeconds` comment.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. Lands idle suspension, the suspension release, the suspended-session lifetime, resume on any message, and the TTL rule.
      Tiers 0, 11. Depends on: S1, S3, S4
- [ ] **S6 · docs** — DOCS-1a. Lands the reader pages for the driver, the lease, and the retry budget.
      Tiers 0, 11. Depends on: S1, S2, S3
- [ ] **S7 · code** — CODE-2. Lands the single retry budget with its tests.
      Tiers 0, 1, 2, 3. Depends on: S1
- [ ] **S8 · code** — CODE-3. Lands the post-claim callback on every binder path with its tests.
      Tiers 0, 1. Depends on: S1
- [ ] **S9 · code** — CODE-5. Lands the widened Sweeper adoption with its tests.
      Tiers 0, 1, 8. Depends on: S2
- [ ] **S10 · code** — CODE-1. Lands the active-age column, accrual, and sweeps with their tests.
      Tiers 0, 1, 4. Depends on: S4
- [ ] **S11 · code** — CODE-4. Lands the resume driver, the store-only `POST /resume`, buffered delivery, and tree-recovery routing with their tests.
      Tiers 0, 1, 3, 4, 7a. Depends on: S1, S2, S3, S7, S8, S9, S10
- [ ] **S12 · code** — CODE-6. Lands the suspension release, idle suspension, the suspended-session lifetime, podless message routing, the flags, and the chart values with their tests.
      Tiers 0, 1, 5, 7a. Depends on: S5, S10, S11
- [ ] **S13 · docs** — DOCS-1b. Lands the reader pages for active age and idle suspension.
      Tiers 0, 11. Depends on: S4, S5
- [ ] **S14 · test** — TEST-1. Lands the tests at tier 2 and above and the tier-0 and tier-11 checks.
      Tiers 0, 2, 3, 4, 5, 7a, 8, 11. Depends on: S6, S11, S12, S13
- [ ] **S15 · docs** — RECORDS-1. Lands the finding closures, the new findings, the claim rows, and the remediation-plan tick.
      Tiers 0, 11. Depends on: S14
