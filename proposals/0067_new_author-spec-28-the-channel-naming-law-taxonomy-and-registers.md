# Proposal: Author `spec/28` §28.1 through §28.4, the channel naming law, taxonomy, registers, and claim register, as a specification-only landing

- **Status:** **Applied to spec (2026-08-03).** Approved (2026-08-03) by jaf sign-off. Verified (2026-08-03), converged after 7
  adversarial review rounds (16 findings fixed) across one full-pool sweep, the certifying sweep running
  every lens complete with zero confirmed findings.
- **Prerequisites:** none. This proposal creates a specification file and its index rows, amends the
  proposal whose text claims that file, and adds tests over what it lands. It reads no register, runs no
  pass, and stages no file under `tests/registers/`, so nothing it stages depends on another proposal
  having landed first. It does not, on its own, unblock a migration pass: a pass enforces several
  prerequisites and this proposal discharges one of them, the presence of `spec/28`, while the driving
  registers those passes read are the subject of
  `proposals/0066_fix_supply-the-migration-passes-driving-registers-and-give-specs.md` and remain absent
  until it lands. That is a statement about when the passes become runnable rather than about when this
  proposal can be applied. **Lands before:**
  `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, whose SPEC-1 this
  proposal amends, because SPEC-1 states that it creates the file this proposal creates.
- **Date:** 2026-08-03.
- **Scope:** The part of proposal 0064's SPEC-1 that runs no pass, reads no register, and stages no
  register or map data file under `tests/`. It creates `spec/28_communication-channels.md` carrying §28.1 the naming law, §28.2 the
  taxonomy and axes, §28.3 the registers and the naming table, and §28.4 the claim register, appends the
  section's table-of-contents rows to `spec/README.md`, and amends proposal 0064 so that ownership of the
  file transfers rather than being claimed twice. Every rule and every register row is extracted from
  proposal 0064 §4.1 through §4.4 and §4.8, from its SPEC-1 and SPEC-2 change text, and from the channel
  inventory and naming table in the two root analysis documents. This proposal states no new normative
  rule of its own beyond the derived column values §4.3 below names.

This document stages the proposed specification changes and the amendment to proposal 0064. It does not
modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Proposal 0064 renames the communication channels between the gateway, the agent pod, the adapter, and the
runtime, and moves their contract into two new specification sections. Proposal 0065 built the migration
engine `scripts/specshift` and its gates, and is implemented. Proposal 0066 seeds the registers those
passes read and gives the engine a write confinement, and is awaiting sign-off.

Inside 0064, one sub-step is not a pass run. SPEC-1 authors `spec/28_communication-channels.md` §28.1
through §28.4 by hand, and everything else it stages is either a pass run over a register or a hand
correction to an existing file. That authored part needs no pass, no register, and no `tests/` register or map artifact,
and two of the passes hard-error on a tree that does not carry the file. This proposal lands that part on
its own, ahead of 0064, and hands ownership back through an amendment to 0064's own text so that no
sub-step claims to create a file that already exists.

An implementor should read proposal 0064 §4.1 through §4.4 and §4.8 before applying this one. This
document does not restate their reasoning; it stages the section text those subsections describe.

## 1. Problem

**`spec/28` is a structural prerequisite of two passes and does not exist.**
`scripts/specshift/name/declare.go` line 20 and `scripts/specshift/identifier/table.go` line 21 both set
`const channelSectionPrefix = "spec/28"`. `declare.go` lines 96 and 97 return
`index the declared identifiers: the tree carries no spec/28* specification file` when no listed path
carries that prefix, and `table.go` lines 195 and 196 return
`read the naming table: the tree carries no spec/28* specification file` in the same position. `ls spec/`
ends at `27_web-playground.md`, so both branches are the branches taken. Authoring the file requires no
pass, so it is the one prerequisite that is dischargeable by a hand edit.

**A `spec/28` carrying no naming table fails the identifier pass just as hard.**
`scripts/specshift/identifier/table.go` lines 199 and 200 return
`read the naming table: %d file(s) under spec/28* carry no table headed channel | carrier | retired
spelling | canonical spelling` when the file exists and states no such table. Landing §28.1 and §28.2
alone would replace one hard error with another, so §28.3 carries the four-column table the loader
recognizes (`tableColumns` at `scripts/specshift/identifier/table.go` line 143).

**Ownership of the file is claimed by an approved proposal.** 0064's SPEC-1 Target names
`spec/28_communication-channels.md` §28.1 through §28.4 as new
(`proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md` lines 1126 through
1128), and its change text opens with "Write §28.1 through §28.4" (line 1141). A landing that leaves those
sentences standing puts two documents in charge of creating one file, which is the parallel surface the
project principles forbid. The amendment in AMEND-1 is therefore part of this change rather than a
follow-up.

**What is not a problem, and what this proposal does not claim.** The tree carries 150 committed Go
`// spec:` annotations naming a 28.x section (`git grep -n "spec: §\?28\." -- '*.go' | wc -l` returns
150). Every one of them sits in a `_test.go` file or a `testdata/` file of the migration tooling and the
tier-0 gates, across ten files, and `git grep "spec: §\?28\." -- pkg/ cmd/ sdks/ migrations/` returns one
hit, `cmd/lenny-test/cmd_validate_yaml_test.go` line 40, which is itself a test. None of them carries a
line form, so none is a member of the population the citation resolver measures
(`scripts/specshift/line/account.go` line 14, `scripts/specshift/citation/grammar.go` line 204). The
static tier is green on the tree as it stands. Nothing in the tree is red today for want of `spec/28`,
and this proposal turns nothing from red to green.

**`spec/28` is necessary and not sufficient for either pass.** `scripts/specshift/main.go` line 165
returns `-register is required to run the <pass> pass` before any rewriter is built, and
`tests/registers/` carries neither `reserved-phrase-senses.yaml` nor `identifier-senses.yaml`. The name
pass reads `tests/registers/pinned-spec-literals.yaml` (`scripts/specshift/name/pinned.go` lines 133
through 145) at `scripts/specshift/name/name.go` line 236, three lines before it reaches the declared-
identifier index at line 239, and that file is absent as well, so a run today fails there rather than on
`spec/28`. Supplying those registers is proposal 0066's subject.

## 2. Decisions

1. **Ownership transfers rather than being duplicated.** AMEND-1 stages the sentences in proposal 0064
   that change SPEC-1's `spec/28` obligation from creation to confirmation, and adds
   `spec/28_communication-channels.md` to SPEC-2's Target so that SPEC-2 can write the naming-table rows
   for the spellings it fixes. The alternative, which is amending 0064's SPEC-1 to split itself into a
   pass-free part and a pass-driven part and dropping this proposal, is smaller and is recorded in §11 as
   an open decision for the reviewer. This document takes the amendment route because 0066 already stages
   sentence-level amendments to 0064 (its AMEND-1), so the mechanism is established and the reviewer can
   compare the two routes on one page.
2. **§28.4 is included.** The claim register needs no pass, no driving register, and no `tests/` register or map artifact,
   on the same terms as §28.1 through §28.3. Its rows live in `tests/claim-map.json`, which proposal 0064
   SPEC-3 creates and seeds, and its validator lands there too. Including the section here means N4's
   metric deferral names the claim-register row directly, as 0064 SPEC-1 phrases it, rather than being
   rephrased to avoid a section this proposal did not land and rephrased back when 0064 runs. The landed
   file is then the unit 0064 SPEC-1 already scoped.
3. **This proposal mints no identifier spelling.** §28.3's naming table carries only the rows proposal
   0064 already fixes: the `CH-ADAPTEREVENTS` proto RPC, Go symbol, and path rows stated at
   `proposals/0064...` lines 1378 through 1387, and the `CH-RUNTIMEOPS` manifest-key, flag, and socket
   rows stated at lines 1370 through 1376 and 1403 through 1404, and the `CH-RUNTIMEOPS` schema-path row
   stated at lines 1484 and 1485, which name both its retired spelling `lifecycle-events` and its canonical
   spelling `runtime-ops-events`. The flag row states 0064's `--runtime-ops-socket` without the double
   hyphen, because the double hyphen is the shell prefix rather than part of the flag token the declaration
   at `cmd/lenny-adapter/main.go` line 151 writes, and §4.3 states the rule the row follows. The
   `CH-RUNTIMEOPS` Go symbol stem and the `pkg/adapter/lifecyclechannel.go`
   file stem appear nowhere in 0064 (`grep -c RuntimeOps proposals/0064*.md` returns 0), and AMEND-1
   assigns them to SPEC-2, which is the sub-step that performs that rename and now names `spec/28` in its
   Target. Seven rows clear the `len(table.rows) == 0` branch at
   `scripts/specshift/identifier/table.go` lines 199 and 200, which is the structural obligation.
4. **The exclusivity column is left out of §28.3.** Its only source today is the root analysis document
   `gateway-runtime-comms.md`, which proposal 0064 SPEC-3 has not yet frozen as superseded. §28.2 names
   exclusivity as an axis and states that its per-channel values are recorded with the contract cards,
   which is where 0064 SPEC-3 writes the exclusivity and concurrency model. Sourcing a normative column
   from an unfrozen document is avoided rather than annotated.
