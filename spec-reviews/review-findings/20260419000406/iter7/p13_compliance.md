## 13. Compliance, Governance & Data Sovereignty

(Iter7 findings below. IDs continue from iter6; highest iter6 ID is CMP-062.)

### Carry-forward verification from iter6

Iter6 contained **zero Critical/High/Medium** compliance findings (all three iter5 items — CMP-054, CMP-057, CMP-058 — were verified Fixed in iter6). Iter6 also identified four minor cleanup items (CMP-059 Low, CMP-060 Low, CMP-061 Low, CMP-062 Info); none of these was marked as a fix target for iter6 so they carry forward unchanged into iter7 unless addressed by the iter6→iter7 fix commit.

Verification of the iter6→iter7 fix window (commit `8604ce9` "Fix iteration 6: applied fixes for Critical/High/Medium findings + docs sync"):

- **CMP-054 (High — per-region legal-hold escrow, fail-closed)**: Still verified Fixed — `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` remains classified as `PERMANENT`/422 in `spec/15_external-api-surface.md` line 1041, the `legal_hold.escrow_region_resolved` audit event is unchanged at `spec/16_observability.md` line 671, and the Helm install/upgrade preflight in `spec/17_deployment-topology.md` line 490 is unchanged. No regression.
- **CMP-057 (High — `complianceProfile` downgrade ratchet)**: Still verified Fixed — `spec/11_policy-and-controls.md` §11.7 lines 443–449 retain the strict `none < soc2 < fedramp < hipaa` ordering, the dedicated decommission endpoint at `spec/15_external-api-surface.md` line 865 still requires `platform-admin` / `acknowledgeDataRemediation` / `remediationAttestations`, and `spec/25_agent-operability.md` line 4459 still documents the MCP tool rejection path. No regression.
- **CMP-058 (Medium — platform-tenant audit event residency)**: Still verified Fixed for the *spec* side — `spec/11_policy-and-controls.md` line 423 and `spec/12_storage-architecture.md` lines 882–885 co-locate residency gating with the ledger write. **However** the iter6 API-022 fix (which changed `PLATFORM_AUDIT_REGION_UNRESOLVABLE` category from `POLICY` to `PERMANENT` to match sibling residency-mirror codes — see iter6/summary.md and the iter6 API-022 recommendation "Update `docs/reference/error-catalog.md:173` correspondingly") did **not** propagate to `docs/reference/error-catalog.md`. This produces a docs/spec inconsistency for both `PLATFORM_AUDIT_REGION_UNRESOLVABLE` and the pre-existing `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` — see new finding **CMP-063** below. The spec-side classification is authoritative and correct, so this is a docs-sync / client-contract consistency gap rather than a runtime correctness gap.

- **CMP-059 (Low), CMP-060 (Low), CMP-061 (Low), CMP-062 (Info)**: Not included in the iter6 fix scope. Re-examination confirms no material change in the referenced passages:
  - CMP-059: `spec/15_external-api-surface.md` line 865 endpoint row still reads "updates the tenant record atomically. Emits the critical `compliance.profile_decommissioned` audit event" without an explicit single-transaction clause covering both the tenant record and the audit write. Unchanged.
  - CMP-060: `spec/11_policy-and-controls.md` §11.7 rationale bullet 2 (line 446) is unchanged; the "config-time gate vs per-row tag" clarification is still absent.
  - CMP-061: `spec/12_storage-architecture.md` §12.8 Phase 3.5 sub-step 2 (line 883) still ties regional `legal_hold_escrow_kek` rotation to the platform audit signing key by reference with no dedicated per-region rotation audit event type.
  - CMP-062: `spec/15_external-api-surface.md` line 865 endpoint row is still silent on `dataResidencyRegion` / `workspaceTier` / `billingErasurePolicy` persistence across decommission.

All four iter6 carry-forwards retain their original severity calibration (Low / Info). They are neither regressed nor fixed; they remain below the convergence threshold.

### CMP-063. `docs/reference/error-catalog.md` misclassifies residency fail-closed codes `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` and `PLATFORM_AUDIT_REGION_UNRESOLVABLE` under POLICY while the spec classifies both as PERMANENT [Medium]

