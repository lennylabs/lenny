# Changes

This folder holds every change to Lenny that is planned or in progress: what is wrong or missing, the decisions taken, the design, the steps, and the record of how it was reviewed and built. The specification under `spec/` states the current system. A change states how it will differ and how to get there. A finished or cancelled change moves to `archive/` and is not edited again.


## Layout

```text
changes/
  README.md                          this file
  BOARD.md                           generated: steps ready, building, and blocked, by milestone
  0113-pod-lifecycle-controller/
    change.md                        the design, in the template below
    decisions.md                     open decisions, then settled decisions
    steps.md                         validation owed, then the steps
    review-history.md                one entry per review or revision, and the history of the status
    digests/                         one digest per landed step, such as 0113.S2.md
    evidence/                        spikes and test runs the change cites, with a README
  programs/
    v1.md
  archive/
    2026-12-02-0113-pod-lifecycle-controller/
```

A change folder is named `<number>-<slug>`. Numbers continue from the last proposal under `proposals/` and are never reused. A change moves to `archive/` with a date prefix when its status becomes `Implemented` or `Cancelled`.

## `change.md`

A change is split by how often each part changes. `change.md` holds the design, which changes only in a revision. `decisions.md` changes while the owner decides, and `steps.md` changes while the change is built. The three files read standalone together: terms come first in `change.md`, a label from another change is linked and glossed, and interfaces are named.

The template fixes the order of the sections of `change.md`, and a change keeps only the sections it needs:

| Section | Holds | Small change |
| :-- | :-- | :-- |
| Status line | Status, revision, date, and the programs the change belongs to, and nothing else. What a revision changed goes in `review-history.md`. | Yes |
| Problem | What is wrong or missing, with evidence | Yes |
| Scope | What the change covers and what it leaves out | When it is not obvious |
| Terms | Every term the change uses, defined or glossed with a link | When it adds terms |
| High-level design | A plain-language description a reader can follow without the rest of the file | No |
| Requirements | The requirements the change adds, modifies, or retires, by identifier | Yes |
| Current state | What the tree does today, with file references | When it differs from the specification |
| Detailed design | One subsection per mechanism, such as "CT-1 Protocol members" | No |
| Worked example | One scenario traced through the change | No |
| Limits, and alternatives rejected | What the change does not guarantee, and the options it turned down | No |
| Specification and non-specification changes | The specification pages, packages, charts, and schemas the change edits | Yes, as a list |
| Documentation | Each documentation page the change adds, edits, or removes, with its reader (client, operator, runtime author, or contributor) and what changes for that reader, or a statement that no reader-facing behavior changes | Yes |
| Tests | The tests by tier and the requirement each verifies | Yes |

The specification, the code, and the documentation are edited on the change's branch, and the diff is the record of the edit. A change does not stage edits.

## `decisions.md`

| Section | Holds |
| :-- | :-- |
| Open decisions | Questions waiting on the owner, each with its options and a recommendation |
| Decisions | Settled decisions, numbered, each with the owner's answer and its date |

When the owner settles an open decision, it moves to Decisions with the answer. A change with no decisions has no `decisions.md`.

## `steps.md`

| Section | Holds |
| :-- | :-- |
| Validation owed | What has to be measured or run before a step is trusted, and which step waits on it |
| Steps | The step entries |

A change decides once and lands in steps. A step is the unit that is built, reviewed, and merged: one branch, carrying the specification edits, then the code and the tests, then the documentation that the step needs. A large change has many steps, and a small one has one.

Each step is a heading `<change>.S<n> <title>` followed by these fields, then the step's description:

```markdown
### 0127.S3 Pod git path
- status: Planned
- aliases: [EG-3]
- lanes: [specification, code, documentation]
- depends-on: [0127.S1, 0127.S2, 0127.S9, 0119.S2]
- closes: [F-26.2.11, F-14.1.37]

GitLab token reuse, the `git.operation` stream from `netagent`, ...
```

