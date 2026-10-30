# Implementation checklist — 0085_fix_the-6-2-pre-attached-failure-retry-loop-has

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands both §6.2 **Pre-attached failure retry policy:** edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §15.1 `SETUP_COMMAND_FAILED` row edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands every SPEC-3 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · docs** — RECORDS-1. Lands the new BUILD-GAPS finding and the T-6.2.10 retarget.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · code** — CODE-1. Lands the comment and annotation sweep.
      Tiers 0, 1. Depends on: S1, S2, S4
- [ ] **S6 · test** — TEST-1. Lands the SandboxClaim create counter in the finalize failure test.
      Tiers 0, 2. Depends on: S1
