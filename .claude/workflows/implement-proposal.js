// Implement an approved spec proposal end to end: apply its staged spec
// edits to spec/ and verify them, then (optionally) implement the spec
// change in code and close the findings that reference it. This unifies
// the former spec-apply (land + verify spec) and spec-implement (plan +
// build code + close findings) into one entry point.
//
//   Workflow({ name: "implement-proposal", args: {
//     proposalPath: "proposals/NNNN_*.md",  // required
//     date: "YYYY-MM-DD",                    // required (scripts cannot call Date)
//     repoRoot: "/abs/path",                 // optional; defaults to this project
//     implementCode: true,                   // optional, default true; false = land + verify spec only
//     maxApplyRounds: 5,                      // optional; spec apply-verify-fix rounds
//   }})
//
// Spec always comes first: the staged spec edits are applied and verified
// before any code. With implementCode false the run stops after the spec
// is landed and committed (the former spec-apply behavior). The code phase
// is the implement-proposal-build subworkflow (blast radius + ordered
// build sequence + step-by-step implementation with tests).
//
// Preconditions (the skill checks them before invoking): the proposal
// Status is "Approved" (or already "Applied to spec" for a re-run), and
// spec/ is clean in git so the apply verification can diff against a clean
// baseline.
//
// MAINTENANCE: the implement-proposal skill documents this workflow and its
// subworkflow; keep them in sync.

export const meta = {
  name: "implement-proposal",
  description:
    "Apply an approved proposal's spec edits and verify them, then optionally implement the spec change in code and close the findings that reference it",
  phases: [
    { title: "Plan", detail: "read the proposal, gate on approval, extract staged spec edits and findings" },
    { title: "Apply spec", detail: "land the staged spec edits and verify exact alignment until clean" },
    { title: "Implement", detail: "implement-proposal-build subworkflow: blast radius, build sequence, tests" },
    { title: "Close findings", detail: "mark associated BUILD-GAPS.md findings CLOSED" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath || !input.date) {
  throw new Error("args.proposalPath and args.date are required");
}
const repo = input.repoRoot || "/Users/joan/projects/lenny";
const date = input.date;
const implementCode = input.implementCode !== false; // default true
const maxApplyRounds = input.maxApplyRounds || 5;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const relProposal = input.proposalPath.startsWith("/")
  ? input.proposalPath.replace(repo + "/", "")
  : input.proposalPath;

const SPEC_RULES =
  "Spec content rules (these take precedence over verbatim application; record every deviation they force):\n" +
  "- The spec never references source code files or implementation paths (pkg/, cmd/, charts/, sdks/, tests/, migrations/, .go or other source files). Rephrase staged text carrying such a reference into behavioral spec language, or drop the reference.\n" +
  "- The spec cross-references other spec content by section number only: §X.Y or a relative markdown link to a section anchor. Replace a line-number cross-reference in staged text with the containing section's number.\n" +
  "- Line numbers in the proposal's ANCHOR INSTRUCTIONS are location hints for you and never become spec content. Locate anchors by the quoted text and section headings; line numbers drift.\n" +
  "- Apply staged prose as written otherwise; do not restyle it.";

const PLAN = {
  type: "object",
  required: ["approved", "alreadyApplied", "statusLine", "specEdits", "nonSpecStaged", "findingIds"],
  properties: {
    approved: { type: "boolean", description: 'Status bullet begins "Approved"' },
    alreadyApplied: { type: "boolean", description: 'Status bullet begins "Applied to spec"' },
    statusLine: { type: "string" },
    specEdits: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "targetFile", "subsection", "summary"],
        properties: {
          id: { type: "string" },
          targetFile: { type: "string", description: "Path under spec/, relative to the repo root" },
          subsection: { type: "string", description: "The proposal subsection heading that stages this edit" },
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
    findingIds: {
      type: "array",
      items: { type: "string" },
      description: "OPEN findings in BUILD-GAPS.md whose body references this proposal",
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
        properties: { id: { type: "string" }, reason: { type: "string" } },
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
          expected: { type: "string", description: "What the proposal stages, quoted exactly" },
          observed: { type: "string", description: "What the spec now says, quoted exactly" },
          fix: { type: "string" },
        },
      },
    },
  },
};

