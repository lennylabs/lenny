# Technical Design Review Findings — 2026-04-21 (Iteration 7)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-povs.md`
**Iteration:** 7 of N (continuing until 0 Critical/High/Medium findings remain)
**Total findings:** ~172 across 25 review perspectives (all perspectives re-dispatched after iter6 P1–P10 rate-limit deferral)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 13    |
| Low      | ~140  |
| Info     | ~19   |

### Critical Findings

No Critical findings in this iteration.

### High Findings

No High findings in this iteration.

### Medium Findings

| # | Perspective | Finding | Section | Status |
|---|-------------|---------|---------|--------|
| SEC-017 | Security | Ephemeral debug containers can acquire the agent UID and `lenny-cred-readers` GID — §13.1 claims debug containers pinned to a separate `runAsUser` but no admission webhook is scoped to `pods/ephemeralcontainers` | `spec/13_security.md` §13.1; `spec/17_deployment-topology.md` §17.2 webhook inventory | Fixed — §13.1 claim corrected to acknowledge no K8s-default pin; added `lenny-ephemeral-container-cred-guard` as item 13 in §17.2 admission-policies inventory (baseline webhook, always rendered); added `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` PERMANENT/422 to §15.1 error catalog; added `EphemeralContainerCredGuardUnavailable` Warning alert to §16.5; updated §13.2 admission-webhook NetworkPolicy row + §17.2 HA paragraph + feature-gate inventory to reflect 8→9 webhook count; synced `docs/operator-guide/observability.md`, `docs/reference/error-catalog.md`, `docs/operator-guide/namespace-and-isolation.md`. |
| SEC-018 | Security | Elicitation text integrity not preserved across intermediate chain hops — intermediate pods can rewrite elicitation text; `initiator_type` does not identify which chain node authored the visible wording | `spec/09_elicitation.md` §9.2 | Fixed — added normative "Elicitation content integrity (gateway-origin binding)" paragraph to §9.2 establishing the gateway as the authoritative source for elicitation display text; intermediate pods forward by `elicitation_id` only and MUST NOT rewrite `{title, description, schema, inputs}`; added `ELICITATION_CONTENT_TAMPERED` PERMANENT/409 to §15.1 error catalog; added `lenny_elicitation_content_tamper_detected_total{origin_pod, tampering_pod}` counter to §16.1; added `ElicitationContentTamperDetected` Critical alert to §16.5; added `elicitation.content_tamper_detected` audit event to §16.7 with SHA-256 attestation payload (no divergent content persisted); synced `docs/reference/error-catalog.md`, `docs/reference/metrics.md`, `docs/operator-guide/observability.md`, and `docs/runtime-author-guide/platform-tools.md`. |
| KIN-028 | Kubernetes | Admission-plane feature-gate downgrade (`true → false`) declared "invalid" but has no runtime enforcement — `helm upgrade` with reverted `values.yaml` silently vanishes a webhook | `spec/17_deployment-topology.md` §17.2 | Fixed — added "Feature-flag downgrade enforcement (phase-stamp ConfigMap + render-time validation + runtime alert)" three-layer enforcement subsection to §17.2 immediately after the feature-gated chart inventory table: (1) chart-rendered append-only `lenny-deployment-phase-stamp` ConfigMap (one JSON entry per flag with `{enabled, enabledAt RFC3339}`, hook-weight -20 so it precedes `lenny-preflight` at -10, read-only RBAC for `lenny-preflight-sa` and `lenny-ops-sa`, no runtime writer), (2) fail-closed chart render-time validation with error code `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` reading the existing ConfigMap via Helm `lookup`, with an explicit `acceptFeatureFlagDowngrade.<flag>=true` override that emits `deployment.feature_flag_downgrade_acknowledged` audit event and retains the phase-stamp entry so the runtime alert continues to fire, covered by new integration test `tests/integration/phase_stamp_downgrade_test.go` parameterised over every `(from, to)` combination, (3) `AdmissionPlaneFeatureFlagDowngrade` Warning alert added to §16.5 with PromQL comparing `kube_configmap_info`-exported phase-stamp labels against `kube_validatingwebhookconfiguration_info` sustained > 2 min (labels `flag_name` and `expected_webhook_name`); (4) added `PREFLIGHT_PHASE_STAMP_MISMATCH` preflight Job check in §17.2 "Checks performed" table (row 514) to catch the narrow race where `helm template lookup` saw no ConfigMap but the live cluster still carried a `true`-flagged phase-stamp; (5) added baseline-webhook interaction note clarifying `lenny-ephemeral-container-cred-guard` (SEC-017) has no phase-stamp entry (always rendered, caught by existing admission-webhook inventory check); (6) added runbook entry to §17.7 (`docs/runbooks/admission-plane-feature-flag-downgrade.md`) with diagnosis/remediation sequence; synced `docs/operator-guide/observability.md` (new alert row) and `docs/operator-guide/configuration.md` (new "Admission-plane feature flags" + "Acknowledged downgrade override" subsection). Error codes `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` and `PREFLIGHT_PHASE_STAMP_MISMATCH` are install/upgrade-time errors (not HTTP API errors) and are documented in-line in §17.2 rather than in the runtime error catalog. |
| OBS-039 | Observability | Malformed PromQL in `LegalHoldCheckpointAccumulationProjectedBreach` alert — `on(tenant_id) group_left` applied to scalar-vector product is unparseable (introduced by iter6 OBS-038 fix) | `spec/16_observability.md:488` | Fixed — replaced malformed `0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes` with valid PromQL `0.9 * lenny_tenant_storage_quota_bytes` (natural vector-matching on the `{tenant_id}` labelset carried by both sides of `>`); added trailing `and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` guard to restrict firing to tenants with an active legal hold (also closes OBS-043 robustness finding); rewrote accompanying prose to explain why no explicit vector-matching modifier is needed and to name the `and on(tenant_id)` guard. Cross-references (§12.5, §12.8, §11.2, §17.7, runbook `docs/runbooks/legal-hold-quota-pressure.md`) preserved; severity and `root_session_id` label guidance unchanged. |
| OBS-040 | Observability | Docs drift — `docs/operator-guide/observability.md:190` still carries pre-iter6 broken expression `lenny_legal_hold_checkpoint_projected_growth_bytes / (storageQuotaBytes - lenny_storage_quota_bytes_used) > 0.9` | `docs/operator-guide/observability.md:190` | Fixed — replaced the stale broken expression at `docs/operator-guide/observability.md:193` with the current canonical form from `spec/16_observability.md` §16.5 (post-OBS-039): `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0`. Updated the accompanying prose to explain that the right-hand denominator uses the `lenny_tenant_storage_quota_bytes` gauge (not a bare `storageQuotaBytes` config identifier), and to describe the `and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` guard. Severity, runbook link (`legal-hold-quota-pressure`), and row structure preserved. No other bare-identifier references to `storageQuotaBytes` as a Prometheus metric remain in the observability doc. |
| OBS-041 | Observability | Docs drift — `docs/operator-guide/observability.md:189` still describes `QuotaFailOpenUserFractionInoperative` as a startup warning, not the PromQL alert now in §16.5 (iter6 OBS-037 partial docs sync) | `docs/operator-guide/observability.md:189` | Fixed — replaced the stale "Gateway startup warning emitted when `quotaUserFailOpenFraction >= 0.5`" row at `docs/operator-guide/observability.md:192` with the current canonical form from `spec/16_observability.md` §16.5: expression `lenny_quota_user_failopen_fraction >= 0.5` as a continuously-firing Prometheus alert, backed by the `lenny_quota_user_failopen_fraction` gauge. Updated the accompanying prose to describe the continuous-firing semantics (not startup-only) so operators who joined after gateway startup can still see the signal in Prometheus; retained the supplementary mention of the structured log warning at gateway startup and the `lenny-ops` config-validation warning to mirror the spec's explicit two-emission-channel phrasing. Severity (Warning), row structure (alert name / expression / action), and neighbouring-row conventions preserved. Closes POL-034 (Low) on the same docs surface. |
| OBS-042 | Observability | Spec drift — `spec/17_deployment-topology.md:820` still uses bare-identifier `0.9 * storageQuotaBytes`; iter6 OBS-038 fixed §16.5 but missed §17.7 | `spec/17_deployment-topology.md:820` | Fixed — replaced the bare-identifier `0.9 * storageQuotaBytes` PromQL in the `LegalHoldCheckpointAccumulationProjectedBreach` runbook-catalog row at `spec/17_deployment-topology.md` §17.7 with the current canonical form from §16.5 (post-OBS-039): `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0`. Expression now matches §16.5 verbatim; runbook link, severity, and row structure preserved; no other bare-identifier `0.9 * storageQuotaBytes` PromQL references remain in §17.7. |
| CMP-063 | Compliance | `docs/reference/error-catalog.md:174-175` places `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` and `PLATFORM_AUDIT_REGION_UNRESOLVABLE` under POLICY while spec classifies both as PERMANENT/422 (iter6 API-022 docs-sync regression) | `docs/reference/error-catalog.md:174-175` | Fixed — moved `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` and `PLATFORM_AUDIT_REGION_UNRESOLVABLE` rows out of the POLICY table and into the PERMANENT table in `docs/reference/error-catalog.md` adjacent to sibling `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` (rows now at lines 119-120). Row content (code, 422 status, description, `details.*` shape, recommended action) preserved verbatim. Classification now matches spec §15.1 lines 1041-1042, §11.7, and §25.11. No spec, metrics, alert, or configuration changes required. |
| API-026 | API Design | `docs/api/admin.md:712-713` advertises `platform-admin, tenant-admin` on circuit-breakers GET endpoints, contradicting authoritative `platform-admin`-only in spec §11.6 / §15.1 / §24.7 (iter6 API-020 docs-sync gap) | `docs/api/admin.md:712-713` | Fixed — updated the two `GET /v1/admin/circuit-breakers` and `GET /v1/admin/circuit-breakers/{name}` rows in the operator-managed circuit-breakers table (`docs/api/admin.md:712-713`) to show `platform-admin` only, removing the incorrect `, tenant-admin`. Rows now match authoritative spec (§11.6 line 306, §15.1 lines 884-885, §24.7 lines 105-107). The two POST rows (`.../open`, `.../close`) were already `platform-admin`-only and remain unchanged. All four endpoint rows now consistently show `platform-admin`-only, matching the security posture of operator-managed (platform-level) breakers. No spec changes. |
| API-027 | API Design | No per-endpoint `x-lenny-scope` declarations for the four `/v1/admin/circuit-breakers/*` endpoints despite iter6 API-021 adding `circuit_breaker` to scope taxonomy (partial-fix regression) | `spec/15_external-api-surface.md` §15.1, §24.7 | Fixed — added per-endpoint `x-lenny-scope` bindings at `spec/15_external-api-surface.md` §15.1 lines 884–887 (inline in each endpoint row alongside `x-lenny-mcp-tool` and `x-lenny-category`): `GET /v1/admin/circuit-breakers` → `tools:circuit_breaker:read`, `GET /v1/admin/circuit-breakers/{name}` → `tools:circuit_breaker:read`, `POST /v1/admin/circuit-breakers/{name}/open` → `tools:circuit_breaker:write`, `POST /v1/admin/circuit-breakers/{name}/close` → `tools:circuit_breaker:write`. Added a matching `x-lenny-scope` column to the §11.6 circuit-breakers admin-API table (`spec/11_policy-and-controls.md:306–313`) cross-referencing §15.1. Added four corresponding MCP tool entries (`lenny_circuit_breaker_list`/`_get`/`_open`/`_close`) to the §25.12 tool inventory (`spec/25_agent-operability.md:4426–4427, 4474–4475`) declaring the same scope values. Added `circuit_breaker` to the §25.1 domain list (`spec/25_agent-operability.md:79`) so the two domain mirrors (§15.1:915 and §25.1:79) remain in lockstep. Docs sync: updated `docs/api/admin.md:712–715` operator-managed circuit-breakers table to include a Scope column with the four `tools:circuit_breaker:<action>` values. Closes the partial-fix regression of iter6 API-021. |
| API-028 | API Design | `POST /v1/admin/circuit-breakers/{name}/open` and `.../close` are silent on `dryRun` — neither in §15.1 supported-endpoints table nor in excluded-actions list (catalog-integrity gap from iter6 API-020) | `spec/15_external-api-surface.md` §15.1 | Fixed — added `POST /v1/admin/circuit-breakers/{name}/open` and `POST /v1/admin/circuit-breakers/{name}/close` as two new rows in the §15.1 `dryRun` supported-endpoints table (`spec/15_external-api-surface.md:1192-1193`), each documenting its per-endpoint semantics: the gateway validates body/scope-immutability/existence as appropriate, reads Redis but does **not** write, and returns the real-call response shape plus a top-level `simulation` object (`currentState`, `predictedState`, `wouldChangeState`) for idempotency previewing; no `circuit_breaker.state_changed` audit event under `dryRun`. Updated the opening `dryRun` paragraph at line 1132 to call out that circuit-breaker action endpoints are the sole `dryRun`-supporting action-style endpoints (pool/session `drain`/`force-terminate`/`warm-count` remain excluded), and updated the closing exclusion paragraph at line 1197 with the matching rationale. Noted that circuit-breaker `open`/`close` do not carry ETags (Redis-backed, no `version` column) so `If-Match` is ignored regardless of `dryRun` (line 1195). Docs sync: updated `docs/api/admin.md` Dry-run section (line 64-66) to reflect the circuit-breaker exception, appended a **Dry-run: Supported** block to the existing `POST .../open` section, and added a new `POST .../close` endpoint section with a matching **Dry-run: Supported** block. Catalog integrity restored — both endpoints are now in exactly one place (supported table). |
| DOC-031 | Document Quality | Cross-file anchor `12_storage-architecture.md#124-quota-and-rate-limiting` does not exist; §12.4 is "Redis HA and Failure Modes" (`#124-redis-ha-and-failure-modes`) — introduced by iter6 OBS-037 fix | `spec/16_observability.md:203` | Fixed — replaced the broken fragment `#124-quota-and-rate-limiting` with the correct `#124-redis-ha-and-failure-modes` anchor at `spec/16_observability.md` line 204 (in the `lenny_quota_user_failopen_fraction` gauge row added by iter6 OBS-037). §12.4 "Redis HA and Failure Modes" is the authoritative section that documents the `quotaUserFailOpenFraction` config and the per-user fail-open ceiling semantics (lines 222–224), matching the surrounding gauge description. No other instances of `#124-quota-and-rate-limiting` remain in the spec. Closes FMR-021 (Low) in `p20_failure_modes.md` which was the same site under a different ID. |
| DOC-032 | Document Quality | Intra-file anchor `#141-extensibility-rules` does not exist; §14.1 is "WorkspacePlan Schema Versioning" (`#141-workspaceplan-schema-versioning`) — introduced by iter6 CNT-020 fix | `spec/14_workspace-plan-schema.md:104` | Fixed — replaced the broken fragment `#141-extensibility-rules` with the correct `#141-workspaceplan-schema-versioning` anchor at `spec/14_workspace-plan-schema.md:104` (in the "Schema encoding of the request/response asymmetry" paragraph added by iter6 CNT-020). The §14.1 heading "WorkspacePlan Schema Versioning" (line 306) is the authoritative target and already matched by the adjacent cross-reference at line 83. No other instances of `#141-extensibility-rules` remain in the spec. Closes CNT-029 (Low) in `p18_content.md` which was the same site under a different ID. |

