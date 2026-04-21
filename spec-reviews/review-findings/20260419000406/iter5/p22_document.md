# Iter5 Review — Perspective 22: Document Quality, Consistency & Completeness

**Scope.** Cross-reference integrity (intra-file and cross-file anchors), README TOC completeness, heading/title clarity, terminology consistency, typos. Per `feedback_severity_calibration_iter5.md`, severities are anchored to the iter1–iter4 rubric for this class of defect: intra-/cross-anchor resolution failures remain **Medium** (they silently misdirect readers and are invisible on GitHub preview), heading-clarity and README-TOC issues remain **Low** (navigation friction, no ambiguity in normative text), and typos are **Low** unless they change normative meaning.

**Method.** (1) Verified iter4 "Fixed" items (DOC-013 / DOC-014 / DOC-015 / DOC-016). (2) Confirmed iter4 carry-forwards (DOC-017 / DOC-018). (3) Programmatic anchor sweep: parsed every heading in `spec/*.md` to produce the authoritative anchor set, then matched every `](file.md#anchor)` and `](#anchor)` target in the spec against that set. 2,004 link targets scanned; 3 broken cross-anchors surfaced, all of which are new regressions introduced since iter4. (4) Programmatic README TOC parity check vs. actual `X.Y` headings, confirming DOC-018 scope exactly.

