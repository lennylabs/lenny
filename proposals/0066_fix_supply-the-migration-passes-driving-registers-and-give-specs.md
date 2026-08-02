# Proposal: Supply the migration passes' driving registers and give specshift a write-confinement option so proposal 0064 can be applied

- **Status:** Verified (2026-08-01). Converged after 11 adversarial review rounds (46 findings fixed);
  awaiting sign-off.
- **Lands after:** `proposals/0065_new_build-the-specification-migration-tooling-and-its-gates.md`, which is
  implemented. **Lands before:**
  `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, which cannot be
  applied until this proposal lands. Nothing here depends on 0064.
- **Date:** 2026-08-01.
- **Scope:** The data and the one tool option that proposal 0064's specification phase reads but cannot
  create. This proposal changes no file under `spec/`. It seeds the reserved-phrase sense register, the
  pinned-literal register, the specification-slice half of the identifier sense register, the anchor-move
  map, and the anchor sense register, it gives `scripts/specshift` a required write-confinement option so
  one pass, one register, and one fail-closed contract run once inside the specification-apply phase over
  `spec/` and once in the code phase over the rest of the tree, it makes the passes' lister see a file
  the same sub-step has authored but not yet staged, and it states those two invocations in 0064's own
  mechanical sub-steps, which is the only channel that reaches the agent that runs them. The rewrites
  those passes then perform stay in 0064.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file.
Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Proposal 0064 is approved and its prerequisite 0065 is implemented, and 0064 still cannot be applied. The
reason is not in 0064's text. It is that the files its mechanical edits read are absent from the tree, and
that the tool those edits invoke has no way to write less than its whole domain, while the phase that runs
them may write exactly one file.

Both halves are supply problems in the tree rather than defects in an approved document, and both have to
be fixed in a change that touches no file under `spec/`. A change that staged a specification edit would
put its own content back into the phase whose constraint it exists to work around. This proposal therefore
carries test infrastructure under `tests/`, tool code under `scripts/`, and AMEND-1's sentences in
proposal 0064, and nothing else, so the
implementation pipeline plans zero specification edits for it and implements every file in the code phase,
which is the phase permitted to write them.

## 1. Problem

**The data the passes read is absent.** `scripts/specshift`'s name, identifier, and anchor passes are each
driven by a per-occurrence register, and a run fails closed when one is missing. `go run ./scripts/specshift
-pass name -register tests/registers/reserved-phrase-senses.yaml` exits 1 with `read the reserved-phrase
sense register tests/registers/reserved-phrase-senses.yaml: open ...: no such file or directory`, formatted
at `scripts/specshift/name/register.go` line 98 from the flag value, with the flag validated before any file
is read (`scripts/specshift/main.go` lines 171 through 180). Measured against the tree,
`tests/registers/reserved-phrase-senses.yaml`, `tests/registers/identifier-senses.yaml`,
`tests/registers/anchor-senses.yaml`, and `tests/spec-anchor-moves.json` are all absent, while
`tests/registers/line-citations.yaml` and `tests/registers/line-citation-resolution.yaml` exist because
proposal 0065 seeded them. 0065 scopes its register bullet to "the per-class registers and baselines this
proposal seeds" (0065 §11), and 0064 assigns the four missing artifacts to its own sub-steps (0064 §6,
SPEC-1 through SPEC-4).

A fifth artifact is required and is named by no proposal. The name pass also reads
`tests/registers/pinned-spec-literals.yaml` whenever the tree carries a Go carrier under
`tests/tier11_docs/` (`scripts/specshift/name/pinned.go` lines 30 and 126 through 145), which it does, with
42 such files. That register and the anchor sense register are read from fixed paths inside `Rewrite`
rather than from the command line (`scripts/specshift/name/name.go` lines 160 through 162,
`scripts/specshift/anchor/anchor.go` lines 135 through 138 and 275 through 286), so their absence aborts the
run partway through the walk, with the tree byte-identical because `Harness.Apply` plans the whole diff
before writing (`scripts/specshift/pass/pass.go` lines 441 through 464).

All five paths sit under `tests/`. The implementation pipeline applies and commits every `spec/` edit
before any non-spec file is written, constrains each apply agent to one `spec/` file, and constrains each
sub-step commit to `spec/` alone (`.claude/workflows/implement-proposal.js` lines 296 through 305 and 466
through 470). 0064's SPEC-1 therefore cannot create the register its own mechanical edit reads, and the run
stops with `spec-unappliable` (`.claude/workflows/implement-proposal.js` lines 312 and 337 through 346;
`.claude/skills/implement-proposal/SKILL.md` lines 22 and 64).

**Seeding alone does not make 0064 appliable, because a run cannot be confined.** The write domain is
computed once, from the tracked tree less the read, planning, and generated exclusions
(`scripts/specshift/scope/scope.go` lines 416 through 453 and 817 through 837), and `Plan` walks all of it
(`scripts/specshift/pass/pass.go` line 296). Measured, the name and identifier passes report 5297 files and
the anchor and line passes 5300, of which the same 28 are under `spec/`. A run writes only the files whose
contents change, which for the name pass is of order one hundred files (125 tracked files carry a matching
reserved phrase, 12 of them under `spec/`). Either way, a run invoked in the specification-apply phase
writes files outside the one `spec/` file that phase permits. The flag surface is `-root`, `-pass`,
`-register`, `-apply`, and `-domain` (`scripts/specshift/main.go` lines 26 through 33 and 103 through 107),
and no seam reachable from the command line narrows the walk.

**The specification phase's agent fan-out is derived from the sub-step's named `spec/` targets, and none of
those targets carries a site.** `.claude/workflows/implement-proposal.js` lines 289 through 296 compute the
sub-step's distinct target files and spawn one agent per file, in parallel, each carrying `HARD CONSTRAINT:
the only file you may edit is <repo>/<file>`, and each MECHANICAL entry instructs that agent to run the
command itself. The plan agent extracts one `specEdits` entry per `spec/` path the subsection's Target list
names, and a subsection naming several becomes one entry per file (line 202). 0064's SPEC-1 names three
`spec/` paths: `spec/28_communication-channels.md`, `spec/03_high-level-architecture.md`, and
`spec/README.md`. It names the reserved phrases by domain rather than by file, and 0064 deliberately
enumerates no edit sites for a mechanical sub-step, so nothing the plan agent reads widens the fan-out to
the carriers. Measured, the 12 `spec/` files carrying a reserved phrase are `spec/04_system-components.md`,
`spec/05_runtime-registry-and-pool-model.md`, `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`,
`spec/10_gateway-internals.md`, `spec/13_security-model.md`, `spec/15_external-api-surface.md`,
`spec/16_observability.md`, `spec/17_deployment-topology.md`, `spec/18_build-sequence.md`,
`spec/24_lenny-ctl-command-reference.md`, and `spec/26_reference-runtime-catalog.md`, while
`spec/03_high-level-architecture.md` and `spec/README.md` match zero times. A confinement expressed per
named target would therefore cover no carrier and plan an empty diff, which the specification-phase
verifier treats as a failure (`.claude/workflows/implement-proposal.js` line 375). The same holds for
SPEC-2, whose Target list names one `spec/` path while the identifier occurrences under `spec/` span four
files, and for SPEC-3, which stages a run of the line pass over the shifted tree as one atomic sub-step
with its reduction (0064 lines 2531, 2550 through 2552, and 2575 through 2576), stages a run of the anchor
pass over the markdown links into the anchors that reduction retires (0064 lines 2594 through 2597), and
names twelve `spec/` paths in its Target list.

**The apply agent confirms a mechanical command's file list and diff against the sub-step's own text.**
Before applying, it runs the dry-run form and confirms that the command "touches only files this sub-step
targets", then confirms that "the applied diff for this file matches what the dry run predicted", and it
records the edit as unappliable when either fails (`.claude/workflows/implement-proposal.js` line 312).
A run confined to `spec/` writes the class's `spec/` carriers rather than the sub-step's named targets, and
the mechanical diff for a target that carries no site is empty, which the verifier reads as a failure
(line 375). Both facts have to be stated where those agents read them, which is the sub-step's Change
paragraph.

**A per-file confinement would also race.** Were the fan-out widened to the 12 carriers, the mechanical
invocations would run concurrently, and the second and later of them would abort: once the first apply
lands, every register entry for a rewritten file has an occurrence above the count of sites the file now
carries, so `unclaimedReason` returns `the file carries 0 reserved-phrase site(s)` and `checkClaimed` fails
the run (`scripts/specshift/name/name.go` lines 277 through 328). Every run of the name pass also indexes
the declared identifier space from `spec/28*` before it enumerates any site
(`scripts/specshift/name/name.go` lines 160 and 239, `scripts/specshift/name/declare.go` lines 20 and 96
through 101), so a mechanical invocation that starts before the agent authoring §28 has written its tables
aborts as well.

What is needed is a change that supplies both halves and touches no file under `spec/`: the five driving
artifacts, seeded per occurrence on the same fail-closed contract 0065's registers use; a confinement
option that lets one invocation cover the whole of `spec/` and a complementary one cover the rest of the
tree; a lister that sees the §28 file the same sub-step has just authored but not yet staged; and, in
0064's own mechanical sub-steps, the two command lines, the one target file that decides which agent runs
them under which confinement, and the sentences stating which files that run writes.

## 2. Decisions

1. **This proposal stages no edit under `spec/`.** Its content is test infrastructure under `tests/`, tool
   code under `scripts/`, and AMEND-1's sentences in `proposals/0064_*.md`, so the pipeline plans zero
   specification edits for it and implements every file in the code phase. That is the property that lets
   it supply what 0064's specification phase reads.
2. **Proposal 0065 is not edited, and proposal 0064 is edited only in the sentences that state how its
   mechanical sub-steps are invoked.** Two properties this design depends on can reach the apply agent
   through no other artifact, so AMEND-1 stages them into 0064 itself.

   The first is the confinement value. The plan agent composes a mechanical edit's command from the
   proposal it reads (`.claude/workflows/implement-proposal.js` line 202), and it can compose the
   `-register` value because 0064 names the register path in its own Target list. It can compose no
   confinement, because 0064 mentions none anywhere and states the opposite, that "The name pass walks the
   whole domain N3 states in one run"
   (`proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md` line 1162). With
   TOOL-1 item 1 applied, an unconfined invocation is a usage error, and the apply agent records the edit
   as unappliable and stops (`.claude/workflows/implement-proposal.js` lines 312 and 337 through 346),
   which is the `spec-unappliable` outcome this proposal exists to remove.

   The second is which agent issues the invocation. The apply phase computes the sub-step's distinct
   target files and spawns one agent per file, in parallel, each of which runs the command a mechanical
   entry names (lines 289 through 296 and 305 through 312), so a mechanical edit carrying several `spec/`
   targets becomes the same confined command run concurrently by several agents. AMEND-1 gives each
   mechanical edit one `spec/` target file, which makes the plan agent emit one mechanical entry and one
   agent run it.

   The amendment restates how a sub-step is run rather than what it changes, and it is the whole of this
   proposal's edit to either proposal. Sign-off on this proposal is sign-off on that amendment to an
   already-approved 0064.
3. **One register per class, at the path the tool already names.** A per-confinement register pair was
   rejected: the anchor sense register and the pinned-literal register are read from hardcoded paths
   (`scripts/specshift/anchor/senses.go` line 24, `scripts/specshift/name/pinned.go` line 30), 0064 names
   the single paths in its Target lists, and a split register would put the two halves of one enumeration
   out of step.
4. **The confinement is an explicit path set rather than a two-valued specification-or-remainder switch.**
   `-only <path>` is repeatable and matches a tracked path or a directory prefix, and `-except <path>` is
   its repeatable complement. A two-valued switch would name `spec/` in the tool, which no gate's
   population and no later migration is bound to.

   A mechanical sub-step runs each pass it stages exactly twice: once as `-only spec/`, issued by the one
   apply agent AMEND-1 assigns the mechanical edit to, and once as `-except spec/` in the code phase.
   SPEC-1 and SPEC-2 stage one pass each, and SPEC-3 and SPEC-4 stage two each, so those two sub-steps
   issue two confined invocations per phase from the same agent. The assigned file
   is `spec/28_communication-channels.md` for SPEC-1, `spec/15_external-api-surface.md` for SPEC-2,
   `spec/04_system-components.md` for SPEC-3, which carries the larger citation population below the
   shifted ranges the line pass reads (0064 lines 2531 through 2533) and the subsection headings the same
   sub-step inserts ahead of that pass, and which also issues SPEC-3's anchor invocation over the links its
   reduction retires (0064 lines 2594 through 2597), and
   `spec/15_external-api-surface.md` for SPEC-4, which is the carrier of the retired §15.4 anchors. For
   SPEC-1 the assignment orders the invocation after the §28 declaration
   alone, because the assigned file is the one whose authored edit declares the identifier space the pass
   indexes, and one agent applies the edits handed to it in order; the ordering against SPEC-1's own three
   hand corrections stays open, and §11 decision 8 records it. SPEC-2 needs no such ordering, because §28
   is committed by SPEC-1 before SPEC-2 runs. §11 records the orderings SPEC-1's, SPEC-3's, and SPEC-4's
   assignments do not establish. A per-file confinement was rejected on the two measurements §1 states: the agent fan-out is
   derived from the sub-step's named `spec/` targets, none of which carries a site, and concurrent
   per-file invocations race on the declared identifier index and on the entries each other has consumed.
   One invocation per pass per phase removes both, and it makes the covering obligation in decision 8 a
   two-term partition a test can state.

   That invocation writes the 12 `spec/` carriers while its agent's `HARD CONSTRAINT` names one file, and
   the apply agent's mechanical branch asks two further confirmations before it applies: that the command's
   dry run "touches only files this sub-step targets", and that "the applied diff for this file matches what
   the dry run predicted". Failing either, the agent records the edit as unappliable and stops
   (`.claude/workflows/implement-proposal.js` line 312), which is the `spec-unappliable` outcome this
   proposal exists to remove. Neither confirmation holds on its face. The dry run names the 12 carriers §1
   measures, and SPEC-1's Target list names none of them; the mechanical diff for the assigned
   `spec/28_communication-channels.md` is empty, because §28 describes the banned spellings rather than
   reproducing them and so carries no site. AMEND-1 item 5 therefore states both facts in the Change
   paragraph the apply agent and the verifier read, which is the artifact that says what the sub-step
   targets and what its mechanical edit is expected to write. The sentences the two agents apply to a
   wider-than-targeted file list and to an empty mechanical diff sit in the workflow, which §7 leaves
   unchanged, so §11 decision 6 records the residue for the reviewer.

   The deviation is bounded by the phase's own commit scope: the sub-step commit agent stages whatever
   changed under `spec/` (`.claude/workflows/implement-proposal.js` lines 466 through 484), and the verifier
   reads `git diff` for the file it owns (lines 371 through 373). The confinement is what keeps the
   invocation inside that scope, because it prevents the run from writing the approximately 113 carriers
   outside `spec/` in a phase whose commit covers `spec/` alone.
5. **The confinement is required for every run of a pass, apply and dry run alike, with no whole-tree
   value.** A default spanning the tree is the failure this change exists to prevent: it would let a
   specification-phase invocation write about a hundred files outside that phase's commit scope, silently.
   `-domain` takes the flags as optional and prints the whole domain without them, and it honors them when
   they are given, so an operator can measure a confinement before applying it.
6. **The filter is one predicate carried on `Harness` and applied to the domain inside `Plan`.**
   `scope.WriteDomain`, `scope.KeyWriteDomain`, `scope.Writable`, `NewHarness`, and `NewHarnessOver` keep
   their signatures, so `scope` stays the one implementation of the domain and the change is confined to
   the walk. `NewHarnessOver` alone has about 35 call sites in `scripts/specshift/run_test.go`, none of
   which a signature change would leave untouched.
7. **`scope.Writable` stays the whole-tree predicate.** The name and identifier passes ask it in the tree
   direction when they check that every register entry is claimed
   (`scripts/specshift/name/name.go` lines 302 through 328,
   `scripts/specshift/identifier/identifier.go` lines 579 through 613). Narrowing `Writable` would make an
   entry for a code carrier read as an entry outside the pass's write domain.
8. **The register's claimed-entry check is filtered by the run's confinement, in the two passes that carry
   one.** The name and the identifier passes carry a claimed-entry check and the line and the anchor passes
   carry none (`grep -rn "checkClaimed" scripts/specshift/` matches
   `scripts/specshift/name/name.go` line 277 and `scripts/specshift/identifier/identifier.go` line 590
   alone). For the line and the anchor passes a register entry outside the confinement is therefore neither
   consumed nor rejected, and a replayed run over an already-rewritten tree finds no site and exits zero
   with an empty diff rather than aborting. Decision 13's report is what covers those two passes, and §5
   states the outcome for each pass in its own row. In the two passes that carry it, `checkClaimed` skips
   an entry whose file the run does not cover. Without the filter the second parallel apply agent aborts on
   the file the first already rewrote, and the code-phase run aborts on every entry the specification phase
   already applied. Each run checks its register in both directions over its own confinement.

   The filter creates a covering obligation, which this proposal states as a requirement on the caller
   rather than as a property the tool can prove from inside one run: the confinements of a sub-step's
   invocations must partition the pass's write domain, so that every register entry is checked by exactly
   one of them. Decision 4's two-term form, `-only spec/` and `-except spec/`, is that partition, and §8
   case 4 asserts it over the fixture tree. A run that defers entries reports them per decision 13, and the
   gates named there stay red until the complementary run lands, so an unmet obligation is a red gate
   rather than a silent pass.
9. **The key-rewrite channel runs only where the confinement covers a path-keyed register.** Every
   path-keyed register is a `tests/registers/*.yaml` file or one of the two root-level maps
   `tests/change-graph.json` and `tests/spec-map.json` (`scripts/specshift/scope/scope.go` lines 298
   through 345), so a confinement of `-only spec/` covers none of them and the channel is skipped.
   Both emptiness guards are kept for a confinement that does cover one, with the message naming the
   confinement.
10. **The weakening of 0064's atomicity is an accepted failure mode.** 0064 requires application as one
    exclusive change on a quiesced tree, whose stated hazard is a concurrent edit by another change to an
    adapter handler file or the adapter proto. A split into two sequential commits of one in-flight change
    does not violate that requirement, and it does leave a window in which `spec/` carries a canonical
    identifier that the code, the documentation, and the pinned test literals still spell the retired way.
11. **Tier 11 and the citation resolver are the gates that observe the window, and the anchor half of the
    split is unobserved.**
    The concrete case is `spec/04_system-components.md` line 489, whose clause `signals its coordinating
    gateway replica over the per-pod adapter-to-gateway control channel` is pinned verbatim at
    `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69. Tier 11 goes red when the
    specification confinement lands and returns to green when the remainder run rewrites the pinned
    literal. The retired-anchor inconsistency outside `spec/` is observed by nothing across the window. The
    fragment-link gate that would report it does not exist in the tree, 0064 lands it as a Go test under
    `tests/tier0_static/` (0064 line 3119), which is a non-`spec/` target the pipeline implements in the
    code phase (`.claude/workflows/implement-proposal.js` line 202), and 0064 arranges for it to land green
    by hand-correcting the six pre-existing broken links in the sub-step that lands it (0064 lines 4136
    through 4160). The citations from carriers outside `spec/` into the ranges SPEC-3's reduction shifts are
    observed, by the committed citation-resolution gate against the baseline 0065 seeds, per §3.3. §11
    records the decisions this leaves open, which are that SPEC-1 names tier 11 as its own exit criterion,
    that SPEC-3 names the citation resolver against its baseline together with tier 11 as its own exit
    criterion, that SPEC-4 names a line-citation register at zero as its own exit criterion, and that the
    reviewer accepts a window these two gates observe in part.
12. **Consistency is re-established by the remainder run in 0064's code phase**, which rewrites the code,
    schema, chart, documentation, and pinned-literal spellings, followed by the gates 0064 lands there: the
    naming lint, the identifier-resolution gate, the citation and fragment-link gates, the per-class
    residual scans, and tier 11.
13. **The tool reports the confinement rather than leaving it to be inferred.** A confined run names the
    confinement it ran under, the number of files it planned, the register entries it deferred because
    their files lie outside the confinement, and the gates that stay red until every entry is covered. The
    report covers all four passes. For the name and the identifier passes the deferred set is the set
    `checkClaimed` skipped; for the line and the anchor passes, which carry no claimed-entry check, it is
    derived from the driving register the run already holds in full, which is the per-file count map for the
    line pass and the sense register for the anchor pass. That
    report reaches the agent that ran the command and the operator reading its output. It does not reach
    the specification-phase verifier, whose method is to read the proposal subsection, read the target
    file, and run `git diff` (`.claude/workflows/implement-proposal.js` lines 371 through 373), so no claim
    in this proposal rests on the verifier reading it.
14. **Seeded register content carries no specimen of a reserved phrase or a retired spelling in a free-text
    field.** The reason is the residual scan rather than the naming lint: `scope.ReservedPhraseCarrier`
    returns false for `tests/registers/*.yaml` (`scripts/specshift/scope/scope.go` lines 480 through 490),
    so the naming lint does not read these files, while `classRegisters` excludes a class's own sense
    register from that class's scan alone (`scripts/specshift/scope/scope.go` lines 152 through 208), which
    leaves the identifier register inside the reserved-phrase class's scan and the reserved-phrase register
    inside the identifier class's scan. A specimen written into a YAML comment in either is a residual of
    the other class. The sense schemas carry no reason field, so this constrains YAML comments alone.
15. **The passes list the working tree as well as the index, and the gates keep the index.** The name pass indexes the declared
    identifier space from `spec/28*` before it enumerates any site
    (`scripts/specshift/name/name.go` lines 160 and 239, `scripts/specshift/name/declare.go` lines 20 and
    96 through 101), and `spec/28_communication-channels.md` is created by SPEC-1 and staged by nothing
    until the post-verification commit agent (`.claude/workflows/implement-proposal.js` lines 466 through
    484). A lister reading `git ls-files` alone therefore reports no such file and aborts every mechanical
    invocation of SPEC-1 with `index the declared identifiers: the tree carries no spec/28* specification
    file`, which is the `spec-unappliable` outcome this proposal exists to remove. `scope` therefore gains
    a second constructor, `WorkingTreeLister`, which unions `git ls-files -z` with
    `git ls-files -z --others --exclude-standard`, so a file that is authored, unignored, and not yet
    staged is a member, and the four pass constructions and the pass harness use it
    (`scripts/specshift/main.go` lines 81 through 84, `scripts/specshift/pass/pass.go` line 275).
    `--exclude-standard` keeps ignored scratch output outside the domain.

    `scope.GitLister` keeps the index alone and keeps its consumers, which are the line-citation ratchet,
    the citation-resolution gate, the residual gate, and the skip-reason classifier
    (`scripts/specshift/gate/ratchet.go` line 124, `scripts/specshift/gate/resolution.go` line 123,
    `tests/tier0_static/residual_gate_test.go` line 215,
    `tests/tier0_static/skip_reason_classifier_test.go` line 189). Widening the one lister would widen
    those four committed gates' read domain to unignored untracked files, which is a behavior change this
    proposal does not stage: every remedy those gates name is index-keyed, so an authored-but-uncommitted
    file carrying a retired-form citation would fail the ratchet with `Absent: true` and no permitted route
    to green (`scripts/specshift/gate/ratchet.go` lines 154 and 190 through 193,
    `tests/registers/line-citations.yaml` lines 6 through 7), and the residual gate's
    change-graph-coverage class would take a population its sibling check in `cmd/lenny-test` deliberately
    takes from the index for that same reason (`cmd/lenny-test/cmd_validate.go` lines 383 through 389).
    The two constructors are one definition each for the consumers they serve rather than two definitions
    of one membership: a pass must read the file the sub-step it runs inside has just authored, and a gate
    must audit what the tree carries under a remedy that exists.

## 3. Design overview

### 3.1 What lands

This proposal lands five driving artifacts, one tool option, one change to how the tool lists the tree, and
the sentences in 0064 that state how its mechanical sub-steps invoke the tool.

The artifacts are `tests/registers/reserved-phrase-senses.yaml` and
`tests/registers/pinned-spec-literals.yaml`, which drive the name pass; the specification-slice entries of
`tests/registers/identifier-senses.yaml`, which drive the identifier pass; and `tests/spec-anchor-moves.json`
with `tests/registers/anchor-senses.yaml`, which drive the anchor pass. Each is seeded per occurrence on
the fail-closed contract 0065's registers already carry, so a site an enumeration missed aborts the run
rather than being rewritten wrongly.

The option is a required write confinement on `scripts/specshift`: `-only` and `-except`, both repeatable,
filtering the walk without redefining the domain. The listing change gives `scope` a second constructor,
`WorkingTreeLister`, which unions the index with the unignored untracked paths of the working tree and which
the passes use, so a pass reads the specification section the same sub-step has just authored while the
committed gates keep the index-only `GitLister`. `tests/registers/README.md` gains a row per seeded register naming the
pass that reads it, which half of it is outstanding, and the run that empties it where one does.

### 3.2 The two-run protocol

Each pass a mechanical sub-step of 0064 stages becomes two invocations rather than one, so a sub-step that
stages two passes becomes four invocations across the two phases, and the pair per pass is what makes the
sub-step appliable inside a phase whose commit covers `spec/` alone.

Inside the specification-apply phase, the apply agent AMEND-1 assigns the mechanical edit to runs the pass
confined to `spec/`, as `go run ./scripts/specshift -pass name -register
tests/registers/reserved-phrase-senses.yaml -only spec/ -apply`. Both the confinement and the assignment
reach that agent because AMEND-1 writes the command line into 0064's own sub-step and gives the mechanical
edit one `spec/` target file, per decision 2. That invocation runs after the authored
edit that declares the identifier space, because the name pass indexes the declared identifiers from
`spec/28*` before it enumerates a site and fails when no such file exists or when the files it finds
declare none (`scripts/specshift/name/declare.go` lines 96 through 101). Assigning the mechanical edit to
the declaring file is what establishes that ordering for SPEC-1: the edits handed to one agent
are applied in order, and the authored §28 edit precedes the mechanical one. In the code phase, the same
pass, the same register, and the same contract run once more as `-except spec/`, which covers every
remaining carrier: code, schemas, charts, documentation, root-level contract documents, and the pinned
literals under `tests/tier11_docs/`.

A sub-step therefore issues one confined invocation per pass per phase, because AMEND-1 leaves its
mechanical edit one target file and the fan-out spawns one agent per target file, and the two confinements partition
the pass's write domain, which is the covering obligation decision 8 states and §8 case 4 asserts. For the
name and the identifier passes the
register is checked in both directions over each run's own confinement, so an off-by-one enumeration still
aborts the run it belongs to, and the pair checks every entry because it covers every file. A pair of
invocations of either of those two passes whose confinements overlapped would not be safe: the second would
abort on entries the first consumed, which §5 records as a loud failure rather than a silent one, and §8
case 10 pins. The line and the anchor passes carry no claimed-entry check, so neither the abort nor the
skip applies to them: an entry outside the confinement is not read, and an overlapping second run plans an
empty diff and exits zero. The deferred-entry report decision 13 states is what covers those two passes,
and §5 states each outcome in its own row.

### 3.3 What the split costs, and what closes it

Between the two runs the tree is internally inconsistent by construction: `spec/` carries the canonical
spelling and every other carrier still holds the retired one. The inconsistency is observable rather than
silent for the reserved-phrase and identifier classes: tier 11 is red on every pinned literal whose
specification sentence was rewritten, and it returns to green when the remainder run lands in the code
phase of the same in-flight change. The anchor class's half of the window carries no such signal. The
fragment-link gate that would report a retired-anchor link outside `spec/` is landed by 0064 in the code
phase, after the window, and 0064 arranges for it to land green (0064 lines 3119 and 4136 through 4160),
so a link left pointing at a retired anchor between the two runs is reported by nothing. SPEC-3's split
carries its own signal: the citations from carriers outside `spec/` into the ranges its reduction shifts
resolve to the wrong line until the code-phase run converts them, and the committed citation-resolution
gate (`tests/tier0_static/spec_citation_resolution_test.go`) reports them against the baseline 0065 seeds.

That weakens 0064's one-exclusive-change requirement, and §5 records it as an accepted
failure mode rather than as a solved one. The alternative is to hold 0064 until the pipeline can write a
non-specification file inside a specification sub-step, which is a change to
`.claude/workflows/implement-proposal.js` this proposal does not stage.

### 3.4 How this document refers to proposals 0064 and 0065

References to SPEC-1 through SPEC-4 name 0064's four specification sub-steps: SPEC-1 lands the naming law,
the registers, and the reserved-phrase removal and runs the name pass; SPEC-2 lands the wire-contract
rename and runs the identifier pass; SPEC-3 writes the new specification sections, performs the reductions,
and runs both the line pass over the shifted tree and the anchor pass over the links its reductions retire;
SPEC-4 retires the redirected anchors and the line citations. References to TOOL-1 and TEST-1
of 0065 name the tooling and the gate cases that proposal landed. This proposal's own staged changes are
SUPPLY-1 through SUPPLY-3, TOOL-1, TEST-1, and AMEND-1; where the two documents' labels collide, the label
without a proposal number is this document's.

## 4. Detailed design

### 4.1 The confinement option

`scripts/specshift` gains two repeatable flags. `-only <path>` admits a tracked path when it equals the
flag value or sits under it as a directory prefix, where the prefix match is terminated by a path
separator, so `-only spec` admits `spec/04_system-components.md` and admits no sibling path that merely
begins with the same characters. `-except <path>` excludes on the same match rule. Both may be repeated,
and both may be given together, in which case a path is covered when some `-only` admits it and no
`-except` excludes it. When only `-except` values are given, every path the exclusions do not name is
covered.

Running a pass with neither flag is a usage error, in the manner of the existing missing-register error
(`scripts/specshift/main.go` lines 164 through 166). The confinement check sits beside that one, after the
`-domain` early return at lines 161 through 163 and before `LoadRegister`. `-domain` treats both flags as
optional and prints the whole write domain when neither is given.

The filter is a predicate carried on `Harness` and applied to the write domain in the two places the domain
is consumed: inside `Plan`, immediately after `scope.WriteDomain` returns and before the walk begins, and
inside `printWriteDomain` (`scripts/specshift/main.go` lines 228 through 239), which returns before `Plan`
is reached (`scripts/specshift/main.go` lines 161 through 163) and would otherwise print the whole domain
under any confinement. `-domain` under a confinement prints the confined domain and names the confinement
in its count line, which is what makes the partition in §8 case 4 measurable and what lets an operator
measure a confinement before applying it. `scope`'s exported surface is unchanged: the confinement narrows
what a run touches and never widens it, so every exclusion the domain already computes still holds. `Plan`
keeps the zero-file guard and states the confinement in its message, because a confinement matching no file
is an operator error that would otherwise report the empty diff of a completed migration.

The key-rewrite channel is skipped when the confinement covers no path-keyed register, and runs unchanged
when it covers one, with `KeyWriteDomain`'s own emptiness guard intact for that case.

The name and identifier passes receive the same predicate through an optional interface the harness applies
before the walk, and their `checkClaimed` implementations skip an entry whose file the confinement does not
cover. Nothing else about the check changes: an entry inside the confinement whose file the tree does not
carry, whose file sits outside the pass's write domain, or whose occurrence exceeds the site count still
fails the run. The pinned-literal claim check takes the same filter, and so does the identifier pass's
file-name rename planning, which is the one site class a pass reads outside the walk: `planRenames` runs
over the whole tracked tree inside `prepare` and aborts on a file-name carrier the register does not
record, before the confined walk begins (`scripts/specshift/identifier/identifier.go` lines 235 through
245 and 463 through 514). Filtering it is what keeps a `-only spec/` run from aborting on the two
file-name carriers under `pkg/adapter/`, and it leaves that abort to the complementary run, which is also
the run that performs the move. Every other site class a pass reads is a member of the walk and is
filtered with it.

The declared-identifier check is deliberately left unfiltered. `checkRegister` indexes `spec/28*` and
rejects any entry naming an identifier the specification declares nowhere, over the whole register rather
than over the confinement (`scripts/specshift/name/name.go` lines 239 through 260). Filtering it by the
confinement would let a `-only spec/` run pass over a register whose code-side entries name an undeclared
identifier, and the failure would surface only in the code phase. §8 case 16 pins the per-entry loop's
unfiltered behavior over an out-of-confinement entry, which is the only case that separates the unfiltered
form from a filtered one.

The index the check reads is built ahead of it, and its two hard errors are what make the ordering
constraint §3.2 states load-bearing. `declaredIdentifiers` fails when the tree carries no `spec/28*` file
and when the files it finds declare no identifier (`scripts/specshift/name/declare.go` lines 96 through
101), and `checkRegister` returns that error before it reaches any per-entry comparison
(`scripts/specshift/name/name.go` lines 239 through 242). A mechanical invocation that runs before the
declaring section exists therefore fails before any file is written and names the declaring file, which §8
case 9 pins.

### 4.2 What each seeded register carries

`tests/registers/reserved-phrase-senses.yaml` is keyed by file and 1-based occurrence and records the
canonical identifiers each reserved-phrase site denotes, with an optional replacement string for a site
denoting more than one (`scripts/specshift/name/register.go` lines 24 through 39).

`tests/registers/pinned-spec-literals.yaml` is keyed by file and by the 1-based position of the pinning
literal among every Go string literal its carrier holds in source order
(`scripts/specshift/name/pinned.go` lines 41 through 43). The index counts literals that pin nothing
alongside those that do, so it is derived from the file's whole literal sequence and shifts whenever a
tier-11 carrier gains or loses any string literal.

`tests/registers/identifier-senses.yaml` is keyed by file and occurrence and records, per site, the channel
the retired spelling denotes or that the site is not a channel at all
(`scripts/specshift/identifier/senses.go` lines 15 through 62). This proposal seeds its `spec/` entries
alone; SPEC-2 appends the remainder entries in the phase permitted to write them.

`tests/spec-anchor-moves.json` is keyed by retired anchor and carries one successor heading per anchor
(`scripts/specshift/anchor/moves.go` lines 34 through 46). `tests/registers/anchor-senses.yaml` is keyed by
file and occurrence and records the heading a bare section citation of a retired anchor means, in the
`<path>#<anchor>` spelling (`scripts/specshift/anchor/senses.go` lines 20 through 42).

### 4.3 Which seeded content can be checked before 0064 lands

Three of the five can be checked mechanically today and two cannot.

The pinned-literal register can be checked against the tree, and must be, because it is the one artifact
here whose mis-seeding does not fail closed. `checkPinnedClaimed` reports an entry only when its index
exceeds the file's literal count (`scripts/specshift/name/pinned.go` lines 179 through 183), so an in-range
but wrong index leaves the pinning literal outside the site set `findSites` builds and outside the set
`standing` re-checks (`scripts/specshift/name/name.go` lines 164, 176, and 359 through 360). The run exits
zero with the literal unwritten and tier 11 goes red only after 0064 has committed. The seeding is
therefore derived mechanically rather than counted by hand, and TEST-1 adds a tier-0 assertion over the
committed file.

That assertion is written so that it holds on the tree before 0064, during the window between the two runs,
and after the migration. No change empties this register: `loadPinned` requires it whenever the tree
carries a Go carrier under `tests/tier11_docs/` (`scripts/specshift/name/pinned.go` lines 136 through 145),
`loadPinnedLiterals` rejects a document whose `entries` list is empty (lines 83 through 85), and 0064 names
it nowhere. It is standing input to the name pass rather than a migration artifact. The assertion therefore
predicates on what survives the rewrite: the literal at each named position carries a reserved phrase or a
canonical identifier of the naming law. Before and during the migration the first disjunct holds, because
the pinned literals sit under `tests/tier11_docs/` and the specification-confined run does not touch them;
after the code-phase run the second disjunct holds. The assertion ranges over the register's entries alone
and does not require every reserved-phrase literal of a carrier to be registered, for the reason §8 case 8
gives.

The anchor-move map is checked in part: every successor must name a heading a document of the tree
declares, which the pass verifies before it rewrites anything
(`scripts/specshift/anchor/anchor.go` lines 255 through 272). The same check covers every destination the
anchor sense register carries, in the same call, and the same worktree run is what enumerates that
register's members per SUPPLY-3. The headings 0064's reductions create do not exist yet, so the check runs
in a scratch worktree carrying 0064's staged §28 and reduction text.

The reserved-phrase register and the identifier register cannot be checked end to end before §28 exists.
The name pass refuses an identifier the specification does not declare, and the identifier pass reads its
retired spellings from §28.3 (`scripts/specshift/identifier/table.go` lines 19 through 21). Verification of
those two before 0064 lands is a reviewer procedure in a scratch worktree rather than a gate, which §11
records as an open decision. The worktree procedure works because of decision 15: the passes' lister unions the
index with the unignored working tree, so a §28 file written into the worktree and left unstaged is read by
the pass, which is the same property SPEC-1's own mechanical invocation depends on.

## 5. Edge cases and accepted failure modes

| Case | Behavior | Where it is stated |
|:--|:--|:--|
| A pass meets a site its register does not carry, inside the run's confinement | The run aborts non-zero before any write, naming the site, and the tree stays byte-identical because `Apply` plans the whole diff first | The fail-closed rule each pass states, `scripts/specshift/pass/pass.go` lines 441 through 464; §6 TEST-1 case 7 |
| A pass meets such a site outside the run's confinement | The run does not read the site and does not abort. The complementary run reads it and aborts there. This holds for every site class, including the file-name carriers the identifier pass plans renames for outside the walk, which the same predicate filters | §4.1; §6 TOOL-1 item 4; §8 case 15 |
| A register entry names a file outside the run's confinement, for the name or the identifier pass | The claimed-entry check skips it and the run reports it as deferred. The complementary run checks it | Decision 8; §4.1 |
| A register entry names a file outside the run's confinement, for the line or the anchor pass | Neither pass carries a claimed-entry check, so nothing checks the entry in either direction and the entry is neither consumed nor rejected. TOOL-1 item 5 derives the deferred report for these two passes from the line pass's per-file count map and the anchor pass's sense register, so the entry is reported as deferred and the standing red-gate sentence names the gates that stay red | Decision 8; Decision 13; §6 TOOL-1 item 5; §8 case 17 |
| Neither `-only` nor `-except` is given on a run of a pass | Usage error, nothing written | Decision 5; §4.1 |
| A confinement matches no file in the write domain | The run fails with the zero-file guard, naming the confinement, rather than reporting an empty diff | §4.1 |
| Two invocations of one sub-step overlap in their confinements, for the name or the identifier pass | Not prevented by the tool. The second aborts on entries the first consumed, which is a loud failure rather than a silent one. AMEND-1 gives each mechanical edit one target file, so the fan-out spawns one issuing agent per sub-step and the two confinements are disjoint | Decision 2; §3.2; §6 AMEND-1; §8 case 10 |
| Two invocations of one sub-step overlap in their confinements, for the line or the anchor pass | Not prevented by the tool and not loud. Neither pass carries a claimed-entry check, so a replayed run over an already-rewritten tree finds no site, plans an empty diff, and exits zero reporting zero files (`scripts/specshift/line/line.go` lines 118 through 120, `scripts/specshift/anchor/anchor.go` lines 141 through 143, `scripts/specshift/main.go` lines 194 and 202). The deferred report TOOL-1 item 5 adds for these two passes is the signal, and §8 case 17 pins both the report and the empty-diff outcome | Decision 8; Decision 13; §6 TOOL-1 item 5; §8 case 17 |
| The anchor pass's two `-only spec/` invocations overlap across SPEC-3 and SPEC-4, rather than inside one sub-step | Accepted and silent. Both read the whole of `tests/spec-anchor-moves.json`, and the pass scopes its site search by the map alone (`scripts/specshift/anchor/site.go` lines 116 through 146), so SPEC-3's invocation rewrites every `spec/` reference into a retired anchor and SPEC-4's finds no site, plans zero files, and exits zero (`scripts/specshift/anchor/anchor.go` lines 141 through 143). AMEND-1 item 5 states the resulting empty diff for SPEC-4's assigned file. The pair of `-except spec/` runs stands in the same relation, and whichever of the two runs second writes nothing, which no specification-phase verifier reads. The row above covers two invocations of one sub-step; this overlap is across two | Decision 3; Decision 4; §6 AMEND-1 |
| A mechanical invocation runs before the authored edit that declares the identifier space | The run fails on the declared-identifier check before any file is written, naming the declaring file. For SPEC-1 the ordering is established by assigning the mechanical edit to the agent that owns the declaring file, which applies its edits in order, and SPEC-2 runs after SPEC-1 has committed §28 | §3.2; §4.1; §6 AMEND-1; §8 case 9 |
| SPEC-1's three hand-corrected sentences sit at reserved-phrase sites in files the same sub-step's `-only spec/` name run rewrites, and other agents own those files | Unresolved by this proposal. 0064 corrects the interrupt sentences at `spec/07_session-lifecycle.md` line 324 and `spec/15_external-api-surface.md` line 1755 and the slot-failure sentence at `spec/05_runtime-registry-and-pool-model.md` line 540 by hand rather than by substitution, "because a substitution turns each of them into a precise false statement" (0064 lines 1283 through 1286). All three are reserved-phrase sites, so SUPPLY-1's seeding carries an entry for each, and each is applied by an agent the pipeline spawns in parallel with the issuing agent (`.claude/workflows/implement-proposal.js` lines 289 through 297). Both terminal orderings fail. If the run lands first, it substitutes at the three sites 0064 forbids substituting at, and the sibling agent's authored edit no longer locates its anchor and is recorded as unappliable (`.claude/workflows/implement-proposal.js` line 312). If a hand correction lands first and removes the phrase, its file carries fewer sites than the register claims, `unclaimedReason` returns "the file carries N reserved-phrase site(s)" and `checkClaimed` fails the run before any write (`scripts/specshift/name/name.go` lines 243 and 277 through 323); this is deterministic for `spec/07_session-lifecycle.md`, whose single reserved-phrase site is the hand-corrected one (measured: `grep -oiE "(lifecycle\|control)[ -]channel" spec/07_session-lifecycle.md` returns 1) | §6 SUPPLY-1; §11 |
| SPEC-4's hand corrections sit in files other agents own, and its two mechanical invocations are issued by one of them | Unresolved by this proposal. The assignment removes the duplicate invocations and orders neither invocation after a hand correction another agent makes in the same sub-step | §11 |
| SPEC-3's line pass and anchor pass are mechanical runs whose two reduced files are applied by two agents, and its mechanical edit is assigned to one of them | Unresolved by this proposal, in three distinct ways. The assignment orders both runs after `spec/04`'s reduction and heading insertion and orders neither of them after `spec/15`'s reduction, which a parallel agent applies, and a citation converted against an unshifted `spec/15` carries a stale line number. It also orders neither run after the §28.5 channel-contract cards SPEC-3 authors in `spec/28_communication-channels.md` through a parallel agent, which the anchor pass's move successors and sense destinations name (0064 lines 2668 through 2669 and 2164 through 2166); `checkRegisters` returns a hard error before any file is rewritten when one of those headings is absent (`scripts/specshift/anchor/anchor.go` lines 240 through 272), so the anchor invocation aborts rather than writing a stale value. It orders the anchor invocation after none of the 14 markdown links SPEC-3 hand-corrects to a heading other than the map's successor, which are the seven `spec/07_session-lifecycle.md` links and the seven same-page `spec/15_external-api-surface.md` links into the retired `1541-adapterbinary-protocol` anchor (0064 lines 2784 through 2789 and 2830 through 2833); both files sit inside the `-only spec/` confinement and are applied by parallel agents, the anchor pass rewrites a link from the move map with no register consulted (`scripts/specshift/anchor/site.go` lines 129 through 146, `scripts/specshift/anchor/anchor.go` lines 186 through 190), and this residue is silent rather than an abort because the map's successor heading exists, so `checkRegisters` passes and the run rewrites to a resolving but wrong destination and exits zero | §6 AMEND-1; §11 |
| SPEC-3's reduction, its line pass, and its anchor pass are one atomic sub-step in 0064, and the split defers both passes over every non-`spec/` carrier to the code phase | Accepted, and wider than the window decision 10 records. Every citation into the shifted `spec/04` and `spec/15` ranges from a carrier outside `spec/` resolves to the wrong line until the `-except spec/` run lands, and the citation resolver is the gate that reports it, against the baseline 0065 seeds. The anchor half of SPEC-3's split carries no gate for the same reason SPEC-4's does | §3.3; §6 AMEND-1; §11 |
| The specification-confined invocation writes the 12 `spec/` carriers while its agent's `HARD CONSTRAINT` names one file, and its dry run names files the sub-step's Target list does not | Accepted for the commit scope, open for the confirmation. The sub-step commit agent stages whatever changed under `spec/`, and the confinement keeps the run inside that scope by excluding the carriers outside `spec/`. The apply agent's own confirmation that the dry run "touches only files this sub-step targets" is satisfied by AMEND-1 item 5, which states the expected file list in the Change paragraph the agent reads | Decision 4; §6 AMEND-1; §11 |
| SPEC-1's mechanical edit is assigned to `spec/28_communication-channels.md`, which the name pass writes nothing to | The mechanical diff for that file is empty, which the workflow's verify prompt calls a failure. AMEND-1 item 5 states in SPEC-1's Change paragraph that the diff for the assigned file is empty by construction, because §28 reproduces no reserved phrase, and that the run's evidence is the dry-run report together with `git diff -- spec/`. The sentence the verifier applies sits in the workflow, which §7 leaves unchanged, so the residue is a reviewer decision | Decision 4; §6 AMEND-1; §11 |
| SPEC-3's line pass is assigned to `spec/04_system-components.md` and SPEC-4's to `spec/15_external-api-surface.md`, and the line pass writes neither file | The mechanical diff for each assigned file is empty for the same reason SPEC-1's is, because neither file holds an entry in `tests/registers/line-citations.yaml`, whose only `spec/` entries are `spec/17_deployment-topology.md` and `spec/25_agent-operability.md` (lines 4990 through 4993), and a file with no citation is returned unchanged (`scripts/specshift/line/line.go` lines 118 through 120). AMEND-1 item 5 states the empty diff and its measurement in both Change paragraphs. SPEC-3's anchor invocation is in the same position, because no reference in `spec/04_system-components.md` names an anchor the reduction retires: §4.7 keeps its anchor and every same-page link targets a §4.x anchor the reduction keeps, its six file-qualified links into `spec/15_external-api-surface.md` target `#151-rest-api` (lines 194, 1100, 1102, and 1389) and `#1543-runtime-integration-levels` (lines 796 and 967), which both survive, its remaining file-qualified links name target files the reduction retires no anchor in, and it carries no bare `§15.4` citation. SPEC-4's anchor invocation is in the same position for a different reason: SPEC-3's earlier `-only spec/` invocation of the same pass, driven by the same whole move map, has already rewritten every `spec/` reference into a retired anchor, including the `#internal-messagepart-format` link at `spec/15_external-api-surface.md` line 1399, so SPEC-4's run finds no site, plans zero files, and leaves an empty diff for its assigned file. AMEND-1 item 5 states that measurement as well, so five of the six mechanical edits carry the sentence | Decision 4; §6 AMEND-1; §11 |
| The declaring section is authored but not yet staged when the pass runs | The passes' lister unions the index with the unignored untracked paths, so the section is a member of the domain and of the declared-identifier index. The gates keep the index-only lister and their read domain is unchanged | Decision 15; §8 case 13 |
| Between the two runs, `spec/` carries the canonical spelling while code, docs, charts, and pinned literals carry the retired one | Accepted. Tier 11 is red on every rewritten pinned literal until the remainder run lands in the code phase of the same change. The concrete case is `spec/04_system-components.md` line 489 pinned at `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 69 | §3.3; decision 11 |
| Between the two runs, a link outside `spec/` points at an anchor `spec/` has retired | Accepted and unobserved. The fragment-link gate that would report it is landed by 0064 in the code phase, after the window, and 0064 arranges for it to land green (0064 lines 3119 and 4136 through 4160), so no gate is red on the anchor class during the window. The line-citation half of the same split is observed, by the citation resolver per the SPEC-3 row above and by SPEC-4's own exit criterion per the row below | §3.3; decision 11; §11 |
| 0064's SPEC-1 names tier 11 as its own exit criterion, and the split leaves that criterion red at the end of SPEC-1 | Unresolved by this proposal. AMEND-1 is scoped to how a mechanical sub-step is invoked and stages no change to a sub-step's exit criteria. §11 decision 3 states the decision the reviewer takes | §11 |
| 0064's SPEC-3 names the citation resolver against its baseline, together with tier 11, as its own exit criterion (0064 lines 2552 through 2554 and 2646), and the split leaves that criterion red at the end of SPEC-3 | Unresolved by this proposal. The `-only spec/` line-pass run reaches the `spec/` carriers alone, and the only `spec/` carriers holding line citations are `spec/17_deployment-topology.md` and `spec/25_agent-operability.md` (`tests/registers/line-citations.yaml` lines 4990 through 4993), so every citation from a carrier outside `spec/` into the ranges SPEC-3's reduction shifts is converted only by the code-phase `-except spec/` run. The resolver reports each of those as a failure the baseline does not carry (`scripts/specshift/gate/resolution.go` lines 177 through 181), and the specification-phase verifier is required to confirm that the gate a mechanical sub-step names as its exit criterion is green (`.claude/workflows/implement-proposal.js` line 375). No `spec/` edit can convert a citation held by a carrier outside `spec/`, so the fix rounds cannot converge on it either. §11 decision 3 puts the same three options to the reviewer that it puts for SPEC-1's and SPEC-4's criteria | §11 |
| 0064's SPEC-4 names "every per-file count in the line-citation register is zero" as its own exit criterion (0064 line 2876), and the split leaves that criterion red at the end of SPEC-4 | Unresolved by this proposal. The `-only spec/` run reaches the `spec/` carriers alone, so every non-`spec/` count stays above zero (`tests/registers/line-citations.yaml` lines 16 through 19 among about 5,400 lines of entries) until the code-phase `-except spec/` run lands, and the specification-phase verifier is required to confirm that the gate a mechanical sub-step names as its exit criterion is green (`.claude/workflows/implement-proposal.js` line 375). No `spec/` edit can drive a non-`spec/` count to zero, so the fix rounds cannot converge on it either. §11 decision 3 puts the same three options to the reviewer that it puts for SPEC-1's criterion | §11 |
| A pinned-literal entry carries an in-range but wrong index | Does not fail closed. The literal is left unwritten and tier 11 reports it after the fact. TEST-1's tier-0 assertion over the committed register is what catches it beforehand | §4.3 |
| A tier-11 carrier gains or loses a string literal between this proposal landing and 0064's apply | Every index at or after the change shifts, so the register is re-derived rather than hand-patched | §4.2; §6 SUPPLY-1 |
| A seeded register is emptied by 0064 as it proceeds | Accepted and required. SPEC-1 empties the reserved-phrase register and SPEC-4 empties the anchor map and the anchor sense register, so no committed test asserts that a seeded register is non-empty | §6 TEST-1 |
| `tests/registers/pinned-spec-literals.yaml` is emptied | It is not. No proposal names it, `loadPinned` requires it whenever a tier-11 Go carrier exists, and `loadPinnedLiterals` rejects an empty `entries` list. It is standing input to the name pass, and §8 case 8 is written to hold before, during, and after the migration | §4.3; §8 case 8 |
| A seeded register carries a reserved phrase or a retired spelling in a YAML comment | It is reported as a residual of the other class, because `classRegisters` excludes a register from its own class's scan alone | Decision 14 |
| A new tracked register path has no glob key in `tests/change-graph.json` | No key is needed. The completeness check's domain is the `.go` and `.sh` extensions, so a YAML or JSON register is outside it (`cmd/lenny-test/cmd_validate.go` lines 395 through 401), and `tests/registers` already carries a glob key selecting the static and unit tiers | §6 SUPPLY-3 |

## 6. Proposed changes

### SUPPLY-1. Seed the reserved-phrase sense register and the pinned-literal register that drive the name pass

**Target:** `tests/registers/reserved-phrase-senses.yaml`, new;
`tests/registers/pinned-spec-literals.yaml`, new; `tests/registers/README.md`.

**Rationale:** The name pass is driven per occurrence and fails closed at a site the register does not carry
(`scripts/specshift/name/name.go` lines 7 through 15 and 191 through 206). Both registers are absent, and
the pinned-literal register is required whenever the tree carries a Go carrier under `tests/tier11_docs/`,
which it does with 42 files (`scripts/specshift/name/pinned.go` lines 30, 59 through 61, and 126 through
145). Neither is seeded by 0065, and 0064 can stage them only as files under `tests/`, which its
specification phase cannot write. Without both, SPEC-1's mechanical edit aborts before it rewrites a single
site.

**Change (staged description).** Write `tests/registers/reserved-phrase-senses.yaml` in the schema the
loader requires (`scripts/specshift/name/register.go` lines 19 through 39 and 73 through 84), with one
entry per reserved-phrase site the name pass finds under the whole domain 0064's N3 states, each naming the
canonical identifiers the site denotes and, where it denotes more than one, the replacement text those
identifiers sit in:

```yaml
# SPDX-License-Identifier: MIT
#
# Reserved-phrase sense register.
#
# One entry per reserved-phrase site, keyed by file and by the 1-based
# position of the site among the reserved-phrase sites that file carries
# in source order. The name pass reads it per occurrence and fails closed
# on a site with no entry. Proposal 0064 SPEC-1 empties it once the
# rewrite is complete.
kind: reserved-phrase-senses
version: 1
entries:
  - file: spec/04_system-components.md
    occurrence: 1
    identifiers:
      - <canonical identifier>
  - file: pkg/adapter/controlchannel.go
    occurrence: 1
    identifiers:
      - <canonical identifier>
      - <canonical identifier>
    replacement: <the text written at the site>
```

No entry is keyed to `spec/28_communication-channels.md`. Its §28.1 describes the two banned spellings
rather than reproducing them (0064 N3), so the file carries no reserved-phrase site, and an entry keyed to
it would be reported by `unclaimedReason` as `the file carries 0 reserved-phrase site(s)` and would fail
every run (`scripts/specshift/name/name.go` lines 302 through 328). The 12 `spec/` carriers §1 measures are
the whole of this register's `spec/` slice.

The seeding rule is one entry per site, so the register carries an entry for each of the three sentences
0064 SPEC-1 corrects by hand rather than by substitution, which are the interrupt sentences at
`spec/07_session-lifecycle.md` line 324 and `spec/15_external-api-surface.md` line 1755 and the
slot-failure sentence at `spec/05_runtime-registry-and-pool-model.md` line 540 (0064 lines 1283 through
1286). Omitting them is not available: the pass fails closed on a site the register does not carry, so a
run that reaches an omitted site aborts. Carrying them puts the run and the three sibling agents that hold
those files in an ordering AMEND-1 does not establish, and §11 decision 8 records it.

Write `tests/registers/pinned-spec-literals.yaml` in the schema `scripts/specshift/name/pinned.go` lines 29
through 50 requires. Each entry carries `file` and `literal`, where `literal` is the 1-based position of
the pinning literal among every string literal the carrier holds in source order, as `go/parser` enumerates
them (`scripts/specshift/name/pinned.go` lines 41 through 43, `scripts/specshift/name/phrase.go` lines 332
through 338), counting the literals that pin nothing alongside those that do. The index is therefore
derived from the file's whole literal sequence and shifts whenever a tier-11 carrier gains or loses any
string literal, so the register is re-derived rather than hand-maintained if a carrier changes between this
proposal landing and 0064's apply.

```yaml
# SPDX-License-Identifier: MIT
#
# Pinned specification literals.
#
# One entry per Go string literal under tests/tier11_docs/ that pins
# specification prose, a heading slug, or an intra-spec link. `literal`
# is the 1-based position of that literal among every string literal the
# file carries in source order, counting the literals that pin nothing.
kind: pinned-spec-literals
version: 1
entries:
  - file: tests/tier11_docs/eviction_coordinator_route_consistency_test.go
    literal: <position among the file's string literals>
```

The pinned register is the one artifact of this proposal whose mis-seeding does not fail closed.
`checkPinnedClaimed` reports an entry only when its index exceeds the file's literal count
(`scripts/specshift/name/pinned.go` lines 179 through 183), so an in-range but wrong index leaves the
pinning literal outside the site set `findSites` builds and outside the set `standing` re-checks
(`scripts/specshift/name/name.go` lines 164, 176, and 359 through 360), the run exits zero with the literal
unwritten, and tier 11 goes red only after 0064 has committed. The seeding is therefore derived
mechanically, by a scratch script that parses each tier-11 carrier, finds the literals carrying a reserved
phrase, and emits their positions, rather than counted by hand. TEST-1 extends the assertion over this
register beyond schema: for every committed entry, the literal at the named position in the current tree
carries a reserved phrase as `scripts/specshift/name/phrase.go`'s matcher defines it or a canonical
identifier as `scripts/specshift/name/declare.go`'s identifier pattern defines it. The assertion ranges
over the register's entries alone. Completeness of the register against its defined population, one entry
per literal that pins specification prose, a heading slug, or an intra-spec link, is resolved by the
reviewer at seeding time and is not asserted, because a tier-11 carrier also holds diagnostic messages that
carry a reserved phrase and pin nothing
(`tests/tier11_docs/budget_extension_trigger_consistency_test.go` line 183,
`tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 150). That check is a tier-0
assertion over the committed tree and
needs no §28 text, so it closes the gap that §11's open decision on pre-landing verification leaves open
for this register. The disjunction is what makes it hold after the migration as well, per §4.3: this
register is standing input to the name pass and is emptied by nothing.

Add a row per register to `tests/registers/README.md` under `## Baselines and sense maps`, naming the pass
that reads it, the schema, and the change that empties it. The pinned-literal register's row states that
nothing empties it and that the name pass requires it whenever the tree carries a Go carrier under
`tests/tier11_docs/`.

Per decision 14, no YAML comment in either file carries a specimen of a reserved phrase or of a retired
channel spelling.

### SUPPLY-2. Seed the specification-slice entries of the identifier sense register

**Target:** `tests/registers/identifier-senses.yaml`, new; `tests/registers/README.md`.

**Rationale:** The identifier pass resolves each occurrence of a retired spelling from a register keyed per
occurrence rather than per channel, because one retired spelling denotes two channels and a single-channel
spelling still occurs where the text is not a channel
(`scripts/specshift/identifier/senses.go` lines 15 through 62). Both channels carry the token
`LifecycleChannel` inside package `adapter` (`pkg/adapter/lifecyclechannel.go` line 92,
`pkg/adapter/controlchannel.go` line 90), and a not-a-channel occurrence sits at
`spec/17_deployment-topology.md` line 1530. The register is absent, the pass fails closed before any
file is read when it is missing (`scripts/specshift/main.go` lines 164 through 179,
`scripts/specshift/identifier/senses.go` lines 99 through 118), `scope.classRegisters` already reserves the
path without supplying it (`scripts/specshift/scope/scope.go` lines 181 through 183), and SPEC-2's
mechanical edit runs in the specification phase, which cannot create it.

**Change (staged description).** Seed `tests/registers/identifier-senses.yaml` with the entries for the
occurrences under `spec/` alone, which is 17 sites across `spec/04_system-components.md`,
`spec/15_external-api-surface.md`, `spec/17_deployment-topology.md`, and `spec/18_build-sequence.md`,
carrying 8, 5, 3, and 1 occurrence of a retired spelling respectively. The figures are counted per
occurrence rather than per line, because the register is keyed per occurrence and several carrier lines
hold two spellings (`spec/04_system-components.md` lines 739 and 791, `spec/15_external-api-surface.md`
line 2305). The seeding includes the not-a-channel
entry for `spec/17_deployment-topology.md` line 1530, in the schema the loader requires:

```yaml
# SPDX-License-Identifier: MIT
#
# Identifier sense register.
#
# One entry per occurrence of a retired channel spelling whose sense the
# naming table alone cannot decide, keyed by file and by the 1-based
# position of the site among the retired-spelling sites that file carries
# in source order. This file carries the spec/ occurrences; proposal 0064
# SPEC-2 appends the remaining occurrences and runs the pass over the
# rest of the tree.
kind: identifier-senses
version: 1
entries:
  - file: spec/04_system-components.md
    occurrence: 1
    channel: <canonical identifier>
  - file: spec/17_deployment-topology.md
    occurrence: 1
    not-a-channel: true
```

No entry carrying `path: true` belongs in this proposal, because every file-name carrier
(`pkg/adapter/lifecyclechannel.go`, `pkg/adapter/controlchannel.go`) is outside `spec/`. That is safe only
because TOOL-1 item 4 filters the identifier pass's rename planning by the confinement. Unfiltered, that
planning walks the whole tracked tree before the confined walk begins and aborts on a file-name carrier
with no entry, so a `-only spec/` run would abort on both of these files before writing anything. SPEC-2
appends their entries with the rest of the remainder, in the phase whose run covers them.

The remainder entries stay with 0064, which already assigns that seeding to SPEC-2 and implements SPEC-2's
non-specification targets in the code phase. That is also the first point at which §28 exists, so the
remainder entries are validated mechanically by an `-except spec/` dry run to zero aborts rather than by a
reviewer procedure in a scratch worktree. Seeding both halves here would multiply the reviewer's per-site
sign-off, measured at 4 files and 17 occurrences under `spec/` against 104 files and 617 occurrences
tree-wide, both sides counted per occurrence, and would seed the half no committed check can validate today, because the pass reads its
retired spellings from §28.3 (`scripts/specshift/identifier/table.go` lines 19 through 21).

Add the `tests/registers/README.md` row for this register, naming which half of it is outstanding and which
run empties it.

### SUPPLY-3. Seed the anchor-move map and the anchor sense register

**Target:** `tests/spec-anchor-moves.json`, new; `tests/registers/anchor-senses.yaml`, new;
`tests/registers/README.md`.

**Rationale:** The anchor pass reads two artifacts: an anchor-keyed move map supplied on the command line,
which resolves the link class, and a per-occurrence sense register read from the fixed path
`tests/registers/anchor-senses.yaml`, which answers what a bare section citation of a retired anchor means,
because a reduction carves material out of the anchor it moves
(`scripts/specshift/anchor/moves.go` lines 13 through 46, `scripts/specshift/anchor/senses.go` lines 16
through 42, `scripts/specshift/anchor/anchor.go` lines 275 through 286). Both are absent. The sense register
is read inside `Rewrite`, so its absence aborts the run partway through the walk with the tree
byte-identical rather than before the walk begins.

**Change (staged description).** Write `tests/spec-anchor-moves.json` with one entry per retired anchor,
carrying `anchor` and a `successor` of `{file, anchor}`, seeded from the reductions 0064 stages in SPEC-3
and including the retirement of `1541-adapterbinary-protocol`:

```json
{
  "kind": "spec-anchor-moves",
  "version": 1,
  "moves": [
    {
      "anchor": "1541-adapterbinary-protocol",
      "successor": {
        "file": "spec/<file the reduction creates>.md",
        "anchor": "<heading slug the reduction creates>"
      }
    }
  ]
}
```

Every successor must name a heading a document of the tree declares, which the pass checks before it
rewrites anything (`scripts/specshift/anchor/anchor.go` lines 255 through 272), so the map is seeded to name
the headings 0064's reductions create and is verified in a scratch worktree carrying 0064's staged text.

Write `tests/registers/anchor-senses.yaml` with `file`, `occurrence` (1-based over the retired-section
citations the file carries in source order, counting prose and comment citations alone, per
`scripts/specshift/anchor/anchor.go` lines 186 through 193), and `destination` in the `<path>#<anchor>`
spelling.

Its population is every occurrence of a bare `§X.Y`-form citation of a retired §15.4 anchor, in every
carrier the anchor pass reads, which is the class 0064 §3.4 defines and assigns to the pass rather than to
a hand correction. Each occurrence takes one of the three destinations 0064's §15.4 reduction passage
defines: the §28 channel-contract card, the surviving `#translation-fidelity-matrix` heading, and the
surviving `#messageenvelope--unified-message-format` heading, the latter two in
`spec/15_external-api-surface.md`. 0064 states that the population is too large to enumerate in a proposal
and is selectable only by which block each citation means, and it names members at
`pkg/gateway/session/sessioninbox/events.go` line 45, `sdks/runtime/go/runtime/types.go` lines 52 and 224,
`sdks/runtime/python/lenny_runtime/types.py` lines 145 and 391, and
`sdks/runtime/typescript/src/types.ts` lines 59 and 211.

The membership is therefore enumerated mechanically rather than by hand, in the same scratch worktree the
anchor map is verified in: with the map seeded and the sense register carrying the members 0064 names,
because `loadSenses` rejects a register with no entry (`scripts/specshift/anchor/senses.go` lines 82
through 87), a dry run of the anchor pass aborts once per unresolved bare citation, naming the carrier, the
line, and the occurrence (`scripts/specshift/anchor/anchor.go` lines 195 through 198), and the run is
repeated until it reports none. What each enumerated occurrence means is a per-site judgement, which §11 decision 1 records
alongside the identifier and reserved-phrase registers.

```yaml
# SPDX-License-Identifier: MIT
#
# Anchor sense register.
#
# One entry per bare section citation of a retired anchor whose material
# the reduction carves out, so the map's single successor would send it
# to the wrong heading. Keyed by file and by the 1-based position of the
# citation among the retired-section citations that file carries in
# source order. Proposal 0064 SPEC-4 empties it with the map.
kind: anchor-senses
version: 1
entries:
  - file: pkg/gateway/session/sessioninbox/events.go
    occurrence: 1
    destination: <one of the three destinations named above>
```

The illustration uses `pkg/gateway/session/sessioninbox/events.go`, whose comment at line 45 carries a bare
`§15.4.1` citation of the `message_expired` payload, because a carrier of a bare citation is what this
register keys. The markdown-link members 0064 hand-corrects under its target-and-label rule are not entries
here; they are hand edits inside 0064's own sub-steps, which is why `spec/07_session-lifecycle.md`, whose
only members 0064 names are seven such links, carries no entry on that account. Leaving them out of the
sense register does not leave them out of the pass: the anchor pass's link class is driven by
`tests/spec-anchor-moves.json` alone, so a markdown link into a retired anchor is rewritten to that map's
single successor with no register consulted (`scripts/specshift/anchor/site.go` lines 129 through 146,
`scripts/specshift/anchor/anchor.go` lines 186 through 190). A confined run that reads one of those links
before its hand correction lands therefore rewrites it to the successor rather than to the heading 0064
names, and §11 decision 7 records that ordering.

`tests/spec-anchor-moves.json` is a new tracked root-level path under `tests/`, and it needs no glob key in
`tests/change-graph.json`: the change-graph completeness check's domain is the `.go` and `.sh` extensions,
so a JSON or YAML register is outside it (`cmd/lenny-test/cmd_validate.go` lines 395 through 401), which is
why the existing `tests/spec-map.json` carries neither a glob key nor a coverage-baseline prefix today.

Add the `tests/registers/README.md` rows for both artifacts.

### TOOL-1. Give `scripts/specshift` a required write confinement, confine the walk to it, and list the working tree

**Target:** `scripts/specshift/main.go`, `scripts/specshift/pass/pass.go`, `scripts/specshift/scope/scope.go`,
`scripts/specshift/name/name.go`, `scripts/specshift/name/pinned.go`,
`scripts/specshift/identifier/identifier.go`, `scripts/specshift/line/line.go`,
`scripts/specshift/anchor/anchor.go`.

**Rationale:** The write domain is computed once over the whole tracked tree and the harness walks all of
it, so a pass invoked in the specification-apply phase writes files outside that phase's commit scope, and
the flag surface offers no way to narrow the run
(`scripts/specshift/main.go` lines 26 through 33 and 103 through 107,
`scripts/specshift/pass/pass.go` line 296, `scripts/specshift/scope/scope.go` lines 817 through 837).
Seeding the registers does not change that. A confinement whose two values partition the domain at `spec/`
lets one pass, one register, and one fail-closed contract run in two phases. Separately, the lister the
passes are built with reads the index alone (`scripts/specshift/scope/scope.go` lines 660 through 675,
`scripts/specshift/main.go` lines 81 through 84, `scripts/specshift/pass/pass.go` line 275), so the §28
file SPEC-1 authors is invisible to SPEC-1's own mechanical invocation until the post-verification commit,
which aborts the run on the declared-identifier index per decision 15.

**Change (staged description).**

1. **Flags.** Add two repeatable string flags to `parseArgs`
   (`scripts/specshift/main.go` lines 100 through 115), through a `stringList` `flag.Value` implementation
   in the same file:

   ```go
   var only, except stringList
   fs.Var(&only, "only", "confine the run to a tracked path or directory prefix (repeatable)")
   fs.Var(&except, "except", "exclude a tracked path or directory prefix from the run (repeatable)")
   ```

   Extend the usage block (`scripts/specshift/main.go` lines 26 through 33) with both flags and with the
   sentence that a run of a pass requires at least one of them. Fail a pass run that carries neither, in
   the manner of the existing register check at lines 164 through 166, and place the check beside it, after
   the `-domain` early return at lines 161 through 163 and before `LoadRegister`:

   ```go
   if !opts.domain && len(opts.only) == 0 && len(opts.except) == 0 {
       return fmt.Errorf("-only or -except is required to run the %s pass: a run that is not confined writes every file of the pass's write domain", opts.pass)
   }
   ```

   `-domain` keeps both optional and prints the whole write domain when neither is given.

2. **Predicate.** Build one predicate from the two flag sets, matching a tracked path when it equals a value
   or sits under it as a slash-terminated directory prefix, and carry it on `Harness` as a nil-able field
   whose nil value covers everything. A value is matched by segment rather than by substring, so
   `-only spec` covers `spec/04_system-components.md` and covers no sibling path that begins with the same
   characters, in the manner of the segment rule `scope.Readable` already uses for the fixture exclusion
   (pinned at `scripts/specshift/run_test.go` lines 148 through 156). `scope.WriteDomain`,
   `scope.KeyWriteDomain`, `scope.Writable`, `NewHarness`, and `NewHarnessOver` keep their signatures.

3. **Walk.** In `Plan` (`scripts/specshift/pass/pass.go` lines 291 through 300), filter the domain
   `scope.WriteDomain` returns through the predicate before the walk, and keep the zero-file guard over the
   filtered result with the confinement named in the message. Skip the key-rewrite channel when the
   confinement covers no path-keyed register, and keep `KeyWriteDomain`'s emptiness guard for a confinement
   that covers one.

   Apply the same filter in `printWriteDomain` (`scripts/specshift/main.go` lines 228 through 239), which
   `runWith` reaches before the rewriter, the register, and `Plan` are touched (lines 161 through 163). Left
   unfiltered, `-domain` prints the whole write domain under every confinement, the partition §8 case 4
   measures compares the full domain with itself, and the pre-apply measurement decision 5 states does not
   exist. Name the confinement in the count line the function already prints.

4. **Claim checks and rename planning.** Pass the predicate to the rewriter through an optional interface the harness applies
   before the walk, and skip in `checkClaimed` every entry whose file the confinement does not cover
   (`scripts/specshift/name/name.go` line 277, `scripts/specshift/identifier/identifier.go` line 590). The
   same filter applies to `checkPinnedClaimed` (`scripts/specshift/name/pinned.go` lines 160 through 185).
   Without it, the code-phase run aborts on every entry the specification phase already applied.

   The filter's predicate is the confinement rather than the set of files the run planned sites for. An
   implementation keyed on the planned set would also skip an entry whose file the confinement covers and
   whose sites a prior run over the same confinement already consumed, which turns a repeated run into a
   silent no-op instead of the abort §5 relies on. §8 cases 3 and 10 pin both directions for `checkClaimed`,
   and §8 case 14 pins both directions for `checkPinnedClaimed`, whose abort conditions are an entry keyed
   to a file the tree does not carry and an index above its carrier's literal count
   (`scripts/specshift/name/pinned.go` lines 164 through 183). Neither condition is produced by a prior
   rewrite, so cases 3 and 10 never reach that check and cannot pin its filter.

   Apply the same predicate to the identifier pass's file-name rename planning. `planRenames`
   (`scripts/specshift/identifier/identifier.go` lines 463 through 495) iterates the whole tracked tree
   rather than the confined domain, because it runs inside `prepare` before the walk (lines 235 through
   245), and `moveOf` aborts on a file whose name carries a retired spelling and whose occurrence-0 entry
   the register does not hold (lines 502 through 514). Left unfiltered, a `-only spec/` run aborts on
   `pkg/adapter/lifecyclechannel.go` and `pkg/adapter/controlchannel.go`, which SUPPLY-2 deliberately does
   not seed and which the confinement excludes. Skip a path the confinement does not cover, so the
   complementary `-except spec/` run performs the move and takes the abort for an unregistered file-name
   carrier. `recordSymbols` is reached from the same loop and records symbols for `.go` carriers alone
   (lines 542 through 575), which the key-rewrite channel consumes, and that channel is skipped under a
   confinement covering no path-keyed register per decision 9, so the skip removes nothing the confined
   run uses. §8 case 15 pins both directions.

   The declared-identifier check in `checkRegister` (`scripts/specshift/name/name.go` lines 239 through
   260) is left unfiltered, per §4.1.

5. **Report.** Extend the run report (`scripts/specshift/main.go` line 194) with the confinement it ran
   under, the number of files planned, the count and the distinct files of the register entries deferred as
   outside the confinement, and the standing sentence that the naming lint, the identifier-resolution gate,
   the fragment-link gate, the per-class residual scans, and tier 11 stay red until every deferred entry is
   covered by a complementary run. The report covers all four passes, through the deferred set each pass
   reports to the report writer. For the name and the identifier passes that set is the set `checkClaimed`
   skipped. The line and the anchor passes carry no claimed-entry check
   (`checkClaimed` exists in `scripts/specshift/name/name.go` line 277 and
   `scripts/specshift/identifier/identifier.go` line 590 alone), so each derives its deferred set from the
   driving register it already holds in full before the walk, which is the per-file count map for the line
   pass (`scripts/specshift/line/line.go` lines 271 through 283) and the sense register the anchor pass
   loads once per run in `checkRegisters` (`scripts/specshift/anchor/anchor.go` lines 240 through 266): an
   entry whose file the confinement does not cover is deferred. For those two passes the report is the only
   signal of an uncovered entry, because neither an entry outside the confinement nor a replayed run over an
   already-rewritten tree aborts. §8 case 11 asserts the report's content over the run's output for the name
   pass, and §8 case 17 asserts it for the line and the anchor passes together with the empty-diff outcome
   of a replayed run.

6. **Lister.** Add `scope.WorkingTreeLister` beside `scope.GitLister`
   (`scripts/specshift/scope/scope.go` lines 660 through 675), returning the union of `git ls-files -z`
   and `git ls-files -z --others --exclude-standard`, deduplicated and sorted, with a doc comment stating
   that membership is the index plus the unignored working tree because a pass runs inside the apply phase
   that authored the file it must read and nothing stages that file until the phase's commit agent. Use it
   in the four pass constructions (`scripts/specshift/main.go` lines 81 through 84) and in `NewHarness`
   (`scripts/specshift/pass/pass.go` line 275), which are the pass side's only construction points outside
   test files.
   Extend `GitLister`'s existing doc comment to name the gates as its consumers and to name
   `WorkingTreeLister` as the passes'. `GitLister` itself is unchanged, so the line-citation ratchet, the
   citation-resolution gate, the residual gate, and the skip-reason classifier keep the read domain they
   carry today, per decision 15.

7. **Existing test.** `TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite`
   (`scripts/specshift/run_test.go` line 1075) drives a successful run with `-root`, `-pass`, and
   `-register` and no confinement, and asserts at `t.Fatalf` that the run succeeds. Item 1 makes that
   invocation a usage error, so add `-except spec/` to its argument list.

   The sweep criterion is every caller whose assertion depends on the run reaching a check that now sits
   after the confinement check, rather than callers that expect a successful run alone. Item 1 places the
   confinement check before `LoadRegister` (`scripts/specshift/main.go` lines 164 through 179), so a
   caller that supplies a register and no confinement in order to pin a register rejection would return
   the confinement usage error instead, keep both of its assertions, and stop exercising the loader. The
   three sub-cases inside the same test function, over a residual register, a missing register, and a
   malformed register (`scripts/specshift/run_test.go` lines 1102 through 1121), are that class, as is
   `TestRunRequiresARegister` (line 1666), whose run carries no register and no confinement. Add
   `-except spec/` to each of the four, so every one keeps failing for the reason it pins.

The ordering constraints are stated in §3.2: a sub-step issues one confined invocation per pass per phase, and the
specification-phase invocation runs after the authored edit that declares the identifier space, which is
established by attaching it to the agent that owns the declaring file.

### TEST-1. Pin the confinement, the confined claim check, and the pinned-literal register's claim on the tree

**Target:** `scripts/specshift/run_test.go`.

**Rationale:** The confinement changes what a pass writes, which is the property the whole migration's
completeness argument rests on, and the confined claim check relaxes a check that exists to catch an
off-by-one enumeration. Both need cases that fail if the behavior regresses. The pinned-literal register
needs a case because its mis-seeding is the one failure here that does not fail closed.

**Change (staged description).** Every case lands in `scripts/specshift/run_test.go`. The other
`scripts/specshift` packages carry no test file today, every case for them lives in `run_test.go` with its
shared fixture helpers (`repoRoot`, `treeDomain`, `builtPasses`, and the `testdata/` fixture trees), and the
flag-usage case requires `package main`, which `run_test.go` is.

The cases are listed in §8. The same sub-step also updates the one committed case the required confinement
would turn red, `TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite`
(`scripts/specshift/run_test.go` line 1075), per TOOL-1 item 7.

No case asserts that a seeded register is non-empty. 0064 empties the reserved-phrase register at the end of
SPEC-1 and the anchor map and the anchor sense register in SPEC-4, and `tests/change-graph.json` selects the
specshift unit tier on any edit under `tests/registers`, so a committed non-emptiness assertion would turn
tier 0 and tier 1 red mid-application of 0064. Schema, kind, version, and per-entry validity are already
enforced by the loaders on every run (`scripts/specshift/name/register.go` lines 96 through 114 and the
three siblings) and by the committed schema-defect cases, so no case re-asserts them. The one content
assertion this proposal adds is the pinned-literal claim on the tree. That register is emptied by nothing,
per §4.3, and the claim's predicate is a disjunction that holds before, during, and after the migration, so
the case is stable across 0064's application.

### AMEND-1. State each mechanical sub-step's confined command lines and its one `spec/` target file in proposal 0064

**Target:** `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, in the
Change paragraphs of SPEC-1, SPEC-2, SPEC-3, and SPEC-4.

**Rationale:** Decision 2 states it. The plan agent composes a mechanical edit's command from the proposal
text (`.claude/workflows/implement-proposal.js` line 202), 0064 names no confinement and states that the
pass "walks the whole domain N3 states in one run" (0064 line 1162), and an unconfined invocation is a
usage error under TOOL-1 item 1, which the apply agent records as unappliable
(`.claude/workflows/implement-proposal.js` lines 312 and 337 through 346). The apply phase also spawns one
agent per distinct target file of a sub-step, in parallel, each running the command its mechanical entry
names (lines 289 through 296 and 305 through 312), so a mechanical edit carrying several `spec/` targets
becomes several concurrent runs of the identical command. The second and third of those abort in
`checkClaimed` on the entries the first consumed
(`scripts/specshift/name/name.go` lines 294 through 297 and 324 through 325), because their entries lie
inside the confinement and the filter TOOL-1 item 4 stages does not skip them.

SPEC-3 is amended for the same reasons, and it stages two passes rather than one. Its Change paragraphs
stage a run of the `specshift` line pass over
the shifted tree as one atomic sub-step with the reduction (0064 lines 2531, 2550 through 2552, and 2575
through 2576) and a run of the anchor pass over the markdown links into the §15.4 and §4.7 subsections the
reduction retires (0064 lines 2594 through 2597), so both are mechanical edits under the plan agent's
classification, and it names twelve `spec/`
paths in its Target list (0064 lines 1722 through 1752). Left unamended, each invocation carries no
confinement and returns the usage error TOOL-1 item 1 stages, and its twelve targets spawn twelve
concurrent runs of each identical command. Confining the anchor invocation is required on the merits as
well: 0064 assigns the anchor pass the markdown cross-reference redirect class in `docs/` as well as in
`spec/` (0064 line 281), so an unconfined run in the specification phase would write outside that phase's
commit scope.

**Change (staged description).** In each of SPEC-1, SPEC-2, SPEC-3, and SPEC-4, state five things about the
sub-step's mechanical edit, without changing which sites the sub-step rewrites:

1. The mechanical edit's `spec/` target file is one file, named, and every other `spec/` path in the
   sub-step's Target list is an authored edit. The named file is `spec/28_communication-channels.md` for
   SPEC-1, whose §28.3 naming table the name pass indexes; `spec/15_external-api-surface.md` for SPEC-2,
   which is already the only `spec/` path that sub-step targets, so the sentence records the assignment
   rather than narrowing it, and §28 exists by then because SPEC-1 committed it;
   `spec/04_system-components.md` for SPEC-3, which carries the larger citation population below the
   shifted ranges the line pass reads (0064 lines 2531 through 2533) and the subsection headings the same
   sub-step inserts ahead of that pass, and which issues SPEC-3's anchor invocation as well; and
   `spec/15_external-api-surface.md` for SPEC-4, the carrier of the retired §15.4 anchors.
2. The invocations are the last edits the named file's agent applies, and each pass the sub-step stages
   runs once for the sub-step. Where a sub-step stages two passes, the Change paragraph states their order.
   In SPEC-3 the reduction runs first, the subsection headings are inserted over the prose that survives it,
   the line pass runs over the shifted tree (0064 lines 2574 through 2576), and the anchor pass runs last
   among that agent's edits. Running it last among `spec/04_system-components.md`'s own edits does not order
   it after the headings it requires. `checkRegisters` holds every move successor and every sense
   destination to a heading a document of the tree declares and returns a hard error before any file is
   rewritten (`scripts/specshift/anchor/anchor.go` lines 240 through 272), and those headings are the §28.5
   channel-contract cards SPEC-3 authors in `spec/28_communication-channels.md` (0064 lines 2668 through
   2669 and 2164 through 2166), which is a distinct target file of the same sub-step and therefore an agent
   the pipeline spawns in parallel with `spec/04`'s (`.claude/workflows/implement-proposal.js` lines 295
   through 297). §11 decision 7 records that dependency as an abort the reviewer decides on. In SPEC-4 the
   anchor pass runs before the line pass, which is the order its own Change paragraph states (0064 lines
   2769 and 2859 through 2861).
3. The command lines, verbatim, in the dry-run form and the apply form the pipeline's mechanical branch
   runs in that order, together with the complementary code-phase form. For SPEC-1:

   ```
   go run ./scripts/specshift -pass name -register tests/registers/reserved-phrase-senses.yaml -only spec/
   go run ./scripts/specshift -pass name -register tests/registers/reserved-phrase-senses.yaml -only spec/ -apply
   ```

   with `-except spec/` in place of `-only spec/` for the code-phase run. SPEC-2 states the same pair with
   `-pass identifier -register tests/registers/identifier-senses.yaml`. SPEC-3 and SPEC-4 each state one
   pair per pass, because each stages two passes: SPEC-3 with
   `-pass line -register tests/registers/line-citations.yaml` and
   `-pass anchor -register tests/spec-anchor-moves.json`, in the order item 2 states, and SPEC-4
   with `-pass anchor -register tests/spec-anchor-moves.json` and
   `-pass line -register tests/registers/line-citations.yaml`, in the order item 2 states.
4. The one-run sentence the same Change paragraph carries is replaced by the two-run form, so the
   paragraph the plan agent composes the command from does not state the contrary of the command lines
   item 3 adds. In SPEC-1, the clause `walks the whole domain N3 states in one run` (0064 line 1162)
   becomes `walks the whole domain N3 states across the two confined runs stated above`, with the rest of
   that sentence, including its generated-file exclusion clause, unchanged. SPEC-4 carries one such
   sentence per pass, and both are replaced. `Run the anchor
   pass tree-wide` (0064 line 2769) becomes `Run the anchor pass over the two confined runs stated above`,
   and `Run the line pass over every carrier of the citation form to convert line citations to anchor
   citations, driving every per-file count in the line-citation register to zero` (0064 lines 2859 through
   2861) becomes `Run the line pass over every carrier of the citation form across the two confined runs
   stated above, to convert line citations to anchor citations, driving every per-file count in the
   line-citation register to zero across the pair`.
   SPEC-2 carries no such sentence: its Change paragraph states the rename `across
   every machine-readable surface, by script` (0064 line 1369), which names the surfaces the rename covers
   rather than the number of runs, so it is left as it is. In SPEC-3, `in every carrier` (0064 line 2551)
   becomes `in every carrier across the two confined runs stated above`, and `then run the line pass over
   the shifted tree` (0064 lines 2575 through 2576) becomes `then run the line pass over the shifted tree
   in the two confined runs stated above`, with the atomicity sentence at 0064 line 2531 left as it is,
   because the split of that atomicity is a reviewer decision §11 records rather than a sentence this
   amendment resolves. SPEC-3's anchor sentence, `The same change runs the anchor pass over the markdown
   links into the retired §15.4 and §4.7 subsections` (0064 lines 2594 through 2595), is left as it is for
   the reason SPEC-2's rename sentence is: it names the sites the pass covers rather than the number of
   runs, so it states no contrary of the command lines item 3 adds.
5. What the run writes and what the assigned file's diff carries, so the apply agent's two mechanical
   confirmations resolve against the sub-step's own text. The Change paragraph states that the `-only spec/`
   run rewrites every `spec/` carrier of the sub-step's class rather than the assigned file alone, that the
   dry run's file list therefore names `spec/` files the Target list does not, and that this file list is
   the expected output rather than a run that has exceeded its scope. Where the assigned file carries no
   site of the pass's class, the Change paragraph states additionally that the mechanical diff for that file
   is empty by construction, names the measurement that makes it so, and states that the run's evidence is
   the dry-run report together with `git diff -- spec/`. Five of the six mechanical edits are in that
   position. SPEC-1's assigned `spec/28_communication-channels.md` carries no reserved-phrase site, because
   §28 describes the banned spellings rather than reproducing them. SPEC-3's line pass writes nothing to its
   assigned `spec/04_system-components.md`, and SPEC-4's line pass writes nothing to its assigned
   `spec/15_external-api-surface.md`, because neither file holds an entry in
   `tests/registers/line-citations.yaml`, whose only `spec/` entries are `spec/17_deployment-topology.md`
   and `spec/25_agent-operability.md` (`tests/registers/line-citations.yaml` lines 4990 through 4993), and a
   file the register does not carry an entry for is one the line pass finds no citation in and returns
   unchanged (`scripts/specshift/line/line.go` lines 118 through 120). SPEC-3's anchor pass writes nothing
   to the same assigned `spec/04_system-components.md` either, and its Change paragraph takes the sentence a
   fourth time, on the measurement that no reference in that file, in any class the anchor pass reads, names
   an anchor `tests/spec-anchor-moves.json` retires. Every same-page link in the file targets a §4.x anchor
   the reduction keeps, including the twelve `](#47-runtime-adapter)` links, because §4.7 keeps its heading
   and its anchor and gains no `tests/spec-anchor-moves.json` entry (0064 lines 2268 through 2269 and 2285
   through 2286). The file carries 225 file-qualified links, of which six enter
   `spec/15_external-api-surface.md`: four target `#151-rest-api`, at lines 194, 1100, 1102, and 1389, and
   two target `#1543-runtime-integration-levels`, at lines 796 and 967, which 0064 confirms survives (0064
   lines 2801 through 2803). The retired anchors are `1541-adapterbinary-protocol` together with the
   unnumbered headings inside the §15.4.1 block (0064 lines 2663 through 2669), all of which sit in
   `spec/15_external-api-surface.md`, so the remaining file-qualified links name target files in which the
   SPEC-3 reduction retires no anchor. The file carries no bare `§15.4` citation either, so it holds no
   member of the class the anchor sense register keys. SPEC-4's anchor pass takes the sentence a fifth
   time, on a different measurement: its `-only spec/` run finds no site anywhere under `spec/`. The anchor
   pass has no per-sub-step scoping. `findSites` makes every markdown link whose fragment is an anchor of
   the move map a site and rewrites it to that map's successor, with no register and no anchor subset
   consulted (`scripts/specshift/anchor/site.go` lines 116 through 146), and SUPPLY-3 seeds
   `tests/spec-anchor-moves.json` once with one entry per retired anchor, including
   `internal-messagepart-format` (0064 lines 2663 through 2669), because decision 3 rejects a
   per-confinement register pair. SPEC-3's `-only spec/` anchor invocation therefore walks the same 28
   `spec/` files under the same whole map and rewrites the `#internal-messagepart-format` link at
   `spec/15_external-api-surface.md` line 1399 that 0064 assigns to SPEC-4's pass (0064 lines 2805 through
   2808), so by the time SPEC-4's invocation runs no reference under `spec/` names a retired anchor. Every
   file is returned unchanged, the run plans zero files and exits zero
   (`scripts/specshift/anchor/anchor.go` lines 141 through 143), and the diff for the assigned
   `spec/15_external-api-surface.md` is empty by construction. The Change paragraph states that
   measurement and states that the run's evidence is the dry-run report's zero-file count. §11 decision 7
   records the case in which SPEC-3's invocation aborts instead. The two sentences the apply agent
   and the verifier apply, that a dry run must touch "only files this sub-step targets" and that "A
   mechanical edit whose diff is empty is a failure"
   (`.claude/workflows/implement-proposal.js` lines 312 and 375), sit in the workflow, which §7 leaves
   unchanged, so this item states the facts those agents read the proposal for and §11 decision 6 records
   what the reviewer decides about the residue.

The amendment adds no edit site, changes no register, changes no sub-step's exit criterion, and changes
neither proposal's Status bullet. §11 decision 3 records the three exit criteria the split leaves red,
which are SPEC-1's tier 11, SPEC-3's citation resolver against its baseline, and SPEC-4's line-citation
register at zero. It lands with this proposal, which lands before 0064 is applied.

## 7. Non-goals

- **Any specification change.** This proposal touches no file under `spec/`. A specification edit would put
  its own content in the phase whose constraint it exists to work around.
- **No edit to proposal 0065 in any form, and no edit to proposal 0064 beyond AMEND-1.** Neither Status
  bullet changes, and no sub-step's target list of changed files or exit criterion changes. The sentences
  AMEND-1 rewrites are the ones stating how a mechanical sub-step is invoked and what its run writes,
  which includes the one-run sentences item 4 replaces with the two-run form and the file-list and
  diff sentences item 5 adds, and which sites each sub-step rewrites is unchanged.
  Decision 2 shows that this reaches the apply agent through no other artifact.
- **Running the migration.** No pass is run over the tree here and no derived artifact is regenerated. This
  proposal supplies what 0064 runs; 0064 runs it.
- **Seeding `tests/claim-map.json`.** It is read by a tier-0 validator in the code phase rather than by any
  pass during the apply, so its absence blocks nothing here.
- **Changing the fail-closed contract.** A site the register does not carry still aborts the run with the
  tree byte-identical, inside a confinement as well as over the whole domain.
- **Changing `scope.Writable`, `scope.ReservedPhraseCarrier`, the read exclusion list, or the
  generated-artifact rule.** The confinement is a filter over the domain those predicates define rather
  than a redefinition of it. `scope.GitLister` is left as it is and a second constructor is added for the
  passes, per decision 15, because the set of files the tool can see is a separate question from which of
  them a pass may write, and every exclusion those predicates apply still applies to a working-tree member.
- **Changing the read domain of the committed gates.** The line-citation ratchet, the citation-resolution
  gate, the residual gate, and the skip-reason classifier keep `scope.GitLister` and keep the index as
  their membership, so no gate gains an untracked member and no gate's remedy set changes.
- **A second register per class, or a per-confinement register pair.** Decision 3 states the reason.
- **Changing the implement-proposal workflow or skill.**
- **Excluding the anchor-move map from the anchor class's residual scan.** Considered and dropped. The
  exclusion mechanism already exists and already covers driving registers per class (`classRegisters`,
  `scripts/specshift/scope/scope.go` lines 175 through 208, consumed by `ReadableForClass` at lines 378
  through 397), while no residual scan for any class is implemented anywhere in `scripts/`, `cmd/`, or
  `pkg/`: `classRegisters` has one consumer, `ClassReadDomain`, whose one production caller is the anchor
  pass's markdown heading index, and that skips every non-`.md` path
  (`scripts/specshift/anchor/heading.go` lines 69 and 76 through 78). The premise that every map entry
  would be reported as a residual is also unverifiable and probably false: the only operational statement
  of the anchor population either approved proposal makes is the markdown link form and the bare `§X.Y`
  section citation, and a JSON string value is neither. The exclusion would also land in the same file, the
  same phase, and the same commit as the residual predicate whose author would see the failure
  immediately, so staging it here commits the tree to an exclusion whose justification may be vacuous,
  which is the unexplained narrowing the residual machinery exists to prevent.
- **A separate documentation change stating the two-run protocol.** Considered and dropped. AMEND-1 states
  the protocol where the agent that runs it reads it, which is 0064's own mechanical sub-steps. The `-only` and
  `-except` flags belong in the usage block `scripts/specshift/main.go` lines 26 through 33 already
  carries, which TOOL-1 updates; the run report TOOL-1 adds is the only channel that reaches an operator at
  the moment it matters; and SUPPLY-1 through SUPPLY-3 already add the per-register
  `tests/registers/README.md` rows a reader arriving at a half-emptied register looks for. The premise that
  a package comment reaches the specification-phase verifier is false: the verifier reads the proposal
  subsection, the target file, and `git diff` (`.claude/workflows/implement-proposal.js` lines 371 through
  373), and `verifyFilePrompt` is handed no apply-agent output (lines 360 through 382). Writing a
  one-time, 0064-specific protocol permanently into a general-purpose tool's package comment would also
  leave a paragraph describing a state that cannot recur.

## 8. Testing

Every case lands in `scripts/specshift/run_test.go`, carries the `// spec: §28.1` annotation the
surrounding cases use, and carries no `// diagnosis:` comment: a change under `scripts/specshift` or
`tests/registers` selects the static and unit tiers alone
(`cmd/lenny-test/cmd_validate_test.go` lines 1256 through 1289), and no specshift test carries one. The
tiers this change reaches are tier 0 and tier 1, per `.claude/rules/test-coverage.md`.

1. **A run of a pass with neither `-only` nor `-except` fails with the usage error and writes nothing**
   (tier 1), in the form of `TestRunRequiresARegister` (`scripts/specshift/run_test.go` line 1666). This is
   the boundary that makes the confinement required rather than defaulted.
2. **`-only` over a fixture tree whose sites span `spec/` and code writes only the named path and leaves
   every other file byte-identical** (tier 1). The assertion is over the applied tree rather than the diff,
   so a pass that planned correctly and wrote widely still fails.
3. **A second confined run over the tree the first already rewrote completes without an unclaimed-entry
   abort** (tier 1), for the name and the identifier passes. This is the case that fails today and the one
   the confined claim check exists for.
4. **The confined domains of a partition of the tree reconstruct the unconfined write domain** (tier 1):
   the union of the domain `-domain -only spec/` prints and the domain `-domain -except spec/` prints
   equals the write domain `-domain` prints with no confinement, and their intersection is empty, asserted
   over the fixture tree. `-domain` is the one surface that admits an unconfined measurement, which is why
   the comparison is written there rather than over two runs. The case fails unless `printWriteDomain`
   applies the predicate, per TOOL-1 item 3, and it is the assertion of the covering obligation decision 8
   states.
5. **A confinement matching no file fails with the zero-file guard, and the message names the confinement**
   (tier 1), mirroring `scripts/specshift/run_test.go` line 136. This is the empty case.
6. **`-only spec/` plans no key rewrite, and `-except spec/` plans the key rewrite of every
   path-keyed register** (tier 1). The first half pins that the channel is skipped rather than failing its
   emptiness guard; the second pins that the guard still holds where a register is covered.
7. **A site the register does not carry aborts the run it is confined to, with the tree byte-identical,
   while a site in the complementary confinement does not abort that run** (tier 1). This is the
   spec-named failure the confinement weakens, so it carries its own case; the whole-tree fail-closed cases
   at `scripts/specshift/run_test.go` lines 4242, 5641, and 6427 are not duplicated per confinement.
8. **Every committed entry of `tests/registers/pinned-spec-literals.yaml` names a position within its
   carrier's string-literal sequence whose literal carries a reserved phrase or a canonical identifier**
   (tier 0), asserted over the committed tree with `name.FindReservedPhrases`
   (`scripts/specshift/name/phrase.go` line 59) and the identifier spelling
   `scripts/specshift/name/declare.go` defines. The case asserts this per-entry predicate alone and does not
   assert that every literal carrying a reserved phrase is registered. A tier-11 carrier also holds
   diagnostic messages that carry a reserved phrase and pin nothing
   (`tests/tier11_docs/budget_extension_trigger_consistency_test.go` line 183,
   `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 150), so such an assertion would
   be red on the committed tree until those two were registered, and registering them would make them sites
   `findSites` admits (`scripts/specshift/name/phrase.go` lines 101 through 124) and the name pass rewrites,
   which is what naming the literals rather than walking them exists to prevent
   (`scripts/specshift/name/pinned.go` lines 24 through 28). The disjunction is what makes the case hold before
   the migration, across the window between the two runs, and after the code-phase run has rewritten every
   pinned literal, which matters because nothing empties this register and `loadPinnedLiterals` rejects an
   empty `entries` list (`scripts/specshift/name/pinned.go` lines 83 through 85). This is the error path
   `checkPinnedClaimed` does not cover, per §4.3.
9. **A confined run whose tree carries no `spec/28*` file, and one whose `spec/28*` file declares no
   identifier, both fail before any file is written and name the declaring file** (tier 1), with the tree
   byte-identical after each. This pins the ordering constraint §3.2 states. Both sub-cases abort inside
   `declaredIdentifiers` before any per-entry comparison runs (`scripts/specshift/name/declare.go` lines 96
   through 101, `scripts/specshift/name/name.go` lines 239 through 242), so neither distinguishes a
   filtered per-entry loop from an unfiltered one, which case 16 does.
10. **A run confined to a path a prior run already applied over aborts with the unclaimed-entry error
    naming that path, and leaves the tree byte-identical** (tier 1), for the name and the identifier
    passes. This is the fail direction of the filter case 3 pins the pass direction of. An implementation
    that filtered `checkClaimed` by the files the run planned sites for rather than by the files the
    confinement covers satisfies case 3 and turns this run into a silent no-op, which is the failure §5
    records as loud.
11. **A confined name-pass run whose register spans both sides of the confinement names, in its output, the
    confinement it ran under, the number of files planned, and the file of every deferred entry, together
    with the standing red-gate sentence, and its deferred and checked entry counts sum to the register's
    entry count** (tier 1), asserted over the run's output in the manner of
    `scripts/specshift/run_test.go` lines 1093 through 1094. The report is the whole compensating signal for
    the relaxed claimed-entry check, so a regression that drops or under-counts the deferred list would
    otherwise leave every confined run reporting a clean pass over a register it did not consume. Case 3 is
    extended to assert the deferred count it produces.
12. **The confinement matches by path segment rather than by substring** (tier 1): over a fixture tree,
    `-only <dir>` covers every tracked path under `<dir>/` and covers neither a sibling `<dir>-notes/`
    path nor a `<file>.bak` beside a named file, with the same near-miss asserted for `-except`. This is the
    defect class `TestReadableKeepsAPathNamedLikeAFixtureDirectory` (`scripts/specshift/run_test.go` lines
    148 through 156) already pins on the read side, and it decides whether a specification-phase invocation
    writes outside its confinement while every whole-path case still passes.
13. **A pass reads a file that is present in the working tree, unignored, and not staged** (tier 1), over a
    fixture tree carrying such a file, and does not read a file the ignore rules exclude. This pins
    decision 15's lister change, which is what lets SPEC-1's mechanical invocation see the §28 file its own
    sub-step has just authored.
14. **The pinned-literal claim check aborts under a confinement that covers its carrier and skips the same
    entry under one that does not** (tier 1), over the `namepass` fixture tree with a pinned entry whose
    index exceeds its carrier's literal count, and again with an entry keyed to a file the tree does not
    carry. A run confined so the carrier is covered aborts naming the entry and leaves the tree
    byte-identical; a run confined away from it does not abort on that entry. `checkPinnedClaimed`'s two
    abort conditions are produced by no prior rewrite (`scripts/specshift/name/pinned.go` lines 164
    through 183), so cases 3 and 10 never reach it, and the committed cases that pin its unfiltered
    behavior drive the pass through a harness built with no confinement
    (`scripts/specshift/run_test.go` lines 4208 through 4212, 5407, and 5448), which an implementation
    skipping every pinned entry unconditionally would also satisfy. This register's mis-seeding is the one
    failure §5 records as not failing closed, and the out-of-range guard is the compensating check the
    filter narrows.
15. **A `-only spec/` identifier run over a tree whose file-name carriers lie outside the confinement
    completes, while the complementary `-except spec/` run still aborts on an unregistered file-name
    carrier** (tier 1), over a fixture tree carrying a file whose name holds a retired spelling and whose
    register holds no file-name entry. This pins the confinement filter on `planRenames`, which runs over
    the whole tracked tree before the walk (`scripts/specshift/identifier/identifier.go` lines 235 through
    245 and 463 through 514), and it is the case that fails if the filter is applied to `checkClaimed`
    alone.
16. **A `-only spec/` name run aborts on a register entry outside the confinement that names an identifier
    the specification declares nowhere** (tier 1), over a fixture tree whose `spec/28*` file declares the
    canonical identifiers the `spec/` entries use and whose register carries one further entry, keyed to a
    `pkg/` carrier, naming an identifier that file declares nowhere. The run aborts naming that entry with
    the tree byte-identical, and a run whose confinement covers the `pkg/` carrier aborts identically. This
    is the case that separates the unfiltered per-entry loop §4.1 requires from an implementation that
    applies the confinement predicate across the whole of `checkRegister`, which the two sub-cases of case 9
    both pass because they abort before that loop is reached. Without it, a filtered implementation would
    let the specification phase commit `spec/` and defer the abort to the code phase.
17. **A confined line-pass run and a confined anchor-pass run each name their deferred entries, and a
    replayed run of either over the tree it already rewrote exits zero with an empty diff** (tier 1), over a
    fixture tree whose sites and register entries span both sides of the confinement. The first half asserts
    that each run's output names the confinement, the planned file count, the file of every register entry
    outside the confinement, and the standing red-gate sentence, which for these two passes is the only
    signal that an entry is uncovered. The second half asserts the outcome §5 records: neither pass carries
    a claimed-entry check, so the replayed run reports zero files and returns success
    (`scripts/specshift/line/line.go` lines 118 through 120,
    `scripts/specshift/anchor/anchor.go` lines 141 through 143,
    `scripts/specshift/main.go` lines 194 and 202) rather than taking the abort case 10 pins for the name
    and the identifier passes. Without this case the deferred report for these two passes could be dropped
    and every listed case would still pass.

## 9. Findings closed on application

None. This proposal supplies data and one tool option for a migration and closes no `BUILD-GAPS.md`
finding on its own.

## 10. Resolved in adversarial review

Review rounds populate this section. The draft records the two changes dropped before the first round in
§7, with the reason each was dropped.

### Pass 1 (2026-08-01, automated)

- **The two-run protocol covered none of the `spec/` files that carry reserved-phrase sites.** The apply
  fan-out is one agent per distinct `targetFile` of the sub-step's Target list
  (`.claude/workflows/implement-proposal.js` lines 289 through 296), so 0064 SPEC-1's confinements would
  have been its three named `spec/` paths, none of which matches a reserved phrase, while the 12 carriers
  are `spec/04`, `05`, `06`, `07`, `10`, `13`, `15`, `16`, `17`, `18_build-sequence`, `24`, and `26`. §1
  now states that measurement as its own blocker, decision 4 replaces the per-file form with one
  `-only spec/` invocation issued by the agent that owns the sub-step's declaring file, decision 8 states
  the covering obligation as a partition rather than asserting coverage, and §3.2 and §8 case 4 are
  rewritten to match.
- **The confined run could not see `spec/28_communication-channels.md`.** The file is created by SPEC-1 and
  staged by nothing until the post-verification commit agent, while `scope.GitLister` lists the index alone
  (`scripts/specshift/scope/scope.go` lines 660 through 675), so `declaredIdentifiers` aborted every
  mechanical invocation of SPEC-1. Decision 15 and TOOL-1 item 6 widened the lister the passes are built
  with to the index plus the unignored untracked paths, superseded in Pass 2 by the separate
  `scope.WorkingTreeLister` constructor that leaves `GitLister` and its consumers as they are. §8 case 13
  pins the behavior, and SUPPLY-1's seeded register illustration now uses
  `spec/04_system-components.md`, because §28 describes the banned spellings rather than reproducing them
  and so carries no reserved-phrase site.
- **Nothing ordered the mechanical run after the authored §28 edit.** The declared-identifier index is read
  before any site is enumerated and is not filtered by the confinement. §3.2 and §5 now state the ordering
  constraint, decision 4 establishes it by attaching the invocation to the declaring file's agent, §4.1
  states why the declared-identifier check stays unfiltered, and §8 case 9 pins the failure.
- **`-domain` never reached `Plan`, so the confinement filter did not apply to it.** `runWith` returns from
  `printWriteDomain` before `Plan` (`scripts/specshift/main.go` lines 161 through 163 and 228 through 239).
  TOOL-1 item 3 and §12's `main.go` bullet now stage the filter in `printWriteDomain`, and §4.1 and §8
  case 4 state that the partition is measured there.
- **The required confinement would have turned a committed test red.**
  `TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite` (`scripts/specshift/run_test.go` line 1075) drives
  a successful unconfined run. TOOL-1 item 6 is followed by item 7, which adds `-except spec/` to that
  invocation and sweeps the file's other callers, and §12 records the update.
- **The missing-register error was cited at `scripts/specshift/main.go` line 184**, which is inside the
  apply and plan dispatch. Every citation is now lines 164 through 166, matching SUPPLY-2, and decision 2,
  §4.1, and TOOL-1 item 1 state that the confinement check sits beside it.
- **§8 case 8 had no route back to green after the migration.** Nothing empties
  `tests/registers/pinned-spec-literals.yaml`, `loadPinnedLiterals` rejects an empty `entries` list, and the
  code-phase run rewrites every pinned literal. §4.3, §5, SUPPLY-1, TEST-1, §8 case 8, and §12 now state
  that the register is standing input to the name pass, and the case's predicate is a disjunction over the
  reserved phrase and the canonical identifier that holds before, during, and after the migration.
- **No case pinned the deferred-entry run report**, which is the whole compensating signal for the relaxed
  claimed-entry check. §8 case 11 asserts the confinement, the planned file count, every deferred entry's
  file, the standing red-gate sentence, and that deferred plus checked entries sum to the register's entry
  count, and TOOL-1 item 5 names it.
- **SUPPLY-2 named `spec/18_extensibility-model.md`, which does not exist.** The file is
  `spec/18_build-sequence.md`. SUPPLY-2 and §11 decision 1 are corrected.
- **No case pinned that two overlapping confined runs abort.** §8 case 10 asserts that a run confined to a
  path a prior run already applied over aborts with the unclaimed-entry error and leaves the tree
  byte-identical, and TOOL-1 item 4 states that the filter's predicate is the confinement rather than the
  set of files the run planned sites for.
- **No case pinned the confinement's path-match rule at the prefix boundary.** §4.1 and TOOL-1 item 2 state
  the segment-terminated rule, and §8 case 12 asserts the near misses for `-only` and `-except`, in the
  manner of `TestReadableKeepsAPathNamedLikeAFixtureDirectory`.
- **SUPPLY-2's re-derived per-file figures were line counts against a per-occurrence register.** Three
  carrier lines hold two retired spellings each (`spec/04_system-components.md` lines 739 and 791,
  `spec/15_external-api-surface.md` line 2305), so the `spec/` slice carries 8, 5, 3, and 1 occurrence
  rather than 5, 4, 3, and 1, and 17 occurrences rather than 13. The tree-wide comparison also mixed units,
  because its 612 figure was measured per occurrence. SUPPLY-2 now counts per occurrence on both sides,
  gives the tree-wide figure as the measured 617 occurrences over 104 files, and names the lines that carry
  two spellings; §11 decision 1 matches.
- **§8 case 8's completeness conjunct required entries SUPPLY-1's own register definition excludes.** Two
  tier-11 diagnostic messages carry a reserved phrase and pin nothing
  (`tests/tier11_docs/budget_extension_trigger_consistency_test.go` line 183,
  `tests/tier11_docs/eviction_coordinator_route_consistency_test.go` line 150), so the conjunct was red on
  the committed tree until those two were registered, and registering them would make them sites the name
  pass rewrites, against the register's stated reason for naming literals rather than walking them. §4.3,
  SUPPLY-1, and §8 case 8 now assert the per-entry predicate alone and record completeness against the
  register's defined population, one entry per literal that pins specification prose, a heading slug, or an
  intra-spec link, as a reviewer property resolved at seeding time.

### Pass 2 (2026-08-01, automated)

- **Decision 4's single issuing agent was asserted rather than staged, and the fan-out §1 measures
  contradicted it.** The apply phase spawns one agent per distinct target file of a sub-step, in parallel,
  and each runs the command its mechanical entry names
  (`.claude/workflows/implement-proposal.js` lines 289 through 296 and 305 through 312), so SPEC-1's three
  `spec/` targets would have produced three concurrent identical `-only spec/` runs, the second and third
  aborting in `checkClaimed` (`scripts/specshift/name/name.go` lines 294 through 297 and 324 through 325).
  AMEND-1 now stages the sentences that give each mechanical edit one `spec/` target file, and decision 2,
  decision 4, §3.2, §5, §7, and §12 state the amendment and its assignments, including SPEC-4's, which
  decision 4 previously omitted.
- **Nothing conveyed the `-only spec/` value to 0064's mechanical invocation.** 0064 mentions no
  confinement and states that the pass "walks the whole domain N3 states in one run" (0064 line 1162),
  while TOOL-1 item 1 makes an unconfined run a usage error the apply agent records as unappliable. The
  `-register` value is derivable from 0064's Target list and the confinement is derivable from nothing
  either agent reads, so decision 2 is reversed for that one property and AMEND-1 states each sub-step's
  command lines verbatim, in the dry-run, apply, and code-phase forms.
- **TOOL-1's `GitLister` change silently widened the read domain of four committed gates.** The
  line-citation ratchet, the citation-resolution gate, the residual gate, and the skip-reason classifier
  build themselves from `scope.GitLister`, and every remedy they name is index-keyed, so an unignored
  untracked file would fail the ratchet with `Absent: true` and no route to green
  (`scripts/specshift/gate/ratchet.go` lines 124, 154, and 190 through 193). Decision 15 and TOOL-1 item 6
  now add `scope.WorkingTreeLister` for the passes and leave `GitLister` and its consumers as they are,
  with §3.1, §4.3, §5, §7, and §12 matching.
- **TOOL-1 item 7's sweep criterion left three register-rejection assertions passing on the confinement
  error.** The three sub-cases at `scripts/specshift/run_test.go` lines 1102 through 1121 supply a
  register and no confinement to pin a residual, a missing, and a malformed register, and the confinement
  check sits before `LoadRegister`, so each would have returned the usage error with both assertions
  intact. Item 7's criterion is now every caller whose assertion depends on reaching a check that sits
  after the confinement check, and it names those three and `TestRunRequiresARegister`.
- **A `-only spec/` identifier run aborted in the unfiltered rename planning.** `planRenames` walks the
  whole tracked tree inside `prepare`, before the confined walk, and `moveOf` aborts on a file-name carrier
  the register does not record (`scripts/specshift/identifier/identifier.go` lines 235 through 245 and 463
  through 514), so the run would have aborted on `pkg/adapter/lifecyclechannel.go` and
  `pkg/adapter/controlchannel.go`, which SUPPLY-2 declines to seed. TOOL-1 item 4 now applies the
  confinement to the rename planning, §4.1 and §5 row 2 state which site classes are filtered with the
  walk and which are read outside it, SUPPLY-2 states why its omission is safe, and §8 case 15 pins both
  directions.
- **SUPPLY-3 sourced the anchor sense register from an enumeration 0064 does not make.** 0064 enumerates
  the markdown-link members SUPPLY-3 excludes and states that the bare-citation members cannot be
  enumerated. SUPPLY-3 now states the population as every occurrence of a bare `§X.Y` citation of a
  retired §15.4 anchor, names the three destinations, derives the membership from the pass's own aborts in
  the scratch worktree, and illustrates with `pkg/gateway/session/sessioninbox/events.go` rather than with
  the excluded `spec/07_session-lifecycle.md` links. §4.3 states that the same worktree run checks the
  destinations, and §11 decision 1 covers the per-site judgement.
- **The confinement filter on `checkPinnedClaimed` was staged with no case.** Its abort conditions are
  produced by no prior rewrite, so §8 cases 3 and 10 never reach it, and the committed cases drive the
  pass through an unconfined harness. §8 case 14 asserts both directions over the `namepass` fixture tree,
  and TOOL-1 item 4 names it.
- **Two statements still asserted that this proposal edits no proposal.** §5's row on SPEC-1's exit
  criterion and §11 decision 3 both rested on that predicate after AMEND-1 was staged. Both now state that
  AMEND-1 is scoped to how a mechanical sub-step is invoked and stages no change to a sub-step's exit
  criteria, and decision 3's alternatives name extending AMEND-1 rather than amending 0064 separately.
- **AMEND-1 added the confined command lines beside the sentence they supersede.** SPEC-1's Change
  paragraph would have carried both the two invocations and `walks the whole domain N3 states in one run`
  (0064 line 1162), which is the paragraph the plan agent composes the mechanical entry's command from
  (`.claude/workflows/implement-proposal.js` line 202). AMEND-1 item 4 now stages the replacement of that
  clause and of SPEC-4's `Run the anchor pass tree-wide` (0064 line 2769) with the two-run form, records
  that SPEC-2 carries no such sentence, and §7's non-goal is narrowed to permit those sentence-level
  changes while keeping every sub-step's target list, exit criterion, and rewritten sites fixed.
- **§10 Pass 1's lister bullet recorded the widened `GitLister` that Pass 2 reversed.** It stated in the
  present tense that decision 15 and TOOL-1 item 6 widen `GitLister`, against decision 15, TOOL-1 item 6,
  §7, §12, and the Pass 2 bullet above, which add `scope.WorkingTreeLister` for the passes instead. The
  bullet now records the Pass 1 resolution as superseded in Pass 2.

### Pass 3 (2026-08-01, automated)

- **The apply agent's mechanical branch would have recorded the confined run as unappliable.** Before
  applying, it confirms that the command's dry run "touches only files this sub-step targets" and that "the
  applied diff for this file matches what the dry run predicted"
  (`.claude/workflows/implement-proposal.js` line 312), and a `-only spec/` run writes the class's `spec/`
  carriers, none of which SPEC-1's or SPEC-2's Target list names. §1 now states the confirmation as its own
  blocker, decision 4 enumerates it alongside the `HARD CONSTRAINT`, AMEND-1 item 5 stages the sentences
  that state the expected file list in the Change paragraph both agents read, §5 records the row, and §11
  decision 6 records the residue, because the confirming sentences sit in the workflow §7 leaves unchanged.
- **SPEC-1's mechanical edit was assigned to a file the name pass writes nothing to.**
  `spec/28_communication-channels.md` carries no reserved-phrase site, so the mechanical diff for the
  assigned file is empty, which the verifier treats as a failure
  (`.claude/workflows/implement-proposal.js` line 375), and §1 had already rejected the per-file
  confinement on that same ground. AMEND-1 item 5 now states in SPEC-1's Change paragraph that the diff for
  the assigned file is empty by construction and that the run's evidence is the dry-run report together
  with `git diff -- spec/`. §5 records the row and §11 decision 6 puts the alternative, reassigning the
  edit to a carrier the pass writes, in front of the reviewer.
- **SPEC-3 also runs a specshift pass and was amended by nothing.** Its Change paragraphs stage a run of the
  line pass over the shifted tree as one atomic sub-step with the reduction (0064 lines 2531, 2550 through
  2552, and 2575 through 2576), and it names twelve `spec/` paths, so TOOL-1 item 1's required confinement
  would have made it a usage error and its fan-out would have spawned twelve concurrent identical runs.
  AMEND-1's Target and its five items now cover SPEC-3, assigning its mechanical edit to
  `spec/04_system-components.md` and stating its `-only spec/` and `-except spec/` command lines, §3.4 names
  the pass, §5 gains the two rows for the split of its atomicity and for the ordering the assignment does
  not establish, §11 decision 7 states the reviewer's choice, and §12 records the amendment.
- **No case pinned that the declared-identifier check stays unfiltered.** Both sub-cases of §8 case 9 abort
  inside `declaredIdentifiers` before the per-entry loop is reached
  (`scripts/specshift/name/declare.go` lines 96 through 101,
  `scripts/specshift/name/name.go` lines 239 through 242), so an implementation that filtered the whole of
  `checkRegister` by the confinement passed every listed case while deferring the abort §4.1 names to the
  code phase. §8 case 16 now asserts that a `-only spec/` run aborts on an out-of-confinement entry naming
  an undeclared identifier, case 9 is narrowed to the ordering pin, and §4.1 separates the index's two hard
  errors from the per-entry loop and cites the case that pins each.
- **The fragment-link gate cannot be red during the two-run window.** It does not exist in the tree, 0064
  lands it as a Go test under `tests/tier0_static/` in its code phase (0064 line 3119,
  `.claude/workflows/implement-proposal.js` line 202), and 0064 arranges for it to land green by
  hand-correcting the six pre-existing broken links in the same sub-step (0064 lines 4136 through 4160).
  Decision 11, §3.3, the §5 window rows, and §11 decision 2 now state that tier 11 and the citation
  resolver are what observe the window and that the anchor half of the split has no gate signal.
- **SPEC-3's assignment rationale was stated two ways and neither held.** Decision 4 called
  `spec/04_system-components.md` the larger of the two files whose shifted line numbers the pass reads, and
  AMEND-1 item 1, the sentence staged into 0064, called it the carrier of the larger of the two reductions.
  `spec/04_system-components.md` is 1780 lines against `spec/15_external-api-surface.md`'s 2736, and the
  `spec/15` §15.4 reduction retires roughly 600 lines against the roughly 40-line §4.7 block 0064 moves
  (0064 lines 2024 through 2027 and 2243 through 2245), so both predicates are false. The measurement that
  motivates the assignment is the citation population below the shifted ranges, 697 Go citations below
  `spec/04` §4.7 against 39 below `spec/15` §15.4 (0064 lines 2531 through 2533). Both sites now state that
  measurement in identical wording, alongside the heading insertion the same sub-step performs ahead of the
  pass.

### Pass 4 (2026-08-01, automated)

- **AMEND-1 staged no confined command line for SPEC-3's anchor pass.** SPEC-3's Change paragraph runs the
  anchor pass over the markdown links into the retired §15.4 and §4.7 subsections alongside its line pass
  (0064 lines 2594 through 2597), so the plan agent emits a second mechanical command with no confinement,
  which TOOL-1 item 1 makes a usage error and the apply agent records as unappliable
  (`.claude/workflows/implement-proposal.js` line 312). AMEND-1 item 3 now states one command pair per pass
  for SPEC-3, item 1 records that the same assigned `spec/04_system-components.md` issues both invocations,
  item 2 states the order of a sub-step's two invocations for SPEC-3 and SPEC-4, and §1, §3.4, decision 4,
  the §5 SPEC-3 rows, and §11 decision 7 state that SPEC-3 runs two passes.
- **SPEC-4's own exit criterion is unmeetable under the split, and only SPEC-1's was recorded.** SPEC-4
  states that "The sub-step's exit criteria are that every per-file count in the line-citation register is
  zero" (0064 line 2876), while the `-only spec/` run reaches the `spec/` carriers alone and no `spec/` edit
  can drive a non-`spec/` count down (`tests/registers/line-citations.yaml` lines 16 through 19). §5 gains
  the row, §11 decision 3 covers both criteria and puts the same three options to the reviewer, decision 11
  names tier 11 and the citation resolver as the gates that observe the window, and the §5 anchor-window row
  is narrowed to the anchor class.
- **SPEC-3's and SPEC-4's assigned `spec/` files carry no line citation, so their line-pass diffs are empty
  and AMEND-1 item 5 staged the covering sentence for SPEC-1 alone.** Neither
  `spec/04_system-components.md` nor `spec/15_external-api-surface.md` holds an entry in
  `tests/registers/line-citations.yaml`, whose only `spec/` entries are `spec/17_deployment-topology.md` and
  `spec/25_agent-operability.md` (lines 4990 through 4993), and a file with no citation is returned
  unchanged (`scripts/specshift/line/line.go` lines 118 through 120), which is the empty mechanical diff the
  verifier calls a failure (`.claude/workflows/implement-proposal.js` line 375). AMEND-1 item 5 now states
  the empty-diff sentence for every mechanical edit whose assigned file its pass writes nothing to, names
  the three that are in that position and the measurement for each, §5 gains the row, and §11 decision 6
  covers all three.
- **The deferred-entry report and the unclaimed-entry abort were stated as properties of any confined run,
  and only the name and identifier passes carry a claimed-entry check.** `checkClaimed` exists in
  `scripts/specshift/name/name.go` line 277 and `scripts/specshift/identifier/identifier.go` line 590 alone,
  so for the line and the anchor passes an entry outside the confinement is neither checked nor reported and
  a replayed run plans an empty diff and exits zero (`scripts/specshift/line/line.go` lines 118 through 120,
  `scripts/specshift/anchor/anchor.go` lines 141 through 143, `scripts/specshift/main.go` lines 194 and
  202). Decision 8, decision 13, §3.2, the §5 rows, and TOOL-1 item 5 now scope the abort to the two passes
  that carry the check and extend the deferred report to all four, deriving the line pass's deferred set
  from its per-file count map and the anchor pass's from its sense register, both of which each run already
  holds. §8 case 17 pins the report and the empty-diff outcome for those two passes, and TOOL-1's Target and
  §12 add the two files.
- **AMEND-1 item 4 left SPEC-4's line-pass one-run sentence beside the confined command lines it
  contradicts.** SPEC-4 carries a one-run sentence per pass, and item 4 replaced the anchor one alone. Item
  4 now also replaces `Run the line pass over every carrier of the citation form to convert line citations
  to anchor citations, driving every per-file count in the line-citation register to zero` (0064 lines 2859
  through 2861) with the two-run form, and it records that SPEC-3's anchor sentence names the sites the pass
  covers rather than a number of runs and is left as it is, for the reason SPEC-2's rename sentence is. §12
  records the sentence-level changes.
- **Corrections to this pass: SPEC-3's anchor-pass edit was excused from the empty-diff sentence on a false
  measurement.** The pass asserted that `spec/04_system-components.md` links into a retired
  `#47-runtime-adapter` anchor. §4.7 keeps its heading and its anchor and gains no
  `tests/spec-anchor-moves.json` entry (0064 lines 2268 through 2269 and 2285 through 2286), every one of
  the file's same-page links targets an anchor the reduction keeps, and no file-qualified link in it targets
  a retired anchor either, which pass 5 restates on the measured link population. SPEC-3's anchor pass
  therefore writes nothing to its assigned file, so
  AMEND-1 item 5 now takes the empty-diff sentence for that edit as well, and §5's row and §11 decision 6
  cover it. SPEC-4's anchor pass remains the one edit of the four that writes its assigned file, through the
  `#internal-messagepart-format` link at `spec/15_external-api-surface.md` line 1399 (0064 lines 2805
  through 2808).
- **Corrections to this pass: the denominator of mechanical edits was left at the pre-fix figure.** Making
  SPEC-3 a two-pass sub-step raises the total from five mechanical edits to six, because SPEC-1 and SPEC-2
  stage one pass each and SPEC-3 and SPEC-4 stage two each. AMEND-1 item 5 and §11 decision 6 now read four
  of the six rather than three of the five.
- **Corrections to this pass: three sites still stated one confined invocation per sub-step per phase.**
  TOOL-1's closing ordering sentence, §3.2's opening sentence, and decision 4's summary clause carried the
  superseded singular form against the per-pass rule the pass established at §3.2 and decision 4. All three
  now state one confined invocation per pass per phase, and §3.2 records that a two-pass sub-step becomes
  four invocations across the two phases.

### Pass 5 (2026-08-01, automated)

- **AMEND-1 item 2 justified SPEC-3's anchor ordering on an author the tree contradicts.** The item stated
  that the anchor pass runs last because "the reduction and the insertion are what create those headings",
  which named `spec/04_system-components.md`'s own edits as the source of the successor headings. The
  successors are the §28.5 channel-contract cards SPEC-3 authors in `spec/28_communication-channels.md`
  (0064 lines 2668 through 2669 and 2164 through 2166), a distinct target file of the same sub-step, which
  the pipeline applies through an agent it spawns in parallel with `spec/04`'s
  (`.claude/workflows/implement-proposal.js` lines 295 through 297), and `checkRegisters` returns a hard
  error before any file is rewritten when a successor or a sense destination names a heading no document of
  the tree declares (`scripts/specshift/anchor/anchor.go` lines 240 through 272). Item 2 now states the
  order without the false rationale and names the unestablished dependency, the §5 SPEC-3 ordering row
  separates the `spec/15` stale-line-number residue from the `spec/28` abort, and §11 decision 7 records
  the abort, the three options, and why reassigning the anchor edit to `spec/28_communication-channels.md`'s
  agent is not staged.
- **AMEND-1 item 5's measurement for SPEC-3's anchor edit was false.** The item stated that
  `spec/04_system-components.md` carries "only two file-qualified links". The file carries 225 of them, six
  of which enter `spec/15_external-api-surface.md`: four to `#151-rest-api` at lines 194, 1100, 1102, and
  1389, and two to `#1543-runtime-integration-levels` at lines 796 and 967. Item 5 now states the
  measurement that establishes the empty diff, which is that no reference in the file, in any class the
  anchor pass reads, names an anchor `tests/spec-anchor-moves.json` retires: the same-page links all target
  a §4.x anchor the reduction keeps, the two `spec/15` anchor targets survive (0064 lines 2801 through
  2803), the retired anchors all sit in `spec/15_external-api-surface.md` (0064 lines 2663 through 2669) so
  the remaining file-qualified links name target files with no retired anchor, and the file carries no bare
  `§15.4` citation. The §5 row and §11 decision 6 carry the same measurement, and §12's count of mechanical
  edits with an empty assigned-file diff is corrected from three to four.

### Pass 6 (2026-08-01, automated)

- **SPEC-3's confined anchor invocation was not ordered after the 14 markdown links 0064 hand-corrects, and
  the residue was unrecorded.** The §5 ordering row enumerated two residues. The anchor pass's link class is
  driven by `tests/spec-anchor-moves.json` rather than by the sense register, so a markdown link into a
  retired anchor is rewritten to the map's single successor with no register consulted
  (`scripts/specshift/anchor/site.go` lines 129 through 146, `scripts/specshift/anchor/anchor.go` lines 186
  through 190), and the seven `spec/07_session-lifecycle.md` links and the seven same-page
  `spec/15_external-api-surface.md` links SPEC-3 hand-corrects to the heading each cites (0064 lines 2784
  through 2789 and 2830 through 2833) are inside the `-only spec/` confinement and applied by parallel
  sibling agents. The failure is silent rather than an abort, because the map's successor heading exists.
  The §5 row now records three residues, §11 decision 7 records the third with the same three options
  decision 4 puts for SPEC-4 and with the whole-file write behavior of `Apply`
  (`scripts/specshift/pass/pass.go` lines 441 through 464), and SUPPLY-3 states that leaving those links
  out of the sense register does not leave them out of the pass.
- **SPEC-1's three hand-corrected reserved-phrase sentences collided with its own `-only spec/` run, and the
  collision was recorded for SPEC-3 and SPEC-4 alone.** The sentences at `spec/07_session-lifecycle.md`
  line 324, `spec/15_external-api-surface.md` line 1755, and `spec/05_runtime-registry-and-pool-model.md`
  line 540 are reserved-phrase sites in files the run rewrites and parallel agents apply (0064 lines 1283
  through 1286). SUPPLY-1 now states that the register carries an entry for each, because the pass fails
  closed on an omitted site, §5 carries a row of the form SPEC-4's takes, and §11 decision 8 records both
  terminal orderings, the deterministic `checkClaimed` failure at `spec/07_session-lifecycle.md`
  (`scripts/specshift/name/name.go` lines 243 and 277 through 328), the lost writes from `Apply`'s
  whole-file writes, and the three options.
- **SPEC-3's own exit criterion was left red and unmeetable and was counted nowhere.** 0064 SPEC-3 names the
  citation resolver reporting no failure the baseline does not already carry, together with tier 11, as its
  exit criterion (0064 lines 2552 through 2554 and 2646). The `-only spec/` run reaches the `spec/`
  carriers alone, whose only line-citation holders are `spec/17_deployment-topology.md` and
  `spec/25_agent-operability.md` (`tests/registers/line-citations.yaml` lines 4990 through 4993), so every
  citation from a carrier outside `spec/` into a shifted range is reported as a failure
  (`scripts/specshift/gate/resolution.go` lines 177 through 181) and no specification-phase edit can
  convert it. §5 carries a row for it, §11 decision 3 covers it alongside SPEC-1's and SPEC-4's criteria
  with the same three options, and the counts in §2 decision 11 and in AMEND-1's closing paragraph are
  corrected from two to three.
- **§2 decision 4's ordering sweep still read as though SPEC-1 carried no open ordering.** The sweep closed
  with "§11 records the orderings SPEC-3's and SPEC-4's assignments do not establish", which contradicted
  the §11 decision 8 this pass added, the §5 row it added, and SUPPLY-1's new paragraph. The SPEC-1
  sentence now states that the assignment orders the invocation after the §28 declaration alone and that
  the ordering against SPEC-1's three hand corrections stays open, and the closing sentence names SPEC-1,
  SPEC-3, and SPEC-4.
- **The new SPEC-3 exit-criterion passages cited the wrong range in the citation resolver.** Lines 200
  through 213 of `scripts/specshift/gate/resolution.go` are the end of `Run` and the head of `scan`. The
  code that classifies a non-resolving citation as a failure the baseline does not carry is at lines 177
  through 181, and the `Failures` field contract is at lines 101 through 103. The §5 SPEC-3 row, §11
  decision 3's SPEC-3 paragraph, and this pass's own log entry now cite lines 177 through 181.

### Pass 7 (2026-08-01, automated)

- **SPEC-4's anchor invocation was exempted from the empty-diff sentence on a measurement AMEND-1's own
  SPEC-3 invocation falsifies.** AMEND-1 stages an earlier `-only spec/` anchor invocation in SPEC-3 driven
  by the same single move map, and the anchor pass scopes its site search by that map alone: `findSites`
  makes every markdown link whose fragment is any anchor of the map a site and rewrites it to that map's
  successor, with no register and no anchor subset consulted (`scripts/specshift/anchor/site.go` lines 116
  through 146), while SUPPLY-3 seeds `tests/spec-anchor-moves.json` once with one entry per retired anchor,
  including `internal-messagepart-format` (0064 lines 2663 through 2669), and decision 3 rejects a
  per-confinement register pair. SPEC-3's invocation therefore rewrites the `#internal-messagepart-format`
  link at `spec/15_external-api-surface.md` line 1399 that 0064 assigns to SPEC-4's pass (0064 lines 2805
  through 2808), so SPEC-4's `-only spec/` invocation finds no site, returns every file unchanged
  (`scripts/specshift/anchor/anchor.go` lines 141 through 143), plans zero files, and leaves the empty
  mechanical diff the specification-phase verifier treats as a failure
  (`.claude/workflows/implement-proposal.js` line 375), which is the `spec-unappliable` outcome this
  proposal exists to remove. AMEND-1 item 5 now takes the empty-diff sentence for that edit as well, on
  that measurement, and the count of mechanical edits carrying the sentence is corrected from four of six
  to five of six in AMEND-1 item 5, the §5 row, §11 decision 6, and §12. §11 decision 6 also records that
  reassignment is unavailable for this edit and states the options and the choice this proposal stages.
  The pass 4 log entry stating that SPEC-4's anchor pass writes its assigned file is superseded by this
  entry. Decision 4 and AMEND-1 item 1 no longer describe `spec/15_external-api-surface.md` as the file
  both of SPEC-4's passes rewrite.
- **The overlap of the anchor pass's two `-only spec/` confinements across SPEC-3 and SPEC-4 was recorded
  nowhere.** The existing §5 overlap rows are scoped to two invocations of one sub-step. §5 gains a row
  stating that both invocations read the whole move map, that the second is a silent zero-file run rather
  than an abort because the anchor pass carries no claimed-entry check, and that the pair of `-except
  spec/` runs stands in the same relation outside the specification-phase verifier's reach.
- **Correction to this pass: §11 decision 6's reassignment option was widened to five edits while its
  enumeration and its own exception paragraph cover four.** The empty-diff count of five of six is correct
  and stands at §5, AMEND-1 item 5, §11 decision 6's opening, and §12. The reassignment quantifier is a
  separate count: the sentence enumerates SPEC-1, the two line-pass edits, and SPEC-3's anchor-pass edit,
  and the paragraph below it states that reassignment is unavailable for SPEC-4's anchor-pass edit because
  SPEC-3's invocation leaves no `spec/` file carrying a site of that class
  (`scripts/specshift/anchor/site.go` lines 116 through 146,
  `scripts/specshift/anchor/anchor.go` lines 141 through 143, `spec/15_external-api-surface.md` line 1399,
  0064 lines 2663 through 2669). Decision 6 therefore read as offering the reviewer a choice over five
  edits, explaining it for four, and declaring it unavailable for the fifth. The quantifier is corrected to
  the four reassignable mechanical edits.

## 11. Open decisions for review

1. **Who resolves the two-valued occurrences in the sense registers.** The identifier register exists
   because one retired spelling denotes two channels and a single-channel spelling also occurs where the
   text is not a channel, and the reserved-phrase register carries the identifiers each prose site denotes.
   The implementer can seed both from the §28.3 naming table 0064 stages and from the site-by-site
   evidence, with the reviewer auditing the result, or the reviewer can resolve the ambiguous sites before
   the seeding is written. The choice sets whether sign-off on this proposal is sign-off on the per-site
   judgements. SUPPLY-2 reduces the identifier half of that population to the `spec/` occurrences, which is
   17 sites across 4 files. The anchor sense register carries the same kind of judgement over its whole
   population: SUPPLY-3 enumerates its members mechanically from the pass's own aborts, and which of the
   three destinations each occurrence means is decided per site.
2. **Whether the two-run window is accepted, given that two gates observe it in part.** Between the confined runs,
   tier 11 is red on every pinned literal whose specification sentence was rewritten, and the citation
   resolver is red against its baseline on every citation into the ranges SPEC-3's reduction shifts. The
   anchor half of the split is unobserved: the fragment-link gate that would report a retired-anchor link
   outside `spec/` is landed by 0064 in the code phase, after the window, and 0064 arranges for it to land
   green (0064 lines 3119 and 4136 through 4160). The alternative is to hold 0064 until the pipeline can
   write a non-specification file inside a specification sub-step, which is a change to
   `.claude/workflows/implement-proposal.js` this proposal does not stage.
3. **What to do about SPEC-1's, SPEC-3's, and SPEC-4's own exit criteria.** 0064's SPEC-1 names tier 11 as its exit criterion,
   with the eviction-route pinned literal as its stated concrete case, and the specification-phase verifier
   is required to confirm that a mechanical edit's named exit criterion is green
   (`.claude/workflows/implement-proposal.js` line 375). Under the split, SPEC-1 terminates with that
   criterion red until the code-phase run lands. AMEND-1 is scoped to how a mechanical sub-step is invoked
   and stages no change to a sub-step's exit criteria, so the reviewer decides whether to accept the red
   criterion for the duration of the change, to extend AMEND-1 to SPEC-1's exit criterion, or to change the
   pipeline.

   SPEC-3 carries a criterion of the same class. 0064's SPEC-3 states that its line pass converts every
   citation into the reduced `spec/04` and `spec/15` ranges "inside the same change that removes the
   content, and the exit criterion is that the resolver reports no failure the baseline does not already
   carry, together with tier 11" (0064 lines 2552 through 2554), and it restates tier 11 as an exit
   criterion of the sub-step alongside the resolver (0064 line 2646). Its `-only spec/` run reaches the
   `spec/` carriers alone, and the only `spec/` carriers holding line citations are
   `spec/17_deployment-topology.md` and `spec/25_agent-operability.md`
   (`tests/registers/line-citations.yaml` lines 4990 through 4993), so every citation from a carrier
   outside `spec/` into a shifted range stays stale until the code-phase `-except spec/` run lands and the
   resolver reports each one as a failure the baseline does not carry
   (`scripts/specshift/gate/resolution.go` lines 177 through 181). No edit the specification phase is
   permitted to make can convert one of those citations, so neither the verifier's confirmation nor the fix
   rounds that follow it can reach green. The same three options apply: accept the red criterion for the
   duration of the change, extend AMEND-1 to re-scope the criterion to the citations the `spec/` slice
   carries for the specification phase and restate the whole-domain form as the code-phase criterion, or
   change the pipeline.

   SPEC-4 carries a third criterion of the same class, which the split leaves unmeetable rather than
   merely red for a while. 0064's SPEC-4 states that "The sub-step's exit criteria are that every per-file
   count in the line-citation register is zero" (0064 line 2876). Its `-only spec/` line-pass run reaches
   the `spec/` carriers alone, so every non-`spec/` count in `tests/registers/line-citations.yaml` stays
   above zero until the code-phase `-except spec/` run lands, and no edit the specification phase is
   permitted to make can drive one of those counts down, so neither the verifier's confirmation nor the fix
   rounds that follow it can reach green. The same three options apply: accept the red criterion for the
   duration of the change, extend AMEND-1 to re-scope the criterion to the `spec/` slice for the
   specification phase and restate the whole-register form as the code-phase criterion, or change the
   pipeline. This proposal stages none of them, because each changes a sub-step's exit criterion, which
   AMEND-1 is scoped out of.
4. **How SPEC-4's mechanical invocations are ordered against its own hand corrections.** AMEND-1 assigns
   SPEC-4's mechanical edit to one `spec/` target file, which removes the duplicate concurrent
   invocations, and it does not order that invocation after the hand corrections SPEC-4 makes in the other
   files it targets, because those are applied by agents running in parallel. 0064 states that the
   straddling range citations are hand-corrected in SPEC-4 "because the pass fails them rather than
   guessing an anchor", so an invocation that runs first aborts on them. The reviewer decides whether to
   sequence SPEC-4's hand corrections into an earlier sub-step, to assign every one of them to the issuing
   file's agent, or to accept a re-run after the aborts. This proposal stages neither, because either
   choice reorganizes 0064's sub-steps rather than supplying data or a tool option.
5. **Whether the seeded registers should be verified before 0064 lands** by applying 0064's staged §28 text
   in a throwaway worktree and running each pass's dry run to zero aborts. The name pass refuses an
   identifier the specification does not declare and the identifier pass reads its retired spellings from
   §28.3, so no committed tier-0 test can assert that those two registers resolve every site until §28
   exists. The pinned-literal register is covered by §8 case 8 regardless. The pre-landing verification of
   the other registers is a reviewer procedure rather than a gate.
6. **Whether the apply agent's and the verifier's own confirmations are met by AMEND-1 item 5's sentences.**
   The apply agent's mechanical branch confirms that a command's dry run "touches only files this sub-step
   targets" and that "the applied diff for this file matches what the dry run predicted", and the verifier
   treats a mechanical edit whose diff is empty as a failure
   (`.claude/workflows/implement-proposal.js` lines 312 and 375). A `-only spec/` run writes the class's
   `spec/` carriers rather than the assigned target alone, and five of the six mechanical edits take an
   empty mechanical diff for their assigned file: SPEC-1's `spec/28_communication-channels.md`, because §28
   carries no reserved-phrase site; the line-pass edits of SPEC-3 and SPEC-4, whose assigned
   `spec/04_system-components.md` and `spec/15_external-api-surface.md` hold no entry in
   `tests/registers/line-citations.yaml`, whose only `spec/` entries are `spec/17_deployment-topology.md`
   and `spec/25_agent-operability.md` (lines 4990 through 4993); and SPEC-3's anchor-pass edit, because no
   reference in `spec/04_system-components.md` names an anchor the reduction retires, §4.7 keeping its own
   anchor, the file's six file-qualified links into `spec/15_external-api-surface.md` targeting
   `#151-rest-api` and `#1543-runtime-integration-levels`, both of which survive, its remaining
   file-qualified links naming target files the reduction retires no anchor in, and the file carrying no
   bare `§15.4` citation; and SPEC-4's anchor-pass edit, because SPEC-3's earlier `-only spec/` invocation
   of the same pass reads the same whole move map and rewrites every `spec/` reference into a retired
   anchor, including the `#internal-messagepart-format` link at `spec/15_external-api-surface.md` line 1399
   (`scripts/specshift/anchor/site.go` lines 116 through 146; 0064 lines 2805 through 2808), so SPEC-4's
   invocation finds no site and returns every file unchanged
   (`scripts/specshift/anchor/anchor.go` lines 141 through 143). AMEND-1 item 5 states each fact in the
   Change paragraph both agents read, which
   is the artifact that says what the sub-step targets and what its mechanical edit writes. The reviewer
   decides whether that is sufficient, whether to reassign each of the four reassignable mechanical edits
   to a `spec/` carrier its pass writes, which for SPEC-1 means establishing the §28-before-run ordering by the
   declared-identifier abort alone, for the two line-pass edits means assigning them to
   `spec/17_deployment-topology.md` or `spec/25_agent-operability.md`, neither of which the same sub-step
   otherwise targets, and for SPEC-3's anchor-pass edit means assigning it to
   `spec/15_external-api-surface.md`, which the same sub-step targets but a parallel agent applies, or to
   change the pipeline. This proposal stages the first, because the second trades a deterministic ordering
   for a fail-loud one and the third is the workflow change §7 excludes.

   Reassignment is unavailable for SPEC-4's anchor-pass edit, because SPEC-3's invocation leaves no `spec/`
   file carrying a site of that class. Its options are to accept the zero-file run on AMEND-1 item 5's
   sentence, to drop the `-only spec/` invocation from SPEC-4 and let SPEC-3's invocation stand as the
   specification phase's whole anchor run, or to change the pipeline. This proposal stages the first,
   because dropping the invocation would leave SPEC-4 with no confined command for a pass its own Change
   paragraph runs and would move the anchor class's specification-phase coverage into a sub-step other than
   the one 0064 assigns it to.
7. **Whether SPEC-3's atomic reduction, line pass, and anchor pass may be split across the two phases.** 0064 states that
   the reduction and the line pass over `spec/04` and `spec/15` are one atomic sub-step, because removing
   content shifts the line numbers roughly a thousand citations point at (0064 lines 2531 through 2560).
   Under the two-run protocol the `spec/` half of the pass runs in the specification phase and every
   citation from a carrier outside `spec/` is converted in the code phase, so those citations resolve to the
   wrong line for the length of the window and the citation resolver reports them against its baseline. The
   window is wider than the one decision 10 records, which covers spellings rather than line numbers.
   The same sub-step also runs the anchor pass over the markdown links into the anchors its reduction
   retires (0064 lines 2594 through 2597), which the split defers outside `spec/` in the same way, and that
   half carries no gate for the reason decision 11 states.
   AMEND-1 also assigns SPEC-3's mechanical edits to `spec/04_system-components.md`, which orders both runs
   after that file's reduction and heading insertion and orders neither of them after `spec/15`'s reduction,
   applied by a parallel agent, so a citation converted against an unshifted `spec/15` carries a stale line
   number. The anchor invocation carries a second and harder residue. Its move successors and its sense
   destinations name the §28.5 channel-contract cards SPEC-3 authors in `spec/28_communication-channels.md`
   (0064 lines 2668 through 2669 and 2164 through 2166), that file is a distinct target of the same sub-step
   and so is applied by an agent the pipeline spawns in parallel (`.claude/workflows/implement-proposal.js`
   lines 295 through 297), and `checkRegisters` holds every successor and destination to a heading a document
   of the tree declares and returns a hard error before any file is rewritten
   (`scripts/specshift/anchor/anchor.go` lines 240 through 272). An anchor invocation that runs before the
   sibling agent has written §28.5 therefore exits non-zero and the apply agent records the edit as
   unappliable (`.claude/workflows/implement-proposal.js` line 312), which is the `spec-unappliable` outcome
   this proposal exists to remove. Reassigning the anchor mechanical edit to
   `spec/28_communication-channels.md`'s own agent was considered and is not staged: it would put SPEC-3's
   two mechanical invocations in two parallel agents, which leaves the line-then-anchor order item 2 states
   unestablished, and it moves the empty-diff residue of §11 decision 6 onto a file this sub-step creates,
   whose link population no measurement on the current tree can state.

   The anchor invocation carries a third residue, of the class decision 4 records for SPEC-4's hand
   corrections. SPEC-3 hand-corrects 14 markdown links into the retired `1541-adapterbinary-protocol`
   anchor to a heading other than the map's successor: the seven `spec/07_session-lifecycle.md` links, to
   the surviving §15.4 `MessageEnvelope` heading (0064 lines 2784 through 2786), and the seven same-page
   `spec/15_external-api-surface.md` links at lines 1838, 2165, 2489, 2584, 2662, 2684, and 2733, to the
   heading whose material each cites (0064 lines 2786 through 2789 and 2830 through 2833). Both files are
   distinct target files of the same sub-step, so their corrections are applied by agents the pipeline
   spawns in parallel with `spec/04`'s (`.claude/workflows/implement-proposal.js` lines 295 through 297),
   both sit inside the `-only spec/` confinement, and item 2 orders the invocation only last among
   `spec/04`'s own edits. The anchor pass makes every markdown link into a retired anchor a `linkSite` and
   rewrites it to the map's single successor with no register consulted
   (`scripts/specshift/anchor/site.go` lines 129 through 146, `scripts/specshift/anchor/anchor.go` lines
   186 through 190), and SUPPLY-3 seeds the map with the retirement of `1541-adapterbinary-protocol`.
   Unlike the §28.5 case, this residue is silent: the map's successor heading exists, so `checkRegisters`
   passes, the run rewrites the 14 links to a resolving but wrong destination and exits zero. `Apply`
   writes whole files from the planned diff (`scripts/specshift/pass/pass.go` lines 441 through 464), so an
   invocation and a sibling agent editing `spec/07_session-lifecycle.md` or
   `spec/15_external-api-surface.md` at the same time lose each other's writes. The options are the ones
   decision 4 puts for SPEC-4: sequence those hand corrections into an earlier sub-step, assign every one
   of them to the issuing file's agent, or accept the wrong rewrite and a re-correction after the run.

   The reviewer decides whether to accept
   the window and all three orderings, to sequence the §28.5 authoring, the 14 hand-corrected links, and
   the two reductions into separate sub-steps, or to hold both passes entirely for the code phase. This
   proposal stages the split, because
   holding the passes would leave the specification phase's own committed `spec/04` and `spec/15` citations
   and links stale with no run to fix them inside that phase.
8. **How SPEC-1's mechanical invocation is ordered against its own three hand corrections.** 0064 corrects
   the interrupt sentences at `spec/07_session-lifecycle.md` line 324 and
   `spec/15_external-api-surface.md` line 1755 and the slot-failure sentence at
   `spec/05_runtime-registry-and-pool-model.md` line 540 by hand, "because a substitution turns each of
   them into a precise false statement" (0064 lines 1283 through 1286). All three carry the reserved
   phrase, so all three are sites the name pass finds and SUPPLY-1's register carries, and all three sit in
   files the same sub-step's `-only spec/` run rewrites and other agents apply in parallel with the
   issuing agent (`.claude/workflows/implement-proposal.js` lines 289 through 297). Both terminal orderings
   fail. A run that lands first substitutes at the three sites 0064 forbids substituting at, and the
   sibling agent's authored edit no longer locates its anchor and is recorded as unappliable
   (`.claude/workflows/implement-proposal.js` line 312). A hand correction that lands first removes the
   phrase from its file, so the file carries fewer sites than the register claims, `unclaimedReason`
   returns `the file carries N reserved-phrase site(s)` and `checkClaimed` fails the run before any write
   (`scripts/specshift/name/name.go` lines 243 and 277 through 328); this is deterministic for
   `spec/07_session-lifecycle.md`, whose single reserved-phrase site is the hand-corrected one, and it is
   the `spec-unappliable` outcome this proposal exists to remove. The same run also writes
   `spec/05_runtime-registry-and-pool-model.md`, `spec/07_session-lifecycle.md`, and
   `spec/15_external-api-surface.md` while their own agents are editing them, and `Apply` writes whole
   files from a diff planned against the pre-run contents (`scripts/specshift/pass/pass.go` lines 441
   through 464), so the two writers lose each other's edits. The options are the ones decision 4 puts for
   SPEC-4: sequence the three hand corrections into an earlier sub-step, assign all three to the issuing
   file's agent, or accept the substitution and re-correct the three sentences afterwards. This proposal
   stages none of them, because each reorganizes 0064's sub-steps rather than supplying data or a tool
   option.

## 12. Files touched on application

- `tests/registers/reserved-phrase-senses.yaml`, new: the per-occurrence senses that drive the name pass,
  seeded over the whole domain 0064's N3 states, emptied by 0064 SPEC-1.
- `tests/registers/pinned-spec-literals.yaml`, new: the pinned literals under `tests/tier11_docs/`, keyed by
  the literal's position in its carrier's string-literal sequence, derived mechanically per SUPPLY-1, and
  emptied by nothing because the name pass requires it whenever a tier-11 Go carrier exists.
- `tests/registers/identifier-senses.yaml`, new: the `spec/` occurrences alone, with 0064 SPEC-2 appending
  the remainder.
- `tests/spec-anchor-moves.json`, new: one successor heading per retired anchor, seeded from 0064's SPEC-3
  reductions, emptied by 0064 SPEC-4.
- `tests/registers/anchor-senses.yaml`, new: the per-occurrence destinations of the bare section citations
  the carve-outs leave behind, emptied by 0064 SPEC-4.
- `tests/registers/README.md`, for one row per seeded register under `## Baselines and sense maps`, naming
  the pass that reads it, which half of it is outstanding, and the change that empties it where one does.
- `scripts/specshift/main.go`, for the repeatable `-only` and `-except` flags, the `stringList` flag value,
  the usage block, the required-confinement check beside the register check, the confinement filter and the
  named confinement in `printWriteDomain`, the run report, and the four pass constructions taking
  `scope.WorkingTreeLister`.
- `scripts/specshift/pass/pass.go`, for the confinement predicate carried on `Harness`, the filter applied
  to the domain inside `Plan`, the zero-file guard naming the confinement, the skip of the key-rewrite
  channel where the confinement covers no path-keyed register, and `NewHarness` taking
  `scope.WorkingTreeLister`.
- `scripts/specshift/scope/scope.go`, for the new `WorkingTreeLister` constructor, which unions the index
  with the unignored untracked paths of the working tree, and for the doc comment on `GitLister` naming
  which consumers keep the index.
- `scripts/specshift/name/name.go` and `scripts/specshift/name/pinned.go`, for the confinement filter on the
  claimed-entry checks.
- `scripts/specshift/identifier/identifier.go`, for the same filter on its claimed-entry check and on the
  file-name rename planning in `planRenames`.
- `scripts/specshift/line/line.go` and `scripts/specshift/anchor/anchor.go`, for the deferred set each
  reports to the run report, derived from the per-file count map and from the sense register, since neither
  pass carries a claimed-entry check to derive it from.
- `proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md`, for AMEND-1's
  sentences in SPEC-1, SPEC-2, SPEC-3, and SPEC-4 stating each mechanical sub-step's confined command
  lines, one pair per pass for the two sub-steps that stage two passes, its one `spec/` target file, the
  order of its invocations, the `spec/` carriers its confined run writes beyond that file, the replacement
  of each one-run sentence with the two-run form, and, for the five mechanical edits whose assigned file
  their pass writes nothing to, that the mechanical diff for that file is empty by construction.
- `scripts/specshift/run_test.go`, for the cases §8 lists and for adding `-except spec/` to
  `TestRunDrivesAPassWithTheRegisterKeyedForItsRewrite`, whose unconfined successful run the required
  confinement would otherwise turn red.
