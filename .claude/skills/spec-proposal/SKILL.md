---
name: spec-proposal
description: Write an adversarially validated spec change proposal under proposals/ from an inline problem statement, or adversarially review and fix an existing proposal until it converges. Use when the user reports a spec defect, contradiction, or gap, asks for a spec fix or extension proposal, or asks to validate an existing proposal before sign-off. The proposal stages spec edits for sign-off; it never modifies spec/ itself.
argument-hint: <problem statement | path to notes | path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Spec proposal writer and convergence loop

This skill produces a reviewed proposal document in `proposals/` and converges it against the spec and the code. It has two modes sharing one workflow:

- **new**: the input is a problem statement. The workflow validates the problem's premises, drafts a change set, adversarially challenges each change, writes the proposal file, and then enters the review loop.
- **review**: the input is the path of an existing proposal under `proposals/`. The workflow enters the review loop directly.

The review loop is shared: rounds of multi-lens adversarial review, two-skeptic verification of every finding, and fixes, repeated until two consecutive rounds confirm zero findings.

## Hard constraints

- The run creates or edits exactly one file: the proposal (the new file in new mode, the given file in review mode). Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages spec edits as fenced markdown blocks to apply after sign-off.
- All problem input is inline (the argument and the conversation). The skill reads no tracking documents; evidence comes from `spec/`, `schemas/`, `pkg/`, `cmd/`, `charts/`, and git history. Progress-tracking and audit prose elsewhere in the repository are leads to verify, never evidence.
- Prose follows `.claude/rules/doc-style.md`.
- New proposal file names are `NNNN_[new|fix]_<kebab-slug>.md`. `NNNN` is the next free zero-padded number among existing numbered proposals. `fix` is for proposals that correct or reconcile existing spec text (contradictions, wrong ownership assignments, unreachable features). `new` is for proposals that add a capability or component the spec does not describe.
- Review findings are real errors only: false citations, infeasible actor assignments, contradictions, missing edit sites, and broken mechanisms. Style preferences, optional improvements, and hypothetical hardening are excluded by construction (the materiality skeptic refuses them); conventions are handled by a dedicated one-shot pass outside the error loop.

## Proposal format conventions

The writer and reviewer agents receive these conventions. They are derived from the existing files in `proposals/`; when those files and this list disagree, the existing files win.

- Title line: `# Proposal: <title>`.
- Header bullets: `**Status:**` (initially "Draft for review."), `**Date:**`, `**Scope:**` (one- or two-sentence summary naming the finding IDs from the inline input when applicable).
- Staging boilerplate paragraph: "This document stages the proposed spec … edits. It does not modify any spec file. Apply the changes in the … section after sign-off."
- Numbered sections in this order, omitting the ones with no content: Problem; Decisions; Design overview; Detailed design; Observability surface; CRD and RBAC changes; Proposed spec changes; Non-goals; Testing; Findings closed on application; Resolved in adversarial review; Open decisions for review; Files touched on application.
- The Problem section cites spec text as `spec/<file>.md:<line>` or `§X.Y` relative links, cites code as `pkg/...:<line>`, and names the finding IDs the proposal unblocks, if the inline input provided any.
- The Proposed spec changes section has one subsection per target file and section, each with an anchor instruction ("Append after …", "Replace the row …") and a fenced block containing the exact text to insert, written so it can be applied mechanically after sign-off.
- Non-goals records the alternatives that were considered and dropped, with the reason.
- Files touched on application is consistent with the Proposed spec changes section.

## Why the review loop is built this way

These design points come from convergence runs on prior proposals in this repository. Keep them when editing the script.

- **Two consecutive clean rounds, not one.** Fix rounds introduce their own errors: fixers add predicate text that drifts from the design's invariants, and clean rounds have been followed by rounds with confirmed findings in fixer-written text. A single clean round demonstrates nothing about the text the previous fixer wrote.
- **Two skeptics per finding, both must confirm.** One re-derives the evidence from the files; one judges materiality assuming the evidence is true, with instructions to default to refuted. The split kills plausible-but-wrong findings and nitpicks separately.
- **Refuted findings are remembered and injected into later rounds.** Without the memory, a refuted finding resurfaces in a later round, wastes verification, and can block convergence.
- **Dedup before verification.** Independent lenses converge on the same root error under different phrasings; verifying duplicates multiplies cost for nothing.
- **A rotating extra lens.** The fixed lenses develop shared blind spots over rounds because they re-read the same document. The rotating lens (security regression, operational consistency, fresh holistic read) has found confirmed errors in rounds the fixed lenses passed.

## Error classes with a record of surviving verification

The lens prompts in the script enumerate these. They are the classes that have produced confirmed findings; extend the list when a new class surfaces.

