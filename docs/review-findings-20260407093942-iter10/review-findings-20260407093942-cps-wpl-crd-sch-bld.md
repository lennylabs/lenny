# Technical Design Review Findings — 2026-04-07 (Iteration 10)

**Document reviewed:** `docs/technical-design.md` (8,673 lines)
**Iteration:** 10
**Perspectives covered:** 15 (Competitive), 16 (Warm Pool), 17 (Credentials), 18 (Schema), 19 (Build Sequence)
**Category prefixes:** CPS-027+, WPL-028+, CRD-029+, SCH-038+, BLD-029+
**Skipped (by instruction):** All prior Skipped findings remain skipped and are not re-raised.

---

## Prior Findings Status Verification

**P15 Competitive:**
- CPS-022 (`GOVERNANCE.md` phase contradiction): **Fixed** — §19 entry 14 now says "drafted in Phase 2, finalized in Phase 17a", consistent with §2 and §23.2.
- CPS-024 (§23.1 E2B "requires hosted infrastructure" contradicts §23 table): **Fixed** — §23.1 differentiator 3 now accurately says "E2B offers an open-source self-hosted option but requires operators to manage Firecracker/microVM infrastructure separately from their Kubernetes clusters."
- CPS-025 (E2B license "AGPL + commercial"): **Partially fixed** — line 8463 (§19 table) corrected to "Apache 2.0 with a commercial offering." Line 8582 (§23.2 license paragraph) still reads "E2B uses AGPL + commercial." Residual error — see CPS-027 below.

**P16 Warm Pool:**
- WPL-025 (`scaleToZero.timezone` incorrectly attributed to Kubernetes CronJob ≥ 1.27): **Fixed** — now reads "parsed by the PoolScalingController's embedded cron scheduler (Go cron library); any IANA timezone string is accepted on all Kubernetes versions."
- WPL-026 (burst term missing `variant_weight`): **Fixed** — both the default formula (§4.6.2) and mode-adjusted formula (§5.2) now correctly apply `variant_weight` to the burst term.

**P17 Credentials:**
- CRD-025 (`azure_openai` `materializedConfig` schema absent): **Fixed** — full `azure_openai` entry added to the `materializedConfig` schema table covering both API-key pools and Azure AD pools. Residual gap identified — see CRD-029 below.
- CRD-026/027/028 (vestigial `privateKeyJson`, missing `github`/`vault_transit` in provider table, incorrect `anthropic_direct` description): All **Fixed**.

**P18 Schema:**
- SCH-036 (`delegation.completed` webhook `status` enum incomplete): **Fixed** — status enum now reads "completed|failed|terminated|cancelled|expired".

**P19 Build Sequence:**
- BLD-026 (Phase 11.5 references prohibited "credential elicitation flow"): **Fixed** — Phase 11 and 11.5 now use "pre-authorized registration via `POST /v1/credentials`" language throughout.
- BLD-027 (Phase 16 single-line entry with no deliverable detail): **Not fixed** — Phase 16 still reads "Experiment primitives, PoolScalingController experiment integration. | A/B testing infrastructure." Carried forward as BLD-029 below.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 2     |
| Low      | 0     |

---

## Detailed Findings

---

### CPS-027 §23.2 License Paragraph Still Claims "E2B Uses AGPL + Commercial" After Partial Fix [Medium]

**Section:** 23.2, line 8582

**Description:**

The CPS-025 fix (iter9) corrected only one of two occurrences of the incorrect E2B license claim. The §19 resolved-decisions table (line 8463) was updated to "E2B uses Apache 2.0 with a commercial offering." However, the §23.2 open-source license paragraph (line 8582) was not updated and still reads:

> "Evaluation criteria: competitive landscape alignment (**E2B uses AGPL + commercial**, Temporal and LangChain use MIT), enterprise legal review..."

E2B's repositories (`e2b-dev/E2B`, `e2b-dev/infra`, `e2b-dev/dashboard`) are all Apache-2.0. E2B does not use AGPL. The two occurrences in the spec are now contradictory: one says "Apache 2.0 with a commercial offering" (line 8463), the other still says "AGPL + commercial" (line 8582).

