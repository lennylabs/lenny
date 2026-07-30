# Proposal: Build the specification-migration tooling and the gates that prove a migration complete

- **Status:** Verified (2026-07-30). Converged after 10 adversarial review rounds (20 findings fixed);
  awaiting sign-off.
- **Date:** 2026-07-30.
- **Scope:** The tooling half of the first three steps of `gateway-runtime-comms-remediation.md`, split out
  of proposal 0064 so that it lands first. This proposal changes no specification file. It builds
  `scripts/specshift` and its four passes, the citation resolver, the line-citation ratchet, the
  change-graph completeness check, the skip-reason classifier, the proto no-drift test, the `UNVERIFIED`
  verdict state, the register contract they share, and the residual gate that makes every enumeration in
  the migration safe to be incomplete, together with the accept, reject, and boundary cases for each. The
  specification changes those passes then perform stay in 0064, which depends on this proposal.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file.
Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

This proposal exists because of a sequencing constraint that two application attempts of proposal 0064
made concrete. 0064 states that its migration is script-driven, that it enumerates no edit sites, and that
completeness is proven by gates rather than by review. Its own sequencing puts the tooling first, because
the rewrites it stages are mechanical operations over thousands of sites and are not hand edits.

The implementation pipeline applies a proposal's specification edits, verifies them, and commits them
before any code is written. For a proposal whose specification edits are the output of a script, that
order inverts the dependency: the pass that resolves each site does not exist when the site is edited, and
the registers that carry each site's resolution do not exist either. Both application attempts of 0064
therefore asked agents to hand-apply edits that 0064 deliberately never enumerates. Neither converged, and
the second reported the reserved-phrase pass unappliable for exactly this reason, because the 71 sites in
`spec/` are individually two-valued and cannot be resolved without the register that records each sense.

Splitting the tooling into its own proposal resolves the inversion rather than working around it. This
proposal is pure code and test infrastructure, so it lands through the pipeline in the ordinary way. Once
`scripts/specshift`, the registers, and the gates exist in the tree, 0064's specification passes become
what they were designed to be: a script run whose completeness a gate proves, rather than a hand edit
whose completeness a reviewer estimates.

Nothing in this proposal depends on 0064. The passes are built and tested against synthetic fixtures in
`scripts/specshift`'s own `run_test.go`, and every gate this proposal lands is green on the unmodified
tree or green against a baseline this proposal seeds from today's measured population. A reader who wants
the migration this tooling performs, and the naming law and the specification sections it rewrites
references into, will find them in
`proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`.

## 1. Problem

**The migration 0064 stages cannot be performed by hand.** It rewrites citations, anchors, reserved noun
phrases, and retired identifiers across the tracked tree. The populations are in the thousands, several
classes are individually two-valued so a global search and replace corrupts them, and a reviewer reading a
diff of that size cannot tell a complete rewrite from one that missed a spelling the enumeration did not
anticipate.

**Completeness has no proof today.** A rewrite that misses a site leaves a citation resolving to the wrong
place, and nothing in the tree fails. The gates this proposal builds convert that silence into a tier-0
failure that names the missed member and its class.

**An enumeration written by hand is incomplete by construction.** Every class this migration touches was
measured by grep against a moving tree, so each enumeration is a snapshot. The residual gate is what makes
that acceptable: it computes a deliberately over-broad predicate, subtracts what the enumeration and the
register carry, and fails on the remainder, so a member no one anticipated is reported rather than
silently skipped.

## 2. Decisions

1. **The tooling ships as its own proposal, ahead of the specification changes.** The pipeline lands
   specification edits before code, so a proposal whose specification edits are a script's output cannot
   land both halves at once. This is the correction the two failed application attempts of 0064 forced.
2. **The tooling is kept whole rather than split further.** The passes, the resolver, the ratchet, and the
   register contract share one validator, one entry schema, and one set of tests, so splitting them puts a
   hard dependency across a sign-off boundary for no gain.
3. **Every gate lands green.** A gate is either green on the unmodified tree or green against a baseline
   seeded from today's measured population. Narrowing a predicate to reach green is not a closure route.
4. **Each enumerated class carries a residual.** The enumerations stay, because they make the common case
   fast and document what is known, and each one is paired with a broad predicate whose unexplained
   remainder fails the build.

## 3. Design overview

### 3.1 What lands

`scripts/specshift` is the migration engine, carrying the name, identifier, anchor, and line passes. Each
pass is driven by a register keyed per occurrence rather than by a global pattern, and each fails closed on
a site its register does not carry, so a sense the enumeration missed aborts the run instead of being
rewritten wrongly.

The gates are the other half. The citation resolver and the line-citation ratchet hold the citation form,
the change-graph completeness check and the skip-reason classifier close two holes in the existing test
harness, the proto no-drift test pins the generated stubs to their source, and the residual gate ranges
over every enumerated class.

`TESTING.md` gains the `UNVERIFIED` verdict state and the `unverified` tier status, which the harness needs
to distinguish a tier that did not run from one that passed.

### 3.2 The migration is script-driven

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
| Test-harness contract prose in `TESTING.md` that enumerates a value the tooling changes, which is every §7 field-semantics sentence closing an enum the producer widens plus the §21.3 infrastructure-failure sentence | the `UNVERIFIED` verdict state and the `unverified` tier status TOOL-1 adds | hand-authored in the same change as the constant, because no substitution rule produces the sentence | the tier-11 enum-reconciliation test TEST-1 adds, which derives the verdict and tier-status value sets from the exported constants TOOL-1 moves into `cmd/lenny-test/verdictstatus`, and fails when a documented sentence omits one, in the manner of `tests/tier11_docs/backup_status_enum_test.go`; the §21.3 sentence carries no derivable value set and is covered by review. Those sentences are outside every pass's scope even though the rest of the file is not, and TOOL-1 names them and the change that stages them |
| Reserved noun phrases and retired channel identifiers in the tracked root-level contract documents `README.md` and `TESTING.md` | `tests/registers/reserved-phrase-senses.yaml` and `tests/registers/identifier-senses.yaml`, on the same per-occurrence terms as the `spec/` and `docs/` sites | the `specshift` name and identifier passes, whose walk covers tracked root-level markdown under the exclusion list N3 states | the naming lint and the identifier-resolution gate, whose scope is the same walk, so the gate that reads the whole tree has a pass that can write every file it reads |
| Correcting a description the collision made wrong | `tests/registers/reserved-phrase-senses.yaml` | hand-authored | review, because no gate reads meaning; the naming lint and the identifier gate both pass a semantically wrong sentence that carries a canonical spelling |
| A sentence that a reduction falsifies, where no pass repairs its meaning, because the sentence carries no line citation and no moved anchor, and any reserved phrase it carries is rewritten to the current spelling while the false statement stands | the reductions SPEC-3 lands | hand-authored in the same change as the reduction, and enumerated there; the members are the `spec/15` §15.3, §15.4.5, and `MessageEnvelope` sentences, the two §15.7 platform-MCP-tool sentences, the six §15.4.4 pseudocode comments that cite the retired §15.4.1 in the spelled-out `Section 15.4.1` form the anchor pass does not read, the `spec/21_planned-post-v1.md` line 31 link label that names the retired §15.4.1 in the same spelled-out form while its target anchor survives, the three shipped schema descriptions, the `docs/api/internal.md` binary-protocol pointer, the two `schemas/README.md` artifact-table rows, and the ten pointers that name §4.7 as the owner of relocated intra-pod material, nine of them `spec/` and `docs/` sentences, one of those nine being the §15.7 graceful-shutdown bullet, and the tenth the pair of `schemas/lenny-adapter.proto` comments on the intra-pod handshake, all of which SPEC-3 lists | review, plus the tier-11 successor-pointer check where the rewritten sentence names a successor heading |
| An inbound reference into a retired anchor whose cited material is carved out of the reduction and stays where it is, so the anchor map's single successor for that anchor would send the reference to the wrong heading. The class is every reference into a retired anchor whose cited material stays where it is, in any carrier the anchor pass writes, which is the markdown link form and the bare `§X.Y`-form section citation in a comment or in prose alike. The **target-and-label rule** governs every markdown-link member: a hand correction rewrites the link's label as well as its target whenever the label names the retiring subsection, in either the `§15.4.1` or the spelled-out `Section 15.4.1` spelling, because a link whose target is redirected while its label still names §15.4.1 names a section that exists in no `spec/` file. The rewritten label names the section the hand-written target resolves into, and rewriting it leaves no retired citation at that site for SPEC-4's tree-wide citation pass to read | the carve-outs SPEC-3 states with the links it enumerates there, plus `tests/registers/anchor-senses.yaml`, keyed by file and occurrence, which records the destination anchor of every occurrence of a retired anchor the map alone cannot decide, and which SPEC-4 retires with the map | hand-authored in the same change as the split for the markdown-link members, before any anchor pass runs, which today are the seven `spec/07_session-lifecycle.md` links into `1541-adapterbinary-protocol` that cite `MessageEnvelope` material, the seven same-page links into the same anchor inside `spec/15_external-api-surface.md` that cite material the carve-outs keep there, six of them citing the `MessageEnvelope` heading and the seventh, at line 2733, citing the surviving §15.4.2 heading, and the absolute GitHub URL at `docs/reference/adapter-contract.md` line 371 that cites the same anchor for the Translation Fidelity Matrix; the `specshift` anchor pass for the bare section-citation members, driven per occurrence by the sense register and failing an occurrence with no entry rather than substituting the map's single successor | review, plus the fragment-link gate, which confirms the hand-written target resolves for the intra-repo link members; the absolute-URL member is covered by review alone, because the gate does not read an absolute URL; and TEST-1's anchor-pass cases, which pin the fail-closed path for a bare citation with no register entry and pin that a citation of carved-out material resolves to the surviving heading |
| A same-page markdown fragment link carried inside a block a reduction relocates to another file, whose target heading stays behind, so the link breaks although neither it nor its target changed | the blocks SPEC-3 relocates, and the links it enumerates there | hand-authored in the same change that moves the block, rewritten to the file-qualified form against the file the block left; the anchor pass cannot reach them because the map is keyed by retired anchor and these targets survive | the fragment-link gate, which is red on a same-page fragment that no longer resolves against its new page |
| A pre-existing intra-repo markdown fragment link whose target heading does not exist | the seven links SPEC-4 enumerates | hand-authored in the same change as the fragment-link gate | the fragment-link gate, which is green on introduction once they are corrected |

No list of edit sites appears in this proposal for the script-driven classes, and none should appear in
the applied change. A list is stale the moment a step merges, and a reviewer cannot verify one at this
scale, while a gate can. The hand-authored classes are bounded and are enumerated where they land, and the
few sites the script-driven classes name explicitly are named because a gate or a served artifact depends
on them rather than as an attempt at enumeration.


### 3.3 How this document refers to proposal 0064

The design sections and the staged changes below are the tooling half of 0064, carried over with one
recorded divergence, so that the rewrite each pass performs and the population each gate measures are
stated in the same words in both documents. That text refers to the four specification sub-steps of 0064 by their labels, and to
sections of 0064 by number. Those references are to the other document throughout, and they resolve as
follows.

| Reference | What it names in 0064 |
|:--|:--|
| SPEC-1 | The sub-step that lands the naming law, the three registers, and the reserved-phrase removal, and that runs the name pass over the tree |
| SPEC-2 | The sub-step that lands the wire-contract rename, and that runs the identifier pass |
| SPEC-3 | The sub-step that writes the new specification sections and performs the reductions |
| SPEC-4 | The sub-step that retires the redirected anchors and the line citations, and that runs the anchor pass and the line pass |
| §3.5 | 0064's sequencing, which orders the four sub-steps above and states that each gate lands in the sub-step supplying its route to green |
| §4.8 | 0064's heading table, which fixes the title and derived anchor of every new heading |
| §28.1 through §28.5 | The specification sections 0064 creates, which state the naming law, the registers, and the per-channel contract cards |

A reference of the form `§X lines N-M` inside a quoted or illustrative citation is an example of the
citation form under discussion rather than a cross-reference, and is left as written.

