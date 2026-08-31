# Summary: Scope the coordination generation to the session

## Summary

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` equality
gate, the gap-detection reset, and the coordinator-loss hold follow it to whatever scope review settles on.
The proto doc comment that already claims per-session monotonicity becomes true.

**What is fixed.** On a concurrent pod: a legitimate coordinator handoff rejected as
`coordinator_handoff_stale`, a drain barrier rejected so a partial checkpoint is lost, a spurious
`coordinator_generation_gap` and its state reset, a coordinator-loss hold released by an unrelated
session's fence, and a split-brain counter that increments on healthy handoffs.

**Watch out for.** The mechanical half, moving the counter onto the registry entry, is not the hard half.
Hold state is pod-wide today and holding one session of four means per-session admission on every inbound
RPC. That decision is open and is the reason this is a proposal rather than a patch. Proposal 0073 is
converged and is not reopened; this proposal sequences after it.