5. **Specification-only: this proposal stages no register or map data file under `tests/`.** The exclusion
   covers `tests/registers/`, `tests/spec-map.json`, `tests/spec-map-exceptions.yaml`,
   `tests/change-graph.json`, and `tests/claim-map.json`. It does not cover the test files §8 stages, which
   are the tests every change carries under `.claude/rules/test-coverage.md` and which no gate the reasoning
   below names reaches: `tests/tier11_docs` is absent from `componentAndAboveTierDirs`
   (`cmd/lenny-test/cmd_validate.go` lines 125 through 138), so none of the three tier-11 files needs a
   spec-map key,
   and the change-graph completeness check excludes `_test.go` by construction (line 398), so no file §8
   stages needs a graph entry. `validateSpecMapCoverage` iterates
   the map's own sections (`cmd/lenny-test/cmd_validate.go` lines 801 through 821),
   `validateSpecMapPaths` stats the file each existing map entry names (lines 263 through 284), and the
   change-graph completeness check excludes `spec/` and non-`.go`/`.sh` files by construction (lines 389
   through 401). The walk in the other direction, from a heading to a required map key, is proposal 0064's
   heading walker, and no such walker exists under `cmd/`, `pkg/`, or `scripts/`. The probe committed at
   `9c5ff4bf` and reverted at `59672c55` confirmed this empirically: the pipeline created `spec/28` and its
   index row in one sub-step with zero discrepancies and the static tier stayed green. A stale doc comment
   at `cmd/lenny-test/cmd_validate.go` lines 26 through 28 claims a spec-tree walk that no code performs;
   the implementation is authoritative. 0064 SPEC-1 keeps its `tests/spec-map.json` and
   `tests/spec-map-exceptions.yaml` instructions for the §28 headings, which AMEND-1 leaves standing.
6. **§28.1 states N1 through N8 as proposal 0064 §4.1 revises them.** The root plan carries seven rules
   and an N1 requiring the identifier to state the endpoint pair and the plane
   (`gateway-runtime-comms-remediation.md` lines 210 through 212). 0064's review replaced that with a
   mnemonic rule, because the endpoint pair and the plane are register columns, and added N8, the citation
   and successor-pointer rule (`proposals/0064...` lines 365 through 434). Where the two sources disagree,
   0064 governs.
7. **No retired channel spelling appears in `spec/28` outside a naming-table row, and no bare reserved
   noun phrase appears at all.** The identifier pass exempts a retired spelling standing in the row that
   retires it and exempts nothing else (`scripts/specshift/identifier/table.go` lines 92 through 98 and
   244 through 253). Both sense registers are keyed by file and by the 1-based position of the site within
   that file, so an unregistered site introduced in `spec/28` would abort the pass that later walks it.
   This is why the registers carry a provenance column of entry numbers rather than a column of retired
   prose, and why N3 describes its two reserved words rather than reproducing them.
8. **No line-form citation is written anywhere in the new file, in any spelling.** N8 retires the form, and
   the line-citation ratchet reads tree content by matcher, so a line citation authored here would be a new
   member of the population the line pass exists to retire. Cross-references use the anchor form the tree
   already carries.
9. **The heading titles and anchors are taken verbatim from proposal 0064 §4.8** (`proposals/0064...`
   lines 1063 through 1067), so the index rows this proposal writes, the §28.5 through §28.8 headings 0064
   SPEC-3 appends, and the citable handles the later remediation steps write all agree. Rows are written
   only for the headings this proposal authors.
10. **The register column values that no source states verbatim are derived here and named as derived.**
    The channel inventory states each entry's direction, protocol, endpoint, and purpose
    (`gateway-runtime-comms.md` lines 228 through 251). The plane value and the authority-direction value
    are derived per row from that entry's purpose under the definitions §28.2 states, and §11 records the
    derivation as a review item so the reviewer sees the judgement rather than inheriting it. The inventory
    is a summary written before parts of the specification it summarizes, so every register row's writer
    set, reader set, and transport is checked against the current `spec/` statement of that store or
    connection and takes the specification's value where the two disagree. §4.3 names the three rows where
    they disagree today, and §8 item 5 pins the agreement with a tier-11 case.

## 3. Design overview

### 3.1 What lands

`spec/28_communication-channels.md` lands with four subsections. §28.1 states the naming law N1 through
N8. §28.2 states the three identifier classes and the six axes, with the transport and boundary sets
closed. §28.3 states the link register, the channel register, the register-entry register, and the naming
table the identifier pass reads. §28.4 states the claim register and its status vocabulary.

`spec/README.md` gains the section's index row and its four subsection rows.

`proposals/0064...` gains the sentence-level amendments AMEND-1 states, which transfer ownership of
the file and give SPEC-2 the `spec/28` target it needs to record the spellings it fixes.

### 3.2 What this change does not do

It runs no pass, seeds no register, removes no reserved phrase, and renames no identifier. It leaves
the naming lint, the identifier-resolution gate, the heading walker, and the claim-register validator
unbuilt, because each one's route to green is a content change 0064 makes.

Its effect on the migration is one prerequisite of several: after it lands, `declaredIdentifiers` and
`LoadTable` find their file, and both passes still exit at
`scripts/specshift/main.go` line 165 or at `scripts/specshift/name/name.go` line 236 until proposal 0066
supplies the registers.

### 3.3 How this document refers to proposals 0064, 0065, and 0066

A reference of the form "0064 SPEC-1" names a sub-step of
`proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`. A line number cited
against a proposal document is cited as measured on the tree at this proposal's date, and a proposal
document is not `spec/`, so N8's prohibition does not reach these citations. Section numbers of the form
§28.1 name the specification section this proposal creates. Section numbers of the form §4.1 inside a
sentence that names a proposal name that proposal's own subsection.

## 4. Detailed design

### 4.1 The naming law as it lands

§28.1 carries N1 through N8 in the revised form 0064 §4.1 states. N1 is a review-time rule with no gate,
because a mnemonic's fitness is a judgement. N2 fixes the spelling of an identifier. N3 states the
reserved-word prohibition, its domain, and its exclusions, and describes the two reserved words rather
than reproducing the banned spellings, because §28.1 is itself under `spec/` and the naming lint reads it
like any other file in the domain. N4 binds one identifier per channel across the Go stem, the proto RPC
stem, the metric label value, and the single-channel test name fragment, and states the metric deferral.
N5 keeps the link and channel identifier spaces free of a shared stem. N6 names a register for its store
and key. N7 fixes the form an identifier takes in a flag, an environment variable, and a manifest key. N8
retires the line-number citation form and requires a successor pointer from a section that gives up
content.

Two clauses of 0064's N3 name artifacts that land later, and §28.1 is written so that it makes no
present-tense claim about a file the tree does not carry. The literal banned spellings are stated as held
outside the prohibition's domain, in the naming lint's matcher and in the agent-facing naming rules, which
land with the lint. The sentence names neither `.claude/rules/channel-naming.md` nor the lint's file,
because 0064 SPEC-1 adds both, and the clause "which land with the lint" is what keeps the sentence true on
a tree that carries neither. N8 names the citation resolver and the
line-citation ratchet, both of which are in the tree, and does not enumerate the successor-pointer check,
which 0064 lands.

N4's deferral clause names the claim-register row directly, which decision 2 makes available: the metric
half of N4 is discharged by the remediation step that adds the adapter metrics endpoint and the catalog
entries, and the deferral carries a claim-register row with status `ABSENT` whose deferral identifier is
that step. The deferred population is the two metric names at `pkg/adapter/metrics.go` lines 71 and 79,
which are `lenny_adapter_control_events_total` and `lenny_adapter_control_events_dropped_total`. The row
itself is seeded by 0064 SPEC-3 with the rest of the claim register.

### 4.2 The taxonomy and the axes

§28.2 states the three classes the root plan's §3.2 fixes: a link is a transport connection between two
participants, a channel is a typed conversation carried on one transport connection, and a register is
shared state mediating two participants with no live connection. The channel definition names a transport
connection rather than a registered link because the link register declares a connection only where more
than one channel row refers to it, so eight of the fourteen channels run on a connection that no link entry
declares. Reading the definition as a reference to a registered link would make the register contradict its
own class table in those rows. Each class carries its own columns, which is what
makes the registers in §28.3 three tables rather than one.

Six axes are recorded per channel. Dial direction and authority direction are separate axes, because the
two invert on at least one channel: the gateway opens the stream that carries `CH-ADAPTEREVENTS` and the
adapter originates every message on it. Plane takes one of control, content, or state. Transport and
boundary are closed sets, so a new value requires a specification change. Boundary is also the grouping
key of the contract cards, so a channel's boundary value and its card subsection carry the same string.
Exclusivity records granularity plus the enforcing guard, or names the guard as missing, and its
per-channel values are stated with the contract cards for the reason decision 4 gives.

### 4.3 The registers and the naming table

§28.3 carries four tables. The link register, the channel register, and the register-entry register carry
one row per entry with a provenance column. The naming table carries one row per retired spelling per
carrier and is the table `scripts/specshift/identifier/table.go` reads.

The provenance column carries the entry number the channel inventory in `gateway-runtime-comms.md`
assigns, which is `C1` through `C22`. A number rather than a quotation is what keeps the retired prose out
of the section, per decision 7. Every row's participants, transport, endpoint, and purpose come from that
inventory's table (`gateway-runtime-comms.md` lines 228 through 251). Every row's plane value and
authority-direction value are derived from the entry's purpose under §28.2's definitions.

