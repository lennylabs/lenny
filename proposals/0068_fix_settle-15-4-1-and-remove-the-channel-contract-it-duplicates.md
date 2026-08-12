# Proposal: Settle §15.4.1 and remove the channel contract it duplicates

- **Status:** **Approved (2026-08-12) by jaf sign-off.** Verified (2026-08-12), converged after 8
  adversarial review rounds (13 findings fixed) across three full-pool sweeps, the certifying sweep
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

4. **§15.4.1 is retitled.** Once the adapter-to-binary contract is gone, "Adapter↔Binary Protocol" names
   content the section no longer holds, while §28.5.3 owns it. A reader looking for that protocol would
   land on a section named for it that does not contain it.

5. **The four `MessageEnvelope` labels stay at §15.4.** 0064 rewrote them from §15.4.1 to §15.4 on the
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

- Delete the framing sentence opening the subsection.
- Delete the inbound message table and its `**Inbound messages...**` lead.
- Delete the outbound message table and its `**Outbound messages...**` lead.
- Delete the `**`slotId` for concurrent-session multiplexing:**` paragraph.

The subsection's existing successor pointer to §28.5.3 stays and is the sentence N8 requires. The
`input_required` note, the stderr statement, and the flushing requirement with its per-language table stay.
The Translation Fidelity Matrix and the `MessageEnvelope` format are level-4 siblings of §15.4.1 rather
than content inside it (§1.2), and SPEC-1 does not reach them.

### SPEC-2. Complete §28.5.3 with the type it never defined

In `spec/28_communication-channels.md`, inside §28.5.3's Message schemas (`spec/28_communication-channels.md:586`),
add an `**Outbound: `set_tracing_context`**` block carrying the payload, the `slotId` addressing rule and
the outcome of a frame that omits it, the tier availability, and the §8.3 and §16.3 pointers that
§15.4.1's row states today (`spec/15_external-api-surface.md:1494`). The block states the mechanism in
the terms the implementation and §8 use rather than copying the row's adapter-side wording:

- The runtime writes the frame on `CH-MSGSOCK`. On a pod whose pool sets
  `sessionPolicy.maxConcurrentSessions > 1` the frame carries the `slotId` of the session whose
  identifiers it declares, and on a pod that sets that value to 1 it carries no `slotId`.
- On a pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1`, a `set_tracing_context` frame that
  carries no `slotId` names no session, and the adapter discards it without registering any identifiers
  and without returning an error to the runtime. The card states the outcome of an unaddressable message
  in the same form for a `tool_result` whose `id` is unknown
  (`spec/28_communication-channels.md:545-548`).
- The adapter consumes the frame rather than relaying it onward, and registers the identifiers with the
  gateway against the session holding that slot, by calling the `lenny/set_tracing_context`
  platform tool with that session's id injected (`pkg/adapter/tracingcontext.go:25-56`).
- The gateway merges the identifiers into the session row and validates the merged result under §8.3 at
  registration time, and a child entry cannot overwrite or remove a parent entry
  (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:952-966`, `pkg/delegation/tracing/tracing.go:62,
  110`).
- The gateway attaches the registered context to the child's delegation lease when it processes
  `lenny/delegate_task` (`spec/08_recursive-delegation.md:52, 286`).
- The frame is available at all tiers, including Basic, because the adapter issues the platform call
  itself rather than routing it through the runtime's MCP client.

`set_tracing_context` therefore joins `message`, `tool_result`, `response`, and `tool_call` in the card's
`slotId` sentence (`spec/28_communication-channels.md:548-552`), which enumerates the types that carry a
`slotId` on a multi-slot pod. The identifiers the frame declares are per-session state: the gateway merges
them into one session row and validates the merged result against that one session under §8.3
(`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:945-975`), so a frame with no addressee has no
defined target on a pod running several sessions at once. The card already states a mechanism for
per-session frames on this multiplexed channel, and reusing it costs one optional field rather than a
second addressing scheme. Two artifacts follow from that choice:

- `schemas/lenny-adapter-jsonl.schema.json` gains an optional `"slotId": { "type": "string" }` property on
  its `set_tracing_context` definition (`schemas/lenny-adapter-jsonl.schema.json:210-223`), matching the
  property the `message`, `tool_call`, `tool_result`, and `response` definitions already carry (:58, :139,
  :161, :190).
