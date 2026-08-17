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
`ConfigureWorkspaceRequest` (`:1609`) carries the same defect at a different site. Its handler resolves no
root of its own: it takes the working directory from the request (`pkg/adapter/sdkwarm.go:204`) and hands it
to the pre-connected SDK (`:249`). The gateway supplies that value from the pod-global negotiated root
(`pkg/gateway/podlifecycle/podsession/binder.go:989`, whose third argument becomes the `cwd` field at
`pkg/gateway/runtime/adapterclient/client.go:160, :163`), so on a concurrent pod every `preConnect` session
is pointed at a tree no slot owns.

**Coordinator hold state.** `pkg/adapter/holdstate.go:91-96` arms the hold from `s.currentSession()`, which
reads the pod-global `s.sessionID` that only `claimSession` sets (`pkg/adapter/session.go:361-372`).
`StartSession` returns early into `startSessionSlot` (`pkg/adapter/session.go:103-106`) and never calls it,
so `s.sessionID` is empty for the life of a concurrent pod and the hold never arms. §10.1 coordinator-loss
protection is absent on exactly the pods that multiplex the most sessions. §1.9 generalizes this finding
from the hold path to the whole session-validation path.

**The §7.3 step (d) guard.** `pkg/adapter/resume.go:61` compares `ExpectedWorkspaceRoot` against the
pod-global `s.WorkspaceRoot`, so the "same absolute cwd" assertion passes vacuously on a concurrent pod,
and the restore then proceeds into the wrong tree.

### 1.3 Credential rotation defeats the per-slot independence it is specified to provide

`spec/06_warm-pod-model.md:28` specifies per-slot credential leases at
`/run/lenny/slots/{slotId}/credentials.json` when a pool is concurrent, "rather than a single global
`/run/lenny/credentials.json`". `RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`, and
`RevokeCredentialsRequest` each carry `slot_id`, and the adapter honors it
(`pkg/adapter/credentials.go:116, 158, 331`).

No gateway caller sets it. `RotateCredentials` (`pkg/gateway/runtime/adapterclient/client.go:234-241`) and
`ExtendCredentialLease` (`:253-262`) build their requests with no `SlotId`, and the adapter's
`RevokeCredentials` has no gateway caller at all: the only `RevokeCredentials` calls under `pkg/gateway`
are the Token Service's (`pkg/gateway/credentials/credassign/client.go:306`). A rotation on a concurrent pod therefore
rewrites the one pod-global credential file every slot's runtime reads, so one session's rotation
overwrites the bundle a co-tenant session is using. That defeats the file separation `spec/06:28` states as
the reason for the per-slot lease: per-slot files keep "a single credential rotation from disrupting all
concurrent slots simultaneously". The defect and its fix are the file, and the rest of the independence the
same line claims stays partly unmet: the in-flight gate that precedes the rewrite is pod-wide per provider,
which CODE-4 states, §9 records as a limit, and SPEC-4 restates `spec/06:28` on.

The read boundary is a separate property, and neither this defect nor its fix moves it.
`spec/13_security-model.md:30` states that each per-slot file is mode `0440` under the shared
`lenny-cred-readers` group, so "any slot's agent code can read every other slot's credential file", and
that "Lenny does not mitigate this at the pod level". The requirement it places on deployers needing
strict credential-lease isolation, `sessionPolicy.maxConcurrentSessions: 1`, rests on the pod then holding
at most one lease at a time rather than on file separation. This proposal changes neither the group
ownership scheme nor the lease count, so that requirement stands as written and SPEC-4 keeps it.

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
calling `slotlayout.EnsureTree`). Nine code comments repeat or re-import the error, two of them by quoting
`spec/06:391` verbatim as their spec basis and three of them by writing "adapter-assigned" outright
(`pkg/gateway/runtime/adapterclient/client.go:820`, `pkg/gateway/session/executor/subprocess.go:376`, and
`pkg/gateway/session/executor/pod.go:149`).

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
so the key lookup is the identity function. The entry's `sessionID` field records a binding state rather
than an address. `ensureSlotStateLocked` (`pkg/adapter/slot.go:74-95`) creates an entry with `sessionID`
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

**D6. A request message's scope is declared in the specification rather than derived from its field set.**
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
the five specification sentences and the nine code comments.

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
One edit lands at `spec/06:77` inside the `preConnect` rejection row: SPEC-1 restates the row's
"multiplex onto a single pod via `slotId`" clause on the session identifier, because SCHEMA-2 leaves no
`slotId` property on the wire for it to name. The condition the row tests, and the rejection it states,
are what that edit does not change.

Two edits land at `spec/13:30` around the `ConcurrentWorkspaceCredentialSharing` condition itself. §4.7
renames the path placeholder in the paragraph's per-slot credential path from `{slotId}` to `{sessionId}`,
and SPEC-4 adds one sentence recording that a rotation now rewrites the addressed session's own file. The
paragraph's cross-slot readability statement, its "Lenny does not mitigate this at the pod level"
sentence, and its `sessionPolicy.maxConcurrentSessions: 1` requirement with the at-most-one-lease
justification behind it are kept verbatim, because this proposal moves neither the read boundary nor the
lease count they rest on (§1.3). The condition expression the gate tests is what neither edit changes.

The sizing, admission, capacity, draining, and recycling arithmetic is untouched. Every formula is keyed on
the number of slots and already degenerates correctly at one (`spec/05:588`, `spec/10:136`).

`/workspace/shared/`'s contents, its read-only mount, and the population step that fills it are untouched.
What changes is the specification's claim that the directory exists only when `maxConcurrentSessions > 1`
(`spec/06:397`, `spec/26:44`). The adapter mounts and populates it on every pod at warm time, before any
slot exists, so the qualifier is a divergence rather than a rule. SPEC-3 drops it at both sites and corrects
the attribution in the `spec/06:397` paragraph. The tree stays pod-wide and stays outside every slot root, for the
reason §5 records under "Where the identifier sits in the path".

The five gaps `spec/29_communication-scenarios.md:1541-1565` records as unstated are not closed here.
§29.10 currently scopes them to concurrent pods, and §4.4 addresses the scoping rather than the gaps. The
first of them, whether the adapter's hold state is partitioned per slot (`spec/29:1544-1548`), stays open.
What CODE-3 fixes is the separate live defect §1.2 records, that the hold never arms at all on a pod whose
sessions all take the slot path. The hold itself stays what `spec/10_gateway-internals.md:57` and
`spec/28_communication-channels.md:250` state it is, a property of the adapter process, enforced by the
pod-wide interceptor at `pkg/adapter/holdstate.go:246` reading the single `s.hold.active` flag (`:187-190`).
Partitioning it would contradict those two sentences, so it needs a specification change this proposal does
not stage. The termination the hold's timeout performs runs once per bound session, per CODE-3, which leaves
the flag and the fence exit pod-wide.

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
`Resolve`, `ValidateSlotID`, `EnsureTree`, and `RemoveTree`) from the `slot` stem is out of scope. The
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

The table is written out below in the form the specification takes it. Its unit is a request message the
proto declares, spelled exactly as the proto spells it, so the tier-0 gate in §8 can reconcile the two
without a naming convention of its own. Every request message the proto declares on either service is a
row, which is what "in scope" means in that gate. `CheckpointRequest` is the stream envelope rather than an
addressed request, and it is classified for the scope of the `CheckpointStart` frame that opens the stream,
with `CheckpointStart` carrying its own row because §4.5(c) changes its address.

| Request message | Service | Direction | Scope |
|:--|:--|:--|:--|
| `PrepareWorkspaceRequest` | `Adapter` | gateway → adapter | session |
| `FinalizeWorkspaceRequest` | `Adapter` | gateway → adapter | session |
| `RunSetupRequest` | `Adapter` | gateway → adapter | session |
| `StartSessionRequest` | `Adapter` | gateway → adapter | session |
| `ConfigureWorkspaceRequest` | `Adapter` | gateway → adapter | session |
| `SendMessageRequest` | `Adapter` | gateway → adapter | session |
| `AttachRequest` | `Adapter` | gateway → adapter | session |
| `AssignCredentialsRequest` | `Adapter` | gateway → adapter | session |
| `RotateCredentialsRequest` | `Adapter` | gateway → adapter | session |
| `ExtendCredentialLeaseRequest` | `Adapter` | gateway → adapter | session |
| `RevokeCredentialsRequest` | `Adapter` | gateway → adapter | session |
| `InterruptRequest` | `Adapter` | gateway → adapter | session |
| `CheckpointRequest` | `Adapter` | gateway → adapter | session (stream envelope; scope of its `CheckpointStart`) |
| `CheckpointStart` | `Adapter` | gateway → adapter | session |
| `SignalDeadlineRequest` | `Adapter` | gateway → adapter | session |
| `ResumeRequest` | `Adapter` | gateway → adapter | session |
| `CheckpointBarrierRequest` | `Adapter` | gateway → adapter | session |
| `ExportPathsRequest` | `Adapter` | gateway → adapter | session |
| `ReportUsageRequest` | `Adapter` | gateway → adapter | session |
| `ShutdownSlotRequest` | `Adapter` | gateway → adapter | session |
| `ShutdownPodRequest` | `Adapter` | gateway → adapter | pod |
| `CoordinatorFenceRequest` | `Adapter` | gateway → adapter | pod |
| `DemoteSDKRequest` | `Adapter` | gateway → adapter | pod |
| `NegotiateVersionRequest` | `Adapter` | gateway → adapter | pod |
| `GetObservedIntegrationLevelRequest` | `Adapter` | gateway → adapter | pod |
| `AdapterEventsRequest` | `Adapter` | gateway → adapter | pod |
| `ListPlatformToolsRequest` | `GatewayControl` | adapter → gateway | session |
| `CallPlatformToolRequest` | `GatewayControl` | adapter → gateway | session |
| `ListSessionConnectorsRequest` | `GatewayControl` | adapter → gateway | session |
| `ListConnectorToolsRequest` | `GatewayControl` | adapter → gateway | session |
| `CallConnectorToolRequest` | `GatewayControl` | adapter → gateway | session |
| `ReportSessionScrubRequest` | `GatewayControl` | adapter → gateway | session |
| `ReportPodScrubRequest` | `GatewayControl` | adapter → gateway | pod |

`CoordinatorFenceRequest` carries `session_id` and stays pod-scoped, which is why the classification is
declared rather than derived. `DemoteSDKRequest` (`schemas/lenny-adapter.proto:1630-1634`),
`NegotiateVersionRequest` (`:1642-1651`), `GetObservedIntegrationLevelRequest` (`:1674-1684`), and
`AdapterEventsRequest` (`:1699-1701`) carry no session field at all and address the pod's adapter process.

If review declines the `ShutdownSlot`/`ShutdownPod` split, the two shutdown rows collapse into one
`ShutdownRequest` row classified as both, with a note naming `slot_id` presence as the discriminator, and
§9 records that as the one row the declared classification does not resolve.

`ShutdownRequest` is the one message that is genuinely both, and it is the case
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651` pins: a recycle shutdown is a whole-pod
scrub rather than a per-slot teardown, and that is true on a concurrent pod as well as a single-session
one, because a recycle returns the whole pod to the warm pool and scrubs every tree on it rather than
releasing one occupant's slot.
The base case is that it is split into a session-scoped `ShutdownSlotRequest` and a pod-scoped
`ShutdownPodRequest`, so
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
`slot.go:123-133`, `checkpointRootsForSlot` at `:147-173`, `resolvePrepareStagingDir` at
`staging.go:127-140`, and the
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
shared credential-file group-read access, shared egress identity, pod health, `REG-SLOTCOUNT` above one,
and the intra-pod MCP surface, which is one
platform socket and one per-connector socket set for the whole pod. Those are properties of two sessions
sharing a kernel, and they remain properties of concurrent pods alone. Credential-file group-read access
is on the list because `spec/05:515` enumerates it among the properties
`acknowledgeProcessLevelIsolation` makes the deployer accept and the retained `spec/29:1512-1516` bullet
already states it; CODE-4 addresses the rotation defect §1.3 records and leaves that read boundary where
`spec/13:30` puts it. The MCP surface is new to the
co-tenancy list, because CODE-1's start-side merge starts the servers on every pod; its authority rule is
in CODE-1, which resolves the calling session from the slot registry, refuses the call when the pod holds
other than one bound entry, and refuses it permanently once the pod has held more than one bound entry at
any point in its life.

This split is what keeps the change from promoting §29.10's five unstated gaps into
platform-wide open questions. Four of them are gaps about two slots interacting, and they stay in the
co-tenancy half: the hold-state partitioning gap (`spec/29:1544-1548`), which §3 records as staying open,
the `Interrupt` and drain-barrier addressing gap (`spec/29:1549-1555`), whose opening sentence at
`spec/29:1550` asserts the admission rule §4.10 re-keys and is restated by SPEC-6 while the gap itself
stays open, the
`CH-ADAPTEREVENTS` replica gap (`:1556-1559`), and the two-replica coordination gap (`:1560-1563`). The
fifth, the buffering or replay policy for a message the adapter holds on `CH-MSGSOCK` while the runtime is
absent (`:1564-1565`), is already stated for a pod of either kind, so it belongs with the addressing
mechanisms that become general rather than with the co-tenancy analysis, and it moves to the section that
owns `CH-MSGSOCK`.

### 4.5 The gRPC leg, message by message

Eighteen messages carry a `SlotId slot_id` field. They split first by service and then by treatment.
Seventeen are on `service Adapter` (`schemas/lenny-adapter.proto:32`), the gateway-to-adapter surface. One,
`ReportSessionScrubRequest`, is on `service GatewayControl` (`:250`), the adapter-to-gateway surface whose
doc comment at `:234-237` states that the adapter initiates every RPC on it.

**(a) No field is added.** `ExportPathsRequest` (`:1478`) and `ConfigureWorkspaceRequest` (`:1609`) each
carry `SessionId session_id = 1` and no slot field. The delegation-export defect §1.2 records is fixed by
reading `session_id` in `ExportPaths`. The `ConfigureWorkspace` half of that defect is fixed on the sending
side, because the handler resolves no root and takes its working directory from
`ConfigureWorkspaceRequest.cwd`, which `schemas/lenny-adapter.proto:1610-1613` defines as the finalized
working directory; CODE-2 derives the per-session value at the gateway call site. The adapter's remaining
obligation on this message is the §4.2 `session_id` validation. That field comment names
`/workspace/current` as the production value, so §8's tier-11 literal sweep over `schemas/` reaches it and
it is restated for the uniform layout.

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
and `pkg/adapter/gatewaycontrol/scrubreport_test.go:23, 34-36, 42-58`, which is the producer-side test and a
hand rewrite rather than a compile-only edit; and, in the specification, the §4.7
adapter-to-gateway RPC table and the §5.2 scrub-model text, plus the proto comment at `:440-445`. This is a cross-package API change:
`RecordSessionScrub` loses its slot parameter, which is behavior-preserving by inspection because the
implementation already discards it.

Owners for (b′): SCHEMA-1 removes the field and rewrites the proto comment, CODE-1 stages the producer, the
adapter seam, the consumer, and the `RecordSessionScrub` signature change under its explicit (b′) clause,
SPEC-7 stages the §4.7 adapter-to-gateway RPC table and the §5.2 scrub-model text, and §8 inventories
`scrubreport_server_test.go` under the compile-only edits and `scrubreport_test.go` under the hand rewrites.
`pkg/adapter/gatewaycontrol/gatewaycontrol_test.go` is not an edit site: its `stubGatewayControl` field at
`:46` and its `ReportSessionScrub` method at `:90` take the whole request message and name no slot, so the
field removal and the arity change leave them compiling unchanged.

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

- **The session-scoped set** is the frame types that address a session: `messageEnvelope`, `tool_call`,
  `tool_result`, `response`, `set_tracing_context`, and `status`. The first five are the frames the schema
  tags today (`schemas/lenny-adapter-jsonl.schema.json:58, 139, 161, 190, 222`). `status` (`:198-208`) is a
  sixth outbound content frame, "surfaced to the client as a low-priority signal", which the schema declares
  with no identifier property. It belongs in the set because the demultiplexer addresses it per session
  today: `demuxSlotOutput` filters every frame by the identifier it carries
  (`pkg/adapter/attach.go:294`, `if fs := frameSlotID(line); fs != "" && fs != slotID { continue }`), and an
  existing fixture emits a tagged `status` frame that the filter drops for a sibling stream
  (`pkg/adapter/tracingcontext_addressing_test.go:42`). Defining the set by the frames the schema happens to
  tag would leave `status` outside the narrowed predicate below and fan one session's status out to every
  co-tenant Attach stream, which widens the delivery surface rather than preserving it. SCHEMA-2 therefore
  adds the `sessionId` property to the `status` frame with the rename. Both the population rule and the
  rejection rule apply to these six frames only.
- **Adapter to runtime.** The adapter populates the per-session identifier on every frame in that set, on
  every pod, regardless of `sessionPolicy.maxConcurrentSessions`. The conditional descriptions at the five
  schema sites are dropped, and `spec/28_communication-channels.md:547-551` and `:566-571` are restated
  unconditionally.
- **Runtime to adapter.** For a frame in that set, an absent identifier on a pod holding exactly one slot
  resolves to the stream's own binding. The slot count is every entry in the adapter's registry, bound or
  registered-but-unbound, which is the quantity SCHEMA-1's drain gate reads, so the two decisions read one
  number and both fail closed while a second session's workspace is being prepared; SCHEMA-1 states the
  reclaimer that keeps a failed bind from holding that number above one for the pod's life.
  On a pod holding more than one slot it is rejected and relayed to no stream. The decision is taken in
  each Attach stream's demultiplexer, which is where the adapter already filters the shared runtime's
  interleaved output (`pkg/adapter/attach.go:72`, `:278-295`), so a pod holding more than one live Attach
  stream evaluates one unaddressed frame once per stream: `lenny_adapter_unaddressed_frame_rejected_total`
  increments once per rejecting stream rather than once per frame, and the log line is written once per
  rejecting stream. The counter's unit is stated for where the decision sits, because moving the decision
  ahead of the fan-out would need a pod-level reader of the shared runtime output that no code path holds
  today, and §8 pins the increment count on a two-slot pod so the granularity is asserted rather than
  discovered. This is not the conditional D1 retires. Absence never selects
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

The frame field `slotId` is renamed `sessionId` on the five session-scoped frames that carry it, and the
`status` frame, which carries no identifier today, gains `sessionId` under §4.6.1's population rule. It is D2's realization
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
`spec/15` envelope field list, which today describes `slotId` at `spec/15:1593` as "the concurrent slot
this message is addressed to" with no reference to `from`. SPEC-7 owns that sentence's replacement
description and SPEC-1 owns the removal of the presence condition stated in the same sentence. The four frames with no `from` block need no
such note. A contract-tier case pins that a `message` frame whose `from.id` names a different session than
`sessionId` is delivered to the session named by `sessionId`. `toSessionId` is rejected as a name: it
breaks the naming uniformity with the gRPC leg's `session_id` that D2 and D3 rest on, and prices a
one-frame ambiguity into every frame's name.

**The integration-level tension is settled in §4.6.1.** The Basic-level row at
`spec/15_external-api-surface.md:1783` excepts the per-session identifier from the fields a Basic-level
runtime may ignore, for the reason §4.6.1 states. The rename changes only the name that row carries, from
`slotId` to `sessionId`. SPEC-7 stages that row together with the two restatements of the same permission
on the wire leg, `spec/28:588` and `spec/28:604`, and with the reader-facing matrix row at
`docs/runtime-author-guide/integration-levels.md:23`. SPEC-1 takes the presence condition on that same
line, and under §4.6.2 the key spelling as well.

**Blast radius**, additional to what SPEC-6 and §4.8 already stage, falls into four categories.

(i) Mechanical renames of the wire key:

- `schemas/lenny-adapter-jsonl.schema.json:58, 139, 161, 190, 222`, and the `status` frame at `:198-208`,
  which gains the `sessionId` property rather than taking a rename, per §4.6.1. The `status` frame is
  modelled in one runtime SDK only: the Go SDK declares `outboundStatus`
  (`sdks/runtime/go/runtime/types.go:323-328`, `type`, `state`, and `message`), which gains a
  `SessionID string` member with the `sessionId` tag. The declaration has no builder today
  (`grep outboundStatus sdks/runtime/go/runtime/` returns the declaration alone), so the member is the
  type's statement of the frame rather than a stamping site. The Python and TypeScript runtime SDKs model
  no `status` frame at all; their only `status` symbol is the unrelated `MessagePart.status` streaming
  label (`sdks/runtime/python/lenny_runtime/types.py:53-54`, `sdks/runtime/typescript/src/types.ts:42-43`).
  §9 records that absence as a limit.
- The gateway's own JSONL structs, which produce and consume the wire key and which no compiler catches
  because both decode by tag: `pkg/gateway/session/executor/subprocess.go:370-383`, the `messageEnvelope`
  the executor marshals for every outbound `message` frame, whose `SlotID` member and its
  `json:"slotId,omitempty"` tag take the rename together with the doc comment SPEC-8 corrects at `:376`; and
  `pkg/gateway/session/executor/pod.go:224-231, 269-275`, the `toolCallFrame` the approval gate decodes from
  the runtime and re-marshals on an approval. Without these the gateway would emit a key the published
  schema no longer declares and decode one the runtime no longer emits. The envelope's population site,
  `pkg/gateway/session/executor/pod.go:93-107`, is not a rename: it fills the member from `bind.SlotID`,
  which CODE-2 keeps empty on an exclusive pool, so it is staged in CODE-6 as a value re-point rather than
  here.
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
- Two gateway-side test fakes that decode the envelope the executor marshals, by tag and in an anonymous
  struct, so they fail the same silent way: `pkg/gateway/session/executor/pod_test.go:208-213`, the
  `slotCapturingAdapter` decode that feeds `envelopeSlot`, and
  `pkg/gateway/sessionserver/messages_component_test.go:75-78`, the `perSlotAdapter` decode that feeds
  `envSlots`. Both read the key CODE-6 renames on the producer side, so after application each yields the
  empty string and the assertions over it either fail or pass for the wrong reason. §8 states their
  disposition.
- Runtime-author documentation field tables and prose:
  `docs/getting-started/concepts.md:173` and the
  `docs/reference/adapter-contract.md` field rows at `:156, :190, :281, :324` whose "Present only when
  maxConcurrentSessions > 1" conditions §4.6.1's population rule deletes. The same page's JSON examples at
  `:140, :180, :232, :271, :316` take the key rename with those rows, so an example and the field row
  beneath it spell the same key the published schema declares; SPEC-6 stages the value correction at those
  same lines and this deliverable stages the key. The same page's `status` section
  (`:302-308`) carries an identifier-free example and the sentence "Informational. The adapter forwards
  status updates to the gateway for client visibility.", and it takes the `sessionId` property and the echo
  obligation with the schema change, so the page does not document a frame the adapter now rejects when it
  arrives unaddressed on a pod holding more than one slot.
- The §28.5.3 canonical frame blocks that declare the key, `spec/28_communication-channels.md:632, 665,
  689, 795`, together with the prose naming it at `:604` and the `message` example at `:600`. SPEC-1 stages
  the four schema blocks and the prose sentence and SPEC-6 stages the example. Without them the applied
  specification would declare a property the published schema no longer carries, and the tier-11 frame
  reconciliation §8 states would fail on the specification text rather than on the documentation.
- The two specification sentences stating the cwd derivation (`spec/06:392`, `spec/05:513`) and the
  restatement at `spec/29_communication-scenarios.md:1448`.
- `spec/15_external-api-surface.md:2283`, the §15.7 `Message` doc comment enumerating the envelope's
  fields, which names `slotId` among them and mirrors the SDK type at
  `sdks/runtime/go/runtime/types.go:71`. SPEC-6 stages the specification half and CODE-6 the SDK half, in
  one change, so the reference block and the type it documents do not diverge.

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

- `spec/06_warm-pod-model.md:28` (`/run/lenny/slots/{slotId}/credentials.json`, in the per-slot
  credential-lease paragraph SPEC-4 also merges) and the §6.4 block at
  `:373-393`, covering the layout listing (`/workspace/slots/{slotId}/`, `/sessions/{slotId}/`,
  `/artifacts/{slotId}/`) and the adapter and gateway responsibility bullets.
- `spec/05_runtime-registry-and-pool-model.md:513` and the deployer-acknowledgment text at `:515`, which
  states that "each slot's `/run/lenny/slots/{slotId}/credentials.json` is readable by every slot's agent
  process". `:517` is the cross-tenant prohibition, which carries no placeholder and stays out of scope
  per §3.
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

The rewritten rule states first that it governs the session-scoped frame types only, the six frames named
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

`spec/28:838-841`, the "Non-guarantee" paragraph that follows the addressing-rule block, states the
guarantee's limit in terms of "the `slotId` a frame carries". It takes the field rename and drops its
"on a concurrent pod" scoping, because one runtime process serves every slot on every pod under D1 and the
limit it records holds there too.

Code and schema sites: `pkg/adapter/tracingcontext.go:49-59` deletes the `slotID == ""` branch and renames
its parameters accordingly; `docs/reference/adapter-contract.md:328` and the frame table above it;
`docs/runtime-author-guide/platform-tools.md:428` and `:449`, the two runtime-author statements of the same
rule, which are rewrites rather than the key rename §4.6.2(i) covers: each says the identifier is required
only "On a pod running concurrent slots" and that an unaddressable frame "is dropped and logged rather than
applied", and §4.6.1 replaces both halves. Each is restated as the frame carrying the per-session identifier
on every pod, an unaddressed frame resolving to the stream's binding on a pod holding one slot and being
rejected, counted on `lenny_adapter_unaddressed_frame_rejected_total`, and logged on a pod holding more, and
a Basic-level runtime echoing the identifier it was handed. `:449` is the Basic-level tracing path §4.6.1
names, so it is where the echo obligation reaches a Basic-level author. `docs/runtime-author-guide/lifecycle.md:397`
is a third statement of the same rule and takes the same treatment rather than the key rename §4.6.2(i)
covers. It is the `maxConcurrentSessions > 1` bullet of the page's session-mode list, and it tells the
author that a dispatch loop keyed on the identifier is required only there, that "all binary protocol
messages (inbound and outbound) carry a `slotId` field" only there, and that the per-slot workspace applies
only there. After §4.6.1 and D7 all three hold on every pod, so a key rename alone would leave the page the
runtime-author guide owns for this contract telling every author of a default-pool runtime that neither the
identifier nor the dispatch and echo obligations reach them. The bullet is restated as the frame carrying
the per-session identifier on every pod whatever `maxConcurrentSessions`, the runtime echoing the identifier
it was handed at every integration level, and the per-session workspace being
`/workspace/slots/{sessionId}/current` on every pod, and the `maxConcurrentSessions > 1` bullet keeps only
the co-tenancy facts it also states: the shared process namespace and filesystem-level isolation, the shared
CPU and memory with no per-slot cgroup subdivision, and the `preConnect` admission rule. Neither literal
sweep and neither
tier-11 reconciliation in §8 is keyed on this text, so the three lines are staged by name;
`schemas/lenny-adapter-jsonl.schema.json:58-61`; `spec/28_communication-channels.md:795`, the
`set_tracing_context` frame's own schema block, whose conditional description SPEC-1 replaces with the
population rule; and `schemas/examples/jsonl.set_tracing_context.json:8`.

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
- `spec/12_storage-architecture.md` §12.5 (`:337, 340, 352, 362`) and `spec/16_observability.md:198`, also
  staged by SPEC-9: both restate the retention and supersede rules on the two-column key, so both move to
  `session_id` alone in the same change as the index re-key.
- `spec/05_runtime-registry-and-pool-model.md:542`, also staged by SPEC-9: the per-slot checkpoint cap
  selects `last_checkpoint_workspace_bytes` "for the `(session_id, slot_id)` pair in Postgres", which is a
  further normative statement of the same scoping key and drives a CRD-webhook floor. The column is a
  `sessions` column with no slot dimension (`migrations/0087_sessions_retry_policy.up.sql:22`), so after
  MIG-1 the pair names no column any table carries. SPEC-6 edits the same line for §4.10's identifier-order
  justification; the two edits land together.

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

The lock's admission rule is stated once and restated at three further specification sites, and each of the
four statements is wrong on two counts after application: it keys admission on a `slotId` that
`CheckpointStart` no longer carries once §4.5(c) moves the op-lock key onto `session_id`, and it carries the
"on a single-session pod at most one operation may be queued" special case that D1 retires by putting a slot
on every pod. The source statement is `spec/04:691`, the §4.7.2 **Queue depth** bullet, whose identifier-order
clause is staged above; its admission and coalescing clauses are re-keyed here, its single-session sentence
is dropped, and its remaining clauses cease to be conditioned on `maxConcurrentSessions > 1`.
`spec/28:291-294` is the `CH-CHECKPOINT` card's **Exclusivity** bullet, `spec/28:1651-1658` is the §28.6 "One
operation per pod" paragraph, which states the rule and then restates the coalescing sentence, and
`spec/29:805-806` restates it a third time. All four state admission and coalescing per distinct session
identifier, with the single-session clause dropped, so the two normative statements on the
gateway-to-adapter channel agree with the rewritten `spec/04:691` and with the post-removal
`CheckpointStart` address. SPEC-6 stages them with the sites above.

### 4.11 One session check in the adapter

D13 replaces `checkSession` (`pkg/adapter/session.go:346-356`) and `checkSlotSession`
(`pkg/adapter/slotsession.go:116-128`) with a single `checkSessionBound(sessionID)` resolving through
`s.slots`. Under D5 the two are the same check, because the lookup key and the compared value are the same
string. `Server.sessionID` is retired with `useSlot`, and `claimSession` and `claimSessionForConfigure`
become slot-registry claims.

Both claims carry behavior beyond writing the retired field, and the re-key restates each rather than
dropping it.

`claimSession` (`pkg/adapter/session.go:358-372`) returns `Unavailable` with "pod is not idle" for any
second session. The registry claim in `startSessionSlot` (`pkg/adapter/slotsession.go:39-43`) returns
`Unavailable` for a re-claim of a bound slot, and under D5 that is a re-claim by the same session. The
merged claim keeps that refusal and does not reproduce the pod-idleness form, because the adapter is never
told the pod's ceiling: nothing renders `maxConcurrentSessions` into its configuration, and a rule keyed on
the live slot count would be the concurrency conditional D1 retires. The ceiling stays with the gateway,
which allocates the slot and counts occupancy at the same site. The re-key reaches both call sites,
`pkg/adapter/session.go:117` on `StartSession` and `pkg/adapter/resume.go:47` on `Resume`, so a resume for a
second session is admitted onto its own slot on the same terms. The one exception is the SDK-warm pod class,
where the merged claim keeps the different-session refusal for the reason the `claimSessionForConfigure`
paragraph below states. §9 records the limit and §8 pins both arms on both call sites.

`claimSessionForConfigure` (`pkg/adapter/sdkwarm.go:285-301`) additionally reports `fresh`, which gates the
manifest write and the platform MCP start on the SDK-warm path (`sdkwarm.go:217-233`), so an incorrect
`fresh` rewrites the §15.4.3 nonce the runtime already authenticated with. `fresh` is derived from the
entry's bound state rather than from its existence, because the registry holds a registered-but-unbound
state that a prior workspace-prep RPC creates (D2): the claim reports `fresh` when it binds an unbound
entry and reports `fresh=false` when the entry is already bound to the same session. Its second behavior,
the `Unavailable` for a different session on an already-claimed pod (`sdkwarm.go:296-298`), stands rather
than being retired with `claimSession`'s. The ground `claimSession`'s retirement rests on, that a different
session arrives on its own slot, does not hold on the pods this RPC serves: `ConfigureWorkspace` returns
`Unimplemented` on anything but an SDK-warm pod (`sdkwarm.go:212-215`), and `spec/06_warm-pod-model.md:71`
admits `preConnect: true` only with `maxConcurrentSessions: 1`, so the pod holds one pre-connected runtime
process with one working directory, which the handler re-points in place (`sdkwarm.go:249`). Admitting a
second session there would report `fresh=true` and rewrite the pod-global manifest (`sdkwarm.go:229`),
regenerating the §15.4.3 nonce the incumbent runtime already authenticated with, which is the hazard the
sentence above names. The refusal is a ceiling the specification itself fixes at one for this pod class
rather than the live-slot-count conditional D1 retires. The same ground reaches `StartSession` and `Resume`
on the same pod class, so the merged claim keeps the different-session `Unavailable` there too, gated on
the pod being SDK-warm rather than on a live slot count. The adapter can evaluate that gate without a
concurrency value: `s.sdkWarmRuntime()` (`pkg/adapter/sdkwarm.go:139-142`) is the same type assertion
`ConfigureWorkspace` already takes at `:211-215`. Without the gate the staged admission would not be
reachable in any case, because the pre-connected runtime refuses a second session at `Runtime.Start`
(`pkg/adapter/embedded.go:69-71`, reached for this pod class through
`SDKWarmInProcessRuntime.ConfigureWorkspace` at `pkg/adapter/embedded_sdkwarm.go:50-51`), so the admission
would fail there with an unrelated error rather than binding a slot. On a pod-warm pod the refusal is
retired, because a different session there arrives on its own slot and its own runtime session.
§9 records which arms are retired and which stand, and §8 pins each.

`releaseSession` (`pkg/adapter/session.go:384-392`) loses its `s.sessionID` write with the field. Its
remaining work, cancelling the pod's MCP servers and clearing `mcpHandshakeSeen`, moves onto the slot
release path. The platform and connector MCP servers are pod-wide, because they bind the single socket the
controller renders and are tracked by one `mcpCancel` and one `connectorCancels` slice
(`pkg/adapter/server.go:126-131, :343-345`), so the cancellation runs when a `ShutdownSlot` leaves the pod
with no other bound session rather than on every release. A release that leaves a co-tenant bound cancels
nothing, since cancelling would take the co-tenant's own MCP surface down with it.

The sticky `credSessionID` backstop the function's comment describes (`pkg/adapter/session.go:375-383`) is
retired on the same grounds as the two claim arms above. Deleting `useSlot` routes every `AssignCredentials`
through `assignCredentialsSlot`, the branch `pkg/adapter/credentials.go:73-75` guards today, and that
handler reads and writes no `credSessionID` (`pkg/adapter/slotcreds.go:23-51`), so the pod-global F-6.1.12
refusal at `pkg/adapter/credentials.go:84-86` becomes unreachable and the field is never set for any
session. Keeping it would oblige the merged handler to re-impose a pod-wide one-session ceiling, which is
the ceiling D1 moves to the gateway. No adapter-side session-identity refusal survives on
`AssignCredentials`, and the proposal states that rather than naming a replacement the merged handler cannot
evaluate. Under D5 the registry key is the session identifier, so `ensureSlotStateLocked(sessionID)` returns
either an entry whose `sessionID` is empty, which `pkg/adapter/slotcreds.go:33-34` then fills with that same
value, or one already equal to it. The wrong-session arm at `pkg/adapter/slotcreds.go:35-37` is therefore
unreachable, and the request carries no second identifier that could name another session. What the merged
handler still enforces is §4.2's non-empty `session_id` and `writeSlotCredentialFile`'s credentials-root
refusal (`pkg/adapter/slotcreds.go:150-154`). A second session's assignment lands on its own slot rather
than overwriting a co-tenant's credential file, which is isolation rather than a refusal. The remaining
F-6.1.12 defence is the gateway-side teardown loop the comment at `pkg/adapter/session.go:376-380` already
names as the primary one, and §9 records the change of enforcement point. The field
itself (`pkg/adapter/server.go:350-352`) is deleted with `s.sessionID`, and `checkCredentialSession`
(`pkg/adapter/credentials.go:355-371`), its only other reader, goes dead with the pod-global rotate and
revoke branches that call it (`:303`, `:337`). `Server.credLeases` (`pkg/adapter/server.go:353-355`) and
`cloneCredentialLeases` (`pkg/adapter/credentials.go:377-383`) go with them, per CODE-1's move of the
manifest `llm` derivation onto the slot's lease set. §9 records the limit beside the two claim RPCs and §8
pins both arms.

`checkCredentialSession` enforces three refusals and the per-slot handlers carry one of them. The
configured-directory refusal survives through `writeSlotCredentialFile`, as the paragraph below states. The
wrong-session refusal has no per-slot counterpart either, for the reason the paragraph above gives: the
comparisons the per-slot rotate and revoke handlers make (`pkg/adapter/slotcreds.go:68-71` and `:120-123`)
read `st.sessionID != sessionID` on an entry keyed by that same session identifier, so under D5 they fire
only on a registered-but-unbound entry and are a bound test rather than a session-identity test. No adapter
handler refuses an operation for naming a different session after the merge. The third,
`if s.credSessionID == ""` returning `FailedPrecondition` with "no credentials have been assigned to this
pod" (`credentials.go:363-366`), has no per-slot counterpart: the handlers' first branch is
`st, ok := s.slotStateLocked(slotID)` (`slotcreds.go:64-67` and `:116-119`), a bare registry lookup
(`pkg/adapter/slot.go:96-99`) that tests whether the slot is registered rather than whether credentials were
ever assigned, and under D1 every live session holds a registry entry, seeded with an empty lease map by
`ensureSlotStateLocked` (`slot.go:74-95`) from the three workspace-prep RPCs before `StartSession`. Left as
it stands, a `RotateCredentials` for a session that never received `AssignCredentials` would materialize
that session's `/run/lenny/slots/{sessionId}/credentials.json` rather than being refused, and a
`RevokeCredentials` in the same state would rewrite it. That is a fail-closed credential-delivery
precondition, so the merged `RotateCredentials` and `RevokeCredentials` handlers restate it on the slot's
own lease set: an entry whose `creds` map is empty returns `FailedPrecondition` with the same message before
any file is written. §8 pins it with a tier-1 case on a registered, bound slot that received no
`AssignCredentials`.

The `CredentialsDir == ""` `FailedPrecondition` at `pkg/adapter/credentials.go:76-79` survives the merge
without being restated in the merged handler. `writeSlotCredentialFile` returns the same code and the same
message when the resolved per-slot credential directory is empty (`pkg/adapter/slotcreds.go:150-154`), and
an unset credentials root leaves that path empty rather than failing resolution
(`pkg/adapter/slotlayout/slotlayout.go:151-154`). The refusal is therefore raised after the registry entry
and the rest of the slot tree exist rather than before, so §8 pins it rather than leaving the ordering
change to be discovered at runtime.

`checkSessionBound` has one exception, stated in SCHEMA-1 and repeated here because this is the section
that owns the check: the merged `ShutdownSlot` handler admits a registry entry that is registered and not
yet bound when the entry is the named session's, and deletes it. Every other session-scoped RPC keeps the
bound test. Without the exception a bind that fails after `PrepareWorkspace` would leave an entry no code
path can ever remove, and the drain gate and §4.6.1's count both read the registry.

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
and is empty on a warm pod, and asserts `/workspace/slots/` instead. One site relies on that assertion
today: `pkg/adapter/warmlayout_test.go` pins it at tier 1, citing the deleted sentence directly
(`:14-16, :37`). §8 stages that file as a hand-rewrite. If the assertion is relied upon anywhere else this
proposal has not found, the decision needs revisiting before SPEC-3 lands.

## 6. Proposed changes

### SCHEMA-1. Remove the duplicate address from the gRPC leg

`schemas/lenny-adapter.proto` takes the message-by-message treatment in §4.5. No field is added to
`ExportPathsRequest` or `ConfigureWorkspaceRequest`. The fifteen gateway-to-adapter `slot_id` fields in
§4.5(b) and the one adapter-to-gateway field in §4.5(b′) are removed, each with `reserved <n>;` and
`reserved "slot_id";` and a comment naming why. `CheckpointStart` gains `SessionId session_id` at a fresh
field number before its `slot_id = 6` is removed, per §4.5(c).

`ShutdownRequest` is split into `ShutdownSlotRequest` and `ShutdownPodRequest` per §4.1, and its `slot_id`
goes with the split. The split is stated here in full so that no part of it is invented at application time.
`ShutdownRequest` (`:1548-1572`) carries `session_id = 1`, `reason = 2`, `deadline_ms = 3`, `slot_id = 4`,
`recycle = 5`, and `coordination_generation = 6`. `ShutdownSlotRequest` is a new message carrying
`SessionId session_id = 1`, `string reason = 2`, `int32 deadline_ms = 3`, and
`int64 coordination_generation = 4`; it carries no `slot_id`, because under D5 `session_id` is the address,
and no `recycle`, because a recycle is a whole-pod operation. `ShutdownPodRequest` is a new message carrying
`RecycleScrub recycle = 1`, `string reason = 2`, `int32 deadline_ms = 3`, and
`int64 coordination_generation = 4`; it carries no `session_id`, because it addresses the pod. Field
numbering restarts at 1 on both, which is permitted because both are new messages rather than edited ones,
so §4.5(e)'s refusal to recycle a number on a published message does not apply. The single
`rpc Shutdown(ShutdownRequest) returns (ShutdownResponse)` (`:195`) becomes two RPCs,
`rpc ShutdownSlot(ShutdownSlotRequest) returns (ShutdownSlotResponse)` and
`rpc ShutdownPod(ShutdownPodRequest) returns (ShutdownPodResponse)`. A oneof on one RPC is rejected: it
would keep the scope encoded in a field's presence, which is what D6 retires. `ShutdownRequest` and
`ShutdownResponse` are both deleted with the split.

The message names are fixed by the two constraints the toolchain imposes, and both were verified against
`buf` before they were written here. A protobuf method name is a symbol in its service's own scope, so a
relative type reference spelled `ShutdownSlot` inside `service Adapter` resolves to the method
`lenny.adapter.v1.Adapter.ShutdownSlot` rather than to a message of that name, and the file does not parse
(`expected message type, found service method`). The `<Rpc>Request` suffix the file already uses everywhere,
as at `schemas/lenny-adapter.proto:195` over `:1548`, avoids the collision. The two responses are likewise
distinct messages rather than one shared `ShutdownResponse`, because the module's lint configuration
(`buf.yaml:11-19`) uses the `STANDARD` rule set with four exceptions, none covering RPC request or response
naming, so a shared response fails `RPC_REQUEST_RESPONSE_UNIQUE` and `RPC_RESPONSE_STANDARD_NAME` once per
RPC and turns the tier-0 `buf lint` check (`cmd/lenny-test/cmd_run.go:510-515`), which is green on the tree
today, red. `ShutdownSlotResponse` and `ShutdownPodResponse` each carry the `bool exited_cleanly = 1` and
`int32 exit_code = 2` that `ShutdownResponse` (`:1601-1604`) carries today. Adding a lint exception instead
is refused: the exception list is a shared module-wide setting, and widening it for one message would
suppress the rule on every service in `schemas/`.

The adapter's presence branch at `pkg/adapter/session.go:225-245` splits into the two handlers with no
shared dispatcher. `ShutdownSlot` validates a non-empty `session_id` per §4.2 and calls `shutdownSlot`.
`ShutdownPod` runs `startPodScrub` on the carried `RecycleScrub` when one is present and the whole-pod
terminate path when it is not.

`ShutdownSlot` is where the whole per-session teardown lands, on every pod. Today the base-mode path
performs it inside the one `Shutdown` handler: `pkg/adapter/session.go:246-286` runs the final usage flush
(`emitFinalUsage`), the `CH-RUNTIMEOPS` drain signal (`drainViaLifecycle`), `Runtime.Close(sessionID)`,
`releaseSession`, and the `ReportSessionScrub` emission whose comment at `:274-282` states that it is what
makes `maxSessionsPerPod` retirement functional in base mode. `shutdownSlot`
(`pkg/adapter/slotsession.go:63-95`) performs the close, the timer cancellation, the tree removal, the
deregistration, and the `ReportSessionScrub` emission, and it performs neither the usage flush nor the
drain signal. The merged `ShutdownSlot` handler carries all of them, so a session's teardown is one
sequence whatever the pool's concurrency, and `ShutdownPod` performs no per-session work.

One of the two added operations is pod-global rather than per-session, and the merged handler scopes it.
`emitFinalUsage` and `Runtime.Close(sessionID)` name the ending session, but `drainViaLifecycle`
(`pkg/adapter/session.go:294-300`) sends `s.Lifecycle.Terminate(...)` on the single pod-wide
`CH-RUNTIMEOPS` connection to the one runtime process serving every slot, and it takes no session or slot
argument. Sending it while a co-tenant slot is still bound would signal that runtime to terminate while it
is still serving another session. The merged handler therefore deregisters the ending session's slot and
sends the drain signal only when that deregistration leaves the slot registry with no entry at all, as the
paragraph below states. That test is on live occupancy at the adapter, which
is the same quantity the recycle-scrub guard is re-keyed onto in §4.11, and it is not the retired
conditional: absence of an address never selects a scope, and the request's address is the ending session
either way. On a pod holding one session at a time the deregistration empties the registry, so the
base-mode sequence is unchanged.

The predicate reads shared mutable state and then performs a pod-global side effect, so it is evaluated
atomically rather than as a check followed by an act. `shutdownSlot` (`pkg/adapter/slotsession.go:63-95`)
releases `s.mu` at `:78` before closing the runtime at `:82-84` and deregistering the slot through
`releaseSlot` at `:86`, and each ending session reaches the handler on its own gateway flow, since
`Binder.ReleaseSlot` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:524-540`) issues one `ShutdownSlot`
per release with no cross-session serialization. An occupancy read taken before the deregistration therefore
lets two co-tenants ending at once each observe the other and send no drain at all. The merged handler
deregisters the ending slot and takes the drain decision in one critical section under `s.mu`, and sends the
signal immediately after that section, when the section left the registry empty, and before
`Runtime.Close`. The order matters and the handler states it: the drain exists to precede the hard close,
which is the sequence the base-mode path runs today (`drainViaLifecycle` at `pkg/adapter/session.go:261`
then `Runtime.Close` at `:263`), and the last session's `Close` tears the shared runtime down
(`pkg/adapter/socketruntime.go:434-466`). A `terminate` frame sent after that reaches a dead runtime, and
`drainViaLifecycle` swallows `errLifecycleNotConnected` and `errLifecycleClosed`
(`pkg/adapter/session.go:294-301`), so the regression would be silent and the §15.4.2 graceful drain would
be a no-op on every pod. The full sequence in the merged handler is the usage flush, the critical section
that deregisters and decides, the drain signal when the registry is empty, `Runtime.Close` carrying the
same decision, the per-slot
tree removal, and the `ReportSessionScrub` emission. `shutdownSlot`'s current order, which closes the
runtime at `pkg/adapter/slotsession.go:82-84` before `releaseSlot` deregisters at `:86`, is what the merge
reorders. Occupancy for this decision counts every
registry entry rather than the bound entries alone, because `ensureSlotStateLocked`
(`pkg/adapter/slot.go:74-95`) registers an entry with an empty `sessionID` from the three workspace-prep RPCs
before `StartSession` binds it (D2), and a session in that state is about to be served by the same runtime
process. §4.6.1's inbound rule counts the same quantity for the same reason, so the two predicates read one
number.

The same number governs the hard close, and without that the withheld drain would protect nothing.
`Runtime.Close` decides its own teardown from the runtime's active set, which only `Start` populates
(`pkg/adapter/socketruntime.go:181-186`, `:372-376`, `:439-444`), and a registered-but-unbound entry has
never reached `Start`, so it is not in that set. An ending session that is the set's last member would
therefore close the shared connection and kill the spawned child (`socketruntime.go:452-465`) with no
preceding `terminate`, which is the silent regression the ordering rule above exists to prevent, and the
incoming session's later `Runtime.Start` would find no process serving the connection it already holds
(`:181-205`, `:278-299`). `Runtime.Close` therefore takes a second teardown
condition from the caller, beside the active-set result it reads itself. `Close` is a method on the `RuntimeProcess` interface (`pkg/adapter/session.go:62`)
rather than a `SocketRuntimeProcess` helper, so the parameter is added to the interface and every
implementation and caller moves with it: `Close(ctx context.Context, sessionID string, last bool) error`,
where `last` states
that no other session on the pod is being served by the process. `SocketRuntimeProcess.Close`
(`socketruntime.go:434`) removes the ending session from its active set under `p.mu` on every call, and it
tears the shared connection down only when `last` is true and that removal left the set empty, returning
early otherwise. The caller's flag is an additional withholding condition on top of the `releaseActiveLocked`
result the method decides from today (`:440-446`) rather than a replacement for it. Keeping the active-set
term is what preserves the atomicity the teardown holds today. `Start` adds a session to the active set
under `p.mu` whenever the process is connected (`socketruntime.go:181-187`), and that is the same mutex
`Close` takes at `:435`, so a session that starts while the ending session's handler sits between its own
critical section and its close is still observed. A teardown decided from the caller's flag alone would be
a read of one structure under `s.mu` followed by an act on another under `p.mu`, and an incoming session
whose `Runtime.Start` returned during the intervening drain send would lose the connection under it, with
every later send failing on `socket runtime is not connected` (`socketruntime.go:326, :344`) and its own
`ShutdownSlot` finding nothing to close.
`MCPRuntime.Close` (`pkg/adapter/mcpruntime.go:265`) and `InProcessRuntime.Close`
(`pkg/adapter/embedded.go:188-204`) each serve one session and hold no active set, so they tear down when
`last` is true and return without closing when it is false. `SDKWarmInProcessRuntime` inherits `Close`
from the embedded `InProcessRuntime` and calls it directly from `DemoteSDK`
(`pkg/adapter/embedded_sdkwarm.go:57-63`), which exists to tear the pre-connected runtime down and so
passes true. Two production
callers survive the merge that removes `pkg/adapter/session.go:263`. The merged `ShutdownSlot` handler
passes the registry-empty result of its critical section, and `onHoldTimeout`
(`pkg/adapter/holdstate.go:180`) passes false on every member of the set CODE-3 reads from the registry
when the timeout fires but the last, and true on the last, so the shared process is closed once, when the
set is exhausted.
Withholding both the drain and the hard close on one number fails closed on both, keeps the runtime the
incoming session is about to be served by alive, and is discharged by the reclaimer the next paragraph
adds, which performs the close that its own registry-emptying deletion earns.
A registry-empty result implies no other session is active, because a session enters the active set only
through the `StartSession` that binds its registry entry, so the teardown arm is unaffected.

A registered-but-unbound entry must therefore have a reclaimer, and today it has none. `releaseSlot`
(`pkg/adapter/slotsession.go:102-112`) holds the only `delete(s.slots, slotID)`, and it is reached only
after the handler's bound test passes (`:70-74` returns `NotFound` first), while the whole-pod scrub
enumerates the on-disk children rather than the registry, so a recycle does not clear one either. An
ordinary bind failure produces the state: `materializeSlot` returns on a workspace-prep, setup,
credential-assignment, or `StartSession` failure (`pkg/gateway/podlifecycle/podsession/slotbinder.go:276-303`)
after `PrepareWorkspace` has already created the entry through `ensureSlotPaths`, and the only compensation
is `BindReservedSlot`'s `ReleaseSlotReservation` (`slotbinder.go:214`), which issues no adapter RPC and
decrements with `leaked` false, so the pod keeps serving sessions. Left unreclaimed, one such failure makes
the registry permanently non-empty, the drain gate and the teardown decision permanently false, the
§15.4.2 drain a no-op on that pod for the rest of its life, and the runtime process alive after its last
session ends, which is the outcome this paragraph exists to prevent, and it would be silent
because `drainViaLifecycle` swallows its send errors (`pkg/adapter/session.go:294-301`). It would also hold
§4.6.1's count above one forever, rejecting every unaddressed inbound frame from a Basic-level runtime on
that pod.

Two edits close it. On the adapter, the merged `ShutdownSlot` handler admits an entry that is registered
and not yet bound when the entry is the named session's: under D1 and D2 the registry is keyed on the
session identifier, so an unbound entry belongs unambiguously to the session that named it. The handler
deletes the entry and removes its tree, takes the same drain decision, calls `Runtime.Close` carrying that
same decision, and skips the `ReportSessionScrub` emission, because no session was ever started on it. The
close is the arm that discharges a hard close an earlier co-tenant's teardown withheld, because a deletion
that empties the registry ends the last reason to keep the shared process alive, and the conjunction stated
above makes the call safe: whenever some other session is still being served, `releaseActiveLocked` reports
the active set non-empty and `Close` returns early. Skipping the close on this arm instead would leave the
pod with a drained runtime that nothing ever closes. `p.connected` is written false only inside `Close`
(`socketruntime.go:449`), so once the runtime honours the `terminate` frame and exits, the spawned child is
never reaped and the next session's `Runtime.Start` returns at `:182-187` against a dead process without
spawning one, which is the silent failure the ordering rule above exists to prevent. That is the one
exception to
§4.11's `checkSessionBound`, and §4.11 states it beside the rule. On the gateway, each `materializeSlot`
failure branch sends a best-effort `ShutdownSlot` for the session on the connection it already holds,
before the `cl.Close()` it performs today, so the adapter-side entry is cleared on the same failure that
`ReleaseSlotReservation` clears the counter on. `ReleaseSlotReservation` keeps its present job, the counter
half of the release.

The close on this arm is recoverable only once `Close` stops closing the pod's runtime listener, so
SCHEMA-1 drops that statement with the same signature change. `Close` ends at `return p.listener.Close()`
(`socketruntime.go:466`), and the listener is bound once, in `NewSocketRuntimeProcess`
(`socketruntime.go:156-161`), whose only production caller is `cmd/lenny-adapter/main.go:354` at adapter
startup, and no path rebinds or replaces it. After a `Close` that reaches that statement, a later
`Runtime.Start` on the same pod does take the spawn branch, because `p.connected` is false, and then fails
in `accept` (`socketruntime.go:285`) with `net.ErrClosed`, so the pod cannot serve another session for the
rest of its life. It stays a placement candidate in that state: `ReleaseSlotReservation` decrements with
`leaked` false (`pkg/gateway/podlifecycle/podsession/slotbinder.go:498`) and the per-pod claim survives
while sibling slots remain (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:766-769`), so every session
placed on the pod afterwards fails at `Runtime.Start` and is released again. `Close` therefore closes the
shared connection and reaps the spawned child and returns, and the listener's lifetime becomes the adapter
process's: it is bound before the first session and released when the process exits. The change is not
scoped to the reclaim arm, because the same unrecoverable teardown sits under `onHoldTimeout`'s close and
under the last bound session's close on a recycling pool, both of which run on a pod that goes on to serve
another session. Nothing asserts the listener close today, since every case in
`pkg/adapter/socketruntime_test.go` binds its own process, so §8 states the one added assertion rather than
a rewrite.

This is a different predicate from §4.11's recycle-scrub guard, which
tests for a bound slot: a `ShutdownPod` arriving while any session is still bound is a gateway ordering error
rather than a race the adapter has to absorb. §8 pins both interleavings at tier 7a.

The gateway callers take the sequence the concurrent path already runs. `slotbinder.go:538` sends
`ShutdownSlot` for the ending session and `slotbinder.go:570` sends `ShutdownPod` on the occupancy-zero
recycle edge. `binder.shutdownAdapter` (`binder.go:1934-1947`), which today sends one `Shutdown` or
`ShutdownRecycle` for a base-mode release, sends `ShutdownSlot` for the ending session first and then
`ShutdownPod` on the recycle disposition, so the usage flush, the drain, the runtime close, and the
`ReportSessionScrub` emission keep running and the slot registry is empty when `ShutdownPod` arrives.
Without that ordering the base-mode recycle would lose its per-session teardown outright and, because
under D1 every session holds a bound registry slot, would trip the occupancy refusal below on every
release. §4.11's re-key of the recycle-scrub guard onto registry occupancy takes a
different form on each branch of §4.5(d). With the split landed the guard's present form is unconstructible,
because `ShutdownSlot` carries no recycle disposition to test and `ShutdownPod` carries no session: the
`ShutdownPod` handler scrubs on the carried disposition without a session test, and the occupancy test
becomes a refusal in that handler, so a `ShutdownPod` arriving while the registry still holds a bound slot
returns `FailedPrecondition` rather than scrubbing a pod that is still serving a session. If review declines
the split, `ShutdownRequest` survives and the re-keyed predicate is the whole of the guard: the recycle
branch dispatches `startPodScrub` when the registry holds no bound slot and takes the per-session path when
it does. The gateway callers split as stated above: `slotbinder.go:538` and `binder.go:1934-1947` call
`ShutdownSlot`, and `slotbinder.go:570` and `binder.go:1934-1947` call `ShutdownPod`, which is what
§4.5(g) rests on when it deletes the `SlotId` wrapper. §4.5(d) states what survives if review declines the
split.

Under the base case in §4.5(g) the `SlotId` wrapper message (`:587`) and its doc comment (`:584-586`) are
deleted, along with the per-field comments that referenced them. Under the alternative, where the shutdown
split does not land, `SlotId` is retained with one user and its doc comment is rewritten to state that the
field is the per-slot teardown discriminator on `ShutdownRequest`, with the `maxConcurrentSessions: 1`
condition dropped.

`NegotiateVersionResponse` takes the reporting change CODE-2 states. `workspace_root = 5` (`:1664-1671`) is
removed with `reserved 5;` and `reserved "workspace_root";`, and `string workspace_base = 6` is added with a
doc comment naming the `--workspace-base` value and stating that the gateway derives a session's root as
`<base>/slots/{sessionId}/current`. The field is replaced rather than re-pointed, because reusing the number
for a different meaning is the silent change of bytes §4.5(e) refuses.

Generated code under `pkg/proto/adapter/v1/` is regenerated rather than hand-edited.

### SCHEMA-2. State the JSONL population rule and rename the frame field

`schemas/lenny-adapter-jsonl.schema.json` drops the concurrency conditions at `:58, 139, 161, 190, 222` and
states §4.6.1's population rule on all six session-scoped frames. The field is renamed `slotId` to
`sessionId` on the five frames that carry it and added to the `status` frame (`:198-208`), which carries no
identifier today, per §4.6.1 and §4.6.2, and the `message` frame's descriptions distinguish `sessionId` (the addressed
session) from `from.id` (the sending session). `heartbeat` and `heartbeat_ack` are stated as outside the
addressing rule.

`schemas/examples/jsonl.set_tracing_context.json:8` takes the rename and a session-identifier example value.

### SPEC-1. State the rule where the conditional stood

The presence conditions at `spec/15_external-api-surface.md:1593`,
`spec/28_communication-channels.md:547-551, 566-571, 604, 632, 665, 689, 779-783, 795, 1682-1683, 1767`,
`spec/05_runtime-registry-and-pool-model.md:513`, and `spec/29_communication-scenarios.md:435` are
replaced by §4.2's value rule and §4.6.1's population rule. `spec/28:779-783` is the §28.5.3 `status` frame
block, which declares no identifier today and gains the `sessionId` property and the population and
resolve-or-reject rule with the schema change SCHEMA-2 stages, so the canonical frame block and the
published schema state the same frame. `spec/28:795` is the fifth per-frame schema
block, the `set_tracing_context` frame §4.8 rewrites, and `spec/28:1682-1683` is the §28.6 restatement that
a single-session pod's messages carry no identifier; both state the same condition as the four frame blocks
above them and are replaced with them. Under §4.6.2 the four §28.5.3 canonical schema blocks that declare
the key today (`spec/28:632` for `tool_result`, `:665` for `response`, `:689` for `tool_call`, and `:795`
for `set_tracing_context`) and the prose naming it at `:604` also take the rename to `sessionId`, alongside
the `message` example at `:600` SPEC-6 stages, so every §28.5.3 block spells the property
`schemas/lenny-adapter-jsonl.schema.json:139, 161, 190, 222` declares after SCHEMA-2 and §8's tier-11 frame
reconciliation passes on the specification's own text. If §4.6.2 is declined the key stays `slotId` at
those six lines and only the presence condition is replaced. `spec/28:1767` is the `CH-MSGSOCK` row of the §28.8 channel
failure-mode table, which states the multiplexing key as the `slotId` keying "by which a pod serving more
than one concurrent session multiplexes every slot's stream over the one channel". That is the same claim
`:1682-1683` makes in §28.6 prose, so the row is restated on `sessionId` with the concurrency condition
dropped: the pod multiplexes every session's stream over the one channel whatever its concurrency.
`spec/06_warm-pod-model.md:77` makes the same claim in the same words, inside the `maxConcurrentSessions >
1` row of the §6.1 `preConnect` compatibility table: "Concurrent sessions multiplex onto a single pod via
`slotId`." It is restated on the session identifier here, beside `:1767`, because after SCHEMA-2 the
published schema declares no `slotId` property at all and the sentence would name a wire key that no longer
exists. Only the wire-key noun is restated: the row's `maxConcurrentSessions > 1` condition and its
`preConnect` rejection are untouched, per §3. If §4.6.2 is declined the key stays `slotId` at this line and
at `:1767`, and both restatements reduce to dropping the concurrency framing.

Two `spec/29` sentences name the same wire key and take the same restatement, on the same ground that the
published schema declares no `slotId` property after SCHEMA-2. `spec/29:52` states the trace-labeling rule
as keyed to "the `slotId` by which that pod multiplexes every slot's stream over the one `CH-MSGSOCK`
channel", which is `spec/28:1767`'s claim in `spec/28:1767`'s words. It carries no `maxConcurrentSessions`
condition, so it is a rename site alone rather than a presence condition, and it is restated on the session
identifier by which the pod multiplexes every session's stream. `spec/29:435` states that the `CH-ATTACH`
envelope "carries `slotId` only on a pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1`", so it
takes both the presence replacement above and the rename, and is restated as the envelope carrying the
session identifier on every pod. If §4.6.2 is declined the key stays `slotId` at both lines, `:52` takes no
edit, and `:435` keeps only the presence replacement. The
`spec/10_gateway-internals.md:157` sentinel
sentence is outside this deliverable: it states a persistence scoping key rather than a wire-addressing
condition, and SPEC-9 stages it with the rest of §4.9's specification edits.

**The intra-pod MCP scoping sentences.** Five `spec/28` statements say what scopes the two intra-pod MCP
channels, and CODE-1's start-side merge makes each of them false rather than merely conditional. The
`CH-MCP-PLATFORM` card's Exclusivity bullet (`spec/28:1153-1156`) and the `CH-MCP-CONNECTOR` card's
(`:1198-1200`) state that the connection is scoped per session through the `mcpNonce` the adapter
regenerates for each session and rewrites into the manifest before each session's runtime start; the §28.6
restatement carries the same claim in the MCP clause of `:1683-1686`, which begins on the same line as the
`CH-MSGSOCK` clause the list above stages and runs past it; and the Exclusivity column of the §28.8
failure-mode table repeats it in both channel rows (`:1769`, `:1770`). CODE-1 starts the platform and
per-connector servers at most once per pod and has their providers resolve the calling session from the
adapter's slot registry at call time, so on a pod holding more than one bound session the live server carries
the nonce of whichever session started it while the manifest carries the latest session's, and no nonce
scopes a connection to a session. All five sites are restated together: the manifest nonce authenticates a
connection to the pod's intra-pod MCP servers, the servers are pod-wide and started at most once per pod, and
the calling session is resolved from the adapter's slot registry, with the call refused when the pod holds
other than one bound entry and refused permanently once the pod has held more than one bound entry at any
point in its life. Today the sentences are not false, because the concurrent path returns into
`startSessionSlot` before the manifest write and the MCP starts, so the channels do not exist on a concurrent
pod. Without the restatement the applied specification would state per-session scoping in the channel cards
`spec/04:709` directs a reader to for the intra-pod contracts, while the §29.10 bullet SPEC-5 adds states the
pod-wide surface, and the two would contradict each other.

`spec/07_session-lifecycle.md:333` is deconditioned here rather than deleted. The paragraph scopes both the
per-slot inbox and the unresolved-slot fail-closed invariant to `maxConcurrentSessions > 1`, and it is the
normative source `pkg/gateway/session/executor/pod.go:26-27` cites for `SLOT_ID_REQUIRED`. Both statements
become general: every session-mode pod routes through a per-slot inbox per §4.4, and a message that does
not resolve to a bound slot fails closed internally on every pod, stated on the non-empty session
identifier CODE-1 makes the gate require. SPEC-6 takes the `"slot_01"` ordinal inside the same paragraph.

The paragraph also names the wire key three times, and under §4.6.2 all three occurrences take the rename
here. The bold lead-in labels the paragraph "**Per-slot (`slotId`) routing when `maxConcurrentSessions >
1`:**", the body states that "The `slotId` field in the `MessageEnvelope` determines which slot's inbox
receives the message", and the example is "A message with `slotId: \"slot_01\"`". The lead-in is already
edited here for the concurrency condition, and its parenthetical takes the same key as the two sentences
beneath it, so the label reads `**Per-slot (`sessionId`) routing:**`. `MessageEnvelope` is the §15.4
unified format (`spec/15:1552`) whose property SCHEMA-2 renames at
`schemas/lenny-adapter-jsonl.schema.json:58`. Left standing, §7.2 would name a field on that envelope which
the published schema, §15.4, and §28.5.3 no longer declare, which is the same ground `spec/06:77`,
`spec/29:52`, and `spec/28:1767` are staged on. If §4.6.2 is declined the key stays `slotId` at all three
occurrences and only the ordinal and the concurrency conditions change.

The adapter populates the identifier on every session-scoped frame on every pod. A runtime-to-adapter frame
may omit it only on a pod holding one slot, where it resolves to the stream's binding. The field is renamed
per §4.6.2. `spec/28:604` carries three separable elements. This deliverable takes the presence condition,
and under §4.6.2 the key spelling as well, per the rename list above. The Basic-level permission that
sentence also states, and the second statement of it at `spec/28:588`, are stated by SPEC-7 with the
`spec/15:1783` row.

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

`spec/18_build-sequence.md:532` takes the same collapse. That line is the Phase 12c deliverable
"Concurrent session slots (`sessionPolicy.maxConcurrentSessions > 1`) ...: per-slot workspaces, the Redis
slot-counter capacity gate, and the `acknowledgeProcessLevelIsolation` requirement". After D7 and D1 the
per-slot tree is the only layout on every pod, so the Phase 4 workspace deliverables (`spec/18:232-235`, the
Upload Handler staging-to-promotion pipeline and the §14 workspace-plan source-type validators) and the
Phase 6 interactive-session deliverables (`spec/18:351-367`) all require it, and leaving it in Phase 12c
would make an earlier phase's deliverable depend on a later phase's artifact and would keep the concurrency
condition D1 retires inside the build sequence. The Phase 12c bullet keeps the Redis slot-counter capacity
gate and the `acknowledgeProcessLevelIsolation` requirement, and the uniform per-slot workspace layout moves
into the Phase 4 workspace-plan deliverable at `spec/18:232-235`, the first deliverable in the sequence that
materializes a session workspace. Phase 2's §18.6 deliverables (`spec/18:119-137`) name no workspace
materialization, so they are not the destination. Neither §8 sweep reaches the file: it carries no `/workspace/current` occurrence (its only
`/workspace` string is the REST route at `spec/18:229`) and no `/run/lenny/credentials.json`, so the line is
staged by name.

The placeholder token is renamed `{slotId}` to `{sessionId}` at every site §4.7 lists, with the rule stated
once at the §6.4 layout block, and the reason the container directory is not renamed to `sessions` is
recorded there.

§6.4's "`/workspace/shared/` population and enforcement" paragraph states that the gateway populates the
directory during pod initialization. The adapter does, at warm time, before READY and before any slot
exists (`pkg/adapter/warmlayout.go:127-152`, from the `--shared-assets-dir` and `--shared-assets` flags
rendered at `pkg/controller/sandbox/podspec/podspec.go:816-821`). The paragraph is corrected to name the
adapter, and its "when `maxConcurrentSessions > 1`" qualifier is dropped, because the directory is mounted
and populated on every pod. `spec/26_reference-runtime-catalog.md:44` states the same qualifier in the
reference catalog, that `/workspace/shared/` "is used only on pools with
`sessionPolicy.maxConcurrentSessions > 1`", and it is restated here in the same change: the directory is
mounted and populated on every pod, and coding-agent runtimes do not use it. Without that edit the applied
specification would contradict the rewritten §6.4 paragraph, and no sweep reaches the line, because the
`/workspace/current` sweep is keyed on that literal and the credential sweep on
`/run/lenny/credentials.json`. This divergence predates this proposal; it is corrected here because the same
paragraph is being rewritten for the uniform layout and leaving one half stale would be worse than either
state.

D9 removes `/workspace/current` from every pod, so every surviving reference to it becomes false and the
retirement inventory is a sweep rather than a list of the sites this proposal happened to reach. The
sweep's predicate is mechanical: every occurrence of the literal `/workspace/current` under `spec/`,
`docs/`, `schemas/`, `charts/`, `cmd/`, `sdks/`, and the served OpenAPI document is either qualified to the
slot path or restated so that it names no pod-global workspace. §8's tier-11 case asserts the sweep is
complete over that whole predicate, `spec/`, `docs/`, `schemas/`, `charts/`, `cmd/`, `sdks/`, and the served
OpenAPI document, rather than over a named subset. The four directories beyond `spec/`, `docs/`, and the
served document are where most of the surviving occurrences sit, including `cmd/lenny-adapter/main.go`'s
`--workspace-root` default, the scaffold templates, the reference runtimes under `cmd/runtimes/`,
`sdks/runtime/go/runtime/types.go`, `schemas/workspaceplan-v1.json`, and
`schemas/runtime-ops-events.schema.json`, so a gate written over the narrower set would pass with them
intact.

The sites this proposal has located, which the sweep must at minimum reach:

- `spec/04:239`, `:263`, and `:660`; `spec/05:256`; `spec/07:32, 37, 441, 443, 468`; `spec/08:791-792`;
  `spec/10:171`; `spec/13:749`; `spec/14:88, 89, 249`; `spec/15:2016` and `:2243-2244`; `spec/24:239`;
  `spec/26:40, 42, 45, 219, 384, 414, 445, 474`; `spec/28:1133`; and `spec/29:238, 834, 1094-1095`.
  `spec/04:660` and `spec/05:256` are normative adapter obligations rather than prose: the `FinalizeWorkspace`
  row materializes into the path, and the pre-materialization removal is the residual-state guarantee, which
  goes vacuous on a path that never exists. `spec/10:171`'s staging directory and atomic rename are stated
  per slot root.
- `spec/15:2243-2244`'s `CreateRequest.WorkspacePlan` doc comment states the pod-global path with the
  per-slot path as the conditional alternative, and it is mirrored verbatim in the shipped SDK at
  `sdks/runtime/go/runtime/types.go:206-209`. Both are restated on the slot path in the same edit, so the
  specification text and its SDK mirror do not diverge.
- All of `docs/runtime-author-guide/` (`lifecycle.md:37, 40, 79, 81, 83, 397`,
  `integration-levels.md:49`, `echo-runtime.md:257, 339`, and `sdk-examples/go.md:171, 256, 326-330`,
  `python.md:154, 217`, and `typescript.md:141, 171`), `docs/getting-started/architecture.md:204, 278, 531`
  and `concepts.md:65, 362, 364, 366, 398, 436`, `docs/reference/adapter-contract.md:60, 62, 133, 270, 609`,
  `docs/reference/glossary.md:430`, `docs/reference/state-machines.md:87`, `docs/api/rest.md:95`,
  `docs/tutorials/wrap-coding-agent-cli.md:24, 26`, and `docs/about/style-guide.md:75`.
- `docs/runtime-author-guide/lifecycle.md:85-97`, the "Filesystem Layout" block, which is the reader-facing
  mirror of the §6.4 layout this deliverable rewrites and which the sweep predicate does not reach: the
  block renders the path across the code block's indentation (`/workspace/` on one line, `current/` on the
  next), so no line in it contains the literal. It is staged by name and restated on the uniform per-slot
  layout: `/workspace/slots/{sessionId}/current` and `/workspace/slots/{sessionId}/staging`, the pod-global
  `/workspace/staging` D8 retains, `/sessions/{sessionId}`, `/artifacts/{sessionId}`, and no `current` leaf
  under `/workspace`. Without it the page would document, as the runtime's working directory, a path D9
  removes from every pod, contradicting both the rewritten §6.4 and the page's own restated steps at
  `:79-83`.
- The served §15.1 OpenAPI document (`pkg/gateway/externalapi/openapi/openapi.json:218, :861`), which is
  the client-facing contract SDK generators read (`pkg/gateway/externalapi/openapi/openapi.go:3-7`).
- The `lenny-ctl` runtime scaffold templates, which would otherwise emit runtimes writing to a path that no
  longer exists: `cmd/lenny-ctl/runtimescaffold/templates/go-coding.main.go.tmpl:5, 42`,
  `typescript-coding.main.ts.tmpl:6`, `runtime-coding.yaml.tmpl:4`, and
  `python-coding.main.py.tmpl`.
- The schema and proto surfaces, staged by SCHEMA-1 and SCHEMA-2 rather than here:
  `schemas/workspaceplan-v1.json:5, 64, 88, 123`, `schemas/runtime-ops-events.schema.json:166`,
  `schemas/examples/jsonl.tool_call.json:6`, and the `schemas/lenny-adapter.proto` RPC and field doc
  comments at `:43, 146, 179, 704-706, 715, 735, 835, 1464, 1467, 1612`, including `ExportPaths` at `:179`,
  which is the RPC CODE-2 re-points onto the slot root.

### SPEC-4. Unify the credential path

`spec/06_warm-pod-model.md:26` and `:28` merge into one per-slot credential-lease paragraph.

The merged paragraph drops one clause `:28` carries today rather than deconditioning it. `:28` states that
rotation is independent per slot in that "the in-flight gate and `credentials_rotated` acknowledgment apply
to the individual slot being rotated rather than to the pod as a whole", and CODE-4 records that only half
of that is delivered: the credential file and the `cred:{leaseId}` acknowledgement wait are per session,
while `awaitInflightGate` polls a pod-global per-provider counter and the 300-second ceiling is pod-scoped
with it. The clause is therefore restated on what holds, that the credential file and the acknowledgement
wait are per session while the in-flight gate and its ceiling remain pod-wide per provider (§4.7), so a
co-tenant's outstanding request for the same provider gates a sibling session's rotation. `:28`'s adjacent
"other slots' LLM requests are unaffected by a rotation on a sibling slot" sentence is restated on the same
ground, since a sibling's requests continue to run against their own unrewritten credential file while the
gate they share can delay the rotation. This keeps the applied specification, §9's recorded limit, and §8's
tier-9 case stating one rule; `spec/06:28` is the only spec statement of a per-slot in-flight gate
(`spec/04_system-components.md:826` states the counter per provider), so no other sentence covers for it.

`spec/13_security-model.md:26`'s fsGroup delivery paragraph and `spec/04` §4.7 item 4 name the per-slot
path. `spec/13:30` takes the placeholder rename and one added sentence recording that a rotation rewrites
the addressed session's own credential file rather than a shared one, which is CODE-4's fix. Its
cross-slot readability statement, its "Lenny does not mitigate this at the pod level" sentence, and its
requirement that deployers needing strict credential-lease isolation set
`sessionPolicy.maxConcurrentSessions: 1` are kept verbatim. That requirement rests on the pod holding at
most one credential lease at a time, and this proposal changes neither the lease count nor the
`lenny-cred-readers` group ownership that makes every slot's file readable by every slot's agent process
(§1.3, §5). Restating it on the ground that the isolation now holds per slot would weaken a mandatory
control on a property the same paragraph denies.

The merge makes `/run/lenny/slots/{sessionId}/credentials.json` the only credential path on every pod, so
the pod-global `/run/lenny/credentials.json` is retired exactly as `/workspace/current` is under D9, and
every surface naming it is restated on the per-slot path. Two of those surfaces are behavioral rather than
descriptive, and leaving them would repeat the §1.2 defect this proposal exists to close:
`spec/05_runtime-registry-and-pool-model.md:459` is the recycle scrub's step 0, which removes the credential
file before any deployer code runs, and `:469` is step 6, which marks the scrub failed if the file still
exists. Both are re-stated over the per-slot credential files, so the purge and its verification cover the
files that exist rather than one that never does. `spec/05:453`'s recycle-lifecycle summary of step 0 takes
the same restatement.

The workspace half of the same procedure is restated in the same edit, because D9 leaves it naming a
directory no pod carries. `spec/05:465` is step 2, "Remove the workspace directory (`rm -rf
$WORKSPACE_DIR`)", and it is re-stated as the removal of every per-slot workspace tree under
`/workspace/slots/`. That line names neither sweep literal, so it is staged explicitly or nothing reaches it.
`spec/05:469` is step 6, whose "stat-checking the workspace path" clause is re-stated over the same set
alongside the credential clause above; the `/run/lenny/credentials.json` sweep already covers that line's
credential clause, and the workspace clause is what the sweep does not cover. The `cleanupCommands` phase is
stated to run with the workspace base as its working directory, which is the value CODE-1's `CleanupDir`
carries, so the specification fixes the directory the deployer's commands observe rather than leaving the
adapter to pick one.

`spec/05:453`'s parenthetical that `cleanupCommands` execute "with access to session state but without the
previous session's credential file" is restated in the same edit, because the ordering SCHEMA-1 states makes
its first half false. Every ending session's `ShutdownSlot` precedes the pod's `ShutdownPod`, and
`ShutdownSlot` removes that session's per-slot tree through `releaseSlot` and `RemoveTree`
(`pkg/adapter/slotsession.go:86, :102-112`; `pkg/adapter/slotlayout/tree.go:50-57`), so no released session's
workspace, session, artifact, or credential tree is on disk when the scrub's `cleanupCommands` run. The
restatement is that the commands execute in the pod's workspace base, after every ended session's per-slot
tree and credential lease have been removed, with the pod-wide directories the deployer's own
`setupCommands` created still present. The removal at session end is kept rather than deferred to the
scrub's step 2, because deferring it would leave an ended session's credential lease and workspace readable
by a co-tenant and by the deployer's cleanup code, which is the exposure step 0 exists to prevent.
`spec/05:461`'s ordering of the phase is unchanged.

The runtime-facing contract moves with them. `spec/28_communication-channels.md:1048` describes the
`credentials_rotated` frame's `credentialsPath` as "path to updated `/run/lenny/credentials.json`", and
`schemas/examples/runtime-ops.credentials_rotated.json:4` carries that literal as its example value; both
name the per-slot path. `spec/28:1064` states the same rewrite obligation on the adapter in the frame's
timing bullet and takes the per-slot path with them, and `schemas/lenny-adapter.proto:996`'s
`AssignCredentialsRequest.slot_id` doc comment, which states the per-slot file as the conditional
alternative to the pod-global one, is rewritten with the field removal SCHEMA-1 stages.

The remaining descriptive sites are restated in the same sweep, whose scope for this literal is `spec/`,
`docs/`, and `schemas/`: `spec/04:792, 903, 1158`, `spec/13:28`, `spec/15:2174, 2198, 2211, 2252`,
`spec/17:54`, `spec/24:247`, `spec/29:260`, `docs/operator-guide/configuration.md:395`,
`docs/operator-guide/security.md:197`, `docs/runtime-author-guide/lifecycle.md:282`,
`docs/getting-started/concepts.md:584`,
`docs/runbooks/ephemeral-container-cred-guard-unavailable.md:32`, and the comment at
`charts/lenny/templates/admission-policies/ephemeral-container-cred-guard-webhook.yaml:5`. The webhook's
own conditions are keyed on the `/run/lenny` mount prefix and the credential volume name rather than on the
file path, so its behavior is unchanged and only the comment is corrected.

The runtime SDKs and the reference runtimes carry the same literal as a construction-time default
(`sdks/runtime/go/runtime/runtime.go:129` and `options.go:104`,
`sdks/runtime/python/lenny_runtime/runtime.py:58`, `sdks/runtime/typescript/src/runtime.ts:48`, the three
`*-chat` scaffold templates under `cmd/lenny-ctl/runtimescaffold/templates/`, and
`cmd/lenny-compliance/full.go:373`). Retiring the pod-global path leaves those defaults naming a file that
exists on no pod, so the runtime-facing half of the credential contract moves with the storage layout rather
than being excluded.

A construction-time default cannot name a session-scoped file, and no existing surface delivers the path to
a runtime at every integration level. The `credentials_rotated` frame carries a `credentialsPath`
(`pkg/adapter/runtimeops.go:462-468`), but it is a `CH-RUNTIMEOPS` frame that `spec/15:1778` restricts to
the Full level, and no runtime SDK reads the member: the Go SDK's `handleCredentialsRotated`
(`sdks/runtime/go/runtime/lifecycle.go:246-256`) decodes `type`, `provider`, and `leaseId` and then calls
`reloadCredentials`, which re-reads the configured path (`runtime.go:592-596`, `:577`). All three SDKs read
the credential file unconditionally at startup, for every level
(`sdks/runtime/go/runtime/runtime.go:224-230`, `lenny_runtime/runtime.py:547`,
`sdks/runtime/typescript/src/runtime.ts:513`).

The resolved path is therefore delivered on the §4.7 adapter manifest, which is the existing per-session
runtime-facing surface the adapter already writes before spawning the runtime, rather than on a new frame,
field, or environment variable. The manifest field set at `spec/04:767-792` gains a required
`credentialsPath` row naming the session's `/run/lenny/slots/{sessionId}/credentials.json`, and the
Basic-level reading requirement at `spec/04:796` is restated: a Basic-level runtime that reads a credential
file reads the manifest for its path. The gateway-rendered pod carries no credential-path environment
variable to use instead (`pkg/controller/sandbox/podspec/podspec.go:614` renders the only `LENNY_`-prefixed
variable, `LENNY_ADAPTER_SOCKET`; the builder's other rendered variable is the Downward API `POD_NAME` at
`:913-914`, and neither names a credential path), and a rendered variable could not name a session-scoped
path on a warm pod in any case.
CODE-4 stages the adapter's manifest field (`pkg/adapter/manifest.go:106-157`), the three runtime SDKs'
resolution of the credential path from the manifest, the scaffold templates, and
`cmd/lenny-compliance/full.go:373`. The manifest keeps the single pod-global file and the collision §9
records for its `sessionId` and `mcpNonce`, which the new row shares.

### SPEC-5. Rescope §29.10

`spec/29_communication-scenarios.md` §29.10 is split per §4.4. Its addressing mechanisms move to the owning
sections and its co-tenancy analysis stays, retitled to name the condition it actually depends on.

The retitle is stated here so that the heading and its slug are not invented at application time. The
heading at `spec/29:1417`, `### 29.10 The concurrent-session pod`, becomes
`### 29.10 Co-tenancy on a concurrent-session pod`, whose slug is
`#2910-co-tenancy-on-a-concurrent-session-pod`. Two inbound references take the new text and the new
fragment in the same change: the table-of-contents entry at `spec/README.md:290`, whose link text and
fragment both name the retired heading, and the prose reference at `spec/29:17`
("§29.10, the structural analysis of the concurrent-session pod"). Those two are the only references to the
retired slug in the tree, so no anchor-redirect entry is required; `tests/tier0_static/fragment_link_test.go`
resolves every intra-repo fragment link under `spec/` and `docs/` against the headings that exist, and it
stays green only if both are edited with the heading.

The reduced §29.10 carries a successor pointer, which is a separate obligation from the anchor-redirect
entry the preceding paragraph rules out. `spec/28_communication-channels.md:62-63` (N8) states that a
section that gives up content carries a permanent sentence naming the heading that now owns the content and
the identifiers that moved, and it serves a reader arriving by a route no tool rewrites, such as a section
number quoted in a proposal or a review. §29.10 gives up the addressing mechanisms §4.4 names, so the
retitled subsection opens with this sentence: "The addressing mechanisms this subsection previously stated
apply to a pod of either concurrency and are now owned by the sections that state them:
[§28.5.3](28_communication-channels.md#2853-intra-pod) carries `CH-MSGSOCK`, its addressing key, and its
buffering and replay policy; [§6.4](06_warm-pod-model.md#64-pod-filesystem-layout) carries the per-slot
workspace subtree; [§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) and
[§4.9](04_system-components.md#49-credential-leasing-service) carry the per-slot credential lease;
[§6.2](06_warm-pod-model.md#62-pod-state-machine) carries the slot's lifecycle state;
[§7.2](07_session-lifecycle.md#72-interactive-session-model) carries the per-slot inbox and the delivery-path
evaluation; and [§4.7](04_system-components.md#47-runtime-adapter) carries checkpoint admission and the
operation lock." Each clause names the heading that owns the mechanism, which for the workspace subtree is
the paragraph SPEC-3 rewrites, for the credential lease the paragraphs SPEC-4 rewrites, and for the inbox
the paragraph SPEC-1 deconditions; checkpoint admission and the operation lock keep the owners the reduced
bullets already cite. The `CH-MSGSOCK` clause puts a markdown link into the owning card
and a `CH-` identifier on one line, which is what
`tests/tier11_docs/successor_pointer_test.go` requires of a pointer (`:74-76`, `:110-133`).

The slot-lifecycle clause of that sentence needs its destination deconditioned, and the edit is staged here
because no other deliverable reaches §6.2. `spec/06_warm-pod-model.md:148-154` states the per-slot
sub-state machine (`slot_assigned`, `receiving_uploads`, `running`, `slot_cleanup`, `released`, `leaked`,
and `failed`) inside the block headed "Concurrent occupancy (`sessionPolicy.maxConcurrentSessions > 1` ...)"
at `:129-130`. Under D1 every session holds a slot on every pod, so the five sub-states that describe one
slot's own progress are lifted out of that block and stated for a pod of either concurrency:
`slot_assigned`, `receiving_uploads`, `running`, `slot_cleanup`, and `released`, which is the
`slot_assigned ──→ receiving_uploads`, `receiving_uploads ──→ running`, `running ──→ slot_cleanup`, and
`slot_cleanup ──→ released` edges at `:149-151` and `:153`. Without the lift the successor pointer would
assert that
§6.2 states the slot's lifecycle for a pod of either concurrency while §6.2 scopes it to a concurrent pod,
and the applied specification would contradict both D1 and §4.3. The multi-slot occupancy edges at
`:131-146`, which are statements about a pod holding more than one slot at once, keep the concurrency
condition.

The `running ──→ failed` edge at `:152` and the `slot_cleanup ──→ leaked` edge at `:154` stay inside the
concurrent-occupancy block with them, and this is the narrower of the two ways to make §6.2 consistent. Their
normative semantics are not in the lifted block: the **`leaked` slot semantics** paragraph at
`spec/06_warm-pod-model.md:159` defines both states in terms of mechanisms an exclusive pod does not have,
stating that a leaked slot "remains counted in the pod's Redis slot-counter occupancy" and counts with
`failed` toward the `ceil(maxConcurrentSessions/2)` unhealthy threshold that drives the pod to `draining`,
and `spec/05:538` heads the owning rule "Slot failure and cleanup (`maxConcurrentSessions > 1`)" with the
same threshold at `:557`. An exclusive-pool bind reserves no slot and keeps an empty `BindResult.SlotID`
under CODE-2, so it has no counter entry, and `ceil(1/2)` is 1, which would drive every exclusive pod to
`draining` on one leak, a pod-lifecycle behavior this proposal neither states nor tests and which both
drain-request producers refuse to perform for an exclusive pool
(`pkg/gateway/session/recycle/scrubreporter_seams.go:373`,
`pkg/gateway/podlifecycle/podsession/slotbinder.go:579-581`). Lifting the two edges would therefore oblige
this proposal to restate `spec/06:159` and `spec/05:538-557` on a per-pod-class accounting it does not
change. What D1 generalizes is that every session occupies a slot and progresses through that slot's own
states; the failure accounting stays where the counter it reads is defined. `spec/06:159` takes no edit,
and the reader-facing mirror keeps its `claimed -> draining` threshold row at
`docs/reference/state-machines.md:236` inside the concurrency-scoped section for the same reason. The `slotId` spellings inside the lifted block are restated on the session identifier the
runtime is dispatched with, per D2 and §4.7: the sub-states are tracked per session, and the dispatch edge
at `:150` names the session identifier. `docs/reference/state-machines.md:228-240` is the reader-facing
mirror of the same machine, carrying the heading "Concurrent-session occupancy
(`maxConcurrentSessions > 1`)" and the per-slot sub-state line at `:240`; it takes the same split in the
same change, so the mirror does not state a condition the specification has dropped. Neither §8 sweep
reaches either site, because the sweeps are keyed on the `/workspace/current` and
`/run/lenny/credentials.json` literals, so both are staged here by name.

The gate reads a hand-maintained domain rather than deriving which sections were reduced, so the domain is
extended in the same change. `reducedSections` (`:52-55`) names `spec/04` §4.7 and `spec/15` §15.4 today,
and the row `{"spec/29_communication-scenarios.md", "29.10", "28.5"}` is added, so a later deletion of the
pointer fails the case rather than passing unnoticed.

The retained "Shared by the whole pod" list (`spec/29:1491-1502`) gains one bullet, per §4.4: the intra-pod
MCP surface is one platform socket and one per-connector socket set for the pod, served by providers that
resolve the calling session from the adapter's slot registry, refuse the call when the pod holds other
than one bound entry, and refuse it permanently once the pod has held more than one bound entry at any point
in its life. Without the bullet the list would omit a shared surface CODE-1's start-side merge
brings onto every pod.

Four of the five unstated gaps stay with the co-tenancy half, per §4.4. The fifth, the
`CH-MSGSOCK` buffering and replay gap at `spec/29:1564-1565`, is stated for a pod of either kind and moves
with the addressing mechanisms to the section that owns `CH-MSGSOCK`.

### SPEC-6. Correct the examples and the order rules

`spec/28_communication-channels.md:600`, `docs/reference/adapter-contract.md:316`, and
`schemas/examples/jsonl.set_tracing_context.json:8` use a session identifier rather than `"slot_01"` as the
example value. Under §4.6.2 the key at those three lines is also renamed `sessionId`, so the §28.5.3
`message` example, the adapter-contract `set_tracing_context` example, and the schema example spell the key
the published schema declares after SCHEMA-2; SCHEMA-2 already states the rename for
`schemas/examples/jsonl.set_tracing_context.json:8`, and this deliverable states it for the two
specification and documentation examples. If §4.6.2 is declined the key stays `slotId` at all three lines
and only the value changes. At
`spec/15_external-api-surface.md:1569` the example field is renamed `sessionId` and carries a
session-identifier value, matching the schema rename SCHEMA-2 stages, the code rename CODE-6 stages, and
the envelope field description SPEC-7 states. `spec/15:2283`'s §15.7 `Message` doc comment enumerates the
envelope's fields and names `slotId` among them; the field is renamed there together with the SDK type it
mirrors at `sdks/runtime/go/runtime/types.go:71`, so the §15.7 reference block does not name a field the
envelope no longer carries. The `"slot_01"` ordinal inside
`spec/07_session-lifecycle.md:333` is replaced; the same paragraph's concurrency conditions are
deconditioned by SPEC-1. The four `"slotId": null` examples at
`docs/reference/adapter-contract.md:140, 180, 232, 271`, which fail the published schema, are corrected to
a session-identifier string; §4.6.2(i) stages the key rename at those same four lines, so under §4.6.2 the
examples read `sessionId` and under a declined §4.6.2 they read `slotId` with a valid value.

The behavioral sentences keyed on identifier order take §4.10's justification: `spec/04:691`, `spec/05:542`,
`spec/29:1481`, and the comments at `pkg/adapter/oplock.go:48, :73, :160` and `pkg/adapter/checkpoint.go:107`.

The operation lock's admission rule and its three restatements take §4.10's re-key: `spec/04:691`,
`spec/28:291-294`, `spec/28:1651-1658`, and `spec/29:805-806` state admission and coalescing per distinct
session identifier and drop the single-session clause. At `spec/04:691` the re-key covers the **Queue
depth** bullet's admission and coalescing clauses; the same line's identifier-order clause is staged in the
paragraph above, and the bullet's `maxConcurrentSessions > 1` condition goes with the single-session
sentence.

One further sentence describes that same rule from the outside and takes the same re-key. `spec/29:1550`,
inside the §29.10 `Interrupt` and drain-barrier gap bullet that §4.4 keeps in the co-tenancy half, opens by
asserting that "the specification qualifies checkpoint admission by `slotId`" and cites §4.7 and §28.6 for
it. After the re-key neither section carries that qualification, so the sentence is restated as "The
specification qualifies checkpoint admission by the session identifier and states that the lock serializes
`Interrupt` across the pod's slots". The gap the bullet records is unchanged: the specification still states
no slot qualification for the `Interrupt` RPC the lock admits or for the drain barrier `CH-BARRIER` carries.

### SPEC-7. Declare message scope and state the addressing rule

`spec/04` §4.1 gains the classification table in §4.1 of this proposal, under choice (a) as the base case.
Under that base case `spec/04:676`'s §4.7 RPC table row, which names "`Terminate` (proto `Shutdown`)" and
describes the recycle disposition on that one request, splits into a `ShutdownSlot` row and a `ShutdownPod`
row carrying the recycle disposition. `spec/05:457`'s sentence-run in the §5.2 Lenny scrub procedure
splits the same way rather than taking a name substitution, because it both names the RPC that triggers the
whole-pod scrub and states that on the recycle disposition "the adapter closes only the ending session's
runtime". A rename alone would leave §5.2 asserting that the pod-scoped request closes a session's runtime,
which `ShutdownPod` cannot do: it carries no `session_id` and performs no per-session work. The restatement
is that the gateway sends `ShutdownSlot` for the ending session, on which the adapter closes that session's
runtime and runs the rest of the per-session teardown SCHEMA-1 lands there, and then sends `ShutdownPod`
carrying the recycle disposition, the pod identity (`podId`), and the pool's scrub parameters
(`cleanupCommands` and `cleanupTimeoutSeconds`), on which the adapter keeps the pod process alive across the
recycle boundary, runs the scrub asynchronously, and reports the outcome for `podId` via `ReportPodScrub` on
its GatewayControl link. That ordering is the caller ordering SCHEMA-1 states for
`binder.shutdownAdapter`. `spec/29:690-696`, the §29.4 step-12 restatement of that same
§4.7 row, states that one request carries both dispositions and that on the recycle disposition "the
request also carries the pod identity and the whole-pod scrub parameters". After the split no single
request carries both, so the step splits the same way: `ShutdownSlot` is the graceful end-of-session
shutdown of the ending session's runtime, and `ShutdownPod` carries the recycle disposition, the pod
identity, and the scrub parameters. `spec/05:459` is the credential step 0 SPEC-4 stages and is
not touched here.

Three further specification sentences name the retired RPC and are staged here under the same base case.
`spec/11_policy-and-controls.md:263` is step 2 of the §11.4 full-revoke propagation list, which sends a
`Terminate` RPC to the pod for each session in the user's task tree; it names `ShutdownSlot` with reason
`USER_REVOKED`, because the operation ends one session rather than the pod. `spec/11:270` is the §11.4
propagation note, which names the same RPC three times, once for the synchronous local send with a
hyperlink to §4.7, once for the bounded-call statement, and once for the step-2 request the gateway
publishes on Redis pub/sub; all three name `ShutdownSlot`, so the §4.7 link resolves to a declared row.
`spec/07_session-lifecycle.md:49` is step 21 of the §7.1 lifecycle sequence, `Gateway → Pod: Terminate`,
which becomes `Gateway → Pod: ShutdownSlot`. If review declines the split, all six sentences stand
unchanged.

`spec/28_communication-channels.md:809-829` and `:548` take the §28.5.3 rewrite in §4.8.
`spec/15_external-api-surface.md:1593`'s envelope field description distinguishes the addressed session
from `from.id`. That sentence is the one SPEC-1 also names as a presence condition; SPEC-1 removes the
condition and this deliverable writes the replacement description, so the two do not stage competing
rewrites of the same line.

`spec/15:1783`'s Basic-level row is rewritten to except the per-session identifier from
the envelope fields a Basic-level runtime may ignore, per §4.6.1: such a runtime echoes the identifier on
the frames it emits in response, on every pod. That row is staged here whether or not §4.6.2 lands, because
§4.6.1's population rule is what obliges it; the field it names is `sessionId` under §4.6.2 and `slotId`
without it.

The §15.4.6 conformance battery gains the matching Basic-level category row, staged here for the same
reason: `spec/15:2058-2065` is the table that says what each level's checks assert, and an obligation with
no row there is an obligation the suite certifying a third-party runtime never tests. The new row states
that a Basic-level runtime echoes the per-session identifier it was handed on the frames it emits in
response, which is the check CODE-6 adds to `cmd/lenny-compliance`.

The same permission is stated twice more on the wire leg, and both statements take the same exception here
rather than being left to contradict the rewritten row. `spec/28:588` lists the envelope fields a
Basic-level runtime "can safely ignore" and names the identifier among them, and `spec/28:604` restates the
permission as "Ignore all other fields" beside the presence condition SPEC-1 removes. Both are rewritten to
except the per-session identifier and to state the echo obligation. `docs/runtime-author-guide/integration-levels.md:23`
carries the same row in the reader-facing matrix and takes the same exception, and
`docs/reference/adapter-contract.md:158` states the permission a fourth time, directly under the `message`
frame's field table: "Read only `type`, `id`, and `input`. Ignore all other fields safely." That sentence
takes the same exception, so the page documenting the frames states the echo obligation beside the field
row at `:156` that CODE-6 and SCHEMA-2 rename. No literal sweep reaches it, because the
`/workspace/current` and `/run/lenny/credentials.json` sweeps are keyed on those two paths.

`spec/05_runtime-registry-and-pool-model.md:558` states the client-visible slot-exhaustion error, whose
`error.slotId` field CODE-7 replaces in the emitted body. The sentence is restated on `error.sessionId`
identifying the session whose slot failed, with no separate slot key, which is the body CODE-7 emits on both
routes that reach the mapper.

The statement that a client addresses a session rather than a slot needs no new sentence and none is staged
here. `spec/07_session-lifecycle.md:333` already carries it: "clients do not address slots directly, so the
slot is resolved from the addressed session before delivery." SPEC-1 deconditions that paragraph rather than
deleting it, so the statement survives and generalizes to every pod.
`spec/15_external-api-surface.md` states no equivalent sentence today, and this deliverable adds none.

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

The nine code comments §1.6 records are corrected here as well, because the misattribution is one error with
a specification half and a code half and correcting only one half leaves the other asserting the opposite.
Two of them cite `spec/06:391` verbatim as their spec basis and take the rewritten sentence:
`pkg/adapter/slot.go:72-73` on `ensureSlotStateLocked` and `pkg/adapter/slotlayout/tree.go:20-21` on
`EnsureTree`. Four restate the attribution in their own words and are rewritten to name the gateway as the
minter and the adapter as the creator of the tree: `pkg/adapter/slotlayout/tree.go:11` ("the §6.4 adapter
responsibility requires on slot assignment"), `pkg/adapter/slotlayout/slotlayout.go:8` ("an isolated tree
the adapter creates on slot assignment"), `pkg/adapter/server.go:363` ("Populated when a slot bind assigns
a slot"), and `cmd/runtimes/echo-concurrent/main.go:9` ("The adapter assigns a slotId per slot"). Three
more state the misattribution in the exact words "adapter-assigned" and are rewritten the same way:
`pkg/gateway/runtime/adapterclient/client.go:820` ("adapter-assigned §6.4 slotId"),
`pkg/gateway/session/executor/subprocess.go:376` ("SlotID is the adapter-assigned §6.4 slot"), and
`pkg/gateway/session/executor/pod.go:149` ("carries the adapter-assigned slot"). Two of those three are in
files CODE-1 already edits, so the correction lands beside the code change rather than leaving the comment
asserting the opposite of the rewritten specification. None of
the nine breaks the build when left stale, which is why they are staged by name rather than left to the
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

These are every `slot_id` occurrence in §10.1, so the section names the column nowhere after this
deliverable applies. `tests/tier11_docs/checkpoint_pipeline_consistency_test.go` requires §10.1 to name
every non-infrastructure column migration 0178 creates, and MIG-1's drop leaves that migration's `.up.sql`
text unchanged, so the gate takes the hand-rewrite §8 states in the same change.

`spec/10_gateway-internals.md:58`, the hold-state timeout bullet, takes one further edit here, outside
§10.1 and staged in this deliverable for the same reason the `spec/16` rows below are: it is the
specification half of CODE-3, and an edit staged inside a code deliverable would never be written. The
bullet keeps `AdapterTerminating(session_id, reason: coordinator_lost)` and the immediate-transition
consequence, and gains the concurrent case CODE-3 newly reaches: a hold
timeout on a pod holding more than one bound session terminates each of them and sends one
`AdapterTerminating(session_id, reason: coordinator_lost)` per terminated session, so the gateway performs
the immediate transition for each. The `spec/04:704` event-catalog row and the reader-facing row at
`docs/reference/adapter-contract.md:97` state the same contract and stay true, so neither takes an edit here.

`spec/12_storage-architecture.md` §12.5 is the second normative statement of the same two rules and takes
the same re-key, because leaving it would put the retention arithmetic on a key the index no longer
carries. Four sites: `:337`, the checkpoint-retention bullet stating that the GC job and retention policy
"operate on `(session_id, slot_id)` pairs" and that a pod with `maxConcurrentSessions: 8` may retain up to
16 checkpoint objects; `:340`, the partial-manifest bullet stating that the gateway supersedes any prior
active partial manifest "for the same `(session_id, slot_id)`"; `:352`, the per-pod object-count arithmetic
deriving `maxConcurrentSessions × 2` from the per-slot rule; and `:362`, the rotation-versus-TTL guard's
parenthetical "per-slot when `maxConcurrentSessions > 1`". All four state the rule on `session_id` alone.
The retained-object arithmetic follows from concurrency rather than from the scoping key: a pod running
`maxConcurrentSessions` sessions still holds up to `maxConcurrentSessions × 2` checkpoint objects, because
each of those sessions retains two, so `:352` keeps its figure and states the reason as the pod's session
count rather than as a per-slot key.

`spec/05_runtime-registry-and-pool-model.md:542` is the third normative statement of the key, in the
per-slot checkpoint-cap rule: the cap is selected from `last_checkpoint_workspace_bytes` "for the
`(session_id, slot_id)` pair in Postgres". The parenthetical is restated as
`last_checkpoint_workspace_bytes` for the session in Postgres, which is where the column lives
(`migrations/0087_sessions_retry_policy.up.sql:22`). The cap-summing arithmetic in the same bullet keeps its
figures and states them on the pod's session count, the way `spec/12:352` is restated below. SPEC-6 takes
the upload-serialization sentence on the same line for §4.10's justification, so the two edits land in one
change rather than as competing rewrites.

`spec/16_observability.md:198` restates the supersede condition on the two-column key, twice in the same
metric row for `lenny_checkpoint_partial_manifests_superseded_total`. The row is restated on `(session_id)`
so the metric inventory does not name a column the migration drops. `docs/reference/metrics.md` carries no
mirrored statement of that condition and needs no edit for it.

`spec/16` §16.1 also takes the two rows CODE-6's counter needs, staged here rather than inside CODE-6
because the pipeline lands every staged specification edit before the code phase and then blocks `spec/`
writes, so a row staged inside a code deliverable would never be written and
`tests/tier11_docs/adapter_metric_catalog_test.go` would fail on a registered counter that reaches neither
catalog. The first is a new §16.1 catalog row for `lenny_adapter_unaddressed_frame_rejected_total`, a
counter labeled by `frame_type` that increments once per Attach stream that rejects a session-scoped
§28.5.3 frame carrying no per-session identifier on a pod holding more than one slot, which is the
granularity §4.6.1 states because the rejection is taken in each stream's demultiplexer, carrying the note
its sibling adapter rows carry
that the metric is emitted by the adapter process inside the agent pod and is outside the §16.9 default
scrape target set until a deployer wires an adapter scrape target. The second restates `spec/16:188`, the
`lenny_adapter_set_tracing_context_dropped_total` row, because §4.8's rewrite changes what "does not address
the Attach stream that delivered it" means. CODE-6 stages the counter's registration and the
`docs/reference/metrics.md` mirror of both rows.

### CODE-1. One address, one session check, and no presence branch, in both gRPC directions

`pkg/adapter/slot.go`: `useSlot` is deleted, every root-computing site resolves through `slotlayout.Resolve`,
and the pod-global fallback in `workspaceRootForSlot` (`:123-133`) is reduced to the unknown-slot error
branch. Each session-scoped handler validates through `slotlayout.ValidateSlotID` before resolving a root
and returns `InvalidArgument` on empty. The adapter-side consumers listed in §4.5(e) re-point onto
`session_id`.

`checkSession` and `checkSlotSession` collapse into `checkSessionBound(sessionID)` per §4.11.
`Server.sessionID` is retired, `claimSession` and `claimSessionForConfigure` become slot-registry claims,
and Shutdown's recycle-scrub guard is re-keyed on registry occupancy. The five uncaught call sites in §4.11
are edited explicitly rather than left to the compiler.

**The two production readers of `s.currentSession()` outside the session check.** Retiring the field
without re-pointing them would silently empty two session-stamped outputs, and neither break is
compiler-caught, because both read through the accessor rather than the field. `CurrentSessionID`
(`pkg/adapter/adapterevents.go:175-181`) is the token sink's session source: `WireDirectModeUsage` hands it
to `NewSessionTokenSink` (`pkg/adapter/usage.go:247`), and the sink folds every session-less
`llm_request_completed` token count under whatever it returns (`usage.go:206-215`), which is §11.2 budget
and billing input. `emitControlEvent` (`adapterevents.go:149-152`) stamps the `sessionId` of every
`CH-ADAPTEREVENTS` envelope that carries no identifier of its own. That set is every emitter that
constructs a `controlEvent` without a `SessionID`: RATE_LIMITED (`:187`), AUTH_EXPIRED (`:193`),
PROVIDER_UNAVAILABLE (`:198`), LEASE_REJECTED (`:204`), ADAPTER_TERMINATING (`:217`), FINAL_USAGE_REPORT
(`:224`), and `EmitCheckpointBarrierAck` (`pkg/adapter/coordination.go:292-298`). Both re-point onto the
same resolution the MCP providers take
below, the pod's single bound registry entry: the fold and the stamp name that session when the registry
holds exactly one bound entry, and the token frame is dropped and the event is emitted with an empty
`sessionId` when it holds zero or more than one. The drop matches what the meter does on an idle pod
today, where `Add` returns on an empty identifier (`usage.go:118-121`).

One of the seven emitters already holds the session at the call site, and stamps the envelope explicitly
rather than leaving it to the resolution. `emitFinalUsage(ctx, sessionID)` computes one session's totals
and emits them through `EmitFinalUsageReport` (`pkg/adapter/session.go:333`, `:341`), and the gateway
waits for that terminal report before running `budget_return.lua` (§8.3), so an unattributable one on a
concurrent pod loses a session's final token and wall-clock totals. `EmitFinalUsageReport` therefore takes
the session identifier its caller already has. The per-member hold termination CODE-3 stages is the same
case, and `EmitAdapterTerminating` takes the session identifier its loop holds for the reason CODE-3
states. Attributing either
of the two resolved outputs to an arbitrary co-tenant is what the
resolution refuses, since the runtime frame carries no session and one runtime process serves every slot.
§9 records the resulting limit for a pod holding more than one bound session, and §8 pins both outputs.

**The start-side merge, the counterpart of SCHEMA-1's teardown merge.** SCHEMA-1 moves the whole per-session
teardown onto `ShutdownSlot` so a session's end is one sequence whatever the pool's concurrency. The start
takes the same treatment, and without it D1 deletes work no other deliverable replaces. Today
`StartSession` returns early into `startSessionSlot` (`pkg/adapter/session.go:103-106`), whose whole body is
the registry claim and `Runtime.Start` (`pkg/adapter/slotsession.go:27-52`), while the base path below it
resolves the session's connectors, writes the §15.4 adapter manifest, and starts the platform and connector
MCP servers on the freshly generated §15.4.3 nonce (connectors at `session.go:126`, the manifest write at
`session.go:128-143`, the MCP starts at `session.go:149-159`, and `manifest.go:239-260`). §4.5(b)
removes `StartSessionRequest.slot_id`, which is that early return's discriminator, so every session would
take the slot path and no session on any pod would receive a manifest. A Basic-level runtime would lose the
session metadata and the adapter-local tool descriptors it reads at startup, and a Standard- or Full-level
runtime would lose the nonce without which the adapter rejects its intra-pod MCP connection
(`manifest.go:118-125`).

The two branches therefore merge into one `StartSession` body: the registry claim and `Runtime.Start` the
slot path performs, plus the connector resolution, the manifest write, and the MCP server start the base
path performs. The merged body keeps the `RuntimeKind != RuntimeKindMCP` guard around the platform and
connector MCP starts (`pkg/adapter/session.go:149`), because a `type: mcp` runtime connects to no platform
MCP server.

**The manifest's `llm` object moves onto the slot's lease set.** The manifest the merged body writes derives
its `llm` object from `s.manifestLLM()` (`pkg/adapter/manifest.go:259`, declared at `:311-320`), which reads
the pod-global `s.credLeases` and returns nil when that map is empty. The only non-dead writer of
`s.credLeases` is the pod-global `AssignCredentials` branch at `pkg/adapter/credentials.go:97`, which the
`useSlot` deletion makes unreachable, so without this clause every session on every pod would receive a
manifest whose `llm` field serializes as null. That field set is required at all integration levels
(`spec/04:789`, the `llm.deliveryMode` row), and in proxy mode it carries the `baseUrl`, `dialect`, and
`apiKeyEnv` that point the runtime's SDK at the §4.9 LLM proxy, so dropping it would silently move every
proxy-mode session off the mediated path. `manifestLLM` therefore takes the session identifier and derives
the object from that slot's own lease set (`st.creds`, `pkg/adapter/slotcreds.go:47`), which is where every
assignment lands once §4.5(b) removes `AssignCredentialsRequest.slot_id` and §4.5(e) re-points
`pkg/adapter/credentials.go:74` onto `session_id`. `Server.credLeases` (`pkg/adapter/server.go:353-355`) and
`cloneCredentialLeases` (`pkg/adapter/credentials.go:377-383`) are retired with it, alongside the pod-global
expiry path that is their other reader and writer (`pkg/adapter/credexpiry.go:160-172`), which goes dead with
the pod-global assign, rotate, and revoke branches. `pkg/adapter/manifest_fields_test.go:99, :120` sets
`srv.credLeases` directly and is a hand-rewrite onto the slot's lease set, and §8 pins the resulting manifest
field.

The MCP servers are started at most once per pod rather than once per session, and every caller that starts
them guards them on `s.mcpCancel == nil`. The platform server binds `s.MCPSocket`, the single socket the controller
renders for the whole pod (`pkg/adapter/platformmcp.go:16-26`;
`pkg/controller/sandbox/podspec/podspec.go:184, :835` rendering `--mcp-socket=@lenny-platform-mcp`), and
`listenIntraPodMCP` calls `net.Listen("unix", socket)` with no unlink (`pkg/adapter/connectormcp.go:99-102`),
so a second bind on the same pod fails with `EADDRINUSE`. Without the guard the second concurrent
`StartSession` would call `s.releaseSession()` and return `Internal: start platform MCP server`
(`session.go:149-157`), never reaching `Runtime.Start`, which would cap a pod configured for
`maxConcurrentSessions: N` at one live session, abort after the manifest write has already replaced the
co-tenant's §15.4.3 nonce, and tear down the co-tenant's servers through the single pod-global `mcpCancel`
and `connectorCancels` (`session.go:384-408`). The per-connector servers derive their sockets from the same
pod-global base (`connectormcp.go:121-127`) and take the same treatment.

The guard covers all three production callers rather than the merged `StartSession` body alone.
`startPlatformMCP` is called from `pkg/adapter/session.go:150` on `StartSession`,
`pkg/adapter/resume.go:119` on `Resume`, and `pkg/adapter/sdkwarm.go:240` on `ConfigureWorkspace`, each
paired with a `startConnectorMCPServers` call (`session.go:158`, `resume.go:124`, `sdkwarm.go:245`) and each
followed on failure by the pod-wide `s.releaseSession()` (`session.go:151`, `resume.go:120`,
`sdkwarm.go:241`). The resume caller is newly reachable a second time: `Resume` claims through the same
`claimSession` (`resume.go:47`) whose different-session refusal §4.11 retires on a pod-warm pod, so without
the guard a resume
for a second session on a concurrent pod would fail the bind with `EADDRINUSE` and cancel the co-tenant's
platform and per-connector servers through the single pod-global `mcpCancel`. The manifest rewrite is not
among the consequences the guard prevents. The guard is scoped to the server start, and `Resume` writes the
pod-global manifest unconditionally at `resume.go:106`, before it reaches `startPlatformMCP` at
`resume.go:119`, so the resuming session's `sessionId` and `mcpNonce` replace the incumbent's whether or not
the guard is present. That rewrite is the collision §9 records as a limit. `ConfigureWorkspace` keeps its different-session refusal
per §4.11 and so has no second-session path, and it takes the guard on the same terms because its `fresh`
branch and a concurrent `StartSession` or `Resume` on the same pod are otherwise two unsynchronized binds of
one socket. §10 names `resume.go` and `sdkwarm.go` beside `platformmcp.go` and `connectormcp.go` for this
deliverable, and §8 pins a second `Resume` on a pod already holding a bound session.

The shared server is armed with the
nonce of the session whose `StartSession` started it, which is the nonce the pod-global manifest carries at
that moment; §9 records the resulting collision.

The once-per-pod decision is taken inside a single critical section under `s.mu`, the way SCHEMA-1 states
the drain gate, rather than as a read of `s.mcpCancel` followed by a bind. Today nothing holds a lock
across the start: `startPlatformMCP` binds the socket first (`pkg/adapter/platformmcp.go:24`) and writes
`s.mcpCancel` only after `Serve` is launched (`:45-49`), and `claimSession` (`pkg/adapter/session.go:116-119`)
and the slot-registry claim (`pkg/adapter/slotsession.go:39-43`) each take and release `s.mu` internally. A
plain `s.mcpCancel == nil` check would therefore let two concurrent `StartSession` calls both observe nil,
both call `net.Listen` on the one socket, and hand the loser the `EADDRINUSE` failure the guard exists to
prevent. The merged body claims the start under `s.mu` before it binds, so exactly one call performs the
bind and the other proceeds to `Runtime.Start` with the servers already accounted for. §8 pins both
interleavings under `-race`.

**Which session the shared MCP surface acts as.** The platform and connector providers bind one session
identifier at server-start time (`pkg/adapter/platformmcp.go:39-43` constructing `platformToolProvider`,
`pkg/adapter/platformtoolprovider.go:29-38`, and `pkg/adapter/connectormcp.go:85` constructing
`connectorToolProvider`), and every `tools/list` and `tools/call` on those sockets is forwarded to the
gateway under it. The gateway then installs that session's user and tenant as the authenticated principal
for the call (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:114-121`). One runtime process
serves every slot, so a captured identifier on a pod holding more than one bound session would execute a
co-tenant's platform and connector tool calls under the first session's user. Tenant pinning does not bound
that: `spec/05:440` pins a concurrent pod to one tenant rather than to one user. The surface is absent
today only because the concurrent path returns into `startSessionSlot` before the manifest write and the MCP
starts, so the merge would create it.

The providers therefore resolve the calling session at call time from the slot registry rather than
carrying a captured identifier, which is also what replaces the retired `Server.sessionID` (D13) as their
source. The resolution is the pod's single bound session: when the registry holds exactly one bound entry
the call dispatches under that session, and when it holds zero or more than one the provider refuses the
`tools/list` or `tools/call` with `FailedPrecondition` and forwards nothing. This predicate reads the
registry's bound entries, which is a different quantity from the all-entries count SCHEMA-1's drain gate
and §4.6.1's inbound rule read. A registered-but-unbound entry carries no session identifier, so there is
no session to dispatch the call under, and counting it here would refuse a call the pod's one bound session
is entitled to make while its co-tenant's workspace is being prepared. Resolving per
connection is not available, because the one runtime process holds the connection for every slot, and
naming the session on the MCP call itself is a new runtime-facing protocol surface this proposal rules out
with the per-session socket and per-session manifest below.

The live occupancy count is not the whole predicate, because a caller can age out of it. One runtime process
serves every slot and a per-session `Runtime.Close` does not stop the ended session's agent code inside it:
`SocketRuntimeProcess.Close` returns before touching the connection or the child process on a non-last call
(`pkg/adapter/socketruntime.go:440-445`), and SCHEMA-1 keeps the MCP servers up whenever a co-tenant is still
bound. On a pod holding sessions for two different users, the moment the first session's `ShutdownSlot`
completes the registry holds exactly one bound entry, the occupancy refusal stops firing, and a `tools/call`
still issued by the ended session's agent code resolves to the survivor and is forwarded, at which point the
gateway installs the survivor's user and tenant as the authenticated principal
(`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:114-123`;
`pkg/gateway/connectors/connectortools/connectortools.go:166-176`). Tenant pinning does not bound it and
`acknowledgeProcessLevelIsolation` does not cover it: `spec/05:515` enumerates the shared process namespace,
`/tmp`, cgroup memory, network stack, and credential-file group-read access, and names no tool-invocation
authority.

The refusal is therefore sticky at the pod. Once the adapter's slot registry has held more than one bound
entry at any point in the pod's life, a pod-level flag is set and stays set, and every subsequent
`tools/list` and `tools/call` on that pod's platform and connector sockets is refused with
`FailedPrecondition` whatever the current bound count. On a pod that has only ever held one bound session at
a time, which is every pod on a `maxConcurrentSessions: 1` pool and a concurrent pod that never actually
multiplexed, the flag is never set and the call dispatches under that session. Refusing on the pool's
ceiling instead is unavailable, because §4.11 establishes that the adapter is never told
`maxConcurrentSessions`; the flag needs no configuration and no protocol surface, it is the fail-closed
branch §4.11 takes everywhere else, and it keeps the posture a concurrent pod has today, where a Standard- or
Full-level runtime cannot use the intra-pod surface at all. The flag is set on the same registry write that
binds the second entry, under `s.mu`, so it is not itself a read-then-act.

Because the servers and the handshake signal are pod-wide,
`ShutdownSlot` cancels them only when the departing session's release leaves the pod with no other bound
session, and §4.11 and §8 state the teardown on that predicate rather than per session. A per-session MCP
surface would need a per-session socket path and per-session manifest fields, which CODE-1 rules out of
scope with the manifest path. The manifest keeps the path and contents it has today. It stays a single pod-global
`adapter-manifest.json` in `ManifestDir` (`pkg/adapter/server.go:113-116`), because that path is a fixed
runtime-facing contract (`spec/04:713`, `spec/15:1734`) that the SDKs, the scaffold templates, and the
compliance suite read, and because one runtime process serves the whole pod. Making it per-slot is a
runtime-facing path change with its own blast radius and is out of scope here; §9 records the limit that a
second concurrent `StartSession` on the same pod rewrites the single manifest's `sessionId` and `mcpNonce`.
That collision is what the concurrent path avoids today by writing no manifest at all, which leaves
Standard- and Full-level runtimes unusable on a concurrent pod either way. The merge preserves the
base-mode contract exactly, and on a pod that has ever held more than one bound session it adds a reachable
socket that refuses every call, so no call executes under a session other than the one that made it.

**The whole-pod scrub, per SPEC-4 and D7.** SPEC-4 re-states the recycle scrub's step 0 and step 6 over the
per-slot credential files, and D7 makes the per-slot workspace tree the only layout. The code that performs
the purge takes the same change, or the re-stated specification would describe a scrub that touches nothing.
`scrubConfig` (`pkg/adapter/podscrub.go:104-110`) today configures the scrub from two pod-global paths:
`CredentialFile` is `filepath.Join(s.CredentialsDir, credfile.FileName)` (`:116-121`), the retired
`/run/lenny/credentials.json`, and `WorkspaceDir` is `s.WorkspaceRoot`, the retired `/workspace/current`.
Both name a path that never exists after application, so step 0 would purge nothing, step 2 would remove
nothing, and step 6 would verify both absent and pass, while the per-slot trees under `/workspace/slots/`
and `/run/lenny/slots/` that hold a leaked session's workspace and credential lease survive the recycle into
the next session. That is the cross-session residue the scrub exists to prevent.

`scrub.Config`'s `CredentialFile` and `WorkspaceDir` become the plural `CredentialFiles` and
`WorkspaceDirs`, and `pkg/adapter/scrub/scrub.go` takes each step over the set: the step 0 purge
(`:230-231`), the step 2 workspace removal (`:246-247`), and the step 6 verification (`:363` for the
workspaces and `:372-379` for the credential files), which marks the scrub failed when any member survives.
`scrubConfig` enumerates the set from the on-disk children of
`/workspace/slots` and `/run/lenny/slots` rather than from the live registry, because the residue the scrub
must reach belongs to a leaked slot whose registry entry is already gone. `credentialFilePath` is retired
with the pod-global path. `pkg/adapter/podscrub_test.go:492-495`, which asserts the two pod-global values,
is a hand-rewrite.

The scrub package's own tier-1 suite takes the change with it. `pkg/adapter/scrub/scrub_test.go` constructs
`Config` with the two renamed fields at eleven sites (`:102-103`, `:126-128`, `:168`, `:187`, `:211`, `:242`,
`:349-350`, and `:457`), so the package stops compiling the moment this deliverable lands and none of §8's
new tier-1 cases in it can run. The edit is not mechanical, because three cases are the existing pins of the
steps this deliverable re-bases onto the per-slot sets. `TestRun_DirtyWorkspaceFailsVerify_spec_5_2_436`
(`:165`) and `TestRun_CredentialStillPresentFailsVerify_spec_5_2_436` (`:182`) are the fail-closed step-6
pins over a single path that becomes a set, and each is re-based over a two-member set with the fail-closed
arm asserting that one surviving member fails step 6.
`TestRun_CredentialPurgedBeforeCleanup_integration_spec_5_2_424` (`:448`) is the step-0-before-`cleanupCommands`
pin over a single credential file and is re-based over the per-slot set. `TestCleanupEnv_spec_5_2_424`
(`:423-424`) calls `CleanupEnv("/workspace/current", ...)`, the retired pod-global value, and moves onto the
new singular `CleanupDir` carrying the workspace base, which is the value §8's fourth new case asserts the
deployer's commands observe as `PWD`. The file is a hand-rewrite in §8 and §10.

`WorkspaceDir` has two further consumers that the pluralization cannot take over, and both sit in the
`cleanupCommands` phase rather than in a scrub step: it is the working directory the deployer's commands
execute in (`workspace.RunSetup(ctx, cfg.WorkspaceDir, ...)`, `pkg/adapter/scrub/scrub.go:299`) and the
source of the `PWD` entry in their environment (`CleanupEnv(cfg.WorkspaceDir, ...)` at `:307`, reaching
`workspace.DefaultSetupEnv(workdir)` at `pkg/adapter/workspace/setup.go:101-102`). Each requires exactly one
directory, and CODE-2 retires `Server.WorkspaceRoot`, the only value `scrubConfig` has to give them
(`pkg/adapter/podscrub.go:104-110`). `scrub.Config` therefore gains a singular `CleanupDir` alongside the
two plural members, `RunSetup` and `CleanupEnv` take it in place of `WorkspaceDir`, and `scrubConfig` sets
it to the workspace base the adapter runs with (`/workspace` under the rendered `--workspace-base`). The
base is chosen because it is the one workspace path that exists on every pod after D9 and it is the parent
of every per-slot tree, so the deployer's commands run in a directory the pod always carries rather than in
one an ended session owned. It does not preserve the "with access to session state" access `spec/05:453`
states today: the ordering SCHEMA-1 fixes puts every ending session's `ShutdownSlot`, and therefore
`releaseSlot`'s `RemoveTree` (`pkg/adapter/slotsession.go:86, :102-112`), before the `ShutdownPod` the scrub
runs on, so no released session's per-slot tree survives to the `cleanupCommands` phase on any pod. SPEC-4
restates that parenthetical for the new ordering rather than leaving the specification asserting an access
the code does not give. The residue the enumeration above reaches is a leaked slot's, whose `ShutdownSlot`
never arrived. The
`WorkspaceDir` doc comment (`pkg/adapter/scrub/scrub.go:133-136`) names only the step 2 removal and the step
6 verification, which is why the field reads as scrub-only; the two new comments state each member's whole
use. SPEC-4 carries the matching §5.2 restatement of step 2 and of step 6's workspace half.

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

`pkg/gateway/runtime/adapterclient/client.go`: the paired `X`/`XSlot` methods collapse to one method each
for every RPC in §4.5(b), the eight `if slotID != ""` sites those methods carry (`:147, 205, 289, 364, 399,
428, 845, 861`) are removed, and `sendUpload`'s `slot *adapterv1.SlotId` parameter (`:308`) goes with them.

The ninth site, `:900`, is carved out of that instruction and routed to §4.5(d), because it is the
population path for the one field whose presence is a discriminator rather than a duplicate. It sits inside
`shutdown` (`:896-902`), which `Shutdown` (`:885`) and `ShutdownSlot` (`:892`) both call. The file declares
four producers of `ShutdownRequest`, and the base case gives each one a fate. `shutdown` and its
`if slotID != ""` are deleted with `ShutdownRequest`. `Shutdown` and `ShutdownSlot` collapse with
`Terminate` (`:923-932`) into a single `ShutdownSlot(ctx, sessionID, reason string, deadline time.Duration)`
method over the `ShutdownSlot` RPC, which is what the one-method-per-RPC instruction above requires: all
three send the same request, and `Terminate` differs only in populating `reason` and `deadline_ms`, the two
fields SCHEMA-1 keeps on `ShutdownSlot`. Retaining a second Go method over the same RPC would carry a
duplicate implementation of one concern. The two callers that send no reason
(`pkg/gateway/podlifecycle/podsession/binder.go:1945` and `slotbinder.go:538`) pass an empty reason and a
zero deadline, which is the request `shutdown` sends today; `cmd/lenny-gateway/user_revocation.go:129`
passes the `USER_REVOKED` reason and `userTerminateDeadline` it passes today. `Terminate`'s doc comment
(`:910-922`), which states that the §4.7 table names the RPC `Terminate` while the wire contract carries it
as `Shutdown`, is rewritten to name `ShutdownSlot` on both sides, because SPEC-7 makes the mapping it
records false. `ShutdownRecycle` (`:969-981`) re-points onto the `ShutdownPod` RPC and drops its
`sessionID` parameter, which `ShutdownPod` carries no field for; its callers
(`binder.go:1939` and `slotbinder.go:570`) send it after the `ShutdownSlot` for the ending session, per the
ordering SCHEMA-1 states. If review declines the split, `:900` and all four producers are retained
unchanged, because removing the site would leave a per-slot teardown indistinguishable from a whole-pod
shutdown on the wire and would silently convert every teardown into a pod shutdown.
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
slot root using `session_id` (`pkg/adapter/exportpaths.go:39, 48`). `ConfigureWorkspace` resolves no root
inside the adapter and needs none: the handler reads the working directory off the request
(`pkg/adapter/sdkwarm.go:204`) and passes it to the pre-connected SDK (`:249`). Its root arrives on the wire
from `pkg/gateway/podlifecycle/podsession/binder.go:989`, which is another consumer of the negotiated root
and takes the same per-session derivation as the persist site below. The §7.3
step (d) guard compares against the slot root (`pkg/adapter/resume.go:61`), which makes the assertion
load-bearing for the first time.

The recorded workspace root moves with the layout. `sessions.workspace_root`
(`migrations/0089_sessions_workspace_root.up.sql:10`) stores the absolute path the adapter reported, the
gateway replays it as `ExpectedWorkspaceRoot` (`pkg/gateway/sessionserver/start.go:3917`), and the adapter
rejects a session whose stored root does not match the one it holds (`pkg/adapter/resume.go:61-67`). Under
the uniform layout a row written before the change records `/workspace/current` while the pod now reports a
slot path, so every such session fails to resume. Two constants carry the stale default, and neither can be
re-pointed, because each is a package-level string and the value replacing it is per-session:
`DefaultExpectedWorkspaceRoot` (`pkg/gateway/sessionserver/lifecycle.go:38`) and
`archive.DefaultWorkspaceRoot` (`pkg/upload/archive/archive.go:39`) are deleted and their call sites compute
a slot root, as the containment paragraph below states. The platform is pre-deployment and
carries no sessions written under the old layout, so the migration rewrites the column rather than teaching
the guard a second accepted value; a compatibility branch here would be a shim for a population that does
not exist. The stored checkpoint bytes need no migration: `ArchiveTree` writes entries under the relative
prefixes `workspace/` and `sessions/` (`pkg/adapter/workspace/tree.go:41-42, 52-55`), so a checkpoint tar is
layout-independent and replays into whatever root receives it.

**The argv the operator renders.** The workspace root the adapter runs with is not a default the adapter
picks. `pkg/controller/sandbox/podspec/podspec.go` hard-codes `"--workspace-root=" + workspaceMount +
"/current"` into the sidecar-adapter argv (`:568`) and the embedded-adapter argv (`:669`), and the `// spec:
§6.4` comment above each (`:562-567`, `:663-668`) states the single-session `/workspace/current` cwd as the
contract and defers the per-slot tree to a later change of this builder. D7 and D9 are that change, so both
argv sites and both comments are staged here. The builder renders the workspace base
(`--workspace-base=<workspaceMount>`) rather than a `current` leaf, the two comments are rewritten to state
the uniform per-slot layout, and `cmd/lenny-adapter/main.go` takes the base directly rather than deriving it
from the parent of `--workspace-root` (`:117, :265, :272`).

The embedded argv is parsed by a different binary from the sidecar argv, so the flag replacement moves with
it. `embeddedArgs` becomes the `runtime` container's `Args` (`podspec.go:678-683`), so the binary that parses
it is the runtime image's own, and the in-tree embedded-model runtimes are
`cmd/runtimes/echo-embedded/main.go` and `cmd/runtimes/preconnect-echo/main.go`, the only two binaries
outside `cmd/lenny-adapter` that construct an `adapter.New` server. Each declares `--workspace-root`
(`echo-embedded/main.go:77`, `preconnect-echo/main.go:56`) and calls `flag.Parse()`, which exits on an
undeclared flag, a hazard `echo-embedded/main.go:90-94` already records for the platform-MCP flags. Both
therefore replace that flag with `--workspace-base` and set `adapterSrv.WorkspaceBase` in place of the
retired `adapterSrv.WorkspaceRoot` (`echo-embedded/main.go:111`, `preconnect-echo/main.go:88`), and their
argv-passing tests move with them (`echo-embedded/main_test.go:34`, `preconnect-echo/main_test.go:34`).
Leaving them on `--workspace-root` would crash every embedded-model pod at container start, and the
compiler would not catch it: the only build break in those two files is the `adapterSrv.WorkspaceRoot`
assignment, which an implementer can repair while keeping the old flag declared.

`Server.WorkspaceRoot` is retired with
`/workspace/current`, the way `Server.sessionID` is retired with `useSlot` under CODE-1: every root a
handler resolves comes from `slotlayout.Resolve` against the base and the session identifier. The tier-11
sweep in §8 does not reach these sites, because its predicate is the literal `/workspace/current` over
`spec/`, `docs/`, `schemas/`, `charts/`, `cmd/`, and `sdks/`, and the builder is under `pkg/`; §8 pins them
with a tier-2 podspec case instead.

**What the adapter reports, and what the gateway derives.** Retiring `Server.WorkspaceRoot` retires the only
producer of the value the column stores. `NegotiateVersion` fills `NegotiateVersionResponse.workspace_root`
from that field (`pkg/adapter/server.go:389-397`), the gateway captures it into `negotiated`
(`pkg/gateway/podlifecycle/podsession/binder.go:1117`, `:1707`), propagates it onto every `BindResult` and
`PrepareResult` (`binder.go:808, :939, :1034, :1615`), copies it from the `PrepareResult` onto the launch
result (`pkg/gateway/sessionserver/start.go:2608`), and persists it at two sites:
`pkg/gateway/sessionserver/start.go:2854` on the launch path and
`pkg/gateway/sessionserver/finalize.go:363` on the finalize path, whose doc comment (`:345-346`) records
that the persist moved there because the prepare phase now runs there.
`persistWorkspaceRoot` writes only a non-empty value and
`pkg/adapter/resume.go:61` compares only a non-empty expectation, so leaving the field unproduced would skip
the §7.3 step (d) assertion for every session, which is the vacuity §1.2 records and this deliverable
closes. The field also cannot carry the new value: `NegotiateVersion` is pod-scoped in §4.1 and runs at
handshake time before any slot exists, so it can report no session's root.

The adapter therefore reports the workspace base and the gateway derives the session's root from it.
SCHEMA-1 reserves `workspace_root = 5` on `NegotiateVersionResponse` and adds
`string workspace_base = 6` carrying the `--workspace-base` value, rather than re-pointing field 5 at a
different meaning, which §4.5(e) refuses. The propagated member carries one meaning on every path: the
`WorkspaceRoot` member on `negotiated`, `BindResult`, and `PrepareResult` is renamed `WorkspaceBase` and
holds the reported base verbatim on the base-mode path (`binder.go:781, :808, :939, :1034, :1615`) and on
the concurrent slot path alike, and no bind path puts an already-derived root on it. Overloading one member
with the base on one path and the derived root on the other would make the single persist site derive twice
on the concurrent path and write `<base>/slots/{sid}/current/slots/{sid}/current`, which the §7.3 step (d)
guard at `pkg/adapter/resume.go:61` then rejects with `FailedPrecondition`.

The gateway computes `<base>/slots/{sessionId}/current` at two sites. The first is
`persistWorkspaceRoot` (`pkg/gateway/sessionserver/start.go:3271-3284`), whose fourth parameter becomes the
workspace base and which derives the session's root from that base and the `sessionID` it already takes
before it writes the column. Both of its call sites (`pkg/gateway/sessionserver/start.go:2854` on the launch
path and `pkg/gateway/sessionserver/finalize.go:363` on the finalize path) therefore pass the base and take
the derivation by construction, which is what keeps the two paths from disagreeing on what the column holds.
The second is the `ConfigureWorkspace` call that carries the root to a pre-connected SDK as the `cwd`
(`pkg/gateway/podlifecycle/podsession/binder.go:989`), whose value never reaches the column and so needs its
own derivation. The resume is not a derivation site. `start.go:3917`
replays the persisted column verbatim (`ExpectedWorkspaceRoot: row.WorkspaceRoot`, inside the
`s.podBinder.Resume` argument list opened at `:3898`) and no workspace base is in scope there, because the
replacement pod's base arrives from its own `NegotiateVersion` inside `Binder.Resume`
(`pkg/gateway/podlifecycle/podsession/binder.go:1581`). Deriving the expectation there from the replacement
pod's own base and the session identifier would compare a value against itself and reinstate the vacuity
§1.2 records as its fourth defect, since the adapter's step (d) comparison resolves the same path through
`slotlayout.Resolve` against its base and the request's session identifier. The replay stays verbatim, which
is what makes the comparison load-bearing (a value the gateway recorded from the original pod against the
replacement pod's own resolved slot root) and what §8's tier-2 negative case and MIG-1's rewrite of
`sessions.workspace_root` rest on. With the derivation inside `persistWorkspaceRoot`, `row.WorkspaceRoot`
already holds the slot path, so the replay needs no edit. The derivation sits in the callee rather than at
each call site because `persistWorkspaceRoot` is first-non-empty-wins
(`start.go:3275-3277`): a call site left passing an underived base would fix the column at `/workspace`
whenever that site ran first, and the step (d) guard would then reject every resume with
`FailedPrecondition`, which is the outcome this deliverable exists to prevent. The adapter's step (d) comparison resolves the same path through
`slotlayout.Resolve` against its base and the request's session identifier rather than reading a retired
pod-global field.

The concurrent bind reports nothing today, so it takes the other half of the reporting chain here. On a
`maxConcurrentSessions > 1` pool the bind never runs through `binder.go`'s `negotiated` capture:
`bindReservedSlot` and `connectSlot` call `NegotiateVersion` and read only `resp.GetIncompatible()`
(`pkg/gateway/podlifecycle/podsession/slotbinder.go:239-249` and `:461-470`), and the `BindResult`
`materializeSlot` returns carries no workspace member at all (`slotbinder.go:319-334`). `finalize.go` cannot
supply one either, because it returns before the prepare and persist for a concurrent pool
(`pkg/gateway/sessionserver/finalize.go:238-240`). `persistWorkspaceRoot` returns early on an empty value
(`start.go:3272-3273`) and `pkg/adapter/resume.go:61` compares only a non-empty expectation, so without this
clause a concurrent-pool session persists nothing at start and skips the step (d) assertion at resume, which
leaves the guard vacuous on exactly the pods §1.2 names. Both slot-side handshake sites therefore capture
`NegotiateVersionResponse.workspace_base` and `materializeSlot`'s `BindResult` carries that base on the
same `WorkspaceBase` member the base-mode `BindResult` uses, so `publishBinding`'s single
`persistWorkspaceRoot` call (`start.go:2854`) derives the slot root once for both bind paths. The slot path
performs no derivation of its own; deriving there as well would hand the persist site a root it would
derive from a second time. §10 lists `slotbinder.go` under the propagation entry and §8 adds a tier-2 case
on a `maxConcurrentSessions > 1` pool.

`ArchivePolicy.workspace_root` is defaulted to the pod-global `/workspace/current` on every bind, which is
wrong today rather than only under this proposal: a concurrent pod has no such path, and §1.2's own subject
is a value that means one thing and is populated as another. It is defaulted to the bind's slot root at all
three sites that compute it. `pkg/gateway/podlifecycle/podsession/slotbinder.go:269-271` is the concurrent
bind. `binder.go:892-895` is the base-mode `Binder.Prepare` path, whose §13.4 containment root is
`firstNonEmpty(req.ArchivePolicy.GetWorkspaceRoot(), neg.WorkspaceRoot, archive.DefaultWorkspaceRoot)`;
after this deliverable both fallbacks name a path no pod carries, so every base-mode upload would
canonicalize symlink targets against a nonexistent root while the session's files land under its slot tree.
`binder.go:1342-1343` carries the same fallback inside `rewriteExtractedSources`. `archive.DefaultWorkspaceRoot`
(`pkg/upload/archive/archive.go:39`) is deleted rather than re-pointed, because it is a package-level
constant and the replacement value is per-session: each of the three sites computes the bind's slot root
from the workspace base and the session identifier instead. `DefaultExpectedWorkspaceRoot`
(`pkg/gateway/sessionserver/lifecycle.go:38`) goes the same way, since the expectation the gateway replays
is now derived per session.

**The gateway half of the resume, handed here by proposal 0070.** The adapter changes above are half of a
resume: they let the adapter restore into the right tree once it is told which session. The gateway must
supply one. `Binder.Resume` returns a `BindResult` carrying neither `SlotID` nor `MaxConcurrentSessions`
(`pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`), because `connect` issues a whole-pod
`podclaim.ClaimRequest` that reserves no slot (`binder.go:1652-1670`,
`pkg/gateway/podlifecycle/podclaim/claimer.go:63-73`, whose field set is `Pool`, `SessionID`, and
`TenantID` and carries no slot field). Two consequences follow, and this
proposal owns both. A resumed session on a concurrent pool holds a whole-pod claim and no slot, so it has
no slot root to restore into whatever the adapter is told. And the §7.2 fail-closed gate
(`pkg/gateway/session/executor/pod.go:146`) evaluates `0 > 1` on that path and never fires, so the
unresolved-slot invariant it exists to enforce is unenforced exactly where a resumed session would breach
it.

The resume therefore reserves a slot on the pools the start path reserves one on, those whose
`sessionPolicy.maxConcurrentSessions` is greater than one. The returned
`BindResult` carries that slot together with the concurrency of the pod it holds, resolved from the
`PoolMatch` the caller already has at `pkg/gateway/sessionserver/start.go:3876` and normalized to a minimum
of 1. The concurrency is reported on every resume; the slot is reserved only where the start path reserves
one.

The reservation cannot go through the start path's `SlotClaimer.ClaimSlot`, and this deliverable stages the
entry point it needs instead. `ClaimSlot` selects the pod itself: `SlotRequest`
(`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:207-236`) carries `Pool`, `SessionID`, `TenantID`,
`MaxConcurrentSessions`, and `MaxPodUptimeSeconds` and no pod field, and the function lists every Sandbox in
the pool and picks one, preferring a pod that already hosts slots for the tenant (`:392-417`). `Binder.Resume`
has already acquired a different pod by that point, through `connect`'s whole-pod `podclaim.ClaimRequest`
(`binder.go:1580-1584`, `:1639-1670`), and every later resume RPC runs against it. A pool scan would therefore
increment `active_slots` on a pod other than the one the session resumes onto, and
`ReleaseSlotReservation(sandboxName, sessionID)` would have no way to name the pod the scan chose.
`podclaim.SlotClaimer` gains `ReserveSlotOnPod(ctx, sandboxName string, req SlotRequest) (*SlotResult, error)`,
which resolves the named Sandbox and its live per-pod claim and calls the existing `reserveSlot`
(`slotclaimer.go:597`) with `freshPod` false, so the Redis reservation, the tenant stamp, and the
`SlotResult` are the ones the start path already produces and only the pool scan is skipped. The per-pod
claim `connect` created is the `existing` claim that call takes, so no second claim is created. `ClaimSlot`
keeps its present body and its two placement passes.

`ResumeRequest` (`pkg/gateway/podlifecycle/podsession/binder.go:611-641`) gains
`MaxConcurrentSessions int32`, populated by the caller from the `PoolMatch` at `start.go:3876` and normalized
to a minimum of 1, because `Binder.Resume` evaluates the greater-than-one scoping itself and
`ReserveSlotOnPod` inherits `ClaimSlot`'s refusal of a value below 1 and of a nil `Counter`
(`slotclaimer.go:392-410`). `MaxPodUptimeSeconds` is not added: it is the placement filter the pool scan
applies, and the resume's pod is already chosen.

The reservation is scoped that way because two gateway release paths dispatch on `BindResult.SlotID`
presence, and making the field non-empty on an exclusive pool would silently re-route that session's
release. `PodExecutor.Release` reads `if bind.SlotID != "" { return e.binder.ReleaseSlot(ctx, bind) }`
(`pkg/gateway/session/executor/pod.go:331-333`), and `rollbackBinding` takes the same branch
(`pkg/gateway/sessionserver/start.go:3079-3083`) on the `BindResult` the resume publishes into the registry
(`start.go:3936`) and hands it on the coordination-lease branch (`start.go:3933`). Two regressions follow
from the re-route on a `maxConcurrentSessions: 1` pool. A failed adapter teardown makes
`Binder.ReleaseSlot` mark the release leaked (`slotbinder.go:532, :539`), and `SlotClaimer.ReleaseSlot`
returns early on `leaked` without decrementing the counter or disposing the claim
(`slotclaimer.go:751-757`), so the pod is held at `active_slots = 1` with its `SandboxClaim` intact until
pod termination, where `Binder.Release` shuts the adapter down best-effort and drains regardless
(`binder.go:1944`, `:1904-1909`). The reachable trigger is the coordinator handoff: `acquireCoordinationLease`
returns `ErrHeld` and `rollbackBinding` runs against a pod that was just restored. On a deployment without
Redis the release is lost outright, since `SlotCounter` is nil when `redisClient == nil`
(`cmd/lenny-gateway/stores.go:2036-2037`), an exclusive-pool-only deployment is supported without Redis,
and `SlotClaimer.ReleaseSlot` hard-errors on a nil counter (`slotclaimer.go:745-750`) after the session has
already been removed from the registry (`pod.go:328`), so the pod is never shut down and never drained.
Scoping the reservation keeps both dispatchers reading the same value on the resume path that they read on
the start path, which is the smaller change; re-keying them on the pool's concurrency would touch two
release paths this proposal otherwise leaves alone. §8 adds a tier-2 case on the exclusive-pool resume
release.

The reservation comes with its compensating release, as it does on the start path. `Binder.Resume` returns
on every failure after the handshake without releasing anything today and states that the caller retries on
a fresh pod (`pkg/gateway/podlifecycle/podsession/binder.go:1574-1584` and `:1599-1602`), which is correct
while it reserves nothing and leaks `lenny:pod:{pod_id}:active_slots` once it reserves. A
checkpoint-restore failure is the retryable case this deliverable exists to enable, so the leak would
compound per attempt, and `ReleaseSlot` and `ReleaseSlotReservation` are the only decrements outside the
post-Redis-restart rehydration. `Binder.Resume` therefore wraps the reservation the way `ClaimSlot` and
`BindReservedSlot` do (`slotbinder.go:161-174` and `:207-221`): every failure after the increment, including
a dial, `NegotiateVersion`, or adapter `Resume` failure, calls
`ReleaseSlotReservation(sandboxName, sessionID)` exactly once before returning, on the resumes that
reserved a slot. The `sandboxName` is unambiguous because `ReserveSlotOnPod` reserves on the pod `connect`
already resolved, which is the pod `Binder.Resume` holds for the rest of the call. The reservation is taken
after `connect` returns, so a `resolveSandbox` or claim failure precedes the increment and releases nothing.
The release is owned by
`Binder.Resume` itself rather than by the caller, so `rollbackBinding` does not run it a second time. §8
adds a tier-2 case on the failure path. The gate then evaluates a true value for the first time on the resume path, and a checkpoint-restore
resume onto a concurrent-workspace pool restores into the slot it reserved.

Proposal 0070 §1.3 states this defect and hands it here rather than correcting it, because the correction
its own review arrived at was to refuse such a resume outright, with a client-visible
`422 CONCURRENT_POOL_RESUME_UNSUPPORTED` and the specification text to match, on the ground that no
slot-aware resume path exists. This proposal builds that path, so the refusal would be staged and deleted
within two proposals. Nothing of 0070's is duplicated here: it keeps the tracing-handler fix, which is
independent.

### CODE-3. Arm hold state from the slot registry

`pkg/adapter/holdstate.go:91-96` arms from the slot registry rather than from the pod-global `s.sessionID`,
so the hold arms on a pod whose sessions all take the slot path, which is the §1.2 defect.
`s.currentSession()` is removed with `Server.sessionID` under CODE-1 rather than left to be reintroduced,
and CODE-1 re-points its two other production readers, the direct-mode token sink and
`emitControlEvent`'s session stamp, onto the same single-bound-session resolution.

The hold stays a property of the adapter process. `pkg/adapter/holdstate.go:246` rejects every inbound RPC
outside `coordinatorHoldAllowedMethods` from the single `s.hold.active` flag (`:187-190`), which is what
`spec/10:57` and `spec/28:250` state, and `CoordinatorFenceRequest` is pod-scoped in §4.1, so a fence exits
the one hold the pod holds. This deliverable changes what arms the hold and what its timeout terminates, and
changes neither its unit nor its exit, so it stages no `spec/28` edit. It needs one `spec/10` edit, stated
under the notification paragraph below and staged by SPEC-9 rather than here. Whether the hold should
be partitioned per slot is the §29.10 gap `spec/29:1544-1548` records, which §3 and §4.4 leave open.

**What the timeout terminates.** The hold carries a termination action beside the rejection gate, and that
action is single-valued today: `holdState` holds one `session` (`pkg/adapter/holdstate.go:39-45`),
`enterHoldState` takes one and is idempotent (`:105`, `:113-114`), and `onHoldTimeout` writes one
post-mortem and closes one session's runtime (`:175`, `:180`). Arming from the registry makes that action
reachable on a pod holding more than one bound session for the first time, and one runtime process serves
every slot, so a single-valued action would either terminate one session and leave every co-tenant's agent
process running with no coordinator, or, passing SCHEMA-1's teardown decision as true on that one call,
close the process out from under co-tenants for which no post-mortem was written. Both are what the
`spec/10:58` termination exists to prevent. Nothing else reaches that state, because the
pod is alive and the 60-second orphan-session reconciler `spec/10:58` names as the backstop does not fire.
The hold therefore terminates the set of bound sessions the slot registry holds at the moment
`onHoldTimeout` fires, and runs the termination once per member of that set, one `Runtime.Close` and one
post-mortem each, passing SCHEMA-1's teardown decision as false on every member but the last and true on
the last, so the shared runtime process is closed when the last member is released. The set is read when
the timeout fires rather than recorded when the hold arms, because `coordinatorHoldAllowedMethods`
(`pkg/adapter/holdstate.go:53-59`) gates RPC admission rather than binding. `holdStateUnaryInterceptor`
evaluates `s.inHoldState()` once, at entry, and then calls the handler (`:245-249`), so a `StartSession`
admitted before the hold arms performs its registry claim and its `Runtime.Start` afterwards, and arming is
two steps: `onCoordinatorChannelClosed` reads the pod's bound sessions (`:91-96`) and `enterHoldState` then
sets `s.hold.active` (`:115`). A session admitted between those two steps binds after the read and would be
absent from a set recorded there, and the hold's 120-second default (`:71-77`) leaves it live when the
timeout fires. Terminating a recorded set would then leave that session's agent process running with no
coordinator, because `Close` on the last recorded member finds the missed session still in the runtime's
active set and returns early (`pkg/adapter/socketruntime.go:440-445`), which is the outcome the per-member
termination exists to prevent. §8 pins the interleaving at tier 7a. `s.hold.active` stays one pod-wide flag and a fence still exits the one hold, so the unit §3 states
is unchanged.

**What the notification carries.** The `AdapterTerminating` notification is one per terminated member, sent
from inside the same loop, and each one names the member it terminated. The envelope already carries a
session identifier: `controlEvent` declares a `SessionID` member
serialized as `sessionId` (`pkg/adapter/adapterevents.go:60`) and `emitControlEvent`
stamps it from the pod's bound session for every event that arrives without one
(`:149-152`), which is the set `EmitAdapterTerminating` belongs to (`:216-217`). CODE-1's
single-bound-session resolution yields no entry on a pod holding more than one, so leaving the stamp to the
accessor would emit one envelope with an empty `sessionId` on exactly the pods this deliverable newly
reaches. `EmitAdapterTerminating` therefore takes the session identifier its caller already holds,
`EmitAdapterTerminating(sessionID, reason string)`, which is the change CODE-1 already makes to
`EmitFinalUsageReport` for the same reason and at the same call site.

That is not a change to the `CH-ADAPTEREVENTS` contract. The envelope's `sessionId` member is unchanged, the
§4.7 event-catalog row already states `session_id` among the message's fields (`spec/04:704`), and the
reader-facing row states the same (`docs/reference/adapter-contract.md:97`), so both stay true and neither
takes an edit. What changes is which value the field holds on a concurrent pod, and only the Go emitter
signature moves. The alternative, one pod-scoped notification with the field unset, was rejected because
the `spec/10:58` immediate transition it forfeits has no replacement: the 60-second orphan session
reconciler that bullet names is predicated on a terminated pod (`spec/10:59` matches a session "whose
corresponding pod has `Terminated` phase in the `agent_pod_state` mirror"), and after a hold timeout the pod
is alive. `onHoldTimeout` (`pkg/adapter/holdstate.go:154-182`) emits, writes the post-mortem, and closes the
runtime without exiting the adapter process, and SCHEMA-1 drops the listener close precisely so the pod goes
on to serve another session. Every co-tenant's session row would then sit in a non-terminal state holding
quota behind a dead agent, with no bounded recovery, which is strictly worse than the base-mode behavior
this deliverable extends.

`spec/10_gateway-internals.md:58` therefore takes one edit, which records the concurrent case rather than
withdrawing the immediate transition. SPEC-9 stages that edit, for the reason it states
about its own `spec/16` rows: the pipeline lands every staged specification edit before the code phase and
then blocks `spec/` writes, so an edit staged inside a code deliverable would never be written. The bullet keeps
`AdapterTerminating(session_id, reason: coordinator_lost)` and the immediate-transition consequence, and
gains the concurrent case: a hold timeout on a pod holding more than one
bound session terminates each of them and sends one `AdapterTerminating(session_id, reason:
coordinator_lost)` per terminated session, so the gateway performs the immediate transition for each. §8
pins the per-member arity and attribution at tier 7a.

### CODE-4. Address credential operations by session

`pkg/gateway/runtime/adapterclient/client.go:234-241` and `:253-262` address rotation and lease extension by
the session identifier the request already carries, and the adapter's credential handlers resolve the
per-slot lease from it (`pkg/adapter/credentials.go:74, 116, 158, 331`). The revocation path gains its
gateway caller, or the adapter's `RevokeCredentials` is removed if review establishes the Token Service path
is the only intended one; §9 records this as open.

**The rotation merge keeps the §4.7 protocol**, the same way SCHEMA-1's teardown merge and CODE-1's
start-side merge keep the work their pod-global branch performed. Today `RotateCredentials` returns early
into `rotateCredentialsSlot` when a slot is named (`pkg/adapter/credentials.go:116-117`), and that branch
performs the per-slot file rewrite alone, stating that the §4.7 Full-level protocol is the runtime's
concern (`pkg/adapter/slotcreds.go:57-60`). The pod-global branch below it runs that protocol through
`rotateProviderFull` (`credentials.go:132`, declared at `:167-172`): the in-flight LLM-request completion
gate, the 300s revocation ceiling with its counter and the durable `credential.rotation_ceiling_hit` audit
event, the `credentials_rotated` send, and the 60s `credentials_acknowledged` timeout that falls through to
the standard path. Deleting `useSlot` makes the per-slot branch universal, so without this clause no
session on any pod would run the protocol. The merged handler therefore performs the per-slot file rewrite
and then, when the runtime declares `credential_rotation` support, runs `rotateProviderFull` for each
rotated provider against that session's own credential file.

Two of the protocol's three waits are session-separable and one is not, and the deliverable states which is
which rather than claiming isolation the code does not give. The file rewrite is per session, and the
acknowledgement wait is keyed on the lease identifier (`cred:{leaseId}`,
`pkg/adapter/runtimeops.go:462-469`), so two sessions rotating at once wait on distinct keys. The in-flight
gate is not: `awaitInflightGate` polls `s.Lifecycle.InflightCount(provider)` (`pkg/adapter/credentials.go:178,
:244-249, :271-273`), a pod-global per-provider counter adjusted from the `llm_request_started` and
`llm_request_completed` frames, which carry a provider and no session
(`pkg/adapter/runtimeops.go:365-368, :404-408`), on the one `CH-RUNTIMEOPS` connection to the one runtime
process serving every slot (`pkg/adapter/server.go:234-239`). The 300s ceiling exists only to bound that
gate, so it is pod-scoped with it, and the `credentials_rotated` frame carries a provider, a credentials
path, and a lease id and no session, so the shared runtime is handed one session's per-slot credential file
with nothing naming whose it is. Making the per-slot branch universal therefore runs this protocol on
concurrent pods for the first time with that coupling: a co-tenant's outstanding request for the same
provider gates the rotating session's wait, can drive it into the ceiling on its own (emitting
`lenny_credential_rotation_inflight_ceiling_hit_total` and the durable `credential.rotation_ceiling_hit`
audit event with the co-tenant's outstanding count attributed to the rotating session), and the rotation
then proceeds while the co-tenant still holds requests on the old credential. §9 records the pod-wide gate,
the pod-wide ceiling, and the session-less frame as a limit beside the manifest collision. Giving the
protocol a session dimension needs per-session in-flight counts, a session field on the `llm_request_*` and
`credentials_rotated`/`credentials_acknowledged` frames and their correlation key, and the matching §4.7 and
§28 frame-contract edits, which is its own deliverable and is not staged here. §8's tier-8 block pins the
protocol on the merged path and §8's tier-9 block pins the co-tenant coupling as the recorded behavior.

The runtime-facing half of SPEC-4 lands here. The adapter's manifest type (`pkg/adapter/manifest.go:106-157`)
gains a `CredentialsPath string` member with the `credentialsPath` tag, filled from
`slotlayout.Resolve` against the session identifier at the manifest write CODE-1 merges into `StartSession`.
The three runtime SDKs resolve their credential path from the manifest they already load at startup and fall
back to their construction-time option when the caller sets one, so the pod-global literal stops being a
default: `sdks/runtime/go/runtime/runtime.go:129, :224-230, :577` and `options.go:104`,
`sdks/runtime/python/lenny_runtime/runtime.py:58, :547`, and `sdks/runtime/typescript/src/runtime.ts:48,
:109, :513`. The Go SDK's `handleCredentialsRotated`
(`sdks/runtime/go/runtime/lifecycle.go:246-256`) additionally decodes the frame's `credentialsPath` and
re-reads that path, so a Full-level rotation lands on the file the adapter rewrote rather than on the path
the SDK started with. The three `*-chat` scaffold templates under
`cmd/lenny-ctl/runtimescaffold/templates/` and `cmd/lenny-compliance/full.go:373` take the same resolution.

### CODE-5. Remove the sentinel and the Go surface of the dropped columns

`partialmanifeststore.SlotDefault` (`:67-70`) and its four application sites (`:367-369`,
`pgstore/pgstore.go:157-159`, `pkg/gateway/sessionserver/derive.go:481`, and
`pkg/gateway/checkpoint/checkpointer/checkpointer.go:500-503`) are removed, along with the ten-line
explanatory comment at `checkpointer.go:536-545` and the `slotIDField` helper at `:586-600`, which goes dead
once `CheckpointStart.slot_id` is removed.

MIG-1 drops two columns, and every Go reader and writer of them goes with the columns in this deliverable.
The SQL half is not caught by the compiler, because the statements naming the columns are string literals,
so a partial edit fails at runtime with an undefined-column error on every checkpoint insert and rotation.
The Go half is caught, because deleting `Record.SlotID` from both stores, dropping the `slotID` parameter
from three `Store` methods, and renaming `LatestActiveForSlot` breaks every caller. §8 gives each test file
that constructs those fields or calls those methods its own disposition, because until they take it
`pkg/gateway/checkpoint/checkpointretention`, `pkg/gateway/checkpoint/partialmanifeststore`,
`pkg/gateway/checkpoint/checkpointer`, `pkg/gateway/sessionserver`, `tests/tier2_component`, and
`tests/tier4_integration` do not build and tiers 0, 1, 2, and 4 cannot run.

- `pkg/gateway/checkpoint/checkpointretention` owns `session_checkpoints`. `Record.SlotID`
  (`checkpointretention.go:45-53`) is deleted, the `slotID` parameter is dropped from
  `Store.Rotate`, `Store.List`, and `Store.HardDelete` and from the `MemoryStore` implementations of each
  (`:103, 109, 119, 188, 194, 229, 234, 263`), and the `slot_id` occurrences in the SQL are removed:
  `pgstore/pgstore.go:40` (the select list), `:60-63` (the INSERT column list and its arguments), `:87` and
  `:147` (the `WHERE` clauses of both queries), `:200` (the `HardDelete` placeholder parameter), and `:241`
  (the row scan).
- `pkg/gateway/checkpoint/partialmanifeststore` owns `checkpoint_manifest`. `Record.SlotID` (`:82`) is
  deleted, `Store.LatestActiveForSlot` (`:244`) becomes `LatestActiveForSession(ctx, tenantID, sessionID)`
  with the `MemoryStore` implementation at `:536-545` following, and the `slot_id` occurrences in
  `pgstore/pgstore.go:40, 50, 98, 115, 126, 135, 318` and `:543` are removed with the supersede predicates
  they scope. `partialmanifeststore.go:405` and `:416`, which compare `row.SlotID`, drop the comparison.
- `pkg/gateway/checkpoint/checkpointer/uploaddriver.go` carries the dimension into both stores. The
  `slotID` field on the upload driver (`:198`), its two populations (`:320` and the `SlotID:` literal it
  fills), and its use in the in-flight lock key (`:132-133`) are removed, and the supersede call at `:406-407`
  moves onto the session-scoped selector.

§4.9's re-keyed indexes are what these edits align to, and §8's tier-2 retention case exercises the result.

### CODE-6. Rename the JSONL frame field and make population unconditional

The adapter frame helpers, the demultiplexer, the gateway's own JSONL structs, the three runtime SDKs, the
two reference runtimes, and the prose and diagnosis comments listed in §4.6.2 take the rename and the
unconditional population. `pkg/adapter/tracingcontext.go:49-59` loses its `slotID == ""` branch per §4.8.

The gateway half is `pkg/gateway/session/executor/subprocess.go:370-383` (the `messageEnvelope` member and
its tag) and
`pkg/gateway/session/executor/pod.go:224-231, 269-275` (the `toolCallFrame` member and the re-marshal on an
approval), together with the envelope's population site, which takes a value change rather than a key
change. `pkg/gateway/session/executor/pod.go:93-107` fills the member from `bind.SlotID`, and CODE-2 keeps
`BindResult.SlotID` empty on a `maxConcurrentSessions: 1` pool so the two presence-keyed release
dispatchers it names keep reading an untouched value. Renaming the key alone would therefore emit an
envelope whose renamed member is the empty string on exactly the pool D1 exists to normalize, and the
`omitempty` tag would drop the key outright. The site is re-pointed onto the `sessionID` the executor is
already sending on, so the key is non-empty on a pool of either concurrency, the local `slotID` variable and
its registry read (`pod.go:93-96`) go with it, and `BindResult.SlotID` is untouched. Both structs carry the key as a `json` tag rather than as a generated accessor, so a missed edit
here compiles and diverges at the wire, which is why §8's tier-3 case asserts the emitted key rather than
the struct. The Python and TypeScript SDK emitters are in the same position with no compiler at all
(`sdks/runtime/python/lenny_runtime/runtime.py:368, 385` and `tool.py:147`;
`sdks/runtime/typescript/src/runtime.ts:329, 344` and `src/tool.ts:133`), so §8 adds a per-SDK tier-3 case
on the emitted key in the existing `tests/tier3_contract/sdks/runtime_sdk_test.go` harness.

That harness runs each SDK's example runtime under `cmd/lenny-compliance` and reads only its named-check
JSON report (`tests/tier3_contract/sdks/runtime_sdk_test.go:198-212`), so the compliance binary is where the
per-SDK case gets its frames and this deliverable stages it. Every inbound `message` frame the binary sends
carries no identifier of any kind today (`cmd/lenny-compliance/main.go:388`, `:625-627`, and
`standard.go:488`; `grep -rn "slotId\|slot_id" cmd/lenny-compliance/` returns nothing), and all three SDKs
derive the outbound identifier from the inbound envelope, so a runtime driven by the harness echoes nothing
and the case would pass vacuously against an empty string. The three inbound `message` literals carry
`sessionId`, and a Basic-level check asserts that the runtime echoes it on the `response` it returns, and on
the `tool_call` at the level that exercises one. Without that check the Basic-level echo obligation SPEC-7
stages is unverified by the suite that certifies a third-party runtime against the level it declares.

The inbound resolve-or-reject rule in §4.6.1 lands here as well. For a session-scoped frame carrying no
identifier, the adapter resolves the frame to the receiving stream's binding when the pod holds exactly one
slot, and rejects it when the pod holds more than one, incrementing
`lenny_adapter_unaddressed_frame_rejected_total` (a counter labeled by `frame_type`), logging the frame
type and the pod's slot count, and relaying the frame to no stream. The decision sits in the per-stream
demultiplexer, per §4.6.1, so the counter and the log line are per rejecting Attach stream: one unaddressed
frame on a pod holding two live streams increments the counter twice. The counter is new because no existing
adapter counter covers the condition: `lenny_adapter_set_tracing_context_dropped_total`
(`pkg/adapter/metrics.go:89`) is scoped to one frame type and to a mis-addressed frame rather than to an
unaddressed one, while this rule spans the six session-scoped frames. The metric inventory is
single-sourced in `spec/16_observability.md` §16.1 and mirrored in `docs/reference/metrics.md`, and
`tests/tier11_docs/adapter_metric_catalog_test.go` fails any adapter metric that reaches neither, so both
rows land with the counter. The `spec/16` half of that pair is staged by SPEC-9 rather than here, because
the pipeline applies every staged specification edit in its own commit and then blocks `spec/` writes for
the code phase, so a `spec/16` row staged inside a code deliverable would never be written and the tier-11
gate would fail on the registered counter. This deliverable stages the counter's registration in
`pkg/adapter/metrics.go` and its `docs/reference/metrics.md` row, including the note the sibling adapter
rows carry, that the metric is emitted by the adapter process inside the agent pod and is outside the §16.9
default scrape target set until a deployer wires an adapter scrape target. `docs/reference/metrics.md:179`,
the `lenny_adapter_set_tracing_context_dropped_total` mirror, is restated in the same change, because §4.8's
rewrite changes what "does not address the Attach stream that delivered it" means; SPEC-9 restates its
`spec/16:188` row. The slot count is read from the
adapter's own registry rather than from a manifest value or a request field, and it counts every entry,
bound or registered-but-unbound, which is the quantity SCHEMA-1's drain gate reads.
`demuxSlotOutput` (`pkg/adapter/attach.go:253-295`) keeps its pass-through for `heartbeat` and
`heartbeat_ack` and narrows by frame type for the six session-scoped frames.

`cmd/runtimes/echo-concurrent`
loses its empty-string default worker and its no-slotId routing paths, and `cmd/runtimes/echo-embedded`
takes D7's uniform layout.

The bundled reference runtimes take the echo obligation itself, because the Basic-level check this
deliverable adds is run against them and not against the SDK example runtimes alone.
`tests/tier10_conformance/scaffolds_test.go:202-212` drives `echo`, `streaming-echo`, and `delegation-echo`
through the Basic battery and requires every check in it to pass, `:345-352` repeats that for `echo` on
every pull request, `tests/tier10_conformance/reference_battery_test.go:41-43` repeats it for the `echo`
image, and `cmd/lenny-compliance/standard_test.go:73-82` requires delegation-echo's Standard battery, which
is the Basic battery plus the Standard categories, to report no failure. None of the three can echo the
identifier today: each decodes an inbound envelope declaring only `type`, `id`, and `input`, and emits a
`response` declaring only `type` and `output`
(`pkg/runtimekit/echocore/echocore.go:110-114, 176-179`, which `cmd/runtimes/echo` and
`cmd/runtimes/echo-embedded` both run; `cmd/runtimes/streaming-echo/main.go:396-400, 466-469`;
`cmd/runtimes/delegation-echo/main.go:235-239, 608-612`). This deliverable therefore stages
`pkg/runtimekit/echocore`, `cmd/runtimes/streaming-echo`, and `cmd/runtimes/delegation-echo`: each inbound
struct reads `sessionId` and each `response` stamps the value it was handed. `cmd/runtimes/echo` and
`cmd/runtimes/echo-embedded` inherit the edit through `echocore` and take no change of their own. No
bundled runtime emits a `tool_call`, and the Standard-level correlation check already tolerates a runtime
that emits none (`cmd/lenny-compliance/standard.go:692-702`), so the `tool_call` half of the obligation is
exercised by a runtime that emits one rather than by these three.

### CODE-7. Reconcile the client SDKs and the client-facing surfaces

The `slotId` field is removed from the client-SDK message payloads (`sdks/client/go/lenny/types.go:189-190`,
`sdks/client/python/lenny/types.py:266, 279-280`, `sdks/client/typescript/src/types.ts:168`) and from the
references in their client docs (`go/lenny/client.go:265`, `typescript/src/client.ts:325`,
`python/lenny/client.py:326`), matching what the gateway accepts. The comment at
`pkg/gateway/sessionserver/messages.go:186-193` states that the field is not part of the client contract
rather than that a supplied value is ignored.

The tool-approval detail and SSE payload (`pkg/gateway/sessionserver/toolapproval.go:101-102, 216-221`) each
carry a duplicate of a session identifier the enclosing object already carries
(`toolapproval.go:107` carries `SessionID`, and the stream is per session), so the `slotId` key is dropped
from both.

The `/start` 422 `SLOT_FAILED` body (`pkg/gateway/sessionserver/start.go:317`, sourced from
`podsession.SlotFailedError.SlotID` at `slotfailure.go:125-130`) is a different case and takes a
replacement rather than a bare removal. Its envelope carries `code`, `category`, `message`, `retryable`, and
`details` (`pkg/gateway/sessionserver/sessionserver.go:2421-2427`) and no session member, `SlotFailedError`
carries no session member for one to be filled from, and one of the two routes that reaches the mapper is
the one-call `POST /v1/sessions/start` (`sessionserver.go:2039`, dispatching through
`writePodClaimError` at `start.go:186-193`), whose path carries no session identifier and whose session row
is rolled back rather than persisted. Dropping the key alone would leave the client a 422 naming nothing.
`SlotFailedError` therefore gains a `SessionID` member, populated at the producer
(`start.go:2785`), and `writeSlotFailed` emits `"sessionId"` in its place, so the body names the session
whose slot failed on both routes. SPEC-7 restates `spec/05:558` on that key in the same change, since the
sentence is a specified client-facing contract and the specification must name the field the gateway
emits.

### MIG-1. Drop the persisted duplicate columns

A migration drops `session_checkpoints.slot_id` and `checkpoint_manifest.slot_id` and re-keys the three
indexes per §4.9. There is no backfill, because there is no value to preserve. The same migration rewrites
`sessions.workspace_root` to the slot path per CODE-2.

`tests/tier2_component/migrations/prod_columns_test.go`'s matrix keeps its 0112 entry with an empty column
list and a comment naming the drop migration, the way the file already handles 0040/`concurrency_style`
(`:60-66`) and 0022/`task_policy` (`:46-49`), because `scripts/lint-migrations.sh` requires every migration
to be referenced by number in a test (check 3, `:11-16`). A matrix entry for the new drop migration is added so
`TestProdMigrationsRollBackPerStep` exercises its `.down.sql`.

### REG-1. Retire the register rows and the gate machinery they drive

The five slot rows in `tests/claim-map.json` (`:77, 175, 289, 413`, spelled "X slot identifier field", and
`:313`, spelled `ResumeRequest.slot_id`) are retired outright with the fields SCHEMA-1 removes, as is the
`ABSENT` "Slot-qualified interrupt, deadline, usage, and barrier" row at `:436-442`, whose deferred
capability SCHEMA-1 removes rather than defers. The earlier draft staged these five rows as moving from
`UNWIRED` to `ABSENT`; that edit is dropped rather than amended, because all five fields exist
(`InterruptRequest :1091`, `SignalDeadlineRequest :1275`, `CheckpointBarrierRequest :1444`, `ResumeRequest
:1361`, `ReportUsageRequest :1539`) and the rows are correctly `UNWIRED` under §28.4's definition.

Three rows are added for the per-slot credential addressing §1.3 records as unwired, one for rotation, one
for lease extension, and one for revocation. They name that behavior rather than the `slot_id` fields
SCHEMA-1 deletes, so the register keeps a record of the gap after the fields are gone. Each is `ABSENT`
until CODE-4 lands its half, and moves to `WIRED` then.

The `Checkpoint restore onto a concurrent pod` row (`tests/claim-map.json:68-75`) moves from `ABSENT` to
`WIRED`. Its whole subject is the defect CODE-2 fixes, the restore path discarding the slot dimension it is
handed, so after application its status, its `surface` (`pkg/adapter/resume.go:25` reads no slot dimension
from the request), and its `note` are all false. The row does not retire with the field, because it names
the restore behavior rather than a proto field, so SCHEMA-1's removal does not carry it away. The new
surface names the corrected restore path (`pkg/adapter/resume.go` resolving the checkpoint roots from the
request's session identifier) and the `deferral_id` `R22` is dropped with the deferral.

The generation-fence row on the message SCHEMA-1 deletes moves with it, under the base case in §4.5(d).
`tests/claim-map.json:404-410` carries "ShutdownRequest.coordination_generation generation fence field",
whose `surface` is `schemas/lenny-adapter.proto`; `registerProtoDisagreements` matches it through
`claimQualifiedField` and reports a row naming a field the proto does not declare
(`tests/tier0_static/claim_register_proto_agreement_test.go:116-124`). The row is retired and two rows
replace it, `ShutdownSlotRequest.coordination_generation generation fence field` and
`ShutdownPodRequest.coordination_generation generation fence field`, each carrying `status: UNWIRED`,
`spec_anchor: "#2851-gateway-to-pod"`, `surface: "schemas/lenny-adapter.proto"`, `deferral_id: "R16"`, and
the note its sibling fence rows carry at `:419-426`, that the field is carried on the request and no
production reader compares it until the generation fence lands. Both rows are required rather than
optional: the fence loop at `:139-149` iterates every proto message declaring `coordination_generation`,
exempts only `CoordinatorFenceRequest` (`:47`), and reports a declared fence field with no register row, so
the two new messages would fail the gate twice over. If review declines the split, `ShutdownRequest`
survives and the row stands unchanged.

The rows that change status under this proposal are therefore the six retired outright, the fence row
retired with `ShutdownRequest` under the base case, the restore row above, and the five rows added, three
for credentials and two for the post-split fence fields.

Their removal leaves unused machinery in `tests/tier0_static/claim_register_proto_agreement_test.go`:
`slotIDField` (`:41`), `claimSlotField` (`:59`), and the `claimSlotField` branch of `namedField`
(`:157-159`) are deleted, the slot-spelling case at `:233-237` and the `InterruptRequest slot identifier
field` row in the acceptance fixture at `:297` are dropped, and the two synthetic protos (`:194-198`,
`:284-288`), which carry `SlotId slot_id = 5`, are updated to the post-removal message set. Nothing here
fails to compile, because the synthetic protos are inlined, which is why the cleanup is staged rather than
left to the gate to surface. The `ResumeRequest.slot_id` row is matched by `claimQualifiedField` (`:58`) and
touches no slot-specific code. The generation-fence half of the gate (`:139-149, :216-219, :251-254`) keeps
its code unchanged and is re-run against the post-split message set, which is what the two replacement rows
above satisfy.

Two refusal cases do turn red on the synthetic-proto update and are staged with it. The cases at `:238-243`
and `:244-249` assert that a register row claiming the absence of a declared field is refused, and both are
written over the spelling "no `slot_id` on `InterruptRequest`". `registerProtoDisagreements` emits that finding only when
`fields[m[2]][m[1]]` holds (`:127-134`), so once the synthetic `InterruptRequest` no longer declares
`slot_id` both cases produce zero findings and fail at `:269-271` ("the check accepted a register it must
refuse"). Both are re-pointed at `coordination_generation`, which the post-removal synthetic proto still
declares, rather than deleted, so the `absenceAssertion` path (`:62`) keeps its refusal coverage after the
slot rows are gone.

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

**Tier 0.** A gate reads the §4.1 classification table and the proto text together: every request message
in scope that the proto declares appears in the table, so a message added later fails the gate until it is
classified, and no table row names a message the proto does not declare. In scope is a predicate the gate
evaluates against the proto rather than a judgement: a message is in scope when it is the request type of a
declared RPC on `service Adapter` or `service GatewayControl`, plus `CheckpointStart`, which §4.5(c)
re-addresses and which the table classifies in its own right. Nested message types that are not an RPC's
request type are out of scope. The gate's message parse is
service-aware, since the existing regexes cannot tell which service a message belongs to and would
otherwise fail on every `GatewayControl` request the table omits under choice (b).

The gate is a new tier-0 file rather than an added check inside
`tests/tier0_static/claim_register_proto_agreement_test.go`, whose remaining subject after REG-1 is the
generation fence and whose `// spec:` annotation and `// diagnosis:` comment (`:166-171, :188-191`) are
written for the register-to-proto agreement question. `protoFields`/`braceDelta` (`:68-99`) and
`protoMessageOpen`/`protoField` (`:52-55`) are extracted into a shared helper the two gates share rather
than copied.

The claim-register validator runs over the corrected rows, and the register-to-proto gate runs over the
post-split message set: no row names a field the proto no longer declares, and each of
`ShutdownSlotRequest` and `ShutdownPodRequest` carries the fence row REG-1 adds, which is what the gate's
fence loop requires of every
message declaring `coordination_generation`.

A second tier-0 case pins REG-1's seeding change in `scripts/seed-claim-register.py`. A row naming a field
the tree holds seeds as its wired or unwired status, and a row naming a field the tree does not hold seeds
as `ABSENT` whatever a deferral step's scope note says. The non-happy path is the one the old inference
produced: a row whose deferral note claims a scope the tree does not carry seeds `ABSENT` rather than
taking the note's status, so a removed field cannot be recorded as merely deferred.

**Tier 1, `pkg/adapter`.** A session-scoped request with an empty `session_id` is rejected with
`InvalidArgument` before a root is resolved, one case per handler. An unknown identifier returns
`FailedPrecondition`. `slotlayout.Resolve` is the only path builder reached. `checkSessionBound` rejects an
unbound slot and a released one, covering the five §4.11 handlers that today call `checkSession`.

**Tier 1, the two session-stamped outputs CODE-1 re-points.** Both readers of the retired
`s.currentSession()` outside the session check carry an existing pin that writes `Server.sessionID`
directly, so both files break the `pkg/adapter` build and both are hand-rewrites. `pkg/adapter/usage_test.go`
sets the field at `:157`, `:197`, `:235`, and `:268`; the three `ReportUsage` cases (`:152`, `:193`,
`:265`) bind their session by writing it and are re-based onto a bound registry slot for the session the
request names, and `TestWireDirectModeUsageInstallsMeterAndSink_spec_11_2` (`:218`) keeps its subject with
the fold asserted through `m.Cumulative(ctx, "sess-wire")` (`:251-256`) on a pod holding that one bound
slot. `pkg/adapter/adapterevents_test.go` sets it at `:96` and `:187` and takes the same re-base, keeping
its assertion that the RATE_LIMITED envelope carries the session identifier (`:106-108`). Two new cases
pin the resolution itself: a `llm_request_completed` fold on a pod holding one bound slot lands in that
session's cumulative total and is dropped on a pod holding zero bound slots and on one holding two, and a
RATE_LIMITED event emitted on a pod holding one bound slot carries that session's identifier while the
same event on a pod holding two carries an empty `sessionId`. Without them the fold and the stamp would go
to the empty session on every pod with no compiler error and no failing case. A third case covers the
emitter CODE-1 stamps explicitly rather than resolving: `TestEmitFinalUsageOnShutdownPath_spec_4_7`
(`:183-200`), which binds by writing the field at `:187` and drives `emitFinalUsage` at `:196`, is re-based
onto a pod holding two bound slots, and asserts the FINAL_USAGE_REPORT envelope carries the ending
session's identifier. Left on the resolution, the envelope would be unstamped on that pod and the
§8.3 `budget_return` input would be unattributable.

**Tier 1, the merged start.** These cases pin CODE-1's start-side merge, which no other case reaches. A
`StartSession` on a pod of either concurrency writes the §15.4 manifest carrying the session's identifier,
its `taskId`, its `adapterLocalTools`, a freshly generated `mcpNonce`, and the `credentialsPath` SPEC-4 and
CODE-4 add, which resolves to that session's `/run/lenny/slots/{sessionId}/credentials.json`, and the
platform MCP server is started on that nonce, which is what a base-mode session receives today and what the deleted branch would
otherwise have taken with it. The same case asserts a non-null `llm` object whose `deliveryMode`, and in
proxy mode whose `dialect`, `baseUrl`, and `apiKeyEnv`, match the lease that session was assigned, on a pod
of either concurrency. That is the `spec/04:788-792` field set CODE-1 re-points from `s.credLeases` onto the
slot's own lease set, and without the case the merge ships a manifest whose `llm` is null for every session,
which no compiler and no other case detects. On a pod whose two slots hold different provider leases, the
manifest each `StartSession` writes carries the starting session's own lease, which pins that the derivation
reads the slot's lease set rather than a pod-global one; the rewrite of the single manifest by the later
start is the collision §9 already records. The non-happy path is a manifest-write failure, which releases the claim and
returns the §16.3 transient error rather than leaving a bound slot behind. A second case asserts the limits
§9 records rather than behaviors the proposal claims: a second `StartSession` for a different session on
the same pod is admitted and reaches `Runtime.Start`, it rewrites the single manifest so the file names the
later session, and it starts no second platform or connector MCP server, because both bind the pod's one
socket. Without the once-per-pod guard CODE-1 states, the second start fails at `net.Listen` with
`EADDRINUSE`, returns `Internal: start platform MCP server`, and tears the first session's servers down
through the shared cancel, so the case fails in exactly that state.

**Tier 1, the re-based claims.** §4.11 re-keys `claimSession` and `claimSessionForConfigure` onto the slot
registry, and both carry a spec-named failure path with no case in this section today. For `StartSession`:
a second `StartSession` for a session whose slot is already bound returns `Unavailable`, which is the
`startSessionSlot` arm the merged claim keeps, and a `StartSession` for a different session on a pod-warm
pod of either concurrency is admitted, which is the pod-idleness refusal that moves to the gateway per §9's
limit, so the change of enforcement point is asserted rather than discovered. A third arm asserts that the
same `StartSession` on an SDK-warm pod is still `Unavailable`, which is the refusal §4.11 keeps for that
pod class. For `ConfigureWorkspace`: a repeat call
for the same session is idempotent, leaving the manifest nonce unchanged and the MCP server unrestarted,
including when a prior workspace-prep RPC has already left a registered-but-unbound registry entry, which is
the state that would make an existence-keyed `fresh` report the wrong answer; a call for a different session
on a pod already holding one returns `Unavailable`, which is the refusal §4.11 keeps because the pod class
is fixed at `maxConcurrentSessions: 1`, with the incumbent's manifest nonce asserted unchanged so the
regeneration hazard is pinned rather than assumed; and a call whose runtime
configuration fails releases the claim, so a fresh call for the same session succeeds. Each carries a
`// spec:` tie to §4.7 and, for the nonce, §15.4.3.

`AssignCredentials` takes the same treatment, because §4.11 retires its sticky `credSessionID` refusal with
the two claim arms and no case in this section covered the merged handler. Three cases are added. An
`AssignCredentials` for a second, different session after that pod's first session was released is admitted
and writes only the second session's own `/run/lenny/slots/{sessionId}/credentials.json`, leaving no
pod-global credential file behind, which is the F-6.1.12 arm §9's limit retires and the enforcement point it
names. No case asserts a wrong-session refusal on the merged handler, because under D5 the request carries
one identifier and it is the registry key, so the arm at `pkg/adapter/slotcreds.go:35-37` is unreachable and
such a case is unconstructible; a repeat assignment for the same session rewrites that slot's file
idempotently, which is the arm that survives. An `AssignCredentials` on an adapter configured with no credentials root is refused with
`FailedPrecondition`, which pins that the precondition survives the merge through
`writeSlotCredentialFile` (`pkg/adapter/slotcreds.go:150-154`) rather than being lost with the pod-global
branch. A fourth case pins the prior-assignment refusal §4.11 adds to the merged rotate and revoke handlers:
a `RotateCredentials` and a `RevokeCredentials` on a registered, bound slot that received no
`AssignCredentials` are each refused with `FailedPrecondition`. That is the third refusal
`checkCredentialSession` enforced and the one the per-slot handlers' registry lookup does not reproduce, so
without the case a rotation would materialize a credential file for a session that was never assigned one
and a revocation would rewrite it. Each carries a `// spec:` tie to §6.1 and §4.7.

`pkg/adapter/one_session_only_test.go` and `pkg/adapter/sdkwarm_test.go` are hand-rewrites, because their
cases drive the retired `claimSession`, `claimSessionForConfigure`, and `releaseSession` directly
(`one_session_only_test.go:107-122`, `sdkwarm_test.go:118-121, 124-147, 149-159, 161-175`). The idempotency
and release-on-failure subjects survive as the cases above.
`sdkwarm_test.go:149-159` (`TestConfigureWorkspaceDifferentSessionUnavailable_spec_4_7`) also survives with
its assertion intact, because §4.11 keeps `claimSessionForConfigure`'s different-session `Unavailable`; only
its workspace paths move to the per-slot layout. `sdkwarm_test.go:118-121`, a `StartSession` for a second
session after a `ConfigureWorkspace` on the same SDK-warm fixture, survives on the same ground: §4.11 keeps
the different-session refusal on an SDK-warm pod for `StartSession` and `Resume` as well, so the case keeps
its `Unavailable` assertion and gains a `// spec:` tie to the §4.11 gate. Three cases assert the
pod-idleness refusal on a pod-warm pod and are inverted rather than carried over, because §9's limit
retires it there: `one_session_only_test.go:110-122`
(`TestClaimSessionRejectsConcurrentSession_spec_6_1`, F-6.1.12), `pkg/adapter/session_test.go:244`
(`TestStartSessionRejectsSecondSession`), and `pkg/adapter/resume_test.go:283`
(`TestResumeRejectsANonIdlePod`). Each is rewritten to assert admission on the
second session's own slot, and the ceiling it used to assert is pinned at the gateway. The last two reach
the refusal through the two `claimSession` call sites §4.11 re-keys, `pkg/adapter/session.go:117` on
`StartSession` and `pkg/adapter/resume.go:47` on `Resume`, and neither break is compiler-caught, because
`startReq` (`session_test.go:225-229`) and `resumeReq` (`resume_test.go:102-108`) build their requests with
no `SlotId`. The resume case is the only pin of the refusal on the resume path, so its rewrite is what
pins that a resume for a second session is admitted onto its own slot.

`pkg/adapter/session_test.go` is therefore a hand-rewrite beyond the `sessionServer` helper stated below,
and two further cases in it are re-based rather than inverted. `TestSendMessageRejectsUnknownSession`
(`:289`) and `TestShutdownRejectsUnknownSession` (`:395`) each require `NotFound` for a session the pod
never held, which the pod-global comparison returned and the merged handlers do not: a lookup for an
unregistered identifier returns `FailedPrecondition` with "no assigned session"
(`pkg/adapter/slotsession.go:64-68`, `:116-121`), which is the same re-base this block applies to the
credential twin above and which the reclaimer paragraph already relies on. Both request literals carry no
`SlotId`, so both compile unchanged and fail at runtime.

`pkg/adapter/one_session_only_test.go`'s two credential cases and `pkg/adapter/credentials_test.go` are
hand-rewrites for the same reason, and neither is compiler-caught because both drive the RPCs rather than
the deleted helpers. `TestReleaseSessionClearsSessionIdButKeepsCredSessionId_spec_6_1` (`:33-53`) and
`TestAssignCredentialsRejectsDifferentSessionAfterShutdown_spec_6_1` (`:55-88`) assert the sticky
`credSessionID` §4.11 retires; the first goes with the field and the second is inverted into the admission
case above. In `pkg/adapter/credentials_test.go`, `TestAssignCredentialsRejectsADifferentSession` (`:138`)
is inverted rather than re-based: its second assignment for `sess-B` after `sess-A` lands on `sess-B`'s own
slot and is admitted, so the case asserts the admission and the two separate per-slot files, which is the
same arm §9's limit records. `TestAssignCredentialsRequiresACredentialsDir` (`:127`) keeps its
assertion and moves onto the merged handler, and the three cases pinning the pod-global
`checkCredentialSession` (`TestRotateCredentialsRequiresAPriorAssignment` at `:186`,
`TestRotateCredentialsRejectsADifferentSession` at `:197`, and
`TestRevokeCredentialsRequiresAPriorAssignment` at `:244`) are re-based onto the merged handlers.
`TestRotateCredentialsRejectsADifferentSession` (`:197`) is re-based onto the refusal the merged handler
does reach for that sequence: a rotation naming `sess-B` after an assignment for `sess-A` addresses a
registry entry that does not exist and is refused with `FailedPrecondition` rather than the `NotFound` the
pod-global comparison returned, since under D5 no request can name a session other than the entry's own. The two prior-assignment cases move
onto the empty-lease-set refusal §4.11 adds, driven on a registered, bound slot that received no
`AssignCredentials`, since the registry lookup those handlers open with tests registration rather than
assignment and would admit the call. That pair is the tier-1 pin §4.11 names, and it is what keeps the
fail-closed refusal from being retired with the pod-global branch.

`pkg/adapter/credexpiry_test.go` is a third credential hand-rewrite, and none of its breaks is
compiler-caught. Its base-mode cases drive `AssignCredentials`, `RotateCredentials`, `RevokeCredentials`,
and `ExtendCredentialLease` with `session_id` and no slot (`assignOne` at `:104-112`), so they take the
pod-global branch today and route to the merged per-slot handlers once CODE-1 deletes `useSlot`. Their
assertions read the pod-global credential file through `fileProviders` (`:116-124`) and the pod-global timer
map `s.expiryTimers` (`:164, 196, 215, 273, 313, 379, 460, 513`). Both still compile and both go red at
runtime, the file read outright, once the file moves to `/run/lenny/slots/{sessionId}/credentials.json` and
the timers move to the slot entry's own set (`pkg/adapter/slotcreds.go:44, :49`). Each case re-bases onto the
session's credential file at `slotlayout.Resolve`'s `CredentialsDir` and onto the per-slot timer set, and the
§4.9 arms that must survive the re-base are named rather than left to the rewrite: the timer armed at the
lease's `expires_at`, the expiry fire deleting the provider's entry and emitting AUTH_EXPIRED, revoke
cancelling the timer, extension moving the deadline without rewriting the file, and per-provider
independence within one slot. `TestReleaseSessionCancelsExpiryTimers_spec_4_9` (`:322-343`) drives the
retired `releaseSession` directly and moves onto the slot release path, asserting that a `ShutdownSlot`
stops that session's armed timers. Its slot-specific cases (`:406, 571, 610, 632`) collapse their distinct
session and slot fixtures onto one identifier under D5. Without this disposition a fail-closed
lease-expiry surface changes both its storage location and its timer set with every existing pin silently
red and no listed replacement.

**Tier 1, the inbound JSONL rule.** These cases pin both branches of §4.6.1's resolve-or-reject rule over
the six session-scoped frame types. On a pod holding one slot, a frame carrying no identifier resolves to the
receiving stream's binding and is delivered. On a pod holding two slots with both Attach streams live, the
same frame is rejected, neither stream receives it, and
`lenny_adapter_unaddressed_frame_rejected_total` for the frame's type increments by exactly two, which is
the per-rejecting-stream unit §4.6.1 and SPEC-9's §16.1 row state; the exact count is asserted rather than
a non-zero increment, so the granularity cannot drift from the catalog row. That is the case that would
otherwise relay one session's frame to another. A frame naming a slot the registry does not hold is rejected on both pod
sizes. `heartbeat` and `heartbeat_ack` carrying no identifier pass through on both pod sizes, so the
narrowed demux predicate is asserted not to close the path §4.6.1 keeps open. A `status` frame carrying one
session's identifier reaches that session's Attach stream and no co-tenant's, which pins the membership
decision §4.6.1 records: a predicate defined over the five frames the schema tags today would fan that
frame out to every stream on the pod.

**Tier 1, the existing §28.5.3 addressing pin.** `pkg/adapter/tracingcontext_addressing_test.go` is the
tier-1 file the rule above replaces, and it is a hand-rewrite rather than the one-line field drop an earlier
draft classified it as. Its `openTracingAttach` helper does take the field drop at `:62`
(`req.SlotId = &adapterv1.SlotId{Value: slotID}`), and that one is compiler-caught: SCHEMA-2 and CODE-1
remove `AttachRequest.slot_id`, so `pkg/adapter` stops building until the assignment goes. Three further
edits are what keep the file honest, and none of the three is caught by the compiler.
Every fixture in it pairs a session with a distinct slot (`:234-236`, `:259-261`, `:288-289`, and
`:386-387`, where `"sess-slot-a"` is bound to `"slot-a"`), which is the state D5 makes unconstructible, so
each pair collapses onto one identifier. `TestSetTracingContextTaggedOnSingleSessionPodIsDropped_spec_28_5_3`
(`:334`) inverts: its premise, stated at `:326-329`, is that "no session on a single-session pod holds a slot
id", which D1 retires, so a frame carrying the pod's one session identifier is accepted and registered rather
than dropped, and the case is renamed and restated for that outcome. The `on a slot-bound stream is dropped`
subtest of `TestSetTracingContextUnreadableSlotIDIsTheEmptyAddress_spec_28_5_3` (`:385-398`) runs against a
pod holding exactly one slot and requires the empty address to be dropped, which is the opposite branch of
§4.6.1's resolve-on-one-slot rule, so it is re-based: on a pod holding one slot the unreadable identifier
resolves to the stream's own binding and is delivered, and the rejection case moves to a pod holding two.
The rejections that survive count on `lenny_adapter_unaddressed_frame_rejected_total` rather than on
`lenny_adapter_set_tracing_context_dropped_total`, per CODE-6 and the two §16.1 catalog rows SPEC-9 stages,
so each `requireDrops`
and `requireDropLogs` assertion names the counter its branch now increments. None of these three semantic
edits breaks the build, so nothing surfaces them at application time unless §8 states them.

`pkg/adapter/tracingcontext_sampling_test.go` is the second existing pin of the same rule and is the pin of
its atomicity property, that both terms of condition 2 are read under one `s.mu` hold. It is a hand-rewrite
for three reasons at once. It writes the retired `Server.sessionID` (`:45`, `:50`), so it breaks the
`pkg/adapter` build. It pairs session `sess-slot-a` with slot `slot-a` (`:46`), the fixture D5 makes
unconstructible. Its whole premise is the slotless-stream branch §4.8 deletes: it drives
`tracingFrameAddressesStream(sessionID, "", "")` (`:65`) against a registry toggled between the two states
that branch reads, and asserts the frame is rejected on every sampling. The subject it loses is replaced by
the rule that succeeds it, which reads the stream's binding and the pod's slot count from the same shared
registry: under `-race`, an unaddressed session-scoped frame evaluated while a co-tenant slot is being
registered and released never resolves to a binding the registry no longer holds, and the two terms come
from one sampling. Without the replacement §4.6.1's resolve-on-one-slot rule would ship a two-term read of
the registry with no atomicity pin, which is the property this file exists to hold.

**Tier 1, the demultiplexer's own pin.** `pkg/adapter/attach_test.go` is a hand-rewrite beside the file
above rather than a field drop from the gRPC removal, and none of its breaks is compiler-caught beyond the
two `AttachRequest.SlotId` assignments. It carries its own JSONL decoder, `frameSlotIDForTest`
(`:191-196`), which reads the wire key off a struct tag, so after SCHEMA-2 and CODE-6 it returns the empty
string and every assertion over it fails silently; the tag becomes `sessionId`. Three cases carry subjects
this proposal changes. `TestAttachDemultiplexesConcurrentSlotsBySlotID_spec_6_4` (`:213`) is the tier-1 pin
of the demultiplexer CODE-6 replaces with `demuxSessionOutput`, and it feeds runtime frames spelled
`slotId` (`:256-257`) and asserts on them (`:264`), so after the rename the frames carry no identifier, both
`Recv()` calls time out under §4.6.1's two-slot rejection, and the pin stops testing the demux; its frames
spell `sessionId` and its two sessions collapse onto one identifier each under D5, since `:219-220` pairs
session `sess-slot-a` with slot `slot-a`. `TestAttachNoSlotIDServesBasePath_spec_6_4` (`:290`) pins
absence-as-pod-scope, which D1 retires, and is re-based onto §4.6.1's resolve-on-one-slot branch.
`TestAttachStampsInboundSlotID_spec_6_4` (`:334`, asserting at `:389`) asserts the stamped key, which §4.6.2
makes `sessionId`. The file joins the fixture corpus below for its distinct-identifier pairs.

**Tier 1, the per-slot routing pin.** `pkg/adapter/slot_test.go` is a hand-rewrite for two of its cases
rather than the fixture swap alone, and neither break is compiler-caught once the request literals lose the
field. `TestNoSlotIDKeepsBasePath_spec_6_4` (`:340`) is this file's pin of absence-as-pod-scope on
`StartSession`, stated in its own comment (`:335-339`) as the rule that a frame with no `slotId` "degenerates
to the base claim on the pod-global runtime". D1 retires that premise, so the case is deleted with the
branch it pins: its body drives one `StartSession` and asserts the runtime started, which
`TestStartSessionStartsRuntime` (`:232`) already covers on the merged path. The cross-session arm of
`TestSendMessageSlotRoutesToSlotRuntime_spec_6_4` (`:265-274`) requires `NotFound` and no delivery for a
`SendMessage` naming `sess-slot-b` against `slot-a`; once the request carries only the session identifier
that arm addresses a session bound on the same pod, so the message is delivered and both assertions go red.
It is re-based onto the refusal the merged path does reach, since under D5 no request can name a session
other than its own entry's: a `SendMessage` for a session with no registry entry is refused with
`FailedPrecondition` (`pkg/adapter/slotsession.go:116-121`) and no envelope reaches the shared runtime. The
file's remaining sites, including the `ShutdownSlot` call-site swap at `:290`, take the fixture collapse
stated below.

**Tier 1, the recycle-scrub guard.** §4.11 re-keys Shutdown's recycle-scrub guard from
`s.currentSession() == ""` onto the slot registry's occupancy, and §1.9 names the regression the re-key
prevents. Two cases pin the re-keyed predicate, against whichever message survives §4.5(d). Under the base
case the message is `ShutdownPodRequest`, per SCHEMA-1: one against a pod whose slot registry holds no bound slot,
which runs the whole-pod scrub, and one against a pod still holding a bound slot, which is refused with
`FailedPrecondition` rather than scrubbing a pod that is still serving a session. If review declines the
split, the same pair runs against a recycle `ShutdownRequest`: an empty registry dispatches
`startPodScrub`, and a pod still holding a bound slot takes the per-session path instead, which is the
branch that becomes universally taken if the guard is left on the pod-global read. The existing conformance case
(`tests/tier10_conformance/recycle_scrub_conformance_test.go:272-274`) drives a pod with no registered slot
at all, so it passes before and after the re-key and cannot detect the regression. Both cases carry a
`// spec:` tie to §5.2 and §4.7.

A third case covers the base-mode release sequence SCHEMA-1 states, which is where the split could silently
delete a session's teardown. A pod serving one session at a time is driven through `ShutdownSlot` followed
by `ShutdownPod`, and the case asserts that the final usage report is flushed, the `CH-RUNTIMEOPS` drain
signal is sent, the runtime is closed for that session, `ReportSessionScrub` is emitted for it, and
`startPodScrub` then runs on the `ShutdownPod`. Without the `ShutdownSlot` leg the pod-scrub handler carries
no session, so every one of those steps disappears and the occupancy refusal fires instead; the case fails
in exactly that state.

A fourth case runs the merged handler on a pod holding two bound slots, which is the arm the third case
does not reach. A `ShutdownSlot` for one of the two flushes the final usage report for that session alone,
closes that session's runtime, emits `ReportSessionScrub` for it, and sends no `CH-RUNTIMEOPS` drain
signal, because the co-tenant's entry is still in the registry; the co-tenant's slot, its stream, and its
runtime survive the teardown. A `ShutdownSlot` for the second session then sends the drain signal before
closing that session's runtime, since deregistering it leaves the registry empty. Without the registry-occupancy test the first teardown signals
the shared runtime to terminate and the surviving session loses its runtime, which is why the pair is
pinned rather than inspected.

A fifth case pins the reclaimer SCHEMA-1 adds for a registered-but-unbound entry. A `PrepareWorkspace` for
one session, followed by a bind failure that never reaches `StartSession`, followed by a `ShutdownSlot`
naming that session, leaves the registry with no entry for it and its tree removed, with no
`ReportSessionScrub` for a session that never started. A later session on the same pod then takes
the drain signal on its own `ShutdownSlot`, and an unaddressed inbound frame on that pod resolves rather
than being rejected. Without the reclaimer both stay wrong for the life of the pod, and neither failure
raises an error, because `drainViaLifecycle` swallows its send errors.

A sixth case pins the close the reclaimer discharges, which is the half a reclaimer that only deleted the
entry would leave undone. A pod holding one bound session and one registered-but-unbound entry is driven
through the bound session's `ShutdownSlot`, which withholds the drain and the hard close because the
registry is still occupied, and then through the reclaim `ShutdownSlot` for the unbound entry. The case
asserts that the shared runtime process is closed once the reclaim empties the registry, and that a later
session's `Runtime.Start` on the same pod spawns a fresh process, accepts its connection, and serves it,
rather than returning against a `connected` process that has already exited. Without the close on the
reclaim arm the pod keeps serving sessions against a dead runtime and no send reports it, since
`ReleaseSlotReservation` decrements with `leaked` false. The second half of the assertion holds only
because SCHEMA-1 drops the listener close from `Close`, so it is driven against the real
`SocketRuntimeProcess` rather than a stub: with the close left in place the later `Start` fails in `accept`
(`pkg/adapter/socketruntime.go:285`) with `net.ErrClosed`, and the pod, still claimed while a sibling slot
remains (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:766-769`), fails every session placed on it
afterwards. A seventh case pins the same property on the ordinary teardown, which reaches the same close:
after the last bound session's `ShutdownSlot` on a pod whose claim survives, a later session's
`Runtime.Start` accepts on the listener bound at adapter startup
(`pkg/adapter/socketruntime.go:156-161`). Both carry a `// spec:` tie to §5.2 and §4.7.

An eighth case pins the withholding arm on the two non-socket `RuntimeProcess` implementations, which the
seven above never reach because each is written against `SocketRuntimeProcess` and its active set.
`InProcessRuntime.Close` (`pkg/adapter/embedded.go:188-204`) and `MCPRuntime.Close`
(`pkg/adapter/mcpruntime.go:265`) hold no active set, so SCHEMA-1's `last` parameter is the whole of their
teardown decision, and the arm is reachable: SCHEMA-1 counts every registry entry toward occupancy, so an
embedded-model or MCP-backed pod whose co-tenant entry is mid-workspace-prep passes `last` false. Each
implementation is driven twice, once with `last` true, which ends the loop and closes the output channel as
today, and once with `last` false, which leaves the loop running and the channel open. Without the case the
withholding arm is inferred from the socket implementation's active-set behavior and a `Close` that tears
down unconditionally ships green.

`pkg/adapter/drain_test.go` is the existing tier-1 pin of that drain signal and is a hand-rewrite under the
base case. `TestShutdownDrainsViaLifecycle_spec_15_4_2` (`:16`) claims the pod through the retired
`claimSession` (`:24`) and drives the deleted `ShutdownRequest` (`:28`) to assert the `terminate` frame
(`:36-38`), so it breaks twice over, and its premise that a Shutdown always drains is the premise the
occupancy gate qualifies. The rewrite binds the session's slot, sends `ShutdownSlot`, and keeps the frame
assertion as the registry-emptying arm of the gate; the two `drainViaLifecycle` cases at `:47` and `:56`
keep their subjects. `pkg/adapter/platformmcp_test.go` (`:29, 89, 212, 289`) and
`pkg/adapter/connectormcp_test.go` (`:69, 154`) construct the deleted message only to tear a session down
between cases, so both are `ShutdownSlot` call-site swaps.

`pkg/adapter/podscrub_test.go` is the tier-1 pin of the whole-pod recycle scrub and is a hand-rewrite
beyond the `Server.WorkspaceRoot` swap stated below. It constructs the deleted message at eight sites
(`:195, :242, :292, :334, :366, :405, :442, :682`), none of them a call-site swap. Five bind a session
through `startRecycleSession` (`:161-167`) and then send a recycle request on the same pod
(`TestShutdownRecycleRunsWholePodScrubAndReportsSuccess_spec_5_2` at `:190`,
`TestShutdownRecycleEmptyCleanupCommandsStillReportsSuccess_spec_5_2` at `:238`,
`TestShutdownRecycleVMRestartReportsSuccessNoWithhold_spec_5_2` at `:362`,
`TestShutdownRecycleVMRestartReportsFailedOnDirtyScrub_spec_5_2` at `:400`, and
`TestShutdownRecycleScrubIsAsynchronous_spec_5_2` at `:666`), which SCHEMA-1 turns into a `ShutdownPod`
that the occupancy refusal rejects while a bound slot remains, so under D1 each would hit the refusal
instead of running the scrub. Each sends `ShutdownSlot` for the ending session first, which keeps the five
spec-named §5.2 outcomes they own, the standard profile, the empty-cleanup-commands case, the two
vm-restart cases, and the asynchronous scrub, pinned rather than silently uncovered.
`TestShutdownRecycleConcurrentModeTriggersWholePodScrub_spec_5_2` (`:284`) takes no `ShutdownSlot`, because
it starts no session and registers no slot (`:287-291`), so a `ShutdownSlot` for it would be refused with
`FailedPrecondition` by the merged handler (`pkg/adapter/slotsession.go:64-68`) and the case would fail at
its first call. It moves onto `ShutdownPod` alone, and it is the case pinning the admit arm of the re-keyed
occupancy guard, where an empty registry runs the whole-pod scrub, beside the new refusal case for a pod
still holding a bound slot.
`TestShutdownRejectsRecycleWithEmptySessionID_spec_4_7` (`:331`) pins a non-empty-`session_id` guard on a
recycle request, which `ShutdownPodRequest` cannot express, so it is re-based onto `ShutdownSlot`'s
empty-`session_id` refusal under §4.2. `TestShutdownTerminatePathRunsNoScrub_spec_4_7` (`:435`) pins the
terminate path on a request that after the split carries no recycle field, so it is re-based onto a
`ShutdownSlot` that runs no pod scrub. If review declines the split, the file takes only the field swap.

`pkg/adapter/sessionscrub_emit_test.go` is the tier-1 pin of the §5.2 `ReportSessionScrub` emission and is
a hand-rewrite for the same reason. Its four `shutdownSlotReq` fixtures (`:78-83`) pair a session with a
distinct slot and collapse onto one identifier, and the calls move onto `ShutdownSlot`.
`TestBaseRecycleShutdownEmitsSessionScrub_spec_5_2` (`:216-244`) requires exactly one report on the recycle
request and treats a non-empty slot id as a failure, which is the base-mode empty-slot emission D1 retires;
it is re-based onto the `ShutdownSlot`-then-`ShutdownPod` sequence with the slot assertion deleted.
`TestBaseTerminateShutdownEmitsNoSessionScrub_spec_5_2` (`:256-271`) requires a base-mode terminate Shutdown
to emit zero reports, which SCHEMA-1 inverts by putting the emission on `ShutdownSlot` unconditionally, so
it becomes a case asserting that a retire-disposition `ShutdownSlot` emits exactly one `ReportSessionScrub`.
That inversion is a behavior change in its own right, since a retire release now advances `sessions_served`
where it did not before, and the case states the `maxSessionsPerPod` accounting consequence. Neither edit is
compiler-caught beyond the message construction.

**Tier 1, the MCP teardown on the slot release path.** §4.11 moves `releaseSession`'s remaining work onto
the slot release path, and that path performs no MCP teardown today: `releaseSlot`
(`pkg/adapter/slotsession.go:99-112`) deletes the registry entry and removes the tree, and neither
`mcpCancel` nor `mcpHandshakeSeen` appears in `pkg/adapter/slotsession.go` or `pkg/adapter/slot.go`. The
move is therefore new behavior on that path, and CODE-1's start-side merge starts the pod's platform MCP
server on the §15.4.3 nonce of the session whose `StartSession` started it, so the failure mode §4.11 names
is reachable on every pod. The servers are pod-wide, so the predicate is the departing session leaving the
pod with none other bound: a `ShutdownSlot` on a pod holding one bound slot cancels the platform and
per-connector MCP servers (`pkg/adapter/session.go:387-407`) and clears `mcpHandshakeSeen`, so a later
session on the same pod is not observed at Standard on the prior session's handshake signal, which is the
second case. The non-happy path is a co-tenant: a `ShutdownSlot` for one session on a pod holding two bound
slots cancels nothing, and the co-tenant's MCP servers and its runtime stream survive.

**Tier 1, `pkg/adapter/scrub`, the per-slot purge.** SPEC-4 re-states step 0 and step 6 over the per-slot
credential files and CODE-1 re-points the scrub configuration onto them, so both arms are pinned. On a pod
that held two slots, a whole-pod scrub removes both `/run/lenny/slots/{sessionId}/credentials.json` files
and both `/workspace/slots/{sessionId}` trees, and the removal happens before `cleanupCommands` run, which
is the deployer-code exposure step 0 exists to prevent. The fail-closed arm is the case that would otherwise
ship silently: when one credential file survives the purge, step 6 marks the scrub failed rather than
verifying an absent pod-global path and passing. A third case covers the residue a leaked slot leaves, whose
registry entry is already gone at recycle time, so the enumeration reads the on-disk children rather than
the registry. A fourth case pins the `cleanupCommands` working directory: on a pod that held two slots and
whose two `ShutdownSlot` calls preceded the `ShutdownPod`, the commands run with the workspace base as their
working directory, observe it as `PWD`, and find no per-slot tree under `/workspace/slots` and no per-slot
credential file under `/run/lenny/slots`, which is the state the ordering SCHEMA-1 states produces and the
state SPEC-4 restates `spec/05:453` for. Without it the singular `CleanupDir` CODE-1 adds could be left
empty or pointed at one arbitrary slot root and no assertion would notice.

`pkg/adapter/scrub/scrub_test.go` is a hand-rewrite and takes the package's existing pins with it, because
CODE-1's pluralization stops the package compiling and none of the four cases above can run until it does.
Its eleven `Config` field sites move onto `CredentialFiles` and `WorkspaceDirs`.
`TestRun_DirtyWorkspaceFailsVerify_spec_5_2_436` (`:165`) and
`TestRun_CredentialStillPresentFailsVerify_spec_5_2_436` (`:182`) are re-based over a two-member set with
the fail-closed arm asserting that one surviving member fails step 6.
`TestRun_CredentialPurgedBeforeCleanup_integration_spec_5_2_424` (`:448`) is re-based over the per-slot
credential set. `TestCleanupEnv_spec_5_2_424` (`:423-424`) calls `CleanupEnv("/workspace/current", ...)`,
the retired pod-global value, and moves onto the singular `CleanupDir` carrying the workspace base, so it
and the fourth case above state the same value. A mechanical field-name swap would leave a green suite
asserting a single-file purge and a `/workspace/current` cleanup working directory.

**Tier 1, the coordinator hold.** These cases cover CODE-3's arming. Coordinator loss on a pod whose
sessions hold bound slots arms the adapter's hold, which is the §1.2 defect where the pod-global read armed
none, and every inbound RPC other than `CoordinatorFence` is then rejected with `UNAVAILABLE`, which is the
pod-wide unit `spec/10:57` states and CODE-3 does not change. A second case drives the hold timeout on a pod
holding two bound slots and asserts that both sessions' runtimes are closed and both post-mortems written,
which is the per-member termination CODE-3 states; without it one session is terminated and the
co-tenant's agent process keeps running with no coordinator and no reconciler that reaches it. The case
also asserts that the shared runtime process is closed once, on the last member, which is the teardown
decision SCHEMA-1's `Close` signature takes from the loop, and that one `AdapterTerminating` envelope is
emitted per terminated session, each carrying that session's identifier, which is the arity CODE-3 states. Both cases drive the termination through the registry read CODE-3 takes
when the timeout fires, so a session bound after the arming read is still terminated; tier 7a drives that
interleaving concurrently. The non-happy paths: a
pod whose only slot is registered and not yet bound arms no hold, and a fence exits the hold so a later RPC
is admitted.

**Tier 1, `pkg/gateway/checkpoint`.** No sentinel value reaches the store or the wire, and
`CheckpointStart` carries a populated `session_id`.

**Tier 1, the checkpoint packages, under CODE-5.** Three test files own at least one case each whose
subject is the per-slot scoping CODE-5 deletes, so each is a hand-rewrite rather than a compile-only edit.
`pkg/gateway/checkpoint/checkpointretention/checkpointretention_test.go` carries
`TestRotate_PerSlotIndependent` (`:254-258`), whose `// spec: §12.5` annotation states that "the 'latest 2'
cap applies independently per slot" and whose body inserts three rows under each of `slot-a` and `slot-b`
for one session (`:263`), rotates `slot-a` (`:269`), and requires `slot-b`'s three rows intact (`:277`).
SPEC-9 re-keys that rule onto `session_id` alone, so the case is re-based on the session-keyed cap: one
session's six rows rotate down to two. Every other `Rotate`, `List`, and `HardDelete` call in the file drops
its `slotID` argument, and the `SlotID` member drops out of every `Record` literal.
`pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore_test.go` carries
`TestLatestActiveForSlotIsSlotScoped` (`:305`), which puts two active rows for session `s1` under `slot-0`
and `slot-1` and requires the slot-scoped selector to return the lower-generation row (`:327`). D5 makes
that fixture unconstructible and CODE-5 deletes the method it exercises, so the case is deleted; the
session-wide `LatestActive` assertion it opens with is already covered by the package's own `LatestActive`
cases. The same file's second per-slot case, `TestPutSupersedeScopedToSlot` (`:280-299`), carries a
`// spec: §10.1.7` annotation stating that the supersede predicate is scoped to `(session_id, slot_id)`, and
its body puts two active rows for session `s1` under `slot-0` (`:285`) and `slot-1` (`:287`) and requires
both to survive (`:293-296`). CODE-5 deletes the `row.SlotID` comparisons that predicate rests on, so the
case is deleted as well; the rule it pinned is restated on the session-keyed supersede SPEC-9 states, which
the package's existing supersede cases already assert. Its distinct-identifier fixture goes with the case,
so the fixture rule below carries no entry for it. The `SlotDefault` assertion at `:52` is deleted with the
sentinel, and the remaining `LatestActiveForSlot` calls (`:418`) move onto `LatestActiveForSession`.
This file's tier-2 sibling, `tests/tier2_component/stores/partialmanifeststore_test.go`, is a hand-rewrite
for the same reason rather than a compile-only edit or a fixture swap. It is the Postgres contract pin of
the selector CODE-5 renames: its `"latest active is scoped to the slot"` subtest (`:154-188`) carries a
`// spec: §10.1.7` comment stating that `LatestActiveForSlot` returns the row for the exact
`(session, slot)` the supersede path scopes on rather than the session-wide winner, it seeds two active
partial rows for one session under `slot-0` and `slot-1` (`:163-170`), and it requires
`LatestActiveForSlot(..., "slot-2")` to return `ErrNotFound` (`:186-188`). All three parts fail after
application. MIG-1 re-keys `partial_manifest_active_uniq` on `(session_id) WHERE partial = TRUE AND
deleted_at IS NULL`, so the second `Put` supersedes the first against the post-drop schema and the
two-row fixture is unconstructible rather than collapsible, and the `ErrNotFound` assertion inverts,
because the only call the renamed selector can express is `LatestActiveForSession(ctx, tenant, session)`,
which finds the session's row. The subtest is therefore deleted with the selector it exercises, the
`SlotDefault` assertion at `:93-94` is deleted with the sentinel, and a replacement subtest pins the
post-change behavior against real Postgres: a `Put` of a second active partial row for the same session
supersedes the first under the re-keyed index, `LatestActiveForSession` returns the surviving row, and an
unknown session returns `ErrNotFound`. It carries a `// spec:` tie to §10.1.7 and to §12.5 as re-keyed by
SPEC-9, and it is the only tier-2 case over the session-keyed selector, whose SQL the compiler does not
check. The file leaves the compile-only inventory and the fixture corpus below.

`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go` is a hand-rewrite for the same reason.
`TestDriverSupersedeReleasesTargetSlotPriorRow_spec_10_1` (`:1025`) exists to pin the slot-scoped supersede
lookup: its `// spec: §10.1.7` block and its `// diagnosis:` comment (`:1017-1024`) state that resolving the
prior row through the session-wide selector is the defect, and its two seeded rows pair session `s1` with
`partialmanifeststore.SlotDefault` (`:1041`) and with `"slot-other"` at a higher generation (`:1051`) so the
session-wide winner sits on a different slot. CODE-5 makes that session-scoped resolution the specified
behavior at `uploaddriver.go:406-407`, so the case asserts the inverse of the applied deliverable and a
field drop would leave it doing so. It is deleted, and its §10.1.7 tie moves onto the session-keyed
supersede release the package's remaining driver cases assert. Its distinct-identifier fixture goes with the
case, so the fixture rule below carries no entry for it. The file's other `SlotID:
partialmanifeststore.SlotDefault` literals (`:332, 355, 921, 980`) are compile-only field drops.

**Compile-only edits from CODE-5's store-API change.** These drop a deleted struct member or a dropped
argument and assert nothing about slots:
`pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go:47`,
`tests/tier4_integration/checkpoint_chunk_helpers_test.go:120, 163`,
`tests/tier4_integration/checkpoint_intent_generation_test.go:63, 129`, and
`tests/tier2_component/legalholdreconciler/reconciler_test.go`, whose `seedManifest` helper sets
`Record.SlotID` from a `slot` parameter (`:68, 75`) that its three callers supply (`:145, 167, 203`). The
field, the parameter, and the three arguments are dropped together. The helper's slot values are inert
fixture text that no assertion in the file reads, so the edit carries no subject. This file and
`tests/tier2_component/stores/partialmanifeststore_test.go`, which reads the deleted member at `:93-94` and
`:163` and calls the renamed selector at `:179` and takes the hand-rewrite stated above rather than a
compile-only edit, are together why `tests/tier2_component` does not build
until §8's dispositions are taken.
`pkg/gateway/checkpoint/checkpointer/checkpointer_test.go` takes both an arity drop and a fixture rewrite:
its `fakeRetention.Rotate` signature loses the `slotID` parameter (`:627`) and its `Insert` fake
(`:622`) stops recording `Record.SlotID`, and the bind at `:727` pairs session `"cw"` with `SlotID: "slot-3"` and asserts that slot value
reaches the store at `:735`, so it joins the fixture corpus below and the two slot assertions are deleted
with the field.

**Tier 1, the warm-time layout, under CODE-2 and D9.** `pkg/adapter/warmlayout_test.go` is the tier-1 pin of
the invariant D9 retires, and it is the site D8's cost paragraph in §5 asks to be found. Its file comment
states the subject as "the §6.1 warm-pod invariant: /workspace/current and the staging directory exist (and
current/ is empty) before the pod is claimed" (`:14-16`), and
`TestEnsureWarmWorkspaceLayout_CreatesSubdirs` asserts the emptiness of `current` under a
`// spec: §6.1 — /workspace/current "exists but is empty"` annotation (`:37-44`), which is the sentence
SPEC-3 deletes from `spec/06:11`. `TestEnsureWarmWorkspaceLayout_UnconfiguredDirsSkipped` (`:67`) and
`TestEnsureWarmWorkspaceLayout_RootModeReadable` (`:89`) assert the same `current` leaf's creation and its
`warmWorkspaceRootMode` bits. Five constructions in the file set `Server.WorkspaceRoot`
(`:22, :53, :71, :95, :195`), the field CODE-2 retires, so the file breaks the `pkg/adapter` build before
any assertion runs. It is a hand-rewrite: the three cases are re-based on the post-D8 warm layout against
the workspace base CODE-2 introduces, asserting that `/workspace/slots/` and `/workspace/staging` exist and
are empty, that the slots directory carries the same group and other read and execute bits the runtime
needs, and that no `current` leaf is created. The idempotence case and the shared-assets cases keep
their subjects and take the field swap. The negative case is what D9 needs and no other case supplies:
`EnsureWarmWorkspaceLayout` creates no `/workspace/current`, so a partial application that leaves the
pod-global branch in place turns this case red rather than shipping a path the applied specification says
never exists.

Every other `pkg/adapter` test that sets `Server.WorkspaceRoot`, whether in a composite literal or by
assignment onto a constructed `Server`, takes the same field swap onto the workspace base, because CODE-2
retires the field and both forms break the build. One of them covers a §1.2 defect CODE-2 re-points, so its
subject moves with the field rather than only its construction. `pkg/adapter/exportpaths_test.go` (`:53, 97, 116, 144, 158,
180, 190`) is the pin of the `ExportPaths` root, which CODE-2 re-points at the slot root resolved from
`session_id`, so each case supplies a session identifier and asserts the exported paths land under that
session's slot tree. `pkg/adapter/manifest_test.go` (`:59, 90, 120, 260, 268, 299, 316`) and
`pkg/adapter/manifest_fields_test.go` (`:87, 119, 141`) additionally hard-code the literal
`"/workspace/current"` as the constructed root, so their swap is a value change as well as a field change.
`pkg/adapter/tracing_internal_test.go` (`:47, 69, 92, 113`) constructs a temporary directory, asserts only
on spans, and is a compile-only field swap.

`pkg/adapter/staging_test.go` and `pkg/adapter/files_updated_test.go` are subject re-bases rather than field
swaps, on the same ground as `exportpaths_test.go`. `FinalizeWorkspace` and `RunSetup` resolve their target
from `s.WorkspaceRoot` today (`pkg/adapter/staging.go:179`), and §4.2 and CODE-1 make every root-computing
site resolve through `slotlayout.Resolve` while CODE-2 retires the field for `WorkspaceBase`, so after the
swap both handlers materialize into `<base>/slots/{sessionId}/current` and every assertion that reads the
file back at the swapped root's top level fails at run time with no compiler warning. Each case binds the
session it names and reads its assertions under that session's slot root. In `staging_test.go` those are
`TestFinalizeWorkspaceMaterializes` (`:53`, reading `filepath.Join(root, "docs", "readme.md")` at `:61`),
`TestFinalizeWorkspacePlumsArchivePolicy` (`:117`, stat-ing the symlink at `:132`),
`TestRunSetupExecutesCommands` (`:247`, reading `result.txt` at `:254` and stat-ing `sub` at `:261`), and
the schema-version, unknown-source-type, and path-collision cases at `:154`, `:190`, and `:217`; the two
cases that construct a `Server` with no root and require `FailedPrecondition`
(`TestFinalizeWorkspaceRequiresWorkspaceRoot` at `:87` and `TestRunSetupRequiresWorkspaceRoot` at `:283`)
keep their code and their subject moves from the pod-global field onto the resolution the merged path
fails. In `files_updated_test.go` they are the §7.4 mid-session overlay pin
`TestFinalizeWorkspaceMidSessionOverlaysAndSignals_spec_7_4_433` (`:52`, asserting at `:94` and `:97`) and
`TestFinalizeWorkspacePreStartDoesNotSignal_spec_7_4_433` (`:114`, asserting at `:129` and `:132`). These
are the tier-1 pins of the §4.7 workspace-materialization handlers CODE-1 re-points, so a field swap alone
leaves them red and the behavior unpinned.

The assignment form is concentrated in one helper. `pkg/adapter/session_test.go`'s `sessionServer`
(`:215-222`) builds the package's shared fixture by setting `s.WorkspaceRoot = root` and returning that
`root` as the value its callers compare against, and twelve files in the package call it, so the helper is
rewritten first: it prepares a workspace base, binds one slot for the session under test, and returns that
slot's root. Every caller keeps its subject and inherits the new root. `session_test.go` additionally sets
the field directly at `:411`. The remaining assignment sites take the same swap:
`pkg/adapter/mcpruntime_test.go` (`:256, 308`), `pkg/adapter/embedded_sdkwarm_test.go` (`:40`),
`pkg/adapter/shutdown_demote_test.go` (`:78, 152`), `pkg/adapter/tracing_external_test.go` (`:87`),
`pkg/adapter/sdkwarm_test.go` (`:82`), `pkg/adapter/checkpoint_stream_test.go` (`:82, 236, 517, 564, 613`),
and `pkg/adapter/podscrub_test.go` (`:152, 576, 612, 634, 672`, plus `:468`, which hard-codes the literal
`"/workspace/current"` and so takes a value change as well). Until each takes it, `pkg/adapter` does not
build and no tier-1 case in the package runs.

**The `Server.WorkspaceRoot` holders outside `pkg/adapter`.** The field is exported, and packages across the
gateway, a shared test fixture, four higher tiers, and one `cmd/` binary's test set it on a constructed
`adapter.Server`, so CODE-2's retirement breaks their builds too and each holder takes the same swap onto
the workspace base. Scoping the disposition to `pkg/adapter` would leave those packages unbuildable with no
stated edit, which is the same defect pass 12 corrected inside `pkg/adapter`. The shared fixture is
rewritten first, because other tiers dial their adapter through it:
`tests/testinfra/coordfixture/coordfixture.go:79` prepares a workspace base and binds the named session's
slot, and every caller of its `StartPod` inherits the resulting root. The gateway-side holders are
`pkg/gateway/runtime/adapterclient/client_test.go` (the constructions from `:129`, including `:274, :292,
:420, :960`) and `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:44`;
`pkg/gateway/session/executor/pod_test.go:47`; `pkg/gateway/coordination/barrier/wiring_test.go:43`;
`pkg/gateway/podlifecycle/podsession/binder_archive_test.go` (`:88, :139, :188, :278`),
`binder_phases_test.go` (the constructions from `:80`), `binder_readopt_test.go:119`,
`binder_test.go` (39 sites, from `:329`), `sdkwarm_bind_test.go` (`:101, :135, :184, :210`), and
`one_session_only_test.go:45` in that same package, which is a
different file from the `pkg/adapter/one_session_only_test.go` hand-rewrite staged above; and
`pkg/gateway/sessionserver/start_pod_test.go` (the constructions from `:215`), `create_test.go` (`:175,
:1508, :1579, :1632, :1681, :1723`), `pool_selection_component_test.go` (`:56, :117, :168, :244`),
`pool_exhaustion_queue_test.go:73`, `delegated_child_materialize_test.go:67`,
`resume_external_effect_regression_test.go:76`, `start_pod_lease_component_test.go:101`,
`upload_to_session_test.go:81`, and `recycle_scrub_fold_component_test.go:94`. Above tier 2 the holders are
`tests/tier3_contract/adapter_usage_wired/wired_reportusage_test.go:105`,
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:272`,
`tests/tier4_integration/recycle_scrub_path_test.go` (`:328, :668`),
`tests/tier4_integration/credential_delivery_gate_test.go:128`,
`tests/tier4_integration/checkpoint_grant_remint_test.go:149`,
`tests/tier4_integration/cross_environment_delegation_test.go:724`,
`tests/tier4_integration/delegation_child_materialization_test.go:294`,
`tests/tier4_integration/mcp_runtime_lifecycle_test.go:73`,
`tests/tier4_integration/eager_claim_lifecycle_test.go:221`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go:154`,
`tests/tier4_integration/concurrent_workspace_test.go:109`,
`tests/tier7a_load_local/tracing_context_release_race_test.go:321`,
`tests/tier9_security/adapter_mcp_nonce_test.go:135`,
`tests/tier9_security/credential_delivery_gate_test.go:167`,
`tests/tier9_security/delegation_credential_deny_leakage_test.go:99`,
`tests/tier9_security/delegation_child_materialization_cred_test.go:157`,
`tests/tier10_conformance/recycle_scrub_conformance_test.go` (`:168, :264, :440`),
`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:195`, and
`cmd/lenny-gateway/direct_usage_quota_integration_test.go:146`. Four of them construct the literal
`/workspace/current` leaf as the field's value
(`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:195`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go:154`,
`tests/tier4_integration/concurrent_workspace_test.go:109`, and
`tests/tier4_integration/recycle_scrub_path_test.go:328`), so their swap is a value change as well as a
field change. The rest are compile-only field swaps, except the four this section stages as hand-rewrites
on other grounds: `pkg/gateway/podlifecycle/podsession/binder_test.go`,
`pkg/gateway/sessionserver/recycle_scrub_fold_component_test.go`,
`tests/tier4_integration/concurrent_workspace_test.go`, and
`pkg/gateway/runtime/adapterclient/client_test.go`, whose shutdown-surface rewrite this section states
below. Until every holder takes the swap, tiers 1 through
10 do not build in these packages.

`pkg/adapter/resume_test.go` takes its root from that rewritten helper (`:319, 332, 344, 357`) and drives
the §7.3 step (d) guard through `ExpectedWorkspaceRoot` (`:322, :335, :347`, and `:360, :367` on the
release case), which CODE-2 makes load-bearing for the first time; its matching, mismatching,
empty-expectation, and claim-release cases are restated against a slot root, and the empty-expectation case
keeps its subject because `pkg/adapter/resume.go:61` still compares only a non-empty expectation. Its fifth
case, `TestResumeRejectsANonIdlePod` (`:283`), is the pod-idleness inversion the block above stages, because
`Resume` claims through the same `claimSession` (`pkg/adapter/resume.go:47`) that §4.11 re-keys.

**Tier 3, `tests/tier3_contract`.** `session_id` is the sole per-session discriminator on the gRPC leg,
replacing `tests/tier3_contract/adapter_slot_identity/slot_identity_wire_test.go`. A capture on a pod with
one slot and a restore of that checkpoint resolve the same root, which is the general form of §1.2's first
defect. A `message` frame whose `from.id` names a different session than `sessionId` is delivered to the
session named by `sessionId`, per §4.6.2. On the JSONL leg, an outbound frame in the session-scoped set
carries `sessionId` on a pod of either concurrency, and an inbound frame that omits it is resolved against
the stream's binding on a pod holding one slot and rejected on a pod holding two, which pins §4.6.1's rule
at the wire rather than only inside the adapter. A further case asserts the key the gateway itself emits:
the `message` envelope the executor marshals carries `sessionId` and no `slotId`, and a `tool_call` the
approval gate re-marshals after an approval carries the same key, which is the divergence
`pkg/gateway/session/executor/subprocess.go:370-383` and `pod.go:224-231` would otherwise ship silently
because both decode by struct tag.

**Tier 3, the published JSONL key.** `tests/tier3_contract/adapter_jsonl/set_tracing_context_test.go` is the
tier-3 pin of the property SCHEMA-2 renames, and it is a hand-rewrite rather than a fixture swap. Neither of
its two cases pairs a session identifier with a slot identifier, so §8's fixture rule does not reach it: its
frames carry `slotId` and no session field at all. `TestSetTracingContextFrameCarriesSlotID` (`:44`)
validates the published example and three inline frames whose tagged and empty forms spell `slotId`
(`:55-56`), and `TestSetTracingContextRejectsNonStringSlotID` (`:73`) requires the schema to reject
`"slotId":1`, `"slotId":null`, and `"slotId":{"id":"slot_01"}` (`:78-83`). The second case inverts on
application. The `set_tracing_context` definition declares no `additionalProperties: false`
(`schemas/lenny-adapter-jsonl.schema.json:211-224`; the file's only `additionalProperties` is the `true` at
`:220`, inside `context`), so once SCHEMA-2 renames `"slotId": { "type": "string" }` (`:222`) the three
malformed frames validate as unconstrained extra properties and the case fails its own `want rejection`
assertion, while its sibling passes while asserting a property the schema no longer declares. Both cases are
renamed for the identifier they now pin, both frame sets spell `sessionId`, and the rejection case is
re-based onto the `sessionId` property SCHEMA-2 declares, so a non-string value stays rejected by the
published artifact rather than by nothing.

**Tier 3, the frame key each runtime SDK emits.** The Python and TypeScript SDKs write the wire key as a
string literal (`sdks/runtime/python/lenny_runtime/runtime.py:368, 385` and `tool.py:147`;
`sdks/runtime/typescript/src/runtime.ts:329, 344` and `src/tool.ts:133`), so no compiler catches a missed
rename in either language, and a shipped runtime that keeps emitting `slotId` resolves against the stream's
binding on a pod holding one slot under §4.6.1 and is rejected only on a pod holding more, which is the
silent degradation this proposal exists to close. `tests/tier3_contract/sdks/runtime_sdk_test.go` already
builds and runs an example runtime for each of the Go, Python, and TypeScript SDKs (`:5-24`), so the case
extends that harness rather than adding a per-SDK mechanism. The harness carries no frame plumbing of its
own: it reads only `lenny-compliance`'s named-check JSON report (`:198-212`), so the case is stated against
the Basic-level echo check CODE-6 adds to that binary, which drives the runtime with a `message` frame
carrying `sessionId` and requires the `response` it returns to carry the same value and no `slotId`. Each
SDK's example runtime passes that check, and so does each bundled reference runtime, through the
`echocore`, `streaming-echo`, and `delegation-echo` edits CODE-6 stages for that purpose. The check also
carries its reject arm: a runtime that returns a `response` omitting the identifier it was handed, or
carrying a different one, fails the Basic battery, so the check is pinned to refuse as well as to pass.
Without both halves no case in this section asserts the key an SDK
emits, the echo obligation SPEC-7 stages goes unverified, and the tier-10 battery in
`tests/tier10_conformance/concurrent_slot_conformance_test.go` reaches the Go reference runtime alone.

**Tier 3, the shutdown split.** The split replaces two published messages and one RPC with four messages and
two RPCs, which is a wire-contract change in its own right. A case pins the post-split descriptor:
`ShutdownSlotRequest` carries `session_id`, `reason`, `deadline_ms`, and `coordination_generation` and no
slot or recycle field; `ShutdownPodRequest` carries `recycle`, `reason`, `deadline_ms`, and
`coordination_generation` and no session field; `ShutdownSlotResponse` and `ShutdownPodResponse` each carry
`exited_cleanly` and `exit_code`; both RPCs are declared on `service Adapter`; and none of `Shutdown`,
`ShutdownRequest`, or `ShutdownResponse` survives in the descriptor. `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` is a hand-rewrite
under the base case rather than a fixture swap: all three of its cases are written against `ShutdownRequest`
(`:43`, `:103`, `:161`), and the golden encoding at `:62-70` carries a hand-computed `slot_id` field-4 tag,
so the file does not compile once the message is deleted. Its golden encoding and its `RecycleScrub` round
trip move onto `ShutdownPodRequest`. The fixture swap at `:48` applies only if review declines the split.

**Tier 3, the client-facing surfaces.** These cases cover CODE-7's removals at the HTTP and SSE boundary.
A message request body carrying a `slotId` key is accepted and the key ignored, and the session the message
reaches is the one the route names, so the removal from the SDK payloads does not turn a stale client into
a rejected one and a supplied value addresses nothing. The tool-approval detail and the tool-approval SSE
payload carry no `slotId` key, and the session identifier the enclosing object already carries is
unchanged. The `/start` 422 `SLOT_FAILED` body carries `error.sessionId` naming the session whose slot
failed and no `slotId`, asserted on the one-call `POST /v1/sessions/start` route as well as the two-step
one, since that route's path carries no session identifier and the body is the only place the client can
read one. The client SDK message payload types serialize no
`slotId` key, asserted per SDK against the encoded request body.

**Tier 4.** The end-to-end case C-53 exists to enable: a concurrent pool captures a slot's checkpoint, loses
the pod, and resumes onto a replacement with the workspace intact. This case closes T-4.4.21. A second case
covers the same round trip on a pool with `maxConcurrentSessions: 1`, which under this proposal exercises
the identical path.

`tests/tier4_integration/checkpoint_concurrent_pool_test.go` is a tier-4 hand-rewrite under §4.5(c) and
CODE-5, and it breaks on three staged edits at once. Its file comment and `// spec:` annotation state the
model the proposal retires: the gateway "sends the slot's own slotID on CheckpointStart" and "finalises a
manifest keyed on (session_id, slot_id)" (`:8-10, :35-36`). It calls `receivedSlotID()` (`:65, :68`), the
harness helper at `tests/tier4_integration/checkpoint_driver_harness_test.go:87-88` that reads
`first.GetStart().GetSlotId()`, which SCHEMA-1 removes and which §8 already stages at `:100`. It reads
`recA.SlotID` and `recB.SlotID` on `partialmanifeststore.Record` (`:76, :79`), the member CODE-5 deletes.
And it pairs `sess-a` with `slot-a` and `sess-b` with `slot-b` (`:51-52`), which the fixture rule below
retires. The rewrite addresses the two streams by their session identifiers, drops the two
`receivedSlotID()` assertions with the helper, and states each manifest as keyed on `session_id` alone,
keeping the independence property the case exists to hold: each session's manifest carries its own
`chunk_count` and `workspace_bytes`, its chunk objects live under its own prefix, and neither session's
checkpoint carries the other's content. Staging the harness helper without this file, its only caller,
would leave the tier-4 package uncompilable.

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

`tests/tier5_e2e_kind/checkpoint_resume_test.go:242-247` is the second tier-5 hand-rewrite. It is the
end-to-end assertion of the restore path CODE-2 re-points, and it reads the checkpointed marker by
`exec`-ing `cat /workspace/current/resume-marker.txt` on the resumed pod. D9 removes that path from every
pod, so the case fails on every run after application. The rewritten case reads the marker at
`/workspace/slots/{sessionId}/current/resume-marker.txt` on the resumed pod, where `{sessionId}` is the
resumed session's identifier under D5.

The literal `/workspace/current` survives in four further test files that no deliverable and no sweep
reaches, because SPEC-3's sweep predicate excludes `tests/`:
`tests/tier4_integration/eager_claim_lifecycle_test.go`, `tests/tier5_e2e_kind/eager_claim_e2e_test.go`,
`tests/testinfra/sessiondriver/sessiondriver.go`, and
`tests/tier7a_load_local/scenarios/oversized_request_rejection_recovery/scenario.go:53`. Each is restated on
the slot path with the two files already staged above
(`tests/tier4_integration/concurrent_workspace_test.go` and `tests/tier5_e2e_kind/execution_modes_test.go`),
so the inventory is the set of sites the literal reaches rather than the sites this proposal happened to
enumerate.

**Tier 2, the layout.** A warm pod holds `/workspace/slots` and `/workspace/staging` and no
`/workspace/current`, on a pool of either concurrency, which pins D7 against the branch it removes and D9
against the path it retires. A slot's tree appears at assignment and is gone after cleanup, asserted over
all four trees the adapter creates, since `RemoveTree` removing three of four would leak state a later slot
could read. `tests/tier4_integration/concurrent_workspace_test.go:196-199`, which asserts nothing writes
`/workspace/current` on a concurrent pod, is restated over every pod rather than deleted: the property it
protects outlives the condition it was written under.

**Tier 2, the rendered adapter argv.** `pkg/controller/sandbox/podspec/podspec.go` is what puts the
workspace root into the adapter's command line, and the tier-11 sweep does not reach `pkg/`. A case over the
built pod spec asserts that neither the sidecar-adapter argv nor the embedded-adapter argv names
`/workspace/current`, and that both carry the workspace base CODE-2 states, on a pool of either concurrency.
Without it the operator would keep rendering an adapter that runs against a directory the applied
specification says never exists, and no other case in this proposal would notice.

A second case pins the other half of the embedded argv, that the binary parsing it accepts it. The
embedded-adapter argv is the runtime container's, so a case on an embedded-model pool starts
`cmd/runtimes/echo-embedded` with the argv the builder renders and asserts that the process comes up and
serves a handshake rather than exiting on an undeclared flag. The rendering assertion alone passes while
the binary still declares `--workspace-root`, which is the divergence CODE-2 records as compiler-invisible.

**Tier 1, the persisted workspace root.** `pkg/gateway/sessionserver/workspace_root_persist_test.go` is the
only tier-1 pin of `persistWorkspaceRoot`, and it is a hand-rewrite that no compiler catches, because
CODE-2 changes the meaning of the function's fourth parameter without changing its type or its arity
(`func (s *Server) persistWorkspaceRoot(ctx context.Context, tenantID, sessionID, root string)`,
`pkg/gateway/sessionserver/start.go:3271`). All three cases hard-code the retired `/workspace/current`
literal as both the argument and the expected column value.
`TestPersistWorkspaceRootRecordsFirstValue_spec_7_3_15` (`:20-38`) passes a workspace base and asserts the
stored column holds `<base>/slots/{sessionID}/current`, which is the unit-level pin of the derivation the
tier-2 round trip below checks end to end. `TestPersistWorkspaceRootIgnoresEmpty_spec_7_3_15` (`:43`) keeps
its subject on the empty-base early return. `TestPersistWorkspaceRootDoesNotOverwriteRecorded_spec_7_3_15`
(`:67`) keeps its first-non-empty-wins subject with both its seeded value and its second argument restated
as derived slot roots, which is the property CODE-2 relies on when it puts the derivation in the callee.
Without the disposition the file builds, runs, and fails at runtime on a value the applied specification
says no pod carries.

**Tier 2, the workspace root round trip.** A session started under the uniform layout records
`<base>/slots/{sessionId}/current` in `sessions.workspace_root`, derived from the base the adapter reports
on `NegotiateVersionResponse.workspace_base` and the session identifier, and resumes against it. The
assertion is on the exact value rather than on non-emptiness, and the case drives a session that reaches
ready through the finalize path, because `persistWorkspaceRoot` is first-non-empty-wins and
`pkg/gateway/sessionserver/finalize.go:363` is the site that runs first there. A non-emptiness assertion
would pass on a row holding the workspace base, which is the value CODE-2 makes the adapter report and the
value that fails the §7.3 step (d) guard at resume. That
assertion is what fails if the reporting field is retired with no replacement, because the column is written
only for a non-empty value and the §7.3 step (d) guard is skipped for an empty one. The negative case is the one that would have shipped
silently: a row holding the retired `/workspace/current` is rejected at resume by the §7.3 step (d) guard
rather than being accepted against a slot root, so the migration's completeness is asserted rather than
assumed. A workspace finalization followed by a resume covers the interaction between `promoteStaging`'s
rename of `current` and the recorded root.

A second case runs the same round trip on a `maxConcurrentSessions > 1` pool, which is the arm the finalize
path never reaches (`pkg/gateway/sessionserver/finalize.go:238-240`) and where the column is unwritten
today. It asserts the exact `<base>/slots/{sessionId}/current` value in `sessions.workspace_root` after a
slot bind, so the concurrent half of the chain CODE-2 stages in `slotbinder.go` is pinned. Without
it the guard stays vacuous on exactly the pods §1.2 names and no case notices, because the column stays
empty and both `persistWorkspaceRoot` and `pkg/adapter/resume.go:61` skip an empty value. The exact-value
assertion is also what catches the second failure mode CODE-2 rules out: a slot path that derives the root
onto its own `BindResult` writes a doubled `slots/{sessionId}/current` segment through the persist site's
derivation, and only a case reading the stored string detects it.

**Tier 2, the cwd a pre-connected SDK is pointed at.** A `preConnect` bind against the
`pkg/gateway/podlifecycle/podsession` envtest harness sends a `ConfigureWorkspaceRequest` whose `cwd` is
`<base>/slots/{sessionId}/current` rather than the workspace base the adapter now reports. This is the only
one of CODE-2's two derivation sites whose value never reaches `sessions.workspace_root`, so the round-trip
case above cannot detect it, and the defect it guards is the one §1.2 records: a session pointed at a tree
no slot owns.

**Tier 2, the base-mode release sequence at the producer.** The tier-1 case above drives the adapter through
the two messages and cannot detect a binder that emits only one, so the sending side takes its own case
against the package's envtest harness. A base-mode `Release` on a recycling pool emits `ShutdownSlot` for
the ending session and then `ShutdownPod` carrying the `RecycleScrub`, in that order, and a release on the
retire disposition emits `ShutdownSlot` alone. Without it the ordering SCHEMA-1 states rests on inspection
of `binder.shutdownAdapter` (`pkg/gateway/podlifecycle/podsession/binder.go:1934-1947`) alone.
`pkg/gateway/podlifecycle/podsession/binder_test.go` is a hand-rewrite under the base case: its
`recordingShutdownAdapter.Shutdown` fake takes an `*adapterv1.ShutdownRequest` (`:1167`) that the split
deletes, and `TestReleaseRecyclingSendsRecycleScrubShutdown_spec_5_2` (`:1246`) and
`TestReleaseFailedOnRecyclingPoolSendsPlainShutdown_spec_6_2` (`:1298`) each assert that exactly one
Shutdown reaches the adapter, which the split makes wrong. The file also sets `srv.WorkspaceRoot` at 39
sites (from `:329`) and so takes the CODE-2 field swap stated above. If review declines the split, only
that field swap applies.

`pkg/gateway/sessionserver/recycle_scrub_fold_component_test.go` is the second producer test on that path
and is a hand-rewrite on the same terms. Its `recordingRecycleAdapter` serves the deleted `Shutdown` RPC
over an `*adapterv1.ShutdownRequest` (`:33-45`), and `TestRecycleScrubConfigFoldsEndToEnd_spec_5_2`
(`:184-200`) reads `rec.lastShutdown()` and requires the single captured request to carry the
`RecycleScrub` with pod id `sbx-r`, the two deployer cleanup commands, and the 45-second cleanup timeout,
all of which now ride on the `ShutdownPod` that follows the ending session's `ShutdownSlot`. The fake
serves both RPCs and records each, `lastShutdown` (`:46-53`) returns the recorded `ShutdownPodRequest`, and the
fold assertions move onto it, with the preceding `ShutdownSlot` asserted as the case's second requirement. The file also sets `adapterSrv.WorkspaceRoot` (`:94`) and so takes the CODE-2 field
swap stated above. If review declines the split, only the field swap applies.

The remaining constructions of the deleted message split two ways. Those that end one session and assert
nothing about the teardown are call-site swaps onto `ShutdownSlot`:
`tests/tier4_integration/mcp_runtime_lifecycle_test.go` (`:138, :157`),
`tests/tier9_security/adapter_mcp_nonce_test.go:149`, the two `pkg/adapter` MCP files named in the tier-1
block above, `pkg/adapter/session_test.go` (`:332, :360, :385`, with `:400` taken by the re-based
unknown-session case above rather than by a swap), `pkg/adapter/mcpruntime_test.go`
(`:290, :319`), `pkg/adapter/slot_test.go:290`,
`tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:146`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go:427`, and
`tests/tier10_conformance/recycle_scrub_conformance_test.go` (`:186, :274, :457`, distinct from the
`:378-415` rewrite the tier-10 block states).
`tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` is neither: its two
`ShutdownRequest` entries (`:79`, `:150`) are rows in the per-message fence table it walks, so each becomes
two rows, one per post-split message, matching the two register rows REG-1 adds. The two files that own the
teardown's subject are hand-rewrites, stated in the tier-1 block above.

`pkg/gateway/runtime/adapterclient/client_test.go` is the only pin of the adapter client's shutdown method
surface, and it is a hand-rewrite under the base case beyond the `Server.WorkspaceRoot` swap and the
`capturingAdapter` field drop already stated. Its `recordingAdapter` fake serves the deleted `Shutdown` RPC
over the deleted `ShutdownRequest` (`:544`, `:571`) and serves both new RPCs after the split. Four
`Terminate` cases (`:661`, `:686`, `:699`, `:722`) pin the `reason` and `deadline_ms` population CODE-1
folds into the collapsed `ShutdownSlot` method and SPEC-7 re-points onto the §11.4 `USER_REVOKED` send;
`TestTerminateSendsReasonAndDeadline` asserts `USER_REVOKED` and a 10s deadline (`:661-683`), and that
assertion is what keeps the collapsed method's population pinned. Those four and the two surviving plain
`Shutdown` calls (`:440` and `:527`) re-base onto the collapsed method.
`TestShutdownRecyclePopulatesTheRecycleSubMessage` (`:744-763`) requires the recycle request to carry
session id `sess-r` (`:762`), which `ShutdownPodRequest` has no field for, so the session assertion becomes
an assertion that the request carries the `RecycleScrub` and no session.
`TestShutdownLeavesTheRecycleSubMessageNil` (`:784`) exists to pin "the two dispositions apart on the same
proto RPC" (`:783`), which is the presence-encoded scope D6 retires, so it is deleted with the encoding.
`TestShutdownRecycleSurfacesRPCError` (`:811`) keeps its subject on the `ShutdownPod` RPC. Without this
disposition nothing asserts that the collapsed method populates `reason` and `deadline_ms` or that
`ShutdownRecycle` populates `RecycleScrub` and no session. If review declines the split, the file takes only
the field swap and the `capturingAdapter` drop.

**Tier 2, the resume bind.** `Binder.Resume` returns a bind carrying the pod's concurrency, and a slot on a
pool whose `maxConcurrentSessions` is greater than one,
against the package's envtest harness rather than a fake client, since a completed `Resume` reaches the
kube-apiserver. The case that matters is the one the §7.2 gate was written for and has never been able to
run: a resumed session on a concurrent pool is driven through `PodExecutor.streamFor`. The gate's
post-change predicate is the one CODE-1 states, a non-empty session identifier, so the case is stated on
that predicate rather than on the retired concurrency-conditioned one. The resumed bind carries a session
identifier and is admitted whatever the pod's concurrency and whatever `SlotID` held before, and a bind
whose session identifier is empty is refused with the renamed `ErrSlotIDRequired`. Both arms run on a path
that previously evaluated a zero and never fired. The concurrent arm additionally asserts that the
`active_slots` increment lands on the pod the resumed session is bound to, which is what
`ReserveSlotOnPod` gives and what a pool scan would not: the counter for the sandbox the resume's
`BindResult` names is one higher after the resume, and no other pod in the pool is incremented.

A third case covers the release path of a resumed session on a `maxConcurrentSessions: 1` pool, which is
the pool the reservation is scoped away from. The resumed bind carries an empty `SlotID`, so
`PodExecutor.Release` and `rollbackBinding` take the session-mode branch: the terminal disposition is
recorded and the sandbox is drained, and an adapter teardown that fails still drains the pod rather than
leaving `active_slots` at 1 with the claim intact. The case fails if the reservation is widened to
exclusive pools without re-keying the two presence dispatchers CODE-2 names, and it runs on a harness with
no Redis-backed slot counter, which is the deployment an exclusive-pool-only installation runs.

A fourth case covers the reservation's failure path, which the reservation adds and which no case reaches
today. A resume whose adapter `Resume` RPC fails leaves `lenny:pod:{pod_id}:active_slots` at its pre-resume
value, and a second attempt on the same pod does not double-count. A checkpoint-restore failure is the
retryable case CODE-2 exists to enable, so an uncompensated reservation would leak one slot per attempt and
cap the pod's real capacity with phantom occupancy until a Redis restart rehydrated the counter.

A fifth case covers the bind failure SCHEMA-1's reclaimer depends on. A `materializeSlot` that fails after
`PrepareWorkspace`, on any of its four failure branches, sends a `ShutdownSlot` for the session before it
closes the connection, so the adapter holds no registry entry for that session afterwards, and
`ReleaseSlotReservation` still decrements the counter exactly once. The case asserts both halves, because
the counter half exists today and the adapter half is what this proposal adds, and a later session's drain
on the same pod is what the missing half would break.

`pkg/gateway/sessionserver/messages_component_test.go:315-347` is a hand-rewrite rather than a compile-only
edit, because its premise inverts. `TestMessagesFailsClosedOnConcurrentBindWithNoSlot` (`:326`) builds a
bind with a non-empty session identifier, an empty `SlotID`, and `MaxConcurrentSessions: 4` (`:331-333`) and
requires a non-200 (`:341-342`), which the re-keyed gate admits, so the case turns red at runtime with no
compiler warning. The rewrite states the same fail-closed invariant on the new predicate: a bind carrying
an empty session identifier is refused and no envelope reaches the adapter. The file's other entry in the
compile-only inventory below, `:67`, is the `bindSlots` capture in the fake and is unaffected by this
rewrite.

The same file and `pkg/gateway/session/executor/pod_test.go` each carry a second reader of the renamed wire
key that no compiler reaches, per §4.6.2(i): the anonymous decode structs at
`messages_component_test.go:75-78` and `pod_test.go:208-213` take the tag rename onto `sessionId`, and the
assertions reading what they capture are restated on the session identifier. In
`pod_test.go` those are `:273-274`, `:334-335`, `:361-362`, and `:388-389`; in
`messages_component_test.go` they are `:226-227`, `:269-270`, and `:310-312`. The three that today expect an
empty value on an exclusive pod (`pod_test.go:361-362, :388-389` and `messages_component_test.go:310-312`)
invert, because under D1 the envelope carries a non-empty session identifier on a pool of either
concurrency. Left as they are, the emptiness assertions pass against a decode of a key the producer no
longer emits, and the value assertions fail with no build error.

**Tier 2, the archive containment root.** `materializeSlot` passes the bind's slot root as
`RuntimeAllow.WorkspaceRoot` when `ArchivePolicy.workspace_root` is unset, rather than the deleted
`archive.DefaultWorkspaceRoot` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:269-271`). A second case
asserts the same value on the base-mode `Binder.Prepare` path (`binder.go:892-895`) and on
`rewriteExtractedSources` (`binder.go:1342-1343`), which are the two sites that would otherwise fall back to
a path no pod carries. These are the binder half of the tier-9 containment case, asserted at the value the
binder computes rather than at the extractor's behavior.

**Tier 2, the persistence layer.** `tests/tier2_component/rls/checkpoint_manifest_test.go:150-188` is a
hand-rewrite: the distinct-slot case at `:183-186` is deleted, the `// diagnosis:` comment and the
at-most-one-active-partial invariant are restated on `(session_id)` alone, and the slot argument is dropped
from the `insertManifest` helper. The new drop migration carries its own `migrations/` test asserting the
column is gone and the three re-keyed indexes exist, because
`migrations/0178_checkpoint_manifest_test.go:86-91` and `migrations/session_checkpoints_slot_id_test.go:24-27`
assert against the old migrations' `.up.sql` text, which the drop does not change, so they keep passing and
cover nothing.

Checkpoint retention takes its own case, because the SQL half of CODE-5's edits to
`pkg/gateway/checkpoint/checkpointretention` is the half the compiler does not catch: an insert and a
rotation run against the post-drop schema and both succeed, where a missed `slot_id` in the package's SQL
would fail with an undefined-column error. The rotation case asserts that the "latest 2" limit applies per
session against the re-keyed index, which is the behavior spec/12 §12.5 states after SPEC-9's re-key.

**Tier 8.** CODE-4's rotation merge makes the per-slot branch universal, so the §4.7 Full-level rotation
protocol runs on a path that never carried it. Three cases pin it, and they are the tier the change reaches
that no other block covers. A fault-triggered rotation on a pod holding one slot whose runtime withholds
its in-flight request hits the 300s revocation ceiling, emits `credentials_rotated`, increments
`lenny_credential_rotation_inflight_ceiling_hit_total`, and records the `credential.rotation_ceiling_hit`
audit event. A proactive-renewal rotation on the same pod waits without a ceiling. An unacknowledged
rotation falls through to the standard path after the 60s `credentials_acknowledged` timeout. A fourth case
covers the guard: a Token Service outage during lease extension extends only the rotating session's
enforced deadline.

`tests/tier8_chaos/credential_rotation_ceiling_test.go` is a hand-rewrite rather than a compile-only edit.
Its three cases (`:231`, `:306`, `:367`) drive the real `adapter.Server`'s `RotateCredentials` for a session
that holds no slot (`:248`, `:324`, `:382`), which is the state D1 retires, so each fixture takes a bound
slot and the assertions move onto the merged handler. `tests/tier8_chaos/token_service_unavailability_guard_test.go`
is a hand-rewrite for the same reason: its `AssignCredentials` and `ExtendCredentialLease` calls (`:315`,
`:354`) run on the slotless path. Neither file's requests change on the wire, because both already carry
`session_id`, so the compiler catches nothing here and this is the runtime-only regression class §4.5(e)
carves out.

Three further files are in that class and take the same disposition, because each drives the real
`adapter.Server`'s credential handlers for a session that holds no slot and then reads the bundle back at
the pod-global `filepath.Join(CredentialsDir, credfile.FileName)`, which after CODE-1 moves to that
session's `slotlayout.Resolve` `CredentialsFile` under `<roots.Credentials>/slots/<sessionId>`
(`pkg/adapter/slotlayout/slotlayout.go:151-153`).
`tests/tier4_integration/credential_lifecycle_test.go` sets `adapterSrv.CredentialsDir` (`:167`), assigns
and rotates by `SessionId` alone (`:194-198`, `:232-237`), and reads the file through `credProviderEntry`
(`:504-506`); it is the only end-to-end pin of the §4.7 Full-level assign, rotate, rebind, and revoke
lifecycle against a real `credential_rotation`-capable runtime, which is the protocol CODE-4's rotation
merge makes universal.
`tests/tier4_integration/token_service_unavailability_guard_test.go` sets `s.CredentialsDir` (`:170`),
assigns at `:244`, and reads through `credentialFileHasProvider` (`:195-197`, called at `:254`).
`tests/tier10_conformance/token_service_unavailability_guard_conformance_test.go` sets `s.CredentialsDir`
(`:166`), assigns at `:172-183`, extends at `:195-201`, and reads through `credentialFileBytes`
(`:118-120`); it is the tier-10 conformance pin of the §4.9 adapter enforcement point the staged tier-8
guard file mirrors. Each takes a bound slot for the session it names and each credential-file read moves
onto that session's resolved `CredentialsFile`. None of the three breaks is compiler-caught.

**Tier 9.** A credential rotation on a concurrent pod rewrites only the rotating slot's credential file and
leaves the co-tenant slot's lease intact. This is §1.3, and it belongs in the security tier because the
defect is the loss of the per-slot file separation `spec/06:28` gives as the reason for the per-slot lease:
a rotation for one session today overwrites the credential bundle a co-tenant session's runtime is reading.
The in-flight gate the same line claims per slot stays pod-wide per provider and is pinned by the coupling
case below, on the predicate SPEC-4 restates `spec/06:28` on. The read boundary `spec/13:30` states is
unchanged, per §1.3.

A case beside it pins that a proxy-mode session is still pointed at the §4.9 LLM proxy after CODE-1's
start-side merge. A session assigned a proxy-mode lease receives a manifest whose `llm.deliveryMode` is
`proxy` and whose `llm.baseUrl` is that lease's `proxyUrl`, on a pod of either concurrency, so the runtime's
SDK reaches the provider through the mediated path rather than directly. The case belongs in the security
tier because a null `llm` object moves the session off the proxy, which is where the §4.9 mediation and the
model-governance controls apply, and it fails in exactly the state the retired `s.credLeases` derivation
would leave.

A case beside it pins the coupling CODE-4 records as a limit, since a limit nothing asserts is a limit
nothing detects. On a pod holding two bound slots, a rotation for one session while the co-tenant holds an
outstanding in-flight request for the same provider waits on the pod-global counter and, at the ceiling,
increments `lenny_credential_rotation_inflight_ceiling_hit_total` and records the
`credential.rotation_ceiling_hit` audit event naming the rotating session with the pod's outstanding count.
The case asserts that observed behavior rather than an isolation the code does not provide, so a later
change that gives the gate a session dimension turns it red and is recognized as the fix rather than as a
regression. Lease extension is a second handler with its own cross-slot
defect and takes its own case beside the rotation one: today `ExtendCredentialLease` falls through the
unpopulated slot branch (`pkg/adapter/credentials.go:158`) to the pod-global `s.extendExpiryTimer`
(`:161-163`), so an extension issued for one session re-arms the pod-global expiry timer governing its
sibling slots. The case asserts that an extension for one session on a two-slot pod re-arms only that
session's expiry timer and leaves the co-tenant's enforced deadline unchanged. CODE-4 changes this behavior
and REG-1 flips the lease-extension register row on it, so it is pinned rather than assumed. The case
carries a `// spec:` tie to §6.1 and §4.9.

A second case covers what a session's teardown leaves running. On a pod holding one bound slot, a
`ShutdownSlot` leaves no reachable platform or connector MCP endpoint authenticated with that session's
§15.4.3 nonce, which is the surface an ended session must not keep. On a pod holding two bound slots the
same `ShutdownSlot` leaves the pod's endpoints and the co-tenant's runtime stream serving, because the MCP
servers are pod-wide, and the case asserts that the co-tenant is unaffected rather than that the departing
session's nonce is revoked. That asymmetry is the limit §9 records for the single pod-global manifest and
socket, so it is pinned rather than left to be discovered: one session's teardown must not reach another's
surface, and an ended session's nonce survives on a co-tenant pod only because the manifest and the socket
are pod-wide.

A case beside that one pins which session the shared MCP surface acts as, which CODE-1's start-side merge makes
reachable on a concurrent pod for the first time. On a pod holding two bound slots, a `tools/list` and a
`tools/call` arriving on the pod's platform socket, and a `tools/call` on a connector socket, are refused
with `FailedPrecondition` and forward nothing to the gateway, so neither session's traffic dispatches under
the other's principal. On a pod that has only ever held one bound slot the same calls are forwarded under
that session's identifier, and after the session's `ShutdownSlot` the same calls are refused rather than
forwarded under the ended session. A third arm drives the sequence a live-occupancy predicate alone would
admit and is the case the sticky flag exists for: a pod holding two bound slots for two different users, the
first session's `ShutdownSlot`, and then a `tools/call` on the platform socket and one on a connector socket,
both asserted refused with `FailedPrecondition` and forwarding nothing, rather than dispatched under the
surviving session's principal now that the registry holds one bound entry again. The case carries a
`// spec:` tie to §4.7 and §9.1. Without it a regression that
restores a captured session identifier on the provider, or that drops the sticky flag for the live count
alone, would execute one user's tool calls as another
user's, inside the tenant the pod is pinned to, and no other case would see it.

A third case asserts that no credential file is readable anywhere under `/run/lenny` at the moment a
recycle scrub's `cleanupCommands` execute, on a pod that held two slots. This is the security-tier statement
of the tier-1 purge above: a scrub configured on the retired pod-global path would leave both slots' leases
readable by the deployer's cleanup code, which is a cross-session credential read rather than a bookkeeping
error.

A fourth pair covers the containment root CODE-2 re-points. `ArchivePolicy.workspace_root` feeds
`upload.RuntimeAllow.WorkspaceRoot` (`pkg/upload/upload.go:107-110`), the §13.4 symlink-containment root
the archive extractor validates every uploaded entry's resolved path against, and CODE-2 changes its
default from the deleted `archive.DefaultWorkspaceRoot` to the bind's slot root. The tier-9 case asserts
that an archive entry whose resolved path escapes the slot root is rejected and one inside it is accepted,
so a regression that reinstates the pod-global default is caught as a containment failure rather than
passing unnoticed. The pair runs on a pool with `maxConcurrentSessions: 1`, which is the base-mode
`Binder.Prepare` path the retired default applied to. `tests/tier9_security/tracing_context_session_isolation_test.go`
is a hand-rewrite: its slotless-stream cases at `:365-391` lose their premise, and its distinct-identifier
fixtures at `:242, 260` are unconstructible under D5.

**Tier 7a.** `tests/tier7a_load_local/tracing_context_release_race_test.go` is a hand-rewrite for the same
reason, and takes a second independent break from the gRPC removal.

Two new cases pin SCHEMA-1's drain gate, which is the one decision this proposal adds that reads shared
adapter state and then performs a pod-global side effect. Both run against `pkg/adapter` under `-race`. In
the first, two concurrent `ShutdownSlot` calls on a pod holding two bound slots produce exactly one
`CH-RUNTIMEOPS` drain signal on either interleaving, and it is sent before the last session's runtime is
closed, which is the order SCHEMA-1 states and the order the §15.4.2 drain depends on. A check-then-act
evaluation produces no signal at all on the interleaving where each call reads the registry before either
deregisters, and the case fails in that state. In the second, a `ShutdownSlot` for one session races the
workspace-prep and `StartSession` of a second session on the same pod. No drain signal is sent while the
incoming session's registry entry exists, which is the registered-but-unbound arm SCHEMA-1 counts toward
occupancy, and the case additionally asserts that the shared runtime survives the teardown and that the
incoming session's `StartSession` reaches a live `Runtime.Start`. That second assertion is what fails when
the hard close reads the runtime's own active set rather than the number the drain gate reads: the ending
session is the active set's only member, so `Runtime.Close` would tear the runtime down while the drain was
being withheld to protect it. The same case carries the opposite interleaving,
in which the ending session's critical section observes the registry empty, so `last` is true and the drain
signal is sent, and the incoming session's `Runtime.Start` completes during that send. The property asserted
there is the connection teardown alone: the ending session's `Close` leaves the shared connection open, and
a send on the incoming session's stream succeeds afterwards. That is what fails when `Close` decides the
teardown from `last` alone rather than from `last` and the active-set result together, which is the
conjunction SCHEMA-1 states. The `terminate` frame the drain already sent on this interleaving is a separate
exposure the conjunction does not cover, because a runtime that honours the frame exits and nothing reaps it
(`pkg/adapter/runtimeops.go:481-491`, `pkg/adapter/socketruntime.go:449`), so the case runs against a
runtime stub that ignores `terminate` and asserts nothing about the process being alive. §9 records the
drain gate's read-then-act window as a limit. Both carry a `// spec:` tie to §6.4
and §5.2. The tier-1 pair above drives the two teardowns
sequentially and reaches neither interleaving.

A third case pins CODE-1's once-per-pod MCP start, which is the second decision this proposal adds that
reads shared adapter state and then performs a pod-global side effect. Two concurrent `StartSession` calls
for different sessions on a pod with `maxConcurrentSessions: 2` both reach `Runtime.Start` on either
interleaving, exactly one platform MCP server and one per-connector socket set is bound, neither call
returns `Internal: start platform MCP server`, and neither call cancels the other's servers through the
shared `mcpCancel` or `connectorCancels`. A check-then-act guard fails the case on the interleaving where
both calls read `s.mcpCancel` as nil and the second bind returns `EADDRINUSE`. It runs under `-race` and
carries a `// spec:` tie to §4.7 and §15.4.3. The tier-1 merged-start pair drives the second `StartSession`
after the first has returned and reaches neither interleaving. The same case is run a second time with the
second call being a `Resume` rather than a `StartSession`, because `Resume` is the other caller §4.11's
retirement of `claimSession`'s different-session refusal newly admits onto an occupied pod, and it reaches
`startPlatformMCP` through its own site (`pkg/adapter/resume.go:119`). It carries the same four assertions:
exactly one platform MCP server and one per-connector socket set stay bound, the live server keeps the nonce
of the session whose start bound it, neither call returns `Internal: start platform MCP server`, and neither
call cancels the other's servers. Those are what fail when the guard is applied to the merged `StartSession`
body alone. On the manifest the case asserts what holds on both interleavings, since both calls write the one
pod-global file and neither write is ordered against the other: after both calls return, the file carries
exactly one session's `sessionId` and `mcpNonce`, whichever call's write landed last, so the incumbent's
values do not survive. `Resume` participates in the rewrite because it writes the manifest unconditionally
at `pkg/adapter/resume.go:106`, before it reaches the guarded start. The live platform MCP server keeps the
nonce it was armed with whether or not the file still carries that nonce. This pins the collision §9 records
rather than a behavior CODE-1 claims.

A fourth case pins CODE-3's fire-time registry read against the interleaving the allowlist does not cover.
A `StartSession` for a second session is admitted through `holdStateUnaryInterceptor`
(`pkg/adapter/holdstate.go:245-249`) before the coordinator channel closes and binds its slot after
`enterHoldState` has set `s.hold.active` (`:115`), and the hold then times out. The case asserts that both
sessions' runtimes are closed and both post-mortems are written, and that the shared runtime process is
closed once. A set recorded at arming time omits the late binder, so `Close` on the last recorded member
finds it in the runtime's active set and returns early (`pkg/adapter/socketruntime.go:440-445`), leaving an
agent process running with no coordinator; the case fails in exactly that state. It asserts in addition that
exactly two `AdapterTerminating` envelopes are emitted, one per terminated session, and that each carries
that session's own `sessionId`, which is the per-member attribution CODE-3 states and `spec/10:58` gains a
sentence for. An emitter left to `emitControlEvent`'s single-bound-session stamp produces one envelope with
an empty `sessionId` there, and the case fails in that state. It runs under `-race` with
the hold timer driven through the `HoldAfterFunc` seam (`pkg/adapter/holdstate.go:62-68`) and carries a
`// spec:` tie to §10.1 and §5.2.

**Tier 10, the credential path a runtime resolves.** A Basic-level runtime built on each shipped SDK reads
the manifest's `credentialsPath` and loads the credential bundle at it, on a pool of either concurrency.
This is the case that fails today's code once the pod-global path is retired, because every SDK reads a
construction-time default. The non-happy path is a session with no active lease, where the file is absent
and the runtime starts without credentials rather than failing.

**Tier 10.** `tests/tier10_conformance/concurrent_slot_conformance_test.go` is rewritten rather than
inverted: its whole-pod-default premise disappears under §4.6.1, its cwd assertions at `:225-238` are
restated for a pod holding one slot, and its "a single-session pod's response carries no identifier"
expectation at `:212-222` is retired. `tests/tier10_conformance/recycle_scrub_conformance_test.go:378-415` is
rewritten: it pairs session `slot-sess` with slot `slot-1`, which D5 makes unconstructible, and its
`reported slotId` assertion at `:414` disappears with the field. This is the in-process runtime-adapter
conformance battery, distinct from the generated external-adapter compliance suite §4.5(e) refuses to break.

The bundled-runtime batteries pin the echo obligation against the reference runtimes and take no rewrite,
which is the disposition the obligation needs at this tier.
`tests/tier10_conformance/scaffolds_test.go`'s `TestBasicAdapterProtocol` (`:197`) and
`TestBundledRuntimesEveryPR` (`:340-352`), `tests/tier10_conformance/reference_battery_test.go:41-43`, and
`cmd/lenny-compliance/standard_test.go:73-82` each require every check in the battery to pass for `echo`,
`streaming-echo`, and `delegation-echo`, so they turn red on the new Basic check until CODE-6's runtime
edits land and stay green afterwards. `standard_test.go` derives its expected check total from
`basicCases()` (`:73`) rather than from a literal, so adding one Basic check does not move a count. The
reject arm is stated in the tier-3 block above and runs in the same battery.

**Tier 11.** The documented workspace path matches the specified one. The case is a sweep for the literal
`/workspace/current` over the same directory set SPEC-3's retirement predicate names: `spec/`, `docs/`,
`schemas/`, `charts/`, `cmd/`, `sdks/`, and the served OpenAPI document
(`pkg/gateway/externalapi/openapi/openapi.json`). The scaffold templates under
`cmd/lenny-ctl/runtimescaffold/templates/` and the SDK mirror at `sdks/runtime/go/runtime/types.go:206-209`
fall inside it rather than being checked as a named subset. Any surviving occurrence fails the case, so a
site this proposal did not enumerate is caught rather than shipped. A second sweep runs for
`/run/lenny/credentials.json` per SPEC-4, over `spec/`, `docs/`, and `schemas/`, which is the scope SPEC-4
states for that literal.

`tests/tier11_docs/adapter_metric_catalog_test.go` reconciles
`lenny_adapter_unaddressed_frame_rejected_total` against both catalogs, so CODE-6's counter is documented
in `spec/16_observability.md` §16.1 and `docs/reference/metrics.md` rather than existing only in
`pkg/adapter/metrics.go`.

A further tier-11 case reconciles the frames that carry an identifier: every frame the JSONL schema declares
an identifier property on carries it in both its §28.5.3 block and its `docs/reference/adapter-contract.md`
section, which is what keeps the `status` frame's three statements of itself in agreement after SCHEMA-2,
SPEC-1, and §4.6.2(i) add the property to each.

`tests/tier11_docs/successor_pointer_test.go` gains the `spec/29` §29.10 row SPEC-5 states, so the successor
pointer the reduction leaves behind is gated rather than written once. Its domain is a hand-maintained list
(`:52-55`), and the case fails when the pointer is missing or names no channel identifier.

`tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go` is a hand-rewrite, because it
asserts the removed doc sentences verbatim at `:130-143`.

`tests/tier11_docs/checkpoint_pipeline_consistency_test.go` is a second tier-11 hand-rewrite.
`TestCheckpointManifestColumnSetMatchesMigration0178` extracts the column list from the
`CREATE TABLE checkpoint_manifest` body of `migrations/0178_checkpoint_manifest.up.sql` (`:38` declares
`slot_id`) and requires every non-infrastructure column to be named in a markdown code span inside §10.1
(`:72-113`). SPEC-9 removes every `slot_id` code span from §10.1, `spec/10_gateway-internals.md:153, 157,
163, 167, 171, 189, 192` being its only occurrences in the section, so the gate turns red at the spec
commit and stays red after MIG-1, whose drop migration leaves 0178's `.up.sql` text unchanged. The rewrite
adds a dropped-column set beside `infraColumns` (`:52-55`) carrying `slot_id` with a comment naming the
drop migration, which is the form MIG-1 already uses for `prod_columns_test.go`'s 0112 entry. Re-pointing
the gate's column source away from migration 0178 is rejected, because that file is the gate's only
statement of the created table and every remaining column would lose its bidirectional agreement with
§10.1.

`tests/tier11_docs/recycle_scrub_trigger_consistency_test.go` is a hand-rewrite under the base case in
§4.5(d). It anchors on the two spellings SPEC-7 renames: `requireLine` over §4.7 for the literal
"`Terminate` (proto `Shutdown`)" at `:65` and again at `:139`, and `requireAllContain` over the §5.2 trigger
paragraph for "`Terminate`" and "proto `Shutdown`" at `:78-83`. Both spellings disappear with the split, so the gate turns
red at the spec commit and stays red. The rewrite re-anchors the §4.7 row on `ShutdownPod` and the §5.2
assertions on the renamed trigger sentence, keeping the three recycle parameters and the §4.7-to-§5.2
cross-reference the case exists to hold in agreement. If review declines the split, the file is untouched.

`tests/tier11_docs/redis_key_prefix_registry_test.go` is a second tier-11 hand-rewrite under the base case.
Its `requireAllContain` over the §11.4 full-revoke propagation note anchors on the literal
"publishes the step-2 `Terminate` request" (`:118-121`), which SPEC-7's rewrite of `spec/11:270` removes, so
the gate turns red at the spec commit. The rewrite re-anchors on the `ShutdownSlot` spelling and keeps the
Redis pub/sub fan-out assertion the case exists to hold. If review declines the split, the file is
untouched.

`pkg/adapter/gatewaycontrol/scrubreport_test.go` is the §4.5(b′) producer-side hand-rewrite. It is the only
test whose subject is the field the removal deletes, and neither of its two cases survives as a field drop.
`TestReportSessionScrubReleased_spec_5_2` (`:23, :34-36`) calls the producer with the fourth `slotID`
argument CODE-1 deletes and asserts that the emitted request carries `slot_id` = `"slot-3"` for session
`"sess-1"`, which is both an arity break and a distinct-identifier fixture; the call drops the argument and
the slot assertion is deleted, leaving the pod, session, and outcome assertions. Its replacement asserts
that the emitted `ReportSessionScrubRequest` descriptor carries no slot field, so the case turns red if the
removal leaves the field behind. `TestReportSessionScrubLeakedOmitsSlotWhenEmpty_spec_5_2` (`:42-58`) is
deleted: its premise is that a leaked cleanup "on a single-session pod (`maxConcurrentSessions: 1`)" carries
no slot id, which is the base-mode empty-slot emission D1 retires, and the leaked outcome itself is already
covered by the released case's outcome assertion together with the consumer's leaked case at
`scrubreport_server_test.go`. Its `// spec:` tie to §5.2 moves onto the surviving case.

**Fixture rule, across tiers.** Once D5 is normative, a test fixture that pairs a session identifier with a
different slot identifier encodes an unconstructible state, so every such fixture is rewritten to a single
identifier. The corpus is eleven files. Three are named nowhere else:
`tests/tier4_integration/concurrent_workspace_test.go:131-133` (a judgement rewrite, because `:186` asserts
on-disk path text and `:198-200` asserts that the whole-pod `/workspace/current` was never written, and the
asserted leaf becomes the session identifier under D7),
`tests/tier4_integration/concurrent_delegation_proxy_test.go:182-183` (a literal swap in the
`concurrentSlotSession` fixtures), and `tests/tier10_conformance/recycle_scrub_conformance_test.go:379-390,
414-415`. The other eight are staged above:
`pkg/gateway/checkpoint/checkpointer/checkpointer_test.go:727` (session `"cw"` with `SlotID: "slot-3"`),
`tests/tier4_integration/checkpoint_concurrent_pool_test.go:51-52` (`sess-a`/`slot-a` and
`sess-b`/`slot-b`),
`tests/tier10_conformance/concurrent_slot_conformance_test.go`, `cmd/runtimes/echo-concurrent/main_test.go`,
`tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go`,
`pkg/adapter/tracingcontext_addressing_test.go` (whose four `sess-slot-a`/`slot-a` and
`sess-slot-b`/`slot-b` pairs collapse as part of the tier-1 hand-rewrite stated above),
`pkg/adapter/attach_test.go` (whose `sess-slot-a`/`slot-a` and `sess-slot-b`/`slot-b` pairs at `:219-220`
and `:338-339` collapse as part of the tier-1 hand-rewrite stated above), and
`pkg/adapter/tracingcontext_sampling_test.go` (whose `sess-slot-a`/`slot-a` pair at `:46` collapses as part
of the tier-1 hand-rewrite stated above).

**Compile-only edits from the gRPC removal.** These drop the field from a request literal or an assertion,
because `session_id` already carries the address:
`tests/tier3_contract/adapter_extendcredlease/extend_credential_lease_wire_test.go`,
`tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go`,
`tests/tier4_integration/concurrent_workspace_test.go`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go`,
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go`, `pkg/gateway/session/executor/pod_test.go:200`,
`pkg/gateway/sessionserver/messages_component_test.go:67`, each of the last two carrying, beyond this
compile-only drop, the frame-key decode and the assertion restatements the tier-2 block above states,
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go:74, 102, 124, 146, 151, 212`,
the §4.5(b′) consumer file whose edits the `RecordSessionScrub` arity change forces, and
`pkg/gateway/runtime/adapterclient/client_test.go:1363-1389`, whose `capturingAdapter` records the slot field
on SendMessage and Attach, which is a different fake from the `recordingAdapter` the hand-rewrite paragraph
below stages in the same file.

`tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` is not a compile-only edit. Its
exact-count assertion over `ReportUsageRequest`'s field set is restored to its pre-01d19af0 form per
§4.5(f) and §7, so the count is one lower and the test fails if the removal leaves the field behind.

**Fixture rewrites from the gRPC removal**, where the fixture pairs a slot value distinct from its session:
`pkg/adapter/slot_test.go` (nine sites, `"slot-a"`, on top of the two cases the tier-1 block above states
as hand-rewrites) and
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:48` (`"slot-3"`).
`shutdown_recycle_wire_test.go:48` is a fixture swap only if review declines the split; under the base case
the whole file is the hand-rewrite the tier-3 block above states.
`pkg/adapter/credexpiry_test.go` is a hand-rewrite in the tier-1 credential block above rather than a
fixture swap at its four slot sites.

**Deletions and retargeting tied to §4.5(c).** `pkg/gateway/checkpoint/checkpointer/slotid_test.go:88-93`,
whose whole subject is which requests carry the slot field on the wire and that the sentinel is kept off it;
`pkg/adapter/checkpoint_stream_test.go`; `tests/tier4_integration/checkpoint_driver_harness_test.go:100`
(`first.GetStart().GetSlotId()`, whose declaration at `:87-88` and whose sole caller,
`tests/tier4_integration/checkpoint_concurrent_pool_test.go`, go with it per the tier-4 block above); and
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:143, 153-156`.

**Whole-pod-scrub expectation.** `pkg/gateway/podlifecycle/podsession/slotbinder_test.go:649-651` is restated
against `ShutdownPodRequest` per §4.1, and
`tests/tier2_component/translators/openai_singleshot_lifecycle_test.go:480-481` takes the same treatment.
Both restatements are conditional on the split landing, per §4.5(d). If review declines it, the two tests
keep asserting on `ShutdownRequest` and its `slot_id` presence, and the fixture rewrite at
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:48` is the only edit they need.

## Resolved in adversarial review

### Pass 1 (2026-08-17, automated)

- **SPEC-1's `spec/15` presence-condition citations.** Five of the six cited lines carried no presence
  condition (`:1479` and `:1491` are the stdout-flushing requirement, and `:1480`, `:1490`, and `:1616` are
  blank). The list is now `spec/15:1593` alone, which is the file's only wire-addressing presence
  condition, and the second conditional at `spec/15:2243-2244` is staged by SPEC-3 with its SDK mirror.
- **SPEC-1's `spec/28` list.** `:1624` is §28.6 introductory prose and carried no slot condition; it is
  dropped. The two genuine conditions that were in no deliverable are added: `:795`, the
  `set_tracing_context` frame schema §4.8 rewrites, and `:1682-1683`, the §28.6 restatement that a
  single-session pod's messages carry no identifier. `:795` is also added to §4.8's schema site list.
- **The Basic-level permission on the wire leg.** `spec/28:588` and `:604` state the same permission
  `spec/15:1783` states, and neither was staged with the echo exception. Both are added to SPEC-7, along
  with the reader-facing mirror at `docs/runtime-author-guide/integration-levels.md:23`. SPEC-1 now takes
  the presence condition on `:604`, and under §4.6.2 the key spelling as well.
- **SPEC-3's `spec/15:2268` citation.** That line is the tail of the `ManifestSnapshot` doc comment. The two
  live sites, `spec/15:2016` and `:2243-2244`, replace it, and the verbatim SDK mirror at
  `sdks/runtime/go/runtime/types.go:206-209` is staged with them.
- **The `/workspace/current` retirement inventory.** SPEC-3 stated a partial list while D9 removes the path
  from every pod. The inventory is now a stated sweep predicate over `spec/`, `docs/`, `schemas/`,
  `charts/`, `cmd/`, `sdks/`, and the served OpenAPI document, with the located sites enumerated under it:
  the normative obligations at `spec/04:660` and `spec/05:256`, the docs corpus, the served
  `openapi.json:218, :861`, the published workspace-plan and runtime-ops-events schemas, the proto RPC doc
  comments including `ExportPaths` at `:179`, and the four `lenny-ctl` scaffold templates. §8's tier-11 case
  runs the sweep rather than checking a named subset, and §10 lists the added files.
- **`spec/12` §12.5.** The proposal declared `spec/12` untouched while re-keying the two rules §12.5
  restates. `spec/12:337, 340, 352, 362` are added to SPEC-9 and to §4.9, the retained-object arithmetic is
  restated on the pod's session count, and the "`spec/12` is not touched" sentence is replaced.
- **`spec/16:198`.** The partial-manifest supersede metric row states its condition on `(session_id,
  slot_id)` twice. It is added to SPEC-9 and `spec/16` to §10.
- **CODE-6's rejection counter.** The counter had no name and reached neither metric catalog. It is named
  `lenny_adapter_unaddressed_frame_rejected_total`, labeled by `frame_type`, with its `spec/16` §16.1 row
  and its `docs/reference/metrics.md` mirror staged, the §8 tier-1 case asserting the named counter, and a
  tier-11 catalog assertion. `spec/16:188` and `docs/reference/metrics.md:179` are restated with it, because
  §4.8's rewrite changes what that row describes.
- **The Go surface of the dropped columns.** MIG-1 dropped `session_checkpoints.slot_id` and
  `checkpoint_manifest.slot_id` while every reader and writer survived unstaged in
  `pkg/gateway/checkpoint/checkpointretention` and `partialmanifeststore`. CODE-5 now stages both packages
  field by field and query by query, re-points `uploaddriver.go` onto a session-scoped selector, and §8
  gains a tier-2 retention case, since none of these edits fails to compile.
- **§4.1's classification.** The section carried two prose lists rather than a table, named an RPC and two
  unnamed paths as entries, and omitted nine declared request messages. It is now an actual table, one row
  per request message the proto declares on each service, with a direction column, and §8 defines the
  gate's in-scope predicate against the proto.
- **SPEC-8's comment enumeration.** Three comments state the misattribution in the words "adapter-assigned"
  and were unlisted, two of them in files CODE-1 edits. `client.go:820`, `subprocess.go:376`, and
  `pod.go:149` are added, and §1.6 and SPEC-8 now say nine rather than six.
- **SPEC-4's credential-path radius.** Unifying the credential path onto the per-slot tree retires
  `/run/lenny/credentials.json`, which the recycle scrub's step 0 and step 6 verification are keyed on.
  `spec/05:453, 459, 469`, the `credentials_rotated` frame contract at `spec/28:1048` and its published
  example, the descriptive sites across `spec/` and `docs/`, and the chart webhook comment are added, and
  the tier-11 sweep covers the literal.
- **`spec/07:333`.** The paragraph scoping the per-slot inbox and the `SLOT_ID_REQUIRED` invariant to
  concurrent pods was staged only for its ordinal. SPEC-1 now deconditions both statements, matching what
  CODE-1 makes the gate do and what §4.4 makes the inbox.
- **REG-1's closing sentence.** `tests/claim-map.json:68-75`, the `ABSENT` "Checkpoint restore onto a
  concurrent pod" row, names the defect CODE-2 fixes and does not retire with any field. It is staged as
  moving to `WIRED` with a corrected surface and no `deferral_id`, and the closing sentence names the rows
  that change.
- **`spec/05:558`.** The client-visible exhaustion error names `error.slotId`, which CODE-7 drops from the
  emitted body. SPEC-7 restates the sentence on the session identifier, and CODE-7's disjunction is
  resolved to dropping the key.
- **`spec/15:2283`.** The §15.7 `Message` doc comment names `slotId` among the envelope's fields. It is
  added to §4.6.2(i) and SPEC-6, renamed together with the SDK type it mirrors.
- **`spec/15:1590`.** Two deliverables cited a blank line for the envelope field description. Both now cite
  `:1593`, and SPEC-7 is named as the owner of the replacement description while SPEC-1 owns the removal of
  the condition in the same sentence.
- **The `ShutdownSlot`/`ShutdownPod` split.** SCHEMA-1 named the two messages and nothing else. It now
  states both field sets and numbers, the two RPCs and their shared response, the reason a oneof is
  refused, the split of the adapter handler at `pkg/adapter/session.go:225-245`, and the gateway callers,
  and SPEC-7 stages `spec/04:676` and `spec/05:457`'s `Terminate` naming under the base case.
- **CODE-1 and `client.go:900`.** That site is the sole population path for `ShutdownRequest.slot_id`, the
  one presence discriminator on the leg. It is carved out of CODE-1's blanket removal and routed to
  §4.5(d), which states its fate under both branches of the split.
- **CODE-4's lease-extension half.** §8 covered rotation only. A tier-9 case is added asserting that an
  extension for one session on a two-slot pod re-arms only that session's timer, which is the behavior
  CODE-4 changes and REG-1 flips a register row on.
- **The recycle-scrub guard's re-key.** §4.11 re-keys the guard and §1.9 names its failure mode, with no
  test. A tier-1 pair is added covering an empty registry and a still-bound slot, since the existing
  conformance case drives a pod with no registered slot and passes either way.
- **CODE-2's archive containment root.** Changing `ArchivePolicy.workspace_root`'s default changes the
  §13.4 symlink-containment root. A tier-2 case on the binder's computed value and a tier-9 case on
  acceptance and rejection at the extractor are added.
- **SPEC-7's `Terminate` citation.** The line staged for the `ShutdownPod` renaming was `spec/05:459`, which
  is the credential purge SPEC-4 stages and names no RPC. The sentence naming `Terminate` as the whole-pod
  scrub trigger is `spec/05:457`. SPEC-7 now cites `:457` and states that `:459` is SPEC-4's.
- **D11's comment count.** D11 still said six code comments after §1.6 and SPEC-8 were corrected to nine. It
  now says nine.
- **The recycle-scrub guard under the shutdown split.** SCHEMA-1 assigned the guard to the `ShutdownSlot`
  handler, which carries no recycle disposition to test, so the guard was unconstructible under the base
  case and §8's tier-1 pair was written against the deleted `ShutdownRequest`. SCHEMA-1 now states the
  guard's form on each branch of §4.5(d): with the split landed `ShutdownPod` scrubs without a session test
  and the occupancy test becomes a refusal when a slot is still bound, and without the split the re-keyed
  predicate is the whole of the guard. §8's pair is restated against whichever message survives.
- **The tier-11 sweep's scope.** The `/workspace/current` case named a subset of SPEC-3's predicate while
  claiming to be the predicate, omitting `charts/`, `sdks/`, and all of `cmd/` outside the scaffold
  templates, which would have missed the SDK mirror at `sdks/runtime/go/runtime/types.go:206-209` and the
  reference runtimes. The case now sweeps the same directory set SPEC-3 states, and the
  `/run/lenny/credentials.json` sweep is stated separately over `spec/`, `docs/`, and `schemas/`.
- **SPEC-4's credential-path enumeration.** Four located sites were in no deliverable and would have turned
  the new credential sweep red: `docs/operator-guide/security.md:197`,
  `docs/runtime-author-guide/lifecycle.md:282`, `docs/getting-started/concepts.md:584`, and
  `schemas/lenny-adapter.proto:996`, along with the adapter's rewrite obligation at `spec/28:1064`. All are
  added, `security.md` is added to §10, and the runtime SDK and reference-runtime construction-time
  defaults are recorded as a limit in §9 rather than swept.

### Pass 2 (2026-08-17, automated)

- **§29.10's unstated-gap count.** The cited passage records five gaps rather than four, and after CODE-3
  closes the hold-state gap four remain rather than three. The fifth bullet, the `CH-MSGSOCK` buffering and
  replay policy at `spec/29:1564-1565`, states its gap for a pod of either kind, so it is not a co-tenancy
  gap and had no destination under SPEC-5's split. §3 now cites `:1541-1565` and says five, §4.4 names the
  three that stay with the co-tenancy half, and SPEC-5 moves the `CH-MSGSOCK` bullet with the addressing
  mechanisms to the section that owns that channel.
- **`spec/05:542`'s Postgres scoping key.** The per-slot checkpoint cap selects
  `last_checkpoint_workspace_bytes` "for the `(session_id, slot_id)` pair in Postgres", which MIG-1 leaves
  naming a pair no table carries, and it drives a CRD-webhook floor rather than describing one. The line is
  added to §4.9 and SPEC-9, restated on the session, with a note that SPEC-6 edits the same line for
  identifier order so the two land together.
- **The Token Service revocation path.** `pkg/gateway/credassign/` does not exist. Both citations, in §1.3
  and in §9's open question, now name `pkg/gateway/credentials/credassign/client.go:306`.
- **§4.7's placeholder-rename sites.** `spec/06:30` is the SDK-warm paragraph and `spec/05:517` is the
  cross-tenant prohibition; neither carries a `{slotId}` placeholder. The two paragraphs that do are
  `spec/06:28` and `spec/05:515`, and the list now names them.
- **Two `spec/28` sentences naming the frame field.** `:838`, the §28.5.3 non-guarantee paragraph, and
  `:1767`, the `CH-MSGSOCK` row of the §28.8 failure-mode table, both name `slotId` and sit outside every
  staged range, and `:1767` restates the concurrency condition D1 retires. `:838` is added to §4.8 and
  `:1767` to SPEC-1.
- **The fourth Basic-level ignore permission.** `docs/reference/adapter-contract.md:158` states the same
  permission under the `message` frame's field table and was in no deliverable, so the reference page would
  have contradicted the rewritten `spec/15:1783` row. It is added to SPEC-7's exception list.
- **The base-mode release path under the shutdown split.** `binder.shutdownAdapter` sends one `Shutdown` or
  `ShutdownRecycle`, and that single request drives the whole per-session teardown inside the handler
  (`pkg/adapter/session.go:246-286`). Routing it to a session-less `ShutdownPod` deleted the usage flush,
  the drain signal, the runtime close, and the `ReportSessionScrub` emission, and, because under D1 every
  session holds a bound registry slot, tripped the stated occupancy refusal on every base-mode recycle.
  SCHEMA-1 now states that the base path sends `ShutdownSlot` for the ending session before `ShutdownPod`,
  that `ShutdownSlot` carries the usage flush and the drain signal `shutdownSlot` lacks today, and §8 pins
  the sequence.
- **The pod builder that renders the workspace root.** `pkg/controller/sandbox/podspec/podspec.go`
  hard-codes `--workspace-root=/workspace/current` into both adapter argv sites and defers the per-slot tree
  in the comment above each, and it was in no deliverable and outside the tier-11 sweep's `pkg/`-excluding
  scope. CODE-2 now stages both argv sites, both comments, and the `--workspace-base` the adapter takes in
  their place, §10 lists the file, and §8 adds a tier-2 case over the built pod spec.
- **The whole-pod scrub's purge paths.** SPEC-4 re-stated step 0 and step 6 over the per-slot credential
  files while `scrubConfig` (`pkg/adapter/podscrub.go:104-110`) kept configuring the scrub from
  `/run/lenny/credentials.json` and `/workspace/current`, so after application the scrub would verify two
  absent paths and pass while every per-slot tree survived the recycle. CODE-1 now stages `podscrub.go` and
  `pkg/adapter/scrub/` over the per-slot sets, §10 lists both, and §8 adds a tier-1 pair covering the purge
  and its fail-closed verification plus a tier-9 case over what is readable when `cleanupCommands` run.
- **The `status` frame.** The session-scoped set was defined as the five frames the schema tags today, which
  left the sixth outbound content frame outside the narrowed demux predicate and would have relayed one
  session's status to every co-tenant stream, where the current identifier filter drops it. The set is now
  defined by the frames that address a session, `status` is a member, and SCHEMA-2 adds `sessionId` to it.
  §8 pins the delivery.
- **SPEC-5's retitle and its inbound link.** The deliverable never stated the new §29.10 heading, and a
  retitle moves the slug `spec/README.md:290` links to, which `tests/tier0_static/fragment_link_test.go`
  resolves. SPEC-5 now states the heading text and its slug, stages `spec/README.md:290` and the prose
  reference at `spec/29:17`, and records that no anchor-redirect entry is required because those are the
  only references to the retired slug.
- **REG-1's synthetic-proto update.** Two refusal cases in
  `tests/tier0_static/claim_register_proto_agreement_test.go` (`:238-249`) depend on the synthetic
  `InterruptRequest` declaring `slot_id`, so removing it makes both produce zero findings and fail at
  `:269-271`. REG-1 now stages both, re-pointed at `coordination_generation`, and drops its claim that
  nothing it stages turns red.
- **The shutdown split's wire coverage.** The split replaces one message and one RPC with four and had no
  tier-3 case, while the one existing tier-3 shutdown test is written against `ShutdownRequest` including a
  golden encoding carrying the `slot_id` tag, so the staged fixture swap was not the edit it needs. §8 adds
  a tier-3 case over the post-split descriptor and states the file as a hand-rewrite under the base case.
- **SCHEMA-1's shutdown-caller line number.** The two sentences added above for the base-mode release path
  cited `slotbinder.go:539` for the `ShutdownSlot` send, which disagreed with §4.5(d) and with the file: the
  call is at `pkg/gateway/podlifecycle/podsession/slotbinder.go:538` and `:539` assigns `leaked`. Both
  occurrences in SCHEMA-1 now read `:538`.

### Pass 3 (2026-08-17, automated)

- **CODE-2's resume-claimer path.** `pkg/gateway/podlifecycle/podsession/podclaim/` does not exist. The
  claimer is at `pkg/gateway/podlifecycle/podclaim/claimer.go:63-73`, beside the `slotclaimer.go` the
  proposal already cites correctly. CODE-2 takes the corrected path and §10 lists
  `pkg/gateway/podlifecycle/podclaim/` as its own entry rather than as a child of `podsession/`.
- **§4.2's staging-root symbol.** `slotStagingDir` exists nowhere in the tree. The function at the cited
  location is `resolvePrepareStagingDir` (`pkg/adapter/staging.go:127-140`), whose `s.useSlot(slotID)`
  branch at `:128` is the one §4.2 removes. The name is corrected.
- **MIG-1's lint citation.** `scripts/lint-migrations.sh:17-20` is check 4, the expand-contract
  `NOT NULL`/`DEFAULT` rule. The every-migration-is-tested rule MIG-1 relies on is check 3 at `:11-16`, and
  the citation now names it.
- **CODE-3's unit.** The deliverable partitioned the adapter's hold state per slot while `spec/10:57` and
  `spec/28:250` state the hold of the adapter and the pod-wide interceptor at `pkg/adapter/holdstate.go:246`
  enforces it that way, and no deliverable staged either sentence. CODE-3 is scoped to the arming source,
  which is the §1.2 defect, and the hold stays pod-wide with the pod-scoped `CoordinatorFence` exiting the
  one hold. The §29.10 partitioning gap therefore stays open: §3 records it, §4.4 counts four of five gaps
  staying with the co-tenancy half, SPEC-5 follows, and §8's hold cases assert the pod-wide unit. This
  supersedes the pass-2 entry that counted the gap as closed.
- **§29.4's restatement of the `Terminate` RPC.** `spec/29:690-696` restates the §4.7 row nearly verbatim,
  including the claim that one request carries both dispositions, which SCHEMA-1's split makes false. It is
  added to SPEC-7's base-case list with the same carve-out the other two sentences carry.
- **The gateway's own JSONL structs.** `messageEnvelope`
  (`pkg/gateway/session/executor/subprocess.go:370-383`), its population at `pod.go:100-107`, and
  `toolCallFrame` (`pod.go:224-231, 269-275`) carry the renamed wire key as a `json` tag and were in no
  rename deliverable, so the gateway would have emitted a key the schema no longer declares and decoded one
  the runtime no longer emits. All three are added to §4.6.2(i) and CODE-6, and §8's tier-3 block pins the
  emitted key.
- **The tier-5 checkpoint-resume assertion.** `tests/tier5_e2e_kind/checkpoint_resume_test.go:242-247` reads
  the restored marker at `/workspace/current`, which D9 removes from every pod, and the tier-11 sweep
  excludes `tests/`. It is staged as a tier-5 hand-rewrite, and the four further `tests/` files carrying the
  literal are inventoried with it.
- **The §15.4 manifest on the merged start path.** D1 routes every `StartSession` through
  `startSessionSlot`, which writes no manifest, resolves no connectors, and generates no MCP nonce, so no
  session on any pod would receive the manifest its runtime reads at startup. CODE-1 gains the start-side
  counterpart of SCHEMA-1's teardown merge: the two branches become one body carrying the registry claim,
  `Runtime.Start`, the connector resolution, the manifest write, and the MCP server start. The manifest
  keeps its fixed pod-global path, because that path is a runtime-facing contract the SDKs, the scaffold
  templates, and the compliance suite read, and §9 records the single-file collision on a concurrent pod.
  §8 adds a tier-1 pair.
- **The `status` frame's other two statements.** `spec/28:779-783`, the §28.5.3 frame block, and
  `docs/reference/adapter-contract.md:302-308`, the reference section, both show an identifier-free frame
  the published schema will declare `sessionId` on. The first is added to SPEC-1 and the second to
  §4.6.2(i), and §8's tier-11 block gains a reconciliation over every frame the schema declares an
  identifier property on.
- **The SDK `status` emitters.** §4.6.2(i) claimed the SDKs stamp the identifier on a `status` frame "on the
  same lines that stamp it on the other outbound frames". No such lines exist: the Go SDK's
  `outboundStatus` (`sdks/runtime/go/runtime/types.go:323-328`) carries no identifier member and has no
  builder, and the Python and TypeScript SDKs model no `status` frame. The bullet now states the tree's
  actual state, adds `types.go:323-328` to the Go line list, and §9 records the two absences.
- **CODE-6's `spec/16` rows.** Two `spec/16` edits were staged inside a code deliverable, which the pipeline
  cannot apply: staged specification edits land in their own commit and the guard hook then blocks `spec/`
  writes for the code phase, so the new §16.1 catalog row and the `:188` restatement would never be written
  and `tests/tier11_docs/adapter_metric_catalog_test.go` would fail on the registered counter. Both move to
  SPEC-9, CODE-6 keeps the registration and the `docs/reference/metrics.md` mirror, and §10 follows.
- **The tier-11 recycle-trigger gate.** `tests/tier11_docs/recycle_scrub_trigger_consistency_test.go`
  anchors on the literals "`Terminate` (proto `Shutdown`)" (`:65`, `:139`) and on "`Terminate`" and
  "proto `Shutdown`" in the §5.2 trigger paragraph (`:78-83`), all of which SPEC-7's renames remove. It is
  staged as a hand-rewrite under the base case, untouched if review declines the split, and listed in §10.
- **`StartSession`'s not-idle refusal.** `claimSession` (`pkg/adapter/session.go:358-372`) enforces
  `Unavailable` for any second session, and re-basing it on the registry admits a second, different session
  because under D5 that session resolves to its own key. §4.11 now states the re-based predicate and why the
  ceiling stays with the gateway, §9 records the limit, §8 pins both arms, and
  `pkg/adapter/one_session_only_test.go` is inventoried as a hand-rewrite.
- **`ConfigureWorkspace`'s idempotency and conflict contract.** `claimSessionForConfigure` reports `fresh`,
  which gates the manifest write and the MCP start, and the registry's registered-but-unbound state makes an
  existence-keyed `fresh` wrong. §4.11 derives `fresh` from the bound state, states what `releaseSession`
  becomes, §8 adds the idempotency, different-session, and release-on-failure cases, and
  `pkg/adapter/sdkwarm_test.go` is inventoried as a hand-rewrite.
- **`ConfigureWorkspace`'s different-session arm, correcting the entry above.** The pass-3 rewrite restated
  only `fresh` and left `claimSessionForConfigure`'s second behavior unstated: the `Unavailable` for a
  different session on an already-claimed pod (`pkg/adapter/sdkwarm.go:294-297`, the arm its doc comment at
  `:285-289` names beside idempotency). §4.11 now states that the arm is retired on the same grounds as
  `claimSession`'s, §9's limit names both RPCs, and §8 adds a tier-1 case asserting that a
  `ConfigureWorkspace` for a second session is admitted onto its own slot with `fresh=true`.
- **§8's test-inventory claim, correcting the entry above.** "Their subjects survive as the cases above" was
  false for three of the five cited cases. `one_session_only_test.go:110-122`
  (`TestClaimSessionRejectsConcurrentSession_spec_6_1`, F-6.1.12), `sdkwarm_test.go:118-121`, and
  `sdkwarm_test.go:149-159` (`TestConfigureWorkspaceDifferentSessionUnavailable_spec_4_7`) all assert the
  pod-idleness refusal §9's limit retires. The sentence now separates the two idempotency and
  release-on-failure subjects, which survive, from the three refusal cases, which are inverted to assert
  admission.
- **CODE-1's start-side citation.** `session.go:123-135` covers the connector resolution and the manifest
  write but neither MCP start, which is the half of the merge the paragraph argues is fatal to lose. The
  citation now names the connectors at `:126`, the manifest write at `:128-143`, and the MCP starts at
  `:149-159`, and the deliverable states that the merged body keeps the `RuntimeKind != RuntimeKindMCP`
  guard at `:149` around them.

### Pass 4 (2026-08-17, automated)

- **`spec/11`'s full-revoke propagation.** §11.4 states the mechanism in terms of the `Terminate` RPC the
  shutdown split retires, at `spec/11:263` and three times at `:270`, one of them hyperlinked to a §4.7 row
  that would no longer exist, and `spec/07:49` names the same RPC in the §7.1 lifecycle sequence. None was
  in a deliverable, in a sweep predicate, or in §10. All four are added to SPEC-7's base-case list on
  `ShutdownSlot`, `spec/11` is added to §10, and
  `tests/tier11_docs/redis_key_prefix_registry_test.go`, whose `requireAllContain` anchors on the retired
  spelling at `:118-121`, is staged as a tier-11 hand-rewrite.
- **The producer of the recorded workspace root.** CODE-2 retired `Server.WorkspaceRoot` while
  `NegotiateVersionResponse.workspace_root` (`schemas/lenny-adapter.proto:1664-1671`,
  `pkg/adapter/server.go:389-397`) is its only producer, so `sessions.workspace_root` would never be written
  and the §7.3 step (d) guard would be skipped for every session, which is the vacuity CODE-2 claims to fix.
  SCHEMA-1 now reserves field 5 and adds `workspace_base = 6`, CODE-2 states the gateway-side derivation of
  `<base>/slots/{sessionId}/current` through the `negotiated`, `BindResult`, and `PrepareResult`
  propagation, §10 lists the sites, and §8's tier-2 round trip asserts a non-empty slot root.
- **The base-mode symlink-containment root.** `archive.DefaultWorkspaceRoot` has three consumers and CODE-2
  staged one. `pkg/gateway/podlifecycle/podsession/binder.go:892-895` and `:1342-1343` are the base-mode
  `Prepare` and `rewriteExtractedSources` fallbacks, which after D7 and D9 canonicalize §13.4 symlink
  targets against a path no pod carries. Both are added, the constant is deleted rather than re-pointed,
  since a package-level string cannot hold a per-session root, and §8's tier-2 and tier-9 cases cover the
  base-mode path.
- **`spec/26:44`'s shared-assets qualifier.** §3 attributed its removal to SPEC-3, which staged only the
  §6.4 paragraph, so the applied specification would have contradicted itself. SPEC-3 now names the line and
  its restatement, §3 says both sites, and §10 records the correction.
- **The credential path the runtime SDKs read.** SPEC-4 retired `/run/lenny/credentials.json` and excluded
  the SDK defaults on the ground that the live path arrives on the `credentials_rotated` frame. No SDK reads
  that member (`sdks/runtime/go/runtime/lifecycle.go:246-256`), the frame is Full-level only
  (`spec/15:1778`), and all three SDKs read the file at startup at every level, so every shipped runtime
  would read a file that exists on no pod. The resolved path is delivered on the §4.7 adapter manifest, the
  existing per-session runtime-facing surface, rather than on a new one: SPEC-4 stages the `spec/04:767-792`
  field-set row and the `spec/04:796` Basic-level restatement, CODE-4 stages the adapter member and the
  three SDKs, §9's limit is replaced with the manifest dependency, and §8 adds a tier-10 case.
- **The operation lock's other three restatements.** `spec/28:291-294`, `spec/28:1651-1658`, and
  `spec/29:805-806` key checkpoint admission on `slotId` and carry the single-session special case, both of
  which application falsifies. All three are added to §4.10 and SPEC-6, restated on the session identifier
  with the conditional dropped.
- **The generation-fence register rows.** SCHEMA-1 deletes `ShutdownRequest` and creates two messages each
  declaring `coordination_generation`, which orphans `tests/claim-map.json:404-410` and leaves the fence
  loop at `tests/tier0_static/claim_register_proto_agreement_test.go:139-149` reporting two unregistered
  fields. REG-1 now retires that row and adds `ShutdownSlot` and `ShutdownPod` rows with their status,
  anchor, surface, deferral, and note, the "untouched" and "no other register row changes status" sentences
  are corrected, and §8's tier-0 block states that the gate re-runs over the post-split message set.
- **The base-mode release at the producer.** SCHEMA-1 turns one `Shutdown` call into an ordered pair in
  `binder.shutdownAdapter`, and §8's only case drove the adapter handlers. A tier-2 case over
  `Binder.Release` is added, and `pkg/gateway/podlifecycle/podsession/binder_test.go`, whose
  `recordingShutdownAdapter` and two `len(reqs) != 1` assertions are written against the deleted
  `ShutdownRequest`, is inventoried as a hand-rewrite.
- **The merged `ShutdownSlot` on a co-tenant pod.** The merge adds a pod-global operation to the per-slot
  teardown: `drainViaLifecycle` (`pkg/adapter/session.go:294-300`) signals the one runtime process serving
  every slot. SCHEMA-1 now scopes it, sending the drain only when the ending session's slot is the last
  bound one, and §8 adds a tier-1 case on a two-slot pod covering the flush, the co-tenant's survival, and
  the drain on the second teardown.
- **The MCP teardown on the slot release path.** §4.11 moves the platform and connector MCP cancellation
  onto a path that performs none today (`pkg/adapter/slotsession.go:99-112`), on the same change that starts
  an MCP server for every session. §8 adds a tier-1 case asserting the cancellation, the cleared handshake
  signal, and the co-tenant's untouched servers, and a tier-9 case that no endpoint authenticated with an
  ended session's nonce survives its teardown.
- **SPEC-4's env-var parenthetical.** The sentence cited `podspec.go:164`, a `const` declaration rather than
  a render site, and claimed the builder renders `LENNY_ADAPTER_SOCKET` "and nothing else", which
  `PodNameEnvVar` at `:175`, rendered by `podNameEnv()` at `:596`, `:691`, and `:913-914`, falsifies. The
  parenthetical now cites the render at `:614`, keeps the qualifier that `LENNY_ADAPTER_SOCKET` is the only
  `LENNY_`-prefixed variable the builder renders, and names `POD_NAME` as the other rendered variable.
  SPEC-4's conclusion is unchanged: neither variable names a credential path.

### Pass 5 (2026-08-17, automated)

- **The frame key the interpreted runtime SDKs emit.** §4.6.2(i) renames the wire key in the Python and
  TypeScript SDKs, which write it as a string literal
  (`sdks/runtime/python/lenny_runtime/runtime.py:368, 385` and `tool.py:147`;
  `sdks/runtime/typescript/src/runtime.ts:329, 344` and `src/tool.ts:133`), and §8 listed no case exercising
  either SDK's emission. A missed edit would ship a runtime whose frames resolve on a pod holding one slot
  and are rejected only on a pod holding more, which is the silent degradation §4.6.1 closes. §8 adds a
  tier-3 case in the existing per-SDK harness `tests/tier3_contract/sdks/runtime_sdk_test.go` asserting that
  a runtime built on each shipped SDK emits `sessionId`, emits no `slotId`, and echoes the identifier it was
  handed, CODE-6 names the interpreted emitters beside the gateway structs, and §10 lists the file.
- **§29.10's successor pointer.** SPEC-5 reduces §29.10 and moves its addressing mechanisms out, which
  `spec/28_communication-channels.md:62-63` (N8) makes a section that gives up content, obliging a permanent
  successor pointer naming the heading that now owns the content and the identifiers that moved. SPEC-5
  addressed only the anchor-redirect half and concluded no redirect entry was needed, which is a different
  obligation, so the applied specification would have violated N8. SPEC-5 now states the pointer's exact
  text into `[§28.5.3](28_communication-channels.md#2853-intra-pod)` naming `CH-MSGSOCK` and the moved
  mechanisms, adds the `{"spec/29_communication-scenarios.md", "29.10", "28.5"}` row to the hand-maintained
  `reducedSections` domain in `tests/tier11_docs/successor_pointer_test.go:52-55`, and §8's tier-11 block and
  §10 record the addition.
- **The merged `ShutdownSlot`'s drain gate under concurrency.** SCHEMA-1's original last-bound test reads the
  adapter's live registry and then terminates the one runtime process serving every slot, on a path
  co-tenant sessions reach concurrently: `shutdownSlot` releases `s.mu` (`pkg/adapter/slotsession.go:78`)
  before closing the runtime and deregistering the slot (`:82-86`), and `Binder.ReleaseSlot`
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:524-540`) issues one `ShutdownSlot` per release with no
  cross-session serialization, so two co-tenants ending at once can each observe the other and send no drain.
  The predicate also ignored the registered-but-unbound registry state
  (`pkg/adapter/slot.go:74-95`), so a session mid-start could lose the shared runtime. SCHEMA-1 now
  deregisters the ending slot and takes the drain decision in one critical section under `s.mu`, counts every
  registry entry rather than the bound ones alone, and states why that differs from §4.11's recycle-scrub
  guard. §8 adds two tier-7a cases under `-race`, one on two concurrent teardowns and one on a teardown
  racing an incoming session's workspace prep, and §10 lists the tier.
- **The §29.10 successor pointer named one owner for mechanisms six sections own.** The pointer text this
  pass first staged said the moved mechanisms "are now owned by §28.5.3", which owns only `CH-MSGSOCK`, its
  addressing key, and its buffering and replay policy. The workspace subtree is §6.4, the credential lease
  is §6.1 and §4.9, the slot lifecycle is §6.2, the per-slot inbox is §7.2, and checkpoint admission and the
  operation lock are §4.7, which is what the bullets being reduced already cite
  (`spec/29_communication-scenarios.md:1443-1482`) and what §4.4 and SPEC-5's opening state. N8 requires the
  pointer to name the heading that now owns the content, and the tier-11 gate accepts any pointer into
  §28.5 carrying a `CH-` identifier (`tests/tier11_docs/successor_pointer_test.go:111-134`), so the wrong
  owner list would have passed the gate and landed in the specification. SPEC-5 now attributes each moved
  mechanism to its own heading and keeps the §28.5.3 link and the `CH-MSGSOCK` identifier on one line.
- **The drain predicate was stated two ways.** The correction above re-keyed the drain gate onto registry
  occupancy, but SCHEMA-1's normative sentence and §8's tier-1 fourth case still stated it as the last-bound
  test. The two disagree on the registered-but-unbound entry `ensureSlotStateLocked` creates
  (`pkg/adapter/slot.go:74-95`), which is the case the correction was written for. SCHEMA-1's sentence and
  the tier-1 case now both state that the handler deregisters the ending slot under `s.mu` and drains when
  that leaves the registry with no entry.

### Pass 6 (2026-08-17, automated)

- **The example key rename was staged at one site and skipped at its parallels.** SPEC-6 renamed the key at
  `spec/15:1569` but staged only a value change at `spec/28:600`, `docs/reference/adapter-contract.md:316`,
  and the four `"slotId": null` examples at `docs/reference/adapter-contract.md:140, 180, 232, 271`. Under
  SCHEMA-2 the published schema declares `sessionId` on the five frames that carry the field
  (`schemas/lenny-adapter-jsonl.schema.json:58, 139, 161, 190, 222`), so those examples would have kept a
  key the schema no longer declares, and §8's own tier-11 reconciliation over the frames that carry an
  identifier would have failed on the proposal's own staged text. SPEC-6 now states the key rename at the
  two specification and documentation examples under §4.6.2, with the declined-rename fallback the rest of
  the rename carries, and §4.6.2(i) extends the `docs/reference/adapter-contract.md` entry from the field
  rows at `:156, :190, :281, :324` to the JSON examples at `:140, :180, :232, :271, :316`, so an example and
  the field row beneath it spell the same key.
- **The re-keyed fail-closed gate had no case on its new predicate.** CODE-1 turns
  `pkg/gateway/session/executor/pod.go:146`'s `bind.MaxConcurrentSessions > 1 && bind.SlotID == ""` into a
  non-empty check on the session identifier, but §8's only gate case asserted a refusal arm the change
  removes, and the existing component case inverts:
  `TestMessagesFailsClosedOnConcurrentBindWithNoSlot`
  (`pkg/gateway/sessionserver/messages_component_test.go:326`) builds a bind with a session identifier, an
  empty `SlotID`, and `MaxConcurrentSessions: 4` and requires a non-200, which the re-keyed gate admits.
  §8's tier-2 resume case now states both arms on the session identifier, and that file moves from the
  compile-only inventory (which keeps its unrelated `:67` entry) to the hand-rewrite list in §8 and §10.
- **Routing every rotation onto the per-slot handler dropped the §4.7 protocol, and tier 8 was absent.**
  Deleting `useSlot` makes `rotateCredentialsSlot` universal, and that branch performs the file rewrite
  alone (`pkg/adapter/slotcreds.go:57-60`) while the in-flight gate, the revocation ceiling with its counter
  and `credential.rotation_ceiling_hit` audit, the `credentials_rotated` send, and the ack timeout live only
  on the pod-global branch (`pkg/adapter/credentials.go:132`, `rotateProviderFull` at `:167-172`). No case
  at any tier asserted them, and the tier that does, tier 8, appeared nowhere in the proposal. CODE-4 now
  states that the merged handler keeps the §4.7 protocol keyed on the rotating session, §8 gains a tier-8
  block pinning the ceiling, the unbounded proactive wait, the ack-timeout fallthrough, and the Token
  Service outage guard, `tests/tier8_chaos/credential_rotation_ceiling_test.go` and
  `tests/tier8_chaos/token_service_unavailability_guard_test.go` are inventoried as hand-rewrites, and §10's
  tier list gains tier 8.

### Pass 7 (2026-08-17, automated)

- **SPEC-7 staged a `spec/15` sentence with no section, no anchor, and no text.** The line read
  "`spec/15` states that a client addresses a session and never a slot", which named no heading and supplied
  no wording, so an implementor would have had to invent both. `spec/15_external-api-surface.md` states no
  such sentence today; the statement the proposal relies on elsewhere is
  `spec/07_session-lifecycle.md:333` ("clients do not address slots directly, so the slot is resolved from
  the addressed session before delivery"), which §1.7 already cites and which SPEC-1 deconditions rather
  than deletes. SPEC-7 now records that the statement has an existing owner and that no new sentence is
  staged, which matches the standard SPEC-5 and SCHEMA-1 set for staged text elsewhere in §6.
- **`AssignCredentials`'s F-6.1.12 refusal was asserted to be unchanged while CODE-1 retires it.** Deleting
  `useSlot` routes every `AssignCredentials` through `assignCredentialsSlot`
  (`pkg/adapter/credentials.go:73-75`), which reads and writes no `credSessionID`
  (`pkg/adapter/slotcreds.go:23-51`), so the pod-global refusal at `pkg/adapter/credentials.go:84-86` becomes
  unreachable and the field is never set. §4.11 claimed the backstop was unchanged, §9's limit named only the
  two claim RPCs, and §8 inventoried neither `pkg/adapter/one_session_only_test.go:33-88` nor
  `pkg/adapter/credentials_test.go`, both of which pin the retired behavior and neither of which the
  compiler catches. §4.11 now retires the backstop explicitly on the same grounds as the two claim arms,
  because keeping it would re-impose the pod-wide one-session ceiling D1 moves to the gateway. §9's limit
  names the third RPC and the enforcement that remains. Pass 20 corrects the replacement this bullet
  originally named: the per-slot arm at `pkg/adapter/slotcreds.go:35-37` is unreachable under D5, so no
  adapter-side session-identity refusal survives.
- **The `CredentialsDir` precondition and the merged handler had no case.** §8 gains three tier-1 cases on
  `AssignCredentials`: the admitted second session writing only its own per-slot file, the refusal for a
  session other than the slot's bound one together with the idempotent rewrite for the same session, and the
  `FailedPrecondition` for an adapter configured with no credentials root, which survives the merge through
  `writeSlotCredentialFile` (`pkg/adapter/slotcreds.go:150-154`) and is raised after the slot tree exists
  rather than before. §8 and §10 inventory `pkg/adapter/credentials_test.go` and the two credential cases in
  `pkg/adapter/one_session_only_test.go` as hand-rewrites, including the three cases pinning the pod-global
  `checkCredentialSession`, which goes dead with the branches that call it. Pass 20 removed the wrong-session
  case this bullet named, because the arm at `pkg/adapter/slotcreds.go:35-37` is unreachable under D5. §8's
  `AssignCredentials` block now carries the admitted second session, the idempotent same-session rewrite, and
  the credentials-root `FailedPrecondition`.

### Pass 8 (2026-08-17, automated)

- **SPEC-7 renamed `spec/05:457` in place and left the renamed sentence attributing per-session teardown to
  a pod-scoped request.** The line is one sentence-run that both names the RPC triggering the whole-pod
  scrub and states that "the adapter closes only the ending session's runtime" on the recycle disposition
  (`spec/05_runtime-registry-and-pool-model.md:457`). SPEC-7 staged a name substitution alone, which would
  have left §5.2 asserting that `ShutdownPod` closes a session's runtime while SCHEMA-1 states twice that
  `ShutdownPod` carries no `session_id` and performs no per-session work. SPEC-7 already applies the split
  to the two sibling sentences carrying the identical claim (`spec/04:676` and `spec/29:690-696`). The
  `spec/05:457` clause now stages the same split: the gateway sends `ShutdownSlot` for the ending session,
  on which the adapter closes that session's runtime, and then `ShutdownPod` carrying the recycle
  disposition, the pod identity, and the scrub parameters, on which the adapter keeps the pod process alive,
  runs the scrub asynchronously, and reports via `ReportPodScrub`. That is the caller ordering SCHEMA-1
  states for `binder.shutdownAdapter`, and the existing carve-out covering all six renamed sentences under a
  declined split still applies.
- **The §4.5(b′) producer test was absent and the file listed in its place needs no edit.** §4.5(b′) and §8
  both named `pkg/adapter/gatewaycontrol/gatewaycontrol_test.go:46, 90`, which is the `stubGatewayControl`
  field and its `ReportSessionScrub` method; both take the whole request message and name no slot, so the
  field removal and the `RecordSessionScrub` arity change leave them compiling unchanged. The file carrying
  the subject, `pkg/adapter/gatewaycontrol/scrubreport_test.go`, appeared nowhere in the draft, and neither
  of its cases is a mechanical field drop: the released case calls the producer with the deleted `slotID`
  argument and pairs session `"sess-1"` with slot `"slot-3"` (`:23, :34-36`), and
  `TestReportSessionScrubLeakedOmitsSlotWhenEmpty_spec_5_2` (`:42-58`) pins the base-mode empty-slot
  emission D1 retires. §4.5(b′) now names `scrubreport_test.go` and records that `gatewaycontrol_test.go` is
  not an edit site, §8 moves it out of the compile-only bucket into a hand-rewrite paragraph stating the
  rewrite of the released case and the deletion of the leaked case, and §10's hand-rewrite inventory gains
  the file.

### Pass 9 (2026-08-17, automated)

- **`Client.Terminate`, which sends the §11.4 revoke RPC SPEC-7 renames, sat in no deliverable.** CODE-1
  stated the client-side fate of the shutdown surface as two methods, `Shutdown` (`:885`) and `ShutdownSlot`
  (`:892`), over the shared `shutdown` helper (`:896-902`). `pkg/gateway/runtime/adapterclient/client.go`
  declares four producers of `ShutdownRequest`: those two through the helper, `Terminate` (`:923-932`), and
  `ShutdownRecycle` (`:969-981`). `Terminate` is the only client method that populates `Reason` and
  `DeadlineMs`, the two fields SCHEMA-1 keeps on the new `ShutdownSlot` message, and it is the Go
  implementation of the §11.4 full-revoke send SPEC-7 re-points onto `ShutdownSlot` at `spec/11:263` and
  `:270`. Neither it nor its sole caller, `cmd/lenny-gateway/user_revocation.go:129`, appeared in any
  deliverable, and `cmd/lenny-gateway/` appeared nowhere in §10, so the specification would have landed
  saying the gateway sends `ShutdownSlot` for a full revoke while the deliverable editing the sending client
  described a different method. `Terminate`'s doc comment (`:910-922`) also states the retired
  `Terminate`-to-`Shutdown` mapping, which SPEC-7 makes false. CODE-1's `client.go` paragraph now gives all
  four producers a fate: `shutdown` is deleted with `ShutdownRequest`; `Shutdown`, `ShutdownSlot`, and
  `Terminate` collapse into one `ShutdownSlot(ctx, sessionID, reason, deadline)` method over the
  `ShutdownSlot` RPC, which is what the paragraph's own one-method-per-RPC instruction requires and avoids a
  duplicate implementation of one concern; the doc comment is rewritten to name `ShutdownSlot` on both
  sides; and `ShutdownRecycle` re-points onto `ShutdownPod` and drops the `sessionID` parameter that message
  carries no field for. §10 gains `cmd/lenny-gateway/user_revocation.go`, whose call passes the
  `USER_REVOKED` reason and `userTerminateDeadline` through the collapsed method.

### Pass 10 (2026-08-17, automated)

- **The `cleanupCommands` working directory.** CODE-1 pluralized `scrub.Config.WorkspaceDir` and enumerated
  three consumers, while `cfg.WorkspaceDir` also supplies the working directory the deployer's
  `cleanupCommands` execute in (`pkg/adapter/scrub/scrub.go:299`) and the `PWD` entry in their environment
  (`:307`, reaching `pkg/adapter/workspace/setup.go:101-102`). Neither can take a set, and CODE-2 retires
  `Server.WorkspaceRoot`, the only value the sole producer `scrubConfig` (`pkg/adapter/podscrub.go:104-110`)
  has to give them, so an implementor would have had to invent one. CODE-1 now adds a singular `CleanupDir`
  alongside the two plural members, sets it to the workspace base, states why the base is the chosen value,
  and corrects the step citations to `:246-247`, `:363`, and `:372-379`. SPEC-4 gains the matching §5.2
  restatement of step 2 (`spec/05:465`) and of step 6's workspace clause (`:469`), which D9 would otherwise
  leave naming a directory no pod carries: `:465` names neither sweep literal, and `:469` sits inside the
  `/run/lenny/credentials.json` sweep for its credential clause alone. The edit also carries the
  statement that the cleanup phase runs with the workspace base as its working directory. §8's tier-1 scrub
  cases gain a fourth case asserting the working directory and `PWD` the commands observe on a pod that held
  two slots.
- **The sweep reach of `spec/05:469`.** The bullet above and SPEC-4's workspace paragraph both claimed that
  neither §8 tier-11 sweep predicate reaches `spec/05:465` or `:469`. `:469` contains the literal
  `/run/lenny/credentials.json` twice, so the second sweep §8 defines for that literal does reach it, and
  SPEC-4's preceding paragraph already stages the same line for that literal. Only `:465` names neither
  predicate. Both statements now say that: `:465` is staged explicitly because no sweep reaches it, and
  `:469` is staged for its workspace clause, which the credential sweep does not cover.

### Pass 11 (2026-08-17, automated)

- **The §29.10 gap bullet that asserts the pre-re-key admission rule.** §4.4 keeps the `Interrupt` and
  drain-barrier addressing gap (`spec/29:1549-1555`) in the co-tenancy half, and that bullet's opening
  sentence at `spec/29:1550` states that "the specification qualifies checkpoint admission by `slotId`",
  citing §4.7 and §28.6. SPEC-6 re-keys admission on the session identifier at `spec/04:691`,
  `spec/28:291-294`, `spec/28:1651-1658`, and `spec/29:805-806`, and §4.5(c) removes
  `CheckpointStart.slot_id`, so after
  application neither cited section carries a `slotId` qualification and the retained bullet would describe
  a rule the specification no longer states. `spec/29:1550` appeared in no edit list. SPEC-6 now stages it
  with the same re-key, restating the sentence as "The specification qualifies checkpoint admission by the
  session identifier and states that the lock serializes `Interrupt` across the pod's slots", and §4.4 names
  the restatement where it lists the bullet. The gap itself is unchanged, because the specification still
  states no slot qualification for the `Interrupt` RPC the lock admits or for the drain barrier
  `CH-BARRIER` carries.
- **The §4.7 source sentence the restatement rests on.** The bullet above justified the `spec/29:1550`
  restatement with the claim that after the re-key neither §4.7 nor §28.6 qualifies checkpoint admission by
  `slotId`, but no deliverable staged the §4.7 sentence that carries the qualification. `spec/04:691` is the
  §4.7.2 **Queue depth** bullet, and it states that "the lock admits one pending checkpoint per distinct
  `slotId` and promotes the pending checkpoints in slot-ID order ...; a checkpoint whose `slotId` is already
  pending coalesces". §4.10 staged that line for its identifier-order clause alone, and both §4.10's
  enumeration and SPEC-6's re-key list named only the three restatements, so §4.7 would have kept the
  `slotId` qualification and the restated `spec/29:1550` would have cited it for a rule §4.7 does not state.
  §4.10 and SPEC-6 now carry `spec/04:691` in the re-key alongside `spec/28:291-294`, `spec/28:1651-1658`,
  and `spec/29:805-806`: its admission and coalescing clauses key on the session identifier, its
  single-session queue-depth sentence is dropped, and its `maxConcurrentSessions > 1` condition goes with
  that sentence. Its identifier-order clause stays where §4.10 staged it.

### Pass 12 (2026-08-17, automated)

- **CODE-5's claim that the compiler catches nothing.** The deliverable stated that "nothing here is caught
  by the compiler", which held for the SQL string literals and was wrong for the Go surface: deleting
  `Record.SlotID` from both stores, dropping the `slotID` parameter from `Store.Rotate`, `Store.List`, and
  `Store.HardDelete`, and renaming `LatestActiveForSlot` breaks every caller, and the callers include test
  files the proposal named nowhere. The sentence now separates the two halves and states that
  `pkg/gateway/checkpoint/checkpointretention`, `pkg/gateway/checkpoint/partialmanifeststore`,
  `pkg/gateway/checkpoint/checkpointer`, `pkg/gateway/sessionserver`, and `tests/tier4_integration` do not
  build until each affected test file takes its disposition.
- **The two store packages' own tests, whose subjects CODE-5 deletes.** §8 gains a tier-1 block staging both
  as hand-rewrites. `checkpointretention_test.go`'s `TestRotate_PerSlotIndependent` (`:254-258`) carries a
  `// spec: §12.5` annotation asserting that the "latest 2" cap applies independently per slot and requires
  `slot-b`'s three rows intact after rotating `slot-a` (`:263, :269, :277`), which is the rule SPEC-9 re-keys
  onto `session_id`; it is re-based on the session-keyed cap so the tier-1 case and §8's new tier-2 retention
  case state the same rule. `partialmanifeststore_test.go`'s `TestLatestActiveForSlotIsSlotScoped` (`:305,
  :327`) asserts a supersede selector scoped to a slot distinct from its session, which D5 makes
  unconstructible and CODE-5 deletes the method for, so the case is deleted and the `SlotDefault` assertion
  at `:52` goes with the sentinel.
- **The five further callers of the deleted store surface.** §8 and §10 gain
  `pkg/gateway/checkpoint/checkpointer/checkpointer_test.go` (the `fakeRetention.Rotate` arity at `:627`,
  the `Insert` fake at `:622`, and the `SlotID: "slot-3"` bind at `:727` with its assertion at `:735`),
  `pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go` (`:332, 355, 921, 980`),
  `pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go:47`,
  `tests/tier4_integration/checkpoint_chunk_helpers_test.go:120, 163`, and
  `tests/tier4_integration/checkpoint_intent_generation_test.go:63, 129`. `checkpointer_test.go:727` joins
  the fixture-rule corpus, since it pairs session `"cw"` with a distinct slot identifier.
- **The tier-4 case whose whole subject is per-slot checkpoint addressing.**
  `tests/tier4_integration/checkpoint_concurrent_pool_test.go` appeared in no deliverable and no §8 bucket,
  yet it breaks on three staged edits at once: it is the sole caller of `receivedSlotID()`
  (`checkpoint_driver_harness_test.go:87-88`, whose `:100` the proposal already stages), it reads
  `partialmanifeststore.Record.SlotID` (`:76, :79`), and it pairs `sess-a`/`slot-a` with `sess-b`/`slot-b`
  (`:51-52`). Its file comment and `// spec:` annotation state the retired model directly (`:8-10, :35-36`).
  §8 now stages it as a tier-4 hand-rewrite addressing the two streams by session identifier and keying each
  manifest on `session_id` alone, the §4.5(c) deletion bucket names it beside the harness helper it calls,
  and §10 lists it.
- **The fixture-rule corpus count.** The rule stated a corpus of eight files. With
  `checkpointer_test.go:727` and `checkpoint_concurrent_pool_test.go:51-52` added, the corpus is ten, and
  both are named in the staged-above half of the list.
- **The tier-1 pin of the warm-pod invariant D9 retires.** `pkg/adapter/warmlayout_test.go` appeared nowhere
  in the proposal. Five of its constructions set `Server.WorkspaceRoot` (`:22, :53, :71, :95, :195`), the
  field CODE-2 deletes, and three cases assert the `current` leaf's existence, emptiness, and mode, one of
  them citing `// spec: §6.1 — /workspace/current "exists but is empty"` (`:37`), the sentence SPEC-3 deletes
  from `spec/06:11`. §8 gains a tier-1 block re-basing the three cases on the post-D8 warm layout, with a
  negative assertion that no `current` leaf is created. §5's D8 cost paragraph, which asked for any site
  relying on the retired assertion to be found, now names this one. §10 lists the file.
- **The other `pkg/adapter` tests that set the deleted field.** The same §8 block gives each an
  explicit disposition: `exportpaths_test.go` (`:53, 97, 116, 144, 158, 180, 190`) is a subject rewrite onto
  the slot root CODE-2 re-points `ExportPaths` at, `manifest_test.go` and `manifest_fields_test.go`
  hard-code the `"/workspace/current"` literal and take a value change with the field change,
  `files_updated_test.go`, `staging_test.go`, and `tracing_internal_test.go` are field swaps, and
  `resume_test.go`'s §7.3 step (d) cases are restated against a slot root.
- **The second per-slot case in `partialmanifeststore_test.go`.** The block's disposition list reached only
  `TestLatestActiveForSlotIsSlotScoped`, the `SlotDefault` assertion at `:52`, and the surviving
  `LatestActiveForSlot` calls, so `TestPutSupersedeScopedToSlot` (`:280-299`) kept a `// spec: §10.1.7`
  annotation asserting a supersede predicate scoped to `(session_id, slot_id)`, a fixture pairing session
  `s1` with `slot-0` and `slot-1`, and no disposition at all, while CODE-5 deletes the `row.SlotID`
  comparisons at `partialmanifeststore.go:405, :416` that the predicate rests on. §8 now states its
  deletion and moves its §10.1.7 tie onto the session-keyed supersede SPEC-9 states. The block's opening
  predicate is corrected from "a case" to "at least one case", and §10's parallel sentence with it.
- **`uploaddriver_test.go`'s classification.** The file sat in the compile-only bucket under a predicate
  that its edits "assert nothing about slots", but `:1041` and `:1051` are the two seeded rows of
  `TestDriverSupersedeReleasesTargetSlotPriorRow_spec_10_1` (`:1025`), whose `// spec:` block and
  `// diagnosis:` comment (`:1017-1024`) state that resolving the prior row through the session-wide
  selector is the defect. CODE-5 makes exactly that resolution the specified behavior at
  `uploaddriver.go:406-407`, so a field drop would leave the case asserting the inverse of the applied
  deliverable. §8 moves the file into the hand-rewrite bucket with the case deleted and its §10.1.7 tie
  restated, leaves `:332, 355, 921, 980` as compile-only field drops, and §10 follows. Both deleted cases
  take their distinct-identifier fixtures with them, so the fixture-rule corpus keeps its ten files.
- **`pkg/adapter`'s blast radius scoped to composite literals.** The block's predicate named only test files
  that construct `&Server{WorkspaceRoot: ...}`, which left every file that assigns the field onto a
  constructed `Server` undisposed, and CODE-2 retires the field so both forms break the build. The
  predicate now reads "sets `Server.WorkspaceRoot`, whether in a composite literal or by assignment".
  `session_test.go`'s `sessionServer` helper (`:215-222`) gets its own disposition, since it sets the field
  and returns the root that twelve files in the package compare against, and `session_test.go:411`,
  `mcpruntime_test.go`, `embedded_sdkwarm_test.go`, `shutdown_demote_test.go`, `tracing_external_test.go`,
  `sdkwarm_test.go`, `checkpoint_stream_test.go`, and `podscrub_test.go` (including the
  `"/workspace/current"` literal at `:468`) are added to §8 and §10. The `resume_test.go` entry drops its
  "constructs no such `Server`" clause, states that its root comes from the rewritten helper, cites the
  `ExpectedWorkspaceRoot` sites at `:322, :335, :347` rather than `:318, :331, :343`, and adds the
  claim-release case at `:356-367` that the three-case count omitted.
- **§8's tier-2 retention predicate.** The paragraph still said CODE-5's edits to
  `pkg/gateway/checkpoint/checkpointretention` "are the ones the compiler does not catch", contradicting
  the corrected CODE-5 sentence and the tier-1 block that stages `checkpointretention_test.go` as a
  hand-rewrite for the compiler-caught half. It now narrows the predicate to the package's SQL.

### Pass 13 (2026-08-17, automated)

- **The `Server.WorkspaceRoot` blast radius stopped at `pkg/adapter`.** §8 scoped the field's disposition to
  "every other `pkg/adapter` test" and closed with "`pkg/adapter` does not build", but the field is exported
  and is set on a constructed `adapter.Server` from `pkg/gateway/runtime/adapterclient`,
  `pkg/gateway/session/executor`, `pkg/gateway/coordination/barrier`,
  `pkg/gateway/podlifecycle/podsession`, `pkg/gateway/sessionserver`, the shared fixture
  `tests/testinfra/coordfixture/coordfixture.go:79`, tiers 2, 3, 4, 7a, 9, and 10, and
  `cmd/lenny-gateway/direct_usage_quota_integration_test.go:146`, none of which appeared anywhere in the
  draft. §8 gains a paragraph naming every holder with its lines, ordered with the shared fixture first
  because other tiers dial through its `StartPod`, and marking the four that construct the literal
  `/workspace/current` leaf as value changes as well as field changes. §10 gains the matching entry. This is
  the defect pass 12 corrected inside `pkg/adapter`, one directory up.
- **Two undisposed decoders of the renamed frame key.** §4.6.2(i) enumerates the sites that carry the wire
  key as a `json` tag precisely because no compiler catches them, and it missed the anonymous decode structs
  at `pkg/gateway/session/executor/pod_test.go:208-213` and
  `pkg/gateway/sessionserver/messages_component_test.go:75-78`, which read the envelope key CODE-6 renames
  on the producer side. Both are added to that list, and §8 states the disposition: the tags move to
  `sessionId` and the assertions reading them (`pod_test.go:273-274, :334-335, :361-362, :388-389` and
  `messages_component_test.go:226-227, :269-270, :310-312`) are restated on the session identifier, with the
  three emptiness assertions inverting because under D1 the envelope carries a non-empty session identifier
  on a pool of either concurrency. The compile-only inventory entries for the two files now point at that
  paragraph rather than implying the edits are mechanical.
- **Six undisposed constructors of the deleted `ShutdownRequest`.** `pkg/adapter/drain_test.go`,
  `pkg/gateway/sessionserver/recycle_scrub_fold_component_test.go`, `pkg/adapter/platformmcp_test.go`,
  `pkg/adapter/connectormcp_test.go`, `tests/tier4_integration/mcp_runtime_lifecycle_test.go`, and
  `tests/tier9_security/adapter_mcp_nonce_test.go` construct the message SCHEMA-1 deletes and were named
  nowhere in the draft. Two are behavioral pins rather than field drops, so §8 stages them as hand-rewrites:
  `drain_test.go:16-42` pins the `CH-RUNTIMEOPS` drain SCHEMA-1 re-gates on registry occupancy and also
  drives the retired `claimSession` (`:24`), and `recycle_scrub_fold_component_test.go` is the second
  producer test on the base-mode release path, its fake serving the deleted `Shutdown` RPC (`:33-45`) and
  its fold assertions (`:184-200`) reading the `RecycleScrub` that now rides on `ShutdownPod`. It also
  carries the CODE-2 field swap at `:94`. The other four are `ShutdownSlot` call-site swaps. §10 gains the
  matching entries.
- **The largest `WorkspaceRoot` holder was missing from the enumeration this pass added, and one file's line
  list was short.** `pkg/gateway/podlifecycle/podsession/binder_test.go` sets the field on a constructed
  `adapter.Server` at 39 sites (from `:329`, on servers built by `adapter.New("adapter-test")` at `:328` and
  dialed through `adapterDialer` at `:268`) and appeared in neither the §8 paragraph nor the §10 bullet, and
  `sdkwarm_bind_test.go` was listed with two of its four sites. Both lists are corrected, and the paragraph's
  closing sentence now names three hand-rewrites rather than two, `binder_test.go` being the third.
- **A stale conditional on the same file.** The SCHEMA-1 producer block closed "If review declines the split,
  the file is untouched," which CODE-2 makes false: the field retirement is unconditional, so
  `binder_test.go` takes its 39 swaps either way. The sentence now matches the form used one paragraph below
  for `recycle_scrub_fold_component_test.go`, that declining the split leaves only the field swap.

### Pass 14 (2026-08-17, automated)

- **The four §28.5.3 per-frame schema blocks kept the key SCHEMA-2 renames.** SPEC-1 staged
  `spec/28_communication-channels.md:632`, `:665`, `:689`, and `:795` as presence conditions alone, while
  each of those lines declares the property literally as `"slotId"` inside the `tool_result`, `response`,
  `tool_call`, and `set_tracing_context` schema blocks, and SCHEMA-2 renames the same four properties in
  `schemas/lenny-adapter-jsonl.schema.json:139, 161, 190, 222`. The applied specification would have
  declared a property the published schema no longer carries, and §8's own tier-11 frame reconciliation
  would have failed on it. SPEC-1 now states the rename at those four lines and at the prose naming the key
  at `:604`, alongside the `message` example at `:600` SPEC-6 stages, with the same declined-rename
  fallback the rest of §4.6.2 carries, and §4.6.2(i) gains the matching site bullet.
- **SPEC-9 turned the tier-11 checkpoint-manifest column gate permanently red with no disposition.** The
  lines SPEC-9 rewrites (`spec/10_gateway-internals.md:153, 157, 163, 167, 171, 189, 192`) are every
  `slot_id` occurrence in §10.1, and `tests/tier11_docs/checkpoint_pipeline_consistency_test.go` requires
  each non-infrastructure column of `migrations/0178_checkpoint_manifest.up.sql` to be named in a code span
  inside that section. MIG-1's drop migration leaves 0178's `.up.sql` text unchanged, so the column stays in
  the extracted set and the gate errors from the spec commit onward. §8's tier-11 block now stages the file
  as a hand-rewrite that adds a dropped-column set beside `infraColumns` (`:52-55`) naming the drop
  migration, which is the form MIG-1 already uses for `prod_columns_test.go`'s 0112 entry, and records why
  re-pointing the gate's column source is rejected. SPEC-9 names the consequence and §10 gains the entry.

### Pass 15 (2026-08-17, automated)

- **The SDK-warm `ConfigureWorkspace` cwd was neither re-pointed at the adapter nor derived at the
  gateway.** CODE-2 said `ConfigureWorkspace` resolves against the slot root the way `ExportPaths` does, but
  the handler resolves no root: it reads `req.GetCwd()` (`pkg/adapter/sdkwarm.go:204`) and passes it to the
  pre-connected SDK (`:249`). The value is supplied by
  `pkg/gateway/podlifecycle/podsession/binder.go:989`, which passes `neg.WorkspaceRoot`, the field CODE-2
  turns into the workspace base, so applying the proposal as written would have pointed every `preConnect`
  session at `/workspace` instead of its slot root, which is the defect §1.2 exists to close. That call site
  appeared in no deliverable and in no §10 entry. §1.2 now locates the `ConfigureWorkspace` half of the
  delegation defect at the gateway, §4.5(a) states that the root arrives on the wire and that the adapter's
  remaining obligation is the §4.2 `session_id` validation, CODE-2 names `binder.go:989` as another consumer
  of the negotiated root taking the per-session derivation, and §8 gains a tier-2 case asserting the `cwd` a
  `preConnect` bind sends.
- **A third `persistWorkspaceRoot` call site was unstaged, so the finalize path would have written the
  workspace base into `sessions.workspace_root`.** CODE-2 named one persist site and derived the slot root
  there alone. `pkg/gateway/sessionserver/finalize.go:363` calls the same helper with `prep.WorkspaceRoot`,
  on the path its own doc comment (`:345-346`) says now owns the prepare phase, and `persistWorkspaceRoot`
  is first-non-empty-wins (`start.go:3275-3277`). An unedited finalize site would have fixed the column at
  the workspace base and the §7.3 step (d) guard would then have rejected the resume with
  `FailedPrecondition`, the failure CODE-2 exists to prevent. CODE-2 now names both persist sites and states
  why both take the derivation, `start.go:2608` is described as the copy onto the launch result rather than
  as a persist, §10's `pkg/gateway/sessionserver/` entry lists `finalize.go`, and §8's tier-2 round trip is
  scoped to a session reaching ready through the finalize path and asserts the exact
  `<base>/slots/{sessionId}/current` value rather than non-emptiness.
- **Correction to the first entry above: §10 still credited the derivation to `start.go` alone.** The
  entry claims the `binder.go:989` call site was missing from §10, but the §10 bullet for
  `pkg/adapter/server.go`'s `NegotiateVersion` handler named `binder.go` only as propagation and named
  `pkg/gateway/sessionserver/start.go` as the sole derivation site, so an implementor reading §10 for the
  blast radius of the derivation change saw one of the four sites CODE-2 stages. That bullet now lists all
  four: the launch-path persist and the `ExpectedWorkspaceRoot` replay in `start.go`, the finalize-path
  persist in `finalize.go`, and the `ConfigureWorkspace` `cwd` at `binder.go:989`.
- **Correction to both entries above: CODE-2's forward reference carried a count the second entry made
  stale.** CODE-2 called `binder.go:989` "a third consumer of the negotiated root" taking "the same
  per-session derivation as the two below", written when the paragraph below named one persist site and the
  replay. Splitting the persist into two sites made that paragraph enumerate four derivation sites, so the
  ordinal and the count no longer resolved. The sentence now reads "another consumer of the negotiated root"
  taking "the same per-session derivation as the persist and replay sites below", and the first entry above
  is reworded to match.

### Pass 16 (2026-08-17, automated)

- **SCHEMA-1 gave the two new messages the names of their own RPCs, which protoc refuses to compile.** The
  deliverable staged `rpc ShutdownSlot(ShutdownSlot)` and `rpc ShutdownPod(ShutdownPod)`, and it staged them
  "in full so that no part of it is invented at application time". A protobuf method name is a symbol in its
  service's own scope, so the relative reference `ShutdownSlot` inside `service Adapter` resolves to the
  method rather than to the message. Reproducing the staged declarations under the repository's own module
  config gives `expected message type, found service method` from both `buf lint` and `buf build`, so the
  proto would not parse, `buf generate` could not regenerate `pkg/proto/adapter/v1/`, and every deliverable
  compiling against the generated code (CODE-1, CODE-4, CODE-5, CODE-7, and all of §8) would be unreachable.
  The messages are now `ShutdownSlotRequest` and `ShutdownPodRequest`, matching the `<Rpc>Request` convention
  the file already uses at `schemas/lenny-adapter.proto:195` over `:1548`. The spelling is carried through
  §4.1's two table rows and the sentence below them, REG-1's two replacement fence rows, §8's tier-3
  descriptor case and its register-to-proto gate paragraph, and the `lastShutdown` return type in §8's
  producer-test rewrite. The sites that name the split or the RPCs keep the bare `ShutdownSlot` and
  `ShutdownPod` spellings, which stay correct as RPC names.
- **Sharing one `ShutdownResponse` across the two new RPCs turns tier 0's `buf lint` red with no recorded
  disposition.** SCHEMA-1 stated the two RPCs return the existing `ShutdownResponse` "because neither carries
  a distinct result". The module lint config (`buf.yaml:11-19`) uses the `STANDARD` rule set with four
  exceptions, none covering RPC request or response naming, so one response over two RPCs fails
  `RPC_REQUEST_RESPONSE_UNIQUE` and `RPC_RESPONSE_STANDARD_NAME` once per RPC. Reproducing it under that
  config yields those four findings, and `buf lint` (`cmd/lenny-test/cmd_run.go:510-515`) exits 0 on the tree
  today, so the staged text would turn a green gate red with no deliverable, §8 entry, or recorded limit
  disposing of it. SCHEMA-1 now declares `ShutdownSlotResponse` and `ShutdownPodResponse`, each carrying the
  `exited_cleanly` and `exit_code` fields `ShutdownResponse` (`:1601-1604`) carries today, and retires
  `ShutdownResponse` with `ShutdownRequest`. Adding a lint exception was rejected because the exception list
  is module-wide and would suppress the rule across every service in `schemas/`. SCHEMA-1 also records both
  toolchain constraints so the names are not re-derived at application time, and §8's tier-3 descriptor case
  now pins four messages and two RPCs and asserts that none of `Shutdown`, `ShutdownRequest`, or
  `ShutdownResponse` survives.
- **Three §8 sites still spelled a message by the retired name.** The rename's carry-through list above
  missed three sites that denote a message rather than an RPC, each of which named `ShutdownPod`, which
  SCHEMA-1 no longer declares. §8's tier-1 recycle-scrub guard case named "the message" and paired it in the
  same sentence with the declined-split alternative `ShutdownRequest`; §8's tier-3 shutdown-split case
  assigned a golden wire encoding and a `RecycleScrub` round trip to it, both properties of a request
  message; and §8's whole-pod-scrub expectation restated a test "against `ShutdownPod` per §4.1", where §4.1
  carries the row `ShutdownPodRequest`. All three now spell `ShutdownPodRequest`, so §8's tier-0
  register-to-proto and classification gates reconcile against the message set SCHEMA-1 declares. The bare
  `ShutdownSlot` and `ShutdownPod` spellings stand everywhere they name the RPCs or the split.

### Pass 17 (2026-08-17, automated)

- **A tier-2 test constructs the `Record.SlotID` member CODE-5 deletes and appeared in no inventory.**
  CODE-5's blast-radius sentence named `pkg/gateway/checkpoint/checkpointretention`,
  `pkg/gateway/checkpoint/partialmanifeststore`, `pkg/gateway/checkpoint/checkpointer`,
  `pkg/gateway/sessionserver`, and `tests/tier4_integration` as the packages that stop building until §8's
  dispositions are taken, and it omitted `tests/tier2_component`.
  `tests/tier2_component/legalholdreconciler/reconciler_test.go` carries `//go:build component` and its
  `seedManifest` helper builds a `partialmanifeststore.Record` literal setting the deleted member
  (`:68, 75`) from a `slot` parameter its three callers supply (`:145, 167, 203`), so after CODE-5 that
  package does not compile and tier 2 cannot run in it. The file appeared nowhere in the proposal. The
  sentence now names `tests/tier2_component` and tier 2, §8's compile-only list from CODE-5's store-API
  change carries the file with the field, the parameter, and the three arguments as one drop, and §10 lists
  it beside the other field and argument drops. The helper's slot values are inert fixture text that no
  assertion reads, so the edit carries no subject.
- **The tier-3 pin of the renamed JSONL key was filed under a fixture rule whose predicate does not reach
  it, and one of its two cases inverts.** §8's fixture rule covers a fixture that pairs a session identifier
  with a different slot identifier, and it listed
  `tests/tier3_contract/adapter_jsonl/set_tracing_context_test.go` as one of its ten files. That file's
  frames carry `slotId` and no session field at all (`:55-56`, `:78-80`), so the rule's predicate does not
  reach it and collapsing two identifiers into one is not the edit it needs. What it needs is the key
  rename plus a re-based subject: `TestSetTracingContextRejectsNonStringSlotID` (`:73`) requires the
  published schema to reject three non-string `slotId` values, and after SCHEMA-2 the schema declares no
  `slotId` property. The `set_tracing_context` definition sets no `additionalProperties: false`
  (`schemas/lenny-adapter-jsonl.schema.json:211-224`, whose only `additionalProperties` is the `true` at
  `:220` inside `context`), so all three malformed frames would validate and the case would fail its own
  `want rejection` assertion while its sibling passed vacuously. The file is removed from the fixture
  corpus, and §8 gains a tier-3 hand-rewrite block stating
  the key rename in both cases, the two function renames, and the rejection case's re-basing onto the
  `sessionId` property SCHEMA-2 declares. §10 lists it.
- **The tier-1 pin of the §28.5.3 addressing rule was classified as a one-line compile-only edit while two
  of its cases invert.** §8 listed `pkg/adapter/tracingcontext_addressing_test.go:62` among edits that drop
  a field from a request literal, and that line was the file's only appearance in any deliverable. The file
  is the tier-1 pin of the rule §4.6.1 and §4.8 rewrite.
  `TestSetTracingContextTaggedOnSingleSessionPodIsDropped_spec_28_5_3` (`:334`) rests on the premise that
  "no session on a single-session pod holds a slot id" (`:326-329`), which D1 retires, so its frame must now
  be accepted. The `on a slot-bound stream is dropped` subtest of
  `TestSetTracingContextUnreadableSlotIDIsTheEmptyAddress_spec_28_5_3` (`:385-398`) runs against a pod
  holding exactly one slot and requires the empty address to be dropped, which is the opposite of §4.6.1's
  resolve-on-one-slot branch. Every fixture in the file pairs a session with a distinct slot (`:234-236`,
  `:259-261`, `:288-289`, `:386-387`), the state D5 makes unconstructible, and the file was not in the
  fixture corpus. None of this breaks the build, so nothing would surface it at application time. The file
  moves out of the compile-only list into a named tier-1 hand-rewrite stating the fixture collapse, the
  inversion of the single-session tagged case, the re-basing of the unreadable-identifier subtest onto both
  branches of §4.6.1, and which rejections now count on
  `lenny_adapter_unaddressed_frame_rejected_total` rather than on
  `lenny_adapter_set_tracing_context_dropped_total`. §10 lists it.
- **The fixture-rule count did not absorb the file this pass added to the corpus.** The tier-1 block written
  above states that every fixture in `pkg/adapter/tracingcontext_addressing_test.go` pairs a session with a
  distinct slot and that each pair collapses onto one identifier, which is exactly the rule's predicate,
  while §8's fixture rule still read "the corpus is nine files" with six staged above. Six of the nine are
  themselves staged above, so being staged above does not exempt a file from the count. The rule now reads
  ten files with seven staged above, and the tracing file is named in the staged-above list with a pointer
  to the tier-1 block that carries its collapse. The tier-3 bullet above no longer restates the count, so
  the corpus is inventoried in one place.
- **The tier-1 block miscounted its own edits and called the compiler-caught one harmless.** It said "three
  further edits" and then closed on "none of these four edits breaks the build". The `:62` assignment
  `req.SlotId = &adapterv1.SlotId{Value: slotID}` stops compiling once SCHEMA-2 and CODE-1 remove
  `AttachRequest.slot_id` (`schemas/lenny-adapter.proto:967`), which is why the line sat in the compile-only
  list before this pass. The block now states that the `:62` drop is compiler-caught and is why the file
  cannot be left as it stands, and that the three semantic edits are the ones nothing surfaces at
  application time. The two counts agree.
- **`tests/tier2_component` was attributed to one file when two of its files stop building.** The
  compile-only block said `tests/tier2_component/legalholdreconciler/reconciler_test.go` "is the reason
  `tests/tier2_component` joins the packages CODE-5 breaks", while
  `tests/tier2_component/stores/partialmanifeststore_test.go`, staged two paragraphs earlier and in the
  fixture corpus, reads the deleted `Record.SlotID` member (`:93-94`, `:163`) and calls the renamed
  `LatestActiveForSlot` (`:179`). The sentence now names both files as the reason the package does not build
  until §8's dispositions are taken.

### Pass 18 (2026-08-17, automated)

- **§10 attributed a retired-path workspace flag to `cmd/runtimes/echo`, which declares no flags.** The
  bullet listed the file as an edit site on the ground that its workspace flag defaults to
  `/workspace/current`. `cmd/runtimes/echo/` holds one file, `main.go`, which parses no flags and names no
  workspace path, and it appears nowhere else in the proposal. The two sibling attributions are correct
  (`cmd/runtimes/echo-embedded/main.go:77` and `cmd/runtimes/preconnect-echo/main.go:56`). `cmd/runtimes/echo`
  is dropped from the bullet and recorded as not an edit site.
- **CODE-2 listed `start.go:3917` as a fourth derivation site, which would re-vacuate the §7.3 step (d)
  guard.** That line is `ExpectedWorkspaceRoot: row.WorkspaceRoot` inside the `s.podBinder.Resume` argument
  list opened at `:3898`, a replay of the persisted column rather than a computation, and the replacement
  pod's base is produced inside `Binder.Resume` (`binder.go:1581`) strictly after the request literal is
  built, so no base is in scope there. Deriving the expectation from the replacement pod's own base and the
  session identifier would compare a value against itself, reinstating the vacuity §1.2 records, and would
  leave §8's tier-2 negative case and MIG-1's column rewrite with nothing to act on. CODE-2's derivation
  list is now three sites, the replay is stated as staying verbatim with the reason, and §10 follows.
- **CODE-7 dropped `error.slotId` from the `/start` 422 on the strength of a session identifier neither the
  body nor one of its two routes carries.** `writeSlotFailed` puts `category`, `retryable`, and `slotId`
  into `details` (`pkg/gateway/sessionserver/start.go:311-318`), the `errorBody` envelope has no session
  member (`sessionserver.go:2421-2427`), `SlotFailedError` has none (`slotfailure.go:125-130`), and the
  one-call `POST /v1/sessions/start` route (`sessionserver.go:2039`) reaches the same mapper with no session
  in its path and its session row rolled back. A bare removal would have left the client a 422 naming
  nothing while SPEC-7's restated `spec/05:558` asserted the error names the session. CODE-7 now gives
  `SlotFailedError` a `SessionID` member filled at the producer (`start.go:2785`) and emits `"sessionId"` in
  place of `"slotId"`, SPEC-7 restates the sentence on that key, and §8's tier-3 client-surface case asserts
  it on both routes. The tool-approval detail and the SSE payload keep the bare removal, since each carries
  `SessionID` already (`toolapproval.go:107`).
- **`spec/18` §18.30 still assigned per-slot workspaces to Phase 12c as a `maxConcurrentSessions > 1`
  deliverable.** After D7 and D1 the per-slot tree is the only layout, so the adapter's workspace
  materialization in Phase 2 and the session deliverables of Phases 4 and 6 require it, and `spec/18:532`
  would have made earlier phases depend on a later phase's artifact and kept the retired concurrency
  condition in the build sequence. The file is in no sweep (its only `/workspace` string is the REST route
  at `:229`) and was in no deliverable. SPEC-3 now stages the line and §10 lists `spec/18`.
- **`pkg/adapter/attach_test.go` was filed as a compile-only field drop while it is the tier-1 pin of the
  demultiplexer CODE-6 renames.** Its `frameSlotIDForTest` decoder reads the wire key by struct tag
  (`:191-196`), so the rename silently empties every assertion over it;
  `TestAttachDemultiplexesConcurrentSlotsBySlotID_spec_6_4` (`:213`) feeds frames spelled `slotId`
  (`:256-257`) that §4.6.1 would relay to no stream after the rename;
  `TestAttachNoSlotIDServesBasePath_spec_6_4` (`:290`) pins the absence-as-pod-scope reading D1 retires; and
  `TestAttachStampsInboundSlotID_spec_6_4` (`:334`) asserts the stamped key. §8 stages the file as a tier-1
  hand-rewrite beside `tracingcontext_addressing_test.go`, adds it to the fixture corpus (now eleven files,
  eight staged above), and §10 lists it.
- **The concurrent bind captures no workspace root, so CODE-2's step (d) guard stayed vacuous on the pods
  §1.2 names.** `bindReservedSlot` and `connectSlot` read only `resp.GetIncompatible()` from
  `NegotiateVersion` (`slotbinder.go:239-249`, `:461-470`), `materializeSlot`'s `BindResult` carries no
  workspace member (`:319-334`), and `finalize.go:238-240` returns before the persist for a concurrent pool,
  so the column is never written and both `persistWorkspaceRoot` and `pkg/adapter/resume.go:61` skip an
  empty value. CODE-2 now stages the capture at both slot-side handshake sites and the derived slot root on
  the slot `BindResult`, §10 lists `slotbinder.go` under the derivation entry, and §8 adds a tier-2 case on
  a `maxConcurrentSessions > 1` pool.
- **CODE-4 claimed the merged rotation's in-flight gate and ceiling are keyed on the rotating session.**
  Only the file rewrite and the `cred:{leaseId}` acknowledgement wait are. `awaitInflightGate` polls
  `s.Lifecycle.InflightCount(provider)` (`credentials.go:178, :244-249`), a pod-global per-provider counter
  fed by session-less `llm_request_started` and `llm_request_completed` frames
  (`runtimeops.go:365-368, :404-408`) over the one connection to the one runtime process
  (`server.go:234-239`); the 300s ceiling bounds that gate; and `credentials_rotated` carries no session
  (`runtimeops.go:462-469`). Making the per-slot branch universal therefore runs the protocol on concurrent
  pods with a co-tenant able to gate the rotating session, drive it to the ceiling, and hold the old
  credential across the rotation. CODE-4 now states what is separable and what is not, §9 records the
  coupling as a limit beside the manifest collision, and §8's tier-9 block pins it as observed behavior.
- **The "no credentials have been assigned" fail-closed refusal was retired unannounced.**
  `checkCredentialSession` enforces three refusals (`credentials.go:358-371`) and the per-slot handlers
  carry two: their opening `st, ok := s.slotStateLocked(slotID)` (`slotcreds.go:64-67`, `:116-119`) is a
  bare registry lookup (`slot.go:96-99`) that tests registration rather than assignment, and under D1 every
  live session holds an entry seeded with an empty lease map by `ensureSlotStateLocked` (`slot.go:74-95`).
  A rotation for a session that never received `AssignCredentials` would have materialized its credential
  file instead of being refused. §4.11 now states that the merged rotate and revoke handlers restate the
  refusal on the slot's own lease set, §8's re-basing instruction is corrected to name the refusal each of
  the three cases moves onto, and a tier-1 case pins the new arm.
- **CODE-1's start-side merge started a per-session MCP server on a pod-global socket.**
  `startPlatformMCP` binds `s.MCPSocket` (`platformmcp.go:16-26`), the one socket the controller renders
  (`podspec.go:184, :835`), and `listenIntraPodMCP` calls `net.Listen("unix", …)` with no unlink
  (`connectormcp.go:99-102`), so a second concurrent `StartSession` would fail with `EADDRINUSE`, return
  `Internal: start platform MCP server` before `Runtime.Start` (`session.go:149-157`), leave the pod-global
  manifest already rewritten, and tear the co-tenant's servers down through the single `mcpCancel` and
  `connectorCancels` (`session.go:384-408`), capping an `N`-slot pod at one live session. CODE-1 now starts
  the servers at most once per pod under an `s.mcpCancel == nil` guard and names the nonce they are armed
  with, §4.11 and §8's tier-1 teardown case state the cancellation on the last-bound predicate, §8's tier-9
  case is restated for a pod-wide surface, and §9 extends the manifest limit to the whole intra-pod MCP
  surface.
- **The mandated `ShutdownSlot`-then-`ShutdownPod` ordering removes every per-slot tree before
  `cleanupCommands` run.** `releaseSlot` (`slotsession.go:86, :102-112`) calls `RemoveTree`
  (`slotlayout/tree.go:50-57`), and the commands run inside the later `ShutdownPod` scrub
  (`scrub/scrub.go:228-241`), so no released session's state survives to the cleanup phase and CODE-1's
  claim to preserve `spec/05:453`'s "with access to session state" access was false, with §8's fourth
  tier-1 case unreachable. The removal stays at session end, because deferring it would leave an ended
  session's credential lease and workspace readable by a co-tenant and by the deployer's cleanup code, which
  is the exposure step 0 exists to prevent. CODE-1 drops the claim, SPEC-4 restates the `spec/05:453`
  parenthetical for the new ordering, and §8's fourth case asserts what is reachable.
- **CODE-2 added a slot reservation to the resume path with no compensating release.** `Binder.Resume`
  returns on every post-handshake failure without releasing (`binder.go:1574-1584`, `:1599-1602`), while the
  start path releases on each such failure and says so (`slotbinder.go:161-174`, `:207-221`), so each failed
  resume would leak `lenny:pod:{pod_id}:active_slots` and compound per retry on exactly the
  checkpoint-restore case the deliverable enables. CODE-2 now states that `Binder.Resume` owns a single
  `ReleaseSlotReservation` on every failure after the increment, so `rollbackBinding` does not run it twice,
  and §8 adds a tier-2 case on the failure path.
- **§8's tier-7a case pinned the drain signal as sent after the runtimes are closed.** The drain exists to
  precede the hard close (`session.go:261-263`), and the last session's `Close` tears the shared runtime
  down (`socketruntime.go:434-466`), while `drainViaLifecycle` swallows the not-connected and closed errors
  (`session.go:294-301`), so a signal sent afterwards is a silent no-op. SCHEMA-1 now states the merged
  handler's full order, with the drain sent immediately after the critical section and before
  `Runtime.Close`, and §8's tier-7a and tier-1 cases state the same order.
- **The per-SDK tier-3 frame-key case ran through a harness that hands the runtime no identifier.**
  `tests/tier3_contract/sdks/runtime_sdk_test.go` reads only `lenny-compliance`'s named-check report
  (`:198-212`), and every inbound `message` the binary sends carries no identifier
  (`cmd/lenny-compliance/main.go:388, :625-627`, `standard.go:488`), so "echoing the identifier the inbound
  frame handed it" was unexercisable and "carrying `sessionId`" would have passed vacuously. CODE-6 now
  stages the compliance binary's three inbound literals and a Basic-level echo check, SPEC-7 stages the
  matching §15.4.6 Basic category row, §8's case is restated against that check, and §10 lists the files.
- **The runtime-author tracing prose was staged as a key rename while it states the retired rules.**
  `docs/runtime-author-guide/platform-tools.md:428` and `:449` require the identifier only "On a pod running
  concurrent slots" and say an unaddressable frame "is dropped and logged rather than applied", both of
  which §4.6.1 replaces, and `:449` is the Basic-level tracing path that must carry the echo obligation. The
  two lines move out of §4.6.2(i)'s mechanical-rename bullet into §4.8's site list with the rewrite stated,
  since no sweep and no tier-11 reconciliation is keyed on that text.
- **The runtime-author filesystem-layout block was reached by neither SPEC-3's enumeration nor its sweep.**
  `docs/runtime-author-guide/lifecycle.md:85-97` renders the retired pod-global tree, and the path is split
  across the code block's indentation, so no line in it contains the sweep literal. SPEC-3 now stages the
  block by name with the uniform per-slot layout it is restated on.
- **`pkg/adapter/podscrub_test.go`'s eight recycle-Shutdown cases invert and were in no disposition.** Five
  bind a session and then send a recycle request on the same pod (`:190`, `:238`, `:362`, `:400`, `:666`),
  which the occupancy refusal rejects under D1; `TestShutdownRejectsRecycleWithEmptySessionID_spec_4_7`
  (`:331`) pins a guard `ShutdownPodRequest` cannot express; and `TestShutdownTerminatePathRunsNoScrub_spec_4_7`
  (`:435`) pins a disposition the split removes from the message. §8 stages the file as a hand-rewrite
  keeping its five spec-named §5.2 outcomes pinned, and the "four remaining constructions" sentence is
  replaced by the full inventory, which also omitted `session_test.go`, `mcpruntime_test.go`,
  `slot_test.go`, `scrub_wire_test.go`, `concurrent_delegation_proxy_test.go`,
  `recycle_scrub_conformance_test.go`, and the fence table in `generation_fence_wire_test.go`.
- **`pkg/adapter/sessionscrub_emit_test.go` was filed as a compile-only edit while both its base-mode cases
  invert.** `TestBaseTerminateShutdownEmitsNoSessionScrub_spec_5_2` (`:256-271`) requires zero reports on a
  terminate Shutdown, which SCHEMA-1 inverts by putting the emission on `ShutdownSlot` unconditionally, and
  `TestBaseRecycleShutdownEmitsSessionScrub_spec_5_2` (`:216-244`) treats a non-empty slot id as a failure,
  which is the base-mode empty-slot emission D1 retires. §8 stages the file as a hand-rewrite and states the
  `maxSessionsPerPod` accounting consequence, that a retire release now advances `sessions_served`, which no
  case covered.
- **The adapter client's shutdown method surface lost its only pin.**
  `pkg/gateway/runtime/adapterclient/client_test.go` was listed only as a field swap and a
  `capturingAdapter` drop, while its `recordingAdapter` serves the deleted `Shutdown` RPC (`:544`, `:571`),
  four `Terminate` cases pin the `reason` and `deadline_ms` population the collapsed method carries
  (`:661-683` asserting `USER_REVOKED` and a 10s deadline),
  `TestShutdownRecyclePopulatesTheRecycleSubMessage` requires a session id `ShutdownPodRequest` has no field
  for (`:762`), and `TestShutdownLeavesTheRecycleSubMessageNil` (`:784`) pins the presence encoding D6
  retires. §8 stages the file as a hand-rewrite and §10 lists it.
- **CODE-1's `scrub.Config` pluralization left `pkg/adapter/scrub/scrub_test.go` unbuildable.** The
  package's own tier-1 suite constructs `Config` with the two renamed fields at eleven sites, so none of
  §8's four new cases in that package could run, and three of its cases own the subjects the deliverable
  re-bases: the two step-6 verify pins (`:165`, `:182`), the purge-before-cleanup pin (`:448`), and
  `TestCleanupEnv_spec_5_2_424` (`:423-424`), which pins `/workspace/current` as the deployer-visible
  working directory the fourth new case asserts. CODE-1, §8, and §10 stage the file as a hand-rewrite.

Corrections to this pass, from a review of its own edits.

- **CODE-2's derivation-site count went stale within the pass.** One edit in this pass reduced the
  enumeration to three sites by removing the `start.go:3917` replay, and another eleven lines below added a
  fourth computation of `<base>/slots/{sessionId}/current` on the concurrent bind, where `materializeSlot`
  carries the derived slot root on its `BindResult` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:319-334`).
  CODE-2 now states four sites and names the concurrent one, "The resume is not a fourth site" reads "The
  resume is not a derivation site", and §8 and §10 carry the same count, with §10 naming `slotbinder.go` as
  the fourth site rather than as an addendum to a list of three.
- **§8's hand-rewrite exception list did not admit the file this pass converted.** The `Server.WorkspaceRoot`
  holders paragraph classified `pkg/gateway/runtime/adapterclient/client_test.go` as a compile-only field
  swap while the same pass staged it as a hand-rewrite for the deleted `Shutdown` RPC its `recordingAdapter`
  serves (`:544`, `:571`). The list now names four hand-rewrites and points at the block that states this
  one, so an implementor planning the field swap reaches the shutdown-surface rewrite.
- **SCHEMA-1's drain-order citation named the wrong line.** The sentence establishing that the drain
  precedes the hard close cited `pkg/adapter/session.go:262` for `drainViaLifecycle`, which is the
  `contextWithGraceDeadline` call. `drainViaLifecycle` is at `:261` and `Runtime.Close` at `:263`. SCHEMA-1
  and this pass's tier-7a record now cite `:261` and `:261-263`.

### Pass 19 (2026-08-17, automated)

- **SPEC-3 moved the uniform workspace layout into a Phase 2 deliverable that does not exist.** The
  ordering argument was sound and the destination was not: `spec/18:117-124` is the §18.6 Phase 2 heading
  and its first four bullets, and no bullet in §18.6 builds workspace materialization. The workspace
  pipeline first appears as a phase deliverable in Phase 4 (`spec/18:232-235`, the Upload Handler
  staging-to-promotion pipeline and the §14 workspace-plan source-type validators). SPEC-3 and its §10
  restatement now move the uniform per-slot layout into that Phase 4 deliverable and state that Phase 2
  names no workspace materialization, so the destination is located rather than invented at application
  time.
- **`spec/06:77` stated the multiplexing key and was in no edit list.** The `maxConcurrentSessions > 1` row
  of the §6.1 `preConnect` compatibility table says "Concurrent sessions multiplex onto a single pod via
  `slotId`", which is the claim SPEC-1 restates at `spec/28:1767`. Left standing, the applied specification
  would name a wire key the published schema no longer declares after SCHEMA-2. SPEC-1 now stages the line
  beside `:1767`, with the declined-rename fallback, and §3 records that the row's condition and its
  `preConnect` rejection are untouched.
- **SPEC-5's successor pointer named §6.2 as owning a now-general slot lifecycle that §6.2 still scopes to
  concurrent pods.** `spec/06:148-154` states the per-slot sub-states inside the concurrent-occupancy block
  at `:129-130`, and no deliverable reached it. SPEC-5 now stages the lift: the sub-states are stated for a
  pod of either concurrency, the multi-slot occupancy edges at `:131-146` keep the condition, the `slotId`
  spellings in the lifted block move onto the session identifier, and the reader-facing mirror at
  `docs/reference/state-machines.md:228-240` takes the same split. §10 records both.
- **The start-side merge gave every co-tenant the platform and connector MCP authority of whichever session
  started the servers.** The providers bind one session identifier at server-start time
  (`pkg/adapter/platformmcp.go:39-43`, `pkg/adapter/platformtoolprovider.go:29-38`,
  `pkg/adapter/connectormcp.go:85`), and the gateway installs that session's user and tenant as the
  principal for every forwarded call (`platformtools.go:114-121`), while tenant pinning bounds a concurrent
  pod to one tenant rather than one user (`spec/05:440`). The surface is absent today only because the
  concurrent path returns before the MCP starts, so "no worse than it is" was false. CODE-1 now resolves the
  calling session from the slot registry at call time, dispatching under the pod's single bound session and
  refusing the call with `FailedPrecondition` when the pod holds zero or more than one, which also replaces
  the retired `Server.sessionID` as the providers' source. §4.4, SPEC-5's retained §29.10 list, §9's
  recorded limit, and a new tier-9 case state the same rule.
- **The once-per-pod MCP guard was a check-then-act on pod-global state.** `startPlatformMCP` binds the
  socket before writing `s.mcpCancel` (`pkg/adapter/platformmcp.go:24, :45-49`) and no lock spans the two,
  so two concurrent `StartSession` calls could both observe nil and hand the loser `EADDRINUSE`, which is
  the outcome the guard exists to prevent. CODE-1 now takes the start decision in one critical section under
  `s.mu`, as SCHEMA-1 states for the drain gate, and §8's tier-7a block gains a `-race` case over both
  interleavings.
- **The resumed `BindResult`'s non-empty `SlotID` flipped two unstaged presence-keyed release
  dispatchers.** `PodExecutor.Release` (`pkg/gateway/session/executor/pod.go:331-333`) and `rollbackBinding`
  (`pkg/gateway/sessionserver/start.go:3079-3083`) both dispatch on `SlotID` presence, so a resumed
  exclusive-pool session would release through `ReleaseSlot`, which retains occupancy on a leaked teardown
  (`slotclaimer.go:751-757`) and hard-errors without Redis (`slotclaimer.go:745-750`) after the registry
  entry is already gone. CODE-2 now scopes the resume's slot reservation to pools with
  `maxConcurrentSessions > 1`, the concurrency stays reported on every resume, and §8 gains a tier-2 case
  over the exclusive-pool resume release on a harness with no slot counter.
- **The merged `ShutdownSlot`'s drain gate counted registry entries that had no reclaimer.** An ordinary
  bind failure after `PrepareWorkspace` (`slotbinder.go:276-303`) leaves an entry with an empty `sessionID`
  that nothing deletes, because `releaseSlot` is reached only past the bound test, so the drain gate and
  §4.6.1's count would stay above one for the pod's life and the §15.4.2 drain would silently become a
  no-op. SCHEMA-1 now states the reclaimer: the merged handler admits and deletes a registered-but-unbound
  entry for the named session, and each `materializeSlot` failure branch sends a best-effort `ShutdownSlot`
  before closing the connection. §4.11 states the one exception to `checkSessionBound`, §4.6.1 and CODE-6
  state that both decisions count every entry, and §8 gains a tier-1 and a tier-2 case.
- **The tier-2 Postgres pin of the slot-scoped selector was filed as a compile-only edit.**
  `tests/tier2_component/stores/partialmanifeststore_test.go:154-188` is the contract pin of exactly the
  behavior CODE-5 and MIG-1 change: its fixture seeds two active partial rows for one session, which the
  re-keyed `partial_manifest_active_uniq` makes unconstructible, and its `ErrNotFound` assertion at
  `:186-188` inverts under the renamed selector. §8 now states it as a tier-2 hand-rewrite that deletes the
  subtest and adds a Postgres pin of the session-keyed selector, and the file leaves the compile-only
  inventory and the fixture corpus, which is one file shorter.
- **`docs/runtime-author-guide/lifecycle.md:397` was staged as a mechanical key rename while it scopes the
  identifier obligation to `maxConcurrentSessions > 1`.** A rename alone would leave the runtime-author
  guide telling every default-pool author that neither the identifier nor the dispatch and echo obligations
  reach them, which contradicts §4.6.1 and SPEC-7. The line moves from §4.6.2(i) to §4.8's rewrite list
  beside `platform-tools.md:428` and `:449`, restated on the universal identifier, the Basic-level echo, and
  the per-session workspace, with the concurrency bullet keeping only its co-tenancy facts.
- **§4.4 restated the new MCP authority rule with a narrower refusal predicate than the four other
  statements of it.** It said the provider refuses "when the pod holds more than one bound session", which
  licenses dispatching a `tools/call` on a pod holding zero, the post-`ShutdownSlot` state the new tier-9
  case asserts is refused. §4.4 now says "other than one bound session", matching SPEC-5's retained §29.10
  bullet, CODE-1, and §9's recorded limit.
- **CODE-1 told an implementor to reuse §4.6.1's slot count for a predicate that reads a different
  quantity.** §4.6.1 and SCHEMA-1 count every registry entry, bound or registered-but-unbound, while the MCP
  resolution keys on bound entries alone, so a pod holding one bound session beside a workspace-prep entry
  (`pkg/adapter/slot.go:74-95`) refuses an unaddressed frame and dispatches an MCP call. CODE-1 now names the
  bound-entry count as its own quantity and states why an unbound entry carries no session to dispatch under.
- **§8's tier-2 resume-bind block opened on the reservation rule CODE-2 had just narrowed.** Its first
  sentence asked for a slot on every resume while its third case asserts an empty `SlotID` on a
  `maxConcurrentSessions: 1` pool. The opening sentence now reports the concurrency on every resume and
  scopes the slot to pools above one.
- **CODE-2's two new `start.go` citations named the wrong lines.** `s.podRegistry.Put(result.Result)` is at
  `start.go:3936` and `s.rollbackBinding(ctx, result.Result)` at `:3933`; the cited `:3925` is a comment line
  and `:3934` is the following `return`. Both citations are corrected.

### Pass 20 (2026-08-17, automated)

- **The `spec/28` sentences that scope the intra-pod MCP channels per session were in no edit list.** The
  `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` Exclusivity bullets (`spec/28:1153-1156`, `:1198-1200`), the MCP
  clause of the §28.6 restatement (`:1683-1686`, which begins on the same line as the `CH-MSGSOCK` clause
  SPEC-1 already staged and runs past it), and the Exclusivity column of the two §28.8 failure-mode rows
  (`:1769`, `:1770`) all state that the connection is scoped per session through the `mcpNonce`. CODE-1
  starts the servers at most once per pod and resolves the calling session from the slot registry, so all
  five become false, and the applied specification would have contradicted the pod-wide §29.10 bullet
  SPEC-5 adds in the cards `spec/04:709` directs a reader to. SPEC-1 now stages all five, and §10 records
  them beside the other `spec/28` edits.
- **The unaddressed-frame rejection counter was defined per frame and implemented per Attach stream.**
  `demuxSlotOutput` is constructed once per Attach stream (`pkg/adapter/attach.go:72`, inside `Attach` at
  `:28`), and every stream subscribes to the same runtime fan-out (`:271-274`), so one unaddressed frame on
  a pod holding more than one live stream is evaluated once per stream. §4.6.1, CODE-6, and SPEC-9's §16.1
  row now state the counter and the log line as per rejecting Attach stream, and §8's tier-1 case asserts an
  increment of exactly two on a two-slot pod rather than a non-zero increment. Moving the decision ahead of
  the fan-out would need a pod-level reader of the shared runtime output that no code path holds, which is
  recorded rather than staged.
- **SPEC-3 and §8 stated two different domains for the same tier-11 `/workspace/current` gate.** SPEC-3
  named the seven-surface predicate and then described the gate as covering `docs/`, `spec/`, and the served
  document, while §8 states all seven. Every surviving occurrence outside the three, including
  `cmd/lenny-adapter/main.go`'s `--workspace-root` default, the scaffold templates, the reference runtimes
  under `cmd/runtimes/`, `sdks/runtime/go/runtime/types.go`, `schemas/workspaceplan-v1.json`, and
  `schemas/runtime-ops-events.schema.json`, would have passed a gate written from SPEC-3. SPEC-3 now states
  the gate over the whole predicate and names those surfaces.
- **CODE-1 emptied the source of the required §4.7 manifest `llm` object.** `manifestLLM`
  (`pkg/adapter/manifest.go:311-320`) reads `s.credLeases`, whose only non-dead writer is the pod-global
  `AssignCredentials` branch at `pkg/adapter/credentials.go:97` that the `useSlot` deletion makes
  unreachable, so every session would have received a manifest whose `llm` serializes as null. That drops a
  field `spec/04:788-792` marks required at all levels and, in proxy mode, the `baseUrl`, `dialect`, and
  `apiKeyEnv` that point the runtime at the §4.9 LLM proxy. CODE-1 now derives the object from the bound
  slot's lease set (`pkg/adapter/slotcreds.go:47`) and retires `Server.credLeases`,
  `cloneCredentialLeases`, and the pod-global expiry path with it; §8 gains the tier-1 manifest assertion
  and a tier-9 case on the proxy-mode session.
- **§4.11, §8, and §9 named an unreachable branch as the surviving F-6.1.12 credential refusal.** Under D5
  the registry key is the session identifier, so `assignCredentialsSlot` resolves an entry whose `sessionID`
  is either empty, which `pkg/adapter/slotcreds.go:33-34` then fills with that same value, or already equal,
  and the arm at `:35-37` cannot fire; the request carries no second identifier, so §8's case was
  unconstructible. All three sections now state that no adapter-side session-identity refusal survives on
  `AssignCredentials`, that the merged handler's remaining preconditions are §4.2's non-empty `session_id`
  and the credentials-root check, and that the gateway-side teardown loop is the remaining defence. The
  wrong-session arms of the per-slot rotate and revoke handlers (`:68-71`, `:120-123`) are recorded as bound
  tests for the same reason, and the two `credentials_test.go` cases that drove them are re-based onto the
  refusals the merged handlers do reach.
- **CODE-3 armed a single-valued termination from a registry that can hold many bound sessions.**
  `holdState` carries one `session` (`pkg/adapter/holdstate.go:39-45`) and `onHoldTimeout` closes one
  session's runtime and writes one post-mortem (`:175`, `:180`), while `SocketRuntimeProcess.Close` returns
  early with a sibling active (`pkg/adapter/socketruntime.go:440-446`), so the §10.1.4 termination would
  have left every co-tenant running unsupervised with no reconciler reaching it. CODE-3 now records the set
  of bound sessions at arming and runs the termination once per member, keeping `s.hold.active` and the
  fence exit pod-wide, §3 states the same, §8 gains a tier-1 case driving the timeout on a two-slot pod, and
  §9 records that the `AdapterTerminating` event still names no session.
- **`pkg/adapter/credexpiry_test.go` was staged as a four-line fixture swap.** Its base-mode §4.9 cases
  drive the credential RPCs with `session_id` alone, so CODE-1 routes them to the per-slot handlers while
  their assertions still read the pod-global file (`fileProviders`, `:116-124`) and the pod-global timer map
  (`:164, 196, 215, 273, 313, 379, 460, 513`), neither of which the compiler catches. The file is now a
  hand-rewrite in §8's tier-1 credential block, with the §4.9 arms that must survive the re-base named and
  `TestReleaseSessionCancelsExpiryTimers_spec_4_9` moved onto the slot release path, and §10 inventories it.
- **§8 prescribed a `ShutdownSlot` for a podscrub case that binds no session.**
  `TestShutdownRecycleConcurrentModeTriggersWholePodScrub_spec_5_2` (`pkg/adapter/podscrub_test.go:284`)
  starts no session and registers no slot (`:287-291`), so the prescribed call would be refused by
  `pkg/adapter/slotsession.go:64-68` and the case would fail at its first call. §8 now moves it onto
  `ShutdownPod` alone and names it as the pin of the admit arm of the re-keyed occupancy guard.
- **The `credexpiry_test.go` pointer stranded the split-conditional sentence in §8's fixture paragraph.**
  Inserting the pointer between the two-file fixture list and the sentence beginning "That last one" made
  that sentence resolve to `pkg/adapter/credexpiry_test.go`, which is a tier-1 `pkg/adapter` file whose
  hand-rewrite is stated in §8's tier-1 credential block and whose disposition carries no dependency on the
  §4.5(d) shutdown split. The sentence now names
  `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:48`, its actual referent and the
  file §8's whole-pod-scrub paragraph records as the only edit needed if review declines the split, and the
  `credexpiry_test.go` pointer follows it.
- **The Pass 7 record still listed the wrong-session `AssignCredentials` case as §8 content.** That case was
  removed from §8 in this pass, because the arm at `pkg/adapter/slotcreds.go:35-37` is unreachable under D5.
  The Pass 7 bullet now records the removal and names the three cases §8 carries.

### Pass 21 (2026-08-17, automated)

- **SPEC-4 rewrote `spec/13:30`'s mandatory lease-isolation control on a premise that paragraph denies.**
  §1.3 claimed the paragraph relies on per-slot credential isolation, and SPEC-4 restated its advice on the
  ground that "with §1.3 fixed the isolation holds per slot". The paragraph states the opposite: each
  per-slot file is mode `0440` under the shared `lenny-cred-readers` group, so "any slot's agent code can
  read every other slot's credential file", Lenny "does not mitigate this at the pod level", and the
  `sessionPolicy.maxConcurrentSessions: 1` requirement rests on the pod holding at most one lease at a
  time. §5 keeps that group-ownership scheme, so the restatement would have weakened a fail-closed control
  on a false ground. §1.3 now states the property the pod-global rewrite actually breaks, which is the
  rotation independence `spec/06:28` gives as the reason for the per-slot lease, SPEC-4 and §3 narrow the
  `spec/13:30` edit to the placeholder rename plus one sentence on per-session rotation with the
  readability statement and the requirement kept verbatim, and §4.4's retained co-tenancy list gains
  shared credential-file group-read access, which `spec/05:515` enumerates and the retained
  `spec/29:1512-1516` bullet already states.
- **SCHEMA-1's drain gate and the hard close read two different quantities.** The gate counted every
  registry entry, while `Runtime.Close` decides its teardown from the runtime's active set, which only
  `Start` populates (`pkg/adapter/socketruntime.go:181-186, :372-376, :439-444`). A registered-but-unbound
  entry is not in that set, so in the interleaving §8 pins the ending session was the set's only member:
  the shared runtime would have been closed and its listener shut (`:452-466`) with no preceding
  `terminate`, which is the silent §15.4.2 regression the ordering rule exists to prevent, and the incoming
  session's `Runtime.Start` would have found nothing to accept. `Runtime.Close` now takes the teardown
  decision from the merged handler's critical section, so the drain and the hard close are withheld on one
  number, and §8's second tier-7a case asserts that the shared runtime survives and the incoming session's
  `StartSession` reaches a live `Runtime.Start`.
- **CODE-1 retired `Server.sessionID` without re-pointing its two surviving production readers.**
  `CurrentSessionID` (`pkg/adapter/adapterevents.go:175-181`) is the direct-mode token sink's session
  source through `WireDirectModeUsage` (`pkg/adapter/usage.go:247`), and `emitControlEvent` (`:149-152`)
  stamps every unstamped `CH-ADAPTEREVENTS` envelope from it. Both would have folded and stamped under the
  empty session on every pod, with no compiler error. CODE-1 now re-points both onto the pod's single bound
  registry entry, the resolution its MCP providers already take, dropping the fold and leaving the envelope
  unstamped when the pod holds other than one bound session; §9 records the resulting limit, §8 stages
  `pkg/adapter/usage_test.go` and `pkg/adapter/adapterevents_test.go` as hand-rewrites with two new cases
  on the resolution, and §10 inventories both files and `adapterevents.go`.
- **The atomicity pin of the §28.5.3 addressing decision had no disposition.**
  `pkg/adapter/tracingcontext_sampling_test.go` pins that both terms of condition 2 are read under one
  `s.mu` hold, and its premise is the slotless branch §4.8 deletes (`:65`). It also writes the retired
  `Server.sessionID` (`:45`, `:50`) and pairs `sess-slot-a` with `slot-a` (`:46`), so it breaks the
  `pkg/adapter` build and carries a fixture D5 makes unconstructible, while appearing nowhere in the draft.
  §8 now stages it as a hand-rewrite whose subject moves onto the two-term read §4.6.1 introduces, the
  fixture corpus counts eleven files, and §10 inventories it.
- **Correction: SCHEMA-1's caller-supplied teardown decision changed an interface with no blast radius.**
  `Close` is a method on `RuntimeProcess` (`pkg/adapter/session.go:62`), so the decision parameter reaches
  every implementation and caller. SCHEMA-1 now states the signature and what `SocketRuntimeProcess`,
  `MCPRuntime`, `InProcessRuntime`, and `SDKWarmInProcessRuntime`'s `DemoteSDK` each do with it, §10 inventories
  `mcpruntime.go`, `embedded.go`, and `embedded_sdkwarm.go` beside the interface, and a new §10 bullet
  carries the `RuntimeProcess` test doubles and the direct `Close` call sites. `onHoldTimeout`
  (`pkg/adapter/holdstate.go:180`) reads no registry, so CODE-3 states the value its per-member loop
  passes, false on every member but the last, which is what keeps "closed when the last member is
  released" true under the new contract; CODE-3's premise and §8's hold case no longer reason from the
  `releaseActiveLocked` early return alone, which the parameter now conjoins with.
- **Correction: CODE-1's `emitControlEvent` re-point named three of the seven unstamped emitters.** Every
  emitter constructing a `controlEvent` without a `SessionID` is stamped by the accessor, which adds
  LEASE_REJECTED (`adapterevents.go:204`), ADAPTER_TERMINATING (`:217`), FINAL_USAGE_REPORT (`:224`), and
  `EmitCheckpointBarrierAck` (`pkg/adapter/coordination.go:292-298`). FINAL_USAGE_REPORT is the
  consequential one: `emitFinalUsage(ctx, sessionID)` holds one session's totals at the call site
  (`pkg/adapter/session.go:333`, `:341`) and the gateway waits for it before `budget_return.lua`, so
  CODE-1 stamps it from the caller rather than the registry and §8 pins it on a two-slot pod. The
  supporting analogy that `AdapterTerminating` "names no session at all" was false at
  `adapterevents.go:216-218`, which passes a reason and leaves the stamp to the same accessor, and is
  removed.
- **Correction: the fixture corpus totalled eleven over a subordinate count of seven.** The staged-above
  list names eight files. §8 now reads eight.
- **Correction: §8's tier-9 rationale still called §1.3 a cross-session credential read.** The Pass 21
  rewrite of §1.3 narrowed the defect to rotation independence and left the `spec/13:30` read boundary
  untouched. §8's rationale, §1.3's heading, and REG-1's row description now name that property.
- **Correction: two citations added this round were off by a line or a block.**
  `socketruntime.go:455-465` is the `cmd` grace block alone, so the sentence naming the connection close
  and the listener close cites `:452-466`; the `tracingFrameAddressesStream(sessionID, "", "")` call is at
  `tracingcontext_sampling_test.go:65` rather than `:66`.

### Pass 22 (2026-08-17, automated)

- **The reclaim arm sent the drain and skipped the hard close, so the withheld close was never
  discharged.** SCHEMA-1 named the reclaimer as the bound on a withheld `Runtime.Close`, and the reclaimer
  closed nothing. On a pod holding one bound session and one registered-but-unbound entry, the bound
  session's `ShutdownSlot` withholds the close, and the reclaim `ShutdownSlot` then sent a `terminate`
  frame and returned, leaving no caller to close the process: the three production `Runtime.Close` callers
  are `pkg/adapter/slotsession.go:83`, `pkg/adapter/session.go:263`, and `pkg/adapter/holdstate.go:180`,
  and the whole-pod scrub closes nothing. `p.connected` is written false only inside `Close`
  (`pkg/adapter/socketruntime.go:449`), so after the runtime honoured the frame and exited, the child would
  never be reaped, the listener would stay open, and the next session's `Runtime.Start` would return at
  `:182-187` against a dead process. The reclaim arm now calls `Runtime.Close` carrying its own
  registry-empty result, and §8's tier-1 reclaimer block gains a sixth case asserting that the process is
  closed once the reclaim empties the registry and that a later session's `Runtime.Start` spawns a fresh
  one.
- **Deciding the hard close from the caller's flag alone dropped the teardown's atomicity with a
  concurrent `Start`.** `Start` adds a session to the active set under `p.mu` whenever the process is
  connected (`pkg/adapter/socketruntime.go:181-187`), the same mutex `Close` takes at `:435`, so today's
  `releaseActiveLocked` test observes a session that starts during a teardown. A `last` computed earlier,
  under `s.mu` and against the slot registry, makes the teardown a read of one structure followed by an act
  on another, and an incoming session whose `Start` returned during the intervening drain send would lose
  the connection under it, with every later send failing on `socket runtime is not connected` (`:326`,
  `:344`). SCHEMA-1 now states the conjunction: `Close` removes the ending session from the active set on
  every call and tears the connection down only when `last` is true and that removal left the set empty.
  The widening the registered-but-unbound case needs is preserved, and §8's second tier-7a case gains the
  interleaving in which the ending session's critical section reads the registry empty while the incoming
  session's `Start` completes, asserting that the shared connection is still open afterwards and the
  incoming session's stream is usable. The Pass
  18 record's phrasing that the parameter replaces the `releaseActiveLocked` early return is corrected to
  the conjunction.
- **The Basic-level echo check CODE-6 adds would have failed every bundled reference runtime, and §8
  claimed coverage for the SDK example runtimes alone.** The Basic battery runs against `echo`,
  `streaming-echo`, and `delegation-echo`, and existing cases require every check in it to pass
  (`tests/tier10_conformance/scaffolds_test.go:202-212` and `:340-352`,
  `tests/tier10_conformance/reference_battery_test.go:41-43`, and
  `cmd/lenny-compliance/standard_test.go:73-82`). None of the three can echo an identifier: each decodes an
  inbound envelope declaring only `type`, `id`, and `input` and emits a `response` declaring only `type`
  and `output` (`pkg/runtimekit/echocore/echocore.go:110-114, 176-179`,
  `cmd/runtimes/streaming-echo/main.go:396-400, 466-469`, and
  `cmd/runtimes/delegation-echo/main.go:235-239, 608-612`). CODE-6 now stages `pkg/runtimekit/echocore`,
  `cmd/runtimes/streaming-echo`, and `cmd/runtimes/delegation-echo`, with `cmd/runtimes/echo` and
  `cmd/runtimes/echo-embedded` inheriting the edit through `echocore`; §8's tier-10 block gives the
  bundled-runtime batteries their disposition; the tier-3 block states the reject arm, that a runtime
  returning a `response` without the identifier it was handed fails the battery; and §10 records the three
  runtime files and narrows its `cmd/runtimes/echo` sentence to the workspace-path edit it was about. The
  `tool_call` half of the obligation stays vacuous for these three, because none emits one and the
  Standard-level correlation check tolerates a runtime that emits none
  (`cmd/lenny-compliance/standard.go:692-702`).
- **The tier-7a interleaving this pass added asserted runtime liveness on the one path where the drain had
  already been sent.** The `Close` conjunction protects the shared connection object; it withholds nothing
  from the drain, and on the registry-empty interleaving the drain was not withheld. `RuntimeOps.Terminate`
  tells the runtime to exit within `deadlineMs` and is followed by SIGTERM at the deadline
  (`pkg/adapter/runtimeops.go:481-491`), and `p.connected` is written false only inside `Close`
  (`pkg/adapter/socketruntime.go:449`), so a runtime that honours the frame exits and nothing reaps it. The
  assertion pinned a property SCHEMA-1 does not deliver on that path, and it named the wrong mechanism: what
  the interleaving exposes is the drain gate's own read-then-act window rather than the `Close` conjunction.
  §8's case now asserts the connection teardown alone, that the ending session's `Close` leaves the shared
  connection open and a send on the incoming session's stream succeeds, states that it runs against a
  runtime stub that ignores `terminate`, and §9 records the drain window as a limit with the two ways to
  narrow it.

### Pass 23 (2026-08-17, automated)

- **CODE-3's hold-termination set was recorded at arming time and could go stale.** The deliverable argued
  that no session binds after the hold arms because `StartSession` is outside
  `coordinatorHoldAllowedMethods`. The allowlist gates RPC admission rather than binding:
  `holdStateUnaryInterceptor` evaluates `s.inHoldState()` once at entry and then calls the handler
  (`pkg/adapter/holdstate.go:245-249`), and arming is two steps, the read of the bound sessions in
  `onCoordinatorChannelClosed` (`:91-96`) and the `s.hold.active` write in `enterHoldState` (`:115`). A
  `StartSession` admitted before that write binds after the read, is absent from a recorded set, and is
  still live when the 120-second default fires (`:71-77`); `Close` on the last recorded member then finds it
  in the runtime's active set and returns early (`pkg/adapter/socketruntime.go:440-445`), leaving the
  co-tenant outcome the per-member termination exists to prevent. CODE-3 now reads the registry when
  `onHoldTimeout` fires, SCHEMA-1's teardown-decision sentence names that set, and §8 adds a tier-7a case
  driving the interleaving under `-race`.
- **SCHEMA-1's reclaim arm called a `Runtime.Close` that cannot be recovered from.** `Close` ends at
  `return p.listener.Close()` (`pkg/adapter/socketruntime.go:466`), and the listener is bound once, in
  `NewSocketRuntimeProcess` (`:156-161`) from `cmd/lenny-adapter/main.go:354` at adapter startup, with no
  path rebinding it, so a later `Runtime.Start` takes the spawn branch and then fails in `accept` (`:285`)
  with `net.ErrClosed`. The pod stays placeable in that state, because `ReleaseSlotReservation` decrements
  with `leaked` false (`pkg/gateway/podlifecycle/podsession/slotbinder.go:498`) and the per-pod claim
  survives while sibling slots remain (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:766-769`), so
  §8's sixth case could not pass against the real `SocketRuntimeProcess`. SCHEMA-1 now drops the listener
  close from `Close` with the same signature change, on the ground that the listener's lifetime is the
  adapter process's and the same unrecoverable teardown sits under `onHoldTimeout`'s close and the last
  bound session's close on a recycling pool. §8's sixth case asserts the later `Start` accepts and serves,
  a seventh case pins the same property on the ordinary teardown, and §10 lists the change.
- **`pkg/adapter/session_test.go` was filed as a helper rewrite while three of its cases assert refusals
  the merged handlers no longer return.** `TestStartSessionRejectsSecondSession` (`:244`) is a fourth
  pod-idleness case, since §4.11's merged claim does not reproduce that form, and
  `TestSendMessageRejectsUnknownSession` (`:289`) and `TestShutdownRejectsUnknownSession` (`:395`) require
  `NotFound` where the merged handlers return `FailedPrecondition` for an unregistered identifier
  (`pkg/adapter/slotsession.go:64-68`, `:116-121`). None is compiler-caught, because `startReq`
  (`:225-229`) sets no `SlotId`. §8 names the file as a hand-rewrite, adds the pod-idleness case to the
  inversion list, states the two re-bases, and drops `:400` from the call-site-swap list.
- **`pkg/adapter/slot_test.go` was filed as a nine-site fixture swap while two of its cases lose their
  subject.** `TestNoSlotIDKeepsBasePath_spec_6_4` (`:340`) is this file's pin of absence-as-pod-scope, whose
  premise D1 retires, and the cross-session arm of `TestSendMessageSlotRoutesToSlotRuntime_spec_6_4`
  (`:265-274`) requires `NotFound` and no delivery for a request that, once it carries only the session
  identifier, names a session bound on the same pod and is delivered. §8 now states the file as a tier-1
  hand-rewrite for those two cases, deleting the first and re-basing the second onto the
  `FailedPrecondition` the merged path reaches, and the fixture entry points at it.
- **The resume path's pod-idleness refusal was retired with no disposition for its only pin.** `Resume`
  claims through the same `claimSession` (`pkg/adapter/resume.go:47`), so CODE-1's re-key admits a resume
  for a second session, and `TestResumeRejectsANonIdlePod` (`pkg/adapter/resume_test.go:283`) asserts the
  opposite while compiling unchanged. §4.11 and §9 now name both `claimSession` call sites, §8 adds the
  case to the pod-idleness inversion list, and the `resume_test.go` paragraph points at it.

### Pass 24 (2026-08-17, automated)

- **CODE-2 rendered a flag the embedded-model runtime binaries do not declare.** The argv at
  `pkg/controller/sandbox/podspec/podspec.go:669` is the `runtime` container's `Args` (`:678-683`), parsed by
  the runtime image's own binary, and the two in-tree embedded-model binaries declare `--workspace-root` and
  call `flag.Parse()` (`cmd/runtimes/echo-embedded/main.go:77, :99`;
  `cmd/runtimes/preconnect-echo/main.go:56, :76`), which exits on an undeclared flag, a hazard
  `echo-embedded/main.go:90-94` already records. Rendering `--workspace-base` would crash every
  embedded-model pod at container start, and the only compiler-caught break in those files is the
  `adapterSrv.WorkspaceRoot` assignment. CODE-2 now stages the flag replacement and the
  `adapterSrv.WorkspaceBase` assignment in both binaries with their argv-passing tests, §10 names the
  assignments rather than the defaults alone, and §8 adds a tier-2 case starting an embedded runtime with
  the rendered argv.
- **Two `spec/29` sentences naming the JSONL wire key sat in no rename deliverable.** SPEC-1 listed
  `spec/29:52` and `:435` only as presence conditions, while SCHEMA-2 leaves the published schema declaring
  no `slotId` property, so both sentences would name a wire key that no longer exists. `spec/29:52` carries
  no `maxConcurrentSessions` condition at all and is a rename site alone; it is removed from the
  presence-condition list. SPEC-1 now states the §4.6.2 rename for both lines beside `spec/28:1767` and
  `spec/06:77`, with the declined-§4.6.2 fallback for each.
- **CODE-1's once-per-pod MCP guard covered one of three start sites.** `startPlatformMCP` is called from
  `pkg/adapter/session.go:150`, `pkg/adapter/resume.go:119`, and `pkg/adapter/sdkwarm.go:240`, and §4.11's
  retirement of `claimSession`'s different-session refusal newly admits a second session through `Resume`
  (`resume.go:47`). Unguarded, that resume rewrites the co-tenant's manifest nonce (`resume.go:106`), fails
  the bind with `EADDRINUSE`, and cancels the co-tenant's MCP servers through the pod-global `mcpCancel`
  (`resume.go:120`). CODE-1 now applies the guard at all three sites under the same critical section, §10
  names them, and §8 adds a tier-7a case driving a second `Resume` onto an occupied pod.
- **SPEC-4 rewrote the §6.1 credential paragraph and left its per-slot in-flight-gate clause standing.**
  `spec/06_warm-pod-model.md:28` states that the in-flight gate and the `credentials_rotated`
  acknowledgement apply per slot, which CODE-4 and §9 record as undelivered: `awaitInflightGate` polls a
  pod-global per-provider counter and the 300-second ceiling is pod-scoped with it. SPEC-4 now states that
  the merged paragraph restates the clause on the credential file and the `cred:{leaseId}` wait being per
  session while the gate and its ceiling stay pod-wide per provider (`spec/04:826`), and §1.3 and §8's
  tier-9 block name the file separation as the half the fix restores.
- **Retiring `ConfigureWorkspace`'s different-session refusal re-pointed the one pre-connected SDK.** The
  RPC is admitted only on an SDK-warm pod (`pkg/adapter/sdkwarm.go:212-215`) and
  `spec/06_warm-pod-model.md:71` fixes that pod class at `maxConcurrentSessions: 1`, so a second session has
  no runtime of its own: the handler re-points the incumbent's process (`sdkwarm.go:249`) after rewriting
  the pod-global manifest (`sdkwarm.go:229`) and regenerating the nonce the incumbent authenticated with.
  §4.11 now keeps `claimSessionForConfigure`'s `Unavailable` (`sdkwarm.go:296-298`) while `claimSession`'s is
  retired, §9's limit separates the two, §8's tier-1 case asserts the refusal and the unchanged nonce, and
  `sdkwarm_test.go:149-159` moves from the inversion list to the surviving cases.
- **§9's `AdapterTerminating` limit stated the event carries no session.** The envelope declares a
  `sessionId` and `emitControlEvent` stamps it for every event arriving without one
  (`pkg/adapter/adapterevents.go:60, :149-152`), which the same document already records at CODE-1 and in
  the Pass-22 entry. The limit this change introduces is that CODE-1's single-bound-session resolution
  leaves the field empty on a pod holding more than one, which CODE-3 newly reaches. §9's bullet now states
  that limit with the correct citations, and CODE-3 stages the `spec/10:58` sentence covering the concurrent
  case and the orphan-reconciler backstop rather than resting its omission on the false premise. §8's
  tier-7a hold case asserts one envelope with an empty `sessionId`.
- **Correction to the entry above: the `spec/10:58` edit was staged inside a code deliverable.** CODE-3 is a
  code deliverable, and this document states twice that the pipeline lands every staged specification edit
  before the code phase and then blocks `spec/` writes, so an edit staged inside a code deliverable is never
  written; the Pass-3 entry above records the same defect for CODE-6's two `spec/16` rows and cured it the
  same way. The `spec/10:58` edit now moves to SPEC-9, which already owns the `spec/10` edits. CODE-3 states
  the edit it needs and points at SPEC-9 for the staging, SPEC-9 carries the bullet's new text, and §9 and
  §10 attribute the edit to SPEC-9.
- **Correction to the SDK-warm entry above: the parallel `StartSession` case was left inverted.**
  `sdkwarm_test.go:118-121` is a `StartSession` for a second session on the same SDK-warm fixture, so the
  ground that keeps `claimSessionForConfigure`'s refusal holds there unchanged, and the staged inversion was
  not reachable in any case: the pre-connected runtime refuses a second session at `Runtime.Start`
  (`pkg/adapter/embedded.go:69-71`, reached through `embedded_sdkwarm.go:50-51`), so the case could not
  assert admission on a second slot. §4.11 now keeps the different-session refusal on an SDK-warm pod for
  `StartSession` and `Resume` as well, gated on the `s.sdkWarmRuntime()` type assertion
  (`pkg/adapter/sdkwarm.go:139-142`) rather than on a live slot count, and retires it on a pod-warm pod
  alone. §4.11's `claimSession` paragraph, §4.7's MCP-guard paragraph, §9's limit, and §8's tier-1 and
  hand-rewrite blocks all carry the pod-warm qualification, and `sdkwarm_test.go:118-121` moves from the
  inversion list to the surviving cases.

### Pass 25 (2026-08-17, automated)

- **§8's tier-7a Resume case asserted a manifest property CODE-1's guard does not deliver.** The case
  asserted that the incumbent session's manifest nonce survives a second-session `Resume`, and CODE-1
  listed the co-tenant manifest rewrite among the consequences the guard prevents. The guard is scoped to
  the MCP server start, while `Resume` writes the pod-global manifest unconditionally at
  `pkg/adapter/resume.go:106`, before it reaches `startPlatformMCP` at `:119`, so the resuming session's
  `sessionId` and `mcpNonce` replace the incumbent's whether or not the guard is present. The assertion also
  contradicted §8's tier-1 merged-start case and §9's recorded limit, which both state that a later start
  rewrites the single manifest. The tier-7a case now asserts what the guard preserves (one platform MCP
  server and one per-connector socket set bound, the live server holding the nonce of the session that bound
  it, no `Internal: start platform MCP server` return, and no cross-cancellation) and asserts the manifest
  rewrite in the opposite direction, pinning §9's limit. CODE-1's sentence lists the rewrite as a
  consequence the guard does not prevent.
- **CODE-2 gave one propagated member two meanings, so the single persist site derived twice on the
  concurrent path.** `negotiated`, `BindResult`, and `PrepareResult` were stated to carry the workspace base
  while `materializeSlot`'s `BindResult` was stated to carry the already-derived slot root, and both feed the
  one `persistWorkspaceRoot` call at `pkg/gateway/sessionserver/start.go:2854` reached from `publishBinding`
  for both bind paths. A concurrent bind would therefore have written
  `<base>/slots/{sid}/current/slots/{sid}/current` into `sessions.workspace_root`, which the §7.3 step (d)
  guard at `pkg/adapter/resume.go:61` rejects with `FailedPrecondition`, and §8's tier-2 concurrent case
  would have failed as written. The member now carries one meaning: it is renamed `WorkspaceBase` on
  `negotiated`, `BindResult`, and `PrepareResult`, every bind path propagates the reported base verbatim, and
  the derivation moves into `persistWorkspaceRoot` (`start.go:3271-3284`), which derives from the base and the
  session identifier it already takes. This keeps the derivation at one site for both persist call sites
  rather than duplicating it at each, which matters because the function is first-non-empty-wins. The
  derivation-site count drops from four to two (the persist function and the `ConfigureWorkspace` `cwd` at
  `pkg/gateway/podlifecycle/podsession/binder.go:989`), and CODE-2, §8's two tier-2 cases, and §10 carry the
  same count. §8's concurrent round-trip case states that its exact-value assertion is also what catches a
  doubled `slots/{sessionId}/current` segment.
- **Correction to the first item: the replacement manifest assertion was interleaving-dependent.** The
  replacement asserted that the pod-global file ends up carrying the resuming session's `sessionId` and
  `mcpNonce`, but the case is defined to hold on either interleaving and both calls write that one file:
  `StartSession` at `pkg/adapter/session.go:129` and `Resume` at `pkg/adapter/resume.go:106`, with neither
  write ordered against the other and neither under the guard CODE-1 scopes to `startPlatformMCP`. On the
  interleaving where the `StartSession` write lands last, the file names the starting session and the
  assertion fails on the applied code. The case now asserts the interleaving-independent property §9 records:
  after both calls return the file carries exactly one session's `sessionId` and `mcpNonce`, so the
  incumbent's values do not survive, and the live platform MCP server keeps the nonce it was armed with
  whether or not the file still carries it.

### Pass 26 (2026-08-17, automated)

- **CODE-2 told `Binder.Resume` to reserve its slot through a claim that selects its own pod.** The start
  path's claim is `SlotClaimer.ClaimSlot`, whose `SlotRequest`
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:207-236`) carries no pod field and which lists every
  Sandbox in the pool and picks one (`:392-417`), while `Binder.Resume` has already acquired a different pod
  through `connect`'s whole-pod `podclaim.ClaimRequest` (`binder.go:1580-1584`, `:1639-1670`) and runs every
  later resume RPC against it. As staged the reservation would have incremented `active_slots` on a third
  pod, left the resumed session running on a pod with no reservation, and given
  `ReleaseSlotReservation(sandboxName, sessionID)` (`slotbinder.go:489-499`) no way to name the pod the scan
  chose. Two inputs were also absent from the actor: `ClaimSlot` refuses `MaxConcurrentSessions` below 1 and
  a nil `Counter`, and `ResumeRequest` (`binder.go:611-641`) carries no concurrency while the proposal
  locates the resolution at the caller. CODE-2 now stages the entry point the action needs, a pod-targeted
  `ReserveSlotOnPod(ctx, sandboxName, req)` factored onto the existing `reserveSlot` (`slotclaimer.go:597`)
  with `freshPod` false, so the reservation lands on the pod `connect` already claimed and the compensating
  release names that same pod; `ResumeRequest` gains `MaxConcurrentSessions`; §8's tier-2 resume case
  asserts the increment lands on the pod the resumed session is bound to; and §10 records both.
- **The shared MCP surface's occupancy-keyed refusal is a property of the pod, not of the caller.** A pod
  holding two bound sessions for two users refuses every `tools/list` and `tools/call`, but a per-session
  `Runtime.Close` does not stop the ended session's agent code inside the one runtime process
  (`pkg/adapter/socketruntime.go:440-445`) and SCHEMA-1 keeps the servers up while a co-tenant is bound, so
  the moment the first session's `ShutdownSlot` completes the registry holds one bound entry, the refusal
  stops firing, and that code's calls are forwarded under the survivor's user and tenant
  (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:114-123`,
  `pkg/gateway/connectors/connectortools/connectortools.go:166-176`). Tenant pinning bounds the pod to one
  tenant rather than one user, and `spec/05:515` enumerates what
  `acknowledgeProcessLevelIsolation` makes a deployer accept without naming tool-invocation authority.
  CODE-1 now makes the refusal sticky: once the pod's slot registry has held more than one bound entry at
  any point in its life, every subsequent call on its platform and connector sockets is refused whatever the
  current count, set on the same registry write under `s.mu`. Refusing on the pool ceiling is unavailable,
  because §4.11 establishes the adapter is never told it. §4.4, SPEC-1's intra-pod MCP restatement, SPEC-5's
  retained §29.10 bullet, and §9's recorded limit carry the same predicate, and §8's tier-9 MCP case gains
  the one-ends-one-remains arm no listed case drove.
- **SPEC-5 lifted the whole per-slot sub-state machine while two of its states are defined only for a
  concurrent pod.** The `leaked` and `failed` states take their normative semantics from
  `spec/06_warm-pod-model.md:159`, which counts a leaked slot in the pod's Redis slot-counter occupancy and
  against the `ceil(maxConcurrentSessions/2)` threshold `spec/05:538-557` owns, and no deliverable reached
  that paragraph. An exclusive-pool bind reserves no slot and keeps an empty `BindResult.SlotID` under
  CODE-2, so it has no counter entry, and `ceil(1/2)` is 1, which would drive every exclusive pod to
  `draining` on one leak, a behavior both drain-request producers refuse to perform for an exclusive pool
  (`pkg/gateway/session/recycle/scrubreporter_seams.go:373`, `slotbinder.go:579-581`). SPEC-5 now lifts the
  five sub-states that describe one slot's own progress (`spec/06:149-151` and `:153`) and leaves the
  `running ──→ failed` and `slot_cleanup ──→ leaked` edges (`:152`, `:154`) inside the concurrent-occupancy
  block with the accounting they rest on, which is the narrower of the two ways to make §6.2 consistent and
  leaves `spec/06:159`, `spec/05:538-557`, and the reader-facing threshold row at
  `docs/reference/state-machines.md:236` unedited. §10 records the narrowed lift.
- **CODE-3 named a backstop the same deliverable proves cannot fire.** The staged `spec/10:58` sentence had
  a concurrent-pod hold timeout emit one notification with no session identifier and fall back to the
  60-second orphan session reconciler, while that reconciler matches a session "whose corresponding pod has
  `Terminated` phase in the `agent_pod_state` mirror" (`spec/10:59`) and `onHoldTimeout`
  (`pkg/adapter/holdstate.go:154-182`) leaves the adapter process and the pod alive, which CODE-3 states
  three paragraphs earlier. Every co-tenant session would have held quota in a non-terminal state behind a
  dead agent with no bounded recovery. CODE-3 now emits one `AdapterTerminating(session_id, reason:
  coordinator_lost)` per terminated member, with `EmitAdapterTerminating` taking the session identifier its
  loop already holds, which is the change CODE-1 already makes to `EmitFinalUsageReport` at the same call
  site and no change to the wire contract, since the envelope's `sessionId` member and the `spec/04:704`
  catalog row already carry the field. SPEC-9's bullet records the concurrent case rather than withdrawing
  the immediate transition, §9's limit is restated on what now holds, and §8's tier-1 and tier-7a hold cases
  assert one envelope per terminated session carrying that session's identifier.
- **The parallel `AdapterTerminating` statements in the §4.7 event catalog and the adapter-contract page.**
  `spec/04:704` and `docs/reference/adapter-contract.md:97` state that the message carries `session_id` and
  lets the gateway transition the session without waiting for the reconciler, and the staged `spec/10:58`
  rewrite would have contradicted both for a concurrent pod. Per-member emission resolves the divergence at
  its source rather than propagating a qualification to three sites: both rows stay true as written and
  neither takes an edit. SPEC-9 records that explicitly so the omission is a stated decision rather than a
  gap.
- **The gateway's outbound `message` envelope was staged as a key rename, which leaves it empty on an
  exclusive pool.** `pkg/gateway/session/executor/pod.go:93-107` fills the member from `bind.SlotID`, and
  CODE-2 deliberately keeps `BindResult.SlotID` empty on a `maxConcurrentSessions: 1` pool so the two
  presence-keyed release dispatchers keep reading it, so a rename alone would emit an envelope whose
  renamed member is the empty string on the pool D1 exists to normalize and whose `omitempty` tag drops the
  key outright. §8's tier-3 case asserting the key the gateway emits would fail, and the three exclusive-pod
  assertions §8 says invert would keep passing. The site moves out of §4.6.2(i)'s mechanical-rename list
  into CODE-6 as a value re-point onto the `sessionID` the executor is already sending on, with
  `BindResult.SlotID` untouched.
- **The only tier-1 pin of `persistWorkspaceRoot` was in no inventory and inverts silently.**
  `pkg/gateway/sessionserver/workspace_root_persist_test.go` appeared nowhere in the proposal. CODE-2
  re-means the function's fourth parameter without changing its type or arity
  (`start.go:3271`), so the file keeps building while all three cases fail on the retired
  `/workspace/current` literal they pass and expect. §8 gains a tier-1 block stating the re-base of each
  case, including the first-non-empty-wins property CODE-2 relies on, and §10 records the file beside the
  derivation it pins.
- **Three further credential-lifecycle tests drive the slotless credential path and read the pod-global
  file.** `tests/tier4_integration/credential_lifecycle_test.go`,
  `tests/tier4_integration/token_service_unavailability_guard_test.go`, and
  `tests/tier10_conformance/token_service_unavailability_guard_conformance_test.go` are in the same class as
  the two tier-8 files §8 already stages: each sets `CredentialsDir`, assigns and rotates or extends by
  `SessionId` alone, and reads the bundle back at `filepath.Join(dir, credfile.FileName)`, which CODE-1
  moves to `<roots.Credentials>/slots/<sessionId>` (`pkg/adapter/slotlayout/slotlayout.go:151-153`). The
  first is the only end-to-end pin of the §4.7 Full-level lifecycle against a real
  `credential_rotation`-capable runtime and the third is the tier-10 conformance pin of the §4.9 enforcement
  point. All three are added to §8's tier-8 disposition and to §10, with the note that none of the breaks is
  compiler-caught.
- **The tier-1 pin of `InProcessRuntime` was outside the `Close` blast radius and the new withholding arm
  had no case.** `pkg/adapter/embedded_test.go` calls `Close(ctx, sessionID)` at four sites and appeared in
  no inventory, so the package does not build until an unstated edit is made. Beyond the arity, SCHEMA-1
  gives `InProcessRuntime.Close` and `MCPRuntime.Close` a second outcome, returning without closing on a
  `last` false call, and every close case in §8 is written against `SocketRuntimeProcess` and its active
  set. The arm is reachable, because SCHEMA-1 counts registered-but-unbound entries toward occupancy. §10's
  `RuntimeProcess` bullet gains the file and §8 gains an eighth tier-1 close case driving both
  implementations on both values of `last`.
- **`spec/07:333` keeps the `MessageEnvelope` key SPEC-6 renames at every parallel example site.** The
  paragraph names `slotId` on the §15.4 unified envelope (`spec/15:1552`), whose property SCHEMA-2
  renames at `schemas/lenny-adapter-jsonl.schema.json:58`, and SPEC-1 staged only a decondition while SPEC-6
  staged only the ordinal, so §7.2 would have named a wire key the schema, §15.4, and §28.5.3 no longer
  declare. That is the ground `spec/06:77`, `spec/29:52`, and `spec/28:1767` are already staged on. SPEC-1's
  `spec/07:333` clause now states the rename under §4.6.2, with the declined-rename fallback.
- **The `spec/07:333` rename clause staged two of the line's three `slotId` spans.** The line carries the
  key a third time in its bold lead-in, `**Per-slot (`slotId`) routing when `maxConcurrentSessions > 1`:**`,
  which names the same `MessageEnvelope` routing key. SPEC-1 already edits that lead-in for the concurrency
  condition, but the clause scoped the rename to "both occurrences", so an implementor would have left the
  label naming `slotId` above two sentences reading `sessionId`, and §7.2 would have contradicted itself
  line-internally against a schema that declares no such property. The clause now states three occurrences,
  gives the lead-in's new text, and the declined-§4.6.2 fallback holds `slotId` at all three.
- **`staging_test.go` and `files_updated_test.go` were filed as compile-only field swaps while both assert
  on the materialized path the change relocates.** `FinalizeWorkspace` and `RunSetup` resolve their target
  from `s.WorkspaceRoot` (`pkg/adapter/staging.go:179`), which §4.2 and CODE-1 route through
  `slotlayout.Resolve` and CODE-2 retires, so after the swap both handlers materialize into
  `<base>/slots/{sessionId}/current` and every read-back at the swapped root's top level fails at run time.
  §8 and §10 now state both files as subject re-bases naming each affected case, including the two
  "requires a workspace root" cases whose refusal moves onto the merged path's resolution, and
  `tracing_internal_test.go` stays a field swap because it asserts only on spans.

## 9. Open questions, and what was considered and rejected

### Open

- **`RevokeCredentials`.** Whether the adapter's handler should gain a gateway caller or be removed. The
  Token Service path (`pkg/gateway/credentials/credassign/client.go:306`) may be the only intended revocation, in which
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
- SCHEMA-1's drain gate keeps a read-then-act window that the `Close` conjunction does not close. The
  handler decides the drain inside its `s.mu` critical section and sends the `terminate` frame after
  releasing the lock, so a session whose registry entry is created after the decision and before the send
  is served by a runtime that has already been told to exit within `deadlineMs`
  (`pkg/adapter/runtimeops.go:481-491`). The conjunction protects the connection object on that
  interleaving, and the connection stays open, but nothing keeps the runtime process alive once it honours
  the frame. Narrowing the window means taking the drain send inside the same lock hold as the decision, or
  re-reading occupancy immediately before the send, and neither is staged here. §8's tier-7a case therefore
  asserts the connection property and runs against a runtime stub that ignores `terminate`.
- The service-mode sense of "slot" is distinguished but not unified with the session-mode sense, and is not
  addressed by D2, D3, D4, D7, or D10.
- The runtime-facing contract changes whether or not §4.6.2 lands. A runtime built against the current
  contract, which is entitled to see no identifier on a single-session pod, must be updated. This proposal
  does not claim a runtime-compatible path.
- If §4.6.2 is deferred, §4.6.1 still lands and the proposal is coherent, but the JSONL leg then retains a
  field named for a pod-side resource the runtime does not hold, and the two legs disagree on how a session
  is spelled.
- The adapter manifest stays a single pod-global file. CODE-1 merges the manifest write into the one
  `StartSession` path, so every session receives the manifest a base-mode session receives today, and the
  file keeps its fixed runtime-facing path in `ManifestDir` (`pkg/adapter/server.go:113-116`;
  `WriteManifest` at `manifest.go:183`). Its `sessionId` and `mcpNonce` are single-valued
  (`manifest.go:105-159`), so a second concurrent `StartSession` on the same pod rewrites both and
  invalidates the nonce a co-tenant runtime authenticated with. Today the concurrent path writes no manifest
  at all, since `startSessionSlot` (`pkg/adapter/slotsession.go:27-52`) returns after `Runtime.Start` and
  the only writers are the whole-pod paths at `pkg/adapter/session.go:129`, `pkg/adapter/resume.go:106`, and
  `pkg/adapter/sdkwarm.go:229`, so Standard- and Full-level runtimes are unusable on a concurrent pod before
  and after this proposal. Making the manifest per-slot changes a path the SDKs, the scaffold templates, the
  documentation, and the compliance suite all read, and it is out of scope here.
- The whole intra-pod MCP surface is pod-global with the manifest: one platform socket
  (`pkg/controller/sandbox/podspec/podspec.go:184, :835` rendering `--mcp-socket=@lenny-platform-mcp`), one
  nonce, one `mcpCancel` and one `connectorCancels` slice (`pkg/adapter/server.go:126-131, :343-345`), and
  one `mcpHandshakeSeen` flag. CODE-1 therefore starts the servers at most once per pod, under one critical
  section, and cancels them only when a `ShutdownSlot` leaves the pod with no other bound session. The
  consequences on a concurrent pod are that the shared server stays armed with the nonce of whichever
  session started it, that a later session's manifest write invalidates that nonce for the runtime already
  authenticated with it, and that the socket stays reachable after a session ends. What the shared surface
  does not do is act as a session other than the caller: CODE-1 resolves the calling session from the slot
  registry at call time, refuses the call when the pod holds zero or more than one bound entry, and refuses
  it permanently once the pod has held more than one bound entry at any point in its life, because a
  per-session `Runtime.Close` leaves the ended session's agent code running inside the shared process
  (`pkg/adapter/socketruntime.go:440-445`) and a live-occupancy predicate alone would start dispatching that
  code's calls under the surviving co-tenant's principal. A tool call from a pod that ever multiplexed is
  therefore refused rather than dispatched under a neighbour's principal, and a Standard- or
  Full-level runtime remains unusable on a concurrent pod. A per-session MCP surface needs a per-session
  socket path and per-session manifest fields, which is the same out-of-scope change as the per-slot
  manifest above.
- The §4.7 Full-level rotation protocol is session-separable only in part, per CODE-4. The per-slot file
  rewrite and the `cred:{leaseId}` acknowledgement wait are per session; the in-flight gate
  (`pkg/adapter/credentials.go:178, :244-249`) reads a pod-global per-provider counter fed by session-less
  `llm_request_started` and `llm_request_completed` frames (`pkg/adapter/runtimeops.go:365-368, :404-408`),
  the 300s ceiling bounds that gate and is pod-scoped with it, and the `credentials_rotated` frame carries
  no session (`runtimeops.go:462-469`). On a pod holding more than one bound slot a co-tenant's outstanding
  request for the same provider therefore gates the rotating session, can drive it to the ceiling on its
  own, and the rotation completes while the co-tenant still holds requests on the old credential. Giving the
  protocol a session dimension is its own deliverable, stated in CODE-4 and not staged here; §8's tier-9
  block pins the coupling as the recorded behavior.
- The adapter manifest carries no static value a runtime could key on for concurrency behavior. It has no
  `maxConcurrentSessions` or slot-count field. If a later change gives the runtime an obligation needing
  one, that change must first stage a per-slot manifest write and resolve the collision recorded above.
  This proposal does neither and does not presuppose either.
- The adapter cannot enforce the pod-idleness refusal the pod-global claim enforced, per §4.11. Nothing
  renders `maxConcurrentSessions` into the adapter's configuration, so the ceiling stays with the gateway,
  which allocates the slot and counts occupancy (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`
  returning `SlotID` alongside `ActiveSlots`). The adapter keeps the refusal in the one form it can
  evaluate, a `StartSession`, `Resume`, or `ConfigureWorkspace` for a session whose slot is already bound.
  `Resume` claims through the same `claimSession` (`pkg/adapter/resume.go:47`), so a resume for a second
  session on a pod-warm pod is admitted on the same terms. `StartSession` and `Resume` lose the
  different-session arm with `claimSession` (`pkg/adapter/session.go:358-372`) on a pod-warm pod and keep
  it on an SDK-warm one, and `claimSessionForConfigure`'s different-session `Unavailable`
  (`pkg/adapter/sdkwarm.go:296-298`) stands on the same terms, because `spec/06_warm-pod-model.md:71` fixes
  the SDK-warm pod class at `maxConcurrentSessions: 1`, so a second session there has no runtime of its own
  and admitting it would re-point the incumbent's pre-connected process and invalidate its nonce, per §4.11.
  `AssignCredentials`'s sticky `credSessionID` refusal (`pkg/adapter/credentials.go:84-86`, F-6.1.12) is
  retired with them, per §4.11: routing every assignment through `assignCredentialsSlot` leaves the
  pod-global field unset and unread, and no per-slot refusal replaces it. Under D5 the registry key is the
  session identifier, so the wrong-session arm at `pkg/adapter/slotcreds.go:35-37` is unreachable and the
  merged handler's only preconditions are §4.2's non-empty `session_id` and `writeSlotCredentialFile`'s
  credentials-root check. A pod that survived termination and received an `AssignCredentials` for a
  different session now writes that session's own per-slot credential file rather than being refused, and
  the gateway-side teardown loop the comment at `pkg/adapter/session.go:376-380` names as the primary
  enforcement is the remaining defence. The same reading retires the wrong-session arms of the per-slot
  rotate and revoke handlers (`pkg/adapter/slotcreds.go:68-71` and `:120-123`), which become bound tests on
  a registered-but-unbound entry.
- The coordinator hold's `AdapterTerminating` notification keeps its session attribution on every pod, at
  the cost of one emitter-signature change. The envelope carries a `sessionId` that `emitControlEvent`
  stamps for every event arriving without one (`pkg/adapter/adapterevents.go:60, :149-152`, reached from
  `EmitAdapterTerminating` at `:216-217`), and CODE-1 re-points that stamp onto the registry's single bound
  entry, which resolves to nothing on a concurrent pod. CODE-3 therefore has `EmitAdapterTerminating` take
  the session identifier its per-member loop already holds, the way CODE-1 does for `EmitFinalUsageReport`,
  and emits one notification per terminated session so the `spec/10:58` immediate transition runs for each.
  Leaving the field unset was rejected because the 60-second orphan session reconciler that bullet names as
  the backstop is predicated on a terminated pod (`spec/10:59`) and a hold timeout leaves the pod alive, so
  every co-tenant session would hold quota in a non-terminal state behind a dead agent with no bounded
  recovery. The wire contract is unchanged: the field already exists and `spec/04:704` already states it.
  SPEC-9 stages the `spec/10:58` sentence recording the concurrent case, and §8's tier-7a case pins the
  per-member arity and attribution.
- Direct-mode token counts and unstamped control events are attributed only on a pod holding exactly one
  bound session. The `llm_request_completed` frame carries no session identifier
  (`pkg/adapter/usage.go:200-206`) and one runtime process serves every slot, so once CODE-1 retires
  `Server.sessionID` the fold and `emitControlEvent`'s stamp resolve from the registry's single bound
  entry and give up on a pod holding more than one. The fold is dropped and the RATE_LIMITED, AUTH_EXPIRED,
  and PROVIDER_UNAVAILABLE envelopes carry an empty `sessionId` there, so §11.2 direct-mode accounting and
  the per-session attribution of those three events are absent on a concurrent pod. Carrying the session on
  the frame is a `CH-RUNTIMEOPS` contract change, and it is not staged here.
- The Python and TypeScript runtime SDKs model no `status` frame, so neither carries the `sessionId`
  property SCHEMA-2 adds to it. A runtime written on either SDK emits `status` frames by hand or not at all,
  and this proposal adds no frame type to those SDKs. The Go SDK's `outboundStatus`
  (`sdks/runtime/go/runtime/types.go:323-328`) gains the member and stays without a builder.
- A runtime that reads a credential file now reads the §4.7 adapter manifest to find it, at every
  integration level, per SPEC-4 and CODE-4. `spec/04:796` states today that a Basic-level runtime does not
  need the manifest for core operation, and the restated bullet carves out the credential path. A runtime
  that reads neither the manifest nor a caller-supplied option finds no credential file, where today it
  finds the pod-global one on a pool with `maxConcurrentSessions: 1`.
- The unbounded-overtake window in the operation lock is recorded in §3 as a separate finding against the
  `spec/05:542` and `spec/04:691` rules, out of scope here.

## 10. Files touched on application

- `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`,
  `schemas/workspaceplan-v1.json`, `schemas/runtime-ops-events.schema.json`,
  `schemas/examples/jsonl.set_tracing_context.json`, `schemas/examples/jsonl.tool_call.json`,
  `schemas/examples/runtime-ops.credentials_rotated.json`, and the regenerated `pkg/proto/adapter/v1/`.
- `spec/04`, `spec/05`, `spec/06`, `spec/07`, `spec/08`, `spec/10`, `spec/11`, `spec/12`, `spec/13`,
  `spec/14`, `spec/15`, `spec/16`, `spec/17`, `spec/18`, `spec/24`, `spec/26`, `spec/28`, and `spec/29`, plus
  `spec/README.md`,
  whose table-of-contents entry at `:290` carries the §29.10 heading text and fragment SPEC-5 retitles.
  `spec/10` takes SPEC-9's `AdapterTerminating` sentence at `:58`, the specification half of CODE-3, beside
  SPEC-9's §10.1 scoping-key edits.
  `spec/12` §12.5 takes
  SPEC-9's re-key of the retention and supersede rules; SPEC-8 records its one slot-assignment site
  (`spec/12:191-192`) as already correct and untouched. `spec/16` takes SPEC-9's supersede-condition
  restatement at `:198`, SPEC-9's new §16.1 row for the counter CODE-6 registers, and SPEC-9's restatement
  of the `:188` row. `spec/11` takes SPEC-7's rename of the §11.4 full-revoke propagation step and note
  (`:263`, `:270`) under the base case in §4.5(d), and `spec/07` takes the same rename at the §7.1 lifecycle
  step (`:49`) beside its other edits. `spec/26` takes SPEC-3's shared-assets correction at `:44` as well as
  the `/workspace/current` sweep. `spec/06` takes SPEC-5's lift of the §6.2 per-slot sub-states
(`:149-151` and `:153`) out of the concurrent-occupancy block, beside its §6.1 and §6.4 edits; the
`failed` and `leaked` edges at `:152` and `:154` stay in that block, so the **`leaked` slot semantics**
paragraph at `:159` and the `spec/05` threshold it rests on take no edit. `spec/18` takes SPEC-3's restatement of the Phase 12c deliverable at
  `:532`, which keeps the Redis slot-counter capacity gate and the `acknowledgeProcessLevelIsolation`
  requirement in that phase and moves the uniform per-slot workspace layout into the Phase 4
  workspace-plan deliverable at `:232-235`, the first deliverable that materializes a session workspace.
  `spec/28` takes SPEC-1's restatement of the intra-pod MCP scoping sentences beside its other edits: the
  `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` Exclusivity bullets (`:1153-1156`, `:1198-1200`), the MCP clause
  of the §28.6 restatement (`:1683-1686`), and the Exclusivity column of the two §28.8 failure-mode rows
  (`:1769`, `:1770`).
- `docs/getting-started/`, all of `docs/runtime-author-guide/` (including `lifecycle.md`,
  `platform-tools.md`, `integration-levels.md`, `echo-runtime.md`, and `sdk-examples/`),
  `docs/reference/adapter-contract.md`, `docs/reference/glossary.md`, `docs/reference/metrics.md`,
  `docs/reference/state-machines.md`, which takes SPEC-5's split of the per-slot sub-state machine at
  `:228-240` as well as the `/workspace/current` sweep at `:87`, `docs/api/rest.md`,
  `docs/tutorials/wrap-coding-agent-cli.md`,
  `docs/about/style-guide.md`, `docs/operator-guide/configuration.md`,
  `docs/operator-guide/security.md`, and
  `docs/runbooks/ephemeral-container-cred-guard-unavailable.md`.
- `pkg/gateway/externalapi/openapi/openapi.json`, the served §15.1 document, and
  `cmd/lenny-ctl/runtimescaffold/templates/` (`go-coding.main.go.tmpl`, `python-coding.main.py.tmpl`,
  `typescript-coding.main.ts.tmpl`, and `runtime-coding.yaml.tmpl`), both of which name the retired
  workspace path.
- `charts/lenny/templates/admission-policies/ephemeral-container-cred-guard-webhook.yaml`, whose comment
  names the retired pod-global credential path.
- `pkg/adapter/` (`slot.go`, `slotframe.go`, `slotsession.go`, `slotcreds.go`, `slotlayout/`, `resume.go`,
  `exportpaths.go`, `holdstate.go`, `session.go`, `sdkwarm.go`, `checkpoint.go`, `oplock.go`, `staging.go`,
  `credentials.go`, `credexpiry.go` for the pod-global lease-expiry path that goes dead with
  `Server.credLeases`, `attach.go`, `tracingcontext.go`, `lifecycle.go`, `coordination.go`, `usage.go`
  and `adapterevents.go` for the two readers of `s.currentSession()` CODE-1 re-points onto the pod's
  single bound session,
  `server.go`, `socketruntime.go`, and `mcpruntime.go`, `embedded.go`, and `embedded_sdkwarm.go` for the
  `RuntimeProcess.Close` teardown-decision parameter SCHEMA-1 adds to the interface in `session.go`,
  with `socketruntime.go` also dropping the listener close from `SocketRuntimeProcess.Close` so a teardown
  leaves the pod able to serve another session,
  `sessionscrubreporter.go`, `metrics.go` for CODE-6's rejection counter,
  `podscrub.go` and `scrub/` for the per-slot purge CODE-1 stages, `manifest.go` for the `credentialsPath`
  row CODE-4 adds, `platformmcp.go`, `platformtoolprovider.go`, and `connectormcp.go` for the once-per-pod
  start CODE-1 takes under one critical section and the call-time session resolution its providers gain,
  with the guard applied at all three start sites, `session.go:150, :158`, `resume.go:119, :124`, and
  `sdkwarm.go:240, :245`,
  and `gatewaycontrol/scrubreport.go`).
- The three existing pins of the addressing rule §4.6.1 and §4.8 rewrite, all hand-rewrites §8 states:
  `pkg/adapter/tracingcontext_addressing_test.go`, whose distinct session and slot fixtures collapse onto
  one identifier and whose single-session tagged case and unreadable-identifier subtest invert onto the
  resolve-on-one-slot branch, `pkg/adapter/tracingcontext_sampling_test.go`, the atomicity pin whose
  slotless-stream premise and `Server.sessionID` writes go with the deleted branch and whose subject moves
  onto the two-term read §4.6.1 introduces, and
  `tests/tier3_contract/adapter_jsonl/set_tracing_context_test.go`, whose
  two cases move onto the `sessionId` property SCHEMA-2 declares.
- `pkg/adapter/attach_test.go`, the tier-1 pin of the demultiplexer CODE-6 renames, a hand-rewrite §8
  states: its `frameSlotIDForTest` decoder reads the wire key by struct tag, its demux case feeds frames
  spelled `slotId`, its no-identifier case pins absence-as-pod-scope, and its fixtures pair a session with a
  distinct slot.
- `pkg/adapter/scrub/scrub_test.go`, the scrub package's own tier-1 suite, a hand-rewrite §8 states: its
  eleven `Config` field sites move onto the plural members, its two step-6 verify cases and its
  purge-before-cleanup case are re-based over the per-slot sets, and `TestCleanupEnv_spec_5_2_424` moves
  onto the singular `CleanupDir`.
- `pkg/adapter/podscrub_test.go` and `pkg/adapter/sessionscrub_emit_test.go` as hand-rewrites §8 states,
  beyond the field swap listed below: the first owns five recycle cases that the occupancy refusal inverts
  plus the empty-`session_id` and terminate-path cases the split retires, and the second owns the §5.2
  `ReportSessionScrub` emission cases the teardown merge inverts.
- `pkg/adapter/usage_test.go` (`:157, 197, 235, 268`) and `pkg/adapter/adapterevents_test.go` (`:96, 187`)
  as hand-rewrites §8 states: each binds its session by writing the retired `Server.sessionID`, so both
  break the `pkg/adapter` build, and each is re-based onto a bound registry slot with the fold and the
  stamp resolved from the pod's single bound session.
- `pkg/gateway/runtime/adapterclient/client_test.go` as a hand-rewrite §8 states, since its
  `recordingAdapter` serves the deleted `Shutdown` RPC and its four `Terminate` cases are the only pin of
  the `reason` and `deadline_ms` population the collapsed `ShutdownSlot` method carries.
- `cmd/lenny-compliance/full.go`, whose credential-path default takes CODE-4's manifest resolution, and
  `cmd/lenny-compliance/main.go` (`:388`, `:625-627`) and `standard.go:488`, whose inbound `message`
  literals gain `sessionId` so the Basic-level echo check CODE-6 adds has an identifier to echo, and
  `basicCases()` (`cmd/lenny-compliance/main.go:220`) gains the check itself.
- `pkg/gateway/podlifecycle/podsession/binder.go`, `slotbinder.go`, and `slotfailure.go`, together with
  `pkg/gateway/podlifecycle/podclaim/` (`claimer.go` and `slotclaimer.go`), for
  the slot the resume reserves on a concurrent pool and the concurrency the resume `BindResult` reports on
  every pool, with
  `pkg/gateway/sessionserver/start.go` populating it from the resolved `PoolMatch`. `slotclaimer.go` gains
  the pod-targeted `ReserveSlotOnPod` entry point CODE-2 states, factored onto the existing `reserveSlot`
  (`:597`) so the resume reserves on the pod `connect` already claimed rather than running `ClaimSlot`'s pool
  scan, and `binder.go`'s `ResumeRequest` gains the `MaxConcurrentSessions` field that entry point requires.
- `cmd/lenny-gateway/user_revocation.go`, whose §11.4 full-revoke send (`:129`) calls the `Terminate`
  method CODE-1 collapses into `ShutdownSlot`.
- `pkg/gateway/runtime/adapterclient/client.go`, `pkg/gateway/session/executor/pod.go` and
  `subprocess.go`, `pkg/gateway/checkpoint/` (`partialmanifeststore/`, `checkpointretention/`,
  `checkpointer/`), `pkg/gateway/sessionserver/`
  (`derive.go`, `messages.go`, `toolapproval.go`, `start.go`, and `finalize.go` for the second
  `persistWorkspaceRoot` call site CODE-2 re-points), and
  `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`.
- The test files CODE-5's store-API change breaks, per §8:
  `pkg/gateway/checkpoint/checkpointretention/checkpointretention_test.go` and
  `pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore_test.go` (two cases) and
  `pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go` and
  `tests/tier2_component/stores/partialmanifeststore_test.go` as hand-rewrites, since each
  owns at least one case whose subject is the per-slot scoping the deliverable deletes; the tier-2 file
  loses its slot-scoped selector subtest and gains the Postgres pin of the session-keyed selector §8
  states, and
  `pkg/gateway/checkpoint/checkpointer/checkpointer_test.go`,
  `pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go`,
  `tests/tier4_integration/checkpoint_chunk_helpers_test.go`,
  `tests/tier4_integration/checkpoint_intent_generation_test.go`, and
  `tests/tier2_component/legalholdreconciler/reconciler_test.go` as field and argument drops.
- `tests/tier4_integration/checkpoint_concurrent_pool_test.go`, the hand-rewrite §8 states under §4.5(c)
  and CODE-5: it is the sole caller of the `receivedSlotID()` harness helper the proposal already stages,
  it reads the `partialmanifeststore.Record.SlotID` member CODE-5 deletes, and its two sessions carry
  distinct slot identifiers.
- `sdks/client/go/`, `sdks/client/python/`, and `sdks/client/typescript/`.
- `sdks/runtime/go/`, `sdks/runtime/python/`, and `sdks/runtime/typescript/`.
- `pkg/adapter/warmlayout.go`, for the warm-time layout D8 changes and the shared-assets directory the
  corrected §6.4 paragraph describes, with `pkg/adapter/warmlayout_test.go` as the hand-rewrite §8 states,
  since its three `current`-leaf cases pin the §6.1 invariant D9 retires and its constructions set the
  `Server.WorkspaceRoot` field CODE-2 deletes.
- The remaining `pkg/adapter` test files that set `Server.WorkspaceRoot`, by literal or by assignment, per
  §8: `exportpaths_test.go` as a subject rewrite onto the slot root, `manifest_test.go`,
  `manifest_fields_test.go`, which beyond the literal swap sets `srv.credLeases` directly at `:99` and
  `:120` and re-bases those two cases onto the slot's lease set CODE-1 derives the manifest `llm` object
  from, and `podscrub_test.go:468` as literal-and-field swaps off `/workspace/current`,
  and `staging_test.go` and `files_updated_test.go` as subject re-bases onto the slot root, since their
  materialization, mid-session-overlay, and setup-command assertions read the path the handlers move off,
  and `tracing_internal_test.go`, `mcpruntime_test.go`,
  `embedded_sdkwarm_test.go`, `shutdown_demote_test.go`, `tracing_external_test.go`, `sdkwarm_test.go`,
  `checkpoint_stream_test.go`, and the rest of `podscrub_test.go` as field swaps, together with
  `session_test.go`, whose `sessionServer` helper (`:215-222`) supplies the root a dozen files in the
  package inherit and is rewritten to return a bound slot's root, and `resume_test.go`, which takes its root
  from that helper and whose §7.3 step (d) cases are restated against a slot root. `session_test.go`,
  `resume_test.go`, and `slot_test.go` are hand-rewrites beyond those swaps, per §8: the two
  pod-idleness cases (`session_test.go:244`, `resume_test.go:283`), the two unknown-session refusals
  re-based from `NotFound` onto `FailedPrecondition` (`session_test.go:289`, `:395`), and `slot_test.go`'s
  absence-as-pod-scope case (`:340`) and cross-session `SendMessage` arm (`:265-274`).
- The `RuntimeProcess` test doubles and the direct `Close` callers, which move with the teardown-decision
  parameter SCHEMA-1 adds: `pkg/adapter/session_test.go`'s `fakeRuntime` (`:137`),
  `pkg/adapter/holdstate_test.go`'s `holdRuntime` (`:36`), `pkg/adapter/podscrub_test.go`'s
  `recycleRuntime` (`:96`), `tests/tier10_conformance/recycle_scrub_conformance_test.go`'s
  `scrubConformanceRuntime` (`:71`),
  `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go`'s `ssRespondingRuntime` (`:93`),
  and the literal call sites in `pkg/adapter/mcpruntime_test.go`, `pkg/adapter/socketruntime_test.go`,
  `pkg/adapter/socketruntime_e2e_test.go`, and `pkg/adapter/embedded_test.go`, which is the tier-1 pin of
  `InProcessRuntime` and calls `rt.Close(context.Background(), "s1")` at `:37`, `:67`, `:87`, and `:111`, so
  the package does not build until it takes the parameter, and whose
  `TestInProcessRuntimeCloseEndsTheLoop` (`:56`) is the case §8's eighth tier-1 close case extends with the
  withholding arm. `pkg/adapter/socketruntime_test.go`'s two-session cases
  (`:258-294`) additionally assert the sibling-active early return the parameter replaces and are
  re-based onto it.
- The `Server.WorkspaceRoot` holders outside `pkg/adapter`, per §8: `tests/testinfra/coordfixture/coordfixture.go`
  first, since other tiers dial their adapter through its `StartPod`; then
  `pkg/gateway/runtime/adapterclient/client_test.go` and `checkpointbarrier_test.go`,
  `pkg/gateway/session/executor/pod_test.go`, `pkg/gateway/coordination/barrier/wiring_test.go`,
  `pkg/gateway/podlifecycle/podsession/binder_archive_test.go`, `binder_phases_test.go`,
  `binder_readopt_test.go`, `binder_test.go`, `sdkwarm_bind_test.go`, and that package's
  `one_session_only_test.go`,
  `pkg/gateway/sessionserver/start_pod_test.go`, `create_test.go`, `pool_selection_component_test.go`,
  `pool_exhaustion_queue_test.go`, `delegated_child_materialize_test.go`,
  `resume_external_effect_regression_test.go`, `start_pod_lease_component_test.go`,
  `upload_to_session_test.go`, and `recycle_scrub_fold_component_test.go`,
  `tests/tier3_contract/adapter_usage_wired/wired_reportusage_test.go`,
  `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go`,
  `tests/tier4_integration/recycle_scrub_path_test.go`, `credential_delivery_gate_test.go`,
  `checkpoint_grant_remint_test.go`, `cross_environment_delegation_test.go`,
  `delegation_child_materialization_test.go`, `mcp_runtime_lifecycle_test.go`,
  `eager_claim_lifecycle_test.go`, `concurrent_delegation_proxy_test.go`, and `concurrent_workspace_test.go`,
  `tests/tier7a_load_local/tracing_context_release_race_test.go`,
  `tests/tier9_security/adapter_mcp_nonce_test.go`, `credential_delivery_gate_test.go`,
  `delegation_credential_deny_leakage_test.go`, and `delegation_child_materialization_cred_test.go`,
  `tests/tier10_conformance/recycle_scrub_conformance_test.go`,
  `tests/tier2_component/translators/openai_singleshot_lifecycle_test.go`, and
  `cmd/lenny-gateway/direct_usage_quota_integration_test.go`.
- `pkg/controller/sandbox/podspec/podspec.go`, whose sidecar-adapter and embedded-adapter argv render
  `--workspace-root=/workspace/current` (`:568`, `:669`) and whose two `// spec: §6.4` comments
  (`:562-567`, `:663-668`) defer the per-slot tree that D7 makes universal.
- `pkg/gateway/sessionserver/lifecycle.go` and `pkg/upload/archive/archive.go`, for the two
  `/workspace/current` default constants CODE-2 deletes, with the three §13.4 containment sites that
  consumed the second (`pkg/gateway/podlifecycle/podsession/slotbinder.go:269-271` and
  `binder.go:892-895, :1342-1343`) computing a slot root instead.
- `pkg/adapter/server.go`'s `NegotiateVersion` handler, which reports the workspace base in place of the
  retired pod-global root, with the `negotiated`, `BindResult`, and `PrepareResult` member renamed
  `WorkspaceBase` and propagated in `pkg/gateway/podlifecycle/podsession/binder.go` and at the
  `PrepareResult`-to-`BindResult` copy in `pkg/gateway/sessionserver/start.go:2608`, and the two derivation
  sites CODE-2 stages: `persistWorkspaceRoot` in `pkg/gateway/sessionserver/start.go` (`:3271-3284`), which
  derives the session's root from the base its two call sites now pass (`start.go:2854` on the launch path
  and `pkg/gateway/sessionserver/finalize.go:363` on the finalize path), and
  `pkg/gateway/podlifecycle/podsession/binder.go:989` for the `cwd` the `ConfigureWorkspace` call carries to
  a pre-connected SDK. `pkg/gateway/sessionserver/workspace_root_persist_test.go`, the only tier-1 pin of
  `persistWorkspaceRoot`, is the hand-rewrite §8 states for that derivation: its three cases hard-code
  `/workspace/current` as both argument and expectation, and the parameter's type and arity are unchanged,
  so nothing surfaces the break at build time. The `ExpectedWorkspaceRoot` replay at `start.go:3917` is not a derivation site and
  takes no edit: it replays the persisted column verbatim, which is what keeps the §7.3 step (d) comparison
  non-vacuous. `pkg/gateway/podlifecycle/podsession/slotbinder.go` takes the concurrent half of the same
  chain as propagation rather than derivation, capturing the reported base at both handshake sites (`:239`,
  `:461`) and carrying it on the `BindResult` `materializeSlot` returns (`:319-334`).
- `cmd/runtimes/echo-concurrent/` (`main.go`, `dispatch.go`, `slot.go`, and `main_test.go`), and
  `cmd/runtimes/echo-embedded` and `cmd/runtimes/preconnect-echo`, which are the two embedded-model
  binaries that parse the argv `podspec.go:678-683` renders onto the runtime container: each replaces its
  `--workspace-root` flag with `--workspace-base` (`echo-embedded/main.go:77` and
  `preconnect-echo/main.go:56`), sets `adapterSrv.WorkspaceBase` in place of the retired
  `adapterSrv.WorkspaceRoot` (`echo-embedded/main.go:111` and `preconnect-echo/main.go:88`), and updates the
  argv its test passes (`echo-embedded/main_test.go:34` and `preconnect-echo/main_test.go:34`).
  `cmd/runtimes/echo` takes
  no workspace-path edit, because it declares no flags and names no workspace path.
- `pkg/runtimekit/echocore/echocore.go` (`:110-114` and `:176-179`), `cmd/runtimes/streaming-echo/main.go`
  (`:396-400` and `:466-469`), and `cmd/runtimes/delegation-echo/main.go` (`:235-239` and `:608-612`),
  which CODE-6 stages for the Basic-level echo obligation. `cmd/runtimes/echo` and
  `cmd/runtimes/echo-embedded` inherit that edit through `echocore` and take no change of their own.
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
- The tier-0, 1, 2, 3, 4, 5, 7a, 8, 9, 10, and 11 cases in §8 and their `tests/spec-map.json` entries,
  including the hand-rewrites §8 stages by name: `tests/tier5_e2e_kind/checkpoint_resume_test.go`, whose
  restore assertion reads the retired `/workspace/current`;
  `tests/tier11_docs/recycle_scrub_trigger_consistency_test.go`, whose anchors are the two `Terminate`
  spellings SPEC-7 renames; `tests/tier11_docs/redis_key_prefix_registry_test.go`, whose §11.4 anchor is a
  third; `tests/tier11_docs/checkpoint_pipeline_consistency_test.go`, whose migration-0178 column gate
  requires §10.1 to name the `slot_id` column SPEC-9 removes from it; the four further `tests/` files
  carrying the `/workspace/current` literal
  (`tests/tier4_integration/eager_claim_lifecycle_test.go`,
  `tests/tier5_e2e_kind/eager_claim_e2e_test.go`, `tests/testinfra/sessiondriver/sessiondriver.go`, and
  `tests/tier7a_load_local/scenarios/oversized_request_rejection_recovery/scenario.go`); and
  `pkg/adapter/one_session_only_test.go` and `pkg/adapter/sdkwarm_test.go`, whose cases drive the retired
  pod-global claim and release functions; `pkg/adapter/credentials_test.go`, whose assignment, rotation, and
  revocation cases drive the retired pod-global credential path and its `credSessionID` refusals;
  `pkg/adapter/credexpiry_test.go`, whose base-mode §4.9 lease-expiry cases read the pod-global credential
  file and the pod-global timer map and re-base onto the per-slot file and the slot's own timer set; and
  `pkg/gateway/podlifecycle/podsession/binder_test.go`, whose shutdown fake and two single-request
  assertions are written against the deleted `ShutdownRequest`;
  `pkg/adapter/drain_test.go`, whose drain case claims the pod through the retired `claimSession` and
  drives the deleted `ShutdownRequest`, and whose premise the occupancy gate qualifies;
  `pkg/gateway/sessionserver/recycle_scrub_fold_component_test.go`, whose fake serves the deleted
  `Shutdown` RPC and whose fold assertions move onto `ShutdownPod`;
  `pkg/adapter/platformmcp_test.go`, `pkg/adapter/connectormcp_test.go`,
  `tests/tier4_integration/mcp_runtime_lifecycle_test.go`, and
  `tests/tier9_security/adapter_mcp_nonce_test.go` as `ShutdownSlot` call-site swaps;
  `pkg/adapter/gatewaycontrol/scrubreport_test.go`, whose released case pairs a session with a distinct slot
  identifier and whose leaked case pins the base-mode empty-slot emission D1 retires;
  `pkg/gateway/sessionserver/messages_component_test.go`, whose fail-closed case is written against the
  concurrency-conditioned gate CODE-1 re-keys; and
  `tests/tier8_chaos/credential_rotation_ceiling_test.go` and
  `tests/tier8_chaos/token_service_unavailability_guard_test.go`, whose fixtures drive the credential
  handlers for a session holding no slot, together with the three files in the same class §8 names beside
  them: `tests/tier4_integration/credential_lifecycle_test.go`,
  `tests/tier4_integration/token_service_unavailability_guard_test.go`, and
  `tests/tier10_conformance/token_service_unavailability_guard_conformance_test.go`, each of which reads the
  credential bundle at the pod-global path the per-slot resolution replaces.
- `tests/tier3_contract/sdks/runtime_sdk_test.go`, which gains the per-SDK frame-key case §8 states;
  `tests/tier11_docs/successor_pointer_test.go`, whose hand-maintained `reducedSections` domain gains the
  `spec/29` §29.10 row SPEC-5 states; and `tests/tier7a_load_local/`, which gains the two concurrent-teardown
  cases §8 states for SCHEMA-1's drain gate.
- **SPEC-1's scope statement for `spec/28:604`.** That line carries three separable elements: the
  Basic-level permission, the presence condition, and the key spelling `slotId`. Adding the key spelling to
  SPEC-1's rename list left three sites still saying SPEC-1 takes "only the presence half" of the line, one
  of them twenty lines from the new rename text. §4.6.1's integration-level paragraph, SPEC-1's closing
  paragraph, and this pass record now all state that SPEC-1 takes the presence condition and, under §4.6.2,
  the key spelling, while SPEC-7 takes the Basic-level permission.
- **The tier-11 hand-rewrite ordinal.** `tests/tier11_docs/checkpoint_pipeline_consistency_test.go` was
  numbered "a third hand-rewrite" with only one hand-rewrite before it in the block, which also contradicted
  the base-case pair numbered first and second below it. It is now "a second tier-11 hand-rewrite", leaving
  `recycle_scrub_trigger_consistency_test.go` and `redis_key_prefix_registry_test.go` to carry their own
  numbering under the base case in §4.5(d).
