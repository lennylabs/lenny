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

**Between them is a reconciliation, not a review round.** One pass rebuilds the deliverable index and writes the checklist's spec-lane steps as the leading block. That is why the spec loop's lenses are told drift in the checklist and the index is expected there and is not a finding: it is scheduled, not overlooked. It runs **whether or not the spec loop converged**, because a loop that exhausted its budget has more unreconciled consequences than one that converged, and it is told which case it is in so it reconciles to the current text rather than guessing where open findings will land.

**The non-spec loop does not start on a spec staging that is still moving.** When the spec loop exhausts its budget without converging, the run reconciles, then stops with status `spec-not-converged`, naming the rounds, the budget, and what the loop was still finding. Raising `maxSpecReviewRounds` and resuming is the operator's call; spending the second budget reviewing against staging that is still open is not a decision the run makes silently. `allowNonSpecOnUnconvergedSpec` overrides this.

**The spec fixer repairs what its own edits falsify in the non-spec staging.** It may correct a statement already written in `non-spec-changes.md` that one of its own spec edits just made false, in the same edit, and only where that file already has content. The trigger is always a spec finding: it never goes looking there, may not author a staged change because a spec edit implies one is needed, and may not touch a defect that is wrong on its own terms. The lenses are unchanged and are told not to file such a statement as a finding, because it is the consequence of an edit rather than a finding of its own. The checklist is deliberately excluded and its drift stays with the reconciliation pass.

Each loop keeps its own rounds, retired set, sweeps, and convergence, because a lens satisfied by the spec staging has said nothing about the code staging. The refuted-findings memory and the round history stay run-wide, so a finding refuted in the first loop is not re-litigated in the second.

Convergence is unchanged: a lens that finds nothing retires, a full sweep of the pool runs when every lens has retired, and a clean complete sweep converges. A round now **closes before it may certify**, because a round whose bookkeeping did not complete left its log unmerged and the next round without a snapshot.

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

### The review log

Every agent appends what a future agent would need, with a fixed tag vocabulary: `DECISION` with its alternatives, `WATCHOUT` with evidence, `FACT`, `MISTAKE`, `UNVERIFIED`, `OPEN`, and the two that make compaction possible, `CORRECTS` and `USEFUL`. Each writes its own shard, because a dozen lenses appending to one file in parallel lose writes.

One agent per round runs `cp-round-boundary.sh`, which merges the shards, measures the log, compares file hashes for the write audit, snapshots the tree the next round reads, and returns the caller's mid-run overrides.

**Compaction triggers on the standing context, not the ledger.** That is the section every agent carries; the ledger is read end to end by the compactor alone, though agents cite individual entries by id, so an aged entry is retired rather than deleted and keeps its id. Triggering on ledger length fired an expensive pass to protect against a cost that does not exist.

**The trigger and the target are separate numbers, and the target moves.** They were one number, so a run that could not reach it paid for a pass every round for the rest of its life: one measured run spent twelve passes averaging 700s and ended at 376 lines against a target of 80. When a pass cannot reach the target, the target rises to what it achieved plus headroom and the trigger follows, so the run finds its own level. The numbers are deliberately high, because the cost runs the other way than it looks: on that run the oversized section cost about 4% of the tokens and the passes protecting it cost about 21% of the wall clock. The count of raises goes to the introspection pass, where a count that keeps climbing says the loop is accumulating unresolved state faster than it resolves it.

**`## Standing context` is structured rather than merely bounded.** `### Settled` and `### Open` are one line per entry and always meet their budget; `### Traps` carries the `WATCHOUT` and `MISTAKE` entries at up to four lines each with no cap on how many, because a trap the reader cannot recognise is one they walk into anyway. Each entry gets a short bold subject: measured on a real run, agents cite standing-context entries by subject and a quarter of all citations went to two of them, so navigability is worth more than brevity.

