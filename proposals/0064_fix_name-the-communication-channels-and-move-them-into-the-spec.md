# Proposal: Name the communication channels, move their contract into the specification, and rewrite every reference by script

- **Status:** Draft for review.
- **Date:** 2026-07-27.
- **Scope:** The first three steps of `gateway-runtime-comms-remediation.md`, which are the foundation
  every later remediation step depends on. The plan's tooling step is included whole rather than split,
  because the migrations below cannot run without it and it shares one validator and one register contract
  with the gates. Step one gives every communication channel between the gateway, the
  agent pod, the adapter, and the runtime a single canonical identifier under a stated naming law, and
  retires the collision in which two unrelated mechanisms are both called a lifecycle channel. Step two
  creates `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md` as the normative
  home for the channel contract and the end-to-end traces, so the knowledge in
  `gateway-runtime-comms.md` stops being re-derived from source on every question. It also builds the
  tooling that performs both migrations mechanically. This proposal enumerates no edit sites. Every
  reference in code, tests, schemas, charts, and documentation is located and rewritten by scripts this
  proposal specifies, and completeness is proven by gates rather than by review.

This document stages the proposed specification, code, and test changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Two documents at the repository root are the source material, and an implementor should read both before
starting. This proposal is deliberately shorter than either, and it does not restate them.

**`gateway-runtime-comms.md`** maps every communication channel between the gateway, the agent pod, the
adapter, and the runtime, traces each end-to-end scenario, and records thirty-two verified gaps. It was
built from independent per-surface and per-scenario derivations and then adversarially verified, and
seventeen of twenty load-bearing claims required correction during that pass. Its status vocabulary is
load-bearing and this proposal adopts it: `WIRED` means reachable from production code, `UNWIRED` means
implemented with no production caller, and `ABSENT` means specified and not implemented. Section 3 is the
channel inventory this proposal names, section 6 holds the gap records, and section 8 records what the
analysis could not establish.

**`gateway-runtime-comms-remediation.md`** sequences twenty-five steps that close those records. This
proposal implements its first three steps: the tooling step, the channel identification and naming step,
and the specification enhancement step. The plan carries material this proposal does not duplicate and an
implementor will want: the full naming table with every channel's name today and after, the invariants
governing the specification changes, the wave ordering, the serialization rules, and the gate catalogue.
Where this proposal says "the naming table" or "the register contract", the plan is where the detail
lives.

**Why these steps come first.** Every one of the remaining twenty-two steps names a channel, cites a
contract card, and registers a claim. None of those exist today. The plan's critical path begins here for
that reason, and the concurrency it later sustains depends on the decomposition and the gates this
proposal delivers. A rename performed after later steps land costs more with each one, because the
identifier spreads into new packages, new services, new flags, and their tests at every tier.

**What this proposal deliberately does not do.** It closes no capability gap and changes no runtime
behavior. Its output is a vocabulary, a normative home for the contract, and the tooling and gates that
keep both true. Judged on its own it looks like overhead. Judged as the substrate for twenty-two steps
that would otherwise each pay the re-derivation cost that produced the reference document, it is the
cheapest part of the sequence.

## 1. Problem

`gateway-runtime-comms.md` maps every communication channel between the gateway, the agent pod, the
adapter, and the runtime, and records thirty-two verified gaps. Producing it required reading the source,
because the specification does not carry the information in a form a reader can use. Three problems block
every remediation step that follows.

**1. The channels have no names.** The reference had to invent a flat `C1` through `C22` list to discuss
them. That list mixes three different kinds of thing: `C1` and `C7` are whole gRPC services, `C2` through
`C6` are individual RPCs carried on `C1`, and four entries are shared database or cache state with no live
counterparty. The reference's own exclusivity table writes "not applicable" five times because a single
column cannot mean one thing across those three kinds.

**2. Two unrelated mechanisms share a name.** The adapter-to-runtime Unix-socket JSON Lines channel
(`pkg/adapter/lifecyclechannel.go`) and the gateway-to-adapter gRPC streaming RPC
(`schemas/lenny-adapter.proto`, handler `pkg/adapter/controlchannel.go`) are both called a lifecycle
channel. They run between different participants, in different directions, over different transports. The
collision has already produced incorrect normative text: the shipped
`schemas/lenny-adapter-jsonl.schema.json` describes intra-pod frames as riding the gRPC stream, and both
halves of that description are wrong. This proposal's own analysis had to correct the same conflation
twice before it held.

