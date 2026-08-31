# Plan: rework the change-proposal and implement-proposal pipeline

Written 2026-08-31. Scope: `.claude/workflows/change-proposal.js`,
`.claude/workflows/implement-proposal.js`,
`.claude/workflows/implement-proposal-build.js`, the two matching skills, the
`spec/` guard hook in `.claude/settings.json`, the `proposals/` layout, and the
workflow test harness under `.claude/tests/`.

This plan is a design and a sequencing document. It states what each change is,
why, what it touches, and how it is tested. **§13** records the seven decisions
taken while writing it, `D1`–`D7`, all settled.

---

## 1. What is being changed, in one page

| Area | Today | After |
|:--|:--|:--|
| Proposal on disk | one `proposals/NNNN_kind_slug.md` | a directory holding eight files with fixed roles; existing proposals migrate lazily, one at a time, when a workflow next touches them |
| Status | a prose bullet in the document | `.status.md` with a typed frontmatter |
| Execution order | spec is a phase; the checklist's spec lane is decorative | one sequence: the checklist, spec steps included. One lane per step; spec steps lead by default, an interleave is justified on its line (§9.1) |
| `spec/` guard | greps every proposal for `Status: Approved` | a per-step lease naming one proposal and the files that step targets |
| Review loop | one loop over the whole document | two loops: spec first, then non-spec |
| Verification of a finding | two skeptics in parallel | materiality first, evidence only if it survives |
| Fixing | one agent for every confirmed finding | plan into groups, design each group, fix per group |
| Lens crash recovery | the whole round re-runs | a lens cache keyed on lens, round, and content hash makes a re-run free |
| Cross-round memory | finding titles and a pass log inside the proposal | a structured `.review-log.md` with a compaction pass |
| Introspection | counters wake one agent; a panel only on stop | a warrant gate, then a verdict-specific falsification panel every time |
| Deviations | in-memory per step, lost at step end | `.deviations.md`, durable, read by the reviewers |
| Step build loop | implement → verify → review, tests every round | implement → conformance → tests → scoped re-checks |
| Test selection | the agent's judgement, biased conservative | a change-class table plus a cost ledger |
| Configurability | six knobs and `lensPrompt` | a per-agent prompt map, a classified argument set, and mid-run argument overrides |

Nothing here touches `spec/`, `pkg/`, `cmd/`, `charts/`, or `migrations/`, so
`spec-driven-development.md` does not require a change proposal for this work.
It is tooling. It does land on a feature branch and merge with `--no-ff` per
`git-workflow.md`.

---

## 2. The proposal directory

### 2.1 Layout

```
proposals/0081_fix_some-proposal/
  0081_fix_some-proposal.problem-statement.md
  0081_fix_some-proposal.summary.md
  0081_fix_some-proposal.status.md
  0081_fix_some-proposal.implementation-checklist.md
  0081_fix_some-proposal.spec-changes.md
  0081_fix_some-proposal.non-spec-changes.md
  0081_fix_some-proposal.review-log.md
  0081_fix_some-proposal.deviations.md
```

The directory name and the file stem are the same string. Your sketch used
`0000-some-proposal`; I have kept the repository's existing `NNNN_kind_slug`
stem instead (**D1**). The reason is continuity rather than a gate: the `kind`
segment (`new` or `fix`) is used by the skill and by `BUILD-GAPS.md` and
`PROPOSAL-QUEUE.md` cross-references, and every existing reference is written
in that form. The one regex that reads proposal names,
`qualifyingDocument` in `tests/tier0_static/citation_document_test.go`, matches
the bare four-digit number as its own alternative, so it tolerates a hyphen
form. Switching the delimiter is therefore a mechanical follow-on if you want
it; say so and I will fold it into phase 1.

A single resolver function is added to both workflows so no agent prompt ever
concatenates a path by hand:

```js
function proposalFiles(root) {           // root = proposals/0081_fix_slug  OR  proposals/0081_fix_slug.md
  // returns { layout: "folder"|"legacy", dir, stem,
  //           problem, summary, status, checklist, spec, nonSpec, log, deviations }
  // legacy: every role resolves to the single .md file, so an unmigrated
  // proposal still runs end to end with the section-based prompts.
}
```

Legacy support is not a compatibility shim for the outside world; it is
required because 79 proposals exist and 38 of them are `Applied to spec` with
code still to land.

### 2.2 File roles and who may write them

This table is encoded once as a `FILE_ROLES` constant and injected into every
agent prompt in both workflows, alongside the existing "the only file you may
edit is X" constraint.

| File | change-proposal | implement-proposal | Human |
|:--|:--|:--|:--|
| `.problem-statement.md` | writes at init; rewrites only on a `reframe` | read | edit |
| `.summary.md` | **sole writer** | read | edit |
| `.status.md` | Draft → Reviewed | Approved → Implemented | Reviewed → Approved |
| `.implementation-checklist.md` | seeds and maintains | **may edit**; ticks boxes | edit |
| `.spec-changes.md` | **sole writer** | read | edit |
| `.non-spec-changes.md` | writes | reconciles references invalidated by a spec-apply deviation, and nothing else (§9.7) | edit |
| `.review-log.md` | every agent appends; compaction rewrites | append | read |
| `.deviations.md` | read only | **sole writer** | read |

Enforcement is prompt-level, as it is today, plus a **write audit**: the hashes
of every file are taken at round start and compared at round end, and any file
that changed outside its owner's allowance is reported. This mirrors the
existing `proposal-edit-audit` in the build workflow: it does not prevent the
write, it makes it visible. A violation is reported in the run result and never
silently reverted.

The audit is **not its own agent**. An earlier draft ran two agents per stage,
which at eight stages a round is sixteen agent calls to notice something that
almost never happens. It lives in the round-boundary script
of §5.5, which records the hashes for the next round and compares the ones it
recorded for this one. It is deliberately not appended to a reviewing agent: a
skipped audit fails silently and takes a guarantee with it.

### 2.3 Internal structure of each file

**`.problem-statement.md`** — the entry point.

```
# Problem: <title>
## Statement            one to three paragraphs, declarative
## Evidence             file:line citations, each marked verified | unverified
## Who observes it      the reader, operator, or component that meets the defect
## What breaks if nothing changes
## Findings this unblocks     BUILD-GAPS / TEST-GAPS ids, or "none"
## Prior art considered       existing spec surfaces, landed proposals, why they do not cover it
## Validated premises         the dossier the Validate stage produced (verdict, evidence, notes)
```

**`.summary.md`** — the one file every implement-side agent reads in full.

```
# Summary: <title>
## What changes          3–6 bullets, one per top-level change, each naming its surface
## Goals
## Non-goals             including alternatives dropped in Challenge, with reasons
## Fixed decisions       the decisions an implementor must not revisit, one line each
## Watch out for         the traps: a surface that looks safe and is not; an ordering that
                         matters; a test that will mislead; a prior attempt that failed and why
## Deliverable index     id → file → one line.  This is the only place SPEC-3, CODE-1, TEST-2
                         resolve, and the checklist and both change files cite it.
```

**`.status.md`** — typed frontmatter, prose body.

```yaml
---
proposal: 0081_fix_some-proposal
title: Some proposal
kind: fix                      # new | fix
status: Approved               # Draft | Reviewed | Approved | Implemented
drafted:     { date: 2026-08-31, by: change-proposal }
reviewed:    { date: 2026-09-01, by: change-proposal }
approved:    { date: 2026-09-02, by: alice }
implemented: { date: , by: }
review:      { specRounds: 7, nonSpecRounds: 9, findingsFixed: 41, converged: true }
---
```

`status` carries exactly the four values you named and nothing else. There is
no `specApplied` field and no fifth state, because **partial progress is not a
status** — it is the tick marks in `.implementation-checklist.md`, which are
strictly more informative than any boolean and are already the resumption
record. A proposal is `Approved` from sign-off until every box is ticked and
the run is green, and `Implemented` after that. §9.1 is why this works, and it
is the largest structural change in the plan.

Two small tools read this file so nothing greps prose again:

- `scripts/proposal-status.mjs <dir> [--field status]` — prints one field, exit 1
  if the file is malformed. Used by the guard hook and by the skills.
- `scripts/proposal-status.mjs --set status=Approved --by alice --date …` —
  the only supported way to change a status by hand.

**`.spec-changes.md`**

```
# Spec changes — <title>
## Design (as the spec must state it)
## Edge cases and accepted failure modes   rows the SPEC text owns
## Staged edits
   ### SPEC-1 · spec/04_….md § 4.6.3
   Anchor: "Replace the row beginning …"
   ```
   <exact text>
   ```
## Spec files touched
```

**`.non-spec-changes.md`**

```
# Non-spec changes — <title>
## Design (implementation-facing)
## Staged code changes        ### CODE-1 · pkg/gateway/…
## Staged schema, chart, and migration changes    ### SCHEMA-1 / CHART-1 / MIG-1
## Staged docs changes        ### DOCS-1 · docs/…
## Testing                    ### TEST-1 · tier, // spec: tie, non-happy path it covers
## Edge cases and accepted failure modes   rows docs/ or code owns
## Open decisions for review
## Files touched on application (non-spec)
```

**`.implementation-checklist.md`** — the existing format, unchanged in form.
One lane per step, and the standard pattern is every spec step in a leading
block (§9.1).

```
- [ ] **S1 · spec** — SPEC-1. The §4.6.3 ownership row for the drop counter.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §16.1 catalog entry.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · code** — CODE-1. Emit the counter from the adapter.
      Tiers 0, 1, 3. Depends on: S1, S2
- [ ] **S4 · docs** — DOCS-1. The metrics reference row.
      Tiers 0, 11. Depends on: S2
```

A justified interleave, which is the exception rather than the shape to aim
for:

```
- [ ] **S1 · code** — CODE-1. The migration pass and its register.
      Tiers 0, 1. Depends on: —
- [ ] **S2 · spec** — SPEC-1. Applied by running the pass from S1; the edit
      sites are resolved from its register and are not enumerable by hand.
      Interleaved because the staged edit is this tool's output.
      Tiers 0. Depends on: S1
```

**`.review-log.md`** — §6.

**`.deviations.md`** — §9.4.

### 2.4 Migration: lazy, per proposal, automatic

**There is no batch migration and no migration script to run.** A proposal
migrates the first time a workflow touches it, as part of that run. Converge
0076 and it arrives as a directory; nothing else in `proposals/` moves. The
tree stays mixed for as long as it stays mixed, which the resolver in §2.1
already handles.

This is a better shape than a big-bang for three reasons. The split needs
judgement about which design paragraphs are spec-facing and which are not, so
it was always going to be agent work rather than a script. Migrating a proposal
nobody is working on buys nothing and risks a bad split landing unreviewed.
And a lazy migration is reviewed in the same commit as the work that triggered
it, by the person who asked for that work.

#### The migrator

`.claude/workflows/migrate-proposal.js` — a small workflow, invoked by
`change-proposal` and by `implement-proposal` at startup when `proposalFiles()`
reports `layout: "legacy"`. One implementation, called from two places, rather
than the same procedure written twice in prose across two skills.

What it does, for one proposal:

1. Read the legacy `NNNN_kind_slug.md` in full.
2. Create `proposals/NNNN_kind_slug/` and partition the document into the eight
   files per §2.3. Nothing is rewritten, only relocated: Problem → problem
   statement, Summary → summary, the checklist → checklist, spec-targeted
   subsections of Proposed changes → spec-changes, the rest → non-spec-changes,
   the "Resolved in adversarial review" history → the review log's `Retired`
   section, the Status bullet → the frontmatter. `.deviations.md` is created
   with its header only.
3. Run `scripts/check-proposal-split.mjs <legacy> <dir>` (below) and fix any
   line it reports as lost.
4. Update the inbound references (below).
5. Delete the legacy file and commit the whole migration as one commit, so it
   is reviewable as a unit and revertable in one move.

**It refuses on an `Implemented` proposal**, and the calling skill refuses the
run. A landed proposal is a historical record and splitting it is an edit
(`spec-driven-development.md`). Under lazy migration this needs no flag and no
policy: an implemented proposal is never converged again and never implemented
again, so nothing ever triggers its migration. D2 is satisfied by construction
rather than by a default.

**It is idempotent and resumable.** Directory present and legacy file gone →
already migrated, return immediately. Both present → a previous run died
mid-split; resume it. This matters because the migration now happens inside a
long run that can be interrupted.

#### The partition check stays mechanical

`scripts/check-proposal-split.mjs <legacy-file> <dir>` — deterministic, no
agent. It asserts every non-blank line of the original appears in at least one
of the new files, ignoring headings the split is allowed to add, and exits
non-zero listing what is missing. The split is agent judgement; the guarantee
that the split lost nothing is not, and keeping it in a script is what stops
"migrated" from meaning "an agent said it was fine".

#### Inbound references, and why they get harder rather than easier

A big-bang migration would have updated every inbound reference in one commit.
Lazy migration means a reference can break at any time, one proposal at a time,
so two things change.

**Consumers must accept both layouts, from phase 1.** In particular
`close-build-gaps.sh` instructs its agent to "extract the referenced
`proposals/NNNN_*.md` path"; that glob does not match a directory. It becomes
"resolve the referenced proposal, which is either `proposals/NNNN_kind_slug.md`
or the directory `proposals/NNNN_kind_slug/`", and status is read through
`scripts/proposal-status.mjs`, which already handles both.

**The migrator updates the references it breaks**, as step 4, because there is
no later commit in which to do it. The sites are enumerable and small:

| Site | Exposure |
|:--|:--|
| `BUILD-GAPS.md`, `PROPOSAL-QUEUE.md` | findings and queue rows naming a proposal path. The common case; retarget to the directory |
| `tests/tier11_docs/spec_28_index_rows_test.go` | `channelsProposalFile = "0067_new_….md"`. 0067 is `Applied to spec`, so this **will** break the first time 0067 is touched. The constant retargets to that proposal's `.spec-changes.md`, since the test reads staged text |
| `tests/tier11_docs/adapter_metric_catalog_test.go` | reads `filepath.Join(root, "proposals", proposal)` from `specCatalogPending`, which is **empty today**. No live exposure, but the pattern breaks if it is repopulated with a migratable proposal. Worth a comment in that file |
| `tests/tier11_docs/spec_28_ownership_test.go` | `renamingProposalFile` names 0064, which is `Implemented` and therefore never migrates. Safe by construction |
| `scripts/specshift/scope/scope.go`, `citation_document_test.go`, `residual_gate_test.go` | all key on the `proposals/` **prefix**, which a directory still satisfies. Unaffected |

The migrator greps for the old path before deleting it and reports every site
it changed, so a reference it could not resolve stops the migration rather than
landing a broken tree. Retargeting needs judgement — a finding wants the
directory, a test that reads staged text wants `.spec-changes.md` — which is
why this is agent work rather than a `sed`.

---

## 3. The `spec/` guard hook

### 3.1 What is wrong now

```sh
case "$f" in spec/*) if grep -q '^- \*\*Status:\*\* Approved' "$r"/proposals/*.md; then exit 0; fi; … esac
```

Three defects, all of which you named:

1. **It is global.** Any approved proposal anywhere unlocks all of `spec/` for
   everything, including an agent working on an unrelated proposal.
2. **It closes too early.** The moment `implement-proposal` writes
   "Applied to spec", the grep stops matching (if no *other* proposal is
   approved) and the build phase can no longer touch `spec/`. A checklist that
   deliberately interleaves a spec step after a code step cannot be executed.
3. **It reads prose.** A status bullet whose wording drifts silently changes
   the security posture of the tree.

### 3.2 The lease

State file `proposals/.spec-lease.json` (tracked, so a stale lease is visible
in `git status`):

