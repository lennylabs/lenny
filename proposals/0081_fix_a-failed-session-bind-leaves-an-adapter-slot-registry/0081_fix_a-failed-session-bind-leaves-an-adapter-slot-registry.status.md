---
proposal: 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry
title: A failed session bind leaves a stale adapter slot registry entry
kind: fix
status: Reviewed
drafted-date: 2026-09-08
drafted-by: change-proposal
reviewed-date: 2026-09-11
reviewed-by: change-proposal
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

On 2026-09-08, an adversarial review run executed two loops. The spec loop ran 7 rounds, converged, and performed 2 full-pool sweeps. The non-spec loop ran 2 rounds, converged, and performed 1 full-pool sweep. Across both loops, 55 findings were fixed. The run did not reach full convergence overall; findings that the loops did not close remain open.

On 2026-09-10, the proposal was amended by hand to carry the bind-epoch design, and a
second review run executed against that amendment. The spec loop converged and was
followed by a recheck pair, then the non-spec loop converged. A non-spec finding during
that loop corrected the §7.1 retry-placement claim and the §5.2 reclaim-hold caller list,
which staged a further spec edit and opened a second recheck pair. The run was stopped by
the operator at that point, with the staging complete and the deliverable index and
implementation checklist reconciled against it. The review log records the entries the
loops did not close; several are statements that the shipped tree does not yet match the
staging, which the implementation steps land, and the rest are noted under "Known residue"
in the review log.