### Severity Calibration Note

Iter7 severity anchors to the iter1–iter6 rubric per `feedback_severity_calibration_iter5.md`. No Critical or High findings — convergence is close.

All 13 Medium findings fall into one of three genuine-correctness categories:

1. **iter6-introduced regressions (8):** OBS-039/040/041/042 all stem from the iter6 OBS-037/OBS-038 fix pass (malformed PromQL + incomplete docs-sync + spec drift to §17.7). DOC-031/032 are new broken anchors introduced by iter6 OBS-037 and CNT-020 fixes. API-026/027/028 are docs-sync / catalog-integrity gaps from the iter6 API-020/021 fix envelope. CMP-063 is a docs-sync regression from iter6 API-022 (the fix changed spec but left docs rows in the POLICY table).
2. **Genuine new security findings (2):** SEC-017 (ephemeral debug container credential exposure with concrete attack path) and SEC-018 (elicitation text integrity across chain hops) — surfaced when iter7 re-dispatched P2 after iter6 P1–P10 deferral.
3. **Genuine operability gap (1):** KIN-028 (admission-plane feature-gate downgrade declared prohibited but with no runtime enforcement) — surfaced when iter7 re-dispatched P1.

8 of the 13 Mediums would have been avoided by a stricter post-fix regression sweep within iter6. The remaining 5 (SEC-017/018, KIN-028, and partially CMP-063 which pre-dates iter5) reflect legitimate new-coverage findings from iter7's P1–P10 re-dispatch after iter6 rate-limit deferral.

