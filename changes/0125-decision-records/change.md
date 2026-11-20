# Decision records

Status: `Draft`, revision 2, 2026-11-19. Programs: [v1](../programs/v1.md).

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, the documentation, or a design under `designs/` on 2026-11-16, at commit `b622d716c`. Revision 2 re-read the sites it corrects on 2026-11-19, at commit `092ef92f6`, and "Current state" records what it found. |
| **[design]** | What this design requires. Nothing implements it. |

## Terms

| Term | Meaning |
| :-- | :-- |
| ADR | An Architecture Decision Record: a file under `docs/adr/` in the MADR 3.0.0 format, with a number, a status, and sections for context, drivers, options, outcome, and consequences. `docs/adr/index.md` is the catalog. |
| Planned ADR | A catalog row in `docs/adr/index.md` with a reserved number and the status `Planned`, and no file. |
| Change | A folder under `changes/` that states a problem, the owner's numbered decisions with the owner's words, the current state with tagged sources, the design, the steps that land the specification, the code, and the documentation, and a review history, with the evidence it rests on under its `evidence/` folder (`changes/README.md`). A change that is implemented or cancelled moves to `changes/archive/` and is not edited again. Change 0113 moved the designs that `designs/` held into `changes/`, and `designs/` no longer exists. |
| Change proposal | A file or directory under `proposals/` written before `changes/` existed, with its problem statement, alternatives, review log, status, and implementation checklist. An implemented proposal is a historical record and is never edited (`.claude/rules/spec-driven-development.md`). The open proposals are outside the v1 program until the owner returns to them (`changes/programs/v1.md`, "Left out for now"), and a new decision does not start as a change proposal. |
| Resolved decisions table | [§19 Resolved Decisions](../../spec/19_resolved-decisions.md#19-resolved-decisions), a numbered table that summarizes the platform's settled decisions. It is transitional: under decision 9 of change 0115, the area changes distribute its rows to the Rationale sections of the owner pages as they reach each item, and §19 then becomes a successor stub. |
| Claim fencing verification | The §4.6.1 paragraph labelled **ADR-007: `PodClaim` optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite.**, which requires two tests showing that the API server's optimistic concurrency fences concurrent claims and a failed-over leader's writes. On 2026-11-16 the label named `SandboxClaim`; 0137.S5 renamed the kind. |
| Labels | The part of the design carries the label REC-1, and requirements carry the label RECR-n. |

### Labels from other designs

| Label | Where | Meaning here |
| :-- | :-- | :-- |
| PLC-1, PLC-4 | [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) | PLC-1 deletes §4.6.1's dependency paragraphs, among them the go/no-go criteria that mention an ADR, and §18.40 item 2, which mentions one. PLC-4 renames `SandboxClaim` to `PodClaim` in the claim fencing verification paragraph, its label included, and rewrites its first sentence. 0137.S5 has landed the kind rename, so the label already names `PodClaim`; the first-sentence rewrite is part of 0137.S1, which is `Planned`. |

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

- ADR-0007's decision is a verification requirement that §4.6.1's claim fencing verification states, and two tests enforce it: `TestLeaderKillMidClaimNoDoubleAssignment` in `tests/chaos/adr007_leader_kill_claim_test.go` and the concurrent-claim tests `TestDoubleClaimVerification` and `TestPodClaimRaceUnder100Goroutines` (named `TestSandboxClaimRaceUnder100Goroutines` on 2026-11-16) in `tests/chaos/concurrency_test.go` **[source]**. The options it rejected, a custom epoch field and a lease-serialized single writer, are recorded under "Current state".
- ADR-0008's decision is carried by the root `LICENSE` file (MIT) and §19 row 14, which lists the candidates and the criteria **[source]**.

Keeping ADRs means writing a second record of each decision that a change already holds, and the catalog shows that the project has not done so.

## Scope

**Removed:**

- `docs/adr/` with its catalog, template, ADR-0000, ADR-0007, and ADR-0008.
- The ADR catalog checks: `scripts/check-adr-catalog.sh` and `tests/docs/adr_test.go`.
- Every requirement to write an ADR, and every statement that a decision is recorded as one, in the specification, the documentation, the root documents, the tests, and the code comments.

**Changed:**

- While §19 exists, its closing paragraph names the changes under `changes/` as the full records of its decisions.
- The contributing and governance pages describe the change process in place of the ADR process.
- The test file and the comments named after ADR-007 are named for the claim fencing verification.

**Kept:**

- §19's rows, with their ADR wording removed, until the area changes distribute them under decision 9 of change 0115. This change does not make §19 permanent.
- The claim fencing verification paragraph of §4.6.1 and its two tests, under a label without "ADR-007: ". Where the paragraph's content goes when the runtimes-and-pools area migrates §4.6.1 is that area change's edit, which the migration ledger of change 0115 describes (`changes/archive/2026-11-16-0115-spec-structure/evidence/spike-migration/spec-next/MIGRATION-LEDGER.md`, the §4.6.1 row for this label: the two tests move to `TESTING.md`, and the fencing argument stays as the rationale of a requirement on the `CLAIM` page).
- The records that state what was true when they were written: `proposals/`, `changes/archive/`, `issues/`, `BUILD-GAPS.md`, `TEST-GAPS.md`, `CODE-REVIEW.md`, and `CHANGELOG.md` keep their words. `BUILD-PLAN.md` and `BUILD-PROGRESS.md`, which revision 1 listed, were removed by 0115.S9.

## High-level design

Lenny records each decision in a change under `changes/`: the problem, the options, the owner's answer in the owner's words, the evidence, the steps that land the specification, the code, and the documentation, and the review history. An implemented change moves to `changes/archive/`, and the specification page that owns the behavior carries the decision's rationale. The change proposals under `proposals/` are historical records written before `changes/` existed. While §19 exists, it lists the decisions that settle the platform's direction and points to the specification sections that carry them, and the area changes distribute it under decision 9 of change 0115. `docs/adr/` goes, and so does every rule that asks for an ADR. The one specification paragraph that carries an ADR number in its label keeps its content and its two tests under a plain label.

## Requirements

| ID | Requirement |
| :-- | :-- |
| RECR-1 | `docs/adr/` and `scripts/check-adr-catalog.sh` do not exist, and no test, script, or `lenny-test` stage reads either. |
| RECR-2 | No specification section, document under `docs/`, root document outside the records of "Scope", `.github/` template, `.claude/rules/` file, Go file, chart template, embedded manifest, or test register names an ADR, `docs/adr/`, or an ADR number, and none requires an ADR for any change. |
| RECR-3 | While §19 exists, it states that the change under `changes/` that made a decision, or for an earlier decision the change proposal under `proposals/`, holds the full record of the decision, and that a change to a decision goes through a change under `changes/`. The requirement lapses for a row that an area change distributes and ends when §19 becomes a successor stub (decision 9 of change 0115). |
| RECR-4 | While the claim fencing verification paragraph is in §4.6.1, it keeps its two tests and its fencing rationale, and its label and text name no ADR. When the runtimes-and-pools area migrates §4.6.1, the paragraph's content goes where that area change places it. |
| RECR-5 | The contributing and governance pages describe the changes under `changes/` as the decision records, with the change statuses of `changes/README.md` ("Status") and who approves a change, and they describe `proposals/` and `changes/archive/` as historical records. |

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
| Tests and registers | `tests/docs/adr_test.go` (`TestADRCatalogContinuity`, three skip sites in `tests/registers/skip-reasons.yaml`); `tests/chaos/adr007_leader_kill_claim_test.go` (two skip sites in the register, one path in `tests/spec-map.json`); `tests/docs/budget_extension_trigger_consistency_test.go` (asserts the ADR-0014 catalog row); `tests/docs/workspace_plan_binding_doc_reconciliation_test.go` (skips the `adr` directory); comments in `tests/chaos/concurrency_test.go`, `leader_election_test.go`, `tests/e2e_kind/crd_test.go` (with a failure message naming ADR-007), `tests/static/license_test.go`, `tests/security/scaffolds_test.go`, and `pkg/gateway/podlifecycle/podclaim/claimer_test.go`; `tests/spec-map.json` (the §4.6.1 and §19 notes); `tests/spec-map-exceptions.yaml` (the §19 and §20 justifications); and `tests/groups.yaml` (the Phase 0 description). |
| Code comments | `pkg/sandboxclaim/state/state.go`, `pkg/admission/sandboxclaim_guard/guard.go`, `pkg/admission/webhook/sandboxclaim_guard.go`, `cmd/lenny-webhook/main.go`, `pkg/gateway/podlifecycle/podclaim/claimer.go`, `charts/lenny/templates/admission-policies/sandboxclaim-guard-webhook.yaml`, `charts/lenny/templates/system-network-policies.yaml`, and `pkg/embedded/manifests/manifests.yaml` cite ADR-007. |
| Designs | Revision 3 of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) names `docs/adr/` only to leave it to this design. At commit `b622d716c`, [`0127-egress-and-git`](../0127-egress-and-git/change.md) and [`0142-security-oversight`](../0142-security-oversight/change.md) each carry a documentation row `adr/` planning a new ADR. |
| Proposals | Approved proposals 0002 and 0023 staged edits to `docs/adr/` (an ADR-0007 note and the ADR-0014 row), and the tree carries both. Several implemented proposals mention ADRs. All of them stay as written. |

