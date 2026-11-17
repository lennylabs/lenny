# Spec structure spike: changes, programs, issues, and the working loop

Status: version 2, 2026-11-16. This file records the recommendation and the owner's decisions on how changes, programs, issues, and the roadmap sit around the specification structure that [TAXONOMY.md](TAXONOMY.md) defines, and why. The operational rules live in two drafts that become the folders' READMEs when the structure lands:

- [draft-records/changes/README.md](draft-records/changes/README.md) covers the change folder, its template, statuses, steps, programs, the workflow, the helpers, and the gates.
- [draft-records/issues/README.md](draft-records/issues/README.md) covers the issue layout, front matter, lifecycle, and the migration of the gap records.

Version 0 made the first recommendation. Version 1 applied the owner's first review, and version 2 applies the second. Nothing here changes the current records.

## Constraints

- Every design under `designs/` and every open gap in `BUILD-GAPS.md` and `TEST-GAPS.md` is fixed before v1, and v1 is due no later than January 2027.
- The owner's attention is the bottleneck. Delegating whole proposals to a long review pipeline let the owner's understanding drift from the tree, and the drift cost refactoring later.
- Lenny has no deployments yet. Review depth and write controls that protect running deployments wait until it has some.

## Records today

| Record | What it holds | Measured on 2026-11-16 |
| :-- | :-- | :-- |
| `designs/` | One design per change of direction, with a "Proposal split and order" section. Ordering across designs is stated in prose inside each design. | 36 designs, about 1.14M words |
| `proposals/` | One directory per change in the role-file layout. Spec edits are staged as quoted-text replacements under numbered sections. | Proposals 0001 to 0112. In 0082 to 0112 the review log is 70 to 85 percent of a proposal's words, and a proposal typically runs 50,000 to 200,000 words. |
| `.claude/workflows/` | The proposal pipeline and its helpers. | 11 workflows, 19,453 lines |
| `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `spec/18_build-sequence.md` | The wave plan to v1-complete, its current-wave pointer, and the phased build sequence. All predate the designs. | 3,334, 6,171, and 10,868 words |
| `BUILD-GAPS.md` | Spec-versus-code findings keyed `F-<section>`. | 821,224 words; 367 open |
| `TEST-GAPS.md` | Spec-versus-test findings. | 520,582 words; 1,371 open |
| `PROPOSAL-QUEUE.md` | Open findings clustered into future proposals. | 16,583 words |
| `ROADMAP.md` | A short public horizon, stale against the designs. | 636 words |

## What replaces what

| Today | After the structure lands | Reason |
| :-- | :-- | :-- |
| `designs/` and `proposals/` | One folder per change under `changes/`, beside `spec/` | A design and the proposals that land it shared no identifier. One folder keeps the decisions, the design, the steps, and the evidence together, in the layout and template `designs/` uses today. `changes/` sits beside `spec/` because agents read everything under `spec/` as current truth; OpenSpec keeps `specs/` and `changes/` as siblings for the same reason. |
| Staged spec edits and staged code | The diff on the change's branch | Staged edits were reviewed as prose before the code existed, and a conformance check then compared the landed tree with the staging. |
| `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `spec/18_build-sequence.md` | A program under `changes/programs/` | No record held a plan spanning several designs, and the existing plan predates them. |
| `ROADMAP.md` | Removed; `docs/about/status.md` is generated from the program's milestones | The roadmap repeated the build sequence in a shorter form and went stale. |
| `BUILD-GAPS.md`, `TEST-GAPS.md`, and `PROPOSAL-QUEUE.md` | One file per issue under `issues/<CODE>/` | The gap files are too large for an agent to read and are keyed by section numbers that the restructure retires. |
| The proposal pipeline in `.claude/workflows/` | The helpers in the changes README | See "What retires". |
| The `spec/` write guard and its lease | Nothing until v1 | Before v1 the gates, the review helper, and the digest catch a wrong edit, and a lease costs a step on every change. |

Proposals 0001 to 0112 stay in `proposals/` unchanged. A proposal that has not landed moves to `changes/` the first time someone works on it, and new changes continue the numbering from 0113.

## Migration order

Option C, accepted by the owner: each area's new pages are written at the target state, from the current specification and the designs that touch the area. The old sections become successor stubs, and the code catches up through each design's code steps. A first change lands the structure itself: conventions, the code registry, the definition-block format, the generators, the reference tool, and the gates.

The code's 18,348 `// spec: §` annotations are not rewritten mechanically, because a current section's content spreads over several pages and placing a citation needs an understanding of the code. A frozen copy of the current specification keeps the old citations resolving. New and touched code cites identifiers, and a per-area count of old citations has to reach zero before an area's milestone exits, so re-annotation happens while an agent already has that code loaded.

A throwaway spike migrated one area's specification and then the gateway pod claimer's code ([../spike-migration/README.md](../spike-migration/README.md)). It supports option C and changes the order of work inside it:

- Most specification effort went to reconciling the designs with each other and with the current text. One area of 11 pages surfaced 13 contradictions, 5 of which needed the owner. Scaled to the tree, that is about 25 areas, each one long agent session plus 30 to 40 judgment rows for the owner.
- In code, re-citing and changing a package in place cost less than rewriting it from the pages would have. 57 citations mapped directly, 70 disappeared under the annotation rule, 41 needed reading, and the tests held edge cases the pages do not state.
- The blockers sit across packages: the kind renames touch 463 Go files, and two removals in the claimer wait on readers in other packages.
- Registers keyed by path or section number, such as `tests/spec-map.json` and the tier-0 inventories, caused most of the friction when the package moved.

