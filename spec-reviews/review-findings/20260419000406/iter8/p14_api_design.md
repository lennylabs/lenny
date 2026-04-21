# Perspective 14 — API Design & External Interface Quality (iter8 — regressions-only scope)

**Scope directive.** Per `feedback_iter8_regressions_only.md`, this review inspects only regressions introduced by the previous fix commit `bed7961` ("Fix iteration 7"). The iter7 disposition of API-017/018/019/020/021/022/023/024/025/026/027/028 is not re-evaluated; only new defects inside the bed7961 fix envelope are flagged.

**Surfaces touched by bed7961 relevant to this perspective.**

- `spec/15_external-api-surface.md` §15.1 — circuit-breakers endpoint-table rows at lines 884-887 extended with `x-lenny-scope`, `x-lenny-mcp-tool`, `x-lenny-category` in-line declarations; dryRun supported-endpoints table extended with `.../open` (line 1192) and `.../close` (line 1193); dryRun exclusion paragraph (line 1197) updated to carve out the two circuit-breaker action endpoints; two new error rows `ELICITATION_CONTENT_TAMPERED` (line 1079) and `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` (line 1091).
- `spec/11_policy-and-controls.md` §11.6 — circuit-breakers admin-API table reshaped from 3-column to 4-column `(Method | Path | x-lenny-scope | Description)` at lines 308-313.
- `spec/25_agent-operability.md` §25.1 — scope-taxonomy domain list mirrored to include `circuit_breaker` at line 79; §25.12 MCP tool inventory extended with four new rows (`lenny_circuit_breaker_list`/`_get` as read tools at lines 4426-4427; `lenny_circuit_breaker_open`/`_close` as action tools at lines 4474-4475).
- `docs/api/admin.md` — circuit-breakers endpoint table extended with a `Scope (x-lenny-scope)` column and GET rows narrowed from `platform-admin, tenant-admin` to `platform-admin` only (lines 712-717); new close-endpoint subsection added at lines 757-770; generic dryRun intro extended with a "Supported on circuit-breaker actions" paragraph at line 66.

**iter7 finding → bed7961 verification.** Each iter7 API-026/027/028 recommendation is resolved on `main` at the surfaces above: docs GET rows now advertise `platform-admin` only (matches spec); per-endpoint `x-lenny-scope` bindings are declared in three mutually-consistent locations; dryRun catalog decision is taken (Option b, both `open` and `close` admitted as supported with a `simulation` object shape). No iter7 API finding is stuck uncorrected.

**Numbering.** iter7 P14 ended at API-028. iter8 API findings begin at API-029.

---

## Regressions introduced by bed7961

### API-029. `POST /v1/admin/circuit-breakers/{name}/close` real-call 404 response documented in docs and dryRun row but absent from the spec's real-call rows [Medium] **[Fixed]**

**Fix summary.** Added the 404-on-unknown-name response to both authoritative spec rows. `spec/15_external-api-surface.md:887` (§15.1 real-call row) now reads: "Returns `404 RESOURCE_NOT_FOUND` if no breaker is registered under `{name}` (no `cb:{name}` key exists in Redis)." `spec/11_policy-and-controls.md:313` (§11.6 policy row) now reads: "Returns `404 RESOURCE_NOT_FOUND` if `{name}` has no persisted `cb:{name}` state in Redis." Error-code vocabulary was unified on the canonical `RESOURCE_NOT_FOUND` code from the §15.1 error catalog (line 979) — the prior `NOT_FOUND` short-form in `docs/api/admin.md:766/770` and `spec/15_external-api-surface.md:1193` (dryRun row) was rewritten to `RESOURCE_NOT_FOUND` for catalog-alignment. The three-way asymmetry is eliminated: docs, dryRun row, and both authoritative spec rows now consistently document the 404 path with the same canonical error code. `/open` was verified to not require a 404 row — it atomically registers the breaker on unknown names, per its documented semantics at `spec/11_policy-and-controls.md:312` and `docs/api/admin.md:747` — so the same class of asymmetry does not apply there.

**Section:** `spec/15_external-api-surface.md:887` (§15.1 endpoint row for close), `spec/11_policy-and-controls.md:313` (§11.6 admin-API row for close), `spec/15_external-api-surface.md:1193` (§15.1 dryRun row for close), `docs/api/admin.md:764-766` (real-call response table for close).

bed7961 introduces inconsistent documentation of the close endpoint's 404 response surface. Three locations now reference or imply a 404-on-unknown-name behavior, and two spec-authoritative locations are silent about it:

```
docs/api/admin.md:764-766  Responses:
docs/api/admin.md:765        - `200 OK` — breaker is closed.
docs/api/admin.md:766        - `404 NOT_FOUND` — no breaker is registered under `{name}`.

spec/15_external-api-surface.md:1193  | `POST` | `/v1/admin/circuit-breakers/{name}/close` | Validates that `{name}` exists in Redis (`404 NOT_FOUND` if it does not) and reads its persisted `limit_tier`/`scope`; does **not** write Redis. ...
```