**Impact:** The §23.2 paragraph is the authoritative license evaluation criteria text — it is the section referenced by the Phase 0 gate description ("see Section 23.2") and is the primary guidance for ADR-008. A decision-maker reading §23.2 to perform the competitive license analysis receives incorrect information about E2B's licensing model. A reader comparing the two sections gets contradictory signals from the same document.

**Recommendation:** Update line 8582 to match the corrected line 8463 text:

Replace:
> "competitive landscape alignment (E2B uses AGPL + commercial, Temporal and LangChain use MIT)"

With:
> "competitive landscape alignment (E2B uses Apache-2.0; Temporal and LangChain use MIT)"

---

### CRD-029 `azure_openai` Secret Shape Table Missing Azure AD Credential Type [Medium]

**Section:** 4.9 (Secret shape table), lines 1016–1024

**Description:**

The Secret shape table (§4.9) documents what Kubernetes Secret keys the Token Service reads for each credential provider. The `azure_openai` entry lists only:

| Provider | Secret key | Value |
|---|---|---|
| `azure_openai` | `apiKey` | Azure OpenAI API key |

However, the `azure_openai` provider documentation in §4.9 explicitly supports **two credential types**:
- API-key pools (Secret contains `apiKey`)
- Azure AD token-backed pools (Secret must contain the Azure service principal credentials needed to mint a short-lived access token)

The `materializedConfig` schema table (added by the CRD-025 fix) correctly documents both types at runtime — `apiKey` for API-key pools and `accessToken` for Azure AD pools. The `leaseTTLSeconds` table (line 995) also notes "Azure AD tokens may be up to 24 hours" separately from API-key pool behavior.

But the Secret shape table documents nothing for Azure AD pools. An operator creating a credential pool backed by Azure AD for `azure_openai` cannot determine from this table:
- What key(s) the Secret must contain (`clientId`, `clientSecret`, `tenantId`, or a service account certificate — all are standard Azure AD credential patterns)
- Whether the Secret uses one key per field or bundles them (e.g., as JSON)
- What naming convention to use (contrast with `vertex_ai` which uses `serviceAccountJson` as a single bundled key)

This is inconsistent with how other multi-type providers are handled: `aws_bedrock` documents two Secret variants (role and access keys) separately in the table.

**Impact:** Operators cannot configure Azure AD-backed `azure_openai` credential pools from the spec. The Token Service's startup validation ("validates that all `secretRef` values referenced in the database are accessible via its RBAC grants") would pass even for a misconfigured Azure AD Secret, since it only checks Secret existence — the content contract is undocumented. Runtime authors integrating Azure OpenAI with AD token authentication cannot verify their Secret is correctly shaped.

**Recommendation:** Add an Azure AD row to the Secret shape table for `azure_openai`, covering what the Token Service expects to read to mint a short-lived access token:

| Provider | Secret key | Value |
|---|---|---|
| `azure_openai` (API-key pools) | `apiKey` | Azure OpenAI API key |
| `azure_openai` (Azure AD pools) | `clientId` | Azure AD application (client) ID |
| | `clientSecret` | Azure AD application client secret |
| | `tenantId` | Azure AD tenant ID |

(Or use a single bundled JSON key — e.g., `azureAdCredentials` containing all three fields — consistent with the `serviceAccountJson` pattern used by `vertex_ai`. Either approach must be documented.) Add a note distinguishing the two pool types, matching the `aws_bedrock` precedent.

---

### BLD-029 Phase 16 Single-Line Entry Has No Deliverable Detail, Prerequisite, or Integration Test Gate [Medium]

**Section:** 18 (Phase 16), line 8436

**Description:**

Phase 16 reads in its entirety:

> `| 16 | Experiment primitives, PoolScalingController experiment integration. | A/B testing infrastructure |`

This is unchanged from iter8 (BLD-027, which was filed but not fixed). It is the least-specified phase in the build sequence by a significant margin. Comparison with adjacent phases:

- **Phase 15**: names 7 deliverables with explicit cross-references.
- **Phase 12b/12c**: explicit integration test gates listed as merging prerequisites.
- **Phase 13.5**: 7 named load test scenarios with SLO cross-references.
- **Phase 17a**: 6 deliverables with community-launch gate semantics.

Section 10.7 ("Experiment Primitives") defines at minimum 10 subsystems needing Phase 16 implementation: `ExperimentSpec` CRD, `ExperimentRouter` (percentage and user-list targeting modes), sticky assignment (session and user scoped), `VariantPoolSpec`, PoolScalingController variant pool lifecycle (creation, `minWarm` recomputation, teardown on pause/conclude), rollback trigger monitoring, isolation monotonicity check for variant pools, and the `ExperimentTargetingCircuitOpen` circuit-breaker. None of these are named in Phase 16.

Additionally missing:
- Whether Phase 15 (environment resource) is a hard prerequisite — the `ExperimentRouter` uses environment-scoped pool selection
- An integration test gate (unlike Phase 12b/12c)
- Whether Phase 13.5 should baseline experiment-targeting latency and sticky-cache throughput (it does not list those scenarios)
- Definition-of-done criteria

**Impact:** A build agent or engineering team reading Phase 16 cannot determine what to build, in what order, or when the phase is complete. All of §10.7 is implicitly in scope with no sequencing. The absence of a Phase 15 prerequisite declaration risks attempting environment resource integration out of order. The absence of an integration test gate risks merging experiment-routing code without validating assignment consistency across sticky cache invalidation and pool recomputation.

**Recommendation:** Expand Phase 16 to match Phase 12b/12c specification level. At minimum add:

- **Prerequisites:** Phase 15 (environment resource) is a hard prerequisite for `ExperimentRouter`'s environment-scoped pool selection.
- **Named deliverables:** `ExperimentSpec` admin API + CRD, `ExperimentRouter` (both percentage-hash and user-list targeting modes), sticky assignment (user and session scoped) with Redis cache, variant pool lifecycle in PoolScalingController (creation, `minWarm` recomputation when activated/paused/concluded, teardown), rollback trigger monitoring (recommended thresholds from §10.7), isolation monotonicity check for variant pools.
- **Integration test gate (prerequisite for merging):**
  1. `ExperimentRouter` assignment consistency: same `(user_id, experiment_id)` always maps to the same variant across reconnects.
  2. Sticky assignment cache invalidation: variant transition to `paused` or `concluded` flushes cached assignments and routes subsequent sessions correctly.
  3. Variant pool `minWarm` correctly recomputed when base pool demand changes or when the variant is activated/paused.
- **Phase 13.5 augmentation:** add experiment-targeting latency (P95 session assignment with active experiment) and sticky-cache throughput as scenarios (7) and (8) in the Phase 13.5 pre-hardening baseline.

---

## Findings Summary Table

| ID | Perspective | Section | Severity | Description |
|----|-------------|---------|----------|-------------|
| CPS-027 | P15 Competitive | 23.2 line 8582 | Medium | §23.2 license paragraph still claims "E2B uses AGPL + commercial" after partial CPS-025 fix; §19 table (line 8463) now correctly says Apache-2.0 — internal contradiction remains |
| CRD-029 | P17 Credentials | 4.9 lines 1016–1024 | Medium | `azure_openai` Secret shape table documents only API-key pool; Azure AD token-backed pool Secret content (clientId, clientSecret, tenantId) is entirely absent, leaving operators unable to configure AD-backed credential pools |
| BLD-029 | P19 Build Sequence | 18 Phase 16 | Medium | Phase 16 single-line entry (unchanged from BLD-027) has no deliverable detail, no Phase 15 prerequisite declaration, no integration test gate, and no definition of done |

**Total new findings: 3 (all Medium). Zero High or Critical.**

---

## Clean Perspectives

**P16 Warm Pool:** Clean after fixes to WPL-025 and WPL-026. No new warm pool design flaws found.

**P18 Schema:** Clean after fix to SCH-036. Schema versioning rules (§15.5 item 7), WorkspacePlan versioning (§14), and OutputPart schema obligations (§15.4.1) are consistent and complete. No new schema design flaws found.
