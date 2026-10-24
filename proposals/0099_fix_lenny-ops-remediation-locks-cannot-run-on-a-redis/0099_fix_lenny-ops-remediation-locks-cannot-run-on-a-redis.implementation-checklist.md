# Implementation checklist — 0099_fix_lenny-ops-remediation-locks-cannot-run-on-a-redis

## Implementation checklist

- [ ] **S1 · spec** — SPEC-3. Lands the §12.4 brace paragraph.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-1. Lands the §25.4 Storage Tiers table row, the Tier 2 paragraph, and the Error Codes `REMEDIATION_LOCK_CONFLICT` row.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-2. Lands the §25.4 epoch key, Clock Source, and Storage Routing edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. (Part 2) Lands the §12.4 shared-topology paragraph and the renewal row.
      Tiers 0, 11. Depends on: S1
- [ ] **S5 · spec** — SPEC-5. (Part 2) Lands the §12.4 migration pre-plan edits.
      Tiers 0, 11. Depends on: S4
- [ ] **S6 · spec** — SPEC-6. (Part 2) Lands the §17.8.2 and §16.5 edits.
      Tiers 0, 11. Depends on: S4
- [ ] **S7 · code** — CODE-1 and TEST-1. Lands the redisstore rewrite, the two cmd/lenny-ops comments, and the tier-1 store tests.
      Tiers 0, 1. Depends on: S1, S2, S3
- [ ] **S9 · test** — TEST-2. Lands the tier-4 cluster test and the tier-2 slot guard.
      Tiers 0, 2, 4. Depends on: S7
- [ ] **S10 · test** — TEST-3. Lands the tier-0 exemption removal and the tier-11 check (e) edits.
      Tiers 0, 11. Depends on: S1, S2, S3, S7
- [ ] **S11 · docs** — REC-1. Lands the F-12.4.28 closure and the P6 progress line.
      Tiers 0, 11. Depends on: S7, S9, S10
- [ ] **S12 · docs** — REC-2. (Part 2) Lands the condition audit, its findings, and the P6 closure.
      Tiers 0, 11. Depends on: S4, S5, S6, S11
- [ ] **S13 · docs** — DOCS-1. Lands the ops-clock-skew runbook clock-source sentence.
      Tiers 0, 11. Depends on: S3
- [ ] **S14 · chart** — CHART-1. Lands the two `charts/lenny/values.yaml` Redis comment corrections.
      Tiers 0, 11. Depends on: S5, S6
