# Proposal: Give every session a slot and absence one meaning

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Every session is bound to a slot on every pod, whatever the pool's concurrency, so that the
  absence of a slot has exactly one meaning. The wire addresses that session by its session identifier on
  both the gateway-to-adapter gRPC leg and the adapter-to-runtime JSONL leg, and the duplicate `slot_id`
  fields are removed from the gRPC leg. A request message's scope is declared in a specification table
  rather than encoded by which address field it carries. The per-slot filesystem tree becomes the only
  layout and the pod-global `/workspace/current` is retired rather than kept as a second name for it. The
  identity invariant that a session-mode slot's identifier is its session's identifier is stated in the
  specification for the first time, the persisted duplicate columns are dropped, and the specification
  sentences that attribute slot assignment to the adapter are corrected. The proposal also corrects the
  defects the conditional rule has produced, the register rows that mis-record them, and the divergence
  between what the specification states and what the code branches on.

This document stages the proposed specification, code, schema, and register changes. It does not modify
any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Four facts overturn the way this question is usually posed, and the design in §4 rests on them.

The conditional rule is **not implemented**. The specification states a rule keyed on the pool's
concurrency. The code implements a rule keyed on field presence, and the two are not the same rule. No
production code path tests `maxConcurrentSessions` to decide whether to emit or honor a slot identifier.

Absence is **already load-bearing** as a pod scope, on concurrent pods as well as single-session ones. It
is not merely the encoding of "this pool has one session per pod".

The persistence layer has **already gone uniform** and pays a fiction to do it. This proposal removes the
fiction rather than introducing the uniformity.

The slot identifier is **already the session identifier**, at the one site that mints it
(`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`, `SlotID: req.SessionID`). The specification has
never said so, and the two-namespace machinery the adapter and the wire carry exists because it has never
said so.

## 1. Problem

### 1.1 The specification states one rule and the code implements another

`spec/15_external-api-surface.md:1593` states that messages on a pod whose pool sets
`sessionPolicy.maxConcurrentSessions: 1` "never carry `slotId`, and runtimes on those pods never see it".
`spec/28_communication-channels.md:547-551`, `:566-571`, `spec/05_runtime-registry-and-pool-model.md:513`,
and `spec/06_warm-pod-model.md:373` state the same condition, and `schemas/lenny-adapter.proto:584-586`
repeats it in the `SlotId` doc comment.

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
returns the pod-global root. `ResumeRequest` does carry an identifier (`SlotId slot_id = 15`,
`schemas/lenny-adapter.proto:1361`); `pkg/adapter/resume.go` ignores the one it is handed. That is a
stronger form of the same finding than a missing field would be: the address arrives and is discarded. A
slot's checkpoint extracts into a directory no slot's runtime reads, no error is returned, and `resumeMode`
reports a normal full restore.

**Delegation file export.** `pkg/adapter/exportpaths.go:39, 48` resolves every export against the pod-global
`s.WorkspaceRoot`. `ExportPathsRequest` (`schemas/lenny-adapter.proto:1478`) carries
`SessionId session_id = 1` and no slot field, so the address the handler needs is present and unread. On a
concurrent pod the export packages files from a tree no slot owns. This is the §8.7 delegation payload path.
`ConfigureWorkspaceRequest` (`:1609`) is in the same position.

**Coordinator hold state.** `pkg/adapter/holdstate.go:91-96` arms the hold from `s.currentSession()`, which
reads the pod-global `s.sessionID` that only `claimSession` sets (`pkg/adapter/session.go:361-372`).
`StartSession` returns early into `startSessionSlot` (`pkg/adapter/session.go:103-106`) and never calls it,
so `s.sessionID` is empty for the life of a concurrent pod and the hold never arms. §10.1 coordinator-loss
protection is absent on exactly the pods that multiplex the most sessions. §1.9 generalizes this finding
from the hold path to the whole session-validation path.

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

Each of the three requests carries `SessionId session_id = 1` beside the unpopulated `slot_id`, so the
address the handler needs is on the wire already and the fix is to read it (§4.5).

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
branch that has no assigned slot state". `slotIDField` (`:586-600`) returns `nil` for an empty identifier.
Proposal 0050 recorded the same rejection at `proposals/0050_fix_slot-aware-checkpointing...md:37`.

So the design has already concluded that a universal scoping key is necessary, adopted a fabricated one to
obtain it, and been unable to use it on the wire because a fabricated identifier is indistinguishable from
a real one. The storage layer and the wire are decoupled, and the coupling point is the presence branch in
§1.1.

The sentinel is also not applied consistently: `session_checkpoints.slot_id` defaults to `''`
(`migrations/0112_session_checkpoints_slot_id.up.sql:19`) and `checkpoint_manifest.slot_id` defaults to
`'default'` (`migrations/0178_checkpoint_manifest.up.sql:38`). The third table an earlier reading of this
defect named, `session_partial_checkpoint_manifest`, does not exist at head:
`migrations/0178_checkpoint_manifest.up.sql:25-28` drops its index, its RLS policy, its tenant guard, and
the table itself, and replaces it with `checkpoint_manifest`.

### 1.5 The wire carries the address twice, under two names

