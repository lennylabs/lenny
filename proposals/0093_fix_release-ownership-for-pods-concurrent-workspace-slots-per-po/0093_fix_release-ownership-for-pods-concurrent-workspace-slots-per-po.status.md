---
proposal: 0093_fix_release-ownership-for-pods-concurrent-workspace-slots-per-po
title: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write
kind: fix
status: Reviewed
drafted-date: 2026-10-16
drafted-by: change-proposal
reviewed-date: 2026-10-19
reviewed-by: change-proposal
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

This section was written on 2026-10-19 from the output of an adversarial review run on this proposal.

The run did not converge. Counts are for this run.

- Spec loop: ran 32 rounds and did not converge. The loop ran 17 full-pool sweeps.
- Non-spec loop: not run.

The run fixed 4 findings. Findings the spec loop had not closed when it stopped remain open. The run did not settle the spec questions it reviewed.

Convergence does not record approval. The approval fields in the frontmatter are not set by the run.

On 2026-10-18 the owner chose option C for the slot-set recovery rebuild. On 2026-10-19 the owner revised the rebuild so that it is add-only. Both decisions preceded the run.

On 2026-10-19 a prune removed restated and withdrawn-design text from the non-spec changes and the summary. The prune changed no design, owner decision, or staged spec edit.

On 2026-10-19, after the run, the owner added the leader-run stale-slot reaper (summary Decision 23). The change added staged spec text to SPEC-1a and a new staged edit, SPEC-1k, and added CODE-9, register rows A32 to A34, and failure-outcome row F21. No review loop has read these additions, so a recheck of both lanes is due before sign-off.

On 2026-10-19 the spec loop stopped at round 32 with the introspection verdict `redesign`, because three facets of one rule (the bind residue A34, the seal window, and the finalize window) had been patched at different sites. Under the owner's delegation the same day, a remedy rebuilt the reaper's timing as one table and one shared timeout (summary Decision 24, review log entry remedy.reaper-age-table.2026-10-19). The remedy changed SPEC-1a and SPEC-1l, CODE-9, register rows A27 and A34, added row A35, and corrected R11 and Decision 15.

The proposal is not converged, and these items are open:

- A relaunch with `resumeState` is due. Spec rounds 30 to 32 read the owner's reaper additions, the non-spec loop has not read them, and no loop has read the remedy, so both lanes need a review before sign-off.
- The review log's **Open** section lists the findings that remain open.
