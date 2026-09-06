# Proposal: Discharge the residues proposal 0073 recorded and deferred

- **Status:** EARLY DRAFT, NOT CONVERGED. This document is an inventory rather than a design. It states
  each gap and the evidence it was found from, and derives no remedy of its own. §1.16 is the one entry
  that states one, because another proposal's review derived it and left it without a home; it is
  reproduced here rather than invented here. The document is expected to be split into per-concern
  proposals once the entries have been triaged, and no entry should be implemented from this text.
- **Date:** 2026-08-31
- **Scope:** Collects the deferrals proposal 0073 recorded and left open, together with the register and
  audit entries that record the same gaps elsewhere, and which no current draft covers. It also holds a
  residue another proposal's review derived and declined to stage, which is §1.16. Proposals 0075,
  0076, 0078, and 0079 already take four of 0073's residues and are out of scope here; §2 names them so a
  reader can tell what is claimed from what is not.

This document stages no changes. It modifies no spec, code, doc, or register file, and its "Proposed
changes" section is deliberately absent, because the point of the inventory is to record what is
outstanding before anyone decides what to do about it.

## 0. How to read this

Each entry states the gap, then names the source that establishes it. The sources are:

- **0073 §9 Recorded limits.** The limits proposal 0073 wrote down as accepted at sign-off. A limit there
  is a property the proposal knew about and chose not to cure.
- **The claim register** (`tests/claim-map.json`, §28.4). A row with status `ABSENT` is a contract the
  specification states and the tree does not implement.
- **The spec-map exceptions** (`tests/spec-map-exceptions.yaml`). An entry with reason
  `pending-implementation` is a heading with no implementation key, carrying the blocker that retires it.
- **The skip register** (`tests/registers/skip-reasons.yaml`) and the `t.Skip` call sites it keys.
- **`BUILD-GAPS.md`.** Findings filed against the tree.
- **Another proposal's open decisions.** A decision a converged proposal put to a reviewer, recommended a
  successor take, and left without one. The entry names the proposal and the decision.

An entry that was verified against the tree while writing this document carries the file and symbol it was
verified at. Where a citation names a line, the line was read on 2026-08-31 and will drift.

## 1. The gaps

### 1.1 The drain gate keeps a read-then-act window

SCHEMA-1's drain gate decides the drain inside the handler's `s.mu` critical section and sends the
`terminate` frame after releasing the lock (`pkg/adapter/runtimeops.go`, the `terminate` frame at `:488`).
A session whose registry entry is bound after the decision and before the send is served by a runtime that
has already been told to exit within `deadlineMs`.

0073 records two residues here and distinguishes them. The hard close leaves a pod whose next
`Runtime.Start` fails and which §5.2 drains. A runtime that honours the `terminate` frame instead exits,
and nothing reaps it, which is the residue the drain gate newly exposes. 0073's tier-7a case asserts the
connection property and runs against a runtime stub that ignores `terminate`, so it does not cover the
second residue.

**Source:** 0073 §9 Recorded limits, third entry.

### 1.2 A bind that fails after PrepareWorkspace leaves an entry nothing removes

A bind failing after `PrepareWorkspace` leaves a registry entry no adapter path removes. A failure before
`AssignCredentials` leaves a registered-but-unbound entry; a failure at credential assignment, or at the
`StartSession` RPC before `Runtime.Start`, leaves a **bound** entry, because `assignCredentialsSlot` writes
`st.sessionID` (`pkg/adapter/slotcreds.go:33-34`) and the bind sequence runs it before `StartSession`.

Both classes hold §4.6.1's inbound count above one for the life of the pod. The bound class additionally
pins the drain gate false for the pod's life, and a later session placed on that pod finds the registry
holding a foreign entry, so its claim arms neither the platform nor the connector MCP server and the pod's
intra-pod MCP surface is absent rather than refusing for the rest of the pod's life.

0073 states plainly that discharging any of this is an obligation it does not take.

**Source:** 0073 §9 Recorded limits, fourth entry. Verified: `pkg/adapter/slotcreds.go:33-35`.

### 1.3 The adapter manifest is pod-global and single-valued