Neither the non-dryRun row at `spec/15_external-api-surface.md:887` nor the §11.6 row at `spec/11_policy-and-controls.md:313` documents a `404 NOT_FOUND` response code for the real-call path against an unknown `{name}`. Line 887 describes only the success semantics ("Close (deactivate) a circuit breaker. Body is empty. The persisted `limit_tier` and `scope` are retained."); line 313 similarly enumerates only the success behavior and the forward cross-reference to `INVALID_BREAKER_SCOPE` on the subsequent `open`.

Pre-bed7961, `docs/api/admin.md` did not contain a close-endpoint subsection at all (only `.../open` was documented); the Responses table at lines 764-766 was created by bed7961 itself. The dryRun row at line 1193 was also added by bed7961. Both new artifacts reference a 404-on-unknown-name behavior that is not established at the authoritative spec row.

This produces a three-way asymmetry:

1. The dryRun row's "404 NOT_FOUND if it does not" claim presumes that the real-call path already returns 404 for unknown names (dryRun's documented job is to mirror real-call validation).
2. The operator-facing docs Responses table states the real-call returns 404 for unknown names.
3. The spec's authoritative real-call rows do not state the 404 path, leaving the contract ambiguous.

If a future spec edit relied on the §15.1 / §11.6 rows as the authoritative close-endpoint response envelope (e.g., for OpenAPI-to-MCP generation per the §15.1 "Admin API MCP extension contract" at line 921), the 404 path would be elided from the generated OpenAPI. Third-party tooling consuming `/v1/openapi.json` would see a `200`-only response surface while the docs and dryRun surface promise 404 — a spec/doc drift identical in kind to iter7 API-026 (docs advertising a role the spec does not grant) but in the opposite direction (docs advertising a response code the spec does not list).

This is the exact class of three-surface sync defect the iter7 fix cycle was intended to eliminate. The fix for iter7 API-028 correctly made a decision about dryRun admissibility; it simultaneously introduced a 404-behavior claim in docs and dryRun text while leaving the real-call spec rows silent.

**Recommendation.** Reconcile by adding the 404 response to the spec's authoritative real-call rows:

- Append to `spec/15_external-api-surface.md:887` description: "Returns `404 NOT_FOUND` if no breaker is registered under `{name}`."
- Append to `spec/11_policy-and-controls.md:313` description: "Returns `404 NOT_FOUND` if `{name}` has no persisted state."

Alternatively (if the real-call is intended to be idempotent against unknown names, returning 200), remove the 404 references from `docs/api/admin.md:766` and `spec/15_external-api-surface.md:1193`. Either resolution is acceptable; the current state is not.

**Severity: Medium** — same class of surface-sync defect iter7 API-026 was graded Medium at (docs advertising a contract the spec does not codify), in the opposite direction. Affects OpenAPI generation if the spec rows drive schema emission.

---

### API-030. `POST /v1/admin/circuit-breakers/{name}/open` dryRun response-shape enumeration omits `opened_at` / `opened_by_sub` / `opened_by_tenant_id` despite claiming to mirror the real-call shape [Medium] **[Fixed]**

**Resolution:** Selected reconciliation (b) from the Recommended fix — dropped the "mirrors the real-call shape" phrasing at `spec/15_external-api-surface.md:1192` for `/open` and replaced it with an explicit statement that the dryRun response is a reduced simulation object with exactly five fields (`name`, `state`, `reason`, `limit_tier`, `scope`), and that the three audit-like fields (`opened_at`, `opened_by_sub`, `opened_by_tenant_id`) are **not** populated under `dryRun` because no state mutation occurs and no audit trail is recorded. Applied the parallel treatment to the `/close` dryRun row at `spec/15_external-api-surface.md:1193` (four-field explicit enumeration plus audit-field-omission rationale). Mirrored both in `docs/api/admin.md` at §750 (per-endpoint `/open` dry-run paragraph), §770 (per-endpoint `/close` dry-run paragraph), and §66 (supported-endpoints summary). Regression-checked: zero remaining "mirrors the real-call shape" occurrences under `spec/` or `docs/`.

**Section:** `spec/15_external-api-surface.md:1192` (dryRun row for open) vs. `docs/api/admin.md:750` (real-call response body for open).

The bed7961 dryRun row for `POST /v1/admin/circuit-breakers/{name}/open` asserts that the dryRun response body "mirrors the real-call shape" and then enumerates the shape parenthetically:

```
spec/15_external-api-surface.md:1192  | `POST` | `/v1/admin/circuit-breakers/{name}/open` | ... Response body mirrors the real-call shape (`name`, `state` — predicted `"open"`, `reason`, `limit_tier`, `scope`) plus a top-level `simulation` object: ...
```

Enumerated shape: `name`, `state`, `reason`, `limit_tier`, `scope` (five fields).

