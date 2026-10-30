# Implementation checklist — 0111_fix_proposal-0077-re-derived-plan-10-2-proposal-0077

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §5.1 example edits and the merge-table rule.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §6.4 enumeration edit and the **Repository stores.** paragraph.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. The §14 cache-served-source paragraph and its wording edits in §14, §14.1, and §26.2.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-4. The §13.4 exception and pointer, and the §7.4 and §2 pointers.
      Tiers 0, 11. Depends on: —
- [ ] **S5 · spec** — SPEC-5. The §4.7.1 `FetchGitPack` row.
      Tiers 0, 11. Depends on: —
- [ ] **S6 · spec** — SPEC-6. The §15.1 `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` row and the §6.2 **Client visibility:** pointer.
      Tiers 0, 11. Depends on: —
- [ ] **S7 · spec** — SPEC-7. The §16.1 metric rows.
      Tiers 0, 11. Depends on: —
- [ ] **S8 · test** — INFRA-1. The gating spike and its recorded results.
      Tiers 7a. Depends on: S3
- [ ] **S9 · code** — CODE-1. `Dockerfile.git`, the pipeline entries, and the BUILD-GAPS filing.
      Tiers 0, 5. Depends on: S4, S5
- [ ] **S10 · code** — CODE-2. The `gitRepository` entry through the CRD, `runtimestore`, the flag, and the decoders.
      Tiers 0, 1, 2, 3. Depends on: S1, S2
- [ ] **S11 · code** — CODE-3. The proto, the mirror, the handler, the stream interceptors, the metrics, and the chart volume.
      Tiers 0, 1, 3, 4, 7a, 9. Depends on: S2, S3, S4, S5, S6, S7, S8, S9, S10
- [ ] **S12 · code** — CODE-4. The adapter warm fill.
      Tiers 0, 1, 3, 5. Depends on: S2, S5, S10, S11
- [ ] **S13 · code** — CODE-5. Binder routing and slot-repository construction.
      Tiers 0, 1, 3, 4. Depends on: S3, S4, S5, S6, S7, S11, S12
- [ ] **S14 · code** — CODE-6. The capture copy and restore completion.
      Tiers 0, 1, 4. Depends on: S3, S4, S5, S13
- [ ] **S15 · test** — TEST-1. The case list across the reached tiers.
      Tiers 0, 1, 2, 3, 4, 5, 7a, 9. Depends on: S10, S11, S12, S13, S14
- [ ] **S16 · docs** — REC-1. The defect filings.
      Tiers 0. Depends on: —
