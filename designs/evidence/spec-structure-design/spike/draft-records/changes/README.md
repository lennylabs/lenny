# Changes

This folder holds every change to Lenny that is planned or in progress: what is wrong or missing, the decisions taken, the design, the steps, and the record of how it was reviewed and built. The specification under `spec/` states the current system. A change states how it will differ and how to get there. A finished or cancelled change moves to `archive/` and is not edited again.

Draft, 2026-11-16, from the specification structure spike. It becomes `changes/README.md` when the structure lands.

## Layout

```text
changes/
  README.md                          this file
  BOARD.md                           generated: active changes, open decisions, programs, and milestone status
  0113-pod-lifecycle-controller/
    change.md                        the change, in the template below
    digest.md                        generated at the end of each step
    review-history.md                one entry per review or revision
    evidence/                        spikes and test runs the change cites, with a README
  programs/
    v1.md
  archive/
    2026-12-02-0113-pod-lifecycle-controller/
```

A change folder is named `<number>-<slug>`. Numbers continue from the last proposal under `proposals/` and are never reused. A change moves to `archive/` with a date prefix when its status becomes `Implemented` or `Cancelled`.

## `change.md`

A change reads standalone. Terms come first, a label from another change is linked and glossed, and interfaces are named. The template fixes the order of the sections, and a change keeps only the sections it needs:

| Section | Holds | Small change |
| :-- | :-- | :-- |
| Status line | Status, revision, date, and the programs the change belongs to | Yes |
| Problem | What is wrong or missing, with evidence | Yes |
| Scope | What the change covers and what it leaves out | When it is not obvious |
| Terms | Every term the change uses, defined or glossed with a link | When it adds terms |
| High-level design | A plain-language description a reader can follow without the rest of the file | No |
| Decisions | Settled decisions, each with the owner's answer and its date | When there are any |
| Open decisions | Questions waiting on the owner, each with its options and a recommendation | When there are any |
| Requirements | The requirements the change adds, modifies, or retires, by identifier | Yes |
| Current state | What the tree does today, with file references | When it differs from the specification |
| Mechanism sections | One section per mechanism | No |
| Worked example | One scenario traced through the change | No |
| Limits, and alternatives rejected | What the change does not guarantee, and the options it turned down | No |
| Specification, non-specification, and documentation changes | The pages, packages, charts, and documentation pages the change edits | Yes, as a list |
| Tests | The tests by tier and the requirement each verifies | Yes |
| Validation owed | What has to be measured or run before the change is trusted | When there is any |
| Steps | The numbered steps with their dependencies | Yes |

The specification and the code are edited on the change's branch, and the diff is the record of the edit. A change does not stage edits.

### Steps

Each step is numbered `<change>.S<n>` and states its lane (specification or code), the issues it closes, and the steps it depends on, in any change:

```markdown
### 0113.S4 Add pod sets per pod-set key and scope claims to the revision
- lane: code
- depends-on: [0113.S1, 0118.S2]
- closes: [GWCLAIM-I3]
```

### Status

| Status | Meaning |
| :-- | :-- |
| `Draft` | The change is being written, or it has open decisions. |
| `Decided` | Every decision is settled, and no step has landed. |
| `Spec landed` | The specification steps have landed, and code steps remain. |
| `Implemented` | Every step has landed. The folder moves to `archive/`. |
| `Cancelled` | The change will not be done. The reason is in `review-history.md`, and the folder moves to `archive/`. |

After a change is implemented, the specification holds what it decided, and each decision's reason becomes rationale on the page that owns it. The archive keeps the history, the alternatives, and the evidence.

## `digest.md`

The digest is generated from the branch at the end of each step and committed with the step. It lists:

- each requirement the step added, modified, or retired, with old and new text side by side;
- the code sites and the tests of each requirement, from the annotations;
- the gate results, the review outcomes (fixes, open questions, and issues filed), and every default the agent applied without asking.