The table above records the tree on 2026-11-16. On 2026-11-19, at commit `092ef92f6`, the tree differs as follows **[source]**:

- §18 and §23 are successor stubs (0115.S9). `changes/programs/v1.md` ("Retired records and sections") applies an edit to a stub to its successor, and the successors (`docs/about/status.md`, `docs/about/comparisons.md`, `docs/about/governance.md`, and `docs/about/contributing.md`) carry no ADR text beyond the sites "Documentation" lists, so the §18 and §23 edits are moot. The specification sites that remain are the claim fencing verification paragraph and the go/no-go paragraph of §4.6.1, §6.3 **Tier 2 promotion gate:**, §16.5 **Startup warning requirement:**, and §19 row 14 and its closing paragraph.
- The kind rename of 0137.S5 and the package moves of change 0117 changed these paths: `pkg/sandboxclaim/state/state.go` is `pkg/podclaim/state/state.go`; `pkg/admission/sandboxclaim_guard/guard.go` is `pkg/admission/podclaim_guard/guard.go`; `pkg/admission/webhook/sandboxclaim_guard.go` is `pkg/admission/webhook/podclaim_guard.go`; `charts/lenny/templates/admission-policies/sandboxclaim-guard-webhook.yaml` is `podclaim-guard-webhook.yaml` in the same directory; `pkg/gateway/podlifecycle/podclaim/claimer.go` and `claimer_test.go` are under `pkg/gateway/core/podclaim/`; `tests/static/license_test.go` is `tests/static/build/license_test.go`; and `TestSandboxClaimRaceUnder100Goroutines` is `TestPodClaimRaceUnder100Goroutines`.
- `tests/spec-map.json` and `tests/spec-map-exceptions.yaml` were retired by 0116.S6, and `ROADMAP.md`, `BUILD-PLAN.md`, and `BUILD-PROGRESS.md` were removed by 0115.S9.
- `cmd/lenny-test/cmd_gate.go` lists `"adr_test.go"` in `gateDocsFiles`, the general documentation gates of the merge gate, and its comment names "ADRs". `gateDocsTestNames` returns an error for a listed file that is missing, so the merge gate cannot pass on a branch that deletes `tests/docs/adr_test.go` and leaves the list unchanged. `TESTING.md` ("Merge gate") names "the ADR catalog" among those gates.
- `tests/static/lint/pod_lifecycle_kind_names_test.go`, added by 0137.S5, exempts the prefix `docs/adr/` in `retiredNameExemptPrefixes`, pins it in `TestRetiredNameExemptionsArePLCR11List` (`wantPrefixes`), and states in a comment that the exemption holds "until change 0125 removes them".
- `TESTING.md` names its tiers as change 0155 renamed them: "12.0 Static (`static`)", "12.8 Chaos (`chaos`)", and "12.11 Documentation (`docs`)", and the "3. Test layer model" row for documentation is the `docs` row.
- `GOVERNANCE.md` names an ADR on six lines, including the heading "## Architecture Decision Records" and a link to `docs/about/governance.md#architecture-decision-records-adrs`.
- `docs/about/governance.md` has no "Foundation" milestone row. "Milestone-gated development" points to the v1 program.
- `pkg/gateway/core/podclaim/claimer_test.go` carries the annotation `// spec: §4.6.1 ADR-007 — ...`, so its replacement has to be a conforming `// spec:` annotation.
- `BUILD-GAPS.md` F-6.3.20 and `TEST-GAPS.md` T-6.3.20 are open, and each names the "Phase 2 exit gate ADR" as missing.