- `pkg/adapter/attach.go` drops a `set_tracing_context` frame that carries no `slotId` on an Attach stream
  serving a slot, rather than registering it. Today the runtime's output stream is fanned out to every
  Attach subscriber (`pkg/adapter/socketruntime.go:340-356`), the per-slot demultiplexer passes an
  untagged frame through so the per-session heartbeat path still observes it
  (`pkg/adapter/attach.go:285-291`), and each stream then calls `handleSetTracingContext` with its own
  session id (`pkg/adapter/attach.go:115`), so one untagged frame on a four-slot pod writes four session
  rows and merges one session's identifiers into every sibling's delegation lease. Dropping the untagged
  frame on a slot-serving stream fails closed and keeps one frame to one registration, and the discard
  bullet above is the staged §28.5.3 sentence that states that outcome, so the tier-1 and tier-3 cases in
  §5 annotate to a section that carries the rule. A single-session
  pod is unaffected, because its Attach stream reads the raw output unfiltered
  (`pkg/adapter/attach.go:70-73`) and its frames carry no `slotId`.

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
rule and the discard outcome, so a runtime author learns that an untagged frame on a multi-slot pod
registers nothing.

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
decision 5.

In `spec/28_communication-channels.md`:

- §28.5.3's Endpoint axis attributes the stdin/stdout newline-delimited JSON framing to §15.4 (lines 525
  through 527), which is the sentence SPEC-1 deletes (`spec/15_external-api-surface.md:1473`). The
  `[§15.4]` citation is dropped and the card states the framing on its own authority, keeping the §4.7
  citation for the abstract-socket sentence that follows. The card is the destination, on the both-legs
  rule, and the §15.4.5 roadmap item SPEC-3 writes then resolves to a section that states the framing.
- §28.5.3's Messages axis cites §15.4 for the enumeration it owns (line 541). The pointer is removed, since
  the card states the messages itself.
- The same axis's `slotId` sentence (lines 548 through 552) drops its `[§15.4]` citation and keeps the
  `[§5.2]` one. Its list of the types that carry a `slotId` gains `set_tracing_context`, per SPEC-2.
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
multi-slot pod, which SPEC-2 makes a dropped frame.

## 4. Non-goals

- **Renumbering §15.4.2 through §15.4.6.**
- **Widening §28.2's boundary set to admit an external-client edge.**
- **Moving the Translation Fidelity Matrix or `MessageEnvelope`.**

## 5. Testing

The change reaches tier 0, tier 1, tier 3, and tier 11.

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

**Tier 1 (unit).** SPEC-2's `pkg/adapter/attach.go` change is a behavioral change to a shipped package, so
the in-process suite over that package pins the new rule. The cases go in
`pkg/adapter/heartbeat_external_test.go`, which already holds the in-process coverage of this frame, and
each carries `// spec: 28.5.3 (set_tracing_context slotId addressing)`.

- Over an Attach stream serving a slot, a `set_tracing_context` frame carrying no `slotId` issues no
  platform call.
- Over an Attach stream serving a slot, a frame carrying that stream's `slotId` issues exactly one
  platform call, against that stream's session id.
- Over a single-session Attach, which reads the raw output unfiltered (`pkg/adapter/attach.go:70-73`), a
  frame carrying no `slotId` still registers, so `TestAttachForwardsSetTracingContext`
  (`pkg/adapter/heartbeat_external_test.go:109`) keeps holding as written.

**Tier 3 (contract).** SPEC-2 makes §28.5.3 the owning statement of the `set_tracing_context` frame that
`schemas/lenny-adapter-jsonl.schema.json:210-223` publishes and that `pkg/adapter/tracingcontext.go:36-56`
consumes and forwards to the gateway's `lenny/set_tracing_context` platform tool, and nothing ties the
three together today.

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
  (delegation validation)`, over an adapter serving two active slots: a `set_tracing_context` frame
  carrying the `slotId` of one slot produces exactly one platform call, against that slot's session id
  and no other, and a frame carrying no `slotId` produces no platform call at all. The case pins the
  granularity SPEC-2 states, which the fan-out in `pkg/adapter/socketruntime.go:340-356` would otherwise
  leave to the number of attached streams.

  The two rejection paths are asserted at the two enforcement points the tree actually has. The schema
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
heartbeat acknowledgement, and the shutdown frame, all of which SPEC-1 deletes from §15.4.1 and §28.5.3
owns (`spec/28_communication-channels.md:606-621, 773-778`). The `15.4.1` half of each of those
annotations is repointed to `28.5.3` and the `15.7` half is kept:
`tests/tier3_contract/sdks/runtime_sdk_test.go:248, 262, 276, 391, 406`. Each of those cases asserts
message-loop behavior rather than the stdout flushing requirement that stays in §15.4.1, so none of them
retains a `15.4.1` citation. The diagnosis prose on each already names §28.5.3 as the owner, so the
annotations follow it.

## 6. Files touched on application

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
  `set_tracing_context` definition, and `pkg/adapter/attach.go`, for the drop of an untagged
  `set_tracing_context` frame on a slot-serving Attach stream.
- `pkg/adapter/heartbeat_external_test.go`, for the tier-1 cases over that drop, per §5.
- `schemas/examples/jsonl.set_tracing_context.json` (new), `tests/tier0_static/schemas_test.go`, and the
  tier-3 contract cases, per §5.
- `pkg/gateway/runtime/adapterclient/client_test.go`, `pkg/gateway/session/executor/pod_test.go`,
  `tests/tier10_conformance/concurrent_slot_conformance_test.go`, and
  `tests/tier3_contract/sdks/runtime_sdk_test.go`, for the `// spec:` annotation updates.
