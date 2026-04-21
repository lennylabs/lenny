# Iteration 9 — API Design Regression Review (scope: `df0e675`)

**Perspective:** API Design
**Previous fix commit reviewed:** `df0e675` (iter8 fix)
**Scope:** Regressions-only, limited to API-relevant surfaces modified in `df0e675` (per scope directive):
- `spec/15_external-api-surface.md` §15.1 line 887 (`/close` real-call row — 404 RESOURCE_NOT_FOUND added), lines 1192-1193 (`/open` and `/close` dryRun rows — explicit simulation-object enumerations), line 1079 (`ELICITATION_CONTENT_TAMPERED` row description normalized)
- `spec/11_policy-and-controls.md` §11.6 line 313 (`/close` row — 404 RESOURCE_NOT_FOUND added)
- `docs/api/admin.md` line 66 (dryRun summary bullet), lines 750/755/770 (dryRun paragraphs)

**Findings numbering:** Continues from API-030; first available is API-031.

## Checklist results

1. **404 response-code consistency (`/close`) across §15.1 ↔ §11.6 ↔ docs/api/admin.md.**
   - `spec/15_external-api-surface.md` line 887: `Returns 404 RESOURCE_NOT_FOUND if no breaker is registered under {name} (no cb:{name} key exists in Redis)` — PASS.
   - `spec/11_policy-and-controls.md` line 313: `Returns 404 RESOURCE_NOT_FOUND if {name} has no persisted cb:{name} state in Redis` — PASS.
   - `docs/api/admin.md` line 766: `404 RESOURCE_NOT_FOUND — no breaker is registered under {name} (no cb:{name} key exists in Redis)` — PASS.
   - `docs/api/admin.md` line 770 (dryRun paragraph): `404 RESOURCE_NOT_FOUND if not` — PASS.
   - `spec/15_external-api-surface.md` line 1193 (dryRun row): `404 RESOURCE_NOT_FOUND if it does not` — PASS.
   - All four locations converge on the `RESOURCE_NOT_FOUND` error code and the `cb:{name}` Redis-key basis. No divergence.

2. **`RESOURCE_NOT_FOUND` error-catalog presence.**
   - `spec/15_external-api-surface.md` line 979: `RESOURCE_NOT_FOUND | PERMANENT | 404 | The requested resource does not exist or is not visible to the caller` — defined.
   - `docs/reference/error-catalog.md` line 72: `RESOURCE_NOT_FOUND | 404 | The requested resource does not exist or is not visible to the caller.` — defined.
   - PASS. The code used by `/close` is a pre-existing catalog entry; no new code introduced, no catalog gap.

3. **`/open` NOT claiming 404 (atomic-register semantics).**
   - `spec/15_external-api-surface.md` §15.1 line 886 (`/open` row): 422 INVALID_BREAKER_SCOPE only — no 404. PASS.
   - `spec/15_external-api-surface.md` line 1192 (`/open` dryRun row): 422 INVALID_BREAKER_SCOPE only. PASS.
   - `spec/11_policy-and-controls.md` line 312 (`/open` row): atomic-register semantics documented, 422 INVALID_BREAKER_SCOPE only — no 404. PASS.
   - `docs/api/admin.md` lines 719-755 (`/open` section): `Responses: 200 OK, 422 INVALID_BREAKER_SCOPE` — no 404 claim. PASS.
   - Atomic-register semantics of `/open` preserved; no regression.

4. **DryRun simulation-object shape enumeration.**
   - `/open`: five fields explicitly enumerated (`name`, `state` predicted `"open"`, `reason`, `limit_tier`, `scope`) in §15.1 line 1192 and docs/api/admin.md line 755 — PASS.
   - `/close`: four fields explicitly enumerated (`name`, `state` predicted `"closed"`, `limit_tier`, `scope`) in §15.1 line 1193 and docs/api/admin.md line 770 — PASS.
   - Neither row claims "mirrors the real-call shape" (the removed phrasing). Both use "reduced simulation object". PASS.

5. **Audit-field omission rationale on both dryRun rows.**
   - `/open` (§15.1 line 1192): `Audit-like fields of the real-call response (opened_at, opened_by_sub, opened_by_tenant_id) are not populated under dryRun because no state mutation occurs and no audit trail is recorded` — PASS.
   - `/close` (§15.1 line 1193): `No audit-like fields are populated since no state mutation occurs` — PASS (shorter phrasing but semantically equivalent, and the real-call shape of `/close` does not itself enumerate audit-like fields in the admin docs, so a briefer rationale is acceptable).
   - `docs/api/admin.md` lines 755 and 770 mirror the spec rationale. PASS.

6. **`ELICITATION_CONTENT_TAMPERED` description consistency between §15.1 and docs/reference/error-catalog.md.**
   - `spec/15_external-api-surface.md` line 1079: references `MCP elicitation/create wire frame (see [Section 15.2.1](#1521-restmcp-consistency-contract) per-kind wire projection)`, existing `elicitation_id`, `{message, schema}` pair, gateway-origin-binding invariant.
   - `docs/reference/error-catalog.md` line 108: references `MCP elicitation/create wire frame`, existing `elicitation_id`, `{message, schema}` pair, gateway-origin-binding invariant.
   - Both locations converge on the normalized `{message, schema}` tuple matching §8.5 authoritative tool schema. PASS.
   - §15.2.1 anchor (`REST/MCP Consistency Contract`) exists at line 1368 of `spec/15_external-api-surface.md` — link target present. PASS.

## Cross-check: summary-bullet tension with generic dryRun contract (observational, not a regression)

`docs/api/admin.md` line 60 (generic dryRun contract, pre-existing): "Response body is identical to a real success, plus `X-Dry-Run: true` header."
Line 66 (circuit-breaker exception, added/revised in `df0e675`): "The response body is a reduced simulation object (a subset of the real-call response fields — audit-like fields … are omitted …) plus a top-level `simulation` object …"

This creates a local contradiction only if one reads line 60 as universal. The text at line 66 is explicitly scoped as a circuit-breaker exception ("Supported on circuit-breaker actions:") and provides the field-level deviation. Prior iterations have accepted a similar split between the generic contract and per-endpoint exception blocks. **Not a regression** — the iter8 fix replaced the previous "mirrors the real-call shape" claim (which was itself inconsistent with audit-field omission) with a more precise "reduced simulation object" framing that narrows (rather than broadens) the generic-contract tension. Scope: regressions-only — no finding.

---

## Result

**No regressions detected.**

All six checklist items in the scope directive pass. The `df0e675` changes to §15.1, §11.6, and `docs/api/admin.md` are mutually consistent on:
- `/close` 404 `RESOURCE_NOT_FOUND` across spec §15.1, §11.6, and docs/api/admin.md (real-call rows, dryRun rows, and free-prose descriptions).
- `/open` retains atomic-register semantics; no 404 introduced on any `/open` surface.
- DryRun response-shape enumeration is explicit for both endpoints (5 fields for `/open`, 4 for `/close`), with the removed "mirrors the real-call shape" claim correctly absent from both rows.
- Audit-field omission rationale is present on both dryRun surfaces.
- `ELICITATION_CONTENT_TAMPERED` description aligns between spec §15.1 line 1079 and `docs/reference/error-catalog.md` line 108, both using the `{message, schema}` tuple.
- Error-catalog integrity: `RESOURCE_NOT_FOUND` is a pre-existing catalog entry (no new code introduced).

No Critical/High/Medium regressions.
