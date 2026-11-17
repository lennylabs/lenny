# Decision records

Status: `Draft`, revision 1, 2026-11-16, with no independent review yet. Nothing here is built or staged. It records the owner's decision of 2026-11-16 to remove Architecture Decision Records (ADRs) from Lenny (D1), and lists every site that names them with the change each site takes. Changes under `changes/` and change proposals under `proposals/` become Lenny's decision records, and §19 stays as the summary table of resolved decisions. It reads against every design under `designs/` as implemented, in particular [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) revision 3, which renames the claim kind inside the §4.6.1 paragraph whose "ADR-007" label this design removes. The changes go through `change-proposal` after review. The design element carries the label REC-1, and requirements carry the label RECR-n. "Review history" at the end records the revisions.

Programs: [v1](../programs/v1.md). The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, the documentation, or a design under `designs/` on 2026-11-16, at commit `b622d716c`. |
| **[design]** | What this design requires. Nothing implements it. |

## Terms

| Term | Meaning |
| :-- | :-- |
| ADR | An Architecture Decision Record: a file under `docs/adr/` in the MADR 3.0.0 format, with a number, a status, and sections for context, drivers, options, outcome, and consequences. `docs/adr/index.md` is the catalog. |
| Planned ADR | A catalog row in `docs/adr/index.md` with a reserved number and the status `Planned`, and no file. |
| Design | A document under `designs/` that states a problem, the owner's numbered decisions with the owner's words, the current state with tagged sources, the specification and code changes it stages, the tests and documentation it owes, and a review history. Evidence a design rests on sits under `designs/evidence/<design>/`. |
| Change proposal | A directory under `proposals/` that stages specification edits and code changes for one change, with its problem statement, alternatives, review log, status, and implementation checklist. Its status is one of Draft, Reviewed, Approved, and Implemented, which `.claude/tools/proposal-status.mjs` reads and writes. An implemented proposal is never edited (`.claude/rules/spec-driven-development.md`). |
| Resolved decisions table | [§19 Resolved Decisions](../../spec/19_resolved-decisions.md#19-resolved-decisions), a numbered table that summarizes the platform's settled decisions. |
| Claim fencing verification | The §4.6.1 paragraph labelled **ADR-007: `SandboxClaim` optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite.**, which requires two tests showing that the API server's optimistic concurrency fences concurrent claims and a failed-over leader's writes. |

### Labels from other designs

| Label | Where | Meaning here |
| :-- | :-- | :-- |
| PLC-1, PLC-4 | [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) | PLC-1 deletes §4.6.1's dependency paragraphs, among them the go/no-go criteria that mention an ADR, and §18.40 item 2, which mentions one. PLC-4 renames `SandboxClaim` to `PodClaim` in the claim fencing verification paragraph, its label included, and rewrites its first sentence. |

## Problem

Lenny records decisions in four places **[source]**:

- §19 "Resolved Decisions", a summary table.
- `docs/adr/`, the MADR ADRs and their catalog.
- `proposals/`, the change proposals, each with its problem, alternatives, review log, and decision, which are never edited once implemented.
- `changes/`, one folder per change, each with its design, its numbered decisions in the owner's words, its review history, and an evidence folder.

The ADR catalog holds little of this record. Only ADR-0000 (the choice of MADR), ADR-0007, and ADR-0008 have files. ADR-0001 to ADR-0006 and ADR-0009 to ADR-0015 are `Planned` rows reserved since Phase 0, each pointing to a §19 row **[source]**.

`docs/adr/index.md` requires an ADR for a change that introduces or removes a CRD, changes the session, pod, or delegation state machine, alters the isolation model, changes the storage architecture, or modifies the credential model **[source]**. The designs make such changes without ADRs, and the record of each decision is the design itself **[source]**:

- [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) retires four CRDs and introduces three.
- [`0145-session-isolation`](../0145-session-isolation/change.md) changes how sessions that share one pod are isolated from each other, with a slot container under its own UID for each slot.
- [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) adds service accounts that own credentials and two credential sources.
- [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors and their OAuth flow, and adds an identity assertion that `netagent` attaches to requests.

The catalog has gone stale. ADR-0004 is titled "`kubernetes-sigs/agent-sandbox` + controller-runtime", which `0137-pod-lifecycle-controller` removes from the specification **[source]**. The two written decision ADRs repeat what the tree already carries:

- ADR-0007's decision is a verification requirement that §4.6.1's claim fencing verification states, and two tests enforce it: `TestLeaderKillMidClaimNoDoubleAssignment` in `tests/tier8_chaos/adr007_leader_kill_claim_test.go` and the concurrent-claim tests `TestDoubleClaimVerification` and `TestSandboxClaimRaceUnder100Goroutines` in `tests/tier8_chaos/concurrency_test.go` **[source]**. The options it rejected, a custom epoch field and a lease-serialized single writer, are recorded under "Current state".
- ADR-0008's decision is carried by the root `LICENSE` file (MIT) and §19 row 14, which lists the candidates and the criteria **[source]**.

Keeping ADRs means writing a third record of each decision that a design and a proposal already hold, and the catalog shows that the project has not done so.

## Scope

**Removed:**

- `docs/adr/` with its catalog, template, ADR-0000, ADR-0007, and ADR-0008.
- The ADR catalog checks: `scripts/check-adr-catalog.sh` and `tests/tier11_docs/adr_test.go`.
- Every requirement to write an ADR, and every statement that a decision is recorded as one, in the specification, the documentation, the root documents, the tests, and the code comments.

**Changed:**

- §19's closing paragraph names designs and change proposals as the full records of its decisions.
- The contributing and governance pages describe the design and change-proposal process in place of the ADR process.
- The test file and the comments named after ADR-007 are named for the claim fencing verification.

**Kept:**

- §19 as the summary table.
- The claim fencing verification paragraph of §4.6.1 and its two tests, under a label without "ADR-007: ".
- The records that state what was true when they were written: `proposals/`, `changes/archive/`, `BUILD-GAPS.md`, `TEST-GAPS.md`, `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, `CODE-REVIEW.md`, and `CHANGELOG.md` keep their words.

## High-level design

Lenny keeps two kinds of decision record and one summary. A change under `changes/` records a decision while it is being made: the problem, the options, the owner's answer in the owner's words, and the evidence. A change proposal under `proposals/` records the decision as it lands: the staged specification edits, the review, the approval, and the implementation. §19 lists the decisions that settle the platform's direction and points to the specification sections that carry them. `docs/adr/` goes, and so does every rule that asks for an ADR. The one specification paragraph that carries an ADR number in its label keeps its content and its two tests under a plain label.

## Decisions

| # | Question | Owner's answer | Applied in |
| :-- | :-- | :-- | :-- |
| D1 | Whether Lenny keeps Architecture Decision Records (2026-11-16). The owner said "Actually I've been wondering whether this whole ADR thing is needed at all", and was given the recommendation to remove ADRs through a separate short design and to take the ADR changes out of `0137-pod-lifecycle-controller`. | "ok" | The whole design. [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) records the same answer as its D9. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| RECR-1 | `docs/adr/` and `scripts/check-adr-catalog.sh` do not exist, and no test, script, or `lenny-test` stage reads either. |
| RECR-2 | No specification section, document under `docs/`, root document outside the records of "Scope", `.github/` template, `.claude/rules/` file, Go file, chart template, embedded manifest, or test register names an ADR, `docs/adr/`, or an ADR number, and none requires an ADR for any change. |
| RECR-3 | §19 states that a change under `changes/` or a change proposal under `proposals/` holds the full record of each decision in its table, and that a change to a decision goes through a change proposal. |
| RECR-4 | §4.6.1's claim fencing verification paragraph keeps its two tests and its fencing rationale, and its label and text name no ADR. |
| RECR-5 | The contributing and governance pages describe designs and change proposals as the decision records, with the proposal statuses and who approves a proposal. |

## Current state

**[source]** throughout.

| Area | What exists |
| :-- | :-- |
| `docs/adr/` | `index.md` (the catalog, `nav_order: 9`, `has_children: true`), `template.md`, `0000-use-madr-for-architecture-decisions.md`, `0007-sandboxclaim-optimistic-locking.md`, and `0008-open-source-license.md`. The catalog lists ADR-0000, ADR-0007, and ADR-0008 as `Accepted` and the rest as `Planned`, and its "When to write an ADR" list names CRD changes, state machine changes, isolation model changes, storage architecture changes, gateway subsystems, and the credential and RLS model. |
| ADR-0007 | Records Kubernetes optimistic locking as the claim fence, with an amendment of 2026-06-15 for per-pod claims. It rejected a custom generation or epoch field, because it duplicates `resourceVersion` and adds controller logic, and a lease-serialized single-writer claim path, because it puts the controller on the claim path and ties claim latency to the 25s failover window. Its confirmation section restates §4.6.1's two tests. |
| ADR-0008 | Records MIT, with candidates and drivers that §19 row 14 and §23.2 **Open-source license.** repeat. |
| Specification | [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle): the claim fencing verification paragraph ("Document the result as ADR-007", "the ADR records", "the ADR must specify", "ADR-007 must record the test results", "If ADR-007's chaos test confirms"); **Go/no-go criteria for the agent-sandbox dependency:**, item 3 ("the ADR-007 chaos test") and "The decision and its rationale are recorded as an ADR". [§6.3 Startup Latency Analysis](../../spec/06_warm-pod-model.md#63-startup-latency-analysis) **Tier 2 promotion gate:** ("the Phase 2 exit gate ADR"). [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos) **Startup warning requirement:** ("the Phase 14.5 exit gate ADR"). [§18.1 Phasing Intent](../../spec/18_build-sequence.md#181-phasing-intent) item 1 ("Repository bootstrap, ADRs,"). [§18.2 Phase Map](../../spec/18_build-sequence.md#182-phase-map) row 1 ("**Phase 0** (ADR-007)"). [§18.3 Phase 0](../../spec/18_build-sequence.md#183-phase-0--bootstrap-the-infrastructure-repo) **Deliverables.**: the ADR-007, ADR-008, and ADR template bullets; the template bullet names `docs/adr/README.md`, which does not exist. [§18.4 Phase 1](../../spec/18_build-sequence.md#184-phase-1--core-types-and-wire-contracts) **Prerequisites.** ("ADR-007 must be merged"). [§18.10 Phase 3.5](../../spec/18_build-sequence.md#1810-phase-35--admission-policies-lenny-ops-first-deploy) ("ADR-007 enforcement"). [§18.40 Hard Prerequisite Chain](../../spec/18_build-sequence.md#1840-hard-prerequisite-chain) items 1 and 2. [§19](../../spec/19_resolved-decisions.md#19-resolved-decisions) row 14 ("**Resolved — MIT (ADR-008).**", "Decision recorded as ADR-008 in `docs/adr/`", "Any change requires a superseding ADR.") and the closing paragraph ("full Architecture Decision Records (ADRs) ... will be maintained in `docs/adr/`"). [§23 Competitive Landscape](../../spec/23_competitive-landscape.md#23-competitive-landscape), the entrants matrix's License row ("MIT (ADR-008)"). [§23.2 Community Adoption Strategy](../../spec/23_competitive-landscape.md#232-community-adoption-strategy): **Decision records:**, **Governance artifact:** ("ADR requirement thresholds"), and **Open-source license.** ("*Resolved — MIT (ADR-008).*", "recorded as ADR-008 in `docs/adr/`; any change requires a superseding ADR"). |
| Documentation | `docs/about/contributing.md` ("Feature requests and design proposals", "ADR process" with its three subsections, and the "Communication" table's ADRs row); `docs/about/governance.md` ("What the BDfN decides", "Steering committee responsibilities", "During BDfN governance", "Architecture Decision Records (ADRs)" with its three subsections, the milestone table's Foundation row, the "Governance artifacts" table's ADRs row, and "Next steps"); `docs/about/status.md` (the "In design" row and the ADRs row); `docs/about/index.md` (the Architecture Decisions row); `docs/about/style-guide.md` (the `we` rule names ADR pages); `docs/getting-started/index.md` (the contributing row names the ADR process); `docs/testing/documentation-tests.md` (the description, the `adr_test.go` and `check-adr-catalog.sh` rows, "ADR catalog", and steps 1 and 5 of the authoring list); `docs/testing/index.md` (the tier 0 and tier 11 rows and the documentation tests link). |
| Root and repository files | `GOVERNANCE.md` (five sites), `ROADMAP.md` (one), `TESTING.md` ("3. Test layer model" tier 11 row; "12.0 Tier 0 — Static" item 11; "12.8 Tier 8 — Chaos" concurrency item; "12.11 Tier 11 — Documentation" item 4; "13.0 Phase 0" two items; "13.7 Phase 3.5" two items; "16. Documentation tests" item 7; "20.12 Branch protection" merge rule), `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/feature_request.md`, and `.claude/rules/doc-content.md` (the ADR escape hatch). |
| Scripts and harness | `scripts/check-adr-catalog.sh`, run by the tier 0 stage list in `cmd/lenny-test/cmd_run.go`. No `Makefile` target or `.github/` workflow names it. |
| Tests and registers | `tests/tier11_docs/adr_test.go` (`TestADRCatalogContinuity`, three skip sites in `tests/registers/skip-reasons.yaml`); `tests/tier8_chaos/adr007_leader_kill_claim_test.go` (two skip sites in the register, one path in `tests/spec-map.json`); `tests/tier11_docs/budget_extension_trigger_consistency_test.go` (asserts the ADR-0014 catalog row); `tests/tier11_docs/workspace_plan_binding_doc_reconciliation_test.go` (skips the `adr` directory); comments in `tests/tier8_chaos/concurrency_test.go`, `leader_election_test.go`, `tests/tier5_e2e_kind/crd_test.go` (with a failure message naming ADR-007), `tests/tier0_static/license_test.go`, `tests/tier9_security/scaffolds_test.go`, and `pkg/gateway/podlifecycle/podclaim/claimer_test.go`; `tests/spec-map.json` (the §4.6.1 and §19 notes); `tests/spec-map-exceptions.yaml` (the §19 and §20 justifications); and `tests/groups.yaml` (the Phase 0 description). |
| Code comments | `pkg/sandboxclaim/state/state.go`, `pkg/admission/sandboxclaim_guard/guard.go`, `pkg/admission/webhook/sandboxclaim_guard.go`, `cmd/lenny-webhook/main.go`, `pkg/gateway/podlifecycle/podclaim/claimer.go`, `charts/lenny/templates/admission-policies/sandboxclaim-guard-webhook.yaml`, `charts/lenny/templates/system-network-policies.yaml`, and `pkg/embedded/manifests/manifests.yaml` cite ADR-007. |
| Designs | Revision 3 of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) names `docs/adr/` only to leave it to this design. At commit `b622d716c`, [`0127-egress-and-git`](../0127-egress-and-git/change.md) and [`0142-security-oversight`](../0142-security-oversight/change.md) each carry a documentation row `adr/` planning a new ADR. |
| Proposals | Approved proposals 0002 and 0023 staged edits to `docs/adr/` (an ADR-0007 note and the ADR-0014 row), and the tree carries both. Several implemented proposals mention ADRs. All of them stay as written. |

## REC-1 Removal

**Specification.** §19 stays as the summary table, and its rows that cite an ADR cite the `LICENSE` file or the section that carries the decision. The claim fencing verification of §4.6.1 keeps its requirement, its tests, and its rationale, and states the outcome as the tests' result rather than as a document. Phase gates that attached results to an ADR attach them to the gate **[design]**. "Specification changes" lists each edit.

**Documentation and repository.** `docs/adr/` is deleted with its five files, and the documentation site's navigation loses the "Architecture Decisions" entry. The contributing and governance pages replace the ADR process with this text **[design]**:

> Lenny records decisions in two places. A design under `designs/` records a decision while it is made: the problem, the options, the maintainer's decision in the maintainer's words, the evidence under `designs/evidence/`, and a review history. A change proposal under `proposals/` stages the specification edits and code changes that carry out a decision, and moves through the statuses Draft, Reviewed, Approved, and Implemented. An implemented proposal is not edited, so it stays as the record of what was decided and when. A change to the platform's architecture, such as a new or removed custom resource, a state machine change, or an isolation or credential model change, starts as a design or a change proposal before code. The maintainer approves designs and proposals today. After the transition to a steering committee, the committee approves them. [§19 of the specification](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) summarizes the resolved decisions.

**Tests and tooling.** The catalog check goes, the leader-kill test file is renamed `tests/tier8_chaos/leader_kill_claim_test.go` with its function name unchanged, and every comment and message that cites ADR-007 cites §4.6.1's claim fencing verification **[design]**.

**Records.** The records of "Scope" keep their words. `CHANGELOG.md` gains an entry for the removal.

## Specification changes

Each edit locates its target by heading, bold paragraph label, and quoted text. No heading changes, so no anchor retires.

| Section | Change |
| :-- | :-- |
| [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), the paragraph whose label ends "optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite." | Drop "ADR-007: " from the start of the label and keep the rest, whatever kind name it carries when this lands. Delete "Document the result as ADR-007." In test 1, "the ADR records \"optimistic locking confirmed, no compensation needed.\" If it does not, the ADR must specify the compensating CAS loop design before Phase 1 proceeds" becomes "the result is \"optimistic locking confirmed, no compensation needed.\" If it does not, a change proposal specifies the compensating CAS loop design before Phase 1 proceeds". In test 2, "ADR-007 must record the test results and explicitly accept or reject the \"optimistic locking is sufficient fencing\" hypothesis before Phase 1 proceeds" becomes "The test results accept or reject the \"optimistic locking is sufficient fencing\" hypothesis before Phase 1 proceeds". In **Why optimistic locking provides sufficient fencing:**, "If ADR-007's chaos test confirms" becomes "If the chaos test confirms". The first sentence and the kind name are PLC-4's edit, so this row and PLC-4 apply in either order. |
| §4.6.1, **Go/no-go criteria for the agent-sandbox dependency:** | PLC-1 deletes the paragraph, with item 3's "the ADR-007 chaos test" and "The decision and its rationale are recorded as an ADR". If this design lands first, it makes the two smaller edits: "the ADR-007 chaos test" becomes "the claim fencing chaos test", and the ADR sentence is deleted. |
| [§6.3 Startup Latency Analysis](../../spec/06_warm-pod-model.md#63-startup-latency-analysis), **Tier 2 promotion gate:** | "attached to the Phase 2 exit gate ADR" becomes "attached to the Phase 2 exit gate". |
| [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), **Startup warning requirement:** | "records benchmark results to the Phase 14.5 exit gate ADR" becomes "records benchmark results with the Phase 14.5 exit gate". |
| [§18.1 Phasing Intent](../../spec/18_build-sequence.md#181-phasing-intent), item 1 | "Repository bootstrap, ADRs, core domain types" becomes "Repository bootstrap, core domain types". |
| [§18.2 Phase Map](../../spec/18_build-sequence.md#182-phase-map), row 1 | "**Phase 0** (ADR-007)" becomes "**Phase 0** (claim fencing verification)". |
| [§18.3 Phase 0](../../spec/18_build-sequence.md#183-phase-0--bootstrap-the-infrastructure-repo), **Deliverables.** | The ADR-007 bullet becomes "The two claim fencing verification tests of §4.6.1 (the concurrent-claim integration test and the leader-kill chaos test) are stubbed; the chaos test executes against the WarmPoolController binary that ships in Phase 3." The ADR-008 bullet becomes "`LICENSE` (MIT, per Resolved Decision 14 in §19) at the repository root." The bullet naming the ADR template and `docs/adr/README.md` is deleted. |
| [§18.4 Phase 1](../../spec/18_build-sequence.md#184-phase-1--core-types-and-wire-contracts), **Prerequisites.** | "ADR-007 must be merged before Phase 1 implementation begins, per the verification requirement in §4.6.1" becomes "The claim fencing verification of §4.6.1 must be settled before Phase 1 implementation begins". |
| [§18.10 Phase 3.5](../../spec/18_build-sequence.md#1810-phase-35--admission-policies-lenny-ops-first-deploy) | "(ADR-007 enforcement: CREATE rejection" becomes "(§4.6.1 claim fencing enforcement: CREATE rejection". |
| [§18.40 Hard Prerequisite Chain](../../spec/18_build-sequence.md#1840-hard-prerequisite-chain) | Item 1 becomes "**Phase 0 → Phase 1 (claim fencing verification).** The claim fencing verification of §4.6.1 must be settled before Phase 1 implementation begins. Its two tests (the concurrent-claim integration test and the leader-kill chaos test) are required before the verification hypothesis is treated as accepted." PLC-1 deletes item 2. If this design lands first, item 2's "and the decision is recorded as an ADR" is deleted. |
| [§19 Resolved Decisions](../../spec/19_resolved-decisions.md#19-resolved-decisions), row 14 | "**Resolved — MIT (ADR-008).**" becomes "**Resolved — MIT.**". "Decision recorded as ADR-008 in `docs/adr/`; the `LICENSE` file (MIT) is committed at the repository root. Any change requires a superseding ADR." becomes "The `LICENSE` file (MIT) at the repository root carries the decision. Any change goes through a change proposal." The license criteria are PLC-1's edit. |
| §19, the paragraph after the table | Replace with "Each decision above is a summary. The change under `changes/` or the change proposal under `proposals/` that made a decision records its context, the alternatives considered, and the reasoning, and the specification section the row points to states the behavior. A change to a decision above goes through a change proposal." |
| [§23 Competitive Landscape](../../spec/23_competitive-landscape.md#23-competitive-landscape), the License row of the entrants matrix | "MIT (ADR-008)" becomes "MIT". |
| [§23.2 Community Adoption Strategy](../../spec/23_competitive-landscape.md#232-community-adoption-strategy), **Decision records:** | Becomes "**Decision records:** Architectural decisions are recorded in changes under `changes/` and change proposals under `proposals/`, and [Section 19](19_resolved-decisions.md) summarizes the resolved ones. A community-proposed change to the architecture starts as a design or a change proposal with its alternatives." |
| §23.2, **Governance artifact:** | "decision-making process, ADR requirement thresholds, and the license/CLA policy" becomes "decision-making process, the design and change-proposal process, and the license/CLA policy". |
| §23.2, **Open-source license.** | "*Resolved — MIT (ADR-008).*" becomes "*Resolved — MIT.*". "The decision and rationale are recorded as ADR-008 in `docs/adr/`; any change requires a superseding ADR." becomes "The `LICENSE` file carries the decision, and any change goes through a change proposal." The criterion naming upstream is PLC-1's edit. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `docs/adr/` | Deleted: `index.md`, `template.md`, `0000-use-madr-for-architecture-decisions.md`, `0007-sandboxclaim-optimistic-locking.md`, and `0008-open-source-license.md`. |
| `scripts/check-adr-catalog.sh` | Deleted. |
| `cmd/lenny-test/cmd_run.go` | The tier 0 stage `scripts/check-adr-catalog.sh` is removed. |
| `tests/tier11_docs/adr_test.go` | Deleted, with its three entries in `tests/registers/skip-reasons.yaml`. |
| `tests/tier8_chaos/adr007_leader_kill_claim_test.go` | Renamed `tests/tier8_chaos/leader_kill_claim_test.go`. `TestLeaderKillMidClaimNoDoubleAssignment` keeps its name. Its comments, `// diagnosis:`, and failure messages cite "§4.6.1 claim fencing verification" in place of ADR-007. Its two `tests/registers/skip-reasons.yaml` entries and its `tests/spec-map.json` path follow the rename. |
| `tests/tier8_chaos/concurrency_test.go`, `leader_election_test.go`, `tests/tier5_e2e_kind/crd_test.go`, `tests/tier9_security/scaffolds_test.go`, and `pkg/gateway/podlifecycle/podclaim/claimer_test.go` | Comments, `// diagnosis:` text, and the `crd_test.go` failure message cite §4.6.1's claim fencing verification in place of ADR-007 or ADR-0007. |
| `tests/tier0_static/license_test.go` | The comments cite the `LICENSE` file and §19 row 14 in place of ADR-0008. |
| `tests/tier11_docs/budget_extension_trigger_consistency_test.go` | The block that reads `docs/adr/index.md` and asserts the ADR-0014 row is removed, and the `// diagnosis:` drops the ADR-0014 index row. The other assertions stay. |
| `tests/tier11_docs/workspace_plan_binding_doc_reconciliation_test.go` | The skip of the `adr` directory and its comment are removed. |
| `tests/spec-map.json` | The §4.6.1 note drops "ADR-007:", and the §19 note reads "Each entry is recorded by a design or a change proposal. Tests are attached to the relevant feature section." |
| `tests/spec-map-exceptions.yaml` | The §19 justification reads "Resolved decisions are recorded by designs and change proposals. Each decision is covered by the test surface of the feature it codifies." The §20 justification drops "Re-validated nightly via the ADR catalog check." |
| `tests/groups.yaml` | The Phase 0 description drops "ADRs,". |
| Go comments and chart templates | `pkg/sandboxclaim/state/state.go`, `pkg/admission/sandboxclaim_guard/guard.go`, `pkg/admission/webhook/sandboxclaim_guard.go`, `cmd/lenny-webhook/main.go`, `pkg/gateway/podlifecycle/podclaim/claimer.go`, `charts/lenny/templates/admission-policies/sandboxclaim-guard-webhook.yaml`, and `charts/lenny/templates/system-network-policies.yaml` cite §4.6.1's claim fencing verification in place of ADR-007. `pkg/embedded/manifests/manifests.yaml` follows the chart through the generator that `embedded_manifests_sync_test.go` checks. File names follow whatever kind name the tree carries when this lands. |
| `.claude/rules/doc-content.md` | The escape hatch "ADRs and other historical records describe a decision at a point in time" becomes "Historical records, such as an implemented change proposal or a design's review history, describe a decision at a point in time". |

## Documentation

| File | Change |
| :-- | :-- |
| `docs/about/contributing.md` | "Feature requests and design proposals" ends with "Architectural changes start as a design or a change proposal. See Design and change proposals below.", where "Design and change proposals" links to that section. The "ADR process" section is renamed "Design and change proposals", and its subsections are replaced by the text of "REC-1". The "Communication" table's ADRs row becomes a "Designs and proposals" row linking `designs/` and `proposals/` on GitHub. |
| `docs/about/governance.md` | "ADR approval or rejection" and "ADR review and approval" become "Approval of designs and change proposals". "During BDfN governance" items 1 and 3 name designs and change proposals in place of draft ADRs and the relevant ADR. The section "Architecture Decision Records (ADRs)" is renamed "Decision records" and carries the text of "REC-1", with the community path: a community member opens a Discussion with the proposal and its alternatives, and the maintainer or the steering committee answers within 2 weeks with the decision and its reasoning, which a design or a change proposal then records. The Foundation milestone row reads "License (MIT), the claim fencing verification, repository setup, branch-protection and DCO policy, CI pipeline." The "Governance artifacts" table's ADRs row becomes "Designs and change proposals, `designs/` and `proposals/`, ongoing". "Next steps" drops "ADRs". |
| `GOVERNANCE.md` | Line 3 names the decision-record process in place of the ADR process. The contributor criterion names design reviews. "Proposals" are filed as GitHub Discussions, designs, or change proposals. "Decisions" are documented in the design, the change proposal, or the issue. The paragraph requiring an ADR points to the "Decision records" section of `docs/about/governance.md`. |
| `ROADMAP.md` | "Large scope changes go through an ADR in `docs/adr/`" becomes "Large scope changes go through a design or a change proposal". |
| `docs/about/status.md` | The "In design" row reads "Design work in progress. No code yet." The ADRs row is removed. |
| `docs/about/index.md` | The Architecture Decisions row is removed. |
| `docs/about/style-guide.md` | The `we` rule reads "only in governance and contributing pages". |
| `docs/getting-started/index.md` | The contributing row reads "Development workflow, code conventions, the design and proposal process, and the canonical spec." |
| `docs/testing/documentation-tests.md` | The description drops "ADR continuity". The `adr_test.go` and `check-adr-catalog.sh` rows and the "ADR catalog" section are removed. Authoring step 1 drops the ADR path, and step 5 is removed. |
| `docs/testing/index.md` | The tier 0 row drops "ADR catalog", the tier 11 row drops "ADR continuity", and the documentation tests link drops "ADR". |
| `TESTING.md` | The tier 11 row of "3. Test layer model" drops "ADR catalog". "12.0 Tier 0 — Static" item 11 is removed. "12.8 Tier 8 — Chaos" names "double-claim verification (§4.6.1 claim fencing)". "12.11 Tier 11 — Documentation" item 4 is removed. "13.0 Phase 0" reads "`LICENSE` (MIT) at repo root" and drops the ADR catalog cross-check. "13.7 Phase 3.5" cites §4.6.1 alone. "16. Documentation tests" item 7 is removed. "20.12 Branch protection" reads "No merge without a design or an approved change proposal for changes that require one (per the contributing guide)." |
| `.github/PULL_REQUEST_TEMPLATE.md` | "Link the issue, discussion, or ADR" becomes "Link the issue, discussion, design, or change proposal". |
| `.github/ISSUE_TEMPLATE/feature_request.md` | "If the change is large enough to need an ADR, note that here." becomes "If the change is large enough to need a design or a change proposal, note that here." |
| `CHANGELOG.md` | States that `docs/adr/` and the ADR catalog checks are removed, that designs and change proposals are the decision records, and that §19 points to them. |

The documentation site loses the "Architecture Decisions" navigation entry with `docs/adr/index.md`. No other page sets `parent: "Architecture Decisions"` **[source]**. Operators and runtime authors meet no change in behaviour, setting, default, or failure mode.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | A repository check finds no `docs/adr/`, no `scripts/check-adr-catalog.sh`, and no `ADR-` number, `docs/adr` path, or "Architecture Decision Record" outside the records of "Scope" and `changes/` (RECR-1, RECR-2). |
| 8 | `TestLeaderKillMidClaimNoDoubleAssignment` runs from `leader_kill_claim_test.go`, and the concurrent-claim tests run unchanged (RECR-4). |
| 11 | The link check finds no link into `docs/adr/` from `docs/`, the root documents, or `.github/`. A check finds §19's closing paragraph naming `changes/` and `proposals/` (RECR-3), and finds the text of "REC-1" in `docs/about/contributing.md` and `docs/about/governance.md` (RECR-5). |

Every test carries the `// spec:` annotation for the section it verifies, and tests at tier 2 and above carry `// diagnosis:`.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Build the documentation site with `docs/adr/` removed and confirm that the navigation renders and that no page links into it. | The site's navigation reads `nav_order` and `parent` from the deleted files. | Open, for REC-B. |
| Confirm that `lenny-test --tier static` passes with the stage removed and that no other harness path calls the script. | `cmd/lenny-test/cmd_run.go` is the one caller found **[source]**. | Open, for REC-B. |

## Instructions for other designs

- [`0127-egress-and-git`](../0127-egress-and-git/change.md): the documentation row `adr/`, "A new ADR records the decisions in this design. The ADR index states that a change to the credential model warrants one.", is removed. The design is its own decision record.
- [`0142-security-oversight`](../0142-security-oversight/change.md): the documentation row `adr/`, "A new ADR records the decisions here.", is removed.
- [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md): revision 3 already leaves every ADR site to this design. PLCR-1 and PLCR-11 exempt `docs/adr/` until this design deletes it.

## Steps

### 0125.S1 Specification changes
- status: Planned
- aliases: [REC-A, REC-1 (part)]
- lanes: [specification]

The specification changes. PLC-A of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) edits the same §4.6.1 paragraph, §19 row 14, and §23.2 paragraph in other phrases, so either lands first. The rows under "Specification changes" state what each section reads under either order.

### 0125.S2 Deletions, renames, comments, tests, registers, and documentation
- status: Planned
- aliases: [REC-B, REC-1 (part)]
- lanes: [code, documentation]
- depends-on: [0125.S1]

The deletions, the renames, the comments, the tests, the registers, and the documentation. It lands after 0125.S1 so every `// spec:` citation and every document points to the new text.