const ALIGNMENT = {
  type: "object",
  required: ["aligned", "missing"],
  properties: {
    aligned: { type: "boolean", description: "every staged spec edit is present at its anchor in spec/" },
    missing: { type: "array", items: { type: "string" } },
  },
};

const CLOSE = {
  type: "object",
  required: ["closed", "committed"],
  properties: {
    closed: { type: "array", items: { type: "string" } },
    committed: { type: "boolean" },
    notes: { type: "string" },
  },
};

// ---- Plan: read the proposal, gate, extract staged edits and findings ----

phase("Plan");
const plan = await agent(
  "Read the proposal at " +
    proposal +
    ' in full and extract its staged changes and the findings that reference it.\n\nYou are a read-only investigator; do not edit any file. Work in ' +
    repo +
    '.\n\nReturn:\n- approved: true when the Status bullet begins "Approved" (approved for implementation).\n- alreadyApplied: true when the Status bullet begins "Applied to spec" (the spec edits were already landed by a prior run). A "Draft" or "Verified" status is neither.\n- statusLine: the Status bullet verbatim.\n- specEdits: one entry per staged change whose target file is under spec/, from the "Proposed spec changes" section: id (the subsection number, e.g. "7.1"), targetFile (the spec/ path), subsection (the heading), summary. A subsection targeting multiple spec files becomes one entry per file.\n- nonSpecStaged: one entry per staged change whose target is outside spec/ (code, charts, docs, schemas). These are implemented in the code phase or reported, never hand-applied here.\n- findingIds: every OPEN finding in BUILD-GAPS.md whose body references this proposal by path or number (the file is large; grep for the proposal number and filename). Empty array if none.',
  { schema: PLAN, label: "plan", phase: "Plan" },
);

if (!plan.approved && !plan.alreadyApplied) {
  return {
    status: "not-approved",
    statusLine: plan.statusLine,
    reason:
      "the proposal is not approved for implementation (Status: " +
      plan.statusLine +
      "). Approve it before implementing.",
  };
}

const files = [...new Set(plan.specEdits.map((e) => e.targetFile))];

// ---- Apply spec (or, on a re-run, confirm it is already aligned) ----

let specStatus = "applied"; // applied | applied-with-blockers | not-clean | aligned | no-spec-edits
let unappliable = [];
let deviations = [];
let appliedIds = new Set();
let applyHistory = [];

