# Competitive Positioning & Open Source Strategy Review — Iteration 2

**Review Date:** 2026-04-19
**Reviewer:** Claude Opus 4.7
**Scope:** Section 23 (Competitive Landscape) and related cross-references (§2, §18, §19, §22)
**Prior findings reviewed:** `iter1/CPS.md` (CPS-001 license status), `iter1/summary.md`

## Status of prior findings

**CPS-001 (license status inconsistency) — FIXED.** All three previously-contradicting locations now consistently describe MIT as resolved:

- §18 Phase 0 (line 7): `*Resolved — MIT.*` with committed `LICENSE` file.
- §19 item 14 (line 20): `**Resolved — MIT (ADR-008).** ... CPS-008 is **resolved**`.
- §23 2026 Entrants Matrix (line 62): `MIT (ADR-008)`.
- §23.2 Open-source license paragraph (line 137): `*Resolved — MIT (ADR-008).*` — phrasing now unambiguous ("license selection was a Phase 0 gating item and is now complete").

The four-candidate list (MIT, Apache 2.0, AGPL + commercial exception, BSL) is preserved as historical evaluation context rather than an open question. Rationale (enterprise adoption friction, copyleft clarity, ecosystem alignment) is consistent across §18, §19, and §23.2. No regressions detected.

---

## New issues

### CPS-002 Differentiator cross-reference off-by-two [LOW]

**Files:** `spec/22_explicit-non-decisions.md` line 13

**Description:**

§22.6 closes with `See [Section 23.1](23_competitive-landscape.md#231-why-lenny), differentiator 6 for the competitive positioning of this principle.` The referenced principle is hooks-and-defaults. In §23.1 (lines 72-86), the ordered differentiator list places hooks-and-defaults as **differentiator 8**, not 6:

- 1 Runtime-agnostic adapter contract
- 2 Self-hosted, Kubernetes-native
- 3 Security by default
- 4 Flexible runtime types and execution modes
- 5 Recursive delegation
- 6 Multi-protocol gateway
- 7 Enterprise controls at the platform layer
- **8 Ecosystem-composable via hooks-and-defaults**  ← target

Line 90 also explicitly states "Beyond the **8 architectural differentiators**", confirming hooks-and-defaults is position 8.

**Impact:** Minor documentation accuracy. A reader following the cross-reference lands on "Multi-protocol gateway" (differentiator 6) instead of the intended hooks-and-defaults entry. Given that §22.6 describes this as a "governing architectural principle" it is specifically the kind of cross-reference an evaluator or new contributor is likely to follow.

**Recommendation:** Change `differentiator 6` to `differentiator 8` in `spec/22_explicit-non-decisions.md` line 13.

---

## Areas checked and clean

- **Upstream risk (`kubernetes-sigs/agent-sandbox`):** §23 table row 1 continues to frame upstream as the adopted infrastructure layer rather than a competitor. §19 item 4 and §18 Phase 0 license criteria both reference the upstream compatibility requirement for license selection. Positioning is internally consistent.
- **Temporal / Modal / LangGraph comparison:** Each has a clearly scoped differentiator story (SDK coupling for Temporal, GPU-first framing for Modal, LangChain coupling for LangGraph/LangSmith). Matrix rows and narrative prose agree. LangSmith's self-hosted-K8s availability is disclosed and the differentiator is narrowed to runtime-agnosticism + per-hop budget/scope.
- **2026 entrants (Scion, OpenShell, OpenSandbox):** Added matrix and narrative are mutually consistent. Workload-focus framing ("Lenny is workload-agnostic") is supported by the `type: agent` + `type: mcp` distinction in §5.1.
- **Hooks/defaults philosophy:** §22.6 and §23.1(8) agree on scope (memory, caching, guardrails, evaluation, routing) and on the "platform layer, not ecosystem layer" stance. §22.2-22.4 reinforce by enumerating what Lenny declines to build.
- **Community adoption funnel:** Runtime authors → platform operators → enterprise teams in §23.2 table aligns with entry points in §15.4 (adapter contract), §17.4 (local dev), and §11/§4.8 (enterprise controls).
- **Governance / CLA:** §23.2 (Phase 2 `CONTRIBUTING.md` + Phase 17a `GOVERNANCE.md`), §19 item 14 ("CLA/DCO policy and `CONTRIBUTING.md` ship in Phase 2"), and §2 (v1 launch deliverables) are consistent.
- **Latency-comparison caveat (line 66):** Still correctly disclaims cold-start vs session-ready measurement differences, with forward reference to Phase 2 benchmark harness in §18.
- **No-backward-compat and no-tier-splitting feedback:** Section does not introduce legacy-flag language or tier-dependent code paths. Single-implementation framing preserved.

---

## Summary

**New issues:** 1 (Low — cross-reference numbering).
**Regressions from iter1:** None.
**CPS-001:** Confirmed fixed across §18, §19, §23.
**Pre-existing, out of scope:** CPS-043 (sustainability model), CPS-048 (K8s adoption barrier) — unchanged from iter1.