### Iter6 Fix Verification (all 14 Medium/High findings)

| Finding | Iter6 Status | Iter7 Verification |
|---------|--------------|--------------------|
| CRD-020 (High) | Fixed | Verified — gauge + alert + user-path branch landed; CRD-021 runbook U1–U5 section present |
| OBS-037 (Medium) | Fixed | Spec fix holds; docs-sync regression → OBS-041, POL-034 |
| OBS-038 (Medium) | Fixed (spec) | PromQL regression → OBS-039; docs drift → OBS-040; §17.7 drift → OBS-042 |
| API-020/021/022/023/024 (Medium ×5) | Fixed | Spec fixes verified; iter7 adds API-026/027/028 on same envelope; CMP-063 is docs-sync residual of API-022 |
| CRD-021 (Medium) | Fixed | Verified — user-path section added to runbook |
| CNT-020/023/024 (Medium ×3) | Fixed | Dual-schema + allOf/if-then + catalog entry verified; DOC-032 is broken-anchor residual of CNT-020 |
| DOC-024/025 (Medium ×2) | Fixed | Verified — 0 occurrences of `#154-errors-and-degradation` or `#1781-helm-values` remain |

Net score: **12 of 14 iter6 fixes clean; 2 introduced new Medium regressions on the same fix envelope** (OBS-038 → OBS-039; CNT-020 → DOC-032).