- `status` is `Planned`, `Building`, `Landed`, or `Cancelled`.
- `aliases` lists the labels that other text uses for the step. Text written before steps existed names a step by its old proposal label, such as EG-3 or AR-B, or as "proposal 3" of its change; the alias resolves it.
- `lanes` lists the kinds of edit the step makes, in the order they land.
- `depends-on` lists steps of any change that must land first. A reason for an edge, when it is not obvious, goes in the description. A step that lands "with or after" another depends on it, and steps that land together share a branch; an ordering that is conditional or that only says "lands with" stays a sentence. When only part of the step waits, the entry carries `(part)`, such as `0127.S5 (part)`, and the description names the part: the rest of the step can land first, and the waiting part lands with or after the other step.
- `closes` lists the issues the step closes. Until a finding migrates to `issues/`, its legacy identifier from `BUILD-GAPS.md` or `TEST-GAPS.md` stands in. A step that closes part of an issue lists it with `(part)` and names the part in its description, and the issue closes when its last part lands.

A step can be built while its change is `Draft` when none of the change's open decisions touches it. Several small steps may share a branch and a digest. A step that needs decisions or evidence of its own, such as one gated on a spike, may get its own change folder, which names the parent change in its status line and is listed by the step as an alias.

### Status

| Status | Meaning |
| :-- | :-- |
| `Draft` | The change is being written, or it has open decisions, and no step has started. |
| `Decided` | Every decision is settled, and no step has started. |
| `In progress` | At least one step is `Building` or `Landed`. Open decisions that arise during the build are listed in the change and on the board. |
| `Implemented` | Every step is `Landed` or `Cancelled`. The folder moves to `archive/`. |
| `Cancelled` | The change will not be done. The reason is in `review-history.md`, and the folder moves to `archive/`. |

The change's status follows from its decisions and its steps' statuses.

After a change is implemented, the specification holds what it decided, and each decision's reason becomes rationale on the page that owns it. The archive keeps the history, the alternatives, and the evidence.

## `digests/`

A digest is generated from the branch at the end of each step and committed with the step as `digests/<step>.md`. Steps that share a branch share a digest named for the range, such as `0113.S1-S3.md`. It lists:

- each requirement the step added, modified, or retired, with old and new text side by side;
- the code sites and the tests of each requirement, from the annotations;
- the documentation pages that describe each touched specification page, from their front matter, marked as edited in the step or not edited, and every catalog name the documentation mentions that the step renamed or removed;
- the gate results, the review outcomes (fixes, open questions, and issues filed), and every default the agent applied without asking.

Because it is generated from the diff, it states what the tree holds.

## `review-history.md`

One entry per review or revision, newest last. An entry has the date, the step it concerns when it concerns one, what triggered it (a review, an owner decision, or a revision), what was found, and what changed. `change.md` carries no history.

## Edits that need no change

An edit that changes no requirement, no defined value, and no operation lands as a plain commit with the gates green. Typos, glosses, links, page moves, catalog regeneration, and documentation edits that bring a page in line with the current specification are such edits.

## Programs

A program is a plan over several changes: its goal, its member changes, milestones with exit criteria, and the current milestone. Members stay in their own folders, and the program lists them by number, so a change can belong to two programs or move between them. A program changes whenever the plan does, including after implementation starts; git history records each revision. Dependency edges live in the changes' steps. `BOARD.md` is generated from the steps and the program and is never edited by hand: for each milestone it lists the steps ready to start (every dependency landed and no open decision touching them), the steps building, and the blocked steps with what blocks them, and it lists the open decisions by change. A program may also state how agents read its member changes together while it runs, such as which state counts as the target and how a conflict between members is raised; agent instructions point to the active program's guidance.

```yaml
---
program: v1
goal: >-
  Every design is implemented, every open gap is closed, and the specification states the result.
members: [0113, 0114, 0115]
milestones:
  - id: M1
    title: Runtimes and pools at target state
    steps: [0114.S1, 0113.S1, 0113.S2, 0113.S4, 0118.S2]
    exit: The runtimes-and-pools pages are landed, and tiers 0 to 5 pass for the requirements of POOL, CLAIM, and APOD.
current: M1
---
```

## Workflow

A change moves through these activities. Their order is typical rather than fixed, and a change repeats any of them as often as it needs:

1. **Frame.** State the problem, the scope, and the open decisions, with evidence from the tree.
2. **Decide.** The owner settles the open decisions, steering the design through as many rounds as it takes.
3. **Build.** On one branch, edit the specification, then the code and the tests, then the documentation each reader needs, until the gates and the tests pass. A choice that neither the specification nor the Decisions section settles becomes an open decision, and the work on other items continues.
4. **Review.** Check the result, confirm or refute each finding, fix what needs no decision, and raise the rest as open decisions or issues.
5. **Land.** Merge the step, close the issues it lists, and update the status. Archive the change after its last step, or when it is cancelled.

## Helpers

The helpers are skills under `.claude/skills/` that any of these activities can use, in any order. Most take their target and their conventions as arguments, so they also work on documents and code outside this folder. Those marked as bound to conventions read the layouts of this folder, `issues/`, and the specification. The logic of the bound helpers that needs no model is in `scripts/changes/changes.py`, which tier 0 tests.

| Helper | What it does | Generic or bound | Available |
| :-- | :-- | :-- | :-- |
| `review` | Reviews a target (a change, documents, a diff, or a branch) with two subagents in parallel: a general reviewer, who reviews the target itself and cross-references it against the tree, the documentation, and the other changes of the same program, and a security reviewer. The session checks each finding's evidence before it fixes or reports it. `--since <revision>` limits it to what changed, `--fix` applies the findings that need no decision, and `--record <file>` appends a summary entry. One round. | Generic | Now |
| `tidy` | Brings a document into a template given as an argument: section order, terms first, each statement made once, history moved out, and the documentation style applied. A verifier refuses the edit if any decision, requirement, value, or dependency changes meaning. | Generic | Now |
| `fact-check` | Checks each factual claim in a document against the tree, and optionally against live external sources, and reports the claims it cannot confirm. | Generic | Now |
| `ask` | Turns the open questions in a document into multiple-choice questions with the recommendation first, and records each answer with its date in the section the caller names. | Generic | Now |
| `triage` | Classifies a list of items (issues, open items of a design, or review findings) into categories the caller gives, with a verifier for the claims that close an item. | Generic | When the first area's findings migrate |
| `build` | Carries out a change's steps on a branch cut from `main`: the specification first, then the code and the tests, iterating with `lenny-test --changed`, then the documentation pages the change names and any page that names what the step changed. It writes the step's digest and stops on a choice the change does not settle, recording it as an open decision. | Bound | Now |
| `change` | Creates a change folder from the template, sets a change's or a step's status, archives a change and rewrites the links into it, checks every step entry, and regenerates `BOARD.md`. | Bound | Now |
| `issue` | Files an issue, links it to a change, and closes it. | Bound | When the first area's findings migrate |
| `digest` | Generates the digest of a step or a branch under `digests/`. Until it exists, `build` writes the digest by hand. | Bound | With 0115 |
| `where` | Shows a requirement's text, its code sites, its tests, the documentation pages that describe its page, its open issues, and the last change that touched it, and answers follow-up questions about how the code meets it. | Bound | With 0115 |
| `since` | Lists the requirements added, modified, and retired since a date or a commit, and the packages changed under each area. | Bound | With 0115 |

## Gates

These checks run on every commit and cost no model tokens. 0115 builds them; until it lands, only the checks that exist today run. `changes/` is outside their domain, as `proposals/` is today: a change records proposed and historical states, so its text is not held to the rules for current truth. The change that creates this folder adds it to the exclusion list in `scripts/specshift/scope/scope.go` and to the residual registers.

- Links and identifiers resolve, and no link targets a heading on another page.
- Every definition block is defined once, and every gloss follows the gloss rule.
- The documentation style lint passes.
- Every requirement a diff adds or modifies has at least one test that cites it.
- Every specification annotation in new or changed code resolves and follows the annotation rule, and an area's count of old `§` citations does not rise.
- Documentation stays in step with the specification ([draft rule](0115-spec-structure/evidence/spike/draft-rules/doc-spec-links.md), which 0115 lands):
  - every documentation page names in its front matter the specification pages it describes, and each code resolves;
  - every setting, metric, alert, error, label, annotation, event, and CLI command a documentation page names exists in the catalogs, and a default, limit, or unit it quotes matches the definition block;
  - a diff that adds, modifies, or retires a requirement or a definition block on a specification page either edits a documentation page that names that page, or the change's Documentation section states that no reader-facing behavior changes;
  - reference pages generated from the catalogs are current.