**3. The contract is scattered, and one authoritative sentence is false.** Answering how the gateway talks
to the pod requires reading across `spec/04`, `spec/07`, `spec/10`, `spec/13`, and `spec/15`. The
architecture diagram at `spec/03_high-level-architecture.md` renders the whole surface as
`Gateway <--mTLS--> Pods (gRPC control protocol)`, which names one protocol where several run and asserts
mTLS the rendered podspec does not provide on either container.

A fourth problem constrains how the first three can be fixed. The repository carries **15,377
`§X line N` citations across 2,353 Go files**. Any specification change that shifts a line invalidates
them, and `spec/10` §10.1 begins at line 3, so inserting a heading near the top of that file moves almost
every citation into it. Hand-editing is not available at this scale, and neither is leaving the citations
stale.

## 2. Decisions

- **The taxonomy separates three classes.** A **link** (`LNK-`) is a transport connection between two
  participants. A **channel** (`CH-`) is a typed conversation carried on one link. A **register** (`REG-`)
  is shared state mediating two participants with no live connection. Every column in the registers then
  means exactly one thing, which the flat list made impossible.

- **Dial direction and authority direction are recorded separately.** The gateway dials the gRPC control
  stream while the adapter originates every message on it. Collapsing those into one "direction" column is
  the root of the naming collision rather than a symptom of it, so the register carries both.

- **Identifiers are mnemonic, never positional.** `CH-EVENTSTREAM`, not `C6`. A channel inserted between
  two others must not renumber its neighbours, and an engineer has to be able to say the identifier out
  loud.

- **Two words are reserved.** Neither `lifecycle` nor `control` may appear as a bare noun phrase anywhere
  in `spec/`, `docs/`, `schemas/`, or a Go doc comment. Both may appear inside a canonical identifier.
  This is the rule that prevents the collision from reforming.

- **New specification sections append; nothing is renumbered.** `spec/` ends at `27_web-playground.md`, so
  `spec/28` and `spec/29` are additive and no existing section number changes.

- **Every reference is rewritten by script, and completeness is proven by a gate.** This proposal names no
  edit sites. For each class of change it specifies the script, the register that drives it, and the gate
  that fails if a reference is missed. Measured counts appear only to convey scale.

- **Line citations are retired in favour of anchors.** The forward-looking citation form names a heading
  (`§28.5.2 CH-EVENTSTREAM`) rather than a line. An anchor survives an insertion above it; a line number
  does not. A ratchet gate keeps the retired population from regrowing.

- **A section that gives up content names its successor in prose.** A reader arriving at a reduced section
  by a stale reference lands on a pointer rather than on adjacent text they might mistake for the answer.

## 3. Design overview

### 3.1 What lands

Two new specification files, one naming law, three registers, and the tooling that migrates every
reference to them.

`spec/28_communication-channels.md` is the normative home. It carries the naming law and taxonomy, the
three registers, the contract cards grouped by participant edge, the exclusivity model, and the
wire-contract artifact register. Grouping the cards by edge is what makes the unbuilt adapter-to-gateway
direction a visible block rather than two rows lost in a twenty-two row table.

`spec/29_communication-scenarios.md` carries the end-to-end traces, each written as a numbered step list
naming channels by identifier.

Existing sections keep their subjects and link to `spec/28` for the channel contract. `spec/03` keeps its
diagram with the false line corrected and a pointer added. `spec/15` §15.4 is reduced to the wire-artifact
pointer it already claims to be.

### 3.2 The two forced renames

Of the twenty-two entries in the reference inventory, twenty are a specification and documentation change
only. Two carry a code and wire rename, because the colliding word is load-bearing on machine-readable
surfaces that prose cannot reach: normative field tables, the adapter manifest emitter, three runtime SDK
public APIs, an adapter flag, a gRPC method name, and the third-party runtime author contract.

`CH-EVENTSTREAM` is the adapter-authored control event stream, which the gateway dials and the adapter
pushes on. `CH-RUNTIMEOPS` is the adapter-to-runtime operations channel, which the adapter listens on and
the runtime dials. Naming both makes the direction and the participants legible from the identifier alone.

Two further entries carry a text correction inside a shipped wire artifact without a rename.

### 3.3 Why the renames must happen now

Every later remediation step names a channel. Renaming before those steps is a text substitution over
today's tree. Renaming after them is a rename across new packages, new services, new flags, and their
tests at every tier. The cost multiplies with each step that lands first, which is why this proposal is
the first of the sequence rather than a cleanup at the end.

