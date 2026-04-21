# Iter7 Review — Perspective 22: Document Quality, Consistency & Completeness

**Scope.** Cross-reference integrity (intra-file and cross-file anchors), README TOC completeness, heading/title clarity, terminology consistency, label/target coherence. Per `feedback_severity_calibration_iter5.md`, severities are anchored to the iter1–iter6 rubric for this class of defect: intra-/cross-anchor resolution failures remain **Medium** (silent misdirection, invisible on GitHub preview); heading-clarity and README-TOC issues remain **Low**; label/target coherence remains **Low**/**Info** per DOC-027/028 precedent.

**Method.** (1) Verified iter6 "Fixed" items (DOC-024 / DOC-025) against the current spec. (2) Re-verified iter6 carry-forwards (DOC-026 / DOC-027 / DOC-028 / DOC-029 / DOC-030). (3) Programmatic anchor sweep: parsed every heading in `spec/*.md` with a GitHub-compliant slugger (github-slugger semantics — lowercase, strip punctuation not in `[a-z0-9_\s\-]`, replace each whitespace char with hyphen preserving consecutive-hyphen output from dropped punctuation), producing the authoritative anchor set. Matched all 2,819 `[label](file.md#anchor)` and `[label](#anchor)` link targets in `spec/` against that set. (4) Also matched all intra-`docs/` anchor links (0 broken — docs site uses Jekyll/kramdown with `attr_list` support so `{: #id }` explicit anchors resolve) and all `docs/`→`spec/` cross-file anchor links (0 broken). (5) Programmatic README TOC parity check against every `^## X\.` and `^### X\.Y` heading in `spec/`. (6) `git diff c941492..8604ce9 -- spec/` to isolate exactly what iter6's fix commit added/removed, so new regressions are attributable to iter6 vs. earlier commits. (7) Programmatic section-label vs. anchor-prefix mismatch scan: for every `[§X.Y](file.md#anchor)` link, verified that `anchor`'s numeric prefix starts with `X.Y` (allowing the `X.Y`-prefix sibling `X.Y.Z` targets to match).

**Numbering.** DOC-031 onward (after iter6's DOC-030).

---

## Iter6 status re-verification

- **DOC-024** — `15_external-api-surface.md#154-errors-and-degradation`. **Fixed in iter6 (commit 8604ce9).** Programmatic sweep: zero occurrences of `#154-errors-and-degradation` remain in `spec/`. All 9 call sites now reference `#151-rest-api` (canonical §15.1 REST API anchor, present at `15_external-api-surface.md:585`). Visible labels also updated — spot-check of `spec/11_policy-and-controls.md:423, 443, 445, 449` / `spec/15_external-api-surface.md` / `spec/16_observability.md` confirms labels read `§15.1` / `[§15.1]` / `[Section 15.1]` to match the retargeted anchor, so iter6 DOC-027 (the co-occurring label mismatch) is also resolved in the same patch.
- **DOC-025** — `17_deployment-topology.md#1781-helm-values`. **Fixed in iter6 (commit 8604ce9).** Programmatic sweep: zero occurrences of `#1781-helm-values` remain in `spec/`. All 3 call sites now reference `#176-packaging-and-installation` (canonical §17.6 Packaging and Installation anchor, present at `17_deployment-topology.md:341`). Visible labels updated from `§17.8.1` → `§17.6` to match, so iter6 DOC-028 (the visible-title vs. prose-content navigation note) is moot — the retarget chose `#176-packaging-and-installation` (a different subsection from iter6's `#1781-operational-defaults--quick-reference` recommendation), which happens to side-step the DOC-028 concern entirely because §17.6 Packaging and Installation is where Helm chart and values material actually lives.
- **DOC-026** — `docs/api/internal.md#lifecycle-channel-messages`. **Not a real broken link on the published docs site; reclassified below.** The target heading at `docs/api/internal.md:312` uses the Jekyll/kramdown `attr_list` explicit-ID syntax (`## Lifecycle channel messages (Full integration level only)` on line 312 followed by `{: #lifecycle-channel-messages }` on line 313). The docs site is built with Jekyll (see `docs/_config.yml:101` `markdown: kramdown` + `input: GFM`), and kramdown honors `attr_list` IDs. The anchor `#lifecycle-channel-messages` **works on the published docs site**; it only fails when a reader views `docs/api/internal.md` on GitHub's raw-markdown blob view (GitHub does not process `attr_list`). This is a **genuine but narrow** GitHub-preview-only degradation, not a broken link in the primary rendering target — **re-filed at Info** (below, DOC-034) rather than Low, with note that it was a false Low-positive in iter6.
- **DOC-027** — Label/target mismatch `§15.4` label / `§15.1` anchor. **Fixed in iter6 as part of DOC-024.** Every previously co-occurring label was updated in the same commit. Zero surviving `§15.4` labels point to REST-error-catalog anchors.
- **DOC-028** — Visible-title vs. prose mismatch at DOC-025 sites. **Moot, not directly "fixed".** The iter6 fix retargeted `#1781-helm-values` → `#176-packaging-and-installation` (§17.6) instead of iter6's recommended `#1781-operational-defaults--quick-reference` (§17.8.1). §17.6 is Packaging and Installation (title matches "Helm chart"/"Helm values" concept); the reader arrives at a topically-correct heading. No longer a concern.
- **DOC-029** — Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics". **Still unfixed** (now sixth iteration). Both headings remain literally at `16_observability.md:648, 677` and the README TOC mirrors them verbatim at `spec/README.md:105–106`. Re-filed below as DOC-032 at unchanged severity (Low).
- **DOC-030** — README TOC omissions of `4.0`, `18.1`, `24.0`. **Still unfixed** (now sixth iteration). Programmatic re-verification against the current `spec/` tree confirms the same three X.Y omissions; no other X.Y-level omission exists. All three target anchors resolve (`#40-agent-operability-additions` at `04_system-components.md:3`, `#181-build-artifacts-introduced-by-section-25` at `18_build-sequence.md:75`, `#240-packaging-and-installation` at `24_lenny-ctl-command-reference.md:19`) and are cited in running prose. Re-filed below as DOC-033 at unchanged severity (Low).

---

## 22. Documentation & Cross-Reference Integrity

### DOC-031. Cross-file anchor `12_storage-architecture.md#124-quota-and-rate-limiting` does not exist — introduced by iter6 OBS-037 fix [Medium]

**File:** `spec/16_observability.md:203`

The single reference is `| Quota user fail-open fraction (\`lenny_quota_user_failopen_fraction\`, gauge without labels — the gateway's currently configured \`quotaUserFailOpenFraction\` value (default \`0.25\`), emitted at startup and on config reload; evaluated by \`QuotaFailOpenUserFractionInoperative\` to fire when the per-user fail-open cap is substantially weakened (\`>= 0.5\`); see [Section 12.4](12_storage-architecture.md#124-quota-and-rate-limiting))`. The fragment `#124-quota-and-rate-limiting` does **not** resolve in `12_storage-architecture.md`: §12.4 at line 173 is titled "Redis HA and Failure Modes" (canonical anchor `#124-redis-ha-and-failure-modes`). There is no heading titled "Quota and Rate Limiting" at any level in that file (`grep -nE '^#+.*[Qq]uota and [Rr]ate [Ll]imiting' spec/12_storage-architecture.md` returns zero matches).

The row was **introduced by the iter6 fix commit (`8604ce9`, OBS-037 — "added `lenny_gateway_quota_user_failopen_fraction` and `lenny_storage_quota_bytes_limit` backing gauges, reconciled alert catalog across spec and docs")**. The intended target is §12.4 Redis HA and Failure Modes, whose "Quota/Rate Limiting Redis instance" subsection (starting around line 247 "Tier 3 Redis write throughput quantification") describes the quota Redis instance that backs the per-user fail-open fraction metric. The canonical anchor is therefore `#124-redis-ha-and-failure-modes`. The visible label "Section 12.4" is already correct — §12.4 IS the target section — so this is an anchor-fragment-only fix (cf. iter6 DOC-025 retarget pattern where only the fragment needed correction).

**Recommendation:** Replace the fragment in `spec/16_observability.md:203`:

```
[Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)
```

Alternative: promote the "Tier 3 Redis write throughput quantification" / "Redis Cluster migration pre-plan" block at `spec/12_storage-architecture.md:247–268` to a named `#### Quota and Rate Limiting` subsection. This yields `#quota-and-rate-limiting` (not `#124-quota-and-rate-limiting`, which was the fabricated form). The fragment-only fix is mechanically simpler and matches the DOC-025 precedent.

Same class of defect as iter4 DOC-013/014/015/016, iter5 DOC-019/020/021, and iter6 DOC-024/025 — a fabricated anchor fragment whose GitHub-preview behavior is to silently scroll to the top of the target file. Root cause is iter6's self-verification claim (the OBS-037 fix scanned observability/policy alert coverage but did not cross-check the new `[Section 12.4](#...)` fragment against `spec/12_storage-architecture.md` headings). Identical process gap iter5 DOC-021 and iter6 DOC-024/025 called out.

### DOC-032. Intra-file anchor `#141-extensibility-rules` does not exist — introduced by iter6 CNT-020 fix [Medium]

**File:** `spec/14_workspace-plan-schema.md:104`

The reference is inside the new "Schema encoding of the request/response asymmetry" paragraph added by iter6's CNT-020 fix. The prose reads: `Because the per-variant object schema sets \`additionalProperties: false\` (see "Per-variant field strictness" in [§14.1](#141-extensibility-rules))...`. The fragment `#141-extensibility-rules` does **not** resolve in `14_workspace-plan-schema.md`: §14.1 at line 306 is titled "WorkspacePlan Schema Versioning" (canonical anchor `#141-workspaceplan-schema-versioning`). There is no subsection titled "Extensibility Rules" at any level — "Per-variant field strictness" at `14_workspace-plan-schema.md:336` is a bolded prose label inside §14.1, not a heading (`grep -nE '^#+' spec/14_workspace-plan-schema.md` returns only `## 14. Workspace Plan Schema` and `### 14.1 WorkspacePlan Schema Versioning`).

The reference was **introduced by the iter6 fix commit (`8604ce9`, CNT-020 — "documented resolvedCommitSha dual-schema pattern")**. The fabricated anchor is particularly unfortunate because the prose uses a quoted landmark ("Per-variant field strictness") that a reader cannot navigate to via the hyperlink — the anchor scrolls to the top of the file on GitHub rather than to the intended prose-label paragraph at line 336.

Same class of defect as DOC-031 above and iter6 DOC-024/025 — a fabricated intra-file anchor. Self-verification gap: CNT-020 was a multi-paragraph insertion whose internal cross-reference was not tested against the file's heading set before commit.

**Recommendation:** Two equivalent options:

- **(a) Retarget to the existing §14.1 anchor.** Because §14.1 IS the subsection that contains "Per-variant field strictness" (at line 336), the fix is to retarget the fragment: `[§14.1](#141-workspaceplan-schema-versioning)`. The reader then lands at the top of §14.1 and scrolls down to the prose label; the visible label `§14.1` is already correct.

- **(b) Promote the landmark to a subsection.** Replace the bolded prose label `**Per-variant field strictness.**` at `spec/14_workspace-plan-schema.md:336` with a `#### Per-variant field strictness` heading. This yields the anchor `#per-variant-field-strictness`. Update the `spec/14_workspace-plan-schema.md:104` reference to `[§14.1 "Per-variant field strictness"](#per-variant-field-strictness)`. This is the reader-experience-preferred form — the hyperlink lands precisely where the prose claims — and matches the resolution recommended by iter6 DOC-028 for DOC-025 (promote a landmark to a heading if a prose link needs precise landing).

Either approach resolves DOC-032; (b) is preferred because it also addresses the more general documentation problem that landmark prose labels inside long subsections are invisible to readers who navigate via TOC.

### DOC-033. Re-file of iter5/iter6 DOC-022 → DOC-029 — "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" headings remain [Low]

**Files:** `spec/16_observability.md:648, 677`; `spec/README.md:105–106`

Unfixed for **six iterations** (iter1 DOC-002 → iter2 DOC-006 → iter3 DOC-011 → iter4 DOC-017 → iter5 DOC-022 → iter6 DOC-029 → iter7). Headings verbatim:

```
### 16.7 Section 25 Audit Events
### 16.8 Section 25 Metrics
```

The juxtaposition `16.7 Section 25 ...` reads as a structural inconsistency (two section numbers in one heading). Every prior iteration (iter1–iter6) proposed the identical fix; iter5 additionally proposed a convergence-termination rule ("any Low-severity finding surviving three consecutive iterations with unchanged recommendation MUST receive either (a) a fix commit or (b) an explicit 'accepted — will not fix' resolution note in the next iteration's fix pass"). Neither outcome has occurred across six iterations; iter7 therefore re-files at unchanged severity.

**Calibration note (per `feedback_severity_calibration_iter5.md`):** this stays **Low** and does not drift upward despite six iterations of recurrence — severity is a function of the defect class (heading-clarity / navigation friction with no normative ambiguity), not of its age.

**Recommendation (unchanged from iter2 through iter6):** Rename to `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics` in `spec/16_observability.md:648, 677`. Open each subsection body with a single-sentence cross-reference such as "Introduced by §25 (Agent Operability)." Update the two mirrored lines in `spec/README.md:105–106` to the new titles, and update the anchors (`#167-section-25-audit-events` → `#167-agent-operability-audit-events`, `#168-section-25-metrics` → `#168-agent-operability-metrics`); grep the tree for any callers and rewrite them in the same commit. If the platform policy is to keep "Section 25" in the heading verbatim, **record that decision explicitly in this finding's resolution note so the re-file cycle stops**.

The re-file cycle is now self-sustaining by inattention. This iter7 report formally requests that iter7's fix pass apply either outcome.

### DOC-034. Re-file of iter5/iter6 DOC-023 → DOC-030 — README TOC still omits three numbered subsections [Low]

**File:** `spec/README.md`

Unfixed for **six iterations** (iter2 DOC-007 → iter3 DOC-012 → iter4 DOC-018 → iter5 DOC-023 → iter6 DOC-030 → iter7). The iter7 programmatic verification against every `X.Y` heading in `spec/` confirms exactly three omissions, unchanged since iter5:

- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README lines 15–23 list 4.1 through 4.9 but not 4.0.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — README line 119 lists §18 as a parent with zero child entries (inconsistent with every other multi-subsection chapter in the TOC).
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README lines 128–148 list 24.1 through 24.20 but not 24.0.

All three target anchors resolve correctly and are cited in running prose throughout the spec (e.g., `17_deployment-topology.md:343` successfully references `#240-packaging-and-installation`; other files reference `#40-agent-operability-additions` and `#181-build-artifacts-introduced-by-section-25`). The defect is purely TOC-level — readers scanning the table of contents do not learn these subsections exist, and any reader who lands on §4, §18, or §24 via the TOC misses the introductory overview content.

**Recommendation (unchanged from iter5/iter6):** Insert three TOC lines in `spec/README.md` using the exact indentation the README already applies to level-3 entries:

```
  - [4.0 Agent Operability Additions](04_system-components.md#40-agent-operability-additions)
  - [18.1 Build Artifacts Introduced by Section 25](18_build-sequence.md#181-build-artifacts-introduced-by-section-25)
  - [24.0 Packaging and Installation](24_lenny-ctl-command-reference.md#240-packaging-and-installation)
```

Insert the §4.0 line between the current `- [4. System Components]` parent (line 14) and `- [4.1 Edge Gateway Replicas]` (line 15); insert the §18.1 line as the first (and currently only) child of `- [18. Build Sequence]` (line 119); insert the §24.0 line between the current `- [24. \`lenny-ctl\` Command Reference]` parent (line 127) and `- [24.1 Bootstrap]` (line 128). If the README convention is specifically that `X.0` overviews and single-subsection chapters are intentionally omitted, **record that convention in this finding's resolution note so the re-file cycle terminates**; otherwise the fix is three one-line inserts.

### DOC-035. `docs/api/internal.md#lifecycle-channel-messages` is a kramdown-only anchor — fails on GitHub's markdown preview (reclassified DOC-026) [Info]

**File:** `docs/api/internal.md:224` (link); `docs/api/internal.md:312–313` (target)

The cross-reference at line 224 reads: `... the adapter first sends a \`checkpoint_request\` on the lifecycle channel, waits for \`checkpoint_ready\` from the runtime, performs the snapshot, then sends \`checkpoint_complete\`. See [Lifecycle channel messages](#lifecycle-channel-messages) below.`. The target at line 312–313 is:

```
## Lifecycle channel messages (Full integration level only)
{: #lifecycle-channel-messages }
```

The `{: #lifecycle-channel-messages }` syntax is a kramdown `attr_list` explicit-ID directive. The docs site uses Jekyll with kramdown (`docs/_config.yml:101–103`), which honors `attr_list` and produces an HTML anchor named `lifecycle-channel-messages` on the target heading. **On the published docs site the link works correctly.**

However, when a reader views `docs/api/internal.md` directly on GitHub's raw-markdown blob page (`github.com/lennylabs/lenny/blob/main/docs/api/internal.md`), GitHub's markdown renderer does NOT process kramdown `attr_list` directives. The heading on GitHub auto-slugs to `lifecycle-channel-messages-full-integration-level-only` (the literal heading text), and the explicit-ID directive renders as visible literal text (`{: #lifecycle-channel-messages }`). The `#lifecycle-channel-messages` link on GitHub therefore scrolls to the top of the page rather than to the target heading.

This is a **narrow GitHub-preview-only degradation** — all doc readers on the published site reach the anchor correctly; only GitHub blob-view readers see the misdirection. Per the iter1–iter6 severity rubric, pure GitHub-preview-only presentation issues (when the target rendering is the docs site, not GitHub) are **Info** severity, not Low — the docs site is the authoritative rendering target and the link works there. Iter6 DOC-026 classified this at Low; **iter7 reclassifies to Info** after discovering the kramdown `attr_list` mechanism, which iter6's sweep did not account for.

**Recommendation (optional, not required to fix):** If the project wishes to keep GitHub blob-view navigation intact for this one file, pick one of:

- **(a) Match heading slug to link.** Rename the heading to just `## Lifecycle channel messages` (drop the "(Full integration level only)" parenthetical) and delete the `{: #lifecycle-channel-messages }` attribute line. GitHub's auto-slug will then produce `lifecycle-channel-messages` matching the existing link. The parenthetical "(Full integration level only)" can move into the first sentence of the section body.

- **(b) Update the link to match the auto-slug.** Change line 224's link fragment to `#lifecycle-channel-messages-full-integration-level-only`. This works on both kramdown and GitHub rendering (both produce the same slug for the heading when `attr_list` is absent or ignored). Remove the `{: #lifecycle-channel-messages }` line at 313.

- **(c) Delete the cross-reference.** The prose at line 224 ("... the adapter first sends a `checkpoint_request` ...") is self-explanatory; the `See [Lifecycle channel messages](#lifecycle-channel-messages) below` phrase adds no information a reader skimming the next 100 lines of the document does not already encounter. Minimal-impact fix.

None of (a)/(b)/(c) is required for v1 if the project's rendering contract is "docs site only." Flagged for awareness.

### DOC-036. Minor section-label/anchor-prefix coherence issues — non-critical but surfaced by iter7 programmatic sweep [Info]

**Files (4 occurrences):**

- `spec/04_system-components.md:1140` — `[§21.9](21_planned-post-v1.md#21-planned--post-v1)`
- `spec/14_workspace-plan-schema.md:93` — `[§21.9](21_planned-post-v1.md#21-planned--post-v1)`
- `spec/15_external-api-surface.md:559, 1342` — `[§21.1](21_planned-post-v1.md#21-planned--post-v1)` (×2)
- `spec/21_planned-post-v1.md:16, 23` — `[Section 21.1](#21-planned--post-v1)` (×2)
- `spec/26_reference-runtime-catalog.md:119` — `[§21.9](21_planned-post-v1.md#21-planned--post-v1)`
- `spec/08_recursive-delegation.md:798` — `[§15](15_external-api-surface.md#151-rest-api)` (should be `[§15.1]`)
- `spec/17_deployment-topology.md:343` — `[§24](24_lenny-ctl-command-reference.md#240-packaging-and-installation)` (should be `[§24.0]`)

**Analysis.** Section 21 ("Planned / Post-V1") has no numbered subsections — its content is organized by bolded prose labels (`**21.1 A2A Full Support.**`, `**21.9 SSH URL support**`, etc.). The label convention `§21.1` / `§21.9` in prose is an informal pointer to the bolded landmark, and the hyperlink anchor correctly points to the top of §21 where the reader can scan for the landmark. This is a deliberate convention and does not misdirect — the reader lands at `#21-planned--post-v1` (the §21 heading) and then scrolls to find the bolded landmark. Classifying as **Info** per the iter1–iter6 rubric for pure label/anchor-prefix coherence issues (no normative ambiguity, hyperlink resolves to the correct parent section).

Two genuine minor mismatches within the set: `[§15] → #151-rest-api` at `spec/08_recursive-delegation.md:798` and `[§24] → #240-packaging-and-installation` at `spec/17_deployment-topology.md:343`. Both labels should be one level more specific (`§15.1` and `§24.0`) to match where the hyperlink actually lands. These are the same failure class as iter6 DOC-027 (§15.4 label / §15.1 anchor) but at lower frequency and without the compounded cross-file broken-fragment issue — **Info** severity.

**Recommendation (optional, not required for convergence):**

1. For the §21-landmark references: either (a) accept the convention and document it with a one-line footnote inside §21 (e.g., "References of the form `§21.X` in other sections point to the bolded `X`-numbered landmark paragraphs within this section"), or (b) promote each `**21.X ...**` prose label to a `#### 21.X ...` heading so the references resolve to precise landing points. Option (a) is zero-work; option (b) is a one-line-per-landmark refactor that improves navigation materially.

2. For the two genuine label mismatches, update the visible label:
   - `spec/08_recursive-delegation.md:798` — `[§15]` → `[§15.1]`
   - `spec/17_deployment-topology.md:343` — `[§24]` → `[§24.0]`

Pure navigation polish; no change required for v1 if convergence is the priority.

---

## Carry-forward summary (iter1 → iter7)

| Iter-1 ID | Iter-2 ID | Iter-3 ID | Iter-4 ID | Iter-5 ID | Iter-6 ID | Iter-7 ID | Topic                                                                    | Severity | Status          |
| --------- | --------- | --------- | --------- | --------- | --------- | --------- | ------------------------------------------------------------------------ | -------- | --------------- |
| DOC-001   | DOC-005   | DOC-008   | DOC-013   | —         | —         | —         | `#1781-operational-defaults--quick-reference` intra                      | Medium   | Fixed iter4    |
| DOC-002   | DOC-006   | DOC-011   | DOC-017   | DOC-022   | DOC-029   | DOC-033   | "16.7 Section 25 …" / "16.8 Section 25 …" headings                       | Low      | Open (6 iters) |
| —         | DOC-007   | DOC-012   | DOC-018   | DOC-023   | DOC-030   | DOC-034   | README TOC omissions (4.0 / 18.1 / 24.0)                                 | Low      | Open (6 iters) |
| —         | —         | —         | DOC-014   | —         | —         | —         | `#253-endpoint-split-between-gateway-and-lenny-ops`                      | Medium   | Fixed iter5    |
| —         | —         | —         | DOC-015   | —         | —         | —         | `#251-overview` stale anchor                                             | Medium   | Fixed iter5    |
| —         | —         | —         | DOC-016   | —         | —         | —         | `#165-alerts` stale anchor                                               | Medium   | Fixed iter5    |
| —         | —         | —         | —         | DOC-019   | —         | —         | `#179-preflight-checks` fabricated anchor                                | Medium   | Fixed iter6    |
| —         | —         | —         | —         | DOC-020   | —         | —         | `#152-mcp-endpoints` fabricated anchor                                   | Medium   | Fixed iter6    |
| —         | —         | —         | —         | DOC-021   | —         | —         | `#154-error-codes` fabricated anchor                                     | Medium   | Fixed iter6    |
| —         | —         | —         | —         | —         | DOC-024   | —         | `#154-errors-and-degradation` fabricated (9 sites)                       | Medium   | Fixed iter7    |
| —         | —         | —         | —         | —         | DOC-025   | —         | `#1781-helm-values` fabricated (3 sites)                                 | Medium   | Fixed iter7    |
| —         | —         | —         | —         | —         | DOC-026   | DOC-035   | `docs/api/internal.md#lifecycle-channel-messages` (kramdown-only)       | ~~Low~~ → Info | Reclassified Info (works on docs site via `attr_list`) |
| —         | —         | —         | —         | —         | DOC-027   | —         | §15.4 label / §15.1 target mismatch (co-occurs DOC-024)                  | Low      | Fixed iter7    |
| —         | —         | —         | —         | —         | DOC-028   | —         | §17.8.1 visible-title vs. prose mismatch (co-occurs DOC-025)             | Info     | Moot iter7 (iter6 chose different retarget) |
| —         | —         | —         | —         | —         | —         | DOC-031   | `#124-quota-and-rate-limiting` fabricated (1 site) — iter6 OBS-037       | Medium   | Open (NEW)     |
| —         | —         | —         | —         | —         | —         | DOC-032   | `#141-extensibility-rules` fabricated (1 site) — iter6 CNT-020           | Medium   | Open (NEW)     |
| —         | —         | —         | —         | —         | —         | DOC-036   | §21/§15/§24 label/anchor-prefix coherence (8 sites, mostly §21 convention) | Info     | Open (NEW)     |

**Fixed this iteration:** DOC-024 / DOC-025 (two Medium anchor regressions from iter6 — 12 total occurrences across 9 sites + 3 sites — all closed; zero surviving references to either broken fragment). DOC-027 (Low — closed as part of DOC-024 fix; visible labels `§15.4` → `§15.1` updated in the same commit). DOC-028 (Info — moot; iter6 chose `#176-packaging-and-installation` instead of `#1781-operational-defaults--quick-reference`, which topically matches the prose).

**Reclassified:** DOC-026 → DOC-035 (Low → Info; works on docs site via kramdown `attr_list`, fails only on GitHub blob view).

**Re-filed (unchanged):** DOC-029 → DOC-033, DOC-030 → DOC-034 (both Low-severity, both at six iterations of recurrence, both with identical mechanical fixes and no decision or fix applied in iter6).

**New this iteration:** DOC-031 (Medium, introduced by iter6 OBS-037 fix). DOC-032 (Medium, introduced by iter6 CNT-020 fix). DOC-036 (Info, surfaced by iter7's label/anchor-prefix coherence sweep — lowest-severity navigation polish, mostly documented convention for §21).

---

## Convergence assessment

**Cross-reference integrity (Medium-severity class).** Iter6's two anchor-integrity defects (DOC-024 / DOC-025) are both verified fixed in-place, and all 12 broken link fragments are gone. However, **iter6's fix commit `8604ce9` itself introduced 2 new broken anchor occurrences across two fabricated fragments** (`#124-quota-and-rate-limiting` × 1 from OBS-037, `#141-extensibility-rules` × 1 from CNT-020). Every iteration in this perspective's history has observed the same failure mode: a non-DOC fix commit in iter N closes 2–12 anchor breakages flagged in iter N-1 while introducing 1–12 new ones in the same patch. The rate has dropped (12 new → 2 new from iter5→iter6 → iter6→iter7), but the pattern is still present.

| Iteration | Fixed anchor refs | Newly introduced |
| --------- | ----------------- | ---------------- |
| iter3 → iter4 | 1 (DOC-008)    | 4 (DOC-013/014/015/016) |
| iter4 → iter5 | 4 (DOC-013–016) | 3 (DOC-019/020/021)     |
| iter5 → iter6 | 3 (DOC-019–021) | 12 (DOC-024 ×9, DOC-025 ×3) |
| iter6 → iter7 | 12 (DOC-024, DOC-025) | 2 (DOC-031, DOC-032)    |

The new-introduction rate is finally trending toward zero. **Iter6 closed 6× more broken references than it introduced** (12 fixed / 2 new — the first iteration where the close:introduce ratio is strongly positive). This suggests the iter5 recommendation — "a 20-line Python script reading every `](file.md#anchor)` and matching it against the heading-derived slug set would have blocked all these at PR time" — was partially absorbed by the iter6 fix-review process (possibly by the implementer re-running the sweep before commit) but not yet converted to a CI gate.

**Recommendation for iter7 fix pass:** Adopt the Python anchor-integrity check script embedded in the iter6 convergence section as a pre-commit hook or a CI step. The script is <30 lines (verified in this iter7 sweep to catch all 2 surviving broken references plus the 12 iter6-fixed references and all historical DOC-013/014/015/016/019/020/021 patterns). Running it on `spec/` in under 1 second wall-clock would have blocked both DOC-031 and DOC-032 at iter6 commit time. Each fix iteration should be expected to introduce 0–3 new anchor regressions until the gate is in CI; with the gate, the expected rate is exactly 0.

**Navigation/heading clarity (Low-severity class).** DOC-033 (§16.7/§16.8 "Section 25 Audit Events/Metrics") and DOC-034 (README TOC omissions of §4.0 / §18.1 / §24.0) have now persisted **six iterations** each with unchanged recommendations and trivial fix costs (a three-line README patch for DOC-034, a two-heading rename plus README mirror update for DOC-033). Iter5 proposed a convergence-termination rule — any Low finding at three consecutive iterations with unchanged recommendation receives either a fix or an explicit "accepted — will not fix" note in the next iteration's fix pass — and the rule was proposed at iter5 and re-affirmed at iter6; it has been applied at neither iter5→iter6 nor iter6→iter7. The re-file cycle is now entirely self-sustaining by inattention at six iterations.

**Explicit formal deferral recommendation.** This iter7 report formally proposes that iter7's fix pass apply one of two resolutions to DOC-033 and DOC-034:

- **Resolution A — Apply the fix.** Both are mechanically trivial (three README line inserts + two heading renames + two README mirror updates = seven line edits total in two files). Estimated 5-minute fix. Removes the re-file cycle.

- **Resolution B — Formal "Accepted — Will Not Fix" note.** Add a single paragraph to each finding in the iter7 fix-pass record (or to `spec/README.md` as an editorial note) recording the decision:
  - DOC-033: "The §16.7/§16.8 heading form `Section 25 Audit Events` / `Section 25 Metrics` is accepted as-is; the cross-reference to §25 in the heading number is intentional and the platform's editorial convention is to retain it."
  - DOC-034: "The README TOC intentionally omits §X.0 overviews and single-subsection chapters (§4.0, §18.1, §24.0); readers entering those sections via the parent TOC entry are expected to encounter the §X.0 content directly."

Either resolution terminates the re-file cycle. **If neither is applied at iter7→iter8, this perspective's iter8 report will flag DOC-033 and DOC-034 at Info severity with a one-line "formally deferred by process" note rather than re-filing the full recommendation**, per the iter5 convergence-termination rule that has not been applied.

**GitHub-preview-only defects (Info).** DOC-035 (kramdown `attr_list` anchor) is a narrow degradation affecting only readers viewing `docs/api/internal.md` on GitHub's blob-view page. Not a convergence blocker; optional fix per the three alternatives in DOC-035's recommendation block.

**Section-label/anchor-prefix coherence (Info).** DOC-036 surfaces an §21-numbering convention where the spec uses informal bolded landmarks (`**21.1 ...**`, `**21.9 ...**`) that cannot be hyperlinked to precise sub-section anchors. The convention is deliberate and the hyperlinks resolve to the §21 parent heading; readers do not misdirect. Two genuine label mismatches (`§15` → §15.1 anchor; `§24` → §24.0 anchor) are trivial single-character edits. Not a convergence blocker; optional fix per the DOC-036 recommendation block.

**Overall iter7 state for Perspective 22.** **6 findings: 2 Medium + 2 Low + 2 Info.** Medium findings (DOC-031 / DOC-032) are mechanically trivial to fix (one fragment correction each + one optional heading promotion for DOC-032) but their persistence as a new-iteration pattern is the strongest signal in this perspective. The close:introduce ratio improved 6× between iter5→iter6 and iter6→iter7 (0.25 → 6.0), suggesting the implementer is absorbing the feedback but the CI gate is not yet in place. The two Low findings (DOC-033 / DOC-034) are six-iteration carry-forwards awaiting either a fix or a formal deferral note. The two Info findings (DOC-035 / DOC-036) are pure polish items.

**No findings rose to High severity** because none of the broken anchors produced normative ambiguity in running text: DOC-031's prose unambiguously names §12.4 as the target (Redis section), DOC-032's prose names the "Per-variant field strictness" landmark inside §14.1, and a reader losing either hyperlink still resolves the correct concept via in-page search. The risk remains silent misdirection to the top of the file on GitHub preview, not misimplementation of a normative requirement — identical to every prior iteration's assessment and consistent with the iter1–iter6 severity anchoring the perspective has applied throughout.

**Convergence on this perspective: No, but close.** Two Medium regressions introduced this iteration (DOC-031 / DOC-032) and two Low findings at six iterations of recurrence without either a fix or a formal defer (DOC-033 / DOC-034). Convergence requires (a) the Python anchor-integrity gate recommended since iter5 to break the remaining regression pattern (12→2 is strong progress but not zero), and (b) application of the iter5-proposed three-iteration rule to the two Low carry-forwards (iter7 fix pass MUST apply one of Resolution A or Resolution B above). Estimated convergence cost if both are addressed: ~30 minutes (add a pre-commit hook + three README edits + two heading renames + two fragment-only fixes).

---

## Appendix — Iter7 anchor-integrity sweep script

This is the exact script used for the iter7 programmatic sweep. It catches every historical DOC anchor finding (DOC-013 through DOC-032) and produces 2 hits against the current iter7 spec tree (DOC-031, DOC-032). Recommended as a CI pre-commit gate.

```python
import re, glob, os
from collections import defaultdict

def slug(title):
    """GitHub-compliant slugger (github-slugger semantics)."""
    h = title.strip()
    h = re.sub(r'#+$', '', h).strip()
    h = h.lower()
    h = re.sub(r'[^a-z0-9_\s\-]', '', h)   # strip non-allowlist chars including em-dash / period / slash / ampersand
    h = re.sub(r' ', '-', h)                # replace EACH whitespace with hyphen (preserves consecutive hyphens from dropped punctuation)
    h = h.strip('-')
    return h

anchors = defaultdict(set)
for f in sorted(glob.glob('spec/*.md')):
    seen = {}
    in_code = False
    fname = os.path.basename(f)
    with open(f) as fh:
        for line in fh:
            if line.lstrip().startswith('```'):
                in_code = not in_code; continue
            if in_code: continue
            m = re.match(r'^(#+)\s+(.+?)\s*$', line)
            if m:
                s = slug(m.group(2))
                c = seen.get(s, -1) + 1; seen[s] = c
                anchors[fname].add(s if c == 0 else f'{s}-{c}')

cross = re.compile(r'\]\(([\w\-]+\.md)#([^\)]+)\)')
intra = re.compile(r'\]\((#[^\)]+)\)')
bad = []
for f in sorted(glob.glob('spec/*.md')):
    fname = os.path.basename(f)
    in_code = False
    with open(f) as fh:
        for i, line in enumerate(fh, 1):
            if line.lstrip().startswith('```'):
                in_code = not in_code; continue
            if in_code: continue
            for m in cross.finditer(line):
                if m.group(1) in anchors and m.group(2) not in anchors[m.group(1)]:
                    bad.append((fname, i, m.group(1), m.group(2), 'cross'))
            for m in intra.finditer(line):
                a = m.group(1)[1:]
                if a not in anchors[fname]:
                    bad.append((fname, i, fname, a, 'intra'))

assert not bad, '\n'.join(f'{r[0]}:{r[1]} -> {r[2]}#{r[3]} ({r[4]})' for r in bad)
```

Running this on the current `spec/` tree produces exactly two output lines corresponding to DOC-031 and DOC-032. Integrating it as a pre-commit hook or a CI step in the review-fix loop would block all future iterations of the DOC-013/014/015/016/019/020/021/024/025/031/032 pattern at PR time.