Three columns are stated against the current `spec/` text rather than against the inventory entry, because
the inventory predates the specification statement it summarizes. `REG-PODSTATE`'s writer set names the
gateway replicas alongside the WarmPoolController, because `spec/12` §12.6 carves `sessions_served` and
`scrub_failure_count` out of the controller-maintained mirror as gateway-written recycle counters and
`spec/04` §4.7 assigns the increments to the gateway on `ReportSessionScrub` and `ReportPodScrub`, while
the inventory's C21 entry states the controller as the only writer. `REG-CLAIM`'s writer set names the
WarmPoolController leader alongside the gateway replicas, because `spec/04` §4.6.3's ownership row assigns
the deletion at pod termination and orphan garbage collection to that controller and its RBAC paragraph
grants the `delete` verb for it, while the inventory's C17 entry states only the acquisition direction.
`LNK-INTERREPLICA`'s transport is gRPC, because `spec/07` §7.2 states that cross-replica message routing
runs over the internal `ForwardMessage` RPC and `spec/18` lists that RPC as a phase deliverable, while the
inventory's C19 entry states no protocol. Its dial direction and lifetime are derived from the same
sentence, which states that the replica holding the message forwards it to the session's coordinator. The
inventory's endpoint cell and the specification both state no address for it, so the endpoint column says
so.

The naming table's heading row is exactly `channel | carrier | retired spelling | canonical spelling`,
lowercased, which is the form `headingColumns` recognizes
(`scripts/specshift/identifier/table.go` lines 143 and 255 through 271). Each row states a channel
identifier that matches the canonical identifier pattern, a carrier drawn from the closed set at lines 30
through 46, and a retired spelling different from its canonical spelling, because `rowFrom` fails the run
on each of those conditions (lines 299 through 312).

A row's retired spelling is the token as its carrier writes it, because the site matcher selects a site by
a case-sensitive prefix test of that spelling against file content on token boundaries
(`scripts/specshift/identifier/site.go` lines 56 through 58 and 91 through 96). A spelling that occurs in no
file inside the write domain resolves no site, and the loader accepts the row, so the miss is silent rather
than fail-closed. The flag row therefore retires `lifecycle-socket` rather than `--lifecycle-socket`: the
double hyphen is the shell prefix a caller types, and the token the declaration writes is
`lifecycleSocket := flag.String("lifecycle-socket", ...)` at `cmd/lenny-adapter/main.go` line 151. The
spelling `--lifecycle-socket` occurs only in `BUILD-GAPS.md`, `gateway-runtime-comms.md`, and
`gateway-runtime-comms-remediation.md`, each of which `scope.readExcludedFiles` places outside the read
domain of every pass and every gate (`scripts/specshift/scope/scope.go` lines 88 through 94). The `socket`
row follows the same discipline and retires `@lenny-lifecycle`, which is the spelling the tree writes.
§8 item 1 asserts the property over every staged row.

`CH-RUNTIMEOPS` carries three path-carrier and Go-symbol spellings, and the seven rows this proposal writes
carry one of them. The schema path `schemas/lifecycle-events.schema.json` has its row here, because 0064
states both its retired and its canonical spelling (`proposals/0064...` lines 1484 and 1485) and decision 3
therefore mints nothing. The other two are the Go symbol stem and the `pkg/adapter/lifecyclechannel.go`
file stem, whose spellings 0064 states nowhere, so AMEND-1 assigns both rows to SPEC-2 and the table the
pass needs carries nine.

The Go symbol row matters twice over. The seven rows leave the retired Go symbol spelling
`LifecycleChannel` mapped to one channel rather than two, and both channels declare that token inside
package `adapter` (`pkg/adapter/controlchannel.go` line 90 and `pkg/adapter/lifecyclechannel.go` line 92),
so the second row is required before the identifier pass can rewrite the second site. Until the two SPEC-2
rows land, a Go symbol site the sense register resolves to `CH-RUNTIMEOPS` finds no row and the pass aborts
before any write, which is fail-closed. The `lifecyclechannel` path carrier behaves differently, and §5
records the difference.

### 4.4 The claim register

§28.4 states that every normative statement about this surface carries a row in `tests/claim-map.json`
with a status drawn from the vocabulary the root analysis document fixes: `WIRED` for a mechanism
reachable from production code, `UNWIRED` for one implemented with no production caller, and `ABSENT` for
one specified and not implemented. A `WIRED` row names the production surface. A row that is not `WIRED`
names the step that closes it, through a deferral identifier.

The section states the register's schema requirement and its location. The file, the seed rows, and the
tier-0 validator land with 0064 SPEC-3, so §28.4 describes a register whose artifact arrives later. §5
records that as an accepted state with the reason no gate reports it in the interval.

### 4.5 The heading titles and the index rows

The headings and their anchors are `28. Communication Channels` at `#28-communication-channels`,
`28.1 Naming law` at `#281-naming-law`, `28.2 Taxonomy and axes` at `#282-taxonomy-and-axes`,
`28.3 Registers` at `#283-registers`, and `28.4 Claim register` at `#284-claim-register`. The anchors
follow the slug rule 0064 §4.8 states, which lowercases the heading text, deletes every character that is
not a letter, a digit, a space, a hyphen, or an underscore, and replaces each remaining space with one
hyphen. `spec/README.md` ends at the `27.10 Roll-forward notes` row at line 190, so the new rows append.

### 4.6 The handover to proposal 0064

After this proposal lands, 0064 SPEC-1 keeps every obligation except authoring the four subsections: the
reserved-phrase removal across the domain N3 states, the naming lint, the `spec/03_high-level-architecture.md`
diagram correction and its pointer to §28, the three hand corrections, the `tests/spec-map.json` keys and
the `tests/spec-map-exceptions.yaml` entries for the §28 headings, the heading-walker seeding, and
`.claude/rules/channel-naming.md`. 0064 SPEC-2 gains `spec/28_communication-channels.md` in its Target and
the obligation to write the naming-table rows for the spellings it fixes, which are the `CH-RUNTIMEOPS` Go
symbol stem and the `pkg/adapter/lifecyclechannel.go` file stem it states there. 0064 SPEC-3 is unchanged:
it appends §28.5 through §28.8, creates `spec/29_communication-scenarios.md`, and seeds
`tests/claim-map.json`.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| §28.4 names `tests/claim-map.json`, which the tree does not carry | The section describes a register whose artifact and validator land with 0064 SPEC-3. No gate reads the path in the interval, because the claim-register validator is the gate that reads it and it lands with the file. A reader of §28.4 in the interval finds the vocabulary and the schema and no rows | §28.4's own sentence naming the register's location, and §4.4 |
| §28.3's naming table carries seven rows and the identifier pass needs nine | 0064 SPEC-2 adds the `CH-RUNTIMEOPS` go-symbol row and the `lifecyclechannel` path row when it performs that rename. The third `CH-RUNTIMEOPS` path carrier, the schema file `schemas/lifecycle-events.schema.json`, has its row here, so the pass moves that file without waiting on SPEC-2. In the interval the two SPEC-2 carriers behave differently. A Go symbol site the sense register resolves to `CH-RUNTIMEOPS` finds no row and the run aborts non-zero at `rowFor` before any write (`scripts/specshift/identifier/identifier.go` lines 377 through 380). The path carrier produces no site at all, because `pathSites` matches the file's own name against the table's retired spellings with a case-sensitive prefix test (`scripts/specshift/identifier/identifier.go` lines 313 through 316, `scripts/specshift/identifier/site.go` line 58) and no row retires the lowercase stem `lifecyclechannel`, so `pkg/adapter/lifecyclechannel.go` is left unmoved rather than failing closed. The identifier-resolution gate 0064 SPEC-2 lands is what reports that residue | §4.3, §4.6, and §11 item 3 |
| §28.3 carries retired spellings in its naming table while the tree still carries them elsewhere | The identifier pass reads no site inside a naming-table row, because the row is the declaration of the spelling rather than a reference to the channel (`scripts/specshift/identifier/table.go` lines 92 through 98 and 244 through 253). The identifier class's residual register and its gate land with 0064 SPEC-2 and read the same exemption | Decision 7 |
| §28.1 states N3 while the naming lint does not exist | The prohibition is stated and unenforced until 0064 SPEC-1 lands the lint and removes the sites. No file is red in the interval. N3's sentence placing the literal spellings in the lint's matcher and in the agent-facing naming rules carries the clause "which land with the lint", so it states where the spellings arrive rather than asserting that either artifact exists today, and no gate reads the claim in the interval | §4.1, and N3's own sentence in SPEC-1 |
| Eight of the fourteen channel rows carry `None` in the Link column | §28.2 defines a channel as a conversation carried on one transport connection, and §28.3's lead paragraph states that the link register declares a connection more than one channel row refers to and that `None` names a connection referred to by one row alone. A reader of such a row recovers the connection from the row's own transport column and from the endpoint the contract cards state. The register gains an entry for that connection at the point a second channel row refers to it | §28.2's class table, §28.3's lead paragraph, and §4.2 |
| §28.2 names exclusivity as an axis and states no per-channel value | A reader of §28.3 finds no exclusivity column and the axis's values arrive with the contract cards. The alternative, sourcing the column from a document 0064 SPEC-3 has not frozen as superseded, is what this accepts instead | Decision 4, and §28.2's sentence deferring the values to the cards |
| A reader follows a `spec/README.md` row for a §28 subsection this proposal does not author | No such row exists. Rows are written only for the headings this proposal lands, and 0064 SPEC-3 appends the rest with the headings | Decision 9, and SPEC-2 below |
| Proposal 0066's decision 15 cites SPEC-1's absent `spec/28` as the example that motivates the working-tree lister | The example goes stale once this proposal lands, and the lister stays correct, because it also covers the other files an apply agent authors before staging. This proposal stages no change to 0066 and is sequenced after it, so 0066's text describes the tree it was written against | §7, and the sequencing line at the head of this document |
| The three Go annotations naming §28.5.1 and §28.5.2 stay unresolvable | Those are contract-card headings 0064 SPEC-3 writes. Both sites are `testdata/` fixtures of the anchor pass, no gate resolves a citation with no line form, and the static tier is green | §1 |
| A later edit adds a fifth table to §28.3 whose heading row carries the four recognized column names | `LoadTable` reads both tables as the naming table and the pass substitutes from rows the section did not intend as naming rows. The loader recognizes the table by its column names rather than by position (`scripts/specshift/identifier/table.go` lines 139 through 143), so the register tables in §28.3 use column names outside that set | §4.3, and the column names the staged text writes |