### 3.4 The migration is script-driven

This is the load-bearing design decision, and it follows from scale. The changes divide into four classes,
each with a script, a register that drives it, and a gate that proves completeness.

| Class | Driven by | Performed by | Proven by |
|:--|:--|:--|:--|
| Reserved-word removal from prose | the naming law | `scripts/specshift` name pass | the naming lint, which fails on any bare reserved noun phrase |
| Identifier rename across code, schemas, SDKs, charts, and docs | the naming table in §28.3 | `scripts/specshift` identifier pass | the identifier-resolution gate, which fails when an identifier resolves to more than one spelling |
| Section-anchor redirect for relocated content | `tests/spec-anchor-moves.json` | the citation resolver, then the `specshift` anchor pass | tier 0 fails an entry whose successor anchor does not exist |
| Line-citation retirement | `tests/registers/line-citations.yaml` | `scripts/specshift` line pass | the line-citation ratchet, which fails a file whose count rises |

No list of edit sites appears in this proposal, and none should appear in the applied change. A list is
stale the moment a step merges, and a reviewer cannot verify one at this scale. A gate can.

### 3.5 Sequencing inside the proposal

Four sub-steps, in order. The tooling is first because the later three depend on it.

1. **Tooling.** `scripts/specshift`, the citation resolver, the heading walker, the naming lint, the
   line-citation ratchet, and the register contract they share.
2. **Naming law, registers, and prose.** The law, the three registers, the reserved-word removal, and the
   `spec/03` correction. No wire surface changes.
3. **The wire contract change.** The two renames that reach the proto, the manifest, the SDKs, the flag,
   and the runtime author contract, applied as one exclusive change so no other work contends with it.
4. **The new sections, and the anchor and citation rewrite.** `spec/28` and `spec/29`, the reductions with
   their successor pointers, and the mechanical retirement of the redirected anchors and the line
   citations.

## 4. Detailed design

### 4.1 The naming law

Seven rules, normative in §28.1.

- **N1.** A channel's canonical name states the endpoint pair and the plane, in that order. It never
  states the transport, because the transport is a column in the register.
- **N2.** Identifiers are mnemonic, uppercase, and hyphenated. Positional identifiers are not used.
- **N3.** `lifecycle` and `control` are reserved and may not appear as bare noun phrases in `spec/`,
  `docs/`, `schemas/`, or a Go doc comment.
- **N4.** One identifier per channel, everywhere: the Go package or file name stem, the proto RPC name
  stem, the metric label value, and the test name fragment for a test scoped to one channel. A gate or a
  test spanning channels is named for the invariant it enforces and carries no channel identifier.
- **N5.** A link identifier and the channel identifiers it carries share no stem, so a search for one
  never returns the other.
- **N6.** A register names the store and the key, never a verb.
- **N7.** A flag, environment variable, or manifest key naming a channel uses that channel's identifier in
  lowercase kebab or snake form.

`.claude/rules/channel-naming.md` states the same rules for future agents, so a conforming name is the
default rather than a lint finding after the fact.

### 4.2 The five axes

Each channel records: dial direction, authority direction, plane, transport, boundary, and exclusivity.
Transport and boundary are closed sets, so a new value requires a specification change rather than an
undeclared extension. Exclusivity records the granularity and the enforcing guard, or names the guard as
missing. That last field is what turns the reference's exclusivity findings into a maintained property.

### 4.3 The registers and the contract cards

The three registers carry one row per entry with a provenance column. The contract cards sit in §28.5,
grouped by boundary, one subsection per boundary value, each opening with a one-edge figure and holding
its cards under a fixed field template with a per-card line cap. The cap is deliberate: a card that cannot
state its contract in the budget is a signal the contract is unclear, not a reason to widen the budget.

The citable handle is the card heading plus the identifier, which is stable across insertions.

### 4.4 The claim register

Every normative statement about this surface carries a row in a claim register with a status drawn from
the reference's vocabulary: `WIRED` for a mechanism reachable from production code, `UNWIRED` for one
implemented with no production caller, and `ABSENT` for one specified and not implemented. A `WIRED` row
names the production surface. A row that is not `WIRED` names the step that will close it.

This is what stops the specification asserting mechanisms that do not run, which is the defect class
behind a third of the reference's records. It also gives the later steps their work queue.

### 4.5 Successor pointers

