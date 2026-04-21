## 13. Compliance, Governance & Data Sovereignty

Scope: iter8 regressions-only (per `feedback_iter8_regressions_only.md`). Reviewing only surfaces touched by the iter7→iter8 fix commit `bed7961` that are CMP-relevant. Prior iter7 finding: CMP-063 (Medium, docs/spec misclassification of two residency fail-closed-mirror codes as POLICY rather than PERMANENT). Start ID: CMP-064.

### Regression verification for fix commit `bed7961`

CMP-relevant surfaces touched by the commit:

1. **`docs/reference/error-catalog.md` lines 118–120** — the three residency fail-closed-mirror codes relocated/grouped into the PERMANENT table:
   - `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` — already in PERMANENT table from iter6; unchanged in this fix.
   - `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` — moved from POLICY (prior line 174) to PERMANENT (now line 119). Verified HTTP 422 preserved, description unchanged (still self-identifies as "Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` / `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` for the legal-hold escrow surface (CMP-054)"), recommended-action text preserved verbatim, `LegalHoldEscrowResidencyViolation` critical-alert cross-reference preserved.
   - `PLATFORM_AUDIT_REGION_UNRESOLVABLE` — moved from POLICY (prior line 175) to PERMANENT (now line 120). Verified HTTP 422 preserved, description unchanged (still self-identifies as "Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` / `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` for the platform-tenant audit-write surface (CMP-058)"), recommended-action text preserved verbatim, `PlatformAuditResidencyViolation` critical-alert cross-reference preserved.
   - All three rows now appear consecutively in the PERMANENT table (lines 118→119→120) grouping the family as recommended by iter7/CMP-063. Grep across `docs/` (`_REGION_UNRESOLVABLE`) confirms no remaining entry for any of the three codes inside the POLICY table (current POLICY table spans lines 150–187 in the updated doc and contains none of the three). CMP-063 is verified **Fixed**.

2. **Cross-references in docs/spec to the three relocated codes.**
   - `docs/operator-guide/configuration.md:569` — the fail-closed-validation paragraph lists all five runtime fail-closed codes in flat enumeration ("`REGION_CONSTRAINT_UNRESOLVABLE`, `BACKUP_REGION_UNRESOLVABLE`, `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`, `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`, `PLATFORM_AUDIT_REGION_UNRESOLVABLE`") without implying retry semantics. Unchanged by the fix. The surrounding guidance ("Configure the missing … entry (or restore … reachability) and re-invoke …") is consistent with PERMANENT-category semantics (fix the deployment config and retry), not with POLICY semantics (grant/quota adjustment). No stale POLICY-mode retry guidance.
   - `docs/operator-guide/configuration.md:573` — the CMP-058 residency paragraph explicitly labels `PLATFORM_AUDIT_REGION_UNRESOLVABLE` as "HTTP 422, `PERMANENT`" and pairs it with the `PlatformAuditResidencyViolation` critical alert. Directly consistent with the relocated catalog row.
   - `docs/reference/metrics.md:336` — the `lenny_platform_audit_region_unresolvable_total` metric row is unchanged by this commit; its description is surface-neutral and does not encode retry semantics.
   - `spec/11_policy-and-controls.md:423`, `spec/11_policy-and-controls.md:430–431` — both spec lines describe the three-code family as the "fail-closed mirror" cluster and point back to the canonical catalog; they do not imply POLICY retry semantics and are unchanged.
   - `spec/15_external-api-surface.md:1041–1042` — the spec-side canonical table already carried `PERMANENT` for both codes (the iter6 API-022 fix). Still `PERMANENT` after `bed7961`; categorization now matches the docs catalog.
   - `spec/12_storage-architecture.md:883–885, 923` — Phase 3.5 and storage-routing descriptions reference the codes as fail-closed aborts; descriptions remain aligned with PERMANENT semantics (config fix and re-invoke). Unchanged.
   - `spec/16_observability.md:247–263, 424–427, 667, 680` — metric, alert, and audit-event cross-references are untouched by `bed7961` and remain consistent with PERMANENT semantics (critical-alert-immediately, non-retryable-as-is).
   - `spec/17_deployment-topology.md:503` — the install/upgrade preflight still rejects releases missing `legalHoldEscrow` entries. Unchanged.
   - `spec/25_agent-operability.md:1495–1500, 4337–4343, 4460` — MCP tool error surface and operator runbook references list all three codes consistently as PERMANENT; unchanged.
   - `spec/24_lenny-ctl-command-reference.md:129` — CLI reference unchanged.

   No cross-reference surface implies POLICY-style retry semantics (grant/quota/permission adjustment with potential retry-after-cooldown). All surfaces describe deployment-config remediation followed by re-invocation of the original operation — the canonical PERMANENT posture. **No regression.**

