# Perspective 18 — Content, Data Formats & Schema Design (Iter8 regressions-only)

Scope per iter8 directive (`feedback_iter8_regressions_only.md`): review ONLY regressions introduced by the iter7 fix commit `bed7961`. Prior CNT IDs end at CNT-029; new IDs start at CNT-030. Only Critical/High/Medium flagged.

Procedure executed:

1. `git show bed7961` reviewed end-to-end.
2. CNT-relevant surfaces verified:
   - **DOC-032 fix** — `spec/14_workspace-plan-schema.md:104` anchor corrected `#141-extensibility-rules → #141-workspaceplan-schema-versioning`. Target heading `### 14.1 WorkspacePlan Schema Versioning` exists at line 306. **Anchor resolves.** ✓
   - **DOC-031 fix** — `spec/16_observability.md:204` anchor corrected `#124-quota-and-rate-limiting → #124-redis-ha-and-failure-modes`. Target heading `### 12.4 Redis HA and Failure Modes` exists at `spec/12_storage-architecture.md:173`. **Anchor resolves.** ✓
   - No residual occurrences of either originally-broken anchor anywhere in `spec/`, `docs/`, or `spec-reviews/`.
3. Webhook-count references (8 → 9) confirmed consistent at `spec/13_security-model.md:221` (NetworkPolicy row — "nine webhook Deployments … all nine Deployments … all nine webhooks … other eight webhooks"), `spec/17_deployment-topology.md:56` (HA contract), `spec/17_deployment-topology.md:60,68,84` (feature-gated chart inventory narrative). **Consistent at every call site.** ✓
4. Error-catalog reconciliation — spec `15_external-api-surface.md:1079/1091` rows `ELICITATION_CONTENT_TAMPERED` and `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` mirror `docs/reference/error-catalog.md:108,123` — same codes, same sub-codes. ✓
5. Admission-policy list — `docs/operator-guide/namespace-and-isolation.md:102` item 8 `lenny-ephemeral-container-cred-guard` aligns with `spec/17_deployment-topology.md:54` item 13 (same description scope). ✓
6. MCP tool inventory — the four new rows in §25.12 (`lenny_circuit_breaker_list/get/open/close`) appear exactly once each under Read Tools (lines 4426–4427) and Action Tools (4474–4475). No duplicates. ✓
7. `circuit_breaker` scope-taxonomy domain is present in the closed list at both `spec/15_external-api-surface.md:915` and `spec/25_agent-operability.md:218`. ✓

---

## New findings

### CNT-030. §17.9 admission-webhook inventory description lists only 4 baseline entries; §17.2 lists 5 [Medium]

**Section:** 17.9 (`spec/17_deployment-topology.md:513`, `Admission webhook inventory` preflight-check row) vs. 17.2 (`spec/17_deployment-topology.md:54,68,82`, item 13 + baseline narrative).

The iter7 SEC-017 fix promoted `lenny-ephemeral-container-cred-guard` into the always-rendered baseline admission-webhook set at §17.2 (item 13, lines 54/68/82). §17.2 now uniformly describes the baseline as **five** entries:

- Line 68: "baseline five ∪ `{lenny-direct-mode-isolation}` if `features.llmProxy` ∪ … a Phase 3.5 install (all three flags `false`) expects exactly the five baseline entries"
- Line 82: "The five baseline webhooks (`lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`) have no feature-flag gate…"
- Line 84: "(1) all flags `false` (Phase 3.5 baseline: **five** entries expected)"

However, the §17.9 `lenny-preflight` `Admission webhook inventory` check description at `spec/17_deployment-topology.md:513` still enumerates the baseline as **four** entries (three validating webhooks plus the conversion webhook):

