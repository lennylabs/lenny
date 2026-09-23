---
name: change-proposal
description: Write an adversarially validated change proposal under proposals/, staging spec edits and/or core-product or test-infrastructure code changes, from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec or implementation defect, contradiction, or gap, asks for a fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages its changes for sign-off; it never modifies spec/, pkg/, or docs/ itself.
argument-hint: <problem statement | path to notes | path to a proposal>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Change proposal writer and convergence loop

This skill produces a reviewed proposal under `proposals/` and converges it against the spec and the code. A proposal may stage spec edits, core-product code changes, test-infrastructure changes, or a combination. It has three modes sharing one workflow:

- **new**: the input is a problem statement. The workflow writes it to disk, validates it through several lenses, drafts it through several design stances, challenges each surviving change, writes the proposal files, and enters the review loops.
- **review**: the input is the path of an existing proposal. The workflow migrates it to the folder layout if it predates that, backfills what is missing, and enters the review loops.
- **redesign**: the input is a proposal path and a list of `focusAreas`. The workflow redesigns those areas first, applies the result, then reviews as `review` does. Use it when you already know which mechanism is wrong.

This file is the operator's guide. Why each stage is built the way it is, with the measurements behind it, is in `DESIGN.md` beside it and in the workflow source comments.

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

A proposal written as a single `NNNN_kind_slug.md` migrates to this layout the first time this skill or `implement-proposal` touches it. The migration makes its own commit at startup, which includes the legacy file's deletion and any references it retargeted outside the proposal directory. An `Implemented` or `Retired` proposal never migrates, because a landed proposal is a historical record. A failed migration ends the run.

`.summary.md` and `.spec-changes.md` are written by this skill alone. A fixer may correct a false citation or an evidence claim in `.problem-statement.md`, in the same edit as the section that restates it, but changing what the problem is takes a `reframe`. `.implementation-checklist.md` is seeded here, kept current by the review loops and the reconciliation pass, and then maintained by the implementor. `.deviations.md` is written by `implement-proposal` alone. Every agent appends to `.review-log.md` through a per-agent shard that the round boundary merges.

Read and write the status with `node .claude/tools/proposal-status.mjs <proposal> --field status`, never by parsing prose. A legacy proposal that reads `Retired` was superseded or withdrawn, and every consumer refuses it.

## Hard constraints

- The run edits only files inside the proposal's directory. Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages its changes and never applies them.
- All problem input is inline. Evidence comes from `spec/`, `schemas/`, `pkg/`, `cmd/`, `charts/`, and git history. Progress-tracking prose elsewhere is a lead to verify, never evidence.
- Prose follows `.claude/rules/doc-style.md`.
- New directory names are `NNNN_[new|fix]_<kebab-slug>`, where `NNNN` is the next free zero-padded number.
- Review findings are real errors only. Style preferences, optional improvements, and hypothetical hardening are refused by the materiality skeptic.

## What a run does

1. **Startup** (new mode). Init writes the proposal files, Validate tests the problem statement, Draft produces competing designs and consolidates them, Challenge questions each surviving change, and Write fills the files.
2. **Spec review loop.** The staged spec edits converge first, reviewed by a pool of lenses. It is skipped when the proposal intends no change under `spec/`, or with `skipSpecReview`.
3. **Reconciliation.** One pass rebuilds the deliverable index and the checklist's spec steps against the spec staging and applies the corrections the spec loop deferred. It runs after the spec loop whether or not it converged, and not after an introspection stop.
4. **Open-decisions-and-impact-review firing** (below).
5. **Non-spec review loop.** Its lenses read both change files, the summary, and the checklist as one document. It does not start on a spec staging that is still moving: a spec loop that exhausts its budget stops the run with status `spec-not-converged`, unless `allowNonSpecOnUnconvergedSpec` is set.
6. Another firing, then any **rechecks** (below), then the status write.

Each round of a loop runs the lenses that have not retired, deduplicates their findings, verifies each finding (materiality, then evidence), and fixes the confirmed ones. A lens that finds nothing retires. When every lens has retired, the whole pool runs again as a sweep, and a clean sweep converges. A dead lens, verifier, or fixer anywhere in a loop blocks that loop's convergence for the rest of the run, so raising the budget cannot fix it; relaunch with `resumeState` instead.

Fixers close each finding with the least text: a rule or a decision's reasoning is stated once and cited elsewhere, and the staged files say what to build while defences, history, and evidence go to the review log. The round log reports the lines each round's fixes added and removed.

