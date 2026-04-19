# Developer Experience (Runtime Authors) Review — DXP

**Review date:** 2026-04-19  
**Reviewer perspective:** Can a runtime author build a Basic-level (Minimum-tier) runtime from the spec?  
**Focus areas:** Basic-level runtime DX, Echo runtime reference, SDK complexity, OutputPart specification  

---

## Summary

After comprehensive review of Sections 15.4 (Runtime Adapter Specification), 15.7 (Runtime Author SDKs), 26 (Reference Runtime Catalog), and local development documentation (Section 17.4), **no real errors or cross-section inconsistencies were found**.

The specification is internally consistent regarding:
- Basic-level runtime capabilities and documented limitations
- OutputPart format (required vs. optional fields)
- Echo runtime pseudocode examples (all three integration levels)
- Local development support (macOS restrictions for Basic-level)
- SDK availability statements
- Runtime author DX roadmap (Section 15.4.5)

---

## Findings

**No real issues found.**

The spec successfully enables a runtime author with no prior Lenny knowledge to:

1. **Start with the Echo runtime sample** (Section 15.4.4) — complete pseudocode for all three integration levels, no pseudo-code or hand-waving
2. **Understand Basic-level scope** (Section 15.4.3) — matrix clearly enumerates unavailable capabilities (delegation, lifecycle channel, platform MCP tools) with no fallback
3. **Build with zero SDK dependency** (Section 15.4.1) — OutputPart construction requires only `type` and `inline` fields; shorthand `{"type": "response", "text": "..."}` is documented and normalized by adapter
4. **Test locally on their machine** (Section 17.4) — `make run` explicitly supports macOS for Basic-level; `docker compose up` (Tier 2) available for Standard/Full
5. **Understand output format complexity** (Section 15.4.1) — OutputPart vs. MCP content blocks decision is explained with concrete examples and a helper method (`from_mcp_content()`) documented as optional

The degraded experience for Basic-level runtimes (no checkpoint, no clean interrupt, no delegation) is clearly documented in the limitations list (lines 1558–1572 of 15_external-api-surface.md) with explicit "N/A" markers in the capability comparison matrix (Section 15.4.3, lines 1541–1556).

All cross-references are valid; no terminology inconsistencies.