## 6. Proposed changes

### SPEC-1. Create `spec/28_communication-channels.md` with §28.1 through §28.4

**Target:** `spec/28_communication-channels.md` (new; §28, §28.1, §28.2, §28.3, §28.4).

**Rationale:** Two migration passes read this file as their declaration source and hard-error without it
(`scripts/specshift/name/declare.go` lines 96 and 97, `scripts/specshift/identifier/table.go` lines 195
and 196), and the identifier pass hard-errors a second time on a `spec/28` carrying no naming table (lines
199 and 200). Authoring the section needs no pass, no register, and no `tests/` register or map artifact, so it is the
part of proposal 0064 SPEC-1 that lands on its own. The naming law is also the vocabulary every later
remediation step cites.

**Anchor:** the file does not exist. Create it with exactly the content below.

```markdown
# 28. Communication Channels

This section is the normative home for the communication channels between the gateway replicas, the agent
pod, the adapter container, the runtime container, and the control plane. §28.1 states the law that fixes
each channel's identifier. §28.2 states the classes an identifier is drawn from and the axes every channel
records. §28.3 carries the registers of links, channels, and register entries, together with the naming
table that records the spelling each carrier takes. §28.4 states the claim register, which records the
implementation status of every statement this section makes.

## 28.1 Naming law

**N1.** A channel's canonical identifier is a mnemonic for the conversation it carries, chosen so that no
two channels on the same boundary share a stem. The endpoint pair, the plane, the dial direction, the
authority direction, and the transport are register columns in §28.3, so an identifier is not required to
encode any of them and is never read as the authoritative statement of one.

**N2.** Identifiers are mnemonic, uppercase, and hyphenated, under one of the three class prefixes §28.2
states. Positional identifiers are not used, because a channel added between two others must not renumber
its neighbours.

**N3.** Two words are reserved and may not stand as a bare noun phrase naming a conversation on this
surface: the word the platform uses for a resource's phase transitions, and the word it uses for a command
plane. The prohibition covers the space-separated spelling and the hyphenated compound spelling, and a
matcher joins two consecutive comment lines before it applies either spelling, so a phrase wrapped across a
comment boundary is one site. The prohibition's domain is `spec/`, `docs/`, `schemas/`, a Go doc comment in
a tracked Go file, and a tracked root-level markdown document the exclusion list below leaves in scope, of
which `README.md` and `TESTING.md` are the two that carry the phrase today. Outside that
domain are the historical audit records `BUILD-GAPS.md` and `TEST-GAPS.md`, the two root planning documents
`gateway-runtime-comms.md` and `gateway-runtime-comms-remediation.md`, the build and queue records
`BUILD-PLAN.md`, `BUILD-PROGRESS.md`, and `PROPOSAL-QUEUE.md`, the `proposals/` directory, and every
`testdata/` directory, each of which records a finding, a plan, or a fixture as it was written rather than
the current contract. This section describes the two reserved words rather than reproducing the banned
spellings, because the section sits inside the prohibition's own domain. The literal spellings are held
outside that domain, in the naming lint's matcher and in the agent-facing naming rules, which land with the
lint. Either word may
appear inside a canonical identifier. A markdown anchor identifier is outside the prohibition in both
spellings, because a kramdown attribute value and the fragment of an intra-repo link are addressable link
targets rather than prose, and an anchor that has to change moves through the anchor-redirect map so that a
redirect exists for every inbound link. An identifier stem may not reuse a term the specification already
binds to an unrelated mechanism.

**N4.** Each channel uses one identifier everywhere: the Go package or file name stem, the proto RPC name
stem, the metric label value, and the test name fragment for a test scoped to one channel. A gate or a test
spanning channels is named for the invariant it enforces and carries no channel identifier. The metric half
of N4 is deferred. The remediation step that adds the adapter metrics endpoint and its catalog entries is
the step that discharges it, because the adapter process emits those metrics inside the agent pod and they
sit outside the default scrape target set until a deployer wires an adapter scrape target. The deferral
carries a claim-register row with status `ABSENT` naming that step, per §28.4.

**N5.** A link identifier and the channel identifiers it carries share no stem, so a search for one never
returns the other.

**N6.** A register is named for the store and the key rather than for a verb.

**N7.** A flag, environment variable, or manifest key naming a channel carries that channel's identifier in
the form its carrier already fixes: a flag uses lowercase kebab, an environment variable uses upper snake,
and a manifest key uses the camelCase convention the §4.7 adapter manifest field set establishes.

**N8.** A specification citation names a heading rather than a line. Citing a specification line number is
retired and may not be written, in any spelling. The prohibition is on the line number rather than on one
form of words, so a spelling a matcher does not yet recognize is a gap in the matcher rather than a
permitted citation. A section that gives up content carries a permanent successor pointer naming the
heading that now owns the content and the identifiers that moved. The citation resolver and the
line-citation ratchet are the gates that hold this rule.

## 28.2 Taxonomy and axes

An identifier is drawn from one of three classes, and the class fixes the columns the entry carries in
§28.3.

| Class | Prefix | What it is | Columns it carries |
|:--|:--|:--|:--|
| Link | `LNK-` | A transport connection between two participants | Participants, dial direction, transport, endpoint, and lifetime |
| Channel | `CH-` | A typed conversation carried on one transport connection | Link, boundary, plane, dial direction, authority direction, transport, and message vocabulary |
| Register | `REG-` | Shared state mediating two participants with no live connection | Store, key or table, writer set, reader set, and semantics |

Every channel records six axes.

| Axis | Values | Why it is recorded separately |
|:--|:--|:--|
| Dial direction | The participant that opens the connection | A stream one participant opens can carry messages the other originates |
| Authority direction | The participant that originates the messages | The boundary a channel is grouped under follows this axis rather than the dial direction |
| Plane | Control, content, or state | Separates a channel carrying agent input and output from one carrying operational commands and one carrying stored data |
| Transport | gRPC, Unix socket JSON Lines, JSON-RPC, HTTP, SQL, Redis, or Kubernetes API | Closed set. A new value requires a specification change rather than an undeclared extension |
| Boundary | `intra-pod`, `gateway-to-pod`, `pod-to-gateway`, `pod-egress`, `gateway-to-store`, `inter-replica`, or `control-plane` | Closed set, and the grouping key of the contract cards, so a channel's boundary value and its card subsection carry the same string |
| Exclusivity | The granularity plus the enforcing guard, or the guard named as missing | Records whether two gateway replicas can hold the channel at once. The per-channel values are stated with the contract cards |

## 28.3 Registers

Three registers carry the entries of the three classes, one row per entry. The provenance column carries
the entry number the channel inventory in `gateway-runtime-comms.md` assigns, so a reader can recover the
derivation of every column without the retired prose being reproduced here. The naming table below the
three registers records the spelling each carrier takes for a channel whose identifier changed, per N7.

The link register declares a transport connection that more than one channel row refers to, either as the
connection it is carried on or as the connection its calls are forwarded over, together with a connection
the specification states and no channel is carried on yet. A channel's Link column names that entry. It
reads `None` when the connection carrying the channel is referred to by that channel's row alone, in which
case the channel row's transport column and the endpoint stated with the contract cards describe the
connection, and the connection takes a link entry at the point a second channel row refers to it.

### Link register

| Identifier | Participants | Dial direction | Transport | Endpoint | Lifetime | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `LNK-POD-GRPC` | Gateway replica and pod adapter | Gateway | gRPC | Pod IP, TCP 50051 | One connection per gateway replica per pod | C1 |
| `LNK-GWCONTROL` | Pod adapter and gateway replica | Pod adapter | gRPC | Gateway service ClusterIP, TCP 50051 | One connection per pod process to one replica | C7 |
| `LNK-INTERREPLICA` | Gateway replica and gateway replica | Forwarding replica | gRPC | The internal gRPC `ForwardMessage` RPC, whose address the specification does not state | One connection per forwarding replica to a session's coordinating replica | C19 |

`LNK-INTERREPLICA` carries no channel row, because the specification states the connection and the
cross-replica message routing it carries is not implemented. That status is recorded as an `ABSENT`
claim-register row per §28.4 rather than as an absent transport in this register.

### Channel register

| Identifier | Link | Boundary | Plane | Dial direction | Authority direction | Transport | Message vocabulary | Provenance |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `CH-ATTACH` | `LNK-POD-GRPC` | `gateway-to-pod` | Content | Gateway | Both | gRPC | Message delivery and agent output | C2 |
| `CH-CHECKPOINT` | `LNK-POD-GRPC` | `gateway-to-pod` | State | Gateway | Both | gRPC | Workspace capture and restore | C3 |
| `CH-FENCE` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Coordinator handoff fence | C4 |
| `CH-BARRIER` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Quiesce and hold during gateway drain | C5 |
| `CH-PODHEALTH` | `LNK-POD-GRPC` | `gateway-to-pod` | Control | Gateway | Gateway | gRPC | Adapter liveness probing | C20 |
| `CH-ADAPTEREVENTS` | `LNK-POD-GRPC` | `pod-to-gateway` | Control | Gateway | Pod adapter | gRPC | Adapter-to-gateway operational events | C6 |
| `CH-MSGSOCK` | None | `intra-pod` | Content | Runtime | Both | Unix socket JSON Lines | Agent message plane | C8 |
| `CH-RUNTIMEOPS` | None | `intra-pod` | Control | Runtime | Both | Unix socket JSON Lines | Cooperative quiesce, interrupt acknowledgement, and credential rotation | C9 |
| `CH-MCP-PLATFORM` | None | `intra-pod` | Content | Runtime | Runtime | JSON-RPC | Platform tool calls, forwarded over `LNK-GWCONTROL` | C10 |
| `CH-MCP-CONNECTOR` | None | `intra-pod` | Content | Runtime | Runtime | JSON-RPC | Connector tool calls, forwarded over `LNK-GWCONTROL` | C11 |
| `CH-LLMPROXY` | None | `pod-egress` | Content | Runtime | Runtime | HTTP | Proxy-mode model calls | C12 |
| `CH-OBJSTORE` | None | `pod-egress` | State | Pod adapter | Pod adapter | HTTP | Checkpoint chunk upload and restore | C13 |
| `CH-EVENTRELAY` | None | `gateway-to-store` | State | Gateway | Gateway | Redis | Cross-replica session event backlog | C16 |
| `CH-ADMISSION` | None | `control-plane` | Control | Admission webhook | Admission webhook | HTTP | Drain-readiness admission on pod eviction | C22 |

### Register-entry register

| Identifier | Store | Key or table | Writer set | Reader set | Semantics | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `REG-COORDLEASE` | Redis | `t:<tenant>:lease:session:<session>` | Gateway replicas | Gateway replicas | One holder per tenant and session, on a compare-and-set with a 60 second expiry | C14 |
| `REG-COORDMIRROR` | Postgres | `coordination_lease` | Gateway sweeper | Gateway replicas | A single-valued row per tenant and session, and a projection rather than an exclusion primitive | C15 |
| `REG-SLOTCOUNT` | Redis | `lenny:pod:<pod>:active_slots` | Gateway replicas | Gateway replicas | An atomic per-pod counter that ceilings concurrent slots | C18 |
| `REG-PODSTATE` | Postgres | `agent_pod_state` | WarmPoolController for the mirrored `Sandbox` status columns, and gateway replicas for `sessions_served` and `scrub_failure_count` | Gateway replicas | One row per pod. A read-optimized mirror carrying the pod phase the orphan-session reconciler reads, together with the gateway-written reuse counters the recycle disposition evaluates ([§12.6](12_storage-architecture.md#126-interface-design), [§4.7](04_system-components.md#47-runtime-adapter)) | C21 |
| `REG-CLAIM` | Kubernetes API | `SandboxClaim` named `claim-<podName>` | Gateway replicas for the create, the status-subresource binding-state writes, and the hold-expiry delete, and the WarmPoolController leader for the deletes at pod termination and orphan garbage collection | Gateway replicas and controllers | Cluster-wide per-pod acquisition on first claim. The object carries no owner reference, so the controller's delete is the reclamation path for a claim its holder did not remove ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) | C17 |

### Naming table

The table records, per carrier, the spelling a channel whose identifier changed takes on that carrier. A
retired spelling standing in the row that retires it is the declaration of that spelling rather than a
reference to the channel.

| channel | carrier | retired spelling | canonical spelling |
|:--|:--|:--|:--|
| `CH-ADAPTEREVENTS` | proto-rpc | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | go-symbol | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | path | `controlchannel` | `adapterevents` |
| `CH-RUNTIMEOPS` | manifest-key | `lifecycleChannel` | `runtimeOps` |
| `CH-RUNTIMEOPS` | flag | `lifecycle-socket` | `runtime-ops-socket` |
| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| `CH-RUNTIMEOPS` | path | `lifecycle-events` | `runtime-ops-events` |

## 28.4 Claim register

Every normative statement this section makes about a mechanism carries a row in the claim register at
`tests/claim-map.json`, with a status drawn from a closed set. `WIRED` means the mechanism is reachable
from production code. `UNWIRED` means it is implemented and has no production caller. `ABSENT` means it is
specified and not implemented.

A `WIRED` row names the production surface that reaches the mechanism. A row whose status is not `WIRED`
names, through a deferral identifier, the step that closes it, which makes the register the work queue for
the steps that follow.

The claim register carries its own schema rather than the entry schema the migration registers share,
because a `WIRED` row is a permanent statement about the tree and carries no expiry, while a migration
register's entry expires. The register file, its seed rows, and the validator that reads it land with the
contract cards.
```

