# Spec structure spike: changes, programs, issues, and the working loop

Status: version 1, 2026-11-16. Version 0 recommended how designs, proposals, build plans, gap records, and the roadmap fit around the specification structure that [TAXONOMY.md](TAXONOMY.md) defines. Version 1 applies the owner's review of version 0: it drops the change sizes, settles where a program's members live, records that programs change after implementation starts, takes option C as the migration order, and replaces the proposal pipeline with a lighter working loop and the helpers that make it fast for the owner. "Owner decisions" at the end records what is settled. Nothing here changes the current records.

## Constraints

- Every design under `designs/` and every open gap in `BUILD-GAPS.md` and `TEST-GAPS.md` is fixed before v1, and v1 is due no later than January 2027.
- The owner's attention is the bottleneck. Delegating whole proposals to a long review pipeline let the owner's understanding drift from the tree, and the drift cost refactoring later.
- Lenny has no deployments yet, so the review depth that protects running deployments is not needed until it has some.

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

## Layout

The layout replicates `designs/`: one Markdown file per change, and an evidence folder named after it when a spike or a test run backs it.

```text
spec/                                  current truth only (TAXONOMY.md)
changes/
  README.md                            generated board: active changes, what waits on the owner, programs
  0113-pod-lifecycle-controller.md     one file per change, in the design template
  0140-reject-empty-pool-name.md       a small change uses the same template with fewer sections
  evidence/
    0113-pod-lifecycle-controller/     spikes and test runs the change cites, with a README
  programs/
    v1.md
  archive/
    2026-12-02-0113-pod-lifecycle-controller.md
    evidence/
      2026-12-02-0113-pod-lifecycle-controller/
issues/
  README.md                            generated: open issues by area, unplanned issues, issues per change
  POOL/
    POOL-I12-pod-set-label-not-immutable.md
```

`changes/` sits beside `spec/` because agents read everything under `spec/` as current truth, and the write guard and the gates on `spec/` are scoped by path. OpenSpec keeps `openspec/specs/` and `openspec/changes/` as siblings for the same reason.

## Changes

A change is one file in the template the designs under `designs/` use today. A small change keeps only the sections it needs, and the template fixes their order:

| Section | Holds | Small change |
| :-- | :-- | :-- |
| Status line | Status, revision, date, and the programs the change belongs to | Yes |
| Problem | What is wrong or missing, with evidence | Yes |
| Scope | What the change covers and what it leaves out | When it is not obvious |
| Terms | Every term the change uses, defined or glossed with a link | When it adds terms |
| High-level design | A plain-language description a reader can follow without the rest of the file | No |
| Decisions | Settled decisions, each with the owner's answer and its date | When there are any |
| Open decisions | Questions still waiting on the owner, each with options and a recommendation | When there are any |
| Requirements | The requirements the change adds, modifies, or retires, by identifier | Yes |
| Current state | What the tree does today, with file references | When it differs from the spec |
| Mechanism sections | One section per mechanism, as the designs number them | No |
| Worked example | One scenario traced through the change | No |
| Limits, and alternatives rejected | What the change does not guarantee, and the options it turned down | No |
| Specification, non-specification, and documentation changes | The pages, packages, charts, and documentation pages the change edits | Yes, as a list |
| Tests | The tests by tier and the requirement each verifies | Yes |
| Validation owed | What has to be measured or run before the change is trusted | When there is any |
| Steps | The numbered steps with their dependencies, replacing "Proposal split and order" | Yes |
| Review history | One line per revision | Yes |

A change file reads standalone: terms come first, labels from other changes are linked and glossed, interfaces are named, and history stays in "Review history" at the end. Staged spec edits and staged code no longer exist: the spec and the code are edited on the change's branch, and the diff is the artifact. A design that today names several proposals becomes one change with a step for each.

"Steps" numbers each step as `<change>.S<n>`. Each step states its lane (specification or code), the issues it closes, and the steps it depends on, in any change:

```markdown
### 0113.S4 Add pod sets per pod-set key and scope claims to the revision
- lane: code
- depends-on: [0113.S1, 0118.S2]
- closes: [GWCLAIM-I3]
```

