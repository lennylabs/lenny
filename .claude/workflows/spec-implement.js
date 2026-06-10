// Implement an applied spec proposal in code, then close the findings
// that reference it. The implementation step is the spec-implement-build
// subworkflow (blast radius + ordered build sequence + step-by-step
// implementation with tests). This workflow verifies the proposal's spec
// edits are already applied (it does not apply them — spec-apply does),
// runs the subworkflow, and marks the associated findings CLOSED.
//
//   Workflow({ name: "spec-implement", args: {
//     proposalPath: "proposals/NNNN_*.md",  // required
//     date: "YYYY-MM-DD",                    // required (scripts cannot call Date)
//     repoRoot: "/abs/path",                 // optional; defaults to this project
//     maxTier: "e2e"                          // optional; cap forwarded to the build subworkflow
//   }})
//
// MAINTENANCE: the spec-implement skill (.claude/skills/spec-implement)
// documents this workflow and its subworkflow; keep them in sync.

export const meta = {
  name: "spec-implement",
  description:
    "Verify an approved proposal's spec edits are applied, implement the spec change in code via the build subworkflow, then close the findings that reference it",
  phases: [
    { title: "Verify applied", detail: "proposal status Applied to spec + spec actually contains the staged edits" },
    { title: "Implement", detail: "spec-implement-build subworkflow: blast radius, build sequence, tests" },
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
const relProposal = input.proposalPath.replace(/^\//, "").replace(repo.replace(/^\//, "") + "/", "");
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;

const VERIFY = {
  type: "object",
  required: ["applied", "specAligned", "statusLine", "findingIds"],
  properties: {
    applied: {
      type: "boolean",
      description: 'the proposal Status bullet begins "Applied to spec"',
    },
    specAligned: {
      type: "boolean",
      description:
        "the proposal's staged spec edits are actually present in spec/ (verified by reading, not by trusting the status)",
    },
    statusLine: { type: "string" },
    findingIds: {
      type: "array",
      items: { type: "string" },
      description: "OPEN findings in BUILD-GAPS.md whose body references this proposal",
    },
    blockReason: { type: "string" },
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

// ---- Verify the spec edits are applied ----

phase("Verify applied");
const v = await agent(
  "Verify that an approved spec proposal's spec edits have already been applied to spec/, and find the findings that reference it.\n\n" +
    "You are a read-only verifier; do not edit any file. Work in " +
    repo +
    ".\n\nProposal: " +
    proposal +
    ".\n" +
    '1. Read its Status bullet. applied is true only when the Status begins "Applied to spec" (spec-apply sets this after a clean application). A Status of "Approved", "Verified", or "Draft" means the spec edits are NOT yet applied — set applied false.\n' +
    "2. Independently confirm the spec actually contains the change: read the proposal's Proposed spec changes section and check that each staged block is present at its anchor in the named spec/ file (do not trust the status alone). Set specAligned true only when every staged spec edit is present.\n" +
    "3. Find every OPEN finding in BUILD-GAPS.md whose body references this proposal by path or number (the file is large; grep for the proposal number and filename). Return their IDs in findingIds (empty array if none).\n" +
    "Set blockReason when applied or specAligned is false, naming what is missing.",
  { schema: VERIFY, label: "verify-applied", phase: "Verify applied" },
);

if (!v) {
  return { status: "aborted", abortReason: "verify agent failed" };
}
if (!v.applied || !v.specAligned) {
  return {
    status: "not-applied",
    statusLine: v.statusLine,
    reason:
      (v.blockReason || "the proposal's spec edits are not applied to spec/") +
      " — run the spec-apply skill on " +
      relProposal +
      " before implementing.",
    findingIds: v.findingIds || [],
  };
}
log(
  "Spec edits verified applied; " +
    (v.findingIds.length || 0) +
    " finding(s) reference the proposal",
);

// ---- Implement: the build subworkflow ----

phase("Implement");
let build;
try {
  build = await workflow("spec-implement-build", {
    proposalPath: input.proposalPath,
    date,
    repoRoot: repo,
    maxTier: input.maxTier,
  });
} catch (e) {
  return {
    status: "aborted",
    abortReason: "spec-implement-build subworkflow failed: " + (e && e.message),
    findingIds: v.findingIds,
  };
}
log(
  "Implementation done: " +
    (build.steps ? build.steps.length : 0) +
    " steps, green=" +
    !!build.green,
);

// ---- Close the findings that reference the proposal ----

phase("Close findings");
let close = { closed: [], committed: false };
if (v.findingIds.length > 0) {
  if (!build.green) {
    log(
      "Implementation is not green; leaving findings OPEN and reporting for review",
    );
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
        v.findingIds.join(", ") +
        ".\n\nFor each finding: re-read it, confirm the landed implementation resolves it (the proposal's spec edits are applied and its code blast radius is implemented and green), flip the heading checkbox to [x] and the trailing marker to CLOSED, and add a one or two sentence Resolution note citing the proposal path and the implementing commit SHA(s). Do not open or re-open any finding. Then commit BUILD-GAPS.md on the current branch following the repository's commit conventions. Return the IDs you closed.",
      { schema: CLOSE, label: "close-findings", phase: "Close findings" },
    );
  }
}

return {
  status: build.green ? "implemented" : "implemented-not-green",
  proposal: relProposal,
  blastRadius: build.blastRadius,
  steps: build.steps,
  commits: build.commits,
  green: !!build.green,
  changedLineCoverage: build.changedLineCoverage,
  failures: build.failures || [],
  findingsReferencing: v.findingIds,
  findingsClosed: close.closed || [],
};
