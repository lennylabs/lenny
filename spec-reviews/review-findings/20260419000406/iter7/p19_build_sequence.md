# Perspective 19: Build Sequence & Implementation Risk — Iter7

**Scope:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) with cross-references to [`spec/17_deployment-topology.md`](../../../spec/17_deployment-topology.md) §17.2 / §17.8.5 / §17.9 (for dependency verification and row-number traceability); [`spec/15_external-api-surface.md`](../../../spec/15_external-api-surface.md) §15.2 (Shared Adapter Types for BLD-016 carry-over); [`spec/25_agent-operability.md`](../../../spec/25_agent-operability.md) §25.13 (bundled alerting rules for BLD-017 carry-over).

**Iter6 fix commit consulted:** `8604ce9` (iter6 C/H/M fix pass). `git show 8604ce9 -- spec/18_build-sequence.md` is empty — **the iter6 fix cycle did not touch §18.** All three iter6 P19 findings (BLD-016, BLD-017, BLD-018) were Low-severity and explicitly deferred by iter6's convergence note as non-blocking polish; none were batched into the iter6 fix pass. This is consistent with the iter6 convergence assessment ("skipping them to iter7 is also acceptable given their Low severity").

---

## Iter6 carry-forward verification

### BLD-012 (iter5 High). `lenny-ops` phase assignment. **STILL FIXED — no regression.**

Re-verified end-to-end against the current spec:

- §18.1 line 92 **Phase assignments** subsection still contains the four per-artifact phase anchors (`ImageResolver` → Phase 2; `pkg/alerting/rules` + `pkg/recommendations/rules` → Phase 2.5; `lenny-ops` → Phase 3.5; `lenny-backup` → Phase 13).
- §18 Phase 2 (line 10) still carries the `ImageResolver` clause; Phase 2.5 (line 11) still carries the shared-rule-packages clause; Phase 3.5 (line 14) still carries the mandatory `lenny-ops` first-deploy paragraph; Phase 13 (line 63) still carries the `lenny-backup` first-ship clause.
- §17.8.5's mandatory-component contract is unchanged; §18 no longer contains a `features.ops` flag reference anywhere.
- No regression to BLD-009 or BLD-011 (iter4) has been introduced by the iter6 cycle (which did not touch §18 at all).

### BLD-014 (iter5 Medium). `lenny-ops-sa` RBAC preflight-check traceability + core-Deployment test-suite naming. **STILL FIXED — no regression.**

Re-verified: §17.9 row 503 still carries the "Unconditional from Phase 3.5 onward, per the §17.8.5 mandatory-`lenny-ops` contract — there is no `features.ops` flag because chart validation rejects any attempt to disable `lenny-ops`, so this check fires on every preflight run." annotation; §17.2 line 71 still names `tests/integration/core_deployment_inventory_test.go` as a parallel suite; §18 Phase 3.5 still names the same test suite inside the Mandatory `lenny-ops` first-deploy paragraph.

### BLD-016 (iter6 Low). Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry. **NOT fixed in iter6.**

`grep 'shared\.go|session-event-v1|adapter-shared-v1|SessionEventKind|Shared Adapter' spec/18_build-sequence.md` returns zero matches. Phase 1's wire-contract artifact list (`spec/18_build-sequence.md` line 8) is byte-identical to iter6: it still contains only `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/outputpart.schema.json`, `schemas/workspaceplan-v1.json`. Neither `pkg/adapter/shared.go` nor the JSON Schema pair (`schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json`) is named.

§15.2 continues to define these types normatively — the closed `SessionEventKind` enum (lines 315–323), the "Shared Adapter Types" section (line 178 heading), and the `SupportedEventKinds []SessionEventKind` member (line 83) are all unchanged from iter5/iter6 state. Re-raised at Low severity below (BLD-019), continuing the lineage iter4 BLD-010 → iter5 BLD-013 → iter6 BLD-016.

### BLD-017 (iter6 Low). Phase 13 observability milestone does not name the §25.13 bundled-alerting-rules deliverable. **NOT fixed in iter6.**

