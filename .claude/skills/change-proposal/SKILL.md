---
name: change-proposal
description: Write an adversarially validated change proposal under proposals/, staging spec edits and/or core-product or test-infrastructure code changes, from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec or implementation defect, contradiction, or gap, asks for a fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages its changes for sign-off; it never modifies spec/, pkg/, or docs/ itself.
argument-hint: <problem statement | path to notes | path to a proposal>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Change proposal writer and convergence loop

This skill produces a reviewed proposal under `proposals/` and converges it against the spec and the code. A proposal may stage spec edits, core-product code changes, test-infrastructure changes, or a combination. It has three modes sharing one workflow:

- **new**: the input is a problem statement. The workflow writes it to disk, validates it through six lenses, drafts through six design stances, challenges each surviving change, writes the proposal files, and enters the review loops.
- **review**: the input is the path of an existing proposal. The workflow migrates it to the folder layout if it predates that, backfills what is missing, and enters the review loops.
- **redesign**: the input is a proposal path and a list of `focusAreas`. The workflow redesigns those areas first, applies the result, then reviews as `review` does. Use it when you already know which mechanism is wrong.

## A proposal is a directory

```
proposals/NNNN_kind_slug/
  NNNN_kind_slug.problem-statement.md         what is wrong, and the evidence
  NNNN_kind_slug.summary.md                   what changes, goals, non-goals, decisions,
                                              watch-outs, and the deliverable index
  NNNN_kind_slug.status.md                    typed frontmatter: Draft, Reviewed, Approved, Implemented
  NNNN_kind_slug.implementation-checklist.md  the one execution sequence
  NNNN_kind_slug.spec-changes.md              every staged edit to spec/, and nothing else
  NNNN_kind_slug.non-spec-changes.md          code, schemas, charts, migrations, docs, and Testing
  NNNN_kind_slug.review-log.md                what earlier agents learned, curated
  NNNN_kind_slug.review-log-archive.md        entries already curated; created lazily, read by nobody
  NNNN_kind_slug.deviations.md                owned by the implementor; empty until one departs
```

**Migration is lazy and automatic.** There is no batch script. A proposal written as a single `NNNN_kind_slug.md` migrates the first time this skill or `implement-proposal` touches it, through the `migrate-proposal` subworkflow, in the same commit as the work that triggered it. Nothing else moves. An `Implemented` or `Retired` proposal never migrates, because nothing ever triggers it: a landed proposal is a historical record and splitting it is an edit.

A partition check asserts that every content line of the original survived the split, and the migrator stops rather than finishing if it cannot retarget an inbound reference. A failed migration ends the run rather than reviewing a half-split tree.

**Who may write what.** `.summary.md` and `.spec-changes.md` are written by this skill alone. `.problem-statement.md` is editable by a fixer, and bounded: correcting the record there is required — a false citation, a drifted line number, an evidence claim the tree refutes — and is done in the same edit as the section that restates it, because leaving the two disagreeing is worse than leaving both wrong. Changing what the problem *is* is a `reframe`, which is the introspection pass's decision and not a fixer's. `.deviations.md` is written by `implement-proposal` alone and read here. `.implementation-checklist.md` is seeded here and maintained by the implementor. Every agent appends to `.review-log.md` through a per-agent shard the round boundary merges.

**The status is typed.** Read and write it with `node .claude/tools/proposal-status.mjs <proposal> --field status`, never by parsing prose. It handles both layouts, and the spec-lease hook reads it through the same tool. A legacy proposal that reads `Retired` was superseded or withdrawn; that is a read-only outcome outside the four states, and every consumer refuses it.

## Hard constraints

- The run edits only files inside the proposal's directory. Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages its changes and never applies them.
- All problem input is inline. Evidence comes from `spec/`, `schemas/`, `pkg/`, `cmd/`, `charts/`, and git history. Progress-tracking prose elsewhere is a lead to verify, never evidence.
- Prose follows `.claude/rules/doc-style.md`.
- New directory names are `NNNN_[new|fix]_<kebab-slug>`, where `NNNN` is the next free zero-padded number.
- Review findings are real errors only. Style preferences, optional improvements, and hypothetical hardening are excluded by construction, because the materiality skeptic refuses them.
- The workflow sandbox gives a script only `agent`, `parallel`, `pipeline`, `phase`, `log`, `workflow`, `budget`, and `args`. This was established by running a probe rather than read from a comment: there is no `require`, no `process`, no `fetch`, and the `Function`-constructor route out is closed at the V8 level. A script cannot touch a file; anything that does goes through an agent. Run `node .claude/tests/run.mjs` after editing a workflow.

## How the run is shaped, and why

### Startup

**Init** writes the eight files first, placing the problem statement verbatim and the caller's citations as unverified. Every stage after that reads the problem from disk rather than from an argument, which is what makes a `reframe` restart possible: rewrite the file and re-enter at Validate.

**Validate** runs six lenses and a consolidator. A decomposer plus a skeptic per premise attacked premises and nothing else, so a problem that was real but mis-scoped, already solved, or not worth solving passed straight through. The lenses are `premise`, `evidence` (opens every citation), `prior-art` (has the spec, a landed proposal, or an open finding already answered it), `scope` (one problem or several), `impact` (who observes it, with the default posture that it does not matter), and `alternatives` (is there a framing under which no change is needed). The consolidator rewrites the problem statement to what survives and keeps the refuted premises in the record, because a later stage reading only the survivors re-derives the refuted one.

