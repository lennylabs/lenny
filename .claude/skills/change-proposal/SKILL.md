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
  NNNN_kind_slug.summary.md                   what changes, goals, non-goals, fixed decisions,
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

**The spec loop runs a smaller pool.** It drops the `test-coverage` lens (`open-decisions` runs in both, because a decision staged in the spec half is still a decision), since the tests a change needs are staged in the non-spec half, so spec convergence certifies nothing about test coverage.

**The reconciliation does not run after an introspection stop.** A `halt` or `reframe` ends the run where it stands and nothing is reconciled.

**The non-spec loop does not start on a spec staging that is still moving.** When the spec loop exhausts its budget without converging, the run reconciles, then stops with status `spec-not-converged`, naming the rounds, the budget, and what the loop was still finding. Raising `maxSpecReviewRounds` and resuming is the operator's call; spending the second budget reviewing against staging that is still open is not a decision the run makes silently. `allowNonSpecOnUnconvergedSpec` overrides this.

**The spec fixer repairs what its own edits falsify in the non-spec staging.** It may correct a statement already written in `non-spec-changes.md` that one of its own spec edits just made false, in the same edit, and only where that file already has content. The trigger is always a spec finding: it never goes looking there, may not author a staged change because a spec edit implies one is needed, and may not touch a defect that is wrong on its own terms. The lenses are unchanged and are told not to file such a statement as a finding, because it is the consequence of an edit rather than a finding of its own. The checklist is deliberately excluded and its drift stays with the reconciliation pass.

Each loop keeps its own rounds, retired set, sweeps, and convergence, because a lens satisfied by the spec staging has said nothing about the code staging. The refuted-findings memory and the round history stay run-wide, so a finding refuted in the first loop is not re-litigated in the second.

Convergence is unchanged: a lens that finds nothing retires, a full sweep of the pool runs when every lens has retired, and a clean complete sweep converges. `operational` and `fresh` were once a second pool with their own schedule, one rotating in per round. That is a second mechanism for the job retirement already does, and the worse of the two, because it withheld a lens on a round number rather than on evidence. It also produced a lens that had never run, which cannot retire, which blocks the sweep, so a clean proposal spent a whole round discharging one lens: a measured run went thirteen lenses, then `fresh` alone, then a fourteen-lens sweep. The singleton round costs a snapshot, a dedup, the verifiers and a boundary whatever it contains. A round now **closes before it may certify**, because a round whose bookkeeping did not complete left its log unmerged and the next round without a snapshot.

### Open decisions

One lens owns every decision the proposal has not closed, and it is the only lens permitted to file on a
section recording decisions for the human reviewer. Every other lens leaves those sections alone, which is
what stops a dozen reviewers re-litigating one decision every round. The review bar is built per lens for
this one clause, so each lens reads a single rule stated positively about a section that either is or is
not its own. A shared string carrying an "unless your lens is X" carve-out would make every other lens
read an exception that does not apply to it and make the one lens that needs it read the prohibition
first, and both are self-identification steps that can fail. The cost of that immunity, before this
lens existed, was that a decision parked in the open-decisions section could go stale, be answered
elsewhere, or ask the wrong question, and nothing in the loop was allowed to notice.

**An open decision is the whole of its subject.** A decision the proposal records as settled is not the
lens's, and it may not re-open one, re-argue it, or file a finding that it was decided wrongly. That covers
the settled-decisions section, a non-goal recorded with its reason, and a historical pass record. Those are
the record of work already done, and the argument for reopening one always reads well, because once a
decision closed nobody kept writing down the counter-argument. The one exception is cascade: when an answer
available to an open decision would falsify a settled one, that goes in the open decision's own `cascades`
field, naming which answer falsifies what. It never becomes a decision of its own, since nobody is asking
whether the settled decision was right; the cascade is part of what an answer costs.

Decisions live in four places and the lens inventories all four before judging any: the open-decisions
section, a detailed-design item still stated as a choice rather than a constraint, an `IMPLEMENTOR'S CHOICE:`
marker, and an unclosed `OPEN` in the review log. The last is the one that leaks, because the human never
reads the review log. A decision that appears in one of these and is settled elsewhere counts as open, since
the proposal disagrees with itself about whether it is decided; resolving it means deleting the open
statement and citing the settled one.

**It elaborates before it determines.** The lens works in four steps and may not form a determination before
the last: inventory every decision without judging any, elaborate one at a time by opening the primary
sources and quoting the load-bearing sentence, interrogate by writing the questions a skeptical reviewer
would ask about the ground rather than the choice and answering each from a file opened in the pass, and
only then determine. At least one question per decision must be one whose answer would kill the answer the
agent is drifting toward, and a question that cannot be answered is a result rather than a gap: it is
either the fact that would reverse the decision or the reason the decision is the human's.

The third step is the one that earns the lens its cost. On a measured run the output was produced without
it, from five agents' summaries of the evidence rather than the evidence, and a single follow-up question
from a human reversed the recommendation on the spot. The human supplied no decision and nothing new beyond
one document finally read in full.

