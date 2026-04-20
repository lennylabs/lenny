# Iter3 DOC Review

**Date:** 2026-04-19
**Iteration:** 3
**Spec Size:** 28 files
**Scope:** Regression-check iter1+iter2 DOC fixes; audit anchor integrity after iter2 restructure (terminology rename, §15.1/§16.5/§17.4 changes).

---

## Status vs iter2

- **DOC-003** (intra-file link mislabeled `§6.2` / `#62-session-lifecycle-state-machine-and-timers` on `27_web-playground.md:145`): FIXED. The broken target no longer appears in `27_web-playground.md`; §6.2 refs there now use `06_warm-pod-model.md#62-pod-state-machine`.
- **DOC-004** (`25_agent-operability.md#255-event-stream` on `15_external-api-surface.md:633`): FIXED. Ref now reads `#255-operational-event-stream`.
- **DOC-005** (`#166-service-level-objectives` on `16_observability.md:30`): FIXED. All SLO cross-refs now point to `#165-alerting-rules-and-slos`.
- **DOC-006** (16.7/16.8 titled `Section 25 Audit Events` / `Section 25 Metrics`): NOT FIXED. Re-filed below as DOC-011 (still Low).
- **DOC-007** (README TOC missing `4.0`, `24.0`, `18.1`): NOT FIXED. Re-filed below as DOC-012 (still Low).

---

## New findings (iter3)

### DOC-008 Intra-file anchor `#73-retry-and-resume` does not resolve in `06_warm-pod-model.md` [Medium]

**File:** `06_warm-pod-model.md:278`

The line ends `... defined in [§7.3](#73-retry-and-resume). This preserves ...`. The `(#...)` form is an intra-file anchor, but `06_warm-pod-model.md` contains no `§7.3` heading — §7.3 ("Retry and Resume") lives in `07_session-lifecycle.md:336`. On GitHub, this link resolves to the top of the current file rather than the intended section. The same paragraph gets the second reference to §7.3 right (plain text "the `retryPolicy` defined in §7.3" with no link), so this is a link-target bug, not a section-number bug.

This reference was introduced in the iter2 fix commit (SES-004/005 "resumeMode enum parity + starting→resume_pending transitions"), which added the SES-005 paragraph that includes this link. A regression of the kind the DOC checker was designed to catch.

**Recommendation:** Change `[§7.3](#73-retry-and-resume)` → `[§7.3](07_session-lifecycle.md#73-retry-and-resume)`.

---

### DOC-009 Cross-file anchor `16_observability.md#167-audit-event-catalogue` does not exist [Medium]

**File:** `11_policy-and-controls.md:299`

The line reads: `... appear in the catalogued audit event list in [§16.7](16_observability.md#167-audit-event-catalogue) / equivalent.` The actual heading at `16_observability.md:532` is **"16.7 Section 25 Audit Events"**, whose anchor is `#167-section-25-audit-events`. There is no heading titled "Audit Event Catalogue" anywhere in the spec. The trailing "/ equivalent" hedge in the prose suggests the author anticipated this rename mismatch but did not finish the fix.

This reference was introduced by the iter2 POL-014 fix ("Clarified AdmissionController as pre-chain gate"), which rewrote the audit-events paragraph in §11.6. The section the author had in mind does exist (the §16.7 table of §25-derived audit events), so the correct fix is to point at the real anchor.

**Recommendation:** Change `(16_observability.md#167-audit-event-catalogue)` → `(16_observability.md#167-section-25-audit-events)`. When DOC-011 (below) is addressed by renaming 16.7 to "Agent Operability Audit Events", update this link in the same pass to `#167-agent-operability-audit-events`.

---

### DOC-010 Two broken `06/12` anchors in the admission-policies enumeration [Medium]

**File:** `17_deployment-topology.md:51` and `17_deployment-topology.md:52`

Both were introduced by the iter2 K8S-037 fix ("Expanded §17.2 admission-policies enumeration to 12 items + preflight check").

1. **Line 51** — `lenny-t4-node-isolation` entry reads `(see [Section 6.4](06_warm-pod-model.md#64-resource-limits-and-isolation))`. Section 6.4 at `06_warm-pod-model.md:347` is actually titled **"Pod Filesystem Layout"** (anchor `#64-pod-filesystem-layout`). T4 dedicated-node rules live inside that section starting at line 389 — the fragment name "resource-limits-and-isolation" does not exist in the file. Grep confirms zero occurrences of any heading named "Resource Limits" in the spec.

2. **Line 52** — `lenny-drain-readiness` entry reads `(see [Section 12.5](12_storage-architecture.md#125-minio-object-storage) and NET-037 in [Section 13.2](13_security-model.md#132-network-isolation))`. Section 12.5 at `12_storage-architecture.md:270` is titled **"Artifact Store"** (anchor `#125-artifact-store`); there is no `#125-minio-object-storage` heading. MinIO is discussed within §12.5, but the anchor fragment does not match.