The manifest keeps one fixed runtime-facing path in `ManifestDir` (`pkg/adapter/server.go:111-114`,
`WriteManifest` at `pkg/adapter/manifest.go:194`), and its `sessionId` and `mcpNonce` are single-valued
(`Manifest.SessionID` at `manifest.go:110`). A second concurrent `StartSession` on the same pod rewrites
both and invalidates the nonce a co-tenant runtime authenticated with.

0073 merged the manifest write into the one `StartSession` path, so every session now receives the manifest
a base-mode session received before. That merge is what makes the single-valued file reachable on a
concurrent pod: before it, the concurrent path wrote no manifest at all.

**Source:** 0073 §9 Recorded limits. Verified: the cited symbols exist as described.

### 1.4 The intra-pod MCP surface is pod-global with the manifest

One platform socket serves the pod. The surface was not partitioned when the manifest was not, and the two
are recorded together.

**Source:** 0073 §9 Recorded limits, the entry following the manifest limit.

### 1.5 Full-level rotation is session-separable only in part

Per CODE-4, the per-slot credential file is rewritten for the addressed session, and the rest of the §4.7
Full-level rotation protocol is not separated per session.

**Source:** 0073 §9 Recorded limits.

### 1.6 The adapter cannot enforce the pod-idleness refusal

§4.11's pod-idleness refusal was enforced by the pod-global claim. Nothing enforces it now.

**Source:** 0073 §9 Recorded limits.

### 1.7 The leaked slot state is scoped to concurrent pods in the specification and not in the tree

SPEC-5 leaves the `leaked` slot state and its `claimed → draining` consequence scoped to
`maxConcurrentSessions > 1` in §6.2 and §5.2, while the shipped drain path already applies the same
consequence to an exclusive pod: `drainLedger.RecordLeak` stamps `lenny.dev/drain-request` on a pod's first
leaked session scrub, because `slothealth.UnhealthyThreshold` clamps a denominator below 1 to 1.

0073 names the two ways to close it, restating the threshold per pod class or changing the ledger so the
clamp stops applying, and stages neither.

**Source:** 0073 §9 Recorded limits.

### 1.8 The operation lock has an unbounded overtake window

`release` picks the lowest key present at release time (`lowestKey` at `pkg/adapter/oplock.go:206`, called
from `release` at `:194`) and `Begin` (`:89`) re-enqueues a slot as soon as its previous operation is
promoted, so a slot with a high identifier can be overtaken without bound by a faster-cycling sibling with
a lower one. Per-slot coalescing (`errOpCoalesced`, `:40`) bounds the pending set's size rather than the
overtake count. The same window applies to the §5.2 upload-serialization rule and the §4.4 queue-depth
rule.

0073's D12 corrects how those rules are described and does not change what they do. 0073 §3 records the
window as a separate finding rather than fixing it, and no finding has been filed.

**Source:** 0073 §3 Out of scope. Verified: `pkg/adapter/oplock.go:89, 194, 206`.

### 1.9 The runtime SDKs model no status frame

The Python and TypeScript runtime SDKs model no `status` frame, so neither carries the `sessionId` the
frame would stamp.

**Source:** 0073 §9 Recorded limits, penultimate entries.

### 1.10 T-4.4.21 has no closing test case

§7 and D15 of 0073 close `TEST-GAPS.md`'s T-4.4.21 against a tier-4 case §8 stages: a concurrent pool
captures a slot's checkpoint, loses the pod, and resumes onto a replacement with the workspace intact. That
case is not in the tree. `tests/tier4_integration/checkpoint_concurrent_pool_test.go` carries capture
independence rather than the capture, pod-loss, and resume round trip.

The implementation left T-4.4.21 OPEN rather than closing it against coverage that does not exist, and
recorded the deviation on 0073's S21. Either the case is written or §7 and D15 are corrected to close
T-4.4.21 only in part.

**Source:** proposal 0073's S21 deviation record; `TEST-GAPS.md:692`.

### 1.11 Two suites are skipped on preconditions this work did not meet

`TestTaskModeRecycleScrubsWorkspaceBetweenSessions` (`tests/tier5_e2e_kind/execution_modes_test.go:123`) is
skipped because §5.2 recycle pod reuse does not hold on the §4.7 sidecar transport. Proposals 0078 and 0079
take that property, so this entry is a reminder to un-skip rather than a gap of its own.

`TestConcurrentWorkspaceSlots` (`tests/tier7b_load_kind/scaffolds_test.go:402`) is skipped because per-slot
workspace multiplexing is pod-internal and the gateway exposes no per-slot endpoint to load-test against.
Nothing covers that.

