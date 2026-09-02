# Non-spec changes: Scope the coordination generation to the session

The staged changes below target the schema, the adapter code, and the tests. The caveat that opens the
proposal's "Proposed changes" section applies to them.

**IMPLEMENTOR TO FILL THE BLANKS.** These are indicative targets; the text is written during convergence,
against the post-0073 state of each file.

### SCHEMA-1. Make the comments true

`schemas/lenny-adapter.proto`: every doc comment SPEC-2 names as a wire carrier takes the wording SPEC-2
states for it, so the wire text and the applied specification state one rule apiece. The carriers are the
`CoordinatorFence` RPC comment, the message-level `CoordinatorFenceRequest` and `CoordinatorFenceResponse`
comments, the `CheckpointBarrier` RPC comment, the message-level `CheckpointBarrierRequest` comment, the
`CheckpointBarrierRequest.coordination_generation` field comment, and the `coordination_generation` field
comments on `SendMessageRequest`, `AttachRequest`, `RotateCredentialsRequest`,
`ExtendCredentialLeaseRequest`, `RevokeCredentialsRequest`, `InterruptRequest`, `CheckpointRequest`,
`SignalDeadlineRequest`, `ResumeRequest`, `ExportPathsRequest`, `ReportUsageRequest`, and
`ShutdownRequest`. The `CoordinatorFenceRequest.coordination_generation` field comment is the one carrier
that takes no edit: it already states per-session monotonicity, and SPEC-2 records that it keeps its
wording.
`make generate-proto` (`Makefile:91-94`) regenerates the committed stubs from the edited proto, and they
land in the same commit as it, because `TestProtoStubsMatchGeneratedOutput`
(`tests/tier0_static/proto_no_drift_test.go:70`) reproduces that target and diffs its output against the
committed `pkg/proto` tree at tier 0. protoc-gen-go copies a leading comment into the stub verbatim
(`schemas/lenny-adapter.proto:1451` reappears at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`), so a
field or message comment lands in `pkg/proto/adapter/v1/lenny-adapter.pb.go` and an RPC comment lands in
`pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` (`:180`, `:632`). Both stubs are generated output and are
never hand-edited.

### CODE-1. Move the state

`pkg/adapter/coordination.go`: `coordinationState`, with its `lastFenced`, `initialized`, and `quiesced`
fields together, moves onto the slot registry entry `slotState` (`pkg/adapter/slot.go:21`). The `coord
coordinationState` field on `Server` (`pkg/adapter/server.go:302`) is removed, and `hold holdState` at
`:307` stays under D5 with only its `gen` field dropped by CODE-3. `initialized` moves with `lastFenced`,
because leaving it on `Server` flips it on the first fence anywhere on the pod and makes every later
co-tenant's first fence report a gap, which is D6's cascade.

`barrierGate` (`pkg/adapter/server.go:314`, declared at `pkg/adapter/coordination.go:148`) moves onto the
same entry. §10.1.8 step 3 already fixes the gate's unit at the session, because the gateway opens the
`Checkpoint` stream for each quiesced session concurrently with that session's `CheckpointBarrier` RPC and
the ack echoes the checkpoint id that session's stream carried. The single pod-wide gate is therefore a
pre-existing departure from shipped specification text rather than a requirement this proposal creates, and
D7 together with the counter baseline turns it into the ordinary co-tenant drain outcome: the gateway
dispatches to every target concurrently (`pkg/gateway/coordination/barrier/barrier.go:190-201`), `open()`
overwrites `done`, `checkpointID`, and `signaled` unconditionally (`pkg/adapter/coordination.go:158-166`),
and `link()` records a stream's checkpoint id into whichever gate is open with no session check (`:180-188`),
so two barriers landing in one waiting slot leave the first blocked to the shared ack deadline and returning
an empty or cross-linked `checkpoint_ref` that `dispatchOne` persists into `session_checkpoint_meta` under
the wrong session id (`pkg/gateway/coordination/barrier/barrier.go:238-245`). The gate keeps its own leaf
mutex on the entry, independent of `coord.mu`.

`CoordinatorFence`, `CheckpointBarrier`, and `Checkpoint` each resolve the entry once for the session
the request names, under the guard that already precedes the resolve on that path: `checkSessionBound`
for the first two (`pkg/adapter/coordination.go:89`, `:216`) and `checkpointRootsForSession` for the
stream (`pkg/adapter/checkpoint.go:94`). Each holds the resolved pointer for the life of the call, the
barrier's deferred quiescence clear and the stream's deferred `complete()` included, because both
deregistration paths delete the map key and return the pointer with no field zeroed: `Shutdown`
(`pkg/adapter/session.go:237-239`) and the hold timeout's `deregisterStartedSessions`
(`pkg/adapter/slotsession.go:347-361`). A session deregistered mid-call therefore leaves the pointer
valid where a second lookup by session id returns nothing. The quiesce-and-hold in `CheckpointBarrier`
(`pkg/adapter/coordination.go:245-269`) sets and clears `quiesced` on the resolved entry and opens and
releases that entry's gate, and `Checkpoint` links `CheckpointStart`'s checkpoint id into the same
entry's gate (`pkg/adapter/checkpoint.go:122`) and completes it on stream termination (`:124`).

`checkpointRootsForSession` (`pkg/adapter/slot.go:153-166`) returns the resolved `*slotState` alongside
the roots, so the stream links and completes on the entry its own `FailedPrecondition` guard validated.
That guard does not make a second lookup safe. `s.ops.Begin` (`pkg/adapter/checkpoint.go:111`) admits a
checkpoint for a session identifier no pending checkpoint names and queues it behind the running one
(`pkg/adapter/oplock.go:119-129`), so the interval between the guard and the link is bounded by a
co-tenant session's whole upload, and either deregistration path can run inside it. A re-lookup there
would return nothing, the waiting `CheckpointBarrier` would never be linked, and its ack would carry an
empty `checkpoint_ref` after blocking to the gateway's ack deadline. `restoreChunks`
(`pkg/adapter/resume.go:178`) is the helper's other caller and discards the returned entry.

The pod-wide accessors `LastFencedGeneration()`, `isQuiescedForBarrier()`, and `BarrierWaiting()`
(`pkg/adapter/coordination.go:44`, `:52`, `:63`) become per-session reads. `Server.LastFencedGeneration`
takes a session id and returns zero for a session with no recorded value, and no pod-wide variant survives
beside it. Its only production caller is the hold's pod-level arming read at
`pkg/adapter/holdstate.go:119`, which sits where no session id exists, so CODE-3 deletes that read rather
than giving it an argument, and reads each terminated session's value off the `*slotState` the
`heldSession` already carries (`pkg/adapter/slotsession.go:283-285`). CODE-1 and CODE-3 therefore land in
one step, because no tree between the two deliverables compiles. `coordfixture.Pod`'s `Fence`,
`LastFenced`, and `StaleRPCRejected` take the session key
(`tests/testinfra/coordfixture/coordfixture.go:109`, `:115`, `:122`), so `FenceReadopter.ReadoptAndFence`
(`:220-241`) fences the session it is adopting rather than the one `StartPod` named, which is the session it
passes today.

CODE-1 reaches tiers 0, 1, 2, 4, 7a, and 8.

### CODE-2. The barrier reads the same place

`pkg/adapter/coordination.go:236`: the generation gate refuses the barrier when `initialized && gen !=
fenced` holds for the entry resolved for the session the request names, which is D7 and staged §10.1.2
step 3, whose comparison operator §7's first open decision owns. The entry the gate reads, and every other
site that reads or writes the relocated state, is the one CODE-1's resolve rule fixes.

### CODE-3. Hold state

`pkg/adapter/holdstate.go`: the hold stays pod-scoped under D5, and its arming signal, its rejection set,
and its termination set are unchanged. The `gen` field on `holdState` (`:43`) is dropped, and with it the
pod-wide `LastFencedGeneration()` read that arms the hold and is the field's only writer (`:119`, `:128`),
`onHoldTimeout`'s read of the field, which is its only reader (`:187`), and the generation parameter that
read passes to `terminateHeldSession` (`:206`) and on to `writeHoldPostMortem`. Those sites are the whole
of what the field drags. The pod-level `coordinator_connection_lost` line carries the started-session count
and no generation (`:130-132`). `terminateHeldSession` (`:225`) and `writeHoldPostMortem` (`:283`) read
each terminated session's own last fenced generation off the `*slotState` the `heldSession` already carries
(`pkg/adapter/slotsession.go:282-285`), and report zero for a session no coordinator fenced on that pod.

CODE-3 reaches tiers 0 and 1, and it lands in the same step as CODE-1, which runs CODE-1's tier set:
CODE-1 removes the accessor the arming read calls and `holdState.gen` has no other writer, so no tree
between the two deliverables compiles. The hold carries no tier-8 case: the chaos suite has no
coordinator-loss hold coverage, and tier 8 reaches this change through CODE-1's fixture accessor and
CODE-4's baseline rather than through the hold.

### CODE-4. The session row's counter is baselined at 1

`migrations/0181_sessions_coordination_generation_baseline.up.sql` sets
`sessions.coordination_generation` to `DEFAULT 1`, backfills with
`UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0`, and sets
`coordination_lease.coordination_generation` to `DEFAULT 1`
(`migrations/0164_coordination_lease.up.sql:44`) so the mirror column states the same baseline as the row
it mirrors. It leaves the inline `CHECK (coordination_generation >= 0)` that
`migrations/0050_session_record_fields.up.sql:38-39` created on the session row in place. 0181 is the next
free migration number, since `migrations/0180_drop_checkpoint_slot_id.up.sql` is the last one taken. The
`.down.sql` restores the `DEFAULT 0` and rolls no row back, because §4.2 states the counter is never
reset.

`pkg/gateway/session/sessionstore/pgstore/pgstore.go` `Create` (`:140`) floors a zero
`CoordinationGeneration` to 1 before the insert, beside the `schemaVersion == 0` normalisation already
there (`:244-248`), and `pkg/gateway/session/sessionstore/memstore/memstore.go` `Create` (`:46`) takes the
same floor beside its own `SchemaVersion` normalisation (`:58-61`). `Create` names
`coordination_generation` in its insert column list (`pgstore.go:177`), so the column default baselines
nothing and the two `Create` floors are the whole enforcement. The migration and both floors land in one
commit as one deliverable. 0181 does not tighten the session row's check to
`CHECK (coordination_generation >= 1)`. The migrate Job is a Helm `pre-install,pre-upgrade` hook at weight
-5 that completes before the gateway Deployment rolls
(`charts/lenny/templates/migrate-job.yaml:10-16`, `:37-39`), so the schema is ahead of the binaries for the
whole rolling window and every insert the still-running old fleet issues through `pgstore.Create` writes an
explicit zero. §10.5 states the rule for that case: mixed-version replicas must coexist during rollout, and
a constraint that old-version writes violate belongs to a Phase 3 migration in a subsequent deployment. The
retained `>= 0` check accepts those inserts, and the two `Create` floors baseline every row the new
binaries write.

`pkg/gateway/coordination/coordfence/coordfence.go:147-153` loses its floor of a non-positive row value
and the comment on it. The value read at `:143` is sent as it stands.

When the baseline does not fire and a row still carries 0, the fence carries 0 and the adapter refuses it
with `InvalidArgument` (`pkg/adapter/coordination.go:93-94`), and a barrier carrying 0 is refused the same
way (`:224-226`). Both refusals are loud and fail closed. The two `Create` floors are what keep a new row
off zero, and the session row's `CHECK (coordination_generation >= 0)` is unchanged. A row an old binary
wrote at 0 during the rolling window takes that same refusal until its first takeover bumps it.

CODE-4 reaches tiers 0, 1, 2, 3, 4, 7a, and 8.

### TEST-1. The co-tenant handoff case

The cases land in `pkg/adapter/coordination_test.go`, `pkg/adapter/holdstate_test.go`,
`pkg/adapter/checkpoint_stream_test.go` (the mid-flight deregistration case, which needs the external
`adapter_test` package),
`tests/tier4_integration` on proposal 0060's two-replica harness, whose real `Sweeper` drives
`coordfixture.FenceReadopter` and a genuine `CoordinatorFence` over the in-process adapter
(`tests/testinfra/coordfixture/coordfixture.go:220-241`, `:231`), and `tests/tier7a_load_local`. §8 states
the assertions each case makes and the tier each runs at.

TEST-1 reaches tiers 1, 4, and 7a.

## 8. Testing

The tiers this change reaches are 0, 1, 2 (the registry state, the migration, and the Postgres session
store's floor), 3 (the wire gate's behavior), 4 (a handoff that crosses the gateway, the lease store, and
the pod), 7a (concurrent handoffs), and 8 (crash takeover). Proposal 0060 built a two-replica gateway
harness and tier-8 crash-takeover coverage for §10.1; read what it built before designing here. The cases
split by what each tier can observe. The per-session fenced value, the hold records, the independence of
the barrier gates, and the lifetime of a resolved registry entry across a deregistration are observable
only inside `pkg/adapter` and are pinned at tier 1. The handoff a co-tenant's fence is refused on today
crosses the gateway's sweeper, the lease store, and the pod, so it is pinned on proposal 0060's harness
at tier 4. Production runs that path through `coordfence.Fencer`'s retry-and-relinquish loop
(`pkg/gateway/coordination/coordfence/coordfence.go:155-188`). The harness substitutes
`coordfixture.FenceReadopter`, which issues the same `CoordinatorFence` RPC without the policy loop
(`tests/testinfra/coordfixture/coordfixture.go:231`), and the case asserts the pod's per-session
verdict, which the substitution does not change. Tier 1 runs under `-race`
(`cmd/lenny-test/cmd_run.go:880`), so a case that arranges concurrent calls only as the way to reach
process-local state, such as the entry-lifetime case below, stays at tier 1. The cases whose subject is
contention itself, where the assertion is that each of two co-tenant sessions' concurrent RPCs records and
returns its own state, are pinned at tier 7a under `-race`.

`tests/testinfra/coordfixture` carries no build tag, so tier 0's `go vet ./...` compiles the fixture
itself and catches the accessor change inside it. Its callers do not compile at tier 0:
`tests/tier4_integration`, `tests/tier7a_load_local`, and `tests/tier8_chaos` carry the `integration`,
`load_local`, and `chaos` build tags, and tier 0 vets the untagged tree and the contract tree alone
(`cmd/lenny-test/cmd_run.go:498-508`). Run `go vet` under each of those tags after
CODE-1. A signature change left unvetted there surfaces as a compile failure when those tiers run, which
loses every case in the package at once rather than failing one assertion.

The cases that pin this defect follow. Each must fail against the pre-fix code, except where a bullet
states otherwise.

- Tier 1, `pkg/adapter/coordination_test.go`: one `adapter.Server` with `sess-a` and `sess-b` both bound
  and started. A fence of `sess-a` to 7 followed by a fence of `sess-b` to 2 is accepted with
  `gap_detected` false, where the pre-fix pod rejects it with `FailedPrecondition` and
  `coordinator_handoff_stale` because 2 is not above the pod-wide 7 (`pkg/adapter/coordination.go:99`). A
  `CheckpointBarrier` for `sess-b` at 2 is accepted, where the pre-fix gate refuses it because 2 does not
  equal 7 (`:236`). `LastFencedGeneration` for `sess-a` still reads 7 after both. On a fresh pod with
  `sess-a` fenced at 7, `sess-b`'s first fence at 9 logs no `coordinator_generation_gap`, where the
  pre-fix pod-wide `initialized` makes it a gap (`:108-116`).
- Tier 1, `pkg/adapter/coordination_test.go`: two bound sessions hold their own barrier gates. Each
  `link` and `complete` reaches only the barrier of the session whose `Checkpoint` stream carried it, a
  session holding no open gate ignores the other session's link, and each accepted barrier's ack carries
  the checkpoint id its own stream linked. Against the single pod-wide gate the second `open()`
  overwrites the first's channel, checkpoint id, and signal (`pkg/adapter/coordination.go:158-165`).
- Tier 1, `pkg/adapter`: a `Checkpoint` stream that has passed `checkpointRootsForSession` and is queued
  on the pod-level op lock behind a co-tenant session's checkpoint has its own registry entry
  deregistered before it reaches the link site, either by a `Shutdown` for that session or by the
  coordinator-loss hold timeout. The stream still links its checkpoint id into the entry the waiting
  `CheckpointBarrier` holds, that barrier's ack carries that id rather than an empty `checkpoint_ref`,
  the barrier's deferred quiescence clear runs against the detached entry and the RPC returns rather
  than panicking, and a co-tenant session's open barrier is unaffected. The case lands in the external
  `adapter_test` package beside the concurrent co-tenant stream fixture
  (`pkg/adapter/checkpoint_stream_test.go:417`, with `driveCheckpointConc` at `:384`, `concurrentServer`
  at `pkg/adapter/slot_test.go:24`, `slotStartReq` at `:37`, and `adapterClient` at
  `pkg/adapter/server_test.go:90`), because `pkg/adapter/coordination_test.go` is `package adapter` and
  cannot reach them. Both deregistration paths remove the session's slot tree
  (`pkg/adapter/session.go:271`, `pkg/adapter/holdstate.go:249`), so the assertions are on the link, the
  ack, and the return rather than on a successful `CheckpointSummary`, which can fail mid-upload. This
  case does not fail against the pre-fix code, whose single pod-wide gate is never absent. It fails
  against a reading of CODE-1 that re-looks the entry up by session id at the link site, which is what
  makes it the case that pins CODE-1's rule that each handler holds the resolved pointer for the life of
  the call. Tier 1 runs under `-race` (`cmd/lenny-test/cmd_run.go:880`) and the path is observable only
  inside `pkg/adapter`, so it needs no higher tier.
- Tier 1, `pkg/adapter/holdstate_test.go`:
  `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` (`:674`) fences `sess-a` at 7 and
  then asserts one expected `lastGeneration` for both terminated sessions (`:700-716`). Its post-mortem
  block becomes a per-session table. `sess-a`'s `coordinator_lost` line and post-mortem carry 7,
  `sess-b`, which no coordinator fenced, carries 0, and the pod-level `coordinator_connection_lost` line
  carries `started_sessions` and no `last_generation` key (`pkg/adapter/holdstate.go:130-132`). This case
  is an amendment of a landed one rather than a new case, and it is the disposition CODE-3 needs so that
  the step landing CODE-3 does not turn tier 1 red.
- Tier 4, `tests/tier4_integration`: on proposal 0060's two-replica harness, one pod holds two sessions
  (`coordfixture.StartPod` starts `sess-a`, and `sess-b` is started over the pod's already-dialed
  `Pod.Client`, `tests/testinfra/coordfixture/coordfixture.go:76`, `:98-102`) and replica 1 coordinates
  both. The two rows are seeded apart, `sess-a` at 7 and `sess-b` at 2, and the case drives replica 1's
  at-bind fence for `sess-a` to 7 explicitly, as the landed single-session case does
  (`tests/tier4_integration/coordination_fence_split_brain_test.go:83`), because nothing fences a
  normally-started session. `sess-b` is never fenced on that pod. Replica 1's lease on `sess-a` stays
  live, so when `sess-b`'s lease lapses the survivor's `Sweeper` adopts `sess-b` alone, skipping `sess-a`
  on `ErrHeld` (`pkg/gateway/coordination/coordination/coordination.go:341`), bumps only `sess-b`'s row to
  3, and drives a genuine `CoordinatorFence` for `sess-b` at 3 through `coordfixture.FenceReadopter`. The
  fence is accepted on `sess-b`'s own unset entry with `gap_detected` false, the binding is published, and
  `sess-a`'s value on the pod still reads 7. Against the pre-fix code the pod-wide `lastFenced` is 7, so 3
  is refused with `coordinator_handoff_stale` (`pkg/adapter/coordination.go:99`), `FenceReadopter`
  releases the lease and returns an error (`tests/testinfra/coordfixture/coordfixture.go:220-241`), the
  `Sweeper` records an adoption backoff (`pkg/gateway/coordination/coordination/coordination.go:408`,
  `:512-517`), no binding is published, and `sess-b` stays unadoptable until that backoff elapses.
- Tier 7a, `tests/tier7a_load_local`: two concurrent `CoordinatorFence` calls for different co-tenant
  sessions on one pod each record their own value, under `-race`.
- Tier 7a, `tests/tier7a_load_local`: two `CheckpointBarrier` RPCs accepted concurrently on one pod each
  receive their own stream's checkpoint id in the ack, neither ack carrying an empty `checkpoint_ref` nor
  the co-tenant's id, and both return well inside the barrier ack deadline, under `-race`. The two
  `Checkpoint` streams do not run concurrently: the pod-level op lock admits one checkpoint at a time and
  queues the distinct co-tenant session id behind the running one (`pkg/adapter/checkpoint.go:111`,
  `pkg/adapter/oplock.go:117-128`), so the second barrier's ack returns only after the first stream's
  archive finishes and the case asserts nothing about the two acks' relative timing. That op lock is
  shipped behavior no deliverable here changes. Against a pod-wide gate the second `open()` replaces the
  first barrier's channel, checkpoint id, and signal (`pkg/adapter/coordination.go:158-165`), so the first
  barrier is never signalled and blocks to its ack deadline.
- Tier 8, `tests/tier8_chaos/coordination_crash_takeover_test.go`: the landed crash-takeover coverage is
  amended rather than extended. Its `pod.LastFenced()` reads at `:150`, `:195`, and `:223` take the session
  key CODE-1 gives `coordfixture.Pod.LastFenced` (`tests/testinfra/coordfixture/coordfixture.go:115`), and
  its generation assertions shift under CODE-4's baseline as stated below. A failure there means the
  survivor's fence did not land on the adopted session's own registry entry, so the pod's per-session
  fenced record did not survive the coordinator crash. This is the disposition CODE-1 and CODE-4 need so
  that the steps landing them do not turn tier 8 red.

The persisted half of that last case, two `session_checkpoint_meta` rows carrying distinct
`checkpoint_ref` values for two co-tenant sessions drained together, rides proposal 0060's existing
multi-replica drain coverage rather than a tier of its own.

**IMPLEMENTOR'S CHOICE:** for each new case whose own bullet leaves these open, the helper that binds
and starts a second session on one `adapter.Server`, and which existing tier-1 file in `pkg/adapter` the
case lands in beside the files named above. The mid-flight deregistration case is fixed rather than open.
Its bullet places it in `pkg/adapter/checkpoint_stream_test.go` in the external `adapter_test` package
and names the fixtures it binds the second session with. The constraint is that the assertions above are
the ones made, that each new case carries a `// spec:` annotation naming §10.1.2 and §10.1.8, and that
the amended hold case keeps its current name so that the `// diagnosis:` comment already attached to it
stays attached.

The cases that pin CODE-4's baseline follow. Each carries a `// spec:` annotation naming §10.1 and §4.2. A
tier-1 case in `memstore` asserts that `Create` of a `Session` with a zero `CoordinationGeneration` writes
1. That inverts the landed `TestCreateDefaultsSessionRecordFields`
(`pkg/gateway/session/sessionstore/memstore/memstore_test.go:309-325`), whose doc comment states that the
generations start at zero, so the case is an amendment of a landed one and the step landing CODE-4 amends
it rather than leaving tier 1 red. The `pgstore` half of the same floor is a tier-2 case in
`tests/tier2_component/stores/sessionstore_test.go`, which builds the store over a Postgres container with
the production migrations applied (`:79`), because `pgstore.New` takes a `*pgxpool.Pool` and `Create` runs
its insert through `pgtenant.InTx` (`pgstore.go:60`, `:249`), so that path does not run at tier 1. The case
asserts that `Create` of a session with a zero `CoordinationGeneration` reads back 1. The landed tier-1
case `TestFenceZeroGenerationFencesAtBaseline`
(`pkg/gateway/coordination/coordfence/coordfence_test.go:173-183`) asserts the floor CODE-4 deletes, so the
step landing CODE-4 amends it in the same commit rather than leaving tier 1 red: it keeps its
zero-returning generation reader (`:177`) and asserts the fencer sends 0 rather than 1, its doc comment is
restated so it no longer claims a baseline floor, and the adapter's `InvalidArgument` on a non-positive
generation (`pkg/adapter/coordination.go:93-94`) is named there as the backstop. A tier-2 case over
migration 0181 lands in a new file under `tests/tier2_component/migrations/` and asserts that the migration
backfills a row carrying 0 to 1, that `sessions.coordination_generation` and
`coordination_lease.coordination_generation` both default to 1, that the session row's
`CHECK (coordination_generation >= 0)` is left in place so an insert at 0 from an old binary is still
accepted during the rolling window, and that the `.down.sql` restores the `DEFAULT 0`. That directory is
fixed rather than incidental: pass 3 of `scripts/lint-migrations.sh`
(`:45`, `:74-88`) runs inside tier 0 (`cmd/lenny-test/cmd_run.go:635-641`) and fails when a migration's
sequence number is referenced by no file under it, so a case landing in `tests/tier2_component/stores/`
alone leaves tier 0 red. Migration 0181 also takes the entry `{migration: "0181", table: "sessions"}` in
`tests/tier2_component/migrations/prod_columns_test.go`. A migration that adds no column still takes an
entry there, naming its table and no columns, which is how `migrations/0180_drop_checkpoint_slot_id.up.sql`
(`prod_columns_test.go:583`) and migration 0112 (`:295`) sit in that suite.
`TestProdMigrationsRollBackPerStep` (`:610`) walks `prodMigrationSchema` alone, highest number first, and
calls `MigrateTo(number-1)` for each entry, so a migration absent from the table never has its `.down.sql`
applied as its own step. The behavioral assertions live in the migration's own file, as
`tests/tier2_component/migrations/checkpoint_slot_id_drop_test.go` holds 0180's. A
tier-2 case asserts that a resume fence at 1 followed by a crash-takeover compare-and-swap to 2 is accepted
rather than rejected as `coordinator_handoff_stale`, and it must fail against the pre-fix code. The tier-3
wire case D7 stages asserts that a `CheckpointBarrier` naming a bound session the pod holds no fenced
generation for is accepted and records no value, and the baseline is what makes that case reachable,
because the barrier now carries the session's own row value rather than the zero the adapter refuses first.
Its landed counterpart `TestCheckpointBarrierRejectsWithoutFence`
(`pkg/adapter/coordination_test.go:184-197`) asserts the refusal D7 retires, so the step landing CODE-2
amends it in the same commit rather than leaving tier 1 red. Tier 4 covers the same flow across the
gateway, the session store, and the pod.

The baseline shifts landed tests in two classes, and one further landed case sits outside both, the
coordfence baseline-floor case named above. The step that lands CODE-4 corrects all three. The first
class is every assertion that reads a session row's `CoordinationGeneration` after a create that left the
field unset. Each shifts by one, because each such assertion reads either the baseline itself or a number
of handoff bumps counted from it: the assertions in `tests/tier2_component/coordination/sweep_test.go`
(which run from `:275` to `:594`), those in `pkg/gateway/coordination/coordination`'s takeover test, whose
rows are seeded through `mustCreate` with the field unset (`coordination_takeover_test.go:74`, `:142`,
`:241`, `:301`), and the three in `tests/tier8_chaos/coordination_crash_takeover_test.go` at `:267`,
`:283`, and `:296`, whose session is seeded with the field unset (`:239-241`) and whose asserted 1, 1, and
2 become 2, 2, and 3. That tier-8 file also takes CODE-1's accessor edit, and the two edits land in
different steps: the step landing CODE-1 rescopes the `pod.LastFenced` reads at `:150`, `:195`, and
`:223` and leaves the three assertions at 1, 1, and 2, and the step landing CODE-4 shifts them. The two
edits sit on disjoint lines, those reads belonging to the two subtests whose sessions are seeded at 1
explicitly (`:118`, `:179`), so neither step turns tier 8 red.
`tests/tier7a_load_local/coordination_colocation_race_test.go` is the other file taking both a CODE-1
accessor edit and a CODE-4 baseline shift, and it splits the same way. Its `pod.LastFenced` read at
`:260` takes the accessor edit in the step landing CODE-1, while its `CoordinationGeneration: 0` seed
at `:144`, written explicitly through `memstore.Create`, and its assertion of 0 at `:287-288` take the
baseline in the step landing CODE-4, so tier 7a is green at both steps. That file's other session is
seeded at 1 (`:130`) and its assertion of 2 (`:264-265`) is already correct under the baseline.

The second class is a fixture that seeds some other row's generation as a constant chosen relative to a
session row created unset. Its one site is `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1`
(`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go:992`), which seeds a prior active manifest row
at `CoordinationGeneration: 1` as a fenced newer writer (`:1007`) against a session that `runningSession`
creates with the field unset (`checkpointer_test.go:89-96`). That constant becomes 2 under the baseline,
and the case's own doc comment restates the incoming attempt's generation as 1 rather than 0 (`:993-995`).
Leaving it costs the assertions rather than the wording alone: the supersede guard
(`pkg/gateway/checkpoint/checkpointer/uploaddriver.go:422`) and the store's fence
(`pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:394`) both compare strictly greater,
so a prior row at 1 against a session generation the baseline floors to 1 satisfies neither, the
stale-generation rejection never fires, and the case fails at its `t.Fatal` (`:1015`) and at each assertion
that the higher-generation writer's row, reservation, chunk prefix, and supersede counter were left
untouched. The step landing CODE-4 amends it in the same commit rather than leaving tier 1 red. A sweep of
the seeded `CoordinationGeneration` constants in the test tree finds no other fixture whose constant is
chosen against a session row the fixture creates unset, so this class has one site. An assertion over a
generation a fixture seeds explicitly above the baseline is unaffected, which covers
`pkg/gateway/coordination/coordination/coordination_mirror_test.go:116`, whose `s1` row is seeded at 2
(`:84`) and whose two rows seeded with the field unset (`:85-86`) are never asserted on the generation,
`pkg/gateway/coordination/barrier/wiring_test.go:171`, and
`pkg/gateway/coordination/coordlease/coordlease_test.go:37`, `:58`. Those three files take no edit and are
absent from §9 for that reason. A constant seeded at 1 falls outside that exemption, because the baseline
makes it equal to the session row's value rather than above it, which is what puts the supersede case in
the second class. The raw-SQL session fixtures omit the column and take its default, which the migration
moves to 1. No check is tightened, so no seed path breaks.

## 9. Files touched on application

- `spec/10_gateway-internals.md` (§10.1, §10.1.2, §10.1.4, and §10.1.8)
- `spec/28_communication-channels.md`
- `spec/29_communication-scenarios.md`
- `spec/04_system-components.md`
- `schemas/lenny-adapter.proto`
- `pkg/proto/adapter/v1/lenny-adapter.pb.go` and `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go`
  (regenerated from the proto by `make generate-proto`; generated output, never hand-edited)
- `migrations/0181_sessions_coordination_generation_baseline.up.sql` and its `.down.sql`
- `pkg/adapter/coordination.go`
- `pkg/adapter/server.go`
- `pkg/adapter/checkpoint.go` (the barrier link and the deferred complete run on the entry the stream
  resolved)
- `pkg/adapter/holdstate.go`
- `pkg/adapter/resume.go` (`restoreChunks` is `checkpointRootsForSession`'s other caller and discards the
  returned entry)
- `pkg/adapter/slot.go` (the registry entry `slotState` gains the coordination state and the barrier gate,
  and `checkpointRootsForSession` returns the resolved entry alongside the roots)
- `pkg/adapter/slotsession.go` (`heldSession` and the deregistration paths carry each terminated session's
  own value)
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`
- `pkg/gateway/session/sessionstore/memstore/memstore.go` and `memstore_test.go`
- `pkg/gateway/coordination/coordfence/coordfence.go` and `coordfence_test.go`
- `pkg/gateway/coordination/coordination/coordination_takeover_test.go`
- `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go`
- `pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go` (the fenced-newer-writer constant in
  `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` takes the baseline)
- `pkg/adapter/coordination_test.go`, `pkg/adapter/holdstate_test.go`, and
  `pkg/adapter/checkpoint_stream_test.go` (the mid-flight deregistration case)
- `tests/testinfra/coordfixture/coordfixture.go` (its `Pod` methods take the session key, and its fence
  comment names the exemption as the first fence for a bound session on that pod rather than the first
  fence on the pod, `:106-108`)
- `tests/tier2_component/stores/sessionstore_test.go` (the tier-2 half of CODE-4's `Create` floor)
- a new file under `tests/tier2_component/migrations/` (the tier-2 case over migration 0181; that
  directory is where `scripts/lint-migrations.sh` pass 3 looks for the reference)
- `tests/tier2_component/migrations/prod_columns_test.go` (the `prodMigrationSchema` entry for 0181, which
  is what makes `TestProdMigrationsRollBackPerStep` step the migration's `.down.sql`)
- `tests/tier2_component/coordination/sweep_test.go`
- `tests/tier4_integration/coordination_fence_split_brain_test.go`
- `tests/tier7a_load_local/coordination_colocation_race_test.go`
- `tests/tier8_chaos/coordination_crash_takeover_test.go`

## Resolved in adversarial review

### Pass 23 (2026-09-02, automated)

- **Migration 0181 no longer tightens the session row's check.** The staged migration dropped
  `0050`'s inline `CHECK (coordination_generation >= 0)` and added a named `CHECK (coordination_generation
  >= 1)`, and CODE-4 reasoned only about in-commit ordering. The migrate Job is a Helm
  `pre-install,pre-upgrade` hook at weight -5 that completes before the gateway Deployment rolls
  (`charts/lenny/templates/migrate-job.yaml:10-16`, `:37-39`), so the tightened check would be live in
  Postgres while the whole old gateway fleet still served, and `pgstore.Create` binds
  `sess.CoordinationGeneration` with no floor (`pkg/gateway/session/sessionstore/pgstore/pgstore.go:177`,
  `:260`), so every old-binary session insert would be rejected for the rolling window. §10.5 places a
  constraint that old-version writes violate in a Phase 3 migration and a separate deployment. 0181 now
  carries the `DEFAULT 1` on both columns and the backfill alone, and the two `Create` floors are the whole
  enforcement. The `.down.sql` restores the `DEFAULT 0` only. CODE-4's migration paragraph, its commit
  paragraph, and its backstop paragraph, §8's tier-2 stores case, §8's tier-2 migration case, §8's closing
  seed-path sentence, and the summary's "Watch out for" paragraph all state that. A phase split was
  rejected: §10.5 forbids applying the Phase 3 file in this release, so staging it would leave a migration
  file with no step, and nothing in the proposal or the tree reads the `>= 1` check.
- **The landed coordfence baseline-floor case takes its disposition.**
  `TestFenceZeroGenerationFencesAtBaseline`
  (`pkg/gateway/coordination/coordfence/coordfence_test.go:173-183`) drives `fence` over a zero-returning
  generation reader and asserts the fencer put 1 on the wire, which is the floor CODE-4 deletes
  (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`). §8 staged a new tier-1 coordfence case and
  gave the landed one no disposition, so step S4's declared tier 1 would have gone red. §8 now states the
  amendment in place of the new case: the reader stays, the assertion becomes 0, the doc comment is
  restated, and the adapter's `InvalidArgument` on a non-positive generation
  (`pkg/adapter/coordination.go:93-94`) is the backstop. The two-class sentence now names it as the one
  landed case outside both classes, and the summary's list of the tests the CODE-4 step amends carries it.
  Class 2's "this class has one site" claim is scoped to that class and stands.