if (plan.specEdits.length === 0) {
  specStatus = "no-spec-edits";
  log("Proposal stages no spec edits; nothing to land");
} else if (plan.alreadyApplied) {
  // Idempotent re-run: the spec was already landed and committed, so a
  // diff-based check would be empty. Confirm by presence instead.
  phase("Apply spec");
  log("Status is Applied to spec; verifying the staged edits are present");
  const align = await agent(
    "Confirm an already-applied proposal's staged spec edits are present in spec/.\n\n" +
      "You are a read-only verifier; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      ". For each staged edit in its 'Proposed spec changes' section, read the named spec/ file and confirm the staged block is present at its anchor. Set aligned true only when every staged edit is present; list any missing ones.",
    { schema: ALIGNMENT, label: "verify-aligned", phase: "Apply spec" },
  );
  if (!align || !align.aligned) {
    return {
      status: "not-aligned",
      statusLine: plan.statusLine,
      reason:
        "the proposal reads Applied to spec but its staged edits are not all present in spec/: " +
        ((align && align.missing) || []).join("; ") +
        ". The spec drifted from the proposal; re-land it before implementing.",
    };
  }
  specStatus = "aligned";
} else {
  // Fresh apply: the proposal is Approved and spec/ is a clean baseline.
  phase("Apply spec");
  log(
    plan.specEdits.length +
      " staged spec edits across " +
      files.length +
      " files; applying",
  );
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
            "\n\nFor each edit: read the proposal subsection, locate the anchor in the target file by its quoted text and section heading, and apply the staged text exactly as written (fenced blocks verbatim; replacement instructions replace exactly the text they name). " +
            SPEC_RULES +
            "\n\nIf an anchor cannot be located with certainty, skip that edit and record it as unappliable with the reason; never guess a location. Return the applied edit ids, the unappliable edits, and every rule-forced deviation.",
          { schema: APPLY_RESULT, label: "apply:" + f.split("/").pop(), phase: "Apply spec" },
        );
      }),
    )
  ).filter(Boolean);

  unappliable = applyResults.flatMap((r) => r.unappliable);
  deviations = applyResults.flatMap((r) => r.deviations);
  appliedIds = new Set(applyResults.flatMap((r) => r.applied));
  if (deviations.length > 0) log(deviations.length + " rule-forced deviations recorded");
  if (unappliable.length > 0) log(unappliable.length + " edits unappliable (drifted anchors)");

  const verifiableEdits = plan.specEdits.filter((e) => appliedIds.has(e.id));
  const DEVIATION_NOTE =
    deviations.length > 0
      ? "\n\nRecorded rule-forced deviations (EXPECTED differences from the staged text; do not report them as discrepancies):\n" +
        JSON.stringify(deviations, null, 2)
      : "";

  const verifyFilePrompt = (f, edits, round) =>
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
    "\n\nReport each discrepancy with exact expected and observed quotes and a concrete fix. An empty list means the file aligns.";

  const sweepPrompt = (round) =>
    "You are a mechanical rules sweep over the applied spec diff. Round " +
    round +
    ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
    repo +
    ".\n\nRun `git diff -- spec/` and inspect ONLY the added lines (lines starting with '+'). Flag as a discrepancy:\n" +
    "- any reference to source code files or implementation paths: pkg/, cmd/, charts/, sdks/, tests/, migrations/, or a source file extension such as .go;\n" +
    "- any cross-reference by line number ('line 123', 'lines 45-48') to spec or any other file. Cross-references only; incidental prose is fine.\n" +
    "Pre-existing text (context and removed lines) is out of scope. Quote each offending added line exactly, name its file, and give the rule-conformant replacement." +
    DEVIATION_NOTE;

  const fixPrompt = (f, found, round) =>
    "You fix verified discrepancies between applied spec edits and the proposal that staged them. Round " +
    round +
    ".\n\nHARD CONSTRAINT: the only file you may edit is " +
    repo +
    "/" +
    f +
    ". Never modify the proposal or any other file.\n\nProposal: " +
    proposal +
    ".\n" +
    SPEC_RULES +
    "\n\nDiscrepancies to fix (the expected text is authoritative except where a content rule forces a deviation, which you record in your reply):\n" +
    JSON.stringify(found, null, 2) +
    "\n\nMake the smallest edits that resolve each discrepancy. Return a short summary of each fix.";

  let round = 0;
  let clean = false;
  while (round < maxApplyRounds && !clean) {
    round++;
    log("Spec verification round " + round);
    const checks = files.map((f) => () =>
      agent(verifyFilePrompt(f, verifiableEdits.filter((e) => e.targetFile === f), round), {
        schema: DISCREPANCIES,
        label: "verify:" + f.split("/").pop() + ":r" + round,
        phase: "Apply spec",
      }),
    );
    checks.push(() =>
      agent(sweepPrompt(round), { schema: DISCREPANCIES, label: "verify:rules-sweep:r" + round, phase: "Apply spec" }),
    );
    const results = (await parallel(checks)).filter(Boolean);
    if (results.length === 0) {
      applyHistory.push({ round, discrepancies: -1, note: "verifiers failed" });
      break;
    }
    const found = results.flatMap((r) => r.discrepancies);
    applyHistory.push({ round, discrepancies: found.length, titles: found.map((d) => d.title) });
    log("Round " + round + ": " + found.length + " discrepancies");
    if (found.length === 0) {
      clean = true;
      break;
    }
    const fixFiles = [...new Set(found.map((d) => d.file))];
    await parallel(
      fixFiles.map((f) => () =>
        agent(fixPrompt(f, found.filter((d) => d.file === f), round), {
          label: "fix:" + f.split("/").pop() + ":r" + round,
          phase: "Apply spec",
        }),
      ),
    );
  }

  specStatus = clean ? (unappliable.length > 0 ? "applied-with-blockers" : "applied") : "not-clean";

  if (specStatus === "not-clean") {
    return {
      status: "spec-not-clean",
      reason: "the spec apply verification did not converge within " + maxApplyRounds + " rounds; the staged edits are partially applied in the working tree for inspection.",
      applyHistory,
      unappliable,
    };
  }
  if (specStatus === "applied-with-blockers") {
    return {
      status: "spec-applied-with-blockers",
      reason: "some staged spec edits could not be located (drifted anchors); resolve them before implementing code.",
      unappliable,
      appliedEdits: [...appliedIds],
      applyHistory,
    };
  }

  // Clean apply: record the status on the proposal and commit the spec edits.
  await agent(
    "Record application on a proposal's Status bullet, then commit the applied spec edits.\n\n" +
      "Work in " +
      repo +
      ". Edit only " +
      proposal +
      " (the Status bullet) and commit the applied spec/ files alongside it. Do not edit code or other files.\n\n" +
      '1. In ' +
      proposal +
      ', replace the Status bullet\'s leading state (for example "Approved for implementation as written (...).") with: "Applied to spec (' +
      date +
      ')." Preserve later clauses that remain true; follow ' +
      repo +
      "/.claude/rules/doc-style.md.\n" +
      "2. Commit the changed spec/ files together with " +
      relProposal +
      " on the current branch, message in the repository's convention (read `git log --oneline -5`), e.g. 'spec: apply " +
      relProposal +
      "'. Spec lands as its own commit before any code.",
    { label: "mark-and-commit-spec", phase: "Apply spec" },
  );
  log("Spec applied, status recorded, and committed");
}

