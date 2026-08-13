# Proposal: Settle §15.4.1 and remove the channel contract it duplicates

- **Status:** **Superseded (2026-08-13) by proposal 0069.** Approved (2026-08-13) by jaf sign-off and
  never applied: the tree was reverted to the sign-off commit before implementation completed. Its
  specification move is carried forward unchanged by 0069; its frame-addressing design is replaced.
  0068 taught the adapter its pod's concurrency through a new CRD field, a poolstore mirror, and a
  launch argument, on the premise that the adapter could not otherwise know it. The adapter holds a
  more precise fact already: an Attach stream is bound to one `(session, slot)` address, and the
  stream's own slot id carries what an untagged frame means on that pod. Verified (2026-08-12),
  converged after 10
  adversarial review rounds (23 findings fixed) across three full-pool sweeps, the certifying sweep
  running every lens complete with zero confirmed findings.
- **Date:** 2026-08-12.
- **Scope:** Settles the contradiction proposal 0064 leaves over the fate of `spec/15` §15.4.1, keeps the
  subsection, removes the `CH-MSGSOCK` contract it duplicates from §28.5.3, completes the one outbound
  message type §28.5.3 never schema-defines, and reconciles the labels 0064's retirement premise produced.
  It also amends proposal 0064.

This document stages the proposed specification changes. It does not modify any spec, code, or doc file.
Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Proposal 0064 reduced §4.7 and §15.4, moving the intra-pod channel contracts to §28.5. The content
disposition landed correctly. What did not settle is the fate of the `#### 15.4.1 Adapter↔Binary
Protocol` heading, and 0064 says two things about it that cannot both be true.

## 1. Problem

### 1.1 The contradiction in 0064

0064 states that §15.4.1 retires. It calls it "the retired §15.4.1" throughout, says a label left at
§15.4.1 "would name a subsection that exists in no `spec/` file after the reduction", and on that ground
specifies fourteen hand-corrections that retarget links away from the `1541-adapterbinary-protocol`
anchor and rewrite their labels from §15.4.1 to §15.4.

It also states that content stays there. The link 0064's reduction kept at `spec/15` cites §15.4.1 "for
the stdin/stdout JSON Lines framing and the stdout flushing requirement, which stays here". A heading
that holds content is not retired.

The tree carries the consequence of both readings. The heading survives, while 0064 applied its fourteen
hand-corrections on the premise that it would not.

### 1.2 What §15.4.1 holds, and what duplicates §28.5.3

0064 describes §15.4.1 as a block of four unnumbered `####` subsections. Two carried the adapter-to-binary
wire contract and moved to §28.5.3. Two state client-facing external-protocol contracts and were carved
out, because §28.5 groups its cards over the closed boundary set §28.2 fixes and none of them names the
external-client-to-gateway edge.

What 0064 does not classify is the block's own preamble, which stayed. Read against §28.5.3, it separates
cleanly:

| Statement in the §15.4 block | Stated in §28.5.3 | Kind |
|:--|:--|:--|
| stdin/stdout JSON Lines framing | yes | channel contract |
| inbound `message`, `tool_result`, `heartbeat`, `shutdown` table | yes, as full schemas | channel contract |
| outbound `response`, `tool_call`, `heartbeat_ack`, `status` table | yes, as full schemas | channel contract |
| outbound `set_tracing_context` row | enumerated, never schema-defined | channel contract |
| `slotId` concurrent-session multiplexing | yes | channel contract |
| `prompt` removal note | no | historical note |
| `input_required` removal note | no | historical note |
| stderr is not parsed as protocol messages | no | binary I/O obligation |
| stdout flushing requirement and its per-language table | rule only, no guidance | binary I/O obligation |
| Translation Fidelity Matrix (carve-out heading) | no | client-facing contract |
| `MessageEnvelope` format (carve-out heading) | no | client-facing contract |

The last two rows are the carve-out headings. `#### Translation Fidelity Matrix`
(`spec/15_external-api-surface.md:1520`) and `#### `MessageEnvelope` — Unified Message Format` (:1575) are
level-4 headings, the same depth as `#### 15.4.1` (:1471), so they are siblings that terminate §15.4.1's
body rather than content inside it. The numbered section that encloses them is §15.4. The heading index the
anchor pass builds derives the same answer, popping open headings at `Level >= h.Level` before it records
the enclosing section (`scripts/specshift/anchor/heading.go:62-67, 168-176, 238-244`), and the tier-11 gate
defines a section body as running to the next heading at the same depth or shallower
(`tests/tier11_docs/successor_pointer_test.go:62-63`). The rows above the two carve-out rows are the body
of §15.4.1 itself.

Everything restating the `CH-MSGSOCK` contract is duplicated. Everything unique is either a binary I/O
obligation or a client-facing contract, and §28.5's boundary set can own neither.

The duplication is the defect 0064's reduction existed to remove, and it is live: §28.5.3 says it "owns
the schema of each `CH-MSGSOCK` message" while §15.4.1 tabulates the same messages.

### 1.3 The circular pointer

§28.5.3's Messages axis enumerates the message types and cites §15.4 as their source, while the same card
schema-defines them further down. A reader following the pointer arrives at the section that points back.

## 2. Decisions

1. **§15.4.1 keeps its number and position.** Retiring it would leave §15.4 followed by two unnumbered
   `####` blocks and then §15.4.2, and 0064 keeps §15.4.2's number and anchor. The hole is worse for a
   reader than the wrapper is.

2. **The duplicated channel contract is removed**, and only where §28.5.3 states the same thing. A statement
   with no destination stays, per 0064's both-legs rule.

3. **`set_tracing_context` gains its schema block in §28.5.3** rather than being deleted from §15.4.1.
   It is the one outbound type the card enumerates and never defines, so deleting its row would drop
   normative prose with no destination. Completing the card is the destination, and SPEC-2 states the
   mechanism as the gateway implements it rather than copying the row's adapter-side wording.

4. **The frame's addressing resolves a name rather than branching on a mode.** The adapter registers the
   identifiers against the session a frame names, and discards a frame that names none. The set of names
   follows the pod's declared concurrency. On a pod whose pool sets `sessionPolicy.maxConcurrentSessions`
   above 1, a frame names a session by carrying that session's `slotId`, so a frame carrying none and a
   frame carrying a `slotId` the pod does not hold both name nothing. On a pod whose pool sets it to 1 the
   pod has a single addressee and no session there holds a `slotId` at all
   (`pkg/gateway/sessionserver/start.go:2111-2124`, `pkg/adapter/slot.go:37-45`), so every frame names that
   session whether or not it carries the field. Branching on the pod's mode instead needs a case per
   combination, and an earlier implementation attempt found two it got wrong: a defensive `slotId` on a
   single-session pod was dropped, and a `slotId` the pod did not hold registered against the wrong
   session.

5. **The pod is told its concurrency at construction, and infers nothing.** `maxConcurrentSessions` is a
   property of the pod for its whole life, so it is declared to the adapter as a launch argument rather
   than inferred at frame time. The value is not on the surface the podspec builder holds today: §5.2
   declares `sessionPolicy.maxConcurrentSessions` on the pool configuration and §4.6.3 gives every
   `SandboxTemplate.spec.*` write to the PoolScalingController
   (`spec/04_system-components.md:602`), but the CRD Go type declares `recycle` alone
   (`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:28-34`) and the rendered manifest exposes `recycle`
   alone (`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:186-222`), so the field the spec already
   states is missing from the implementation. SPEC-2 stages that field and the poolstore mirror that fills
   it, and states the propagation path end to end. The same attempt inferred the value from observed claim
   order and could not make that hold: the inference is order-dependent, the terminal-state repair broke
   the per-slot claim path, and declaring the value per claim widens the §4.7 claim contract with a
   pod-lifetime fact. The declared value separates a pool at the default of 1 from a value that did not
   propagate. A pod whose resolved template declares no value carries 1, which is the §5.2 default
   (`spec/05_runtime-registry-and-pool-model.md:393-397`), and a pod whose template does not resolve
   carries the undeclared sentinel. Collapsing the two onto one rendered value would put every default
   pool on the fail-closed branch and disable the frame across the default deployment.

6. **§15.4.1 is retitled.** Once the adapter-to-binary contract is gone, "Adapter↔Binary Protocol" names
   content the section no longer holds, while §28.5.3 owns it. A reader looking for that protocol would
   land on a section named for it that does not contain it.

7. **The four `MessageEnvelope` labels stay at §15.4.** 0064 rewrote them from §15.4.1 to §15.4 on the
   premise that §15.4.1 would not exist. Keeping §15.4.1 removes that premise, and the labels are still
   correct on the rule the tree's own tooling applies: a label names the numbered section the target
   heading is written inside, and the two carve-out headings are level-4 siblings of §15.4.1 whose
   enclosing numbered section is §15.4 (§1.2). Relabelling them §15.4.1 would name a subsection that does
   not contain the target and that SPEC-3 retitles to message format and binary I/O requirements. The same
   rule also governs the two carve-out labels outside §15.7, and both are left as they are:
   `docs/reference/adapter-contract.md:371` already writes `§15.4`, and the `§15.4.1` label at
   `spec/21_planned-post-v1.md:31` predates this proposal and is out of its scope.

## 3. Proposed changes

### SPEC-1. Remove from §15.4.1 what §28.5.3 states

In `spec/15_external-api-surface.md`, inside `#### 15.4.1`:

- Delete the first two sentences of the paragraph opening the subsection
  (`spec/15_external-api-surface.md:1473`), which state the stdin/stdout JSON Lines framing and the
  one-JSON-object-per-line rule. §28.5.3 states both, the framing on its Endpoint axis
  (`spec/28_communication-channels.md:525-527`) and the per-line rule on its Messages axis
  (`spec/28_communication-channels.md:541-542`). The paragraph's third sentence, the `prompt` removal
  note, stays: `spec/28_communication-channels.md` states no equivalent, so it has no destination, which
  is the ground on which the `input_required` note also stays.
- Delete the inbound message table and its `**Inbound messages...**` lead.
- Delete the outbound message table and its `**Outbound messages...**` lead.
- Delete the `**`slotId` for concurrent-session multiplexing:**` paragraph.

The subsection's existing successor pointer to §28.5.3 stays and is the sentence N8 requires. The `prompt`
removal note, the `input_required` note, the stderr statement, and the flushing requirement with its
per-language table stay.
The Translation Fidelity Matrix and the `MessageEnvelope` format are level-4 siblings of §15.4.1 rather
than content inside it (§1.2), and SPEC-1 does not reach them.

### SPEC-2. Complete §28.5.3 with the type it never defined

In `spec/28_communication-channels.md`, inside §28.5.3's Message schemas (`spec/28_communication-channels.md:586`),
add an `**Outbound: `set_tracing_context`**` block carrying the payload, the `slotId` addressing rule, the
outcome of a frame that names no session, the reading on a pod whose concurrency was not declared at
launch, the tier availability, and the §8.3 and §16.3 pointers that
§15.4.1's row states today (`spec/15_external-api-surface.md:1494`). The block states the mechanism in
the terms the implementation and §8 use rather than copying the row's adapter-side wording:

- The runtime writes the frame on `CH-MSGSOCK`. On a pod whose pool sets
  `sessionPolicy.maxConcurrentSessions` above 1, the frame names the session whose identifiers it declares
  by carrying that session's `slotId`. On a pod whose pool sets it to 1 the pod has a single addressee, no
  session holds a `slotId`, and the name is implicit, so a conforming frame carries no `slotId` and a
  frame that carries one anyway names that same session.
- The adapter registers the identifiers against the session the frame names. On a pod whose pool sets
  `sessionPolicy.maxConcurrentSessions` above 1, a frame that carries no `slotId` and a frame that carries
  a `slotId` no session on the pod holds both name nothing, regardless of how many slots are occupied when
  the frame arrives. Such a frame is dropped and logged as a protocol error, the outcome the Messages axis
  already states for a `tool_result` whose `id` is unknown
  (`spec/28_communication-channels.md:545-548`). Nothing is sent back to the runtime, because the
  adapter-to-runtime direction of `CH-MSGSOCK` is the closed set `message`, `tool_result`, `heartbeat`, and
  `shutdown` (`spec/28_communication-channels.md:539-540`), and a report frame would be a new inbound
  message type on a channel this proposal is otherwise reducing.
- The pod's concurrency is declared to the adapter at launch (§4.7). An adapter whose concurrency was not
  declared resolves no implicit name: a frame that carries no `slotId` names nothing and is dropped and
  logged as the same protocol error, and a frame that carries the `slotId` of a session the pod holds
  names that session and registers there.
- The adapter consumes the frame rather than relaying it onward, and registers the identifiers by calling
  the `lenny/set_tracing_context` platform tool with the named session's id injected
  (`pkg/adapter/tracingcontext.go:25-56`).
- The gateway merges the identifiers into the session row and validates the merged result under §8.3 at
  registration time, and a child entry cannot overwrite or remove a parent entry
  (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `pkg/delegation/tracing/tracing.go:62, 110`).
