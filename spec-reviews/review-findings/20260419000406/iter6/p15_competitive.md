# Iter6 — Perspective 15: Competitive Positioning & Open Source Strategy

**Review Date:** 2026-04-20
**Reviewer:** Claude Opus 4.7
**Scope:** Market position, differentiation narrative, community adoption strategy, open-source governance, and upstream (`kubernetes-sigs/agent-sandbox`) risk framing. Re-scan of `spec/23_competitive-landscape.md`, `spec/22_explicit-non-decisions.md` §22.6, `spec/21_planned-post-v1.md`, `spec/19_resolved-decisions.md` item 14, `spec/18_build-sequence.md` Phase 0/Phase 2/Phase 17a, `spec/01_executive-summary.md` Core Design Principles, `spec/02_goals-and-non-goals.md`, `spec/04_system-components.md` §4.6 (upstream abstraction + go/no-go + fallback), `spec/05_runtime-registry-and-pool-model.md` §5.1, and `docs/adr/index.md` + `docs/adr/` directory contents.
**Prior:** `iter5/p15_competitive.md` (COM-001 through COM-004). No iter5 fixes landed in this perspective's surface — the iter5 fix commit (`c941492`) did not touch §21, §22, §23, §19 item 14, or `docs/adr/`.
**Category:** CPS (per review-findings header convention); in-document finding IDs continue the iter5 COM-NNN numbering line.

**Severity calibration note.** Per `feedback_severity_calibration_iter5.md`, this perspective is usually Low/Info — strategic/narrative polish. Elevation to Medium+ is reserved for tangible technical impact (e.g., upstream dependency abandonment breaks v1 feasibility). The iter5 COM-001 / COM-002 / COM-004 Low calibrations and COM-003 Info calibration are re-used verbatim here: all four remain unfixed regressions/carry-forwards under the calibration rubric. The standing-skipped items CPS-043 (sustainability / commercial model) and CPS-048 (K8s adoption barrier) are NOT reopened (pure business-model questions, no v1 technical impact).

**Numbering:** Continuing the CPS/COM line from iter5. Iter5 ended at COM-004. This iteration introduces COM-005 (one new regression-adjacent finding surfaced during re-verification). Iter5 COM-001–COM-004 are all re-verified as still-unfixed and are promoted from iter5 "new findings" to iter6 "carry-forward regressions" with the same severities. The iter2 precedent (CPS-002 re-raised as iter5 COM-001) establishes that a finding surviving a fix iteration without action stays at its original severity on re-raise — this is applied uniformly to COM-001–COM-004 here.

---

## Status of prior competitive-positioning findings

### Iter5 COM-001 (differentiator cross-reference off-by-two, Low) — NOT FIXED

Re-verified on 2026-04-20: `spec/22_explicit-non-decisions.md` line 13 still reads `differentiator 6`. §23.1 still lists "Multi-protocol gateway" at position 6 (line 82) and "Ecosystem-composable via hooks-and-defaults" at position 8 (line 86), with line 90 explicitly confirming "Beyond the **8 architectural differentiators**" as the authoritative count. The iter5 fix commit touched neither §22 nor §23. **Re-raised as COM-001 carry-forward, still Low.** This is now the same finding's *third* consecutive un-fixed appearance (iter2 CPS-002 → iter5 COM-001 → iter6 COM-001); escalation beyond Low remains unwarranted under the calibration rubric (single-line documentation accuracy issue, no technical impact) but the persistence is itself a convergence signal worth naming: four review iterations have flagged it and no fix commit has landed. The fix is one token (`6` → `8`).

### Iter5 COM-002 (ADR-008 referenced as "recorded" but the file does not exist, Low) — NOT FIXED

Re-verified on 2026-04-20. The `docs/adr/` directory still contains only `0000-use-madr-for-architecture-decisions.md`, `index.md`, and `template.md`. The `docs/adr/index.md` catalog still lists `ADR-0008 | Open-source license selection (MIT) | Planned` (line 83). Meanwhile:

- `spec/19_resolved-decisions.md` line 20 (item 14) still reads "Decision recorded as ADR-008 in `docs/adr/`"
- `spec/23_competitive-landscape.md` line 62 (feature matrix) still reads "MIT (ADR-008)"
- `spec/23_competitive-landscape.md` line 137 still reads "The decision and rationale are recorded as ADR-008 in `docs/adr/`"
- `spec/18_build-sequence.md` line 7 (Phase 0) still reads "Decision recorded as ADR-008 in `docs/adr/`"

The narrative-vs-artefact gap persists. **Re-raised as COM-002 carry-forward, still Low.** Impact unchanged from iter5: external evaluators and enterprise legal reviewers reading §23.2 as part of license due diligence will expect to find a populated ADR at the referenced location; its absence weakens the governance-narrative credibility.

### Iter5 COM-003 (§21 Planned/Post-V1 omits delegation / experiment / eval extensions, Info) — NOT ADDRESSED

