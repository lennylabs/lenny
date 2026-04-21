# Perspective 18 — Content Model, Data Formats & Schema Design (Iter5)

Scope: `WorkspacePlan` (§14), `OutputPart` / `MessageEnvelope` (§15), `RuntimeDefinition` / `runtimeOptionsSchema` (§5.1). Iter5 revisits iter4 CNT-007…CNT-011 and audits the newly-adjusted §14 `sources[]` catalogue (CNT-008 iter4 fix, CNT-009 iter4 fix, CNT-011 iter4 fix) plus the published JSON Schema descriptor language introduced in §14.1.

## Iter4 carry-over verification

| Iter4 ID | Title (summary) | Iter5 status |
|---|---|---|
| CNT-007 [High] | `gitClone` SSH URL contract unresolved | **Fixed** — §14 line 93 now restricts `gitClone.url` to HTTPS and pins the JSON Schema `pattern` to `^https://`; SSH deferred to §21.9. The `GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `_HOST_AMBIGUOUS` codes (§15.1) are scoped to HTTPS. |
| CNT-008 [Low] | §14 `gitClone.auth` paragraph lacks session-binding sentence | **Open** — carried forward as **CNT-012** (below). §14 line 95 still omits the session-id / `credential.leased` sentence; the equivalent sentence at §26.2 line 119 ("The lease is bound to the originating session ID for audit traceability.") is present but remains unpaired in the schema-of-record. |
| CNT-009 [Medium] | `uploadArchive.stripComponents` semantics undefined for `zip` | **Fixed** — §14 line 100 defines a format-independent `/`-split algorithm, applies it to `tar`, `tar.gz`, and `zip`, specifies skip behaviour for too-few-segments and empty-post-strip entries, and introduces the `workspace_plan_strip_components_skip` warning event. §7.4 line 459 cross-references back to this definition. |
| CNT-010 [Low] | `workspace_plan_unknown_source_type` / `workspace_plan_path_collision` not in §16.6 catalog | **Open** — carried forward as **CNT-013** (below). Neither event is enumerated in §16.6; iter4's fix introduced a third warning (`workspace_plan_strip_components_skip`) which also needs registration. |
| CNT-011 [Medium] | Published `WorkspacePlan` JSON Schema per-variant `additionalProperties` policy unspecified | **Fixed** — §14 line 332 "Per-variant field strictness" adds `additionalProperties: false` for every known `source.type` variant, describes the `oneOf` / `if`-`then` branching over the `type` discriminator, and explicitly resolves the mixed-shape case. |

## New findings

### CNT-012. §14 `gitClone.auth` paragraph still lacks session-binding sentence (iter4 CNT-008 unfixed) [Low]

**Section:** 14 (line 95 `gitClone.auth` paragraph), 26.2 (line 119), 4.9 (`credential.leased` audit event)

The `gitClone.auth` paragraph in §14 describes lease-scope resolution, host-pattern matching, and the HTTPS credential-helper flow but still omits the normative statement that the lease is session-scoped and traceable via the `credential.leased` audit event. §26.2 line 119 carries the paired sentence ("The lease is bound to the originating session ID for audit traceability.") but §14 is the schema-of-record for the `gitClone` source (CNT-002 iter2 established this), so the binding obligation belongs in §14 as well — auditors and client authors who consult §14 directly have no line of sight to §26.2. This is a documentation drift, not a functional gap (the gateway emits the audit event regardless of where the sentence lives), hence the Low severity, which matches iter4's CNT-008 rating under the severity-calibration rule for prior-iteration carry-overs.

**Recommendation:** Append one sentence to §14 line 95 after the credential-helper sentence: "The lease issued for a `gitClone` source is bound to the originating `session_id` and recorded in the `credential.leased` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)) for traceability." This is verbatim the iter4 recommendation for CNT-008 and is sufficient to close the drift.

### CNT-013. §14 workspace-plan warning events absent from §16.6 Operational Events Catalog (iter4 CNT-010 unfixed, now extended) [Low]

**Section:** 14 (lines 100 `workspace_plan_strip_components_skip`, 330 `workspace_plan_unknown_source_type`, 334 `workspace_plan_path_collision`), 16.6 (Operational Events Catalog enumeration at lines 605, 615)

§14 defines three gateway-emitted warning events associated with `WorkspacePlan` materialization:

1. `workspace_plan_unknown_source_type` (§14 line 330) — per skipped source entry when `source.type` is unrecognized.
2. `workspace_plan_path_collision` (§14 line 334) — per detected last-writer-wins overwrite during materialization.
3. `workspace_plan_strip_components_skip` (§14 line 100) — per skipped archive entry when `stripComponents` exceeds the entry's segment count. Introduced by the iter4 CNT-009 fix.

None of the three appears in the §16.6 "Gateway-emitted" enumeration (line 605) or any adjacent catalog section. §16.6 line 603 declares itself "the canonical enumeration" of operational events, and other §14-originated events (e.g., `session_completed` / `session_failed`) are listed, so the absence is real — consumers that filter the SSE stream, Redis stream, or in-memory buffer ([§25.5](25_agent-operability.md#255-operational-event-stream)) against the catalog will drop these three as unknown. Iter4 rated the two-event version of this finding Low; adding the third event (CNT-009-induced) does not raise severity because the impact — silent filtering by catalog-driven consumers — is identical.

**Recommendation:** Add a new bullet or dedicated paragraph to §16.6 after the experiment-events paragraph (line 613) enumerating the three workspace-plan warning events and their payload field sets, e.g.:

> **Workspace plan events (gateway-emitted, operational).** The gateway emits the following warning events during `WorkspacePlan` validation and materialization ([§14](14_workspace-plan-schema.md)). All are CloudEvents with `type: dev.lenny.<short_name>` and flow through the same Redis stream / in-memory buffer as the gateway-emitted events above.
>
> - `workspace_plan_unknown_source_type` (warning) — emitted when a consumer encounters an unknown `source.type` and skips the source entry per the open-string extensibility contract. Payload fields: `tenant_id`, `session_id`, `schemaVersion`, `unknownType`, `sourceIndex`.
> - `workspace_plan_path_collision` (warning) — emitted when two or more `sources` entries resolve to the same workspace path during materialization and the later entry wins under the last-writer-wins rule. Payload fields: `tenant_id`, `session_id`, `path`, `winningSourceIndex`, `losingSourceIndex`.
> - `workspace_plan_strip_components_skip` (warning) — emitted per skipped archive entry when `uploadArchive.stripComponents` exceeds the entry's segment count or the post-strip path is empty. Payload fields: `tenant_id`, `session_id`, `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`.

Also update the line-605 inline list to include these three `short_name`s so the "Gateway-emitted:" one-liner remains exhaustive.

### CNT-014. `inlineFile.mode` / `uploadFile.mode` / `mkdir.mode` string format has no regex, range, or setuid/setgid/sticky-bit constraint [Medium]

**Section:** 14 (lines 87 `inlineFile.mode`, 88 `uploadFile.mode`, 90 `mkdir.mode`, 14.1 lines 309, 332 Published JSON Schema / `additionalProperties: false`)

The three `mode` optional fields are described uniformly as "octal string, default `0644`" (or `0755` for `mkdir`) but the published schema description in §14.1 does not specify:

1. **Regex / pattern.** Clients could submit `"644"` (no leading zero), `"0o644"` (Go/Python prefix), `"0x1A4"` (hex), `"rw-r--r--"` (symbolic), or even arbitrary non-numeric strings. Without an explicit `pattern` the JSON Schema validator will accept any string and the gateway implementation becomes the de facto spec — different gateway versions may parse differently.
2. **Numeric range / allowed bit-set.** No constraint prevents values such as `"06777"` (setuid+setgid+sticky+777) or `"04755"` (setuid+755). Setuid/setgid/sticky bits on a file materialized inside `/workspace/current/` are not inherently unsafe (the pod's effective UID is the sandbox user), but the absence of a documented bit-mask means a future defense-in-depth hardening (e.g., reject setuid/setgid) would be a breaking change. The spec should pin the allowed range explicitly now while the schema is still at v1.
3. **Symbolic-vs-octal canonicalization.** `0644` and `"644"` are both unambiguous as octal, but the spec does not say the validator accepts both or only the 4-digit form shown in the examples. CNT-009 (iter4) fixed the equivalent ambiguity for `stripComponents` by defining a canonical algorithm independent of format; the same clarity is missing here.

This is a Medium-severity gap because (a) the schema is published and third-party clients will encode against it (wire-format contract), (b) `mode` semantics on a Unix filesystem are security-relevant even inside a sandbox (setuid bits propagate through archive extraction and can interact with user-namespace mappings on hosts that remap UIDs), and (c) the fix is schema-only — no code change is required, matching the "SHOULD fix, has workaround" Medium bar.

**Recommendation:** In §14 line 100 area (within the "Field notes:" block) add a `mode` normalization note and amend the published JSON Schema to enforce it. Proposed language:

> - **`mode` (all variants — `inlineFile`, `uploadFile`, `mkdir`).** Octal string representing Unix file permissions. The JSON Schema constrains `mode` with `"type": "string", "pattern": "^0[0-7]{3,4}$"`: three or four octal digits, leading zero required. The four-digit form encodes setuid (`04xxx`), setgid (`02xxx`), and sticky (`01xxx`) bits; the three-digit form is equivalent to leading `0` on the high bits (no special bits set). V1 rejects `mode` values containing setuid or setgid bits (`04xxx` or `02xxx`) with `400 WORKSPACE_PLAN_INVALID` (`details.field = "sources[<n>].mode"`, `details.reason = "setuid_setgid_prohibited"`); sticky (`01xxx`) is permitted on `mkdir` but rejected on `inlineFile` / `uploadFile` (sticky on a regular file is a legacy Linux feature with no defined semantics on modern kernels). Non-matching strings (`"644"`, `"rw-r--r--"`, `"0o644"`, etc.) are rejected under the same error code with `details.reason = "invalid_mode_format"`. The gateway parses the validated string with `strconv.ParseUint(mode, 8, 32)` and applies it via `os.Chmod` after file write.

The `additionalProperties: false` already present for each variant (CNT-011 iter4 fix) means this amendment is isolated to the `mode` keyword's schema object and does not affect the `oneOf` / `if`-`then` branching.

### CNT-015. `gitClone.ref` reproducibility semantics undefined — branch-name refs drift between session creation and resume/retry [Medium]

**Section:** 14 (line 91 `gitClone` row `ref` field), 14.1 (line 322 Gateway reconciliation / resumed or retried sessions), 7.2 (session resume semantics)

The `gitClone` `ref` field is described as "branch, tag, or commit SHA" (line 91) with no guidance on reproducibility. Because §14.1 line 322 requires the gateway to "read back the stored `WorkspacePlan` when replaying workspace setup for resumed or retried sessions", a plan that specifies a mutable ref (a branch name like `main`, or even a floating tag) can materialize different repository contents at:

1. Session creation (first materialization).
2. Any retry within the `maxResumeWindowSeconds` window ([§14](14_workspace-plan-schema.md) `retryPolicy.maxResumeWindowSeconds`).
3. Any resumed session after gateway eviction / checkpoint restore.

This violates the implicit expectation — reinforced by §14.1 "Gateway reconciliation (live consumer)" — that replay produces the same workspace the first attempt saw. A client debugging a session failure by resuming it may see a different `main` than the one that triggered the failure; a retry of a failed build may succeed against a newer commit that wasn't in the original failure scope; a deterministic-retry contract claimed by `retryPolicy.mode: auto_then_client` is silently violated for any plan that specifies a branch. The contract asymmetry with `uploadArchive` / `uploadFile` / `inlineFile` — all of which reference immutable uploaded content via `uploadRef` or embedded `content` — is stark.

This is Medium severity because: (a) the behaviour is surprising and undocumented; (b) the fix is partly a documentation update and partly a gateway resolution step that SHOULD be specified now while the schema is at v1; and (c) the contract distortion affects audit replayability and billing-event-driven rebuilds ([§25.9](25_agent-operability.md#259-audit-log-query-api)).

**Recommendation:** Append a paragraph to §14 after line 95 (or within the "Field notes:" block) specifying ref resolution semantics:

> - **`gitClone.ref` resolution.** At session creation, the gateway resolves `ref` to an immutable commit SHA by performing a `git ls-remote` against the target repository (using the same credential-lease as the clone itself). The resolved SHA is persisted alongside the stored `WorkspacePlan` as `sources[<n>].resolvedCommitSha`. All subsequent materializations for the same session (retries, resumes, checkpoint restores) clone `resolvedCommitSha` rather than re-resolving `ref`, so a moving branch or tag does not change the workspace contents across the session's lifetime. `resolvedCommitSha` is a read-only, gateway-written field — clients MAY observe it in `GET /v1/sessions/{id}` for audit purposes but MUST NOT set it at session creation. When `ref` is already a 40-character hexadecimal string, the gateway treats it as a commit SHA and skips the `ls-remote` step. New sessions that share a `WorkspacePlan` template (recursive delegation children, session-from-session retries outside the original session's `maxResumeWindowSeconds`) re-resolve `ref` and MAY see a different `resolvedCommitSha` than the parent — the guarantee is per-session, not per-plan.

Also add a corresponding sentence to §14.1 line 322 referring to `resolvedCommitSha` so the reconciliation loop's contract is self-contained.

### CNT-016. Published `WorkspacePlan` JSON Schema lacks `minimum: 1` on `schemaVersion` [Low]

**Section:** 14.1 (line 316 `schemaVersion` field; line 309 Published JSON Schema)

`schemaVersion` is described as an "integer" field identifying the schema revision. The spec normatively defines producer/consumer obligations for higher-than-known versions and for durable-consumer forward-read, but the published JSON Schema description does not pin a lower bound. A plan submitted with `"schemaVersion": 0` or `"schemaVersion": -1` is not covered by any of §14.1's normative clauses (the "higher than I understand" rule assumes positive integers). The gateway's implementation will presumably reject zero/negative values, but the schema-of-record should document the constraint so third-party validators reject on parse and the audit/analytics forward-read rule doesn't encounter undefined-behaviour values.

This is Low severity — no documented adversarial use, easy fix, no impact on existing v1 behaviour.

**Recommendation:** In §14.1 line 316 (or the Published JSON Schema block at line 309), add: "The published schema constrains `schemaVersion` with `{"type": "integer", "minimum": 1}` — values less than 1 are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`."

## Convergence assessment

- **Two iter4 items remain open** (CNT-012, CNT-013) both Low severity, both documentation-only fixes within §14 and §16.6. Neither blocks convergence on a structural basis.
- **Three new iter5 items** (CNT-014 Medium `mode` format, CNT-015 Medium `gitClone.ref` reproducibility, CNT-016 Low `schemaVersion` minimum) are schema-surface polish items that the iter4 CNT-011 fix (per-variant strictness declaration) naturally surfaces by putting the published JSON Schema under review — each is bounded, isolated to §14 / §14.1, and does not imply a structural redesign.
- **No Critical or High findings this iteration.** The HIGH-severity iter4 item (CNT-007 SSH URL) is fully fixed, and iter5 did not uncover a new HIGH. The schema-of-record for `WorkspacePlan` is internally consistent and the iter4 fixes hold.
- **Recommendation:** Perspective 18 is on track to converge at iter6 if CNT-012 and CNT-013 (carry-overs), plus CNT-014 and CNT-015 (Medium), are addressed. CNT-016 can be deferred to a post-convergence polish pass without risk. No cross-perspective dependencies were surfaced — all findings are resolvable within §14, §14.1, and §16.6 alone.