### The open-decisions-and-impact-review phase

A subworkflow, `.claude/workflows/change-proposal-decisions.js`, fires after each loop and after each recheck. It is skipped, and recorded as `skipped-unchanged`, when nothing in the proposal changed since its last firing. Set `periodEvery` to also fire every that many non-spec rounds.

- It collects the decisions the proposal leaves to a human, the `IMPLEMENTOR'S CHOICE:` markers and unbounded blanks, the defects declared out of scope, and the proposal's impacts on other proposals under `proposals/`.
- For each item it proposes a disposition (`resolve`, `human`, `implementor`, `out-of-scope-stands`, `out-of-scope-wrong`, or an impact row), and one falsifier per item gates it. A refuted disposition is set aside rather than replaced. When a falsifier shows a decision left to the human is answerable, an agent designs the answer and a second falsifier gates that answer before it is applied.
- The surviving items are applied one at a time. A resolved decision is written into the staged changes as a requirement and leaves the summary. A decision that stays with the human is written so it can be answered without reading the proposal.
- An item the review loop later reverts is marked CONTESTED. An agent confirms the reversal against the staged files and the summary; a confirmed one is written into the summary's open decisions once and left to the human, and a false alarm is cleared. A contested item is never re-applied.
- It may edit both change files, the summary, the review log, and the problem statement's record, and the checklist only to keep its step-to-deliverable mapping true when an answer splits or renames a deliverable. Under `lockSpecChanges` the staged spec edits are out of bounds: a resolution that needs them is recorded for the operator with the edit it would have made, and you report it.
- **It commits the proposal directory before each firing**, because it reads what it changed from git rather than from its agents.

The phase state is saved to `scratchpad/cp-state/<runTag>/decisions-state.json`, with each applied item's text in `records/`, so a relaunch with `resumeState` does not re-collect or re-falsify what earlier runs settled. To relaunch with the state loaded without an agent, build a launch copy and launch that, with `resumeState: true`, instead of the workflow:

```
node .claude/tools/cp-state.mjs launch-copy scratchpad/cp-state/<runTag>/decisions-state.json \
  .claude/workflows/change-proposal.js scratchpad/cp-launch/<runTag>/change-proposal.js
```

The launch copy drops comment-only lines and refuses to write a copy over the Workflow tool's size limit; launching the workflow itself with `resumeState` also works. To run under a new `runTag` with an earlier run's state, copy `scratchpad/cp-state/<oldTag>/` to `scratchpad/cp-state/<newTag>/` first. A state saved before record files existed is converted with `node .claude/tools/cp-state.mjs migrate-records <state> <records dir>`.

### Rechecks

The run converges only when no lane's staging changed since that lane's last review. A firing, or a non-spec fixer editing the spec staging, can change a lane after its review:

- A spec edit after convergence runs a **recheck pair**, a `spec-recheck` then a `non-spec-recheck`, each on `maxRecheckRounds`. The second half is skipped (`skipped: "read-set-unchanged"`) when nothing it reads changed.
- A non-spec edit alone runs a **lone `non-spec-recheck`**.
- `maxRecheckPairs` and `maxNonSpecRechecks` bound them. Exhausting either stops the run with status `recheck-budget-exhausted`, naming the lane, the outstanding edit, and what to raise.

Under `lockSpecChanges` no pair runs. In the ordinary case no recheck runs at all.

### What the summary holds

`summary.md` carries these sections, in this order: `## Summary` (holding **Problem statement.**, **What changes.**, **Decisions.**, and **Watch out for.**), `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and `## Deliverable index`. Each open decision carries a stable identifier that later firings match it by. A resolved or withdrawn decision leaves the file and is reported in `decisionsResolved`.

### The review log

The review log carries `## Standing context` (`### Settled`, `### Open`, `### Traps`, and `### Deferred`), which every agent reads, and `## Ledger`, where each round's shards land. A compaction pass rewrites the standing context when it grows past `standingContextTrigger`, and the round boundary then moves the ledger to the archive. The tags are `DECISION`, `WATCHOUT`, `FACT`, `MISTAKE`, `UNVERIFIED`, `OPEN`, `DEFERRED`, `CORRECTS`, and `USEFUL`.

### Introspection

An introspection pass runs every `introspectEvery` rounds and when the churn counters wake it. In the default `advisory` mode, `healthy` continues and **any other verdict stops the run at once** with status `stopped-redesign`, `stopped-prune`, `stopped-reframe`, or `stopped-halt`, carrying the pass's reasoning and its proposed next steps. The stop is yours to act on, as the next section describes. `introspectMode: "acting"` instead puts every verdict to a panel of judges and executes redesigns and prunes inside the loop.