**Draft** runs six stances and a consolidator. One drafter made the entire design surface of a run a single sample. The stances are `minimal`, `spec-first`, `reuse`, `failure-modes`, `implementor` (which produces the sequence as part of the design), and `contrarian` (which argues no change is needed and produces a design only if it fails). Each is told to commit to its own reading rather than hedge toward the others. The consolidator picks a spine it will defend by name and grafts onto it, rather than averaging six designs into one nobody argued for, and a stance that dissented reaches it as an argument to answer.

**Challenge** is unchanged and runs per surviving change, with the default posture that the change is unnecessary.

### Two review loops

The spec staging converges first, alone. Then the non-spec staging converges, with the lenses reading **both change files as one document**, because a non-spec change that contradicts a staged spec edit is a finding only a reviewer holding both can see. The spec loop is skipped only when a cheap probe reports the proposal INTENDS no change under `spec/`. Intent rather than completeness: a proposal that names a spec target it has not written yet needs the loop more than one whose staging is finished, because writing that text is the work the loop does.

**Between them is a reconciliation.** One pass rebuilds the deliverable index and writes the checklist's spec-lane steps as the leading block, and it discharges the `DEFERRED` entries the spec loop recorded: it applies the ones that are repairs to text already written, filing a `CORRECTS` for each, and carries forward as an `OPEN` any that would require authoring a staged change no non-spec lens has read. That is the one place the pass changes what the proposal says, and only to apply a correction the spec loop already derived. Before it existed, those corrections accumulated for four and a half hours on a measured run as an errata list in the summary promising fixes that no lane owned. That is why the spec loop's lenses are told drift in the checklist and the index is expected there and is not a finding: it is scheduled, not overlooked. It runs **whether or not the spec loop converged**, because a loop that exhausted its budget has more unreconciled consequences than one that converged, and it is told which case it is in so it reconciles to the current text rather than guessing where open findings will land.

**The spec loop runs a smaller pool.** It drops the `test-coverage` lens, since the tests a change needs are staged in the non-spec half, so spec convergence certifies nothing about test coverage.

**The reconciliation does not run after an introspection stop.** A `halt` or `reframe` ends the run where it stands and nothing is reconciled.

**The non-spec loop does not start on a spec staging that is still moving.** When the spec loop exhausts its budget without converging, the run reconciles, then stops with status `spec-not-converged`, naming the rounds, the budget, and what the loop was still finding. Raising `maxSpecReviewRounds` and resuming is the operator's call; spending the second budget reviewing against staging that is still open is not a decision the run makes silently. `allowNonSpecOnUnconvergedSpec` overrides this.

**The spec fixer repairs what its own edits falsify in the non-spec staging.** It may correct a statement already written in `non-spec-changes.md` that one of its own spec edits just made false, in the same edit, and only where that file already has content. The trigger is always a spec finding: it never goes looking there, may not author a staged change because a spec edit implies one is needed, and may not touch a defect that is wrong on its own terms. The lenses are unchanged and are told not to file such a statement as a finding, because it is the consequence of an edit rather than a finding of its own. The checklist is deliberately excluded and its drift stays with the reconciliation pass.

Each loop keeps its own rounds, retired set, sweeps, and convergence, because a lens satisfied by the spec staging has said nothing about the code staging. The refuted-findings memory and the round history stay run-wide, so a finding refuted in the first loop is not re-litigated in the second.

Convergence is unchanged: a lens that finds nothing retires, a full sweep of the pool runs when every lens has retired, and a clean complete sweep converges. `operational` and `fresh` were once a second pool with their own schedule, one rotating in per round. That is a second mechanism for the job retirement already does, and the worse of the two, because it withheld a lens on a round number rather than on evidence. It also produced a lens that had never run, which cannot retire, which blocks the sweep, so a clean proposal spent a whole round discharging one lens: a measured run went thirteen lenses, then `fresh` alone, then a fourteen-lens sweep. The singleton round costs a snapshot, a dedup, the verifiers and a boundary whatever it contains. A round now **closes before it may certify**, because a round whose bookkeeping did not complete left its log unmerged and the next round without a snapshot.

### The open-decisions-and-impact-review phase

Resolving an open decision is different work from fixing a gap. A gap is a defect: the proposal is wrong,
incomplete, or contradicts itself, and repairing it makes a false thing true. An open decision is a question
the proposal deliberately left unanswered, so answering it is an act of authoring and produces text that did
not exist before. The review loop is built for repair end to end. A lens files a defect claim, the
materiality skeptic asks whether leaving it unfixed would make the applied spec inconsistent, a citation
false, or the described implementation not work, and an entry in the open-decisions section stages no spec
edit, no code, and no test, so its wording cannot make any of those wrong. On a measured run the skeptics
refused most of what the retired `open-decisions` lens filed, and they were right to: the repair gate admits
a resolution only where the resolution happens also to be a defect, and that band is chosen by coincidence
rather than by the decision's importance. So the lens is gone and this phase owns the work, with a gate and
a write path of its own.

**It is a subworkflow**, at `.claude/workflows/change-proposal-decisions.js`, invoked by path from the
review workflow. One invocation is one firing, and each firing runs every sub-task.

