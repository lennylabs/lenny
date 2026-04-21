# Iteration 9 — Compliance (CMP) Regression Review

**Scope:** Regressions introduced by iter8 fix commit `df0e675`, CMP-relevant surfaces only.
**Prior-iteration last finding ID:** CMP-063.

## Commit surface inspected

- `docs/reference/error-catalog.md` line 108 — `ELICITATION_CONTENT_TAMPERED` description updated: `{title, description, schema, inputs}` prose replaced by `{message, schema}` + explicit "MCP `elicitation/create` wire frame" framing (SES-011 follow-through).

## Cross-surface consistency checks

1. **Authoritative spec §15.1 row** (`spec/15_external-api-surface.md:1079`) — carries `PERMANENT` category, 409 HTTP status, `{message, schema}` vocabulary, and the same gateway-origin-binding-invariant framing. Matches the updated docs row.
2. **Authoritative narrative §9.2** (`spec/09_mcp-integration.md:56`) — "Elicitation content integrity (gateway-origin binding)" paragraph defines the invariant in terms of the original `{message, schema}` pair recorded at origination, with the `elicitation/create` wire-frame re-emission as the tamper-detection trigger. Matches the updated docs row's framing verbatim.
3. **Paired §16.7 audit event** `elicitation.content_tamper_detected` (`spec/16_observability.md:664`) — `divergent_fields` enumeration post-SES-011 names only `message`, `schema`; `original_sha256` / `attempted_sha256` canonicalize over the `{message, schema}` pair. No field-vocabulary contradiction remains between audit event, error code, and narrative.
4. **Paired `lenny_elicitation_content_tamper_detected_total` / `ElicitationContentTamperDetected` alert** (`docs/reference/metrics.md:251, 477`) — already synced to the `{message, schema}` vocabulary pre-df0e675; consistent with the updated row.

## Categorization / retry-mode integrity

- `PERMANENT` categorization preserved — the docs table sits under the "## PERMANENT errors" heading (`docs/reference/error-catalog.md:63`) and the §15.1 spec row still reads `PERMANENT`.
- `409` HTTP status preserved on both the docs row and the §15.1 spec row.
- No retry-mode assumption drift — the retry guidance ("emit a new `lenny/request_elicitation` establishing a fresh `elicitation_id`; do not rewrite an existing one") is semantically identical pre- and post-fix and remains consistent with the §9.2 "not retryable as-is" wording on the spec-side row.

## New-surface audit check (FMR-024 addition)

The iter8 commit also added a new §16.7 audit event `deployment.feature_flag_downgrade_acknowledged` (`spec/16_observability.md:669`). This is a deployment-scope operator-initiated lifecycle event written under the platform tenant; no tenant PII, no residency/legal-hold/data-protection surface touched. Entry follows the existing catalogue conventions (`Retained under audit.gdprRetentionDays`, standard append-only path, paired to an operational runbook). Confirms the task-prompt's assessment: operations-scope, no CMP surface engaged.

## Regressions flagged

**None.**

No regressions detected — iter8 CMP-relevant surfaces pass inspection.