Re-verified on 2026-04-20. `spec/21_planned-post-v1.md` still contains items 21.1–21.9 with no forward-looking entries for the three differentiators §23.1 leans on (recursive delegation, experiment primitives, eval hooks). No top-of-section clarifying note was added. **Re-raised as COM-003 carry-forward, still Info.** As noted in iter5, this is a narrative-consistency observation rather than a correctness gap — §21 does not claim to be exhaustive — and the recommendation remains optional. Not a convergence blocker at Info.

### Iter5 COM-004 (Feature Comparison Matrix "Cold-start" row labelling, Low) — NOT FIXED

Re-verified on 2026-04-20. `spec/23_competitive-landscape.md` line 39 still uses the row label "Cold-start" for both Lenny's session-ready P95 and the competitors' container-boot numbers. The correcting explanatory note at line 66 is still the only reconciliation between the two measurements. **Re-raised as COM-004 carry-forward, still Low.** Presentation-quality issue in the single most-cited comparison artefact in §23. The iter5 recommendation (rename row or split into two rows) remains the minimum-effort fix.

### Pre-existing items explicitly skipped

**CPS-043 (sustainability / commercial model)** and **CPS-048 (K8s adoption barrier)** remain out of scope per prior-iteration instructions — pure business-model questions with no v1 technical impact under the severity-calibration rule.

---

## New findings

### COM-005 `docs/adr/index.md` "Planned" status ambiguity for ADR-0008 contradicts the Phase 0 gating claim [LOW]

**Section:** `docs/adr/index.md` line 83 (ADR-0008 catalog row); `spec/18_build-sequence.md` line 7 (Phase 0 entry) and line 8 (Phase 1 "Prerequisite: ADR-008 (license selection) is resolved (MIT) and the `LICENSE` file is committed at the repository root — this gate is satisfied"); `spec/19_resolved-decisions.md` line 20 (item 14).

The catalog note at `docs/adr/index.md` line 91 says: *"`Planned` ADRs have reserved numbers but no file yet. When a contributor writes one, they flip the status in both the ADR and this table (to `Accepted` or whatever the outcome is) in the same PR."* The catalog marks ADR-0008 as `Planned`, which under this rule means the ADR has not yet been written.

But the spec asserts the decision is finished: `spec/18_build-sequence.md` line 8 explicitly states "ADR-008 (license selection, [§23.2]) is resolved (MIT) and the `LICENSE` file is committed at the repository root — **this gate is satisfied**", and every other spec location treats the decision as closed past-tense. This is a stronger statement than COM-002 (file-not-written) alone: it is a direct semantic contradiction between the spec's Phase 1 prerequisite declaration ("this gate is satisfied") and the ADR catalog's own definition of `Planned` ("no file yet"). A reader following the catalog key literally will conclude the gate is NOT satisfied because the catalog status row says `Planned` and the catalog rule says `Planned` means "no file yet" — which means the satisfaction claim in §18 Phase 1 is, by the catalog's own rule, unsubstantiated.