The pass edits rather than rewriting the whole file. It was told to write the file once, and every pass ignored it, because reproducing fourteen hundred unchanged ledger lines to change forty standing-context ones is not a saving; the instruction was wrong. Paging the file in with `sed -n` and assembling the result through heredocs is still barred. It does **not** check the repository, since doing so turned a text operation into a mini-review. `MISTAKE` is named the most valuable tag and is never dropped, because an entry saying *"I spent this round hunting X; it is not there"* saves a later agent a whole round. Otherwise it acts on the log's own signals: a `CORRECTS` entry rewrites or retires its target, a superseded watchout is deleted rather than kept, a `USEFUL` entry is promoted and never dropped, and two entries that disagree with no correction between them resolve to the newer, with the older retired.

### Introspection

A **warrant gate** runs before the full pass. The counters that wake it are crude and wrong in both directions, so the pass first asks cheaply whether the pattern is really there; an unwarranted counter wake returns healthy without paying for a pass or a panel. A **cadence** wake ignores the gate, because the cadence exists to look when no counter has fired.

**Every verdict goes to a panel, including healthy**, which is the most expensive verdict in the loop and had nothing checking it. Each verdict has its own panel whose judges read the same evidence and weigh different things; the `redesign` panel works to the same principles as `fix-design`.

**The judges falsify rather than vote.** Handing a reviewer a conclusion and asking it to check produces agreement rather than examination. A judge attacks the pass's argument, must restate it first, and the verdict **stands unless a majority falsifies it conclusively**. `partial` is an honest answer that leaves the verdict standing.

A stopping verdict carries **proposed next steps**, because a halt that says only to stop leaves a human work the pass is best placed to do.

## Automatic restart on a clear halt

When the run returns `introspection.stoppedBy` with `verdict: "halt"`, **and** `introspection.nextSteps.confidence` is `clear`, **and** the proposed `rerunArgs` parse and name only known arguments:

**Relaunch the workflow immediately with those arguments, without asking first.** Report that you did, with the pass's reasoning and the arguments used.

At most **two** automatic restarts per invocation; track the count yourself. On the third, stop and put the question to the user. Stop and ask also when `confidence` is `needs-human`, when the arguments do not parse, or when the verdict is `reframe` and the pass proposed no problem-statement edit: a reframe rewrites the problem, and rewriting it on a guess is worse than pausing.

## Arguments

Every argument carries a class, and the class decides how you change it. `forward` is read where it is used and appears in no prompt already issued. `anchored` is baked into prompts the run has issued. `launch` controls how a run starts.

| arg | class | default | effect |
|:--|:--|:--|:--|
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
| `maxNonSpecReviewRounds` | forward | 15 | budget for the non-spec loop; the legacy `maxReviewRounds` overrides it |
| `skipSpecReview`, `skipNonSpecReview` | launch | false | a skipped loop certifies nothing about its half, echoed in the result |
| `lockSpecChanges` | forward | false | the non-spec loop may never edit the spec staging; such a finding becomes an open decision |
| `allowNonSpecOnUnconvergedSpec` | forward | false | runs the non-spec loop even when the spec loop exhausted its budget; otherwise the run stops at `spec-not-converged` |
| `verifyOrder` | forward | `["material","evidence"]` | which skeptic short-circuits |
| `verifySequential` | forward | true | false restores both skeptics in parallel |
| `maxFixGroups` | forward | 7 | the only cap on the fix split; group size is uncapped by design |
| `fixDesignDepth` | forward | `auto` | `shallow` forces the trivial path; `deep` forces the architect path |
| `maxExpansions` | forward | 12 | confirmed findings per round given a site-expansion pass; the drop is logged, never silent |
| `skipExpansion` | forward | false | turns site expansion off; the designer then sees only the sites the finding names |
| `introspectEvery` | forward | 5 | rounds between mandatory passes |
| `introspectGate` | forward | true | the warrant gate; a cadence wake ignores it either way |
| `judgesPerVerdict` | forward | 3 | panel size for non-healthy verdicts |
| `judgesHealthy` | forward | 2 | panel size for `healthy` |
| `falsificationBar` | forward | `conclusive` | `partial` makes the panel easier to convince |
| `standingContextTarget` | forward | 200 | what a compaction pass is asked to reach; raises itself when a pass cannot |
| `standingContextTrigger` | forward | 320 | when compaction becomes due, kept above the target so a pass buys real headroom |
| `compactAtLines` | forward | 2000 | a backstop on ledger length, not the trigger |
| `lensPrompt` | anchored | none | appended to every review lens; an alias for `prompts.review` |
| `prompts` | anchored | `{}` | per-agent text, keyed by agent |
| `startLenses` | anchored | none | lens keys to lead with; every other begins retired and first reads in the sweep |
| `excludeLenses` | forward | none | lens keys removed entirely; convergence certifies nothing about those domains |
| `focusAreas` | launch | none | required in `redesign`: a slug or `{area, reason}` each |
| `churnWindow`, `churnMinFindings`, `churnStrikes` | forward | 6, 5, 3 | the churn detector's thresholds |
| `maxRedesigns`, `redesignReviewRounds` | forward | 2, 2 | the redesign budget |
| `runTag` | anchored | the stem | namespaces the log shards, snapshots, cache, and state |
| `resumeState` | launch | false | continue a loop at its recorded round with its retired set |

