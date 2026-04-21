# Technical Design Review Findings — 2026-04-20 (Iteration 6)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-povs.md`
**Iteration:** 6 of N (continuing until 0 Critical/High/Medium findings remain)
**Total findings:** ~68 across 15 reviewed perspectives (10 perspectives deferred — see Dispatch Anomaly below)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 1     |
| Medium   | 13    |
| Low      | ~50   |
| Info     | ~4    |

### Critical Findings

No Critical findings in this iteration.

### High Findings

| # | Perspective | Finding | Section |
|---|-------------|---------|---------|
| CRD-020 | Credential | `lenny_credential_revoked_with_active_leases` gauge and `CredentialCompromised` alert cannot observe user-scoped revocation-propagation failures — iter5 CRD-015 fix introduced a production-monitoring asymmetry | `spec/16_observability.md` §16.4 (gauge labels `pool, provider`) and §16.5 (alert expression); `spec/04_system-components.md` §4.9 line 1348 user revoke handler |

### Medium Findings

| # | Perspective | Finding | Section |
|---|-------------|---------|---------|
| OBS-037 | Observability | `QuotaFailOpenUserFractionInoperative` is an alert in docs, a startup log warning in the spec, and absent from §16.5 | `spec/12_storage-architecture.md` §12.4; `spec/16_observability.md` §16.5; `docs/operator-guide/observability.md` |
| OBS-038 | Observability | `LegalHoldCheckpointAccumulationProjectedBreach` PromQL uses `storageQuotaBytes` as a bare identifier, which is a config value, not a metric — alert will not evaluate | `spec/16_observability.md` §16.5 |
| API-020 | API Design | `/v1/admin/circuit-breakers/*` endpoints absent from the §15.1 endpoint table despite being the sole referent of `INVALID_BREAKER_SCOPE` | `spec/15_external-api-surface.md:888`, endpoint catalog 773-886 |
| API-021 | API Design | `circuit_breaker` domain missing from the §15.1 closed scope taxonomy | `spec/15_external-api-surface.md:911` |
| API-022 | API Design | `PLATFORM_AUDIT_REGION_UNRESOLVABLE` categorized `POLICY` breaks the fail-closed-mirror family convention (`BACKUP/ARTIFACT/LEGAL_HOLD_ESCROW *_REGION_UNRESOLVABLE` are `PERMANENT`) | `spec/15_external-api-surface.md:1037`; `spec/25_agent-operability.md:4339` |
| API-023 | API Design | `GIT_CLONE_REF_UNRESOLVABLE` conflates transient (`network_error`) and permanent (`auth_failed`, `ref_not_found`) sub-reasons under one `PERMANENT`/422 code | `spec/15_external-api-surface.md:1058` |
| API-024 | API Design | `GIT_CLONE_REF_UNRESOLVABLE` missing from §15.2.1 `RegisterAdapterUnderTest` session-creation rejection matrix | `spec/15_external-api-surface.md:1390` |
| CRD-021 | Credential | `docs/runbooks/credential-revocation.md` covers only pool-credential revocation; user-credential revoke path has no operator runbook | `docs/runbooks/credential-revocation.md`; `spec/04_system-components.md` §4.9 user revoke handler |
| CNT-020 | Content Model | `resolvedCommitSha` request/response schema asymmetry is undocumented (request rejects it, response populates it; `additionalProperties: false` + `readOnly: true` interaction unclear) | `spec/14_workspace-plan-schema.md` §14 gitClone variant |
| CNT-023 | Content Model | §14.1 `oneOf` + open-fallthrough branching description is not valid JSON Schema 2020-12 semantics — should be `allOf`+`if`/`then` | `spec/14_workspace-plan-schema.md` §14.1 line 334 |
| CNT-024 | Content Model | `WORKSPACE_PLAN_INVALID` referenced 6× in §14/§14.1 but missing from §15.1 error catalog and `docs/reference/error-catalog.md` | `spec/14_workspace-plan-schema.md`; `spec/15_external-api-surface.md` §15.1; `docs/reference/error-catalog.md` |
| DOC-024 | Document Quality | Cross-file anchor `15_external-api-surface.md#154-errors-and-degradation` does not exist — 9 broken occurrences introduced by iter5 compliance-fix commit | Multiple spec files |
| DOC-025 | Document Quality | Cross-file anchor `17_deployment-topology.md#1781-helm-values` does not exist — 3 broken occurrences introduced by iter5 compliance-fix commit | Multiple spec files |

### Severity Calibration Note

Iter6 severity anchors to the iter1-iter5 rubric per `feedback_severity_calibration_iter5.md`. The single High finding (CRD-020) is a direct regression of the iter5 CRD-015 fix: the production-monitoring gauge and alert do not cover the user-credential path that iter5 added to the deny-list, so the runbook assertions ("confirm `CredentialCompromised` clears within 60s") are structurally unavailable for user revocations. Same failure-class as CRD-015 (High anchor).