**The collectors are separate briefs.** One brief over every population would ask its question of all of
them, and the question that fits a decision left to the human is the wrong one to ask of a bounded
implementor blank. Sub-task 1 collects every decision the proposal leaves to a human, including any left in
a staged change file's retired `## Open decisions for review` section, and decides for each whether it is
really the human's or whether the workflow can answer it; three independent agents run it, because it is the
widest judgment in the phase, and an item reaches `resolve` only when all three resolve it to the same
answer. Sub-task 2 collects every `IMPLEMENTOR'S CHOICE:` marker and every unbounded blank, and specifies
one only where leaving it to the implementor is a clear risk or an obviously wrong path; the default is to
leave it alone, because the escape hatch exists so a proposal does not grow hair. Sub-task 3 collects every
defect the proposal calls out as out of scope and asks whether that is the right call, defaulting to yes,
and stages a fix where it is not. Sub-task 4 sweeps `proposals/` for impacts, under the rule that a draft
may be invalidated freely, a recently reviewed proposal warrants care, and an implemented one is already in
the tree; the corpus inventory it reads is gathered by one cheap agent at the phase's first firing, carried
on the phase state, and reused by every later firing, with a legacy single-file proposal's last commit date
declared as such where `proposal-status.mjs` carries no review date.
Sub-task 5 is the gate, sub-task 6 the write path, sub-task 7 the summary cleanup, and sub-task 8 a
read-only check of factual accuracy and format conformance.

**Falsification and Apply are one agent per item.** A single agent reading a dozen dispositions gives each
one a fraction of its attention and returns a verdict that answers to the batch rather than to the item; a
falsifier holding one item can open every file that item touches. The fan-out pays twice: each Apply
produces its own diff, which is what detecting a reversal per item rests on, and an agent that dies takes
one item with it rather than the batch. Falsifiers run in parallel. **Apply agents run sequentially**,
because they edit the same files and concurrent edits to one markdown file lose writes, and each after the
first is told what the earlier ones in that firing wrote.

**The gate is falsification rather than defect review.** The question about an authoring act is whether the
judgment is sound, which is what a falsification panel asks, and the brief differs by disposition following
the introspection panels. The defaults are asymmetric and are drawn from the two gates the workflow already
runs. A disposition that creates text nothing else reviews takes the defect gate's posture and needs
affirmative support, so an uncertain verdict refutes it. A disposition that leaves the proposal as it stands
takes the falsification panel's posture and stands unless its falsifier refutes it conclusively. The tally
is script-side: if Apply decided what survived, the gate would not be a gate.

**A refuted disposition is set aside rather than replaced.** The phase invents no substitute. A refuted
`resolve` leaves the decision open and its entry stays listed for the human, a refuted `human` names no
replacement and carries the refutation against it, and a refuted `implementor` leaves the blank as the
proposal wrote it. Each item's record carries the refutation, so a later firing sees that the ground was
contested without re-arguing it.

**A reversal is not re-argued.** Before a later firing adjudicates anything it checks each applied item
against the tree. Where the review loop reverted or overwrote what the phase wrote, the item is marked
CONTESTED, recorded with both positions, and routed to the human. It is never re-applied and never
re-adjudicated, which is the whole of the loop-prevention rule. Reopening an item because its text changed
is what would make the phase insist. An item nothing has touched carries its earlier disposition forward,
and an item whose entry the loop reworded matches its record through the stable identifier and carries
forward too.

**What it may edit** is `spec-changes.md`, `non-spec-changes.md`, and the summary, plus the review log,
where it records what it wrote and files the `DEFERRED` entries the cleanup relocates, and
`.problem-statement.md`, where it may correct a falsehood and may not restate what the problem is. It
authors into the staged change files because that is where a resolution belongs and because the review that
follows certifies it. The implementation checklist and every file outside the proposal are out of bounds.
Under `lockSpecChanges` the spec staging is closed, and a resolution needing it is recorded for the operator
with the edit it would have made rather than staged.

**Change detection comes from git rather than from the agents.** The workflow commits the proposal directory
before each firing, and the firing reads `git status --porcelain` and a diff around each Apply under that
same pathspec. An Apply that claims a resolution landed while its diff is empty is recorded as a failed
item. The same evidence is what a later firing compares against to detect a reversal.

**Where it fires.** Every review loop is followed by a firing: the spec loop, the non-spec loop, and every
recheck of either lane. That is why there is no condition asking whether any decision changed, since the
answer is always to look, and carrying an untouched item's disposition forward is what keeps an empty firing
cheap. The firing after the spec loop sits before the non-spec loop starts and runs on the paths where the
spec loop never ran and on the paths where the non-spec loop does not run, so a run that stops early is
still adjudicated once. A periodic firing runs inside the non-spec loop as well, at the round boundary every
`periodEvery` rounds, so decisions are adjudicated as they accumulate rather than in one batch at the end.
Its cadence counts firings rather than rounds, a round that exits on introspection is covered by the
post-loop firing instead, and no periodic firing runs inside a recheck. It is the only trigger whose count
is open-ended and the only one with a budget of its own, `maxPeriodicFirings`: exhausting it stops the
periodic trigger for the rest of the run, is reported in the result object, and leaves every post-loop
firing running. **All firings are full firings.**

### Rechecks, and when the run may converge

The run may converge only when no lane's staging has changed since that lane's own last review. A firing is
what can change a lane's staging after its review, and a recheck is what reviews it again. Every line the
phase writes into `spec-changes.md` is therefore read by a spec-scoped review and every line it writes into
`non-spec-changes.md` by a non-spec-scoped one, under that lane's own `editable` and `scopeNote`.

Each trigger is a content hash taken by the script rather than a claim from an agent. The spec hash covers
`spec-changes.md` and is taken at each spec-scoped convergence; the non-spec hash covers
`non-spec-changes.md` and the summary and is taken at each non-spec-scoped one. Both are re-taken at every
convergence, because a baseline held from the first one would read a recheck's own edits as drift and fire
against them forever. On a run whose spec loop never ran, the spec baseline is taken immediately before the
first firing, which is the last point that lane was settled.