**Not in scope for this sub-step.** §28.5 through §28.8, `spec/29_communication-scenarios.md`, the
`spec/03_high-level-architecture.md` diagram correction and its pointer to §28, the reserved-phrase
removal, the identifier rename, `.claude/rules/channel-naming.md`, `tests/spec-map.json` keys and
`tests/spec-map-exceptions.yaml` entries, and every gate. Each stays with proposal 0064.

### SPEC-2. Append the §28 table-of-contents rows to `spec/README.md`

**Target:** `spec/README.md`.

**Rationale:** Every numbered specification file carries an index row and its subsection rows. Line 180 is
the §27 row and the ten §27.x rows follow it. A section reachable from no index is a gap in the reading
path. The rows land in the same exclusive change as SPEC-1, because a row pointing at a file that does not
exist is a broken link.

**Anchor:** append after the last §27 row, which is the `27.10 Roll-forward notes` row at line 190 and the
last line of the file.

```markdown
- [28. Communication Channels](28_communication-channels.md)
  - [28.1 Naming law](28_communication-channels.md#281-naming-law)
  - [28.2 Taxonomy and axes](28_communication-channels.md#282-taxonomy-and-axes)
  - [28.3 Registers](28_communication-channels.md#283-registers)
  - [28.4 Claim register](28_communication-channels.md#284-claim-register)
```

The link text and the anchors are the ones proposal 0064 §4.8 fixes, so the rows 0064 SPEC-3 later appends
for §28.5 through §28.8 sit under a parent row that already resolves. No row is written for a heading this
proposal does not author.

### AMEND-1. Transfer ownership of `spec/28` in proposal 0064

**Target:** `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`: SPEC-1's
Target list and Change text, SPEC-2's Target list and Change text, the §3.5 build-order bullet at line 316,
the §4.8 lead-in at line 1036, the §11 files-touched bullet at line 6089, and the two SPEC-3 justification
clauses at lines 1953 and 2704. The last five sit outside
SPEC-1's and SPEC-2's text and each states that SPEC-1 creates the file or its §28.1 through §28.4
sections, so a Target confined to the two sub-steps would leave the double ownership standing in five
places. The two SPEC-3 clauses are justifications inside that sub-step's prose rather than instructions to
it: each explains why SPEC-3 does or does not do something by naming who authored §28.1 through §28.4, and
neither states an action SPEC-3 takes, so correcting the attribution changes what SPEC-3 is told to do in
no respect.

**Rationale:** 0064 SPEC-1 states that it creates the file this proposal creates and that it writes the
four subsections this proposal writes. Left standing, an approved sub-step instructs an apply agent to
create a file that already exists and to author headings that are already there. 0064 SPEC-2 states the
spellings the naming table records and has no `spec/28` target to write them into, which is the gap that
would otherwise force a different proposal to mint two normative identifiers.

**Change (staged description).** Twelve sentence-level edits, each leaving the rest of the sub-step intact.
Items 1 through 3, 6, and 7 reach SPEC-1's Target list and Change text, items 4 and 5 reach SPEC-2's, and
items 8 through 12 reach the five sentences outside the two sub-steps that state SPEC-1 creates the file or
its four subsections.

1. In SPEC-1's Target list (lines 1126 through 1128), replace
   `` `spec/28_communication-channels.md` §28.1 through §28.4 (new)`` with
   `` `spec/28_communication-channels.md` §28.1 through §28.4 (existing; created by proposal 0067, which
   this sub-step does not re-author)``.
2. In SPEC-1's Change text, replace the opening sentence
   `Write §28.1 through §28.4: the naming law N1 through N8, which includes the citation and
   successor-pointer rule N8 the citation gates enforce, the taxonomy and its axes, and the three registers
   with the full inventory.` with a sentence stating that proposal 0067 landed §28.1 through §28.4 with the
   naming law N1 through N8, the taxonomy and its axes, the three registers, the naming table, and the
   claim register, that this sub-step authors no heading there, and that its `spec/28` obligation is to
   confirm the four subsections are present before the name pass runs, because the pass indexes the
   declared identifier space out of that file.
3. In SPEC-1's Change text, replace the sentence beginning "Add the `spec/README.md` table-of-contents
   rows for `spec/28` and its §28.1 through §28.4 subsections" with a sentence stating that proposal 0067 wrote those five rows with the link text and anchors §4.8
   fixes, and that this sub-step confirms they are present and adds any that are missing. The remainder of
   that paragraph stages the `tests/spec-map.json` keys and the `tests/spec-map-exceptions.yaml` entries for
   the same headings and the heading-walker seeding, which are unchanged, and two further sentences that
   items 6 and 7 replace.
4. In SPEC-2's Target list (lines 1347 through 1360), add `spec/28_communication-channels.md` to the
   enumeration, for the naming-table rows the sub-step writes.
5. In SPEC-2's Change text, after the sentence
   `The §28.3 table records the spelling each carrier takes, per N7.` (line 1371), add a sentence stating
   that this sub-step writes the §28.3 naming-table rows for every spelling it fixes that the table does
   not already carry, which are the `CH-RUNTIMEOPS` Go symbol stem and the `pkg/adapter/lifecyclechannel.go`
   file stem, that the `schemas/lifecycle-events.schema.json` schema-path row is already carried, and that
   it states both
   spellings here for the reason the paragraph already gives for the `CH-ADAPTEREVENTS` carriers, which is
   that each is derived from a naming rule and the derivation is written down once.