Because prompt text alone did not hold on this workflow before, the lens also has its own output schema,
which is the only enforcement here that is checked at the tool-call layer and retried on mismatch. Each
decision carries required fields for the quoted ground, the questions asked and answered, the case for and
against written at their best, the one fact that would flip it, and whether choosing differently changes
anything downstream. A schema field can still be back-filled to justify a conclusion already reached, which
is why the procedure states an order of work and the fields are its receipts. Neither half is sufficient
alone.

**The test, in order.** Resolve it when the answer is derivable from the tree, the spec, a landed proposal,
or the proposal's own staged text, citing where; this is the outcome to reach for, because a decision
parked for a human that the repository already answers costs a review cycle and teaches the reviewer the
list is noise. Leave it to the implementor when the choice is local, reversible, and has no consequence in
another section. Give it to the human only when it trades two goods the proposal cannot rank, changes what
the proposal is, commits the platform to a contract no evidence settles, decides another proposal's fate or
is decided by one, or accepts a named residual cost. A decision passes to the human only if a person could
answer it in one sitting without reading the whole proposal; one that fails that is restated until it
passes, or resolved.

**The blank is protected.** A properly marked `IMPLEMENTOR'S CHOICE:` exists so a proposal does not grow
hair, and this lens gets no new licence over it. The bar's existing rule is unchanged and is the whole of
what any lens may file: a marker with no constraint, a blank over something the format bars from
delegation, and an unmarked gap. Moving a decision off the human's list into a properly bounded blank is a
good outcome the lens is told to recommend, and over-specification stays a defect.

**The lens owns `## Open decisions` and reconciles it every round.** That section is a required part of the
summary and is the proposal's live answer to what is still undecided, rather than a list written once. The
lens looks for five drifts and files each as a finding: a decision living only in the review log or a
staged-changes bullet, where the human never sees it; an entry a later round answered; an entry whose
citation or quoted text has drifted; an entry asking a question the proposal does not face or asking it in
a form nobody could answer in one sitting; and an entry about a deliverable the proposal no longer stages.
Each decision carries what the lens did to its entry, so maintenance is recorded rather than assumed. Each entry states the question so it can be answered without reading the
proposal, the recommendation, the ground with citations, the alternatives and why each lost, what deciding
otherwise costs, a confidence, and where the decision came from. A decision resolved after being carried
there is withdrawn in place with its reason rather than deleted, because a list that quietly loses an entry
teaches its next reader the entry was answered. The reconciliation pass between the loops carries the spec
loop's routed decisions into that section, since the spec loop can route a decision to a human but the
human does not read the log it routes to.

**It reads sideways, and asks whether the cross-proposal effect is a decision at all.** No other lens reads
`proposals/`. When a staged change bears on another proposal, the first question is whether choosing
differently would change that effect. If every available answer affects it identically, the effect is
already settled by a deliverable nobody is questioning, so the output is a row in the impact section rather
than a question. That distinction is not academic: the first decision this lens was designed against was
routed to a human on the ground that it decided another proposal's fate, and it did not. The proposal's
central deliverable decided that, under every answer to the question being asked.

When the choice does change the outcome, the status decides who answers. An `Implemented` proposal is in
the tree and is unaffected. A `Draft` may be invalidated freely and is recorded rather than asked about. A
`Reviewed` or `Approved` proposal last reviewed within fourteen days goes to the human, because convergence
and human attention were recently spent on it; an older one is recorded with the note that it may have
drifted anyway. The status comes from `.claude/tools/proposal-status.mjs --json`, which carries the review
and approval dates for a folder-layout proposal. A legacy single-file proposal has none, so the fallback is
the file's last commit date, declared as such, because that is when someone last touched the file rather
than when it was reviewed.

`## Impact on other proposals` is a required part of the summary and is the only place the proposal asserts
anything about another proposal's continued validity. Non-goals states what this proposal will not do and
Dependencies states what it applies after, and neither may restate an impact. One proposal carrying two
claims about another is how they come to contradict each other: 0076 said both that whichever landed second
would rebase and that it was independent of 0075, and neither was true.

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

Lens keys: `citations`, `feasibility`, `edit-sites`, `mechanism`, `security`, `kubernetes`, `performance`, `reliability`, `client-surface`, `docs-alignment`, `test-coverage`, `open-decisions`, `applicability`, `operational`, `fresh`, and `plan-conformance` when `planPath` is set. Every lens is scheduled the same way: it runs unless it has retired, and when all have retired the whole pool runs again as a sweep. An unknown key in `startLenses` or `excludeLenses` is a hard error.

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
3. A run the introspection pass stopped returns status `stopped-halt` or `stopped-reframe`; report it as stopped with its findings open rather than as reviewed.
4. On convergence the status is `Reviewed`. The next step is sign-off, which a human records as `Approved`, after which `implement-proposal` runs the sequence.
5. Do not apply any staged edit, and do not commit unless asked.

## Maintenance

The workflow is canonical at `.claude/workflows/change-proposal.js`; this file carries the procedure and the rationale. The behavioural tests are `.claude/tests/change-proposal.test.mjs`; run `node .claude/tests/run.mjs`. When a convergence run surfaces a confirmed error class this file does not list, add it here and to the lens that owns it. Keep the finding bar's exclusions intact: they are what stops the loop converging on nitpicks.