Two passages diverge from 0064. The read exclusion §4.6 states and the residual scan's first exclusion
§4.7 states both exclude every `testdata/` directory here, and the corresponding paragraphs in 0064 do
not. N3's exclusion list is no longer among them: 0064 now states the same `testdata/` clause directly, so
the two documents agree on it and no supersession is needed. The clause carries the same meaning in both,
which is that every `testdata/` directory
is outside the domain of the name pass, the identifier pass, the naming lint, and the
identifier-resolution gate alike, so N3's rule that every file those gates read has a pass that can write
it still holds, and the reserved-phrase and retired-identifier fixtures TEST-1 places under `testdata/`,
including the ones a tracked Go file carries in a doc comment, are read by no gate and written by no pass.
Without that extension the fixtures TEST-1 stages for the name pass's Go-doc-comment case would sit inside
the naming lint's read domain and outside the name pass's write domain, which is the condition N3 exists
to prevent. No tracked `testdata/` file carries a bare reserved phrase or a retired channel spelling
today, measured over `git ls-files`, so the extension removes nothing from the population SPEC-1 and
SPEC-2 measure, and 0064 states the same measurement for the same reason. The two paragraphs in this
proposal supersede those in 0064, because the gates are built
here and the exclusion is what lets their own fixtures carry the retired form. Every measurement 0064
states against the read domain, including SPEC-4's Target and its zero exit criterion, is therefore
measured over the domain the gates this proposal builds actually scan. One sentence in §4.6 depends on
that supersession: where it says the citation list is narrower than the naming list N3 states, the
citation list it compares against is the superseded one, and the two lists differ in the three build and
queue records `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md` rather than in the `testdata/`
clause, which both documents now state.

Nothing in this proposal is blocked on any of those. Each pass is built and tested against fixtures in its
own test file, and each gate this proposal lands is green on the unmodified tree or against a baseline
seeded here.

## 4. Detailed design

### 4.6 The line-citation ratchet

The resolver validates that an existing line citation still points inside its section. It does not stop a
new one being written. Without a second gate the anchor convention is documentation, the retirement
happens once, and the population regrows.

The resolver is red on introduction, so it ships with a baseline of its own rather than as a bare
predicate. A large population of in-tree citations already fails its rule before any content moves. A
measurement over the tracked tree, computing each section's range from the `##` through `######` headings
under `spec/` and applying the read exclusion stated below, finds on the order of 1,500
citations across roughly 500 files that do not resolve inside the section they name; the exact figure
depends on how a section's end line is computed, so TOOL-1 records the count its own resolver produces.
Two verified examples: `pkg/adapter/workspace/materialize.go` line 203 cites `§7.4 line 433`, while §7.4
begins at `spec/07_session-lifecycle.md` line 437 and line 433 sits in §7.3; and
`pkg/gateway/externalapi/admin/erasure.go` line 356 cites `§12.8 line 764`, while §12.8 begins at
`spec/12_storage-architecture.md` line 774. TOOL-1 therefore seeds
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
excluded because a gate cannot read its own baseline as tree content. The resolution baseline is keyed by
file and citation text, so it holds a copy of the text of every non-resolving citation in the tree. Inside
the resolver's read domain each copy is a second occurrence of that citation, filed under the register's
own path rather than under the file the citation was written for, and it is non-resolving by construction,
which is exactly the outcome TEST-1 pins as a failure when it requires that a baseline entry does not
travel between files. Seeding an entry for such a copy would add a further copy of the same citation text
to the register, so the seeding would not converge. The ratchet excludes the same pair for the same
reason: the register would otherwise enter its population as a file with no per-file count and fail on its
first line citation. Excluding the pair from both gates is what lets TOOL-1 land them green, per the sequencing 0064 §3.5 states, step
1.

The `testdata/` exclusion rests on the same argument extended from a gate's baseline to a gate's input.
The resolver, the ratchet, and the line pass are themselves tested, and each accept, reject, and boundary
case TEST-1 states has to present the retired citation form verbatim, in every spelling this section
enumerates. Text of that kind is input to a gate rather than a pointer into the specification. It names no
section to resolve against, and its route out of the population is the deletion of the case rather than a
retirement, so a resolution-baseline entry or a per-file count seeded for it would never fall and SPEC-4's
zero exit criterion would be unmeetable. TEST-1 therefore holds every fixture that carries the retired
form in a `testdata/` file the test reads rather than in a Go string literal in the test source, and
`testdata/` sits outside the read domain of the resolver, the ratchet, and the residual scan, and outside
the write domain of every pass. No tracked `testdata/` file carries the retired form today, measured over
`git ls-files`, so the exclusion removes nothing from the measured population and no gate reaches green
through it. The one in-tree test whose predicate requires the retired form in its own source is
`tests/tier0_static/degradation_lock_line_citation_test.go`, which SPEC-4 rewrites as stated below. The
write exclusion,
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
baseline TOOL-1 seeds are measured with the join applied, so a wrapped citation is counted and resolved
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
repository's current state, so it is the non-durable shell gate this proposal argues against. TOOL-1
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
`goimports`, and the absence of any one of them is the condition TEST-1 requires the test to record as
`UNVERIFIED`. Without the prepend, a tree whose plugins live only in `GOPATH/bin` would fail the test
while `make generate-proto` succeeds there.

The producer's binaries are absent from continuous integration today, so the test needs a workflow change
to reach a conclusion in every job where the gate is enforced. `scripts/setup-dev.sh` is the workstation
path and is the only installer of the two codegen plugins in the tree; nothing under `.github/` installs
either one. Four jobs run the static tier, and only one of them installs any tool. The tier-0 `static` job
installs `gofumpt`, `goimports`, `golangci-lint`, and `buf` (`.github/workflows/pr.yml` lines 65 through
71) before running the static tier (`.github/workflows/pr.yml` line 81), so two of the producer's four
binaries are present there and the two codegen plugins are not. The other three jobs install nothing
beyond `actions/setup-go`, so all four are absent from them: the `pr-fast` job runs a group whose plan
opens with the static tier (`.github/workflows/pr.yml` lines 41 through 44, with the plan at
`cmd/lenny-test/tiers.go` lines 57 through 78), and the phase-gate `gate` job and weekly's
`load-full-system` job run groups whose plans also open with the static tier on the same bare toolchain
(`.github/workflows/phase-gate.yml` line 42, with the per-phase plans at `cmd/lenny-test/tiers.go` lines
114 through 233, and `.github/workflows/weekly.yml` line 105, whose `phase-13.5-gate` plan comes from
`tests/groups.yaml` lines 359 through 362 through `tiersForGroupFromYAML` at `cmd/lenny-test/tiers.go`
line 240). A tier whose status is not `pass` ends the run
and exits non-zero (`cmd/lenny-test/cmd_run.go` line 428), so a test recording `UNVERIFIED` on every run
would leave tier 0 red in all four jobs, stop the later tier-0 checks in the same run from being reached,
and verify `pkg/proto/` nowhere. Installing the two codegen plugins alone would fix one job of the four
and leave the other three permanently red on the two binaries they still lack, which is a worse outcome
than the fail-open behavior the test replaces.

TOOL-1 therefore stages the producer's whole binary set in every job that runs the static tier. The
`static` job's existing install step gains `protoc-gen-go` and `protoc-gen-go-grpc` beside the `goimports`
and `buf` it already installs, so that job installs `gofumpt`, `goimports`, `golangci-lint`, `buf`,
`protoc-gen-go`, and `protoc-gen-go-grpc` (`.github/workflows/pr.yml` lines 65 through 71). The `pr-fast`,
phase-gate `gate`, and `load-full-system` jobs each gain three steps modeled on the `static` job's, which
are a `~/go/bin` cache restore, an install conditional on a cache miss covering `buf`, `goimports`,
`protoc-gen-go`, and `protoc-gen-go-grpc`, and an `Add tool bin to PATH` step.
`pr-fast`'s timeout rises from 5 minutes to the 10 the `static` job carries
(`.github/workflows/pr.yml` lines 32 and 49), because a cold install runs before its group does. TOOL-1
installs all four into `$(go env GOPATH)/bin`, and bumps the
`~/go/bin` cache key from `lenny-go-bin-v2` to `lenny-go-bin-v3` (`.github/workflows/pr.yml` lines 62 and
64, `.github/workflows/nightly.yml` lines 187 and 189, and
`.github/workflows/reusable/tool-cache.yml` lines 40 through 43), because the install step is skipped on
an exact cache hit and an existing cache carries neither plugin.

The three added restore steps read the `lenny-go-bin-v3` key and never write it. They use
`actions/cache/restore` at the pinned `actions/cache` commit, so no post-job save runs. That alone does
not make the `static` job the sole writer of the key, because one already exists: the `mutation` job in
`.github/workflows/nightly.yml` restores `~/go/bin` with a read-write `actions/cache` step whose primary
key string is byte-identical to the `static` job's (`.github/workflows/nightly.yml` lines 184 through 189
against `.github/workflows/pr.yml` lines 57 through 64), and the only binary it installs is `go-mutesting`
(`.github/workflows/nightly.yml` lines 190 through 195). TOOL-1 therefore converts that step to
`actions/cache/restore` at the same pinned commit in the same change. The conversion costs the `mutation`
job nothing, because its `go-mutesting` install is already conditional on `cache-hit != 'true'` and
carries `continue-on-error: true`, so it re-runs on every miss. Leaving it read-write would carry the
second writer onto the empty `lenny-go-bin-v3` namespace, where the first job to save decides the
directory's contents: nightly runs on a schedule against the default branch
(`.github/workflows/nightly.yml` lines 9 through 11) while `pr.yml` never runs on `main`
(`.github/workflows/pr.yml` lines 9 through 12), so the `mutation` job would be the only writer of a
`main`-scoped cache under that key, and a `main`-scoped cache is restorable from every pull-request
branch. A `static` job taking an exact hit on that one-binary directory reaches the fail-open the rest of
this paragraph describes, and, after this proposal, also finds none of the proto producer's four binaries,
so the no-drift test records `UNVERIFIED` and ends the run non-zero on every affected pull request.
The difference from the `static` job's step is load-bearing. The
`static` job's install step is the only one in the tree that installs `gofumpt` and `golangci-lint`, and
it is skipped on an exact key hit (`.github/workflows/pr.yml` lines 65 and 66). A read-write cache step in
a job that installs only the producer's four binaries would save a `~/go/bin` missing those two under the
shared exact key, and the next `static` job to hit that key would skip its install and find neither
binary. Both checks return a nil error when their binary does not resolve
(`cmd/lenny-test/cmd_run.go` lines 537 through 540 and 577 through 579), so `gofumpt` would stop reporting
unformatted files and `golangci-lint` would stop surfacing findings, with tier 0 green throughout. The
window is not hypothetical: `static` runs after `pr-fast` in the same workflow (`.github/workflows/pr.yml`
line 50), including after `pr-fast`'s post-job save, and a cache written on `main` by the phase-gate or
weekly job is readable from every pull-request branch. Restore-only in every job that installs a partial
set, which after TOOL-1's conversion of the `mutation` job is every job but `static`, keeps the shared
key's contents complete by construction, and it needs neither a second key namespace nor a duplicate
install of the two lint binaries in three jobs that do not otherwise need them. With that change `UNVERIFIED` reports a
degraded environment rather than the steady state of every CI run.

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
(built in TOOL-1, with its cases in TEST-1) computes, per class, the set matching the broad predicate minus the enumerated members minus the
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
residual check for a class lands in the sub-step that seeds that class's registers, per the sequencing 0064 §3.5 states.

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


## 5. Edge cases and accepted failure modes

| Case | Behavior | Where it is stated |
|:--|:--|:--|
| A pass meets a site its register does not carry | The run aborts non-zero before any write, naming the site. A partially rewritten tree is never committed | The fail-closed rule each pass states in §6 |
| An enumerated class gains a member no enumeration anticipated | The residual gate fails tier 0 naming the member and its class | §4.7 |
| A residual register entry's member leaves its class | The run that removes the member removes its entry in the same run, so a rewrite that empties a class also empties the in-class part of its register | §4.7 |
| A baseline register is seeded from a measured population that has since drifted | The baseline is rewritten downward only, so drift upward fails and drift downward is absorbed | §4.6 |
| A gate runs but inspects nothing, because its walk root or its exclusion list selects zero files, or because it finds zero inspectable sites, over a tracked tree that is not empty | The run fails and names the gate rather than reporting green. A class whose broad predicate selects zero members over a scan that did select files is the terminal state of that class and stays green | The zero-inspection case §6 states for the residual gate, the change-graph completeness check, the citation resolver, the line-citation ratchet, the skip-reason classifier, and the proto no-drift test |
| A gate is absent from the registered gate list altogether, so nothing invokes it | Out of scope here. The gate-integrity meta-gate over the registered list lands in 0064 with SPEC-4, because its fixed list names gates SPEC-1 through SPEC-4 register | §7 |
| A pass writes a file the write exclusion covers, which no gate reads and no gate could report | Each pass carries a case asserting that a site in `proposals/`, in the two historical audit records, and in the two root planning documents is left byte-identical while an equivalent site in an ordinary carrier in the same run is rewritten. The name and identifier passes cover the three build and queue records as well, and the name and line passes cover a file the per-file generated-artifact rule selects. No case asserts byte-identity for the two citation registers, because the identifier pass rewrites a renamed file's key in them in the same run per §4.6, so they are excluded from site rewriting while remaining subject to that key rewrite; and none asserts it for `testdata/`, which is where each case's own fixture tree sits | The per-pass write-exclusion cases in §6 |
| A gate's own test fixture carries the text that gate fails on | The fixture is held in a `testdata/` file, which is outside the read domain of the resolver, the ratchet, and the residual scan, and outside every pass's write domain, so no gate reports its own input | §4.6 |

