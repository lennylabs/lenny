# Records and workflow

Status: `Implemented`, revision 4, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M0.

## Problem

Lenny's planned work is spread over records that do not share identifiers. The 36 designs under `designs/` each state their own "Proposal split and order", with ordering across designs in prose. The proposals under `proposals/` stage spec edits as quoted text and carry no link to the design they implement. `BUILD-PLAN.md` and `BUILD-PROGRESS.md` predate the designs, so no record holds a plan that spans them. The owner settled a replacement on 2026-11-16 after a spike: one folder per change under `changes/`, issues under `issues/`, and programs that order changes ([`WORKFLOW.md`](../0115-spec-structure/evidence/spike/WORKFLOW.md), "Owner decisions").

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
| Change | A folder under `changes/` holding one planned or in-progress change: `change.md` for the design, `decisions.md`, `steps.md`, `review-history.md`, `digests/` once a step has landed, and `evidence/` when a spike or test run backs it. The changes README defines it. |
| Program | A plan over several changes, under `changes/programs/`, with milestones and their exit criteria. |
| Design | One of the 36 documents under `designs/` before this change. Each becomes a change. |
| Step | The unit of a change that is built, reviewed, and merged on one branch, with its own status and dependencies. The changes README defines the step entry. |

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
