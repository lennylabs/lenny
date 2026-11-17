# Review history: 0125-decision-records Decision records

# Revision notes

- Revision 1, 2026-11-16. Written from the owner's words "Actually I've been wondering whether this whole ADR thing is needed at all" and the owner's "ok" to removing ADRs through a separate short design (D1). The sites were found by searching the tree for `ADR`, `ADRs`, `ADR-` numbers, `docs/adr`, `adr/`, and `check-adr`, outside `proposals/` and the audit records, and each was read in place. While writing it, the author found that the two designs that planned an ADR already had their rows removed in the working tree by a concurrent pass, so "Instructions for other designs" states the instruction for the record. No independent review has read revision 1.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/decision-records-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location. Sentences that named `designs/` as the current home of designs now name `changes/`; dated statements of what was read under `designs/` keep their words.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> with no independent review yet. Nothing here is built or staged. It records the owner's decision of 2026-11-16 to remove Architecture Decision Records (ADRs) from Lenny (D1), and lists every site that names them with the change each site takes. Changes under `changes/` and change proposals under `proposals/` become Lenny's decision records, and §19 stays as the summary table of resolved decisions. It reads against every design under `designs/` as implemented, in particular [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) revision 3, which renames the claim kind inside the §4.6.1 paragraph whose "ADR-007" label this design removes. The changes go through `change-proposal` after review. The design element carries the label REC-1, and requirements carry the label RECR-n. "Review history" at the end records the revisions. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.