**Source:** the two `t.Skip` call sites and `tests/registers/skip-reasons.yaml`.

### 1.12 Twenty claim-register rows are ABSENT

Twenty of the register's seventy-six rows carry status `ABSENT`, meaning specified and not implemented:
adapter metric names carrying the retired channel spelling; agent podspec mTLS certificate material;
agent-pod eviction checkpoint; agent-pod mTLS client identity; cross-replica message forward;
gateway-to-gateway transport; in-flight RPC cancellation on a generation gap; input-wait resolution across
replicas; kubelet probes on agent-pod containers; the LLM proxy pod client; lease or mirror release on
gateway drain; mirror seed at bind; a PodDisruptionBudget for a busy pod; quiesce enforcement against
operational RPCs; the runtime-operations events schema asserted by the external-adapter compliance suite;
session inbox redelivery; single-consumer enforcement on `Attach` at the pod; the `AdapterEvicting`
consumer; the `grpc.health.v1.Health` client on the pod; and the `ready_for_input` signal.

Two carry a named owner already. Single-consumer enforcement on `Attach` is what proposal 0071 builds. The
adapter metric names row is naming-law N4's metric half, deferred with `deferral_id: R12` to the step that
adds the adapter metrics endpoint and its catalog entries, and its surface is recorded as
`pkg/adapter/metrics.go:71, :79`. The other eighteen have no owner.

**Source:** `tests/claim-map.json`, rows with `"status": "ABSENT"`.

### 1.13 Four scenario sections are exempted pending implementation

`tests/spec-map-exceptions.yaml` carries `pending-implementation` entries, each opened 2026-08-15, for
§29.4 (interrupt, terminate, and delete; blocker R10), §29.5 (checkpoint capture; blocker R12), §29.6
(restore and resume), and §29.10 (the structural analysis of the concurrent-session pod; blocker R7).

**Source:** `tests/spec-map-exceptions.yaml`.

### 1.14 Whether the adapter's hold state is partitioned per slot, and what remains of it

`spec/29` records a list of questions the specification leaves unstated
(`spec/29_communication-scenarios.md:1523`, `:1528`, `:1536`, `:1540`). This entry is the first of them.
0073 fixed the separate live defect that the hold never armed at all on a pod whose sessions all take the
slot path, and left the partitioning question open, because partitioning would contradict §10's and §28's
statements that the hold is a property of the adapter process.

**Proposal 0076 answers it and removes the bullet.** Its SPEC-1 states both answers in §10.1.2 and
§10.1.4, and its SPEC-2 removes the partitioning bullet from the list, so this entry loses its subject when
0076 applies. Its D5 keeps the hold pod-scoped and makes a fence from any bound session the correct exit,
which is also its answer to the adjacent defect of a hold entered for one session and released by another's
fence. The remaining list entries stand: the `Interrupt` and drain-barrier addressing question is narrowed
by 0076 rather than removed, and the `CH-ADAPTEREVENTS` arbitration question is the specification half of
§1.18 above.

**Source:** 0073 §3 Out of scope; `spec/29_communication-scenarios.md`; proposal 0076's SPEC-1, SPEC-2, and
D5.

### 1.15 The embedded reference runtimes drift against the flags the reconciler injects

`cmd/runtimes/echo-embedded` and `cmd/runtimes/preconnect-echo` parse with the default `flag.ExitOnError`
set and declare the injected flags from a hand-kept list in two test files, with nothing tying that list to
`pkg/controller/sandbox/podspec`. Every flag the renderer gains on the embedded runtime container breaks
both fixtures until someone reads a pod log. This has now happened twice.

**Source:** `BUILD-GAPS.md` finding F-4.7.23, filed 2026-08-26.

### 1.16 The pod refuses the fence retry §10.1.2 orders

When a fence fails or times out, §10.1.2 step 2 orders the new coordinator to retry "with the same
generation value" (`spec/10_gateway-internals.md:39`). The shipped pod refuses that retry.
`pkg/adapter/coordination.go:99` rejects `gen <= lastFenced` with `FailedPrecondition`, and the fence
driver reads every `FailedPrecondition` as generation-stale, re-reads the row, finds no advance, and
relinquishes the lease (`pkg/gateway/coordination/coordfence/coordfence.go:164-179`). The collision fires
inside a single `Fence` call: attempt one lands, its acknowledgement is lost, the driver's transient arm
retries at the same value, and attempt two is refused as stale. A handoff that succeeded costs its lease,
a sweep cycle, and one increment each of `lenny_coordinator_handoff_stale_total` and
`lenny_coordinator_fence_relinquished_total`, the second of which is a split-brain counter.