Phase 13 (line 63) still says "OpenTelemetry metrics and dashboards, distributed tracing visualization, alerting rules, SLO monitoring" generically without naming the `docs/alerting/rules.yaml` render / `PrometheusRule` / ConfigMap pipeline. §18.1 line 88 still describes the pipeline as a floating expectation without a phase anchor. §18.1 line 95 (Phase assignments — `pkg/alerting/rules`) notes Phase 13 as a consumer in parenthetical form ("the recommendations package is required by … the Phase 13 `PrometheusRule` / ConfigMap templates") but this is a one-way cross-reference in the Phase assignments bullet; the Phase 13 row itself carries no pipeline deliverable. Re-raised at Low severity below (BLD-020), continuing the lineage iter5 BLD-015 → iter6 BLD-017.

### BLD-018 (iter6 Low). §18 Phase 3.5 and §18.1 Phase assignments cite "§17.9 row 501" but the `lenny-ops-sa` RBAC check is at row 503. **NOT fixed in iter6.**

`grep 'row 501' spec/18_build-sequence.md` returns two matches:

1. **Line 14 (Phase 3.5 Mandatory `lenny-ops` first-deploy paragraph):** "there is no `features.ops` flag — §17.8.5 rejects any attempt to disable it, and **§17.9 row 501's unconditional `lenny-ops-sa` RBAC preflight check** is unconditional precisely because `lenny-ops` is mandatory from this phase".
2. **Line 96 (§18.1 Phase assignments — `lenny-ops` container image):** "§17.8.5 rejects any attempt to disable `lenny-ops` at chart validation, and **§17.9 row 501's `lenny-ops-sa` RBAC preflight check** is unconditional precisely because `lenny-ops` is mandatory from this phase onward".

§17.9 row 501 is still the "SIEM endpoint (warning)" row (re-verified at line 501). The `lenny-ops-sa` RBAC check is still at line 503. Both references are therefore still stale. Re-raised at Low severity below (BLD-021), continuing the lineage iter6 BLD-018.

---

## Iter7 findings

### BLD-019. Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry [Low, carry-over of BLD-010 iter4 / BLD-013 iter5 / BLD-016 iter6]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) line 8 (Phase 1 wire-contract list); [`spec/15_external-api-surface.md`](../../../spec/15_external-api-surface.md) §15.2 lines 83, 178, 315–323 (Shared Adapter Types + closed `SessionEventKind` enum).

This is now a **four-iteration carry-over** (iter4 BLD-010 → iter5 BLD-013 → iter6 BLD-016 → iter7 BLD-019). The recommendation is unchanged across all four iterations. Severity remains Low per the iter5 feedback rubric: the gap does not cause a correctness or security breakage because Phase 2's adapter binary protocol implementation and Phase 5's `ExternalAdapterRegistry` can build against the §15 prose and the closed-enum definition in §15.2; the cost is that closed-enum additions after Phase 2 can diverge between the §15 prose, adapter implementations, and the runtime registry without commit-time CI visibility, producing an integration-test break rather than a schema conflict.

The "spec-first artifacts; Phase 2 implements against them and the CI build verifies the implementation stays in sync" principle of Phase 1 is violated for the closed-enum contract that §15.2 declares normative for every external adapter — but this is the same principle violation called out in iter4 BLD-010 and the severity has remained Low across four iterations.

**Recommendation (unchanged from iter4 BLD-010 / iter5 BLD-013 / iter6 BLD-016):** Extend Phase 1's "Machine-readable wire-contract artifacts committed to the repository" list with either:

- `pkg/adapter/shared.go` — Go package containing `SessionMetadata`, `AuthorizedRuntime`, `AdapterCapabilities`, `OutboundCapabilitySet`, `SessionEvent`, `PublishedMetadataRef`, and the closed `SessionEventKind` enum; **or**
- a `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` JSON Schema pair with matching Go codegen, paralleling `workspaceplan-v1.json`.

Add the same CI gate as the other Phase 1 schemas: §15 additions mirror code changes (closed-enum additions bump the schema version and require a `SessionEventKind` row + `AdapterCapabilities.SupportedEventKinds` documentation update).

---

