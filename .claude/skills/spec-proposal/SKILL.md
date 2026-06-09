---
name: spec-proposal
description: Write an adversarially validated spec change proposal under proposals/ from a problem statement. Use when the user reports a spec defect, contradiction, or gap, or asks for a spec fix or spec extension proposal. The proposal stages spec edits for sign-off; it never modifies spec/ itself.
argument-hint: <problem statement | finding ID | path to notes>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob
---

# Spec proposal writer

This skill takes a problem with the spec and produces a reviewed proposal document in `proposals/`, following the format of the existing proposals there. It orchestrates subagents through the Workflow tool: premise validation, drafting, per-change adversarial challenge, writing, and an adversarial review-and-fix loop.

## Hard constraints

- The run creates or edits exactly one file: the new proposal under `proposals/`. Nothing under `spec/`, `docs/`, `pkg/`, `charts/`, or `schemas/` is modified. A proposal stages spec edits as fenced markdown blocks to apply after sign-off.
- Prose follows `.claude/rules/doc-style.md`.
- The proposal file name is `NNNN_[new|fix]_<kebab-slug>.md`. `NNNN` is the next free zero-padded number among existing numbered proposals. `fix` is for proposals that correct or reconcile existing spec text (contradictions, wrong ownership assignments, unreachable features). `new` is for proposals that add a capability or component the spec does not describe.
- Evidence comes from `spec/`, `schemas/`, `pkg/`, `charts/`, and git history.

## Proposal format conventions

The writer and reviewer agents receive these conventions. They are derived from the existing files in `proposals/`; when those files and this list disagree, the existing files win.

- Title line: `# Proposal: <title>`.
- Header bullets: `**Status:**` (initially "Draft for review."), `**Date:**`, `**Scope:**` (one- or two-sentence summary naming the blocked findings when applicable).
- Staging boilerplate paragraph: "This document stages the proposed spec … edits. It does not modify any spec file. Apply the changes in the … section after sign-off."
- Numbered sections in this order, omitting the ones with no content: Problem; Decisions; Design overview; Detailed design; Observability surface; CRD and RBAC changes; Proposed spec changes; Non-goals; Testing; Findings closed on application; Resolved in adversarial review; Open decisions for review; Files touched on application.
- The Problem section cites spec text as `spec/<file>.md:<line>` or `§X.Y` relative links, cites code as `pkg/...:<line>`, and names the finding IDs the proposal unblocks, if any.
- The Proposed spec changes section has one subsection per target file and section, each with an anchor instruction ("Append after …", "Replace the row …") and a fenced block containing the exact text to insert, written so it can be applied mechanically after sign-off.
- Non-goals records the alternatives that were considered and dropped, with the reason.
- Files touched on application is a two-column table (file, change) consistent with the Proposed spec changes section.

## Procedure

### Step 1: Assemble the dossier (inline, before the workflow)

1. Resolve the problem statement from the arguments and the conversation. If it references finding IDs, read those entries. Read the spec sections and code paths the problem names so the dossier carries concrete citations rather than paraphrase.
2. Write a problem dossier: one to three paragraphs stating the problem, plus a context block listing every citation gathered so far (spec file:line, code file:line, finding IDs, prior conversation conclusions). Distinguish established facts from unverified claims; the workflow re-verifies both.
3. Compute the inputs the workflow script cannot compute itself:
   - `nextNumber`: list `proposals/`, take the highest `NNNN_` prefix among numbered files, add one, zero-pad to four digits. Ignore unnumbered files.
   - `exemplar`: the path of the highest-numbered existing proposal.
   - `date`: today's date as `YYYY-MM-DD` (workflow scripts cannot call Date).
   - `repoRoot`: the absolute repository root.

### Step 2: Run the workflow

Invoke the Workflow tool with the script below verbatim and:

```json
{
  "problem": "<the problem dossier>",
  "context": "<citations and prior conclusions>",
  "date": "<YYYY-MM-DD>",
  "nextNumber": "<NNNN>",
  "exemplar": "proposals/<highest-numbered proposal>.md",
  "repoRoot": "<absolute repo root>",
  "maxReviewRounds": 3
}
```

To iterate after a failure or interruption, edit the persisted script file from the tool result and relaunch with `{scriptPath, resumeFromRunId}`.