Because it is generated from the diff, it states what the tree holds.

## `review-history.md`

One entry per review or revision, newest last. An entry has the date, what triggered it (a review, an owner decision, or a revision), what was found, and what changed. `change.md` carries no history.

## Edits that need no change

An edit that changes no requirement, no defined value, and no operation lands as a plain commit with the gates green. Typos, glosses, links, page moves, and catalog regeneration are such edits.

## Programs

A program is a plan over several changes: its goal, its member changes, milestones with exit criteria, and the current milestone. Members stay in their own folders, and the program lists them by number, so a change can belong to two programs or move between them. A program changes whenever the plan does, including after implementation starts; git history records each revision. Dependency edges live in the changes' steps, and `BOARD.md` computes the step graph and the status of each milestone from them.

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
3. **Build.** On one branch, edit the specification, then the code and the tests, until the gates and the tests pass. A choice that neither the specification nor the Decisions section settles becomes an open decision, and the work on other items continues.
4. **Review.** Check the result, confirm or refute each finding, fix what needs no decision, and raise the rest as open decisions or issues.
5. **Land.** Merge the step, close the issues it lists, and update the status. Archive the change after its last step, or when it is cancelled.

## Helpers

The helpers are skills and workflows that any of these activities can use, in any order. Most take their target and their conventions as arguments, so they also work on documents and code outside this folder. Those marked as bound to conventions read the layouts of this folder, `issues/`, and the specification.

| Helper | What it does | Generic or bound |
| :-- | :-- | :-- |
| `review` | Reviews a target (documents, a diff, or a branch) with a few general-purpose reviewers, each given a short charter, then has a verifier confirm or refute each finding with evidence. It can fix confirmed findings that need no decision, and `--since <revision>` limits it to what changed. Default charters: a tree checker for claims about the code and the specification, an implementer for what is ambiguous or would fail, and a security reviewer on request. The caller can pass other charters. | Generic |
| `tidy` | Brings a document into a template given as an argument: section order, terms first, each statement made once, history moved out, and the documentation style applied. A verifier refuses the edit if any decision, requirement, or value changes meaning. | Generic |
| `fact-check` | Checks each factual claim in a document against the tree and reports the claims it cannot confirm. | Generic |
| `ask` | Turns the open questions in a document into multiple-choice questions with the recommendation first, and records each answer with its date in the section the caller names. | Generic |
| `triage` | Classifies a list of items (issues, open items of a design, or review findings) into categories the caller gives, with a verifier for the claims that close an item. | Generic |
| `build` | Carries out a change's steps on a branch: the specification first, then the code and the tests, iterating with `lenny-test --changed`. It stops on a choice the change does not settle and records it as an open decision. | Bound |
| `change` | Creates a change folder from the template, sets its status, and archives it. | Bound |
| `issue` | Files an issue, links it to a change, and closes it. | Bound |
| `digest` | Generates `digest.md` for a step or a branch. | Bound |
| `where` | Shows a requirement's text, its code sites, its tests, its open issues, and the last change that touched it, and answers follow-up questions about how the code meets it. | Bound |
| `since` | Lists the requirements added, modified, and retired since a date or a commit, and the packages changed under each area. | Bound |
| `board` | Regenerates `BOARD.md`. | Bound |

## Gates

These checks run on every commit and cost no model tokens. `changes/` is outside their domain, as `proposals/` is today: a change records proposed and historical states, so its text is not held to the rules for current truth. The change that creates this folder adds it to the exclusion list in `scripts/specshift/scope/scope.go` and to the residual registers.

- Links and identifiers resolve, and no link targets a heading on another page.
- Every definition block is defined once, and every gloss follows the gloss rule.
- The documentation style lint passes.
- Every requirement a diff adds or modifies has at least one test that cites it.
- Every specification annotation in new or changed code resolves and follows the annotation rule, and an area's count of old `§` citations does not rise.