A post-convergence spec edit runs a **recheck pair**: a `spec-recheck` and then a `non-spec-recheck`, each
carrying its lane's pool, `editable`, and `scopeNote` unchanged, on `maxRecheckRounds`, with a firing after
each. The pair is not symmetry for its own sake. A spec edit can falsify non-spec text whether or not the
spec fixer touched that file, and the spec fixer may repair such a statement directly, so the result is
non-spec text no non-spec lens has read, which is the outcome the fixer's own rule exists to prevent. The
trigger is any post-convergence spec edit rather than the phase's alone, since the non-spec fixer holds the
same permission. When the non-spec hash has moved and the spec hash has not, a **lone `non-spec-recheck`**
runs with no `spec-recheck` in front of it. When both have moved, the pair runs and no lone recheck is
taken, because the pair's `non-spec-recheck` already reads that text. Each recheck's scope note names the
delta since that lane's last convergence as its lenses' focus while they still read the whole staging: the
pool does not shrink and the attention narrows. Under `lockSpecChanges` no post-convergence spec edit
happens, so no pair runs.

A pair can beget a pair, because `non-spec-recheck` keeps the permission to edit `spec-changes.md` and such
an edit lands after `spec-recheck` converged. Nothing in the mechanism makes the exchange shrink, so budgets
bound it: `maxRecheckPairs` bounds the pair and `maxNonSpecRechecks` the lone recheck, counted separately
because a firing follows every recheck and a lone recheck can beget a pair exactly as a pair can.
Exhausting either is a reported stop condition, naming the lane, its files, the outstanding edit, the last
firing that wrote, and what to raise, in the same posture the run takes for `spec-not-converged`: the run
stops taking rechecks, does not converge, and returns status `recheck-budget-exhausted`. In the ordinary
case no recheck runs at all.

### The summary the phase leaves behind

`summary.md` carries these sections, in this order, and nothing else:

```
# Summary: <title>
## Summary                        a container that carries no prose of its own, holding:
      **Problem statement.**      what the change repairs, without the evidence, the citations, or the
                                  refuted premises, which stay in the problem statement file
      **What changes.**           one bullet per top-level change, each naming the surface it lands on
      **Decisions.**              the decisions an implementor must not revisit, one line each
      **Watch out for.**          the traps
## Goals
## Non-goals
## Open decisions for human to make
## Defects in the shipped tree that this proposal does not stage
## Impacts on other proposals
## Deliverable index
```

Each entry under `## Open decisions for human to make` states the question so it can be answered without
reading the proposal, the recommendation, the ground, the alternatives and why each lost, what deciding
otherwise costs, and a confidence. It also carries a stable identifier, stamped when the entry is first
written and preserved by every later edit of that entry, which is what a later firing matches an item to its
record through. **A resolved or withdrawn decision leaves the summary**, and its record is returned in
`decisionsResolved` rather than parked in the file: a section that keeps answered entries teaches its reader
the list is noise. `## Impacts on other proposals` is the only place the proposal asserts anything about
another proposal's continued validity, which is why it is a section rather than a labelled part.
`## Deliverable index` stays last and the phase leaves it exactly as it stands, since the reconciliation
pass between the loops owns it.

The cleanup pass runs after Apply, because what belongs in each section depends on what Apply resolved, and
it relocates rather than deletes: a block of confirmed shipped-tree defects is promoted to its own section,
prose about other proposals merges into the impacts section, and corrections the proposal owes to files its
loop could not edit become `DEFERRED` entries in the review log. A summary carrying `**Fixed decisions.**`
is renamed in place.

### Verification

Materiality runs first and evidence only if it survives. Materiality reads only the proposal, defaults to refuted, and kills the largest share for the least cost; evidence opens every cited file and is the expensive one. A refusal records which skeptic made it, because "not material" and "the citation is wrong" are different signals to a later round's lens.

A verifier that **died** is not a refusal. The finding reaches neither verdict, is not added to the refuted memory where an outage would suppress it permanently, and the round cannot certify convergence.

### Fixing

Three stages replace one fixer.

**site expansion** runs first, one Sonnet agent per confirmed finding, in parallel. It starts from the sites the finding already names and answers one question about the rest of the repository: if this fix lands, which other text becomes wrong? The test is falsification rather than relatedness, so a site that discusses the same subject and stays true is not reported; consistent restatement is excluded by the review bar. Results land in `potentiallyRelatedSites` on the finding, never merged into `where` or `evidence`, because those two survived two verifiers and these did not. Proposal sites and tree sites are kept apart: the fixer edits the first and may not touch the second, where a falsified site means the proposal is missing an edit site.

**fix-plan** splits the round's confirmed findings into cohesive groups. The only cap is on the number of groups; **group size is uncapped**, because size is the wrong axis. Forty trivial citation corrections that share a subject belong in one group where one fixer applies them consistently, while three deep design findings belong in three groups however few they are. A partition that drops, duplicates, or invents an index is rejected in favour of one group of everything.

**fix-design** designs each group, read-only, in parallel. It adjudicates every potentially related site into `in-scope` (this fix makes it wrong, so it changes in the same edit), `separate-finding` (already wrong for a reason of its own, and fixing it here would be an unreviewed edit), or `not-a-site`. The designer bounds the fixer, which is what keeps expansion from inflating an edit that is already the likeliest source of the next round's findings. A location an earlier round already rewrote arrives with each previous attempt and why it was rejected, and the design must say how this attempt differs in kind rather than in degree. It triages each finding by effort *before* investigating, and spending deep effort on a trivial finding is named a defect in its work rather than thoroughness. On a deep finding it establishes ground truth in the repository before reading what the proposal says, and is asked whether an existing surface already carries the thing, whether one change closes several findings, and whether the strongest answer is to delete rather than specify. It carries an explicit mandate against the proposal growing hair, and records the tempting wrong fix by name.