## Remedy and restart on a stop

A run that returns `introspection.stoppedBy` (status `stopped-redesign`, `stopped-prune`, `stopped-reframe`, or `stopped-halt`) has findings still open, and the verdict names the kind of remedy the pass asked for. It also stops short with `recheck-budget-exhausted` or `spec-not-converged`. In each case, decide first whether a person is needed, and when one is not, **apply the remedy yourself and relaunch without asking**.

**A person is needed**, so stop and ask, when any of these holds: `nextSteps.confidence` is `needs-human`; the remedy chooses between designs the summary's **Decisions.** or its open decisions leave to the human; the verdict is `reframe` and the remedy changes what the problem *is* rather than correcting its record; the proposed `rerunArgs` do not parse or name unknown arguments; or this invocation has already restarted twice.

**Otherwise:**

1. Read `stoppedBy.verdict`, `stoppedBy.reasoning`, `stoppedBy.hardSignals`, `nextSteps`, and the last rounds' `confirmedTitles`. State the diagnosis in one paragraph to the user before acting.
2. Pick the remedy the diagnosis calls for. A rule restated at several sites that the rounds keep re-synchronising is reduced to one normative statement with citations elsewhere. An over-specified section is pruned to a bounded `IMPLEMENTOR'S CHOICE`. A mechanism the rounds keep correcting is redesigned, by the `redesign` mode with `focusAreas` when the areas are clear, or by subagents you brief with the pass's reasoning when the edit is a restructure rather than a redesign. Prefer the remedy that removes text.
3. Apply it to the proposal directory only, review the edit with a fresh subagent that did not make it, and fix what it finds. Commit the proposal directory with a message naming the remedy, so the relaunched run's snapshots start from it.
4. Relaunch with `resumeState: true`, the `directives` this run was itself launched with, the pass's `rerunArgs` where they parse, and a `directives` entry stating what the stopped run diagnosed and what was done about it, so the new run's fixers do not rebuild what the remedy removed.
5. Report what was diagnosed, what was changed, and the arguments used.