- A named actor that does not exist, or an actor assigned a write it cannot perform under the spec/04 §4.6.3 CRD field-ownership table and its RBAC paragraph.
- A check placed at a layer that cannot see the data it needs (an in-process admission webhook resolving a cross-resource reference; a gateway surface evaluating a CRD-only field).
- A data-flow direction asserted backwards (which side of a store-CRD mirror is authoritative; verify in the reconciler code, never from memory).
- A mandatory gate that one write path bypasses (a field settable through a surface the gate never inspects, such as a directly applied custom resource).
- Build-phase ordering violations: a `spec/18` deliverable depending on artifacts a later phase introduces.
- Edits to generated artifacts instead of their authoring source (alert rules are authored in `pkg/alerting/rules` and rendered by `make generate`; check file headers for generation notes).
- Revert and rollout races: a status condition that clears while pods rendered under the old configuration still run.
- Predicate drift: a trigger condition stated with different conjuncts in different sections of the proposal (design section, summary table, constant comment, proposed spec text, tests).
- Missing companion edit sites: a metric without its `spec/16` inventory row and `docs/reference/metrics.md` row; an alert without its `docs/runbooks/` page (`tests/tier11_docs` enforces slug resolution); a classification table without its companion prose list; an operator-guide sentence describing superseded behavior.
- A field defined in one spec section cross-referenced as living in another.
- An alert defined on a metric that no spec-defined evaluation surface collects.

## Procedure

### Step 1: Determine the mode and assemble inputs (inline, before the workflow)

1. If the argument is the path of an existing file under `proposals/`, the mode is **review**. Otherwise the mode is **new** and the argument (plus the conversation) is the problem statement.
2. Common inputs:
   - `date`: today's date as `YYYY-MM-DD` (workflow scripts cannot call Date).
   - `repoRoot`: the absolute repository root.
   - `exemplar`: the path of the highest-numbered existing proposal (in review mode, excluding the proposal under review).
   - `maxReviewRounds`: default 8.
3. New mode only:
   - Read the spec sections and code paths the problem names so the dossier carries concrete citations rather than paraphrase.
   - `problem`: a problem dossier of one to three paragraphs stating the problem.
   - `context`: a block listing every citation gathered so far (spec file:line, code file:line, finding IDs from the inline input, prior conversation conclusions). Distinguish established facts from unverified claims; the workflow re-verifies both.
   - `nextNumber`: list `proposals/`, take the highest `NNNN_` prefix among numbered files, add one, zero-pad to four digits. Ignore unnumbered files.
4. Review mode only:
   - `proposalPath`: the path of the proposal under review, relative to the repository root.
   - `context`: a short list of the spec sections and code packages the proposal touches, with approximate line anchors, gathered by grepping for the proposal's main identifiers. This focuses reviewers; they re-verify everything themselves.

### Step 2: Run the workflow

Invoke the Workflow tool with the script below verbatim and the mode-appropriate args:

```json
{
  "mode": "new",
  "problem": "<the problem dossier>",
  "context": "<citations and prior conclusions>",
  "date": "<YYYY-MM-DD>",
  "nextNumber": "<NNNN>",
  "exemplar": "proposals/<highest-numbered proposal>.md",
  "repoRoot": "<absolute repo root>",
  "maxReviewRounds": 8
}
```

```json
{
  "mode": "review",
  "proposalPath": "proposals/<file>.md",
  "context": "<assembled notes, or empty>",
  "date": "<YYYY-MM-DD>",
  "exemplar": "proposals/<highest-numbered other proposal>.md",
  "repoRoot": "<absolute repo root>",
  "maxReviewRounds": 8
}
```

Pass `args` as a JSON object value in the tool call. The script tolerates a JSON-encoded object string by parsing it; anything else aborts on the args guard.

Agents inherit the session model and effort level. Run this skill with the strongest available model at high effort; reviewer quality determines whether the loop converges on truth or on exhaustion.

### Step 3: Interruptions and non-convergence

- On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Completed agents replay from the journal cache and the run continues live from the cut point.
- On hitting `maxReviewRounds` without two consecutive clean rounds: inspect the trajectory in the returned `review.history`. If the confirmed-finding counts are decreasing and the last round was clean, raise `maxReviewRounds` in the persisted script file's default or pass a larger value, and resume with `{scriptPath, resumeFromRunId}`; the edit does not invalidate the cached prefix. If counts are flat or oscillating, stop and report the recurring findings for a human decision instead of burning rounds.

### Step 4: Report

1. Run `git status --porcelain` and confirm the only created or modified file is the proposal. If anything under `spec/` or any other path changed, restore it and report the violation.
2. On `status: "written"` or `status: "reviewed"`: read the proposal, then report the file path, the title, the refuted premises and dropped changes with reasons (new mode), whether the loop converged, the rounds run, the findings fixed per round with their titles, and the findings the skeptics refuted. State that the next step is sign-off, after which the staged edits can be applied to `spec/`.
3. On `status: "not-viable"` or `status: "no-change-needed"`: no file is written. Report the refuting evidence so the user can correct or withdraw the problem statement.
4. Do not apply any staged edit to `spec/`, and do not commit, unless the user asks.