All 13 Medium findings are genuine contract/correctness breakages introduced as second-order effects of iter5 fixes:
- **OBS-037/038:** Alert catalog drift and invalid PromQL introduced by iter5 storage-alert work (STO-020/021 / QuotaFailOpenUserFraction families).
- **API-020/021/022/023/024:** Endpoint / scope-taxonomy / error-category / session-rejection-matrix omissions introduced by iter5 new-surface work (POL-023 breaker endpoints, CMP-058 platform-audit region error, CNT-015 git-clone ref error).
- **CRD-021:** iter5 introduced a user-credential revoke path but the operator runbook was not synced.
- **CNT-020/023/024:** iter5 CNT-014/015 JSON-Schema work introduced request/response schema asymmetry, invalid `oneOf` branching, and a dangling error-code reference.
- **DOC-024/025:** iter5 compliance-fix commit (`c941492`) introduced 12 new broken cross-file anchors while closing 3 from iter5 — a 4× regression multiplier. Iter5 DOC-019/020/021 flagged this failure class.

### Dispatch Anomaly

Iter6 review perspectives P1-P10 (KIN, SEC, NET, SCP, PRT, DEV, OPS, MTI, STO, DEL) hit the Anthropic API per-agent rate limit during execution (reset: 2026-04-23 14:00 America/Los_Angeles). No iter6 findings are available for those perspectives. Deferral rationale:
- The iter5 fixes that touched these perspectives' areas (NET-070 lenny-ops TLS; STO-017 overshoot formula; STO-020 legal-hold quota alert; STO-021 admin field) were cross-reviewed by P12 (Observability), P14 (API Design), and P17 (Credential) — two Medium findings (OBS-037/038) surfaced from that cross-coverage.
- No Critical / High storage or networking regressions were identified from the coverage that did run.
- Convergence decision for iter6 is predicated on fixing the 14 identified C/H/M findings; a subsequent iteration (iter7) will re-dispatch P1-P10 for regression verification.

---

## Detailed Findings by Perspective

*Per-perspective findings are recorded in the individual files `p11_session.md` through `p25_execution_modes.md` alongside this summary. Deferred perspectives carry `Deferred — sub-agent rate-limit exhaustion` stubs (`p1_k8s.md`, `p2_security.md`, `p3_networking.md`, `p4_scalability.md`, `p5_protocols.md`, `p6_dev_experience.md`, `p7_operator_experience.md`, `p8_multi_tenancy.md`, `p9_storage.md`, `p10_delegation.md`).*

### Perspective 11 — Session Lifecycle & Contract (SES)

See `p11_session.md`. CNT-015 integration verified end-to-end. 3 Low carry-forwards (SES-019/020/021).

### Perspective 12 — Observability & Production Operations (OBS)

See `p12_observability.md`. Iter5 OBS-031..036 verified fixed. **2 new Medium (OBS-037, OBS-038) — both iter5-introduced drift.**

### Perspective 13 — Compliance, Governance & Data Sovereignty (CMP)

See `p13_compliance.md`. Iter5 CMP-054/057/058 verified fixed end-to-end. 4 Low/Info cleanup items (CMP-059..062). Converged.

### Perspective 14 — API Design (API)

See `p14_api_design.md`. Iter5 new surfaces present. **5 new Medium (API-020..024)** — endpoint-table omission, scope-taxonomy omission, error-category inconsistency, transient/permanent conflation, rejection-matrix omission. 4 Low (3 carry + API-025).

### Perspective 15 — Competitive Positioning (COM)

See `p15_competitive.md`. 1 Low (COM-005 ADR-0008 status ambiguity).

### Perspective 16 — Warm Pool / Pre-warming (WPL)

See `p16_warm_pool.md`. 0 new findings. Converged (4 iter4 carry-overs held).

### Perspective 17 — Credential Management (CRD)

See `p17_credential.md`. Iter5 CRD-015 verified fixed. **1 new High (CRD-020)** — gauge/alert cannot observe user-credential revocation-propagation failures. **1 new Medium (CRD-021)** — user-credential runbook missing. 1 Low (CRD-022) + 4 Low carry-forwards (CRD-016..019).

### Perspective 18 — Content Model, Data Formats & Schema (CNT)

See `p18_content.md`. Iter5 CNT-014/015 verified fixed. **3 new Medium (CNT-020, CNT-023, CNT-024)** + 6 Low (3 new + 3 carry). `WORKSPACE_PLAN_INVALID` catalog gap, JSON Schema `oneOf` invalidity, request/response schema asymmetry.

### Perspective 19 — Build Sequence (BLD)

See `p19_build.md`. Iter5 BLD-012/014 verified fixed. 3 Low (2 carry + BLD-018 row-number drift from iter5 fix). Converged for C/H/M.

### Perspective 20 — Failure Mode Reasoning (FMR)

See `p20_failure.md`. Iter5 FMR-018 verified fixed. 1 Low (FMR-020 duplicate of OBS-037). Converged for C/H/M.

### Perspective 21 — Experimentation & Feature Flags (EXP)

See `p21_experimentation.md`. Iter5 EXP-023 verified fixed. 8 Low carry-forwards (EXP-017..025). Converged for C/H/M.

