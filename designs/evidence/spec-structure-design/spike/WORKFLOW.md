# Spec structure spike: changes, programs, and issues

Status: version 0, 2026-11-16. This file recommends how designs, proposals, build plans, gap records, and the roadmap fit around the specification structure that [TAXONOMY.md](TAXONOMY.md) defines. It answers the owner's review of 2026-11-16, which asked whether `spec/` needs a changes folder and an archive, how proposals, designs, and plans spanning several of them tie together, how small and large changes differ, whether gap records move to an `issues/` folder named by the new codes, and whether `ROADMAP.md` is needed. Nothing here changes the current records.

## Records today

| Record | What it holds | Measured on 2026-11-16 |
| :-- | :-- | :-- |
| `designs/` | One design per change of direction: problem, terms, decisions, requirements, the specification and code changes, the tests, owed validations, and a "Proposal split and order" section. Ordering across designs is stated in prose inside each design. | 36 designs, about 1.14M words |
| `proposals/` | One directory per change in the role-file layout of `.claude/proposal-pipeline-rework-plan.md`. Spec edits are staged as quoted-text replacements under numbered sections. | Proposals 0001 to 0112 |
| `BUILD-PLAN.md` and `BUILD-PROGRESS.md` | The wave plan to v1-complete and its current-wave pointer. Both predate the designs and follow §18. | 3,334 and 6,171 words |
| `spec/18_build-sequence.md` | The phased build sequence, inside the specification. | 10,868 words |
| `BUILD-GAPS.md` | Spec-versus-code findings, keyed `F-<section>`, in one file. | 821,224 words; 367 open, 2,330 closed or resolved |
| `TEST-GAPS.md` | Spec-versus-test findings in one file. | 520,582 words; 1,371 open, 475 closed or resolved |
| `PROPOSAL-QUEUE.md` | Open findings clustered into future proposals. | 16,583 words |
| `ROADMAP.md` | A short public horizon. It still names `docs/adr/`, the reference runtime catalog, MCP delegation, and "Now — Design phase", which the designs have overtaken. | 636 words |

Three gaps follow from the table. No record holds a plan that spans several designs, so the refactor in `designs/` has no single ordering. A design and the proposals that land it are separate artifacts with no shared identifier. The gap records are single files too large for an agent to read, keyed by section numbers that the restructure retires.

## Recommendation

- One unit of change, the **change**, replaces the separate design and proposal. A change is a folder. A large change carries a design; a small one does not.
- A **program** is a plan over several changes: its goal, its member changes, milestones with exit criteria, and a dependency graph over the changes' steps. It replaces `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `spec/18_build-sequence.md`.
- `changes/` sits at the repository root beside `spec/`, with `changes/programs/` and `changes/archive/`. A completed change moves to the archive and is never edited again.
- `issues/` holds one file per open finding, in a folder per page code, and replaces `BUILD-GAPS.md`, `TEST-GAPS.md`, and `PROPOSAL-QUEUE.md`.
- `ROADMAP.md` is removed. The public status page is generated from the programs' milestones.

```text
spec/                                  current truth only (TAXONOMY.md)
changes/
  README.md                            generated: active changes, programs, milestone status
  0113-pod-lifecycle-controller/       a feature change
    change.md                          front matter (id, size, status, programs, closes) and summary
    problem-statement.md
    design.md                          feature and program-scale changes only
    spec-changes.md                    edits keyed by identifier
    non-spec-changes.md
    implementation-checklist.md        numbered steps with dependencies
    review-log.md
    deviations.md
    evidence/                          spikes and test runs the design cites
  0140-reject-empty-pool-name/         a fix change: no design.md
  programs/
    v1-redesign.md
  archive/
    2026-12-02-0113-pod-lifecycle-controller/
issues/
  README.md                            generated: open issues by area, unplanned issues, issues per change
  POOL/
    POOL-I12-pod-set-label-not-immutable.md
  GWCLAIM/
    GWCLAIM-I3-fallback-ignores-revision.md
```

The role files are the ones the pipeline already writes. The change adds `design.md` and `evidence/`, moves the status into front matter that `.claude/tools/proposal-status.mjs` reads, and drops the kind from the folder name into a `size` field.

## Why `changes/` sits beside `spec/`

- `spec/` holds only the current statement of the system. An agent that globs `spec/` reads every file it finds as truth, and a proposed state under `spec/changes/` would be read as current.
- The write guard on `spec/` and the gates that read it (citations, the naming law, document style) are scoped by path. A changes folder inside `spec/` would need exceptions in each of them, and a change author would need a write lease to edit a draft.
- OpenSpec, the closest published model, keeps `openspec/specs/` and `openspec/changes/` as siblings, with the archive at `changes/archive/YYYY-MM-DD-<name>/`.
- PEP 1 states the same split for Python: "Once resolution is reached, a PEP is considered a historical document rather than a living specification."