- The gateway attaches the registered context to the child's delegation lease when it processes
  `lenny/delegate_task` (`spec/08_recursive-delegation.md:52, 286`).
- The frame is available at all tiers, including Basic, because the adapter issues the platform call
  itself rather than routing it through the runtime's MCP client.

**The rule resolves a name rather than branching on a mode.** The adapter answers "which session does this
frame name?" from the slots it is serving, which it knows because it admitted each one. It never asks
"what kind of pod am I?" at frame time. Stating the rule this way is what makes the two failure cases
fall out of one sentence rather than needing a case each: on a pod whose pool sets
`sessionPolicy.maxConcurrentSessions` above 1, an untagged frame names nothing and a slot-tagged frame
naming a slot the pod does not hold names nothing, and both are the same discard.

**The pod is told its concurrency when it is built.** The adapter must still know whether "no `slotId`"
is a valid name, which is true on a pod whose pool sets `sessionPolicy.maxConcurrentSessions` to 1 and
false on a pool that sets it above 1. The predicate is the pool's configured value rather than the number
of slots occupied at the moment a frame arrives, which is the phrasing §28.5.3, §15.4, and
`schemas/lenny-adapter.proto:581-583` already use, and which the gateway's own bind carries
(`pkg/gateway/session/executor/pod.go:146`). Occupancy would give the wrong answer twice over: a pool set
to 4 with one slot occupied is momentarily serving one session while `slotId` addressing is still in
force, and slots are claimed and released individually
(`pkg/adapter/slotsession.go:104-106`), so occupancy is not a property of the pod at all.

The configured value is a property of the pod for its whole life, so it is declared at construction. The
sandbox controller passes `--max-concurrent-sessions` to the adapter alongside the `--workspace-root`,
`--staging-dir`, and `--runtime-socket` arguments it already renders
(`pkg/controller/sandbox/podspec/podspec.go:560-578`). Reaching the value takes more than reading a field
the builder already holds. §5.2 declares `sessionPolicy.maxConcurrentSessions` on the pool configuration
(`spec/05_runtime-registry-and-pool-model.md:396-397`) and §4.6.1's CRD validation paragraph states a CEL
rule `sessionPolicy.maxConcurrentSessions >= 1` on the CRD (`spec/04_system-components.md:426`), but the
implementation carries neither: `lennyv1.SessionPolicy` declares `Recycle` alone
(`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:28-34`), the rendered manifest exposes `recycle` alone
(`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:186-222`), and the poolstore-to-CRD mirror
`sessionPolicyToCRD` returns nil for any row with no recycle scrub control
(`pkg/controller/poolscaling/poolstoresource.go:156-163`), so a `maxConcurrentSessions: 4` pool with
`recycle.enabled: false` mirrors no `sessionPolicy` at all. SPEC-2 closes that implementation gap rather
than widening a spec surface: the field the spec already declares is added to the CRD type, and the mirror
that the §4.6.3 ownership table makes the sole writer of `SandboxTemplate.spec.*`
(`spec/04_system-components.md:602`) is extended to carry it. The propagation path is then poolstore row
(`pkg/gateway/runtime/runtimestore/runtimestore.go:1018-1023`), PoolScalingController mirror,
`SandboxTemplate.spec.sessionPolicy.maxConcurrentSessions`, the sandbox reconciler's template resolution
(`pkg/controller/sandbox/controller.go:361-364`), the podspec builder, and the adapter flag.

**A default single-session pool declares 1, and only a broken resolution declares nothing.** The platform
encodes one session per pod as an absent value at every layer: the store field is `omitempty` and
documented as defaulting to 1 (`pkg/gateway/runtime/runtimestore/runtimestore.go:1018-1023`), the
poolscaling controller normalizes a non-positive value with `maxConcurrentSessionsOrOne`
(`pkg/controller/poolscaling/controller.go:1244-1248`), and §5.2 states the default configuration as
`maxConcurrentSessions: 1` with `recycle.enabled: false`
(`spec/05_runtime-registry-and-pool-model.md:393-396`). Collapsing "the pool is at the default of 1" and
"the value did not propagate" onto one rendered value would give every ordinary pool the undeclared
reading and disable `set_tracing_context` registration across the default deployment, so the two are kept
distinct.

Every Sandbox the reconciler sees names a pool. `spec.poolRef` is `+kubebuilder:validation:Required`
(`pkg/apis/lenny/v1alpha1/sandbox_types.go:34-35`), the WarmPoolController sets it from the pool it warms
(`pkg/controller/warmpool/controller.go:522-524`), and the §12.6 `CreatePod` path takes a pool id, rejects
an empty one, and stamps it on every Sandbox it writes (`pkg/podregistry/crd.go:314-323`), so a
gateway-created pod resolves through the same template cases as a warm pod. The sandbox reconciler
resolves the value it hands the builder in three cases:

- A Sandbox whose pool's `SandboxTemplate` resolves and sets `sessionPolicy.maxConcurrentSessions` passes
  that value.
- A Sandbox whose pool's `SandboxTemplate` resolves and leaves the field absent passes 1. The absence is
  authoritative because §4.6.3 makes the PoolScalingController the sole writer of `SandboxTemplate.spec.*`
  (`spec/04_system-components.md:602`) and the mirror SPEC-2 extends writes the field for every store row
  above 1, so a resolved template with no value states a pool at the §5.2 default.
- A Sandbox whose template does not resolve passes 0, the undeclared sentinel. `resolveTemplate` returns
  nil for a pool lookup error, an empty `templateRef`, a template lookup error, and the empty `poolRef`
  the CRD rejects (`pkg/controller/sandbox/controller.go:287-303`), and none of those states lets the
  reconciler state the pod's concurrency. A gateway-created pod whose named pool carries no
  `SandboxWarmPool` or no template reference lands here and stays fail-closed for its life, which is the
  intended reading rather than an accident: the reconciler denies on doubt rather than declaring 1 for a
  pod that may serve several slots.

**An undeclared concurrency fails closed.** A pod is created once and never re-rendered
(`pkg/controller/sandbox/controller.go:539`), so a propagation gap is permanent for that pod's life.
Reading an undeclared value as 1 would be fail-open: a pod genuinely serving four slots would treat every
untagged frame as naming "its one session" and register one session's identifiers against a sibling, which
is the cross-session merge SPEC-2 exists to close. The builder therefore renders the value it is given
verbatim, including `--max-concurrent-sessions=0`, `0` means undeclared, the adapter flag's own default is
`0`, and an adapter whose concurrency is undeclared treats "no `slotId`" as naming nothing and drops the
frame. The §4.7 launch-surface sentence carries the three resolution cases and the sentinel, and the
§28.5.3 block carries the adapter's reading of the sentinel, so both halves are staged text. A dropped
registration loses advisory tracing metadata; a wrong registration crosses a session boundary, so the
isolation-relevant path denies on doubt.

The alternative is for the adapter to infer the pod's concurrency from the session claims it observes,
and that inference is unsound in a way worth recording, because an earlier implementation attempt built
it and spent thirty-five commits failing to make it hold. The inference is order-dependent: a claim
naming no slot arriving before the first slot claim commits the pod to the single-session reading, so an
untagged frame then registers against a pod-global session on a pool that runs several. Making the
concurrent reading terminal instead broke the per-slot claim path on any pod that had once served a
pod-global claim. Declaring the value on each claim
(`StartSessionRequest.max_concurrent_sessions`) fixes the soundness but puts a pod-lifetime fact on every
request and widens the §4.7 claim contract with a field every caller must set correctly. Construction is
where a pod-lifetime fact belongs, and it leaves the wire contracts untouched.

`set_tracing_context` therefore joins `message`, `tool_result`, `response`, and `tool_call` in the card's
`slotId` sentence (`spec/28_communication-channels.md:548-552`), which enumerates the types that carry a
`slotId` on a pod whose pool sets `sessionPolicy.maxConcurrentSessions` above 1. The identifiers the frame declares are per-session state: the gateway merges
them into one session row and validates the merged result against that one session under §8.3, so a frame
naming no session has no defined target. The card already addresses per-session frames on this multiplexed
channel by `slotId`, and reusing that costs one optional field rather than a second addressing scheme.

The artifacts follow, in propagation order:

- `schemas/lenny-adapter-jsonl.schema.json` gains an optional `"slotId": { "type": "string" }` property on
  its `set_tracing_context` definition (`schemas/lenny-adapter-jsonl.schema.json:210-223`), matching the
  property the `message`, `tool_call`, `tool_result`, and `response` definitions already carry (:58, :139,
  :161, :190).
- `pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go` gains `MaxConcurrentSessions int32` on
  `SessionPolicy` (:28-34), tagged `json:"maxConcurrentSessions,omitempty"` and marked `+optional` with
  `+kubebuilder:validation:Minimum=1` so the API server
  enforces the rule `spec/04_system-components.md:426` states, and the type's doc comment stops recording
  the field as poolstore-only. `zz_generated.deepcopy.go` (:896-902) and the rendered manifests
  (`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml`, `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml`)
  are regenerated rather than hand-edited.
- `sessionPolicyToCRD` in `pkg/controller/poolscaling/poolstoresource.go:156-170` renders the store row's
  `MaxConcurrentSessions` (`pkg/gateway/runtime/runtimestore/runtimestore.go:1018-1023`) onto the CRD block
  on both of the function's outcomes: the block it already returns for a row carrying a recycle scrub
  control (:165-170), and a block for a row that sets the value above 1 with no recycle scrub control,
  which it returns nil for today (:156-163). A concurrent pool that also sets a recycle scrub control
  takes the first branch, so carrying the value there is what keeps that pool off the sentinel. The guard
  against emitting a value the `Minimum=1` marker rejects is an explicit predicate applied on both
  outcomes: `sessionPolicyToCRD` sets `MaxConcurrentSessions` on the CRD block only when the store row's
  value is above 1, and leaves the CRD field at its zero value otherwise, so a store row that is unset, at
  1, or non-positive mirrors a block carrying no `maxConcurrentSessions`, which is the §5.2 default the
  reconciler reads as 1. The `json:"maxConcurrentSessions,omitempty"` tag is the serialization consequence
  of that zero value rather than the mechanism, because `omitempty` elides only the zero value and a store
  row can carry an explicit 1: `ValidateSessionPolicy` rejects a negative value and normalizes nothing
  (`pkg/gateway/runtime/poolstore/poolstore.go:551-553`), and `maxConcurrentSessionsOrOne` normalizes the
  controller's derived sizing value rather than the stored row
  (`pkg/controller/poolscaling/controller.go:1244-1248`), so an assignment that carried the row verbatim
  would mirror `maxConcurrentSessions: 1` for a pool at the default. The recycling branch is the one that
  would otherwise emit `0`, because a store row
  defaults `MaxConcurrentSessions` to unset while a recycle scrub control is what makes the function return
  a block at all (`pkg/gateway/runtime/runtimestore/runtimestore.go:1019-1022`,
  `pkg/controller/poolscaling/poolstoresource.go:157-169`), so an ordinary recycling pool at default
  concurrency reaches it. Writing an explicit `0` there would be rejected by the
  `+kubebuilder:validation:Minimum=1` marker, and the API server's Invalid response puts the tuple into the
  per-tuple admission backoff, where `syncTuple` records the denial, swallows the error, and skips the
  tuple on subsequent ticks (`pkg/controller/poolscaling/controller.go:580, 588-590`). Because the
  controller replaces the whole `SandboxTemplate.spec` on every apply (:609-610), that stall blocks every
  template field for that pool, including its recycle scrub controls, and every retry resubmits the same
  rejected content. The PoolScalingController remains the sole writer of `SandboxTemplate.spec.*`
  under §4.6.3 (`spec/04_system-components.md:602`), so no ownership statement changes. The comment at
  `pkg/admission/pool_config_validator/validator.go:366-367`, which records that the CRD carries no
  `maxConcurrentSessions` field, is corrected; the CAP_NET_RAW gate it describes is unchanged and stays
  where it is.
- `pkg/controller/sandbox/podspec/podspec.go` gains a `MaxConcurrentSessions` field on `Inputs`, populated
  at `pkg/controller/sandbox/controller.go:382` from the three resolution cases above, alongside the grace
  and workspace-size fields the reconciler already reads off the resolved template (:361-364). The builder
  renders `--max-concurrent-sessions` on both adapter argument lists, the sidecar `adapterArgs` (:560-578)
  and the embedded `embeddedArgs` (:661-673), because §5.2 admits `maxConcurrentSessions > 1` on either §4.7
  deployment model (`spec/05_runtime-registry-and-pool-model.md:429-433` names no model) and the embedded
  runtime is the adapter. It renders the value it is given without reinterpreting it, so an `Inputs` value
  of 0 renders `--max-concurrent-sessions=0`.