---

## Detailed Findings by Perspective

Per-perspective findings are recorded in individual files `p1_kubernetes.md` through `p25_execution_modes.md` alongside this summary.

### Perspective 1 — Kubernetes Infrastructure & Controller Design (KIN)

See `p1_kubernetes.md`. 1 Medium (KIN-028 admission-plane feature-gate downgrade enforcement gap), 11 Low (KIN-026/027/029/030/031/032/033/035/036/037/038), 1 Info (KIN-034). 5 iter5 Low carry-forwards (KIN-021–025) unchanged.

### Perspective 2 — Security & Threat Modeling (SEC)

See `p2_security.md`. 2 Medium (SEC-017 ephemeral debug container credential exposure; SEC-018 elicitation text integrity across chain hops), 2 Low (SEC-019/020), 1 Info (SEC-021). 5 iter5 carry-forwards re-verified no regression (SEC-008/010/011/012/013); SEC-009 remains deferred pending user direction.

### Perspective 3 — Network Security & Isolation (NET)

See `p3_networking.md`. 5 Low (NET-073/074/075/076/077), 1 Info (NET-078). Iter5 NET-070 (Medium) fully verified; 2 iter5 Low carry-forwards (NET-071/072) unchanged.

### Perspective 4 — Scalability & Performance Engineering (SCP)