6. In SPEC-1's Change text (lines 1304 through 1306), replace the sentence beginning "The rows land in the
   same change that creates" and ending "no row in this proposal precedes its
   target file." with a sentence stating that proposal 0067 created the file with those five headings and
   wrote those rows before this sub-step runs, so every row resolves and no row in this proposal precedes
   its target file.
7. In SPEC-1's Change text (lines 1314 through 1317), replace the clause stating that `spec/README.md` is
   hand-maintained, has no generator, and has "its last numbered entry today is §27.10 at line 190" with a
   clause stating that the file is hand-maintained, has no generator, and already carries the §28 rows
   proposal 0067 appended, so a heading appended without a row is invisible to a reader scanning the index.
   The stale part is the position claim, which proposal 0067's SPEC-2 supersedes by appending after that
   row.
8. In §3.5's build-order bullet 2 (lines 316 through 319), replace the sentence beginning
   "`spec/28_communication-channels.md` is created here carrying" and ending "and the `spec/03`
   correction." with a sentence stating that proposal 0067 created `spec/28_communication-channels.md` carrying §28.1 through
   §28.4, which are the law and the three registers, and that this sub-step carries the reserved-word
   removal and the `spec/03` correction.
9. In §4.8's lead-in (line 1036), replace the clause "SPEC-1 creates
   `spec/28_communication-channels.md` carrying §28.1 through §28.4," with a clause stating
   that proposal 0067 created `spec/28_communication-channels.md` carrying §28.1 through §28.4. The rest of
   that sentence, which states that SPEC-3 appends §28.5 through §28.8 and creates
   `spec/29_communication-scenarios.md`, is unchanged, and the heading table itself is unchanged.
10. In §11's files-touched list (line 6089), replace the bullet reading
    "`spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, both new." with a
    bullet stating that `spec/29_communication-scenarios.md` is new and that
    `spec/28_communication-channels.md` exists, created by proposal 0067, and is appended to by SPEC-3.
11. In SPEC-3's Change text (line 1953), replace the clause
    `while the §28.3 register its bijection reads already exists from SPEC-1` with a clause stating that the
    §28.3 register its bijection reads already exists, created by proposal 0067. The rest of the sentence,
    which states that the §28.8 matrix completeness check lands in SPEC-3 because §28.8 is written there and
    that the bijection first holds at that sub-step's exit, is unchanged.
12. In SPEC-3's Change text (line 2704), replace the clause
    `because SPEC-1 created them together with their sections and wrote their key or their exceptions entry
    there` with a clause stating that proposal 0067 created the §28.1 through §28.4 headings together with
    their sections and that SPEC-1 wrote their `tests/spec-map.json` key or their
    `tests/spec-map-exceptions.yaml` entry. The rest of the sentence, which states that SPEC-3 neither
    writes nor retires coverage for those headings, is unchanged, and the split keeps decision 5's statement
    that SPEC-1 retains the spec-map obligation for the §28 headings.

No other sub-step instruction in proposal 0064 changes. Items 11 and 12 reach justification clauses inside
SPEC-3's prose and leave every action SPEC-3 is instructed to take as written. Its status line and its
remaining sub-steps are untouched. Its §1 scope paragraph describes the two-proposal change at the level of the whole document, and
its convergence record states decisions as they were made, so both are left as written.

## 7. Non-goals

- **Running any specshift pass.** The name, identifier, anchor, and line passes are left as they stand.
- **Seeding any register under `tests/registers/`**, including `reserved-phrase-senses.yaml`,
  `identifier-senses.yaml`, `pinned-spec-literals.yaml`, and the anchor sense register. Those are proposal
  0066's subject. This proposal reads no register and is not sequenced against it.
- **The reserved-phrase removal and the identifier rename across the tree**, which are 0064 SPEC-1 and
  SPEC-2 work.
- **The naming lint, the identifier-resolution gate, the heading walker, the claim-register validator, the
  successor-pointer check, and the gate-integrity meta-gate.** Each one's route to green is a content
  change this proposal does not make.
- **§28.5 the contract cards, §28.6 the exclusivity and concurrency model, §28.7 the wire-contract
  artifact register, and §28.8 the failure and degradation matrix**, together with `spec/29` and the
  scenario traces.
- **The exclusivity column in §28.3.** Its only source is a document 0064 SPEC-3 has not yet frozen as
  superseded, so the axis is named in §28.2 and its values arrive with the contract cards, per decision 4.
- **The §4.7 and §15.4 reductions, the successor pointers, and the anchor and line-citation retirement.**
- **The `spec/03_high-level-architecture.md` diagram correction and its pointer to §28**, which 0064
  SPEC-1 carries. §28 therefore lands with no inbound pointer from `spec/03` until 0064 runs.
- **Freezing `gateway-runtime-comms.md` with the superseded header**, which is 0064 SPEC-3's work.
- **Any edit to proposals 0065 and 0066.** The amendment in AMEND-1 reaches proposal 0064 alone, and it is
  a goal rather than a non-goal, because ownership of one file cannot sit in two documents.
- **Any register or map data file under `tests/`**, which is `tests/registers/`, a `tests/spec-map.json`
  key, a `tests/spec-map-exceptions.yaml` exception, a `tests/change-graph.json` entry, and
  `tests/claim-map.json`, per decision 5. The three tier-11 test files §8 stages are in scope, and §12
  lists them.
- **Regenerating `pkg/proto/`, editing `schemas/`, or touching any Go file under `pkg/`, `cmd/`, `sdks/`,
  or `migrations/`, any chart file, or any documentation file.** The Go test files §8 stages under
  `scripts/specshift` and `tests/tier11_docs` are in scope.

## 8. Testing

The change reaches tier 0, tier 1, and tier 11 under `.claude/rules/test-coverage.md`: it adds a
specification file two `scripts/specshift` loaders read, and specification and index content the
documentation tier reconciles. It reaches no higher tier, because it changes no runtime behavior. Each
test below carries the `// spec:` annotation naming the section it exercises, and the tier-11 tests carry
a `// diagnosis:` comment, in the form the surrounding files use.

1. **`LoadTable` over the tracked tree returns the naming table, and a `spec/28` carrying every other §28
   table returns the no-table error** (tier 1), in `scripts/specshift/identifier/table_test.go` as
   `TestLoadTableReadsTheLandedNamingTable_spec_28_3`. The accept case asserts a non-empty row set, that
   every row's carrier is a member of `Carriers()`, and that each row's retired and canonical spellings
   differ. It also asserts that every row's retired spelling occurs at least once in a tracked file inside
   the write domain, which is the property that makes the row reachable by `findSites`, and that would have
   caught a flag row retiring `--lifecycle-socket` when the only in-domain occurrence is the token
   `lifecycle-socket`. The reject case runs over a fixture tree whose `spec/28` carries the link, channel, and
   register-entry tables and no four-column heading, and asserts the error names the four column names, so
   the case fails if a register table's column set ever drifts into the recognized set. `// spec: §28.3`.
2. **`declaredIdentifiers` over the tracked tree indexes one identifier of each class, and a prose-only
   `spec/28` returns the declares-none error** (tier 1), in `scripts/specshift/name/declare_test.go` as
   `TestDeclaredIdentifiersIndexesTheLandedSection_spec_28_3`. The accept case asserts that the index
   carries a `LNK-`, a `CH-`, and a `REG-` identifier from the landed tables. The reject case runs over a
   fixture whose `spec/28` carries §28.1 alone, which is the empty-declaration boundary at
   `scripts/specshift/name/declare.go` lines 99 through 101. `// spec: §28.1, §28.3`.
3. **Every §28 index row resolves to a heading in the section, and every §28 heading carries an index
   row** (tier 11), in `tests/tier11_docs/spec_28_index_rows_test.go` as
   `TestSection28IndexRowsResolve_spec_28`. The test derives each anchor from the heading text under the
   slug rule the tree's existing anchors follow, asserts the two directions, and carries a reject case over
   a fixture row whose anchor collapses a run of punctuation to one hyphen, which is the derivation error
   that would otherwise report correct rows as broken. `// diagnosis:` states that a failure means the
   index and the section disagree on a heading title or an anchor. `// spec: §28`.
4. **The section that states the reserved-word prohibition does not violate it, and states the domain the
   shared predicate implements** (tier 11), in
   `tests/tier11_docs/spec_28_reserved_phrase_test.go` as
   `TestSection28StatesN3WithoutViolatingIt_spec_28_1`. The test applies both banned spellings under the
   comment-continuation join over `spec/28_communication-channels.md` and asserts zero occurrences outside
   a naming-table row, and carries a reject case over a fixture line holding the space-separated spelling
   and a second over the hyphenated compound. The same file asserts that the domain N3 states matches the
   domain `scope.ReservedPhraseCarrier` admits, by asserting that the predicate answers true for a path
   under each tree N3 names, for a tracked Go file, and for a tracked root-level markdown document other
   than `README.md` and `TESTING.md`, and false for a path outside them, so the section and the shared
   predicate cannot drift (`scripts/specshift/scope/scope.go` lines 480 through 489). This is the case that fails if a later edit quotes a specimen
   into the section, which is the state that would leave 0064's naming lint red on the section that states
   its rule. `// diagnosis:` states that a failure means `spec/28` carries a site the naming lint will
   report and no pass is scheduled to write. `// spec: §28.1`.
