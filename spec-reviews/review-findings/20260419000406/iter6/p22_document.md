# Iter6 Review — Perspective 22: Document Quality, Consistency & Completeness

**Scope.** Cross-reference integrity (intra-file and cross-file anchors), README TOC completeness, heading/title clarity, terminology consistency, typos. Per `feedback_severity_calibration_iter5.md`, severities are anchored to the iter1–iter5 rubric for this class of defect: intra-/cross-anchor resolution failures remain **Medium** (silent misdirection, invisible on GitHub preview), heading-clarity and README-TOC issues remain **Low** (navigation friction, no ambiguity in normative text).

**Method.** (1) Verified iter5 "Fixed" items (DOC-019 / DOC-020 / DOC-021) against the current spec. (2) Re-verified iter5 carry-forwards (DOC-022 / DOC-023). (3) Programmatic anchor sweep: parsed every heading in `spec/*.md` with a GitHub-compliant slugger (github-slugger semantics — lowercase, strip punctuation except word chars/spaces/hyphens/underscores, spaces to hyphens preserving consecutive hyphens from multi-space input), producing the authoritative anchor set. Matched every `](file.md#anchor)` and `](#anchor)` target in `spec/` against that set: 2,384 cross-file and 422 intra-file targets scanned. (4) Also matched all `docs/**/*.md` links into `spec/` (14 targets, 0 broken) and all intra-`docs/` anchor links (57 targets, 1 pre-existing broken link unrelated to iter5). (5) Programmatic README TOC parity check against every `^\d+\.\d+` heading in `spec/`. (6) `git diff 5c8c86a..c941492 -- spec/` to isolate exactly what iter5's fix commit added/removed, so new regressions are attributable to iter5 vs. earlier commits.

