# Records and workflow

Status: `Decided`, revision 2, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M0.

## Problem

Lenny's planned work is spread over records that do not share identifiers. The 36 designs under `designs/` each state their own "Proposal split and order", with ordering across designs in prose. The proposals under `proposals/` stage spec edits as quoted text and carry no link to the design they implement. `BUILD-PLAN.md` and `BUILD-PROGRESS.md` predate the designs, so no record holds a plan that spans them. The owner settled a replacement on 2026-11-16 after a spike: one folder per change under `changes/`, issues under `issues/`, and programs that order changes ([`WORKFLOW.md`](../../designs/evidence/spec-structure-design/spike/WORKFLOW.md), "Owner decisions").

The design evidence also fails tier 0 today: `TestResidualGateCertifiesTheTree` reports 1,665 files under `designs/evidence/` that the change-graph coverage register does not classify.

## Scope

This change creates the records and moves the designs into them. It changes no specification text and no Go code.

- **In scope.**
  - `changes/` with its README, the v1 program, and a board.
  - `issues/` with its README and no issues.
  - One change folder for each design.
  - The design evidence moved into those folders.
  - The gate domain excluding `changes/`.
  - The rules and agent instructions that name `designs/`.
- **Out of scope.**
  - The open proposals: the owner set them aside on 2026-11-16, and they stay in `proposals/` unchanged.
  - The helpers (0114).
  - The specification structure, its tooling, and the generated board, digest, and status page (0115).
  - The retirement of `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `ROADMAP.md`. The naming law in §28, two tier-0 tests and one tier-9 test, `spec-citations.md`, and the repository's README, contribution guide, changelog, pull request template, and two pages under `docs/about/` name these files, so their retirement edits the specification and moves to 0115.
  - Migrating findings from `BUILD-GAPS.md` and `TEST-GAPS.md` into `issues/`, which happens area by area.

## Terms

| Term | Meaning |
| :-- | :-- |
| Change | A folder under `changes/` holding one planned or in-progress change: `change.md`, `review-history.md`, `digest.md` once a step has landed, and `evidence/` when a spike or test run backs it. The changes README defines it. |
| Program | A plan over several changes, under `changes/programs/`, with milestones and their exit criteria. |
| Design | One of the 36 documents under `designs/` before this change. Each becomes a change. |

## Decisions

Settled by the owner on 2026-11-16 during the specification structure spike:

1. One folder per change under `changes/`, beside `spec/`, with `change.md` in the template of the designs, `digest.md` and `review-history.md` as separate files, and `evidence/` as a subfolder.
2. Status is one of `Draft`, `Decided`, `Spec landed`, `Implemented`, and `Cancelled`. A change moves to `changes/archive/` after `Implemented` or `Cancelled`, and archived is not a status.
3. `changes/` is outside the domain of the tier-0 gates, as `proposals/` is.
4. The records, the helpers, and the specification structure are three changes: this one, 0114, and 0115.
5. The open proposals are left for now. This change moves only `designs/`.
6. M0 to M2 of the v1 program are fixed now. The area milestones are ordered once 0115 lands.

Settled by the owner on 2026-11-16 in answer to this change's open decisions:

7. **Numbering.** 0114 and 0115 as decided, 0116 for coverage derived from annotations, 0117 for the gateway package layout, and the designs from 0118 to 0153 in alphabetical order of their file names.
8. **Status of the moved designs.** Every moved design keeps the status `Draft`, because the owner runs a final cross-review of them before any is `Decided`. The status line keeps the design's revision and its date.
9. **Reading a program's changes.** The owner's working rule of treating designs as implemented applies to this refactor and is not a general rule. It becomes reading guidance on the v1 program ("Reading the program" in [`v1.md`](../programs/v1.md)), and `AGENTS.md` points to it. The changes README lets any program state such guidance.
10. **Prose that names `designs/`.** Each path is rewritten to its new location, and each sentence that names the folder as a concept names changes instead. Each moved change records the edit in its review history.

## Current state

- `designs/` holds 36 `*-design.md` files and `designs/evidence/` with evidence folders for 11 of them and for the specification structure spike. Some designs cite another design's evidence folder; `designs/evidence/README.md` records which.
- About 5,800 relative links inside `designs/` point at other designs or at evidence. No file under `spec/`, `docs/`, `pkg/`, `cmd/`, `tests/`, `scripts/`, `.claude/`, or `proposals/` links into `designs/`. `BUILD-GAPS.md` names it.
- Each design ends with "Review history" and most have "Proposal split and order". Their status lines read "design for owner review, revision N, date".
- `.claude/rules/` does not name `designs/`. The owner's working rules about designs live in agent memory.

## Specification and non-specification changes

- `changes/README.md` and `issues/README.md`, from the spike's drafts.
- `changes/BOARD.md`, written by hand until 0115 generates it.
- `changes/programs/v1.md`.
- `changes/0118-*` to `changes/0153-*`, one per design: `change.md` from the design file, `review-history.md` from its "Review history" section, Steps from its "Proposal split and order", and `evidence/` from its evidence folder. Links are rewritten.
- `changes/0115-spec-structure/`, holding the specification structure spike as its evidence and a `change.md` skeleton at `Draft`. `changes/0114-helpers/` gets a skeleton at `Draft`.
- `designs/` removed.
- `scripts/specshift/scope/scope.go` and the residual registers exclude `changes/`.
- `AGENTS.md` describes where planned work lives and points to the active program's reading guidance. Its instructions for the beads tracker go, because commit `d09b5df5b` removed beads from the project.

## Documentation

`docs/about/contributing.md` describes how design proposals are raised, under "Feature requests and design proposals". It gains one sentence that names `changes/` as the place where accepted designs live. No other reader-facing behavior changes.

## Tests

- Tier 0 passes. `TestResidualGateCertifiesTheTree` stops reporting the design evidence once `changes/` is excluded.
- A link check over `changes/` finds no broken relative link or anchor.
- A count check confirms each design produced exactly one change folder, and that its review history and evidence moved with it.

## Steps

### 0113.S1 Create the records
- lane: non-specification
- Write `changes/README.md`, `issues/README.md`, `changes/BOARD.md`, and `changes/programs/v1.md`, and the skeletons of 0114 and 0115.

### 0113.S2 Move the designs
- lane: non-specification
- depends-on: [0113.S1]
- A script moves each design and its evidence, splits out the review history, renames "Proposal split and order" to Steps, sets the status line, rewrites links and path mentions, and moves the spike's evidence into 0115. The script lives in this change's `evidence/` and is not committed elsewhere.

### 0113.S3 Exclude `changes/` from the gates, and update the agent instructions
- lane: non-specification
- depends-on: [0113.S2]
- Update `scope.go`, the residual registers, `AGENTS.md`, and the contribution page, and run tier 0 and the link check.