5. **§28.3's register rows and its `LNK-INTERREPLICA` row agree with the specification of the store or the
   connection they name** (tier 11), in `tests/tier11_docs/spec_28_register_writers_test.go` as
   `TestSection28RegisterWritersMatchTheSpec_spec_28_3`. The test reads `spec/28`'s register-entry register
   and asserts, per row, that its writer set is consistent with the section that specifies the store: that
   `REG-PODSTATE`'s writer cell names the gateway alongside the WarmPoolController and that
   `spec/12_storage-architecture.md` states `sessions_served` and `scrub_failure_count` as gateway-written,
   and that `REG-CLAIM`'s writer cell names the WarmPoolController alongside the gateway replicas and that
   `spec/04_system-components.md`'s ownership row states the controller's delete at pod termination and
   orphan garbage collection. The test then reads `spec/28`'s link register and asserts that
   `LNK-INTERREPLICA`'s transport cell states gRPC, that its dial direction cell names the forwarding
   replica, and that its lifetime cell names the session's coordinating replica, and that
   `spec/07_session-lifecycle.md` §7.2 states the sentence those three cells are derived from, which is the
   sentence stating that the coordinator forwarding mechanism reuses the same internal gRPC
   `ForwardMessage` RPC used for all cross-replica message routing and the sentence stating that the
   forwarding replica forwards the message to the session's coordinator. This is the third of the three
   cells §4.3 states against the current `spec/` text rather than against the channel inventory, so all
   three carry an assertion. Each assertion pins the specification's sentence as a string literal, in the
   form the surrounding `tests/tier11_docs` reconciliation tests use, so an edit to either side fails the
   case. `// diagnosis:` states that a failure means `spec/28`'s register and the store's or the
   connection's own section disagree on who writes it or on how it is carried.
   `// spec: §28.3, §12.6, §4.6.3, §7.2`.
6. **No new line-citation test is added.** The line-citation ratchet already reads every tracked file and
   fails a file whose count rises, and `spec/28` enters at zero, so a line citation written into the
   section later fails tier 0 without a new predicate.

Coverage: the change adds no Go lines, so the new-code coverage floor applies to the test files alone.
Run `lenny-test --changed --max-tier 11` before declaring the change done, and
`lenny-test coverage --diff <base-ref>` over the tests above.

## 9. Findings closed on application

None. This proposal authors a specification section and closes no `BUILD-GAPS.md` finding on its own.

## 10. Resolved in adversarial review

Review rounds populate this section. The draft records in §11 the two decisions that were taken against a
stated alternative before the first round.

### Pass 1 (2026-08-03, automated)

- **AMEND-1 left four further sentences of proposal 0064 assigning creation of `spec/28` to SPEC-1.** Its
  Target reached SPEC-1's and SPEC-2's text alone, so 0064 lines 316, 1036, 1304 through 1306, and 6089
  would have kept stating that SPEC-1 creates the file, one of them inside the paragraph item 3 declared
  unchanged. AMEND-1's Target now names those three further sites, items 6 through 10 stage them, item 3
  describes the remainder of its paragraph correctly, and item 7 replaces the stale claim that
  `spec/README.md`'s last numbered entry is §27.10 at line 190, which SPEC-2 of this proposal supersedes.
- **The staged N3 asserted in the present tense that a naming lint's matcher and an agent-facing rules
  file carry the banned spellings.** Neither artifact is in the tree (`.claude/rules/` carries no
  `channel-naming.md`, and `tests/tier0_static/residual_gate_test.go` lines 179 through 191 record the
  naming classes as absent), and 0064 SPEC-1 lands both. The sentence now states that the spellings are
  held outside the prohibition's domain in those two places, "which land with the lint", and §4.1 and §5
  row 4 state the same predicate.
- **The staged N3's exclusion list dropped the two root planning documents.** 0064 §4.1 lines 384 through
  386 exclude them, and lines 392 and 393 state that the list is the one the identifier pass and the
  identifier-resolution gate read, so the omission would have put `gateway-runtime-comms.md` and
  `gateway-runtime-comms-remediation.md` inside a gate's read domain with no pass able to write them. Both
  are now named in the exclusion sentence.
- **§28.3 wrote `None` in the Link column of eight channel rows while §28.2 defined a channel as carried
  on one link.** §28.2's channel definition now names a transport connection, §28.3's lead paragraph states
  when the link register declares a connection and what `None` names, §4.2 and a new §5 row carry the same
  rule, and no link identifier is minted, which keeps the proposal inside decision 3.
- **§7 and §12 excluded every file under `tests/` while §8 staged two tier-11 test files.** Decision 5, the
  §7 non-goal, and §12 now scope the exclusion to the register and map data files under `tests/`, and §12
  lists the five test files §8 stages together with the reason `tests/tier11_docs` needs no spec-map key and
  no change-graph entry.
- **§5's fail-closed row was wrong at the path carrier and stated a row count the document contradicts.**
  `pathSites` matches the file name against the table's retired spellings with a case-sensitive prefix test,
  and no row retires the lowercase stem `lifecyclechannel`, so `pkg/adapter/lifecyclechannel.go` yields no
  site rather than aborting. The row now states both carriers separately, names the identifier-resolution
  gate as what reports the residue, and states eight rows, which is what §4.6 and §11 item 3 already imply
  by assigning SPEC-2 two rows. §4.3 carries the same statement.
- **§28.3's `REG-PODSTATE` row named the warm pool controller as the sole writer.** `spec/12` §12.6 carves
  `sessions_served` and `scrub_failure_count` out of the controller-maintained mirror as gateway-written
  recycle counters and `spec/04` §4.7 assigns the increments to the gateway, so the row now states both
  writer sets and names the reuse counters the recycle disposition evaluates.
- **§28.3's `REG-CLAIM` row listed the controllers as readers only.** `spec/04` §4.6.3's ownership row and
  its RBAC paragraph assign the deletes at pod termination and orphan garbage collection to the
  WarmPoolController, so the row now states that writer alongside the gateway replicas and records that the
  object carries no owner reference.
- **§28.3's `LNK-INTERREPLICA` row stated no transport for a link the specification specifies.** `spec/07`
  §7.2 states the internal gRPC `ForwardMessage` RPC for cross-replica message routing and `spec/18` lists
  it as a phase deliverable, so the row now carries gRPC, the dial direction, and the lifetime that sentence
  states, and the not-implemented fact moves to an `ABSENT` claim-register row per §28.4.
- **Decision 10 now requires every register row's writer set, reader set, and transport to be checked
  against the current `spec/` statement of that store or connection**, because the channel inventory the
  rows are lifted from predates parts of the specification it summarizes. §4.3 names the three rows where
  the two disagree, and §8 item 5 adds a tier-11 case pinning the agreement.
- **Decision 5 and the §7 non-goal still counted two tier-11 test files after §8 item 5 added a third.**
  §8 items 3, 4, and 5 stage `tests/tier11_docs/spec_28_index_rows_test.go`,
  `tests/tier11_docs/spec_28_reserved_phrase_test.go`, and
  `tests/tier11_docs/spec_28_register_writers_test.go`, and §12 already listed the three, so the two
  carve-out statements under-counted the files they carve out. Decision 5 now states that none of the three
  tier-11 files needs a spec-map key, and the §7 non-goal states that the three tier-11 test files are in
  scope. The gate fact is unchanged and holds: `cmd/lenny-test/cmd_validate.go` lines 125 through 138 list
  no `tests/tier11_docs` entry in `componentAndAboveTierDirs`.
- **AMEND-1's item-range sentence mis-partitioned the ten edits.** Items 6 and 7 sit in SPEC-1's Change
  text, so SPEC-1 and SPEC-2 take seven of the ten edits and only three sit outside them, which is what
  AMEND-1's Target paragraph and §12's per-file breakdown already state. The sentence now assigns items 1
  through 3, 6, and 7 to SPEC-1, items 4 and 5 to SPEC-2, and items 8 through 10 to the three sentences
  outside the two sub-steps, and it no longer describes item 7, which replaces the stale `spec/README.md`
  position claim, as a sentence stating that SPEC-1 creates the file.

### Pass 2 (2026-08-03, automated)

- **Decision 10 claimed that §8 item 5 pins all three spec-derived register cells while the case covered
  only the two writer-set rows.** `LNK-INTERREPLICA` is a row of §28.3's link register rather than of the
  register-entry register, and its spec-derived cells are transport, dial direction, and lifetime, so the
  item as written reached none of them and the third cell landed with no test. Item 5 now reads the link
  register as well and asserts that `LNK-INTERREPLICA`'s transport, dial direction, and lifetime cells
  agree with the `spec/07` §7.2 sentences they are derived from, which are the sentence stating that the
  coordinator forwarding mechanism reuses the same internal gRPC `ForwardMessage` RPC used for all
  cross-replica message routing and the sentence stating that a non-coordinator replica forwards the
  message to the session's coordinator (`spec/07_session-lifecycle.md` line 330, inside §7.2 at line 114).
  The item's title and `// diagnosis:` sentence cover the connection as well as the store, and its
  `// spec:` annotation gains §7.2. Extending the existing tier-11 case rather than adding a parallel one
  keeps a single test reconciling §28.3 against the sections it derives from.
- **Two line anchors into proposal 0064 were off by one and landed on blank lines.** §1 cited the opening
  of SPEC-1's Change text at line 1140 and AMEND-1 item 5 cited the sentence
  `The §28.3 table records the spelling each carrier takes, per N7.` at line 1370, and both lines are
  empty. The second is a staged insertion anchor, so an apply agent working by line number would have
  inserted the new sentence before the paragraph rather than after its first sentence. The two anchors are
  now 1141 and 1371, which are the lines carrying the quoted text. Every other 0064 anchor AMEND-1 gives
  (316, 1036, 1126 through 1128, 1304 through 1306, 1314 through 1317, 1347, and 6089) was re-measured on
  the current tree and is correct.