At most **two** remedy-and-restart cycles per invocation; track the count yourself. On the third stop, put the question to the user with the three diagnoses side by side, because a run that stops three times on different remedies has a problem none of them named.

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
| `periodEvery` | forward | 0 | rounds between periodic firings of the open-decisions phase, counted at the non-spec loop's round boundary. `0`, the default, turns periodic firings off: the phase fires after each loop, and only when the proposal changed since its last firing |
| `humanReadings` | forward | 1 | independent readings of each open decision in the phase's sub-task 1. The falsifier is the adversarial check; `3` restores the unanimity join |
| `collectorModel`, `collectorEffort` | launch | `opus`, `low` | the model and effort the phase's single collectors run at. The falsifier stays on the base tier |
| `maxPeriodicFirings` | forward | 5 | the periodic firing's own budget; exhausting it is reported and stops that trigger alone, leaving every post-loop firing running |
| `maxRecheckPairs` | forward | 2 | how many `spec-recheck` plus `non-spec-recheck` pairs may run; exhausting it stops the run with the outstanding spec edit unreviewed |
| `maxNonSpecRechecks` | forward | 2 | how many lone `non-spec-recheck` loops may run, under the same reported stop |
| `maxRecheckRounds` | forward | 5 | round budget for each recheck loop, held apart from the two loop budgets above |
| `skipSpecReview`, `skipNonSpecReview` | launch | false | a skipped loop certifies nothing about its half, echoed in the result |
| `lockSpecChanges` | forward | false | the non-spec loop may never edit the spec staging; such a finding becomes an open decision |
| `allowNonSpecOnUnconvergedSpec` | forward | false | runs the non-spec loop even when the spec loop exhausted its budget; otherwise the run stops at `spec-not-converged` |
| `verifyOrder` | forward | `["material","evidence"]` | which skeptic short-circuits |
| `verifySequential` | forward | true | false restores both skeptics in parallel, as two agents |
| `verifyPrefilter` | forward | true | refutes a style-grounded rewording of commentary without a verifier; `false` sends every finding to the verifier |
| `verifyMode` | forward | `merged` | `merged`: one agent answers both skeptics' questions in `verifyOrder`, stopping at the first refusal. `split`: two agents |
| `deltaReads` | forward | true | in a partial round, a lens that read the whole proposal last round reads only what changed. `false` restores a full read every round |
| `maxFixGroups` | forward | 7 | the only cap on the fix split; group size is uncapped by design |
| `fixDesignDepth` | forward | `auto` | `shallow` forces the trivial path; `deep` forces the architect path |
| `maxExpansions` | forward | 12 | confirmed findings per round given a site-expansion pass. A finding the cap skipped, or whose pass died, is marked as NOT SEARCHED in the designer's and fixer's prompts, so absence of sites is never read as evidence there are none |
| `skipExpansion` | forward | false | turns site expansion off; the designer then sees only the sites the finding names |
| `introspectEvery` | forward | 5 | rounds between mandatory passes |
| `introspectGate` | forward | true | the warrant gate; a cadence wake ignores it either way |
| `introspectMode` | forward | `advisory` | `advisory`: `healthy` continues and any other verdict stops the run for the caller to correct. `acting`: panels on every verdict, redesign and prune executed in the loop |
| `introspectModel`, `introspectEffort` | launch | `opus`, `high` | what the pass and its judges run at |
| `haltWindow`, `haltRepeatTitle` | forward | 4, 3 | the informational hard signals a stop carries: rounds over which confirmed findings did not fall, and confirmations of one title |
| `directives` | anchored | none | strings carried into every lens, fix-design, and fixer prompt from round 1; how a relaunch carries what the stopped run learned |
| `judgesPerVerdict` | forward | 3 | panel size for non-healthy verdicts, in the `acting` mode |
| `judgesHealthy` | forward | 2 | panel size for `healthy`, in the `acting` mode |
| `falsificationBar` | forward | `conclusive` | `partial` makes the panel easier to convince |
| `standingContextTarget` | forward | 200 | what a compaction pass is asked to reach; raises itself when a pass cannot |
| `standingContextTrigger` | forward | 320 | when compaction becomes due, kept above the target so a pass buys real headroom |
| `compactAtLines` | forward | 2000 | a backstop on ledger length, not the trigger |
| `compactGrowthLines` | forward | 400 | has no effect; do not set it |
| `kind` | launch | `fix` | selects the `NNNN_[new/fix]_<slug>` directory segment in `new` mode |
| `lensPrompt` | anchored | none | appended to every review lens. This is the only route to them; there is no `prompts.review` key |
| `prompts` | anchored | `{}` | per-agent text, keyed by agent |
| `startLenses` | anchored | none | lens keys to lead with; every other begins retired and first reads in the sweep |
| `excludeLenses` | forward | none | lens keys removed entirely; convergence certifies nothing about those domains |
| `enableLenses` | forward | none | lens keys to switch back on from the disabled-by-default set (`feasibility`, `operational`) |
| `focusAreas` | launch | none | required in `redesign`: a slug or `{area, reason}` each |
| `churnWindow`, `churnMinFindings`, `churnStrikes` | forward | 6, 5, 3 | the churn detector's thresholds |
| `maxRedesigns`, `redesignReviewRounds` | forward | 2, 2 | the redesign budget |
| `maxPrunes` | forward | 2 | the prune budget; a section the run already pruned is not pruned again |
| `cacheScope` | launch | none | names the lens cache this run may read and write; empty means no cache and every lens reviews. Two runs sharing a scope share answers |
| `runTag` | anchored | the stem | namespaces the log shards, snapshots, cache, and state |
| `resumeState` | launch | false | continue a loop at its recorded round with its retired set, and load the decisions phase's saved state |
| `skipBootstrap` | launch | false | skip the backfill and the conventions pass, for a proposal a recent run already took through them; migration still runs |
| `decisionsFirst` | launch | false | fire the open-decisions phase before the spec review as well as after it |
| `impactWindow` | forward | 15 | how far, in proposal numbers, the impacts sweep reaches from this proposal |
| `firstCompactionAtLines` | forward | 400 | the ledger length at which the first compaction pass runs, before the standing context can trigger one |

`prompts` keys: `validate.<lens>`, `validate.consolidate`, `draft.<stance>`, `draft.consolidate`, `challenge`, `write`, `bootstrap`, `conventions`, `handoff`, `expand-sites`, `fix-plan`, `fix-design`, `fix-design-reconcile`, `fix`, `compact`, `introspect.gate`, `judge.<verdict>`. The introspection pass itself takes no injected text. To add text to every review lens, use `lensPrompt`. Injected text is framed as added context that lowers no bar.