Fixing the gateway alone does not reach the case. The adapter returns its `CoordinatorFenceResponse`
alongside a non-OK status (`pkg/adapter/coordination.go:102-106`) and the client drops the body
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56`), so the driver cannot tell a re-fence at
the recorded value from a genuinely stale one without parsing the detail string or changing what the
adapter returns.

**What needs to change.** The remedy is one comparison and the carriers that state it.

- The pod accepts the equal case. `pkg/adapter/coordination.go:99` compares `gen < lastFenced` rather than
  `gen <= lastFenced`, so a re-fence at the recorded generation is acknowledged rather than refused, and
  the fence becomes idempotent at its own value as step 2's ordered retry requires.
- The wire comment that states the acceptance predicate is restated. `CoordinatorFenceResponse`'s comment
  says `accepted` is false when the supplied generation "is not greater than the last fenced generation"
  (`schemas/lenny-adapter.proto:1455-1458`); it becomes false when the generation is strictly older.
- The specification states that the pod honours the ordered retry. Step 2 orders the retry without saying
  the pod must accept it, and the gap clause beside it governs only the higher case, so the equal case is
  unstated on both sides of the contract. The §28.5.1, §28.6 `CH-FENCE`, §28.8, and §29.8 arms proposal
  0076 stages enumerate the older and the higher case and are silent on the equal one, so each would gain
  it.
- A test case covers the lost acknowledgement: a fence lands, its acknowledgement is dropped, the driver
  retries at the same value, and the pod accepts without incrementing either counter.

Two things a remedy must take on rather than discover late. It reverses the strictly-monotonic handler
that `BUILD-GAPS.md` records as finding F-4.7.2's resolution, so that record is corrected in the same
change. And it accepts a permanent residual: for a session row an old binary minted at 0 during a rolling
window, the resume path fences at the retained `coordfence` floor of 1
(`pkg/gateway/coordination/coordfence/coordfence.go:147-153`) while a takeover's `RecordHandoff` bumps 0 to
1 and fences at 1 as well, so two replicas carry the same generation for that row and accepting equality
admits a genuine split-brain there.

This entry sequences after proposal 0076, which rewrites the predicate at `:99` whichever way this is
answered: its CODE-1 moves `lastFenced` and `initialized` off the pod-wide `Server.coord` onto the slot
entry. Landing the comparison change before that rewrite would put it on a line 0076 replaces.

The conflation this leaves standing is separate and larger. The driver reads three distinct adapter
refusals through one status code, and §16.1's row for `lenny_coordinator_handoff_stale_total` states that
it increments on a generation-stale rejection (`spec/16_observability.md:183`). Accepting the equal case
removes one producer of false increments. Separating the remaining two means naming a new counter or label
and changing what the adapter returns on a refusal, which is a wider surface than this entry opens.

**Source:** proposal 0076's OD2, which derived the remedy, recommended that a successor own it, and left
the successor unnamed; proposal 0076's record of the fence driver's conflation of three failure classes.

### 1.17 The barrier-target mirror lags one generation across a takeover

On the sweep iteration that performs a coordinator handoff, the sweeper mints the post-handoff generation
through `RecordHandoff` and fences the pod at it
(`pkg/gateway/coordination/coordination/coordination.go:371`), then passes the pre-bump value from its own
List snapshot to `upsertMirror` (`:430`), so the `coordination_lease` row carries the prior generation
while the pod holds the new one. The barrier assembly reads that row
(`pkg/gateway/coordination/barrier/wiring.go:104-114`) and the dispatcher puts its value on the wire, so
every drain barrier assembled from the mirror in that interval is refused under any comparison operator.

The window is bounded and the outcome inside it fails safe. `upsertMirror` runs for every lease the replica
holds on every sweep, from a fresh List row, so the next sweep writes the post-handoff value; the sweep
cadence defaults to 15s (`pkg/gateway/coordination/coordination/coordination.go:182-185`). Inside the
window the pod refuses with `FailedPrecondition`, the dispatcher marks the target stale and leaves `Acked`
false (`pkg/gateway/coordination/barrier/barrier.go:230-237`), and preStop's post-barrier per-session loop
therefore captures that session instead of skipping it
(`pkg/gateway/podlifecycle/prestop/prestop.go:395`). The cost is an unquiesced capture rather than a lost
one.

The lag is load-bearing for text outside itself, which is the reason this entry exists rather than a
`BUILD-GAPS.md` line. §10.1.8 step 1 describes the value the barrier carries as current
(`spec/10_gateway-internals.md:183`), and §29.7 step 4 does the same
(`spec/29_communication-scenarios.md:1186`). Both sentences are false on the healthy path today, and
proposal 0076's ruling that neither is an edit site for it rests on their already being false through this
lag. Repairing the lag turns both into edit sites, one of them in a file 0076's SPEC-1 owns, so a repair
that lands without that spec work leaves two sentences newly true by accident and unverified.

The repair itself is small and local: pass the generation `RecordHandoff` returned rather than the List
snapshot's, on the branch that performed the handoff. Establishing that no other `upsertMirror` caller
carries the same staleness is the work.

**Source:** proposal 0076's record of the defects it does not stage, which states that no other proposal
and no `BUILD-GAPS.md` finding owns the repair. Verified against the tree on 2026-09-06.

### 1.18 Twenty-four claim-register rows are UNWIRED, and this inventory did not cover them

§1.12 counts the rows the register marks `ABSENT`. The register carries a second unmet status. Of its
seventy-six rows, thirty-two are `WIRED`, twenty are `ABSENT`, and twenty-four are `UNWIRED`, meaning
implemented on one side and unreachable because the other side does not exist. An `UNWIRED` row is not a
weaker `ABSENT`: the code is written, the tests may pin it, and nothing in a deployed system reaches it,
which is the failure mode least likely to surface in review.

Thirteen of the twenty-four are the `coordination_generation` fence field on individual request messages,
all under deferral `R16`: `AttachRequest`, `CheckpointBarrierRequest`, `CheckpointRequest`,
`ExportPathsRequest`, `ExtendCredentialLeaseRequest`, `InterruptRequest`, `ReportUsageRequest`,
`ResumeRequest`, `RevokeCredentialsRequest`, `RotateCredentialsRequest`, `SendMessageRequest`,
`ShutdownRequest`, and `SignalDeadlineRequest`. `R16` also carries the best-effort eviction snapshot and
the `AdapterEvicting` producer. The rest are the credential revocation addressed to the session's own lease
file and the proxy SPIFFE lease binding (`R14`); the Redis hot routing cache, `coordinator_address`
population, and the `coordlease.GetBySession` routing read (`R13`); the runtime channel at deployment, the
service-account token credential on `GatewayControl`, and the `Adapter/AdapterEvents` client (`R12`); and
the SSE live tail across replicas (`R19`).

One of them has a consequence worth stating on its own, because it disarms a mechanism rather than leaving
a surface unbuilt. The `Adapter/AdapterEvents` client is the gateway half of the stream whose closure arms
the pod's coordinator-loss hold. Outside the generated code the only reference under `pkg/gateway` or
`cmd/` is a comment (`pkg/gateway/runtime/adapterclient/client.go:464`), and the one production
construction of an adapter client (`:38`) exposes no method that opens it. The hold's single arming path is
the `defer` in `Server.AdapterEvents` calling `onCoordinatorChannelClosed`
(`pkg/adapter/adapterevents.go:100-108`), which calls `enterHoldState`
(`pkg/adapter/holdstate.go:90-100`), and `enterHoldState` has no other caller outside the tests. The hold
therefore never arms in a deployed system. The code is exercised: tier 2 opens the real stream through the
generated client and drops it
(`tests/tier2_component/slotrelease/revoke_double_teardown_test.go:309-336`) and tier 9 does the same
(`tests/tier9_security/adapter_hold_termination_surface_test.go:87-90`), so the suite is green and the
deployed path is absent.

Building that client is a channel implementation rather than a wiring step. It needs the dial, the
reconnect policy, and the arbitration of which replica's connection carries the pod's events, which §28.8's
`CH-ADAPTEREVENTS` row records the specification as not stating
(`spec/28_communication-channels.md:1810`). The specification gap is the first thing to close.

A deferral identifier is a marker rather than an owner. `R12`, `R13`, `R14`, `R16`, and `R19` name when a
row was set aside; none of them names a proposal, and no proposal outside this inventory takes any of the
twenty-four.

**Source:** `tests/claim-map.json`, rows with `"status": "UNWIRED"`; proposal 0076's record of the
`CH-ADAPTEREVENTS` client among the defects it does not stage.

### 1.19 One status code carries three fence-refusal classes, and §16.1 describes only one

`Fencer.fence`'s rejection arm reads every `FailedPrecondition` from a fence as generation-stale
(`pkg/gateway/coordination/coordfence/coordfence.go:164-179`), and the adapter returns that code for three
distinct refusals: a genuine stale fence, where the requested generation is below the recorded value
(`pkg/adapter/coordination.go:99`, refused at `:105-106`); a fence for a session the pod's slot registry
does not hold bound, which `checkSessionBound` refuses before the generation is read
(`pkg/adapter/slotsession.go:271-273`); and a re-fence at the already-recorded generation after a lost
acknowledgement, which reaches the same predicate at `:99`. The status code is all the driver has, because
the adapter returns its response body alongside a non-OK status and the client drops the body
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56`).