- `tests/tier0_static/heading_walker_test.go`, for the `headingSlug` case pinning the retitled §15.4.1
  slug, per §5.
- `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, for a superseded
  note recording that decision 1 replaces its retirement premise and that its label rule stands on the
  enclosing-section derivation rather than on that premise.

`tests/spec-anchor-moves.json` and `tests/registers/anchor-senses.yaml` are not touched, per SPEC-3.

## 7. Resolved in adversarial review

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
  contradict :1463, which already names all nine as defined in §28.5.3. SPEC-2's edit list and §6 now
  include that sentence.
- **SPEC-3's roadmap replacement sent the reader to §28.5.3 for a framing §28.5.3 attributed to the
  §15.4 sentence SPEC-1 deletes.** The `CH-MSGSOCK` Endpoint axis states the framing as a quotation:
  `spec/28_communication-channels.md:525-527` reads "[§15.4] states that the adapter communicates with the
  agent binary over stdin and stdout using newline-delimited JSON", and
  `spec/15_external-api-surface.md:1473` is the only statement of that framing in §15.4. Left alone, the
  roadmap item would point at a card that points back at an emptied section, which is the circular pointer
  §1.3 names. SPEC-4's scope now covers the framing alongside the enumeration and the `slotId` rule, its
  edit list drops the `[§15.4]` citation from the Endpoint axis so the card states the framing on its own
  authority, and §6 records the axis.
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
  `schemas/lenny-adapter-jsonl.schema.json` into §6 for the optional `slotId` property alone.
- **§5 named two heading-walker gates that cannot see the retitled level-4 heading.** The walker's
  in-scope predicates are `indexedHeading`, which matches `##` and `###` headings numbered `N` or `N.M`,
  and `cardHeading`, which matches the §28.5 cards (`tests/tier0_static/heading_walker_test.go:42-43,
  47-48, 100-108`), so `#### 15.4.1` matches neither and the index carries a row for §15.4 alone
  (`spec/README.md:126`). `TestHeadingWalkerSlugMatchesTheRenderedAnchor` is a fixed case table that
  reads no specification file (:161-176). The bullet now states that the walker is unaffected and that
  no index row is required, stages a `headingSlug` case pinning
  `1541-message-format-and-binary-io-requirements`, names the fragment-link gate as the one that
  certifies the rewritten link, and §6 gains `tests/tier0_static/heading_walker_test.go`.
- **The annotation sweep omitted the tier-3 runtime SDK tests that cite §15.4.1 for content SPEC-1
  deletes.** `tests/tier3_contract/sdks/runtime_sdk_test.go:248, 262, 276, 391, 406` carry
  `// spec: 15.4.1, 15.7` for the stdin/stdout framing, the `message`/`response` round trip, the
  heartbeat acknowledgement, and the shutdown frame, all of which SPEC-1 removes from §15.4.1 and
  §28.5.3 owns (`spec/28_communication-channels.md:606-621, 773-778`). The sweep now repoints the
  `15.4.1` half of each to `28.5.3`, keeps `15.7`, and §6 gains the file.
- **§5 asserted a repository-wide search for the old slug returns nothing, contradicting §6.** The slug
  survives in `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, which
  §6 edits only to add a superseded note, and in eighteen files under
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
  table row at `spec/08_recursive-delegation.md:540` is corrected alongside it, and §6 gains the file.
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
  slot's session and none for an untagged frame, and §6 gains
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
  reads the raw output unfiltered (`pkg/adapter/attach.go:70-73`). §6 gains
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
  `set_tracing_context`, and §6's `spec/29` entry records the enumeration alongside the two citations.

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
  whose sentence at :316 repeats the adapter-stores wording SPEC-2 corrects, and §6 gains the file.
