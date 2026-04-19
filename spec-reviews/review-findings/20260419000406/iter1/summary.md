# Technical Design Review Findings — 2026-04-19 (Iteration 1)

**Document reviewed:** `spec/` (28 files, ~17,741 lines)
**Review framework:** `spec-reviews/review-povs.md` (25 perspectives + Web Playground)
**Iteration:** 1 of 3
**Total findings:** 52 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 5     |
| High     | 12    |
| Medium   | 25    |
| Low      | 10    |
| Info     | 0     |

### Critical Findings

| #   | ID       | Perspective                  | Finding                                                            | Section                |
| --- | -------- | ---------------------------- | ------------------------------------------------------------------ | ---------------------- |
| 1   | OBS-001  | Observability                | Undefined `lenny_checkpoint_duration_seconds` metric               | §16.1, §16.5           |
| 2   | OBS-004  | Observability                | TTFTBurnRate alert uses `isolation_profile` label not on metric    | §16.5                  |
| 3   | API-001  | API Design                   | `TENANT_SUSPENDED` error code missing from catalog                 | §15.1                  |
| 4   | BLD-001  | Build Sequence               | Phase 4.5 references "Phase 7's AuthEvaluator" (should be 5.75)    | §18                    |
| 5   | EXM-001  | Execution Modes              | Leaked slots counting ambiguous in pod replacement trigger         | §5.2, §6.2             |

### High Findings

| #   | ID       | Perspective                     | Finding                                                            | Section                |
| --- | -------- | ------------------------------- | ------------------------------------------------------------------ | ---------------------- |
| 1   | K8S-035  | Kubernetes Infrastructure       | Pool-config validating webhook lacks formal name and alert         | §4.6.3, §16.5          |
| 2   | PRT-001  | Protocol/Adapter                | A2AAdapter capability mismatch (static vs dynamic elicitation)     | §15.4, §21.1           |
| 3   | PRT-002  | Protocol/Adapter                | `schemaVersion` round-trip loss via A2A metadata undocumented      | §15.4.1, §21.1         |
| 4   | TNT-001  | Tenancy                         | `noEnvironmentPolicy` default not enforced at gateway startup      | §10, §9, §11           |
| 5   | OBS-002  | Observability                   | 10 metric entries lack names in metrics table                      | §16.1                  |
| 6   | API-002  | API Design                      | `If-Match` missing from warm-count, circuit-breaker, rbac-config   | §15.1                  |
| 7   | BLD-002  | Build Sequence                  | Phase 5.75 dependency on Phase 4.5 not explicit                    | §18                    |
| 8   | FLR-001  | Failure Modes                   | Billing stream MAXLEN formula uses 60s but RTO < 30s               | §17.8, §12.3           |
| 9   | MSG-001  | Messaging                       | DLQ overflow `message_dropped` reason code unspecified             | §7.2, §15.4.1          |
| 10  | WPP-001  | Web Playground                  | OIDC cookie-to-bearer token exchange undefined                     | §27, §10.2, §15        |
| 11  | DEL-001  | Delegation                      | `maxDelegationPolicy` inheritance semantics ambiguity              | §8.3                   |
| 12  | DEL-003  | Delegation                      | Deep-tree recovery with intermediate `maxDelegationPolicy` narrow  | §8.3, §8.10            |

---

## Detailed Findings by Perspective

### 1. Kubernetes Infrastructure & Controller Design (K8S)
See [K8S.md](K8S.md) — 1 finding: K8S-035 (High).

### 2. Security (SEC)
See [SEC.md](SEC.md) — No real issues.

### 3. Network Security & Isolation (NET)
See [NET.md](NET.md) — No real issues.

### 4. Scalability & Performance (PRF)
See [PRF.md](PRF.md) — 1 finding: PRF-001 (Medium).

### 5. Protocol & Adapter Contracts (PRT)
See [PRT.md](PRT.md) — 4 findings: PRT-001, PRT-002 (High); PRT-003 (Medium); PRT-004 (Low).

### 6. Developer Experience (DXP)
See [DXP.md](DXP.md) — No real issues.

### 7. Operator Experience (OPS)
See [OPS.md](OPS.md) — 3 findings: OPS-001 (Medium), OPS-002, OPS-003 (Low).

