// Implement an approved spec proposal end to end: apply its staged spec
// edits to spec/ and verify them, then (optionally) implement the spec
// change in code and close the findings that reference it. This unifies
// the former spec-apply (land + verify spec) and spec-implement (plan +
// build code + close findings) into one entry point.
//
//   Workflow({ name: "implement-proposal", args: {
//     proposalPath: "proposals/NNNN_*.md",  // required
//     date: "YYYY-MM-DD",                    // required (scripts cannot call Date)
//     repoRoot: "/abs/path",                 // required: the repository root
//     implementCode: true,                   // optional, default true; false = land + verify spec only
//     maxApplyRounds: 5,                      // optional; spec apply-verify-fix rounds
//   }})
//
// Spec always comes first: the staged spec edits are applied and verified
// before any code, one sub-step at a time. Each sub-step is applied, verified
// to clean, and committed before the next begins, so a defect is attributable
// to the sub-step that introduced it and a bad sub-step is revertable without
// discarding the ones that already verified clean. An edit whose anchor cannot
// be located stops the run rather than being skipped, because a partially
// applied file makes every other discrepancy ambiguous. With implementCode false the run stops after the spec
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
    { title: "Close findings", detail: "mark associated BUILD-GAPS.md findings CLOSED" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath || !input.date) {
  throw new Error("args.proposalPath and args.date are required");
}
// Required rather than defaulted: a default is one machine's checkout, and a
// workflow that silently runs against the wrong tree is worse than one that
// stops. change-proposal.js requires it for the same reason.
if (!input.repoRoot) throw new Error("args.repoRoot is required and missing");
const repo = input.repoRoot;
const date = input.date;
const implementCode = input.implementCode !== false; // default true
const maxApplyRounds = input.maxApplyRounds || 5;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const relProposal = input.proposalPath.startsWith("/")
  ? input.proposalPath.replace(repo + "/", "")
  : input.proposalPath;
// Where this proposal's parts live. Folder layout or legacy single file; every
// prompt below names a role through this rather than concatenating a path.
const P = proposalFiles(input.proposalPath, repo);

// ---- The spec/ write lease -----------------------------------------------
//
// spec/ is read-only unless a lease is open (.claude/tools/spec-lease.mjs and
// the PreToolUse hook in settings.json). The lease names THIS proposal and the
// files it is allowed to write, and it expires. Nothing in this phase can edit
// spec/ without it, which is the point: an agent that wanders into spec/ is
// refused rather than trusted.
//
// A workflow script has no filesystem access, so opening and releasing are
// agent calls. They are DEDICATED agents given one exact command, not an
// instruction appended to an agent that has other work: the lease is the
// security boundary here, its failure is not benign, and an agent with a large
// task in front of it is not a reliable executor of a small instruction at the
// end of its prompt.
const leaseTtlHours = input.leaseTtlHours || 24;
let leaseOpen = false;

async function openLease(step, allowFiles) {
  const allow = (allowFiles || []).join(",");
  const out = await agent(
    "Run exactly this command and reply with its stdout and nothing else:\n\n" +
      "node " + repo + "/.claude/tools/spec-lease.mjs open '" + P.root + "'" +
      " --step '" + (step || "apply") + "'" +
      " --ttl-hours " + leaseTtlHours +
      " --allow '" + allow + "'" +
      "\n\nDo nothing else. Do not read, summarise, or edit any file.",
    { label: "lease-open:" + (step || "apply"), model: "haiku", phase: "Apply spec" },
  );
  leaseOpen = true;
  return out;
}

// Called on EVERY return path and at the end of every spec step, whether it
// succeeded or failed. A release that does not happen leaves spec/ writable,
// so this is never folded into an agent that has other work to do.
async function releaseLease(step) {
  if (!leaseOpen) return;
  leaseOpen = false;
  await agent(
    "Run exactly this command and reply with its stdout and nothing else:\n\n" +
      "node " + repo + "/.claude/tools/spec-lease.mjs release" +
      (step ? " --step '" + step + "'" : "") +
      "\n\nDo nothing else. Do not read, summarise, or edit any file.",
    { label: "lease-release:" + (step || "apply"), model: "haiku", phase: "Apply spec" },
  );
}