A change's status is `Draft`, `Decided`, `Spec landed`, `Implemented`, or archived. A change that has no open decisions starts at `Decided`. At `Implemented` the file and its evidence move to `changes/archive/` under a date prefix and are never edited again. The specification then holds what the design decided, including the owner's answers as rationale on the capability page, and the archive keeps the history, alternatives, and evidence.

Proposals 0001 to 0112 stay in `proposals/` unchanged. A proposal that has not landed moves to `changes/` the first time someone works on it. New changes continue the numbering from 0113.

## Programs

A program is a plan over changes: the goal, the member changes, milestones with exit criteria, and the `current` milestone. Members live beside each other in `changes/`, and the program lists them by identifier. A change can join a second program or move to another one without moving its folder.

A program is a living document. The owner adds, removes, reorders, and splits milestones and members at any time, including after implementation has started, and git history records each revision. The immutability rule applies to archived changes only. Dependency edges live in the steps, so replanning edits the program file alone, and the generated board recomputes the step graph and status from the Steps sections of the members.

```yaml
---
program: v1
goal: >-
  Every design is implemented, every open gap is closed, and the specification states the result.
members: [0113, 0114, 0115, ...]
milestones:
  - id: M1
    title: Runtimes and pools at target state
    steps: [0114.S1, 0113.S1, 0113.S2, 0113.S4, 0118.S2]
    exit: The runtimes-and-pools pages are landed, and tiers 0 to 5 pass for the requirements of POOL, CLAIM, and APOD.
current: M1
---
```

`BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `spec/18_build-sequence.md` retire into the program. `ROADMAP.md` is removed, and `docs/about/status.md` is generated from the program's milestones.

## Migration order

Option C, accepted by the owner: each area's new pages are written at the target state, from the current specification and the designs that touch the area. The old sections become successor stubs, and the code catches up through each design's code steps. A first change lands the structure itself: conventions, the code registry, the definition-block format, the generators, the reference tool, and the gates.

The code's 18,348 `// spec: §` annotations are not rewritten mechanically, because a current section's content spreads over several pages and placing a citation needs an understanding of the code. A frozen copy of the current specification keeps the old citations resolving. New and touched code cites identifiers, and a per-area count of old citations has to reach zero before an area's milestone exits, so re-annotation happens while an agent already has that code loaded.

A throwaway spike is migrating one area's specification and then the gateway pod claimer's code, to measure what this costs in practice. Its results will update this section.

## Issues

An issue is a discrepancy nobody has planned. Planned work is a step of a change.

- **Location and name.** `issues/<CODE>/<CODE>-I<n>-<slug>.md`, where `CODE` is the code of the page that owns the requirement in question. Page codes survive reorganizations, so the folders never move. Numbers are never reused.
- **Front matter.** `id`, `kind` (`build-gap`, `test-gap`, `spec-defect`, or `question`), `severity`, `requirements` (identifiers on any page), `change` (the change and step that will close it), and `legacy` (the `F-` identifier of a migrated finding).
- **Closing.** The closing step lists the issue under `closes`, and the step's commit deletes the file. The archived change and git history keep the record.
- **The queue.** `PROPOSAL-QUEUE.md` becomes the `change` field and the generated list of open issues that no change claims.

Every open gap is fixed before v1, so every open finding migrates. The triage helper below classifies each one against the designs first. A finding that a design removes or settles closes with a pointer to that design's change. The rest are filed under the code that owns their requirement, area by area, as each area's pages land.

## The working loop

1. **Decide.** The owner answers the change's brief before any spec or code is written.
2. **Build.** On one branch, the spec edits are committed first, then the code and tests.
3. **Review once.** After the build and tests pass, a small set of reviewers checks the diff, a verifier checks each finding, the agent fixes what needs no decision, and the owner gets the decisions and a digest.
4. **Adversarial review where it pays.** Design decisions, security-sensitive changes, and, once deployments exist, changes that could break one keep a capped adversarial review.

## Helpers

The helpers are built for the owner's attention: put decisions in front of the owner in a form that takes seconds to answer, stop agents from deciding silently, replace reviewer agents with deterministic gates wherever a rule can be checked mechanically, keep the owner's reading of a result short and tied to the tree, and keep the owner's picture of the tree current.

