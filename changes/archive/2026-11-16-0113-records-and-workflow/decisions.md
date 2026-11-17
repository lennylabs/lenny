# Decisions: 0113 Records and workflow

## Decisions

Settled by the owner on 2026-11-16 during the specification structure spike:

1. One folder per change under `changes/`, beside `spec/`, with `change.md` in the template of the designs, `digest.md` and `review-history.md` as separate files, and `evidence/` as a subfolder.
2. Status is one of `Draft`, `Decided`, `Spec landed`, `Implemented`, and `Cancelled` (decision 12 replaced `Spec landed`). A change moves to `changes/archive/` after `Implemented` or `Cancelled`, and archived is not a status.
3. `changes/` is outside the domain of the tier-0 gates, as `proposals/` is.
4. The records, the helpers, and the specification structure are three changes: this one, 0114, and 0115.
5. The open proposals are left for now. This change moves only `designs/`.
6. M0 to M2 of the v1 program are fixed now. The area milestones are ordered once 0115 lands.

Settled by the owner on 2026-11-16 in answer to this change's open decisions:

7. **Numbering.** 0114 and 0115 as decided, 0116 for coverage derived from annotations, 0117 for the gateway package layout, and the designs from 0118 to 0153 in alphabetical order of their file names.
8. **Status of the moved designs.** Every moved design keeps the status `Draft`, because the owner runs a final cross-review of them before any is `Decided`. The status line keeps the design's revision and its date.
9. **Reading a program's changes.** The owner's working rule of treating designs as implemented applies to this refactor and is not a general rule. It becomes reading guidance on the v1 program ("Reading the program" in [`v1.md`](../../programs/v1.md)), and `AGENTS.md` points to it. The changes README lets any program state such guidance.
10. **Prose that names `designs/`.** Each path is rewritten to its new location, and each sentence that names the folder as a concept names changes instead. Each moved change records the edit in its review history.

Settled by the owner on 2026-11-16, after the move showed that each moved design plans several proposals:

11. **A design stays one change, and its proposals become steps.** Each step lands on its own branch with its own status, dependencies, and aliases for the old proposal labels. A step gets its own change folder only when it needs decisions or evidence of its own.
12. **Statuses.** A step is `Planned`, `Building`, `Landed`, or `Cancelled`. A change is `Draft`, `Decided`, `In progress`, `Implemented`, or `Cancelled`, and `In progress` replaces `Spec landed`, because in the v1 program a change's specification edits land through the area migrations rather than all at once. Each step has its own digest under `digests/`.
13. **Converting the moved designs.** All 36 are converted now (0113.S4), before the owner's cross-review, so the review and the program see the steps. `BOARD.md` is generated from the steps.

Settled by the owner on 2026-11-16, on the layout of a change folder:

14. **A change folder splits by how often each part changes.** `change.md` holds the design, `decisions.md` the open and settled decisions, and `steps.md` the validation owed and the steps. The status line of `change.md` holds only the status, revision, date, and programs, and the rest of the old status lines moves to `review-history.md`. The mechanism sections of `change.md` sit under one "Detailed design" heading.
