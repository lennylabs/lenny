# Iter8 Review — Perspective 24: Policy Engine & Admission Control

**Scope directive:** iter8+ regressions-only, relative to the previous fix commit `bed7961`.

**Previous iter7 findings reference:** `spec-reviews/review-findings/20260419000406/iter7/p24_policy.md` (5 Low carry-forwards POL-029..POL-033 + the iter7-new POL-034 closed by OBS-041 fix on the same docs row).

## Surfaces touched by `bed7961` that fall under POL

1. `spec/11_policy-and-controls.md` §11.6 "Admin API" table at lines 306–313 — added `x-lenny-scope` column with `tools:circuit_breaker:read|write` bindings for the four circuit-breaker admin endpoints.
2. `spec/25_agent-operability.md` §25.1 domain-list mirror at line 79 — added `circuit_breaker` to the closed domain list and pointed to §15.1 as the authoritative source.
3. `spec/25_agent-operability.md` §25.12 MCP tool inventory at lines 4426–4427 (Read Tools) and 4474–4475 (Action Tools) — added four new rows `lenny_circuit_breaker_list|get|open|close` carrying the matching scope strings.
4. `spec/15_external-api-surface.md` §15.1 at line 915 — added `circuit_breaker` to the scope taxonomy domain list; at lines 884–887, augmented the four circuit-breaker rows with `x-lenny-scope`, `x-lenny-mcp-tool`, `x-lenny-category` declarations.
5. `docs/operator-guide/observability.md` quota user fail-open alert row — POL-034-class remediation of `QuotaFailOpenUserFractionInoperative` to use the continuous PromQL `lenny_quota_user_failopen_fraction >= 0.5`.

## Regression checks (iter8 scope directive § 3)

### Check 1 — `circuit_breaker` added to the §25.1 closed-enumeration domain list and cross-references §15.1 as authoritative

**Result:** Pass. `spec/25_agent-operability.md:79` now lists `circuit_breaker` as the last domain in the enumeration and appends the sentence "The authoritative closed list lives at [Section 15.1](15_external-api-surface.md#151-rest-api) Scope taxonomy; this paragraph mirrors it." The mirror relationship is declared explicitly, so a future domain-list divergence surfaces as a docs-sync issue rather than a silent authoritative-source ambiguity.

`spec/15_external-api-surface.md:915` includes `circuit_breaker` at the end of the Domains bullet. The two domain lists are position-identical (`..., runtime, quota, config, circuit_breaker`). No regression.

### Check 2 — §11.6 lines 306–313 scope bindings match §25.12 MCP tool rows by (domain, read/write split)

**Result:** Pass. The five-element cross-surface correspondence is:

| Endpoint | §11.6 scope | §15.1 scope | §15.1 mcp-tool | §15.1 category | §25.12 MCP row scope |
|---|---|---|---|---|---|
| `GET /v1/admin/circuit-breakers` | `tools:circuit_breaker:read` | `tools:circuit_breaker:read` | `lenny_circuit_breaker_list` | `observation` | `tools:circuit_breaker:read` |
| `GET /v1/admin/circuit-breakers/{name}` | `tools:circuit_breaker:read` | `tools:circuit_breaker:read` | `lenny_circuit_breaker_get` | `observation` | `tools:circuit_breaker:read` |
| `POST /v1/admin/circuit-breakers/{name}/open` | `tools:circuit_breaker:write` | `tools:circuit_breaker:write` | `lenny_circuit_breaker_open` | `destructive` | `tools:circuit_breaker:write` |
| `POST /v1/admin/circuit-breakers/{name}/close` | `tools:circuit_breaker:write` | `tools:circuit_breaker:write` | `lenny_circuit_breaker_close` | `mutation` | `tools:circuit_breaker:write` |

The scope string `tools:circuit_breaker:read|write` agrees across §11.6 ↔ §15.1 ↔ §25.12 for each endpoint, with the `read`/`write` split mirroring the GET-vs-POST verb split. The `lenny_circuit_breaker_<tool>` names are consistent across §15.1 and §25.12. No regression.

The asymmetric category choice (`open` → `destructive`, `close` → `mutation`) is defensible — opening a breaker actively denies admission (higher blast radius and categorized alongside `backup`/`restore`/`upgrade` per §25.12 line 4608), closing restores normal admission and is a routine state change. The asymmetry is consistent with the §25.12 category rubric and not a regression.

### Check 3 — `lenny_quota_user_failopen_fraction` alert PromQL uses a metric that is defined in §16

**Result:** Pass. The metric is defined at `spec/16_observability.md:204` (gauge without labels, default `0.25`, emitted at startup and on config reload). The alert at `spec/16_observability.md:453` uses the concrete expression `lenny_quota_user_failopen_fraction >= 0.5`. The iter7 POL-034 docs-sync remediation at `docs/operator-guide/observability.md:191` now reads `lenny_quota_user_failopen_fraction >= 0.5 — continuously-firing Prometheus alert`, matching the spec alert expression verbatim. The metric-gauge ↔ alert-PromQL ↔ docs-sync trio is aligned. No regression.

### Check 4 — scope name consistency across §11.6 ↔ §15.1 ↔ §25

**Result:** Pass. A `grep` for `tools:circuit_breaker` across `spec/` returns eight hits (§11.6 lines 310–313, §15.1 lines 884–887, §25.12 lines 4426–4427 and 4474–4475) — all use identical `tools:circuit_breaker:read` or `tools:circuit_breaker:write` spelling, matching the domain-list entry `circuit_breaker`. No regression.

## Convergence assessment

**No regressions detected.** The four circuit-breaker scope bindings added by `bed7961` at `spec/11_policy-and-controls.md:306–313` are internally consistent (§11.6 scope column matches the §15.1 and §25.12 scope strings for every endpoint, with the read/write split mirroring the HTTP verb split), consistent with the `circuit_breaker` domain newly added to the §15.1 / §25.1 closed-enumeration domain lists, and the alert PromQL `lenny_quota_user_failopen_fraction >= 0.5` at `docs/operator-guide/observability.md:191` uses a metric that is defined at `spec/16_observability.md:204` and wired to the §16.5 alert at `spec/16_observability.md:453`.

Iter7 open items POL-029..POL-033 are outside iter8's regressions-only scope (they pre-date `bed7961`) and are not re-listed here; they remain tracked in the iter7 findings file.

No Critical, High, or Medium regressions introduced by `bed7961`.
