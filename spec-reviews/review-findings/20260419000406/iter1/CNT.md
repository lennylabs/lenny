# Content Model, Data Formats & Schema Design Review
**Date:** 2026-04-19 | **Spec sections:** 14 (WorkspacePlan Schema), 15 (External API), 05 (Runtime Registry)

---

### CNT-001 RuntimeOptions Schema Temperature Range Mismatch [REAL ERROR]

**Files:** `14_workspace-plan-schema.md`

**Description:** The `claude-code` runtime's `runtimeOptions` schema declares `temperature` with bounds `{ "type": "number", "minimum": 0, "maximum": 1 }` (line 150), while all other first-party runtimes declare `{ "type": "number", "minimum": 0, "maximum": 2 }`:

- `openai-assistants`: `"maximum": 2` (line 181)
- `gemini-cli`: `"maximum": 2` (line 199)
- `codex`: `"maximum": 2` (line 215)
- `chat`: `"maximum": 2` (line 243)

This is a **field type mismatch across runtime schema definitions**. Either `claude-code` should match the other runtimes at `maximum: 2` (likely correct, as Claude models support temperature up to 2.0), or the others should be restricted to 1.0. No documentation explains the discrepancy.

**Quote from spec:**
```json
// claude-code (line 150)
"temperature": { "type": "number", "minimum": 0, "maximum": 1, "description": "Sampling temperature" }

// openai-assistants (line 181)
"temperature": { "type": "number", "minimum": 0, "maximum": 2 }
```

**Recommendation:** Verify intent with model team. If Claude models legitimately support `maximum: 2`, change `claude-code` to `"maximum": 2` for consistency. If intentional, add a comment explaining why `claude-code` differs.

---

## Summary

One real error found: a field type mismatch in the `temperature` constraint across runtime `runtimeOptions` schemas. The `claude-code` runtime restricts temperature to `[0, 1]` while all other runtimes allow `[0, 2]`, with no documented rationale. All other schema versioning strategy, OutputPart translation fidelity, MessageEnvelope sufficiency, and RuntimeDefinition inheritance rules are internally consistent and correctly specified.