3. **`spec/15_external-api-surface.md` §15.1 two new PERMANENT rows** (added in iter7 fix):
   - `ELICITATION_CONTENT_TAMPERED` (PERMANENT/409) — paired with:
     - Audit event `elicitation.content_tamper_detected` at `spec/16_observability.md:664` under §16.7 with full payload (`elicitation_id`, `origin_pod`, `tampering_pod`, `session_id`, `tenant_id`, `user_id`, `delegation_depth`, `initiator_type`, `divergent_fields` field-names-only, `original_sha256`, `attempted_sha256`, `detected_at`); correctly noted as non-sampled (every tamper attempt is security-salient); written through the append-only audit path per §11.7.
     - Metric `lenny_elicitation_content_tamper_detected_total` referenced; labels `origin_pod` and `tampering_pod`.
     - Critical alert `ElicitationContentTamperDetected` at `spec/16_observability.md:434` with the firing expression `increase(lenny_elicitation_content_tamper_detected_total[5m]) > 0` and operator triage guidance.
     - Docs catalog row at `docs/reference/error-catalog.md:108` carries matching description and retry guidance ("To present transformed text to a different audience, emit a new `lenny/request_elicitation` … do not rewrite an existing one"). Retry-guidance text is consistent with PERMANENT (not-retryable-as-is; caller must issue a new operation rather than retry). No compliance/audit-trail gap.
   - `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` (PERMANENT/422) — paired with:
     - Admission webhook `lenny-ephemeral-container-cred-guard` inventoried as item 13 in `spec/17_deployment-topology.md:54–82` admission-policies; failurePolicy `Fail` per the rejection-when-webhook-down posture recorded on the `EphemeralContainerCredGuardUnavailable` warning alert.
     - Security-model cross-reference at `spec/13_security-model.md:27, 221` keyed to the `lenny-cred-readers` GID membership boundary.
     - Warning alert `EphemeralContainerCredGuardUnavailable` at `spec/16_observability.md:479` for webhook-down detection; the rejection itself is covered by the `PERMANENT`/422 error at the client surface (so no dedicated critical alert is needed — every rejection is an operator-visible client error, not a silent failure).
     - Docs catalog row at `docs/reference/error-catalog.md:123` carries the full `details.reason` sub-code vocabulary (`runAsUser_equals_adapter_uid`, `runAsUser_equals_agent_uid`, `cred_readers_gid_in_supplementalGroups`, `cred_readers_gid_in_runAsGroup`, `runAsUser_absent`, `runAsGroup_absent`, `supplementalGroups_absent`) and a concrete remediation ("Resubmit the ephemeral container with `securityContext.runAsUser`/`runAsGroup`/`supplementalGroups` explicitly set to values outside the adapter UID, agent UID, and the `lenny-cred-readers` GID").
     - Retry guidance is consistent with PERMANENT (not-retryable-as-is; caller must fix the submitted ephemeral-container `securityContext`). No residency/audit-trail gap for this code because the rejection blocks the ephemeral-container attach *before* any credential-file read takes place — there is no tenant data event to audit beyond the generic admission-controller decision record that Kubernetes emits.

   **No regression.** Both new iter7 codes carry appropriate audit/metric/alert coverage and their retry semantics are consistent with the PERMANENT category they are filed under.

### Convergence assessment (Perspective 13)

Under the iter8 regressions-only scope, the only new surfaces touched by `bed7961` that are CMP-relevant are:

1. the three residency fail-closed-mirror codes consolidated in the PERMANENT table of `docs/reference/error-catalog.md` (CMP-063 fix), and
2. the two new PERMANENT-table rows `ELICITATION_CONTENT_TAMPERED` and `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`.

Verification:

- The three residency-mirror codes now share a category (`PERMANENT`) and position in the same table adjacent to one another; no POLICY-table entries remain for them.
- HTTP status codes (422 for all three) and descriptions are preserved verbatim.
- Cross-references in docs (operator-guide, metrics) and spec (§11, §12, §15, §16, §17, §24, §25) do not imply POLICY-type retry behavior for any of the three codes. All consistently describe deployment-config remediation followed by re-invocation — the PERMANENT posture. This rules out the regression pattern the iter8 directive specifically flags.
- `ELICITATION_CONTENT_TAMPERED` has a paired `elicitation.content_tamper_detected` audit event in §16.7 with full forensic payload and a critical alert, and `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` is backed by an inventoried admission webhook and a webhook-down warning alert. Both new codes' compliance/audit-trail story is complete.

**No Critical/High/Medium regressions detected.**

Counts: **C=0 H=0 M=0 L=0 Info=0.**

**Converged: YES (for Perspective 13 under iter8 regressions-only scope).** CMP-063 (iter7 Medium) is verified Fixed. The two new iter7 error codes introduced regression-free under the compliance perspective. The iter6/iter7 carry-forward Low/Info items (CMP-059, CMP-060, CMP-061, CMP-062) are out of scope under the iter8 regressions-only directive and remain in their prior posture.

No regressions detected.