### BLD-020. Phase 13 observability milestone does not name the §25.13 bundled-alerting-rules deliverable [Low, carry-over of BLD-015 iter5 / BLD-017 iter6]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) Phase 13 line 63 (full observability stack); §18.1 line 88 (Alerting-rule artifacts); [`spec/25_agent-operability.md`](../../../spec/25_agent-operability.md) §25.13 (bundled alerting rules).

Three-iteration carry-over (iter5 BLD-015 → iter6 BLD-017 → iter7 BLD-020). The recommendation is unchanged. Severity remains Low per the iter5 feedback rubric: the pipeline exists in spec (Phase 2.5 produces `pkg/alerting/rules`; §18.1 line 88 describes the deployer-rendering pipeline; §16.5 line 524 specifies `monitoring.format` for `PrometheusRule` vs ConfigMap), but Phase 13 — where the deployer-visible manifests become the canonical build artifact — does not name the rendering pipeline as a phase deliverable.

The Phase 2.5 → Phase 3.5 → Phase 13 chain for the shared alerting-rules package is incomplete: Phase 2.5 produces the package; Phase 3.5 consumes it in-process for admission-plane alerts; Phase 13 is where the `PrometheusRule`/ConfigMap render becomes the shipping deployer artifact and the CI divergence guard (§18.1 line 88: "deployer-visible manifests and the in-process compiled rules never diverge") first becomes blocking. Without naming this at Phase 13, the divergence guard has no phase at which it is enforceable, mirroring the iter6 summary's Cross-Cutting Theme 1 ("Iter5 new-surface additions consistently miss catalog/index inclusion").

**Recommendation (unchanged from iter5 BLD-015 / iter6 BLD-017):** Extend Phase 13's deliverable list with an explicit clause: "Bundled alerting rules pipeline (§25.13): `docs/alerting/rules.yaml` generated from `pkg/alerting/rules`; Helm chart's `PrometheusRule`/ConfigMap template (controlled by `monitoring.format`) consumes the same source; CI gate fails the build if deployer-visible manifests diverge from the in-process compiled rules."

---

### BLD-021. §18 Phase 3.5 and §18.1 Phase assignments still cite "§17.9 row 501" for the `lenny-ops-sa` RBAC check, which is at row 503 [Low, carry-over of BLD-018 iter6]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) Phase 3.5 line 14 (Mandatory `lenny-ops` first-deploy paragraph) and §18.1 line 96 (Phase assignments — `lenny-ops` container image); [`spec/17_deployment-topology.md`](../../../spec/17_deployment-topology.md) §17.9 row 503 (actual `lenny-ops-sa` RBAC check) — row 501 is now "SIEM endpoint (warning)".

Two-iteration carry-over (iter6 BLD-018 → iter7 BLD-021). The iter6 fix cycle did not touch §18 and the two stale row-501 references remain in place. A reader following the cross-reference to §17.9 row 501 lands on the SIEM-endpoint warning rather than the `lenny-ops-sa` RBAC check.

Severity remains Low per the iter5 feedback rubric: the referent is unambiguous from the surrounding prose (both mentions inline-name the `lenny-ops-sa` RBAC preflight check), and the §17.8.5 cross-reference is correct — a reader who follows the row-501 pointer can recover the intended check by scanning the table. No runtime or install-time impact.

**Recommendation (unchanged from iter6 BLD-018):** Update both sites in `spec/18_build-sequence.md` to cite row 503 (or, more robustly, cite the row by its table key "`lenny-ops-sa` RBAC" rather than by numeric position, so future §17.9 additions do not require cross-file edits):

- Phase 3.5 paragraph (line 14): "§17.8.5 rejects any attempt to disable it, and §17.9's **`lenny-ops-sa` RBAC** preflight check is unconditional precisely because `lenny-ops` is mandatory from this phase".
- §18.1 line 96: "§17.8.5 rejects any attempt to disable `lenny-ops` at chart validation, and §17.9's **`lenny-ops-sa` RBAC** preflight check is unconditional precisely because `lenny-ops` is mandatory from this phase onward".

Prefer referring to §17.9 check rows by their human-readable table key rather than line/row number throughout §18, mirroring how §17.9 itself refers to other rows by name in its error-message column.

---

## Convergence assessment

**Status: Converged — no Critical, High, or Medium findings. Three Low findings (all carry-over from prior iterations) remain.**