## Detailed design

### REC-1 Removal

**Specification.** The §19 edits are transitional: they remove the ADR wording from row 14 and the closing paragraph, and the area changes distribute §19 under decision 9 of change 0115. The `LICENSE` file carries the license decision, and `docs/about/governance.md` ("License") states it. The claim fencing verification of §4.6.1 keeps its requirement, its tests, and its rationale, and states the outcome as the tests' result rather than as a document. Where its content goes when the runtimes-and-pools area migrates §4.6.1 is that area change's edit, as "Scope" states. Benchmark results that the specification attached to an ADR are recorded in the evidence folder of the change that runs the benchmark **[design]**. "Specification changes" lists each edit.

**Documentation and repository.** `docs/adr/` is deleted with its five files, and the documentation site's navigation loses the "Architecture Decisions" entry. The contributing and governance pages replace the ADR process with this text **[design]**:

> Lenny records each decision in a change under `changes/`, one folder per change. A change records the problem, the options, the maintainer's decision in the maintainer's words, the evidence, and a review history, and its steps land the specification edits, the code, and the documentation that carry out the decision. A change moves through the statuses Draft, Decided, In progress, and Implemented, or is Cancelled. An implemented or cancelled change moves to `changes/archive/` and is not edited, so it stays as the record of what was decided and when, and the specification page that owns the behavior states the decision's rationale. The change proposals under `proposals/` are earlier records of the same kind and are not edited. A change to the platform's architecture, such as a new or removed custom resource, a state machine change, or an isolation or credential model change, starts as a change before code. The maintainer settles the decisions of a change today. After the transition to a steering committee, the committee settles them.

