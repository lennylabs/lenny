# Document Quality, Consistency & Completeness Review
**Date:** 2026-04-19  
**Iteration:** 1  
**Spec Size:** 28 files, ~17,741 lines

---

## Findings Summary

Found **2 real issues**:

1. **Incorrect relative paths** (16 instances) — Files reference `../spec/` when all files are in the same directory
2. **Confusing section titles** (2 instances) — Sections 16.7 and 16.8 titles conflate two section numbers

All other checks passed: section numbering is correct, README cross-references are valid, no broken markdown anchors, no duplicate content, no undefined critical terms.

---

## Detailed Findings

### DOC-001 Incorrect Cross-File Relative Path References [Medium]

**Files:** `04_system-components.md`, `08_recursive-delegation.md`, `12_storage-architecture.md`, `25_agent-operability.md`

**Issue:** 16 instances of references use `../spec/` relative path when all spec files are in the same `/Users/joan/projects/lenny/spec/` directory. Examples:

- Line 230 (04_system-components.md): `[Section 11.7](../spec/11_policy-and-controls.md#117-audit-logging)`
- Line 232 (04_system-components.md): `[Section 11.7](../spec/11_policy-and-controls.md#117-audit-logging) Wire Format`
- Line 1408 (04_system-components.md): `[§10](../spec/10_gateway-internals.md)`
- Line 59 (08_recursive-delegation.md): `[§13](../spec/13_security-model.md#133-credential-flow)`
- Multiple instances in `12_storage-architecture.md` (lines 16, 17, 612, 650, 654)
- Multiple instances in `25_agent-operability.md` (lines 642, 644, 2316, 2318, 3415, 3421)

The `../spec/` prefix suggests the files are one level up from current location, which is incorrect. When rendering on GitHub or other markdown viewers, these links break.

**Recommendation:** Replace all `](../spec/` with `](` to use relative same-directory references. Example: `](../spec/11_policy-and-controls.md#117-audit-logging)` → `](11_policy-and-controls.md#117-audit-logging)`.

---

### DOC-002 Confusing Section Title Mixing [Low]

**Files:** `16_observability.md`, `README.md`

**Issue:** Sections 16.7 and 16.8 have titles that reference "Section 25":
- `### 16.7 Section 25 Audit Events`
- `### 16.8 Section 25 Metrics`

While technically these sections *do* describe events and metrics added by Section 25, the titles are confusing because they mix section numbers (the current section is 16.x, but the title says "Section 25"). The README amplifies this confusion by rendering them as:
- `16.7 Section 25 Audit Events`
- `16.8 Section 25 Metrics`

**Recommendation:** Clarify the titles to make the relationship explicit without mixing section numbers. Options:
- `16.7 Agent Operability Audit Events` (forward-reference to Section 25 in body)
- `16.8 Agent Operability Metrics` (forward-reference to Section 25 in body)
- Or keep current titles but add clarifying intro: "The following audit event types are introduced by Section 25 (Agent Operability):"

---

## Verification Summary

| Check | Result |
|-------|--------|
| Section numbering (01–27, 28 files) | ✓ Correct |
| Markdown anchor validity (README → actual headers) | ✓ Valid |
| Cross-section references (e.g., "Section 5.1") | ✓ All exist |
| File references in paths | ✗ **16 broken relative paths** |
| Duplicate content (same heading across files) | ✓ Expected (subsection names like "Metrics", "Endpoints") |
| Empty sections (e.g., section 20) | ✓ Intentional (resolved in section 19) |
| Undefined acronyms (SPIFFE, mTLS, HPA, RBAC, OCSF) | ✓ Used in context; no formal definitions needed in spec |
| Title consistency (confusing names) | ✗ **2 instances (16.7, 16.8)** |

---

## Notes

- **Section 20 (Open Questions)** is correctly empty with a note pointing to Section 19.
- **Large files** (25_agent-operability.md at 4,942 lines) are appropriate given the feature scope; no actionable issues.
- **Cross-references** to non-existent subsections (e.g., Section 15.4.5) were verified to exist.
- **RLS, TOAST, JWT, RFC 8693, CloudEvents** and other domain terms are used correctly in context without requiring in-spec definitions.