When a section gives up content, it keeps a sentence naming the channel identifiers that moved and the
heading that now owns them. The sentence is normative and permanent.

It is not redundant with the anchor-redirect map. That map is consumed by the citation resolver and is
emptied once the rewrite completes, while the prose pointer serves a reader arriving by a route no tool
rewrites: a citation the mechanical pass missed, a section number quoted in a proposal document, a commit
message, or a code review. A reduced section still exists and still discusses adjacent material, so a
reader who lands on it can take the remaining prose for the answer.

### 4.6 The line-citation ratchet

The resolver validates that an existing line citation still points inside its section. It does not stop a
new one being written. Without a second gate the anchor convention is documentation, the retirement
happens once, and the population regrows.

The ratchet is a tier-0 Go test rather than a linter rule, because the repository's lint invocation is
downgraded to a warning and a check whose script is absent passes silently. Its baseline is a per-file
count. A file absent from the register fails on its first line citation, and a file present fails when its
count rises. A count that falls rewrites the register downward, so retirement is recorded. When every
count reaches zero the register is empty and the gate becomes a flat prohibition.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it is documented |
|:--|:--|:--|
| A reference is missed by the identifier rewrite | The identifier-resolution gate fails at tier 0, naming the file. The rename is not complete until the gate is green, so a miss blocks the merge rather than shipping | §28.1 states that one identifier resolves to one spelling tree-wide |
| A reader follows a stale section number to a reduced section | The section names its successor heading and the identifiers that moved | The successor-pointer sentence, normative in the reduced section |
| A third-party runtime reads the renamed manifest key | The runtime author contract is updated in the same change, and the manifest emitter and the three SDKs move together, so a runtime built against the published SDK stays correct | The runtime author guide, amended in the wire-contract sub-step |
| A deployer pins an older runtime image built against the old manifest key | The key rename is a breaking change to the runtime contract and is not compatibility-shimmed, per the repository's no-backward-compatibility rule for a pre-deployment platform | Stated in Non-goals |
| The naming lint cannot land green because prose violations remain | The violations are enumerated into an exception register, each naming the step that retires it. The gate never widens and suppression is not used | The register contract, shared by every gate in the remediation plan |
| A line citation is written after the retirement | The ratchet fails the file at tier 0 | §28.1 and `.claude/rules/` |
| Content moves again after this proposal | The successor pointer names the heading and the identifiers rather than a line, so it survives a further move | §28.1 |

## 6. Proposed changes

### TOOL-1. The migration and gate tooling

**Target:** `scripts/specshift`, `cmd/lenny-test`, `tests/tier0_static/`, `tests/registers/`.

**Change.** Build `scripts/specshift` with four passes: a name pass that removes reserved bare noun
phrases from prose, an identifier pass that rewrites a channel identifier across code, schemas, SDKs,
charts, and documentation, an anchor pass that rewrites a retired section anchor to its successor, and a
line pass that rewrites or retires a line citation. Each pass is driven by a register file and carries a
dry-run mode whose output is the entry criterion for applying it. `scripts/specshift` ships with its own
`run_test.go`.

Build the citation resolver, the heading walker, the naming lint, the identifier-resolution gate, and the
line-citation ratchet as Go tests under `tests/tier0_static/` or as checks in the map validator, because
those are the two channels the repository hard-gates. A gate delivered as a shell script under `scripts/`
is not durable here: the repository's lint invocation is downgraded to a non-fatal warning, several tier-0
checks are non-fatal, and a set of checks pass silently when their script is absent. One documented
enforcement location in the tree names a script that does not exist, which is the same failure this whole
remediation addresses, occurring inside the test infrastructure.

Build the shared register contract every gate in the remediation plan uses, with an entry schema carrying
a subject, a verdict, an owner, an opened-at date, an expiry, a blocker, and a reason, and with three
ratchet rules: an unregistered violation fails, a passed expiry fails, and a blocker that does not resolve
to an open item fails. The pattern already exists in tree in two pending-list files and is generalized
here rather than invented.

Add the remaining tooling the plan's step three carries, which is included in this proposal because it
shares the validator, the register contract, and the same authors:

- **Change-graph completeness**, so a change that should have propagated to a derived artifact cannot pass
  unnoticed.
- **An `UNVERIFIED` verdict state**, so a check that could not reach a conclusion is distinguishable from
  one that passed. The verdict type already carries a third state with a promotion switch, so this is one
  constant and one branch rather than new machinery.
