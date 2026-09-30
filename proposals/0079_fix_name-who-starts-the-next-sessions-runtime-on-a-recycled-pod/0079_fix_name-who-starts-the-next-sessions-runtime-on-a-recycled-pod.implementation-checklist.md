# Implementation checklist: Name who starts the next session's runtime on a recycled pod

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, and SPEC-4. The staged `spec/04_system-components.md` edits.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-5, SPEC-6, SPEC-7, and SPEC-8. The staged `spec/05_runtime-registry-and-pool-model.md` edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-9, SPEC-10, SPEC-11, SPEC-12, SPEC-13, SPEC-14, SPEC-15, SPEC-16, and SPEC-17. The staged edits to the other specification files.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · code** — CODE-1, CODE-2, CODE-3, and CODE-4 with TEST-1, TEST-2, TEST-3, TEST-4, TEST-5, and TEST-17. The transport, the terminate frame, the runtime generation, and the pod-scope teardown.
      Tiers 0, 1, 4, 7a. Depends on: S3 and proposal 0078
- [ ] **S5 · code** — CODE-5, CODE-6, and CODE-7 with TEST-6, TEST-7, TEST-8, and TEST-9. The liveness report, the `Decide` branches, and the gateway threading.
      Tiers 0, 1, 3, 4. Depends on: S4
- [ ] **S6 · code** — CODE-8 and CODE-10 with TEST-12, TEST-13, TEST-14, and TEST-18. Tenant pin admission and pinned idle inventory.
      Tiers 0, 1, 2, 9. Depends on: S3
- [ ] **S7 · code** — CODE-9. The comment corrections.
      Tiers 0, 1. Depends on: S5
- [ ] **S8 · test** — FIXTURE-1, TEST-10, TEST-11, and TEST-15. The fixture and the conformance, cluster, and scenario tests.
      Tiers 0, 5, 7a, 10. Depends on: S5 and S6
- [ ] **S9 · docs** — DOC-1, DOC-2, DOC-3, DOC-4, DOC-5, and DOC-6 with TEST-16. The documentation.
      Tiers 0, 11. Depends on: S3 and S7
