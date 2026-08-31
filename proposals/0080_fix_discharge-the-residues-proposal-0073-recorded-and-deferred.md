# Proposal: Discharge the residues proposal 0073 recorded and deferred

- **Status:** EARLY DRAFT, NOT CONVERGED. This document is an inventory rather than a design. It states
  each gap and the evidence it was found from, and deliberately proposes no remedy for any of them. It is
  expected to be split into per-concern proposals once the entries have been triaged, and no entry should
  be implemented from this text.
- **Date:** 2026-08-31
- **Scope:** Collects the deferrals proposal 0073 recorded and left open, together with the register and
  audit entries that record the same gaps elsewhere, and which no current draft covers. Proposals 0075,
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

This document proposes no remedy, no spec text, and no code change for any entry above. It does not rank
the entries, and it does not assert that every entry warrants a fix. Triage is the next step, and each
entry that survives it should become its own proposal with its own adversarial review.

## 5. Files touched on application

None. This document stages no changes.
