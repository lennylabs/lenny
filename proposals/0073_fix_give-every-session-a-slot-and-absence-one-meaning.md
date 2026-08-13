# Proposal: Give every session a slot and absence one meaning

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Retires the rule that a slot identifier is carried only on a pod whose pool sets
  `sessionPolicy.maxConcurrentSessions > 1`. Every session-scoped message on the gateway-to-adapter
  control plane carries a slot identifier, whatever the pool's concurrency; every pod-scoped message
  carries no such field at all. Corrects the defects the rule has produced, the register rows that
  mis-record them, and the divergence between what the specification states and what the code branches on.

This document stages the proposed specification, code, schema, and register changes. It does not modify
any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Three facts overturn the way this question is usually posed, and the design in §4 rests on them.

The conditional rule is **not implemented**. The specification states a rule keyed on the pool's
concurrency. The code implements a rule keyed on field presence, and the two are not the same rule. No
production code path tests `maxConcurrentSessions` to decide whether to emit or honor a slot identifier.

Absence is **already load-bearing** as a pod scope, on concurrent pods as well as single-session ones. It
is not merely the encoding of "this pool has one session per pod".

The persistence layer has **already gone uniform** and pays a fiction to do it. This proposal removes the
fiction rather than introducing the uniformity.

## 1. Problem

### 1.1 The specification states one rule and the code implements another

`spec/15_external-api-surface.md:1498` states that messages on a pod whose pool sets
`sessionPolicy.maxConcurrentSessions: 1` "never carry `slotId`, and runtimes on those pods never see it".
`spec/15_external-api-surface.md:1616`, `spec/28_communication-channels.md:549`, `:604`, `:1624`,
`spec/05_runtime-registry-and-pool-model.md:513`, and `spec/06_warm-pod-model.md:373` state the same
condition, and `schemas/lenny-adapter.proto:584-586` repeats it in the `SlotId` doc comment.

The code decides differently. `useSlot` (`pkg/adapter/slot.go:43-45`) is `return slotID != ""`, and every
gateway producer site is an `if slotID != ""` (`pkg/gateway/runtime/adapterclient/client.go:147, 205, 289,
364, 399, 428, 845, 861, 900`). The single concurrency-conditioned test in the tree runs the opposite
direction: `pkg/gateway/session/executor/pod.go:146-148` rejects an *empty* slot on a concurrent bind, and
its own doc states a `<= 1` bind never triggers it.

The two rules agree on today's traffic only because the gateway happens to populate the field exactly when
the pool is concurrent. Nothing enforces the correspondence, and §1.2 is what happens where it lapses.

### 1.2 Absence conflates "pod scope" with "nobody plumbed it"

`useSlot` resolves an empty slot identifier to the pod-global layout. That is correct for a genuinely
pod-scoped operation and wrong for a session-scoped one whose caller omitted the field, and the two are
indistinguishable at the branch. Every un-plumbed session-scoped call site therefore degrades into a
working-looking pod-global path rather than failing.

Four defects have been produced this way, all silent:

**Checkpoint restore.** Capture is slot-scoped: `pkg/adapter/checkpoint.go:100` resolves
`checkpointRootsForSlot(start.GetSlotId().GetValue())`, which swaps in `/workspace/slots/{slotId}/current`
(`pkg/adapter/slot.go:147-173`). Restore is not: `pkg/adapter/resume.go:179` calls
`ExtractTree(s.checkpointRoots(), pr)`, and `checkpointRoots()` (`pkg/adapter/checkpoint.go:28-38`) always
returns the pod-global root. `ResumeRequest` (`schemas/lenny-adapter.proto:1219`) has no `slot_id` field to
carry, so the omission is structural. A slot's checkpoint extracts into a directory no slot's runtime
reads, no error is returned, and `resumeMode` reports a normal full restore.

**Delegation file export.** `pkg/adapter/exportpaths.go:39,48` resolves every export against the pod-global
`s.WorkspaceRoot`, and `ExportPathsRequest` (`:1397`) carries no `slot_id`. On a concurrent pod the export
packages files from a tree no slot owns. This is the §8.7 delegation payload path.

