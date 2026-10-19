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

The run did not converge. Findings the run had not closed when it stopped remain open.

- Spec loop: 35 rounds, recorded as converged over 18 full-pool sweeps.
- Non-spec loop: 9 rounds, recorded as converged over 3 full-pool sweeps.

The run fixed 19 findings. The run-level result is not converged, so this proposal is not converged, even though each loop record reads converged.

Convergence does not record approval. The approval fields in the frontmatter are not set by the run.

On 2026-10-18 the owner chose option C for the slot-set recovery rebuild. On 2026-10-19 the owner revised the rebuild so that it is add-only.

On 2026-10-19 the owner added the leader-run stale-slot reaper (summary Decision 23). The addition put staged spec text in SPEC-1a, a new staged edit SPEC-1k, CODE-9, register rows A32 to A34, and failure-outcome row F21.

A previous spec loop stopped at round 32 with the introspection verdict `redesign`, because three facets of one rule (the bind residue A34, the seal window, and the finalize window) had been patched at different sites. A remedy then rebuilt the reaper's timing as one table and one shared timeout (summary Decision 24, review log entry remedy.reaper-age-table.2026-10-19). The remedy changed SPEC-1a and SPEC-1l, CODE-9, register rows A27 and A34, added row A35, and corrected R11 and Decision 15.

The run summary does not state whether the run read the reaper additions or the remedy.

## Open items

- The proposal is not converged. Findings the run had not closed remain open and are listed in the review log's **Open** section.
- Further review rounds in both the spec and non-spec lanes are needed before the proposal can be treated as converged.