Lens keys: `single-source`, `citations`, `feasibility`, `edit-sites`, `mechanism`, `security`, `kubernetes`, `performance`, `reliability`, `client-surface`, `docs-alignment`, `test-coverage`, `applicability`, `operational`, `fresh`, and `plan-conformance` when `planPath` is set. `feasibility` and `operational` are off by default and `enableLenses` switches them on. Convergence certifies nothing about a disabled or excluded lens's domain. An unknown key in `startLenses` or `excludeLenses` is a hard error.

### The lens cache

A lens can be served its own earlier answer instead of reviewing again. It is **off unless `cacheScope` names one**, and it stays off in almost every case.

- **Set it in one case only**: a run that died part-way through a round, relaunched to finish that round. Pass the string the dead run used, and the lenses that already answered return their answers.
- **Do not set it** for a fresh review of a proposal reviewed before, after editing a lens's text, `lensPrompt`, or `context`, or after the tree moved under the proposal. The cache key covers the proposal files and the base tier, and neither the tree nor the prompt, so in each of these cases it replays a stale answer and the loop reports a convergence nobody reviewed.
- Name the scope for the run rather than the proposal. Characters outside letters, digits, underscore, and dash are stripped.

### Starting partway through

`startPhase` names the first phase to run and skips everything before it. Nothing checks that the skipped phases were done, and the run logs that it assumed so. It is refused in `new` mode for anything but the default. `resumeFromRunId` is finer-grained: it replays a run's cached agent calls and continues at the exact interruption point, so prefer it when the interrupted run is still addressable. It caches only successful calls, so after an account-level outage most of the run re-runs live.

### The base tier

`baseModel` and `baseEffort` set what every agent runs at, **independent of the session's own model and effort**, so two runs of one proposal stay comparable. Every run logs its tier. Some agents name their own model and effort, and those names are absolute: `haiku` at high effort for the mechanical bookkeeping agents, and `opus` at low effort for `init`, the conventions pass, the checklist verifier, site expansion, and the decisions phase's triage and single collectors. Keep the base at or above `opus` at low effort, or those agents end up above it. After two failures an agent is retried on `sonnet`, and every fallback is logged. A burst of failures inside a minute or two is usually a rate-limit window to ride out rather than a model fault.

## Changing an argument on a run in flight

| Situation | What to do |
|:--|:--|
| Run is live, changing a mergeable argument | Write `scratchpad/cp-args/<runTag>.json`. Do not stop the run; it takes effect at the next round boundary. Only `maxFixGroups`, `fixDesignDepth`, `lockSpecChanges`, `maxExpansions`, `skipExpansion`, `standingContextTarget`, `standingContextTrigger`, `compactAtLines`, `firstCompactionAtLines`, `compactGrowthLines` and `introspectEvery` merge in flight. Every other argument needs a relaunch, the review-round budgets included, so recovering from `spec-not-converged` means relaunching with a higher `maxSpecReviewRounds` |
| Run is live, changing an `anchored` argument | `TaskStop`, then relaunch with `resumeState: true` and the new arguments |
| Run died, nothing changed | Relaunch with `{scriptPath, resumeFromRunId}` |
| Run died, only `forward` arguments changed | Relaunch with `{scriptPath, resumeFromRunId}` and the new arguments |
| Run died, any `anchored` argument changed | Relaunch fresh with `resumeState: true` and the new arguments |
| Run died PART-WAY THROUGH A ROUND and you are relaunching to finish it | Add `cacheScope`, set to the string the dead run used, so the lenses that already answered do not repeat the work. Set it in no other situation: see **The lens cache** |

A wrong choice costs tokens, never correctness. `resumeFromRunId` after an anchored change busts the journal cache and re-does that work under the new argument. `resumeState` after only a forward change relaunches fresh and continues from the recorded round. An anchored key written into the override file is rejected by the whitelist and logged. The script compares the recorded arguments at startup and names any anchored one that changed, so a caller who changed one by accident finds out.

Report the `runTag` and the override path when you launch, so the user has the affordance without asking. Report the `cacheScope` too when you set one, because a later run reusing it by accident is the failure **The lens cache** describes.

## Procedure

### Step 1: assemble the inputs

