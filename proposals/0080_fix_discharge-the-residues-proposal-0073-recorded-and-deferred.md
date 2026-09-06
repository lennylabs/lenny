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

### 1.14 Whether the adapter's hold state is partitioned per slot is still unstated

This is the first of the five gaps `spec/29` records as unstated. 0073 fixed the separate live defect that
the hold never armed at all on a pod whose sessions all take the slot path, and left the partitioning
question open, because partitioning would contradict §10's and §28's statements that the hold is a property
of the adapter process.

Proposal 0076 takes the adjacent defect, that a hold entered for one session is released by another's
fence, and does not answer the partitioning question.

**Source:** 0073 §3 Out of scope; `spec/29_communication-scenarios.md`.

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

## 2. Already owned, and therefore out of scope here

- **F-5.2.33 parts (a) and (b)**, the sidecar-transport runtime lifetime, are taken by proposals 0078 and
  0079.
- **D6's declared message-scope table** and the §4.1 `ShutdownRequest` classification limit that shares its
  cause are taken by proposal 0075, which retires the table and its gate.
- **A hold entered for one session released by another's fence** is taken by proposal 0076.
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