§16.1's row for `lenny_coordinator_handoff_stale_total` states that it increments on a generation-stale
rejection (`spec/16_observability.md:183`). The tree contradicts that row for two of the three classes, so
the counter an operator reads during a handoff incident overcounts by an unknown amount and the
specification gives no way to tell which class fired.

Two of the three classes are accounted for elsewhere. Proposal 0076 removes one producer of false
increments by recording the generation per bound session, which ends the class where a co-tenant session's
legitimate lower value read as stale. §1.16 above takes the re-fence at the recorded value. What is left is
the bound-session refusal sharing a code with the genuine stale fence, and the false §16.1 row that covers
both.

**What needs to change.** The refusal classes must be distinguishable on the wire before the metric can be
correct, and neither half is reachable from the gateway alone.

- The adapter distinguishes the classes in something the client keeps. Today the body is dropped with the
  non-OK status, so the options are a distinct status code per class, a typed error detail, or returning
  the body on the refusal path. Picking among them is the design question this entry opens.
- The driver counts each class separately. That means naming a new counter or a label on the existing one,
  and adding its §16.1 row, which is why this is a wider surface than §1.16: it opens an observability
  contract rather than changing a comparison.
- §16.1's row is restated to what the counter then counts.

This sequences after proposal 0076 and after §1.16, both of which change the predicate and the classes
before the accounting is settled. Doing it first would meter a set of classes about to change.