**Section:** `docs/reference/error-catalog.md` lines 174–175 (inside the POLICY errors table starting at line 146); cross-reference `spec/15_external-api-surface.md` lines 1041–1042, `spec/11_policy-and-controls.md` line 423, `spec/12_storage-architecture.md` lines 882–885, `spec/25_agent-operability.md` line 4339, `docs/operator-guide/configuration.md` line 541, `docs/reference/metrics.md` lines 335–336.

**What the spec says.** The spec classifies both residency fail-closed-mirror codes as `PERMANENT`/422:

- `spec/15_external-api-surface.md` line 1041: `| LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE | 422 | PERMANENT | ... |`
- `spec/15_external-api-surface.md` line 1042: `| PLATFORM_AUDIT_REGION_UNRESOLVABLE | 422 | PERMANENT | ... |`
- `spec/11_policy-and-controls.md` line 423: describes `PLATFORM_AUDIT_REGION_UNRESOLVABLE` as a fail-closed residency mirror of `BACKUP_REGION_UNRESOLVABLE` / `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`, all three of which are classified `PERMANENT` on the canonical error-code table.
- `spec/25_agent-operability.md` line 4339: the MCP tool error surface lists `PLATFORM_AUDIT_REGION_UNRESOLVABLE` as `PERMANENT`.
- The sibling residency-mirror code `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` is correctly located in the PERMANENT table in the same docs file (`docs/reference/error-catalog.md` line 117), so the established convention in the docs is "all three residency fail-closed codes live under PERMANENT." Only the legal-hold and platform-audit entries are in the wrong table.

**What the docs say.** Both codes appear in the POLICY errors table:

- `docs/reference/error-catalog.md` line 174: `| LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE | 422 | Phase 3.5 of the tenant force-delete lifecycle aborted... Fail-closed mirror of BACKUP_REGION_UNRESOLVABLE / ARTIFACT_REPLICATION_REGION_UNRESOLVABLE for the legal-hold escrow surface (CMP-054). |` — located under the "## POLICY errors" heading (line 146).
- `docs/reference/error-catalog.md` line 175: `| PLATFORM_AUDIT_REGION_UNRESOLVABLE | 422 | A platform-tenant audit event... Fail-closed mirror of BACKUP_REGION_UNRESOLVABLE / LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE for the platform-tenant audit-write surface (CMP-058)... |` — also under the POLICY heading.

The description text of both rows in the docs file explicitly self-identifies them as "fail-closed mirror" codes of `BACKUP_REGION_UNRESOLVABLE` and `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` — both of which are in the PERMANENT section — so the intended family placement is unambiguous and the rows are miscategorized against the text they themselves print.

**Iter6 fix intent.** The iter6 API-022 finding explicitly changed `PLATFORM_AUDIT_REGION_UNRESOLVABLE` from `POLICY` to `PERMANENT` in the spec and recommended (per `iter6/summary.md` API-022 entry): "Update `docs/reference/error-catalog.md:173` correspondingly." Commit `8604ce9` ("Fix iteration 6: applied fixes for Critical/High/Medium findings + docs sync") made the spec change but did not move the `docs/reference/error-catalog.md` row into the PERMANENT section. A follow-on effect is that `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` — which was already `PERMANENT` in the spec in iter5 — has been miscategorized in docs since CMP-054 landed and was not caught because it sits next to the platform-audit row.

**Compliance-side impact.**

1. **Client contract consistency.** The docs catalog is the canonical reference for MCP and REST clients building error-handling logic. A client that keys retry / escalation decisions on the category column ("POLICY errors require configuration changes, quota adjustments, or permission grants") — which is what the POLICY section heading tells them — would treat a residency-resolution failure as a policy-grant problem rather than a permanent deployment misconfiguration. The two categories carry different operator-remediation semantics (POLICY implies grant/quota/permission, PERMANENT implies "fix the request or the deployment config and retry"), and fail-closed residency codes are definitionally deployment-config failures, not policy-grant failures. Tenant-admin dashboards that filter on category will silently route these incidents to the wrong operator queue.

