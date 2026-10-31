# Implementation checklist — 0111_fix_proposal-0077-re-derived-plan-10-2-proposal-0077

## Implementation checklist

External prerequisites: proposal 0112 lands first (summary Decision 22). Plan step R14 (agent-pod mTLS client identity) is a recorded prerequisite for private-store serving to take effect in a deployment and does not block landing 0111 (summary Decision 25).

- [ ] **S1 · spec** — SPEC-1. The §5.1 example edits and the merge-table rule.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §6.4 enumeration edit and the **Repository stores.** and **Private repository stores.** paragraphs.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-12. The §7.1 **Bind bounds.** paragraph and its §6.2 and §15.1 pointers.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-3. The §14 **`gitClone.url` restrictions.** rules, the §14 cache-served-source paragraph, and its edits in §14, §14.1, §26.2, §7.1, §4.7.1, and §10.1.7.
      Tiers 0, 11. Depends on: S2, S3
- [ ] **S5 · spec** — SPEC-4. The §13.4 exception and pointer, and the §7.4 and §2 pointers.
      Tiers 0, 11. Depends on: S2, S4
- [ ] **S6 · spec** — SPEC-5. The §4.7.1 `FetchGitPack` row.
      Tiers 0, 11. Depends on: S2, S4
- [ ] **S7 · spec** — SPEC-6. The §15.1 `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` and `GIT_CLONE_REF_RESOLVE_TRANSIENT` rows and the §6.2 **Client visibility:** pointer.
      Tiers 0, 11. Depends on: S4
- [ ] **S8 · spec** — SPEC-7. The §16.1 metric rows.
      Tiers 0, 11. Depends on: S4, S6
- [ ] **S9 · spec** — SPEC-8. The §4.9 `git-https` provider edits, and the §14, §15.1, §26.2, and §26.3 pointers.
      Tiers 0, 11. Depends on: —
- [ ] **S10 · spec** — SPEC-9. The §4.2 table row and the §5.2 pool-dimensions bullet.
      Tiers 0, 11. Depends on: S2
- [ ] **S11 · spec** — SPEC-10. The §12.4 rows and exception, the §28.3 row, and the §28.5.7 sentence.
      Tiers 0, 11. Depends on: S4, S6
- [ ] **S12 · spec** — SPEC-11. The §17.8.1 rows.
      Tiers 0, 11. Depends on: S2
- [ ] **S13 · test** — INFRA-1. The gating spike and its recorded results.
      Tiers 0, 7a. Depends on: S4
- [ ] **S14 · code** — CODE-1. `Dockerfile.git`, the pipeline entries, and the BUILD-GAPS filing.
      Tiers 0, 1, 3, 5. Depends on: S5, S6
- [ ] **S15 · code** — CODE-2. The `gitRepository` entry through the CRD, `runtimestore`, the flag, and the decoder, the `podIdentityEnforced` predicate, and the url predicate.
      Tiers 0, 1, 2, 3. Depends on: S1, S2, S4
- [ ] **S16 · code** — CODE-7 and TEST-1 items 23 to 26. The `git-https` provider, host binding, `lenny/vcs_token`, and the key parameter on the VCS Secret reader.
      Tiers 0, 1, 3, 4, 9. Depends on: S9
- [ ] **S17 · code** — CODE-8. The pool owner record, its admission, resolution, and the tenant-delete dependent.
      Tiers 0, 1, 2, 4, 9. Depends on: S2, S10, S15
- [ ] **S18 · code** — CODE-9 and TEST-1 item 34. The restore lineage record and its claim-register row.
      Tiers 0, 1, 4. Depends on: S3, S4, S11, S17
- [ ] **S19 · code** — CODE-3. The proto, the git child runner, the mirror, the handler, the stream interceptors, the metrics, and the chart.
      Tiers 0, 1, 3, 4, 7a, 9, 11. Depends on: S2, S3, S4, S5, S6, S7, S8, S9, S11, S12, S13, S14, S15, S16, S17, S18
- [ ] **S20 · code** — CODE-4. The adapter warm fill, its controller-rendered deadline, and the projected SA token on a TLS GatewayControl dial; the token takes effect after plan step R14, and the Kind values stay without `global.saTokenAudience`.
      Tiers 0, 1, 3, 5. Depends on: S2, S6, S12, S15, S19
- [ ] **S21 · code** — CODE-5. Binder routing, the bind deadlines, and slot-repository construction.
      Tiers 0, 1, 3, 4. Depends on: S2, S3, S4, S5, S6, S7, S8, S17, S19, S20
- [ ] **S22 · code** — CODE-6. The capture copy and restore completion.
      Tiers 0, 1, 4. Depends on: S4, S5, S6, S21
- [ ] **S23 · docs** — DOCS-1. The pages that mirror the staged spec edits.
      Tiers 0, 11. Depends on: S1, S2, S3, S4, S6, S7, S8, S9, S10, S12, S19, S20
- [ ] **S24 · docs** — REC-1. The defect filings.
      Tiers 0. Depends on: —
- [ ] **S25 · test** — TEST-1, less the items S16 and S18 land. The case list across the reached tiers, with the private rows at tiers 3 and 4 and the Kind private rows as recorded skips naming R14.
      Tiers 0, 1, 2, 3, 4, 5, 7a, 8, 9, 11. Depends on: S14, S15, S16, S17, S18, S19, S20, S21, S22, S23
