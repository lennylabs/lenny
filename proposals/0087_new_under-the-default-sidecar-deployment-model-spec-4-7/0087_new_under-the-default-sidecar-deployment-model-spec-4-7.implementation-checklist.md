## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the §5.1 `command` field text.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the §4.7.10 edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the §4.7.9 step 4 and step 7 edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. Lands §4.7.11 item 8.
      Tiers 0, 11. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. Lands the §5.2 edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S6 · spec** — SPEC-6. Lands the §13.1 paragraph.
      Tiers 0, 11. Depends on: S2
- [ ] **S7 · code** — CODE-1 with TEST-1(a). Lands the CRD field and the registration refusals. It precedes S8 only because S8 is held until S9 (see S8), and CODE-1 implements no statement S8 stages.
      Tiers 0, 1, 2. Depends on: S1
- [ ] **S8 · spec** — SPEC-7. Lands the `CH-SUPERVISE` register row, card, §28.6 paragraph, and §28.8 row. It is placed immediately before S9 because §28.4 requires a claim-register row for the card, and that row can only be a `WIRED` row naming surfaces S9 creates: no remediation-plan step exists for an `ABSENT` deferral.
      Tiers 0, 11. Depends on: S2, S3, S4, S5
- [ ] **S9 · code** — CODE-2 and CODE-3 with TEST-1(b). Lands both ends of `CH-SUPERVISE`, which share `pkg/supervise/wire.go`, together with their claim-register rows.
      Tiers 0, 1, 3, 7a. Depends on: S2, S3, S4, S5, S8
- [ ] **S10 · code** — CODE-4 with TEST-1(c). Lands the supervised pod rendering and the render-time refusals.
      Tiers 0, 1, 2. Depends on: S1, S2, S6, S7, S9
- [ ] **S11 · code** — CODE-5. Lands the comment and admission-text scoping.
      Tiers 0, 1. Depends on: S2, S5
- [ ] **S12 · test** — FIXTURE-1 with TEST-1(d). Lands the probe runtime, the supervised Kind pool, and the cluster-tier tests.
      Tiers 0, 4, 5, 6, 8, 9. Depends on: S9, S10
- [ ] **S13 · docs** — DOC-1 with TEST-1(e). Lands the documentation and the tier-11 checks.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S5, S6, S8, S12