The designs are produced in parallel and none sees the others, so **one reconciliation pass** runs over all of them before any is applied. It resolves rather than reports, prefers merging two additions into one, and returns revised designs for the groups it changed. Catching a conflict there costs one agent; catching it in the post-fix review costs a round and two edits to unpick.

**fix** runs once per group, sequentially, applying a design rather than inventing one. Each fixer after the first is told what the earlier groups in that round actually did, which the design stage could not know because it ran before any edit landed. It receives the alternatives so it neither re-derives them nor quietly picks one already ruled out, and a fixer that judges a design wrong declares it rather than substituting its own silently.

One **post-fix review** runs per round over every group's edits, which catches the risk the split introduces: drift between two groups that each edited correctly.

### A run's history lives in the review log

A fixer, a redesign, and a prune each record what they did to their log shard rather than to the staged
changes. They used to append a `### Pass <N>` subsection to `.spec-changes.md` or `.non-spec-changes.md`
every round, which is not a staged change, compacts nothing, and is read in full by every lens every round:
one measured proposal reached 22 subsections and 1258 lines, 68% of its spec-changes file. The pipeline also
disagreed with itself, because `migrate-proposal` moved that history to the archive on migration while the
fixer rebuilt it in the file the migration had just cleaned.

A withdrawn alternative takes the tag that carries it. A staged change a round reverted, or an option it
tried and rejected, is a `WATCHOUT`, or a `MISTAKE` where the round staged it and then took it out. It is
not only a `DECISION`, because a `DECISION` becomes one line in the standing context's lookup table and the
line that stops the next round re-deriving the option is the reason it lost. `### Traps` gives it four lines,
is uncapped, and is the part of the budget the compaction pass is told to protect. A trap recording a
withdrawn option is never dropped while the design that replaced it still stands.

Pass history an earlier run already wrote is evacuated to the archive by the round boundary, lazily and once,
on whatever it finds. There is no batch migration, for the same reason the folder split has none.

### The review log

Most agents append what a future agent would need, with a fixed tag vocabulary: `DECISION` with its alternatives, `WATCHOUT` with evidence, `FACT`, `MISTAKE`, `UNVERIFIED`, `OPEN`, `DEFERRED` for a correction the agent derived but may not land because its remedy is in a file its loop cannot edit, and the two that make compaction possible, `CORRECTS` and `USEFUL`. An `OPEN` is a question nobody has answered and a `DEFERRED` is an answer nobody has applied, which is why they are separate tags and only one of them has an owner. Each writes its own shard, because a dozen lenses appending to one file in parallel lose writes.

One agent per round runs `cp-round-boundary.sh`, which merges the shards, measures the log, compares file hashes for the write audit, snapshots the tree the next round reads, and returns the caller's mid-run overrides.

**Compaction triggers on the standing context, with the ledger as a backstop.** That is the section every agent carries; the ledger is read end to end by the compactor alone, though agents cite individual entries by id, so an aged entry is retired rather than deleted and keeps its id. Triggering on ledger length fired an expensive pass to protect against a cost that does not exist.

**The trigger and the target are separate numbers, and the target moves.** They were one number, so a run that could not reach it paid for a pass every round for the rest of its life: one measured run spent twelve passes averaging 700s and ended at 376 lines against a target of 80. When a pass cannot reach the target, the target rises to what it achieved plus headroom and the trigger follows, so the run finds its own level. It decays the same way: as the section shrinks the target follows it back down, never below the number the caller asked for, so one failed pass does not hold the target up for the rest of the run. Both directions preserve the caller's own gap between target and trigger rather than reverting to the default. The numbers are deliberately high, because the cost runs the other way than it looks: on that run the oversized section cost about 4% of the tokens and the passes protecting it cost about 21% of the wall clock. The count of raises goes to the introspection pass, where a count that keeps climbing says the loop is accumulating unresolved state faster than it resolves it.

**`## Standing context` is structured rather than merely bounded.** `### Settled` and `### Open` are one line per entry and always meet their budget; `### Traps` carries the `WATCHOUT` and `MISTAKE` entries at up to four lines each with no cap on how many, because a trap the reader cannot recognise is one they walk into anyway. `### Deferred` holds every unclosed `DEFERRED` whole rather than summarised, because the reconciliation pass applies these and cannot apply a headline. Each entry gets a short bold subject: measured on a real run, agents cite standing-context entries by subject and a quarter of all citations went to two of them, so navigability is worth more than brevity.

**Compaction is two halves: the agent curates, the script drains.** The pass rewrites `## Standing context` from the WHOLE ledger; the round boundary then appends that ledger to `<stem>.review-log-archive.md`, whole and with its ids, and empties it. The archive is a **separate file the agent is never given the path to**, rather than a section of the log guarded by an instruction. Measured against this same prompt, instructions of that kind do not hold: "read the file once, write it once" was ignored by every pass, "do not page through it with `sed`" was answered with forty Bash calls, and the pass invented a destination the prompt never described. Keeping the archive out of the file also keeps the log small for the twelve agents a round that read its standing context. The move happens at the FOLLOWING boundary and before that round's shards are merged, so it can never sweep up entries the pass did not read, and it is gated on a pass having actually run rather than having been asked for. Entries move whole rather than as a one-line summary, so a dead agent loses nothing and a human chasing a citation still finds it; the archive grows without bound, which is free because nothing reads it. Two consequences the prompt states: a `CORRECTS` is honoured against the standing context, since the entry it names may already be archived, and the standing context is therefore the whole live record — a claim that is not there is one the run has lost. The ledger holds one compaction window rather than accumulating, so it sawtooths instead of ratcheting across runs.