### 8. Tenancy (TNT)
See [TNT.md](TNT.md) — 1 finding: TNT-001 (High).

### 9. Storage Architecture (STR)
See [STR.md](STR.md) — 4 findings: STR-001, STR-002, STR-003 (Medium); STR-004 (Low).

### 10. Recursive Delegation (DEL)
See [DEL.md](DEL.md) — 5 findings: DEL-001, DEL-003 (High); DEL-002, DEL-004, DEL-005 (Medium).

### 11. Session Lifecycle (SES)
See [SES.md](SES.md) — 3 findings: SES-001 (Medium); SES-002, SES-003 (Low).

### 12. Observability (OBS)
See [OBS.md](OBS.md) — 6 findings: OBS-001, OBS-004 (Critical); OBS-002 (High); OBS-003, OBS-005 (Medium); OBS-006 (Low).

### 13. Compliance (CMP)
See [CMP.md](CMP.md) — 2 findings: CMP-042, CMP-043 (Medium).

### 14. API Design (API)
See [API.md](API.md) — 3 findings: API-001 (Critical); API-002 (High); API-003 (Medium).

### 15. Competitive Positioning / OSS (CPS)
See [CPS.md](CPS.md) — 1 finding: CPS-001 (Medium).

### 16. Warm Pool & Pod Lifecycle (WPL)
See [WPL.md](WPL.md) — No real issues.

### 17. Content Model / Schema (CNT)
See [CNT.md](CNT.md) — 1 finding: CNT-001 (Medium).

### 18. Build Sequence (BLD)
See [BLD.md](BLD.md) — 3 findings: BLD-001 (Critical); BLD-002 (High); BLD-003 (Medium).

### 19. Failure Modes (FLR)
See [FLR.md](FLR.md) — 1 finding: FLR-001 (High).

### 20. Evaluation / Experiment (EXP)
See [EXP.md](EXP.md) — 1 finding: EXP-001 (Medium).

### 21. Documentation Quality (DOC)
See [DOC.md](DOC.md) — 2 findings: DOC-001 (Medium); DOC-002 (Low).

### 22. Messaging Semantics (MSG)
See [MSG.md](MSG.md) — 3 findings: MSG-001 (High); MSG-002, MSG-003 (Medium).

### 23. Policy & Admission (POL)
See [POL.md](POL.md) — No real issues.

### 24. Execution Mode Matrix (EXM)
See [EXM.md](EXM.md) — 3 findings: EXM-001 (Critical); EXM-002 (Medium); EXM-003 (Low).

### 25. Web Playground (WPP)
See [WPP.md](WPP.md) — 4 findings: WPP-001 (High); WPP-002, WPP-004 (Medium); WPP-003 (Low).

---

## Cross-Cutting Themes

1. **Undefined / orphaned references**: Several alerts, error codes, and metrics are referenced but not defined (OBS-001, OBS-002, OBS-004, API-001, PRT-003, FLR-001). Spec lacks a final cross-reference validation pass.

2. **Startup-time configuration enforcement**: Gateway and controllers have normative defaults that aren't guaranteed to be enforced at startup (TNT-001, K8S-035). Several fail-closed webhooks have corresponding alerts, but one does not.

3. **Cross-section value asymmetries**: Numerical or state values diverge across sections (FLR-001 RTO window; CNT-001 temperature bounds; OBS-004 metric labels; API-002 If-Match documentation pattern).

4. **Delegation inheritance semantics underspecified**: Multiple DEL findings point to unclear behavior when parent policy changes or narrows (DEL-001, DEL-002, DEL-003, DEL-005). Tree recovery and cross-environment hops need explicit rules for snapshot-vs-live policy.

5. **Phase / lifecycle references stale**: Build sequence and roadmap references (BLD-001, CPS-001, BLD-003) reflect earlier plans; needs consistency pass with current phase numbering.

6. **Concurrent-mode semantics leak into non-applicable sections**: Task and stateless execution modes share response schemas with concurrent-workspace, creating ambiguity (EXM-001, EXM-002). Field reuse across modes is inherited but not always explicitly bounded.

7. **Schema/error catalogs drift from usage**: Error codes and metrics used in endpoint docs or alert rules are not always registered in their canonical catalogs (API-001, PRT-003, OBS-001, OBS-002).