This is subtly distinct from COM-002: COM-002 is a narrative-to-artefact gap (spec says "recorded", no file exists); COM-005 is a narrative-to-own-process-rule contradiction (the ADR catalog's own "Planned means no file yet" definition directly falsifies the "gate is satisfied" claim when the ADR status is still `Planned`). Fixing COM-002 via option (a) — writing the ADR and flipping `Planned` → `Accepted` — would also resolve COM-005. Fixing COM-002 via option (b) — softening spec phrasing to "reserved as ADR-008 (planned)" — would resolve COM-002 but NOT fully resolve COM-005 unless `spec/18_build-sequence.md` line 8 is also softened from "gate is satisfied" to "gate is satisfied in so far as the LICENSE file is committed; ADR artefact is reserved" (or equivalent). The `LICENSE` file existing at the repository root is load-bearing for the Phase 1 prerequisite, but the spec text binds the satisfaction claim to the ADR decision record as well, not just the LICENSE file.

**Competitive-positioning impact.** Identical class to COM-002: external contributors and legal evaluators performing due diligence on governance discipline will land on an internal contradiction. The `docs/adr/index.md` catalog is the canonical decision-record directory, not a separate artefact — the spec cannot claim a gate satisfied by a decision record the catalog itself declares to be missing.

**Recommendation.** Option (a) is preferred and resolves COM-002 and COM-005 in one move: write `docs/adr/0008-open-source-license-selection-mit.md` (MADR body substantially liftable from §19 item 14 already-drafted rationale) and flip the catalog status row from `Planned` to `Accepted`. Option (b) (leave ADR unwritten but soften narrative) requires coordinated edits to §18 Phase 0, §18 Phase 1 line 8, §19 item 14, and §23 line 137 to avoid both COM-002 and COM-005. Option (a) is substantially cheaper.

---

## Areas checked and clean (re-verification)

All iter5 "Areas checked and clean" items re-verified on 2026-04-20; none regressed in the iter5 fix cycle:

- **Upstream risk (`kubernetes-sigs/agent-sandbox`)**: `spec/04_system-components.md` §4.6 still carries the `PodLifecycleManager` / `PoolManager` abstraction layer, the Phase-1-exit go/no-go assessment, the documented 2–3 engineering-week fallback to custom kubebuilder controllers, and the quarterly dependency-review triggers. No regression.
- **Temporal / Modal / LangGraph positioning**: §23 narrative rows and §23.1 differentiators 1, 5, 6 still accurately scope each comparison without over-claiming.
- **2026 entrants (Scion, OpenShell, OpenSandbox)**: Three-way matrix at §23 lines 47–64 intact with honest competitive disclosure.
- **Community adoption funnel**: §23.2 three-persona table still aligns with `lenny up` (§17.4), `lenny-ctl` (§24), enterprise controls (§11, §4.8), and adapter contract (§15.4). TTHW < 5 min commitment intact with CI smoke test requirement.
- **Hooks-and-defaults philosophy**: §22.6 and §23.1 item 8 still agree on scope and posture. The ordering/numbering mismatch is COM-001 (cross-reference, not content).
- **Governance model**: BDfN → steering committee criteria, ADR lifecycle, Phase 2 `CONTRIBUTING.md` + Phase 17a `GOVERNANCE.md` deliverables, Phase 17a early-development notice removal — all internally consistent and unchanged. `CONTRIBUTING.md` and `GOVERNANCE.md` files present at repo root.
- **MCP vs custom-gRPC positioning**: §01 Core Design Principles item 4 ("MCP for interaction, custom protocol for infrastructure") and §23.1 differentiator 6 still agree.
- **License consistency**: MIT is still consistently described as resolved across §18, §19, §23, §23.2, and the repository LICENSE file. iter1 CPS-001 remains fixed. (The artefact-existence gap is COM-002 / COM-005, not a license-consistency regression.)
- **Latency-comparison caveat**: §23 line 66 still correctly disclaims cold-start vs. session-ready measurement differences. The matrix-row-label gap is COM-004.

No new upstream risks surfaced. No new differentiator-claim over-reach. No new governance-narrative inconsistencies beyond COM-005.

---

## Convergence assessment

**New findings this iteration:**
- Critical: 0
- High: 0
- Medium: 0
- Low: 1 (COM-005)
- Info: 0

**Iter5 carry-forward (unfixed by iter5 fix commit):**
- Low: 3 (COM-001, COM-002, COM-004)
- Info: 1 (COM-003)

**Total open CPS findings after iter6:** 5 (4 Low, 1 Info).

**Pre-existing skipped items (out of scope per standing instructions):** CPS-043, CPS-048 — no change.

**Converged (Y/N):** **N** — but only nominally. All five open findings are severity Low or Info, none describe technical risk to v1 feasibility, and all five are single-edit fixes (one-token cross-reference, one-file ADR write or four-token spec phrasing softening, optional §21 addendum or clarifier note, one matrix-row rename or split). Under a strict reading of "converged = zero Critical/High/Medium", this perspective IS converged: zero C/H/M findings across both iter5 and iter6, and the perspective has held that state for two consecutive iterations.

The persistence of COM-001 through COM-004 across an iter5 fix cycle that did not touch this perspective's surfaces is NOT a quality signal against convergence — it is a scope signal that the iter5 fix pass prioritized C/H/M work and deferred Low/Info narrative hygiene, which is the intended behaviour. **From a competitive-positioning narrative standpoint the spec is mature and stable**: 16 architectural-differentiator / platform-capability entries with spec cross-references, two comparison matrices (sandbox cluster + 2026 Apache-2.0 entrants), a calibrated latency-comparison caveat, explicit trade-offs disclosed to evaluators, three-persona community funnel aligned with concrete entry points, a governance model with transition criteria, and a documented upstream-dependency fallback. No structural changes recommended.

**Recommended iter7 scope for this perspective:** Skip unless (a) COM-001 through COM-005 are batch-fixed (then re-verify only), or (b) changes land in §23, §22.6, §21, §19 item 14, `docs/adr/`, or `spec/18_build-sequence.md` Phase 0/1/2/17a.

**Convergence flag for summary aggregation:** Yes (0 Critical/High/Medium, 4 Low, 1 Info — same calibrated-converged state as iter5).

---

## Machine-readable summary

```yaml
perspective: 15
category: CPS
new_findings_total: 1
new_findings_by_severity:
  critical: 0
  high: 0
  medium: 0
  low: 1
  info: 0
carry_forward_total: 4
carry_forward_by_severity:
  critical: 0
  high: 0
  medium: 0
  low: 3
  info: 1
carry_forward_ids: [COM-001, COM-002, COM-003, COM-004]
new_finding_ids: [COM-005]
converged: true  # 0 C/H/M; Low/Info carry-forwards do not block convergence under iter5 severity calibration
skipped_preexisting: [CPS-043, CPS-048]
```