### Perspective 22 — Document Quality & Consistency (DOC)

See `p22_document.md`. Iter5 DOC-019/020/021 verified fixed. **2 new Medium (DOC-024, DOC-025)** — 12 new broken cross-file anchors introduced by iter5 compliance-fix commit. 3 Low + 1 Info + 2 carry-forwards.

### Perspective 23 — Messaging & Event Propagation (MSG)

See `p23_messaging.md`. 5 Low carry-forwards (MSG-023..027). Converged for C/H/M.

### Perspective 24 — Policy Engine & Admission Control (POL)

See `p24_policy.md`. Iter5 POL-023/025/026 verified fixed end-to-end. 5 Low (3 carry + 2 new — POL-032 `25_agent-operability.md` field description drift; POL-033 `docs/reference/cloudevents-catalog.md` payload-field drift). Converged for C/H/M.

### Perspective 25 — Execution Modes (EXM)

See `p25_execution_modes.md`. 3 Low carry-forwards (EXM-016..018). Converged for C/H/M.

---

## Cross-Cutting Themes

### 1. Iter5 new-surface additions consistently miss catalog/index inclusion (6 findings)

Multiple iter5 fixes introduced new endpoints / scopes / error codes / session-creation rejections but did not update the corresponding catalog / taxonomy / enumeration:
- `/v1/admin/circuit-breakers/*` added but not in §15.1 endpoint table (API-020)
- `circuit_breaker` scope added but not in §15.1 closed taxonomy (API-021)
- `WORKSPACE_PLAN_INVALID` referenced but not catalogued (CNT-024)
- `GIT_CLONE_REF_UNRESOLVABLE` added but not in §15.2.1 RegisterAdapterUnderTest matrix (API-024)
- `QuotaFailOpenUserFractionInoperative` alert referenced but not in §16.5 (OBS-037)
- `resolvedCommitSha` field written on responses but not declared in §14 schema (API-025 Low)

**Systemic fix:** A single-source-of-truth CI gate that cross-checks endpoint/scope/error-code/event/alert enumerations against references would have caught all six. This pattern recurred from iter4 (OBS-023) and iter5 (OBS-031/032, DOC-019/020/021) — it is the dominant failure mode of the review-fix loop.

### 2. Iter5 fixes introduced family/category convention inconsistencies (2 findings)

- `PLATFORM_AUDIT_REGION_UNRESOLVABLE` categorized POLICY while its three siblings (BACKUP/ARTIFACT/LEGAL_HOLD_ESCROW `_REGION_UNRESOLVABLE`) are PERMANENT (API-022)
- `GIT_CLONE_REF_UNRESOLVABLE` is a single PERMANENT code covering both transient (`network_error`) and permanent (`auth_failed`, `ref_not_found`) sub-reasons, breaking the iter4 `*_UNAVAILABLE`/`*_MISSING` split pattern (API-023)

### 3. Iter5 fixes introduced documentation/operational-surface drift (4 findings)

- User-credential revocation path added but `docs/runbooks/credential-revocation.md` not updated (CRD-021)
- `VALIDATION_ERROR` vs. `WORKSPACE_PLAN_INVALID` discrepancy in `docs/reference/workspace-plan.md` (CNT-025 Low)
- `opener`/`closer` vs. `operator_sub` field name drift in `docs/reference/cloudevents-catalog.md` (POL-033 Low)
- `opener`/`closer` vs. `operator_sub` field name drift in `spec/25_agent-operability.md` (POL-032 Low)

**Systemic fix:** The `feedback_docs_sync_after_spec_changes.md` directive was partially but not fully honored in the iter5 docs-sync pass. A checklist covering every new endpoint/error/event/field/runbook needs to be enforced.

### 4. Iter5 fixes introduced 12 new broken cross-file anchors (2 Medium findings)

The iter5 compliance-fix commit (`c941492`) touched cross-file anchors without verifying them — introducing `#154-errors-and-degradation` (9 sites) and `#1781-helm-values` (3 sites) while closing only 3 broken anchors. This is a 4× regression multiplier. Iter5 DOC-019 called out the need for a 20-line Python anchor-integrity gate; it was not added.

### 5. Monitoring blind-spots at path boundaries (1 High finding)

The CRD-015 fix correctly added a tagged-union deny-list covering `{source: "pool"}` and `{source: "user"}` entries, but the observability did not follow the structural change: `lenny_credential_revoked_with_active_leases{pool, provider}` labels are unusable for user entries (no pool), and the `CredentialCompromised` alert expression is similarly pool-bound. This is the same class of issue as iter4 OBS-023 (runbook references undefined alert) and iter5 OBS-031/032 (runbook→alert gaps) — a fix changed the data model but the production-monitoring surface was not mirrored.

### 6. JSON Schema formalism (1 Medium finding)

§14.1's "`oneOf` with open fallthrough" description (CNT-023) is not valid JSON Schema 2020-12 semantics — `oneOf` requires exactly-one match, which conflicts with the intended "known types OR catch-all" behavior. Requires `allOf`+`if`/`then` or similar construction.

---

**End of iter6 consolidated summary.**