2. **Docs-vs-spec authority.** Under `feedback_docs_sync_after_spec_changes.md`, the iter6 fix's obligation to synchronize docs was not completed for this exact row. The iter6 review-fix iteration declared convergence on the spec-side API-022 change without reconciling the corresponding docs change. This is the kind of partial-fix residue that the docs-sync feedback is designed to prevent: the spec is correct, the behavior at runtime is correct, but the client-facing contract surface lies about the category.

3. **Compliance observability.** `LegalHoldEscrowResidencyViolation` and `PlatformAuditResidencyViolation` are both *critical* alerts (spec/16_observability.md lines 422–423). A runbook that says "on critical residency alert, check the error-catalog category to classify the incident type" would route the on-call to the wrong playbook chapter. Minor but real operator-experience friction on a compliance-critical path.

**Why Medium (not Low).** Under the iter5 severity calibration (`feedback_severity_calibration_iter5.md`), this would normally default to Low — it is a documentation inconsistency on a surface whose authoritative behavior is correctly specified in the spec, not a runtime correctness gap. I calibrate **Medium** because (a) the docs file is the canonical client contract and the inconsistency is on a compliance-critical residency code family (not a peripheral surface); (b) it is the specific unreconciled remainder of an iter6 fix that the iter6 recommendation explicitly named — so it is a broken fix, not a new gap, and the review-fix process should close it deterministically; (c) the precedent in iter6 (DOC-024/025 carrying similar docs/spec category inconsistencies across the residency family were classified Medium). If calibrated Low the overall finding count stays under threshold; if calibrated Medium it does not, so I am flagging this explicitly for fix.

**Recommendation.** Move both rows from the POLICY errors table to the PERMANENT errors table in `docs/reference/error-catalog.md`:

1. Remove lines 174 (`LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`) and 175 (`PLATFORM_AUDIT_REGION_UNRESOLVABLE`) from the table under the `## POLICY errors` heading (line 146).
2. Insert both rows in the `## PERMANENT errors` table (line 63), adjacent to the existing `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` row (line 117). Preserve each row's description and recommended-action text verbatim — only the table placement changes. The three residency-mirror codes should appear consecutively under PERMANENT so that the table explicitly groups the fail-closed family.
3. In the commit message, note that this is the residue of the iter6 API-022 fix's docs-sync obligation (per `iter6/summary.md` recommendation "Update `docs/reference/error-catalog.md:173` correspondingly") plus the pre-existing misplacement of `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (unreconciled since CMP-054 landed).

No spec change is required — the spec is already correct. No metrics or alert changes are required — `lenny_legal_hold_escrow_region_unresolvable_total` and `lenny_platform_audit_region_unresolvable_total` already live in `docs/reference/metrics.md` lines 335–336 and their dimensions are unchanged. No configuration change is required — `docs/operator-guide/configuration.md` line 541 already uses `PERMANENT` for `PLATFORM_AUDIT_REGION_UNRESOLVABLE`. Only the client-facing catalog table needs the move.

### Convergence assessment (Perspective 13)

Iter6 declared convergence on the compliance perspective with all iter5 High/Medium findings (CMP-054, CMP-057, CMP-058) verified Fixed and four minor cleanup items (CMP-059 Low, CMP-060 Low, CMP-061 Low, CMP-062 Info) below the convergence threshold.

Iter7 re-verification confirms the spec-side fixes remain intact and no regression occurred on any iter5 or iter6 carry-forward item. The iter6 fix window introduced **one new Medium finding** (CMP-063): the iter6 API-022 category correction for `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (`POLICY` → `PERMANENT`) did not propagate to `docs/reference/error-catalog.md`, and the pre-existing misplacement of `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` in the same POLICY section (unreconciled since iter5 CMP-054 landed) surfaces in the same row cluster. Both residency fail-closed-mirror codes must be relocated to the PERMANENT errors table to match the spec and the sibling `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` row.

Counts: **C=0 H=0 M=1 L=3 Info=1.**

**Converged: NO** — CMP-063 (Medium) blocks convergence under the compliance perspective. The fix is mechanical (move two rows between tables in one docs file; the spec is already authoritative and correct) and should land in the iter7→iter8 fix window. Once CMP-063 is resolved, the perspective returns to the iter6 posture of zero blocking findings with only Low/Info documentation-clarity items outstanding.
