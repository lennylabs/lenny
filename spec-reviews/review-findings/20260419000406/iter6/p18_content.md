# Perspective 18 — Content Model, Data Formats & Schema Design (Iter6)

Scope: `WorkspacePlan` (§14), `WorkspacePlan JSON Schema` (§14.1), `OutputPart` / `MessageEnvelope` (§15.4.1), `RuntimeDefinition` / `runtimeOptionsSchema` (§5.1). Iter6 revisits iter5 CNT-012…CNT-016 against the iter5 fix commit (c941492), and audits the §14 `mode` and `resolvedCommitSha` additions end-to-end (JSON Schema wording, error-code catalog consistency, read-only contract, public-repo path, ref-is-SHA shortcut).

## Iter5 carry-over verification

| Iter5 ID | Title (summary) | Severity | Iter6 status |
|---|---|---|---|
| CNT-012 | §14 `gitClone.auth` paragraph lacks session-binding sentence (iter4 CNT-008 repeat) | Low | **Open** — carried forward as **CNT-017** below. §14 line 95 still has no `credential.leased` / session-binding sentence. §26.2 line 119 still carries the paired sentence. |
| CNT-013 | §14 workspace-plan warning events absent from §16.6 Operational Events Catalog (iter4 CNT-010 repeat + `workspace_plan_strip_components_skip`) | Low | **Open** — carried forward as **CNT-018** below. §16.6 line 631 "Gateway-emitted:" inline list does not include any of the three `workspace_plan_*` events; no dedicated paragraph was added after §16.6 experiment-events (line 633). |
| CNT-014 | `inlineFile.mode` / `uploadFile.mode` / `mkdir.mode` string format has no regex, range, or setuid/setgid/sticky-bit constraint | Medium | **Fixed** — §14 line 101 adds the `^0[0-7]{3,4}$` pattern, setuid/setgid rejection (both variants), sticky-on-file rejection on `inlineFile`/`uploadFile`, reason codes (`invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`), `strconv.ParseUint(mode, 8, 32)` implementation guidance, and defaults. The `mode` column headers in the `sources[]` table at lines 87–90 cross-reference the note. |
| CNT-015 | `gitClone.ref` reproducibility semantics undefined — branch-name refs drift between session creation and resume/retry | Medium | **Fixed** — §14 line 102 adds the per-session-immutability paragraph: `git ls-remote` at session creation, `resolvedCommitSha` persisted beside the plan, read-only gateway-written field with `details.reason = "gateway_written_field"` rejection on client-supplied values, 40-character hex shortcut, `resolvedCommitSha` surfaced via `GET /v1/sessions/{id}` (§15.1 line 597), per-session-not-per-plan guarantee scoped to delegation children / reuse. `GIT_CLONE_REF_UNRESOLVABLE` (§15.1 line 1058) covers the `ls-remote`-failure path. §14.1 line 324 gateway-reconciliation paragraph explicitly states the replay path clones `resolvedCommitSha`. |
| CNT-016 | Published `WorkspacePlan` JSON Schema lacks `minimum: 1` on `schemaVersion` | Low | **Open** — carried forward as **CNT-019** below. §14.1 line 318 `schemaVersion` paragraph still says only "integer"; no `minimum` constraint documented. |

## Verification of iter5 fixes — end-to-end

**CNT-014 (`mode` format) end-to-end walk-through.**

- Pattern: `^0[0-7]{3,4}$` is regex-precise and testable. Rejects `"644"` (no leading zero), `"0o644"` (starts with `0o`), `"0x1A4"` (hex), `"rw-r--r--"` (symbolic), `"012345"` (5 digits), `"08"` (digit 8 outside [0-7]). Accepts `"0644"`, `"0755"`, `"04755"`, `"06755"` (then rejected at application layer for setuid/setgid).
- Setuid/setgid rejection is enforced "on any variant" — uniform across `inlineFile`, `uploadFile`, `mkdir`. Reason code `setuid_setgid_prohibited`. Text is precise.
- Sticky-bit rules: permitted on `mkdir`, rejected on `inlineFile`/`uploadFile`. Reason code `sticky_on_file_prohibited`. Text is precise.
- Defaults: `0644` for `inlineFile`/`uploadFile`, `0755` for `mkdir` — consistent with pre-CNT-014 example and runtime norms.
- Error code: `400 WORKSPACE_PLAN_INVALID`, `details.field = "sources[<n>].mode"`, `details.reason ∈ {"invalid_mode_format", "setuid_setgid_prohibited", "sticky_on_file_prohibited"}`. Consistent with §14.1 line 315 "`WORKSPACE_PLAN_INVALID` is reserved for inner-plan schema failures" — note: `setuid_setgid_prohibited` and `sticky_on_file_prohibited` are application-level checks (not expressible in JSON Schema), so strictly speaking they are not "inner-plan schema failures" in the pure JSON Schema sense. This is a minor fuzz: see new finding **CNT-022** below.