## Change sizes

| Size | When it applies | Files | Review and approval |
| :-- | :-- | :-- | :-- |
| Editorial | The edit changes no requirement's meaning, no defined value, and no operation: a typo, a gloss, a link, a page move, a split that keeps every identifier, or a catalog regeneration. | None. One commit under an editorial write lease, with the gates green. | The diff. The lease refuses an edit that adds, changes, or retires a requirement, a definition block, or an operation. |
| Fix | The edit changes requirements inside one area and settles no decision with alternatives worth recording. | `change.md`, `problem-statement.md`, `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`, `review-log.md`. | Adversarial review to convergence, then owner approval. |
| Feature | The edit adds a concept, crosses areas, removes a mechanism, or settles decisions with alternatives. | The fix files and `design.md`, plus `evidence/` when a spike ran. | Design review and owner decisions first, then the fix review over the staged edits. |
| Program | The work needs several changes in a set order with milestones. | A program file under `changes/programs/`. Each member is its own fix or feature change. | The owner approves the program's milestones and member list. Each member change is approved on its own. |

A design that today names "one proposal" becomes one feature change. A design that names several proposals also becomes one change whose checklist has a step per former proposal. Each step can land on its own branch, so splitting a design across proposals no longer needs separate artifacts.

## Steps and dependencies

The checklist numbers steps as `<change>.S<n>`. Each step states the lane it lands in (specification or code), the issues it closes, and the steps it depends on, in any change:

```markdown
### 0113.S4 Add pod sets per pod-set key and scope claims to the revision
- lane: code
- depends-on: [0113.S1, 0118.S2]
- closes: [GWCLAIM-I3]
- [ ] PodSet controller and the warm split
- [ ] Claim scope by pod hash
```

The dependency edges live only in the steps. A program does not restate them; its graph is generated from the steps of its member changes.

## Specification edits keyed by identifier

Under the new structure, `spec-changes.md` locates each edit by identifier instead of by quoted text under a numbered section. Each edit adds, modifies, or retires a requirement by its identifier, a definition block by its name, or a page by its code, and it gives the full new text:

````markdown
## POOL (capabilities/runtimes-and-pools/warm-pools.md)

### MODIFY POOL-R5. Pool and pod-set labels are required and immutable.
Every agent pod carries `lenny.dev/pool` and `lenny.dev/pod-set` ...

### RETIRE POOL-R9 → CLAIM-R3

### MODIFY setting maxIdleSeconds
```yaml lenny-def
setting: maxIdleSeconds
...
```
````

An edit keyed by identifier survives a page move and does not depend on the surrounding text, so a change applies cleanly after other changes land.

## Lifecycle of a change

`Draft` → `Reviewed` → `Approved` → `Spec landed` → `Implemented` → archived.

- `Spec landed` is new. It records that the specification lanes have landed while code steps remain. The specification leads the code, as `spec-driven-development.md` requires.
- An unfinished step of an active change is planned work. It is not filed as an issue.
- At `Implemented` the change moves to `changes/archive/<date>-<id>/` in the same commit as its last step. The archive is outside every gate and is never edited, like an implemented proposal today.
- After archiving, the specification holds what the design decided, including the current rationale on each capability page. The design's history, alternatives, and evidence stay in the archive. This matches the owner's preference to treat `designs/` as temporary.

## Programs

A program file holds the goal, the member changes, and milestones. A milestone names the steps it needs and an exit criterion that a test tier or a gate checks. The `current` field replaces the current-wave pointer of `BUILD-PROGRESS.md`. The program's step graph and completion status are generated into `changes/README.md` from the member changes' checklists.

```yaml
---
program: v1-redesign
goal: >-
  The specification states the target design of every area, and the code and tests conform to it.
members: [0113, 0114, 0115, ...]
milestones:
  - id: M1
    title: Runtimes and pools at target state
    steps: [0114.S1, 0113.S1, 0113.S2, 0113.S4, 0118.S2]
    exit: The runtimes-and-pools pages are landed, and tiers 0 to 5 pass for the requirements of POOL, CLAIM, and APOD.
current: M1
---
```

A program is archived with its last milestone.

## Order of the restructure and the refactor

The restructure of the specification and the refactor in `designs/` both rewrite most of the text, so their order matters.