**Tests and tooling.** The catalog check goes, the leader-kill test file is renamed `tests/chaos/leader_kill_claim_test.go` with its function name unchanged, and every comment and message that cites ADR-007 cites §4.6.1 in a form `.claude/rules/spec-annotations.md` allows **[design]**. A `// spec:` annotation reads `§4.6.1 (Warm Pool Controller (Pod Lifecycle))`, or cites the `CLAIM` requirement once the claim page has landed. A prose comment, a `// diagnosis:` line, or a failure message names the bold label "`PodClaim` optimistic-locking and failover fencing verification" of §4.6.1.

**Records.** The records of "Scope" keep their words. `CHANGELOG.md` gains an entry for the removal.

## Specification changes

Each edit locates its target by heading, bold paragraph label, and quoted text. No heading changes, so no anchor retires. When an area change has migrated a section below before 0125.S1 lands, the edit applies to the page that now carries the text, as the section's successor stub states, and an edit with no successor is moot (`changes/programs/v1.md`, "Retired records and sections").

Revision 1 also listed edits to §18.1, §18.2, §18.3, §18.4, §18.10, §18.40, the License row of the §23 entrants matrix, and the **Decision records:**, **Governance artifact:**, and **Open-source license.** paragraphs of §23.2. 0115.S9 turned §18 and §23 into successor stubs, and the successors carry no ADR text beyond the sites "Documentation" lists, so revision 2 drops those rows as moot.