- `cmd/lenny-adapter/main.go` gains the matching flag next to the arguments declared at :112-130,
  defaulting to `0`, and assigns it to the `adapter.Server` it constructs, which is what carries it to the
  frame-addressing gate. Every binary the builder's `embeddedArgs` path launches declares each argument
  that path renders unconditionally, because the default `flag.ExitOnError` parser exits 2 on an unknown
  flag and crash-loops the pod. The tree carries two such binaries:
  `cmd/runtimes/echo-embedded/main.go:90-99` and `cmd/runtimes/preconnect-echo/main.go:67-76`, the §6.1
  SDK-warm reference runtime declared `deploymentModel: embedded` in the Kind agent workload
  (`tests/testinfra/kind/agent-workload.yaml:104-107`) and driven by the tier-7b startup-latency scaffold
  (`tests/tier7b_load_kind/scaffolds_test.go:380`). In the embedded model the reference main is the
  adapter: it constructs `adapter.New(version)` and copies each parsed launch argument onto the
  `adapter.Server` it serves, with nothing picked up implicitly
  (`cmd/runtimes/echo-embedded/main.go:110-135`, `cmd/runtimes/preconnect-echo/main.go:87-107`). Both
  mains therefore assign the parsed `--max-concurrent-sessions` onto that server, the same
  declare-and-assign pattern they apply to `--workspace-root`, `--staging-dir`, `--mcp-socket`, and
  `--gateway-grpc-addr`. Declaring the flag and leaving it unassigned would hold every embedded pod at the
  `0` sentinel, including a default single-session embedded pool whose conforming frames carry no
  `slotId`, so parsing the argument is necessary and not sufficient.
  §4.7's adapter launch surface gains the argument, since it is part of how the platform launches an
  adapter. The §4.7 sentence states that the argument carries the pool's configured
  `sessionPolicy.maxConcurrentSessions`, that a pod whose resolved template declares no value carries 1,
  that the controller renders `0` when the pod's pool or template does not resolve, and that `0` declares
  the concurrency undeclared, which the adapter reads as the fail-closed case §28.5.3 states.
- `pkg/adapter` gains the declared concurrency as a field on `Server`, alongside the other
  launch-supplied fields the mains assign (`pkg/adapter/server.go:73-99`), and resolves each
  `set_tracing_context` frame to the session it names and registers there,
  dropping and logging a frame that names none. Today the runtime's output is fanned out to every
  Attach subscriber (`pkg/adapter/socketruntime.go:340-356`), the per-slot demultiplexer passes an
  untagged frame through so the per-session heartbeat path still observes it
  (`pkg/adapter/attach.go:285-291`), and each stream then calls `handleSetTracingContext` with its own
  session id (`pkg/adapter/attach.go:115`), so one untagged frame on a four-slot pod writes four session
  rows and merges one session's identifiers into every sibling's delegation lease. Resolving the name
  once, against the slots the adapter is serving, keeps one frame to one registration.

The §15.4.1 row states instead that the adapter stores the context and automatically attaches it to all
subsequent `lenny/delegate_task` requests, with gateway validation when the delegation request arrives
(`spec/15_external-api-surface.md:1494`). The adapter stores and attaches nothing, and the gateway
validates at registration, so the block corrects the row rather than copying it. The correction matters
beyond wording: custody of the runtime-self-reported identifiers and the §8.3 enforcement point (the size
cap, the sensitive-key blocklist, the URL rejection, and the parent-entry merge rule) sit in the gateway,
which applies them against the authenticated session. SPEC-1 deletes the §15.4.1 row along with the
outbound table. The platform-tool table row at `spec/08_recursive-delegation.md:540` repeats the same
adapter-stores wording and is corrected to the mechanism above, so the two surviving statements agree.