See `p4_scalability.md`. 8 Low (SCP-001–004 iter5 carry-forwards; SCP-005–008 new). No Critical/High/Medium under iter5-anchored rubric.

### Perspective 5 — Protocol Design & Future-Proofing (PRT)

See `p5_protocols.md`. 3 Low (PRT-018/019/020), 3 Info (PRT-021/022/023) — all iter5 carry-forwards, no new findings.

### Perspective 6 — Developer Experience — Runtime Authors (DEV)

See `p6_dev_experience.md`. 7 Low (4 iter5 carry-forwards DEV-022–025; 3 new DEV-026/027/028). Converged.

### Perspective 7 — Operator & Deployer Experience (OPS)

See `p7_operator_experience.md`. 7 Low (6 iter5 carry-forwards OPS-016–021; 1 new OPS-022 — `issueRunbooks` routes `CIRCUIT_BREAKER_OPEN` to `gateway-replica-failure` while iter6 added dedicated `docs/runbooks/circuit-breaker-open.md`).

### Perspective 8 — Multi-Tenancy & Tenant Isolation (MTI)

See `p8_multi_tenancy.md`. 2 Low carry-forwards (MTI-001, MTI-002). MTI-003 iter6 API-022 integrity check passed. No Critical/High/Medium.

### Perspective 9 — Storage Architecture & Data Management (STO)