The pass edits rather than rewriting the whole file. It was told to write the file once, and every pass ignored it, because reproducing fourteen hundred unchanged ledger lines to change forty standing-context ones is not a saving; the instruction was wrong. Paging the file in with `sed -n` and assembling the result through heredocs is still barred. It does **not** check the repository, since doing so turned a text operation into a mini-review. `MISTAKE` is named the most valuable tag and is never dropped, because an entry saying *"I spent this round hunting X; it is not there"* saves a later agent a whole round. Otherwise it acts on the log's own signals: a `CORRECTS` entry rewrites or retires its target, a superseded watchout is deleted rather than kept, a `USEFUL` entry is promoted and never dropped, and two entries that disagree with no correction between them resolve to the newer, with the older retired.

### Introspection

A **warrant gate** runs before the full pass. The counters that wake it are crude and wrong in both directions, so the pass first asks cheaply whether the pattern is really there; an unwarranted counter wake returns healthy without paying for a pass or a panel. A **cadence** wake ignores the gate, because the cadence exists to look when no counter has fired.

**Every verdict goes to a panel, including healthy**, which is the most expensive verdict in the loop and had nothing checking it. Each verdict has its own panel whose judges read the same evidence and weigh different things; the `redesign` panel works to the same principles as `fix-design`.

**The judges falsify rather than vote.** Handing a reviewer a conclusion and asking it to check produces agreement rather than examination. A judge attacks the pass's argument, must restate it first, and the verdict **stands unless a majority falsifies it conclusively**. `partial` is an honest answer that leaves the verdict standing.

A stopping verdict carries **proposed next steps**, because a halt that says only to stop leaves a human work the pass is best placed to do.

## Automatic restart on a clear halt

When the run returns `introspection.stoppedBy` with `verdict: "halt"`, **and** `introspection.nextSteps.confidence` is `clear`, **and** the proposed `rerunArgs` parse and name only known arguments:

**Relaunch the workflow immediately with those arguments, without asking first.** Report that you did, with the pass's reasoning and the arguments used.

A stopped run returns status `stopped-halt` or `stopped-reframe`, and its findings remain open.

At most **two** automatic restarts per invocation; track the count yourself. On the third, stop and put the question to the user. Stop and ask also when `confidence` is `needs-human`, when the arguments do not parse, or when the verdict is `reframe` and the pass proposed no problem-statement edit: a reframe rewrites the problem, and rewriting it on a guess is worse than pausing.

## Arguments

Every argument carries a class, and the class decides how you change it. `forward` is read where it is used and appears in no prompt already issued. `anchored` is baked into prompts the run has issued. `launch` controls how a run starts.

| arg | class | default | effect |
|:--|:--|:--|:--|
| `startPhase` | launch | `validate` | the first phase to run: `validate`, `draft`, `write`, `conventions`, `spec-review`, `non-spec-review`, `finalize` |
| `baseModel` | launch | `opus` | the model every agent runs at unless it names its own |
| `baseEffort` | launch | `medium` | the reasoning effort every agent runs at |
| `mode` | launch | — | `new`, `review`, or `redesign` |
| `problem` | anchored | — | required in `new`: the problem dossier |
| `proposalPath` | launch | — | required in `review` and `redesign`: the directory, or a legacy `.md` |
| `nextNumber` | launch | — | required in `new`: the next free `NNNN` |
| `date` | anchored | — | today as `YYYY-MM-DD`; scripts cannot call Date |
| `repoRoot` | launch | — | absolute repository root |
| `exemplar` | anchored | — | the highest-numbered other proposal |
| `context` | anchored | none | citations gathered so far; the run re-verifies all of them |
| `planPath` | anchored | none | a plan this proposal implements steps of; enables `plan-conformance` |
| `maxSpecReviewRounds` | forward | 15 | budget for the spec loop |
| `maxNonSpecReviewRounds` | forward | 15 | budget for the non-spec loop; it wins over `maxReviewRounds`, which applies only when this is unset |
| `maxReviewRounds` | forward | none | a fallback budget for the non-spec loop, used only when `maxNonSpecReviewRounds` is absent |
| `periodEvery` | forward | 3 | rounds between periodic firings of the open-decisions phase, counted at the non-spec loop's round boundary |
| `maxPeriodicFirings` | forward | 5 | the periodic firing's own budget; exhausting it is reported and stops that trigger alone, leaving every post-loop firing running |
| `maxRecheckPairs` | forward | 2 | how many `spec-recheck` plus `non-spec-recheck` pairs may run; exhausting it stops the run with the outstanding spec edit unreviewed |
| `maxNonSpecRechecks` | forward | 2 | how many lone `non-spec-recheck` loops may run, under the same reported stop |
| `maxRecheckRounds` | forward | 5 | round budget for each recheck loop, held apart from the two loop budgets above |
| `skipSpecReview`, `skipNonSpecReview` | launch | false | a skipped loop certifies nothing about its half, echoed in the result |
| `lockSpecChanges` | forward | false | the non-spec loop may never edit the spec staging; such a finding becomes an open decision |
| `allowNonSpecOnUnconvergedSpec` | forward | false | runs the non-spec loop even when the spec loop exhausted its budget; otherwise the run stops at `spec-not-converged` |
| `verifyOrder` | forward | `["material","evidence"]` | which skeptic short-circuits |
| `verifySequential` | forward | true | false restores both skeptics in parallel |
| `maxFixGroups` | forward | 7 | the only cap on the fix split; group size is uncapped by design |
| `fixDesignDepth` | forward | `auto` | `shallow` forces the trivial path; `deep` forces the architect path |
| `maxExpansions` | forward | 12 | confirmed findings per round given a site-expansion pass. A finding the cap skipped, or whose pass died, is marked as NOT SEARCHED in the designer's and fixer's prompts, so absence of sites is never read as evidence there are none |
| `skipExpansion` | forward | false | turns site expansion off; the designer then sees only the sites the finding names |
| `introspectEvery` | forward | 5 | rounds between mandatory passes |
| `introspectGate` | forward | true | the warrant gate; a cadence wake ignores it either way |
| `judgesPerVerdict` | forward | 3 | panel size for non-healthy verdicts |
| `judgesHealthy` | forward | 2 | panel size for `healthy` |
| `falsificationBar` | forward | `conclusive` | `partial` makes the panel easier to convince |
| `standingContextTarget` | forward | 200 | what a compaction pass is asked to reach; raises itself when a pass cannot |
| `standingContextTrigger` | forward | 320 | when compaction becomes due, kept above the target so a pass buys real headroom |
| `compactAtLines` | forward | 2000 | a backstop on ledger length, not the trigger |
| `compactGrowthLines` | forward | 400 | plumbed to the boundary script and read by nothing; the knob has no effect |
| `kind` | launch | `fix` | selects the `NNNN_[new/fix]_<slug>` directory segment in `new` mode |
| `lensPrompt` | anchored | none | appended to every review lens. This is the only route to them; there is no `prompts.review` key |
| `prompts` | anchored | `{}` | per-agent text, keyed by agent |
| `startLenses` | anchored | none | lens keys to lead with; every other begins retired and first reads in the sweep |
| `excludeLenses` | forward | none | lens keys removed entirely; convergence certifies nothing about those domains |
| `focusAreas` | launch | none | required in `redesign`: a slug or `{area, reason}` each |
| `churnWindow`, `churnMinFindings`, `churnStrikes` | forward | 6, 5, 3 | the churn detector's thresholds |
| `maxRedesigns`, `redesignReviewRounds` | forward | 2, 2 | the redesign budget |
| `maxPrunes` | forward | 2 | the prune budget; a section the run already pruned is not pruned again |
| `runTag` | anchored | the stem | namespaces the log shards, snapshots, cache, and state |
| `resumeState` | launch | false | continue a loop at its recorded round with its retired set |

