# Iter7 — Perspective 15: Competitive Positioning & Open Source Strategy

**Review Date:** 2026-04-21
**Reviewer:** Claude Opus 4.7
**Scope:** Market position, differentiation narrative, community adoption strategy, open-source governance, and upstream (`kubernetes-sigs/agent-sandbox`) risk framing. Re-scan of `spec/23_competitive-landscape.md`, `spec/22_explicit-non-decisions.md` §22.6, `spec/21_planned-post-v1.md`, `spec/19_resolved-decisions.md` item 14, `spec/18_build-sequence.md` Phase 0 / Phase 1 / Phase 2 / Phase 17a, `spec/01_executive-summary.md` Core Design Principles, `spec/02_goals-and-non-goals.md`, `spec/04_system-components.md` §4.6 (upstream abstraction + go/no-go + fallback), `spec/05_runtime-registry-and-pool-model.md` §5.1, and `docs/adr/index.md` + `docs/adr/` directory contents.
**Prior:** `iter6/p15_competitive.md` (COM-001 through COM-005). Per `feedback_severity_calibration_iter5.md`, iter6 declared the perspective at **calibrated convergence** (zero Critical / High / Medium, four Low, one Info) and recommended iter7 skip "unless COM-001–COM-005 are batch-fixed (then re-verify only) or changes land in §23, §22.6, §21, §19 item 14, `docs/adr/`, or `spec/18_build-sequence.md` Phase 0/1/2/17a."
**Category:** CPS (per review-findings header convention); in-document finding IDs continue the COM-NNN numbering line from iter5.

**iter6 fix commit scope.** Iter6 fix commit `8604ce9` (`Fix iteration 6: applied fixes for Critical/High/Medium findings + docs sync`) touched the following spec files per `git diff c941492..HEAD --stat -- spec/`: `11_policy-and-controls.md`, `12_storage-architecture.md`, `14_workspace-plan-schema.md`, `15_external-api-surface.md`, `16_observability.md`, `25_agent-operability.md`. **None of this perspective's surfaces were touched** — `18_build-sequence.md`, `19_resolved-decisions.md`, `21_planned-post-v1.md`, `22_explicit-non-decisions.md`, `23_competitive-landscape.md`, and `docs/adr/` are all unchanged since the iter5 fix commit `c941492`. This is the expected outcome of iter6's "C/H/M first, Low/Info deferred" prioritization and does not indicate regression; it does mean COM-001 through COM-005 re-raise verbatim under the "persistence without action stays at original severity" precedent established at iter5.

**Severity calibration note.** Per `feedback_severity_calibration_iter5.md`, this perspective is structurally Low/Info — strategic / narrative polish. Elevation to Medium+ is reserved for tangible technical impact (e.g., upstream dependency abandonment breaks v1 feasibility). No such impact was observed on re-scan. The iter5 COM-001 / COM-002 / COM-004 Low calibrations, iter5 COM-003 Info calibration, and iter6 COM-005 Low calibration are re-used verbatim in this iteration — all five carry-forward under the same severity anchors.

**Numbering.** Continuing the COM/CPS line from iter6. Iter6 ended at COM-005. This iteration introduces **zero new findings** — a third consecutive "zero C/H/M" iteration for this perspective (iter5 introduced COM-001–COM-004 as "new findings" with zero C/H/M; iter6 introduced COM-005 as Low; iter7 surfaces nothing net-new). The standing-skipped items CPS-043 (sustainability / commercial model) and CPS-048 (K8s adoption barrier) are **not reopened** (pure business-model questions, no v1 technical impact under the calibration rubric).

---

## Prior-iteration carry-forwards

### COM-001 — differentiator cross-reference off-by-two (Low) — NOT FIXED, re-raised unchanged

**Section:** `spec/22_explicit-non-decisions.md` line 13 (cross-reference to §23.1); `spec/23_competitive-landscape.md` §23.1 items 6 and 8, line 90.