1. A path under `proposals/` means **review** mode. Otherwise the mode is **new** and the argument plus the conversation is the problem statement.
2. Compute `repoRoot`, `date`, and `exemplar` (the highest-numbered other proposal).
3. New mode: read the spec sections and code the problem names, so `context` carries concrete citations, and compute `nextNumber` from the highest existing `NNNN`.
4. Review mode: gather a short `context` of the spec sections and packages the proposal touches, by grepping for its main identifiers.
5. Review and redesign modes: **measure the review log's standing context before launching.** Every reviewing, designing, and fixing agent reads that section, so its size is paid once per agent. Run `awk '/^## Standing context/{s=1} /^## Ledger/{s=0} s' <stem>.review-log.md | wc -lw`. Above about 600 lines, hard-compact it first, because the in-run compaction pass cannot: it may not drop an `OPEN`, an `UNVERIFIED`, or a `MISTAKE`, and when it misses its target the target rises to the size it found so an oversized section stays that size for the whole run. The hard compaction is one subagent: append the whole section verbatim to `<stem>.review-log-archive.md` under a dated heading, then rewrite the section against the CURRENT staging to at most 450 lines, keeping the traps, the verified code facts, the single-home rules, the decisions with their rejected alternatives, and the entries still open or deferred against the current text, preserving entry ids, and leaving `## Ledger` untouched. Commit the result with the proposal before launching.

### Step 2: run the workflow

Invoke by **path**, never by name: a name resolves to a cached copy, so a run launched by name after an edit executes the previous version.

```json
{
  "mode": "review",
  "baseModel": "opus",
  "baseEffort": "high",
  "proposalPath": "proposals/NNNN_fix_slug",
  "date": "2026-08-31",
  "exemplar": "proposals/MMMM_fix_other",
  "repoRoot": "/abs/path",
  "context": "…"
}
```

Agents do NOT inherit the session's model or effort: the tier is `baseModel` and `baseEffort`, defaulting to `opus` at `medium`, and it is pinned so two runs of the same proposal stay comparable. Raise `baseEffort` to `high` when reviewer quality is what decides whether the loop converges on truth or on exhaustion, which is most runs that matter.

### Step 3: interruptions and non-convergence

- On interruption, follow the table above rather than reflexively resuming.
- On `introspection.stoppedBy`, apply the automatic-restart rule.
- On a loop that did not converge, read `review.loops` first for `reviewersFailed` and `fixersFailed`: a dead agent blocks convergence, and the remedy is a relaunch with `resumeState` rather than a larger budget. Otherwise each loop records its rounds, sweeps, and retired set. Counts falling with the retired set growing means raise the budget and resume. Counts flat, or one lens reviving on every sweep, means stop and report: a lens that revives every sweep is usually pointing at a design contradiction the loop cannot fix by editing prose.

### Step 4: report

1. Run `git status --porcelain` and confirm the only changes are inside the proposal directory, plus the reference retargeting if a migration ran. Restore anything else and report the violation.
2. Report the path, the title, what validation refuted, what the challenge dropped, whether each loop converged, the rounds, and the findings fixed. Report `review.loops[].specTouched` when the non-spec loop edited the spec staging.
3. Report `decisionsResolved` and `decisionsLeftToHuman`. The first names each decision the run closed, with its disposition, its citation, and the authority that resolved or withdrew it, because a withdrawal citing a falsification panel and one citing nobody render identically in the file. The second is what the human still has to answer. `decisions.rechecks` and `rechecks.stop` say whether a recheck ran and whether a budget stopped the run with a lane's staging unreviewed.
4. Report the result's `status` truthfully, and do not read `reviewed` or `written` as converged:
   - `reviewed` (review and redesign modes) and `written` (new mode) are returned whether or not the loops converged; `review.converged`, `specGate`, and `rechecks.stop` say whether they did. A new-mode run that the spec gate or a recheck budget stopped still returns `written`, so apply the remedy section on those two fields rather than on `status`.
   - `spec-not-converged` and `recheck-budget-exhausted` are the stops the remedy section covers.
   - `stopped-redesign`, `stopped-prune`, `stopped-reframe`, and `stopped-halt` are introspection stops: report the run as stopped with its findings open.
   - `migration-failed`, `not-viable`, `no-change-needed`, and `interrupted` end a run before it reviews anything; report the reason the result carries.
5. On convergence the status is `Reviewed`. The next step is sign-off, which a human records as `Approved`, after which `implement-proposal` runs the sequence.
6. Do not apply any staged edit. This workflow does commit: it commits the proposal directory before each firing of the open-decisions-and-impact-review phase, because that phase reads the tree to tell what it changed, and a migration commits its own changes at startup. Commit nothing else unless asked.

## Maintenance

The workflows are canonical at `.claude/workflows/change-proposal.js` and `.claude/workflows/change-proposal-decisions.js`. `DESIGN.md` records why each stage is built as it is, and says how to edit and test the workflows.