Both anchors also appear to be fabrications (invented for the expanded enumeration) rather than renames of previously working anchors.

**Recommendation:**
- line 51: `#64-resource-limits-and-isolation` → `#64-pod-filesystem-layout` (the correct containing section for T4 node isolation)
- line 52: `#125-minio-object-storage` → `#125-artifact-store`

---

### DOC-011 Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing [Low]

**Files:** `16_observability.md:532, 552`, `README.md:105–106`

Re-file of DOC-002 (iter1) and DOC-006 (iter2). Titles juxtapose two section numbers (`16.7` + "Section 25"), which reads as a structural error on first pass. The README TOC mirrors the same string. This finding has now survived two iterations — either the rename is blocked on a policy decision, or it has been lost in the backlog.

**Recommendation:** Same as DOC-006 — `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics`, with an opening sentence in each body that states "Introduced by §25 (Agent Operability)". Update README TOC lines 105–106 to match. Opens a path for DOC-009 to cleanly reference `#167-agent-operability-audit-events`.

---

### DOC-012 README TOC still omits first subsection of three sections [Low]

**File:** `README.md`

Re-file of DOC-007 (iter2). Confirmed unfixed:

- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README jumps from `4. System Components` to `4.1 Edge Gateway Replicas`.
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README jumps from `24. lenny-ctl Command Reference` to `24.1 Bootstrap`.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — §18 has zero subsection entries in the README, unlike every other section with subsections.

Grep for `18.` in README TOC: zero subsection entries. Grep for `4.0` or `24.0` prefix in README TOC: zero entries.

**Recommendation:** Same as DOC-007.

---

## Verification Summary

| Check | Result |
|-------|--------|
| iter2 DOC-003 (27_web-playground §6.2 anchor) | FIXED |
| iter2 DOC-004 (§25.5 event-stream anchor) | FIXED |
| iter2 DOC-005 (§16.6 SLO anchor) | FIXED |
| iter2 DOC-006 (16.7/16.8 confusing titles) | NOT FIXED → DOC-011 |
| iter2 DOC-007 (README TOC omissions) | NOT FIXED → DOC-012 |
| Cross-file anchor validity (22 files, 180+ unique refs) | 3 broken (DOC-008 intra-file, DOC-009, DOC-010×2) |
| Intra-file anchor validity (all files) | 1 broken (DOC-008) |
| `../spec/` relative-path regression | None |
| §N.M plain-text references (e.g., §6.4, §12.5, §17.4, §25.5) | All resolve (non-anchor references) |
| Terminology rename (Tier 0/1/2 → Embedded/Source/Compose Mode) | No anchor breakage — §17.4 heading retains stable anchor `#174-local-development-mode-lenny-dev`; all 14 references across 10 files resolve |
| New §15.1 error catalog entries (EXTENSION_COOL_OFF_ACTIVE, CLASSIFICATION_CONTROL_VIOLATION) | Resolve from 8 cross-references |
| New §16.5 alerts (PoolConfigValidatorUnavailable, etc.) | All cross-references resolve |
| §4.4 "Event / Checkpoint Store" double-hyphen anchor | `#44-event--checkpoint-store` resolves correctly in all 20+ references (both `/`-adjacent spaces preserved per GitHub slug rules) |
| §21.1-style bold-prose "sub-headings" | No anchor-style links point to them; all refs are plain-text `§21.1` |
| Textual `§` references | 4 false-positive "missing" cases — all are citations to external standards (HIPAA 45 CFR §164.312/410/502/530, RFC 7519 §4.1.4, RFC 6749 §2.3); not spec-internal |

---

## Notes

- All three broken-anchor findings (DOC-008, DOC-009, DOC-010) were **introduced by the iter2 fix commit (2a46fb6)** — they are regressions directly attributable to new content, not pre-existing problems. DOC-008 rode in on SES-004/005; DOC-009 on POL-014; DOC-010 on K8S-037.
- The anchor audit used GitHub's slugification rules: lowercase, strip non-word/non-space/non-hyphen (preserving whitespace runs so `/`-adjacent spaces become `--`), whitespace-to-hyphen.
- No new broken anchors were introduced by the Embedded/Source/Compose Mode rename — §17.4's anchor did not change and all 14 pre-existing `§17.4` references still resolve.
- No new duplicate headings, no empty sections added, no TOC regressions beyond DOC-012.
- The `../docs/operator-guide/external-llm-proxy.md` reference at `04_system-components.md:1543` points out of the spec directory into the docs tree — treated as an intentional cross-tree link, not a DOC bug. Not verified for existence in this review.
- Total spec-internal anchor references counted: 184 (cross-file) + ~45 (intra-file). 4 broken = 98% resolution rate.