**Numbering.** DOC-024 onward (after iter5's DOC-023).

---

## Iter5 status re-verification

- **DOC-019** — `17_deployment-topology.md#179-preflight-checks`. **Fixed in iter5.** Zero occurrences remain. The single call site at `13_security-model.md:211` now uses `(17_deployment-topology.md#checks-performed)`, which resolves to `#### Checks performed` at `17_deployment-topology.md:465`.
- **DOC-020** — `15_external-api-surface.md#152-mcp-endpoints`. **Fixed in iter5.** Zero occurrences remain. `25_agent-operability.md:3663` now references `(15_external-api-surface.md#152-mcp-api)` — the canonical §15.2 anchor.
- **DOC-021** — `15_external-api-surface.md#154-error-codes`. **Fixed in iter5.** Zero occurrences remain. `25_agent-operability.md:4458` retargeted to `(15_external-api-surface.md#151-rest-api)` (REST error catalog location) and the alert reference to `(16_observability.md#165-alerting-rules-and-slos)`.
- **DOC-022** — Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics". **Still unfixed** (now fifth iteration). Both headings remain literally at `16_observability.md:644, 673` and the README TOC mirrors them verbatim at `spec/README.md:105–106`. Re-filed as DOC-029 below at iter5 severity (Low).
- **DOC-023** — README TOC omissions of `4.0`, `18.1`, `24.0`. **Still unfixed** (now fifth iteration). Programmatic re-verification against the current `spec/` tree confirms the same three X.Y omissions; no other X.Y-level omission exists. Re-filed as DOC-030 below at iter5 severity (Low).

---

## 22. Documentation & Cross-Reference Integrity

### DOC-024. Cross-file anchor `15_external-api-surface.md#154-errors-and-degradation` does not exist (9 occurrences) [Medium]

**Status: Fixed** — Global find-and-replace executed across `spec/11_policy-and-controls.md`, `spec/12_storage-architecture.md`, `spec/14_workspace-plan-schema.md`, `spec/15_external-api-surface.md:816` (intra-file self-reference), and `spec/16_observability.md`. Link fragments changed from `15_external-api-surface.md#154-errors-and-degradation` to `#151-rest-api`; citation labels updated from `§15.4` to `§15.1`.

**Files:**
- `spec/11_policy-and-controls.md:423, 443, 445` (CMP-058 platform-tenant audit residency prose + CMP-057 compliance-profile downgrade ratchet)
- `spec/12_storage-architecture.md:885` (Phase 3.5 legal-hold escrow Step 4 residency gate)
- `spec/15_external-api-surface.md:816` (intra-file form `(#154-errors-and-degradation)` inside the PUT `/v1/admin/tenants/{id}` admin-API row)
- `spec/16_observability.md:243, 244, 422, 423` (`lenny_legal_hold_escrow_region_unresolvable_total` and `lenny_platform_audit_region_unresolvable_total` metric definitions + `LegalHoldEscrowResidencyViolation` and `PlatformAuditResidencyViolation` alert definitions)

All nine call sites were **introduced by the iter5 fix commit (`c941492`, CMP-054 / CMP-057 / CMP-058)**. The target fragment `#154-errors-and-degradation` does not resolve in `15_external-api-surface.md`: §15.4 at line 1402 is "Runtime Adapter Specification" (canonical anchor `#154-runtime-adapter-specification`) and there is no heading named "Errors and Degradation" at any level in that file (`grep -E '^#+\s+.*[Ee]rrors? and [Dd]egradation' spec/**/*.md` returns zero matches). The intended target in every case is the REST error-code table under §15.1, where `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`, `PLATFORM_AUDIT_REGION_UNRESOLVABLE`, `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`, and `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` are all defined inline in the error table at lines 988–1070. The canonical anchor is therefore `#151-rest-api`. The citation text `§15.4` is also wrong — §15.4 is "Runtime Adapter Specification" and is unrelated to REST/admin error codes; the prose actually wanted to say "§15.1 REST API error catalog". This is the same class of defect iter1 DOC-001 / iter2 DOC-005 / iter3 DOC-008 / iter4 DOC-013–016 / iter5 DOC-019–021 flagged — a self-invented anchor fragment that silently misdirects the GitHub preview reader to the top of the target file. The pattern is identical to iter5's assessment: "**self-verification claims that were not actually executed are the root cause**".

**Recommendation:** Global find-and-replace in `spec/`:
- Link fragment: `15_external-api-surface.md#154-errors-and-degradation` → `15_external-api-surface.md#151-rest-api` (eight cross-file occurrences)
- Intra-file anchor at `15_external-api-surface.md:816`: `[§15.4](#154-errors-and-degradation)` → `[§15.1](#151-rest-api)`
- Section label in the surrounding prose: `§15.4` → `§15.1` where the `§15.4` citation pointed at this fragment (nine occurrences). The label change is load-bearing — readers who follow the link land on §15.1 and will be confused if the citation still says "§15.4".

Alternative, if the reviewer wants a dedicated stable anchor for the error catalog: promote the inline error-code table at `15_external-api-surface.md:988` to a named subsection `#### 15.1.1 Error Code Catalog`. This yields `#1511-error-code-catalog` and future references become independent of §15.1's surrounding prose. Either approach resolves DOC-024; the global find-and-replace is mechanically simpler and matches the iter5 pattern for DOC-020 / DOC-021.

### DOC-025. Cross-file anchor `17_deployment-topology.md#1781-helm-values` does not exist (3 occurrences) [Medium]

**Status: Fixed** — Replaced the fabricated anchor `#1781-helm-values` with `#176-packaging-and-installation` (the canonical §17.6 Packaging and Installation anchor, where Helm values including `storage.regions.<region>.*` entries are documented) across `spec/11_policy-and-controls.md:421`, `spec/15_external-api-surface.md:1041`, and `spec/16_observability.md:425`. Citation labels updated from `§17.8.1` to `§17.6` accordingly.

**Files:**
- `spec/11_policy-and-controls.md:421` (CMP-058 platform-tenant audit residency, rule 1, "one logical platform-Postgres per region is the deployment topology ([§17.8.1](17_deployment-topology.md#1781-helm-values))")
- `spec/15_external-api-surface.md:1036` (`LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` error-code row, "See [Section 17.8.1](17_deployment-topology.md#1781-helm-values) per-region escrow defaults")
- `spec/16_observability.md:422` (`LegalHoldEscrowResidencyViolation` alert, "See [Section 17.8.1](17_deployment-topology.md#1781-helm-values)")

All three call sites were **introduced by the iter5 fix commit (`c941492`, CMP-054 / CMP-058)**. The target fragment `#1781-helm-values` does not resolve in `17_deployment-topology.md`: §17.8.1 at line 837 is "Operational Defaults — Quick Reference" (canonical anchor `#1781-operational-defaults--quick-reference`). There is no heading titled "Helm Values" at any level in `17_deployment-topology.md` (`grep -E '^#+\s+.*Helm\s+[Vv]alues\s*$' spec/17_deployment-topology.md` returns zero matches). The intended target in each case is the per-region residency configuration defaults at §17.8.1 (the "Operational Defaults — Quick Reference" table) which does list `storage.regions.<region>.*` and related Helm-configurable values. Same class of defect as DOC-024 — a fabricated anchor fragment whose GitHub-preview behavior is to silently scroll to the top of the target file. Same root cause as DOC-024 (iter5 NET-067-style self-verification gap).

**Recommendation:** Global find-and-replace in the three call sites:
- `17_deployment-topology.md#1781-helm-values` → `17_deployment-topology.md#1781-operational-defaults--quick-reference`

The section label `§17.8.1` in the surrounding prose is already correct (§17.8.1 IS the target); only the anchor fragment needs correction. Verify the retargeted anchor is stable (it is — confirmed present via `grep -n '^### 17\.8\.1 ' spec/17_deployment-topology.md` → line 837).

### DOC-026. `docs/api/internal.md` carries a pre-existing broken intra-file anchor to `#lifecycle-channel-messages` [Low]

**File:** `docs/api/internal.md:224`

The prose reads `... the adapter first sends a `checkpoint_request` on the lifecycle channel, waits for `checkpoint_ready` from the runtime, performs the snapshot, then sends `checkpoint_complete`. See [Lifecycle channel messages](#lifecycle-channel-messages) below.` The fragment `#lifecycle-channel-messages` does not resolve to any heading in `docs/api/internal.md`. This is **not introduced by iter5** (the file has not been touched in iter5 commits per `git log -- docs/api/internal.md`); the broken link has been present since commit `3875674` (comprehensive-documentation introduction). It is flagged here because the iter6 comprehensive anchor sweep surfaces it and the user-facing docs sync feedback (`feedback_docs_sync_after_spec_changes.md`) makes the integrity of `docs/` a tracked concern for this perspective. Severity is **Low** (single broken link in a single doc, the prose around it is self-explanatory, the reader can still follow the procedure without the anchor).

**Recommendation:** Either (a) rename an existing subsection in `docs/api/internal.md` to produce the slug `lifecycle-channel-messages` (e.g., add a `#### Lifecycle Channel Messages` heading above the relevant block), or (b) delete the "`[Lifecycle channel messages](#lifecycle-channel-messages) below`" phrase, since the prose already describes the sequence inline and the cross-reference adds no information the reader does not already have. Option (a) is preferred if there is content downstream in the same file describing those messages as a named section; option (b) is the minimal fix.

### DOC-027. Inline label/target mismatch — link goes to §15.1 but label says §15.4 (co-occurs with DOC-024) [Low]

**Files:**
- `spec/11_policy-and-controls.md:423, 443, 445`
- `spec/12_storage-architecture.md:885`
- `spec/16_observability.md:243, 244, 422, 423`

Separate from the anchor-fragment breakage in DOC-024, **the surrounding visible prose in every occurrence reads "§15.4" or "[Section 15.4]" while linking to the REST error catalog at §15.1**. When DOC-024 is mechanically fixed by only retargeting the fragment (`#154-errors-and-degradation` → `#151-rest-api`) without changing the visible label, the reader sees "§15.4" in the rendered text but lands on §15.1 after clicking. This is a distinct readability defect: iter1–5 severity rubric classes a mismatched visible label as **Low** because the content the reader reaches is correct — the reader loses one click of navigation clarity, not normative meaning. Called out separately so the fix pass for DOC-024 explicitly updates both the fragment and the visible label in one commit; if only the fragment is fixed, DOC-027 remains open as a secondary Low-severity finding.

**Recommendation:** Bundle the fix with DOC-024. In every occurrence, replace the visible label `§15.4` / `[§15.4]` / `[Section 15.4]` with `§15.1` / `[§15.1]` / `[Section 15.1]` (matching the retargeted anchor). The existing `[Section 15.4]` phrasings that DO correctly refer to §15.4 (Runtime Adapter Specification) — e.g., `15_external-api-surface.md:8` and the many other correct `§15.4` / `#154-runtime-adapter-specification` links — MUST remain unchanged; the label update applies only where the link target is the REST error catalog.

### DOC-028. Inline label/target mismatch — link goes to §17.8.1 "Operational Defaults — Quick Reference" but prose says "per-region escrow defaults" / "deployment topology" [Info]

**Files:**
- `spec/11_policy-and-controls.md:421`
- `spec/15_external-api-surface.md:1036`
- `spec/16_observability.md:422`

Minor follow-on from DOC-025. When the anchor fragment is retargeted from `#1781-helm-values` → `#1781-operational-defaults--quick-reference`, the visible label `§17.8.1` is already correct and the prose around the link describes the target content accurately (per-region defaults, deployment topology). However, §17.8.1's actual title is "Operational Defaults — Quick Reference", which the reader sees on arrival; a reader skimming the link text "per-region escrow defaults" may initially doubt they are in the right place because the landing heading says something different. This is **Info** severity (strictly navigation polish, no misdirection — the content at §17.8.1 does include per-region residency defaults in its table). Called out so the reviewer knows the DOC-025 retarget is technically correct but the reader experience could be improved.

**Recommendation:** Optional. If DOC-025 is fixed by retargeting to `#1781-operational-defaults--quick-reference`, consider appending a locating phrase to the visible link text, e.g., `[§17.8.1 "Operational Defaults — Quick Reference" per-region residency defaults](...)`. Alternatively, add a short named subsection inside §17.8.1 like `#### Per-region residency defaults` (slug `#per-region-residency-defaults`) and retarget the three DOC-025 links to that slug for a more specific landing point. Pure navigation improvement; no change required if the DOC-025 mechanical fix is accepted as sufficient.

### DOC-029. Re-file of iter5 DOC-022 — "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" headings remain [Low]

**Files:** `spec/16_observability.md:644, 673`; `spec/README.md:105–106`

Unfixed for **five iterations** (iter1 DOC-002 → iter2 DOC-006 → iter3 DOC-011 → iter4 DOC-017 → iter5 DOC-022 → iter6). Headings verbatim:
```
### 16.7 Section 25 Audit Events
### 16.8 Section 25 Metrics
```
The juxtaposition `16.7 Section 25 ...` reads as a structural inconsistency (two section numbers in one heading). Every prior iteration proposed the identical fix; iter5 also proposed a convergence-termination rule ("any Low-severity finding surviving three consecutive iterations with unchanged recommendation MUST receive either (a) a fix commit or (b) an explicit 'accepted — will not fix' resolution note in the next iteration's fix pass"). Neither outcome has occurred; iter6 therefore re-files at unchanged severity. **Calibration note:** per `feedback_severity_calibration_iter5.md`, this stays **Low** and does not drift upward despite five iterations of recurrence — severity is a function of the defect class, not of its age.

**Recommendation (unchanged):** Rename to `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics` in `spec/16_observability.md`. Open each subsection body with a single-sentence cross-reference such as "Introduced by §25 (Agent Operability)." Update the two mirrored lines in `spec/README.md:105–106` to the new titles, and update the anchors (`#167-section-25-audit-events` → `#167-agent-operability-audit-events`, `#168-section-25-metrics` → `#168-agent-operability-metrics`); grep the tree for any callers and rewrite them in the same commit. If the platform policy is to keep "Section 25" in the heading verbatim, record that decision explicitly in this finding's resolution note so the re-file cycle stops.

### DOC-030. Re-file of iter5 DOC-023 — README TOC still omits three numbered subsections [Low]

**File:** `spec/README.md`

Unfixed for **five iterations** (iter2 DOC-007 → iter3 DOC-012 → iter4 DOC-018 → iter5 DOC-023 → iter6). The iter6 programmatic verification against every `X.Y` heading in `spec/` confirms exactly three omissions, unchanged since iter5:

- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README lines 14–23 list 4.1 through 4.9 but not 4.0.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — README line 119 lists §18 as a parent with zero child entries (inconsistent with every other multi-subsection chapter in the TOC).
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README lines 127–147 list 24.1 through 24.20 but not 24.0.

All three target anchors resolve correctly and are cited in running prose throughout the spec (e.g., `17_deployment-topology.md:328` successfully references `#240-packaging-and-installation`; other files reference `#40-agent-operability-additions` and `#181-build-artifacts-introduced-by-section-25`). The defect is purely TOC-level — readers scanning the table of contents do not learn these subsections exist, and any reader who lands on §4, §18, or §24 via the TOC misses the introductory overview content.

**Recommendation (unchanged from iter5):** Insert three TOC lines in `spec/README.md` using the exact indentation the README already applies to level-3 entries:

```
  - [4.0 Agent Operability Additions](04_system-components.md#40-agent-operability-additions)
  - [18.1 Build Artifacts Introduced by Section 25](18_build-sequence.md#181-build-artifacts-introduced-by-section-25)
  - [24.0 Packaging and Installation](24_lenny-ctl-command-reference.md#240-packaging-and-installation)
```

Insert the §4.0 line between the current `- [4. System Components]` parent and `- [4.1 Edge Gateway Replicas]`; insert the §18.1 line as the first (and currently only) child of `- [18. Build Sequence]`; insert the §24.0 line between the current `- [24. `lenny-ctl` Command Reference]` parent and `- [24.1 Bootstrap]`. If the README convention is specifically that `X.0` overviews and single-subsection chapters are intentionally omitted, record that convention in this finding's resolution note so the re-file cycle terminates; otherwise the fix is three one-line inserts.

---

## Carry-forward summary (iter1 → iter6)

| Iter-1 ID | Iter-2 ID | Iter-3 ID | Iter-4 ID | Iter-5 ID | Iter-6 ID | Topic                                                   | Severity | Status     |
| --------- | --------- | --------- | --------- | --------- | --------- | ------------------------------------------------------- | -------- | ---------- |
| DOC-001   | DOC-005   | DOC-008   | DOC-013   | —         | —         | `#1781-operational-defaults--quick-reference` intra     | Medium   | Fixed iter4 |
| DOC-002   | DOC-006   | DOC-011   | DOC-017   | DOC-022   | DOC-029   | "16.7 Section 25 …" heading                             | Low      | Open (5 iters) |
| —         | DOC-007   | DOC-012   | DOC-018   | DOC-023   | DOC-030   | README TOC omissions (4.0 / 18.1 / 24.0)                | Low      | Open (5 iters) |
| —         | —         | —         | DOC-014   | —         | —         | `#253-endpoint-split-between-gateway-and-lenny-ops`     | Medium   | Fixed iter5 |
| —         | —         | —         | DOC-015   | —         | —         | `#251-overview` stale anchor                            | Medium   | Fixed iter5 |
| —         | —         | —         | DOC-016   | —         | —         | `#165-alerts` stale anchor                              | Medium   | Fixed iter5 |
| —         | —         | —         | —         | DOC-019   | —         | `#179-preflight-checks` fabricated anchor               | Medium   | Fixed iter6 |
| —         | —         | —         | —         | DOC-020   | —         | `#152-mcp-endpoints` fabricated anchor                  | Medium   | Fixed iter6 |
| —         | —         | —         | —         | DOC-021   | —         | `#154-error-codes` fabricated anchor                    | Medium   | Fixed iter6 |
| —         | —         | —         | —         | —         | DOC-024   | `#154-errors-and-degradation` fabricated (9 sites)      | Medium   | Open (NEW) |
| —         | —         | —         | —         | —         | DOC-025   | `#1781-helm-values` fabricated (3 sites)                | Medium   | Open (NEW) |
| —         | —         | —         | —         | —         | DOC-026   | `docs/api/internal.md` broken intra anchor (pre-existing) | Low    | Open (NEW surfaced) |
| —         | —         | —         | —         | —         | DOC-027   | §15.4 label / §15.1 target mismatch (co-occurs DOC-024) | Low      | Open (NEW) |
| —         | —         | —         | —         | —         | DOC-028   | §17.8.1 visible-title vs. prose mismatch (co-occurs DOC-025) | Info | Open (NEW) |

**Fixed this iteration:** DOC-019 / DOC-020 / DOC-021 (three Medium anchor regressions from iter5 all closed; zero surviving references to any of the broken fragments).

**Re-filed (unchanged):** DOC-022 → DOC-029, DOC-023 → DOC-030 (both Low-severity, both at five iterations of recurrence, both with identical mechanical fixes).

**New this iteration:** DOC-024 / DOC-025 (two Medium anchor regressions, 12 total occurrences, both introduced by the iter5 fix commit `c941492` for CMP-054 / CMP-057 / CMP-058). DOC-026 (Low, pre-existing intra-docs broken anchor surfaced by the iter6 programmatic sweep). DOC-027 (Low, co-occurs with DOC-024). DOC-028 (Info, co-occurs with DOC-025).

---

## Convergence assessment

**Cross-reference integrity (Medium-severity class).** Iter5's three anchor-integrity defects (DOC-019 / DOC-020 / DOC-021) are all verified fixed in-place. However, **iter5's fix commit `c941492` itself introduced 12 new broken anchor occurrences across two fabricated fragments** (`#154-errors-and-degradation` × 9 + `#1781-helm-values` × 3). Every other iteration in this perspective's history has observed the same failure mode: a non-DOC fix commit in iter N closes 2–4 anchor breakages flagged in iter N-1 while introducing 2–12 new ones in the same patch. The pattern is now fully regular over five iterations:

| Iteration | Fixed anchor refs | Newly introduced |
| --------- | ----------------- | ---------------- |
| iter3 → iter4 | 1 (DOC-008)    | 4 (DOC-013/014/015/016) |
| iter4 → iter5 | 4 (DOC-013–016) | 3 (DOC-019/020/021)     |
| iter5 → iter6 | 3 (DOC-019–021) | 12 (DOC-024 ×9, DOC-025 ×3) |

iter5 called out explicitly that "**the pattern of self-verification claims that were not actually executed is the root cause**" and recommended "a 20-line Python script reading every `](file.md#anchor)` and matching it against the heading-derived slug set would have blocked DOC-014/015/016/019/020/021 at PR time." That gate was not added between iter5 and iter6 — the iter5 fix commit's self-verification (if any was performed) did not catch the 12 new broken refs in its own patch. **Until the programmatic anchor-integrity gate exists in CI or in the fix-review runbook, every iteration should expect to introduce 3–12 new anchor regressions proportional to the volume of fix-commit edits.** The exact 20-line check used in this iteration's sweep is below and could be adopted verbatim as a pre-commit hook or a CI step; the author estimates it at <0.5 sec wall-clock on the full `spec/` tree:

```python
# Pseudocode; full working script is embedded in the iter6 review-runbook scratch.
import re, glob
from collections import defaultdict

def slug(title):
    h = title.strip().rstrip('#').strip().lower()
    h = re.sub(r'[^\w\s\-]', '', h)
    return h.replace(' ', '-')

anchors = defaultdict(set)
for f in glob.glob('spec/*.md'):
    seen = {}
    in_code = False
    for line in open(f):
        if line.lstrip().startswith('```'):
            in_code = not in_code; continue
        if in_code: continue
        m = re.match(r'^(#+)\s+(.+?)\s*$', line)
        if m:
            s = slug(m.group(2))
            c = seen.get(s, -1) + 1; seen[s] = c
            anchors[f.split('/')[-1]].add(s if c == 0 else f'{s}-{c}')

cross = re.compile(r'\]\(([\w\-]+\.md)#([\w\-]+)\)')
intra = re.compile(r'\]\((#[\w\-]+)\)')
bad = []
for f in glob.glob('spec/*.md'):
    fname = f.split('/')[-1]
    in_code = False
    for i, line in enumerate(open(f), 1):
        if line.lstrip().startswith('```'):
            in_code = not in_code; continue
        if in_code: continue
        for m in cross.finditer(line):
            if m.group(2) not in anchors.get(m.group(1), set()):
                bad.append((fname, i, m.group(1), m.group(2)))
        for m in intra.finditer(line):
            if m.group(1)[1:] not in anchors[fname]:
                bad.append((fname, i, fname, m.group(1)[1:]))

assert not bad, '\n'.join(f'{r[0]}:{r[1]} -> {r[2]}#{r[3]}' for r in bad)
```

**Navigation/heading clarity (Low-severity class).** DOC-029 (§16.7/§16.8 "Section 25 Audit Events/Metrics") and DOC-030 (README TOC omissions of §4.0 / §18.1 / §24.0) have now persisted **five iterations** each with unchanged recommendations and trivial fix costs (a three-line README patch for DOC-030, a two-heading rename plus README mirror update for DOC-029). Iter5 proposed a convergence-termination rule — any Low finding at three consecutive iterations with unchanged recommendation receives either a fix or an explicit "accepted — will not fix" note in the next iteration's fix pass — and the rule was not applied. The re-file cycle is now self-sustaining by inattention. **Recommendation (for the convergence meta-discussion, unchanged from iter5 and restated):** if the reviewer does not intend to apply either outcome in iter6 → iter7, this perspective's iter7 report should stop re-filing these items and instead flag them at Info severity with a one-line note "DOC-029 and DOC-030 are formally deferred by process; see convergence assessment of iter5/iter6 for the fix procedure." That stops the re-file noise without losing the record.

**Overall iter6 state for Perspective 22.** **7 findings: 2 Medium + 4 Low + 1 Info.** Medium findings (DOC-024 / DOC-025) are mechanically trivial to fix (12 link-fragment corrections and 8 visible-label updates) but their recurrence at 12× the closed-item count is the strongest signal in this perspective and warrants the CI-gate remediation above — the convergence blocker is a process gap, not a content gap. The Low findings (DOC-026 / DOC-027 / DOC-029 / DOC-030) split into one new-surfaced pre-existing item, one co-occurring DOC-024 follow-on, and two five-iteration carry-forwards. The Info finding (DOC-028) is a pure-navigation polish item flagged only because it co-occurs with the DOC-025 fix pass. **No findings rose to High severity** because none of the broken anchors produced normative ambiguity in running text: in each occurrence the prose surrounding the broken link unambiguously names the concept being referenced (error codes, per-region residency defaults, §25 audit events) so a reader losing the hyperlink still resolves the correct concept via text search or via the error code's name. **The risk remains silent misdirection to the wrong section, not misimplementation of a normative requirement** — identical to iter5's assessment and consistent with the iter1–5 severity anchoring the perspective has applied throughout.

**Convergence on this perspective: No.** Two Medium regressions introduced this iteration (DOC-024 / DOC-025) and two Low findings at five iterations of recurrence without either a fix or a formal defer (DOC-029 / DOC-030). Convergence requires (a) the iter5-recommended programmatic anchor gate to break the regression cycle, and (b) application of the iter5-proposed three-iteration rule to the two Low carry-forwards.