| Order | Cost |
| :-- | :-- |
| A. Restructure the current specification first, then land each design as a change against the new pages. | Every page is written twice: once at the current state, then again for each design that touches it. The map shows that most areas are touched by one or more designs. |
| B. Land the designs into the current specification first, then restructure. | Every design's edits are staged as quoted text against a structure that is about to be replaced. Agents cross-reference the current specification worst, so this order spends the most review effort where reading is hardest. |
| C. Write each area's new pages at the target state, from the current specification and the designs that touch the area. | The specification lands ahead of the code for every design at once, area by area, and code catch-up runs as the code steps of each design's change. |

The recommendation is C, incrementally by area, inside one program:

1. A first change lands the structure: `conventions.md`, the code registry, the definition-block format, the generators, the reference tool and its gates (TAXONOMY.md, "References and moves"), and the editorial lease.
2. Each area is one specification step. It writes the area's pages at the target state, which discharges the specification lane of every design touching that area. It replaces the old sections with successor stubs that name the new identifiers, as `spec-citations.md` requires. It rewrites the code's `// spec: §` citations for that area to identifiers, from the paragraph assignment.
3. The code steps of each design's change follow their area's specification step, in the order of the step graph.
4. When the last area lands, the numbered chapters hold only successor stubs, and a final editorial change removes them.

The slice in `proposed/` was written this way, from the current specification and the designs at once.

## Issues

An issue is a discrepancy nobody has planned: the code or the tests disagree with the specification, the specification contradicts itself, or a question needs the owner. Planned work is a step of a change.

- **Location and name.** `issues/<CODE>/<CODE>-I<n>-<slug>.md`, where `CODE` is the code of the page that owns the requirement in question. Page codes survive any reorganization of the tree, so the folders never move. Numbers are never reused.
- **Front matter.** `id`, `kind` (`build-gap`, `test-gap`, `spec-defect`, or `question`), `severity`, `requirements` (the identifiers involved, on any page), `change` (the change and step that will close it, once one exists), and `legacy` (the `F-` identifier for a migrated finding).
- **Body.** The headings `BUILD-GAPS.md` uses today: the specification text, the evidence, the gap, and the suggested resolution.
- **Closing.** The closing step lists the issue under `closes`, and the step's commit deletes the file. The archived change keeps the record. Git history keeps the text.
- **The queue.** `PROPOSAL-QUEUE.md` becomes the `change` field and a generated view in `issues/README.md` of open issues that no change claims, grouped by page code.
- **Outside reports.** A GitHub issue from a user becomes an `issues/` file once it is confirmed against the specification.

One file per issue keeps an agent's read small, lets parallel workers close issues without merge conflicts in a large file, and lets an agent working on a page read `issues/<CODE>/` and nothing else.

**Migration.** `BUILD-GAPS.md` and `TEST-GAPS.md` are frozen as they stand. Only open findings migrate. Each is triaged against the designs before it moves: a finding the target design removes or settles closes with a pointer to that design's change, and a finding that survives is filed under the code that owns its requirement. The paragraph assignment from the restructure gives the section-to-code mapping, and a finding whose section splits across pages is placed by hand.

## `ROADMAP.md`

The roadmap repeats `spec/18_build-sequence.md` and the build plan in a shorter form, and it has gone stale against the designs. Once programs exist, the remaining content is the public view of where the project is. The recommendation is to remove `ROADMAP.md` and generate `docs/about/status.md` from the active programs' milestones, with the repository `README.md` linking that page.

## Existing proposals

Proposals 0001 to 0112 stay in `proposals/` unchanged. An implemented proposal is a historical record, and moving it changes the paths that code comments and records cite. A proposal that has not landed migrates to `changes/` the first time a pipeline skill touches it, which matches the rule for the role-file layout today. New changes continue the numbering from 0113.

## Tooling this implies

- `.claude/tools/proposal-status.mjs` gains `Spec landed` and reads the status from `change.md`.
- `change-proposal` asks for a size, writes `design.md` for a feature, and stages edits by identifier.
- `implement-proposal` runs one step or a range of steps, applies edits by identifier, and archives the change after its last step.
- `close-build-gaps.sh`, `build-gaps-spec-unblock`, and `close-test-gaps` read `issues/` by code instead of the two gap files.
- The `spec/` write guard gains the editorial lease. `changes/` and `issues/` need no lease.
- Generators write `changes/README.md`, `issues/README.md`, and `docs/about/status.md`.

## Open decisions for the owner

1. `changes/` beside `spec/` at the repository root, as recommended, or under `spec/`.
2. The order of the restructure and the refactor: option C by area, as recommended, or option A or B.
3. Closed issues are deleted, with the closing change as the record, or moved to `issues/closed/`.
4. The editorial lane lands specification edits that change no requirement without a change folder.