// ---- Where a proposal's parts live ---------------------------------------
//
// A proposal is a directory of role-scoped files:
//   proposals/NNNN_kind_slug/NNNN_kind_slug.problem-statement.md
//   ...summary, status, implementation-checklist, spec-changes,
//      non-spec-changes, review-log, deviations
//
// A proposal written before that layout is a single NNNN_kind_slug.md, and 79
// of those exist. Both resolve here so no prompt ever concatenates a path by
// hand, and so a legacy proposal still runs end to end: every role points at
// the single file, and the prompts that consume a role say "the <role> section
// of" rather than "the file".
//
// The layout is decided from the path string rather than by looking: a
// workflow script has no filesystem access (see the sandbox note above), and a
// path ending in .md is a legacy proposal while one that does not is a
// directory. The pipeline calls migrate-proposal.js at startup on a legacy
// path, so by the time the review or build loops run the path is a directory.
function proposalFiles(ref, repoRoot) {
  const abs = ref.startsWith("/") ? ref : repoRoot + "/" + ref;
  const legacy = /\.md$/.test(abs);
  if (legacy) {
    const stem = abs.replace(/^.*\//, "").replace(/\.md$/, "");
    return {
      layout: "legacy",
      stem,
      dir: abs.replace(/\/[^/]*$/, ""),
      root: abs,
      problem: abs,
      summary: abs,
      status: abs,
      checklist: abs,
      spec: abs,
      nonSpec: abs,
      log: abs,
      deviations: abs,
    };
  }
  const stem = abs.replace(/\/+$/, "").replace(/^.*\//, "");
  const f = (role) => abs.replace(/\/+$/, "") + "/" + stem + "." + role + ".md";
  return {
    layout: "folder",
    stem,
    dir: abs.replace(/\/+$/, ""),
    root: abs.replace(/\/+$/, ""),
    problem: f("problem-statement"),
    summary: f("summary"),
    status: f("status"),
    checklist: f("implementation-checklist"),
    spec: f("spec-changes"),
    nonSpec: f("non-spec-changes"),
    log: f("review-log"),
    deviations: f("deviations"),
  };
}

// How a prompt names a role, so one sentence works for both layouts. On a
// folder proposal it is a file; on a legacy one it is a section of the one
// file, and saying so is the difference between an agent reading the right
// thing and an agent reading nothing.
function roleRef(P, role, sectionName) {
  return P.layout === "folder"
    ? P[role]
    : "the `" + sectionName + "` section of " + P.root;
}

// ---- Argument classification ---------------------------------------------
//
// forward: read where it is used, present in no prompt already issued.
// anchored: baked into prompts the run has issued.
// launch: controls how a run starts.
// The workflow lint holds every `input.<name>` this script reads to appearing
// here, so the classification cannot drift away from the code.
const ARG_CLASS = {
  proposalPath: "launch",
  repoRoot: "launch",
  date: "anchored",
  implementCode: "launch",
  leaseTtlHours: "forward",
  specReviewFocus: "anchored",
  acceptedDivergences: "anchored",
  plan: "anchored",
  reverifyDoneSteps: "launch",
  maxApplyRounds: "forward",
  maxAlignRepairs: "forward",
};

const SPEC_RULES =
  "Spec content rules (these take precedence over verbatim application; record every deviation they force):\n" +
  "- The spec never references source code files or implementation paths (pkg/, cmd/, charts/, sdks/, tests/, migrations/, .go or other source files). Rephrase staged text carrying such a reference into behavioral spec language, or drop the reference.\n" +
  "- The spec cross-references other spec content by section number only: §X.Y or a relative markdown link to a section anchor. Replace a line-number cross-reference in staged text with the containing section's number.\n" +
  "- Line numbers in the proposal's ANCHOR INSTRUCTIONS are location hints for you and never become spec content. Locate anchors by the quoted text and section headings; line numbers drift.\n" +
  "- A staged edit that introduces a brand-new section or subsection is appended at the end of its level, after the last existing sibling at that level, and numbered as the next ordinal. Never insert a new section or subsection between existing ones: inserting in the middle forces every following section to be renumbered and breaks existing cross-references. When a staged anchor instruction would place a new section or subsection between existing ones, append it at the end of that level instead, renumber it to the next ordinal, and record the deviation. Editing the body of an existing section in place is unaffected by this rule; it applies only to introducing a new numbered section or subsection.\n" +
  "- Apply staged prose as written otherwise; do not restyle it.\n" +
  "- These rules govern text you author from a staged block. For a mechanical edit you do not author the text: if the script's output violates one of them, that is a defect in the script or its register, so record it as a deviation and stop, rather than hand-correcting the output, which would put the tree and the register out of step.";

// The proposal's Summary orients the spec-apply agents the same way it orients the
// build agents: what changes, which decisions are closed, and the traps. An
// applier that knows a decision is closed does not relitigate it in a sub-step.
// The script cannot read the proposal: the workflow sandbox has no `require` and
// no filesystem access, so the agent reads its own Summary.
const SUMMARY_BLOCK =
  "\n\nTHE PROPOSAL'S SUMMARY. Read " +
  roleRef(P, "summary", "## Summary") +
  " before you start. It states the top-level changes, the decisions that are closed and must not be " +
  "reopened, and the traps this change has already fallen into. A proposal written before that section " +
  "existed may not have one; when it is absent, read the Problem and Decisions sections in its place.\n";
const BLANKS_BLOCK =
  "\n\nA proposal may delegate a detail with an explicit **IMPLEMENTOR'S CHOICE:** marker naming what is open " +
  "and the constraint any answer must satisfy. That is a delegation rather than an unappliable edit: make the " +
  "choice, satisfy the constraint, and record it in your result. An UNMARKED gap in a staged edit is still " +
  "unappliable and still stops the sub-step.\n";

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
        required: ["id", "targetFile", "subsection", "summary", "method"],
        properties: {
          id: { type: "string" },
          targetFile: { type: "string", description: "Path under spec/, relative to the repo root" },
          subsection: { type: "string", description: "The proposal subsection heading that stages this edit" },
          summary: { type: "string" },
          method: {
            type: "string",
            enum: ["authored", "mechanical"],
            description:
              "authored: the proposal stages the literal text to write, and an agent applies it. " +
              "mechanical: the proposal stages a script run over a register and enumerates no edit " +
              "sites, so the script applies it and an agent must not reproduce its effect by hand.",
          },
          command: {
            type: "string",
            description:
              "For method mechanical only: the exact command the proposal states, including its " +
              "dry-run form when it has one. Empty for authored edits.",
          },
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
  "Read a proposal and extract its staged changes and the findings that reference it.\n\n" +
    "The staged SPEC changes are in " + roleRef(P, "spec", "Proposed spec changes") + ".\n" +
    "The staged NON-SPEC changes are in " + roleRef(P, "nonSpec", "Proposed changes") + ".\n" +
    "The implementation sequence is in " + roleRef(P, "checklist", "## Implementation checklist") + ".\n" +
    "The status is read with `node " + repo + "/.claude/tools/proposal-status.mjs " + P.root + " --field status`," +
    " which is the ONLY supported way to read it: do not parse a Status bullet yourself.\n\n" +
    ' Read them in full and extract the staged changes and the findings that reference this proposal.\n\nYou are a read-only investigator; do not edit any file. Work in ' +
    repo +
    '.\n\nReturn:\n- approved: true when the status tool prints exactly "Approved".\n- alreadyApplied: true when the staged spec edits are ALREADY PRESENT in spec/. Check a sample of them rather than inferring it from the status: spec application is recorded per deliverable in the implementation checklist now, not as a status value, so an Approved proposal may have some, none, or all of its spec edits landed.\n- statusLine: what the status tool printed.\n- specEdits: one entry per staged change whose target file is under spec/, from the "Proposed spec changes" section: id (the subsection number, e.g. "7.1"), targetFile (the spec/ path), subsection (the heading), summary. A subsection targeting multiple spec files becomes one entry per file. Classify each entry\'s method. Use "mechanical" when the proposal stages the edit as a run of a script, pass, or generator over a register or map rather than as literal text to write, which a proposal signals by enumerating no edit sites, by naming a command, or by stating that completeness is proven by a gate rather than by review; put the exact command in command, including its dry-run form when the proposal states one. Use "authored" when the proposal stages the literal text together with an anchor for it. When one subsection stages both, split it into one mechanical entry and one authored entry. Defaulting to "authored" for an edit the proposal means a script to make is a defect: it sets an agent guessing at sites the proposal deliberately does not list.\n- nonSpecStaged: one entry per staged change whose target is outside spec/ (code, charts, docs, schemas). These are implemented in the code phase or reported, never hand-applied here.\n- findingIds: every OPEN finding in BUILD-GAPS.md whose body references this proposal by path or number (the file is large; grep for the proposal number and filename). Empty array if none.',
  { schema: PLAN, label: "plan", phase: "Plan" },
);

if (!plan.approved && !plan.alreadyApplied) {
  await releaseLease();
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

// ---- One execution sequence ----------------------------------------------
//
// Spec used to be a PHASE here: every staged edit landed, verified and
// committed before implement-proposal-build was invoked at all. That made the
// proposal's implementation checklist carry a `spec` lane the executor ignored,
// so one proposal had two orderings and two progress records that could
// disagree -- and the alignment-repair branch existed only to reconcile them
// after they had.
//
// There is one sequence now, and it is the checklist. A spec-lane step applies
// the deliverables its line names under a lease scoped to their files, verifies
// and commits, and ticks the same box a code step ticks. Progress is
// per-deliverable, in one place, and strictly more informative than the
// "Applied to spec" state it replaces -- which is why `.status.md` carries the
// four states and no fifth.
//
// The safety the phase provided is not lost, it is restated more precisely: a
// step may only depend on spec deliverables whose steps are already ticked.
// That is checkable from the checklist's own Depends-on lines and is sharper
// than a phase boundary, which said nothing about WHICH spec statement a code
// step was written against.
//
// This phase now only reports what the plan found, and the sequence runs below.

const specStaged = plan.specEdits.length;
if (specStaged === 0) {
  log("The proposal stages no spec edits");
} else {
  log(
    specStaged + " staged spec edit(s) across " + files.length +
      " file(s); they land as the checklist's spec-lane steps rather than as a phase",
  );
}

let specStatus = specStaged === 0 ? "no-spec-edits" : "sequenced";
const unappliable = [];
const deviations = [];
const appliedIds = new Set();
const applyHistory = [];

// ---- Implement code (optional) via the build subworkflow ----

if (!implementCode) {
  await releaseLease();
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

// The implement-proposal-build subworkflow IS the implement stage: it runs
// inline and brings its own phase group (Plan, Build, Verify, Review) under a
// "▸ implement-proposal-build" heading, so no redundant parent "Implement"
// phase wraps it.
log("Implementing the spec change via the implement-proposal-build subworkflow");
let build;
try {
  // Invoked by path rather than by name. A named workflow resolves to a copy
  // the runtime cached, which can be older than the file on disk: a run
  // launched six seconds after a commit to this subworkflow used the previous
  // revision, so several fixes appeared to have no effect and were debugged
  // twice. A path is read from disk at call time, so the child that runs is the
  // child that was edited.
  build = await workflow(
    { scriptPath: repo + "/.claude/workflows/implement-proposal-build.js" },
    {
      proposalPath: input.proposalPath,
      date,
      repoRoot: repo,
      reverifyDoneSteps: !!input.reverifyDoneSteps,
      acceptedDivergences: input.acceptedDivergences,
      plan: input.plan,
    },
  );
} catch (e) {
  await releaseLease();
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
        ".\n\nFor each finding: re-read it, confirm the landed implementation resolves it (the proposal's spec edits are applied and its code blast radius is implemented and green), flip the heading checkbox to [x] and the trailing marker to CLOSED, and add a one or two sentence Resolution note citing the proposal path and the implementing commit SHA(s). Do not open or re-open any finding. Then commit BUILD-GAPS.md on the current branch following the repository's commit conventions; the commit message and the Resolution note reference durable sources only (the proposal file path, the finding id, the spec section, the commit SHA), never the proposal's internal change/section/decision/pass/step labels. Return the IDs you closed.",
      { schema: CLOSE, label: "close-findings", phase: "Close findings" },
    );
  }
}

await releaseLease();
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