**Coordinator hold state.** `pkg/adapter/holdstate.go:91-96` arms the hold from `s.currentSession()`, which
reads the pod-global `s.sessionID` that only `claimSession` sets (`pkg/adapter/session.go:361-369`).
`StartSession` returns early into `startSessionSlot` (`pkg/adapter/session.go:103-106`) and never calls it,
so `s.sessionID` is empty for the life of a concurrent pod and the hold never arms. §10.1 coordinator-loss
protection is absent on exactly the pods that multiplex the most sessions.

**The §7.3 step (d) guard.** `pkg/adapter/resume.go:61` compares `ExpectedWorkspaceRoot` against the
pod-global `s.WorkspaceRoot`, so the "same absolute cwd" assertion passes vacuously on a concurrent pod,
and the restore then proceeds into the wrong tree.

### 1.3 Credential rotation defeats the isolation it is specified to provide

`spec/06_warm-pod-model.md:28` specifies per-slot credential leases at
`/run/lenny/slots/{slotId}/credentials.json` when a pool is concurrent, "rather than a single global
`/run/lenny/credentials.json`". `RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`, and
`RevokeCredentialsRequest` each carry `slot_id`, and the adapter honors it
(`pkg/adapter/credentials.go:116, 158, 331`).

No gateway caller sets it. `RotateCredentials` (`pkg/gateway/runtime/adapterclient/client.go:234-241`) and
`ExtendCredentialLease` (`:253-262`) build their requests with no `SlotId`, and the adapter's
`RevokeCredentials` has no gateway caller at all: the only `RevokeCredentials` calls under `pkg/gateway`
are the Token Service's (`pkg/gateway/credassign/client.go:306`). A rotation on a concurrent pod therefore
rewrites the pod-global credential file, which every slot on the pod can read, defeating the per-slot
credential isolation `spec/06:28` states and `spec/13_security-model.md:30` relies on when it tells
deployers requiring strict lease isolation to set `maxConcurrentSessions: 1`.

These three fields match §28.4's definition of `UNWIRED` precisely. `tests/claim-map.json` does not carry
a row for any of them.

### 1.4 The persistence layer invented a slot identifier the wire refuses

`spec/10_gateway-internals.md:157` states that `slot_id` is "set to the sentinel `'default'` for pools with
`maxConcurrentSessions: 1` so the `(session_id, slot_id)` scoping key is well-defined for every session".
The sentinel is implemented at `pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:67-70`
and applied at `:367-369`, at `pgstore/pgstore.go:157-159`, at `pkg/gateway/sessionserver/derive.go:481`,
and at `pkg/gateway/checkpoint/checkpointer/checkpointer.go:500-503`. The partial unique index, the
supersede-on-write rule, the reassembly predicate (`spec/10:153, 163, 171`), and the §10.1.8 intent rows
are all keyed `(session_id, slot_id)` on the strength of it.

The same function then refuses to put the value it just derived onto the wire.
`pkg/gateway/checkpoint/checkpointer/checkpointer.go:536-545` is a ten-line comment explaining why: "the
adapter branches on non-empty slot_id presence and would route the `"default"` sentinel down the per-slot
branch that has no assigned slot state". `slotIDField` (`:595-600`) returns `nil` for an empty identifier.
Proposal 0050 recorded the same rejection at `proposals/0050_fix_slot-aware-checkpointing...md:37`.

So the design has already concluded that a universal slot key is necessary, adopted a fabricated one to
obtain it, and been unable to use it on the wire because a fabricated identifier is indistinguishable from
a real one. The storage layer and the wire are decoupled, and the coupling point is the presence branch in
§1.1.

The sentinel is also not applied consistently: `session_checkpoints.slot_id` defaults to `''`
(`migrations/0112_*.up.sql`), `checkpoint_manifest.slot_id` defaults to `'default'`
(`migrations/0178_*.up.sql:38`), and `session_partial_checkpoint_manifest`
(`migrations/0150_*.up.sql`) has no such column.

### 1.5 Five register rows record a field that does not exist

