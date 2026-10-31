# Implementation checklist — 0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3. Lands the §4.9 Credential Pool, Credential Storage Topology, Emergency Credential Revocation, and Credential Lifecycle and Secret Management edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-4. Lands the §5 Minimal Configuration example.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-5. Lands the §10.2 matrix row and footnote.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-6. Lands the §13.2.4 Token Service egress and the NET-040 callout.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-7. Lands the §15.1 endpoint and error catalog rows.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · spec** — SPEC-8. Lands the §16.1 metric row and the §16.4 logging bullet.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-9. Lands the §12.8 Phase 4 erasure step and the §12.9 credential pool secrets Store cell.
      Tiers 0, 11. Depends on: S1
- [ ] **S8 · spec** — SPEC-10. Lands the §17.2, §17.6, and §17.7 edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S9 · spec** — SPEC-11. Lands the §24 thin-client exception and the §24.5 table and footnote.
      Tiers 0, 11. Depends on: S1, S3
- [ ] **S10 · spec** — SPEC-12. Lands the §25 action tool rows.
      Tiers 0, 11. Depends on: S1
- [ ] **S11 · spec** — SPEC-13. Lands the §28.7 `lenny-tokenservice.proto` register row.
      Tiers 0, 11. Depends on: S1
- [ ] **S12 · spec** — SPEC-14. Lands the §18.38 uninstall residual item.
      Tiers 0, 11. Depends on: S1
- [ ] **S13 · code** — CODE-1. Lands the ref parser and the Lenny-written name helpers.
      Tiers 0, 1. Depends on: S1
- [ ] **S14 · code** — CODE-2. Lands the proto change and the Token Service access, metric, and redaction seam.
      Tiers 0, 1, 2, 3, 9. Depends on: S1, S6, S11
- [ ] **S15 · code** — CODE-3. Lands canonical refs, the registration and prefix checks, and the single 422 key in the admin handlers and bootstrap.
      Tiers 0, 1, 3, 4, 9. Depends on: S1, S3, S5, S13, S14
- [ ] **S16 · code** — CODE-4. Lands the `secretData` write path, the Lenny-written Secret operations, and the re-enable role.
      Tiers 0, 1, 2, 3, 4, 7a, 8, 9. Depends on: S1, S5, S9, S15
- [ ] **S17 · code** — CODE-5. Lands the tenant-erasure Secret deletion.
      Tiers 0, 1, 4, 8, 9. Depends on: S1, S7, S13, S16
- [ ] **S18 · code** — CODE-6. Lands the Token-Service-backed VCS reader.
      Tiers 0, 1, 3, 9. Depends on: S1, S5, S13, S14, S15
- [ ] **S19 · code** — CODE-7. Lands the preflight warning text.
      Tiers 0, 1. Depends on: S1, S8
- [ ] **S20 · code** — CODE-8. Lands the `lenny-ctl` unreferenced-Secret listing.
      Tiers 0, 1. Depends on: S1, S9, S13
- [ ] **S21 · code** — CHART-1. Lands the credentials namespace templates, the egress rule, the flags, and their chart tests.
      Tiers 0, 1, 5, 9, 11. Depends on: S1, S4, S8, S14, S15, S16
- [ ] **S22 · test** — TEST-1. Lands the tier 3, tier 5, and tier 9 cross-tier tests.
      Tiers 3, 5, 9. Depends on: S14, S15, S16, S17, S18, S20, S21
- [ ] **S23 · docs** — DOCS-1. Lands the operator, API, metrics, and runbook docs.
      Tiers 0, 11. Depends on: S1, S5, S6, S8, S9, S10
- [ ] **S24 · docs** — REC-1. Lands the BUILD-GAPS records.
      Tiers 0. Depends on: S22, S23