The authoritative real-call response body documented in `docs/api/admin.md:750` (which was itself authored by bed7961) is broader:

```
docs/api/admin.md:750  `200 OK` — breaker is open. Response body: `{ "name": "<name>", "state": "open", "reason": "...", "opened_at": "...", "opened_by_sub": "...", "opened_by_tenant_id": "...", "limit_tier": "...", "scope": {...} }`
```

Real-call shape per docs: `name`, `state`, `reason`, `opened_at`, `opened_by_sub`, `opened_by_tenant_id`, `limit_tier`, `scope` (eight fields).

The dryRun parenthetical omits `opened_at`, `opened_by_sub`, and `opened_by_tenant_id`. Third-party tooling reading the §15.1 dryRun row literally — in particular OpenAPI-schema generators or MCP-tool-schema builders that treat the parenthetical as an authoritative field list — will emit a dryRun response shape missing three fields that the real-call returns. Tooling that diffs dryRun against real-call for invariant checks (e.g., `X-Dry-Run: true` header comparability tests) will report a spurious shape mismatch.

Two secondary problems follow from the same text:

1. **`opened_at` under dryRun is semantically ambiguous.** The real-call `opened_at` records the Redis-write timestamp; dryRun performs no Redis write, so the field's value under dryRun is either (a) the current wall time (which misleadingly conveys "this is when we'd write"), (b) `null`, (c) a mirror of the currently-persisted `opened_at` for an already-open breaker (idempotent no-op case), or (d) omitted. The dryRun row does not say.
2. **`opened_by_sub` / `opened_by_tenant_id` under dryRun.** These fields reflect the caller identity of the write-that-wasn't. dryRun would have them populated from the caller's identity even though the write did not occur. The row does not establish this.

Either outcome — the dryRun returns all eight fields (the literal reading of "mirrors the real-call shape") or a strict subset (five fields per the parenthetical enumeration) — is defensible, but the two statements in the same row contradict each other.

**Recommendation.** Choose one of:

- **Option (a):** Expand the parenthetical at `spec/15_external-api-surface.md:1192` to include all eight fields: `(name, state, reason, opened_at, opened_by_sub, opened_by_tenant_id, limit_tier, scope)`. Add a sentence clarifying the dryRun semantics of `opened_at` (e.g., "under dryRun, `opened_at` is the wall-clock time the dryRun evaluation ran; no Redis write occurs"), `opened_by_sub`, and `opened_by_tenant_id` (populated from the caller even though no write occurs).
- **Option (b):** Drop the parenthetical entirely: "Response body mirrors the real-call shape documented in the `POST /v1/admin/circuit-breakers/{name}/open` response body reference plus a top-level `simulation` object: …". This makes `docs/api/admin.md:750` the single source-of-truth for the response shape and the dryRun row does not need to be edited whenever the real-call shape grows.

Option (b) is structurally safer (eliminates a future drift vector) and is consistent with the §15.1 "Admin API MCP extension contract" philosophy that OpenAPI is the single source-of-truth for response shapes (line 921).

**Severity: Medium** — catalog-integrity gap equivalent to iter7 API-028 (dryRun row making a shape/behavior claim that is not consistent with the real-call authoritative location). Affects dryRun-vs-real-call invariant testing and OpenAPI emission for the endpoint's response schema.

---

## Summary

**Scope of verification.** All iter7 API-026/027/028 recommendations are resolved in bed7961 at the three surfaces (spec §15.1, spec §11.6, docs/api/admin.md). No pre-existing iter5/iter6 Low carry-forwards are re-evaluated under the iter8 regressions-only scope.

**Regressions identified.** Two Medium regressions, both scoped to the circuit-breakers close endpoint's surface-sync between the three authoritative locations bed7961 was obligated to reconcile:

| Finding | Severity | Surface |
| --- | --- | --- |
| API-029 | Medium | Close endpoint 404 response documented in docs and dryRun row but absent from spec real-call rows (§15.1 / §11.6) **[Fixed]** |
| API-030 | Medium | Open endpoint dryRun response-shape parenthetical enumerates 5 of the 8 real-call fields despite claiming to mirror the real-call shape **[Fixed]** |

Both are the exact class of "fix introduced incomplete cross-surface reconciliation" that the iter7 fix cycle was intended to eliminate — the same class as iter7 API-026 (docs advertising a surface the spec does not carry) and iter7 API-028 (dryRun catalog silent on a new endpoint). The iter8 fix for these should edit only `spec/15_external-api-surface.md:887`, `spec/11_policy-and-controls.md:313`, and either expand or defer the parenthetical enumeration at `spec/15_external-api-surface.md:1192`.

**Verdict: Not converged — 2 Medium regressions inside the bed7961 fix envelope.**

---

**Perspective:** 14 — API Design & External Interface Quality
**Category:** API
**Count:** 2 (API-029, API-030 — both Medium)
**Path:** `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter8/p14_api_design.md`