The operator-facing entry at `docs/reference/adapter-contract.md:310-316` is the page a runtime author
reads for this frame, and it states the same adapter-stores wording ("Registers tracing identifiers that
the adapter attaches to all subsequent `lenny/delegate_task` gRPC requests", :316). That sentence is
corrected to the same registration-and-attachment mechanism, and the entry gains the `slotId` addressing
rule and the discard outcome, so a runtime author learns that on a pod whose pool sets
`sessionPolicy.maxConcurrentSessions` above 1 an untagged frame registers nothing and is logged as a
protocol error rather than answered on the channel.

The card then states nine message types, so the pointer sentence in `spec/15_external-api-surface.md`
at lines 1702 through 1707, which enumerates the eight types the card schema-defines today, gains
`set_tracing_context` in its list. The enumeration then matches both the completed card and the
`schemas/lenny-adapter-jsonl.schema.json` bullet at `spec/15_external-api-surface.md:1463`, which already
names all nine as defined in §28.5.3.

### SPEC-3. Retitle §15.4.1

`#### 15.4.1 Adapter↔Binary Protocol` becomes `#### 15.4.1 Message Format and Binary I/O Requirements`,
and its anchor becomes `1541-message-format-and-binary-io-requirements`.

The one link into the old anchor is rewritten by hand as part of this proposal, and
`tests/spec-anchor-moves.json` stays empty, so this change stages no anchor-pass run. An empty map is a
load error rather than a pass that rewrites nothing: `loadMoves` rejects a map whose `moves` array is
empty and returns before it reads any citation (`scripts/specshift/anchor/moves.go:125-127`). The
anchor-move map is a retirement register rather than a
rename table: its loader derives a retired section number from the leading digits of each retired anchor
slug (`scripts/specshift/anchor/moves.go:74-79, 136-139`), so an entry keyed `1541-adapterbinary-protocol`
would make `retiresSection("15.4.1")` true (`moves.go:88-97`) and turn every bare `§15.4.1` citation in the
write domain into an occurrence the pass must resolve from `tests/registers/anchor-senses.yaml`, aborting
non-zero before any write where the register has no entry (`scripts/specshift/anchor/anchor.go:28-32,
239-247`). Two such citations are live at `cmd/runtimes/streaming-echo/main.go:451` and
`cmd/runtimes/streaming-echo/schemaversion_test.go:18`, and §15.4.1 survives under decision 1, so those
citations are correct as written and there is nothing for a sense entry to resolve. The register's
documented retirement condition is a change that leaves no link into a retired anchor
(`tests/registers/README.md:154`), which the hand rewrite below satisfies.

The link is the §15.4.5 Runtime Author Roadmap step for Basic-level authors at
`spec/15_external-api-surface.md:2035-2036`. Its current sentence sends the reader to §15.4.1 for the
framing that SPEC-1 deletes, so retargeting the anchor alone would leave a false statement. Item 2 becomes:

> 2. **[Section 28.5.3](28_communication-channels.md#2853-intra-pod)** — the message types, the
>    `MessagePart` format, the stdin/stdout JSON Lines framing, and the simplified response shorthand,
>    with **[Section 15.4.1](#1541-message-format-and-binary-io-requirements)** for the stdout flushing
>    requirement and its per-language guidance, which stay here.

### SPEC-4. Repoint the references SPEC-1's deletions falsify

SPEC-1 removes the §15.4 statement of the stdin/stdout framing, of the message enumeration, and of the
`slotId` multiplexing rule, so every citation naming §15.4 as the source of any of the three has to be
reconciled. The four links into `#messageenvelope--unified-message-format` keep their `§15.4` label, per
decision 7.

In `spec/28_communication-channels.md`:

- §28.5.3's Endpoint axis attributes the stdin/stdout newline-delimited JSON framing to §15.4 (lines 525
  through 527), which is the sentence SPEC-1 deletes (`spec/15_external-api-surface.md:1473`). The
  `[§15.4]` citation is dropped and the card states the framing on its own authority, keeping the §4.7
  citation for the abstract-socket sentence that follows. The card is the destination, on the both-legs
  rule, and the §15.4.5 roadmap item SPEC-3 writes then resolves to a section that states the framing.
- §28.5.3's Messages axis cites §15.4 for the enumeration it owns (line 541). The pointer is removed, since
  the card states the messages itself.
- The same axis carries a second `[§15.4]` citation, closing the sentence group that runs from the
  one-JSON-object-per-line rule to the minimal-schema statement
  (`spec/28_communication-channels.md:541-545`). That citation is dropped as well, and the card states the
  group on its own authority. The per-line rule it attributes to §15.4 is the sentence SPEC-1 deletes, and
  §15.4 states it nowhere else (`spec/15_external-api-surface.md:1473`). The statement that `heartbeat`
  and `shutdown` use their own minimal schemas survives in §15.4 only at the `MessageEnvelope` carve-out,
  which itself sends the reader to §28.5.3 for those schemas
  (`spec/15_external-api-surface.md:1577`), so keeping the citation would restore the circular pointer
  §1.3 describes. This is the treatment SPEC-4 gives the Endpoint axis above.
- The same axis's `slotId` sentence (lines 548 through 552) drops its `[§15.4]` citation and keeps the
  `[§5.2]` one. Its list of the types that carry a `slotId` gains `set_tracing_context`, per SPEC-2. The
  sentence keeps its current form otherwise, stating what a conforming runtime emits; SPEC-2's
  `set_tracing_context` block is where the adapter's treatment of a non-conforming `slotId` on a pod whose
  pool sets `sessionPolicy.maxConcurrentSessions` to 1 is stated, so the two do not restate one rule.
- The Exclusivity axis (lines 567 through 570) drops its `[§15.4]` citation.
- The card gains the runtime-side obligation the deleted paragraph carried, stated on the Messages axis
  next to the `slotId` sentence: a runtime serving a pool that sets `maxConcurrentSessions > 1` implements
  a dispatch loop keyed on `slotId`, and the one channel carries multiple independent concurrent session
  streams. The card is the destination, on the same both-legs rule SPEC-2 applies to `set_tracing_context`.
- The §28.6 scoping paragraph (lines 1622 through 1626) and the §28.8 `CH-MSGSOCK` row's exclusivity cell
  (line 1710) drop the `[§15.4]` citation on the `slotId` keying and cite §28.5.3 instead.

In `spec/15_external-api-surface.md`, the `MessageEnvelope` `slotId` field note (line 1616) currently ends
"See the `slotId` multiplexing note in [§28.5.3]", which pointed at a card that cited §15.4 back. With the
rule stated in the card, the pointer resolves and the sentence stands as written.

In `spec/29_communication-scenarios.md`, the two sentences that attribute the rule to §15.4 are repointed
to §28.5.3: line 1477, which cites §28.5.3 and §15.4 for the dispatch loop, drops the `[§15.4]` citation,
and lines 1499 through 1501, which name §15.4 as stating the multiple independent concurrent session
streams, name §28.5.3 instead. The same bullet at lines 1475 through 1476 restates the §28.5.3
enumeration of the types that carry a `slotId`, so its list gains `set_tracing_context` and reads
`message`, `tool_result`, `response`, `tool_call`, and `set_tracing_context`, matching the amended
`spec/28_communication-channels.md:549-550`. Leaving it at four types would state one enumeration two
ways, and a reader working from the shorter list would take an untagged frame to be well formed on a
pod whose pool sets `sessionPolicy.maxConcurrentSessions` above 1, which SPEC-2 makes a dropped frame.

### SPEC-5. The concurrent-registration fix that landed before this proposal

The gateway's `lenny/set_tracing_context` handler computed the §8.3 merge from a read taken before the
store's update transaction locked the row, then assigned the result inside that transaction, so two
callers registering against one session lost one registration while both reported success. That defect
predates this proposal: the adapter's forward and the runtime's own platform call have both reached the
handler since before §28.5.3 existed. It surfaced while an earlier attempt implemented SPEC-2, and it is
fixed and tested on the branch already
(`pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `tests/tier7a_load_local/tracing_context_concurrent_registration_test.go`).
It is recorded here so a reviewer does not raise it again, and so the implementation does not stage it a
second time.

## 4. Non-goals

- **Renumbering §15.4.2 through §15.4.6.**
- **Widening §28.2's boundary set to admit an external-client edge.**
- **Moving the Translation Fidelity Matrix or `MessageEnvelope`.**

## 5. Testing

The change reaches tier 0, tier 1, tier 2, tier 3, tier 5, and tier 11. Tier 2 is reached because SPEC-2
adds a CRD field and extends the PoolScalingController mirror that writes it, which
`.claude/rules/test-coverage.md` maps to an envtest against a real API server. Tier 5 is reached because
SPEC-2 changes the container argument list the builder renders onto every agent pod on both deployment
models and regenerates the served CRD manifest the chart installs, and `.claude/rules/test-coverage.md:37`
maps pod lifecycle to the Kind stage.

**Tier 0 (static).** The retitle and the citation edits are read by the gates over the specification tree.

- `TestFragmentLinkGateCertifiesTheTree` (`tests/tier0_static/fragment_link_test.go:375`) resolves the
  rewritten same-page link at `spec/15_external-api-surface.md:2036` against the new
  `1541-message-format-and-binary-io-requirements` anchor, and fails on any surviving reference to
  `1541-adapterbinary-protocol`. The gate reads `spec/` and `docs/`
  (`tests/tier0_static/fragment_link_test.go:371-373`), and after application no fragment link under
  either tree targets `1541-adapterbinary-protocol`. The old slug survives outside that surface in
  `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, which is a
  historical record, and in the frozen fixture trees under `scripts/specshift/testdata/anchorpass/`,
  which are inputs the anchor-pass cases assert against. Both stay as written.
- `TestEveryCitationNamesADocumentAReaderCanReach`
  (`tests/tier0_static/citation_document_test.go:412`) covers the citations SPEC-4 rewrites in `spec/28`
  and `spec/29`.
- The heading walker does not read the retitled heading, and the retitle requires no `spec/README.md`
  row. Its in-scope predicates are `indexedHeading`, which matches a `##` or `###` heading numbered
  `N` or `N.M`, and `cardHeading`, which matches the §28.5 cards
  (`tests/tier0_static/heading_walker_test.go:42-43, 47-48, 100-108`). A level-4 heading numbered
  `15.4.1` matches neither, and the index carries a row for §15.4 alone (`spec/README.md:126`).
  `TestHeadingWalkerSlugMatchesTheRenderedAnchor` (:161) is a fixed case table over `headingSlug` that
  reads no specification file. This proposal adds the case
  `{"15.4.1 Message Format and Binary I/O Requirements",
  "1541-message-format-and-binary-io-requirements"}` to that table (:163-168) so the slug SPEC-3
  derives is pinned, and the fragment-link gate named above is what certifies the rewritten link.
- `tests/spec-anchor-moves.json` and `tests/registers/anchor-senses.yaml` stay empty, so this change
  stages no anchor-pass run and the two bare `§15.4.1` citations in `cmd/runtimes/streaming-echo` stand
  unchanged (SPEC-3). No anchor-pass case is added. The loader's treatment of an empty map is already
  pinned: `TestAnchorPassRejectsAMissingOrMalformedAnchorMoveMap` requires the load of
  `maps/empty-moves.json` to fail with "carries no move" (`scripts/specshift/run_test.go:8373, 8385`;
  `scripts/specshift/anchor/moves.go:125-127`).

**Tier 1 (unit).** SPEC-2 changes several shipped packages, so the in-process suite over each pins its
part of the rule.

The frame-addressing cases go in `pkg/adapter/heartbeat_external_test.go`, which already holds the
in-process coverage of this frame, and each carries
`// spec: 28.5.3 (set_tracing_context slotId addressing)`.

- On a pod launched with `--max-concurrent-sessions` above 1, a `set_tracing_context` frame carrying no
  `slotId` names no session: it issues no platform call, registers nothing, and is logged as a protocol
  error.
- On the same pod, a frame carrying the `slotId` of a slot the adapter is serving issues exactly one
  platform call, against that slot's session id and no other.
- On the same pod, a frame carrying a `slotId` no session on the pod holds names no session: it issues no
  platform call and is logged as a protocol error. This is the stale-or-foreign slot case, distinct from
  the untagged one and resolved by the same rule.
- On a pod launched with `--max-concurrent-sessions` at 1, a frame carrying no `slotId` registers against
  the pod's one session, and a frame carrying any `slotId` registers against that same session, since the
  pod has a single addressee and no session there holds a slot id. A runtime that stamps the field
  defensively is not penalised for it.
- On a pod launched with `--max-concurrent-sessions` at 0, which is the undeclared value, a frame carrying
  no `slotId` names nothing and registers nothing. This is the fail-closed boundary the propagation gaps
  land on.
- On the same undeclared pod serving a slot, a frame carrying that slot's `slotId` issues exactly one
  platform call against that slot's session id, so the undeclared reading drops the frames that name
  nothing and keeps the frames that name a session the pod holds. This pins the second half of the
  undeclared rule SPEC-2 stages, which the case above covers only for the drop.

The shipped case `TestAttachForwardsSetTracingContext_spec_15_4_1_1455`
(`pkg/adapter/heartbeat_external_test.go:109`) sends an untagged `set_tracing_context` frame on a
single-session Attach and asserts the forward. It keeps that behavior under this change, because a pod on a
pool at the §5.2 default is launched with `--max-concurrent-sessions=1` and a frame carrying no `slotId`
names its one session, so the case is retained. Its `// spec:` annotation is repointed to
`28.5.3 (set_tracing_context slotId addressing)` alongside the cases above, and its setup declares the
launch value explicitly rather than relying on the flag's undeclared default.

The rendering cases go in `pkg/controller/sandbox/podspec/podspec_test.go` and
`pkg/controller/sandbox/controller_test.go`, mirroring the two-level precedent the tree already carries
for the `--require-so-peercred` render decision (`pkg/controller/sandbox/podspec/nonce_only_test.go:23,
58`; `pkg/controller/sandbox/nonce_only_test.go:89`), and each carries
`// spec: 4.7 (adapter launch surface), 5.2 (maxConcurrentSessions)`.

- `Build` renders `--max-concurrent-sessions` with the resolved value on the sidecar adapter container's
  argument list (`pkg/controller/sandbox/podspec/podspec.go:560-578`).
- `Build` renders the same argument with the same value on the embedded runtime container's argument list
  (:661-673), so an embedded pod on a concurrent pool is not left undeclared.
- `Build` renders `--max-concurrent-sessions=0` when `Inputs.MaxConcurrentSessions` is unset, rather than
  omitting the argument, so the rendered pod states the undeclared case explicitly.
- `Reconcile` populates `Inputs.MaxConcurrentSessions` with the declared value for a Sandbox whose
  resolved SandboxTemplate carries `sessionPolicy.maxConcurrentSessions`, with 1 for a Sandbox whose
  resolved SandboxTemplate leaves the field absent, and with 0 for a Sandbox whose pool or template does
  not resolve (`pkg/controller/sandbox/controller.go:287-303`). The three-case split is what keeps a
  default single-session pool off the undeclared sentinel. Every case drives a Sandbox carrying
  `spec.poolRef`, which the CRD requires (`pkg/apis/lenny/v1alpha1/sandbox_types.go:34-35`) and which the
  §12.6 `CreatePod` path stamps on every Sandbox it writes (`pkg/podregistry/crd.go:314-323`).
- `sessionPolicyToCRD` renders a `sessionPolicy` block carrying `maxConcurrentSessions` for a store row
  that sets the value above 1 with no recycle scrub control, which returns nil today
  (`pkg/controller/poolscaling/poolstoresource.go:156-163`), and leaves the CRD field unset for a store row
  at 1 with no recycle scrub control, so the API server never sees a value the `Minimum=1` marker rejects.
  Both cases go in `pkg/controller/poolscaling`'s existing fake-client suite.
- `sessionPolicyToCRD` also carries `maxConcurrentSessions` on the branch that already returns a block: a
  store row at `maxConcurrentSessions: 4` with a recycle scrub control set, for example
  `recycle.enabled: true` and `scrubProfile: vm-restart`, mirrors a block carrying both `recycle` and
  `sessionPolicy.maxConcurrentSessions: 4` (`pkg/controller/poolscaling/poolstoresource.go:165-170`). The
  case carries a `// diagnosis:` stating that dropping the value on this branch launches every pod of a
  concurrent recycling pool, which §5.2 lists as a supported configuration
  (`spec/05_runtime-registry-and-pool-model.md:425`), declaring 1 and re-opens the cross-session merge.
  Without it an implementation that extends only the nil-returning branch passes every other listed case.
- `sessionPolicyToCRD` leaves `maxConcurrentSessions` unset on that same branch for a store row with a
  recycle scrub control set, for example `recycle.enabled: true` and `scrubProfile: vm-restart`, and
  `maxConcurrentSessions` unset or at 1: the mirrored block carries `recycle` and no
  `maxConcurrentSessions`, so the API server never sees a value the `Minimum=1` bound rejects. This is the
  ordinary recycling pool at default concurrency, since the store row's `MaxConcurrentSessions` defaults to
  unset (`pkg/gateway/runtime/runtimestore/runtimestore.go:1019-1022`) and a recycle scrub control is what
  makes the function return a block at all (`pkg/controller/poolscaling/poolstoresource.go:157-169`). The
  case carries a `// diagnosis:` stating that emitting `0` on this branch makes the API server reject the
  write as Invalid, which puts the pool's `SandboxTemplate` tuple into the per-tuple admission backoff
  where `syncTuple` records the denial, swallows the error, and skips the tuple on subsequent ticks
  (`pkg/controller/poolscaling/controller.go:580, 588-590`), stalling all of that pool's template
  reconciliation because the controller replaces the whole spec on every apply (:609-610). An
  implementation that carries the store value verbatim on this branch, or that declares the CRD field
  without `omitempty`, passes every other listed case.
- `cmd/lenny-adapter` parses `--max-concurrent-sessions`, defaults it to 0, and assigns it to the
  `adapter.Server` it constructs.
- Each embedded reference main parses the full argument list `embeddedArgs` renders, including
  `--max-concurrent-sessions`, extending the fixed argument lists
  `TestEmbeddedMainAcceptsPlatformMCPFlags_spec_9_1` (`cmd/runtimes/echo-embedded/main_test.go:21`) and
  `TestPreconnectMainAcceptsPlatformMCPFlags_spec_9_1`
  (`cmd/runtimes/preconnect-echo/main_test.go:21-40`) pass today.
- Each embedded reference main assigns the parsed value onto the `adapter.Server` it constructs, so the
  declared concurrency reaches the frame-addressing gate. Both mains build that server inline in `main`
  today (`cmd/runtimes/echo-embedded/main.go:110-118`, `cmd/runtimes/preconnect-echo/main.go:87-92`), so
  the flag-to-server assignment moves into a small helper each main's test calls, asserting that a
  rendered argument list carrying `--max-concurrent-sessions=4` produces a server declaring 4 and that one
  carrying `=1` produces a server declaring 1. A declare-and-ignore implementation satisfies the parse
  case above and still holds every embedded pod at the `0` sentinel, which drops every conforming frame
  on the whole embedded deployment model, including the `preconnect-echo` pool that §5.2 admits only at
  `maxConcurrentSessions` of 1 (`spec/05_runtime-registry-and-pool-model.md:434`).

**Tier 2 (component).** `TestPoolScalingControllerMirrorsMaxConcurrentSessions` goes in
`tests/tier2_component/controllers/`, alongside and mirroring `TestPoolScalingControllerMirrorsNonceOnlyAck`
(`tests/tier2_component/controllers/poolscaling_nonce_only_test.go:44`). It drives the
PoolScalingController Sync from a PoolStoreSource against an envtest API server with the `lenny.dev` CRDs
installed, and asserts that a store row setting `maxConcurrentSessions: 4` with `recycle.enabled: false`
survives a round trip through the real `SandboxTemplate` CRD schema, which the fake-client case cannot
show. It drives a second store row alongside it, at `maxConcurrentSessions: 1` with
`recycle.enabled: false`, and asserts that the mirrored template carries no
`sessionPolicy.maxConcurrentSessions`, so the default pool's write is accepted rather than rejected by the
`Minimum=1` bound and the reconciler's absent-field reading is the one a default pool produces. It drives
a third store row at `maxConcurrentSessions: 4` with `recycle.enabled: true` and
`scrubProfile: vm-restart`, and asserts that the mirrored template carries both the `recycle` block and
`sessionPolicy.maxConcurrentSessions: 4`, so the branch that already returns a block is covered against
the real CRD schema. It drives a fourth store row with `maxConcurrentSessions` unset,
`recycle.enabled: true`, and `scrubProfile: vm-restart`, which is the ordinary recycling pool at default
concurrency, and asserts that the API server accepts the write and that the mirrored template carries the
`recycle` block and no `sessionPolicy.maxConcurrentSessions`, so the branch that can emit a value never
presents `0` to the `Minimum=1` bound. It carries
`// spec: 4.6.3 (CRD field ownership), 5.2 (maxConcurrentSessions)` and a
`// diagnosis:` comment naming the failure modes: the regenerated CRD manifest omits the field so
the API server drops it on write, the mirror does not copy the store row, or the controller's full-spec
overwrite clears it. Any of those leaves every pod on the pool launched undeclared, which fails
closed and silently disables `set_tracing_context` registration for that pool. The diagnosis also names
the fourth row's failure mode: the mirror emits `maxConcurrentSessions: 0` for a default-concurrency
recycling pool, the API server rejects the write as Invalid, and the tuple enters the per-tuple admission
backoff (`pkg/controller/poolscaling/controller.go:580, 588-590`), which stalls every field of that
pool's `SandboxTemplate` because the controller replaces the whole spec on each apply (:609-610).

**Tier 3 (contract).** SPEC-2 publishes two wire contracts, the served `SandboxTemplate` CRD schema and the
adapter JSONL frame.

The CRD case goes in `tests/tier3_contract/crd_schema/`, alongside
`TestSandboxTemplateServedSchemaExposesAcknowledgeNonceOnlyAuth`
(`tests/tier3_contract/crd_schema/nonce_only_fields_test.go:125-127`), which is the precedent case on the
same CRD for the same feature family SPEC-2 mirrors. It asserts that the regenerated
`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` exposes `spec.sessionPolicy.maxConcurrentSessions` as
an integer carrying the `minimum: 1` bound, and it carries
`// spec: 4.6.1 (CRD validation), 5.2 (maxConcurrentSessions)` with a `// diagnosis:` naming the
consequence: the field is absent or unbounded in the served schema, so the mirror's write is dropped or an
out-of-range value is accepted, and every pod on the pool launches undeclared. The tier-2 envtest cannot
substitute, because it drives one in-range value and passes as long as that value round-trips, so a
regeneration that publishes the field at the wrong nesting or without the bound survives it. The suite's
own header states the seam: the tier-0 drift check compares the Go types against the manifest, while this
suite pins the served field and type contract the manifest publishes
(`tests/tier3_contract/crd_schema/nonce_only_fields_test.go:6-15`).

The JSONL cases cover the `set_tracing_context` frame that
`schemas/lenny-adapter-jsonl.schema.json:210-223` publishes and that `pkg/adapter/tracingcontext.go:36-56`
consumes and forwards to the gateway's `lenny/set_tracing_context` platform tool, which nothing ties
together today.

- Add `schemas/examples/jsonl.set_tracing_context.json` and validate it in
  `TestAdapterJSONLExamplesValidate` (`tests/tier0_static/schemas_test.go:184`), whose current example
  list covers `message`, `heartbeat`, `tool_call`, and `response` alone.
- Add a contract case annotated `// spec: 28.5.3 (set_tracing_context schema), 8.3 (delegation
  validation)` asserting that a well-formed frame validates, that a frame with a missing `context` is
  rejected by the schema, that a frame with an empty `context` validates against the schema and is
  dropped by the adapter without a platform call, and that a well-formed frame is consumed by the
  adapter, which issues the `lenny/set_tracing_context` platform call rather than relaying the frame
  onward.
- Add a multi-slot case to the same contract test, annotated `// spec: 28.5.3 (slotId multiplexing), 8.3
  (delegation validation)`, over an adapter launched with `--max-concurrent-sessions` above 1 and serving
  two active slots: a `set_tracing_context` frame
  carrying the `slotId` of one slot produces exactly one platform call, against that slot's session id
  and no other, and a frame carrying no `slotId` produces no platform call at all. The case pins the
  granularity SPEC-2 states, which the fan-out in `pkg/adapter/socketruntime.go:340-356` would otherwise
  leave to the number of attached streams.

  The two rejection paths are asserted at the two enforcement points the tree has. The schema
  rejects a missing `context` through `"required": ["type", "context"]`, and it accepts an empty object,
  since `context` is typed `{"type": "object", "additionalProperties": true}` with no `minProperties`
  (`schemas/lenny-adapter-jsonl.schema.json:210-223`; the file states no `minProperties` anywhere). The
  empty frame is caught by the adapter's `len(frame.Context) == 0` guard
  (`pkg/adapter/tracingcontext.go:42-46`). No emptiness rule is staged into the schema: the §8.3
  validators are `TRACING_CONTEXT_TOO_LARGE`, `TRACING_CONTEXT_SENSITIVE_KEY`, and
  `TRACING_CONTEXT_URL_NOT_ALLOWED` (`spec/18_build-sequence.md:428`), none of which is an emptiness
  rule, so tightening the published schema would add a contract the spec does not state. SPEC-2's
  `set_tracing_context` block therefore states the schema as published and describes the adapter-side
  drop as adapter behavior. The only schema edit SPEC-2 stages is the optional `slotId` property.

**Tier 5 (e2e Kind).** The builder renders `--max-concurrent-sessions` unconditionally onto both container
argument lists, and an embedded runtime image that does not declare it exits at `flag.Parse` before it
serves, so the pod never becomes Ready. That failure is a pod-lifecycle failure the in-process tiers
cannot reproduce.

`TestPodLifecycle` (`tests/tier5_e2e_kind/pod_lifecycle_test.go:32`) already warms one pod per §4.7
deployment model from the Kind agent workload and asserts each reaches Ready with the container set its
model requires. The change extends it, or stages a case beside it annotated
`// spec: 4.7 (adapter launch surface), 5.2 (maxConcurrentSessions)`, asserting that with the argument
rendered the sidecar pool and both embedded-model runtimes of the workload,
`echo-runtime-embedded` and `preconnect-echo-runtime`
(`tests/testinfra/kind/agent-workload.yaml:80, 107`), still reach Ready with no `flag.Parse` exit and
serve a session. A second assertion applies a `SandboxTemplate` carrying
`spec.sessionPolicy.maxConcurrentSessions` against the CRD the installed chart serves and requires the
API server to accept it, which is what confirms the regenerated manifest reached the chart. The
`// diagnosis:` names both consequences: an undeclared flag crash-loops every embedded pod, and a CRD
that does not serve the field leaves every pod on the pool launched undeclared.

**Tier 11 (documentation).** `TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent`
(`tests/tier11_docs/successor_pointer_test.go:96`) reads the §15.4 body that SPEC-1 empties and requires a
pointer naming a `CH-` identifier and a §28.5 card. The gate reads each line on its own and counts a
pointer as named only when the `CH-` token sits on the same line as the link (:104-113), so the wrapped
pointer at `spec/15_external-api-surface.md:1516-1518` carries the identifier on :1516 and the link on
:1517 and does not satisfy the identifier half. The lines that do satisfy it are
`spec/15_external-api-surface.md:1463, 1465, 1577, 1756, 2044, 2077`, all outside the body SPEC-1 empties
and all surviving the change. The case is run after SPEC-1 to confirm that, and
`TestSuccessorPointersNameACardThatExists` (:137) confirms the card it names is declared.

**Annotation updates.** The tests that cite §15.4.1 for the `slotId` multiplexing rule are repointed to
§28.5.3, which owns the rule after SPEC-1 and SPEC-4:
`pkg/gateway/runtime/adapterclient/client_test.go:1423, 1437, 1451, 1472`,
`pkg/gateway/session/executor/pod_test.go:252, 366`, and
`tests/tier10_conformance/concurrent_slot_conformance_test.go:39, 143`.

The tier-3 runtime SDK tests cite §15.4.1 for the framing, the `message`/`response` round trip, the
heartbeat acknowledgement, the shutdown frame, and the adapter-local tool helpers, all of which SPEC-1
deletes from §15.4.1 and §28.5.3
owns (`spec/28_communication-channels.md:606-621, 773-778`). The `15.4.1` half of each of those
annotations is repointed to `28.5.3` and the `15.7` half is kept:
`tests/tier3_contract/sdks/runtime_sdk_test.go:248, 262, 276, 372, 391, 406`. The case at :372,
`TestRuntimeSDKWorkspaceHelpers`, is in that set because the only thing §15.4.1 states about tool calls
today is the `tool_call` row of the outbound table (`spec/15_external-api-surface.md:1491`) and the
`tool_result` row of the inbound table (:1480), both of which SPEC-1 deletes, and its own diagnosis
already names §28.5.3 as the owner of the helpers it exercises. Each of those cases asserts
message-loop or tool-helper behavior rather than the stdout flushing requirement that stays in §15.4.1, so
none of them retains a `15.4.1` citation. The diagnosis prose on each already names §28.5.3 as the owner,
so the annotations follow it.

## 6. Resolved in adversarial review

### Pass 1 (2026-08-12, automated)

- **SPEC-4 relabelled the four `MessageEnvelope` links `§15.4.1`, a subsection that does not contain the
  target.** `#### Translation Fidelity Matrix` and `#### `MessageEnvelope` — Unified Message Format`
  (`spec/15_external-api-surface.md:1520, 1575`) are level-4 headings, the same depth as `#### 15.4.1`
  (:1471), so they terminate §15.4.1 rather than sit inside it, and the enclosing numbered section is
  §15.4. The heading index derives the same answer (`scripts/specshift/anchor/heading.go:62-67, 168-176,
  238-244`) and the tier-11 gate defines a section body the same way
  (`tests/tier11_docs/successor_pointer_test.go:62-63`). The bullet is dropped, decision 5 now states that
  the four labels stay at `§15.4` and why, §1.1, the §1.2 table, and SPEC-1's stays list no longer
  describe the two sibling blocks as content of §15.4.1, and decision 5 records the two carve-out labels
  outside §15.7 at `docs/reference/adapter-contract.md:371` and `spec/21_planned-post-v1.md:31`.
- **SPEC-1 deleted the framing sentence while §15.4.5's roadmap said the framing stays in §15.4.1.** The
  one same-page link into the old anchor is the Basic-level roadmap step at
  `spec/15_external-api-surface.md:2035-2036`, whose sentence claims §15.4.1 holds the stdin/stdout JSON
  Lines framing that SPEC-1 deletes (:1473). SPEC-3 now gives the replacement text, attributing the
  framing to §28.5.3, which states it on the `CH-MSGSOCK` card, and citing the retitled §15.4.1 for the
  stdout flushing requirement and its per-language guidance.
- **SPEC-1 deleted the only §15.4 statement of `slotId` multiplexing while §28 and §29 still cited §15.4
  for it.** The deleted paragraph (`spec/15_external-api-surface.md:1498`) is the sole source of the
  adapter-assigned `slotId` and the runtime's dispatch loop, and six sites cite §15.4 for it:
  `spec/28_communication-channels.md:548-552, 567-570, 1622-1626, 1710` and
  `spec/29_communication-scenarios.md:1477, 1499-1501`. SPEC-4 now reconciles each, states the
  runtime-side dispatch-loop obligation in the `CH-MSGSOCK` card so the deleted paragraph has a
  destination, and resolves the loop the back-pointer at `spec/15_external-api-surface.md:1616` closed.
  `spec/29_communication-scenarios.md` is added to the files-touched list.
- **The staged anchor-move entry would have declared §15.4.1 retired and aborted the pass it drives.** The
  map's loader derives a retired section number from an anchor slug's leading digits
  (`scripts/specshift/anchor/moves.go:74-79, 136-139`), so a `1541-adapterbinary-protocol` entry makes
  `retiresSection("15.4.1")` true (:88-97) and every bare `§15.4.1` citation with no sense entry stops the
  run non-zero before any write (`scripts/specshift/anchor/anchor.go:28-32, 239-247`), including
  `cmd/runtimes/streaming-echo/main.go:451` and `cmd/runtimes/streaming-echo/schemaversion_test.go:18`.
  Because decision 1 keeps §15.4.1, those citations are correct as written. SPEC-3 now drops the map entry,
  states that the single inbound link is rewritten by hand, and both registers stay empty, which matches
  the retirement condition recorded at `tests/registers/README.md:154`.
- **The proposal named no test, tier, or gate.** §5 Testing now names tier 0 (the fragment-link,
  citation-document, and heading-walker gates over the retitle and the rewritten citations, and the
  empty-map behavior SPEC-3 relies on), tier 3 (a `set_tracing_context` example validated against
  `schemas/lenny-adapter-jsonl.schema.json` plus its reject paths and the adapter's consume-rather-than-
  relay behavior), tier 11 (the successor pointer over the §15.4 body SPEC-1 empties), and the `// spec:`
  annotation updates for the eight `slotId` tests that cite §15.4.1.
- **SPEC-2 made §28.5.3 define a ninth message type while `spec/15`'s eight-type enumeration stayed.** The
  pointer sentence at `spec/15_external-api-surface.md:1702-1707` lists the eight types the card
  schema-defines today and would be wrong once the `set_tracing_context` block lands, and it would
  contradict :1463, which already names all nine as defined in §28.5.3. SPEC-2's edit list and §7 now
  include that sentence.
- **SPEC-3's roadmap replacement sent the reader to §28.5.3 for a framing §28.5.3 attributed to the
  §15.4 sentence SPEC-1 deletes.** The `CH-MSGSOCK` Endpoint axis states the framing as a quotation:
  `spec/28_communication-channels.md:525-527` reads "[§15.4] states that the adapter communicates with the
  agent binary over stdin and stdout using newline-delimited JSON", and
  `spec/15_external-api-surface.md:1473` is the only statement of that framing in §15.4. Left alone, the
  roadmap item would point at a card that points back at an emptied section, which is the circular pointer
  §1.3 names. SPEC-4's scope now covers the framing alongside the enumeration and the `slotId` rule, its
  edit list drops the `[§15.4]` citation from the Endpoint axis so the card states the framing on its own
  authority, and §7 records the axis.
- **§5's tier-11 assurance named a pointer the gate does not count as named.**
  `TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent` evaluates each line independently and tests
  the `CH-` identifier against the same line as the §28.5 link
  (`tests/tier11_docs/successor_pointer_test.go:52, 58-59, 104-113`). `CH-MSGSOCK` sits on
  `spec/15_external-api-surface.md:1516` and the §28.5.3 link on :1517, so that pointer joins the unnamed
  set. The tier-11 paragraph now states that and names the lines in the §15.4 body that carry both on one
  line and survive SPEC-1: :1463, :1465, :1577, :1756, :2044, and :2077.

### Pass 2 (2026-08-12, automated)

- **The tier-3 case asserted that the JSONL schema rejects an empty `context`, which it accepts.**
  `$defs.set_tracing_context` requires `type` and `context` and types `context` as an object with
  `additionalProperties: true` and no `minProperties`, so `{"type":"set_tracing_context","context":{}}`
  validates (`schemas/lenny-adapter-jsonl.schema.json:210-223`). The empty frame is caught by the
  adapter's `len(frame.Context) == 0` guard (`pkg/adapter/tracingcontext.go:42-46`). The §5 bullet now
  splits the assertion across the two enforcement points and records why no `minProperties` is staged:
  the §8.3 validators are `TRACING_CONTEXT_TOO_LARGE`, `TRACING_CONTEXT_SENSITIVE_KEY`, and
  `TRACING_CONTEXT_URL_NOT_ALLOWED` (`spec/18_build-sequence.md:428`), and none of them is an emptiness
  rule, so no `minProperties` is staged. Pass 4 brings
  `schemas/lenny-adapter-jsonl.schema.json` into §7 for the optional `slotId` property alone.
- **§5 named two heading-walker gates that cannot see the retitled level-4 heading.** The walker's
  in-scope predicates are `indexedHeading`, which matches `##` and `###` headings numbered `N` or `N.M`,
  and `cardHeading`, which matches the §28.5 cards (`tests/tier0_static/heading_walker_test.go:42-43,
  47-48, 100-108`), so `#### 15.4.1` matches neither and the index carries a row for §15.4 alone
  (`spec/README.md:126`). `TestHeadingWalkerSlugMatchesTheRenderedAnchor` is a fixed case table that
  reads no specification file (:161-176). The bullet now states that the walker is unaffected and that
  no index row is required, stages a `headingSlug` case pinning
  `1541-message-format-and-binary-io-requirements`, names the fragment-link gate as the one that
  certifies the rewritten link, and §7 gains `tests/tier0_static/heading_walker_test.go`.
- **The annotation sweep omitted the tier-3 runtime SDK tests that cite §15.4.1 for content SPEC-1
  deletes.** `tests/tier3_contract/sdks/runtime_sdk_test.go:248, 262, 276, 391, 406` carry
  `// spec: 15.4.1, 15.7` for the stdin/stdout framing, the `message`/`response` round trip, the
  heartbeat acknowledgement, and the shutdown frame, all of which SPEC-1 removes from §15.4.1 and
  §28.5.3 owns (`spec/28_communication-channels.md:606-621, 773-778`). The sweep now repoints the
  `15.4.1` half of each to `28.5.3`, keeps `15.7`, and §7 gains the file.
- **§5 asserted a repository-wide search for the old slug returns nothing, contradicting §7.** The slug
  survives in `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, which
  §7 edits only to add a superseded note, and in eighteen files under
  `scripts/specshift/testdata/anchorpass/`, which are frozen gate fixtures. The sentence is scoped to the
  surface the fragment-link gate reads, `spec/` and `docs/`
  (`tests/tier0_static/fragment_link_test.go:371-373`), and both surviving sets are named as record and
  fixture that stay as written.

### Pass 3 (2026-08-12, automated)

- **§5 staged an anchor-pass case asserting behavior the loader refuses to produce.** The bullet claimed
  a case over `scripts/specshift/testdata/anchorpass/` asserting that an empty move map leaves a bare
  `§15.4.1` citation untouched. `loadMoves` returns an error for a map whose `moves` array is empty
  before it reads any citation (`scripts/specshift/anchor/moves.go:125-127`), and the fixture that case
  would use, `maps/empty-moves.json`, is already bound to the opposite assertion by
  `TestAnchorPassRejectsAMissingOrMalformedAnchorMoveMap` (`scripts/specshift/run_test.go:8373, 8385`).
  The invented case is dropped. §5 now states that the registers stay empty, that this change stages no
  anchor-pass run, and that the loader's rejection of an empty map is already pinned. SPEC-3 carries the
  same statement so the two sections agree.
- **SPEC-2 staged an adapter-stores-and-attaches mechanism into §28.5.3 that no component implements.**
  The §15.4.1 row SPEC-2 was copying says the adapter stores the tracing context and attaches it to every
  subsequent `lenny/delegate_task` request, with gateway validation when the delegation request arrives
  (`spec/15_external-api-surface.md:1494`). The adapter stores and attaches nothing: it consumes the
  frame and calls the gateway's `lenny/set_tracing_context` platform tool with the bound session id
  injected (`pkg/adapter/tracingcontext.go:25-56`). The gateway merges the identifiers into the session
  row and validates the merged result under §8.3 at registration time, refusing a child entry that would
  overwrite a parent entry (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:952-966`,
  `pkg/delegation/tracing/tracing.go:62, 110`), and it attaches the registered context when it processes
  `lenny/delegate_task` (`spec/08_recursive-delegation.md:52, 286`). Copying the row would have located
  custody of the runtime-self-reported identifiers and the §8.3 enforcement point in the in-pod adapter
  rather than in the gateway that applies them against the authenticated session, and it would have
  contradicted §8, the code, and this proposal's own tier-3 case. SPEC-2 now states the mechanism in
  those terms and records that it corrects the row. The same adapter-stores wording in the platform-tool
  table row at `spec/08_recursive-delegation.md:540` is corrected alongside it, and §7 gains the file.
  §5's tier-3 paragraph now describes `pkg/adapter/tracingcontext.go` as consuming and forwarding the
  frame.

### Pass 4 (2026-08-12, automated)

- **SPEC-2 bound `set_tracing_context` to "the session the channel is bound to", a granularity
  `CH-MSGSOCK` does not have on a multi-slot pod.** One channel carries every concurrent session's stream
  on a pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1`, and the card lists `message`,
  `tool_result`, `response`, and `tool_call` as the types that carry a `slotId` there
  (`spec/28_communication-channels.md:548-552`), while `schemas/lenny-adapter-jsonl.schema.json:210-223`
  gives `set_tracing_context` no `slotId`. The code the bullet cited does not produce a single
  registration either: the runtime's output is fanned out to every Attach subscriber
  (`pkg/adapter/socketruntime.go:340-356`), the per-slot demultiplexer passes an untagged frame through
  (`pkg/adapter/attach.go:285-291`), and each stream calls `handleSetTracingContext` with its own session
  id (`pkg/adapter/attach.go:115`), so one frame on a four-slot pod writes four session rows and merges
  one session's identifiers into every sibling's delegation lease. SPEC-2 now states that the frame
  carries the `slotId` of the session whose identifiers it declares on a multi-slot pod and none on a
  single-session pod, and that the adapter registers against the session holding that slot. The
  per-session binding is kept rather than the pod-wide fan-out, because the identifiers are per-session
  state that the gateway merges into one session row and validates against that session under §8.3
  (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:945-975`), and because reusing the card's existing
  `slotId` mechanism costs one optional field rather than a second addressing scheme. SPEC-2 stages the
  optional `slotId` property on the schema definition and the adapter drop of an untagged frame on a
  slot-serving Attach stream, SPEC-4's `slotId` bullet records that the card's type list gains
  `set_tracing_context`, §5 gains a tier-3 multi-slot case asserting one platform call against the owning
  slot's session and none for an untagged frame, and §7 gains
  `schemas/lenny-adapter-jsonl.schema.json` and `pkg/adapter/attach.go`.
- **The `pkg/adapter/attach.go` change staged above left §5's tier list saying the change reaches tier 0,
  tier 3, and tier 11 alone.** Before that edit the proposal touched no file under `pkg/`, and
  `.claude/rules/test-coverage.md` requires tier 0 and tier 1 on every touched package. `pkg/adapter`
  carries an in-process suite that pins the current pass-through of this exact frame:
  `TestAttachForwardsSetTracingContext` sends an untagged `set_tracing_context` frame and asserts the
  registration happens (`pkg/adapter/heartbeat_external_test.go:109, 128`). §5 now reads tier 0, tier 1,
  tier 3, and tier 11, and a tier-1 paragraph stages three cases in that file: no platform call for an
  untagged frame on a slot-serving stream, exactly one call against the owning stream's session id for a
  frame tagged with that stream's `slotId`, and a surviving registration on a single-session Attach, which
  reads the raw output unfiltered (`pkg/adapter/attach.go:70-73`). §7 gains
  `pkg/adapter/heartbeat_external_test.go`.

### Pass 5 (2026-08-12, automated)

- **SPEC-4 extended the §28.5.3 `slotId` type list and left the same enumeration in `spec/29` at four
  types.** `spec/29_communication-scenarios.md:1475-1477` restates the enumeration and names §28.5.3 as
  its source, so after application §28.5.3 would list five types carrying a `slotId`
  (`spec/28_communication-channels.md:549-550`) while `spec/29` still listed `message`, `tool_result`,
  `response`, and `tool_call`, one line above the citation SPEC-4 already edits. The divergence is
  operational as well, since SPEC-2 makes an untagged `set_tracing_context` a dropped frame on a
  slot-serving Attach stream (`pkg/adapter/attach.go:285-291`), and a reader working from the shorter
  list would not learn that. SPEC-4's `spec/29` bullet now extends lines 1475 through 1476 with
  `set_tracing_context`, and §7's `spec/29` entry records the enumeration alongside the two citations.

### Pass 6 (2026-08-12, automated)

- **The discard of an untagged `set_tracing_context` frame lived only in SPEC-2's rationale, while the
  tier-1 and tier-3 cases annotated `// spec: 28.5.3` for it.** SPEC-2's bullets stated what a conforming
  frame carries and staged the `pkg/adapter/attach.go` change from pass-through
  (`pkg/adapter/attach.go:283-291`) to discard, but no staged sentence in §28.5.3, in the card additions
  SPEC-4 lists, or in any docs edit said what happens to a frame that carries no `slotId` on a pod whose
  pool sets `sessionPolicy.maxConcurrentSessions > 1`. The card points the other way today, since it
  states that on a single-session pod no message carries `slotId`
  (`spec/28_communication-channels.md:548-552`). SPEC-2 now stages the outcome as a bullet of the
  `set_tracing_context` block, in the form the Messages axis already uses for the other discard path on
  this channel, where a `tool_result` whose `id` is unknown is dropped and logged as a protocol error
  (`spec/28_communication-channels.md:545-548`). The bullet is named in the block's enumerated contents
  so the tier-1 and tier-3 `// spec: 28.5.3` annotations resolve to text that carries the rule. SPEC-2
  also mirrors the rule into the operator-facing entry at `docs/reference/adapter-contract.md:310-316`,
  whose sentence at :316 repeats the adapter-stores wording SPEC-2 corrects, and §7 gains the file.

### Pass 7 (2026-08-12, automated)

- **SPEC-2 read `--max-concurrent-sessions` off a CRD field that does not exist.**
  `lennyv1.SessionPolicy` declares `Recycle` alone
  (`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:28-34`), the rendered manifest exposes `recycle`
  alone (`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:186-222`), and the sandbox reconciler reads
  only the grace and workspace-size fields off the resolved template
  (`pkg/controller/sandbox/controller.go:361-364`), so the value SPEC-2 told the builder to pass was
  unreachable and decision 5 with it. The gap is an implementation gap rather than a spec position: §5.2
  declares `sessionPolicy.maxConcurrentSessions` (`spec/05_runtime-registry-and-pool-model.md:396-397`)
  and §4.6.1 states a CEL rule `sessionPolicy.maxConcurrentSessions >= 1` on the CRD
  (`spec/04_system-components.md:426`). SPEC-2 now stages the field on `v1alpha1.SessionPolicy` with its
  kubebuilder markers, the regenerated deepcopy and CRD manifests, the corrected comment at
  `pkg/admission/pool_config_validator/validator.go:366-367`, and the extension of `sessionPolicyToCRD`
  that carries the value. The PoolScalingController remains the sole writer of `SandboxTemplate.spec.*`
  under §4.6.3 (`spec/04_system-components.md:602`), so no ownership statement changes. The claim that
  the change costs one existing-struct field and one flag is dropped, and §7 lists every site.
- **SPEC-2 required the adapter to report a discarded frame to the runtime over a channel with no frame
  that can carry it.** The adapter-to-runtime direction of `CH-MSGSOCK` is the closed set `message`,
  `tool_result`, `heartbeat`, and `shutdown` (`spec/28_communication-channels.md:539-540`), the published
  JSONL schema is a closed `oneOf` with no report type
  (`schemas/lenny-adapter-jsonl.schema.json:8-19`), and SPEC-2 staged no new inbound type, so neither the
  normative sentence nor the tier-1 and tier-3 cases could be implemented. The outcome is now stated in
  the form the Messages axis already uses for the channel's other discard path, where a `tool_result`
  whose `id` is unknown is dropped and logged as a protocol error
  (`spec/28_communication-channels.md:545-548`). No new frame, `$defs` entry, or docs inbound row is
  staged, which keeps the proposal's minimal-protocol-surface position, and the tier-1 and tier-3
  assertions are rewritten to that observable. No §16 counter is staged either: §16 declares no
  protocol-error counter for this channel, and adapter metrics sit outside the default scrape target set
  (`spec/16_observability.md:186-187`).
- **The staged rule discarded the defensive `slotId` that decision 4 and the tier-1 case required it to
  accept.** On a pod whose pool sets `maxConcurrentSessions` to 1 no session holds a slot id at all: the
  gateway reserves a slot only above 1 (`pkg/gateway/sessionserver/start.go:2111-2124`), the adapter
  client sets `SlotId` only for a non-empty slot
  (`pkg/gateway/runtime/adapterclient/client.go:205-206`), and the adapter's per-slot path keys on slot-id
  presence alone (`pkg/adapter/slot.go:37-45`), so "a `slotId` no session holds" covered every tagged
  frame there and the two tier-1 cases prescribed opposite outcomes for the same input. SPEC-2 and
  decision 4 now state that such a pod has a single addressee and that a frame naming it registers whether
  or not it carries the field. That reading is chosen over discarding because a single-addressee pod
  carries no cross-session risk, so discarding would lose advisory identifiers for a purely formal reason,
  and it keeps the tolerance decision 4 already recorded. It sits with
  `spec/28_communication-channels.md:549-551` and `spec/15_external-api-surface.md:1498`, which state what
  a conforming runtime emits rather than how the adapter treats a non-conforming frame. The contradicting
  tier-1 bullet is deleted.
- **The discard predicate keyed on how many sessions the pod was currently serving while everything else
  keyed on the declared maximum.** A pool set to 4 with one slot occupied is momentarily "a pod serving
  one session", and slots are claimed and released individually
  (`pkg/adapter/slotsession.go:104-106`), so occupancy is not a property of the pod for its life and the
  sentence justifying construction-time declaration stated a runtime-varying condition as a constant.
  Every other statement of the rule keys on the pool's configured value
  (`spec/28_communication-channels.md:548-552`, `spec/15_external-api-surface.md:1498`,
  `schemas/lenny-adapter.proto:581-583`, `pkg/gateway/session/executor/pod.go:146`). The SPEC-2 bullets,
  the construction paragraph, decision 4, the docs sentence, and the tier-1 and tier-3 case wording now
  all read `sessionPolicy.maxConcurrentSessions` above 1 or at 1.
- **No test covered the podspec, controller, and adapter-flag change, and the tier list omitted tier 2.**
  Every staged case presumed the rendered argument rather than pinning it. §5 now lists tier 2, stages
  tier-1 rendering cases over both argument lists, the unresolvable-template case, the reconciler input,
  the `sessionPolicyToCRD` mirror, and the `cmd/lenny-adapter` flag default, and stages
  `TestPoolScalingControllerMirrorsMaxConcurrentSessions` as a tier-2 envtest mirroring
  `TestPoolScalingControllerMirrorsNonceOnlyAck`
  (`tests/tier2_component/controllers/poolscaling_nonce_only_test.go:44`). §7 lists the test files.
- **SPEC-2 cited a `--sessions-root` argument the builder does not pass.** The sidecar list renders
  `--addr`, `--workspace-root`, `--staging-dir`, `--runtime-uid`, and `--runtime-socket`
  (`pkg/controller/sandbox/podspec/podspec.go:560-578`), and `--sessions-root` appears only as the
  adapter's own flag declaration (`cmd/lenny-adapter/main.go:119`). The sentence now names
  `--staging-dir` and `--runtime-socket`.
- **Only the sidecar argument list was staged, leaving an embedded pod on a concurrent pool undeclared.**
  The builder renders two adapter argument lists, `adapterArgs` at
  `pkg/controller/sandbox/podspec/podspec.go:560` and `embeddedArgs` at :661, and §5.2 names no deployment
  model in the prerequisites for `maxConcurrentSessions > 1`
  (`spec/05_runtime-registry-and-pool-model.md:429-433`). SPEC-2 now stages the argument on both lists and
  on the embedded reference runtime's declared flag set (`cmd/runtimes/echo-embedded/main.go:90-95`),
  where an undeclared controller-injected flag crashes the container at `flag.Parse`, and §5 pins both
  lists.
- **Every propagation gap failed open, because the flag defaulted to 1.** `sessionPolicyToCRD` returns nil
  for a row with no recycle scrub control (`pkg/controller/poolscaling/poolstoresource.go:156-163`),
  `resolveTemplate` returns nil on any miss (`pkg/controller/sandbox/controller.go:287-303`), and a pod is
  rendered once and never re-rendered (:539), so a four-slot pod could be told it serves one and merge one
  session's identifiers into a sibling's. SPEC-2 now states the propagation path end to end, stages the
  `sessionPolicyToCRD` change that carries the value for a pool with no recycle block, and makes the
  undeclared case fail closed: the builder renders `--max-concurrent-sessions=0`, the adapter flag
  defaults to 0, and an adapter whose concurrency is undeclared treats "no `slotId`" as naming nothing.
  §5 pins the boundary at tier 1 and tier 2.
- **The tier-1 lead counted three shipped packages while the bullets under it named five and §7 staged
  more.** The count was written for the earlier scope and went stale when the tier-1 list widened to
  `pkg/adapter`, `pkg/controller/sandbox/podspec`, `pkg/controller/sandbox`, `pkg/controller/poolscaling`,
  and `cmd/lenny-adapter`, with `pkg/apis/lenny/v1alpha1`, `pkg/admission/pool_config_validator`, and
  `cmd/runtimes/echo-embedded` staged in §7 as well. The sentence now names no count, per
  `.claude/rules/doc-style.md`.
- **The undeclared-concurrency case was pinned by tests citing §28.5.3 and §4.7 while neither staged
  section stated it.** The fail-closed reading of `0` lived in SPEC-2's rationale alone: the staged
  §28.5.3 block enumerated two states, a pool above 1 and a pool at 1, and the §4.7 entry staged the
  launch argument with no semantics for an unresolved value, so the tier-1 cases at
  `// spec: 28.5.3 (set_tracing_context slotId addressing)` and
  `// spec: 4.7 (adapter launch surface), 5.2 (maxConcurrentSessions)` cited text the application would
  not carry. This is the defect the pass-6 bullet above fixed for the discard outcome. SPEC-2 now stages
  a §28.5.3 bullet stating that an adapter whose concurrency was not declared at launch resolves no
  implicit name, so an untagged frame is dropped and logged as a protocol error while a frame carrying
  the `slotId` of a session the pod holds still registers, and the §4.7 edit now states the value the
  argument carries and the `0` sentinel. Both are named in the block's enumerated contents and in §7.

### Pass 8 (2026-08-12, automated)

- **The staged propagation never produced a declared value of 1, so every default single-session pool
  would have launched on the undeclared sentinel and dropped the frame.** The platform encodes one session
  per pod as an absent value at every layer: the store field is `omitempty` and documented as defaulting to
  1 (`pkg/gateway/runtime/runtimestore/runtimestore.go:1019-1022`), the poolscaling controller normalizes a
  non-positive value with `maxConcurrentSessionsOrOne` (`pkg/controller/poolscaling/controller.go:1244-1248`),
  and the CRD `SessionPolicy` is documented as nil on a default pool
  (`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:216-220`). The staged mirror extension widened
  `sessionPolicyToCRD` for rows above 1 alone, so a default pool mirrored no `sessionPolicy`
  (`pkg/controller/poolscaling/poolstoresource.go:156-163`), the builder rendered
  `--max-concurrent-sessions=0`, and every conforming frame on that pod, which carries no `slotId` because
  no session there holds one (`pkg/gateway/sessionserver/start.go:2111`), was dropped. That contradicted
  the block's own availability sentence, the §15.4.1 row it corrects, and the shipped case
  `TestAttachForwardsSetTracingContext_spec_15_4_1_1455` (`pkg/adapter/heartbeat_external_test.go:109`).
  SPEC-2 now separates a pool at the default of 1 from a value that did not propagate: the reconciler
  passes 1 for a Sandbox with no `spec.poolRef` (a §12.6 CreatePod pod, which holds no slot), 1 for a
  resolved template that declares no value, the declared value when the template carries one, and 0 only
  when a named pool's template does not resolve. The absent-field reading is sound because §4.6.3 makes the
  PoolScalingController the sole writer of `SandboxTemplate.spec.*` (`spec/04_system-components.md:602`) and
  the extended mirror writes the field for every row above 1. The mirror leaves the field unset at or below
  1 rather than writing `0`, which the `Minimum=1` marker would reject into the per-tuple admission backoff
  (`pkg/controller/poolscaling/controller.go:507-519`). Decision 5, the SPEC-2 propagation paragraph, the
  mirror bullet, the builder bullet, and the §4.7 launch-surface sentence all carry the same three cases.
  §5 gains the three-case reconciler assertion, a mirror case for a row at 1, and a second tier-2 envtest
  row at 1, and it records that the shipped single-session case is retained because such a pod now launches
  at 1.
- **The launch argument was staged on one of the tree's two embedded reference runtimes, so every
  `preconnect-echo` pod would have crash-looped at `flag.Parse`.** `cmd/runtimes/preconnect-echo` is the
  §6.1 SDK-warm reference runtime, is declared `deploymentModel: embedded` in the Kind agent workload
  (`tests/testinfra/kind/agent-workload.yaml:90-107`), and is therefore launched through the same
  `embeddedArgs` list (`pkg/controller/sandbox/podspec/podspec.go:661-673`). It declares the
  controller-injected flag set with the same rationale comment and then calls `flag.Parse` on the default
  `ExitOnError` parser (`cmd/runtimes/preconnect-echo/main.go:67-76`), so an unconditionally rendered
  `--max-concurrent-sessions` would exit the container before it served and break the SDK-warm arm of the
  Kind workload and the tier-7b startup-latency scaffold (`tests/tier7b_load_kind/scaffolds_test.go:380`).
  SPEC-2's artifact bullet and §7 now name both embedded mains and state the general rule that every binary
  the `embeddedArgs` path launches declares each argument that path renders unconditionally. §5 stages a
  tier-1 case over each embedded main parsing the full rendered argument list, and records that the failure
  itself is a pod-lifecycle failure only tier 5 reproduces.
- **No test pinned the served CRD schema field, though the change publishes a CRD wire contract and the
  tier list claimed tier 3.** Every tier-3 case §5 listed was over
  `schemas/lenny-adapter-jsonl.schema.json`, and the tier-2 envtest drove one in-range value, so a
  regeneration publishing the field at the wrong nesting, under the wrong type, or without the bound would
  have passed while every pod on the pool launched undeclared. The tree already carries the suite written
  for this seam on this CRD (`tests/tier3_contract/crd_schema/nonce_only_fields_test.go:6-15, 125-127`).
  §5's tier-3 paragraph now stages a case asserting the regenerated
  `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` exposes `spec.sessionPolicy.maxConcurrentSessions` as
  an integer carrying the `minimum: 1` bound, annotated
  `// spec: 4.6.1 (CRD validation), 5.2 (maxConcurrentSessions)` with a `// diagnosis:` naming the
  consequence, and §7 gains the file.
- **The undeclared-concurrency rule's registering half was staged as normative text with no listed test.**
  SPEC-2 states that an adapter whose concurrency was not declared drops a frame that carries no `slotId`
  and registers a frame that carries the `slotId` of a session the pod holds, while §5 listed only the drop
  case. An implementation reading "undeclared" as "drop everything" would have satisfied every listed case
  and still contradicted the staged sentence, disabling registration on every pool with a propagation gap.
  §5's tier-1 list now stages a sixth case in `pkg/adapter/heartbeat_external_test.go`: on a pod launched
  with `--max-concurrent-sessions=0` and serving a slot, a frame carrying that slot's `slotId` issues
  exactly one platform call against that slot's session id.

### Pass 9 (2026-08-12, automated)

- **The embedded reference mains only declared `--max-concurrent-sessions`, so every embedded pod would
  have run on the fail-closed sentinel.** In the embedded model the reference main is the adapter: it
  constructs `adapter.New(version)` and copies each parsed launch argument onto the `adapter.Server` by
  hand, with nothing picked up implicitly (`cmd/runtimes/echo-embedded/main.go:110-135`,
  `cmd/runtimes/preconnect-echo/main.go:87-107`). A flag that is declared and never assigned leaves the
  field at `0`, which SPEC-2 defines as the undeclared sentinel, so every embedded pod would have dropped
  every conforming frame, including a default single-session pool where the builder renders
  `--max-concurrent-sessions=1` and no session holds a `slotId`. That contradicted SPEC-2's
  availability bullet, since both embedded reference runtimes are `integrationLevel: basic`
  (`tests/testinfra/kind/agent-workload.yaml:77, 104`), and `preconnect-echo` is admitted only at
  `maxConcurrentSessions` of 1 (`spec/05_runtime-registry-and-pool-model.md:434`), so it never carries a
  `slotId` at all. SPEC-2's artifact bullet and §7 now state that both mains assign the parsed value onto
  the `adapter.Server`, following the same declare-and-assign pattern they apply to `--workspace-root`,
  `--staging-dir`, `--mcp-socket`, and `--gateway-grpc-addr`, and SPEC-2 stages the field on
  `pkg/adapter/server.go`. §5 keeps the parse case and adds an assignment case over each main, since a
  declare-and-ignore implementation satisfies the parse case alone.
- **A §12.6 `CreatePod` pod always names a pool, so the first resolution case was misattributed and
  unreachable.** `CreatePod` takes a pool id, rejects an empty one, and stamps `sb.Spec.PoolRef` on every
  Sandbox it writes (`pkg/podregistry/crd.go:314-323`); the only other constructor is the
  WarmPoolController, which sets `PoolRef` from the pool it warms
  (`pkg/controller/warmpool/controller.go:522-524`); and the CRD marks the field
  `+kubebuilder:validation:Required` (`pkg/apis/lenny/v1alpha1/sandbox_types.go:34-35`). The staged case
  therefore named a state no path produces, while a gateway-created pod whose named pool has no
  `SandboxWarmPool` or template reference fell onto the `0` sentinel unremarked. Decision 5, SPEC-2's
  resolution list, the staged §4.7 sentence, and the tier-1 assertion now state the same three reachable
  cases, a resolved template that sets the field, a resolved template that leaves it absent, and a pool or
  template that does not resolve, and SPEC-2 states the fail-closed outcome of the third case as the
  intended reading rather than an accident, on the deny-on-doubt rule the proposal already applies to the
  sentinel.
- **§5 omitted tier 5 while its own prose named tier 5 as the only tier that reproduces the failure.**
  SPEC-2 re-renders the container argument list of every agent pod on both deployment models
  (`pkg/controller/sandbox/podspec/podspec.go:560, 661`) and regenerates the chart CRD manifest, which
  `.claude/rules/test-coverage.md:37` maps to tier 5, and the embedded mains parse with the default
  `flag.ExitOnError` parser, so an undeclared argument exits the container before it serves. §5's lead
  sentence now lists tier 5 with its reason, a tier-5 paragraph stages the assertion over `TestPodLifecycle`
  (`tests/tier5_e2e_kind/pod_lifecycle_test.go:32`) and the workload's two embedded-model runtimes
  (`tests/testinfra/kind/agent-workload.yaml:80, 107`) plus the served `SandboxTemplate` field, and the
  contradicting sentence under the tier-1 embedded bullet is removed. §7 gains the tier-5 file.
- **SPEC-1's opening deletion had no determinate extent, and one reading removed the `prompt` note that
  has no destination.** The subsection opens with one line carrying three sentences
  (`spec/15_external-api-surface.md:1473`): the stdin/stdout JSON Lines framing, the
  one-JSON-object-per-line rule, and the `prompt` removal note. §28.5.3 states the first two
  (`spec/28_communication-channels.md:525-527, 541-542`) and states no equivalent of the third, which a
  grep for `prompt` over `spec/28_communication-channels.md` confirms. SPEC-1's bullet now names the two
  sentences it deletes and records that the `prompt` note stays on the same both-legs ground the
  `input_required` note stays on, the §1.2 table gains a row classifying the note, and SPEC-1's stays list
  names it.
- **No listed test covered the `sessionPolicyToCRD` branch that already returns a block.** The function
  returns nil for a row with no scrub control and a `SessionPolicy{Recycle: ...}` block for a row that has
  one (`pkg/controller/poolscaling/poolstoresource.go:161-170`), while every listed tier-1 and tier-2 case
  drove a row with `recycle.enabled: false`, so an implementation extending only the nil-returning branch
  passed them all. A concurrent recycling pool, which §5.2 lists as a supported configuration
  (`spec/05_runtime-registry-and-pool-model.md:425`), takes the untested branch, mirrors a template with no
  `maxConcurrentSessions`, and launches every pod declaring 1 while serving N slots, which is the
  cross-session merge SPEC-2 exists to close. SPEC-2's mirror bullet now states that the value is carried
  on both outcomes, §5 stages a tier-1 case over a row at `maxConcurrentSessions: 4` with
  `recycle.enabled: true` and `scrubProfile: vm-restart`, and the tier-2 envtest gains the same row.

### Pass 10 (2026-08-12, automated)

- **No listed test pinned the `Minimum=1` guard on the `sessionPolicyToCRD` branch that can emit a
  rejected value, and that branch is the ordinary recycling pool.** After the staged change the
  nil-returning branch returns a block only for a row above 1, so the branch that already returns a block
  is the only one that can present `0` to the API server, and every listed case drove it at 4
  (`pkg/controller/poolscaling/poolstoresource.go:157-169`). That branch is reached by an ordinary
  recycling pool at default concurrency, because the store row defaults `MaxConcurrentSessions` to unset
  (`pkg/gateway/runtime/runtimestore/runtimestore.go:1019-1022`) while a recycle scrub control is what
  makes the function return a block. An implementation carrying the store value verbatim there, or
  declaring the CRD field without `omitempty`, would have passed every listed case and written
  `maxConcurrentSessions: 0` for every recycling pool at default concurrency, which the API server rejects
  as Invalid and which puts the tuple into the per-tuple admission backoff where `syncTuple` swallows the
  error and skips the tuple (`pkg/controller/poolscaling/controller.go:580, 588-590`), stalling every
  field of that pool's `SandboxTemplate` because the controller replaces the whole spec on each apply
  (:609-610). SPEC-2's mirror bullet now states that the guard holds by construction on both outcomes
  through the `json:"maxConcurrentSessions,omitempty"` tag, the CRD type bullet states the tag, §5's
  tier-1 list gains a case over a row with a recycle scrub control and `maxConcurrentSessions` unset or at
  1 with a `// diagnosis:` naming the backoff stall, and the tier-2 envtest gains the same row.
- **The tier-3 SDK annotation sweep omitted `runtime_sdk_test.go:372`, which cites §15.4.1 for the
  tool-call contract SPEC-1 deletes.** `TestRuntimeSDKWorkspaceHelpers` is annotated
  `// spec: 15.4.1, 15.7 (runtime SDK workspace helpers)` and its diagnosis names §28.5.3 as the owner of
  the adapter-local tool helpers it exercises. The only §15.4.1 statements about tool calls are the
  `tool_call` row of the outbound table (`spec/15_external-api-surface.md:1491`) and the `tool_result` row
  of the inbound table (:1480), both inside the tables SPEC-1 deletes, so the case would have been left
  mapped to a subsection that no longer states the behavior it pins. §5's sweep list now reads
  `tests/tier3_contract/sdks/runtime_sdk_test.go:248, 262, 276, 372, 391, 406` and records why :372 is in
  the set.

### Pass 11 (2026-08-12, automated)

- **The mirror's guard could not hold by construction, so SPEC-2 prescribed the implementation its own
  tier-1 case is written to fail.** The mirror bullet stated that no branch-local check was needed because
  the `json:"maxConcurrentSessions,omitempty"` tag elides a store row at or below 1, while §5's tier-1 case
  requires the mirrored block to carry no `maxConcurrentSessions` for a row at 1. `omitempty` elides the
  zero value alone, and an explicit 1 is a reachable store state: `ValidateSessionPolicy` rejects a
  negative value and normalizes nothing (`pkg/gateway/runtime/poolstore/poolstore.go:551-553`), and
  `maxConcurrentSessionsOrOne` normalizes the controller's derived sizing value rather than the stored row
  (`pkg/controller/poolscaling/controller.go:1244-1248`), so a verbatim assignment on either outcome of
  `sessionPolicyToCRD` (`pkg/controller/poolscaling/poolstoresource.go:156-170`) would mirror
  `maxConcurrentSessions: 1` for a pool at the §5.2 default. The mirror bullet now states the guard as an
  explicit predicate on both outcomes, setting the CRD field only when the store row's value is above 1
  and leaving it at its zero value otherwise, and records `omitempty` as the serialization consequence of
  that zero value. §5's tier-1 and tier-2 cases already required that outcome and are unchanged.

### Pass 12 (2026-08-12, automated)

- **SPEC-4 left the second Messages-axis citation, which attributes the one-JSON-object-per-line rule to
  a §15.4 sentence SPEC-1 deletes.** SPEC-4's Messages-axis bullet reached the citation at
  `spec/28_communication-channels.md:541` alone, while a second `[§15.4]` citation at
  `spec/28_communication-channels.md:545` closes the sentence group that states the per-line rule
  (541-542), the `MessagePart` input and output arrays (542-543), and the minimal schemas of `heartbeat`
  and `shutdown` (543-544). SPEC-1 deletes the only statement of the per-line rule in §15.4
  (`spec/15_external-api-surface.md:1473`), so applying the proposal as written would leave §28.5.3
  sourcing that rule to a section that no longer states it, which is the dangling pointer §1.3 exists to
  remove and the defect pass 1 fixed for the Endpoint axis. SPEC-4 now drops that citation too, so the
  card states the group on its own authority. Splitting the group to keep an attribution for its
  surviving halves was rejected: §15.4's minimal-schema statement lives at the `MessageEnvelope`
  carve-out, which already points to §28.5.3 for those schemas
  (`spec/15_external-api-surface.md:1577`), so a retained citation would point back at a section pointing
  here.

## 7. Files touched on application

- `spec/15_external-api-surface.md`, for SPEC-1, SPEC-3 including the §15.4.5 roadmap sentence, and the
  §15.4 message enumeration SPEC-2 completes.
- `spec/28_communication-channels.md`, for SPEC-2 and the SPEC-4 citation edits on the Endpoint, Messages,
  and Exclusivity axes, in §28.6, and in the §28.8 row.
- `spec/29_communication-scenarios.md`, for the two `slotId` citations SPEC-4 repoints and for the
  `slotId` type enumeration at lines 1475 through 1476 that SPEC-4 extends with `set_tracing_context`.
- `spec/08_recursive-delegation.md`, for the `lenny/set_tracing_context` platform-tool row at line 540,
  whose adapter-stores wording SPEC-2 corrects to the registration-and-attachment mechanism.
- `docs/reference/adapter-contract.md`, for the `set_tracing_context` entry at lines 310 through 316,
  whose adapter-stores wording SPEC-2 corrects and which gains the `slotId` addressing rule and the
  discard outcome.
- `schemas/lenny-adapter-jsonl.schema.json`, for the optional `slotId` property SPEC-2 adds to the
  `set_tracing_context` definition.
- `pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go`, for the `MaxConcurrentSessions` field on
  `SessionPolicy` and the doc comment that records the field as poolstore-only, with
  `pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go`,
  `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml`, and
  `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml` regenerated rather than hand-edited.
- `pkg/controller/poolscaling/poolstoresource.go`, for the `sessionPolicyToCRD` mirror that carries the
  poolstore value onto the CRD, including for a row with no recycle scrub control.
- `pkg/admission/pool_config_validator/validator.go`, for the comment stating that the CRD carries no
  `maxConcurrentSessions` field.
- `pkg/controller/sandbox/podspec/podspec.go` and `pkg/controller/sandbox/controller.go`, for the
  `MaxConcurrentSessions` input the builder renders as `--max-concurrent-sessions` on both the sidecar and
  the embedded argument lists, and `cmd/lenny-adapter/main.go`, `cmd/runtimes/echo-embedded/main.go`, and
  `cmd/runtimes/preconnect-echo/main.go` for the flag that receives it. Both embedded reference mains
  declare it, because the builder renders the argument onto every pod the `embeddedArgs` path launches and
  an undeclared flag exits the container at `flag.Parse`, and both assign the parsed value onto the
  `adapter.Server` they construct, since in the embedded model that server is the adapter that reads it.
- `pkg/adapter/server.go`, for the declared-concurrency field on `Server` that the sidecar adapter and
  both embedded mains assign from the flag.
- `spec/04_system-components.md` §4.7, for the launch argument the adapter now takes, the three cases the
  reconciler resolves its value from, and the `0` sentinel that declares the pod's concurrency undeclared.
- `pkg/adapter`, for resolving a `set_tracing_context` frame to the session it names and dropping and
  logging a frame that names none.
- `pkg/adapter/heartbeat_external_test.go`, `pkg/controller/sandbox/podspec/podspec_test.go`,
  `pkg/controller/sandbox/controller_test.go`, `cmd/runtimes/echo-embedded/main_test.go`,
  `cmd/runtimes/preconnect-echo/main_test.go`, the `pkg/controller/poolscaling` fake-client suite, and a
  new `tests/tier2_component/controllers/` envtest, for the tier-1 and tier-2 cases, per §5.
- `tests/tier5_e2e_kind/pod_lifecycle_test.go`, for the Kind case over the rendered argument list and the
  served `SandboxTemplate` field, per §5.
- `schemas/examples/jsonl.set_tracing_context.json` (new), `tests/tier0_static/schemas_test.go`,
  `tests/tier3_contract/crd_schema/` for the served `sessionPolicy.maxConcurrentSessions` case, and the
  tier-3 JSONL contract cases, per §5.
- `pkg/gateway/runtime/adapterclient/client_test.go`, `pkg/gateway/session/executor/pod_test.go`,
  `tests/tier10_conformance/concurrent_slot_conformance_test.go`, and
  `tests/tier3_contract/sdks/runtime_sdk_test.go`, for the `// spec:` annotation updates.
- `tests/tier0_static/heading_walker_test.go`, for the `headingSlug` case pinning the retitled §15.4.1
  slug, per §5.
- `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, for a superseded
  note recording that decision 1 replaces its retirement premise and that its label rule stands on the
  enclosing-section derivation rather than on that premise.

`tests/spec-anchor-moves.json` and `tests/registers/anchor-senses.yaml` are not touched, per SPEC-3.