The work inside option C therefore runs in this order:

1. Land the structure: conventions, the registry, the reference tool, the gates, and the annotation rule.
2. Do the kind renames of the pod lifecycle design once, tree-wide, with Go-aware tooling.
3. Replace `tests/spec-map.json` and the path-keyed tier-0 inventories with coverage derived from annotations.
4. Decide the gateway's package layout, including names under `pkg/gateway`, and give the API surface and the domain stores a home in TAXONOMY.
5. Migrate area by area: the pages first, then the code steps of each design touching the area, removing what has no outside dependents and filing issues for the rest. Split `sessionserver` along the seams as its own change.

## Citations in code

New code cites the specification far less than the current code does. Today 18,912 annotation lines cite the specification: 12,075 in tests and 6,837 in non-test code, and one file carries 133. A draft rule, [draft-rules/spec-annotations.md](draft-rules/spec-annotations.md), keeps citations where they map a requirement to its test or to the code that makes its decision, and a tier-0 lint enforces it. The rule replaces the citation part of `spec-driven-development.md` and the format part of `code-best-practices.md` when the structure lands. The code half of the spike applies it.

## Documentation

A change updates the documentation along with the specification and the code. Version 2 of this file left documentation implicit in the change template, and the owner's review of 2026-11-16 found that the workflow, the helpers, and the gates never named it. The changes README now gives every change a Documentation section, a documentation lane for steps, a reader charter in `review`, documentation in `build`, `where`, and the digest, and a gate. A draft rule, [draft-rules/doc-spec-links.md](draft-rules/doc-spec-links.md), makes the gate possible: every documentation page names in front matter the specification pages it describes, names and values it quotes are checked against the catalogs, and the reference pages that list catalog entries are generated. The rule extends `doc-content.md` when the structure lands.

## Helpers

The changes README lists the helpers without assigning them to workflow activities, so the owner uses each where it helps. Two properties shape the list:

- Most helpers take their target and conventions as arguments, so the same `review` checks a design, a diff, or a specification page, and the same `tidy` brings any document into any template.
- The helpers that read the layouts of `changes/`, `issues/`, and the specification are named as bound to conventions, so a change of convention touches those helpers alone.

Reviewers get short charters instead of fixed lenses, and no helper loops until its findings run out. The owner decides when a design has converged.

### What retires

| Today | Replaced by |
| :-- | :-- |
| `change-proposal`, `change-proposal-decisions`, and `mechanism-table-converge` | `review`, `tidy`, `fact-check`, and `ask` |
| `implement-proposal` and `implement-proposal-build` | `build` |
| `proposal-conformance` | `digest`, because no staged edits exist to conform to |
| `build-gaps-spec-unblock` and `close-test-gaps` | `triage`, `issue`, and `build` |
| `migrate-proposal` and `fix-line-citations` | Nothing; the legacy proposals stay as they are, and line citations end with the restructure |
| `fidelity-review` | `review` with a charter that derives the behavior from the code before reading the specification |

### Build order

The helpers are themselves work, so they come in the order that unblocks changes soonest:

1. `review`, `tidy`, `fact-check`, `ask`, `change`, and `build`. They run against the current specification, so the next change can use them immediately.
2. The reference tool, the gates, `digest`, `where`, `since`, and `board`, as part of the change that lands the structure.
3. `triage` and `issue`, when the first area's issues migrate.

The first few changes record the owner's time and the tokens each change used, and the helpers are adjusted from those numbers.

## Owner decisions

Recorded from the owner's reviews of 2026-11-16.

1. **Change sizes.** Dropped. What a change holds follows from its content.
2. **Layout.** One folder per change under `changes/`, beside `spec/`, with `change.md` in the template of `designs/`, `digest.md` and `review-history.md` as separate files, and `evidence/` as a subfolder.
3. **Status.** `Draft`, `Decided`, `Spec landed`, `Implemented`, and `Cancelled`. Archiving is the move to `changes/archive/`, which follows `Implemented` or `Cancelled`, and is not a status.
4. **Programs.** Members live in their own folders, and the program lists them. Programs change after implementation starts.
5. **Issues.** Front matter carries a status. A closed issue moves to `issues/closed/` with its status updated.
6. **Edits that change no requirement.** These need no change folder.
7. **Write leases.** None before v1, for the specification or the code.
8. **Migration order.** Option C, by area.
9. **Scope of v1.** Every design and every open gap.
10. **Process and helpers.** The workflow is described as activities, and the helpers are listed for the owner to use as needed. Helpers are as generic as their purpose allows, and adversarial review uses general-purpose reviewers with short charters.
11. **Citations in code.** New code cites the specification far less than the current code, under a project rule.
12. **Gate domain.** `changes/` is outside the domain of the tier-0 gates, as `proposals/` is. `designs/` stays inside it until it retires.
13. **Documentation.** A change updates the documentation each affected reader needs, in the same change as the specification and the code, and a gate finds the documentation pages a specification change affects.