### Pass 3 (2026-08-03, automated)

- **The naming table omitted the `CH-RUNTIMEOPS` schema-path row 0064 requires, so 0064 SPEC-2's identifier
  run would have aborted.** `CH-RUNTIMEOPS` carries three path-carrier and Go-symbol spellings rather than
  two. Proposal 0064 lines 1429 through 1431 and 1484 through 1485 state that the §28.3 naming table renames
  `schemas/lifecycle-events.schema.json` to `schemas/runtime-ops-events.schema.json`, and that file is in
  the tree today. None of the eight rows the document previously counted retires the stem `lifecycle-events`,
  and `findSites` matches a path spelling by case-sensitive prefix on token boundaries
  (`scripts/specshift/identifier/site.go` lines 56 through 58 and 90 through 94), so `moveOf` would have
  returned no site and left the schema unmoved (`scripts/specshift/identifier/identifier.go` lines 503
  through 505), and the sense-register entry 0064 seeds for that path would have been reported as unclaimed
  and failed the run (lines 605 through 606 and 613 through 614). The pass's own fixtures carry the row
  (`scripts/specshift/testdata/idpass/spec/spec/28_communication-channels.md`). §28.3's staged table now
  carries a seventh row whose channel is `CH-RUNTIMEOPS`, whose carrier is `path`, whose retired spelling is
  `lifecycle-events`, and whose canonical spelling is `runtime-ops-events`. That row mints nothing, because
  0064 states both spellings, so decision 3 holds. Decision 3, §4.3, §4.6, §5 row 2,
  AMEND-1 item 5, and §11 item 3 now state seven landed rows of the nine the pass needs, and each names
  SPEC-2's two remaining rows as the Go symbol stem and the `pkg/adapter/lifecyclechannel.go` file stem so
  that the third path carrier is no longer folded into the phrase "file stem".

### Pass 4 (2026-08-03, automated)

- **The naming table's flag row retired a spelling that occurs in no file the identifier pass may write, so
  the flag would never have been renamed.** The row retired `--lifecycle-socket`, and `findSites` selects a
  site by a case-sensitive prefix test of the retired spelling against file content on token boundaries
  (`scripts/specshift/identifier/site.go` lines 56 through 58 and 91 through 96), so a spelling absent from
  the write domain resolves no site while `LoadTable` still accepts the row. The only occurrences of
  `--lifecycle-socket` in the tracked tree are in `BUILD-GAPS.md`, `gateway-runtime-comms.md`, and
  `gateway-runtime-comms-remediation.md`, each listed in `scope.readExcludedFiles`
  (`scripts/specshift/scope/scope.go` lines 88 through 94) as outside the read domain of every pass and
  every gate, and the in-domain occurrence is the bare token `lifecycle-socket` in the declaration at
  `cmd/lenny-adapter/main.go` line 151. The miss would have been silent, because the residual scan looks for
  the same unmatchable spelling, leaving 0064 SPEC-2's stated rename of the flag undone and N7 unsatisfied on
  that carrier. The row now retires `lifecycle-socket` and states the canonical spelling
  `runtime-ops-socket`, which is 0064's `--runtime-ops-socket` without the shell prefix. §4.3 states that a
  row's retired spelling is the token as its carrier writes it and gives the site-matcher reason, decision 3
  records why dropping the double hyphen mints nothing, and §8 item 1 asserts that every staged retired
  spelling occurs at least once inside the write domain, which is the check that catches the class of defect.
  The `socket` row already followed the rule by retiring `@lenny-lifecycle`.
- **The staged N3 closed the prohibition's root-level domain to `README.md` and `TESTING.md`, contradicting
  the implemented predicate and 0064 §4.1.** `scope.ReservedPhraseCarrier` admits every tracked root-level
  markdown document (`scripts/specshift/scope/scope.go` lines 480 through 489), and the predicate is held
  there so that the pass that writes the sites and the lint that reads them share one statement of the
  domain (lines 475 through 477). Ten tracked root-level markdown documents beyond the excluded records,
  among them `AGENTS.md`, `CONTRIBUTING.md`, and `SECURITY.md`, sit inside the implemented domain and outside
  the staged one, so landing the sentence as written would have put the normative rule and the code that
  enforces it in disagreement, and would also have left the following exclusion sentence excluding root-level
  records from a domain that never contained them. 0064 §4.1 lines 372 through 375 state the broader form and
  name `README.md` and `TESTING.md` as the two that carry the phrase today rather than as the domain. N3 now
  reads "a tracked root-level markdown document the exclusion list below leaves in scope, of which
  `README.md` and `TESTING.md` are the two that carry the phrase today", which is 0064's wording, and §8
  item 4 gains an assertion that the domain N3 states matches the trees and extensions
  `scope.ReservedPhraseCarrier` admits.

### Pass 5 (2026-08-03, automated)

- **AMEND-1's edit set could not reach two sentences that its own exit check forbids.** AMEND-1 closes with
  a grep confirmation that no sentence in proposal 0064 outside its convergence record still assigns
  creation of `spec/28_communication-channels.md` to SPEC-1, and two sentences in SPEC-3's Change text, at
  lines 1953 and 2704, assert that SPEC-1 authored the §28.3 register and the §28.1 through §28.4 headings.
  Both sit far above the convergence record, which starts at line 3376, and both now contradict the amended
  SPEC-1 Target and Change text, so applying the ten staged edits alone left proposal 0064 internally
  inconsistent about who authored the four subsections while the exit check read as unmet. AMEND-1's Target
  now names the two sites, items 11 and 12 stage them, and the closing paragraph records that both are
  justification clauses rather than instructions, so the bound on changing SPEC-3's actions still holds. The
  split between the two documents is preserved in the replacement text: proposal 0067 authored the sections
  and the register, and SPEC-1 keeps the `tests/spec-map.json` key and the `tests/spec-map-exceptions.yaml`
  entry that decision 5 leaves with it.

## 11. Open decisions for review

1. **Whether to take the smaller route instead.** Amending 0064 SPEC-1 to split itself into a pass-free
   part, which authors `spec/28` §28.1 through §28.4 and the `spec/README.md` rows, and a pass-driven
   part, which performs the reserved-phrase removal and lands the lint, would achieve the same landing with
   no new proposal. This document takes the amendment route instead, for the reason decision 1 states. A
   reviewer who prefers the split should reject this proposal and direct the amendment to 0064.
2. **The derived column values in §28.3.** The plane value and the authority-direction value of each
   channel row are derived here from the entry's purpose under §28.2's definitions, because no source
   states them per entry. The judgements a reviewer should check are `CH-CHECKPOINT` and `CH-OBJSTORE` as
   state rather than content, `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` as content rather than control, and
   `CH-EVENTRELAY` as state rather than content. Confirm them, or name different values.
3. **The `CH-RUNTIMEOPS` Go symbol stem and `pkg/adapter/lifecyclechannel.go` file stem.** AMEND-1 item 5
   assigns both to 0064 SPEC-2, which is the sub-step that performs the rename and which already states the
   `CH-ADAPTEREVENTS` counterparts. Confirm that assignment, or direct that this proposal state the two
   spellings and write their naming-table rows. The third `CH-RUNTIMEOPS` path carrier, the schema file
   `schemas/lifecycle-events.schema.json`, is not in that question: 0064 lines 1484 and 1485 state both its
   retired and its canonical spelling, so §28.3 carries its row and the table lands with seven rows of the
   nine the pass needs.

## 12. Files touched on application

- `spec/28_communication-channels.md`, new, carrying §28, §28.1 the naming law, §28.2 the taxonomy and
  axes, §28.3 the three registers and the naming table, and §28.4 the claim register, exactly as SPEC-1
  stages them.
- `spec/README.md`, for the §28 index row and the four subsection rows appended after the `27.10
  Roll-forward notes` row, exactly as SPEC-2 stages them.
- `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, for the twelve
  sentence-level edits AMEND-1 states: five in SPEC-1's Target and Change text, which change its `spec/28`
  and `spec/README.md` obligations from authoring to confirmation and drop the stale index position claim,
  two in SPEC-2's Target and Change text, which give it the `spec/28` target and the obligation to write
  the naming-table rows for the spellings it fixes, three in §3.5, §4.8, and §11, which are the
  remaining sentences stating that SPEC-1 creates the file, and two justification clauses in SPEC-3's
  Change text, which credit the §28.1 through §28.4 sections and the §28.3 register to proposal 0067 while
  leaving SPEC-1's spec-map key and exceptions-entry obligation and SPEC-3's own actions unchanged. After
  the edits, no sentence in proposal 0064 outside its §9 convergence record assigns creation of
  `spec/28_communication-channels.md` or of its four subsections to SPEC-1.
- `scripts/specshift/identifier/table_test.go` and `scripts/specshift/name/declare_test.go`, for the tier-1
  cases §8 items 1 and 2 state, added to the existing files.
- `tests/tier11_docs/spec_28_index_rows_test.go`, `tests/tier11_docs/spec_28_reserved_phrase_test.go`, and
  `tests/tier11_docs/spec_28_register_writers_test.go`, new, for the tier-11 cases §8 items 3, 4, and 5
  state. `tests/tier11_docs` is outside `componentAndAboveTierDirs` (`cmd/lenny-test/cmd_validate.go` lines
  125 through 138), so the three files need no `tests/spec-map.json` key, and the change-graph completeness
  check excludes `_test.go` (line 398), so none of the five test files needs a `tests/change-graph.json`
  entry.
- No register or map data file under `tests/` is touched, and no file under `pkg/`, `cmd/`, `sdks/`,
  `schemas/`, `charts/`, or `docs/` is touched, per decision 5 and §7.
