# Implementation checklist — 0102_fix_ending-a-platform-admin-impersonation-session-revokes-nothin

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §13.3 **Platform-admin impersonation.** paragraph and the tenant-scope pointer.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §10.2 **Signing topology.** citation.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §12.2 `TokenIssuanceStore` row edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands the §16.7 started-payload and ended-trigger edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. Lands the §11.4 step 5 and Note edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · spec** — SPEC-6. Lands the three §15.1 rows.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · code** — CODE-1. Lands migration 0190 and its tests.
      Tiers 0, 1, 2. Depends on: S3
- [ ] **S8 · code** — CODE-2. Lands the impersonation-scoped store queries.
      Tiers 0, 1, 2. Depends on: S1, S3, S7
- [ ] **S9 · code** — CODE-3. Lands the service over `issued_tokens`, the deletions, and the wiring.
      Tiers 0, 1, 2, 4. Depends on: S1, S4, S6, S8
- [ ] **S10 · code** — CODE-4. Lands the handler changes and the OpenAPI edit.
      Tiers 0, 1, 3. Depends on: S6, S9
- [ ] **S11 · code** — CODE-5. Lands the full-revoke cascade and partial-result handling.
      Tiers 0, 1, 4, 9. Depends on: S1, S5, S8
- [ ] **S12 · test** — TEST-1. Lands the unit and Postgres-tier tests.
      Tiers 0, 1, 2. Depends on: S8, S9, S10, S11
- [ ] **S13 · test** — TEST-2. Lands the two-replica and `lenny-ops` tests.
      Tiers 0, 4, 9. Depends on: S9, S10, S11
- [ ] **S14 · docs** — DOCS-1. Lands the operator guide, admin API reference, and BUILD-GAPS closure.
      Tiers 0, 11. Depends on: S6, S12, S13