`tests/claim-map.json` carries five rows as `UNWIRED`, citing `schemas/lenny-adapter.proto` as the surface:
`ResumeRequest.slot_id` (`:240-245`), and the slot identifier fields on `CheckpointBarrierRequest`
(`:59-64`), `InterruptRequest` (`:122-127`), `ReportUsageRequest` (`:219-224`), and `SignalDeadlineRequest`
(`:298-303`). §28.4 defines `UNWIRED` as implemented with no production caller.

None of the five fields exists. All five should be `ABSENT`, and all five contradict sibling rows in the
same file: `:52-56` records "no `slot_id` on `ResumeRequest`" as `ABSENT`, and `:317-322` records
"Slot-qualified interrupt, deadline, usage, and barrier" as `ABSENT`.

The rows were seeded by this branch. `scripts/seed-claim-register.py` inferred each status from the
deferral step's scope note, which says the proto fields land in an earlier step, rather than reading the
tree. Proposal 0072 §1.11 stages a correction for one of the five and does not name the other four.

### 1.6 Three surfaces state a contract the platform does not honor

All three client SDKs expose and serialize a `slotId` on the message payload
(`sdks/client/go/lenny/types.go:190`, `sdks/client/python/lenny/types.py:266, 279-280`,
`sdks/client/typescript/src/types.ts:168`). The gateway deliberately drops it:
`pkg/gateway/sessionserver/messages.go:187-193` states that "A client-supplied slotId is silently ignored
because the field does not deserialize onto the payload".

`spec/15_external-api-surface.md:1592` and `spec/28_communication-channels.md:600` give `"slot_01"` as the
example slot identifier, an ordinal. The implementation uses the session identifier
(`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`, `SlotID: req.SessionID`).

`SendMessageRequest` (`schemas/lenny-adapter.proto:945`) and `AttachRequest` (`:958`) carry `slot_id` with
no doc comment, the only two of the thirteen carrying fields with no stated rule. The JSONL schema states
the condition on the `message` frame (`schemas/lenny-adapter-jsonl.schema.json:58-61`) and states nothing
on `tool_call` (`:139`), `tool_result` (`:161`), or `response` (`:190`).

### 1.7 The specification never defines what a slot is

