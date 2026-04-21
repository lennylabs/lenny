## 13. Compliance, Governance & Data Sovereignty

(Iter6 findings below. IDs continue from iter5; highest iter5 ID is CMP-058.)

### Carry-forward verification from iter5

All three iter5 findings are verified **Fixed** and the implementation is end-to-end consistent across the spec and docs. Specific verification cross-references:

#### CMP-054 (High — per-region legal-hold escrow, fail-closed) — **Verified Fixed**

- §12.8 Phase 3.5 sub-step 2 (spec/12_storage-architecture.md line 883) resolves `dataResidencyRegion` into per-region `storage.regions.<region>.legalHoldEscrow.{endpoint, bucket, kmsKeyId, escrowKekId}` and rejects unresolvable regions with `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (HTTP 422, `PERMANENT`).
- Region-scoped escrow KEK identifier is `platform:legal_hold_escrow:<region>` (not a single global KEK); decrypt capability is jurisdiction-bounded.
- `legal_hold.escrow_region_resolved` INFO audit event records the residency decision per-override (spec/16_observability.md line 671).
- `LegalHoldEscrowResidencyViolation` critical alert (spec/16_observability.md line 422) and `lenny_legal_hold_escrow_region_unresolvable_total` counter (spec/16_observability.md line 243) are present.
- Helm install/upgrade preflight "Legal-hold escrow per-region coverage" (spec/17_deployment-topology.md line 490) fails the release if `features.compliance: true` and any declared region lacks a complete `legalHoldEscrow` entry — so the residency-bypass posture cannot survive chart render.
- Single-region default path via `storage.legalHoldEscrowDefault.*` + `platform:legal_hold_escrow:default` is preserved with no dual-mode code path (consistent with feedback_no_backward_compat.md — the single-region case is the same fail-closed flow with an implicit one-region map).

#### CMP-057 (High — `complianceProfile` downgrade ratchet) — **Verified Fixed**

- §11.7 "Compliance profile downgrade ratchet" (spec/11_policy-and-controls.md lines 443–449) enforces the strict one-way `none < soc2 < fedramp < hipaa` ordering on the generic `PUT /v1/admin/tenants/{id}` surface with `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` (HTTP 422, `POLICY`; spec/15_external-api-surface.md line 1065).
- Dedicated decommission endpoint `POST /v1/admin/tenants/{id}/compliance-profile/decommission` (spec/15_external-api-surface.md line 865) requires `platform-admin` scope (tenant-admin explicitly rejected), `acknowledgeDataRemediation: true`, non-empty `justification`, at least one `remediationAttestations` entry, `previousProfile` concurrency guard equal to the current profile, and `targetProfile` strictly lower in the ratchet order (same-profile rejected with `VALIDATION_ERROR`).
- §11.7 line 441 "Gateway startup with a regulated tenant and no SIEM" error message correctly points operators at the decommission endpoint rather than the generic PUT (which would be rejected). Operational remediation guidance is consistent with the ratchet.
- `compliance.profile_decommissioned` critical audit event (spec/16_observability.md line 659) records `previous_profile`, `target_profile`, `operator_sub`, `operator_tenant_id`, `justification`, `remediation_attestations`, `acknowledged_data_remediation: true`, and `decommissioned_at`. Retained under `audit.gdprRetentionDays`.
- `CompliancePostureDecommissioned` warning alert (spec/16_observability.md line 483) fires per-event — symmetric with `LegalHoldOverrideUsedTenant`.
- §25.11 MCP tool `lenny_tenant_compliance_profile_decommission` (spec/25_agent-operability.md line 4459) documents the attested-wind-down semantics and explicitly names the generic `lenny_tenant_update` rejection path, closing the agent operability gap.
- No dual-mode legacy path: the generic `PUT` surface does not retain a flag-gated downgrade escape; the decommission endpoint is the sole legitimate downgrade path (consistent with feedback_no_backward_compat.md).

#### CMP-058 (Medium — platform-tenant audit event residency) — **Verified Fixed**

- §11.7 "Platform-tenant audit event residency" (spec/11_policy-and-controls.md lines 420–425) specifies the three-tier routing rule: (1) target has `dataResidencyRegion` + region declared → `PlatformPostgres(region)`; (2) no residency set (or tombstone snapshot lost) → global `PlatformPostgres()`; (3) residency set but region unresolvable → fail-closed with `PLATFORM_AUDIT_REGION_UNRESOLVABLE`.
- §25.11 Storage Routing (spec/25_agent-operability.md line 1495) documents the `PlatformPostgres(region)` method with its `storage.regions.<region>.postgresEndpoint` binding — the same map used for runtime writes, backups, and legal-hold escrow, so there is one multi-region topology story.
- §15.4 error catalog includes `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (HTTP 422, `POLICY`; spec/15_external-api-surface.md line 1037).
- `PlatformAuditResidencyViolation` critical alert (spec/16_observability.md line 423) and `lenny_platform_audit_region_unresolvable_total{region, failure_mode}` counter (spec/16_observability.md line 244, and §16.8 line 688 listing).
- §11.7 line 423 explicitly states the originating operation (impersonation, Phase 3.5 ledger write, decommission) halts on residency failure because "audit must be durable before any externally observable side effect" — impersonation ticket issuance fails, Phase 3.5 stalls in `deleting`, and "decommission is rolled back". The atomicity with the tenant record update is called out in §11.7 line 449 ("updates the tenant record atomically").
- The `DataResidencyViolationAttempt` emission path correctly handles the self-reference problem: "if the target region is the source of the failure, the violation falls back to the global `PlatformPostgres()` so the incident does not disappear into the unreachable region" (spec/11_policy-and-controls.md line 423 final sentence). This is a genuinely subtle correctness property and the spec calls it out explicitly.
- §12.8 Phase 3.5 sub-step 4 (spec/12_storage-architecture.md line 885) co-locates the ledger-write residency gate with the bucket/KEK residency gate so a deployment with `legalHoldEscrow` configured but `postgresEndpoint` missing fails fast on the ledger write with a distinct error code, not silently after writing orphaned escrow ciphertext.
- Docs synced (error-catalog.md, metrics.md, observability.md, security.md, configuration.md, admin.md) — spot-checked, all carry the new error code, counter, and alert row.