## 6. Proposed changes

### TOOL-1. The migration and gate tooling

**Target:** `scripts/specshift`, `cmd/lenny-test`, `tests/tier0_static/`, `tests/registers/`,
`TESTING.md` §7 and §21.3, which state the verdict enum and the `tiers.<name>.status` enum that the
`UNVERIFIED` verdict state and the `unverified` tier status extend, and the workflow files that run the
static tier, which are `.github/workflows/pr.yml`, `.github/workflows/phase-gate.yml`,
`.github/workflows/weekly.yml`, and the shared tool list in
`.github/workflows/reusable/tool-cache.yml`, together with `.github/workflows/nightly.yml`, for the
`~/go/bin` cache key it restores alongside `.github/workflows/pr.yml` and the shared tool list, and for
the conversion of its `mutation` job's read-write cache step to `actions/cache/restore` that §4.6
requires.

**Rationale:** Every later sub-step is a mechanical rewrite over thousands of sites. Without the tooling
the rewrites are hand edits, and without the gates their completeness is unverifiable. The tooling is kept
whole rather than split because these pieces share one validator, one register contract, and one set of
tests, and because splitting them would put a hard dependency across a sign-off boundary for no gain.

TOOL-1 also builds the residual gate §4.7 defines: per enumerated class, compute the broad-predicate
set, subtract the enumerated members and the class's residual register, and fail tier 0 on a non-empty
remainder naming each member and its class. It scans the read domain §4.7 states, which is the tracked
tree less the read exclusion §4.6 states, less the further root-level records N3 names for the
reserved-phrase and identifier classes, and less every residual register and every pass or baseline
register a class's predicate would match as tree content. It is a `tests/tier0_static` Go test reading the per-class
residual registers rather than new machinery, and it is what makes every enumeration in this proposal
safe to be incomplete. Each class has a residual register of its own at
`tests/registers/residual-<class>.yaml`, distinct from the register or baseline that drives the class's
pass, because a pass register is keyed for its rewrite and several of them are emptied as the rewrite
completes. A residual register carries the entry schema §4.7 states, which is a member, a
class, an `in-class` or `excluded` disposition, and a reason, rather than the shared register contract's
entry schema, because an exclusion is permanent, an in-class entry is retired by the event that takes its
member out of the class where the class has one and is permanent for the same reason an exclusion is where
it has none, so no entry is retired by a date, and the shared contract fails an entry whose expiry has
passed or whose blocker resolves to no open item. Each residual register is seeded with today's measured
population so the check lands green on a real population. In a class whose members can leave it the run
that takes a member out removes that member's entry in the same run, per §4.7, so a rewrite that empties a
class also empties the in-class part of its residual register, and a tracked path that gains a glob key or
a skip reason that gains a category drops out of its register on the same downward rewrite its baseline
performs. In the generated-artifact class no member leaves the class, so its in-class entries persist and
the gate stays green on them run after run. Narrowing a predicate to reach green is the one closure route the check does not accept.

The check for a class lands in the sub-step that seeds that class's registers, per the sequencing 0064 §3.5 states, because the
broad predicate matches a live population until the residual register exists. TOOL-1 lands the checks for
the classes whose registers it seeds, which are the two line-citation classes over
`tests/registers/residual-line-citations.yaml` and
`tests/registers/residual-line-citation-resolution.yaml`, the generated-artifact class over
`tests/registers/residual-generated-artifacts.yaml`, whose pass driver remains the generated-artifact
denylist in `scripts/specshift`,
the change-graph coverage class over `tests/registers/residual-change-graph-coverage.yaml`, and the
skip-reason class over `tests/registers/residual-skip-reasons.yaml`.

The generated-artifact class's broad predicate is the union §4.7 states, which is a generation marker in
the file header or in top-level document metadata, or membership in the output set of a producer §4.6
names. That is the same union §4.6 states as its per-file write exclusion, so the residual scan, the
exclusion, and the `scripts/specshift` denylist all range over one predicate and cannot drift apart. The
marker branch alone under-selects the tree, because `charts/lenny/crds/` and `pkg/embedded/crds/` carry no
generation marker, so the second disjunct is what makes the class complete. The check derives the producer
output sets from the producer list §4.6 states, together with the chart-to-embedded copy, rather than by
running a producer, so it is decidable at tier 0.

The reserved-phrase class over
`tests/registers/residual-reserved-phrases.yaml` lands in SPEC-1, the identifier class over
`tests/registers/residual-identifiers.yaml` in SPEC-2, and the anchor class over
`tests/registers/residual-anchors.yaml` in SPEC-3, each with the sub-step that seeds the class's pass
register.

**Change (staged description).** Build `scripts/specshift` with four passes: a name pass that removes
reserved bare noun phrases from prose, an identifier pass that rewrites a channel identifier across code,
schemas, SDKs, charts, and documentation, an anchor pass that rewrites a retired section anchor to its
successor, and a line pass that rewrites or retires a line citation. Each pass is driven by a register
file and carries a dry-run mode whose output is the entry criterion for applying it. `scripts/specshift`
ships with its own `run_test.go`.

This sub-step builds only the gates whose route to green it also supplies, which are the citation
resolver, the line-citation ratchet, the proto no-drift test, the change-graph completeness check, the
skip-reason classifier, the shared register contract, and the residual gate with the residual checks for
the classes whose registers this sub-step seeds. The naming lint,
the heading walker, and the identifier-resolution gate are red against the unmodified tree, and their
routes to green are content changes that later sub-steps make: the reserved-phrase removal, the
`spec/README.md` rows and `tests/spec-map.json` keys, and the collapse of each retired spelling to one
canonical identifier. Each of those three therefore lands in the sub-step that makes it green, per the sequencing 0064 §3.5 states,
so tier 0 is green at the exit of every sub-step and no gate is staged twice. The gate-integrity meta-gate
lands in SPEC-4 for the same reason, because its fixed list names gates that SPEC-1 through SPEC-4
register and it is red anywhere earlier.

Seed the citation resolver's baseline, `tests/registers/line-citation-resolution.yaml`, in the same
sub-step that builds the resolver, per §4.6. The resolver is red on introduction against roughly 1,500
already-stale citations, so the gate is stated as failing a citation that neither resolves inside its
section nor appears in the baseline, and every sub-step below takes "no new resolver failure relative to
the baseline" as its criterion rather than "the resolver is green".

Build the citation resolver and the line-citation ratchet as Go tests under `tests/tier0_static/` or as
checks in the map validator, because
those are the two channels the repository hard-gates. A gate delivered as a shell script under `scripts/`
is not durable here: the repository's lint invocation is downgraded to a non-fatal warning, several tier-0
checks are non-fatal, and a set of checks pass silently when their script is absent. One documented
enforcement location in the tree names a script that does not exist, which is the same failure this whole
remediation addresses, occurring inside the test infrastructure.

Add a tier-0 Go proto no-drift test that reproduces the whole `make generate-proto` target into a
temporary directory and diffs the result against the committed stubs under `pkg/proto/`, for the same
reason. `scripts/check-proto-generated.sh` cannot serve: it exits 0 when `buf` is absent, and it exits 0
whenever `schemas/buf.gen.yaml` carries no uncommented `remote:` plugin line, which is the repository's
state today, so it certifies `pkg/proto/` unconditionally. `pkg/proto/` is the only generated artifact
whose producer is not reachable from `make generate`, and four sub-steps rewrite its source, which are
SPEC-1 with the name pass, SPEC-2 with the identifier pass, and SPEC-3 and SPEC-4 with the line pass.

The test reproduces the target's `PATH="$(go env GOPATH)/bin:$PATH"` prepend, runs `buf generate`, and
then applies the target's two post-generation steps, which are the
`// SPDX-License-Identifier: MIT` prepend and `goimports -w -local github.com/lennylabs/lenny`, before it
compares (`Makefile` lines 91 through 100, with `GOPATH_BIN` defined at `Makefile` line 20). The `PATH`
prepend is part of the target rather than an environment detail, because `schemas/buf.gen.yaml` lines 16
through 21 declare `protoc-gen-go` and `protoc-gen-go-grpc` as `local:` plugins that `buf generate`
resolves from `PATH`, and `scripts/setup-dev.sh` lines 390 and 391 install both into `GOPATH/bin`.
Diffing raw `buf generate` output against the committed stubs
would report drift on every generated file at introduction, because the plugins emit neither the SPDX
header nor the regrouped import block. The test is verified green against the unmodified tree in TOOL-1,
both on a workstation provisioned by `scripts/setup-dev.sh` and in the continuous-integration job that
runs tier 0, before SPEC-1, SPEC-2, SPEC-3, and SPEC-4 take it as an exit criterion, and TEST-1 names its
cases, because a test
that returns early when any of `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`, or `goimports` is absent
reproduces the fail-open behavior that disqualifies the shell script it replaces.

Green in the second of those environments needs a workflow change, which TOOL-1 stages with the test, per
§4.6. No job under `.github/` installs `protoc-gen-go` or `protoc-gen-go-grpc` today, and three of the
four jobs that run the static tier install `buf` and `goimports` no more than they install the plugins, so
the producer cannot run in any of the four and the test would record `UNVERIFIED` on every run, which ends
the run non-zero (`cmd/lenny-test/cmd_run.go` line 428) and leaves `pkg/proto/` unverified in every
environment that enforces the gate. TOOL-1 therefore stages the whole binary set rather than the plugins
alone. The `static` job in `.github/workflows/pr.yml` gains a `go install` of `protoc-gen-go` and
`protoc-gen-go-grpc` into `$(go env GOPATH)/bin` in the install step that already covers `goimports` and
`buf`. The `pr-fast` job in the same file, the gate job in `.github/workflows/phase-gate.yml`, and the
full-system load job in `.github/workflows/weekly.yml` each gain a `~/go/bin` cache restore, a
conditional install of all four binaries, and the `Add tool bin to PATH` step that the `static` job
carries today, and `pr-fast`'s timeout rises from 5 minutes to 10 to cover a cold install. The three added
restore steps use `actions/cache/restore` rather than `actions/cache`, so they read the shared key without
saving to it, and the `mutation` job's existing read-write step in `.github/workflows/nightly.yml` lines
184 through 189 is converted to `actions/cache/restore` at the same pinned commit, so that the `static`
job becomes the only writer, for the reason §4.6 gives. TOOL-1 adds the
two plugins to the `buf` group of the shared tool list in
`.github/workflows/reusable/tool-cache.yml` so one list names the producer's binaries, and bumps the
`~/go/bin` cache key to `lenny-go-bin-v3` in every workflow that restores it, because an exact hit on the
existing key skips the install step and the cached directory carries neither plugin.

Build the shared register contract every gate in the remediation plan uses, with an entry schema carrying
a subject, a verdict, an owner, an opened-at date, an expiry, a blocker, and a reason, and with three
ratchet rules: an unregistered violation fails, a passed expiry fails, and a blocker that does not resolve
to an open item fails. The pattern already exists in tree in two pending-list files and is generalized
here rather than invented.

Add the remaining tooling the plan's step three carries, which is included in this proposal because it
shares the validator, the register contract, and the same authors:

- **Change-graph completeness**, so a change that should have propagated to a derived artifact cannot pass
  unnoticed. The predicate is the reverse of the one that runs today: `validateChangeGraphFileExistence`
  walks the glob keys of `tests/change-graph.json` and confirms each resolves on disk
  (`cmd/lenny-test/cmd_validate.go` lines 272 through 316), while completeness fails a tracked source path
  that no glob covers. The new check lands in `runValidateMaps` (`cmd/lenny-test/cmd_validate.go`), which
  runs inside the `validate-maps` tier-0 check and hard-fails the tier
  (`cmd/lenny-test/cmd_run.go` lines 734 and 742), so this is a behavior change to a running gate rather
  than new tooling and TEST-1 names its cases. The check is red on introduction and is seeded with a
  baseline in the same sub-step that builds it, in the way §4.6 states for the citation resolver rather
  than through the shared exception register. The unmapped population measured over `git ls-files` is on
  the order of 750 of the 1,378 tracked non-test `.go` files under `pkg/` and `cmd/`, spread over roughly
  340 of their 498 package directories, and it includes shipped packages such as `pkg/adapter`,
  `pkg/preflight`, and `pkg/gateway/mcpfabric/mcptools`. The shared register cannot carry that population,
  because its third ratchet rule fails an entry whose `blocker` does not resolve to an open item and a
  package absent from the change graph has no pending step to name, which is the same argument the
  heading walker makes at a tenth of the scale. `tests/change-graph-pending.txt` cannot carry it either,
  because that file lists change-graph glob keys for paths committed ahead of their implementation, which
  is the inverse population, and `readPendingPaths` matches an entry by exact string equality on the glob
  key (`cmd/lenny-test/cmd_validate.go` lines 292 through 295 and 637 through 654). TOOL-1 therefore seeds
  `tests/registers/change-graph-coverage.yaml`, keyed by path prefix, with today's unmapped paths, and the
  gate fails a tracked source path that is neither covered by a glob in `tests/change-graph.json` nor
  covered by a prefix in that baseline. The baseline is rewritten downward only, so a path that gains a
  glob key cannot lose it again, and TOOL-1 records the exact count its own check produces.
