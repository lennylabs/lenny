# Technical Design Review — Perspective 15: Competitive Positioning & Open Source Strategy

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 15 — Competitive Positioning & Open Source Strategy
**Iteration:** 14
**Prior finding status:** CPS was clean in iteration 13. No prior CPS findings to verify.
**New findings:** 3

## Medium

| # | ID | Finding | Section | Lines |
|---|-----|---------|---------|-------|
| 1 | CPS-035 | TTHW paragraph references wrong section for `make run` local dev mode. Text says "The Phase 2 `make run` local dev mode (Section 18)" but Section 18 is the Build Sequence. The `make run` local dev mode is documented in Section 17.4. Fix: replace "(Section 18)" with "(Section 17.4)". | 23.2 | 8590 |
| 2 | CPS-036 | Enterprise persona entry point references wrong section. The persona table directs enterprise platform teams to "enterprise controls documentation (Section 16)" but Section 16 is "Observability" (metrics, tracing, alerting). Enterprise controls — policy engine, rate limiting, auth, token budgets — are in Sections 4.8, 8, and 10. Fix: change "Section 16" to a more accurate reference such as "Sections 4.8, 8, 10" or add "and observability (Section 16)" as a secondary reference. | 23.2 | 8588 |
| 3 | CPS-037 | Phase 0 milestone text contradicts Phase 17a community gate. Phase 0 milestone reads "repository open for external contribution" but Phase 17a explicitly states "no external contributor PR solicitation before 17a completes." At Phase 0, the repo has only a license and two ADRs — no CONTRIBUTING.md, no `make run`, no documentation. The milestone overstates what Phase 0 achieves. Fix: change Phase 0 milestone to "License committed; ADR-007 and ADR-008 both recorded" (removing "repository open for external contribution"), or qualify it as "repository legally open for contribution" to distinguish from Phase 17a's active solicitation gate. | 18, 23.2 | 8403, 8455 |

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **Differentiation narrative (Section 23.1):** Six differentiators are concrete, each cites specific spec sections, and each is substantiated by the referenced design (runtime-agnostic adapter in 15.4, recursive delegation in 8, self-hosted K8s in 17, multi-protocol in 15, enterprise controls in 4.8/8/16, hooks-and-defaults in 22.6). No aspirational claims presented as architectural commitments.
- **agent-sandbox upstream risk (Section 4.6.1):** Go/no-go criteria (API stability, community support SLO, integration test pass rate) are well-defined with clear fallback plan (2-3 week kubebuilder replacement). Dependency pinning policy, quarterly review cadence, and Phase 1 exit gate are all specified. Internal abstraction layer (`PodLifecycleManager`, `PoolManager`) correctly insulates consumers from upstream CRD changes.
- **Community adoption funnel (Section 23.2):** Three personas (runtime authors, platform operators, enterprise teams) with distinct entry points. TTHW < 5 min target with CI smoke test. Governance model (BDfN to steering committee) with transition criteria. CONTRIBUTING.md and GOVERNANCE.md phasing is internally consistent (published Phase 2, finalized Phase 17a) — except the cross-reference errors noted above.
- **Hooks-and-defaults philosophy (Section 22.6):** Clearly articulated as a governing principle. Cross-referenced correctly by Section 23.1 differentiator 6. Non-decisions 22.2–22.5 consistently apply the principle (no built-in eval, guardrails, memory extraction).
- **Competitor comparison table (Section 23):** Eight competitors listed with accurate characterizations. Latency comparison note correctly distinguishes cold-start vs full session-ready time. LangSmith entry is internally consistent with differentiators 1 and 2.
- **License selection (Section 23.2, 19 #14, Phase 0):** Evaluation criteria, candidate licenses, ADR-008 reference, and Phase 0 gating are all consistent across the three locations that discuss it.
- **Community runtime registry:** Correctly scoped as post-v1. v1 distribution via standard Go modules/container registries is sensible.
- **Comparison guides phasing:** Section 23.2 says "Phase 17 deliverables" while build sequence places them in Phase 17a specifically. Imprecise but not incorrect since 17a is a sub-phase of 17.