- **The tier-4 co-tenant case states the precondition that makes it discriminate.** As staged, nothing
  fenced `sess-a`, so `sess-b`'s fence was the first fence on the pod, and the pre-fix stale arm is guarded
  on `s.coord.initialized` (`pkg/adapter/coordination.go:99`, `:108`), which is false on an unfenced pod
  (`tests/testinfra/coordfixture/coordfixture.go:73-75`). The pre-fix code accepted the fence, so the case
  passed before and after the fix, against §8's own preamble. The bullet now seeds `sess-a` at 7 and
  `sess-b` at 2, drives replica 1's at-bind fence for `sess-a` to 7 explicitly as the landed single-session
  case does (`tests/tier4_integration/coordination_fence_split_brain_test.go:83`), keeps replica 1's lease
  on `sess-a` live so the sweeper adopts `sess-b` alone on `ErrHeld`, and has the takeover mint 3, so the
  pre-fix pod-wide value of 7 refuses 3 while the per-session pod accepts it and leaves `sess-a` at 7.
  Moving the case to tier 1 was rejected because its subject crosses the sweeper, the lease store, and the
  pod, and leaving the generations to the implementor was rejected because they are what makes the case
  discriminate.
- **The tier-7a barrier case no longer asserts that the two acks are independent in time.** An accepted
  `CheckpointBarrier` returns only when its gate is signalled by the `Checkpoint` stream's deferred
  `complete()`, and each stream first passes the pod-level op lock, which admits one checkpoint at a time
  and queues a distinct co-tenant session id behind the running one (`pkg/adapter/checkpoint.go:111`,
  `:122-125`, `pkg/adapter/oplock.go:117-128`, `:133-140`). The second barrier's ack therefore cannot
  return before the first stream's archive finishes, so "neither waits on the other" was false against the
  fixed implementation. The bullet now asserts that each ack carries its own stream's checkpoint id, that
  neither ack is empty or cross-linked, and that both return well inside the barrier ack deadline, and it
  states the op lock as shipped behavior this proposal does not change. §8's tier-split sentence, which
  generalised the tier-7a assertion as non-interference, now states that each concurrent RPC records and
  returns its own state, which covers both tier-7a bullets. Changing the op lock so co-tenant checkpoints
  run concurrently was rejected as a deliverable outside this proposal's subject.