**Source:** proposal 0076's record of the defects it does not stage, which records the conflation and
states that no deliverable of its own touches the stale arm.

### 1.20 Nothing owns the retirement of the fence path's non-positive generation floor

`pkg/gateway/coordination/coordfence/coordfence.go:147-153` floors a non-positive `coordination_generation`
read at 1 before fencing, because a fresh row that has not been stamped would otherwise be refused by an
adapter that requires a positive value. Proposal 0076's CODE-4 keeps the floor and states that the release
which tightens the session row's `CHECK (coordination_generation >= 0)`
(`migrations/0050_session_record_fields.up.sql:38-39`) to `>= 1` retires it.

That sentence, inside a landed proposal's staged text, is the only thing in the repository that owns the
retirement. `BUILD-GAPS.md`, `TEST-GAPS.md`, and `PROPOSAL-QUEUE.md` carry no finding or queued proposal
for it, and no file outside 0076 mentions the tightening. The floor is correct and costs nothing while it
stands; what is missing is any record a later reader would find.

Proposal 0076's OD9 put the tightening to a reviewer and offered no recommendation, on the ground that the
specification does not compel the constraint: §10.5's Phase 3 rule is a permission and an ordering
(`spec/10_gateway-internals.md:432`), and `spec/` carries no DDL constraint text for the column. The
reviewer accepted the floor for that release, which is what puts the retirement here rather than in a
commissioned successor.