See `p9_storage.md`. 6 Low (STO-022–027), 1 Info (STO-028), plus 2 Low carry-forwards (STO-018/019). Iter5 STO-017/020/021 and iter6 OBS-037/038/CMP-046/054/058 all verified fixed.

### Perspective 10 — Recursive Delegation & Task Trees (DEL)

See `p10_delegation.md`. 4 Low carry-forwards (DEL-008, DEL-011, DEL-014, DEL-015), 3 Info observations. Iter4 DEL-011/012/013 fixes verified intact. Converged.

### Perspective 11 — Session Lifecycle & State Management (SES)

See `p11_session.md`. 6 Low (3 carry-forwards SES-019/020/021; 3 new SES-022/023/024). No Critical/High/Medium.

### Perspective 12 — Observability & Production Operations (OBS)

See `p12_observability.md`. **4 Medium (OBS-039/040/041/042 — all iter6-introduced regressions of OBS-037/038 fixes)**, 3 Low (OBS-043/044/045).

### Perspective 13 — Compliance, Governance & Data Sovereignty (CMP)

See `p13_compliance.md`. **1 Medium (CMP-063 — docs error-catalog POLICY→PERMANENT docs-sync regression from iter6 API-022)**, 3 Low iter6 carry-forwards (CMP-059/060/061), 1 Info (CMP-062).

### Perspective 14 — API Design & External Interface Quality (API)

See `p14_api_design.md`. **3 Medium (API-026 docs role drift; API-027 per-endpoint scope bindings missing; API-028 dryRun-coverage silent) — all scoped to iter6 circuit-breakers fix envelope**, 3 Low iter5 carry-forwards (API-017/018/019). Iter6 API-020–024 and CNT-020 (API-025) all verified fixed on spec side.

### Perspective 15 — Competitive Positioning & Open Source Strategy (COM)

See `p15_competitive.md`. 4 Low carry-forwards (COM-001/002/004/005), 1 Info (COM-003). Zero net-new findings — third consecutive iteration at 0 Critical/High/Medium.

### Perspective 16 — Warm Pool & Pod Lifecycle Management (WPL)

See `p16_warm_pool.md`. 3 Low carry-forwards (WPL-001/002/003), 1 Info (WPL-004). Fourth consecutive iteration at 0 Critical/High/Medium.

### Perspective 17 — Credential Management & Secret Handling (CRD)

See `p17_credential.md`. 11 Low (5 carry-forwards CRD-016/017/018/019/022; 6 new CRD-023–028). Iter6 CRD-020 (High) and CRD-021 (Medium) both verified fixed end-to-end.

### Perspective 18 — Content Model, Data Formats & Schema Design (CNT)

See `p18_content.md`. 6 Low (4 carry-forwards CNT-026/027/028/030; 2 new CNT-029/031). Iter6 CNT-020/023/024 all verified fixed; DOC-032 (Medium, DOC perspective) is a broken-anchor residual of CNT-020.

### Perspective 19 — Build Sequence & Implementation Risk (BLD)

See `p19_build_sequence.md`. 3 Low (BLD-019/020/021 — all iter6 carry-forwards BLD-016/017/018 unchanged). Iter5 BLD-012/014 re-verified as fixed.

### Perspective 20 — Failure Modes & Resilience Engineering (FMR)