Re-verified on 2026-04-21: `spec/22_explicit-non-decisions.md` line 13 still reads:

> "See [Section 23.1](23_competitive-landscape.md#231-why-lenny), differentiator 6 for the competitive positioning of this principle."

Meanwhile `spec/23_competitive-landscape.md` §23.1 still lists "Multi-protocol gateway" at position 6 (line 82) and "Ecosystem-composable via hooks-and-defaults" at position 8 (line 86). Line 90 still explicitly states "Beyond the **8 architectural differentiators**" as the authoritative count. The cross-reference target is `differentiator 8`, not `differentiator 6` — the iter6 re-raise rationale was correct and remains applicable.

**Persistence note.** This is now the same finding's *fourth* consecutive un-fixed appearance (iter2 CPS-002 → iter5 COM-001 → iter6 COM-001 → iter7 COM-001). None of the four review iterations has escalated it beyond Low; none of three fix cycles (iter2, iter5, iter6) has addressed it. Under the "persistence without action stays at original severity" precedent, escalation remains unwarranted: this is a one-token documentation accuracy issue (`6` → `8`) with zero technical impact. **Re-raised at Low.**

**Recommendation unchanged from iter5/iter6.** Single-token edit at `spec/22_explicit-non-decisions.md` line 13.

---

### COM-002 — ADR-008 referenced as "recorded" but the file does not exist (Low) — NOT FIXED, re-raised unchanged

**Section:** `spec/19_resolved-decisions.md` line 20 (item 14); `spec/23_competitive-landscape.md` line 62 and line 137; `spec/18_build-sequence.md` line 7 (Phase 0) and line 73 (Open-source readiness note); `docs/adr/index.md` line 83 (catalog row for ADR-0008).

Re-verified on 2026-04-21. The `docs/adr/` directory still contains only:

- `0000-use-madr-for-architecture-decisions.md`
- `index.md`
- `template.md`

No `0008-*.md` file. Meanwhile:

- `spec/19_resolved-decisions.md` line 20 (item 14) still reads "Decision recorded as ADR-008 in `docs/adr/`"
- `spec/23_competitive-landscape.md` line 62 (feature matrix) still reads "MIT (ADR-008)"
- `spec/23_competitive-landscape.md` line 137 still reads "The decision and rationale are recorded as ADR-008 in `docs/adr/`"
- `spec/18_build-sequence.md` line 7 (Phase 0) still reads "Decision recorded as ADR-008 in `docs/adr/`"
- `docs/adr/index.md` line 83 still lists `ADR-0008 | Open-source license selection (MIT) | Planned`

The narrative-vs-artefact gap persists. **Re-raised at Low.** Impact unchanged from iter5/iter6: external evaluators and enterprise legal reviewers reading §23.2 as part of license due diligence will expect to find a populated ADR at the referenced location; its absence weakens governance-narrative credibility.