### For step 1

- **`triage`** (workflow: one classifier per batch, one verifier per claim). Input: the open issues of one page code or component, or the open items of a design ("Open decisions", "Validation owed"). Each item is classified as clear (the spec states the rule, and the code or the tests deviate), decision (the spec is silent or contradictory, or a choice is open), obsolete (a design removes or settles it, with the design's text quoted), or duplicate. The verifier checks every obsolete and duplicate claim against the quoted text. Clear items go to implementation batches without a brief. Decision items go to `brief`. The owner sees the counts and the decision items.
- **`brief`** (skill: one writer, one fact checker). For a change without a design, it writes the change file's Problem, Open decisions, Requirements, and Steps sections, kept to one screen. Each open decision has its question, its options, a recommendation, and its consequence, one line apiece. The defaults the agent will apply without asking are listed too. For a change with a design, it reads the design's Open decisions. The fact checker verifies each factual claim against the tree before the owner reads it, so the owner never decides on a false premise. The skill then presents the open decisions as multiple-choice questions with the recommendation first, and moves each answer into Decisions with its date. After the change lands, each answer becomes rationale on the page that owns the decision.

Converging on a design can take several rounds of the owner steering it. Two helpers serve those rounds, and either runs at any point:

- **`tidy-design`** (skill: one editor, one verifier). Puts the change file into the template: section order, terms first, labels linked and glossed, statements made once, history moved to one line per revision in "Review history", superseded text and alternatives that the Decisions section has closed removed, and the documentation style applied. The verifier compares the before and after and refuses the edit if any decision, requirement, or value changed meaning. The owner gets the diff.
- **`design-review`** (workflow: two general-purpose reviewers and a verifier per finding). Each reviewer gets a short charter rather than a canned lens:
  - The tree reviewer checks every claim the design makes about the current code, the specification, and other changes against the tree, and looks for conflicts with other active changes.
  - The implementer reviewer reads the design as the person who has to build it, and reports what is ambiguous, missing, or contradictory, or what would fail.

  Every finding carries evidence (a file and the quoted text). The verifier confirms or refutes each finding. Confirmed findings that need no decision are fixed in place, and the rest become Open decisions. `--since <revision>` limits the review to the sections changed since that revision and the sections they affect, so a round of steering costs a review of what moved. A security reviewer joins when the change is marked security-sensitive. The owner decides when a design has converged; the workflow does not loop until its findings run out.

### For step 2

- **`build-change`** (workflow). Creates the branch in its own worktree, opens a write lease on the pages the change names for the life of the branch, commits the spec edits, then writes the code and tests and iterates with `lenny-test --changed` up to the tiers the change reaches.
  - It has a stop rule: when the agent meets a choice that neither the spec nor the change's Decisions section settles, it stops that item, adds the question to Open decisions, and continues with the items it can finish. The board shows the open questions, and `brief` presents them in batches.
  - Several changes can run at once in separate worktrees. Only the owner's answers are serialized.
- **The write lease** changes to name a change and its pages, and to last for the branch. Today it names an approved proposal and one checklist step.

### Gates that replace reviewers

These run before any reviewer and cost no model tokens:

- Links and identifiers resolve, and no link targets another page's heading (the reference tool, TAXONOMY.md "References and moves").
- Every definition block is defined once, and every gloss follows the gloss rule.
- The documentation style lint.
- Every requirement the diff adds or modifies has at least one test that cites it.
- Every `// spec:` annotation on new or changed code resolves, and the area's count of old `§` citations does not rise.

Today's reviewer dimensions for citations, edit sites, single sourcing, documentation alignment, and test presence become these gates.

### For step 3

- **`review-change`** (workflow: three reviewers, a verifier per finding, and a fixer). The reviewers run in parallel on the branch diff and the touched pages:
  - the spec reviewer checks that the edited pages agree with each other and with the change's Decisions section;
  - the code reviewer checks the code against the requirements it cites, using the `code-review` skill at medium effort;
  - the test reviewer checks that each touched requirement's tests reach its error, boundary, and concurrent paths.

  Each finding goes to a verifier that confirms it with evidence or refutes it. The fixer applies confirmed findings that need no decision and reruns the gates and tests. A finding that needs a decision goes to the owner's questions. A low-severity finding becomes an issue. The workflow runs one round, and the only re-review is of the fixer's diff. Severities follow one fixed rubric, and each run records its token use, so the cost per change is visible.
- **`digest`** (generated, no model). Prints a digest at the end of each step, before the step's branch merges. The digest is generated on demand and not committed. It lists:
  - each requirement the step added, modified, or retired, old and new text side by side, from the spec diff;
  - the code sites and tests of each requirement, from the annotations;
  - gate results, review outcomes (fixes as one line each, open questions, and issues filed), and every place the agent applied a default from the brief.

  The owner approves the merge from the digest. Because it is generated from the diff, it cannot drift from the tree.

### For staying close to the tree

- **`where`** (skill backed by the reference tool). `where POOL-R5` returns the requirement's text, its code sites, its tests, its open issues, and the last change that touched it. A follow-up question to the same skill reads the code and explains how it meets the requirement, with file and line references.
- **`since`** (generated). Lists the requirements added, modified, and retired since a date or a commit, and the packages that changed under each area. The owner reads it at the start of a session.
- **The board** (`changes/README.md`, generated). Shows each active change, its status, its branch, its test status, and its open questions, with the questions waiting on the owner at the top.

### For step 4

- **`design-review`**, described under step 1, runs on every new design, on every change marked security-sensitive, and, after the first deployment, on changes that alter a stored schema, an API, or an upgrade path.

### Citations in code

New code cites the specification far less than the current code does. Today 18,912 annotation lines cite the specification: 12,075 in tests and 6,837 in non-test code, and one file carries 133. A draft rule, [draft-rules/spec-annotations.md](draft-rules/spec-annotations.md), keeps citations where they map a requirement to its test or to the code that makes its decision, and a tier-0 lint enforces it. The rule replaces the citation part of `spec-driven-development.md` and the format part of `code-best-practices.md` when the structure lands. The code half of the spike applies it.

### What retires

| Today | Replaced by |
| :-- | :-- |
| `change-proposal`, `change-proposal-decisions`, and `mechanism-table-converge` | `brief`, `tidy-design`, and `design-review` |
| `implement-proposal` and `implement-proposal-build` | `build-change` |
| `proposal-conformance` | The digest, because no staged edits exist to conform to |
| `build-gaps-spec-unblock` and `close-test-gaps` | `triage` and `build-change` over issues |
| `migrate-proposal` and `fix-line-citations` | Nothing; the legacy proposals stay as they are, and line citations end with the restructure |

`fidelity-review` stays available for step 4.

### Build order

The helpers are themselves work, so they come in the order that unblocks changes soonest:

1. `brief`, `tidy-design`, `design-review`, `build-change` with the new lease, `review-change`, and a first `digest` keyed by section headings. These run against the current specification, so the next change can use them immediately.
2. The reference tool, the gates, `where`, `since`, and the identifier-keyed digest, as part of the change that lands the structure.
3. `triage`, when the first area's issues migrate.

The first few changes through the loop record the owner's time and the tokens per change, and the helpers are adjusted from those numbers.

## Owner decisions

Recorded from the owner's reviews of 2026-11-16.

1. **Change sizes.** Dropped. What a change holds follows from its content.
2. **Program members.** Side by side in `changes/`, listed by identifier. Programs change after implementation starts.
3. **Migration order.** Option C, by area.
4. **Scope of v1.** Every design and every open gap.
5. **Process.** A lighter loop with helpers, with adversarial review kept for designs and security-sensitive changes. Design review uses general-purpose reviewers with short charters, and a tidy helper keeps designs in the template between rounds of steering.
6. **Layout.** Changes replicate the layout and the section template of `designs/`.
7. **Citations in code.** New code cites the specification far less than the current code, under a project rule.

## Open decisions for the owner

1. Whether `changes/` sits beside `spec/` at the repository root, as recommended, or under `spec/`.
2. Whether a closed issue's file is deleted, with the closing change as the record, or moved to `issues/closed/`.
3. Whether edits that change no requirement (typos, glosses, links, and page moves) land with the gates alone and no change folder.
4. The helpers and their build order above.
