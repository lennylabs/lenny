# Deviations: Scope the coordination generation to the session

Where the landed code departs from what the proposal states. Recorded on 2026-09-07, after the
implementation run, from the deviations its build steps returned. Each entry names what the proposal
says, what landed, why, and what a later reader would otherwise get wrong.

## 1. The claim-register row's surface pointer was left stale (S5, CODE-1)

**The proposal says.** The defect entry on the missing §10.1.2 cancel-and-reset states that CODE-1
rewrites the doc comment the claim-register row's `surface` points at, "so that pointer is re-resolved
when CODE-1 lands". The row is `In-flight RPC cancellation on a generation gap`, status `ABSENT`,
deferral `R16`, surface `conceded at pkg/adapter/coordination.go:81-82` (`tests/claim-map.json:173-179`).

**What landed.** The pointer stands as it was. The concession sentences survived the rewrite and moved:
they are now at `pkg/adapter/coordination.go:103` (the `CoordinatorFence` doc comment) and `:140` (the
gap branch). Line `:81-82` now falls inside `BarrierWaiting`'s doc comment, which is a different
subject.

**Why no legal change closes it here.** `tests/claim-map.json` is generator-produced and gated
byte-for-byte by `TestClaimRegisterIsReproducibleFromItsGenerator`. Its row source is the root audit
record, which is neither a target of this step nor in the proposal's files-touched list. Editing the
register alone turns tier 0 red, and editing the audit record amends a historical finding.

**What a later reader gets wrong.** Someone following the row's surface to see the concession lands on
an unrelated accessor comment and may conclude the concession was removed, which would read as the gap
having been closed. It has not been: the adapter still performs no cancel-and-reset on a generation gap.

**Suggested next step.** Re-point the row through its generator when the audit record is next touched.
No gate reads the line range for an `ABSENT` row, so this is a correctness-of-record issue rather than
a build failure.

## 2. Migration 0181's backfill needs a tenant context the proposal does not state (S6, CODE-4)

**The proposal says.** CODE-4 states the backfill as
`UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0`, and says nothing
about the tenant context that write needs.

**What landed.** `migrations/0181_sessions_coordination_generation_baseline.up.sql:37-43` wraps the
backfill in `SET LOCAL lenny.allow_all_sentinel = 'true'` and `SET LOCAL app.current_tenant = '__all__'`,
restoring both to `DEFAULT` immediately after, which is what migration 0180's whole-table `sessions`
rewrite already does.

**Why.** The bare `UPDATE` is rejected by the §12.3 tenant guard trigger with
`lenny_tenant_guard: app.current_tenant is not set (SQLSTATE 42501)`. The tier-2 migration case caught
it. The pair is `SET LOCAL`, so it is confined to the migration's own transaction.

**What a later reader gets wrong.** Reading CODE-4 as the whole of the migration would suggest the guard
does not apply to migrations, and a later whole-table backfill written from that reading fails at apply
time.

**Suggested next step.** None for this proposal. A future proposal staging a whole-table write on a
guarded table should state the sentinel pair as part of the deliverable.

## 3. The baseline shift reaches two assertions §8 does not enumerate, in a file it names (S6, CODE-4)

**The proposal says.** §8 enumerates the landed assertions the baseline shifts and names, in
`pkg/gateway/session/sessionstore/memstore/memstore_test.go`, only `TestCreateDefaultsSessionRecordFields`
(`:309-325`).

**What landed.** Two further assertions in the same file shifted:
`TestUpdateAdvancesGenerationCounters` (asserted 2, now 3) and
`TestUpdateConcurrentGenerationBumpsPreserveMonotonicity` (asserted n, now n+1).

**Why.** Both read a generation after a create that left the field unset, which is the first class the
proposal itself defines. The enumeration missed them inside a file §9 already lists. Left un-shifted
they leave tier 1 red, so no legal implementation of CODE-4 avoids touching them.

**What a later reader gets wrong.** Treating §8's list as exhaustive when scoping a similar baseline
change.

## 4. The shift also reaches the fenced-generation assertions, not only the row reads (S6, CODE-4)

**The proposal says.** The §8 shift list for `tests/tier2_component/coordination/sweep_test.go` and
`pkg/gateway/coordination/coordination/coordination_takeover_test.go` names the `CoordinationGeneration`
assertions.

**What landed.** The fenced-generation assertions passed to the readopter in those two files
(`readopter.gens[0]` and `readopter.calls[0].generation`) shifted by one as well.

**Why.** Those read the post-handoff generation minted from a row created unset, so they are the same
class. The enumeration lists the row reads only.

**What a later reader gets wrong.** The same misreading as entry 3: that the baseline touches only what
reads the row directly, when it touches everything downstream of a row created unset.

## Not deviations

The run recorded none beyond the four above. `pkg/gateway/coordination/coordfence/coordfence.go:147-153`
keeps its non-positive floor deliberately, which CODE-4 states, so it is not a departure. Proposal 0076's
OD9 accepted that floor for this release and proposal 0080 §1.20 carries its retirement.