See `p20_failure_modes.md`. 5 Low (FMR-017 carry-forwards + FMR-021 new broken anchor from iter6 OBS-037 fix, now part of DOC perspective's DOC-031). Iter6 FMR-020 verified closed.

### Perspective 21 — Experimentation & A/B Testing Primitives (EXP)

See `p21_experimentation.md`. 10 Low (8 iter6 carry-forwards EXP-017–022/024/025; 2 new EXP-026/027 — docs-sync gaps). Third consecutive Low-only iteration.

### Perspective 22 — Document Quality, Consistency & Completeness (DOC)

See `p22_document.md`. **2 Medium (DOC-031 broken cross-file anchor from iter6 OBS-037 fix; DOC-032 broken intra-file anchor from iter6 CNT-020 fix)**, 2 Low (DOC-033/034 — six-iteration carry-forwards), 2 Info (DOC-035/036). Iter6 DOC-024/025 both verified fixed (12 occurrences closed, 2 introduced — 6× improvement).

### Perspective 23 — Messaging, Conversational Patterns & Multi-Turn Interactions (MSG)

See `p23_messaging.md`. 5 Low (MSG-028/029/030/031 iter6 carry-forwards; MSG-032 new), 1 Info (MSG-033 deferred). No Critical/High/Medium.

### Perspective 24 — Policy Engine & Admission Control (POL)

See `p24_policy.md`. 6 Low (POL-029–034). Iter5 POL-023 (High), POL-025/026 (Medium) fixes remain in place.

### Perspective 25 — Execution Modes & Concurrent Workloads (EXM)

See `p25_execution_modes.md`. 3 Low (EXM-019/020/021 — same three persistent carry-forwards from iter4 EXM-010–012). No Critical/High/Medium.

---

## Cross-Cutting Themes

1. **iter6 fix envelope regressions dominate iter7 Mediums.** 8 of 13 Mediums (OBS-039/040/041/042, API-026/027/028, DOC-031/032, CMP-063) are second-order regressions within the iter6 fix envelope — broken PromQL expressions, docs-sync gaps, broken anchors, and missing catalog entries on freshly-added surfaces. A docs-sync pre-commit gate (as recommended in iter5 DOC guidance and the iter7 P22 appendix anchor-integrity script) would have caught 6 of the 8.

2. **Iter7 re-dispatch of P1–P10 (deferred in iter6) surfaced 3 legitimate new-coverage Mediums.** KIN-028, SEC-017, SEC-018 were not introduced by iter6 fixes — they are pre-existing gaps that the iter6 rate-limit deferral prevented from being caught earlier. These are the only Mediums in iter7 that are not iter6-fix-envelope regressions.

3. **Persistent Low-severity carry-forwards accumulate.** EXP-017/018/020 (6 iterations), CNT-026 (5 iterations), DOC-033/034 (6 iterations), OPS-019/020/021 (6 iterations), MSG-028/029/030 (3 iterations). Each iteration adds 2–4 new Lows without clearing the carry-forward tail. Recommendation: batch-close all pre-existing Low carry-forwards in a dedicated iter7 follow-up pass OR accept them formally as "will not fix" via DECISION-LOG entries, since otherwise they will appear in every future review in perpetuity.

4. **Circuit-breakers admin surface is the single largest iter7-regression locus.** 5 of the 13 Mediums (API-026/027/028, CMP-063 partial, and OPS-022) cluster on the iter6 API-020/021/022 fixes. Single-commit iter7 fix covering (a) `docs/api/admin.md` role correction, (b) per-endpoint `x-lenny-scope` bindings at §11.6/§25.12, (c) `dryRun` inclusion/exclusion for open/close, (d) moving 2 docs error-catalog rows from POLICY to PERMANENT, and (e) re-routing `issueRunbooks[CIRCUIT_BREAKER_OPEN]` closes the surface.

5. **Docs-sync enforcement remains the highest-leverage systemic improvement.** Across iter5/iter6/iter7, the majority of Medium findings are docs-sync regressions from fixes that touched spec files but did not propagate to `docs/`. The iter4 feedback memo (`feedback_docs_sync_after_spec_changes.md`) enshrined the rule; iter7 still finds 6 violations (OBS-040/041, CMP-063, API-026, EXP-026/027 docs drifts; POL-034 already classified Low). Recommendation (out of scope for iter7 fix pass): add a spec↔docs drift-detection pass to `/fix-findings` itself or to a pre-commit hook.

6. **Per-perspective convergence is asymmetric.** 14 of 25 perspectives are at 0 Critical/High/Medium and stable across 2+ iterations (COM, WPL, DEL, STO, SCP, PRT, BLD, FMR, EXM, SES, MSG, MTI, NET, EXP). Remaining 11 either have iter7-introduced Mediums or long-lived Low carry-forwards. A targeted iter7 fix pass scoped to the 13 Mediums above is sufficient to converge; the Low carry-forwards are a separate, bounded, low-risk cleanup that need not block release.
