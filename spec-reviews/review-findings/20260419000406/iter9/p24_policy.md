# POL Review — Iteration 9 (regressions-only)

**Scope:** regressions introduced by iter8 fix commit `df0e675`.
**Surfaces reviewed:** `spec/11_policy-and-controls.md` §11.6 line 313 (`/close` row 404 addition, API-029 fix).
**Cross-checks performed:**
- `spec/15_external-api-surface.md` §15.1 `/close` real-call row (line 887) — 404 `RESOURCE_NOT_FOUND` present, matches §11.6.
- `spec/15_external-api-surface.md` §15.1 dryRun row for `/close` (line 1193) — 404 `RESOURCE_NOT_FOUND` present, consistent with real-call row.
- `spec/15_external-api-surface.md` §15.1 error catalog line 979 — `RESOURCE_NOT_FOUND` already codified as 404.
- `spec/15_external-api-surface.md` §15.1 scope taxonomy line 915 — `circuit_breaker` domain unchanged; mirrored in `spec/25_agent-operability.md` line 79.
- `spec/25_agent-operability.md` §25.12 MCP tool row for `lenny_circuit_breaker_close` (line 4475) — tool→endpoint mapping unchanged; summary row does not enumerate response codes (consistent with peer rows).

**Iter8 POL delta:** one targeted addition — a 404 `RESOURCE_NOT_FOUND` response clause inserted into the §11.6 `/close` row. No changes to scope taxonomy, admission evaluation prose, audit-event payloads, sampling semantics, or observability fields.

## Findings

No regressions detected.