```js
export const meta = {
  name: "spec-proposal",
  description:
    "Validate a spec problem, draft and write a proposal (new mode), then adversarially review and fix it until two consecutive clean rounds",
  whenToUse:
    "Write a spec change proposal from a problem statement, or converge an existing proposals/*.md before sign-off",
};

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || typeof input !== "object") {
  throw new Error(
    "args must be a JSON object or a JSON-encoded object string, received " +
      typeof args,
  );
}
for (const k of ["mode", "date", "exemplar", "repoRoot"]) {
  if (!input[k]) throw new Error("args." + k + " is required and missing");
}
const mode = input.mode;
if (mode !== "new" && mode !== "review") {
  throw new Error('args.mode must be "new" or "review"');
}
if (mode === "new") {
  for (const k of ["problem", "nextNumber"]) {
    if (!input[k])
      throw new Error("args." + k + " is required in new mode and missing");
  }
} else if (!input.proposalPath) {
  throw new Error("args.proposalPath is required in review mode and missing");
}

const repo = input.repoRoot;
const date = input.date;
const exemplar = input.exemplar;
const context = input.context || "none provided";
const maxRounds = input.maxReviewRounds || 8;
const CLEAN_TARGET = 2;

const READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file. Cite evidence as file:line.";
const EVIDENCE =
  "Verify every claim directly against spec/, schemas/, pkg/, cmd/, charts/, and git history in " +
  repo +
  ". Spec files are large; use Grep and targeted Read offsets, never read a whole spec file. Treat the problem statement itself and any progress-tracking or audit prose elsewhere in the repository as leads to verify rather than as evidence.";
const PRINCIPLES = [
  "v1 ships a single canonical implementation per concern; no tier-dependent code paths.",
  "No deployments exist in the wild; no dual modes, legacy flags, or migration shims for external compatibility.",
  "Prefer extending an existing spec surface or pattern over inventing a parallel one.",
  "Minimal new protocol surface; every new RPC, frame, field, or endpoint must survive the question of whether an existing surface already covers it.",
].join(" ");

const PREMISES = {
  type: "object",
  required: ["premises"],
  properties: {
    premises: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "statement", "kind", "loadBearing"],
        properties: {
          id: { type: "string" },
          statement: { type: "string" },
          kind: {
            type: "string",
            enum: [
              "spec-claim",
              "code-claim",
              "gap-claim",
              "consequence-claim",
            ],
          },
          loadBearing: { type: "boolean" },
        },
      },
    },
  },
};

const PREMISE_VERDICT = {
  type: "object",
  required: ["verdict", "evidence", "notes"],
  properties: {
    verdict: { type: "string", enum: ["confirmed", "refuted", "revised"] },
    revisedStatement: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
  },
};

const DRAFT = {
  type: "object",
  required: [
    "viable",
    "title",
    "kind",
    "problemRestatement",
    "decisions",
    "changes",
    "nonGoals",
  ],
  properties: {
    viable: { type: "boolean" },
    whyNotViable: { type: "string" },
    title: { type: "string" },
    kind: { type: "string", enum: ["new", "fix"] },
    problemRestatement: { type: "string" },
    decisions: { type: "array", items: { type: "string" } },
    changes: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "title", "targets", "rationale", "sketch"],
        properties: {
          id: { type: "string" },
          title: { type: "string" },
          targets: { type: "array", items: { type: "string" } },
          rationale: { type: "string" },
          sketch: { type: "string" },
        },
      },
    },
    nonGoals: { type: "array", items: { type: "string" } },
    openQuestions: { type: "array", items: { type: "string" } },
  },
};

const CHALLENGE = {
  type: "object",
  required: ["verdict", "reasons", "evidence"],
  properties: {
    verdict: { type: "string", enum: ["keep", "revise", "drop"] },
    reasons: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    revision: { type: "string" },
  },
};

const FINDINGS = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: [
          "title",
          "where",
          "claim",
          "why_wrong",
          "evidence",
          "suggested_fix",
        ],
        properties: {
          title: {
            type: "string",
            description: "Short unique title for the error",
          },
          where: {
            type: "string",
            description: "Location in the proposal (section, line)",
          },
          claim: {
            type: "string",
            description: "What the proposal asserts or proposes there",
          },
          why_wrong: {
            type: "string",
            description:
              "Why this makes the applied spec or implementation wrong",
          },
          evidence: {
            type: "string",
            description:
              "Exact file:line citations with short quotes for both the proposal claim and the contradicting source",
          },
          suggested_fix: { type: "string" },
        },
      },
    },
  },
};

const VERDICT = {
  type: "object",
  required: ["confirmed", "reason"],
  properties: {
    confirmed: { type: "boolean" },
    reason: { type: "string" },
  },
};

// ---- New mode: validate, draft, challenge, write ----

let path;
let draftTitle = null;
let premiseStats = null;
let keptTitles = [];
let droppedChanges = [];

if (mode === "new") {
  const problem = input.problem;
  const num = input.nextNumber;

  phase("Validate");
  log("Decomposing the problem into testable premises");
  const decomposition = await agent(
    "Decompose a reported spec problem into individually testable premises.\n\n" +
      "Problem:\n" +
      problem +
      "\n\nContext:\n" +
      context +
      "\n\n" +
      READ_ONLY +
      "\n" +
      "List every premise the problem rests on, including implicit ones (assumptions about process lifetimes, ownership, ordering, or who calls what). " +
      "Each premise is one falsifiable statement about what the spec says (spec-claim), what the code does (code-claim), what is missing (gap-claim), or what would go wrong (consequence-claim). " +
      "Mark loadBearing: true when refuting the premise would invalidate or materially redirect the problem. Cap the list at the ten most consequential premises.",
    { schema: PREMISES, label: "decompose" },
  );
  const premises = decomposition.premises.slice(0, 10);
  log(
    premises.length +
      " premises identified; dispatching one skeptic per premise",
  );

  const verdicts = (
    await parallel(
      premises.map(
        (p) => () =>
          agent(
            "Try to REFUTE this premise about the spec or implementation.\n\n" +
              "Premise (" +
              p.kind +
              "): " +
              p.statement +
              "\n\n" +
              "Original problem statement, for context only:\n" +
              problem +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Read the actual spec sections and code the premise is about. Return confirmed only when you found direct supporting evidence, refuted when the evidence contradicts the premise, and revised when the premise is directionally right but wrong in a detail that matters (provide revisedStatement). " +
              "Default to refuted when you cannot find supporting evidence.",
            {
              schema: PREMISE_VERDICT,
              label: "skeptic:" + p.id,
              phase: "Validate",
            },
          ).then((v) => ({ premise: p, ...v })),
      ),
    )
  ).filter(Boolean);

  const refuted = verdicts.filter((v) => v.verdict === "refuted");
  const standing = verdicts.filter((v) => v.verdict !== "refuted");
  premiseStats = { standing: standing.length, refuted: refuted.length };
  log(
    "Premises: " +
      standing.length +
      " standing, " +
      refuted.length +
      " refuted",
  );

  const loadBearing = verdicts.filter((v) => v.premise.loadBearing);
  if (
    loadBearing.length > 0 &&
    loadBearing.every((v) => v.verdict === "refuted")
  ) {
    return {
      mode,
      status: "not-viable",
      reason: "every load-bearing premise was refuted",
      verdicts,
    };
  }

  phase("Draft");
  const dossier = verdicts
    .map(
      (v) =>
        "- [" +
        v.verdict.toUpperCase() +
        "] " +
        (v.revisedStatement || v.premise.statement) +
        "\n  evidence: " +
        v.evidence.join("; ") +
        "\n  notes: " +
        v.notes,
    )
    .join("\n");

  const draft = await agent(
    "Draft a spec change proposal.\n\n" +
      "Problem:\n" +
      problem +
      "\n\n" +
      "Premise verdicts from independent skeptics (refuted premises are course corrections; the draft must not rest on them):\n" +
      dossier +
      "\n\n" +
      READ_ONLY +
      " Output the draft as structured data only; another agent writes the file.\n" +
      EVIDENCE +
      "\n" +
      "Project principles: " +
      PRINCIPLES +
      "\n" +
      "Read " +
      exemplar +
      " for the level of specificity expected, and read the spec sections each change targets. " +
      "Produce: a title; kind (fix corrects or reconciles existing spec text, new adds a capability the spec lacks); a problem restatement grounded in the confirmed evidence; the review decisions that constrain the design; the change set (each change names its target spec files and sections, the rationale, and a concrete sketch of the staged edit); non-goals; open questions only for decisions that genuinely belong to the human reviewer. " +
      "Set viable: false with whyNotViable when the confirmed evidence shows no spec change is needed.",
    { schema: DRAFT, label: "draft" },
  );

  if (!draft.viable) {
    return { mode, status: "not-viable", reason: draft.whyNotViable, verdicts };
  }
  draftTitle = draft.title;
  log(
    'Draft "' + draft.title + '" proposes ' + draft.changes.length + " changes",
  );

  phase("Challenge");
  const challenged = (
    await parallel(
      draft.changes.map(
        (c) => () =>
          agent(
            "Adversarially challenge one proposed spec change. Your default posture is that the change is unnecessary.\n\n" +
              "Full draft for context:\n" +
              JSON.stringify(draft, null, 2) +
              "\n\n" +
              "Change under challenge: " +
              c.id +
              " — " +
              c.title +
              "\nTargets: " +
              c.targets.join(", ") +
              "\nRationale: " +
              c.rationale +
              "\nSketch: " +
              c.sketch +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Project principles: " +
              PRINCIPLES +
              "\n" +
              "Answer each question with evidence: (1) Does an existing spec surface, RPC, frame, field, or code path already cover this? (2) Is every factual premise under the change true in both the spec and the code, including process-lifetime and ownership assumptions? (3) Does the change contradict any other spec section? (4) Does it violate the project principles? (5) Is there a strictly smaller change that resolves the same problem? " +
              "Return drop when the change is unnecessary or rests on a false premise, revise with a concrete revision when the need is real but the change is wrong or oversized, and keep only when it survives all five questions.",
            { schema: CHALLENGE, label: "challenge:" + c.id, phase: "Challenge" },
          ).then((v) => ({ change: c, ...v })),
      ),
    )
  ).filter(Boolean);

  const kept = [];
  for (const r of challenged) {
    if (r.verdict === "drop")
      droppedChanges.push({
        id: r.change.id,
        title: r.change.title,
        reasons: r.reasons,
        evidence: r.evidence,
      });
    else if (r.verdict === "revise")
      kept.push({
        ...r.change,
        sketch: r.revision || r.change.sketch,
        challengeNotes: r.reasons,
      });
    else kept.push(r.change);
  }
  keptTitles = kept.map((c) => c.title);
  log(
    "Challenge: " +
      kept.length +
      " changes kept, " +
      droppedChanges.length +
      " dropped",
  );
  if (kept.length === 0) {
    return { mode, status: "no-change-needed", dropped: droppedChanges, verdicts };
  }

  phase("Write");
  const slug = draft.title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
  path = repo + "/proposals/" + num + "_" + draft.kind + "_" + slug + ".md";

  await agent(
    "Write a spec change proposal file.\n\n" +
      "HARD CONSTRAINT: the only file you may create or edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/. The proposal stages spec edits as fenced markdown blocks; it never applies them.\n\n" +
      "Draft (apply the challenge revisions in each sketch verbatim):\n" +
      JSON.stringify({ ...draft, changes: kept }, null, 2) +
      "\n\n" +
      "Dropped alternatives to record in Non-goals with their reasons:\n" +
      JSON.stringify(droppedChanges, null, 2) +
      "\n\n" +
      "Date: " +
      date +
      "\n" +
      "Format: follow the structure of " +
      exemplar +
      ' exactly (read it first): the "# Proposal:" title; Status ("Draft for review."), Date, and Scope bullets; the staging boilerplate paragraph; numbered sections (Problem with file:line citations and any finding IDs the input named; Decisions; design sections; Proposed spec changes with one subsection per target file and an anchor instruction plus a fenced block of the exact text to insert; Non-goals; Testing; Findings closed on application; Resolved in adversarial review, initially noting that review rounds populate it; Open decisions for review when the draft has open questions; Files touched on application consistent with the staged changes).\n' +
      "Prose rules: follow " +
      repo +
      "/.claude/rules/doc-style.md (read it first). " +
      "Read the spec sections each staged edit targets so anchors and surrounding text are quoted accurately.",
    { label: "write", phase: "Write" },
  );
  log("Proposal written to " + path);
} else {
  path = input.proposalPath.startsWith("/")
    ? input.proposalPath
    : repo + "/" + input.proposalPath;
}

// ---- Conventions pass (shared, one-shot, outside the error loop) ----

phase("Conventions");
await agent(
  "Check one proposal file against the written conventions and fix only violations.\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "The written rules: section structure and citation formats per the exemplar " +
    exemplar +
    " (read it first), and prose per " +
    repo +
    "/.claude/rules/doc-style.md (read it first). " +
    "Fix structural deviations and doc-style violations (fragments, missing list conjunctions, decorative em-dashes, marketing language). Do not change technical content, citations, or design decisions. If the file already conforms, change nothing and say so.",
  { label: "conventions" },
);

// ---- Review loop (shared): multi-lens review, two-skeptic verify, fix ----

const CONTEXT =
  "Repository: " +
  repo +
  " (your working directory). The technical spec lives in spec/ (files are large; use Grep and targeted Read offsets, never read a whole spec file), implementation in pkg/ and cmd/, Helm chart in charts/, operator docs in docs/.\n" +
  "The proposal under review: " +
  path +
  " (read it fully first).\n" +
  "Standing reference points (re-verify anything you rely on; line numbers drift):\n" +
  "- spec/04 section 4.6.3: the CRD field-ownership table (which controller owns which spec/status subresource) and the controller RBAC paragraph. Every condition or status write in a proposal must match it.\n" +
  "- spec/10 section 10.3 and spec/13 section 13.2: agent pods have zero RBAC and no apiserver path; NetworkPolicy egress tables; admission webhooks are purely in-process validators.\n" +
  "- spec/16: metric and alert inventories; the alert catalog is single-sourced in pkg/alerting/rules and rendered by make generate (check file headers for generation notes); docs/reference/metrics.md and docs/runbooks/ mirror them, and tests/tier11_docs enforces alert-to-runbook resolution.\n" +
  "- spec/18: phased build sequence; a phase deliverable cannot depend on artifacts of a later phase.\n" +
  "Notes from the orchestrator (leads, not evidence):\n" +
  context +
  "\n" +
  EVIDENCE;

const BAR =
  "REPORT ONLY REAL ERRORS. A finding qualifies only if at least one of these holds:\n" +
  "(a) A citation in the proposal is false: the cited file, line, or section does not say what the proposal claims, or the proposal attributes behavior to the wrong component.\n" +
  "(b) The proposal assigns an actor an action it cannot perform: it violates the section 4.6.3 ownership table or RBAC, the section 13.2 egress posture, the section 10.3 zero-RBAC agent-pod posture, admission-webhook in-process purity, process boundaries between deployment models, or spec/18 build-phase ordering.\n" +
  "(c) The proposal contradicts the current spec, the current code, or itself, such that applying its edits would leave the spec internally inconsistent or the described implementation broken.\n" +
  "(d) The proposal misses an edit site: a spec/, docs/, schemas/, or charts/ surface that would become wrong after the proposed edits are applied and that is absent from the proposal's edit lists. Editing a generated artifact instead of its authoring source counts.\n" +
  "(e) A described mechanism cannot work: race conditions, bypassable mandatory gates, unreachable trigger states, wrong defaults, mismatched granularity, predicate drift between sections, or ordering problems.\n\n" +
  "DO NOT report: style or wording, documentation polish, optional improvements, extra test ideas, hypothetical hardening, redundancy, preferences between workable designs, or anything whose absence does not make the applied spec or implementation wrong. If you are unsure whether something meets the bar, do not report it. An empty findings list is a fully acceptable answer and is the expected answer for a converged proposal.\n\n" +
  'The proposal\'s "Resolved in adversarial review" section is a historical record of earlier passes; its descriptions of earlier drafts are not findings. Sections recording deliberately open decisions for the human reviewer are not findings.\n\n' +
  "Every finding MUST carry evidence: exact file paths with line numbers and short quotes for both the proposal's claim and the contradicting source. Read the files to verify line numbers; never cite from memory.";

const LENSES = [
  {
    key: "citations",
    text: 'Lens: citation audit. Extract every concrete citation in the proposal (file paths with line numbers, spec section references, quoted spec text, attributed behaviors such as "section X assigns Y to Z" or "function F does G"). Verify each one against the actual file content at the cited location. A citation whose target says something materially different, attributes the behavior to a different component, or does not exist is a finding. Off-by-a-few line drift on an otherwise accurate claim is NOT a finding unless the drift changes the meaning. Check data-flow directions (which side of a mirror is authoritative) in the reconciler code itself.',
  },
  {
    key: "feasibility",
    text: "Lens: actor-action feasibility. For every action the proposal assigns to a component, verify the component exists under that name and can perform the action under the spec: the section 4.6.3 ownership table and RBAC paragraph, the section 13.2 NetworkPolicy egress rules, the section 10.3 agent-pod posture, webhook purity (pure-decision packages that cannot resolve cross-resource references), process boundaries between deployment models, and spec/18 phase ordering (a deliverable in phase N must not require artifacts that first exist in a later phase). Also verify each layer can see the data its check needs. Any assignment the actor cannot fulfil is a finding.",
  },
  {
    key: "edit-sites",
    text: "Lens: edit-site completeness. Enumerate every identifier the proposal adds, changes, or removes (field names, flag names, condition types, metric names, alert names, error strings, Helm value names, yaml keys). Grep spec/, docs/, schemas/, and charts/ for each one and for the concepts they replace. Any surface that becomes incorrect or internally inconsistent after the proposed edits are applied, and that is missing from the proposal's edit lists, is a finding. Check authored-vs-generated chains (file headers note generation; the proposal must edit the source). Check companion pairs: classification tables with their prose lists, metrics with their spec/16 and docs/reference/metrics.md inventory rows, alerts with their docs/runbooks/ pages, operator-guide sentences describing behavior the proposal supersedes.",
  },
  {
    key: "mechanism",
    text: "Lens: end-to-end mechanism. Trace each flow the proposal describes from origin to final effect and hunt for: states where declared config and running pods diverge (revert and rollout races), mandatory gates that one write path bypasses (fields settable through a surface the gate never inspects, such as a directly applied custom resource), triggers that can never fire, defaults that contradict stated behavior, checks at layers that cannot see the data they need, granularity mismatches between a setting and what it controls, and predicate drift (the same trigger condition stated with different conjuncts in different sections, summary tables, constant comments, proposed spec text, and tests). Also verify the proposal's quoted replacement spec text is itself consistent with the rest of the spec it will be embedded in.",
  },
];

const EXTRAS = [
  {
    key: "security",
    text: "Lens: security-posture regression. For each proposed change, check whether it weakens any control the spec establishes: the section 10.3 zero-RBAC and no-apiserver agent-pod posture, section 13.2 default-deny networking, section 13.1 pod security, fail-closed admission, mandatory acknowledgment gates, and audit/alert surfacing of degraded states. A change that silently removes, bypasses, or feature-gates a mandatory control is a finding. A change that is merely less strict than it could be is NOT a finding.",
  },
  {
    key: "operational",
    text: "Lens: operational consistency. Check that conditions, metrics, alerts, and operator documentation the proposal touches stay mutually consistent after application: every alert references an emitted metric that a spec-defined evaluation surface can collect, every condition has exactly one writer consistent with section 4.6.3, condition semantics match what operators are told in docs/, and the spec's observability inventories agree with what the proposal says exists. An inconsistency that would mislead an operator about the system's actual state is a finding.",
  },
  {
    key: "fresh",
    text: "Lens: fresh holistic read. Read the proposal as the spec maintainer who must apply its staged edits verbatim tomorrow. Independently spot-check the assumptions the other lenses might share blind spots on, in whatever order seems most suspicious to you. Anything that would make the applied spec wrong, internally inconsistent, or unimplementable is a finding.",
  },
];

function reviewPrompt(lens, round, fixedTitles, rejected) {
  let history = "";
  if (fixedTitles.length > 0) {
    history +=
      "\n\nAlready found and fixed in earlier rounds (the current proposal text reflects these fixes; do not re-litigate them): " +
      fixedTitles.join("; ") +
      ".";
  }
  if (rejected.length > 0) {
    history +=
      "\n\nAlready examined and refuted in earlier rounds (do not re-report these or close variants):\n" +
      rejected.map((r) => "- " + r.title + ": refuted because " + r.reason).join("\n");
  }
  return (
    "You are an adversarial reviewer in round " +
    round +
    " of an iterative convergence loop for a spec change proposal.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\n" +
    BAR +
    "\n\n" +
    lens.text +
    history +
    "\n\nWork method: read the proposal fully, then investigate the repository with Grep and targeted Reads to verify or refute its claims under your lens. Report your findings via the structured output (empty array if you find nothing that meets the bar)."
  );
}

function dedupPrompt(findings) {
  return (
    "You merge duplicate review findings. Below is a JSON array of findings from several independent reviewers examining the same proposal. Merge entries that describe the same root error (even if phrased differently or found at different citation points): keep one entry per root error, choose the clearest title, and combine the strongest evidence. Do not drop distinct errors. Do not add new findings. Do not judge validity. Return the merged list.\n\nFindings:\n" +
    JSON.stringify(findings, null, 2)
  );
}

function evidencePrompt(f) {
  return (
    "You are a skeptical evidence verifier. A reviewer claims the following error in the proposal " +
    path +
    ". Independently re-derive it: read the proposal at the claimed location and read every cited source file at the cited lines.\n\nConfirm ONLY if all three hold: (1) the proposal really says what the finding claims it says; (2) the cited sources really say what the finding claims they say; (3) the contradiction or infeasibility actually follows from (1) and (2). If any citation is wrong, a quote is out of context, the proposal already handles the issue elsewhere in its text, or the conclusion does not follow, refute with the specific reason.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

function materialityPrompt(f) {
  return (
    "You are a skeptical materiality judge for review findings on the proposal " +
    path +
    ". Assume the finding's evidence is factually accurate. Decide ONLY whether fixing it is required for correctness: confirm if leaving it unfixed would make the applied spec internally inconsistent, make a stated citation or attribution false, or make the described implementation not work. Refute if it is style or wording, documentation polish, an optional improvement or hardening, redundancy, a preference between workable designs, a test-coverage suggestion, or anything else whose absence does not make the spec or implementation wrong. Default to refuted when uncertain. You may read " +
    path +
    " for context.\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

function fixPrompt(confirmed, round) {
  return (
    "You are the fixer for round " +
    round +
    " of an iterative convergence loop on the proposal " +
    path +
    ".\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\nApply EXACTLY the confirmed findings below using Edit (or Write for large restructures). Requirements:\n" +
    "- Before each edit, re-verify the relevant spec/code citations yourself with Grep/Read; every claim that remains in the proposal must be accurate and carry file:line evidence. Re-verify every citation in text you touch, including stale line numbers.\n" +
    "- Make the smallest change that corrects each finding. Do not expand scope. Do not change design decisions beyond what the findings require; when a finding forces a design choice, pick the option most consistent with the cited spec precedent and the project principles (" +
    PRINCIPLES +
    "), and record the rationale in the proposal.\n" +
    "- When a fix changes a trigger predicate or invariant, propagate the exact same predicate to every section that states it (design sections, summary tables, constant comments, proposed spec text, and tests) so no drift is introduced.\n" +
    "- Keep the Proposed spec changes section and the Files touched section consistent with your edits.\n" +
    '- Append a new subsection to the proposal\'s "Resolved in adversarial review" section titled "### Pass <N> (' +
    date +
    ', automated)", where <N> continues the existing pass numbering (read the section to determine it; create the section before the open-decisions section if it does not exist), with one bullet per finding fixed, matching the format of any existing entries.\n' +
    "- Follow the documentation style rules in " +
    repo +
    '/.claude/rules/doc-style.md: complete declarative sentences, no "X, not Y" rhythm, no decorative em-dashes, no marketing language, conjunctions in lists.\n\nConfirmed findings (JSON):\n' +
    JSON.stringify(confirmed, null, 2) +
    "\n\nReturn a short summary listing each finding and the exact edit you made for it."
  );
}

phase("Review");
const fixedTitles = [];
const rejected = [];
const history = [];
let cleanStreak = 0;
let round = 0;
let reviewersFailed = false;

while (round < maxRounds && cleanStreak < CLEAN_TARGET) {
  round++;
  const lenses = LENSES.concat([EXTRAS[(round - 1) % EXTRAS.length]]);
  log(
    "Round " +
      round +
      ": launching " +
      lenses.length +
      " reviewers (clean streak " +
      cleanStreak +
      "/" +
      CLEAN_TARGET +
      ")",
  );

  // Barrier: the dedup step needs every reviewer's findings at once.
  const results = (
    await parallel(
      lenses.map(
        (l) => () =>
          agent(reviewPrompt(l, round, fixedTitles, rejected), {
            label: "r" + round + ":review:" + l.key,
            phase: "Round " + round + ": review",
            schema: FINDINGS,
          }),
      ),
    )
  ).filter(Boolean);

  if (results.length === 0) {
    log("Round " + round + ": every reviewer failed; stopping");
    reviewersFailed = true;
    break;
  }
  const raw = results.flatMap((r) => r.findings);
  log("Round " + round + ": " + raw.length + " raw findings");

  if (raw.length === 0) {
    cleanStreak++;
    history.push({ round, raw: 0, deduped: 0, confirmed: 0 });
    continue;
  }

  let deduped = raw;
  if (raw.length > 1) {
    const d = await agent(dedupPrompt(raw), {
      label: "r" + round + ":dedup",
      phase: "Round " + round + ": review",
      schema: FINDINGS,
    });
    if (d && d.findings.length > 0) deduped = d.findings;
  }
  log("Round " + round + ": " + deduped.length + " findings after dedup; verifying");

  const verdicts = await parallel(
    deduped.map(
      (f) => () =>
        parallel([
          () =>
            agent(evidencePrompt(f), {
              label: "r" + round + ":verify-evidence",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
          () =>
            agent(materialityPrompt(f), {
              label: "r" + round + ":verify-material",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
        ]).then((vs) => ({ f, vs: vs.filter(Boolean) })),
    ),
  );

  const live = verdicts.filter(Boolean);
  const confirmed = live
    .filter((v) => v.vs.length === 2 && v.vs.every((x) => x.confirmed))
    .map((v) => v.f);
  live
    .filter((v) => !(v.vs.length === 2 && v.vs.every((x) => x.confirmed)))
    .forEach((v) => {
      rejected.push({
        title: v.f.title,
        reason:
          v.vs
            .filter((x) => !x.confirmed)
            .map((x) => x.reason)
            .join(" | ") || "verifier unavailable",
      });
    });
  log(
    "Round " + round + ": " + confirmed.length + "/" + deduped.length + " findings confirmed",
  );
  history.push({
    round,
    raw: raw.length,
    deduped: deduped.length,
    confirmed: confirmed.length,
    confirmedTitles: confirmed.map((f) => f.title),
  });

  if (confirmed.length === 0) {
    cleanStreak++;
    continue;
  }
  cleanStreak = 0;

  const fixSummary = await agent(fixPrompt(confirmed, round), {
    label: "r" + round + ":fix",
    phase: "Round " + round + ": fix",
  });
  confirmed.forEach((f) => fixedTitles.push(f.title));
  history[history.length - 1].fixSummary = fixSummary || "fixer unavailable";
}

return {
  mode,
  status: mode === "new" ? "written" : "reviewed",
  path,
  title: draftTitle,
  premises: premiseStats,
  changes:
    mode === "new"
      ? { kept: keptTitles, dropped: droppedChanges.map((d) => d.title) }
      : undefined,
  review: {
    converged: cleanStreak >= CLEAN_TARGET && !reviewersFailed,
    reviewersFailed,
    rounds: round,
    cleanStreak,
    totalFixed: fixedTitles.length,
    fixedTitles,
    rejectedTitles: rejected.map((r) => r.title),
    history,
  },
};
```

## Maintenance

When a convergence run surfaces a confirmed error class this file does not list, add it to the error-class list and, when it fits an existing lens, to that lens's prompt in the script. Keep the finding bar's DO-NOT-report list intact; it is what keeps the loop from converging on nitpicks. When the proposal format conventions and the existing files in `proposals/` disagree, the existing files win; update the conventions list here.