// ---- Implement code (optional) via the build subworkflow ----

if (!implementCode) {
  return {
    status: "spec-only",
    specStatus,
    statusLine: plan.statusLine,
    files,
    appliedEdits: [...appliedIds],
    deviations,
    nonSpecStaged: plan.nonSpecStaged,
    findingsReferencing: plan.findingIds,
    applyHistory,
  };
}

phase("Implement");
let build;
try {
  build = await workflow("implement-proposal-build", {
    proposalPath: input.proposalPath,
    date,
    repoRoot: repo,
  });
} catch (e) {
  return {
    status: "aborted",
    abortReason: "implement-proposal-build subworkflow failed: " + (e && e.message),
    specStatus,
    findingsReferencing: plan.findingIds,
  };
}
log(
  "Implementation done: " +
    (build.steps ? build.steps.length : 0) +
    " steps, green=" +
    !!build.green +
    ", reviewClean=" +
    !!build.reviewClean +
    (build.status === "step-stuck" ? ", stuck at step " + build.stuckStep : ""),
);

// ---- Close the findings that reference the proposal ----

phase("Close findings");
let close = { closed: [], committed: false };
if (plan.findingIds.length > 0) {
  if (!build.green || !build.reviewClean) {
    log("Implementation is not green or the design-conformance review is not clean; leaving findings OPEN for review");
  } else {
    close = await agent(
      "Close the BUILD-GAPS.md findings whose implementation just landed, then commit.\n\n" +
        "HARD CONSTRAINT: the only file you may edit is " +
        repo +
        "/BUILD-GAPS.md. Do not edit the proposal, spec/, or code.\n\n" +
        "Proposal implemented and verified green: " +
        relProposal +
        ". Implementing commits: " +
        (build.commits || []).join(", ") +
        ".\nFindings to close: " +
        plan.findingIds.join(", ") +
        ".\n\nFor each finding: re-read it, confirm the landed implementation resolves it (the proposal's spec edits are applied and its code blast radius is implemented and green), flip the heading checkbox to [x] and the trailing marker to CLOSED, and add a one or two sentence Resolution note citing the proposal path and the implementing commit SHA(s). Do not open or re-open any finding. Then commit BUILD-GAPS.md on the current branch following the repository's commit conventions. Return the IDs you closed.",
      { schema: CLOSE, label: "close-findings", phase: "Close findings" },
    );
  }
}

return {
  status:
    build.status === "step-stuck"
      ? "build-step-stuck"
      : build.green && build.reviewClean
        ? "implemented"
        : "implemented-not-green",
  proposal: relProposal,
  specStatus,
  blastRadius: build.blastRadius,
  steps: build.steps,
  commits: build.commits,
  green: !!build.green,
  reviewClean: !!build.reviewClean,
  reviewFindings: build.reviewFindings || [],
  stuckStep: build.stuckStep,
  changedLineCoverage: build.changedLineCoverage,
  failures: build.failures || [],
  resumeNote: build.resumeNote,
  findingsReferencing: plan.findingIds,
  findingsClosed: close.closed || [],
};