- **An `UNVERIFIED` verdict state**, so a check that could not reach a conclusion is distinguishable from
  one that passed. The verdict type already carries a third state with a promotion switch, so the
  aggregation side is one constant and one branch rather than new machinery. It changes the verdict
  aggregation and the exit-code contract CI reads, so TEST-1 names its precedence, exit-code, and
  fail-open cases in `cmd/lenny-test/verdict_test.go`. The aggregation side alone is not enough, because
  nothing in the tree can produce the status for a tier-0 Go test to carry. `runStaticTier` composes tier 0
  as a table of checks whose result is a Go error, the `go test ./tests/tier0_static/...` entry included
  (`cmd/lenny-test/cmd_run.go` lines 717 through 720), and the composing loop collapses every check to two
  values, returning `"fail"` on the first error and `"pass"` otherwise
  (`cmd/lenny-test/cmd_run.go` lines 747 through 753); the only other value the tier emits is `"skipped"`
  when `go` is absent (line 473). `recordTier` reclassifies only a failing tier, only into `inconclusive`,
  and only on a fixed infrastructure-pattern list that names no toolchain absence
  (`cmd/lenny-test/verdict.go` lines 235 through 237 and 279 through 309). TOOL-1 therefore also lands the
  producer: the check table carries a per-check status, the `go test ./tests/tier0_static/...` entry parses
  a sentinel line a tier-0 test writes to report that it could not reach a conclusion, and the composing
  loop propagates that status instead of collapsing it. Without the producer the proto no-drift test has
  only the two outcomes TEST-1 rules out, which are a hard `FAIL` on a tree with no drift and an early
  return that reproduces the fail-open behavior of the shell script it replaces. TEST-1 names the
  producer's cases in `cmd/lenny-test/cmd_run` alongside the aggregation cases. TOOL-1 also moves the two
  constant blocks out of `package main` into a new `cmd/lenny-test/verdictstatus` package as exported
  constants, updating the callsites in `cmd/lenny-test/verdict.go` and `cmd/lenny-test/cmd_run.go`, which
  are the only two files that reference them. The move is what makes the tier-11 reconciliation test below
  possible, because Go forbids importing a `main` package and the constants are unexported today
  (`cmd/lenny-test/verdict.go` lines 3 and 23 through 33), so without it that test could only restate the
  values and go stale with the sentence it guards. An ordinary sub-package under `cmd/` that tests and
  `pkg/` import already exists in `cmd/lenny-ctl/runtimescaffold` (`pkg/ctlcli/runtime.go` line 15 and
  `tests/tier3_contract/sdks/runtime_sdk_test.go` line 38), so this extends an in-tree pattern rather than
  adding a parallel one. `TESTING.md` owns both
  changed enums in prose and is amended in the same change. §7 field semantics states that the verdict is
  one of `PASS`, `FAIL`, and `INCONCLUSIVE` (`TESTING.md` line 521), the next sentence in the same list
  states that `tiers.<name>.status` is one of `pass`, `fail`, `skipped`, and `not-selected`
  (`TESTING.md` line 522), and §21.3 states the retry-then-`FAIL` path for the one non-`PASS`, non-`FAIL`
  verdict value (`TESTING.md` line 2572). All three become false the moment the harness emits the new
  values, and a CI consumer reading §7 to interpret `tests/results/latest.json` mis-parses them. The
  producer widens both enums, because the per-tier status is the field the new state lands on first
  (`cmd/lenny-test/verdict.go` line 68 serializes it as `status`) and the verdict is what the aggregation
  derives from it. The §7 verdict sentence gains
  `UNVERIFIED` with its meaning, which is that a check could not reach a conclusion, and its exit code
  distinct from the `INCONCLUSIVE` code; the §7 tier-status sentence gains `unverified` with the same
  meaning stated for a tier, which is that a check in that tier could not reach a conclusion, and gains
  `inconclusive`, which the harness already emits for a tier whose failure `classifyInfraFailure`
  reclassifies (`cmd/lenny-test/verdict.go` lines 236 and 237) and which that sentence has never
  enumerated, because the tier-11 test TEST-1 adds derives the documented set from the constant block and
  is red at introduction while the omission stands; and the
  §21.3 sentence distinguishes the infrastructure-failure
  `INCONCLUSIVE` path from the new `UNVERIFIED` path. No substitution rule produces those three sentences,
  so that edit is hand-authored and carries the class row §3.4 adds for it, and the two §7 sentences are
  held to the emitted constants afterwards by the tier-11 reconciliation test TEST-1 adds. The rest of `TESTING.md` is inside
  the name and identifier passes' walk, per the root-contract-document row §3.4 adds and the N3 scope.
- **The additional `tests/spec-map-exceptions.yaml` fields and the reason class** the new specification
  sections need to register a heading whose implementation is pending. The reason class is
  `pending-implementation`, and the fields it requires are `blocker`, naming the remediation step that
  implements the heading, and `opened_at`, the date the entry was written. Both field names are taken from
  the shared register contract's entry schema rather than invented, so one vocabulary covers both
  registers. This is a behavior change to a running tier-0 gate rather than new tooling:
  `validateSpecMapExceptionsYAML` hard-codes the accepted reason set
  (`cmd/lenny-test/cmd_validate_yaml.go` lines 185 through 193) and the three-field entry struct
  (lines 168 through 175), and it runs inside the `validate-maps` tier-0 check
  (`cmd/lenny-test/cmd_run.go` lines 734 and 742). Widening it changes both its accept and its reject
  predicate, and the new class is the route by which the heading walker lands green for a heading with no
  spec-map key, so TEST-1 names its cases.
- **An AST-based skip-reason classifier.** Its target is `tests/tier0_static/` and
  `tests/registers/skip-reasons.yaml`. The existing convention is implemented in a downgraded shell script
  whose allow pattern matches any `t.Skipf` regardless of reason
  (`scripts/lint-test-conventions.sh` line 102), invoked non-fatally and passing when the script is absent
  (`cmd/lenny-test/cmd_run.go` lines 690 through 696), so the convention is unenforced today. Parsing the
  syntax tree rather than the string is what enforces the convention. The predicate is: a `t.Skip` or
  `t.Skipf` call whose first argument is a string literal must open with one of the reason categories
  `cmd/lenny-test/cmd_validate.go` lines 853 through 865 already enumerate for §17.9, which are
  `not implemented:`, `blocked:`, `phase-gated:`, `not-yet-applicable:`, `not yet applicable:`,
  `flaky-time:`, `flaky-network:`, `flaky-ordering:`, and `quarantined:`; a call to a `SkipUnless*` harness
  helper and a bare `t.Skip()` with no reason are accepted, because neither states a reason the classifier
  can read; a reason built from a non-literal expression is reported against the register rather than
  accepted silently; and a file that does not parse fails rather than being skipped. The check is fatal at
  tier 0 rather than a warning, which is the point of moving it out of the shell script. It is red on
  introduction against both call forms, because the predicate reads a string-literal reason wherever it
  appears rather than only in the formatted call. A measurement over `tests/`, `pkg/`, and `cmd/`, counting
  a call whose first argument is a string literal that opens with none of the categories and discarding a
  line whose code is commented out, with the comment test anchored to the start of the source line so that a
  reason carrying a URL is not discarded, finds 204 `t.Skipf` sites and a further 189 `t.Skip` sites,
  so the seeded population is on the order of 390 sites rather than the `t.Skipf` sites alone,
  and TOOL-1 records the exact count its own classifier produces, in the way §4.6 states for the citation
  resolver. Almost all of them are permanently-correct host-capability skips, such as
  `tests/testinfra/kind/kind.go` line 104 and `tests/testinfra/envtest/envtest.go` line 94 in the formatted
  form and `tests/testinfra/kind/kind.go` line 71 and `tests/testinfra/chaos/chaos.go` line 52 in the
  unformatted form, so TOOL-1 seeds
  `tests/registers/skip-reasons.yaml` with both populations, keyed by file and call site, and the baseline is
  rewritten downward only. The non-literal branch is red on introduction too, and its population is seeded
  into the same register in the same run: ten `t.Skip` calls across five files pass a first argument the
  classifier cannot read as a literal, at `tests/testinfra/kind/install.go` lines 56, 59, 93, 108, and 116,
  `tests/testinfra/matrix/matrix.go` line 102, `tests/tier5_e2e_kind/gvisor_isolation_test.go` line 105,
  `tests/tier5_e2e_kind/execution_modes_test.go` lines 304 and 314, and
  `tests/tier9_security/adapter_peercred_test.go` line 125. None of them is a `SkipUnless*` helper call, so
  a classifier that accepted them silently would ship with the hole it replaces, and none of them is
  covered by the two figures above, which count string-literal reasons alone. The shared exception register cannot hold that population, because its entry
  schema requires an owner, an expiry, and a `blocker` resolving to an open item, and a skip that fires
  because Docker is absent has no pending step to name and no date on which it becomes wrong. This is the
  same construction §4.6 gives the citation resolver and the ratchet, and TEST-1 names the classifier's
  cases.


### TEST-1. Gate cases

**Target:** `scripts/specshift`'s `run_test.go`, `cmd/lenny-test`'s `cmd_validate_test.go` and
`verdict_test.go`, `tests/tier0_static/`, `tests/tier11_docs/verdict_enum_test.go`, new, and the
`testdata/` fixture directory beside each of them.

**Rationale:** Each gate closes the loop on one class of rewrite. A gate whose own accept, reject, and
boundary behavior is untested is an assertion about the tree rather than a check on it.

**Change (staged description).** This sub-step adds each gate's cases.

Every case that has to present the retired citation form, a retired anchor, or a reserved noun phrase
verbatim holds that text in a `testdata/` fixture file the test reads rather than in a Go string literal
in the test source. `testdata/` is outside the read domain of the resolver, the ratchet, and the residual
scan per §4.6, so the citation spellings the cases below quote do not enter the resolver's baseline, do
not raise a per-file ratchet count, and are not reported as residuals, and tier 0 is green at this
sub-step's exit. A fixture in the test source would fail all three, because the fixture files are new and
are therefore absent from the baselines TOOL-1 seeds from today's measured population, and those baselines
are rewritten downward only.

Each pass also carries a case for the write exclusion §4.6 states, because that exclusion is the one
boundary no gate observes. The files it covers sit outside the read domain of the resolver, the ratchet,
and the residual scan, so a pass whose walk did not honor it would rewrite a historical record while every
gate stayed green, and the population at risk is large: `BUILD-GAPS.md` carries 1,831 lines matching the
retired citation form and `TEST-GAPS.md` carries 48, measured at commit `c7d0f7f8`, alongside
`proposals/`, the two root planning documents, and the three build and queue records. Each case below is
stated over a fixture tree that reproduces those paths, asserts that the excluded file is byte-identical
after an applied run and appears in neither the dry-run output nor the applied diff, and asserts that an
equivalent site in an ordinary carrier in the same run is rewritten, so the case cannot pass through a
pass that rewrote nothing.

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
gate reports; a
malformed or missing residual register fails rather than certifying the
class; a run whose scan selects zero files over a tracked tree that is not empty fails and names the
class rather than reporting no residual, so a walk root or an exclusion list that silently excludes
everything is a failure rather than a green result; and a run whose scan selects files while the class's
broad predicate selects zero members passes, because an emptied class is the terminal state the pass and
the remediation reach rather than a defect, unless the class's residual register still carries an
`in-class` entry, which is the register-versus-tree inconsistency the in-class survival case above fails. Each class has its own `tests/registers/residual-<class>.yaml`, separate from the register or
baseline that drives the class's pass, and it carries the entry schema §4.7 states, which is a
member, a class, an `in-class` or `excluded` disposition, and a reason, so the shared register contract's
expiry and blocker ratchet rules do not range over them. The gate is built in TOOL-1, and the check for
each class lands in the sub-step that seeds that class's registers, per the sequencing 0064 §3.5 states. Like every gate here
it lands green by seeding today's population into the register rather than by narrowing the predicate.

