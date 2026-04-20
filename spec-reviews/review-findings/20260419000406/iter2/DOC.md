# Document Quality, Consistency & Completeness Review — Iteration 2

**Date:** 2026-04-19
**Iteration:** 2
**Spec Size:** 28 files, ~17,991 lines (+250 net vs iter1)

---

## Status vs iter1

- **DOC-001 (../spec/ relative paths):** FIXED. `grep -r "\.\./spec/"` in `/spec/` returns zero hits.
- **DOC-002 (confusing 16.7/16.8 titles "Section 25 Audit Events" / "Section 25 Metrics"):** NOT FIXED. Titles at `16_observability.md:514` and `:533`, and README lines 105–106, are unchanged. Leaving as-is may be a conscious editorial choice, so re-filed as DOC-006 below at **Low** priority, not a regression.

## New findings (iter2)

### DOC-003 Broken cross-file anchor `#62-session-lifecycle-state-machine-and-timers` [Medium]

**File:** `27_web-playground.md:145`

Reference reads `[§6.2](06_warm-pod-model.md#62-session-lifecycle-state-machine-and-timers)`. The target section 6.2 in `06_warm-pod-model.md:67` is titled **"Pod State Machine"** (anchor `#62-pod-state-machine`); no section in `06_warm-pod-model.md` uses the phrase "session lifecycle state machine and timers". From the surrounding text ("hard override of the runtime's `maxIdleTimeSeconds`"), the intended link is most likely `07_session-lifecycle.md#72-interactive-session-model` (which documents `maxIdleTimeSeconds`), or `06_warm-pod-model.md#62-pod-state-machine` if the pod-level state machine was meant.

**Recommendation:** Either
- change target to `07_session-lifecycle.md#72-interactive-session-model` and rename the link text to `§7.2`, or
- change anchor to `#62-pod-state-machine` and keep `§6.2`.

---

### DOC-004 Broken cross-file anchor `#255-event-stream` [Medium]

**File:** `15_external-api-surface.md:633` (`INVALID_CALLBACK_URL` row)

Reference reads `[Section 25.5](25_agent-operability.md#255-event-stream)`. The actual H2 at `25_agent-operability.md:2315` is **"25.5 Operational Event Stream"**, whose GitHub anchor is `#255-operational-event-stream`. Simple typo — the word "Operational" is missing from the anchor fragment. README line 153 uses the correct anchor.

**Recommendation:** `#255-event-stream` → `#255-operational-event-stream`.

---

### DOC-005 Broken intra-file anchor `#166-service-level-objectives` [Medium]

**File:** `16_observability.md:30` (Checkpoint-duration metric row)

Reference reads `[Section 16.6](#166-service-level-objectives)`. Section 16.6 at line 506 is actually titled **"Operational Events Catalog"** (anchor `#166-operational-events-catalog`). There is no dedicated "Service Level Objectives" section — SLOs are documented inline in 16.5 "Alerting Rules and SLOs". The "Checkpoint duration SLO" is defined within 16.5.

**Recommendation:** Change target to `#165-alerting-rules-and-slos` (the checkpoint-duration SLO lives there). If a future refactor breaks SLOs into a dedicated 16.6, keep the rename in mind.

---

### DOC-006 Section titles "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing [Low]

**Files:** `16_observability.md:514, 533`, `README.md:105–106`

Same finding as iter1 DOC-002 — not fixed. Re-filing at Low as the text is parseable in context, but the juxtaposition of two section numbers in the heading (`16.7` and `Section 25` in the title text) still confuses the reader and the README TOC.

**Recommendation:** `16.7 Section 25 Audit Events` → `16.7 Agent Operability Audit Events` (and equivalent for 16.8), with a clarifying inline sentence if a back-reference to §25 is desired.

---

### DOC-007 TOC omits first subsection of three sections [Low]

**File:** `README.md`

The README TOC omits three valid H3/H2 subsections that exist in the spec files:

- **`4.0 Agent Operability Additions`** (`04_system-components.md:3`) — the README jumps from `4. System Components` to `4.1 Edge Gateway Replicas`.
- **`24.0 Packaging and Installation`** (`24_lenny-ctl-command-reference.md:19`) — the README jumps from `24. lenny-ctl Command Reference` to `24.1 Bootstrap`.
- **`18.1 Build Artifacts Introduced by Section 25`** (`18_build-sequence.md:75`) — the README has no subsection entries at all for Section 18, unlike every other section with subsections.

These subsections introduce Section 25-derived content that readers approaching via the TOC cannot discover. Section 4.0 is particularly important because it inventories the new `lenny-ops` component and shared packages.

**Recommendation:** Add the three TOC entries following the existing formatting. If the convention is "skip `*.0` front-matter subsections", then 18.1 is still a required entry.

---

## Verification Summary

| Check                                                    | Result                                  |
| -------------------------------------------------------- | --------------------------------------- |
| Cross-file anchor validity (177 unique refs, 22 files)   | 2 broken (DOC-003, DOC-004)             |
| Intra-file anchor validity                               | 1 broken (DOC-005)                      |
| `../spec/` relative-path regression                      | None                                    |
| Section numbering continuity (e.g., 4.0–4.9, 16.1–16.10) | Clean, no gaps                          |
| README TOC coverage of H3 subsections                    | 3 missing entries (DOC-007)             |
| iter1 DOC-002 regression                                 | Not fixed (re-filed as DOC-006, Low)    |
| Empty sections                                           | Only §20 (intentional, points to §19)   |
| Duplicate headings / content                             | None new                                |
| New §25 cross-references (`§25.3`–`§25.13`)              | All resolve                             |
| New §27.3.1 cross-references                             | All resolve (target exists as H4)       |
| New §16.5 `PoolConfigValidatorUnavailable` alert ref     | Resolves                                |

---

## Notes

- Total unique cross-file anchor references counted: 177. Three are broken; the remaining 174 resolve correctly.
- Anchor generator used mirrors GitHub's rule that `/` removal preserves surrounding spaces (so "Event / Checkpoint Store" → `#44-event--checkpoint-store` with double hyphen); validated against the README's own anchor for section 4.4.
- Large new prose in `04_system-components.md:578` (PoolConfigValidatorUnavailable reference) and `25.x` / `27.x` additions all link correctly.
