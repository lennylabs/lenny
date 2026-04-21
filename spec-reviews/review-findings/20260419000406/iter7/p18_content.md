# Perspective 18 — Content Model, Data Formats & Schema Design (Iter7)

Scope: `WorkspacePlan` (§14), `WorkspacePlan JSON Schema` (§14.1), `OutputPart` / `MessageEnvelope` (§15.4.1), `RuntimeDefinition` / `runtimeOptionsSchema` (§5.1). Iter7 verifies the iter6 fixes (commit 8604ce9) for CNT-020, CNT-023, CNT-024 end-to-end; re-audits the iter6 documentation-only carry-forwards (CNT-017, CNT-018, CNT-019) and the iter6 docs-sync carry-forward (CNT-025); and examines whether the iter6 CNT-020 fix introduced any new regressions in §14.

## Iter6 carry-over verification

| Iter6 ID | Title (summary) | Severity | Iter7 status |
|---|---|---|---|
| CNT-017 | §14 `gitClone.auth` paragraph still lacks session-binding sentence (iter5 CNT-012, iter4 CNT-008, iter3 CNT-007 unfixed) | Low | **Open** — carried forward as **CNT-026** below. §14 line 95 still has no `credential.leased` / session-binding sentence; `spec/26_reference-runtime-catalog.md:119` continues to carry the paired sentence. Fifth consecutive iteration without fix. |
| CNT-018 | §14 workspace-plan warning events absent from §16.6 Operational Events Catalog (iter5 CNT-013, iter4 CNT-010 unfixed) | Low | **Open** — carried forward as **CNT-027** below. `spec/16_observability.md:635` "Gateway-emitted:" inline list still does not include `workspace_plan_unknown_source_type`, `workspace_plan_path_collision`, or `workspace_plan_strip_components_skip`; no dedicated paragraph for these three events exists. |
| CNT-019 | Published `WorkspacePlan` JSON Schema lacks `minimum: 1` on `schemaVersion` (iter5 CNT-016 unfixed) | Low | **Open** — carried forward as **CNT-028** below. `spec/14_workspace-plan-schema.md:320` still describes `schemaVersion` as an "integer" field without a lower bound; no `minimum` constraint documented. |
| CNT-020 | `resolvedCommitSha` request/response schema asymmetry undocumented | Medium | **Fixed** — `spec/14_workspace-plan-schema.md:104` now carries the "Schema encoding of the request/response asymmetry" paragraph that documents `readOnly: true` on the `gitClone` variant, the dual-schema pattern (request-time check before JSON Schema validation), and the analogy to `last_used_at` on `GET /v1/credentials`. Wire-format contract is now internally consistent and auditable. See §14 line 104 walk-through below for one remaining small regression (CNT-029). |
| CNT-023 | §14.1 `oneOf`+open-fallthrough branching description is not valid JSON Schema 2020-12 semantics | Medium | **Fixed** — `spec/14_workspace-plan-schema.md:336` now uses `allOf` with per-variant `if`/`then` branching on `type.const`, including the concrete branch shape (`{"if": {"properties": {"type": {"const": "<variantName>"}}, "required": ["type"]}, "then": {<variant object schema with additionalProperties: false>}}`) and the top-level `{"type": "object", "required": ["type"], "properties": {"type": {"type": "string"}}}` shape. Unknown-`type` entries correctly fall through every `if` without triggering a `then`, matching the open-extensibility contract. Precise and implementable. |
| CNT-024 | `WORKSPACE_PLAN_INVALID` error code referenced in §14 but absent from §15.1 error catalog | Medium | **Fixed** — `spec/15_external-api-surface.md:977` carries the `WORKSPACE_PLAN_INVALID` row (PERMANENT/400) with the full sub-code enumeration (`invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`, `gateway_written_field`, `schema_validation_failed`) and cross-references §14. Docs-side mirror at `docs/reference/error-catalog.md:70` also present. Client-side error-code contract now has a canonical catalog entry. |
| CNT-025 | `docs/reference/workspace-plan.md` uses `VALIDATION_ERROR` where spec uses `WORKSPACE_PLAN_INVALID` (docs-sync drift) | Low | **Open** — carried forward as **CNT-030** below. `docs/reference/workspace-plan.md:79` and `docs/reference/workspace-plan.md:91` still say `400 VALIDATION_ERROR`; line 79 still omits `uploadFile` from the file-mode paragraph. The spec-side error-catalog mirror was added (CNT-024 fix) but the per-field mention in the distillation page was not rewritten. Two-iteration docs-sync drift. |