### CMP-059. Decommission endpoint description should note that audit-event emission is part of the transaction, not a post-commit side-effect [Low]

**Section:** spec/15_external-api-surface.md §15.1 `POST /v1/admin/tenants/{id}/compliance-profile/decommission` (line 865); spec/11_policy-and-controls.md §11.7 "Compliance profile downgrade ratchet" — "Legitimate wind-down path" (line 449)

§11.7 line 449 states the endpoint "emits the critical `compliance.profile_decommissioned` audit event…and updates the tenant record atomically." §11.7 line 423 (CMP-058 residency rule) asserts on the cross-section correctness side that "decommission is rolled back" when the audit write fails residency resolution. These two statements jointly imply the tenant record update and the audit write are in a single transaction (otherwise a rollback of only the tenant record would leave an orphan decommission-pending audit write, or a rollback of only the audit write would leave the tenant's profile lowered with no record of the transition — the exact gap the ratchet is designed to prevent).

The §15.1 endpoint row (line 865), however, says only "updates the tenant record atomically. Emits the critical `compliance.profile_decommissioned` audit event" — the word "atomically" attaches to the tenant-record update and does not explicitly state the audit write is part of the same commit. A reader of §15.1 in isolation might (incorrectly) infer the audit event is a post-commit fire-and-forget, in which case the rollback semantics of CMP-058 line 423 would not hold. This is a documentation gap, not a behavioral gap — §11.7 is authoritative and the correct behavior is specified there.

**Recommendation:** Append one clause to the §15.1 endpoint row making the atomicity scope explicit: "The tenant-record update and the `compliance.profile_decommissioned` audit write are committed in a single transaction; if the audit write fails residency resolution (`PLATFORM_AUDIT_REGION_UNRESOLVABLE`, see [§11.7](11_policy-and-controls.md#117-audit-logging) 'Platform-tenant audit event residency'), the tenant-record update is rolled back and the decommission has no observable effect." Matches the `gdpr.legal_hold_overridden_tenant` / Phase 3.5 documentation style where the halt-on-audit-failure semantics are co-located with the endpoint.

### CMP-060. `audit.gdprRetentionDays` floor drop at decommission is not a partition-level purge, but the ratchet rationale implies it might be; clarify [Low]

**Section:** spec/11_policy-and-controls.md §11.7 "Compliance profile downgrade ratchet" rationale bullet 2 (line 446)

The ratchet rationale says: "The `audit.gdprRetentionDays` floor of 2190 days that applies under any regulated profile is removed on downgrade, so future retention pruning would silently delete rows the regulated profile would have retained." A careful reader parses this correctly — "future retention pruning" means a subsequent operator action that lowers `audit.gdprRetentionDays` from its current value (≥ 2190) to a lower value — but a first-pass reader could plausibly infer that the decommission endpoint *itself* drops the floor on existing `gdpr.*` rows as of the decommission moment, which is not the case.

In fact: (a) the audit GC uses partition-time retention, not per-row compliance-profile tagging; (b) `gdpr.*` rows continue to be protected by whatever `audit.gdprRetentionDays` value is currently in effect; (c) the only way retention is actually lowered for existing rows is if the deployer subsequently reduces `audit.gdprRetentionDays`. The decommission endpoint by itself does not prune any existing row — it merely relaxes the startup-validation constraint that required `audit.gdprRetentionDays >= 2190`.

Once the constraint is relaxed, a subsequent `helm upgrade` lowering `audit.gdprRetentionDays` would succeed (where under a regulated profile the chart would have rejected the value). A defense-in-depth improvement would be to preserve a `auditGdprRetentionFloor` snapshot at decommission time equal to the floor the tenant operated under, so that the deployer's later retention lowering is measured against the snapshot rather than against the (now-none) profile. This is a strictly additive hardening and is not required for the iter5 fix to be correct; it is a Low-severity clarity / defense-in-depth finding.

**Recommendation:** Rewrite bullet 2 in §11.7 line 446 to read: "The `audit.gdprRetentionDays` startup-validation floor of 2190 days, which under any regulated profile prevented the deployer from configuring a lower retention, is removed on downgrade. The floor is a config-time gate, not a per-row tag — existing `gdpr.*` rows remain protected by whatever `audit.gdprRetentionDays` value is currently configured. After decommission, a subsequent Helm upgrade lowering `audit.gdprRetentionDays` would become admissible and would silently shorten retention for rows written under the prior regulated posture. Deployers winding down a regulated profile SHOULD retain `audit.gdprRetentionDays` at ≥ 2190 for the regulatory lookback period following the decommission (e.g., 7 years for HIPAA §164.312(b)) even though the config-time gate is relaxed." Optionally, track a `pre_decommission_gdpr_retention_days_floor` attribute in the `compliance.profile_decommissioned` event payload to give auditors a single-row record of what the floor was at wind-down time.

### CMP-061. Per-region `legal_hold_escrow_kek` rotation policy is stated ("same as the platform audit signing key") without a corresponding per-region rotation audit event type [Low]

**Section:** spec/12_storage-architecture.md §12.8 Phase 3.5 sub-step 2 (line 883 — "Rotation of each regional escrow KEK follows the same KMS rotation policy as the platform audit signing key.")

The spec ties rotation of each regional `legal_hold_escrow_kek` to the platform audit signing key's rotation policy by reference. The platform audit signing key rotation is already covered in §11.7 audit-chain machinery, and `audit_sig.key_rotated` (or similar; the spec uses the `platform.config_changed` / `platform.registry_updated` path) is emitted on signing-key rotation. For the escrow KEK, however, there is no corresponding per-region rotation event — on a fleet with five regions each holding its own escrow KEK, a compliance reviewer cannot observe from the audit trail that rotation actually occurred region-by-region and at which cadence. This is asymmetric with `artifact.cross_region_replication_verified` (CMP-053), which emits a per-batch positive residency attestation precisely so that residency-at-write-time is independently observable per region.

The impact is marginal: the regional escrow KEK's lifecycle is directly consequential only during a force-delete-override flow, which is already a high-signal audited event (`gdpr.legal_hold_overridden_tenant` records `escrow_kek_id` and `escrow_region`). A stale / missing rotation is most visibly caught at rotation time by KMS-side alerting (cloud providers typically surface key rotation failures independently). This finding is Low / Info severity under the iter5 severity calibration: it is a documentation / operability completeness gap on an audit surface, not a policy-enforcement gap.

**Recommendation:** Document in §12.8 Phase 3.5 sub-step 2 (or as a small paragraph immediately after) that regional escrow KEK rotation is surfaced through the existing `platform.config_changed` event with `subsystem: "legal_hold_escrow_kek"`, `region`, `old_key_id`, `new_key_id`, `rotated_at`, `rotation_trigger` (`scheduled` | `manual` | `compromise_response`). Alternatively introduce a dedicated `legal_hold.escrow_kek_rotated` audit event mirroring the granularity of `legal_hold.escrow_region_resolved`. Either form is consistent with the CMP-053 positive-attestation pattern and closes the regional-KEK observability gap.

### CMP-062. `dataResidencyRegion` field persistence across decommission is unstated; operators can reasonably infer that residency survives but the spec is silent [Info]

**Section:** spec/15_external-api-surface.md §15.1 `POST /v1/admin/tenants/{id}/compliance-profile/decommission` (line 865); spec/12_storage-architecture.md §12.8 "Data residency" (line 911)

The decommission endpoint description specifies the fields it updates (`complianceProfile`) and the fields it consumes (`previousProfile`, `targetProfile`, `acknowledgeDataRemediation`, `justification`, `remediationAttestations`); it is silent on any other tenant field. The reasonable implementation is that `dataResidencyRegion` is a separate field, independent of `complianceProfile`, and therefore survives decommission unchanged — a HIPAA tenant decommissioned to `none` retains its `eu-west-1` residency, all EU-region runtime / backup / audit rules continue to apply, and a later `PUT /v1/admin/tenants/{id}` that tries to null-out `dataResidencyRegion` is a separate field-level operation not governed by this endpoint. This is the correct behavior and matches the implicit "only `complianceProfile` is touched" semantics of the endpoint.

The spec does not explicitly state this, however. A reader who infers incorrectly (that decommission is a "reset all compliance-related fields to defaults" operation) might expect `dataResidencyRegion` to also be cleared, which would be a silent cross-border transfer authorization. This is a documentation clarity gap, not a behavioral gap. Severity: Info.

**Recommendation:** Add a single sentence to the §15.1 endpoint row (line 865) or §11.7 wind-down paragraph (line 449): "Decommission does not modify other tenant fields — `dataResidencyRegion`, `workspaceTier`, and `billingErasurePolicy` are unchanged. In particular, an EU-residency tenant remains EU-resident after decommission; the jurisdiction of ingested data cannot be relaxed by lowering the compliance profile." This makes the field-scope explicit and forecloses the misreading.

### Convergence assessment (Perspective 13)

All three iter5 High/Medium findings (CMP-054, CMP-057, CMP-058) are verified **Fixed** with end-to-end implementation across §11.7 / §12.8 / §15.1 / §15.4 / §16.5 / §16.7 / §17.8 / §25.11, and the docs/ tree is synced. The iter5 fixes preserve the feedback_no_backward_compat.md invariant (no dual-mode paths, no legacy flags): the decommission endpoint is the sole legitimate downgrade path, the generic `PUT` surface rejects; legal-hold escrow has one flow that is fail-closed for unresolvable regions in both multi-region and single-region deployments; platform-tenant audit residency is a single routing rule keyed on `target_tenant_id` + `dataResidencyRegion`, not an opt-in mode.

Iter6 identifies three minor cleanup items:

- **CMP-059** (Low): §15.1 endpoint row should make the single-transaction atomicity of the tenant record update + audit write explicit (§11.7 already states it; §15.1 is slightly vague).
- **CMP-060** (Low): §11.7 ratchet rationale bullet 2 should clarify that the retention floor is a config-time gate (not a per-row tag), and that deployers winding down a regulated posture should voluntarily retain `audit.gdprRetentionDays >= 2190` during the regulatory lookback period.
- **CMP-061** (Low): Regional `legal_hold_escrow_kek` rotation is declared by reference to the platform audit signing key rotation; a per-region rotation audit event (or a documented `platform.config_changed` sub-type) would close the observability symmetry with CMP-053's positive attestation pattern.
- **CMP-062** (Info): Decommission endpoint row should explicitly state that `dataResidencyRegion`, `workspaceTier`, and `billingErasurePolicy` are unchanged — forecloses an incorrect "reset all compliance fields" reading.

None of these four are genuine policy-enforcement gaps; all are documentation / audit-symmetry completeness items on surfaces whose authoritative behavior is already specified elsewhere in the spec and is correct.

Counts: **C=0 H=0 M=0 L=3 Info=1.**

**Converged: YES** — all iter5 compliance findings are Fixed and verified end-to-end. The four iter6 cleanup items are strictly documentation-clarity or audit-symmetry improvements at Low / Info severity that do not block deployment. Under the iter5 severity calibration (feedback_severity_calibration_iter5.md, default Low when ambiguous), they properly land below the convergence threshold.