**What needs to change.** Either the tightening lands as a Phase 3 migration in a later release and the
floor goes with it, or the floor is affirmed as permanent and CODE-4's sentence is corrected so it stops
promising a retirement nothing will perform. A tightening carries three obligations §10.5 states: the
migration opens with a preflight verification block whose count query captures the rows still below 1
(`spec/10_gateway-internals.md:434`), it waits the minimum inter-phase interval, `maxSessionAge` for the
session table (`:433`), and it cannot be the whole discharge. The fence reads through
`coordfence.GenerationReader`, wired to the configured session store
(`cmd/lenny-gateway/main.go:373-380`), which is Postgres only when a pool exists and otherwise the
in-memory store (`cmd/lenny-gateway/stores.go:1015`, `:1035`), whose §17.4 restore path replaces its map
wholesale from JSON without passing through `Create`
(`pkg/gateway/session/sessionstore/memstore/snapshot.go:27-37`). A Postgres check constrains one store
behind that interface, so retiring the floor means establishing that no configured store can deliver a
non-positive value.

**Source:** proposal 0076's OD9, answered by accepting the floor for that release, which leaves the
retirement unowned.

### 1.21 The migrate Job has no run-time budget

The chart bounds the preflight, crd-validate, and backup Jobs and does not bound the migrate Job, and no
run-time budget for it exists in `spec/` or the chart. `docs/operator-guide/upgrades.md:22` shows
`--wait --timeout 10m` as an example command rather than a stated contract, and
`docs/runbooks/schema-migration-failure.md` states no expected or maximum run time. An operator upgrading a
large installation has nothing to size a maintenance window against and no signal that distinguishes a slow
migration from a stuck one.

The runner applies a migration file as one statement batch, so an unbatched backfill inside it is a single
long-running statement rather than a resumable series. Migration 0180 already backfills the whole `sessions`
table unbatched in the same Job, and proposal 0076's migration 0181 does the same, so the exposure predates
either and grows with the largest table the Job touches.

**What needs to change.** The budget is an operational contract on the migrate Job that outlives any one
migration, so it belongs in the chart and the runbook rather than in a migration file: a bound on the Job,
a stated expected and maximum run time in `docs/runbooks/schema-migration-failure.md`, and an upgrade-guide
timeout that follows from the bound rather than illustrating one. Whether a backfill that could exceed the
bound is then batched is a consequence of the number, and is not answerable before it exists.

**Source:** proposal 0076's OD14, which put both halves to a reviewer, offered no recommendation on either,
and recorded that neither `spec/` nor the chart takes a position the repository can read off.

## 2. Already owned, and therefore out of scope here

- **F-5.2.33 parts (a) and (b)**, the sidecar-transport runtime lifetime, are taken by proposals 0078 and
  0079.
- **D6's declared message-scope table** and the §4.1 `ShutdownRequest` classification limit that shares its
  cause are taken by proposal 0075, which retires the table and its gate.
- **A hold entered for one session released by another's fence** was recorded as taken by proposal 0076.
  0076's D5 keeps the hold pod-scoped and makes a fence from any bound session the correct exit, so it
  declines the item rather than taking it. The item is not restated as a gap above because D5 answers it.
- **The false tier-3 coverage clause for `CoordinatorFenceRequest`** is taken by proposal 0075. 0076
  recorded it and left the repair conditional on its OD3; under the answer given, the clause's basis is
  the §4.1 table 0075 retires, so 0075's TEST-2 carries it.
- **Single-consumer enforcement on `Attach`** is taken by proposal 0071.

## 3. Deliberate exclusions that are not gaps

These are recorded so a later reader does not mistake them for oversights. 0073 excluded each by design and
gave its reason: the isolation-gate conditions keyed on `maxConcurrentSessions > 1`; the sizing, admission,
capacity, draining, and recycling arithmetic; the service-mode sense of "slot", which is named and
distinguished but deliberately not unified; and renaming the `pkg/adapter/slotlayout` package surface.

0073 also states that it claims no runtime-compatible path: a runtime built against the previous contract
must be updated. That is a stated non-goal rather than an outstanding item.

## 4. Non-goals

This document stages no spec text and no code change, and it writes no text any file could take. §1.16
states a direction because the proposal that derived it stated one; the direction is not staged edits and
has been through no adversarial review here. It does not rank the entries, and it does not assert that every entry warrants a fix. Triage is the next step, and each
entry that survives it should become its own proposal with its own adversarial review.

## 5. Files touched on application

None. This document stages no changes.
