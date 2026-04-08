# Review Findings — Perspective 5: Protocol Design & Future-Proofing

**Spec file:** `technical-design.md` (8,691 lines)
**Iteration:** 14
**Reviewer focus:** MCP, A2A, AP, OpenAI protocol strategy; abstraction layer; translation fidelity; spec evolution risk
**Prior finding status:** PRT-035 (artifact:// URI scheme x2) — **FIXED** (no `artifact://` references remain in spec)

---

## Findings

### PRT-036 — Translation Fidelity Matrix tags MCP `ref` as `[exact]` despite mandatory dereference (MEDIUM)

**Location:** Section 15.4.1, Translation Fidelity Matrix (line ~6904) vs. Section 15.4.1 blob dereference obligation (line ~6846) and Section 15.1 blob resolution endpoint (line ~6163)

**Problem:** The Translation Fidelity Matrix tags the `ref` (`lenny-blob://` URI) field for MCP as **`[exact]`** with the note "mapped to MCP `resource.uri`." However, two separate normative statements in the same spec require MCP adapters to dereference `ref` before sending:

1. Line ~6846: "External protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields before serializing outbound messages to external clients — external protocols do not speak `lenny-blob://`."
2. Line ~6163: "external protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields internally and MUST NOT pass `lenny-blob://` URIs to external callers."

The matrix cell itself even acknowledges this: "Adapters dereference before sending to external MCP clients (see SCH-005 resolution protocol)." But it still carries the `[exact]` tag.

If the adapter resolves the `lenny-blob://` URI to inline content before sending to the MCP client, the URI reference is lost on the wire — the round-trip is not reversible. This matches the behavior described for OpenAI Completions, which is correctly tagged `[dropped]`.

**Impact:** Implementers consulting the fidelity matrix will incorrectly believe `ref` survives a MCP round-trip. Clients building on this assumption (e.g., storing `ref` URIs and expecting to recover them from MCP responses) will encounter silent data loss.

**Suggested fix:** Change the MCP column for `ref` from `[exact]` to `[dropped]` (or `[lossy]` if the resolved content is inlined with a different representation). Update the description to: "Adapter dereferences `lenny-blob://` URI and inlines resolved content before sending to external MCP clients. Round-trip: `ref` scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part." This aligns with the normative dereference obligation and matches the OpenAI treatment.

---

## Summary

| # | ID | Severity | Finding | Section |
|---|--------|----------|---------|---------|
| 1 | PRT-036 | MEDIUM | Translation Fidelity Matrix tags MCP `ref` as `[exact]` but normative rules require adapters to dereference `lenny-blob://` URIs before sending to MCP clients — should be `[dropped]` | 15.4.1 |

**Resolved from prior iteration:** PRT-035 (artifact:// URI scheme x2) — FIXED
**New findings:** 1 (0 Critical, 0 High, 1 Medium)
