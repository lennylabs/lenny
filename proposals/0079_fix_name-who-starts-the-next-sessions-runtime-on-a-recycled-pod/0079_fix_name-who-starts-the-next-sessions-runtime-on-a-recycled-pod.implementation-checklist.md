# Implementation checklist: Name who starts the next session's runtime on a recycled pod

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4. The §4.7.9 step, the §4.7.10 lifetime paragraph and row, the §4.7 `Shutdown` and `ReportPodScrub` rows, and the §4.6.1 and §4.6.3 reserve, re-warm, retire, and pin statements.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-5, SPEC-6, SPEC-7, SPEC-8. The §5.2 recycle lifecycle, scrub reach, retirement and sizing, the `warn`-outcome retire, acknowledgment, tenant pin, cross-tenant paragraph, `Pod reuse` configuration row, and client visibility.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-9, SPEC-10, SPEC-11, SPEC-12, SPEC-13. The §6.1 row and §6.2 projection, edges, hold, and preConnect re-warm carve-out; the §7.1 `residualStateWarning` row; §15.4 and §15.4.2; §29.2 step 25 and §29.4 steps 12, 13, and 17; §11.4 full-revoke step 3.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · code** — CODE-1, CODE-2, CODE-4 with TEST-1, TEST-2, TEST-3, TEST-17. The kept transport, the sticky ended state, the terminate frame at pod exit, and the pod-scope teardown, with the dispositions of proposal 0078's socket cases, of the `slotsession_test.go` terminate-frame cases, and of the `holdstate_test.go` `:922` terminate-frame assertion.
      Tiers 0, 1, 4. Depends on: S3 and proposal 0078
- [ ] **S5 · code** — CODE-3 with TEST-4 and TEST-5. The generation without reset, the `holdstate_test.go` `:911` sole-session change, the drain-gate test deletion, and the reader-stop race.
      Tiers 0, 1, 7a. Depends on: S4
- [ ] **S6 · code** — CODE-5 with TEST-7's adapter cases (including `gatewaycontrol/scrubreport_test.go`) and TEST-8. The liveness sample and the `runtime_live` wire field.
      Tiers 0, 1, 3. Depends on: S4
- [ ] **S7 · code** — CODE-6, CODE-7 with TEST-6, TEST-7's gateway cases (including `cmd/lenny-gateway/scrub_report_wiring_test.go`), and TEST-9. The `Decide` branches and the gateway threading.
      Tiers 0, 1, 4. Depends on: S6
- [ ] **S8 · code** — CODE-8, CODE-10 with TEST-12, TEST-13, TEST-14, TEST-18. Tenant pin admission on every idle acquisition, the fallback pin check inside the mirror transaction and the fallback pin stamp, the cross-tenant flag, and pinned idle pods excluded from the unpinned warm target.
      Tiers 0, 1, 2, 9. Depends on: S3
- [ ] **S9 · code** — CODE-9. The comment corrections.
      Tiers 0, 1. Depends on: S4
- [ ] **S10 · test** — FIXTURE-1, TEST-10, TEST-11, TEST-15. The conformance case, the tier-5 un-skip, and the tier-7a scenario extension.
      Tiers 0, 5, 7a, 10. Depends on: S5, S7, S8
- [ ] **S11 · docs** — DOC-1 through DOC-5 with TEST-16.
      Tiers 0, 11. Depends on: S3