## Verification of iter6 fixes — end-to-end

**CNT-020 (`resolvedCommitSha` request/response schema asymmetry) end-to-end walk-through.**

The iter6 fix at `spec/14_workspace-plan-schema.md:104` adds a "Schema encoding of the request/response asymmetry" paragraph. Verified end-to-end against the iter6 CNT-020 acceptance criteria:

- **Schema declaration (request + response path):** "declared on the `gitClone` variant in the published JSON Schema with `\"readOnly\": true`" — correct JSON Schema 2020-12 wording; `readOnly` is informational in 2020-12 and this limitation is explicitly acknowledged in the paragraph ("`readOnly: true` is informational in JSON Schema 2020-12 — it does not by itself cause a validator to reject a value on the request path").
- **Response-path consumer:** "so response-side consumers (`GET /v1/sessions/{id}`) can validate its presence" — correct; response body includes `resolvedCommitSha` on every stored `gitClone` source, and any third-party client-side validator that downloads the published schema can validate the response.
- **Request-path gateway rejection:** "The gateway therefore performs a second request-time check that rejects `resolvedCommitSha` when present on any `sources[<n>]` entry in `CreateSessionRequest` with the field-specific error above" — correct; the pre-JSON-Schema check emits `400 WORKSPACE_PLAN_INVALID`, `details.reason = "gateway_written_field"` per §14 line 102, and the reason code is preserved over the generic `additionalProperties` violation.
- **Tooling obligation:** "tooling that round-trips the response into a new request MUST strip it first" — correct explicit obligation for SDK authors; this is the correct load-bearing contract for clients.
- **Pattern name and canonical referent:** "This dual-schema pattern … is the canonical encoding for gateway-written fields in Lenny and is identical to the encoding used for `last_used_at` on `GET /v1/credentials`" — claims a canonical pattern with `last_used_at` as the analog, but see new finding **CNT-031** below: `last_used_at` in §4.9 is not actually documented with `readOnly: true` or request-time rejection semantics, so the "identical to" claim is weak.
- **Anchor:** Reference `[§14.1](#141-extensibility-rules)` in the same paragraph at line 104 uses a **broken anchor slug** — the §14.1 heading is "WorkspacePlan Schema Versioning" (slug `#141-workspaceplan-schema-versioning`), not `#141-extensibility-rules`. See new finding **CNT-029** below. This is a regression introduced by the iter6 fix commit.

**CNT-023 (`oneOf`-to-`allOf`+`if`/`then` rewrite) end-to-end walk-through.**

The iter6 rewrite at `spec/14_workspace-plan-schema.md:336` is structurally correct JSON Schema 2020-12:

- **Construction:** "JSON Schema 2020-12 `allOf` + per-variant `if`/`then` branching on `type.const`" — correct construction. `allOf` requires every subschema to validate, and each `if`/`then` pair is a no-op when its `if` condition does not match (the JSON Schema 2020-12 semantics of `if`/`then`/`else`).
- **Concrete branch shape:** "`{\"if\": {\"properties\": {\"type\": {\"const\": \"<variantName>\"}}, \"required\": [\"type\"]}, \"then\": {<variant object schema with additionalProperties: false>}}`" — correct; the `const` discriminator plus `required: ["type"]` on the `if` ensures the condition matches only when the entry has the exact discriminator value.
- **Top-level shape:** "a top-level `{\"type\": \"object\", \"required\": [\"type\"], \"properties\": {\"type\": {\"type\": \"string\"}}}` ensures every entry has a string `type` without constraining its value" — correct; this guarantees the discriminator is always present as a string, enabling the `if` conditions to evaluate without error.
- **Unknown-`type` behaviour:** "An entry whose `type` matches no known variant passes validation (none of the `if` clauses fire, so no `then` is enforced) and is skipped by the consumer per the open-extensibility rule" — correct; matches the iter5 "skip + warning" contract for unknown types.
- **Rationale for not using `oneOf`:** "not `oneOf` — `oneOf` would require exactly-one-match, which is incompatible with the intent that an unknown-`type` entry matches no variant branch and is still accepted" — correct rationale; this is the key insight that the iter6 CNT-023 finding identified.

