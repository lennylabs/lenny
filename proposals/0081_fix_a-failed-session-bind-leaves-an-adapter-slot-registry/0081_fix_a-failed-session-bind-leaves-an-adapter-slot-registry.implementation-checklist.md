## Implementation checklist

Every spec step leads. Each implementation step carries the tests for the tiers its line
names, per `## Testing` in the non-spec changes file.

- [ ] **S1 · spec** — SPEC-1. §4.1's scope sentence and the §4.7 `Shutdown` row state the slot release and the runtime teardown as two teardowns with two preconditions, and state the no-op answer for a session the adapter holds no entry for. §29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §7.1 gains the pod-side reclaim obligation as a paragraph of its own after the atomicity paragraph, covering the creation finalize block, the §15.1 start transition and the §7.3 re-attach, with the obligation's begin and end boundary, its `leaked` disposition, the exclusive-pod case and the tree hazard; §7.2's mid-resume snapshot-close sequence loses its premise sentence, gains one sentence in step 2 and a replaced step 3; §5.2's slot-retry `**Max retries:**` bullet states the placement constraint; §7.3's resume flow, §6.2's mid-resume cancel bullet and §4.7.9 step 5 point at §7.1.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. §5.2's `**Slot cleanup:**` action list gains the slot's credential directory and the §4.9 timer cancellation, and its `**Scrub model.**` paragraph covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-4. §6.2 gains the `receiving_uploads → slot_cleanup` edge and the pre-`running` cleanup paragraph, which cites the §5.2 sentence S3 lands. Tier 11 is deferred to S5 and tier 1 to S6, because the reader-facing table and the transcribed edge list are the two artifacts those gates reconcile against this block.
      Tiers 0. Depends on: S3
- [ ] **S5 · docs** — DOCS-1. The per-slot sub-state table in the state-machine reference gains the row matching the new §6.2 edge.
      Tiers 0, 11. Depends on: S4
- [ ] **S6 · code** — CODE-3. `ValidTransitions()` and its doc comment in `pkg/sandbox/slotstate` gain the new edge, and `TestValidTransitions_spec_6_2` moves to the seven-edge set with a positive and a negative `IsValid` assertion.
      Tiers 0, 1. Depends on: S4
- [ ] **S7 · code** — CODE-1. The adapter's `Shutdown` releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given, reading that last predicate through the new `runtimeHoldsLocked` accessor in `pkg/adapter/runtimegeneration.go`.
      Tiers 0, 1. Depends on: S1, S3
- [ ] **S8 · code** — CODE-2. `noteRuntimeStarted` confirms the slot survived and reports whether it did, and `StartSession` and `Resume` take the session back off the runtime and refuse the start when a reclaim landed after their claim, reporting no cleanup outcome. The reverse ordering, in which the claim re-creates the entry the confirmation reads, is an accepted failure mode rather than a deliverable. Its tier-1 work includes re-fixturing the shipped callers that record without a bound entry.
      Tiers 0, 1, 7a. Depends on: S2, S7
- [ ] **S9 · code** — CODE-4. The gateway sends the compensating `Shutdown` at every post-connection bind failure and at a failed `Resume`, on the still-open connection and a detached context, releases the session's credential leases on the bind paths, and carries the outcome as the `leaked` disposition through `ReleaseSlotReservation`.
      Tiers 0, 1, 4, 7a. Depends on: S2, S3, S7, S8
- [ ] **S10 · code** — CODE-5. One `accountSlotFailure` helper serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold, and the retry after an unacknowledged reclaim carries `ExcludePods` so `ClaimSlot` places it on a different pod.
      Tiers 0, 1, 2, 4. Depends on: S9