Add the register-contract validator tests in the style of the two in-tree validators the contract
generalizes (`cmd/lenny-test/cmd_validate_yaml_test.go` lines 241-400), one case per ratchet rule: a
violation absent from the register fails, an entry whose expiry has passed fails, an entry whose blocker
does not resolve to an open item fails, a malformed or missing register file fails rather than passing
silently, and a well-formed register passes. Every gate that uses the shared contract lands green by
seeding that register, so a rule that is silently a no-op would certify an exempted tree indefinitely. The
heading walker, the line-citation ratchet, the citation resolver, the change-graph completeness check, and
the residual gate land green by their own baselines instead, for the reasons stated at the end of this
sub-step.

Add the change-graph completeness check's own cases in `cmd/lenny-test/cmd_validate_test.go`, for the same
reason: it is a new accept-and-reject predicate on the hard-gated `validate-maps` tier-0 check, and the
existing change-graph checks carry no unit case in tree today. The cases are: a tracked source path
covered by neither a glob in `tests/change-graph.json` nor a prefix in
`tests/registers/change-graph-coverage.yaml` fails and names the path; a path covered by a prefix in that
baseline passes; a path that gains a glob key is removed from the baseline in the same run and the
baseline is rewritten downward, and a run that would add a prefix to the baseline fails, so coverage
cannot be given back; a malformed or missing `tests/registers/change-graph-coverage.yaml` fails rather
than certifying the tree; a malformed or missing `tests/change-graph.json` fails
rather than certifying the tree, which is the fail-open outcome the register-contract cases above rule out
for the other registers; a run whose walk selects zero tracked source paths fails and names the check
rather than reporting full coverage; and a fully mapped tree passes. Add one further case pinning the interaction with
the identifier pass at this register: a file the pass renames leaves the change-graph completeness check
green, because the pass rewrites the glob key in `tests/change-graph.json` in the same run that moves the
file. The other registers are pinned in `scripts/specshift`'s `run_test.go` rather than here, because
`validate-maps` existence-checks the change-graph glob keys, the `spec_file` pointer, and the
`::<symbol>` references, and deliberately does not existence-check the `schemas` paths
(`cmd/lenny-test/cmd_validate.go` lines 236 through 238), so a `validate-maps` result cannot observe a
stale `schemas` entry or a stale citation baseline. The cases carry no `// spec:`
tie, matching the validator cases they sit beside, because the map validator is test infrastructure.

Add a case-per-rule battery for the widened `tests/spec-map-exceptions.yaml` validator, in
`cmd/lenny-test/cmd_validate_yaml_test.go` alongside the four cases that pin its current behavior
(`TestValidateSpecMapExceptionsHappy` at line 187, `TestValidateSpecMapExceptionsUnknownReason` at line
204, `TestValidateSpecMapExceptionsMissingJustification` at line 215, and
`TestValidateSpecMapExceptionsDuplicateSection` at line 225). TOOL-1 widens both the accepted reason set
and the entry schema of a validator that runs inside the `validate-maps` tier-0 check, and the
`pending-implementation` class is the route by which the heading walker lands green for a heading with no
spec-map key, so an entry that passes with an empty field exempts that heading permanently, which is the
suppression outcome this sub-step's standing rule forbids. The cases are: an entry under
`pending-implementation` carrying `blocker` and `opened_at` passes; an entry under that class missing
either field fails and names the section; an entry under that class whose `blocker` does not name an open
step fails; an entry whose `opened_at` is not a date fails; a reason value outside the widened allowlist
still fails; and `blocker` and `opened_at` on an entry under one of the existing reason classes are
validated by the same rules. The cases carry no `// spec:` tie, matching the four they sit beside, because
the exceptions validator is test infrastructure rather than a spec behavior, which
`.claude/rules/spec-driven-development.md` names as an escape hatch from the annotation requirement.

Add the name pass's own cases, in `scripts/specshift`'s `run_test.go`, because the
fail-on-unregistered-site rule is what substitutes for a gate on the wrong-mechanism class and a silent
default substitution would pass the naming lint and the identifier-resolution gate while converting every ambiguous sentence
into a precise false one: a reserved-phrase occurrence with no entry in
`tests/registers/reserved-phrase-senses.yaml` aborts the pass non-zero, names the file and the line, and
leaves the tree unmodified; a site whose entry resolves to a canonical identifier is substituted; an entry
whose identifier is not a declared §28 identifier fails rather than substituting; a site whose correct
sense is a link is substituted with that link identifier rather than collapsed onto a channel or aborting
the pass, with the `allow-pod-egress-base` sites at `spec/13_security-model.md` lines 79, 92, and 100
resolving to `LNK-GWCONTROL` as the worked case; a site whose entry carries more than one identifier is
substituted with each at the position the entry records rather than collapsed onto one, with the mTLS
handshake metric at `spec/16_observability.md` line 51 resolving to `LNK-POD-GRPC` for `gateway_to_pod`
and `CH-LLMPROXY` for `pod_to_gateway` as the worked case; a reserved-phrase site in a Go doc comment and
a site in a `schemas/` JSON `description` value are each substituted, so the pass writes every surface the
naming lint reads; a markdown anchor identifier carrying a reserved phrase is left unmodified and requires
no register entry, with the kramdown attribute `{: #lifecycle-channel }` at `docs/reference/glossary.md`
line 207 and the same-page fragment link at `docs/api/internal.md` line 229 as the worked accept cases,
with the matching assertion that the naming lint is green on those same two sites left to SPEC-1, where
the lint lands, because the lint does not exist at this sub-step's exit; a site
in a file the generated-file exclusion covers is left unmodified; a reserved-phrase site in each file
group of the write exclusion §4.6 states, over a fixture tree carrying `proposals/`, `BUILD-GAPS.md`,
`TEST-GAPS.md`, `gateway-runtime-comms.md`, `gateway-runtime-comms-remediation.md`, `BUILD-PLAN.md`,
`BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`, is left byte-identical and appears in neither the dry-run
output nor the applied diff, while an equivalent site in an ordinary carrier in the same run is
substituted; a malformed or
missing sense register fails rather than passing with zero substitutions; and
the dry-run output equals the applied diff for the same input. Each case carries a `// spec:` tie to the §28.1 naming law.