**CNT-024 (`WORKSPACE_PLAN_INVALID` catalog entry) end-to-end walk-through.**

The iter6 fix adds the `WORKSPACE_PLAN_INVALID` row to `spec/15_external-api-surface.md:977` and mirrors it to `docs/reference/error-catalog.md:70`:

- **Spec-side row (§15.1 line 977):** PERMANENT/400, descriptor covers both JSON Schema violations (`"gitClone.url` not `https://`") and application-layer checks (`mode` setuid/setgid, `resolvedCommitSha` client-supplied), `details.field` + `details.reason` fields documented, `details.fields` multi-violation report mentioned, distinction from `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` made. Sub-code enumeration (`invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`, `gateway_written_field`, `schema_validation_failed`) covers every currently-documented reason from §14.
- **Docs-side row (`docs/reference/error-catalog.md:70`):** Identical sub-code enumeration; positioned alphabetically between `VALIDATION_ERROR` (line 69) and `INVALID_STATE_TRANSITION` (line 71). Mirror-consistent with spec.
- **Residual gap:** The `docs/reference/workspace-plan.md:79` and `docs/reference/workspace-plan.md:91` still reference `VALIDATION_ERROR` rather than `WORKSPACE_PLAN_INVALID`, which is the CNT-025 iter6 docs-sync carry-forward (now **CNT-030** below). The catalog entries themselves are complete; the distillation page references are stale.

## New findings

### CNT-026. §14 `gitClone.auth` paragraph still lacks session-binding sentence (iter6 CNT-017, iter5 CNT-012, iter4 CNT-008, iter3 CNT-007 unfixed) [Low]

**Section:** 14 (line 95 `gitClone.auth` paragraph), 26.2 (line 119), 4.9 (`credential.leased` audit event catalog at line 1738).

Fifth consecutive iteration (iter3 → iter4 → iter5 → iter6 → iter7) that this Low-severity documentation drift has been flagged and remains unaddressed. §14 line 95 describes the `auth.mode: "credential-lease"` contract, the `vcs.<provider>.read|write` scope convention, host-to-pool resolution via `hostPatterns`, the `GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `GIT_CLONE_AUTH_HOST_AMBIGUOUS` rejection codes, and the HTTPS credential-helper flow, but still does not carry the normative sentence pairing `gitClone` credential leases to the originating `session_id` and the `credential.leased` audit event. §26.2 line 119 continues to carry the paired sentence ("The lease is bound to the originating session ID for audit traceability."), so §14 — the schema-of-record per CNT-002 (iter2) — remains out of sync with §26.2 and §4.9.

Per the iter5 severity-calibration feedback (`feedback_severity_calibration_iter5.md`), this finding is anchored at **Low** to match its iter3/iter4/iter5/iter6 rating. It is documentation-only with no functional impact — the gateway still emits `credential.leased` via the normal code path — but five-iteration persistence without fix is a strong convergence signal and warrants a simple one-sentence edit.

**Recommendation:** Append one sentence to `spec/14_workspace-plan-schema.md:95` after the credential-helper sentence:

> The lease issued for a `gitClone` source is bound to the originating `session_id` and recorded in the `credential.leased` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)) for traceability.

Verbatim the iter3/iter4/iter5/iter6 recommendation; closes the drift.

### CNT-027. §14 workspace-plan warning events still absent from §16.6 Operational Events Catalog (iter6 CNT-018, iter5 CNT-013, iter4 CNT-010 unfixed) [Low]

**Section:** 14 (lines 100, 334, 338 — three gateway-emitted `workspace_plan_*` warning events), 16.6 (`spec/16_observability.md:633` catalog header, line 635 "Gateway-emitted:" inline list).

§14 defines three gateway-emitted warning events associated with `WorkspacePlan` materialization (`workspace_plan_unknown_source_type`, `workspace_plan_path_collision`, `workspace_plan_strip_components_skip`), but none appears in the §16.6 "Gateway-emitted:" inline enumeration at line 635, and no dedicated paragraph for them exists between the experiment-events paragraph (lines 637–644) and the `lenny-ops`-emitted list (line 646). §16.6 line 633 declares itself "the canonical enumeration" of operational events, so the omission means consumers that filter the SSE stream, Redis stream, or in-memory buffer ([§25.5](25_agent-operability.md#255-operational-event-stream)) against the catalog will silently drop these three events as unknown.

This is the third consecutive iteration this finding has been carried forward. Severity anchored at **Low** per the iter4/iter5/iter6 rating for this finding (persistent documentation-only drift).

**Recommendation:** Add the following dedicated paragraph to `spec/16_observability.md` after line 644 (the end of the experiment-events paragraph), and update the line-635 inline list:

> **Workspace plan events (gateway-emitted, operational).** The gateway emits the following warning events during `WorkspacePlan` validation and materialization ([§14](14_workspace-plan-schema.md)). All are CloudEvents with `type: dev.lenny.<short_name>` and flow through the same Redis stream / in-memory buffer as the gateway-emitted events above.
>
> - `workspace_plan_unknown_source_type` (warning) — emitted when a consumer encounters an unknown `source.type` and skips the source entry per the open-string extensibility contract. Payload fields: `tenant_id`, `session_id`, `schemaVersion`, `unknownType`, `sourceIndex`.
> - `workspace_plan_path_collision` (warning) — emitted when two or more `sources` entries resolve to the same workspace path during materialization and the later entry wins under the last-writer-wins rule. Payload fields: `tenant_id`, `session_id`, `path`, `winningSourceIndex`, `losingSourceIndex`.
> - `workspace_plan_strip_components_skip` (warning) — emitted per skipped archive entry when `uploadArchive.stripComponents` exceeds the entry's segment count or the post-strip path is empty. Payload fields: `tenant_id`, `session_id`, `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`.

Also update the line-635 "Gateway-emitted:" inline list to append `workspace_plan_unknown_source_type`, `workspace_plan_path_collision`, `workspace_plan_strip_components_skip` so the one-liner enumeration remains exhaustive.

### CNT-028. Published `WorkspacePlan` JSON Schema still lacks `minimum: 1` on `schemaVersion` (iter6 CNT-019, iter5 CNT-016 unfixed) [Low]

**Section:** 14.1 (`spec/14_workspace-plan-schema.md:320` `schemaVersion` field paragraph, line 313 Published JSON Schema description).

Iter5 and iter6 flagged this as a Low polish item deferrable to post-convergence. §14.1 line 320 still describes `schemaVersion` as "a `schemaVersion` integer field" without a lower bound, and the Published JSON Schema paragraph at line 313 enumerates only the field set (`$schema`, `schemaVersion`, `sources[]`, `setupCommands[]`) without version-number constraints. A plan with `"schemaVersion": 0` or `"schemaVersion": -1` is not covered by any of §14.1's normative clauses — the "higher than I understand" rule assumes positive integers and the audit/analytics forward-read rule assumes well-formed records. Severity anchored at **Low** per iter5/iter6 rating.

**Recommendation:** In `spec/14_workspace-plan-schema.md:320` (or the Published JSON Schema description at line 313), append one sentence:

> The published schema constrains `schemaVersion` with `{ "type": "integer", "minimum": 1 }` — values less than 1 are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`, `details.field = "schemaVersion"`, `details.reason = "invalid_schema_version"`.

If this recommendation is adopted, also add `invalid_schema_version` to the sub-code enumeration in the `WORKSPACE_PLAN_INVALID` row at `spec/15_external-api-surface.md:977` and at `docs/reference/error-catalog.md:70` (both rows currently enumerate `invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`, `gateway_written_field`, `schema_validation_failed`, but not `invalid_schema_version`).

### CNT-029. Broken internal anchor `#141-extensibility-rules` introduced by iter6 CNT-020 fix [Low] — **Fixed**

**Status:** Fixed — Closed by DOC-032 fix — same anchor correction. Replaced `[§14.1](#141-extensibility-rules)` with `[§14.1](#141-workspaceplan-schema-versioning)` at `spec/14_workspace-plan-schema.md:104`; verified no remaining occurrences of `#141-extensibility-rules` in `spec/`, `docs/`, or `spec-reviews/`.

**Section:** 14 (`spec/14_workspace-plan-schema.md:104`), 14.1 (heading at line 306).

The iter6 fix for CNT-020 introduced a new paragraph at `spec/14_workspace-plan-schema.md:104` containing this cross-reference:

> ... declaring `resolvedCommitSha` inside that object means a strict validator would accept a client-supplied value. Because the per-variant object schema sets `additionalProperties: false` (see "Per-variant field strictness" in [§14.1](#141-extensibility-rules)), ...

The anchor `#141-extensibility-rules` does **not exist** in the spec. The §14.1 heading is `### 14.1 WorkspacePlan Schema Versioning` (line 306), whose autogenerated slug is `#141-workspaceplan-schema-versioning`. `#141-extensibility-rules` resolves to no heading anywhere in the codebase (verified via `grep` across `spec/`, `docs/`, and `spec-reviews/`). The correct anchor that carries the "Per-variant field strictness" paragraph is either the section anchor (`#141-workspaceplan-schema-versioning`) or the canonical paragraph-level pattern used elsewhere in the spec (plain-text deep link via the paragraph's bold lead-in `**Per-variant field strictness.**`).

This is a regression introduced by the iter6 fix commit (`8604ce9`) — the same commit that fixed the three Medium findings CNT-020/023/024 also introduced this broken anchor. Severity **Low** because the paragraph text is still intuitively correct (the reader can find "Per-variant field strictness" in §14.1 by scrolling), and no tooling-critical path depends on this anchor resolving. However, this is exactly the same failure class as DOC-024/DOC-025 from iter6 (12 broken cross-file anchors introduced by the iter5 compliance-fix commit) and warrants the same anchor-integrity CI gate that iter6 DOC-024/025 flagged.

**Recommendation:** Edit `spec/14_workspace-plan-schema.md:104` to replace `[§14.1](#141-extensibility-rules)` with `[§14.1](#141-workspaceplan-schema-versioning)`:

> Because the per-variant object schema sets `additionalProperties: false` (see "Per-variant field strictness" in [§14.1](#141-workspaceplan-schema-versioning)), declaring `resolvedCommitSha` inside that object means a strict validator would accept a client-supplied value.

Verifiable by running `grep -R "#141-extensibility-rules" spec/ docs/ spec-reviews/` after the fix — the only occurrence is line 104, so a single edit closes it.

### CNT-030. `docs/reference/workspace-plan.md` still uses `VALIDATION_ERROR` where spec uses `WORKSPACE_PLAN_INVALID` (iter6 CNT-025 unfixed, docs-sync drift) [Low]

**Section:** 14 (spec-side normative contract), `docs/reference/workspace-plan.md` (docs-side lines 79, 91).

Per the `feedback_docs_sync_after_spec_changes.md` feedback, docs must reconcile with spec changes after each review-fix iteration before declaring convergence. The iter5 CNT-014 fix updated §14 line 101 to say `mode` violations return `400 WORKSPACE_PLAN_INVALID`; the iter6 CNT-024 fix added the `WORKSPACE_PLAN_INVALID` row to both `spec/15_external-api-surface.md:977` and `docs/reference/error-catalog.md:70`. However, `docs/reference/workspace-plan.md` — the docs-side distillation page — was not updated during either the iter5 or iter6 docs-sync passes and still says:

- **Line 79:** "Invalid values are rejected at session creation with `400 VALIDATION_ERROR`." — should be `WORKSPACE_PLAN_INVALID` to match spec §14 line 101.
- **Line 79 (additional):** "**File mode (`inlineFile`, `mkdir`).**" — this omits `uploadFile`. Spec §14 line 101 covers all three variants (`inlineFile`, `uploadFile`, `mkdir`).
- **Line 91:** "Invalid plans are rejected with `400 VALIDATION_ERROR`." — should be `WORKSPACE_PLAN_INVALID` to match spec §14.1 line 317.

This is the second consecutive iteration (iter6 CNT-025 flagged it, iter7 confirms it is unfixed). `VALIDATION_ERROR` is a separate, general-purpose code documented in `docs/reference/error-catalog.md:69` — it is not wrong in isolation but is not the spec's chosen code for inner-plan failures, and the two codes now coexist in the docs' error catalog (lines 69 and 70 respectively) making the stale reference on the workspace-plan page directly contradictable by the adjacent catalog entry.

Severity **Low** — the divergence is limited to one docs page, the docs page explicitly says "This page is a reference distillation; for every field's full semantics … consult the spec", and the client-observable HTTP status (400) is the same. But the code mismatch is a real contract break for tooling that grep-matches error codes and for SDK authors who read the distillation page as the primary source.

**Recommendation:** Apply two small edits to `docs/reference/workspace-plan.md`:

1. **Line 79:** Rewrite the file-mode paragraph to cover all three variants and use the correct error code:

   > **File mode (`inlineFile`, `uploadFile`, `mkdir`).** The optional `mode` field on these sources is a POSIX-style octal string matching the regex `^0[0-7]{3,4}$` (e.g., `"0644"`, `"0755"`). The setuid (`04000`) and setgid (`02000`) bits are prohibited for all source types; the sticky bit (`01000`) is accepted only on `mkdir`. Invalid values are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`.

2. **Line 91:** Rewrite the validation bullet to use the correct error code:

   > - Plans are validated against the registered JSON Schema (`schemaVersion`) at session creation. Invalid plans are rejected with `400 WORKSPACE_PLAN_INVALID`.

### CNT-031. "`last_used_at` is the canonical analog" claim in `resolvedCommitSha` paragraph is weak — `last_used_at` is not documented with `readOnly: true` request/response asymmetry [Low]

**Section:** 14 (`spec/14_workspace-plan-schema.md:104`), 4.9 (`spec/04_system-components.md:1346` — `GET /v1/credentials` response schema).

The iter6 CNT-020 fix at `spec/14_workspace-plan-schema.md:104` concludes with:

> This dual-schema pattern (schema declares the field with `readOnly: true` for response validation; request-time check enforces non-set) is the canonical encoding for gateway-written fields in Lenny and is identical to the encoding used for `last_used_at` on `GET /v1/credentials`.

The claim is that `resolvedCommitSha` and `last_used_at` share an "identical" encoding, with `last_used_at` serving as the canonical example to anchor the pattern. However, the actual `last_used_at` specification at §4.9:

- **`spec/04_system-components.md:1346`:** Describes `GET /v1/credentials` as returning `{credential_ref, provider, label, created_at, last_used_at}` — a plain response-side field listing with no mention of `readOnly: true`, no published JSON Schema URL, and no documented request-time rejection path for client-supplied values.
- **`spec/04_system-components.md:1362`:** States "`last_used_at` is updated on each successful resolution" — this is a server-side-populated field, but the request-side contract (`POST /v1/credentials` — can a client pre-supply `last_used_at`? what error code is returned if they do?) is not documented anywhere in §4.9.

So the "identical to the encoding used for `last_used_at`" claim is not load-bearing — `last_used_at` is an undocumented analog. This weakens the iter6 CNT-020 fix's attempt to establish `resolvedCommitSha`'s pattern as canonical: if readers follow the analogy to §4.9, they find no encoding to copy. Two resolutions are possible:

1. **Drop the analogy.** Remove the "identical to the encoding used for `last_used_at` on `GET /v1/credentials`" phrase. The paragraph stands on its own without needing an external canonical referent.
2. **Establish the analogy explicitly.** Add a similar "Schema encoding of the request/response asymmetry" note to §4.9 for `last_used_at` (and any other `GET /v1/credentials` gateway-written field — `created_at` is another candidate). This creates a cross-section canonical pattern that future `readOnly` fields (resolved URLs, resolved credential provider IDs, materialization-time hashes, etc.) can reference.

Severity **Low** because the iter6 CNT-020 fix is structurally correct on its own — the analogy is decorative, not load-bearing — but the weak analogy dilutes the "canonical pattern" claim and reduces the fix's pedagogical value for future schema authors.

**Recommendation:** Adopt resolution (1) — drop the analogy sentence from `spec/14_workspace-plan-schema.md:104`. Replace:

> This dual-schema pattern (schema declares the field with `readOnly: true` for response validation; request-time check enforces non-set) is the canonical encoding for gateway-written fields in Lenny and is identical to the encoding used for `last_used_at` on `GET /v1/credentials`.

with:

> This dual-schema pattern (schema declares the field with `readOnly: true` for response validation; request-time check enforces non-set) is the canonical encoding for gateway-written fields in Lenny; any future read-only plan field should follow the same pattern.

Alternatively, if resolution (2) is preferred, add a one-paragraph note to `spec/04_system-components.md` (near line 1346) documenting `last_used_at` and `created_at` as `readOnly: true` gateway-written fields with the same request-time rejection behaviour.

## Convergence assessment

- **Three iter6 items remain open** (CNT-017 Low, CNT-018 Low, CNT-019 Low, now CNT-026/027/028), all documentation-only fixes within §14, §14.1, §16.6, and `docs/`. CNT-026 (session-binding sentence) is now on its **fifth** iteration without fix — this is the longest-lived unresolved CNT finding and, while Low severity, is a substantive convergence signal. CNT-027 (workspace-plan events catalog) and CNT-028 (`minimum: 1` constraint) are both on their third iteration.
- **Three iter6 items were fixed** (CNT-020 Medium, CNT-023 Medium, CNT-024 Medium), verified end-to-end in the iter6 fix commit (8604ce9). CNT-023 and CNT-024 are clean, precise fixes. CNT-020 is structurally correct but introduces one new regression (CNT-029 broken anchor) and includes one weak analogy (CNT-031 `last_used_at` reference), both Low.
- **One iter6 Low item remains open** (CNT-025 docs-sync, now CNT-030) — the workspace-plan distillation page was not updated in either the iter5 or iter6 docs-sync pass and still carries the stale `VALIDATION_ERROR` reference plus the incomplete three-variant list (`inlineFile`, `mkdir` without `uploadFile`).
- **Six iter7 findings** (all Low — three carry-forwards CNT-026/027/028, one regression CNT-029, one docs-sync CNT-030, one polish CNT-031):
  - CNT-029 is an iter6-fix-adjacent regression — the CNT-020 fix introduced a broken anchor while closing its original gap. Same failure class as DOC-024/025 from iter6. Trivially fixable.
  - CNT-031 is an iter6-fix-adjacent polish — the CNT-020 fix's canonical-pattern claim leans on an undocumented analog. Drop or substantiate.
  - CNT-026 / CNT-027 / CNT-028 / CNT-030 are all multi-iteration carry-forwards, all bounded to single-file documentation edits.
- **No Critical, High, or Medium findings this iteration.** All three iter6 Mediums (CNT-020, CNT-023, CNT-024) are fixed; no new Medium regressions surfaced. The three Medium fixes from iter6 collectively make the §14 / §14.1 / §15.1 contract internally consistent — the error catalog, JSON Schema construction description, and read-only-field encoding all align.
- **Recommendation:** Perspective 18 is **close to converged** at iter7 for Critical/High/Medium. The residual six Low findings are all single-sentence or single-edit fixes within §14, §14.1, §16.6, and `docs/reference/{workspace-plan,error-catalog}.md`. If iter8 fixes CNT-029 (broken anchor regression — a single char-level edit), and ideally also closes the four-plus-iteration carry-forwards (CNT-026, CNT-027, CNT-028, CNT-030), Perspective 18 converges. CNT-031 can be resolved in either direction at the editor's preference. No cross-perspective dependencies surfaced.
