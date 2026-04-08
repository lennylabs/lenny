# Technical Design Review Findings — 2026-04-07 (Iteration 9, Perspective 15: Competitive Positioning & Open Source Strategy)

**Document reviewed:** `docs/technical-design.md` (8,671 lines)
**Review perspective:** Competitive Positioning & Open Source Strategy
**Iteration:** 9
**Category prefix:** CPS (starting at 025, per instruction)
**Scope:** Genuine factual errors in competitive claims only

Prior CPS findings status:
- CPS-001 through CPS-004: Fixed (iter1)
- CPS-005: Open (iter1–8, carried forward — not a factual error, out of scope this iteration)
- CPS-006: Fixed (iter7/8)
- CPS-007: Open (iter1–8, carried forward — not a factual error, out of scope this iteration)
- CPS-008: Fixed (iter1/2)
- CPS-009, CPS-010: Addressed (iter2–3)
- CPS-021: Fixed (iter4)
- CPS-022: Open (iter7–8, GOVERNANCE.md phase contradiction — not a factual competitive claim, out of scope this iteration)
- CPS-023: Fixed (iter7/8)
- CPS-024: Open (iter8, §23.1 differentiator 3 E2B hosted claim contradicts §23 table — carried forward, not re-reviewed here as it is already filed)

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 2     |

---

## Detailed Findings

---

### CPS-025 E2B License Described as "AGPL + Commercial" — E2B Is Apache-2.0 [Medium]
**Section:** 19 (table entry 14, line 8461), 23.2 (line 8580), 18 (Phase 0, line 8383)

The spec asserts that E2B uses an "AGPL + commercial" licensing model in three places:

- **Line 8383 (Phase 0 description):** "evaluate candidate licenses (MIT, Apache 2.0, AGPL + commercial, BSL) against ... competitive positioning (see Section 23.2)"
- **Line 8461 (§19 resolved decisions table, entry 14):** "License selection is a hard Phase 0 gating item — evaluated against competitive positioning (**E2B uses AGPL + commercial**, Temporal and LangChain use MIT)"
- **Line 8580 (§23.2 open-source license paragraph):** "Evaluation criteria: competitive landscape alignment (**E2B uses AGPL + commercial**, Temporal and LangChain use MIT)"

This is factually incorrect. E2B's repositories are licensed under **Apache-2.0**:
- `e2b-dev/E2B` (main SDK): Apache-2.0
- `e2b-dev/infra` (self-hosting infrastructure): Apache-2.0
- `e2b-dev/dashboard`: Apache-2.0

E2B does not use AGPL. There is no AGPL + commercial dual-licensing structure. The spec's characterization is straightforwardly wrong.

**Impact:** The E2B license claim is used as a direct input to the ADR-008 license selection process (Phase 0 gating item). A decision-maker evaluating candidates against the stated criterion "competitive landscape alignment (E2B uses AGPL + commercial)" is working from a false premise. Specifically, the AGPL characterization might lead to over-weighting AGPL as a pattern used by direct competitors, or to misunderstanding the copyleft exposure for integrators. Since ADR-008 is a hard prerequisite before any external contributor engagement, this error has downstream consequences for the community launch gate.

**Recommendation:** Correct all three occurrences. Replace "E2B uses AGPL + commercial" with "E2B uses Apache-2.0" in lines 8461 and 8580. The Phase 0 description (line 8383) need not list competitor licenses explicitly; if it does, correct to "Apache-2.0 (E2B, Temporal server)." Suggested corrected text for lines 8461 and 8580:

> "competitive landscape alignment (E2B uses Apache-2.0; Temporal server uses MIT; LangChain uses MIT; LangSmith is proprietary)"

---

### CPS-026 Google A2A Protocol Described as "Under AAIF Governance Alongside MCP" — A2A Is a Separate Linux Foundation Project [Medium]
**Section:** 23 (competitive table, line 8534)

The §23 competitive table states:

> "Google A2A Protocol — Agent-to-agent protocol **now under AAIF governance alongside MCP**. Addressed via `ExternalAdapterRegistry`, `publishedMetadata`, and `allowedExternalEndpoints` (Sections 15, 5.1, 8.3)"

This is factually incorrect on the governance claim. The actual governance structure as of the document's review date:

- **A2A (Agent2Agent Protocol):** Google donated A2A to the Linux Foundation in **June 2025**. It is a standalone Linux Foundation project — not part of AAIF.
- **AAIF (Agentic AI Foundation):** Announced by the Linux Foundation in **December 2025**. AAIF's three founding projects are Anthropic's MCP, Block's goose, and OpenAI's AGENTS.md. A2A is not an AAIF project.

The two are complementary but organizationally distinct: both sit within the Linux Foundation umbrella, but A2A and AAIF are separate initiatives with separate governance. A2A is not "alongside MCP" under AAIF; MCP is an AAIF project, A2A is not.

**Impact:** The competitive table is a factual reference. Readers relying on it to understand the protocol governance landscape — particularly enterprise evaluators and platform operators assessing standards risk — will have a false picture of how A2A and MCP relate institutionally. The error also misrepresents the stability and convergence status of A2A: being an independent Linux Foundation project vs. being co-governed under AAIF are different maturity signals.

**Recommendation:** Correct the governance description:

> "Google A2A Protocol — Agent-to-agent protocol contributed to the Linux Foundation as a standalone project (June 2025). MCP is governed under the Linux Foundation's AAIF (December 2025); A2A is a separate Linux Foundation project. Addressed via `ExternalAdapterRegistry`, `publishedMetadata`, and `allowedExternalEndpoints` (Sections 15, 5.1, 8.3)"

---

## Findings Summary Table

| ID      | Section | Severity | Description |
|---------|---------|----------|-------------|
| CPS-025 | 19, 23.2, 18 | Medium | E2B license described as "AGPL + commercial"; E2B repos are Apache-2.0 — error propagated into ADR-008 license selection criteria |
| CPS-026 | 23 | Medium | A2A described as "under AAIF governance alongside MCP"; A2A is a separate Linux Foundation project, not an AAIF project |

**Total new findings: 2 (both Medium). Zero High or Critical.**