> …baseline entries `lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, and the `lenny-crd-conversion` conversion webhook for each Lenny CRD are always expected; `lenny-direct-mode-isolation` is added iff `features.llmProxy`…

`lenny-ephemeral-container-cred-guard` is missing from the explicit baseline enumeration in the §17.9 cell that describes what the `lenny-preflight` Job's expected set contains. This contradicts the narrative at §17.2 line 68 (same file, same fix commit) which declares the Phase-3.5 expected set has five entries, and directly contradicts line 84's integration-test parameterisation ("all flags `false` … five entries expected").

This is a load-bearing description because §17.9 is the operator-guide-level reference for preflight check semantics — an operator reading the row to understand what the preflight Job checks will see a different (narrower) baseline than the §17.2 chart-inventory narrative prescribes. It is the same class of content-consistency regression as iter6-DOC-024/025 and iter7-DOC-031/032: a fix pass amended one section of a multi-section invariant but left sibling mirrors stale.

Severity **Medium**, calibrated against the iter7 rubric:

- Iter7 DOC-031 (broken anchor at §16.5 pointing to a non-existent §12.4 sub-anchor) was Medium — a cross-file reference that a reader resolves by jumping to the wrong subsection.
- Iter7 DOC-032 (broken anchor at §14 pointing to a non-existent §14.1 sub-anchor) was Medium — same class.
- This finding is slightly stronger than both because the mismatched baseline count is not just a broken link; it's a **substantive count/enum mismatch in normative prose** that, if relied on by a chart author to implement the preflight check, would produce a narrower expected set than the paragraphs at §17.2 require. The implementation gap is real, not cosmetic.

**Recommendation:** Edit `spec/17_deployment-topology.md:513` to add `lenny-ephemeral-container-cred-guard` to the baseline enumeration and align the count language. Minimal change:

> …baseline entries `lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, the `lenny-crd-conversion` conversion webhook for each Lenny CRD, **and `lenny-ephemeral-container-cred-guard`** are always expected; `lenny-direct-mode-isolation` is added iff `features.llmProxy`, `lenny-drain-readiness` iff `features.drainReadiness`, and `lenny-data-residency-validator` + `lenny-t4-node-isolation` iff `features.compliance`.

Verifiable post-fix: `grep -n "baseline entries \`lenny-" spec/17_deployment-topology.md` should return a single enumeration including `lenny-ephemeral-container-cred-guard` at position 5, matching the §17.2 line-68/82 baseline-set prose.

### CNT-031. Docs cross-reference to `docs/runbooks/admission-plane-feature-flag-downgrade.md` / `.html` is a broken link — runbook file was never created [Medium]

**Section:** `spec/17_deployment-topology.md:80` (§17.2 "Feature-flag downgrade enforcement" layer 4 — `AdmissionPlaneFeatureFlagDowngrade` Warning alert narrative) and `spec/17_deployment-topology.md:201` (§17.7 "Admission-plane feature-flag downgrade" runbook stub) and `docs/operator-guide/observability.md:188` (docs row for `AdmissionPlaneFeatureFlagDowngrade`).

The iter7 KIN-028 fix introduced the `AdmissionPlaneFeatureFlagDowngrade` Warning alert with a paired runbook cross-reference at three sites:

- `spec/17_deployment-topology.md:80` — "Cross-references the `docs/runbooks/admission-plane-feature-flag-downgrade.md` runbook ([§17.7](#177-operational-runbooks))…"
- `spec/17_deployment-topology.md:201` — "**Admission-plane feature-flag downgrade** (`docs/runbooks/admission-plane-feature-flag-downgrade.md`)" in the §17.7 runbook stubs list.
- `docs/operator-guide/observability.md:188` — "Follow [admission-plane-feature-flag-downgrade](../runbooks/admission-plane-feature-flag-downgrade.html)…"

The referenced file `docs/runbooks/admission-plane-feature-flag-downgrade.md` **was not created** in commit `bed7961`. `ls docs/runbooks/admission-plane-feature-flag-downgrade.md` returns `No such file or directory`, and `grep -l "admission-plane-feature-flag-downgrade" docs/runbooks/` returns no matches (not even `index.md`).

Companion check — `docs/runbooks/index.md` was also not updated with the three new alerts from iter7:

- `ElicitationContentTamperDetected` (Critical, new) — not in index
- `EphemeralContainerCredGuardUnavailable` (Warning, new) — not in index
- `AdmissionPlaneFeatureFlagDowngrade` (Warning, new) — not in index

But the index omission is a secondary symptom. The primary regression is the three spec/docs cross-references pointing to a non-existent file.

Severity **Medium**, calibrated against the iter7 rubric:

- Iter7 DOC-031/DOC-032 (both Medium) were broken intra-spec anchors where the target section existed but the fragment was wrong. This finding is **worse** in kind: the target file does not exist at all, so the `docs/operator-guide/observability.md` link is 100% dead (not just a fragment offset — a full 404), and the spec's prose-level reference is purely aspirational.
- The `feedback_docs_sync_after_spec_changes.md` instruction specifically says "reconcile docs/ with spec changes after each review-fix iteration before declaring convergence." Adding a runbook reference in the spec without materializing the runbook file is a direct violation.

**Recommendation:** One of two resolutions:

1. **Create the runbook stub.** Add `docs/runbooks/admission-plane-feature-flag-downgrade.md` using the spec §17.7 stub at line 201 as the authoritative source. The *Trigger*, *Diagnosis*, *Remediation* structure already exists in spec prose — it only needs to be extracted into a standalone `.md` file with the three-part heading pattern used by sibling runbooks (e.g., `docs/runbooks/legal-hold-quota-pressure.md`). Also append entries to `docs/runbooks/index.md`:
   - Under "Warning alerts": `| AdmissionPlaneFeatureFlagDowngrade | [admission-plane-feature-flag-downgrade](admission-plane-feature-flag-downgrade.html) | admission |`
   - While there, add rows for `EphemeralContainerCredGuardUnavailable` (Warning, admission) and `ElicitationContentTamperDetected` (Critical, gateway) as well. The iter7 fix envelope added all three alerts and none got the index row — each `Follow docs/runbooks/X` pointer in observability.md needs a live target.

2. **Drop the file-level references.** Remove the `.md` / `.html` filename references from spec lines 80/201 and docs observability.md line 188, leaving only the `(§17.7)` section cross-reference. This acknowledges that the runbook-stub inventory in §17.7 is the spec's sole authoritative runbook catalogue and defers the file materialization to a later docs pass. Less preferred because the existing sibling runbook files (e.g., `legal-hold-quota-pressure.md`) all exist as real files, so the precedent is file-level.

Resolution (1) is the docs-sync-consistent path and matches the feedback anchor.

---

## Verification of iter7 CNT-relevant touch points

| Iter7 fix site | Status | Notes |
|---|---|---|
| `spec/14_workspace-plan-schema.md:104` anchor `#141-extensibility-rules` → `#141-workspaceplan-schema-versioning` (DOC-032 / CNT-029) | Clean | Target heading exists (line 306), no residual occurrences anywhere in tree. |
| `spec/16_observability.md:204` anchor `#124-quota-and-rate-limiting` → `#124-redis-ha-and-failure-modes` (DOC-031) | Clean | Target heading exists (`spec/12_storage-architecture.md:173`), no residuals. |
| `spec/13_security-model.md:221` `8 → 9` webhook count in NetworkPolicy row | Clean | Body text and `other eight webhooks` parenthetical consistent (N=9 total, so N−1=8 other). |
| `spec/17_deployment-topology.md:56,60,68,84` feature-gated chart inventory narrative | Clean for §17.2 self-consistency; **broken for §17.9 mirror** | See CNT-030 above. |
| `spec/15_external-api-surface.md:886,887` (circuit-breaker `open`/`close` endpoints) `x-lenny-category` annotations (`destructive`/`mutation`) | Clean | Both endpoints carry full `x-lenny-scope` + `x-lenny-mcp-tool` + `x-lenny-category` triplet matching the scope taxonomy at §15.1 line 915. |
| `spec/25_agent-operability.md:4426,4427,4474,4475` new MCP tool rows | Clean | Each tool appears exactly once in the Read or Action tables. Scope string matches §15.1 endpoint annotations. |
| `spec/15_external-api-surface.md:1079` `ELICITATION_CONTENT_TAMPERED` row | Clean | Cross-refs §9.2 resolve (heading exists at line 42); doc mirror at `docs/reference/error-catalog.md:108` present. |
| `spec/15_external-api-surface.md:1091` `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` row | Clean | Cross-refs §17.2 admission-policies inventory item 13 (line 54 exists); doc mirror at `docs/reference/error-catalog.md:123` present. |
| `docs/operator-guide/namespace-and-isolation.md:102` item 8 `lenny-ephemeral-container-cred-guard` | Clean | Description matches spec §17.2 item 13 content scope. Docs numbering differs from spec (docs starts at 1; spec starts at 5 for validating-webhook entries) — pre-existing structural divergence, not a regression. |
| `docs/operator-guide/observability.md:187–189` three new-alert rows | **Clean for two of three rows**; **broken cross-ref in row 188** | See CNT-031 above. |

---

## Convergence note

No Critical or High regressions detected in the iter7 fix commit scope. Two Mediums:

- **CNT-030** (baseline-count mismatch at §17.9) — single-line edit to align the admission-webhook-inventory preflight-check description with the §17.2 five-entry baseline.
- **CNT-031** (broken cross-file reference to `admission-plane-feature-flag-downgrade.md`) — either create the runbook stub + index rows, or prune the `.md` filename references.

Both are trivial to close. The iter7 fix pass otherwise executed cleanly on CNT-adjacent surfaces (anchors repaired, webhook counts reconciled, error-catalog mirrored, MCP-tool inventory deduplicated, scope taxonomy closed-list extended). No residual occurrences of iter7's targeted broken anchors remain.
