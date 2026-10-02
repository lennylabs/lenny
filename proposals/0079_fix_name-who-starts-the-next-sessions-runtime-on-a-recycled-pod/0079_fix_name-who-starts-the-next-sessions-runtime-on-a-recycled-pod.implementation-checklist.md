# Implementation checklist: Name who starts the next session's runtime on a recycled pod

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. Lands the `spec/04_system-components.md` §4.7.9 edit.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands the `spec/04_system-components.md` §4.7.10 edit.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands the `spec/04_system-components.md` §4.7 adapter RPC table edits.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. Lands the `spec/04_system-components.md` §4.6.1 and §4.6.3 edits. Its tier-11 run leaves the `spec_28_register_writers_test.go` case red until S19 lands.
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · spec** — SPEC-5. Lands the `spec/05_runtime-registry-and-pool-model.md` §5.1 setup commands and Runtime definition example edits and the §5.2 recycle lifecycle and scrub procedure edits.
      Tiers 0, 11. Depends on: S4
- [ ] **S6 · spec** — SPEC-6. Lands the `spec/05_runtime-registry-and-pool-model.md` §5.2 scrub steps edits.
      Tiers 0, 11. Depends on: S5
- [ ] **S7 · spec** — SPEC-7. Lands the `spec/05_runtime-registry-and-pool-model.md` §5.2 retirement and sizing edits.
      Tiers 0, 11. Depends on: S6
- [ ] **S8 · spec** — SPEC-8. Lands the `spec/05_runtime-registry-and-pool-model.md` §5.2 acknowledgment, tenant pin, and client visibility edits.
      Tiers 0, 11. Depends on: S7
- [ ] **S9 · spec** — SPEC-9. Lands the `spec/06_warm-pod-model.md` §6.1 and §6.2 edits.
      Tiers 0, 11. Depends on: S8
- [ ] **S10 · spec** — SPEC-10. Lands the `spec/07_session-lifecycle.md` §7.1 `podReuse` and `residualStateWarning` row edits.
      Tiers 0, 11. Depends on: S9
- [ ] **S11 · spec** — SPEC-11. Lands the `spec/15_external-api-surface.md` §15.4, §15.4.1, §15.4.2, and §15.4.3 edits.
      Tiers 0, 11. Depends on: S10
- [ ] **S12 · spec** — SPEC-12. Lands the `spec/29_communication-scenarios.md` §29.2, §29.4, §29.9, and §29.10 edits.
      Tiers 0, 11. Depends on: S11
- [ ] **S13 · spec** — SPEC-13. Lands the `spec/11_policy-and-controls.md` §11.4 full-revoke step 3 edit.
      Tiers 0, 11. Depends on: S12
- [ ] **S14 · spec** — SPEC-14. Lands the `spec/28_communication-channels.md` §28.3, §28.5.3, and §28.8 edits. Its tier-11 run leaves the `spec_28_register_writers_test.go` case red until S19 lands.
      Tiers 0, 11. Depends on: S13
- [ ] **S15 · spec** — SPEC-15. Lands the `spec/13_security-model.md` §13.1 credential-read boundary edits.
      Tiers 0, 11. Depends on: S14
- [ ] **S16 · spec** — SPEC-16. Lands the `spec/16_observability.md` §16.1 `lenny_warmpool_idle_pods` and `lenny_warmpool_reserved_pods` row edits.
      Tiers 0, 11. Depends on: S15
- [ ] **S17 · spec** — SPEC-17. Lands the `spec/17_deployment-topology.md` §17.8.2 **First-week monitoring workflow.** edit.
      Tiers 0, 11. Depends on: S16
- [ ] **S18 · spec** — SPEC-18. Lands the `spec/10_gateway-internals.md` §10.1.4 **Hold state timeout:** edits.
      Tiers 0, 11. Depends on: S17
- [ ] **S19 · code** — CODE-8, CODE-10, and CODE-11 with FIXTURE-1, TEST-12, TEST-13, TEST-14, TEST-18, TEST-19, and TEST-21. Tenant pin admission, the acquisition refusal and drain on a pool outside the process-reuse rule, pinned idle inventory, and the process-reuse admission rule. It precedes S20, so no landed tree keeps a runtime process across sessions without the pin read and the admission rule.
      Tiers 0, 1, 2, 4, 5, 8, 9, 11. Depends on: S4, S8, S9, S12, S14, S16, and S17
- [ ] **S20 · code** — CODE-1, CODE-2, CODE-3, and CODE-4 with TEST-1, TEST-2, TEST-3, TEST-4, TEST-5, and TEST-20. The transport, the terminate-frame deletion, the runtime generation, and the pod-scope teardown at adapter exit and at the coordinator hold timeout.
      Tiers 0, 1, 7a, 9. Depends on: S1, S2, S3, S5, S6, S7, S9, S11, S12, S13, S14, S18, S19, and proposal 0078
- [ ] **S21 · code** — CODE-5, CODE-6, and CODE-7 with TEST-6, TEST-7, TEST-8, and TEST-9. The liveness report, the `Decide` branch, and the gateway threading.
      Tiers 0, 1, 2, 3, 4. Depends on: S3, S4, S7, S9, and S20
- [ ] **S22 · code** — CODE-9. The comment corrections and the regenerated SandboxClaim CRD descriptions.
      Tiers 0, 1. Depends on: S1, S2, S3, S4, S5, S6, S8, S11, S13, S14, S16, S18, S19, and S21
- [ ] **S23 · test** — TEST-10, TEST-11, and TEST-15. The conformance, cluster, and scenario tests.
      Tiers 0, 5, 7a, 10. Depends on: S19 and S21
- [ ] **S24 · docs** — DOC-1, DOC-2, DOC-3, DOC-4, DOC-5, and DOC-6 with TEST-16. The documentation.
      Tiers 0, 11. Depends on: S18 and S22