**Recommendation unchanged.** Either (a) write `docs/adr/0008-open-source-license-selection-mit.md` (MADR body substantially liftable from §19 item 14's already-drafted rationale) and flip the catalog status from `Planned` to `Accepted`, or (b) soften the spec phrasing to "reserved as ADR-008 (planned)" across §18 Phase 0, §19 item 14, and §23 lines 62 and 137. Option (a) is substantially cheaper and also resolves COM-005.

---

### COM-003 — §21 Planned/Post-V1 omits delegation / experiment / eval extensions (Info) — NOT ADDRESSED, re-raised unchanged

**Section:** `spec/21_planned-post-v1.md` (entire section).

Re-verified on 2026-04-21. `spec/21_planned-post-v1.md` still contains items 21.1–21.9 — A2A full support, A2A intra-pod support, Agent Protocol support, future conversational patterns, Environment Management UI, Environment Resource post-V1 deferred items, Multi-Cluster Federation, UI and CLI, and SSH Git URL support. No forward-looking entries for the three differentiators §23.1 leans on most heavily in §23.1's differentiator narrative:

- Recursive delegation extensions (cross-tree visibility, delegation-aware experiment targeting, etc.) — referenced in §23.1 item 5 as a flagship platform primitive
- Experiment primitives extensions (bandits, statistical significance, auto-winner) — §23.1 item 11 explicitly names these as "not built in" with no forward pointer
- Eval hooks extensions (LLM-as-judge integration, automated eval pipelines) — §23.1 item 12 similarly disclaims without forward pointer

No top-of-section clarifying note was added in iter6. **Re-raised at Info.** As noted in iter5 and iter6, this is a narrative-consistency observation rather than a correctness gap — §21 does not claim to be exhaustive — and the recommendation remains optional. Not a convergence blocker at Info; explicitly anchored to the iter5 CPS Info rubric.

**Recommendation unchanged.** Either add brief entries (21.10, 21.11, 21.12) referencing the three differentiators, or add a top-of-section note clarifying that §21 is a representative rather than exhaustive post-V1 list and pointing readers to the "not built in" disclaimers in §22 and §23.1 items 11–12.

---

### COM-004 — Feature Comparison Matrix "Cold-start" row labelling (Low) — NOT FIXED, re-raised unchanged

**Section:** `spec/23_competitive-landscape.md` line 39 (matrix row); line 66 (explanatory note).

Re-verified on 2026-04-21. `spec/23_competitive-landscape.md` line 39 still uses the row label "Cold-start" for both Lenny's session-ready P95 and the competitors' container-boot numbers:

> `| **Cold-start**             | P95 <2s runc, <5s gVisor (session-ready)   | ~150ms (container boot) | Sub-90ms (container boot) | ~300ms (checkpoint/restore) | N/A (persistent workers) | Sub-second (container boot) | N/A (persistent)                |`

The correcting explanatory note at line 66 is still the only reconciliation between the two measurements. A reader scanning the matrix without reading the paragraph below will draw an unfair comparison. **Re-raised at Low.** Presentation-quality issue in the single most-cited comparison artefact in §23.

**Recommendation unchanged from iter5/iter6.** Rename row to "Startup latency (see note below)" or split into two rows ("Session-ready P95" for Lenny / N/A elsewhere, and "Container-boot P95" for all — leaving Lenny's entry as "~ms claim step only, see §6.3" to preserve the apples-to-apples comparison). Minimum-effort fix.

---

### COM-005 — `docs/adr/index.md` "Planned" status ambiguity for ADR-0008 contradicts the Phase 1 gating claim (Low) — NOT FIXED, re-raised unchanged

**Section:** `docs/adr/index.md` line 83 (ADR-0008 catalog row) and line 91 (catalog rule); `spec/18_build-sequence.md` line 8 (Phase 1 "Prerequisite: ADR-008 (license selection) is resolved (MIT) and the `LICENSE` file is committed at the repository root — this gate is satisfied"); `spec/19_resolved-decisions.md` line 20 (item 14).

Re-verified on 2026-04-21. `docs/adr/index.md` line 91 still reads:

> "`Planned` ADRs have reserved numbers but no file yet. When a contributor writes one, they flip the status in both the ADR and this table (to `Accepted` or whatever the outcome is) in the same PR."

Under this catalog rule, the ADR-0008 row (still marked `Planned` on line 83) means "no file yet." Meanwhile `spec/18_build-sequence.md` line 8 still reads:

> "**Prerequisite:** ADR-008 (license selection, [Section 23.2](23_competitive-landscape.md#232-community-adoption-strategy)) is resolved (MIT) and the `LICENSE` file is committed at the repository root — this gate is satisfied."

A reader following the catalog rule literally concludes the gate is NOT satisfied, because the catalog status `Planned` means "no file yet," which contradicts the spec's "gate is satisfied" declaration. This is subtly but meaningfully distinct from COM-002:

- **COM-002** = narrative-to-artefact gap (spec asserts "recorded" but no file exists)
- **COM-005** = narrative-to-own-process-rule contradiction (the ADR catalog's own `Planned` definition directly falsifies the "gate is satisfied" claim)

**Re-raised at Low.** Severity anchor: same class as COM-002 (governance-narrative credibility gap for external contributors / legal evaluators performing due diligence on ADR discipline).

**Recommendation unchanged.** Option (a) resolves COM-002 and COM-005 in one move: write `docs/adr/0008-open-source-license-selection-mit.md` and flip the catalog status row from `Planned` to `Accepted`. Option (b) (leave ADR unwritten and soften narrative) requires coordinated edits to §18 Phase 0 line 7, §18 Phase 1 line 8 ("gate is satisfied" → "the `LICENSE` file is committed; ADR artefact is reserved"), §19 item 14, and §23 lines 62 and 137. Option (a) is substantially cheaper.

---

### Pre-existing items explicitly skipped

**CPS-043 (sustainability / commercial model)** and **CPS-048 (K8s adoption barrier)** remain out of scope per the standing iter4/iter5/iter6 instructions — pure business-model questions with no v1 technical impact under the severity-calibration rule. No reopening in iter7.

---

## New findings

**None.**

A full re-verification of the areas checked and clean in iter6 (listed in the iter6 "Areas checked and clean" subsection) was conducted against the current spec on 2026-04-21. No regression surfaced in any of the following:

- **Upstream risk (`kubernetes-sigs/agent-sandbox`)**: `spec/04_system-components.md` §4.6 still carries the `PodLifecycleManager` / `PoolManager` abstraction layer (lines 340–356), default `AgentSandboxPodLifecycleManager` / `AgentSandboxPoolManager` implementations (line 359), the explicit "a breaking upstream change … requires changing only the implementations behind these interfaces" commitment (line 363), and the Phase-1 ADR-007 go/no-go prerequisite (line 376). The fallback path to custom kubebuilder controllers is preserved. No regression.
- **Temporal / Modal / LangGraph positioning**: §23 narrative rows and §23.1 differentiators 1, 5, 6 still accurately scope each comparison without over-claiming.
- **2026 entrants (Scion, OpenShell, OpenSandbox)**: Three-way matrix at §23 lines 47–64 intact with honest competitive disclosure. No new 2026-Q2 entrants flagged for inclusion at this time.
- **Community adoption funnel**: §23.2 three-persona table still aligns with `lenny up` (§17.4), `lenny-ctl` (§24), enterprise controls (§11, §4.8), and adapter contract (§15.4). TTHW < 5 min commitment intact with CI smoke test requirement.
- **Hooks-and-defaults philosophy**: §22.6 and §23.1 item 8 still agree on scope and posture. The ordering/numbering mismatch is COM-001 (cross-reference, not content).
- **Governance model**: BDfN → steering committee criteria, ADR lifecycle, Phase 2 `CONTRIBUTING.md` + Phase 17a `GOVERNANCE.md` deliverables, Phase 17a early-development notice removal — all internally consistent and unchanged. `CONTRIBUTING.md` and `GOVERNANCE.md` files are present at repo root.
- **MCP vs custom-gRPC positioning**: §01 Core Design Principles item 4 ("MCP for interaction, custom protocol for infrastructure") and §23.1 differentiator 6 still agree.
- **License consistency**: MIT is still consistently described as resolved across §18, §19, §23, §23.2, and the repository `LICENSE` file. iter1 CPS-001 remains fixed. (The artefact-existence gap is COM-002 / COM-005, not a license-consistency regression.)
- **Latency-comparison caveat**: §23 line 66 still correctly disclaims cold-start vs. session-ready measurement differences. The matrix-row-label gap is COM-004.

No new upstream risks surfaced. No new differentiator-claim over-reach. No new governance-narrative inconsistencies beyond the already-flagged COM-002 / COM-005 pair. No positioning drift introduced by iter6 fixes — iter6 fix commit `8604ce9` touched only observability / policy / API / workspace-plan / credential surfaces (`spec/11`, `spec/12`, `spec/14`, `spec/15`, `spec/16`, `spec/25`), and none of those touches propagated back into §23 / §22.6 / §21 / §19 item 14 cross-references.

---

## Convergence assessment

**New findings this iteration:**
- Critical: 0
- High: 0
- Medium: 0
- Low: 0
- Info: 0

**Iter6 carry-forward (unfixed by iter6 fix commit):**
- Low: 4 (COM-001, COM-002, COM-004, COM-005)
- Info: 1 (COM-003)

**Total open CPS findings after iter7:** 5 (4 Low, 1 Info) — **unchanged from iter6 total of 5 (4 Low, 1 Info)**.

**Pre-existing skipped items (out of scope per standing instructions):** CPS-043, CPS-048 — no change.

**Converged (Y/N):** **Y (calibrated).**

- **Zero Critical / High / Medium findings for the third consecutive iteration** (iter5 C/H/M = 0, iter6 C/H/M = 0, iter7 C/H/M = 0).
- **Zero net-new findings this iteration** — the first iteration in this perspective's review history to surface nothing new beyond carry-forwards.
- All five open findings are severity Low or Info.
- None describes technical risk to v1 feasibility.
- All five are single-edit fixes (one-token cross-reference, one-file ADR write or four-location spec phrasing softening, optional §21 addendum or clarifier note, one matrix-row rename or split).

Under a strict reading of "converged = zero Critical/High/Medium," this perspective **IS converged** and has held that state for three consecutive iterations. The persistence of COM-001 through COM-005 across the iter6 fix cycle (which correctly prioritized C/H/M work and deferred Low/Info narrative hygiene) is **not** a quality signal against convergence — it is a scope signal that the Low/Info backlog has accumulated without being worked, which is explicitly the intended behaviour under the iter5 severity-calibration rule ("If an iteration produces zero legitimate C/H/M findings by this rubric, that is the convergence signal. Don't invent Medium findings to keep the loop running.").

**From a competitive-positioning narrative standpoint the spec is mature and stable:** 16 architectural-differentiator / platform-capability entries with spec cross-references, two comparison matrices (sandbox cluster + 2026 Apache-2.0 entrants), a calibrated latency-comparison caveat, explicit trade-offs disclosed to evaluators, three-persona community funnel aligned with concrete entry points, a governance model with transition criteria, and a documented upstream-dependency fallback. **No structural changes are recommended; no positioning drift observed in iter7.**

**Recommended iter8 scope for this perspective:** Skip unless (a) COM-001 through COM-005 are batch-fixed (then re-verify only), or (b) changes land in §23, §22.6, §21, §19 item 14, `docs/adr/`, or `spec/18_build-sequence.md` Phase 0 / 1 / 2 / 17a. If neither condition is met, another iteration on this perspective will produce the same finding set with the same severities and is not a productive use of review capacity.

**Convergence flag for summary aggregation:** **Yes** (0 Critical / High / Medium, 4 Low, 1 Info — same calibrated-converged state as iter5 and iter6; this is the third consecutive iteration at calibrated convergence and the first with zero net-new findings).

---

## Machine-readable summary

```yaml
perspective: 15
category: CPS
new_findings_total: 0
new_findings_by_severity:
  critical: 0
  high: 0
  medium: 0
  low: 0
  info: 0
carry_forward_total: 5
carry_forward_by_severity:
  critical: 0
  high: 0
  medium: 0
  low: 4
  info: 1
carry_forward_ids: [COM-001, COM-002, COM-003, COM-004, COM-005]
new_finding_ids: []
converged: true  # 0 C/H/M for third consecutive iteration; 0 net-new findings; Low/Info carry-forwards do not block convergence under iter5 severity calibration
skipped_preexisting: [CPS-043, CPS-048]
iterations_at_calibrated_convergence: 3  # iter5, iter6, iter7
net_new_findings_this_iteration: 0
iter6_fix_commit_touched_perspective_surfaces: false
recommended_iter8_action: skip_unless_cpsfindings_batch_fixed_or_scope_files_change
```
