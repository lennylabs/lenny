# Content Model, Data Formats & Schema Design Review — Iteration 2
**Date:** 2026-04-19 | **Spec sections:** 14, 15.4.1, 26 | **Prior iter:** iter1/CNT.md (CNT-001 temperature range — explanatory note added; confirmed resolved)

---

### CNT-002 WorkspacePlan JSON Schema Coverage Claim Contradicts the Canonical Example [REAL ERROR]

**Files:** `14_workspace-plan-schema.md`

**Description:** Section 14.1 declares that the published JSON Schema at `https://schemas.lenny.dev/workspaceplan/v1.json` covers the full `WorkspacePlan` object, enumerating: `sources[]`, `setupCommands[]`, `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, and `delegationLease`.

However, the canonical request example in the same section (lines 7–81) places **only** `$schema`, `schemaVersion`, `sources`, and `setupCommands` **inside** the `workspacePlan` object. All other listed fields (`env`, `labels`, `runtimeOptions`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `delegationLease`) are **siblings** of `workspacePlan` at the top-level request body.

**Quote from spec (§14 example, abbreviated):**
```json
{
  "pool": "...",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "$schema": "...",
    "schemaVersion": 1,
    "sources": [...],
    "setupCommands": [...]
  },
  "env": { ... },                     // OUTSIDE workspacePlan
  "labels": { ... },                  // OUTSIDE workspacePlan
  "runtimeOptions": { ... },          // OUTSIDE workspacePlan
  "timeouts": { ... },                // OUTSIDE workspacePlan
  "retryPolicy": { ... },             // OUTSIDE workspacePlan
  "credentialPolicy": { ... },        // OUTSIDE workspacePlan
  "callbackUrl": "...",               // OUTSIDE workspacePlan
  "callbackSecret": "...",            // OUTSIDE workspacePlan
  "delegationLease": { ... }          // OUTSIDE workspacePlan
}
```

Section 14.1 states the JSON Schema "covers" all these fields as part of the `WorkspacePlan` object, but then says the gateway "performs identical validation at `POST /v1/sessions`" — i.e., validates against the full request body, not just the `workspacePlan` sub-object. The terminology conflates two distinct envelopes: the **`WorkspacePlan` proper** (sources, setupCommands, schemaVersion) and the **session-creation request body** that embeds it (env, labels, timeouts, callback*, delegationLease, runtimeOptions, etc.).

**Impact:** (1) Clients implementing local validation against the published schema will not know which fields the schema describes. (2) `WORKSPACE_PLAN_INVALID` (§15.4 error table) and `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (§14.1 versioning) become ambiguous — do they apply to the whole request body or just the inner plan? (3) The `$schema` keyword is described as reference-able "on their `workspacePlan` object", which is inconsistent with a schema that covers outer fields like `callbackUrl`.

**Recommendation:** Either (a) move `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, `delegationLease` **inside** the `workspacePlan` object in the example so the structure matches the schema claim, or (b) rename the outer envelope (e.g., `CreateSessionRequest`) and clarify that the published schema covers only the inner `WorkspacePlan` sub-object (sources, setupCommands, schemaVersion), with sibling fields documented under a separate envelope schema. Option (b) better matches common REST patterns and the shape shown in §26.2 where only `sources`/`setupCommands` live under `workspacePlan`.

---

### CNT-003 `gitClone` Source Type Referenced But Not Catalogued in Section 14 [MINOR — DOCUMENTATION GAP]

**Files:** `14_workspace-plan-schema.md`, `26_reference-runtime-catalog.md`

**Description:** Section 26 (lines 43, 111, 202) refers to a `WorkspacePlan.sources[].type: gitClone` source type as a real v1 feature — credential-lease scopes (`vcs.github.read`/`vcs.github.write`) are only issued when this source type is present. However, Section 14's `sources` catalogue (shown only by example, lines 14–39) enumerates `inlineFile`, `uploadFile`, `uploadArchive`, and `mkdir` — not `gitClone`.

Section 14 explicitly makes `source.type` an open string (line 305), so `gitClone` is not *forbidden*. But Section 14 is the section documenting the WorkspacePlan schema; a v1 feature relied upon by the reference runtime catalogue and the credential-leasing service should have its shape (required fields: `url`, `ref`, `auth`? `depth`? `submodules`?) defined here, otherwise the canonical JSON Schema cannot include a proper `if/then` branch for it and implementations will diverge.

**Recommendation:** Add a `gitClone` entry to Section 14's sources catalogue documenting its required and optional fields (URL, ref/branch, depth, auth mode, submodule handling), or explicitly note that `gitClone` is deferred post-v1 and update Section 26 accordingly.

---

### CNT-004 Translation Fidelity Matrix Header Includes Post-V1 Adapter [MINOR — DOCUMENTATION CONSISTENCY]

**Files:** `15_external-api-surface.md`

**Description:** Section 15.4.1 "Translation Fidelity Matrix" (line 1104) says the matrix covers "each built-in adapter". The built-in adapter inventory (line 166, line 177) lists MCP, OpenAI Completions, Open Responses as v1 built-ins, and `A2AAdapter` as **Post-V1**. The matrix columns (line 1116) are MCP, OpenAI Completions, Open Responses, REST, A2A — i.e., the v1 built-ins plus REST (v1, documented under §15.1) plus A2A (post-v1). This is not a logic bug but is terminology-imprecise: A2A is not a v1 built-in adapter yet the matrix presents its fidelity as normative-looking v1 content alongside the shipping adapters.

**Recommendation:** Either qualify the matrix preamble (e.g., "each built-in adapter, plus the REST surface, plus the Post-V1 A2A adapter for forward planning"), or move the A2A column to a clearly-labelled "Planned (Post-V1)" sub-section so implementers know the A2A column is a design target, not a shipping contract.

---

## Summary

Three findings. **CNT-002** is the most important regression-class issue: Section 14.1 claims the published JSON Schema covers fields that the section's own example places outside the `workspacePlan` object. This creates contradictions between the example, the schema claim, and error-code scope. **CNT-003** flags a documentation gap where a credential-relevant source type (`gitClone`) used by the reference runtime catalogue is not catalogued in the authoritative WorkspacePlan section. **CNT-004** is a minor terminology issue where a post-v1 adapter (A2A) sits in a matrix described as "built-in adapters". OutputPart schemaVersion rules, MessageEnvelope sufficiency (including DAG threading, `delegationDepth`, `delivery` semantics), canonical type registry translation, and `RuntimeDefinition` inheritance/merge rules remain internally consistent. CNT-001 (temperature mismatch) was resolved via explanatory note in iter1 and remains cleanly addressed.