| Section | Change |
| :-- | :-- |
| [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), the paragraph whose label ends "optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite." | Drop "ADR-007: " from the start of the label and keep the rest, whatever kind name it carries when this lands. Delete "Document the result as ADR-007." In test 1, "the ADR records \"optimistic locking confirmed, no compensation needed.\" If it does not, the ADR must specify the compensating CAS loop design before Phase 1 proceeds" becomes "the result is \"optimistic locking confirmed, no compensation needed.\" If it does not, a change under `changes/` specifies the compensating CAS loop design before Phase 1 proceeds". The edit removes the ADR wording only; the phase wording that remains leaves with the paragraph when the runtimes-and-pools area migrates §4.6.1, as "Scope" states. In test 2, "ADR-007 must record the test results and explicitly accept or reject the \"optimistic locking is sufficient fencing\" hypothesis before Phase 1 proceeds" becomes "The test results accept or reject the \"optimistic locking is sufficient fencing\" hypothesis before Phase 1 proceeds". In **Why optimistic locking provides sufficient fencing:**, "If ADR-007's chaos test confirms" becomes "If the chaos test confirms". The first sentence and the kind name are PLC-4's edit, so this row and PLC-4 apply in either order. |
| §4.6.1, **Go/no-go criteria for the agent-sandbox dependency:** | PLC-1 deletes the paragraph, with item 3's "the ADR-007 chaos test" and "The decision and its rationale are recorded as an ADR". If this design lands first, it makes the two smaller edits: "the ADR-007 chaos test" becomes "the claim fencing chaos test", and the ADR sentence is deleted. |
| [§6.3 Startup Latency Analysis](../../spec/06_warm-pod-model.md#63-startup-latency-analysis), **Tier 2 promotion gate:** | "attached to the Phase 2 exit gate ADR" becomes "recorded in the evidence folder of the change that runs the benchmark". §18 is a successor stub, so "the Phase 2 exit gate" has no definition to attach to. |
| [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), **Startup warning requirement:** | "records benchmark results to the Phase 14.5 exit gate ADR" becomes "records benchmark results in the evidence folder of the change that runs the benchmark". |
| [§19 Resolved Decisions](../../spec/19_resolved-decisions.md#19-resolved-decisions), row 14 | "**Resolved — MIT (ADR-008).**" becomes "**Resolved — MIT.**". "Decision recorded as ADR-008 in `docs/adr/`; the `LICENSE` file (MIT) is committed at the repository root. Any change requires a superseding ADR." becomes "The `LICENSE` file (MIT) at the repository root carries the decision. A change to the license goes through a change under `changes/`." The license criteria are PLC-1's edit. When an area change distributes row 14, the `LICENSE` file and `docs/about/governance.md` ("License") remain the home of the license decision. |
| §19, the paragraph after the table | Replace with "Each decision above is a summary. The change under `changes/` that made a decision, or for an earlier decision the change proposal under `proposals/`, records its context, the alternatives considered, and the reasoning, and the specification section the row points to states the behavior. A change to a decision above goes through a change under `changes/`." The paragraph holds while §19 exists; the area changes distribute §19 under decision 9 of change 0115. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `docs/adr/` | Deleted: `index.md`, `template.md`, `0000-use-madr-for-architecture-decisions.md`, `0007-sandboxclaim-optimistic-locking.md`, and `0008-open-source-license.md`. |
| `scripts/check-adr-catalog.sh` | Deleted. |
| `cmd/lenny-test/cmd_run.go` | The `static` tier stage `scripts/check-adr-catalog.sh` is removed. |
| `cmd/lenny-test/cmd_gate.go` | `"adr_test.go"` is removed from `gateDocsFiles`, and the comment above the list drops "ADRs". Without this edit the merge gate fails on the branch, because `gateDocsTestNames` returns an error for a listed file that is missing. |
| `tests/docs/adr_test.go` | Deleted, with its three entries in `tests/registers/skip-reasons.yaml`. |
| `tests/chaos/adr007_leader_kill_claim_test.go` | Renamed `tests/chaos/leader_kill_claim_test.go`. `TestLeaderKillMidClaimNoDoubleAssignment` keeps its name. Its comments, `// diagnosis:`, and failure messages cite §4.6.1 in the form "REC-1 Removal" (**Tests and tooling.**) gives, in place of ADR-007. Its two `tests/registers/skip-reasons.yaml` entries, which key the test by path, follow the rename. |
| `tests/chaos/concurrency_test.go`, `leader_election_test.go`, `tests/e2e_kind/crd_test.go`, `tests/security/scaffolds_test.go`, and `pkg/gateway/core/podclaim/claimer_test.go` | Comments, `// diagnosis:` text, and the `crd_test.go` failure message cite §4.6.1 in the form "REC-1 Removal" (**Tests and tooling.**) gives, in place of ADR-007 or ADR-0007. The `// spec:` annotation of `claimer_test.go` reads `§4.6.1 (Warm Pool Controller (Pod Lifecycle))`, or cites the `CLAIM` requirement once the claim page has landed. |
| `tests/static/build/license_test.go` | The comments cite the `LICENSE` file and §19 row 14 in place of ADR-0008. |
| `tests/docs/budget_extension_trigger_consistency_test.go` | The block that reads `docs/adr/index.md` and asserts the ADR-0014 row is removed, and the `// diagnosis:` drops the ADR-0014 index row. The other assertions stay. |
| `tests/docs/workspace_plan_binding_doc_reconciliation_test.go` | The skip of the `adr` directory and its comment are removed. |
| `tests/static/lint/pod_lifecycle_kind_names_test.go` | `"docs/adr/"` is removed from `retiredNameExemptPrefixes`, the pinned value `wantPrefixes` of `TestRetiredNameExemptionsArePLCR11List` becomes `"changes/,proposals/"`, and the comment drops "the decision records under docs/adr/ (until change 0125 removes them)". PLCR-11 of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) exempts `docs/adr/` only until this change deletes it. |
| `tests/groups.yaml` | The Phase 0 description drops "ADRs,". |
| Go comments and chart templates | `pkg/podclaim/state/state.go`, `pkg/admission/podclaim_guard/guard.go`, `pkg/admission/webhook/podclaim_guard.go`, `cmd/lenny-webhook/main.go`, `pkg/gateway/core/podclaim/claimer.go`, `charts/lenny/templates/admission-policies/podclaim-guard-webhook.yaml`, and `charts/lenny/templates/system-network-policies.yaml` cite §4.6.1 in the form "REC-1 Removal" (**Tests and tooling.**) gives, in place of ADR-007. `pkg/embedded/manifests/manifests.yaml` follows the chart through the generator that `embedded_manifests_sync_test.go` checks. File names and package paths follow whatever kind name and package layout the tree carries when this lands. |
| `.claude/rules/doc-content.md` | The escape hatch "ADRs and other historical records describe a decision at a point in time" becomes "Historical records, such as an implemented change proposal or an archived change under `changes/archive/`, describe a decision at a point in time". |

Revision 1 also listed edits to `tests/spec-map.json` and `tests/spec-map-exceptions.yaml`. 0116.S6 retired both files, and `changes/programs/v1.md` ("Spec-map and change-graph edits") reads such an edit as the `// spec:` annotation the test carries, so revision 2 drops the two rows.

## Documentation

| File | Change |
| :-- | :-- |
| `docs/about/contributing.md` | "Feature requests and design proposals" ends with "Architectural changes start as a change under `changes/`. See Decision records below.", where "Decision records" links to that section. The "ADR process" section is renamed "Decision records", and its subsections are replaced by the text of "REC-1". The "Communication" table's ADRs row becomes a "Changes" row linking `changes/` on GitHub. |
| `docs/about/governance.md` | "ADR approval or rejection" and "ADR review and approval" become "Settling the decisions of a change". "During BDfN governance" items 1 and 3 name a change under `changes/` in place of draft ADRs and the relevant ADR. The section "Architecture Decision Records (ADRs)" is renamed "Decision records" and carries the text of "REC-1", with the community path: a community member opens a Discussion with the proposal and its alternatives, and the maintainer or the steering committee answers within 2 weeks with the decision and its reasoning, which a change under `changes/` then records. "License" names the `LICENSE` file as the record of the license decision. The "Governance artifacts" table's ADRs row becomes "Changes, `changes/`, ongoing". "Next steps" drops "ADRs". Revision 1 also edited a "Foundation" milestone row, which the page no longer has. |
| `GOVERNANCE.md` | Line 3 names the decision-record process in place of the ADR process. The contributor criterion names change reviews in place of ADR reviews. "Proposals" are filed as GitHub Discussions or as changes under `changes/`. "Decisions" are documented in the change or the issue. The heading "Architecture Decision Records" is renamed "Decision records", and the paragraph requiring an ADR states that a significant architectural decision starts as a change and links `docs/about/governance.md#decision-records`. The file names an ADR on six lines (3, 14, 21, 23, 26, and 28 on 2026-11-19), and none remains. |
| `docs/about/status.md` | The "In design" row reads "Design work in progress. No code yet." The ADRs row is removed. |
| `docs/about/index.md` | The Architecture Decisions row is removed. |
| `docs/about/style-guide.md` | The `we` rule reads "only in governance and contributing pages". |
| `docs/getting-started/index.md` | The contributing row reads "Development workflow, code conventions, the change process, and the canonical spec." |
| `docs/testing/documentation-tests.md` | The description drops "ADR continuity". The `adr_test.go` and `check-adr-catalog.sh` rows and the "ADR catalog" section are removed. Authoring step 1 drops the ADR path, and step 5 is removed. |
| `docs/testing/index.md` | The tier 0 row drops "ADR catalog", the tier 11 row drops "ADR continuity", and the documentation tests link drops "ADR". |
| `TESTING.md` | The `docs` row of "3. Test layer model" drops "ADR catalog". "12.0 Static (`static`)" item 11 is removed. "12.8 Chaos (`chaos`)" names "double-claim verification (§4.6.1 claim fencing)". "12.11 Documentation (`docs`)" item 4 is removed. "Merge gate" drops "the ADR catalog" from the general documentation gates. "13.0 Phase 0" reads "`LICENSE` (MIT) at repo root" and drops the ADR catalog cross-check. "13.7 Phase 3.5" cites §4.6.1 alone. "16. Documentation tests" item 7 is removed. "20.12 Branch protection" reads "No merge without a change under `changes/` for changes that require one (per the contributing guide)." |
| `.github/PULL_REQUEST_TEMPLATE.md` | "Link the issue, discussion, or ADR" becomes "Link the issue, discussion, or change". |
| `.github/ISSUE_TEMPLATE/feature_request.md` | "If the change is large enough to need an ADR, note that here." becomes "If the change is large enough to need a change under `changes/`, note that here.", and the link into `docs/adr` goes. |
| `CHANGELOG.md` | States that `docs/adr/` and the ADR catalog checks are removed and that the changes under `changes/` are the decision records. |

Revision 1 also edited `ROADMAP.md`, which 0115.S9 removed.

The documentation site loses the "Architecture Decisions" navigation entry with `docs/adr/index.md`. No other page sets `parent: "Architecture Decisions"` **[source]**. Operators and runtime authors meet no change in behaviour, setting, default, or failure mode.

## Tests

| Tier | Coverage |
| :-- | :-- |
| `static` | A repository check in `tests/static/lint` finds no `docs/adr/` and no `scripts/check-adr-catalog.sh` (RECR-1), and no match of `\bADRs?\b`, `ADR-[0-9]+`, `docs/adr`, or "Architecture Decision Record" outside `changes/`, `proposals/`, `issues/`, the audit records (`BUILD-GAPS.md`, `TEST-GAPS.md`, `gateway-runtime-comms.md`, and `gateway-runtime-comms-remediation.md`), `CODE-REVIEW.md`, `CHANGELOG.md`, and every `testdata/` directory (RECR-2). The word boundaries keep identifiers such as `LOADRUNNER` from matching, and the check spells its own patterns without matching itself, as `tests/static/lint/pod_lifecycle_kind_names_test.go` does with placeholders. |
| `chaos` | `TestLeaderKillMidClaimNoDoubleAssignment` runs from `leader_kill_claim_test.go`, and the concurrent-claim tests run unchanged (RECR-4). |
| `docs` | The existing link gate finds no link into `docs/adr/` from `docs/`, the root documents, or `.github/`. |

RECR-3 and RECR-5 state wording in §19 and in two documentation pages. `.claude/rules/test-coverage.md` ("Documentation checks") admits only general gates and generator checks and forbids a test that matches specification prose, so review of 0125.S1 and 0125.S2 verifies RECR-3 and RECR-5, and no test does.

The repository check verifies a property of the repository and no specification requirement, so it carries no `// spec:` annotation and states why in a comment (`.claude/rules/spec-annotations.md`). The renamed chaos test keeps its annotation in the form "REC-1 Removal" (**Tests and tooling.**) gives, and tests in a tier that requires `// diagnosis:` carry it.

## Instructions for other designs

- [`0127-egress-and-git`](../0127-egress-and-git/change.md): the documentation row `adr/`, "A new ADR records the decisions in this design. The ADR index states that a change to the credential model warrants one.", is removed. The design is its own decision record.
- [`0142-security-oversight`](../0142-security-oversight/change.md): the documentation row `adr/`, "A new ADR records the decisions here.", is removed.
- [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md): revision 3 already leaves every ADR site to this design. PLCR-1 and PLCR-11 exempt `docs/adr/` until this design deletes it.