```json
{
  "proposal": "proposals/0081_fix_some-proposal",
  "runId": "wf_ab12cd",
  "step": "S3",
  "opened": "2026-08-31T10:04:00Z",
  "expires": "2026-09-01T10:04:00Z",   // opened + leaseTtlHours, default 24
  "allow": ["spec/04_control-plane.md", "spec/28_communication-channels.md"]
}
```

Hook algorithm, replacing the grep:

1. Path not under `spec/` → allow (unchanged).
2. No lease file → **block**.
3. Lease `expires` in the past → block, with "stale lease; release it or re-run".
4. `scripts/proposal-status.mjs <lease.proposal> --field status` is not
   `Approved` → block.
5. `allow` is non-empty and the path is not in it → block, naming the list.
6. Otherwise allow.

Properties this buys:

- **Independent of every other proposal.** Only the leased one is consulted.
- **Spec and code may interleave, without a standing grant.** The lease is
  opened for one `spec`-lane checklist step and released when that step ticks
  (§9.1). `allow` holds exactly the files that step's `SPEC-n` deliverables
  target, taken from `.spec-changes.md`. A `code`-lane step holds no lease at
  all, so `spec/` is locked for the great majority of a run's wall-clock. This
  is narrower than both the current hook and a run-scoped lease: today one
  approved proposal anywhere unlocks all of `spec/` for everything.
- **The window is the step, not the run.** The lease is open only while a
  `spec`-lane step is executing and only over that step's files. Steps run
  sequentially, so no code-writing agent is ever alive while a lease is held.
  This is the actual guarantee, and it does not depend on the lane order: an
  interleaved proposal has more windows, not weaker ones.
- **A `code`-lane step asserts the lease is closed before it starts**, and
  fails the run if one is held. Releasing on step exit is the primary
  mechanism; this is the check that makes the invariant hold rather than merely
  be intended, and it is what closes the one real hazard interleaving
  introduces — a spec step that dies mid-flight leaving the lease open for
  whatever runs next.
- **Fails closed.** No lease, no writes.
- **Auditable.** The lease is a tracked file; an abandoned one shows up in
  `git status` and in the skill's precondition check.

**Opening and releasing are agent calls, not script code.** A workflow script
has no filesystem access (§5.4), so `openLease()` and `releaseLease()` are one
`haiku` agent each, running one command. The open agent also derives the
`allow` list, by reading the `SPEC-n` deliverables the step names out of
`.spec-changes.md` and collecting their target files. The `code`-step assert
folds into the step's compile-gate agent (step 0 of §9.3), which already runs a
Bash command per step, so it costs nothing extra.

That an agent can fail is the reason `expires` exists. `leaseTtlHours` (default
**24**) sets it at open time. A release that dies on a terminal return path
leaves a lease no later step will assert against, and the TTL plus the skill's
Step 1 precondition plus the file being tracked in git are the three things
that catch it.

**One honest limit.** The hook trusts the lease, and any agent can write the
lease file — it is not under `spec/`, so the hook does not guard it. This
raises the bar from "any approved proposal anywhere unlocks everything" to "an
agent must deliberately forge a lease naming an `Approved` proposal", which is
the right bar for a threat model of accidental writes rather than adversarial
ones. It is not a sandbox and is not claimed as one.

Release is not optional and not best-effort. `releaseLease()` is called at the
end of every `spec`-lane step **whether that step succeeded or failed**, and on
**every** return path — `not-approved`, `spec-unappliable`, `spec-not-clean`,
`step-stuck`, `aborted`, `spec-only`, and success. A `try/finally` around the
whole body is not available (the script is one top-level async body with early
`return`s), so the test in §11 asserts the call on each path by label.

The skill's Step 1 precondition adds: if a lease exists and names another
proposal or has expired, stop and report; `--force-release` is an explicit
operator action, never automatic.

`close-build-gaps.sh` rule B and S1 are rewritten to describe the lease rather
than the grep, and to read status through `proposal-status.mjs`.

---

## 4. change-proposal: startup

### 4.1 `Init` (replaces `Write`'s single-file creation)

`Init` runs before `Validate` in `new` mode. It creates the directory and six
skeletons: problem-statement, summary, status, implementation-checklist,
spec-changes, non-spec-changes. `.review-log.md` is created empty with its
three section headings. `.deviations.md` is created with a header only and the
note that the implementor owns it.

The problem statement is the entry point, so `Init` writes it first, from one
of two sources:

- an inline `problem` argument (today's path), or
- `problemStatementPath` — a pre-seeded file the caller already wrote, which
  `Init` moves into place verbatim.

Everything after this reads the problem from the file, never from the argument.
That is what makes a `reframe` restart possible: the workflow rewrites the file
and re-enters at `Validate`.

### 4.2 `Validate` — six lenses plus a consolidator

Today: one decomposer, then one skeptic per premise. That structure only
attacks premises. The six lenses attack the problem from six directions and one
consolidator produces the verdict and the refined problem statement.

| key | brief |
|:--|:--|
| `premise` | decompose into falsifiable premises and try to refute each. Today's behaviour, kept whole. |
| `evidence` | open every citation in the statement. Report each as verified, drifted, or false. |
| `prior-art` | does an existing spec surface, a landed proposal, or an open BUILD-GAPS/TEST-GAPS finding already cover this? |
| `scope` | is this one problem or several? Is it the right grain for one proposal, and where would you cut it? |
| `impact` | who observes the defect, in what circumstance, and what is the consequence of leaving it? Default posture: it does not matter. |
| `alternatives` | is there a framing under which no change is needed, or a strictly smaller problem that dissolves this one? |

Consolidator (`validate:consolidate`) reads all six, rewrites
`.problem-statement.md` with the surviving statement, the verified evidence,
the prior art, and the premise dossier, and returns
`{ viable, whyNotViable, restatement, loadBearingRefuted }`. Viability rule is
unchanged: every load-bearing premise refuted → `not-viable`.

### 4.3 `Draft` — six stances plus a consolidator

Today: one drafter, then one adversarial challenge per change. The single
drafter is the whole design surface of the run, and it is one sample. Six
independent stances, then a synthesis, is a judge-panel pattern that has a much
better chance of not committing to a bad spine.

| key | stance |
|:--|:--|
| `minimal` | the smallest change that resolves the problem, and nothing else. |
| `spec-first` | what the specification must say; derive everything else from it. |
| `reuse` | extend an existing spec surface, RPC, frame, or code path rather than adding one. Adding a surface is a last resort you must justify. |
| `failure-modes` | design backwards from crash, restart, failover, and partition. |
| `implementor` | design from what is buildable and testable as an ordered sequence of commits; produce the checklist as part of the design. |
| `contrarian` | argue the problem needs no change, or that a different problem is the real one. Produce a design only if you fail. |

Consolidator (`draft:consolidate`) picks a spine, grafts what the others got
right, records what it rejected and why (this becomes Non-goals), and emits the
existing `DRAFT` schema. `Challenge` is unchanged and still runs per change:
the panel produces the design, the challenge tries to kill each piece of it.

`Write` then writes the six content files from the consolidated draft rather
than one document, using the structures in §2.3.

### 4.4 `Bootstrap` — now a splitter as well as a backfiller

`Bootstrap` runs in `review` and `redesign` modes and does whichever applies:

- **Legacy single file → folder.** Not Bootstrap's job. `migrate-proposal.js`
  (§2.4) has already run at startup, so by the time Bootstrap sees the proposal
  it is a directory. Bootstrap's remaining work on a just-migrated proposal is
  the backfill case below, because a split of an old document usually leaves
  one or two of the eight files thin or empty.
- **Folder with missing files → backfill.** Create what is absent, deriving it
  from what exists, exactly as today's Summary/checklist bootstrap does, and
  marking any inferred ordering as inferred.
- **Complete folder → SKIPPED**, changing nothing.

`Conventions` runs once after `Bootstrap`, over all six content files.

---

## 5. change-proposal: the two review loops

### 5.1 Factoring

The existing `while (round < maxRounds && !converged) { … }` body becomes

```js
async function runReviewLoop(cfg) // cfg = { name, pool, editable, readable, maxRounds,
                                  //         lensExtra, fixScopeNote, budgetKey }
```

and is called twice. Everything inside — retirement, sweeps, dedup,
verification, fix grouping, post-fix review, introspection, churn, redesign —
is shared. What differs is the configuration.

**Spec loop** (`review-spec`), runs first.

- Skipped when a cheap probe agent (`probe:spec-changes`, `haiku`) reports that
  `.spec-changes.md` stages no edits.
- Pool: every lens except `test-coverage` (the Testing section lives in
  non-spec-changes). `plan-conformance` joins when `planPath` is set.
- Editable by the fixer: `.spec-changes.md`, `.summary.md`, and
  `.review-log.md`. The summary is in the set because the standing fixer rules
  require it to stay true and because its deliverable index resolves the
  `SPEC-n` ids this loop adds and removes; a loop that may not touch it would
  leave every one of its own edits mis-indexed until the next loop.
- **Not** editable here: `.implementation-checklist.md`. Under the unified
  execution sequence (§9.1) the checklist names spec steps, so it looks like
  spec-loop business, but writing it against a set of deliverables that is
  still changing produces a sequence that is rewritten every round. It is
  built once, at the handoff below, against a settled staging.
- Readable by the lenses: everything, but a finding must have a fix that lands
  in `.spec-changes.md`. A finding whose only remedy is elsewhere is out of
  scope and the lens is told so. If the same lens raises it again in the
  non-spec loop, it lands there.
- The lenses are told explicitly that **the checklist and the summary's
  deliverable index are reconciled at the handoff and are not findings here.**
  Without that they will report drift in them every round, correctly and
  uselessly, because the drift is real and is scheduled to be fixed.

**The handoff**, between the two loops. The spec staging is settled and the
checklist has not been written against it, so one agent runs before the
non-spec loop opens:

- rebuild the summary's deliverable index from what `.spec-changes.md` and
  `.non-spec-changes.md` now stage;
- write the checklist's spec-lane steps against the settled `SPEC-n` set, in
  the leading block the standard pattern calls for (§9.1), and reconcile the
  existing non-spec steps' `Depends on:` against them;
- change nothing else.

This is a reconciliation, not a review round: the design is settled for the
spec half and the non-spec half is about to be reviewed against it. It is the
step that makes "the checklist is out of scope for the spec loop" a schedule
rather than an omission.

**Non-spec loop** (`review-non-spec`), runs after the handoff.

- Pool: the full set, `test-coverage` included.
- Lenses read **both change files plus the checklist and the summary as one
  document**. This is the whole point: a non-spec change that contradicts a
  staged spec edit is a finding, and only a reviewer holding both can see it.
- Editable by the fixer: `.non-spec-changes.md`, `.implementation-checklist.md`,
  `.summary.md`, `.review-log.md`, and — unless `lockSpecChanges` — `.spec-changes.md`.
- With `lockSpecChanges: false` (the default) the fixer may touch
  `.spec-changes.md`, and is told to prefer any resolution that does not.
  Every such edit is recorded in the round history as `specTouched` and echoed
  in the run result, so a "non-spec" run that quietly rewrote the spec staging
  is visible.
- With `lockSpecChanges: true` a finding that can only be closed by a spec edit
  is closed by recording an open decision in `.non-spec-changes.md` with the
  constraint any answer must satisfy — the existing `escalated` path.

Convergence logic is untouched: lens retirement, reactivation on a surviving
finding, and a complete clean sweep of the whole pool as the stop condition.
Each loop keeps its own `retired` set, round counter, sweep count, and budget
(`maxSpecReviewRounds` default 10, `maxNonSpecReviewRounds` default 16).

The `applicability` lens's checklist clause moves to the non-spec loop only —
the checklist is not spec staging.

### 5.2 Sequential verification

```js
// today
const verdicts = await parallel(deduped.map(f => () => parallel([evidence(f), material(f)])));

// after
for each finding (still parallel across findings):
  v1 = await verify(order[0])            // default: materiality
  if (!v1.confirmed) → refuted, stop
  v2 = await verify(order[1])            // evidence
  confirmed = v1.confirmed && v2.confirmed
```

`verifyOrder` defaults to `["material", "evidence"]` and is configurable
(**D3**). Materiality runs first because it is the cheaper of the two — it
assumes the evidence is true and reads only the proposal — and because it is
instructed to default to refuted, so it kills the largest share of findings.
Evidence verification opens every cited file and is the expensive one.

Two consequences to handle in the script:

- The `rejected` list records only the first refuter's reason. Record which
  stage refuted (`refutedBy: "material"`), because "not material" and "evidence
  is wrong" are different signals to a later round's lens.
- `verifyComplete` (the guard that stops a round with a dead verifier from
  certifying convergence) must now mean "every finding reached a terminal
  verdict", where a materiality refusal is terminal with one verdict. A dead
  first verifier is still incomplete.
- A finding whose verifier died is **neither confirmed nor refuted**. It is not
  fixed this round, it is not added to `rejected` (which would suppress it
  permanently on the strength of an outage), and the round is marked
  incomplete so it cannot certify convergence. It is simply carried, and the
  next round's lens will raise it again if it is real.

### 5.3 Fix planning, design, and grouped fixing

Three stages replace the single `fix` call.

**`fix-plan`** — one agent, once per round.

```json
{ "groups": [ { "id": "G1", "title": "…", "rationale": "why these belong together",
                "findings": [0, 3, 7], "sharedSubject": "the section or mechanism they share",
                "risk": "low|medium|high", "order": 1 } ],
  "notes": "" }
```

Prompt constraints: **at most** `maxFixGroups` (default 7) groups, and **no
cap on group size**. Group by shared text, shared section, or shared mechanism,
because findings that share a root produce contradictory edits when closed
separately; a finding whose fix will cascade into other sections gets its own
group; order the groups so no group's edits destroy a later group's anchors;
and use fewer than the maximum when the findings genuinely cluster into fewer
subjects, since each group costs a `fix-design` and a `fix` agent.

Group size is deliberately unbounded, and not only because a per-group cap
combined with a group cap would bound the total findings a round can fix — 7×8
would silently make a 60-finding round unpartitionable. The stronger reason is
that **size is the wrong axis**. Forty trivial citation corrections that share
a subject belong in one group, where `fix-design` triages them in a handful of
tokens and one fixer applies them consistently; three deep design findings
belong in three groups however few they are. A size cap would split the first
case for no reason and would not help the second. Cohesion and effort are what
the planner is asked to balance, and it states the split it chose and why.

A round whose confirmed count is very large is a signal in its own right rather
than something grouping should absorb. It is recorded in the round history and
shown to the introspection pass, which is the machinery that exists to notice
it.

Script-side validation, because a planner that drops a finding silently loses
it: every confirmed index must appear exactly once. On any violation, log it
and fall back to a single group containing everything — the current behaviour,
which is safe.

**`fix-design`** — one agent per group, in parallel across groups, read-only.

This is the piece you flagged as most important to get right, so its brief is
written around three properties.

*Triage before depth.* The agent classifies each finding as `trivial`,
`moderate`, or `deep` **before** doing any investigation, and the classification
governs its budget:

- `trivial` — the reviewer's `suggested_fix` is unambiguous, lands in one
  place, and changes nothing another section states. Output: one line, "apply
  as suggested". No repository reading. Most citation, bookkeeping, and
  attribution findings are trivial.
- `moderate` — the fix is clear but touches more than one statement, or picks
  between two obvious options. Output: the choice, the sites, one sentence of
  why.
- `deep` — the fix requires inventing or changing a mechanism, or the reviewer
  offered open-ended options, or closing it plausibly cascades. Only these get
  the architect treatment.

The prompt says explicitly that spending deep effort on a trivial finding is a
defect, and that a group of eight trivial findings should return in a fraction
of the tokens a single deep one costs.

*Architect posture on `deep` findings.* The agent is told to establish ground
truth in the repository **before** reading what the proposal says about the
mechanism — the same rule that makes the existing redesign subworkflow work —
and to answer, in order:

1. Does an existing spec surface, RPC, frame, field, or code path already carry
   this? (`PRINCIPLES`, verbatim.)
2. Is there one change that closes several findings in this group at once?
3. Is the strongest available answer to **delete** something rather than
   specify it? A smaller mechanism beats a better-specified larger one.
4. If a new mechanism is unavoidable, what state does it read, which sites set
   and clear that state, who are all its callers, what happens when it does not
   fire and what observes that, and which test pins it? (The four properties
   the fixer's `newMechanisms` already demands, moved earlier, where a designer
   rather than an editor answers them.)
5. What else in the proposal must change as a consequence? Name every section.

It reads `.claude/rules/code-best-practices.md`, `doc-content.md`, and
`channel-naming.md` when the design touches their domains, and it reads the
review log's Standing context before anything else.

*Anti-hair mandate.* Stated as a first-class goal: the proposal should read as
one coherent design at the end of the run, not as a design plus forty patches.
A design that adds a conditional to avoid restating a rule, adds a second
mechanism that does what an existing one nearly does, or answers a finding with
an exception clause, must say so and must offer the coherent alternative even
when it is larger.

Output:

```json
{ "designs": [ { "findingTitle": "…", "effort": "trivial|moderate|deep",
                 "chosen": { "approach": "…", "edits": [ { "file": "…", "where": "…", "what": "…" } ],
                             "why": "…" },
                 "alternatives": [ { "approach": "…", "whyNot": "…" } ],
                 "cascades": ["sections that must change as a result"],
                 "invariantsToPreserve": ["…"],
                 "doNotDo": ["the tempting wrong fix and why it is wrong"] } ],
  "groupNote": "one change that closes several of these, if there is one",
  "newMechanisms": [ … same schema as FIX_RESULT.newMechanisms … ] }
```

`doNotDo` is worth the field: the loop's recorded failure mode is a fixer
taking the obvious local edit that a later round then has to undo.

**When `fix-design` returns nothing** (a dead agent after retries), its group's
fixer is handed the raw findings and today's fixer brief, which is the current
behaviour and is safe. The group is marked `designless` in the round history,
because a run where the design stage keeps dying is producing exactly the
unspecified-mechanism edits this stage exists to prevent, and the introspection
pass should see that.

**Cross-group design conflict is caught rather than prevented.** The designs
are produced in parallel and none sees the others, so two groups can design
edits that disagree. `fix-plan` reduces the odds by grouping on shared subject
and ordering the groups, and the round's single `post-fix` review is what
actually catches it — that is the drift check it already performs, now with a
second source of drift to look for.

`FIX_RESULT` gains `designRejected`: one entry per finding whose design the
fixer judged wrong, naming what it did instead. A fixer that silently
substitutes its own design is the failure this whole stage is meant to remove,
so the field is required and an empty array is the expected value.

**`fix`** — one agent per group, **sequential** across groups.

Sequential rather than parallel because the groups edit the same markdown files
and concurrent edits to one file lose writes. `fixGroupsParallel` exists as an
escape hatch (default `false`) for a caller who knows the groups are
file-disjoint.

Each fix agent receives: its group's findings, the chosen design per finding,
the alternatives considered (so it does not re-derive them or silently pick a
different one), `doNotDo`, `cascades`, the review log's Standing context, the
mechanism strike table, and the standing fixer rules (no counts, reconcile
enumerations, keep the checklist and summary current, propagate a changed
predicate everywhere). Its own scope for design decisions shrinks: it applies a
design rather than inventing one, and if it finds the design is wrong it says
so in `designRejected` rather than improvising.

**`post-fix` review** runs **once per round, after every group**, against the
snapshot taken before the first group. That keeps its cost flat while catching
the new risk the grouping introduces: drift *between* groups, where G1's edit
and G4's edit disagree. One follow-up fix, capped as today.

### 5.4 Lens result caching across a crash

The runtime's `resumeFromRunId` already replays completed `agent()` calls from
the journal, so a resume of the *same run* does not re-review. The gap is a
**fresh relaunch**, which is what actually happens after an auth expiry or a
script edit.

**A workflow script cannot do this in code.** This was verified empirically
rather than taken from the source comments, because ~200 agent calls in this
plan rest on it. A probe workflow returning `typeof` results for every escape
route found:

```
globalThis keys:  log, phase, console, budget, setTimeout, clearTimeout,
                  Date, agent, parallel, pipeline, workflow, args
require, process, globalThis.process, fetch, Buffer, module, __dirname
                                              → all undefined
Function("return process")()  → THROWS: Code generation from strings
                                        disallowed for this context
```

That is the whole surface. There is no filesystem, no `process`, no network,
and the `Function`-constructor route out is closed at the V8 level rather than
by convention, so a script cannot reach a file by any means. The source comment
in `change-proposal.js` records the incident that motivated the rule: an
earlier revision called `require("fs")` inside a `try`/`catch`, silently
produced an empty result, and disabled the per-round diff, the bootstrap step,
and the growth signal at once without ever failing.

So the design question is never "code or agent" but "how few agents", and the
standing answer is to fold every file operation into an agent the run is
already making. For the lens cache, that is none beyond the lens itself.

Mechanism: give each lens agent a cache instruction. No extra agents.

```
CACHE. First run:
  H=$(cat <spec-changes> <non-spec-changes> <checklist> | md5sum | cut -c1-12)
  test -f <dir>/<lens>-r<N>-$H.json && cat <dir>/<lens>-r<N>-$H.json
If that printed JSON, return exactly it as your structured output and do no
other work. Otherwise do the review, and immediately before returning write
your findings JSON to <dir>/<lens>-r<N>-$H.json (mkdir -p first).
```

`<dir>` is `scratchpad/cp-cache/<runTag>/`. `runTag` defaults to the proposal
stem and is a caller argument, so two runs against the same proposal stay
apart.

**The key is (lens, round, content-hash), and that removes the cleanup
entirely.** An earlier draft of this section keyed on `(lens, round)` alone and
therefore needed the round's directory deleted after the fixes landed, by a
dedicated agent, in a specific window — between the fix landing and the state
write. That was both an extra agent per round and an ordering constraint that a
crash in the wrong place defeats.

Adding the content hash makes staleness impossible by construction instead. A
cache hit now means *the same lens, in the same round, over byte-identical
proposal text*, which is exactly and only the crash-resume case. After a fix
lands the hash changes, every key misses, and the lenses re-read the changed
text because that is the only thing they can do. Nothing needs deleting, and no
window exists in which a stale entry could be served.

Two loose ends, both minor:

- **Disk.** `scratchpad/` is gitignored and the entries are small JSON. If it
  ever matters, a `find scratchpad/cp-cache -mtime +7 -delete` folds into an
  agent the run already makes at startup rather than justifying its own.
- **Repository drift.** The hash covers the proposal, not the tree, so a lens
  cached before a rebase would be served afterwards. Within a single run the
  tree does not move under the loop, and the round-keyed scheme had the same
  exposure, so this is not a regression.

The same instruction goes on the verify agents, which are the other expensive
fan-out; their hash covers the finding they are verifying rather than the
proposal.

Cost: zero extra agents; two extra Bash calls per lens.

### 5.5 The per-round bookkeeping, and how it is made reliable

The sandbox has no filesystem (§5.4, verified), so every file operation costs
an agent invocation. An earlier draft of this section drew the wrong conclusion
from that — "never add an agent for a file operation; append it to an agent the
run is already making" — which optimises agent count at the cost of the thing
that actually matters. **An agent with a large task in front of it is not a
reliable executor of a small mechanical instruction appended to the end of its
prompt.** A lens agent asked to review a proposal adversarially and *also* write
a hash file will sometimes not write the hash file, and the miss is silent.

The codebase already knows this. Every mechanical file operation in
`change-proposal.js` and `implement-proposal-build.js` today is a **dedicated
`haiku` agent given one exact command and told to do nothing else**:

```js
"Run exactly this command and reply with the single word DONE:\n\n" +
  "mkdir -p " + SNAPDIR + " && cp " + path + " " + dest + "\n\n" +
  "Do nothing else. Do not read, summarise, or edit either file."
```

`snapshot`, `diffHunks`, and the checklist `tick` are all written this way. The
consolidation rule was a regression against a pattern this repository converged
on, and it is withdrawn.

#### The rule, corrected

**(a) Mechanical logic lives in a repo shell script; the agent runs one
command.** Rather than handing an agent eight bash commands, the plan ships
`scripts/cp-round-boundary.sh` and the agent's entire prompt is:

```
Run exactly this command and reply with its stdout and nothing else:
  bash scripts/cp-round-boundary.sh --tag <runTag> --loop <loop> --round <N>
Do nothing else. Do not read, summarise, or edit any file.
```

Three things follow, and together they are worth more than the agent count ever
was. The logic is **in version control**, so it is reviewable and changes are
diffable rather than buried in a prompt string. It is **testable in layer 4**,
by real execution against a fixture tree with no agent involved, which turns
the most failure-prone part of the design into the most testable part. And the
agent becomes a **transport** with essentially no room to deviate: one command,
stdout back, a non-zero exit reported rather than papered over.

The script does what the earlier draft asked eight commands to do — merge and
delete the log shards, count the ledger, compare the proposal-directory hashes
against the ones it recorded last round, write the state file, take the next
round's snapshot, record fresh hashes, and print the override file — and
returns one JSON object. If it exits non-zero the round is marked incomplete
rather than proceeding on unknown state, the same fail-closed treatment a dead
lens gets.

**(b) An operation may be appended to a substantive agent only when its failure
is benign** — meaning the run loses an optimisation or a signal, never a
guarantee. That test sorts the cases cleanly:

| Operation | Appended to | Failure if skipped | Verdict |
|:--|:--|:--|:--|
| lens cache read and write | the lens | cache miss; the lens simply reviews | benign, **append** |
| cost-ledger append (§9.4) | the verifier that ran the tiers | tier reads as unknown-cost, so it runs | benign and fails toward more testing, **append** |
| log shard write | every agent | that agent's entries are lost from the log | tolerable, **append** — the alternative is an agent per agent |
| miss record (§9.3) | the final-gate agent | a calibration signal is lost | borderline; **append**, and the record is reconciled by the boundary script |
| write-audit hashes | — | the audit silently never fires | **dedicated**, in the boundary script |
| state file write | — | resume breaks | **dedicated**, in the boundary script |
| lease open and release (§3.2) | — | `spec/` stays unlocked | **dedicated**, one exact command each |

The lease line is the correction that matters most. An earlier draft folded the
release into the spec step's commit agent to save a call. The lease is the
security boundary of this entire design, its failure is not benign, and a
commit agent that forgets it leaves `spec/` writable. It goes back to a
dedicated agent with one exact command, on the same reasoning that makes
`snapshot` one today.

#### What it costs

One `haiku` agent per round for the boundary script, plus one per lease open
and one per release on each `spec`-lane step, of which there are few. Against a
round in which twelve lens agents each run for minutes, that is roughly a
minute of wall clock across a 25-round run and a rounding error in tokens. The
version this replaced — nine separate calls a round, over two hundred across a
run — was worth removing. Going from one call a round to zero is not worth
trading determinism for.

---

## 6. The review log

### 6.1 Why it is a file and not a section

Today's cross-round memory is three things: a list of fixed finding titles, a
list of refuted findings, and the "Resolved in adversarial review" section
inside the proposal. None of them carries *why* a decision was taken, what a
future agent should not try, or what turned out to be misleading. The log is
where that lives, and it is read by every agent in both workflows.

### 6.2 Format

```
# Review log — 0081_fix_some-proposal

## Standing context
<curated by the compaction pass; what every future agent must know before it starts.
 Target ≤ 80 lines. Nothing here without an entry id it was distilled from.>

## Ledger
<chronological, newest last>

## Retired
<one line per entry compaction demoted, with why>
```

A ledger entry:

```
### [non-spec.4.fix-design.G2] · 2026-08-31 · fix-design · group G2
- DECISION: routed the drop counter through the existing adapter metrics endpoint
  — BECAUSE a second endpoint would need its own scrape target (§16 has one)
  — ALTERNATIVES: a new /metrics/tracing endpoint (rejected: a deployer must wire it);
    piggy-backing on the JSONL frame (rejected: the consumer is Prometheus, not the gateway)
- WATCHOUT: the adapter's metrics are outside the default scrape target set, so any
  claim that a metric is "collected" needs the deployer step stated — EVIDENCE: spec/16 §16.4
- FACT: pkg/alerting/rules is the single source for the alert catalog; docs/reference/metrics.md
  is rendered — EVIDENCE: pkg/alerting/rules/rules.go:12
- CORRECTS [spec.2.review-citations.3]: that entry says §16.2 owns the inventory; §16.1 does.
- USEFUL [spec.1.fix-design.G1]: its enumeration of the frame consumers saved a full re-derivation.
- UNVERIFIED: nobody has checked whether tier-3 has a frame-address contract test.
- OPEN: whether the counter is per-session or per-pod needs the human reviewer.
- RETIRED [non-spec.2.fix.G1]: superseded by the redesign in round 3.
```

Tag vocabulary, fixed and enforced by the prompts:

| tag | meaning |
|:--|:--|
| `DECISION` | a choice made, with `BECAUSE` and `ALTERNATIVES` |
| `WATCHOUT` | a trap a future agent will hit, with `EVIDENCE` |
| `FACT` | a durable fact about the tree that cost effort to establish, with `EVIDENCE` |
| `MISTAKE` | something an earlier round got wrong and what it cost |
| `UNVERIFIED` | a claim nobody has checked, and who should |
| `OPEN` | a question for a later round or a human |
| `CORRECTS <id>` | a named earlier entry is wrong or misleading, and what is true |
| `USEFUL <id>` | a named earlier entry saved real work — compaction must keep it |
| `RETIRED <id>` | a named earlier entry no longer applies |

Entry ids are `[<loop>.<round>.<stage-label>.<n>]`, allocated by the agent
within its own scope. `(loop, round, stage-label)` is unique per agent, so no
coordination is needed.

`CORRECTS` and `USEFUL` are the two you asked for and they are what makes
compaction possible: they are the signal that separates an entry that should be
promoted from one that should be deleted.

### 6.3 Writing without a race

Twelve lenses appending to one file in parallel loses writes. Each parallel
agent writes its own **shard**:

```
scratchpad/cp-log/<runTag>/<loop>.<round>.<label>.md
```

and the round-boundary script (§5.5) concatenates the shards into
`.review-log.md` in a deterministic order and deletes them. Every agent writes
a shard, including the sequential ones, because one rule is simpler to state
than two.

**`READ_ONLY` has to be amended, or every lens prompt contradicts itself.** The
existing constant says *"You are a read-only investigator. Do not create, edit,
or delete any file."* An agent told that and also told to write a log shard has
been given two incompatible instructions, and which one it follows is not
something to leave to chance. The constant becomes: *"Do not create, edit, or
delete any file except your own log shard at `<path>`, which you append to
before you return."* Layer 1 gains no check for this; layer 3's golden digest
does, because `READ_ONLY` is one of the invariant blocks it hashes.

**The merge is idempotent**, because a crash mid-merge would otherwise
duplicate entries on the retry: each shard is appended and then deleted, one at
a time, so a shard that survives is one that was not yet appended.

Cost: no agent of its own; the merge is one step of the round-boundary script.

### 6.4 Compaction

Trigger, checked after every review round by a cheap size probe (`wc -l`):

- Ledger exceeds `compactAtLines` (default **400**), **or**
- Ledger grew by more than `compactGrowthLines` (default **150**) since the
  last compaction.

The 400-line default is chosen from carriage cost, not aesthetics. Every lens
prompt already carries ~3k tokens of standing text; a 400-line log is roughly
6–8k more, and at twelve lenses that is ~90k tokens per round spent on log
carriage alone. Compaction pays for itself in about one round.

Targets after a pass: Standing context ≤ `standingContextMaxLines` (80),
Ledger ≤ 200 lines.

Compaction rules, in the agent's brief:

1. **Age-graded.** Entries from the current round are untouched. Entries one to
   three rounds old are merged where they share a subject. Entries older than
   three rounds are reduced to their durable residue — `FACT`, `WATCHOUT`,
   `DECISION` — which moves into Standing context, and the rest is deleted or
   moved to Retired with a one-line reason.
2. **Honour `CORRECTS`.** The corrected entry is rewritten to what is true, or
   retired. A superseded `WATCHOUT` is deleted, not kept "for the record": a
   warning about a trap that no longer exists costs every future agent a
   detour.
3. **Honour `USEFUL`.** An entry another agent cited as useful is promoted into
   Standing context and is never dropped while its subject stands.
4. **Resolve contradictions actively.** When two entries disagree and neither
   corrects the other, verify against the tree — the compaction agent has Read
   and Grep — and keep the true one, retiring the other with the evidence.
5. **Never drop an `OPEN` or an `UNVERIFIED`** until something closes it. An
   `UNVERIFIED` that a later round verified is rewritten as a `FACT`.
6. **Leave a changelog.** The first lines of Standing context record what this
   pass merged, promoted, and retired, so the next compaction can be judged.

The compaction agent may edit only `.review-log.md`.

---

## 7. Introspection

### 7.1 The warrant gate

The counters that wake introspection are crude and, as the current comments
say, "often wrong in both directions". The first thing the pass does now is ask
whether it should be running at all.

`introspect:gate` — a cheap agent, given the wake reason, the counter output,
the log's Standing context, and the round history. Returns
`{ warranted: bool, why, whatTheCounterMissedOrOverread }`.

- Woken by a **counter** and `warranted: false` → return
  `{ verdict: "healthy", gated: true, why }` immediately. No full pass, no panel.
- Woken by the **cadence** (`round - lastIntrospectRound >= introspectEvery`) →
  the full pass runs regardless of the gate, exactly as you specified. The
  gate's answer is still recorded and shown to the pass, because "the counters
  were wrong three times in a row" is itself evidence about the run.

`introspectGate` (default `true`) turns the gate on; set it `false` to run the full pass on every wake.

### 7.2 More evidence in the pass

The pass's brief gains, on top of what it has today:

- The review log's **Standing context** in full.
- **Every `CORRECTS` entry in the run.** A log full of corrections is direct
  evidence the loop is misleading itself, and no counter sees it.
- **Every `OPEN` and `UNVERIFIED`** still outstanding.
- The **per-round log summary** for all rounds, not only the last
  `introspectEvery` — the round history it already gets, plus one line per round
  naming what the log recorded.
- The **fix-design output** for the last few rounds, which is where a mechanism
  being repaired facet-by-facet is most visible.
- Whether the gate thought this wake was warranted.

### 7.3 A panel on every verdict, specialised per verdict

Today the panel convenes only for `reframe` and `halt`, and it votes on the
verdict. Two changes.

**Every preliminary verdict goes to a panel, including `healthy`.** A wrong
`healthy` is the verdict that costs the most tokens, and nothing currently
checks it.

**Each verdict type has its own panel.** Judges within a panel are similar —
same evidence, same brief — and differ in the question they weigh, which is
what makes their agreement informative. `judgesPerVerdict` defaults to 3,
`judgesHealthy` to 2.

| Preliminary verdict | Judges |
|:--|:--|
| `healthy` | *trajectory-skeptic* (find the evidence it is not draining); *blind-spot* (is a quiet area quiet because it is clean or because no lens is examining it) |
| `prune` | *necessity* (is the named text genuinely redundant); *dependency* (does anything else in the proposal cite what would be deleted); *delegation* (would an `IMPLEMENTOR'S CHOICE` blank actually bound the choice, per `FORMAT_BLANKS`) |
| `redesign` | *architecture*; *smaller-mechanism*; *cascade* — **briefed with the same principles block as `fix-design`** (§5.3), so a redesign is judged by the standard the fixes are designed to |
| `reframe` | *problem-fit* (is the problem statement still the right problem, read against `.problem-statement.md`); *scope* (is this one proposal or several); *evidence* (does the tree still support the framing) |
| `halt` | *human-question* (can a human actually answer the question as stated); *cost* (what does continuing buy against what it costs); *self-help* (is there a legal move the loop has not tried) |

**The judges falsify; they do not vote.** Each returns:

```json
{ "falsified": true, "howConclusive": "conclusive|partial|none",
  "theArgumentIAttacked": "…", "reasoning": "…", "evidence": ["file:line"],
  "fallbackVerdict": "healthy|prune|redesign|reframe|halt" }
```

Rule: **the preliminary verdict stands unless a majority of the panel returns
`falsified: true` at `conclusive`.** `falsificationBar` (default
`"conclusive"`) can be relaxed to `"partial"`. When the verdict is falsified,
the panel's decision is the *least disruptive* `fallbackVerdict` any falsifier
named, on the same asymmetry that governs the loop today. A panel that cannot
reach quorum leaves the preliminary verdict standing, except for `halt` and
`reframe`, where no quorum means continue — the existing bias, kept.

This inverts today's majority-vote in exactly the way you asked: the burden is
on the challenger, and an unfalsified argument wins.

### 7.4 Next steps on `reframe` and `halt`

The pass now returns, and the panel checks:

```json
{ "proposedNextSteps": {
    "summary": "what should happen next, in two sentences",
    "confidence": "clear" | "needs-human",
    "humanDecision": "the question, when confidence is needs-human",
    "rerun": {
      "mode": "review|redesign|new",
      "args": { "lockSpecChanges": true, "startLenses": ["mechanism"], "maxFixGroups": 4 },
      "prompts": { "review.mechanism": "…", "fix-design": "…" },
      "problemStatementEdit": "for reframe: the restatement to write before re-entering"
    } } }
```

`confidence: "clear"` requires: the `rerun.args` validate against the arg
schema; every prompt key is known; and the panel did not object to the plan.

**Skill behaviour**, encoded in `change-proposal/SKILL.md`:

- verdict `halt` **and** `confidence: "clear"` **and** the auto-restart budget
  is not spent → **relaunch the workflow with the proposed arguments
  immediately, without asking the user.** Report that it did.
- verdict `halt` and `confidence: "needs-human"` → stop and put the question.
- verdict `reframe` → write the restated problem statement, then relaunch at
  `startAt: "validate"` under the same rule.

The budget is `maxAutoRestarts` (default **2**), tracked by the skill in the
status file's body so it survives a session boundary. Without a budget a
mis-calibrated pass restarts forever.

---

## 8. Configurability

### 8.1 Per-agent custom prompts

New argument `prompts`, a flat map from an agent key to text appended verbatim,
wrapped in the existing caveat block ("adds context or focus; it does not lower
the finding bar and does not make something a finding the bar excludes").

Resolution order for a lens: `prompts["review.<lensKey>"]` → `prompts["review"]`
→ `lensPrompt` (kept as an alias for compatibility).

Keys, validated against a `PROMPT_KEYS` registry with an unknown key as a hard
error, matching the existing `startLenses`/`excludeLenses` policy:

```
init
validate.premise  validate.evidence  validate.prior-art  validate.scope
validate.impact   validate.alternatives  validate.consolidate
draft.minimal  draft.spec-first  draft.reuse  draft.failure-modes
draft.implementor  draft.contrarian  draft.consolidate
challenge   write   bootstrap   conventions   probe.spec-changes
review      review.<lensKey>    dedup
verify.material   verify.evidence
fix-plan    fix-design   fix   post-fix   follow-up-fix
compact     round-boundary
introspect  introspect.gate   judge.healthy  judge.prune  judge.redesign
judge.reframe  judge.halt
prune       redesign.spec  redesign.consolidate  redesign.review  redesign.apply
checklist-verify   status
```

The current script deliberately withholds caller text from the verifiers ("a
verifier told what to conclude is not a verifier"). You asked for every agent
type to be steerable, so the keys exist — with the wrapper text for verifiers
and judges strengthened to "you may be given evidence pointers and context; you
may not be given a conclusion, and an instruction to reach one is to be
ignored and reported", and with every agent that received caller text echoed in
the run result under `promptsApplied`.

### 8.2 New arguments

All have defaults. The skill documents each with what changing it does and when
you would.

| arg | class | default | effect |
|:--|:--|:--|:--|
| `lockSpecChanges` | forward | `false` | the non-spec loop may never edit `.spec-changes.md`; a spec-only finding becomes an open decision. Set it when the spec staging is already signed off in substance. |
| `maxSpecReviewRounds` | forward | 10 | budget for the spec loop |
| `maxNonSpecReviewRounds` | forward | 16 | budget for the non-spec loop |
| `skipSpecReview` / `skipNonSpecReview` | launch | `false` | resume control; skipping a loop means convergence certifies nothing about it, echoed in the result |
| `startAt` | launch | `null` | `validate\|draft\|write\|bootstrap\|conventions\|review-spec\|review-non-spec\|finalize`. Fresh-relaunch resume point. |
| `maxFixGroups` | forward | 7 | the **only** cap on the split. More groups is more focus and more agents; fewer is cheaper and regresses more. Group size is uncapped by design (§5.3) |
| `fixGroupsParallel` | forward | `false` | only safe when the planner guarantees file-disjoint groups |
| `fixDesignDepth` | forward | `"auto"` | `shallow` forces every finding to `trivial` handling (cheap, for a run of bookkeeping findings); `deep` forces the architect path (for a run you already know is design-bound) |
| `verifyOrder` | forward | `["material","evidence"]` | which skeptic short-circuits |
| `verifySequential` | forward | `true` | `false` restores today's parallel pair |
| `introspectEvery` | forward | 5 | rounds between mandatory passes |
| `introspectGate` | forward | `true` | the warrant gate |
| `judgesPerVerdict` | forward | 3 | panel size for non-healthy verdicts |
| `judgesHealthy` | forward | 2 | panel size for `healthy` |
| `falsificationBar` | forward | `"conclusive"` | `"partial"` makes the panel easier to convince |
| `compactAtLines` | forward | 400 | ledger size that triggers compaction |
| `compactGrowthLines` | forward | 150 | growth since last compaction that triggers it |
| `standingContextMaxLines` | forward | 80 | compaction target |
| `maxAutoRestarts` | skill | 2 | skill-side budget for automatic `halt`/`reframe` relaunches |
| `runTag` | **anchored** | proposal stem | namespace for the lens cache, log shards, and state file |
| `resumeState` | launch | `false` | read `scratchpad/cp-state/<runTag>.json` at startup and continue from the recorded round |
| `argsOverridePath` | launch | `scratchpad/cp-args/<runTag>.json` | §8.3 |

**These are `change-proposal`'s arguments. `implement-proposal` needs the same
treatment**, and had none: layer-1 checks 4 and 5 and §8.4's decision table are
written for both scripts, so both need an `ARG_CLASS` and a documented table.
Its new arguments from this plan, none of which were tabulated anywhere:

| arg | class | default | effect |
|:--|:--|:--|:--|
| `leaseTtlHours` | forward | 24 | how long an opened spec lease stays valid before the hook rejects it as stale (§3.2) |
| `maxPhaseOscillations` | forward | 5 | 4↔6 cycles before the stuck judges are woken on that pattern (§9.3) |
| `maxFinalGateFailures` | forward | 5 | final-gate failures in one step before the judges are woken (§9.3) |
| `expensiveTierSeconds` | forward | 300 | ledger median above which a tier needs a named justifying hunk (§9.4) |

Its existing arguments (`implementCode`, `maxApplyRounds`, `maxAlignRepairs`,
`maxPlanRounds`, `maxStepAttempts`, `maxDeadAttempts`, `maxReplans`,
`replanEvery`, `replanStruggleAttempts`, `maxVerifyRounds`, `maxReviewRounds`,
`coverageFloor`, `introspectEvery`, `minUnproductiveRounds`,
`reverifyDoneSteps`, `skipBuild`) are `forward`; `specReviewFocus`,
`acceptedDivergences`, and `plan` are **anchored**. `maxAlignRepairs` is
deleted with the alignment-repair branch under D7.

`change-proposal`'s existing arguments keep their meanings and defaults. Their
classes:
`maxReviewRounds`, `churnWindow`, `churnMinFindings`, `churnStrikes`,
`maxRedesigns`, `redesignReviewRounds`, and `excludeLenses` are `forward`;
`planPath`, `startLenses`, `lensPrompt`, `context`, `exemplar`, `mode`, and
`problem` are **anchored**, because each is baked into prompts the run has
already issued. `focusAreas` is `launch`.

The class column is what §8.4's decision table is looked up against, and layer 1
check 5 keeps it honest: an argument the script reads and `ARG_CLASS` does not
carry is a lint failure.

### 8.3 Changing arguments without a relaunch

Three mechanisms, because the three situations are genuinely different. §8.4 is
how the skill picks between them, which matters more than the mechanisms do.

**(a) Mid-run overrides — the answer to "resume with different arguments".**
At the top of every round, one cheap agent reads
`scratchpad/cp-args/<runTag>.json` and returns its contents (`{}` when absent).
The script merges it over the current knobs and logs what changed. Only
`forward`-class arguments are mergeable (§8.4). An operator, or the skill on
the operator's behalf, changes the file mid-run and the next round picks it up.
No relaunch, no cache invalidation, one `haiku` agent per round.

**(b) `resumeFromRunId` for `forward`-class arguments.** The journal replays a
call only when `(prompt, opts)` are unchanged, so changing an argument that
appears in no earlier prompt resumes cleanly and changes only future stages.
This already works today.

**(c) `resumeState` for `anchored`-class arguments.** At the end of every round
the script writes `scratchpad/cp-state/<runTag>.json` through one cheap agent:
round number, per-loop retired sets, `areaLog`, `introducedMechanisms`,
`history`, `rejected`, `fixedTitles`, `introspections`, `overruledStops`,
`redesignsRun`, and **the arguments the run is currently using**. A fresh
relaunch with `resumeState: true` reads it back and continues from the recorded
round with its convergence state intact. This is what makes a fresh launch with
a changed `prompts` map behave like a resume, which `resumeFromRunId` cannot do.

### 8.4 How the skill chooses, and why a wrong choice is cheap

Three mechanisms described in prose is precisely the kind of thing an outer
agent gets wrong: it will reach for `resumeFromRunId` because that is what the
current skill says, and silently bust the journal cache or silently fail to
apply a new prompt. So the choice is made a lookup rather than a judgement, and
the script guards it rather than trusting the skill.

**Every argument carries a class, in one registry.** `ARG_CLASS` in the script
maps each argument to `forward` or `anchored`:

- `forward` — the value is read at the point it is used and appears in no
  prompt issued before the current round. Every numeric knob, every boolean,
  `excludeLenses`, `verifyOrder`, `fixDesignDepth`.
- `anchored` — the value is baked into prompts the run has already issued.
  `prompts`, `lensPrompt`, `planPath`, `context`, `exemplar`, `startLenses`,
  and the `mode`/`problem` inputs.

Layer 1 gains a sixth check: every `input.<name>` the script reads appears in
`ARG_CLASS`, and the skill's argument table renders each argument's class. So
"which mechanism" is a table lookup on a fact the lint keeps true.

**The skill's decision table**, keyed on what the operator wants, not on the
mechanism:

| Situation | What the skill does |
|:--|:--|
| The run is live and you want to change a `forward` argument | Write `scratchpad/cp-args/<runTag>.json`. Do not stop the run. It takes effect next round |
| The run is live and you want to change an `anchored` argument | `TaskStop`, then relaunch with `resumeState: true` and the new arguments |
| The run died and nothing about the arguments changed | Relaunch with `{scriptPath, resumeFromRunId}` |
| The run died and only `forward` arguments changed | Relaunch with `{scriptPath, resumeFromRunId}` and the new arguments |
| The run died and any `anchored` argument changed | Relaunch fresh with `resumeState: true` and the new arguments |

Row one is the one worth calling out. The `Workflow` tool returns a task id
immediately and the outer agent stays alive, so when you say "this is going
badly, tighten it up" mid-run, the skill writes the override file instead of
killing and relaunching. That is a capability the current pipeline does not
have at all.

**The skill reports the affordance on every launch.** Its launch message names
the `runTag`, the override path, and the `forward` arguments that can be
changed mid-run, so the operator has it without asking and without reading this
document.

**A wrong choice costs tokens, never correctness.** This is what makes the
whole scheme safe to hand to an agent, and it is worth stating explicitly
because it is not obvious:

- Choosing `resumeFromRunId` when an `anchored` argument changed: the prompt
  string differs, so `(prompt, opts)` differs, so the journal misses from the
  first affected call onward and the run re-does that work under the new
  argument. Expensive, correct.
- Choosing `resumeState` when only a `forward` argument changed: a fresh launch
  that reads the state file and continues from the recorded round. Marginally
  wasteful, correct.
- Writing an `anchored` argument into the override file: the script's whitelist
  rejects it, logs that it did, and the round proceeds unchanged. Ignored,
  correct.

There is no combination that produces a wrong result, only a slower one.

**And the script checks the skill's work.** The state file records the
arguments the run is using, and the script reads it at startup on **every**
launch rather than only under `resumeState`. When a state file exists for this
`runTag` and an `anchored` argument differs from the recorded one, the script
logs a loud line naming which argument changed and what that means for the
cached prefix. The skill does not have to be right for the operator to find out
that it was not.

---

## 9. implement-proposal

### 9.1 One execution sequence: the checklist

This subsection is new. It answers a question the rest of the plan had left
open, and it is the reason `.status.md` carries no `specApplied` field.

#### The contradiction that is in the tree today

Three statements about ordering coexist in the current pipeline and they do not
agree.

1. **`FORMAT_CHECKLIST` in `change-proposal.js` permits an interleave.** Its
   own words: *"Interleaving a code step before a remaining spec step is
   allowed where it is genuinely more efficient, and a step that does so states
   why on its line, so an interleave is a deliberate and reviewable act rather
   than an accident."*
2. **The `applicability` lens forbids one**, in its EXECUTION-MODEL INVERSION
   class: *"The pipeline that applies this proposal lands its spec/ edits
   FIRST, verifies them, and commits them as their own commit, before any code
   is written."* A proposal whose spec edit depends on code it also builds is
   reported as unappliable in any order.
3. **`implement-proposal.js` cannot execute one at all.** Its `Apply spec`
   phase derives its own sub-step order from `.spec-changes.md`'s subsections,
   in document order, and lands every spec edit before
   `implement-proposal-build` is invoked. The checklist is never consulted for
   spec work.

So the checklist's `spec` lane is decorative: the proposal writes `S1 · spec —
SPEC-1`, review validates that ordering, and the executor ignores it and uses a
different sequence. There are **two independent orderings of the same
proposal**, and a progress record for each — the tick marks for code, the
Status bullet for spec — which can and do disagree. The `alreadyApplied`
alignment-repair branch in `implement-proposal.js` exists entirely to reconcile
them after they have.

Your question about `specApplied` is the visible end of that. A single fact
saying "the spec is applied" can only be true if spec application is a phase,
and the moment a spec step can land after a code step, there is no such moment
to record.

#### The change

**The implementation checklist becomes the single execution sequence, spec
steps included.** `implement-proposal` stops having a separate spec phase. It
runs one loop over the checklist, and dispatches on the step's lane:

- a **`spec`-lane step** applies the `SPEC-n` deliverables its line names,
  reading their staged text from `.spec-changes.md`, verifies alignment against
  what the proposal stages, runs the rules sweep over its own diff, and commits;
- a **`code`, `schema`, `migration`, `test`, or `docs` step** runs the build
  loop of §9.2;

and both tick the same box when they finish. That is the whole change. The
per-sub-step apply/verify/fix/commit machinery already in `implement-proposal.js`
becomes the spec-lane step handler; nothing about it is thrown away.

#### What this buys

- **`status` is exactly your four values**, with no invented fact and no fifth
  state. Partial progress lives in the checklist, where it is per-deliverable
  rather than a boolean, and where it is already the resumption record.
- **One progress record instead of two**, so they cannot disagree. The
  `alreadyApplied` alignment-repair branch collapses into the same
  already-ticked handling `reverifyDoneSteps` gives a code step: a ticked spec
  step is verified-present rather than re-applied. One mechanism, not two.
- **The lease gets tighter, not looser.** §3.2 had the lease open across the
  whole run with a proposal-wide `allow` list, because the build phase might
  need `spec/`. Per-step, the lease is opened for a `spec`-lane step with an
  `allow` list of exactly the files that step's deliverables target, and closed
  when the step ticks. A `code` step holds no lease at all. That is a much
  narrower grant than either the current hook or my §3.2 draft.
- **A lens class disappears.** EXECUTION-MODEL INVERSION exists only because
  the executor cannot interleave. Once it can, a spec edit that depends on a
  script the proposal builds is an ordinary dependency — `S3 · spec — depends
  on S2 · code` — rather than a defect that forces the proposal to be split.
  Proposals 0065 and 0066 (the migration tooling and the passes that drive it)
  are exactly this pattern and were split for exactly this reason.
- **Three statements become one.** `FORMAT_CHECKLIST` already describes the
  behaviour; the lens and the executor are what lag.

#### Lanes and the standard pattern

Unifying the sequence does not mean the order becomes arbitrary. Two rules,
one hard and one a norm, and they are what keep the unified loop well-behaved.

**Hard: one lane per step.** A step carries exactly one lane and names
deliverables of that lane only. A step naming both a `SPEC-n` and a `CODE-n`
deliverable is a defect, because under the unified sequence **the lane is the
dispatch key** — it selects the step handler — and a step with two lanes has no
handler. This was previously a soft preference that nothing enforced; it is now
structural. `FORMAT_CHECKLIST` already pushes this way ("Prefer one deliverable
per step. Bundle two only when separating them gains nothing, which means they
touch the same file"), since two deliverables in one file are necessarily one
lane. The rule just makes it explicit and checkable.

**The norm: spec steps first, in a leading block.** The standard checklist is
every `spec` step at the top, then the rest. `FORMAT_CHECKLIST`'s existing
sentence stands unchanged and becomes the operative rule rather than an
aspiration:

> Interleaving a code step before a remaining spec step is allowed where it is
> genuinely more efficient, and a step that does so states why on its line, so
> an interleave is a deliberate and reviewable act rather than an accident.

The norm is not decoration. It earns its place three times over.

1. **Fewer lease windows, all of them before any code has run.** This is a
   defence-in-depth argument rather than the guarantee; the guarantee is stated
   in §3.2 and holds under any lane order. With the spec steps in a leading
   block the lease opens and closes across a contiguous prefix and the last
   window shuts before the first code agent starts, so on a conforming proposal
   no code-writing agent ever runs in a session where `spec/` has been unlocked.
   An interleaved proposal reopens a window later, after code has landed. Both
   are safe, because a `code` step refuses to start while a lease is held
   (§3.2), but the conforming shape has fewer windows and a smaller blast
   radius if that check ever has to fire.
2. **Spec-only mode stays total.** `implementCode: false` becomes exactly "run
   the leading `spec`-lane prefix and stop". When the norm holds, that prefix
   *is* every spec step, so `close-build-gaps.sh --mode proposals` keeps working
   with no change in guarantee. It errors only on a proposal that genuinely
   interleaves, which is now a visible and justified rarity rather than an
   invisible impossibility.
3. **Code is written against complete landed spec text**, which was the
   original rationale for the phase. The norm preserves it as the ordinary case
   while allowing the exception the phase could not express.

**What justifies an interleave**, so that "states why on its line" is
reviewable rather than a rubber stamp. It qualifies only when the spec text
cannot be written or applied until the earlier non-spec step lands:

- the staged spec edit is the **output of a tool this proposal builds** (a
  migration pass over a register, a generator), which is the `mechanical`
  edit method the apply loop already models;
- the spec text's **content depends on a fact only the built artifact fixes**
  (a generated identifier, a register key, an exact artifact name).

What does not qualify: efficiency, convenience, wanting to test before writing
the specification, or a preference for building first. A step claiming one of
those is a finding.

#### The guarantee that must not be lost

Spec-first is not only mechanical. It means code is written against committed,
reviewed spec text, and that a proposal cannot quietly have its spec bent to
match code that was written first. Dropping the phase must not drop that.

It does not, because the guarantee restates precisely and gets stronger:

> A step may only depend on spec deliverables whose steps are already ticked.

That is checkable from the checklist's own `Depends on:` lines, it is what the
`applicability` lens polices instead of "spec comes first", and it is a
*sharper* rule than the phase was: today a code step may be written against any
landed spec text including text it has nothing to do with, and the phase
boundary says nothing about whether the right spec statement landed. The
restated rule names the dependency.

Concretely, EXECUTION-MODEL INVERSION is deleted and the `applicability` lens's
CHECKLIST clause gains:

> LANES AND ORDER. Each step carries exactly one lane and names deliverables of
> that lane only. A step naming both a spec deliverable and a non-spec one is a
> finding: the lane is the pipeline's dispatch key and a step with two has no
> handler.
>
> The standard pattern is every spec step first, in a leading block, then the
> rest. Report as a finding any step that breaks that order without stating on
> its own line why the interleave is necessary. Then judge the stated reason.
> It qualifies only when the spec text cannot be written or applied until the
> earlier non-spec step lands: the staged edit is the output of a tool this
> proposal builds, or its content depends on a fact only the built artifact
> fixes. Efficiency, convenience, or a preference for building before writing
> do not qualify, and a step claiming one of those is a finding.
>
> Whatever the lane order, every code step's Depends-on names the spec steps
> staging the statements its work implements. A code step that implements a
> spec statement staged by a LATER step is a finding regardless of lanes.

#### What it costs

This is the largest structural change in the plan, and it is a merge of two
phases in `implement-proposal.js` rather than an addition to one. Against that:
it is a net **simplification** — one sequence, one loop, one progress record,
one already-done handler — and it deletes code rather than adding it. The
alignment-repair branch (roughly 90 lines) and the `alreadyApplied` fork go
away.

Two knock-ons to handle:

- **`implementCode: false` (spec-only mode)** means "run the leading
  `spec`-lane prefix and stop". Under the standard pattern that prefix is every
  spec step and the mode is total, so `close-build-gaps.sh --mode proposals`
  is unaffected. On a proposal that genuinely interleaves, the run **errors
  rather than silently skipping**, naming the spec step that sits behind a
  non-spec dependency, because the caller asked for something the proposal's
  own ordering forbids.
- **The build workflow's `plan`** already carries `checklistStep` per step and
  already accepts a caller-supplied sequence. Spec steps join it as steps with
  a lane, which is a schema addition (`lane` on `STEP_ITEM`, a required enum
  with no mixed value), not a redesign. An absent or unrecognised lane is an
  error rather than a guess: the dispatch has no safe default.

#### The alternative, if you want the smaller change

Keep the two phases and `specApplied`, and treat a build-time spec write as a
narrow escape hatch: the lease stays open through the build with a
proposal-wide `allow` list, every write is reported as `specTouchedDuringBuild`,
and the checklist's spec lane stays decorative. This is perhaps a fifth of the
work and it does answer the operational pain — a build that discovers a wrong
spec edit can fix it instead of aborting the run. It leaves the two-sequence
contradiction in the tree, keeps two progress records that can disagree, and
keeps `status` carrying a fact that is only true because a phase exists.

This is **D7**, and my recommendation is to unify.

### 9.2 Folder awareness

`proposalFiles()` from §2.1 is shared. The concrete substitutions:

- `SUMMARY_BLOCK` reads `<stem>.summary.md` (whole file) rather than "the
  `## Summary` section of the proposal".
- The `Plan` agent reads `<stem>.spec-changes.md` for `specEdits`,
  `<stem>.non-spec-changes.md` for `nonSpecStaged`, and
  `<stem>.implementation-checklist.md` for the sequence.
- The checklist tick agent edits `<stem>.implementation-checklist.md`.
- The status update writes `<stem>.status.md` frontmatter through
  `scripts/proposal-status.mjs`, not a prose bullet.
- The `proposal-edit-audit` widens to "any file under the proposal directory
  except `.implementation-checklist.md` and `.deviations.md`", which the phase
  is allowed to write.
- Legacy single-file proposals keep today's section-based prompts through the
  resolver's `legacy` branch.

### 9.3 The step build loop

Replacing the current `implement → verify(tiers) → review(conformance,
invariants)` inner loop with the order you specified:

```
0.  compile gate            go build ./<changed>/...   (cheap; a reviewer must read code that compiles)
1.  implement the step
2.  IC   = conformance + invariants review of the step's diff        ← no tests run
3.  while IC findings:  fix(IC) → recompile → goto 2
4.  TESTS = scope decision (§9.3) + run
5.  while test failures: fix → run the subset the fix warrants → repeat until green
6.  if step 5 changed anything:
      a. IC' = conformance + invariants, scoped to the diff step 5 produced
      b. while IC' findings: fix → goto 6a
      c. if 6b changed anything: goto 4, scoped to 6b's diff
7.  FINAL GATE: the full tier set the checklist names, over the finished tree
      not green → record the miss (below), then goto 5 with its failures.
                  The gate re-runs when the loop reaches 7 again; a step is
                  never ticked on a gate that did not pass.
8.  tick the checklist box
```

Why this is cheaper: the current loop calls the independent verifier on every
attempt, so a step that takes six attempts to satisfy the conformance reviewer
runs its tier set six times. Under the new order the tier set runs once at step
4, again only for what a fix actually touched, and once at step 7. Your
observation that most fixes are conformance and invariant fixes is exactly the
case this optimises.

Why step 0 exists: moving the review before the tests means the reviewer may
read code that does not compile, which produces confident nonsense. A
`go build` on the changed packages is seconds and removes the failure mode.

Why step 7 is unconditional: your requirement that every test the checklist
names runs before the step is ticked. It runs against the exact tree being
ticked, which is the guarantee every scoped run in between borrows against —
the property the existing `fullVerifyCurrent` flag protects, kept and made
mandatory rather than conditional.

**What happens when the final gate is not green**, stated rather than left
implied. Its failures go back to **step 5**, not to step 4: step 4 decides a
test scope, and at step 7 there is nothing to decide because the gate has
handed over concrete failures. Step 5 fixes them and runs what the fix
warrants; step 6 then fires as it would for any other fix, since step 5 changed
something; and the loop arrives back at step 7, which runs again. The gate is
not a one-shot check that a step may pass on the second attempt without
re-running — a step is ticked only on a gate that passed over the tree being
ticked. Today's script already behaves this way (a failed full pass sets
`stepGreen = false` and `continue`s into the attempt loop); this makes it
explicit under the new ordering.

**Every final-gate failure is a scoping miss, and is recorded as one.** The
scoped runs in steps 4 through 6 asserted that a fix could not affect some
tier; the full pass just proved otherwise. That is the only direct evidence
available about whether the change-class table in §9.4 is calibrated, and it is
worth more than intuition. Each failure appends
`{step, tier, whatWasSkippedAndWhy, whatFailed}` to
`scratchpad/test-times/<branch>.misses.json` alongside the cost ledger, and the
run result carries the list. A table row that keeps producing misses gets its
minimum tier set raised; a run with no misses over several steps is evidence
the scoping is sound rather than merely cheap.

`maxFinalGateFailures` (default 5) per step is a detector rather than a bound:
reaching it means the scoped runs are systematically missing something, so it
wakes the stuck judges rather than waiting for `introspectEvery`, with the miss
records in their brief. It does not stop the step. See the counter table below.

#### Counters: what stops, and what only wakes the judges

An earlier draft of this section listed four counters as "loop bounds" without
saying what reaching one does. Working that out removed two of them.

**Nothing new stops a step.** That follows from your own rule for the implement
side: a stuck loop is resolved by filing a deviation, and the build loop is not
otherwise halted or short-circuited. Adding hard caps per phase would have
created three new ways to halt a step that are not the deviation path, which
contradicts it. The stopping conditions are unchanged and there are three:

| Condition | Default | Effect |
|:--|:--|:--|
| `maxStepAttempts` | 50 | **hard stop.** The step aborts and the sequence aborts rather than building dependents on it. Existing behaviour |
| `maxDeadAttempts` | 3 | **hard stop.** Consecutive dead agents mean an account or transport failure no retry clears. Existing behaviour, and not a statement about the code |
| judges return `unproductive` | — | **hard stop.** A legal change exists and the loop is not making it, so the work goes to a human rather than being ticked over. Existing behaviour (§9.6) |

Everything else is a **detector that wakes the judges**, which is the same
division `change-proposal.js` already records for its own introspection: a
counter cannot tell a hard-but-converging step from a stuck one, so its output
is a reason to look rather than a decision. The judges then reach one of the
three verdicts they already have, and `unresolvable` files a deviation (§9.5)
so the loop continues rather than halting.

| Detector | Default | Pattern it sees | Evidence it hands the judges |
|:--|:--|:--|:--|
| `introspectEvery` | 5 attempts | a step not converging, generally | the round log, now phase-tagged (§9.6) |
| `maxPhaseOscillations` | 5 | the 4↔6 cycle: every fix to one check breaks the other | both phase logs, and which fix broke which check |
| `maxFinalGateFailures` | 5 | the scoped runs are systematically missing a tier | the miss records above |

**Why `maxICRounds` and `maxTestRounds` are gone.** Both would have fired on
"one phase has run many rounds", and `introspectEvery` already catches that: it
counts attempts across phases and every IC fix and every test fix is an
attempt, so at eight rounds in one phase the cadence has already fired at five.
They were two knobs restating a signal the run already has. The two detectors
that survive earn their place because each sees something the attempt cadence
structurally cannot: a cadence counting total attempts cannot distinguish
oscillation from steady progress, and it cannot see a scoping miss at all.

**A fired detector re-arms rather than re-firing.** After a counter wakes the
judges it resets and must accumulate its full count again before firing a
second time. Without that, every oscillation past the threshold costs its own
three-judge panel, and the machinery meant to diagnose an expensive loop
becomes the expensive part of it.

### 9.4 Deciding which tests to run

Two mechanisms, because "be smarter" needs to be made mechanical or it
regresses to "run everything".

**A change-class classifier, grounded in a real diff.** It classifies what git
says changed, never what an agent says it changed or remembers asking for.
This is the highest-stakes decision in the section — its output is what *not*
to test — and the pipeline has already learned this lesson once, in
`change-proposal.js`: the post-fix reviewer was being asked about drift from
the fixer's own prose summary, *"which is precisely the document that omits an
edit the fixer did not notice making"*. The same omission here is worse,
because the consequence is a skipped tier rather than an extra round.

Four rules make it concrete:

1. **It is handed a git range, not a description.** The build workflow already
   captures `stepRef` and `baseRef` with `git rev-parse HEAD`; the classifier
   gets `<preFixRef>..HEAD` for a scoped re-run and `<stepRef>..HEAD` for the
   final gate, which are two different questions and two different refs.
2. **It runs the diff itself** rather than receiving diff text. Carrying a few
   thousand lines through an agent return value costs more than the work it
   feeds, and an agent asked to return that much verbatim summarises it
   instead — the reason the change-proposal loop passes snapshot *paths* rather
   than diffs.
3. **The fix agent's self-report is advisory and adversarial.** Where the diff
   and the report disagree, the diff wins, and the disagreement is itself
   reported: a fixer that changed more than it said it did is worth knowing
   about independently of this classification.
4. **The classification is justified per hunk**, in the structured output, so
   the reason a tier was skipped is inspectable rather than asserted.

**The three cheapest classes are decided by a script, not by the agent.**
`comment-only`, `doc-only`, and `test-only` carry the most risk if they are
wrong — `comment-only` skips everything above tier 0 — and all three are
mechanically decidable, so they do not go to an agent at all.
`scripts/classify-diff.mjs <range>` reports:

- `comment-only` when every added and removed line in a code file is a comment
  or blank (a small state machine for block comments);
- `doc-only` when every changed file has a non-code extension;
- `test-only` when every changed file is `_test.go` or under `tests/`.

Its verdict is **authoritative where it fires**. If the script says the diff is
not comment-only, the agent may not classify it as comment-only, whatever the
fix agent reported. This is the same split as `check-proposal-split.mjs` in
§2.4: agent judgement where judgement is needed, a script where the guarantee
matters. It closes the failure mode that worries me most in this design — a
fixer that adjusts one line of logic while editing the comment above it,
self-reports "updated a comment", and has every test that would catch it
skipped.

The remaining classes need judgement and stay with the agent. The class maps to
a **minimum** tier set through a table held in the script:

| class | minimum tiers |
|:--|:--|
| `comment-only` | 0 |
| `doc-only` (not runbook/alert/metric) | 0 |
| `doc-only` (runbook, alert, or metric page) | 0, 11 |
| `test-only` | 0, plus the tier that owns the test |
| `rename-local` (no exported identifier crosses a package boundary) | 0, 1 for the package |
| `logic` | 0, 1, plus the step's tiers whose subject the diff touches |
| `wire` (proto, JSONL, HTTP, CRD schema) | + 3 |
| `security` (auth, isolation, egress, credentials) | + 9 |
| `concurrency` (ordering, atomicity, rate) | + 7a |
| `schema` | + 0 schema checks, + 3 |
| `chart` | + 5 |

The scoping agent may add a tier above the table, and must state the specific
hunk that requires it. It may go **below** the table only for `comment-only`,
`doc-only`, and `rename-local`, which is where today's conservatism costs the
most — and for the first two of those the script has already decided, so the
agent is acting on a mechanical fact rather than its own reading. Everything it
skips is named, with the reason and the hunks it read, in the step result.

The miss records from §9.3 carry the classification that produced them, so a
final-gate failure traces back to the specific class and hunk that justified
skipping the tier. A class that keeps appearing in the miss log gets its
minimum tier set raised.

**A test-cost ledger.** `scratchpad/test-times/<branch>.json`, keyed by
`tier|package`, holding `{ medianSeconds, runs, lastRun }`. **No agent of its
own**: the verifier that just ran the tiers already knows the wall-clock, so it
appends the entries before it returns, and the scoping agent reads the ledger
in the same Bash call it uses to read the diff. The same rule applies to the
miss records of §9.3, which the final-gate agent appends. The lease open and
release do **not** fold into anything: they are the security boundary and their
failure is not benign, so each is a dedicated `haiku` agent given one exact
command (§5.5). Across the implement side this plan adds two agents per
`spec`-lane step and none per code step.

The ledger turns "the more expensive the test, the more the agent should think"
into a rule: for any tier whose ledger median exceeds `expensiveTierSeconds`
(default **300**), the scoping agent must name, in one sentence, the hunk that
requires it. No named hunk, no run. For a tier with no ledger entry, it runs
once and the entry is created — an unknown cost is treated as expensive but is
never a reason to skip a first run.

The ledger is per-branch and gitignored (`scratchpad/` already is), so it warms
up over a run and across steps, which is what you asked for.

### 9.5 Deviations

`<stem>.deviations.md`, owned by `implement-proposal-build`.

```
# Deviations — 0081_fix_some-proposal

## D1 · step S7 · 2026-08-31 · accepted
**Status:** accepted            (proposed | accepted | withdrawn)
**Proposal says:** … (`.spec-changes.md` § SPEC-3)
**Implemented instead:** … (pkg/gateway/router.go:214)
**Why:** …
**Consequence if the proposal is not corrected:** …
**Suggested next step:** correct the proposal | file a follow-up proposal | no action
**Evidence:** commit a1b2c3d; judges motion/high, remaining-work/high, forecast/high
```

Who may write what:

- A **fix agent** may record a deviation only as `proposed`. It may not accept
  one, and a `proposed` deviation does not suppress anything.
- The **stuck judges** promote `proposed` → `accepted` when all three return
  `unresolvable`, and **create** an `accepted` entry outright when no
  `proposed` one matches, which is the common case since a finding usually
  reaches them without any fixer having proposed anything. This replaces
  today's in-memory `suppressedNote()`, which dies with the step.
- The **stuck judges** are the only mechanism that accepts one, and an
  `unresolvable` verdict with no matching `proposed` entry **creates** an
  `accepted` entry rather than failing to find one to promote (§9.6).
- Nothing withdraws a deviation automatically; a human does, or a later
  proposal supersedes it.

**Reviewers read the file.** The conformance and invariants lenses, both
per-step and whole-change, are given the deviations file and told: an
`accepted` deviation is adjudicated; do not report it, a rephrasing of it, or a
near neighbour, and do not treat leaving it alone as leaving the step
unfinished. This is the durable version of today's `acceptedDivergences`
argument, which a caller has to supply by hand on every relaunch. The argument
stays, and its entries are written into the file at startup so the two
mechanisms are one mechanism.

Script-side belt and braces: after the reviewers return, the script drops any
finding whose title fuzzy-matches an `accepted` deviation title, and logs that
it did. Prompt suppression alone has been observed to leak.

The file is returned in the run result and reported by the skill as the run's
final statement of what did not land as proposed.

**change-proposal reads it too, when it exists.** §2.2 grants the read and
nothing was using it. A proposal being re-converged after a partial
implementation has a `.deviations.md` that is the best evidence in the
repository about where its design proved unbuildable — better than any lens's
reasoning, because it was established by trying. So the review lenses and
`fix-design` are given it, with the framing that an `accepted` deviation is a
place the tree won an argument with the document, and that the proposal should
usually be corrected toward it rather than restated. A `proposed` entry is a
lead to verify rather than evidence.

### 9.6 Implement-side introspection

Two changes, both small, matching your "no major changes".

**The stuck detector must see the new loop shape.** `stepRounds` entries gain a
`phase` field (`implement`, `IC`, `tests`, `IC'`, `tests'`).
`unproductiveRunLength` counts trailing rounds **within the same phase** that
committed and still returned findings, and `formatRounds` prints the phase, so
the judges can see the pattern that the new structure makes possible: a loop
that keeps clearing IC, breaking tests, and returning to IC. A new
`maxPhaseOscillations` counter (default 5) on the 4↔6 cycle wakes the judges on
that pattern specifically. At this default the attempt cadence has usually
fired first, since one oscillation is at least two attempts, so this is not an
*earlier* alarm so much as a **better-evidenced second look**: the panel it
convenes is handed both phase logs and which fix broke which check, which the
cadence panel does not have.

**Breaking a stuck loop means filing a deviation, and nothing else.** When the
judges return `unresolvable`, the resolution is now to **write an `accepted`
entry into `.deviations.md`** and continue. The step is not halted, not
short-circuited, and no other escape exists — which is what you asked for. The
`unproductive` verdict is unchanged: the finding is real and legal, so the step
still stops and the outstanding work goes to a human, and no deviation is
filed for it.

### 9.7 Reconciling the proposal with what actually landed

§2.2 grants `implement-proposal` a narrow write on `.non-spec-changes.md` "to
resolve an irreconcilable conflict", and your brief called for exactly that.
Nothing in the plan implemented it. This section does, and bounds it hard,
because an implementor that may edit the document it is measured against has
inverted the pipeline.

**The case is real and it is not the deviation case.** A deviation records that
the *code* departs from the proposal; the proposal stays coherent and a human
decides later. This is different: the proposal contradicts *itself* after the
spec lands, so there is no coherent target to deviate from. The likely cause is
mechanical. `SPEC_RULES` in `implement-proposal.js` **forces** deviations at
apply time — most sharply, *"a staged edit that introduces a brand-new section
or subsection is appended at the end of its level and numbered as the next
ordinal"*. So `.spec-changes.md` stages a new §4.6.4, the applier appends it as
§4.6.7 and records the deviation, and every sentence in `.non-spec-changes.md`
and the checklist that says "implement §4.6.4" now cites a section that does
not exist. No code change fixes that; the proposal text is what is wrong.

**The mechanism.** After each `spec`-lane step, a reconciliation agent runs:

1. Read that step's recorded apply deviations (the `deviations` array of
   `APPLY_RESULT`, which already carries `{id, rule, original, replacement}`).
2. Grep `.non-spec-changes.md`, `.implementation-checklist.md`, and
   `.summary.md` for references each deviation invalidates: a section number,
   an identifier spelling, an anchor quote.
3. Rewrite **only those references**, to what landed.
4. Append a `## Reconciliations` entry to `.deviations.md` for each: what the
   proposal said, what landed, what the reference now reads, and the apply
   deviation that caused it. The edit is visible in the same place everything
   else is.

**The bound, which is the whole point.** It may change a reference that a
**recorded apply deviation makes false**, and nothing else. It may not change
what the non-spec staging asks for, add or remove a deliverable, or resolve a
substantive contradiction. When the inconsistency is substantive — the
non-spec staging asks for something the applied spec makes impossible — the run
**stops** with `status: "proposal-inconsistent"`, naming both texts and the
question. Deciding what the proposal should have said is a human's call, which
is the rule the build phase already states and this does not weaken.

So the narrow write is mechanical bookkeeping downstream of an edit the
pipeline itself forced, and every substantive conflict still goes to a person.

---

## 10. Files touched

| File | Change |
|:--|:--|
| `.claude/workflows/change-proposal.js` | `FORMAT_CHECKLIST` lane rules, `applicability` CHECKLIST clause rewritten and EXECUTION-MODEL INVERSION deleted (§9.1); large rewrite: init, six-lens Validate, six-stance Draft, folder-aware Bootstrap, two review loops, sequential verify, fix-plan/fix-design/grouped fix, lens cache, log shards, compaction, introspection gate and panels, prompts map, state file, mid-run overrides |
| `.claude/workflows/implement-proposal.js` | folder-aware paths, lease open/release on every return path, status through `proposal-status.mjs` |
| `.claude/workflows/implement-proposal-build.js` | folder-aware paths, new step loop, change-class classifier, cost ledger, deviations file, phase-aware stuck judging |
| `.claude/skills/change-proposal/SKILL.md` | rewritten procedure, the new layout, every argument with its default and its effect, the auto-restart rule |
| `.claude/skills/implement-proposal/SKILL.md` | rewritten procedure, the lease, the new step loop, deviations reporting |
| `.claude/settings.json` | the hook replaced by the lease check |
| `scripts/proposal-status.mjs` | new: read and write the status frontmatter. Stays under `scripts/` because the hook and operators call it |
| `.claude/workflows/migrate-proposal.js` | new: the lazy per-proposal migrator, called by both parents at startup (§2.4) |
| `scripts/check-proposal-split.mjs` | new: deterministic partition check the migrator runs |
| `scripts/classify-diff.mjs` | new: deterministic `comment-only` / `doc-only` / `test-only` verdict over a git range (§9.4) |
| `scripts/cp-round-boundary.sh` | new: the whole per-round bookkeeping as one script a `haiku` agent invokes (§5.5) |
| `.claude/tests/` | new: runner, harness, the five layers, fixtures, goldens (§11.0) |
| `scripts/check-workflow-scripts.mjs` | **moved** to `.claude/tests/lint-workflows.mjs` and extended |
| `scripts/test-stuck-judging.mjs`, `test-reverify-done-steps.mjs`, `test-spec-review-focus.mjs` | **moved** into `.claude/tests/*.test.mjs` |
| `tests/registers/residual-change-graph-coverage.yaml` | four rows deleted, eight added for the new `.mjs`/`.sh` files. Tier 0 fails without this |
| `close-build-gaps.sh` | rules B, S1, P: lease and status-tool wording, and proposal resolution that accepts **both** layouts |
| `.claude/workflows/build-gaps-spec-unblock.js` | proposal-path handling for folders |
| `BUILD-GAPS.md`, `PROPOSAL-QUEUE.md`, `tests/tier11_docs/spec_28_index_rows_test.go` | **not touched by this work.** Each is retargeted by the migrator, per proposal, at the moment that proposal migrates (§2.4) |
| `.claude/rules/spec-driven-development.md` | one paragraph: the proposal is a directory; the immutability rule applies to the directory |

---

## 11. Tests

### 11.0 Where they live, and why they are not product tests

Everything in this section lives under **`.claude/tests/`**, beside the
workflows and skills it exercises. Nothing goes under `tests/`, nothing is
selected by any `lenny-test` tier, and nothing is reachable from
`tests/change-graph.json`. These tests are about the agent harness; the tier
model in `test-coverage.md` is about the product, and mixing them would put a
Node script that stubs `agent()` into the same suite as an envtest that stands
up a kube-apiserver.

```
.claude/tests/
  run.mjs                       runner: discovers *.test.mjs, runs them, prints a
                                summary, exits non-zero on any failure.
                                `--lint` runs layer 1 alone; `--record` re-records goldens.
  harness.mjs                   runWorkflow(scriptPath, args, stubs) → { result, calls, logs },
                                stub builders, and the assertion helpers.
  lint-workflows.mjs            layer 1. Today's scripts/check-workflow-scripts.mjs,
                                moved here and extended.
  change-proposal.test.mjs      layer 2
  implement-proposal.test.mjs   layer 2
  golden.test.mjs               layer 3
  tools.test.mjs                layer 4 (proposal-status, migrate-proposals)
  hook.test.sh                  layer 4 (the guard hook, real shell)
  smoke.md                      layer 5: the manual procedure and what to look for
  fixtures/                     fixture proposals and trees (.md — no register rows needed)
  golden/                       recorded prompt digests (.txt/.json — no register rows needed)
```

The three existing behavioural tests (`scripts/test-stuck-judging.mjs`,
`scripts/test-reverify-done-steps.mjs`, `scripts/test-spec-review-focus.mjs`)
and `scripts/check-workflow-scripts.mjs` move here in phase 0 and are folded
into the two `*.test.mjs` files and `lint-workflows.mjs`. `scripts/` keeps only
the two tools the *pipeline itself* calls (`proposal-status.mjs`, which the
guard hook and both skills invoke, and `check-proposal-split.mjs`, which the
migrator invokes), because those are production tooling rather than tests.

Entry point: `node .claude/tests/run.mjs`. **Nothing outside `.claude/`
references it** — no `Makefile` target, no tier, no CI job — so the suite is
self-contained with the workflows and skills it tests, a red workflow test
never blocks a product build, and a red product build never hides a workflow
regression. The two SKILL.md lines that say "run `node
scripts/check-workflow-scripts.mjs .claude/workflows/*.js` after editing a
workflow" become "run `node .claude/tests/run.mjs`".

**One chore not to forget.** `tests/registers/residual-change-graph-coverage.yaml`
requires a disposition row for every tracked file that no change-graph glob key
covers, excluding a documented set of data extensions. `.md`, `.json`, `.yaml`,
and `.txt` are excluded, so fixtures and goldens are free, but every `.mjs` and
`.sh` file needs a row with `disposition: excluded` and the reason already used
for the existing four ("a node behavioural test over an agent workflow driver,
run with node rather than selected by a test tier"). That is eight new rows and
four deletions. `residual_gate_test.go` in tier 0 fails without them, which is
the one way this work can break the product suite. It is also the reason the
file count is kept deliberately small: one file per layer with many assertions
inside, following the existing pattern, rather than one file per test case.

### 11.1 The five layers

| Layer | What it runs | Agents | Runtime | Runs when |
|:--|:--|:--|:--|:--|
| 1 Static | parses the workflow scripts | none | < 1 s | every edit |
| 2 Behavioural | the **real** workflow body, `agent()` stubbed | stubbed | seconds | every edit |
| 3 Golden | the real body in record mode, prompts digested | stubbed | seconds | every edit |
| 4 Tools and hook | `proposal-status`, `migrate-proposals`, the hook command | none | seconds | edits to the layout, tools, or hook |
| 5 Live smoke | two real workflow runs | **real** | tens of minutes, real tokens | once per phase group, and before merge |

Layers 1–4 verify the **machine**: which agent runs, in what order, told what,
and what the script does with the answer. They cannot verify the **judgement**:
whether a model given the `fix-design` brief actually triages instead of
over-thinking a typo, whether the log conventions survive contact with twelve
parallel lenses, whether the change-class table makes the build cheaper rather
than merely different. Every claim in this plan about token savings is a
hypothesis, and layer 5 is the only thing that tests it. That asymmetry is why
layer 5 is in the plan at all rather than being left to "we'll see how it goes".

---

### Layer 1 — Static gates (`lint-workflows.mjs`)

Parses each workflow the way the runtime does: strips `export const meta`,
wraps the body in an async function with the sandbox globals, and constructs
it. `node --check` treats the file as a module and misses this class, which is
why the existing checker exists.

*Existing checks, kept:* parse under the runtime wrapper; an uppercase
identifier referenced but never declared (this has shipped three times, each
time throwing hours into a run); a `require(` call, which the sandbox does not
define and which used to degrade silently inside a `try`/`catch`.

*New checks:*

1. **Label uniqueness within a round scope.** Every `agent()` label built in one
   round must be distinct. A duplicate breaks both the runtime journal cache and
   the new lens cache, and does so invisibly — the second call returns the
   first's result.
2. **Prompt-key registry drift.** Every key in the script's `PROMPT_KEYS`
   constant is documented in the matching `SKILL.md`, and every key the skill
   documents exists in the script. This is the gate that keeps script and skill
   from drifting, which is how the current pair drifted.
3. **Argument documentation.** Every `input.<name>` the script reads appears in
   the skill with a stated default.
4. **No hardcoded proposal paths.** Every prompt string containing `only file
   you may edit` interpolates a value from `proposalFiles()` rather than a
   literal path. This is what stops the folder migration from half-landing.
5. **Argument-class registry.** Every `input.<name>` the script reads appears in
   `ARG_CLASS` as `forward` or `anchored`, and the skill's argument table
   renders the same class. This is what makes §8.4's decision table a lookup
   rather than a judgement, so it has to stay true.
6. **Sandbox surface.** No global outside `agent`, `parallel`, `pipeline`,
   `phase`, `log`, `workflow`, `budget`, `args` and the JS built-ins is
   referenced — a generalisation of the `require` check that also catches
   `process`, `fs`, `Date.now`, and `Math.random`, the last two of which break
   workflow resume.

*Cannot catch:* anything about what the script does.

---

### Layer 2 — Behavioural, real script with stubbed agents

The load-bearing layer, and an extension of the pattern the repository already
uses in `scripts/test-stuck-judging.mjs`. The workflow body is executed for
real; only the boundary to the model is replaced.

```js
const { result, calls, logs } = await runWorkflow(
  ".claude/workflows/change-proposal.js",
  { mode: "review", proposalPath: "proposals/0081_fix_x", repoRoot: "/repo", date: "d", ... },
  {
    // keyed by label prefix; returns the canned structured output
    "review:":       ({ label }) => ({ coverage: "…", findings: FINDINGS_FIXTURE }),
    "verify.material": () => ({ confirmed: true, reason: "" }),
    "fix-plan":      () => ({ groups: [ … ] }),
    ...
  },
);
```

`calls` is the ordered list of `{ label, prompt, opts }` the script made. Three
kinds of assertion are written against it.

**(a) Control flow** — which agents ran, in what order, how many times, and
which did not run. This is where the interesting behaviour lives: the
short-circuit in sequential verification is *the absence* of a
`verify.evidence` call; the IC-before-tests ordering is *the absence* of a test
agent before the IC agents go clean; the introspection gate is *the absence* of
the full pass.

**(b) Prompt content** — that a given agent was told the right thing. Not the
prose, but the load-bearing substrings: the file it may edit, the design object
it must apply, the tier list it must run, the cache path, the shard path, the
suppression clause.

**(c) Result shape** — the returned status and the fields a caller branches on.

Split across two files.

**`change-proposal.test.mjs`**

| # | Assertion |
|:--|:--|
| B1 | `Init` names all six skeleton paths, and no agent before `Bootstrap` names a path outside the proposal directory |
| B2 | `Validate` dispatches exactly six lens agents plus one consolidator; a `null` from one lens does not crash and is recorded as incomplete |
| B3 | a consolidator returning `viable: false` returns `not-viable` and no `Draft` agent runs |
| B4 | `Draft` dispatches six stances plus one consolidator, and `Challenge` still runs once per surviving change |
| B5 | `Bootstrap` on a legacy single file names all eight split targets and runs the partition check afterwards |
| B6 | `Bootstrap` on a complete folder returns SKIPPED and no fixer runs |
| B7 | the spec loop is skipped when `probe:spec-changes` reports none, and the non-spec loop still runs |
| B8 | no `review-non-spec` agent runs before `review-spec` converges |
| B9 | `lockSpecChanges: true` — the non-spec fix prompt names only the non-spec editable set; `false` — it names `.spec-changes.md` too and a round that touched it sets `specTouched` |
| B10 | sequential verify, refuted: materiality returns `confirmed: false` → **no `verify.evidence` call** for that finding |
| B11 | sequential verify, confirmed: evidence runs, and the finding is confirmed only when both confirm |
| B12 | `verifyOrder: ["evidence","material"]` reverses the order |
| B13 | `fix-plan` returning more than `maxFixGroups` groups → clamped and logged |
| B13b | a round confirming far more findings than `maxFixGroups` still partitions: every finding lands in a group, no group is dropped, and the large count is recorded in the round history for introspection |
| B14 | `fix-plan` dropping a finding, or assigning one to two groups → fallback to a single group, logged |
| B15 | `fix-design` runs once per group, and its `chosen`, `alternatives`, and `doNotDo` appear verbatim in the matching `fix` prompt |
| B16 | `fix` runs once per group in `order`, sequentially |
| B17 | `post-fix` runs once per round after the last group, and its diff instruction names the snapshot taken before the **first** group |
| B18 | every lens prompt carries the cache instruction, keyed on lens, round, and a content hash of the change files; **no cache-clear agent exists** |
| B18b | the round-boundary agent's prompt is one exact `bash scripts/cp-round-boundary.sh …` invocation and a "do nothing else" clause; it carries no other instruction |
| B18c | a non-zero exit from the boundary script marks the round incomplete, so it cannot certify convergence |
| B19 | every parallel agent's prompt names a distinct shard path; the round-boundary agent runs once per round; no two agents in one `parallel()` batch are told to write `.review-log.md` |
| B20 | compaction fires above `compactAtLines`, does not fire below it, and fires on growth above `compactGrowthLines` |
| B21 | introspection gate: counter wake with `warranted: false` → verdict `healthy`, `gated: true`, no full pass |
| B22 | introspection gate: **cadence** wake with `warranted: false` → the full pass runs anyway |
| B23 | a `healthy` preliminary verdict convenes a panel of `judgesHealthy`, and stands with no conclusive falsification |
| B24 | a majority falsifying at `conclusive` flips the verdict to the least disruptive `fallbackVerdict` named |
| B25 | the `redesign` panel's prompts carry the `fix-design` principles block; the `reframe` panel's prompts name `.problem-statement.md` |
| B26 | `halt` with `confidence: "clear"` returns `proposedNextSteps.rerun` whose `args` validate against the argument schema (the test validates them) |
| B31 | the `applicability` lens prompt carries the LANES AND ORDER clause and no longer carries EXECUTION-MODEL INVERSION; `FORMAT_CHECKLIST` carries the one-lane rule |
| B27 | **convergence guarantee, per loop:** a sweep that finds nothing but had a failed lens does not converge. A regression test for the existing property under the new structure |
| B28 | `prompts["review.security"]` reaches only the security lens; `prompts["review"]` reaches every lens; an unknown key throws; `promptsApplied` echoes what was applied |
| B29 | a stubbed override file changes `maxFixGroups` from the next round; an `anchored` key in it is **rejected and logged**, and the round proceeds unchanged |
| B29b | every `input.<name>` the script reads is present in `ARG_CLASS` (asserted by layer 1, re-asserted here against the live script) |
| B29c | a state file recording a different `anchored` argument makes the script log the mismatch by name at startup, on a launch with **no** `resumeState` |
| B30 | the state-writer agent runs at round end; `resumeState: true` with a stubbed state file starts at the recorded round with the retired sets restored |

**`implement-proposal.test.mjs`**

| # | Assertion |
|:--|:--|
| C1 | the lease is opened before the first spec-editing agent |
| C2 | **the lease is released on every return path** — one case each for `not-approved`, `spec-unappliable`, `spec-not-clean`, `spec-applied-with-blockers`, `not-aligned`, `aborted`, `spec-only`, `implemented` |
| C3 | with IC findings outstanding, no test agent runs until IC is clean |
| C4 | a test-stage fix triggers a scoped IC re-run, and a change from that IC re-run triggers a scoped test re-run |
| C5 | `maxPhaseOscillations` exceeded **wakes the stuck judges** and does not stop the step; the loop continues after a `resolvable` verdict |
| C5b | a fired detector re-arms: a second panel is not convened until its full count accumulates again |
| C5c | the only stops are `maxStepAttempts`, `maxDeadAttempts`, and an `unproductive` verdict; no phase counter aborts a step |
| C6 | the final full tier pass runs over the finished tree even when every intermediate run was scoped, and a step is never ticked on a gate that did not pass |
| C6b | a **failing** final gate sends its failures to step 5, not step 4; the loop then re-reaches step 7 and the gate runs again before any tick |
| C6c | each final-gate failure appends a miss record naming the tier, what was skipped and why, and what failed |
| C6d | `maxFinalGateFailures` reached wakes the stuck judges, with the miss records in their brief, and the step continues |
| C7 | a `comment-only` classification → the verifier prompt names tier 0 only |
| C7b | the classifier prompt names a concrete `<ref>..HEAD` range and instructs the agent to run the diff itself; it is never handed diff text or the fix agent's `filesChanged` as the subject |
| C7c | `classify-diff.mjs` reporting "not comment-only" **overrides** an agent classification of `comment-only`, and the full tier set is requested |
| C7d | a diff that disagrees with the fix agent's self-report is reported as a discrepancy, independently of the classification |
| C7e | the scoped re-run uses `<preFixRef>..HEAD` and the final gate uses `<stepRef>..HEAD`; the two are not conflated |
| C8 | a tier whose ledger median exceeds `expensiveTierSeconds` is requested only when the prompt carries a named justifying hunk |
| C9 | the ledger-write agent runs after each tier run, carrying the tier key and the measured seconds |
| C10 | an `unresolvable` verdict writes an `accepted` deviation, and the next IC reviewer's prompt names the deviations file and carries the do-not-re-report clause |
| C11 | a reviewer returning a finding matching an `accepted` deviation title has it dropped by the script, with a log line |
| C12 | an `unproductive` verdict stops the step and writes **no** accepted deviation |
| C13 | folder awareness: `SUMMARY_BLOCK` names `.summary.md`, the plan agent names `.spec-changes.md`, the tick agent names `.implementation-checklist.md`; a legacy proposal makes all three name the single path |
| C23 | after a `spec` step with a recorded apply deviation, the reconciliation agent runs and rewrites only the references that deviation invalidates |
| C24 | a substantive contradiction returns `proposal-inconsistent` and stops, rather than being reconciled away |
| C25 | every reconciliation appends a `## Reconciliations` entry naming the apply deviation that caused it |
| C14 | `proposal-edit-audit` widened: an edit to `.summary.md` during the build is reported; one to `.implementation-checklist.md` or `.deviations.md` is not |
| C16 | *(D7)* a checklist with lanes `S1 spec, S2 code, S3 spec` executes in that order, and `S3`'s lease is opened after `S2`'s commit |
| C17 | *(D7)* a `code` step opens **no** lease; a `spec` step's lease `allow` list holds exactly the files its `SPEC-n` deliverables target |
| C21 | *(D7)* a `spec` step that fails still releases its lease, and the next step starts with none held |
| C22 | *(D7)* a `code` step that finds a lease held **fails the run** rather than proceeding, naming the step that leaked it |
| C18 | *(D7)* a ticked `spec` step is verified-present rather than re-applied, and a missing edit is repaired in place |
| C19 | *(D7)* `implementCode: false` runs the leading `spec` prefix and stops; with a `spec` step behind a non-spec dependency it **errors**, naming it, rather than skipping |
| C20 | *(D7)* a step naming both a `SPEC-n` and a `CODE-n` deliverable is rejected before the loop starts; an absent or unrecognised `lane` errors rather than defaulting |
| C15 | *(ported)* the three existing behavioural tests: stuck judging, reverify-done-steps, spec-review-focus, re-asserted against the new loop shape |

**`migrate-proposal.test.mjs`** — the lazy migrator (§2.4)

| # | Assertion |
|:--|:--|
| M1 | a legacy path makes both parents invoke the migrator at startup; a directory path makes neither |
| M2 | the split prompt names all eight targets and the source sections each draws from |
| M3 | `check-proposal-split.mjs` is run after the split, and a non-zero exit sends the migrator back to fix the loss rather than proceeding |
| M4 | an `Implemented` proposal is **refused**, and the calling workflow returns without running |
| M5 | idempotence: directory present and legacy file gone → returns immediately, no agent runs. Both present → resumes the split |
| M6 | inbound references are grepped before the legacy file is deleted; an unresolvable reference **stops** the migration rather than landing a broken tree |

*Cannot catch:* whether a real model obeys the prompt it was given. A test can
assert the fixer was told the design; it cannot assert the fixer applied it.

---

### Layer 3 — Golden prompt digests (`golden.test.mjs`)

The same harness, run against a fixture proposal with a record-only stub, then
reduced to a **digest** per stage and diffed against a checked-in golden file.

The digest is deliberately lossy. For each agent call it records: the stage,
the label, the schema name, the ordered list of invariant blocks present (the
file-ownership clause, `RULES_FULL`, `SUMMARY_BLOCK`, `BLANKS_BLOCK`,
`ACCEPTED_BLOCK`, the cache instruction, the log-shard instruction), and a hash
of each block — never the prose around them.

This is what it buys: a refactor that drops `HARD CONSTRAINT: the only file you
may edit is …` from one prompt out of forty is invisible to layer 2 unless a
test happens to assert that specific prompt, and invisible to review because
the diff is large. The digest makes it a one-line failure. Reordering stages,
losing a schema, or silently dropping the preflight note fail the same way.

`node .claude/tests/run.mjs --record` re-records after an intended change, and
the re-record is reviewed as part of the commit.

*Cannot catch:* prose quality, or a block that is present but wrong.

---

### Layer 4 — Tools and the hook, executed for real

No agents, no workflow bodies. Real processes against fixture trees in a
temporary directory.

**`hook.test.sh`** extracts the hook command from `.claude/settings.json` and
feeds it the JSON payload the harness feeds it, against a fixture tree with
fabricated lease and status files. This is the only security-relevant surface
in the whole change, so it gets a real test rather than a stubbed one.

| # | Case | Expect |
|:--|:--|:--|
| H1 | path outside `spec/` | allow |
| H2 | no lease file | **block** |
| H3 | lease names an `Approved` proposal | allow |
| H4 | lease names a `Draft` proposal | block |
| H5 | lease `expires` in the past | block, naming staleness |
| H6 | another proposal is `Approved`, no lease | **block** — the independence property |
| H7 | lease has an `allow` list, path outside it | block, naming the list |
| H8 | lease file is malformed JSON | block (fail closed, not fail open) |
| H9 | `proposal-status.mjs` exits non-zero on the leased proposal | block |

**`tools.test.mjs`**

| # | Assertion |
|:--|:--|
| T1 | `proposal-status.mjs --field status` reads each of the four values; a fifth value is an error |
| T2 | `--set` rewrites only the named field and preserves the rest of the frontmatter and the body |
| T3 | `check-proposal-split.mjs` passes on a faithful split of a fixture legacy proposal |
| T4 | it **fails, naming the lines**, when content of the original appears in none of the outputs |
| T5 | it tolerates headings the split is allowed to add, and blank-line and trailing-whitespace differences |
| T7 | `classify-diff.mjs` returns `comment-only` for a comment-only Go diff, including block comments and a comment containing code-like text |
| T8 | it returns **not** comment-only when a single logic line changes alongside a comment edit — the case the whole script exists for |
| T9 | `doc-only` and `test-only` are decided by path, and a mixed diff falls through to the agent rather than claiming a cheap class |
| T10 | `cp-round-boundary.sh` merges shards, deletes each only after appending it, and is idempotent across a re-run |
| T11 | it detects a file changed outside its owner's allowance and names it; a file changed by its owner is not reported |
| T12 | it exits non-zero on a malformed state file or an unwritable path, so the round is marked incomplete rather than proceeding |
| T6 | `proposalFiles()` resolves both layouts, and the legacy branch points every role at the single file |

*Cannot catch:* anything about the workflows.

---

### Layer 5 — Live smoke (`smoke.md`, manual, not CI)

Two real runs, with the procedure and the things to look for written down so
the observation is repeatable rather than impressionistic.

**S1 — `change-proposal` in `new` mode** against a small seeded problem
statement, at low `maxSpecReviewRounds` and `maxNonSpecReviewRounds`. What to
read afterwards:

- The `fix-design` outputs. Did trivial findings actually get one-line
  treatment, or did the architect brief leak into all of them? This is the
  single most likely way the design in §5.3 fails, and no stub can show it.
- The review log after two compactions. Did the tag vocabulary hold? Did
  compaction promote the entries marked `USEFUL` and retire the ones marked
  `CORRECTS`? Is the Standing context something an agent would actually use?
- The per-round token spend against the same proposal's pre-change run, if one
  exists. The grouping change is a bet that focus beats batching; this is where
  it is settled.

**S1b — the first real lazy migration.** Converge an existing draft (0076 is
the natural candidate: it is `Draft` and small) and read the resulting
directory against the original file. Was the spec/non-spec partition of the
design sections sensible, or did the migrator put implementation detail in
`.spec-changes.md`? Did `check-proposal-split.mjs` pass first time? This is the
one part of the design where the agent has real latitude and the script cannot
check the quality of its judgement, only that nothing was lost.

**S2 — `implement-proposal` in `spec-only` mode** against an already approved
small proposal. What to read:

- The lease's whole lifecycle in `git status` and the logs, including one
  deliberate mid-run interruption to confirm the stale-lease path reports
  rather than silently unlocking.
- The test-scoping decisions in the step logs: which tiers were skipped, with
  what stated reason, and whether the final full gate caught anything the
  scoped runs missed. A single instance of the final gate catching a real
  failure validates the design; a run where it never catches anything after
  several steps is weak evidence the scoping is too permissive.

Output is a note in `tmp/`, not a checked-in artifact. Budget both before phase
10 closes and expect to tune the `fix-design` brief and the change-class table
from what they show.

## 12. Sequencing

Eleven phases, each one commit, each leaving the tree working. Phases 1, 8, and
8b are the ones that must not be split further: a half-migrated layout, a
half-changed step loop, and a half-unified execution sequence are each worse
than either end state.

| Phase | Content | Gate |
|:--|:--|:--|
| 0 | `.claude/tests/` runner and harness, `lint-workflows.mjs` with its checks, the four moved files, the register rows | `node .claude/tests/run.mjs` green on the **unchanged** workflows, and tier 0 still green after the register edit |
| 1 | `proposalFiles()` resolver in both workflows, `proposal-status.mjs`, `check-proposal-split.mjs`, `migrate-proposal.js` and its call from both parents, the status frontmatter, the lease and the new hook, `close-build-gaps.sh` accepting both layouts | H1–H9, T1–T6, M1–M6, C13 |
| 2 | change-proposal `Init`, six-lens `Validate`, six-stance `Draft`, folder-aware `Bootstrap`, `Conventions` over the file set | B1–B6 |
| 3 | Split `runReviewLoop`, the spec/non-spec loops, `lockSpecChanges`, sequential verification | B7–B12, B27 |
| 4 | `fix-plan`, `fix-design`, grouped `fix`, single `post-fix` per round | B13–B17 |
| 5 | Review log format, shards, `cp-round-boundary.sh`, compaction, `READ_ONLY` amended, and the read-the-log instruction in every prompt | B18b, B18c, B19, B20, T10–T12 |
| 6 | Lens cache (content-keyed, no cleanup), state file with recorded args, mid-run overrides, `ARG_CLASS` registry, `startAt`/`resumeState` | B18, B29, B29b, B29c, B30 |
| 7 | Introspection gate, verdict panels, falsification rule, `proposedNextSteps`, skill auto-restart | B21–B26 |
| 8 | implement-proposal step loop restructure (§9.3) including the 7→5 failure path and the miss record, `classify-diff.mjs`, the change-class classifier, cost ledger | C3–C9, C6b–C6d, C7b–C7e, T7–T9, C15 |
| 8b | **D7:** merge the Apply-spec phase into the checklist loop, per-step lease, `lane` on `STEP_ITEM`, delete the alignment-repair branch and EXECUTION-MODEL INVERSION, `implementCode:false` errors on an interleaved dependency, `applicability` CHECKLIST clause rewritten | C1, C2, C16–C22, B31, H3, H7 |
| 9 | Deviations file, judge-driven acceptance, reviewer suppression, phase-aware stuck judging, the §9.7 reconciliation path | C10–C12, C14, C23–C25 |
| 10 | Both `SKILL.md` rewrites including §8.4's decision table and the per-argument class column, `spec-driven-development.md` paragraph, layer-1 drift checks 2, 3 and 5 turned on, golden digests recorded, the two live smoke runs | layer 1 checks 2–3 and 5, layer 3, S1, S1b, S2 |

Phases 2–7 all touch `change-proposal.js` and are ordered so each builds on the
last; they cannot be parallelised across agents against one file. Phases 8–9
touch the implement side and are independent of 2–7, so they can run in
parallel with them on a separate branch if you want the wall-clock. The one
cross-cutting item is phase 8b's rewrite of the `applicability` lens, which
lives in `change-proposal.js`: land it after phase 7 to avoid a conflict, or
carry it as a separate small commit.

Phase 8b is separable from phase 8 in the diff but not in the design: D7 is
taken, so phase 1 builds the per-step lease and the four-value status on the
assumption that 8b lands.

Rough sizing, for planning rather than as a commitment: phase 1 is the largest
single piece of mechanical work (the migration and its inbound references),
phases 3 and 4 are the largest design pieces, and phase 8 is the largest change
to the build workflow. Phases 0, 5, 6, and 9 are each a day's work or less.

---

## 13. Decisions

**D1 — Directory naming. SETTLED: `NNNN_kind_slug`.** The underscore form
preserves the `kind` segment (used by the skill and by BUILD-GAPS and
PROPOSAL-QUEUE cross-references) and matches every existing inbound reference.
No gate forces it either way.

**D2 — Migrating implemented proposals. SETTLED: leave them.** The 20
`Implemented` proposals stay as single files, because
`spec-driven-development.md` says a landed proposal is a historical record and
is not edited, and splitting one into eight files is an edit. The consequence
to live with is a permanently mixed tree, which costs nothing extra: the
resolver has to carry the legacy layout anyway for the 38 in-flight proposals
until those land, and by then the legacy branch is exercised only by history.
`--include-implemented` remains available if you change your mind later; the
split is a partition, so it is reversible and content-preserving.

**D3 — Verification order. SETTLED: materiality first.** Materiality runs first because it is cheaper and
refuses more. The counter-argument is that a materiality judge reasoning about
a finding whose evidence is fabricated may confirm it as material, so the
expensive verifier still runs on a finding that evidence would have killed
instantly. Both orderings save tokens on the majority case; materiality-first
saves more. `verifyOrder` makes it a one-line change either way.

**D4 — Status enum. SETTLED, and it opened D7.** `.status.md` carries exactly
your four values, with no `specApplied` field and no fifth state. You were
right that a single "the spec is applied" fact stops making sense the moment
spec and code can interleave: it can only be true if spec application is a
phase. Partial progress moves to where it already lives and is more
informative — the tick marks in `.implementation-checklist.md`. What that
exposed is D7.

**D5 — Caller prompts on verifiers and judges. SETTLED: expose them, wrapped.** You asked for a custom prompt
per agent type. The current script deliberately withholds caller text from the
verifiers, on the reasoning that a verifier told what to conclude is not a
verifier. I have exposed the keys with a strengthened wrapper and a result
echo, so a run steered at its verifiers is visible after the fact.

**D6 — Non-goals' home. SETTLED: `.summary.md`.** Goals and Non-goals live there rather
than in `.non-spec-changes.md`, on the grounds that the implementor reads the
summary and the non-goals are a constraint on implementation. The dropped
alternatives and their reasons live there too, with the detail in the review
log.

**D7 — One execution sequence. SETTLED: unify.** The full argument is
§9.1. In short: three statements about ordering coexist in the tree today and
contradict each other. `FORMAT_CHECKLIST` permits an interleave with a stated
justification; the `applicability` lens forbids one outright, citing a pipeline
constraint; and `implement-proposal.js` cannot execute one at all, because it
derives its own spec sub-step order from `.spec-changes.md` and lands every
spec edit before the build workflow is invoked. The checklist's `spec` lane is
decorative, there are two independent orderings of one proposal, and the
`alreadyApplied` alignment-repair branch exists to reconcile the two progress
records after they disagree.

Unified on the checklist: one loop, dispatching on the step's lane, with one
lane per step as a hard rule and spec steps leading as the standard pattern
(§9.1). An interleave stays permitted and stays a deliberate, justified,
reviewable act, exactly as `FORMAT_CHECKLIST` already words it. It is the only model in which `status` carries your four values without
an invented fact; it deletes a lens class (EXECUTION-MODEL INVERSION) rather
than adding one; it makes the lease per-step and therefore much narrower than
either the current hook or my own §3.2 draft; and it removes roughly 90 lines
of alignment-repair rather than adding code. The spec-first safety guarantee is
not lost, it is restated more precisely as "a step may only depend on spec
deliverables whose steps are already ticked", which is checkable from the
checklist and is sharper than a phase boundary.

The cost is that this is the largest structural change in the plan and lands in
phase 8b. The rejected alternative was to keep the phase, keep a run-scoped
lease, and treat a build-time spec write as a reported escape hatch: about a
fifth of the work, but it leaves the contradiction in the tree and keeps two
progress records that can disagree.
