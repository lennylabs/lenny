---
name: change
description: Create, update, and archive the change records under changes/ - a new numbered change folder, a change's or a step's status, archiving with link rewriting, and checking the step entries and regenerating BOARD.md. Bound to the layout in changes/README.md. Use whenever a change is created, a step starts or lands, or the board needs regenerating.
argument-hint: new <slug> [title] | status <number> <status> | step <step> <status> | archive <number> | check | board
---

# Change

Run the change records commands. `scripts/changes/changes.py` does the work; this skill says when to use each command and what to do around it. `changes/README.md` defines the records.

## Commands

Run from the repository root.

| Command | Use |
| :-- | :-- |
| `python3 scripts/changes/changes.py new <slug> [title]` | Create the next numbered change folder with `change.md`, `decisions.md`, `steps.md`, and `review-history.md`. Then fill in the problem and scope, and add the change to a program's `members` if it belongs to one. |
| `python3 scripts/changes/changes.py status <number> <status>` | Set a change's status. It refuses a status the steps contradict, such as `Implemented` with a step open. Bump the revision on the status line by hand when the design text changed, and add an entry to `review-history.md`. |
| `python3 scripts/changes/changes.py step <step> <status>` | Set a step's status: `Building` when work starts, `Landed` when its branch merges, `Cancelled` when it is dropped. It warns when the change's status no longer agrees; set that next. |
| `python3 scripts/changes/changes.py archive <number>` | Move an `Implemented` or `Cancelled` change to `changes/archive/<date>-<folder>/` and rewrite every link into and out of it. Then grep for the old folder path in prose and code spans outside the archive, which the command does not rewrite, and fix what names it as a current location. |
| `python3 scripts/changes/changes.py check` | Check every step entry: format, identifiers, dependencies, cycles, and statuses. A warning names steps that wait on each other through partial dependencies; read their descriptions to confirm the parts are distinct. |
| `python3 scripts/changes/changes.py board` | Regenerate `changes/BOARD.md`. Run it after any status or dependency edit, and commit it with that edit; tier 0 fails when it is stale. |
| `python3 scripts/changes/changes.py links [--baseline <file>]` | List broken links under `changes/`, or only those not in a baseline written earlier with `--write-baseline`. Use it around any edit that moves or renames sections. |

## Rules

- Never edit `BOARD.md` by hand.
- Run `check` and `board` before committing an edit to any `steps.md` or status line.
- An archived change is not edited again, except by a later `archive` that rewrites a link into it.
