## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands both §6.2 **Pre-attached failure retry policy:** edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §15.1 `SETUP_COMMAND_FAILED` row edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §4.7 `ConfigureWorkspace` row and §29.2 step 23 edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · docs** — RECORDS-1. Lands the new BUILD-GAPS finding and the T-6.2.10 retarget.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · code** — CODE-1. Lands the comment and annotation sweep. Lands after 0082's code steps (see the summary's **Watch out for.**).
      Tiers 0, 1. Depends on: S1, S2, S4
- [ ] **S6 · test** — TEST-1. Lands the SandboxClaim create counter in the finalize failure test. Lands after 0082's code steps (see the summary's **Watch out for.**).
      Tiers 0, 2. Depends on: S1
