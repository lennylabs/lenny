---
proposal: 0093_fix_release-ownership-for-pods-concurrent-workspace-slots-per-po
title: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write
kind: fix
status: Draft
drafted-date: 2026-10-16
drafted-by: change-proposal
reviewed-date: 
reviewed-by: 
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

This section was written on 2026-10-19.

Adversarial review runs covered this proposal in two loops. The round counts below are cumulative, because each run resumed from the recorded state of the previous one.

- Spec loop: ran 28 rounds and converged. The loop ran 14 full-pool sweeps.
- Non-spec loop: ran 13 rounds and did not converge. The loop ran 3 full-pool sweeps.

The latest run, on 2026-10-19, ran spec round 28 and non-spec round 13 and fixed 3 findings. It stopped at non-spec round 13 on an introspection `prune` verdict.

The spec loop converged on the spec changes staged in this proposal. Convergence does not record approval, and the approval fields in the frontmatter are unchanged.

The non-spec loop did not converge. Findings that loop had not closed remain open, and the non-spec changes in this proposal are not settled.

On 2026-10-18 the owner chose option C for the slot-set recovery rebuild, and on 2026-10-19 the owner revised it so that the rebuild is add-only. Both edits preceded the latest run. After that run stopped, a prune on 2026-10-19 removed restated and withdrawn-design text from the non-spec changes and the summary, and it changed no design, owner decision, or staged spec edit. No review round has run on the pruned text.
