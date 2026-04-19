# Competitive Positioning & Open Source Strategy Review - CPS-001

**Review Date:** 2026-04-19  
**Reviewer:** Claude Haiku 4.5  
**Scope:** Section 23 (Competitive Landscape) and related sections (2, 18, 19)

---

### CPS-001 License Decision Status Inconsistency [MEDIUM]

**Files:** 
- `spec/23_competitive-landscape.md` (lines 62, 137-138)
- `spec/18_build-sequence.md` (line 7)
- `spec/19_resolved-decisions.md` (line 20)

**Description:**

The Feature Comparison Matrix in Section 23 (line 62) lists Lenny's license as "MIT (ADR-008)", implying the license decision has been finalized. However, the Build Sequence (Section 18, Phase 0) and Resolved Decisions (Section 19, item 14) both state that license selection is a **Phase 0 gating item** where the decision **must** be made before Phase 1 begins. ADR-008 is described as the location where the decision will be "recorded," not as evidence that the decision has been made.

**Specific Evidence:**

1. Section 23.2 (line 137): "License selection is a **Phase 0 gating item** (ADR-008, recorded in `docs/adr/`). The license must be committed to the repository root before any contributor engagement..."

2. Section 18, Phase 0 (line 7): "Open-source license selection (ADR-008)... Candidate licenses: MIT, Apache 2.0, AGPL + commercial, BSL. **Decision recorded as ADR-008** in `docs/adr/`. The license must be committed to the repository root before Phase 1 begins."

3. Section 19, item 14 (line 20): "Candidate licenses: MIT, Apache 2.0, AGPL + commercial exception, BSL. Decision recorded as ADR-008 in `docs/adr/` and the selected license committed to the repository root **before Phase 1 begins**."

The matrix entry "MIT (ADR-008)" contradicts all three of these — it presents MIT as the decided license rather than one of four candidates under evaluation.

**Recommendation:**

Update the Feature Comparison Matrix (line 62) to reflect the actual status. Options:

1. **If license is still undecided (more likely given Phase 0 status):** Change entry to "TBD (ADR-008 pending Phase 0 decision)" or list candidates: "MIT, Apache 2.0, AGPL+commercial, or BSL (decision in Phase 0, ADR-008)"

2. **If license has been decided to MIT:** Add a note below the matrix documenting Phase 0 completion and the date of decision, and update Section 18/19 language to use past tense ("License selection was completed in Phase 0 as MIT, recorded in ADR-008").

Clarify the current project status in the executive context (Section 1 or 2) regarding which phase the implementation is at, so readers can distinguish between gating decisions (Phase 0) and resolved decisions (Phase 1+).

---

## Summary

**Issues Found:** 1 (factual inconsistency)

**Severity Distribution:**
- Medium: 1 (license decision status representation)

**Cross-Section Consistency:** The competitive landscape section's license claim conflicts with the build sequence and resolved decisions on what constitutes a "resolved" vs. "gating" decision. This affects external audience understanding of project readiness and governance status — particularly relevant for the "Governance model" community adoption narrative in Section 23.2.

**No upstream-risk issues or broken internal references detected.** All section cross-references verified. Competitor descriptions (E2B, Daytona, Fly.io, OpenShell, OpenSandbox, Scion) are factually stated without internal contradiction, and timeline claims (Jan 2026 Fly.io, March 2026 OpenSandbox, April 2026 Scion, GTC 2026 OpenShell) are consistent within the document. Community adoption funnel (runtime authors → platform operators → enterprise teams) is clearly articulated with entry points specified.

---

**Reference:** CPS-043 (sustainability model) and CPS-048 (K8s adoption barrier) noted as pre-existing and skipped per instructions.