`prompts` keys: `validate.<lens>`, `validate.consolidate`, `draft.<stance>`, `draft.consolidate`, `challenge`, `write`, `bootstrap`, `conventions`, `handoff`, `expand-sites`, `fix-plan`, `fix-design`, `fix-design-reconcile`, `fix`, `compact`, `introspect.gate`, `judge.<verdict>`. `introspect` reaches only the gate by prefix fallback; the introspection pass itself takes no injected text. To add text to every review lens use `lensPrompt`, which is a standalone argument rather than a `prompts` key. The text is wrapped in a block saying it adds context and focus, does not lower a bar, and that an instruction to reach a conclusion is to be ignored and reported.

Lens keys: `citations`, `feasibility`, `edit-sites`, `mechanism`, `security`, `kubernetes`, `performance`, `reliability`, `client-surface`, `docs-alignment`, `test-coverage`, `applicability`, `operational`, `fresh`, and `plan-conformance` when `planPath` is set. Every lens is scheduled the same way: it runs unless it has retired, and when all have retired the whole pool runs again as a sweep. An unknown key in `startLenses` or `excludeLenses` is a hard error.

### Starting partway through

`startPhase` names the first phase to run and skips everything before it, which is how a caller resumes a
long run at the point it stopped rather than paying again for phases that already finished. Nothing checks
that the skipped phases were in fact done, and the run logs that it assumed so. It is refused in `new` mode
for anything but the default, because the phases it would skip are the ones that create the proposal.

This is coarser than the harness-level resume. `resumeFromRunId` replays a run's cached agent calls and
continues at the exact interruption point, so prefer it when the interrupted run is still addressable. Note
what it does not cover: only successful calls are cached, so every agent that FAILED re-runs live. After an
account-level outage that is most of the run, and the replay looks like a phase re-running rather than a
hand-off. `startPhase` is the blunter instrument for when that is not what you want.

### The base tier

`baseModel` and `baseEffort` set what every agent runs at, and they are **independent of the session's own
model and effort**. A loop that silently changed tier because the operator had switched their own model
would produce a run nobody could compare against an earlier one, so the defaults are the tier this workflow
was tuned on rather than whatever the caller happens to be running. Every run logs its tier and says whether
it was caller-set or defaulted, so a transcript records what the run was measured at. An unknown value fails
the run at launch rather than being silently ignored.

A number of agents name their own model and effort, and those names are **absolute rather than relative to
the base**. On `haiku` at high effort: the snapshot, diff-count, resume-state, round-boundary, spec-changes
probe, both status writers, and the growth measurement. On `sonnet` at high effort: `init`, the conventions
pass, the checklist verifier, and site expansion. Everything else takes the base.

The pairing is deliberate. A cheap model is not the same request as a shallow one: these agents sit on a
small model because their work is mechanical and well specified, and on high effort because getting it
wrong corrupts a round's bookkeeping quietly rather than loudly. The consequence of keeping the names
absolute is that the tiering is only coherent while the base sits at or above `sonnet`: lowering the base
under a hard-coded model would raise that agent above the base rather than below it.

The retry path is the one place a model changes on its own. After two failures an agent is retried on
`sonnet`, because a 529 is usually capacity-pool-specific and a lens completing on a lower tier beats a lens
dropped for the round. Every fallback is logged, so a round certified clean partly on a fallback is visible
in the transcript.

