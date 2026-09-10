## Implementation checklist

Every spec step leads. Each implementation step carries the tests for the tiers its line
names, per `## Testing` in the non-spec changes file.

- [ ] **S1 · spec** — SPEC-1. §4.1's scope sentence and the §4.7 `Shutdown` row state the slot release and the runtime teardown as two teardowns with two preconditions, and state the no-op answer for a session the adapter holds no entry for. §29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §7.1 gains the pod-side reclaim obligation as a paragraph of its own after the atomicity paragraph, covering the creation finalize block, the §15.1 start transition and the §7.3 re-attach, with the obligation's begin and end boundary, its `leaked` disposition, the exclusive-pod case and the tree hazard; §7.2's mid-resume snapshot-close sequence loses its premise sentence, gains one sentence in step 2 and a replaced step 3; §5.2's slot-retry `**Max retries:**` bullet states the placement constraint; §7.3's resume flow, §6.2's mid-resume cancel bullet and §4.7.9 step 5 point at §7.1.
      Tiers 0, 11. Depends on: —
- [ ] **S3 · spec** — SPEC-3. §5.2's `**Slot cleanup:**` action list gains the slot's credential directory and the §4.9 timer cancellation, and its `**Scrub model.**` paragraph covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release.
      Tiers 0, 11. Depends on: —
- [ ] **S4 · spec** — SPEC-4. §6.2 gains the `receiving_uploads → slot_cleanup` edge and the pre-`running` cleanup paragraph, which cites the §5.2 sentence S3 lands. Tier 11 is deferred to S6 and tier 1 to S7, because the reader-facing table and the transcribed edge list are the two artifacts those gates reconcile against this block.
      Tiers 0. Depends on: S3
- [ ] **S5 · spec** — SPEC-5. §4.7.1 gains the bind-epoch block after the RPC tables, stating what the adapter mints, when it reports it, and the caller rules; §15.4 gains the published bind-epoch contract and the slot-identifier occupancy contract after the SDK-warm demotion contract, each with its non-conformance statement.
      Tiers 0, 11. Depends on: S1
- [ ] **S6 · docs** — DOCS-1. The per-slot sub-state table in the state-machine reference gains the row matching the new §6.2 edge.
      Tiers 0, 11. Depends on: S4
- [ ] **S7 · code** — CODE-3. `ValidTransitions()` and its doc comment in `pkg/sandbox/slotstate` gain the new edge, and `TestValidTransitions_spec_6_2` moves to the seven-edge set with a positive and a negative `IsValid` assertion.
      Tiers 0, 1. Depends on: S4
- [ ] **S8 · schema** — SCHEMA-1. `schemas/lenny-adapter.proto` gains the `SlotReclaimOutcome` enum and the nine additive fields, `make generate-proto` runs, and the regenerated `pkg/gen/adapter/v1` package lands in the same commit. The two `WIRED` rows land in `tests/claim-map.json`. The edit takes rule S-2's second window, whose precondition is that every in-flight `pkg/adapter` handler edit has merged, so this step precedes every step below that opens a handler file.
      Tiers 0, 3. Depends on: S1, S5
- [ ] **S9 · code** — CODE-6. `pkg/adapter/bindepoch.go` lands the epoch counter and its lazy seed, `slotState.epoch`, the reclaim-hold side table and its accessors, the refusal sentinel, and the two shared resolve helpers; `ensureSlotStateLocked` gains the hold refusal and the epoch mint, `ensureSlotPaths` widens to return the epoch, the five resolve sites move to the shared helper, the seven handlers report the epoch, and the adapter client gains the epoch latch, `BindEpoch`, and `ShutdownReclaim`.
      Tiers 0, 1, 3. Depends on: S5, S8
- [ ] **S10 · code** — CODE-1. The adapter's `Shutdown` refuses a reclaim naming a bind epoch the entry does not carry, answering `SLOT_RECLAIM_OUTCOME_ABSENT` or `SLOT_RECLAIM_OUTCOME_SUPERSEDED` and removing nothing, takes the reclaim hold in the deregistration's own critical section with the release deferred, releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given, reading that last predicate through the new `runtimeHoldsLocked` accessor in `pkg/adapter/runtimegeneration.go`.
      Tiers 0, 1, 9. Depends on: S1, S3, S9
- [ ] **S11 · code** — CODE-2. `noteRuntimeStarted` takes the bind epoch its own claim observed and confirms the registry still holds an entry bound to this session at that epoch, and `StartSession` and `Resume` take the session back off the runtime and refuse the start when it does not, reporting no cleanup outcome. Both orderings are covered: the reclaim landing after the claim leaves no entry, and the reclaim landing before it leaves an entry at a different epoch, which is the ABA case. Its tier-1 work includes re-fixturing the shipped callers that record without a bound entry.
      Tiers 0, 1, 7a. Depends on: S2, S10
- [ ] **S12 · code** — CODE-4. The gateway sends the compensating `Shutdown` at every post-connection bind failure and at a failed `Resume`, on the still-open connection and a detached context, carrying the bind epoch the connection latched, and never re-dialling. It maps the answer outcome-first (RPC error leaked; `SUPERSEDED` or `ABSENT` not leaked whatever `exited_cleanly` reports; `RECLAIMED` and not clean leaked; otherwise not leaked), releases the session's credential leases on the bind paths, and carries the disposition through `ReleaseSlotReservation`.
      Tiers 0, 1, 4, 7a. Depends on: S2, S3, S10, S11
- [ ] **S13 · code** — CODE-5. One `accountSlotFailure` helper serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold, and the retry after an unacknowledged reclaim carries `ExcludePods` so `ClaimSlot` places it on a different pod.
      Tiers 0, 1, 2, 4. Depends on: S12
- [ ] **S14 · code** — CONF-1. `tests/tier10_conformance/bind_epoch_conformance_test.go` lands the four properties of §15.4's published bind-epoch contract: the epoch is minted per entry and strictly increases, one entry reports one epoch on every response, a superseded reclaim performs nothing, and a zero epoch is the unconditional teardown.
      Tiers 0, 10. Depends on: S5, S10
