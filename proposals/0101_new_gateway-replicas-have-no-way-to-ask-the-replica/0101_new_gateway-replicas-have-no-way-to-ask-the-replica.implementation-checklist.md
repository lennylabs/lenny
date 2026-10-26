# Implementation checklist — 0101_new_gateway-replicas-have-no-way-to-ask-the-replica

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-6, SPEC-7. Lands their edits to spec/28, spec/10, spec/13, spec/29, spec/11, spec/12, and spec/27 as one commit. Bundled because these deliverables cite labels one another creates in a cycle (SPEC-1 and SPEC-5 cite §10.1.1 **Replica address:** from SPEC-2, SPEC-2 cites §29.3 **Forwarding.** from SPEC-5, SPEC-3 and SPEC-5 cite §10.3 NET-072 from SPEC-7, SPEC-7 cites SPEC-5, and SPEC-4 and SPEC-6 cite each other), and because the tier 0 §28.3-to-§28.8 bijection gate fails when SPEC-1's channel rows land without SPEC-4's matrix rows.
      Tiers 0. Depends on: —
- [ ] **S2 · spec** — SPEC-8. Lands the §18.19 bullet edit.
      Tiers 0. Depends on: S1
- [ ] **S3 · test** — TEST-7. Lands all of TEST-7.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S4 · docs** — REC-1. Lands the claim-register rows at their spec-landing status.
      Tiers 0, 11. Depends on: S1, S3
- [ ] **S5 · schema** — CHART-1. Lands all of CHART-1.
      Tiers 0, 1, 5, 11. Depends on: S1, S3
- [ ] **S6 · code** — CODE-1, and the REC-1 entries that name CODE-1. Lands the holder value, listener, mTLS, credential carriage, and forward helper core.
      Tiers 0, 1, 3, 11. Depends on: S1, S4, S5
- [ ] **S7 · test** — TEST-1. Lands the R18 tests.
      Tiers 0, 1, 2, 3. Depends on: S6
- [ ] **S8 · code** — CODE-2, and the REC-1 entries that name CODE-2. Lands the forward at every site, the DLQ fallback, and the harness forward lane.
      Tiers 0, 1, 2, 4, 7a, 8, 11. Depends on: S1, S6, proposal 0100 CODE-1, CODE-4, and CODE-7 item 4 landed, proposal 0097 CODE-4 landed
- [ ] **S9 · test** — TEST-2. Lands the two-replica route tests.
      Tiers 0, 1, 4. Depends on: S8
- [ ] **S10 · test** — TEST-3, and the REC-1 entry that names TEST-3. Lands the concurrency tests.
      Tiers 0, 7a, 11. Depends on: S8
- [ ] **S11 · code** — CODE-4. Lands the `bindrelease` package rename.
      Tiers 0, 1, 3, 11. Depends on: S1, S3
- [ ] **S12 · code** — CODE-3, and the REC-1 entries that name CODE-3. Lands the drain publish.
      Tiers 0, 1, 4, 7a, 11. Depends on: S1, S4, S11, proposal 0100 CODE-7 item 6 landed
- [ ] **S13 · test** — TEST-4. Lands the drain and rename tests.
      Tiers 0, 1, 4. Depends on: S12
- [ ] **S14 · test** — TEST-5. Lands the chaos cases.
      Tiers 0, 8. Depends on: S8, S12
- [ ] **S15 · test** — TEST-6. Lands the Kind, security, and chart tests.
      Tiers 0, 1, 5, 9. Depends on: S5, S8, S12
- [ ] **S16 · docs** — DOCS-1. Lands the operator security guide edits.
      Tiers 0, 11. Depends on: S1, S3, S5