Add the identifier pass's own cases in the same `run_test.go`, because the retired `LifecycleChannel`
spelling denotes both renamed channels inside package `adapter` and the identifier-resolution gate reads
the forward relation only, so it does not observe one spelling resolved to the wrong identifier: an
occurrence of a retired spelling with no entry in `tests/registers/identifier-senses.yaml`
aborts the pass non-zero, names the file and the line, and leaves the tree unmodified; an occurrence whose
entry resolves is substituted to that entry's canonical identifier; a retired spelling the §28.3 table
maps to exactly one channel, occurring in unrelated text such as a command-line file argument, aborts the
pass when it has no entry and is left unmodified when its entry records it as not a channel; a retired
spelling in each file group of the write exclusion §4.6 states, including `proposals/`, `BUILD-GAPS.md`,
`TEST-GAPS.md`, the two root planning documents, and `BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and
`PROPOSAL-QUEUE.md`, is left byte-identical and appears in neither the dry-run output nor the applied
diff, while an equivalent occurrence in an ordinary carrier in the same run is substituted; and a gRPC
full-method string literal is resolved from the proto RPC row rather than from the Go type row. Add the
register re-key cases in the same file, asserted by reading each register after the run rather than by a
`validate-maps` result: a run that renames a file rewrites that file's key in every path-keyed register
§4.6 names, including the `schemas` array entries in `tests/spec-map.json` that no check
existence-checks; a run that renames a symbol rewrites every `::<symbol>` reference to it in
`tests/spec-map.json`, so the `validate-maps` test-function check stays green; and a run that leaves a
register carrying the old key or the old symbol fails rather than completing. Add the same dry-run case
the other three passes carry: the dry-run output equals the applied diff for the same input, including the
file moves and the register re-key edits. The case is stated for this pass because the dry run is the
entry criterion TOOL-1 gives for applying it, this is the pass whose applied change is the largest and the
hardest to reverse, and no other listed test observes a divergence, since the register cases read the
tree after the run and the identifier-resolution gate runs after the pass has been applied. The tier-0 assertion over
`coordinatorHoldAllowedMethods` (`pkg/adapter/holdstate.go` line 54) is not staged here. SPEC-2 lands it
alongside the identifier-resolution gate, in the sub-step that runs the identifier pass over those
literals, so the assertion is staged once and this proposal stages no file for it.

Add the anchor pass's own cases in the same `run_test.go`, for the same reason the other three passes have
them. No gate over the anchor classes distinguishes a correct run from a silent no-op or a
destructive one. §3.4 row 3 names this pass's own abort behavior as its proof for exactly that reason, and
the row-4 fragment-link gate reads markdown
links only and passes a link redirected to the wrong existing heading. A bare `§15.4.1`-style anchor
citation in a comment or in prose is read by neither, because the citation resolver and the ratchet match
the retired line-citation form alone. SPEC-4 then empties
`tests/spec-anchor-moves.json`, so a pass that resolved zero sites, aborted silently on a malformed map,
or substituted a default destroys its own record with every gate green. The cases are: a citation naming a
retired anchor with an entry in the map is rewritten to that entry's successor; a citation naming a
retired anchor with no entry aborts the pass non-zero, names the file and the line, and leaves the tree
unmodified rather than substituting a default; a malformed or missing anchor-move map fails rather than
passing with zero rewrites; an entry whose successor heading does not exist fails before any write; a
`[...](NN_file.md#anchor)` link into a retired anchor is rewritten while a link into a surviving anchor is
left untouched; a same-page `[...](#anchor)` link into a retired anchor is rewritten while a same-page
link into a surviving anchor is left untouched, which is the majority form inside
`spec/15_external-api-surface.md`; a bare `§15.4.1`-form citation whose occurrence
`tests/registers/anchor-senses.yaml` does not record aborts the pass non-zero, names the file and the
line, and leaves the tree unmodified rather than substituting the anchor map's single successor; a bare
citation the register records as citing carved-out material is rewritten to the surviving `spec/15`
heading, taking the `MessageEnvelope` citation at `sdks/runtime/go/runtime/types.go` line 52 as the worked
case; a retired anchor carried in each file group of the write exclusion §4.6 states, including
`proposals/`, the two historical audit records, and the two root planning documents, is left
byte-identical and appears in neither the dry-run output nor the applied diff, while an equivalent
citation in an ordinary carrier in the same run is rewritten; and the dry-run output equals the applied
diff for the same input. Each case carries a
`// spec:` tie to N8, the §28.1 citation rule.

Add the line pass's own cases in the same `run_test.go`. The two gates §3.4 names for this class cannot
distinguish a correct conversion from a destructive one, because the ratchet counts citations and the
resolver only validates a citation that still exists, so a pass that deletes a citation instead of
converting it produces count zero, no resolver failure, and a greener gate result than a correct
conversion, while discarding the anchor this proposal exists to establish and breaking the standing rule
that spec-derived logic carries a spec citation (`.claude/rules/code-best-practices.md` line 57). The
dry-run diff is not a substitute at a scale of 2,353 Go files and 264 non-Go files, by §3.4's own
argument. The cases are: a `§X.Y line N` citation in a Go comment becomes the anchor form with the
citation retained rather than removed; a section-level `§X line N` citation becomes the anchor for the
section it names; a comma-list citation such as `§4.8 lines 1057-1058, 1077` becomes a single anchor
citation with no line number left behind, so the pass consumes the whole list rather than its head; a
slash-continuation citation such as `§10.7 line 694 / line 743` and an `and`-continuation citation each
become a single anchor citation with no line number left behind, for the same reason; a
`+`-continuation citation whose members carry trailing glosses, such as
`§10 line 437 ("...") + line 443 ("...")` at `pkg/preflight/crdschema.go` lines 22 through 25, becomes a
single anchor citation with no orphan integer left behind, so a gloss does not terminate the match; a
member list that
repeats the keyword, such as `§10.6 line 601, line 629`, is consumed whole; a qualified citation such as
`§11.7 item 3 line 364` becomes the anchor for the section it names with the qualifier carried through; an
en-dash range citation such as `§4.4 lines 263–291` becomes a single anchor, so the range separator is not
read as an ASCII hyphen alone; a path-form citation such as `spec/04_system-components.md line 1145`
becomes the anchor for the section that contains the cited line; a colon-form citation becomes a single
anchor citation in both its variants, with `§15.1:805-812, 876-878` at
`pkg/gateway/externalapi/admin/me.go` line 109 as the section-number worked case, which also shows the
colon and comma-list spellings composing, and `spec/15_external-api-surface.md:1315` at
`tests/tier3_contract/rest_mcp_consistency/published_schema_test.go` as the path worked case; a path-form citation naming a file that
does not resolve under `spec/`, such as `11_security-trust-model.md line 414` at
`pkg/audit/ocsf/catalog.go` line 149, fails the pass and is reported for hand correction rather than being
converted against a guessed file; a
`§X.Y lines N-M` citation whose endpoints straddle two sections fails the pass and is reported for hand
correction rather than being converted to either section's anchor; a citation wrapped across two comment
lines becomes a single anchor with no line number left behind, with a case per carrier dialect (`//`, `#`,
and `--`) and one case per wrap position, which are a wrap between the section reference and the keyword,
a wrap between the keyword and its first member, and a wrap that straddles a member list, with
`pkg/gateway/sessionserver/messages.go` lines 156 and 157 as the worked case; a citation in a served client artifact
(`pkg/gateway/externalapi/openapi/openapi.json`, `pkg/gateway/mcpfabric/mcptools/mcptools.go`, and the
`desc:` struct tags on `pkg/chart/values/values.go`) is stripped while every other carrier is converted,
with a case per carrier dialect; a stripped served-artifact citation whose authoring source carries no
surviving spec tie after the strip fails the pass rather than being dropped silently, which is the case
the three `pkg/chart/values/values.go` fields with no doc comment sit in per §4.6; a retired-form citation
in each file group of the write exclusion §4.6 states, including `proposals/`, `BUILD-GAPS.md`,
`TEST-GAPS.md`, and the two root planning documents, is left byte-identical and appears in neither the
dry-run output nor the applied diff, while an equivalent citation in an ordinary carrier in the same run
is converted; a citation in a file the per-file generated-artifact rule selects is left unmodified,
including one in `charts/lenny/crds/`, which the rule selects through its producer-output disjunct rather
than through a generation marker, so the pass does not write a file whose route to zero is regeneration; a
run that reduces a
file's citation count without emitting a corresponding anchor fails rather than reporting a retirement;
and the dry-run output equals the applied diff for the same input. Each case carries a `// spec:` tie to N8, the §28.1 citation rule.

Add the `UNVERIFIED` verdict state's own cases in `cmd/lenny-test/verdict_test.go`, mirroring the three
that pin `INCONCLUSIVE` today (`TestRecordTierFailOutranksInconclusive` at line 140, `TestExitCodeFor` at
line 149, and `TestRecordTierSkippedKeepsPass` at line 164). The state is a behavior change to the value
CI gates on, and its purpose is a fail-closed distinction, so the paths that matter are the precedence and
the exit code rather than the happy path. `recordTier`'s switch carries no default branch
(`cmd/lenny-test/verdict.go` lines 245 through 254) and the verdict is initialized to `verdictPASS`
(line 106), so a status the switch does not handle leaves the run at `PASS` and `exitCodeFor` returns 0
(`cmd/lenny-test/cmd_run.go` lines 457 through 466), which is the fail-open outcome the state exists to
prevent. The cases are: a tier recorded as unverified sets the overall verdict to `UNVERIFIED` rather than
leaving it `PASS`; `FAIL` outranks `UNVERIFIED` and `UNVERIFIED` outranks `PASS` regardless of the order
the tiers are recorded in; `exitCodeFor("UNVERIFIED")` returns a non-zero code distinct from the
`INCONCLUSIVE` code, which is 2 today; and a status value the switch does not handle does not leave the
run at `PASS`. Add the producer's cases beside them, over the tier-0 composition in
`cmd/lenny-test/cmd_run.go`, because the aggregation cases pin what happens once the status arrives and
nothing else pins that it can arrive: a tier-0 check reporting that it could not reach a conclusion yields
tier status `unverified` rather than `pass` or `fail`; a failing check in the same run still yields
`fail`, so the new status cannot mask a real failure; and a check that reports nothing yields `pass` as it
does today. The cases carry no `// spec:` tie, matching the three they sit beside, because
`cmd/lenny-test` verdict aggregation is test infrastructure rather than a spec behavior, which
`.claude/rules/spec-driven-development.md` names as an escape hatch from the annotation requirement. A
bare `§7` would resolve to `spec/07_session-lifecycle.md` under the repository's citation convention,
which is Session Lifecycle and states no verdict schema, so the harness would report the new tests as
coverage of that section. The verdict schema is owned by `TESTING.md` §7, which TOOL-1 amends.

Add a tier-11 reconciliation test in `tests/tier11_docs/verdict_enum_test.go` that pins the two amended
`TESTING.md` §7 sentences to the constants the harness emits, because the drift TOOL-1 names is a CI
consumer parsing `tests/results/latest.json` against a stale enumeration, and review does not fail when
the next value is added. `.claude/rules/test-coverage.md` line 42 assigns documentation consistency to
tier 11, `tests/tier11_docs/backup_status_enum_test.go` already reconciles a documented status enum
against the constants the code emits in exactly this manner, and tier-11 tests already read `TESTING.md`
directly (`tests/tier11_docs/docs_test.go` lines 54 through 72), so the surface is reachable with no new
machinery beyond the constant move TOOL-1 stages. The test derives the expected sets by importing the
constants rather than restating them. The precedent works only because its constants are exported from an
importable library package: `backup_status_enum_test.go` is `package tier11_docs_test` and reads
`backup.StatusPending` and its siblings from `pkg/ops/backup` (lines 21 through 41, against
`pkg/ops/backup/service.go` line 16). Neither property holds for the constants as they stand today, since
`cmd/lenny-test/verdict.go` declares `package main`, which Go forbids importing, and every identifier in
the two blocks is unexported (`statusPass` through `statusNotSelected` at lines 24 through 28 and
`verdictPASS` through `verdictINCONCLUSIVE` at lines 30 through 32). TOOL-1 therefore moves both blocks
into the importable `cmd/lenny-test/verdictstatus` package as exported constants, and the test imports
that package and reads the tier-status set and the verdict set from it, each including the value TOOL-1
adds. The cases are: every verdict constant appears in the §7 verdict
sentence; every tier-status constant appears in the §7 `tiers.<name>.status` sentence; and, for the reject
direction, the same assertion run against a fixture sentence with one value removed fails and names the
missing value, so a sentence that stops enumerating a constant cannot pass. The tier-status case is red
against today's file for a reason predating this proposal, because `statusInconclusive` is emitted for a
tier whose failure `classifyInfraFailure` reclassifies (`cmd/lenny-test/verdict.go` lines 236 and 237)
while the §7 sentence enumerates `pass`, `fail`, `skipped`, and `not-selected` alone (`TESTING.md`
line 522) and the lowercase value appears nowhere in the file. TOOL-1's amendment of that sentence
therefore adds `inconclusive` alongside `unverified`, so the test is green at this sub-step's exit under
decision 3. The cases carry no `// spec:` tie, matching the verdict cases above, because the harness
verdict schema is owned by `TESTING.md` rather than by a spec section.

Add the citation resolver's own cases, for the reason the ratchet and the change-graph check have theirs.
The resolver lands green through a seeded baseline of roughly 1,500 pre-existing failures and is the exit
criterion SPEC-3 rests its atomicity on, so a baseline load that degrades to exempting everything reports
no failure for exactly the citations the reduction breaks. The cases are: a citation that resolves inside
the section it names passes; a citation that does not resolve and is absent from
`tests/registers/line-citation-resolution.yaml` fails and names the file, the line, and the citation text;
a citation present in the baseline under the same file and the same citation text passes, while the same
citation text under a different file fails, so a baseline entry does not travel; a baselined non-resolving
citation in a file the identifier pass renamed still passes under the new path after the run, so the
baseline follows the file it was written for; a citation text carried inside
`tests/registers/line-citation-resolution.yaml` itself is outside the read domain and reports no failure,
and the same holds for the ratchet's per-file count over
`tests/registers/line-citations.yaml`, so neither gate reads its own baseline as tree content; a baseline entry whose
citation no longer exists in the tree is removed in the same run, so an exemption cannot outlive the
citation it was written for; a range or multi-member citation fails unless every member resolves; a
citation broken by a heading move fails rather than being absorbed by the baseline; a malformed or
missing `tests/registers/line-citation-resolution.yaml` fails rather than certifying the tree; and a run
whose walk selects zero files fails and names the resolver rather than reporting no failure. Each case
carries a `// spec:` tie to N8, the §28.1 citation rule.

Add the line-citation ratchet's own cases, which the five register-contract cases above do not supply because none of them is a
count comparison: a file already in the register whose count rises by one fails at tier 0 and names the
file; a file at count N that drops to N-1 passes and the register is rewritten to N-1 in the same run; a
file at the rewritten N-1 that returns to N fails, so the retirement cannot be undone; a file at count
zero fails on any new citation, which is the flat-prohibition end state; a file at count zero fails on a
new range citation as well as on a new singular one, so the plural spelling cannot regrow after
retirement; a file at count zero fails on a new section-level citation such as `§10 line 437`, so the
spelling that carries no subsection component cannot regrow either; a file at count zero fails on a new
comma-list citation such as `§4.8 lines 1057-1058, 1077`, counted as one citation rather than as a
converted anchor plus orphan integers, so the list spelling cannot regrow either; a file at count zero
fails on a new slash-continuation citation such as `§10.7 line 694 / line 743`, on a new
`+`-continuation citation with trailing glosses such as `§10 line 437 ("...") + line 443 ("...")`,
counted as one citation rather than as a converted anchor plus an orphan integer, on a new qualified
citation such as `§11.7 item 3 line 364`, on a new en-dash range citation such as `§4.4 lines 263–291`,
on a new path-form citation such as `spec/04_system-components.md line 1145`, and on a new colon-form
citation in either variant, such as `§17.6:404` or `spec/15_external-api-surface.md:1315`, so none of the
remaining spellings can regrow after retirement; a file at count zero fails on a new citation wrapped
across two comment lines, counted as one citation in each of the three wrap positions and in each carrier
dialect, so the wrapped spelling cannot regrow either; a file renamed with its citation count
unchanged passes and its register key moves with it, so a rename is not read as a new file at its first
citation; a file absent from the register fails on its first citation; and a run whose walk selects zero
files fails and names the ratchet rather than reporting every count held. The rise case is the boundary the mechanism exists for, and the fall
case mutates the gate's own baseline, so a downward rewrite that silently does not happen lets a file
regrow to its old count with the gate green. Each case carries a `// spec:` tie to N8, the
§28.1 citation rule.

Add the skip-reason classifier's own cases in `tests/tier0_static/`, because it is a new accept-and-reject
predicate replacing a check that accepts everything: a `t.Skip` or `t.Skipf` whose reason opens with each
of the categories `cmd/lenny-test/cmd_validate.go` lines 853 through 865 enumerate is accepted, one case
per category; a `SkipUnless*` helper call and a bare `t.Skip()` are accepted; a free-text string-literal
reason is rejected and named with its file and line in the `t.Skip` call form as well as in the `t.Skipf`
call form, so the seeded population TOOL-1 measures over both forms is the population the predicate
selects; a
`t.Skip` or `t.Skipf` whose first argument is a non-literal expression is reported and named with its file
and line rather than accepted, with `tests/testinfra/kind/install.go` line 56 as the worked case, because
that branch is where the shell script's silent accept would otherwise reappear; a site
present in `tests/registers/skip-reasons.yaml` under the same file and call site passes while the same
reason at a different site fails; a site whose reason is rewritten to open with one of the categories is
removed from `tests/registers/skip-reasons.yaml` in the same run and the baseline is rewritten downward,
which is the downward rewrite §4.7 rests the in-class residual entry's removal on; a site removed by that
rewrite whose reason returns to free text fails, so the remediation cannot be given back; a run that would
add a site to the baseline fails, so the exemption cannot be given back; a malformed or missing
`tests/registers/skip-reasons.yaml` fails rather than
certifying the tree; a file that does not parse fails rather than being skipped; and a run whose walk
selects zero Go files, or that inspects zero skip call sites, fails and names the classifier rather than
reporting the convention held. The cases carry no
`// spec:` tie, matching the validator cases they sit beside, because the skip convention is test
infrastructure.

Add the proto no-drift test's own cases in `tests/tier0_static/`, because it is the sole `pkg/proto/` exit
criterion SPEC-1, SPEC-2, SPEC-3, and SPEC-4 each declare and its spec-named failure mode is the fail-open path
that disqualifies `scripts/check-proto-generated.sh`. Its producer needs four external binaries, which are
`buf`, `protoc-gen-go`, `protoc-gen-go-grpc`, and `goimports`: `schemas/buf.gen.yaml` lines 16 through 21
declare the two codegen plugins as `local:` entries, so `buf generate` resolves them from `PATH`, and
`scripts/setup-dev.sh` lines 390 and 391 install them into `$(go env GOPATH)/bin` alongside `goimports`
while `buf` is installed as a system package (`scripts/setup-dev.sh` line 308). The test therefore
reproduces the target's `PATH="$(go env GOPATH)/bin:$PATH"` prepend before it invokes `buf`
(`Makefile` lines 91 through 100, with `GOPATH_BIN` defined at `Makefile` line 20), because without it a
tree whose plugins are installed only in `GOPATH/bin` fails the test while `make generate-proto` succeeds.
No continuous-integration job installs all four today, so TOOL-1 installs the whole set in every job that
runs the static tier and bumps the shared `~/go/bin` cache key, per §4.6; without that the cases below
would record `UNVERIFIED` on every run rather than on a degraded environment.
The skip-reason classifier accepts a bare `t.Skip()`, so nothing else would catch a test that returns
early when one of the four is missing. The cases
are: committed stubs matching a reproduced `make generate-proto` run pass; a stub carrying a hand-edited
comment fails and names the file; a run in which any of the four producer binaries is unavailable records
the tier as `UNVERIFIED` rather than passing or failing, through the tier-0 status route TOOL-1 lands with
the state, since the composing loop collapses a check to `pass` or `fail` without it; and a run that
produces zero generated files
fails rather than reporting no drift. The cases carry no `// spec:` tie, matching the validator cases they sit beside, because the
generated-artifact no-drift check is test infrastructure.


## 7. Non-goals

- **Any specification change.** This proposal touches no file under `spec/`. The passes it builds are run
  over `spec/` by proposal 0064, which depends on this one.
- **Running the migration.** The passes land with their own fixture tests and are not run over the tree
  here. A pass that rewrote sites before the registers that resolve them exist would reproduce the failure
  this split corrects.
- **The naming law, the channel identifiers, and the new specification sections.** Those are 0064's
  subject. This proposal names no channel and states no naming rule.
- **The gates whose route to green is a specification change.** The naming lint, the identifier-resolution
  gate, the heading walker, the fragment-link gate, the claim-register validator, the successor-pointer
  check, and the gate-integrity meta-gate all land in 0064, because each one is red until the content
  change that makes it green lands. This proposal builds only the gates that are green on the unmodified
  tree or against a baseline it seeds.

## 8. Findings closed on application

None. This proposal builds tooling for a migration and closes no `BUILD-GAPS.md` finding on its own.

## 9. Resolved in adversarial review

### Pass 1 (2026-07-30, automated)

- **TEST-1's name-pass case asserted the naming lint green, and the naming lint is not built here.** The
  markdown-anchor accept case required the lint to be green on the same two sites, while TOOL-1 states the
  lint is red against the unmodified tree and §7 lists it among the gates that land in 0064. The case now
  keeps the pass-side accept assertion at `docs/reference/glossary.md` line 207 and `docs/api/internal.md`
  line 229, and leaves the matching lint assertion to SPEC-1, where the lint lands, so this proposal
  asserts nothing about a gate that does not exist at its exit.
- **The gate tests' own fixtures had no disposition against the ratchet, the resolver, and the residual
  scan.** Each accept, reject, and boundary case in TEST-1 has to present the retired citation form
  verbatim, and the files carrying it are new, so they are absent from the baselines TOOL-1 seeds and the
  ratchet fails them on their first citation while the resolver fails any that does not resolve. Seeding
  is not available either, because the baselines are rewritten downward only and a fixture citation never
  retires. §4.6 now excludes every `testdata/` directory from the read domain the resolver and the ratchet
  share, on the argument it already makes for the two citation registers, extended from a gate's baseline
  to a gate's input; §4.7 carries the same exclusion for the residual scan; TEST-1 holds every verbatim
  fixture in a `testdata/` file rather than in a Go string literal; and §5 and §11 record the exclusion and
  the new fixture directories. No tracked `testdata/` file carries the retired form today, measured over
  `git ls-files`, so the exclusion removes nothing from the measured population.
- **The `coordinatorHoldAllowedMethods` tier-0 assertion was staged both here and in 0064's SPEC-2.**
  0064 states that landing point in its §3.5, in SPEC-2, and in its own review record, so applying both
  proposals would have added the gate twice, against this proposal's rule that no gate is staged twice.
  The assertion was also absent from TOOL-1's gate list and from §11, so nothing here staged the file it
  lands in. TEST-1 now records that SPEC-2 lands it alongside the identifier-resolution gate and that this
  proposal stages no file for it.
- **The `testdata/` exclusion left §3.3's copy invariant false and silently re-described N3.** §3.3 stated
  that the design sections are carried over unchanged, while the exclusion added to §4.6's read domain and
  to §4.7's residual scan appears in neither of the corresponding paragraphs of 0064, which is approved
  and lands afterward. 0064 measures SPEC-4's Target and its §11 against its own narrower read domain, and
  §4.6's sentence comparing the citation list to the naming list N3 states silently widened N3's list,
  whose text names only the audit records, the planning documents, and the build and queue records. §3.3
  now records the divergence, states that the two paragraphs here supersede their counterparts in 0064 so
  that 0064's read-domain measurements are taken over the domain these gates scan, and fixes N3's
  exclusion list to the superseded citation list plus the three build and queue records.

### Pass 2 (2026-07-30, automated)

- **No continuous-integration job installs the proto producer's codegen plugins, so the proto no-drift
  test could not reach a conclusion where the gate is enforced.** `scripts/setup-dev.sh` is the only
  installer of `protoc-gen-go` and `protoc-gen-go-grpc` in the tree, and nothing under `.github/` installs
  either one, while `schemas/buf.gen.yaml` declares both as `local:` plugins resolved from `PATH`. Every
  job that runs the static tier would therefore record the test as `UNVERIFIED`, which ends the run
  non-zero (`cmd/lenny-test/cmd_run.go` line 428), leaves tier 0 red on every pull request, and verifies
  `pkg/proto/` nowhere, so decision 3 and TOOL-1's "verified green" claim were false in the enforcing
  environment. §4.6 and TOOL-1 now stage the plugin install in every job that runs the static tier, which
  are the `static` and `pr-fast` jobs in `.github/workflows/pr.yml`, the gate job in
  `.github/workflows/phase-gate.yml`, and the full-system load job in `.github/workflows/weekly.yml`, add
  the pair to the shared tool list in `.github/workflows/reusable/tool-cache.yml`, and bump the
  `~/go/bin` cache key to `lenny-go-bin-v3` so an exact hit on the existing key cannot skip the install.
  TOOL-1's Target, TEST-1's proto paragraph, and §11 carry the workflow files.
- **§5's row for a gate that never runs pointed at §6 cases that did not exist.** No per-gate case stated
  that a run inspecting zero members fails, so an exclusion list or walk root that selected nothing left
  every listed case passing and the gate green, which is the silent no-op §5 said was not accepted. §6 now
  states a zero-inspection case for the residual gate, the change-graph completeness check, the citation
  resolver, the line-citation ratchet, and the skip-reason classifier, matching the zero-output case the
  proto no-drift test already carried, and §5 splits the row: the gate that inspects nothing fails and
  names itself, while a gate absent from the registered list is covered by the gate-integrity meta-gate,
  which §7 already assigns to 0064.
- **No listed case pinned the passes' write exclusion, the one destructive path no gate observes.** The
  excluded files sit outside the read domain of the resolver, the ratchet, and the residual scan, so a
  pass that wrote one of them would corrupt a historical record with every gate green, and the population
  is 1,831 matching lines in `BUILD-GAPS.md` and 48 in `TEST-GAPS.md` at commit `c7d0f7f8`, plus
  `proposals/`, the two root planning documents, and the three build and queue records. Only the name pass
  carried an exclusion case, and it covered the generated-file branch the existing no-drift tests already
  catch. TEST-1 now states the rationale once and adds a write-exclusion case to each of the four passes,
  each asserting that the excluded file is byte-identical after an applied run and appears in neither the
  dry-run output nor the applied diff while an equivalent site in an ordinary carrier is rewritten, with
  the line pass additionally covering a citation in a file the per-file generated-artifact rule selects
  through its producer-output disjunct, which is `charts/lenny/crds/`. §5 records the boundary.
- **Correction: the new zero-inspection case failed the residual gate on a class the migration empties.**
  As first written, the case failed a run whose class predicate selected zero members, which is the
  terminal state of every class whose members can leave it, so the reserved-phrase class would have turned
  tier 0 red at the exit of the sub-step that empties it. That reading contradicted the in-class removal
  case two sentences earlier, §5's row for a member leaving its class, and §4.7's statement that the route
  out of a class is the pass or the remediation making every member stop matching. §6 now fails only a scan
  that selects zero files over a tracked tree that is not empty, and states that a predicate selecting zero
  members over a scan that did select files stays green unless the class's residual register still carries
  an `in-class` entry. §5's row carries the same split.
- **Correction: §5's write-exclusion row claimed per-group cases the added cases do not state.** The row
  asserted a byte-identical case for each excluded file group, while the four cases name `proposals/`, the
  two historical audit records, and the two root planning documents, with the three build and queue records
  in the name and identifier passes alone. The claim was also false for the two citation registers, which
  the identifier pass rewrites on a rename per §4.6. The row now states the groups the cases cover, records
  the generated-artifact coverage in the name and line passes, and states that the citation registers and
  `testdata/` carry no byte-identity assertion, with the reason for each.
- **Correction: §11 staged a `~/go/bin` cache-key bump in two workflows that restore no such cache.**
  `.github/workflows/phase-gate.yml` and `.github/workflows/weekly.yml` carry no `lenny-go-bin` key, and
  their only `cache:` entries are `actions/setup-go`'s module cache, so two of the five files the bullet
  listed had no edit to apply. §4.6 and TOOL-1 already named the three workflows that restore the key. §11
  now splits the bullet into the plugin install across `pr.yml`, `phase-gate.yml`, `weekly.yml`, and
  `reusable/tool-cache.yml`, and the key bump across `pr.yml`, `nightly.yml`, and
  `reusable/tool-cache.yml`, and TOOL-1's Target states why `nightly.yml` is in the list.

### Pass 3 (2026-07-30, automated)

- **The staged workflow change installed two of the proto producer's four binaries, so the new test would
  have recorded `UNVERIFIED` on every run of the three static-tier jobs that install nothing.** Only the
  tier-0 `static` job in `.github/workflows/pr.yml` installs any tool; the `pr-fast` job (`pr.yml` lines 29
  through 44), the gate job in `.github/workflows/phase-gate.yml` (lines 27 through 42), and weekly's
  `load-full-system` job (`.github/workflows/weekly.yml` lines 91 through 105) run only checkout, setup-go,
  a build, and a group whose plan opens with the static tier, so `buf` and `goimports` are absent from them
  as well as the two plugins. Under §4.6's own rule the test would have recorded `UNVERIFIED` there, which
  ends the run non-zero (`cmd/lenny-test/cmd_run.go` line 428) and leaves three of the four enforcing jobs
  permanently red, a weaker outcome than the fail-open skip it replaces
  (`cmd/lenny-test/cmd_run.go` lines 493 through 495 and 556 through 559). §4.6, TOOL-1, TEST-1's proto
  paragraph, and §11 now stage the producer's whole binary set: the `static` job's existing install step
  gains the two plugins, and the other three jobs gain the `~/go/bin` cache restore, a conditional install
  of all four binaries, and the `Add tool bin to PATH` step that the `static` job already carries, with
  `pr-fast`'s timeout raised from 5 minutes to the 10 the `static` job uses so a cold install fits.
- **The `TESTING.md` enum widening was closed by review alone, with no test that fails when the documented
  enum drifts from the emitted one.** TOOL-1 names the hazard, which is a CI consumer parsing
  `tests/results/latest.json` against a stale §7 enumeration, and `.claude/rules/test-coverage.md` line 42
  assigns documentation consistency to tier 11, where `tests/tier11_docs/backup_status_enum_test.go`
  already reconciles a documented status enum against the constants the code emits and where tier-11 tests
  already read `TESTING.md` directly. TEST-1 now adds `tests/tier11_docs/verdict_enum_test.go`, which
  derives both value sets from those constant blocks and asserts each §7 sentence
  enumerates every one of them, with a reject case over a fixture sentence missing a value. The §3.2 class
  row, TEST-1's Target, and §11 carry the file.
- **Correction: the new tier-11 test is red against today's `TESTING.md` for a reason predating this
  proposal.** `statusInconclusive` is emitted for a tier whose failure `classifyInfraFailure` reclassifies
  (`cmd/lenny-test/verdict.go` lines 236 and 237), while the §7 tier-status sentence enumerates `pass`,
  `fail`, `skipped`, and `not-selected` alone (`TESTING.md` line 522) and the lowercase value appears
  nowhere in the file. TOOL-1's amendment of that sentence now adds `inconclusive` alongside `unverified`,
  so the test is green at the sub-step's exit under decision 3. TEST-1 and §11 state the same.
- **Correction: §11's cache-key bullet still described the pre-fix staging of `phase-gate.yml` and
  `weekly.yml`.** It stated that those two files "restore no tool cache", which the preceding bullet
  contradicts, because the widened workflow change gives the phase-gate `gate` job and weekly's
  `load-full-system` job a `~/go/bin` restore step, and §4.6 states that the three restore steps TOOL-1
  adds use the `lenny-go-bin-v3` key. §11 now states that `pr.yml`, `nightly.yml`, and
  `reusable/tool-cache.yml` are the files carrying an existing `lenny-go-bin-v2` key, and that the restore
  steps added to `phase-gate.yml` and `weekly.yml` are authored at `lenny-go-bin-v3` from the start, so
  neither carries a key to bump.
- **Correction: §4.6's rewritten CI measurement cited `cmd/lenny-test/tiers.go` for a plan that file does
  not hold.** Weekly's `load-full-system` job runs the `phase-13.5-gate` group
  (`.github/workflows/weekly.yml` line 105), and `tiersForGroup` has no case for it, so the plan resolves
  from `tests/groups.yaml` lines 359 through 362 through `tiersForGroupFromYAML`
  (`cmd/lenny-test/tiers.go` line 240). The per-phase cases also run to line 233 rather than line 215,
  which sits inside the `phase-15-gate` plan. §4.6 now cites each source against the job it covers. The
  measured claim is unchanged, because the `phase-13.5-gate` selector is `tiers: [static]`.

### Pass 4 (2026-07-30, automated)

- **The three staged `~/go/bin` cache steps shared the `static` job's key while installing a narrower
  binary set, so an exact-key hit would have skipped the only install of `gofumpt` and `golangci-lint` and
  both tier-0 checks would have passed on their absence.** The `static` job's step is a read-write
  `actions/cache` (`.github/workflows/pr.yml` lines 57 through 64) whose install step is conditional on
  `cache-hit != 'true'` (line 66) and is the sole installer of `gofumpt` and `golangci-lint` (lines 68 and
  70), and `static` runs after `pr-fast` in the same workflow (line 50). A `pr-fast`, phase-gate, or
  weekly job saving a four-binary `~/go/bin` under the shared `lenny-go-bin-v3` key would therefore poison
  it for `static`, and both checks return a nil error when their binary does not resolve
  (`cmd/lenny-test/cmd_run.go` lines 537 through 540 and 577 through 579), so the tier would stay green
  while `gofumpt` stopped reporting unformatted files and `golangci-lint` stopped surfacing findings. A
  cache written on `main` by the phase-gate or weekly job is also readable from every pull-request branch.
  §4.6, TOOL-1, and §11 now state that the three added steps use `actions/cache/restore` at the pinned
  `actions/cache` commit and never write the key. Pass 5 records the second writer this bullet missed,
  which is the `mutation` job in `.github/workflows/nightly.yml`, and the conversion that makes the
  `static` job the sole writer of a directory carrying the full six-binary set. Restore-only was chosen
  over a second key namespace and over installing all six binaries in three jobs that do not need the lint pair, because it keeps one cache key
  for one concern and adds no parallel surface.

### Pass 5 (2026-07-30, automated)

- **The "the `static` job is the sole writer of the `~/go/bin` cache key" invariant was false, and the
  staged key bump carried the second writer forward.** The `mutation` job in
  `.github/workflows/nightly.yml` restores `~/go/bin` with a read-write `actions/cache` step
  (lines 184 through 189) whose primary key string is byte-identical to the `static` job's
  (`.github/workflows/pr.yml` lines 57 through 64), and the only binary it installs is `go-mutesting`
  (lines 190 through 195). Bumping both to `lenny-go-bin-v3` empties the namespace and lets that job save
  a one-binary `~/go/bin` under the shared key. Nightly runs on a schedule against the default branch
  (lines 9 through 11) while `pr.yml` never runs on `main` (`.github/workflows/pr.yml` lines 9 through
  12), so the `mutation` job would be the only writer of a `main`-scoped cache under that key, and a
  `main`-scoped cache is restorable from every pull-request branch. A `static` job taking an exact hit on
  it skips the sole install of `gofumpt`, `goimports`, `golangci-lint`, and `buf`, which is the fail-open
  Pass 4 introduced restore-only to close, and after this proposal also leaves the proto no-drift test
  without its four producer binaries, so the run records `UNVERIFIED` and exits non-zero
  (`cmd/lenny-test/cmd_run.go` line 428). TOOL-1 now converts that step to `actions/cache/restore` at the
  same pinned commit in the same change, which costs the `mutation` job nothing because its
  `go-mutesting` install is already conditional on a cache miss and carries `continue-on-error: true`.
  Conversion was chosen over a separate key namespace for the mutation job and over adding `go-mutesting`
  to the `static` job's install set, because it applies the rule §4.6 already states, which is that only
  the job installing the complete set writes the shared key, rather than adding a second key or a binary
  three other jobs do not need. §4.6, TOOL-1, §9 Pass 4, and §11 now state the writer set that holds after
  the change rather than asserting it already held, and §11 lists the nightly cache step as a staged
  change rather than a key-bump-only file.
- **The staged tier-11 verdict-enum test could not derive its value sets from the constants it cited.**
  The cited precedent works because `tests/tier11_docs/backup_status_enum_test.go` is
  `package tier11_docs_test` and imports the exported `pkg/ops/backup` constants
  (lines 21 through 41, against `pkg/ops/backup/service.go` line 16). `cmd/lenny-test/verdict.go` declares
  `package main` (line 3), which Go forbids importing, and every identifier in the two cited blocks is
  unexported (lines 23 through 33), so the specified derivation does not compile. Parsing the file as
  source text is the new machinery the paragraph said was unnecessary, and restating the values
  reintroduces the drift the test exists to catch. TOOL-1 now moves both blocks into a new exported
  `cmd/lenny-test/verdictstatus` package and updates the callsites in `cmd/lenny-test/verdict.go` and
  `cmd/lenny-test/cmd_run.go`, which are the only two files referencing them, and TEST-1 states that the
  tier-11 test imports that package. The move was chosen over source parsing because
  `cmd/lenny-ctl/runtimescaffold` is the in-tree pattern for an ordinary sub-package under `cmd/` that
  `pkg/` and tests import (`pkg/ctlcli/runtime.go` line 15 and
  `tests/tier3_contract/sdks/runtime_sdk_test.go` line 38), so it extends an existing surface instead of
  adding a parallel one. §3.2's class row, TEST-1, and §11 carry the new package.
- **Correction: the bullet above cited §3.4 for a row this document carries in §3.2.** The class row
  naming `cmd/lenny-test/verdictstatus` is the `TESTING.md` contract-prose row inside `### 3.2 The
  migration is script-driven`. This proposal has no §3.4, and the §3.3 cross-reference key maps only
  SPEC-1 through SPEC-4, §3.5, §4.8, and §28.1 through §28.5, so a bare §3.4 resolved to nothing in
  either document. The bullet now cites §3.2, matching the Pass 3 bullet that records the same row.
- **Correction: TOOL-1's Target listed `nightly.yml` for the cache key alone, after this pass gave
  TOOL-1 a second edit in that file.** §4.6, TOOL-1's body, and §11 all state that the `mutation` job's
  read-write `actions/cache` step becomes `actions/cache/restore` at the same pinned commit, so an
  implementor working from the Target's enumeration would have bumped the key and left the second writer
  in place, which is the fail-open this pass exists to close. The Target now names both edits.

### Pass 6 (2026-07-30, automated)

- **The `testdata/` exclusion was not carried into N3, so the naming lint and the identifier-resolution
  gate read fixture files no pass could write.** §4.6 states that `testdata/` sits outside the read domain
  of the resolver, the ratchet, and the residual scan, and outside the write domain of every pass, and §5
  repeats it, while §3.3 recorded the divergence from 0064 as covering two paragraphs and then fixed N3's
  list as carrying no `testdata/` clause. N3's list in 0064 is the one the name pass, the identifier pass,
  the naming lint, and the identifier-resolution gate all read, so the two statements left the same two
  passes with incompatible write domains. Under §3.3's reading the name pass could not write the tracked
  Go fixture TEST-1 stages for its Go-doc-comment case, while 0064's naming lint reads a Go doc comment in
  any tracked Go file, which is the writerless-site failure N3's exclusion paragraph exists to prevent;
  the same split applied to the identifier pass and its retired-spelling fixtures. §3.3 now records three
  diverging passages and states that the `testdata/` exclusion extends N3's list as well, so all four
  surfaces exclude `testdata/` and 0064's rule that every file those gates read has a pass that can write
  it still holds. Extending the exclusion was chosen over holding the fixtures in a carrier outside N3's
  domain, because §4.7 already requires that a class's residual scan range over the same domain as that
  class's pass and gates, and a `.go.txt` carrier would put the name pass's Go-doc-comment case on a
  surface the pass does not otherwise meet. §3.3 also records that no tracked `testdata/` file carries a
  bare reserved phrase or a retired channel spelling today, measured over `git ls-files`, so the extension
  removes nothing from the population SPEC-1 and SPEC-2 measure, and the sentence comparing the citation
  list with the naming list now names the three build and queue records as the difference.

## 10. Open decisions for review

None.

## 11. Files touched on application

- `scripts/specshift`, new: the migration engine and its four passes, with `run_test.go` carrying each
  pass's accept, reject, and boundary cases.
- `cmd/lenny-test`, for the citation resolver, the line-citation ratchet, the change-graph completeness
  check, the widened exceptions validator, and the `UNVERIFIED` verdict state, with cases in
  `cmd_validate_test.go` and `verdict_test.go`.
- `tests/tier0_static/`, for the skip-reason classifier, the proto no-drift test, and the residual gate,
  each with its own cases.
- `tests/registers/`, new: the shared register contract and the per-class registers and baselines this
  proposal seeds, which are the line-citation baselines, the citation-resolution baseline, the
  change-graph coverage register, the skip-reason register, and the residual registers for the classes
  this proposal seeds.
- A `testdata/` fixture directory beside each of `scripts/specshift`, `cmd/lenny-test`, and
  `tests/tier0_static/`, new, carrying the retired citation, anchor, and reserved-phrase text the gate
  cases present verbatim, which §4.6 places outside every gate's read domain and every pass's write
  domain, and carrying the fixture tree of excluded paths each pass's write-exclusion case runs against.
- `tests/change-graph.json` and `tests/change-graph-pending.txt`, for the completeness check's baseline.
- `TESTING.md`, for the §7 verdict-enum sentence, the §7 `tiers.<name>.status` enum sentence, and the
  §21.3 infrastructure-failure sentence, which the `UNVERIFIED` verdict state and the `unverified` tier
  status make false, with the tier-status sentence also gaining the `inconclusive` value the harness
  already emits.
- `cmd/lenny-test/verdictstatus`, new, holding the §7 verdict and tier-status constants as exported
  identifiers moved out of `cmd/lenny-test/verdict.go`, with the callsites in `cmd/lenny-test/verdict.go`
  and `cmd/lenny-test/cmd_run.go` updated, so that a tier-11 test can import them.
- `tests/tier11_docs/verdict_enum_test.go`, new, reconciling both amended §7 sentences against the verdict
  and tier-status constants it imports from `cmd/lenny-test/verdictstatus`.
- `.github/workflows/pr.yml`, `.github/workflows/phase-gate.yml`, `.github/workflows/weekly.yml`, and
  `.github/workflows/reusable/tool-cache.yml`, so that every job running the static tier carries the proto
  producer's four binaries. The `static` job in `pr.yml` gains `protoc-gen-go` and `protoc-gen-go-grpc` in
  its existing install step, the `pr-fast` job in `pr.yml`, the gate job in `phase-gate.yml`, and the
  full-system load job in `weekly.yml` each gain a restore-only `~/go/bin` cache step
  (`actions/cache/restore`), a conditional install of
  `buf`, `goimports`, `protoc-gen-go`, and `protoc-gen-go-grpc`, and an `Add tool bin to PATH` step, with
  `pr-fast`'s timeout raised from 5 minutes to 10, and the shared tool list in `reusable/tool-cache.yml`
  gains the two plugins in its `buf` group. The three added steps do not write the `lenny-go-bin-v3` key,
  per §4.6.
- `.github/workflows/nightly.yml`, whose `mutation` job restores `~/go/bin` with a read-write
  `actions/cache` step under a key string byte-identical to the `static` job's while installing
  `go-mutesting` alone (lines 184 through 195). That step becomes `actions/cache/restore` at the same
  pinned commit, which leaves the `static` job the sole writer of a directory that also carries `gofumpt`
  and `golangci-lint`, per §4.6. The job's `go-mutesting` install is already conditional on a cache miss,
  so it re-runs whenever the restore misses.
- `.github/workflows/pr.yml`, `.github/workflows/nightly.yml`, and
  `.github/workflows/reusable/tool-cache.yml`, for the `lenny-go-bin-v2` to `lenny-go-bin-v3` cache-key
  bump that stops an existing cache from skipping the install. Those three are the workflows that carry an
  existing `lenny-go-bin-v2` key. The restore steps this proposal adds to `phase-gate.yml` and
  `weekly.yml` are authored at `lenny-go-bin-v3` from the start, so neither file carries a key to bump.