**Reading a failed run's tier distribution needs care, because the fallback distorts it.** A measured run
that hit the weekly cap mid-flight recorded 20 of 26 `sonnet` calls failing against 20 of 104 on `opus`,
which reads as the smaller model being unreliable. It is an artifact: `sonnet` is reached almost only AS a
fallback retry, so it is sampled exclusively from already-failing contexts and can never look healthy.
`haiku`, which names its own model and is never a fallback target, failed zero times in the same run. Every
one of those failures was `rate_limit`.

Read the timing before the distribution. In that run every one of the 40 failures fell inside a 72-second
window, after 3h49m of clean running across all three models and with normal operation resuming five
minutes later. Roughly ten agents were in flight and each spent its whole retry budget inside that window,
because the retries are fast relative to a short rate-limit window. That is a transient to ride out rather
than a per-agent fault, and neither the model nor the effort level distinguishes it: an earlier run at the
same tier recorded none.

## Changing an argument on a run in flight

| Situation | What to do |
|:--|:--|
| Run is live, changing one of the ten mergeable arguments | Write `scratchpad/cp-args/<runTag>.json`. Do not stop the run; it takes effect at the next round boundary. Only `maxFixGroups`, `fixDesignDepth`, `lockSpecChanges`, `maxExpansions`, `skipExpansion`, `standingContextTarget`, `standingContextTrigger`, `compactAtLines`, `compactGrowthLines` and `introspectEvery` merge in flight. Every other argument needs a relaunch, the review-round budgets included, so recovering from `spec-not-converged` means relaunching with a higher `maxSpecReviewRounds` |
| Run is live, changing an `anchored` argument | `TaskStop`, then relaunch with `resumeState: true` and the new arguments |
| Run died, nothing changed | Relaunch with `{scriptPath, resumeFromRunId}` |
| Run died, only `forward` arguments changed | Relaunch with `{scriptPath, resumeFromRunId}` and the new arguments |
| Run died, any `anchored` argument changed | Relaunch fresh with `resumeState: true` and the new arguments |

A wrong choice costs tokens, never correctness. `resumeFromRunId` after an anchored change busts the journal cache and re-does that work under the new argument. `resumeState` after only a forward change relaunches fresh and continues from the recorded round. An anchored key written into the override file is rejected by the whitelist and logged. The script compares the recorded arguments at startup and names any anchored one that changed, so a caller who changed one by accident finds out.

Report the `runTag` and the override path when you launch, so the user has the affordance without asking.

## Procedure

### Step 1: assemble the inputs

1. A path under `proposals/` means **review** mode. Otherwise the mode is **new** and the argument plus the conversation is the problem statement.
2. Compute `repoRoot`, `date`, and `exemplar` (the highest-numbered other proposal).
3. New mode: read the spec sections and code the problem names, so `context` carries concrete citations, and compute `nextNumber` from the highest existing `NNNN`.
4. Review mode: gather a short `context` of the spec sections and packages the proposal touches, by grepping for its main identifiers.

### Step 2: run the workflow

Invoke by **path**, never by name: a name resolves to a cached copy, so a run launched by name after an edit executes the previous version.

```json
{
  "mode": "review",
  "baseModel": "opus",
  "baseEffort": "high",
  "proposalPath": "proposals/0081_fix_slug",
  "date": "2026-08-31",
  "exemplar": "proposals/0080_fix_other",
  "repoRoot": "/abs/path",
  "context": "…"
}
```

Agents do NOT inherit the session's model or effort: the tier is `baseModel` and `baseEffort`, defaulting to `opus` at `medium`, and it is pinned so two runs of the same proposal stay comparable. Raise `baseEffort` to `high` when reviewer quality is what decides whether the loop converges on truth or on exhaustion, which is most runs that matter.

### Step 3: interruptions and non-convergence

- On interruption, follow the table above rather than reflexively resuming.
- On `introspection.stoppedBy`, apply the automatic-restart rule.
- On a loop exhausting its budget without a clean sweep, read `review.loops`: each records its rounds, sweeps, and retired set. Counts falling with the retired set growing means raise the budget and resume. Counts flat, or one lens reviving on every sweep, means stop and report: a lens that revives every sweep is usually pointing at a design contradiction the loop cannot fix by editing prose.

### Step 4: report

1. Run `git status --porcelain` and confirm the only changes are inside the proposal directory, plus the reference retargeting if a migration ran. Restore anything else and report the violation.
2. Report the path, the title, what validation refuted, what the challenge dropped, whether each loop converged, the rounds, and the findings fixed. Report `review.loops[].specTouched` when the non-spec loop edited the spec staging.
3. Report `decisionsResolved` and `decisionsLeftToHuman`. The first names each decision the run closed, with its disposition, its citation, and the authority that resolved or withdrew it, because a withdrawal citing a falsification panel and one citing nobody render identically in the file. The second is what the human still has to answer. `decisions.rechecks` and `rechecks.stop` say whether a recheck ran and whether a budget stopped the run with a lane's staging unreviewed.
4. A run the introspection pass stopped returns status `stopped-halt` or `stopped-reframe`; report it as stopped with its findings open rather than as reviewed.
5. On convergence the status is `Reviewed`. The next step is sign-off, which a human records as `Approved`, after which `implement-proposal` runs the sequence.
6. Do not apply any staged edit. This workflow does commit: it commits the proposal directory before each firing of the open-decisions-and-impact-review phase, because that phase reads the tree to tell what it changed rather than taking its agents' word for it. Commit nothing else unless asked.

## Maintenance

The workflow is canonical at `.claude/workflows/change-proposal.js`; this file carries the procedure and the rationale. The behavioural tests are `.claude/tests/change-proposal.test.mjs`; run `node .claude/tests/run.mjs`. When a convergence run surfaces a confirmed error class this file does not list, add it here and to the lens that owns it. Keep the finding bar's exclusions intact: they are what stops the loop converging on nitpicks.
