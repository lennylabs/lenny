# Implementation checklist — 0110_fix_session-owner-authorization-plan-10-2-session-owner-authoriz

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2. Lands the §10.2 **Session ownership.** paragraph, the `user` bullet, and the matrix cell.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. Lands the §29.3 step 2 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-5. Lands the §9.2 **`respond_to_elicitation` authorization:** edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-6. Lands the `ELICITATION_NOT_FOUND` catalog row.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-4. Lands the amendment to 0108's §15 Open Responses text; applies only after 0108 S1 has landed.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · code** — CODE-1 with its TEST-1 item 11 updates. Lands the principal-bound owner on the sessionserver create paths, the client owner-field deletions, and the existing-caller updates.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9. Depends on: S1
- [ ] **S7 · code** — CODE-2 with its TEST-1 item 11 updates. Lands the access predicate, the wrapper owner check, and the owner-filtered list.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9. Depends on: S1, S3, S4, S6
- [ ] **S8 · code** — CODE-3 with its TEST-1 item 11 updates. Lands the MCP tool and SSE attach ownership checks.
      Tiers 0, 1, 3, 4, 7a, 8, 9. Depends on: S1, S3, S4, S7
- [ ] **S9 · test** — TEST-1 except item 11. Lands the part A tests.
      Tiers 0, 1, 3, 4, 9. Depends on: S6, S7, S8
- [ ] **S10 · code** — CODE-5 with TEST-2 item 7's CODE-5 group. Lands the translator owner binding; applies only after 0108 S8 has landed.
      Tiers 0, 1, 3. Depends on: S1, S6
- [ ] **S11 · code** — CODE-4 with TEST-2 items 5 and 6 and item 7's CODE-4 group. Lands the Open Responses resolve-step check and warm condition 4; applies only after 0108 S7, S8, and S10 have landed.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9. Depends on: S1, S5, S7, S10
- [ ] **S12 · test** — TEST-2 items 1 through 4. Lands the part B tests.
      Tiers 0, 1, 3, 9. Depends on: S10, S11
- [ ] **S13 · docs** — DOCS-1. Lands the security, OpenAI completions, and SDK example edits.
      Tiers 0, 11. Depends on: S1, S6
- [ ] **S14 · docs** — REC-1. Lands the BUILD-GAPS and remediation-plan records.
      Tiers 0. Depends on: S9, S12, S13
