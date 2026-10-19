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

This section was written on 2026-10-18.

An adversarial review run covered this proposal in two loops.

- Spec loop: ran 27 rounds and converged. The loop ran 13 full-pool sweeps.
- Non-spec loop: ran 12 rounds and did not converge. The loop ran 3 full-pool sweeps.

The run fixed 10 findings across both loops.

The spec loop converged on the spec changes staged in this proposal. Convergence does not record approval, and the approval fields in the frontmatter are unchanged.

The non-spec loop did not converge. Findings that loop had not closed remain open, and the non-spec changes in this proposal are not settled.

After the run, on 2026-10-18, the owner chose option C for the slot-set recovery rebuild, and the proposal was edited to apply it: the rebuild removes the members of sessions whose rows are terminal in place of replaying failed releases. That edit rewrote non-spec content and one edge-case bullet in the spec-changes file, and it changed no staged spec edit. No review loop has run on the edited content, so the non-spec changes remain unsettled, and the spec loop's convergence predates the edited bullet.

On 2026-10-19 the owner revised option C so that the rebuild is add-only: it adds the ledger's entries, deletes the flag of each listed pod, and removes no member, and a member whose release failed during an outage stays counted until a later release of it or the orphan GC. That edit rewrote the same non-spec content and renamed the same edge-case bullet, and it changed no staged spec edit. No review loop has run on it.