**CNT-015 (`gitClone.ref` resolution) end-to-end walk-through.**

- JSON Schema pattern wording: `resolvedCommitSha` is described as "read-only, gateway-written field" with explicit rejection (`details.reason = "gateway_written_field"`). However, the `gitClone` variant's JSON Schema (per §14.1 line 334 "`additionalProperties: false`") does not list `resolvedCommitSha` in its properties. Two possible interpretations:
  1. Published schema is request-only; `resolvedCommitSha` is rejected at request time by `additionalProperties: false` with a generic "additionalProperty not allowed" error, and the spec's "`gateway_written_field`" reason code is a special-cased pre-validation check the gateway does before JSON Schema validation (so it can return a specific reason).
  2. Published schema is shared between request and response; `resolvedCommitSha` is declared with JSON Schema Draft 2020-12 `readOnly: true`, which would mean request-path validators can reject it while response-path validators accept it.
  The spec does not state which interpretation applies. See new finding **CNT-020** below.
- Error code semantics consistent with §15 catalog: `GIT_CLONE_REF_UNRESOLVABLE` is catalogued at §15.1 line 1058 with `PERMANENT`/422 category and the correct `details.url` / `details.ref` / `details.sourceIndex` / `details.reason` fields. `details.reason` enum values (`network_error`, `auth_failed`, `ref_not_found`) match between §14 line 102 and §15.1 line 1058. Consistent.
- `resolvedCommitSha` read-only contract clear: §14 line 102 says "clients MUST NOT set it in the `CreateSessionRequest`" with a dedicated reason code. §15.1 line 597 documents response inclusion. The request/response asymmetry is implied but not explicit — see **CNT-020** below.
- Public-repo (no auth) path handled: §14 line 102 "using the same credential-lease as the clone itself (or unauthenticated for public repos per the `gitClone.auth` paragraph above)". `gitClone.auth` paragraph at line 95 states "Omit `auth` for public repositories — no URL-to-pool binding is required when `auth` is absent." Consistent.
- Ref-is-already-a-SHA shortcut specified: §14 line 102 "When `ref` is already a 40-character lowercase hexadecimal string matching `^[0-9a-f]{40}$`, the gateway treats it as a commit SHA and skips the `ls-remote` step (`resolvedCommitSha` equals `ref`)." Precise; regex anchored at both ends; lowercase-only is a correct canonicalization choice for Git SHA-1 object IDs. SHA-256 object IDs (Git's experimental transition format, 64 hex chars) are out of scope for v1 and are not referenced in the spec — reasonable given Git SHA-256 adoption is minimal.
- `git ls-remote` semantics: one small ambiguity — the spec does not say **what** `ls-remote` is invoked against (the URL as given) or how ref disambiguation works when a ref exists as both a branch and a tag (Git's precedence is: exact-match refspec > `refs/heads/<ref>` > `refs/tags/<ref>` > `refs/remotes/<ref>`). See new finding **CNT-021** below.

**§14 JSON Schema structural claims (CNT-011 iter4 fix verification).**

- The §14.1 line 334 claim that the schema uses "JSON Schema 2020-12 `oneOf` over the known variants with `if`/`then` branching on `type`, and an open fallthrough branch for unknown-`type` entries" is structurally questionable. `oneOf` requires **exactly one** subschema to match; if the fallthrough branch accepts arbitrary-`type` entries, then for a known-`type` entry (e.g., `{"type":"inlineFile",...}`) both the typed branch and the fallthrough branch would match, violating `oneOf` (exactly-one) semantics. To make this work cleanly in JSON Schema 2020-12, either (a) the fallthrough branch needs an explicit `not: { properties: { type: { enum: [<known>] } } }` so it only matches unknown types, or (b) the whole construct should use `allOf` + `if`/`then` chains instead of `oneOf`, or (c) the fallthrough branch should be the absence of a branch (i.e., unknown-`type` entries are "schema-valid by default" because the `if` conditions don't fire, and the consumer-skip behaviour is enforced at application layer rather than via a fallthrough branch in the schema). See new finding **CNT-023** below.

## New findings

### CNT-017. §14 `gitClone.auth` paragraph still lacks session-binding sentence (iter5 CNT-012, iter4 CNT-008, iter3 CNT-007 unfixed) [Low]

**Section:** 14 (line 95 `gitClone.auth` paragraph), 26.2 (line 119), 4.9 (`credential.leased` audit event).

This is the fourth consecutive iteration (iter3 → iter4 → iter5 → iter6) that this Low-severity documentation drift has been flagged and the fourth in which it has not been addressed. §14 line 95 describes lease-scope resolution, host-pattern matching, and the HTTPS credential-helper flow in detail but still does not carry the normative sentence pairing `gitClone` leases to the originating `session_id` and the `credential.leased` audit event. §26.2 line 119 continues to carry the paired sentence ("The lease is bound to the originating session ID for audit traceability."), but §14 — the schema-of-record per CNT-002 (iter2) — remains out of sync with §26.2.

Per the iter5 severity-calibration feedback (`feedback_severity_calibration_iter5.md`), this finding is anchored at **Low** to match its iter3/iter4/iter5 rating. It is a documentation-only drift with no functional impact (the gateway emits `credential.leased` regardless of where the sentence lives); however, four-iteration persistence without fix is itself a convergence signal worth tracking.

**Recommendation:** Append one sentence to §14 line 95 after the credential-helper sentence: "The lease issued for a `gitClone` source is bound to the originating `session_id` and recorded in the `credential.leased` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)) for traceability." This is verbatim the iter3/iter4/iter5 recommendation and closes the drift.

### CNT-018. §14 workspace-plan warning events still absent from §16.6 Operational Events Catalog (iter5 CNT-013, iter4 CNT-010 unfixed) [Low]

**Section:** 14 (lines 100 `workspace_plan_strip_components_skip`, 332 `workspace_plan_unknown_source_type`, 336 `workspace_plan_path_collision`), 16.6 (Operational Events Catalog at lines 627, 631 `Gateway-emitted` inline enumeration, 633 experiment-events paragraph).

§14 defines three gateway-emitted warning events associated with `WorkspacePlan` materialization (`workspace_plan_unknown_source_type`, `workspace_plan_path_collision`, `workspace_plan_strip_components_skip`) but none appears in the §16.6 "Gateway-emitted:" inline list at line 631, and no dedicated paragraph for them exists after the §16.6 experiment-events paragraph (lines 633–640). The `workspace_plan_strip_components_skip` event was introduced by the iter4 CNT-009 fix and also remains unregistered.

§16.6 line 629 declares itself "the canonical enumeration" of operational events and other §14-originated events (e.g., `session_completed` / `session_failed`) are listed at line 631, so the omission is real — consumers that filter the SSE stream, Redis stream, or in-memory buffer ([§25.5](25_agent-operability.md#255-operational-event-stream)) against the catalog will silently drop these three events as unknown. Severity anchored at **Low** per the iter4/iter5 rating for this finding (matches severity-calibration rubric for persistent documentation drift).

**Recommendation:** Add the following dedicated paragraph to §16.6 after the experiment-events paragraph (line 640):

> **Workspace plan events (gateway-emitted, operational).** The gateway emits the following warning events during `WorkspacePlan` validation and materialization ([§14](14_workspace-plan-schema.md)). All are CloudEvents with `type: dev.lenny.<short_name>` and flow through the same Redis stream / in-memory buffer as the gateway-emitted events above.
>
> - `workspace_plan_unknown_source_type` (warning) — emitted when a consumer encounters an unknown `source.type` and skips the source entry per the open-string extensibility contract. Payload fields: `tenant_id`, `session_id`, `schemaVersion`, `unknownType`, `sourceIndex`.
> - `workspace_plan_path_collision` (warning) — emitted when two or more `sources` entries resolve to the same workspace path during materialization and the later entry wins under the last-writer-wins rule. Payload fields: `tenant_id`, `session_id`, `path`, `winningSourceIndex`, `losingSourceIndex`.
> - `workspace_plan_strip_components_skip` (warning) — emitted per skipped archive entry when `uploadArchive.stripComponents` exceeds the entry's segment count or the post-strip path is empty. Payload fields: `tenant_id`, `session_id`, `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`.

Also update the line-631 inline "Gateway-emitted:" list to add `workspace_plan_unknown_source_type`, `workspace_plan_path_collision`, `workspace_plan_strip_components_skip` so the one-liner enumeration remains exhaustive.

### CNT-019. Published `WorkspacePlan` JSON Schema still lacks `minimum: 1` on `schemaVersion` (iter5 CNT-016 unfixed) [Low]

**Section:** 14.1 (line 318 `schemaVersion` paragraph; line 311 Published JSON Schema paragraph).

Iter5 flagged this as a Low polish item deferrable to post-convergence. §14.1 line 318 still describes `schemaVersion` as an "integer" without a lower bound, and the Published JSON Schema paragraph at line 311 enumerates only the field set (`$schema`, `schemaVersion`, `sources[]`, `setupCommands[]`) without version-number constraints. A plan with `"schemaVersion": 0` or `"schemaVersion": -1` is not covered by any of §14.1's normative clauses — the "higher than I understand" rule assumes positive integers and the audit/analytics forward-read rule assumes well-formed records. Severity anchored at **Low** per iter5 rating.

**Recommendation:** In §14.1 line 318 (or the Published JSON Schema description at line 311), add one sentence: "The published schema constrains `schemaVersion` with `{ \"type\": \"integer\", \"minimum\": 1 }` — values less than 1 are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`, `details.field = \"schemaVersion\"`, `details.reason = \"invalid_schema_version\"`."

### CNT-020. `resolvedCommitSha` request/response schema asymmetry undocumented [Medium]

**Section:** 14 (line 102 `gitClone.ref` resolution note), 14.1 (lines 311 Published JSON Schema, 334 Per-variant field strictness / `additionalProperties: false`), 15.1 (line 597 `GET /v1/sessions/{id}` response).

The iter5 CNT-015 fix declares `resolvedCommitSha` a "read-only, gateway-written field" with two distinct behaviours:

1. **Request path (`POST /v1/sessions`):** client-supplied `resolvedCommitSha` is rejected with `400 WORKSPACE_PLAN_INVALID`, `details.reason = "gateway_written_field"`.
2. **Response path (`GET /v1/sessions/{id}`):** the stored `workspacePlan` is returned with `resolvedCommitSha` populated for each `gitClone` source (§15.1 line 597).

But the published JSON Schema at `https://schemas.lenny.dev/workspaceplan/v1.json` (§14.1 line 311) is a single document, and §14.1 line 334 says the `gitClone` variant carries `additionalProperties: false`. Two problems follow:

- **Request-path ambiguity.** Because `additionalProperties: false` already rejects unknown fields with a generic "additionalProperty not allowed" JSON Schema error, the gateway's named `"gateway_written_field"` reason code must come from a special-cased pre-check that runs *before* JSON Schema validation, or from a post-validation error-detail mapping. The spec does not specify which, so third-party validators replicating the published schema for client-side validation will emit a different (generic) reason code than the gateway, which degrades the error-message contract.
- **Response-path inconsistency.** If the response body is validated against the same `additionalProperties: false` schema used for the request, the response containing `resolvedCommitSha` would fail validation. Either the schema must declare `resolvedCommitSha` as a known optional property on the `gitClone` variant (with JSON Schema Draft 2020-12 `readOnly: true` to convey the request-path restriction), or the spec must explicitly state that the published schema is request-only and responses are not validated against it.

This is Medium severity because: (a) the schema is the wire-format contract for third-party clients and mismatched validation behaviour on either request or response path is a real DX problem; (b) the fix is a small documentation clarification plus one JSON Schema keyword (`readOnly: true`), no behaviour change; (c) `resolvedCommitSha` was just introduced by the iter5 fix and establishes the precedent for any future "gateway-written read-only plan field" — a class including many candidate future fields (resolved URLs, resolved credential provider IDs, materialization-time hashes, etc.).

**Recommendation:** Amend §14.1 line 334 (Per-variant field strictness) to specify:

> The `gitClone` variant's schema declares `resolvedCommitSha` as a known optional property with `"readOnly": true`. The gateway rejects client-supplied `resolvedCommitSha` on the request path with `400 WORKSPACE_PLAN_INVALID`, `details.reason = "gateway_written_field"` — this check runs **before** JSON Schema validation so the named reason code is emitted in preference to the generic `additionalProperties` violation. The same schema validates the response body; `readOnly: true` is informational on the response path. Third-party client-side validators that do not implement the pre-check will emit the generic JSON Schema error ("additionalProperty: resolvedCommitSha") when they encounter a client-supplied value.

Alternatively, if the spec prefers to keep the published schema request-only: amend §14.1 line 311 to say "The published schema describes the shape of the `workspacePlan` sub-object on the **request** path; response bodies include additional gateway-written read-only fields (currently `sources[<n>].resolvedCommitSha` for `gitClone`) documented in [§15.1](15_external-api-surface.md#151-rest-api)." Either resolution is acceptable; the current state (neither stated) is the gap.

### CNT-021. `gitClone.ref` resolution does not specify `ls-remote` ref disambiguation rules [Low]

**Section:** 14 (line 102 `gitClone.ref` resolution note).

The spec says the gateway resolves `ref` via `git ls-remote` but does not specify how ambiguity is resolved when a single `ref` string matches multiple Git ref namespaces. `git ls-remote <url> <ref>` returns matches across `refs/heads/`, `refs/tags/`, `refs/remotes/`, and `refs/pull/` namespaces; if a repository has both `refs/heads/main` and `refs/tags/main` (uncommon but legal), a bare `ref: "main"` is ambiguous. Native `git` behaviour in `git clone --branch` prefers the branch namespace, but the spec does not state this.

Additionally, the spec does not specify behaviour for:
- Multi-match cases (e.g., `refs/pull/123/head` vs `refs/heads/pr-123`).
- Annotated-tag resolution (`ref: "v1.0"` where `v1.0` is an annotated tag — does `resolvedCommitSha` equal the annotated-tag object's SHA or the SHA of the commit the tag points to, reached via `ls-remote`'s `^{}` peel syntax?).
- Partial-SHA `ref` values (e.g., `ref: "abc123"` — does the gateway reject as non-40-char-hex and fall through to `ls-remote`, or expand it via another mechanism?).

Severity **Low** because these are edge cases with well-defined Git conventions that most clients follow implicitly; the spec gap is for auditors and implementers wanting deterministic behaviour. No known adversarial use.

**Recommendation:** Append a short sentence to §14 line 102 after the 40-hex-char shortcut: "When `ref` matches multiple Git ref namespaces, the gateway follows the `git ls-remote` native precedence — `refs/heads/<ref>` (branches) first, then `refs/tags/<ref>` (tags, with annotated tags peeled via the `^{}` syntax so `resolvedCommitSha` always equals a commit SHA not a tag-object SHA), then `refs/remotes/<ref>`, then any other matching namespace. Partial-SHA `ref` values that do not match the `^[0-9a-f]{40}$` shortcut are resolved via `ls-remote` like any other ref and are rejected with `422 GIT_CLONE_REF_UNRESOLVABLE` if no full ref matches."

### CNT-022. `mode` application-layer rejections not cleanly separable from "inner-plan schema failures" contract [Low]

**Section:** 14 (line 101 `mode` field note), 14.1 (lines 315–316 Gateway validation; "`WORKSPACE_PLAN_INVALID` is reserved for inner-plan schema failures only").

§14.1 line 316 states: "`WORKSPACE_PLAN_INVALID` is **reserved for inner-plan schema failures only** and is not emitted for outer-envelope violations." But the iter5 CNT-014 fix introduces `WORKSPACE_PLAN_INVALID` with `details.reason = "setuid_setgid_prohibited"` and `details.reason = "sticky_on_file_prohibited"` — these are **application-layer** rules (bit-mask inspection on the parsed octal value), not expressible as pure JSON Schema `pattern` / `enum` / `oneOf` constraints. Strictly speaking, they are not "inner-plan schema failures" in the JSON Schema sense.

Two readings are possible: (a) "inner-plan schema failures" is a synonym for "failures detected during inner-plan validation", which includes both pure JSON Schema checks and post-schema application-layer checks on the inner plan (broad reading); or (b) "inner-plan schema failures" strictly means JSON Schema validation failures (narrow reading) — in which case `setuid_setgid_prohibited` should use a different error code. The spec is consistent with reading (a) based on CNT-014's error-code reuse, but the text at line 316 strongly implies reading (b).

Severity **Low** — the client-observable behaviour is identical (same HTTP 400, same error code); this is a specification-language precision issue.

**Recommendation:** In §14.1 line 316, add a parenthetical: "`WORKSPACE_PLAN_INVALID` is **reserved for inner-plan validation failures only** (both JSON Schema validation and post-schema application-layer checks on plan fields, such as `mode` setuid/setgid/sticky-bit constraints) and is not emitted for outer-envelope violations."

### CNT-023. §14.1 `oneOf` + open-fallthrough branching description is not valid JSON Schema 2020-12 semantics [Medium]

**Section:** 14.1 (line 334 Per-variant field strictness; last sentence describing the `oneOf` / `if`-`then` / fallthrough construct).

§14.1 line 334 ends: "the `sources[]` item schema uses a JSON Schema 2020-12 `oneOf` over the known variants with `if`/`then` branching on `type`, and an open fallthrough branch for unknown-`type` entries (which the consumer then skips per 'Unknown `source.type` handling' above)."

This description is structurally problematic in JSON Schema 2020-12:

- `oneOf` requires **exactly one** subschema in the array to validate — if more than one validates, the whole construct fails.
- An "open fallthrough branch for unknown-`type` entries" presumably accepts any object with a `type` field (so unknown types validate). But such a branch will also validate known-`type` entries (because they also have a `type` field) unless it explicitly excludes them via `not: { properties: { type: { enum: ["inlineFile", "uploadFile", "uploadArchive", "mkdir", "gitClone"] } } }`.
- Without the explicit negation, a known-`type` entry matches both the typed branch AND the fallthrough branch → `oneOf` fails → **all known entries would be rejected**.

The same intent is achievable in JSON Schema 2020-12 with `allOf` + a chain of `if`/`then` conditions (one per known variant) that apply per-variant `additionalProperties: false` if the discriminator matches. Unknown-`type` entries fall through all `if` conditions and produce no constraints, which aligns with the "skip entry, emit warning" consumer behaviour described elsewhere in §14.1. The `oneOf` framing is wrong.

This is Medium severity because: (a) §14.1 is the spec-of-record for the published JSON Schema (line 311) — downstream implementations will follow this description and produce a schema that rejects all valid plans; (b) the fix is a small description change (replace `oneOf` with `allOf` + `if`/`then`) with no wire-format impact; (c) JSON Schema authoring is a well-known trap area and getting the spec language right now prevents implementation drift.

**Recommendation:** Amend §14.1 line 334 to replace the final sentence:

> Clients extending the schema with vendor-specific fields MUST register a new `type` value (exercising the open-string discriminator) rather than attaching extra fields to a built-in type. Concretely, the `sources[]` item schema uses a JSON Schema 2020-12 `allOf` containing one `if`/`then` pair per known variant: each pair's `if` matches on the `type` discriminator (e.g., `{ "properties": { "type": { "const": "inlineFile" } } }`) and the matching `then` applies the variant-specific property set, `required` list, and `additionalProperties: false` rule. Unknown-`type` entries fall through every `if` condition without matching, so no per-variant constraint applies, and the consumer's "skip entry + emit `workspace_plan_unknown_source_type` warning" behaviour (see "Unknown `source.type` handling" above) runs at the application layer after schema validation succeeds. This allOf + if/then construction is the idiomatic JSON Schema 2020-12 pattern for discriminated-union with open-string discriminator; a `oneOf` over the known variants plus an open fallthrough would fail for known-`type` entries that match both the typed branch and the fallthrough.

### CNT-024. `WORKSPACE_PLAN_INVALID` error code referenced in §14 but absent from §15.1 error catalog [Medium]

**Section:** 14 (lines 93, 101, 102, 334), 14.1 (lines 315, 316), 15.1 (lines 1019–1078 error catalog).

`WORKSPACE_PLAN_INVALID` is referenced six times in §14/§14.1 as the authoritative error code for inner-plan validation failures (including iter4 CNT-007's HTTPS-only restriction, CNT-011's `additionalProperties: false` rejections, iter5 CNT-014's `mode` checks, and iter5 CNT-015's `resolvedCommitSha` client-supplied rejection). Yet §15.1's error catalog lists only the related `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (line 1019) and does not contain an entry for `WORKSPACE_PLAN_INVALID`. The docs-side error catalog at `docs/reference/error-catalog.md` likewise lists `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (line 90) but not `WORKSPACE_PLAN_INVALID`.

§15.1 line 1055 carries `ENV_VAR_BLOCKLISTED` (for outer-envelope `env` violations) and §15.1 line 1058 carries `GIT_CLONE_REF_UNRESOLVABLE` (the CNT-015 new code), establishing that §15.1 is the canonical catalog and inner-plan-related codes belong there. `WORKSPACE_PLAN_INVALID` is a first-class catalog miss.

This is Medium severity because: (a) the error code is the single most-referenced inner-plan error in §14, and client-side retry logic / error-message UX depends on its catalogue entry (the `details` structure, the retry category, the HTTP status code); (b) the catalog miss creates a genuine contract hole — clients reading §15.1 in isolation will not learn of this code; (c) the fix is a one-row catalog addition with no behaviour change.

**Recommendation:** Add a row to §15.1's error catalog (alphabetically between `VARIANT_ISOLATION_UNAVAILABLE` and `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`):

| Error Code | Category | HTTP | Description |
|---|---|---|---|
| `WORKSPACE_PLAN_INVALID` | `PERMANENT` | 400 | Session creation rejected because the inner `workspacePlan` sub-object failed validation — either a JSON Schema violation (e.g., `gitClone.url` not `https://`, `mode` fails `^0[0-7]{3,4}$`, unknown property on a known `source.type` variant) or a post-schema application-layer check (e.g., `mode` setuid/setgid bit set, client-supplied `resolvedCommitSha`). `details.field` identifies the offending field path (e.g., `sources[0].mode`); `details.reason` carries the specific sub-code when the failure is an application-layer check (`invalid_mode_format`, `setuid_setgid_prohibited`, `sticky_on_file_prohibited`, `gateway_written_field`, `invalid_schema_version`, ...); a JSON Schema validation report is included for pure schema failures. Not retryable as-is — the client must correct the plan. Distinct from `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (stored plan with future `schemaVersion`, HTTP 422). See [Section 14](14_workspace-plan-schema.md). |

Also mirror this row in `docs/reference/error-catalog.md`.

### CNT-025. `docs/reference/workspace-plan.md` uses `VALIDATION_ERROR` where spec uses `WORKSPACE_PLAN_INVALID` (docs-sync drift) [Low]

**Section:** 14 (spec-side), `docs/reference/workspace-plan.md` (docs-side lines 79, 91).

Per the `feedback_docs_sync_after_spec_changes.md` feedback, docs must reconcile with spec changes after each review-fix iteration before declaring convergence. The iter5 CNT-014 fix updates §14 line 101 to say `mode` violations return `400 WORKSPACE_PLAN_INVALID`. However, the docs-side page at `docs/reference/workspace-plan.md`:

- Line 79: "Invalid values are rejected at session creation with `400 VALIDATION_ERROR`." — should be `WORKSPACE_PLAN_INVALID` to match spec §14 line 101.
- Line 79 also says "**File mode (`inlineFile`, `mkdir`).**" — this omits `uploadFile`. Spec §14 line 101 covers all three variants (`inlineFile`, `uploadFile`, `mkdir`).
- Line 91: "Invalid plans are rejected with `400 VALIDATION_ERROR`." — should be `WORKSPACE_PLAN_INVALID` to match spec §14.1 line 315.

`VALIDATION_ERROR` is a separate, general-purpose code documented in `docs/reference/error-catalog.md` line 69 — it is not wrong in isolation but is not the spec's chosen code for inner-plan failures. The docs-side reference must match the spec exactly for the error-code contract to hold.

This is Low severity — the divergence is limited to one docs page, the docs page explicitly says it is "a reference distillation" pointing to the spec as source of truth, and the client-observable HTTP status (400) is the same. But the code mismatch is a real contract break for tooling that grep-matches error codes.

**Recommendation:** Apply three small edits to `docs/reference/workspace-plan.md`:

1. Line 79: "**File mode (`inlineFile`, `uploadFile`, `mkdir`).** The optional `mode` field on these sources is a POSIX-style octal string matching the regex `^0[0-7]{3,4}$` (e.g., `\"0644\"`, `\"0755\"`). The setuid (`04000`) and setgid (`02000`) bits are prohibited for all source types; the sticky bit (`01000`) is accepted only on `mkdir`. Invalid values are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`."
2. Line 91: "Plans are validated against the registered JSON Schema (`schemaVersion`) at session creation. Invalid plans are rejected with `400 WORKSPACE_PLAN_INVALID`."
3. If CNT-024 is adopted, add the corresponding `WORKSPACE_PLAN_INVALID` row to `docs/reference/error-catalog.md` alongside the existing `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` row.

## Convergence assessment

- **Three iter5 items remain open** (CNT-017 Low, CNT-018 Low, CNT-019 Low), all documentation-only fixes within §14, §14.1, and §16.6. CNT-017 is now on its fourth iteration without fix — this is the longest-lived unresolved CNT finding and, while Low severity, warrants attention as a convergence signal.
- **Two iter5 items were fixed** (CNT-014 Medium, CNT-015 Medium), verified end-to-end in the iter5 fix commit (c941492). Both fixes are precise, cross-section consistent (§14 ↔ §15.1), and include the requested detail (regex, reason codes, `GIT_CLONE_REF_UNRESOLVABLE` catalog entry, `resolvedCommitSha` response surfacing).
- **Seven new iter6 findings** (CNT-020 Medium, CNT-021 Low, CNT-022 Low, CNT-023 Medium, CNT-024 Medium, CNT-025 Low, plus carry-forwards CNT-017/CNT-018/CNT-019):
  - Three Medium findings (CNT-020 `resolvedCommitSha` schema asymmetry, CNT-023 `oneOf`-vs-`allOf` JSON Schema correctness, CNT-024 `WORKSPACE_PLAN_INVALID` catalog miss) are iter5-fix-adjacent polish items — each fix surfaces a small but real specification gap that the iter5 additions made visible. None is a structural redesign; all are bounded to §14, §14.1, and §15.1.
  - Four Low findings (CNT-017 / CNT-018 / CNT-019 carry-forwards, CNT-021 `ls-remote` precedence, CNT-022 error-code-reservation wording, CNT-025 docs-sync drift) are documentation-polish items with no behaviour implications.
- **No Critical or High findings this iteration.** No cross-perspective dependencies surfaced — all findings resolvable within §14, §14.1, §15.1, §16.6, and `docs/reference/{workspace-plan,error-catalog}.md`.
- **Recommendation:** Perspective 18 is **not yet converged** at iter6 due to three carry-forward Lows (CNT-017, CNT-018, CNT-019) persisting across multiple iterations, plus three new Mediums (CNT-020, CNT-023, CNT-024). The carry-forwards are all one-sentence documentation fixes and the new Mediums are all bounded edits to a single section — resolution in iter7 is straightforward. CNT-017 in particular (now four iterations unfixed) should be prioritised. If iter7 addresses all three Mediums (CNT-020, CNT-023, CNT-024) and the three Low carry-forwards (CNT-017, CNT-018, CNT-019), Perspective 18 converges at iter7 with only CNT-021/CNT-022/CNT-025 as optional polish.