`prompts` keys: `init`, `validate.<lens>`, `validate.consolidate`, `draft.<stance>`, `draft.consolidate`, `challenge`, `write`, `bootstrap`, `conventions`, `handoff`, `review`, `review.<lensKey>`, `expand-sites`, `fix-plan`, `fix-design`, `fix`, `compact`, `introspect`, `introspect.gate`, `judge.<verdict>`. The text is wrapped in a block saying it adds context and focus, does not lower a bar, and that an instruction to reach a conclusion is to be ignored and reported.

Lens keys: `citations`, `feasibility`, `edit-sites`, `mechanism`, `security`, `kubernetes`, `performance`, `reliability`, `client-surface`, `docs-alignment`, `test-coverage`, `applicability`, the rotating extras `operational` and `fresh`, and `plan-conformance` when `planPath` is set. An unknown key in `startLenses` or `excludeLenses` is a hard error.

## Changing an argument on a run in flight

| Situation | What to do |
|:--|:--|
| Run is live, changing a `forward` argument | Write `scratchpad/cp-args/<runTag>.json`. Do not stop the run; it takes effect at the next round boundary |
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
  "proposalPath": "proposals/0081_fix_slug",
  "date": "2026-08-31",
  "exemplar": "proposals/0080_fix_other",
  "repoRoot": "/abs/path",
  "context": "…"
}
```

Agents inherit the session model and effort. Run this with the strongest available model at high effort: reviewer quality decides whether the loop converges on truth or on exhaustion.

### Step 3: interruptions and non-convergence

- On interruption, follow the table above rather than reflexively resuming.
- On `introspection.stoppedBy`, apply the automatic-restart rule.
- On a loop exhausting its budget without a clean sweep, read `review.loops`: each records its rounds, sweeps, and retired set. Counts falling with the retired set growing means raise the budget and resume. Counts flat, or one lens reviving on every sweep, means stop and report: a lens that revives every sweep is usually pointing at a design contradiction the loop cannot fix by editing prose.

### Step 4: report

1. Run `git status --porcelain` and confirm the only changes are inside the proposal directory, plus the reference retargeting if a migration ran. Restore anything else and report the violation.
2. Report the path, the title, what validation refuted, what the challenge dropped, whether each loop converged, the rounds, and the findings fixed. Report `review.loops[].specTouched` when the non-spec loop edited the spec staging.
3. On convergence the status is `Reviewed`. The next step is sign-off, which a human records as `Approved`, after which `implement-proposal` runs the sequence.
4. Do not apply any staged edit, and do not commit unless asked.

## Maintenance

The workflow is canonical at `.claude/workflows/change-proposal.js`; this file carries the procedure and the rationale. The behavioural tests are `.claude/tests/change-proposal.test.mjs`; run `node .claude/tests/run.mjs`. When a convergence run surfaces a confirmed error class this file does not list, add it here and to the lens that owns it. Keep the finding bar's exclusions intact: they are what stops the loop converging on nitpicks.