On the gateway-to-adapter gRPC leg, eighteen request messages carry a `SlotId slot_id` field
(`schemas/lenny-adapter.proto:449, 684, 716, 837, 917, 948, 967, 999, 1018, 1039, 1057, 1091, 1170, 1275,
1361, 1444, 1539, 1556`). Sixteen of them also carry `SessionId session_id` as field 1, and the two fields
hold the same string whenever both are populated, because the one site that mints a slot identifier
returns the session identifier (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`). One message,
`CheckpointStart` (`:1151-1171`), carries `slot_id` and no session field at all, so its `slot_id` is the
checkpoint stream's sole per-session address. One, `ShutdownRequest` (`:1548-1572`), uses the field's
presence as a teardown-scope discriminator rather than as an address.

On the adapter-to-runtime JSONL leg, five frames carry `slotId`
(`schemas/lenny-adapter-jsonl.schema.json:58, 139, 161, 190, 222`) and no frame carries a field addressing
the receiving session. The `message` envelope's required `from.id` block (`:25, :70-84`) is
`sess_{session_id}` and names the *sending* session, which is a different thing.

The duplication has a recorded origin and no surviving rationale. The design record's argument for a
distinct slot namespace was channel multiplexing, by analogy to HTTP/2 stream identifiers. No opacity
argument, no entitlement argument, and no ordinal-pool argument appears anywhere in the specification, the
design record, or the code. The analogy does not hold: an HTTP/2 stream identifier is a per-connection
ordinal precisely because there is no other name for the stream, and here there is.

### 1.6 The identity invariant is unstated, and the assignment is misattributed

The specification never states that a slot identifier equals a session identifier. The invariant lives in
Go comments and in one assignment. Everything downstream of it, including the reconstruct-from-session-id
path at `pkg/gateway/sessionserver/start.go:2107` and the checkpoint scoping key in §1.4, is correct only
because of it.

Five specification sentences state or presuppose the opposite of the truth about who assigns the
identifier. `spec/05_runtime-registry-and-pool-model.md:513` states "the adapter assigns a `slotId` per
slot"; `spec/28_communication-channels.md:548` and `:569` say the same; `spec/29_communication-scenarios.md:1435`
repeats it; and `spec/06_warm-pod-model.md:391` presupposes it with "The adapter creates the slot directory
on `slotId` assignment". The gateway mints the identifier at claim time. The adapter creates the tree on
first reference to an identifier it was handed (`pkg/adapter/slot.go:73-88`, `ensureSlotStateLocked`
calling `slotlayout.EnsureTree`). Six code comments repeat or re-import the error, two of them by quoting
`spec/06:391` verbatim as their spec basis.

### 1.7 Three surfaces state a contract the platform does not honor

All three client SDKs expose and serialize a `slotId` on the message payload
(`sdks/client/go/lenny/types.go:189-190`, `sdks/client/python/lenny/types.py:266, 279-280`,
`sdks/client/typescript/src/types.ts:168`). The gateway deliberately drops it:
`pkg/gateway/sessionserver/messages.go:186-193` states that "A client-supplied slotId is silently ignored
because the field does not deserialize onto the payload", and `spec/07_session-lifecycle.md:333` states that
clients do not address slots directly. This is a contradiction between the SDKs and the contract rather
than a live cross-session addressing vector.

`spec/15_external-api-surface.md:1569` and `spec/28_communication-channels.md:600` give `"slot_01"` as the
example slot identifier, an ordinal, as do `docs/reference/adapter-contract.md:316` and
`schemas/examples/jsonl.set_tracing_context.json:8`. The implementation uses the session identifier.
`docs/reference/adapter-contract.md:140, 180, 232, 271` document four `"slotId": null` examples that fail
the published schema, which types the field as `string`.

`SendMessageRequest` (`schemas/lenny-adapter.proto:948`) and `AttachRequest` (`:967`) carry `slot_id` with
no doc comment. The JSONL schema states the concurrency condition on the `message` frame
(`schemas/lenny-adapter-jsonl.schema.json:58-61`) and states nothing on `tool_call` (`:139`), `tool_result`
(`:161`), `response` (`:190`), or `set_tracing_context` (`:222`).

### 1.8 The specification never defines what a slot is

There is no glossary entry and no standalone definition. Every definitional sentence derives the concept
from concurrency: `spec/05_runtime-registry-and-pool-model.md:513`, `spec/29_communication-scenarios.md:1434`,
and `spec/28_communication-channels.md:139`.

Service mode already uses the term without `sessionPolicy` at all
(`spec/05_runtime-registry-and-pool-model.md:519`), where a slot is unnamed per-pod request capacity: a
counted in-flight request with no identifier, no session binding, no workspace subtree, no credential
lease, and no tracked lifecycle (`pkg/adapter/statelessslot/statelessslot.go` implements it as a bare
`Gate.Acquire`/`Release` counter). The word therefore carries two senses, and the specification
distinguishes neither.

### 1.9 The adapter validates a session in two places, and one of them is going dead

`checkSession` (`pkg/adapter/session.go:346-356`) reads the pod-global `s.sessionID` and returns
`FailedPrecondition "pod has no assigned session"` when it is empty. Three sites write that field:
`claimSession` (`:361-372`), `releaseSession` (`:383-386`, which clears it), and `claimSessionForConfigure`
on the SDK-warm `ConfigureWorkspace` path (`pkg/adapter/sdkwarm.go:290-301`). `startSessionSlot`
(`pkg/adapter/slotsession.go:27-52`) records the session on the slot-registry entry (`st.sessionID = sessionID`,
`:45`) and never touches `s.sessionID`. The per-slot check is `checkSlotSession` (`slotsession.go:116-128`).

Eight handlers call `checkSession`. Five of them (`pkg/adapter/lifecycle.go:30` Interrupt, `:77`
SignalDeadline, `pkg/adapter/coordination.go:89` CoordinatorFence, `:216` CheckpointBarrier, and
`pkg/adapter/usage.go:264` ReportUsage) have no slot branch at all. Once every session holds a slot,
`StartSession` always lands in `startSessionSlot`, `claimSession` is never called, `s.sessionID` stays
empty for the life of every pod, and those five handlers fail for every session. The Shutdown
recycle-scrub guard at `pkg/adapter/session.go:242-245`, which fires when `s.currentSession() == ""`,
becomes universally true and short-circuits every recycle Shutdown into `startPodScrub`.

## 2. Decisions

The decisions are numbered D1 through D15 and are referenced by those numbers throughout. §9 records what
was considered and rejected.

**D1. Every session is bound to a slot, on every pod, and absence has exactly one meaning.** A pod serving
one session at a time holds one slot exactly as a pod serving four holds four. No slot exists or fails to
exist because of a pool's concurrency, and no addressed surface resolves an absent identifier to a scope
other than the one the caller already holds. This is what makes §1.2's ambiguity unconstructible rather
than merely fixed: the adapter no longer holds a branch that maps an unaddressed request to a pod-global
layout, so a call site that omits the address produces an error rather than a plausible result.

The statement is about what absence may mean, rather than a prohibition on ever reading the pod's slot
count. §4.6.1's inbound rule on the runtime-to-adapter leg tolerates an absent identifier on a pod holding
one slot and rejects it on a pod holding more, and §4.6.1 and §9 state why that tolerance is not the
conditional this decision retires.

**D2. The wire addresses a session by its session identifier, on both legs.** "Slot" survives as the name
of the pod-side resource a session occupies. The gateway mints and allocates it, the adapter registers it,
and leaked or failed slots are retained until the pod terminates and still count against pod occupancy.
The runtime addresses no slot: it never allocates, releases, or reasons about slot occupancy, and it holds
no namespace distinct from the session identifier it is handed. The runtime does consume that identifier
structurally, because a runtime on a concurrent pod derives its working directory as
`/workspace/slots/{identifier}/current/` (`spec/06_warm-pod-model.md:392`,
`spec/05_runtime-registry-and-pool-model.md:513`), which the tier-10 battery asserts
(`tests/tier10_conformance/concurrent_slot_conformance_test.go:225-238` against
`cmd/runtimes/echo-concurrent/slot.go:117-127`). The claim is that the runtime holds no second namespace,
rather than that it never sees the path.

The adapter keeps its slot registry internally. Under D5 the registry is keyed by the session identifier,
so the key lookup is the identity function. The entry's `sessionID` field is not an address; it is a
binding state. `ensureSlotStateLocked` (`pkg/adapter/slot.go:74-95`) creates an entry with `sessionID`
empty, reached from `ensureSlotPaths` (`:109-117`) by the three workspace-prep RPCs
(`pkg/adapter/staging.go:129, :181, :339`) before `StartSession`. The field is assigned in
`startSessionSlot` (`pkg/adapter/slotsession.go:45`) or, when credentials arrive first, in
`assignSlotCredentials` (`pkg/adapter/slotcreds.go:33-34`), and it is never cleared while the entry lives,
because `releaseSlot` deletes the entry (`slotsession.go:102-112`). The registry therefore distinguishes
three states, absent, registered-but-unbound, and bound, and `st.sessionID == sessionID` reads under D5 as
a bound test rather than as a mapping test. The test survives at every site that performs it today:
`shutdownSlot` (`slotsession.go:70`), the credential checks (`slotcreds.go:35, :68, :120`), and
`checkpointRootsForSlot` (`slot.go:152-163`). At the fourth site, `checkSlotSession`
(`slotsession.go:116-128`), the test survives while the function does not: D13 and §4.11 merge
`checkSlotSession` and `checkSession` into one `checkSessionBound(sessionID)` that performs the same bound
test through `s.slots`, and CODE-1 stages the merge. Only the gateway-to-adapter mapping is the identity
function; the registry's session comparison is not. The registry becomes a mapping between two distinct
namespaces only if a future capability needs one, and §9 records why that is a cheap change to make later.

**D3. The duplicate `slot_id` fields are removed from the gRPC surface in both directions, message by
message.** The surface is the gateway-to-adapter `service Adapter` and the adapter-to-gateway
`service GatewayControl`. §4.5 enumerates the eighteen messages individually and splits them into five
groups: (a) the two messages that already carry `session_id` and gain no field, (b) the gateway-to-adapter
removal set of fifteen, (b′) the adapter-to-gateway removal set of one, `ReportSessionScrubRequest`,
(c) the replacement set of one, `CheckpointStart`, and (d) the conditional set of one, `ShutdownRequest`.
Sets (b), (b′), and (c) remove seventeen fields between them. There is no blanket property being asserted:
`CheckpointStart` gains a session field before it loses its slot field, `ReportSessionScrubRequest`'s
removal is behavior-preserving on its own because the consumer already discards the argument, and
`ShutdownRequest`'s removal is conditional on the `ShutdownSlot`/`ShutdownPod` split landing.

**D4. The JSONL leg carries the per-session identifier on every session-scoped frame on every pod, and the
frame field is renamed `slotId` to `sessionId`.** The population change is forced by D1 and is separable
from the rename; §4.6 states each half and what it costs. The rename is D2's realization on the JSONL leg,
so the only field addressing the receiving session is named for what it carries. It changes no behavior,
because under D5 the field already carries the session identifier.

**D5. The identity invariant becomes normative, scoped to session mode.** A session-mode slot's identifier
is the identifier of the session bound to it, and the gateway mints both as one value at claim time. The
invariant rests on one mint site, `pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`, with the session
identifier a UUIDv8 from `session.NewID()` (`pkg/api/v1/session/session.go:39-45`) that is never
caller-supplied. It is idempotent across a resume onto a replacement pod, which is what makes the §7.3
step (d) assertion in §1.2 meaningful instead of vacuous; it is globally unique, so a recycled pod
accumulates no cross-session residue; and it is reconstructable from persisted columns. `'default'` is
rejected for the recycle-residue reason, and an ordinal is rejected because it needs a per-pod allocator
and breaks reconstructability.

A service-mode slot is a different thing and is stated separately where §5.2 defines service mode:
unnamed per-pod request capacity, per §1.8. The two senses are distinguished at their points of use rather
than unified.

**D6. A request message's scope is declared in the specification, not derived from its field set.**
`CoordinatorFenceRequest` carries `SessionId session_id = 1` (`schemas/lenny-adapter.proto:1403-1409`) and
is pod-scoped: the fence state is a single `coord coordinationState` field on `Server`
(`pkg/adapter/server.go:301-304`) mutated pod-wide (`pkg/adapter/coordination.go:97-121`), and the session
identifier there is a binding check rather than an address (`coordination.go:85-91`). Because `session_id`
is present on pod-scoped messages too, the presence of a single address field cannot separate the classes.
§4.1 states the classification table and §4.2 states the value rule that replaces the presence rule.

**D7. The pod filesystem layout is the slot layout, on every pod.** A slot is the unit a session's files
are materialized into, whatever the pool's concurrency, so there is one layout rather than a pair selected
by `maxConcurrentSessions`. A pod serving one session at a time materializes into
`/workspace/slots/{sessionId}/current` exactly as a pod serving four does. The per-tree paths are unchanged
from what concurrent pods use today: `/workspace/slots/{sessionId}/{current,staging}`,
`/sessions/{sessionId}`, `/artifacts/{sessionId}`, and `/run/lenny/slots/{sessionId}`. What changes is that
they are no longer conditional.

The container directory keeps the name `slots`: it names the pod-side resource class, and the leaf names
its occupant. Renaming it to `sessions` would collide with the existing sibling tree `/sessions/{...}`,
which carries per-session runtime state (`spec/06:377-390`; `slotlayout` `Roots.Sessions` and
`Paths.Sessions`, `pkg/adapter/slotlayout/slotlayout.go:74-76, :95-96, :145-146`). What changes in the
documented layout is the placeholder token, from `{slotId}` to `{sessionId}`, stated once at the §6.4
layout block and applied at every documented path site.

Two alternative arrangements were measured against the tree and rejected; §5 records the evidence under
"Where the identifier sits in the path". Putting the identifier above the trees, so that one root holds
everything a slot owns (`/slots/{sessionId}/workspace`, `/slots/{sessionId}/sessions`), reads better to a
consumer, but the four trees are distinguished by mount path and carry different media: `/sessions` is a
memory-backed `emptyDir` whose medium §6.4 states the data-at-rest guarantee on, `/workspace/shared` takes
its immutability from a nested read-only mount, and `/run/lenny` carries a tmpfs and a group-ownership
boundary. Collapsing them into one volume regresses a normative guarantee; preserving them puts the
identifier back under each tree, which is this decision. Dropping the `slots/` segment, giving
`/workspace/{sessionId}/current`, collides with the `shared` mount point and the pod-global `staging` and
`.staging` directories that live in the same namespace.

**D8. Slot directories are created at assignment, which is what concurrent pods already do.** This
resolves the ordering problem that an identifier equal to the session identifier cannot exist at warm
time. A warm pod pre-creates `/workspace/slots/` and `/workspace/staging` and holds no slot tree, and the
adapter creates the slot tree on the assignment edge exactly as it does today on a concurrent pod. No new
mechanism is introduced, and workspace materialization already happens at assignment, so §6.3's latency
accounting is unchanged. §5 records this as the decision most worth a second opinion.

**D9. `/workspace/current` is retired rather than kept as a symlink.** An earlier draft of this decision
kept it as a non-normative debugging link on a pod holding one slot. Two mechanical facts retire it.
`promoteStaging` renames the `current` directory on every workspace finalization
(`pkg/adapter/workspace/materialize.go:391-408` renames `current` to a backup and the build directory into
its place), so a symlink there is replaced by a real directory the first time a session materializes files,
and the link would have to be recreated after every promotion to stay true. And
`tests/tier4_integration/concurrent_workspace_test.go:196-199` already asserts that nothing writes
`/workspace/current` on a concurrent pod, which a link into a live slot tree contradicts. A path that is
sometimes a link, sometimes a directory, and sometimes stale is worse for the operator it was meant to
serve than no path at all. The adapter stops creating `/workspace/current`, and an operator reaching a
workspace by `kubectl exec` reaches it at its slot path like every other consumer.

**D10. The persisted `slot_id` columns are dropped rather than converged and backfilled.** Under D5 the
persisted value carries one of two things and never information beyond `session_id`: on a concurrent pod
it is the session identifier, and on every other pod `binding.SlotID` is empty
(`pkg/gateway/podlifecycle/podsession/binder.go:490-499`) and the gateway substitutes a pure sentinel. The
scoping key the sentinel was introduced to make well-defined (`spec/10:157`) is already well-defined on
`session_id` alone. There is no row whose value is worth preserving, so there is nothing to backfill.
§4.9 states the column drops and the index re-keys.

**D11. The specification's attribution of slot assignment to the adapter is corrected.** The gateway mints
the identifier at the single site named in D5. The adapter creates the tree on first reference. §1.6 names
the five specification sentences and the six code comments.

**D12. Ordinal examples and the rules keyed on identifier order are corrected.** The `slot_01` examples are
replaced by session identifiers, the four `"slotId": null` documentation examples are corrected, and the
sentences that describe promotion and upload serialization as running in "slot-ID order" are restated as a
lexicographic tie-break over opaque identifiers, chosen for reproducibility rather than for any ordinal
meaning. §4.10 states the justification and the liveness property it does not claim.

**D13. The adapter's session validation is unified on the slot registry.** `checkSession` and
`checkSlotSession` become one `checkSessionBound(sessionID)` resolving through `s.slots`, and
`Server.sessionID` is retired with `useSlot`. Under D5 the two checks are the same check, because the
lookup key and the compared value are the same string. This is atomic with CODE-1; §1.9 states what breaks
without it. A surviving `Server.sessionID` would be the same pressure D10 identifies in the persistence
layer, a second namespace for the same value that pulls the two-namespace model back in.

**D14. The register rows 0072 would correct are retired here instead, and neither proposal makes the
correction.** Proposal 0072 §1.11 and its REG-1 stage a status correction, from `UNWIRED` to `ABSENT`, on
register rows this proposal retires. That correction is not made by either proposal, because REG-1
establishes that the rows are already correct: every field the rows name is present in the tree today, so
`UNWIRED` is the right status under §28.4's definition. What happens to the rows instead is that SCHEMA-1
removes the fields and REG-1 retires the rows outright with them. Proposal 0072 therefore drops §1.11 and
REG-1 rather than handing them over, since there is no correction left to hand. §7 records that amendment,
and also records the amendment this proposal makes to proposal 0064's deliverable.

**D15. This proposal supersedes C-53's scope and closes T-4.4.21.** C-53 (`PROPOSAL-QUEUE.md:570-576`) owns
the slot-aware restore companion as a point fix. Its deliverable is a slot-scoped restore path, which
CODE-2 stages here alongside the sibling defects the point fix would have left standing: the export and
step (d) defects in CODE-2, and the hold-state defect in CODE-3. §7 records
the queue update.

### Decisions retired from the earlier draft

The earlier draft carried a decision 1 and a decision 2 that this revision replaces, and convergence should
not restore either.

The earlier decision 1 required a distinct `slot_id` field on every session-scoped message and forbade it
on every pod-scoped one. Its first half survives as D1: every session holds a slot and absence has exactly
one meaning. Its second half, that the *address* is a distinct `slot_id` field whose presence encodes
scope, is replaced by D3 and D6. Scope is declared rather than encoded, and the address is `session_id`.

The earlier decision 2 scoped the change to the gateway-to-adapter leg and preserved the JSONL leg's
advisory rule. It does not survive D1, for the reason §4.6 states: once every session holds a slot, the
adapter's presence-keyed stamping sends a tagged frame to a single-session pod's runtime, which
`spec/15:1593` forbids today and the tier-10 battery asserts against. The only way to preserve the earlier
decision 2 is an adapter rule that withholds the identifier on a pod holding one slot, which is a
live-slot-count conditional and restores absence as an address on the JSONL leg. It is the conditional this
proposal exists to retire. Decision 2 is replaced rather than qualified.

## 3. Out of scope, and why

The condition expressions of the isolation gates keyed on `maxConcurrentSessions > 1` are untouched and
stay keyed on it: `acknowledgeProcessLevelIsolation` (`spec/05:431, 515`), tenant pinning (`:433, 440,
517`), the `allowCrossTenantReuse` prohibition (`:447, 517`), the `CAP_NET_RAW` rejection (`:515`), the
`preConnect` admission rule (`:434`, `spec/06:71, 77`), and the `ConcurrentWorkspaceCredentialSharing`
condition (`spec/13:30`). None of them depends on how a session is addressed on the wire; they are
statements about isolation posture, and a pod serving one session in one slot has the posture it always
had.

What is out of scope is the condition each gate tests, rather than the sites those conditions appear at.
Two edits land at `spec/13:30` around the `ConcurrentWorkspaceCredentialSharing` condition itself. SPEC-4
restates the surrounding advice to deployers requiring strict lease isolation, because §1.3's fix changes
the reason that advice gives, and §4.7 renames the path placeholder in the same sentence from `{slotId}` to
`{sessionId}`. The condition expression the gate tests is what neither edit changes.

The sizing, admission, capacity, draining, and recycling arithmetic is untouched. Every formula is keyed on
the number of slots and already degenerates correctly at one (`spec/05:588`, `spec/10:136`).

`/workspace/shared/`'s contents, its read-only mount, and the population step that fills it are untouched.
What changes is the specification's claim that the directory exists only when `maxConcurrentSessions > 1`
(`spec/06:397`, `spec/26:44`). The adapter mounts and populates it on every pod at warm time, before any
slot exists, so the qualifier is a divergence rather than a rule; SPEC-3 drops it and corrects the
attribution in the same paragraph. The tree stays pod-wide and stays outside every slot root, for the
reason §5 records under "Where the identifier sits in the path".

The four gaps `spec/29_communication-scenarios.md:1541-1564` records as unstated are not closed here, with
one exception. §29.10 currently scopes them to concurrent pods, and §4.4 addresses the scoping rather than
the gaps. The exception is adapter hold-state partitioning, which §1.2 establishes is a live defect rather
than an unstated gap, and which CODE-3 fixes.

The service-mode sense of "slot" is named and distinguished (D5) and is otherwise untouched. Service mode
has no adapter slot registry, no CH-MSGSOCK frames, and no `/workspace/slots/` tree, so D2, D3, D4, D7, and
D10 do not reach it.

The unbounded-overtake window in the operation lock is recorded as a separate finding rather than fixed
here. `release` (`pkg/adapter/oplock.go:154-175`) picks the lowest key present at release time and `Begin`
(`:99-107`) re-enqueues a slot as soon as its previous operation is promoted, so a slot with a high
identifier can be overtaken without bound by a faster-cycling sibling with a lower one. Per-slot coalescing
(`errOpCoalesced`, `:33-36`) bounds the pending set's size rather than the overtake count. The same window
applies to the `spec/05:542` upload-serialization rule and the `spec/04:691` queue-depth rule. D12
corrects how those rules are *described*; it does not change what they do.

Renaming the `pkg/adapter/slotlayout` package and its exported identifiers (`Roots`, `SlotPaths`,
`Resolve`, `ValidateSlotID`, `EnsureTree`, `RemoveTree`) from the `slot` stem is out of scope. The
package's subject is the pod-side resource, which keeps its name under D2. What lands there is the
placeholder token in its package doc and path templates (D7, §4.7).

The exclusion is on that package's own surface rather than on every identifier holding the stem, and three
deliverables rename identifiers outside it. CODE-1 collapses `checkSession` and `checkSlotSession` into
`checkSessionBound` and renames `ErrSlotIDRequired`
(`pkg/gateway/session/executor/pod.go`) for the non-empty session identifier it now requires. CODE-6
renames the four adapter frame helpers that are named for the wire key §4.6.2 renames, giving
`frameSessionID`, `stampSessionID`, `demuxSessionOutput`, and `writeSessionEnvelope`; they do not keep
their current names, because a helper named for a field the frame no longer carries is the divergence D4
exists to close, and the rename rides with §4.6.2 rather than landing without it. §4.8 renames the
parameters of `tracingFrameAddressesStream` and `handleSetTracingContext`
(`pkg/adapter/tracingcontext.go`) from `slotID` to the session identifier they carry. Every other Go
identifier holding the stem, including the slot registry `Server.slots`, `slotState`, `startSessionSlot`,
`releaseSlot`, and `assignSlotCredentials`, names the pod-side resource and is untouched.

## 4. Detailed design

### 4.1 Every request message's scope is declared

Each request message is either session-scoped or pod-scoped. Under D6 the classification is declared in a
table in §4.1 of the specification rather than derived from the field set, because `session_id` appears on
messages of both classes.

The table's coverage is one of two choices, and the proposal states which it takes:

(a) The table classifies every request message on both services, with the `GatewayControl` rows carrying a
direction column, `ReportSessionScrubRequest` classified session-scoped and `ReportPodScrubRequest`
pod-scoped.

(b) The table is scoped to `service Adapter`, and the specification states in the same place what
classifies the `GatewayControl` request messages and why they are outside the gate.

Under (b) the limit in §9 applies: the `GatewayControl` request messages are outside the tier-0 gate
entirely. Choice (a) has no such limit and is the base case this proposal takes; review may take (b).

**Session-scoped.** The operations that address one session's slot: `PrepareWorkspaceRequest`,
`FinalizeWorkspaceRequest`, `RunSetupRequest`, `ConfigureWorkspaceRequest`, `StartSessionRequest`,
`SendMessageRequest`, `AttachRequest`, `ExportPathsRequest`, `AssignCredentialsRequest`,
`RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`, `RevokeCredentialsRequest`, `InterruptRequest`,
`SignalDeadlineRequest`, `ResumeRequest`, `CheckpointStart`, `CheckpointBarrierRequest`,
`ReportUsageRequest`, and (on `GatewayControl`) `ReportSessionScrubRequest`.

**Pod-scoped.** `ReportPodScrubRequest`, `CoordinatorFenceRequest`, `NegotiateVersion`, and the pod-scrub
and SDK-demotion paths. These address the whole pod. `CoordinatorFenceRequest` carries `session_id` and
stays pod-scoped, which is why the classification is declared rather than derived.

`ShutdownRequest` is the one message that is genuinely both, and it is the case
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651` pins: a recycle shutdown is a whole-pod
scrub rather than a per-slot teardown, and that is true on a concurrent pod as well as a single-session
one, because a recycle returns the whole pod to the warm pool and scrubs every tree on it rather than
releasing one occupant's slot.
The base case is that it is split into a session-scoped `ShutdownSlot` and a pod-scoped `ShutdownPod`, so
the distinction the test encodes is expressed in the type rather than in a field's presence. The split is
the one part of this classification review may decline; §4.5(d) states what `ShutdownRequest` keeps if it
does, §4.5(g) states the wrapper type's fate either way, and §9 records the choice as open.

### 4.2 The rule

The rule is a value rule on the single address field rather than a presence rule on a second one.

A session-scoped request whose `session_id` is empty is rejected at the adapter boundary with
`InvalidArgument`, before any root is resolved. A pod-scoped request never resolves a session root
regardless of what `session_id` carries. `slotlayout.ValidateSlotID`
(`pkg/adapter/slotlayout/slotlayout.go:114-131`) already rejects the empty string and already guards
against path traversal, so the check is a call rather than new logic.

`useSlot` (`pkg/adapter/slot.go:43-45`) is deleted. Every root-computing site (`workspaceRootForSlot` at
`slot.go:123-133`, `checkpointRootsForSlot` at `:147-173`, `slotStagingDir` at `staging.go:123-133`, and the
credential handlers at `credentials.go:74, 116, 158, 331`) resolves through `slotlayout.Resolve`
unconditionally. The pod-global fallback in `workspaceRootForSlot`, which today absorbs both an empty
identifier and an unknown one, keeps only the unknown-slot branch and returns `FailedPrecondition` as
`checkpointRootsForSlot` already does at `slot.go:160-163`.

This still makes §1.2's silent degradation unconstructible, because the adapter no longer has a branch
mapping an unaddressed request to the pod-global layout. A call site that forgets to populate the address
stops producing a plausible pod-global result and starts producing a loud error at the first call.

### 4.3 What a slot is

§5.2 gains the definition the specification lacks, stated independently of concurrency. A session-mode slot
is the unit of per-pod session capacity, owning a workspace subtree, a credential lease, and a lifecycle,
and identified by the identifier of the session bound to it. A pod holds between zero and
`sessionPolicy.maxConcurrentSessions` slots; a warm pod serving no session holds none, per D8.
`sessionPolicy.maxConcurrentSessions` keeps its meaning
exactly: the ceiling on simultaneous slots. What changes is that one is no longer a special case of the
ceiling but the smallest value of it.

A service-mode slot is a different thing, per D5 and §1.8, and §5.2 states the distinction where it defines
service mode. The glossary entry carries both senses.

A slot is a real pod-side resource rather than a synonym for a session. Leaked and failed slots are
retained until the pod terminates and still count against pod occupancy, which is why the word survives
even though it names nothing on the wire.

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

### 4.5 The gRPC leg, message by message

Eighteen messages carry a `SlotId slot_id` field. They split first by service and then by treatment.
Seventeen are on `service Adapter` (`schemas/lenny-adapter.proto:32`), the gateway-to-adapter surface. One,
`ReportSessionScrubRequest`, is on `service GatewayControl` (`:250`), the adapter-to-gateway surface whose
doc comment at `:234-237` states that the adapter initiates every RPC on it.

**(a) No field is added.** `ExportPathsRequest` (`:1478`) and `ConfigureWorkspaceRequest` (`:1609`) each
carry `SessionId session_id = 1` and no slot field. The delegation-export defect §1.2 records is fixed by
reading `session_id`.

**(b) Gateway-to-adapter removal set, fifteen messages.** `PrepareWorkspaceRequest` (`slot_id` #4),
`FinalizeWorkspaceRequest` (#5), `RunSetupRequest` (#4), `StartSessionRequest` (#11), `SendMessageRequest`
(#2), `AttachRequest` (#2), `AssignCredentialsRequest` (#3), `RotateCredentialsRequest` (#4),
`ExtendCredentialLeaseRequest` (#5), `RevokeCredentialsRequest` (#4), `InterruptRequest` (#5),
`SignalDeadlineRequest` (#5), `ResumeRequest` (#15), `CheckpointBarrierRequest` (#4), and
`ReportUsageRequest` (#4). On all fifteen, `session_id` is field 1.

**(b′) Adapter-to-gateway removal set, one message.** `ReportSessionScrubRequest` (`:446-451`; `pod_id=1`,
`session_id=2`, `slot_id=3`, `outcome=4`). The argument here is about the reporting direction rather than
about request addressing: the adapter reports a released session's cleanup outcome, and under D5 `slot_id`
when populated is that same session's identifier. It is inert at the consumer today, because
`RecordSessionScrub` discards both the session and the slot argument
(`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:453`, whose signature is
`func (r *ScrubReporter) RecordSessionScrub(ctx context.Context, podID, _, _ string, leaked bool) error`),
so this removal is behavior-preserving on its own and does not depend on CODE-1 or D7.

Edit sites for (b′), none of which appear in the consumer list under (e): the producer at
`pkg/adapter/gatewaycontrol/scrubreport.go:80-88`, which drops the `slotID` parameter and the
`if slotID != ""` population block, with the doc comment at `:71-79` corrected; the adapter seam at
`pkg/adapter/sessionscrubreporter.go:23-27` and `:80`, with the callers at `pkg/adapter/slotsession.go:87`
and `pkg/adapter/session.go:274-282` whose comments describe the empty-slot base-mode emission D1 retires;
the consumer at `scrubreport_server.go:45` (the interface), `:93-94` (the read and its "empty value is the
single-session case" comment), and `:453`; the tests at
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go:74, 102, 124, 146, 151, 212`
and `pkg/adapter/gatewaycontrol/gatewaycontrol_test.go:46, 90`; and, in the specification, the §4.7
adapter-to-gateway RPC table and the §5.2 scrub-model text, plus the proto comment at `:440-445`. This is a cross-package API change:
`RecordSessionScrub` loses its slot parameter, which is behavior-preserving by inspection because the
implementation already discards it.

Owners for (b′): SCHEMA-1 removes the field and rewrites the proto comment, CODE-1 stages the producer, the
adapter seam, the consumer, and the `RecordSessionScrub` signature change under its explicit (b′) clause,
SPEC-7 stages the §4.7 adapter-to-gateway RPC table and the §5.2 scrub-model text, and §8 inventories the
two test files under the compile-only edits.

**(c) Replacement set, one message.** `CheckpointStart` (`:1151-1171`) carries `SlotId slot_id = 6` and no
session field, and neither its envelope `CheckpointRequest` (`:1131-1145`) nor the RPC signature
(`rpc Checkpoint(stream CheckpointRequest)`, `:131`) supplies one. Its `slot_id` is the checkpoint stream's
sole per-session address. Add `SessionId session_id` at a fresh field number, populate it from the binding
at `pkg/gateway/checkpoint/checkpointer/checkpointer.go:526-545`, switch the adapter's root resolution
(`pkg/adapter/checkpoint.go:100`, `checkpointRootsForSlot`) and op-lock key (`:112`) onto it, and only then
remove `slot_id = 6`. Behavior is unchanged under D5.

Preserve the `!ok || sess == ""` disjunction at `pkg/adapter/slot.go:160` verbatim in meaning. Its comment
states the fail-closed rule, that a slot with no registry entry or no bound session is rejected so a
checkpoint never captures an empty or nonexistent subtree for an unassigned slot. The re-point must not
fold the bound test into the lookup test. Two other surfaces move with this change:
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:143, 153-156`, and the §10.1.7
checkpoint-stream specification text.

**(d) Conditional set, one message.** `ShutdownRequest` (`:1548-1572`) is not a duplicate. `slot_id`
presence is the only wire-level discriminator between a per-slot teardown
(`pkg/gateway/podlifecycle/podsession/slotbinder.go:538`) and a whole-pod shutdown or recycle scrub
(`binder.go:1945`, `slotbinder.go:570`); both populate `session_id`, and `pkg/adapter/session.go:225-245`
branches on that presence. Removing it is conditional on the `ShutdownSlot`/`ShutdownPod` split in §4.1
landing first. Absent the split, `ShutdownRequest` keeps `slot_id`, and the split is a precondition this
proposal carries forward rather than an independent option.

**(e) Mechanic, edit surface, and what the compiler does not catch.** The only mechanical part is the
reserved markers: each removal adds `reserved <n>;` and `reserved "slot_id";` with a comment naming why the
field went, matching the file's own convention at `StartSessionRequest` (`:888-889`) and `ResumeRequest`
(`:1288-1289`). The repository's no-backward-compatibility rule
(`.claude/rules/code-best-practices.md:62`) covers shims, dual modes, legacy flags, and migration paths. It
does not license recycling a field number or name on a schema that runtime authors and the generated
external-adapter compliance suite compile against (`spec/28_communication-channels.md:1731`,
`spec/24_lenny-ctl-command-reference.md:114`).

The edit surface is the non-test, non-generated Go files listed below, the four further files (b′) names,
the two CODE-1 edits at `pkg/adapter/slot.go` and `pkg/gateway/session/executor/pod.go`, the test files §8
inventories, and the regenerated `pkg/proto/adapter/v1/lenny-adapter.pb.go`. Thirty-four Go files match
`adapterv1.SlotId|GetSlotId()|SlotId:` in total.

- Consumers, adapter-side reads re-pointed onto `session_id`: `pkg/adapter/credentials.go:74, :116, :158,
  :331` (assign, rotate, extend, revoke); `pkg/adapter/session.go:103, :185, :225` (start, send-message,
  shutdown); `pkg/adapter/staging.go:77, :180, :338` (prepare, finalize, run-setup);
  `pkg/adapter/attach.go:40`; and `pkg/adapter/checkpoint.go:100, :112`. Eleven of these are
  `s.useSlot(slotID)` presence branches (`pkg/adapter/slot.go:43-45`); the checkpoint pair resolves roots
  and the op-lock key directly.
- Producers, gateway-side writes deleted rather than re-pointed because `session_id` is already populated
  beside each: `pkg/gateway/runtime/adapterclient/client.go`, whose twelve `SlotId` references are the sole
  population path for the removal set (the presence conditionals at `:147, :205, :289, :364, :399, :428,
  :845, :861`, the assignments they guard, `:900`, and the `slot *adapterv1.SlotId` parameter of
  `sendUpload` at `:308`); and `pkg/gateway/checkpoint/checkpointer/checkpointer.go`'s `slotIDField` helper
  at `:586-600`, which goes dead once (c) removes `CheckpointStart.slot_id`.

Removing the field deletes both the generated `GetSlotId()` accessor and the `SlotId` struct member, so
consumer reads and producer assignments alike fail to build, and the interface arity change under (b′) is
caught in every implementor and test fake. That safety net does not cover the pod-global session-check
sites, which never referenced the removed field, compile unchanged, and regress only at runtime. They are
covered by §4.11 and CODE-1 rather than by this list, and the hazard of an incomplete edit must not be
stated as "a build failure rather than a silent regression" without that carve-out.

**Precondition.** The fifteen removals in (b) are atomic with CODE-1, which deletes `useSlot` and reduces
`workspaceRootForSlot`'s pod-global fallback to the unknown-slot error branch, with D7, which makes the
per-slot layout universal, and with §4.11. The re-point is behavior-preserving only once the presence
branch is gone; re-pointing onto `session_id` while `useSlot` survives would flip single-session pods onto
the per-slot layout.

**(f) Revert disclosure.** Five of the removals, `InterruptRequest`, `SignalDeadlineRequest`,
`ResumeRequest`, `CheckpointBarrierRequest`, and `ReportUsageRequest`, revert commit 01d19af0 "Address the
per-slot adapter requests to a slot" (2026-08-15, this branch, proposal 0064). That commit's stated
rationale was that "a request named the pod alone and could not say which slot it acted on". Each of the
five already carried `session_id` as field 1 before the commit, so the request could always name the
session, and under D5 that is the same as naming the slot. The commit's premise held only under the
two-namespace reading D2 retires. The removal retires
`tests/tier3_contract/adapter_slot_identity/slot_identity_wire_test.go`, which is replaced by a test
pinning that `session_id` is the sole per-session discriminator on the gRPC leg. The two `tests/spec-map.json`
rows the commit added are dropped, the exact-count assertion in
`tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` is restored to its pre-01d19af0 form,
and §7 records the amendment to 0064's deliverable the way D14 handles 0072.

**(g) The wrapper type's fate.** `SlotId` (`schemas/lenny-adapter.proto:587`) exists only to wrap the field
being removed. A repo-wide scan of the proto surface finds eighteen users, all of them `SlotId slot_id`
fields (`:449, 684, 716, 837, 917, 948, 967, 999, 1018, 1039, 1057, 1091, 1170, 1275, 1361, 1444, 1539,
1556`), and no other reference. Sets (b), (b′), and (c) remove seventeen. The type's survival is therefore
conditional on the same precondition as (d). If the `ShutdownSlot`/`ShutdownPod` split lands,
`ShutdownRequest.slot_id = 4` (`:1556`) goes with it and `SlotId` is deleted along with its doc comment;
this is the base case. If the split does not land, `SlotId` is retained with exactly one user and its doc
comment is rewritten to state that the field is the per-slot teardown discriminator on `ShutdownRequest`,
dropping the "Empty when `maxConcurrentSessions: 1`" condition at `:584-586` that D1 retires. Deleting an
unused message type is a source-level change to a published artifact that runtime authors compile against
and that the generated compliance suite reads, so it is announced in the schema deliverable. It is not a
wire change and does not engage (e)'s refusal to recycle field numbers and names.

### 4.6 The JSONL leg

Two changes land on this leg. The first is forced by D1 and the second is separably approvable.

#### 4.6.1 Population and resolution, forced by D1

D1 puts a slot on every pod, and the adapter's runtime-facing behavior is keyed on the identifier's
presence alone (`pkg/adapter/slot.go:43-45`; the stamp at `pkg/adapter/attach.go:258-267` reached from
`:54, :185, :246`; the demultiplexer at `:70-73`). Once every session holds a slot, a single-session pod's
runtime receives a stamped frame, which `spec/15_external-api-surface.md:1593` forbids today and
`tests/tier10_conformance/concurrent_slot_conformance_test.go:212-222` asserts against. D7 independently
obliges that runtime to derive `/workspace/slots/{sessionId}/current/` (`spec/06:392`, `spec/05:513`,
`cmd/runtimes/echo-concurrent/slot.go:117-127`). The JSONL population change, the `spec/15:1593` rewrite,
the `echo-concurrent` rewrite, and the tier-10 rewrite therefore belong to D1 and D7. They do not lapse if
the rename in §4.6.2 is cut.

The rule, scoped by frame type:

- **The session-scoped set** is the five frames the schema tags today: `messageEnvelope`, `tool_call`,
  `tool_result`, `response`, and `set_tracing_context` (`schemas/lenny-adapter-jsonl.schema.json:58, 139,
  161, 190, 222`). Both the population rule and the rejection rule apply to these frames only.
- **Adapter to runtime.** The adapter populates the per-session identifier on every frame in that set, on
  every pod, regardless of `sessionPolicy.maxConcurrentSessions`. The conditional descriptions at the five
  schema sites are dropped, and `spec/28_communication-channels.md:547-551` and `:566-571` are restated
  unconditionally.
- **Runtime to adapter.** For a frame in that set, an absent identifier on a pod holding exactly one slot
  resolves to the stream's own binding. On a pod holding more than one slot it is rejected, counted, and
  logged, and is never relayed to any stream. This is not the conditional D1 retires. Absence never selects
  a scope: on the single-slot pod it resolves to the binding the receiving stream already holds, which is
  the only session the frame could be addressed to, and on every other pod it is an error rather than a
  fallback. The retired rule went the other way, withholding the identifier on the outbound leg so that its
  absence carried the pod-global meaning §1.2 records. The tolerance is a leniency toward a runtime that
  emits nothing, and the adapter reads its own registry to decide, which no runtime has to observe. This
  rule is adapter-side behavior in the demultiplexer and
  the tracing-frame path, so CODE-6 stages it with the rest of `demuxSlotOutput` and
  `pkg/adapter/tracingcontext.go`, SPEC-1 and §4.8's §28.5.3 rewrite state it, and the tier-1 and tier-3
  cases in §8 pin both branches of it.
- **Protocol-level frames.** `heartbeat` and `heartbeat_ack` (schema `:87-106`) carry no per-session
  identifier by design and are outside the addressing rule. The untagged pass-through in `demuxSlotOutput`
  (`pkg/adapter/attach.go:288-295`) is retained for them. The adapter runs one heartbeat monitor per Attach
  stream (`attach.go:78`, `pkg/adapter/heartbeat.go:148-157`) against a single pod-global runtime that
  answers unstamped (`cmd/runtimes/echo-concurrent/slot.go:172-187`), and a monitor that misses its ack
  SIGTERMs the runtime (`pkg/adapter/heartbeat.go:159-168`). Closing that path would deadline out every
  concurrent-pod runtime.
- **The demux predicate narrows by type rather than reopening.** A frame passes through when its type is
  not in the session-scoped set; a frame whose type is in that set takes the resolve-or-reject rule above.
  The fan-out narrows for content frames and is preserved for protocol frames.
- **The runtime's obligation is per-frame rather than pod state.** The runtime echoes the identifier it was
  handed on the inbound frame. No runtime obligation is keyed to the pod's live slot count, which a runtime
  cannot observe, and none is keyed to a static manifest value either, for the reason §9 records under the
  manifest limit. The resolve-or-reject rule is the adapter's obligation alone, and the adapter does
  observe its own slot count.

**The Basic-level integration row takes the exception.**
`spec/15_external-api-surface.md:1783` states that a Basic-level runtime needs only `type`, `id`, and
`input` and may safely ignore all other envelope fields. The per-session identifier is excepted from that
permission: a Basic-level runtime echoes it on the frames it emits in response, on every pod, and the row
says so. The alternative, stating that a Basic-level runtime is not offered on a pod whose pool sets
`maxConcurrentSessions > 1`, is rejected because it reintroduces a concurrency condition into a
runtime-facing contract, which is what D1 retires. The obligation arises from this subsection's population
rule rather than from the rename, so it lands whether or not §4.6.2 does; under §4.6.2 the field the row
names is `sessionId`, and without it the field is `slotId`. SPEC-7 stages the row.

This half is forced rather than optional. `tracingFrameAddressesStream` rejects on `frameSlot != slotID`
before touching the registry (`pkg/adapter/tracingcontext.go:49-59`) and `handleSetTracingContext` then
drops the frame (`:81-88`). Once every session holds a slot, every untagged `set_tracing_context` frame
from a Basic runtime is dropped, and that JSONL frame is the only tracing path such a runtime has, because
the SDK helpers go over MCP (`sdks/runtime/go/runtime/mcp.go:248-253`). Without this rule, D1 silently
disables it.

#### 4.6.2 The rename, separately approvable

The frame field `slotId` is renamed `sessionId` on the five session-scoped frames. It is D2's realization
on this leg, so the only field addressing the receiving session is named for what it carries, and it
changes no behavior because under D5 the field already carries the session identifier.

The premise is that the leg carries no field *addressing the receiving session* today, rather than that it
carries no session field at all. One session identifier is already present, on one frame: the `message`
envelope's required `from` block, whose `id` is `sess_{session_id}` when `kind` is `agent` and which names
the sending session (`schemas/lenny-adapter-jsonl.schema.json:25, :70-84`; `spec/15:1583`). After the
rename, `sessionId` is the only field addressing the receiving session, and the `message` frame is the only
frame carrying both.

Disambiguation is a deliverable rather than a note. On the `message` frame, the schema description for
`sessionId` states that it names the session the frame is addressed to, and the description for `from.id`
states that it names the sending session. The same distinction is made in the §28.5.3 text and in the
`spec/15` envelope field list, which today describes `slotId` at `spec/15:1590` as "the concurrent slot
this message is addressed to" with no reference to `from`. The four frames with no `from` block need no
such note. A contract-tier case pins that a `message` frame whose `from.id` names a different session than
`sessionId` is delivered to the session named by `sessionId`. `toSessionId` is rejected as a name: it
breaks the naming uniformity with the gRPC leg's `session_id` that D2 and D3 rest on, and prices a
one-frame ambiguity into every frame's name.

**The integration-level tension is settled in §4.6.1.** The Basic-level row at
`spec/15_external-api-surface.md:1783` excepts the per-session identifier from the fields a Basic-level
runtime may ignore, for the reason §4.6.1 states. The rename changes only the name that row carries, from
`slotId` to `sessionId`. SPEC-7 stages the row and SPEC-1 carries no clause about it.

**Blast radius**, additional to what SPEC-6 and §4.8 already stage. Four categories.

(i) Mechanical renames of the wire key:

- `schemas/lenny-adapter-jsonl.schema.json:58, 139, 161, 190, 222`.
- Adapter frame helpers, which are named for the wire key and take the rename with it rather than keeping
  their current names: `pkg/adapter/slotframe.go:11-33` (`frameSlotID` and `stampSlotID` become
  `frameSessionID` and `stampSessionID`), `pkg/adapter/attach.go:72, 253-295` (`demuxSlotOutput` and
  `writeSlotEnvelope` become `demuxSessionOutput` and `writeSessionEnvelope`), and
  `pkg/adapter/slotframe_test.go`. §3 records these as the identifiers its `slotlayout` exclusion does not
  cover.
- Runtime SDK struct tags, field names, and frame keys, nine files: Go
  (`sdks/runtime/go/runtime/types.go:71, 298, 306, 320`; `runtime.go:452-484`; `tool.go:26, 51`), Python
  (`lenny_runtime/types.py:139, 165`; `tool.py:124-147`; `runtime.py:352, 367-368, 384-385`), and
  TypeScript (`src/types.ts:71`; `src/runtime.ts:314, 329, 344`; `src/tool.ts:20, 114, 132-133`).
- `tests/tier4_integration/concurrent_workspace_test.go:264-274`, the `slotResponse` decode struct whose
  `SlotID string` field carries a `json:"slotId"` tag and reads the outbound `response` frame. It decodes rather than
  compiling against a generated accessor, so it fails as a wrong-value assertion rather than as a build
  error.
- Runtime-author documentation field tables and prose: `docs/runtime-author-guide/lifecycle.md:397`,
  `docs/runtime-author-guide/platform-tools.md:428, :449`, `docs/getting-started/concepts.md:173`, and the
  `docs/reference/adapter-contract.md` field rows at `:156, :190, :281, :324` whose "Present only when
  maxConcurrentSessions > 1" conditions §4.6.1's population rule deletes.
- The two specification sentences stating the cwd derivation (`spec/06:392`, `spec/05:513`) and the
  restatement at `spec/29_communication-scenarios.md:1448`.

(ii) Judgement rewrites, where the change is the retirement of absence-as-address rather than a key rename:

- `cmd/runtimes/echo-concurrent`: `dispatch.go:17-35, 63, 93-94, 119-150` (the empty-string default worker
  and the no-slotId routing comments), `slot.go:39-60, 75-84, 117-133` (the `slotCwd` and `slotWriter`
  empty branches), `main.go:9-37` (the doc block stating the no-slotId path as the single-session
  contract), and `main_test.go`.
- The Python and TypeScript conditional emitters (`if env.slot_id` at `runtime.py:367, 384`;
  `...(env.slotId ? … : {})` at `runtime.ts:329, 344`), which become unconditional.
- `tests/tier10_conformance/concurrent_slot_conformance_test.go`, whose whole-pod-default premise
  disappears. §4.6.1 already forces this rewrite and the rename extends it.

(iii) Prose and diagnosis comments that name the JSONL field and go stale at the rename. None breaks the
build, so (e)'s compile-failure safety net does not apply and the list is exhaustive here:
`pkg/adapter/slot.go:19, 38-41, 73, 120, 139-146, 182`; `pkg/adapter/server.go:76-82, 363-365`;
`pkg/adapter/socketruntime.go:42-49, 225, 318, 336`; the test doc blocks and `// diagnosis:` comments at
`tests/tier4_integration/concurrent_workspace_test.go:16-40, 91-93, 117, 141, 205-213, 226-227, 247, 266,
301`, `tests/tier4_integration/concurrent_delegation_proxy_test.go:24, 163`,
`tests/tier5_e2e_kind/execution_modes_test.go:15, 75, 185, 224`, and
`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:481, 518-580`; and the deployment
manifests and install script at `tests/testinfra/kind/agent-workload.yaml:172-174`,
`tests/testinfra/kind/bootstrap-overlay.gen.yaml` (generated from `install.sh`, so regenerated rather than
hand-edited), `tests/testinfra/kind/install.sh:123, 729, 733, 857`, and
`tests/testinfra/k8s/agent-workload-load.yaml.tmpl:86`. This category is grounded in the requirement that a
comment state what the code does, and in this proposal's own standard that a staged deliverable name every
file it touches. It is not grounded in `.claude/rules/channel-naming.md`, whose N3 prohibition covers
`lifecycle channel` and `control channel` and whose N4 governs `LNK-`/`CH-`/`REG-` identifiers, neither of
which covers a frame field.

(iv) The path-template placeholder sites belong to D7's placeholder rename rather than to this one:
`pkg/adapter/slotcreds.go:18`, `pkg/adapter/server.go:76, 82`, `pkg/adapter/slot.go:120, 139-146`,
`tests/tier9_security/credential_file_contract_test.go:67`,
`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:192`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go:14, 50, 126, 361, 385, 392`,
`tests/tier4_integration/concurrent_workspace_test.go:20, 39, 160-163`, and
`tests/tier5_e2e_kind/execution_modes_test.go:38, 174-182, 254`.

`cmd/runtimes/echo-embedded/main.go:74` carries no `slotId` and belongs to D7's uniform-layout radius rather
than to this leg.

### 4.7 The filesystem placeholder

D7 keeps `/workspace/slots/{...}/current` and the sibling trees, and renames the documented placeholder
token from `{slotId}` to `{sessionId}`. The rule is stated once at the §6.4 layout block: the directory
names the pod-side resource class, the leaf names its occupant, and under D5 that occupant's identifier is
the session identifier. The defense of the layout is the §5 measurement against
`pkg/controller/sandbox/podspec/podspec.go:926-961` rather than any claim that the runtime is unaware of
the path, which D2 refuses.

Sites, beyond the three cwd-derivation sentences §4.6.2 lists:

- `spec/06_warm-pod-model.md:30` (`/run/lenny/slots/{slotId}/credentials.json`) and the §6.4 block at
  `:373-393`, covering the layout listing (`/workspace/slots/{slotId}/`, `/sessions/{slotId}/`,
  `/artifacts/{slotId}/`) and the adapter and gateway responsibility bullets.
- `spec/05_runtime-registry-and-pool-model.md:513` and the acknowledgment text at `:517`.
- `spec/13_security-model.md:30`.
- `spec/14_workspace-plan-schema.md:5`.
- The remaining occurrences under `spec/04`, `spec/29`, `docs/runtime-author-guide/lifecycle.md`,
  `docs/getting-started/concepts.md`, and `schemas/lenny-adapter.proto`. Twenty-five sites in total under
  `spec/`, `docs/`, and `schemas/`, enumerated in the deliverable.
- `pkg/adapter/slotlayout/slotlayout.go:11-18, :44-48, :74-76, :95-96`, whose package doc is the stated
  single source of truth the adapter and the runtime agree on.
- The path-template prose sites routed here from §4.6.2(iv).

### 4.8 The §28.5.3 addressing rule

`spec/28_communication-channels.md:809-829` states the rule that resolves an inbound frame against an
Attach stream, and `:548` states it per message. §4.6.1 changes how it resolves, §4.6.2 renames the field
it resolves on, and D1 deletes its slotless case, so the rule is rewritten rather than left standing.

The rewritten rule states first that it governs the session-scoped frame types only, the five frames named
in §4.6.1, and does not reach `heartbeat` or `heartbeat_ack`.

Condition 1 becomes exact equality between the frame's per-session identifier and the stream's session. The
"absent or empty identifier counting as the empty string" branch is replaced by §4.6.1's resolution rule:
on a pod holding one slot, resolve to the stream's binding; on a pod holding more than one, reject and
never relay.

Condition 2 is stated as: the registry holds the address, and that entry carries a bound session. Both
reasons it may reject are given, because `slotStateLocked` returns `!ok` for a released or never-registered
binding, and a registered-but-unbound entry (the pre-`StartSession` window §4.5 and D2 describe) carries no
session to confirm. The sentence that condition 2 may only reject is kept.

What is and is not reachable today is recorded, so the text is not read as describing a live rejection
path. Inside `tracingFrameAddressesStream` the bound conjunct is currently unreachable as a rejection,
because condition 1 rejects on `frameSlot != slotID` before the registry is read
(`pkg/adapter/tracingcontext.go:49-52`) and `checkSlotSession` at Attach bind time
(`pkg/adapter/attach.go:41-44`) forbids a stream on an unbound slot, with no bound-to-unbound transition
possible. The conjunct is kept as the specification's statement of the invariant and as defence in depth;
the `!ok` term carries the non-vacuity argument at this site.

The "when the stream carries no `slotId`, the pod holds no registered slot" branch is deleted as
unconstructible once every session holds a slot. What is given up is a frame addressed to an identifier
since rebound to another session, which global uniqueness of session identifiers makes unconstructible.

Code and schema sites: `pkg/adapter/tracingcontext.go:49-59` deletes the `slotID == ""` branch and renames
its parameters accordingly; `docs/reference/adapter-contract.md:328` and the frame table above it;
`schemas/lenny-adapter-jsonl.schema.json:58-61`; and `schemas/examples/jsonl.set_tracing_context.json:8`.

### 4.9 The persistence layer

Under D10 the persisted `slot_id` carries sentinel-or-copy and never information beyond `session_id`. The
column is dropped rather than converged and backfilled. MIG-1 stages the schema edits below, CODE-5 stages
the sentinel's removal from Go, and SPEC-9 stages the `spec/10` edits.

- `session_checkpoints` (migration 0112): drop `slot_id`; re-point the rotation index from
  `(tenant_id, session_id, slot_id, created_at DESC)` to `(tenant_id, session_id, created_at DESC)`.
- `checkpoint_manifest` (migration 0178): drop `slot_id`; re-key `partial_manifest_active_uniq` on
  `(session_id) WHERE partial = TRUE AND deleted_at IS NULL`; re-key the coordination-generation index on
  `(tenant_id, session_id, coordination_generation DESC)`.
- `session_partial_checkpoint_manifest`: no edit. The table was dropped by migration 0178
  (`migrations/0178_checkpoint_manifest.up.sql:25-28`).
- `spec/10_gateway-internals.md`, staged by SPEC-9: the sentinel sentence at `:157`, the supersede-on-write rule, the
  reassembly predicate, and the §10.1.8 intent-row text drop `slot_id` from the scoping key and state
  `session_id` alone. The rationale sentence at `spec/10:167`, "the former pair is the correct invariant
  because at most one partial-upload attempt per slot can be in-flight at any wall-clock instant", is
  rewritten to state the invariant on `session_id` rather than left standing beside a single-column index.

A retained duplicate column in gateway-owned tables is the pressure that would put the slot identifier back
on the wire, so the split D2 draws is stable only with this change.

### 4.10 Identifier order

D12 keeps the lowest-identifier promotion rule as implemented and corrects how the specification describes
it. Identifier order is a lexicographic tie-break over opaque identifiers, chosen so that the promotion
pick is a pure function of the pending set rather than of Go's map iteration order, which gives a
reproducible and testable order (`lowestKey`, `pkg/adapter/oplock.go:178-189`; pinned at
`pkg/adapter/oplock_test.go:130-171`). The order carries no ordinal, arrival-order, or fairness meaning.

Three things the specification must not say about it. It must not claim starvation-freedom or any other
liveness property, for the reason §3 records. It must not restate the rule as FIFO or arrival order, which
would change behavior rather than its description. And it must not attribute the determinism to replica
agreement: `opLock` is per-adapter-process state inside one agent pod.

Sites: `spec/04_system-components.md:691` ("promotes the pending checkpoints in slot-ID order"),
`spec/05_runtime-registry-and-pool-model.md:542` (eviction upload serialization),
`spec/29_communication-scenarios.md:1481`, and the comments at `pkg/adapter/oplock.go:48, :73, :160` and
`pkg/adapter/checkpoint.go:107`.

### 4.11 One session check in the adapter

D13 replaces `checkSession` (`pkg/adapter/session.go:346-356`) and `checkSlotSession`
(`pkg/adapter/slotsession.go:116-128`) with a single `checkSessionBound(sessionID)` resolving through
`s.slots`. Under D5 the two are the same check, because the lookup key and the compared value are the same
string. `Server.sessionID` is retired with `useSlot`, and `claimSession` and `claimSessionForConfigure`
become slot-registry claims.

Call sites split by what the compiler catches.

- Uncaught, and the reason this deliverable exists: `pkg/adapter/lifecycle.go:30` (Interrupt), `:77`
  (SignalDeadline), `pkg/adapter/coordination.go:89` (CoordinatorFence), `:216` (CheckpointBarrier), and
  `pkg/adapter/usage.go:264` (ReportUsage). These five call `checkSession` with no slot branch and read only
  `session_id`. They are the `checkSession` callers that never resolve a slot, which is a different set from
  §4.5(f)'s five messages whose `slot_id` arrives and is unread: `CoordinatorFenceRequest` carries no
  `slot_id` at all (D6), and `ResumeRequest`'s unread `slot_id` (§1.2) is handled by CODE-2 rather than here.
  All five compile unchanged after the proto field is removed and would fail at runtime for every session on
  every pod.
- Branch collapses: `pkg/adapter/session.go:186/198` (SendMessage), `:246` (Shutdown), and
  `pkg/adapter/attach.go:42/45` (Attach). The slot half already calls `checkSlotSession`; the merged path
  keeps that check rather than the pod-global one.

Shutdown's recycle-scrub guard at `pkg/adapter/session.go:242-245` fires when `s.currentSession() == ""`,
which becomes universally true once no path writes `s.sessionID`. It is re-keyed on the slot registry's
occupancy.

§1.2's coordinator-hold finding is the special case of this, and CODE-3 fixes only `holdstate.go`'s arming.
This clause generalizes that fix to the validation path.

## 5. The rejected alternatives and the measurements behind D7 and D8

D7 and D8 each chose one arrangement over others that were measured against the tree rather than argued
from taste. This section records those measurements and the arrangements they rejected. The decisions
themselves are D7 and D8 in §2, and SPEC-3 stages them.

### Where the identifier sits in the path

D7 keeps the identifier under each tree. The arrangement that puts it above them was measured against the
pod builder rather than argued from taste, and the measurements are these.

The agent pod's volumes are built in Go at `pkg/controller/sandbox/podspec/podspec.go` rather than in the
chart, and `podVolumes()` (`:926-961`) declares each tree as its own volume: `/workspace` and `/artifacts`
are disk-backed `emptyDir`s (`:933-934`), `/sessions` is `Medium: Memory` with a 256Mi limit (`:950-952`),
`/tmp` likewise (`:947-949`), and `/workspace/shared` is a separate volume mounted over `/workspace`
(`:939`) specifically so that the runtime container's `readOnly` mount (`:557`) is enforced by the kernel
rather than by file mode, which the comment at `:124-128` states. Both containers run with
`ReadOnlyRootFilesystem: true` (`:1276`), so every writable path is a declared mount.

A single `/slots` volume holding each slot's workspace, sessions, and artifacts is therefore not a rename.
It withdraws the memory medium from `/sessions`, which is where §6.4's "data is guaranteed gone" statement
rests, and it withdraws the kernel-enforced immutability of `/workspace/shared`, which cannot move under a
slot root in any case: it is pod-wide, populated once before any slot exists, and one read-only mount is
what makes it immutable. Credentials are in the same position, carrying both a tmpfs and the
`lenny-cred-readers` group boundary (`podspec.go:59-67, 940-942, 1000`), so they would stay outside the
slot root and break the premise that one root holds everything a slot owns. Keeping every medium and
mounting the four trees where they are now returns the identifier to a position under each one, which is
D7.

Dynamic identity is not what blocks the arrangement, and the distinction matters for anyone revisiting
this. Slot trees are directories the adapter creates inside already-mounted volumes
(`pkg/adapter/slotlayout/tree.go:22-45`), so an identifier unknown at pod-render time is no obstacle to
them. It would only block a design that needed a distinct *mount* per slot, which neither arrangement does.

Dropping the `slots/` segment instead, giving `/workspace/{sessionId}/current`, saves one path element and
introduces a namespace hazard: `shared` is a real mount point inside `/workspace` (`podspec.go:123`),
`.staging` is a pod-global directory there (`:152`), and `current` and `staging` are created there at warm
time (`pkg/adapter/warmlayout.go:96-113`). `slotlayout.ValidateSlotID` (`slotlayout.go:114-131`) rejects
separators and dot segments and nothing else, so no rule prevents an identifier colliding with a mount
point. The same constant builds the credential path (`slotlayout.go:64`, used at `:151`), where dropping
the segment collides with `credentials.json`.

### The warm-time ordering

D8 resolves the warm-pod ordering problem by creating slot trees at assignment. The reasoning is that
concurrent pods already do exactly this, so the alternative is not a simpler design but a second one. The
two alternatives, and why they are not staged:

A fixed slot identity known at warm time reintroduces the special case through the back door, since the
warm-time identity cannot be the session identifier and every pod would hold one slot whose identifier is
allocated differently from every other slot's.

Pre-allocating `maxConcurrentSessions` identifiers at pod creation restores the ordinal naming D5 rejects,
and gives a warm pod N empty trees to scrub on recycle rather than none.

What D8 costs is that `spec/06:11-12`'s warm-pod checklist stops asserting that `/workspace/current` exists
and is empty on a warm pod, and asserts `/workspace/slots/` instead. If that assertion is relied upon
anywhere this proposal has not found, the decision needs revisiting before SPEC-3 lands.

## 6. Proposed changes

### SCHEMA-1. Remove the duplicate address from the gRPC leg

`schemas/lenny-adapter.proto` takes the message-by-message treatment in §4.5. No field is added to
`ExportPathsRequest` or `ConfigureWorkspaceRequest`. The fifteen gateway-to-adapter `slot_id` fields in
§4.5(b) and the one adapter-to-gateway field in §4.5(b′) are removed, each with `reserved <n>;` and
`reserved "slot_id";` and a comment naming why. `CheckpointStart` gains `SessionId session_id` at a fresh
field number before its `slot_id = 6` is removed, per §4.5(c). `ShutdownRequest` is split into
`ShutdownSlot` and `ShutdownPod` per §4.1, and its `slot_id` goes with the split.

Under the base case in §4.5(g) the `SlotId` wrapper message (`:587`) and its doc comment (`:584-586`) are
deleted, along with the per-field comments that referenced them. Under the alternative, where the shutdown
split does not land, `SlotId` is retained with one user and its doc comment is rewritten to state that the
field is the per-slot teardown discriminator on `ShutdownRequest`, with the `maxConcurrentSessions: 1`
condition dropped.

Generated code under `pkg/proto/adapter/v1/` is regenerated rather than hand-edited.

### SCHEMA-2. State the JSONL population rule and rename the frame field

`schemas/lenny-adapter-jsonl.schema.json` drops the concurrency conditions at `:58, 139, 161, 190, 222` and
states §4.6.1's population rule on all five session-scoped frames. The field is renamed `slotId` to
`sessionId` per §4.6.2, and the `message` frame's descriptions distinguish `sessionId` (the addressed
session) from `from.id` (the sending session). `heartbeat` and `heartbeat_ack` are stated as outside the
addressing rule.

`schemas/examples/jsonl.set_tracing_context.json:8` takes the rename and a session-identifier example value.

### SPEC-1. State the rule where the conditional stood

The presence conditions at `spec/15_external-api-surface.md:1479, 1480, 1490, 1491, 1593, 1616`,
`spec/28_communication-channels.md:547-551, 566-571, 604, 632, 665, 689, 1624`,
`spec/05_runtime-registry-and-pool-model.md:513`, and `spec/29_communication-scenarios.md:52, 435` are
replaced by §4.2's value rule and §4.6.1's population rule. The `spec/10_gateway-internals.md:157` sentinel
sentence is outside this deliverable: it states a persistence scoping key rather than a wire-addressing
condition, and SPEC-9 stages it with the rest of §4.9's specification edits.

The adapter populates the identifier on every session-scoped frame on every pod. A runtime-to-adapter frame
may omit it only on a pod holding one slot, where it resolves to the stream's binding. The field is renamed
per §4.6.2. The Basic-level row is stated by SPEC-7 rather than here.

### SPEC-2. Define a slot, in both senses

`spec/05_runtime-registry-and-pool-model.md` §5.2 gains the session-mode definition in §4.3, placed before
the first use of the term, and states separately where it defines service mode that a service-mode slot is
unnamed per-pod request capacity, per §1.8 and D5. The glossary (`docs/reference/glossary.md`) gains an
entry carrying both senses.

### SPEC-3. Collapse the two filesystem layouts and rename the placeholder

`spec/06_warm-pod-model.md` §6.4 states one layout, per D7. The base-layout block and the per-slot block
collapse into a single tree, and lines `:373` and `:395`, the "does not apply" and "applies when 1" pair,
are deleted with them. `:392`'s runtime obligation is stated unconditionally against
`/workspace/slots/{sessionId}/current`, and the sentence telling a runtime not to assume a global
`/workspace/current` "when `maxConcurrentSessions > 1`" loses its condition and becomes a statement that no
such path exists. `:11-12`'s warm-pod checklist asserts `/workspace/slots/` and `/workspace/staging` per D8
and stops asserting `/workspace/current`, which D9 retires.

The placeholder token is renamed `{slotId}` to `{sessionId}` at every site §4.7 lists, with the rule stated
once at the §6.4 layout block, and the reason the container directory is not renamed to `sessions` is
recorded there.

§6.4's "`/workspace/shared/` population and enforcement" paragraph states that the gateway populates the
directory during pod initialization. The adapter does, at warm time, before READY and before any slot
exists (`pkg/adapter/warmlayout.go:127-152`, from the `--shared-assets-dir` and `--shared-assets` flags
rendered at `pkg/controller/sandbox/podspec/podspec.go:816-821`). The paragraph is corrected to name the
adapter, and its "when `maxConcurrentSessions > 1`" qualifier is dropped, because the directory is mounted
and populated on every pod. This divergence predates this proposal; it is corrected here because the same
paragraph is being rewritten for the uniform layout and leaving one half stale would be worse than either
state.

The `/workspace/current` references at `spec/04:263`, `spec/29:835`, `spec/15:2268`, `spec/10:171`, and the
remaining sites across `spec/07`, `spec/08`, `spec/13`, `spec/14`, `spec/24`, `spec/26`, and `spec/28` are
qualified to the slot path, and the corresponding sites under `docs/getting-started/`,
`docs/runtime-author-guide/`, `docs/reference/adapter-contract.md`, and `docs/reference/glossary.md` take
the same qualification. `spec/10:171`'s staging directory and atomic rename are stated per slot root.

### SPEC-4. Unify the credential path

`spec/06_warm-pod-model.md:26` and `:28` merge into one per-slot credential-lease paragraph.
`spec/13_security-model.md:26`'s fsGroup delivery paragraph and `spec/04` §4.7 item 4 name the per-slot
path. `spec/13:30`'s advice to deployers requiring strict lease isolation is restated: with §1.3 fixed the
isolation holds per slot, and the remaining reason to prefer one session per pod is the co-tenancy surface
in §4.4.

### SPEC-5. Rescope §29.10

`spec/29_communication-scenarios.md` §29.10 is split per §4.4. Its addressing mechanisms move to the owning
sections and its co-tenancy analysis stays, retitled to name the condition it actually depends on. The
three remaining unstated gaps stay with the co-tenancy half.

### SPEC-6. Correct the examples and the order rules

`spec/28_communication-channels.md:600`, `docs/reference/adapter-contract.md:316`, and
`schemas/examples/jsonl.set_tracing_context.json:8` use a session identifier rather than `"slot_01"`. At
`spec/15_external-api-surface.md:1569` the example field is renamed `sessionId` and carries a
session-identifier value, matching the schema rename SCHEMA-2 stages, the code rename CODE-6 stages, and
the envelope field description SPEC-7 states. The ordinal reasoning at
`spec/07_session-lifecycle.md:333` is replaced. The four `"slotId": null` examples at
`docs/reference/adapter-contract.md:140, 180, 232, 271`, which fail the published schema, are corrected.

The behavioral sentences keyed on identifier order take §4.10's justification: `spec/04:691`, `spec/05:542`,
`spec/29:1481`, and the comments at `pkg/adapter/oplock.go:48, :73, :160` and `pkg/adapter/checkpoint.go:107`.

### SPEC-7. Declare message scope and state the addressing rule

`spec/04` §4.1 gains the classification table in §4.1 of this proposal, under choice (a) as the base case.
`spec/28_communication-channels.md:809-829` and `:548` take the §28.5.3 rewrite in §4.8.
`spec/15_external-api-surface.md:1590`'s envelope field description distinguishes the addressed session
from `from.id`, and `spec/15:1783`'s Basic-level row is rewritten to except the per-session identifier from
the envelope fields a Basic-level runtime may ignore, per §4.6.1: such a runtime echoes the identifier on
the frames it emits in response, on every pod. That row is staged here whether or not §4.6.2 lands, because
§4.6.1's population rule is what obliges it; the field it names is `sessionId` under §4.6.2 and `slotId`
without it. `spec/15` states that
a client addresses a session and never a slot.

The adapter-to-gateway half of the classification lands here too, per §4.5(b′). `spec/04` §4.7's
adapter-to-gateway RPC table classifies `ReportSessionScrubRequest` as session-scoped and
`ReportPodScrubRequest` as pod-scoped, and drops the slot field from the `ReportSessionScrubRequest` row.
`spec/05` §5.2's scrub-model text states that a released session's cleanup outcome is reported against the
session identifier, with the slot identifier no longer named, per D5.

### SPEC-8. State the identity invariant and correct the assignment in the specification and in code

`spec/05` §5.2 states D5's invariant: a session-mode slot's identifier is the identifier of the session
bound to it, and the gateway mints both as one value at claim time.

The five sentences in §1.6 are corrected. `spec/05:513`, `spec/28:548`, `spec/28:569`, and `spec/29:1435`
name the gateway as the minter. `spec/06:391` is rewritten to say that the adapter creates the tree on
first reference to a gateway-minted identifier, which is what `pkg/adapter/slot.go:73-88` does, and the
implication that the adapter is the assigner is removed from the surrounding responsibility bullet.

The six code comments §1.6 records are corrected here as well, because the misattribution is one error with
a specification half and a code half and correcting only one half leaves the other asserting the opposite.
Two of them cite `spec/06:391` verbatim as their spec basis and take the rewritten sentence:
`pkg/adapter/slot.go:72-73` on `ensureSlotStateLocked` and `pkg/adapter/slotlayout/tree.go:20-21` on
`EnsureTree`. Four restate the attribution in their own words and are rewritten to name the gateway as the
minter and the adapter as the creator of the tree: `pkg/adapter/slotlayout/tree.go:11` ("the §6.4 adapter
responsibility requires on slot assignment"), `pkg/adapter/slotlayout/slotlayout.go:8` ("an isolated tree
the adapter creates on slot assignment"), `pkg/adapter/server.go:363` ("Populated when a slot bind assigns
a slot"), and `cmd/runtimes/echo-concurrent/main.go:9` ("The adapter assigns a slotId per slot"). None of
the six breaks the build when left stale, which is why they are staged by name rather than left to the
compiler. The `echo-concurrent` doc block is rewritten once, taking this correction together with the
§4.6.2(ii) judgement rewrite CODE-6 stages at the same lines.

The bare term "slot assignment" where the gateway is the subject (`pkg/gateway/podlifecycle/podclaim/*`,
`spec/05:367`, `spec/12:191-192`) is correct and is untouched.

### SPEC-9. Re-key the persistence scoping key on the session identifier

`spec/10_gateway-internals.md` takes §4.9's specification edits, which are the specification half of the
column drops MIG-1 stages and the sentinel removal CODE-5 stages. The sentinel sentence at `:157` is
deleted: under D5 and D10 there is no sentinel, and the scoping key is `session_id` alone. The
supersede-on-write rule and the reassembly predicate (`:153, 163, 171`) and the §10.1.8 intent rows drop
`slot_id` from the scoping key and state `session_id`. The rationale sentence at `:167`, "the former pair is
the correct invariant because at most one partial-upload attempt per slot can be in-flight at any
wall-clock instant", is rewritten to state the invariant on `session_id`, so the text does not stand beside
a single-column index asserting a two-column one.

`spec/10:171`'s staging directory and atomic rename are stated per slot root by SPEC-3 rather than here;
this deliverable takes only the scoping-key half of that line.

### CODE-1. One address, one session check, no presence branch, in both gRPC directions

`pkg/adapter/slot.go`: `useSlot` is deleted, every root-computing site resolves through `slotlayout.Resolve`,
and the pod-global fallback in `workspaceRootForSlot` (`:123-133`) is reduced to the unknown-slot error
branch. Each session-scoped handler validates through `slotlayout.ValidateSlotID` before resolving a root
and returns `InvalidArgument` on empty. The adapter-side consumers listed in §4.5(e) re-point onto
`session_id`.

`checkSession` and `checkSlotSession` collapse into `checkSessionBound(sessionID)` per §4.11.
`Server.sessionID` is retired, `claimSession` and `claimSessionForConfigure` become slot-registry claims,
and Shutdown's recycle-scrub guard is re-keyed on registry occupancy. The five uncaught call sites in §4.11
are edited explicitly rather than left to the compiler.

**The adapter-to-gateway leg, per §4.5(b′).** The scrub-report producer
(`pkg/adapter/gatewaycontrol/scrubreport.go:80-88`) drops its `slotID` parameter and the `if slotID != ""`
population block, with the doc comment at `:71-79` corrected. The adapter seam
(`pkg/adapter/sessionscrubreporter.go:23-27, :80`) drops the parameter with it, and its two callers
(`pkg/adapter/slotsession.go:87`, `pkg/adapter/session.go:274-282`) lose the comments describing the
empty-slot base-mode emission D1 retires. On the gateway side the `ScrubReporter` interface
(`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:45`), the read and its
"empty value is the single-session case" comment (`:93-94`), and the `RecordSessionScrub` signature (`:453`)
lose the slot argument. The signature change is behavior-preserving by inspection, because the
implementation already discards the argument, and the arity change is caught in every implementor and test
fake. This clause is independent of the rest of CODE-1: it does not depend on `useSlot`'s deletion or on
D7, so it may land on its own.

`pkg/gateway/runtime/adapterclient/client.go`: the paired `X`/`XSlot` methods collapse to one method each,
the nine `if slotID != ""` sites (`:147, 205, 289, 364, 399, 428, 845, 861, 900`) are removed, and
`sendUpload`'s `slot *adapterv1.SlotId` parameter (`:308`) goes with them.
`pkg/gateway/session/executor/pod.go:146-148`'s concurrency-conditioned check becomes an unconditional
non-empty check on the session identifier and `ErrSlotIDRequired` is renamed for what it now requires.

The first three parts of this deliverable, the adapter's root resolution, the session-check merge, and the
gateway producer removals, are atomic with each other, per §4.5's precondition. The (b′) clause is not:
it touches the adapter-to-gateway leg alone and carries no dependency on the other three.

### CODE-2. Wire the three silent defects that resolve a root

Three of the four §1.2 defects are a root resolved against the pod-global tree, and this deliverable
stages all three. The fourth, coordinator hold state, is a session read rather than a root resolution and
is CODE-3.

Restore takes the session identifier from `ResumeRequest` and resolves through `checkpointRootsForSlot`
(`pkg/adapter/resume.go:179`), which corrects the discard §1.2 records. `ExportPaths` resolves against the
slot root using `session_id` (`pkg/adapter/exportpaths.go:39, 48`), as does `ConfigureWorkspace`. The §7.3
step (d) guard compares against the slot root (`pkg/adapter/resume.go:61`), which makes the assertion
load-bearing for the first time.

The recorded workspace root moves with the layout. `sessions.workspace_root`
(`migrations/0089_sessions_workspace_root.up.sql:10`) stores the absolute path the adapter reported, the
gateway replays it as `ExpectedWorkspaceRoot` (`pkg/gateway/sessionserver/start.go:3917`), and the adapter
rejects a session whose stored root does not match the one it holds (`pkg/adapter/resume.go:61-67`). Under
the uniform layout a row written before the change records `/workspace/current` while the pod now reports a
slot path, so every such session fails to resume. Two constants carry the stale default and change with it:
`DefaultExpectedWorkspaceRoot` (`pkg/gateway/sessionserver/lifecycle.go:38`) and
`archive.DefaultWorkspaceRoot` (`pkg/upload/archive/archive.go:39`). The platform is pre-deployment and
carries no sessions written under the old layout, so the migration rewrites the column rather than teaching
the guard a second accepted value; a compatibility branch here would be a shim for a population that does
not exist. The stored checkpoint bytes need no migration: `ArchiveTree` writes entries under the relative
prefixes `workspace/` and `sessions/` (`pkg/adapter/workspace/tree.go:41-42, 52-55`), so a checkpoint tar is
layout-independent and replays into whatever root receives it.

`ArchivePolicy.workspace_root` is defaulted to the pod-global `/workspace/current` even on a concurrent
bind (`pkg/gateway/podlifecycle/podsession/slotbinder.go:270`), which is wrong today rather than only under
this proposal: a concurrent pod has no such path, and §1.2's own subject is a value that means one thing
and is populated as another. It is defaulted to the bind's slot root.

**The gateway half of the resume, handed here by proposal 0070.** The adapter changes above are half of a
resume: they let the adapter restore into the right tree once it is told which session. The gateway must
supply one. `Binder.Resume` returns a `BindResult` carrying neither `SlotID` nor `MaxConcurrentSessions`
(`pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`), because `connect` issues a whole-pod
`podclaim.ClaimRequest` that reserves no slot (`binder.go:1652-1670`,
`pkg/gateway/podlifecycle/podsession/podclaim/claimer.go:63-74`). Two consequences follow, and this
proposal owns both. A resumed session on a concurrent pool holds a whole-pod claim and no slot, so it has
no slot root to restore into whatever the adapter is told. And the §7.2 fail-closed gate
(`pkg/gateway/session/executor/pod.go:146`) evaluates `0 > 1` on that path and never fires, so the
unresolved-slot invariant it exists to enforce is unenforced exactly where a resumed session would breach
it.

The resume therefore reserves a slot as the start path does, through the same claim, and the returned
`BindResult` carries that slot together with the concurrency of the pod it holds, resolved from the
`PoolMatch` the caller already has at `pkg/gateway/sessionserver/start.go:3876` and normalized to a minimum
of 1. The gate then evaluates a true value for the first time on the resume path, and a checkpoint-restore
resume onto a concurrent-workspace pool restores into the slot it reserved.

Proposal 0070 §1.3 states this defect and hands it here rather than correcting it, because the correction
its own review arrived at was to refuse such a resume outright, with a client-visible
`422 CONCURRENT_POOL_RESUME_UNSUPPORTED` and the specification text to match, on the ground that no
slot-aware resume path exists. This proposal builds that path, so the refusal would be staged and deleted
within two proposals. Nothing of 0070's is duplicated here: it keeps the tracing-handler fix, which is
independent.

### CODE-3. Arm hold state per slot

`pkg/adapter/holdstate.go:91-96` arms from the slot registry rather than from the pod-global `s.sessionID`,
so a concurrent pod's slots each carry a hold. The dead pod-global read in `s.currentSession()` is removed
with `Server.sessionID` under CODE-1 rather than left to be reintroduced.

### CODE-4. Address credential operations by session

`pkg/gateway/runtime/adapterclient/client.go:234-241` and `:253-262` address rotation and lease extension by
the session identifier the request already carries, and the adapter's credential handlers resolve the
per-slot lease from it (`pkg/adapter/credentials.go:74, 116, 158, 331`). The revocation path gains its
gateway caller, or the adapter's `RevokeCredentials` is removed if review establishes the Token Service path
is the only intended one; §9 records this as open.

### CODE-5. Remove the sentinel

`partialmanifeststore.SlotDefault` (`:67-70`) and its four application sites (`:367-369`,
`pgstore/pgstore.go:157-159`, `pkg/gateway/sessionserver/derive.go:481`,
`pkg/gateway/checkpoint/checkpointer/checkpointer.go:500-503`) are removed, along with the ten-line
explanatory comment at `checkpointer.go:536-545` and the `slotIDField` helper at `:586-600`, which goes dead
once `CheckpointStart.slot_id` is removed.

### CODE-6. Rename the JSONL frame field and make population unconditional

The adapter frame helpers, the demultiplexer, the three runtime SDKs, the two reference runtimes, and the
prose and diagnosis comments listed in §4.6.2 take the rename and the unconditional population.
`pkg/adapter/tracingcontext.go:49-59` loses its `slotID == ""` branch per §4.8.

The inbound resolve-or-reject rule in §4.6.1 lands here as well. For a session-scoped frame carrying no
identifier, the adapter resolves the frame to the receiving stream's binding when the pod holds exactly one
slot, and rejects it when the pod holds more than one, incrementing a rejection counter, logging the frame
type and the pod's slot count, and relaying the frame to no stream. The slot count is read from the
adapter's own registry rather than from a manifest value or a request field, per §4.6.1's last bullet.
`demuxSlotOutput` (`pkg/adapter/attach.go:253-295`) keeps its pass-through for `heartbeat` and
`heartbeat_ack` and narrows by frame type for the five session-scoped frames.

`cmd/runtimes/echo-concurrent`
loses its empty-string default worker and its no-slotId routing paths, and `cmd/runtimes/echo-embedded`
takes D7's uniform layout.

### CODE-7. Reconcile the client SDKs and the client-facing surfaces

The `slotId` field is removed from the client-SDK message payloads (`sdks/client/go/lenny/types.go:189-190`,
`sdks/client/python/lenny/types.py:266, 279-280`, `sdks/client/typescript/src/types.ts:168`) and from the
references in their client docs (`go/lenny/client.go:265`, `typescript/src/client.ts:325`,
`python/lenny/client.py:326`), matching what the gateway accepts. The comment at
`pkg/gateway/sessionserver/messages.go:186-193` states that the field is not part of the client contract
rather than that a supplied value is ignored.

The tool-approval detail and SSE payload (`pkg/gateway/sessionserver/toolapproval.go:101-102, 216-221`) and
the `/start` 422 error body (`pkg/gateway/sessionserver/start.go:317`, sourced from
`podsession.SlotFailedError.SlotID` at `slotfailure.go:125-130`) each carry a duplicate of a session
identifier the enclosing object or route already carries. The `slotId` key is dropped where the session
identifier is already present, or the specification states that the value is the session identifier.

### MIG-1. Drop the persisted duplicate columns

A migration drops `session_checkpoints.slot_id` and `checkpoint_manifest.slot_id` and re-keys the three
indexes per §4.9. There is no backfill, because there is no value to preserve. The same migration rewrites
`sessions.workspace_root` to the slot path per CODE-2.

`tests/tier2_component/migrations/prod_columns_test.go`'s matrix keeps its 0112 entry with an empty column
list and a comment naming the drop migration, the way the file already handles 0040/`concurrency_style`
(`:60-66`) and 0022/`task_policy` (`:46-49`), because `scripts/lint-migrations.sh` requires every migration
to be referenced by number in a test (`:17-20`). A matrix entry for the new drop migration is added so
`TestProdMigrationsRollBackPerStep` exercises its `.down.sql`.

### REG-1. Retire the register rows and the gate machinery they drive

The five slot rows in `tests/claim-map.json` (`:77, 175, 289, 413`, spelled "X slot identifier field", and
`:313`, spelled `ResumeRequest.slot_id`) are retired outright with the fields SCHEMA-1 removes, as is the
`ABSENT` "Slot-qualified interrupt, deadline, usage, and barrier" row at `:436-442`, whose deferred
capability SCHEMA-1 removes rather than defers. The earlier draft staged these five rows as moving from
`UNWIRED` to `ABSENT`; that edit is dropped rather than amended, because all five fields exist
(`InterruptRequest :1091`, `SignalDeadlineRequest :1275`, `CheckpointBarrierRequest :1444`, `ResumeRequest
:1361`, `ReportUsageRequest :1539`) and the rows are correctly `UNWIRED` under §28.4's definition.

Three rows are added for the per-slot credential isolation §1.3 records as unwired, one for rotation, one
for lease extension, and one for revocation. They name that behavior rather than the `slot_id` fields
SCHEMA-1 deletes, so the register keeps a record of the gap after the fields are gone. Each is `ABSENT`
until CODE-4 lands its half, and moves to `WIRED` then. No other register row changes status under this
proposal.

Their removal leaves unused machinery in `tests/tier0_static/claim_register_proto_agreement_test.go`:
`slotIDField` (`:41`), `claimSlotField` (`:59`), and the `claimSlotField` branch of `namedField`
(`:157-159`) are deleted, the slot-spelling case at `:233-237` and the `InterruptRequest slot identifier
field` row in the acceptance fixture at `:297` are dropped, and the two synthetic protos (`:194-198`,
`:284-288`), which carry `SlotId slot_id = 5`, are updated to the post-removal message set. Nothing here
fails to compile or turns red on its own, because the synthetic protos are inlined, which is why the
cleanup is staged rather than left to the gate to surface. The `ResumeRequest.slot_id` row is matched by
`claimQualifiedField` (`:58`) and touches no slot-specific code. The `absenceAssertion` path (`:62`) and the
generation-fence half of the gate (`:139-149, :216-219, :251-254`) are untouched.

`scripts/seed-claim-register.py` stops inferring a status from a deferral step's scope note. A row naming a
field is `ABSENT` unless the field is present in the tree at seeding time, and the script reads the tree to
decide.

## 7. Amendments to other artifacts

Proposal 0072 drops §1.11 and REG-1 without replacement, per D14. The status correction they stage is not
made by either proposal: the rows are correctly `UNWIRED` today, and REG-1 here retires them outright when
SCHEMA-1 removes the fields they name. 0072 cites this proposal for the retirement. Its §1.3,
which stages the specification half of the restore defect, is superseded by SPEC-3 and CODE-2 and is
dropped with a pointer here; the rest of 0072 is unaffected.

Proposal 0064's deliverable is amended the same way. Commit 01d19af0 landed five of the `slot_id` fields
SCHEMA-1 removes; 0064 records that those five fields are retired here and why its premise did not survive
D2, per §4.5(f). The two `tests/spec-map.json` rows the commit added are dropped and the exact-count
assertion in `tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` is restored to its
pre-01d19af0 form.

`PROPOSAL-QUEUE.md`'s C-53 entry is updated to record that its scope lands here, per D15. `TEST-GAPS.md`'s
T-4.4.21 is closed by the tier-4 case in §8. The summary-counts line is not edited.

## 8. Testing

**Tier 0.** A gate reading the §4.1 classification table and the proto text together: every request message
in scope that the proto declares appears in the table, so a message added later fails the gate until it is
classified, and no table row names a message the proto does not declare. The gate's message parse is
service-aware, since the existing regexes cannot tell which service a message belongs to and would
otherwise fail on every `GatewayControl` request the table omits under choice (b).

The gate is a new tier-0 file rather than an added check inside
`tests/tier0_static/claim_register_proto_agreement_test.go`, whose remaining subject after REG-1 is the
generation fence and whose `// spec:` annotation and `// diagnosis:` comment (`:166-171, :188-191`) are
written for the register-to-proto agreement question. `protoFields`/`braceDelta` (`:68-99`) and
`protoMessageOpen`/`protoField` (`:52-55`) are extracted into a shared helper the two gates share rather
than copied.

The claim-register validator runs over the corrected rows.

A second tier-0 case pins REG-1's seeding change in `scripts/seed-claim-register.py`. A row naming a field
the tree holds seeds as its wired or unwired status, and a row naming a field the tree does not hold seeds
as `ABSENT` whatever a deferral step's scope note says. The non-happy path is the one the old inference
produced: a row whose deferral note claims a scope the tree does not carry seeds `ABSENT` rather than
taking the note's status, so a removed field cannot be recorded as merely deferred.

**Tier 1, `pkg/adapter`.** A session-scoped request with an empty `session_id` is rejected with
`InvalidArgument` before a root is resolved, one case per handler. An unknown identifier returns
`FailedPrecondition`. `slotlayout.Resolve` is the only path builder reached. `checkSessionBound` rejects an
unbound slot and a released one, covering the five §4.11 handlers that today call `checkSession`.

**Tier 1, the inbound JSONL rule.** Both branches of §4.6.1's resolve-or-reject rule, over the five
session-scoped frame types. On a pod holding one slot, a frame carrying no identifier resolves to the
receiving stream's binding and is delivered. On a pod holding two slots, the same frame is rejected, the
rejection counter increments, and neither stream receives it, which is the case that would otherwise relay
one session's frame to another. A frame naming a slot the registry does not hold is rejected on both pod
sizes. `heartbeat` and `heartbeat_ack` carrying no identifier pass through on both pod sizes, so the
narrowed demux predicate is asserted not to close the path §4.6.1 keeps open.

**Tier 1, the coordinator hold.** CODE-3's arming. A coordinator fence on a pod holding two bound slots
arms a hold for each of them, which is the §1.2 defect where the pod-global read armed none. The
non-happy paths: a slot registered but not yet bound arms no hold, and a released slot's hold does not
survive the release, so the fix does not arm a hold that outlives its session.

**Tier 1, `pkg/gateway/checkpoint`.** No sentinel value reaches the store or the wire, and
`CheckpointStart` carries a populated `session_id`.

**Tier 3, `tests/tier3_contract`.** `session_id` is the sole per-session discriminator on the gRPC leg,
replacing `tests/tier3_contract/adapter_slot_identity/slot_identity_wire_test.go`. A capture on a pod with
one slot and a restore of that checkpoint resolve the same root, which is the general form of §1.2's first
defect. A `message` frame whose `from.id` names a different session than `sessionId` is delivered to the
session named by `sessionId`, per §4.6.2. On the JSONL leg, an outbound frame in the session-scoped set
carries `sessionId` on a pod of either concurrency, and an inbound frame that omits it is resolved against
the stream's binding on a pod holding one slot and rejected on a pod holding two, which pins §4.6.1's rule
at the wire rather than only inside the adapter.

**Tier 3, the client-facing surfaces.** CODE-7's removals, at the HTTP and SSE boundary. A message request
body carrying a `slotId` key is accepted and the key ignored, and the session the message reaches is the
one the route names, so the removal from the SDK payloads does not turn a stale client into a rejected one
and a supplied value addresses nothing. The tool-approval detail, the tool-approval SSE payload, and the
`/start` 422 error body carry no `slotId` key, and the session identifier the enclosing object or the route
already carries is unchanged. The client SDK message payload types serialize no `slotId` key, asserted per
SDK against the encoded request body.

**Tier 4.** The end-to-end case C-53 exists to enable: a concurrent pool captures a slot's checkpoint, loses
the pod, and resumes onto a replacement with the workspace intact. Closes T-4.4.21. A second case covers the
same round trip on a pool with `maxConcurrentSessions: 1`, which under this proposal exercises the identical
path.

**Tier 5.** `tests/tier5_e2e_kind/execution_modes_test.go` is a hand-rewrite, which is what §4.6.2(iii) and
(iv) stage at `:15, 75, 185, 224` and `:38, 174-182, 254`. Its `TestConcurrentSlotsIsolateWorkspaceDirectories`
premise is that `/workspace/current` is the whole-pod path a concurrent pod must never materialize into,
which D9 retires by removing the path from every pod, and its doc blocks and `// diagnosis:` comments
describe the per-slot tree as the concurrent-pool arrangement. The rewritten case asserts on a real agent
pod that each of two co-tenant sessions holds only its own `/workspace/slots/{sessionId}/current` content
and that `/workspace/current` does not exist, rather than that it exists and is empty. A second case runs
the same assertions on a pool with `maxConcurrentSessions: 1`, which is the non-happy path for D7 and D9 on
a real container boundary: the single-session pod that previously materialized into `/workspace/current`
now materializes into its slot tree, and a regression that reinstates the pod-global path fails there
rather than passing unnoticed on the pool that never used it.

**Tier 2, the layout.** A warm pod holds `/workspace/slots` and `/workspace/staging` and no
`/workspace/current`, on a pool of either concurrency, which pins D7 against the branch it removes and D9
against the path it retires. A slot's tree appears at assignment and is gone after cleanup, asserted over
all four trees the adapter creates, since `RemoveTree` removing three of four would leak state a later slot
could read. `tests/tier4_integration/concurrent_workspace_test.go:196-199`, which asserts nothing writes
`/workspace/current` on a concurrent pod, is restated over every pod rather than deleted: the property it
protects outlives the condition it was written under.

**Tier 2, the workspace root round trip.** A session started under the uniform layout records a slot root in
`sessions.workspace_root` and resumes against it. The negative case is the one that would have shipped
silently: a row holding the retired `/workspace/current` is rejected at resume by the §7.3 step (d) guard
rather than being accepted against a slot root, so the migration's completeness is asserted rather than
assumed. A workspace finalization followed by a resume covers the interaction between `promoteStaging`'s
rename of `current` and the recorded root.

**Tier 2, the resume bind.** `Binder.Resume` returns a bind carrying a slot and the pod's concurrency,
against the package's envtest harness rather than a fake client, since a completed `Resume` reaches the
kube-apiserver. The case that matters is the one the §7.2 gate was written for and has never been able to
run: a resumed session on a concurrent pool is driven through `PodExecutor.streamFor`, and the gate is shown
to admit a bind that resolved a slot and to refuse one that did not, on a path where it previously evaluated
a zero and never fired.

**Tier 2, the persistence layer.** `tests/tier2_component/rls/checkpoint_manifest_test.go:150-188` is a
hand-rewrite: the distinct-slot case at `:183-186` is deleted, the `// diagnosis:` comment and the
at-most-one-active-partial invariant are restated on `(session_id)` alone, and the slot argument is dropped
from the `insertManifest` helper. The new drop migration carries its own `migrations/` test asserting the
column is gone and the three re-keyed indexes exist, because
`migrations/0178_checkpoint_manifest_test.go:86-91` and `migrations/session_checkpoints_slot_id_test.go:24-27`
assert against the old migrations' `.up.sql` text, which the drop does not change, so they keep passing and
cover nothing.

**Tier 9.** A credential rotation on a concurrent pod rewrites only the rotating slot's credential file and
leaves the co-tenant slot's lease intact. This is §1.3, and it belongs in the security tier because the
defect is a cross-session credential read. `tests/tier9_security/tracing_context_session_isolation_test.go`
is a hand-rewrite: its slotless-stream cases at `:365-391` lose their premise, and its distinct-identifier
fixtures at `:242, 260` are unconstructible under D5.

**Tier 7a.** `tests/tier7a_load_local/tracing_context_release_race_test.go` is a hand-rewrite for the same
reason, and takes a second independent break from the gRPC removal.

**Tier 10.** `tests/tier10_conformance/concurrent_slot_conformance_test.go` is rewritten rather than
inverted: its whole-pod-default premise disappears under §4.6.1, its cwd assertions at `:225-238` are
restated for a pod holding one slot, and its "a single-session pod's response carries no identifier"
expectation at `:212-222` is retired. `tests/tier10_conformance/recycle_scrub_conformance_test.go:378-415` is
rewritten: it pairs session `slot-sess` with slot `slot-1`, which D5 makes unconstructible, and its
`reported slotId` assertion at `:414` disappears with the field. This is the in-process runtime-adapter
conformance battery, distinct from the generated external-adapter compliance suite §4.5(e) refuses to break.

**Tier 11.** The documented workspace path matches the specified one across `docs/` and `spec/`.
`tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go` is a hand-rewrite, because it
asserts the removed doc sentences verbatim at `:130-143`.

**Fixture rule, across tiers.** Once D5 is normative, a test fixture that pairs a session identifier with a
different slot identifier encodes an unconstructible state, so every such fixture is rewritten to a single
identifier. The corpus is eight files. Three are named nowhere else:
`tests/tier4_integration/concurrent_workspace_test.go:131-133` (a judgement rewrite, because `:186` asserts
on-disk path text and `:198-200` asserts that the whole-pod `/workspace/current` was never written, and the
asserted leaf becomes the session identifier under D7),
`tests/tier4_integration/concurrent_delegation_proxy_test.go:182-183` (a literal swap in the
`concurrentSlotSession` fixtures), and `tests/tier10_conformance/recycle_scrub_conformance_test.go:379-390,
414-415`. The other five are staged above:
`tests/tier10_conformance/concurrent_slot_conformance_test.go`, `cmd/runtimes/echo-concurrent/main_test.go`,
`tests/tier3_contract/adapter_jsonl/set_tracing_context_test.go`,
`tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go`, and
`tests/tier2_component/stores/partialmanifeststore_test.go` (whose entry extends from `:94`, which falls out
with CODE-5, to the distinct-identifier fixtures at `:168-176`).

**Compile-only edits from the gRPC removal.** These drop the field from a request literal or an assertion,
because `session_id` already carries the address:
`tests/tier3_contract/adapter_extendcredlease/extend_credential_lease_wire_test.go`,
`tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go`,
`tests/tier4_integration/concurrent_workspace_test.go`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go`, `pkg/adapter/attach_test.go`,
`pkg/adapter/sessionscrub_emit_test.go`, `pkg/adapter/tracingcontext_addressing_test.go:62`,
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go`, `pkg/gateway/session/executor/pod_test.go:200`,
`pkg/gateway/sessionserver/messages_component_test.go:67`,
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go:74, 102, 124, 146, 151, 212`
and `pkg/adapter/gatewaycontrol/gatewaycontrol_test.go:46, 90`, which are the two §4.5(b′) files and whose
edits the `RecordSessionScrub` arity change forces, and
`pkg/gateway/runtime/adapterclient/client_test.go:1363-1389`, whose `capturingAdapter` records the slot field
on SendMessage and Attach.

`tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` is not a compile-only edit. Its
exact-count assertion over `ReportUsageRequest`'s field set is restored to its pre-01d19af0 form per
§4.5(f) and §7, so the count is one lower and the test fails if the removal leaves the field behind.

**Fixture rewrites from the gRPC removal**, where the fixture pairs a slot value distinct from its session:
`pkg/adapter/slot_test.go` (nine sites, `"slot-a"`), `pkg/adapter/credexpiry_test.go:406, 571, 610, 632`,
and `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:48` (`"slot-3"`, conditional on
§4.5(d)).

**Deletions and retargeting tied to §4.5(c).** `pkg/gateway/checkpoint/checkpointer/slotid_test.go:88-93`,
whose whole subject is which requests carry the slot field on the wire and that the sentinel is kept off it;
`pkg/adapter/checkpoint_stream_test.go`; `tests/tier4_integration/checkpoint_driver_harness_test.go:100`
(`first.GetStart().GetSlotId()`); and
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:143, 153-156`.

**Whole-pod-scrub expectation.** `pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651` is restated
against `ShutdownPod` per §4.1, and
`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:480-481` takes the same treatment.
Both restatements are conditional on the split landing, per §4.5(d). If review declines it, the two tests
keep asserting on `ShutdownRequest` and its `slot_id` presence, and the fixture rewrite at
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:48` is the only edit they need.

## 9. Open questions, and what was considered and rejected

### Open

- **`RevokeCredentials`.** Whether the adapter's handler should gain a gateway caller or be removed. The
  Token Service path (`pkg/gateway/credassign/client.go:306`) may be the only intended revocation, in which
  case the adapter handler is dead code and CODE-4 shrinks.
- **D8.** §5, under "The warm-time ordering", states the reasoning and the one assertion it invalidates.
- **D7's path arrangement.** §5 records the measurements behind keeping the identifier under each tree
  rather than above them. The arrangement that reads better to a consumer is available at the price of one
  volume topology change and a §6.4 data-at-rest amendment, and review may take that trade.
- **D9.** Retiring `/workspace/current` leaves an operator reaching a workspace by its slot path. Whether
  the adapter should still create the empty directory, so that a `kubectl exec` into a warm pod finds a
  familiar path rather than nothing, is a question this proposal answers with no and review may answer
  differently.
- **§4.1's table coverage.** Choice (a) is the base case. Review may take (b) and accept the limit below.
- **§4.5(d) and (g).** The base case is that the `ShutdownSlot`/`ShutdownPod` split lands and the `SlotId`
  wrapper is deleted. Review may decline the split, in which case both survive with one user.

### Considered and rejected

These were raised during review and refused, with the reason. Convergence should not re-litigate them.

**Preserving decision 2 by withholding the identifier on a single-slot pod.** This is the only rule that
would keep the JSONL leg untouched under D1. It is a live-slot-count conditional on the adapter's outbound
frames, it restores absence as an address on that leg, and it is precisely the conditional this proposal
exists to retire. §4.6.1's inbound tolerance is a different rule: it never lets absence select a scope,
because on a single-slot pod the frame resolves to the binding the receiving stream already holds and on
every other pod it is rejected rather than resolved.

**Keeping `slot_id` as the mode discriminator.** The objection is that presence or absence of `slot_id` is
the only wire signal telling the adapter whether it is on a concurrent pod, since `session_id` is always
non-empty. The reading of the current code is right and the conclusion does not follow: the mode
discriminator is the thing D1 retires. After D1 there is no mode to discriminate, because every pod takes
the per-slot path.

**Keeping the field so a future non-identity mapping stays cheap.** The objection is that once no message
names a slot, the gateway has no way to address one, so a later non-identity mapping needs both the field
back and a transfer of allocation authority. The gateway is already the allocator and the occupancy
accountant (`slotclaimer.go:682` returning `SlotID` alongside `ActiveSlots`, and `ReleaseSlot` at `:739`),
so no authority transfers. Re-adding a field to a proto the platform owns end to end, pre-deployment, is a
cheap change; carrying a duplicate address on every message until then is not.

**Renaming `CoordinatorFenceRequest`'s field to restore a machine-derivable rule.** The proposal is that
naming that one field for its caller-identity role would restore the structural rule "a message whose
address field is `session_id` is session-scoped", checkable as a property rather than as agreement between
two hand-maintained lists. The evidence is right and the conclusion is not: the rule would then rest on
every future message author choosing the name correctly, which is the same hand-maintained agreement moved
into the field name, and the specification is already the source of truth for scope.

**A `google.protobuf.MessageOptions` extension carrying the classification.** It would work and would not
break external compilation, since descriptor.proto is a well-known type every toolchain bundles. It
requires importing descriptor.proto, adding an `extend` block to a file with no custom options today
(`schemas/lenny-adapter.proto:14-20`), regenerating `pkg/proto/adapter/v1`, and changing what runtime
authors and the generated compliance suite compile, for a classification the specification already owns.
This is not the same objection as §4.5(e)'s refusal to recycle field numbers, which is about silently
changing the meaning of bytes.

**`toSessionId` as the JSONL field name.** Rejected in §4.6.2: it breaks naming uniformity with the gRPC
leg's `session_id` and prices a one-frame ambiguity into every frame's name.

**Splitting the proposal.** Raised twice, on the ground that the combined change spans the proto and its
generated mirror, the specification sections §10 lists, the client SDKs, the runtime SDKs, the reference
runtimes under `cmd/runtimes/`, a migration, and the test tiers §8 stages. The size is right and no proposed
seam survives inspection: the gRPC removal, the JSONL population rule, the filesystem layout, the persistence
drop, and the adapter's session-validation unification are each a consequence of D1 and D5, and landing any
subset leaves the tree in a state where absence still has two meanings on some surface. The one genuine
seam is §4.6.2, the JSONL rename, which §4.6 states as separately approvable and §9's limits price.

**Reversing migration 0178's stated rationale for the column without answering it.** Answered rather than
refused: `spec/10:157`'s scoping key is already well-defined on `session_id` alone under D5, and §4.9 states
the invariant on `session_id` rather than leaving the rationale sentence beside a single-column index.

**Adding the MCP session-event projection to CODE-7's inventory.** `pkg/gateway/mcpfabric/mcp/projection.go:319`
declares a `SlotID` field in a local decode struct that is never read again, so the projection re-emits
nothing and is not a client-facing surface carrying the field.

### Recorded limits

- §4.5(d) is conditional. If the shutdown split does not land, `ShutdownRequest` keeps `slot_id`, the gRPC
  leg retains one message whose scope is encoded by field presence, and the `SlotId` wrapper survives with
  one user.
- D6 replaces a machine-derivable classification with a declared one. The tier-0 gate checks that every
  in-scope message is classified and that the table and the proto agree on the message set. It cannot check
  that a declared scope matches what the adapter handler does with the request. Under §4.1's choice (b) the
  `GatewayControl` request messages are outside the gate entirely.
- The service-mode sense of "slot" is distinguished but not unified with the session-mode sense, and is not
  addressed by D2, D3, D4, D7, or D10.
- The runtime-facing contract changes whether or not §4.6.2 lands. A runtime built against the current
  contract, which is entitled to see no identifier on a single-session pod, must be updated. This proposal
  does not claim a runtime-compatible path.
- If §4.6.2 is deferred, §4.6.1 still lands and the proposal is coherent, but the JSONL leg then retains a
  field named for a pod-side resource the runtime does not hold, and the two legs disagree on how a session
  is spelled.
- The adapter manifest cannot today carry any static value a runtime could key on for concurrency behavior.
  It has no `maxConcurrentSessions` or slot-count field (`pkg/adapter/manifest.go:105-159`), it is a single
  pod-global file in `ManifestDir` (`pkg/adapter/server.go:113-116`; `WriteManifest` at `manifest.go:183`)
  whose `sessionId` is single-valued, and on the concurrent path no manifest is written at all, since
  `startSessionSlot` (`pkg/adapter/slotsession.go:27-52`) returns after `Runtime.Start` and the only writers
  are the whole-pod paths at `pkg/adapter/session.go:129`, `pkg/adapter/resume.go:106`, and
  `pkg/adapter/sdkwarm.go:229`. If a later change gives the runtime an obligation needing a static value,
  that change must first stage a per-slot manifest write on the concurrent path and resolve the collision
  between the manifest's pod-global `sessionId` and per-slot session identity. This proposal does neither
  and does not presuppose either.
- The unbounded-overtake window in the operation lock is recorded in §3 as a separate finding against the
  `spec/05:542` and `spec/04:691` rules, out of scope here.

## 10. Files touched on application

- `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`,
  `schemas/examples/jsonl.set_tracing_context.json`, and the regenerated `pkg/proto/adapter/v1/`.
- `spec/04`, `spec/05`, `spec/06`, `spec/07`, `spec/08`, `spec/10`, `spec/13`, `spec/14`,
  `spec/15`, `spec/24`, `spec/26`, `spec/28`, `spec/29`. `spec/12` is not touched: SPEC-8 records its one
  slot-assignment site as already correct.
- `docs/getting-started/`, `docs/runtime-author-guide/` (`lifecycle.md`, `platform-tools.md`),
  `docs/reference/adapter-contract.md`, and `docs/reference/glossary.md`.
- `pkg/adapter/` (`slot.go`, `slotframe.go`, `slotsession.go`, `slotcreds.go`, `slotlayout/`, `resume.go`,
  `exportpaths.go`, `holdstate.go`, `session.go`, `sdkwarm.go`, `checkpoint.go`, `oplock.go`, `staging.go`,
  `credentials.go`, `attach.go`, `tracingcontext.go`, `lifecycle.go`, `coordination.go`, `usage.go`,
  `server.go`, `socketruntime.go`, `sessionscrubreporter.go`, `gatewaycontrol/scrubreport.go`).
- `pkg/gateway/podlifecycle/podsession/binder.go`, `slotbinder.go`, `slotfailure.go`, and `podclaim/`, for
  the slot the resume reserves and the concurrency the resume `BindResult` reports, with
  `pkg/gateway/sessionserver/start.go` populating it from the resolved `PoolMatch`.
- `pkg/gateway/runtime/adapterclient/client.go`, `pkg/gateway/session/executor/pod.go`,
  `pkg/gateway/checkpoint/` (`partialmanifeststore/`, `checkpointer/`), `pkg/gateway/sessionserver/`
  (`derive.go`, `messages.go`, `toolapproval.go`, `start.go`),
  `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`.
- `sdks/client/go/`, `sdks/client/python/`, `sdks/client/typescript/`.
- `sdks/runtime/go/`, `sdks/runtime/python/`, `sdks/runtime/typescript/`.
- `pkg/adapter/warmlayout.go`, for the warm-time layout D8 changes and the shared-assets directory the
  corrected §6.4 paragraph describes.
- `pkg/gateway/sessionserver/lifecycle.go` and `pkg/upload/archive/archive.go`, for the two
  `/workspace/current` default constants CODE-2 moves to the slot root.
- `cmd/runtimes/echo-concurrent/` (`main.go`, `dispatch.go`, `slot.go`, `main_test.go`), and
  `cmd/runtimes/echo`, `cmd/runtimes/echo-embedded`, and `cmd/runtimes/preconnect-echo`, whose workspace
  flags default to the retired path.
- `cmd/lenny-adapter/main.go`, whose `WorkspaceBase` is derived by taking the parent of `--workspace-root`
  (`:272`), a derivation that only holds while the workspace root is one level under the base.
- A new migration dropping the two `slot_id` columns, re-keying the three indexes, and rewriting
  `sessions.workspace_root` to the slot path.
- `tests/testinfra/kind/agent-workload.yaml`, `tests/testinfra/kind/install.sh` (with
  `bootstrap-overlay.gen.yaml` regenerated), and `tests/testinfra/k8s/agent-workload-load.yaml.tmpl`.
- `tests/claim-map.json`, `tests/spec-map.json`, `tests/tier0_static/claim_register_proto_agreement_test.go`,
  `scripts/seed-claim-register.py`, `PROPOSAL-QUEUE.md`, `TEST-GAPS.md`,
  `proposals/0072_fix_correct-the-inconsistencies-the-scenario-authoring-surfaced.md`, and
  `proposals/0064_*.md`.
- The tier-0, 1, 2, 3, 4, 5, 7a, 9, 10, and 11 cases in §8 and their `tests/spec-map.json` entries.