- **The additional `tests/spec-map-exceptions.yaml` fields and the reason class** the new specification
  sections need to register a heading whose implementation is pending.
- **An AST-based skip-reason classifier.** The existing convention is implemented in a downgraded shell
  script whose allow pattern matches any skip regardless of reason, so the convention is unenforced today.
  Parsing the syntax tree rather than the string is what makes it real.
- **A gate-integrity meta-gate** asserting that no gate this proposal adds can be disabled by deleting a
  script.

**Rationale.** Every later sub-step is a mechanical rewrite over thousands of sites. Without the tooling
the rewrites are hand edits, and without the gates their completeness is unverifiable. The tooling is kept
whole rather than split because these pieces share one validator, one register contract, and one set of
tests, and because splitting them would put a hard dependency across a sign-off boundary for no gain.

### SPEC-1. The naming law, the registers, and the prose correction

**Target:** `spec/28_communication-channels.md` §28.1 through §28.4 (new), `spec/03_high-level-architecture.md`,
and the reserved bare noun phrases wherever they appear in `spec/` and `docs/`.

**Change.** Write §28.1 through §28.4: the naming law, the taxonomy and its axes, and the three registers
with the full inventory. Correct the `spec/03` diagram line, which names one protocol where several run
and asserts mTLS the rendered podspec does not provide, and add a pointer to §28. Remove the reserved bare
noun phrases from specification and documentation prose by script.

Add `.claude/rules/channel-naming.md` stating N1 through N7.

**Not in scope for this sub-step.** The Go file and symbol renames, which belong to the wire-contract
sub-step so exactly one change moves each file. Metric label renames, which are deferred to the step that
first makes those metrics observable, with a claim-register row naming it.

### SPEC-2. The wire contract change

**Target:** `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, the normative field
tables that name the colliding key, the adapter manifest emitter, the three runtime SDKs, the adapter
flag, and the runtime author guide.

**Change.** Rename the two colliding channels to their canonical identifiers across every machine-readable
surface, by script, driven by the naming table in §28.3. Correct the two shipped wire artifacts whose
descriptions are wrong, including the JSON Lines schema description that attributes intra-pod frames to
the gRPC stream.

Apply as one exclusive change on a quiesced tree. While it is in flight no other change edits an adapter
handler file or the adapter proto, because a rename and a concurrent edit to the same file produce a
conflict that resolves silently in the wrong direction.

**Rationale.** The colliding word is load-bearing on surfaces prose cannot reach, and the runtime author
contract instructs third-party authors to read the key by name. Leaving the machine-readable surfaces
ambiguous while renaming only prose produces a worse state than either alternative.

### SPEC-3. The new sections, the reductions, and the successor pointers

**Target:** `spec/28_communication-channels.md` §28.5 through §28.7 (new),
`spec/29_communication-scenarios.md` (new), `spec/15_external-api-surface.md` §15.4, `spec/04_system-components.md` §4.7,
and the sections that link to §28 rather than describing the contract themselves.

**Change.** Write the contract cards grouped by participant edge, the exclusivity and concurrency model,
and the wire-contract artifact register derived mechanically from the schemas directory rather than
hand-enumerated. Write §29 as end-to-end traces naming channels by identifier.

Reduce `spec/15` §15.4 to the wire-artifact pointer it already claims to be, and reduce the `spec/04` §4.7
channel prose, in both cases leaving a successor pointer naming the identifiers that moved and the heading
that now owns them. Ship `tests/spec-anchor-moves.json` mapping each retired anchor to its successor, so
the citation resolver stays green until the rewrite runs.

Seed the claim register from the reference document's status tables.

### SPEC-4. Anchor and line-citation retirement

**Target:** every `// spec:` citation in the tree, and `tests/spec-map.json`.

**Change.** Run the anchor pass to rewrite each redirected citation to its successor, and empty
`tests/spec-anchor-moves.json`. Run the line pass to convert line citations to anchor citations, driving
every per-file count in the line-citation register to zero. Break the oversized multi-contract paragraph
that five later steps would otherwise contend over into separately addressable blocks.

Apply as one exclusive change, scheduled after the wire-contract sub-step, executed by `scripts/specshift`
with a proven dry run as the entry criterion. No other change's output depends on this one, because every
step that needs a citable anchor cites a `spec/28` card, which lives in a new file and needs no in-file
surgery.

**Rationale.** This is a very large diff with no judgement in it. Its risk is concentrated entirely in the
tooling, which is why the tooling ships first with its own tests and a dry-run gate.