**Numbering.** DOC-019 onward (after iter4's DOC-018).

---

## Iter4 status re-verification

- **DOC-013** — `#1781-operational-defaults--quick-reference` in `10_gateway-internals.md`. **Fixed in iter4.** Both remaining references (`10_gateway-internals.md:139`, `:155`) now use the cross-file form `(17_deployment-topology.md#1781-operational-defaults--quick-reference)`. No intra-file form survives in `10_gateway-internals.md`.
- **DOC-014** — `25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops`. **Fixed in iter4.** Zero occurrences of the fabricated fragment in `spec/`. `13_security-model.md` Gateway row now points at the canonical `#253-gateway-side-ops-endpoints`.
- **DOC-015** — `25_agent-operability.md#251-overview`. **Fixed in iter4.** Zero occurrences in `spec/`. Retargeted to `#254-the-lenny-ops-service`, which is the normative home of the "`lenny-ops` is mandatory" statement.
- **DOC-016** — `16_observability.md#165-alerts`. **Fixed in iter4.** Zero occurrences in `spec/`.
- **DOC-017** — headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics". **Still unfixed** (now fourth iteration). Re-filed as DOC-022 below at the iter4 severity (Low).
- **DOC-018** — README TOC omissions of `4.0`, `24.0`, `18.1`. **Still unfixed** (now fourth iteration). Re-filed as DOC-023 below at the iter4 severity (Low).

---

## 21. Documentation & Cross-Reference Integrity

### DOC-019. Cross-file anchor `17_deployment-topology.md#179-preflight-checks` does not exist [Medium]

**Section:** `13_security-model.md:211` (NET-067 DNS egress peer requirement blockquote)

Introduced by the iter3/iter4 NET-067 fix ("DNS egress peer requirement"). The blockquote reads `... The `lenny-preflight` Job enforces this via the "NetworkPolicy DNS `podSelector` parity" check ([Section 17.9](17_deployment-topology.md#179-preflight-checks)) and fails the install/upgrade on any DNS egress rule whose peer omits `podSelector`.` The fragment `#179-preflight-checks` does not resolve: `17.9` in `17_deployment-topology.md:1306` is "Deployment Answer Files" (anchor `#179-deployment-answer-files`), not "Preflight Checks". There is no heading named "Preflight Checks" at any level in `17_deployment-topology.md`. The actual "NetworkPolicy DNS `podSelector` parity (NET-067)" check lives at line 493 inside the table under `#### Checks performed` (line 465), which is itself inside `### 17.6 Packaging and Installation` (anchor `#176-packaging-and-installation`). The anchor emitted by GitHub for the subheading `#### Checks performed` is `#checks-performed` (unqualified), so the citation silently points a reader at a completely different section (§17.9 Answer Files). This is the **same class of defect as iter4 DOC-014/015/016** (a cross-anchor that references a non-existent fragment), and it was introduced in the same NET-067 fix commit chain — the self-verification was not performed end-to-end.

**Recommendation:** Change `[Section 17.9](17_deployment-topology.md#179-preflight-checks)` → `[Section 17.6](17_deployment-topology.md#checks-performed)`. Alternatively, if the reviewer prefers a section-number-qualified citation, the canonical heading-level anchor is `#176-packaging-and-installation` and the prose should then direct the reader to the "Checks performed" table therein — but `#checks-performed` is a valid GitHub slug in that file (there is no other heading with that title in `17_deployment-topology.md`) and the section-number-free form is cleaner.

### DOC-020. Cross-file anchor `15_external-api-surface.md#152-mcp-endpoints` does not exist [Medium]

**Section:** `25_agent-operability.md:3625` (Audit Log Query API — `POST /v1/admin/audit-events/{id}/republish` scope note)

The `POST /v1/admin/audit-events/{id}/republish` row ends `... a caller lacking the scope receives `403 FORBIDDEN` (scope taxonomy: `tools:audit:republish`, [§15.2](15_external-api-surface.md#152-mcp-endpoints))`. The fragment `#152-mcp-endpoints` does not resolve: `### 15.2` at `15_external-api-surface.md:1256` is titled "MCP API", producing the canonical anchor `#152-mcp-api`. No heading named "MCP Endpoints" exists at any level in `15_external-api-surface.md` (verified by grep of `^#+\s`). Same class of defect as DOC-014 — a fabricated anchor fragment that silently points GitHub to the top of the target file. The parallel citation on the preceding row (line 3624, `[§11.7](11_policy-and-controls.md#117-audit-logging)`) resolves correctly, indicating this is a localized slip in the `republish` row rather than a rename not propagated.

**Recommendation:** Change `[§15.2](15_external-api-surface.md#152-mcp-endpoints)` → `[§15.2](15_external-api-surface.md#152-mcp-api)`.

### DOC-021. Cross-file anchor `15_external-api-surface.md#154-error-codes` does not exist [Medium]

**Section:** `25_agent-operability.md:4418` (MCP Management Server — `lenny_tenant_force_delete` row)

The row reads `... without the override, or when omitted, tenant-delete is rejected with `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` ([§15.4](15_external-api-surface.md#154-error-codes)) if holds exist. ...`. The fragment `#154-error-codes` does not resolve: `### 15.4` at `15_external-api-surface.md:1396` is titled "Runtime Adapter Specification" (anchor `#154-runtime-adapter-specification`). No "Error Codes" heading exists at any level in `15_external-api-surface.md`. The error-code table for the REST/admin surface is actually inside §15.1 (REST API) — searching for the canonical error code `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` would land the reader inside §15.1's REST tables, not §15.4 which is the adapter wire protocol. This is the same class of defect as DOC-014/015/016/019/020 — a fabricated anchor that silently misdirects. The prose intent is unambiguous ("the error code is defined in §15") but the specific landing target is wrong.

**Recommendation:** Two options: (a) retarget to the section where `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` is actually defined — search the spec for the code's defining row. A best-effort retarget is `[§15.1](15_external-api-surface.md#151-rest-api)` if the admin-API error is catalogued under §15.1. (b) If the intended reference was a general error-code index that does not yet exist, either create the index section with a stable anchor or drop the anchor fragment and cite the section-level anchor only. Pending a reviewer decision, a safe minimal fix is `[§15](15_external-api-surface.md#15-external-api-surface)` — inelegant but resolves.

### DOC-022. Re-file of iter4 DOC-017 — "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" headings remain [Low]

**Files:** `spec/16_observability.md:617, 643`; `spec/README.md:105–106`

Unfixed for **four iterations** (iter1 DOC-002 → iter2 DOC-006 → iter3 DOC-011 → iter4 DOC-017 → iter5). The headings still juxtapose `16.7` / `16.8` with "Section 25", which reads as a structural inconsistency on first pass (two section numbers in one heading) and is mirrored verbatim in the README TOC. Iter1-through-iter4 recommendations have been consistent:

**Recommendation (unchanged):** Rename to `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics` in `spec/16_observability.md`. Open each subsection body with a single-sentence cross-reference such as "Introduced by §25 (Agent Operability)." Update the two mirrored lines in `spec/README.md:105–106` to the new titles (the anchors change too — from `#167-section-25-audit-events` / `#168-section-25-metrics` to `#167-agent-operability-audit-events` / `#168-agent-operability-metrics`; grep the tree for any callers and rewrite them in the same commit). If the platform policy is to keep "Section 25" in the heading verbatim, record that decision explicitly in this finding's resolution note so the re-file cycle stops.

### DOC-023. Re-file of iter4 DOC-018 — README TOC still omits three numbered subsections [Low]

**File:** `spec/README.md`

Unfixed for **four iterations** (iter2 DOC-007 → iter3 DOC-012 → iter4 DOC-018 → iter5). Programmatic verification against every `X.Y` heading in `spec/` (pattern: `^\d+\.\d+\s`, non-`X.Y.Z`) confirms exactly three omissions:

- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README lines 14–23 list 4.1 through 4.9 but not 4.0.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — README line 119 lists §18 as a parent with zero child entries, unlike every other multi-subsection chapter.
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README lines 127–147 list 24.1 through 24.20 but not 24.0.

All three target anchors resolve correctly and are referenced in running prose (e.g., `17_deployment-topology.md:328` cites `#240-packaging-and-installation` successfully). The defect is purely TOC-level: readers scanning the table of contents do not learn these subsections exist, and any reader who lands on §4, §18, or §24 via the TOC misses the introductory overview content.

**Recommendation:** Insert three TOC lines in `spec/README.md` using the exact indentation the README already applies to level-3 entries:

```
  - [4.0 Agent Operability Additions](04_system-components.md#40-agent-operability-additions)
  - [18.1 Build Artifacts Introduced by Section 25](18_build-sequence.md#181-build-artifacts-introduced-by-section-25)
  - [24.0 Packaging and Installation](24_lenny-ctl-command-reference.md#240-packaging-and-installation)
```

The file-scoped programmatic anchor sweep confirms all three slugs resolve. Insert the §4.0 line between the current `- [4. System Components]` parent and `- [4.1 Edge Gateway Replicas]`; insert the §18.1 line as the first (and currently only) child of `- [18. Build Sequence]`; insert the §24.0 line between the current `- [24. `lenny-ctl` Command Reference]` parent and `- [24.1 Bootstrap]`. As with DOC-022, if the README convention is specifically that `X.0` overviews and single-subsection chapters are intentionally omitted, record that convention in this finding's resolution note so the re-file cycle terminates; otherwise the fix is three one-line inserts.

---

## Convergence assessment

**Cross-reference integrity (Medium-severity class).** Iter4's four anchor-integrity defects (DOC-013–016) are all verified fixed in-place, with zero surviving references to the broken fragments. However, three **new** anchor regressions were introduced in the same iter3 → iter4 commit chain that closed the prior ones (DOC-019 from NET-067, DOC-020 and DOC-021 from §25.9 audit-query surface edits and §25.12 MCP Management Server edits). This is the same failure mode called out explicitly in iter4 DOC-013 ("iter3 CPS-004 introduced the exact class iter3 DOC-008 had closed") and in iter4 DOC-014/015 ("the NET-051 self-verification note claimed the anchors resolved; they did not"). **The pattern of self-verification claims that were not actually executed is the root cause.** Convergence on this class requires an automated anchor-resolution check in CI before fix commits land — a 20-line Python script reading every `](file.md#anchor)` and matching it against the heading-derived slug set would have blocked DOC-014/015/016/019/020/021 at PR time. Until that CI gate exists, every new iteration should expect 2–4 new anchor regressions introduced by that iteration's non-DOC fixes.

**Navigation/heading clarity (Low-severity class).** DOC-022 (§16.7/§16.8 "Section 25 Audit Events/Metrics") and DOC-023 (README TOC omissions of §4.0 / §18.1 / §24.0) have now persisted **four iterations** each with unchanged recommendations and trivial fix costs (a three-line README patch for DOC-023, a two-heading rename plus README mirror update for DOC-022). Both are deliberate-looking enough that the reviewer cycle cannot distinguish "accepted" from "deferred" without an explicit policy statement. **Recommendation for the convergence meta-discussion:** adopt the rule that any Low-severity finding surviving three consecutive iterations with unchanged recommendation MUST receive either (a) a fix commit or (b) an explicit "accepted — will not fix" resolution note in the next iteration's fix pass. Open-ended re-files are a symptom of unclear ownership, not of genuine disagreement.

**Overall iter5 state for Perspective 22.** Five findings this iteration (three Medium anchor regressions and two Low carry-forwards). The Medium items are mechanically trivial to fix but their recurrence is the strongest signal in this perspective and warrants the CI-gate remediation above — the convergence blocker is a process gap, not a content gap. The Low items should be either fixed or formally accepted in iter5's fix pass per the rule proposed above, ending the re-file cycle. No findings rose to High severity because none of the broken anchors produced normative ambiguity in running text: in each case the prose surrounding the broken link unambiguously names the concept being referenced, so a reader losing the hyperlink still resolves the correct concept via text search. The risk is silent misdirection to the wrong section, not misimplementation of a normative requirement.