Iter7 P19 finds no new Critical/High/Medium issues and no regressions introduced by the iter6 fix cycle. The iter6 fix commit (`8604ce9`) did not modify `spec/18_build-sequence.md`, so the three Low findings from iter6 (BLD-016/017/018) carry forward unchanged as BLD-019/020/021 with the same recommendations and severities.

**All three open findings are Low-severity polish items anchored to prior-iteration rubric:**

- **BLD-019** — four-iteration carry-over (iter4 BLD-010 → iter5 BLD-013 → iter6 BLD-016 → iter7 BLD-019). Low is consistent with the severity originally assigned in iter4 and reconfirmed in iter5 and iter6. Upgrading severity would violate the iter5 feedback rubric ("avoid severity drift that blocks convergence"); the gap does not cause correctness or security breakage.
- **BLD-020** — three-iteration carry-over (iter5 BLD-015 → iter6 BLD-017 → iter7 BLD-020). Low is consistent with iter5 and iter6 severity. The pipeline is specified; what is missing is a phase anchor.
- **BLD-021** — two-iteration carry-over (iter6 BLD-018 → iter7 BLD-021). Low per the iter5 rubric for cross-reference-staleness issues; no runtime/install impact; unambiguous referent.

**Per-finding summary:**

| Finding | Severity | Status | Carry-forward lineage |
|---|---|---|---|
| BLD-009 (iter4) | High | Fixed iter4, verified iter5/iter6/iter7 | — |
| BLD-010 (iter4) | Low | Carry-over → BLD-013 (iter5) → BLD-016 (iter6) → BLD-019 (iter7) | Unfixed across four iterations |
| BLD-011 (iter4) | High | Fixed iter4, verified iter5/iter6/iter7 | — |
| BLD-012 (iter5) | High | Fixed iter5, verified iter6/iter7 | Closed |
| BLD-013 (iter5) | Low | Carry-over → BLD-016 (iter6) → BLD-019 (iter7) | Continues iter4 BLD-010 |
| BLD-014 (iter5) | Medium | Fixed iter5, verified iter6/iter7 | Closed |
| BLD-015 (iter5) | Low | Carry-over → BLD-017 (iter6) → BLD-020 (iter7) | Unfixed across three iterations |
| BLD-016 (iter6) | Low | Carry-over → BLD-019 (iter7) | Continues iter5 BLD-013 / iter4 BLD-010 |
| BLD-017 (iter6) | Low | Carry-over → BLD-020 (iter7) | Continues iter5 BLD-015 |
| BLD-018 (iter6) | Low | Carry-over → BLD-021 (iter7) | Stale row-number reference |
| **BLD-019 (iter7)** | Low | **New (carry-over of BLD-016 / BLD-013 / BLD-010)** | — |
| **BLD-020 (iter7)** | Low | **New (carry-over of BLD-017 / BLD-015)** | — |
| **BLD-021 (iter7)** | Low | **New (carry-over of BLD-018)** | — |

**Convergence: Yes.** P19 has now held at "no C/H/M findings" for two consecutive iterations (iter6, iter7). The three open Low-severity findings are a mix of stable carry-overs (BLD-019 at iteration #4, BLD-020 at iteration #3) and a single iter6-introduced docs-sync lapse (BLD-021 at iteration #2). All three have clear, small recommendations that could be batched in a single docs-sync commit if the fix loop chooses to close them; none individually blocks convergence.

**Recommendation to the fix loop:** Since BLD-019/020/021 have each persisted across at least two iterations with identical, low-risk recommendations, the iter7 fix cycle could close them in a single small §18 patch:
1. Add `pkg/adapter/shared.go` (or the JSON Schema pair) to Phase 1's wire-contract artifact list (BLD-019).
2. Add an explicit bundled-alerting-rules pipeline clause to Phase 13's deliverable list (BLD-020).
3. Replace the two "§17.9 row 501" citations in §18 with "§17.9 `lenny-ops-sa` RBAC" row-key references (BLD-021).

These three edits, all to `spec/18_build-sequence.md` only, would fully close P19. No docs/ changes are required — §18 is an internal build-sequencing section not mirrored to docs/.