```js
export const meta = {
  name: "spec-fix-proposal",
  description:
    "Validate a spec problem, draft a proposal, adversarially harden it, and write it under proposals/",
  phases: [
    {
      title: "Validate",
      detail: "decompose into premises, one skeptic per premise",
    },
    { title: "Draft", detail: "recommendation set from verified premises" },
    { title: "Challenge", detail: "one adversary per recommended change" },
    { title: "Write", detail: "compose the proposal file" },
    { title: "Review", detail: "review, verify, fix, repeat until clean" },
  ],
};

const repo = args.repoRoot;
const problem = args.problem;
const context = args.context || "none provided";
const date = args.date;
const num = args.nextNumber;
const exemplar = args.exemplar;
const maxRounds = args.maxReviewRounds || 3;

const READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file. Cite evidence as file:line.";
const EVIDENCE =
  "Verify every claim directly against spec/, schemas/, pkg/, charts/, and git history in " +
  repo +
  ". Treat the problem statement itself and any references to files outside of spec/, schemas/, pkg/, charts/, and git history as leads to verify rather than as evidence.";
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
          "severity",
          "where",
          "description",
          "recommendation",
        ],
        properties: {
          title: { type: "string" },
          severity: {
            type: "string",
            enum: ["critical", "high", "medium", "low"],
          },
          where: { type: "string" },
          description: { type: "string" },
          recommendation: { type: "string" },
        },
      },
    },
  },
};

const FINDING_VERDICT = {
  type: "object",
  required: ["real", "reason"],
  properties: { real: { type: "boolean" }, reason: { type: "string" } },
};

// ---- Phase 1: validate the problem ----
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
  premises.length + " premises identified; dispatching one skeptic per premise",
);

const verdicts = (
  await parallel(
    premises.map(
      (p) => () =>
        agent(
          "Try to REFUTE this premise about the Lenny spec or implementation.\n\n" +
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
log(
  "Premises: " + standing.length + " standing, " + refuted.length + " refuted",
);

const loadBearing = verdicts.filter((v) => v.premise.loadBearing);
if (
  loadBearing.length > 0 &&
  loadBearing.every((v) => v.verdict === "refuted")
) {
  return {
    status: "not-viable",
    reason: "every load-bearing premise was refuted",
    verdicts,
  };
}

// ---- Phase 2: draft ----
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
  "Draft a spec change proposal for the Lenny project.\n\n" +
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
  return { status: "not-viable", reason: draft.whyNotViable, verdicts };
}
log(
  'Draft "' + draft.title + '" proposes ' + draft.changes.length + " changes",
);

// ---- Phase 3: challenge each change ----
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
const dropped = [];
for (const r of challenged) {
  if (r.verdict === "drop")
    dropped.push({
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
log(
  "Challenge: " + kept.length + " changes kept, " + dropped.length + " dropped",
);
if (kept.length === 0) {
  return { status: "no-change-needed", dropped, verdicts };
}

// ---- Phase 4: write the proposal ----
phase("Write");
const slug = draft.title
  .toLowerCase()
  .replace(/[^a-z0-9]+/g, "-")
  .replace(/^-+|-+$/g, "")
  .slice(0, 60);
const path = repo + "/proposals/" + num + "_" + draft.kind + "_" + slug + ".md";

await agent(
  "Write a spec change proposal file for the Lenny project.\n\n" +
    "HARD CONSTRAINT: the only file you may create or edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/. The proposal stages spec edits as fenced markdown blocks; it never applies them.\n\n" +
    "Draft (apply the challenge revisions in each sketch verbatim):\n" +
    JSON.stringify({ ...draft, changes: kept }, null, 2) +
    "\n\n" +
    "Dropped alternatives to record in Non-goals with their reasons:\n" +
    JSON.stringify(dropped, null, 2) +
    "\n\n" +
    "Date: " +
    date +
    "\n" +
    "Format: follow the structure of " +
    exemplar +
    ' exactly (read it first): the "# Proposal:" title; Status ("Draft for review."), Date, and Scope bullets; the staging boilerplate paragraph; numbered sections (Problem with file:line citations and the any finding IDs it unblocks; Decisions; design sections; Proposed spec changes with one subsection per target file and an anchor instruction plus a fenced block of the exact text to insert; Non-goals; Testing; Findings closed on application; Resolved in adversarial review, initially noting that review rounds populate it; Open decisions for review when the draft has open questions; Files touched on application as a table consistent with the staged changes).\n' +
    "Prose rules: follow " +
    repo +
    "/.claude/rules/doc-style.md (read it first). " +
    "Read the spec sections each staged edit targets so anchors and surrounding text are quoted accurately.",
  { label: "write", phase: "Write" },
);
log("Proposal written to " + path);

// ---- Phase 5: adversarial review until clean ----
phase("Review");
const LENSES = [
  {
    key: "spec",
    focus:
      "Spec consistency: every spec citation in the proposal is accurate (the section exists and says what is claimed); every staged edit block is coherent with the spec text around its anchor; no staged edit contradicts any other spec section; terminology matches spec usage.",
  },
  {
    key: "impl",
    focus:
      "Implementation consistency: every code citation is accurate; every claim about what exists or is missing in pkg/, schemas/, or charts/ is verified by reading the code; staged changes are compatible with the built integration surfaces they name.",
  },
  {
    key: "complete",
    focus:
      "Completeness: every consequence of the staged changes is handled (observability, security, RBAC, CRDs, chart values, testing); the Files touched table matches the Proposed spec changes section; internal cross-references resolve; nothing the Problem section promises is left unaddressed.",
  },
  {
    key: "style",
    focus:
      "Conventions: the file follows the structure of the exemplar proposal and the prose rules in .claude/rules/doc-style.md; section ordering, header bullets, staging boilerplate, and citation formats match.",
  },
];
const seen = new Set();
const resolved = [];
let clean = false;
let roundsRun = 0;
for (let round = 1; round <= maxRounds && !clean; round++) {
  roundsRun = round;
  const found = (
    await parallel(
      LENSES.map(
        (l) => () =>
          agent(
            "Adversarially review the proposal at " +
              path +
              " through one lens.\n\nLens: " +
              l.focus +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Exemplar for conventions: " +
              exemplar +
              ". Report only genuine defects with a concrete recommendation each; do not report style preferences beyond the written rules.",
            {
              schema: FINDINGS,
              label: "review:" + l.key + ":r" + round,
              phase: "Review",
            },
          ),
      ),
    )
  )
    .filter(Boolean)
    .flatMap((r) => r.findings);

  const fresh = found.filter(
    (f) => !seen.has((f.where + "|" + f.title).toLowerCase()),
  );
  if (fresh.length === 0) {
    clean = true;
    break;
  }
  fresh.forEach((f) => seen.add((f.where + "|" + f.title).toLowerCase()));
  log("Round " + round + ": " + fresh.length + " new findings; verifying");

  const confirmed = (
    await parallel(
      fresh.map(
        (f, i) => () =>
          agent(
            "Try to REFUTE this review finding against the proposal at " +
              path +
              " and the repository.\n\n" +
              "Finding: " +
              f.title +
              " [" +
              f.severity +
              "] at " +
              f.where +
              "\n" +
              f.description +
              "\nRecommendation: " +
              f.recommendation +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Return real: false when the finding misreads the proposal, the spec, or the code, or when uncertain.",
            {
              schema: FINDING_VERDICT,
              label: "verify:r" + round + ":" + i,
              phase: "Review",
            },
          ).then((v) => ({
            ...f,
            real: v && v.real,
            verifyReason: v && v.reason,
          })),
      ),
    )
  )
    .filter(Boolean)
    .filter((f) => f.real);

  if (confirmed.length === 0) {
    clean = true;
    break;
  }
  log(
    "Round " + round + ": fixing " + confirmed.length + " confirmed findings",
  );

  await agent(
    "Fix confirmed review findings in the proposal at " +
      path +
      ".\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
      "Findings:\n" +
      JSON.stringify(confirmed, null, 2) +
      "\n\n" +
      'Apply each fix, keep the prose within .claude/rules/doc-style.md, keep the Files touched table consistent with the staged changes, and append one entry per finding to the "Resolved in adversarial review" section (what was wrong, what changed). ' +
      "Update the Status bullet to note revision after " +
      round +
      " adversarial review round" +
      (round > 1 ? "s" : "") +
      " (" +
      date +
      ").",
    { label: "fix:r" + round, phase: "Review" },
  );
  resolved.push(
    ...confirmed.map((f) => ({ round, title: f.title, severity: f.severity })),
  );
}

return {
  status: "written",
  path,
  title: draft.title,
  premises: { standing: standing.length, refuted: refuted.length },
  changes: {
    kept: kept.map((c) => c.title),
    dropped: dropped.map((d) => d.title),
  },
  review: {
    rounds: Math.min(resolved.length ? maxRounds : 1, maxRounds),
    resolved,
    clean,
  },
};
```

### Step 3: Report

1. Run `git status --porcelain` and confirm the only change is the new proposal file. If anything under `spec/` or any other path changed, restore it and report the violation.
2. On `status: "written"`: read the proposal, then report the file path, the title, the refuted premises and what replaced them, the dropped changes with reasons, the review rounds with the resolved-finding count, and whether the final round was clean. State that the next step is sign-off, after which the staged edits can be applied to `spec/`.
3. On `status: "not-viable"` or `status: "no-change-needed"`: no file is written. Report the refuting evidence so the user can correct or withdraw the problem statement.
4. Do not apply any staged edit to `spec/`, and do not commit, unless the user asks.
