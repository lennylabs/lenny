# Proposal: Name the communication channels, move their contract into the specification, and rewrite every reference by script

- **Blocked on:** `proposals/0065_new_build-the-specification-migration-tooling-and-its-gates.md`, which
  must be applied, implemented, and green in the tree before this proposal is applied. The tooling
  sub-step that was part of this proposal now lives there, for the sequencing reason §3.5 states. Applying
  this proposal against a tree without `scripts/specshift` and the registers means hand-editing the sites
  this document deliberately does not enumerate, which is how both earlier application attempts failed.
- **Status:** **Approved (2026-07-31) by jaf sign-off.** Verified (2026-07-30), converged after 10
  adversarial review rounds (16 findings fixed) across 4 full-pool sweeps, the certifying sweep running
  every lens complete with zero confirmed findings. This supersedes the sign-off of 2026-07-29, which was
  withdrawn after the application that followed it did not converge; the split that followed is what
  reconciled this proposal with the pipeline that applies it. **Sign-off does not authorize application
  before 0065 lands**, which the prerequisite above states, and application now stops of its own accord
  against a tree without it, because every sub-step here is a run of a pass 0065 builds. The following decisions stand as staged: the wire-contract rename is in
  scope now, accepting that it breaks the runtime author contract, because deferring it costs more with
  every later step that lands first; and `spec/29` is a separate file from `spec/28`. The §28 and §29
  line budgets the plan states are sizing indicators and are left as written. The convergence record
  below lists every pass and the findings it fixed.
- **Date:** 2026-07-27.
- **Scope:** The first three steps of `gateway-runtime-comms-remediation.md`, which are the foundation
  every later remediation step depends on. The plan's tooling step was originally included here and is now
  proposal 0065, which this proposal depends on, because the pipeline applies specification edits before it
  writes code and the migrations below are that tooling's output. Step one gives every communication channel between the gateway, the
  agent pod, the adapter, and the runtime a single canonical identifier under a stated naming law, and
  retires the collision in which two unrelated mechanisms are both called a lifecycle channel. Step two
  creates `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md` as the normative
  home for the channel contract and the end-to-end traces, so the knowledge in
  `gateway-runtime-comms.md` stops being re-derived from source on every question. The tooling that performs both migrations
  mechanically is built by proposal 0065. This proposal enumerates no edit sites. Every
  reference in code, tests, schemas, charts, and documentation is located and rewritten by scripts this
  proposal specifies, and completeness is proven by gates rather than by review.

This document stages the proposed specification, code, and test changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

**Entry criterion.** Proposal 0065 must be applied, implemented, and green before any sub-step below is
applied. It builds `scripts/specshift` and its four passes, the registers each pass consumes, and the gates
that prove a rewrite complete. Every sub-step below is a run of one of those passes over a register rather
than a hand edit, so applying this proposal against a tree that lacks them means hand-resolving sites this
document deliberately does not enumerate. Two application attempts made before the split did exactly that
and neither converged.

Two documents at the repository root are the source material, and an implementor should read both before
starting. This proposal is shorter than either, and it does not restate them.

**`gateway-runtime-comms.md`** maps every communication channel between the gateway, the agent pod, the
adapter, and the runtime, traces each end-to-end scenario, and records thirty-two verified gaps. It was
built from independent per-surface and per-scenario derivations and then adversarially verified, and
seventeen of twenty claims the analysis rests on required correction during that pass. Its status
vocabulary carries exact meanings and this proposal adopts it: `WIRED` means reachable from production code, `UNWIRED` means
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
proposal adds. A rename performed after later steps land costs more with each one, because the
identifier spreads into new packages, new services, new flags, and their tests at every tier.

**What this proposal does not do.** It closes no capability gap and changes no runtime
behavior. Its output is a vocabulary, a normative home for the contract, and the tooling and gates that
keep both true. Judged on its own it looks like overhead. Judged as the substrate for twenty-two steps
that would otherwise each pay the re-derivation cost that produced the reference document, it costs less
than the alternative.

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
channel. They run between different participants, in different directions, and over different transports.
The collision has already produced incorrect normative text: the shipped
`schemas/lenny-adapter-jsonl.schema.json` describes intra-pod frames as riding the gRPC stream, and both
halves of that description are wrong. The glossary entry that owns the term
(`docs/reference/glossary.md` lines 206 through 209) carries the same conflation, and it is the page a
reader consults to resolve the ambiguity. This proposal's own analysis had to correct the same conflation
twice before it held.

**3. The contract is scattered, and one authoritative line collapses it.** Answering how the gateway talks
to the pod requires reading across `spec/04`, `spec/07`, `spec/10`, `spec/13`, and `spec/15`. The
architecture diagram at `spec/03_high-level-architecture.md` line 29 renders the whole surface as
`Gateway ←──mTLS──→ Pods (gRPC control protocol)`, which names one protocol where several run and one
arrow where several channels do. The mTLS half of that line is a normative requirement the specification
states elsewhere and this proposal keeps: `spec/10_gateway-internals.md` line 190 lists the gateway-to-pod
hop as mTLS plus a projected service account token, NET-060 at `spec/10_gateway-internals.md` line 321
requires symmetric SAN validation in both directions, `spec/15_external-api-surface.md` line 1456 states
gateway-to-pod communication over gRPC and mTLS, and `spec/04_system-components.md` line 641 titles the
adapter contract as an mTLS API. The agent podspec does not yet mount the certificate material those rules
require, which is an implementation gap the reference document records and a later remediation step closes,
so it becomes a claim-register row with status `ABSENT` rather than a specification edit here.

A fourth problem constrains how the first three can be fixed. The repository carries **15,376 citations
of the `§X line N` and `§X lines N-M` forms across 2,353 Go files**, and a further **3,666 across 264
non-Go files**, which sit
mostly under `charts/` (103 files) and `migrations/` (110 files) and appear in the SQL (`-- spec:`) and
YAML (`# spec:`) comment dialects, in YAML block scalars, and in JSON string values. Any specification
change that shifts a line invalidates them wherever they are carried,
and `spec/10` §10.1 begins at line 3, so inserting a heading near the top of that file moves almost
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

- **Identifiers are mnemonic rather than positional.** A channel is named `CH-ADAPTEREVENTS` instead of
  `C6`. A channel inserted between two others must not renumber its neighbours, and an engineer has to be
  able to say the identifier out loud. The identifier is a mnemonic for the conversation, and the endpoint
  pair, the plane, the two directions, and the transport are register columns, so an identifier is never
  read as the authoritative statement of any of them.

- **Two words are reserved, and no identifier reuses a bound term.** Neither `lifecycle` nor `control` may
  appear as a bare noun phrase anywhere in `spec/`, `docs/`, `schemas/`, or a Go doc comment. Both may
  appear inside a canonical identifier and inside a markdown anchor identifier, which is an addressable
  link target rather than prose. Separately, an identifier stem may not reuse a term the
  specification already binds to an unrelated mechanism. Together these are the rules that prevent the
  collision from reforming.

- **C6 is `CH-ADAPTEREVENTS`.** The naming table in `gateway-runtime-comms-remediation.md` §3.4
  originally gave C6 the identifier `CH-EVENTSTREAM`. This proposal's review renamed it, and the plan's
  naming table now carries `CH-ADAPTEREVENTS` together with a provenance note recording the rename and
  prohibiting the retired stem. The plan's R1b scope table also carries the current stem, so no plan row
  is corrected here. SPEC-2 states the carrier spellings for `CH-ADAPTEREVENTS` under N3 and N4 so that
  every carrier of the identifier is spelled from the rules rather than inferred. The rename was made
  because "event stream" is already bound in this specification to the operational event stream
  (`spec/README.md` line 154, `spec/25_agent-operability.md` §25.5), to the per-session event stream
  (`spec/07_session-lifecycle.md` line 286), and to `pkg/ops/events/service.go`. Under N4 the identifier
  becomes a Go file stem and a metric label value, so adopting it would seed a second collision of the
  class this proposal exists to retire. `CH-ADAPTEREVENTS` names the author of the messages and is unbound in the tree. Every other row
  of the plan's naming table is adopted unchanged.

- **New specification sections append; nothing is renumbered.** `spec/` ends at `27_web-playground.md`, so
  `spec/28` and `spec/29` are additive and no existing section number changes. `spec/README.md` is a
  hand-maintained table of contents with no generator, so it gains top-level and subsection rows for
  `spec/28` and `spec/29` and revised rows for the reduced sections in the same change.

- **Every reference is rewritten by script, and completeness is proven by a gate.** This proposal names no
  edit sites. For each class of change it specifies the script, the register that drives it, and the gate
  that fails if a reference is missed. Measured counts appear only to convey scale.

- **Line citations are retired in favour of anchors.** The forward-looking citation form names a heading
  (`§28.5.2 CH-ADAPTEREVENTS`) rather than a line. An anchor survives an insertion above it; a line number
  does not. A ratchet gate keeps the retired population from regrowing. The convention is normative as
  N8 in §28.1, so the gates and their tests cite a rule the specification states.

- **Measured populations are scale indicators, and every enumerated class carries a residual.** A count
  or a file list in this proposal states what was measured, at a commit, with a reproducing command. It
  does not define a class and nothing keys off it: every gate, exit criterion, and register baseline
  computes the number it needs when it runs. An enumeration lists the members known at writing and seeds
  the class's residual register; the class itself is defined by a deliberately broad predicate, and
  anything matching that predicate which is in neither the enumeration nor that register is a residual
  that fails tier 0 by name. A residual is closed by registering the member as in-class or as an explicit
  exclusion with a reason, never by widening a gate. See §4.7.

- **A section that gives up content names its successor in prose.** A reader arriving at a reduced section
  by a stale reference lands on a pointer rather than on adjacent text they might mistake for the answer.

## 3. Design overview

### 3.1 What lands

This proposal lands two new specification files, one naming law, three registers, and the tooling that
migrates every reference to them.

`spec/28_communication-channels.md` is the normative home. It carries the naming law and taxonomy, the
three registers, the contract cards grouped by participant edge, the exclusivity model, the wire-contract
artifact register, and the failure and degradation matrix. Grouping the cards by edge is what makes the
unbuilt adapter-to-gateway direction a visible block rather than two rows lost in a twenty-two row table.
The artifact register is also what supersedes the `schemas/README.md` artifact table, which stands for the
same set and names a strict subset of it. The `spec/24` compliance-suite sentence is corrected in place
rather than superseded, because it states the artifact subset the external-adapter compliance suite asserts
against rather than the register's whole artifact set, and today that subset omits the runtime-ops events
schema. The corrected sentence states the artifact set the suite is required to assert against. The
shipped harness compiles two schema files, so the distance between the corrected sentence and the code is
recorded as an `ABSENT` claim-register row under §4.4 rather than closed here. The degradation matrix is the one subsection with no antecedent in the tree, so it is authored here
rather than relocated. §4.8 fixes every heading in both files, with the anchor each title produces, because
three separate obligations in this proposal need those anchors before the sections that carry them exist.

`spec/29_communication-scenarios.md` carries the end-to-end traces, each written as a numbered step list
naming channels by identifier, together with the off-holder matrix. The matrix is keyed by session-scoped
client route, and each row states what happens when the replica serving that route is not the replica
holding the session's pod control stream. The client-to-gateway session REST surface is not a channel in
the §28 register, so no §28 card owns it and this matrix is where the specification states its required
behavior.

Existing sections keep their subjects and link to `spec/28` for the channel contract. `spec/03` keeps its
diagram with the collapsed protocol line corrected and a pointer added. `spec/15` §15.4 gives up its
channel-contract prose, which is the normative-ownership sentence in the §15.4 preamble, and the
§15.4.1 subsection, and keeps the
wire-artifact pointer it already claims to be, which is the artifact list the preamble opens with together
with the release-tag versioning, breaking-change, and reference-implementation sentences that state the
compatibility contract for those same artifacts. The SDK-warm demotion contract states an obligation on the
runtime author's adapter, so it stays in §15.4 with the subsections below it. Its remaining subsections keep their subjects.
§15.4.2 RPC Lifecycle State Machine states the adapter's own state machine and the version-negotiation
handshake on it, which is an obligation on the adapter implementation, and §15.4.3 Runtime Integration
Levels, §15.4.4 Sample Echo Runtime, §15.4.5 Runtime Author Roadmap, and
§15.4.6 Conformance Test Suite state the runtime-author contract rather than a channel contract. §28
has no heading that owns either. Those five headings, their anchors, and their subjects
survive the reduction, and no content moves out of them. Their prose is still rewritten in place by the
name pass, the identifier pass, and the anchor pass, as SPEC-3 states.

### 3.2 The two forced renames

Of the twenty-two entries in the reference inventory, twenty are a specification and documentation change
only. Two carry a code and wire rename, because the colliding word is embedded in machine-readable
surfaces that prose cannot reach: normative field tables, the adapter manifest emitter, three runtime SDK
public APIs, an adapter flag, a gRPC method name, and the third-party runtime author contract.

`CH-ADAPTEREVENTS` is the adapter-authored event stream on the gRPC control plane, which the gateway
dials and the adapter pushes on. `CH-RUNTIMEOPS` is the adapter-to-runtime operations channel, which the
adapter listens on and the runtime dials. Each identifier is a mnemonic that distinguishes the two
conversations. The participants, the dial direction, and the authority direction are read from the
register rows in §28.3 rather than from the identifier.

Two further entries carry a text correction inside a shipped wire artifact without a rename, and the
glossary entry that owns the colliding term states the wrong mechanism. SPEC-2 stages those three by hand,
together with the artifact-scope sentence at `spec/15_external-api-surface.md` line 1463, which describes
one of the same artifacts from the specification side and makes the mirrored error.
The wrong-mechanism class is not bounded, and this proposal no longer claims it is. The colliding
phrase is two-valued inside `spec/` as well, and a set of normative sentences already attribute the
intra-pod channel's frames to the gateway. `spec/07_session-lifecycle.md` line 324 and
`spec/15_external-api-surface.md` line 1755 both say the gateway "sends an interrupt signal on the
lifecycle channel" and waits for `interrupt_acknowledged`, while `spec/04_system-components.md` line 702
defines that channel as an abstract Unix socket the runtime dials and the adapter listens on, and the
gateway's actual hop is the unary `Adapter.Interrupt` RPC with the adapter then writing `interrupt_request`
on the intra-pod socket. `spec/05_runtime-registry-and-pool-model.md` line 540 says "The gateway is
notified via the lifecycle channel" for slot failure, which is an adapter-to-gateway event. These
sentences are tolerable today because the phrase is ambiguous, and a substitution makes each of them
precise and false. They are corrected by hand in SPEC-1, and the class the naming register must resolve is
every prose site rather than a fixed list.

### 3.3 Why the renames must happen now

Every later remediation step names a channel. Renaming before those steps is a text substitution over
today's tree. Renaming after them is a rename across new packages, new services, new flags, and their
tests at every tier. The cost multiplies with each step that lands first, which is why this proposal is
the first of the sequence rather than a cleanup at the end.

### 3.4 The migration is script-driven

This design decision follows from scale. The changes divide into the classes
below. Each script-driven class names a script, a register that drives it, and a gate that proves
completeness. The hand-authored classes are those where no substitution rule produces the content, and
each of those names its edit sites explicitly. One class is neither: a derived artifact is regenerated
from the source the passes rewrite rather than edited, and its gate is the existing no-drift test.

| Class | Driven by | Performed by | Proven by |
|:--|:--|:--|:--|
| Reserved-word removal from prose | `tests/registers/reserved-phrase-senses.yaml`, keyed by file and occurrence, which maps each reserved-phrase site to the canonical identifier that replaces it, drawn from the whole §28 identifier space of links, channels, and registers rather than from the channel register alone | `scripts/specshift` name pass, which fails a site with no register entry rather than substituting a default and which excludes markdown anchor identifiers per N3 | the naming lint, which fails on any bare reserved noun phrase outside a markdown anchor identifier, reading the same exclusion the pass reads |
| Identifier rename across code, schemas, SDKs, charts, and docs | the naming table in §28.3, plus `tests/registers/identifier-senses.yaml`, keyed by file and occurrence, carrying an entry for every occurrence of a retired spelling whose site the pass cannot prove is the channel, which covers both a spelling the table maps to more than one channel and a single-channel spelling appearing at a site that is not a channel | `scripts/specshift` identifier pass, which fails a site with no register entry rather than substituting a default | the identifier-resolution gate, which fails when an identifier resolves to more than one spelling and which reads a retired spelling per context so an occurrence the register records as not a channel does not fail it, and the tier-0 assertion over the `coordinatorHoldAllowedMethods` entries in `pkg/adapter/holdstate.go`, that an entry whose service part is `lenny.adapter.v1.Adapter` names a method or stream `Adapter_ServiceDesc` declares and an entry whose service part is another service names a method of a service the adapter registers |
| Section-anchor redirect in spec citations | `tests/spec-anchor-moves.json` | the citation resolver, then the `specshift` anchor pass | the anchor pass itself, which aborts non-zero before any write on a citation naming a retired anchor with no map entry and on a map entry whose successor heading does not exist, with those cases in TEST-1, and the anchor class's residual check, which fails tier 0 on a retired anchor left in the tree with no residual-register entry |
| Markdown cross-reference redirect in `spec/` and `docs/` | `tests/spec-anchor-moves.json` | the `specshift` anchor pass, extended to every intra-repo markdown fragment link the fragment-link gate reads, which is a link whose target is a tracked `.md` file or the citing page itself, so the same-page `[...](#anchor)` form is inside the pass as well as the file-qualified `[...](NN_file.md#anchor)` form | the fragment-link gate, which fails any intra-repo markdown fragment link that resolves to neither an existing heading slug nor an existing explicit kramdown anchor attribute |
| Line-citation retirement wherever the retired citation form §4.6 states appears, in every spelling that form covers, which includes the section-level spelling (`§10 line 437`), the multi-member spellings whatever separates the members (`§4.8 lines 1057-1058, 1077`, `§10.7 line 694 / line 743`, `§10 line 437 ("...") + line 443 ("...")`, and `§10.6 line 601, line 629`), the qualified spelling (`§11.7 item 3 line 364`) and the trailing-gloss spelling (`§7.3 line 408 step (e)`), the en-dash and em-dash range spellings (`§4.4 lines 263–291`), the path-form spelling (`spec/04_system-components.md line 1145`), the colon spelling in both its variants (`§17.6:404` and `spec/15_external-api-surface.md:1315`), and any of those spellings wrapped across two comment lines, each consumed whole | `tests/registers/line-citations.yaml`, keyed per file | `scripts/specshift` line pass, with the straddling range citations §4.6 enumerates hand-corrected in SPEC-4 because the pass fails them rather than guessing an anchor | the line-citation ratchet, which fails a file whose count rises |
| Regeneration of a derived artifact whose source the passes rewrite | the generated-artifact denylist in `scripts/specshift` | `make generate`, `make generate-proto`, `go generate ./pkg/gateway/mcpfabric/mcptools/...`, `go run ./cmd/lenny-chart-schema-gen`, `go run ./cmd/lenny-ocsf-mapping-gen`, the hand-applied CRD post-generation re-application, and the chart-to-embedded CRD re-copy, run as the exit criterion of the pass that touched the source | `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4`, `TestEmbeddedCRDsAreCopiesOfChartManifests_spec_10_437`, `TestEmbeddedCRDsCarrySchemaVersionAnnotation_spec_10_437`, `TestEmbeddedCRDsPreserveUnknownFields_spec_10_437`, the new tier-0 proto no-drift test, `TestGeneratedSchemasMatchOpenAPI_spec_15_2_1_1386`, `TestGeneratedToolsMatchOpenAPI_spec_25_12`, `TestSchemaIsCommitted_spec_17_6_655`, and `TestMappingYAMLInSync`, each of which fails on drift between a derived file and its source |
| Specification prose, heading slugs, and intra-spec links pinned as Go string literals in a `tests/tier11_docs/` reconciliation test | the register of every Go string literal under `tests/tier11_docs/` that names a spec heading slug, an intra-spec markdown link, or pinned spec prose, which includes but is not limited to the `specSection`, `requireLine`, and `requireAllContain` literals | the `specshift` name pass and the reduction sub-steps, extended to those literals | tier 11, run as the exit criterion of every sub-step that edits pinned prose |
| Specification index entries in `spec/README.md` | the `## N` and `### N.M` headings under `spec/`, the deeper headings the index already carries, and the §28.5 card headings | hand-authored in the same change as the heading | the heading walker, which fails an in-scope heading with no index entry whose anchor resolves |
| Test-harness contract prose in `TESTING.md` that enumerates a value the tooling changes, which is every §7 field-semantics sentence closing an enum the producer widens plus the §21.3 infrastructure-failure sentence | the `UNVERIFIED` verdict state and the `unverified` tier status proposal 0065 adds | **not staged here.** Proposal 0065 hand-authors these sentences in the same change as the constant that falsifies them, so this proposal neither restates nor re-edits them; its own `TESTING.md` edits are the reserved-phrase and retired-identifier sites its passes rewrite | proposal 0065, whose review closes them |
| Reserved noun phrases and retired channel identifiers in the tracked root-level contract documents `README.md` and `TESTING.md` | `tests/registers/reserved-phrase-senses.yaml` and `tests/registers/identifier-senses.yaml`, on the same per-occurrence terms as the `spec/` and `docs/` sites | the `specshift` name and identifier passes, whose walk covers tracked root-level markdown under the exclusion list N3 states | the naming lint and the identifier-resolution gate, whose scope is the same walk, so the gate that reads the whole tree has a pass that can write every file it reads |
| Correcting a description the collision made wrong | `tests/registers/reserved-phrase-senses.yaml` | hand-authored | review, because no gate reads meaning; the naming lint and the identifier gate both pass a semantically wrong sentence that carries a canonical spelling |
| A sentence that a reduction falsifies, where no pass repairs its meaning, because the sentence carries no line citation and no moved anchor, and any reserved phrase it carries is rewritten to the current spelling while the false statement stands | the reductions SPEC-3 lands | hand-authored in the same change as the reduction, and enumerated there; the members are the `spec/15` §15.3, §15.4.5, and `MessageEnvelope` sentences, the two §15.7 platform-MCP-tool sentences, the six §15.4.4 pseudocode comments that cite the retired §15.4.1 in the spelled-out `Section 15.4.1` form the anchor pass does not read, the `spec/21_planned-post-v1.md` line 31 link label that names the retired §15.4.1 in the same spelled-out form while its target anchor survives, the three shipped schema descriptions, the `docs/api/internal.md` binary-protocol pointer, the two `schemas/README.md` artifact-table rows, and the fifteen pointers that name §4.7 as the owner of relocated intra-pod material, thirteen of them `spec/` and `docs/` sentences, one of those thirteen being the §15.7 graceful-shutdown bullet and four of them the `spec/11` and `spec/12` crash-recovery pointers at `spec/11_policy-and-controls.md` lines 53, 153, and 171 and `spec/12_storage-architecture.md` line 161, the fourteenth the pair of `schemas/lenny-adapter.proto` comments on the intra-pod handshake, and the fifteenth the `STATUS_INTERRUPT_TIMEOUT` comment at `schemas/lenny-adapter.proto` line 1063, all of which SPEC-3 lists | review, plus the tier-11 successor-pointer check where the rewritten sentence names a successor heading |
| An inbound reference into a retired anchor whose cited material is carved out of the reduction and stays where it is, so the anchor map's single successor for that anchor would send the reference to the wrong heading. The class is every reference into a retired anchor whose cited material stays where it is, in any carrier the anchor pass writes, which is the markdown link form and the bare `§X.Y`-form section citation in a comment or in prose alike. The **target-and-label rule** governs every markdown-link member: a hand correction rewrites the link's label as well as its target whenever the label names the retiring subsection, in either the `§15.4.1` or the spelled-out `Section 15.4.1` spelling, because a link whose target is redirected while its label still names §15.4.1 names a section that exists in no `spec/` file. The rewritten label names the section the hand-written target resolves into, and rewriting it leaves no retired citation at that site for SPEC-4's tree-wide citation pass to read | the carve-outs SPEC-3 states with the links it enumerates there, plus `tests/registers/anchor-senses.yaml`, keyed by file and occurrence, which records the destination anchor of every occurrence of a retired anchor the map alone cannot decide, and which SPEC-4 retires with the map | hand-authored in the same change as the split for the markdown-link members, before any anchor pass runs, which today are the seven `spec/07_session-lifecycle.md` links into `1541-adapterbinary-protocol` that cite `MessageEnvelope` material, the seven same-page links into the same anchor inside `spec/15_external-api-surface.md` that cite material the carve-outs keep there, six of them citing the `MessageEnvelope` heading and the seventh, at line 2733, citing the surviving §15.4.2 heading, and the absolute GitHub URL at `docs/reference/adapter-contract.md` line 371 that cites the same anchor for the Translation Fidelity Matrix; the `specshift` anchor pass for the bare section-citation members, driven per occurrence by the sense register and failing an occurrence with no entry rather than substituting the map's single successor | review, plus the fragment-link gate, which confirms the hand-written target resolves for the intra-repo link members; the absolute-URL member is covered by review alone, because the gate does not read an absolute URL; and TEST-1's anchor-pass cases, which pin the fail-closed path for a bare citation with no register entry and pin that a citation of carved-out material resolves to the surviving heading |
| A same-page markdown fragment link carried inside a block a reduction relocates to another file, whose target heading stays behind, so the link breaks although neither it nor its target changed | the blocks SPEC-3 relocates, and the links it enumerates there | hand-authored in the same change that moves the block, rewritten to the file-qualified form against the file the block left; the anchor pass cannot reach them because the map is keyed by retired anchor and these targets survive | the fragment-link gate, which is red on a same-page fragment that no longer resolves against its new page |
| A pre-existing intra-repo markdown fragment link whose target heading does not exist | the seven links SPEC-4 enumerates | hand-authored in the same change as the fragment-link gate | the fragment-link gate, which is green on introduction once they are corrected |

No list of edit sites appears in this proposal for the script-driven classes, and none should appear in
the applied change. A list is stale the moment a step merges, and a reviewer cannot verify one at this
scale, while a gate can. The hand-authored classes are bounded and are enumerated where they land, and the
few sites the script-driven classes name explicitly are named because a gate or a served artifact depends
on them rather than as an attempt at enumeration.

### 3.5 Sequencing inside the proposal

The sub-steps below are applied in order. The tooling they run comes first, and it is no longer part of
this proposal.

1. **Tooling, landed by proposal 0065.** `scripts/specshift` and its four passes, the citation resolver,
   the line-citation ratchet, the proto no-drift test, the change-graph completeness check, the
   skip-reason classifier, the register contract they share, and the residual gate §4.7 defines together
   with its checks for the classes that proposal seeds. This proposal does not build them and cannot be
   applied before they exist in the tree, because every sub-step below is a script run over a register
   rather than a hand edit. The split exists because the implementation pipeline applies specification
   edits before it writes code, so a proposal that carried both halves would be asked to hand-apply the
   edits its own unbuilt script is meant to produce. Two application attempts of the combined proposal
   failed in exactly that way, the second reporting the reserved-phrase pass unappliable because the
   registers that resolve each site did not exist. §0 states the entry criterion this places on
   application.
2. **Naming law, registers, and prose.** Proposal 0067 created `spec/28_communication-channels.md`
   carrying §28.1 through §28.4, which are the law and the three registers, and this sub-step carries the
   reserved-word removal and the
   `spec/03` correction. No wire surface changes: the name pass writes `.proto` comments alone, and this
   sub-step runs `make generate-proto` so the committed stubs under `pkg/proto/` match them. The naming lint
   lands here, because this is the sub-step
   that removes the reserved phrases the lint reads.
3. **The wire contract change.** The two renames that reach the proto, the manifest, the SDKs, the flag,
   and the runtime author contract, applied as one exclusive change so no other work contends with it. The
   identifier-resolution gate and the `coordinatorHoldAllowedMethods` assertion land here, because this is
   the sub-step that collapses each retired spelling to one canonical identifier.
4. **The new sections, and the anchor and citation rewrite.** `spec/28` §28.5 through §28.8, the new
   `spec/29`, the reductions with
   their successor pointers, the numbered subsection headings inserted into `spec/04`, `spec/10`, and
   `spec/13`, and the mechanical retirement of the redirected anchors and the line
   citations. Each file's heading insertion runs ahead of the pass that converts that file's line
   citations, because an
   inserted heading shifts every line below it and the pass has to read the shifted line numbers; putting
   both in one exclusive change is what keeps the shift from invalidating a population no pass is running
   over. For `spec/04` that pass is the one inside the reduction sub-step, which converts every citation
   into the file, so the §4.4 and §4.7 headings land there; the `spec/10` and `spec/13` headings land with
   the tree-wide citation pass. The freeze of `gateway-runtime-comms.md` lands here as well, since it is the point at which §28 and
   §29 supersede it. The heading walker, the tier-11 successor-pointer check, the claim register's
   schema-only validator, the §28.8 matrix completeness check, and the artifact-register supersession
   check land with the new sections, the reductions, and the seeded register, the
   fragment-link gate lands with the anchor pass that rewrites the links into the
   retired anchors, and the reference-document freeze check lands with the freeze header it reads. The gate-integrity meta-gate lands here too, because it ranges over the gates this
   proposal registers and the fragment-link gate is the last of them, so this is the first sub-step at
   whose exit every name on its fixed list is registered.
5. **The gate cases.** The accept, reject, and boundary cases for every gate the four sub-steps above
   land, plus the register-contract and validator batteries.

Each gate has exactly one landing sub-step, and it is the sub-step that supplies its route to green. A
gate whose route to green is a baseline proposal 0065 seeds lands in step 1; a gate whose route to green is a
content change lands with that content. TEST-1 adds the cases rather than the gates, so no gate is staged
twice and none lands at tier 0 before the sub-step that makes it green.

The residual gate is the one gate whose landing is stated per class. §4.7 gives each enumerated class its
own residual check over that class's residual register, and each check lands with the sub-step that seeds
that register: the line-citation, generated-artifact, change-graph-coverage, and skip-reason classes in step 1,
the reserved-phrase class in step 2, the identifier class in step 3, and the anchor class in step 4. Each
check therefore has exactly one landing sub-step under the same rule, and none reaches tier 0 before the
register that makes it green exists.

## 4. Detailed design

### 4.1 The naming law

The rules below are normative in §28.1.

- **N1.** A channel's canonical identifier is a mnemonic for the conversation it carries, chosen so that
  no two channels on the same boundary share a stem. The endpoint pair, the plane, the dial direction, the
  authority direction, and the transport are register columns in §28.3, so an identifier is not required
  to encode any of them and is never read as the authoritative statement of one. N1 is a review-time rule
  and carries no gate, because a mnemonic's fitness is a judgement.
- **N2.** Identifiers are mnemonic, uppercase, and hyphenated. Positional identifiers are not used.
- **N3.** `lifecycle` and `control` are reserved and may not appear as bare noun phrases, in either the
  space-separated spelling or the hyphenated compound spelling, in `spec/`,
  `docs/`, `schemas/`, a Go doc comment in a tracked Go file, or a tracked root-level markdown document
  the exclusion list below leaves in scope, of which `README.md` and `TESTING.md` are the two that carry
  the phrase today. The matcher applies the comment-marker continuation join §4.6 states for citations
  before it applies either spelling, because a reserved phrase wraps across two consecutive comment lines
  the same way a citation does, and without the join a line-oriented matcher reads neither line as a
  violation, so the site is written by no pass and read by no gate. SPEC-1 names the sites the tree carries
  in that position. N3 describes the two banned spellings rather than reproducing them, because §28.1 is
  itself under `spec/` and the naming lint reads it like any other file in the domain, so a quoted
  specimen would fail the lint in the section that states the rule. The literal spellings are carried in
  `.claude/rules/channel-naming.md` and in the lint's own matcher, both of which sit outside the domain
  N3 names. The excluded root-level files are the historical audit records
  `BUILD-GAPS.md` and `TEST-GAPS.md`, the two root planning documents, and the build and queue records
  `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`, excluded in the same way `proposals/` is,
  because each records findings, plans, and decisions as they were written rather than the current
  contract. Every `testdata/` directory is excluded as well, in the same way and for a different reason:
  a fixture exists to carry the form its gate rejects, so a fixture holding a bare reserved phrase or a
  retired identifier is correct rather than a violation, and a matcher that reads it reports the gate's own
  test data as a defect. The exclusion is stated here rather than per-gate because it has to hold on both
  sides at once. That exclusion list is the one the name pass, the identifier pass, the naming lint, and the
  identifier-resolution gate all read, so every file those gates read has a pass that can write it, and a
  `testdata/` fixture inside the lint's read domain but outside every pass's write domain is exactly the
  writerless site this rule exists to prevent. No tracked `testdata/` file carries a bare reserved phrase
  or a retired channel spelling today, measured over `git ls-files`, so the exclusion removes nothing from
  the population SPEC-1 and SPEC-2 measure and no exit criterion below moves. A
  markdown anchor identifier is outside the reserved-phrase matcher in both spellings: a kramdown `{: #id }`
  attribute value and the fragment of an intra-repo markdown link are addressable link targets rather than
  prose, and rewriting one breaks every inbound link, including the untracked links this repository cannot
  see. `{: #lifecycle-channel }` at `docs/reference/glossary.md` line 207 is the worked accept case. An
  anchor that has to change moves through the anchor class in §3.4 with an entry in
  `tests/spec-anchor-moves.json`, so a redirect exists for every inbound link, rather than through the name
  pass. An identifier stem may not reuse a term the specification
  already binds to an unrelated mechanism, which is why C6 is `CH-ADAPTEREVENTS` rather than the
  `CH-EVENTSTREAM` the plan's table originally carried.
- **N4.** Each channel uses one identifier everywhere: the Go package or file name stem, the proto RPC
  name stem, the metric label value, and the test name fragment for a test scoped to one channel. A gate
  or a test spanning channels is named for the invariant it enforces and carries no channel identifier.
  The metric half of N4 is deferred, and §28.1 states the deferral rather than leaving it to this document:
  N4 binds the metric-label namespace, and the remediation step that adds the adapter metrics endpoint and
  the catalog entries, which is R12 in `gateway-runtime-comms-remediation.md`, is the step that discharges
  it. The deferred population is the two metric names at `pkg/adapter/metrics.go` lines 71 and 79, which
  name the channel by its retired spelling and which are emitted by the adapter process inside the agent
  pod and so sit outside the default scrape target set until a deployer wires an adapter scrape target, on
  the rule `spec/16_observability.md` lines 186 and 187 already state for the other adapter-emitted
  metrics. Renaming an unobservable metric ahead of that step produces churn with no observer, so the
  deferral carries a claim-register row with status `ABSENT` whose `deferral_id` is R12, seeded in SPEC-3
  with the rest of the register.
- **N5.** A link identifier and the channel identifiers it carries share no stem, so a search for one
  never returns the other.
- **N6.** A register is named for the store and the key rather than for a verb.
- **N7.** A flag, environment variable, or manifest key naming a channel carries that channel's identifier
  in the form its carrier already fixes: a flag uses lowercase kebab, an environment variable uses upper
  snake, and a manifest key uses the camelCase convention the §4.7 adapter manifest field set establishes.
  The rule names the carrier's form rather than one form for all three because the manifest is a
  client-facing JSON document whose every other key is camelCase, and a kebab or snake key would put the
  naming law and the §4.7 field reference in disagreement on a surface third-party runtimes parse.
- **N8.** A specification citation names a heading rather than a line. Citing a specification line number
  is retired and may not be written, in any spelling. The prohibition is on the line number rather than on
  one form of words, so a spelling the current matcher does not recognize is a gap in the matcher rather
  than a permitted citation, and §4.6 states the matcher and the spellings the tree carries today. A
  section that gives up content carries a permanent successor pointer naming the heading that now owns the content
  and the identifiers that moved. N8 is the rule the citation resolver, the line-citation ratchet, and the
  successor-pointer check enforce, and it is the section every test those gates carry cites.

N8 sits with N1 through N7 because the §28 card headings are the citable handles the naming law creates,
so the rule that citations name a heading is the same rule that makes an identifier's contract findable.
`.claude/rules/channel-naming.md` states the same rules for future agents, so a conforming name is the
default rather than a lint finding after the fact, and the standing prohibition on line numbers in a
`// spec:` citation (`.claude/rules/code-best-practices.md` line 57) is the code-side half of N8.

### 4.2 The axes

Each channel records: dial direction, authority direction, plane, transport, boundary, and exclusivity.
Transport and boundary are closed sets, so a new value requires a specification change rather than an
undeclared extension. Exclusivity records the granularity and the enforcing guard, or names the guard as
missing. That last field is what turns the reference's exclusivity findings into a maintained property.

### 4.3 The registers and the contract cards

The three registers carry one row per entry with a provenance column. The contract cards sit in §28.5,
grouped by boundary, one subsection per boundary value in the order §4.8 fixes, each opening with a
one-edge figure and holding its cards under a fixed field template. The template fixes which fields a card states and in what order,
so a reader can compare two cards field by field. It sets no line budget, because a card states the
contract its channel has, and a channel whose contract is genuinely long is described in full
rather than truncated to meet a number.

The citable handle is the card heading plus the identifier, which is stable across insertions.

### 4.4 The claim register

Every normative statement about this surface carries a row in a claim register, `tests/claim-map.json`, with a status drawn from
the reference's vocabulary: `WIRED` for a mechanism reachable from production code, `UNWIRED` for one
implemented with no production caller, and `ABSENT` for one specified and not implemented. A `WIRED` row
names the production surface. A row that is not `WIRED` names the step that will close it.

This is what stops the specification asserting mechanisms that do not run, which is the defect class
behind a third of the reference's records. It also gives the later steps their work queue.

The claim register carries its own schema rather than the shared register contract's entry schema, because
a `WIRED` row is a permanent statement about the tree and has no expiry, while the shared contract requires
one and fails an entry whose expiry has passed. What lands here is that schema, the seed rows, and the
tier-0 validator SPEC-3 lands with them, whose predicate and cases TEST-1 states. The join from a `WIRED` row to a surface a reachability gate reports
reachable needs a gate this proposal does not build and lands with it.

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

The resolver is red on introduction, so it ships with a baseline of its own rather than as a bare
predicate. A large population of in-tree citations already fails its rule before any content moves. A
measurement over the tracked tree, computing each section's range from the `##` through `######` headings
under `spec/` and applying the read exclusion stated below, finds on the order of 1,500
citations across roughly 500 files that do not resolve inside the section they name; the exact figure
depends on how a section's end line is computed, so proposal 0065 records the count its own resolver produces.
Two verified examples: `pkg/adapter/workspace/materialize.go` line 203 cites `§7.4 line 433`, while §7.4
begins at `spec/07_session-lifecycle.md` line 437 and line 433 sits in §7.3; and
`pkg/gateway/externalapi/admin/erasure.go` line 356 cites `§12.8 line 764`, while §12.8 begins at
`spec/12_storage-architecture.md` line 774. Proposal 0065 therefore seeds
`tests/registers/line-citation-resolution.yaml` with today's non-resolving citations, keyed by file and
citation text, and the gate fails a citation that neither resolves nor appears in that baseline. This is
the same transitional machinery the ratchet gets, and it is used instead of the shared exception register
because a stale citation's correct disposition is retirement in SPEC-4 rather than an entry with an owner
and an expiry, which is the argument TEST-1 already makes for the heading walker at a tenth of this scale.
The baseline shrinks as the line pass retires citations, and SPEC-4 empties it when every per-file count
reaches zero.

The ratchet is a tier-0 Go test rather than a linter rule, because the repository's lint invocation is
downgraded to a warning and a check whose script is absent passes silently. Its baseline is a per-file
count. A file absent from the register fails on its first line citation, and a file present fails when its
count rises. A count that falls rewrites the register downward, so retirement is recorded. When every
count reaches zero the register is empty and the gate becomes a flat prohibition.

Both baselines are keyed by file path, and SPEC-2 moves files that carry line citations before the line
pass retires them, so `scripts/specshift`'s identifier pass rewrites the keys of any file it renames in
the same run, in every path-keyed test-infrastructure register the move invalidates, which today is
`tests/registers/line-citations.yaml`, `tests/registers/line-citation-resolution.yaml`,
`tests/change-graph.json`, and `tests/spec-map.json`, per SPEC-2. The same run also rewrites every
`::<symbol>` reference in `tests/spec-map.json` that names a symbol the pass renames, because those
references are keyed by symbol rather than by path and a tier-0 check resolves each one against its
declaring file, per SPEC-2. Without that the ratchet's own rule fires on a rename
that changed no citation, because the new path is absent from the register and fails on its first
citation, and every baselined non-resolving citation under the old path reappears as a new resolver
failure. The population is concrete: `pkg/adapter/lifecyclechannel.go` carries nine citations of the
matched form and `pkg/adapter/controlchannel.go` carries five, with their test siblings
`pkg/adapter/lifecyclechannel_test.go` and `pkg/adapter/controlchannel_test.go` carrying five and four.
Neither of the two escapes the proposal otherwise permits is available, because the ratchet never widens
and suppression is not used.

The resolver, the ratchet, and the line pass all match one citation form, stated below, wherever it
appears in a tracked file, rather than matching a
comment dialect. The one place a carrier's comment marker enters the matcher is the continuation join
stated below, which is what lets a citation wrapped across two comment lines be read as one citation. They
walk the whole tree with an explicit
exclusion list rather than an inclusion list of directories.

That exclusion list has two levels, because a pass writes and a gate only reads. The read exclusion, which
the resolver and the ratchet share, is `proposals/`, the historical audit records `BUILD-GAPS.md` and
`TEST-GAPS.md`, the two root planning documents `gateway-runtime-comms.md` and
`gateway-runtime-comms-remediation.md`, and the two citation registers the two gates themselves consume,
`tests/registers/line-citation-resolution.yaml` and `tests/registers/line-citations.yaml`, and every
`testdata/` directory. The first four
groups are excluded for the reason N3 gives, which is
that they record findings as they were written rather than the current contract. The two registers are
excluded because a gate cannot read its own baseline as tree content. Every `testdata/` directory is
excluded for the fixture reason N3 gives, which is that a fixture exists to carry the form its gate
rejects, so a gate that read its own test data would report it as a defect. The resolution baseline is keyed by
file and citation text, so it holds a copy of the text of every non-resolving citation in the tree. Inside
the resolver's read domain each copy is a second occurrence of that citation, filed under the register's
own path rather than under the file the citation was written for, and it is non-resolving by construction,
which is exactly the outcome TEST-1 pins as a failure when it requires that a baseline entry does not
travel between files. Seeding an entry for such a copy would add a further copy of the same citation text
to the register, so the seeding would not converge. The ratchet excludes the same pair for the same
reason: the register would otherwise enter its population as a file with no per-file count and fail on its
first line citation. Excluding the pair from both gates is what lets proposal 0065 land them green, per §3.5 step
1. The write exclusion,
which the line pass reads, is those groups plus every file the per-file generated-artifact rule
stated at the end of this section covers. A generated artifact is therefore inside the resolver's and the
ratchet's read domain and carries a per-file count, and its route to zero is the regeneration of its
source rather than a write, which is what makes the ratchet's flat prohibition reach `pkg/proto/`,
`charts/lenny/crds/`, `pkg/embedded/manifests/`, `charts/lenny/values.schema.json`, and
`schemas/ocsf-mapping.yaml`. Every measurement in this section, every baseline the gates seed, SPEC-4's
Target, and §11 are stated against the read domain, so the zero end state is measured over the population
the gates observe and no file outside that domain is ever opened for a per-file count.

The citation list is narrower than the naming list N3 states, which additionally excludes `BUILD-PLAN.md`,
`BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`. The two lists differ because the subjects differ. A reserved
phrase in a build or queue record is part of what was written at the time and rewriting it would edit the
record, while a line citation in the same file is a pointer that has to keep resolving, and N8 bans the
form wherever it is written. The population the difference covers is four citations across two files: the
keyword-form citation at `BUILD-PROGRESS.md` line 34 and the three colon-form citations at
`PROPOSAL-QUEUE.md` lines 459 and 477 (`§8.3:470`, `§8.8:879`, and `§15.1:630`).

A citation of the retired form has three parts. It names a specification section, either by section number
as `§X` with any number of dotted subsection components or by file path as `spec/NN_<file>.md` with the
`spec/` prefix optional. It may then carry a short qualifier naming a sub-element of that section, such as
`item 3`, `rule 4`, `table`, `preamble`, `step 2`, or `NET-063`. It then carries either the word `line` or
`lines`, or a colon standing in for that keyword and written directly against the reference
(`§17.6:404`, `spec/15_external-api-surface.md:1315`), followed by one or more members. A member is a single line number or a range of two line numbers
separated by an ASCII hyphen, an en dash (U+2013), or an em dash (U+2014). Members are separated by a
comma, a slash, a plus sign, or the word `and`; a continuation member repeats neither the section reference
nor the qualifier and may repeat the word `line`. A member may carry a short trailing gloss naming what
the line says, written as a parenthesized phrase, a quoted fragment, or a bare word or two, as in
`line 437 ("`lenny.dev/schema-version` annotation on the CRD object")`, `line 408 step (e)`,
`line 240 messagingScope`, and `line 1779 audit event`; the gloss is consumed with its member rather than
terminating the match. The matcher consumes every member of a citation rather than its
head, and the pass converts the whole citation to a single anchor rather than leaving a residue.

A citation may be wrapped across two consecutive comment lines, and the matcher joins the continuation
before it applies the form. The join consumes the newline together with the carrier's comment marker
(`//`, `#`, `--`, or the leading `*` of a block comment) and the whitespace on either side of it, and
reads what follows as the continuation of the same citation. All three wrap positions are covered, which
are a wrap between the section reference and the `line` or `lines` keyword, a wrap between the keyword and
its first member, and a wrap inside a member list. Without the join a line-oriented scan sees a section
reference with no line-number token on the first line and a line-number token with no section reference on
the second, so neither the form stated above nor the residual predicate §4.7 states reads the citation,
the resolver does not resolve it, the ratchet does not count it, and the file reaches count zero while a
stale pointer survives. Measured over `git ls-files` under the read exclusion stated above at commit
`668deca8`, the wrapped population is 768 occurrences across 436 files, of which 45 across 32 files cite
§4.7, §4.8, §4.9, §15.4, or §15.5, which are the ranges the SPEC-3 reduction shifts.
`pkg/gateway/sessionserver/messages.go` lines 156 and 157 wrap between the keyword and its first member
(`spec: §15.4 lines` then `1672-1721.`) and point into a shifted range;
`pkg/gateway/policy/interceptor/immutability.go` lines 14 and 15 (`(spec: §4.8` then `line 1060)`),
`cmd/lenny-gateway/main.go` lines 1775 and 1776 (`spec: §10.1` then `line 51. F-10.1.5.`), and
`pkg/alerting/rules/slo.go` lines 99 and 100 (`§16.10` then `lines 732-736`) wrap between the section
reference and the keyword; and `charts/lenny/values.yaml` lines 892 and 893 (`spec: §17.9.1` then
`line 1350`) carry the same wrap in the `#` dialect. The resolver baseline and the per-file ratchet
baseline proposal 0065 seeds are measured with the join applied, so a wrapped citation is counted and resolved
from the first run rather than surfacing after SPEC-4 reports every count zero. The in-tree precedent
`tests/tier0_static/degradation_lock_line_citation_test.go` line 38 is single-line by construction, so the
join is one of the ways the resolver widens it.

A range citation resolves as valid only when
both endpoints fall inside the cited section, a multi-member citation resolves as valid only when every
member resolves, a section-level citation resolves against the whole of
the section it names, a qualified citation resolves against the section it names with the qualifier
carried through the conversion, and a path-form citation resolves against the section of the file it names
that contains the cited line. The form is
stated this widely because the prohibition N8 ends in is on the line number rather than on one spelling,
and each spelling below is a measured population in the tree today rather than a hypothetical one.

The multi-member comma spelling is the first of them: 617 occurrences across 341 files
carry one `§X(.Y)* line(s)` prefix followed by two or more comma-separated line numbers or ranges, 86 of
them across 57 files naming §4.7, §4.8, §4.9, §15.4, or §15.4.x, which are the ranges the SPEC-3 reduction
shifts. A matcher that stops at the first comma converts the head of each of those citations and leaves
the remaining line numbers in place, where the resolver does not read them, the ratchet does not count
them, and the converted comment reads as an anchor followed by orphan integers, so the file reaches count
zero while a stale pointer survives. `cmd/lenny-gateway/adminrouter.go` line 205
(`§4.8 lines 1057-1058, 1077`), `pkg/gateway/sessionserver/taskrecord.go` line 29
(`§8.8 lines 806-823, 897-917; §4.2 line 157`), and `cmd/lenny-gateway/stores.go` line 764
(`§12.5 line 315, 321-325`, which uses the singular `line` before a list) are verified instances.

The slash and `and` separators carry the same failure. Measured over `git ls-files` under the read
exclusion stated above, 50 occurrences across 42 files separate their members
with a slash and 2 more separate them with the word `and`, and the continuation member repeats the word
`line` without the `§` prefix. `pkg/experiment/experiment.go` line 184 and
`pkg/controller/poolscaling/variants.go` line 98 both carry `§10.7 line 694 / line 743`,
`cmd/lenny-gateway/httpsurface.go` line 144 carries `§25.3 line 441 / lines 527-528`,
`pkg/gateway/sessionserver/create.go` line 170 carries `§7.1 line 18 / line 75`,
`pkg/gateway/storage/pgtenant/pgtenant.go` line 177 carries `§4.2 line 163 / line 177`, and
`cmd/lenny-controller/setup.go` line 110 and `pkg/embedded/crds/crds_test.go` line 90 carry
`§10 line 437 / line 443`, which is the same section-level family SPEC-4 hand-edits in the chart CRD
annotation blocks. A member list whose separator is a comma is also written with the word `line` repeated,
as at `pkg/gateway/mcpfabric/mcptools/mcptools.go` lines 1445 and 1503 (`§10.6 line 601, line 629`), which
is why a member is allowed to repeat the keyword.

The plus sign is the fourth separator and carries the same failure as the other three. Measured over
`git ls-files` under the read exclusion stated above, a
`+`-separated continuation member appears in 11 files. One instance ties a fail-closed
security control: `pkg/preflight/crdschema.go` lines 22 through 25 carry
`§10 line 437 ("...") + line 443 ("...")` on `CRDSchemaVersionAnnotation`, and
`CRDSchemaVersionCheck` at line 51 is the preflight comparison that aborts an upgrade fail-closed when a
CRD's `lenny.dev/schema-version` annotation does not match. That is the same `§10 line 437 / §10 line 443`
family SPEC-4 hand-edits in the chart CRD annotation blocks. The other instances are
`pkg/adapter/checkpoint.go` line 25 (`§7.3 line 408 step (e) (replay workspace checkpoint) + line 409`),
`pkg/adapter/resume.go` line 86, `pkg/adapter/connectormcp_test.go` line 48,
`pkg/gateway/mcpfabric/mcptools/mcptools_register.go` line 286
(`§7.2 line 240 messagingScope + line 373 parent`), `pkg/gateway/sessionserver/events_test.go` line 192,
`pkg/ops/opsserver/operations.go` line 58 (`§25.4 line 1779 audit event + line 1769 error code`),
`pkg/ops/opsserver/event_subscriptions_test.go` line 181, `cmd/lenny-gateway/flags.go` line 1188,
`tests/tier2_component/migrations/phase3_gate_test.go` line 36, and
`charts/lenny/tests/backend-invariants_test.yaml` line 105. Several of them also carry the trailing gloss
the form admits, which is why a gloss does not terminate a member.

The qualified spelling interposes a short sub-element name between the section reference and the word
`line`. Measured over `pkg`, `cmd`, `tests`, `sdks`, `spec`, `docs`, `schemas`, `charts`, and
`migrations`, and excluding the slash and comma continuations already counted, 136 occurrences sit across
68 files. `pkg/audit/jcs/jcs.go` lines 15 and 39 carry `§11.7 item 3 line 364`,
`pkg/gateway/mcpfabric/mcptools/mcptools.go` line 28 and
`pkg/gateway/mcpfabric/mcptools/generated_schemas.go` lines 9 and 16 carry `§15.2.1 rule 4 line 1386`,
which is inside a served MCP tool schema, `sdks/client/go/lenny/client.go` lines 332, 345, 363, and 380
carry `§7.2 table line 124` inside a shipped client SDK, and `charts/lenny/templates/mtls-pki.yaml` line
104 carries `§10.3 NET-063 (spec line 327`. A matcher requiring `line` to follow the section reference
immediately leaves every one of them unconverted, unread by the resolver, uncounted by the ratchet, and
free to regrow after retirement.

The en-dash range spelling is a fourth population: 65 occurrences across 38 files under `pkg`, `cmd`,
`tests`, `sdks`, `schemas`, `charts`, `migrations`, `docs`, `spec`, `scripts`, `build`, `compose`, and
`.github` separate a range's endpoints with U+2013 rather than with an ASCII hyphen, including
`pkg/gateway/storage/evictionfallback/evictionfallback.go` lines 3, 31, and 377 (`§4.4 lines 263–291`),
`pkg/gateway/mcpfabric/mcptools/elicitation.go` line 93 (`§9.2 lines 58–64`), and
`pkg/gateway/policy/policy/authevaluator.go` line 47 (`§4.8 lines 1025–1028`), which points into a
section the SPEC-3 reduction shifts. The range separator is therefore a character class covering the
ASCII hyphen, the en dash, and the em dash.

The path-form spelling names the specification file rather than the section number, so it carries no `§`
at all: 123 occurrences across 59 files under `pkg`, `cmd`, `tests`, `charts`, `migrations`, `docs`,
`schemas`, `sdks`, and `scripts`, of which 39 point at or below `spec/04_system-components.md` line 637 or
`spec/15_external-api-surface.md` line 1458, which are the ranges the SPEC-3 reduction shifts.
`pkg/adapter/metrics.go` line 12 cites `spec/04_system-components.md lines 870-888`, a range inside §4.7
itself; `pkg/credential/lease.go` line 150 cites `spec/04_system-components.md line 1145`;
`pkg/controller/poolscaling/capacityplanning.go` line 95 cites `spec/04_system-components.md line 523`;
and `pkg/audit/ocsf/catalog.go` line 149 carries the same form in a `# spec:` line as
`11_security-trust-model.md line 414`. Leaving the path form out of the matcher would satisfy SPEC-3's
exit criterion while 39 citations point at the wrong lines, so it is inside the class rather than given a
separate disposition.

A path-form citation naming a file that does not resolve under `spec/` has the same disposition as the
straddling range, which is that the line pass fails it and reports it rather than guessing an anchor. The
stated resolution rule computes the section from the file the citation names, so a citation naming no
tracked specification file has no section to resolve against, and left alone it holds its file above count
zero and makes SPEC-4's zero exit criterion unmeetable. The population today is one nonexistent file name,
`11_security-trust-model.md`, carried at seven sites: `pkg/audit/ocsf/catalog.go` line 149,
`pkg/audit/ocsf/catalog_test.go` lines 26, 44, 75, and 113, `cmd/lenny-ocsf-mapping-gen/main.go` line 10,
and the generated `schemas/ocsf-mapping.yaml` line 3, which mirrors the const at
`pkg/audit/ocsf/catalog.go` lines 147 through 150. `spec/11_security-trust-model.md` is not a tracked file;
the OCSF mapping table those citations mean sits at `spec/11_policy-and-controls.md` line 414 and the
`line 365` citation at `pkg/audit/ocsf/catalog_test.go` line 113 sits in the same file, and both lines fall
inside `### 11.7 Audit Logging` at `spec/11_policy-and-controls.md` line 341. SPEC-4 hand-corrects the six
authored sites to that heading before the final line-pass run, and `schemas/ocsf-mapping.yaml` follows
through the regeneration §4.6 already stages for it.

The colon spelling replaces the `line` or `lines` keyword with a colon written directly against the
reference, and it is carried in both the section-number and the file-path variants. Measured over
`git ls-files` under the read exclusion stated above, the section-number variant appears 18 times across
10 files and the file-path variant appears 11 times across 7 files. One of the 18 sits inside `spec/`
itself, at `spec/17_deployment-topology.md` line 450 (`the bootstrap Job's readiness poll (§17.6:404)`),
so a matcher requiring the keyword would leave a specification line citation standing in the file the
naming law governs and would let SPEC-4 report every count zero while it stands. Others sit in shipped Go:
`pkg/adapter/usage.go` lines 52, 62, 152, and 259 carry `§11.2:46`, and
`pkg/gateway/externalapi/admin/me.go` lines 78, 92, 103, and 109 carry `§15.1:778-780`, `§15.1:798-800`,
`§15.1:802`, and `§15.1:805-812, 876-878`, the last of which shows the colon spelling taking the comma
member list, so the two spellings compose rather than partition. The file-path variant reaches
`charts/lenny/tests/ops-rbac_test.yaml`, `tests/tier0_static/ops_rbac_spec_sync_test.go`,
`pkg/gateway/externalapi/errorclassify/errorclassify.go`
(`15_external-api-surface.md:976-1099`, which omits the `spec/` prefix), and
`tests/tier3_contract/rest_mcp_consistency/published_schema_test.go`
(`spec/15_external-api-surface.md:1315`). `tests/tier0_static/degradation_lock_line_citation_test.go`
carries it at lines 74 and 77 (`spec/25_agent-operability.md:2057` and
`spec/25_agent-operability.md:2215`), so the file SPEC-4 hand-rewrites for its `§25.4 line N` predicate
carries two further citations in this spelling, and the same hand rewrite covers them.

The section-level spelling is a measured population: 556 occurrences across 148
files carry `§X line N` or `§X lines N-M` with no subsection component, in the same carriers the dotted
spelling uses. Three of them point into the §15.4 block SPEC-3 reduces
(`pkg/gateway/sessionserver/expiry_warning.go` lines 32 and 50 and
`pkg/gateway/runtime/adapterclient/client.go` line 484 all cite `§15 line 2141`, and §15.4 runs from
`spec/15_external-api-surface.md` line 1458 to line 2459), and 31 of them are the `§10 line 437` sites the
chart CRD annotation blocks and `tests/tier0_static/crds_test.go` carry, which SPEC-4 rewrites. A matcher
requiring the subsection component would leave all 556 unconverted, unchecked by the resolver, and free to
regrow. The resolver baseline and the per-file ratchet baseline are both measured under this widened
predicate. The range spelling is a measured population rather than a
hypothetical one: 3,606 range citations sit across 1,198 files, 294 of them pointing into §4.7, §4.8,
§4.9, §15.4, and §15.5, which are the sections the SPEC-3 reduction shifts, and the §1 magnitude counts
every spelling. Matching a subset would leave the rest free to regrow without limit, so the
prohibition the ratchet ends in would not be flat. The in-tree precedent matches the hyphenated range form
for one section: `tests/tier0_static/degradation_lock_line_citation_test.go` line 38 compiles
`§25\.4 lines? (\d+)(?:-(\d+))?`. That expression is the starting point rather than the target, because it
pins one section number, accepts an ASCII hyphen alone, and stops at the first member, so generalizing it
into the resolver means widening all three. That file is also a running tier-0 gate rather than only a
precedent, and its predicate requires the retired form to be present, so SPEC-4 rewrites it in the same
change as the line pass over `pkg/ops/`. Each of these scopings is a correction of an earlier
draft, and each is forced by the same measurement. The form is carried outside every comment dialect:
`pkg/gateway/externalapi/openapi/openapi.json` holds twenty citations inside served JSON values, nineteen
of them in a `description` and one in the `summary` that `openapi-to-mcp` copies into the generated tool
inventory, `pkg/gateway/mcpfabric/mcptools/mcptools.go` holds them inside the Go string literals that
become the served MCP tool schemas, twenty-nine of the thirty-four citations under `charts/lenny/crds/` sit in
`description:` block scalars rather than on a `# spec:` line, and `schemas/workspaceplan-v1.json` and
`tests/tier9_security/pentest/v1-baseline-bundle.json` carry the same form in JSON values. The population is 3,666 citations across 264 non-Go files, of which 1,218 across
230 files sit under `migrations/`, `charts/`, `sdks/`, `tests/`, and `build/`, roughly 2,168 sit in the
historical audit records `BUILD-GAPS.md` and `TEST-GAPS.md` at the repository root, and the remainder sit
under `pkg/`, `schemas/`, `scripts/`, `docs/`, `compose/`, `.github/`, and `dist/`. The two root-level
audit records are outside every pass, so their citations are outside the writable population and outside
every per-file count in the line-citation register; the two root planning documents carry seven citations
between them and are excluded on the same rule. The excluded figure is stated so the writable population
is not read as larger than it is. An inclusion list of directories is what leaves a carrier
ungated, and an ungated carrier means the prohibition the ratchet ends in is not flat.

A range whose endpoints straddle a section boundary has a disposition rather than a fail-and-stop. The
line pass fails such a citation instead of guessing an anchor, and the tree already carries seventeen of
them across fifteen files, measured by computing each section's range from the `##` through `######`
headings under `spec/` and testing both endpoints of every hyphenated `lines N-M` citation under the
read exclusion stated above. That measurement is re-run under the widened predicate
before the hand correction, because a range written with an en dash or reached through the qualified or
path-form spelling can straddle a boundary in the same way and the seventeen are the hyphen-only figure.
Left alone, a straddling range holds its file above count zero, so the ratchet's
flat-prohibition end state would be unreachable and SPEC-4's exit criterion unmeetable. The pass therefore
reports every straddling range it finds, and the sub-step running the pass hand-corrects each citation to
the section that contains the content it cites before the pass is re-run. The population is:
`pkg/alerting/rules/openslo.go`, `pkg/alerting/rules/openslo_test.go`, and
`tests/tier0_static/openslo_export_test.go` (`§16.10 lines 742-746`, while §16.10 begins at
`spec/16_observability.md` line 743); `pkg/delegation/lease/lease.go`,
`pkg/delegation/lease/lease_test.go`, and `pkg/gateway/mcpfabric/delegation/service.go`
(`§8.4 lines 515-521`, while §8.4 begins at `spec/08_recursive-delegation.md` line 517);
`pkg/gateway/externalapi/admin/audit_query.go` and
`tests/tier2_component/auditstore/admin_query_test.go` (`§25.9 lines 3653-3710`, while §25.9 begins at
`spec/25_agent-operability.md` line 3655);
`pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go` and
`pkg/gateway/sessionserver/sessionserver.go` (`§8.10 lines 1014-1027`, while §8.10 begins at line 1016);
`pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/taskrecord.go`,
`pkg/gateway/sessionserver/taskrecord_test.go`, `pkg/sessionrecord/record.go`, and
`pkg/sessionrecord/record_test.go` (`§8.8 lines 806-823` and `§8.8 lines 804-940`, while §8.8 begins at
line 808); and `pkg/platform/store/types.go` (`§12.6 lines 363-415`, while §12.6 begins at
`spec/12_storage-architecture.md` line 369). The resolver baseline does not substitute for the hand
correction, because a baselined citation still has to be retired for the per-file count to fall.

The generated-artifact half of the exclusion list is stated as a rule rather than as a fixed set of
directories, and the rule is the union §4.7 states for this class: a file whose header
declares it generated, or whose top-level document metadata declares it generated when the format carries
no comment syntax, or which is a member of the output set of a producer named below, is read by the
resolver and the ratchet and is never written by a pass. Excluding a
directory that mixes derived and authored content is what makes the ratchet's zero end state unreachable,
so the rule is applied per file. The third disjunct is necessary rather than redundant, because the
five CRDs in `charts/lenny/crds/` and their five copies in `pkg/embedded/crds/` are controller-gen and
copy output that carries no header generation declaration and no document-metadata declaration
(`charts/lenny/crds/lenny.dev_runtimes.yaml` lines 1 through 6, whose first content after the document
marker is `apiVersion`). A marker-only rule would classify all ten as ordinary carriers and direct a pass
to write them, and it would leave the residual gate for this class unable to select any future producer
output that carries no marker. The output sets are read from the producer list stated immediately below
rather than by running a producer, so the rule is decidable at tier 0. The generated artifacts carrying the citation form today are
`pkg/embedded/manifests/manifests.yaml`, `pkg/embedded/crds/`, `charts/lenny/crds/`, `pkg/proto/`,
`pkg/gateway/mcpfabric/mcptools/generated_schemas.go`, `pkg/ops/mcp/generated_tools.go`,
`charts/lenny/values.schema.json`, `docs/alerting/rules.yaml`,
`docs/alerting/routing-recommendations.md`, and
`schemas/ocsf-mapping.yaml`. `charts/lenny/values.schema.json` is the case the second half
of the rule covers: it is JSON, it carries no header comment, and its generation notice sits in the
top-level `description` value at `charts/lenny/values.schema.json` line 5, which reads "generated from
pkg/chart/values". A rule keyed only on a header comment would classify it as an ordinary carrier and
direct a pass to write it. Each artifact has a distinct producer, and the sub-step that rewrites a
producer's source runs that producer and takes its no-drift test as an exit criterion:

- `make generate` produces `pkg/embedded/manifests/manifests.yaml` from the chart templates and
  `charts/lenny/crds/` from the doc comments on `pkg/apis/lenny/v1alpha1/*.go`, and it also refreshes
  `pkg/ops/mcp/generated_tools.go`, `docs/alerting/rules.yaml`, and
  `docs/alerting/routing-recommendations.md`.
- `make generate-proto` (`buf generate`) produces `pkg/proto/` from `schemas/*.proto`. It is a separate
  target and is not reached by `make generate`, so a sub-step that rewrites a `.proto` file runs it
  explicitly.
- `go generate ./pkg/gateway/mcpfabric/mcptools/...` produces
  `pkg/gateway/mcpfabric/mcptools/generated_schemas.go` from
  `pkg/gateway/externalapi/openapi/openapi.json`. It is also outside `make generate`, so a sub-step that
  rewrites `openapi.json` runs it explicitly.
- `go run ./cmd/lenny-chart-schema-gen` produces `charts/lenny/values.schema.json` from the `desc:` struct
  tags on `pkg/chart/values/values.go`, whose text it copies verbatim into the JSON `description` values.
  `TestSchemaIsCommitted_spec_17_6_655` in `pkg/chart/values/schema_test.go` requires the committed file to
  byte-match a fresh `Generate()`. The producer is reached by neither `make generate` nor
  `make generate-proto`, and `Makefile` carries no target for it, so a sub-step that rewrites those tags
  runs it explicitly. The seven citations the schema carries are stripped from the `desc:` tags rather than
  converted to anchors, under the same rule SPEC-3 applies to the other served client artifacts, and the
  spec tie is kept in the Go doc comment above the field. A citation is stripped from a `desc:` tag only
  where the field's doc comment already carries the same tie, which holds for four of the seven fields:
  `Cluster` (`pkg/chart/values/values.go` lines 56 through 62), `IsolationProfile` (lines 63 through 69),
  `MaintenanceMode` (lines 129 through 131), and `NoEnvironmentPolicy` (lines 132 through 135). The line
  pass converts those doc comments to the anchor form like any other Go comment. The remaining three fields
  have no doc comment at all, so their `desc:` tag is today the only carrier of the tie:
  `SpiffeTrustDomain` at line 136 carries `§10.3 line 316`, `TraceSamplingRate` at line 137 carries
  `§16.3 line 359`, and `SaTokenAudience` at line 138 carries `§10.3 line 334`. The type-level comment on
  `Global` (lines 119 through 122) carries neither of the two `§10.3` line citations nor any `§16.3`
  citation, so it is not a surviving tie for them. The sub-step that strips those three tags adds an
  anchor-form `// spec:` doc comment above each of the three fields in the same edit, because stripping the
  only carrier would delete the spec tie the standing rule requires
  (`.claude/rules/code-best-practices.md` line 57) rather than relocate it, and both the ratchet and the
  resolver read a deleted citation as a retirement.
- `go run ./cmd/lenny-ocsf-mapping-gen` produces `schemas/ocsf-mapping.yaml` from the Go catalog in
  `pkg/audit/ocsf`, whose `mappingHeader` const at `pkg/audit/ocsf/catalog.go` lines 147 through 150 is the
  authoring source of the committed file's `# spec: 11_security-trust-model.md line 414` header line, which
  §4.6 names above as a path-form citation whose named file does not resolve, so the line pass fails it and
  SPEC-4 hand-corrects the const to the `spec/11_policy-and-controls.md` §11.7 anchor.
  `TestMappingYAMLInSync` in `pkg/audit/ocsf/catalog_test.go` requires the committed file to byte-match a
  fresh `MarshalMappingYAML()`, so a run that rewrites the const without regenerating turns it red and a
  run that skips regeneration leaves the committed file above count zero in the line-citation register,
  which makes SPEC-4's zero exit criterion unmeetable. As with `cmd/lenny-chart-schema-gen`, no `make`
  target reaches this producer, so a sub-step that rewrites the const runs it explicitly.
- The chart-to-embedded copy produces `pkg/embedded/crds/` from `charts/lenny/crds/`.

`charts/lenny/crds/` is controller-gen output plus two hand-applied post-generation layers that no Go doc
comment and no chart template produces, which are the `lenny.dev/schema-version` annotation with its
explanatory comment block and the top-level spec and status `x-kubernetes-preserve-unknown-fields: true`
markers. `make generate` overwrites the directory with raw controller-gen output and deletes both layers,
and the losses are behavioral: the schema-version annotation is the fail-closed CRD currency check the
controllers read at startup and `lenny-preflight` reads at upgrade
(`pkg/preflight/crdschema.go` lines 26 through 54), and the preserve-unknown markers stop a stale CRD
pruning fields a newer controller writes. `TestCRDManifestsInSyncWithGoTypes` strips exactly those lines
from both sides before comparing (`tests/tier0_static/crds_test.go` lines 162 through 192), so it goes
green on a regeneration that dropped them. Regeneration of that directory therefore has a re-application
step, and its exit criteria include the two tests that read the hand-applied layers rather than strip
them, which are `TestEmbeddedCRDsCarrySchemaVersionAnnotation_spec_10_437` and
`TestEmbeddedCRDsPreserveUnknownFields_spec_10_437` in `pkg/embedded/crds/crds_test.go`. Both read the
embedded copies, which the chart-to-embedded copy keeps byte-identical to the chart CRDs, so they cover
the chart side once the re-copy has run.

Two of the three directory exclusions an earlier draft stated are corrected here.
`pkg/embedded/localcli/`, `pkg/embedded/stack/`, and `pkg/embedded/k3s/` are hand-written Go packages with
no generator and no `go:generate` directive, and they carry 132 line citations, so the line pass rewrites
them like any other Go source and only `pkg/embedded/manifests/` and `pkg/embedded/crds/` are excluded.
`pkg/proto/` carries 60 line citations mirrored by protoc from the 48 in `schemas/*.proto`, and it reaches
zero only through `make generate-proto`, which is why that target is named above rather than folded into
`make generate`. The ten hand-applied citations in the five chart CRD annotation blocks, which carry the
section-level spelling `§10 line 437 / §10 line 443`, reach zero through the
hand edit SPEC-4 stages, described there, rather than through regeneration.

A no-drift test is durable only when it is a Go test. `scripts/check-proto-generated.sh` exits 0 both when
`buf` is absent and whenever `schemas/buf.gen.yaml` carries no active `remote:` plugin line, which is the
repository's current state, so it is the non-durable shell gate this proposal argues against. Proposal 0065
therefore adds a tier-0 Go test that regenerates the proto bindings into a temporary directory and diffs
them against the committed stubs.

The producer the test must reproduce is the whole `make generate-proto` target rather than `buf generate`
alone. That target runs `buf generate` and then applies two post-generation steps the plugins do not
perform: it prepends `// SPDX-License-Identifier: MIT` to every file that lacks the header, and it runs
`goimports -w -local github.com/lennylabs/lenny ./pkg/proto`, which regroups the import block
(`Makefile` lines 91 through 100; `schemas/buf.gen.yaml` lines 6 and 7 record the same fact). A test that diffs
raw `buf generate` output against the committed stubs reports drift on every generated file with no drift
present, which was confirmed by running `buf generate` into a temporary directory and diffing all six
committed stubs. The test therefore applies the same SPDX prepend and the same `goimports` normalization
before it compares.

The target's `PATH="$(go env GOPATH)/bin:$PATH"` prepend is part of the producer as well, so the test
reproduces it before invoking `buf` (`Makefile` line 94, with `GOPATH_BIN` defined at `Makefile` line 20).
`schemas/buf.gen.yaml` lines 16 through 21 declare `protoc-gen-go` and `protoc-gen-go-grpc` as `local:`
plugins, which `buf generate` resolves from `PATH`, and `scripts/setup-dev.sh` lines 390 and 391 install
both into `GOPATH/bin` while `buf` itself is installed as a system package at line 308. The producer
therefore depends on four external binaries, which are `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`, and
`goimports`, and the absence of any one of them is the condition proposal 0065 requires the test to record
as `UNVERIFIED`. Without the prepend, a tree whose plugins live only in `GOPATH/bin` would fail the test
while `make generate-proto` succeeds there. The test, its cases, and the workflow changes that install
those binaries in the enforcing environment are all built by proposal 0065; this paragraph states the
producer's dependency because the regeneration §4.6 requires of a pass that rewrites a `.proto` source is
what makes the test an exit criterion of the sub-steps below.

### 4.7 Measured populations, and the residual that catches what they miss

This sub-section governs every place this proposal states a measured fact about the tree: a count of
occurrences, a list of files, or an enumeration of the spellings and directories a class covers. Those
statements are the product of repeated measurement and they are worth keeping. What follows changes their
status rather than their content, because a measured fact about a moving tree is true on the day it is
written and drifts afterwards, and the drift is unbounded.

**Measured populations are not normative.** Every count and file list in this proposal is a scale
indicator. It tells a reader the order of magnitude of a class so the reader can judge whether a
mechanism is proportionate, and it carries the commit it was measured at and the command that
reproduces it. It does not define the class, and it is not a claim the implementation must preserve.

**No gate, exit criterion, or register baseline may key off a number stated in prose.** Each computes the
number it needs at the moment it runs. A sub-step whose exit criterion is "the register is empty"
computes emptiness; it never compares against a figure written here. While a gate or an exit criterion
depends on a stated count, a count that has drifted is a genuine defect, and there is no end to
finding them. Once nothing depends on the prose figure, a drifted figure is a documentation nit that
changes no behavior.

**Every enumerated class carries a residual.** An enumeration in this proposal lists the class members
known at the time of writing. It is the fast path and the seed for the class's residual register. It is
not the definition. Each class is defined by a deliberately broad predicate, and anything that matches
the predicate and appears in neither the enumeration nor the class's residual register is a **residual**.

**A residual fails the build.** It is never skipped, and it is never silently absorbed. The residual gate
(built by proposal 0065, with its cases there) computes, per class, the set matching the broad predicate minus the enumerated members minus the
members the class's residual register records, and fails tier 0 on a non-empty remainder, naming each
member and its class. Its read domain is stated rather than left to the implementation, because a gate
whose domain is the whole tree cannot land green. The residual scan reads a tracked file unless one of
three exclusions covers it. The first is the read exclusion §4.6 states, which is `proposals/`, the
historical audit records `BUILD-GAPS.md` and `TEST-GAPS.md`, and the two root planning documents
`gateway-runtime-comms.md` and `gateway-runtime-comms-remediation.md`, excluded for the reason N3 gives,
together with every `testdata/` directory, excluded for the fixture reason §4.6 states, so the residual
scan does not report a gate's own fixture as an unclassified member of the class that fixture exercises.
The second applies to the reserved-phrase class and the identifier class, whose scan additionally excludes
the root-level records N3 names, `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`, so the
residual scan for a class ranges over the same domain as that class's pass and gates. The third is every
register a class's predicate would otherwise match as tree content, which is each
`tests/registers/residual-<class>.yaml` together with the pass or baseline register the class's own gate
already excludes for the same reason. That third exclusion rests on the argument §4.6 makes, which is that
a gate cannot read its own baseline as tree content: a residual entry holds a copy of its member's text,
so scanning the register would report that copy under the register's own path as a further residual, and
seeding an entry for it would add another copy, so the seeding would not converge. Closing a residual
means one of two things: the member belongs to the class, so it
joins that register as `in-class` and the pass handles it; or it does not, so it joins that register as an
explicit exclusion with a reason. Both are recorded. Neither is a silent widening of the gate. An
`excluded` entry is permanent, because the member never belonged to the class. The lifetime of an
`in-class` entry depends on whether its member can stop matching the class's broad predicate, rather than
on whether a `scripts/specshift` pass runs over the class. In a class whose members can leave it, which
are the two line-citation classes, the reserved-phrase class, the identifier class, the anchor class, the
change-graph coverage class, and the skip-reason class, an `in-class` entry is transitional. In the first
five of those the route out is the pass: registering the member as in-class is how the pass reaches it,
and once the pass has handled it the member no longer matches the predicate. In the change-graph coverage
and skip-reason classes the route out is the remediation the gate exists to drive, which is a tracked path
gaining a glob key in `tests/change-graph.json` and a skip reason being rewritten to open with one of the
categories, and the member stops matching the predicate at that point. In both routes the `in-class` entry
is removed in the same run in which its member stops matching, on the same downward rewrite the class's
own baseline already performs, so ordinary coverage work does not leave a dead entry behind and does not
turn tier 0 red. In the generated-artifact class alone an `in-class` entry is permanent for the same
reason an exclusion is, because a generated file keeps matching the predicate for as long as it exists and
the class's driver is a denylist rather than a rewrite.

The residual check reads a register of its own per class, at `tests/registers/residual-<class>.yaml`,
seeded in the same sub-step that seeds the class's pass or baseline register and held separately from it.
The two record different things. A pass register drives a rewrite and is keyed for that rewrite, and
several of them are transitional: the citation baselines are per-file counts and per-citation entries that
SPEC-4 empties, the sense maps are keyed by file and occurrence, and the change-graph baseline is keyed by
path prefix and is rewritten downward only. A residual register records a triage decision. An entry is a
member, its class, a disposition of `in-class` or `excluded`, and a reason, with no owner, no opened-at
date, no expiry, and no blocker. The argument for that schema is the one §4.4 makes for
`tests/claim-map.json`: an explicit exclusion is a permanent statement about the tree, so it has no date
on which it becomes wrong nor an open item a blocker could resolve to, and an in-class entry is retired by
the event that takes its member out of the class where the class has one and is otherwise as permanent as
an exclusion, so in neither case is there a date on which the entry becomes wrong or an open item a
blocker could name, and under the shared contract's expiry and blocker ratchet rules every such entry
would fail.
In a class whose members can leave it, an `in-class` entry is removed from the residual register in
the same run in which its member stops matching the broad predicate, on the run-completeness criterion
§4.6 states for the citation baselines and SPEC-1 states for
`tests/registers/reserved-phrase-senses.yaml`, so the register
carries no entry for a member the predicate no longer matches. In the generated-artifact class the entry
stays for as long as the member matches the predicate, and removing it would re-expose the member as a
residual on the next run. That is why the two dispositions are
recorded distinctly rather than as one population. The precedent for holding a population in a register of
its own rather than in the shared exception register is the one §4.6 makes for the citation baselines. The
residual check for a class lands in the sub-step that seeds that class's registers, per §3.5.

The broad predicates are deliberately over-broad and are triaged rather than made precise. For the
citation classes the predicate matches any occurrence of a section sigil or a section path adjacent to a
line-number token, without committing to a separator, a range form, or a comment dialect. Adjacency
tolerates the continuation join §4.6 states, so the two tokens count as adjacent when a comment marker and
a newline sit between them, and a wrapped spelling the enumerated form misses is still reported as a
residual. For the
generated-artifact class the predicate is the union of a generation marker in the file header, a
generation declaration in top-level document metadata where the format carries no comment syntax, and
membership in the output set of a producer §4.6 names, since no one signal covers the tree. Precision is what
failed: a precise predicate is another enumeration wearing a regular expression, and it misses the same
tail. Over-breadth plus triage is what makes the class complete, and the register is where the triage is
recorded.

This is why the enumerations stay. They make the common case fast, they document what is known, and they
seed each residual register so the gate lands green on a real population rather than on an empty one. The
residual is what makes them safe to be incomplete.

### 4.8 The heading set, and the anchors every index row resolves to

Every heading `spec/28` and `spec/29` land is fixed here, with its exact title and the anchor that title
produces. This subsection exists because three separate obligations in this proposal need the anchors
before the sections that carry them are written: the `spec/README.md` index rows, the
`tests/spec-map.json` keys, and the heading walker's predicate that a row's anchor resolves. The headings
land across two sub-steps: proposal 0067 created `spec/28_communication-channels.md` carrying §28.1 through
§28.4, and SPEC-3 appends §28.5 through §28.8 to it and creates `spec/29_communication-scenarios.md`. This
proposal and the remediation steps after it also cite a §28.5 card by its subsection number before SPEC-3
runs. Without a title stated in advance, a row's link text and its anchor would be invented in whichever
sub-step happened to write the row, and an invented anchor resolves to nothing. Fixing the titles once,
here, is what keeps every row and the heading it points at agreeing across sub-steps.

The anchor is derived from the title by the slug algorithm the tree's existing anchors follow: lowercase
the heading text, delete every character that is not a letter, a digit, a space, a hyphen, or an
underscore, and then replace each remaining space with one hyphen. A deleted character is not replaced by
a hyphen, so a dot inside a section number vanishes, a punctuation mark standing between two spaces
leaves two consecutive hyphens, and a punctuation mark standing between two word characters leaves none.
Three rows of `spec/README.md` fix the rule, and they are the three cases the derivation and the heading
walker's anchor-resolution step both have to reproduce: line 147, where `24.19.1 Image Management` carries
`#24191-image-management`; line 18, where `4.4 Event / Checkpoint Store` carries
`#44-event--checkpoint-store`; and line 53, where `9.3 Connector Definition and OAuth/OIDC` carries
`#93-connector-definition-and-oauthoidc`. A rule that replaced each run of non-alphanumeric characters
with a single hyphen would compute `#44-event-checkpoint-store` and
`#93-connector-definition-and-oauth-oidc`, so a walker built from it would report those two correct index
rows as non-resolving and would be red at SPEC-1's exit on rows this proposal stages no correction for. It
would also fail to reproduce three anchors this proposal writes and relies on, which are
`adapter--runtime-protocol-intra-pod`, `protocol-reference--message-schemas`, and
`messageenvelope--unified-message-format`. The titles below are chosen to survive the derivation without
ambiguity, so no two anchors in either file collide.

| Heading | Anchor | Level |
|:--|:--|:--|
| `28. Communication Channels` | `#28-communication-channels` | 2 |
| `28.1 Naming law` | `#281-naming-law` | 3 |
| `28.2 Taxonomy and axes` | `#282-taxonomy-and-axes` | 3 |
| `28.3 Registers` | `#283-registers` | 3 |
| `28.4 Claim register` | `#284-claim-register` | 3 |
| `28.5 Contract cards` | `#285-contract-cards` | 3 |
| `28.5.1 Gateway-to-pod` | `#2851-gateway-to-pod` | 4 |
| `28.5.2 Pod-to-gateway` | `#2852-pod-to-gateway` | 4 |
| `28.5.3 Intra-pod` | `#2853-intra-pod` | 4 |
| `28.5.4 Inter-replica` | `#2854-inter-replica` | 4 |
| `28.5.5 Pod-egress` | `#2855-pod-egress` | 4 |
| `28.5.6 Control-plane` | `#2856-control-plane` | 4 |
| `28.5.7 Gateway-to-store` | `#2857-gateway-to-store` | 4 |
| `28.6 Exclusivity and concurrency model` | `#286-exclusivity-and-concurrency-model` | 3 |
| `28.7 Wire-contract artifact register` | `#287-wire-contract-artifact-register` | 3 |
| `28.8 Failure and degradation matrix` | `#288-failure-and-degradation-matrix` | 3 |
| `29. Communication Scenarios` | `#29-communication-scenarios` | 2 |
| `29.1 Participants and trace notation` | `#291-participants-and-trace-notation` | 3 |
| `29.2 Session start` | `#292-session-start` | 3 |
| `29.3 Interactive message send` | `#293-interactive-message-send` | 3 |
| `29.4 Interrupt, terminate, and delete` | `#294-interrupt-terminate-and-delete` | 3 |
| `29.5 Checkpoint capture` | `#295-checkpoint-capture` | 3 |
| `29.6 Restore and resume` | `#296-restore-and-resume` | 3 |
| `29.7 Gateway drain` | `#297-gateway-drain` | 3 |
| `29.8 Coordinator handoff and crash takeover` | `#298-coordinator-handoff-and-crash-takeover` | 3 |
| `29.9 Agent pod eviction` | `#299-agent-pod-eviction` | 3 |
| `29.10 The concurrent-session pod` | `#2910-the-concurrent-session-pod` | 3 |

The §28.5.1 through §28.5.7 order is fixed by this table over the closed boundary set §28.2 states, and it
is normative here rather than
incidental. Later remediation steps and this proposal's own worked example cite a card by its subsection
number, so a different order silently redirects every such citation to the wrong card. §2's worked
handle `§28.5.2 CH-ADAPTEREVENTS` resolves against this table, and it is the check that the order landed
as stated: `CH-ADAPTEREVENTS` carries the pod-to-gateway boundary, so a §28.5.2 that is not pod-to-gateway
means the order drifted.

§28.8 is the one subsection with no antecedent anywhere in the tree. Every other §28 subsection relocates
or restates material that exists today, so its content is carried in by a reduction. The failure and
degradation matrix has no source section to carry, so SPEC-3 authors it from the per-channel degradation
behavior the contract cards state: one row per channel identifier, naming what the channel does when its
peer is absent, when its transport fails mid-stream, and when the holder of its exclusivity constraint
changes, plus the observable the operator sees in each case. It is authored rather than relocated, and the
completeness check is that every identifier in the §28.3 channel register has exactly one row.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it is documented |
|:--|:--|:--|
| A reference is missed by the identifier rewrite | The identifier-resolution gate fails at tier 0, naming the file. The rename is not complete until the gate is green, so a miss blocks the merge rather than shipping | §28.1 states that one identifier resolves to one spelling tree-wide |
| A class member exists in the tree that no enumeration in this proposal lists | The residual gate fails tier 0 and names the member and the class that should own it, so the build stops rather than the member being silently skipped. Closing it records the member in the class's residual register as in-class or as an explicit exclusion with a reason. A count stated in this proposal that has drifted since it was measured is NOT this case and is not a defect, because nothing keys off a prose figure | §4.7 states the rule; TEST-1 states the gate's cases |
| A citation in `BUILD-GAPS.md` or `TEST-GAPS.md` points into a range this proposal shifts | The citation goes stale and stays stale. Both files are excluded from every pass, the resolver, the ratchet, and the residual scan, so nothing rewrites them and no gate reports them. They are historical audit records, and rewriting their citations would edit the record of what was found at the time it was found. A reader of either file resolves a stale citation through the successor pointer in the reduced section | §11 states the exclusion and this row states the outcome. No specification or `docs/` page changes, because neither file is reader-facing documentation |
| A reader follows a stale section number to a reduced section | The section names its successor heading and the identifiers that moved | The successor-pointer sentence, normative in the reduced section |
| A third-party runtime reads the renamed manifest key | The runtime author contract is updated in the same change, and the manifest emitter and the three SDKs move together, so a runtime built against the published SDK stays correct. The tier-3 round-trip test in SPEC-2 is what establishes that they moved together, because review cannot check a cross-language parse | The runtime author guide, amended in the wire-contract sub-step, and the SPEC-2 tests block |
| A deployer pins an older runtime image built against the old manifest key | The key rename is a breaking change to the runtime contract and is not compatibility-shimmed, per the repository's no-backward-compatibility rule for a pre-deployment platform | Stated in Non-goals |
| A sentence carries the canonical spelling and still describes the wrong mechanism | No gate detects it, because the naming lint reads spellings and the identifier gate reads resolution. `tests/registers/reserved-phrase-senses.yaml` carries an entry per reserved-phrase site and the name pass fails a site with no entry, so the reviewer resolves each ambiguous sentence before the substitution runs rather than after. TEST-1 pins that fail-closed path with its own cases. The sites whose current text names the wrong participant are corrected by hand in SPEC-1, and the wrong artifact descriptions and the wrong artifact-scope sentence at `spec/15_external-api-surface.md` line 1463 in SPEC-2 | §3.2, SPEC-1, SPEC-2, and the hand-authored rows in the §3.4 class table |
| The naming lint cannot land green because prose violations remain | The lint's domain is the same domain the name pass walks and both apply the continuation join §4.6 states, so SPEC-1 writes every site the lint reads, including a phrase wrapped across two comment lines, and no violation survives the sub-step. The gate never widens and suppression is not used | SPEC-1's name-pass domain and the TEST-1 sentence stating the lint's domain |
| A line citation is written after the retirement | The ratchet fails the file at tier 0 | §28.1 N8 and `.claude/rules/` |
| Content moves again after this proposal | The successor pointer names the heading and the identifiers rather than a line, so it survives a further move | §28.1 N8 |

## 6. Proposed changes

### SPEC-1. The naming law, the registers, and the prose correction

**Target:** `spec/28_communication-channels.md` §28.1 through §28.4 (existing; created by proposal 0067,
which this sub-step does not re-author), `spec/03_high-level-architecture.md`,
`spec/README.md`, `tests/spec-map.json`, `tests/spec-map-exceptions.yaml`,
`tests/registers/reserved-phrase-senses.yaml`, the reserved bare noun phrases
wherever they appear in the domain N3 states, which is `spec/`, `docs/`, `schemas/`, the Go doc comments
of every tracked Go file, and the tracked root-level contract documents `README.md` and `TESTING.md`,
`pkg/proto/` (regenerated rather than rewritten, because the name pass writes `schemas/*.proto`),
and the `tests/tier11_docs/` reconciliation tests that pin the
rewritten sentences as string literals. The naming lint lands in this sub-step, because this is the
sub-step that removes every site the lint reads.

**Rationale:** The channels carry no canonical names today, and two unrelated mechanisms are both called a
lifecycle channel, so every later step that names a channel has nothing to name it by. The naming law, the
registers, and the reserved-word removal are the vocabulary the rest of this proposal and the remediation
steps after it cite.

**Change (staged description).** Proposal 0067 landed §28.1 through §28.4, which are the naming law N1
through N8, the taxonomy and its axes, the three registers, the naming table, and the claim register, so
this sub-step authors no heading there and its `spec/28` obligation is to confirm the four subsections are
present before the name pass runs, because the pass indexes the declared identifier space out of that file.
§28.1's statement of N4 carries the deferral clause §4.1
states, which is that N4 binds the metric-label namespace and that the remediation step adding the adapter
metrics endpoint and the catalog entries, R12, is the step that discharges the metric half, with a
claim-register row naming R12. Without that clause §28.1 lands binding the metric-label namespace while
the two adapter metrics at `pkg/adapter/metrics.go` lines 71 and 79 keep the retired spelling and nothing
in `spec/` or in the register records that the metric surface is exempt until R12. Each register carries one row per link, channel, or register
entry, per §4.3. Correct the `spec/03` diagram line at
`spec/03_high-level-architecture.md` line 29 so it stops standing for several channels and one protocol
under a single arrow, and add a pointer to §28. The edit keeps the mTLS assertion, which §10.2, NET-060 in
§10.3, §15.4, and §4.7 all require and which no edit in this proposal retracts; the podspec's missing
certificate material is recorded as a claim-register row with status `ABSENT` naming the later step that
wires it, per §4.4. Retracting the requirement instead would mean staging matching edits to
`spec/10_gateway-internals.md` lines 190 and 321, `spec/15_external-api-surface.md` lines 1456 and 2554,
`spec/04_system-components.md` lines 641 and 856, and the documentation pages that state it
(`docs/api/internal.md` line 13 and its "mTLS requirements" section at line 44,
`docs/getting-started/architecture.md` lines 75 and 417, and `docs/reference/adapter-contract.md`
line 18), which this proposal does not do. Remove the reserved bare noun phrases by script, driven by
`tests/registers/reserved-phrase-senses.yaml`, with the pass failing a site the register does not resolve.

The name pass walks the whole domain N3 states in one run rather than `spec/` and `docs/` alone, under the
generated-file exclusion §4.6 states. Its matcher applies the comment-marker continuation join §4.6 states
before it applies either banned spelling, per N3, so a reserved phrase wrapped across two consecutive
comment lines is one site the pass rewrites and one site the register resolves rather than two half-sites
neither reads. The tree carries that wrap inside the domain N3 names, in `schemas/` at
`schemas/lenny-adapter.proto` lines 1219 and 1220, which read `// lifecycle channel; false when the pod's
runtime has no lifecycle` then `// channel`, and whose comments `protoc` mirrors into
`pkg/proto/adapter/v1/lenny-adapter.pb.go` lines 4623 and 4624, and in Go doc comments at
`pkg/adapter/mcpruntime.go` lines 238 and 239, `pkg/adapter/usage.go` lines 237 and 238,
`pkg/embedded/stack/catalog.go` lines 193 and 194, `pkg/gateway/session/executor/subprocess.go` lines 34 and
35, and `sdks/runtime/go/runtime/lifecycle_test.go` lines 17 and 18 together with lines 102 and 103. Without
the join, SPEC-1 would exit with the collision standing in a shipped wire artifact and its generated stubs
while the exit criterion below reported zero. That domain is exactly the naming lint's domain, which is what TEST-1
requires, and the tree carries the banned phrases outside `spec/` and `docs/` in quantity. In the
space-separated spelling that is 65 occurrences
across 11 files in `spec/`, 55 across 16 files in `docs/`, 10 across 4 files in `schemas/`, 124 in Go doc
comments across 55 tracked Go files, and 10 in `README.md` and `TESTING.md`.

Run `make generate-proto` after the name pass, because `schemas/lenny-adapter.proto` is one of the four
`schemas/` files the pass rewrites and `protoc` copies its comments verbatim into the committed stubs under
`pkg/proto/`, which no pass writes. The affected sites are `schemas/lenny-adapter.proto` lines 31, 137, 138,
1219, 1578, and 1589, mirrored at `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 73, 166, 167, 518,
611, and 612 and `pkg/proto/adapter/v1/lenny-adapter.pb.go` lines 4623, 6232, and 6283. The occurrence at
`schemas/lenny-adapter.proto` line 1219 continues onto line 1220, and its mirror at
`pkg/proto/adapter/v1/lenny-adapter.pb.go` line 4623 continues onto line 4624, so the pass consumes each of
those pairs as one site under the continuation join rather than leaving the second line unwritten. The tier-0 proto
no-drift test proposal 0065 adds is therefore an exit criterion of this sub-step as well, on the rule §4.6 states
that the sub-step which rewrites a producer's source runs that producer and takes its no-drift test as an
exit criterion. Without the regeneration this sub-step exits with that tier-0 test red on every stub the
comment rewrite reached.

The matcher covers the hyphenated compound as well, per N3, because the tree carries that spelling in
normative text the space-separated measurement misses. Measured under the compound spelling the tree
carries 6 further occurrences across 4 files in `spec/`, 3 across 2 files in `docs/`, 29 in Go doc
comments across 18 tracked Go files, none in `schemas/`, and 2 in `TESTING.md`. The `docs/` figure is the
compound population net of the markdown anchor identifiers N3 places outside the matcher: `docs/` carries
6 compound occurrences across 4 files, and three of them are anchor identifiers rather than prose, which
are the kramdown attributes at `docs/reference/glossary.md` line 207 and `docs/api/internal.md` line 318
and the same-page fragment link at `docs/api/internal.md` line 229. The three prose sites the pass
rewrites are `docs/reference/adapter-contract.md` line 84 and `docs/runtime-author-guide/lifecycle.md`
lines 69 and 319. No `spec/` compound occurrence is an anchor identifier, so the `spec/` figure is
unaffected. `spec/18_build-sequence.md` carries three of them, at lines 164, 165, and 408, and carries no
space-separated occurrence at all, so a matcher restricted to the space-separated form leaves that file
outside the pass, outside the seeded register, and outside §11's touched-file list while §28 and the
rewritten sections name `CH-RUNTIMEOPS`. Five tracked Go files are in the same position, which are
`pkg/adapter/credentials.go`, `pkg/gateway/runtime/watchdog/watchdog.go`,
`tests/tier11_docs/budget_extension_trigger_consistency_test.go`,
`tests/tier4_integration/credential_test.go`, and
`tests/tier8_chaos/credential_rotation_ceiling_test.go`. Both figures are measured under N3's stated
domain, which is a Go doc comment in a tracked Go file, so an occurrence carried in a Go string literal is
outside them. `pkg/ctlcli/runtime.go` line 424 carries the compound in the `runtimeValidateUsage` raw
string literal that `lenny runtime validate` prints as operator-facing help text, which the name pass does
not write and the naming lint does not read. That site is out of scope on the same ground as the other
Go string literals, and this proposal leaves the printed help text unchanged. The `spec/` population the
register is seeded against is
therefore 71 occurrences across 12 files, and the exit criterion below is a search for each reserved bare
noun phrase in both spellings under the continuation join. A pass
scoped to `spec/` and `docs/` would leave the remaining sites with a gate that reads them and no pass that
writes them, which is the failure the root-contract-document class already exists to prevent, and neither
escape is available: the pass is fail-closed on an unregistered site, and the shared exception register
fails an entry whose `blocker` does not resolve to an open item. `tests/registers/reserved-phrase-senses.yaml`
is therefore seeded against that whole population rather than against the `spec/` occurrences alone. The
identifier pass, which is a different pass on a different register, still runs in SPEC-2.

The Go doc-comment population is stated over every tracked Go file rather than over `pkg/`, `cmd/`, and
`sdks/`, because 23 of the 124 occurrences sit in 9 files under `tests/`, among them
`tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 8, which is already a target of
this sub-step for the spec prose it pins, `tests/tier3_contract/sdks/runtime_sdk_test.go` line 338, and
`tests/tier4_integration/credential_lifecycle_test.go` line 11. A `// spec:` or `// diagnosis:` comment
under `tests/` is a Go doc comment in N3's sense, and the naming lint reads it, so a pass scoped to the
three product directories would leave those 23 sites red with no writer, which is the failure this
paragraph exists to prevent one directory over. `migrations/` carries no occurrence.

The register's value space is the whole §28 identifier space rather than the channel register alone,
because several reserved-phrase sites denote a link. Two distinct gRPC connections default to port 50051,
so a port-50051 site is resolved from the direction the surrounding text describes rather than from the
port. `LNK-POD-GRPC` in the naming table is the gateway-to-pod `Adapter` service on `adapter.grpcPort`,
which the gateway dials at the pod IP and which carries `CoordinatorFence`, `NegotiateVersion`,
`CH-ADAPTEREVENTS`, and `CH-PODHEALTH` on the one connection (`pkg/adapter/holdstate.go` lines 54 through
59; the ingress site at `spec/13_security-model.md` line 72 names that port).
`LNK-GWCONTROL` is the pod-to-gateway `GatewayControl` service on `gateway.grpcPort`, which the gateway
hosts and the adapter dials (`schemas/lenny-adapter.proto` lines 230 through 247). The normative
default-deny egress prose and the rendered NetworkPolicy at `spec/13_security-model.md` lines 79, 92, and
100 all sit inside `allow-pod-egress-base` and describe the pod's egress allowance, so each resolves to
`LNK-GWCONTROL`, as does `docs/getting-started/architecture.md` line 506 in documentation prose.
Restricting the register to channel identifiers would either abort the pass at every such site, leaving
SPEC-1 unable to complete, or narrow a security-normative sentence to one of the conversations the link
carries.

One site resolves to two identifiers rather than one, so a register entry carries one or more identifiers
and the name pass substitutes each at the position the entry records. The mTLS handshake metric definition
at `spec/16_observability.md` line 51 labels the histogram by `direction` and instruments the two values
because they are distinct paths, which the definition states: the gateway originates the adapter gRPC
dial, and the pod originates the LLM-proxy connection. The LLM proxy runs on port 8443, which
`spec/13_security-model.md` line 79 excludes from the base allowance and which the naming table records as
`CH-LLMPROXY` on the pod-egress boundary. That site is recorded in
`tests/registers/reserved-phrase-senses.yaml` as denoting `LNK-POD-GRPC` for the `gateway_to_pod`
direction and `CH-LLMPROXY` for the `pod_to_gateway` direction, and the substitution names both. N5 keeps
the link and channel identifier spaces free of a shared stem, so the substituted identifier stays
unambiguous.

The sense mapping is a migration register under `tests/registers/` rather than a column in normative
§28.3, for three reasons. It is keyed by occurrence site rather than by channel, which the one-row-per-
entry register schema in §4.3 cannot carry, and the measured baseline is roughly 236 reserved-phrase
occurrences across the whole N3 domain against roughly twenty-two channels, so many sites map to one
channel row. It is an edit-site list of a script-driven class, which §3.4 states should not appear in the
applied change. It is also stale once the pass has run over its whole domain, because the pass removes
every phrase the register indexes, so normative text would enumerate sites that no longer contain the
phrase.

The register is emptied at the end of SPEC-1, and its entry criterion is run completeness measured against
the tree rather than the completion of the sub-step, which is that a search for each reserved bare noun
phrase under the continuation join over the whole domain N3 names returns zero occurrences outside a
markdown anchor identifier, which is the population the naming lint reads. The search applies the join
because a criterion measured line by line reports zero while a wrapped occurrence stands, which is the
outcome §4.6 records for a line-oriented scan over the citation classes. That is the same criterion SPEC-4 uses
before emptying `tests/spec-anchor-moves.json`, and it is stated that way because an empty register is also
what a pass that resolved nothing leaves behind, and because a register emptied while sites remain leaves
the naming lint red with no writer available. SPEC-2's identifier pass reads
`tests/registers/identifier-senses.yaml` rather than this register, so the retirement does not strand it.

Correct by hand, rather than by substitution, the spec sentences whose current text names the wrong
participant, because a substitution turns each of them into a precise false statement: the interrupt
sentences at `spec/07_session-lifecycle.md` line 324 and `spec/15_external-api-surface.md` line 1755, and
the slot-failure sentence at `spec/05_runtime-registry-and-pool-model.md` line 540.

Eleven files under `tests/tier11_docs/` pin spec prose as Go string literals through `specSection`,
`requireLine`, and `requireAllContain`, and those literals are neither `spec/`, `docs/`, `schemas/`, nor a
Go doc comment, so the name pass does not reach them and the naming lint does not see them. Two further
files pin spec heading slugs and one intra-spec markdown link through a `specCrossRef` table and a
`mustContain` list rather than through any of those three helpers, so the class register is stated over
every Go string literal under `tests/tier11_docs/` that names a spec heading slug, an intra-spec markdown
link, or pinned spec prose, rather than over the three helper names. The pass is extended to all of them
and tier 11 is the exit criterion of this sub-step. The rewrite of
`spec/04_system-components.md` line 489 is the concrete case: its "per-pod adapter-to-gateway control
channel" is exactly the phrase N3 bans, and
`tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69 asserts that clause verbatim.

Proposal 0067 wrote the `spec/README.md` table-of-contents rows for `spec/28` and its §28.1 through §28.4
subsections, at the depth the existing entries use, with the link text and the anchors §4.8 fixes, so this
sub-step confirms those five rows are present and adds any that are missing. Take the link text and the
anchor for each row from the heading table in §4.8, which fixes the title and the derived anchor of every
§28 and §29 heading, so these rows carry `28. Communication Channels`, `28.1 Naming law`,
`28.2 Taxonomy and axes`, `28.3 Registers`, and `28.4 Claim register` with the anchors that table states.
Proposal 0067 created `spec/28_communication-channels.md` with those five
headings and wrote those rows before this sub-step runs, so every row resolves and no row in this proposal
precedes its target file. Write for each of those headings the other
half of the walker's predicate as well, which is a `tests/spec-map.json` key naming the tests that encode
the heading, on the rule SPEC-4 states for the numbered subsection headings it inserts over existing prose:
a heading whose section is written at the same sub-step's exit takes a key. Where the tests a key would
name do not exist until TEST-1 adds the gate cases, the heading takes a `tests/spec-map-exceptions.yaml`
entry instead, under the `pending-implementation` reason class proposal 0065 adds with its `blocker` and
`opened_at` fields, with the `blocker` naming TEST-1 and TEST-1 replacing the entry with a key in the same
change that lands those cases. No entry for §28.1 through §28.4 names SPEC-3, because SPEC-3 writes none of
that content. The file is
hand-maintained, has no generator, and already carries the §28 rows proposal 0067 appended, so a heading
appended without a row is invisible to a reader scanning the index.

Seed the heading walker's predicate to green in the same sub-step, which is a bounded job rather than a
register entry. The seeding is stated over the walker's whole domain, which is every `## N` heading, every
`### N.M` heading, and every deeper heading the index already carries, rather than over the `### N.M`
headings alone. The index carries `## N` and `### N.M` rows, with `spec/README.md` line 147 as its only
level-4 row, and 49 `### N.M` headings have no row today: the forty §18 build phases, §17.8.1 through
§17.8.6, §16.1.1, §4.0, and §24.0. Write those rows. Fifty of the same headings have no `tests/spec-map.json`
key; write a key for each, or an entry in `tests/spec-map-exceptions.yaml` under the
`pending-implementation` reason class proposal 0065 adds, carrying the `blocker` and `opened_at` fields that
class requires. `24.19.1 Image Management`, the one deeper heading the index already carries
(`spec/README.md` line 147) and therefore the one deeper heading inside the walker's domain, needs the
same treatment: `tests/spec-map.json` carries a `24.19` key and no `24.19.1` key, and
`tests/spec-map-exceptions.yaml` carries no entry for it, so it gets a key or an exceptions entry under
the same rule. Without it the walker's spec-map half is red at SPEC-3 on a heading no other instruction in
this proposal reaches, and the standing rule forbids widening the gate or suppressing the finding.

Add `.claude/rules/channel-naming.md` stating N1 through N8. The file sits outside the domain N3 names,
so it also carries the two banned spellings verbatim, as the specimen authors and the naming lint's
matcher are checked against. That is the assertion §28.1's statement of N3 makes about this file, so the
file has to carry the spellings for the section to be true on landing.

**Not in scope for this sub-step.** The Go file and symbol renames, which belong to the wire-contract
sub-step so exactly one change moves each file, and which carry the register re-keying §4.6 states. The
metric renames, which are the two metric names at `pkg/adapter/metrics.go` lines 71 and 79 and which are
deferred to R12, the step that first makes those metrics observable. §28.1 states that deferral as part of
N4, per §4.1, and SPEC-3 seeds the claim-register row with status `ABSENT` and `deferral_id` R12.

### SPEC-2. The wire contract change

**Target:** `schemas/lenny-adapter.proto`, `pkg/proto/` (regenerated rather than rewritten),
`schemas/lenny-adapter-jsonl.schema.json`, `spec/28_communication-channels.md`, for the naming-table rows
this sub-step writes, the artifact-scope sentence at
`spec/15_external-api-surface.md` line 1463, the normative field
tables that name the colliding key, the adapter manifest emitter, the second manifest emitter in the
external-adapter compliance harness, the three runtime SDKs, the adapter flag, the runtime author guide,
`docs/reference/glossary.md`, the tracked root-level contract documents `TESTING.md` and `README.md`, `tests/registers/identifier-senses.yaml`,
`tests/registers/line-citations.yaml`, `tests/registers/line-citation-resolution.yaml`,
`tests/change-graph.json`, and `tests/spec-map.json`, for the file
keys and path entries of the renamed files and for the `::<symbol>` references naming symbols the pass
renames, the gRPC full-method string
literals in `pkg/adapter/holdstate.go` and `pkg/adapter/holdstate_test.go`, and
`tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go`, whose exact-field-count assertion the
field additions below widen. The identifier-resolution gate
and the `coordinatorHoldAllowedMethods` assertion land in this sub-step, because this is the sub-step that
collapses each retired spelling to one canonical identifier and therefore the first at whose exit the gate
can be green.

**Rationale:** The colliding word is embedded in surfaces prose cannot reach, and the runtime author
contract instructs third-party authors to read the key by name. Leaving the machine-readable surfaces
ambiguous while renaming only prose produces a worse state than either alternative.

**Change (staged description).** Rename the two colliding channels to their canonical identifiers across
every machine-readable surface, by script, driven by the naming table in §28.3.

The §28.3 table records the spelling each carrier takes, per N7. This sub-step writes the §28.3
naming-table rows for every spelling it fixes that the table does not already carry, which are the
`CH-RUNTIMEOPS` Go symbol stem and the `pkg/adapter/lifecyclechannel.go` file stem, while the
`schemas/lifecycle-events.schema.json` schema-path row is already carried; it states both spellings here
for the reason the paragraph already gives for the `CH-ADAPTEREVENTS` carriers, which is that each is
derived from a naming rule and the derivation is written down once. The adapter manifest key becomes
`runtimeOps`, in the camelCase form every sibling key in the §4.7 field set uses
(`spec/04_system-components.md` lines 788 through 796), and the adapter flag becomes
`--runtime-ops-socket` in lowercase kebab. The manifest spelling is stated here rather than read off the
plan's scope table, because that table predates N7 and states a target for the key without stating the
form rule it follows.

The `CH-ADAPTEREVENTS` carrier spellings are stated here for the same reason, which is that each one is
derived from a naming rule and the derivation has to be written down once. N3 forbids an identifier stem
that reuses a bound term, and N4 requires the proto RPC name stem and the Go file name stem to be the
channel's identifier. Under N4 the RPC at `schemas/lenny-adapter.proto` line 227 becomes
`rpc AdapterEvents(stream AdapterEventsRequest) returns (stream AdapterEventsResponse)`, the two
message types become `AdapterEventsRequest` and `AdapterEventsResponse`, the gRPC full-method literal at
`pkg/adapter/holdstate.go` line 57 and `pkg/adapter/holdstate_test.go` line 292 becomes
`/lenny.adapter.v1.Adapter/AdapterEvents`, and `pkg/adapter/controlchannel.go` and its test sibling
become `pkg/adapter/adapterevents.go` and `pkg/adapter/adapterevents_test.go`. The §28.3 table records
these spellings. Without them stated, an implementor deriving the rename from the identifier alone can
publish a gRPC method name, two message type names, and a Go file stem that contradict §28.1, and the
identifier-resolution gate then sees `CH-ADAPTEREVENTS` resolving to more than one spelling.

The retired spelling `LifecycleChannel` is two-valued at the identifier level, not only in prose, so the
identifier pass is driven by a per-occurrence register in the same way the name pass is. Both channels
carry that exact Go token inside package `adapter`: `pkg/adapter/lifecyclechannel.go` line 92 declares
`type LifecycleChannel struct` for `CH-RUNTIMEOPS`, and `pkg/adapter/controlchannel.go` line 90 declares
`func (s *Server) LifecycleChannel(stream adapterv1.Adapter_LifecycleChannelServer) error` for
`CH-ADAPTEREVENTS`. A register keyed by channel cannot resolve which occurrence maps to which canonical
identifier, so `tests/registers/identifier-senses.yaml` carries one entry per occurrence site, and the
pass aborts on a site with no entry rather than substituting a default. This mirrors the fail-closed rule
§3.4 row 1 states for the name pass.

The register's trigger is occurrence-scoped rather than channel-count-scoped. A spelling the §28.3 table
maps to exactly one channel still occurs at sites that are not that channel, and a channel-count trigger
substitutes at those sites blind. The verified case is the socket token `@lenny-lifecycle`, which the
naming table renames to `@lenny-runtime-ops` and which the table maps to `CH-RUNTIMEOPS` alone.
`spec/17_deployment-topology.md` line 1530 uses `@lenny-lifecycle.json` as an `az` command-line file
argument (`az storage account management-policy create --account-name <account> --policy
@lenny-lifecycle.json`), where the `@` is the `az` read-from-file prefix and the name is the local
blob-lifecycle policy document the code fence at `spec/17_deployment-topology.md` lines 1510 through 1529
produces. Its two sibling lines still read `file://lenny-lifecycle.json` for AWS at line 1490 and
`--lifecycle-file=lenny-lifecycle.json` for GCP at line 1509. A blind substitution renames a storage
lifecycle-policy file after a runtime-operations channel and names a file the surrounding spec never
produces. That occurrence is recorded in `tests/registers/identifier-senses.yaml` as a not-a-channel site
the pass leaves unmodified, and the identifier-resolution gate reads the same per-context predicate it
already reads for the `coordinatorHoldAllowedMethods` literals, so a permanently correct non-channel
occurrence is not routed through the shared exception register, whose owner and expiry it could never
retire against.

The same socket token is a genuine channel reference in the test-harness contract, which is why the passes
walk the tracked root-level contract documents rather than stopping at `spec/`, `docs/`, and `schemas/`.
`TESTING.md` line 1996 states the runtime-author SDK Full-level conformance battery as "connect to
`@lenny-lifecycle`, capability handshake, checkpoint flow, interrupt flow, credential rotation, deadline
notification", which is the battery this sub-step's tier-10 bullet re-runs over the renamed socket, so
leaving it behind would instruct SDK authors and conformance implementors to dial a socket no adapter
opens. `TESTING.md` lines 788, 858, 874, 993, 1315, 1527, 1972, 1996, and 2248 and `README.md` line 155 carry
the reserved bare phrase, and SPEC-1's name pass has already rewritten them from
`tests/registers/reserved-phrase-senses.yaml`, because its walk covers the whole N3 domain. What this
sub-step writes in those two files is the retired identifier spelling, and `TESTING.md` carries it at two
lines rather than one. Line 1996 holds the socket token. Line 1521 holds three further retired spellings,
which are the schema path `schemas/lifecycle-events.schema.json`, the example-fixture glob
`schemas/examples/lifecycle.*.json`, and the name of the test that validates them,
`tests/tier0_static/schemas_test.go::TestLifecycleEventExamplesValidate`. The §28.3 naming table renames
that schema file, this sub-step's tier-0 schema-bijection bullet renames its fixtures, and N4 carries the
rename into the test name, so all three go stale in the same change. A search of both files for every
retired spelling returns those two lines and no other, so
`tests/registers/identifier-senses.yaml` is seeded against both rather than against the socket token
alone; the pass aborts on a site with no entry, so an unseeded line 1521 would stop the run. Without this the
identifier-resolution gate, whose domain is the whole tree, would see `CH-RUNTIMEOPS` resolving to two
spellings at this sub-step's exit with no pass able to write the file, and the occurrence could not be
recorded as a not-a-channel site because it is a channel.

The other root-level markdown files that carry a retired spelling or the reserved phrase are the ones N3
excludes, which is why the gate's domain and the passes' walk are the same list. `BUILD-PLAN.md` line 259
names the `LifecycleChannel` stream in a build-plan entry, `BUILD-PROGRESS.md` line 30 and
`PROPOSAL-QUEUE.md` lines 289 and 625 carry the reserved bare phrase, and `BUILD-GAPS.md` and
`TEST-GAPS.md` carry both in quantity. Each records what was planned, found, or decided at the time it was
written, so nothing rewrites it and no gate reports it, which is the disposition §5 already states for the
audit records.

A mis-resolved site is not uniformly loud. A mis-mapped Go symbol fails to compile, but a gRPC full-method
string literal does not: `pkg/adapter/holdstate.go` line 57 carries
`"/lenny.adapter.v1.Adapter/LifecycleChannel": true` in the `coordinatorHoldAllowedMethods` allowlist that
the hold-state interceptors read (`// spec: §10.1 line 49.` at line 53), and a pass that resolves it to
`CH-RUNTIMEOPS` rewrites it to a method the proto no longer declares. The pass rewrites the parallel
literal at `pkg/adapter/holdstate_test.go` line 292 identically, so the test stays green while a new
coordinator's control-stream open is rejected during hold state. Every gRPC full-method literal is
therefore resolved from the proto RPC row rather than from the Go type row, and TEST-1 adds a tier-0
assertion over `coordinatorHoldAllowedMethods`, so a mis-resolved literal cannot pass with its own test
rewritten alongside it.

The assertion is stated per service part rather than over the map as a whole, because two of the five
entries are not `adapterv1` methods: `pkg/adapter/holdstate.go` lines 58 and 59 carry
`/grpc.health.v1.Health/Check` and `/grpc.health.v1.Health/Watch`, which come from the standard health
service `pkg/adapter/transport.go` registers from `google.golang.org/grpc/health/grpc_health_v1` and which
have no descriptor under `pkg/proto/`. An assertion requiring every entry to name an `adapterv1` method
would be red on the unmodified tree against two entries that are correct and permanently correct, and the
proposal's standing rule is that a gate never lands green by widening or suppression. The predicate is
therefore: an entry whose service part is `lenny.adapter.v1.Adapter` names a method or stream
`Adapter_ServiceDesc` declares, and an entry whose service part is another service names a method of a
service the adapter registers, which today is only `grpc.health.v1.Health`. A mis-resolved rename
preserves the `/lenny.adapter.v1.Adapter/` prefix, so the failure this assertion exists to catch stays
inside the first branch.

The renamed Go files still carry their line citations at this point, because the line pass that retires
them does not run until SPEC-3 and SPEC-4. `pkg/adapter/lifecyclechannel.go` carries nine and
`pkg/adapter/controlchannel.go` carries five, with five and four more in their test siblings. The
identifier pass therefore rewrites the file keys in `tests/registers/line-citations.yaml` and
`tests/registers/line-citation-resolution.yaml` in the same run that moves each file, per §4.6, so the
ratchet and the resolver see an unchanged count and an unchanged baseline at this sub-step's exit rather
than a path they have never seen.

The rule is stated over every path-keyed test-infrastructure register the pass's file moves invalidate,
rather than over those two registers alone, because a hand-written enumeration of registers is the same
kind of list §3.4 rules out for edit sites. Two further registers are in that position today. The naming
table renames `schemas/lifecycle-events.schema.json` to `schemas/runtime-ops-events.schema.json`, and
`tests/change-graph.json` line 495 carries the old path as a glob key.
`validateChangeGraphFileExistence` stats every glob key and fails the check when one does not resolve on
disk (`cmd/lenny-test/cmd_validate.go` lines 294 through 305), and it runs inside the `validate-maps`
tier-0 check, which hard-fails the tier (`cmd/lenny-test/cmd_run.go` lines 734 and 742), so leaving the key
behind ends this sub-step with tier 0 red. `tests/change-graph-pending.txt` is not the remedy, because it
lists paths committed ahead of their implementation rather than paths that moved. `tests/spec-map.json`
carries the same path in its `schemas` arrays at lines 438, 2360, and 2376, which no check
existence-checks, so those entries would go stale silently.

The same register also carries two symbol references the pass renames rather than moves, and those are
existence-checked. `tests/spec-map.json` lines 2187 and 2370 name
`tests/tier0_static/schemas_test.go::TestLifecycleEventExamplesValidate`, declared at
`tests/tier0_static/schemas_test.go` line 146, which N4 renames with the schema it validates, and lines
2188 and 2385 name `cmd/lenny-compliance/full.go::checkLifecycleHandshake`, declared at
`cmd/lenny-compliance/full.go` line 225, which N4 renames with the channel it exercises.
`validateSpecMapTestFuncs` reads the referenced file and requires a top-level `func <Name>(` declaration
(`cmd/lenny-test/cmd_validate.go` lines 564 and 602 through 617), it is registered in `runValidateMaps`
at line 53, and it runs inside the same `validate-maps` tier-0 check, which hard-fails the tier
(`cmd/lenny-test/cmd_run.go` lines 734 and 747 through 750). Renaming either symbol without rewriting its
references therefore ends this sub-step with tier 0 red. The re-key rule §4.6 states covers both a path
key the pass's file moves invalidate and a `::<symbol>` reference naming a symbol the pass renames, so a
hand-written enumeration is still not what drives it. The identifier pass rewrites every one of them in
the same run, and TEST-1 pins the outcome per register rather than as a single `validate-maps` result,
because `validate-maps` does not read the `schemas` arrays.

Run `make generate-proto` afterwards, because the rename edits `schemas/lenny-adapter.proto` and
`pkg/proto/` is generated output the passes do not write. That target is separate from `make generate`, so
every sub-step whose pass writes a `.proto` file runs it explicitly, which is SPEC-1 for the name pass,
this sub-step for the identifier pass, and SPEC-3 and SPEC-4 for the line pass. The tier-0 proto no-drift
test proposal 0065 adds is an exit criterion of this sub-step.

Correct by hand the artifact descriptions and the one specification sentence that state the wrong
mechanism, because a spelling substitution preserves a wrong sentence:

- `schemas/lenny-adapter-jsonl.schema.json`, whose `description` at line 5 calls checkpoint, interrupt,
  `credentials_rotated`, and `deadline_approaching` a "gateway↔adapter lifecycle channel" carried on the
  gRPC stream. Those frames are `CH-RUNTIMEOPS` frames between the adapter and the runtime over the
  intra-pod socket, so both halves of the sentence are wrong.
- `spec/15_external-api-surface.md` line 1463, which describes the same artifact from the specification
  side and makes the mirrored error: it calls `schemas/lenny-adapter-jsonl.schema.json` the JSON Schema for
  "every adapter↔binary stdin/stdout message ... and every lifecycle-channel message". The artifact
  schematizes no such message. Its `$defs` are exactly `messageEnvelope`, `from`, `heartbeat`,
  `heartbeat_ack`, `shutdown`, `tool_call`, `tool_result`, `response`, `status`, and
  `set_tracing_context`, which is the message list the same sentence already enumerates, and the
  checkpoint, interrupt, `credentials_rotated`, and `deadline_approaching` frames are schematized in
  `schemas/runtime-ops-events.schema.json`, the artifact this sub-step renames from
  `schemas/lifecycle-events.schema.json`. The parenthetical is rewritten to close after
  `set_tracing_context` and to send the runtime-operations frames to
  `schemas/runtime-ops-events.schema.json`, so the two published representations of the same wire artifact
  agree once the corrected `description` above ships. The correction is hand-authored under the §3.4 row
  for a description the collision made wrong: the sense register cannot repair it, because whichever
  identifier it substitutes for the reserved phrase, the sentence stays a precise false statement about
  the artifact's contents, which is the failure mode §3.2 names. SPEC-3 carves this line out of the §15.4
  reduction and SPEC-4 rewrites its `#1541-adapterbinary-protocol` link, so the sentence survives the
  later sub-steps and has to be true when it does.
- The `CheckpointBarrierAck` comment in `schemas/lenny-adapter.proto` lines 166-172, which names the
  stream by the colliding word.
- The RPC doc comment in `schemas/lenny-adapter.proto` lines 223 through 226, which credits the
  gateway-to-adapter gRPC stream with `checkpoint_ready`, `interrupt_acknowledged`,
  `credentials_acknowledged`, and `deadline_approaching`, and sends the reader to §15.4 for the taxonomy.
  Those frames are `CH-RUNTIMEOPS` frames on the intra-pod socket. The stream the comment documents
  carries the event set `pkg/adapter/controlchannel.go` lines 17 through 44 declare, which is
  `RATE_LIMITED`, `AUTH_EXPIRED`, `PROVIDER_UNAVAILABLE`, `LEASE_REJECTED`, `AdapterTerminating`,
  `FINAL_USAGE_REPORT`, and `CheckpointBarrierAck`. The comment is rewritten over that event set, in the
  same vocabulary the `CH-ADAPTEREVENTS` glossary entry below takes, and its pointer is rewritten to the
  adapter-to-gateway events table at `spec/04_system-components.md` lines 679 through 689, which is the
  specification statement of that taxonomy. Where §15.4 names `checkpoint_ready` and
  `interrupt_acknowledged` today, at `spec/15_external-api-surface.md` lines 2158, 2159, 2451, and 2452, it
  states them as intra-pod runtime obligations, and it states no adapter-to-gateway event anywhere.
- The request and response message comment in `schemas/lenny-adapter.proto` lines 1594 through 1598, which
  states that the envelope taxonomy "is defined in lenny-adapter-jsonl.schema.json under the lifecycle
  section in the spec". The envelopes the two messages carry are the same adapter-to-gateway events, which
  that artifact schematizes nowhere: its `$defs` are the stdin/stdout frames listed above, and the
  runtime-operations frames it is wrongly credited with move to `schemas/runtime-ops-events.schema.json` in
  the first two corrections in this list. The comment is rewritten to name the same
  `spec/04_system-components.md` events table as the taxonomy's owner, so both comments on this stream and
  the glossary entry state one source.
- `docs/reference/glossary.md`, whose "Lifecycle Channel" entry (lines 206 through 209)
  defines the gRPC gateway-to-pod stream and then credits it with checkpoint requests and credential
  rotation, which are `CH-RUNTIMEOPS` frames on the intra-pod socket, and with session start/stop and
  workspace notifications, which are separate `Adapter` service RPCs. Split it into one entry for
  `CH-ADAPTEREVENTS`, carrying the vocabulary the handler emits (`pkg/adapter/controlchannel.go`
  lines 18-44: `RATE_LIMITED`, `AUTH_EXPIRED`, `PROVIDER_UNAVAILABLE`, `LEASE_REJECTED`,
  `AdapterTerminating`, `FINAL_USAGE_REPORT`, and `CheckpointBarrierAck`), and one entry for
  `CH-RUNTIMEOPS`, carrying the checkpoint, interrupt, credential-rotation, and deadline frames. Keep the
  existing `{: #lifecycle-channel }` anchor on a redirect stub so no inbound link breaks. N3 places a
  markdown anchor identifier outside the reserved-phrase matcher, so SPEC-1's name pass leaves that
  attribute in place and the stub inherits an anchor that still exists. Without this the
  identifier pass produces a glossary asserting that `CH-ADAPTEREVENTS` carries checkpoint requests, and
  both gates pass it.

The events table the two rewritten proto comments point at sits at `spec/04_system-components.md` lines 679
through 689, above the line 691 opening of the block SPEC-3's §4.7 reduction moves, so it stays in §4.7 and
neither comment joins the class SPEC-3 re-points at a §28.5 card. Both corrections therefore land here in
full, which is also where they belong, because it is this sub-step's identifier pass that renames the RPC
and the two message types inside them and would otherwise leave a precise false statement that
`CH-ADAPTEREVENTS` carries the intra-pod runtime-operations frames, in the published gRPC contract and in
its generated mirrors at `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 242 and 687 and
`pkg/proto/adapter/v1/lenny-adapter.pb.go` line 6327. That is the failure mode §3.2 names. No pass reaches
either comment: the name pass's measured proto population does not include them, neither carries a line
citation, and §15.4 and §4.7 both keep their anchors. The `make generate-proto` run this sub-step already
takes carries both corrections into `pkg/proto/`. Pointing the two comments at
`schemas/runtime-ops-events.schema.json` was rejected, because that artifact schematizes the intra-pod
frames rather than the adapter-to-gateway events these two messages carry.

Add, in the same change and therefore inside the same `make generate-proto` run, the request-message
fields that later remediation steps read. Every operational gateway-to-pod request message gains
`coordination_generation`, which is the fence a stale coordinator's request is rejected on.
`InterruptRequest`, `SignalDeadlineRequest`, `ReportUsageRequest`, and `CheckpointBarrierRequest` each gain
a slot identifier, because a pod serving concurrent sessions receives these per slot rather than per pod.
`ResumeRequest` gains `slot_id` for the same reason. The fields land unread: no handler branches on them at
this sub-step, and each carries a claim-register row with status `UNWIRED` naming the later step that reads
it, so a field that exists and is ignored is tracked rather than mistaken for a delivered capability. Those
rows are seeded by SPEC-3 together with the rest of the register rather than written here, because
`tests/claim-map.json` does not exist before SPEC-3 creates it and §3.5 gives the register's validator
exactly one landing sub-step. SPEC-3's seeding instruction names the field rows explicitly, because the
fields do not exist in the tree before this sub-step adds them and so no status table in the reference
document carries a row for any of them.

Two of the named messages already carry one of the two fields, so the addition is per missing field rather
than per message: `CheckpointBarrierRequest` already declares `coordination_generation` at field 2
(`schemas/lenny-adapter.proto` line 1366) and gains the slot identifier alone, and `CheckpointStart`
already declares `slot_id` at field 6 (`schemas/lenny-adapter.proto` line 1120, asserted at
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` line 153). On the streaming
`Checkpoint` RPC the `coordination_generation` fence lands on `CheckpointRequest`, the client message the
gateway sends, rather than on the `CheckpointStart` arm inside it. The placement is forced by an existing
tier-3 assertion: `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` line 150 requires
`CheckpointStart` to have exactly six fields, while the assertion over `CheckpointRequest` at line 121 reads
the `msg` oneof's arm count alone, so a non-oneof field added to `CheckpointRequest` leaves both assertions
green. Carrying the fence on the request message is also the correct granularity, because the fence applies
to every frame the gateway sends on the stream rather than to the opening frame alone.

One further running tier-3 assertion is exact rather than subset-scoped and goes red on these additions.
`ReportUsageRequest` is both an operational gateway-to-pod request message and one of the four messages
that gain a slot identifier, so it goes from two fields to four, and
`tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` line 63 compares
`fields.Len()` against its `want` slice and aborts with `t.Fatalf` on any count change. That test's `want`
slice therefore gains `coordination_generation` and the slot identifier at their assigned numbers and kinds
in the same change as the proto edit, so the exact-count assertion pins the widened contract rather than the
retired one. `TestReportUsageRequestDefaultCumulativeWireIdentical` in the same file is unchanged, because
proto3 emits nothing for an unset field and the default-read encoding is byte-identical. The other
exhaustive descriptor field-count assertions a tree-wide search returns are
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` line 172 over `RecycleScrub`,
`tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` line 58 over
`CheckpointBarrierResponse`, `tests/tier3_contract/interceptor_proto/contract_test.go` line 41 over the
`interceptorv1` messages, and `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` line
150 over `CheckpointStart`. None of them reads a message this sub-step widens: `RecycleScrub` and
`CheckpointBarrierResponse` are not gateway-to-pod request messages, `interceptorv1` is a separate package,
and the `CheckpointStart` assertion is the one the paragraph above already dispositions. The
`reportusage_wire_test.go` `want` slice is therefore the only one that changes, and re-measuring the
field-addition set against the final message list re-runs this check over that list. Tier 3 is an exit
criterion of this sub-step for that reason.

They land here rather than in the steps that read them because a `.proto` change costs one regeneration of
`pkg/proto/` and one breaking-change disposition, and this sub-step already pays both. Deferring them makes
each later step pay again on a package that is already stable-versioned, and each of those regenerations is
a second chance to leave `pkg/proto/` drifted from `schemas/`.

**The breaking-change gate, and the decision this sub-step records.** `cmd/lenny-test/cmd_run.go` lines 499
through 536 run `buf breaking schemas/ --against .git#branch=main`. `buf.yaml` sets
`ignore_unstable_packages: true`, which does not exempt this change, because the package is
`lenny.adapter.v1` and a stable version suffix is not an unstable package, so renaming an RPC and two
message types in that package is a breaking change by buf's definition and the check reports it.

The recorded disposition is that the rename needs no exception, because of how the check computes its
verdict. Its only inputs are buf's exit status and the current branch name
(`cmd/lenny-test/cmd_run.go` lines 511 and 527 through 535). Off `main` the check downgrades the findings
to advisory itself, on the ground its own comment states, which is that v1 is built on a long-lived feature
branch whose proto is deliberately evolved from the Phase-1 skeleton committed to `main`. On `main` the
comparison baseline is branch `main`, so once this rename is on `main` the tree and the baseline both carry
it, buf reports no finding, and the hard-fail branch is not reached. This sub-step therefore lands green
without widening the gate or suppressing the finding, and it stages no edit to the check.

Two alternatives were considered and rejected. Recording the disposition as entries in the shared exception
register proposal 0065 adds would be inert, because the check reads no register and this proposal stages no edit
that would make it read one, so the entries could not change any verdict while carrying owners and expiries
that imply they could. Advancing the check's baseline ref was rejected because the ref is read by every
proto change in the tree, and moving it to accommodate one sub-step suppresses breaking-change detection
for every other change in flight.

Apply as one exclusive change on a quiesced tree. While it is in flight no other change edits an adapter
handler file or the adapter proto, because a rename and a concurrent edit to the same file produce a
conflict that resolves silently in the wrong direction.

**Tests.** The rename changes a wire contract and the runtime adapter contract, which
`.claude/rules/test-coverage.md` maps to tier 3 and tier 10. Both tiers already carry suites that
construct the renamed symbols and drive the renamed handshake, so the change reaches them. The renamed
manifest key and socket also cross a process boundary between the adapter and a separate runtime binary,
which `.claude/rules/test-coverage.md` line 36 maps to tier 4, so the change reaches tier 4 as well.

- **Tier 3, manifest round trip.** Emit a manifest from `pkg/adapter/manifest.go` and parse it with each
  of the three runtime SDKs (`sdks/runtime/go/runtime/types.go`, `sdks/runtime/python/lenny_runtime/types.py`,
  `sdks/runtime/typescript/src/types.ts`), asserting that the renamed key and its socket resolve in all
  three and that a manifest carrying only the retired key resolves no operations channel in any of them,
  so the Full-level socket is reported absent. The assertion is stated that way rather than as a rejection
  of the retired key, because §4.7 makes silent ignoring of unknown top-level manifest fields normative
  for runtimes (`spec/04_system-components.md` line 818) and the shipped Go SDK documents that behavior
  (`sdks/runtime/go/runtime/types.go` lines 113 through 116). A rejection assertion would require the
  three SDKs to fail on unknown top-level fields, which contradicts that rule and is a runtime behavior
  change this proposal does not make. This is the cross-language property §5
  asserts, and review cannot check it.
- **Tier 3, wire agreement.** Assert that the adapter's listen address derived from the renamed flag
  equals the socket the manifest advertises, and that the renamed proto RPC and the JSON Lines schema
  `$defs` agree with the adapter handler and with the §28 register.
- **Tier 3, the widened descriptor pin.** Update
  `TestReportUsageRequestWireContract` in
  `tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` so its `want` slice carries
  `coordination_generation` and the slot identifier alongside `session_id` and `cumulative`, keeping the
  exact-count comparison at line 63 as the assertion that a later renumber or drop is caught. The whole
  tier-3 run is an exit criterion of this sub-step, alongside the tier-4 run and the proto no-drift test,
  because the field additions widen a descriptor an existing tier-3 test pins exactly.
- **Tier 0, schema bijection.** Assert the renamed schema files and their example fixtures round-trip.
- **Tier 4, live runtime process.** Run `tests/tier4_integration/credential_lifecycle_test.go` over the
  renamed manifest key, the renamed socket, and the renamed `--runtime-ops-socket` flag, asserting that
  the real `cmd/runtimes/streaming-echo` process resolves its operations socket from the renamed key and
  completes the `credentials_rotated` and `credentials_acknowledged` round trip, and asserting the
  negative case that a manifest carrying only the retired key leaves the runtime with no operations
  socket, so the Full-level flow fails rather than degrading silently. This tier is the only one that
  drives the renamed key against a real runtime binary: `cmd/runtimes/streaming-echo` parses the manifest
  through its own struct tag at `cmd/runtimes/streaming-echo/main.go` line 147 rather than through any of
  the three runtime SDKs, so a rename applied to the emitter and the SDKs but not to that runtime is a
  silent JSON unmarshal miss that every other listed test passes. The run is an exit criterion of this
  sub-step.
- **Tier 10, conformance.** Run the Full battery over the renamed socket, handshake, and check
  identifier, asserting that `cmd/lenny-compliance/full.go`, `cmd/lenny-ctl/runtimescaffold/probe.go`, and
  `tests/tier10_conformance/scaffolds_test.go` agree with the production emitter.

The manifest key has two independent emitters, so `cmd/lenny-compliance/full.go` line 98, which writes the
manifest for the external-adapter compliance run that gates a third-party adapter from
`pending_validation` to `active`, moves in the same change, as does the fixture at
`tests/tier4_integration/credential_lifecycle_test.go` line 362. The manifest also has a fourth reader
beyond the three runtime SDKs, which is `cmd/runtimes/streaming-echo/main.go` line 147, and it moves in
the same change. Each new test carries a `// spec:` tie to the §28 card for the channel it exercises,
which for the tier-4 assertions is the `CH-RUNTIMEOPS` card.

### SPEC-3. The new sections, the reductions, and the successor pointers

**Target:** `spec/28_communication-channels.md` §28.5 through §28.8 (new),
`spec/29_communication-scenarios.md` (new), `spec/15_external-api-surface.md` §15.3, §15.4 (including the
carved-out wire-artifact pointer at line 1460 and its bullet list, restated over the corrected artifact
set), §15.4.2,
§15.4.3, §15.4.4 (for the six pseudocode citations of the retired §15.4.1 alone), §15.4.5, §15.4.6, and
§15.7, `spec/07_session-lifecycle.md` lines 116, 296, 323, 343, 349, and 433 (for the seven links into the
retired `1541-adapterbinary-protocol` anchor whose targets and labels are hand-corrected to the surviving
§15.4 `MessageEnvelope` heading) and line 330 (for the pointer from the coordinator-routing paragraph to
the §29 off-holder matrix), `spec/21_planned-post-v1.md` line 31 (for the `Section 15.4.1` link label
the §15.4.1 reduction falsifies), `schemas/README.md` (for the artifact
enumeration §28.7 supersedes), `spec/24_lenny-ctl-command-reference.md` line 114 and
`docs/reference/adapter-contract.md` lines 654, 658, and 659 (for the consumer-scoped enumeration that
gains the runtime-ops events schema, and for the JSONL row whose Purpose cell claims the runtime-operations
frames), `docs/runtime-author-guide/publishing.md` line 367 (for the third statement of the same
consumer-scoped enumeration), `spec/04_system-components.md` §4.4 and §4.7 (§4.4 for the numbered subsection
headings inserted below, §4.7 for the reduction, its successor pointers, and the same headings),
`spec/05_runtime-registry-and-pool-model.md`, `spec/09_mcp-integration.md`,
`spec/11_policy-and-controls.md`, `spec/12_storage-architecture.md`, and
`docs/runbooks/credential-rotation-failure.md` (for the §4.7 pointers the §4.7 reduction falsifies),
`schemas/runtime-ops-events.schema.json`, `schemas/messagepart.schema.json`, and
`schemas/lenny-adapter-jsonl.schema.json` (post-SPEC-2 names, for the
hand-corrected `description` pointers), `schemas/lenny-adapter.proto` (for the line pass, for the two
hand-corrected §4.7 handshake comments at lines 214 through 216 and line 1577, and for the
`STATUS_INTERRUPT_TIMEOUT` comment at line 1063),
`docs/api/internal.md` (for the hand-split binary-protocol
pointer at line 544),
`spec/README.md`, `tests/spec-map.json`, `tests/spec-map-exceptions.yaml`,
`tests/spec-anchor-moves.json` and `tests/registers/anchor-senses.yaml` (seeded here),
the sections that link to §28 rather than describing the contract themselves,
`tests/claim-map.json`, and
`pkg/proto/` (regenerated rather than rewritten, because the line pass rewrites `schemas/*.proto`). The
heading walker, the tier-11 successor-pointer check, and the claim register's schema-only tier-0 validator
land in this sub-step, because the walker's
predicate names the §28.5 card headings this sub-step creates, the check reads the pointers this
sub-step writes, and the validator reads the register this sub-step seeds.

**Rationale:** The channel contract is spread across `spec/04`, `spec/07`, `spec/10`, `spec/13`, and
`spec/15`, and no section owns it, so answering how the gateway talks to the pod requires reading all five.
`spec/28` and `spec/29` give the contract and the end-to-end traces one normative home, and each reduced
section keeps a successor pointer so a reader arriving by a stale reference lands on a pointer rather than
on adjacent text.

**Change (staged description).** Write the contract cards grouped by participant edge under the §28.5.1
through §28.5.7 boundary subsections §4.8 fixes, in the order that table states, the exclusivity and
concurrency model, and the wire-contract artifact register derived mechanically from the schemas directory
rather than hand-enumerated, with one row per artifact naming the surface it schematizes and the heading
that owns that surface. Write §28.8 as the failure and degradation matrix §4.8 describes, authored
from the per-channel degradation behavior the cards state rather than relocated, with one row per
identifier in the §28.3 channel register. Write §29 as end-to-end traces naming channels by identifier,
including the off-holder matrix §29 owes. That matrix is keyed by session-scoped client route, and where
one route has more than one off-holder outcome it carries one row per route and session state. Each row
states what happens when the replica serving the route is not the replica holding that session's pod
control stream, which is the case the co-located binding makes reachable. One route and state is already
covered normatively: `spec/07_session-lifecycle.md` line 330, in §7.2, requires a `delivery: immediate`
message landing on a non-coordinator replica to be forwarded to the session's coordinator, and requires
the forwarding replica to fall back to inbox buffering with a `queued` receipt when the coordinator is
unreachable so the message is not silently dropped. The coordinator is the holder, because the
coordinating replica is the one holding the session-coordination lease (`spec/04_system-components.md`
line 489) and the pod-session binding is re-established on the new coordinating replica after a handoff
(`pkg/gateway/podlifecycle/podsession/registry.go` lines 12 and 13). §7.2 line 330 states that rule for both
of its message sources, which are the external client route `POST /v1/sessions/{id}/messages` and the
inter-session `lenny/send_message` tool on the MCP tool surface, so the §29 rows for both of those routes
with `delivery: immediate` restate §7.2's requirement rather
than specifying a refusal, and cite §7.2 as the section that owns it, and `spec/07` line 330 gains a
pointer to the §29 matrix in the same change. Keeping the rule in §7.2 and pointing at it, rather than
relocating it, is what keeps one normative statement of the recovery path: the rule is session-lifecycle
state-machine content that §7.2's surrounding paths depend on, and the matrix's job is to state the
off-holder outcome per route. Every row other than those two `delivery: immediate` resume rows states a
case no section states today. The row domain is the session REST surface registered at
`pkg/gateway/sessionserver/sessionserver.go` together with the three other production entry points that
reach the same executor send, which are `POST /v1/chat/completions`, `POST /v1/responses`, and the MCP
tool surface, matching the plan's statement that the matrix states the required off-holder behavior per
route (`gateway-runtime-comms-remediation.md` lines 586 and 587, and lines 1130 through 1133). Keying the
matrix by gateway-to-pod RPC instead would leave no row for the outcomes that corrupt durable state or
lose a message without reaching a pod at all: the `inReplyTo` send in state `input_required` resolves
against a process-local wait map, answers HTTP 200 `queued`, and buffers a message that is never
redelivered; the `delivery: immediate` send in state `suspended` flips the session row from `suspended` to
`running` with no pod resumed; and the two SSE reads deliver a backlog and then fall permanently silent
(`gateway-runtime-comms.md` lines 1995 through 2001, with the process-local map at
`pkg/gateway/podlifecycle/podsession/registry.go` lines 14 through 17). A per-RPC statement of what an
off-holder replica does with an operational request is carried as a column of the matrix or as §28.5.1
card content rather than as the matrix's key. The client-to-gateway session REST surface is not a channel
in the §28 register, so no §28 card owns it and this matrix is where the specification states its required
off-holder behavior, except for the `delivery: immediate` resume, which §7.2 owns and the matrix restates
with a pointer.

Reduce `spec/15` §15.4 to the wire-artifact pointer it already claims to be, and reduce the `spec/04` §4.7
channel prose, in both cases leaving a successor pointer naming the identifiers that moved and the heading
that now owns them.

Every reduction in this sub-step is a relocation, and a relocation lands only when both of its legs land in
the same change: the source's removal, and the destination text in `spec/28` that carries what the source
held. The reduction of a table, a tool set, a message-schema list, or a rule set is not authorized by this
proposal unless the destination card or register states that content, so a reduction whose destination
staging is absent is a defect in this proposal rather than an instruction to delete. Where a source
statement has no destination because it belongs to a different concern, the statement is carved out of the
reduction and left in place rather than removed; §15.4's third-party compatibility sentences are the worked
case, and they are carved out below for exactly that reason.

The §28.7 wire-contract artifact register supersedes the one artifact enumeration outside `spec/15` that
stands for the same set and is incomplete, which is the `schemas/README.md` artifact table. §28.7's rows
name the artifact, the surface it schematizes, and the heading that owns that surface, so a reference to
the register carries what that table carried.

`spec/24_lenny-ctl-command-reference.md` line 114 describes the
external-adapter compliance suite as schema-driven and names three artifacts, omitting the runtime-ops
events schema, and that sentence is the one that gates a third-party adapter from `pending_validation` to
`active`, so as written it states an artifact set under which `CH-RUNTIMEOPS` frames are asserted against
nothing. Add
`schemas/runtime-ops-events.schema.json` to that enumeration by hand, and leave the enumeration in place
rather than replacing it with a reference to §28.7. The addition states the artifact set the suite is
required to assert against and changes no code, which is consistent with §7: `cmd/lenny-compliance/schemavalidate.go`
lines 29 through 34 declare two schema files, `loadSchemas` at lines 46 through 70 compiles those two
alone, and `checkLifecycleHandshake` at `cmd/lenny-compliance/full.go` lines 225 through 258 checks the
handshake reply field by field with no schema involved. Per §4.4 the distance between the corrected
sentence and the shipped harness is recorded as a claim-register row with status `ABSENT` and
`deferral_id` R8, which is the plan step that carries the frame-level requirements of
`schemas/runtime-ops-events.schema.json` into `cmd/lenny-compliance`
(`gateway-runtime-comms-remediation.md` lines 991 through 1002). The replacement was rejected on the same reasoning that
withdraws the `spec/18` line 92 edit one paragraph below. §28.7 is derived mechanically from the schemas
directory, which also holds `schemas/lenny-interceptor.proto`, `schemas/lenny-tokenservice.proto`,
`schemas/workspaceplan-v1.json`, `schemas/ocsf-mapping.yaml`, and `schemas/audit-events/v1.json`, none of
which a third-party runtime adapter implements and none of which the shipped harness reads: the suite the
sentence gates validates against two schema files (`cmd/lenny-compliance/schemavalidate.go` lines 31
through 33, driven from `pkg/gateway/externalapi/admin/external_adapters.go`). A reference to
the whole directory-derived register would therefore either over-specify a validation gate with assertions
from the interceptor SPI, the token-service proto, the WorkspacePlan schema, the OCSF mirror, and the
audit-event schema, or leave the asserted set undecidable, because §28.7 as staged carries no column
selecting the runtime-adapter-contract subset. `docs/reference/adapter-contract.md` line 658 states the
same artifact set for runtime authors, so its "Canonical artifacts" table gains a
`runtime-ops-events.schema.json` row in the same change and its lead sentence at line 654 is restated over
the artifacts the table names, which keeps the normative gating sentence and the published reference page
in agreement after application. The table's `lenny-adapter-jsonl.schema.json` row at line 659 is
hand-corrected in the same change, because its Purpose cell ends its frame list with "lifecycle frames"
and so makes the same wrong-mechanism claim SPEC-2 corrects in the artifact's own `description` and at
`spec/15_external-api-surface.md` line 1463. That row's Purpose cell is rewritten to name only the
stdin/stdout frames the artifact's top-level `oneOf` admits, which are `message`, `heartbeat`,
`heartbeat_ack`, `shutdown`, `tool_call`, `tool_result`, `response`, `status`, and `set_tracing_context`,
with the runtime-operations frames attributed to the new `runtime-ops-events.schema.json` row. Without
this correction the page carries two adjacent rows crediting two artifacts with the same frames, and no
pass or gate in this proposal reads the cell: it carries no reserved phrase in either banned spelling, no
retired identifier, no line citation, and no fragment.

`docs/runtime-author-guide/publishing.md` line 367 is the third published statement of the same artifact
set and takes the same hand correction in the same change. It reads that the compliance suite "validates
every JSON Lines frame your runtime emits against the canonical schemas published at
schemas.lenny.dev/adapter/v1/ -- `lenny-adapter-jsonl.schema.json` for stdin/stdout frames and
`messagepart.schema.json` for structured content parts", which quantifies over every frame the runtime
emits while naming two artifacts. A Full-level runtime also emits JSON Lines frames on the
runtime-operations socket, and SPEC-2 is the change that removes those frames from the JSONL schema's
stated scope and attributes them to `schemas/runtime-ops-events.schema.json` in the artifact's own
`description` and at `spec/15_external-api-surface.md` line 1463, so after SPEC-2 this sentence routes the
runtime-operations frames to an artifact that does not schematize them. That is the same defect the
`spec/24` line 114 correction addresses, on the page a runtime author reads immediately before publishing,
and the sentence links to the `docs/reference/adapter-contract.md` "Canonical artifacts" table for the
schema list, which this sub-step corrects. The sentence is therefore restated over the artifact set the
suite is required to assert against, naming `schemas/runtime-ops-events.schema.json` for the Full-level
runtime-operations frames alongside `lenny-adapter-jsonl.schema.json` for stdin/stdout frames and
`messagepart.schema.json` for content parts, so the published statements of that artifact set agree after
application. No
pass or gate reaches the sentence: it carries no reserved bare noun phrase in either spelling, no retired
identifier (neither named schema is renamed), and no line citation, its only fragment link targets the
surviving `#canonical-artifacts` anchor, and the supersession check exempts it on the second ground.

`spec/15_external-api-surface.md` line 1460 is the specification's own statement of the same artifact set
and takes the same hand correction in the same change. It reads "The runtime adapter contract is published
as three machine-readable artifacts committed to the repository and released alongside each Lenny release",
and the bullets at lines 1462 through 1464 name `schemas/lenny-adapter.proto`,
`schemas/lenny-adapter-jsonl.schema.json`, and `schemas/messagepart.schema.json`. §15.4 is the runtime
author's contract and the section §15.3 points at for the published wire contract, and it is the section
`docs/reference/adapter-contract.md` and `docs/runtime-author-guide/publishing.md` mirror, so leaving the
count and the bullet list as they stand would put the specification in disagreement with the three
statements corrected above and with its own line 1463, which SPEC-2 rewrites to attribute the
runtime-operations frames to `schemas/runtime-ops-events.schema.json`. Line 1460 is therefore restated over
the artifact set without a count, and a fourth bullet naming
`schemas/runtime-ops-events.schema.json` and the Full-level runtime-operations frames it schematizes is
added after line 1464. The correction lands here rather than in SPEC-2 because it is the same edit as the
three published statements above and because the artifact it adds carries its SPEC-2 name by the time this
sub-step runs. No pass reaches line 1460: it carries no reserved bare noun phrase in either spelling, no
retired identifier, no line citation, and no fragment link, and the §15.4 reduction carves the whole
wire-artifact pointer out unchanged. This is the specification half of the misdescribed-wire-artifact
record §8 closes; `gateway-runtime-comms.md` lines 2468 through 2472 record that half and lines 2474
through 2478 record the shipped-artifact half.

`schemas/README.md` carries the enumeration the register supersedes. Its opening sentence introduces the directory's
wire-contract artifacts and its table omits two of the artifacts the directory carries, which are
`schemas/lifecycle-events.schema.json`, which SPEC-2 renames to `schemas/runtime-ops-events.schema.json`,
and `schemas/lenny-tokenservice.proto`, so the table stands for the artifact set while naming a strict
subset of it. Replace the table with a reference to §28.7 for the artifact set, the surface each artifact
schematizes, and the heading that owns that surface. The
remaining sections of that README, which cover validation, versioning, and examples, are untouched. The
replacement is also what disposes of the `schemas/README.md` member of the falsified-sentence class below,
because the table's `lenny-adapter-jsonl.schema.json` and `messagepart.schema.json` rows both point at §15.4
for material the §15.4.1 reduction moves to §28. Adding the two missing rows was rejected: it would leave a
second per-artifact enumeration of a register §28 derives mechanically, and the table stands for the whole
directory's artifact set rather than for the subset a named consumer asserts against, which is the ground
that keeps the `spec/24` sentence in place.

The phase-deliverable lists in `spec/18_build-sequence.md` are outside the supersession, and the earlier
reading that sent `spec/18` line 92 into it is withdrawn. Line 92 sits under the Phase 1 deliverables
heading at line 87 and names the artifacts Phase 1 delivers, so its omission of the runtime-ops events
schema is phase scoping rather than drift: `spec/18_build-sequence.md` line 165 makes that same artifact a
Phase 2.8 deliverable. Replacing line 92 with a reference to a register derived from the schemas directory
would make Phase 1 responsible for every artifact under `schemas/`, including
`schemas/runtime-ops-events.schema.json`, `schemas/lenny-tokenservice.proto`, and
`schemas/lenny-interceptor.proto`, none of which any earlier phase delivers, and it would state the
runtime-ops events schema as both a Phase 1 and a Phase 2.8 deliverable, which §18 forbids because a phase
deliverable cannot depend on an artifact of a later phase. `spec/18`'s occurrences at lines 164, 165, and
408 are still rewritten by SPEC-1's name pass for their reserved-phrase spellings, which is a separate
class. `spec/15`'s own
pointer is carved out of the §15.4 reduction rather than superseded, for the reason stated below, and it is
named as exempt in the supersession check's predicate: it enumerates the artifacts §15.4's prose documents
rather than standing for the register's artifact set. The exemption is stated over the corrected
enumeration rather than over the one in the tree today. SPEC-2's hand correction to line 1463 sends the
runtime-operations frames to `schemas/runtime-ops-events.schema.json`, and this sub-step's correction to
line 1460 and the bullet list adds that artifact to the set the pointer names, so the surviving pointer
names every artifact §15.4's prose documents, including the one that schematizes the Full-level
runtime-operations frames §15.4.3 and §15.4.6 state as runtime obligations.

Two gates land in this sub-step under §3.5, because this is the sub-step that supplies each one's route to
green. The §28.8 matrix completeness check is a tier-0 gate, and it lands here because §28.8 is written
here while the §28.3 register its bijection reads already exists from proposal 0067, so the bijection first holds
at this sub-step's exit. The artifact-register supersession check is a tier-11 gate, and it lands here
because the enumeration it forbids, the `schemas/README.md` table, is replaced here. Its read domain is stated rather than left to the implementation: the tracked markdown under
`spec/`, `docs/`, and `schemas/`, together with the tracked root-level markdown documents N3 leaves in
scope, which is the markdown subset of the walk the naming lint reads. The predicate states the exemption
as a rule rather than as a list of sites, because that domain carries further enumerations beyond the one
this sub-step replaces. An enumeration is exempt on any of three grounds: it names the artifacts the
enumerating page's own prose documents, on the ground the paragraph above states for the §15.4
wire-artifact pointer; it names the artifact subset a named consumer asserts against rather than the
register's artifact set; or it names what a build phase delivers. Exempt on the first ground is the §15.4
pointer as this sub-step corrects it, which is the sentence at `spec/15_external-api-surface.md` line 1460
restated without a count over a bullet list that names `schemas/runtime-ops-events.schema.json` alongside
the three artifacts it names today. Exempt on the second ground are the compliance-suite sentence at
`spec/24_lenny-ctl-command-reference.md` line 114 and the "Canonical artifacts" table at
`docs/reference/adapter-contract.md` line 658 and the schema list at
`docs/runtime-author-guide/publishing.md` line 367, all three of which name the artifacts the
external-adapter compliance suite generates its assertions from and all three of which this sub-step
corrects by hand to include the runtime-ops events schema. Exempt on the third ground are the `spec/18`
phase-deliverable lists and the Phase 1 wire-contract artifact list at `TESTING.md` line 1449. At this
sub-step's exit no unexempt enumeration in the check's domain names a subset of the register.
TEST-1 states each gate's predicate and adds its cases.

Restate the §15.3 sentence at `spec/15_external-api-surface.md` line 1456 by hand in the same change,
because the reduction makes it false. It reads "The wire contract is published as machine-readable
artifacts in [Section 15.4](#154-runtime-adapter-specification); this section (15.4 and its subsections)
is the normative prose reference and is kept in sync with those artifacts", which asserts a normative
ownership §15.4 gives up here. The restated sentence names §28 as the normative reference for the
gateway-to-pod channel contract and §15.4 for the wire artifacts. The edit is hand-authored because the
sentence sits above the §15.4 heading and so outside the reduction, carries no line citation, carries no
anchor that moves, and carries neither reserved phrase as a bare noun phrase, so no pass reaches it and no
gate reads it. Its companion at line 1466 sits inside the §15.4 preamble at lines 1458 through 1468 and
is covered by the reduction at sentence granularity. Line 1466 carries four sentences, and the reduction
removes the last one alone, which begins "**This section (15.4 and its subsections) remains the normative
prose description**" and ends "any discrepancy between the artifacts and this prose is a bug that must be
reconciled before release". The three sentences that open the line state the compatibility contract for
the same artifacts §15.4 is reduced to: the release-tag versioning rule, the buf-style
breaking-change rule for the `.proto` file together with the `additionalProperties` discipline for the
JSON Schemas, and the `examples/runtimes/echo/` reference implementation built from the same `.proto`
file. They are carved out and stay where they are, on the same rule the wire-artifact pointer at lines
1460 through 1464 gets, because they are part of that pointer rather than a channel contract. A tree-wide
grep for `buf.build`, `buf-style`, `breaking-change rules`, `additionalProperties discipline`, `versioned
by Lenny release tag`, and `executable reference` over `spec/` and `docs/` returns
`spec/15_external-api-surface.md` line 1466 alone, so removing them would delete the only statement in the
tree of the compatibility contract third-party runtime authors and SDK maintainers build against.

One paragraph of that preamble is carved out and stays where it is: the SDK-warm demotion contract at
`spec/15_external-api-surface.md` line 1468, which requires adapters for runtimes that declare
`capabilities.preConnect: true` to implement the `DemoteSDK` RPC and which fixes the 10s teardown timeout
followed by SIGKILL, the post-demotion pod state, and the `UNIMPLEMENTED` error code for adapters that do
not support demotion. It is carved out on the same rule §15.4.3 through §15.4.6 get below, because it
states an obligation on the runtime author's adapter rather than a channel contract, and §28.5 holds
contract cards grouped over the closed boundary set below, none of which owns an adapter RPC
implementation obligation. Those particulars appear nowhere else: `spec/04_system-components.md` line 652
states the RPC's purpose and a gateway-side fallback timeout alone, and `spec/06_warm-pod-model.md` lines
40 and 67 state the mandatory-support rule and the separate SIGTERM-internal timeout. Keeping the
paragraph in place is also what keeps the inbound references at
`spec/05_runtime-registry-and-pool-model.md` line 22, `spec/15_external-api-surface.md` line 1114,
`docs/reference/adapter-contract.md` line 64, `docs/reference/configuration.md` line 153, and
`docs/reference/error-catalog.md` line 100 resolving to a normative statement of the contract.

The §15.4 reduction covers the channel-contract prose alone, which is the §15.4 preamble at
`spec/15_external-api-surface.md` line 1458 other than the wire-artifact pointer at lines 1460 through
1464, the three compatibility-contract sentences that open line 1466, and the SDK-warm demotion paragraph
at line 1468. Within the preamble the reduction therefore removes the normative-ownership sentence that
closes line 1466 and leaves the rest standing. The wire-artifact pointer is
carved out because it is the pointer §15.4 is reduced to: line 1460 is the sentence "The runtime adapter
contract is published as three machine-readable artifacts committed to the repository and released
alongside each Lenny release", and lines 1462 through 1464 name `schemas/lenny-adapter.proto`,
`schemas/lenny-adapter-jsonl.schema.json`, and `schemas/messagepart.schema.json`. Removing them would
leave the restated §15.3 sentence pointing at a §15.4 that names no artifact, and would remove line 1463,
which SPEC-4 counts among the same-page links its anchor pass rewrites. Line 1463 states the scope of
`schemas/lenny-adapter-jsonl.schema.json` wrongly today, and SPEC-2 corrects it by hand in the same change
that corrects the artifact's own `description`. Line 1460 and the bullet list state the artifact set
incompletely today, and this sub-step corrects both in the change that corrects the three other published
statements of that set, as the paragraph above states, so the carve-out preserves a block whose every
sentence is true and whose bullet list names four artifacts. The reduction also covers
the `#### 15.4.1 Adapter↔Binary Protocol` subsection at line 1470.

`#### 15.4.2 RPC Lifecycle State Machine` at `spec/15_external-api-surface.md` line 2068 is carved out of
the reduction on the rule §15.4.3 through §15.4.6 get below. It states the adapter's own RPC state machine
and the version-negotiation handshake the adapter performs on that machine's first transition, which is an
obligation on the adapter implementation rather than a channel contract, and §28.5 groups its cards over
the closed boundary set §28.2 fixes, none of which owns an adapter RPC implementation obligation. The §4.8
heading table therefore lands no §28 heading that could receive the subsection, and adding one would widen
§28 past that boundary set for a single state machine. Its particulars appear nowhere else in `spec/`: the
`INIT` row at `spec/15_external-api-surface.md` line 2081 is the only statement of the `AdapterInit` and
`AdapterInitAck` version negotiation, of the `PROTOCOL_VERSION_INCOMPATIBLE` stream close, and of the
current protocol version `"1.0.0"`, so reducing the subsection with no destination staged would delete
normative content, which the both-legs rule above forbids. The heading keeps its
`1542-rpc-lifecycle-state-machine` anchor, `tests/spec-anchor-moves.json` carries no entry for that anchor,
and both the one inbound markdown link at `spec/15_external-api-surface.md` line 2395 and the bare
`§15.4.2`-form citations under `pkg/` and `sdks/` are left untouched. The anchor gains one further inbound
link, at `spec/15_external-api-surface.md` line 2733, which cites the retiring §15.4.1 anchor for the
version negotiation this subsection states and which SPEC-3 hand-corrects to this anchor, as the carve-out
class below states. Its prose is still rewritten in place
where a pass reaches it, which is the reserved bare noun phrase in the `ACTIVE` row at
`spec/15_external-api-surface.md` line 2083 that SPEC-1's name pass rewrites.

The §15.4.1 block is not one
heading: four unnumbered `####` subsections and their `#####` children sit between line 1470 and line 2068,
which are the internal `MessagePart` format heading (line 1515), `#### Translation Fidelity Matrix`
(line 1653), the `MessageEnvelope` unified message format heading (line 1708), and
`#### Protocol Reference — Message Schemas` (line 1836) with the eight `##### Inbound:` and
`##### Outbound:` message schemas under it at lines 1840, 1856, 1864, 1872, 1909, 1929, 2023, and 2029.
Two of the four carry the adapter-to-binary wire contract, which are the internal `MessagePart` format
heading and `#### Protocol Reference — Message Schemas` with its eight message schemas. Those two are
inside the §15.4.1 block the reduction retires, they move to §28 with it, and their anchors retire with
their headings.

The other two state client-facing external-protocol contracts and are carved out of the reduction, because
§28's boundary set has no card that can own them. §28.5 groups its cards by the boundary values §28.2
closes, which are `intra-pod`, `gateway-to-pod`, `pod-to-gateway`, `pod-egress`, `gateway-to-store`,
`inter-replica`, and `control-plane`, and none of them names the external-client-to-gateway edge. This is
the same rule §15.4.3 through §15.4.6 get below, applied one heading level in.

- `#### Translation Fidelity Matrix` (line 1653), together with its `protocolHints` and
  round-trip-asymmetry children, documents field-level round-trip fidelity of `MessagePart` through each
  `ExternalProtocolAdapter` (`spec/15_external-api-surface.md` line 1655, with the column header at line
  1672 naming MCP, OpenAI Completions, Open Responses, REST, and A2A), which is the §15.2 client surface,
  and it is implemented gateway-side in `pkg/gateway/externalapi/outputpartfidelity` rather than in the
  adapter. The
  heading and its `translation-fidelity-matrix` anchor stay in `spec/15`,
  `tests/spec-anchor-moves.json` carries no entry for that anchor, and the anchor pass leaves the targets of
  the two inbound links at `spec/15_external-api-surface.md` line 1399 and `spec/21_planned-post-v1.md`
  line 31 untouched. The `spec/21_planned-post-v1.md` line 31 link keeps that target and has its
  `Section 15.4.1` label hand-corrected here, as the falsified-sentence class below states, because the
  label names the subsection the reduction retires. A third inbound link cites the matrix through the
  retiring anchor and is hand-corrected here: `docs/reference/adapter-contract.md` line 371 links to
  `.../spec/15_external-api-surface.md#1541-adapterbinary-protocol` for "Spec §15.4.1 -- Translation
  Fidelity Matrix". It is an absolute GitHub URL, so neither the anchor pass nor the fragment-link gate
  reads it, and its page is the client-facing adapter-contract reference, so leaving it would drop a
  runtime author at the top of `spec/15`. SPEC-3 rewrites its fragment to `#translation-fidelity-matrix`
  and its `Spec §15.4.1 -- Translation Fidelity Matrix` label to `Spec §15.4 -- Translation Fidelity
  Matrix`, in the same change that splits the heading, which is the carve-out class §3.4 states applied
  where the link sits. The label edit travels with the fragment edit under the target-and-label rule §3.4
  states for this class: the matrix survives inside §15.4, and a label reading §15.4.1 would name a
  subsection that exists in no `spec/` file after the reduction.
- The `MessageEnvelope` unified message format heading (line 1708) is split before the reduction runs. Its
  own first sentence states that the envelope is carried "across the stdin binary protocol, platform MCP
  server tools, and all external APIs" (line 1710), and the block holds the canonical `delivery` closed
  enum with its `400 INVALID_DELIVERY_VALUE` rejection rule (lines 1751 and 1759), the `delivery_receipt`
  schema that the client-facing `lenny/send_message` tool returns together with its `reason` enum (lines
  1761 and 1775), and the `message_expired` event schema and `reason` enum emitted on the sender session's
  event stream (line 1796). Those four keep their text in `spec/15` under the existing heading, which
  keeps its `messageenvelope--unified-message-format` anchor and gains no
  `tests/spec-anchor-moves.json` entry. Only the stdin and stdout envelope framing moves to §28. That first
  sentence closes "see Protocol Reference below", and the Protocol Reference block it points at moves to
  §28, so the sentence is a member of the falsified-sentence class below and its closing clause is
  hand-corrected in the same change.
  `spec/07_session-lifecycle.md` lines 116, 296, 323, 343, 349 (twice on that line), and 433 cite the
  retired `1541-adapterbinary-protocol` anchor for exactly this material, seven links across six lines.
  `tests/spec-anchor-moves.json` is keyed by retired anchor and carries one successor per anchor, so it
  cannot send those seven links to the surviving `spec/15` heading while sending the other links into the
  same anchor to a §28 card. SPEC-3 therefore rewrites those seven links by hand, in the same change that
  splits the heading, to the surviving `messageenvelope--unified-message-format` anchor, which is the
  hand-authored class §3.4 states for this case. Each of the seven carries a label naming the retiring
  subsection, `Section 15.4.1` at lines 116, 323, and twice at 349, and `§15.4.1` at lines 296, 343, and
  433, so the same hand correction rewrites each label to `Section 15.4` or `§15.4` respectively, under the
  target-and-label rule §3.4 states for this class. §15.4 is the surviving section the envelope material
  sits in, and a label left at §15.4.1 would name a subsection that exists in no `spec/` file after the
  reduction. Rewriting the label also leaves no retired `§15.4.1` citation at those sites for SPEC-4's
  tree-wide citation pass to read. The same collision recurs among the same-page links
  inside `spec/15_external-api-surface.md`, where seven links into the retired anchor cite material the
  carve-outs keep in `spec/15`. Six of the seven cite the surviving `MessageEnvelope`
  material: line 1838 (the opening sentence of the retiring Protocol Reference block, citing "the full
  `MessageEnvelope` format"), line 2165 (the `MessageEnvelope` fields row of the integration-level matrix),
  line 2489
  (`MessageEnvelope` in the schema-versioning list), line 2584 (the first of the two links on that line,
  labelled "`MessageEnvelope` — Unified Message Format"), line 2662 (the SDK comment quoting the same
  heading title), and line 2684 (the SDK comment citing the "Ordering guarantee" bullet, which sits at
  `spec/15_external-api-surface.md` line 1829 inside the surviving block rather than in the stdin and
  stdout framing). SPEC-3 rewrites those six by hand in the same change, to the same surviving anchor, and
  rewrites the label of each under the same target-and-label rule: `Section 15.4.1` becomes `Section 15.4`
  at lines 1838, 2165, and 2489, and `§15.4.1` becomes `§15.4` at line 2584 (the first of the two links),
  line 2662, and line 2684.
  The seventh cites a different surviving heading: line 2733, in §15.7, states that the gateway honors "the
  protocol version negotiation from [§15.4.1](#1541-adapterbinary-protocol)" so that a runtime built
  against an older SDK keeps working. The version negotiation is stated only by the `INIT` row at line 2081
  inside §15.4.2, which the carve-out above keeps in `spec/15`, and §15.4.1 states none of it, so SPEC-3
  rewrites that link by hand to `#1542-rpc-lifecycle-state-machine` and corrects its `§15.4.1` label to
  `§15.4.2` in the same change. Left to the anchor pass it would take the map's single §28 successor and
  send a normative SDK-versioning sentence to a card that by the carve-out's own argument states no version
  negotiation, and the fragment-link gate would pass it, because that gate reads resolution rather than
  destination.
  Six of the seven stay in `spec/15` and take the same-page form, five of them targeting
  `#messageenvelope--unified-message-format` and line 2733 targeting
  `#1542-rpc-lifecycle-state-machine`. Line 1838 sits inside the Protocol Reference block
  that moves to §28, so its hand-written target is the file-qualified form
  `[Section 15.4](15_external-api-surface.md#messageenvelope--unified-message-format)`, because a §28 card
  citing the same-page form would resolve against §28, which does not define the envelope.
  Line 2584 is the case that shows one successor per anchor cannot serve the whole population, because it
  carries two links to `1541-adapterbinary-protocol` on one line whose correct destinations differ: its
  second link cites the internal `MessagePart` format, which retires with the block and resolves to the
  §28 successor. SPEC-4's anchor pass then reads a tree with no link into
  a retired anchor at those six `spec/07` lines, none at `spec/15` lines 1838, 2165, 2489, 2662, 2684, and
  2733, and only
  the second of the two links on `spec/15` line 2584, which that pass rewrites to the §28 successor along
  with the other links the hand corrections leave in place. The four remaining file-qualified links into
  `1541-adapterbinary-protocol` resolve to that anchor's §28 successor: `spec/09_mcp-integration.md` line
  24 (the `lenny/output` message schema), `spec/26_reference-runtime-catalog.md` line 221 (the
  `tool_call` envelope), `spec/17_deployment-topology.md` line 361 (the stdin and stdout JSON Lines
  protocol), and `spec/08_recursive-delegation.md` line 829 (`MessagePart` schema versioning).

The same one-successor collision reaches the bare `§15.4.1`-form section citations, which the anchor pass
also writes, and those are the larger population. Outside `spec/` and `proposals/` the tree carries 669
occurrences of the bare `§15.4.1` citation across 150 files, 595 across 148 once §4.6's read exclusion of
`BUILD-GAPS.md` and `TEST-GAPS.md` is applied, among them 293 under `pkg/`, 189 under `sdks/`, and 64
under `cmd/`, and many of them cite the two blocks the carve-outs keep in `spec/15` rather than the
adapter-to-binary block that moves. Eleven sit in
`pkg/gateway/externalapi/outputpartfidelity`, the gateway-side implementation of the Translation Fidelity
Matrix, starting with the package comment at `pkg/gateway/externalapi/outputpartfidelity/matrix.go` line
3 ("encodes the §15.4.1 Translation Fidelity"). Sixteen sit in `pkg/gateway/session/sessioninbox`,
including `pkg/gateway/session/sessioninbox/events.go` line 45, which cites §15.4.1 for the
`message_expired` payload the carve-out keeps in `spec/15`. All three published runtime SDKs document
their canonical `MessageEnvelope` type the same way, at `sdks/runtime/go/runtime/types.go` lines 52 and
224, `sdks/runtime/python/lenny_runtime/types.py` lines 145 and 391, and
`sdks/runtime/typescript/src/types.ts` lines 59 and 211. Rewriting those to the map's single §28
successor would send a third-party runtime author to a channel-contract card that does not define the
envelope or the matrix, which is the failure the carve-out exists to prevent. The population is too large
to enumerate and is selectable only by which block each citation means, so the anchor pass takes the same
treatment the name and identifier passes take for a two-valued term: SPEC-3 seeds
`tests/registers/anchor-senses.yaml`, keyed by file and occurrence, recording for each occurrence of a
retired §15.4 anchor whether its destination is the §28 card, `#translation-fidelity-matrix`, or
`#messageenvelope--unified-message-format`, and the pass fails an occurrence with no entry rather than
substituting the map's successor. SPEC-4 retires the register with
`tests/spec-anchor-moves.json`, on the same run-completeness criterion. The §4.7 reduction carries no
comparable population, because the material it moves has no bare subsection citation form: §4.7 carries no
numbered subsections on the unmodified tree, so every existing citation of the intra-pod block reads `§4.7`
and resolves to a section that survives. The subsections this sub-step inserts are created after those
citations are read, so they add no anchor sense to disambiguate.

A same-page fragment link carried inside a block the reduction relocates is a further hand-authored class
this sub-step lands. A `[...](#anchor)` link resolves against the page it sits on, so moving the text to
`spec/28` breaks the link even though neither the link nor its target heading changed. The anchor pass
cannot repair it, because `tests/spec-anchor-moves.json` is keyed by retired anchor, these targets are
surviving anchors that carry no map entry, and the pass's stated rule leaves a same-page link into a
surviving anchor untouched. No other class reaches them, because they carry no reserved phrase, no retired
identifier, and no line citation. SPEC-3 therefore rewrites each of them to the file-qualified form against
the file the block left, in the same change that moves the block. Today's population is six links across
five lines inside the internal `MessagePart` format block at `spec/15_external-api-surface.md` line
1515, which are the `#155-api-versioning-and-stability` links at lines 1537, 1538 (twice on that line), and
1575, the `#157-runtime-author-sdks` link at line 1649, and the `#154-runtime-adapter-specification` link
at line 1650. Each is
rewritten to the `15_external-api-surface.md#...` form, because §15.4, §15.5, and §15.7 all stay where
they are. The `spec/04` side of this class is empty: the one same-page fragment link inside the §4.7
block, the `#49-credential-leasing-service` link at `spec/04_system-components.md` line 807, sits in the
adapter manifest field reference, which the §4.7 boundary below carves out of the reduction, so the link
neither travels nor needs rewriting. The line 1838 correction above is the same class
seen from the other side, where the link's target is the surviving `MessageEnvelope` heading rather than a
numbered section. The population is re-measured against the final reduction boundary before the change
lands, because the boundary decides which links travel. The fragment-link gate SPEC-4 lands is red on any
of these left unrewritten, so SPEC-4's red-on-introduction population is the seven pre-existing links only
once this rewrite has run.

Moving either block instead would require §28.2 to open its closed boundary set to an external-client
edge and §28.5 to carry a card for it, which widens this proposal from a channel contract into the client
API surface, and would leave the normative citations above resolving to a card that does not define the
material they cite.
The class table stages a hand-authored correction for every sentence a reduction falsifies. The §15.3
sentence above is one member. The remaining members are enumerated at the end of this sub-step, after the
§4.7 reduction boundary that falsifies them is stated.

`#### 15.4.3 Runtime
Integration Levels` (line 2089), `#### 15.4.4 Sample Echo Runtime` (line 2187), `#### 15.4.5 Runtime
Author Roadmap` (line 2387), and `#### 15.4.6 Conformance Test Suite` (line 2416) keep their headings,
their anchors, and their subjects, and none of their prose moves to §28. Their prose is still written
where the passes reach it: SPEC-1's name pass rewrites the 20 reserved-phrase occurrences between lines
2089 and 2459, SPEC-2's identifier pass rewrites the retired socket token and manifest key at lines 2305,
2425, and 2450, and SPEC-4's anchor pass rewrites the four links into the retired `15.4.1`
anchor at lines 2163, 2164, 2394, and 2441. Line 2395 is not among them, because it links to the
`1542-rpc-lifecycle-state-machine` anchor the carve-out above keeps in `spec/15`. Line 2165 is not among them, because it cites the
`MessageEnvelope` material the carve-out keeps in `spec/15` and SPEC-3 rewrites it by hand. What is unchanged is the set of headings, the
anchors, and the ownership of the content those four subsections carry. The four state the runtime-author contract rather than a channel contract,
and §28 has no heading that can own them: §28.5 holds contract cards grouped by participant edge, and a
runtime-author contract is not a channel contract. Two sentences inside them do point at §4.7 for
material the §4.7 reduction relocates, at line 2115 in §15.4.3 and line 2435 in §15.4.6, and each
of those two is a member of the falsified-sentence class below and is corrected by hand there. The
authentication bullet at line 2116 is not a member: its
`([Section 4.7](04_system-components.md#47-runtime-adapter), item 1)` parenthetical cites item 1 of
§4.7's `#### Adapter-Agent Security Boundary` at `spec/04_system-components.md` line 890, which is the only
statement of the manifest-nonce handshake and sits below the block the reduction touches, so the pointer and
its `item 1` qualifier still resolve after the reduction and the line takes no edit. Six
pseudocode comments inside §15.4.4 point at the retired §15.4.1 in the spelled-out `Section 15.4.1` form,
at lines 2214, 2217, 2275, 2278, 2372, and 2375, and they are one further member of that class, corrected
by hand there for the reason the class states: the spelled-out form is neither a markdown link nor a line
citation, so no pass reaches it. Retiring them would also break inbound normative
references that this proposal stages
no edit for, among them `spec/05_runtime-registry-and-pool-model.md` line 40 (the CRD `integrationLevel`
semantics, citing §15.4.6), `spec/04_system-components.md` line 796 (the `adapterLocalTools` manifest
field, citing §15.4.3), `spec/26_reference-runtime-catalog.md` line 10 and
`spec/17_deployment-topology.md` line 291 (the echo runtime, citing §15.4.4), the integration-level
vocabulary the three runtime SDKs, `README.md`, `TESTING.md`, and `docs/runtime-author-guide/` branch on,
and the `"15.4.6"` section string `cmd/lenny-compliance/full.go` line 40 stamps into the client-facing
compliance report.

**The `spec/04` §4.7 reduction covers the channel prose alone, and the adapter manifest material is
carved out.** §4.7 runs from `spec/04_system-components.md` line 637 to line 968, and its
`#### Adapter ↔ Runtime Protocol (Intra-Pod)` block runs from line 691 to line 820, ending where
`#### Runtime Integration Levels (agent-type only)` opens at line 822. Only the first part of that block
is a channel contract: Part A at line 695 naming the platform and per-connector MCP servers, Part B at
line 702 stating the intra-pod JSON Lines channel and its socket, and the message-schema table at lines
715 through 731. Those move to §28, and the successor pointer the reduction leaves names the §28.5 cards
that now own them. The adapter manifest material at lines 733 through 820 stays in `spec/04` under the
same heading, which keeps its `adapter--runtime-protocol-intra-pod` anchor and gains no
`tests/spec-anchor-moves.json` entry. That material is the `**Adapter manifest:**` paragraph at line 733
with the JSON example that follows it, the **Adapter manifest field reference** table at lines 783 through 810, the
**Level reading requirements** at lines 812 through 816, the **Forward compatibility** silent-ignore rule
at line 818, and the `runtimeMcpServers` reservation at line 820. It is carved out on the same rule
§15.4.3 through §15.4.6 get: the manifest field set states an obligation on the runtime author, whose
third-party runtimes parse the document field by field, rather than a channel contract, and §28.5 holds
contract cards grouped by participant edge under a fixed field template that a manifest field table does
not fit. The carve-out is also what keeps two statements this proposal makes elsewhere true. N7 in §28.1
reads the camelCase manifest convention off the §4.7 field set, and SPEC-2 justifies the `runtimeOps`
spelling against the sibling keys at `spec/04_system-components.md` lines 788 through 796; both resolve
to a §4.7 that still carries the field set. The manifest material staying is what keeps those two
statements true; the sentences the reduction does falsify are enumerated below.

**The falsified-sentence class is a population of twenty-seven, and every member is hand-corrected in the same
change as the reduction that falsifies it.** The §15.3 sentence at `spec/15_external-api-surface.md` line
1456 is the first member and is staged above. The other twenty-six are falsified by the §4.7 reduction or by the
§15.4.1 reduction, and eight of them sit outside `spec/`: five in shipped wire artifacts, one in the
reference page written for runtime adapter authors, one in the schemas directory's own README, and one in an
operator runbook. Fifteen of the members name §4.7 as the owner of
material the §4.7 reduction relocates, thirteen of them `spec/` and `docs/` sentences, one a pair of comments in
the shipped `schemas/lenny-adapter.proto`, and one a further comment in that same artifact, and they are
listed last, because §4.7 keeps its heading and its
anchor, so each of them survives every pass this proposal runs while stating an ownership §4.7 gave up:

- `spec/15_external-api-surface.md` line 2558, in §15.7 Runtime Author SDKs, which enumerates the
  `lenny/*` platform MCP tool names and then states that §4.7 "is authoritative for the platform MCP tool
  set; this list tracks it". Part A at `spec/04_system-components.md` line 697 is the only enumeration of
  that tool set in §4.7, and it moves to §28, so the ownership claim becomes false. The sentence is
  restated to name the §28.5 card that owns the intra-pod MCP server contract as authoritative and §4.7 as
  the owner of the adapter manifest. No pass reaches it: it carries no line citation, its reserved word
  appears as "lifecycle signal" rather than as a reserved bare noun phrase, and its `#47-runtime-adapter`
  link survives and gains no `tests/spec-anchor-moves.json` entry, so the anchor pass touches that line
  only for its separate `#1541-adapterbinary-protocol` link.
- `spec/15_external-api-surface.md` line 2700, in the §15.7 Runtime Author SDK `Reply` type
  documentation, which reads "// MCP tool ([§4.7](04_system-components.md#47-runtime-adapter) Part A)
  with" and so attributes the `lenny/output` platform MCP tool to §4.7 Part A. Part A moves to §28, after
  which §4.7 has no Part A and the parenthetical dangles. The parenthetical is rewritten to name the §28.5
  card that owns the intra-pod platform MCP server contract. No pass reaches it, on the same reasoning as
  line 2558: it carries no line citation, carries neither reserved word as a bare noun phrase, and its
  `#47-runtime-adapter` link survives and gains no `tests/spec-anchor-moves.json` entry.
- `spec/15_external-api-surface.md` line 2402, item 7 of the §15.4.5 Runtime Author Roadmap, which sends a
  Standard-level runtime author to §4.7 for the adapter manifest field reference and then attributes the
  Part B message schemas to the same section. The manifest half stays true under the carve-out and the
  message-schema half does not, so the sentence is split by hand to point the message schemas at §28.5.
  SPEC-1's name pass rewrites the reserved phrase on that line to the current spelling and leaves the
  section pointer wrong, which is why the correction is hand-authored rather than script-driven.
- `schemas/runtime-ops-events.schema.json`, the artifact SPEC-2 renames from
  `schemas/lifecycle-events.schema.json`, whose top-level `description` states that the frame field names
  are camelCase "to match the §4.7 message-schema table" and closes with "See spec/04_system-components.md
  §4.7 and spec/15_external-api-surface.md §15.4". The table moves to §28, so the description points a
  third-party runtime author at a section that no longer carries the contract it names. `schemas/embed.go`
  embeds the artifact so `cmd/lenny-compliance` and `lenny runtime validate` carry it into repositories
  where `schemas/` is absent, which makes the description a shipped statement of the contract on the same
  footing as the `schemas/lenny-adapter-jsonl.schema.json` description SPEC-2 corrects. Both pointers are
  rewritten to the §28.5 card that owns the intra-pod runtime-operations channel. No pass reaches them:
  the name pass rewrites only the reserved phrase in that sentence, the line pass matches only the
  line-citation form and neither pointer carries a line number, and §4.7 and §15.4 both keep their
  anchors.
- `schemas/messagepart.schema.json`, whose `description` at line 5 closes with "See
  spec/15_external-api-surface.md §15.4". The internal `MessagePart` format block at
  `spec/15_external-api-surface.md` line 1515, which is where that format is defined, moves to §28 with
  the §15.4.1 reduction, so the pointer is rewritten to the §28.5 card that owns the adapter-to-binary
  contract. The artifact is embedded by the same `schemas/embed.go` and reaches the same external
  consumers.
- `schemas/lenny-adapter-jsonl.schema.json`, whose `description` at line 5 closes with the same sentence,
  "See spec/15_external-api-surface.md §15.4". SPEC-2 corrects the wrong-mechanism half of that
  `description` and leaves the pointer, and the §15.4.1 reduction then falsifies most of it. The artifact
  schematizes the adapter↔binary stdin/stdout messages, whose normative definitions are
  `#### Protocol Reference — Message Schemas` at `spec/15_external-api-surface.md` line 1836 and its eight
  `#####` message children, all of which move to §28 with the reduction, while its `messageEnvelope` `$def`
  is defined by the `MessageEnvelope` unified message format heading at line 1708, which the carve-out
  keeps in §15.4. The pointer is therefore half true after the reduction, on the same structure as the
  `docs/api/internal.md` member below, and it is split by hand so that the stdin/stdout message schemas
  point at the §28.5 card that owns the adapter-to-binary contract while the `messageEnvelope` reference
  keeps pointing at the surviving `MessageEnvelope` heading in §15.4. No pass reaches it: the name pass
  rewrites only the reserved phrase in that `description`, the line pass matches only the line-citation
  form and the pointer carries no line number, and §15.4 keeps its anchor. The artifact is embedded by the
  same `schemas/embed.go` and reaches the same external consumers.
- `docs/api/internal.md` line 544, on the page whose audience line at line 11 names runtime adapter
  authors, which reads "For the complete binary protocol specification, including `MessagePart` format,
  `MessageEnvelope` schema, and level-specific behavior, see the technical design document Section 15.4".
  The binary protocol specification and the internal `MessagePart` format block at
  `spec/15_external-api-surface.md` line 1515 move to §28 with the §15.4.1 reduction, while the
  `MessageEnvelope` unified message format heading at line 1708 and the level-specific behavior in §15.4.3
  through §15.4.6 are carved out and stay in §15.4. The sentence is therefore half true, on the same
  structure as the §15.4.5 item 7 member above, and it is split by hand so that the binary protocol
  specification and the `MessagePart` format point at the §28.5 card that owns the adapter-to-binary
  contract while the `MessageEnvelope` schema and the level-specific behavior keep pointing at §15.4. No
  pass reaches it: the sentence carries no line citation, it is bare prose rather than a markdown link, it
  names the surviving §15.4 rather than a retired anchor and so gains no `tests/spec-anchor-moves.json` or
  `tests/registers/anchor-senses.yaml` entry, and it carries neither reserved word as a bare noun phrase.
- `schemas/README.md`, the artifact index for the directory, whose table sends
  `lenny-adapter-jsonl.schema.json` and `messagepart.schema.json` to §15.4 in its `Spec section` column, for
  the adapter-to-binary stdin/stdout JSONL messages and the `MessagePart` envelope respectively. The
  normative definitions of both move to §28 with the §15.4.1 reduction, which are the
  `#### Protocol Reference — Message Schemas` heading at `spec/15_external-api-surface.md` line 1836 with its
  `#####` message children and the internal `MessagePart` format block at line 1515, so both rows assert an
  ownership §15.4 gives up. The correction is the table's replacement by a reference to §28.7 staged above,
  which removes both rows rather than restating them, so this member takes no second edit. No pass reaches
  the rows: they carry no line citation, they carry no fragment in their links and so are outside the anchor
  pass and the fragment-link gate, and they carry neither reserved word as a bare noun phrase.
- `spec/15_external-api-surface.md` line 1710, the first sentence of the surviving `MessageEnvelope`
  heading, which closes "see Protocol Reference below". The Protocol Reference block at line 1836 moves to
  §28 with the §15.4.1 reduction, so the positional pointer names a block that is no longer below it, on the
  page it was written for. The closing clause is rewritten by hand to name the §28.5 card that owns the
  adapter-to-binary message schemas. No pass reaches it: it carries no line citation, it is bare prose
  rather than a markdown link, and it carries neither reserved word as a bare noun phrase. The sentence is
  the one the carve-out paragraph above cites as its reason for keeping the block in `spec/15`, so the
  carve-out and this correction land together.
- `spec/15_external-api-surface.md` lines 2214, 2217, 2275, 2278, 2372, and 2375, the six pseudocode
  comments inside the surviving §15.4.4 Sample Echo Runtime, each of which reads
  `flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)`. The flush requirement they
  cite is part of the adapter-to-binary stdin/stdout contract that moves to §28 with the §15.4.1 reduction,
  and the reduction retires the `15.4.1` heading, so after the change the six comments send a runtime author
  to a section number that no longer exists in `spec/`. All six are rewritten by hand to name the §28.5 card
  that owns the adapter-to-binary contract. No pass reaches them: they are not markdown links, so the anchor
  pass's markdown domain and the fragment-link gate do not read them; they carry no line number, so the
  retired citation form §4.6 states excludes them and the line pass, the resolver, and the ratchet never see
  them; they carry neither reserved word as a bare noun phrase; and they use the spelled-out
  `Section 15.4.1` spelling rather than the `§15.4.1` spelling the `tests/registers/anchor-senses.yaml`
  population is stated over, so the anchor pass neither rewrites them nor fails closed on them. This is the
  same disposition the `docs/api/internal.md` line 544 member takes, on the same ground that the citation is
  bare prose rather than a markdown link. Widening the anchor pass's matcher to the spelled-out spelling was
  rejected: the spelling also appears where it is correct, at
  `sdks/client/python/lenny/types.py` line 262 and in the `[Section 15.4](...)` links across `spec/`, each
  of which names a §15.4 that survives, and every remaining spelled-out `Section 15.4.1` occurrence in
  `spec/` sits inside a markdown link whose target anchor retires. That redirect reaches only the
  markdown-link members the hand corrections above enumerate, under the target-and-label rule §3.4 states;
  the anchor pass itself, run against `tests/spec-anchor-moves.json`'s single successor, rewrites a link's
  target without touching its label, so a spelled-out label on a link the anchor pass redirects
  mechanically survives the change naming a subsection that exists in no `spec/` file. Eleven links take
  that mechanical redirect while carrying the spelled-out label: `spec/15_external-api-surface.md` lines
  714, 1069, 1078, 1112, 1463, 2163, 2164, 2394, and 2514, `spec/08_recursive-delegation.md` line 829, and
  `spec/17_deployment-topology.md` line 361. This proposal leaves those eleven labels uncorrected: the
  anchor pass's target-only redirect is the disposition every non-carved-out §15.4.1 markdown link takes,
  and a label-aware rewrite of that population is out of this proposal's scope. The residue a widened
  matcher would serve is these six comments together with the `spec/21_planned-post-v1.md` line 31 label
  below and the eleven uncorrected labels above, a population of eighteen, and a tree-wide matcher would
  still need a per-occurrence register to tell a §15.4.1 that names the retiring subsection from one that
  does not.
- `spec/21_planned-post-v1.md` line 31, the durable-consumer obligation for A2A-mediated
  `MessagePart.schemaVersion`, whose link reads
  `[Section 15.4.1](15_external-api-surface.md#translation-fidelity-matrix)`. The
  `translation-fidelity-matrix` anchor is carved out of the reduction and survives, so the link resolves
  after the change and the anchor pass correctly leaves its target alone, while its label names a §15.4.1
  that exists in no `spec/` file after the reduction retires the subsection. The label is rewritten by hand
  to `Section 15.4` in the same change as the §15.4.1 reduction, keeping the `#translation-fidelity-matrix`
  target, because §15.4 is the surviving section that carries the matrix and the label then names the
  section the target sits in. No pass reaches it and no gate reports it: the anchor pass leaves a link into
  a surviving anchor untouched by the rule stated above, the line pass matches only the line-citation form
  and the link carries no line number, the name pass sees neither reserved word as a bare noun phrase, the
  spelled-out spelling is outside the `tests/registers/anchor-senses.yaml` population, and the
  fragment-link gate reads whether a link resolves rather than whether its label names the heading it
  resolves to.
- `spec/15_external-api-surface.md` line 2556, the graceful-shutdown bullet of the §15.7 Runtime Author
  SDK transport list, which states that the SIGTERM handling and the `terminate` and `shutdown` deadline
  contract come from `[§4.7](04_system-components.md#47-runtime-adapter)` and `[§15.4.1](#1541-adapterbinary-protocol)`.
  The only §4.7 statement of that contract is the `terminate` row of the message-schema table at
  `spec/04_system-components.md` line 725, which states that the runtime must exit within `deadlineMs` and
  that the adapter sends SIGTERM on timeout, and that row is inside the 715 through 731 table the reduction
  moves. §4.7 keeps no other statement of the deadline or of the SIGTERM rule after the move: the
  `Terminate` (proto `Shutdown`) RPC row at `spec/04_system-components.md` line 664 states the disposition
  and the scrub alone. The §4.7 half of the pointer is therefore rewritten by hand to the §28.5 card that
  owns the intra-pod runtime-operations channel, and the `§15.4.1` half continues to take its
  `tests/registers/anchor-senses.yaml` redirect. No pass reaches the §4.7 half: the line carries no line
  citation, it carries neither reserved word as a bare noun phrase, and §4.7 keeps its `#47-runtime-adapter`
  anchor and gains no `tests/spec-anchor-moves.json` entry.
- `spec/04_system-components.md` line 241, the full-level checkpoint bullet in §4.4, which states that the
  cooperative `checkpoint_request` and `checkpoint_ready` handshake "via the lifecycle channel (see
  [Section 4.7](#47-runtime-adapter))" is the only mechanism producing consistent checkpoints under all
  isolation profiles. Both frame definitions sit in the message-schema table at lines 715 through 731 that
  moves to §28, so the pointer is rewritten by hand to the §28.5 card that owns the intra-pod
  runtime-operations channel. This member is recovery-normative: it is the only statement in `spec/` of the
  mechanism that makes a checkpoint consistent under gVisor and Kata.
- `spec/05_runtime-registry-and-pool-model.md` line 41, the registration-time admission step, which cites
  §4.7 for the adapter's first `lifecycle_capabilities` and `lifecycle_support` exchange, and line 47, which
  states that the `lifecycle_support` handshake from §4.7 "remains the **runtime source of truth**" for
  integration level. Both frames move to §28 with the table, so both pointers are rewritten by hand to the
  same §28.5 card. Line 47 is the statement the gateway's integration-level admission check compares a
  declared level against, so leaving it pointed at §4.7 sends a reader looking for the handshake's
  definition to a section that no longer carries it.
- `schemas/lenny-adapter.proto` lines 214 through 216, the `GetObservedIntegrationLevel` RPC comment, which
  derives the observed level "from whether the runtime completed the §4.7
  `lifecycle_capabilities`/`lifecycle_support` exchange (full) and whether it connected to the intra-pod
  platform MCP server (standard)", and line 1577, the `GetObservedIntegrationLevelRequest.wait_ms` comment,
  which bounds how long the adapter waits for "its first §4.7 lifecycle handshake". Both frames sit in the
  message-schema table at `spec/04_system-components.md` lines 718 and 719 that moves to §28, and the
  intra-pod platform MCP server is the Part A bullet at line 697 that moves with it, so both comments are
  rewritten by hand, the RPC comment to the §28.5 cards that own the intra-pod runtime-operations channel and
  the intra-pod MCP servers and the `wait_ms` comment to the card that owns the intra-pod runtime-operations
  channel. This is a fourth shipped wire artifact on the same footing as the three schema `description`
  members above: `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 235 and 680 and
  `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 6231 mirror the two comments verbatim, and SPEC-3 already
  runs `make generate-proto` after the line pass, so the regeneration that carries the correction into
  `pkg/proto/` is the run this sub-step already takes. No pass reaches either comment: neither carries a line
  number, so the retired citation form §4.6 states excludes them and the line pass, the resolver, and the
  ratchet never read them; §4.7 keeps its `#47-runtime-adapter` anchor and gains no
  `tests/spec-anchor-moves.json` entry, so the anchor pass leaves the bare `§4.7` alone; and
  `lifecycle_capabilities` and `lifecycle_support` are wire identifiers this proposal does not rename rather
  than reserved bare noun phrases, so the identifier pass has no entry for them. Line 1578's "dials the
  lifecycle channel" is rewritten by SPEC-1's name pass, which is the class's structure exactly, in that the
  phrase becomes the canonical identifier while the section pointer stays wrong.
- `schemas/lenny-adapter.proto` line 1063, the `InterruptResponse.Status` enum comment, which reads
  `STATUS_INTERRUPT_TIMEOUT = 2; // deadline elapsed with no acknowledgement (§4.7)` and so attributes a
  spec-named failure status to §4.7. The only §4.7 statement of that status is the `interrupt_request` row
  of the message-schema table at `spec/04_system-components.md` line 723, which states the `deadlineMs`
  timeout behavior and the `INTERRUPT_TIMEOUT` status the `Interrupt` RPC returns to the gateway, and that
  row is inside the 715 through 731 table the reduction moves. §4.7 keeps no other statement of it: the
  surviving `Interrupt` RPC row at `spec/04_system-components.md` line 654 reads "Interrupt current agent
  work" alone, and line 723 is that file's only occurrence of the status name. The parenthetical is
  therefore rewritten by hand to name the §28.5 card that owns the intra-pod runtime-operations channel, in
  the same SPEC-3 change as the reduction, and `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 457 mirrors
  the comment verbatim, so the `make generate-proto` run SPEC-3 already takes carries the correction into
  `pkg/proto/`. No pass reaches it, on the same reasoning as the two handshake comments above: it carries
  no line number, so the line pass, the resolver, and the ratchet skip it; §4.7 keeps its
  `#47-runtime-adapter` anchor, so the anchor pass skips it; and it carries neither a reserved bare noun
  phrase nor a retired identifier. The adjacent `STATUS_BUSY = 3` comment at
  `schemas/lenny-adapter.proto` line 1064 is not a member and takes no edit, because the `BUSY` drop it
  cites is stated at `spec/04_system-components.md` line 677, which sits above the reduction boundary and
  stays in §4.7.
- `spec/09_mcp-integration.md` line 8, the "Adapter ↔ Runtime (intra-pod)" row of the §9 channel table,
  which cites §4.7 for the platform and per-connector tool servers. Part A at
  `spec/04_system-components.md` line 695 with its enumeration at line 697 is that material and it moves to §28, so the row's pointer is
  rewritten by hand to the §28.5 card that owns the intra-pod MCP servers. The other §4.7 pointer on that
  page, at `spec/09_mcp-integration.md` line 142, is not a member and takes no edit: it states that each
  authorized connector gets its own MCP server "in the adapter manifest", and the manifest's
  `connectorServers` field reference at `spec/04_system-components.md` lines 792 through 794 is inside the
  carve-out and stays in §4.7, so the pointer still resolves to the material it cites.
- `spec/11_policy-and-controls.md` line 49, the direct-mode budget and quota accounting path, which cites
  §4.7 for the `llm_request_completed` frames the runtime sends and from which the adapter accumulates the
  per-session token total. That frame's definition moves to §28 with the table, so the pointer is rewritten
  by hand to the §28.5 card that owns the intra-pod runtime-operations channel. This member carries the
  accepted underreporting risk for direct mode, so the reader it sends to §4.7 is a reader checking what the
  platform trusts a runtime to self-report.
- `spec/11_policy-and-controls.md` lines 53, 153, and 171, and `spec/12_storage-architecture.md` line 161,
  four members that cite §4.7 for the pod-side cumulative usage total the gateway reconstructs quota and
  billing counters from after a replica crash. Line 53 is the "Crash Recovery for Quota Counters" rule and
  states that "each pod's runtime adapter retains a cumulative usage total that is re-reported on
  reconnection to a new gateway replica", from which the gateway takes the maximum of the Postgres
  checkpoint and the pod-reported cumulative. Line 153 reconstructs the dropped in-memory billing
  write-ahead buffer from the same pod-reported usage, line 171 is the `GATEWAY_CRASH_RECONSTRUCTION`
  billing-correction reason, and `spec/12_storage-architecture.md` line 161 is the Postgres-failover
  write-durability row that names the same reconstruction for billing events in the write-ahead buffer. The
  retention of that per-session cumulative is stated in §4.7 only by the `llm_request_completed` row of the
  message-schema table at `spec/04_system-components.md` line 731, which moves to §28 with the table; the
  surviving `ReportUsage` RPC row at `spec/04_system-components.md` line 663 states that the gateway pulls
  token counts and persists them, with no retention and no re-report across a gateway restart. All four
  pointers are therefore rewritten by hand to the §28.5 card that owns the intra-pod runtime-operations
  channel, in the same change as the reduction, so the crash-recovery reconstruction rule keeps naming the
  section that states the mechanism it depends on.
- `spec/15_external-api-surface.md` line 2115, in §15.4.3, which sends a Standard-level runtime author to
  "the tools listed in Part A of this section" for the platform MCP server's tool set. The only enumeration
  of that tool set is Part A at `spec/04_system-components.md` line 697, which moves to §28, and after the
  reduction no Part A exists in either section, so the reference is rewritten by hand to name the §28.5 card
  that owns the intra-pod MCP servers. The other §4.7 pointer in §15.4.3, at
  `spec/15_external-api-surface.md` line 2116, is not a member and takes no edit: its
  "identical in mechanism to the lifecycle channel handshake
  ([Section 4.7](04_system-components.md#47-runtime-adapter), item 1)" parenthetical cites item 1 of §4.7's
  `#### Adapter-Agent Security Boundary` at `spec/04_system-components.md` line 890, which is where the
  manifest-nonce handshake is stated and which the reduction boundary above leaves in `spec/04`. No sentence
  in Part A, Part B, or the message-schema table states a nonce handshake, so §28.5 would carry no such
  mechanism and no `item 1` for the parenthetical to name. Repointing it would state a mechanism identity
  against a card that does not state the mechanism, which is the defect the line 2733 correction above
  records for the version-negotiation pointer.
- `spec/15_external-api-surface.md` line 2435, in §15.4.6, which states that the local observed-level probe
  and the registration-time admission check "both compare declared against the `lifecycle_support`
  handshake from [§4.7]". The handshake moves to §28, so the pointer is rewritten by hand to the same §28.5
  card as the `spec/05` line 47 member, and the two statements of the same comparison keep naming one owner.
- `docs/runbooks/credential-rotation-failure.md` lines 11 and 19, the `symptoms` entry in the runbook's
  front matter and the opening sentence of its body, both of which read "the §4.7 credential rotation
  handshake failed for an active session". The `credentials_rotated` and `credentials_acknowledged` frames
  that handshake consists of move to §28 with the table, so both sites are rewritten by hand to the §28.5
  card that owns the intra-pod runtime-operations channel. Neither string is mirrored from an alert
  annotation in `pkg/alerting/rules`, so the edit is confined to the runbook and tier 11's
  alert-to-runbook resolution is unaffected.

Each of the fifteen §4.7-pointer members is corrected in the same change as the §4.7 reduction. None of them
is reachable by a pass, on one structure: §4.7 keeps its heading and its `#47-runtime-adapter` anchor, so
`tests/spec-anchor-moves.json` carries no entry and the anchor pass leaves the citation alone; none carries
a line citation, so the line pass does not read it; and SPEC-1's name pass rewrites any reserved phrase on
the line to the current spelling while leaving the section pointer wrong, which is the structure the
§15.4.5 item 7 member above already records. The class's proof is review plus the tier-11 successor-pointer
check where the rewritten sentence names a successor heading, per §3.4.

**The reduction and the line pass over the two reduced files are one atomic sub-step.** `spec/04` §4.7
runs from line 637 to line 968, and 697 Go citations of the form `§4.8 line N`, `§4.9 line N`, and
`§4.9.1 line N` point below it; `spec/15` §15.4 runs from line 1458 to line 2459, with 39 `§15.5 line N`
citations below it. A further 294 citations of the range spelling point into §4.7, §4.8, §4.9, §15.4, and
§15.5 and are inside the same blast radius, for example
`pkg/adapter/controlchannel.go` line 89 (`§4.7 lines 652-662`) and
`pkg/api/v1/session/delivery_receipt.go` line 13
(`§15.4 lines 1725-1737`). A further 86 citations across 57 files use the comma-list spelling into the
same sections, for example `cmd/lenny-gateway/adminrouter.go` line 205
(`§4.8 lines 1057-1058, 1077`). A further 39 citations use the path-form spelling into the same ranges,
naming `spec/04_system-components.md` at or below line 637 or `spec/15_external-api-surface.md` at or
below line 1458, for example `pkg/adapter/metrics.go` line 12 (`spec/04_system-components.md lines
870-888`) and `pkg/credential/lease.go` line 150 (`spec/04_system-components.md line 1145`), and the
en-dash range spelling reaches the same sections, for example
`pkg/gateway/policy/policy/authevaluator.go` line 47 (`§4.8 lines 1025–1028`). Removing content from
either section shifts every one of those line numbers, and
each citation whose target leaves its section becomes a new citation resolver failure relative to the
baseline proposal 0065 seeds.
`tests/spec-anchor-moves.json` cannot rescue them, because §4.8, §4.9, and §15.5 do not move and have no
retired anchor, so the map has no entry for them. The `specshift` line pass therefore converts every
citation into `spec/04` and `spec/15`, in every section of those two files, in every carrier, and in every
spelling of the retired citation form §4.6 states, to anchor citations inside the same change that removes the
content, and the exit criterion is that the resolver reports no failure the baseline does not already
carry, together with tier 11. The criterion is stated against the baseline rather than as a green resolver
because the resolver is red on introduction against roughly 1,500 pre-existing stale citations, per §4.6,
so "the resolver is green" is unreachable here and would not distinguish a citation this reduction broke
from one that was already stale.

**The `spec/04` numbered subsection headings land inside this atomic sub-step, between the reduction and
the line pass.** Insert §4.4.1 through §4.4.5 into `### 4.4 Event / Checkpoint Store`
(`spec/04_system-components.md` line 220) and §4.7.1 through §4.7.11 into `### 4.7 Runtime Adapter`
(`spec/04_system-components.md` line 637), each with the `spec/README.md` row and the
`tests/spec-map.json` key the heading walker requires, written in this same change. The titles are
authored from the paragraph subjects that remain in each section after the reduction, so the insertion
adds headings without rewriting the prose under them, and there is no `pending-implementation` case,
because the prose each heading names is already written. The reason is the ordering reason SPEC-4 states
for the same insertion into `spec/10` and `spec/13`, and for `spec/04` it binds one sub-step earlier: this
sub-step's line pass converts every citation into `spec/04`, in every section of the file, to an anchor
citation, so at SPEC-4's turn no `§4.4 line N` or `§4.7 line N` citation is left in the tree for a later
heading insertion to serve, and no pass in this proposal re-points a whole-section anchor to a subsection
anchor. Measured over `git ls-files` under the read exclusion §4.6 states, the population is 513
occurrences of the `§4.4 line(s) N` form and 106 of the `§4.7 line(s) N` form, and inserting the headings
in SPEC-4 would retire all of them onto `#44-event--checkpoint-store` and `#47-runtime-adapter`, which is
the precision loss the insertion exists to prevent. Ordering inside this sub-step is therefore fixed: run
the reduction, insert the headings over the prose that survives it, then run the line pass over the
shifted tree, so the pass reads the final line numbers and converts each §4.4 and §4.7 citation to the
subsection anchor that contains its line. The headings are inserted after the reduction rather than before
it because §4.7 loses the Part A, Part B, and message-schema-table prose at
`spec/04_system-components.md` lines 695 through 731 to §28, so titles authored before the reduction would
name material that leaves the section. Both orderings put the insertion ahead of the line pass, which is
the property the citations depend on.

The line pass rewrites `schemas/*.proto`, whose comments the committed stubs under `pkg/proto/` mirror and
which no pass writes, so this sub-step runs `make generate-proto` after the line pass and takes the tier-0
proto no-drift test proposal 0065 adds as a further exit criterion. Without it `pkg/proto/` holds the
pre-reduction citations at the sub-step's exit, and several of them point into the shifted ranges:
`pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 178, 189, 194, and 1343 cite `§4.7` lines 632, 631,
660, and 942, `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 3287 cites `§4.7 line 822`, and
`pkg/proto/tokenservice/v1/lenny-tokenservice.pb.go` line 622 cites `§4.9 lines 1246-1298`. §4.6 states
that a generated file is read by the resolver, so leaving the regeneration to SPEC-4 would break the
atomicity this sub-step rests on. This is the treatment the sub-step already gives the two OpenAPI-derived
producers below.

The same change runs the anchor pass over the markdown links into the retired §15.4 and §4.7
subsections, because those headings cease to exist the moment the reduction lands.
`tests/spec-anchor-moves.json` records the retired anchors and their successors in §28, and SPEC-4 empties
it once the tree-wide pass has run.

The atomic sub-step covers the two carriers the resolver would otherwise miss. The first is
`pkg/gateway/externalapi/openapi/openapi.json`, which carries twenty line citations inside served JSON
values, nineteen in a `description` and one in the `summary` at line 2200 that `openapi-to-mcp` copies
into the generated tool inventory, among them `§15.4 line 1715` at line 461, a line inside the range this
reduction shifts. That file is embedded with `//go:embed` and served at `/openapi.yaml`, `/openapi.json`, and
`/v1/openapi.json`, and it is the source the MCP tool schema generator reads, so a stale citation ships to
clients. Those descriptions lose the citation rather than gaining an anchor, because a served client
artifact is not a place to cite the specification, and the same rule applies to the schema descriptions in
`pkg/gateway/mcpfabric/mcptools/mcptools.go`. A tier-3 assertion that the served OpenAPI document and the
committed generated MCP tool schemas carry no citation of the retired line form lands with them. The
assertion is stated over the line form rather than over every citation form, because the strip this
sub-step performs is scoped to the line form and `pkg/gateway/externalapi/openapi/openapi.json` carries
citations that name a section and no line, which survive it: of the 75 section-symbol citations in that
file, 21 name a line and the rest sit on 47 lines that name only a section, among them the
`§10.2 OIDC/OAuth 2.1 access token` description at line 18. The two artifacts derived from it inherit the
same state, so `pkg/gateway/mcpfabric/mcptools/generated_schemas.go` still carries the `§11.5` and `§7.3`
descriptions in the served `lenny/create_session` input schema after the regeneration. Removing those as
well is a served-description text change with no citation gate behind it, which this proposal does not
stage, so an assertion over every form would have no route to green at the sub-step that lands it.

Two committed generated artifacts are derived byte for byte from `openapi.json` and carry the citation
form, so this sub-step regenerates them rather than editing them and takes their drift tests as exit
criteria. `pkg/gateway/mcpfabric/mcptools/generated_schemas.go` is `genmcpschemas` output carrying seven
citations inside the served `lenny/create_session` input schema, its producer is
`go generate ./pkg/gateway/mcpfabric/mcptools/...` rather than `make generate`, and
`TestGeneratedSchemasMatchOpenAPI_spec_15_2_1_1386` requires byte identity with a fresh derivation.
`pkg/ops/mcp/generated_tools.go` is `openapi-to-mcp` output pinned by
`TestGeneratedToolsMatchOpenAPI_spec_25_12` and is refreshed by `make generate`. Both producers run inside
this sub-step, so the two artifacts do not sit drifted between here and SPEC-4, and the same
citation-removal rule applies to both sides so the derived schemas keep matching the document they mirror.
The second is the
tier-11 tests that pin §4.7 prose: `tests/tier11_docs/eviction_coordinator_route_consistency_test.go`,
`tests/tier11_docs/recycle_scrub_trigger_consistency_test.go`, and
`tests/tier11_docs/budget_extension_trigger_consistency_test.go` all scope assertions to `"### 4.7 "`.
Every row they pin sits above the reduction boundary stated above, which opens at
`spec/04_system-components.md` line 691: the `Terminate` (proto `Shutdown`) row at line 664, which
`tests/tier11_docs/recycle_scrub_trigger_consistency_test.go` lines 65 and 139 select with `requireLine`;
the `AdapterTerminating` event row at line 688, which
`tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 88 selects the same way; and the
whole-section absence assertion over `ExtendLease` at
`tests/tier11_docs/budget_extension_trigger_consistency_test.go` line 175, which stays true over a §4.7
that has lost only the intra-pod block. §4.7 therefore keeps the gateway-to-adapter RPC tables at lines
643 through 671 and the adapter-to-gateway event table at lines 679 through 689, and all three files stay
green without an edit at this sub-step. They are named here so an implementor confirms that state rather
than assuming the reduction reached them, on the same rule as the two embedded-anchor files below. The
standing rule holds that a row relocated into a §28.5 card carries its pin with it, re-scoped to
`spec/28`, and under the boundary stated above no row these three tests pin relocates, so the rule stages
no edit here. Tier 11 is an exit criterion of this sub-step alongside the resolver. The one edit any of
the three takes lands elsewhere: `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line
69 pins a §4.6.1 clause carrying a reserved noun phrase, and SPEC-1's name pass rewrites that literal with
the §4.6.1 prose it asserts, under the tier-11 literal class §3.4 states.

Two further tier-11 files pin §15.4 subsection anchors without using any of those three helpers, and the
narrowed reduction is what keeps them green rather than a rewrite.
`tests/tier11_docs/embedded_mode_anchors_test.go` line 43 requires the heading slug
`1543-runtime-integration-levels` to exist in `spec/15_external-api-surface.md`, and
`tests/tier11_docs/embedded_echo_placement_test.go` line 38 requires `1544-sample-echo-runtime`; both
assert against `headingSlugs()` over the live file. `tests/tier11_docs/embedded_echo_placement_test.go`
line 116 additionally requires `spec/17_deployment-topology.md` to contain the verbatim string
`[Section 15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)`. Because §15.4.3 and §15.4.4 keep
their headings and their anchors, `tests/spec-anchor-moves.json` carries no entry for
`1543-runtime-integration-levels` or `1544-sample-echo-runtime`, the anchor pass leaves the
`spec/17_deployment-topology.md` links at lines 181, 291, and 353 untouched, and both files stay green
without an edit. They are named here so an implementor confirms that state rather than assuming the
reduction reached them. The retired §15.4 anchors are the one numbered anchor
`1541-adapterbinary-protocol`, together with the anchors of the
unnumbered headings inside the §15.4.1 block that the reduction retires, which are
`internal-messagepart-format`, `protocol-reference--message-schemas`, and the eight `##### Inbound:` and
`##### Outbound:` message-schema anchors under the last of them. `tests/spec-anchor-moves.json` carries an
entry for each of those anchors with its §28 successor, and no other anchor of this file.
`1542-rpc-lifecycle-state-machine`, `translation-fidelity-matrix`, and
`messageenvelope--unified-message-format` are absent from the map,
because all three headings survive the carve-outs above.

Add the `spec/README.md` rows for `spec/29`, for §28.5 through §28.8, and for the §28.5.1 through §28.5.7
card headings, and revise the §4.7 and §15.4 rows so the index describes what those sections contain after
the reduction. Take the link text and the anchor for every one of those rows from the heading table in
§4.8, which fixes the title and the derived anchor of each heading, including the boundary order the
§28.5.1 through §28.5.7 card headings land in. The card headings are level-4 and the index carries only one
level-4 row today, so they are
an explicit addition rather than a consequence of the depth convention: they are the citable handles every
later remediation step uses, and the heading walker's predicate names them for that reason. Neither §4.7
nor §15.4 has a subsection row in the index on the unmodified tree, and §4.7 has no numbered subsections
there, so there are no
subsection rows to revise for either. The §4.4.1 through §4.4.5 and §4.7.1 through §4.7.11 rows this
sub-step writes for the headings it inserts are additions on the same rule.

Write, in the same change as those rows, the other half of the heading walker's predicate for every
heading this sub-step creates: a `tests/spec-map.json` key, or an entry in `tests/spec-map-exceptions.yaml`
under the `pending-implementation` reason class proposal 0065 adds, carrying the `blocker` and `opened_at` fields
that class requires. The headings are §28.5, §28.6, §28.7, §28.8, the §28.5.1 through §28.5.7 card
headings, `## 29`, each `### N.M` subsection of `spec/29`, and the §4.4.1 through §4.4.5 and §4.7.1
through §4.7.11 headings this sub-step inserts into `spec/04`. The `spec/28` and `spec/29` headings are
named in §4.8; the §4.4 and §4.7 titles are authored inside this sub-step from the paragraph subjects that
survive the reduction, so their keys are written against the titles the insertion produces. The §28.1 through
§28.4 headings are not among them, because proposal 0067 created them together with their sections and SPEC-1
wrote their key or their exceptions entry, so this sub-step neither writes nor retires coverage for them.
Without this the walker is red at the exit of the
sub-step that lands it, on headings that carry a `spec/README.md` row and no spec-map coverage. The
existing accepted reason set is hard-coded at `cmd/lenny-test/cmd_validate_yaml.go` lines 185 through 193
and the validator runs inside the hard-failing `validate-maps` tier-0 check
(`cmd/lenny-test/cmd_run.go` lines 734 and 742), so an entry under a reason class that file does not
carry fails tier 0 rather than passing silently, which is why the `pending-implementation` class is a
deliverable of proposal 0065.

Seed the claim register from the reference document's status tables, and land its schema-only tier-0
validator over `tests/claim-map.json` in the same change. Two rows are written explicitly rather than read
off those tables, because the tables carry no row for either, a third is written for the corrected
compliance-suite enumeration, and a fourth group is written for the request-message fields SPEC-2 adds.
The first is the metric half of N4: status
`ABSENT`, `deferral_id` R12, naming the two metric names at `pkg/adapter/metrics.go` lines 71 and 79 that
keep the retired spelling until R12 adds the adapter metrics endpoint and the catalog entries. The second
is the agent podspec's missing mTLS certificate material, per §4.4 and SPEC-1: status `ABSENT`,
`deferral_id` R14, which is the plan's agent-pod mTLS client identity step. The third is the
runtime-ops events schema that the corrected `spec/24` line 114 sentence, the corrected
`docs/reference/adapter-contract.md` table, and the corrected
`docs/runtime-author-guide/publishing.md` sentence name as an artifact the external-adapter compliance
suite asserts against: status `ABSENT`, `deferral_id` R8, naming `cmd/lenny-compliance/schemavalidate.go` as the
surface that compiles two schema files and reads no third. The fourth group is one row per field SPEC-2
adds to a gateway-to-pod request message, which is `coordination_generation` on every operational
gateway-to-pod request message that lacks it, the slot identifier on `InterruptRequest`,
`SignalDeadlineRequest`, `ReportUsageRequest`, and `CheckpointBarrierRequest`, and `ResumeRequest.slot_id`.
Each row is written explicitly for the same reason the three above are, which is that the field does not
exist in the tree before SPEC-2 adds it and no status table in the reference document can carry a row for
it. Each carries status `UNWIRED` and a `deferral_id` naming the step the plan assigns as the field's
reader, which is R16 for the generation fence and R22 for the slot identifiers
(`gateway-runtime-comms-remediation.md` lines 438, 1950, and 1958). Every seeded row carries a `deferral_id`
naming a step the plan states, which is what the validator's rule for a row that is not `WIRED` requires. This sub-step is the one that supplies the
validator's route to green, because the register does not exist before it and the validator fails a
missing register file, and every rule the validator checks reads a seeded row or a `spec/28` heading this
sub-step writes. TEST-1 states the validator's predicate and adds its cases.

### SPEC-4. Anchor and line-citation retirement

**Target:** every citation of the retired form §4.6 states, in the tree under the read exclusion
§4.6 states, in every spelling that form
covers, which includes the 556
section-level occurrences that carry no subsection component across 148 files, the 617 comma-list
occurrences across 341
files, the 50 slash-separated and 2 `and`-separated multi-member occurrences across 42 files, the
`+`-separated multi-member occurrences across 11 files, the 136
qualified occurrences across 68 files, the 65 en-dash range occurrences across 38 files, the 123
path-form occurrences across 59 files, and the colon-form occurrences, which are 18 across 10 files in the
section-number variant and 11 across 7 files in the path variant; in every carrier of the
form, which is the `// spec:`,
`-- spec:`, and `# spec:` comment dialects, YAML block scalars, JSON string values, and Go string literals
holding served schema descriptions; the generated artifacts listed in §4.6, which are
`pkg/embedded/manifests/manifests.yaml`, `pkg/embedded/crds/`, `charts/lenny/crds/`, `pkg/proto/`,
`pkg/gateway/mcpfabric/mcptools/generated_schemas.go`, `pkg/ops/mcp/generated_tools.go`,
`charts/lenny/values.schema.json`, `docs/alerting/rules.yaml`,
`docs/alerting/routing-recommendations.md`, and `schemas/ocsf-mapping.yaml`,
regenerated rather than rewritten; the hand-applied annotation blocks in `charts/lenny/crds/` and the
literal prefixes that match them in `tests/tier0_static/crds_test.go`; every intra-repo markdown fragment
link in `spec/` and `docs/`;
`tests/spec-map.json`; the seventeen straddling range citations across fifteen files §4.6 enumerates,
hand-corrected; the six authored path-form citations naming the nonexistent
`11_security-trust-model.md` that §4.6 enumerates, hand-corrected; the paragraph at `spec/04_system-components.md` line 489;
`tests/tier11_docs/eviction_coordinator_route_consistency_test.go`; and
`tests/tier0_static/degradation_lock_line_citation_test.go`, whose predicate requires the retired citation
form to be present and which is hand-rewritten here; the seven markdown fragment links enumerated below
whose target heading does not exist today; `spec/10_gateway-internals.md` §10.1 and
`spec/13_security-model.md` §13.2, for the numbered subsection headings inserted below, with the
`spec/04_system-components.md` §4.4 and §4.7 headings inserted in SPEC-3 for the ordering reason that
sub-step states; and `gateway-runtime-comms.md`, for the point-in-time header that freezes it. The fragment-link gate lands in this sub-step, because this is
the sub-step whose anchor pass rewrites the links into the retired `15.4.1` anchor.

**Rationale:** The citation and link rewrite is a large diff with no judgement in it, and its risk is
concentrated entirely in the tooling, which is why the tooling ships first with its own tests and a dry-run
gate. The paragraph break is the exception and is hand-authored.

**Change (staged description).** Run the anchor pass tree-wide to rewrite each remaining redirected
citation and each remaining markdown link into a retired anchor to its successor, then empty
`tests/spec-anchor-moves.json` and `tests/registers/anchor-senses.yaml`. The entry criterion for emptying
both is run completeness measured
against the tree, which is that a search for each retired anchor the map names returns zero remaining
citations and zero remaining markdown links, because an empty map is also what a pass that resolved
nothing leaves behind. The sense register SPEC-3 seeds is retired on the same criterion, since a retired
anchor with no remaining occurrence has no occurrence left to disambiguate. The pass rewrites markdown fragment links as well as comment
citations, and its markdown domain is the fragment-link gate's domain, which is every link whose target is
a tracked `.md` file or the citing page itself, so the same-page `[...](#anchor)` form is rewritten
alongside the file-qualified `[...](NN_file.md#anchor)` form. Stating the domain that way is what keeps
the pass and the gate over one population: reducing §15.4 retires the §15.4.1 subsection and
`spec/` and `docs/` carry 36 markdown links into its numbered anchor
`1541-adapterbinary-protocol`, of which 24 are same-page links
inside `spec/15_external-api-surface.md` itself and 11 are file-qualified links across `spec/` and
`docs/`. Seven of those 11 are the `spec/07_session-lifecycle.md` links SPEC-3 hand-corrects to the
surviving `MessageEnvelope` heading before this pass runs, so this pass rewrites the other four. Seven of
the 24 same-page links, at `spec/15_external-api-surface.md` lines 1838, 2165, 2489, 2584 (the first of the two
links on that line), 2662, 2684, and 2733, are hand-corrected in that earlier sub-step to the heading whose
material each cites, which is the surviving `MessageEnvelope` heading for the first six and the surviving
§15.4.2 heading for line 2733,
so this pass rewrites the other 17. A pass matching the file-qualified form alone would leave those 17 same-page links pointing at
deleted anchors and the gate red on them, including the four links SPEC-4 names by line inside §15.4.3
through §15.4.6 at lines 2163, 2164, 2394, and 2441, and the second of the two links on line 2584. The
remaining link of the 36 is the absolute GitHub URL at `docs/reference/adapter-contract.md` line 371,
which neither the pass nor the gate reads. It cites the retired anchor for the Translation Fidelity
Matrix, which the carve-out keeps in `spec/15`, so it is a member of the carve-out class §3.4 states and
SPEC-3 rewrites its fragment by hand to `#translation-fidelity-matrix` and its `Spec §15.4.1 -- Translation
Fidelity Matrix` label to `Spec §15.4 -- Translation Fidelity Matrix`, under the target-and-label rule §3.4
states, in the same change that splits the heading. Among the
file-qualified links is
`spec/17_deployment-topology.md` line 361, which sends a Source Mode reader to the stdin/stdout JSON Lines
protocol. `spec/04_system-components.md` carries no link into the retired `1541-adapterbinary-protocol`
anchor, so the anchor pass
stages no edit there; its line 967 link to `#1543-runtime-integration-levels`, which carries the intra-pod
MCP nonce wire format, targets a surviving anchor and is confirmed untouched in the same way the two
tier-11 anchor files are. The retired §15.4.1 block also retires the two unnumbered `####` anchors and the
`#####` children SPEC-3 names, which are `#internal-messagepart-format` and
`#protocol-reference--message-schemas`, and one further markdown link targets one of them:
`spec/15_external-api-surface.md` line 1399 links to `#internal-messagepart-format`, and the pass rewrites
that link from the same map. The same line's link to `#translation-fidelity-matrix` and
`spec/21_planned-post-v1.md` line 31's link to the same anchor keep their targets, because that heading and
its anchor survive the carve-out SPEC-3 states. The `spec/21_planned-post-v1.md` line 31 link still takes
one hand correction in SPEC-3, to its `Section 15.4.1` label rather than to its target, because the label
names the subsection the reduction retires. The 30 links whose target
is one of the `1543` through `1546` anchors are untouched, because §15.4.3 through §15.4.6 keep their
headings and their anchors, per SPEC-3. Links written inside those four subsections are a separate
population and are rewritten like any other: four of them target the retired numbered anchor
`1541-adapterbinary-protocol`, at `spec/15_external-api-surface.md` lines 2163, 2164, 2394, and 2441. Two
further links in that range are untouched. Line 2395 targets `1542-rpc-lifecycle-state-machine`, which the
§15.4.2 carve-out keeps in `spec/15` and which `tests/spec-anchor-moves.json` therefore carries no entry
for. Line 2165 is one of the seven SPEC-3 hand-corrects, because it cites the surviving `MessageEnvelope`
heading.

Correct the seven markdown fragment links whose target heading does not exist on the unmodified tree, by
hand, in the same change that lands the fragment-link gate. Each names a heading that was renamed at some
point without its inbound links being updated, so the anchor map has no entry to redirect and no pass
produces the replacement. The gate is red on introduction against exactly these seven and green once they
are corrected, which is why they are enumerated rather than registered: a permanently wrong link resolves
to no open remediation item, so the shared exception register's `blocker` rule cannot hold it, and the
proposal's standing rule forbids widening the gate or suppressing the finding. The links the SPEC-3
reduction retires are not part of that count, because the anchor pass rewrites each of them to its §28
successor earlier in this same sub-step, with the seven `spec/07_session-lifecycle.md` links and the seven
same-page `spec/15_external-api-surface.md` links SPEC-3
hand-corrects to the surviving heading each cites already rewritten in that earlier sub-step, so
the gate reads a tree in which every retired anchor has already been redirected. The same-page fragments
SPEC-3 carries out of `spec/15` and `spec/04` into `spec/28` are likewise already rewritten to their
file-qualified form, so the gate reads them as resolving and the count of seven stands.

| Link site | Current target | Heading that exists | Corrected target |
|:--|:--|:--|:--|
| `spec/09_mcp-integration.md` line 56 | `08_recursive-delegation.md#85-platform-tool-inventory` | `### 8.5 Delegation Tools` (`spec/08_recursive-delegation.md` line 525) | `08_recursive-delegation.md#85-delegation-tools` |
| `docs/runbooks/artifact-replication-residency-violation.md` line 82 | `../../spec/17_deployment-topology.md#175-cloud-deployment-shapes` | `### 17.5 Cloud Portability` (`spec/17_deployment-topology.md` line 382) | `../../spec/17_deployment-topology.md#175-cloud-portability` |
| `docs/runbooks/legal-hold-escrow-residency-violation.md` line 84 | `../../spec/17_deployment-topology.md#175-cloud-deployment-shapes` | the same heading | `../../spec/17_deployment-topology.md#175-cloud-portability` |
| `docs/runbooks/ops-lock-split-brain.md` line 78 | `../../spec/25_agent-operability.md#254-remediation-locks-and-escalations` | ``## 25.4 The `lenny-ops` Service`` (`spec/25_agent-operability.md` line 783) | `../../spec/25_agent-operability.md#254-the-lenny-ops-service` |
| `docs/runbooks/ops-lock-split-brain.md` line 78 | `../../spec/10_gateway-internals.md#104-runtime-extensibility` | `### 10.4 Gateway Reliability` (`spec/10_gateway-internals.md` line 376) | `../../spec/10_gateway-internals.md#104-gateway-reliability` |
| `docs/runbooks/otlp-plaintext-egress-detected.md` line 79 | `../../spec/13_security-model.md#132-network-policy` | `### 13.2 Network Isolation` (`spec/13_security-model.md` line 32) | `../../spec/13_security-model.md#132-network-isolation` |
| `docs/runbooks/admission-plane-feature-flag-downgrade.md` line 151 | `#` (empty fragment on the citing page) | `### Step 5 — Post-incident drift-snapshot refresh` (`docs/runbooks/admission-plane-feature-flag-downgrade.md` line 149) | `#step-5--post-incident-drift-snapshot-refresh` |

The seventh is the one link in `spec/` and `docs/` written as `](#)`. Its path is empty, so its target is
the citing page, which is a tracked `.md` file and therefore inside the gate's domain, and its empty
fragment matches no heading slug and no anchor attribute. The `.html` exclusion below does not reach it,
and a permanently wrong link holds no shared-register entry, so it is corrected here with the other six
rather than excluded.

Two of the seven point at a section whose current subject differs from what the citing sentence expects, so
each corrected target is checked against the citing sentence before it is written rather than resolved by
section number alone. The gate's domain is a link whose target is a tracked `.md` file or the citing page
itself. A link written to the rendered documentation site, which is the `.html` form the `docs/` pages use
for site-internal navigation, is resolved by the site generator rather than against a markdown heading and
is outside the gate. Run the line pass over
every carrier of the citation form to convert line citations to anchor citations, driving every per-file
count in the line-citation register to zero.

Hand-correct the straddling range citations §4.6 enumerates before the final line-pass run, because the
pass fails each of them rather than guessing an anchor and they would otherwise hold their files above
count zero. Each is rewritten to name the section that contains the content it cites.

Hand-correct the path-form citations whose named file does not resolve under `spec/`, in the same run and
for the same reason. §4.6 enumerates today's population, which is the six authored sites naming
`11_security-trust-model.md` at `pkg/audit/ocsf/catalog.go` line 149,
`pkg/audit/ocsf/catalog_test.go` lines 26, 44, 75, and 113, and `cmd/lenny-ocsf-mapping-gen/main.go`
line 10. Each is rewritten to the `spec/11_policy-and-controls.md` §11.7 anchor, which is the heading that
contains both line 414 and line 365. `pkg/audit/ocsf/catalog.go` line 149 is the `mappingHeader` const, so
`go run ./cmd/lenny-ocsf-mapping-gen` runs after that edit and carries the correction into
`schemas/ocsf-mapping.yaml`, with `TestMappingYAMLInSync` as the exit criterion §4.6 already states.

The sub-step's exit criteria are that every per-file count in the line-citation register is zero and that
the pass reports no remaining straddling range and no remaining unresolvable path-form citation, so the
flat-prohibition end state §4.6 describes is reached rather than
blocked by a fail-closed rule with no remedy. The register carries a per-file count for every file inside
the resolver's and the ratchet's read domain, so the criterion is measured over that domain rather than
over `BUILD-GAPS.md`, `TEST-GAPS.md`, and the two root planning documents, which
the passes and the gates never open. A generated artifact inside that domain reaches zero through the
regeneration of its source rather than through a write.

Run the producers of every generated artifact §4.6 lists afterwards, so each follows its source. That is
`make generate`, `make generate-proto`, `go generate ./pkg/gateway/mcpfabric/mcptools/...`,
`go run ./cmd/lenny-chart-schema-gen`, `go run ./cmd/lenny-ocsf-mapping-gen`, the
re-application of the hand-applied CRD layers described below, and the chart-to-embedded CRD re-copy, in
that order. Run `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4`,
`TestEmbeddedCRDsAreCopiesOfChartManifests_spec_10_437`,
`TestEmbeddedCRDsCarrySchemaVersionAnnotation_spec_10_437`,
`TestEmbeddedCRDsPreserveUnknownFields_spec_10_437`, `tests/tier0_static/crds_test.go`, the tier-0 proto
no-drift test, `TestGeneratedSchemasMatchOpenAPI_spec_15_2_1_1386`,
`TestGeneratedToolsMatchOpenAPI_spec_25_12`, `TestSchemaIsCommitted_spec_17_6_655`, and
`TestMappingYAMLInSync` as exit criteria.
`go run ./cmd/lenny-chart-schema-gen` and `go run ./cmd/lenny-ocsf-mapping-gen` are named explicitly
because no Makefile target reaches either, so a
regeneration sequence that names only the `make` targets leaves `charts/lenny/values.schema.json` stale
against the `desc:` tags the line pass rewrote and `TestSchemaIsCommitted_spec_17_6_655` red, and leaves
`schemas/ocsf-mapping.yaml` stale against the `mappingHeader` const the line pass rewrote,
`TestMappingYAMLInSync` red, and that file above count zero in the line-citation register.

Most of the chart CRD text is controller-gen output whose citations originate in the doc comments on
`pkg/apis/lenny/v1alpha1/*.go`, and controller-gen re-wraps description text at a fixed width, so a direct
substitution into the CRD would be stale against the Go types and would not match what regeneration emits.
The exception is the two hand-applied post-generation layers §4.6 describes, which `make generate`
deletes and which nothing regenerates: the `lenny.dev/schema-version` annotation with its comment block,
one per CRD file, and the top-level spec and status `x-kubernetes-preserve-unknown-fields: true` markers.
Re-apply both after `make generate` and before the chart-to-embedded re-copy.

The ten citations inside those five annotation blocks are hand-edited in this sub-step rather than left to
the line pass, because the pass does not write a generated file and regeneration does not reproduce them,
so they would otherwise hold `charts/lenny/crds/` above count zero and block the ratchet's
flat-prohibition end state. They carry the section-level spelling `# spec: §10 line 437 / §10 line 443`
(`charts/lenny/crds/lenny.dev_runtimes.yaml` line 7 and the same line in the four sibling CRD files), which
the widened citation form §4.6 states matches and the subsection-only form would not, so their disposition
is a hand edit rather than an exclusion. The citation text is required as an exact literal:
`tests/tier0_static/crds_test.go` lines 178
through 183 recognize the block by the exact prefix `"# spec: §10 line 437"` and strip it before comparing
against controller-gen output, so converting the block to the anchor form without updating that prefix
would stop the normalizer stripping the line and turn the drift test red. The prefix literals move in the
same edit, and `tests/tier0_static/crds_test.go` is a target of this sub-step for that reason.

Rewrite `tests/tier0_static/degradation_lock_line_citation_test.go` in the same change as the line pass
over `pkg/ops/`, because that file is a running tier-0 gate whose predicate is the presence of the retired
form rather than a matcher precedent. `TestSpec254DegradationWarningLineCitationsAreFresh` compiles
`§25\.4 lines? (\d+)(?:-(\d+))?` at line 38, reads the comment block above each declaration it names, and
calls `t.Fatalf` when the expression does not match (lines 104 and 105), so converting
`pkg/ops/opsidem/writers.go` line 53 and `pkg/ops/coordination/service.go` line 153 to the anchor form
turns tier 0 red. Tier 0 hard-fails on a failing `go test ./tests/tier0_static/...`
(`cmd/lenny-test/cmd_run.go` lines 717 and 745 through 748), and SPEC-4's other exit criteria are all
satisfiable while it is red, so the sub-step would otherwise end broken. The test is rewritten rather than
deleted, because its `wantSubstring` check is a freshness property the resolver and the ratchet do not
carry: they read whether a citation resolves and whether a count rose, not whether the cited section
contains the quoted sentence. The rewritten predicate reads the anchor-form citation above each
declaration and requires the §25.4 heading it names to have a body containing that declaration's
`wantSubstring`. This is a predicate change to a running tier-0 gate rather than new tooling, and the
rewritten predicate is green from the moment it lands, so TEST-1 names its cases on the same rule the
change-graph completeness check and the widened exceptions validator are held to. Tier 0 over `pkg/ops/`
is an exit criterion of this sub-step for that reason. The same
hand rewrite covers the two colon-form path citations that file carries in its comments, at lines 74 and
77 (`spec/25_agent-operability.md:2057` and `spec/25_agent-operability.md:2215`), which the widened
citation form §4.6 states matches and which would otherwise hold the file above count zero after the
predicate rewrite.

`spec/17_deployment-topology.md` line 450 carries the only colon-form citation inside `spec/`: the
bootstrap Job's readiness-poll reference `(§17.6:404)` inside the `playground.devTenantId` ordering
paragraph. The line pass converts it with every other colon-form citation, to the anchor for
`### 17.6 Packaging and Installation` at `spec/17_deployment-topology.md` line 391. It is named here because a citation inside `spec/` is the
one place where a spelling the matcher misses would leave the specification contradicting the naming law
the same change makes normative.

Break the oversized multi-contract paragraph at `spec/04_system-components.md` line 489 into separately
addressable blocks, which five later steps would otherwise contend over. This part is a hand-authored edit
rather than part of the mechanical pass, because it re-cuts a normative paragraph that a running gate
treats as one unit: `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` selects that
paragraph with `requireLine` and then requires eight substrings and two inline anchor links inside the one
line, so splitting it into five paragraphs fails every assertion whose substring lands outside the first
block. That test moves with the edit, with each `requireLine` re-scoped to the block carrying its
substrings, and tier 11 is an exit criterion of this sub-step alongside the citation resolver.

Land the gate-integrity meta-gate in this sub-step, in `tests/tier0_static/`. It asserts that every gate
this proposal registers at tier 0 is reachable as a Go test under `tests/tier0_static/` or as a check
inside `runValidateMaps`, which are the two channels the repository hard-gates, and that none of them is
invoked through a shell script under `scripts/` whose absence or non-zero exit is tolerated. Its predicate
is a fixed list of those gate names, checked against the tier-0 registration, so deleting a gate's file
fails the meta-gate rather than silently removing the gate. Its domain is the tier-0 gates alone. The gates
this proposal registers outside tier 0 are outside it and are named as such: the tier-11
successor-pointer check and the tier-11 artifact-register supersession check, which SPEC-3 lands in
`tests/tier11_docs/`, the tier-11 reference-document freeze check, which this sub-step lands in the same
package, and the tier-3 assertion SPEC-3 lands
with the served OpenAPI document and the generated MCP tool schemas. The harness runs those suites as Go
test packages and fails the tier on a failing test, so each is registered through the channel its tier
uses rather than through `tests/tier0_static/`, and the meta-gate's fixed list and its accepted-channel
set name the same population. The meta-gate lands here rather than with the tooling because several of the gates it
names are registered by SPEC-1 through SPEC-4, among them the naming lint, the identifier-resolution gate,
the heading walker, the claim register's schema-only validator, the §28.8 matrix completeness check, and
the fragment-link gate, and a fixed
list checked against the tier-0 registration
fails on a name that is not yet registered in exactly the way it fails on a deleted one, so any earlier
landing point would leave tier 0 red. It is green on introduction here, because every gate it names is
registered at the point it lands, and TEST-1 names its cases.

**The numbered headings the retirement depends on.** Insert numbered subsection headings into the two
sections whose line citations this sub-step's pass is the first to convert:
`spec/10_gateway-internals.md` gains §10.1.1
through §10.1.8, and `spec/13_security-model.md` gains §13.2.1 through §13.2.7. The titles are authored from
the existing paragraph subjects in each section, so the insertion adds headings without rewriting the prose
under them. The matching insertion into `spec/04_system-components.md` §4.4 and §4.7 lands in SPEC-3
instead, because SPEC-3's atomic line pass has already converted every `spec/04` citation by the time this
sub-step runs, so a heading inserted here would have no line citation left to serve.

This is what makes the retirement a rewrite rather than a downgrade. Retiring a line citation into a
300-line section leaves an anchor citation naming the whole section, which is markedly less precise than the
line it replaced, and a reader following it has to search the section for the sentence the citation meant.
Adding the subsections ahead of the pass that converts those citations gives each of them an anchor at
roughly paragraph granularity, so
the retired precision is preserved rather than lost. Without the headings the retirement still passes every
gate, which is why the insertion has to be stated as a deliverable rather than left to follow from the
ratchet.

The insertion into these two files belongs in this sub-step for the reason that makes it expensive: an
inserted heading shifts every line below it, so it invalidates line citations into those files. `spec/10`
§10.1 begins at line 3, so a heading inserted there shifts essentially every citation into the file. Doing
the insertion here, inside the one exclusive change that is already rewriting every remaining line citation
in the
tree, means the shift is absorbed by the same pass rather than invalidating a population no pass is running
over. Ordering inside the sub-step is therefore fixed: insert the headings first, then run the citation
pass over the shifted tree, so the pass reads the final line numbers. Running the pass first and inserting
afterwards would leave every rewritten citation into these two files stale on landing. The same ordering
rule places the `spec/04` insertion inside SPEC-3, which is where the pass over that file runs.

The new headings enter the heading walker's domain as they are created, so each one lands with a
`spec/README.md` row whose anchor resolves and a `tests/spec-map.json` key, written in this same change.
There is no `pending-implementation` case here, because the section the heading names is already written;
the heading is being added over existing prose.

**Freeze the reference document.** Add a header to `gateway-runtime-comms.md` stating that it is a
point-in-time reading of the working tree at `fcda83e3`, and that §28 and §29 supersede it for all current
behavior. Leave its body unchanged. Land a tier-11 test asserting both halves: the header is present, and
the body below it matches the committed text.

The document is the source this proposal's §28 and §29 content is derived from, and on landing it becomes a
second, unmarked description of the same contract. That is the exact failure mode this proposal exists to
end, reproduced one level up: a reader who finds the reference first reads a description that was accurate
when it was written and drifts from that moment on, and §28's provenance column points at it as a source
without saying it is superseded. Freezing it costs one header and one test, and it is the difference between
a superseded document and a competing one. The test pins the body as well as the header because an
unfrozen reference invites incremental correction, and a corrected reference is a maintained second source
rather than a historical record.

Apply as one exclusive change, scheduled after the wire-contract sub-step, executed by `scripts/specshift`
with a proven dry run as the entry criterion. No other change's output depends on this one, because every
step that needs a citable anchor cites a `spec/28` card, which lives in a new file and needs no in-file
surgery.

### TEST-1. Gates

**Target:** `tests/tier0_static/`, the map validator, `cmd/lenny-test`, and `tests/tier11_docs/`. The
tier-3 and tier-10 tests the wire rename reaches are listed under SPEC-2, because they belong to that
change rather than to the gate set.

**The residual gate.** One gate stands apart from the per-class gates below, because it is what makes
their incompleteness safe. For each enumerated class it computes the set matching the class's broad
predicate, subtracts the enumerated members and the class's residual register, and fails tier 0 on a non-empty
remainder, naming each unclassified member and the class that should own it. Its cases are: a residual in
each class fails and names the member; a member moved into the register as in-class passes; a member
registered as an explicit exclusion with a reason passes; an exclusion whose reason field is empty fails;
an `excluded` entry naming a member the predicate no longer matches fails, so the register cannot
accumulate dead exclusions; an `in-class` entry whose member has stopped matching the class's broad
predicate, whether because the pass handled it, because the tracked path gained a glob key in
`tests/change-graph.json`, or because the skip reason was rewritten to open with one of the categories, is
removed from the register in the same run and the gate stays green, so neither a class the pass empties
nor ordinary change-graph and skip-reason remediation turns tier 0 red on the entries that routed their
own members out; an `in-class` entry whose member no longer matches the predicate and which
survives the run fails, so an in-class entry cannot outlive the remediation it recorded; an `in-class`
entry in the generated-artifact class, whose member still matches the predicate, survives repeated runs
and the gate stays green, so that class is not
required to empty; a file that is a member of a producer's output set, carries no header or
document-metadata generation marker, and appears in neither the enumeration nor
`tests/registers/residual-generated-artifacts.yaml` is reported as a residual and named, so the
generated-artifact predicate's second disjunct is pinned rather than left to the marker branch; a member's
text carried inside `tests/registers/residual-<class>.yaml` itself, or inside the pass or baseline
register the class's own gate excludes, reports no residual, so the scan does not read a register as tree
content; an occurrence in a file the read domain §4.7 states excludes, including `BUILD-GAPS.md` and
`TEST-GAPS.md`, reports no residual, so the gate does not report the populations §5 and §11 promise no
gate reports; and a
malformed or missing residual register fails rather than certifying the
class. Each class has its own `tests/registers/residual-<class>.yaml`, separate from the register or
baseline that drives the class's pass, and it carries the entry schema §4.7 states, which is a
member, a class, an `in-class` or `excluded` disposition, and a reason, so the shared register contract's
expiry and blocker ratchet rules do not range over them. The gate is built by proposal 0065, and the check for
each class lands in the sub-step that seeds that class's registers, per §3.5. Like every gate here
it lands green by seeding today's population into the register rather than by narrowing the predicate.

**Rationale:** Each gate closes the loop on one class of rewrite. Without them the completeness of a
several-thousand-site mechanical change rests on review, which cannot verify it.

**Change (staged description).** This sub-step states each gate's predicate and adds each gate's cases.
The gates themselves land in the sub-step §3.5 assigns them, which is the sub-step that supplies the
route to green: the naming lint in SPEC-1, the identifier-resolution gate and the
`coordinatorHoldAllowedMethods` assertion in SPEC-2, the heading walker, the tier-11 successor-pointer
check, the claim register's schema-only validator, the §28.8 matrix completeness check, and the
artifact-register supersession check in SPEC-3, the fragment-link gate, the
gate-integrity meta-gate, the reference-document freeze check, and the rewritten §25.4 freshness gate in
SPEC-4, and the gates proposal 0065
seeds a baseline for.
Stating the predicate here and landing the gate there keeps one landing point per gate, so no gate
reaches tier 0 before the sub-step that makes it green.

The naming lint enforces the reserved-word ban, and only that ban, across
`spec/`, `docs/`, `schemas/`, the Go doc comments of every tracked Go file, and
the tracked root-level contract documents N3 names, under the same exclusion list and under the same
markdown-anchor-identifier exclusion the name pass reads, so the lint's domain is
exactly what the name pass can write. The
identifier-resolution gate asserts each canonical identifier resolves to exactly one spelling across the
tree under that same exclusion list, so every file the gate reads has a pass that writes it,
reading a retired spelling per context so that an occurrence `tests/registers/identifier-senses.yaml`
records as not a channel, such as the `az` file argument at `spec/17_deployment-topology.md` line 1530,
passes without an entry in the shared exception register. The citation resolver asserts that every remaining line citation either resolves inside its
section or appears in the `tests/registers/line-citation-resolution.yaml` baseline proposal 0065 seeds, and
the line-citation ratchet asserting no file acquires a new one; both match the retired citation form §4.6
states, in every spelling that form covers, which is the section reference given by number or by file
path, the optional qualifier, the optional trailing gloss on a member, the hyphen, en-dash, and em-dash
range separators, and the comma, slash, plus-sign, and
`and` member separators consumed whole, wherever the form appears
in a tracked file, including YAML block scalars, JSON string values, and Go string literals
holding served schema descriptions, and both walk the tree under the exclusion list §4.6 states rather
than an inclusion list of directories. The heading walker asserts that every `## N` and `### N.M`
heading under `spec/`, every deeper heading the index already carries, and the §28.5.1 through §28.5.7
card headings carry both a `tests/spec-map.json` entry, or a `tests/spec-map-exceptions.yaml` entry under
a stated reason class, which for a heading whose implementation is pending is `pending-implementation`
with its `blocker` and `opened_at` fields, and a `spec/README.md` table-of-contents entry whose anchor resolves, so an
appended or reduced section cannot silently miss the hand-maintained index. The predicate is stated at
those depths because the index carries two levels today, with `spec/README.md` line 147 as its only
level-4 row, and the walker lands green through the rows and keys SPEC-1 seeds together with the §4.4,
§4.7, §28, and §29 rows and keys SPEC-3 writes, rather than through a
register, because roughly a hundred exemptions would each need an owner and an expiry this proposal is in
no position to set. The fragment-link gate is a Go test under
`tests/tier0_static/` asserting that every intra-repo markdown fragment link in `spec/` and `docs/`
resolves to a heading slug that exists or to an explicit kramdown anchor attribute (`{: #id }`) that
exists, over links whose target is a tracked `.md` file or the citing page itself; the existing
`scripts/check-markdown-links.sh` cannot serve, because it resolves
the file rather than the fragment and exits 0 when `markdown-link-check` is absent. The predicate reads
the anchor attribute as well as the heading because the `docs/` pages carry 75 of them in
`docs/reference/glossary.md` alone, `docs/api/internal.md` line 229 links to `#lifecycle-channel-messages`
on its own page and resolves only through the attribute at line 318, and SPEC-2's glossary redirect stub
keeps the `{: #lifecycle-channel }` anchor for the same reason. A heading-only predicate would be red on
that link and on every other attribute-resolved fragment, none of which SPEC-4 stages a correction for.
Under the stated predicate the gate is red against the
unmodified tree on the seven links SPEC-4 enumerates and green once SPEC-4 corrects them, so it lands with
those corrections rather than with a baseline. That population holds only because SPEC-3 has already
rewritten the same-page fragments it carried out of `spec/15` and `spec/04` into `spec/28` to their
file-qualified form; a relocated fragment left in the same-page form would be an eighth red link the
gate reports here. The tier-11 check asserts that each reduced section carries
a successor pointer whose named heading resolves.

Add the naming lint's cases in the tier-0 package that hosts it, each carrying a `// spec:` tie to §28.1
N3, because the lint is one of the two gates that prove the naming law landed and its predicate has a
large false-positive and false-negative surface. The tree carries 610 case-insensitive occurrences of the
word `lifecycle`
across 25 files under `spec/` against a banned population of 71 in both spellings, so the lint has to
reject the bare noun
phrase and accept the roughly 540 bound senses, and neither a matcher that is silently a no-op nor one
that is red on permanently-correct prose is distinguishable from a correct one without cases. The cases
are: a bare reserved noun phrase in `spec/` prose fails and names the file and the line; the same phrase in
its hyphenated compound spelling fails as well, with `spec/18_build-sequence.md` line 165 as the worked
case, because that file carries no space-separated occurrence; a bare reserved noun phrase wrapped across
two consecutive comment lines fails, with `schemas/lenny-adapter.proto` lines 1219 and 1220 as the worked
case, so the lint reads the joined population the name pass writes and the wrapped sites are seeded into
`tests/registers/reserved-phrase-senses.yaml` rather than left invisible to both; the same word
inside a canonical identifier, such as `CH-RUNTIMEOPS` or `LNK-GWCONTROL`, passes; an unrelated bound
sense passes, with the cloud-storage prose at `spec/17_deployment-topology.md` line 1512 and the in-fence
command arguments at lines 1490 and 1509 as the worked cases; a bare reserved noun phrase in each of the
other domains N3 names fails, which is a `docs/` page, a `description` string in a `schemas/` JSON
document, a Go doc comment, and `README.md` or `TESTING.md`; an occurrence in a file the N3 exclusion list
names does not fail; a markdown anchor identifier passes in both of the forms N3 places outside the
matcher, which are the kramdown `{: #id }` attribute at `docs/reference/glossary.md` line 207 and the
same-page fragment link at `docs/api/internal.md` line 229, and this case is required rather than
incidental because the anchor identifier carries the hyphenated compound spelling verbatim, so a matcher
built to the compound case above and to no exclusion fails on a site the naming law exempts; a site under
any `testdata/` directory passes, including a fixture that carries a bare reserved phrase in either
spelling in order to exercise a gate that rejects it, because a matcher that reads a fixture reports the
gate's own test data as a defect and puts that file inside the lint's read domain while every pass's write
domain excludes it; the normative statement of N3 in `spec/28_communication-channels.md` passes, because
it describes the two banned spellings rather than quoting them and no exclusion covers the file that
states the rule; and a run that matches zero sites on a tree seeded with a known violation fails
rather than reporting green.

Add the identifier-resolution gate's cases in the same package and with the same `// spec:` tie, because
§5 row 1 rests the completeness of the whole rename on it and its per-context branch is what keeps it from
either failing on a correct occurrence or passing a missed one. The cases are: an identifier that resolves
to one spelling tree-wide passes; an identifier that resolves to two spellings fails and names both files;
an occurrence `tests/registers/identifier-senses.yaml` records as not a channel passes with no entry in
the shared exception register, with the `az` file argument at `spec/17_deployment-topology.md` line 1530
as the worked case; a genuine channel reference left at the retired spelling fails, with the socket token
at `TESTING.md` line 1996 as the worked case; a malformed or missing
`tests/registers/identifier-senses.yaml` fails rather than certifying the tree; and the gate is verified
red against the pre-SPEC-2 tree and green after the pass, so it is known to observe the rename rather than
to be inert.

Add the heading walker's own cases in the tier-0 package that hosts it, each carrying a `// spec:` tie to
§28.1 N8, because the walker is the only gate that observes whether the hand-maintained `spec/README.md`
index gained rows for the two appended files, and both its domain selector and its anchor resolution are
silently satisfiable. Red-on-introduction against the 49 rows the index is missing today does not
substitute, because it shows the walker firing on `### N.M` headings rather than reaching the level-4
§28.5 card headings SPEC-3 adds, which is the population the walker is landed for. The cases are: a
`### N.M` heading with a `tests/spec-map.json` key and a `spec/README.md` row whose anchor resolves
passes; a heading with a key and no README row fails and names the heading; a heading whose README row
carries an anchor that does not resolve fails; a heading with no spec-map key and a well-formed
`pending-implementation` exceptions entry passes, while the same heading with neither a key nor an entry
fails; a level-4 §28.5 card heading with no README row fails, so the domain selector is shown to reach the
card headings; a deeper heading the index does not carry is outside the domain and does not fail; and a
run that inspects zero headings fails rather than reporting green.

Add the gate-integrity meta-gate's own cases in the same package: a gate registered as a
`tests/tier0_static/` Go test or as a `runValidateMaps` check passes; a gate whose file is deleted fails
and names the gate; a gate reached only through a shell script under `scripts/` fails, which is the
condition the meta-gate exists to detect; and the fixed list names exactly the gates this proposal
registers at tier 0, so a name added to the list without a tier-0 registration fails and the three tier-11
checks (the successor-pointer check, the artifact-register supersession check, and the reference-document
freeze check) and the tier-3 no-line-citation assertion are absent from the list rather than
unsatisfiable entries on it. The cases carry no `// spec:` tie, for the same reason.

Add the cases for the claim register's schema-only tier-0 validator over `tests/claim-map.json`, which
SPEC-3 lands alongside the seeded register that supplies its route to green, one case per rule: a `WIRED` row with no
`surface` fails; a `WIRED` row whose `surface` is a bare line number rather than a symbol reference fails;
an `UNWIRED` or `ABSENT` row with no `deferral_id` fails, and one whose `deferral_id` does not name a step
in the plan fails; a duplicate `claim_id` fails; a `spec_anchor` that does not resolve to a `spec/28`
heading fails; a missing or malformed register file fails rather than passing silently; and a well-formed
seeded register passes. Only the schema validator is in scope. The join, which is that every
`WIRED` claim cites a surface the reachability gate reports reachable, needs that gate and lands with it in
a later step.

Add the fragment-link gate's own cases in the `tests/tier0_static/` package that hosts it, each carrying a
`// spec:` tie to §28.1 N8, because §3.4 rests the markdown cross-reference redirect class, the
relocated-same-page-fragment class, and the pre-existing-broken-link class on this gate alone and rests
part of the carve-out class on it, and because the gate is green tree-wide once SPEC-4 corrects the seven
links it enumerates, so a predicate that selects zero links, that drops the same-page form, or that never
reads the kramdown attribute branch is green in the same way a correct one is. The cases are: a
file-qualified link to an existing heading slug passes; a link that resolves only through an explicit
kramdown `{: #id }` attribute passes, with `docs/api/internal.md` line 229 against the attribute at line
318 as the worked case; a link to a slug carried by no heading and no attribute fails and names the file
and the line; a same-page `[...](#anchor)` form whose target heading now lives in another file fails,
which is the class §3.4 rests on the gate; a link to the rendered-site `.html` form and an absolute URL
are outside the domain and do not fail; and a run that inspects zero links fails rather than reporting
green.

Add the tier-11 successor-pointer check's own cases in `tests/tier11_docs/`, each carrying a `// spec:` tie
to §28.1 N8, because SPEC-3 writes the successor pointers and lands the check in the same sub-step, so the
check is green from the moment it exists and no other observation separates a working predicate from one
that selects zero sections. The behavior is normative: N8 makes the pointer permanent law, §4.5 states the
pointer is the only route that serves a reader who arrives by a reference no tool rewrites, and §3.4 names
the check as the mechanical half of the falsified-sentence class's proof. The cases are: a reduced section
carrying a pointer whose named §28 heading exists passes; a reduced section with no pointer fails and names
the section; a pointer naming a heading that exists in no `spec/` file fails and names both the section and
the missing heading; a section that gave up no content is outside the domain and does not fail; and a run
that inspects zero reduced sections fails rather than reporting green, so the check cannot ship inert
against the §4.7 and §15.4 reductions it is landed for.

Add the rewritten §25.4 freshness gate's own cases in the `tests/tier0_static/` package that hosts
`degradation_lock_line_citation_test.go`, each carrying a `// spec:` tie to §25.4 and to §28.1 N8. SPEC-4
replaces the predicate of a running, hard-gated tier-0 test rather than adding a new one, which is the
same condition under which the change-graph completeness check and the widened exceptions validator get
cases in this sub-step. The rewritten predicate is the sole carrier of the freshness property, because the
resolver and the ratchet read whether a citation resolves and whether a count rose rather than whether the
cited section contains the quoted sentence, and it is green from the moment it lands, while the current
implementation fails loudly only through the `t.Fatalf` its regular expression triggers on a non-match
(`tests/tier0_static/degradation_lock_line_citation_test.go` line 38 and lines 104 and 105). A rewrite
whose anchor-form matcher matches nothing, whose §25.4 heading lookup returns an empty
body, or whose declaration table selects zero entries is green with the property gone. The cases are: a
declaration whose anchor-form §25.4 citation names a heading whose body contains that declaration's
`wantSubstring` passes; a declaration whose cited heading exists and whose body does not contain the
`wantSubstring` fails and names the file and the declaration; a declaration carrying no citation fails
rather than being skipped; a citation naming a §25.4 heading that does not resolve fails and names the
heading; a declaration still carrying the retired `§25.4 line N` form fails, so the retirement cannot be
undone; and a run that inspects zero declarations fails rather than reporting green, so the rewritten
predicate cannot ship inert.

**What the gates do not cover.** The naming lint reads spellings and the identifier-resolution gate reads
resolution, so neither detects a sentence that carries a canonical identifier and describes the wrong
mechanism. N1 and N6 are review-time rules for the same reason. What substitutes for a gate here is
`tests/registers/reserved-phrase-senses.yaml`: the name pass fails a reserved-phrase site with no
register entry, so a human resolves the sense of every site before the substitution runs. The sites whose
current text names the wrong participant are corrected by hand in SPEC-1, and the wrong artifact
descriptions together with the artifact-scope sentence at `spec/15_external-api-surface.md` line 1463 are
corrected by hand in SPEC-2. A wrong-mechanism sentence found after those sub-steps is a review finding
rather than a gate failure.

Each gate lands green by enumerating today's violations into a named register, never by widening the gate
and never by suppression. Several gates land green by a different route because the shared exception
register's entry schema does not fit them. The heading walker lands green through the rows and keys SPEC-1
seeds together with the §4.4, §4.7, §28, and §29 rows and keys SPEC-3 writes, for the reason stated above. The line-citation ratchet lands green through its per-file count
baseline. The citation resolver lands green through the
`tests/registers/line-citation-resolution.yaml` baseline proposal 0065 seeds, because a citation that is stale
today is retired by SPEC-4 rather than owned and dated by a person. The change-graph completeness check
lands green through `tests/registers/change-graph-coverage.yaml`. The skip-reason classifier lands green
through `tests/registers/skip-reasons.yaml`, because a host-capability skip is permanently correct and has
neither a blocker nor an expiry. The naming lint and the identifier-resolution gate land green through the
content changes SPEC-1 and SPEC-2 make, which remove every site each one reads. The fragment-link gate
lands green through the seven hand-authored link corrections SPEC-4 enumerates, because a link that points
at a heading that never existed resolves to no open remediation item and so cannot hold a shared-register
entry with a blocker and an expiry.

The deliverables added above are covered by three gates and by cases on two existing gates, each in the
tier `.claude/rules/test-coverage.md` maps its surface to. TEST-1 states each predicate and adds each set
of cases, while the gate itself lands in the sub-step §3.5 assigns it: the §28.8 matrix completeness check
and the artifact-register supersession check in SPEC-3, and the reference-document freeze check in SPEC-4.
**The §28.8 matrix completeness check** is a tier-0 test asserting a bijection between the
channel identifiers in the §28.3 register and the rows of the §28.8 matrix, so an identifier with no
degradation row fails by name and a matrix row naming no registered identifier fails the same way. Its
non-happy cases are an identifier added to the register with no matrix row, a matrix row for a retired
identifier, and a duplicate row for one identifier. **The reference-document freeze check** is a tier-11
test asserting that `gateway-runtime-comms.md` carries the point-in-time header naming the commit and the
superseding sections, and that its body below the header matches the committed text; its non-happy cases
are a missing header, a header naming a different commit, and any body edit. **The artifact-register
supersession check** is a tier-11 test asserting that no enumeration outside §28.7 that stands for the
register's artifact set names a subset of it, which is what stops `schemas/README.md` from
drifting back to a hand-written list. Its read domain is the tracked markdown under `spec/`, `docs/`, and
`schemas/`, together with the tracked root-level markdown documents N3 leaves in scope, which is the
markdown subset of the walk the naming lint reads. The predicate exempts an enumeration on the three
grounds SPEC-3 states, which are that it names the artifacts the enumerating page's own prose documents,
that it names the artifact subset a named consumer asserts against, and that it names what a build phase
delivers. Those grounds cover every enumeration the domain carries at that
sub-step's exit, so the check is green at the sub-step that lands it. Its non-happy cases are a re-added artifact table
in `schemas/README.md`, a further page in the domain adding
an enumeration that stands for the set and falls under none of the three exemptions, and an artifact
present in the register and absent from the schemas directory. Its accept cases include the corrected
`spec/24_lenny-ctl-command-reference.md` line 114 sentence, the corrected "Canonical artifacts" table at
`docs/reference/adapter-contract.md` line 658, and the corrected schema list at
`docs/runtime-author-guide/publishing.md` line 367, each of which names a consumer-scoped subset and so
passes under the second exemption, and the corrected §15.4 wire-artifact pointer at
`spec/15_external-api-surface.md` line 1460 with its four-artifact bullet list, which passes under the
first exemption because it names the artifacts §15.4's own prose documents. **The unread-field claim rows** are covered by the claim register's
existing schema-only validator, whose cases TEST-1 already states, extended with one case per field added
by SPEC-2, whose rows SPEC-3 seeds with the rest of the register: a request-message field with no
`UNWIRED` claim row fails, and an `UNWIRED` row naming no later step fails. The claim rows are the only new coverage those fields need, because the one running test whose
descriptor assertion the additions falsify is `TestReportUsageRequestWireContract`, whose `want` slice
SPEC-2 widens in the same change as the proto edit rather than through a new test. SPEC-2's `buf breaking` disposition adds no register entry and no case, because the check's
verdict is computed from buf's exit status and the current branch name alone and this proposal stages no
edit to it.

The gate-integrity meta-gate is green on introduction in SPEC-4, which
is the sub-step at whose exit every gate on its fixed list is registered at tier 0. The residual gate
lands green through the per-class residual registers, which carry the entry schema §4.7 states, because an
explicit exclusion is a permanent statement about the tree and an in-class entry is either retired by the
event that takes its member out of the class or, in the generated-artifact class where no member leaves,
permanent for the same reason an exclusion is, so no entry carries an expiry or a blocker. Every other gate uses the shared register with an owner and an expiry per entry.

## 7. Non-goals

- **Closing any capability gap.** This proposal builds no channel, wires no consumer, and changes no
  runtime behavior. The reference document's records are closed by later steps, which this proposal
  exists to unblock.
- **Renumbering or moving existing specification sections.** New sections append. The existing sections
  that change are those giving up content, each of which keeps a successor pointer, plus `spec/README.md`,
  which is the hand-maintained index and gains rows for the appended sections and revised rows for the
  reduced ones.
- **A compatibility shim for the renamed manifest key.** The platform is pre-deployment and the repository
  rule forbids compatibility paths. The key rename is breaking for a runtime built against the old
  contract, and the SDKs and author guide move with it.
- **Metric renames.** The two metric names at `pkg/adapter/metrics.go` lines 71 and 79 are deferred to
  R12, the step that first makes the adapter metrics observable. §28.1 states that deferral as part of N4
  and SPEC-3 seeds the claim-register row with status `ABSENT` and `deferral_id` R12.
- **Deleting the three comments describing a kubelet-path handler that does not exist.** They are the only
  in-tree description of that mechanism. They become seed rows in the claim register with status `ABSENT`,
  and the step that owns the mechanism either implements it or removes them.
- **Absorbing `gateway-runtime-comms.md` wholesale into the specification.** The reference is a
  point-in-time analysis with code evidence. The specification carries the contract, and code evidence
  lives in the claim register, whose schema is validated at tier 0 by the validator SPEC-3 lands with the
  seeded register and TEST-1 supplies the cases for. The join
  from a `WIRED` row to a reachable surface lands with the reachability gate in a later step.

## 8. Findings closed on application

This proposal closes no record from `gateway-runtime-comms.md` section 6 by itself, other than the
specification and shipped-artifact halves of the record about wire-contract artifacts being misdescribed.
It also discharges, as a standing mechanism rather than as a one-time fix, the section 8 item recording
that no test tier was run and that every claim is a static reading of the working tree
(`gateway-runtime-comms.md` line 2760, mapped in the plan as item 8.1). The `UNVERIFIED` verdict state and
the tier-0 gates make a conclusion the harness could not reach visible instead of indistinguishable from a
pass. The plan's closure step discharges the item itself with a full-tier run.
Its function is to make the remaining records closable: every later step names a channel, cites a card,
and registers a claim, none of which exist today.

## 9. Resolved in adversarial review

### Pass 1 (2026-07-27, automated)

- **Section 8 attributed a nonexistent item to the reference document.** The closure named "the section 8
  item about the test harness being unable to detect an unreachable surface", which
  `gateway-runtime-comms.md` section 8 does not contain. Section 8 now names the item the reference
  records at line 2760 and the plan maps as 8.1, which is that no test tier was run, and states
  what the `UNVERIFIED` verdict state and the tier-0 gates discharge.

- **The adopted identifiers violated N1, and §3.2 claimed a legibility they do not provide.** N1 required a
  channel name to state the endpoint pair and the plane and to omit the transport, which
  `CH-EVENTSTREAM`, `CH-RUNTIMEOPS`, and `CH-MSGSOCK` all fail. N1 is restated to describe what an
  identifier is, which is a mnemonic for the conversation, with the endpoint pair, the plane, the two
  directions, and the transport carried as register columns, and it is marked as a review-time rule with
  no gate. The §3.2 sentence claiming direction and participants are legible from the identifier alone is
  replaced by a pointer to the register rows. N3 gains the rule that an identifier stem may not reuse a
  term the specification already binds, and C6 is renamed from the plan's `CH-EVENTSTREAM` to
  `CH-ADAPTEREVENTS`, because "event stream" is already bound to the operational event stream in §25.5, to
  the per-session event stream in `spec/07_session-lifecycle.md` line 286, and to
  `pkg/ops/events/service.go`, and N4 would have made it a Go file stem and a metric label value.

- **`spec/README.md` was in no class and no gate.** The hand-maintained table of contents runs to §27.10
  at line 190 and has no generator, so appending `spec/28` and `spec/29` and reducing §4.7 and §15.4 would
  have left it silently wrong. It is now a target of SPEC-1 and SPEC-3 and an entry in §11, it has its own
  row in the §3.4 class table as a hand-authored class, and the heading walker in TEST-1 asserts that an
  in-scope heading carries an index entry whose anchor resolves. Pass 2 corrected this bullet's original
  claim that the index lists every numbered heading and fixed the depth the walker's predicate uses.

- **The SQL and YAML citation dialects were outside the line-citation class.** The `// spec:` scope covered
  Go and proto and left 3,666 citations across 264 files ungated while the §4.7 reduction shifted the
  lines they point at. SPEC-4's target, §4.6, §11, and the §3.4 class row now cover them. Pass 2 replaced
  the dialect-and-directory scoping this bullet introduced with a match on the citation form and a
  tree-wide walk, because the form is also carried in YAML block scalars, JSON values, and Go string
  literals.

- **The anchor-move map could not keep the resolver green across the reductions.** Removing content from
  `spec/04` §4.7 (lines 637-968) shifts the 697 `§4.8`, `§4.9`, and `§4.9.1` line citations below it, and
  removing content from `spec/15` §15.4 (lines 1458-2459) shifts the 39 `§15.5` ones; those sections do
  not move, so `tests/spec-anchor-moves.json` has no entry for them. SPEC-3 now makes the reduction and
  the line pass over the two reduced files one atomic sub-step with the resolver as its exit criterion,
  and the claim that the anchor-move map holds the resolver green is removed.

- **The wire rename reached tier 3 and tier 10 with no test named.** SPEC-2 gains a Tests block naming the
  tier-3 manifest round trip against all three runtime SDKs, the tier-3 assertion that the flag-derived
  listen address matches the advertised socket and that the proto RPC and the JSON Lines schema agree with
  the handler and the §28 register, the tier-0 schema bijection, and the tier-10 Full conformance run over
  the renamed socket, handshake, and check identifier. The second manifest emitter at
  `cmd/lenny-compliance/full.go` line 98 and the fixture at
  `tests/tier4_integration/credential_lifecycle_test.go` line 362 are named as part of the same change.

- **Markdown cross-links into the reduced subsections had no class and no gate.** `spec/` and `docs/`
  carry 43 links into §15.4, 30 of them into subsection anchors the reduction retires as the reduction
  stood at that pass. A class row is added, the
  `specshift` anchor pass is extended to `[...](NN_file.md#anchor)` targets, the links into the retired
  subsections are rewritten in the same change as the reduction, and TEST-1 adds a Go fragment-link gate,
  because `scripts/check-markdown-links.sh` resolves the file rather than the fragment and exits 0 when
  `markdown-link-check` is absent.

- **The glossary kept the conflation, and the rename passes would have made it canonical.**
  `docs/reference/glossary.md` lines 206-209 define "Lifecycle Channel" as the gRPC stream and credit it
  with checkpoint requests and credential rotation, which are `CH-RUNTIMEOPS` frames, and with session
  start/stop and workspace notifications, which are separate `Adapter` RPCs. SPEC-2 now names it as a
  third artifact whose description is wrong and stages the split into a `CH-ADAPTEREVENTS` entry and a
  `CH-RUNTIMEOPS` entry with a redirect stub on the existing anchor. A hand-authored class row is added,
  and TEST-1 states that neither the naming lint nor the identifier gate detects a semantically wrong
  sentence carrying a canonical spelling.

- **The shared register contract shipped with no test.** Every gate lands green by seeding that register,
  so a ratchet rule that is silently a no-op would certify an exempted tree indefinitely. TEST-1 now names
  a case per rule in the style of `cmd/lenny-test/cmd_validate_yaml_test.go` lines 241-400: an
  unregistered violation fails, a passed expiry fails, an unresolvable blocker fails, a malformed or
  missing register fails rather than passing silently, and a well-formed register passes. Pass 2 replaced
  this bullet's reuse of the same five cases for the line-citation ratchet with the ratchet's own count
  cases, because none of the five compares a count.

### Pass 2 (2026-07-27, automated)

- **The heading walker rested on a false premise about `spec/README.md`.** The index carries two heading
  levels, with line 147 as its only level-4 row, so an every-numbered-heading predicate is red on 71
  existing headings today, 49 of them at `### N.M` level, and the `tests/spec-map.json` half is red on 67.
  The register escape does not fit, because each entry needs an owner and an expiry this proposal cannot
  set for the forty §18 build phases. The predicate in §3.4, TEST-1, and SPEC-1 is now stated at the two
  depths the index carries, plus the deeper headings the index already carries and the §28.5.1 through
  §28.5.7 card headings this proposal creates, which SPEC-3 now adds to the index because they are the
  citable handles every later step uses. SPEC-1 seeds the 49 missing rows and the 50 missing spec-map keys
  so the walker lands green. SPEC-3's instruction to revise the §4.7 and §15.4 subsection rows is removed,
  because the index has no subsection row for either and §4.7 has no numbered subsections.

- **The line-citation class was defined by comment dialect and an inclusion list of directories.** The
  `§X.Y line N` form is also carried in JSON string values, YAML block scalars, and Go string literals:
  `pkg/gateway/externalapi/openapi/openapi.json` holds twenty of them in served `description` values,
  including `§15.4 line 1715` at line 461, which is inside the range SPEC-3 shifts and ships to clients at
  `/openapi.json`; `pkg/gateway/mcpfabric/mcptools/mcptools.go` holds them in the served MCP tool schemas;
  twenty-nine of the thirty-four citations under `charts/lenny/crds/` sit in `description:` block scalars;
  and `schemas/`, `tests/**/*.json`, `compose/`, `.github/`, `dist/`, and the repository root sit outside
  the walk list entirely. §4.6, TEST-1, SPEC-3, SPEC-4, and §11 now scope all three passes by the citation
  form and walk the tree under an exclusion list. Served schema descriptions lose the citation rather than
  gaining an anchor, with a tier-3 assertion to that effect. The counts in §1, §4.6, and §11 are corrected
  to the measured 3,666 citations across 264 non-Go files, 1,218 of them across 230 files under the five
  directories the earlier draft named. Pass 3 widened the form this bullet introduced to cover the
  `§X.Y lines N-M` spelling as well.

- **The line pass would have broken two no-drift tests and edited generated output.**
  `pkg/embedded/manifests/manifests.yaml` is a `helm template` render of `charts/lenny` carrying 110
  citations, and `pkg/embedded/crds/` must stay byte-identical to `charts/lenny/crds/`, which carries 34,
  so rewriting the chart side alone fails `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4` and
  `TestEmbeddedCRDsAreCopiesOfChartManifests_spec_10_437`. The chart CRDs are controller-gen output whose
  citations originate in `pkg/apis/lenny/v1alpha1/*.go` doc comments. §3.4 gains a generated-artifact class
  row, §4.6 states the `pkg/embedded/`, `charts/lenny/crds/`, and `pkg/proto/` denylist, and SPEC-4 runs
  `make generate` and the CRD re-copy with those tests and `tests/tier0_static/crds_test.go` as exit
  criteria. Pass 3 corrected this bullet's denylist: it was over-broad on `pkg/embedded/`, wrong about the
  producer of `pkg/proto/`, incomplete against the OpenAPI-derived and alerting-derived artifacts, and it
  treated `charts/lenny/crds/` as pure controller-gen output.

- **Tier-11 tests pin the rewritten prose as Go string literals, in no class and no gate.** Eleven files
  under `tests/tier11_docs/` assert spec sentences verbatim through `specSection`, `requireLine`, and
  `requireAllContain`, and those literals are outside the name pass's scope and the naming lint's scope.
  `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69 pins the exact bare noun
  phrase N3 bans at `spec/04_system-components.md` line 489, and three files scope assertions to
  `"### 4.7 "`, which SPEC-3 empties. §3.4 gains a class row, SPEC-1 and SPEC-3 extend the passes to those
  literals and name tier 11 as an exit criterion, and a §4.7 row relocated into a §28.5 card carries its
  pin with it. Pass 6 widened the register this bullet keyed on the three helper names, because two further
  files pin retired §15.4 anchors through a `specCrossRef` table and a `mustContain` list.

- **SPEC-4's paragraph break destroys an invariant two tier-11 assertions depend on.** The paragraph at
  `spec/04_system-components.md` line 489 is one markdown line, and
  `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` selects it with `requireLine` and then
  requires eight substrings and two inline anchor links inside that one line, so splitting it into five
  paragraphs fails every assertion outside the first block. SPEC-4 now states that the break is a
  hand-authored edit rather than part of the mechanical pass, names that test as moving with it, and adds
  tier 11 to the sub-step's exit criteria. The rationale no longer describes the whole sub-step as
  judgement-free.

- **The wrong-mechanism class was bounded at three artifacts outside `spec/`.** The colliding phrase is
  two-valued inside `spec/` as well: `spec/07_session-lifecycle.md` line 324,
  `spec/15_external-api-surface.md` line 1755, and `spec/05_runtime-registry-and-pool-model.md` line 540
  attribute intra-pod frames or adapter-to-gateway events to the wrong participant, so a substitution makes
  each precise and false. §3.4 row 1 now drives the name pass from a per-occurrence sense column in the
  §28.3 register and fails a site with no entry, §3.2 drops the bound of three, the §5 row and TEST-1 state
  the register as what substitutes for a gate, and SPEC-1 corrects those three sites by hand. Pass 3 moved
  that sense mapping out of normative §28.3 into `tests/registers/reserved-phrase-senses.yaml` and gave the
  name pass's fail-closed rule its own tests.

- **The line-citation ratchet's defining cases had no test.** TEST-1 deferred to the shared register
  contract's five schema cases, none of which compares a count, so neither the rise that the ratchet exists
  to catch nor the downward rewrite of its own baseline was pinned. TEST-1 now names the ratchet's own
  cases: a rise fails and names the file, a fall passes and rewrites the register in the same run, a return
  to the old count fails, a file at zero fails on any citation, and a file absent from the register fails
  on its first.

- **The claim register was asserted to be gated with no validator listed.** TEST-1 now adds a schema-only
  tier-0 validator over `tests/claim-map.json` with a case per rule, §4.4 states that the register carries
  its own schema because a `WIRED` row has no expiry and the shared contract requires one, and §7 says that
  only the schema is validated here while the join from a `WIRED` row to a reachable surface lands with the
  reachability gate in a later step.

### Pass 3 (2026-07-27, automated)

- **`charts/lenny/crds/` was treated as pure controller-gen output.** The committed CRDs carry two
  hand-applied post-generation layers that no Go doc comment and no chart template produces, which are the
  `lenny.dev/schema-version` annotation with its comment block and the top-level spec and status
  `x-kubernetes-preserve-unknown-fields: true` markers. `make generate` overwrites the directory and
  deletes both, and `TestCRDManifestsInSyncWithGoTypes` strips exactly those lines before comparing, so the
  three exit criteria the proposal named went green on a regeneration that dropped the fail-closed CRD
  currency annotation and the pruning guard. §4.6 and SPEC-4 now describe the two layers, make
  re-application part of the regeneration step before the chart-to-embedded re-copy, and add
  `TestEmbeddedCRDsCarrySchemaVersionAnnotation_spec_10_437` and
  `TestEmbeddedCRDsPreserveUnknownFields_spec_10_437` to the exit criteria and to the §3.4 generated-artifact
  row. The five hand-applied citations gain an explicit disposition: SPEC-4 hand-edits the annotation blocks
  to the anchor form together with the matching literal prefixes in `tests/tier0_static/crds_test.go` lines
  178 through 183, which that file's drift normalizer matches verbatim, so the directory can reach count
  zero without turning the drift test red.

- **`pkg/proto/` was excluded on a false premise.** §4.6 attributed its content to `make generate`, which
  does not touch it; `make generate-proto` (`buf generate`) produces it from `schemas/*.proto`. The line
  pass was therefore forbidden from writing 60 citations that no scheduled regeneration would refresh, and
  several point below §4.7 into ranges the SPEC-3 reduction shifts. §4.6 now names the correct producer,
  SPEC-2 runs `make generate-proto` after the proto rename and lists `pkg/proto/` in its Target, SPEC-4
  runs it in the regeneration sequence, and §11 gains a bullet. proposal 0065 adds a tier-0 Go proto no-drift test,
  because `scripts/check-proto-generated.sh` exits 0 both when `buf` is absent and whenever
  `schemas/buf.gen.yaml` has no uncommented `remote:` plugin line, which is the repository's state today.

- **`pkg/embedded/` was excluded wholesale as a generated tree.** Only `pkg/embedded/manifests/` and
  `pkg/embedded/crds/` are derived. `pkg/embedded/localcli/`, `pkg/embedded/stack/`, and
  `pkg/embedded/k3s/` are hand-written Go with no `go:generate` directive and no Makefile target, and they
  carry 132 line citations that nothing would have rewritten and nothing would have regenerated, so the
  ratchet's zero end state was unreachable. §4.6 narrows the exclusion to the two derived subdirectories
  and restates the rule per file rather than per directory, and §11 records that the three packages are
  rewritten by the line pass.

- **The generated-artifact denylist omitted the OpenAPI-derived artifacts.**
  `pkg/gateway/mcpfabric/mcptools/generated_schemas.go` is `genmcpschemas` output carrying seven citations
  inside the served `lenny/create_session` input schema, and its producer is
  `go generate ./pkg/gateway/mcpfabric/mcptools/...` rather than `make generate`, so the line pass would
  have edited generated output and `TestGeneratedSchemasMatchOpenAPI_spec_15_2_1_1386` would have gone red.
  `pkg/ops/mcp/generated_tools.go`, `docs/alerting/rules.yaml`, and `docs/alerting/routing-recommendations.md`
  were in the same position. §4.6 now states the exclusion as a rule covering any file whose header
  declares it generated, lists each artifact with its producer, and SPEC-3 runs the two OpenAPI-derived
  producers inside the sub-step that strips `openapi.json`'s citations, with their drift tests as exit
  criteria. §4.6 and SPEC-3 also correct the claim that all twenty `openapi.json` citations sit in a
  `description`, since one sits in the `summary` at line 2200 that `openapi-to-mcp` copies into the
  generated tool inventory.

- **The line-citation class matched only the singular spelling.** The tree carries 3,606 citations of the
  `§X.Y lines N-M` form across 1,198 files, 294 of them pointing into §4.7, §4.8, §4.9, §15.4, and §15.5,
  which are the sections the SPEC-3 reduction shifts, and the §1 magnitude already counted both spellings.
  A matcher on the singular form alone would have left those 294 silently stale after the reduction and
  would have left the ratchet's prohibition non-flat. §1, the §3.4 class row, §4.6, SPEC-3, SPEC-4, TEST-1,
  and §11 now state both spellings, a range resolves as valid only when both endpoints fall inside the
  cited section, and TEST-1 adds a ratchet case for a new range citation at count zero. The in-tree
  precedent at `tests/tier0_static/degradation_lock_line_citation_test.go` line 38 already matches both.

- **The per-occurrence sense register sat inside normative §28.3.** That is an edit-site list of a
  script-driven class, which §3.4 forbids in the applied change; it is stale the moment SPEC-1's own pass
  removes the phrases it indexes; and its granularity, one entry per occurrence site, does not fit the
  one-row-per-entry register schema §4.3 states, since roughly sixty occurrences across eleven files map to
  roughly twenty-two channel rows. It moves to `tests/registers/reserved-phrase-senses.yaml` alongside the
  other migration registers, and SPEC-1 empties it once the name pass has run, which is the retirement
  SPEC-4 applies to `tests/spec-anchor-moves.json`. §3.4 rows 1 and 9, §5, SPEC-1, and TEST-1 now name that
  register, and §28.3 keeps the per-channel rows.

- **SPEC-2's tier-3 round trip required the retired manifest key to be rejected.**
  `spec/04_system-components.md` line 818 makes silent ignoring of unknown top-level manifest fields
  normative for runtimes, and `sdks/runtime/go/runtime/types.go` lines 113 through 116 document that
  behavior, so a rejection assertion could pass only by changing all three SDKs, which contradicts the
  normative rule and the proposal's own statement that it changes no runtime behavior. The assertion is
  restated as what the rename guarantees: a manifest carrying only the retired key resolves no operations
  channel in any of the three SDKs, and a manifest carrying the renamed key resolves it in all three.

- **The name pass's fail-on-unregistered-site rule had no test.** That rule is what substitutes for a gate
  on the wrong-mechanism class, and a silent default substitution would pass the naming lint and the
  identifier-resolution gate while turning each ambiguous sentence into a precise false one. TEST-1 applies
  the standard it already applies to the other three validators and names the pass's own cases: an
  unregistered site aborts non-zero and leaves the tree unmodified, a resolved site is substituted, an entry
  naming an undeclared channel fails, a malformed or missing register fails rather than passing with zero
  substitutions, and the dry-run output equals the applied diff.

### Pass 4 (2026-07-27, automated)

- **`charts/lenny/values.schema.json` was classified as an authored carrier the line pass rewrites.** The
  file is generator output byte-pinned to `pkg/chart/values`: its seven citations are copied verbatim from
  the `desc:` struct tags in `pkg/chart/values/values.go`, and `TestSchemaIsCommitted_spec_17_6_655` in
  `pkg/chart/values/schema_test.go` requires the committed file to byte-match a fresh `Generate()`.
  Directing a pass to write it contradicts §3.4's rule that a derived artifact is regenerated rather than
  edited, and §4.6's header-comment detection rule could not catch it, because the file is JSON whose only
  generation notice sits in the top-level `description` value at line 5. The file moves from §4.6's carrier
  sentence into §4.6's generated-artifact list, §4.6's detection rule is widened to cover a generation
  notice carried as a document field, §4.6 gains a producer bullet naming `go run ./cmd/lenny-chart-schema-gen`
  and its authoring source, and SPEC-4 adds that producer to its regeneration sequence and
  `TestSchemaIsCommitted_spec_17_6_655` to its exit criteria and to the §3.4 generated-artifact row. The
  producer is named explicitly because `Makefile` carries no target for it. The seven citations are
  stripped from the `desc:` tags rather than converted to anchors, under the rule SPEC-3 already applies to
  served client artifacts, and the spec tie is kept in the Go doc comment above each field, which carries
  the same citation and which the line pass converts to the anchor form. Pass 6 corrected that last claim,
  which holds for four of the seven fields and not for the three that have no doc comment.

- **The citation resolver was red on introduction with no baseline.** The tree already carries on the order
  of 1,500 line citations across roughly 500 files that do not resolve inside the section they name, for
  example `pkg/adapter/workspace/materialize.go` line 203 citing `§7.4 line 433` when §7.4 begins at
  `spec/07_session-lifecycle.md` line 437, and `pkg/gateway/externalapi/admin/erasure.go` line 356 citing
  `§12.8 line 764` when §12.8 begins at `spec/12_storage-architecture.md` line 774. proposal 0065 could not
  produce a green gate, and the shared exception register does not fit, because a stale citation is retired
  by SPEC-4 rather than owned and dated, which is the argument TEST-1 already makes for the heading walker.
  SPEC-3's exit criterion was also unusable, since a resolver failing on a thousand pre-existing citations
  cannot distinguish one the reduction broke. §4.6 records the measurement and states the baseline
  register `tests/registers/line-citation-resolution.yaml`, proposal 0065 seeds it, TEST-1 states the gate as
  failing a citation that neither resolves nor appears in the baseline, SPEC-3's criterion becomes no new
  resolver failure relative to the baseline, and SPEC-4 empties it when every per-file count reaches zero.
  TEST-1's claim that every gate lands green through the shared register is corrected to name the three
  gates that land green through their own baselines.

- **SPEC-3 declared the resolver its exit criterion without regenerating `pkg/proto/`.** The sub-step's
  line pass rewrites `schemas/*.proto`, whose comments the committed stubs mirror and which no pass writes,
  and several mirrored citations point into the ranges the reduction shifts:
  `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 178, 189, 194, and 1343 cite `§4.7` lines 632, 631,
  660, and 942, `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 3287 cites `§4.7 line 822`, and
  `pkg/proto/tokenservice/v1/lenny-tokenservice.pb.go` line 622 cites `§4.9 lines 1246-1298`. Because §4.6
  makes a generated file readable by the resolver, the resolver was red exactly where the sub-step declared
  it green. SPEC-3 now runs `make generate-proto` after the line pass, takes the tier-0 proto no-drift test
  as a further exit criterion, and lists `pkg/proto/` in its Target, which is the treatment it already gave
  the two OpenAPI-derived producers.

- **The identifier pass had no way to resolve the two-valued `LifecycleChannel` spelling.** Both renamed
  channels carry that exact Go token inside package `adapter`: `pkg/adapter/lifecyclechannel.go` line 92
  declares `type LifecycleChannel struct` for `CH-RUNTIMEOPS` and `pkg/adapter/controlchannel.go` line 90
  declares the `LifecycleChannel` stream handler for `CH-ADAPTEREVENTS`. A register keyed by channel, which
  is the §4.3 one-row-per-entry schema, cannot say which occurrence maps to which identifier, and the
  identifier-resolution gate reads the forward relation only, so it does not observe one spelling resolved
  to the wrong identifier. The failure is silent on the gRPC full-method literal at
  `pkg/adapter/holdstate.go` line 57, which sits in the coordinator-hold allowlist and whose parallel
  literal at `pkg/adapter/holdstate_test.go` line 292 the pass rewrites identically, so a mis-resolution
  would reject a new coordinator's control-stream open during hold state with the test still green. §3.4
  row 2 and SPEC-2 now drive the identifier pass from a per-occurrence register,
  `tests/registers/identifier-senses.yaml`, for any retired spelling the §28.3 table maps to more than one
  channel, with the pass failing a site that has no entry rather than substituting a default, which mirrors
  the name pass's fail-closed rule. Pass 6 replaced that trigger with an occurrence-scoped one, because a
  single-channel spelling also occurs at sites that are not the channel. Every gRPC full-method literal is resolved from the proto RPC row
  rather than from the Go type row, and TEST-1 adds the identifier pass's own cases and a tier-0 assertion
  that every method string in `coordinatorHoldAllowedMethods` names a method the generated `adapterv1`
  service descriptor declares.

### Pass 5 (2026-07-27, automated)

- **The tier-0 proto no-drift gate diffed raw `buf generate` output against post-processed stubs.**
  `make generate-proto` runs `buf generate` and then applies two steps the plugins do not perform: it
  prepends `// SPDX-License-Identifier: MIT` to every file and runs
  `goimports -w -local github.com/lennylabs/lenny ./pkg/proto`, which regroups the import block
  (`Makefile` lines 91 through 100; `schemas/buf.gen.yaml` lines 6 and 7 record the same fact). A diff against
  raw `buf generate` output reports drift on every generated file with none present, confirmed by
  generating into a temporary directory and diffing all six committed stubs, so the gate could not go green
  and could not serve as the exit criterion SPEC-2, SPEC-3, and SPEC-4 each declare it to be. §4.6 and
  TOOL-1 now state the gate against the whole `make generate-proto` producer, with the same SPDX prepend
  and `goimports` normalization applied before the comparison, and TOOL-1 verifies it green against the
  unmodified tree before any sub-step takes it as a criterion.

- **The `coordinatorHoldAllowedMethods` assertion was red on introduction.** Two of the five entries are
  not `adapterv1` methods: `pkg/adapter/holdstate.go` lines 58 and 59 carry `/grpc.health.v1.Health/Check`
  and `/grpc.health.v1.Health/Watch`, which come from the standard health service
  `pkg/adapter/transport.go` registers from `google.golang.org/grpc/health/grpc_health_v1` and which have
  no descriptor under `pkg/proto/`. Requiring every entry to name a method the `adapterv1` descriptor
  declares would fail at tier 0 against two permanently correct entries, and neither TEST-1's shared
  register, which needs an owner and an expiry per entry, nor a widened gate is available under the
  proposal's own rules. §3.4 row 2, SPEC-2, and TEST-1 now state the predicate per service part: an entry
  whose service part is `lenny.adapter.v1.Adapter` names a method or stream `Adapter_ServiceDesc` declares,
  and an entry whose service part is another service names a method of a service the adapter registers. A
  mis-resolved rename preserves the `/lenny.adapter.v1.Adapter/` prefix, so the failure the assertion
  exists to catch stays inside the predicate.

- **The citation matcher required a subsection component, leaving the section-level spelling ungated.** The
  tree carries 556 occurrences of `§X line N` and `§X lines N-M` across 148 files, in the same carriers the
  dotted spelling uses. Three of them cite `§15 line 2141`
  (`pkg/gateway/sessionserver/expiry_warning.go` lines 32 and 50 and
  `pkg/gateway/runtime/adapterclient/client.go` line 484), which sits inside the §15.4 block SPEC-3 reduces,
  so the reduction would have broken them while SPEC-3's exit criterion stayed satisfied. Thirty-one of
  them are the `§10 line 437` sites SPEC-4 depends on rewriting, whose hand-edit paragraph rested on a
  premise its own matcher contradicted. The remainder would have stayed free to regrow, so the ratchet's
  flat-prohibition end state was unreachable. §3.4 row 5, §4.6, SPEC-3, SPEC-4, TEST-1, and §11 now state
  one form with the section component optional, which is `§X(.Y)* line N` and `§X(.Y)* lines N-M`; a
  section-level citation resolves against the whole of the section it names; both baselines are measured
  under the widened predicate; and TEST-1 gains a ratchet case for a new section-level citation at count
  zero. The chart CRD annotation count is corrected from five citations to ten across five blocks.

- **The `UNVERIFIED` verdict state changed the exit-code contract with no test listed.** The state adds a
  fourth value to the enum CI gates on, and its three siblings are each pinned in
  `cmd/lenny-test/verdict_test.go` (lines 140, 149, and 164). `recordTier`'s switch has no default branch
  (`cmd/lenny-test/verdict.go` lines 245 through 254) and the verdict starts at `verdictPASS` (line 106),
  so a status the switch does not handle leaves the run at `PASS` with exit code 0
  (`cmd/lenny-test/cmd_run.go` lines 457 through 466), which is the fail-open outcome §8 claims the state
  prevents. TEST-1 now names its cases: an unverified tier sets the overall verdict, `FAIL` outranks
  `UNVERIFIED` and `UNVERIFIED` outranks `PASS` in either recording order, `exitCodeFor("UNVERIFIED")`
  returns a non-zero code distinct from the `INCONCLUSIVE` code, and an unhandled status does not leave the
  run at `PASS`. proposal 0065's bullet points at those cases.

- **The line pass had no test case, and both gates named for it are green on a destructive run.** The
  ratchet counts citations and the resolver validates only citations that still exist, so a pass that
  deletes a citation instead of converting it yields count zero, no resolver failure, and a better gate
  result than a correct conversion, while discarding the anchor and breaking the rule that spec-derived
  logic carries a spec citation (`.claude/rules/code-best-practices.md` line 57). §3.4 rules out review as
  the control at this scale. TEST-1 now names the line pass's own cases in `scripts/specshift`'s
  `run_test.go`: a Go-comment citation is converted with the citation retained, a section-level citation
  converts to the anchor for the section it names, a range straddling two sections fails rather than
  converting, a served client artifact is stripped while every other carrier is converted with a case per
  dialect, a run that reduces a count without emitting an anchor fails, and the dry-run output equals the
  applied diff.

### Pass 6 (2026-07-27, automated)

- **The `desc:`-tag citation strip rested on a doc-comment fallback three of the seven fields do not
  have.** §4.6 stated that the spec tie survives the strip in the Go doc comment above each field, which
  holds for `Cluster`, `IsolationProfile`, `MaintenanceMode`, and `NoEnvironmentPolicy` and is false for
  `SpiffeTrustDomain`, `TraceSamplingRate`, and `SaTokenAudience` at `pkg/chart/values/values.go` lines 136
  through 138, whose `desc:` tags are today the only carrier of `§10.3 line 316`, `§16.3 line 359`, and
  `§10.3 line 334`. The type-level comment on `Global` carries none of the three. Stripping as written
  would delete a spec tie the standing rule requires (`.claude/rules/code-best-practices.md` line 57)
  rather than relocate it, and it would leave the ratchet and the resolver green on the deletion because
  both read a removed citation as a retirement. §4.6 now names the four fields whose doc comment already
  carries the tie, states that a tag is stripped only where one does, and adds an anchor-form `// spec:`
  doc comment above the three fields that have none in the same edit. TEST-1 gains a line-pass case in
  which a stripped served-artifact citation whose authoring source keeps no surviving tie fails the pass.

- **Two tier-11 files pin retired §15.4 anchors and fall outside the class register.**
  `tests/tier11_docs/embedded_mode_anchors_test.go` line 43 requires the heading slug
  `1543-runtime-integration-levels` and `tests/tier11_docs/embedded_echo_placement_test.go` line 38
  requires `1544-sample-echo-runtime`, both asserted against `headingSlugs()` over the live spec file, and
  the second file's line 116 requires `spec/17_deployment-topology.md` to contain the verbatim link
  `[Section 15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)`, which the anchor pass rewrites
  at `spec/17_deployment-topology.md` lines 181, 291, and 353. Neither file uses `specSection`,
  `requireLine`, or `requireAllContain`, so the row-7 register keyed on those three helper names reached
  neither, and tier 11 was unreachable as an exit criterion of SPEC-3 and SPEC-4. The row-7 register is
  widened to every Go string literal under `tests/tier11_docs/` naming a spec heading slug, an intra-spec
  markdown link, or pinned spec prose; SPEC-1 records the widening; and SPEC-3 names both files as targets
  so their anchor rows and the link literal move to their §28.5 successors in the same change as the
  reduction. Pass 10 narrowed the §15.4 reduction so that §15.4.3 and §15.4.4 keep their headings and
  anchors, which leaves both files green without an edit and removes them from SPEC-3's Target.

- **The identifier register's fail-closed trigger was channel-count-scoped, so a single-channel token was
  substituted blind at a non-channel site.** `@lenny-lifecycle` maps to `CH-RUNTIMEOPS` alone, so it earned
  no `tests/registers/identifier-senses.yaml` entry and no abort, while
  `spec/17_deployment-topology.md` line 1530 uses `@lenny-lifecycle.json` as an `az` read-from-file
  argument naming the local blob-lifecycle policy the code fence at lines 1510 through 1529 produces, with
  the AWS and GCP siblings at lines 1490 and 1509 still reading `lenny-lifecycle.json`. Blind substitution
  would rename a storage lifecycle-policy file after a runtime-operations channel and name a file the
  section never produces, and no gate reads meaning. §3.4 row 2 and SPEC-2 now state the register
  occurrence-scoped, carrying an entry for every occurrence whose site the pass cannot prove is the
  channel, with that line recorded as a not-a-channel site the pass leaves unmodified. The
  identifier-resolution gate reads a retired spelling per context, the same predicate SPEC-2 already gives
  the `coordinatorHoldAllowedMethods` literals, so a permanently correct non-channel occurrence is not
  routed through an exception register whose expiry it can never meet. TEST-1 gains the matching case.

- **The line-citation ratchet and the resolver baseline are keyed per file path, and SPEC-2 renames files
  that still carry citations.** SPEC-2 moves `pkg/adapter/lifecyclechannel.go` (nine citations) and
  `pkg/adapter/controlchannel.go` (five), with `pkg/adapter/lifecyclechannel_test.go` (five) and
  `pkg/adapter/controlchannel_test.go` (four) alongside, and the line pass that retires those citations
  does not run until SPEC-3 and SPEC-4. The new paths are absent from
  `tests/registers/line-citations.yaml`, so the ratchet's own rule fails them on their first citation at
  SPEC-2's exit, and their entries in `tests/registers/line-citation-resolution.yaml` detach, so every
  baselined stale citation reappears as a new resolver failure. §4.6 and SPEC-2 now state that the
  identifier pass rewrites the register keys of any file it moves in the same run, SPEC-2 lists both
  registers in its Target, and TEST-1 gains a ratchet case in which a renamed file with an unchanged count
  passes and its key moves with it.

- **The reserved-phrase sense register admitted only channel identifiers, while several sites denote a
  link.** `spec/13_security-model.md` lines 79, 92, and 100 describe the default-deny egress allowance and
  `docs/getting-started/architecture.md` line 506 is the documentation case. A channel-only value space
  either aborts the pass at every such site, leaving
  SPEC-1 unable to complete, or narrows a security-normative sentence to one of the conversations the link
  carries. §3.4 row 1 and SPEC-1 now state the value space as the whole §28 identifier space of links,
  channels, and registers, TEST-1's validator case is restated against a declared §28 identifier rather
  than a declared §28.3 channel, and TEST-1 gains a name-pass case covering a site whose correct sense is
  a link.

- **The anchor pass, one of four rewrite passes, had no test case.** The other three each carry their own
  cases in `scripts/specshift`'s `run_test.go` because their class gates cannot separate a correct run
  from a silent no-op or a destructive one, and the anchor pass is in the same position: the row-3 gate
  validates map entries rather than tree rewrites, the row-4 fragment-link gate reads markdown links only
  and passes a link redirected to a wrong existing heading, and a bare `§15.4.1`-style anchor citation is
  matched by neither the resolver nor the ratchet. SPEC-4 then empties `tests/spec-anchor-moves.json`, so
  a pass that resolved nothing destroys its own record with every gate green. TEST-1 now names the anchor
  pass's cases, and SPEC-4 states run completeness against the tree, which is zero remaining citations and
  links for each retired anchor, as the entry criterion for emptying the map.

### Pass 7 (2026-07-27, automated)

- **SPEC-1 assigned the gateway-to-pod link identifier to the pod-egress sites.** Two distinct gRPC
  connections default to port 50051. `LNK-POD-GRPC` is the gateway-to-pod `Adapter` service on
  `adapter.grpcPort`, which the gateway dials at the pod IP and which the ingress site at
  `spec/13_security-model.md` line 72 names, and the `pkg/adapter/holdstate.go` allowlist SPEC-1 cited is
  entirely `lenny.adapter.v1.Adapter` methods, so it supports that reading rather than the one the text
  drew. `LNK-GWCONTROL` is the pod-to-gateway `GatewayControl` service on `gateway.grpcPort`
  (`schemas/lenny-adapter.proto` lines 230 through 247). Every site SPEC-1 prescribed the identifier for
  sits inside `allow-pod-egress-base` (`spec/13_security-model.md` lines 79, 92, and 100) or restates it in
  documentation (`docs/getting-started/architecture.md` line 506), so each is the pod-originated link.
  Applying the paragraph as written would have put the gateway-to-pod identifier into security-normative
  default-deny egress prose, left the link the allowance permits unnamed, and put the applied §13.2 out of
  agreement with the §28.2 link register, with no gate able to detect it. SPEC-1 now names both links and
  their ports, resolves those four sites to `LNK-GWCONTROL`, states that a port-50051 site is resolved from
  the policy direction rather than from the port, and TEST-1's worked name-pass case is restated against
  the `allow-pod-egress-base` sites.

- **The §16 mTLS handshake metric spans two connections, so no single identifier substitutes.**
  `spec/16_observability.md` line 51 states that the two `direction` label values are instrumented because
  each side initiates handshakes in distinct paths, which are the gateway-originated adapter gRPC dial and
  the pod-originated LLM-proxy connection. The LLM proxy runs on port 8443, which
  `spec/13_security-model.md` line 79 excludes from the base allowance and which the naming table records
  as `CH-LLMPROXY`. Collapsing the site onto one link identifier would have made a normative metric
  definition assert that the `pod_to_gateway` histogram measures the gRPC control link. SPEC-1 now records
  the site as denoting `LNK-POD-GRPC` for `gateway_to_pod` and `CH-LLMPROXY` for `pod_to_gateway`, states
  that a sense-register entry carries one or more identifiers, and TEST-1 gains a name-pass case for a site
  whose entry carries more than one identifier so the pass cannot collapse it.

- **The comma-separated citation spelling fell outside the stated matcher.** The tree carries 617
  occurrences across 341 files of one `§X(.Y)* line(s)` prefix followed by two or more comma-separated line
  numbers or ranges, 86 of them across 57 files naming §4.7, §4.8, §4.9, §15.4, or §15.4.x, which are the
  ranges SPEC-3 shifts. A matcher stopping at the first comma converts the head and leaves live line
  numbers behind, where the resolver does not read them and the ratchet does not count them, so a file
  reaches count zero with a stale pointer surviving. The proposal's own worked example,
  `cmd/lenny-gateway/adminrouter.go` line 205 (`§4.8 lines 1057-1058, 1077`), is an instance. §3.4 row 5,
  §4.6, SPEC-3, SPEC-4, TEST-1, and §11 now state the form as `§X(.Y)* line(s) L` with `L` a
  comma-separated list of line numbers and ranges, a comma-list citation resolves only when every member
  resolves, and TEST-1 gains a line-pass case converting a comma list to a single anchor with no residue
  and a ratchet case for a comma-list citation at count zero.

- **The straddling-range rule had no remedy, so SPEC-4's zero end state was unreachable.** The line pass is
  specified to fail a range whose endpoints straddle two sections, and the tree carries seventeen such
  citations across fifteen files, measured by computing each section's range from the `##` through `######`
  headings under `spec/`. Every other blocker to count zero has a disposition and this population had none,
  so those files could not reach zero and the ratchet's flat prohibition could not be reached. §4.6 now
  enumerates the population with the section each citation should name, SPEC-3's blast-radius example list
  separates the comma-list case from the plain range case, SPEC-4 stages the hand correction before the
  final line-pass run and lists the population in its Target, and SPEC-4's exit criteria add that the pass
  reports no remaining straddling range.

- **§5 rows 7 and 8 and three TEST-1 test ties named a §28.1 rule no staged edit wrote.** §4.1 stated N1
  through N7, every one a naming rule about identifiers, and SPEC-1 scoped §28.1 the same way, so the
  anchor-citation convention announced as a decision was normative nowhere in `spec/` and three families of
  new tier-0 tests would have carried `// spec:` annotations naming a rule the section does not state. §4.1
  gains N8, which states that a citation names a heading, retires the `§X(.Y)* line(s) L` form, and
  requires a permanent successor pointer from a section that gives up content. SPEC-1's Change sentence and
  `.claude/rules/channel-naming.md` now carry N1 through N8, and the §5 rows and the three TEST-1 tie
  clauses name N8.

- **The widened spec-map-exceptions validator had no test, and it is the heading walker's escape hatch.**
  `validateSpecMapExceptionsYAML` hard-codes its reason set (`cmd/lenny-test/cmd_validate_yaml.go` lines 185
  through 193) and its three-field entry struct (lines 168 through 175) and runs inside the `validate-maps`
  tier-0 check (`cmd/lenny-test/cmd_run.go` lines 734 and 742), so proposal 0065's addition changes both its accept
  and its reject predicate while its four existing behaviors are each pinned in tree. An entry passing with
  an empty field would exempt a heading from the walker permanently, which is the suppression outcome the
  proposal forbids. TOOL-1 now names the class `pending-implementation` and its `blocker` and `opened_at`
  fields, taken from the shared register contract's vocabulary, SPEC-1 names both fields where it seeds the
  entry, and TEST-1 adds a case-per-rule battery beside the four existing cases in
  `cmd/lenny-test/cmd_validate_yaml_test.go`.

### Pass 8 (2026-07-27, automated)

- **The slash and `and` member separators fell outside the stated matcher.** The tree carries 50
  occurrences across 42 files whose members are separated by a slash and 2 more separated by the word
  `and`, with the continuation member repeating the word `line` and carrying no `§` prefix, for example
  `§10.7 line 694 / line 743` at `pkg/experiment/experiment.go` line 184 and
  `pkg/controller/poolscaling/variants.go` line 98, `§25.3 line 441 / lines 527-528` at
  `cmd/lenny-gateway/httpsurface.go` line 144, and `§10 line 437 / line 443` at
  `cmd/lenny-controller/setup.go` line 110 and `pkg/embedded/crds/crds_test.go` line 90, which is the same
  section-level family SPEC-4 hand-edits in the chart CRD annotation blocks. A comma list also occurs with
  the keyword repeated (`§10.6 line 601, line 629` at `pkg/gateway/mcpfabric/mcptools/mcptools.go` lines
  1445 and 1503), which the bare-number member grammar did not admit. Every consequence §4.6 states for the
  comma list applies unchanged: the pass converts the head, the resolver never reads the trailing number,
  the ratchet counts the file at zero, and SPEC-4's zero exit criterion is met with a stale pointer alive.
  §4.6 now states the member separator as a comma, a slash, or `and`, admits a repeated `line` keyword, and
  records the population; §3.4 row 5, SPEC-4's Target, TEST-1, and §11 carry the same predicate; and TEST-1
  gains a line-pass case and a ratchet case for the spelling.

- **The qualified spelling fell outside the stated matcher.** A short qualifier between the section
  reference and the word `line` (`item 3`, `rule 4`, `table`, `preamble`, `step 2`, or `NET-063`) put 136
  occurrences across 68 files, measured over `pkg`, `cmd`, `tests`, `sdks`, `spec`, `docs`, `schemas`,
  `charts`, and `migrations`, in no class, no pass, and no gate, among them `§11.7 item 3 line 364` at
  `pkg/audit/jcs/jcs.go` lines 15 and 39, `§15.2.1 rule 4 line 1386` inside the served MCP tool schema at
  `pkg/gateway/mcpfabric/mcptools/generated_schemas.go` lines 9 and 16, and `§7.2 table line 124` in the
  shipped client SDK at `sdks/client/go/lenny/client.go` lines 332, 345, 363, and 380. N8 as written
  prohibited one spelling rather than the line number, so the population was free to regrow after
  retirement and a file could reach count zero with live line citations. N8 is restated as a prohibition on
  citing a line number in any spelling, §4.6 admits an optional qualifier and carries it through the
  conversion, and TEST-1 gains a line-pass case and a ratchet case for it.

- **The en-dash range spelling fell outside the stated matcher.** 65 occurrences across 38 files separate
  a range's endpoints with U+2013 rather than an ASCII hyphen, including `§4.4 lines 263–291` at
  `pkg/gateway/storage/evictionfallback/evictionfallback.go` lines 3, 31, and 377 and `§4.8 lines
  1025–1028` at `pkg/gateway/policy/policy/authevaluator.go` line 47, which points into a section the
  SPEC-3 reduction shifts. The in-tree precedent §4.6 pointed the implementor at,
  `tests/tier0_static/degradation_lock_line_citation_test.go` line 38, compiles
  `§25\.4 lines? (\d+)(?:-(\d+))?`, which pins one section, accepts an ASCII hyphen alone, and stops at the
  first member. §4.6 now states the range separator as a character class covering the ASCII hyphen, the en
  dash, and the em dash, states that the precedent is the starting point and must be widened on all three
  counts, and records the population; SPEC-3's blast radius names the en-dash case; and TEST-1 gains a
  line-pass case and a ratchet case.

- **The path-form spelling was in no class, and 39 of its occurrences sit in the SPEC-3 blast radius.**
  123 occurrences across 59 files name the specification file rather than the section number
  (`spec/04_system-components.md line 1145` at `pkg/credential/lease.go` line 150,
  `spec/04_system-components.md lines 870-888` at `pkg/adapter/metrics.go` line 12,
  `11_security-trust-model.md line 414` at `pkg/audit/ocsf/catalog.go` line 149), so they carry no `§` and
  the stated matcher did not reach them. 39 point at or below `spec/04_system-components.md` line 637 or
  `spec/15_external-api-surface.md` line 1458, which are the ranges the reduction shifts, so SPEC-3's exit
  criterion was satisfiable while those citations pointed at the wrong lines. §4.6's citation form now
  admits the path-form section reference with the `spec/` prefix optional, SPEC-3's blast radius counts the
  39, SPEC-4's Target and §11 carry the population, and TEST-1 gains a line-pass case and a ratchet case.

- **The `UNVERIFIED` verdict state left `TESTING.md` stale and tied its tests to the wrong §7.**
  `TESTING.md` line 521 states that the verdict is one of `PASS`, `FAIL`, and `INCONCLUSIVE`, and line 2572
  states the retry-then-`FAIL` path for the one non-`PASS`, non-`FAIL` value, and both become false when
  the harness emits a fourth value. `TESTING.md` sits outside every pass's scope and appeared in no target
  list, so nothing staged the edit. TOOL-1 now names `TESTING.md` §7 and §21.3 in its Target and states the
  two sentences, §3.4 gains a hand-authored class row for the class, and §11 records the file. Separately,
  TEST-1's instruction to tie the new cases to "the §7 verdict schema section" would have put
  `// spec: §7` on verdict-aggregation tests, and a bare `§7` resolves to `spec/07_session-lifecycle.md`
  under the repository's convention, so the harness would have counted them as Session Lifecycle coverage.
  The tie is dropped, matching the three sibling cases in `cmd/lenny-test/verdict_test.go` at lines 140,
  149, and 164, which carry none, because verdict aggregation is test infrastructure.

- **The wire rename invalidates a hard-gated key in `tests/change-graph.json` that no class covered.** The
  naming table renames `schemas/lifecycle-events.schema.json` to `schemas/runtime-ops-events.schema.json`,
  and `tests/change-graph.json` line 495 carries the old path as a glob key.
  `validateChangeGraphFileExistence` stats every glob key and fails when one does not resolve
  (`cmd/lenny-test/cmd_validate.go` lines 294 through 305) inside the `validate-maps` tier-0 check, which
  hard-fails the tier (`cmd/lenny-test/cmd_run.go` lines 734 and 742), so SPEC-2 would have exited with
  tier 0 red and no remedy staged; `tests/change-graph-pending.txt` covers paths committed ahead of
  implementation rather than paths that moved. `tests/spec-map.json` carries the same path in its
  `schemas` arrays at lines 438, 2360, and 2376, which nothing existence-checks. §4.6 and SPEC-2 now state
  the re-keying rule over every path-keyed test-infrastructure register the move invalidates rather than
  over the two line-citation registers alone, name both files, and add them to SPEC-2's Target and to §11,
  and TEST-1 gains a case asserting that a file the identifier pass renames leaves `validate-maps` green.

- **Change-graph completeness had no predicate, no register, no landing-green route, and no test.** The
  bullet was one sentence, and the item is a new accept-and-reject predicate on `runValidateMaps`, which
  the proposal itself identifies as one of the two channels the repository hard-gates. Its two siblings in
  the same bullet list each state their target and carry a TEST-1 battery, and the existing change-graph
  checks carry no unit case in tree, so there was nothing to fall back on. TOOL-1 now states the predicate
  against the reverse-direction check that runs today (`cmd/lenny-test/cmd_validate.go` lines 272 through
  316), names its target, and states that it lands green through the existing
  `tests/change-graph-pending.txt` channel extended with the shared contract's `blocker` and `opened_at`
  fields, and TEST-1 adds its cases, including the deny path, the expired-pending path, the fail-closed
  path on a malformed or missing register, and the mapped-tree pass.

- **§3.4 row 2 stated the coordinator-hold assertion over every gRPC full-method literal in the tree.**
  SPEC-2 and TEST-1 state it over the five entries of `coordinatorHoldAllowedMethods`, while the class
  table ranged it over every literal, whose second branch is red on the unmodified tree against permanently
  correct literals: `cmd/lenny-token-service/spiffe_test.go` line 54 and
  `pkg/proto/tokenservice/v1/lenny-tokenservice_grpc.pb.go` lines 40 through 43 carry TokenService
  literals and `pkg/proto/interceptor/v1/lenny-interceptor_grpc.pb.go` line 39 carries a
  RequestInterceptor literal, and the adapter registers only the `Adapter` service and the standard health
  service (`pkg/adapter/transport.go` lines 50 and 54). Neither the shared exception register nor a widened
  gate is available under the proposal's own rules, so the row as written described a gate that cannot go
  green. Row 2's "Proven by" cell now states the same domain SPEC-2 and TEST-1 use, which is the
  `coordinatorHoldAllowedMethods` entries in `pkg/adapter/holdstate.go`.

### Pass 9 (2026-07-27, automated)

- **The tracked root-level contract documents fell into no class, so the wire rename left the conformance
  battery pointing at a socket no adapter opens.** `TESTING.md` line 1996 states the runtime-author SDK
  Full-level battery as "connect to `@lenny-lifecycle`, capability handshake, checkpoint flow, interrupt
  flow, credential rotation, deadline notification", which is the battery SPEC-2's tier-10 bullet re-runs
  over the renamed socket, and the file also carries the reserved bare phrase at lines 788, 874, 1315,
  1527, and 1972 while `README.md` line 155 carries it in the integration-level table. The proposal
  declared `TESTING.md` outside every pass's scope and staged only its two verdict sentences, and
  `README.md` appeared in no target list, so no pass and no hand edit reached either occurrence, and the
  tree-wide identifier-resolution gate would have seen `CH-RUNTIMEOPS` resolving to two spellings with no
  writer available. The occurrence is a genuine channel reference, so the not-a-channel register entry was
  unavailable and the shared exception register requires an owner and an expiry it could never retire
  against. N3 and the naming lint now scope to the tracked root-level contract documents alongside `spec/`,
  `docs/`, `schemas/`, and Go doc comments, with `BUILD-GAPS.md`, `TEST-GAPS.md`, and the two root planning
  documents excluded as historical audit records in the way `proposals/` already is. §3.4 gains a class row
  for those documents naming the two registers, the two passes, and the two gates; the row for the
  verdict-enum prose now scopes "outside every pass's scope" to the two sentences rather than to the whole
  file, and proposal 0065's sentence matches. SPEC-2 names both files in its Target, states the battery site and
  the phrase sites, and §11 records both with their reasons. The identifier-resolution gate's domain is
  stated as the same exclusion list the passes walk, so every file the gate reads has a pass that can write
  it.

- **The change-graph completeness gate was red on introduction against a population its stated landing
  route cannot hold.** `tests/change-graph.json` carries 142 glob keys, and measured over `git ls-files` on
  the order of 750 of the 1,378 tracked non-test `.go` files under `pkg/` and `cmd/`, across roughly 340 of
  their 498 package directories, match no key, including shipped packages such as `pkg/adapter`,
  `pkg/preflight`, and `pkg/gateway/mcpfabric/mcptools`. `tests/change-graph-pending.txt` holds 25 entries
  today, matches by exact string equality on a change-graph glob key
  (`cmd/lenny-test/cmd_validate.go` lines 292 through 295 and 637 through 654), and covers the inverse
  population of paths committed ahead of their implementation, and the shared contract's third ratchet rule
  fails an entry whose `blocker` names no open item, which a package absent from the graph has none of.
  Because `validate-maps` hard-fails tier 0, TOOL-1 would have ended with tier 0 red under every later
  sub-step's exit criteria. TOOL-1 now gives the check its own seeded baseline,
  `tests/registers/change-graph-coverage.yaml`, keyed by path prefix and rewritten downward only, in the
  way §4.6 already grants one to the citation resolver and the ratchet, states the measured population and
  that the check is red on introduction, and states why neither the pending file nor the shared register
  can carry it. TEST-1's cases are restated against the baseline, including the fail-closed case on a
  malformed or missing baseline and the case that coverage cannot be given back, and the sentence naming
  the gates that land green by their own baselines now includes this one.

- **The `+` member separator fell outside the citation matcher, so the line pass would have left live line
  numbers behind.** Under the stated grammar the pass consumed the head member and left the continuation
  in place, carrying no `§` and no path form, so the resolver never read it and the ratchet never counted
  it, and a file reached count zero with a stale pointer alive. That is the failure §4.6 already states for
  the comma, slash, and `and` spellings, so SPEC-4's exit criterion and N8's flat prohibition were both
  satisfiable with these pointers live. The population is 11 files outside `proposals/`, the two root
  planning documents, and `BUILD-GAPS.md`, and it includes the spec tie of a fail-closed control:
  `pkg/preflight/crdschema.go` lines 22 through 25 carry `§10 line 437 ("...") + line 443 ("...")` on the
  annotation the `CRDSchemaVersionCheck` upgrade gate compares, which is the same `§10 line 437` family
  SPEC-4 depends on rewriting. A second un-admitted feature travels with it, which is a trailing gloss on a
  member such as `line 408 step (e)`, `line 240 messagingScope`, `line 1779 audit event`, and a quoted
  fragment. §4.6 now admits the plus sign as a member separator and the trailing gloss as part of its
  member, records the measured population and every site, and the same predicate is carried into §3.4
  row 5, SPEC-4's Target, §11, and TEST-1, which gains a line-pass case converting
  `§10 line 437 ("...") + line 443 ("...")` to a single anchor with no orphan integer and a ratchet case
  failing a file at count zero on a new `+`-separated citation.

### Pass 10 (2026-07-27, automated)

- **§4.6 attributed 2,168 root-level citations to the reference and plan documents.** The two root planning
  documents carry seven citations between them; the 2,168 sit in the historical audit records
  `BUILD-GAPS.md` and `TEST-GAPS.md`, which the §4.6 breakdown named nowhere. Because the sentence named
  the two files N3 excludes, the largest non-Go carrier read as already-excluded population, while the
  measurement paragraphs at three other points in §4.6 excluded only `proposals/` and the two planning
  documents, so the passes walked `BUILD-GAPS.md` and SPEC-4's zero exit criterion required script-rewriting
  a historical audit record the proposal declares out of scope. §4.6 now attributes the figure correctly and
  states one exclusion list, which is `proposals/`, `BUILD-GAPS.md`, `TEST-GAPS.md`, the two root planning
  documents, and the per-file generated-artifact rule, shared by the resolver, the ratchet, and the line
  pass. Every measurement paragraph in §4.6, SPEC-4's Target, SPEC-4's zero exit criterion, and §11 are
  restated against that one list.

- **The naming lint's domain was wider than any staged rewrite, so it could not land green.** N3 and the
  lint cover `spec/`, `docs/`, `schemas/`, Go doc comments, and the two tracked root-level contract
  documents, while SPEC-1's name pass ran over `spec/` and `docs/` alone. The tree carries the banned bare
  phrase at 100 Go doc-comment sites across 45 files under `pkg/`, `cmd/`, and `sdks/` and at 10 sites
  across 4 files under `schemas/`, of which SPEC-2 hand-corrected two. Both branches failed: a pass that
  walked them could not complete, because it is fail-closed on an unregistered site and the register was
  seeded only for `spec/`; a pass that did not walk them left tier 0 red with no writer available, and
  neither the §5 escape nor the shared exception register was reachable, since no sub-step retires those
  sites. SPEC-1's name pass now walks the whole N3 domain in one run under the generated-file exclusion,
  `tests/registers/reserved-phrase-senses.yaml` is sized to that population (65 in `spec/`, 55 in `docs/`,
  10 in `schemas/`, 100 in Go doc comments, and 6 in the two root documents), SPEC-1's Target and §11 carry
  the domain, the §5 row states that the lint's domain equals what the pass writes, and TEST-1 gains
  name-pass cases for a Go doc comment and a `schemas/` JSON `description` value.

- **The §15.4 reduction retired four runtime-author subsections into §28.5 cards that cannot hold them.**
  Only §15.4.1 and §15.4.2 carry channel prose. §15.4.3 Runtime Integration Levels, §15.4.4 Sample Echo
  Runtime, §15.4.5 Runtime Author Roadmap, and §15.4.6 Conformance Test Suite state the runtime-author
  contract, and §28.5 holds channel contract cards with no destination stated for any of the four,
  while `spec/05` line 40, `spec/04` line 796, `spec/26` line 10, `spec/17` line 291, the three runtime
  SDKs, and the `"15.4.6"` string `cmd/lenny-compliance/full.go` line 40 serves all depend on them. §3.1 and
  SPEC-3 now scope the reduction to the §15.4 preamble and the §15.4.1 and §15.4.2 subsections and state
  that the remaining four keep their headings, anchors, and content. The `tests/spec-anchor-moves.json`
  entries for `1543-runtime-integration-levels` and `1544-sample-echo-runtime` are dropped, so
  `tests/tier11_docs/embedded_mode_anchors_test.go` and
  `tests/tier11_docs/embedded_echo_placement_test.go` stay green without an edit and leave SPEC-3's Target
  and §11 accordingly, and SPEC-4's link count is corrected to the 37 links into the two retired anchors.

- **The `spec/03` correction treated a normative mTLS requirement as a false statement.** mTLS on the
  gateway-to-pod hop is required by `spec/10` line 190, NET-060 at `spec/10` line 321, `spec/15` line 1456,
  and `spec/04` line 641, none of which the proposal edits, and by four documentation pages it stages no
  edit for. The missing certificate material in the podspec is an implementation gap, and under
  `.claude/rules/spec-driven-development.md` the code is the defect where code and spec disagree. Editing
  `spec/03` alone would have left the applied specification denying a transport property four other
  sections require, with no gate able to detect it. §1 and SPEC-1 now scope the `spec/03` edit to the
  collapsed protocol line and the §28 pointer, keep the mTLS assertion, record the podspec gap as a
  claim-register row with status `ABSENT`, and name the edits a retraction would require instead.

- **SPEC-1 emptied the reserved-phrase sense register before SPEC-2's fail-closed name pass needed it.**
  SPEC-2 stated that the name pass rewrites `README.md` line 155 and `TESTING.md` lines 788, 874, 1315,
  1527, and 1972 from a register SPEC-1 had already emptied, and the pass aborts non-zero on an
  unregistered site, so the sub-step could not complete; the alternative reading is the silent default the
  proposal rules out. With SPEC-1's pass now covering the whole N3 domain, those six sites are written in
  SPEC-1, and the register's emptying carries an entry criterion of run completeness measured against the
  tree, which is zero remaining occurrences of each reserved phrase across the whole domain, the same
  criterion SPEC-4 uses before emptying `tests/spec-anchor-moves.json`. The rationale asserting that the
  pass removes every phrase the register indexes is restated so it holds. SPEC-2 now states that it writes
  the retired identifier spelling at `TESTING.md` line 1996 and reads its own register.

- **The AST skip-reason classifier had no target, no predicate, no landing-green route, and no cases.** Its
  three siblings in the same TOOL-1 list each state all three. The measured population is 201 `t.Skipf`
  sites under `tests/`, `pkg/`, and `cmd/` whose reason matches no §17.9 category, almost all of them
  permanently-correct host-capability skips such as `tests/testinfra/kind/kind.go` line 104, so a
  category-requiring classifier is red at tier 0 on introduction with no route to green, and a
  category-tolerant one is the silent no-op it exists to replace. TOOL-1 now states the target, the
  predicate against the categories `cmd/lenny-test/cmd_validate.go` lines 853 through 865 enumerate, the
  accepted `SkipUnless*` and bare-`t.Skip()` forms, the fail-closed behavior on an unparseable file, and a
  seeded `tests/registers/skip-reasons.yaml` baseline rewritten downward only, in the way §4.6 already
  grants one to the resolver and the ratchet. The gate-integrity meta-gate gains the same treatment.
  TEST-1 adds a case battery for each, and the sentence naming the gates that land green by their own
  baselines now covers both.

- **SPEC-4's line-citation retirement turns an existing hard tier-0 gate red.**
  `TestSpec254DegradationWarningLineCitationsAreFresh` compiles `§25\.4 lines? (\d+)(?:-(\d+))?`
  (`tests/tier0_static/degradation_lock_line_citation_test.go` line 38) and calls `t.Fatalf` when it does
  not match (lines 104 and 105), so its predicate is the presence of the retired form above
  `pkg/ops/opsidem/writers.go` line 53 and `pkg/ops/coordination/service.go` line 153. The proposal named
  the file only as matcher precedent, and SPEC-4's stated exit criteria are all satisfiable with tier 0
  red. §4.6 now records that the file is a running gate, SPEC-4 names it as a hand-authored target and
  states the rewritten predicate, which reads the anchor-form citation and requires the named §25.4
  heading's body to carry the declaration's `wantSubstring`, tier 0 over `pkg/ops/` becomes an exit
  criterion, and §11 gains the entry.

- **The citation resolver was a baselined fail-closed gate with no listed cases.** Its two baselined
  siblings each carry their own, and the resolver is the exit criterion SPEC-3 rests its atomicity on while
  landing green through a baseline of roughly 1,500 pre-existing failures, so a baseline load that
  degrades to exempting the tree reports no failure for exactly the citations the reduction breaks. TEST-1
  now names the resolver's cases: a resolving citation passes; a non-resolving citation absent from the
  baseline fails and names file, line, and citation text; a baselined citation passes only under the file
  and citation text it was baselined for; a baseline entry whose citation no longer exists is removed in
  the same run; a range or multi-member citation fails unless every member resolves; a citation broken by a
  heading move fails rather than being absorbed; and a malformed or missing baseline fails.

### Pass 11 (2026-07-27, automated)

- **The fragment-link gate was red on introduction with no route to green.** Six intra-repo markdown
  fragment links in `spec/` and `docs/` already point at headings that do not exist, and no sub-step staged
  an edit for any of them: `spec/09_mcp-integration.md` line 56 targets
  `08_recursive-delegation.md#85-platform-tool-inventory` while `spec/08_recursive-delegation.md` line 525
  is `### 8.5 Delegation Tools`; two runbooks target
  `spec/17_deployment-topology.md#175-cloud-deployment-shapes` while line 382 is `### 17.5 Cloud
  Portability`; `docs/runbooks/ops-lock-split-brain.md` targets
  `#254-remediation-locks-and-escalations` and `#104-runtime-extensibility` while
  `spec/25_agent-operability.md` line 783 is ``## 25.4 The `lenny-ops` Service`` and
  `spec/10_gateway-internals.md` line 376 is `### 10.4 Gateway Reliability`; and
  `docs/runbooks/otlp-plaintext-egress-detected.md` targets `#132-network-policy` while
  `spec/13_security-model.md` line 32 is `### 13.2 Network Isolation`. A link pointing at a heading that
  never existed resolves to no open remediation item, so the shared exception register's `blocker` rule
  cannot hold it. SPEC-4 now names the six as a bounded hand-authored correction in the change that lands
  the gate, with a table giving each link's current target, the heading that exists, and the corrected
  target; the gate's domain is stated as a link whose target is a tracked `.md` file or the citing page;
  §3.4 gains the class row; the landing-green paragraph and §11 record the route.

- **The naming lint, the heading walker, and the identifier-resolution gate were staged twice, and the
  tooling placement landed them red.** §3.5 item 1 and TOOL-1 built all three, while TEST-1 added them
  again, and all three are red against the unmodified tree with their routes to green in later sub-steps.
  Each gate now has one landing sub-step, and it is the sub-step that supplies its route to green: the
  naming lint in SPEC-1, the identifier-resolution gate and the `coordinatorHoldAllowedMethods` assertion
  in SPEC-2, the heading walker and the tier-11 successor-pointer check in SPEC-3, the fragment-link gate
  in SPEC-4, and the gates proposal 0065 seeds a baseline for in TOOL-1. §3.5 gains a fifth item for the gate
  cases and states the one-landing-point rule, proposal 0065 states which gates it builds and why the other three
  land elsewhere, each sub-step's Target records the gate it lands, and TEST-1 states each gate's predicate
  and adds its cases rather than adding the gate.

- **§15.3's normative sentence naming §15.4 as the prose reference was in no edit list and becomes false.**
  `spec/15_external-api-surface.md` line 1456 states that §15.4 and its subsections are the normative prose
  reference for gateway-to-pod communication, and it sits above the §15.4 heading, so the reduction does not
  cover it. It carries no line citation, no anchor that moves, and no bare reserved phrase, so no pass
  reaches it. SPEC-3 now names it in its Target and stages a hand-authored restatement pointing at §28 for
  the channel contract and §15.4 for the wire artifacts, §3.4 gains a hand-authored class row for a
  normative sentence a reduction falsifies, and §11 records the file.

- **The name pass's Go doc-comment domain stopped at `pkg/`, `cmd/`, and `sdks/` while the lint read every
  Go doc comment.** 23 occurrences of the banned bare phrase across 9 files sit in Go doc comments under
  `tests/`, among them `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 8,
  `tests/tier3_contract/sdks/runtime_sdk_test.go` line 338, and
  `tests/tier4_integration/credential_lifecycle_test.go` line 11, and `tests/` was in neither the exclusion
  list nor the write domain. N3, SPEC-1's Target, SPEC-1's domain paragraph, TEST-1's lint sentence, and
  §11 now state one domain, which is the Go doc comments of every tracked Go file under the single
  exclusion list, and the seeded population is re-measured to 124 occurrences across 55 files, of which 23
  across 9 are under `tests/`. `migrations/` carries none.

- **The root-level markdown scope was stated two incompatible ways and three tracked files fell between
  them.** `BUILD-PLAN.md` line 259 carries the retired `LifecycleChannel` spelling, and
  `BUILD-PROGRESS.md` line 30 and `PROPOSAL-QUEUE.md` lines 289 and 625 carry the reserved bare phrase,
  while §3.4 admitted all tracked root-level markdown to the passes' walk and N3 scoped it to two files. N3
  now names `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md` as excluded build and queue
  records alongside `BUILD-GAPS.md` and `TEST-GAPS.md`, states that the name pass, the identifier pass, the
  naming lint, and the identifier-resolution gate all read that one list, and SPEC-2 records the three
  files and their occurrences. §4.6 states why the citation list stays narrower, which covers one citation
  at `BUILD-PROGRESS.md` line 34. The reserved-phrase population in the two contract documents is
  re-measured from 6 to 10, which is `TESTING.md` lines 788, 858, 874, 993, 1315, 1527, 1972, 1996, and
  2248 and `README.md` line 155, and SPEC-2 and §11 carry the corrected figure.

- **The two gates that prove the naming law landed carried no test cases.** Every other new tier-0
  predicate in TEST-1 has an accept, reject, and boundary battery, on the stated ground that a rule that is
  silently a no-op certifies an exempted tree indefinitely, while the naming lint and the
  identifier-resolution gate had only their assertion sentence, and §5 rows 1 and 6 rest the completeness
  of the rename and the naming law on them. TEST-1 now adds a battery for each, tied to §28.1 N3: for the
  lint, a bare phrase in each domain N3 names fails, the same word inside a canonical identifier passes, a
  bound sense passes with `spec/17_deployment-topology.md` lines 1490, 1509, and 1512 as worked cases, an
  excluded file does not fail, and a zero-match run on a seeded tree fails; for the gate, one spelling
  passes, two spellings fail and name both files, a not-a-channel occurrence passes with no shared-register
  entry with `spec/17_deployment-topology.md` line 1530 as the worked case, a genuine channel reference
  left at the retired spelling fails with `TESTING.md` line 1996 as the worked case, a malformed or missing
  `tests/registers/identifier-senses.yaml` fails, and the gate is verified red before SPEC-2 and green
  after it.

### Pass 12 (2026-07-28, automated)

- **SPEC-4 offered `spec/04` line 967 as a worked example of a link into a retired §15.4 anchor, and it is
  a link into a surviving one.** That line's only markdown link targets
  `#1543-runtime-integration-levels`, which is §15.4.3 and which SPEC-3 preserves, and
  `spec/04_system-components.md` carries no occurrence of either retired anchor, so the example directed an
  implementor to redirect a correct client-facing cross-reference into a §28.5 card that owns neither the
  nonce wire format nor the runtime-integration-level contract. SPEC-4 now names
  `spec/17_deployment-topology.md` line 361 as the worked example, states that `spec/04_system-components.md`
  carries no link into either retired anchor and that its line 967 link is confirmed untouched, and the
  Pass 1 record no longer carries the example.

- **`schemas/ocsf-mapping.yaml` was a generated carrier of the retired citation form with no producer, no
  regeneration step, and no drift-test exit criterion.** Its header declares it generated, so the per-file
  rule in §4.6 bars every pass from writing it, while its authoring source is the `mappingHeader` const at
  `pkg/audit/ocsf/catalog.go` lines 147 through 150, which the line pass does rewrite. Without a
  regeneration step `TestMappingYAMLInSync` in `pkg/audit/ocsf/catalog_test.go` goes red and the committed
  file stays above count zero, which makes SPEC-4's zero exit criterion unmeetable. §4.6 now lists the
  file and its producer `go run ./cmd/lenny-ocsf-mapping-gen`, noting as for `cmd/lenny-chart-schema-gen`
  that no `make` target reaches it; SPEC-4's regeneration sequence runs that producer and takes
  `TestMappingYAMLInSync` as an exit criterion; the §3.4 generated-artifact row names both; and §11 records
  the file.

- **The gate-integrity meta-gate landed in TOOL-1 while ranging over six gates that SPEC-1 through SPEC-4
  register.** A fixed list checked against the tier-0 registration fails on a not-yet-written gate in
  exactly the way it fails on a deleted one, so the meta-gate was red at proposal 0065's exit with its route to
  green four sub-steps away. It now lands in SPEC-4, which is the first sub-step at whose exit every gate
  on its list is registered, and §3.5, TOOL-1, SPEC-4, and TEST-1 all state that placement.

- **The meta-gate's two accepted registration channels excluded tier 11 and tier 3, while its list named
  gates that land there.** The tier-11 successor-pointer check and the tier-3 assertion that the served
  OpenAPI document and the generated MCP tool schemas carry no citation can satisfy neither the
  `tests/tier0_static/` channel nor the `runValidateMaps` channel, so the list held two names that could
  never pass. The meta-gate's domain is now stated as the gates this proposal registers at tier 0, the two
  gates outside it are named with the tier-11 and tier-3 suites as their registration channel, and TEST-1
  adds the case asserting the fixed list and the accepted-channel set name the same population.

- **§11's `TESTING.md` entry enumerated five reserved-phrase lines where the file carries nine.** Lines
  858, 993, and 2248 were absent, and lines 788, 874, 1315, 1527, 1972, 1996, and 2248 are the rest, which
  is the population SPEC-2 already enumerates and the count §11 already states in aggregate. An
  implementor seeding `tests/registers/reserved-phrase-senses.yaml` from §11 would have left three sites
  unregistered, and the name pass is fail-closed on an unregistered site. §11 now lists all nine lines.

- **The §15.4 reduction retired four unnumbered subsections whose anchors were excluded from the
  retired-anchor set.** Between `#### 15.4.1` at line 1470 and `#### 15.4.2` at line 2068 sit the internal
  `MessagePart` format, the Translation Fidelity Matrix, the `MessageEnvelope` unified message format, and
  the Protocol Reference message schemas with their eight `#####` children, and three live intra-repo
  fragment links target two of them at `spec/15_external-api-surface.md` line 1399 and
  `spec/21_planned-post-v1.md` line 31. SPEC-3 now states the reduction's scope over the whole §15.4.1
  block and lists those headings, SPEC-4's retired-anchor sentence covers their anchors as
  `tests/spec-anchor-moves.json` entries, the anchor pass rewrites the three links, and SPEC-4 states that
  links the reduction retires are redirected earlier in the same sub-step so the fragment-link gate's
  red-on-introduction population stays the six enumerated ones.

- **The tier-0 proto no-drift test had no listed cases, including the tool-absent path it exists to
  eliminate.** It is the sole `pkg/proto/` exit criterion SPEC-2, SPEC-3, and SPEC-4 each declare, its
  producer needs external binaries (`Makefile` lines 91 through 100), and the skip-reason classifier
  accepts a bare `t.Skip()`, so a test that returns early when a binary is missing reproduces the
  fail-open behavior that disqualifies `scripts/check-proto-generated.sh`. TEST-1 now names its cases:
  matching stubs pass, a hand-edited stub fails and names the file, an unavailable producer binary
  records the tier as `UNVERIFIED` rather than passing, and a run producing zero generated files fails.

- **The fragment-link gate was red on a seventh pre-existing link.** `docs/api/internal.md` line 229 links
  to `#lifecycle-channel-messages` on its own page, and that fragment resolves only through the kramdown
  anchor attribute at line 318, which a heading-only predicate does not read. SPEC-2's glossary redirect
  stub relies on the same mechanism, and `docs/reference/glossary.md` carries 75 anchor attributes. The
  gate's predicate now resolves a fragment against heading slugs and explicit kramdown anchor attributes,
  under which the gate is red on exactly the links SPEC-4 enumerates, and §3.4's row states the same
  predicate.

### Pass 13 (2026-07-28, automated)

- **The path-form citation staged for the OCSF mapping named a specification file that does not exist, so
  the line pass had no anchor to convert it to and no stated disposition.** `11_security-trust-model.md` is
  not a tracked file, and the stated resolution rule computes a citation's anchor from the file it names, so
  the seven occurrences at `pkg/audit/ocsf/catalog.go` line 149, `pkg/audit/ocsf/catalog_test.go` lines 26,
  44, 75, and 113, `cmd/lenny-ocsf-mapping-gen/main.go` line 10, and the generated
  `schemas/ocsf-mapping.yaml` line 3 held four files above count zero with no route down. §4.6 now gives the
  case the straddling range's disposition, which is that the pass fails and reports it, and enumerates the
  population; SPEC-4 hand-corrects the six authored sites to the `### 11.7 Audit Logging` heading at
  `spec/11_policy-and-controls.md` line 341, which contains both line 414 and line 365, and regenerates
  `schemas/ocsf-mapping.yaml`; and TEST-1 carries the line-pass case.

- **The anchor pass was specified over file-qualified markdown links alone, while the majority of links into
  the retired §15.4 anchors are same-page bare fragments.** `spec/15_external-api-surface.md` carries 25
  same-page links into the two retired numbered anchors against 11 file-qualified ones, and every link SPEC-3
  and SPEC-4 name by line is same-page, so the stated pattern would have left them pointing at deleted
  anchors and the fragment-link gate red on a population no pass writes. §3.4 row 4 and SPEC-4 now state the
  pass's markdown domain as the fragment-link gate's domain, which is every link whose target is a tracked
  `.md` file or the citing page itself, and TEST-1 adds the same-page accept and reject cases.

- **§4.6 put generated artifacts in the one exclusion list shared by the resolver, the ratchet, and the line
  pass, while three later arguments required the resolver and the ratchet to read and count exactly those
  files.** §4.6 now states two levels: the read exclusion the resolver and the ratchet share is `proposals/`,
  the two historical audit records, and the two root planning documents, and the write exclusion the passes
  read is those four groups plus every generated artifact. A generated artifact therefore carries a per-file
  count whose route to zero is the regeneration of its source, which is what the SPEC-3 atomicity argument,
  the OCSF regeneration argument, and SPEC-4's chart-CRD hand edit each rest on, and every measurement,
  baseline, Target, and §11 figure is stated against the read domain.

- **The claim register's schema-only tier-0 validator landed in TEST-1, after the gate-integrity meta-gate
  that must name it had already landed green in SPEC-4.** Its route to green is the seeded register, so it
  now lands in SPEC-3 alongside `tests/claim-map.json`, per §3.5's rule that a gate lands in the sub-step
  that supplies its route to green. §3.5, SPEC-3, SPEC-4's meta-gate list, and TEST-1's gate-landing
  enumeration all state that placement, and §4.4 and the §7 non-goal no longer credit TEST-1 with landing it.

- **A seventh pre-existing broken fragment link left the gate red at SPEC-4's exit.**
  `docs/runbooks/admission-plane-feature-flag-downgrade.md` line 151 writes `](#)`, whose empty path targets
  the citing page and whose empty fragment matches no heading slug and no anchor attribute, so it sits inside
  the gate's domain and outside the `.html` exclusion. SPEC-4's correction table now carries a seventh row
  pointing it at `### Step 5 — Post-incident drift-snapshot refresh` at line 149, and SPEC-4, §3.4 row 13,
  TEST-1, the landing-green paragraph, and §11 all state seven.

- **TEST-1 gave the naming lint the one-identifier rule as part of its predicate, which SPEC-1 cannot make
  green.** SPEC-1 changes no identifier, and the retired spellings are still live in `spec/` and `schemas/`
  at its exit, so an N4 clause would have left the lint red at its landing sub-step. TEST-1 now states the
  lint's predicate as the reserved-word ban and only that ban, matching §3.4 row 1, §3.5, §5, SPEC-1, and the
  lint's own case list. N4 stays with the identifier-resolution gate, which lands in SPEC-2.

- **N7 mandated a kebab or snake manifest key, which the §4.7 manifest field set cannot take.** Every sibling
  key in the adapter manifest is camelCase and the three runtime SDKs type it that way, so a kebab or snake
  key would put the naming law and the §4.7 field reference in disagreement on a surface third-party runtimes
  parse. N7 now names the form each carrier already fixes, which is lowercase kebab for a flag, upper snake
  for an environment variable, and camelCase for a manifest key, and SPEC-2 records `runtimeOps` and
  `--runtime-ops-socket` as the concrete targets rather than leaving them to be read off the plan's table.

- **SPEC-3's tier-3 assertion over the served client artifacts was red at its own landing sub-step.** The
  strip that sub-step performs is scoped to the line form, while `pkg/gateway/externalapi/openapi/openapi.json`
  carries 75 section-symbol citations of which 21 name a line, so 47 lines naming a section survive it and the
  two derived artifacts inherit them. The assertion is now stated over the retired line form, with the reason
  recorded, so it has a route to green in the sub-step that lands it.

- **The skip-reason classifier's non-literal-reason branch had no listed case, and it is the branch that
  reproduces the shell script's silent accept.** TOOL-1 now seeds that branch's population into
  `tests/registers/skip-reasons.yaml`, which is ten `t.Skip` calls across five files that pass a non-literal
  first argument and none of which is a `SkipUnless*` helper, and TEST-1 adds the case asserting such a call
  is reported and named with its file and line, with `tests/testinfra/kind/install.go` line 56 as the worked
  case.

- **SPEC-2 and §11 enumerated the retired identifier spellings in the two root contract documents as the
  single socket token at `TESTING.md` line 1996, and line 1521 carries three more.** That line names the
  schema path `schemas/lifecycle-events.schema.json`, the example-fixture glob
  `schemas/examples/lifecycle.*.json`, and `TestLifecycleEventExamplesValidate`, all of which this sub-step
  renames, and the identifier pass aborts on a site with no register entry, so an implementor seeding from
  the stated population would have stopped the run. SPEC-2 and §11 now state both lines.

- **The heading walker's domain includes `24.19.1`, which has no `tests/spec-map.json` key and no exception,
  and no sub-step seeded one.** `spec/README.md` line 147 is the index's only level-4 row and
  `tests/spec-map.json` carries `24.19` and no `24.19.1`, so the walker's spec-map half was red at SPEC-3 on
  a heading SPEC-1's `### N.M` seeding instruction did not reach. SPEC-1 now states the seeding over the
  walker's whole domain and names `24.19.1` explicitly, and §11 records it.

### Pass 14 (2026-07-28, automated)

- **The renamed test and compliance-check functions left two existence-checked `::<symbol>` references in
  `tests/spec-map.json` dangling, which hard-fails `validate-maps` at tier 0.** `tests/spec-map.json` lines
  2187 and 2370 name `tests/tier0_static/schemas_test.go::TestLifecycleEventExamplesValidate` (declared at
  `tests/tier0_static/schemas_test.go` line 146) and lines 2188 and 2385 name
  `cmd/lenny-compliance/full.go::checkLifecycleHandshake` (declared at `cmd/lenny-compliance/full.go` line
  225), both of which N4 renames. `validateSpecMapTestFuncs` requires a top-level `func <Name>(`
  declaration for each (`cmd/lenny-test/cmd_validate.go` lines 564 and 602 through 617, registered at line
  53) inside the hard-gating `validate-maps` check (`cmd/lenny-test/cmd_run.go` lines 734 and 747 through
  750), so the claim that the spec-map entries are not existence-checked was true of the `schemas` paths
  alone. §4.6 and SPEC-2 now extend the re-key rule from path keys to any `::<symbol>` reference naming a
  symbol the pass renames, SPEC-2 names the four lines, and §11 records the register.

- **The reserved-phrase population was measured in the space-separated spelling alone, leaving
  `spec/18_build-sequence.md` outside the pass, the register, and the touched-file list.** That file
  carries the hyphenated compound at lines 164, 165, and 408 and carries no space-separated occurrence, so
  after SPEC-1 it would still have read "adding a lifecycle-channel client" while §28 named
  `CH-RUNTIMEOPS`, and the stated exit criterion would have been false. Measured across the tree the
  compound adds 6 occurrences in `spec/`, 6 in `docs/`, 30 in Go doc comments across 19 tracked Go files,
  and 2 in `TESTING.md`, and it brings in five Go files that carry no space-separated occurrence. N3 now
  states both spellings, SPEC-1 and §11 record the augmented population and the `spec/` count of 71 across
  12 files, and TEST-1 adds the naming lint's hyphenated-spelling case with
  `spec/18_build-sequence.md` line 165 as the worked case.

- **TEST-1 stated the heading walker's predicate and added none of its cases.** The walker is the only
  gate that observes whether the hand-maintained `spec/README.md` index gained rows for the two appended
  files, and neither its domain selector nor its anchor resolution is observable from red-on-introduction
  against the 49 rows the index misses today, because those are `### N.M` rows rather than the level-4
  §28.5 card headings SPEC-3 adds. TEST-1 now adds the walker's accept, reject, and boundary cases,
  including the card-heading case and a run that inspects zero headings.

- **The one case listed for the identifier pass's register re-keying could not observe two of the four
  registers.** It was stated as a `validate-maps` outcome, and `validate-maps` existence-checks the
  change-graph glob keys, the `spec_file` pointer, and the `::<symbol>` references while deliberately
  leaving the `schemas` paths unchecked (`cmd/lenny-test/cmd_validate.go` lines 236 through 238), so the
  case was green whether or not the pass rewrote the `tests/spec-map.json` `schemas` entries, and the
  citation resolver carried no rename case at all. TEST-1 now states the re-key cases per register in
  `scripts/specshift`'s `run_test.go`, asserted by reading each register after the run, and adds the
  resolver case that a baselined non-resolving citation still passes under the new path after a rename.

- **Correction to this pass: SPEC-2's Target paragraph still scoped `tests/spec-map.json` to path
  entries after the re-key rule had been widened to symbol references.** The sub-step's own scope
  declaration read "for the file keys and path entries of the renamed files" while SPEC-2's body, §4.6,
  and §11 all stated both halves, so an implementor working the Target list would have left
  `tests/spec-map.json` lines 2187, 2188, 2370, and 2385 dangling and ended the sub-step with
  `validate-maps` red. The Target clause now names the `::<symbol>` references alongside the path
  entries, so all four statements carry the same predicate.

### Pass 15 (2026-07-28, automated)

- **SPEC-3 cited a blank line as the companion of the §15.3 normative-ownership sentence.**
  `spec/15_external-api-surface.md` line 1467 is empty; the second assertion that §15.4 and its
  subsections remain the normative prose description sits on line 1466, inside the §15.4 preamble at
  lines 1458 through 1469. The disposition was already right, because line 1466 is inside the block the
  §15.4 reduction removes, so only the citation was wrong. SPEC-3 now cites line 1466 and quotes the
  sentence's opening and closing text, so an implementor auditing the reduction boundary lands on the
  sentence rather than on an empty line.

- **The markdown anchors SPEC-2 and TEST-1 depend on were inside the population the name pass rewrites
  and the naming lint fails.** N3 banned both spellings across `docs/`, and three of the six hyphenated
  occurrences the tree carries there are link targets rather than prose: the kramdown attributes at
  `docs/reference/glossary.md` line 207 and `docs/api/internal.md` line 318, and the same-page fragment
  link at `docs/api/internal.md` line 229. The reserved-phrase register carries no leave-unmodified
  disposition, so the pass would have rewritten all three, which contradicts SPEC-2's instruction to keep
  `{: #lifecycle-channel }` on a redirect stub and removes the attribute-resolved fragment TEST-1 uses to
  justify the fragment-link gate's predicate. N3 now places a markdown anchor identifier, meaning a
  kramdown `{: #id }` attribute value and the fragment of an intra-repo markdown link, outside the
  reserved-phrase matcher in both spellings, because an anchor is an addressable link target and rewriting
  one breaks inbound links this repository cannot see. An anchor that has to change moves through the
  anchor class with an entry in `tests/spec-anchor-moves.json` instead. The §4.1 principle bullet, the
  §3.4 class-table row, the naming lint's domain in TEST-1, SPEC-1's zero-occurrence exit criterion, and
  the `docs/` compound figure in SPEC-1 and §11 all carry the narrowed predicate, and the `docs/` compound
  population the register is seeded against is now the 3 prose occurrences across 2 files
  (`docs/reference/adapter-contract.md` line 84 and `docs/runtime-author-guide/lifecycle.md` lines 69 and
  319). TEST-1 adds the accept case, asserting that the pass leaves both anchor sites unmodified without a
  register entry and that the naming lint is green on the same two sites.

### Pass 16 (2026-07-28, automated)

- **SPEC-3 landed the heading walker while staging only half of the walker's predicate for the headings it
  creates.** SPEC-3 wrote a `spec/README.md` row for §28.5, §28.6, §28.7, the §28.5.1 through §28.5.7 card
  headings, `## 29`, and each `spec/29` subsection, and staged no `tests/spec-map.json` key and no
  `tests/spec-map-exceptions.yaml` entry for any of them, so the walker was red on at least eleven
  headings at the exit of the sub-step that lands it. `tests/spec-map.json` is keyed by section number and
  the exceptions validator runs inside the hard-failing `validate-maps` tier-0 check
  (`cmd/lenny-test/cmd_run.go` lines 734 and 742), so a heading acquires walker-visible coverage only
  through one of those two files. SPEC-3's Target now names both files, its change description states the
  key-or-exception instruction over every heading it creates, and the two landing sentences in TEST-1 and
  §6 now read "the §28 and §29 rows and keys SPEC-3 writes". SPEC-1's instruction is stated explicitly for
  §28 and §28.1 through §28.4 as well, rather than left implied by its Target list.

- **SPEC-3 classified two client-facing blocks inside §15.4.1 as adapter-to-binary wire prose and moved
  them to a section with no card that can own them.** The Translation Fidelity Matrix documents
  round-trip fidelity of `MessagePart` through each `ExternalProtocolAdapter`
  (`spec/15_external-api-surface.md` lines 1655 and 1672), and the `MessageEnvelope` block states by its
  own first sentence that the envelope is carried across the stdin binary protocol, the platform MCP
  server tools, and all external APIs (line 1710), holding the `delivery` enum, the `delivery_receipt`
  schema and its `reason` enum, and the `message_expired` event schema and `reason` enum. §28's boundary
  set is closed over `intra-pod`, `gateway-to-pod`, `pod-to-gateway`, `pod-egress`, `gateway-to-store`,
  `inter-replica`, and `control-plane`, so no §28.5 card holds the external-client edge, and moving the
  material would have left the `spec/07_session-lifecycle.md` citations at lines 116, 296, 323, 343, 349,
  and 433 resolving to a card that does not define what they cite. SPEC-3 now carves both blocks out of
  the reduction on the same rule it already applies to §15.4.3 through §15.4.6, keeps the
  `translation-fidelity-matrix` and `messageenvelope--unified-message-format` anchors out of
  `tests/spec-anchor-moves.json`, moves only the stdin and stdout envelope framing to §28, and
  hand-corrects the seven `spec/07` links to the surviving `spec/15` heading. SPEC-4's anchor-pass paragraph
  carries the same narrowed set: the retired unnumbered anchors are `#internal-messagepart-format` and
  `#protocol-reference--message-schemas` with its children, one link is rewritten rather than three, and
  the two links into `#translation-fidelity-matrix` at `spec/15_external-api-surface.md` line 1399 and
  `spec/21_planned-post-v1.md` line 31 are untouched.

- **SPEC-2 declared the manifest rename to reach tiers 3 and 10 while editing a tier-4 fixture in the same
  change.** The renamed key and socket cross a process boundary between the adapter and a separate runtime
  binary, which `.claude/rules/test-coverage.md` line 36 maps to tier 4, and
  `tests/tier4_integration/credential_lifecycle_test.go` is the only in-tree test that drives that flow
  against a real runtime process over a live Unix socket. The listed SDK-parse coverage does not
  substitute, because `cmd/runtimes/streaming-echo` parses the manifest through its own struct tag at
  `cmd/runtimes/streaming-echo/main.go` line 147 rather than through any of the three SDKs, so a rename
  that misses it is a silent JSON unmarshal miss rather than a compile error. SPEC-2 now states that the
  change reaches tier 4, adds a tier-4 item covering the rotation round trip and the negative case of a
  manifest carrying only the retired key as an exit criterion of the sub-step, names
  `cmd/runtimes/streaming-echo/main.go` as a manifest reader the rename moves, and ties the tier-4
  assertions to the `CH-RUNTIMEOPS` card. §11 lists the file.

Corrections to this pass, found by review of its own edits:

- **The MessageEnvelope carve-out named an outcome the anchor pass cannot produce.**
  `tests/spec-anchor-moves.json` is keyed by retired anchor with one successor per anchor, as §3.4 row 3,
  SPEC-4's pass description, and TEST-1's pass cases all state, so the pass cannot send the
  `spec/07_session-lifecycle.md` links into `1541-adapterbinary-protocol` to the surviving `spec/15`
  heading while sending the other links into that same anchor to a §28 card. SPEC-3 now rewrites those
  links by hand, in the same change that splits the heading, so the pass reads a tree with no link into a
  retired anchor at those sites, and §3.4 carries a class row for the case. The register's key, the pass
  description, and the TEST-1 cases are unchanged.

- **The carve-out enumerated five of the seven links.** `spec/07_session-lifecycle.md` line 116 cites the
  same retired anchor for the same surviving material ("All content delivery uses the `MessageEnvelope`
  format"), and line 349 carries two such links rather than one, so the set is seven links across six
  lines: 116, 296, 323, 343, 349 twice, and 433. The bullet, SPEC-4's file-qualified count, and the Pass
  16 entry above now carry the full set, and the four file-qualified links that do go to §28 are named
  with the material each cites.

- **The Translation Fidelity Matrix bullet cited a package path that does not exist.** The implementation
  is `pkg/gateway/externalapi/outputpartfidelity`, which is also the path `tests/spec-map.json` records.

### Pass 17 (2026-07-28, automated)

- **The carve-out's hand-authored class stopped at `spec/07` and left the same collision live among the
  same-page links inside `spec/15`.** Four of the 25 same-page links into `1541-adapterbinary-protocol`
  cite the `MessageEnvelope` material the carve-out keeps in `spec/15`, at
  `spec/15_external-api-surface.md` lines 2165, 2489, 2584, and 2662, and SPEC-4 handed all 25 to the
  mechanical anchor pass, whose map carries one successor per retired anchor. Line 2584 is decisive,
  because it carries two links to that one anchor whose correct destinations differ: the first is labelled
  with the surviving heading's own title and the second cites the internal `MessagePart` format, which
  retires. No gate observes the misdirection, since the fragment-link gate checks only that the target
  resolves, and SPEC-4 then empties `tests/spec-anchor-moves.json`. §3.4 row 12 now states the class as
  every link into a retired anchor whose cited material stays where it is, and names the four `spec/15`
  same-page links alongside the seven `spec/07` links. SPEC-3 enumerates the four and rewrites them by
  hand in the same change that splits the heading, its anchor-pass list for §15.4.3 through §15.4.6 drops
  line 2165 and reads five links rather than six, and SPEC-4 restates the same-page population as 21 links
  after the hand corrections.

- **The §15.4 preamble reduction removed the SDK-warm demotion contract, a client-facing adapter
  obligation with no successor.** `spec/15_external-api-surface.md` line 1468 sits inside the stated
  preamble range and is a normative obligation on third-party adapter implementers: adapters for runtimes
  declaring `capabilities.preConnect: true` must implement `DemoteSDK`, with a 10s teardown timeout
  followed by SIGKILL, a defined post-demotion pod state, and the `UNIMPLEMENTED` error code. Those
  particulars appear nowhere else, because `spec/04_system-components.md` line 652 states the RPC's
  purpose and a gateway-side fallback timeout alone and `spec/06_warm-pod-model.md` lines 40 and 67 state
  the mandatory-support rule and the separate SIGTERM-internal timeout, and §28.5's closed boundary set
  has no card that owns an adapter RPC implementation obligation. SPEC-3 now carves the paragraph out of
  the reduction on the same rule §15.4.3 through §15.4.6 get, restates the preamble range as lines 1458
  through 1468 with that paragraph excluded, and records the inbound references at
  `spec/05_runtime-registry-and-pool-model.md` line 22, `spec/15_external-api-surface.md` line 1114,
  `docs/reference/adapter-contract.md` line 64, `docs/reference/configuration.md` line 153, and
  `docs/reference/error-catalog.md` line 100 that continue to resolve to it. §3.1 carries the same scope.

- **The identifier pass had no dry-run equivalence case while the other three passes each had one.**
  TOOL-1 makes the dry-run output the entry criterion for applying every pass, and the identifier pass is
  the one whose applied change is the largest and the hardest to reverse, since SPEC-2 runs it as one
  exclusive change on a quiesced tree and it both moves files and rewrites their keys in four path-keyed
  registers plus the `::<symbol>` references in `tests/spec-map.json`. No other listed test observes a
  divergence, because the register cases read the tree after the run and the identifier-resolution gate
  runs after the pass has been applied. TEST-1 now carries the same case the other three passes carry,
  covering the file moves and the register re-key edits.

- **Correction to this pass: SPEC-3's post-condition sentence contradicted its own line 2584 case.** The
  sentence stated that the anchor pass reads a tree with no link into a retired anchor at those four
  `spec/15` lines, while the preceding sentence leaves the second link on line 2584 pointing into
  `1541-adapterbinary-protocol` for the pass to redirect to the §28 successor. SPEC-3 now states the
  post-condition per line: no remaining link at lines 2165, 2489, and 2662, and only the second of the two
  links on line 2584, which the pass rewrites. This is also what makes SPEC-4's count of 21 same-page links
  add up.

- **Correction to this pass: SPEC-4's "every link SPEC-3 and SPEC-4 name by line" qualifier went stale when
  the same-page population was narrowed from 25 to 21.** The four SPEC-3 hand-corrects are named by line
  and are no longer in that population, so the qualifier asserted membership the same paragraph denies for
  line 2165. The qualifier now names the five links inside §15.4.3 through §15.4.6 at lines 2163, 2164,
  2394, 2395, and 2441, and the second of the two links on line 2584.

### Pass 18 (2026-07-28, automated)

- **The compound reserved-phrase Go population named a file whose only occurrence is a CLI help string and
  omitted the file that holds that position.** N3's domain is a Go doc comment in a tracked Go file, and
  under that predicate the compound population is 29 occurrences across 18 files rather than 30 across 19:
  `pkg/ctlcli/runtime.go` line 424 sits inside the `runtimeValidateUsage` raw string literal the CLI
  prints, which the name pass does not write and the naming lint does not read. The file that carries the
  compound in a doc comment while its only space-separated occurrence is a string literal is
  `tests/tier8_chaos/credential_rotation_ceiling_test.go`, at lines 64 and 153. Seeding the register from
  the stated population would have registered a site the pass never visits and left the tier-8 doc comment
  unregistered, which aborts the fail-closed run. SPEC-1's five-file list now names the tier-8 file in
  place of `pkg/ctlcli/runtime.go`, SPEC-1 and §11 state 29 across 18, and SPEC-1 gives the CLI help
  string an explicit out-of-scope disposition alongside the other Go string literals.

- **Two same-page `spec/15` links citing surviving `MessageEnvelope` material were handed to the
  single-successor anchor pass.** `spec/15_external-api-surface.md` line 2684 cites the "Ordering
  guarantee" bullet, which sits at line 1829 inside the surviving envelope block, and line 1838 cites the
  full `MessageEnvelope` format, whose definition stays in `spec/15` while line 1838 itself travels to §28
  with the Protocol Reference block. The anchor map carries one successor per retired anchor, so both
  would have resolved to a §28 card that does not define the envelope, and the fragment-link gate cannot
  see it because it checks only that a target resolves. SPEC-3's hand-corrected set is now six same-page
  links, with line 1838 taking the file-qualified form because it lands in §28, §3.4 row 12 states six,
  and SPEC-4's mechanical same-page count is 19 rather than 21.

- **SPEC-3's stated §15.4 preamble reduction range deleted the wire-artifact list the same sub-step says
  §15.4 keeps.** The wire-artifact pointer is the preamble: `spec/15_external-api-surface.md` line 1460
  and the three artifact bullets at lines 1462 through 1464 are what §15.4 is reduced to, and the stated
  exclusion set spared only the SDK-warm demotion paragraph at line 1468. Applying the range as written
  would have left the restated §15.3 sentence pointing at a §15.4 that names no artifact and removed line
  1463, which SPEC-4 counts among the same-page links its anchor pass rewrites. SPEC-3 and §3.1 now state
  the exclusion set as lines 1460 through 1464 together with line 1468, so the preamble reduction removes
  the normative-ownership sentence at line 1466.

- **Same-page fragment links inside the relocated blocks pointed at headings that stay behind, and the
  anchor pass was instructed to leave exactly those links untouched.** A `[...](#anchor)` link resolves
  against the citing page, so six links across five lines inside the internal `MessagePart` format block
  (`spec/15_external-api-surface.md` lines 1537, 1538 twice, 1575, 1649, and 1650) and the `[§4.9]` link at
  `spec/04_system-components.md` line 807 inside the relocated intra-pod protocol block break on the move
  even though their targets, §15.4, §15.5, §15.7, and §4.9, all survive. The anchor map has no entry for a
  surviving anchor, and the pass's stated rule leaves such links alone, so the applied spec would have
  carried seven dangling fragments in §28 and the fragment-link gate would have been red on them beyond
  the seven links SPEC-4 enumerates. §3.4 gains a class row for a same-page fragment carried out of its
  file by a reduction, SPEC-3 enumerates today's population and rewrites each to the file-qualified form
  in the same change that moves the block, and SPEC-4 and TEST-1 state that the gate's
  red-on-introduction population is the seven pre-existing links only once that rewrite has run.

### Pass 19 (2026-07-28, automated)

- **§2 attributed `CH-EVENTSTREAM` to a plan section that no longer carries it.** The naming table at
  `gateway-runtime-comms-remediation.md` line 225 gives C6 the identifier `CH-ADAPTEREVENTS` at line 238,
  and the provenance note at lines 256 through 261 records this proposal's rename and states that
  `CH-EVENTSTREAM` "appears nowhere in the plan and must not be reintroduced". §0 sends an implementor to
  the plan for the naming table, so the present-tense claim of a divergence sent that reader to a table
  that contradicts it. The §2 bullet and N3 now state the rename in the past tense against the plan's
  current text, and the substantive decision is unchanged. The Pass 1 record is left as written, because it
  records what was decided at the time.

- **The `spec/04` §4.7 reduction had no stated boundary and would have carried the adapter manifest field
  set into §28.** The `#### Adapter ↔ Runtime Protocol (Intra-Pod)` block runs from
  `spec/04_system-components.md` line 691 to line 820, and lines 733 through 820 are the manifest
  paragraph, the **Adapter manifest field reference** table, the **Level reading requirements**, the
  **Forward compatibility** silent-ignore rule, and the `runtimeMcpServers` reservation. Relocating the
  whole block would have moved a runtime-author contract into a §28.5 card set that by SPEC-3's own
  carve-out rule cannot own one, and would have falsified N7 in §28.1 and SPEC-2's `runtimeOps`
  justification, both of which read the camelCase convention off the §4.7 field set. SPEC-3 now states the
  boundary line by line, as it already did for §15.4, and carves the manifest material out on the rule
  §15.4.3 through §15.4.6 get, so §4.7 keeps the field set, the heading keeps its anchor, and the
  falsified-sentence class stays a population of one. The `#49-credential-leasing-service` link at
  `spec/04_system-components.md` line 807 leaves the relocated-same-page-link class for the same reason,
  which reduces that class to the six `spec/15` links.

- **A client-facing link into a retired anchor for carved-out material fell into no class.**
  `docs/reference/adapter-contract.md` line 371 cites
  `.../spec/15_external-api-surface.md#1541-adapterbinary-protocol` for the Translation Fidelity Matrix,
  whose heading and `translation-fidelity-matrix` anchor SPEC-3 keeps in `spec/15`. It is a member of the
  carve-out class §3.4 defines, which the class row states covers such a link wherever it sits, but the
  enumeration that drives the hand-authored work omitted it and SPEC-4 disposed of it as an absolute URL
  that neither the pass nor the gate reads. §3.4, SPEC-3, SPEC-4, and §11 now carry it, with its fragment
  hand-rewritten to `#translation-fidelity-matrix` in the change that splits the heading, and the class
  row states that the absolute-URL member is covered by review because the fragment-link gate does not
  read an absolute URL.

- **The retired citation form omitted the colon spelling, which is live in `spec/` itself.** The stated
  form required the literal word `line` or `lines`, so it matched none of the 18 section-number colon
  citations across 10 files (`§17.6:404` at `spec/17_deployment-topology.md` line 450, `§11.2:46` at
  `pkg/adapter/usage.go` lines 52, 62, 152, and 259, and `§15.1:778-780`, `§15.1:798-800`, `§15.1:802`,
  and `§15.1:805-812, 876-878` at `pkg/gateway/externalapi/admin/me.go` lines 78, 92, 103, and 109, among
  others) nor the 11 path-variant colon citations across 7 files (`spec/15_external-api-surface.md:1315`,
  `spec/25_agent-operability.md:2057`, and the rest). Under the proposal's own reasoning for the other
  spellings, SPEC-4 would have reported every per-file count zero and the ratchet would have reached its
  flat prohibition while 29 live line citations survived, one of them inside `spec/` after the change that
  makes N8 normative, and two of them inside the tier-0 gate file SPEC-4 hand-rewrites. §4.6 now
  admits a colon in place of the keyword in both variants and records the measured populations, §3.4 row 5
  names the spelling, TEST-1 adds the line-pass and ratchet cases, and SPEC-4 accounts for
  `spec/17_deployment-topology.md` line 450 and for the two colon citations in
  `tests/tier0_static/degradation_lock_line_citation_test.go` lines 74 and 77. §11 carries the counts.

- **Correction to the preceding entry: §4.6 still sized the read-exclusion difference against the keyword
  spelling alone.** Widening the form to admit a colon brings `PROPOSAL-QUEUE.md` into the population,
  because `grep -nE "§[0-9]+(\.[0-9]+)*:[0-9]+"` over the three files the naming exclusion adds returns
  three hits, all in that file: `§8.3:470` at line 459, and `§8.8:879` and `§15.1:630` at line 477. The
  keyword form adds nothing further to those files, since the same grep with ` lines? ` returns 0 for
  `BUILD-PLAN.md`, 0 for `PROPOSAL-QUEUE.md`, and 1 for `BUILD-PROGRESS.md`, which is the line 34 citation
  already named. All three files sit inside the citation read domain, so the line pass converts them and
  SPEC-4's per-file-count-zero exit criterion covers them. §4.6 now states the difference as four
  citations across two files and names each. The Pass 13 restatement is left as written, because it
  records what was measured at the time.

- **Correction to the preceding entry: SPEC-4's Target enumeration was left at the pre-fix seven
  spellings.** §11, §3.4, and TEST-1 took the colon spelling, but SPEC-4's **Target** list, which is the
  parallel of the §11 blast-radius enumeration and the place an implementor reads what the line pass must
  reach, still ended at the path form. SPEC-4's own later prose already accounts for the spelling in
  `spec/17_deployment-topology.md` and in the tier-0 gate file it hand-rewrites. The Target list now
  carries the colon spelling and its measured counts in the same words §11 uses.

### Pass 20 (2026-07-28, automated)

- **The §4.7 reduction falsified two further `spec/15` sentences that the "population of one" bound
  excluded.** §15.7 Runtime Author SDKs at `spec/15_external-api-surface.md` line 2558 enumerates the
  `lenny/*` platform MCP tool names and states that §4.7 "is authoritative for the platform MCP tool set;
  this list tracks it", and item 7 of the §15.4.5 roadmap at line 2402 sends a Standard-level runtime
  author to §4.7 for both the adapter manifest field reference and the Part B message schemas. SPEC-3
  moves Part A at `spec/04_system-components.md` line 697, which is the only enumeration of that tool set
  in §4.7, and the message-schema table at lines 715 through 731, so both sentences assert an ownership
  §4.7 no longer holds. No pass repaired either one: neither carries a line citation, the §15.7 sentence
  carries the reserved word only as "lifecycle signal", and the `#47-runtime-adapter` anchor survives and
  gains no `tests/spec-anchor-moves.json` entry. SPEC-3 now enumerates both as hand-authored corrections,
  and the "exactly one"/"population of one" bound is removed from SPEC-3, from the §3.4 class row, and
  from the §4.7 carve-out paragraph. The Pass 19 record is left as written, because it records what was
  measured at the time.

- **The same reduction falsified two shipped schema descriptions outside `spec/`, which fell into no
  class.** `schemas/lifecycle-events.schema.json` line 5, which SPEC-2 renames to
  `schemas/runtime-ops-events.schema.json`, tells runtime authors that the frame field names are camelCase
  "to match the §4.7 message-schema table" and closes with "See spec/04_system-components.md §4.7 and
  spec/15_external-api-surface.md §15.4", and `schemas/messagepart.schema.json` line 5 closes with "See
  spec/15_external-api-surface.md §15.4" for a format whose defining block at
  `spec/15_external-api-surface.md` line 1515 moves to §28. `schemas/embed.go` embeds both artifacts so
  `cmd/lenny-compliance` and `lenny runtime validate` carry them into repositories where `schemas/` is
  absent, which puts these descriptions on the same footing as the
  `schemas/lenny-adapter-jsonl.schema.json` description SPEC-2 already corrects by hand. The name pass
  rewrites only the reserved phrase, the line pass matches only the line-citation form and neither pointer
  carries a line number, and both cited anchors survive, so no pass reached them. SPEC-3 now stages both
  as hand-authored corrections re-pointed at the owning §28.5 cards, §11 lists both files, and the claim
  that no normative sentence outside `spec/15` is falsified by the reduction is replaced by the
  enumerated set.

- **Correction to this pass: SPEC-3's Target did not carry the four new edit sites the pass added.** The
  Target scoped the `spec/15` edits to §15.3 and §15.4 and named no `schemas/` artifact, while the two
  bullets above put hand corrections in §15.7 at line 2558, in the §15.4.5 roadmap at line 2402, and in
  the `description` of `schemas/runtime-ops-events.schema.json` and `schemas/messagepart.schema.json`, all
  of which §11 attributes to SPEC-3. An implementor scoping the sub-step from the Target would have
  applied the reduction without the corrections. The Target now reads §15.3, §15.4, §15.4.5, and §15.7 for
  `spec/15` and lists both schema artifacts under their post-SPEC-2 names.

### Pass 21 (2026-07-28, automated)

- **The §15.4 carve-out preserved a sentence that contradicts the artifact description SPEC-2 corrects.**
  `spec/15_external-api-surface.md` line 1463 states that `schemas/lenny-adapter-jsonl.schema.json` is the
  JSON Schema for "every adapter↔binary stdin/stdout message ... and every lifecycle-channel message". The
  artifact defines exactly `messageEnvelope`, `from`, `heartbeat`, `heartbeat_ack`, `shutdown`,
  `tool_call`, `tool_result`, `response`, `status`, and `set_tracing_context`, and its own `description` at
  line 5 declares the checkpoint, interrupt, `credentials_rotated`, and `deadline_approaching` frames out
  of scope; those frames are schematized in `schemas/lifecycle-events.schema.json`, which SPEC-2 renames to
  `schemas/runtime-ops-events.schema.json`. SPEC-2 corrected the artifact `description` and no pass
  corrected the specification sentence, while SPEC-3 carved that sentence out of the §15.4 reduction so it
  survives, leaving the two published representations of the same wire artifact in disagreement. The sense
  register cannot repair it, because every candidate substitution leaves a precise false statement about
  the artifact's contents. SPEC-2 now corrects line 1463 by hand in the same change as the artifact
  `description`, closing the parenthetical after `set_tracing_context` and sending the runtime-operations
  frames to `schemas/runtime-ops-events.schema.json`. SPEC-2's Target adds the line, the SPEC-3 carve-out
  paragraph records that the surviving sentence is corrected upstream, the §5 risk row naming the
  hand-corrected wrong-mechanism sites includes it, and §11 records it under
  `spec/15_external-api-surface.md`.
- **Correction to the entry above: the §7 statement of what substitutes for a gate still carried the
  superseded split.** The "What the gates do not cover" paragraph in §7 assigned every in-`spec/` member of
  the ungated wrong-mechanism class to SPEC-1 and described SPEC-2's hand corrections as "the wrong
  descriptions in the three shipped artifacts", which contradicts SPEC-2 as now written and repeats the
  count removed from the SPEC-2 lead-in and the §11 `schemas/` bullet. The paragraph now matches the §5
  risk row: the sites whose current text names the wrong participant are corrected by hand in SPEC-1, and
  the wrong artifact descriptions together with the artifact-scope sentence at
  `spec/15_external-api-surface.md` line 1463 are corrected by hand in SPEC-2.
- **Correction to the entry above: §3.2 named the three artifact-side corrections as SPEC-2's whole
  hand-correction set.** The paragraph read "SPEC-2 stages those three by hand" and then bounded the class
  "at three". It now names the line 1463 correction alongside them and drops the number from the
  unboundedness sentence, so §3.2, §5, §7, SPEC-2, and §11 state the same set.

### Pass 22 (2026-07-28, automated)

- **The §15.4 preamble reduction deleted the wire-artifact compatibility contract, the only statement of
  it in the tree.** `spec/15_external-api-surface.md` line 1466 is one markdown line carrying four
  sentences, and only the last states the normative ownership the reduction retires. The first three state
  the compatibility contract third-party runtime authors and SDK maintainers build against: the artifacts
  are versioned by Lenny release tag, `.proto` breaking changes follow buf-style breaking-change rules
  while JSON Schema changes follow the `additionalProperties` discipline, and `examples/runtimes/echo/` is
  built from the same `.proto` file and serves as the executable reference. A tree-wide grep for
  `buf.build`, `buf-style`, `breaking-change rules`, `additionalProperties discipline`, `versioned by Lenny
  release tag`, and `executable reference` over `spec/` and `docs/` returns that line alone, so the
  reduction as stated destroyed the contract with no carve-out and no successor pointer, since the
  successor pointer names channel identifiers and this material is not a channel contract. SPEC-3's two
  statements of the range also disagreed with each other, one removing the whole line and the other
  removing the ownership sentence alone. SPEC-3 now states the reduction at sentence granularity: the
  three compatibility-contract sentences are carved out on the same rule the wire-artifact pointer at
  lines 1460 through 1464 gets, and the reduction removes the normative-ownership sentence that closes the
  line. §3.1's summary, the reduction paragraph, and the carve-out paragraph now state the same range.
- **A second §15.7 site attributes the platform MCP tool set to a §4.7 Part A the reduction moves, and it
  was in no pass and in no class.** `spec/15_external-api-surface.md` line 2700, inside the Runtime Author
  SDK `Reply` type documentation, reads "// MCP tool ([§4.7](04_system-components.md#47-runtime-adapter)
  Part A) with" and so attributes the `lenny/output` platform MCP tool to §4.7 Part A. Part A at
  `spec/04_system-components.md` line 695 moves to §28, after which §4.7 has no Part A. No pass reaches the
  line, by the same reasoning SPEC-3 already records for line 2558: it carries no line citation, carries
  neither reserved word as a bare noun phrase, and its `#47-runtime-adapter` link survives and gains no
  `tests/spec-anchor-moves.json` entry. SPEC-3 now lists line 2700 in the hand-authored falsified-sentence
  enumeration with the parenthetical rewritten to name the §28.5 card that owns the intra-pod platform MCP
  server contract, states the class population as six, and §11 names the correction alongside the other
  three in `spec/15_external-api-surface.md`.

### Pass 23 (2026-07-28, automated)

- **SPEC-3's tier-11 instruction contradicted its own §4.7 reduction boundary and would have turned tier 11
  red at the sub-step's exit.** The atomic sub-step told an implementor that the invariant the three
  `tests/tier11_docs/` §4.7 tests encode moves to a §28.5 card and that a relocated row carries its pin
  with it, re-scoped to `spec/28`. No row those tests pin is inside the relocated block. The reduction
  boundary SPEC-3 states moves the content of `spec/04_system-components.md` lines 695 through 731 alone,
  inside the block that opens at line 691, while
  `tests/tier11_docs/recycle_scrub_trigger_consistency_test.go` pins the `Terminate` (proto `Shutdown`)
  row at line 664, `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` pins the
  `AdapterTerminating` event row at line 688, and
  `tests/tier11_docs/budget_extension_trigger_consistency_test.go` asserts the absence of `ExtendLease`
  over the whole section. All three sit above the boundary, and a grep of `tests/tier11_docs/` for
  `Adapter ↔ Runtime`, `Part A`, `Part B`, and `adapter--runtime-protocol` returns nothing, so nothing
  those tests pin relocates. Re-scoping the `specSection(..., "### 4.7 ")` calls to `spec/28` would have
  demanded rows `spec/28` does not carry and would have stopped guarding the §4.6.1 coordinator-loss route
  the eviction test exists for. SPEC-3 now records that §4.7 keeps the gateway-to-adapter RPC and event
  tables, that all three files stay green without an edit at this sub-step, and that they are named so an
  implementor confirms that state, on the same rule as the two embedded-anchor files. The conditional rule
  is kept and its population today is empty. The one edit those files take, the reserved noun phrase in
  the pinned §4.6.1 clause at `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69,
  is attributed to SPEC-1's name pass.
- **The carve-out class covered markdown links alone, so bare `§15.4.1` citations of carved-out material,
  including in all three published runtime SDKs, would have been redirected to the wrong §28 card.** The
  anchor pass rewrites bare section citations as well as links, and
  `tests/spec-anchor-moves.json` carries one successor per retired anchor, so every bare `§15.4.1`
  citation of the Translation Fidelity Matrix or of `MessageEnvelope` material would have been sent to the
  §28 adapter-to-binary card that, by SPEC-3's own carve-out argument, does not define either. Outside
  `spec/` and `proposals/` the tree carries 669 such occurrences across 150 files, 595 across 148 once
  §4.6's read exclusion of `BUILD-GAPS.md` and `TEST-GAPS.md` is applied, among them
  `pkg/gateway/externalapi/outputpartfidelity/matrix.go` line 3, `pkg/gateway/session/sessioninbox/events.go`
  line 45 for the `message_expired` payload, and the canonical `MessageEnvelope` documentation in all
  three runtime SDKs at `sdks/runtime/go/runtime/types.go` lines 52 and 224,
  `sdks/runtime/python/lenny_runtime/types.py` lines 145 and 391, and
  `sdks/runtime/typescript/src/types.ts` lines 59 and 211. §3.4's carve-out row now states the class over
  every reference in any carrier the anchor pass writes rather than over links alone. SPEC-3 seeds
  `tests/registers/anchor-senses.yaml`, keyed by file and occurrence, recording each occurrence's
  destination among the §28 card, `#translation-fidelity-matrix`, and
  `#messageenvelope--unified-message-format`, on the same per-occurrence mechanism the name and identifier
  passes already use for a two-valued term, and the anchor pass fails an occurrence with no entry rather
  than substituting the map's successor. TEST-1 gains the fail-closed case and a case pinning that a
  citation of carved-out material resolves to the surviving `spec/15` heading, and SPEC-4 retires the
  register with the map on the same run-completeness criterion.
- **Correction to the bullet above: the bare-citation population figures were measured over a scope wider
  than the sentence stated, so two of the three headline figures counted `spec/` and `proposals/` sites
  the sentence excludes.** The stated 689 citations across 159 files and 26 of the `§15.4.2` form matched
  no single population. Measured outside `spec/`, `proposals/`, and `.git/`, the tree carries 669
  occurrences of `§15.4.1` across 150 files and 21 of `§15.4.2` across 9 files; 159 is the file count only
  when `spec/` (4 files) and `proposals/` (5 files) are added back, and 26 is the `§15.4.2` count only
  tree-wide. Applying §4.6's read exclusion of `BUILD-GAPS.md` (69 and 6 occurrences) and `TEST-GAPS.md`
  (5) leaves 595 across 148 files and 15 across 8. The figure sizes the per-occurrence seed of
  `tests/registers/anchor-senses.yaml`, and an implementor sizing that seed from 159 files would have
  included `proposals/` sites that no pass writes. SPEC-3, the §11 files-touched bullet, and this record
  now carry the corrected figures and name which measurement each is. The per-directory sub-counts change
  only for `pkg/`, which is 293 occurrences over 292 matching lines; `sdks/` (189) and `cmd/` (64) are
  unchanged, and every named site still resolves.

### Pass 24 (2026-07-28, automated)

- **The falsified-sentence population omitted the `docs/api/internal.md` pointer that sends runtime
  adapter authors to §15.4 for the binary protocol and the `MessagePart` format.** SPEC-3 closed the class
  at six members, all inside `spec/` or the two shipped schema descriptions, while `docs/api/internal.md`
  line 544 states "For the complete binary protocol specification, including `MessagePart` format,
  `MessageEnvelope` schema, and level-specific behavior, see the technical design document Section 15.4"
  on a page whose audience line at line 11 names runtime adapter authors. The §15.4.1 reduction retires
  `#### 15.4.1 Adapter↔Binary Protocol` at `spec/15_external-api-surface.md` line 1470 and moves the
  internal `MessagePart` format heading at line 1515 to §28, so two of the four things the sentence names
  leave §15.4, while the `MessageEnvelope` heading at line 1708 and the level-specific behavior in §15.4.3
  through §15.4.6 are carved out and stay. That is the same half-true structure the §15.4.5 item 7 member
  already gets, and the same criterion under which
  `schemas/messagepart.schema.json` is corrected. No pass reaches the sentence: it carries no line
  citation, it is bare prose rather than a markdown link, it names the surviving §15.4 rather than a
  retired anchor, and it carries neither reserved word as a bare noun phrase. The population is now seven,
  the sentence is split by hand in the change that lands the reduction so the binary protocol
  specification and the `MessagePart` format point at the §28.5 adapter-to-binary card while the
  `MessageEnvelope` schema and the level-specific behavior keep pointing at §15.4, and `docs/api/internal.md`
  is added to SPEC-3's Target, to §3.4's falsified-sentence row, and to §11.

### Pass 25 (2026-07-28, automated)

- **The falsified-sentence population omitted the shipped JSONL schema's own spec pointer.** SPEC-3 closed
  the class at seven members and named only `schemas/runtime-ops-events.schema.json` and
  `schemas/messagepart.schema.json` among the `schemas/` artifacts, while the `description` of
  `schemas/lenny-adapter-jsonl.schema.json` at line 5 closes with the identical sentence "See
  spec/15_external-api-surface.md §15.4". That artifact schematizes the adapter↔binary stdin/stdout
  messages, whose definitions are `#### Protocol Reference — Message Schemas` at
  `spec/15_external-api-surface.md` line 1836 and its eight `#####` children, which SPEC-3 itself says move
  to §28 with the §15.4.1 reduction, while its `messageEnvelope` `$def` is defined by the `MessageEnvelope`
  heading at line 1708 that the carve-out keeps. SPEC-2 runs first and corrects only the wrong-mechanism
  half of that `description`, so the pointer survives the sub-step that falsifies it, and no pass repairs
  it on SPEC-3's own reasoning for the other two artifacts. The artifact is embedded through
  `schemas/embed.go` and carried by `cmd/lenny-compliance` and `lenny runtime validate` into third-party
  runtime repositories. The population is now eight, the pointer is split by hand on the same rule as the
  `docs/api/internal.md` member so the stdin/stdout message schemas point at the §28.5 adapter-to-binary
  card while the `messageEnvelope` reference keeps pointing at §15.4, and the artifact is added to SPEC-3's
  Target, to §3.4's falsified-sentence row, and to §11.
- **TEST-1 landed no cases for the fragment-link gate.** §3.5 sub-step 5 states that TEST-1 adds the
  accept, reject, and boundary cases for every gate the earlier sub-steps land, and TEST-1 added them for
  every other gate while stating only the fragment-link gate's predicate. §3.4 rests three classes on that
  gate alone and part of the carve-out class on it, and the gate is green tree-wide once SPEC-4 corrects
  the seven links it enumerates, so a predicate that selects zero links, that drops the same-page
  `[...](#anchor)` form, or that never reads the kramdown attribute branch is indistinguishable from a
  correct one. TEST-1 now carries a cases paragraph for the gate, with `docs/api/internal.md` line 229
  against the attribute at line 318 as the worked attribute-branch case and a zero-selection case.
- **TEST-1 landed no cases for the tier-11 successor-pointer check.** The check's whole contribution was
  one sentence stating its predicate, and SPEC-3 writes the successor pointers and lands the check in the
  same sub-step, so the check is green on introduction and nothing showed it fires. The pointer is
  normative under §28.1 N8 and §4.5, and §3.4 names the check as the mechanical half of the
  falsified-sentence class's proof. TEST-1 now carries a cases paragraph in `tests/tier11_docs/` covering
  the present pointer, the missing pointer, the unresolvable named heading, the out-of-domain section, and
  the zero-selection run, matching the treatment the naming lint and the heading walker already get.

### Pass 26 (2026-07-28, automated)

- **The citation resolver's seeded baseline sat inside the resolver's own read domain.**
  `tests/registers/line-citation-resolution.yaml` is keyed by file and citation text, so it holds a copy
  of the text of roughly 1,500 non-resolving citations, and §4.6's read exclusion named only `proposals/`,
  the two historical audit records, and the two root planning documents. The resolver therefore read every
  copy a second time under the register's own path, where each one is non-resolving by construction and is
  the exact outcome TEST-1 pins as a failure when it requires that a baseline entry does not travel between
  files, and seeding an entry for a copy would add a further copy, so the seeding would not converge. The
  ratchet had the matching defect, because the register was a file absent from
  `tests/registers/line-citations.yaml` and would fail on its first line citation. Both outcomes contradict
  §3.5 step 1's requirement that proposal 0065's gates land green. §4.6's read exclusion now names both citation
  registers as a fifth group, with the reason stated, and TEST-1's resolver cases gained a case pinning
  that neither gate reads its own baseline as tree content.
- **The skip-reason classifier's seeded baseline was measured over `t.Skipf` alone.** The predicate covers
  a `t.Skip` or `t.Skipf` call whose first argument is a string literal, while the two seeded populations
  were 201 non-conforming `t.Skipf` sites and ten non-literal `t.Skip` sites, leaving non-conforming
  `t.Skip("<literal>")` sites in neither. A measurement over `tests/`, `pkg/`, and `cmd/` under the
  proposal's own category list finds 186 such sites, among them
  `tests/testinfra/kind/kind.go` line 71 and `tests/testinfra/chaos/chaos.go` line 52, so the fatal tier-0
  check would have been red on introduction against an unregistered population that the downward-only
  baseline forbids adding later. TOOL-1 now states the measurement over both call forms, seeds both
  populations into `tests/registers/skip-reasons.yaml`, and records the exact count its own classifier
  produces, and TEST-1's rejection case now covers both call forms.
- **TEST-1 landed no cases for the rewritten §25.4 freshness gate.** SPEC-4 replaces the predicate of the
  running tier-0 test `tests/tier0_static/degradation_lock_line_citation_test.go`, and the proposal states
  that its `wantSubstring` check is a freshness property no other gate carries. The rewritten predicate is
  green from the moment it lands, so a matcher that matches nothing, a heading lookup that returns an empty
  body, or a declaration table that selects zero entries is indistinguishable from a working one, which is
  the inertness case §3.5 sub-step 5 requires for every comparable gate. TEST-1 now carries a cases
  paragraph covering the resolving citation, the stale `wantSubstring`, the absent citation, the
  unresolvable heading, the retired `§25.4 line N` form, and the zero-selection run, and SPEC-4 states that
  TEST-1 names them.
- **Correction to the preceding bullet: the fail-loud citation named the wrong lines.** The new cases
  paragraph cited `tests/tier0_static/degradation_lock_line_citation_test.go` "lines 38 and 101 through
  103" for the `t.Fatalf` the argument rests on. Lines 101 through 103 hold the path join, the comment-block
  read, and the regular-expression match; the non-match guard is at line 104 and the `t.Fatalf` is at line
  105. The two parallel statements of the same mechanism cited "lines 103 and 104", so the three sites
  disagreed with each other as well as with the file. All three now name line 38 for the expression and
  lines 104 and 105 for the guard and the fatal call.

### Pass 27 (2026-07-28, automated)

- **The citation resolver's seeded baseline inside its own read domain was re-checked and is closed.** The
  read exclusion in §4.6 now names `tests/registers/line-citation-resolution.yaml` and
  `tests/registers/line-citations.yaml` as a fifth group with the non-convergence reason stated, §4.6
  states that every measurement, every seeded baseline, SPEC-4's Target, and §11 are stated against that
  read domain, and TEST-1 carries the case pinning that neither gate reads its own baseline as tree
  content. The finding required no further change beyond that verification.
- **The skip-reason classifier's seeded population was re-measured over both call forms and the two figures
  were corrected.** TOOL-1 stated 201 non-conforming `t.Skipf` sites and 186 non-conforming
  `t.Skip("<literal>")` sites. Counting under the proposal's own category list over `tests/`, `pkg/`, and
  `cmd/`, discarding a line whose code is commented out, gives 204 and 189. TOOL-1 now states those figures
  and states the
  counting predicate that produces them, so the red-on-introduction population proposal 0065 seeds into
  `tests/registers/skip-reasons.yaml` is stated at the size the tree carries. The seeding of both call
  forms, the ten non-literal sites, and TEST-1's rejection case covering both forms were already in place
  and are unchanged.
- **Correction to the re-measurement above: the 202 and 185 figures were short by six sites and are now 204
  and 189.** The filter that produced 202 and 185 discarded any line matching `:\s*//`, which also matches
  the `://` inside a URL, so six live non-conforming skip sites whose reason text carries a URL were dropped:
  `tests/testinfra/load/k6.go` lines 101 and 110, `tests/tier1_unit/helm/helm_test.go` line 55,
  `tests/testinfra/security/kubebench/kubebench.go` line 31, `tests/testinfra/security/sbom/sbom.go` line 32,
  and `tests/testinfra/security/zap/zap.go` line 37. Each is executable code, opens with none of the nine
  categories, and is the body of a `SkipUnless*` helper rather than a call to one, so the classifier selects
  all six. Re-running the count with the comment test anchored to the start of the source line gives 204
  `t.Skipf` sites and 189 `t.Skip("<literal>")` sites. TOOL-1 now states those figures and states the
  anchoring, so the seeded `tests/registers/skip-reasons.yaml` baseline covers every site the classifier
  reports and lands green as §3.5 step 1 requires. The parallel statement that the seeded population is on
  the order of 390 sites still holds at 393 and is unchanged.

### Pass 28 (2026-07-28, automated)

- **The proto no-drift test's toolchain-absence branch was enumerated over two binaries when its producer
  needs four.** TEST-1 stated that `make generate-proto` needs `buf` and `goimports`, so a run missing a
  codegen plugin would have turned tier 0 red on a tree with no drift while the target itself succeeds
  there, and the `UNVERIFIED` state this proposal adds would never have fired for that condition.
  `schemas/buf.gen.yaml` lines 16 through 21 declare `protoc-gen-go` and `protoc-gen-go-grpc` as `local:`
  plugins that `buf generate` resolves from `PATH`, `scripts/setup-dev.sh` lines 390 and 391 install both
  into `GOPATH/bin`, and `Makefile` line 94 prepends `$(GOPATH_BIN)` to `PATH` for exactly that reason.
  §4.6 and TOOL-1 now state the producer's dependencies as `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`,
  and `goimports`, require the test to reproduce the target's `PATH` prepend before invoking `buf`, and
  TEST-1's case now reads that a run in which any of the four producer binaries is unavailable records the
  tier as `UNVERIFIED` rather than passing or failing. The separate case that a `buf generate` producing
  zero files fails is unchanged.

- **§28.1's own statement of N3 quoted both banned spellings, so the naming lint could not be green at the
  exit of the sub-step that lands it.** N3 named the space-separated and hyphenated-compound spellings by
  reproducing them, and `spec/28_communication-channels.md` is inside `spec/`, which is inside both the
  lint's domain and SPEC-1's exit criterion, with no code-span or self-quotation exclusion stated anywhere.
  N3 now describes the two spellings rather than reproducing them, and states that the literal spellings
  are carried in `.claude/rules/channel-naming.md` and in the lint's own matcher, both outside the domain
  N3 names. Describing them was chosen over adding a self-quotation exclusion because it keeps the matcher
  a single predicate with one stated exclusion, which is the markdown anchor identifier. TEST-1's naming
  lint cases now pin that the normative statement of N3 passes the lint. §3.4 row 1's gate column and
  SPEC-1's exit criterion are unchanged, because no exclusion was added.

- **Correction to the fix above: N3 asserted that `.claude/rules/channel-naming.md` carries the two
  literal spellings, but SPEC-1's only instruction for that file was to state N1 through N8, which after
  the de-quoting carry no specimen.** As written, §28.1 would land asserting a location for the specimens
  that nothing in the proposal creates. SPEC-1 now instructs the file to carry the two banned spellings
  verbatim alongside N1 through N8, and states why the file can carry them: it is not under `spec/`,
  `docs/`, or `schemas/`, is not a tracked root-level markdown document, and is not a Go doc comment, so
  it is outside every part of the domain N3 names. §11's touched-file entry records the same content.

### Pass 29 (2026-07-29, automated)

- **The residual gate was placed on the shared register contract, whose expiry and blocker ratchet rules
  no permanent class-register entry can satisfy.** TOOL-1 described the gate as a `tests/tier0_static` Go
  test "over the shared register contract", while §4.7 closes a residual either as a seeded in-class
  member or as an explicit exclusion with a reason. Both are permanent statements about the tree, so
  neither carries a date on which it becomes wrong nor an open item a blocker can resolve to, and the
  shared contract's second and third ratchet rules would have failed every entry, leaving the gate unable
  to land green. §4.7 now gives each class register its own permanent-entry schema of a member, a class,
  an `in-class` or `excluded` disposition, and a reason, on the same argument §4.4 makes for
  `tests/claim-map.json` and §4.6 makes for the citation baselines. TOOL-1 and TEST-1 state the same
  schema, TEST-1 adds the missing malformed-or-missing-register case, and the residual gate now appears in
  the TEST-1 and §6 sentences listing the gates that land green by their own baselines rather than through
  the shared register.

- **The residual gate landed in TOOL-1 while three of the class registers it subtracts are first seeded in
  SPEC-1, SPEC-2, and SPEC-3.** `tests/registers/reserved-phrase-senses.yaml` is seeded in SPEC-1,
  `tests/registers/identifier-senses.yaml` in SPEC-2, and `tests/registers/anchor-senses.yaml` in SPEC-3,
  and each of those classes has a large live population on the unmodified tree, so a single TOOL-1 landing
  would have left tier 0 red at the exit of TOOL-1, SPEC-1, and SPEC-2, against §3.5's rule that a gate
  lands with the sub-step that supplies its route to green. The residual gate is now stated with a
  per-class landing: the mechanism is built in TOOL-1, and each class's check lands with the sub-step that
  seeds that class's register. §3.5 states the per-class rule and lists the assignment, proposal 0065 names the
  registers whose checks it lands and those it defers, proposal 0065's closed list of the gates it builds now
  includes the residual gate and its own checks, and TEST-1's landing sentence matches.

- **No proto RPC or message-type spelling was stated for `CH-ADAPTEREVENTS`, and the plan rows the
  proposal deferred to still carry the retired `EVENTSTREAM` stem.** The plan's naming table carries
  `CH-ADAPTEREVENTS` and its provenance note bans the retired stem, but the plan's R1b scope table at
  `gateway-runtime-comms-remediation.md` line 418 and line 423 still spells the rename targets as
  `rpc AdapterEventStream(stream AdapterEventStreamRequest) returns (stream AdapterEventStreamResponse)`
  and `pkg/adapter/eventstream.go`, both of which fail N3 and N4. An implementor driving the rename from
  the plan would have published a gRPC method name, two message type names, and a Go file stem that
  contradict §28.1. SPEC-2 now states the carrier spellings explicitly, as it already did for the manifest
  key and the flag: `AdapterEvents`, `AdapterEventsRequest`, and `AdapterEventsResponse` for the RPC at
  `schemas/lenny-adapter.proto` line 227 and its two messages, `/lenny.adapter.v1.Adapter/AdapterEvents`
  for the full-method literals in `pkg/adapter/holdstate.go` line 57 and `pkg/adapter/holdstate_test.go`
  line 292, and `pkg/adapter/adapterevents.go` with its test sibling for `pkg/adapter/controlchannel.go`.
  SPEC-2 also stages the correction of the two plan rows, the §2 decision bullet no longer claims that no
  divergence from the plan remains, and §11 records both touched surfaces.

- **The permanent-entry schema was asserted of the pass registers, five of which are keyed differently and
  are transitional.** The previous bullet's fix stated that "each class register" carries a member, a
  class, an `in-class` or `excluded` disposition, and a reason, and that every such register is permanent,
  while TOOL-1 named those registers as `tests/registers/line-citations.yaml` (a per-file count that
  reaches zero), `tests/registers/line-citation-resolution.yaml` (keyed by file and citation text, emptied
  by SPEC-4), `tests/registers/change-graph-coverage.yaml` (keyed by path prefix, rewritten downward
  only), `tests/registers/reserved-phrase-senses.yaml` and `tests/registers/identifier-senses.yaml` (keyed
  by file and occurrence, mapping a site to a canonical identifier or a sense), and
  `tests/registers/anchor-senses.yaml` (keyed by file and occurrence, retired by SPEC-4 with the map). The
  claim was false of every one of them, and an implementor was told to read a permanent
  member-and-disposition schema out of counts, per-occurrence sense maps, and a prefix baseline. §4.7 now
  gives the residual check a register of its own per class at `tests/registers/residual-<class>.yaml`,
  seeded alongside the class's pass or baseline register and held separately from it, and states why the
  two record different things. proposal 0065 names the residual register for each of the classes whose checks it
  lands and for each of the three it defers, TEST-1's malformed-or-missing case names the residual
  register, and §3.5, §5, the §2 decision bullet, and §6's closing paragraph use the same term.

- **The permanence argument cited §4.6, which argues the opposite.** §4.7 credited both §4.4 and §4.6 for
  the permanent-entry schema, but §4.6 argues that a stale citation's correct disposition is retirement in
  SPEC-4 rather than an owned and dated entry, which is an argument for a transitional baseline. §4.7 now
  cites §4.4's `tests/claim-map.json` argument for permanence and cites §4.6 only as the precedent for
  holding a population in a register of its own rather than in the shared exception register.

- **The generated-artifact class's register was a denylist compiled into `scripts/specshift`, which can
  carry no disposition and no reason and cannot be malformed or missing.** TOOL-1 subtracted "the
  generated-artifact denylist in `scripts/specshift`", so a residual in that class had no route to closure
  as an explicit exclusion with a reason and TEST-1's malformed-or-missing case was untestable for it.
  TOOL-1 now names `tests/registers/residual-generated-artifacts.yaml` as that class's residual register,
  states its broad predicate as the per-file generation rule §4.6 states, and leaves the denylist as the
  pass driver it is in the §3.4 class table.

### Pass 30 (2026-07-29, automated)

- **An `in-class` residual entry was declared permanent, so TEST-1's dead-entry case would have turned
  tier 0 red at the exit of the sub-step that seeded the register.** §4.7 asserted that both dispositions
  are permanent and that "neither has a date on which it becomes wrong", while registering a member
  `in-class` is the route by which the pass handles it. For every pass-driven class the seeded in-class
  population is exactly what the pass eliminates, so at the sub-step's exit criterion (SPEC-1's zero
  occurrences of each reserved bare noun phrase, SPEC-2's identifier collapse, SPEC-3's and SPEC-4's anchor
  and citation passes) those members no longer match the class's broad predicate and TEST-1's case for an
  entry naming a member the predicate no longer matches fired on them, with no route to retirement in a
  schema that carries no expiry and forbids narrowing the predicate. §4.7 now separates the two lifetimes:
  an `excluded` entry is permanent, and an `in-class` entry is transitional and is removed from the
  residual register in the same run in which the pass handles its member, on the run-completeness criterion
  §4.6 states for the citation baselines and SPEC-1 states for
  `tests/registers/reserved-phrase-senses.yaml`. proposal 0065's seeding sentence and the SPEC-4 sentence on the
  residual gate's route to green carry the same rule, and TEST-1's dead-entry case now ranges over
  `excluded` entries only, with two new cases pinning that a handled in-class member is removed in the same
  run and that an in-class entry surviving the run that handled its member fails.

- **The lifetime rule above declared every `in-class` entry transitional, which is false for the three
  classes no pass eliminates.** proposal 0065 lands residual checks for eight classes, and only five of them are
  emptied by a pass. The generated-artifact class's members keep their generation marker and its driver is
  a skip denylist, the change-graph coverage class is a path-prefix baseline rewritten downward only, and
  the skip-reason class's population is the permanently-correct host-capability skips whose baseline is
  also rewritten downward only. For those three an in-class member never stops matching the broad
  predicate, so removing its entry "in the same run in which the pass handles its member" would re-expose
  the member as a residual on the next run and turn tier 0 red by the gate's own subtraction rule. The
  blanket rule also left the no-expiry argument in TOOL-1 and SPEC-4 resting on retirement by a pass that,
  for those three classes, never runs. §4.7 now conditions the lifetime on the class: an `in-class` entry
  is transitional in a class whose pass eliminates its members and permanent, for the same reason an
  exclusion is, in a class no pass eliminates. proposal 0065's seeding sentence and SPEC-4's route-to-green
  sentence carry both cases. TEST-1 defines a handled member as one the class's broad predicate no longer
  matches, so its two new cases cannot fire on a permanently-matching member, and adds a case pinning that
  an in-class entry in a non-eliminating class survives repeated runs green.

### Pass 31 (2026-07-29, automated)

- **§4.7 and TOOL-1 stated two different broad predicates for the generated-artifact residual class, and
  §4.7 called proposal 0065's insufficient.** TOOL-1 pointed the class's predicate at §4.6's per-file generation
  rule, which was marker-based only, while §4.7 states the predicate as the union of a generation marker
  and membership in a generator's output set "since neither signal alone covers the tree". The two are not
  the same set, and the marker branch under-selects: the five CRDs in `charts/lenny/crds/` and their five
  copies in `pkg/embedded/crds/` are controller-gen and copy output whose first content after the document
  marker is `apiVersion` and which carry no generation declaration anywhere
  (`charts/lenny/crds/lenny.dev_runtimes.yaml` lines 1 through 6), so a marker-keyed predicate would both
  direct a pass to write them and leave the residual gate unable to select a future producer output with
  no marker, which is the member the residual exists to catch. §4.6's rule now states the same union
  proposal 0065 and §4.7 state, with the producer-output-set disjunct named as necessary and the output sets
  read from the producer list §4.6 already carries rather than by running a producer, so the residual
  scan, the write exclusion, and the `scripts/specshift` denylist range over one predicate. proposal 0065's
  class list is restated so the predicate explanation follows the list rather than interrupting it, and
  TEST-1's residual-gate cases gain a case pinning the second disjunct, which is that a file in a
  producer's output set carrying no marker and absent from both the enumeration and
  `tests/registers/residual-generated-artifacts.yaml` is reported as a residual and named.

- **Correction to the entry above: §4.7's own predicate sentence was left at two disjuncts while §4.6
  and TOOL-1 were rewritten to three and both cite §4.7 as the definition.** §4.7 governs the residual
  mechanism, so a file whose generation declaration sits in top-level document metadata rather than a
  header comment matched §4.6 and TOOL-1 but not the section they defer to, and
  `charts/lenny/values.schema.json` is that branch's case, with its notice in the top-level `description`
  value at line 5 rather than in a header comment. §4.7 now states the same three disjuncts, which are a
  generation marker in the file header, a generation declaration in top-level document metadata where the
  format carries no comment syntax, and membership in the output set of a producer §4.6 names, so the
  governing sentence and the two sentences that quote it describe one set.

- **The proto no-drift test's missing-toolchain path had no producer for the `UNVERIFIED` status.** The
  proposal staged only the aggregation half, which is one constant and one branch in
  `cmd/lenny-test/verdict.go`, while nothing in the tree can emit the status for a tier-0 Go test.
  `runStaticTier` composes tier 0 as a table of checks returning a Go error
  (`cmd/lenny-test/cmd_run.go` lines 717 through 720) and the composing loop collapses each to `"fail"` or
  `"pass"` (lines 747 through 753), with `"skipped"` reachable only when `go` is absent (line 473);
  `recordTier` reclassifies only a failing tier into `inconclusive` and only on a fixed
  infrastructure-pattern list that names no toolchain absence (`cmd/lenny-test/verdict.go` lines 235
  through 237 and 279 through 309). The test therefore had only the two outcomes TEST-1 rules out, which
  are a hard failure on a tree with no drift and an early return that reproduces the fail-open behavior of
  the shell script it replaces. proposal 0065's `UNVERIFIED` bullet now also lands the producer: a per-check
  status in the tier-0 check table, a sentinel line the `go test ./tests/tier0_static/...` check parses
  out of the test output, and a composing loop that propagates the status instead of collapsing it.
  TEST-1 gains the producer's cases over `cmd/lenny-test/cmd_run.go` beside the aggregation cases, and the
  proto no-drift case names the route it depends on.

### Pass 32 (2026-07-29, automated)

- **A citation wrapped across two comment lines matched neither the retired citation form nor the residual
  predicate, so a large live population survived SPEC-4's zero-count exit criterion.** §4.6 stated the form
  as one contiguous string and stated that the matchers do not read a comment dialect, and §4.7's residual
  predicate required the section sigil to be adjacent to the line-number token. A citation written across
  two comment lines separates the two by a newline plus the carrier's comment marker, so on a line-oriented
  scan the first line carries a sigil with no line number and the second carries a line number with no
  sigil, and neither the enumerated form nor the residual caught it. Measured over `git ls-files` under
  §4.6's read exclusion at commit `668deca8`, the population is 768 occurrences across 436 files, of which
  45 across 32 files cite §4.7, §4.8, §4.9, §15.4, or §15.5, which are the ranges the SPEC-3 reduction
  shifts, so those pointers would have gone silently wrong while SPEC-4 reported every count zero. §4.6 now
  states a continuation join that consumes the newline together with the comment marker and the surrounding
  whitespace, covering the wrap between the section reference and the keyword, the wrap between the keyword
  and its first member, and the wrap inside a member list, with worked cases in the `//` and `#` dialects;
  the sentence that rules out comment-dialect matching names the join as its one exception; §4.7's
  adjacency tolerates the same join, so the residual still catches a wrapped spelling the enumerated form
  misses; the §3.4 class row carries the wrapped spelling; and TEST-1 gains a line-pass case per carrier
  dialect and per wrap position, with `pkg/gateway/sessionserver/messages.go` lines 156 and 157 as the
  worked case, together with a ratchet case for a wrapped citation at count zero. §4.6 also states that the
  resolver and ratchet baselines proposal 0065 seeds are measured with the join applied.

- **`TESTING.md` §7 closes a second enum that the `UNVERIFIED` producer widens, and it was not staged.**
  TOOL-1 amended the §7 verdict sentence (`TESTING.md` line 521) and the §21.3 infrastructure-failure
  sentence (line 2572), but the producer also emits a new `tiers.<name>.status` value, which TEST-1 names
  as `unverified` and which `cmd/lenny-test/verdict.go` line 68 serializes as `status`. `TESTING.md` line
  522, in the same §7 field-semantics list, states that `tiers.<name>.status` is one of `pass`, `fail`,
  `skipped`, and `not-selected`, so after the proposal's edits that sentence would state a closed set for
  the field the new state lands on, and no gate reads a prose enumeration. proposal 0065's `UNVERIFIED`
  bullet now amends line 522 as well, the §3.4 class row is restated as every §7 field-semantics sentence
  closing an enum the producer widens plus the §21.3 sentence rather than as two named sentences, proposal 0065's
  Target names both enums, and §11 lists all three `TESTING.md` sentences with their lines.

### Pass 33 (2026-07-29, automated)

- **The change-graph-coverage and skip-reason residual entries were declared permanent on a premise their
  own downward-ratcheted baselines contradict, so ordinary coverage work would have turned tier 0 red.**
  §4.7 keyed the lifetime of an `in-class` entry on whether a `scripts/specshift` pass runs over the class,
  and grouped the change-graph coverage and skip-reason classes with the generated-artifact class as
  classes whose entries are permanent because the member keeps matching the predicate for as long as it
  exists. That premise is false for the first two. Both classes are defined by a baseline TOOL-1 rewrites
  downward precisely because members leave the class: a tracked path that gains a glob key in
  `tests/change-graph.json` stops matching the coverage predicate, which is the reverse of
  `validateChangeGraphFileExistence` (`cmd/lenny-test/cmd_validate.go` lines 282 through 315), and a skip
  whose reason is rewritten to open with one of the categories
  (`cmd/lenny-test/cmd_validate.go` lines 853 through 865) stops matching the skip-reason predicate. The
  entry left behind is then a dead entry, and TEST-1's case for an `in-class` entry whose member no longer
  matches the predicate fails tier 0 on it, with no removal rule reaching it because §4.7's removal rule
  fired only when a pass handled the member. §4.7 now keys the lifetime on whether a member can leave the
  class rather than on whether a pass runs, names the change-graph coverage and skip-reason classes as
  classes whose members leave by the remediation the gate exists to drive, states that the entry is removed
  in the same run on the same downward rewrite the class's own baseline performs, and keeps the permanent
  branch for the generated-artifact class alone. proposal 0065's seeding sentence, the §6 route-to-green sentence,
  and TEST-1's residual cases carry the same predicate.

### Pass 34 (2026-07-29, automated)

- **The skip-reason baseline's downward rewrite had no case, unlike the two sibling baselines governed by
  the same rule.** proposal 0065 states that `tests/registers/skip-reasons.yaml` is rewritten downward only, and
  §4.7 rests the removal of an `in-class` residual entry on that same-run downward rewrite. TEST-1's
  skip-reason list pinned only the upward guard, so a classifier whose baseline never shrinks would have
  passed every listed case, leaving a remediated skip listed and its exemption available again. The two
  sibling baselines each carry the removal case, which are the change-graph check and the line-citation
  ratchet. TEST-1 now adds the matching pair: a site whose reason is rewritten to open with one of the
  categories is removed from the baseline in the same run and the baseline is rewritten downward, and a
  site removed by that rewrite whose reason returns to free text fails.
- **The residual gate had no stated read domain, so as written it reported the `BUILD-GAPS.md` and
  `TEST-GAPS.md` populations that §5 and §11 promise no gate reports.** §4.7, TOOL-1, and TEST-1 all
  described the residual computation without naming what the scan reads, while every other reader in the
  proposal carries an explicit domain: §4.6 states the resolver's and the ratchet's read exclusion and N3
  states the exclusion list the passes and the two naming gates share. Applied to the whole tree the scan
  would have reported the thousands of citation sites in the two audit records, this proposal's own quoted
  specimens under `proposals/`, and the member text held inside each residual register, and §4.7 admits no
  closure route but registration, so the check could not have landed green in TOOL-1. §4.7 now states the
  domain as the tracked tree less the read exclusion §4.6 states, less the further root-level records N3
  names for the reserved-phrase and identifier classes, and less every residual register and every pass or
  baseline register a class's predicate would match as tree content, with the non-convergent-seeding
  argument §4.6 already makes for the citation registers. proposal 0065 restates the same domain, TEST-1 adds the
  register self-reference case and the read-exclusion case, and the §5 row and §11 now name the residual
  scan alongside the resolver and the ratchet.

### Pass 35 (2026-07-29, hand-authored after the failed application)

The application attempt following the withdrawn sign-off did not converge, and the review of that failure
found two classes of defect that all 34 automated passes above had missed. Both classes were structural
rather than incidental. The first is that no lens ever attempted to APPLY the proposal in its stated order,
so an edit that cannot be carried out reads as sound prose. The second is that every lens checked the
proposal against the repository and none against the remediation plan, so a deliverable the plan assigns to
a claimed step and the proposal never mentions was invisible to all of them. The convergence record above
certifies internal consistency and accuracy against the tree, and neither of those properties implies
applicability or plan coverage.

- **Two staged edits could not be applied at all.** SPEC-1 stages `spec/README.md` rows for §28.1 through
  §28.4 and SPEC-3 stages rows for §28.5 through §28.7 and the card headings, and every such row needs link
  text plus an anchor that resolves. The proposal stated no heading titles for §28 anywhere, and SPEC-3 is
  the sub-step that creates the file, so applying SPEC-1 meant inventing titles and guessing their slugs.
  §4.8 now fixes the title and the derived anchor of every §28 and §29 heading in one table, both sub-steps
  take their link text and anchors from it, and SPEC-1 records that its rows precede their target file
  deliberately along with the `pending-implementation` exceptions entries that keep the walker green until
  SPEC-3 lands.
- **The §28.5 boundary order was unstated while the proposal's own example depended on it.** §4.3 said only
  "one subsection per boundary value" and fixed no order, yet §2 uses `§28.5.2 CH-ADAPTEREVENTS` as the
  worked citable handle. §4.8 now fixes the order, §4.3 points at it, and §4.8 states the check that the
  order landed as written: `CH-ADAPTEREVENTS` carries the pod-to-gateway boundary, so a §28.5.2 that is not
  pod-to-gateway means the order drifted.
- **§28.8 was absent.** The failure and degradation matrix is one of the §28 subsections the plan requires,
  and the plan flags it as the only one with no owner anywhere in the tree, so nothing else supplies it.
  §4.8 states what it holds and why it is authored rather than relocated, SPEC-3 writes it, and TEST-1 adds
  the tier-0 bijection against the §28.3 register.
- **The §29 off-holder matrix was absent.** SPEC-3 now states it: one row per operational request the
  gateway can make of a pod, giving the outcome when the receiving replica is not the replica holding that
  pod's control stream. That case is reachable under the co-located binding and no section states it today.
- **The proto field additions were absent, which lost the single-regeneration property.** SPEC-2 now adds
  `coordination_generation` to every operational gateway-to-pod request message, a slot identifier to the
  four per-slot request messages, and `ResumeRequest.slot_id`, all inside the one `make generate-proto` run
  this sub-step already pays for, each with an `UNWIRED` claim row naming the later step that reads it.
- **The breaking-change gate had no recorded disposition.** SPEC-2 renames an RPC and two message types in
  `lenny.adapter.v1`, and `buf breaking` hard-fails on `main` with `ignore_unstable_packages` giving no
  exemption for a stable version suffix, so the sub-step could not have landed green. SPEC-2 now records the
  disposition as enumerated exception entries with expiries, and states why advancing the baseline ref was
  rejected.
- **The two incomplete artifact enumerations outside `spec/15` were not superseded.** SPEC-3 now replaces
  the lists at `spec/18` line 92 and `spec/24` line 114 with a reference to §28.7. The `spec/24` sentence is
  the one that gates a third-party adapter to `active`, and its list omits the runtime-ops events schema, so
  until it is corrected the shipped compliance suite cannot validate `CH-RUNTIMEOPS`.
- **The numbered-heading insertion was absent, which made the citation retirement a downgrade.** Retiring a
  line citation into a 300-line section leaves a whole-section anchor. SPEC-4 now inserts §4.4.1 through
  §4.4.5, §4.7.1 through §4.7.11, §10.1.1 through §10.1.8, and §13.2.1 through §13.2.7, ordered before the
  citation pass inside the same exclusive change so the shift is absorbed by the pass already running.
- **The reference document was never frozen.** SPEC-4 now adds the point-in-time header to
  `gateway-runtime-comms.md` and TEST-1 adds the tier-11 test pinning the header and the body, so the
  document §28 derives from does not survive as a competing description of the same contract.
- **Several reductions deleted content rather than relocating it.** The application attempt removed the
  §15.4.1 and §15.4.2 material, the `CH-RUNTIMEOPS` message-schema table, and the §4.7 platform MCP tool
  set and operating rules without staging them into `spec/28`. SPEC-3 now states the rule that authorizes a
  reduction only when both legs land in the same change, and that a source statement with no destination is
  carved out and left in place rather than removed.

### Pass 36 (2026-07-29, automated)

- **SPEC-2 staged a correction to two plan rows the plan no longer contains, anchored to line numbers
  holding other text.** Commit `cc7614b8` already reconciled the plan with this proposal's rename: the R1b
  scope table now reads `rpc AdapterEvents(stream AdapterEventsRequest) returns (stream
  AdapterEventsResponse)` at `gateway-runtime-comms-remediation.md` line 425 and
  `pkg/adapter/adapterevents.go` at line 430, and the provenance note at line 263 states that
  `CH-EVENTSTREAM` appears nowhere in the plan. Line 418 holds a quotation of
  `.claude/rules/code-best-practices.md` and line 423 holds the scope table's header row, so the staged
  edit had no resolvable anchor and would have rewritten unrelated text. The staged correction is dropped
  from SPEC-2's Target, from the §2 `CH-ADAPTEREVENTS` bullet, and from §11, and SPEC-2 now justifies
  stating the `CH-ADAPTEREVENTS` carrier spellings as N3 and N4 conformance rather than as a correction of
  the plan. The Pass 29 record is left as written, because it records what the plan held at the time.
- **The gates covering the Pass 35 deliverables had no landing sub-step, and the tier-0 one contradicted
  the gate-integrity meta-gate.** §3.5 requires exactly one landing sub-step per gate, and the meta-gate's
  fixed list has to name exactly the tier-0 gates this proposal registers while being green at SPEC-4's
  exit. The §28.8 matrix completeness check now lands in SPEC-3, which writes §28.8 against the §28.3
  register SPEC-1 already staged, so it is registered before SPEC-4 and the meta-gate names it. The
  artifact-register supersession check lands in SPEC-3, which replaces the `spec/18` and `spec/24`
  enumerations it forbids. The reference-document freeze check lands in SPEC-4 with the freeze header it
  reads. §3.5 step 4, SPEC-3, SPEC-4's meta-gate paragraph, TEST-1's gate-to-sub-step list, TEST-1's
  meta-gate case, and TEST-1's paragraph on these deliverables all carry the same assignment, and SPEC-4
  now enumerates the tier-11 successor-pointer check, the tier-11 artifact-register supersession check, the
  tier-11 reference-document freeze check, and the tier-3 assertion as the gates outside the meta-gate's
  domain.
- **The §3.4 row-3 anchor-map gate had no owner, no landing sub-step, and no cases.** Row 3 named a tier-0
  check over `tests/spec-anchor-moves.json` that proposal 0065 does not build, TEST-1 does not land, and no
  sub-step owns. The register also does not exist before SPEC-3 and is emptied by SPEC-4, so a gate over
  its entries would be vacuous at the proposal's exit. Rather than add a gate for it, row 3's proof is
  restated as the mechanisms that already land: the anchor pass aborts non-zero before any write on a
  citation naming a retired anchor with no map entry and on a map entry whose successor heading does not
  exist, with those cases already stated in TEST-1, and the anchor class's residual check fails tier 0 on a
  retired anchor left in the tree with no residual-register entry. TEST-1's paragraph on the anchor pass's
  cases no longer refers to a row-3 gate.
- **The §28.8 gate's landing justification claimed SPEC-3 writes the §28.3 register.** SPEC-1's Target
  covers §28.1 through §28.4 and its change text writes the three registers, and SPEC-3's Target is limited
  to §28.5 through §28.8, so the earlier sentence asserted a duplicate staging of the channel register that
  §3.5's one-landing-sub-step rule forbids. SPEC-3 and this record now state the correct premise: §28.8 is
  written in SPEC-3 against the §28.3 register SPEC-1 already staged, so the bijection first holds at
  SPEC-3's exit.
- **The artifact-register supersession check had no route to green at SPEC-3, because SPEC-3 leaves the
  §15.4 wire-artifact pointer standing.** That pointer names `schemas/lenny-adapter.proto`,
  `schemas/lenny-adapter-jsonl.schema.json`, and `schemas/messagepart.schema.json`, and the register §28.7
  derives from the schemas directory carries more than those, so a predicate reading every enumeration
  outside §28.7 was red on introduction. The predicate now covers enumerations that stand for the register's
  artifact set and names the §15.4 pointer as exempt, because that pointer enumerates the artifacts §15.4's
  prose documents and, after SPEC-2's correction to line 1463, states where the runtime-operations frames
  are schematized. SPEC-3's carve-out paragraph, SPEC-3's route-to-green paragraph, and TEST-1's predicate
  and cases carry the same exemption.

### Pass 37 (2026-07-29, automated)

- **The §15.4.2 reduction had no destination staged, so the both-legs rule left it unauthorized and the
  anchor map had no successor to write.** SPEC-3 sent `#### 15.4.2 RPC Lifecycle State Machine` into the
  reduction while naming no `spec/28` heading that receives it, and the §4.8 heading table lands none that
  could: §28.5 groups its cards over the closed boundary set §28.2 fixes, and none of those owns an adapter
  RPC implementation obligation. Applying the reduction as written would have deleted the only statement in
  `spec/` of the `AdapterInit` and `AdapterInitAck` version negotiation, of the
  `PROTOCOL_VERSION_INCOMPATIBLE` stream close, and of the current protocol version, which are in the `INIT`
  row at `spec/15_external-api-surface.md` line 2081. §15.4.2 is now carved out on the rule §15.4.3 through
  §15.4.6 get, rather than given a new §28 heading, because a heading outside the boundary set would widen
  §28 into the adapter implementation contract. It keeps its heading and its
  `1542-rpc-lifecycle-state-machine` anchor, the anchor map carries no entry for it, and the one inbound
  markdown link at `spec/15_external-api-surface.md` line 2395 together with the bare `§15.4.2`-form
  citations under `pkg/` and `sdks/` stay untouched. §3.1, SPEC-3's reduction boundary, the retired-anchor
  set, SPEC-4's Target sentence, SPEC-4's link population (36 links into `1541-adapterbinary-protocol`, 24
  of them same-page, of which the pass rewrites 18), the four links SPEC-4 rewrites inside §15.4.3 through
  §15.4.6 at lines 2163, 2164, 2394, and 2441, and §11's citation-population bullet all carry the narrowed
  reduction. The reserved bare noun phrase in the `ACTIVE` row at line 2083 is still rewritten in place by
  SPEC-1's name pass.
- **The proposal contradicted itself on which sub-step creates `spec/28_communication-channels.md`.** §4.8
  and SPEC-1 said SPEC-3 creates the file while SPEC-1's Target, its change text, and §3.5 step 2 said
  SPEC-1 writes §28.1 through §28.4 into it, which left the tree state at SPEC-1's exit and the whole
  `pending-implementation` route undecidable. It is settled on SPEC-1, the reading SPEC-2's identifier pass
  and the §28.8 gate's landing argument both depend on: SPEC-1 creates the file with §28.1 through §28.4, so
  its `spec/README.md` rows resolve at its own exit and no row in this proposal precedes its target file.
  Each of those headings takes a `tests/spec-map.json` key on the rule SPEC-4 already states for a heading
  written in the same sub-step, and takes a `pending-implementation` exceptions entry blocked on TEST-1 only
  where the tests a key would name land with the gate cases. SPEC-3's instruction to retire entries for
  §28.1 through §28.4 is dropped, and §3.5 steps 2 and 4 name which sections each creates.
- **SPEC-1's name pass rewrote `schemas/lenny-adapter.proto` comments with no regeneration, leaving proposal 0065's
  proto no-drift test red at its exit.** `protoc` copies those comments verbatim into the committed stubs, so
  the six rewritten `.proto` sites at lines 31, 137, 138, 1219, 1578, and 1589 are mirrored in
  `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` and `pkg/proto/adapter/v1/lenny-adapter.pb.go`, and TOOL-1
  verifies the test green against the unmodified tree. SPEC-1 now lists `pkg/proto/` in its Target as
  regenerated rather than rewritten, runs `make generate-proto` after the name pass, and takes the tier-0
  proto no-drift test as an exit criterion, on the rule §4.6 states for a producer whose source a pass
  writes. SPEC-2's sentence claiming the target is reached by no other sub-step is corrected to name SPEC-1,
  SPEC-2, SPEC-3, and SPEC-4.
- **SPEC-4's by-line enumeration still sent the anchor pass at the link the §15.4.2 carve-out preserves.**
  The one site that names the links inside §15.4.3 through §15.4.6 by line read "five of them target the two
  retired numbered anchors, at lines 2163, 2164, 2394, 2395, and 2441", which contradicted SPEC-3's
  exclusion of line 2395, SPEC-4's own link population of four, and the retired-anchor set that names
  `1541-adapterbinary-protocol` alone. Applying it would have rewritten the single inbound link to
  `1542-rpc-lifecycle-state-machine`, for which the anchor map deliberately carries no entry, so the pass
  would have aborted on TEST-1's retired-anchor-with-no-entry case. The enumeration now names the four links
  into `1541-adapterbinary-protocol` at lines 2163, 2164, 2394, and 2441 and records line 2395 as untouched
  on the carve-out beside line 2165. SPEC-4's `spec/04_system-components.md` sentence names the one retired
  anchor rather than "either retired anchor".
- **TEST-1 and TOOL-1 still excluded SPEC-1 from the proto no-drift exit criterion and from the set of
  passes that rewrite `.proto` source.** Both enumerations predate the correction above that made SPEC-1 run
  `make generate-proto` and take the tier-0 test as an exit criterion, so the proposal gave two answers to
  which sub-steps own that gate. TEST-1's case justification and proposal 0065's two sentences now name SPEC-1,
  SPEC-2, SPEC-3, and SPEC-4, matching SPEC-2's corrected sentence.

### Pass 38 (2026-07-29, automated)

- **Superseding the `spec/18` line 92 artifact list made a Phase 1 deliverable claim later-phase
  artifacts.** Line 92 sits under the Phase 1 deliverables heading at `spec/18_build-sequence.md` line 87
  and states what Phase 1 delivers, so its omission of the runtime-ops events schema is phase scoping rather
  than drift, because line 165 makes that same artifact a Phase 2.8 deliverable. Pointing line 92 at a
  register derived from the schemas directory would have made Phase 1 responsible for
  `schemas/runtime-ops-events.schema.json`, `schemas/lenny-tokenservice.proto`, and
  `schemas/lenny-interceptor.proto`, which no earlier phase delivers, and would have stated one artifact as
  both a Phase 1 and a Phase 2.8 deliverable. `spec/18` is dropped from the supersession set, the `spec/24`
  line 114 correction that gates a third-party adapter to `active` is kept, and the supersession check's
  predicate now names the `spec/18` phase-deliverable lists as exempt beside the §15.4 wire-artifact
  pointer, so the check's domain no longer contains an enumeration with no disposition. §3.1, SPEC-3's
  Target, SPEC-3's supersession paragraph, SPEC-3's route-to-green paragraph, TEST-1's predicate and cases,
  and §11 all carry the narrowed supersession.
- **The recorded `buf breaking` disposition was inert, because nothing reads the exception register and no
  edit to the check was staged.** The check at `cmd/lenny-test/cmd_run.go` lines 511 and 527 through 535
  computes its verdict from buf's exit status and the current branch name alone, so entries in
  `tests/registers/` could not change its outcome. SPEC-2 now records the actual disposition, which is that
  the rename needs no exception: off `main` the check downgrades its findings to advisory itself, and on
  `main` the comparison baseline is branch `main`, so once the rename is on `main` the tree and the baseline
  both carry it and buf reports no finding. The register entries and their expiry are dropped, SPEC-2 states
  why an inert register entry was rejected alongside the already-rejected baseline-ref move, and TEST-1 no
  longer claims a validator case for entries the proposal no longer adds.

### Pass 39 (2026-07-29, automated)

- **§4.8 attributed the §28.5 subsection order to §3.2, which fixes no boundary order, and attributed the
  worked handle to §4.3, which does not contain it.** §3.2 names the two renamed channels and states that
  the participants, the dial direction, and the authority direction are read from the §28.3 register rows.
  It states nothing about boundaries and fixes neither the closed boundary set nor an order over it, so an
  implementor following the citation found no order there. Everywhere else the proposal attributes the closed
  boundary set to §28.2 and the subsection order to the §4.8 table itself, and §28.2's enumeration begins
  with `intra-pod` while §28.5.1 is Gateway-to-pod, so the discarded attribution also invited a reader to
  reconcile two orders where the table alone is authoritative. §4.8 now states that the order is fixed by its
  own table over the closed boundary set §28.2 states. The worked handle `§28.5.2 CH-ADAPTEREVENTS` appears
  in §2, where the anchor-citation convention is introduced, and §4.3 states only that the citable handle is
  the card heading plus the identifier, so the sentence now cites §2.
- **The Pass 35 record named a third home for the same worked handle.** The Pass 35 bullet on the §28.5
  boundary order stated that §4.6 uses `§28.5.2 CH-ADAPTEREVENTS` as the worked citable handle. §4.6 is the
  line-citation ratchet and contains no occurrence of the handle, so the audit record contradicted both §4.8
  and the Pass 39 correction above. The bullet now names §2, which is where the handle appears, so §4.8 and
  the review record agree on one home.

### Pass 40 (2026-07-29, automated)

- **The falsified-sentence class omitted `schemas/README.md`, whose artifact table points two rows at §15.4
  for material the §15.4.1 reduction moves.** The table's `lenny-adapter-jsonl.schema.json` row and its
  `messagepart.schema.json` row both name §15.4 as the owning section, for the stdin/stdout JSONL messages
  defined at `spec/15_external-api-surface.md` line 1836 with its `#####` children and for the internal
  `MessagePart` format block at line 1515, all of which move to §28. No pass reached those rows: they carry
  no line citation, their links carry no fragment, and they carry neither reserved word as a bare noun
  phrase, and the falsified-sentence class has no residual register, so applying the proposal would have left
  a tracked artifact index asserting an ownership §15.4 gave up. `schemas/README.md` is now the ninth member
  of the class, named in SPEC-3's Target, in the class enumeration, in the §3.4 class row, and in §11, and
  the class population reads nine.
- **The artifact-register supersession check had no stated read domain, and `schemas/README.md` carried an
  unexempted subset enumeration inside it.** The README opens on the directory's wire-contract artifacts and
  tables all but `schemas/lifecycle-events.schema.json`, which SPEC-2 renames to
  `schemas/runtime-ops-events.schema.json`, and `schemas/lenny-tokenservice.proto`, so it stands for the
  register's artifact set while naming a strict subset of it, and neither stated exemption reached it. SPEC-3
  and TEST-1 now state the check's read domain, which is the tracked markdown under `spec/`, `docs/`, and
  `schemas/` together with the tracked root-level markdown documents N3 leaves in scope, the markdown subset
  of the walk the naming lint reads. SPEC-3 replaces the README's table with a reference to §28.7 on the same rule the
  `spec/24` line 114 sentence gets, which is what gives the check a route to green in its landing sub-step and
  what corrects both falsified rows in one edit. Adding the two missing rows was rejected, because it would
  keep a second per-artifact enumeration of a register §28 derives from the schemas directory. §2's overview
  sentence, SPEC-3's supersession paragraph, SPEC-3's route-to-green paragraph, TEST-1's predicate and cases,
  and §11 all carry the two-enumeration supersession, and §28.7's staged description now states that each row
  names the artifact, the surface it schematizes, and the heading that owns that surface, so the reference
  carries what the table carried.
- **The reserved-phrase matcher was specified without the continuation join the citation matcher gets, so a
  wrapped occurrence was written by no pass and read by no gate.** The tree carries the phrase wrapped across
  two consecutive comment lines inside the domain N3 names, including `schemas/lenny-adapter.proto` lines
  1219 and 1220, whose comments `protoc` mirrors into `pkg/proto/adapter/v1/lenny-adapter.pb.go` lines 4623
  and 4624, and Go doc comments at `pkg/adapter/mcpruntime.go` lines 238 and 239,
  `pkg/adapter/usage.go` lines 237 and 238, `pkg/embedded/stack/catalog.go` lines 193 and 194,
  `pkg/gateway/session/executor/subprocess.go` lines 34 and 35, and
  `sdks/runtime/go/runtime/lifecycle_test.go` lines 17 and 18 together with lines 102 and 103. A line-oriented
  matcher writes none of them, the naming lint reads none of them, and SPEC-1's exit criterion returned zero
  while the collision survived in a shipped wire artifact and its generated stubs, which is the hole §4.6
  closes for citations. N3 and SPEC-1's name-pass description now apply the comment-marker continuation join
  §4.6 states before either banned spelling, with the worked sites and the quoted specimen carried in SPEC-1
  rather than in N3 because §28.1 sits inside the lint's own domain, SPEC-1's exit criterion and the register's entry criterion are
  restated as a search under the join, SPEC-1's proto site enumeration records that line 1219 continues onto
  line 1220 and that its mirror continues onto line 4624, the §5 row on the lint's domain names the shared
  join, and TEST-1 adds a naming-lint case that a wrapped bare reserved noun phrase fails, with
  `schemas/lenny-adapter.proto` lines 1219 and 1220 as the worked case.
- **Correction to this pass: the stated read domain admitted a third undisposed subset enumeration, so the
  supersession check still had no route to green at SPEC-3.** Naming `docs/` in the domain pulls in the
  "Canonical artifacts" section of `docs/reference/adapter-contract.md`, whose lead sentence at line 654
  claims the adapter protocol is defined by three published artifacts and whose table at line 658 names the
  same three the `spec/24` line 114 sentence names, omitting the runtime-ops events schema even though its
  JSONL row claims to cover lifecycle frames. The schema list at `docs/runtime-author-guide/publishing.md`
  line 367 and the Phase 1 wire-contract artifact list at `TESTING.md` line 1449 sit in the domain on the
  same footing, and SPEC-3 stages no edit to any of the three. The two-member exempt list reached none of
  them. SPEC-3's route-to-green paragraph and TEST-1's restatement now state the exemption as a rule: an
  enumeration is exempt when it names the artifacts the enumerating page's own prose documents, which is the
  ground the §15.4 wire-artifact pointer already had, or when it names what a build phase delivers. The
  three sites are named against the ground each falls under, so the check is red on no site in its domain at
  the sub-step that lands it, and TEST-1's non-happy case for a further page in the domain now reads on an
  enumeration that falls under neither exemption.
- **Correction to this pass: the domain sentence claimed the check's walk is the naming lint's walk, which N3
  states is broader.** N3 names `spec/`, `docs/`, `schemas/`, Go doc comments in tracked Go files, and the
  root-level markdown left in scope, and SPEC-1 measures that domain over non-markdown carriers, so the
  lint's walk is larger than tracked markdown and the appositive gave one gate two domains. SPEC-3, TEST-1,
  and this pass's own bullet above now read "the markdown subset of the walk the naming lint reads", leaving
  the enumerated domain as the operative definition.

### Pass 41 (2026-07-29, automated)

- **§4.8's anchor-derivation rule was attributed to `spec/README.md` and contradicted two of its rows.** The
  rule read the section number with its dots removed and each run of non-alphanumeric characters replaced by
  a single hyphen, which computes `#44-event-checkpoint-store` for `spec/README.md` line 18 and
  `#93-connector-definition-and-oauth-oidc` for line 53, while the tree carries
  `#44-event--checkpoint-store` and `#93-connector-definition-and-oauthoidc`. §4.8 is the proposal's only
  statement of the algorithm and the heading walker's second half is that a `spec/README.md` row's anchor
  resolves, so a walker built from the stated rule would be red at SPEC-1's exit on two correct pre-existing
  index rows that this proposal stages no correction, residual entry, or exemption for. The same rule cannot
  produce `adapter--runtime-protocol-intra-pod`, `protocol-reference--message-schemas`, or
  `messageenvelope--unified-message-format`, three anchors the proposal itself writes. §4.8 now states the
  algorithm the tree's anchors follow, which is to lowercase the heading, delete every character that is not
  a letter, a digit, a space, a hyphen, or an underscore, and replace each remaining space with one hyphen,
  and it names `spec/README.md` lines 18, 53, and 147 as the three cases the derivation and the walker's
  resolution step both reproduce.
- **The falsified-sentence class was closed at nine while nine further `spec/` and `docs/` sentences
  attribute the relocated §4.7 material to §4.7.** The §4.7 reduction moves Part A, Part B, and the
  message-schema table at `spec/04_system-components.md` lines 715 through 731, which is the sole normative
  definition of the intra-pod frame vocabulary. `spec/04_system-components.md` line 241,
  `spec/05_runtime-registry-and-pool-model.md` lines 41 and 47, `spec/09_mcp-integration.md` line 8,
  `spec/11_policy-and-controls.md` line 49, `spec/15_external-api-surface.md` lines 2115, 2116, and 2435, and
  `docs/runbooks/credential-rotation-failure.md` lines 11 and 19 each cite §4.7 as the owner of exactly that
  material, and none of them was reachable by a pass: §4.7 keeps its anchor, none carries a line citation,
  and the name pass rewrites the reserved phrase while leaving the section pointer wrong, which is the
  structure the §15.4.5 item 7 member already recorded. Three of them are security- or recovery-normative,
  which are the direct-mode token accounting path, the `lifecycle_support` handshake the admission check
  compares against, and the only statement of the mechanism producing consistent checkpoints under gVisor
  and Kata. All nine are now members of the class, staged as hand-authored corrections in the same change as
  the reduction, and the class population, SPEC-3's Target, the §3.4 class row, the §15.4.3 through §15.4.6
  ownership sentence, and §11 all carry them. The class keeps its enumerated form with review plus the
  tier-11 successor-pointer check as its proof, rather than gaining a broad predicate and a residual
  register, because a predicate over §4.7 citations would have to be seeded with every surviving-true §4.7
  reference in the tree as an explicit exclusion, which is the same enumeration with a gate that has no
  route to green in its landing sub-step.
- **The `MessageEnvelope` sentence the §15.4.1 carve-out rests on points at a block the same reduction
  moves.** `spec/15_external-api-surface.md` line 1710 closes "see Protocol Reference below", and the
  Protocol Reference block at line 1836 moves to §28, so the surviving sentence directs a reader to a block
  that is no longer on the page. No pass reaches it, because it carries no line citation, it is bare prose
  rather than a markdown link, and it carries neither reserved phrase. It is now a member of the
  falsified-sentence class, with its closing clause hand-corrected to name the §28.5 card that owns the
  adapter-to-binary message schemas, and the carve-out paragraph that cites the sentence records the
  correction alongside it.
- **Replacing the `spec/24` compliance-suite enumeration with a reference to §28.7 would have made a
  validation gate assert against artifacts a runtime adapter never emits.** §28.7 is derived mechanically
  from `schemas/`, which also holds the interceptor SPI proto, the token-service proto,
  `workspaceplan-v1.json`, `ocsf-mapping.yaml`, and `audit-events/v1.json`, while the suite the sentence
  gates validates against two schema files (`cmd/lenny-compliance/schemavalidate.go` lines 31 through 33).
  The reference would either over-specify the `pending_validation` to `active` gate or leave its asserted set
  undecidable, which is the reasoning the proposal already accepts to withdraw the identical `spec/18` line
  92 edit. SPEC-3 now adds `schemas/runtime-ops-events.schema.json` to the enumeration and leaves it in
  place, the supersession check gains a third exemption ground for an enumeration naming the artifact subset
  a named consumer asserts against, and `docs/reference/adapter-contract.md` lines 654 and 658 take the same
  correction in the same change, so the normative gating sentence and the published reference page state one
  artifact set. §3.1, SPEC-3's Target and supersession paragraphs, TEST-1's predicate and cases, and §11
  carry the change, and TEST-1's reject case no longer names `spec/24`.
- **§29's off-holder matrix was keyed by gateway-to-pod RPC, which leaves no row for the off-holder outcomes
  that corrupt state or lose messages.** The plan states the matrix per route
  (`gateway-runtime-comms-remediation.md` lines 586 and 587, and lines 1130 through 1133) and states that no
  §28 card owns the client-to-gateway REST surface. The silent-loss case, the `suspended` to `running` row
  flip with no pod resumed, and the two SSE cases reach no pod at all, so a per-RPC key cannot express them,
  and one route carries three outcomes selected by session state. §3.1 and SPEC-3 now key the matrix by
  session-scoped client route, with one row per route and session state where a route has more than one
  outcome, name the row domain as the session REST surface plus the three other entry points that reach the
  same executor send, and carry any per-RPC statement as a matrix column or as §28.5.1 card content.
- **The metric half of N4 was deferred with neither the §28.1 statement nor the claim-register row the plan
  assigns to this step.** §4.1 stated N4 binding the metric label value with no deferral clause, so §28.1
  would land binding the metric namespace while `pkg/adapter/metrics.go` lines 71 and 79 keep the retired
  spelling, and the deferral rested on a register row no sub-step authored: SPEC-3's seeding instruction
  reads the reference document's status tables, which carry no row for the adapter metrics endpoint or the
  rename. §4.1 N4 and SPEC-1's change text now state the deferral, naming R12 as the step that discharges it
  and the two metric names as the deferred population, and SPEC-3 writes the row explicitly with status
  `ABSENT` and `deferral_id` R12, alongside the podspec mTLS row with `deferral_id` R14, so both satisfy the
  validator's rule that a row which is not `WIRED` names a step the plan states. "Metric label renames" reads
  "metric renames" in SPEC-1's out-of-scope paragraph and in §7, since the deferred population is two metric
  names.
- **The §15.4.2 carve-out list of same-page links missed `spec/15_external-api-surface.md` line 2733.** That
  link cites the retiring `1541-adapterbinary-protocol` anchor for the protocol version negotiation, which
  the carve-out keeps in `spec/15` at the `INIT` row of §15.4.2 and which §15.4.1 states nowhere. Left out of
  the hand-corrected population, SPEC-4's anchor pass would rewrite it from the map's single successor and
  send a normative §15.7 SDK-versioning sentence to a §28.5 card that states no version negotiation, and the
  fragment-link gate would pass it, because that gate reads resolution rather than destination. Line 2733 is
  now the seventh member of the same-page hand-corrected population, retargeted to
  `#1542-rpc-lifecycle-state-machine` with its `§15.4.1` label corrected to `§15.4.2`, and SPEC-4's link
  arithmetic reads seven hand-corrected and 17 rewritten by the pass, with the §3.4 class row and the
  §15.4.2 carve-out paragraph carrying the same count.
- **Correction to this pass: the N4 deferral cited the second deferred metric one line above its name.**
  The six sites this pass wrote or rewrote read "`pkg/adapter/metrics.go` lines 71 and 78". Line 71 carries
  `lenny_adapter_control_events_total`, but line 78 is the `mustCounterVec` call that opens the second
  counter's options literal and `lenny_adapter_control_events_dropped_total` is on line 79, so the
  `ABSENT`/`deferral_id` R12 claim-register row named a line with no metric name on it and disagreed with the
  plan, which cites the pair as `pkg/adapter/metrics.go:71` and `:79`
  (`gateway-runtime-comms-remediation.md` lines 360 and 361). All six sites now read "lines 71 and 79":
  §4.1 N4, SPEC-1's change text, SPEC-1's out-of-scope paragraph, SPEC-3's claim-register seeding
  instruction, §7's non-goal, and the bullet above.

### Pass 42 (2026-07-29, automated)

- **The falsified-sentence class omitted the six spelled-out `Section 15.4.1` citations inside the surviving
  §15.4.4.** `spec/15_external-api-surface.md` lines 2214, 2217, 2275, 2278, 2372, and 2375 each read
  `flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)` inside the §15.4.4 Sample Echo
  Runtime pseudocode, which the reduction keeps in `spec/15` while retiring the `15.4.1` heading the six
  comments cite. No staged mechanism reaches them: they are not markdown links, so the anchor pass's
  markdown domain and the fragment-link gate do not read them; they carry no line number, so the retired
  citation form §4.6 states excludes them; they carry neither reserved word as a bare noun phrase; and they
  use the spelled-out spelling rather than the `§15.4.1` spelling the `tests/registers/anchor-senses.yaml`
  population is stated over, so the pass neither rewrites them nor fails closed on them. This is the
  disposition the `docs/api/internal.md` line 544 member already takes. The six are now one member of the
  falsified-sentence class, hand-corrected in the same change as the §15.4.1 reduction to name the §28.5
  card that owns the adapter-to-binary contract. Widening the anchor pass's matcher to the spelled-out
  spelling was rejected, because that spelling also appears where it is correct, at
  `sdks/client/python/lenny/types.py` line 262, so a tree-wide matcher would need a per-occurrence register
  for a population of six. §3.4's class row, the §15.4.3 through §15.4.6 survival paragraph, SPEC-3's
  Target, the class enumeration, and §11 carry the member.
- **The falsified-sentence class omitted the §15.7 graceful-shutdown pointer the §4.7 reduction falsifies.**
  `spec/15_external-api-surface.md` line 2556 names §4.7 as the source of the SIGTERM handling and the
  `terminate` and `shutdown` deadline contract, and the only §4.7 statement of that contract is the
  `terminate` row at `spec/04_system-components.md` line 725, inside the 715 through 731 message-schema
  table the reduction moves. The surviving `Terminate` (proto `Shutdown`) RPC row at
  `spec/04_system-components.md` line 664 states the disposition and the scrub alone, with no deadline and
  no SIGTERM rule, so after the reduction the sentence sends a runtime-SDK author to a section that no
  longer carries what it cites. No pass reaches the §4.7 half and no gate reports it, because §4.7 keeps its
  `#47-runtime-adapter` anchor, the line carries no line citation, and the class's proof is review plus the
  successor-pointer check, which never fires on a sentence nobody rewrites. Line 2556 is now the tenth
  §4.7-pointer member, with its §4.7 half hand-repointed at the §28.5 card that owns the intra-pod
  runtime-operations channel and its `§15.4.1` half left to the anchor-senses redirect. The class population
  reads twenty-one, the §4.7-pointer count reads ten in the class paragraph, the closing paragraph, and
  §3.4's class row, and §11's `spec/15` bullet reads ten hand-authored corrections.
- **SPEC-2's proto field additions turned a running tier-3 descriptor assertion red with no staged edit.**
  `ReportUsageRequest` is both an operational gateway-to-pod request message and one of the four messages
  that gain a slot identifier, so it goes from two fields (`schemas/lenny-adapter.proto` lines 1435 through
  1447) to four, while `tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go` line 63 compares
  `fields.Len()` against its `want` slice and aborts with `t.Fatalf` on any count change. SPEC-2 now names
  that file in its Target, states the `want`-slice widening as a tier-3 test bullet, and carries the whole
  tier-3 run as an exit criterion alongside the tier-4 run and the proto no-drift test.
  `TestReportUsageRequestDefaultCumulativeWireIdentical` in the same file is unchanged, because proto3 emits
  nothing for an unset field. The field-addition paragraph also records the two placements that keep the
  other exhaustive assertions green: `coordination_generation` lands on `CheckpointRequest` rather than on
  the `CheckpointStart` arm, whose exactly-six-field assertion sits at
  `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` line 150 while the
  `CheckpointRequest` assertion at line 121 reads the `msg` oneof's arm count alone, and the additions are
  per missing field, since `CheckpointBarrierRequest` already declares `coordination_generation`
  (`schemas/lenny-adapter.proto` line 1366) and `CheckpointStart` already declares `slot_id`
  (`schemas/lenny-adapter.proto` line 1120). TEST-1's unread-field sentence and §11 carry the same edit.
- **Correction to this pass: line 2556 was enumerated first, which falsified the "listed last" ordering and
  double-counted the member in §3.4's class row.** The bullet for `spec/15_external-api-surface.md` line 2556
  was placed at the head of SPEC-3's class enumeration, ahead of six members that are not §4.7 pointers, so
  the class paragraph's "they are listed last" no longer held and the trailing block the closing paragraph's
  single-structure argument is written over still contained nine members. The bullet now sits at the head of
  that trailing block, immediately before the `spec/04_system-components.md` line 241 bullet, so the block is
  the ten the class paragraph and the closing paragraph both name. §3.4's class row counted the same line
  twice, once inside "the `spec/15` ... §15.7 ... sentences" and again inside the ten, summing to
  twenty-two for a population of twenty-one. That row now reads "the two §15.7 platform-MCP-tool sentences"
  for lines 2558 and 2700 and names the graceful-shutdown bullet as one of the ten, so the enumeration sums
  to twenty-one and an implementor can recover which members the ten are.
- **Correction to this pass: the tier-3 paragraph asserted an empty tree-wide search that is not empty.** The
  paragraph closed with a claim that only `reportusage_wire_test.go` and the `checkpoint_stream/` assertions
  match a tree-wide search for an exhaustive descriptor field-count assertion. Three further ones exist:
  `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` line 172 over `RecycleScrub`,
  `tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` line 58 over
  `CheckpointBarrierResponse`, and `tests/tier3_contract/interceptor_proto/contract_test.go` line 41 over the
  `interceptorv1` messages. The conclusion holds, because none of the three reads a message this sub-step
  widens, so the paragraph now names all four assertions and states that ground per file instead of claiming
  an empty search. A later re-measurement of the field-addition set against the final message list therefore
  has the list to re-check rather than a claim that there is nothing to check.

### Pass 43 (2026-07-29, automated)

- **The falsified-sentence class, closed at twenty-one, omitted the two `schemas/lenny-adapter.proto`
  comments that attribute the relocated intra-pod handshake to §4.7.** The `GetObservedIntegrationLevel` RPC
  comment at `schemas/lenny-adapter.proto` lines 214 through 216 derives the observed integration level from
  the `lifecycle_capabilities`/`lifecycle_support` exchange and the intra-pod platform MCP server and
  attributes both to §4.7, and the `GetObservedIntegrationLevelRequest.wait_ms` comment at line 1577 waits
  for "its first §4.7 lifecycle handshake". Both frames sit in the message-schema table at
  `spec/04_system-components.md` lines 718 and 719 and the MCP server is the Part A bullet at line 697, all
  of which the §4.7 reduction moves, which is the same ground on which the proposal already hand-corrects
  `spec/05_runtime-registry-and-pool-model.md` lines 41 and 47 and `spec/15_external-api-surface.md` line
  2435. No pass reached either comment, because neither carries a line number, §4.7 keeps its
  `#47-runtime-adapter` anchor and gains no `tests/spec-anchor-moves.json` entry, and the two frame names are
  wire identifiers this proposal does not rename. SPEC-1's name pass does rewrite line 1578's "dials the
  lifecycle channel", which reproduces the class's signature failure of a canonical phrase over a wrong
  section pointer. The two comments are now one further member of the class, hand-repointed at the §28.5
  cards that own the intra-pod runtime-operations channel and the intra-pod MCP servers in the same SPEC-3
  change as the reduction, with `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 235 and 680 and
  `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 6231 following through the `make generate-proto` run
  SPEC-3 already takes after the line pass. The class population now reads twenty-two, the §4.7-pointer count
  reads eleven in the class paragraph, the closing paragraph, and §3.4's class row, the outside-`spec/` count
  reads seven across four shipped wire artifacts, and `schemas/lenny-adapter.proto` is named in SPEC-3's
  Target and in both §11 entries that cover it.
- **`spec/09_mcp-integration.md` line 142 was dispositioned while enumerating and is not a member.** The
  sentence sends a reader to §4.7 for the per-connector MCP server, and its stated target is the adapter
  manifest, whose `connectorServers` field reference at `spec/04_system-components.md` lines 792 through 794
  is inside the manifest carve-out and stays in §4.7. The pointer therefore still resolves after the
  reduction and takes no edit. The disposition is recorded on the `spec/09_mcp-integration.md` line 8 bullet
  so a later reviewer does not have to re-derive it.

### Pass 44 (2026-07-29, automated)

- **`spec/15_external-api-surface.md` line 2116 was a member of the falsified-sentence class, and the
  reduction does not falsify it.** The authentication bullet in §15.4.3 states that the intra-pod MCP
  manifest-nonce handshake is "identical in mechanism to the lifecycle channel handshake
  ([Section 4.7](04_system-components.md#47-runtime-adapter), item 1)". The item it cites is item 1 of
  §4.7's `#### Adapter-Agent Security Boundary` at `spec/04_system-components.md` line 890, which is the
  only statement of the manifest-nonce handshake and which the reduction boundary this proposal fixes at
  `spec/04_system-components.md` lines 691 through 731 does not touch. No sentence in Part A at line 695,
  in Part B at line 702, or in the message-schema table at lines 715 through 731 mentions a nonce, so the
  §28.5 runtime-operations card would state no such mechanism and carry no `item 1` for the parenthetical to
  name. The staged hand rewrite would therefore have sent a normative authentication rule to a destination
  that does not state the mechanism it asserts an identity with, while the surviving statement of that
  mechanism sat unreferenced in §4.7, which is the failure the line 2733 version-negotiation correction
  already records, and no gate catches it, because the tier-11 successor-pointer check reads only that the
  named heading exists. Line 2116 is dropped from the class and dispositioned on the line 2115 bullet and in
  the §15.4.3 through §15.4.6 carve-out paragraph, in the form Pass 43 used for
  `spec/09_mcp-integration.md` line 142. The class population now reads twenty-one, the §4.7-pointer count
  reads ten in the class paragraph, the closing paragraph, and §3.4's class row, with nine of the ten
  `spec/` and `docs/` sentences, and §11's `spec/15` bullet reads nine hand-authored corrections and names
  the two §4.7 pointers at lines 2115 and 2435 alone. The
  counts inside the Pass 41, Pass 42, and Pass 43 records describe the population at the time of those
  passes and are superseded by this one. SPEC-1's name pass still rewrites the reserved phrase "lifecycle channel
  handshake" on line 2116, which is unaffected by this disposition.
- **Corrections to this pass.** Dropping line 2116 from §11's `spec/15` enumeration left the count at ten
  while the list ran to nine members: the §15.3 sentence at line 1456, the §15.7 graceful-shutdown bullet
  at line 2556, the six §15.4.4 pseudocode comments as one member, the §15.7 sentence at line 2558, the
  §15.7 `Reply` doc comment at line 2700, item 7 of the §15.4.5 roadmap at line 2402, the `MessageEnvelope`
  sentence at line 1710, and the two §4.7 pointers at lines 2115 and 2435. The bullet now reads nine, and
  the propagation sentence above lists it. The disposition of line 2116 also called it "the third §4.7
  pointer" in the §15.4.3 through §15.4.6 carve-out paragraph and in §11, and neither scope has three:
  `spec/15_external-api-surface.md` carries §4.7 markdown links at lines 2116, 2160, 2402, 2435, and 2443
  inside §15.4.3 through §15.4.6, and further ones at lines 1456, 1462, 2553 through 2558, 2584, and 2591
  through 2700 elsewhere in the file. Both sentences now name the site by its line and its subject and
  assert no total.

### Pass 45 (2026-07-29, automated)

- **The falsified-sentence class omitted the `spec/21_planned-post-v1.md` link label that names the retired
  §15.4.1, and the proposal declared that link untouched.** `spec/21_planned-post-v1.md` line 31 reads
  `[Section 15.4.1](15_external-api-surface.md#translation-fidelity-matrix)`. The reduction retires the
  `#### 15.4.1 Adapter↔Binary Protocol` subsection while the carve-out keeps the Translation Fidelity Matrix
  heading and its anchor in `spec/15`, so the link resolves after the change and its label names a section
  number that exists in no `spec/` file. No staged mechanism reached it. The anchor pass leaves a link into a
  surviving anchor untouched, by the proposal's own rule; the line pass matches only the line-citation form
  and the link carries no line number; the name pass sees neither reserved word as a bare noun phrase; the
  spelled-out `Section 15.4.1` spelling is outside the `tests/registers/anchor-senses.yaml` population; and
  the fragment-link gate reads whether a link resolves rather than whether its label names the heading it
  resolves to. The proposal's ground for not widening the anchor pass's matcher to the spelled-out spelling
  asserted that every such occurrence in `spec/` names a §15.4 that survives, which this occurrence
  falsifies. The site is now a member of the falsified-sentence class, hand-corrected in the same SPEC-3
  change as the §15.4.1 reduction: the label becomes `Section 15.4`, which is the surviving section that
  carries the matrix, and the `#translation-fidelity-matrix` target is kept. The class population now reads
  twenty-two, §3.4's class row names the new member, `spec/21_planned-post-v1.md` line 31 is named in
  SPEC-3's Target and in §11, the two sentences that called the link untouched now state that its target is
  untouched while its label is corrected, and the matcher-widening rationale states that the residue a
  widened matcher would serve is the six §15.4.4 pseudocode comments together with this label, a population
  of seven. The counts inside the Pass 41 through Pass 44 records describe the population at the time of
  those passes and are superseded by this one.

### Pass 46 (2026-07-29, automated)

- **Thirteen hand-corrected links kept a `§15.4.1` label the reduction retires, while the proposal staged
  the identical label correction at three sibling sites.** SPEC-3 repaired each inbound link into the
  retiring `1541-adapterbinary-protocol` anchor by rewriting its target alone: the seven
  `spec/07_session-lifecycle.md` links at lines 116, 296, 323, 343, 349 (twice) and 433, the six same-page
  `spec/15_external-api-surface.md` links at lines 1838, 2165, 2489, 2584 (the first of the two links on
  that line), 2662, and 2684, and the absolute GitHub URL at `docs/reference/adapter-contract.md` line 371.
  Every one of the thirteen carries `Section 15.4.1` or `§15.4.1` as its label text, so a redirected target
  with an untouched label names a section that exists in no `spec/` file after the reduction retires the
  subsection. The proposal already treated that state as a defect and staged the label correction at three
  sibling sites: `spec/21_planned-post-v1.md` line 31, `spec/15_external-api-surface.md` line 2733, and
  line 1838, whose replacement it spelled out in full. Nothing caught the other twelve, because the
  fragment-link gate reads whether a link resolves rather than whether its label names the heading it
  resolves to, and no pass and no gate reads an absolute URL at all. §3.4's carve-out class row now states
  the **target-and-label rule**, which is that a hand correction in this class rewrites the link's label as
  well as its target whenever the label names the retiring subsection, in either spelling, and that the
  rewritten label names the section the hand-written target resolves into. The three SPEC-3 instructions now
  state both legs per site: `Section 15.4` at `spec/07` lines 116, 323, and 349 and at `spec/15` lines 1838,
  2165, and 2489, `§15.4` at `spec/07` lines 296, 343, and 433 and at `spec/15` lines 2584, 2662, and 2684,
  and `Spec §15.4 -- Translation Fidelity Matrix` at `docs/reference/adapter-contract.md` line 371. Because
  the label rewrite leaves no retired `§15.4.1` citation at those sites, SPEC-4's tree-wide citation pass
  reads nothing there, which SPEC-3 now states. `spec/07_session-lifecycle.md` is named in SPEC-3's Target
  for this edit with its six lines, and §11 records the label corrections for both files beside the existing
  `spec/15` members and in the `docs/reference/adapter-contract.md` bullet.
- **Correction to this pass: SPEC-4's absolute-URL disposition still described the `docs/reference/adapter-contract.md`
  line 371 correction as the fragment alone after this pass added the label leg.** The absolute-URL
  paragraph in SPEC-4 said only that "SPEC-3 rewrites its fragment by hand to `#translation-fidelity-matrix`
  in the same change that splits the heading", which was true before this pass and stale after it, because
  the same site now also takes the label rewrite to `Spec §15.4 -- Translation Fidelity Matrix` under the
  target-and-label rule this pass states, and SPEC-4 already names that label leg for the sibling
  `spec/15_external-api-surface.md` line 2733 correction two paragraphs later. SPEC-4 now names both legs
  for line 371 as well.
- **Correction to this pass: the matcher-widening rationale still asserted that every remaining spelled-out
  `Section 15.4.1` occurrence in `spec/` is already reached, which the target-and-label rule this pass adds
  falsifies for the population the rule does not cover.** The rationale against widening the anchor pass's
  matcher argued that "every remaining spelled-out `Section 15.4.1` occurrence in `spec/` sits inside a
  markdown link whose target anchor retires, so the anchor pass and the hand corrections above already
  reach it," naming a residue of seven (the six §15.4.4 pseudocode comments plus the
  `spec/21_planned-post-v1.md` line 31 label). That was accurate before this pass, when every markdown-link
  member of the carve-out class took a hand correction. This pass draws a distinction the rationale does not
  carry forward: the target-and-label rule rewrites a label only where a hand correction runs, and the
  ordinary anchor pass, run against `tests/spec-anchor-moves.json`'s single successor for every
  non-carved-out link, redirects a link's target without touching its label. Eleven links take that
  mechanical redirect while still carrying the spelled-out `Section 15.4.1` label: `spec/15_external-api-surface.md`
  lines 714, 1069, 1078, 1112, 1463, 2163, 2164, 2394, and 2514, `spec/08_recursive-delegation.md` line 829,
  and `spec/17_deployment-topology.md` line 361. After the reduction, each keeps a label naming a subsection
  that exists in no `spec/` file, the same defect the carve-out class exists to prevent, but reached by
  redirection rather than by an untouched target. The rationale now states that the residue a widened
  matcher would serve is these eleven links together with the six pseudocode comments and the corrected
  `spec/21` label, a population of eighteen, and that this proposal leaves the eleven mechanically-redirected
  labels uncorrected: the target-only redirect is the disposition every non-carved-out §15.4.1 markdown link
  takes, and a label-aware rewrite of that population is out of this proposal's scope. The count inside the
  Pass 45 record describes the population at the time of that pass and is superseded by this one.

### Pass 47 (2026-07-29, automated)

- **The paragraph at `spec/10_gateway-internals.md` line 50 does not use "lifecycle channel" twice in
  conflicting senses.** §3.2 and the SPEC-1 hand-correction instruction both claimed that paragraph uses
  the phrase in both senses two clauses apart. The paragraph contains "lifecycle channel" exactly once,
  used in the correct sense (the adapter-to-runtime intra-pod socket that `spec/04_system-components.md`
  line 702 defines), and separately contains a different reserved phrase, "pod-to-gateway control channel"
  (port 50051), naming a distinct mechanism. There is no collision to correct at that site. §3.2 no longer
  cites it as a mixed-sense sentence, the SPEC-1 hand-correction list drops it, leaving the interrupt
  sentences at `spec/07_session-lifecycle.md` line 324 and `spec/15_external-api-surface.md` line 1755 and
  the slot-failure sentence at `spec/05_runtime-registry-and-pool-model.md` line 540 as the three sites
  SPEC-1 corrects by hand, and the Pass 3 history entry and the files-touched list in §11 are updated to
  match: the wrong-mechanism-sites count returns from four to three and `spec/10_gateway-internals.md` is
  no longer listed as touched for a hand-corrected participant sentence.

### Pass 48 (2026-07-30, automated)

- **The `spec/04` numbered subsection headings were staged one sub-step after the pass that would have
  used them, so the §4.4 and §4.7 line citations would have retired onto whole-section anchors.** SPEC-3's
  atomic sub-step converts every citation into `spec/04`, in every section of the file, to an anchor
  citation, and SPEC-4 inserted the §4.4.1 through §4.4.5 and §4.7.1 through §4.7.11 headings afterwards.
  By SPEC-4's turn no `§4.4 line N` or `§4.7 line N` citation remained for the new anchors to receive, and
  no pass in this proposal re-points a whole-section anchor to a subsection anchor, so the insertion into
  `spec/04` was a no-op for the population it exists to serve. Measured over `git ls-files` under the read
  exclusion §4.6 states, that population is 513 occurrences of the `§4.4 line(s) N` form and 106 of the
  `§4.7 line(s) N` form, all of which would have landed on `#44-event--checkpoint-store` and
  `#47-runtime-adapter`, which is the precision loss SPEC-4's own rationale names as the defect. The
  `spec/04` half of the insertion moves into SPEC-3's atomic sub-step, ordered after the reduction and
  before the line pass, with the `spec/README.md` rows and the `tests/spec-map.json` keys each heading
  takes. The headings are inserted after the reduction rather than before it because §4.7 loses the Part A,
  Part B, and message-schema-table prose at `spec/04_system-components.md` lines 695 through 731 to §28, so
  titles authored earlier would name material that leaves the section; both orderings put the insertion
  ahead of the line pass, which is the property the citations depend on. The `spec/10` §10.1 and `spec/13`
  §13.2 insertions stay in SPEC-4, where the pass that converts their citations is the tree-wide one, and
  SPEC-4's ordering paragraph now covers those two files alone. SPEC-3's Target and its index-row
  paragraph, SPEC-4's Target, the §3.5 step 4 ordering sentence, the anchor-sense paragraph's statement
  that §4.7 carries no numbered subsections, and the §11 files-touched entry are updated to state the same
  placement.
- **Correction to the bullet above: the spec-map half of the heading walker's predicate was enumerated
  over a set that excluded the sixteen `spec/04` headings the move added to SPEC-3.** The index-row
  paragraph was updated for the move, but the paragraph immediately after it still listed the headings
  SPEC-3 writes a `tests/spec-map.json` key for as §28.5 through §28.8, the §28.5.1 through §28.5.7 card
  headings, `## 29`, and each `### N.M` subsection of `spec/29`, closed with "all of them named in §4.8".
  An implementor following that list writes no key for §4.4.1 through §4.4.5 or §4.7.1 through §4.7.11,
  each of which now carries a `spec/README.md` row from the same sub-step, so the walker is red at the exit
  of the sub-step that lands it, which is the failure the same paragraph names. The closing qualifier was
  also false for the new headings: §4.8 fixes titles and derived anchors for `spec/28` and `spec/29`
  alone, while the §4.4 and §4.7 titles are authored inside SPEC-3 from the paragraph subjects that survive
  the reduction. The enumeration now names the sixteen headings and states where each group's title comes
  from. The two statements of the walker's route to green, in TEST-1's heading-walker paragraph and in the
  gates-land-green paragraph, carried the same omission and now read "the §4.4, §4.7, §28, and §29 rows and
  keys SPEC-3 writes".

### Pass 49 (2026-07-30, automated)

- **The falsified-sentence class omitted the four crash-recovery pointers that name §4.7 as the owner of
  the pod-side cumulative usage total.** The §4.7 reduction moves the message-schema table at
  `spec/04_system-components.md` lines 715 through 731 to §28, and the `llm_request_completed` row at line
  731 is the only sentence in §4.7 that states the adapter retains a per-session cumulative token total.
  The surviving `ReportUsage` RPC row at `spec/04_system-components.md` line 663 states that the gateway
  pulls token counts and persists them to Postgres, with no retention and no re-report across a gateway
  restart. Four sentences cite §4.7 for that retained cumulative and were not members of the class:
  `spec/11_policy-and-controls.md` line 53 ("Crash Recovery for Quota Counters", which takes the maximum of
  the Postgres checkpoint and the pod-reported cumulative), line 153 (the in-memory billing write-ahead
  buffer reconstructed from pod-reported token usage), line 171 (the `GATEWAY_CRASH_RECONSTRUCTION` billing
  correction reason), and `spec/12_storage-architecture.md` line 161 (the Postgres-failover
  write-durability row for billing events in the write-ahead buffer). None carries a line citation or a
  reserved bare noun phrase, and §4.7 keeps its `#47-runtime-adapter` anchor, so by the class's own
  reasoning no pass reaches any of them and each would have survived the reduction asserting an ownership
  §4.7 gave up, leaving the specification's quota and billing reconstruction rule pointing at a section
  that no longer states the mechanism it depends on. All four are now members, re-pointed by hand at the
  §28.5 card that owns the intra-pod runtime-operations channel in the same SPEC-3 change as the reduction,
  on the same footing as the `spec/11_policy-and-controls.md` line 49 member. The class population now
  reads twenty-six and the §4.7-pointer count reads fourteen in the class paragraph, in the closing
  paragraph, and in the §3.4 class row. `spec/12_storage-architecture.md` is added to SPEC-3's Target and
  the §11 files-touched entry now names `spec/11_policy-and-controls.md` lines 49, 53, 153, and 171 and
  `spec/12_storage-architecture.md` line 161.

### Pass 50 (2026-07-30, automated)

- **SPEC-3 claimed the `spec/24` line 114 correction gave the shipped compliance suite a capability it
  stages no code for.** §3.1, SPEC-3, and the §11 bullet all read as though adding
  `schemas/runtime-ops-events.schema.json` to the enumeration makes the suite able to validate
  `CH-RUNTIMEOPS`, which contradicts §7's statement that this proposal changes no runtime behavior.
  `cmd/lenny-compliance/schemavalidate.go` lines 29 through 34 declare two schema files, `loadSchemas` at
  lines 46 through 70 compiles those two alone, and `checkLifecycleHandshake` at
  `cmd/lenny-compliance/full.go` lines 225 through 258 checks the handshake reply field by field with no
  schema involved, so after application the sentence that gates a third-party adapter from
  `pending_validation` to `active` would have named an artifact no code path reads, with no claim-register
  row recording it. The three statements now say the correction makes the specification and the published
  runtime-author reference name the artifact the suite is required to assert against, and SPEC-3 seeds a
  third explicitly-written claim-register row alongside the R12 and R14 rows: status `ABSENT`,
  `deferral_id` R8, naming `cmd/lenny-compliance/schemavalidate.go` as the surface that reads two schema
  files and no third. R8 is the plan's reciprocal host-conformance step, which covers the frame-level
  requirements of `schemas/runtime-ops-events.schema.json` in `cmd/lenny-compliance`
  (`gateway-runtime-comms-remediation.md` lines 991 through 1002). Staging the harness change here was
  rejected because §7 scopes this proposal to naming and relocation.
- **SPEC-3 edited the `docs/reference/adapter-contract.md` "Canonical artifacts" table by hand and left its
  JSONL row crediting the wrong artifact with the runtime-operations frames.** Line 659's Purpose cell ends
  its frame list with "lifecycle frames", which is the wrong-mechanism claim SPEC-2 corrects in the
  artifact's own `description` and at `spec/15_external-api-surface.md` line 1463. That artifact's `$defs`
  are `messageEnvelope`, `from`, `heartbeat`, `heartbeat_ack`, `shutdown`, `tool_call`, `tool_result`,
  `response`, `status`, and `set_tracing_context`, while the runtime-operations frames are `$defs` of the
  file SPEC-2 renames to `schemas/runtime-ops-events.schema.json`. With only the new row added, the page a
  third-party runtime author reads would have carried two adjacent rows crediting two artifacts with the
  same frames, and no pass or gate reaches the cell, because it carries no reserved phrase in either banned
  spelling, no retired identifier, no line citation, and no fragment. The row is now a staged hand
  correction in SPEC-3, with its Purpose cell rewritten over the frames the artifact schematizes. Line 659
  is named in SPEC-3's Target list and in the §11 bullet for that page.
- **SPEC-3 staged the §29 off-holder matrix as the normative statement of off-holder behavior for a route
  and state §7.2 already governs.** `spec/07_session-lifecycle.md` line 330 requires a `delivery: immediate`
  message landing on a non-coordinator replica to be forwarded to the session's coordinator, and requires
  the forwarding replica to fall back to inbox buffering with a `queued` receipt when the coordinator is
  unreachable so the message is not silently dropped. The coordinator is the holder
  (`spec/04_system-components.md` line 489 and `pkg/gateway/podlifecycle/podsession/registry.go` lines 12
  and 13). The claim that no section states the off-holder case today was therefore false for that row, and
  a typed refusal there would have contradicted §7.2's no-drop fallback. SPEC-3 now states that the §29 rows
  for `POST /v1/sessions/{id}/messages` and for `lenny/send_message` with `delivery: immediate` restate
  §7.2's requirement and cite
  §7.2 as the owning section, that `spec/07` line 330 gains a pointer to the matrix in the same change, and
  that every other row states a case no section states today. Relocating the paragraph into §29 was
  rejected because the rule is session-lifecycle state-machine content the surrounding paths in §7.2 depend
  on. `spec/07_session-lifecycle.md` line 330 is named in SPEC-3's Target list and in the §11 entry for
  that file.
- **Correction to the §7.2 reconciliation above: it covered one of the two message sources §7.2 governs.**
  `spec/07_session-lifecycle.md` line 330 states the coordinator-routing rule and its no-drop fallback for
  both sources, which are the external client route `POST /v1/sessions/{id}/messages` and the inter-session
  `lenny/send_message` tool (`spec/09_mcp-integration.md` line 29), and the MCP tool surface is in the
  matrix's row domain. The §29 row for `lenny/send_message` with `delivery: immediate` therefore states a
  case §7.2 already states, so as written SPEC-3 would have had an implementor specify an independent typed
  refusal there against §7.2's fallback, and its closing exception, which is route-agnostic, contradicted
  the restatement sentence. SPEC-3 now names both routes as restating §7.2 and citing it as owner, and
  reads "Every row other than those two `delivery: immediate` resume rows states a case no section states
  today."
- **Correction to the staged `docs/reference/adapter-contract.md` line 659 rewrite: it listed `from` as a
  stdin/stdout frame.** `from` is a property subschema of `messageEnvelope`
  (`schemas/lenny-adapter-jsonl.schema.json` line 39, defined at lines 70 through 73 as adapter-injected
  sender context) rather than a frame; the artifact's top-level `oneOf` at lines 9 through 19 admits nine
  frames and does not include it. The ten-name list was the artifact's `$defs`, carried over from SPEC-2's
  enumeration. SPEC-3 now names the frames the top-level `oneOf` admits, so the replacement cell does not
  present sender context as a wire frame in a published runtime-author reference.
- **Correction to the coordinator-is-holder citation: the quoted sentence is on lines 12 and 13.**
  `pkg/gateway/podlifecycle/podsession/registry.go` line 11 ends the teardown clause and the handoff clause
  runs from line 12 to line 13. Both the SPEC-3 sentence and the review record above now cite lines 12
  and 13.

### Pass 51 (2026-07-30, automated)

- **SPEC-2 was assigned claim-register rows in a register SPEC-3 states does not exist yet, and no sub-step
  wrote them.** SPEC-2's change text read that each field it adds "gets a claim-register row with status
  `UNWIRED`", while `tests/claim-map.json` is absent from SPEC-2's Target list and SPEC-3 states that "the
  register does not exist before it and the validator fails a missing register file". SPEC-3's seeding
  instruction was closed over the reference document's status tables plus three explicitly written rows,
  and the fields SPEC-2 adds do not exist in the tree today (`schemas/lenny-adapter.proto` declares
  `coordination_generation` only at lines 1338 and 1366), so no status table can carry a row for any of
  them. At SPEC-3's exit the validator's per-field rule would have fired on every field SPEC-2 added,
  contradicting §3.5's rule that each gate has exactly one landing sub-step that supplies its route to
  green. SPEC-3's seeding instruction now carries a fourth explicitly written group, one `UNWIRED` row per
  field SPEC-2 adds, each with a `deferral_id` naming the plan step that reads it, which is R16 for the
  generation fence and R22 for the slot identifiers (`gateway-runtime-comms-remediation.md` lines 438,
  1950, and 1958). SPEC-2 now states that those rows are seeded by SPEC-3 with the rest of the register,
  and TEST-1's per-field cases name SPEC-3 as the seeding sub-step. Creating the register in SPEC-2 was
  rejected because it splits one register and one validator across two sub-steps.
- **The §4.7 reduction falsifies a third `§4.7` pointer in the shipped proto, which was in no edit list and
  reachable by no pass.** `schemas/lenny-adapter.proto` line 1063 reads
  `STATUS_INTERRUPT_TIMEOUT = 2; // deadline elapsed with no acknowledgement (§4.7)`, and the only §4.7
  statement of that status is the `interrupt_request` row at `spec/04_system-components.md` line 723, which
  sits inside the 715 through 731 table the reduction moves. The surviving `Interrupt` RPC row at line 654
  reads "Interrupt current agent work" alone, and line 723 is that file's only occurrence of the status
  name, so after application the comment would attribute a spec-named failure status to a section that no
  longer states it, and it ships mirrored at `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 457. The site
  is now a member of the falsified-sentence class on the same disposition as the §15.7 graceful-shutdown
  member, hand-corrected to name the §28.5 card that owns the intra-pod runtime-operations channel in the
  same SPEC-3 change as the reduction and carried into `pkg/proto/` by the `make generate-proto` run SPEC-3
  already takes. The class population reads twenty-seven and the §4.7-pointer count reads fifteen in §3.4's
  class row, in the class paragraph, and in its closing sentence, and line 1063 is named in SPEC-3's Target
  list and in §11. The adjacent `STATUS_BUSY` comment at line 1064 is recorded as a non-member, because
  `spec/04_system-components.md` line 677 states the `BUSY` drop above the reduction boundary.
- **`docs/runtime-author-guide/publishing.md` line 367 was exempted from the supersession check and never
  corrected, leaving three published statements of one artifact set in disagreement.** The sentence states
  that the compliance suite validates every JSON Lines frame a runtime emits against
  `lenny-adapter-jsonl.schema.json` and `messagepart.schema.json`, which after SPEC-2 routes the
  runtime-operations frames to an artifact whose top-level `oneOf` does not admit them. That is the defect
  SPEC-3 corrects at `spec/24_lenny-ctl-command-reference.md` line 114 and in the
  `docs/reference/adapter-contract.md` "Canonical artifacts" table, on the page a runtime author reads
  immediately before publishing and which links to the corrected table for the schema list. No pass or gate
  reaches the sentence. SPEC-3 now stages the same hand correction, restating the sentence over the
  artifact set the suite is required to assert against, and the exemption's second ground is restated over
  the corrected sentence rather than over that page's frames. The site is named in SPEC-3's Target list, in
  the supersession check's accept cases in TEST-1, and in §11.

### Pass 52 (2026-07-30, automated)

- **The renamed RPC's own doc comment and its message-type comment state the wrong mechanism, and neither
  was in any edit list.** `schemas/lenny-adapter.proto` lines 223 through 226 credit the gateway-to-adapter
  gRPC stream with `checkpoint_ready`, `interrupt_acknowledged`, `credentials_acknowledged`, and
  `deadline_approaching`, which are `CH-RUNTIMEOPS` frames on the intra-pod socket, and send the reader to
  §15.4 for the taxonomy. Lines 1594 through 1598 state that the envelope taxonomy is defined in
  `lenny-adapter-jsonl.schema.json`, which is the claim SPEC-2 falsifies when it rewrites that artifact's
  `description` and `spec/15_external-api-surface.md` line 1463. The stream carries the event set
  `pkg/adapter/controlchannel.go` lines 17 through 44 declare, which is `RATE_LIMITED`, `AUTH_EXPIRED`,
  `PROVIDER_UNAVAILABLE`, `LEASE_REJECTED`, `AdapterTerminating`, `FINAL_USAGE_REPORT`, and
  `CheckpointBarrierAck`. No pass reaches either comment, and SPEC-2's identifier pass renames the RPC and
  the two message types inside them, which would leave a precise false statement that `CH-ADAPTEREVENTS`
  carries the intra-pod runtime-operations frames, in the published gRPC contract and in its mirrors at
  `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` lines 242 and 687 and
  `pkg/proto/adapter/v1/lenny-adapter.pb.go` line 6327. Both sites are now members of SPEC-2's
  hand-authored wrong-mechanism class, rewritten over the event set the handler emits and pointed at the
  adapter-to-gateway events table at `spec/04_system-components.md` lines 679 through 689, which is the
  specification statement of that taxonomy. That table sits above line 691, where the block SPEC-3's §4.7
  reduction moves opens, so it stays in §4.7 and neither comment joins the class SPEC-3 re-points at a
  §28.5 card. Pointing the comments at `schemas/runtime-ops-events.schema.json` was rejected, because that
  artifact schematizes the intra-pod frames rather than the adapter-to-gateway events these two messages
  carry. §11's `schemas/lenny-adapter.proto` bullet names both sites, and the `make generate-proto` run
  SPEC-2 already takes carries both corrections into `pkg/proto/`.
- **The §15.4 wire-artifact pointer kept saying the runtime adapter contract is published as three
  artifacts while the same change widened every parallel statement to four.** `spec/15_external-api-surface.md`
  line 1460 states a three-artifact contract and the bullets at lines 1462 through 1464 name
  `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, and
  `schemas/messagepart.schema.json`, while SPEC-3 adds `schemas/runtime-ops-events.schema.json` by hand to
  `spec/24_lenny-ctl-command-reference.md` line 114, to the `docs/reference/adapter-contract.md`
  "Canonical artifacts" table and its lead sentence at line 654, and to
  `docs/runtime-author-guide/publishing.md` line 367. The pointer is carved out of the §15.4 reduction
  unchanged, is reachable by no pass, and is exempt from the supersession check on its first ground, so
  nothing repaired it, and §15.4 would have asserted a three-artifact set two lines above its own
  corrected line 1463 on the section third-party runtime authors read as the contract. SPEC-3 now restates
  line 1460 over the artifact set without a count and adds a fourth bullet naming
  `schemas/runtime-ops-events.schema.json` and the Full-level runtime-operations frames it schematizes, in
  the same change as the three other statements of that set. The supersession check's first-ground
  exemption is restated over the corrected enumeration, as pass 51 did for the `publishing.md` site. The
  site is named in SPEC-3's Target list, in the §15.4 carve-out paragraph, and in §11's
  `spec/15_external-api-surface.md` bullet. This also makes §8's claim to close the specification half of
  the misdescribed-wire-artifact record true; `gateway-runtime-comms.md` lines 2468 through 2472 record
  that half as the framing of the contract as three artifacts.
- **The §15.4 preamble carve-out paragraph still described the reduced §15.4 as a three-artifact set after
  the same sub-step widened it to four.** The paragraph reasoning about
  `spec/15_external-api-surface.md` line 1466 said the line's three opening sentences state the
  compatibility contract for "the same three artifacts §15.4 is reduced to", two paragraphs after the
  sub-step restated line 1460 over the artifact set without a count and added a fourth bullet. Line 1466
  itself carries no count and so ranges over whatever the bullet list names. The count is removed from the
  carve-out paragraph, which now matches the count-free phrasing the rest of the sub-step adopted.
- **The citation offered for §8's both-halves claim covered one half.** The line 1460 correction paragraph
  cited `gateway-runtime-comms.md` lines 2467 through 2471 as recording both halves of the
  misdescribed-wire-artifact record. That range is the specification half alone, and its line 2467 is
  blank while the sentence it ends runs to line 2472. The shipped-artifact half, which is the wrong
  `description` in `schemas/lenny-adapter-jsonl.schema.json`, is the separate paragraph at lines 2474
  through 2478. The citation is split accordingly, and the parallel citation in this pass's own record
  above is corrected to lines 2468 through 2472.

## 10. Open decisions for review

**All decisions below were RATIFIED on the 2026-07-29 sign-off and remain settled. None is open.** The
sign-off itself is withdrawn, for the reasons the Status line states, and none of those reasons touches
these decisions: each concerns a missing deliverable or an unappliable edit rather than a choice made here.
They are retained as the record of what was decided and why, so a later step does not reopen a settled
question, and the re-review that follows treats them as closed.

1. **RATIFIED: the tooling ships in this proposal.** It is included here because the three
   migration sub-steps cannot run without it and because splitting it puts a hard dependency across a
   sign-off boundary. The argument for splitting is that the tooling is testable on its own and touches no
   specification text.

2. **RATIFIED: the wire-contract rename is in scope now.** Renaming later costs more with every step that
   lands first. Renaming now makes this proposal a breaking change to the runtime author contract. The
   planning material treated the deferral as the more expensive option, and this proposal follows it.

3. **Settled, no longer open: the per-card line budget in §28.5.** The cap is removed. A card states the
   contract its channel has, and the fixed field template carries the comparability the cap was meant to
   force. A channel whose contract is genuinely long is described in full rather than truncated to meet a
   number, and a card that reads as unclear is a signal to rewrite the card rather than evidence that the
   budget was correct.

4. **RATIFIED: `spec/29` is a separate file.** Separate keeps each file
   scannable and matches the modularity goal. Combined keeps one file to consult.

## 11. Files touched on application

- `spec/29_communication-scenarios.md`, new. `spec/28_communication-channels.md` exists, created by
  proposal 0067, and SPEC-3 appends to it.
- `spec/03_high-level-architecture.md`, `spec/04_system-components.md`, and
  `spec/15_external-api-surface.md`, for the diagram correction, the reductions, and the successor
  pointers. `spec/15_external-api-surface.md` also carries nine hand-authored corrections for sentences
  the reductions falsify: the §15.3 sentence at line 1456, which names §15.4 as the normative prose
  reference for the gateway-to-pod channel contract and becomes false when §15.4 gives that content up;
  the §15.7 graceful-shutdown bullet at line 2556, whose §4.7 pointer names the `terminate` deadline and
  SIGTERM-on-timeout rule that the §4.7 reduction moves with the message-schema table; the six pseudocode
  comments inside the surviving §15.4.4 at lines 2214, 2217, 2275, 2278, 2372, and 2375, which cite the
  retired §15.4.1 in the spelled-out `Section 15.4.1` form no pass reads; the §15.7 sentence at line 2558,
  which names §4.7 as authoritative for the platform MCP tool set that
  the §4.7 reduction moves to §28; the §15.7 SDK `Reply` doc comment at line 2700, which attributes the
  `lenny/output` platform MCP tool to a §4.7 Part A that no longer exists after the reduction; item 7
  of the §15.4.5 roadmap at line 2402, which attributes the Part B message schemas to §4.7 after they
  move; the `MessageEnvelope` sentence at line 1710, whose closing "see Protocol Reference below" names a
  block the §15.4.1 reduction moves to §28; and the two §4.7 pointers at lines 2115 and 2435, which
  send a runtime author to §4.7 for the platform MCP tool list and the
  `lifecycle_support` handshake respectively, both of which the §4.7 reduction moves. The §4.7 pointer at
  line 2116, the authentication bullet in §15.4.3, takes no edit, because it cites the
  Adapter-Agent Security Boundary item at `spec/04_system-components.md` line 890 that stays in §4.7. It also carries one hand-authored correction that SPEC-2
  lands rather than SPEC-3: the artifact-scope sentence at line 1463, which credits
  `schemas/lenny-adapter-jsonl.schema.json` with schematizing the runtime-operations frames that live in
  `schemas/runtime-ops-events.schema.json`. It carries one further hand-authored correction, landed by
  SPEC-3 with the other statements of the same artifact set: the §15.4 wire-artifact pointer at line 1460,
  restated over the artifact set without a count, with a fourth bullet after line 1464 naming
  `schemas/runtime-ops-events.schema.json` and the Full-level runtime-operations frames it schematizes, so
  the specification, `spec/24_lenny-ctl-command-reference.md` line 114, the
  `docs/reference/adapter-contract.md` "Canonical artifacts" table, and
  `docs/runtime-author-guide/publishing.md` line 367 name one artifact set. Separately from those, it carries the seven same-page links into
  the retired `1541-adapterbinary-protocol` anchor that SPEC-3 hand-corrects under the carve-out class, at
  lines 1838, 2165, 2489, 2584 (the first of the two links on that line), 2662, 2684, and 2733. Each of the
  seven takes both legs of the target-and-label rule: the target becomes the surviving
  `#messageenvelope--unified-message-format` anchor for the first six and `#1542-rpc-lifecycle-state-machine`
  for line 2733, and the label becomes `Section 15.4` at lines 1838, 2165, and 2489, `§15.4` at lines 2584,
  2662, and 2684, and `§15.4.2` at line 2733.
- `spec/07_session-lifecycle.md` lines 116, 296, 323, 343, 349, and 433, for the seven links into the retired
  `1541-adapterbinary-protocol` anchor that cite `MessageEnvelope` material the carve-out keeps in `spec/15`,
  hand-corrected in the same SPEC-3 change as the heading split. Each takes both legs of the
  target-and-label rule: the target becomes `15_external-api-surface.md#messageenvelope--unified-message-format`
  and the label becomes `Section 15.4` at lines 116, 323, and 349, and `§15.4` at lines 296, 343, and 433.
  The same file's coordinator-routing paragraph at line 330 gains a pointer to the §29 off-holder matrix,
  which restates that paragraph's requirement for the `delivery: immediate` route rather than replacing it,
  so the forward-to-coordinator rule and its no-drop fallback keep one owning section.
- `spec/21_planned-post-v1.md` line 31, for one hand-authored correction the §15.4.1 reduction forces in the
  same SPEC-3 change. Its
  `[Section 15.4.1](15_external-api-surface.md#translation-fidelity-matrix)` link keeps its target, because
  the Translation Fidelity Matrix heading and its anchor are carved out of the reduction, and its label is
  rewritten to `Section 15.4` so that it no longer names a subsection that exists in no `spec/` file.
- `spec/05_runtime-registry-and-pool-model.md` and `spec/07_session-lifecycle.md`, together with the
  interrupt row in `spec/15_external-api-surface.md`, for the sentences whose current text names the wrong
  participant, corrected by hand.
- `spec/04_system-components.md` line 241, `spec/05_runtime-registry-and-pool-model.md` lines 41 and 47,
  `spec/09_mcp-integration.md` line 8, `spec/11_policy-and-controls.md` lines 49, 53, 153, and 171,
  `spec/12_storage-architecture.md` line 161,
  `schemas/lenny-adapter.proto` lines 214 through 216, line 1063, and line 1577, and
  `docs/runbooks/credential-rotation-failure.md` lines 11 and 19, for the §4.7 pointers whose cited material
  the §4.7 reduction relocates, re-pointed by hand at the §28.5 card that owns it in the same SPEC-3 change
  as the reduction. Each survives every pass, because §4.7 keeps its anchor, none of them carries a line
  citation, and the name pass rewrites the reserved phrase while leaving the section pointer wrong. The
  `spec/04` and `spec/05` edits are separate from the reduction and the wrong-participant corrections listed
  above, and the runbook strings are carried by the runbook alone rather than mirrored from an alert
  annotation in `pkg/alerting/rules`.
- `spec/README.md`, for the `spec/28` and `spec/29` table-of-contents rows, the §28.5.1 through §28.5.7
  card rows, the revised §4.7 and §15.4 rows, and the 49 `### N.M` rows the index is missing today. The
  file is hand-maintained and has no generator, so it is edited in the same change as the headings it
  indexes.
- `tests/spec-map.json` and `tests/spec-map-exceptions.yaml`, for the new `spec/28` and `spec/29` headings,
  the 50 existing `### N.M` headings that carry no key, and `24.19.1`, the one deeper heading the index
  already carries and which has neither a key nor an exceptions entry today. `tests/spec-map.json` is also
  re-keyed by the identifier pass, for the renamed schema path in its `schemas` arrays and for the two
  existence-checked `::<symbol>` references to `TestLifecycleEventExamplesValidate` and
  `checkLifecycleHandshake`.
- `tests/tier11_docs/`, for the reconciliation tests that pin rewritten or relocated spec prose, spec
  heading slugs, or intra-spec markdown links as string literals, re-scoped to the heading that owns each
  sentence or anchor after the change. `tests/tier11_docs/embedded_mode_anchors_test.go` and
  `tests/tier11_docs/embedded_echo_placement_test.go` pin the §15.4.3 and §15.4.4 anchors through a
  `specCrossRef` table and a `mustContain` list rather than through the three helper functions the other
  files use; those two headings survive the narrowed §15.4 reduction, so both files are confirmed green
  rather than edited. The three files that scope assertions to `"### 4.7 "` pin rows above the §4.7
  reduction boundary, so the reduction edits none of them; the reserved noun phrase in the pinned §4.6.1
  clause at `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69 is rewritten by
  SPEC-1's name pass.
- Files carrying reserved bare noun phrases anywhere in the domain N3 states, in either spelling, rewritten
  by script. In the space-separated spelling that is
  `spec/` (65 occurrences across 11 files), `docs/` (55 across 16), `schemas/` (10 across 4), the Go doc
  comments of tracked Go files (124 across 55, of which 23 across 9 sit under `tests/`), and the two
  tracked root-level contract documents (10, which is 9 in `TESTING.md` and 1 in `README.md`). The
  hyphenated compound adds 6 in `spec/`, 3 in `docs/` (the 6 the tree carries less the three markdown
  anchor identifiers N3 places outside the matcher, at `docs/reference/glossary.md` line 207 and
  `docs/api/internal.md` lines 229 and 318), 29 in Go doc comments, and 2 in `TESTING.md`, and
  it brings in `spec/18_build-sequence.md` (lines 164, 165, and 408) and five Go files that carry no
  space-separated occurrence, so the `spec/` population is 71 across 12 files. Both spellings are rewritten
  under the
  generated-file exclusion §4.6 states and the N3 exclusion list for root-level markdown.
- `schemas/lenny-adapter.proto` and `schemas/lenny-adapter-jsonl.schema.json`, for the renames and the
  incorrect descriptions SPEC-2 corrects. The `schemas/lenny-adapter.proto` corrections in that sub-step are
  the `CheckpointBarrierAck` comment at lines 166 through 172, the renamed RPC's own doc comment at lines
  223 through 226, and the request and response message comment at lines 1594 through 1598, the last two of
  which state that the gateway-to-adapter stream carries the intra-pod runtime-operations frames and take
  the corrections SPEC-2 lists. `schemas/lenny-adapter.proto` is edited a second time under
  SPEC-3, for the line pass, for the two §4.7 handshake comments listed above, and for the
  `STATUS_INTERRUPT_TIMEOUT` comment at line 1063, and both edits are carried
  into `pkg/proto/` by the `make generate-proto` run each sub-step already takes.
- `schemas/runtime-ops-events.schema.json` (renamed from `schemas/lifecycle-events.schema.json` by SPEC-2),
  `schemas/messagepart.schema.json`, and `schemas/lenny-adapter-jsonl.schema.json` a second time under
  SPEC-3, for the `description` pointers into `spec/04` §4.7 and `spec/15`
  §15.4 that the SPEC-3 reductions falsify, re-pointed by hand at the §28.5 cards that own the material,
  with the `schemas/lenny-adapter-jsonl.schema.json` pointer split so its `messageEnvelope` reference keeps
  pointing at the surviving `MessageEnvelope` heading in §15.4.
  All three artifacts are embedded through `schemas/embed.go`, so the descriptions ship to external
  consumers.
- `schemas/README.md`, for the artifact table §28.7 supersedes, replaced by hand with a reference to the
  register in the same SPEC-3 change as the reduction. The table omits the runtime-ops events schema and
  `schemas/lenny-tokenservice.proto` while standing for the directory's artifact set, and its
  `lenny-adapter-jsonl.schema.json` and `messagepart.schema.json` rows point at §15.4 for material the
  §15.4.1 reduction moves to §28, so the replacement is what makes the supersession check green in its
  landing sub-step and what corrects both falsified rows. The validation, versioning, and examples sections
  of that file are unchanged.
- `docs/reference/glossary.md`, for the split of the conflated entry into one `CH-ADAPTEREVENTS` entry and
  one `CH-RUNTIMEOPS` entry, with a redirect stub on the existing anchor.
- `docs/api/internal.md`, for the sentence at line 544 that sends runtime adapter authors to §15.4 for the
  binary protocol specification and the `MessagePart` format, both of which move to §28 with the §15.4.1
  reduction, split by hand so that the two surviving halves keep pointing at §15.4.
- `docs/reference/adapter-contract.md`, for the absolute GitHub URL at line 371 whose fragment cites the
  retiring `1541-adapterbinary-protocol` anchor for the Translation Fidelity Matrix. The matrix stays in
  `spec/15`, and no pass or gate reads an absolute URL, so the fragment is hand-rewritten to
  `#translation-fidelity-matrix` in the SPEC-3 change that splits the heading, and its
  `Spec §15.4.1 -- Translation Fidelity Matrix` label is rewritten to `Spec §15.4 -- Translation Fidelity
  Matrix` in the same change, under the target-and-label rule §3.4 states. The same page carries the
  "Canonical artifacts" lead sentence at line 654 and table at line 658, which name the three artifacts the
  external-adapter compliance suite asserts against and omit the runtime-ops events schema; the table gains
  a row for that schema and the lead sentence is restated over the artifacts the table names, in the same
  SPEC-3 change as the `spec/24` line 114 and `docs/runtime-author-guide/publishing.md` line 367
  corrections, so the three published statements of the adapter-validated artifact set agree. The table's `lenny-adapter-jsonl.schema.json` row at line 659 is
  hand-corrected in the same change, because its Purpose cell credits that artifact with the
  runtime-operations frames the new row carries, which is the wrong-mechanism claim SPEC-2 corrects in the
  artifact's own `description` and at `spec/15_external-api-surface.md` line 1463.
- The adapter manifest emitter, the adapter flag, and the three runtime SDKs, rewritten by script.
- `pkg/adapter/controlchannel.go` and `pkg/adapter/controlchannel_test.go`, renamed to
  `pkg/adapter/adapterevents.go` and `pkg/adapter/adapterevents_test.go` under N4, with the RPC and its
  two message types in `schemas/lenny-adapter.proto` renamed to `AdapterEvents`, `AdapterEventsRequest`,
  and `AdapterEventsResponse`.
- `pkg/adapter/holdstate.go` and `pkg/adapter/holdstate_test.go`, whose gRPC full-method string literals
  name the renamed RPC and are resolved from the proto RPC row rather than from the Go type row.
- `cmd/lenny-compliance/full.go` and `tests/tier4_integration/credential_lifecycle_test.go`, the second
  manifest emitter and its integration fixture.
- `cmd/runtimes/streaming-echo/main.go`, the manifest reader outside the three runtime SDKs, which parses
  the renamed key through its own struct tag and is what the tier-4 run exercises.
- `docs/runtime-author-guide/`, for the renamed manifest key, and
  `docs/runtime-author-guide/publishing.md` line 367 a second time under SPEC-3, for the schema list that
  states the artifact set the compliance suite validates a runtime's JSON Lines frames against. That
  sentence names two artifacts and quantifies over every frame the runtime emits, so after SPEC-2 moves the
  runtime-operations frames to `schemas/runtime-ops-events.schema.json` it routes them to an artifact that
  does not schematize them. It is restated by hand over the corrected artifact set in the same SPEC-3
  change as the `spec/24` line 114 and `docs/reference/adapter-contract.md` corrections, so the three
  published statements of that set agree.
- `spec/09_mcp-integration.md`, `docs/runbooks/artifact-replication-residency-violation.md`,
  `docs/runbooks/legal-hold-escrow-residency-violation.md`, `docs/runbooks/ops-lock-split-brain.md`, and
  `docs/runbooks/otlp-plaintext-egress-detected.md`, and
  `docs/runbooks/admission-plane-feature-flag-downgrade.md`, for the seven markdown fragment links whose
  target heading does not exist today, corrected by hand in the change that lands the fragment-link gate.
- Every file carrying a citation of the retired form §4.6 states, in any carrier and any spelling,
  including the 556
  section-level occurrences across 148 files that carry no subsection component, the 617 comma-list
  occurrences across 341 files, the 50 slash-separated and 2 `and`-separated multi-member occurrences
  across 42 files, the `+`-separated multi-member occurrences across 11 files, the 136 qualified
  occurrences across 68 files, the 65 en-dash range occurrences across
  38 files, the 123 path-form occurrences across 59 files, and the colon-form occurrences, which are 18
  across 10 files in the section-number variant and 11 across 7 files in the path variant,
  which is 2,353 Go files and 264 non-Go
  files: 230 of the non-Go files sit under `migrations/`, `charts/`, `sdks/`, `tests/`, and `build/`, and
  the rest sit under `pkg/`, `schemas/`, `scripts/`, `docs/`, `compose/`, `.github/`, and `dist/`.
  Every file in that population is rewritten by script. `BUILD-GAPS.md` and `TEST-GAPS.md` are **not touched by this proposal**. They are
  named in the read exclusion §4.6 states, so no `specshift` pass opens them, the resolver, the
  ratchet, and the residual scan §4.7 defines skip them, and the line-citation register carries no per-file
  count for either. They hold roughly
  2,168 citations between them, which is the largest non-Go population in the tree, and none of it is in
  scope here. Both are historical audit records rather than authored specification or code carriers, so
  rewriting their citations would edit the record of what was found at the time it was found. A citation in
  either that points into a range this proposal shifts goes stale and stays stale, which is the accepted
  outcome recorded in §5.
- `pkg/gateway/externalapi/openapi/openapi.json` and `pkg/gateway/mcpfabric/mcptools/mcptools.go`, whose
  served schema descriptions carry the citation form inside JSON values and Go string literals and lose it
  rather than gaining an anchor.
- `pkg/embedded/manifests/manifests.yaml`, `pkg/embedded/crds/`, and `charts/lenny/crds/`, regenerated by
  `make generate` and the chart-to-embedded copy rather than rewritten, from the chart templates and the
  doc comments on `pkg/apis/lenny/v1alpha1/*.go` that the passes do rewrite. The `lenny.dev/schema-version`
  annotation blocks and the top-level spec and status preserve-unknown markers in `charts/lenny/crds/` are
  hand-applied after regeneration and are hand-edited in SPEC-4, together with the matching literal
  prefixes in `tests/tier0_static/crds_test.go`.
- `pkg/proto/`, regenerated by `make generate-proto` from `schemas/*.proto` rather than rewritten.
- `pkg/gateway/mcpfabric/mcptools/generated_schemas.go`, `pkg/ops/mcp/generated_tools.go`,
  `docs/alerting/rules.yaml`, and `docs/alerting/routing-recommendations.md`, regenerated from
  `pkg/gateway/externalapi/openapi/openapi.json` and from `pkg/alerting/rules` rather than rewritten.
- `charts/lenny/values.schema.json`, regenerated by `go run ./cmd/lenny-chart-schema-gen` rather than
  rewritten, from the `desc:` struct tags on `pkg/chart/values/values.go` that the passes do rewrite.
- `schemas/ocsf-mapping.yaml`, regenerated by `go run ./cmd/lenny-ocsf-mapping-gen` rather than rewritten,
  from the `mappingHeader` const at `pkg/audit/ocsf/catalog.go` lines 147 through 150 that the line pass
  does rewrite, with `TestMappingYAMLInSync` in `pkg/audit/ocsf/catalog_test.go` as the drift gate.
- `tests/tier0_static/degradation_lock_line_citation_test.go`, a running tier-0 gate whose predicate
  requires a `§25.4 line N` citation to be present above two declarations in `pkg/ops/`. Its predicate is
  hand-rewritten in SPEC-4 to read the anchor form and to require the named §25.4 heading's body to carry
  the quoted sentence.
- `pkg/embedded/localcli/`, `pkg/embedded/stack/`, and `pkg/embedded/k3s/`, which are hand-written Go with
  no generator and carry 132 line citations, rewritten by the line pass like any other Go source.
- Markdown files in `spec/` and `docs/` carrying a fragment link into a retired anchor, rewritten by
  script.
- Files carrying a bare `§15.4.1`-form citation, which outside `spec/` and `proposals/`
  is 669 occurrences across 150 files (595 once §4.6's read exclusion of `BUILD-GAPS.md` and
  `TEST-GAPS.md` is applied), among them the three published runtime SDKs and
  `pkg/gateway/externalapi/outputpartfidelity`, rewritten by the anchor pass to the destination
  `tests/registers/anchor-senses.yaml` records for each occurrence.
- `tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go`, whose `want` slice in
  `TestReportUsageRequestWireContract` gains the two fields SPEC-2 adds to `ReportUsageRequest`, so the
  exact-field-count assertion pins the widened contract in the same change as the proto edit.
- `tests/tier0_static/`, `tests/tier3_contract/`, `tests/tier10_conformance/`, `tests/tier11_docs/`,
  `tests/registers/`, `tests/spec-anchor-moves.json`, `tests/claim-map.json`, `tests/spec-map.json`,
  `tests/change-graph.json`, and `tests/change-graph-pending.txt`, for the registers this proposal seeds
  and the gates whose route to green is one of its content changes. `scripts/specshift` and `cmd/lenny-test`
  are read and run rather than written here: proposal 0065 builds them, and this proposal invokes the
  passes and adds entries to the registers they consume.
- `TESTING.md`, for the
  retired identifier spellings at line 1996, which is the socket token in the Full-level conformance
  battery, and at line 1521, which is the schema path, the example-fixture glob, and the validating test
  name of the runtime-ops event schema; and for the reserved bare phrase at lines
  788, 858, 874, 993, 1315, 1527, 1972, 1996, and 2248, rewritten by script. Those nine lines are the same
  population SPEC-2 enumerates and the same count this section states above, and the two identifier lines
  are the population SPEC-2 enumerates for the identifier pass. The §7 verdict-enum sentence, the §7
  `tiers.<name>.status` enum sentence, and the §21.3 infrastructure-failure sentence are **not** edited
  here. Proposal 0065 hand-authors all three in the same change as the `UNVERIFIED` constant that falsifies
  them, and it also gives the tier-status sentence the `inconclusive` value the harness already emits, so
  restating them here would stage the same edit twice and risk reverting that addition. The line numbers
  above are measured against the current file and shift once those edits land, which is immaterial because
  the passes that rewrite these sites resolve them from their registers rather than from a line.
- `README.md`, for the reserved bare phrase in the integration-level table at line 155, rewritten by
  script.
- `.claude/rules/channel-naming.md`, new: N1 through N8, plus the two banned spellings verbatim as the
  specimen §28.1's N3 points to. The file is outside the naming lint's domain, so the specimen does not
  trip it.
- `spec/24_lenny-ctl-command-reference.md` line 114, for the compliance-suite artifact enumeration, which
  gains `schemas/runtime-ops-events.schema.json` by hand. That sentence is the one that gates a third-party adapter from
  `pending_validation` to `active`, and its three-artifact list omits the runtime-ops events schema today,
  so the correction is what makes the specification and the two published runtime-author pages name the
  artifact the suite is required to assert against for `CH-RUNTIMEOPS`. The shipped harness compiles two
  schema files, and that distance is carried as the `ABSENT` claim-register row with `deferral_id` R8 that
  SPEC-3 seeds. The
  enumeration stays rather than becoming a reference to §28.7, because §28.7 is derived from the whole
  schemas directory and would either bind the gate to artifacts a runtime adapter never implements or leave
  the asserted set undecidable; the sentence is exempt from the supersession check on the ground that it
  names the artifact subset a named consumer asserts against, per SPEC-3. The
  `spec/18_build-sequence.md` phase-deliverable lists take no hand edit here, for the phase-scoping reason
  SPEC-3 states; that file is listed above for its reserved-phrase occurrences at lines 164, 165, and 408,
  which SPEC-1's name pass rewrites.
- `spec/10_gateway-internals.md` §10.1 and `spec/13_security-model.md` §13.2, for the numbered subsection
  headings SPEC-4 inserts, and `spec/04_system-components.md` §4.4 and §4.7, for the same headings inserted
  in SPEC-3, so the
  retired line citations into those sections land on anchors at paragraph granularity rather than on a
  whole-section anchor. Each new heading also lands a `spec/README.md` row and a `tests/spec-map.json` key
  in the same change. The `spec/04` headings land in SPEC-3 because SPEC-3's atomic line pass converts
  every citation into that file, so the headings have to exist before it runs.
- `gateway-runtime-comms.md`, for the point-in-time header naming the working-tree commit and the sections
  that supersede it. The body is unchanged, and a tier-11 test pins both the header and the body so the
  document stays a historical record rather than becoming a maintained second description of the same
  contract.

The specific files inside each script-driven class are the script's output rather than this document's
content, and the gates in TEST-1 are what prove the class is complete.