There is no glossary entry and no standalone definition. Every definitional sentence derives the concept
from concurrency: `spec/05_runtime-registry-and-pool-model.md:513` ("Setting
`sessionPolicy.maxConcurrentSessions` above 1 multiplexes simultaneous sessions onto a single pod, each in
its own slot"), `spec/29_communication-scenarios.md:1434`, and `spec/28_communication-channels.md:139`.

Service mode already uses the term without `sessionPolicy` at all
(`spec/05_runtime-registry-and-pool-model.md:519`), where a slot is the per-pod request-capacity unit and
the readiness probe reflects slot availability. The concept is therefore already general in one execution
mode and concurrency-derived in the other.

## 2. Decisions

1. **Absence is given exactly one meaning: pod scope.** A slot identifier is required on every
   session-scoped control-plane message and absent from the field set of every pod-scoped one. No message
   carries an optional slot identifier. This is what makes the ambiguity in §1.2 unconstructible rather
   than merely fixed: a session-scoped message with no slot cannot be built, and a pod-scoped message has
   no field to leave empty.

2. **The change is scoped to the gateway-to-adapter control plane. The adapter-to-runtime JSONL channel
   keeps its advisory rule.** Every defect in §1.2 and §1.3 is on the control plane. The cost of mandatory
   slot dispatch falls on the runtime channel, which is the platform's third-party extension point.
   `spec/15:1498`'s permission for a runtime on a single-slot pod to ignore `slotId` is preserved, restated
   as a property of holding one slot rather than of the pool's concurrency.

3. **The slot identifier is the session identifier, which is already true.** It is idempotent across a
   resume onto a replacement pod, which is what makes the §7.3 step (d) assertion in §1.2 meaningful
   instead of vacuous; it is globally unique, so a recycled pod accumulates no cross-session residue; and
   it is reconstructable from persisted columns (`pkg/gateway/sessionserver/start.go:2107`). `'default'` is
   rejected for the recycle-residue reason, and an ordinal is rejected because it needs a per-pod
   allocator and breaks reconstructability.

4. **Slot directories are created at assignment, which is what concurrent pods already do.** This resolves
   the ordering problem that a slot identifier equal to the session identifier cannot exist at warm time.
   A warm pod pre-creates `/workspace/slots/` and `/workspace/staging` and holds no slot tree, and the
   adapter creates the slot tree on the assignment edge exactly as it does today on a concurrent pod. No
   new mechanism is introduced, and workspace materialization already happens at assignment, so §6.3's
   latency accounting is unchanged. §5 records this as the decision most worth a second opinion.

5. **`/workspace/current` survives as a non-normative debugging symlink.** On a pod holding exactly one
   slot the adapter links `/workspace/current` to that slot's `current` tree. The contract is uniform and
   the runtime obligation is stated against the slot path; the symlink exists so an operator's
   `kubectl exec` and the documentation corpus keep working. Runtimes MUST NOT depend on it, and it is
   absent on a pod holding more than one slot.

6. **The `'default'` sentinel is removed rather than promoted.** With a real identifier on every path the
   fabrication in §1.4 has no purpose. The persisted `(session_id, slot_id)` key, the two indexes, and the
   reassembly predicate all survive unchanged and become correct without a caveat.

7. **The register correction moves here in full, and 0072 gives it up.** Proposal 0072 §1.11 and its REG-1
   stage a correction for one of the five rows in §1.5. Two proposals staging edits to the same file for
   the same defect is a conflict, so this proposal owns the whole correction and 0072 is amended to point
   here. §7 records the amendment.

8. **This proposal supersedes C-53's scope and closes T-4.4.21.** C-53
   (`PROPOSAL-QUEUE.md:570-576`) owns the slot-aware restore companion as a point fix. Its deliverable is
   `slot_id` on `ResumeRequest` plus a slot-scoped restore path, which SCHEMA-1 and CODE-2 stage here
   alongside the three sibling defects the point fix would have left standing. §7 records the queue update.

## 3. Out of scope, and why

The isolation gates keyed on `maxConcurrentSessions > 1` are untouched and stay keyed on it:
`acknowledgeProcessLevelIsolation` (`spec/05:431, 515`), tenant pinning (`:433, 440, 517`),
the `allowCrossTenantReuse` prohibition (`:447, 517`), the `CAP_NET_RAW` rejection (`:515`), the
`preConnect` admission rule (`:434`, `spec/06:71, 77`), and the `ConcurrentWorkspaceCredentialSharing`
condition (`spec/13:30`). None of them depends on whether a slot identifier is on the wire; they are
statements about isolation posture, and a pod serving one session in one slot has the posture it always
had.

The sizing, admission, capacity, draining, and recycling arithmetic is untouched. Every formula is keyed on
the number and already degenerates correctly at one (`spec/05:588`, `spec/10:136`).

`/workspace/shared/` stays conditional on `maxConcurrentSessions > 1` (`spec/06:397`, `spec/26:44`). It is
a cross-slot sharing affordance, and a pod with one slot has nothing to share.

The four gaps `spec/29_communication-scenarios.md:1541-1564` records as unstated are not closed here, with
one exception. §29.10 currently scopes them to concurrent pods, and §4.4 addresses the scoping rather than
the gaps. The exception is adapter hold-state partitioning, which §1.2 establishes is not an unstated gap
but a live defect, and which CODE-3 fixes.

## 4. Detailed design

### 4.1 Every control-plane message is classified

Each message on the gateway-to-adapter control plane is either session-scoped or pod-scoped, and the
classification determines whether it carries a slot identifier at all.

**Session-scoped, carries a required slot identifier.** The thirteen that carry the field today
(`ReportSessionScrubRequest`, `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`, `RunSetupRequest`,
`StartSessionRequest`, `SendMessageRequest`, `AttachRequest`, `AssignCredentialsRequest`,
`RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`, `RevokeCredentialsRequest`, `CheckpointStart`,
`ShutdownRequest`), plus the four that need it and lack it (`ResumeRequest`, `ExportPathsRequest`,
`InterruptRequest`, `ConfigureWorkspaceRequest`), plus the three the register already tracks as absent
(`SignalDeadlineRequest`, `ReportUsageRequest`, `CheckpointBarrierRequest`).

**Pod-scoped, carries no such field.** `ReportPodScrubRequest`, `CoordinatorFenceRequest`,
`NegotiateVersion`, and the pod-scrub and SDK-demotion paths. These are the operations that legitimately
address the whole pod, and removing the field from them is what stops absence from being ambiguous.

`ShutdownRequest` is the one message that is genuinely both, and it is the case
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651` pins: a recycle shutdown carries no slot
identifier because a whole-pod scrub is not a per-slot teardown, and that is true on a concurrent pod as
well as a single-session one. It is split into a session-scoped `ShutdownSlot` carrying a required
identifier and a pod-scoped `ShutdownPod` carrying none, so the distinction the test encodes is expressed
in the type rather than in the value.

### 4.2 The rule

A session-scoped request whose slot identifier is empty is rejected at the adapter boundary with
`InvalidArgument`, before any root is resolved. `slotlayout.ValidateSlotID`
(`pkg/adapter/slotlayout/slotlayout.go:110-129`) already rejects the empty string and already guards
against path traversal, so the check is a call rather than new logic.

`useSlot` (`pkg/adapter/slot.go:43-45`) is deleted. Every root-computing site
(`workspaceRootForSlot` at `slot.go:123-133`, `checkpointRootsForSlot` at `:147-173`, `slotStagingDir` at
`staging.go:123-133`, and the credential handlers at `credentials.go:74, 116, 158, 331`) resolves through
`slotlayout.Resolve` unconditionally. The pod-global fallback in `workspaceRootForSlot`, which today
absorbs both an empty identifier and an unknown one, keeps only the unknown-slot branch and returns
`FailedPrecondition` as `checkpointRootsForSlot` already does at `slot.go:160-163`.

The consequence worth stating plainly: a call site that forgets to populate the field stops producing a
plausible pod-global result and starts producing a loud error at the first call. That conversion is the
point of the change.

### 4.3 What a slot is

§5.2 gains the definition the specification lacks, stated independently of concurrency: a slot is the unit
of per-pod session capacity, identified by a slot identifier equal to the session identifier, owning a
workspace subtree, a credential lease, and a lifecycle. A pod holds between one and
`sessionPolicy.maxConcurrentSessions` slots. The service-mode usage at `spec/05:519` becomes an instance of
the definition rather than a separate sense of the word.

`sessionPolicy.maxConcurrentSessions` keeps its meaning exactly: the ceiling on simultaneous slots. What
changes is that one is no longer a special case of the ceiling but the smallest value of it.

### 4.4 §29.10 is rescoped rather than generalized

`spec/29:1429-1442` scopes the whole subsection to concurrent pods and states that "nothing in this
subsection applies" to a single-session pod. Under this proposal the addressing half of that scoping is
false, and the isolation half remains true.

The subsection is split. The addressing mechanisms (the `CH-MSGSOCK` addressing key, the per-slot inbox,
the workspace subtree, the credential lease, slot lifecycle, checkpoint admission, the operation lock)
become general and move to the sections that own them. What remains under a concurrency condition is the
co-tenancy analysis: shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack,
shared egress identity, pod health, and `REG-SLOTCOUNT` above one. Those are properties of two sessions
sharing a kernel, and they remain properties of concurrent pods alone.

This split is what keeps the change from promoting §29.10's three remaining unstated gaps into
platform-wide open questions: they are gaps about two slots interacting, and they stay in the co-tenancy
half.

## 5. The decision this proposal does not make

Decision 4 resolves the warm-pod ordering problem by creating slot trees at assignment. The reasoning is
that concurrent pods already do exactly this, so the alternative is not a simpler design but a second
one. The two alternatives, and why they are not staged:

A fixed slot identity known at warm time reintroduces the special case through the back door, since the
warm-time identity cannot be the session identifier and every pod would hold one slot whose identifier is
allocated differently from every other slot's.

Pre-allocating `maxConcurrentSessions` identifiers at pod creation restores the ordinal naming decision 3
rejects, and gives a warm pod N empty trees to scrub on recycle rather than none.

What decision 4 costs is that `spec/06:11-12`'s warm-pod checklist stops asserting that
`/workspace/current` exists and is empty on a warm pod, and asserts `/workspace/slots/` instead. If that
assertion is relied upon anywhere this proposal has not found, the decision needs revisiting before
SPEC-3 lands.

## 6. Proposed changes

### SCHEMA-1. Classify every control-plane message

`schemas/lenny-adapter.proto` adds `SlotId slot_id` to `ResumeRequest` (`:1219`), `ExportPathsRequest`
(`:1397`), `InterruptRequest` (`:1042`), `ConfigureWorkspaceRequest` (`:1503`), `SignalDeadlineRequest`
(`:1203`), `ReportUsageRequest` (`:1429`), and `CheckpointBarrierRequest` (`:1355`), each at the next free
field number. It removes the field from any message §4.1 classifies pod-scoped, and splits
`ShutdownRequest` into `ShutdownSlot` and `ShutdownPod` per §4.1.

The `SlotId` doc comment (`:584-586`) states the §4.2 rule instead of the concurrency condition, and each
of the per-field comments is replaced by a reference to it. `SendMessageRequest:945` and
`AttachRequest:958` gain the comment they lack.

`schemas/lenny-adapter-jsonl.schema.json` states the runtime-channel rule from decision 2 on all four
frames that carry `slotId` (`:58-61, 139, 161, 190`), rather than on one.

Generated code is regenerated rather than hand-edited.

### SPEC-1. State the rule where the conditional stood

The presence conditions at `spec/15_external-api-surface.md:1479, 1480, 1490, 1491, 1498, 1616`,
`spec/28_communication-channels.md:549, 604, 632, 665, 689, 1624`,
`spec/05_runtime-registry-and-pool-model.md:513`, `spec/29_communication-scenarios.md:52, 435`, and the
`spec/10_gateway-internals.md:157` sentinel sentence are replaced by the §4.2 rule. `spec/15:1616`'s
`slotId` moves from optional to required. `spec/28:588`'s Basic-level permission is restated against
holding one slot rather than against the pool's concurrency, per decision 2.

### SPEC-2. Define a slot

`spec/05_runtime-registry-and-pool-model.md` §5.2 gains the definition in §4.3, placed before the first use
of the term, and `spec/05:519`'s service-mode usage is stated as an instance of it. The glossary
(`docs/reference/glossary.md`) gains the matching entry.

### SPEC-3. Collapse the two filesystem layouts

`spec/06_warm-pod-model.md` §6.4 states one layout. Lines `:373` and `:395`, the "does not apply" and
"applies when 1" pair, are deleted. `:392`'s runtime obligation is stated unconditionally. `:11-12`'s
warm-pod checklist asserts `/workspace/slots/` and `/workspace/staging` per decision 4, and the
non-normative symlink from decision 5 is stated with its MUST NOT.

The `/workspace/current` references at `spec/04:263`, `spec/29:835`, `spec/15:2268`, `spec/10:171`, and the
remaining sites across `spec/07`, `spec/08`, `spec/13`, `spec/14`, `spec/24`, `spec/26`, and
`spec/28` are qualified to the slot path, and the corresponding sites under `docs/getting-started/`,
`docs/runtime-author-guide/`, `docs/reference/adapter-contract.md`, and `docs/reference/glossary.md` take
the same qualification. `spec/10:171`'s staging directory and atomic rename are stated per slot root.

### SPEC-4. Unify the credential path

`spec/06_warm-pod-model.md:26` and `:28` merge into one per-slot credential-lease paragraph.
`spec/13_security-model.md:26`'s fsGroup delivery paragraph and `spec/04` §4.7 item 4 name the per-slot
path. `spec/13:30`'s advice to deployers requiring strict lease isolation is restated: with §1.3 fixed the
isolation holds per slot, and the remaining reason to prefer one session per pod is the co-tenancy surface
in §4.4.

### SPEC-5. Rescope §29.10

`spec/29_communication-scenarios.md` §29.10 is split per §4.4. Its addressing mechanisms move to the
owning sections and its co-tenancy analysis stays, retitled to name the condition it actually depends on.
The three remaining unstated gaps stay with the co-tenancy half.

### SPEC-6. Correct the examples

`spec/15_external-api-surface.md:1592` and `spec/28_communication-channels.md:600` use a session identifier
as the example slot identifier rather than `"slot_01"`.

### CODE-1. Require the identifier at the boundary

`pkg/adapter/slot.go`: `useSlot` is deleted, every root-computing site resolves through
`slotlayout.Resolve`, and the pod-global fallback in `workspaceRootForSlot` (`:123-133`) is reduced to the
unknown-slot error branch. Each session-scoped handler validates through `slotlayout.ValidateSlotID` before
resolving a root and returns `InvalidArgument` on empty.

`pkg/gateway/runtime/adapterclient/client.go`: the paired `X`/`XSlot` methods collapse to one method each
taking a required identifier, and the nine `if slotID != ""` sites (`:147, 205, 289, 364, 399, 428, 845,
861, 900`) are removed. `pkg/gateway/session/executor/pod.go:146-148`'s concurrency-conditioned check
becomes an unconditional non-empty check and `ErrSlotIDRequired` keeps its name.

### CODE-2. Wire the four silent defects

Restore takes the slot identifier from `ResumeRequest` and resolves through `checkpointRootsForSlot`
(`pkg/adapter/resume.go:179`). `ExportPaths` resolves against the slot root
(`pkg/adapter/exportpaths.go:39, 48`). The §7.3 step (d) guard compares against the slot root
(`pkg/adapter/resume.go:61`), which makes the assertion load-bearing for the first time.

### CODE-3. Arm hold state per slot

`pkg/adapter/holdstate.go:91-96` arms from the slot registry rather than from the pod-global
`s.sessionID`, so a concurrent pod's slots each carry a hold. The dead pod-global read in
`s.currentSession()` is removed rather than left to be reintroduced.

### CODE-4. Pass the slot on credential operations

`pkg/gateway/runtime/adapterclient/client.go:234-241` and `:253-262` populate the identifier on rotation
and lease extension. The revocation path gains its gateway caller, or the adapter's `RevokeCredentials` is
removed if review establishes the Token Service path is the only intended one; §8 records this as open.

### CODE-5. Remove the sentinel

`partialmanifeststore.SlotDefault` (`:67-70`) and its four application sites (`:367-369`,
`pgstore/pgstore.go:157-159`, `pkg/gateway/sessionserver/derive.go:481`,
`pkg/gateway/checkpoint/checkpointer/checkpointer.go:500-503`) are removed, along with the ten-line
explanatory comment at `checkpointer.go:536-545` and the `slotIDField` nil-return at `:595-600`.

A migration converges `session_checkpoints.slot_id` (default `''`, migration 0112) and
`checkpoint_manifest.slot_id` (default `'default'`, migration 0178) onto a non-null column with no
default, and adds the column to `session_partial_checkpoint_manifest` (migration 0150). Rows carrying
either sentinel are backfilled from `session_id`, which decision 3 makes correct by construction.

### CODE-6. Reconcile the client SDKs

The `slotId` field is removed from the client-SDK message payloads
(`sdks/client/go/lenny/types.go:190`, `sdks/client/python/lenny/types.py:266, 279-280`,
`sdks/client/typescript/src/types.ts:168`), matching what the gateway accepts. The comment at
`pkg/gateway/sessionserver/messages.go:187-193` states that the field is not part of the client contract
rather than that a supplied value is ignored. A slot is assigned by the gateway and is not client-supplied.

### REG-1. Correct the register

`tests/claim-map.json`: the five rows in §1.5 move from `UNWIRED` to `ABSENT`. Three rows are added as
`UNWIRED` for the credential fields in §1.3, which match the definition. The rows CODE-2 and CODE-4 wire
move to `WIRED` as those changes land.

`scripts/seed-claim-register.py` stops inferring a status from a deferral step's scope note. A row naming a
field is `ABSENT` unless the field is present in the tree at seeding time, and the script reads the tree to
decide.

## 7. Amendments to other artifacts

Proposal 0072 drops §1.11 and REG-1 and cites this proposal for the register correction, per decision 7.
Its §1.3, which stages the specification half of the restore defect, is superseded by SPEC-3 and CODE-2 and
is dropped with a pointer here; the rest of 0072 is unaffected.

`PROPOSAL-QUEUE.md`'s C-53 entry is updated to record that its scope lands here, per decision 8.
`TEST-GAPS.md`'s T-4.4.21 is closed by the tier-4 case in §8. The summary-counts line is not edited.

## 8. Testing

**Tier 0.** A schema gate asserting the §4.1 classification holds: every session-scoped request message
carries the field and every pod-scoped one does not, read off the proto rather than from a fixed list, so
a message added later is classified rather than skipped. The claim-register validator over the corrected
rows.

**Tier 1, `pkg/adapter`.** A session-scoped request with an empty identifier is rejected with
`InvalidArgument` before a root is resolved, one case per handler. An unknown identifier returns
`FailedPrecondition`. `slotlayout.Resolve` is the only path builder reached.

**Tier 1, `pkg/gateway/checkpoint`.** No sentinel value reaches the store or the wire.

**Tier 3, `tests/tier3_contract`.** A capture on a pod with one slot and a restore of that checkpoint
resolve the same root. This is the general form of §1.2's first defect and the case that would have caught
it.

**Tier 4.** The end-to-end case C-53 exists to enable: a concurrent pool captures a slot's checkpoint,
loses the pod, and resumes onto a replacement with the workspace intact. Closes T-4.4.21. A second case
covers the same round trip on a pool with `maxConcurrentSessions: 1`, which under this proposal exercises
the identical path.

**Tier 9.** A credential rotation on a concurrent pod rewrites only the rotating slot's credential file and
leaves the co-tenant slot's lease intact. This is §1.3, and it belongs in the security tier because the
defect is a cross-session credential read.

**Tier 10.** The conformance battery's expectation that a single-session pod's response carries no
identifier (`tests/tier10_conformance/concurrent_slot_conformance_test.go:217-218`) is inverted, and the
whole-pod-scrub expectation (`pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651`) is restated
against `ShutdownPod` per §4.1. `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:480-481`
takes the same inversion.

**Tier 11.** The documented workspace path matches the specified one across `docs/` and `spec/`.

## 9. Open questions for review

- **`RevokeCredentials`.** Whether the adapter's handler should gain a gateway caller or be removed. The
  Token Service path (`pkg/gateway/credassign/client.go:306`) may be the only intended revocation, in
  which case the adapter handler is dead code and CODE-4 shrinks.
- **Decision 4.** §5 states the reasoning and the one assertion it invalidates.
- **The runtime-channel boundary.** Decision 2 keeps `slotId` advisory on the JSONL channel. If review
  prefers uniformity there too, the cost is that every third-party runtime implements slot dispatch, and
  `cmd/runtimes/echo` and `cmd/runtimes/echo-concurrent` collapse into one reference rather than two.

## 10. Files touched on application

- `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, and the regenerated
  `pkg/proto/adapter/v1/`.
- `spec/04`, `spec/05`, `spec/06`, `spec/07`, `spec/08`, `spec/10`, `spec/13`, `spec/14`, `spec/15`,
  `spec/24`, `spec/26`, `spec/28`, `spec/29`.
- `docs/getting-started/`, `docs/runtime-author-guide/`, `docs/reference/adapter-contract.md`,
  `docs/reference/glossary.md`, `docs/reference/configuration.md`.
- `pkg/adapter/` (`slot.go`, `slotlayout/`, `resume.go`, `exportpaths.go`, `holdstate.go`, `session.go`,
  `checkpoint.go`, `staging.go`, `credentials.go`).
- `pkg/gateway/runtime/adapterclient/client.go`, `pkg/gateway/session/executor/pod.go`,
  `pkg/gateway/checkpoint/` (`partialmanifeststore/`, `checkpointer/`),
  `pkg/gateway/sessionserver/` (`derive.go`, `messages.go`), `pkg/gateway/podlifecycle/podsession/`.
- `sdks/client/go/`, `sdks/client/python/`, `sdks/client/typescript/`.
- A new migration converging the three `slot_id` columns.
- `tests/claim-map.json`, `scripts/seed-claim-register.py`, `PROPOSAL-QUEUE.md`, `TEST-GAPS.md`,
  `proposals/0072_fix_correct-the-inconsistencies-the-scenario-authoring-surfaced.md`.
- The tier-0, 1, 3, 4, 9, 10, and 11 cases in §8 and their `tests/spec-map.json` entries.
