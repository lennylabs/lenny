---
name: spec-apply
description: Apply an approved spec change proposal's staged edits to spec/, then verify every applied edit aligns exactly with the proposal and fix discrepancies until a verification round is clean. Use when a proposal under proposals/ is signed off and its spec edits should land. Applies spec/ targets only; staged code, chart, and docs changes remain with the implementation work.
argument-hint: <path to proposals/*.md>
allowed-tools: Workflow Agent Bash Read Write Edit Grep Glob TaskStop
---

# Spec apply

This skill takes an approved proposal under `proposals/` and lands its staged spec edits in `spec/`. The workflow applies the edits, verifies that every applied edit aligns exactly with the proposal's staged text, fixes any discrepancy, and re-verifies until a full verification round reports none.

## Hard constraints

- The run edits only the `spec/` files that the proposal's "Proposed spec changes" section names as targets. The proposal file itself, `docs/`, `pkg/`, `cmd/`, `charts/`, and `schemas/` are never modified. Staged changes whose targets are outside `spec/` are reported as remaining work, never applied.
- The proposal must record approval in its Status bullet. Without recorded approval the run stops before any edit.
- `spec/` must be clean in git before the run starts, so `git diff -- spec/` is exactly the applied change set and verification can compare it against the proposal.
- Anchors are located by the quoted text and section headings in the proposal's anchor instructions. Line numbers in anchor instructions are location hints that drift; an edit whose anchor text cannot be found is recorded as unappliable rather than guessed.

## Spec content rules

These rules take precedence over verbatim application. Every deviation they force is recorded and reported; the verifier treats recorded deviations as expected differences.

- The spec never references source code files or implementation paths (`pkg/`, `cmd/`, `charts/`, `sdks/`, `tests/`, `migrations/`, `.go` or other source files). Staged text carrying such a reference is rephrased into behavioral spec language or the reference is dropped.
- The spec cross-references other spec content by section number only: `§X.Y` or a relative markdown link to a section anchor. A line-number cross-reference in staged text is replaced with the containing section's number.
- `.claude/rules/doc-style.md` governs spec prose. Staged text is applied as written (the proposal pipeline already styled it); the applier does not restyle it.

## Procedure

### Step 1: Preconditions (inline, before the workflow)

1. Resolve the proposal path from the arguments and read the proposal.
2. Confirm the Status bullet records approval (for example "Approved for implementation"). If it records a draft or in-review state, stop and report; do not invoke the workflow.
3. Run `git status --porcelain -- spec/`. If any `spec/` file is dirty, stop and report; the verification model requires a clean baseline.
4. Compute `repoRoot` (the absolute repository root).

### Step 2: Run the workflow

Invoke the Workflow tool with the script below verbatim and:

```json
{
  "proposalPath": "proposals/<file>.md",
  "repoRoot": "<absolute repo root>",
  "maxRounds": 5
}
```

Pass `args` as a JSON object value. Agents inherit the session model and effort level.

### Step 3: Interruptions

On interruption (auth expiry, crash): stop the stale task with TaskStop, then relaunch with `{scriptPath, resumeFromRunId}` from the original tool result. Completed agents replay from the journal cache; because appliers edit files, confirm after resume that the final verification round ran against the final file state (the loop re-verifies all files every round, so a completed run guarantees this).

### Step 4: Report

1. Run `git status --porcelain` and confirm the only modified files are `spec/` targets named by the proposal. If anything else changed, restore it and report the violation.
2. On `status: "applied"`: report the edits applied per file, the recorded rule deviations, the verification rounds run with the discrepancy counts, and the staged non-spec changes that remain for the implementation work. Suggest updating the proposal's Status bullet to record application and committing; do neither unless the user asks.
3. On `status: "applied-with-blockers"`: as above, plus the unappliable edits with their reasons (usually drifted anchors). The user resolves the blockers; the spec holds the partial application, which the report states plainly.
4. On `status: "not-clean"`: the round cap was hit with discrepancies outstanding. Report them with their expected and observed text for a human decision.
5. On `status: "not-approved"` or `status: "dirty-baseline"`: no file changed; report the reason.

```js
export const meta = {
  name: "spec-apply",
  description:
    "Apply an approved proposal's staged spec edits and verify exact alignment until clean",
  whenToUse: "Land a signed-off proposals/*.md in spec/",
};

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || !input.proposalPath || !input.repoRoot) {
  throw new Error("args.proposalPath and args.repoRoot are required");
}
const repo = input.repoRoot;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const maxRounds = input.maxRounds || 5;

const RULES =
  "Spec content rules (these take precedence over verbatim application; record every deviation they force):\n" +
  "- The spec never references source code files or implementation paths (pkg/, cmd/, charts/, sdks/, tests/, migrations/, .go or other source files). Rephrase staged text carrying such a reference into behavioral spec language, or drop the reference.\n" +
  "- The spec cross-references other spec content by section number only: §X.Y or a relative markdown link to a section anchor. Replace a line-number cross-reference in staged text with the containing section's number.\n" +
  "- Line numbers in the proposal's ANCHOR INSTRUCTIONS are location hints for you and never become spec content. Locate anchors by the quoted text and section headings; line numbers drift.\n" +
  "- Apply staged prose as written otherwise; do not restyle it.";

const PLAN = {
  type: "object",
  required: ["approved", "statusLine", "specEdits", "nonSpecStaged"],
  properties: {
    approved: { type: "boolean" },
    statusLine: { type: "string" },
    specEdits: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "targetFile", "subsection", "summary"],
        properties: {
          id: { type: "string" },
          targetFile: {
            type: "string",
            description: "Path under spec/, relative to the repo root",
          },
          subsection: {
            type: "string",
            description:
              "The proposal subsection heading that stages this edit",
          },
          summary: { type: "string" },
        },
      },
    },
    nonSpecStaged: {
      type: "array",
      items: {
        type: "object",
        required: ["subsection", "target", "summary"],
        properties: {
          subsection: { type: "string" },
          target: { type: "string" },
          summary: { type: "string" },
        },
      },
    },
  },
};

const APPLY_RESULT = {
  type: "object",
  required: ["applied", "unappliable", "deviations"],
  properties: {
    applied: { type: "array", items: { type: "string" } },
    unappliable: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "reason"],
        properties: {
          id: { type: "string" },
          reason: { type: "string" },
        },
      },
    },
    deviations: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "rule", "original", "replacement"],
        properties: {
          id: { type: "string" },
          rule: { type: "string" },
          original: { type: "string" },
          replacement: { type: "string" },
        },
      },
    },
  },
};

const DISCREPANCIES = {
  type: "object",
  required: ["discrepancies"],
  properties: {
    discrepancies: {
      type: "array",
      items: {
        type: "object",
        required: ["title", "file", "where", "expected", "observed", "fix"],
        properties: {
          title: { type: "string" },
          file: { type: "string" },
          where: { type: "string" },
          expected: {
            type: "string",
            description: "What the proposal stages, quoted exactly",
          },
          observed: {
            type: "string",
            description: "What the spec now says, quoted exactly",
          },
          fix: { type: "string" },
        },
      },
    },
  },
};

// ---- Plan ----

phase("Plan");
const plan = await agent(
  "Read the proposal at " +
    proposal +
    ' in full and extract its staged changes.\n\nYou are a read-only investigator; do not edit any file.\n\nReturn:\n- approved: whether the Status bullet records approval (an "Approved" state). A draft or in-review status is not approval.\n- statusLine: the Status bullet verbatim.\n- specEdits: one entry per staged change whose target file is under spec/. Use the "Proposed spec changes" section\'s own subsections as the unit: id (the subsection number, e.g. "7.1"), targetFile (the spec/ path it names), subsection (the heading), summary (one line). A subsection that targets multiple spec files becomes one entry per file.\n- nonSpecStaged: one entry per staged change whose target is outside spec/ (code, charts, docs, schemas, generated artifacts). These are reported, never applied.',
  { schema: PLAN, label: "plan" },
);

if (!plan.approved) {
  return {
    status: "not-approved",
    statusLine: plan.statusLine,
  };
}
if (plan.specEdits.length === 0) {
  return {
    status: "applied",
    note: "the proposal stages no spec/ edits",
    nonSpecStaged: plan.nonSpecStaged,
    rounds: 0,
  };
}

const files = [...new Set(plan.specEdits.map((e) => e.targetFile))];
log(
  plan.specEdits.length +
    " staged spec edits across " +
    files.length +
    " files; " +
    plan.nonSpecStaged.length +
    " non-spec staged changes left to the implementation work",
);

// ---- Apply (one agent per target file; files are disjoint) ----

phase("Apply");
const applyResults = (
  await parallel(
    files.map((f) => () => {
      const edits = plan.specEdits.filter((e) => e.targetFile === f);
      return agent(
        "Apply staged spec edits from an approved proposal to one spec file.\n\n" +
          "HARD CONSTRAINT: the only file you may edit is " +
          repo +
          "/" +
          f +
          ". Never modify the proposal or any other file.\n\n" +
          "Proposal: " +
          proposal +
          " (read the whole 'Proposed spec changes' section first for context).\n" +
          "Edits to apply to this file, in order:\n" +
          JSON.stringify(edits, null, 2) +
          "\n\n" +
          "For each edit: read the proposal subsection, locate the anchor in the target file by its quoted text and section heading, and apply the staged text exactly as written (fenced blocks verbatim; replacement instructions replace exactly the text they name). " +
          RULES +
          "\n\nIf an anchor cannot be located with certainty, skip that edit and record it as unappliable with the reason; never guess a location. Return the applied edit ids, the unappliable edits, and every rule-forced deviation (rule, original staged text, replacement you applied).",
        { schema: APPLY_RESULT, label: "apply:" + f.split("/").pop() },
      );
    }),
  )
).filter(Boolean);

const unappliable = applyResults.flatMap((r) => r.unappliable);
const deviations = applyResults.flatMap((r) => r.deviations);
if (deviations.length > 0)
  log(deviations.length + " rule-forced deviations recorded");
if (unappliable.length > 0)
  log(unappliable.length + " edits unappliable (drifted anchors)");

const appliedIds = new Set(applyResults.flatMap((r) => r.applied));
const verifiableEdits = plan.specEdits.filter((e) => appliedIds.has(e.id));

// ---- Verify and fix until a full round is clean ----

const DEVIATION_NOTE =
  deviations.length > 0
    ? "\n\nRecorded rule-forced deviations (EXPECTED differences from the staged text; do not report them as discrepancies):\n" +
      JSON.stringify(deviations, null, 2)
    : "";

function verifyFilePrompt(f, edits, round) {
  return (
    "You verify that applied spec edits align exactly with the proposal that staged them. Round " +
    round +
    ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
    repo +
    ".\n\nProposal: " +
    proposal +
    ". Target file: " +
    f +
    ". Edits expected in this file:\n" +
    JSON.stringify(edits, null, 2) +
    "\n\nMethod: read each proposal subsection; read the current target file; run `git diff -- " +
    f +
    "` to see exactly what changed against the clean baseline. Verify all of:\n" +
    "1. Every staged block appears at its anchored location, character-exact (modulo the recorded deviations below).\n" +
    "2. Text the proposal replaces or removes is gone, and nothing it keeps was altered.\n" +
    "3. The diff for this file contains nothing beyond the staged edits: no stray edits, no duplicate insertions, no truncated surroundings.\n" +
    "4. Every cross-reference the applied text adds resolves: a §X.Y number names an existing section, and a relative markdown link's anchor exists in its target file.\n" +
    "5. No added line references source code files or implementation paths, and no added cross-reference uses line numbers (flag cross-references only; incidental prose containing the word 'line' is fine).\n" +
    DEVIATION_NOTE +
    "\n\nReport each discrepancy with exact expected and observed quotes and a concrete fix. An empty list means the file aligns."
  );
}

function sweepPrompt(round) {
  return (
    "You are a mechanical rules sweep over the applied spec diff. Round " +
    round +
    ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
    repo +
    ".\n\nRun `git diff -- spec/` and inspect ONLY the added lines (lines starting with '+'). Flag as a discrepancy:\n" +
    "- any reference to source code files or implementation paths: pkg/, cmd/, charts/, sdks/, tests/, migrations/, or a source file extension such as .go;\n" +
    "- any cross-reference by line number ('line 123', 'lines 45-48') to spec or any other file. Cross-references only; incidental prose is fine.\n" +
    "Pre-existing text (context and removed lines) is out of scope. Quote each offending added line exactly, name its file, and give the rule-conformant replacement." +
    DEVIATION_NOTE
  );
}

function fixPrompt(f, found, round) {
  return (
    "You fix verified discrepancies between applied spec edits and the proposal that staged them. Round " +
    round +
    ".\n\nHARD CONSTRAINT: the only file you may edit is " +
    repo +
    "/" +
    f +
    ". Never modify the proposal or any other file.\n\nProposal: " +
    proposal +
    ".\n" +
    RULES +
    "\n\nDiscrepancies to fix (apply each exactly; the expected text is authoritative except where a content rule forces a deviation, which you record in your reply):\n" +
    JSON.stringify(found, null, 2) +
    "\n\nMake the smallest edits that resolve each discrepancy. Return a short summary listing each discrepancy and the exact edit you made."
  );
}

let round = 0;
let clean = false;
const history = [];

while (round < maxRounds && !clean) {
  round++;
  log("Verification round " + round);
  const checks = files.map((f) => () =>
    agent(
      verifyFilePrompt(
        f,
        verifiableEdits.filter((e) => e.targetFile === f),
        round,
      ),
      {
        schema: DISCREPANCIES,
        label: "verify:" + f.split("/").pop() + ":r" + round,
        phase: "Round " + round + ": verify",
      },
    ),
  );
  checks.push(() =>
    agent(sweepPrompt(round), {
      schema: DISCREPANCIES,
      label: "verify:rules-sweep:r" + round,
      phase: "Round " + round + ": verify",
    }),
  );

  const results = (await parallel(checks)).filter(Boolean);
  if (results.length === 0) {
    log("Round " + round + ": every verifier failed; stopping");
    history.push({ round, discrepancies: -1, note: "verifiers failed" });
    break;
  }
  const found = results.flatMap((r) => r.discrepancies);
  history.push({
    round,
    discrepancies: found.length,
    titles: found.map((d) => d.title),
  });
  log("Round " + round + ": " + found.length + " discrepancies");

  if (found.length === 0) {
    clean = true;
    break;
  }

  const fixFiles = [...new Set(found.map((d) => d.file))];
  await parallel(
    fixFiles.map((f) => () =>
      agent(
        fixPrompt(
          f,
          found.filter((d) => d.file === f),
          round,
        ),
        {
          label: "fix:" + f.split("/").pop() + ":r" + round,
          phase: "Round " + round + ": fix",
        },
      ),
    ),
  );
}

return {
  status: clean
    ? unappliable.length > 0
      ? "applied-with-blockers"
      : "applied"
    : "not-clean",
  files,
  appliedEdits: [...appliedIds],
  unappliable,
  deviations,
  nonSpecStaged: plan.nonSpecStaged,
  rounds: round,
  history,
};
```

## Maintenance

When application surfaces a new discrepancy class (a way applied text can diverge from staged text, or a new forbidden reference pattern), add it to the verifier checklist or the rules sweep above. Keep the content rules short and mechanical; the verifiers quote them verbatim.