### TEST-1. Gates

**Target:** `tests/tier0_static/`, the map validator, and `tests/tier11_docs/`.

**Change.** Add the naming lint enforcing the reserved-word ban and the one-identifier rule across `spec/`,
`docs/`, `schemas/`, and Go doc comments. Add the identifier-resolution gate asserting each canonical
identifier resolves to exactly one spelling across the tree. Add the citation resolver asserting every
remaining line citation resolves inside its section, and the line-citation ratchet asserting no file
acquires a new one. Add the heading walker asserting every numbered heading carries a map entry. Add a
tier-11 check asserting each reduced section carries a successor pointer whose named heading resolves.

Each gate lands green by enumerating today's violations into a named register with an owner and an expiry,
never by widening the gate and never by suppression.

**Rationale.** Each gate closes the loop on one class of rewrite. Without them the completeness of a
several-thousand-site mechanical change rests on review, which cannot verify it.

## 7. Non-goals

- **Closing any capability gap.** This proposal builds no channel, wires no consumer, and changes no
  runtime behavior. The reference document's records are closed by later steps, which this proposal
  exists to unblock.
- **Renumbering or moving existing specification sections.** New sections append. The only existing
  sections that change are those giving up content, and each keeps a successor pointer.
- **A compatibility shim for the renamed manifest key.** The platform is pre-deployment and the repository
  rule forbids compatibility paths. The key rename is breaking for a runtime built against the old
  contract, and the SDKs and author guide move with it.
- **Metric label renames.** Deferred to the step that first makes the adapter metrics observable, with a
  claim-register row naming that step.
- **Deleting the three comments describing a kubelet-path handler that does not exist.** They are the only
  in-tree description of that mechanism. They become seed rows in the claim register with status `ABSENT`,
  and the step that owns the mechanism either implements it or removes them.
- **Absorbing `gateway-runtime-comms.md` wholesale into the specification.** The reference is a
  point-in-time analysis with code evidence. The specification carries the contract, and code evidence
  lives in the claim register, which is gated.

## 8. Findings closed on application

This proposal closes no record from `gateway-runtime-comms.md` section 6 by itself, other than the
specification and shipped-artifact halves of the record about wire-contract artifacts being misdescribed.
It also discharges, as a standing mechanism rather than as a one-time fix, the section 8 item about the
test harness being unable to detect an unreachable surface; the plan's closure step discharges the item
itself once every gate is populated.
Its function is to make the remaining records closable: every later step names a channel, cites a card,
and registers a claim, none of which exist today.

## 9. Open decisions for review

1. **Whether the tooling ships in this proposal or as its own.** It is included here because the three
   migration sub-steps cannot run without it and because splitting it puts a hard dependency across a
   sign-off boundary. The argument for splitting is that the tooling is testable on its own and touches no
   specification text.

2. **Whether the wire-contract rename is in scope now.** Renaming later costs more with every step that
   lands first. Renaming now makes this proposal a breaking change to the runtime author contract. The
   planning material treated the deferral as the more expensive option, and this proposal follows it.

3. **The per-card line budget in §28.5.** A cap forces clarity and risks truncating a genuinely complex
   contract. The budget is stated so a reviewer can judge it against the first cards written.

4. **Whether `spec/29` is a separate file or a subsection of `spec/28`.** Separate keeps each file
   scannable and matches the modularity goal. Combined keeps one file to consult.

## 10. Files touched on application

- `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, both new.
- `spec/03_high-level-architecture.md`, `spec/04_system-components.md`, and
  `spec/15_external-api-surface.md`, for the diagram correction, the reductions, and the successor
  pointers.
- Specification and documentation files carrying reserved bare noun phrases, rewritten by script.
- `schemas/lenny-adapter.proto` and `schemas/lenny-adapter-jsonl.schema.json`, for the renames and the two
  incorrect descriptions.
- The adapter manifest emitter, the adapter flag, and the three runtime SDKs, rewritten by script.
- `docs/runtime-author-guide/`, for the renamed manifest key.
- Every file carrying a `// spec:` citation, rewritten by script.
- `scripts/specshift`, `cmd/lenny-test`, `tests/tier0_static/`, `tests/registers/`,
  `tests/spec-anchor-moves.json`, and `tests/spec-map.json`.
- `.claude/rules/channel-naming.md`, new.

The specific files inside each script-driven class are the script's output rather than this document's
content, and the gates in TEST-1 are what prove the class is complete.
