# Technical Design Review Findings — 2026-04-07 (Iteration 7, Perspective 15: Competitive Positioning & Open Source Strategy)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Competitive Positioning & Open Source Strategy
**Iteration:** 7
**Category prefix:** CPS (starting at 022)
**Total findings:** 4 (2 Medium + 2 carried-forward open Medium)

Prior CPS findings reviewed: CPS-001 through CPS-021.
- CPS-001 through CPS-004: Fixed (iter1)
- CPS-005, CPS-006, CPS-007: Open from iter1; never Fixed or Skipped in iter2–6 — carried forward below
- CPS-008: Fixed (iter1/iter2)
- CPS-009, CPS-010: Addressed by spec changes in iterations 2–3 (Section 17.4 local dev mode, echo runtime, TTHW target, delegation-echo runtime)
- CPS-011, CPS-012: Info only (not tracked)
- CPS-021 (iter4): Fixed in spec at §23.1 differentiator 4 ("Lenny implements MCP Tasks at the gateway's external MCP interface; internal delegation between gateway and agent pods uses a custom gRPC protocol with equivalent semantics")

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 4     |

---

## Detailed Findings

### CPS-022 `GOVERNANCE.md` Ship Phase Contradicts Between §19 and §23.2 [Medium]
**Section:** 19, 23.2, 2, 18

§23.2 (line 8385) states: "`GOVERNANCE.md` published in Phase 2 alongside `CONTRIBUTING.md`. Both files are v1 launch deliverables." §2 (line 55) also states both are "published in Phase 2." However, §19 Resolved Decisions table entry 14 (line 8269) states: "`CONTRIBUTING.md` ship in Phase 2... `GOVERNANCE.md` ships in Phase 17a." These are directly contradictory on when `GOVERNANCE.md` first ships.

The Phase 18 (build sequence) Phase 17a row (line 8243) says `CONTRIBUTING.md` and `GOVERNANCE.md` are "review and finalization" deliverables — consistent with them being first published in Phase 2 and finalized in Phase 17a. However, §19 says `GOVERNANCE.md` ships first in Phase 17a, not Phase 2.

**Impact:** An implementor reading §19 will schedule `GOVERNANCE.md` for Phase 17a. An implementor reading §2 or §23.2 will schedule it for Phase 2. Contributors onboarding between Phase 2 and Phase 17a will either find `GOVERNANCE.md` missing (if §19 governs) or present-but-unfinalized (if §23.2 governs). The contradiction creates ambiguity about a community-launch gate.

**Recommendation:** Align §19 entry 14 with §2 and §23.2: `GOVERNANCE.md` ships a v1 draft in Phase 2 (alongside `CONTRIBUTING.md`) and is finalized in Phase 17a. Rewrite §19 entry 14 to: "`CONTRIBUTING.md` and `GOVERNANCE.md` both ship as v1 drafts in Phase 2 alongside the `make run` quick-start. Both are reviewed and finalized in Phase 17a before community launch announcement."

---

### CPS-023 §23 Competitive Table Incorrectly Claims LangSmith Has No Self-Hosted Path [Medium]
**Section:** 23

§23 states: "LangSmith is also a hosted service with no self-hosted Kubernetes-native deployment path." This is factually inaccurate as a blanket statement. LangSmith has offered a self-hosted Enterprise deployment option (Docker Compose and Kubernetes-based) since at least 2024. While LangSmith's self-hosted path is an enterprise/paid offering and is significantly more operationally complex than Lenny's Helm-chart-plus-CRD deployment, the claim that it has "no self-hosted Kubernetes-native deployment path" is demonstrably false.

**Impact:** Competitive claims that are factually incorrect undermine Lenny's credibility with the enterprise platform teams and runtime authors who are the primary target adopters (§23.2). An informed LangChain/LangSmith user evaluating Lenny will immediately identify this as an error, casting doubt on the accuracy of the broader competitive analysis.

**Recommendation:** Replace the inaccurate blanket claim with an accurate, nuanced comparison. Suggested replacement: "LangSmith has a self-hosted Enterprise deployment option, but it requires a commercial license and is operationally heavier than Lenny's Helm chart + CRDs; the standard offering is hosted-only." This preserves Lenny's differentiation (open-source, no commercial license required, lighter operational model) without making a claim that can be immediately refuted.

---

### CPS-005 (Open, iter1) External Interceptors Require gRPC — Polyglot Barrier Undocumented [Medium]
**Section:** 4.8, 23.2

This finding was raised in iter1 and never Fixed or Skipped. The current state of the spec:

- Runtime binaries (agent code): can be any language — the sidecar model uses stdin/stdout JSON Lines (Section 15.4.1, §4.7 table line 745: "Any language (stdin/stdout)"). This is well-documented and addresses the polyglot concern for runtime authors.
- External interceptors (deployer extension point, §4.8): require a gRPC server (`service RequestInterceptor { rpc Intercept(...) }`) — this is a Go or gRPC-capable language requirement. The spec never explicitly states that interceptors are language-constrained, nor does it acknowledge the barrier for Python/TypeScript deployers who want to implement content policy hooks without a gRPC service.

The distinction matters because the two audience groups are different: runtime authors (addressed by sidecar) vs. deployers writing custom policy hooks (gRPC requirement undisclosed). Section 23.2's "Runtime authors" persona is addressed, but the "Platform operators" and "Enterprise platform teams" personas who need custom interceptors are not told about the gRPC requirement.

**Recommendation:** Add one sentence to the §4.8 interceptor registration block and to §23.2 (or §15.4): "External interceptors require a gRPC service implementation; Python and TypeScript deployments can use grpc.io libraries for their respective ecosystems. HTTP webhook variants are not currently supported — this is a potential enhancement for a future release." This sets honest expectations without misrepresenting the platform's capabilities.

---

### CPS-006 (Open, iter1) No Community Runtime Registry or Discovery Concept [Medium]
**Section:** 23.2, 5.1, 15.4

This finding was raised in iter1 and never Fixed or Skipped. The current state of the spec: there is no concept of a community runtime registry, marketplace, or catalog — no place where runtime authors can publish their adapter images/configurations for operators to discover and deploy. The spec defines the runtime adapter contract (§15.4), three integration tiers (§15.4.3), and an echo runtime sample (§15.4.4), which are good onboarding tools. But there is no mechanism for the ecosystem to grow through shared community runtimes.

The competitive context makes this a meaningful gap: E2B has an "E2B Sandbox Templates" community catalog; Daytona has a workspace template repository. Lenny's open-source positioning (§23.2) targets runtime authors as a primary community persona, but gives them no place to publish their work or for operators to discover it.

**Note:** A full registry implementation is out of scope for a v1 technical spec. The gap is that §23.2's community strategy does not acknowledge this as a planned future capability or explicitly scope it out of v1.

**Recommendation:** Add one sentence to §23.2 under community strategy: "A community runtime registry (enabling runtime authors to publish adapter configurations and operators to discover community runtimes) is out of scope for v1 but is a planned post-v1 community infrastructure investment. Until then, the primary discovery mechanism is the published adapter specification (§15.4) and examples in the repository." This closes the gap between the community persona's expectations and v1 deliverables.
